package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Purchase Order Status constants
const (
	POStatusDraft           = "draft"
	POStatusPendingApproval = "pending_approval"
	POStatusApproved        = "approved"
	POStatusOrdered         = "ordered"
	POStatusPartial         = "partial"
	POStatusReceived        = "received"
	POStatusCancelled       = "cancelled"
)

// ListPurchaseOrders returns paginated list of purchase orders
func (h *Handler) ListPurchaseOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	// Pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Build query with filters
	baseQuery := `
		SELECT id, tenant_id, organization_id, order_number, vendor_id, contact_person_id,
			   order_date, expected_date, currency_id, exchange_rate, subtotal, discount_amount,
			   tax_amount, shipping_amount, total_amount, status, payment_status, payment_terms,
			   reference, vendor_reference, notes, internal_notes, warehouse_id,
			   requested_by, approved_by, approved_at, created_by, created_at, updated_at
		FROM purchase_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM purchase_orders WHERE tenant_id = $1 AND deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by payment_status
	if paymentStatus := c.Query("payment_status"); paymentStatus != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND payment_status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND payment_status = $%d", argCount)
		args = append(args, paymentStatus)
	}

	// Filter by vendor_id
	if vendorID := c.Query("vendor_id"); vendorID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND vendor_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND vendor_id = $%d", argCount)
		args = append(args, vendorID)
	}

	// Filter by date range
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND order_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND order_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND order_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND order_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(order_number) LIKE $%d OR LOWER(reference) LIKE $%d OR LOWER(vendor_reference) LIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(order_number) LIKE $%d OR LOWER(reference) LIKE $%d OR LOWER(vendor_reference) LIKE $%d)", argCount, argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count purchase orders")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		response.InternalError(c, "Failed to fetch purchase orders: "+err.Error())
		return
	}
	defer rows.Close()

	var orders []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan, vendorID uuid.UUID
		var organizationID, contactPersonID, currencyID, warehouseID, requestedBy, approvedBy, createdBy sql.NullString
		var orderNumber string
		var orderDate time.Time
		var expectedDate, approvedAt sql.NullTime
		var exchangeRate, subtotal, discountAmount, taxAmount, shippingAmount, totalAmount float64
		var status, paymentStatus, reference, vendorReference, notes, internalNotes sql.NullString
		var paymentTerms int
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &tenantIDScan, &organizationID, &orderNumber, &vendorID, &contactPersonID,
			&orderDate, &expectedDate, &currencyID, &exchangeRate, &subtotal, &discountAmount,
			&taxAmount, &shippingAmount, &totalAmount, &status, &paymentStatus, &paymentTerms,
			&reference, &vendorReference, &notes, &internalNotes, &warehouseID,
			&requestedBy, &approvedBy, &approvedAt, &createdBy, &createdAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		order := map[string]interface{}{
			"id":              id.String(),
			"tenant_id":       tenantIDScan.String(),
			"order_number":    orderNumber,
			"vendor_id":       vendorID.String(),
			"order_date":      orderDate.Format("2006-01-02"),
			"exchange_rate":   exchangeRate,
			"subtotal":        subtotal,
			"discount_amount": discountAmount,
			"tax_amount":      taxAmount,
			"shipping_amount": shippingAmount,
			"total_amount":    totalAmount,
			"status":          status.String,
			"payment_status":  paymentStatus.String,
			"payment_terms":   paymentTerms,
			"created_at":      createdAt,
			"updated_at":      updatedAt,
		}

		if organizationID.Valid {
			order["organization_id"] = organizationID.String
		}
		if contactPersonID.Valid {
			order["contact_person_id"] = contactPersonID.String
		}
		if expectedDate.Valid {
			order["expected_date"] = expectedDate.Time.Format("2006-01-02")
		}
		if currencyID.Valid {
			order["currency_id"] = currencyID.String
		}
		if reference.Valid {
			order["reference"] = reference.String
		}
		if vendorReference.Valid {
			order["vendor_reference"] = vendorReference.String
		}
		if notes.Valid {
			order["notes"] = notes.String
		}
		if warehouseID.Valid {
			order["warehouse_id"] = warehouseID.String
		}
		if approvedBy.Valid {
			order["approved_by"] = approvedBy.String
		}
		if approvedAt.Valid {
			order["approved_at"] = approvedAt.Time
		}

		orders = append(orders, order)
	}

	response.Paginated(c, orders, page, pageSize, total)
}

// CreatePurchaseOrder creates a new purchase order
func (h *Handler) CreatePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input struct {
		VendorID        string `json:"vendor_id" binding:"required"`
		ContactPersonID string `json:"contact_person_id,omitempty"`
		OrderDate       string `json:"order_date" binding:"required"`
		ExpectedDate    string `json:"expected_date,omitempty"`
		CurrencyID      string `json:"currency_id,omitempty"`
		ShippingAmount  float64 `json:"shipping_amount,omitempty"`
		PaymentTerms    int     `json:"payment_terms,omitempty"`
		Reference       string  `json:"reference,omitempty"`
		VendorReference string  `json:"vendor_reference,omitempty"`
		Notes           string  `json:"notes,omitempty"`
		InternalNotes   string  `json:"internal_notes,omitempty"`
		WarehouseID     string  `json:"warehouse_id,omitempty"`
		Lines           []struct {
			ProductID    string  `json:"product_id" binding:"required"`
			Description  string  `json:"description,omitempty"`
			Quantity     float64 `json:"quantity" binding:"required,gt=0"`
			UnitID       string  `json:"unit_id,omitempty"`
			UnitPrice    float64 `json:"unit_price" binding:"required,gte=0"`
			DiscountAmount float64 `json:"discount_amount,omitempty"`
			TaxID        string  `json:"tax_id,omitempty"`
			WarehouseID  string  `json:"warehouse_id,omitempty"`
			Notes        string  `json:"notes,omitempty"`
		} `json:"lines" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Parse vendor ID
	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor_id")
		return
	}

	// Parse order date
	orderDate, err := time.Parse("2006-01-02", input.OrderDate)
	if err != nil {
		response.BadRequest(c, "Invalid order_date format, expected YYYY-MM-DD")
		return
	}

	// Generate order number
	orderNumber := "PO-" + time.Now().Format("20060102") + "-" + uuid.New().String()[:6]

	orderID := uuid.New()
	now := time.Now()

	// Calculate totals from lines
	var subtotal, taxAmount, discountAmount float64
	for _, line := range input.Lines {
		lineTotal := line.Quantity * line.UnitPrice
		subtotal += lineTotal - line.DiscountAmount
		discountAmount += line.DiscountAmount
	}

	totalAmount := subtotal + taxAmount + input.ShippingAmount

	// Parse expected date if provided
	var expectedDate *time.Time
	if input.ExpectedDate != "" {
		ed, err := time.Parse("2006-01-02", input.ExpectedDate)
		if err == nil {
			expectedDate = &ed
		}
	}

	// Parse optional UUIDs
	var contactPersonID, currencyID, warehouseID *uuid.UUID
	if input.ContactPersonID != "" {
		id, _ := uuid.Parse(input.ContactPersonID)
		contactPersonID = &id
	}
	if input.CurrencyID != "" {
		id, _ := uuid.Parse(input.CurrencyID)
		currencyID = &id
	}
	if input.WarehouseID != "" {
		id, _ := uuid.Parse(input.WarehouseID)
		warehouseID = &id
	}

	var createdBy, requestedBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
		requestedBy = &userID
	}

	// Insert purchase order
	query := `
		INSERT INTO purchase_orders (
			id, tenant_id, order_number, vendor_id, contact_person_id,
			order_date, expected_date, currency_id, exchange_rate, subtotal, discount_amount,
			tax_amount, shipping_amount, total_amount, status, payment_status, payment_terms,
			reference, vendor_reference, notes, internal_notes, warehouse_id,
			requested_by, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)`

	_, err = h.db.Exec(query,
		orderID, tenantID, orderNumber, vendorID, contactPersonID,
		orderDate, expectedDate, currencyID, 1.0, subtotal, discountAmount,
		taxAmount, input.ShippingAmount, totalAmount, POStatusDraft, "unpaid", input.PaymentTerms,
		input.Reference, input.VendorReference, input.Notes, input.InternalNotes, warehouseID,
		requestedBy, createdBy, now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create purchase order: "+err.Error())
		return
	}

	// Insert order lines
	for i, line := range input.Lines {
		lineID := uuid.New()
		productID, _ := uuid.Parse(line.ProductID)

		lineTotal := line.Quantity * line.UnitPrice - line.DiscountAmount

		var unitID, taxID, lineWarehouseID *uuid.UUID
		if line.UnitID != "" {
			id, _ := uuid.Parse(line.UnitID)
			unitID = &id
		}
		if line.TaxID != "" {
			id, _ := uuid.Parse(line.TaxID)
			taxID = &id
		}
		if line.WarehouseID != "" {
			id, _ := uuid.Parse(line.WarehouseID)
			lineWarehouseID = &id
		}

		lineQuery := `
			INSERT INTO purchase_order_lines (
				id, purchase_order_id, line_number, product_id, description,
				quantity, unit_id, unit_price, discount_amount,
				tax_id, tax_amount, line_total, quantity_received, quantity_invoiced,
				warehouse_id, notes, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`

		h.db.Exec(lineQuery,
			lineID, orderID, i+1, productID, line.Description,
			line.Quantity, unitID, line.UnitPrice, line.DiscountAmount,
			taxID, 0.0, lineTotal, 0.0, 0.0,
			lineWarehouseID, line.Notes, now, now,
		)
	}

	// Return created order
	orderResponse := map[string]interface{}{
		"id":              orderID.String(),
		"tenant_id":       tenantID.String(),
		"order_number":    orderNumber,
		"vendor_id":       vendorID.String(),
		"order_date":      orderDate.Format("2006-01-02"),
		"subtotal":        subtotal,
		"discount_amount": discountAmount,
		"tax_amount":      taxAmount,
		"shipping_amount": input.ShippingAmount,
		"total_amount":    totalAmount,
		"status":          POStatusDraft,
		"payment_status":  "unpaid",
		"payment_terms":   input.PaymentTerms,
		"created_at":      now,
	}

	if input.Reference != "" {
		orderResponse["reference"] = input.Reference
	}
	if input.VendorReference != "" {
		orderResponse["vendor_reference"] = input.VendorReference
	}
	if input.Notes != "" {
		orderResponse["notes"] = input.Notes
	}
	if expectedDate != nil {
		orderResponse["expected_date"] = expectedDate.Format("2006-01-02")
	}

	response.Created(c, orderResponse)
}

// GetPurchaseOrder returns a single purchase order by ID
func (h *Handler) GetPurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Get order
	query := `
		SELECT id, tenant_id, organization_id, order_number, vendor_id, contact_person_id,
			   order_date, expected_date, currency_id, exchange_rate, subtotal, discount_amount,
			   tax_amount, shipping_amount, total_amount, status, payment_status, payment_terms,
			   reference, vendor_reference, notes, internal_notes, warehouse_id,
			   requested_by, approved_by, approved_at, created_by, created_at, updated_at
		FROM purchase_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`

	var id, tenantIDScan, vendorID uuid.UUID
	var organizationID, contactPersonID, currencyID, warehouseID, requestedBy, approvedBy, createdBy sql.NullString
	var orderNumber string
	var orderDate time.Time
	var expectedDate, approvedAt sql.NullTime
	var exchangeRate, subtotal, discountAmount, taxAmount, shippingAmount, totalAmount float64
	var status, paymentStatus, reference, vendorReference, notes, internalNotes sql.NullString
	var paymentTerms int
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(query, orderID, tenantID).Scan(
		&id, &tenantIDScan, &organizationID, &orderNumber, &vendorID, &contactPersonID,
		&orderDate, &expectedDate, &currencyID, &exchangeRate, &subtotal, &discountAmount,
		&taxAmount, &shippingAmount, &totalAmount, &status, &paymentStatus, &paymentTerms,
		&reference, &vendorReference, &notes, &internalNotes, &warehouseID,
		&requestedBy, &approvedBy, &approvedAt, &createdBy, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch purchase order: "+err.Error())
		return
	}

	order := map[string]interface{}{
		"id":              id.String(),
		"tenant_id":       tenantIDScan.String(),
		"order_number":    orderNumber,
		"vendor_id":       vendorID.String(),
		"order_date":      orderDate.Format("2006-01-02"),
		"exchange_rate":   exchangeRate,
		"subtotal":        subtotal,
		"discount_amount": discountAmount,
		"tax_amount":      taxAmount,
		"shipping_amount": shippingAmount,
		"total_amount":    totalAmount,
		"status":          status.String,
		"payment_status":  paymentStatus.String,
		"payment_terms":   paymentTerms,
		"created_at":      createdAt,
		"updated_at":      updatedAt,
	}

	if organizationID.Valid {
		order["organization_id"] = organizationID.String
	}
	if contactPersonID.Valid {
		order["contact_person_id"] = contactPersonID.String
	}
	if expectedDate.Valid {
		order["expected_date"] = expectedDate.Time.Format("2006-01-02")
	}
	if currencyID.Valid {
		order["currency_id"] = currencyID.String
	}
	if reference.Valid {
		order["reference"] = reference.String
	}
	if vendorReference.Valid {
		order["vendor_reference"] = vendorReference.String
	}
	if notes.Valid {
		order["notes"] = notes.String
	}
	if internalNotes.Valid {
		order["internal_notes"] = internalNotes.String
	}
	if warehouseID.Valid {
		order["warehouse_id"] = warehouseID.String
	}
	if requestedBy.Valid {
		order["requested_by"] = requestedBy.String
	}
	if approvedBy.Valid {
		order["approved_by"] = approvedBy.String
	}
	if approvedAt.Valid {
		order["approved_at"] = approvedAt.Time
	}

	// Get order lines
	linesQuery := `
		SELECT id, line_number, product_id, description, quantity, unit_id, unit_price,
			   discount_amount, tax_id, tax_amount, line_total,
			   quantity_received, quantity_invoiced, warehouse_id, notes
		FROM purchase_order_lines
		WHERE purchase_order_id = $1
		ORDER BY line_number`

	linesRows, err := h.db.Query(linesQuery, orderID)
	if err == nil {
		defer linesRows.Close()
		var lines []map[string]interface{}
		for linesRows.Next() {
			var lineID, productID uuid.UUID
			var lineNumber int
			var description, lineNotes sql.NullString
			var quantity, unitPrice, lineDiscountAmount, lineTaxAmount, lineTotal, qtyReceived, qtyInvoiced float64
			var unitID, taxID, lineWarehouseID sql.NullString

			err := linesRows.Scan(
				&lineID, &lineNumber, &productID, &description, &quantity, &unitID, &unitPrice,
				&lineDiscountAmount, &taxID, &lineTaxAmount, &lineTotal,
				&qtyReceived, &qtyInvoiced, &lineWarehouseID, &lineNotes,
			)
			if err != nil {
				continue
			}

			line := map[string]interface{}{
				"id":                lineID.String(),
				"line_number":       lineNumber,
				"product_id":        productID.String(),
				"quantity":          quantity,
				"unit_price":        unitPrice,
				"discount_amount":   lineDiscountAmount,
				"tax_amount":        lineTaxAmount,
				"line_total":        lineTotal,
				"quantity_received": qtyReceived,
				"quantity_invoiced": qtyInvoiced,
			}

			if description.Valid {
				line["description"] = description.String
			}
			if unitID.Valid {
				line["unit_id"] = unitID.String
			}
			if taxID.Valid {
				line["tax_id"] = taxID.String
			}
			if lineWarehouseID.Valid {
				line["warehouse_id"] = lineWarehouseID.String
			}
			if lineNotes.Valid {
				line["notes"] = lineNotes.String
			}

			lines = append(lines, line)
		}
		order["lines"] = lines
	}

	response.Success(c, order)
}

// UpdatePurchaseOrder updates an existing purchase order
func (h *Handler) UpdatePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	var input struct {
		ExpectedDate    *string  `json:"expected_date,omitempty"`
		ShippingAmount  *float64 `json:"shipping_amount,omitempty"`
		PaymentTerms    *int     `json:"payment_terms,omitempty"`
		Reference       *string  `json:"reference,omitempty"`
		VendorReference *string  `json:"vendor_reference,omitempty"`
		Notes           *string  `json:"notes,omitempty"`
		InternalNotes   *string  `json:"internal_notes,omitempty"`
		WarehouseID     *string  `json:"warehouse_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if order exists and is in draft status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", orderID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if currentStatus != POStatusDraft {
		response.BadRequest(c, "Can only update orders in draft status")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.ExpectedDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("expected_date = $%d", argCount))
		if *input.ExpectedDate != "" {
			ed, _ := time.Parse("2006-01-02", *input.ExpectedDate)
			args = append(args, ed)
		} else {
			args = append(args, nil)
		}
	}
	if input.ShippingAmount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("shipping_amount = $%d", argCount))
		args = append(args, *input.ShippingAmount)
	}
	if input.PaymentTerms != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("payment_terms = $%d", argCount))
		args = append(args, *input.PaymentTerms)
	}
	if input.Reference != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("reference = $%d", argCount))
		args = append(args, *input.Reference)
	}
	if input.VendorReference != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("vendor_reference = $%d", argCount))
		args = append(args, *input.VendorReference)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.InternalNotes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("internal_notes = $%d", argCount))
		args = append(args, *input.InternalNotes)
	}
	if input.WarehouseID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("warehouse_id = $%d", argCount))
		if *input.WarehouseID != "" {
			wid, _ := uuid.Parse(*input.WarehouseID)
			args = append(args, wid)
		} else {
			args = append(args, nil)
		}
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, orderID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE purchase_orders SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to update purchase order: "+err.Error())
		return
	}

	// Fetch and return updated order
	h.GetPurchaseOrder(c)
}

// DeletePurchaseOrder soft deletes a purchase order
func (h *Handler) DeletePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Check if order is in draft status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", orderID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if currentStatus != POStatusDraft {
		response.BadRequest(c, "Can only delete orders in draft status")
		return
	}

	result, err := h.db.Exec(
		"UPDATE purchase_orders SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), orderID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete purchase order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Purchase order")
		return
	}

	response.NoContent(c)
}

// ApprovePurchaseOrder approves a purchase order
func (h *Handler) ApprovePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", orderID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if currentStatus != POStatusDraft && currentStatus != POStatusPendingApproval {
		response.BadRequest(c, "Can only approve orders in draft or pending approval status")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE purchase_orders SET status = $1, approved_by = $2, approved_at = $3, updated_at = $4 WHERE id = $5 AND tenant_id = $6",
		POStatusApproved, userID, now, now, orderID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to approve purchase order")
		return
	}

	h.GetPurchaseOrder(c)
}

// ReceivePurchaseOrder marks items as received on a purchase order
func (h *Handler) ReceivePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	var input struct {
		Lines []struct {
			LineID           string  `json:"line_id" binding:"required"`
			QuantityReceived float64 `json:"quantity_received" binding:"required,gte=0"`
		} `json:"lines" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check current status - must be approved or ordered or partial
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", orderID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if currentStatus != POStatusApproved && currentStatus != POStatusOrdered && currentStatus != POStatusPartial {
		response.BadRequest(c, "Can only receive items for approved, ordered, or partial orders")
		return
	}

	now := time.Now()

	// Update line quantities
	for _, line := range input.Lines {
		lineID, err := uuid.Parse(line.LineID)
		if err != nil {
			continue
		}

		h.db.Exec(
			"UPDATE purchase_order_lines SET quantity_received = quantity_received + $1, updated_at = $2 WHERE id = $3 AND purchase_order_id = $4",
			line.QuantityReceived, now, lineID, orderID,
		)
	}

	// Check if all items are received
	var totalQty, totalReceived float64
	err = h.db.QueryRow(`
		SELECT COALESCE(SUM(quantity), 0), COALESCE(SUM(quantity_received), 0)
		FROM purchase_order_lines WHERE purchase_order_id = $1`, orderID).Scan(&totalQty, &totalReceived)
	if err == nil {
		newStatus := POStatusPartial
		if totalReceived >= totalQty {
			newStatus = POStatusReceived
		}
		h.db.Exec(
			"UPDATE purchase_orders SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
			newStatus, now, orderID, tenantID,
		)
	}

	h.GetPurchaseOrder(c)
}

package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// PURCHASE ORDER HANDLERS
// =====================================================

// ListPurchaseOrders returns a paginated list of purchase orders
func (h *Handler) ListPurchaseOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	status := c.Query("status")
	vendorID := c.Query("vendor_id")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// Build query
	baseQuery := `
		SELECT po.id, po.order_number, po.vendor_id, c.name as vendor_name,
			   po.order_date, po.expected_date, po.subtotal, po.discount_amount,
			   po.tax_amount, po.shipping_amount, po.total_amount, po.status,
			   po.payment_status, po.payment_terms, po.vendor_reference, po.notes,
			   po.approved_at, po.created_at, po.updated_at
		FROM purchase_orders po
		LEFT JOIN contacts c ON po.vendor_id = c.id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM purchase_orders po WHERE po.tenant_id = $1 AND po.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.status = $%d", argCount)
		args = append(args, status)
	}

	if vendorID != "" {
		if vid, err := uuid.Parse(vendorID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND po.vendor_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND po.vendor_id = $%d", argCount)
			args = append(args, vid)
		}
	}

	if dateFrom != "" {
		if df, err := time.Parse("2006-01-02", dateFrom); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND po.order_date >= $%d", argCount)
			countQuery += fmt.Sprintf(" AND po.order_date >= $%d", argCount)
			args = append(args, df)
		}
	}

	if dateTo != "" {
		if dt, err := time.Parse("2006-01-02", dateTo); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND po.order_date <= $%d", argCount)
			countQuery += fmt.Sprintf(" AND po.order_date <= $%d", argCount)
			args = append(args, dt)
		}
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (po.order_number ILIKE $%d OR c.name ILIKE $%d OR po.vendor_reference ILIKE $%d)", argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count purchase orders", "error", err)
		response.InternalError(c, "Failed to list purchase orders")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY po.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list purchase orders", "error", err)
		response.InternalError(c, "Failed to list purchase orders")
		return
	}
	defer rows.Close()

	orders := make([]*entity.PurchaseOrderResponse, 0)
	for rows.Next() {
		var po entity.PurchaseOrderResponse
		var expectedDate sql.NullTime
		var paymentTerms, vendorReference, notes sql.NullString
		var approvedAt sql.NullTime

		err := rows.Scan(
			&po.ID, &po.OrderNumber, &po.VendorID, &po.VendorName,
			&po.OrderDate, &expectedDate, &po.Subtotal, &po.DiscountAmount,
			&po.TaxAmount, &po.ShippingAmount, &po.TotalAmount, &po.Status,
			&po.PaymentStatus, &paymentTerms, &vendorReference, &notes,
			&approvedAt, &po.CreatedAt, &po.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan purchase order", "error", err)
			continue
		}

		if expectedDate.Valid {
			po.ExpectedDate = &expectedDate.Time
		}
		if paymentTerms.Valid {
			po.PaymentTerms = &paymentTerms.String
		}
		if vendorReference.Valid {
			po.VendorReference = &vendorReference.String
		}
		if notes.Valid {
			po.Notes = &notes.String
		}
		if approvedAt.Valid {
			po.ApprovedAt = &approvedAt.Time
		}

		orders = append(orders, &po)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, orders, pagination)
}

// CreatePurchaseOrder creates a new purchase order
func (h *Handler) CreatePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreatePurchaseOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse vendor ID
	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor ID")
		return
	}

	// Verify vendor exists and belongs to tenant
	var vendorName string
	err = h.db.QueryRow("SELECT name FROM contacts WHERE id = $1 AND tenant_id = $2 AND type IN ('vendor', 'both') AND deleted_at IS NULL", vendorID, tenantID).Scan(&vendorName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Vendor")
		return
	}
	if err != nil {
		h.log.Error("Failed to verify vendor", "error", err)
		response.InternalError(c, "Failed to create purchase order")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Generate order number
	orderNumber := input.OrderNumber
	if orderNumber == "" {
		var count int
		h.db.QueryRow("SELECT COUNT(*) FROM purchase_orders WHERE tenant_id = $1", tenantID).Scan(&count)
		orderNumber = fmt.Sprintf("PO-%s-%04d", now.Format("2006"), count+1)
	}

	// Parse order date
	orderDate := now
	if input.OrderDate != "" {
		if od, err := time.Parse("2006-01-02", input.OrderDate); err == nil {
			orderDate = od
		}
	}

	// Parse expected date
	var expectedDate *time.Time
	if input.ExpectedDate != "" {
		if ed, err := time.Parse("2006-01-02", input.ExpectedDate); err == nil {
			expectedDate = &ed
		}
	}

	// Parse optional UUIDs
	var contactPersonID, currencyID, warehouseID *uuid.UUID
	if input.ContactPersonID != "" {
		if cpid, err := uuid.Parse(input.ContactPersonID); err == nil {
			contactPersonID = &cpid
		}
	}
	if input.CurrencyID != "" {
		if cid, err := uuid.Parse(input.CurrencyID); err == nil {
			currencyID = &cid
		}
	}
	if input.WarehouseID != "" {
		if wid, err := uuid.Parse(input.WarehouseID); err == nil {
			warehouseID = &wid
		}
	}

	exchangeRate := input.ExchangeRate
	if exchangeRate == 0 {
		exchangeRate = 1.0
	}

	// Calculate totals
	var subtotal, discountTotal, taxTotal float64
	for _, line := range input.Lines {
		lineSubtotal := line.Quantity * line.UnitPrice
		lineDiscount := line.DiscountAmount
		lineTax := (lineSubtotal - lineDiscount) * line.TaxPercent / 100
		subtotal += lineSubtotal
		discountTotal += lineDiscount
		taxTotal += lineTax
	}

	totalAmount := subtotal - discountTotal + taxTotal + input.ShippingAmount

	// Prepare optional strings
	var paymentTerms, vendorReference, notes, internalNotes *string
	if input.PaymentTerms != "" {
		paymentTerms = &input.PaymentTerms
	}
	if input.VendorReference != "" {
		vendorReference = &input.VendorReference
	}
	if input.Notes != "" {
		notes = &input.Notes
	}
	if input.InternalNotes != "" {
		internalNotes = &input.InternalNotes
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create purchase order")
		return
	}
	defer tx.Rollback()

	// Insert purchase order
	query := `
		INSERT INTO purchase_orders (
			id, tenant_id, order_number, vendor_id, contact_person_id,
			order_date, expected_date, currency_id, exchange_rate,
			subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
			status, payment_status, payment_terms, vendor_reference,
			notes, internal_notes, warehouse_id, requested_by,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
	`

	_, err = tx.Exec(query,
		id, tenantID, orderNumber, vendorID, contactPersonID,
		orderDate, expectedDate, currencyID, exchangeRate,
		subtotal, discountTotal, taxTotal, input.ShippingAmount, totalAmount,
		entity.POStatusDraft, entity.PaymentStatusUnpaid, paymentTerms, vendorReference,
		notes, internalNotes, warehouseID, userID,
		now, now,
	)
	if err != nil {
		h.log.Error("Failed to insert purchase order", "error", err)
		response.InternalError(c, "Failed to create purchase order")
		return
	}

	// Insert line items
	lineQuery := `
		INSERT INTO purchase_order_lines (
			id, purchase_order_id, line_number, product_id, description,
			quantity, unit_id, unit_price, discount_amount, tax_id, tax_amount,
			line_total, quantity_received, quantity_invoiced, warehouse_id, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`

	lines := make([]entity.PurchaseOrderLine, 0, len(input.Lines))
	for i, line := range input.Lines {
		lineID := uuid.New()

		var productID, unitID, taxID, lineWarehouseID *uuid.UUID
		if line.ProductID != "" {
			if pid, err := uuid.Parse(line.ProductID); err == nil {
				productID = &pid
			}
		}
		if line.UnitID != "" {
			if uid, err := uuid.Parse(line.UnitID); err == nil {
				unitID = &uid
			}
		}
		if line.TaxID != "" {
			if tid, err := uuid.Parse(line.TaxID); err == nil {
				taxID = &tid
			}
		}
		if line.WarehouseID != "" {
			if wid, err := uuid.Parse(line.WarehouseID); err == nil {
				lineWarehouseID = &wid
			}
		} else if warehouseID != nil {
			lineWarehouseID = warehouseID
		}

		lineSubtotal := line.Quantity * line.UnitPrice
		lineTax := (lineSubtotal - line.DiscountAmount) * line.TaxPercent / 100
		lineTotal := lineSubtotal - line.DiscountAmount + lineTax

		var lineNotes *string
		if line.Notes != "" {
			lineNotes = &line.Notes
		}

		_, err = tx.Exec(lineQuery,
			lineID, id, i+1, productID, line.Description,
			line.Quantity, unitID, line.UnitPrice, line.DiscountAmount, taxID, lineTax,
			lineTotal, 0, 0, lineWarehouseID, lineNotes,
			now, now,
		)
		if err != nil {
			h.log.Error("Failed to insert purchase order line", "error", err)
			response.InternalError(c, "Failed to create purchase order")
			return
		}

		lines = append(lines, entity.PurchaseOrderLine{
			ID:              lineID,
			PurchaseOrderID: id,
			LineNumber:      i + 1,
			ProductID:       productID,
			Description:     line.Description,
			Quantity:        line.Quantity,
			UnitID:          unitID,
			UnitPrice:       line.UnitPrice,
			DiscountAmount:  line.DiscountAmount,
			TaxID:           taxID,
			TaxAmount:       lineTax,
			LineTotal:       lineTotal,
			WarehouseID:     lineWarehouseID,
			Notes:           lineNotes,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create purchase order")
		return
	}

	resp := &entity.PurchaseOrderResponse{
		ID:              id,
		OrderNumber:     orderNumber,
		VendorID:        vendorID,
		VendorName:      vendorName,
		ContactPersonID: contactPersonID,
		OrderDate:       orderDate,
		ExpectedDate:    expectedDate,
		Subtotal:        subtotal,
		DiscountAmount:  discountTotal,
		TaxAmount:       taxTotal,
		ShippingAmount:  input.ShippingAmount,
		TotalAmount:     totalAmount,
		Status:          entity.POStatusDraft,
		PaymentStatus:   entity.PaymentStatusUnpaid,
		PaymentTerms:    paymentTerms,
		VendorReference: vendorReference,
		Notes:           notes,
		Lines:           lines,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	response.Created(c, resp)
}

// GetPurchaseOrder returns a single purchase order by ID
func (h *Handler) GetPurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid purchase order ID")
		return
	}

	query := `
		SELECT po.id, po.order_number, po.vendor_id, c.name as vendor_name,
			   po.contact_person_id, po.order_date, po.expected_date,
			   po.subtotal, po.discount_amount, po.tax_amount, po.shipping_amount,
			   po.total_amount, po.status, po.payment_status, po.payment_terms,
			   po.vendor_reference, po.notes, po.approved_at, po.created_at, po.updated_at
		FROM purchase_orders po
		LEFT JOIN contacts c ON po.vendor_id = c.id
		WHERE po.id = $1 AND po.tenant_id = $2 AND po.deleted_at IS NULL
	`

	var po entity.PurchaseOrderResponse
	var expectedDate, approvedAt sql.NullTime
	var contactPersonID sql.NullString
	var paymentTerms, vendorReference, notes sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&po.ID, &po.OrderNumber, &po.VendorID, &po.VendorName,
		&contactPersonID, &po.OrderDate, &expectedDate,
		&po.Subtotal, &po.DiscountAmount, &po.TaxAmount, &po.ShippingAmount,
		&po.TotalAmount, &po.Status, &po.PaymentStatus, &paymentTerms,
		&vendorReference, &notes, &approvedAt, &po.CreatedAt, &po.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		h.log.Error("Failed to get purchase order", "error", err)
		response.InternalError(c, "Failed to get purchase order")
		return
	}

	if expectedDate.Valid {
		po.ExpectedDate = &expectedDate.Time
	}
	if contactPersonID.Valid {
		if cpid, err := uuid.Parse(contactPersonID.String); err == nil {
			po.ContactPersonID = &cpid
		}
	}
	if paymentTerms.Valid {
		po.PaymentTerms = &paymentTerms.String
	}
	if vendorReference.Valid {
		po.VendorReference = &vendorReference.String
	}
	if notes.Valid {
		po.Notes = &notes.String
	}
	if approvedAt.Valid {
		po.ApprovedAt = &approvedAt.Time
	}

	// Get line items
	linesQuery := `
		SELECT pol.id, pol.purchase_order_id, pol.line_number, pol.product_id,
			   pol.description, pol.quantity, pol.unit_id, pol.unit_price,
			   pol.discount_amount, pol.tax_id, pol.tax_amount, pol.line_total,
			   pol.quantity_received, pol.quantity_invoiced, pol.warehouse_id, pol.notes,
			   COALESCE(p.name, '') as product_name, COALESCE(u.name, '') as unit_name,
			   pol.created_at, pol.updated_at
		FROM purchase_order_lines pol
		LEFT JOIN products p ON pol.product_id = p.id
		LEFT JOIN units_of_measure u ON pol.unit_id = u.id
		WHERE pol.purchase_order_id = $1
		ORDER BY pol.line_number ASC
	`

	rows, err := h.db.Query(linesQuery, id)
	if err != nil {
		h.log.Error("Failed to get purchase order lines", "error", err)
		response.InternalError(c, "Failed to get purchase order")
		return
	}
	defer rows.Close()

	po.Lines = make([]entity.PurchaseOrderLine, 0)
	for rows.Next() {
		var line entity.PurchaseOrderLine
		var productID, unitID, taxID, warehouseID sql.NullString
		var lineNotes sql.NullString

		err := rows.Scan(
			&line.ID, &line.PurchaseOrderID, &line.LineNumber, &productID,
			&line.Description, &line.Quantity, &unitID, &line.UnitPrice,
			&line.DiscountAmount, &taxID, &line.TaxAmount, &line.LineTotal,
			&line.QuantityReceived, &line.QuantityInvoiced, &warehouseID, &lineNotes,
			&line.ProductName, &line.UnitName,
			&line.CreatedAt, &line.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if productID.Valid {
			if pid, err := uuid.Parse(productID.String); err == nil {
				line.ProductID = &pid
			}
		}
		if unitID.Valid {
			if uid, err := uuid.Parse(unitID.String); err == nil {
				line.UnitID = &uid
			}
		}
		if taxID.Valid {
			if tid, err := uuid.Parse(taxID.String); err == nil {
				line.TaxID = &tid
			}
		}
		if warehouseID.Valid {
			if wid, err := uuid.Parse(warehouseID.String); err == nil {
				line.WarehouseID = &wid
			}
		}
		if lineNotes.Valid {
			line.Notes = &lineNotes.String
		}

		po.Lines = append(po.Lines, line)
	}

	response.Success(c, po)
}

// UpdatePurchaseOrder updates an existing purchase order
func (h *Handler) UpdatePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid purchase order ID")
		return
	}

	// Check if order exists and is editable
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		h.log.Error("Failed to check purchase order", "error", err)
		response.InternalError(c, "Failed to update purchase order")
		return
	}

	// Only allow editing draft orders
	if currentStatus != string(entity.POStatusDraft) {
		response.BadRequest(c, "Only draft orders can be edited")
		return
	}

	var input entity.UpdatePurchaseOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.VendorID != nil {
		if vid, err := uuid.Parse(*input.VendorID); err == nil {
			argCount++
			updates = append(updates, fmt.Sprintf("vendor_id = $%d", argCount))
			args = append(args, vid)
		}
	}
	if input.ExpectedDate != nil {
		if ed, err := time.Parse("2006-01-02", *input.ExpectedDate); err == nil {
			argCount++
			updates = append(updates, fmt.Sprintf("expected_date = $%d", argCount))
			args = append(args, ed)
		}
	}
	if input.PaymentTerms != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("payment_terms = $%d", argCount))
		args = append(args, *input.PaymentTerms)
	}
	if input.VendorReference != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("vendor_reference = $%d", argCount))
		args = append(args, *input.VendorReference)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.InternalNotes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("internal_notes = $%d", argCount))
		args = append(args, *input.InternalNotes)
	}
	if input.ShippingAmount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("shipping_amount = $%d", argCount))
		args = append(args, *input.ShippingAmount)
	}

	if len(updates) == 0 && len(input.Lines) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE conditions
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE purchase_orders SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update purchase order", "error", err)
		response.InternalError(c, "Failed to update purchase order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Purchase order")
		return
	}

	response.Success(c, gin.H{"message": "Purchase order updated successfully"})
}

// DeletePurchaseOrder soft deletes a purchase order
func (h *Handler) DeletePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid purchase order ID")
		return
	}

	// Check if order is deletable (only draft or cancelled)
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		h.log.Error("Failed to check purchase order", "error", err)
		response.InternalError(c, "Failed to delete purchase order")
		return
	}

	if currentStatus != string(entity.POStatusDraft) && currentStatus != string(entity.POStatusCancelled) {
		response.BadRequest(c, "Only draft or cancelled orders can be deleted")
		return
	}

	query := `
		UPDATE purchase_orders
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete purchase order", "error", err)
		response.InternalError(c, "Failed to delete purchase order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Purchase order")
		return
	}

	response.NoContent(c)
}

// ApprovePurchaseOrder approves a purchase order
func (h *Handler) ApprovePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid purchase order ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		h.log.Error("Failed to check purchase order", "error", err)
		response.InternalError(c, "Failed to approve purchase order")
		return
	}

	if currentStatus != string(entity.POStatusDraft) && currentStatus != string(entity.POStatusPendingApproval) {
		response.BadRequest(c, "Order cannot be approved in current status")
		return
	}

	now := time.Now()
	query := `
		UPDATE purchase_orders
		SET status = $1, approved_by = $2, approved_at = $3, updated_at = $3
		WHERE id = $4 AND tenant_id = $5 AND deleted_at IS NULL
	`

	_, err = h.db.Exec(query, entity.POStatusApproved, userID, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to approve purchase order", "error", err)
		response.InternalError(c, "Failed to approve purchase order")
		return
	}

	response.Success(c, gin.H{"message": "Purchase order approved successfully", "status": entity.POStatusApproved})
}

// ReceivePurchaseOrder records goods receipt for a purchase order
func (h *Handler) ReceivePurchaseOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid purchase order ID")
		return
	}

	var input entity.ReceivePurchaseOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		h.log.Error("Failed to check purchase order", "error", err)
		response.InternalError(c, "Failed to receive purchase order")
		return
	}

	if currentStatus != string(entity.POStatusApproved) && currentStatus != string(entity.POStatusOrdered) && currentStatus != string(entity.POStatusPartial) {
		response.BadRequest(c, "Order cannot be received in current status")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to receive purchase order")
		return
	}
	defer tx.Rollback()

	now := time.Now()

	// Update line items
	for _, line := range input.Lines {
		lineID, err := uuid.Parse(line.LineID)
		if err != nil {
			continue
		}

		_, err = tx.Exec(`
			UPDATE purchase_order_lines
			SET quantity_received = quantity_received + $1, updated_at = $2
			WHERE id = $3 AND purchase_order_id = $4
		`, line.QuantityReceived, now, lineID, id)
		if err != nil {
			h.log.Error("Failed to update line", "error", err)
			response.InternalError(c, "Failed to receive purchase order")
			return
		}
	}

	// Check if all lines are fully received
	var totalQty, totalReceived float64
	err = tx.QueryRow(`
		SELECT COALESCE(SUM(quantity), 0), COALESCE(SUM(quantity_received), 0)
		FROM purchase_order_lines WHERE purchase_order_id = $1
	`, id).Scan(&totalQty, &totalReceived)
	if err != nil {
		h.log.Error("Failed to check totals", "error", err)
		response.InternalError(c, "Failed to receive purchase order")
		return
	}

	newStatus := entity.POStatusPartial
	if totalReceived >= totalQty {
		newStatus = entity.POStatusReceived
	}

	_, err = tx.Exec(`
		UPDATE purchase_orders SET status = $1, updated_at = $2 WHERE id = $3
	`, newStatus, now, id)
	if err != nil {
		h.log.Error("Failed to update order status", "error", err)
		response.InternalError(c, "Failed to receive purchase order")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to receive purchase order")
		return
	}

	response.Success(c, gin.H{
		"message": "Goods received successfully",
		"status":  newStatus,
	})
}
