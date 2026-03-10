package service

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/google/uuid"
)

// IntercompanySyncService handles automatic document synchronization between companies
type IntercompanySyncService struct {
	db *sql.DB
}

// NewIntercompanySyncService creates a new sync service
func NewIntercompanySyncService(db *sql.DB) *IntercompanySyncService {
	return &IntercompanySyncService{db: db}
}

// SyncSaleOrderToPurchaseOrder creates a PO in the customer's company when SO is created.
// When company A creates an SO for intercompany customer B, this creates a PO in company B
// with company A as the vendor.
func (s *IntercompanySyncService) SyncSaleOrderToPurchaseOrder(tenantID uuid.UUID, saleOrderID uuid.UUID) error {
	// Get sale order details
	var so struct {
		ID             uuid.UUID
		OrderNumber    string
		CustomerID     uuid.UUID
		OrganizationID *uuid.UUID
		CurrencyID     *uuid.UUID
		ExchangeRate   float64
		Subtotal       float64
		DiscountAmount float64
		TaxAmount      float64
		ShippingAmount float64
		TotalAmount    float64
		Notes          *string
		WarehouseID    *uuid.UUID
		PaymentTerms   int
	}

	soQuery := `
		SELECT id, order_number, customer_id, organization_id, currency_id,
		       COALESCE(exchange_rate, 1), COALESCE(subtotal, 0), COALESCE(discount_amount, 0),
		       COALESCE(tax_amount, 0), COALESCE(shipping_amount, 0), COALESCE(total_amount, 0),
		       notes, warehouse_id, COALESCE(payment_terms, 30)
		FROM sales_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`
	err := s.db.QueryRow(soQuery, saleOrderID, tenantID).Scan(
		&so.ID, &so.OrderNumber, &so.CustomerID, &so.OrganizationID, &so.CurrencyID,
		&so.ExchangeRate, &so.Subtotal, &so.DiscountAmount,
		&so.TaxAmount, &so.ShippingAmount, &so.TotalAmount,
		&so.Notes, &so.WarehouseID, &so.PaymentTerms,
	)
	if err != nil {
		return fmt.Errorf("failed to get sale order: %w", err)
	}

	// Derive sourceOrgID from the SO itself
	if so.OrganizationID == nil {
		return nil // SO has no organization, cannot determine source org
	}
	sourceOrgID := *so.OrganizationID

	// Check if the customer is an intercompany contact by looking at source_organization_id
	var targetOrgID uuid.UUID
	err = s.db.QueryRow(`
		SELECT source_organization_id FROM contacts
		WHERE id = $1 AND tenant_id = $2 AND source_organization_id IS NOT NULL AND deleted_at IS NULL
	`, so.CustomerID, tenantID).Scan(&targetOrgID)
	if err != nil {
		// Customer is not an intercompany contact, nothing to do
		return nil
	}

	// Find the vendor contact for source org in target org's context
	var vendorID uuid.UUID
	err = s.db.QueryRow(`
		SELECT id FROM contacts
		WHERE tenant_id = $1 AND organization_id = $2 AND source_organization_id = $3
		  AND type IN ('vendor', 'both') AND deleted_at IS NULL
		LIMIT 1
	`, tenantID, targetOrgID, sourceOrgID).Scan(&vendorID)
	if err != nil {
		// No vendor contact exists in target org for source org - skip
		return fmt.Errorf("no vendor contact found in target org %s for source org %s", targetOrgID, sourceOrgID)
	}

	// Check if a PO was already created for this SO (prevent duplicates)
	var existingLink int
	s.db.QueryRow(`
		SELECT COUNT(*) FROM intercompany_document_links
		WHERE tenant_id = $1 AND source_document_type = 'sale_order' AND source_document_id = $2
	`, tenantID, saleOrderID).Scan(&existingLink)
	if existingLink > 0 {
		return nil // Already synced
	}

	// Also check reverse: this SO might have been created FROM a PO (intercompany PO→SO)
	var reverseLink int
	s.db.QueryRow(`
		SELECT COUNT(*) FROM intercompany_document_links
		WHERE tenant_id = $1 AND linked_document_type = 'sale_order' AND linked_document_id = $2
	`, tenantID, saleOrderID).Scan(&reverseLink)
	if reverseLink > 0 {
		return nil // This SO was created from a PO, don't create another PO
	}

	// Create purchase order in target organization
	poID := uuid.New()
	poNumber := fmt.Sprintf("PO-IC-%s", time.Now().Format("20060102150405"))
	now := time.Now()

	notes := fmt.Sprintf("Auto-created from intercompany SO: %s", so.OrderNumber)

	poInsertQuery := `
		INSERT INTO purchase_orders (
			id, tenant_id, organization_id, order_number, vendor_id,
			order_date, expected_date, currency_id, exchange_rate,
			subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
			status, payment_status, payment_terms,
			vendor_reference, notes, warehouse_id,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
	`

	_, err = s.db.Exec(poInsertQuery,
		poID, tenantID, targetOrgID, poNumber, vendorID,
		now, now.AddDate(0, 0, 7), so.CurrencyID, so.ExchangeRate,
		so.Subtotal, so.DiscountAmount, so.TaxAmount, so.ShippingAmount, so.TotalAmount,
		entity.POStatusDraft, entity.PaymentStatusUnpaid, so.PaymentTerms,
		so.OrderNumber, notes, so.WarehouseID,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("failed to create intercompany PO: %w", err)
	}

	// Copy SO lines to PO lines
	soLinesQuery := `
		SELECT product_id, description, quantity, unit_id, unit_price,
		       COALESCE(discount_amount, 0), tax_id, COALESCE(tax_amount, 0),
		       COALESCE(line_total, 0), warehouse_id, notes,
		       packaging_id, packaging_qty
		FROM sales_order_lines
		WHERE sales_order_id = $1
		ORDER BY line_number
	`
	rows, err := s.db.Query(soLinesQuery, saleOrderID)
	if err == nil {
		defer rows.Close()
		lineNum := 0
		for rows.Next() {
			lineNum++
			var productID uuid.UUID
			var description sql.NullString
			var qty, unitPrice, discountAmt, taxAmt, lineTotal float64
			var unitID, taxID, lineWarehouseID, packagingID *uuid.UUID
			var lineNotes sql.NullString
			var packagingQty *float64

			rows.Scan(&productID, &description, &qty, &unitID, &unitPrice,
				&discountAmt, &taxID, &taxAmt, &lineTotal,
				&lineWarehouseID, &lineNotes, &packagingID, &packagingQty)

			lineID := uuid.New()
			s.db.Exec(`
				INSERT INTO purchase_order_lines (
					id, purchase_order_id, line_number, product_id, description,
					quantity, unit_id, unit_price, discount_amount, tax_id, tax_amount,
					line_total, quantity_received, quantity_invoiced,
					warehouse_id, notes, packaging_id, packaging_qty,
					created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
			`, lineID, poID, lineNum, productID, description,
				qty, unitID, unitPrice, discountAmt, taxID, taxAmt,
				lineTotal, 0.0, 0.0,
				lineWarehouseID, lineNotes, packagingID, packagingQty,
				now, now)
		}
	}

	// Create document link for traceability
	s.createDocumentLink(tenantID, sourceOrgID, "sale_order", saleOrderID,
		targetOrgID, "purchase_order", poID, "auto_created")

	return nil
}

// SyncPurchaseOrderToSaleOrder creates an SO in the vendor's company when PO is created.
// When company A creates a PO for intercompany vendor B, this creates an SO in company B
// with company A as the customer.
func (s *IntercompanySyncService) SyncPurchaseOrderToSaleOrder(tenantID uuid.UUID, purchaseOrderID uuid.UUID) error {
	// Get purchase order details
	var po struct {
		ID             uuid.UUID
		OrderNumber    string
		VendorID       uuid.UUID
		OrganizationID *uuid.UUID
		CurrencyID     *uuid.UUID
		ExchangeRate   float64
		Subtotal       float64
		DiscountAmount float64
		TaxAmount      float64
		ShippingAmount float64
		TotalAmount    float64
		Notes          *string
		WarehouseID    *uuid.UUID
		PaymentTerms   *int
	}

	poQuery := `
		SELECT id, order_number, vendor_id, organization_id, currency_id,
		       COALESCE(exchange_rate, 1), COALESCE(subtotal, 0), COALESCE(discount_amount, 0),
		       COALESCE(tax_amount, 0), COALESCE(shipping_amount, 0), COALESCE(total_amount, 0),
		       notes, warehouse_id, payment_terms
		FROM purchase_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`
	err := s.db.QueryRow(poQuery, purchaseOrderID, tenantID).Scan(
		&po.ID, &po.OrderNumber, &po.VendorID, &po.OrganizationID, &po.CurrencyID,
		&po.ExchangeRate, &po.Subtotal, &po.DiscountAmount,
		&po.TaxAmount, &po.ShippingAmount, &po.TotalAmount,
		&po.Notes, &po.WarehouseID, &po.PaymentTerms,
	)
	if err != nil {
		return fmt.Errorf("failed to get purchase order: %w", err)
	}

	// Derive sourceOrgID from the PO itself
	if po.OrganizationID == nil {
		return nil // PO has no organization, cannot determine source org
	}
	sourceOrgID := *po.OrganizationID

	// Check if the vendor is an intercompany contact
	var targetOrgID uuid.UUID
	err = s.db.QueryRow(`
		SELECT source_organization_id FROM contacts
		WHERE id = $1 AND tenant_id = $2 AND source_organization_id IS NOT NULL AND deleted_at IS NULL
	`, po.VendorID, tenantID).Scan(&targetOrgID)
	if err != nil {
		// Vendor is not an intercompany contact, nothing to do
		return nil
	}

	// Find the customer contact for source org in target org's context
	var customerID uuid.UUID
	err = s.db.QueryRow(`
		SELECT id FROM contacts
		WHERE tenant_id = $1 AND organization_id = $2 AND source_organization_id = $3
		  AND type IN ('customer', 'both') AND deleted_at IS NULL
		LIMIT 1
	`, tenantID, targetOrgID, sourceOrgID).Scan(&customerID)
	if err != nil {
		// No customer contact exists in target org for source org - skip
		return fmt.Errorf("no customer contact found in target org %s for source org %s", targetOrgID, sourceOrgID)
	}

	// Check if an SO was already created for this PO (prevent duplicates)
	var existingLink int
	s.db.QueryRow(`
		SELECT COUNT(*) FROM intercompany_document_links
		WHERE tenant_id = $1 AND source_document_type = 'purchase_order' AND source_document_id = $2
	`, tenantID, purchaseOrderID).Scan(&existingLink)
	if existingLink > 0 {
		return nil // Already synced
	}

	// Create sales order in target organization
	soID := uuid.New()
	soNumber := fmt.Sprintf("SO-IC-%s", time.Now().Format("20060102150405"))
	now := time.Now()

	notes := fmt.Sprintf("Auto-created from intercompany PO: %s", po.OrderNumber)
	paymentTerms := 30
	if po.PaymentTerms != nil {
		paymentTerms = *po.PaymentTerms
	}

	soInsertQuery := `
		INSERT INTO sales_orders (
			id, tenant_id, organization_id, order_number, customer_id,
			order_date, expected_date, currency_id, exchange_rate,
			subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
			status, payment_status, payment_terms,
			po_number, notes, warehouse_id,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
	`

	_, err = s.db.Exec(soInsertQuery,
		soID, tenantID, targetOrgID, soNumber, customerID,
		now, now.AddDate(0, 0, 7), po.CurrencyID, po.ExchangeRate,
		po.Subtotal, po.DiscountAmount, po.TaxAmount, po.ShippingAmount, po.TotalAmount,
		entity.OrderStatusDraft, entity.PaymentStatusUnpaid, paymentTerms,
		po.OrderNumber, notes, po.WarehouseID,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("failed to create intercompany SO: %w", err)
	}

	// Copy PO lines to SO lines
	poLinesQuery := `
		SELECT product_id, description, quantity, unit_id, unit_price,
		       COALESCE(discount_amount, 0), tax_id, COALESCE(tax_amount, 0),
		       COALESCE(line_total, 0), warehouse_id, notes,
		       packaging_id, packaging_qty
		FROM purchase_order_lines
		WHERE purchase_order_id = $1
		ORDER BY line_number
	`
	rows, err := s.db.Query(poLinesQuery, purchaseOrderID)
	if err == nil {
		defer rows.Close()
		lineNum := 0
		for rows.Next() {
			lineNum++
			var productID *uuid.UUID
			var description sql.NullString
			var qty, unitPrice, discountAmt, taxAmt, lineTotal float64
			var unitID, taxID, lineWarehouseID, packagingID *uuid.UUID
			var lineNotes sql.NullString
			var packagingQty *float64

			rows.Scan(&productID, &description, &qty, &unitID, &unitPrice,
				&discountAmt, &taxID, &taxAmt, &lineTotal,
				&lineWarehouseID, &lineNotes, &packagingID, &packagingQty)

			lineID := uuid.New()
			s.db.Exec(`
				INSERT INTO sales_order_lines (
					id, sales_order_id, line_number, product_id, description,
					quantity, unit_id, unit_price, discount_amount,
					tax_id, tax_amount, line_total,
					quantity_delivered, quantity_invoiced,
					warehouse_id, notes, packaging_id, packaging_qty,
					created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
			`, lineID, soID, lineNum, productID, description,
				qty, unitID, unitPrice, discountAmt,
				taxID, taxAmt, lineTotal,
				0.0, 0.0,
				lineWarehouseID, lineNotes, packagingID, packagingQty,
				now, now)
		}
	}

	// Create document link for traceability
	s.createDocumentLink(tenantID, sourceOrgID, "purchase_order", purchaseOrderID,
		targetOrgID, "sale_order", soID, "auto_created")

	return nil
}

// SyncInvoiceToBill creates a vendor bill in target company when invoice is created
func (s *IntercompanySyncService) SyncInvoiceToBill(tenantID, sourceOrgID uuid.UUID, invoiceID uuid.UUID) error {
	// Find applicable rule
	rule, err := s.findActiveRule(tenantID, sourceOrgID, "invoice_to_bill")
	if err != nil || rule == nil {
		return nil // No rule configured, skip
	}

	// Get invoice details
	var inv struct {
		ID             uuid.UUID
		InvoiceNumber  string
		CustomerID     uuid.UUID
		OrganizationID uuid.UUID
		CurrencyID     *uuid.UUID
		TotalAmount    float64
		DueDate        time.Time
	}

	invQuery := `
		SELECT id, invoice_number, customer_id, organization_id, currency_id, total_amount, due_date
		FROM invoices
		WHERE id = $1 AND tenant_id = $2
	`
	err = s.db.QueryRow(invQuery, invoiceID, tenantID).Scan(
		&inv.ID, &inv.InvoiceNumber, &inv.CustomerID, &inv.OrganizationID, &inv.CurrencyID, &inv.TotalAmount, &inv.DueDate,
	)
	if err != nil {
		return s.logTransaction(tenantID, rule.ID, sourceOrgID, rule.TargetOrganizationID,
			"invoice", invoiceID, "",
			"bill", nil, "",
			"failed", fmt.Sprintf("Failed to get invoice: %v", err), 0)
	}

	// Check if target org matches customer
	if inv.CustomerID != rule.TargetOrganizationID {
		return nil // Not an inter-company invoice
	}

	// Create vendor bill in target organization
	billID := uuid.New()
	billNumber := fmt.Sprintf("BILL-IC-%s", time.Now().Format("20060102150405"))

	// Get or create vendor
	var vendorID uuid.UUID
	vendorQuery := `SELECT id FROM contacts WHERE tenant_id = $1 AND organization_id = $2 AND source_organization_id = $3 AND type IN ('vendor', 'both') AND deleted_at IS NULL LIMIT 1`
	err = s.db.QueryRow(vendorQuery, tenantID, rule.TargetOrganizationID, sourceOrgID).Scan(&vendorID)
	if err != nil {
		return s.logTransaction(tenantID, rule.ID, sourceOrgID, rule.TargetOrganizationID,
			"invoice", invoiceID, inv.InvoiceNumber,
			"bill", nil, "",
			"failed", "No vendor contact found for intercompany billing", inv.TotalAmount)
	}

	// Insert vendor bill
	status := "draft"
	if rule.AutoValidate {
		status = "posted"
	}

	billInsertQuery := `
		INSERT INTO invoices (
			id, tenant_id, organization_id, invoice_number, invoice_type, customer_id,
			invoice_date, due_date, status, total_amount, currency_id,
			notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'vendor_bill', $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
	`

	invNotes := fmt.Sprintf("Auto-created from inter-company invoice: %s", inv.InvoiceNumber)
	_, err = s.db.Exec(billInsertQuery,
		billID, tenantID, rule.TargetOrganizationID, billNumber, vendorID,
		time.Now(), inv.DueDate, status, inv.TotalAmount, inv.CurrencyID,
		invNotes,
	)
	if err != nil {
		return s.logTransaction(tenantID, rule.ID, sourceOrgID, rule.TargetOrganizationID,
			"invoice", invoiceID, inv.InvoiceNumber,
			"bill", nil, "",
			"failed", fmt.Sprintf("Failed to create bill: %v", err), inv.TotalAmount)
	}

	// Create document link
	s.createDocumentLink(tenantID, sourceOrgID, "invoice", invoiceID,
		rule.TargetOrganizationID, "bill", billID, "auto_created")

	// Log successful transaction
	logStatus := "created"
	if rule.AutoValidate {
		logStatus = "validated"
	}
	return s.logTransaction(tenantID, rule.ID, sourceOrgID, rule.TargetOrganizationID,
		"invoice", invoiceID, inv.InvoiceNumber,
		"bill", &billID, billNumber,
		logStatus, "", inv.TotalAmount)
}

// Helper methods

func (s *IntercompanySyncService) findActiveRule(tenantID, sourceOrgID uuid.UUID, ruleType string) (*entity.IntercompanyRule, error) {
	query := `
		SELECT id, tenant_id, source_organization_id, target_organization_id,
			   rule_type, is_active, auto_validate, sync_prices,
			   default_warehouse_id, pricing_method, markup_percent
		FROM intercompany_rules
		WHERE tenant_id = $1 AND source_organization_id = $2 AND rule_type = $3 AND is_active = true
		LIMIT 1
	`

	var rule entity.IntercompanyRule
	var defaultWarehouseID sql.NullString

	err := s.db.QueryRow(query, tenantID, sourceOrgID, ruleType).Scan(
		&rule.ID, &rule.TenantID, &rule.SourceOrganizationID, &rule.TargetOrganizationID,
		&rule.RuleType, &rule.IsActive, &rule.AutoValidate, &rule.SyncPrices,
		&defaultWarehouseID, &rule.PricingMethod, &rule.MarkupPercent,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if defaultWarehouseID.Valid {
		id, _ := uuid.Parse(defaultWarehouseID.String)
		rule.DefaultWarehouseID = &id
	}

	return &rule, nil
}

func (s *IntercompanySyncService) logTransaction(
	tenantID uuid.UUID, ruleID uuid.UUID,
	sourceOrgID uuid.UUID, targetOrgID uuid.UUID,
	sourceDocType string, sourceDocID uuid.UUID, sourceDocNumber string,
	targetDocType string, targetDocID *uuid.UUID, targetDocNumber string,
	status string, errorMsg string, amount float64,
) error {
	query := `
		INSERT INTO intercompany_transaction_logs (
			id, tenant_id, rule_id,
			source_organization_id, source_document_type, source_document_id, source_document_number,
			target_organization_id, target_document_type, target_document_id, target_document_number,
			status, error_message, source_amount, processed_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW())
	`

	var errMsgPtr *string
	if errorMsg != "" {
		errMsgPtr = &errorMsg
	}

	var targetDocNumberPtr *string
	if targetDocNumber != "" {
		targetDocNumberPtr = &targetDocNumber
	}

	_, err := s.db.Exec(query,
		uuid.New(), tenantID, ruleID,
		sourceOrgID, sourceDocType, sourceDocID, sourceDocNumber,
		targetOrgID, targetDocType, targetDocID, targetDocNumberPtr,
		status, errMsgPtr, amount,
	)
	return err
}

func (s *IntercompanySyncService) createDocumentLink(
	tenantID uuid.UUID,
	sourceOrgID uuid.UUID, sourceDocType string, sourceDocID uuid.UUID,
	linkedOrgID uuid.UUID, linkedDocType string, linkedDocID uuid.UUID,
	linkType string,
) error {
	query := `
		INSERT INTO intercompany_document_links (
			id, tenant_id,
			source_organization_id, source_document_type, source_document_id,
			linked_organization_id, linked_document_type, linked_document_id,
			link_type, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT DO NOTHING
	`

	_, err := s.db.Exec(query,
		uuid.New(), tenantID,
		sourceOrgID, sourceDocType, sourceDocID,
		linkedOrgID, linkedDocType, linkedDocID,
		linkType,
	)
	return err
}

// GetLinkedDocument returns the linked document for a source document
func (s *IntercompanySyncService) GetLinkedDocument(tenantID uuid.UUID, sourceDocType string, sourceDocID uuid.UUID) (*entity.IntercompanyDocumentLink, error) {
	query := `
		SELECT id, tenant_id,
			   source_organization_id, source_document_type, source_document_id,
			   linked_organization_id, linked_document_type, linked_document_id,
			   link_type, created_at
		FROM intercompany_document_links
		WHERE tenant_id = $1 AND source_document_type = $2 AND source_document_id = $3
		LIMIT 1
	`

	var link entity.IntercompanyDocumentLink
	err := s.db.QueryRow(query, tenantID, sourceDocType, sourceDocID).Scan(
		&link.ID, &link.TenantID,
		&link.SourceOrganizationID, &link.SourceDocumentType, &link.SourceDocumentID,
		&link.LinkedOrganizationID, &link.LinkedDocumentType, &link.LinkedDocumentID,
		&link.LinkType, &link.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &link, nil
}

// GetLinkedDocumentReverse returns the source document for a linked document (reverse lookup).
// E.g., given a PO (linked side), find the SO (source side) that created it.
func (s *IntercompanySyncService) GetLinkedDocumentReverse(tenantID uuid.UUID, linkedDocType string, linkedDocID uuid.UUID) (*entity.IntercompanyDocumentLink, error) {
	query := `
		SELECT id, tenant_id,
			   source_organization_id, source_document_type, source_document_id,
			   linked_organization_id, linked_document_type, linked_document_id,
			   link_type, created_at
		FROM intercompany_document_links
		WHERE tenant_id = $1 AND linked_document_type = $2 AND linked_document_id = $3
		LIMIT 1
	`

	var link entity.IntercompanyDocumentLink
	err := s.db.QueryRow(query, tenantID, linkedDocType, linkedDocID).Scan(
		&link.ID, &link.TenantID,
		&link.SourceOrganizationID, &link.SourceDocumentType, &link.SourceDocumentID,
		&link.LinkedOrganizationID, &link.LinkedDocumentType, &link.LinkedDocumentID,
		&link.LinkType, &link.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &link, nil
}
