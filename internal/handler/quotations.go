package handler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/listparams"
	"github.com/genixerp/genix-backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListQuotations returns all quotations for the tenant
func (h *Handler) ListQuotations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	status := c.Query("status")
	customerID := c.Query("customer_id")

	// Common list params: page, page_size, sort, order, search
	lp := listparams.Parse(c,
		[]string{"created_at", "quotation_number", "customer_name", "total_amount", "valid_until", "status"},
		"created_at")

	query := `
		SELECT id, tenant_id, quotation_number, customer_id, customer_name, contact_person, email,
			   valid_until, subtotal, discount_percent, discount_amount, tax_percent, tax_amount,
			   total_amount, status, converted_to_order, sales_order_id, notes, created_at, updated_at
		FROM sales_quotations
		WHERE tenant_id = $1 AND deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM sales_quotations WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" {
		argCount++
		query += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	if customerID != "" {
		argCount++
		query += fmt.Sprintf(" AND customer_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND customer_id = $%d", argCount)
		args = append(args, customerID)
	}

	// Search by quotation_number, customer_name
	if lp.Search != "" {
		argCount++
		pattern := "%" + strings.ToLower(lp.Search) + "%"
		query += fmt.Sprintf(" AND (LOWER(quotation_number) LIKE $%d OR LOWER(customer_name) LIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(quotation_number) LIKE $%d OR LOWER(customer_name) LIKE $%d)", argCount, argCount)
		args = append(args, pattern)
	}

	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count quotations", "error", err)
	}

	query += lp.OrderClause("") + fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, lp.PageSize, lp.Offset())

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list quotations", "error", err)
		response.InternalError(c, "Failed to list quotations")
		return
	}
	defer rows.Close()

	var quotations []*entity.QuotationResponse
	for rows.Next() {
		var q entity.Quotation
		var customerID, salesOrderID sql.NullString
		var contactPerson, email, notes, convertedToOrder sql.NullString
		var validUntil sql.NullTime

		err := rows.Scan(
			&q.ID, &q.TenantID, &q.QuotationNumber, &customerID, &q.CustomerName, &contactPerson, &email,
			&validUntil, &q.Subtotal, &q.DiscountPercent, &q.DiscountAmount, &q.TaxPercent, &q.TaxAmount,
			&q.TotalAmount, &q.Status, &convertedToOrder, &salesOrderID, &notes, &q.CreatedAt, &q.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan quotation", "error", err)
			continue
		}

		if customerID.Valid {
			cid, _ := uuid.Parse(customerID.String)
			q.CustomerID = &cid
		}
		if contactPerson.Valid {
			q.ContactPerson = &contactPerson.String
		}
		if email.Valid {
			q.Email = &email.String
		}
		if validUntil.Valid {
			q.ValidUntil = &validUntil.Time
		}
		if convertedToOrder.Valid {
			q.ConvertedToOrder = &convertedToOrder.String
		}
		if notes.Valid {
			q.Notes = &notes.String
		}

		// Load items
		q.Items = h.loadQuotationItems(q.ID)

		quotations = append(quotations, q.ToResponse())
	}

	if quotations == nil {
		quotations = []*entity.QuotationResponse{}
	}

	response.Paginated(c, quotations, lp.Page, lp.PageSize, total)
}

// GetQuotation returns a single quotation by ID
func (h *Handler) GetQuotation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	quotationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid quotation ID")
		return
	}
	if !h.salesOrgScopeOK(c, "sales_quotations", quotationID, tenantID) {
		response.NotFound(c, "Quotation")
		return
	}

	q, err := h.getQuotationByID(tenantID, quotationID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Quotation")
			return
		}
		response.InternalError(c, "Failed to get quotation")
		return
	}

	response.Success(c, q.ToResponse())
}

// CreateQuotation creates a new quotation
func (h *Handler) CreateQuotation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Get organization ID from middleware header
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var input entity.CreateQuotationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	quotationID := uuid.New()
	// MAX+1 over sales_quotations — the old COUNT(*) read the abandoned `quotations`
	// table (always 0 → every quote got QT00001 and the second one hit the unique
	// constraint), raced under concurrency, and reused numbers after soft-deletes.
	var qtNum int64
	h.db.QueryRow(`
		SELECT COALESCE(MAX(CAST(SUBSTRING(quotation_number FROM 3) AS BIGINT)), 0) + 1
		FROM sales_quotations
		WHERE tenant_id = $1 AND quotation_number ~ '^QT[0-9]+$'`,
		tenantID).Scan(&qtNum)
	if qtNum < 1 {
		qtNum = 1
	}
	quotationNumber := fmt.Sprintf("QT%05d", qtNum)

	// Calculate totals
	subtotal := 0.0
	for _, item := range input.Items {
		subtotal += item.Quantity * item.UnitPrice
	}
	discountAmount := subtotal * (input.DiscountPercent / 100)
	afterDiscount := subtotal - discountAmount
	taxAmount := afterDiscount * (input.TaxPercent / 100)
	totalAmount := afterDiscount + taxAmount

	// Parse customer ID
	var customerID *uuid.UUID
	if input.CustomerID != "" {
		cid, err := uuid.Parse(input.CustomerID)
		if err == nil {
			customerID = &cid
		}
	}

	// Parse valid until
	var validUntil *time.Time
	if input.ValidUntil != "" {
		t, err := time.Parse("2006-01-02", input.ValidUntil)
		if err == nil {
			validUntil = &t
		}
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Insert quotation
	_, err := h.db.Exec(`
		INSERT INTO sales_quotations (
			id, tenant_id, organization_id, quotation_number, customer_id, customer_name, contact_person, email,
			valid_until, subtotal, discount_percent, discount_amount, tax_percent, tax_amount,
			total_amount, status, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		quotationID, tenantID, orgIDPtr, quotationNumber, customerID, input.CustomerName, input.ContactPerson, input.Email,
		validUntil, subtotal, input.DiscountPercent, discountAmount, input.TaxPercent, taxAmount,
		totalAmount, "draft", input.Notes, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create quotation", "error", err)
		response.InternalError(c, "Failed to create quotation")
		return
	}

	// Insert items
	for i, item := range input.Items {
		itemID := uuid.New()
		var productID *uuid.UUID
		if item.ProductID != "" {
			pid, err := uuid.Parse(item.ProductID)
			if err == nil {
				productID = &pid
			}
		}
		itemTotal := item.Quantity * item.UnitPrice

		_, err := h.db.Exec(`
			INSERT INTO sales_quotation_items (
				id, quotation_id, product_id, product_name, quantity, unit_price, total, sort_order, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			itemID, quotationID, productID, item.ProductName, item.Quantity, item.UnitPrice, itemTotal, i, now,
		)
		if err != nil {
			h.log.Error("Failed to create quotation item", "error", err, "item_index", i)
		}
	}

	// Fetch and return the created quotation
	q, err := h.getQuotationByID(tenantID, quotationID)
	if err != nil {
		response.InternalError(c, "Quotation created but failed to retrieve")
		return
	}

	response.Created(c, q.ToResponse())
}

// UpdateQuotation updates an existing quotation
func (h *Handler) UpdateQuotation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	quotationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid quotation ID")
		return
	}
	if !h.salesOrgScopeOK(c, "sales_quotations", quotationID, tenantID) {
		response.NotFound(c, "Quotation")
		return
	}

	var input entity.UpdateQuotationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get existing quotation
	q, err := h.getQuotationByID(tenantID, quotationID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Quotation")
			return
		}
		response.InternalError(c, "Failed to get quotation")
		return
	}

	now := time.Now()

	// Build update query dynamically
	updates := []string{"updated_at = $1"}
	args := []interface{}{now}
	argCount := 1

	if input.CustomerName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("customer_name = $%d", argCount))
		args = append(args, *input.CustomerName)
	}
	if input.CustomerID != nil {
		argCount++
		if *input.CustomerID == "" {
			updates = append(updates, fmt.Sprintf("customer_id = $%d", argCount))
			args = append(args, nil)
		} else {
			updates = append(updates, fmt.Sprintf("customer_id = $%d", argCount))
			args = append(args, *input.CustomerID)
		}
	}
	if input.ContactPerson != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("contact_person = $%d", argCount))
		args = append(args, *input.ContactPerson)
	}
	if input.Email != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("email = $%d", argCount))
		args = append(args, *input.Email)
	}
	if input.ValidUntil != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("valid_until = $%d", argCount))
		if *input.ValidUntil == "" {
			args = append(args, nil)
		} else {
			t, _ := time.Parse("2006-01-02", *input.ValidUntil)
			args = append(args, t)
		}
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	// Recalculate totals if items provided
	if input.Items != nil {
		subtotal := 0.0
		for _, item := range *input.Items {
			subtotal += item.Quantity * item.UnitPrice
		}

		discountPercent := q.DiscountPercent
		taxPercent := q.TaxPercent
		if input.DiscountPercent != nil {
			discountPercent = *input.DiscountPercent
		}
		if input.TaxPercent != nil {
			taxPercent = *input.TaxPercent
		}

		discountAmount := subtotal * (discountPercent / 100)
		afterDiscount := subtotal - discountAmount
		taxAmount := afterDiscount * (taxPercent / 100)
		totalAmount := afterDiscount + taxAmount

		argCount++
		updates = append(updates, fmt.Sprintf("subtotal = $%d", argCount))
		args = append(args, subtotal)
		argCount++
		updates = append(updates, fmt.Sprintf("discount_percent = $%d", argCount))
		args = append(args, discountPercent)
		argCount++
		updates = append(updates, fmt.Sprintf("discount_amount = $%d", argCount))
		args = append(args, discountAmount)
		argCount++
		updates = append(updates, fmt.Sprintf("tax_percent = $%d", argCount))
		args = append(args, taxPercent)
		argCount++
		updates = append(updates, fmt.Sprintf("tax_amount = $%d", argCount))
		args = append(args, taxAmount)
		argCount++
		updates = append(updates, fmt.Sprintf("total_amount = $%d", argCount))
		args = append(args, totalAmount)

		// Delete old items and insert new ones
		h.db.Exec("DELETE FROM sales_quotation_items WHERE quotation_id = $1", quotationID)

		for i, item := range *input.Items {
			itemID := uuid.New()
			var productID *uuid.UUID
			if item.ProductID != "" {
				pid, err := uuid.Parse(item.ProductID)
				if err == nil {
					productID = &pid
				}
			}
			itemTotal := item.Quantity * item.UnitPrice

			h.db.Exec(`
				INSERT INTO sales_quotation_items (
					id, quotation_id, product_id, product_name, quantity, unit_price, total, sort_order, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				itemID, quotationID, productID, item.ProductName, item.Quantity, item.UnitPrice, itemTotal, i, now,
			)
		}
	} else if input.DiscountPercent != nil || input.TaxPercent != nil {
		// Recalculate if only discount/tax changed
		discountPercent := q.DiscountPercent
		taxPercent := q.TaxPercent
		if input.DiscountPercent != nil {
			discountPercent = *input.DiscountPercent
		}
		if input.TaxPercent != nil {
			taxPercent = *input.TaxPercent
		}

		discountAmount := q.Subtotal * (discountPercent / 100)
		afterDiscount := q.Subtotal - discountAmount
		taxAmount := afterDiscount * (taxPercent / 100)
		totalAmount := afterDiscount + taxAmount

		argCount++
		updates = append(updates, fmt.Sprintf("discount_percent = $%d", argCount))
		args = append(args, discountPercent)
		argCount++
		updates = append(updates, fmt.Sprintf("discount_amount = $%d", argCount))
		args = append(args, discountAmount)
		argCount++
		updates = append(updates, fmt.Sprintf("tax_percent = $%d", argCount))
		args = append(args, taxPercent)
		argCount++
		updates = append(updates, fmt.Sprintf("tax_amount = $%d", argCount))
		args = append(args, taxAmount)
		argCount++
		updates = append(updates, fmt.Sprintf("total_amount = $%d", argCount))
		args = append(args, totalAmount)
	}

	argCount++
	args = append(args, quotationID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE sales_quotations SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update quotation", "error", err)
		response.InternalError(c, "Failed to update quotation")
		return
	}

	// Fetch and return updated quotation
	q, err = h.getQuotationByID(tenantID, quotationID)
	if err != nil {
		response.InternalError(c, "Quotation updated but failed to retrieve")
		return
	}

	response.Success(c, q.ToResponse())
}

// DeleteQuotation soft deletes a quotation
func (h *Handler) DeleteQuotation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	quotationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid quotation ID")
		return
	}
	if !h.salesOrgScopeOK(c, "sales_quotations", quotationID, tenantID) {
		response.NotFound(c, "Quotation")
		return
	}

	now := time.Now()
	result, err := h.db.Exec(
		"UPDATE sales_quotations SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		now, quotationID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete quotation")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Quotation")
		return
	}

	response.NoContent(c)
}

// ConvertQuotationToOrder converts a quotation to a sales order
func (h *Handler) ConvertQuotationToOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	quotationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid quotation ID")
		return
	}
	if !h.salesOrgScopeOK(c, "sales_quotations", quotationID, tenantID) {
		response.NotFound(c, "Quotation")
		return
	}

	// Get quotation
	q, err := h.getQuotationByID(tenantID, quotationID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Quotation")
			return
		}
		response.InternalError(c, "Failed to get quotation")
		return
	}

	if q.ConvertedToOrder != nil || q.SalesOrderID != nil {
		converted := ""
		if q.ConvertedToOrder != nil {
			converted = *q.ConvertedToOrder
		}
		response.BadRequest(c, "Quotation already converted to order: "+converted)
		return
	}

	// Validate that quotation has a customer
	if q.CustomerID == nil {
		response.BadRequest(c, "Quotation must have a customer to convert to order")
		return
	}

	// Validate that quotation has items with valid products
	if len(q.Items) == 0 {
		response.BadRequest(c, "Quotation must have at least one item to convert to order")
		return
	}
	// All-or-nothing: silently skipping product-less items left header totals
	// disagreeing with the line sum (audit §8).
	for _, item := range q.Items {
		if item.ProductID == nil {
			response.BadRequest(c, fmt.Sprintf("Quotation item '%s' has no product — cannot convert", item.ProductName))
			return
		}
	}

	// The quotation's org (fallback: request header org). The old INSERT dropped
	// organization_id entirely, so converted orders vanished from org-scoped lists.
	var orderOrgID *uuid.UUID
	var qOrgID sql.NullString
	_ = h.db.QueryRow(`SELECT organization_id FROM sales_quotations WHERE id = $1 AND tenant_id = $2`,
		quotationID, tenantID).Scan(&qOrgID)
	if qOrgID.Valid && qOrgID.String != "" {
		if oid, oerr := uuid.Parse(qOrgID.String); oerr == nil {
			orderOrgID = &oid
		}
	}
	if orderOrgID == nil {
		if headerOrg, orgOk := middleware.GetOrganizationID(c); orgOk && headerOrg != uuid.Nil {
			ho := headerOrg
			orderOrgID = &ho
		}
	}

	now := time.Now()
	orderID := uuid.New()

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	tx, txErr := h.db.Begin()
	if txErr != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Same S00001 series as CreateSalesOrder — the old SO-YYYYMMDD-random format
	// never sorted into the sequence. No deleted_at filter: soft-deleted rows still
	// hold their numbers under the unique constraint.
	nextOrderNumber := func() string {
		var maxNum int64
		if orderOrgID != nil {
			tx.QueryRow(`
				SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number, '[^0-9]', '', 'g') AS BIGINT)), 0)
				FROM sales_orders
				WHERE tenant_id = $1 AND organization_id = $2 AND order_number ~ '^S[0-9]+$'`,
				tenantID, *orderOrgID).Scan(&maxNum)
		} else {
			tx.QueryRow(`
				SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number, '[^0-9]', '', 'g') AS BIGINT)), 0)
				FROM sales_orders
				WHERE tenant_id = $1 AND organization_id IS NULL AND order_number ~ '^S[0-9]+$'`,
				tenantID).Scan(&maxNum)
		}
		return fmt.Sprintf("S%05d", maxNum+1)
	}
	orderNumber := nextOrderNumber()

	// Create sales order (org-scoped). The idempotent claim below runs in the same
	// tx AFTER this insert (sales_order_id FK needs the row to exist); if the claim
	// loses a concurrent race, the rollback discards this order too.
	_, err = tx.Exec(`
		INSERT INTO sales_orders (
			id, tenant_id, organization_id, order_number, customer_id,
			order_date, subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
			status, payment_status, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		orderID, tenantID, orderOrgID, orderNumber, q.CustomerID,
		now, q.Subtotal, q.DiscountAmount, q.TaxAmount, 0, q.TotalAmount,
		"draft", "unpaid", q.Notes, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create sales order from quotation", "error", err)
		response.InternalError(c, "Failed to create sales order")
		return
	}

	// Copy items to sales order lines
	for i, item := range q.Items {
		lineTotal := item.Quantity * item.UnitPrice
		if _, lerr := tx.Exec(`
			INSERT INTO sales_order_lines (
				id, sales_order_id, line_number, product_id, description, quantity, unit_price, line_total, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			uuid.New(), orderID, i+1, item.ProductID, item.ProductName, item.Quantity, item.UnitPrice, lineTotal, now,
		); lerr != nil {
			h.log.Error("Failed to create sales order line", "error", lerr)
			response.InternalError(c, "Failed to create sales order lines")
			return
		}
	}

	// Idempotent claim: a concurrent convert of the same quotation sees 0 rows
	// here and rolls back its order.
	claimRes, claimErr := tx.Exec(`
		UPDATE sales_quotations SET status = 'accepted', converted_to_order = $1, sales_order_id = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5 AND sales_order_id IS NULL AND converted_to_order IS NULL`,
		orderNumber, orderID, now, quotationID, tenantID,
	)
	if claimErr != nil {
		h.log.Error("Failed to claim quotation for conversion", "error", claimErr)
		response.InternalError(c, "Failed to update quotation")
		return
	}
	if n, _ := claimRes.RowsAffected(); n == 0 {
		response.BadRequest(c, "Quotation already converted to order")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit conversion")
		return
	}

	// Return the quotation with updated status
	q, _ = h.getQuotationByID(tenantID, quotationID)
	response.Success(c, q.ToResponse())
}

// Helper functions

func (h *Handler) getQuotationByID(tenantID, quotationID uuid.UUID) (*entity.Quotation, error) {
	var q entity.Quotation
	var customerID, salesOrderID sql.NullString
	var contactPerson, email, notes, convertedToOrder sql.NullString
	var validUntil sql.NullTime

	err := h.db.QueryRow(`
		SELECT id, tenant_id, quotation_number, customer_id, customer_name, contact_person, email,
			   valid_until, subtotal, discount_percent, discount_amount, tax_percent, tax_amount,
			   total_amount, status, converted_to_order, sales_order_id, notes, created_at, updated_at
		FROM sales_quotations
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		quotationID, tenantID,
	).Scan(
		&q.ID, &q.TenantID, &q.QuotationNumber, &customerID, &q.CustomerName, &contactPerson, &email,
		&validUntil, &q.Subtotal, &q.DiscountPercent, &q.DiscountAmount, &q.TaxPercent, &q.TaxAmount,
		&q.TotalAmount, &q.Status, &convertedToOrder, &salesOrderID, &notes, &q.CreatedAt, &q.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if customerID.Valid {
		cid, _ := uuid.Parse(customerID.String)
		q.CustomerID = &cid
	}
	if contactPerson.Valid {
		q.ContactPerson = &contactPerson.String
	}
	if email.Valid {
		q.Email = &email.String
	}
	if validUntil.Valid {
		q.ValidUntil = &validUntil.Time
	}
	if convertedToOrder.Valid {
		q.ConvertedToOrder = &convertedToOrder.String
	}
	if notes.Valid {
		q.Notes = &notes.String
	}

	q.Items = h.loadQuotationItems(q.ID)

	return &q, nil
}

func (h *Handler) loadQuotationItems(quotationID uuid.UUID) []entity.QuotationItem {
	rows, err := h.db.Query(`
		SELECT id, quotation_id, product_id, product_name, quantity, unit_price, total, sort_order, created_at
		FROM sales_quotation_items
		WHERE quotation_id = $1
		ORDER BY sort_order`,
		quotationID,
	)
	if err != nil {
		return []entity.QuotationItem{}
	}
	defer rows.Close()

	var items []entity.QuotationItem
	for rows.Next() {
		var item entity.QuotationItem
		var productID sql.NullString

		err := rows.Scan(
			&item.ID, &item.QuotationID, &productID, &item.ProductName,
			&item.Quantity, &item.UnitPrice, &item.Total, &item.SortOrder, &item.CreatedAt,
		)
		if err != nil {
			continue
		}

		if productID.Valid {
			pid, _ := uuid.Parse(productID.String)
			item.ProductID = &pid
		}

		items = append(items, item)
	}

	if items == nil {
		return []entity.QuotationItem{}
	}

	return items
}
