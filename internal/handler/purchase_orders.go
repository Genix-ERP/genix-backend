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
		var vendorID sql.NullString
		var vendorName sql.NullString

		err := rows.Scan(
			&po.ID, &po.OrderNumber, &vendorID, &vendorName,
			&po.OrderDate, &expectedDate, &po.Subtotal, &po.DiscountAmount,
			&po.TaxAmount, &po.ShippingAmount, &po.TotalAmount, &po.Status,
			&po.PaymentStatus, &paymentTerms, &vendorReference, &notes,
			&approvedAt, &po.CreatedAt, &po.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan purchase order", "error", err)
			continue
		}

		if vendorID.Valid {
			po.VendorID, _ = uuid.Parse(vendorID.String)
		}
		if vendorName.Valid {
			po.VendorName = vendorName.String
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// Trigger intercompany sync: if vendor is an intercompany contact, create SO in their company
	if err := h.icSync.SyncPurchaseOrderToSaleOrder(tenantID, id); err != nil {
		h.log.Error("Intercompany PO->SO sync failed", "error", err, "order_id", id)
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
	var vendorID, vendorName sql.NullString
	var paymentTerms sql.NullInt32
	var vendorReference, notes sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&po.ID, &po.OrderNumber, &vendorID, &vendorName,
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

	if vendorID.Valid {
		if vid, err := uuid.Parse(vendorID.String); err == nil {
			po.VendorID = vid
		}
	}
	if vendorName.Valid {
		po.VendorName = vendorName.String
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
			h.log.Error("Failed to scan PO line", "error", err, "po_id", id)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		// Block statuses that must go through dedicated endpoints
		// approved → POST /:id/approve (creates stock operations)
		// received → POST /:id/receive (updates inventory)
		if *input.Status == "approved" {
			response.BadRequest(c, "Use the approve endpoint to approve a purchase order")
			return
		}
		if *input.Status == "received" {
			response.BadRequest(c, "Use the receive endpoint to receive a purchase order")
			return
		}
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
		// Auto-approve with stock operation creation
		if err := h.approvePOAndCreateReceipt(tenantID, userID, id); err != nil {
			h.log.Error("Failed to auto-approve purchase order", "error", err)
			response.InternalError(c, "Failed to approve purchase order")
			return
		}
		response.Success(c, gin.H{
			"message":       "Purchase order auto-approved",
			"status":        entity.POStatusApproved,
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

// approvePOAndCreateReceipt is a shared helper that approves a PO and creates the stock receipt
// operation in a single transaction. Used by both ApprovePurchaseOrder and SubmitPOForApproval (auto-approve).
func (h *Handler) approvePOAndCreateReceipt(tenantID, userID, poID uuid.UUID) error {
	var orderNumber string
	var vendorID uuid.UUID
	var warehouseID *uuid.UUID
	var orgID *uuid.UUID
	var expectedDate *time.Time

	err := h.db.QueryRow(`
		SELECT order_number, vendor_id, warehouse_id, organization_id, expected_date
		FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, poID, tenantID).Scan(&orderNumber, &vendorID, &warehouseID, &orgID, &expectedDate)
	if err != nil {
		return fmt.Errorf("failed to fetch PO details: %w", err)
	}

	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Update PO status to approved
	_, err = tx.Exec(`
		UPDATE purchase_orders
		SET status = $1, approved_by = $2, approved_at = $3, updated_at = $3
		WHERE id = $4 AND tenant_id = $5 AND deleted_at IS NULL
	`, entity.POStatusApproved, userID, now, poID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to approve PO: %w", err)
	}

	// Cancel any pending workflow for this document (non-critical, log errors)
	if _, wfErr := tx.Exec(`
		UPDATE approval_workflow_instances
		SET status = 'cancelled', completed_at = $1, updated_at = $1
		WHERE document_type = 'purchase_order' AND document_id = $2 AND status = 'pending'
	`, now, poID); wfErr != nil {
		h.log.Warn("Failed to cancel pending workflows", "error", wfErr)
	}

	// 2. Create stock operation (warehouse receipt) — like Odoo's _create_picking()
	var opTypeID uuid.UUID
	var srcLocID, destLocID *uuid.UUID

	opTypeQuery := `
		SELECT id, default_location_src_id, default_location_dest_id
		FROM warehouse_operation_types
		WHERE tenant_id = $1 AND type = 'receipt' AND is_active = true
	`
	opTypeArgs := []interface{}{tenantID}

	if warehouseID != nil {
		opTypeQuery += " AND warehouse_id = $2 ORDER BY sequence LIMIT 1"
		opTypeArgs = append(opTypeArgs, *warehouseID)
	} else {
		opTypeQuery += " ORDER BY sequence LIMIT 1"
	}

	err = h.db.QueryRow(opTypeQuery, opTypeArgs...).Scan(&opTypeID, &srcLocID, &destLocID)
	if err != nil {
		// No receipt operation type found — log warning but don't block approval
		h.log.Warn("No receipt operation type found for stock operation creation",
			"error", err, "tenant_id", tenantID, "warehouse_id", warehouseID)
	}

	if err == nil {
		opID := uuid.New()
		opName := h.nextStockOperationName(tenantID, "receipt")

		var totalSteps int
		h.db.QueryRow("SELECT COUNT(*) FROM operation_type_steps WHERE operation_type_id = $1 AND tenant_id = $2", opTypeID, tenantID).Scan(&totalSteps)
		if totalSteps == 0 {
			totalSteps = 1
		}

		_, err = tx.Exec(`
			INSERT INTO stock_operations (
				id, tenant_id, organization_id, name, operation_type_id, direction,
				date, scheduled_date, partner_id, source_document,
				source_location_id, dest_location_id,
				state, current_step, total_steps, priority,
				source_type, source_id,
				responsible_id, created_by, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,'receipt',$6,$7,$8,$9,$10,$11,'draft',1,$12,'normal','purchase_order',$15,$13,$13,$14,$14)
		`,
			opID, tenantID, orgID, opName, opTypeID,
			now, expectedDate, vendorID, orderNumber,
			srcLocID, destLocID,
			totalSteps, userID, now, poID,
		)
		if err != nil {
			return fmt.Errorf("failed to create stock operation: %w", err)
		}

		// 3. Create stock operation lines from PO lines
		rows, err := h.db.Query(`
			SELECT pol.product_id, pol.quantity, pol.unit_price,
			       COALESCE(u.name, 'unit') as uom
			FROM purchase_order_lines pol
			LEFT JOIN units_of_measure u ON u.id = pol.unit_id
			WHERE pol.purchase_order_id = $1 AND pol.product_id IS NOT NULL
		`, poID)
		if err != nil {
			return fmt.Errorf("failed to fetch PO lines: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var productID uuid.UUID
			var qty float64
			var unitPrice float64
			var uom string

			if err := rows.Scan(&productID, &qty, &unitPrice, &uom); err != nil {
				h.log.Error("Failed to scan PO line", "error", err)
				continue
			}

			_, err = tx.Exec(`
				INSERT INTO stock_operation_lines (
					id, tenant_id, operation_id, product_id,
					expected_qty, done_qty, uom, unit_price,
					quality_status, created_at, updated_at
				) VALUES (uuid_generate_v4(),$1,$2,$3,$4,$4,$5,$6,'good',$7,$7)
			`,
				tenantID, opID, productID,
				qty, uom, unitPrice, now,
			)
			if err != nil {
				return fmt.Errorf("failed to create stock operation line for product %s: %w", productID, err)
			}
		}

		// 4. Create initial step log entry (only if steps are configured)
		var firstStep struct {
			ID   uuid.UUID
			Name string
		}
		stepErr := h.db.QueryRow(`
			SELECT id, name FROM operation_type_steps
			WHERE operation_type_id = $1 AND tenant_id = $2
			ORDER BY sequence LIMIT 1
		`, opTypeID, tenantID).Scan(&firstStep.ID, &firstStep.Name)
		if stepErr == nil {
			_, _ = tx.Exec(`
				INSERT INTO stock_operation_step_log (
					id, tenant_id, operation_id, step_id, step_sequence, step_name, state, created_at
				) VALUES (uuid_generate_v4(),$1,$2,$3,1,$4,'ready',$5)
			`, tenantID, opID, firstStep.ID, firstStep.Name, now)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
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

	// Check PO status
	var currentStatus string
	err = h.db.QueryRow(`
		SELECT status FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		h.log.Error("Failed to check purchase order", "error", err)
		response.InternalError(c, "Failed to approve purchase order")
		return
	}

	if currentStatus != string(entity.POStatusDraft) &&
		currentStatus != string(entity.POStatusPendingApproval) &&
		currentStatus != string(entity.POStatusOrdered) {
		response.BadRequest(c, "Order cannot be approved in current status")
		return
	}

	if err := h.approvePOAndCreateReceipt(tenantID, userID, id); err != nil {
		h.log.Error("Failed to approve purchase order", "error", err)
		response.InternalError(c, "Failed to approve purchase order")
		return
	}

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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// Sync: mark linked stock operation as done when PO is fully received
	if newStatus == entity.POStatusReceived {
		var opID uuid.UUID
		err := h.db.QueryRow(`
			SELECT id FROM stock_operations
			WHERE source_type = 'purchase_order' AND source_id = $1
			  AND tenant_id = $2 AND state != 'done' AND state != 'cancelled'
			  AND deleted_at IS NULL
			ORDER BY created_at DESC LIMIT 1
		`, id, tenantID).Scan(&opID)
		if err == nil {
			// Update operation lines done_qty from PO lines
			h.db.Exec(`
				UPDATE stock_operation_lines sol
				SET done_qty = pol.quantity_received, updated_at = $1
				FROM purchase_order_lines pol
				WHERE sol.operation_id = $2 AND sol.tenant_id = $3
				  AND sol.product_id = pol.product_id AND pol.purchase_order_id = $4
			`, now, opID, tenantID, id)

			h.db.Exec(`
				UPDATE stock_operations
				SET state = 'done', done_at = $1, updated_at = $1
				WHERE id = $2 AND tenant_id = $3
			`, now, opID, tenantID)
		}
	}

	// Update inventory for received line items
	var poWarehouseID sql.NullString
	var poOrgID *uuid.UUID
	h.db.QueryRow("SELECT warehouse_id, organization_id FROM purchase_orders WHERE id = $1", id).Scan(&poWarehouseID, &poOrgID)

	// Fall back to the first warehouse for this tenant if PO has no warehouse
	var defaultWarehouseID sql.NullString
	if !poWarehouseID.Valid || poWarehouseID.String == "" {
		h.db.QueryRow("SELECT id FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY is_default DESC, created_at ASC LIMIT 1", tenantID).Scan(&defaultWarehouseID)
	}

	// Auto-assign first warehouse if PO has none
	if !poWarehouseID.Valid || poWarehouseID.String == "" {
		var firstWH string
		if h.db.QueryRow(`SELECT id::text FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&firstWH) == nil {
			poWarehouseID = sql.NullString{String: firstWH, Valid: true}
			h.db.Exec(`UPDATE purchase_orders SET warehouse_id = $1 WHERE id = $2 AND tenant_id = $3`, firstWH, id, tenantID)
		}
	}

	for _, line := range input.Lines {
		lineID, err := uuid.Parse(line.LineID)
		if err != nil {
			continue
		}

		var productID uuid.UUID
		var unitPrice float64
		var lineWarehouseID sql.NullString
		err = h.db.QueryRow(`
			SELECT product_id, unit_price, warehouse_id
			FROM purchase_order_lines WHERE id = $1 AND purchase_order_id = $2
		`, lineID, id).Scan(&productID, &unitPrice, &lineWarehouseID)
		if err != nil {
			continue
		}

		// Use line warehouse, fall back to PO warehouse, then default warehouse
		whStr := lineWarehouseID
		if !whStr.Valid || whStr.String == "" {
			whStr = poWarehouseID
		}
		if !whStr.Valid || whStr.String == "" {
			whStr = defaultWarehouseID
		}
		if !whStr.Valid || whStr.String == "" {
			h.log.Warn("Skipping inventory update: no warehouse found for PO receive", "po_id", id, "line_id", lineID)
			continue
		}
		warehouseID, err := uuid.Parse(whStr.String)
		if err != nil {
			continue
		}

		// Get or create inventory record
		var inventoryID uuid.UUID
		err = h.db.QueryRow(`
			SELECT id FROM inventory
			WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		`, tenantID, productID, warehouseID).Scan(&inventoryID)

		if err == sql.ErrNoRows {
			inventoryID = uuid.New()
			h.db.Exec(`
				INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, organization_id, quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, 0, 0, $6, $7, $7)
			`, inventoryID, tenantID, productID, warehouseID, poOrgID, unitPrice, now)
		}

		// Update inventory quantity
		_, err = h.db.Exec(`
			UPDATE inventory
			SET quantity_on_hand = quantity_on_hand + $1,
				unit_cost = $2,
				last_movement_date = $3,
				updated_at = $3
			WHERE id = $4
		`, line.QuantityReceived, unitPrice, now, inventoryID)
		if err != nil {
			h.log.Error("Failed to update inventory from PO receive", "error", err, "inventory_id", inventoryID)
		}

		// Create inventory transaction for audit trail
		txnID := uuid.New()
		h.db.Exec(`
			INSERT INTO inventory_transactions (
				id, tenant_id, inventory_id, organization_id, transaction_type, quantity,
				unit_cost, total_cost, reference_type, reference_id,
				reason, transaction_date, created_at
			) VALUES ($1, $2, $3, $4, 'receipt', $5, $6, $7, 'purchase_order', $8, 'PO Goods Receipt', $9, $9)
		`, txnID, tenantID, inventoryID, poOrgID, line.QuantityReceived, unitPrice, line.QuantityReceived*unitPrice, id, now)
	}

	response.Success(c, gin.H{
		"message": "Goods received successfully",
		"status":  newStatus,
	})
}

// CreateBillFromPO creates a vendor bill (purchase invoice) from a purchase order
// @Summary Create vendor bill from purchase order
// @Description Create a new vendor bill from an existing purchase order
// @Tags Purchase
// @Accept json
// @Produce json
// @Param id path string true "Purchase order ID"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /purchase-orders/{id}/bill [post]
func (h *Handler) CreateBillFromPO(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	poID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase order ID")
		return
	}

	// Get PO details
	var vendorID uuid.UUID
	var organizationID *uuid.UUID
	var poNumber string
	var subtotal, taxAmount, totalAmount float64
	var currentStatus string
	var orgIDStr sql.NullString

	err = h.db.QueryRow(`
		SELECT po.vendor_id, po.organization_id, po.order_number,
		       COALESCE(po.subtotal, po.total_amount), COALESCE(po.tax_amount, 0), COALESCE(po.total_amount, 0),
		       po.status
		FROM purchase_orders po
		WHERE po.id = $1 AND po.tenant_id = $2 AND po.deleted_at IS NULL`,
		poID, tenantID).Scan(
		&vendorID, &orgIDStr, &poNumber, &subtotal, &taxAmount, &totalAmount, &currentStatus,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch purchase order", "error", err, "po_id", poID)
		response.InternalError(c, "Failed to fetch purchase order")
		return
	}

	// Only allow bill creation from approved, ordered, or received orders
	allowedStatuses := map[string]bool{"approved": true, "ordered": true, "confirmed": true, "received": true, "partial": true}
	if !allowedStatuses[currentStatus] {
		response.BadRequest(c, "Can only create bill from approved, ordered, or received orders")
		return
	}

	// Check if a bill already exists for this PO (prevent duplicates)
	var existingBillCount int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM purchase_invoices
		WHERE purchase_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND status != 'cancelled'`,
		poID, tenantID).Scan(&existingBillCount)
	if existingBillCount > 0 {
		response.BadRequest(c, "A bill already exists for this purchase order")
		return
	}

	// Parse organization ID
	if orgIDStr.Valid && orgIDStr.String != "" {
		parsed, _ := uuid.Parse(orgIDStr.String)
		if parsed != uuid.Nil {
			organizationID = &parsed
		}
	}

	billID := uuid.New()
	invoiceNumber := fmt.Sprintf("BILL-%s-%s", time.Now().Format("20060102"), uuid.New().String()[:6])
	now := time.Now()
	dueDate := now.AddDate(0, 0, 30) // 30 days payment term

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Use a transaction for bill creation + GL posting
	tx, txErr := h.db.Begin()
	if txErr != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Create vendor bill
	_, err = tx.Exec(`
		INSERT INTO purchase_invoices (
			id, tenant_id, organization_id, invoice_number, vendor_id,
			purchase_order_id, invoice_date, due_date,
			subtotal, tax_amount, total_amount, amount_paid,
			status, payment_status, three_way_match_status,
			notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, 'confirmed', 'unpaid', 'matched', $12, $13, $14, $14)`,
		billID, tenantID, organizationID, invoiceNumber, vendorID,
		poID, now, dueDate,
		subtotal, taxAmount, totalAmount,
		fmt.Sprintf("Created from PO %s", poNumber), createdBy, now)
	if err != nil {
		h.log.Error("Failed to create vendor bill", "error", err)
		response.InternalError(c, "Failed to create vendor bill")
		return
	}

	// Copy PO lines to bill lines — collect first, then insert
	type poLine struct {
		ID        uuid.UUID
		ProductID *uuid.UUID
		Desc      string
		Qty       float64
		UnitPrice float64
		TaxAmt    float64
		LineTotal float64
	}
	var poLines []poLine

	linesRows, err := tx.Query(`
		SELECT id, product_id, description, quantity, unit_price, COALESCE(tax_amount, 0), COALESCE(line_total, quantity * unit_price)
		FROM purchase_order_lines
		WHERE purchase_order_id = $1`, poID)
	if err == nil {
		for linesRows.Next() {
			var pl poLine
			if err := linesRows.Scan(&pl.ID, &pl.ProductID, &pl.Desc, &pl.Qty, &pl.UnitPrice, &pl.TaxAmt, &pl.LineTotal); err != nil {
				continue
			}
			poLines = append(poLines, pl)
		}
		linesRows.Close()
	}

	for i, pl := range poLines {
		tx.Exec(`
			INSERT INTO purchase_invoice_lines (
				id, purchase_invoice_id, purchase_order_line_id, line_number,
				product_id, description, quantity, unit_price,
				tax_amount, line_total, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			uuid.New(), billID, pl.ID, i+1,
			pl.ProductID, pl.Desc, pl.Qty, pl.UnitPrice, pl.TaxAmt, pl.LineTotal, now)
	}

	// ========== GL JOURNAL ENTRY POSTING ==========
	var journalEntryID *uuid.UUID

	// Get Purchase Journal
	var purchaseJournalID uuid.UUID
	var numberPrefix sql.NullString
	journalErr := tx.QueryRow(`
		SELECT id, number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('PURCHASE', 'PUR', 'PURCH') AND deleted_at IS NULL`,
		tenantID,
	).Scan(&purchaseJournalID, &numberPrefix)

	if journalErr == nil {
		// Find AP account
		apAccountID := findAccount(tx, tenantID, organizationID, "accounts payable", "2000")
		if apAccountID == uuid.Nil {
			apAccountID = findAccount(tx, tenantID, organizationID, "accounts payable", "2100")
		}

		if apAccountID != uuid.Nil {
			taxAccountID := findAccount(tx, tenantID, organizationID, "tax", "2100")

			// Per-category accounting: resolve Stock Interim Receipt per product
			type billLineAcct struct {
				ProductID uuid.UUID
				LineTotal float64
				InputAcct uuid.UUID
			}
			var billLines []billLineAcct
			for _, pl := range poLines {
				if pl.ProductID != nil && pl.LineTotal > 0 {
					billLines = append(billLines, billLineAcct{
						ProductID: *pl.ProductID,
						LineTotal: pl.LineTotal,
					})
				}
			}
			for i := range billLines {
				ca := getCategoryAccounts(tx, tenantID, organizationID, billLines[i].ProductID)
				billLines[i].InputAcct = ca.StockInputAccountID
			}

			// Group by stock input account
			inputGrouped := make(map[uuid.UUID]float64)
			for _, bl := range billLines {
				inputGrouped[bl.InputAcct] += bl.LineTotal
			}

			// Fallback if no product lines matched
			if len(inputGrouped) == 0 && subtotal > 0 {
				fallbackInput := findAccount(tx, tenantID, organizationID, "stock interim receipt", "2230")
				if fallbackInput == uuid.Nil {
					fallbackInput = findAccount(tx, tenantID, organizationID, "stock interim receipt", "2200")
				}
				if fallbackInput == uuid.Nil {
					fallbackInput = findAccount(tx, tenantID, organizationID, "cost of goods", "5000")
				}
				if fallbackInput != uuid.Nil {
					inputGrouped[fallbackInput] = subtotal
				}
			}

			// Generate unique entry number using UUID suffix (guaranteed unique)
			prefix := "BILL-"
			if numberPrefix.Valid && numberPrefix.String != "" {
				prefix = numberPrefix.String
			}
			entryNumber := fmt.Sprintf("%s%s-%s", prefix, now.Format("20060102"), uuid.New().String()[:6])

			totalDebit := totalAmount
			totalCredit := totalAmount

			// Create journal entry
			jeID := uuid.New()
			journalEntryID = &jeID
			jeDescription := fmt.Sprintf("Vendor Bill %s", invoiceNumber)

			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
				jeID, tenantID, organizationID, purchaseJournalID, entryNumber, now, invoiceNumber, jeDescription,
				"purchase_invoice", billID, 1.0, totalDebit, totalCredit, createdBy, now, now,
			); jeErr != nil {
				h.log.Error("CreateBillFromPO: failed to insert journal entry", "error", jeErr)
				response.InternalError(c, "Failed to create journal entry")
				return
			}

			jeLineNumber := 1

			// Debit: Stock Interim Receipt (per category)
			for inputAcct, amount := range inputGrouped {
				if _, err := tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, contact_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
					uuid.New(), jeID, jeLineNumber, inputAcct, vendorID, "Stock Interim Receipt",
					amount, 0.0, 1.0, now,
				); err != nil {
					h.log.Error("CreateBillFromPO: failed to insert debit JE line", "error", err, "account", inputAcct)
					response.InternalError(c, "Failed to create journal entry")
					return
				}
				tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", amount, now, inputAcct)
				jeLineNumber++
			}

			// Debit: Tax
			if taxAccountID != uuid.Nil && taxAmount > 0 {
				if _, err := tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					uuid.New(), jeID, jeLineNumber, taxAccountID, "Input Tax",
					taxAmount, 0.0, 1.0, now,
				); err != nil {
					h.log.Error("CreateBillFromPO: failed to insert tax JE line", "error", err)
					response.InternalError(c, "Failed to create journal entry")
					return
				}
				tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", taxAmount, now, taxAccountID)
				jeLineNumber++
			}

			// Credit: Accounts Payable
			if _, err := tx.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, contact_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				uuid.New(), jeID, jeLineNumber, apAccountID, vendorID, "Accounts Payable",
				0.0, totalAmount, 1.0, now,
			); err != nil {
				h.log.Error("CreateBillFromPO: failed to insert credit JE line", "error", err)
				response.InternalError(c, "Failed to create journal entry")
				return
			}
			tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalAmount, now, apAccountID)

			// Link JE to bill
			tx.Exec("UPDATE purchase_invoices SET journal_entry_id = $1 WHERE id = $2", jeID, billID)
		}
	}

	// Commit
	if err := tx.Commit(); err != nil {
		h.log.Error("CreateBillFromPO: commit failed", "error", err)
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":                billID.String(),
		"invoice_number":    invoiceNumber,
		"vendor_id":         vendorID.String(),
		"purchase_order_id": poID.String(),
		"invoice_date":      now.Format("2006-01-02"),
		"due_date":          dueDate.Format("2006-01-02"),
		"total_amount":      totalAmount,
		"status":            "confirmed",
		"journal_entry_id":  journalEntryID,
		"created_at":        now,
	})
}
