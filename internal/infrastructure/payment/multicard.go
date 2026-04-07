package payment

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Client is a Multicard payment gateway client.
type Client struct {
	applicationID string
	secret        string
	storeID       int
	baseURL       string
	httpClient    *http.Client

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

// NewClient creates a new Multicard client.
func NewClient(applicationID, secret string, storeID int, baseURL string) *Client {
	return &Client{
		applicationID: applicationID,
		secret:        secret,
		storeID:       storeID,
		baseURL:       baseURL,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ----- Auth -----

type authRequest struct {
	ApplicationID string `json:"application_id"`
	Secret        string `json:"secret"`
}

type authResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
	Role    string `json:"role"`
	Expiry  string `json:"expiry"` // "2023-03-18 16:40:31"
}

// token returns a valid bearer token, fetching a new one if needed.
func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	body, _ := json.Marshal(authRequest{ApplicationID: c.applicationID, Secret: c.secret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	rawBody, _ := io.ReadAll(resp.Body)
	var ar authResponse
	if err := json.Unmarshal(rawBody, &ar); err != nil {
		return "", fmt.Errorf("multicard auth decode error: %w", err)
	}
	if ar.Token == "" {
		return "", fmt.Errorf("multicard auth failed: status=%d body=%s", resp.StatusCode, string(rawBody))
	}

	// Token is valid for 24 hours; cache for 23 to be safe
	c.cachedToken = ar.Token
	c.tokenExpiry = time.Now().Add(23 * time.Hour)
	return c.cachedToken, nil
}

// ----- Invoice -----

// InvoiceRequest is the payload for creating a Multicard invoice.
type InvoiceRequest struct {
	StoreID       int    `json:"store_id"`
	Amount        int64  `json:"amount"`        // in tiyin (UZS * 100)
	InvoiceID     string `json:"invoice_id"`    // our internal order ID
	ReturnURL     string `json:"return_url"`    // redirect on success
	ReturnErrURL  string `json:"return_error_url"` // redirect on failure
	CallbackURL   string `json:"callback_url"`  // our webhook
}

// InvoiceResponse is returned by Multicard after invoice creation.
type InvoiceResponse struct {
	UUID        string `json:"uuid"`
	CheckoutURL string `json:"checkout_url"`
	ShortLink   string `json:"short_link"`
}

// CreateInvoice creates a payment invoice and returns the checkout URL.
func (c *Client) CreateInvoice(ctx context.Context, req InvoiceRequest) (*InvoiceResponse, error) {
	token, err := c.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	req.StoreID = c.storeID
	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/payment/invoice", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			UUID        string `json:"uuid"`
			CheckoutURL string `json:"checkout_url"`
			ShortLink   string `json:"short_link"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Details string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode invoice response: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("multicard invoice error %s: %s", result.Error.Code, result.Error.Details)
	}

	return &InvoiceResponse{
		UUID:        result.Data.UUID,
		CheckoutURL: result.Data.CheckoutURL,
		ShortLink:   result.Data.ShortLink,
	}, nil
}

// ----- Webhook -----

// WebhookPayload is the callback body sent by Multicard after payment.
type WebhookPayload struct {
	UUID        string  `json:"uuid"`
	Amount      int64   `json:"amount"`
	InvoiceID   string  `json:"invoice_id"`
	Status      string  `json:"status"` // draft, progress, success, error, revert, hold
	BillingID   string  `json:"billing_id"`
	PaymentTime string  `json:"payment_time"`
	RefundTime  *string `json:"refund_time"`
	Phone       string  `json:"phone"`
	CardPan     string  `json:"card_pan"`
	PS          string  `json:"ps"`
	CardToken   string  `json:"card_token"`
	ReceiptURL  *string `json:"receipt_url"`
	Sign        string  `json:"sign"`
}

// ----- Payment Status -----

// PaymentStatus is returned when querying an invoice's current state.
type PaymentStatus struct {
	UUID        string `json:"uuid"`
	InvoiceID   string `json:"invoice_id"`
	Amount      int64  `json:"amount"`
	Status      string `json:"status"`
	BillingID   string `json:"billing_id"`
	PaymentTime string `json:"payment_time"`
	Phone       string `json:"phone"`
	CardPan     string `json:"card_pan"`
	PS          string `json:"ps"`
	CardToken   string `json:"card_token"`
}

// GetPaymentStatus queries Multicard for the current status of an invoice by UUID.
func (c *Client) GetPaymentStatus(ctx context.Context, uuid string) (*PaymentStatus, error) {
	token, err := c.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/payment/invoice/%s", c.baseURL, uuid), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result struct {
		Success bool          `json:"success"`
		Data    PaymentStatus `json:"data"`
		Error   struct {
			Code    string `json:"code"`
			Details string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode payment status: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("multicard status error %s: %s", result.Error.Code, result.Error.Details)
	}
	return &result.Data, nil
}

// VerifySign checks the MD5 signature from the webhook payload.
// sign = MD5( uuid + invoice_id + amount + secret )
func (c *Client) VerifySign(p WebhookPayload) bool {
	raw := fmt.Sprintf("%s%s%d%s", p.UUID, p.InvoiceID, p.Amount, c.secret)
	h := md5.New()
	h.Write([]byte(raw))
	expected := fmt.Sprintf("%x", h.Sum(nil))
	return expected == p.Sign
}
