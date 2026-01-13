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
// INVENTORY HANDLERS
// =====================================================

// ListInventory returns a paginated list of inventory records
func (h *Handler) ListInventory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	productID := c.Query("product_id")
	warehouseID := c.Query("warehouse_id")
	locationID := c.Query("location_id")
	lowStock := c.Query("low_stock") == "true"
	outOfStock := c.Query("out_of_stock") == "true"
	expiring := c.Query("expiring") == "true"
	expiryDays, _ := strconv.Atoi(c.DefaultQuery("expiry_days", "30"))

	// Build query
	baseQuery := `
		SELECT i.id, i.tenant_id, i.product_id, i.warehouse_id, i.location_id,
			   i.lot_number, i.serial_number, i.expiry_date,
			   i.quantity_on_hand, i.quantity_reserved, i.quantity_available,
			   i.unit_cost, i.total_value, i.last_count_date, i.last_movement_date,
			   i.created_at, i.updated_at,
			   p.code as product_code, p.name as product_name, p.min_stock_level, p.reorder_point,
			   w.code as warehouse_code, w.name as warehouse_name,
			   wl.code as location_code, wl.name as location_name
		FROM inventory i
		JOIN products p ON i.product_id = p.id
		JOIN warehouses w ON i.warehouse_id = w.id
		LEFT JOIN warehouse_locations wl ON i.location_id = wl.id
		WHERE i.tenant_id = $1
	`
	countQuery := `
		SELECT COUNT(*) FROM inventory i
		JOIN products p ON i.product_id = p.id
		WHERE i.tenant_id = $1
	`

	args := []interface{}{tenantID}
	argCount := 1

	if productID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND i.product_id = $%d", argCount)
		args = append(args, productID)
	}

	if warehouseID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.warehouse_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND i.warehouse_id = $%d", argCount)
		args = append(args, warehouseID)
	}

	if locationID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.location_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND i.location_id = $%d", argCount)
		args = append(args, locationID)
	}

	if lowStock {
		baseQuery += " AND i.quantity_available <= p.reorder_point AND i.quantity_available > 0"
		countQuery += " AND i.quantity_available <= p.reorder_point AND i.quantity_available > 0"
	}

	if outOfStock {
		baseQuery += " AND i.quantity_on_hand <= 0"
		countQuery += " AND i.quantity_on_hand <= 0"
	}

	if expiring {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.expiry_date IS NOT NULL AND i.expiry_date <= NOW() + INTERVAL '%d days'", expiryDays)
		countQuery += fmt.Sprintf(" AND i.expiry_date IS NOT NULL AND i.expiry_date <= NOW() + INTERVAL '%d days'", expiryDays)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (p.code ILIKE $%d OR p.name ILIKE $%d OR i.lot_number ILIKE $%d OR i.serial_number ILIKE $%d)", argCount, argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count inventory", "error", err)
		response.InternalError(c, "Failed to list inventory")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY p.code ASC, w.code ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list inventory", "error", err)
		response.InternalError(c, "Failed to list inventory")
		return
	}
	defer rows.Close()

	type InventoryResponse struct {
		ID                uuid.UUID  `json:"id"`
		TenantID          uuid.UUID  `json:"tenant_id"`
		ProductID         uuid.UUID  `json:"product_id"`
		ProductCode       string     `json:"product_code"`
		ProductName       string     `json:"product_name"`
		WarehouseID       uuid.UUID  `json:"warehouse_id"`
		WarehouseCode     string     `json:"warehouse_code"`
		WarehouseName     string     `json:"warehouse_name"`
		LocationID        *uuid.UUID `json:"location_id,omitempty"`
		LocationCode      string     `json:"location_code,omitempty"`
		LocationName      string     `json:"location_name,omitempty"`
		LotNumber         *string    `json:"lot_number,omitempty"`
		SerialNumber      *string    `json:"serial_number,omitempty"`
		ExpiryDate        *time.Time `json:"expiry_date,omitempty"`
		QuantityOnHand    float64    `json:"quantity_on_hand"`
		QuantityReserved  float64    `json:"quantity_reserved"`
		QuantityAvailable float64    `json:"quantity_available"`
		UnitCost          float64    `json:"unit_cost"`
		TotalValue        float64    `json:"total_value"`
		MinStockLevel     float64    `json:"min_stock_level"`
		ReorderPoint      float64    `json:"reorder_point"`
		NeedsReorder      bool       `json:"needs_reorder"`
		LastCountDate     *time.Time `json:"last_count_date,omitempty"`
		LastMovementDate  *time.Time `json:"last_movement_date,omitempty"`
		CreatedAt         time.Time  `json:"created_at"`
		UpdatedAt         time.Time  `json:"updated_at"`
	}

	inventory := make([]*InventoryResponse, 0)
	for rows.Next() {
		var i entity.Inventory
		var locationID sql.NullString
		var lotNumber, serialNumber sql.NullString
		var expiryDate, lastCountDate, lastMovementDate sql.NullTime
		var productCode, productName string
		var minStockLevel, reorderPoint float64
		var warehouseCode, warehouseName string
		var locationCode, locationName sql.NullString

		err := rows.Scan(
			&i.ID, &i.TenantID, &i.ProductID, &i.WarehouseID, &locationID,
			&lotNumber, &serialNumber, &expiryDate,
			&i.QuantityOnHand, &i.QuantityReserved, &i.QuantityAvailable,
			&i.UnitCost, &i.TotalValue, &lastCountDate, &lastMovementDate,
			&i.CreatedAt, &i.UpdatedAt,
			&productCode, &productName, &minStockLevel, &reorderPoint,
			&warehouseCode, &warehouseName,
			&locationCode, &locationName,
		)
		if err != nil {
			h.log.Error("Failed to scan inventory", "error", err)
			continue
		}

		resp := &InventoryResponse{
			ID:                i.ID,
			TenantID:          i.TenantID,
			ProductID:         i.ProductID,
			ProductCode:       productCode,
			ProductName:       productName,
			WarehouseID:       i.WarehouseID,
			WarehouseCode:     warehouseCode,
			WarehouseName:     warehouseName,
			QuantityOnHand:    i.QuantityOnHand,
			QuantityReserved:  i.QuantityReserved,
			QuantityAvailable: i.QuantityAvailable,
			UnitCost:          i.UnitCost,
			TotalValue:        i.TotalValue,
			MinStockLevel:     minStockLevel,
			ReorderPoint:      reorderPoint,
			NeedsReorder:      i.QuantityAvailable <= reorderPoint,
			CreatedAt:         i.CreatedAt,
			UpdatedAt:         i.UpdatedAt,
		}

		if locationID.Valid {
			lid, _ := uuid.Parse(locationID.String)
			resp.LocationID = &lid
		}
		if lotNumber.Valid {
			resp.LotNumber = &lotNumber.String
		}
		if serialNumber.Valid {
			resp.SerialNumber = &serialNumber.String
		}
		if expiryDate.Valid {
			resp.ExpiryDate = &expiryDate.Time
		}
		if lastCountDate.Valid {
			resp.LastCountDate = &lastCountDate.Time
		}
		if lastMovementDate.Valid {
			resp.LastMovementDate = &lastMovementDate.Time
		}
		if locationCode.Valid {
			resp.LocationCode = locationCode.String
		}
		if locationName.Valid {
			resp.LocationName = locationName.String
		}

		inventory = append(inventory, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, inventory, pagination)
}

// GetInventorySummary returns inventory summarized by product
func (h *Handler) GetInventorySummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	search := c.Query("search")
	categoryID := c.Query("category_id")
	warehouseID := c.Query("warehouse_id")
	lowStock := c.Query("low_stock") == "true"
	outOfStock := c.Query("out_of_stock") == "true"

	baseQuery := `
		SELECT p.id as product_id, p.code as product_code, p.name as product_name,
			   p.min_stock_level, p.reorder_point,
			   COALESCE(SUM(i.quantity_on_hand), 0) as total_on_hand,
			   COALESCE(SUM(i.quantity_reserved), 0) as total_reserved,
			   COALESCE(SUM(i.quantity_available), 0) as total_available,
			   COALESCE(SUM(i.total_value), 0) as total_value,
			   COUNT(DISTINCT i.warehouse_id) as warehouse_count
		FROM products p
		LEFT JOIN inventory i ON p.id = i.product_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
	`
	countQuery := `
		SELECT COUNT(DISTINCT p.id)
		FROM products p
		LEFT JOIN inventory i ON p.id = i.product_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
	`

	args := []interface{}{tenantID}
	argCount := 1

	if categoryID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.category_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.category_id = $%d", argCount)
		args = append(args, categoryID)
	}

	if warehouseID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.warehouse_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND i.warehouse_id = $%d", argCount)
		args = append(args, warehouseID)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (p.code ILIKE $%d OR p.name ILIKE $%d)", argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	baseQuery += " GROUP BY p.id, p.code, p.name, p.min_stock_level, p.reorder_point"

	if lowStock {
		baseQuery += " HAVING COALESCE(SUM(i.quantity_available), 0) <= p.reorder_point AND COALESCE(SUM(i.quantity_available), 0) > 0"
	}

	if outOfStock {
		baseQuery += " HAVING COALESCE(SUM(i.quantity_on_hand), 0) <= 0"
	}

	// Get count (simplified - count all products first)
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count inventory summary", "error", err)
		response.InternalError(c, "Failed to get inventory summary")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY p.code ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to get inventory summary", "error", err)
		response.InternalError(c, "Failed to get inventory summary")
		return
	}
	defer rows.Close()

	summaries := make([]*entity.InventorySummary, 0)
	for rows.Next() {
		var s entity.InventorySummary

		err := rows.Scan(
			&s.ProductID, &s.ProductCode, &s.ProductName,
			&s.MinStockLevel, &s.ReorderPoint,
			&s.TotalOnHand, &s.TotalReserved, &s.TotalAvailable,
			&s.TotalValue, &s.WarehouseCount,
		)
		if err != nil {
			h.log.Error("Failed to scan inventory summary", "error", err)
			continue
		}

		s.NeedsReorder = s.TotalAvailable <= s.ReorderPoint

		summaries = append(summaries, &s)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, summaries, pagination)
}

// AdjustInventory creates an inventory adjustment
func (h *Handler) AdjustInventory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.InventoryAdjustmentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse UUIDs
	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	warehouseID, err := uuid.Parse(input.WarehouseID)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	var locationID *uuid.UUID
	if input.LocationID != "" {
		lid, err := uuid.Parse(input.LocationID)
		if err == nil {
			locationID = &lid
		}
	}

	// Verify product exists and belongs to tenant
	var productExists bool
	h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		productID, tenantID,
	).Scan(&productExists)
	if !productExists {
		response.NotFound(c, "Product")
		return
	}

	// Verify warehouse belongs to tenant
	var warehouseExists bool
	h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		warehouseID, tenantID,
	).Scan(&warehouseExists)
	if !warehouseExists {
		response.NotFound(c, "Warehouse")
		return
	}

	now := time.Now()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to adjust inventory")
		return
	}
	defer tx.Rollback()

	// Find or create inventory record
	var inventoryID uuid.UUID
	var currentQtyOnHand float64

	err = tx.QueryRow(`
		SELECT id, quantity_on_hand FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND COALESCE(location_id::text, '') = COALESCE($4::text, '')
		AND COALESCE(lot_number, '') = COALESCE($5, '')
		AND COALESCE(serial_number, '') = COALESCE($6, '')
	`, tenantID, productID, warehouseID, locationID, input.LotNumber, input.SerialNumber).Scan(&inventoryID, &currentQtyOnHand)

	if err == sql.ErrNoRows {
		// Create new inventory record
		inventoryID = uuid.New()
		currentQtyOnHand = 0

		var lotNumber, serialNumber *string
		if input.LotNumber != "" {
			lotNumber = &input.LotNumber
		}
		if input.SerialNumber != "" {
			serialNumber = &input.SerialNumber
		}

		_, err = tx.Exec(`
			INSERT INTO inventory (
				id, tenant_id, product_id, warehouse_id, location_id,
				lot_number, serial_number, quantity_on_hand, quantity_reserved, quantity_available,
				unit_cost, total_value, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $8, $9, $10, $11, $11)
		`, inventoryID, tenantID, productID, warehouseID, locationID,
			lotNumber, serialNumber, input.Quantity, input.UnitCost, input.Quantity*input.UnitCost, now)

		if err != nil {
			h.log.Error("Failed to create inventory record", "error", err)
			response.InternalError(c, "Failed to adjust inventory")
			return
		}
	} else if err != nil {
		h.log.Error("Failed to find inventory record", "error", err)
		response.InternalError(c, "Failed to adjust inventory")
		return
	} else {
		// Update existing inventory record
		newQtyOnHand := currentQtyOnHand + input.Quantity
		if newQtyOnHand < 0 {
			response.BadRequest(c, "Adjustment would result in negative inventory")
			return
		}

		_, err = tx.Exec(`
			UPDATE inventory SET
				quantity_on_hand = quantity_on_hand + $1,
				quantity_available = quantity_available + $1,
				unit_cost = CASE WHEN $2 > 0 THEN $2 ELSE unit_cost END,
				total_value = (quantity_on_hand + $1) * CASE WHEN $2 > 0 THEN $2 ELSE unit_cost END,
				last_movement_date = $3,
				updated_at = $3
			WHERE id = $4
		`, input.Quantity, input.UnitCost, now, inventoryID)

		if err != nil {
			h.log.Error("Failed to update inventory", "error", err)
			response.InternalError(c, "Failed to adjust inventory")
			return
		}
	}

	// Create inventory transaction record
	transactionID := uuid.New()
	transactionType := entity.TransactionTypeAdjustment

	var reason, notes *string
	if input.Reason != "" {
		reason = &input.Reason
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	_, err = tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, inventory_id, transaction_type, quantity,
			unit_cost, total_cost, reason, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $10)
	`, transactionID, tenantID, inventoryID, transactionType, input.Quantity,
		input.UnitCost, input.Quantity*input.UnitCost, reason, notes, now, userID)

	if err != nil {
		h.log.Error("Failed to create inventory transaction", "error", err)
		response.InternalError(c, "Failed to adjust inventory")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to adjust inventory")
		return
	}

	response.Success(c, gin.H{
		"message":        "Inventory adjusted successfully",
		"inventory_id":   inventoryID,
		"transaction_id": transactionID,
		"quantity":       input.Quantity,
		"new_balance":    currentQtyOnHand + input.Quantity,
	})
}

// TransferInventory transfers inventory between warehouses
func (h *Handler) TransferInventory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.InventoryTransferInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse UUIDs
	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	fromWarehouseID, err := uuid.Parse(input.FromWarehouseID)
	if err != nil {
		response.BadRequest(c, "Invalid source warehouse ID")
		return
	}

	toWarehouseID, err := uuid.Parse(input.ToWarehouseID)
	if err != nil {
		response.BadRequest(c, "Invalid destination warehouse ID")
		return
	}

	if fromWarehouseID == toWarehouseID {
		response.BadRequest(c, "Source and destination warehouses must be different")
		return
	}

	var fromLocationID, toLocationID *uuid.UUID
	if input.FromLocationID != "" {
		fid, _ := uuid.Parse(input.FromLocationID)
		fromLocationID = &fid
	}
	if input.ToLocationID != "" {
		tid, _ := uuid.Parse(input.ToLocationID)
		toLocationID = &tid
	}

	// Verify warehouses belong to tenant
	var fromExists, toExists bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		fromWarehouseID, tenantID).Scan(&fromExists)
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		toWarehouseID, tenantID).Scan(&toExists)

	if !fromExists {
		response.NotFound(c, "Source warehouse")
		return
	}
	if !toExists {
		response.NotFound(c, "Destination warehouse")
		return
	}

	now := time.Now()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to transfer inventory")
		return
	}
	defer tx.Rollback()

	// Find source inventory
	var sourceInventoryID uuid.UUID
	var sourceQtyAvailable float64
	var unitCost float64

	err = tx.QueryRow(`
		SELECT id, quantity_available, unit_cost FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND COALESCE(location_id::text, '') = COALESCE($4::text, '')
	`, tenantID, productID, fromWarehouseID, fromLocationID).Scan(&sourceInventoryID, &sourceQtyAvailable, &unitCost)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "No inventory found at source location")
		return
	}
	if err != nil {
		h.log.Error("Failed to find source inventory", "error", err)
		response.InternalError(c, "Failed to transfer inventory")
		return
	}

	if sourceQtyAvailable < input.Quantity {
		response.BadRequest(c, fmt.Sprintf("Insufficient inventory. Available: %.2f, Requested: %.2f", sourceQtyAvailable, input.Quantity))
		return
	}

	// Decrease source inventory
	_, err = tx.Exec(`
		UPDATE inventory SET
			quantity_on_hand = quantity_on_hand - $1,
			quantity_available = quantity_available - $1,
			total_value = (quantity_on_hand - $1) * unit_cost,
			last_movement_date = $2,
			updated_at = $2
		WHERE id = $3
	`, input.Quantity, now, sourceInventoryID)

	if err != nil {
		h.log.Error("Failed to decrease source inventory", "error", err)
		response.InternalError(c, "Failed to transfer inventory")
		return
	}

	// Find or create destination inventory
	var destInventoryID uuid.UUID

	err = tx.QueryRow(`
		SELECT id FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND COALESCE(location_id::text, '') = COALESCE($4::text, '')
	`, tenantID, productID, toWarehouseID, toLocationID).Scan(&destInventoryID)

	if err == sql.ErrNoRows {
		// Create new destination inventory
		destInventoryID = uuid.New()

		_, err = tx.Exec(`
			INSERT INTO inventory (
				id, tenant_id, product_id, warehouse_id, location_id,
				quantity_on_hand, quantity_reserved, quantity_available,
				unit_cost, total_value, last_movement_date, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, 0, $6, $7, $8, $9, $9, $9)
		`, destInventoryID, tenantID, productID, toWarehouseID, toLocationID,
			input.Quantity, unitCost, input.Quantity*unitCost, now)

		if err != nil {
			h.log.Error("Failed to create destination inventory", "error", err)
			response.InternalError(c, "Failed to transfer inventory")
			return
		}
	} else if err != nil {
		h.log.Error("Failed to find destination inventory", "error", err)
		response.InternalError(c, "Failed to transfer inventory")
		return
	} else {
		// Increase destination inventory
		_, err = tx.Exec(`
			UPDATE inventory SET
				quantity_on_hand = quantity_on_hand + $1,
				quantity_available = quantity_available + $1,
				total_value = (quantity_on_hand + $1) * unit_cost,
				last_movement_date = $2,
				updated_at = $2
			WHERE id = $3
		`, input.Quantity, now, destInventoryID)

		if err != nil {
			h.log.Error("Failed to increase destination inventory", "error", err)
			response.InternalError(c, "Failed to transfer inventory")
			return
		}
	}

	// Create transfer transaction records
	transferID := uuid.New()
	transactionType := entity.TransactionTypeTransfer

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	// Source (outbound) transaction
	_, err = tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, inventory_id, transaction_type, quantity,
			unit_cost, total_cost, from_warehouse_id, to_warehouse_id,
			from_location_id, to_location_id, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $13)
	`, uuid.New(), tenantID, sourceInventoryID, transactionType, -input.Quantity,
		unitCost, -input.Quantity*unitCost, fromWarehouseID, toWarehouseID,
		fromLocationID, toLocationID, notes, now, userID)

	if err != nil {
		h.log.Error("Failed to create source transaction", "error", err)
		response.InternalError(c, "Failed to transfer inventory")
		return
	}

	// Destination (inbound) transaction
	_, err = tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, inventory_id, transaction_type, quantity,
			unit_cost, total_cost, from_warehouse_id, to_warehouse_id,
			from_location_id, to_location_id, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $13)
	`, uuid.New(), tenantID, destInventoryID, transactionType, input.Quantity,
		unitCost, input.Quantity*unitCost, fromWarehouseID, toWarehouseID,
		fromLocationID, toLocationID, notes, now, userID)

	if err != nil {
		h.log.Error("Failed to create destination transaction", "error", err)
		response.InternalError(c, "Failed to transfer inventory")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to transfer inventory")
		return
	}

	response.Success(c, gin.H{
		"message":     "Inventory transferred successfully",
		"transfer_id": transferID,
		"quantity":    input.Quantity,
		"from_warehouse_id": fromWarehouseID,
		"to_warehouse_id":   toWarehouseID,
	})
}

// ListInventoryMovements returns inventory movement history
func (h *Handler) ListInventoryMovements(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	// Parse filters
	productID := c.Query("product_id")
	warehouseID := c.Query("warehouse_id")
	transactionType := c.Query("type")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	baseQuery := `
		SELECT t.id, t.tenant_id, t.inventory_id, t.transaction_type, t.reference_type, t.reference_id,
			   t.quantity, t.unit_cost, t.total_cost, t.from_warehouse_id, t.to_warehouse_id,
			   t.from_location_id, t.to_location_id, t.reason, t.notes, t.transaction_date,
			   t.created_by, t.created_at,
			   p.code as product_code, p.name as product_name,
			   fw.name as from_warehouse_name, tw.name as to_warehouse_name,
			   e.first_name as created_by_first_name, e.last_name as created_by_last_name
		FROM inventory_transactions t
		JOIN inventory i ON t.inventory_id = i.id
		JOIN products p ON i.product_id = p.id
		LEFT JOIN warehouses fw ON t.from_warehouse_id = fw.id
		LEFT JOIN warehouses tw ON t.to_warehouse_id = tw.id
		LEFT JOIN employees e ON t.created_by = e.id
		WHERE t.tenant_id = $1
	`
	countQuery := `
		SELECT COUNT(*) FROM inventory_transactions t
		JOIN inventory i ON t.inventory_id = i.id
		WHERE t.tenant_id = $1
	`

	args := []interface{}{tenantID}
	argCount := 1

	if productID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND i.product_id = $%d", argCount)
		args = append(args, productID)
	}

	if warehouseID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND (t.from_warehouse_id = $%d OR t.to_warehouse_id = $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (t.from_warehouse_id = $%d OR t.to_warehouse_id = $%d)", argCount, argCount)
		args = append(args, warehouseID)
	}

	if transactionType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.transaction_type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.transaction_type = $%d", argCount)
		args = append(args, transactionType)
	}

	if dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.transaction_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.transaction_date >= $%d", argCount)
		args = append(args, dateFrom)
	}

	if dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.transaction_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.transaction_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count inventory movements", "error", err)
		response.InternalError(c, "Failed to list inventory movements")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY t.transaction_date DESC, t.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list inventory movements", "error", err)
		response.InternalError(c, "Failed to list inventory movements")
		return
	}
	defer rows.Close()

	type MovementResponse struct {
		ID                uuid.UUID              `json:"id"`
		TenantID          uuid.UUID              `json:"tenant_id"`
		InventoryID       uuid.UUID              `json:"inventory_id"`
		TransactionType   entity.TransactionType `json:"transaction_type"`
		ReferenceType     *string                `json:"reference_type,omitempty"`
		ReferenceID       *uuid.UUID             `json:"reference_id,omitempty"`
		Quantity          float64                `json:"quantity"`
		UnitCost          *float64               `json:"unit_cost,omitempty"`
		TotalCost         *float64               `json:"total_cost,omitempty"`
		FromWarehouseID   *uuid.UUID             `json:"from_warehouse_id,omitempty"`
		FromWarehouseName string                 `json:"from_warehouse_name,omitempty"`
		ToWarehouseID     *uuid.UUID             `json:"to_warehouse_id,omitempty"`
		ToWarehouseName   string                 `json:"to_warehouse_name,omitempty"`
		FromLocationID    *uuid.UUID             `json:"from_location_id,omitempty"`
		ToLocationID      *uuid.UUID             `json:"to_location_id,omitempty"`
		Reason            *string                `json:"reason,omitempty"`
		Notes             *string                `json:"notes,omitempty"`
		TransactionDate   time.Time              `json:"transaction_date"`
		ProductCode       string                 `json:"product_code"`
		ProductName       string                 `json:"product_name"`
		CreatedBy         *uuid.UUID             `json:"created_by,omitempty"`
		CreatedByName     string                 `json:"created_by_name,omitempty"`
		CreatedAt         time.Time              `json:"created_at"`
	}

	movements := make([]*MovementResponse, 0)
	for rows.Next() {
		var m MovementResponse
		var refType, reason, notes sql.NullString
		var refID, fromWH, toWH, fromLoc, toLoc, createdBy sql.NullString
		var unitCost, totalCost sql.NullFloat64
		var fromWHName, toWHName, createdFirstName, createdLastName sql.NullString

		err := rows.Scan(
			&m.ID, &m.TenantID, &m.InventoryID, &m.TransactionType, &refType, &refID,
			&m.Quantity, &unitCost, &totalCost, &fromWH, &toWH,
			&fromLoc, &toLoc, &reason, &notes, &m.TransactionDate,
			&createdBy, &m.CreatedAt,
			&m.ProductCode, &m.ProductName,
			&fromWHName, &toWHName,
			&createdFirstName, &createdLastName,
		)
		if err != nil {
			h.log.Error("Failed to scan movement", "error", err)
			continue
		}

		if refType.Valid {
			m.ReferenceType = &refType.String
		}
		if refID.Valid {
			rid, _ := uuid.Parse(refID.String)
			m.ReferenceID = &rid
		}
		if unitCost.Valid {
			m.UnitCost = &unitCost.Float64
		}
		if totalCost.Valid {
			m.TotalCost = &totalCost.Float64
		}
		if fromWH.Valid {
			fwh, _ := uuid.Parse(fromWH.String)
			m.FromWarehouseID = &fwh
		}
		if toWH.Valid {
			twh, _ := uuid.Parse(toWH.String)
			m.ToWarehouseID = &twh
		}
		if fromLoc.Valid {
			fl, _ := uuid.Parse(fromLoc.String)
			m.FromLocationID = &fl
		}
		if toLoc.Valid {
			tl, _ := uuid.Parse(toLoc.String)
			m.ToLocationID = &tl
		}
		if reason.Valid {
			m.Reason = &reason.String
		}
		if notes.Valid {
			m.Notes = &notes.String
		}
		if createdBy.Valid {
			cb, _ := uuid.Parse(createdBy.String)
			m.CreatedBy = &cb
		}
		if fromWHName.Valid {
			m.FromWarehouseName = fromWHName.String
		}
		if toWHName.Valid {
			m.ToWarehouseName = toWHName.String
		}
		if createdFirstName.Valid || createdLastName.Valid {
			m.CreatedByName = strings.TrimSpace(createdFirstName.String + " " + createdLastName.String)
		}

		movements = append(movements, &m)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, movements, pagination)
}

// GetInventoryValuation returns inventory valuation report
func (h *Handler) GetInventoryValuation(c *gin.Context) {
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
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	warehouseID := c.Query("warehouse_id")
	categoryID := c.Query("category_id")

	baseQuery := `
		SELECT p.id as product_id, p.code as product_code, p.name as product_name,
			   COALESCE(pc.name, 'Uncategorized') as category_name,
			   COALESCE(SUM(i.quantity_on_hand), 0) as quantity_on_hand,
			   COALESCE(AVG(i.unit_cost), p.cost_price) as average_cost,
			   COALESCE(SUM(i.total_value), 0) as total_value,
			   p.cost_price as last_purchase_price
		FROM products p
		LEFT JOIN product_categories pc ON p.category_id = pc.id
		LEFT JOIN inventory i ON p.id = i.product_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true AND p.is_stockable = true
	`
	countQuery := `
		SELECT COUNT(DISTINCT p.id)
		FROM products p
		LEFT JOIN inventory i ON p.id = i.product_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true AND p.is_stockable = true
	`

	args := []interface{}{tenantID}
	argCount := 1

	if warehouseID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.warehouse_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND i.warehouse_id = $%d", argCount)
		args = append(args, warehouseID)
	}

	if categoryID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.category_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.category_id = $%d", argCount)
		args = append(args, categoryID)
	}

	baseQuery += " GROUP BY p.id, p.code, p.name, pc.name, p.cost_price"
	baseQuery += " ORDER BY total_value DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count valuation", "error", err)
		response.InternalError(c, "Failed to get inventory valuation")
		return
	}

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to get inventory valuation", "error", err)
		response.InternalError(c, "Failed to get inventory valuation")
		return
	}
	defer rows.Close()

	valuations := make([]*entity.InventoryValuationReport, 0)
	var totalInventoryValue float64

	for rows.Next() {
		var v entity.InventoryValuationReport

		err := rows.Scan(
			&v.ProductID, &v.ProductCode, &v.ProductName,
			&v.CategoryName, &v.QuantityOnHand, &v.AverageCost,
			&v.TotalValue, &v.LastPurchasePrice,
		)
		if err != nil {
			h.log.Error("Failed to scan valuation", "error", err)
			continue
		}

		totalInventoryValue += v.TotalValue
		valuations = append(valuations, &v)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, gin.H{
		"items":         valuations,
		"total_value":   totalInventoryValue,
		"currency":      "UZS", // TODO: Get from tenant settings
	}, pagination)
}
