package service

import (
	"database/sql"
	"fmt"
	"strings"
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

// resolveCrossOrgProductID maps a product from the source organisation
// to the matching product in the target organisation via search_key.
//
// Scenario: construction company A raises a PO for product
// "ПЛИТЫ ПЕРЕКРЫТИЙ 1 ПК 59.10-6ШВ С8" (A's product row). The sync
// creates an SO in manufacturing company B — but B doesn't own A's
// product row, B sells the same material under "ПБ-5.9*100а" with its
// own product row. We find B's row by search_key match and use
// *that* id on the SO line so B can ship from their inventory.
//
// Returns the target org's product id if a match exists; otherwise
// returns the original sourceProductID so the line still carries a
// product reference (even if unlinked from the target org).
func (s *IntercompanySyncService) resolveCrossOrgProductID(
	tenantID, targetOrgID uuid.UUID, sourceProductID *uuid.UUID,
) *uuid.UUID {
	if sourceProductID == nil {
		return nil
	}
	// Read the source product's search_key.
	var key sql.NullString
	if err := s.db.QueryRow(
		`SELECT search_key FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		*sourceProductID, tenantID,
	).Scan(&key); err != nil || !key.Valid || key.String == "" {
		return sourceProductID // no key → can't resolve, keep original
	}
	// Find a product in the target org with the same key.
	var targetID uuid.UUID
	err := s.db.QueryRow(`
		SELECT p.id
		FROM products p
		INNER JOIN product_organization_settings pos
		        ON pos.product_id = p.id AND pos.organization_id = $3
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND upper(p.search_key) = upper($2)
		ORDER BY p.created_at ASC
		LIMIT 1`,
		tenantID, key.String, targetOrgID,
	).Scan(&targetID)
	if err != nil {
		return sourceProductID // no match in target org → keep source
	}
	return &targetID
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
	// Same numbering scheme as CreatePurchaseOrder: MAX over `PO-` + up to six
	// digits, %03d so the width grows on its own. COUNT(*) re-issued a number
	// after any deletion.
	var poCount int
	s.db.QueryRow(`
		SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number, '[^0-9]', '', 'g') AS BIGINT)), 0)
		FROM purchase_orders
		WHERE tenant_id = $1 AND order_number ~ '^PO-[0-9]{1,6}$'`,
		tenantID).Scan(&poCount)
	poNumber := fmt.Sprintf("PO-%03d", poCount+1)
	now := time.Now()

	notes := fmt.Sprintf("Auto-created from intercompany SO: %s", so.OrderNumber)

	// Use target org's default warehouse, not source org's
	var targetWarehouseID *uuid.UUID
	var twID uuid.UUID
	if err := s.db.QueryRow(`
		SELECT id FROM warehouses
		WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL
		ORDER BY created_at ASC LIMIT 1
	`, tenantID, targetOrgID).Scan(&twID); err == nil {
		targetWarehouseID = &twID
	}

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
		entity.POStatusOrdered, entity.PaymentStatusUnpaid, so.PaymentTerms,
		so.OrderNumber, notes, targetWarehouseID,
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

			// Resolve seller's product → buyer's equivalent via
			// search_key, so the PO line references a product in
			// the buyer's (target) organisation.
			srcID := productID
			resolvedProductID := s.resolveCrossOrgProductID(tenantID, targetOrgID, &srcID)

			// When resolution succeeds, swap the description to
			// the target product's name so the buyer sees their
			// own product name on their PO (not the seller's).
			lineDescription := description
			if resolvedProductID != nil && *resolvedProductID != productID {
				var targetName sql.NullString
				if err := s.db.QueryRow(
					`SELECT name FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
					*resolvedProductID, tenantID,
				).Scan(&targetName); err == nil && targetName.Valid && targetName.String != "" {
					lineDescription = targetName
				}
			}

			lineID := uuid.New()
			s.db.Exec(`
				INSERT INTO purchase_order_lines (
					id, purchase_order_id, line_number, product_id, description,
					quantity, unit_id, unit_price, discount_amount, tax_id, tax_amount,
					line_total, quantity_received, quantity_invoiced,
					warehouse_id, notes, packaging_id, packaging_qty,
					created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
			`, lineID, poID, lineNum, resolvedProductID, lineDescription,
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

	// Also check reverse: this PO might have been created FROM an SO (intercompany SO→PO)
	var reverseLink int
	s.db.QueryRow(`
		SELECT COUNT(*) FROM intercompany_document_links
		WHERE tenant_id = $1 AND linked_document_type = 'purchase_order' AND linked_document_id = $2
	`, tenantID, purchaseOrderID).Scan(&reverseLink)
	if reverseLink > 0 {
		return nil // This PO was created from an SO, don't create another SO
	}

	// Create sales order in target organization.
	// Use the same sequential numbering scheme as user-created SOs
	// (S00001, S00002, ...) scoped by (tenant_id, organization_id),
	// so the intercompany-synced SO is indistinguishable from a
	// regular one in the list. The unique constraint is
	// (tenant_id, organization_id, order_number); we retry on
	// collision below if a concurrent INSERT grabbed the same number.
	soID := uuid.New()
	nextSONumber := func() string {
		var maxNum int
		s.db.QueryRow(`
			SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number, '[^0-9]', '', 'g') AS BIGINT)), 0)
			FROM sales_orders
			WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL
			  AND order_number ~ '^S[0-9]+$'`,
			tenantID, targetOrgID,
		).Scan(&maxNum)
		return fmt.Sprintf("S%05d", maxNum+1)
	}
	soNumber := nextSONumber()
	now := time.Now()

	notes := fmt.Sprintf("Auto-created from intercompany PO: %s", po.OrderNumber)
	paymentTerms := 30
	if po.PaymentTerms != nil {
		paymentTerms = *po.PaymentTerms
	}

	// Use target org's default warehouse, not source org's
	var targetWarehouseID *uuid.UUID
	var twID uuid.UUID
	if err := s.db.QueryRow(`
		SELECT id FROM warehouses
		WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL
		ORDER BY created_at ASC LIMIT 1
	`, tenantID, targetOrgID).Scan(&twID); err == nil {
		targetWarehouseID = &twID
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

	// Retry on number collision: another concurrent SO insert in the
	// target org may have grabbed the same MAX+1. Up to 5 attempts.
	for attempt := 0; attempt < 5; attempt++ {
		_, err = s.db.Exec(soInsertQuery,
			soID, tenantID, targetOrgID, soNumber, customerID,
			now, now.AddDate(0, 0, 7), po.CurrencyID, po.ExchangeRate,
			po.Subtotal, po.DiscountAmount, po.TaxAmount, po.ShippingAmount, po.TotalAmount,
			entity.OrderStatusDraft, entity.PaymentStatusUnpaid, paymentTerms,
			po.OrderNumber, notes, targetWarehouseID,
			now, now,
		)
		if err == nil {
			break
		}
		// Detect unique-constraint collision on order_number; recompute and retry.
		msg := err.Error()
		if attempt < 4 && (strings.Contains(msg, "sales_orders_tenant_org_order_number_key") ||
			strings.Contains(msg, "duplicate key") ||
			strings.Contains(msg, "unique constraint")) {
			soNumber = nextSONumber()
			continue
		}
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

			// Resolve buyer's product → seller's equivalent via
			// search_key. The SO must reference a product that
			// belongs to the seller (target) organisation,
			// otherwise inventory / pricing joins break.
			resolvedProductID := s.resolveCrossOrgProductID(tenantID, targetOrgID, productID)

			// When resolution succeeds, also replace the line
			// description with the target product's name. The
			// source description carries Company A's product name
			// (e.g. "АНКЕРНЫЙ БОЛТ С ГАЙКОЙ 10X100 ММ"); on
			// Company B's side we want B's own name (e.g.
			// "Анкер 10×100"). Falls back to the source
			// description when either no target product was
			// found or its name is empty.
			lineDescription := description
			if resolvedProductID != nil && (productID == nil || *resolvedProductID != *productID) {
				var targetName sql.NullString
				if err := s.db.QueryRow(
					`SELECT name FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
					*resolvedProductID, tenantID,
				).Scan(&targetName); err == nil && targetName.Valid && targetName.String != "" {
					lineDescription = targetName
				}
			}

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
			`, lineID, soID, lineNum, resolvedProductID, lineDescription,
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

// Helper methods

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
