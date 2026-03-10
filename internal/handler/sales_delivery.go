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

// ListDeliveryOrders returns paginated list of sales delivery orders
func (h *Handler) ListDeliveryOrders(c *gin.Context) {
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
		SELECT sdo.id, sdo.tenant_id, sdo.delivery_number, sdo.sales_order_id, sdo.so_number,
			   sdo.customer_id, sdo.customer_name, sdo.delivery_date, sdo.scheduled_date,
			   sdo.warehouse_id, sdo.shipping_method, sdo.tracking_number, sdo.carrier,
			   sdo.status, sdo.notes, sdo.created_at, sdo.updated_at,
			   w.name as warehouse_name
		FROM sales_delivery_orders sdo
		LEFT JOIN warehouses w ON sdo.warehouse_id = w.id
		WHERE sdo.tenant_id = $1 AND sdo.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM sales_delivery_orders WHERE tenant_id = $1 AND deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by sales_order_id
	if soID := c.Query("sales_order_id"); soID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.sales_order_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND sales_order_id = $%d", argCount)
		args = append(args, soID)
	}

	// Filter by customer_id
	if customerID := c.Query("customer_id"); customerID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.customer_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND customer_id = $%d", argCount)
		args = append(args, customerID)
	}

	// Filter by date range
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.delivery_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND delivery_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.delivery_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND delivery_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(sdo.delivery_number) LIKE $%d OR LOWER(sdo.so_number) LIKE $%d OR LOWER(sdo.customer_name) LIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(delivery_number) LIKE $%d OR LOWER(so_number) LIKE $%d OR LOWER(customer_name) LIKE $%d)", argCount, argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count delivery orders")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY sdo.created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to fetch delivery orders", "error", err)
		response.InternalError(c, "Failed to fetch delivery orders")
		return
	}
	defer rows.Close()

	var deliveryOrders []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan, salesOrderID uuid.UUID
		var deliveryNumber, soNumber, status string
		var customerID, warehouseID sql.NullString
		var customerName, shippingMethod, trackingNumber, carrier, notes, warehouseName sql.NullString
		var deliveryDate time.Time
		var scheduledDate sql.NullTime
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &tenantIDScan, &deliveryNumber, &salesOrderID, &soNumber,
			&customerID, &customerName, &deliveryDate, &scheduledDate,
			&warehouseID, &shippingMethod, &trackingNumber, &carrier,
			&status, &notes, &createdAt, &updatedAt,
			&warehouseName,
		)
		if err != nil {
			continue
		}

		do := map[string]interface{}{
			"id":              id.String(),
			"tenant_id":       tenantIDScan.String(),
			"delivery_number": deliveryNumber,
			"sales_order_id":  salesOrderID.String(),
			"so_number":       soNumber,
			"delivery_date":   deliveryDate.Format("2006-01-02"),
			"status":          status,
			"created_at":      createdAt,
			"updated_at":      updatedAt,
		}

		if customerID.Valid {
			do["customer_id"] = customerID.String
		}
		if customerName.Valid {
			do["customer_name"] = customerName.String
		}
		if scheduledDate.Valid {
			do["scheduled_date"] = scheduledDate.Time.Format("2006-01-02")
		}
		if warehouseID.Valid {
			do["warehouse_id"] = warehouseID.String
		}
		if warehouseName.Valid {
			do["warehouse_name"] = warehouseName.String
		}
		if shippingMethod.Valid {
			do["shipping_method"] = shippingMethod.String
		}
		if trackingNumber.Valid {
			do["tracking_number"] = trackingNumber.String
		}
		if carrier.Valid {
			do["carrier"] = carrier.String
		}
		if notes.Valid {
			do["notes"] = notes.String
		}

		deliveryOrders = append(deliveryOrders, do)
	}

	response.Paginated(c, deliveryOrders, page, pageSize, total)
}

// CreateDeliveryOrder creates a new delivery order from a sales order
func (h *Handler) CreateDeliveryOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Get organization ID from middleware header
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var input struct {
		SalesOrderID   string `json:"sales_order_id" binding:"required"`
		DeliveryDate   string `json:"delivery_date"`
		ScheduledDate  string `json:"scheduled_date"`
		WarehouseID    string `json:"warehouse_id"`
		ShippingMethod string `json:"shipping_method"`
		Carrier        string `json:"carrier"`
		Notes          string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse sales order ID
	salesOrderID, err := uuid.Parse(input.SalesOrderID)
	if err != nil {
		response.BadRequest(c, "Invalid sales_order_id")
		return
	}

	// Get sales order details
	var soNumber, soStatus string
	var soCustomerID sql.NullString
	var soCustomerName sql.NullString
	var soWarehouseID sql.NullString
	var soExpectedDate sql.NullTime

	err = h.db.QueryRow(`
		SELECT so.order_number, so.status, so.customer_id, c.name, so.warehouse_id, so.expected_date
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.id = $1 AND so.tenant_id = $2 AND so.deleted_at IS NULL
	`, salesOrderID, tenantID).Scan(&soNumber, &soStatus, &soCustomerID, &soCustomerName, &soWarehouseID, &soExpectedDate)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch sales order", "error", err)
		response.InternalError(c, "Failed to fetch sales order")
		return
	}

	// Validate SO status - must be confirmed or processing
	if soStatus != "confirmed" && soStatus != "processing" {
		response.BadRequest(c, "Sales order must be confirmed or processing to create delivery order")
		return
	}

	// Generate delivery number
	now := time.Now()
	var doCount int
	h.db.QueryRow("SELECT COUNT(*) FROM sales_delivery_orders WHERE tenant_id = $1", tenantID).Scan(&doCount)
	deliveryNumber := fmt.Sprintf("DO%05d", doCount+1)

	// Use provided warehouse or SO warehouse
	warehouseID := input.WarehouseID
	if warehouseID == "" && soWarehouseID.Valid {
		warehouseID = soWarehouseID.String
	}

	// Use provided delivery date or today
	deliveryDate := now
	if input.DeliveryDate != "" {
		parsed, err := time.Parse("2006-01-02", input.DeliveryDate)
		if err == nil {
			deliveryDate = parsed
		}
	}

	// Create delivery order
	doID := uuid.New()
	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	query := `
		INSERT INTO sales_delivery_orders (
			id, tenant_id, organization_id, delivery_number, sales_order_id, so_number,
			customer_id, customer_name, delivery_date, scheduled_date,
			warehouse_id, shipping_method, carrier, notes,
			status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 'draft', $15, $16, $16)`

	var customerIDVal interface{}
	if soCustomerID.Valid {
		customerIDVal = soCustomerID.String
	}

	var warehouseIDVal interface{}
	if warehouseID != "" {
		warehouseIDVal = warehouseID
	}

	var scheduledDateVal interface{}
	if input.ScheduledDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ScheduledDate); err == nil {
			scheduledDateVal = parsed
		}
	} else if soExpectedDate.Valid {
		scheduledDateVal = soExpectedDate.Time
	}

	_, err = h.db.Exec(query,
		doID, tenantID, orgIDPtr, deliveryNumber, salesOrderID, soNumber,
		customerIDVal, soCustomerName.String, deliveryDate, scheduledDateVal,
		warehouseIDVal, input.ShippingMethod, input.Carrier, input.Notes,
		createdBy, now,
	)
	if err != nil {
		h.log.Error("Failed to create delivery order", "error", err)
		response.InternalError(c, "Failed to create delivery order")
		return
	}

	// Copy SO lines to DO lines (only undelivered quantities)
	_, err = h.db.Exec(`
		INSERT INTO sales_delivery_order_lines (
			id, delivery_order_id, so_line_id, product_id, product_name,
			quantity_ordered, quantity_to_deliver, unit_price, warehouse_id, created_at
		)
		SELECT uuid_generate_v4(), $1, sol.id, sol.product_id, p.name,
			   sol.quantity, sol.quantity - COALESCE(sol.quantity_delivered, 0),
			   sol.unit_price, $2, NOW()
		FROM sales_order_lines sol
		JOIN products p ON p.id = sol.product_id
		WHERE sol.sales_order_id = $3 AND sol.quantity > COALESCE(sol.quantity_delivered, 0)
	`, doID, warehouseIDVal, salesOrderID)

	if err != nil {
		h.log.Error("Failed to create delivery order lines", "error", err)
	}

	// Return created delivery order
	c.Params = append(c.Params, gin.Param{Key: "id", Value: doID.String()})
	h.GetDeliveryOrder(c)
}

// GetDeliveryOrder returns a single delivery order with its lines
func (h *Handler) GetDeliveryOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	doID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid delivery order ID")
		return
	}

	// Get delivery order
	query := `
		SELECT sdo.id, sdo.tenant_id, sdo.delivery_number, sdo.sales_order_id, sdo.so_number,
			   sdo.customer_id, sdo.customer_name, sdo.delivery_date, sdo.scheduled_date,
			   sdo.warehouse_id, sdo.shipping_method, sdo.tracking_number, sdo.carrier,
			   sdo.status, sdo.notes, sdo.created_at, sdo.updated_at,
			   w.name as warehouse_name
		FROM sales_delivery_orders sdo
		LEFT JOIN warehouses w ON sdo.warehouse_id = w.id
		WHERE sdo.id = $1 AND sdo.tenant_id = $2 AND sdo.deleted_at IS NULL`

	var id, tenantIDScan, salesOrderID uuid.UUID
	var deliveryNumber, soNumber, status string
	var customerID, warehouseID sql.NullString
	var customerName, shippingMethod, trackingNumber, carrier, notes, warehouseName sql.NullString
	var deliveryDate time.Time
	var scheduledDate sql.NullTime
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(query, doID, tenantID).Scan(
		&id, &tenantIDScan, &deliveryNumber, &salesOrderID, &soNumber,
		&customerID, &customerName, &deliveryDate, &scheduledDate,
		&warehouseID, &shippingMethod, &trackingNumber, &carrier,
		&status, &notes, &createdAt, &updatedAt,
		&warehouseName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Delivery order")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch delivery order", "error", err)
		response.InternalError(c, "Failed to fetch delivery order")
		return
	}

	do := map[string]interface{}{
		"id":              id.String(),
		"tenant_id":       tenantIDScan.String(),
		"delivery_number": deliveryNumber,
		"sales_order_id":  salesOrderID.String(),
		"so_number":       soNumber,
		"delivery_date":   deliveryDate.Format("2006-01-02"),
		"status":          status,
		"created_at":      createdAt,
		"updated_at":      updatedAt,
	}

	if customerID.Valid {
		do["customer_id"] = customerID.String
	}
	if customerName.Valid {
		do["customer_name"] = customerName.String
	}
	if scheduledDate.Valid {
		do["scheduled_date"] = scheduledDate.Time.Format("2006-01-02")
	}
	if warehouseID.Valid {
		do["warehouse_id"] = warehouseID.String
	}
	if warehouseName.Valid {
		do["warehouse_name"] = warehouseName.String
	}
	if shippingMethod.Valid {
		do["shipping_method"] = shippingMethod.String
	}
	if trackingNumber.Valid {
		do["tracking_number"] = trackingNumber.String
	}
	if carrier.Valid {
		do["carrier"] = carrier.String
	}
	if notes.Valid {
		do["notes"] = notes.String
	}

	// Get delivery order lines
	linesQuery := `
		SELECT dol.id, dol.so_line_id, dol.product_id, dol.product_name,
			   dol.quantity_ordered, dol.quantity_to_deliver, dol.quantity_delivered,
			   dol.unit_price, dol.warehouse_id, dol.notes
		FROM sales_delivery_order_lines dol
		WHERE dol.delivery_order_id = $1
		ORDER BY dol.created_at`

	rows, err := h.db.Query(linesQuery, doID)
	if err != nil {
		h.log.Error("Failed to fetch delivery order lines", "error", err)
	} else {
		defer rows.Close()
		var lines []map[string]interface{}
		for rows.Next() {
			var lineID, productID uuid.UUID
			var soLineID, lineWarehouseID sql.NullString
			var productName sql.NullString
			var qtyOrdered, qtyToDeliver, qtyDelivered float64
			var unitPrice sql.NullFloat64
			var lineNotes sql.NullString

			err := rows.Scan(
				&lineID, &soLineID, &productID, &productName,
				&qtyOrdered, &qtyToDeliver, &qtyDelivered,
				&unitPrice, &lineWarehouseID, &lineNotes,
			)
			if err != nil {
				continue
			}

			line := map[string]interface{}{
				"id":                  lineID.String(),
				"product_id":          productID.String(),
				"quantity_ordered":    qtyOrdered,
				"quantity_to_deliver": qtyToDeliver,
				"quantity_delivered":  qtyDelivered,
			}
			if soLineID.Valid {
				line["so_line_id"] = soLineID.String
			}
			if productName.Valid {
				line["product_name"] = productName.String
			}
			if unitPrice.Valid {
				line["unit_price"] = unitPrice.Float64
			}
			if lineWarehouseID.Valid {
				line["warehouse_id"] = lineWarehouseID.String
			}
			if lineNotes.Valid {
				line["notes"] = lineNotes.String
			}
			lines = append(lines, line)
		}
		do["lines"] = lines
	}

	response.Success(c, do)
}

// UpdateDeliveryOrder updates delivery order details
func (h *Handler) UpdateDeliveryOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	doID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid delivery order ID")
		return
	}

	var input struct {
		DeliveryDate   string `json:"delivery_date"`
		ScheduledDate  string `json:"scheduled_date"`
		ShippingMethod string `json:"shipping_method"`
		TrackingNumber string `json:"tracking_number"`
		Carrier        string `json:"carrier"`
		Notes          string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_delivery_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", doID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Delivery order")
		return
	}
	if currentStatus != "draft" && currentStatus != "ready" {
		response.BadRequest(c, "Can only update delivery orders in draft or ready status")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.DeliveryDate != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("delivery_date = $%d", argCount))
		if dd, err := time.Parse("2006-01-02", input.DeliveryDate); err == nil {
			args = append(args, dd)
		}
	}
	if input.ScheduledDate != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("scheduled_date = $%d", argCount))
		if sd, err := time.Parse("2006-01-02", input.ScheduledDate); err == nil {
			args = append(args, sd)
		}
	}
	if input.ShippingMethod != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("shipping_method = $%d", argCount))
		args = append(args, input.ShippingMethod)
	}
	if input.TrackingNumber != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("tracking_number = $%d", argCount))
		args = append(args, input.TrackingNumber)
	}
	if input.Carrier != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("carrier = $%d", argCount))
		args = append(args, input.Carrier)
	}
	if input.Notes != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, input.Notes)
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
	args = append(args, doID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE sales_delivery_orders SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update delivery order", "error", err)
		response.InternalError(c, "Failed to update delivery order")
		return
	}

	h.GetDeliveryOrder(c)
}

// ValidateDeliveryOrder ships the delivery order and updates inventory
func (h *Handler) ValidateDeliveryOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	doID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid delivery order ID")
		return
	}

	// Get delivery order details
	var currentStatus string
	var salesOrderID uuid.UUID
	var warehouseID sql.NullString

	err = h.db.QueryRow(`
		SELECT status, sales_order_id, warehouse_id
		FROM sales_delivery_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, doID, tenantID).Scan(&currentStatus, &salesOrderID, &warehouseID)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Delivery order")
		return
	}
	if currentStatus != "draft" && currentStatus != "ready" {
		response.BadRequest(c, "Can only validate delivery orders in draft or ready status")
		return
	}

	now := time.Now()

	// Get delivery order lines
	rows, err := h.db.Query(`
		SELECT id, so_line_id, product_id, quantity_to_deliver, warehouse_id
		FROM sales_delivery_order_lines
		WHERE delivery_order_id = $1 AND quantity_to_deliver > 0
	`, doID)
	if err != nil {
		h.log.Error("Failed to fetch delivery order lines", "error", err)
		response.InternalError(c, "Failed to fetch delivery order lines")
		return
	}
	defer rows.Close()

	type doLine struct {
		ID              uuid.UUID
		SOLineID        sql.NullString
		ProductID       uuid.UUID
		QtyToDeliver    float64
		LineWarehouseID sql.NullString
	}
	var lines []doLine
	for rows.Next() {
		var line doLine
		err := rows.Scan(&line.ID, &line.SOLineID, &line.ProductID, &line.QtyToDeliver, &line.LineWarehouseID)
		if err != nil {
			continue
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		response.BadRequest(c, "No items to deliver")
		return
	}

	// Pre-validate stock availability for all lines before making any changes
	type insufficientItem struct {
		ProductName string  `json:"product_name"`
		Available   float64 `json:"available"`
		Requested   float64 `json:"requested"`
	}
	var insufficientItems []insufficientItem

	for _, line := range lines {
		effectiveWarehouseID := warehouseID.String
		if line.LineWarehouseID.Valid {
			effectiveWarehouseID = line.LineWarehouseID.String
		}
		if effectiveWarehouseID == "" {
			continue
		}

		var qtyAvailable float64
		var productName string
		err := h.db.QueryRow(`
			SELECT COALESCE(i.quantity_available, 0), COALESCE(p.name, 'Unknown')
			FROM products p
			LEFT JOIN inventory i ON i.product_id = p.id AND i.tenant_id = p.tenant_id AND i.warehouse_id = $3
			WHERE p.id = $1 AND p.tenant_id = $2
		`, line.ProductID, tenantID, effectiveWarehouseID).Scan(&qtyAvailable, &productName)
		if err != nil {
			qtyAvailable = 0
			productName = line.ProductID.String()
		}

		if qtyAvailable < line.QtyToDeliver {
			insufficientItems = append(insufficientItems, insufficientItem{
				ProductName: productName,
				Available:   qtyAvailable,
				Requested:   line.QtyToDeliver,
			})
		}
	}

	if len(insufficientItems) > 0 {
		c.JSON(422, gin.H{
			"success": false,
			"message": "Insufficient stock for delivery",
			"errors":  insufficientItems,
		})
		return
	}

	// Process each line - update inventory
	for _, line := range lines {
		// Use line warehouse or DO warehouse
		effectiveWarehouseID := warehouseID.String
		if line.LineWarehouseID.Valid {
			effectiveWarehouseID = line.LineWarehouseID.String
		}

		if effectiveWarehouseID == "" {
			h.log.Warn("No warehouse specified for delivery line", "line_id", line.ID)
			continue
		}

		// Find inventory record
		var inventoryID uuid.UUID
		var qtyOnHand, qtyAvailable float64
		var unitCost float64

		err := h.db.QueryRow(`
			SELECT id, quantity_on_hand, quantity_available, unit_cost
			FROM inventory
			WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		`, tenantID, line.ProductID, effectiveWarehouseID).Scan(&inventoryID, &qtyOnHand, &qtyAvailable, &unitCost)

		if err == sql.ErrNoRows {
			h.log.Warn("No inventory found for product", "product_id", line.ProductID, "warehouse_id", effectiveWarehouseID)
			continue
		}
		if err != nil {
			h.log.Error("Failed to fetch inventory", "error", err)
			continue
		}

		// Decrease inventory
		_, err = h.db.Exec(`
			UPDATE inventory
			SET quantity_on_hand = quantity_on_hand - $1,
				last_movement_date = $2,
				updated_at = $2
			WHERE id = $3
		`, line.QtyToDeliver, now, inventoryID)

		if err != nil {
			h.log.Error("Failed to update inventory", "error", err)
			continue
		}

		// Create inventory transaction (outbound)
		transactionID := uuid.New()
		var createdBy *uuid.UUID
		if userID != uuid.Nil {
			createdBy = &userID
		}

		_, err = h.db.Exec(`
			INSERT INTO inventory_transactions (
				id, tenant_id, inventory_id, transaction_type, quantity,
				unit_cost, total_cost, reference_type, reference_id,
				reason, transaction_date, created_by, created_at
			) VALUES ($1, $2, $3, 'issue', $4, $5, $6, 'sales_delivery', $7, 'Sales Delivery', $8, $9, $8)
		`, transactionID, tenantID, inventoryID, -line.QtyToDeliver, unitCost, line.QtyToDeliver*unitCost, doID, now, createdBy)

		if err != nil {
			h.log.Error("Failed to create inventory transaction", "error", err)
		}

		// Update DO line quantity_delivered
		_, err = h.db.Exec(`
			UPDATE sales_delivery_order_lines
			SET quantity_delivered = quantity_to_deliver
			WHERE id = $1
		`, line.ID)

		if err != nil {
			h.log.Error("Failed to update DO line", "error", err)
		}

		// Update SO line quantity_delivered
		if line.SOLineID.Valid {
			_, err = h.db.Exec(`
				UPDATE sales_order_lines
				SET quantity_delivered = COALESCE(quantity_delivered, 0) + $1,
					updated_at = $2
				WHERE id = $3
			`, line.QtyToDeliver, now, line.SOLineID.String)

			if err != nil {
				h.log.Error("Failed to update SO line", "error", err)
			}
		}
	}

	// Update DO status to shipped
	_, err = h.db.Exec(`
		UPDATE sales_delivery_orders
		SET status = 'shipped', updated_at = $1
		WHERE id = $2
	`, now, doID)

	if err != nil {
		response.InternalError(c, "Failed to update delivery order status")
		return
	}

	// Check if SO is fully delivered and update SO status
	var totalQty, totalDelivered float64
	err = h.db.QueryRow(`
		SELECT COALESCE(SUM(quantity), 0), COALESCE(SUM(COALESCE(quantity_delivered, 0)), 0)
		FROM sales_order_lines
		WHERE sales_order_id = $1
	`, salesOrderID).Scan(&totalQty, &totalDelivered)

	if err == nil && totalQty > 0 {
		newSOStatus := "processing"
		if totalDelivered >= totalQty {
			newSOStatus = "shipped"
		}
		_, _ = h.db.Exec(`
			UPDATE sales_orders
			SET status = $1, updated_at = $2
			WHERE id = $3 AND status NOT IN ('shipped', 'delivered', 'cancelled')
		`, newSOStatus, now, salesOrderID)
	}

	h.log.Info("Delivery order validated", "do_id", doID, "so_id", salesOrderID)

	h.GetDeliveryOrder(c)
}

// CancelDeliveryOrder cancels a delivery order
func (h *Handler) CancelDeliveryOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	doID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid delivery order ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_delivery_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", doID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Delivery order")
		return
	}
	if currentStatus != "draft" && currentStatus != "ready" {
		response.BadRequest(c, "Can only cancel delivery orders in draft or ready status")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE sales_delivery_orders
		SET status = 'cancelled', updated_at = $1
		WHERE id = $2 AND tenant_id = $3
	`, now, doID, tenantID)

	if err != nil {
		response.InternalError(c, "Failed to cancel delivery order")
		return
	}

	h.GetDeliveryOrder(c)
}
