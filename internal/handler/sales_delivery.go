package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	errDeliveryAlreadyProcessed = errors.New("delivery order already validated or cancelled")
	errOverDelivery             = errors.New("over-delivery: shipped quantity would exceed ordered quantity")
)

// nextDeliveryNumber generates DO##### from the numeric MAX per tenant. The old
// COUNT(*)+1 raced under concurrency and reused numbers after soft-deletes; the
// regex filter keeps legacy timestamp-style numbers (DO-YYYYMMDD...) out of the MAX.
func nextDeliveryNumber(q interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}, tenantID uuid.UUID) string {
	var n int64
	_ = q.QueryRow(`
		SELECT COALESCE(MAX(CAST(SUBSTRING(delivery_number FROM 3) AS BIGINT)), 0) + 1
		FROM sales_delivery_orders
		WHERE tenant_id = $1 AND delivery_number ~ '^DO[0-9]+$'`,
		tenantID).Scan(&n)
	if n < 1 {
		n = 1
	}
	return fmt.Sprintf("DO%05d", n)
}

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
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
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
	// Aliased `sdo` to stay textually parallel with baseQuery. Not a bug today
	// — the count has no joins, so bare columns resolve fine — but it is a
	// divergence waiting to break the first time someone adds a joined
	// predicate to baseQuery and copy-pastes it down here.
	countQuery := `SELECT COUNT(*) FROM sales_delivery_orders sdo WHERE sdo.tenant_id = $1 AND sdo.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND sdo.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND sdo.status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by sales_order_id
	if soID := c.Query("sales_order_id"); soID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.sales_order_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND sdo.sales_order_id = $%d", argCount)
		args = append(args, soID)
	}

	// Filter by customer_id
	if customerID := c.Query("customer_id"); customerID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.customer_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND sdo.customer_id = $%d", argCount)
		args = append(args, customerID)
	}

	// Filter by date range
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.delivery_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND sdo.delivery_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND sdo.delivery_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND sdo.delivery_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(sdo.delivery_number) LIKE $%d OR LOWER(sdo.so_number) LIKE $%d OR LOWER(sdo.customer_name) LIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(sdo.delivery_number) LIKE $%d OR LOWER(sdo.so_number) LIKE $%d OR LOWER(sdo.customer_name) LIKE $%d)", argCount, argCount, argCount)
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
	var soOrgID sql.NullString

	err = h.db.QueryRow(`
		SELECT so.order_number, so.status, so.customer_id, c.name, so.warehouse_id, so.expected_date, so.organization_id
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.id = $1 AND so.tenant_id = $2 AND so.deleted_at IS NULL
	`, salesOrderID, tenantID).Scan(&soNumber, &soStatus, &soCustomerID, &soCustomerName, &soWarehouseID, &soExpectedDate, &soOrgID)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch sales order", "error", err)
		response.InternalError(c, "Failed to fetch sales order")
		return
	}

	// If orgIDPtr is nil (middleware didn't set it), derive from sales order's organization_id
	if orgIDPtr == nil && soOrgID.Valid && soOrgID.String != "" {
		parsedOrgID, parseErr := uuid.Parse(soOrgID.String)
		if parseErr == nil {
			orgIDPtr = &parsedOrgID
		}
	}

	// Validate SO status - must be confirmed or processing
	if soStatus != "confirmed" && soStatus != "processing" {
		response.BadRequest(c, "Sales order must be confirmed or processing to create delivery order")
		return
	}

	// Generate delivery number
	now := time.Now()
	deliveryNumber := nextDeliveryNumber(h.db, tenantID)

	// Use provided warehouse or SO warehouse
	warehouseID := input.WarehouseID
	if warehouseID == "" && soWarehouseID.Valid {
		warehouseID = soWarehouseID.String
	}

	// If warehouse is set, validate it belongs to the same organization
	if warehouseID != "" && orgIDPtr != nil {
		var warehouseOrgMatch bool
		h.db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND organization_id = $3 AND deleted_at IS NULL)",
			warehouseID, tenantID, *orgIDPtr,
		).Scan(&warehouseOrgMatch)
		if !warehouseOrgMatch {
			// Try to find a warehouse that belongs to this organization instead
			var orgWarehouseID string
			err := h.db.QueryRow(
				"SELECT id FROM warehouses WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL ORDER BY is_default DESC, created_at ASC LIMIT 1",
				tenantID, *orgIDPtr,
			).Scan(&orgWarehouseID)
			if err == nil {
				warehouseID = orgWarehouseID
			}
		}
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
	if !h.salesOrgScopeOK(c, "sales_delivery_orders", doID, tenantID) {
		response.NotFound(c, "Delivery order")
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
	if !h.salesOrgScopeOK(c, "sales_delivery_orders", doID, tenantID) {
		response.NotFound(c, "Delivery order")
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
	if !h.salesOrgScopeOK(c, "sales_delivery_orders", doID, tenantID) {
		response.NotFound(c, "Delivery order")
		return
	}

	// Get delivery order details
	var currentStatus string
	var salesOrderID uuid.UUID
	var warehouseID sql.NullString
	var doOrgID sql.NullString

	err = h.db.QueryRow(`
		SELECT status, sales_order_id, warehouse_id, organization_id
		FROM sales_delivery_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, doID, tenantID).Scan(&currentStatus, &salesOrderID, &warehouseID, &doOrgID)

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

	// If no warehouse is set on the DO, try to find one from the org
	if (!warehouseID.Valid || warehouseID.String == "") && doOrgID.Valid && doOrgID.String != "" {
		var orgWarehouse string
		err := h.db.QueryRow(
			"SELECT id FROM warehouses WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL ORDER BY is_default DESC, created_at ASC LIMIT 1",
			tenantID, doOrgID.String,
		).Scan(&orgWarehouse)
		if err == nil {
			warehouseID = sql.NullString{String: orgWarehouse, Valid: true}
		}
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

		// Validate that the effective warehouse belongs to the same organization
		if doOrgID.Valid && doOrgID.String != "" {
			var whOrgMatch bool
			h.db.QueryRow(
				"SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND organization_id = $3 AND deleted_at IS NULL)",
				effectiveWarehouseID, tenantID, doOrgID.String,
			).Scan(&whOrgMatch)
			if !whOrgMatch {
				// Fall back to org's default warehouse
				var orgWH string
				if h.db.QueryRow(
					"SELECT id FROM warehouses WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL ORDER BY is_default DESC, created_at ASC LIMIT 1",
					tenantID, doOrgID.String,
				).Scan(&orgWH) == nil {
					effectiveWarehouseID = orgWH
				}
			}
		}

		var qtyAvailable float64
		var productName string
		// On-hand, NOT quantity_available: this order's own confirm-time reservation
		// sits in quantity_reserved, so "available" would block the very order the
		// stock is reserved FOR. The hard floor stays applyStockDelta (on-hand >= 0).
		err := h.db.QueryRow(`
			SELECT COALESCE(SUM(i.quantity_on_hand), 0), COALESCE(MAX(p.name), 'Unknown')
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

	isPartial := c.Query("partial") == "true"

	if len(insufficientItems) > 0 && !isPartial {
		c.JSON(422, gin.H{
			"success": false,
			"message": "Insufficient stock for delivery",
			"errors":  insufficientItems,
		})
		return
	}

	// Build availability map for partial delivery
	type stockInfo struct {
		InventoryID uuid.UUID
		Available   float64
		UnitCost    float64
		WarehouseID string
		ProductName string
	}
	stockMap := make(map[uuid.UUID]stockInfo) // keyed by product_id

	for _, line := range lines {
		effectiveWarehouseID := warehouseID.String
		if line.LineWarehouseID.Valid {
			effectiveWarehouseID = line.LineWarehouseID.String
		}
		if effectiveWarehouseID == "" {
			continue
		}

		// Validate warehouse belongs to the same organization (same check as above)
		if doOrgID.Valid && doOrgID.String != "" {
			var whOrgMatch bool
			h.db.QueryRow(
				"SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND organization_id = $3 AND deleted_at IS NULL)",
				effectiveWarehouseID, tenantID, doOrgID.String,
			).Scan(&whOrgMatch)
			if !whOrgMatch {
				var orgWH string
				if h.db.QueryRow(
					"SELECT id FROM warehouses WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL ORDER BY is_default DESC, created_at ASC LIMIT 1",
					tenantID, doOrgID.String,
				).Scan(&orgWH) == nil {
					effectiveWarehouseID = orgWH
				}
			}
		}

		var inventoryID uuid.UUID
		var qtyOnHand, qtyAvailable, unitCost float64
		var productName string

		// quantity_available intentionally replaced by on-hand here too — see the
		// pre-check note above (own reservation must not block own shipment).
		err := h.db.QueryRow(`
			SELECT i.id, i.quantity_on_hand, i.quantity_on_hand, i.unit_cost, COALESCE(p.name, 'Unknown')
			FROM inventory i
			JOIN products p ON p.id = i.product_id AND p.tenant_id = i.tenant_id
			WHERE i.tenant_id = $1 AND i.product_id = $2 AND i.warehouse_id = $3
		`, tenantID, line.ProductID, effectiveWarehouseID).Scan(&inventoryID, &qtyOnHand, &qtyAvailable, &unitCost, &productName)

		if err != nil {
			// No inventory record exists — create one with zero quantity
			// so that inventory deduction is tracked (may go negative, which is valid for backorders)
			var pName string
			var pCost float64
			h.db.QueryRow("SELECT COALESCE(name, ''), COALESCE(cost_price, 0) FROM products WHERE id = $1 AND tenant_id = $2", line.ProductID, tenantID).Scan(&pName, &pCost)

			newInvID := uuid.New()
			// Get organization_id from the warehouse
			var whOrgID *uuid.UUID
			h.db.QueryRow("SELECT organization_id FROM warehouses WHERE id = $1 AND tenant_id = $2", effectiveWarehouseID, tenantID).Scan(&whOrgID)

			_, createErr := h.db.Exec(`
				INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id, quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, 0, 0, $6, $7, $7)
			`, newInvID, tenantID, whOrgID, line.ProductID, effectiveWarehouseID, pCost, now)
			if createErr != nil {
				h.log.Error("Failed to create inventory record for delivery", "error", createErr, "product_id", line.ProductID)
				stockMap[line.ProductID] = stockInfo{WarehouseID: effectiveWarehouseID, ProductName: pName}
				continue
			}

			stockMap[line.ProductID] = stockInfo{
				InventoryID: newInvID,
				Available:   0,
				UnitCost:    pCost,
				WarehouseID: effectiveWarehouseID,
				ProductName: pName,
			}
			continue
		}
		stockMap[line.ProductID] = stockInfo{
			InventoryID: inventoryID,
			Available:   qtyAvailable,
			UnitCost:    unitCost,
			WarehouseID: effectiveWarehouseID,
			ProductName: productName,
		}
	}

	// Determine actual delivery quantities per line
	type deliveryAction struct {
		Line         doLine
		QtyToShip    float64
		QtyBackorder float64
	}
	var actions []deliveryAction
	var hasBackorder bool

	for _, line := range lines {
		stock := stockMap[line.ProductID]
		if isPartial && stock.Available < line.QtyToDeliver {
			qtyToShip := stock.Available
			if qtyToShip < 0 {
				qtyToShip = 0
			}
			actions = append(actions, deliveryAction{
				Line:         line,
				QtyToShip:    qtyToShip,
				QtyBackorder: line.QtyToDeliver - qtyToShip,
			})
			hasBackorder = true
		} else {
			actions = append(actions, deliveryAction{
				Line:      line,
				QtyToShip: line.QtyToDeliver,
			})
		}
	}

	// Process each line - update inventory for shipped quantities
	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	var doOrgPtr *uuid.UUID
	if doOrgID.Valid && doOrgID.String != "" {
		if parsed, pErr := uuid.Parse(doOrgID.String); pErr == nil && parsed != uuid.Nil {
			doOrgPtr = &parsed
		}
	}

	// All stock work — FIFO lot consumption, balance decrement and the
	// ledger row via applyStockDelta — lands in ONE transaction for the
	// whole document. The old per-line h.db loop could stop halfway and
	// leave balance ≠ ledger (audit finding #1). COGS JEs post after
	// commit with the ACTUAL FIFO cost consumed, so the GL finally sees
	// stock leave at shipment time, not at invoicing with a stale price.
	type shippedCost struct {
		ProductID uuid.UUID
		Qty       float64
		UnitCost  float64
	}
	var shippedCosts []shippedCost

	// Tenant valuation method: FIFO values the movement at the consumed
	// lots' weighted cost; AVECO (default) values at the balance row's
	// stored average (UnitCost=0 → applyStockDelta uses the average).
	useFIFO := h.tenantUsesFIFO(h.db, tenantID)

	shipErr := func() error {
		tx, txErr := h.db.Begin()
		if txErr != nil {
			return txErr
		}
		defer tx.Rollback()

		// Atomic claim: flip the status INSIDE the stock transaction. Two concurrent
		// validates both used to pass the plain status read at the top of the handler
		// and ship twice (audit §5); the second one now sees 0 rows and aborts.
		claimRes, claimErr := tx.Exec(`
			UPDATE sales_delivery_orders SET status = 'shipped', updated_at = $1
			WHERE id = $2 AND tenant_id = $3 AND status IN ('draft','ready')`,
			now, doID, tenantID)
		if claimErr != nil {
			return claimErr
		}
		if n, _ := claimRes.RowsAffected(); n == 0 {
			return errDeliveryAlreadyProcessed
		}

		for _, action := range actions {
			if action.QtyToShip <= 0 {
				continue
			}

			stock := stockMap[action.Line.ProductID]
			if stock.WarehouseID == "" {
				h.log.Warn("No warehouse resolved for delivery line", "product_id", action.Line.ProductID)
				continue
			}
			whID, whErr := uuid.Parse(stock.WarehouseID)
			if whErr != nil {
				continue
			}

			// FIFO: consume from oldest lots first (collect, then update —
			// no other statements while a result set is open on the tx).
			type lotSlice struct {
				ID   uuid.UUID
				Qty  float64
				Cost float64
			}
			var lots []lotSlice
			lotRows, lotErr := tx.Query(`
				SELECT id, remaining_quantity, unit_cost FROM inventory_lots
				WHERE tenant_id = $1 AND product_id = $2 AND status = 'available' AND remaining_quantity > 0
				ORDER BY received_date ASC
			`, tenantID, action.Line.ProductID)
			if lotErr == nil {
				for lotRows.Next() {
					var l lotSlice
					if lotRows.Scan(&l.ID, &l.Qty, &l.Cost) == nil {
						lots = append(lots, l)
					}
				}
				lotRows.Close()
			}

			remainingToConsume := action.QtyToShip
			var fifoValue float64 // Σ consumed qty × that lot's cost
			var fifoConsumed float64
			for _, l := range lots {
				if remainingToConsume <= 0 {
					break
				}
				consume := remainingToConsume
				if consume > l.Qty {
					consume = l.Qty
				}
				remainingToConsume -= consume
				fifoValue += consume * l.Cost
				fifoConsumed += consume

				if _, uErr := tx.Exec(`UPDATE inventory_lots SET remaining_quantity = remaining_quantity - $1, updated_at = $2 WHERE id = $3`,
					consume, now, l.ID); uErr != nil {
					return uErr
				}
				if _, uErr := tx.Exec(`UPDATE inventory_lots SET status = 'depleted' WHERE id = $1 AND remaining_quantity <= 0`, l.ID); uErr != nil {
					return uErr
				}
			}

			// Weighted FIFO cost across ALL consumed lots (the old code
			// valued the whole movement at the LAST lot's cost). Under
			// AVECO the movement is valued at the stored average instead.
			txUnitCost := 0.0
			if useFIFO && fifoConsumed > 0 {
				txUnitCost = fifoValue / fifoConsumed
			}

			_, valuedCost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
				TenantID: tenantID, OrgID: doOrgPtr, ProductID: action.Line.ProductID,
				WarehouseID: whID, Qty: -action.QtyToShip, UnitCost: txUnitCost,
				TxType: "issue", RefType: "sales_delivery", RefID: doID.String(),
				Reason: "Sales Delivery", FromWH: &whID,
				CreatedBy: userID, When: now,
				// The pre-check above 422s (or, with ?partial, clamps to available), so a
				// negative here means a concurrent movement won the race — fail, don't drift.
				AllowNeg: false,
			})
			if dErr != nil {
				return dErr
			}

			// Over-delivery guard on the order line, enforced in the SAME transaction as
			// the stock movement: two pre-created DOs each carrying the full remaining
			// quantity can no longer both ship it (audit §2, §10).
			if action.Line.SOLineID.Valid {
				clampRes, clampErr := tx.Exec(`
					UPDATE sales_order_lines
					SET quantity_delivered = COALESCE(quantity_delivered, 0) + $1, updated_at = $2
					WHERE id = $3 AND COALESCE(quantity_delivered, 0) + $1 <= quantity + 0.0001`,
					action.QtyToShip, now, action.Line.SOLineID.String)
				if clampErr != nil {
					return clampErr
				}
				if n, _ := clampRes.RowsAffected(); n == 0 {
					return fmt.Errorf("%w: order line %s", errOverDelivery, action.Line.SOLineID.String)
				}
			}

			// Release the confirm-time reservation for what actually shipped
			// (floored at zero — pre-reservation orders have nothing booked).
			if _, rErr := tx.Exec(`
				UPDATE inventory SET quantity_reserved = GREATEST(0, quantity_reserved - $1), updated_at = $2
				WHERE tenant_id = $3 AND product_id = $4 AND warehouse_id = $5`,
				action.QtyToShip, now, tenantID, action.Line.ProductID, whID); rErr != nil {
				return rErr
			}

			// FIFO only: roll product cost_price forward to the next
			// available lot's cost. methodCostPrice re-resolves the
			// product's EFFECTIVE method, because useFIFO is the tenant
			// default — a category overridden to AVCO must keep showing its
			// average (issues never move an average, §3.2), not the next
			// lot this block would have written.
			if useFIFO {
				var nextLotCost float64
				if tx.QueryRow(`
					SELECT unit_cost FROM inventory_lots
					WHERE tenant_id = $1 AND product_id = $2 AND status = 'available' AND remaining_quantity > 0
					ORDER BY received_date ASC LIMIT 1
				`, tenantID, action.Line.ProductID).Scan(&nextLotCost) == nil && nextLotCost > 0 {
					displayCost := h.methodCostPrice(tx, tenantID, doOrgPtr, action.Line.ProductID, nextLotCost)
					if _, uErr := tx.Exec(`UPDATE products SET cost_price = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4`,
						displayCost, now, action.Line.ProductID, tenantID); uErr != nil {
						return uErr
					}
					if doOrgPtr != nil {
						if _, uErr := tx.Exec(`UPDATE product_organization_settings SET cost_price = $1, updated_at = $2 WHERE product_id = $3 AND organization_id = $4`,
							displayCost, now, action.Line.ProductID, *doOrgPtr); uErr != nil {
							return uErr
						}
					}
				}
			}

			shippedCosts = append(shippedCosts, shippedCost{
				ProductID: action.Line.ProductID,
				Qty:       action.QtyToShip,
				UnitCost:  valuedCost,
			})
		}
		return tx.Commit()
	}()
	if shipErr != nil {
		h.log.Error("Delivery stock movement failed; NOTHING moved", "error", shipErr, "do_id", doID)
		switch {
		case errors.Is(shipErr, errDeliveryAlreadyProcessed):
			response.BadRequest(c, "ALREADY_SHIPPED: delivery order was already validated")
		case errors.Is(shipErr, errOverDelivery):
			response.BadRequest(c, "OVER_DELIVERY: shipped quantity would exceed the ordered quantity")
		case errors.Is(shipErr, errInsufficientStock):
			c.JSON(422, gin.H{"success": false, "error": gin.H{"code": "INSUFFICIENT_STOCK", "message": "Insufficient stock for delivery"}})
		default:
			response.InternalError(c, "Failed to apply stock movement for delivery")
		}
		return
	}

	// COGS at shipment: DR cost-of-goods / CR inventory per shipped line,
	// valued at the actual FIFO cost. Idempotency-keyed per (DO, product) —
	// CreateInvoiceFromOrder checks these keys and skips its own COGS leg
	// so the same shipment is never expensed twice.
	for _, sc := range shippedCosts {
		h.postInventoryConsumptionJE(h.db, postInventoryConsumptionArgs{
			TenantID:       tenantID,
			OrganizationID: doOrgPtr,
			ProductID:      sc.ProductID,
			Quantity:       sc.Qty,
			UnitCost:       sc.UnitCost,
			SourceType:     "sales_delivery",
			SourceID:       &doID,
			IdempotencyKey: fmt.Sprintf("DO-COGS-%s-%s", doID, sc.ProductID),
			Description:    "Yetkazib berish tannarxi (COGS)",
		})
		var bal float64
		_ = h.db.QueryRow(`SELECT COALESCE(SUM(quantity_on_hand),0) FROM inventory WHERE tenant_id=$1 AND product_id=$2`,
			tenantID, sc.ProductID).Scan(&bal)
		h.emitInventoryAdjusted(tenantID, sc.ProductID, -sc.Qty, bal)
	}

	// Document-line status updates (outside the stock tx, as before)
	for _, action := range actions {
		if action.QtyToShip <= 0 {
			continue
		}

		// Update DO line: set quantity_delivered to what was actually shipped
		if isPartial && action.QtyBackorder > 0 {
			// Partial: update qty_to_deliver to what was shipped, set delivered
			_, err = h.db.Exec(`
				UPDATE sales_delivery_order_lines
				SET quantity_to_deliver = $1, quantity_delivered = $1
				WHERE id = $2
			`, action.QtyToShip, action.Line.ID)
		} else {
			_, err = h.db.Exec(`
				UPDATE sales_delivery_order_lines
				SET quantity_delivered = quantity_to_deliver
				WHERE id = $1
			`, action.Line.ID)
		}
		if err != nil {
			h.log.Error("Failed to update DO line", "error", err)
		}

		// SO line quantity_delivered is updated inside the stock transaction above
		// (with the over-delivery clamp) — not repeated here.
	}

	// For lines with 0 qty to ship in partial mode, remove them from this DO
	if isPartial {
		for _, action := range actions {
			if action.QtyToShip <= 0 && action.QtyBackorder > 0 {
				// Remove line from current DO (it will be in backorder DO)
				_, _ = h.db.Exec(`DELETE FROM sales_delivery_order_lines WHERE id = $1`, action.Line.ID)
			}
		}
	}

	// DO status was already flipped to 'shipped' by the atomic claim inside the stock tx.

	// Create backorder DO if partial delivery
	var backorderNumber string
	if isPartial && hasBackorder {
		backorderNumber = nextDeliveryNumber(h.db, tenantID)

		backorderID := uuid.New()

		// Get original DO details for backorder
		var orgID, customerID, customerName, soNumber, shippingMethod, carrier, notes sql.NullString
		h.db.QueryRow(`
			SELECT organization_id, customer_id, customer_name, so_number, shipping_method, carrier, notes
			FROM sales_delivery_orders WHERE id = $1
		`, doID).Scan(&orgID, &customerID, &customerName, &soNumber, &shippingMethod, &carrier, &notes)

		_, err = h.db.Exec(`
			INSERT INTO sales_delivery_orders (
				id, tenant_id, organization_id, delivery_number, sales_order_id, so_number,
				customer_id, customer_name, delivery_date, scheduled_date,
				warehouse_id, shipping_method, carrier, notes,
				status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10, $11, $12, $13, 'draft', $14, $9, $9)
		`, backorderID, tenantID, orgID, backorderNumber, salesOrderID, soNumber,
			customerID, customerName, now, warehouseID, shippingMethod, carrier,
			sql.NullString{String: "Kutilmoqda: qoldiq yetkazma / Backorder from DO " + doID.String()[:8], Valid: true},
			createdBy)

		if err != nil {
			h.log.Error("Failed to create backorder DO", "error", err)
		} else {
			// Create backorder lines
			for _, action := range actions {
				if action.QtyBackorder <= 0 {
					continue
				}
				lineID := uuid.New()
				var productName string
				h.db.QueryRow("SELECT COALESCE(name, '') FROM products WHERE id = $1 AND tenant_id = $2", action.Line.ProductID, tenantID).Scan(&productName)

				var unitPrice float64
				if action.Line.SOLineID.Valid {
					h.db.QueryRow("SELECT COALESCE(unit_price, 0) FROM sales_order_lines WHERE id = $1", action.Line.SOLineID.String).Scan(&unitPrice)
				}

				_, err = h.db.Exec(`
					INSERT INTO sales_delivery_order_lines (
						id, delivery_order_id, so_line_id, product_id, product_name,
						quantity_ordered, quantity_to_deliver, quantity_delivered,
						unit_price, warehouse_id, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, $9, $10)
				`, lineID, backorderID, action.Line.SOLineID, action.Line.ProductID, productName,
					action.QtyBackorder, action.QtyBackorder, unitPrice, action.Line.LineWarehouseID, now)

				if err != nil {
					h.log.Error("Failed to create backorder line", "error", err)
				}
			}
			h.log.Info("Backorder DO created", "backorder_id", backorderID, "backorder_number", backorderNumber)
		}
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

	h.log.Info("Delivery order validated", "do_id", doID, "so_id", salesOrderID, "partial", isPartial)

	if isPartial && hasBackorder {
		// Return both shipped DO info and backorder info
		c.JSON(200, gin.H{
			"success":          true,
			"message":          "Partial delivery completed. Backorder created for remaining items.",
			"backorder_number": backorderNumber,
		})
		return
	}

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
	if !h.salesOrgScopeOK(c, "sales_delivery_orders", doID, tenantID) {
		response.NotFound(c, "Delivery order")
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
