package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ==================== DROPSHIP ORDERS ====================

// ListDropshipOrders returns a list of dropship orders
func (h *Handler) ListDropshipOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit

	var filter entity.DropshipOrderListFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid filter parameters")
		return
	}

	// Build query
	query := `
		SELECT
			d.id, d.tenant_id, d.sales_order_id, d.sales_order_number,
			d.purchase_order_id, d.purchase_order_number,
			d.vendor_id, d.vendor_name, d.customer_id, d.customer_name,
			d.shipping_address, d.status, d.shipping_method, d.tracking_number,
			d.carrier, d.estimated_delivery, d.shipped_date, d.delivered_date,
			d.subtotal, d.shipping_cost, d.total_cost, d.margin, d.margin_percent,
			d.vendor_notes, d.internal_notes, d.customer_notes,
			d.created_by, d.created_at, d.updated_at
		FROM dropship_orders d
		WHERE d.tenant_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM dropship_orders d WHERE d.tenant_id = $1`
	args := []interface{}{tenantID}
	argIndex := 2

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += fmt.Sprintf(" AND d.organization_id = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND d.organization_id = $%d", argIndex)
		args = append(args, orgID)
		argIndex++
	}

	// Apply filters
	if filter.Status != "" {
		query += fmt.Sprintf(" AND d.status = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND d.status = $%d", argIndex)
		args = append(args, filter.Status)
		argIndex++
	}

	if filter.VendorID != "" {
		vendorID, err := uuid.Parse(filter.VendorID)
		if err == nil {
			query += fmt.Sprintf(" AND d.vendor_id = $%d", argIndex)
			countQuery += fmt.Sprintf(" AND d.vendor_id = $%d", argIndex)
			args = append(args, vendorID)
			argIndex++
		}
	}

	if filter.CustomerID != "" {
		customerID, err := uuid.Parse(filter.CustomerID)
		if err == nil {
			query += fmt.Sprintf(" AND d.customer_id = $%d", argIndex)
			countQuery += fmt.Sprintf(" AND d.customer_id = $%d", argIndex)
			args = append(args, customerID)
			argIndex++
		}
	}

	if filter.SalesOrderID != "" {
		soID, err := uuid.Parse(filter.SalesOrderID)
		if err == nil {
			query += fmt.Sprintf(" AND d.sales_order_id = $%d", argIndex)
			countQuery += fmt.Sprintf(" AND d.sales_order_id = $%d", argIndex)
			args = append(args, soID)
			argIndex++
		}
	}

	if filter.Search != "" {
		query += fmt.Sprintf(" AND (d.sales_order_number ILIKE $%d OR d.purchase_order_number ILIKE $%d OR d.vendor_name ILIKE $%d OR d.customer_name ILIKE $%d OR d.tracking_number ILIKE $%d)", argIndex, argIndex, argIndex, argIndex, argIndex)
		countQuery += fmt.Sprintf(" AND (d.sales_order_number ILIKE $%d OR d.purchase_order_number ILIKE $%d OR d.vendor_name ILIKE $%d OR d.customer_name ILIKE $%d OR d.tracking_number ILIKE $%d)", argIndex, argIndex, argIndex, argIndex, argIndex)
		args = append(args, "%"+filter.Search+"%")
		argIndex++
	}

	// Get total count
	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count dropship orders", "error", err)
		response.InternalError(c, "Failed to count dropship orders")
		return
	}

	// Add ordering and pagination
	query += " ORDER BY d.created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIndex, argIndex+1)
	args = append(args, limit, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query dropship orders", "error", err)
		response.InternalError(c, "Failed to fetch dropship orders")
		return
	}
	defer rows.Close()

	var orders []entity.DropshipOrder
	for rows.Next() {
		var o entity.DropshipOrder
		err := rows.Scan(
			&o.ID, &o.TenantID, &o.SalesOrderID, &o.SalesOrderNumber,
			&o.PurchaseOrderID, &o.PurchaseOrderNumber,
			&o.VendorID, &o.VendorName, &o.CustomerID, &o.CustomerName,
			&o.ShippingAddress, &o.Status, &o.ShippingMethod, &o.TrackingNumber,
			&o.Carrier, &o.EstimatedDelivery, &o.ShippedDate, &o.DeliveredDate,
			&o.Subtotal, &o.ShippingCost, &o.TotalCost, &o.Margin, &o.MarginPercent,
			&o.VendorNotes, &o.InternalNotes, &o.CustomerNotes,
			&o.CreatedBy, &o.CreatedAt, &o.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan dropship order", "error", err)
			continue
		}
		orders = append(orders, o)
	}

	response.Paginated(c, orders, page, limit, total)
}

// GetDropshipOrder returns a single dropship order with lines
func (h *Handler) GetDropshipOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Get order
	var o entity.DropshipOrder
	err = h.db.QueryRow(`
		SELECT
			id, tenant_id, sales_order_id, sales_order_number,
			purchase_order_id, purchase_order_number,
			vendor_id, vendor_name, customer_id, customer_name,
			shipping_address, status, shipping_method, tracking_number,
			carrier, estimated_delivery, shipped_date, delivered_date,
			subtotal, shipping_cost, total_cost, margin, margin_percent,
			vendor_notes, internal_notes, customer_notes,
			created_by, created_at, updated_at
		FROM dropship_orders
		WHERE id = $1 AND tenant_id = $2
	`, orderID, tenantID).Scan(
		&o.ID, &o.TenantID, &o.SalesOrderID, &o.SalesOrderNumber,
		&o.PurchaseOrderID, &o.PurchaseOrderNumber,
		&o.VendorID, &o.VendorName, &o.CustomerID, &o.CustomerName,
		&o.ShippingAddress, &o.Status, &o.ShippingMethod, &o.TrackingNumber,
		&o.Carrier, &o.EstimatedDelivery, &o.ShippedDate, &o.DeliveredDate,
		&o.Subtotal, &o.ShippingCost, &o.TotalCost, &o.Margin, &o.MarginPercent,
		&o.VendorNotes, &o.InternalNotes, &o.CustomerNotes,
		&o.CreatedBy, &o.CreatedAt, &o.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Dropship order not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get dropship order", "error", err)
		response.InternalError(c, "Failed to fetch dropship order")
		return
	}

	// Get lines
	rows, err := h.db.Query(`
		SELECT
			id, dropship_order_id, sales_order_line_id, purchase_order_line_id,
			product_id, product_name, sku, description,
			quantity, quantity_shipped, quantity_delivered,
			vendor_price, sale_price, line_cost, line_revenue, line_margin,
			status, notes, created_at, updated_at
		FROM dropship_order_lines
		WHERE dropship_order_id = $1
		ORDER BY created_at
	`, orderID)
	if err != nil {
		h.log.Error("Failed to get dropship lines", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var l entity.DropshipOrderLine
			err := rows.Scan(
				&l.ID, &l.DropshipOrderID, &l.SalesOrderLineID, &l.PurchaseOrderLineID,
				&l.ProductID, &l.ProductName, &l.SKU, &l.Description,
				&l.Quantity, &l.QuantityShipped, &l.QuantityDelivered,
				&l.VendorPrice, &l.SalePrice, &l.LineCost, &l.LineRevenue, &l.LineMargin,
				&l.Status, &l.Notes, &l.CreatedAt, &l.UpdatedAt,
			)
			if err != nil {
				h.log.Error("Failed to scan dropship line", "error", err)
				continue
			}
			o.Lines = append(o.Lines, l)
		}
	}

	response.Success(c, o)
}

// CreateDropshipOrder creates a new dropship order from a sales order
func (h *Handler) CreateDropshipOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
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

	var input entity.CreateDropshipOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	salesOrderID, err := uuid.Parse(input.SalesOrderID)
	if err != nil {
		response.BadRequest(c, "Invalid sales order ID")
		return
	}

	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor ID")
		return
	}

	// Get sales order details
	var so struct {
		OrderNumber     string
		CustomerID      uuid.UUID
		CustomerName    string
		ShippingAddress []byte
	}
	err = h.db.QueryRow(`
		SELECT so.order_number, so.customer_id, c.name, so.shipping_address
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.id = $1 AND so.tenant_id = $2
	`, salesOrderID, tenantID).Scan(&so.OrderNumber, &so.CustomerID, &so.CustomerName, &so.ShippingAddress)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get sales order", "error", err)
		response.InternalError(c, "Failed to fetch sales order")
		return
	}

	// Get vendor name
	var vendorName string
	h.db.QueryRow(`SELECT name FROM contacts WHERE id = $1`, vendorID).Scan(&vendorName)

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create dropship order")
		return
	}
	defer tx.Rollback()

	// Create dropship order
	orderID := uuid.New()
	now := time.Now()

	var subtotal, totalCost, margin float64

	// Calculate totals from lines
	for _, line := range input.Lines {
		// Get sales order line details
		var solPrice float64
		var productName string
		var sku sql.NullString
		soLineID, _ := uuid.Parse(line.SalesOrderLineID)

		err = h.db.QueryRow(`
			SELECT sol.unit_price, COALESCE(p.name, sol.description), p.sku
			FROM sales_order_lines sol
			LEFT JOIN products p ON sol.product_id = p.id
			WHERE sol.id = $1
		`, soLineID).Scan(&solPrice, &productName, &sku)
		if err != nil {
			continue
		}

		lineCost := line.Quantity * line.VendorPrice
		lineRevenue := line.Quantity * solPrice
		lineMargin := lineRevenue - lineCost

		subtotal += lineRevenue
		totalCost += lineCost
		margin += lineMargin
	}

	marginPercent := float64(0)
	if subtotal > 0 {
		marginPercent = (margin / subtotal) * 100
	}

	_, err = tx.Exec(`
		INSERT INTO dropship_orders (
			id, tenant_id, organization_id, sales_order_id, sales_order_number,
			vendor_id, vendor_name, customer_id, customer_name,
			shipping_address, status, shipping_method,
			subtotal, shipping_cost, total_cost, margin, margin_percent,
			vendor_notes, internal_notes,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
	`,
		orderID, tenantID, orgIDPtr, salesOrderID, so.OrderNumber,
		vendorID, vendorName, so.CustomerID, so.CustomerName,
		so.ShippingAddress, entity.DropshipStatusPending, input.ShippingMethod,
		subtotal, 0, totalCost, margin, marginPercent,
		input.VendorNotes, input.InternalNotes,
		userID, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create dropship order", "error", err)
		response.InternalError(c, "Failed to create dropship order")
		return
	}

	// Create lines
	for _, line := range input.Lines {
		soLineID, _ := uuid.Parse(line.SalesOrderLineID)
		var productID *uuid.UUID
		if line.ProductID != "" {
			pid, _ := uuid.Parse(line.ProductID)
			productID = &pid
		}

		// Get sales order line details
		var solPrice float64
		var productName string
		var sku sql.NullString

		err = h.db.QueryRow(`
			SELECT sol.unit_price, COALESCE(p.name, sol.description), p.sku
			FROM sales_order_lines sol
			LEFT JOIN products p ON sol.product_id = p.id
			WHERE sol.id = $1
		`, soLineID).Scan(&solPrice, &productName, &sku)
		if err != nil {
			productName = "Unknown Product"
		}

		lineCost := line.Quantity * line.VendorPrice
		lineRevenue := line.Quantity * solPrice
		lineMargin := lineRevenue - lineCost

		lineID := uuid.New()
		_, err = tx.Exec(`
			INSERT INTO dropship_order_lines (
				id, dropship_order_id, sales_order_line_id,
				product_id, product_name, sku,
				quantity, quantity_shipped, quantity_delivered,
				vendor_price, sale_price, line_cost, line_revenue, line_margin,
				status, notes, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		`,
			lineID, orderID, soLineID,
			productID, productName, sku,
			line.Quantity, 0, 0,
			line.VendorPrice, solPrice, lineCost, lineRevenue, lineMargin,
			entity.DropshipLineStatusPending, line.Notes, now, now,
		)
		if err != nil {
			h.log.Error("Failed to create dropship line", "error", err)
		}

		// Update sales order line to mark as dropship
		tx.Exec(`
			UPDATE sales_order_lines
			SET is_dropship = true, dropship_vendor_id = $1, dropship_status = 'pending'
			WHERE id = $2
		`, vendorID, soLineID)
	}

	// Update sales order has_dropship_items flag
	tx.Exec(`
		UPDATE sales_orders SET has_dropship_items = true, dropship_status = 'pending'
		WHERE id = $1
	`, salesOrderID)

	// If CreatePO is true, create a purchase order for the vendor
	var poID *uuid.UUID
	var poNumber string
	if input.CreatePO {
		poID, poNumber, err = h.createDropshipPO(tx, tenantID, orderID, vendorID, vendorName, input, userID)
		if err != nil {
			h.log.Error("Failed to create dropship PO", "error", err)
			// Continue without PO
		} else {
			// Update dropship order with PO reference
			tx.Exec(`
				UPDATE dropship_orders
				SET purchase_order_id = $1, purchase_order_number = $2, status = $3, updated_at = $4
				WHERE id = $5
			`, poID, poNumber, entity.DropshipStatusPOCreated, now, orderID)
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create dropship order")
		return
	}

	// Fetch and return the created order
	h.GetDropshipOrder(c)
}

// createDropshipPO creates a purchase order for a dropship order
func (h *Handler) createDropshipPO(tx *sql.Tx, tenantID, dropshipOrderID, vendorID uuid.UUID, vendorName string, input entity.CreateDropshipOrderInput, userID uuid.UUID) (*uuid.UUID, string, error) {
	poID := uuid.New()
	now := time.Now()

	// Generate PO number
	var lastNum int
	h.db.QueryRow(`
		SELECT COALESCE(MAX(CAST(SUBSTRING(order_number FROM 4) AS INTEGER)), 0)
		FROM purchase_orders WHERE tenant_id = $1 AND order_number LIKE 'PO-%'
	`, tenantID).Scan(&lastNum)
	poNumber := fmt.Sprintf("PO-%06d", lastNum+1)

	// Calculate totals
	var subtotal float64
	for _, line := range input.Lines {
		subtotal += line.Quantity * line.VendorPrice
	}

	// Create PO
	_, err := tx.Exec(`
		INSERT INTO purchase_orders (
			id, tenant_id, order_number, vendor_id,
			order_date, status, payment_status,
			subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
			exchange_rate, references, notes,
			requested_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`,
		poID, tenantID, poNumber, vendorID,
		now, "draft", "unpaid",
		subtotal, 0, 0, 0, subtotal,
		1, fmt.Sprintf(`{"dropship_order_id": "%s"}`, dropshipOrderID), input.VendorNotes,
		userID, now, now,
	)
	if err != nil {
		return nil, "", err
	}

	// Create PO lines
	lineNum := 1
	for _, line := range input.Lines {
		soLineID, _ := uuid.Parse(line.SalesOrderLineID)

		// Get product details
		var productID *uuid.UUID
		var productName string
		if line.ProductID != "" {
			pid, _ := uuid.Parse(line.ProductID)
			productID = &pid
		}

		h.db.QueryRow(`
			SELECT COALESCE(p.name, sol.description)
			FROM sales_order_lines sol
			LEFT JOIN products p ON sol.product_id = p.id
			WHERE sol.id = $1
		`, soLineID).Scan(&productName)

		lineID := uuid.New()
		lineTotal := line.Quantity * line.VendorPrice

		_, err = tx.Exec(`
			INSERT INTO po_lines (
				id, purchase_order_id, line_number,
				product_id, description, quantity, unit_price,
				discount_amount, tax_amount, line_total,
				quantity_received, quantity_invoiced,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		`,
			lineID, poID, lineNum,
			productID, productName, line.Quantity, line.VendorPrice,
			0, 0, lineTotal,
			0, 0,
			now, now,
		)
		if err != nil {
			return nil, "", err
		}

		// Update dropship line with PO line reference
		tx.Exec(`
			UPDATE dropship_order_lines
			SET purchase_order_line_id = $1, status = 'ordered', updated_at = $2
			WHERE dropship_order_id = $3 AND sales_order_line_id = $4
		`, lineID, now, dropshipOrderID, soLineID)

		lineNum++
	}

	return &poID, poNumber, nil
}

// UpdateDropshipOrder updates a dropship order
func (h *Handler) UpdateDropshipOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	var input entity.UpdateDropshipOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if input.Status != nil {
		updates = append(updates, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, *input.Status)
		argIndex++
	}
	if input.ShippingMethod != nil {
		updates = append(updates, fmt.Sprintf("shipping_method = $%d", argIndex))
		args = append(args, *input.ShippingMethod)
		argIndex++
	}
	if input.TrackingNumber != nil {
		updates = append(updates, fmt.Sprintf("tracking_number = $%d", argIndex))
		args = append(args, *input.TrackingNumber)
		argIndex++
	}
	if input.Carrier != nil {
		updates = append(updates, fmt.Sprintf("carrier = $%d", argIndex))
		args = append(args, *input.Carrier)
		argIndex++
	}
	if input.EstimatedDelivery != nil {
		estDate, err := time.Parse("2006-01-02", *input.EstimatedDelivery)
		if err == nil {
			updates = append(updates, fmt.Sprintf("estimated_delivery = $%d", argIndex))
			args = append(args, estDate)
			argIndex++
		}
	}
	if input.VendorNotes != nil {
		updates = append(updates, fmt.Sprintf("vendor_notes = $%d", argIndex))
		args = append(args, *input.VendorNotes)
		argIndex++
	}
	if input.InternalNotes != nil {
		updates = append(updates, fmt.Sprintf("internal_notes = $%d", argIndex))
		args = append(args, *input.InternalNotes)
		argIndex++
	}
	if input.ShippingCost != nil {
		updates = append(updates, fmt.Sprintf("shipping_cost = $%d", argIndex))
		args = append(args, *input.ShippingCost)
		argIndex++

		// Recalculate total cost
		updates = append(updates, fmt.Sprintf("total_cost = subtotal + $%d", argIndex-1))
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	args = append(args, orderID, tenantID)

	query := fmt.Sprintf(`
		UPDATE dropship_orders SET %s
		WHERE id = $%d AND tenant_id = $%d
	`, joinStrings(updates, ", "), argIndex, argIndex+1)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update dropship order", "error", err)
		response.InternalError(c, "Failed to update dropship order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Dropship order not found")
		return
	}

	h.GetDropshipOrder(c)
}

// MarkDropshipShipped marks a dropship order as shipped
func (h *Handler) MarkDropshipShipped(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	var input entity.MarkShippedInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	var estDelivery *time.Time
	if input.EstimatedDelivery != "" {
		est, err := time.Parse("2006-01-02", input.EstimatedDelivery)
		if err == nil {
			estDelivery = &est
		}
	}

	result, err := h.db.Exec(`
		UPDATE dropship_orders
		SET status = $1, tracking_number = $2, carrier = $3,
			estimated_delivery = $4, shipped_date = $5, updated_at = $6
		WHERE id = $7 AND tenant_id = $8 AND status IN ('pending', 'po_created', 'sent_to_vendor')
	`, entity.DropshipStatusShipped, input.TrackingNumber, input.Carrier, estDelivery, now, now, orderID, tenantID)
	if err != nil {
		h.log.Error("Failed to mark shipped", "error", err)
		response.InternalError(c, "Failed to update order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.BadRequest(c, "Order not found or cannot be marked as shipped")
		return
	}

	// Update lines status
	h.db.Exec(`
		UPDATE dropship_order_lines
		SET status = 'shipped', quantity_shipped = quantity, updated_at = $1
		WHERE dropship_order_id = $2
	`, now, orderID)

	// Update related sales order line statuses
	h.db.Exec(`
		UPDATE sales_order_lines sol
		SET dropship_status = 'shipped'
		FROM dropship_order_lines dol
		WHERE dol.sales_order_line_id = sol.id AND dol.dropship_order_id = $1
	`, orderID)

	h.GetDropshipOrder(c)
}

// MarkDropshipDelivered marks a dropship order as delivered
func (h *Handler) MarkDropshipDelivered(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	now := time.Now()

	result, err := h.db.Exec(`
		UPDATE dropship_orders
		SET status = $1, delivered_date = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5 AND status = 'shipped'
	`, entity.DropshipStatusDelivered, now, now, orderID, tenantID)
	if err != nil {
		h.log.Error("Failed to mark delivered", "error", err)
		response.InternalError(c, "Failed to update order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.BadRequest(c, "Order not found or cannot be marked as delivered")
		return
	}

	// Update lines status
	h.db.Exec(`
		UPDATE dropship_order_lines
		SET status = 'delivered', quantity_delivered = quantity, updated_at = $1
		WHERE dropship_order_id = $2
	`, now, orderID)

	// Update related sales order line statuses
	h.db.Exec(`
		UPDATE sales_order_lines sol
		SET dropship_status = 'delivered', quantity_delivered = sol.quantity
		FROM dropship_order_lines dol
		WHERE dol.sales_order_line_id = sol.id AND dol.dropship_order_id = $1
	`, orderID)

	h.GetDropshipOrder(c)
}

// CancelDropshipOrder cancels a dropship order
func (h *Handler) CancelDropshipOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	now := time.Now()

	result, err := h.db.Exec(`
		UPDATE dropship_orders
		SET status = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4 AND status NOT IN ('delivered', 'cancelled')
	`, entity.DropshipStatusCancelled, now, orderID, tenantID)
	if err != nil {
		h.log.Error("Failed to cancel dropship order", "error", err)
		response.InternalError(c, "Failed to cancel order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.BadRequest(c, "Order not found or cannot be cancelled")
		return
	}

	// Update lines status
	h.db.Exec(`
		UPDATE dropship_order_lines
		SET status = 'cancelled', updated_at = $1
		WHERE dropship_order_id = $2
	`, now, orderID)

	// Reset sales order line dropship status
	h.db.Exec(`
		UPDATE sales_order_lines sol
		SET is_dropship = false, dropship_status = NULL, dropship_vendor_id = NULL
		FROM dropship_order_lines dol
		WHERE dol.sales_order_line_id = sol.id AND dol.dropship_order_id = $1
	`, orderID)

	response.Success(c, gin.H{"message": "Dropship order cancelled"})
}

// GetDropshipStats returns dropshipping statistics
func (h *Handler) GetDropshipStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var stats entity.DropshipStats
	err := h.db.QueryRow(`
		SELECT
			COUNT(*) as total_orders,
			COUNT(*) FILTER (WHERE status = 'pending' OR status = 'po_created' OR status = 'sent_to_vendor') as pending_orders,
			COUNT(*) FILTER (WHERE status = 'shipped') as shipped_orders,
			COUNT(*) FILTER (WHERE status = 'delivered') as delivered_orders,
			COALESCE(SUM(subtotal), 0) as total_revenue,
			COALESCE(SUM(total_cost), 0) as total_cost,
			COALESCE(SUM(margin), 0) as total_margin,
			COALESCE(AVG(margin_percent), 0) as avg_margin_pct
		FROM dropship_orders
		WHERE tenant_id = $1 AND status != 'cancelled'
	`, tenantID).Scan(
		&stats.TotalOrders, &stats.PendingOrders, &stats.ShippedOrders, &stats.DeliveredOrders,
		&stats.TotalRevenue, &stats.TotalCost, &stats.TotalMargin, &stats.AvgMarginPct,
	)
	if err != nil {
		h.log.Error("Failed to get dropship stats", "error", err)
		response.InternalError(c, "Failed to get statistics")
		return
	}

	response.Success(c, stats)
}

// ==================== DROPSHIP VENDOR SETTINGS ====================

// ListDropshipVendors returns vendors with dropship settings
func (h *Handler) ListDropshipVendors(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT
			dvs.id, dvs.tenant_id, dvs.vendor_id, c.name as vendor_name,
			dvs.is_dropship_enabled, dvs.auto_send_orders,
			dvs.notification_email, dvs.order_format,
			dvs.default_lead_time_days, dvs.ships_direct_to_customer, dvs.provides_tracking,
			dvs.markup_type, dvs.default_markup,
			dvs.min_order_value, dvs.min_order_quantity,
			dvs.notes, dvs.is_active, dvs.created_at, dvs.updated_at
		FROM dropship_vendor_settings dvs
		JOIN contacts c ON dvs.vendor_id = c.id
		WHERE dvs.tenant_id = $1
		ORDER BY c.name
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to query dropship vendors", "error", err)
		response.InternalError(c, "Failed to fetch vendors")
		return
	}
	defer rows.Close()

	var vendors []entity.DropshipVendorSettings
	for rows.Next() {
		var v entity.DropshipVendorSettings
		err := rows.Scan(
			&v.ID, &v.TenantID, &v.VendorID, &v.VendorName,
			&v.IsDropshipEnabled, &v.AutoSendOrders,
			&v.NotificationEmail, &v.OrderFormat,
			&v.DefaultLeadTimeDays, &v.ShipsDirectToCustomer, &v.ProvidesTracking,
			&v.MarkupType, &v.DefaultMarkup,
			&v.MinOrderValue, &v.MinOrderQuantity,
			&v.Notes, &v.IsActive, &v.CreatedAt, &v.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan vendor", "error", err)
			continue
		}
		vendors = append(vendors, v)
	}

	response.Success(c, vendors)
}

// CreateDropshipVendorSettings creates dropship settings for a vendor
func (h *Handler) CreateDropshipVendorSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateDropshipVendorSettingsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor ID")
		return
	}

	// Check if vendor exists and is a vendor type
	var vendorExists bool
	h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND type IN ('vendor', 'both'))`, vendorID).Scan(&vendorExists)
	if !vendorExists {
		response.BadRequest(c, "Vendor not found or not a valid vendor contact")
		return
	}

	// Set defaults
	isEnabled := true
	if input.IsDropshipEnabled != nil {
		isEnabled = *input.IsDropshipEnabled
	}
	autoSend := false
	if input.AutoSendOrders != nil {
		autoSend = *input.AutoSendOrders
	}
	orderFormat := "standard"
	if input.OrderFormat != "" {
		orderFormat = input.OrderFormat
	}
	leadTime := 3
	if input.DefaultLeadTimeDays != nil {
		leadTime = *input.DefaultLeadTimeDays
	}
	shipsDirect := true
	if input.ShipsDirectToCustomer != nil {
		shipsDirect = *input.ShipsDirectToCustomer
	}
	tracking := true
	if input.ProvidesTracking != nil {
		tracking = *input.ProvidesTracking
	}
	markupType := "percentage"
	if input.MarkupType != "" {
		markupType = input.MarkupType
	}
	markup := 20.0
	if input.DefaultMarkup != nil {
		markup = *input.DefaultMarkup
	}

	id := uuid.New()
	now := time.Now()

	_, err = h.db.Exec(`
		INSERT INTO dropship_vendor_settings (
			id, tenant_id, vendor_id,
			is_dropship_enabled, auto_send_orders,
			notification_email, order_format,
			default_lead_time_days, ships_direct_to_customer, provides_tracking,
			markup_type, default_markup,
			min_order_value, min_order_quantity,
			notes, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (tenant_id, vendor_id) DO UPDATE SET
			is_dropship_enabled = EXCLUDED.is_dropship_enabled,
			auto_send_orders = EXCLUDED.auto_send_orders,
			notification_email = EXCLUDED.notification_email,
			order_format = EXCLUDED.order_format,
			default_lead_time_days = EXCLUDED.default_lead_time_days,
			ships_direct_to_customer = EXCLUDED.ships_direct_to_customer,
			provides_tracking = EXCLUDED.provides_tracking,
			markup_type = EXCLUDED.markup_type,
			default_markup = EXCLUDED.default_markup,
			min_order_value = EXCLUDED.min_order_value,
			min_order_quantity = EXCLUDED.min_order_quantity,
			notes = EXCLUDED.notes,
			updated_at = EXCLUDED.updated_at
	`,
		id, tenantID, vendorID,
		isEnabled, autoSend,
		input.NotificationEmail, orderFormat,
		leadTime, shipsDirect, tracking,
		markupType, markup,
		input.MinOrderValue, input.MinOrderQuantity,
		input.Notes, true, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create vendor settings", "error", err)
		response.InternalError(c, "Failed to create vendor settings")
		return
	}

	response.Created(c, gin.H{"id": id, "message": "Vendor settings created"})
}

// UpdateDropshipVendorSettings updates dropship settings for a vendor
func (h *Handler) UpdateDropshipVendorSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	settingsID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid settings ID")
		return
	}

	var input entity.UpdateDropshipVendorSettingsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if input.IsDropshipEnabled != nil {
		updates = append(updates, fmt.Sprintf("is_dropship_enabled = $%d", argIndex))
		args = append(args, *input.IsDropshipEnabled)
		argIndex++
	}
	if input.AutoSendOrders != nil {
		updates = append(updates, fmt.Sprintf("auto_send_orders = $%d", argIndex))
		args = append(args, *input.AutoSendOrders)
		argIndex++
	}
	if input.NotificationEmail != nil {
		updates = append(updates, fmt.Sprintf("notification_email = $%d", argIndex))
		args = append(args, *input.NotificationEmail)
		argIndex++
	}
	if input.OrderFormat != nil {
		updates = append(updates, fmt.Sprintf("order_format = $%d", argIndex))
		args = append(args, *input.OrderFormat)
		argIndex++
	}
	if input.DefaultLeadTimeDays != nil {
		updates = append(updates, fmt.Sprintf("default_lead_time_days = $%d", argIndex))
		args = append(args, *input.DefaultLeadTimeDays)
		argIndex++
	}
	if input.ShipsDirectToCustomer != nil {
		updates = append(updates, fmt.Sprintf("ships_direct_to_customer = $%d", argIndex))
		args = append(args, *input.ShipsDirectToCustomer)
		argIndex++
	}
	if input.ProvidesTracking != nil {
		updates = append(updates, fmt.Sprintf("provides_tracking = $%d", argIndex))
		args = append(args, *input.ProvidesTracking)
		argIndex++
	}
	if input.MarkupType != nil {
		updates = append(updates, fmt.Sprintf("markup_type = $%d", argIndex))
		args = append(args, *input.MarkupType)
		argIndex++
	}
	if input.DefaultMarkup != nil {
		updates = append(updates, fmt.Sprintf("default_markup = $%d", argIndex))
		args = append(args, *input.DefaultMarkup)
		argIndex++
	}
	if input.MinOrderValue != nil {
		updates = append(updates, fmt.Sprintf("min_order_value = $%d", argIndex))
		args = append(args, *input.MinOrderValue)
		argIndex++
	}
	if input.MinOrderQuantity != nil {
		updates = append(updates, fmt.Sprintf("min_order_quantity = $%d", argIndex))
		args = append(args, *input.MinOrderQuantity)
		argIndex++
	}
	if input.Notes != nil {
		updates = append(updates, fmt.Sprintf("notes = $%d", argIndex))
		args = append(args, *input.Notes)
		argIndex++
	}
	if input.IsActive != nil {
		updates = append(updates, fmt.Sprintf("is_active = $%d", argIndex))
		args = append(args, *input.IsActive)
		argIndex++
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	args = append(args, settingsID, tenantID)

	query := fmt.Sprintf(`
		UPDATE dropship_vendor_settings SET %s
		WHERE id = $%d AND tenant_id = $%d
	`, joinStrings(updates, ", "), argIndex, argIndex+1)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update vendor settings", "error", err)
		response.InternalError(c, "Failed to update vendor settings")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Vendor settings not found")
		return
	}

	response.Success(c, gin.H{"message": "Vendor settings updated"})
}

// DeleteDropshipVendorSettings deletes dropship settings for a vendor
func (h *Handler) DeleteDropshipVendorSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	settingsID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid settings ID")
		return
	}

	result, err := h.db.Exec(`
		DELETE FROM dropship_vendor_settings
		WHERE id = $1 AND tenant_id = $2
	`, settingsID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete vendor settings", "error", err)
		response.InternalError(c, "Failed to delete vendor settings")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Vendor settings not found")
		return
	}

	response.Success(c, gin.H{"message": "Vendor settings deleted"})
}

// ==================== DROPSHIP PRODUCT VENDORS ====================

// ListDropshipProductVendors returns vendors for a specific product
func (h *Handler) ListDropshipProductVendors(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	productID := c.Query("product_id")
	vendorID := c.Query("vendor_id")

	query := `
		SELECT
			dpv.id, dpv.tenant_id, dpv.product_id, dpv.vendor_id,
			dpv.vendor_price, dpv.currency, dpv.lead_time_days,
			dpv.is_available, dpv.min_order_qty, dpv.priority,
			dpv.vendor_sku, dpv.is_active, dpv.created_at, dpv.updated_at,
			p.name as product_name, c.name as vendor_name
		FROM dropship_product_vendors dpv
		JOIN products p ON dpv.product_id = p.id
		JOIN contacts c ON dpv.vendor_id = c.id
		WHERE dpv.tenant_id = $1
	`
	args := []interface{}{tenantID}
	argIndex := 2

	if productID != "" {
		pid, err := uuid.Parse(productID)
		if err == nil {
			query += fmt.Sprintf(" AND dpv.product_id = $%d", argIndex)
			args = append(args, pid)
			argIndex++
		}
	}

	if vendorID != "" {
		vid, err := uuid.Parse(vendorID)
		if err == nil {
			query += fmt.Sprintf(" AND dpv.vendor_id = $%d", argIndex)
			args = append(args, vid)
			argIndex++
		}
	}

	query += " ORDER BY dpv.priority, c.name"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query product vendors", "error", err)
		response.InternalError(c, "Failed to fetch product vendors")
		return
	}
	defer rows.Close()

	var items []entity.DropshipProductVendor
	for rows.Next() {
		var pv entity.DropshipProductVendor
		err := rows.Scan(
			&pv.ID, &pv.TenantID, &pv.ProductID, &pv.VendorID,
			&pv.VendorPrice, &pv.Currency, &pv.LeadTimeDays,
			&pv.IsAvailable, &pv.MinOrderQty, &pv.Priority,
			&pv.VendorSKU, &pv.IsActive, &pv.CreatedAt, &pv.UpdatedAt,
			&pv.ProductName, &pv.VendorName,
		)
		if err != nil {
			h.log.Error("Failed to scan product vendor", "error", err)
			continue
		}
		items = append(items, pv)
	}

	response.Success(c, items)
}

// CreateDropshipProductVendor links a product to a dropship vendor
func (h *Handler) CreateDropshipProductVendor(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateDropshipProductVendorInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor ID")
		return
	}

	// Set defaults
	currency := "UZS"
	if input.Currency != "" {
		currency = input.Currency
	}
	leadTime := 3
	if input.LeadTimeDays != nil {
		leadTime = *input.LeadTimeDays
	}
	minQty := 1.0
	if input.MinOrderQty != nil {
		minQty = *input.MinOrderQty
	}
	priority := 100
	if input.Priority != nil {
		priority = *input.Priority
	}

	id := uuid.New()
	now := time.Now()

	_, err = h.db.Exec(`
		INSERT INTO dropship_product_vendors (
			id, tenant_id, product_id, vendor_id,
			vendor_price, currency, lead_time_days,
			is_available, min_order_qty, priority,
			vendor_sku, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (tenant_id, product_id, vendor_id) DO UPDATE SET
			vendor_price = EXCLUDED.vendor_price,
			currency = EXCLUDED.currency,
			lead_time_days = EXCLUDED.lead_time_days,
			min_order_qty = EXCLUDED.min_order_qty,
			priority = EXCLUDED.priority,
			vendor_sku = EXCLUDED.vendor_sku,
			is_available = true,
			is_active = true,
			updated_at = EXCLUDED.updated_at
	`,
		id, tenantID, productID, vendorID,
		input.VendorPrice, currency, leadTime,
		true, minQty, priority,
		input.VendorSKU, true, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create product vendor link", "error", err)
		response.InternalError(c, "Failed to link product to vendor")
		return
	}

	// Also mark the product as dropshippable
	h.db.Exec(`
		UPDATE products SET is_dropshippable = true WHERE id = $1
	`, productID)

	response.Created(c, gin.H{"id": id, "message": "Product linked to vendor"})
}

// UpdateDropshipProductVendor updates a product-vendor link
func (h *Handler) UpdateDropshipProductVendor(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	linkID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid link ID")
		return
	}

	var input entity.UpdateDropshipProductVendorInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if input.VendorPrice != nil {
		updates = append(updates, fmt.Sprintf("vendor_price = $%d", argIndex))
		args = append(args, *input.VendorPrice)
		argIndex++
	}
	if input.Currency != nil {
		updates = append(updates, fmt.Sprintf("currency = $%d", argIndex))
		args = append(args, *input.Currency)
		argIndex++
	}
	if input.LeadTimeDays != nil {
		updates = append(updates, fmt.Sprintf("lead_time_days = $%d", argIndex))
		args = append(args, *input.LeadTimeDays)
		argIndex++
	}
	if input.IsAvailable != nil {
		updates = append(updates, fmt.Sprintf("is_available = $%d", argIndex))
		args = append(args, *input.IsAvailable)
		argIndex++
	}
	if input.MinOrderQty != nil {
		updates = append(updates, fmt.Sprintf("min_order_qty = $%d", argIndex))
		args = append(args, *input.MinOrderQty)
		argIndex++
	}
	if input.Priority != nil {
		updates = append(updates, fmt.Sprintf("priority = $%d", argIndex))
		args = append(args, *input.Priority)
		argIndex++
	}
	if input.VendorSKU != nil {
		updates = append(updates, fmt.Sprintf("vendor_sku = $%d", argIndex))
		args = append(args, *input.VendorSKU)
		argIndex++
	}
	if input.IsActive != nil {
		updates = append(updates, fmt.Sprintf("is_active = $%d", argIndex))
		args = append(args, *input.IsActive)
		argIndex++
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	args = append(args, linkID, tenantID)

	query := fmt.Sprintf(`
		UPDATE dropship_product_vendors SET %s
		WHERE id = $%d AND tenant_id = $%d
	`, joinStrings(updates, ", "), argIndex, argIndex+1)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update product vendor", "error", err)
		response.InternalError(c, "Failed to update link")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Product-vendor link not found")
		return
	}

	response.Success(c, gin.H{"message": "Product-vendor link updated"})
}

// DeleteDropshipProductVendor removes a product-vendor link
func (h *Handler) DeleteDropshipProductVendor(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	linkID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid link ID")
		return
	}

	result, err := h.db.Exec(`
		DELETE FROM dropship_product_vendors
		WHERE id = $1 AND tenant_id = $2
	`, linkID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete product vendor", "error", err)
		response.InternalError(c, "Failed to delete link")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Product-vendor link not found")
		return
	}

	response.Success(c, gin.H{"message": "Product-vendor link deleted"})
}

// GetDropshippableProducts returns products that can be dropshipped
func (h *Handler) GetDropshippableProducts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT
			p.id, p.name, p.sku, p.is_dropshippable, p.dropship_only,
			p.default_dropship_vendor_id, dv.name as default_vendor_name,
			(SELECT COUNT(*) FROM dropship_product_vendors dpv WHERE dpv.product_id = p.id AND dpv.is_active = true) as available_vendors,
			(SELECT MIN(vendor_price) FROM dropship_product_vendors dpv WHERE dpv.product_id = p.id AND dpv.is_active = true) as lowest_price,
			(SELECT MIN(lead_time_days) FROM dropship_product_vendors dpv WHERE dpv.product_id = p.id AND dpv.is_active = true) as fastest_lead_time
		FROM products p
		LEFT JOIN contacts dv ON p.default_dropship_vendor_id = dv.id
		WHERE p.tenant_id = $1 AND p.is_dropshippable = true AND p.deleted_at IS NULL
		ORDER BY p.name
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to query dropshippable products", "error", err)
		response.InternalError(c, "Failed to fetch products")
		return
	}
	defer rows.Close()

	var products []entity.DropshipableProduct
	for rows.Next() {
		var p entity.DropshipableProduct
		var defaultVendorName sql.NullString
		err := rows.Scan(
			&p.ProductID, &p.ProductName, &p.SKU, &p.IsDropshippable, &p.DropshipOnly,
			&p.DefaultVendorID, &defaultVendorName,
			&p.AvailableVendors, &p.LowestVendorPrice, &p.FastestLeadTimeDays,
		)
		if err != nil {
			h.log.Error("Failed to scan product", "error", err)
			continue
		}
		if defaultVendorName.Valid {
			p.DefaultVendorName = defaultVendorName.String
		}
		products = append(products, p)
	}

	response.Success(c, products)
}
