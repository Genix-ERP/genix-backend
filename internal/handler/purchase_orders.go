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
// ListPurchaseOrders godoc
// @Summary List purchase orders
// @Description Get a paginated list of purchase orders
// @Tags Purchase
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param status query string false "Filter by status (draft, confirmed, received)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase/orders [get]
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

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

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
		var paymentTerms sql.NullInt32
		var vendorReference, notes sql.NullString
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
			pt := int(paymentTerms.Int32)
			po.PaymentTerms = &pt
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
// CreatePurchaseOrder godoc
// @Summary Create a new purchase order
// @Description Create a new purchase order with line items
// @Tags Purchase
// @Accept json
// @Produce json
// @Param input body entity.CreatePurchaseOrderInput true "Purchase order input"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase/orders [post]
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
	var vendorReference, notes, internalNotes *string
	// Convert payment terms string to integer (database stores as INTEGER - days)
	paymentTerms := 30 // default
	if input.PaymentTerms != "" {
		switch input.PaymentTerms {
		case "prepaid", "due_on_receipt":
			paymentTerms = 0
		case "net_15":
			paymentTerms = 15
		case "net_30", "Net 30":
			paymentTerms = 30
		case "net_60", "Net 60":
			paymentTerms = 60
		case "net_90", "Net 90":
			paymentTerms = 90
		default:
			// Try to parse as integer
			if val, err := strconv.Atoi(input.PaymentTerms); err == nil {
				paymentTerms = val
			}
		}
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

	// Get organization ID
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
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
			id, tenant_id, organization_id, order_number, vendor_id, contact_person_id,
			order_date, expected_date, currency_id, exchange_rate,
			subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
			status, payment_status, payment_terms, vendor_reference,
			notes, internal_notes, warehouse_id, requested_by,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)
	`

	_, err = tx.Exec(query,
		id, tenantID, orgIDPtr, orderNumber, vendorID, contactPersonID,
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
			packaging_id, packaging_qty, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	`

	lines := make([]entity.PurchaseOrderLine, 0, len(input.Lines))
	for i, line := range input.Lines {
		lineID := uuid.New()

		var productID, unitID, taxID, lineWarehouseID, packagingID *uuid.UUID
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
		if line.PackagingID != "" {
			if pkgid, err := uuid.Parse(line.PackagingID); err == nil {
				packagingID = &pkgid
			}
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
			packagingID, line.PackagingQty, now, now,
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
			PackagingID:     packagingID,
			PackagingQty:    line.PackagingQty,
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
		PaymentTerms:    &paymentTerms,
		VendorReference: vendorReference,
		Notes:           notes,
		Lines:           lines,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	response.Created(c, resp)
}

// GetPurchaseOrder returns a single purchase order by ID
// GetPurchaseOrder godoc
// @Summary Get purchase order by ID
// @Description Get a single purchase order with line items by ID
// @Tags Purchase
// @Accept json
// @Produce json
// @Param id path string true "Purchase order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase/orders/{id} [get]
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
	var paymentTerms sql.NullInt32
	var vendorReference, notes sql.NullString

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
		pt := int(paymentTerms.Int32)
		po.PaymentTerms = &pt
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
			   pol.packaging_id, pol.packaging_qty,
			   COALESCE(p.name, '') as product_name, COALESCE(u.name, '') as unit_name,
			   COALESCE(pkg.name, '') as packaging_name, COALESCE(pkg.qty, 0) as packaging_unit_qty,
			   pol.created_at, pol.updated_at
		FROM purchase_order_lines pol
		LEFT JOIN products p ON pol.product_id = p.id
		LEFT JOIN units_of_measure u ON pol.unit_id = u.id
		LEFT JOIN product_packagings pkg ON pol.packaging_id = pkg.id
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
		var productID, unitID, taxID, warehouseID, packagingID sql.NullString
		var lineNotes sql.NullString
		var packagingQty sql.NullFloat64
		var packagingName string
		var packagingUnitQty float64

		err := rows.Scan(
			&line.ID, &line.PurchaseOrderID, &line.LineNumber, &productID,
			&line.Description, &line.Quantity, &unitID, &line.UnitPrice,
			&line.DiscountAmount, &taxID, &line.TaxAmount, &line.LineTotal,
			&line.QuantityReceived, &line.QuantityInvoiced, &warehouseID, &lineNotes,
			&packagingID, &packagingQty,
			&line.ProductName, &line.UnitName,
			&packagingName, &packagingUnitQty,
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
		if packagingID.Valid {
			if pkgid, err := uuid.Parse(packagingID.String); err == nil {
				line.PackagingID = &pkgid
				line.Packaging = &entity.ProductPackaging{
					ID:   pkgid,
					Name: packagingName,
					Qty:  packagingUnitQty,
				}
			}
		}
		if packagingQty.Valid {
			line.PackagingQty = &packagingQty.Float64
		}

		po.Lines = append(po.Lines, line)
	}

	response.Success(c, po)
}

// UpdatePurchaseOrder updates an existing purchase order
// UpdatePurchaseOrder godoc
// @Summary Update purchase order
// @Description Update an existing purchase order. Updates inventory when status changes to 'received'
// @Tags Purchase
// @Accept json
// @Produce json
// @Param id path string true "Purchase order ID"
// @Param input body entity.UpdatePurchaseOrderInput true "Update input"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase/orders/{id} [put]
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
		// Convert payment terms string to integer (database stores as INTEGER - days)
		paymentTerms := 30 // default
		switch *input.PaymentTerms {
		case "prepaid", "due_on_receipt":
			paymentTerms = 0
		case "net_15":
			paymentTerms = 15
		case "net_30", "Net 30":
			paymentTerms = 30
		case "net_60", "Net 60":
			paymentTerms = 60
		case "net_90", "Net 90":
			paymentTerms = 90
		default:
			// Try to parse as integer
			if val, err := strconv.Atoi(*input.PaymentTerms); err == nil {
				paymentTerms = val
			}
		}
		argCount++
		updates = append(updates, fmt.Sprintf("payment_terms = $%d", argCount))
		args = append(args, paymentTerms)
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
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
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

	// ============================================
	// UPDATE INVENTORY WHEN PO STATUS → "received"
	// ============================================
	if input.Status != nil && *input.Status == "received" {
		now := time.Now()

		// Get PO warehouse_id and organization_id
		var poWarehouseID sql.NullString
		var poOrgID sql.NullString
		h.db.QueryRow("SELECT warehouse_id, organization_id FROM purchase_orders WHERE id = $1", id).Scan(&poWarehouseID, &poOrgID)

		// Determine warehouse: PO warehouse → org's first warehouse → tenant's first warehouse
		var warehouseID uuid.UUID
		if poWarehouseID.Valid && poWarehouseID.String != "" {
			warehouseID, _ = uuid.Parse(poWarehouseID.String)
		} else if poOrgID.Valid && poOrgID.String != "" {
			orgID, _ := uuid.Parse(poOrgID.String)
			err := h.db.QueryRow(`
				SELECT id FROM warehouses
				WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL
				ORDER BY created_at ASC LIMIT 1
			`, tenantID, orgID).Scan(&warehouseID)
			if err != nil {
				// Fallback to any warehouse in the tenant
				err = h.db.QueryRow(`
					SELECT id FROM warehouses
					WHERE tenant_id = $1 AND deleted_at IS NULL
					ORDER BY created_at ASC LIMIT 1
				`, tenantID).Scan(&warehouseID)
				if err != nil {
					h.log.Error("No warehouse found for inventory update", "error", err)
					response.Success(c, gin.H{"message": "Purchase order updated but no warehouse for inventory"})
					return
				}
			}
		} else {
			err := h.db.QueryRow(`
				SELECT id FROM warehouses
				WHERE tenant_id = $1 AND deleted_at IS NULL
				ORDER BY created_at ASC LIMIT 1
			`, tenantID).Scan(&warehouseID)
			if err != nil {
				h.log.Error("No warehouse found for inventory update", "error", err)
				response.Success(c, gin.H{"message": "Purchase order updated but no warehouse for inventory"})
				return
			}
		}

		// Get PO lines with product info
		poLines, err := h.db.Query(`
			SELECT id, product_id, quantity, unit_price
			FROM purchase_order_lines
			WHERE purchase_order_id = $1 AND product_id IS NOT NULL
		`, id)
		if err == nil {
			defer poLines.Close()
			for poLines.Next() {
				var lineID, productID uuid.UUID
				var qty, unitPrice float64
				if err := poLines.Scan(&lineID, &productID, &qty, &unitPrice); err != nil {
					continue
				}

				// Update quantity_received on PO line
				h.db.Exec(`
					UPDATE purchase_order_lines
					SET quantity_received = quantity, updated_at = $1
					WHERE id = $2
				`, now, lineID)

				// Get or create inventory record
				var inventoryID uuid.UUID
				err := h.db.QueryRow(`
					SELECT id FROM inventory
					WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
					ORDER BY created_at ASC LIMIT 1
				`, tenantID, productID, warehouseID).Scan(&inventoryID)

				if err == sql.ErrNoRows {
					inventoryID = uuid.New()
					h.db.Exec(`
						INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at)
						VALUES ($1, $2, $3, $4, 0, 0, $5, $6, $6)
					`, inventoryID, tenantID, productID, warehouseID, unitPrice, now)
				}

				// Update inventory quantity
				h.db.Exec(`
					UPDATE inventory
					SET quantity_on_hand = quantity_on_hand + $1,
						unit_cost = $2,
						last_movement_date = $3,
						updated_at = $3
					WHERE id = $4
				`, qty, unitPrice, now, inventoryID)

				// Create inventory transaction for audit trail
				txID := uuid.New()
				h.db.Exec(`
					INSERT INTO inventory_transactions (
						id, tenant_id, inventory_id, transaction_type, quantity,
						unit_cost, total_cost, reference_type, reference_id,
						reason, transaction_date, created_at
					) VALUES ($1, $2, $3, 'receipt', $4, $5, $6, 'purchase_order', $7, 'Purchase Order Received', $8, $8)
				`, txID, tenantID, inventoryID, qty, unitPrice, qty*unitPrice, id, now)
			}
		}

		h.log.Info("Inventory updated from PO received", "po_id", id)

		// ============================================
		// CREATE JOURNAL ENTRY: Debit Stock Valuation, Credit Stock Interim Receipt (per category, Odoo-style)
		// ============================================
		var poTotal float64
		var poNumber string
		var poVendorName sql.NullString
		var poVendorID sql.NullString
		h.db.QueryRow(`
			SELECT total_amount, order_number, c.name, po.vendor_id
			FROM purchase_orders po
			LEFT JOIN contacts c ON po.vendor_id = c.id
			WHERE po.id = $1
		`, id).Scan(&poTotal, &poNumber, &poVendorName, &poVendorID)

		var jeEntryID uuid.UUID // will be set if JE is created, used by vendor bill

		if poTotal > 0 {
			var orgIDPtr *uuid.UUID
			if poOrgID.Valid && poOrgID.String != "" {
				parsed, _ := uuid.Parse(poOrgID.String)
				if parsed != uuid.Nil {
					orgIDPtr = &parsed
				}
			}

			// Get PO lines with product info for per-category accounting
			type poLineAcct struct {
				ProductID  uuid.UUID
				LineTotal  float64
				ValuationAcct uuid.UUID
				InputAcct     uuid.UUID
			}
			var poLines []poLineAcct
			rows, err := h.db.Query(`
				SELECT product_id, COALESCE(line_total, 0)
				FROM purchase_order_lines
				WHERE purchase_order_id = $1
			`, id)
			if err == nil {
				for rows.Next() {
					var pl poLineAcct
					if err := rows.Scan(&pl.ProductID, &pl.LineTotal); err == nil && pl.LineTotal > 0 {
						poLines = append(poLines, pl)
					}
				}
				rows.Close()
				// Resolve category accounts after closing rows
				for i := range poLines {
					ca := getCategoryAccounts(h.db, tenantID, orgIDPtr, poLines[i].ProductID)
					poLines[i].ValuationAcct = ca.StockValuationAccountID
					poLines[i].InputAcct = ca.StockInputAccountID
				}
			}

			// Group by account pair to minimize JE lines
			type acctPair struct {
				Debit  uuid.UUID
				Credit uuid.UUID
			}
			grouped := make(map[acctPair]float64)
			for _, pl := range poLines {
				key := acctPair{Debit: pl.ValuationAcct, Credit: pl.InputAcct}
				grouped[key] += pl.LineTotal
			}

			// If no lines found, fall back to total with default accounts
			if len(grouped) == 0 && poTotal > 0 {
				valAcct := findAccount(h.db, tenantID, orgIDPtr, "inventory", "1300")
				if valAcct == uuid.Nil {
					valAcct = findAccount(h.db, tenantID, orgIDPtr, "stock", "1300")
				}
				inputAcct := findAccount(h.db, tenantID, orgIDPtr, "stock interim receipt", "2200")
				if inputAcct == uuid.Nil {
					// If no interim account, fall back to AP directly (backward compat)
					inputAcct = findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "2000")
				}
				if valAcct != uuid.Nil && inputAcct != uuid.Nil {
					grouped[acctPair{Debit: valAcct, Credit: inputAcct}] = poTotal
				}
			}

			if len(grouped) > 0 {
				// Find purchase journal
				var journalID uuid.UUID
				var nextNumber int
				err := h.db.QueryRow(`
					SELECT id, next_number FROM journals
					WHERE tenant_id = $1 AND type = 'purchase' AND is_active = true
					ORDER BY created_at ASC LIMIT 1
				`, tenantID).Scan(&journalID, &nextNumber)
				if err != nil {
					h.db.QueryRow(`
						SELECT id, next_number FROM journals
						WHERE tenant_id = $1 AND type = 'general' AND is_active = true
						ORDER BY created_at ASC LIMIT 1
					`, tenantID).Scan(&journalID, &nextNumber)
				}

				if journalID != uuid.Nil {
					entryID := uuid.New()
					jeEntryID = entryID
					entryNumber := fmt.Sprintf("PUR%06d", nextNumber)
					vendorName := ""
					if poVendorName.Valid {
						vendorName = poVendorName.String
					}
					description := fmt.Sprintf("Purchase Order %s received - %s", poNumber, vendorName)

					var contactID *uuid.UUID
					if poVendorID.Valid {
						parsed, _ := uuid.Parse(poVendorID.String)
						if parsed != uuid.Nil {
							contactID = &parsed
						}
					}

					// Create journal entry header
					h.db.Exec(`
						INSERT INTO journal_entries (
							id, tenant_id, organization_id, journal_id, entry_number,
							entry_date, description, source_type, source_id, status, total_debit, total_credit,
							created_at, updated_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, 'purchase_order', $8, 'posted', $9, $9, $10, $10)
					`, entryID, tenantID, orgIDPtr, journalID, entryNumber,
						now, description, id.String(), poTotal, now)

					lineNumber := 1
					for pair, amount := range grouped {
						// Debit: Stock Valuation
						debitLineID := uuid.New()
						h.db.Exec(`
							INSERT INTO journal_entry_lines (
								id, journal_entry_id, account_id, contact_id, description,
								debit_amount, credit_amount, line_number, created_at
							) VALUES ($1, $2, $3, $4, $5, $6, 0, $7, $8)
						`, debitLineID, entryID, pair.Debit, contactID, "Stock Valuation", amount, lineNumber, now)
						lineNumber++

						// Credit: Stock Interim Receipt
						creditLineID := uuid.New()
						h.db.Exec(`
							INSERT INTO journal_entry_lines (
								id, journal_entry_id, account_id, contact_id, description,
								debit_amount, credit_amount, line_number, created_at
							) VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $8)
						`, creditLineID, entryID, pair.Credit, contactID, "Stock Interim Receipt", amount, lineNumber, now)
						lineNumber++

						// Update account balances
						h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, amount, now, pair.Debit)
						h.db.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, amount, now, pair.Credit)
					}

					// Update journal next_number
					h.db.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID)

					h.log.Info("Journal entry created for PO received (Odoo-style)", "entry_id", entryID, "amount", poTotal)
				}
			}
		}

		// ============================================
		// CREATE VENDOR BILL (purchase_invoice) from PO
		// ============================================
		var vendorID uuid.UUID
		var poSubtotal, poTaxAmt float64
		h.db.QueryRow(`
			SELECT vendor_id, COALESCE(subtotal, total_amount), COALESCE(tax_amount, 0)
			FROM purchase_orders WHERE id = $1
		`, id).Scan(&vendorID, &poSubtotal, &poTaxAmt)

		if vendorID != uuid.Nil && poTotal > 0 {
			// Check if a bill already exists for this PO
			var existingBillID uuid.UUID
			h.db.QueryRow(`
				SELECT id FROM purchase_invoices
				WHERE purchase_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
				LIMIT 1
			`, id, tenantID).Scan(&existingBillID)

			if existingBillID == uuid.Nil {
				billID := uuid.New()
				invoiceNumber := fmt.Sprintf("BILL-%s-%s", now.Format("20060102"), billID.String()[:6])

				var orgIDForBill *uuid.UUID
				if poOrgID.Valid && poOrgID.String != "" {
					parsed, _ := uuid.Parse(poOrgID.String)
					if parsed != uuid.Nil {
						orgIDForBill = &parsed
					}
				}

				dueDate := now.AddDate(0, 0, 30) // 30 days payment term

				var jePtr *uuid.UUID
				if jeEntryID != uuid.Nil {
					jePtr = &jeEntryID
				}
				createdBy, _ := middleware.GetUserID(c)
				var createdByPtr *uuid.UUID
				if createdBy != uuid.Nil {
					createdByPtr = &createdBy
				}

				h.db.Exec(`
					INSERT INTO purchase_invoices (
						id, tenant_id, organization_id, invoice_number, vendor_id,
						purchase_order_id, invoice_date, due_date,
						subtotal, tax_amount, total_amount, amount_paid,
						status, payment_status, three_way_match_status,
						journal_entry_id, notes, created_by, created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, 'confirmed', 'unpaid', 'matched', $12, $13, $14, $15, $15)
				`, billID, tenantID, orgIDForBill, invoiceNumber, vendorID,
					id, now, dueDate,
					poSubtotal, poTaxAmt, poTotal,
					jePtr,
					fmt.Sprintf("Auto-generated from PO %s", poNumber), createdByPtr, now)

				// Create bill lines from PO lines
				billLines, _ := h.db.Query(`
					SELECT id, product_id, description, quantity, unit_price, COALESCE(tax_amount, 0), COALESCE(line_total, quantity * unit_price)
					FROM purchase_order_lines
					WHERE purchase_order_id = $1
				`, id)
				if billLines != nil {
					lineNum := 0
					for billLines.Next() {
						var polID uuid.UUID
						var productID *uuid.UUID
						var desc string
						var qty, unitPrice, lineTax, lineTotal float64
						if err := billLines.Scan(&polID, &productID, &desc, &qty, &unitPrice, &lineTax, &lineTotal); err != nil {
							continue
						}
						lineNum++
						h.db.Exec(`
							INSERT INTO purchase_invoice_lines (
								id, purchase_invoice_id, purchase_order_line_id, line_number,
								product_id, description, quantity, unit_price,
								tax_amount, line_total, created_at
							) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
						`, uuid.New(), billID, polID, lineNum,
							productID, desc, qty, unitPrice, lineTax, lineTotal, now)
					}
					billLines.Close()
				}

				h.log.Info("Vendor bill created from PO received", "bill_id", billID, "po_id", id)
			}
		}
	}

	response.Success(c, gin.H{"message": "Purchase order updated successfully"})
}

// DeletePurchaseOrder soft deletes a purchase order
// DeletePurchaseOrder godoc
// @Summary Delete purchase order
// @Description Soft delete a purchase order. Only draft or cancelled orders can be deleted
// @Tags Purchase
// @Accept json
// @Produce json
// @Param id path string true "Purchase order ID"
// @Success 204 "No Content"
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase/orders/{id} [delete]
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

// SubmitPOForApproval submits a purchase order for approval (evaluates procurement rules)
// SubmitPOForApproval godoc
// @Summary Submit purchase order for approval
// @Description Submit a purchase order for approval. Evaluates procurement rules and may auto-approve, require approval, or block
// @Tags Purchase
// @Accept json
// @Produce json
// @Param id path string true "Purchase order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase/orders/{id}/submit [post]
func (h *Handler) SubmitPOForApproval(c *gin.Context) {
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

	// Get order details
	var currentStatus string
	var totalAmount float64
	var vendorID uuid.UUID
	var orderNumber string
	err = h.db.QueryRow(`
		SELECT status, total_amount, vendor_id, order_number
		FROM purchase_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus, &totalAmount, &vendorID, &orderNumber)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		h.log.Error("Failed to get purchase order", "error", err)
		response.InternalError(c, "Failed to submit purchase order")
		return
	}

	if currentStatus != string(entity.POStatusDraft) {
		response.BadRequest(c, "Only draft orders can be submitted for approval")
		return
	}

	// Evaluate procurement rules
	result, err := h.EvaluateRules(tenantID, entity.DocumentTypePurchaseOrder, id, totalAmount, &vendorID, userID)
	if err != nil {
		h.log.Error("Failed to evaluate rules", "error", err)
		// Fall back to pending_approval
		result = &entity.RuleEvaluationResult{Action: entity.ActionRoute}
	}

	now := time.Now()

	switch result.Action {
	case entity.ActionAutoApprove:
		// Auto-approve
		query := `
			UPDATE purchase_orders
			SET status = $1, approved_by = $2, approved_at = $3, updated_at = $3
			WHERE id = $4 AND tenant_id = $5 AND deleted_at IS NULL
		`
		_, err = h.db.Exec(query, entity.POStatusApproved, userID, now, id, tenantID)
		if err != nil {
			h.log.Error("Failed to auto-approve purchase order", "error", err)
			response.InternalError(c, "Failed to approve purchase order")
			return
		}
		response.Success(c, gin.H{
			"message":      "Purchase order auto-approved",
			"status":       entity.POStatusApproved,
			"auto_approved": true,
		})

	case entity.ActionBlock:
		response.BadRequest(c, result.Message)

	case entity.ActionWarn:
		// Set to pending approval but include warning
		query := `
			UPDATE purchase_orders
			SET status = $1, updated_at = $2
			WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
		`
		_, err = h.db.Exec(query, entity.POStatusPendingApproval, now, id, tenantID)
		if err != nil {
			h.log.Error("Failed to submit purchase order", "error", err)
			response.InternalError(c, "Failed to submit purchase order")
			return
		}
		response.Success(c, gin.H{
			"message": "Purchase order submitted for approval",
			"status":  entity.POStatusPendingApproval,
			"warning": result.Message,
		})

	default: // ActionRoute or any other
		// Create approval workflow if approvers specified
		if len(result.ApproverIDs) > 0 {
			workflow, err := h.CreateApprovalWorkflow(tenantID, entity.DocumentTypePurchaseOrder, id, orderNumber, result)
			if err != nil {
				h.log.Error("Failed to create approval workflow", "error", err)
			}

			// Set to pending approval
			query := `
				UPDATE purchase_orders
				SET status = $1, updated_at = $2
				WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
			`
			_, err = h.db.Exec(query, entity.POStatusPendingApproval, now, id, tenantID)
			if err != nil {
				h.log.Error("Failed to submit purchase order", "error", err)
				response.InternalError(c, "Failed to submit purchase order")
				return
			}

			response.Success(c, gin.H{
				"message":     "Purchase order submitted for approval",
				"status":      entity.POStatusPendingApproval,
				"workflow_id": workflow.ID,
				"approvers":   result.ApproverIDs,
			})
		} else {
			// No specific approvers, just set to pending approval
			query := `
				UPDATE purchase_orders
				SET status = $1, updated_at = $2
				WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
			`
			_, err = h.db.Exec(query, entity.POStatusPendingApproval, now, id, tenantID)
			if err != nil {
				h.log.Error("Failed to submit purchase order", "error", err)
				response.InternalError(c, "Failed to submit purchase order")
				return
			}
			response.Success(c, gin.H{
				"message": "Purchase order submitted for approval",
				"status":  entity.POStatusPendingApproval,
			})
		}
	}
}

// ApprovePurchaseOrder approves a purchase order (direct approval by authorized user)
// ApprovePurchaseOrder godoc
// @Summary Approve purchase order
// @Description Directly approve a purchase order. Only draft or pending approval orders can be approved
// @Tags Purchase
// @Accept json
// @Produce json
// @Param id path string true "Purchase order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase/orders/{id}/approve [post]
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

	// Cancel any pending workflow for this document
	_, _ = h.db.Exec(`
		UPDATE approval_workflow_instances
		SET status = 'cancelled', completed_at = $1, updated_at = $1
		WHERE document_type = 'purchase_order' AND document_id = $2 AND status = 'pending'
	`, now, id)

	response.Success(c, gin.H{"message": "Purchase order approved successfully", "status": entity.POStatusApproved})
}

// ReceivePurchaseOrder records goods receipt for a purchase order
// ReceivePurchaseOrder godoc
// @Summary Receive goods for purchase order
// @Description Record goods receipt for a purchase order. Updates line item quantities received
// @Tags Purchase
// @Accept json
// @Produce json
// @Param id path string true "Purchase order ID"
// @Param input body entity.ReceivePurchaseOrderInput true "Receive input"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase/orders/{id}/receive [post]
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
