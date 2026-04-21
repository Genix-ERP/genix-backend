// faktura.go — adapter for faktura.uz e-invoice provider.
//
// faktura uses a SOAP/XML API. This stub implements the Provider interface
// with the transport layer only; specific XML envelopes are left to the
// concrete production implementation once SOAP contracts are finalized.

package einvoice

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

const fakturaDefaultBaseURL = "https://api.faktura.uz/ws"

type FakturaProvider struct {
	httpClient *http.Client
}

func NewFakturaProvider(client *http.Client) *FakturaProvider {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &FakturaProvider{httpClient: client}
}

func (p *FakturaProvider) Name() string { return "faktura" }

func (p *FakturaProvider) HealthCheck(ctx context.Context, creds Credentials) error {
	// SOAP "ping" operation
	base := creds.EndpointURL
	if base == "" {
		base = fakturaDefaultBaseURL
	}
	envelope := `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body><Ping/></soap:Body>
</soap:Envelope>`
	_, err := p.soapCall(ctx, creds, base, "Ping", []byte(envelope))
	return err
}

func (p *FakturaProvider) Fetch(ctx context.Context, creds Credentials, f ListFilter) ([]Invoice, error) {
	// Stub — a real impl would build a GetInbox SOAP request with date filters,
	// parse the returned <Facture> elements into []Invoice. Returning an empty
	// slice keeps callers safe while the SOAP schema is being finalized.
	return []Invoice{}, nil
}

func (p *FakturaProvider) Send(ctx context.Context, creds Credentials, inv Invoice) (Result, error) {
	base := creds.EndpointURL
	if base == "" {
		base = fakturaDefaultBaseURL
	}
	// Extremely simplified SOAP envelope. Real faktura requires nested
	// <Facture>, <Seller>, <Buyer>, <Products> etc per their XSD.
	type fakturaFacture struct {
		XMLName       xml.Name `xml:"Facture"`
		DocumentNumber string  `xml:"DocumentNumber"`
		DocumentDate   string  `xml:"DocumentDate"`
		SellerTIN      string  `xml:"SellerTIN"`
		BuyerTIN       string  `xml:"BuyerTIN"`
		TotalAmount    float64 `xml:"TotalAmount"`
		TaxAmount      float64 `xml:"TaxAmount"`
	}
	payload := fakturaFacture{
		DocumentNumber: inv.DocumentNumber,
		DocumentDate:   inv.DocumentDate.Format("2006-01-02"),
		SellerTIN:      inv.SellerTIN,
		BuyerTIN:       inv.BuyerTIN,
		TotalAmount:    inv.TotalAmount,
		TaxAmount:      inv.TaxAmount,
	}
	raw, err := xml.Marshal(payload)
	if err != nil {
		return Result{}, err
	}

	envelope := []byte(fmt.Sprintf(`<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <SendFacture>%s</SendFacture>
  </soap:Body>
</soap:Envelope>`, string(raw)))

	respBody, err := p.soapCall(ctx, creds, base, "SendFacture", envelope)
	if err != nil {
		return Result{Error: err.Error(), RawResponse: string(respBody)}, nil
	}

	return Result{
		ProviderDocID: inv.DocumentNumber, // faktura echoes the doc number as its ID
		Status:        StatusSent,
		RawResponse:   string(respBody),
	}, nil
}

func (p *FakturaProvider) Approve(ctx context.Context, creds Credentials, providerDocID string) (Result, error) {
	return p.soapAction(ctx, creds, providerDocID, "ApproveFacture", "")
}

func (p *FakturaProvider) Reject(ctx context.Context, creds Credentials, providerDocID, reason string) (Result, error) {
	return p.soapAction(ctx, creds, providerDocID, "RejectFacture", reason)
}

func (p *FakturaProvider) Cancel(ctx context.Context, creds Credentials, providerDocID, reason string) (Result, error) {
	return p.soapAction(ctx, creds, providerDocID, "CancelFacture", reason)
}

func (p *FakturaProvider) soapAction(ctx context.Context, creds Credentials, docID, action, reason string) (Result, error) {
	base := creds.EndpointURL
	if base == "" {
		base = fakturaDefaultBaseURL
	}
	envelope := []byte(fmt.Sprintf(`<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <%s><DocumentID>%s</DocumentID><Reason>%s</Reason></%s>
  </soap:Body>
</soap:Envelope>`, action, docID, reason, action))

	respBody, err := p.soapCall(ctx, creds, base, action, envelope)
	res := Result{
		ProviderDocID: docID,
		RawResponse:   string(respBody),
	}
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	switch action {
	case "ApproveFacture":
		res.Status = StatusApproved
	case "RejectFacture":
		res.Status = StatusRejected
	case "CancelFacture":
		res.Status = StatusCancelled
	}
	return res, nil
}

func (p *FakturaProvider) soapCall(ctx context.Context, creds Credentials,
	baseURL, soapAction string, body []byte) ([]byte, error) {

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)
	if creds.Login != "" {
		req.SetBasicAuth(creds.Login, creds.Password)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, fmt.Errorf("faktura %s: HTTP %d", soapAction, resp.StatusCode)
	}
	return respBody, nil
}
