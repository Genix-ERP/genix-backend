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

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND i.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

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

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

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
	organizationID, _ := middleware.GetOrganizationID(c)

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

	var variantID *uuid.UUID
	if input.VariantID != "" {
		vid, err := uuid.Parse(input.VariantID)
		if err == nil {
			variantID = &vid
		}
	}

	// Verify product exists and get cost_price and has_variants flag
	var productCostPrice float64
	var hasVariants bool
	err = h.db.QueryRow(
		"SELECT COALESCE(cost_price, 0), COALESCE(has_variants, false) FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		productID, tenantID,
	).Scan(&productCostPrice, &hasVariants)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Product")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch product", "error", err)
		response.InternalError(c, "Failed to adjust inventory")
		return
	}

	// If product has variants, require variant_id
	if hasVariants && variantID == nil {
		response.BadRequest(c, "This product has variants. Please specify a variant to adjust stock.")
		return
	}

	// If variant_id is provided, verify it belongs to this product
	if variantID != nil {
		var variantExists bool
		h.db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM product_variants WHERE id = $1 AND product_id = $2 AND deleted_at IS NULL)",
			variantID, productID,
		).Scan(&variantExists)
		if !variantExists {
			response.BadRequest(c, "Invalid variant ID for this product")
			return
		}
	}

	// Use provided unit_cost or fall back to product's cost_price
	unitCost := input.UnitCost
	if unitCost == 0 {
		unitCost = productCostPrice
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
		AND COALESCE(variant_id::text, '') = COALESCE($7::text, '')
	`, tenantID, productID, warehouseID, locationID, input.LotNumber, input.SerialNumber, variantID).Scan(&inventoryID, &currentQtyOnHand)

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
				id, tenant_id, organization_id, product_id, warehouse_id, location_id,
				lot_number, serial_number, variant_id, quantity_on_hand, quantity_reserved,
				unit_cost, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 0, $11, $12, $12)
		`, inventoryID, tenantID, organizationID, productID, warehouseID, locationID,
			lotNumber, serialNumber, variantID, input.Quantity, unitCost, now)

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
				unit_cost = CASE WHEN $2 > 0 THEN $2 ELSE unit_cost END,
				last_movement_date = $3,
				updated_at = $3
			WHERE id = $4
		`, input.Quantity, unitCost, now, inventoryID)

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
			id, tenant_id, organization_id, inventory_id, transaction_type, quantity,
			unit_cost, total_cost, reason, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $11)
	`, transactionID, tenantID, organizationID, inventoryID, transactionType, input.Quantity,
		unitCost, input.Quantity*unitCost, reason, notes, now, userID)

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

	newBalance := currentQtyOnHand + input.Quantity

	// Trigger workflow rules for stock changes
	go func() {
		// Get product info for the trigger
		var productName, productCode string
		var reorderPoint float64
		h.db.QueryRow(`SELECT name, code, COALESCE(reorder_point, 0) FROM products WHERE id = $1`, input.ProductID).
			Scan(&productName, &productCode, &reorderPoint)

		if reorderPoint > 0 && newBalance <= reorderPoint {
			h.EvaluateWorkflowRules(tenantID, "inventory.low_stock", map[string]interface{}{
				"product_id":    input.ProductID,
				"product_name":  productName,
				"product_code":  productCode,
				"reorder_point": reorderPoint,
				"available":     newBalance,
			})
		}

		h.EvaluateWorkflowRules(tenantID, "inventory.adjusted", map[string]interface{}{
			"product_id":   input.ProductID,
			"product_name": productName,
			"product_code": productCode,
			"quantity":     input.Quantity,
			"new_balance":  newBalance,
		})
	}()

	response.Success(c, gin.H{
		"message":        "Inventory adjusted successfully",
		"inventory_id":   inventoryID,
		"transaction_id": transactionID,
		"quantity":       input.Quantity,
		"new_balance":    newBalance,
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
	organizationID, _ := middleware.GetOrganizationID(c)

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

	// If unit_cost is 0, fall back to product's cost_price
	if unitCost == 0 {
		h.db.QueryRow(
			"SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1 AND tenant_id = $2",
			productID, tenantID,
		).Scan(&unitCost)
	}

	// Decrease source inventory
	_, err = tx.Exec(`
		UPDATE inventory SET
			quantity_on_hand = quantity_on_hand - $1,
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
				id, tenant_id, organization_id, product_id, warehouse_id, location_id,
				quantity_on_hand, quantity_reserved,
				unit_cost, last_movement_date, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, $9, $9, $9)
		`, destInventoryID, tenantID, organizationID, productID, toWarehouseID, toLocationID,
			input.Quantity, unitCost, now)

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
			id, tenant_id, organization_id, inventory_id, transaction_type, quantity,
			unit_cost, total_cost, from_warehouse_id, to_warehouse_id,
			from_location_id, to_location_id, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $14)
	`, uuid.New(), tenantID, organizationID, sourceInventoryID, transactionType, -input.Quantity,
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
			id, tenant_id, organization_id, inventory_id, transaction_type, quantity,
			unit_cost, total_cost, from_warehouse_id, to_warehouse_id,
			from_location_id, to_location_id, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $14)
	`, uuid.New(), tenantID, organizationID, destInventoryID, transactionType, input.Quantity,
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
			   COALESCE(fw.name, iw.name) as from_warehouse_name, tw.name as to_warehouse_name,
			   e.first_name as created_by_first_name, e.last_name as created_by_last_name
		FROM inventory_transactions t
		JOIN inventory i ON t.inventory_id = i.id
		JOIN products p ON i.product_id = p.id
		LEFT JOIN warehouses fw ON t.from_warehouse_id = fw.id
		LEFT JOIN warehouses tw ON t.to_warehouse_id = tw.id
		LEFT JOIN warehouses iw ON i.warehouse_id = iw.id
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

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND t.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND t.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

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

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

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

// =====================================================
// BILL OF MATERIALS (BOM) HANDLERS
// =====================================================

// ListBOMs returns a paginated list of BOMs
func (h *Handler) ListBOMs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

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
	productID := c.Query("product_id")
	bomType := c.Query("bom_type")
	isActive := c.Query("is_active")

	baseQuery := `
		SELECT b.id, b.code, b.name, b.product_id, b.bom_type, b.quantity, b.version,
			   b.is_active, b.is_default, b.effective_date, b.expiry_date, b.notes,
			   b.created_at,
			   p.code as product_code, p.name as product_name,
			   (SELECT COUNT(*) FROM bom_lines bl WHERE bl.bom_id = b.id) as line_count,
			   COALESCE((SELECT SUM(bl2.quantity * COALESCE(cp.cost_price, 0) * (1 + bl2.scrap_percent/100))
			     FROM bom_lines bl2 JOIN products cp ON bl2.component_id = cp.id
			     WHERE bl2.bom_id = b.id), 0) as total_cost
		FROM product_boms b
		JOIN products p ON b.product_id = p.id
		WHERE b.tenant_id = $1 AND b.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM product_boms b WHERE b.tenant_id = $1 AND b.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND b.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND b.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if productID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND b.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND b.product_id = $%d", argCount)
		args = append(args, productID)
	}

	if bomType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND b.bom_type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND b.bom_type = $%d", argCount)
		args = append(args, bomType)
	}

	if isActive != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND b.is_active = $%d", argCount)
		countQuery += fmt.Sprintf(" AND b.is_active = $%d", argCount)
		args = append(args, isActive == "true")
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (b.code ILIKE $%d OR b.name ILIKE $%d OR p.code ILIKE $%d OR p.name ILIKE $%d)", argCount, argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count BOMs", "error", err)
		response.InternalError(c, "Failed to list BOMs")
		return
	}

	baseQuery += " ORDER BY b.code ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list BOMs", "error", err)
		response.InternalError(c, "Failed to list BOMs")
		return
	}
	defer rows.Close()

	boms := make([]*entity.BOMResponse, 0)
	for rows.Next() {
		var b entity.BOMResponse
		var effectiveDate, expiryDate sql.NullTime
		var notes sql.NullString

		err := rows.Scan(
			&b.ID, &b.Code, &b.Name, &b.ProductID, &b.BOMType, &b.Quantity, &b.Version,
			&b.IsActive, &b.IsDefault, &effectiveDate, &expiryDate, &notes,
			&b.CreatedAt,
			&b.ProductCode, &b.ProductName, &b.LineCount, &b.TotalCost,
		)
		if err != nil {
			h.log.Error("Failed to scan BOM", "error", err)
			continue
		}

		if effectiveDate.Valid {
			s := effectiveDate.Time.Format("2006-01-02")
			b.EffectiveDate = &s
		}
		if expiryDate.Valid {
			s := expiryDate.Time.Format("2006-01-02")
			b.ExpiryDate = &s
		}

		boms = append(boms, &b)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, boms, pagination)
}

// GetBOM returns a single BOM with lines
func (h *Handler) GetBOM(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	var b entity.BOMResponse
	var effectiveDate, expiryDate sql.NullTime
	var notes sql.NullString

	err = h.db.QueryRow(`
		SELECT b.id, b.code, b.name, b.product_id, b.bom_type, b.quantity, b.version,
			   b.is_active, b.is_default, b.effective_date, b.expiry_date, b.notes,
			   b.created_at,
			   p.code as product_code, p.name as product_name
		FROM product_boms b
		JOIN products p ON b.product_id = p.id
		WHERE b.id = $1 AND b.tenant_id = $2 AND b.deleted_at IS NULL
	`, bomID, tenantID).Scan(
		&b.ID, &b.Code, &b.Name, &b.ProductID, &b.BOMType, &b.Quantity, &b.Version,
		&b.IsActive, &b.IsDefault, &effectiveDate, &expiryDate, &notes,
		&b.CreatedAt,
		&b.ProductCode, &b.ProductName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "BOM")
		return
	}
	if err != nil {
		h.log.Error("Failed to get BOM", "error", err)
		response.InternalError(c, "Failed to get BOM")
		return
	}

	if effectiveDate.Valid {
		s := effectiveDate.Time.Format("2006-01-02")
		b.EffectiveDate = &s
	}
	if expiryDate.Valid {
		s := expiryDate.Time.Format("2006-01-02")
		b.ExpiryDate = &s
	}

	// Get BOM lines
	rows, err := h.db.Query(`
		SELECT l.id, l.line_number, l.component_id, l.quantity, l.unit_of_measure,
			   l.scrap_percent, l.is_optional,
			   p.code as component_code, p.name as component_name, COALESCE(p.cost_price, 0) as unit_cost
		FROM bom_lines l
		JOIN products p ON l.component_id = p.id
		WHERE l.bom_id = $1
		ORDER BY l.line_number
	`, bomID)
	if err != nil {
		h.log.Error("Failed to get BOM lines", "error", err)
		response.InternalError(c, "Failed to get BOM")
		return
	}
	defer rows.Close()

	b.Lines = make([]entity.BOMLineResponse, 0)
	var totalCost float64

	for rows.Next() {
		var l entity.BOMLineResponse
		err := rows.Scan(
			&l.ID, &l.LineNumber, &l.ComponentID, &l.Quantity, &l.UnitOfMeasure,
			&l.ScrapPercent, &l.IsOptional,
			&l.ComponentCode, &l.ComponentName, &l.UnitCost,
		)
		if err != nil {
			h.log.Error("Failed to scan BOM line", "error", err)
			continue
		}
		l.TotalCost = l.Quantity * l.UnitCost * (1 + l.ScrapPercent/100)
		totalCost += l.TotalCost
		b.Lines = append(b.Lines, l)
	}

	b.TotalCost = totalCost

	response.Success(c, b)
}

// CreateBOM creates a new BOM
func (h *Handler) CreateBOM(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateBOMInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Verify product exists
	var productExists bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		productID, tenantID).Scan(&productExists)
	if !productExists {
		response.NotFound(c, "Product")
		return
	}

	// Check for duplicate code
	var codeExists bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM product_boms WHERE code = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		input.Code, tenantID).Scan(&codeExists)
	if codeExists {
		response.Conflict(c, "BOM with this code already exists")
		return
	}

	now := time.Now()
	bomID := uuid.New()

	// Get organization ID from middleware header
	var orgIDPtr *uuid.UUID
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	bomType := "manufacturing"
	if input.BOMType != "" {
		bomType = input.BOMType
	}

	quantity := 1.0
	if input.Quantity > 0 {
		quantity = input.Quantity
	}

	var effectiveDate, expiryDate *time.Time
	if input.EffectiveDate != "" {
		t, _ := time.Parse("2006-01-02", input.EffectiveDate)
		effectiveDate = &t
	}
	if input.ExpiryDate != "" {
		t, _ := time.Parse("2006-01-02", input.ExpiryDate)
		expiryDate = &t
	}

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create BOM")
		return
	}
	defer tx.Rollback()

	// If this is default, unset other defaults for this product
	if input.IsDefault {
		tx.Exec("UPDATE product_boms SET is_default = false WHERE product_id = $1 AND tenant_id = $2", productID, tenantID)
	}

	_, err = tx.Exec(`
		INSERT INTO product_boms (
			id, tenant_id, organization_id, code, name, product_id, bom_type, quantity, version,
			is_active, is_default, effective_date, expiry_date, notes,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, true, $9, $10, $11, $12, $13, $14, $14)
	`, bomID, tenantID, orgIDPtr, input.Code, input.Name, productID, bomType, quantity,
		input.IsDefault, effectiveDate, expiryDate, notes, userID, now)

	if err != nil {
		h.log.Error("Failed to create BOM", "error", err)
		response.InternalError(c, "Failed to create BOM")
		return
	}

	// Create lines
	for i, lineInput := range input.Lines {
		componentID, err := uuid.Parse(lineInput.ComponentID)
		if err != nil {
			continue
		}

		lineID := uuid.New()
		unitOfMeasure := "pcs"
		if lineInput.UnitOfMeasure != "" {
			unitOfMeasure = lineInput.UnitOfMeasure
		}

		var substituteID *uuid.UUID
		if lineInput.SubstituteComponentID != "" {
			sid, err := uuid.Parse(lineInput.SubstituteComponentID)
			if err == nil {
				substituteID = &sid
			}
		}

		var lineNotes *string
		if lineInput.Notes != "" {
			lineNotes = &lineInput.Notes
		}

		_, err = tx.Exec(`
			INSERT INTO bom_lines (
				id, bom_id, line_number, component_id, quantity, unit_of_measure,
				scrap_percent, is_optional, substitute_component_id, operation_sequence, notes,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
		`, lineID, bomID, i+1, componentID, lineInput.Quantity, unitOfMeasure,
			lineInput.ScrapPercent, lineInput.IsOptional, substituteID, lineInput.OperationSequence, lineNotes, now)

		if err != nil {
			h.log.Error("Failed to create BOM line", "error", err)
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create BOM")
		return
	}

	// Return full BOM data so frontend can display it immediately
	var productCode, productName string
	h.db.QueryRow("SELECT code, name FROM products WHERE id = $1", productID).Scan(&productCode, &productName)

	lineCount := len(input.Lines)

	response.Created(c, gin.H{
		"id":           bomID,
		"code":         input.Code,
		"name":         input.Name,
		"product_id":   productID,
		"product_code": productCode,
		"product_name": productName,
		"bom_type":     bomType,
		"quantity":     quantity,
		"version":      1,
		"is_active":    true,
		"is_default":   input.IsDefault,
		"line_count":   lineCount,
		"message":      "BOM created successfully",
	})
}

// UpdateBOM updates a BOM
func (h *Handler) UpdateBOM(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	var input entity.UpdateBOMInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Verify BOM exists
	var exists bool
	var productID uuid.UUID
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM product_boms WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL), product_id FROM product_boms WHERE id = $1",
		bomID, tenantID).Scan(&exists, &productID)
	if !exists {
		response.NotFound(c, "BOM")
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.BOMType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("bom_type = $%d", argCount))
		args = append(args, *input.BOMType)
	}
	if input.Quantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("quantity = $%d", argCount))
		args = append(args, *input.Quantity)
	}
	if input.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
	}
	if input.IsDefault != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_default = $%d", argCount))
		args = append(args, *input.IsDefault)

		// If setting as default, unset others
		if *input.IsDefault {
			h.db.Exec("UPDATE product_boms SET is_default = false WHERE product_id = $1 AND tenant_id = $2 AND id != $3",
				productID, tenantID, bomID)
		}
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, bomID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE product_boms SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update BOM", "error", err)
		response.InternalError(c, "Failed to update BOM")
		return
	}

	response.Success(c, gin.H{"message": "BOM updated successfully"})
}

// DeleteBOM soft deletes a BOM
func (h *Handler) DeleteBOM(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	result, err := h.db.Exec(
		"UPDATE product_boms SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), bomID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete BOM", "error", err)
		response.InternalError(c, "Failed to delete BOM")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "BOM")
		return
	}

	response.Success(c, gin.H{"message": "BOM deleted successfully"})
}

// CreateBOMLine adds a line to a BOM
func (h *Handler) CreateBOMLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	var input entity.CreateBOMLineInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	componentID, err := uuid.Parse(input.ComponentID)
	if err != nil {
		response.BadRequest(c, "Invalid component ID")
		return
	}

	// Verify BOM exists
	var bomExists bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM product_boms WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		bomID, tenantID).Scan(&bomExists)
	if !bomExists {
		response.NotFound(c, "BOM")
		return
	}

	// Get next line number
	var maxLineNumber int
	h.db.QueryRow("SELECT COALESCE(MAX(line_number), 0) FROM bom_lines WHERE bom_id = $1", bomID).Scan(&maxLineNumber)

	lineID := uuid.New()
	now := time.Now()

	unitOfMeasure := "pcs"
	if input.UnitOfMeasure != "" {
		unitOfMeasure = input.UnitOfMeasure
	}

	var substituteID *uuid.UUID
	if input.SubstituteComponentID != "" {
		sid, err := uuid.Parse(input.SubstituteComponentID)
		if err == nil {
			substituteID = &sid
		}
	}

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	_, err = h.db.Exec(`
		INSERT INTO bom_lines (
			id, bom_id, line_number, component_id, quantity, unit_of_measure,
			scrap_percent, is_optional, substitute_component_id, operation_sequence, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
	`, lineID, bomID, maxLineNumber+1, componentID, input.Quantity, unitOfMeasure,
		input.ScrapPercent, input.IsOptional, substituteID, input.OperationSequence, notes, now)

	if err != nil {
		h.log.Error("Failed to create BOM line", "error", err)
		response.InternalError(c, "Failed to create BOM line")
		return
	}

	// Update BOM timestamp
	h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", now, bomID)

	response.Created(c, gin.H{
		"id":          lineID,
		"line_number": maxLineNumber + 1,
		"message":     "BOM line created successfully",
	})
}

// DeleteBOMLine removes a line from a BOM
func (h *Handler) DeleteBOMLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	lineID, err := uuid.Parse(c.Param("lineId"))
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}

	// Verify BOM belongs to tenant
	var bomExists bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM product_boms WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		bomID, tenantID).Scan(&bomExists)
	if !bomExists {
		response.NotFound(c, "BOM")
		return
	}

	result, err := h.db.Exec("DELETE FROM bom_lines WHERE id = $1 AND bom_id = $2", lineID, bomID)
	if err != nil {
		h.log.Error("Failed to delete BOM line", "error", err)
		response.InternalError(c, "Failed to delete BOM line")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "BOM line")
		return
	}

	// Update BOM timestamp
	h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", time.Now(), bomID)

	response.Success(c, gin.H{"message": "BOM line deleted successfully"})
}

// =====================================================
// BOM OPERATIONS HANDLERS
// =====================================================

// ListBOMOperations returns all operations for a BOM
func (h *Handler) ListBOMOperations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	// Verify BOM exists and belongs to tenant
	var bomTenantID uuid.UUID
	err = h.db.QueryRow("SELECT tenant_id FROM product_boms WHERE id = $1 AND deleted_at IS NULL", bomID).Scan(&bomTenantID)
	if err != nil {
		response.NotFound(c, "BOM")
		return
	}
	if bomTenantID != tenantID {
		response.Forbidden(c, "Access denied")
		return
	}

	rows, err := h.db.Query(`
		SELECT o.id, o.bom_id, o.sequence, o.operation_name, o.work_center, o.work_center_id,
			   o.setup_time_minutes, o.run_time_minutes, o.labor_cost, o.overhead_cost, o.notes,
			   o.created_at, o.updated_at,
			   wc.name as work_center_name, wc.hourly_cost as work_center_hourly_cost
		FROM bom_operations o
		LEFT JOIN work_centers wc ON o.work_center_id = wc.id
		WHERE o.bom_id = $1
		ORDER BY o.sequence
	`, bomID)
	if err != nil {
		h.log.Error("Failed to list BOM operations", "error", err)
		response.InternalError(c, "Failed to list BOM operations")
		return
	}
	defer rows.Close()

	type BOMOperation struct {
		ID                   uuid.UUID  `json:"id"`
		BOMID                uuid.UUID  `json:"bom_id"`
		Sequence             int        `json:"sequence"`
		OperationName        string     `json:"operation_name"`
		WorkCenter           *string    `json:"work_center"`
		WorkCenterID         *uuid.UUID `json:"work_center_id"`
		SetupTimeMinutes     float64    `json:"setup_time_minutes"`
		RunTimeMinutes       float64    `json:"run_time_minutes"`
		LaborCost            float64    `json:"labor_cost"`
		OverheadCost         float64    `json:"overhead_cost"`
		Notes                *string    `json:"notes"`
		CreatedAt            time.Time  `json:"created_at"`
		UpdatedAt            time.Time  `json:"updated_at"`
		WorkCenterName       *string    `json:"work_center_name,omitempty"`
		WorkCenterHourlyCost *float64   `json:"work_center_hourly_cost,omitempty"`
	}

	operations := make([]BOMOperation, 0)
	for rows.Next() {
		var op BOMOperation
		var workCenter, notes, wcName sql.NullString
		var wcID uuid.NullUUID
		var wcHourlyCost sql.NullFloat64

		err := rows.Scan(
			&op.ID, &op.BOMID, &op.Sequence, &op.OperationName, &workCenter, &wcID,
			&op.SetupTimeMinutes, &op.RunTimeMinutes, &op.LaborCost, &op.OverheadCost, &notes,
			&op.CreatedAt, &op.UpdatedAt, &wcName, &wcHourlyCost,
		)
		if err != nil {
			h.log.Error("Failed to scan BOM operation", "error", err)
			continue
		}

		if workCenter.Valid {
			op.WorkCenter = &workCenter.String
		}
		if wcID.Valid {
			op.WorkCenterID = &wcID.UUID
		}
		if notes.Valid {
			op.Notes = &notes.String
		}
		if wcName.Valid {
			op.WorkCenterName = &wcName.String
		}
		if wcHourlyCost.Valid {
			op.WorkCenterHourlyCost = &wcHourlyCost.Float64
		}

		operations = append(operations, op)
	}

	response.Success(c, operations)
}

// CreateBOMOperation adds an operation to a BOM
func (h *Handler) CreateBOMOperation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	// Verify BOM exists and belongs to tenant
	var bomTenantID uuid.UUID
	err = h.db.QueryRow("SELECT tenant_id FROM product_boms WHERE id = $1 AND deleted_at IS NULL", bomID).Scan(&bomTenantID)
	if err != nil {
		response.NotFound(c, "BOM")
		return
	}
	if bomTenantID != tenantID {
		response.Forbidden(c, "Access denied")
		return
	}

	var input struct {
		Sequence         int     `json:"sequence"`
		OperationName    string  `json:"operation_name" binding:"required"`
		WorkCenter       string  `json:"work_center"`
		WorkCenterID     string  `json:"work_center_id"`
		SetupTimeMinutes float64 `json:"setup_time_minutes"`
		RunTimeMinutes   float64 `json:"run_time_minutes"`
		LaborCost        float64 `json:"labor_cost"`
		OverheadCost     float64 `json:"overhead_cost"`
		Notes            string  `json:"notes"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Get next sequence if not provided
	if input.Sequence == 0 {
		var maxSequence int
		h.db.QueryRow("SELECT COALESCE(MAX(sequence), 0) FROM bom_operations WHERE bom_id = $1", bomID).Scan(&maxSequence)
		input.Sequence = maxSequence + 10
	}

	id := uuid.New()
	now := time.Now()

	var workCenterID *uuid.UUID
	if input.WorkCenterID != "" {
		wcID, err := uuid.Parse(input.WorkCenterID)
		if err == nil {
			// Verify work center exists and belongs to tenant
			var wcExists bool
			h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM work_centers WHERE id = $1 AND tenant_id = $2)",
				wcID, tenantID).Scan(&wcExists)
			if wcExists {
				workCenterID = &wcID
			}
		}
	}

	var workCenter, notes *string
	if input.WorkCenter != "" {
		workCenter = &input.WorkCenter
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	_, err = h.db.Exec(`
		INSERT INTO bom_operations (
			id, bom_id, sequence, operation_name, work_center, work_center_id,
			setup_time_minutes, run_time_minutes, labor_cost, overhead_cost, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
	`, id, bomID, input.Sequence, input.OperationName, workCenter, workCenterID,
		input.SetupTimeMinutes, input.RunTimeMinutes, input.LaborCost, input.OverheadCost, notes, now)

	if err != nil {
		h.log.Error("Failed to create BOM operation", "error", err)
		response.InternalError(c, "Failed to create BOM operation")
		return
	}

	// Update BOM timestamp
	h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", now, bomID)

	response.Created(c, gin.H{
		"id":       id,
		"sequence": input.Sequence,
		"message":  "BOM operation created successfully",
	})
}

// UpdateBOMOperation updates an operation in a BOM
func (h *Handler) UpdateBOMOperation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	operationID, err := uuid.Parse(c.Param("operationId"))
	if err != nil {
		response.BadRequest(c, "Invalid operation ID")
		return
	}

	// Verify BOM exists and belongs to tenant
	var bomTenantID uuid.UUID
	err = h.db.QueryRow("SELECT tenant_id FROM product_boms WHERE id = $1 AND deleted_at IS NULL", bomID).Scan(&bomTenantID)
	if err != nil {
		response.NotFound(c, "BOM")
		return
	}
	if bomTenantID != tenantID {
		response.Forbidden(c, "Access denied")
		return
	}

	var input struct {
		Sequence         *int     `json:"sequence"`
		OperationName    *string  `json:"operation_name"`
		WorkCenter       *string  `json:"work_center"`
		WorkCenterID     *string  `json:"work_center_id"`
		SetupTimeMinutes *float64 `json:"setup_time_minutes"`
		RunTimeMinutes   *float64 `json:"run_time_minutes"`
		LaborCost        *float64 `json:"labor_cost"`
		OverheadCost     *float64 `json:"overhead_cost"`
		Notes            *string  `json:"notes"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if input.Sequence != nil {
		updates = append(updates, fmt.Sprintf("sequence = $%d", argIndex))
		args = append(args, *input.Sequence)
		argIndex++
	}
	if input.OperationName != nil {
		updates = append(updates, fmt.Sprintf("operation_name = $%d", argIndex))
		args = append(args, *input.OperationName)
		argIndex++
	}
	if input.WorkCenter != nil {
		updates = append(updates, fmt.Sprintf("work_center = $%d", argIndex))
		args = append(args, *input.WorkCenter)
		argIndex++
	}
	if input.WorkCenterID != nil {
		if *input.WorkCenterID == "" {
			updates = append(updates, fmt.Sprintf("work_center_id = $%d", argIndex))
			args = append(args, nil)
		} else {
			wcID, err := uuid.Parse(*input.WorkCenterID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("work_center_id = $%d", argIndex))
				args = append(args, wcID)
			}
		}
		argIndex++
	}
	if input.SetupTimeMinutes != nil {
		updates = append(updates, fmt.Sprintf("setup_time_minutes = $%d", argIndex))
		args = append(args, *input.SetupTimeMinutes)
		argIndex++
	}
	if input.RunTimeMinutes != nil {
		updates = append(updates, fmt.Sprintf("run_time_minutes = $%d", argIndex))
		args = append(args, *input.RunTimeMinutes)
		argIndex++
	}
	if input.LaborCost != nil {
		updates = append(updates, fmt.Sprintf("labor_cost = $%d", argIndex))
		args = append(args, *input.LaborCost)
		argIndex++
	}
	if input.OverheadCost != nil {
		updates = append(updates, fmt.Sprintf("overhead_cost = $%d", argIndex))
		args = append(args, *input.OverheadCost)
		argIndex++
	}
	if input.Notes != nil {
		updates = append(updates, fmt.Sprintf("notes = $%d", argIndex))
		args = append(args, *input.Notes)
		argIndex++
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	args = append(args, operationID, bomID)

	query := fmt.Sprintf("UPDATE bom_operations SET %s WHERE id = $%d AND bom_id = $%d",
		strings.Join(updates, ", "), argIndex, argIndex+1)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update BOM operation", "error", err)
		response.InternalError(c, "Failed to update BOM operation")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "BOM operation")
		return
	}

	// Update BOM timestamp
	h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", time.Now(), bomID)

	response.Success(c, gin.H{"message": "BOM operation updated successfully"})
}

// DeleteBOMOperation removes an operation from a BOM
func (h *Handler) DeleteBOMOperation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	bomID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid BOM ID")
		return
	}

	operationID, err := uuid.Parse(c.Param("operationId"))
	if err != nil {
		response.BadRequest(c, "Invalid operation ID")
		return
	}

	// Verify BOM exists and belongs to tenant
	var bomTenantID uuid.UUID
	err = h.db.QueryRow("SELECT tenant_id FROM product_boms WHERE id = $1 AND deleted_at IS NULL", bomID).Scan(&bomTenantID)
	if err != nil {
		response.NotFound(c, "BOM")
		return
	}
	if bomTenantID != tenantID {
		response.Forbidden(c, "Access denied")
		return
	}

	result, err := h.db.Exec("DELETE FROM bom_operations WHERE id = $1 AND bom_id = $2", operationID, bomID)
	if err != nil {
		h.log.Error("Failed to delete BOM operation", "error", err)
		response.InternalError(c, "Failed to delete BOM operation")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "BOM operation")
		return
	}

	// Update BOM timestamp
	h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", time.Now(), bomID)

	response.Success(c, gin.H{"message": "BOM operation deleted successfully"})
}

// =====================================================
// SCRAP MANAGEMENT HANDLERS
// =====================================================

// ListScrapReasons returns all scrap reasons
func (h *Handler) ListScrapReasons(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, code, name, description, is_active, requires_approval, account_id, created_at
		FROM scrap_reasons
		WHERE tenant_id = $1
		ORDER BY code
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to list scrap reasons", "error", err)
		response.InternalError(c, "Failed to list scrap reasons")
		return
	}
	defer rows.Close()

	reasons := make([]*entity.ScrapReason, 0)
	for rows.Next() {
		var r entity.ScrapReason
		var description sql.NullString
		var accountID sql.NullString

		err := rows.Scan(&r.ID, &r.Code, &r.Name, &description, &r.IsActive, &r.RequiresApproval, &accountID, &r.CreatedAt)
		if err != nil {
			continue
		}

		if description.Valid {
			r.Description = &description.String
		}
		if accountID.Valid {
			aid, _ := uuid.Parse(accountID.String)
			r.AccountID = &aid
		}

		reasons = append(reasons, &r)
	}

	response.Success(c, reasons)
}

// CreateScrapReason creates a new scrap reason
func (h *Handler) CreateScrapReason(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateScrapReasonInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check for duplicate code
	var exists bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM scrap_reasons WHERE code = $1 AND tenant_id = $2)",
		input.Code, tenantID).Scan(&exists)
	if exists {
		response.Conflict(c, "Scrap reason with this code already exists")
		return
	}

	id := uuid.New()
	now := time.Now()

	var description, accountID *string
	if input.Description != "" {
		description = &input.Description
	}
	if input.AccountID != "" {
		accountID = &input.AccountID
	}

	_, err := h.db.Exec(`
		INSERT INTO scrap_reasons (id, tenant_id, code, name, description, is_active, requires_approval, account_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, true, $6, $7, $8, $8)
	`, id, tenantID, input.Code, input.Name, description, input.RequiresApproval, accountID, now)

	if err != nil {
		h.log.Error("Failed to create scrap reason", "error", err)
		response.InternalError(c, "Failed to create scrap reason")
		return
	}

	response.Created(c, gin.H{"id": id, "message": "Scrap reason created successfully"})
}

// ListScrapOrders returns a paginated list of scrap orders
func (h *Handler) ListScrapOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	status := c.Query("status")
	productID := c.Query("product_id")
	warehouseID := c.Query("warehouse_id")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	baseQuery := `
		SELECT s.id, s.scrap_number, s.product_id, s.warehouse_id, s.quantity,
			   COALESCE(s.unit_cost, 0), COALESCE(s.total_cost, 0), s.reason, s.reason_notes,
			   s.scrap_date, s.status, s.created_at,
			   p.code as product_code, p.name as product_name,
			   w.code as warehouse_code, w.name as warehouse_name
		FROM scrap_orders s
		JOIN products p ON s.product_id = p.id
		JOIN warehouses w ON s.warehouse_id = w.id
		WHERE s.tenant_id = $1 AND s.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM scrap_orders s WHERE s.tenant_id = $1 AND s.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND s.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND s.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND s.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND s.status = $%d", argCount)
		args = append(args, status)
	}

	if productID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND s.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND s.product_id = $%d", argCount)
		args = append(args, productID)
	}

	if warehouseID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND s.warehouse_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND s.warehouse_id = $%d", argCount)
		args = append(args, warehouseID)
	}

	if dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND s.scrap_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND s.scrap_date >= $%d", argCount)
		args = append(args, dateFrom)
	}

	if dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND s.scrap_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND s.scrap_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count scrap orders", "error", err)
		response.InternalError(c, "Failed to list scrap orders")
		return
	}

	baseQuery += " ORDER BY s.scrap_date DESC, s.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list scrap orders", "error", err)
		response.InternalError(c, "Failed to list scrap orders")
		return
	}
	defer rows.Close()

	orders := make([]*entity.ScrapOrderResponse, 0)
	for rows.Next() {
		var o entity.ScrapOrderResponse
		var reason, reasonNotes sql.NullString
		var productCode, productName, warehouseCode, warehouseName string

		err := rows.Scan(
			&o.ID, &o.ScrapNumber, &o.ProductID, &o.WarehouseID, &o.Quantity,
			&o.UnitCost, &o.TotalCost, &reason, &reasonNotes,
			&o.ScrapDate, &o.Status, &o.CreatedAt,
			&productCode, &productName, &warehouseCode, &warehouseName,
		)
		if err != nil {
			h.log.Error("Failed to scan scrap order", "error", err)
			continue
		}

		o.ProductCode = productCode
		o.ProductName = productName
		o.WarehouseName = warehouseName

		if reason.Valid {
			o.Reason = reason.String
		}
		if reasonNotes.Valid {
			o.ReasonNotes = reasonNotes.String
		}

		orders = append(orders, &o)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, orders, pagination)
}

// GetScrapOrder returns a single scrap order
func (h *Handler) GetScrapOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid scrap order ID")
		return
	}

	var o entity.ScrapOrderResponse
	var reason, reasonNotes sql.NullString
	var productCode, productName, warehouseName string

	err = h.db.QueryRow(`
		SELECT s.id, s.scrap_number, s.product_id, s.warehouse_id, s.quantity,
			   COALESCE(s.unit_cost, 0), COALESCE(s.total_cost, 0), s.reason, s.reason_notes,
			   s.scrap_date, s.status, s.created_at,
			   p.code as product_code, p.name as product_name,
			   w.name as warehouse_name
		FROM scrap_orders s
		JOIN products p ON s.product_id = p.id
		JOIN warehouses w ON s.warehouse_id = w.id
		WHERE s.id = $1 AND s.tenant_id = $2 AND s.deleted_at IS NULL
	`, orderID, tenantID).Scan(
		&o.ID, &o.ScrapNumber, &o.ProductID, &o.WarehouseID, &o.Quantity,
		&o.UnitCost, &o.TotalCost, &reason, &reasonNotes,
		&o.ScrapDate, &o.Status, &o.CreatedAt,
		&productCode, &productName, &warehouseName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Scrap order")
		return
	}
	if err != nil {
		h.log.Error("Failed to get scrap order", "error", err)
		response.InternalError(c, "Failed to get scrap order")
		return
	}

	o.ProductCode = productCode
	o.ProductName = productName
	o.WarehouseName = warehouseName

	if reason.Valid {
		o.Reason = reason.String
	}
	if reasonNotes.Valid {
		o.ReasonNotes = reasonNotes.String
	}

	response.Success(c, o)
}

// CreateScrapOrder creates a new scrap order
func (h *Handler) CreateScrapOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateScrapOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

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

	scrapDate, err := time.Parse("2006-01-02", input.ScrapDate)
	if err != nil {
		response.BadRequest(c, "Invalid scrap date format")
		return
	}

	// Generate scrap number
	var lastNumber int
	h.db.QueryRow(`SELECT COALESCE(MAX(CAST(SUBSTRING(scrap_number FROM 'SCP-(\d+)') AS INTEGER)), 0) FROM scrap_orders WHERE tenant_id = $1`, tenantID).Scan(&lastNumber)
	scrapNumber := fmt.Sprintf("SCP-%06d", lastNumber+1)

	id := uuid.New()
	now := time.Now()

	var locationID, lotID, scrapReasonID *uuid.UUID
	if input.LocationID != "" {
		lid, _ := uuid.Parse(input.LocationID)
		locationID = &lid
	}
	if input.LotID != "" {
		lid, _ := uuid.Parse(input.LotID)
		lotID = &lid
	}
	if input.ScrapReasonID != "" {
		srid, _ := uuid.Parse(input.ScrapReasonID)
		scrapReasonID = &srid
	}

	var reason, reasonNotes, notes *string
	if input.Reason != "" {
		reason = &input.Reason
	}
	if input.ReasonNotes != "" {
		reasonNotes = &input.ReasonNotes
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	totalCost := input.Quantity * input.UnitCost

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	_, err = h.db.Exec(`
		INSERT INTO scrap_orders (
			id, tenant_id, organization_id, scrap_number, product_id, warehouse_id, location_id, lot_id,
			quantity, unit_cost, total_cost, scrap_reason_id, reason, reason_notes,
			scrap_date, status, scrapped_by, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'draft', $16, $17, $18, $18)
	`, id, tenantID, orgIDPtr, scrapNumber, productID, warehouseID, locationID, lotID,
		input.Quantity, input.UnitCost, totalCost, scrapReasonID, reason, reasonNotes,
		scrapDate, userID, notes, now)

	if err != nil {
		h.log.Error("Failed to create scrap order", "error", err)
		response.InternalError(c, "Failed to create scrap order")
		return
	}

	response.Created(c, gin.H{
		"id":           id,
		"scrap_number": scrapNumber,
		"message":      "Scrap order created successfully",
	})
}

// ConfirmScrapOrder confirms and processes a scrap order
func (h *Handler) ConfirmScrapOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid scrap order ID")
		return
	}

	// Get scrap order details
	var productID, warehouseID uuid.UUID
	var locationID sql.NullString
	var quantity float64
	var status string

	err = h.db.QueryRow(`
		SELECT product_id, warehouse_id, location_id, quantity, status
		FROM scrap_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, orderID, tenantID).Scan(&productID, &warehouseID, &locationID, &quantity, &status)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Scrap order")
		return
	}
	if err != nil {
		h.log.Error("Failed to get scrap order", "error", err)
		response.InternalError(c, "Failed to confirm scrap order")
		return
	}

	if status != "draft" && status != "approved" {
		response.BadRequest(c, "Only draft or approved scrap orders can be confirmed")
		return
	}

	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to confirm scrap order")
		return
	}
	defer tx.Rollback()

	// Reduce inventory
	var locID *uuid.UUID
	if locationID.Valid {
		lid, _ := uuid.Parse(locationID.String)
		locID = &lid
	}

	result, err := tx.Exec(`
		UPDATE inventory SET
			quantity_on_hand = quantity_on_hand - $1,
			quantity_available = quantity_available - $1,
			total_value = (quantity_on_hand - $1) * unit_cost,
			last_movement_date = $2,
			updated_at = $2
		WHERE tenant_id = $3 AND product_id = $4 AND warehouse_id = $5
		AND COALESCE(location_id::text, '') = COALESCE($6::text, '')
		AND quantity_available >= $1
	`, quantity, now, tenantID, productID, warehouseID, locID)

	if err != nil {
		h.log.Error("Failed to reduce inventory", "error", err)
		response.InternalError(c, "Failed to confirm scrap order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.BadRequest(c, "Insufficient inventory to scrap")
		return
	}

	// Update scrap order status
	_, err = tx.Exec(`
		UPDATE scrap_orders SET
			status = 'completed',
			approved_by = $1,
			approved_at = $2,
			completed_at = $2,
			updated_at = $2
		WHERE id = $3
	`, userID, now, orderID)

	if err != nil {
		h.log.Error("Failed to update scrap order", "error", err)
		response.InternalError(c, "Failed to confirm scrap order")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to confirm scrap order")
		return
	}

	response.Success(c, gin.H{"message": "Scrap order confirmed and inventory reduced"})
}

// CancelScrapOrder cancels a scrap order
func (h *Handler) CancelScrapOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid scrap order ID")
		return
	}

	result, err := h.db.Exec(`
		UPDATE scrap_orders SET status = 'cancelled', updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND status IN ('draft', 'pending_approval') AND deleted_at IS NULL
	`, time.Now(), orderID, tenantID)

	if err != nil {
		h.log.Error("Failed to cancel scrap order", "error", err)
		response.InternalError(c, "Failed to cancel scrap order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.BadRequest(c, "Scrap order cannot be cancelled")
		return
	}

	response.Success(c, gin.H{"message": "Scrap order cancelled"})
}

// GetScrapSummary returns scrap statistics
func (h *Handler) GetScrapSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	dateFrom := c.DefaultQuery("date_from", time.Now().AddDate(0, -1, 0).Format("2006-01-02"))
	dateTo := c.DefaultQuery("date_to", time.Now().Format("2006-01-02"))

	var summary entity.ScrapSummary

	// Get total counts
	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(quantity), 0), COALESCE(SUM(total_cost), 0)
		FROM scrap_orders
		WHERE tenant_id = $1 AND scrap_date >= $2 AND scrap_date <= $3 AND deleted_at IS NULL AND status = 'completed'
	`, tenantID, dateFrom, dateTo).Scan(&summary.TotalScrapOrders, &summary.TotalQuantity, &summary.TotalValue)

	// Get pending approval count
	h.db.QueryRow(`
		SELECT COUNT(*) FROM scrap_orders
		WHERE tenant_id = $1 AND status = 'pending_approval' AND deleted_at IS NULL
	`, tenantID).Scan(&summary.PendingApproval)

	// Get breakdown by reason
	rows, err := h.db.Query(`
		SELECT COALESCE(reason, 'other'), SUM(total_cost)
		FROM scrap_orders
		WHERE tenant_id = $1 AND scrap_date >= $2 AND scrap_date <= $3 AND deleted_at IS NULL AND status = 'completed'
		GROUP BY reason
	`, tenantID, dateFrom, dateTo)

	if err == nil {
		summary.ByReason = make(map[string]float64)
		defer rows.Close()
		for rows.Next() {
			var reason string
			var value float64
			if rows.Scan(&reason, &value) == nil {
				summary.ByReason[reason] = value
			}
		}
	}

	response.Success(c, summary)
}

// =====================================================
// REORDER RULES HANDLERS
// =====================================================

// ListReorderRules returns a paginated list of reorder rules
func (h *Handler) ListReorderRules(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	productID := c.Query("product_id")
	warehouseID := c.Query("warehouse_id")
	isActive := c.Query("is_active")

	baseQuery := `
		SELECT r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty,
			   r.trigger_type, r.preferred_vendor_id, r.lead_time_days, r.safety_stock,
			   r.auto_create_po, r.is_active, r.created_at,
			   p.code as product_code, p.name as product_name,
			   w.name as warehouse_name,
			   ct.name as vendor_name,
			   COALESCE(SUM(i.quantity_available), 0) as current_stock
		FROM reorder_rules r
		JOIN products p ON r.product_id = p.id
		LEFT JOIN warehouses w ON r.warehouse_id = w.id
		LEFT JOIN contacts ct ON r.preferred_vendor_id = ct.id
		LEFT JOIN inventory i ON r.product_id = i.product_id AND (r.warehouse_id IS NULL OR r.warehouse_id = i.warehouse_id)
		WHERE r.tenant_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM reorder_rules r WHERE r.tenant_id = $1`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND r.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND r.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if productID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND r.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND r.product_id = $%d", argCount)
		args = append(args, productID)
	}

	if warehouseID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND r.warehouse_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND r.warehouse_id = $%d", argCount)
		args = append(args, warehouseID)
	}

	if isActive != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND r.is_active = $%d", argCount)
		countQuery += fmt.Sprintf(" AND r.is_active = $%d", argCount)
		args = append(args, isActive == "true")
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count reorder rules", "error", err)
		response.InternalError(c, "Failed to list reorder rules")
		return
	}

	baseQuery += " GROUP BY r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty, r.trigger_type, r.preferred_vendor_id, r.lead_time_days, r.safety_stock, r.auto_create_po, r.is_active, r.created_at, p.code, p.name, w.name, ct.name"
	baseQuery += " ORDER BY p.code ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list reorder rules", "error", err)
		response.InternalError(c, "Failed to list reorder rules")
		return
	}
	defer rows.Close()

	rules := make([]*entity.ReorderRuleResponse, 0)
	for rows.Next() {
		var r entity.ReorderRuleResponse
		var warehouseID, vendorID sql.NullString
		var maxQty sql.NullFloat64
		var warehouseName, vendorName sql.NullString

		err := rows.Scan(
			&r.ID, &r.ProductID, &warehouseID, &r.MinQty, &maxQty, &r.ReorderQty,
			&r.TriggerType, &vendorID, &r.LeadTimeDays, &r.SafetyStock,
			&r.AutoCreatePO, &r.IsActive, &r.CreatedAt,
			&r.ProductCode, &r.ProductName,
			&warehouseName, &vendorName, &r.CurrentStock,
		)
		if err != nil {
			h.log.Error("Failed to scan reorder rule", "error", err)
			continue
		}

		if warehouseID.Valid {
			wid, _ := uuid.Parse(warehouseID.String)
			r.WarehouseID = &wid
		}
		if vendorID.Valid {
			vid, _ := uuid.Parse(vendorID.String)
			r.PreferredVendorID = &vid
		}
		if maxQty.Valid {
			r.MaxQty = &maxQty.Float64
		}
		if warehouseName.Valid {
			r.WarehouseName = warehouseName.String
		}
		if vendorName.Valid {
			r.PreferredVendorName = vendorName.String
		}

		r.NeedsReorder = r.CurrentStock <= r.MinQty

		rules = append(rules, &r)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, rules, pagination)
}

// GetReorderRule returns a single reorder rule
func (h *Handler) GetReorderRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	ruleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid reorder rule ID")
		return
	}

	var r entity.ReorderRuleResponse
	var warehouseID, vendorID sql.NullString
	var maxQty sql.NullFloat64
	var warehouseName, vendorName sql.NullString

	err = h.db.QueryRow(`
		SELECT r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty,
			   r.trigger_type, r.preferred_vendor_id, r.lead_time_days, r.safety_stock,
			   r.auto_create_po, r.is_active, r.created_at,
			   p.code as product_code, p.name as product_name,
			   w.name as warehouse_name,
			   ct.name as vendor_name
		FROM reorder_rules r
		JOIN products p ON r.product_id = p.id
		LEFT JOIN warehouses w ON r.warehouse_id = w.id
		LEFT JOIN contacts ct ON r.preferred_vendor_id = ct.id
		WHERE r.id = $1 AND r.tenant_id = $2
	`, ruleID, tenantID).Scan(
		&r.ID, &r.ProductID, &warehouseID, &r.MinQty, &maxQty, &r.ReorderQty,
		&r.TriggerType, &vendorID, &r.LeadTimeDays, &r.SafetyStock,
		&r.AutoCreatePO, &r.IsActive, &r.CreatedAt,
		&r.ProductCode, &r.ProductName,
		&warehouseName, &vendorName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Reorder rule")
		return
	}
	if err != nil {
		h.log.Error("Failed to get reorder rule", "error", err)
		response.InternalError(c, "Failed to get reorder rule")
		return
	}

	if warehouseID.Valid {
		wid, _ := uuid.Parse(warehouseID.String)
		r.WarehouseID = &wid
	}
	if vendorID.Valid {
		vid, _ := uuid.Parse(vendorID.String)
		r.PreferredVendorID = &vid
	}
	if maxQty.Valid {
		r.MaxQty = &maxQty.Float64
	}
	if warehouseName.Valid {
		r.WarehouseName = warehouseName.String
	}
	if vendorName.Valid {
		r.PreferredVendorName = vendorName.String
	}

	response.Success(c, r)
}

// CreateReorderRule creates a new reorder rule
func (h *Handler) CreateReorderRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateReorderRuleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	var warehouseID *uuid.UUID
	if input.WarehouseID != "" {
		wid, err := uuid.Parse(input.WarehouseID)
		if err == nil {
			warehouseID = &wid
		}
	}

	// Check for existing rule
	var exists bool
	h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM reorder_rules WHERE product_id = $1 AND tenant_id = $2 AND COALESCE(warehouse_id::text, '') = COALESCE($3::text, ''))
	`, productID, tenantID, warehouseID).Scan(&exists)
	if exists {
		response.Conflict(c, "Reorder rule already exists for this product/warehouse combination")
		return
	}

	id := uuid.New()
	now := time.Now()

	triggerType := "min_qty"
	if input.TriggerType != "" {
		triggerType = input.TriggerType
	}

	var vendorID *uuid.UUID
	if input.PreferredVendorID != "" {
		vid, _ := uuid.Parse(input.PreferredVendorID)
		vendorID = &vid
	}

	var maxQty *float64
	if input.MaxQty > 0 {
		maxQty = &input.MaxQty
	}

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	_, err = h.db.Exec(`
		INSERT INTO reorder_rules (
			id, tenant_id, organization_id, product_id, warehouse_id, min_qty, max_qty, reorder_qty,
			trigger_type, preferred_vendor_id, lead_time_days, safety_stock,
			auto_create_po, is_active, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, true, $14, $15, $16, $16)
	`, id, tenantID, orgIDPtr, productID, warehouseID, input.MinQty, maxQty, input.ReorderQty,
		triggerType, vendorID, input.LeadTimeDays, input.SafetyStock,
		input.AutoCreatePO, notes, userID, now)

	if err != nil {
		h.log.Error("Failed to create reorder rule", "error", err)
		response.InternalError(c, "Failed to create reorder rule")
		return
	}

	response.Created(c, gin.H{"id": id, "message": "Reorder rule created successfully"})
}

// UpdateReorderRule updates a reorder rule
func (h *Handler) UpdateReorderRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	ruleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid reorder rule ID")
		return
	}

	var input entity.UpdateReorderRuleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.MinQty != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("min_qty = $%d", argCount))
		args = append(args, *input.MinQty)
	}
	if input.MaxQty != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("max_qty = $%d", argCount))
		args = append(args, *input.MaxQty)
	}
	if input.ReorderQty != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("reorder_qty = $%d", argCount))
		args = append(args, *input.ReorderQty)
	}
	if input.TriggerType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("trigger_type = $%d", argCount))
		args = append(args, *input.TriggerType)
	}
	if input.LeadTimeDays != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("lead_time_days = $%d", argCount))
		args = append(args, *input.LeadTimeDays)
	}
	if input.SafetyStock != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("safety_stock = $%d", argCount))
		args = append(args, *input.SafetyStock)
	}
	if input.AutoCreatePO != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("auto_create_po = $%d", argCount))
		args = append(args, *input.AutoCreatePO)
	}
	if input.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, ruleID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE reorder_rules SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update reorder rule", "error", err)
		response.InternalError(c, "Failed to update reorder rule")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Reorder rule")
		return
	}

	response.Success(c, gin.H{"message": "Reorder rule updated successfully"})
}

// DeleteReorderRule deletes a reorder rule
func (h *Handler) DeleteReorderRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	ruleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid reorder rule ID")
		return
	}

	result, err := h.db.Exec("DELETE FROM reorder_rules WHERE id = $1 AND tenant_id = $2", ruleID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete reorder rule", "error", err)
		response.InternalError(c, "Failed to delete reorder rule")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Reorder rule")
		return
	}

	response.Success(c, gin.H{"message": "Reorder rule deleted successfully"})
}

// GetReorderAlerts returns products that need reordering based on rules
func (h *Handler) GetReorderAlerts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	alertQuery := `
		SELECT r.id, r.product_id, r.warehouse_id, r.min_qty, r.reorder_qty,
			   r.preferred_vendor_id, r.lead_time_days,
			   p.code as product_code, p.name as product_name,
			   w.name as warehouse_name,
			   ct.name as vendor_name,
			   COALESCE(SUM(i.quantity_available), 0) as current_stock
		FROM reorder_rules r
		JOIN products p ON r.product_id = p.id
		LEFT JOIN warehouses w ON r.warehouse_id = w.id
		LEFT JOIN contacts ct ON r.preferred_vendor_id = ct.id
		LEFT JOIN inventory i ON r.product_id = i.product_id AND (r.warehouse_id IS NULL OR r.warehouse_id = i.warehouse_id)
		WHERE r.tenant_id = $1 AND r.is_active = true
	`
	alertArgs := []interface{}{tenantID}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		alertQuery += " AND r.organization_id = $2"
		alertArgs = append(alertArgs, orgID)
	}
	alertQuery += `
		GROUP BY r.id, r.product_id, r.warehouse_id, r.min_qty, r.reorder_qty,
				 r.preferred_vendor_id, r.lead_time_days, p.code, p.name, w.name, ct.name
		HAVING COALESCE(SUM(i.quantity_available), 0) <= r.min_qty
		ORDER BY COALESCE(SUM(i.quantity_available), 0) / NULLIF(r.min_qty, 0) ASC
	`
	rows, err := h.db.Query(alertQuery, alertArgs...)

	if err != nil {
		h.log.Error("Failed to get reorder alerts", "error", err)
		response.InternalError(c, "Failed to get reorder alerts")
		return
	}
	defer rows.Close()

	type ReorderAlert struct {
		RuleID           uuid.UUID  `json:"rule_id"`
		ProductID        uuid.UUID  `json:"product_id"`
		ProductCode      string     `json:"product_code"`
		ProductName      string     `json:"product_name"`
		WarehouseID      *uuid.UUID `json:"warehouse_id,omitempty"`
		WarehouseName    string     `json:"warehouse_name,omitempty"`
		CurrentStock     float64    `json:"current_stock"`
		MinQty           float64    `json:"min_qty"`
		ReorderQty       float64    `json:"reorder_qty"`
		SuggestedOrderQty float64   `json:"suggested_order_qty"`
		VendorID         *uuid.UUID `json:"vendor_id,omitempty"`
		VendorName       string     `json:"vendor_name,omitempty"`
		LeadTimeDays     int        `json:"lead_time_days"`
	}

	alerts := make([]*ReorderAlert, 0)
	for rows.Next() {
		var a ReorderAlert
		var warehouseID, vendorID sql.NullString
		var warehouseName, vendorName sql.NullString

		err := rows.Scan(
			&a.RuleID, &a.ProductID, &warehouseID, &a.MinQty, &a.ReorderQty,
			&vendorID, &a.LeadTimeDays,
			&a.ProductCode, &a.ProductName, &warehouseName, &vendorName, &a.CurrentStock,
		)
		if err != nil {
			continue
		}

		if warehouseID.Valid {
			wid, _ := uuid.Parse(warehouseID.String)
			a.WarehouseID = &wid
		}
		if vendorID.Valid {
			vid, _ := uuid.Parse(vendorID.String)
			a.VendorID = &vid
		}
		if warehouseName.Valid {
			a.WarehouseName = warehouseName.String
		}
		if vendorName.Valid {
			a.VendorName = vendorName.String
		}

		// Calculate suggested order quantity
		a.SuggestedOrderQty = a.ReorderQty
		if a.CurrentStock < 0 {
			a.SuggestedOrderQty += (-a.CurrentStock)
		}

		alerts = append(alerts, &a)
	}

	response.Success(c, alerts)
}

// RunReplenishment creates purchase orders for products that need replenishment
func (h *Handler) RunReplenishment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Parse input for optional filters
	var input struct {
		ProductIDs   []string `json:"product_ids"`
		WarehouseID  string   `json:"warehouse_id"`
		VendorID     string   `json:"vendor_id"`
		RuleIDs      []string `json:"rule_ids"`
	}
	c.ShouldBindJSON(&input)

	// Get all products that need replenishment
	query := `
		SELECT r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty,
			   r.preferred_vendor_id, r.lead_time_days, r.auto_create_po,
			   p.code as product_code, p.name as product_name,
			   COALESCE(SUM(i.quantity_available), 0) as current_stock
		FROM reorder_rules r
		JOIN products p ON r.product_id = p.id
		LEFT JOIN inventory i ON r.product_id = i.product_id AND (r.warehouse_id IS NULL OR r.warehouse_id = i.warehouse_id)
		WHERE r.tenant_id = $1 AND r.is_active = true
	`
	args := []interface{}{tenantID}
	argCount := 1

	// Apply filters
	if len(input.RuleIDs) > 0 {
		argCount++
		placeholders := make([]string, len(input.RuleIDs))
		for i := range input.RuleIDs {
			placeholders[i] = fmt.Sprintf("$%d", argCount+i)
		}
		query += fmt.Sprintf(" AND r.id IN (%s)", strings.Join(placeholders, ","))
		for _, id := range input.RuleIDs {
			args = append(args, id)
			argCount++
		}
		argCount-- // adjust for the loop
	}

	if len(input.ProductIDs) > 0 {
		argCount++
		placeholders := make([]string, len(input.ProductIDs))
		for i := range input.ProductIDs {
			placeholders[i] = fmt.Sprintf("$%d", argCount+i)
		}
		query += fmt.Sprintf(" AND r.product_id IN (%s)", strings.Join(placeholders, ","))
		for _, id := range input.ProductIDs {
			args = append(args, id)
			argCount++
		}
		argCount--
	}

	if input.WarehouseID != "" {
		argCount++
		query += fmt.Sprintf(" AND r.warehouse_id = $%d", argCount)
		args = append(args, input.WarehouseID)
	}

	if input.VendorID != "" {
		argCount++
		query += fmt.Sprintf(" AND r.preferred_vendor_id = $%d", argCount)
		args = append(args, input.VendorID)
	}

	query += `
		GROUP BY r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty,
				 r.preferred_vendor_id, r.lead_time_days, r.auto_create_po, p.code, p.name
		HAVING COALESCE(SUM(i.quantity_available), 0) <= r.min_qty
		ORDER BY r.preferred_vendor_id NULLS LAST, p.name
	`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to get products needing replenishment", "error", err)
		response.InternalError(c, "Failed to get products needing replenishment")
		return
	}
	defer rows.Close()

	type ReplenishmentItem struct {
		RuleID          uuid.UUID
		ProductID       uuid.UUID
		ProductCode     string
		ProductName     string
		WarehouseID     *uuid.UUID
		MinQty          float64
		MaxQty          float64
		ReorderQty      float64
		CurrentStock    float64
		VendorID        *uuid.UUID
		LeadTimeDays    int
		AutoCreatePO    bool
		OrderQty        float64
	}

	items := make([]*ReplenishmentItem, 0)
	for rows.Next() {
		var item ReplenishmentItem
		var warehouseID, vendorID sql.NullString
		var maxQty sql.NullFloat64

		err := rows.Scan(
			&item.RuleID, &item.ProductID, &warehouseID, &item.MinQty, &maxQty, &item.ReorderQty,
			&vendorID, &item.LeadTimeDays, &item.AutoCreatePO,
			&item.ProductCode, &item.ProductName, &item.CurrentStock,
		)
		if err != nil {
			h.log.Error("Failed to scan replenishment item", "error", err)
			continue
		}

		if warehouseID.Valid {
			wid, _ := uuid.Parse(warehouseID.String)
			item.WarehouseID = &wid
		}
		if vendorID.Valid {
			vid, _ := uuid.Parse(vendorID.String)
			item.VendorID = &vid
		}
		if maxQty.Valid {
			item.MaxQty = maxQty.Float64
		}

		// Calculate order quantity: if max_qty is set, order up to max; otherwise use reorder_qty
		if item.MaxQty > 0 {
			item.OrderQty = item.MaxQty - item.CurrentStock
		} else {
			item.OrderQty = item.ReorderQty
		}

		// Ensure minimum order qty
		if item.OrderQty < item.ReorderQty {
			item.OrderQty = item.ReorderQty
		}

		items = append(items, &item)
	}

	if len(items) == 0 {
		response.Success(c, gin.H{
			"message": "No products need replenishment",
			"orders_created": 0,
		})
		return
	}

	// Group items by vendor
	vendorItems := make(map[string][]*ReplenishmentItem)
	noVendorItems := make([]*ReplenishmentItem, 0)

	for _, item := range items {
		if item.VendorID != nil {
			vendorID := item.VendorID.String()
			vendorItems[vendorID] = append(vendorItems[vendorID], item)
		} else {
			noVendorItems = append(noVendorItems, item)
		}
	}

	// Create purchase orders for each vendor
	ordersCreated := 0
	orderIDs := make([]uuid.UUID, 0)
	skippedItems := make([]map[string]interface{}, 0)

	for vendorID, items := range vendorItems {
		vid, _ := uuid.Parse(vendorID)

		// Generate order number
		var orderNumber string
		err := h.db.QueryRow(`
			SELECT 'PO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-' || LPAD((COALESCE(MAX(CAST(SUBSTRING(order_number FROM 'PO-[0-9]+-([0-9]+)') AS INTEGER)), 0) + 1)::TEXT, 4, '0')
			FROM purchase_orders WHERE tenant_id = $1 AND order_number LIKE 'PO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-%'
		`, tenantID).Scan(&orderNumber)
		if err != nil {
			orderNumber = fmt.Sprintf("PO-%s-%04d", time.Now().Format("20060102"), ordersCreated+1)
		}

		// Create purchase order
		poID := uuid.New()
		_, err = h.db.Exec(`
			INSERT INTO purchase_orders (
				id, tenant_id, order_number, vendor_id, order_date, status, payment_status,
				subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
				exchange_rate, requested_by, notes, is_auto_replenishment, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		`, poID, tenantID, orderNumber, vid, time.Now(), "draft", "unpaid",
			0, 0, 0, 0, 0, 1.0, userID, "Auto-generated by replenishment", true, time.Now(), time.Now())

		if err != nil {
			h.log.Error("Failed to create purchase order", "error", err, "vendor_id", vendorID)
			for _, item := range items {
				skippedItems = append(skippedItems, map[string]interface{}{
					"product_id":   item.ProductID,
					"product_name": item.ProductName,
					"reason":       "Failed to create purchase order",
				})
			}
			continue
		}

		// Create purchase order lines
		var subtotal float64
		lineNumber := 1
		for _, item := range items {
			// Get product price from vendor (if available)
			var unitPrice float64
			err := h.db.QueryRow(`
				SELECT price FROM vendor_prices
				WHERE vendor_id = $1 AND product_id = $2 AND tenant_id = $3
				ORDER BY created_at DESC LIMIT 1
			`, vid, item.ProductID, tenantID).Scan(&unitPrice)
			if err != nil {
				// Try to get the product's purchase price
				h.db.QueryRow(`SELECT COALESCE(purchase_price, 0) FROM products WHERE id = $1`, item.ProductID).Scan(&unitPrice)
			}

			lineTotal := item.OrderQty * unitPrice

			_, err = h.db.Exec(`
				INSERT INTO purchase_order_lines (
					id, purchase_order_id, line_number, product_id, description, quantity,
					unit_price, discount_amount, tax_amount, line_total, warehouse_id,
					quantity_received, quantity_invoiced, reorder_rule_id, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			`, uuid.New(), poID, lineNumber, item.ProductID, item.ProductName, item.OrderQty,
				unitPrice, 0, 0, lineTotal, item.WarehouseID, 0, 0, item.RuleID, time.Now(), time.Now())

			if err != nil {
				h.log.Error("Failed to create purchase order line", "error", err)
				continue
			}

			subtotal += lineTotal
			lineNumber++
		}

		// Update purchase order totals
		h.db.Exec(`
			UPDATE purchase_orders SET subtotal = $1, total_amount = $1, updated_at = $2
			WHERE id = $3
		`, subtotal, time.Now(), poID)

		ordersCreated++
		orderIDs = append(orderIDs, poID)
	}

	// Report items without vendors
	for _, item := range noVendorItems {
		skippedItems = append(skippedItems, map[string]interface{}{
			"product_id":   item.ProductID,
			"product_name": item.ProductName,
			"reason":       "No preferred vendor set in reorder rule",
		})
	}

	response.Success(c, gin.H{
		"message":        fmt.Sprintf("Created %d purchase orders", ordersCreated),
		"orders_created": ordersCreated,
		"order_ids":      orderIDs,
		"skipped_items":  skippedItems,
		"total_items":    len(items),
	})
}

// GetReplenishmentPreview returns a preview of what replenishment would create without actually creating orders
func (h *Handler) GetReplenishmentPreview(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Get all products that need replenishment with vendor info
	rows, err := h.db.Query(`
		SELECT r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty,
			   r.preferred_vendor_id, r.lead_time_days, r.auto_create_po, r.safety_stock,
			   p.code as product_code, p.name as product_name, p.sku,
			   w.name as warehouse_name,
			   ct.name as vendor_name,
			   COALESCE(SUM(i.quantity_available), 0) as current_stock,
			   COALESCE(
				   (SELECT price FROM vendor_prices vp WHERE vp.vendor_id = r.preferred_vendor_id AND vp.product_id = r.product_id AND vp.tenant_id = $1 ORDER BY vp.created_at DESC LIMIT 1),
				   p.purchase_price
			   ) as unit_price
		FROM reorder_rules r
		JOIN products p ON r.product_id = p.id
		LEFT JOIN warehouses w ON r.warehouse_id = w.id
		LEFT JOIN contacts ct ON r.preferred_vendor_id = ct.id
		LEFT JOIN inventory i ON r.product_id = i.product_id AND (r.warehouse_id IS NULL OR r.warehouse_id = i.warehouse_id)
		WHERE r.tenant_id = $1 AND r.is_active = true
		GROUP BY r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty,
				 r.preferred_vendor_id, r.lead_time_days, r.auto_create_po, r.safety_stock,
				 p.code, p.name, p.sku, p.purchase_price, w.name, ct.name
		HAVING COALESCE(SUM(i.quantity_available), 0) <= r.min_qty
		ORDER BY ct.name NULLS LAST, p.name
	`, tenantID)

	if err != nil {
		h.log.Error("Failed to get replenishment preview", "error", err)
		response.InternalError(c, "Failed to get replenishment preview")
		return
	}
	defer rows.Close()

	type ReplenishmentPreviewItem struct {
		RuleID         uuid.UUID  `json:"rule_id"`
		ProductID      uuid.UUID  `json:"product_id"`
		ProductCode    string     `json:"product_code"`
		ProductName    string     `json:"product_name"`
		SKU            string     `json:"sku,omitempty"`
		WarehouseID    *uuid.UUID `json:"warehouse_id,omitempty"`
		WarehouseName  string     `json:"warehouse_name,omitempty"`
		VendorID       *uuid.UUID `json:"vendor_id,omitempty"`
		VendorName     string     `json:"vendor_name,omitempty"`
		CurrentStock   float64    `json:"current_stock"`
		MinQty         float64    `json:"min_qty"`
		MaxQty         float64    `json:"max_qty"`
		ReorderQty     float64    `json:"reorder_qty"`
		SafetyStock    float64    `json:"safety_stock"`
		SuggestedQty   float64    `json:"suggested_qty"`
		UnitPrice      float64    `json:"unit_price"`
		EstimatedCost  float64    `json:"estimated_cost"`
		LeadTimeDays   int        `json:"lead_time_days"`
		Status         string     `json:"status"` // "critical", "low", "reorder"
	}

	items := make([]*ReplenishmentPreviewItem, 0)
	vendorTotals := make(map[string]float64)
	vendorNames := make(map[string]string)

	for rows.Next() {
		var item ReplenishmentPreviewItem
		var warehouseID, vendorID sql.NullString
		var warehouseName, vendorName, sku sql.NullString
		var maxQty, safetyStock, unitPrice sql.NullFloat64

		err := rows.Scan(
			&item.RuleID, &item.ProductID, &warehouseID, &item.MinQty, &maxQty, &item.ReorderQty,
			&vendorID, &item.LeadTimeDays, &item.RuleID, &safetyStock,
			&item.ProductCode, &item.ProductName, &sku,
			&warehouseName, &vendorName, &item.CurrentStock, &unitPrice,
		)
		if err != nil {
			h.log.Error("Failed to scan replenishment preview item", "error", err)
			continue
		}

		if warehouseID.Valid {
			wid, _ := uuid.Parse(warehouseID.String)
			item.WarehouseID = &wid
		}
		if vendorID.Valid {
			vid, _ := uuid.Parse(vendorID.String)
			item.VendorID = &vid
		}
		if warehouseName.Valid {
			item.WarehouseName = warehouseName.String
		}
		if vendorName.Valid {
			item.VendorName = vendorName.String
		}
		if sku.Valid {
			item.SKU = sku.String
		}
		if maxQty.Valid {
			item.MaxQty = maxQty.Float64
		}
		if safetyStock.Valid {
			item.SafetyStock = safetyStock.Float64
		}
		if unitPrice.Valid {
			item.UnitPrice = unitPrice.Float64
		}

		// Calculate suggested order quantity
		if item.MaxQty > 0 {
			item.SuggestedQty = item.MaxQty - item.CurrentStock
		} else {
			item.SuggestedQty = item.ReorderQty
		}
		if item.SuggestedQty < item.ReorderQty {
			item.SuggestedQty = item.ReorderQty
		}

		item.EstimatedCost = item.SuggestedQty * item.UnitPrice

		// Determine status
		if item.CurrentStock <= 0 {
			item.Status = "critical"
		} else if item.CurrentStock <= item.SafetyStock {
			item.Status = "low"
		} else {
			item.Status = "reorder"
		}

		// Track vendor totals
		if item.VendorID != nil {
			vendorTotals[item.VendorID.String()] += item.EstimatedCost
			vendorNames[item.VendorID.String()] = item.VendorName
		}

		items = append(items, &item)
	}

	// Build vendor summary
	type VendorSummary struct {
		VendorID   uuid.UUID `json:"vendor_id"`
		VendorName string    `json:"vendor_name"`
		ItemCount  int       `json:"item_count"`
		TotalCost  float64   `json:"total_cost"`
	}

	vendorSummaries := make([]*VendorSummary, 0)
	vendorItemCounts := make(map[string]int)
	noVendorCount := 0

	for _, item := range items {
		if item.VendorID != nil {
			vendorItemCounts[item.VendorID.String()]++
		} else {
			noVendorCount++
		}
	}

	for vidStr, total := range vendorTotals {
		vid, _ := uuid.Parse(vidStr)
		vendorSummaries = append(vendorSummaries, &VendorSummary{
			VendorID:   vid,
			VendorName: vendorNames[vidStr],
			ItemCount:  vendorItemCounts[vidStr],
			TotalCost:  total,
		})
	}

	// Calculate totals
	var totalCost float64
	criticalCount := 0
	lowCount := 0
	reorderCount := 0

	for _, item := range items {
		totalCost += item.EstimatedCost
		switch item.Status {
		case "critical":
			criticalCount++
		case "low":
			lowCount++
		case "reorder":
			reorderCount++
		}
	}

	response.Success(c, gin.H{
		"items":            items,
		"total_items":      len(items),
		"critical_count":   criticalCount,
		"low_count":        lowCount,
		"reorder_count":    reorderCount,
		"no_vendor_count":  noVendorCount,
		"total_cost":       totalCost,
		"vendor_summaries": vendorSummaries,
	})
}
