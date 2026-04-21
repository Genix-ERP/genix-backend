// didox.go — adapter for didox.uz e-invoice provider.
//
// didox uses a JSON/REST API with Bearer token auth. This adapter covers the
// minimal flow needed for §8.2: authenticate, list inbox, send outgoing
// invoice, approve/reject incoming. Full API docs at https://didox.uz/docs.
//
// The adapter is intentionally a skeleton: it implements the Provider
// interface and performs real HTTP calls, but the response parsers are
// thin and will need tuning once the actual API responses are inspected
// against real didox credentials.

package einvoice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const didoxDefaultBaseURL = "https://api.didox.uz/v1"

// DidoxProvider is the didox.uz adapter.
type DidoxProvider struct {
	httpClient *http.Client
}

// NewDidoxProvider creates a default didox adapter. Pass an http.Client
// with custom timeouts / proxy settings if needed.
func NewDidoxProvider(client *http.Client) *DidoxProvider {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &DidoxProvider{httpClient: client}
}

func (p *DidoxProvider) Name() string { return "didox" }

// HealthCheck hits the /health endpoint.
func (p *DidoxProvider) HealthCheck(ctx context.Context, creds Credentials) error {
	base := creds.EndpointURL
	if base == "" {
		base = didoxDefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	if err != nil {
		return err
	}
	p.addAuth(req, creds)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("didox health: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Fetch pulls incoming invoices from the didox inbox.
// Endpoint: GET /invoices?direction=...&date_from=...&date_to=...&status=...
func (p *DidoxProvider) Fetch(ctx context.Context, creds Credentials, f ListFilter) ([]Invoice, error) {
	base := creds.EndpointURL
	if base == "" {
		base = didoxDefaultBaseURL
	}

	u := base + "/invoices?direction=" + string(f.Direction)
	if !f.DateFrom.IsZero() {
		u += "&date_from=" + f.DateFrom.Format("2006-01-02")
	}
	if !f.DateTo.IsZero() {
		u += "&date_to=" + f.DateTo.Format("2006-01-02")
	}
	if f.MaxResults > 0 {
		u += fmt.Sprintf("&limit=%d", f.MaxResults)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	p.addAuth(req, creds)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("didox fetch: HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Expected shape: {"data": [ {invoice}, ... ]}
	var payload struct {
		Data []didoxInvoiceJSON `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("didox parse: %w", err)
	}

	out := make([]Invoice, 0, len(payload.Data))
	for _, raw := range payload.Data {
		out = append(out, raw.toInvoice())
	}
	return out, nil
}

// Send uploads an outgoing invoice.
// Endpoint: POST /invoices
func (p *DidoxProvider) Send(ctx context.Context, creds Credentials, inv Invoice) (Result, error) {
	base := creds.EndpointURL
	if base == "" {
		base = didoxDefaultBaseURL
	}

	body, err := json.Marshal(didoxInvoiceJSONFrom(inv))
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/invoices", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.addAuth(req, creds)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	rawResp, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{RawResponse: string(rawResp),
			Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}, nil
	}

	var parsed struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(rawResp, &parsed)

	return Result{
		ProviderDocID: parsed.ID,
		Status:        Status(parsed.Status),
		RawResponse:   string(rawResp),
	}, nil
}

func (p *DidoxProvider) Approve(ctx context.Context, creds Credentials, providerDocID string) (Result, error) {
	return p.simpleAction(ctx, creds, providerDocID, "approve", "")
}

func (p *DidoxProvider) Reject(ctx context.Context, creds Credentials, providerDocID, reason string) (Result, error) {
	return p.simpleAction(ctx, creds, providerDocID, "reject", reason)
}

func (p *DidoxProvider) Cancel(ctx context.Context, creds Credentials, providerDocID, reason string) (Result, error) {
	return p.simpleAction(ctx, creds, providerDocID, "cancel", reason)
}

func (p *DidoxProvider) simpleAction(ctx context.Context, creds Credentials, docID, action, reason string) (Result, error) {
	base := creds.EndpointURL
	if base == "" {
		base = didoxDefaultBaseURL
	}
	url := fmt.Sprintf("%s/invoices/%s/%s", base, docID, action)

	var body []byte
	if reason != "" {
		body, _ = json.Marshal(map[string]string{"reason": reason})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.addAuth(req, creds)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	rawResp, _ := io.ReadAll(resp.Body)

	res := Result{
		ProviderDocID: docID,
		RawResponse:   string(rawResp),
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	} else {
		switch action {
		case "approve":
			res.Status = StatusApproved
		case "reject":
			res.Status = StatusRejected
		case "cancel":
			res.Status = StatusCancelled
		}
	}
	return res, nil
}

func (p *DidoxProvider) addAuth(req *http.Request, creds Credentials) {
	if creds.Password != "" {
		req.Header.Set("Authorization", "Bearer "+creds.Password)
	}
	if creds.Login != "" {
		req.Header.Set("X-User-Login", creds.Login)
	}
}

// didoxInvoiceJSON matches the didox REST JSON shape (approximate; adjust as
// real API responses are inspected).
type didoxInvoiceJSON struct {
	ID             string  `json:"id"`
	FactureType    string  `json:"facture_type"`
	DocumentNumber string  `json:"document_number"`
	DocumentDate   string  `json:"document_date"` // YYYY-MM-DD
	SellerTIN      string  `json:"seller_tin"`
	SellerName     string  `json:"seller_name"`
	BuyerTIN       string  `json:"buyer_tin"`
	BuyerName      string  `json:"buyer_name"`
	TotalAmount    float64 `json:"total_amount"`
	TaxAmount      float64 `json:"tax_amount"`
	TotalWithTax   float64 `json:"total_with_tax"`
	Currency       string  `json:"currency"`
	Lines          []struct {
		LineNumber  int     `json:"line_number"`
		ProductCode string  `json:"product_code"`
		Description string  `json:"description"`
		Quantity    float64 `json:"quantity"`
		Unit        string  `json:"unit"`
		UnitPrice   float64 `json:"unit_price"`
		LineAmount  float64 `json:"line_amount"`
		TaxRate     float64 `json:"tax_rate"`
		TaxAmount   float64 `json:"tax_amount"`
	} `json:"lines"`
}

func (d didoxInvoiceJSON) toInvoice() Invoice {
	t, _ := time.Parse("2006-01-02", d.DocumentDate)
	inv := Invoice{
		ProviderDocID:  d.ID,
		FactureType:    d.FactureType,
		DocumentNumber: d.DocumentNumber,
		DocumentDate:   t,
		SellerTIN:      d.SellerTIN,
		SellerName:     d.SellerName,
		BuyerTIN:       d.BuyerTIN,
		BuyerName:      d.BuyerName,
		TotalAmount:    d.TotalAmount,
		TaxAmount:      d.TaxAmount,
		TotalWithTax:   d.TotalWithTax,
		Currency:       d.Currency,
	}
	for _, l := range d.Lines {
		inv.Lines = append(inv.Lines, InvoiceLine{
			LineNumber: l.LineNumber, ProductCode: l.ProductCode, Description: l.Description,
			Quantity: l.Quantity, Unit: l.Unit, UnitPrice: l.UnitPrice,
			LineAmount: l.LineAmount, TaxRate: l.TaxRate, TaxAmount: l.TaxAmount,
		})
	}
	return inv
}

func didoxInvoiceJSONFrom(inv Invoice) didoxInvoiceJSON {
	out := didoxInvoiceJSON{
		ID:             inv.ProviderDocID,
		FactureType:    inv.FactureType,
		DocumentNumber: inv.DocumentNumber,
		DocumentDate:   inv.DocumentDate.Format("2006-01-02"),
		SellerTIN:      inv.SellerTIN, SellerName: inv.SellerName,
		BuyerTIN: inv.BuyerTIN, BuyerName: inv.BuyerName,
		TotalAmount: inv.TotalAmount, TaxAmount: inv.TaxAmount,
		TotalWithTax: inv.TotalWithTax, Currency: inv.Currency,
	}
	for _, l := range inv.Lines {
		out.Lines = append(out.Lines, struct {
			LineNumber  int     `json:"line_number"`
			ProductCode string  `json:"product_code"`
			Description string  `json:"description"`
			Quantity    float64 `json:"quantity"`
			Unit        string  `json:"unit"`
			UnitPrice   float64 `json:"unit_price"`
			LineAmount  float64 `json:"line_amount"`
			TaxRate     float64 `json:"tax_rate"`
			TaxAmount   float64 `json:"tax_amount"`
		}{
			LineNumber: l.LineNumber, ProductCode: l.ProductCode, Description: l.Description,
			Quantity: l.Quantity, Unit: l.Unit, UnitPrice: l.UnitPrice,
			LineAmount: l.LineAmount, TaxRate: l.TaxRate, TaxAmount: l.TaxAmount,
		})
	}
	return out
}
