package handler

// File: buxgalteriya_einvoice_sync.go
//
// Bridges the e-invoice domain layer (buxgalteriya_einvoice.go) to the
// provider adapters (internal/integration/einvoice/*).
//
// Exposes two endpoints:
//   POST /finance/einvoices/sync     — fetch incoming invoices from the provider
//                                       and save them via the same ingest path
//                                       used by webhook callbacks.
//   POST /finance/einvoices/:id/send — push an outgoing invoice to the provider.

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	provider "github.com/genixerp/genix-backend/internal/integration/einvoice"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// Process-wide singleton registry; populated on first use.
var (
	einvoiceRegistryOnce sync.Once
	einvoiceRegistry     *provider.Registry
)

// einvoiceRegistryRef returns the lazily-initialised registry.
// Providers are registered here so callers don't need to know the concrete types.
func einvoiceRegistryRef() *provider.Registry {
	einvoiceRegistryOnce.Do(func() {
		einvoiceRegistry = provider.NewRegistry()
		einvoiceRegistry.Register(provider.NewDidoxProvider(&http.Client{Timeout: 30 * time.Second}))
		einvoiceRegistry.Register(provider.NewFakturaProvider(&http.Client{Timeout: 45 * time.Second}))
		einvoiceRegistry.Register(provider.NewSoliqProvider(&http.Client{Timeout: 45 * time.Second}))
	})
	return einvoiceRegistry
}

// loadCredentials pulls the (decrypted) provider credentials for the tenant.
// For now, password is stored encrypted; here we just read it as-is. A future
// refactor will plug tenant KMS / envelope decryption in.
func (h *Handler) loadCredentials(tenantID uuid.UUID, orgID *uuid.UUID, providerName string) (provider.Credentials, error) {
	q := `
		SELECT endpoint_url, login, encrypted_password, certificate_id
		FROM einvoice_provider_credentials
		WHERE tenant_id = $1 AND provider = $2 AND is_active = true`
	args := []interface{}{tenantID, providerName}
	if orgID != nil && *orgID != uuid.Nil {
		q += " AND (organization_id = $3 OR organization_id IS NULL) ORDER BY (organization_id IS NULL) ASC"
		args = append(args, *orgID)
	}
	q += " LIMIT 1"

	var endpoint, login, password, certID sql.NullString
	err := h.db.QueryRow(q, args...).Scan(&endpoint, &login, &password, &certID)
	if err != nil {
		return provider.Credentials{}, err
	}
	return provider.Credentials{
		EndpointURL:   endpoint.String,
		Login:         login.String,
		Password:      password.String, // TODO(security): decrypt via KMS
		CertificateID: certID.String,
	}, nil
}

// SyncEInvoices pulls incoming invoices from the configured provider and
// ingests each one through the same path as manual POST /einvoices/ingest.
// Body:
//   { "provider": "didox", "days_back": 7 }
func (h *Handler) SyncEInvoices(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var body struct {
		Provider string `json:"provider" binding:"required"`
		DaysBack int    `json:"days_back"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid body")
		return
	}
	if body.DaysBack <= 0 {
		body.DaysBack = 7
	}

	reg := einvoiceRegistryRef()
	prov := reg.Get(body.Provider)
	if prov == nil {
		response.BadRequest(c, "Unknown provider: "+body.Provider+
			" (available: "+joinStrings(reg.Names(), ", ")+")")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}
	creds, err := h.loadCredentials(tenantID, orgPtr, body.Provider)
	if err == sql.ErrNoRows {
		response.BadRequest(c, "No active credentials configured for provider "+body.Provider)
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load credentials")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	invoices, err := prov.Fetch(ctx, creds, provider.ListFilter{
		Direction:  provider.DirectionIncoming,
		DateFrom:   time.Now().AddDate(0, 0, -body.DaysBack),
		DateTo:     time.Now(),
		MaxResults: 500,
	})
	if err != nil {
		response.InternalError(c, "Fetch failed: "+err.Error())
		return
	}

	// Ingest each fetched invoice. Use the same INSERT pattern as the manual
	// ingest endpoint so workflow semantics stay identical.
	now := time.Now()
	ingested := 0
	duplicates := 0
	for _, inv := range invoices {
		id := uuid.New()
		res, ierr := h.db.Exec(`
			INSERT INTO einvoices (
				id, tenant_id, organization_id, provider, direction,
				provider_doc_id, facture_type, document_number, document_date,
				seller_tin, seller_name, buyer_tin, buyer_name,
				total_amount, tax_amount, total_with_tax, currency,
				status, received_at, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, 'incoming',
				$5, $6, $7, $8,
				$9, $10, $11, $12,
				$13, $14, $15, $16,
				'received', $17, $17, $17
			)
			ON CONFLICT (tenant_id, provider, provider_doc_id)
			WHERE provider_doc_id IS NOT NULL
			DO NOTHING
		`, id, tenantID, orgPtr, body.Provider,
			inv.ProviderDocID, inv.FactureType, inv.DocumentNumber, nullableDate(inv.DocumentDate),
			inv.SellerTIN, inv.SellerName, inv.BuyerTIN, inv.BuyerName,
			inv.TotalAmount, inv.TaxAmount, inv.TotalWithTax, inv.Currency,
			now)
		if ierr != nil {
			h.log.Error("SyncEInvoices: header insert failed", "error", ierr)
			continue
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			duplicates++
			continue
		}
		ingested++

		// Insert lines
		for i, l := range inv.Lines {
			_, _ = h.db.Exec(`
				INSERT INTO einvoice_lines (
					id, einvoice_id, line_number, product_code, description,
					quantity, unit, unit_price, line_amount, tax_rate, tax_amount, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			`, uuid.New(), id, i+1, l.ProductCode, l.Description,
				l.Quantity, l.Unit, l.UnitPrice, l.LineAmount, l.TaxRate, l.TaxAmount, now)
		}
	}

	response.Success(c, gin.H{
		"provider":      body.Provider,
		"fetched":       len(invoices),
		"ingested":      ingested,
		"duplicates":    duplicates,
		"days_back":     body.DaysBack,
	})
}

// SendEInvoice pushes an outgoing invoice to the configured provider.
// The e-invoice record must already exist in state 'approved' (or similar)
// with direction='outgoing'.
func (h *Handler) SendEInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}

	// Load the local invoice.
	inv, err := h.loadEInvoice(id, tenantID)
	if err != nil {
		response.NotFound(c, "E-invoice")
		return
	}
	if inv.Direction != "outgoing" {
		response.BadRequest(c, "Only outgoing invoices can be sent")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}
	creds, err := h.loadCredentials(tenantID, orgPtr, inv.Provider)
	if err == sql.ErrNoRows {
		response.BadRequest(c, "No credentials for provider "+inv.Provider)
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load credentials")
		return
	}

	prov := einvoiceRegistryRef().Get(inv.Provider)
	if prov == nil {
		response.BadRequest(c, "Provider adapter not registered: "+inv.Provider)
		return
	}

	// Translate our domain shape to the provider shape.
	pInv := provider.Invoice{
		ProviderDocID:  strOrEmpty(inv.ProviderDocID),
		FactureType:    strOrEmpty(inv.FactureType),
		DocumentNumber: strOrEmpty(inv.DocumentNumber),
		SellerTIN:      strOrEmpty(inv.SellerTIN), SellerName: strOrEmpty(inv.SellerName),
		BuyerTIN:       strOrEmpty(inv.BuyerTIN),  BuyerName: strOrEmpty(inv.BuyerName),
		TotalAmount:    inv.TotalAmount,
		TaxAmount:      inv.TaxAmount,
		TotalWithTax:   inv.TotalWithTax,
		Currency:       inv.Currency,
	}
	if inv.DocumentDate != nil {
		if t, perr := time.Parse("2006-01-02", *inv.DocumentDate); perr == nil {
			pInv.DocumentDate = t
		}
	}
	for _, l := range inv.Lines {
		pInv.Lines = append(pInv.Lines, provider.InvoiceLine{
			LineNumber: l.LineNumber, ProductCode: l.ProductCode, Description: l.Description,
			Quantity: l.Quantity, Unit: l.Unit, UnitPrice: l.UnitPrice,
			LineAmount: l.LineAmount, TaxRate: l.TaxRate, TaxAmount: l.TaxAmount,
		})
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	res, err := prov.Send(ctx, creds, pInv)
	if err != nil {
		response.InternalError(c, "Send failed: "+err.Error())
		return
	}

	now := time.Now()
	status := "sent"
	if res.Error != "" {
		status = "failed"
	}
	_, _ = h.db.Exec(`
		UPDATE einvoices
		SET status = $1, sent_at = $2, provider_doc_id = COALESCE($3, provider_doc_id),
		    error_message = NULLIF($4, ''), updated_at = $2
		WHERE id = $5
	`, status, now, nullableStr(res.ProviderDocID), res.Error, id)

	// TT §7.4 — dispatch webhook event
	if status == "sent" {
		go h.DispatchWebhookEvent(tenantID, "einvoice.sent", gin.H{
			"einvoice_id":     id,
			"provider":        inv.Provider,
			"provider_doc_id": res.ProviderDocID,
		})
	}

	response.Success(c, gin.H{
		"einvoice_id":     id,
		"provider_doc_id": res.ProviderDocID,
		"status":          status,
		"error":           res.Error,
		"raw_response":    res.RawResponse,
	})
}

// ---------------------------------------------------------------------------
// tiny string helpers
// ---------------------------------------------------------------------------

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableDate(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

// Note: joinStrings is defined in auth.go (package handler) with signature
// (items []string, sep string) string — we reuse it above. No local helper needed.
