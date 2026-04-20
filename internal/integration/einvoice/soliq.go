// soliq.go — adapter for soliq.uz (State Tax Committee) e-invoice endpoints.
//
// soliq.uz uses OAuth2 + REST for modern integrations. This stub covers the
// Provider interface with the HTTP skeleton in place; specific endpoint paths
// are tuned once the production API spec is wired up.

package einvoice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const soliqDefaultBaseURL = "https://api.soliq.uz/v1"

type SoliqProvider struct {
	httpClient *http.Client
}

func NewSoliqProvider(client *http.Client) *SoliqProvider {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &SoliqProvider{httpClient: client}
}

func (p *SoliqProvider) Name() string { return "soliq" }

func (p *SoliqProvider) HealthCheck(ctx context.Context, creds Credentials) error {
	base := creds.EndpointURL
	if base == "" {
		base = soliqDefaultBaseURL
	}
	token, err := p.getToken(ctx, creds)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("soliq health: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *SoliqProvider) Fetch(ctx context.Context, creds Credentials, f ListFilter) ([]Invoice, error) {
	return []Invoice{}, nil // stub
}

func (p *SoliqProvider) Send(ctx context.Context, creds Credentials, inv Invoice) (Result, error) {
	token, err := p.getToken(ctx, creds)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	base := creds.EndpointURL
	if base == "" {
		base = soliqDefaultBaseURL
	}

	body, _ := json.Marshal(map[string]interface{}{
		"document_number": inv.DocumentNumber,
		"document_date":   inv.DocumentDate.Format("2006-01-02"),
		"seller_tin":      inv.SellerTIN,
		"buyer_tin":       inv.BuyerTIN,
		"total_amount":    inv.TotalAmount,
		"tax_amount":      inv.TaxAmount,
		"currency":        inv.Currency,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/invoices", bytes.NewReader(body))
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{Error: fmt.Sprintf("HTTP %d", resp.StatusCode), RawResponse: string(raw)}, nil
	}

	var parsed struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &parsed)
	return Result{
		ProviderDocID: parsed.ID,
		Status:        StatusSent,
		RawResponse:   string(raw),
	}, nil
}

func (p *SoliqProvider) Approve(ctx context.Context, creds Credentials, providerDocID string) (Result, error) {
	return p.action(ctx, creds, providerDocID, "approve", "")
}
func (p *SoliqProvider) Reject(ctx context.Context, creds Credentials, providerDocID, reason string) (Result, error) {
	return p.action(ctx, creds, providerDocID, "reject", reason)
}
func (p *SoliqProvider) Cancel(ctx context.Context, creds Credentials, providerDocID, reason string) (Result, error) {
	return p.action(ctx, creds, providerDocID, "cancel", reason)
}

func (p *SoliqProvider) action(ctx context.Context, creds Credentials, docID, action, reason string) (Result, error) {
	token, err := p.getToken(ctx, creds)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	base := creds.EndpointURL
	if base == "" {
		base = soliqDefaultBaseURL
	}

	var body []byte
	if reason != "" {
		body, _ = json.Marshal(map[string]string{"reason": reason})
	}
	url := fmt.Sprintf("%s/invoices/%s/%s", base, docID, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	res := Result{ProviderDocID: docID, RawResponse: string(raw)}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return res, nil
	}
	switch action {
	case "approve":
		res.Status = StatusApproved
	case "reject":
		res.Status = StatusRejected
	case "cancel":
		res.Status = StatusCancelled
	}
	return res, nil
}

// getToken performs an OAuth2 client_credentials grant.
// creds.Login = client_id, creds.Password = client_secret.
func (p *SoliqProvider) getToken(ctx context.Context, creds Credentials) (string, error) {
	base := creds.EndpointURL
	if base == "" {
		base = soliqDefaultBaseURL
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", creds.Login)
	form.Set("client_secret", creds.Password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("soliq token: HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return "", err
	}
	return t.AccessToken, nil
}
