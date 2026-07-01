package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ReceiptScanProduct is a product matched from the receipt against the catalog
type ReceiptScanProduct struct {
	ProductID   string  `json:"product_id"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

// ReceiptScanResponse is returned after scanning a receipt image
type ReceiptScanResponse struct {
	Matched   []ReceiptScanProduct `json:"matched"`
	Unmatched []string             `json:"unmatched"`
}

type receiptLineItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

// ScanPurchaseReceipt accepts a receipt/check image, extracts products using AI,
// matches them against the tenant's product catalog, and returns matched/unmatched lists.
func (h *Handler) ScanPurchaseReceipt(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant required")
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "File required (multipart field: file)")
		return
	}
	defer file.Close()

	// Limit: 10 MB
	if header.Size > 10*1024*1024 {
		response.BadRequest(c, "File too large (max 10 MB)")
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		response.InternalError(c, "Failed to read file")
		return
	}

	// Determine MIME type
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	// Normalise e.g. "image/jpeg; charset=..." → "image/jpeg"
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}

	allowed := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
	}
	if !allowed[mimeType] {
		response.BadRequest(c, "Unsupported file type. Use JPEG, PNG or WebP")
		return
	}

	imageBase64 := base64.StdEncoding.EncodeToString(data)

	// Extract items from image using AI
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()

	items, err := h.extractReceiptLineItems(ctx, tenantID, imageBase64, mimeType)
	if err != nil {
		h.log.Warn("Receipt AI extraction failed", "error", err)
		// Return empty result rather than a hard error so UI degrades gracefully
		response.Success(c, ReceiptScanResponse{
			Matched:   []ReceiptScanProduct{},
			Unmatched: []string{},
		})
		return
	}

	var matched []ReceiptScanProduct
	var unmatched []string

	for _, item := range items {
		if strings.TrimSpace(item.Description) == "" {
			continue
		}

		var productID uuid.UUID
		var productName string
		var costPrice float64

		// Try exact match first, then fuzzy ILIKE
		scanErr := h.db.QueryRowContext(ctx, `
			SELECT id, name, COALESCE(cost_price, 0)
			FROM products
			WHERE tenant_id = $1
			  AND deleted_at IS NULL
			  AND is_active = true
			  AND LOWER(TRIM(name)) = LOWER(TRIM($2))
			LIMIT 1
		`, tenantID, item.Description).Scan(&productID, &productName, &costPrice)

		if scanErr == sql.ErrNoRows {
			// Fuzzy fallback: any word in description appears in product name
			scanErr = h.db.QueryRowContext(ctx, `
				SELECT id, name, COALESCE(cost_price, 0)
				FROM products
				WHERE tenant_id = $1
				  AND deleted_at IS NULL
				  AND is_active = true
				  AND name ILIKE $2
				ORDER BY LENGTH(name) ASC
				LIMIT 1
			`, tenantID, "%"+strings.TrimSpace(item.Description)+"%").Scan(&productID, &productName, &costPrice)
		}

		if scanErr == nil {
			unitPrice := item.UnitPrice
			if unitPrice == 0 {
				unitPrice = costPrice
			}
			qty := item.Quantity
			if qty <= 0 {
				qty = 1
			}
			matched = append(matched, ReceiptScanProduct{
				ProductID:   productID.String(),
				ProductName: productName,
				Quantity:    qty,
				UnitPrice:   unitPrice,
			})
		} else {
			unmatched = append(unmatched, item.Description)
		}
	}

	if matched == nil {
		matched = []ReceiptScanProduct{}
	}
	if unmatched == nil {
		unmatched = []string{}
	}

	response.Success(c, ReceiptScanResponse{
		Matched:   matched,
		Unmatched: unmatched,
	})
}

// receiptScanPrompt is the shared instruction sent to whichever vision model.
const receiptScanPrompt = `You are a receipt/invoice scanner. Extract ALL product line items from this receipt or check image.

Return ONLY a valid JSON array with no extra text:
[
  {"description": "exact product name as written on receipt", "quantity": 1, "unit_price": 0.00},
  ...
]

Rules:
- description: the product/item name exactly as printed
- quantity: numeric quantity ordered (use 1 if not clearly shown)
- unit_price: price per single unit in the receipt currency (use 0 if not shown)
- Include every line item you can read
- Return ONLY the JSON array, no explanation`

// extractReceiptLineItems calls the configured AI provider (OpenAI or Anthropic)
// with the receipt image and returns parsed line items. Provider/key/model come
// from the tenant's own AI settings when set (see resolveTenantAIConfig), and
// otherwise fall back to the server-wide env config. If the provider is unset it
// is inferred from the endpoint. This lets the feature work with either an
// OpenAI (gpt-4o vision) or an Anthropic (Claude) key without code changes.
func (h *Handler) extractReceiptLineItems(ctx context.Context, tenantID uuid.UUID, imageBase64, mimeType string) ([]receiptLineItem, error) {
	provider, apiKey, model, endpoint := h.resolveTenantAIConfig(tenantID)
	if apiKey == "" {
		return nil, fmt.Errorf("AI API key not configured")
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	endpoint = strings.TrimSpace(endpoint)
	if provider == "" {
		// Infer from the endpoint; default to OpenAI (also covers OpenAI-compatible).
		if strings.Contains(endpoint, "anthropic") {
			provider = "anthropic"
		} else {
			provider = "openai"
		}
	}

	if provider == "anthropic" {
		return h.extractReceiptViaAnthropic(ctx, apiKey, endpoint, model, imageBase64, mimeType)
	}
	return h.extractReceiptViaOpenAI(ctx, apiKey, endpoint, model, imageBase64, mimeType)
}

// postReceiptAI sends the prepared request, enforces a timeout, and returns the
// raw body or an error (non-200 included).
func postReceiptAI(req *http.Request) ([]byte, error) {
	httpClient := &http.Client{Timeout: 35 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// parseReceiptItemsJSON pulls the first JSON array out of a model text response.
func parseReceiptItemsJSON(text string) ([]receiptLineItem, error) {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in AI response")
	}
	var items []receiptLineItem
	if err := json.Unmarshal([]byte(text[start:end+1]), &items); err != nil {
		return nil, fmt.Errorf("parse items JSON: %w", err)
	}
	return items, nil
}

// extractReceiptViaAnthropic uses Claude's vision messages API.
func (h *Handler) extractReceiptViaAnthropic(ctx context.Context, apiKey, endpoint, model, imageBase64, mimeType string) ([]receiptLineItem, error) {
	model = strings.TrimSpace(model)
	if !strings.HasPrefix(model, "claude") {
		// config default ("gpt-4") isn't a Claude id — use a valid vision model.
		model = "claude-opus-4-8"
	}
	if endpoint == "" || !strings.Contains(endpoint, "anthropic") {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 1024,
		"messages": []map[string]interface{}{
			{"role": "user", "content": []map[string]interface{}{
				{"type": "image", "source": map[string]interface{}{"type": "base64", "media_type": mimeType, "data": imageBase64}},
				{"type": "text", "text": receiptScanPrompt},
			}},
		},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	respBody, err := postReceiptAI(req)
	if err != nil {
		return nil, err
	}
	var apiResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(apiResp.Content) == 0 {
		return nil, fmt.Errorf("empty AI response")
	}
	return parseReceiptItemsJSON(apiResp.Content[0].Text)
}

// extractReceiptViaOpenAI uses OpenAI's (or an OpenAI-compatible) chat
// completions API with a vision-capable model.
func (h *Handler) extractReceiptViaOpenAI(ctx context.Context, apiKey, endpoint, model, imageBase64, mimeType string) ([]receiptLineItem, error) {
	model = strings.TrimSpace(model)
	// The config default "gpt-4" is text-only; vision needs a 4o/4.1-class model.
	if model == "" || model == "gpt-4" || model == "gpt-3.5-turbo" || strings.HasPrefix(model, "claude") {
		model = "gpt-4o"
	}
	if endpoint == "" || strings.Contains(endpoint, "anthropic") {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}
	dataURL := "data:" + mimeType + ";base64," + imageBase64
	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 1024,
		"messages": []map[string]interface{}{
			{"role": "user", "content": []map[string]interface{}{
				{"type": "text", "text": receiptScanPrompt},
				{"type": "image_url", "image_url": map[string]interface{}{"url": dataURL}},
			}},
		},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	respBody, err := postReceiptAI(req)
	if err != nil {
		return nil, err
	}
	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("empty AI response")
	}
	return parseReceiptItemsJSON(apiResp.Choices[0].Message.Content)
}
