// Package einvoice defines the provider-agnostic interface for electronic
// hisob-faktura (e-invoice) exchange in Uzbekistan. Concrete adapters wrap
// didox.uz (JSON/REST), faktura (SOAP/XML), and soliq.uz API.
//
// TT Buxgalteriya ERP §8.2.
package einvoice

import (
	"context"
	"time"
)

// Direction indicates whether an invoice is incoming (from our supplier) or
// outgoing (to our customer).
type Direction string

const (
	DirectionIncoming Direction = "incoming"
	DirectionOutgoing Direction = "outgoing"
)

// Status mirrors the `einvoices.status` DB enum.
type Status string

const (
	StatusReceived        Status = "received"
	StatusPendingApproval Status = "pending_approval"
	StatusApproved        Status = "approved"
	StatusRejected        Status = "rejected"
	StatusSent            Status = "sent"
	StatusConfirmed       Status = "confirmed"
	StatusCancelled       Status = "cancelled"
)

// Credentials is the authentication material for a provider, pulled from
// einvoice_provider_credentials at runtime.
type Credentials struct {
	EndpointURL   string
	Login         string
	Password      string // decrypted at call time
	CertificateID string
}

// InvoiceLine represents a line item inside an e-invoice envelope.
type InvoiceLine struct {
	LineNumber  int
	ProductCode string
	Description string
	Quantity    float64
	Unit        string
	UnitPrice   float64
	LineAmount  float64
	TaxRate     float64
	TaxAmount   float64
}

// Invoice is the provider-agnostic invoice envelope.
type Invoice struct {
	ProviderDocID  string
	FactureType    string // standard | adjustment | correction
	DocumentNumber string
	DocumentDate   time.Time

	SellerTIN, SellerName string
	BuyerTIN, BuyerName   string

	TotalAmount  float64
	TaxAmount    float64
	TotalWithTax float64
	Currency     string

	Lines  []InvoiceLine
	RawXML string
}

// ListFilter narrows a Fetch call.
type ListFilter struct {
	Direction  Direction
	DateFrom   time.Time
	DateTo     time.Time
	Statuses   []Status
	MaxResults int
}

// Result of Send / Approve / Cancel remote calls.
type Result struct {
	ProviderDocID string
	Status        Status
	RawResponse   string
	Error         string
}

// Provider is the surface every e-invoice integration implements.
type Provider interface {
	// Name returns the canonical provider key ("didox", "faktura", "soliq").
	Name() string

	// HealthCheck pings the endpoint; returns nil on success.
	HealthCheck(ctx context.Context, creds Credentials) error

	// Fetch pulls incoming invoices from the provider's inbox matching the filter.
	// Adapters should page transparently and return a flat list.
	Fetch(ctx context.Context, creds Credentials, f ListFilter) ([]Invoice, error)

	// Send uploads an outgoing invoice to the provider. Returns the provider's
	// assigned doc ID and initial status.
	Send(ctx context.Context, creds Credentials, inv Invoice) (Result, error)

	// Approve acknowledges an incoming invoice we've decided to accept.
	// Some providers require an explicit "confirm" step separate from local approval.
	Approve(ctx context.Context, creds Credentials, providerDocID string) (Result, error)

	// Reject rejects an incoming invoice with a reason.
	Reject(ctx context.Context, creds Credentials, providerDocID, reason string) (Result, error)

	// Cancel voids an outgoing invoice we previously sent.
	Cancel(ctx context.Context, creds Credentials, providerDocID, reason string) (Result, error)
}

// Registry lets the handler layer look up a provider by name.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register attaches an implementation under its canonical name.
func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

// Get returns the provider by name, or nil if unregistered.
func (r *Registry) Get(name string) Provider {
	return r.providers[name]
}

// Names lists all registered provider names for diagnostics.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for n := range r.providers {
		out = append(out, n)
	}
	return out
}
