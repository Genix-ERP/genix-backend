package handler

// File: buxgalteriya_einvoice.go
//
// TT Buxgalteriya ERP §8.2 — E-invoice (elektron hisob-faktura) skeleton.
//
// This is a pass-through layer: it stores incoming e-invoices from soliq.uz /
// didox.uz / faktura providers and lets the buxgalter review/approve them
// before they become purchase invoices + journal entries. Outgoing invoices
// go in the opposite direction.
//
// The actual provider transport (SOAP/XML for soliq.uz, REST for didox) is
// NOT implemented here — this is the domain + workflow layer. A follow-up
// will add the provider adapters in internal/integration/einvoice/.

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// EInvoice is the API-facing shape.
type EInvoice struct {
	ID                   uuid.UUID      `json:"id"`
	Provider             string         `json:"provider"`
	Direction            string         `json:"direction"`
	ProviderDocID        *string        `json:"provider_doc_id,omitempty"`
	FactureType          *string        `json:"facture_type,omitempty"`
	DocumentNumber       *string        `json:"document_number,omitempty"`
	DocumentDate         *string        `json:"document_date,omitempty"`
	SellerTIN            *string        `json:"seller_tin,omitempty"`
	SellerName           *string        `json:"seller_name,omitempty"`
	BuyerTIN             *string        `json:"buyer_tin,omitempty"`
	BuyerName            *string        `json:"buyer_name,omitempty"`
	TotalAmount          float64        `json:"total_amount"`
	TaxAmount            float64        `json:"tax_amount"`
	TotalWithTax         float64        `json:"total_with_tax"`
	Currency             string         `json:"currency"`
	Status               string         `json:"status"`
	LinkedContactID      *uuid.UUID     `json:"linked_contact_id,omitempty"`
	LinkedJournalEntryID *uuid.UUID     `json:"linked_journal_entry_id,omitempty"`
	ErrorMessage         *string        `json:"error_message,omitempty"`
	Lines                []EInvoiceLine `json:"lines,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
}

type EInvoiceLine struct {
	LineNumber  int     `json:"line_number"`
	ProductCode string  `json:"product_code"`
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	Unit        string  `json:"unit"`
	UnitPrice   float64 `json:"unit_price"`
	LineAmount  float64 `json:"line_amount"`
	TaxRate     float64 `json:"tax_rate"`
	TaxAmount   float64 `json:"tax_amount"`
}

// IngestEInvoice accepts a provider payload (already parsed XML→JSON) and
// saves it to einvoices / einvoice_lines for buxgalter review.
// Body:
//
//	{
//	  "provider": "didox",
//	  "direction": "incoming",
//	  "provider_doc_id": "…",
//	  "document_number": "HF-12345",
//	  "document_date": "2026-04-01",
//	  "seller_tin": "302345678", "seller_name": "LLC Supplier",
//	  "buyer_tin":  "302999999", "buyer_name": "LLC Ours",
//	  "total_amount": 10000, "tax_amount": 1200, "total_with_tax": 11200,
//	  "currency": "UZS",
//	  "lines": [ { ... } ]
//	}
func (h *Handler) IngestEInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var payload struct {
		Provider       string         `json:"provider" binding:"required,oneof=didox faktura soliq"`
		Direction      string         `json:"direction" binding:"required,oneof=incoming outgoing"`
		ProviderDocID  *string        `json:"provider_doc_id"`
		FactureType    *string        `json:"facture_type"`
		DocumentNumber *string        `json:"document_number"`
		DocumentDate   *string        `json:"document_date"`
		SellerTIN      *string        `json:"seller_tin"`
		SellerName     *string        `json:"seller_name"`
		BuyerTIN       *string        `json:"buyer_tin"`
		BuyerName      *string        `json:"buyer_name"`
		TotalAmount    float64        `json:"total_amount"`
		TaxAmount      float64        `json:"tax_amount"`
		TotalWithTax   float64        `json:"total_with_tax"`
		Currency       string         `json:"currency"`
		Lines          []EInvoiceLine `json:"lines"`
		RawXML         *string        `json:"raw_xml"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, fmt.Sprintf("Invalid payload: %v", err))
		return
	}
	if payload.Currency == "" {
		payload.Currency = "UZS"
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}

	id := uuid.New()
	now := time.Now()

	// De-dupe: if we've already ingested the same provider_doc_id, return existing.
	if payload.ProviderDocID != nil && *payload.ProviderDocID != "" {
		var existing uuid.UUID
		err := h.db.QueryRow(`
			SELECT id FROM einvoices
			WHERE tenant_id = $1 AND provider = $2 AND provider_doc_id = $3
			LIMIT 1
		`, tenantID, payload.Provider, *payload.ProviderDocID).Scan(&existing)
		if err == nil {
			// Already ingested — return the existing record
			resp, _ := h.loadEInvoice(existing, tenantID)
			c.JSON(200, gin.H{"data": resp, "already_ingested": true})
			return
		}
	}

	var docDate interface{}
	if payload.DocumentDate != nil && *payload.DocumentDate != "" {
		if t, err := time.Parse("2006-01-02", *payload.DocumentDate); err == nil {
			docDate = t
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO einvoices (
			id, tenant_id, organization_id, provider, direction,
			provider_doc_id, facture_type, document_number, document_date,
			seller_tin, seller_name, buyer_tin, buyer_name,
			total_amount, tax_amount, total_with_tax, currency,
			status, raw_xml, received_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13,
			$14, $15, $16, $17,
			'received', $18, $19, $19, $19
		)
	`, id, tenantID, orgPtr, payload.Provider, payload.Direction,
		payload.ProviderDocID, payload.FactureType, payload.DocumentNumber, docDate,
		payload.SellerTIN, payload.SellerName, payload.BuyerTIN, payload.BuyerName,
		payload.TotalAmount, payload.TaxAmount, payload.TotalWithTax, payload.Currency,
		payload.RawXML, now)
	if err != nil {
		h.log.Error("IngestEInvoice: header insert failed", "error", err)
		response.InternalError(c, "Failed to ingest e-invoice")
		return
	}

	for i, l := range payload.Lines {
		_, err := tx.Exec(`
			INSERT INTO einvoice_lines (
				id, einvoice_id, line_number, product_code, description,
				quantity, unit, unit_price, line_amount, tax_rate, tax_amount, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`, uuid.New(), id, i+1, l.ProductCode, l.Description,
			l.Quantity, l.Unit, l.UnitPrice, l.LineAmount, l.TaxRate, l.TaxAmount, now)
		if err != nil {
			h.log.Error("IngestEInvoice: line insert failed", "error", err)
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
	}

	resp, _ := h.loadEInvoice(id, tenantID)
	response.Created(c, resp)
}

// ListEInvoices returns recent e-invoices filtered by direction/status.
func (h *Handler) ListEInvoices(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	direction := c.Query("direction")
	status := c.Query("status")

	q := `
		SELECT id, provider, direction, provider_doc_id, facture_type,
		       document_number, document_date, seller_tin, seller_name,
		       buyer_tin, buyer_name, total_amount, tax_amount, total_with_tax,
		       currency, status, linked_contact_id, linked_journal_entry_id,
		       error_message, created_at
		FROM einvoices
		WHERE tenant_id = $1`
	args := []interface{}{tenantID}
	if direction != "" {
		q += " AND direction = $2"
		args = append(args, direction)
	}
	if status != "" {
		q += fmt.Sprintf(" AND status = $%d", len(args)+1)
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC LIMIT 200"

	rows, err := h.db.Query(q, args...)
	if err != nil {
		response.InternalError(c, "Failed to list")
		return
	}
	defer rows.Close()

	out := make([]EInvoice, 0)
	for rows.Next() {
		inv, err := scanEInvoiceRow(rows)
		if err != nil {
			continue
		}
		out = append(out, inv)
	}
	response.Success(c, out)
}

// ApproveEInvoice marks an e-invoice as approved and optionally links it to a
// purchase invoice (for incoming) or sales invoice (for outgoing). Linking to
// a journal entry happens in a separate step once the PO/SO exists.
func (h *Handler) ApproveEInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}

	var body struct {
		LinkedContactID         *string `json:"linked_contact_id"`
		LinkedPurchaseInvoiceID *string `json:"linked_purchase_invoice_id"`
		LinkedSalesInvoiceID    *string `json:"linked_sales_invoice_id"`
	}
	_ = c.ShouldBindJSON(&body)

	now := time.Now()
	var ownerTenant uuid.UUID
	if err := h.db.QueryRow(
		`SELECT tenant_id FROM einvoices WHERE id = $1`, id,
	).Scan(&ownerTenant); err != nil || ownerTenant != tenantID {
		response.NotFound(c, "E-invoice")
		return
	}

	_, err = h.db.Exec(`
		UPDATE einvoices
		SET status = 'approved',
		    approved_at = $1, approved_by = $2,
		    linked_contact_id = COALESCE($3, linked_contact_id),
		    linked_purchase_invoice_id = COALESCE($4, linked_purchase_invoice_id),
		    linked_sales_invoice_id = COALESCE($5, linked_sales_invoice_id),
		    updated_at = $1
		WHERE id = $6
	`, now, userID,
		parseOptionalUUID(body.LinkedContactID),
		parseOptionalUUID(body.LinkedPurchaseInvoiceID),
		parseOptionalUUID(body.LinkedSalesInvoiceID),
		id)
	if err != nil {
		response.InternalError(c, "Failed to approve")
		return
	}

	inv, _ := h.loadEInvoice(id, tenantID)
	// TT §7.4
	if inv != nil {
		go h.DispatchWebhookEvent(tenantID, "einvoice.approved", inv)
	}
	response.Success(c, inv)
}

// RejectEInvoice marks an e-invoice as rejected with a reason.
func (h *Handler) RejectEInvoice(c *gin.Context) {
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

	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Reason is required")
		return
	}

	var ownerTenant uuid.UUID
	if err := h.db.QueryRow(
		`SELECT tenant_id FROM einvoices WHERE id = $1`, id,
	).Scan(&ownerTenant); err != nil || ownerTenant != tenantID {
		response.NotFound(c, "E-invoice")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE einvoices
		SET status = 'rejected',
		    rejected_at = $1,
		    rejection_reason = $2,
		    updated_at = $1
		WHERE id = $3
	`, now, body.Reason, id)
	if err != nil {
		response.InternalError(c, "Failed to reject")
		return
	}

	inv, _ := h.loadEInvoice(id, tenantID)
	response.Success(c, inv)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (h *Handler) loadEInvoice(id uuid.UUID, tenantID uuid.UUID) (*EInvoice, error) {
	row := h.db.QueryRow(`
		SELECT id, provider, direction, provider_doc_id, facture_type,
		       document_number, document_date, seller_tin, seller_name,
		       buyer_tin, buyer_name, total_amount, tax_amount, total_with_tax,
		       currency, status, linked_contact_id, linked_journal_entry_id,
		       error_message, created_at
		FROM einvoices WHERE id = $1 AND tenant_id = $2
	`, id, tenantID)
	inv, err := scanEInvoiceRow(row)
	if err != nil {
		return nil, err
	}

	// Load lines
	lineRows, err := h.db.Query(`
		SELECT line_number, product_code, description, quantity, unit,
		       unit_price, line_amount, tax_rate, tax_amount
		FROM einvoice_lines WHERE einvoice_id = $1 ORDER BY line_number
	`, id)
	if err == nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var l EInvoiceLine
			var pc, desc, unit sql.NullString
			var taxRate sql.NullFloat64
			if err := lineRows.Scan(&l.LineNumber, &pc, &desc, &l.Quantity, &unit,
				&l.UnitPrice, &l.LineAmount, &taxRate, &l.TaxAmount); err != nil {
				continue
			}
			if pc.Valid {
				l.ProductCode = pc.String
			}
			if desc.Valid {
				l.Description = desc.String
			}
			if unit.Valid {
				l.Unit = unit.String
			}
			if taxRate.Valid {
				l.TaxRate = taxRate.Float64
			}
			inv.Lines = append(inv.Lines, l)
		}
	}
	return &inv, nil
}

func scanEInvoiceRow(s rowScanner) (EInvoice, error) {
	var inv EInvoice
	var providerDocID, factureType, documentNumber,
		sellerTIN, sellerName, buyerTIN, buyerName,
		errorMsg sql.NullString
	var documentDate sql.NullTime
	var linkedContactID, linkedJournalEntryID sql.NullString

	err := s.Scan(
		&inv.ID, &inv.Provider, &inv.Direction, &providerDocID, &factureType,
		&documentNumber, &documentDate, &sellerTIN, &sellerName,
		&buyerTIN, &buyerName, &inv.TotalAmount, &inv.TaxAmount, &inv.TotalWithTax,
		&inv.Currency, &inv.Status, &linkedContactID, &linkedJournalEntryID,
		&errorMsg, &inv.CreatedAt,
	)
	if err != nil {
		return inv, err
	}
	setNullableStrP(&inv.ProviderDocID, providerDocID)
	setNullableStrP(&inv.FactureType, factureType)
	setNullableStrP(&inv.DocumentNumber, documentNumber)
	setNullableStrP(&inv.SellerTIN, sellerTIN)
	setNullableStrP(&inv.SellerName, sellerName)
	setNullableStrP(&inv.BuyerTIN, buyerTIN)
	setNullableStrP(&inv.BuyerName, buyerName)
	setNullableStrP(&inv.ErrorMessage, errorMsg)
	if documentDate.Valid {
		s := documentDate.Time.Format("2006-01-02")
		inv.DocumentDate = &s
	}
	if linkedContactID.Valid {
		if u, err := uuid.Parse(linkedContactID.String); err == nil {
			inv.LinkedContactID = &u
		}
	}
	if linkedJournalEntryID.Valid {
		if u, err := uuid.Parse(linkedJournalEntryID.String); err == nil {
			inv.LinkedJournalEntryID = &u
		}
	}
	return inv, nil
}

func setNullableStrP(dst **string, src sql.NullString) {
	if src.Valid {
		v := src.String
		*dst = &v
	}
}
