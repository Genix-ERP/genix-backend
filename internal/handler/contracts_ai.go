package handler

// AI features for the Shartnomalar module:
//   - ExtractContractFromDocument: upload a contract file (PDF/DOCX/TXT),
//     get field suggestions for the create form. Suggestions only — the
//     human confirms; nothing is auto-saved.
//   - SummarizeContractFile: "Qisqacha mazmun" — a plain-Uzbek summary of
//     one uploaded file version, cached on contract_files.ai_summary.
//
// Both degrade gracefully: when no AI provider is configured the module
// keeps working manually — extract returns the stored file with
// ai_configured=false, summary returns 503.

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	aipkg "github.com/genixerp/genix-backend/internal/infrastructure/ai"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
)

// maxContractTextChars caps how much document text goes into the prompt.
const maxContractTextChars = 20000

var xmlTagPattern = regexp.MustCompile(`<[^>]+>`)

// extractDocumentText pulls plain text out of PDF / DOCX / TXT bytes.
func extractDocumentText(filename string, content []byte) (string, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".txt", ".csv":
		return string(content), nil

	case ".docx":
		zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			return "", fmt.Errorf("invalid docx: %w", err)
		}
		for _, f := range zr.File {
			if f.Name != "word/document.xml" {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			raw, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			// Paragraph/tab boundaries → whitespace, then strip tags.
			text := string(raw)
			text = strings.ReplaceAll(text, "</w:p>", "\n")
			text = strings.ReplaceAll(text, "<w:tab/>", "\t")
			text = xmlTagPattern.ReplaceAllString(text, "")
			return html.UnescapeString(text), nil
		}
		return "", fmt.Errorf("docx has no document.xml")

	case ".pdf":
		reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			return "", fmt.Errorf("invalid pdf: %w", err)
		}
		var sb strings.Builder
		textReader, err := reader.GetPlainText()
		if err != nil {
			return "", fmt.Errorf("pdf text extraction failed: %w", err)
		}
		if _, err := io.Copy(&sb, textReader); err != nil {
			return "", err
		}
		return sb.String(), nil
	}
	return "", fmt.Errorf("unsupported document format")
}

// contractAIChat sends one prompt to the tenant's AI provider. Returns
// ("", false, nil) when AI is not configured.
func (h *Handler) contractAIChat(ctx context.Context, tenantID uuid.UUID, systemPrompt, userPrompt string) (string, bool, error) {
	aiService := h.getAIService(tenantID)
	if aiService == nil {
		return "", false, nil
	}
	if err := aiService.rateLimiter.Wait(ctx); err != nil {
		return "", true, fmt.Errorf("rate limit: %w", err)
	}
	cfg := h.tenantAIConfig(tenantID)
	resp, err := aiService.client.Chat(ctx, &aipkg.ChatRequest{
		Model: cfg.Model,
		Messages: []aipkg.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:   2000,
		Temperature: 0,
	})
	if err != nil {
		return "", true, err
	}
	return resp.Message.Content, true, nil
}

// stripCodeFences removes ```json ... ``` wrappers models like to add.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}

// ExtractContractFromDocument — POST /contracts/ai/extract (multipart "file").
// Stores the file (so the create flow can attach it as version 1 without
// re-uploading) and returns AI field suggestions with confidences.
func (h *Handler) ExtractContractFromDocument(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "No file provided")
		return
	}
	defer file.Close()

	fileID, _, _, uploadErr := h.persistContractFileBytes(c, file, header.Filename, header.Size, tenantID, userID)
	if uploadErr != nil {
		return // response already written
	}

	// Re-read stored content for extraction (bytes already in DB).
	var content []byte
	if err := h.db.QueryRow(`SELECT content FROM uploaded_files WHERE id = $1`, fileID).Scan(&content); err != nil {
		response.InternalError(c, "Failed to read uploaded file")
		return
	}

	base := gin.H{
		"file_id":   fileID,
		"file_name": header.Filename,
	}

	text, extractErr := extractDocumentText(header.Filename, content)
	if extractErr != nil {
		h.log.Warn("Contract text extraction failed", "error", extractErr, "file", header.Filename)
		base["ai_configured"] = false
		base["suggestions"] = nil
		base["reason"] = "text_extraction_failed"
		response.Success(c, base)
		return
	}
	if len(text) > maxContractTextChars {
		text = text[:maxContractTextChars]
	}

	systemPrompt := `You extract structured data from contracts (often in Uzbek or Russian). ` +
		`Respond with ONLY a JSON object, no prose. Schema:
{
  "contract_number": {"value": "string|null", "confidence": 0.0},
  "title": {"value": "string|null", "confidence": 0.0},
  "counterparty_name": {"value": "string|null", "confidence": 0.0},
  "value": {"value": number|null, "confidence": 0.0},
  "currency": {"value": "UZS|USD|EUR|RUB|null", "confidence": 0.0},
  "signed_date": {"value": "YYYY-MM-DD|null", "confidence": 0.0},
  "start_date": {"value": "YYYY-MM-DD|null", "confidence": 0.0},
  "end_date": {"value": "YYYY-MM-DD|null", "confidence": 0.0},
  "subject": {"value": "string|null", "confidence": 0.0},
  "payment_terms": {"value": "string|null", "confidence": 0.0}
}
Use null when a field is absent. confidence is 0..1.`

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.config.AI.RequestTimeout)
	defer cancel()

	started := time.Now()
	raw, configured, aiErr := h.contractAIChat(ctx, tenantID, systemPrompt, "Contract text:\n\n"+text)
	if !configured {
		base["ai_configured"] = false
		base["suggestions"] = nil
		response.Success(c, base)
		return
	}
	if aiErr != nil {
		h.log.Error("Contract AI extraction failed", "error", aiErr, "tenant_id", tenantID)
		base["ai_configured"] = true
		base["suggestions"] = nil
		base["reason"] = "ai_unavailable"
		response.Success(c, base)
		return
	}

	var suggestions map[string]interface{}
	if err := json.Unmarshal([]byte(stripCodeFences(raw)), &suggestions); err != nil {
		h.log.Error("Contract AI extraction returned non-JSON", "error", err, "tenant_id", tenantID)
		base["ai_configured"] = true
		base["suggestions"] = nil
		base["reason"] = "ai_bad_response"
		response.Success(c, base)
		return
	}

	h.log.Info("Contract AI extraction", "tenant_id", tenantID, "file", header.Filename,
		"duration_ms", time.Since(started).Milliseconds())

	base["ai_configured"] = true
	base["suggestions"] = suggestions
	response.Success(c, base)
}

// SummarizeContractFile — POST /contracts/:id/files/:fileId/summary
// Generates (or returns the cached) plain-Uzbek summary for one file
// version: predmet, summa, muddatlar, jarimalar, to'lov shartlari.
func (h *Handler) SummarizeContractFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}
	cfID, err := uuid.Parse(c.Param("fileId"))
	if err != nil {
		response.BadRequest(c, "Invalid file ID")
		return
	}

	var storedID, originalName string
	var cached sql.NullString
	err = h.db.QueryRow(`
		SELECT file_id, original_name, ai_summary FROM contract_files
		WHERE id = $1 AND contract_id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, cfID, contractID, tenantID).Scan(&storedID, &originalName, &cached)
	if err == sql.ErrNoRows {
		response.NotFound(c, "File")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to summarize file")
		return
	}

	if cached.Valid && cached.String != "" {
		response.Success(c, gin.H{"summary": cached.String, "cached": true})
		return
	}

	var content []byte
	if err := h.db.QueryRow(`SELECT content FROM uploaded_files WHERE id = $1`, storedID).Scan(&content); err != nil {
		response.InternalError(c, "Failed to read file")
		return
	}

	text, extractErr := extractDocumentText(originalName, content)
	if extractErr != nil {
		response.Error(c, http.StatusUnprocessableEntity, "TEXT_EXTRACTION_FAILED",
			"Could not extract text from this file format")
		return
	}
	if len(text) > maxContractTextChars {
		text = text[:maxContractTextChars]
	}

	systemPrompt := `Siz shartnoma hujjatlarini tahlil qiluvchi yordamchisiz. Quyidagi shartnoma matnining qisqacha mazmunini SODDA O'ZBEK TILIDA yozing. Tuzilishi:
- Predmet: (shartnoma nimani nazarda tutadi)
- Summa: (qiymat va valyuta)
- Muddatlar: (boshlanish, tugash, muhim sanalar)
- To'lov shartlari: (qanday va qachon to'lanadi)
- Jarimalar: (kechikish/buzilish uchun sanksiyalar, bo'lsa)
Har bir band 1-2 jumla. Hujjatda yo'q bandni "ko'rsatilmagan" deb belgilang. Faqat matnga tayaning, taxmin qilmang.`

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.config.AI.RequestTimeout)
	defer cancel()

	summary, configured, aiErr := h.contractAIChat(ctx, tenantID, systemPrompt, text)
	if !configured {
		response.Error(c, http.StatusServiceUnavailable, "AI_NOT_CONFIGURED", "AI service is not configured")
		return
	}
	if aiErr != nil {
		h.log.Error("Contract AI summary failed", "error", aiErr, "tenant_id", tenantID)
		response.Error(c, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "AI service is temporarily unavailable")
		return
	}

	// Cache per file version.
	if _, err := h.db.Exec(`
		UPDATE contract_files SET ai_summary = $1, ai_summary_at = $2
		WHERE id = $3 AND tenant_id = $4
	`, summary, time.Now(), cfID, tenantID); err != nil {
		h.log.Error("Failed to cache contract summary", "error", err)
	}

	h.log.Info("Contract AI summary generated", "tenant_id", tenantID, "contract_id", contractID, "file", originalName)
	response.Success(c, gin.H{"summary": summary, "cached": false})
}
