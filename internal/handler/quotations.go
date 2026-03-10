package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
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

	query := `
		SELECT id, tenant_id, quotation_number, customer_id, customer_name, contact_person, email,
			   valid_until, subtotal, discount_percent, discount_amount, tax_percent, tax_amount,
			   total_amount, status, converted_to_order, sales_order_id, notes, created_at, updated_at
		FROM sales_quotations
		WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" {
		argCount++
		query += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	if customerID != "" {
		argCount++
		query += fmt.Sprintf(" AND customer_id = $%d", argCount)
		args = append(args, customerID)
	}

	query += " ORDER BY created_at DESC"

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

	response.Success(c, quotations)
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
	var qtCount int
	h.db.QueryRow("SELECT COUNT(*) FROM quotations WHERE tenant_id = $1", tenantID).Scan(&qtCount)
	quotationNumber := fmt.Sprintf("QT%05d", qtCount+1)

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

	if q.Status == "accepted" && q.ConvertedToOrder != nil {
		response.BadRequest(c, "Quotation already converted to order: "+*q.ConvertedToOrder)
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

	now := time.Now()
	orderID := uuid.New()
	orderNumber := fmt.Sprintf("SO-%s-%s", now.Format("20060102"), uuid.New().String()[:6])

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Create sales order
	_, err = h.db.Exec(`
		INSERT INTO sales_orders (
			id, tenant_id, order_number, customer_id,
			order_date, subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
			status, payment_status, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		orderID, tenantID, orderNumber, q.CustomerID,
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
		lineID := uuid.New()
		// Skip items without product_id since sales_order_lines requires it
		if item.ProductID == nil {
			h.log.Warn("Skipping quotation item without product_id", "item_name", item.ProductName)
			continue
		}
		lineTotal := item.Quantity * item.UnitPrice
		_, err := h.db.Exec(`
			INSERT INTO sales_order_lines (
				id, sales_order_id, line_number, product_id, description, quantity, unit_price, line_total, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			lineID, orderID, i+1, item.ProductID, item.ProductName, item.Quantity, item.UnitPrice, lineTotal, now,
		)
		if err != nil {
			h.log.Error("Failed to create sales order line", "error", err)
		}
	}

	// Update quotation status
	_, err = h.db.Exec(`
		UPDATE sales_quotations SET status = 'accepted', converted_to_order = $1, sales_order_id = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5`,
		orderNumber, orderID, now, quotationID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update quotation status", "error", err)
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
