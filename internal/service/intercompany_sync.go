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

// SyncSaleOrderToPurchaseOrder creates a PO in target company when SO is created/confirmed
func (s *IntercompanySyncService) SyncSaleOrderToPurchaseOrder(tenantID, sourceOrgID uuid.UUID, saleOrderID uuid.UUID) error {
	// Find applicable rule
	rule, err := s.findActiveRule(tenantID, sourceOrgID, "sale_to_purchase")
	if err != nil || rule == nil {
		return nil // No rule configured, skip
	}

	// Get sale order details
	var so struct {
		ID             uuid.UUID
		OrderNumber    string
		CustomerID     uuid.UUID
		OrganizationID uuid.UUID
		CurrencyID     *uuid.UUID
		TotalAmount    float64
		Notes          *string
	}

	soQuery := `
		SELECT id, order_number, customer_id, organization_id, currency_id, total_amount, notes
		FROM sales_orders
		WHERE id = $1 AND tenant_id = $2
	`
	err = s.db.QueryRow(soQuery, saleOrderID, tenantID).Scan(
		&so.ID, &so.OrderNumber, &so.CustomerID, &so.OrganizationID, &so.CurrencyID, &so.TotalAmount, &so.Notes,
	)
	if err != nil {
		return s.logTransaction(tenantID, rule.ID, sourceOrgID, rule.TargetOrganizationID,
			"sale_order", saleOrderID, so.OrderNumber,
			"purchase_order", nil, "",
			"failed", fmt.Sprintf("Failed to get sale order: %v", err), so.TotalAmount)
	}

	// Check if target org matches customer (inter-company sale)
	if so.CustomerID != rule.TargetOrganizationID {
		return nil // Not an inter-company sale to configured target
	}

	// Create purchase order in target organization
	poID := uuid.New()
	poNumber := fmt.Sprintf("PO-IC-%s", time.Now().Format("20060102150405"))

	// Get the vendor ID for source org in target org's context
	var vendorID uuid.UUID
	vendorQuery := `SELECT id FROM contacts WHERE tenant_id = $1 AND organization_id = $2 AND name = (SELECT name FROM organizations WHERE id = $3)`
	err = s.db.QueryRow(vendorQuery, tenantID, rule.TargetOrganizationID, sourceOrgID).Scan(&vendorID)
	if err != nil {
		// Create vendor if doesn't exist
		vendorID = uuid.New()
		var orgName string
		s.db.QueryRow("SELECT name FROM organizations WHERE id = $1", sourceOrgID).Scan(&orgName)
		_, err = s.db.Exec(`
			INSERT INTO contacts (id, tenant_id, organization_id, name, contact_type, is_vendor)
			VALUES ($1, $2, $3, $4, 'company', true)
		`, vendorID, tenantID, rule.TargetOrganizationID, orgName)
		if err != nil {
			return s.logTransaction(tenantID, rule.ID, sourceOrgID, rule.TargetOrganizationID,
				"sale_order", saleOrderID, so.OrderNumber,
				"purchase_order", nil, "",
				"failed", fmt.Sprintf("Failed to create vendor: %v", err), so.TotalAmount)
		}
	}

	// Calculate target amount based on pricing method
	targetAmount := so.TotalAmount
	if rule.PricingMethod == entity.ICPricingCostPlusMarkup {
		targetAmount = so.TotalAmount * (1 + rule.MarkupPercent/100)
	}

	// Insert purchase order
	poInsertQuery := `
		INSERT INTO purchase_orders (
			id, tenant_id, organization_id, order_number, vendor_id,
			order_date, expected_date, status, total_amount, currency_id,
			notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		RETURNING id
	`

	status := "draft"
	if rule.AutoValidate {
		status = "confirmed"
	}

	notes := fmt.Sprintf("Auto-created from inter-company SO: %s", so.OrderNumber)
	_, err = s.db.Exec(poInsertQuery,
		poID, tenantID, rule.TargetOrganizationID, poNumber, vendorID,
		time.Now(), time.Now().AddDate(0, 0, 7), status, targetAmount, so.CurrencyID,
		notes,
	)
	if err != nil {
		return s.logTransaction(tenantID, rule.ID, sourceOrgID, rule.TargetOrganizationID,
			"sale_order", saleOrderID, so.OrderNumber,
			"purchase_order", nil, "",
			"failed", fmt.Sprintf("Failed to create PO: %v", err), so.TotalAmount)
	}

	// Copy order lines
	linesQuery := `
		SELECT product_id, quantity, unit_price, discount_percent, tax_amount, total_amount
		FROM sales_order_lines
		WHERE sales_order_id = $1
	`
	rows, err := s.db.Query(linesQuery, saleOrderID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var productID uuid.UUID
			var qty, unitPrice, discount, taxAmt, lineTotal float64
			rows.Scan(&productID, &qty, &unitPrice, &discount, &taxAmt, &lineTotal)

			lineID := uuid.New()
			_, s.db.Exec(`
				INSERT INTO purchase_order_lines (
					id, purchase_order_id, product_id, quantity, unit_price,
					discount_percent, tax_amount, total_amount, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
			`, lineID, poID, productID, qty, unitPrice, discount, taxAmt, lineTotal)
		}
	}

	// Create document link
	s.createDocumentLink(tenantID, sourceOrgID, "sale_order", saleOrderID,
		rule.TargetOrganizationID, "purchase_order", poID, "auto_created")

	// Log successful transaction
	logStatus := "created"
	if rule.AutoValidate {
		logStatus = "validated"
	}
	return s.logTransaction(tenantID, rule.ID, sourceOrgID, rule.TargetOrganizationID,
		"sale_order", saleOrderID, so.OrderNumber,
		"purchase_order", &poID, poNumber,
		logStatus, "", so.TotalAmount)
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
	vendorQuery := `SELECT id FROM contacts WHERE tenant_id = $1 AND organization_id = $2 AND name = (SELECT name FROM organizations WHERE id = $3) LIMIT 1`
	err = s.db.QueryRow(vendorQuery, tenantID, rule.TargetOrganizationID, sourceOrgID).Scan(&vendorID)
	if err != nil {
		vendorID = uuid.New()
		var orgName string
		s.db.QueryRow("SELECT name FROM organizations WHERE id = $1", sourceOrgID).Scan(&orgName)
		s.db.Exec(`
			INSERT INTO contacts (id, tenant_id, organization_id, name, contact_type, is_vendor)
			VALUES ($1, $2, $3, $4, 'company', true)
		`, vendorID, tenantID, rule.TargetOrganizationID, orgName)
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

	notes := fmt.Sprintf("Auto-created from inter-company invoice: %s", inv.InvoiceNumber)
	_, err = s.db.Exec(billInsertQuery,
		billID, tenantID, rule.TargetOrganizationID, billNumber, vendorID,
		time.Now(), inv.DueDate, status, inv.TotalAmount, inv.CurrencyID,
		notes,
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
		status, errMsgPtr, amount, time.Now(),
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
