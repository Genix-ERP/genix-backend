package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
// ListInventory godoc
// @Summary List inventory levels
// @Description Get a paginated list of inventory levels across all warehouses
// @Tags Inventory
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Param product_id query string false "Filter by product ID"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory [get]
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
	if limit < 1 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
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
	// The JOIN to `products` filters out rows belonging to soft-deleted
	// products. Without this, ghost inventory rows for deleted products are
	// still returned by the API, which then drags warehouse totals down in
	// the UI (the products page filters by deleted_at IS NULL, so the
	// orphans are invisible in the list but still contribute to aggregate
	// stats). Same filter is applied at line 5343 of this file for the
	// per-warehouse stock value query, so this matches existing precedent.
	baseQuery := `
		SELECT i.id, i.tenant_id, i.product_id, i.warehouse_id, i.location_id,
			   i.lot_number, i.serial_number, i.expiry_date,
			   i.quantity_on_hand, i.quantity_reserved, i.quantity_available,
			   i.unit_cost, i.total_value, i.last_count_date, i.last_movement_date,
			   i.created_at, i.updated_at,
			   p.code as product_code, p.name as product_name, p.min_stock_level, p.reorder_point,
			   w.code as warehouse_code, w.name as warehouse_name,
			   COALESCE(w.warehouse_type, 'regular') as warehouse_type,
			   wl.code as location_code, wl.name as location_name
		FROM inventory i
		JOIN products p ON i.product_id = p.id AND p.deleted_at IS NULL
		JOIN warehouses w ON i.warehouse_id = w.id
		LEFT JOIN warehouse_locations wl ON i.location_id = wl.id
		WHERE i.tenant_id = $1
	`
	countQuery := `
		SELECT COUNT(*) FROM inventory i
		JOIN products p ON i.product_id = p.id AND p.deleted_at IS NULL
		JOIN warehouses w ON i.warehouse_id = w.id
		WHERE i.tenant_id = $1
	`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization (use warehouse's org since inventory.organization_id may be NULL)
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND w.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND w.organization_id = $%d", argCount)
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
		WarehouseType     string     `json:"warehouse_type"`
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
		var warehouseCode, warehouseName, warehouseType string
		var locationCode, locationName sql.NullString

		err := rows.Scan(
			&i.ID, &i.TenantID, &i.ProductID, &i.WarehouseID, &locationID,
			&lotNumber, &serialNumber, &expiryDate,
			&i.QuantityOnHand, &i.QuantityReserved, &i.QuantityAvailable,
			&i.UnitCost, &i.TotalValue, &lastCountDate, &lastMovementDate,
			&i.CreatedAt, &i.UpdatedAt,
			&productCode, &productName, &minStockLevel, &reorderPoint,
			&warehouseCode, &warehouseName, &warehouseType,
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
			WarehouseType:     warehouseType,
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
// GetInventorySummary godoc
// @Summary Get inventory summary by product
// @Description Get a summarized view of inventory grouped by product across all warehouses
// @Tags Inventory
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param search query string false "Search by product code or name"
// @Param category_id query string false "Filter by category ID"
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Param low_stock query boolean false "Filter low stock items"
// @Param out_of_stock query boolean false "Filter out of stock items"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/summary [get]
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
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
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
			AND NOT EXISTS (SELECT 1 FROM warehouses ws WHERE ws.id = i.warehouse_id AND ws.warehouse_type = 'scrap')
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
	`
	countQuery := `
		SELECT COUNT(DISTINCT p.id)
		FROM products p
		LEFT JOIN inventory i ON p.id = i.product_id
			AND NOT EXISTS (SELECT 1 FROM warehouses ws WHERE ws.id = i.warehouse_id AND ws.warehouse_type = 'scrap')
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

	// ONE accumulated HAVING. These used to be two independent `+= " HAVING …"`
	// appends, so sending low_stock=true&out_of_stock=true emitted
	// "… HAVING … HAVING …" — a Postgres syntax error, i.e. a 500 on a
	// combination the UI can produce.
	havingParts := []string{}
	if lowStock {
		havingParts = append(havingParts,
			"COALESCE(SUM(i.quantity_available), 0) <= p.reorder_point AND COALESCE(SUM(i.quantity_available), 0) > 0")
	}
	if outOfStock {
		havingParts = append(havingParts, "COALESCE(SUM(i.quantity_on_hand), 0) <= 0")
	}
	having := ""
	if len(havingParts) > 0 {
		having = " HAVING " + strings.Join(havingParts, " AND ")
		baseQuery += having
	}

	// The count must see the HAVING too, or total/total_pages/has_next report
	// the unfiltered product count whenever a stock filter is on. A HAVING can't
	// be bolted onto a plain COUNT(*), so wrap the grouped query.
	var total int
	var err error
	if having != "" {
		wrapped := `SELECT COUNT(*) FROM (` + baseQuery + `) t`
		err = h.db.QueryRow(wrapped, args...).Scan(&total)
	} else {
		err = h.db.QueryRow(countQuery, args...).Scan(&total)
	}
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
// AdjustInventory godoc
// @Summary Create inventory adjustment
// @Description Create an inventory adjustment to manually modify inventory quantities
// @Tags Inventory
// @Accept json
// @Produce json
// @Param adjustment body entity.InventoryAdjustmentInput true "Adjustment details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/adjustments [post]
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// If variant_id is provided, verify it belongs to this product and get variant cost
	var variantCostPrice float64
	if variantID != nil {
		err = h.db.QueryRow(
			"SELECT COALESCE(cost_price, 0) FROM product_variants WHERE id = $1 AND product_id = $2 AND deleted_at IS NULL",
			variantID, productID,
		).Scan(&variantCostPrice)
		if err == sql.ErrNoRows {
			response.BadRequest(c, "Invalid variant ID for this product")
			return
		}
	}

	// Use provided unit_cost → variant cost_price → product cost_price
	unitCost := input.UnitCost
	if unitCost == 0 && variantCostPrice > 0 {
		unitCost = variantCostPrice
	}
	if unitCost == 0 {
		unitCost = productCostPrice
	}

	// Verify warehouse belongs to tenant and get its organization_id
	var warehouseOrgID uuid.UUID
	err = h.db.QueryRow(
		"SELECT COALESCE(organization_id, '00000000-0000-0000-0000-000000000000') FROM warehouses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		warehouseID, tenantID,
	).Scan(&warehouseOrgID)
	if err != nil {
		response.NotFound(c, "Warehouse")
		return
	}

	// If organizationID from middleware is nil, derive it from the warehouse
	if organizationID == uuid.Nil && warehouseOrgID != uuid.Nil {
		organizationID = warehouseOrgID
	}

	// Validate warehouse belongs to the same organization if both are set
	if organizationID != uuid.Nil && warehouseOrgID != uuid.Nil && organizationID != warehouseOrgID {
		response.BadRequest(c, "Warehouse does not belong to the selected organization")
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
			id, tenant_id, organization_id, inventory_id, product_id, warehouse_id,
			transaction_type, quantity,
			unit_cost, total_cost, reason, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $13)
	`, transactionID, tenantID, organizationID, inventoryID, productID, warehouseID,
		transactionType, input.Quantity,
		unitCost, input.Quantity*unitCost, reason, notes, now, userID)

	if err != nil {
		h.log.Error("Failed to create inventory transaction", "error", err)
		response.InternalError(c, "Failed to adjust inventory")
		return
	}

	// ===== Create Journal Entry for Inventory Adjustment (Odoo-style) =====
	totalCost := math.Abs(input.Quantity) * unitCost
	if totalCost > 0 {
		var orgIDPtr *uuid.UUID
		if organizationID != uuid.Nil {
			orgIDPtr = &organizationID
		}
		ca := getCategoryAccounts(tx, tenantID, orgIDPtr, productID)
		adjustAcct := findAccount(tx, tenantID, orgIDPtr, "stock adjustment", "6910")
		if adjustAcct == uuid.Nil {
			adjustAcct = findAccount(tx, tenantID, orgIDPtr, "inventory adjustment", "6910")
		}
		if adjustAcct == uuid.Nil {
			adjustAcct = findAccount(tx, tenantID, orgIDPtr, "miscellaneous expense", "9410")
		}

		if ca.StockValuationAccountID != uuid.Nil && adjustAcct != uuid.Nil {
			// Find journal (STOCK preferred, fallback to MISC/GENERAL)
			var journalID uuid.UUID
			var nextNumber int
			tx.QueryRow(`SELECT id, next_number FROM journals WHERE tenant_id = $1 AND code IN ('STOCK','MISC','GENERAL') AND deleted_at IS NULL ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)

			if journalID != uuid.Nil {
				entryID := uuid.New()
				entryNumber := fmt.Sprintf("ADJ%06d", nextNumber)
				description := fmt.Sprintf("Inventory Adjustment - %s", input.Reason)
				if input.Reason == "" {
					description = "Inventory Adjustment"
				}

				// source_id = the inventory_transactions row id (NOT the inventory
				// balance-row id): every adjustment JE then maps 1:1 to its stock
				// movement, so (source_type, source_id) dedupe scans work — with
				// the balance-row id, distinct adjustments on the same product/
				// warehouse were indistinguishable from double-posts.
				tx.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number,
						entry_date, description, source_type, source_id, status, total_debit, total_credit,
						created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, 'inventory_adjustment', $8, 'posted', $9, $9, $10, $10)
				`, entryID, tenantID, organizationID, journalID, entryNumber,
					now, description, transactionID.String(), totalCost, now)

				var debitAcct, creditAcct uuid.UUID
				var debitDesc, creditDesc string
				if input.Quantity > 0 {
					// Adding stock: Debit Stock Valuation / Credit Stock Input (liability, not expense)
					debitAcct = ca.StockValuationAccountID
					creditAcct = ca.StockInputAccountID
					if creditAcct == uuid.Nil {
						creditAcct = findAccount(tx, tenantID, orgIDPtr, "accounts payable", "6010")
					}
					debitDesc = "Stock Valuation (adjustment in)"
					creditDesc = "Stock Input (adjustment in)"
				} else {
					// Removing stock: Debit Adjustment Expense / Credit Stock Valuation
					debitAcct = adjustAcct
					creditAcct = ca.StockValuationAccountID
					debitDesc = "Inventory Adjustment Expense"
					creditDesc = "Stock Valuation (adjustment out)"
				}

				debitLineID := uuid.New()
				tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, account_id, description,
						debit_amount, credit_amount, line_number, created_at
					) VALUES ($1, $2, $3, $4, $5, 0, 1, $6)
				`, debitLineID, entryID, debitAcct, debitDesc, totalCost, now)

				creditLineID := uuid.New()
				tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, account_id, description,
						debit_amount, credit_amount, line_number, created_at
					) VALUES ($1, $2, $3, $4, 0, $5, 2, $6)
				`, creditLineID, entryID, creditAcct, creditDesc, totalCost, now)

				// Update account balances
				tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalCost, now, debitAcct)
				tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalCost, now, creditAcct)

				// Update journal next_number
				tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID)

				h.log.Info("Journal entry created for inventory adjustment", "entry_id", entryID, "amount", totalCost)
			}
		}
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
				"record_id":     input.ProductID,
				"product_id":    input.ProductID,
				"product_name":  productName,
				"product_code":  productCode,
				"reorder_point": reorderPoint,
				"available":     newBalance,
			})
			// In-app low stock notification
			balanceStr := fmt.Sprintf("%.0f", newBalance)
			h.createTranslatedNotification(tenantID, userID, "low_stock",
				map[string]interface{}{
					"product_id":   input.ProductID,
					"product_name": productName,
					"product_code": productCode,
					"available":    newBalance,
				},
				productName, balanceStr,
			)
		}

		h.EvaluateWorkflowRules(tenantID, "inventory.adjusted", map[string]interface{}{
			"record_id":    input.ProductID,
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
// TransferInventory godoc
// @Summary Create inventory transfer
// @Description Transfer inventory between warehouses or locations
// @Tags Inventory
// @Accept json
// @Produce json
// @Param transfer body entity.InventoryTransferInput true "Transfer details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/transfers [post]
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
			id, tenant_id, organization_id, inventory_id, product_id, warehouse_id,
			transaction_type, quantity,
			unit_cost, total_cost, from_warehouse_id, to_warehouse_id,
			from_location_id, to_location_id, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $16)
	`, uuid.New(), tenantID, organizationID, sourceInventoryID, productID, fromWarehouseID,
		transactionType, -input.Quantity,
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
			id, tenant_id, organization_id, inventory_id, product_id, warehouse_id,
			transaction_type, quantity,
			unit_cost, total_cost, from_warehouse_id, to_warehouse_id,
			from_location_id, to_location_id, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $16)
	`, uuid.New(), tenantID, organizationID, destInventoryID, productID, toWarehouseID,
		transactionType, input.Quantity,
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
		"message":           "Inventory transferred successfully",
		"transfer_id":       transferID,
		"quantity":          input.Quantity,
		"from_warehouse_id": fromWarehouseID,
		"to_warehouse_id":   toWarehouseID,
	})
}

// ListInventoryMovements returns inventory movement history
// ListInventoryMovements godoc
// @Summary List inventory movements
// @Description Get a paginated list of all inventory movements and transactions
// @Tags Inventory
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param product_id query string false "Filter by product ID"
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Param movement_type query string false "Filter by movement type"
// @Param start_date query string false "Start date (YYYY-MM-DD)"
// @Param end_date query string false "End date (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/movements [get]
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
	if limit < 1 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
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
			   i.product_id, p.code as product_code, p.name as product_name,
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
		ProductID         uuid.UUID              `json:"product_id"`
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
			&m.ProductID, &m.ProductCode, &m.ProductName,
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
// GetInventoryValuation godoc
// @Summary Get inventory valuation
// @Description Get the total inventory valuation across all warehouses and products
// @Tags Inventory
// @Accept json
// @Produce json
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Param category_id query string false "Filter by category ID"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/valuation [get]
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
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := (page - 1) * limit

	warehouseID := c.Query("warehouse_id")
	categoryID := c.Query("category_id")

	// average_cost is QUANTITY-WEIGHTED (Σqty×cost / Σqty), not a plain AVG
	// over warehouse rows — a 1-unit row must not weigh as much as a
	// 1000-unit row (audit §2). The inventory join is tenant-scoped: the
	// old bare `p.id = i.product_id` join let another tenant's rows leak
	// into the aggregate.
	baseQuery := `
		SELECT p.id as product_id, p.code as product_code, p.name as product_name,
			   COALESCE(pc.name, 'Uncategorized') as category_name,
			   COALESCE(SUM(i.quantity_on_hand), 0) as quantity_on_hand,
			   COALESCE(
			       SUM(i.quantity_on_hand * i.unit_cost) / NULLIF(SUM(i.quantity_on_hand), 0),
			       p.cost_price
			   ) as average_cost,
			   COALESCE(SUM(i.total_value), 0) as total_value,
			   p.cost_price as last_purchase_price
		FROM products p
		LEFT JOIN product_categories pc ON p.category_id = pc.id
		LEFT JOIN inventory i ON p.id = i.product_id AND i.tenant_id = p.tenant_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true AND p.is_stockable = true
	`
	countQuery := `
		SELECT COUNT(DISTINCT p.id)
		FROM products p
		LEFT JOIN inventory i ON p.id = i.product_id AND i.tenant_id = p.tenant_id
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
	baseQuery += " ORDER BY total_value DESC, p.id ASC"
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
		"items":       valuations,
		"total_value": totalInventoryValue,
		"currency":    "UZS", // TODO: Get from tenant settings
	}, pagination)
}

// GetCOGSData returns real COGS data per product using actual sale quantities and purchase cost layers
func (h *Handler) GetCOGSData(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// 1. Get sale quantities per product (from sales orders + POS orders)
	type productCOGS struct {
		ProductID   string  `json:"product_id"`
		ProductCode string  `json:"product_code"`
		ProductName string  `json:"product_name"`
		CostPrice   float64 `json:"cost_price"`
		SaleQty     float64 `json:"sale_qty"`
		CostMethod  string  `json:"costing_method"`
	}

	rows, err := h.db.Query(`
		SELECT p.id, COALESCE(p.code, ''), p.name, COALESCE(p.cost_price, 0),
		       COALESCE(p.costing_method, 'fifo'),
		       COALESCE(sales.qty, 0) + COALESCE(pos.qty, 0) as total_sold
		FROM products p
		LEFT JOIN (
			SELECT sol.product_id, SUM(sol.quantity) as qty
			FROM sales_order_lines sol
			JOIN sales_orders so ON sol.sales_order_id = so.id
			WHERE so.tenant_id = $1 AND so.deleted_at IS NULL
			  AND so.status IN ('confirmed', 'processing', 'shipped', 'delivered')
			GROUP BY sol.product_id
		) sales ON sales.product_id = p.id
		LEFT JOIN (
			SELECT pol.product_id, SUM(pol.quantity) as qty
			FROM pos_order_lines pol
			JOIN pos_orders po ON pol.order_id = po.id
			WHERE po.tenant_id = $1 AND po.status = 'completed'
			GROUP BY pol.product_id
		) pos ON pos.product_id = p.id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
		  AND (COALESCE(sales.qty, 0) + COALESCE(pos.qty, 0)) > 0
		ORDER BY total_sold DESC
		LIMIT 50
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to get COGS data", "error", err)
		response.InternalError(c, "Failed to get COGS data")
		return
	}
	defer rows.Close()

	var products []productCOGS
	var productIDs []uuid.UUID
	for rows.Next() {
		var p productCOGS
		if err := rows.Scan(&p.ProductID, &p.ProductCode, &p.ProductName, &p.CostPrice, &p.CostMethod, &p.SaleQty); err != nil {
			continue
		}
		products = append(products, p)
		pid, _ := uuid.Parse(p.ProductID)
		productIDs = append(productIDs, pid)
	}

	// 2. Get purchase cost layers per product (ordered by date for FIFO/LIFO)
	type costLayer struct {
		Qty       float64
		UnitPrice float64
	}
	costLayers := make(map[string][]costLayer)

	if len(productIDs) > 0 {
		layerRows, err := h.db.Query(`
			SELECT pol.product_id, pol.quantity, pol.unit_price
			FROM purchase_order_lines pol
			JOIN purchase_orders po ON pol.purchase_order_id = po.id
			WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
			  AND po.status IN ('approved', 'ordered', 'partial', 'received')
			  AND pol.product_id IS NOT NULL
			ORDER BY po.created_at ASC
		`, tenantID)
		if err == nil {
			defer layerRows.Close()
			for layerRows.Next() {
				var prodID string
				var qty, price float64
				if err := layerRows.Scan(&prodID, &qty, &price); err == nil {
					costLayers[prodID] = append(costLayers[prodID], costLayer{Qty: qty, UnitPrice: price})
				}
			}
		}
	}

	// 3. Calculate FIFO, WAC, LIFO COGS per product
	type cogsResult struct {
		ProductID   string  `json:"product_id"`
		ProductCode string  `json:"product_code"`
		ProductName string  `json:"product_name"`
		SaleQty     float64 `json:"sale_qty"`
		CostMethod  string  `json:"costing_method"`
		FIFOTotal   float64 `json:"fifo_total"`
		FIFOUnit    float64 `json:"fifo_unit"`
		WACTotal    float64 `json:"wac_total"`
		WACUnit     float64 `json:"wac_unit"`
		LIFOTotal   float64 `json:"lifo_total"`
		LIFOUnit    float64 `json:"lifo_unit"`
	}

	results := make([]cogsResult, 0, len(products))
	for _, p := range products {
		layers := costLayers[p.ProductID]
		r := cogsResult{
			ProductID:   p.ProductID,
			ProductCode: p.ProductCode,
			ProductName: p.ProductName,
			SaleQty:     p.SaleQty,
			CostMethod:  p.CostMethod,
		}

		if len(layers) == 0 {
			// No purchase layers — use product cost_price
			r.FIFOTotal = p.CostPrice * p.SaleQty
			r.FIFOUnit = p.CostPrice
			r.WACTotal = p.CostPrice * p.SaleQty
			r.WACUnit = p.CostPrice
			r.LIFOTotal = p.CostPrice * p.SaleQty
			r.LIFOUnit = p.CostPrice
		} else {
			// FIFO: consume from oldest layers first
			remaining := p.SaleQty
			fifoTotal := 0.0
			for _, l := range layers {
				if remaining <= 0 {
					break
				}
				take := l.Qty
				if take > remaining {
					take = remaining
				}
				fifoTotal += take * l.UnitPrice
				remaining -= take
			}
			// If sale qty exceeds all layers, use last known price for remainder
			if remaining > 0 {
				fifoTotal += remaining * layers[len(layers)-1].UnitPrice
			}
			r.FIFOTotal = fifoTotal
			if p.SaleQty > 0 {
				r.FIFOUnit = fifoTotal / p.SaleQty
			}

			// WAC: weighted average of all layers
			totalQty := 0.0
			totalCost := 0.0
			for _, l := range layers {
				totalQty += l.Qty
				totalCost += l.Qty * l.UnitPrice
			}
			wacUnit := 0.0
			if totalQty > 0 {
				wacUnit = totalCost / totalQty
			}
			r.WACUnit = wacUnit
			r.WACTotal = wacUnit * p.SaleQty

			// LIFO: consume from newest layers first
			remaining = p.SaleQty
			lifoTotal := 0.0
			for i := len(layers) - 1; i >= 0; i-- {
				if remaining <= 0 {
					break
				}
				take := layers[i].Qty
				if take > remaining {
					take = remaining
				}
				lifoTotal += take * layers[i].UnitPrice
				remaining -= take
			}
			if remaining > 0 {
				lifoTotal += remaining * layers[0].UnitPrice
			}
			r.LIFOTotal = lifoTotal
			if p.SaleQty > 0 {
				r.LIFOUnit = lifoTotal / p.SaleQty
			}
		}

		results = append(results, r)
	}

	response.Success(c, results)
}

// =====================================================
// BILL OF MATERIALS (BOM) HANDLERS
// =====================================================

// ListBOMs returns a paginated list of BOMs
// ListBOMs godoc
// @Summary List bills of materials
// @Description Get a paginated list of all bills of materials (BOMs)
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param search query string false "Search by product code or name"
// @Param product_id query string false "Filter by product ID"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms [get]
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
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
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
			   COALESCE((SELECT SUM(bl2.quantity * COALESCE(NULLIF(cp.cost_price, 0), cp.list_price, 0) * (1 + bl2.scrap_percent/100))
			     FROM bom_lines bl2 JOIN products cp ON bl2.component_id = cp.id
			     WHERE bl2.bom_id = b.id), 0)
			   -- Work center cost per product = hourly_cost / capacity_per_hour
			   -- (how many so'm it costs to make one unit at this work center).
			   -- Summed across all operations in the routing, added to the
			   -- components total to yield total cost per finished product.
			   + COALESCE((SELECT SUM(
			         COALESCE(wc.hourly_cost, 0)
			       / GREATEST(COALESCE(wc.capacity_per_hour, 1), 1)
			     )
			     FROM bom_operations bo LEFT JOIN work_centers wc ON bo.work_center_id = wc.id
			     WHERE bo.bom_id = b.id), 0) as total_cost
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
		// Load warehouse_id separately (column may not exist yet if migration 314 hasn't run)
		var whID sql.NullString
		var whName sql.NullString
		h.db.QueryRow(`SELECT b2.warehouse_id, w.name FROM product_boms b2 LEFT JOIN warehouses w ON w.id = b2.warehouse_id WHERE b2.id = $1`, b.ID).Scan(&whID, &whName)
		if whID.Valid {
			wid, _ := uuid.Parse(whID.String)
			b.WarehouseID = &wid
		}
		if whName.Valid {
			b.WarehouseName = &whName.String
		}
		var bomSplit sql.NullBool
		h.db.QueryRow(`SELECT has_split_output FROM product_boms WHERE id = $1`, b.ID).Scan(&bomSplit)
		b.HasSplitOutput = bomSplit.Valid && bomSplit.Bool

		boms = append(boms, &b)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, boms, pagination)
}

// GetBOM returns a single BOM with lines
// GetBOM godoc
// @Summary Get BOM details
// @Description Get detailed information about a specific bill of materials including all lines
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param id path string true "BOM ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{id} [get]
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

	// Load warehouse_id and warehouse_name separately (column may not exist
	// yet if migration 314 hasn't run on this environment).
	var whID sql.NullString
	var whName sql.NullString
	h.db.QueryRow(`SELECT b2.warehouse_id, w.name FROM product_boms b2 LEFT JOIN warehouses w ON w.id = b2.warehouse_id WHERE b2.id = $1`, bomID).Scan(&whID, &whName)
	if whID.Valid {
		wid, _ := uuid.Parse(whID.String)
		b.WarehouseID = &wid
	}
	if whName.Valid {
		b.WarehouseName = &whName.String
	}
	var bomSplit sql.NullBool
	h.db.QueryRow(`SELECT has_split_output FROM product_boms WHERE id = $1`, bomID).Scan(&bomSplit)
	b.HasSplitOutput = bomSplit.Valid && bomSplit.Bool

	// Get BOM lines
	rows, err := h.db.Query(`
		SELECT l.id, l.line_number, l.component_id, l.quantity, l.unit_of_measure,
			   l.scrap_percent, l.is_optional,
			   p.code as component_code, p.name as component_name, COALESCE(NULLIF(p.cost_price, 0), p.list_price, 0) as unit_cost
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

	// Add work-center cost per product (hourly_cost / capacity_per_hour)
	// summed across all operations in the routing, so the detail Total
	// Cost matches the list Total Cost.
	var operationsCost sql.NullFloat64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(
		    COALESCE(wc.hourly_cost, 0)
		  / GREATEST(COALESCE(wc.capacity_per_hour, 1), 1)
		), 0)
		FROM bom_operations bo
		LEFT JOIN work_centers wc ON bo.work_center_id = wc.id
		WHERE bo.bom_id = $1
	`, bomID).Scan(&operationsCost)

	b.TotalCost = totalCost + operationsCost.Float64

	response.Success(c, b)
}

// CreateBOM creates a new BOM
// CreateBOM godoc
// @Summary Create new bill of materials
// @Description Create a new bill of materials for a product
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param bom body entity.CreateBOMInput true "BOM details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms [post]
func (h *Handler) CreateBOM(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateBOMInput
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

	var warehouseID *uuid.UUID
	if input.WarehouseID != "" {
		wid, _ := uuid.Parse(input.WarehouseID)
		if wid != uuid.Nil {
			warehouseID = &wid
		}
	}

	// Try INSERT with warehouse_id and has_split_output
	_, err = tx.Exec(`
		INSERT INTO product_boms (
			id, tenant_id, organization_id, code, name, product_id, bom_type, quantity, version,
			is_active, is_default, effective_date, expiry_date, notes, warehouse_id, has_split_output,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, true, $9, $10, $11, $12, $13, $14, $15, $16, $16)
	`, bomID, tenantID, orgIDPtr, input.Code, input.Name, productID, bomType, quantity,
		input.IsDefault, effectiveDate, expiryDate, notes, warehouseID, input.HasSplitOutput, userID, now)

	if err != nil {
		// Retry without warehouse_id if column doesn't exist
		tx.Rollback()
		tx, _ = h.db.Begin()
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
		// Set warehouse after if column exists
		if warehouseID != nil {
			if _, execErr := h.db.Exec(`UPDATE product_boms SET warehouse_id = $1 WHERE id = $2`, warehouseID, bomID); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE product_boms", "error", execErr)
			}
		}
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
// UpdateBOM godoc
// @Summary Update bill of materials
// @Description Update an existing bill of materials
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param id path string true "BOM ID"
// @Param bom body entity.CreateBOMInput true "BOM details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{id} [put]
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
			if _, execErr := h.db.Exec("UPDATE product_boms SET is_default = false WHERE product_id = $1 AND tenant_id = $2 AND id != $3",
				productID, tenantID, bomID); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE product_boms", "error", execErr)
			}
		}
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.WarehouseID != nil {
		argCount++
		if *input.WarehouseID == "" {
			updates = append(updates, "warehouse_id = NULL")
			argCount-- // no arg added
		} else {
			wid, _ := uuid.Parse(*input.WarehouseID)
			updates = append(updates, fmt.Sprintf("warehouse_id = $%d", argCount))
			args = append(args, wid)
		}
	}
	if input.HasSplitOutput != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("has_split_output = $%d", argCount))
		args = append(args, *input.HasSplitOutput)
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
// DeleteBOM godoc
// @Summary Delete bill of materials
// @Description Soft delete a bill of materials
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param id path string true "BOM ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{id} [delete]
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
// CreateBOMLine godoc
// @Summary Add BOM line
// @Description Add a component line to a bill of materials
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param bom_id path string true "BOM ID"
// @Param line body entity.CreateBOMLineInput true "BOM line details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{bom_id}/lines [post]
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
	if _, execErr := h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", now, bomID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE product_boms", "error", execErr)
	}

	response.Created(c, gin.H{
		"id":          lineID,
		"line_number": maxLineNumber + 1,
		"message":     "BOM line created successfully",
	})
}

// DeleteBOMLine removes a line from a BOM
// DeleteBOMLine godoc
// @Summary Delete BOM line
// @Description Remove a component line from a bill of materials
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param bom_id path string true "BOM ID"
// @Param line_id path string true "BOM Line ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{bom_id}/lines/{line_id} [delete]
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
	if _, execErr := h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", time.Now(), bomID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE product_boms", "error", execErr)
	}

	response.Success(c, gin.H{"message": "BOM line deleted successfully"})
}

// =====================================================
// BOM OPERATIONS HANDLERS
// =====================================================

// ListBOMOperations returns all operations for a BOM
// ListBOMOperations godoc
// @Summary List BOM operations
// @Description Get all manufacturing operations for a bill of materials
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param bom_id path string true "BOM ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{bom_id}/operations [get]
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

	// Check if work_center_id column exists (migration 116)
	var hasWorkCenterID bool
	err = h.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'bom_operations' AND column_name = 'work_center_id'
		)
	`).Scan(&hasWorkCenterID)
	if err != nil {
		hasWorkCenterID = false
	}

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

	if hasWorkCenterID {
		// New schema with work_center_id FK
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
	} else {
		// Legacy schema without work_center_id - use work_center string to join
		rows, err := h.db.Query(`
			SELECT o.id, o.bom_id, o.sequence, o.operation_name, o.work_center,
				   o.setup_time_minutes, o.run_time_minutes, o.labor_cost, o.overhead_cost, o.notes,
				   o.created_at, o.updated_at
			FROM bom_operations o
			WHERE o.bom_id = $1
			ORDER BY o.sequence
		`, bomID)
		if err != nil {
			h.log.Error("Failed to list BOM operations (legacy)", "error", err)
			response.InternalError(c, "Failed to list BOM operations")
			return
		}
		defer rows.Close()

		for rows.Next() {
			var op BOMOperation
			var workCenter, notes sql.NullString

			err := rows.Scan(
				&op.ID, &op.BOMID, &op.Sequence, &op.OperationName, &workCenter,
				&op.SetupTimeMinutes, &op.RunTimeMinutes, &op.LaborCost, &op.OverheadCost, &notes,
				&op.CreatedAt, &op.UpdatedAt,
			)
			if err != nil {
				h.log.Error("Failed to scan BOM operation (legacy)", "error", err)
				continue
			}

			if workCenter.Valid {
				op.WorkCenter = &workCenter.String
			}
			if notes.Valid {
				op.Notes = &notes.String
			}

			operations = append(operations, op)
		}
	}

	response.Success(c, operations)
}

// CreateBOMOperation adds an operation to a BOM
// CreateBOMOperation godoc
// @Summary Create BOM operation
// @Description Add a manufacturing operation to a bill of materials
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param bom_id path string true "BOM ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{bom_id}/operations [post]
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// Check if work_center_id column exists
	var hasWorkCenterID bool
	h.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'bom_operations' AND column_name = 'work_center_id'
		)
	`).Scan(&hasWorkCenterID)

	if hasWorkCenterID {
		_, err = h.db.Exec(`
			INSERT INTO bom_operations (
				id, bom_id, sequence, operation_name, work_center, work_center_id,
				setup_time_minutes, run_time_minutes, labor_cost, overhead_cost, notes,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
		`, id, bomID, input.Sequence, input.OperationName, workCenter, workCenterID,
			input.SetupTimeMinutes, input.RunTimeMinutes, input.LaborCost, input.OverheadCost, notes, now)
	} else {
		_, err = h.db.Exec(`
			INSERT INTO bom_operations (
				id, bom_id, sequence, operation_name, work_center,
				setup_time_minutes, run_time_minutes, labor_cost, overhead_cost, notes,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
		`, id, bomID, input.Sequence, input.OperationName, workCenter,
			input.SetupTimeMinutes, input.RunTimeMinutes, input.LaborCost, input.OverheadCost, notes, now)
	}

	if err != nil {
		h.log.Error("Failed to create BOM operation", "error", err)
		response.InternalError(c, "Failed to create BOM operation")
		return
	}

	// Update BOM timestamp
	if _, execErr := h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", now, bomID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE product_boms", "error", execErr)
	}

	response.Created(c, gin.H{
		"id":       id,
		"sequence": input.Sequence,
		"message":  "BOM operation created successfully",
	})
}

// UpdateBOMOperation updates an operation in a BOM
// UpdateBOMOperation godoc
// @Summary Update BOM operation
// @Description Update a manufacturing operation in a bill of materials
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param bom_id path string true "BOM ID"
// @Param operation_id path string true "Operation ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{bom_id}/operations/{operation_id} [put]
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check if work_center_id column exists
	var hasWorkCenterID bool
	h.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'bom_operations' AND column_name = 'work_center_id'
		)
	`).Scan(&hasWorkCenterID)

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
	if input.WorkCenterID != nil && hasWorkCenterID {
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
	if _, execErr := h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", time.Now(), bomID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE product_boms", "error", execErr)
	}

	response.Success(c, gin.H{"message": "BOM operation updated successfully"})
}

// DeleteBOMOperation removes an operation from a BOM
// DeleteBOMOperation godoc
// @Summary Delete BOM operation
// @Description Remove a manufacturing operation from a bill of materials
// @Tags Inventory - BOM
// @Accept json
// @Produce json
// @Param bom_id path string true "BOM ID"
// @Param operation_id path string true "Operation ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/boms/{bom_id}/operations/{operation_id} [delete]
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

	// Detach any work orders referencing this operation before deleting
	if _, execErr := h.db.Exec("UPDATE work_orders SET operation_id = NULL WHERE operation_id = $1", operationID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE work_orders", "error", execErr)
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
	if _, execErr := h.db.Exec("UPDATE product_boms SET updated_at = $1 WHERE id = $2", time.Now(), bomID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE product_boms", "error", execErr)
	}

	response.Success(c, gin.H{"message": "BOM operation deleted successfully"})
}

// =====================================================
// SCRAP MANAGEMENT HANDLERS
// =====================================================

// ListScrapReasons returns all scrap reasons
// ListScrapReasons godoc
// @Summary List scrap reasons
// @Description Get all available scrap reasons for inventory write-offs
// @Tags Inventory - Scrap
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/scrap/reasons [get]
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
// CreateScrapReason godoc
// @Summary Create scrap reason
// @Description Create a new scrap reason for categorizing inventory write-offs
// @Tags Inventory - Scrap
// @Accept json
// @Produce json
// @Param reason body entity.CreateScrapReasonInput true "Scrap reason details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/scrap/reasons [post]
func (h *Handler) CreateScrapReason(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateScrapReasonInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
// ListScrapOrders godoc
// @Summary List scrap orders
// @Description Get a paginated list of inventory scrap/write-off orders
// @Tags Inventory - Scrap
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param search query string false "Search by reference"
// @Param status query string false "Filter by status"
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/scrap/orders [get]
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
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
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
// GetScrapOrder godoc
// @Summary Get scrap order details
// @Description Get detailed information about a specific scrap order
// @Tags Inventory - Scrap
// @Accept json
// @Produce json
// @Param id path string true "Scrap Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/scrap/orders/{id} [get]
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
// CreateScrapOrder godoc
// @Summary Create scrap order
// @Description Create a new inventory scrap/write-off order
// @Tags Inventory - Scrap
// @Accept json
// @Produce json
// @Param order body entity.CreateScrapOrderInput true "Scrap order details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/scrap/orders [post]
func (h *Handler) CreateScrapOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateScrapOrderInput
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
// ConfirmScrapOrder godoc
// @Summary Confirm scrap order
// @Description Confirm and process a scrap order to write off inventory
// @Tags Inventory - Scrap
// @Accept json
// @Produce json
// @Param id path string true "Scrap Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/scrap/orders/{id}/confirm [post]
func (h *Handler) ConfirmScrapOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid scrap order ID")
		return
	}

	// Get scrap order details
	var productID, warehouseID uuid.UUID
	var locationID, scrapOrgID sql.NullString
	var quantity, unitCost, totalCost float64
	var status, scrapNumber string
	var reason sql.NullString

	err = h.db.QueryRow(`
		SELECT product_id, warehouse_id, location_id, quantity,
			COALESCE(unit_cost, 0), COALESCE(total_cost, 0),
			status, scrap_number, reason, organization_id
		FROM scrap_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, orderID, tenantID).Scan(&productID, &warehouseID, &locationID, &quantity,
		&unitCost, &totalCost, &status, &scrapNumber, &reason, &scrapOrgID)

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

	// Reduce inventory and get inventory ID
	var locID *uuid.UUID
	if locationID.Valid {
		lid, _ := uuid.Parse(locationID.String)
		locID = &lid
	}

	var inventoryID uuid.UUID
	err = tx.QueryRow(`
		UPDATE inventory SET
			quantity_on_hand = quantity_on_hand - $1,
			last_movement_date = $2,
			updated_at = $2
		WHERE tenant_id = $3 AND product_id = $4 AND warehouse_id = $5
		AND COALESCE(location_id::text, '') = COALESCE($6::text, '')
		AND quantity_on_hand >= $1
		RETURNING id
	`, quantity, now, tenantID, productID, warehouseID, locID).Scan(&inventoryID)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "Insufficient inventory to scrap")
		return
	}
	if err != nil {
		h.log.Error("Failed to reduce inventory", "error", err)
		response.InternalError(c, "Failed to confirm scrap order")
		return
	}

	// Create inventory transaction
	txnID := uuid.New()
	var reasonStr *string
	if reason.Valid {
		reasonStr = &reason.String
	}
	_, err = tx.Exec(`
		INSERT INTO inventory_transactions (id, tenant_id, inventory_id, transaction_type,
			reference_type, reference_id, quantity, unit_cost, total_cost,
			reason, notes, created_by, created_at)
		VALUES ($1, $2, $3, 'scrap', 'scrap_order', $4, $5, $6, $7, $8, $9, $10, $11)
	`, txnID, tenantID, inventoryID, orderID.String(),
		-quantity, unitCost, totalCost, reasonStr,
		fmt.Sprintf("Scrap Order: %s", scrapNumber), userID, now)
	if err != nil {
		h.log.Error("Failed to create inventory transaction", "error", err)
	}

	// Update scrap order status
	scrapClaim, err := tx.Exec(`
		UPDATE scrap_orders SET
			status = 'completed',
			approved_by = $1,
			approved_at = $2,
			completed_at = $2,
			inventory_transaction_id = $3,
			updated_at = $2
		WHERE id = $4 AND tenant_id = $5 AND status <> 'completed'
	`, userID, now, txnID, orderID, tenantID)

	if err != nil {
		h.log.Error("Failed to update scrap order", "error", err)
		response.InternalError(c, "Failed to confirm scrap order")
		return
	}
	// Atomic claim: two concurrent confirms both passed the read-side status
	// check and scrapped the stock twice; the loser now matches 0 rows and
	// the whole tx (including its stock deduction) rolls back.
	if n, _ := scrapClaim.RowsAffected(); n == 0 {
		response.BadRequest(c, "This operation is already posted")
		return
	}

	// Create journal entry for the scrap loss
	if totalCost > 0 {
		var orgIDPtr *uuid.UUID
		if orgID != uuid.Nil {
			orgIDPtr = &orgID
		} else if scrapOrgID.Valid {
			parsed, _ := uuid.Parse(scrapOrgID.String)
			if parsed != uuid.Nil {
				orgIDPtr = &parsed
			}
		}

		var journalID uuid.UUID
		var nextNumber int
		tx.QueryRow(`SELECT id, COALESCE(next_number,1) FROM journals WHERE tenant_id=$1 AND code IN ('STOCK','INVENTORY','MISC','GENERAL') AND deleted_at IS NULL ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'INVENTORY' THEN 1 WHEN 'MISC' THEN 2 ELSE 3 END LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)

		if journalID != uuid.Nil {
			// Resolve both accounts BEFORE inserting the JE header: writing the
			// header first would leave a 0-line 'posted' entry when an account
			// is missing (migration 416 deferred trigger issue).
			// Debit: Scrap/Inventory Loss Expense
			scrapAcct := findAccount(tx, tenantID, orgIDPtr, "scrap", "6920")
			if scrapAcct == uuid.Nil {
				scrapAcct = findAccount(tx, tenantID, orgIDPtr, "inventory loss", "6910")
			}
			if scrapAcct == uuid.Nil {
				scrapAcct = findAccount(tx, tenantID, orgIDPtr, "stock adjustment", "6910")
			}

			// Credit: Inventory Asset
			inventoryAcct := findAccount(tx, tenantID, orgIDPtr, "inventory", "1010")
			if inventoryAcct == uuid.Nil {
				inventoryAcct = findAccount(tx, tenantID, orgIDPtr, "stock valuation", "1010")
			}

			if scrapAcct == uuid.Nil || inventoryAcct == uuid.Nil {
				h.log.Error("Cannot find accounts for scrap journal entry, skipping JE", "scrap_number", scrapNumber)
			} else {
				entryID := uuid.New()
				entryNumber := fmt.Sprintf("SCP%06d", nextNumber)
				description := fmt.Sprintf("Scrap Order: %s", scrapNumber)

				if _, err = tx.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number, entry_date,
						description, source_type, source_id, status, total_debit, total_credit,
						created_by, created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, 'scrap', $8, 'posted', $9, $9, $10, $11, $11)
				`, entryID, tenantID, orgIDPtr, journalID, entryNumber, now,
					description, orderID.String(), totalCost, userID, now); err != nil {
					h.log.Error("Failed to create scrap journal entry", "error", err)
					response.InternalError(c, "Failed to confirm scrap order")
					return
				}

				if _, err = tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, 'Scrap Loss', $4, 0, 1, $5)`,
					uuid.New(), entryID, scrapAcct, totalCost, now); err != nil {
					h.log.Error("Failed to insert scrap debit line", "error", err)
					response.InternalError(c, "Failed to confirm scrap order")
					return
				}
				if _, err = tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, 'Inventory Reduction', 0, $4, 2, $5)`,
					uuid.New(), entryID, inventoryAcct, totalCost, now); err != nil {
					h.log.Error("Failed to insert scrap credit line", "error", err)
					response.InternalError(c, "Failed to confirm scrap order")
					return
				}
				if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalCost, now, scrapAcct); err != nil {
					h.log.Error("Failed to update scrap account balance", "error", err)
					response.InternalError(c, "Failed to confirm scrap order")
					return
				}
				if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalCost, now, inventoryAcct); err != nil {
					h.log.Error("Failed to update inventory account balance", "error", err)
					response.InternalError(c, "Failed to confirm scrap order")
					return
				}

				if _, err = tx.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID); err != nil {
					h.log.Error("Failed to bump journal next_number", "error", err)
					response.InternalError(c, "Failed to confirm scrap order")
					return
				}
				if _, err = tx.Exec("UPDATE scrap_orders SET journal_entry_id = $1 WHERE id = $2", entryID, orderID); err != nil {
					h.log.Error("Failed to link journal entry to scrap order", "error", err)
					response.InternalError(c, "Failed to confirm scrap order")
					return
				}

				// TT 12.3: Scrap/write-offs affect budget
				if _, err = tx.Exec(`
					UPDATE budget_lines bl
					SET actual_amount = actual_amount + $1, updated_at = NOW()
					FROM budgets b
					WHERE bl.budget_id = b.id
					  AND b.tenant_id = $2
					  AND b.status = 'approved'
					  AND b.deleted_at IS NULL
					  AND bl.account_id = $3
					  AND (b.start_date IS NULL OR b.start_date <= $4)
					  AND (b.end_date IS NULL OR b.end_date >= $4)
				`, totalCost, tenantID, scrapAcct, now); err != nil {
					h.log.Error("Failed to update budget actuals for scrap", "error", err)
					response.InternalError(c, "Failed to confirm scrap order")
					return
				}
			}
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to confirm scrap order")
		return
	}

	response.Success(c, gin.H{"message": "Scrap order confirmed, inventory reduced, and journal entry created"})
}

// CancelScrapOrder cancels a scrap order
// CancelScrapOrder godoc
// @Summary Cancel scrap order
// @Description Cancel a pending scrap order
// @Tags Inventory - Scrap
// @Accept json
// @Produce json
// @Param id path string true "Scrap Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/scrap/orders/{id}/cancel [post]
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
// GetScrapSummary godoc
// @Summary Get scrap summary
// @Description Get summary statistics of scrap orders and inventory write-offs
// @Tags Inventory - Scrap
// @Accept json
// @Produce json
// @Param start_date query string false "Start date (YYYY-MM-DD)"
// @Param end_date query string false "End date (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/scrap/summary [get]
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
// ListReorderRules godoc
// @Summary List reorder rules
// @Description Get a paginated list of automatic reorder rules
// @Tags Inventory - Reorder
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param product_id query string false "Filter by product ID"
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Param active query boolean false "Filter by active status"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/reorder/rules [get]
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
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
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
		LEFT JOIN inventory i ON r.product_id = i.product_id AND i.tenant_id = r.tenant_id AND (r.warehouse_id IS NULL OR r.warehouse_id = i.warehouse_id)
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
// GetReorderRule godoc
// @Summary Get reorder rule details
// @Description Get detailed information about a specific reorder rule
// @Tags Inventory - Reorder
// @Accept json
// @Produce json
// @Param id path string true "Reorder Rule ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/reorder/rules/{id} [get]
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
// CreateReorderRule godoc
// @Summary Create reorder rule
// @Description Create a new automatic reorder rule for a product
// @Tags Inventory - Reorder
// @Accept json
// @Produce json
// @Param rule body entity.CreateReorderRuleInput true "Reorder rule details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/reorder/rules [post]
func (h *Handler) CreateReorderRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateReorderRuleInput
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

	var warehouseID *uuid.UUID
	if input.WarehouseID != "" {
		wid, err := uuid.Parse(input.WarehouseID)
		if err == nil {
			warehouseID = &wid
		}
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Check for existing rule (scoped to organization)
	var exists bool
	h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM reorder_rules WHERE product_id = $1 AND tenant_id = $2 AND COALESCE(warehouse_id::text, '') = COALESCE($3::text, '') AND COALESCE(organization_id::text, '') = COALESCE($4::text, ''))
	`, productID, tenantID, warehouseID, orgIDPtr).Scan(&exists)
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

	// Auto-calculate reorder_qty if not provided: max_qty - min_qty
	reorderQty := input.ReorderQty
	if reorderQty <= 0 && input.MaxQty > 0 {
		reorderQty = input.MaxQty - input.MinQty
		if reorderQty <= 0 {
			reorderQty = input.MaxQty
		}
	}

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	_, err = h.db.Exec(`
		INSERT INTO reorder_rules (
			id, tenant_id, organization_id, product_id, warehouse_id, min_qty, max_qty, reorder_qty,
			trigger_type, preferred_vendor_id, lead_time_days, safety_stock,
			auto_create_po, is_active, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, true, $14, $15, $16, $16)
	`, id, tenantID, orgIDPtr, productID, warehouseID, input.MinQty, maxQty, reorderQty,
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
// UpdateReorderRule godoc
// @Summary Update reorder rule
// @Description Update an existing reorder rule
// @Tags Inventory - Reorder
// @Accept json
// @Produce json
// @Param id path string true "Reorder Rule ID"
// @Param rule body entity.CreateReorderRuleInput true "Reorder rule details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/reorder/rules/{id} [put]
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
// DeleteReorderRule godoc
// @Summary Delete reorder rule
// @Description Delete a reorder rule
// @Tags Inventory - Reorder
// @Accept json
// @Produce json
// @Param id path string true "Reorder Rule ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/reorder/rules/{id} [delete]
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
// GetReorderAlerts godoc
// @Summary Get reorder alerts
// @Description Get a list of products that need reordering based on their reorder rules
// @Tags Inventory - Reorder
// @Accept json
// @Produce json
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/reorder/alerts [get]
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
		LEFT JOIN inventory i ON r.product_id = i.product_id AND i.tenant_id = r.tenant_id AND (r.warehouse_id IS NULL OR r.warehouse_id = i.warehouse_id)
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
		RuleID            uuid.UUID  `json:"rule_id"`
		ProductID         uuid.UUID  `json:"product_id"`
		ProductCode       string     `json:"product_code"`
		ProductName       string     `json:"product_name"`
		WarehouseID       *uuid.UUID `json:"warehouse_id,omitempty"`
		WarehouseName     string     `json:"warehouse_name,omitempty"`
		CurrentStock      float64    `json:"current_stock"`
		MinQty            float64    `json:"min_qty"`
		ReorderQty        float64    `json:"reorder_qty"`
		SuggestedOrderQty float64    `json:"suggested_order_qty"`
		VendorID          *uuid.UUID `json:"vendor_id,omitempty"`
		VendorName        string     `json:"vendor_name,omitempty"`
		LeadTimeDays      int        `json:"lead_time_days"`
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
// RunReplenishment godoc
// @Summary Run inventory replenishment
// @Description Automatically create purchase orders for products that need replenishment based on reorder rules
// @Tags Inventory - Reorder
// @Accept json
// @Produce json
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Param confirm query boolean false "Confirm and create purchase orders" default(false)
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/replenishment/run [post]
func (h *Handler) RunReplenishment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	organizationID, _ := middleware.GetOrganizationID(c)

	// Parse input for optional filters
	var input struct {
		ProductIDs  []string `json:"product_ids"`
		WarehouseID string   `json:"warehouse_id"`
		VendorID    string   `json:"vendor_id"`
		RuleIDs     []string `json:"rule_ids"`
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
		LEFT JOIN inventory i ON r.product_id = i.product_id AND i.tenant_id = r.tenant_id AND (r.warehouse_id IS NULL OR r.warehouse_id = i.warehouse_id)
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
		RuleID       uuid.UUID
		ProductID    uuid.UUID
		ProductCode  string
		ProductName  string
		WarehouseID  *uuid.UUID
		MinQty       float64
		MaxQty       float64
		ReorderQty   float64
		CurrentStock float64
		VendorID     *uuid.UUID
		LeadTimeDays int
		AutoCreatePO bool
		OrderQty     float64
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

		// Use reorder_qty if explicitly set, otherwise calculate from max_qty
		if item.ReorderQty > 0 {
			item.OrderQty = item.ReorderQty
		} else if item.MaxQty > 0 {
			item.OrderQty = item.MaxQty - item.CurrentStock
			if item.OrderQty <= 0 {
				continue
			}
		} else {
			item.OrderQty = item.MinQty - item.CurrentStock
			if item.OrderQty <= 0 {
				continue
			}
		}

		items = append(items, &item)
	}

	if len(items) == 0 {
		response.Success(c, gin.H{
			"message":        "No products need replenishment",
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

		// Generate sequential order number
		var poCount int
		h.db.QueryRow("SELECT COUNT(*) FROM purchase_orders WHERE tenant_id = $1", tenantID).Scan(&poCount)
		orderNumber := fmt.Sprintf("P%05d", poCount+1+ordersCreated)

		// Calculate expected delivery date from max lead time of items
		maxLeadDays := 0
		for _, item := range items {
			if item.LeadTimeDays > maxLeadDays {
				maxLeadDays = item.LeadTimeDays
			}
		}
		expectedDate := time.Now().AddDate(0, 0, maxLeadDays)

		// Create purchase order
		poID := uuid.New()
		_, err = h.db.Exec(`
			INSERT INTO purchase_orders (
				id, tenant_id, organization_id, order_number, vendor_id, order_date, expected_date, status, payment_status,
				subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
				exchange_rate, requested_by, notes, is_auto_replenishment, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		`, poID, tenantID, organizationID, orderNumber, vid, time.Now(), expectedDate, "draft", "unpaid",
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
			// Get product price: vendor price > cost_price > 0
			var unitPrice float64
			err := h.db.QueryRow(`
				SELECT price FROM vendor_prices
				WHERE vendor_id = $1 AND product_id = $2 AND tenant_id = $3
				ORDER BY created_at DESC LIMIT 1
			`, vid, item.ProductID, tenantID).Scan(&unitPrice)
			if err != nil || unitPrice == 0 {
				h.db.QueryRow(`SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1`, item.ProductID).Scan(&unitPrice)
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
		if _, execErr := h.db.Exec(`
			UPDATE purchase_orders SET subtotal = $1, total_amount = $1, updated_at = $2
			WHERE id = $3
		`, subtotal, time.Now(), poID); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE purchase_orders", "error", execErr)
		}

		ordersCreated++
		orderIDs = append(orderIDs, poID)
	}

	// Create PO for items without vendors (vendor_id = NULL)
	if len(noVendorItems) > 0 {
		var poCount2 int
		h.db.QueryRow("SELECT COUNT(*) FROM purchase_orders WHERE tenant_id = $1", tenantID).Scan(&poCount2)
		orderNumber := fmt.Sprintf("P%05d", poCount2+1)

		// Calculate expected delivery date from max lead time
		maxLeadDays := 0
		for _, item := range noVendorItems {
			if item.LeadTimeDays > maxLeadDays {
				maxLeadDays = item.LeadTimeDays
			}
		}
		expectedDate := time.Now().AddDate(0, 0, maxLeadDays)

		poID := uuid.New()
		_, err = h.db.Exec(`
			INSERT INTO purchase_orders (
				id, tenant_id, organization_id, order_number, vendor_id, order_date, expected_date, status, payment_status,
				subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
				exchange_rate, requested_by, notes, is_auto_replenishment, created_at, updated_at
			) VALUES ($1, $2, $3, $4, NULL, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		`, poID, tenantID, organizationID, orderNumber, time.Now(), expectedDate, "draft", "unpaid",
			0, 0, 0, 0, 0, 1.0, userID, "Auto-generated by replenishment (no vendor)", true, time.Now(), time.Now())

		if err != nil {
			h.log.Error("Failed to create purchase order for no-vendor items", "error", err)
			for _, item := range noVendorItems {
				skippedItems = append(skippedItems, map[string]interface{}{
					"product_id":   item.ProductID,
					"product_name": item.ProductName,
					"reason":       "Failed to create purchase order",
				})
			}
		} else {
			var subtotal float64
			lineNumber := 1
			for _, item := range noVendorItems {
				var unitPrice float64
				h.db.QueryRow(`SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1`, item.ProductID).Scan(&unitPrice)

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

			if _, execErr := h.db.Exec(`
				UPDATE purchase_orders SET subtotal = $1, total_amount = $1, updated_at = $2
				WHERE id = $3
			`, subtotal, time.Now(), poID); execErr != nil {

				h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE purchase_orders", "error", execErr)

			}

			ordersCreated++
			orderIDs = append(orderIDs, poID)
		}
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
// GetReplenishmentPreview godoc
// @Summary Preview replenishment orders
// @Description Get a preview of purchase orders that would be created by replenishment without actually creating them
// @Tags Inventory - Reorder
// @Accept json
// @Produce json
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /inventory/replenishment/preview [get]
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
		RuleID        uuid.UUID  `json:"rule_id"`
		ProductID     uuid.UUID  `json:"product_id"`
		ProductCode   string     `json:"product_code"`
		ProductName   string     `json:"product_name"`
		SKU           string     `json:"sku,omitempty"`
		WarehouseID   *uuid.UUID `json:"warehouse_id,omitempty"`
		WarehouseName string     `json:"warehouse_name,omitempty"`
		VendorID      *uuid.UUID `json:"vendor_id,omitempty"`
		VendorName    string     `json:"vendor_name,omitempty"`
		CurrentStock  float64    `json:"current_stock"`
		MinQty        float64    `json:"min_qty"`
		MaxQty        float64    `json:"max_qty"`
		ReorderQty    float64    `json:"reorder_qty"`
		SafetyStock   float64    `json:"safety_stock"`
		SuggestedQty  float64    `json:"suggested_qty"`
		UnitPrice     float64    `json:"unit_price"`
		EstimatedCost float64    `json:"estimated_cost"`
		LeadTimeDays  int        `json:"lead_time_days"`
		Status        string     `json:"status"` // "critical", "low", "reorder"
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

// =====================================================
// STOCK COUNT HANDLERS
// =====================================================

func (h *Handler) ListStockCounts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT sc.id, sc.tenant_id, sc.warehouse_id, w.name as warehouse_name,
			sc.count_number, sc.count_type, sc.count_date, sc.status, sc.notes,
			sc.started_at, sc.completed_at, sc.approved_at,
			sc.total_system_value, sc.total_counted_value, sc.total_variance_value,
			sc.created_at, sc.updated_at,
			(SELECT COUNT(*) FROM stock_count_lines WHERE stock_count_id = sc.id) as line_count,
			(SELECT COUNT(*) FROM stock_count_lines WHERE stock_count_id = sc.id AND counted_quantity IS NOT NULL) as counted_count,
			sc.counted_by_name
		FROM stock_counts sc
		LEFT JOIN warehouses w ON sc.warehouse_id = w.id
		WHERE sc.tenant_id = $1
	`
	args := []interface{}{tenantID}
	argCount := 1
	whereExtra := ""

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		whereExtra += fmt.Sprintf(" AND sc.organization_id = $%d", argCount)
		args = append(args, orgID)
	}
	if status := c.Query("status"); status != "" {
		argCount++
		whereExtra += fmt.Sprintf(" AND sc.status = $%d", argCount)
		args = append(args, status)
	}
	if warehouseID := c.Query("warehouse_id"); warehouseID != "" {
		argCount++
		whereExtra += fmt.Sprintf(" AND sc.warehouse_id = $%d", argCount)
		args = append(args, warehouseID)
	}

	query += whereExtra
	query += " ORDER BY sc.created_at DESC"

	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list stock counts", "error", err)
		response.InternalError(c, "Failed to list stock counts")
		return
	}
	defer rows.Close()

	type StockCountResponse struct {
		ID                 uuid.UUID  `json:"id"`
		TenantID           uuid.UUID  `json:"tenant_id"`
		WarehouseID        uuid.UUID  `json:"warehouse_id"`
		WarehouseName      string     `json:"warehouse_name"`
		CountNumber        string     `json:"count_number"`
		CountType          string     `json:"count_type"`
		CountDate          time.Time  `json:"count_date"`
		Status             string     `json:"status"`
		Notes              *string    `json:"notes,omitempty"`
		StartedAt          *time.Time `json:"started_at,omitempty"`
		CompletedAt        *time.Time `json:"completed_at,omitempty"`
		ApprovedAt         *time.Time `json:"approved_at,omitempty"`
		TotalSystemValue   float64    `json:"total_system_value"`
		TotalCountedValue  float64    `json:"total_counted_value"`
		TotalVarianceValue float64    `json:"total_variance_value"`
		CreatedAt          time.Time  `json:"created_at"`
		UpdatedAt          time.Time  `json:"updated_at"`
		LineCount          int        `json:"line_count"`
		CountedCount       int        `json:"counted_count"`
		CountedByName      *string    `json:"counted_by,omitempty"`
	}

	counts := make([]StockCountResponse, 0)
	for rows.Next() {
		var sc StockCountResponse
		var notes, countedByName sql.NullString
		var startedAt, completedAt, approvedAt sql.NullTime
		if err := rows.Scan(
			&sc.ID, &sc.TenantID, &sc.WarehouseID, &sc.WarehouseName,
			&sc.CountNumber, &sc.CountType, &sc.CountDate, &sc.Status, &notes,
			&startedAt, &completedAt, &approvedAt,
			&sc.TotalSystemValue, &sc.TotalCountedValue, &sc.TotalVarianceValue,
			&sc.CreatedAt, &sc.UpdatedAt, &sc.LineCount, &sc.CountedCount,
			&countedByName,
		); err != nil {
			h.log.Error("Failed to scan stock count", "error", err)
			continue
		}
		if notes.Valid {
			sc.Notes = &notes.String
		}
		if startedAt.Valid {
			sc.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			sc.CompletedAt = &completedAt.Time
		}
		if approvedAt.Valid {
			sc.ApprovedAt = &approvedAt.Time
		}
		if countedByName.Valid {
			sc.CountedByName = &countedByName.String
		}
		counts = append(counts, sc)
	}

	if !paginate {
		response.Success(c, counts)
		return
	}

	var total int
	countQuery := `SELECT COUNT(*) FROM stock_counts sc WHERE sc.tenant_id = $1` + whereExtra
	_ = h.db.QueryRow(countQuery, args[:argCount]...).Scan(&total)
	response.Paginated(c, counts, page, pageSize, total)
}

func (h *Handler) GetStockCount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid stock count ID")
		return
	}

	// Get header
	type StockCountResponse struct {
		ID                 uuid.UUID   `json:"id"`
		WarehouseID        uuid.UUID   `json:"warehouse_id"`
		WarehouseName      string      `json:"warehouse_name"`
		CountNumber        string      `json:"count_number"`
		CountType          string      `json:"count_type"`
		CountDate          time.Time   `json:"count_date"`
		Status             string      `json:"status"`
		Notes              *string     `json:"notes,omitempty"`
		StartedAt          *time.Time  `json:"started_at,omitempty"`
		CompletedAt        *time.Time  `json:"completed_at,omitempty"`
		TotalSystemValue   float64     `json:"total_system_value"`
		TotalCountedValue  float64     `json:"total_counted_value"`
		TotalVarianceValue float64     `json:"total_variance_value"`
		CreatedAt          time.Time   `json:"created_at"`
		Lines              interface{} `json:"lines"`
	}

	var sc StockCountResponse
	var notes sql.NullString
	var startedAt, completedAt sql.NullTime

	err = h.db.QueryRow(`
		SELECT sc.id, sc.warehouse_id, w.name, sc.count_number, sc.count_type, sc.count_date,
			sc.status, sc.notes, sc.started_at, sc.completed_at,
			sc.total_system_value, sc.total_counted_value, sc.total_variance_value, sc.created_at
		FROM stock_counts sc
		LEFT JOIN warehouses w ON sc.warehouse_id = w.id
		WHERE sc.id = $1 AND sc.tenant_id = $2
	`, id, tenantID).Scan(
		&sc.ID, &sc.WarehouseID, &sc.WarehouseName, &sc.CountNumber, &sc.CountType, &sc.CountDate,
		&sc.Status, &notes, &startedAt, &completedAt,
		&sc.TotalSystemValue, &sc.TotalCountedValue, &sc.TotalVarianceValue, &sc.CreatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock count")
		return
	}
	if err != nil {
		h.log.Error("Failed to get stock count", "error", err)
		response.InternalError(c, "Failed to get stock count")
		return
	}
	if notes.Valid {
		sc.Notes = &notes.String
	}
	if startedAt.Valid {
		sc.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		sc.CompletedAt = &completedAt.Time
	}

	// Get lines
	type LineResponse struct {
		ID               uuid.UUID  `json:"id"`
		ProductID        uuid.UUID  `json:"product_id"`
		ProductName      string     `json:"product_name"`
		ProductCode      string     `json:"product_code"`
		SystemQuantity   float64    `json:"system_quantity"`
		CountedQuantity  *float64   `json:"counted_quantity"`
		VarianceQuantity float64    `json:"variance_quantity"`
		UnitCost         *float64   `json:"unit_cost,omitempty"`
		Status           string     `json:"status"`
		Notes            *string    `json:"notes,omitempty"`
		Resolution       string     `json:"resolution"`
		ResponsibleEmpID *uuid.UUID `json:"responsible_emp_id,omitempty"`
		ResponsibleName  string     `json:"responsible_name,omitempty"`
	}

	lineRows, err := h.db.Query(`
		SELECT scl.id, scl.product_id, COALESCE(p.name,''), COALESCE(p.code,''),
			scl.system_quantity, scl.counted_quantity, scl.variance_quantity,
			scl.unit_cost, scl.status, scl.notes,
			COALESCE(scl.resolution, 'pending'), scl.responsible_emp_id,
			COALESCE(e.first_name || ' ' || e.last_name, '')
		FROM stock_count_lines scl
		LEFT JOIN products p ON scl.product_id = p.id
		LEFT JOIN employees e ON scl.responsible_emp_id = e.id
		WHERE scl.stock_count_id = $1
		ORDER BY COALESCE(p.name,'')
	`, id)
	if err != nil {
		h.log.Error("Failed to get stock count lines", "error", err)
		sc.Lines = []LineResponse{}
	} else {
		defer lineRows.Close()
		lines := make([]LineResponse, 0)
		for lineRows.Next() {
			var l LineResponse
			var countedQty, unitCost sql.NullFloat64
			var lineNotes sql.NullString
			var respEmpID *uuid.UUID
			if err := lineRows.Scan(
				&l.ID, &l.ProductID, &l.ProductName, &l.ProductCode,
				&l.SystemQuantity, &countedQty, &l.VarianceQuantity,
				&unitCost, &l.Status, &lineNotes,
				&l.Resolution, &respEmpID, &l.ResponsibleName,
			); err != nil {
				continue
			}
			if countedQty.Valid {
				l.CountedQuantity = &countedQty.Float64
			}
			if unitCost.Valid {
				l.UnitCost = &unitCost.Float64
			}
			if lineNotes.Valid {
				l.Notes = &lineNotes.String
			}
			l.ResponsibleEmpID = respEmpID
			lines = append(lines, l)
		}
		sc.Lines = lines
	}

	response.Success(c, sc)
}

func (h *Handler) CreateStockCount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	var input entity.CreateStockCountInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	warehouseID, err := uuid.Parse(input.WarehouseID)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	countDate, err := time.Parse("2006-01-02", input.CountDate)
	if err != nil {
		response.BadRequest(c, "Invalid count_date format")
		return
	}

	countType := input.CountType
	if countType == "" {
		countType = "full"
	}

	// Generate count number
	var nextNum int
	h.db.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTRING(count_number FROM 'WS-[0-9]+-([0-9]+)') AS INTEGER)),0)+1 FROM stock_counts WHERE tenant_id=$1 AND count_number LIKE 'WS-%'", tenantID).Scan(&nextNum)
	if nextNum == 0 {
		// Also check old INV- prefix for continuity
		h.db.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTRING(count_number FROM 'INV-[0-9]+-([0-9]+)') AS INTEGER)),0)+1 FROM stock_counts WHERE tenant_id=$1 AND count_number LIKE 'INV-%'", tenantID).Scan(&nextNum)
	}
	if nextNum == 0 {
		nextNum = 1
	}
	countNumber := fmt.Sprintf("WS-%d-%03d", countDate.Year(), nextNum)

	id := uuid.New()
	now := time.Now()

	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}
	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		userIDPtr = &userID
	}
	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}
	var countedByName *string
	if input.CountedByName != "" {
		countedByName = &input.CountedByName
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create stock count")
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO stock_counts (id, tenant_id, organization_id, warehouse_id, count_number, count_type, count_date, status, notes, counted_by_name, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'draft', $8, $9, $10, $11, $11)
	`, id, tenantID, orgIDPtr, warehouseID, countNumber, countType, countDate, notes, countedByName, userIDPtr, now)
	if err != nil {
		h.log.Error("Failed to create stock count", "error", err, "count_number", countNumber, "warehouse_id", warehouseID, "org_id", orgIDPtr)
		h.log.Error("Failed to create stock count", "error", err)
		response.InternalError(c, "Failed to create stock count")
		return
	}

	// Auto-populate lines from current inventory for this warehouse
	// First collect all inventory items, then insert lines (can't exec inside open rows cursor)
	type invItem struct {
		ProductID uuid.UUID
		Qty       float64
		Cost      float64
	}
	var invItems []invItem

	invQuery := `
		SELECT i.product_id, i.quantity_on_hand, COALESCE(i.unit_cost, 0)
		FROM inventory i
		JOIN products p ON i.product_id = p.id AND p.deleted_at IS NULL
		WHERE i.tenant_id = $1 AND i.warehouse_id = $2 AND i.quantity_on_hand != 0
	`
	invArgs := []interface{}{tenantID, warehouseID}

	// Filter by selected products if specified
	if len(input.SelectedProducts) > 0 {
		productUUIDs := make([]uuid.UUID, 0, len(input.SelectedProducts))
		for _, pid := range input.SelectedProducts {
			if parsed, parseErr := uuid.Parse(pid); parseErr == nil {
				productUUIDs = append(productUUIDs, parsed)
			}
		}
		if len(productUUIDs) > 0 {
			placeholders := make([]string, len(productUUIDs))
			for i, pid := range productUUIDs {
				placeholders[i] = fmt.Sprintf("$%d", i+3)
				invArgs = append(invArgs, pid)
			}
			invQuery += " AND i.product_id IN (" + strings.Join(placeholders, ",") + ")"
		}
	}

	invQuery += " ORDER BY p.name"
	invRows, err := tx.Query(invQuery, invArgs...)
	if err != nil {
		h.log.Error("Failed to query inventory for stock count", "error", err)
	} else {
		for invRows.Next() {
			var item invItem
			if err := invRows.Scan(&item.ProductID, &item.Qty, &item.Cost); err != nil {
				continue
			}
			invItems = append(invItems, item)
		}
		invRows.Close()
	}

	for _, item := range invItems {
		sysValue := item.Qty * item.Cost
		lineID := uuid.New()
		_, err := tx.Exec(`
			INSERT INTO stock_count_lines (id, stock_count_id, product_id, system_quantity, unit_cost, system_value, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $7)
		`, lineID, id, item.ProductID, item.Qty, item.Cost, sysValue, now)
		if err != nil {
			h.log.Error("Failed to insert stock count line", "error", err, "product_id", item.ProductID)
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit stock count transaction", "error", err)
		h.log.Error("Failed to create stock count", "error", err)
		response.InternalError(c, "Failed to create stock count")
		return
	}

	c.Params = append(c.Params, gin.Param{Key: "id", Value: id.String()})
	h.GetStockCount(c)
}

func (h *Handler) RecordCountLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	countID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid stock count ID")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.RecordCountLineInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Verify the stock count exists and is not completed
	var status string
	err = h.db.QueryRow("SELECT status FROM stock_counts WHERE id=$1 AND tenant_id=$2", countID, tenantID).Scan(&status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock count")
		return
	}
	if status == "completed" || status == "cancelled" {
		response.BadRequest(c, "Cannot update a "+status+" stock count")
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	now := time.Now()
	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	countedQty := float64(0)
	if input.CountedQuantity != nil {
		countedQty = *input.CountedQuantity
	}

	// Try to update existing line
	result, err := h.db.Exec(`
		UPDATE stock_count_lines SET
			counted_quantity = $1, status = 'counted', counted_by = $2, counted_at = $3, notes = $4, updated_at = $3
		WHERE stock_count_id = $5 AND product_id = $6
	`, countedQty, userID, now, notes, countID, productID)

	if err != nil {
		h.log.Error("Failed to record count line", "error", err)
		response.InternalError(c, "Failed to record count line")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		// Line doesn't exist — create it
		lineID := uuid.New()
		_, err = h.db.Exec(`
			INSERT INTO stock_count_lines (id, stock_count_id, product_id, system_quantity, counted_quantity, unit_cost, status, counted_by, counted_at, notes, created_at, updated_at)
			VALUES ($1, $2, $3, 0, $4, 0, 'counted', $5, $6, $7, $6, $6)
		`, lineID, countID, productID, countedQty, userID, now, notes)
		if err != nil {
			h.log.Error("Failed to insert count line", "error", err)
			response.InternalError(c, "Failed to record count line")
			return
		}
	}

	// Update count status to in_progress if still draft
	if _, execErr := h.db.Exec("UPDATE stock_counts SET status = 'in_progress', started_at = COALESCE(started_at, $1), started_by = COALESCE(started_by, $2), updated_at = $1 WHERE id = $3 AND status = 'draft'", now, userID, countID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_counts", "error", execErr)
	}

	response.Success(c, gin.H{"message": "Count recorded"})
}

func (h *Handler) CompleteStockCount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	countID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid stock count ID")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	// Get stock count
	var warehouseID uuid.UUID
	var countNumber, status string
	err = h.db.QueryRow("SELECT warehouse_id, count_number, status FROM stock_counts WHERE id=$1 AND tenant_id=$2", countID, tenantID).Scan(&warehouseID, &countNumber, &status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock count")
		return
	}
	if status == "completed" || status == "cancelled" {
		response.BadRequest(c, "Stock count already "+status)
		return
	}

	// Verify at least one line has been counted
	var uncountedLines int
	h.db.QueryRow("SELECT COUNT(*) FROM stock_count_lines WHERE stock_count_id = $1 AND counted_quantity IS NULL", countID).Scan(&uncountedLines)
	var totalLines int
	h.db.QueryRow("SELECT COUNT(*) FROM stock_count_lines WHERE stock_count_id = $1", countID).Scan(&totalLines)
	if totalLines > 0 && uncountedLines == totalLines {
		response.BadRequest(c, "All lines must be counted before completing")
		return
	}

	// Get lines with variance
	type varianceLine struct {
		ProductID uuid.UUID
		Variance  float64
		UnitCost  float64
	}

	rows, err := h.db.Query(`
		SELECT product_id, variance_quantity, COALESCE(unit_cost, 0)
		FROM stock_count_lines
		WHERE stock_count_id = $1 AND counted_quantity IS NOT NULL AND variance_quantity != 0
	`, countID)
	if err != nil {
		h.log.Error("Failed to get variance lines", "error", err)
		response.InternalError(c, "Failed to complete stock count")
		return
	}
	defer rows.Close()

	var lines []varianceLine
	for rows.Next() {
		var l varianceLine
		if err := rows.Scan(&l.ProductID, &l.Variance, &l.UnitCost); err != nil {
			continue
		}
		lines = append(lines, l)
	}

	now := time.Now()
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Net (signed) variance matters: a surplus increases inventory (DR 1010)
	// while a shortage decreases it (CR 1010). The old code summed math.Abs
	// and always CR'd inventory, so a surplus raised the physical quantity
	// but lowered the GL valuation.
	netVarianceValue := 0.0
	for _, line := range lines {
		netVarianceValue += line.Variance * line.UnitCost
	}

	// Resolve journal + accounts before the tx (plain reads)
	var journalID uuid.UUID
	var nextNumber int
	var adjustAcct, stockAcct uuid.UUID
	postJE := math.Abs(netVarianceValue) > 0.005
	if postJE {
		h.db.QueryRow(`SELECT id, COALESCE(next_number,1) FROM journals WHERE tenant_id=$1 AND code IN ('STOCK','INVENTORY','MISC','GENERAL') AND deleted_at IS NULL ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'INVENTORY' THEN 1 WHEN 'MISC' THEN 2 ELSE 3 END LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)

		// Debit: Stock Adjustment Expense
		adjustAcct = findAccount(h.db, tenantID, orgIDPtr, "stock adjustment", "6910")
		if adjustAcct == uuid.Nil {
			adjustAcct = findAccount(h.db, tenantID, orgIDPtr, "inventory adjustment", "6910")
		}
		// Credit: Stock Valuation
		stockAcct = findAccount(h.db, tenantID, orgIDPtr, "inventory", "1010")
		if stockAcct == uuid.Nil {
			stockAcct = findAccount(h.db, tenantID, orgIDPtr, "stock valuation", "1010")
		}
		postJE = journalID != uuid.Nil && adjustAcct != uuid.Nil && stockAcct != uuid.Nil
	}

	// Everything the completion changes — variance stock deltas, their ledger
	// rows, the net-variance JE and the 'completed' status — lands in ONE
	// transaction. The old flow applied stock via h.db, posted the JE in a
	// separate tx and flipped the status last: a failure mid-way left stock
	// applied with the count still open, and re-completing double-applied
	// the variances (docs/ombor-audit.md §4).
	completeErr := func() error {
		tx, txErr := h.db.Begin()
		if txErr != nil {
			return txErr
		}
		defer tx.Rollback()

		for _, line := range lines {
			if _, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
				TenantID: tenantID, OrgID: orgIDPtr, ProductID: line.ProductID,
				WarehouseID: warehouseID, Qty: line.Variance, UnitCost: line.UnitCost,
				TxType: "count", RefType: "stock_count", RefID: countID.String(),
				Reason:    fmt.Sprintf("Stock count %s", countNumber),
				Notes:     "Inventory count adjustment: " + countNumber,
				CreatedBy: userID, When: now, AllowNeg: true,
			}); dErr != nil {
				return dErr
			}
		}

		if postJE {
			entryID := uuid.New()
			description := fmt.Sprintf("Inventory Count Adjustment: %s", countNumber)
			amount := math.Abs(netVarianceValue)

			// Inventory GL moves with the physical quantity; the adjustment
			// account takes the opposite side (expense on loss, gain on surplus).
			var stockDr, stockCr, adjDr, adjCr float64
			if netVarianceValue >= 0 {
				stockDr, adjCr = amount, amount // surplus: DR inventory, CR adjustment (gain)
			} else {
				adjDr, stockCr = amount, amount // shortage: DR adjustment (loss), CR inventory
			}

			entryNumber := fmt.Sprintf("ADJ%06d", nextEntryNumberSeq(tx, tenantID, orgIDPtr, "ADJ", nextNumber))
			if _, jErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date,
					description, source_type, source_id, status, total_debit, total_credit,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 'stock_count', $8, 'posted', $9, $9, $10, $11, $11)
			`, entryID, tenantID, orgIDPtr, journalID, entryNumber, now,
				description, countID.String(), amount, userID, now); jErr != nil {
				return jErr
			}
			if _, jErr := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, 'Stock Valuation', $4, $5, 1, $6)`,
				uuid.New(), entryID, stockAcct, stockDr, stockCr, now); jErr != nil {
				return jErr
			}
			if _, jErr := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, 'Stock Count Adjustment', $4, $5, 2, $6)`,
				uuid.New(), entryID, adjustAcct, adjDr, adjCr, now); jErr != nil {
				return jErr
			}
			// Balance updates (both accounts debit-normal): += (debit - credit)
			if _, jErr := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", stockDr-stockCr, now, stockAcct); jErr != nil {
				return jErr
			}
			if _, jErr := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", adjDr-adjCr, now, adjustAcct); jErr != nil {
				return jErr
			}
			if _, jErr := tx.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID); jErr != nil {
				return jErr
			}
			if _, jErr := tx.Exec("UPDATE stock_counts SET adjustment_journal_id = $1 WHERE id = $2", entryID, countID); jErr != nil {
				return jErr
			}
		}

		// Totals + completed status — in the same tx, so a retry after any
		// failure re-runs everything from a clean state.
		if _, uErr := tx.Exec(`
			UPDATE stock_counts SET
				status = 'completed', completed_at = $1, completed_by = $2,
				total_system_value = COALESCE((SELECT SUM(COALESCE(system_value,0)) FROM stock_count_lines WHERE stock_count_id = $3), 0),
				total_counted_value = COALESCE((SELECT SUM(COALESCE(counted_value, counted_quantity * COALESCE(unit_cost,0), 0)) FROM stock_count_lines WHERE stock_count_id = $3), 0),
				total_variance_value = COALESCE((SELECT SUM(ABS(variance_quantity) * COALESCE(unit_cost,0)) FROM stock_count_lines WHERE stock_count_id = $3 AND variance_quantity != 0), 0),
				updated_at = $1
			WHERE id = $3 AND status <> 'completed'
		`, now, userID, countID); uErr != nil {
			return uErr
		}
		if _, uErr := tx.Exec("UPDATE stock_count_lines SET status = 'adjusted' WHERE stock_count_id = $1 AND variance_quantity != 0 AND counted_quantity IS NOT NULL", countID); uErr != nil {
			return uErr
		}
		return tx.Commit()
	}()
	if completeErr != nil {
		h.log.Error("Failed to complete stock count atomically", "error", completeErr, "count_id", countID)
		response.InternalError(c, "Failed to complete stock count")
		return
	}

	// Post-commit: notify workflow rules about the applied variances
	for _, line := range lines {
		var bal float64
		_ = h.db.QueryRow(`SELECT quantity_on_hand FROM inventory WHERE tenant_id=$1 AND product_id=$2 AND warehouse_id=$3`,
			tenantID, line.ProductID, warehouseID).Scan(&bal)
		h.emitInventoryAdjusted(tenantID, line.ProductID, line.Variance, bal)
	}

	c.Params = append(c.Params, gin.Param{Key: "id", Value: countID.String()})
	h.GetStockCount(c)
}

func (h *Handler) DeleteStockCount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid stock count ID")
		return
	}

	var status string
	err = h.db.QueryRow("SELECT status FROM stock_counts WHERE id=$1 AND tenant_id=$2", id, tenantID).Scan(&status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock count")
		return
	}
	if status == "completed" {
		response.BadRequest(c, "Cannot delete a completed stock count")
		return
	}

	// Delete lines first, then header
	if _, execErr := h.db.Exec("DELETE FROM stock_count_lines WHERE stock_count_id = $1", id); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "DELETE stock_count_lines", "error", execErr)
	}
	if _, execErr := h.db.Exec("DELETE FROM stock_counts WHERE id = $1 AND tenant_id = $2", id, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "DELETE stock_counts", "error", execErr)
	}

	response.NoContent(c)
}

// ─── Stock Operations TT Handlers ─────────────────────────────────────────────

// ListStockOperations returns stock operations filtered by direction and state
func (h *Handler) ListStockOperations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, hasOrg := middleware.GetOrganizationID(c)

	direction := c.Query("direction") // receipt, delivery, internal, write_off
	state := c.Query("state")         // draft, in_progress, waiting, done, cancelled
	partnerID := c.Query("partner_id")
	limit := 50

	query := `
		SELECT so.id, so.name, so.direction, so.date, so.scheduled_date,
		       so.state, so.current_step, so.total_steps, so.priority,
		       so.source_document, so.note, so.write_off_reason,
		       so.operation_type_id, wot.name as operation_type_name,
		       so.partner_id, COALESCE(c.name, cp.name) as partner_name,
		       COALESCE(w.name, '') as warehouse_name,
		       so.created_at, so.updated_at
		FROM stock_operations so
		LEFT JOIN warehouse_operation_types wot ON so.operation_type_id = wot.id
		LEFT JOIN warehouses w ON wot.warehouse_id = w.id
		LEFT JOIN contacts c ON so.partner_id = c.id
		LEFT JOIN construction_material_requests cmr ON so.source_type = 'material_request' AND cmr.request_number = so.source_document
		LEFT JOIN construction_projects cp ON cp.id = cmr.project_id
		WHERE so.tenant_id = $1 AND so.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argN := 1

	if hasOrg && organizationID != uuid.Nil {
		argN++
		query += fmt.Sprintf(" AND so.organization_id = $%d", argN)
		args = append(args, organizationID)
	}

	if direction != "" {
		if direction == "delivery" {
			// Include pick/pack (internal) operations that belong to the delivery chain
			argN++
			query += fmt.Sprintf(` AND (so.direction = $%d OR (so.direction = 'internal' AND so.source_type = 'sales_order'
				AND EXISTS (SELECT 1 FROM warehouse_operation_types wt WHERE wt.id = so.operation_type_id AND wt.type IN ('pick','pack'))))`, argN)
			args = append(args, direction)
		} else {
			argN++
			query += fmt.Sprintf(" AND so.direction = $%d", argN)
			args = append(args, direction)
		}
	}
	if state != "" {
		argN++
		query += fmt.Sprintf(" AND so.state = $%d", argN)
		args = append(args, state)
	}
	if partnerID != "" {
		pid, err := uuid.Parse(partnerID)
		if err == nil {
			argN++
			query += fmt.Sprintf(" AND so.partner_id = $%d", argN)
			args = append(args, pid)
		}
	}

	query += fmt.Sprintf(" ORDER BY so.created_at DESC LIMIT %d", limit)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list stock operations", "error", err)
		response.InternalError(c, "Failed to list stock operations")
		return
	}
	defer rows.Close()

	ops := make([]*entity.StockOperation, 0)
	for rows.Next() {
		var op entity.StockOperation
		var scheduledDate sql.NullTime
		var partnerIDNull, opTypeIDNull sql.NullString
		var partnerName, opTypeName, sourceDoc, note, writeOffReason, whName sql.NullString

		err := rows.Scan(
			&op.ID, &op.Name, &op.Direction, &op.Date, &scheduledDate,
			&op.State, &op.CurrentStep, &op.TotalSteps, &op.Priority,
			&sourceDoc, &note, &writeOffReason,
			&opTypeIDNull, &opTypeName,
			&partnerIDNull, &partnerName,
			&whName,
			&op.CreatedAt, &op.UpdatedAt,
		)
		if err != nil {
			continue
		}
		if scheduledDate.Valid {
			t := scheduledDate.Time
			op.ScheduledDate = &t
		}
		if partnerName.Valid {
			op.PartnerName = partnerName.String
		}
		if whName.Valid {
			op.WarehouseName = whName.String
		}
		if sourceDoc.Valid {
			op.SourceDocument = sourceDoc.String
		}
		if note.Valid {
			op.Note = note.String
		}
		if writeOffReason.Valid {
			op.WriteOffReason = writeOffReason.String
		}
		ops = append(ops, &op)
	}

	response.Success(c, ops)
}

// GetStockOperation returns a single stock operation with lines and step logs
func (h *Handler) GetStockOperation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var op entity.StockOperation
	var scheduledDate sql.NullTime
	var partnerName, sourceDoc, note, writeOffReason, opTypeName sql.NullString

	var warehouseName, carrierName, deliveryAddress, trackingNumber sql.NullString
	err = h.db.QueryRow(`
		SELECT so.id, so.name, so.direction, so.date, so.scheduled_date,
		       so.state, so.current_step, so.total_steps, so.priority,
		       so.source_document, so.note, so.write_off_reason,
		       so.operation_type_id, wot.name,
		       so.partner_id,
		       COALESCE(c.name, cp.name) as partner_name,
		       COALESCE(w.name, '') as warehouse_name,
		       so.carrier_id, COALESCE(cr.name, '') as carrier_name,
		       so.delivery_address, so.tracking_number,
		       so.created_at, so.updated_at
		FROM stock_operations so
		LEFT JOIN warehouse_operation_types wot ON so.operation_type_id = wot.id
		LEFT JOIN warehouses w ON wot.warehouse_id = w.id
		LEFT JOIN contacts c ON so.partner_id = c.id
		LEFT JOIN carriers cr ON so.carrier_id = cr.id
		LEFT JOIN construction_material_requests cmr ON so.source_type = 'material_request' AND cmr.request_number = so.source_document
		LEFT JOIN construction_projects cp ON cp.id = cmr.project_id
		WHERE so.id = $1 AND so.tenant_id = $2 AND so.deleted_at IS NULL
	`, id, tenantID).Scan(
		&op.ID, &op.Name, &op.Direction, &op.Date, &scheduledDate,
		&op.State, &op.CurrentStep, &op.TotalSteps, &op.Priority,
		&sourceDoc, &note, &writeOffReason,
		&op.OperationTypeID, &opTypeName,
		&op.PartnerID,
		&partnerName,
		&warehouseName,
		&op.CarrierID, &carrierName,
		&deliveryAddress, &trackingNumber,
		&op.CreatedAt, &op.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to get stock operation")
		return
	}
	if scheduledDate.Valid {
		t := scheduledDate.Time
		op.ScheduledDate = &t
	}
	if partnerName.Valid {
		op.PartnerName = partnerName.String
	}
	if warehouseName.Valid {
		op.WarehouseName = warehouseName.String
	}
	if sourceDoc.Valid {
		op.SourceDocument = sourceDoc.String
	}
	if note.Valid {
		op.Note = note.String
	}
	if writeOffReason.Valid {
		op.WriteOffReason = writeOffReason.String
	}
	if deliveryAddress.Valid {
		op.DeliveryAddress = deliveryAddress.String
	}
	if trackingNumber.Valid {
		op.TrackingNumber = trackingNumber.String
	}

	// Load lines
	lineRows, err := h.db.Query(`
		SELECT sol.id, sol.product_id, p.name, p.sku,
		       sol.expected_qty, sol.done_qty, sol.uom,
		       sol.unit_price, sol.lot_number, sol.expiry_date,
		       sol.quality_status, sol.write_off_reason, sol.note, sol.created_at
		FROM stock_operation_lines sol
		LEFT JOIN products p ON sol.product_id = p.id
		WHERE sol.operation_id = $1 AND sol.tenant_id = $2
		ORDER BY sol.created_at
	`, id, tenantID)
	if err == nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var line entity.StockOperationLine
			var productName, productCode, lotNum, expiryDate, writeOffR, lineNote sql.NullString
			var unitPrice sql.NullFloat64
			var expiryStr sql.NullString
			_ = expiryStr
			err := lineRows.Scan(
				&line.ID, &line.ProductID, &productName, &productCode,
				&line.ExpectedQty, &line.DoneQty, &line.UOM,
				&unitPrice, &lotNum, &expiryDate,
				&line.QualityStatus, &writeOffR, &lineNote, &line.CreatedAt,
			)
			if err != nil {
				continue
			}
			if productName.Valid {
				line.ProductName = productName.String
			}
			if productCode.Valid {
				line.ProductCode = productCode.String
			}
			if lotNum.Valid {
				line.LotNumber = lotNum.String
			}
			if expiryDate.Valid {
				s := expiryDate.String
				line.ExpiryDate = &s
			}
			if unitPrice.Valid {
				line.UnitPrice = &unitPrice.Float64
			}
			if writeOffR.Valid {
				line.WriteOffReason = writeOffR.String
			}
			if lineNote.Valid {
				line.Note = lineNote.String
			}
			line.TenantID = tenantID
			line.OperationID = id
			op.Lines = append(op.Lines, line)
		}
	}

	// Load step logs
	logRows, err := h.db.Query(`
		SELECT id, step_id, step_sequence, step_name, state,
		       started_at, completed_at, started_by, completed_by,
		       approved_by, rejection_reason, note, created_at
		FROM stock_operation_step_log
		WHERE operation_id = $1 AND tenant_id = $2
		ORDER BY step_sequence
	`, id, tenantID)
	if err == nil {
		defer logRows.Close()
		for logRows.Next() {
			var sl entity.StockOperationStepLog
			var stepID sql.NullString
			var startedAt, completedAt sql.NullTime
			var startedBy, completedBy, approvedBy sql.NullString
			var rejReason, slNote, stepName sql.NullString
			err := logRows.Scan(
				&sl.ID, &stepID, &sl.StepSequence, &stepName, &sl.State,
				&startedAt, &completedAt, &startedBy, &completedBy,
				&approvedBy, &rejReason, &slNote, &sl.CreatedAt,
			)
			if err != nil {
				continue
			}
			if stepName.Valid {
				sl.StepName = stepName.String
			}
			if startedAt.Valid {
				t := startedAt.Time
				sl.StartedAt = &t
			}
			if completedAt.Valid {
				t := completedAt.Time
				sl.CompletedAt = &t
			}
			if rejReason.Valid {
				sl.RejectionReason = rejReason.String
			}
			if slNote.Valid {
				sl.Note = slNote.String
			}
			sl.Documents = []string{}
			sl.TenantID = tenantID
			sl.OperationID = id
			op.StepLogs = append(op.StepLogs, sl)
		}
	}

	response.Success(c, op)
}

// nextStockOperationName generates the next sequential operation name
func (h *Handler) nextStockOperationName(tenantID uuid.UUID, direction string) string {
	year := time.Now().Year()
	prefix := map[string]string{
		"receipt":   "REC",
		"delivery":  "DEL",
		"internal":  "INT",
		"write_off": "WO",
	}[direction]
	if prefix == "" {
		prefix = "OP"
	}

	var lastNum int
	h.db.QueryRow(`
		INSERT INTO stock_operation_sequences (tenant_id, direction, year, last_number)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (tenant_id, direction, year)
		DO UPDATE SET last_number = stock_operation_sequences.last_number + 1
		RETURNING last_number
	`, tenantID, direction, year).Scan(&lastNum)

	return fmt.Sprintf("%s-%d-%05d", prefix, year, lastNum)
}

// CreateStockOperation creates a new stock operation with optional lines
func (h *Handler) CreateStockOperation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateStockOperationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	opTypeID, err := uuid.Parse(input.OperationTypeID)
	if err != nil {
		response.BadRequest(c, "Invalid operation_type_id")
		return
	}

	// Count steps configured for this operation type
	var totalSteps int
	h.db.QueryRow("SELECT COUNT(*) FROM operation_type_steps WHERE operation_type_id = $1 AND tenant_id = $2", opTypeID, tenantID).Scan(&totalSteps)
	if totalSteps == 0 {
		totalSteps = 1
	}

	id := uuid.New()
	name := h.nextStockOperationName(tenantID, input.Direction)
	now := time.Now()
	priority := input.Priority
	if priority == "" {
		priority = "normal"
	}

	var partnerID *uuid.UUID
	if input.PartnerID != "" {
		pid, err := uuid.Parse(input.PartnerID)
		if err == nil {
			partnerID = &pid
		}
	}

	var writeOffReason *string
	if input.WriteOffReason != "" {
		writeOffReason = &input.WriteOffReason
	}

	var carrierID *uuid.UUID
	if input.CarrierID != "" {
		cid, err := uuid.Parse(input.CarrierID)
		if err == nil {
			carrierID = &cid
		}
	}

	_, err = h.db.Exec(`
		INSERT INTO stock_operations (
			id, tenant_id, name, operation_type_id, direction,
			date, partner_id, source_document,
			state, current_step, total_steps, priority,
			note, write_off_reason,
			carrier_id, delivery_address, tracking_number,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'draft',1,$9,$10,$11,$12,$13,$14,$15,$16,$16)
	`,
		id, tenantID, name, opTypeID, input.Direction,
		now, partnerID, input.SourceDocument,
		totalSteps, priority,
		input.Note, writeOffReason,
		carrierID, input.DeliveryAddress, input.TrackingNumber,
		now,
	)
	if err != nil {
		h.log.Error("Failed to create stock operation", "error", err)
		response.InternalError(c, "Failed to create stock operation")
		return
	}

	// Create lines
	for _, l := range input.Lines {
		productID, err := uuid.Parse(l.ProductID)
		if err != nil {
			continue
		}
		uom := l.UOM
		if uom == "" {
			uom = "unit"
		}
		qs := l.QualityStatus
		if qs == "" {
			qs = "good"
		}
		if _, execErr := h.db.Exec(`
			INSERT INTO stock_operation_lines (
				id, tenant_id, operation_id, product_id,
				expected_qty, done_qty, uom, unit_price,
				lot_number, expiry_date, quality_status,
				write_off_reason, note, created_at, updated_at
			) VALUES (uuid_generate_v4(),$1,$2,$3,$4,$5,$6,$7,$8,
			  NULLIF($9,'')::date,$10,$11,$12,$13,$13)
		`,
			tenantID, id, productID,
			l.ExpectedQty, l.DoneQty, uom, l.UnitPrice,
			l.LotNumber, l.ExpiryDate, qs,
			l.WriteOffReason, l.Note, now,
		); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT stock_operation_lines", "error", execErr)
		}
	}

	// Create initial step log for step 1
	firstStepName := "Step 1"
	var firstStep struct {
		ID   uuid.UUID
		Name string
	}
	err = h.db.QueryRow(`
		SELECT id, name FROM operation_type_steps
		WHERE operation_type_id = $1 AND tenant_id = $2
		ORDER BY sequence LIMIT 1
	`, opTypeID, tenantID).Scan(&firstStep.ID, &firstStep.Name)
	if err == nil {
		firstStepName = firstStep.Name
	}

	if _, execErr := h.db.Exec(`
		INSERT INTO stock_operation_step_log (
			id, tenant_id, operation_id, step_id, step_sequence, step_name, state, created_at
		) VALUES (uuid_generate_v4(),$1,$2,$3,1,$4,'ready',$5)
	`, tenantID, id, firstStep.ID, firstStepName, now); execErr != nil {

		h.log.Error("write failed (was silently discarded)", "stmt", "INSERT stock_operation_step_log", "error", execErr)

	}

	response.Created(c, map[string]interface{}{
		"id":   id,
		"name": name,
	})
}

// AdvanceStockOperationStep marks current step as done and moves to next step
func (h *Handler) AdvanceStockOperationStep(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	// Parse optional force flag for over-receipt confirmation
	var advanceInput struct {
		Force bool `json:"force"`
	}
	c.ShouldBindJSON(&advanceInput)

	userID, _ := middleware.GetUserID(c)

	var op struct {
		CurrentStep int
		TotalSteps  int
		State       string
		Direction   string
		OpTypeID    uuid.UUID
		OrgID       *uuid.UUID
		SourceType  *string
		SourceID    *uuid.UUID
	}
	err = h.db.QueryRow(`
		SELECT current_step, total_steps, state, direction, operation_type_id, organization_id, source_type, source_id
		FROM stock_operations WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&op.CurrentStep, &op.TotalSteps, &op.State, &op.Direction, &op.OpTypeID, &op.OrgID, &op.SourceType, &op.SourceID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if err != nil {
		h.log.Error("Failed to query stock operation", "error", err)
		response.InternalError(c, "Failed to query stock operation")
		return
	}
	if op.State == "done" || op.State == "cancelled" {
		response.BadRequest(c, "Operation is already "+op.State)
		return
	}

	now := time.Now()
	nextStep := op.CurrentStep + 1
	isLastStep := nextStep > op.TotalSteps

	// Pre-validate stock availability for delivery operations on the final step
	if isLastStep {
		var warehouseIDCheck uuid.UUID
		h.db.QueryRow(`
			SELECT wot.warehouse_id
			FROM warehouse_operation_types wot
			JOIN stock_operations so ON so.operation_type_id = wot.id
			WHERE so.id=$1 AND so.tenant_id=$2
		`, id, tenantID).Scan(&warehouseIDCheck)

		if (op.Direction == "delivery" || op.Direction == "write_off") && warehouseIDCheck != uuid.Nil {
			type insufficientItem struct {
				ProductName string  `json:"product_name"`
				Available   float64 `json:"available"`
				Requested   float64 `json:"requested"`
			}
			var insufficientItems []insufficientItem

			checkLines, _ := h.db.Query(`
				SELECT sol.product_id, sol.done_qty, COALESCE(p.name, 'Unknown')
				FROM stock_operation_lines sol
				JOIN products p ON p.id = sol.product_id AND p.tenant_id = sol.tenant_id
				WHERE sol.operation_id = $1 AND sol.tenant_id = $2 AND sol.done_qty > 0
			`, id, tenantID)
			if checkLines != nil {
				defer checkLines.Close()
				for checkLines.Next() {
					var prodID uuid.UUID
					var doneQty float64
					var productName string
					if err := checkLines.Scan(&prodID, &doneQty, &productName); err != nil {
						continue
					}
					// Availability is checked against the operation's warehouse
					// ONLY — the old tenant-wide fallback let a delivery drain
					// a warehouse it had no stock in (audit finding #7).
					var qtyAvailable float64
					h.db.QueryRow(`
						SELECT COALESCE(SUM(quantity_on_hand), 0)
						FROM inventory
						WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
					`, tenantID, prodID, warehouseIDCheck).Scan(&qtyAvailable)

					if qtyAvailable < doneQty {
						insufficientItems = append(insufficientItems, insufficientItem{
							ProductName: productName,
							Available:   qtyAvailable,
							Requested:   doneQty,
						})
					}
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
		}
	}

	// Check quantity discrepancies on the last step (over-receipt / under-receipt warnings)
	if isLastStep && (op.Direction == "receipt" || op.Direction == "internal") {
		type discrepancyItem struct {
			ProductName string  `json:"product_name"`
			ExpectedQty float64 `json:"expected_qty"`
			DoneQty     float64 `json:"done_qty"`
			Difference  float64 `json:"difference"`
		}
		var overReceiptItems []discrepancyItem
		var underReceiptItems []discrepancyItem

		discLines, _ := h.db.Query(`
			SELECT COALESCE(p.name, 'Unknown'), sol.expected_qty, sol.done_qty
			FROM stock_operation_lines sol
			LEFT JOIN products p ON p.id = sol.product_id AND p.tenant_id = sol.tenant_id
			WHERE sol.operation_id = $1 AND sol.tenant_id = $2 AND sol.expected_qty > 0
		`, id, tenantID)
		if discLines != nil {
			defer discLines.Close()
			for discLines.Next() {
				var productName string
				var expectedQty, doneQty float64
				if err := discLines.Scan(&productName, &expectedQty, &doneQty); err != nil {
					continue
				}
				if doneQty > expectedQty {
					overReceiptItems = append(overReceiptItems, discrepancyItem{
						ProductName: productName,
						ExpectedQty: expectedQty,
						DoneQty:     doneQty,
						Difference:  doneQty - expectedQty,
					})
				} else if doneQty < expectedQty {
					underReceiptItems = append(underReceiptItems, discrepancyItem{
						ProductName: productName,
						ExpectedQty: expectedQty,
						DoneQty:     doneQty,
						Difference:  expectedQty - doneQty,
					})
				}
			}
		}

		// Over-receipt requires explicit force confirmation
		if len(overReceiptItems) > 0 && !advanceInput.Force {
			c.JSON(422, gin.H{
				"success":              false,
				"message":              "Over-receipt detected. Confirm to proceed.",
				"over_receipt_warning": true,
				"over_receipt_items":   overReceiptItems,
				"under_receipt_items":  underReceiptItems,
			})
			return
		}
	}

	// Check if step is timeout-blocked (GAP 3)
	var timeoutBlocked bool
	h.db.QueryRow(`
		SELECT COALESCE(timeout_blocked, false)
		FROM stock_operation_step_log
		WHERE operation_id=$1 AND step_sequence=$2 AND tenant_id=$3
	`, id, op.CurrentStep, tenantID).Scan(&timeoutBlocked)
	if timeoutBlocked {
		c.JSON(403, gin.H{
			"success": false,
			"message": "Step timed out and is blocked. Manager override required.",
		})
		return
	}

	// Check step configuration for required documents and approval
	var stepRequiresApproval bool
	var stepApprovalRole string
	var requiredDocsJSON []byte
	h.db.QueryRow(`
		SELECT COALESCE(requires_approval, false), COALESCE(approval_role, ''),
		       COALESCE(required_documents, '[]'::jsonb)
		FROM operation_type_steps
		WHERE operation_type_id = $1 AND sequence = $2 AND tenant_id = $3
	`, op.OpTypeID, op.CurrentStep, tenantID).Scan(&stepRequiresApproval, &stepApprovalRole, &requiredDocsJSON)

	// Enforce required documents before allowing step completion
	if len(requiredDocsJSON) > 0 && string(requiredDocsJSON) != "[]" && op.State != "awaiting_approval" {
		var requiredDocs []string
		if err := json.Unmarshal(requiredDocsJSON, &requiredDocs); err == nil && len(requiredDocs) > 0 {
			var attachedDocsJSON []byte
			h.db.QueryRow(`
				SELECT COALESCE(documents, '[]'::jsonb)
				FROM stock_operation_step_log
				WHERE operation_id=$1 AND step_sequence=$2 AND tenant_id=$3
			`, id, op.CurrentStep, tenantID).Scan(&attachedDocsJSON)

			var attachedDocs []map[string]interface{}
			json.Unmarshal(attachedDocsJSON, &attachedDocs)

			attachedTypes := make(map[string]bool)
			for _, doc := range attachedDocs {
				if t, ok := doc["type"].(string); ok {
					attachedTypes[t] = true
				}
			}

			var missingDocs []string
			for _, req := range requiredDocs {
				if !attachedTypes[req] {
					missingDocs = append(missingDocs, req)
				}
			}
			if len(missingDocs) > 0 {
				c.JSON(422, gin.H{
					"success":           false,
					"message":           "Required documents are missing",
					"missing_documents": missingDocs,
				})
				return
			}
		}
	}

	if stepRequiresApproval && op.State != "awaiting_approval" {
		// Set step to awaiting_approval instead of completed
		if _, execErr := h.db.Exec(`
			UPDATE stock_operation_step_log
			SET state='awaiting_approval', completed_at=NULL, completed_by=NULL
			WHERE operation_id=$1 AND step_sequence=$2 AND tenant_id=$3
		`, id, op.CurrentStep, tenantID); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_step_log", "error", execErr)
		}
		if _, execErr := h.db.Exec(`
			UPDATE stock_operations
			SET state='awaiting_approval', updated_at=$1
			WHERE id=$2 AND tenant_id=$3
		`, now, id, tenantID); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
		}
		response.Success(c, gin.H{
			"state":             "awaiting_approval",
			"current_step":      op.CurrentStep,
			"approval_required": true,
			"approval_role":     stepApprovalRole,
		})
		return
	}

	// Mark current step as completed
	if _, execErr := h.db.Exec(`
		UPDATE stock_operation_step_log
		SET state='completed', completed_at=$1, completed_by=$2
		WHERE operation_id=$3 AND step_sequence=$4 AND tenant_id=$5
	`, now, userID, id, op.CurrentStep, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_step_log", "error", execErr)
	}

	var newState string
	if isLastStep {
		// Received quantity defaults to the expected quantity.
		//
		// THE BUG THIS FIXES: every line of the stock apply below is filtered
		// by `done_qty > 0`, and nothing on the server ever set done_qty. The
		// browser patched it in a SEPARATE request immediately before calling
		// this endpoint — and swallowed that request's failure into a
		// console.error. So a receipt whose patch did not land (a failed
		// request, a stale line list, any caller that is not that one screen)
		// completed with status "Bajarildi", reported success, and moved no
		// stock at all. REC-2026-00020: 22 units accepted, 0 on hand.
		//
		// The rule is the same one the browser applied, moved to where it
		// cannot be skipped: a line the user never touched receives what was
		// expected. A user who genuinely received a different amount edits the
		// line first, and done_qty > 0 is then left alone.
		if res, dErr := h.db.Exec(`
			UPDATE stock_operation_lines
			SET done_qty = expected_qty, updated_at = $1
			WHERE operation_id = $2 AND tenant_id = $3
			  AND COALESCE(done_qty, 0) = 0 AND expected_qty > 0
		`, now, id, tenantID); dErr != nil {
			// Surfaced, not logged: continuing here is what produced a
			// completed document with no stock behind it.
			h.log.Error("Failed to default done_qty from expected_qty", "error", dErr, "op_id", id)
			response.InternalError(c, "Failed to prepare operation lines")
			return
		} else if n, _ := res.RowsAffected(); n > 0 {
			h.log.Info("Defaulted done_qty to expected_qty", "op_id", id, "lines", n)
		}

		// Write-off approval rules check (TT §5.3)
		if op.Direction == "write_off" && op.State != "awaiting_approval" {
			var approvalRule string
			var amountThreshold, quantityThreshold *float64
			h.db.QueryRow(`
				SELECT COALESCE(approval_rule, 'never'),
				       approval_amount_threshold, approval_quantity_threshold
				FROM warehouse_operation_types
				WHERE id = $1 AND tenant_id = $2
			`, op.OpTypeID, tenantID).Scan(&approvalRule, &amountThreshold, &quantityThreshold)

			needsApproval := false
			switch approvalRule {
			case "always":
				needsApproval = true
			case "by_amount":
				if amountThreshold != nil {
					var totalAmount float64
					h.db.QueryRow(`
						SELECT COALESCE(SUM(done_qty * COALESCE(unit_price, 0)), 0)
						FROM stock_operation_lines WHERE operation_id=$1 AND tenant_id=$2
					`, id, tenantID).Scan(&totalAmount)
					needsApproval = totalAmount > *amountThreshold
				}
			case "by_quantity":
				if quantityThreshold != nil {
					var totalQty float64
					h.db.QueryRow(`
						SELECT COALESCE(SUM(done_qty), 0)
						FROM stock_operation_lines WHERE operation_id=$1 AND tenant_id=$2
					`, id, tenantID).Scan(&totalQty)
					needsApproval = totalQty > *quantityThreshold
				}
			}

			if needsApproval {
				if _, execErr := h.db.Exec(`
					UPDATE stock_operation_step_log
					SET state='awaiting_approval', completed_at=NULL
					WHERE operation_id=$1 AND step_sequence=$2 AND tenant_id=$3
				`, id, op.CurrentStep, tenantID); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_step_log", "error", execErr)
				}
				if _, execErr := h.db.Exec(`
					UPDATE stock_operations
					SET state='awaiting_approval', updated_at=$1
					WHERE id=$2 AND tenant_id=$3
				`, now, id, tenantID); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
				}
				response.Success(c, gin.H{
					"state":             "awaiting_approval",
					"current_step":      op.CurrentStep,
					"approval_required": true,
					"reason":            "write_off_threshold_exceeded",
				})
				return
			}
		}

		// All steps done. The operation is marked done and the JE posts only
		// AFTER the stock movement below succeeds — the old order marked the
		// document done and touched the GL even when the stock write then
		// failed halfway.
		markDoneAndPostJE := func() {
			newState = "done"
			if _, execErr := h.db.Exec(`
				UPDATE stock_operations
				SET state='done', done_at=$1, updated_at=$1
				WHERE id=$2 AND tenant_id=$3
			`, now, id, tenantID); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
			}

			// Create journal entry if auto_post_accounting is enabled
			var autoPost bool
			var cfgJournalID, cfgDebitAcct, cfgCreditAcct *uuid.UUID
			h.db.QueryRow(`
			SELECT wot.auto_post_accounting, wot.journal_id, wot.debit_account_id, wot.credit_account_id
			FROM warehouse_operation_types wot
			WHERE wot.id = $1 AND wot.tenant_id = $2
		`, op.OpTypeID, tenantID).Scan(&autoPost, &cfgJournalID, &cfgDebitAcct, &cfgCreditAcct)

			// Skip auto-posting for material_request deliveries — accounting happens when materials are used in construction stages
			isMaterialRequestDelivery := op.SourceType != nil && *op.SourceType == "material_request" && op.Direction == "delivery"

			// DOUBLE-COUNT GUARD: a receipt that traces to a purchase order must NOT
			// auto-post Dr 1010 / Cr 6010 here — the purchase invoice (bill) for the
			// same PO posts its own JE, so both firing books the vendor payable twice
			// for the same goods (audit Part B, D-7: REC000547 vs its PO's bill JE).
			// For PO-sourced receipts the bill is THE accounting document; skip.
			isPOReceipt := op.SourceType != nil && *op.SourceType == "purchase_order" && op.Direction == "receipt"
			if isPOReceipt && autoPost {
				h.log.Info("Skipping stock-operation JE for PO-sourced receipt (bill is the accounting document)", "operation_id", id)
			}

			if autoPost && op.Direction != "internal" && !isMaterialRequestDelivery && !isPOReceipt {
				// Calculate total value from operation lines
				var totalValue float64
				h.db.QueryRow(`
				SELECT COALESCE(SUM(done_qty * unit_price), 0)
				FROM stock_operation_lines
				WHERE operation_id=$1 AND tenant_id=$2
			`, id, tenantID).Scan(&totalValue)

				if totalValue > 0 {
					// Find journal
					var journalID uuid.UUID
					var nextNumber int
					if cfgJournalID != nil && *cfgJournalID != uuid.Nil {
						h.db.QueryRow("SELECT id, COALESCE(next_number,1) FROM journals WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", *cfgJournalID, tenantID).Scan(&journalID, &nextNumber)
					}
					if journalID == uuid.Nil {
						h.db.QueryRow(`SELECT id, COALESCE(next_number,1) FROM journals WHERE tenant_id=$1 AND code IN ('STOCK','INVENTORY','MISC','GENERAL') AND deleted_at IS NULL ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'INVENTORY' THEN 1 WHEN 'MISC' THEN 2 ELSE 3 END LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)
					}

					if journalID != uuid.Nil {
						// Find accounts based on direction or configured values
						var debitAcct, creditAcct uuid.UUID
						if cfgDebitAcct != nil && *cfgDebitAcct != uuid.Nil {
							debitAcct = *cfgDebitAcct
						}
						if cfgCreditAcct != nil && *cfgCreditAcct != uuid.Nil {
							creditAcct = *cfgCreditAcct
						}

						// Fallback: auto-detect accounts by direction
						if debitAcct == uuid.Nil || creditAcct == uuid.Nil {
							switch op.Direction {
							case "receipt":
								if debitAcct == uuid.Nil {
									debitAcct = findAccount(h.db, tenantID, op.OrgID, "inventory", "1010")
								}
								if creditAcct == uuid.Nil {
									creditAcct = findAccount(h.db, tenantID, op.OrgID, "accounts payable", "6010")
									if creditAcct == uuid.Nil {
										creditAcct = findAccount(h.db, tenantID, op.OrgID, "vendor", "6010")
									}
								}
							case "delivery":
								if debitAcct == uuid.Nil {
									debitAcct = findAccount(h.db, tenantID, op.OrgID, "cost of goods", "9110")
									if debitAcct == uuid.Nil {
										debitAcct = findAccount(h.db, tenantID, op.OrgID, "cogs", "9110")
									}
								}
								if creditAcct == uuid.Nil {
									creditAcct = findAccount(h.db, tenantID, op.OrgID, "inventory", "1010")
								}
							case "write_off":
								if debitAcct == uuid.Nil {
									debitAcct = findAccount(h.db, tenantID, op.OrgID, "scrap", "9430")
									if debitAcct == uuid.Nil {
										debitAcct = findAccount(h.db, tenantID, op.OrgID, "inventory loss", "9420")
									}
								}
								if creditAcct == uuid.Nil {
									creditAcct = findAccount(h.db, tenantID, op.OrgID, "inventory", "1010")
								}
							}
						}

						if debitAcct != uuid.Nil && creditAcct != uuid.Nil {
							entryID := uuid.New()
							prefixMap := map[string]string{"receipt": "REC", "delivery": "DEL", "write_off": "WO"}
							prefix := prefixMap[op.Direction]
							if prefix == "" {
								prefix = "STK"
							}
							entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

							var opName string
							h.db.QueryRow("SELECT name FROM stock_operations WHERE id=$1", id).Scan(&opName)
							description := fmt.Sprintf("Stock Operation: %s", opName)

							tx, txErr := h.db.Begin()
							if txErr != nil {
								h.log.Error("Failed to begin stock-operation journal tx", "error", txErr)
							} else {
								committed := false
								func() {
									defer func() {
										if !committed {
											tx.Rollback()
										}
									}()

									if _, err := tx.Exec(`
									INSERT INTO journal_entries (
										id, tenant_id, organization_id, journal_id, entry_number, entry_date,
										description, source_type, source_id, status, total_debit, total_credit,
										created_by, created_at, updated_at
									) VALUES ($1, $2, $3, $4, $5, $6, $7, 'stock_operation', $8, 'posted', $9, $9, $10, $11, $11)
								`, entryID, tenantID, op.OrgID, journalID, entryNumber, now,
										description, id.String(), totalValue, userID, now); err != nil {
										h.log.Error("Failed to create stock-operation journal entry", "error", err)
										return
									}
									if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, $5, 0, 1, $6)`,
										uuid.New(), entryID, debitAcct, description, totalValue, now); err != nil {
										h.log.Error("Failed to insert stock-operation debit line", "error", err)
										return
									}
									if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, 0, $5, 2, $6)`,
										uuid.New(), entryID, creditAcct, description, totalValue, now); err != nil {
										h.log.Error("Failed to insert stock-operation credit line", "error", err)
										return
									}
									if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalValue, now, debitAcct); err != nil {
										h.log.Error("Failed to update debit account balance", "error", err)
										return
									}
									if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalValue, now, creditAcct); err != nil {
										h.log.Error("Failed to update credit account balance", "error", err)
										return
									}
									if _, err := tx.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID); err != nil {
										h.log.Error("Failed to bump journal next_number", "error", err)
										return
									}
									if _, err := tx.Exec("UPDATE stock_operations SET accounting_posted = true, journal_entry_id = $1, updated_at = $2 WHERE id = $3", entryID, now, id); err != nil {
										h.log.Error("Failed to mark stock operation posted", "error", err)
										return
									}

									// TT 12.3: Write-offs affect budget — update budget_lines.actual_amount
									// for the debit account (expense account) if tracked in an active budget
									if op.Direction == "write_off" && totalValue > 0 {
										if _, err := tx.Exec(`
										UPDATE budget_lines bl
										SET actual_amount = actual_amount + $1, updated_at = NOW()
										FROM budgets b
										WHERE bl.budget_id = b.id
										  AND b.tenant_id = $2
										  AND b.status = 'approved'
										  AND b.deleted_at IS NULL
										  AND bl.account_id = $3
										  AND (b.start_date IS NULL OR b.start_date <= $4)
										  AND (b.end_date IS NULL OR b.end_date >= $4)
									`, totalValue, tenantID, debitAcct, now); err != nil {
											h.log.Error("Failed to update budget actuals", "error", err)
											return
										}
									}

									if err := tx.Commit(); err != nil {
										h.log.Error("Failed to commit stock-operation journal entry", "error", err)
									} else {
										committed = true
									}
								}()
							}
						}
					}
				}
			}
		}

		// ── Inventory movement: adjust quantity_on_hand based on direction ──
		// All lines of the operation apply in ONE transaction (a document is
		// all-or-nothing; the old per-line h.db.Exec loop could stop halfway
		// and leave balance ≠ ledger — audit finding #1). 'internal' now
		// moves stock too: the old guard excluded it, so internal transfer
		// operations completed without touching inventory (audit finding #2).
		// Skip if the SO's stock already left through another path. Checking only
		// "SO fully shipped" left a hole: a PARTIAL sales-delivery keeps the SO at
		// 'processing', and this legacy stock-op chain would then issue the same
		// goods a second time (audit §2/§6) — so any validated DO also skips.
		skipInventory := false
		if op.SourceType != nil && *op.SourceType == "sales_order" && op.SourceID != nil {
			var soStatus string
			if err := h.db.QueryRow("SELECT status FROM sales_orders WHERE id = $1 AND tenant_id = $2", *op.SourceID, tenantID).Scan(&soStatus); err == nil {
				if soStatus == "shipped" || soStatus == "delivered" {
					skipInventory = true
				}
			}
			if !skipInventory {
				var shippedDOs int
				if err := h.db.QueryRow(`
					SELECT COUNT(*) FROM sales_delivery_orders
					WHERE sales_order_id = $1 AND tenant_id = $2 AND status = 'shipped' AND deleted_at IS NULL`,
					*op.SourceID, tenantID).Scan(&shippedDOs); err == nil && shippedDOs > 0 {
					skipInventory = true
				}
			}
		}

		var warehouseID uuid.UUID
		whErr := h.db.QueryRow(`
			SELECT wot.warehouse_id FROM warehouse_operation_types wot
			WHERE wot.id = $1 AND wot.tenant_id = $2
		`, op.OpTypeID, tenantID).Scan(&warehouseID)

		// Fallback: if op.OrgID is nil, get it from the warehouse
		if op.OrgID == nil && warehouseID != uuid.Nil {
			var whOrgID uuid.UUID
			if err := h.db.QueryRow("SELECT organization_id FROM warehouses WHERE id = $1 AND tenant_id = $2", warehouseID, tenantID).Scan(&whOrgID); err == nil && whOrgID != uuid.Nil {
				op.OrgID = &whOrgID
			}
		}

		// Internal transfers: source/destination warehouses come from the
		// operation's locations; the op-type warehouse is the source fallback.
		var srcWH, dstWH uuid.UUID
		if op.Direction == "internal" {
			h.db.QueryRow(`
				SELECT COALESCE(slw.warehouse_id, $3),
				       COALESCE(dlw.warehouse_id, '00000000-0000-0000-0000-000000000000'::uuid)
				FROM stock_operations so
				LEFT JOIN warehouse_locations slw ON slw.id = so.source_location_id
				LEFT JOIN warehouse_locations dlw ON dlw.id = so.dest_location_id
				WHERE so.id = $1 AND so.tenant_id = $2
			`, id, tenantID, warehouseID).Scan(&srcWH, &dstWH)
		}

		h.log.Info("Stock op inventory check",
			"op_id", id, "direction", op.Direction, "op_type_id", op.OpTypeID,
			"warehouse_id", warehouseID, "warehouse_query_err", whErr,
			"skip_inventory", skipInventory, "org_id", op.OrgID, "src_wh", srcWH, "dst_wh", dstWH)

		movesStock := (warehouseID != uuid.Nil && (op.Direction == "delivery" || op.Direction == "receipt" || op.Direction == "write_off")) ||
			(op.Direction == "internal" && srcWH != uuid.Nil && dstWH != uuid.Nil && srcWH != dstWH)

		// ── A document that cannot move stock must not report success ──
		//
		// Until now an unresolvable warehouse produced nothing but a log line:
		// markDoneAndPostJE() ran regardless, so the operation showed
		// "Bajarildi" with the full quantity while quantity_on_hand never
		// changed. Worse, the document was then 'done' and could not be
		// re-advanced, so the only cure was editing the database by hand.
		//
		// This is the same shape as the done_qty bug fixed earlier: a receipt
		// that completes and moves nothing. The quantity gate is closed now;
		// this closes the warehouse gate behind it.
		expectsStockMove := op.Direction == "receipt" || op.Direction == "delivery" ||
			op.Direction == "write_off" || op.Direction == "internal"
		if !skipInventory && expectsStockMove && !movesStock {
			reason := "operatsiya turiga ombor biriktirilmagan"
			if op.Direction == "internal" {
				reason = "ko'chirish uchun manba yoki qabul qiluvchi ombor aniqlanmadi"
			}
			h.log.Warn("Stock operation cannot move stock — refusing to complete it",
				"op_id", id, "direction", op.Direction, "op_type_id", op.OpTypeID,
				"warehouse_id", warehouseID, "src_wh", srcWH, "dst_wh", dstWH)
			h.rollbackStep(id, op.CurrentStep, tenantID)
			c.JSON(422, gin.H{
				"success": false,
				"message": "Zaxira o'zgarmaydi — " + reason + ". " +
					"Ombor sozlamalarida operatsiya turiga ombor tanlang va qaytadan tasdiqlang.",
			})
			return
		}

		// ── Stock that lands where the reader cannot see it ──
		//
		// Writing succeeds, the ledger balances, and the product page still
		// shows Qoldiq 0, because every read filters on the warehouse's
		// organization. Refusing while the step can still be rolled back is
		// the only outcome that leaves the user something to act on.
		if !skipInventory && movesStock {
			targets := []uuid.UUID{warehouseID}
			if op.Direction == "internal" {
				targets = []uuid.UUID{srcWH, dstWH}
			}
			if why := h.checkStockVisible(c, tenantID, targets...); why != "" {
				h.log.Warn("Stock operation would write invisible stock — refusing",
					"op_id", id, "direction", op.Direction, "reason", why)
				h.rollbackStep(id, op.CurrentStep, tenantID)
				c.JSON(422, gin.H{"success": false, "message": why})
				return
			}
		}

		// Cross-path dedupe for PO receipts — the guard the other two receipt
		// paths (POST /receive, goods-receipt complete) have always run and
		// this one lacked. approvePOAndCreateReceipt pre-fills this operation
		// with the FULL ordered quantity, so validating it after a /receive
		// or a goods receipt added the whole PO to stock a second time. The
		// operation still completes as a paper document; only the physical
		// stock write is skipped.
		if !skipInventory && movesStock && op.Direction == "receipt" &&
			op.SourceType != nil && *op.SourceType == "purchase_order" && op.SourceID != nil {
			if h.poStockReceivedVia(tenantID, *op.SourceID, "purchase_order", "goods_receipt") {
				skipInventory = true
				h.log.Info("stock operation validate: PO stock already received via another path — skipping physical stock",
					"operation_id", id, "purchase_order_id", *op.SourceID)
			}
		}

		type movedLine struct {
			ProdID     uuid.UUID
			Qty        float64
			NewBalance float64
		}
		var movedLines []movedLine

		if !skipInventory && movesStock {
			// Collect lines first — we cannot iterate a result set while
			// issuing other statements on the same tx connection.
			type opMoveLine struct {
				ProdID    uuid.UUID
				DoneQty   float64
				UnitPrice float64
				LotNumber string
				Expiry    sql.NullTime
			}
			var moveLines []opMoveLine
			invLines, qErr := h.db.Query(`
				SELECT product_id, done_qty, COALESCE(unit_price, 0),
				       COALESCE(lot_number, ''), expiry_date
				FROM stock_operation_lines
				WHERE operation_id = $1 AND tenant_id = $2 AND done_qty > 0
			`, id, tenantID)
			if qErr != nil {
				h.log.Error("Failed to load stock operation lines", "error", qErr)
				response.InternalError(c, "Failed to load operation lines")
				return
			}
			for invLines.Next() {
				var l opMoveLine
				if scanErr := invLines.Scan(&l.ProdID, &l.DoneQty, &l.UnitPrice, &l.LotNumber, &l.Expiry); scanErr == nil {
					moveLines = append(moveLines, l)
				}
			}
			invLines.Close()

			// Vendor for lot rows (receipts sourced from POs)
			var vendorID *uuid.UUID
			if op.Direction == "receipt" && op.SourceType != nil && *op.SourceType == "purchase_order" && op.SourceID != nil {
				var vid uuid.UUID
				if err := h.db.QueryRow("SELECT vendor_id FROM purchase_orders WHERE id = $1", *op.SourceID).Scan(&vid); err == nil {
					vendorID = &vid
				}
			}

			moveErr := func() error {
				tx, txErr := h.db.Begin()
				if txErr != nil {
					return txErr
				}
				defer tx.Rollback()

				for _, l := range moveLines {
					switch op.Direction {
					case "receipt":
						newBal, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
							TenantID: tenantID, OrgID: op.OrgID, ProductID: l.ProdID,
							WarehouseID: warehouseID, Qty: l.DoneQty, UnitCost: l.UnitPrice,
							TxType: "receipt", RefType: "stock_operation", RefID: id.String(),
							Notes: "Qabul qilish", ToWH: &warehouseID,
							CreatedBy: userID, When: now,
						})
						if dErr != nil {
							return dErr
						}
						movedLines = append(movedLines, movedLine{l.ProdID, l.DoneQty, newBal})

						// Lot record for FIFO layers
						lotNumber := l.LotNumber
						if lotNumber == "" {
							lotNumber = h.generateLotNumber(tenantID)
						}
						var expDate *time.Time
						if l.Expiry.Valid {
							expDate = &l.Expiry.Time
						}
						if _, lErr := tx.Exec(`
							INSERT INTO inventory_lots (
								id, tenant_id, product_id, warehouse_id, lot_number,
								received_date, expiry_date, initial_quantity, remaining_quantity,
								unit_cost, vendor_id, purchase_order_id, status, created_at, updated_at
							) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9, $10, $11, 'available', $12, $12)
						`, uuid.New(), tenantID, l.ProdID, warehouseID, lotNumber,
							now, expDate, l.DoneQty, l.UnitPrice, vendorID, op.SourceID, now); lErr != nil {
							return lErr
						}

						// FIFO: set cost_price to the OLDEST available lot's cost (not latest purchase)
						fifoCost := l.UnitPrice
						var oldestCost float64
						if tx.QueryRow(`
							SELECT unit_cost FROM inventory_lots
							WHERE tenant_id = $1 AND product_id = $2 AND status = 'available' AND remaining_quantity > 0
							ORDER BY received_date ASC LIMIT 1
						`, tenantID, l.ProdID).Scan(&oldestCost) == nil && oldestCost > 0 {
							fifoCost = oldestCost
						}
						if _, pErr := tx.Exec(`UPDATE products SET cost_price = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4`,
							fifoCost, now, l.ProdID, tenantID); pErr != nil {
							return pErr
						}
						if op.OrgID != nil {
							if _, pErr := tx.Exec(`UPDATE product_organization_settings SET cost_price = $1, updated_at = $2 WHERE product_id = $3 AND organization_id = $4`,
								fifoCost, now, l.ProdID, *op.OrgID); pErr != nil {
								return pErr
							}
						}

					case "delivery", "write_off":
						txType := "issue"
						txNotes := "Yetkazib berish"
						if op.Direction == "write_off" {
							txType = "write_off"
							txNotes = "Hisobdan chiqarish"
						}
						newBal, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
							TenantID: tenantID, OrgID: op.OrgID, ProductID: l.ProdID,
							WarehouseID: warehouseID, Qty: -l.DoneQty, UnitCost: l.UnitPrice,
							TxType: txType, RefType: "stock_operation", RefID: id.String(),
							Notes: txNotes, FromWH: &warehouseID,
							CreatedBy: userID, When: now,
						})
						if dErr != nil {
							return dErr
						}
						movedLines = append(movedLines, movedLine{l.ProdID, -l.DoneQty, newBal})

					case "internal":
						// Two legs, one tx: stock can never sit in neither
						// warehouse nor be double-counted.
						newBal, cost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
							TenantID: tenantID, OrgID: op.OrgID, ProductID: l.ProdID,
							WarehouseID: srcWH, Qty: -l.DoneQty, UnitCost: 0,
							TxType: "transfer", RefType: "stock_operation", RefID: id.String(),
							Notes: "Ichki ko'chirish (chiqim)", FromWH: &srcWH, ToWH: &dstWH,
							CreatedBy: userID, When: now,
						})
						if dErr != nil {
							return dErr
						}
						if _, _, dErr = h.applyStockDelta(tx, stockDeltaArgs{
							TenantID: tenantID, OrgID: op.OrgID, ProductID: l.ProdID,
							WarehouseID: dstWH, Qty: l.DoneQty, UnitCost: cost,
							TxType: "transfer", RefType: "stock_operation", RefID: id.String(),
							Notes: "Ichki ko'chirish (kirim)", FromWH: &srcWH, ToWH: &dstWH,
							CreatedBy: userID, When: now,
						}); dErr != nil {
							return dErr
						}
						movedLines = append(movedLines, movedLine{l.ProdID, -l.DoneQty, newBal})
					}
				}
				return tx.Commit()
			}()

			if moveErr != nil {
				// Put the step back so the operation can be re-advanced after
				// the stock problem is fixed.
				h.rollbackStep(id, op.CurrentStep, tenantID)
				if errors.Is(moveErr, errInsufficientStock) {
					c.JSON(422, gin.H{
						"success": false,
						"message": "Insufficient stock for this operation",
					})
					return
				}
				h.log.Error("Failed to apply stock movement for operation", "error", moveErr, "op_id", id)
				response.InternalError(c, "Failed to apply stock movement")
				return
			}

			// Post-commit: workflow events for rules bound to inventory.adjusted
			for _, ml := range movedLines {
				h.emitInventoryAdjusted(tenantID, ml.ProdID, ml.Qty, ml.NewBalance)
			}
		}

		// Stock applied (or nothing to move) — now the operation may be
		// marked done and the auto-post JE created.
		markDoneAndPostJE()

		// Sync: when a receipt operation from a PO completes, mark PO as received
		if op.SourceType != nil && *op.SourceType == "purchase_order" && op.SourceID != nil && *op.SourceID != uuid.Nil {
			// Mirror this operation's received quantities onto the PO lines.
			// Accumulate, don't assign: `SET quantity_received = done_qty` made
			// the newest receipt operation ERASE every earlier one (receive 5,
			// then 3 → the PO showed 3, flipped back from received to partial).
			// Aggregated per product before the UPDATE..FROM (one arbitrary
			// source row per target otherwise), and capped at the ordered
			// quantity like the goods-receipt path.
			if _, execErr := h.db.Exec(`
				UPDATE purchase_order_lines pol
				SET quantity_received = LEAST(COALESCE(pol.quantity_received, 0) + agg.done, pol.quantity),
				    updated_at = $3
				FROM (
					SELECT product_id, SUM(done_qty) AS done
					FROM stock_operation_lines
					WHERE operation_id = $1 AND tenant_id = $2 AND done_qty > 0
					GROUP BY product_id
				) agg
				WHERE pol.purchase_order_id = $4 AND pol.product_id = agg.product_id
			`, id, tenantID, now, *op.SourceID); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE purchase_order_lines", "error", execErr)
			}

			// Check if PO is fully received
			var totalQty, totalReceived float64
			h.db.QueryRow(`
				SELECT COALESCE(SUM(quantity), 0), COALESCE(SUM(quantity_received), 0)
				FROM purchase_order_lines WHERE purchase_order_id = $1
			`, *op.SourceID).Scan(&totalQty, &totalReceived)

			poStatus := "partial"
			if totalReceived >= totalQty {
				poStatus = "received"
			}
			if _, execErr := h.db.Exec(`
				UPDATE purchase_orders SET status = $1, updated_at = $2
				WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
			`, poStatus, now, *op.SourceID, tenantID); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE purchase_orders", "error", execErr)
			}

			// Intercompany: update linked SO to "delivered" when PO is fully received
			if poStatus == "received" {
				var linkedSOID *uuid.UUID
				if link, linkErr := h.icSync.GetLinkedDocumentReverse(tenantID, "purchase_order", *op.SourceID); linkErr == nil && link != nil && link.SourceDocumentType == "sale_order" {
					linkedSOID = &link.SourceDocumentID
				} else if link, linkErr := h.icSync.GetLinkedDocument(tenantID, "purchase_order", *op.SourceID); linkErr == nil && link != nil && link.LinkedDocumentType == "sale_order" {
					linkedSOID = &link.LinkedDocumentID
				}
				if linkedSOID != nil {
					if _, execErr := h.db.Exec(`
						UPDATE sales_orders SET status = 'delivered', updated_at = $1
						WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status != 'delivered'
					`, now, *linkedSOID, tenantID); execErr != nil {
						h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE sales_orders", "error", execErr)
					}

					// Also complete the delivery stock operation linked to this SO
					h.completeLinkedDeliveryOp(tenantID, *linkedSOID, now)
					h.log.Info("Intercompany: updated linked SO to delivered", "po_id", *op.SourceID, "so_id", *linkedSOID)
				}
			}
		}

		// Sync: when a delivery operation from a SO completes, mark SO as delivered
		if op.SourceType != nil && *op.SourceType == "sales_order" && op.SourceID != nil && *op.SourceID != uuid.Nil {
			res, soUpdErr := h.db.Exec(`
				UPDATE sales_orders SET status = 'delivered', updated_at = $1
				WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status != 'delivered'
			`, now, *op.SourceID, tenantID)
			if soUpdErr != nil {
				h.log.Error("Failed to update SO to delivered", "error", soUpdErr, "so_id", *op.SourceID)
			} else if rows, _ := res.RowsAffected(); rows == 0 {
				h.log.Warn("SO not updated to delivered (0 rows affected)", "so_id", *op.SourceID, "tenant_id", tenantID)
			} else {
				h.log.Info("SO marked as delivered", "so_id", *op.SourceID, "rows", rows)
			}

			// Intercompany: when SO delivery completes, update linked PO and its receipt
			var linkedPOID *uuid.UUID
			if link, linkErr := h.icSync.GetLinkedDocument(tenantID, "sale_order", *op.SourceID); linkErr == nil && link != nil && link.LinkedDocumentType == "purchase_order" {
				linkedPOID = &link.LinkedDocumentID
			} else if link, linkErr := h.icSync.GetLinkedDocumentReverse(tenantID, "sale_order", *op.SourceID); linkErr == nil && link != nil && link.SourceDocumentType == "purchase_order" {
				linkedPOID = &link.SourceDocumentID
			}
			if linkedPOID != nil {
				// Always update PO status to received
				if _, execErr := h.db.Exec(`
					UPDATE purchase_orders SET status = 'received', updated_at = $1
					WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
				`, now, *linkedPOID, tenantID); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE purchase_orders", "error", execErr)
				}
				// Try to complete receipt stock op + inventory if it exists
				h.completeLinkedReceiptOp(tenantID, *linkedPOID, now)
				h.log.Info("Intercompany: SO delivery done, updated linked PO", "so_id", *op.SourceID, "po_id", *linkedPOID)
			}
		}

		// Chain: when a pick/pack/delivery op for a SO completes, activate the next waiting operation in the chain
		if op.SourceType != nil && *op.SourceType == "sales_order" && op.SourceID != nil && *op.SourceID != uuid.Nil {
			// Find the next waiting operation for this SO (ordered by operation type sequence)
			var nextOpID uuid.UUID
			err := h.db.QueryRow(`
				SELECT so2.id FROM stock_operations so2
				JOIN warehouse_operation_types wot ON so2.operation_type_id = wot.id
				WHERE so2.source_type = 'sales_order' AND so2.source_id = $1
				  AND so2.tenant_id = $2 AND so2.state = 'waiting'
				  AND so2.deleted_at IS NULL
				ORDER BY wot.sequence ASC LIMIT 1
			`, *op.SourceID, tenantID).Scan(&nextOpID)
			if err == nil && nextOpID != uuid.Nil {
				if _, execErr := h.db.Exec(`
					UPDATE stock_operations SET state = 'draft', updated_at = $1
					WHERE id = $2 AND tenant_id = $3
				`, now, nextOpID, tenantID); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
				}
				h.log.Info("Delivery chain: activated next operation", "completed_op", id, "next_op", nextOpID, "so_id", *op.SourceID)
			}
		}

		// Sync: when a delivery linked to a material_request completes, update the MR status and record construction expenses
		if op.SourceType != nil && *op.SourceType == "material_request" {
			h.completeMaterialRequestFromStockOp(tenantID, id, userID, now)
		} else {
			// Also check by stock_operation_id link (in case source_type was not set)
			var mrExists bool
			h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM construction_material_requests WHERE stock_operation_id = $1 AND tenant_id = $2)`, id, tenantID).Scan(&mrExists)
			if mrExists {
				h.completeMaterialRequestFromStockOp(tenantID, id, userID, now)
			}
		}
	} else {
		newState = "in_progress"
		// Find next step definition
		nextStepName := fmt.Sprintf("Step %d", nextStep)
		var nextStepDef struct {
			ID   uuid.UUID
			Name string
		}
		err = h.db.QueryRow(`
			SELECT id, name FROM operation_type_steps
			WHERE operation_type_id=$1 AND tenant_id=$2 AND sequence=$3
		`, op.OpTypeID, tenantID, nextStep).Scan(&nextStepDef.ID, &nextStepDef.Name)
		if err == nil {
			nextStepName = nextStepDef.Name
		}

		// Check if next step has auto_proceed enabled
		var nextStepAutoProced bool
		h.db.QueryRow(`
			SELECT COALESCE(auto_proceed, false) FROM operation_type_steps
			WHERE operation_type_id=$1 AND tenant_id=$2 AND sequence=$3
		`, op.OpTypeID, tenantID, nextStep).Scan(&nextStepAutoProced)

		// Create next step log entry — auto-start if auto_proceed
		nextStepState := "ready"
		if nextStepAutoProced {
			nextStepState = "in_progress"
		}
		if _, execErr := h.db.Exec(`
			INSERT INTO stock_operation_step_log (
				id, tenant_id, operation_id, step_id, step_sequence, step_name, state, started_at, created_at
			) VALUES (uuid_generate_v4(),$1,$2,$3,$4,$5,$6,$7,$7)
		`, tenantID, id, nextStepDef.ID, nextStep, nextStepName, nextStepState, now); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT stock_operation_step_log", "error", execErr)
		}

		if _, execErr := h.db.Exec(`
			UPDATE stock_operations
			SET current_step=$1, state=$2, updated_at=$3
			WHERE id=$4 AND tenant_id=$5
		`, nextStep, newState, now, id, tenantID); execErr != nil {

			h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)

		}
	}

	response.Success(c, map[string]interface{}{
		"state":        newState,
		"current_step": nextStep,
		"total_steps":  op.TotalSteps,
	})
}

// completeLinkedDeliveryOp completes the delivery stock operation linked to a sales order.
// Used in intercompany flow: when buying org receives goods, selling org's delivery op is auto-completed.
// Also deducts inventory from the selling org's warehouse.
func (h *Handler) completeLinkedDeliveryOp(tenantID uuid.UUID, salesOrderID uuid.UUID, now time.Time) {
	var opID uuid.UUID
	var opTypeID uuid.UUID
	var orgID *uuid.UUID
	err := h.db.QueryRow(`
		SELECT id, operation_type_id, organization_id FROM stock_operations
		WHERE source_type = 'sales_order' AND source_id = $1
		  AND tenant_id = $2 AND direction = 'delivery'
		  AND state NOT IN ('done', 'cancelled')
		  AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, salesOrderID, tenantID).Scan(&opID, &opTypeID, &orgID)
	if err != nil {
		return // No pending delivery op found
	}

	// Set done_qty = expected_qty for all lines
	if _, execErr := h.db.Exec(`
		UPDATE stock_operation_lines
		SET done_qty = expected_qty, updated_at = $1
		WHERE operation_id = $2 AND tenant_id = $3
	`, now, opID, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_lines", "error", execErr)
	}

	// Mark operation as done
	if _, execErr := h.db.Exec(`
		UPDATE stock_operations
		SET state = 'done', done_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3
	`, now, opID, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
	}

	// Deduct inventory from selling org's warehouse
	var warehouseID uuid.UUID
	h.db.QueryRow(`
		SELECT wot.warehouse_id FROM warehouse_operation_types wot
		WHERE wot.id = $1 AND wot.tenant_id = $2
	`, opTypeID, tenantID).Scan(&warehouseID)

	if warehouseID != uuid.Nil {
		lines, _ := h.db.Query(`
			SELECT product_id, done_qty, COALESCE(unit_price, 0)
			FROM stock_operation_lines
			WHERE operation_id = $1 AND tenant_id = $2 AND done_qty > 0
		`, opID, tenantID)
		if lines != nil {
			defer lines.Close()
			for lines.Next() {
				var prodID uuid.UUID
				var doneQty, unitPrice float64
				if scanErr := lines.Scan(&prodID, &doneQty, &unitPrice); scanErr != nil {
					continue
				}

				// Find or create inventory record (upsert to prevent duplicates)
				var invID uuid.UUID
				if orgID != nil {
					newID := uuid.New()
					err := h.db.QueryRow(`
						INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, organization_id, quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at)
						VALUES ($1, $2, $3, $4, $5, 0, 0, $6, $7, $7)
						ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE SET updated_at = NOW()
						RETURNING id
					`, newID, tenantID, prodID, warehouseID, *orgID, unitPrice, now).Scan(&invID)
					if err != nil {
						continue
					}
				} else {
					err := h.db.QueryRow(`
						SELECT id FROM inventory
						WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
						ORDER BY created_at ASC LIMIT 1
					`, tenantID, prodID, warehouseID).Scan(&invID)
					if err != nil {
						continue
					}
				}

				// Decrease inventory
				if _, execErr := h.db.Exec(`
					UPDATE inventory SET quantity_on_hand = quantity_on_hand - $1, last_movement_date = $2, updated_at = $2 WHERE id = $3
				`, doneQty, now, invID); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE inventory", "error", execErr)
				}

				// Create inventory transaction record
				if _, execErr := h.db.Exec(`
					INSERT INTO inventory_transactions (
						id, tenant_id, organization_id, inventory_id, transaction_type, quantity,
						unit_cost, total_cost, from_warehouse_id,
						reference_type, reference_id, notes, transaction_date, created_at
					) VALUES ($1, $2, $3, $4, 'issue', $5, $6, $7, $8, 'stock_operation', $9, 'Intercompany delivery', $10, $10)
				`, uuid.New(), tenantID, orgID, invID, -doneQty, unitPrice, math.Abs(doneQty)*unitPrice, warehouseID, opID.String(), now); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "INSERT inventory_transactions", "error", execErr)
				}
			}
		}
	}

	// Post accounting: Debit COGS (5100), Credit Inventory (1300)
	h.postIntercompanyStockAccounting(tenantID, opID, orgID, "delivery", now)

	h.log.Info("Intercompany: auto-completed delivery stock operation", "op_id", opID, "so_id", salesOrderID)
}

// createReceiptStockOpForPO creates a receipt stock op for a PO if one doesn't exist.
func (h *Handler) createReceiptStockOpForPO(tenantID uuid.UUID, purchaseOrderID uuid.UUID, now time.Time) (uuid.UUID, uuid.UUID, *uuid.UUID) {
	// Check if one already exists (including done)
	var existingID uuid.UUID
	h.db.QueryRow(`
		SELECT id FROM stock_operations
		WHERE source_type = 'purchase_order' AND source_id = $1 AND tenant_id = $2
		  AND direction = 'receipt' AND deleted_at IS NULL AND state != 'cancelled'
	`, purchaseOrderID, tenantID).Scan(&existingID)
	if existingID != uuid.Nil {
		return uuid.Nil, uuid.Nil, nil // Already exists
	}

	var orderNumber string
	var vendorID uuid.UUID
	var warehouseID, poOrgID *uuid.UUID
	var expectedDate *time.Time
	err := h.db.QueryRow(`
		SELECT order_number, vendor_id, warehouse_id, organization_id, expected_date
		FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, purchaseOrderID, tenantID).Scan(&orderNumber, &vendorID, &warehouseID, &poOrgID, &expectedDate)
	if err != nil {
		return uuid.Nil, uuid.Nil, nil
	}

	var opTypeID uuid.UUID
	var srcLocID, destLocID *uuid.UUID
	opTypeQuery := `
		SELECT id, default_location_src_id, default_location_dest_id
		FROM warehouse_operation_types
		WHERE tenant_id = $1 AND type = 'receipt' AND is_active = true
	`
	opTypeArgs := []interface{}{tenantID}
	argIdx := 1
	if poOrgID != nil {
		argIdx++
		opTypeQuery += fmt.Sprintf(" AND (organization_id = $%d OR organization_id IS NULL)", argIdx)
		opTypeArgs = append(opTypeArgs, *poOrgID)
	}
	if warehouseID != nil {
		argIdx++
		opTypeQuery += fmt.Sprintf(" AND warehouse_id = $%d", argIdx)
		opTypeArgs = append(opTypeArgs, *warehouseID)
	}
	opTypeQuery += " ORDER BY organization_id IS NULL, sequence LIMIT 1"
	if err := h.db.QueryRow(opTypeQuery, opTypeArgs...).Scan(&opTypeID, &srcLocID, &destLocID); err != nil {
		// Fallback: try without warehouse filter (warehouse on PO may not match any op type)
		if warehouseID != nil && poOrgID != nil {
			fallbackQuery := `
				SELECT id, default_location_src_id, default_location_dest_id
				FROM warehouse_operation_types
				WHERE tenant_id = $1 AND type = 'receipt' AND is_active = true
				  AND (organization_id = $2 OR organization_id IS NULL)
				ORDER BY organization_id IS NULL, sequence LIMIT 1
			`
			if err2 := h.db.QueryRow(fallbackQuery, tenantID, *poOrgID).Scan(&opTypeID, &srcLocID, &destLocID); err2 != nil {
				return uuid.Nil, uuid.Nil, nil
			}
		} else {
			return uuid.Nil, uuid.Nil, nil
		}
	}

	opID := uuid.New()
	opName := h.nextStockOperationName(tenantID, "receipt")

	var totalSteps int
	h.db.QueryRow("SELECT COUNT(*) FROM operation_type_steps WHERE operation_type_id = $1 AND tenant_id = $2", opTypeID, tenantID).Scan(&totalSteps)
	if totalSteps == 0 {
		totalSteps = 1
	}

	_, opErr := h.db.Exec(`
		INSERT INTO stock_operations (
			id, tenant_id, organization_id, name, operation_type_id, direction,
			date, scheduled_date, partner_id, source_document,
			source_location_id, dest_location_id,
			state, current_step, total_steps, priority,
			source_type, source_id,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,'receipt',$6,$7,$8,$9,$10,$11,'draft',1,$12,'normal','purchase_order',$13,$14,$14)
	`,
		opID, tenantID, poOrgID, opName, opTypeID,
		now, expectedDate, vendorID, orderNumber,
		srcLocID, destLocID,
		totalSteps, purchaseOrderID, now,
	)
	if opErr != nil {
		h.log.Error("Intercompany: failed to create receipt stock op", "error", opErr, "po_id", purchaseOrderID)
		return uuid.Nil, uuid.Nil, nil
	}

	// Create lines from PO lines
	rows, _ := h.db.Query(`
		SELECT pol.product_id, pol.quantity, pol.unit_price, COALESCE(u.name, 'unit')
		FROM purchase_order_lines pol
		LEFT JOIN units_of_measure u ON u.id = pol.unit_id
		WHERE pol.purchase_order_id = $1 AND pol.product_id IS NOT NULL
	`, purchaseOrderID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var productID uuid.UUID
			var qty, unitPrice float64
			var uom string
			if rows.Scan(&productID, &qty, &unitPrice, &uom) == nil {
				if _, execErr := h.db.Exec(`
					INSERT INTO stock_operation_lines (
						id, tenant_id, operation_id, product_id,
						expected_qty, done_qty, uom, unit_price,
						quality_status, created_at, updated_at
					) VALUES (uuid_generate_v4(),$1,$2,$3,$4,$4,$5,$6,'good',$7,$7)
				`, tenantID, opID, productID, qty, uom, unitPrice, now); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "INSERT stock_operation_lines", "error", execErr)
				}
			}
		}
	}

	h.log.Info("Intercompany: created receipt stock op for PO", "op_id", opID, "po_id", purchaseOrderID)
	return opID, opTypeID, poOrgID
}

// completeLinkedReceiptOp completes the receipt stock operation linked to a purchase order.
// Used in intercompany flow: when selling org confirms SO, buying org's receipt is auto-completed.
// Also increases inventory in the buying org's warehouse.
func (h *Handler) completeLinkedReceiptOp(tenantID uuid.UUID, purchaseOrderID uuid.UUID, now time.Time) {
	var opID uuid.UUID
	var opTypeID uuid.UUID
	var orgID *uuid.UUID
	err := h.db.QueryRow(`
		SELECT id, operation_type_id, organization_id FROM stock_operations
		WHERE source_type = 'purchase_order' AND source_id = $1
		  AND tenant_id = $2 AND direction = 'receipt'
		  AND state NOT IN ('done', 'cancelled')
		  AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, purchaseOrderID, tenantID).Scan(&opID, &opTypeID, &orgID)
	if err != nil {
		// No receipt stock op — create one via the same logic as PO approval
		opID, opTypeID, orgID = h.createReceiptStockOpForPO(tenantID, purchaseOrderID, now)
		if opID == uuid.Nil {
			return
		}
	}
	// Also check if already done
	var opState string
	h.db.QueryRow("SELECT state FROM stock_operations WHERE id = $1", opID).Scan(&opState)
	if opState == "done" {
		return
	}

	// Set done_qty = expected_qty for all lines
	if _, execErr := h.db.Exec(`
		UPDATE stock_operation_lines
		SET done_qty = expected_qty, updated_at = $1
		WHERE operation_id = $2 AND tenant_id = $3
	`, now, opID, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_lines", "error", execErr)
	}

	// Mark operation as done
	if _, execErr := h.db.Exec(`
		UPDATE stock_operations
		SET state = 'done', done_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3
	`, now, opID, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
	}

	// Update PO status to received
	if _, execErr := h.db.Exec(`
		UPDATE purchase_orders SET status = 'received', updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, now, purchaseOrderID, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE purchase_orders", "error", execErr)
	}

	// Increase inventory in buying org's warehouse
	var warehouseID uuid.UUID
	h.db.QueryRow(`
		SELECT wot.warehouse_id FROM warehouse_operation_types wot
		WHERE wot.id = $1 AND wot.tenant_id = $2
	`, opTypeID, tenantID).Scan(&warehouseID)

	if warehouseID != uuid.Nil {
		lines, _ := h.db.Query(`
			SELECT product_id, done_qty, COALESCE(unit_price, 0)
			FROM stock_operation_lines
			WHERE operation_id = $1 AND tenant_id = $2 AND done_qty > 0
		`, opID, tenantID)
		if lines != nil {
			defer lines.Close()
			for lines.Next() {
				var prodID uuid.UUID
				var doneQty, unitPrice float64
				if scanErr := lines.Scan(&prodID, &doneQty, &unitPrice); scanErr != nil {
					continue
				}

				// Find or create inventory record (upsert to prevent duplicates)
				var invID uuid.UUID
				if orgID != nil {
					newID := uuid.New()
					err := h.db.QueryRow(`
						INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, organization_id, quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at)
						VALUES ($1, $2, $3, $4, $5, 0, 0, $6, $7, $7)
						ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE SET updated_at = NOW()
						RETURNING id
					`, newID, tenantID, prodID, warehouseID, *orgID, unitPrice, now).Scan(&invID)
					if err != nil {
						continue
					}
				} else {
					err := h.db.QueryRow(`
						SELECT id FROM inventory
						WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
						ORDER BY created_at ASC LIMIT 1
					`, tenantID, prodID, warehouseID).Scan(&invID)
					if err != nil {
						continue
					}
				}

				// Increase inventory
				if _, execErr := h.db.Exec(`
					UPDATE inventory SET quantity_on_hand = quantity_on_hand + $1, unit_cost = $4, last_movement_date = $2, updated_at = $2 WHERE id = $3
				`, doneQty, now, invID, unitPrice); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE inventory", "error", execErr)
				}

				// Create inventory transaction record
				if _, execErr := h.db.Exec(`
					INSERT INTO inventory_transactions (
						id, tenant_id, organization_id, inventory_id, transaction_type, quantity,
						unit_cost, total_cost, to_warehouse_id,
						reference_type, reference_id, notes, transaction_date, created_at
					) VALUES ($1, $2, $3, $4, 'receipt', $5, $6, $7, $8, 'stock_operation', $9, 'Intercompany receipt', $10, $10)
				`, uuid.New(), tenantID, orgID, invID, doneQty, unitPrice, doneQty*unitPrice, warehouseID, opID.String(), now); execErr != nil {
					h.log.Error("write failed (was silently discarded)", "stmt", "INSERT inventory_transactions", "error", execErr)
				}
			}
		}
	}

	// Post accounting: Debit Inventory (1300), Credit Accounts Payable (2100)
	h.postIntercompanyStockAccounting(tenantID, opID, orgID, "receipt", now)

	h.log.Info("Intercompany: auto-completed receipt stock operation", "op_id", opID, "po_id", purchaseOrderID)
}

// postIntercompanyStockAccounting creates journal entries for intercompany stock operations.
// Uses product category accounts per line:
//
//	receipt → Debit StockValuation, Credit StockInput
//	delivery → Debit ExpenseAccount(COGS), Credit StockValuation
func (h *Handler) postIntercompanyStockAccounting(tenantID uuid.UUID, opID uuid.UUID, orgID *uuid.UUID, direction string, now time.Time) {
	// Get operation lines with product IDs
	lineRows, _ := h.db.Query(`
		SELECT product_id, done_qty, COALESCE(unit_price, 0)
		FROM stock_operation_lines WHERE operation_id=$1 AND tenant_id=$2 AND done_qty > 0
	`, opID, tenantID)
	if lineRows == nil {
		return
	}
	defer lineRows.Close()

	type lineEntry struct {
		debitAcct  uuid.UUID
		creditAcct uuid.UUID
		amount     float64
		prodName   string
	}
	var entries []lineEntry
	var totalValue float64

	for lineRows.Next() {
		var prodID uuid.UUID
		var doneQty, unitPrice float64
		if err := lineRows.Scan(&prodID, &doneQty, &unitPrice); err != nil {
			continue
		}
		lineValue := doneQty * unitPrice
		if lineValue <= 0 {
			continue
		}

		ca := getCategoryAccounts(h.db, tenantID, orgID, prodID)

		var debitAcct, creditAcct uuid.UUID
		switch direction {
		case "receipt":
			debitAcct = ca.StockValuationAccountID
			creditAcct = ca.StockInputAccountID
			if creditAcct == uuid.Nil {
				creditAcct = findAccount(h.db, tenantID, orgID, "accounts payable", "6010")
			}
		case "delivery":
			debitAcct = ca.ExpenseAccountID
			creditAcct = ca.StockValuationAccountID
		}

		if debitAcct == uuid.Nil || creditAcct == uuid.Nil {
			continue
		}

		var pName string
		h.db.QueryRow("SELECT COALESCE(name,'') FROM products WHERE id=$1", prodID).Scan(&pName)

		entries = append(entries, lineEntry{debitAcct: debitAcct, creditAcct: creditAcct, amount: lineValue, prodName: pName})
		totalValue += lineValue
	}

	if totalValue <= 0 || len(entries) == 0 {
		return
	}

	// Find journal
	var journalID uuid.UUID
	var nextNumber int
	h.db.QueryRow(`
		SELECT id, COALESCE(next_number,1) FROM journals
		WHERE tenant_id=$1 AND code IN ('STOCK','INVENTORY','MISC','GENERAL') AND deleted_at IS NULL
		ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'INVENTORY' THEN 1 WHEN 'MISC' THEN 2 ELSE 3 END LIMIT 1
	`, tenantID).Scan(&journalID, &nextNumber)

	if journalID == uuid.Nil {
		return
	}

	entryID := uuid.New()
	prefixMap := map[string]string{"receipt": "REC", "delivery": "DEL"}
	prefix := prefixMap[direction]
	if prefix == "" {
		prefix = "STK"
	}
	entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

	var opName string
	h.db.QueryRow("SELECT name FROM stock_operations WHERE id=$1", opID).Scan(&opName)
	description := fmt.Sprintf("Intercompany Stock: %s", opName)

	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("Failed to begin intercompany journal tx", "error", txErr)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date,
			description, source_type, source_id, status, total_debit, total_credit,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'stock_operation', $8, 'posted', $9, $9, $10, $10)
	`, entryID, tenantID, orgID, journalID, entryNumber, now,
		description, opID.String(), totalValue, now); err != nil {
		h.log.Error("Failed to create intercompany journal entry", "error", err)
		return
	}

	lineNum := 0
	for _, e := range entries {
		lineDesc := fmt.Sprintf("%s: %s", description, e.prodName)
		lineNum++
		if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, $5, 0, $6, $7)`,
			uuid.New(), entryID, e.debitAcct, lineDesc, e.amount, lineNum, now); err != nil {
			h.log.Error("Failed to insert intercompany debit line", "error", err)
			return
		}
		lineNum++
		if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, 0, $5, $6, $7)`,
			uuid.New(), entryID, e.creditAcct, lineDesc, e.amount, lineNum, now); err != nil {
			h.log.Error("Failed to insert intercompany credit line", "error", err)
			return
		}

		if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", e.amount, now, e.debitAcct); err != nil {
			h.log.Error("Failed to update intercompany debit balance", "error", err)
			return
		}
		if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", e.amount, now, e.creditAcct); err != nil {
			h.log.Error("Failed to update intercompany credit balance", "error", err)
			return
		}
	}

	if _, err := tx.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID); err != nil {
		h.log.Error("Failed to bump journal next_number", "error", err)
		return
	}
	if _, err := tx.Exec("UPDATE stock_operations SET accounting_posted = true, journal_entry_id = $1, updated_at = $2 WHERE id = $3", entryID, now, opID); err != nil {
		h.log.Error("Failed to mark stock operation posted", "error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit intercompany journal entry", "error", err)
		return
	}

	h.log.Info("Intercompany: posted GL entry", "direction", direction, "entry_id", entryID, "amount", totalValue)
}

// completeMaterialRequestFromStockOp updates a material request to 'approved' status
// and records construction expenses when the linked delivery stock operation completes.
func (h *Handler) completeMaterialRequestFromStockOp(tenantID uuid.UUID, stockOpID uuid.UUID, userID uuid.UUID, now time.Time) {
	// Find the linked material request
	var requestID, projectID int64
	var itemsRaw []byte
	var status string
	err := h.db.QueryRow(`
		SELECT id, project_id, items, status
		FROM construction_material_requests
		WHERE stock_operation_id = $1 AND tenant_id = $2
	`, stockOpID, tenantID).Scan(&requestID, &projectID, &itemsRaw, &status)
	if err != nil {
		return // No linked material request
	}
	if status == "approved" || status == "fulfilled" {
		return // Already processed
	}

	// Resolve approver employee ID
	var approverEmployeeID *uuid.UUID
	if userID != uuid.Nil {
		var empID uuid.UUID
		if err := h.db.QueryRow(`SELECT id FROM employees WHERE user_id = $1 AND tenant_id = $2 LIMIT 1`, userID, tenantID).Scan(&empID); err == nil {
			approverEmployeeID = &empID
		}
	}

	// Update material request status to approved
	if _, execErr := h.db.Exec(`
		UPDATE construction_material_requests
		SET status = 'approved', approved_by = $1, approval_date = $2, updated_date = $2
		WHERE id = $3 AND tenant_id = $4
	`, approverEmployeeID, now, requestID, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE construction_material_requests", "error", execErr)
	}

	// Parse items for expense tracking
	var items []map[string]interface{}
	if err := json.Unmarshal(itemsRaw, &items); err != nil || len(items) == 0 {
		return
	}

	// Get organization ID from stock operation
	var organizationID *uuid.UUID
	h.db.QueryRow(`SELECT organization_id FROM stock_operations WHERE id = $1`, stockOpID).Scan(&organizationID)

	var orgIDPtr *uuid.UUID
	if organizationID != nil {
		orgIDPtr = organizationID
	}

	// Find expense account
	expenseAcct := findAccount(h.db, tenantID, orgIDPtr, "construction expense", "9610")
	if expenseAcct == uuid.Nil {
		expenseAcct = findAccount(h.db, tenantID, orgIDPtr, "cost of goods", "9110")
	}

	var totalExpense float64

	// Upsert project materials and calculate totals
	for _, item := range items {
		productIDStr, _ := item["product_id"].(string)
		if productIDStr == "" {
			continue
		}
		var qty float64
		switch v := item["quantity"].(type) {
		case float64:
			qty = v
		case json.Number:
			qty, _ = v.Float64()
		}
		var unitCost float64
		switch v := item["unit_cost"].(type) {
		case float64:
			unitCost = v
		case json.Number:
			unitCost, _ = v.Float64()
		}
		if unitCost == 0 {
			pid, _ := uuid.Parse(productIDStr)
			h.db.QueryRow(`SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1`, pid).Scan(&unitCost)
		}

		lineCost := qty * unitCost
		totalExpense += lineCost

		productName, _ := item["product_name"].(string)
		uom, _ := item["unit_name"].(string)
		if productName == "" {
			pid, _ := uuid.Parse(productIDStr)
			h.db.QueryRow(`SELECT COALESCE(name,''), COALESCE(unit_name,'') FROM products WHERE id = $1`, pid).Scan(&productName, &uom)
		}

		// Upsert project materials
		if _, execErr := h.db.Exec(`
			INSERT INTO construction_project_materials
				(tenant_id, project_id, product_id, product_name, uom, approved_quantity, unit_cost, created_date, updated_date)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			ON CONFLICT (tenant_id, project_id, product_id) DO UPDATE
				SET approved_quantity = construction_project_materials.approved_quantity + EXCLUDED.approved_quantity,
				    unit_cost = EXCLUDED.unit_cost,
				    product_name = CASE WHEN EXCLUDED.product_name != '' THEN EXCLUDED.product_name ELSE construction_project_materials.product_name END,
				    uom = CASE WHEN EXCLUDED.uom != '' THEN EXCLUDED.uom ELSE construction_project_materials.uom END,
				    updated_date = EXCLUDED.updated_date
		`, tenantID, projectID, productIDStr, productName, uom, qty, unitCost, now); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT construction_project_materials", "error", execErr)
		}
	}

	// Record construction cost tracking
	if totalExpense > 0 {
		if _, execErr := h.db.Exec(`
			INSERT INTO construction_cost_tracking (
				tenant_id, project_id, tracking_date, actual_cost, notes, created_date
			) VALUES ($1, $2, $3, $4, $5, NOW())
		`, tenantID, projectID, now.Format("2006-01-02"), totalExpense,
			fmt.Sprintf("Material Request #%d delivered via stock operation", requestID)); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT construction_cost_tracking", "error", execErr)
		}
	}
}

// ApproveStockOperationStep approves a step that is awaiting approval, then advances the operation
func (h *Handler) ApproveStockOperationStep(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}
	userID, _ := middleware.GetUserID(c)

	// Verify operation is in awaiting_approval state
	var opState string
	var currentStep int
	err = h.db.QueryRow(`
		SELECT state, current_step FROM stock_operations
		WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&opState, &currentStep)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to query stock operation")
		return
	}
	if opState != "awaiting_approval" {
		response.BadRequest(c, "Operation is not awaiting approval")
		return
	}

	// Mark step as approved
	if _, execErr := h.db.Exec(`
		UPDATE stock_operation_step_log
		SET approved_by=$1
		WHERE operation_id=$2 AND step_sequence=$3 AND tenant_id=$4
	`, userID, id, currentStep, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_step_log", "error", execErr)
	}

	// Now advance the operation — since state is 'awaiting_approval',
	// the advance handler will skip the approval check and proceed to completion
	h.AdvanceStockOperationStep(c)
}

// RejectStockOperationStep rejects a step that is awaiting approval
func (h *Handler) RejectStockOperationStep(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Reason == "" {
		response.BadRequest(c, "Rejection reason is required")
		return
	}

	// Verify operation is in awaiting_approval state
	var opState string
	var currentStep int
	err = h.db.QueryRow(`
		SELECT state, current_step FROM stock_operations
		WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&opState, &currentStep)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to query stock operation")
		return
	}
	if opState != "awaiting_approval" {
		response.BadRequest(c, "Operation is not awaiting approval")
		return
	}

	now := time.Now()

	// Set step_log to rejected with reason, then back to ready for re-work
	if _, execErr := h.db.Exec(`
		UPDATE stock_operation_step_log
		SET state='ready', rejection_reason=$1, approved_by=NULL, completed_by=$2
		WHERE operation_id=$3 AND step_sequence=$4 AND tenant_id=$5
	`, input.Reason, userID, id, currentStep, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_step_log", "error", execErr)
	}

	// Return operation to in_progress state
	if _, execErr := h.db.Exec(`
		UPDATE stock_operations
		SET state='in_progress', updated_at=$1
		WHERE id=$2 AND tenant_id=$3
	`, now, id, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
	}

	response.Success(c, map[string]interface{}{
		"state":        "in_progress",
		"current_step": currentStep,
		"rejected":     true,
		"reason":       input.Reason,
	})
}

// AddStepDocument attaches a document reference to a step log
func (h *Handler) AddStepDocument(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid operation ID")
		return
	}
	stepSeq := c.Param("step")

	var input struct {
		DocumentType string `json:"document_type"`
		DocumentURL  string `json:"document_url"`
		FileName     string `json:"file_name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Append document to the step_log's documents JSONB array
	if _, execErr := h.db.Exec(`
		UPDATE stock_operation_step_log
		SET documents = COALESCE(documents, '[]'::jsonb) || $1::jsonb
		WHERE operation_id=$2 AND step_sequence=$3 AND tenant_id=$4
	`, fmt.Sprintf(`[{"type":"%s","url":"%s","name":"%s"}]`, input.DocumentType, input.DocumentURL, input.FileName),
		id, stepSeq, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_step_log", "error", execErr)
	}

	response.Success(c, gin.H{"message": "Document attached"})
}

// CancelStockOperation cancels a stock operation
func (h *Handler) CancelStockOperation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var state string
	err = h.db.QueryRow("SELECT state FROM stock_operations WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", id, tenantID).Scan(&state)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if state == "done" {
		response.BadRequest(c, "Cannot cancel a completed operation")
		return
	}

	now := time.Now()
	if _, execErr := h.db.Exec("UPDATE stock_operations SET state='cancelled', updated_at=$1 WHERE id=$2 AND tenant_id=$3", now, id, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
	}

	response.Success(c, map[string]string{"state": "cancelled"})
}

// ValidateStockOperation confirms a draft stock operation (sets to in_progress)
func (h *Handler) ValidateStockOperation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var state string
	err = h.db.QueryRow("SELECT state FROM stock_operations WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", id, tenantID).Scan(&state)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft operations can be validated")
		return
	}

	now := time.Now()
	if _, execErr := h.db.Exec("UPDATE stock_operations SET state='in_progress', updated_at=$1 WHERE id=$2 AND tenant_id=$3", now, id, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
	}

	response.Success(c, map[string]string{"state": "in_progress"})
}

// UpdateStockOperation edits header fields of a draft stock operation
func (h *Handler) UpdateStockOperation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var state string
	err = h.db.QueryRow("SELECT state FROM stock_operations WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", id, tenantID).Scan(&state)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft operations can be edited")
		return
	}

	var input entity.UpdateStockOperationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	sets := []string{"updated_at = $1"}
	args := []interface{}{now}
	idx := 2

	if input.PartnerID != nil {
		if *input.PartnerID == "" {
			sets = append(sets, fmt.Sprintf("partner_id = NULL"))
		} else {
			pid, err := uuid.Parse(*input.PartnerID)
			if err == nil {
				sets = append(sets, fmt.Sprintf("partner_id = $%d", idx))
				args = append(args, pid)
				idx++
			}
		}
	}
	if input.SourceDocument != nil {
		sets = append(sets, fmt.Sprintf("source_document = $%d", idx))
		args = append(args, *input.SourceDocument)
		idx++
	}
	if input.ScheduledDate != nil {
		if *input.ScheduledDate == "" {
			sets = append(sets, "scheduled_date = NULL")
		} else {
			sets = append(sets, fmt.Sprintf("scheduled_date = $%d", idx))
			args = append(args, *input.ScheduledDate)
			idx++
		}
	}
	if input.Priority != nil {
		sets = append(sets, fmt.Sprintf("priority = $%d", idx))
		args = append(args, *input.Priority)
		idx++
	}
	if input.Note != nil {
		sets = append(sets, fmt.Sprintf("note = $%d", idx))
		args = append(args, *input.Note)
		idx++
	}
	if input.WriteOffReason != nil {
		if *input.WriteOffReason == "" {
			sets = append(sets, "write_off_reason = NULL")
		} else {
			sets = append(sets, fmt.Sprintf("write_off_reason = $%d", idx))
			args = append(args, *input.WriteOffReason)
			idx++
		}
	}

	args = append(args, id, tenantID)
	query := fmt.Sprintf("UPDATE stock_operations SET %s WHERE id=$%d AND tenant_id=$%d",
		strings.Join(sets, ", "), idx, idx+1)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update stock operation", "error", err)
		response.InternalError(c, "Failed to update stock operation")
		return
	}

	response.Success(c, map[string]string{"status": "updated"})
}

// UpdateStockOperationLines batch updates done_qty and other fields on operation lines
func (h *Handler) UpdateStockOperationLines(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	opID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var state string
	err = h.db.QueryRow("SELECT state FROM stock_operations WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", opID, tenantID).Scan(&state)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if state == "done" || state == "cancelled" {
		response.BadRequest(c, "Cannot update lines of a "+state+" operation")
		return
	}

	var input entity.UpdateStockOperationLinesInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	for _, line := range input.Lines {
		lineID, err := uuid.Parse(line.ID)
		if err != nil {
			continue
		}

		sets := []string{"updated_at = $1"}
		args := []interface{}{now}
		idx := 2

		if line.DoneQty != nil {
			sets = append(sets, fmt.Sprintf("done_qty = $%d", idx))
			args = append(args, *line.DoneQty)
			idx++
		}
		if line.LotNumber != nil {
			sets = append(sets, fmt.Sprintf("lot_number = $%d", idx))
			args = append(args, *line.LotNumber)
			idx++
		}
		if line.QualityStatus != nil {
			sets = append(sets, fmt.Sprintf("quality_status = $%d", idx))
			args = append(args, *line.QualityStatus)
			idx++
		}
		if line.UnitPrice != nil {
			sets = append(sets, fmt.Sprintf("unit_price = $%d", idx))
			args = append(args, *line.UnitPrice)
			idx++
		}
		if line.Note != nil {
			sets = append(sets, fmt.Sprintf("note = $%d", idx))
			args = append(args, *line.Note)
			idx++
		}

		if len(sets) <= 1 {
			continue // nothing to update
		}

		args = append(args, lineID, opID, tenantID)
		query := fmt.Sprintf("UPDATE stock_operation_lines SET %s WHERE id=$%d AND operation_id=$%d AND tenant_id=$%d",
			strings.Join(sets, ", "), idx, idx+1, idx+2)
		if _, execErr := h.db.Exec(query, args...); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "exec", "error", execErr)
		}
	}

	response.Success(c, map[string]string{"status": "updated"})
}

// AddStockOperationLine adds a new line to an existing stock operation
func (h *Handler) AddStockOperationLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	opID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var state string
	err = h.db.QueryRow("SELECT state FROM stock_operations WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", opID, tenantID).Scan(&state)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if state == "done" || state == "cancelled" {
		response.BadRequest(c, "Cannot add lines to a "+state+" operation")
		return
	}

	var input entity.AddStockOperationLineInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product_id")
		return
	}

	uom := input.UOM
	if uom == "" {
		uom = "unit"
	}
	qs := input.QualityStatus
	if qs == "" {
		qs = "good"
	}

	now := time.Now()
	lineID := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO stock_operation_lines (
			id, tenant_id, operation_id, product_id,
			expected_qty, done_qty, uom, unit_price,
			lot_number, expiry_date, quality_status,
			note, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')::date,$11,$12,$13,$13)
	`,
		lineID, tenantID, opID, productID,
		input.ExpectedQty, input.DoneQty, uom, input.UnitPrice,
		input.LotNumber, input.ExpiryDate, qs,
		input.Note, now,
	)
	if err != nil {
		h.log.Error("Failed to add stock operation line", "error", err)
		response.InternalError(c, "Failed to add line")
		return
	}

	response.Created(c, map[string]interface{}{"id": lineID})
}

// DeleteStockOperationLine removes a line from a stock operation
func (h *Handler) DeleteStockOperationLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	opID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid operation ID")
		return
	}

	lineID, err := uuid.Parse(c.Param("lineId"))
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}

	var state string
	err = h.db.QueryRow("SELECT state FROM stock_operations WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", opID, tenantID).Scan(&state)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}
	if state == "done" || state == "cancelled" {
		response.BadRequest(c, "Cannot delete lines from a "+state+" operation")
		return
	}

	result, err := h.db.Exec("DELETE FROM stock_operation_lines WHERE id=$1 AND operation_id=$2 AND tenant_id=$3", lineID, opID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete line")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Stock operation line")
		return
	}

	response.Success(c, map[string]string{"status": "deleted"})
}

// CreateBackorder creates a new operation from unfulfilled lines (done_qty < expected_qty)
func (h *Handler) CreateBackorder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Fetch the original operation
	var op struct {
		OperationTypeID uuid.UUID
		Direction       string
		PartnerID       *uuid.UUID
		SourceDocument  string
		Priority        string
		Note            string
		TotalSteps      int
	}
	err = h.db.QueryRow(`
		SELECT operation_type_id, direction, partner_id, COALESCE(source_document,''),
		       COALESCE(priority,'normal'), COALESCE(note,''), total_steps
		FROM stock_operations WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&op.OperationTypeID, &op.Direction, &op.PartnerID,
		&op.SourceDocument, &op.Priority, &op.Note, &op.TotalSteps)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock operation")
		return
	}

	// Find lines with remaining quantities
	rows, err := h.db.Query(`
		SELECT id, product_id, expected_qty, done_qty, uom, unit_price,
		       COALESCE(lot_number,''), quality_status, COALESCE(note,'')
		FROM stock_operation_lines
		WHERE operation_id=$1 AND tenant_id=$2 AND expected_qty > done_qty
	`, id, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to fetch lines")
		return
	}
	defer rows.Close()

	type backorderLine struct {
		ProductID     uuid.UUID
		RemainingQty  float64
		UOM           string
		UnitPrice     *float64
		LotNumber     string
		QualityStatus string
		Note          string
	}
	var lines []backorderLine
	for rows.Next() {
		var l backorderLine
		var origID uuid.UUID
		var expectedQty, doneQty float64
		err = rows.Scan(&origID, &l.ProductID, &expectedQty, &doneQty, &l.UOM, &l.UnitPrice,
			&l.LotNumber, &l.QualityStatus, &l.Note)
		if err != nil {
			continue
		}
		l.RemainingQty = expectedQty - doneQty
		if l.RemainingQty > 0 {
			lines = append(lines, l)
		}
	}

	if len(lines) == 0 {
		response.BadRequest(c, "No remaining quantities for backorder")
		return
	}

	// Create backorder
	backorderID := uuid.New()
	name := h.nextStockOperationName(tenantID, op.Direction)
	now := time.Now()

	_, err = h.db.Exec(`
		INSERT INTO stock_operations (
			id, tenant_id, name, operation_type_id, direction,
			date, partner_id, source_document,
			state, current_step, total_steps, priority,
			note, backorder_id, responsible_id,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'draft',1,$9,$10,$11,$12,$13,$14,$14)
	`,
		backorderID, tenantID, name, op.OperationTypeID, op.Direction,
		now, op.PartnerID, op.SourceDocument,
		op.TotalSteps, op.Priority,
		op.Note, id, userID,
		now,
	)
	if err != nil {
		h.log.Error("Failed to create backorder", "error", err)
		response.InternalError(c, "Failed to create backorder")
		return
	}

	// Create backorder lines
	for _, l := range lines {
		if _, execErr := h.db.Exec(`
			INSERT INTO stock_operation_lines (
				id, tenant_id, operation_id, product_id,
				expected_qty, done_qty, uom, unit_price,
				lot_number, quality_status, note, created_at, updated_at
			) VALUES (uuid_generate_v4(),$1,$2,$3,$4,0,$5,$6,$7,$8,$9,$10,$10)
		`,
			tenantID, backorderID, l.ProductID,
			l.RemainingQty, l.UOM, l.UnitPrice,
			l.LotNumber, l.QualityStatus, l.Note, now,
		); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT stock_operation_lines", "error", execErr)
		}
	}

	// Create initial step log
	firstStepName := "Step 1"
	var firstStep struct {
		ID   uuid.UUID
		Name string
	}
	err = h.db.QueryRow(`
		SELECT id, name FROM operation_type_steps
		WHERE operation_type_id = $1 AND tenant_id = $2
		ORDER BY sequence LIMIT 1
	`, op.OperationTypeID, tenantID).Scan(&firstStep.ID, &firstStep.Name)
	if err == nil {
		firstStepName = firstStep.Name
	}
	if _, execErr := h.db.Exec(`
		INSERT INTO stock_operation_step_log (
			id, tenant_id, operation_id, step_id, step_sequence, step_name, state, created_at
		) VALUES (uuid_generate_v4(),$1,$2,$3,1,$4,'ready',$5)
	`, tenantID, backorderID, firstStep.ID, firstStepName, now); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "INSERT stock_operation_step_log", "error", execErr)
	}

	// Mark the original as having a backorder
	if _, execErr := h.db.Exec("UPDATE stock_operations SET backorder_id=$1, updated_at=$2 WHERE id=$3 AND tenant_id=$4",
		backorderID, now, id, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operations", "error", execErr)
	}

	response.Created(c, map[string]interface{}{
		"id":   backorderID,
		"name": name,
	})
}

// ListOperationTypeSteps returns the steps configured for an operation type
func (h *Handler) ListOperationTypeSteps(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	opTypeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid operation type ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, sequence, name, responsible_role,
		       requires_approval, approval_role, auto_proceed,
		       max_duration_hours, on_timeout, instructions,
		       source_location_id, dest_location_id,
		       created_at, updated_at
		FROM operation_type_steps
		WHERE operation_type_id=$1 AND tenant_id=$2
		ORDER BY sequence
	`, opTypeID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to list steps")
		return
	}
	defer rows.Close()

	steps := make([]*entity.OperationTypeStep, 0)
	for rows.Next() {
		var s entity.OperationTypeStep
		var maxDur sql.NullFloat64
		var instrNote sql.NullString
		var respRole, apprRole sql.NullString
		var srcLoc, destLoc *uuid.UUID
		err := rows.Scan(
			&s.ID, &s.Sequence, &s.Name, &respRole,
			&s.RequiresApproval, &apprRole, &s.AutoProceed,
			&maxDur, &s.OnTimeout, &instrNote,
			&srcLoc, &destLoc,
			&s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			continue
		}
		if maxDur.Valid {
			s.MaxDurationHours = &maxDur.Float64
		}
		if instrNote.Valid {
			s.Instructions = instrNote.String
		}
		if respRole.Valid {
			s.ResponsibleRole = respRole.String
		}
		if apprRole.Valid {
			s.ApprovalRole = apprRole.String
		}
		s.SourceLocationID = srcLoc
		s.DestLocationID = destLoc
		s.NotifyUsers = []string{}
		s.RequiredDocuments = []string{}
		s.TenantID = tenantID
		s.OperationTypeID = opTypeID
		steps = append(steps, &s)
	}

	response.Success(c, steps)
}

// SaveOperationTypeSteps replaces all steps for an operation type
func (h *Handler) SaveOperationTypeSteps(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	opTypeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid operation type ID")
		return
	}

	var steps []struct {
		Sequence         int      `json:"sequence"`
		Name             string   `json:"name"`
		SourceLocationID *string  `json:"source_location_id"`
		DestLocationID   *string  `json:"dest_location_id"`
		ResponsibleRole  string   `json:"responsible_role"`
		RequiresApproval bool     `json:"requires_approval"`
		ApprovalRole     string   `json:"approval_role"`
		AutoProceed      bool     `json:"auto_proceed"`
		MaxDurationHours *float64 `json:"max_duration_hours"`
		OnTimeout        string   `json:"on_timeout"`
		Instructions     string   `json:"instructions"`
	}
	if err := c.ShouldBindJSON(&steps); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()

	// Delete old steps
	if _, execErr := h.db.Exec("DELETE FROM operation_type_steps WHERE operation_type_id=$1 AND tenant_id=$2", opTypeID, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "DELETE operation_type_steps", "error", execErr)
	}

	for i, s := range steps {
		seq := s.Sequence
		if seq == 0 {
			seq = i + 1
		}
		onTimeout := s.OnTimeout
		if onTimeout == "" {
			onTimeout = "warn"
		}
		var srcLoc, destLoc *uuid.UUID
		if s.SourceLocationID != nil && *s.SourceLocationID != "" {
			parsed, _ := uuid.Parse(*s.SourceLocationID)
			if parsed != uuid.Nil {
				srcLoc = &parsed
			}
		}
		if s.DestLocationID != nil && *s.DestLocationID != "" {
			parsed, _ := uuid.Parse(*s.DestLocationID)
			if parsed != uuid.Nil {
				destLoc = &parsed
			}
		}
		if _, execErr := h.db.Exec(`
			INSERT INTO operation_type_steps (
				id, tenant_id, operation_type_id, sequence, name,
				source_location_id, dest_location_id,
				responsible_role, requires_approval, approval_role,
				auto_proceed, max_duration_hours, on_timeout, instructions,
				created_at, updated_at
			) VALUES (uuid_generate_v4(),$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)
		`,
			tenantID, opTypeID, seq, s.Name,
			srcLoc, destLoc,
			s.ResponsibleRole, s.RequiresApproval, s.ApprovalRole,
			s.AutoProceed, s.MaxDurationHours, onTimeout, s.Instructions,
			now,
		); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT operation_type_steps", "error", execErr)
		}
	}

	response.Success(c, map[string]interface{}{"saved": len(steps)})
}

// GetStockOperationSummary returns counts by direction and state
func (h *Handler) GetStockOperationSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	organizationID, hasOrg := middleware.GetOrganizationID(c)

	query := `
		SELECT direction, state, COUNT(*) as cnt
		FROM stock_operations
		WHERE tenant_id=$1 AND deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	if hasOrg && organizationID != uuid.Nil {
		query += " AND organization_id = $2"
		args = append(args, organizationID)
	}
	query += " GROUP BY direction, state"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to get summary")
		return
	}
	defer rows.Close()

	type entry struct {
		Direction string `json:"direction"`
		State     string `json:"state"`
		Count     int    `json:"count"`
	}
	result := make([]entry, 0)
	for rows.Next() {
		var e entry
		rows.Scan(&e.Direction, &e.State, &e.Count)
		result = append(result, e)
	}

	response.Success(c, result)
}

// =====================================================
// EMPLOYEE DEDUCTIONS — Inventory shortage → Payroll bridge
// =====================================================

// AssignResponsible assigns a responsible employee to a shortage line and optionally creates a deduction
func (h *Handler) AssignResponsible(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	lineID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	var input entity.AssignResponsibleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.Resolution != "employee" && input.Resolution != "company" && input.Resolution != "cash" {
		response.BadRequest(c, "Resolution must be 'employee', 'company', or 'cash'")
		return
	}

	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Verify line exists, belongs to tenant, and has shortage
	var stockCountID uuid.UUID
	var productID uuid.UUID
	var varianceQty float64
	var unitCost float64
	var currentResolution string
	err = h.db.QueryRow(`
		SELECT scl.stock_count_id, scl.product_id, scl.variance_quantity, COALESCE(scl.unit_cost, 0), COALESCE(scl.resolution, 'pending')
		FROM stock_count_lines scl
		JOIN stock_counts sc ON sc.id = scl.stock_count_id
		WHERE scl.id = $1 AND sc.tenant_id = $2
	`, lineID, tenantID).Scan(&stockCountID, &productID, &varianceQty, &unitCost, &currentResolution)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stock count line")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to query line")
		return
	}

	// variance_quantity is negative when there's a shortage (counted - system)
	if varianceQty >= 0 {
		response.BadRequest(c, "Bu satrda kamomad yo'q (no shortage on this line)")
		return
	}
	if currentResolution != "pending" {
		response.BadRequest(c, "Bu satr uchun mas'ul allaqachon tayinlangan")
		return
	}

	shortageQty := -varianceQty // make positive
	shortageAmount := shortageQty * unitCost

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	now := time.Now()

	// 1. Update stock_count_line with resolution and responsible
	_, err = tx.Exec(`
		UPDATE stock_count_lines SET resolution = $1, responsible_emp_id = $2, updated_at = $3 WHERE id = $4
	`, input.Resolution, employeeID, now, lineID)
	if err != nil {
		response.InternalError(c, "Failed to update line")
		return
	}

	var deductionResp *entity.EmployeeDeduction

	// 2. If resolution is "employee", create a deduction record
	if input.Resolution == "employee" {
		// Get product name for reason text
		var productName, productUnit string
		h.db.QueryRow("SELECT name, COALESCE(unit_of_measure, 'шт') FROM products WHERE id=$1", productID).Scan(&productName, &productUnit)

		reason := fmt.Sprintf("Inventarizatsiya kamomad: %s %.2f %s x %.0f = %.0f so'm",
			productName, shortageQty, productUnit, unitCost, shortageAmount)

		deductionID := uuid.New()
		var orgIDPtr *uuid.UUID
		if orgID != uuid.Nil {
			orgIDPtr = &orgID
		}

		_, err = tx.Exec(`
			INSERT INTO employee_deductions (id, tenant_id, organization_id, employee_id, amount, reason, source_type, source_id, status, created_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'inventory_shortage', $7, 'pending', $8, $9, $9)
		`, deductionID, tenantID, orgIDPtr, employeeID, shortageAmount, reason, lineID, userID, now)
		if err != nil {
			h.log.Error("Failed to create deduction", "error", err)
			response.InternalError(c, "Failed to create deduction")
			return
		}

		// Get employee name (tenant-scoped)
		var empName string
		h.db.QueryRow("SELECT COALESCE(first_name || ' ' || last_name, first_name, '') FROM employees WHERE id=$1 AND tenant_id=$2", employeeID, tenantID).Scan(&empName)

		deductionResp = &entity.EmployeeDeduction{
			ID:           deductionID,
			TenantID:     tenantID,
			EmployeeID:   employeeID,
			Amount:       shortageAmount,
			Reason:       reason,
			SourceType:   "inventory_shortage",
			SourceID:     &lineID,
			Status:       "pending",
			CreatedBy:    userID,
			CreatedAt:    now,
			UpdatedAt:    now,
			EmployeeName: empName,
		}
		if orgIDPtr != nil {
			deductionResp.OrganizationID = orgIDPtr
		}

		// 3. Create journal entry: Dt 9430 (Shortages/Losses) / Kt product inventory account
		var journalID uuid.UUID
		var nextNumber int
		tx.QueryRow(`SELECT id, COALESCE(next_number,1) FROM journals WHERE tenant_id=$1 AND code IN ('STOCK','INVENTORY','MISC','GENERAL') AND deleted_at IS NULL ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'INVENTORY' THEN 1 WHEN 'MISC' THEN 2 ELSE 3 END LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)

		if journalID != uuid.Nil {
			// Resolve both accounts BEFORE inserting the JE header: writing the
			// header first would leave a 0-line 'posted' entry when an account
			// is missing (migration 416 deferred trigger issue).
			// Dt 9430 - Shortages and losses from damage of valuables
			shortageAcct := findAccount(tx, tenantID, orgIDPtr, "shortage", "9430")
			if shortageAcct == uuid.Nil {
				shortageAcct = findAccount(tx, tenantID, orgIDPtr, "kamomad", "9430")
			}
			// Kt - product inventory account (1010-series)
			inventoryAcct := findAccount(tx, tenantID, orgIDPtr, "inventory", "1010")
			if inventoryAcct == uuid.Nil {
				inventoryAcct = findAccount(tx, tenantID, orgIDPtr, "stock valuation", "1010")
			}

			if shortageAcct == uuid.Nil || inventoryAcct == uuid.Nil {
				h.log.Error("Cannot find accounts for shortage journal entry, skipping JE", "line_id", lineID)
			} else {
				jeID := uuid.New()
				entryNumber := fmt.Sprintf("SHR%06d", nextNumber)
				jeDesc := fmt.Sprintf("Kamomad: %s", reason)

				if _, err = tx.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number, entry_date,
						description, source_type, source_id, status, total_debit, total_credit,
						created_by, created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, 'inventory_shortage', $8, 'posted', $9, $9, $10, $11, $11)
				`, jeID, tenantID, orgIDPtr, journalID, entryNumber, now,
					jeDesc, lineID.String(), shortageAmount, userID, now); err != nil {
					h.log.Error("Failed to create shortage journal entry", "error", err)
					response.InternalError(c, "Failed to create journal entry")
					return
				}

				if _, err = tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, $5, 0, 1, $6)`,
					uuid.New(), jeID, shortageAcct, jeDesc, shortageAmount, now); err != nil {
					h.log.Error("Failed to insert shortage debit line", "error", err)
					response.InternalError(c, "Failed to create journal entry")
					return
				}
				if _, err = tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, 0, $5, 2, $6)`,
					uuid.New(), jeID, inventoryAcct, "Tovar hisobi", shortageAmount, now); err != nil {
					h.log.Error("Failed to insert shortage credit line", "error", err)
					response.InternalError(c, "Failed to create journal entry")
					return
				}
				if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", shortageAmount, now, shortageAcct); err != nil {
					h.log.Error("Failed to update shortage account balance", "error", err)
					response.InternalError(c, "Failed to create journal entry")
					return
				}
				if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", shortageAmount, now, inventoryAcct); err != nil {
					h.log.Error("Failed to update inventory account balance", "error", err)
					response.InternalError(c, "Failed to create journal entry")
					return
				}
				if _, err = tx.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID); err != nil {
					h.log.Error("Failed to bump journal next_number", "error", err)
					response.InternalError(c, "Failed to create journal entry")
					return
				}
			}
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit", "error", err)
		response.InternalError(c, "Failed to commit")
		return
	}

	result := gin.H{
		"success":           true,
		"resolution":        input.Resolution,
		"deduction_created": input.Resolution == "employee",
	}
	if deductionResp != nil {
		result["deduction"] = deductionResp
	}
	response.Success(c, result)
}

// ListEmployeeDeductions lists deductions for an employee
func (h *Handler) ListEmployeeDeductions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	status := c.Query("status") // optional filter

	query := `
		SELECT ed.id, ed.tenant_id, ed.organization_id, ed.employee_id, ed.amount, ed.reason,
			   ed.source_type, ed.source_id, ed.status, ed.payroll_entry_id, ed.deducted_at,
			   ed.cancelled_reason, ed.cancelled_by, ed.cancelled_at,
			   ed.created_by, ed.created_at, ed.updated_at,
			   COALESCE(e.first_name || ' ' || e.last_name, e.first_name, '') as employee_name
		FROM employee_deductions ed
		JOIN employees e ON e.id = ed.employee_id
		WHERE ed.tenant_id = $1 AND ed.employee_id = $2
	`
	args := []interface{}{tenantID, employeeID}

	if status != "" {
		query += " AND ed.status = $3"
		args = append(args, status)
	}
	query += " ORDER BY ed.created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to query deductions")
		return
	}
	defer rows.Close()

	deductions := make([]entity.EmployeeDeduction, 0)
	for rows.Next() {
		var d entity.EmployeeDeduction
		err := rows.Scan(
			&d.ID, &d.TenantID, &d.OrganizationID, &d.EmployeeID, &d.Amount, &d.Reason,
			&d.SourceType, &d.SourceID, &d.Status, &d.PayrollEntryID, &d.DeductedAt,
			&d.CancelledReason, &d.CancelledBy, &d.CancelledAt,
			&d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.EmployeeName,
		)
		if err != nil {
			h.log.Error("Failed to scan deduction", "error", err)
			continue
		}
		deductions = append(deductions, d)
	}

	response.Success(c, deductions)
}

// CancelDeduction cancels an employee deduction
func (h *Handler) CancelDeduction(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	deductionID, err := uuid.Parse(c.Param("did"))
	if err != nil {
		response.BadRequest(c, "Invalid deduction ID")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CancelDeductionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Verify deduction exists and is pending
	var status string
	var sourceID *uuid.UUID
	err = h.db.QueryRow(
		"SELECT status, source_id FROM employee_deductions WHERE id=$1 AND tenant_id=$2",
		deductionID, tenantID,
	).Scan(&status, &sourceID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Deduction")
		return
	}
	if status != "pending" {
		response.BadRequest(c, "Faqat 'pending' holatidagi kamomadni bekor qilish mumkin")
		return
	}

	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Cancel the deduction
	_, err = tx.Exec(`
		UPDATE employee_deductions SET status='cancelled', cancelled_reason=$1, cancelled_by=$2, cancelled_at=$3, updated_at=$3
		WHERE id=$4
	`, input.Reason, userID, now, deductionID)
	if err != nil {
		response.InternalError(c, "Failed to cancel deduction")
		return
	}

	// Reset the stock count line resolution back to pending
	if sourceID != nil {
		tx.Exec("UPDATE stock_count_lines SET resolution='pending', responsible_emp_id=NULL, updated_at=$1 WHERE id=$2", now, *sourceID)
	}

	if err = tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
	}

	response.Success(c, gin.H{"success": true, "message": "Kamomad bekor qilindi"})
}

// ListAllDeductions lists all deductions for the tenant (admin view)
func (h *Handler) ListAllDeductions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	query := `
		SELECT ed.id, ed.tenant_id, ed.organization_id, ed.employee_id, ed.amount, ed.reason,
			   ed.source_type, ed.source_id, ed.status, ed.payroll_entry_id, ed.deducted_at,
			   ed.cancelled_reason, ed.cancelled_by, ed.cancelled_at,
			   ed.created_by, ed.created_at, ed.updated_at,
			   COALESCE(e.first_name || ' ' || e.last_name, e.first_name, '') as employee_name
		FROM employee_deductions ed
		JOIN employees e ON e.id = ed.employee_id
		WHERE ed.tenant_id = $1
	`
	countQuery := "SELECT COUNT(*) FROM employee_deductions WHERE tenant_id = $1"
	args := []interface{}{tenantID}
	countArgs := []interface{}{tenantID}
	argIdx := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argIdx++
		query += fmt.Sprintf(" AND ed.organization_id = $%d", argIdx)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argIdx)
		args = append(args, orgID)
		countArgs = append(countArgs, orgID)
	}

	if status != "" {
		argIdx++
		query += fmt.Sprintf(" AND ed.status = $%d", argIdx)
		countQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, status)
		countArgs = append(countArgs, status)
	}

	var total int
	h.db.QueryRow(countQuery, countArgs...).Scan(&total)

	query += fmt.Sprintf(" ORDER BY ed.created_at DESC LIMIT $%d OFFSET $%d", argIdx+1, argIdx+2)
	args = append(args, limit, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to query deductions")
		return
	}
	defer rows.Close()

	deductions := make([]entity.EmployeeDeduction, 0)
	for rows.Next() {
		var d entity.EmployeeDeduction
		err := rows.Scan(
			&d.ID, &d.TenantID, &d.OrganizationID, &d.EmployeeID, &d.Amount, &d.Reason,
			&d.SourceType, &d.SourceID, &d.Status, &d.PayrollEntryID, &d.DeductedAt,
			&d.CancelledReason, &d.CancelledBy, &d.CancelledAt,
			&d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.EmployeeName,
		)
		if err != nil {
			h.log.Error("Failed to scan deduction", "error", err)
			continue
		}
		deductions = append(deductions, d)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, deductions, pagination)
}

// =====================================================
// INVENTORY LOT HANDLERS
// =====================================================

// generateLotNumber generates a lot number in format LOT-YYYY-NNN
func (h *Handler) generateLotNumber(tenantID uuid.UUID) string {
	year := time.Now().Year()
	prefix := fmt.Sprintf("LOT-%d-", year)

	var count int
	h.db.QueryRow(
		"SELECT COUNT(*) FROM inventory_lots WHERE tenant_id = $1 AND lot_number LIKE $2",
		tenantID, prefix+"%",
	).Scan(&count)

	return fmt.Sprintf("LOT-%d-%03d", year, count+1)
}

// ListInventoryLots returns a paginated list of inventory lots
func (h *Handler) ListInventoryLots(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, _ := middleware.GetOrganizationID(c)

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	productID := c.Query("product_id")
	warehouseID := c.Query("warehouse_id")
	status := c.Query("status")
	expiring := c.Query("expiring") == "true"
	expiryDays, _ := strconv.Atoi(c.DefaultQuery("expiry_days", "30"))

	// Build query
	baseQuery := `
		SELECT il.id, il.tenant_id, il.product_id, il.warehouse_id, il.location_id,
			   il.lot_number, il.serial_number, il.received_date, il.expiry_date,
			   il.manufacture_date, il.initial_quantity, il.remaining_quantity,
			   il.unit_cost, il.vendor_id, il.purchase_order_id, il.status,
			   il.notes, il.created_at, il.updated_at,
			   p.name as product_name, p.code as product_code,
			   w.name as warehouse_name
		FROM inventory_lots il
		JOIN products p ON il.product_id = p.id
		JOIN warehouses w ON il.warehouse_id = w.id
		WHERE il.tenant_id = $1
	`
	countQuery := `
		SELECT COUNT(*) FROM inventory_lots il
		JOIN products p ON il.product_id = p.id
		JOIN warehouses w ON il.warehouse_id = w.id
		WHERE il.tenant_id = $1
	`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization's warehouses
	if organizationID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND w.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND w.organization_id = $%d", argCount)
		args = append(args, organizationID)
	}

	if productID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND il.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND il.product_id = $%d", argCount)
		args = append(args, productID)
	}

	if warehouseID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND il.warehouse_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND il.warehouse_id = $%d", argCount)
		args = append(args, warehouseID)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND il.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND il.status = $%d", argCount)
		args = append(args, status)
	}

	if expiring {
		baseQuery += fmt.Sprintf(" AND il.expiry_date IS NOT NULL AND il.expiry_date <= NOW() + INTERVAL '%d days'", expiryDays)
		countQuery += fmt.Sprintf(" AND il.expiry_date IS NOT NULL AND il.expiry_date <= NOW() + INTERVAL '%d days'", expiryDays)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (il.lot_number ILIKE $%d OR il.serial_number ILIKE $%d)", argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count inventory lots", "error", err)
		response.InternalError(c, "Failed to list inventory lots")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY il.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list inventory lots", "error", err)
		response.InternalError(c, "Failed to list inventory lots")
		return
	}
	defer rows.Close()

	type LotResponse struct {
		entity.InventoryLot
		ProductName   string `json:"product_name"`
		ProductCode   string `json:"product_code"`
		WarehouseName string `json:"warehouse_name"`
	}

	lots := make([]LotResponse, 0)
	for rows.Next() {
		var lot LotResponse
		err := rows.Scan(
			&lot.ID, &lot.TenantID, &lot.ProductID, &lot.WarehouseID, &lot.LocationID,
			&lot.LotNumber, &lot.SerialNumber, &lot.ReceivedDate, &lot.ExpiryDate,
			&lot.ManufactureDate, &lot.InitialQuantity, &lot.RemainingQuantity,
			&lot.UnitCost, &lot.VendorID, &lot.PurchaseOrderID, &lot.Status,
			&lot.Notes, &lot.CreatedAt, &lot.UpdatedAt,
			&lot.ProductName, &lot.ProductCode, &lot.WarehouseName,
		)
		if err != nil {
			h.log.Error("Failed to scan inventory lot", "error", err)
			continue
		}
		lots = append(lots, lot)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, lots, pagination)
}

// GetInventoryLot returns a single inventory lot by ID
func (h *Handler) GetInventoryLot(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	lotID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lot ID")
		return
	}

	type LotResponse struct {
		entity.InventoryLot
		ProductName   string `json:"product_name"`
		ProductCode   string `json:"product_code"`
		WarehouseName string `json:"warehouse_name"`
	}

	var lot LotResponse
	err = h.db.QueryRow(`
		SELECT il.id, il.tenant_id, il.product_id, il.warehouse_id, il.location_id,
			   il.lot_number, il.serial_number, il.received_date, il.expiry_date,
			   il.manufacture_date, il.initial_quantity, il.remaining_quantity,
			   il.unit_cost, il.vendor_id, il.purchase_order_id, il.status,
			   il.notes, il.created_at, il.updated_at,
			   p.name as product_name, p.code as product_code,
			   w.name as warehouse_name
		FROM inventory_lots il
		JOIN products p ON il.product_id = p.id
		JOIN warehouses w ON il.warehouse_id = w.id
		WHERE il.id = $1 AND il.tenant_id = $2
	`, lotID, tenantID).Scan(
		&lot.ID, &lot.TenantID, &lot.ProductID, &lot.WarehouseID, &lot.LocationID,
		&lot.LotNumber, &lot.SerialNumber, &lot.ReceivedDate, &lot.ExpiryDate,
		&lot.ManufactureDate, &lot.InitialQuantity, &lot.RemainingQuantity,
		&lot.UnitCost, &lot.VendorID, &lot.PurchaseOrderID, &lot.Status,
		&lot.Notes, &lot.CreatedAt, &lot.UpdatedAt,
		&lot.ProductName, &lot.ProductCode, &lot.WarehouseName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Inventory lot")
		return
	}
	if err != nil {
		h.log.Error("Failed to get inventory lot", "error", err)
		response.InternalError(c, "Failed to get inventory lot")
		return
	}

	response.Success(c, lot)
}

// UpdateInventoryLot updates an existing inventory lot
func (h *Handler) UpdateInventoryLot(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	lotID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lot ID")
		return
	}

	var input struct {
		ExpiryDate      *string `json:"expiry_date"`
		ManufactureDate *string `json:"manufacture_date"`
		Notes           *string `json:"notes"`
		Status          *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.ExpiryDate != nil {
		if *input.ExpiryDate == "" {
			argCount++
			updates = append(updates, fmt.Sprintf("expiry_date = $%d", argCount))
			args = append(args, nil)
		} else {
			ed, err := time.Parse("2006-01-02", *input.ExpiryDate)
			if err != nil {
				response.BadRequest(c, "Invalid expiry date format, expected YYYY-MM-DD")
				return
			}
			argCount++
			updates = append(updates, fmt.Sprintf("expiry_date = $%d", argCount))
			args = append(args, ed)
		}
	}

	if input.ManufactureDate != nil {
		if *input.ManufactureDate == "" {
			argCount++
			updates = append(updates, fmt.Sprintf("manufacture_date = $%d", argCount))
			args = append(args, nil)
		} else {
			md, err := time.Parse("2006-01-02", *input.ManufactureDate)
			if err != nil {
				response.BadRequest(c, "Invalid manufacture date format, expected YYYY-MM-DD")
				return
			}
			argCount++
			updates = append(updates, fmt.Sprintf("manufacture_date = $%d", argCount))
			args = append(args, md)
		}
	}

	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		if *input.Notes == "" {
			args = append(args, nil)
		} else {
			args = append(args, *input.Notes)
		}
	}

	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, lotID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE inventory_lots SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update inventory lot", "error", err)
		response.InternalError(c, "Failed to update inventory lot")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Inventory lot")
		return
	}

	response.Success(c, gin.H{"message": "Inventory lot updated successfully"})
}

// DeleteInventoryLot deletes an inventory lot
func (h *Handler) DeleteInventoryLot(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	lotID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lot ID")
		return
	}

	// Check that the lot is available and nothing has been consumed
	var status string
	var initialQuantity, remainingQuantity float64
	err = h.db.QueryRow(
		"SELECT status, initial_quantity, remaining_quantity FROM inventory_lots WHERE id = $1 AND tenant_id = $2",
		lotID, tenantID,
	).Scan(&status, &initialQuantity, &remainingQuantity)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Inventory lot")
		return
	}
	if err != nil {
		h.log.Error("Failed to get inventory lot", "error", err)
		response.InternalError(c, "Failed to delete inventory lot")
		return
	}

	if status != "available" {
		response.BadRequest(c, "Cannot delete lot with status '"+status+"', only 'available' lots can be deleted")
		return
	}

	if math.Abs(remainingQuantity-initialQuantity) > 0.0001 {
		response.BadRequest(c, "Cannot delete lot that has been partially consumed")
		return
	}

	result, err := h.db.Exec(
		"DELETE FROM inventory_lots WHERE id = $1 AND tenant_id = $2",
		lotID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete inventory lot", "error", err)
		response.InternalError(c, "Failed to delete inventory lot")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Inventory lot")
		return
	}

	response.Success(c, gin.H{"message": "Inventory lot deleted successfully"})
}

// =====================================================
// STOCK-AT-DATE REPORT
// =====================================================
//
// GetStockAtDate replays the inventory_transactions ledger up to a
// user-supplied date and returns the on-hand quantity + weighted-average
// cost for every (product, warehouse) pair that touched stock by that
// date. Soft-deleted products are included by default — they're the
// whole point of an "as-of" report (you want to see what the warehouse
// looked like before someone deleted a SKU).
//
// Conceptually:
//
//	for each inventory_transactions row T with T.transaction_date <= as_of:
//	  signed_qty = T.quantity, signed according to T.transaction_type
//	  accumulate (T.inventory_id → product_id, warehouse_id):
//	    on_hand += signed_qty
//	    wac_basis += signed_qty * unit_cost  (only when signed_qty > 0)
//	    wac_units += signed_qty              (only when signed_qty > 0)
//	unit_cost_at_date = wac_basis / wac_units   (Odoo's default WAC method)
//
// All write paths in the codebase store `quantity` already signed (issue
// rows have negative quantities — see sales_delivery.go ~line 998), but
// the CASE in the CTE re-signs based on transaction_type as a defense
// against legacy rows / future adapters that get the sign wrong.
//
// GET /api/v1/inventory/stock-at-date
//
// Query params:
//
//	as_of            REQUIRED   YYYY-MM-DD or RFC3339. Interpreted as
//	                            end-of-day UTC when only the date part
//	                            is given so a transaction posted at
//	                            23:55 on the as_of day is still in scope.
//	warehouse_id     optional   uuid — filter to a single warehouse.
//	product_id       optional   uuid — filter to a single product.
//	include_deleted  optional   "true"/"false", default "true". The
//	                            default is intentionally different from
//	                            the live products list because a date
//	                            report that hides deleted SKUs is
//	                            useless — that's the very thing the
//	                            user wants to see at this date.
//
// Response:
//
//	{
//	  "as_of": "2026-06-01T23:59:59Z",
//	  "warehouse_id": null,
//	  "include_deleted": true,
//	  "rows": [
//	    { "product_id":..., "product_code":"...", "product_name":"...",
//	      "is_deleted":false, "warehouse_id":..., "warehouse_name":"...",
//	      "quantity":12.5, "unit_cost":150000.0, "total_value":1875000.0,
//	      "current_sales_price":175000.0,
//	      "last_txn_date":"2026-05-28T10:11:00Z" },
//	    ...
//	  ],
//	  "totals": { "products": N, "quantity": Q, "total_value": V }
//	}
func (h *Handler) GetStockAtDate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	asOfStr := strings.TrimSpace(c.Query("as_of"))
	if asOfStr == "" {
		response.BadRequest(c, "as_of query param is required (YYYY-MM-DD)")
		return
	}
	asOf, err := parseAsOfDate(asOfStr)
	if err != nil {
		response.BadRequest(c, "Invalid as_of format — use YYYY-MM-DD")
		return
	}

	// Optional filters. SQL uses NULL-aware predicates so we can pass
	// untyped nils and have the WHERE clause treat them as "no filter".
	var orgArg, whArg, prodArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgArg = orgID
	}
	if v := strings.TrimSpace(c.Query("warehouse_id")); v != "" {
		whArg = v
	}
	if v := strings.TrimSpace(c.Query("product_id")); v != "" {
		prodArg = v
	}
	includeDeleted := !strings.EqualFold(c.DefaultQuery("include_deleted", "true"), "false")

	rows, err := h.db.Query(stockAtDateQuery,
		tenantID, asOf, orgArg, whArg, prodArg, includeDeleted)
	if err != nil {
		h.log.Error("GetStockAtDate: query failed", "error", err)
		response.InternalError(c, "Failed to compute stock at date")
		return
	}
	defer rows.Close()

	type StockAtDateRow struct {
		ProductID         uuid.UUID  `json:"product_id"`
		ProductCode       string     `json:"product_code"`
		ProductName       string     `json:"product_name"`
		IsDeleted         bool       `json:"is_deleted"`
		WarehouseID       uuid.UUID  `json:"warehouse_id"`
		WarehouseName     string     `json:"warehouse_name"`
		Quantity          float64    `json:"quantity"`
		UnitCost          float64    `json:"unit_cost"`
		TotalValue        float64    `json:"total_value"`
		CurrentSalesPrice float64    `json:"current_sales_price"`
		LastTxnDate       *time.Time `json:"last_txn_date,omitempty"`
	}

	out := make([]StockAtDateRow, 0, 256)
	var totalQty, totalValue float64
	for rows.Next() {
		var r StockAtDateRow
		var last sql.NullTime
		if err := rows.Scan(
			&r.ProductID, &r.ProductCode, &r.ProductName, &r.IsDeleted,
			&r.WarehouseID, &r.WarehouseName,
			&r.Quantity, &r.UnitCost, &r.TotalValue,
			&r.CurrentSalesPrice, &last,
		); err != nil {
			h.log.Error("GetStockAtDate: scan failed", "error", err)
			continue
		}
		if last.Valid {
			r.LastTxnDate = &last.Time
		}
		totalQty += r.Quantity
		totalValue += r.TotalValue
		out = append(out, r)
	}

	response.Success(c, gin.H{
		"as_of":           asOf,
		"warehouse_id":    whArg,
		"product_id":      prodArg,
		"include_deleted": includeDeleted,
		"rows":            out,
		"totals": gin.H{
			"products":    len(out),
			"quantity":    totalQty,
			"total_value": totalValue,
		},
	})
}

// parseAsOfDate accepts "YYYY-MM-DD" (interpreted as 23:59:59 UTC of
// that day so a same-day transaction at 23:30 is included) or any
// RFC3339 timestamp (used verbatim).
func parseAsOfDate(s string) (time.Time, error) {
	// Try date-only first — most common UI input.
	if t, err := time.Parse("2006-01-02", s); err == nil {
		// Roll to end-of-day UTC. Picking UTC matches how Postgres compares
		// timestamptz columns against a literal — keeps the math
		// timezone-stable across the user's local clock.
		return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC), nil
	}
	// Then RFC3339 (allows passing an exact timestamp from automation).
	return time.Parse(time.RFC3339, s)
}

// stockAtDateQuery — see GetStockAtDate above for the full annotation.
// Param order:  $1 tenantID  $2 as_of  $3 orgID  $4 warehouseID
//
//	$5 productID $6 include_deleted
const stockAtDateQuery = `
WITH ledger AS (
    SELECT
        inv.product_id,
        inv.warehouse_id,
        CASE
            WHEN t.transaction_type IN (
                'issue', 'sale', 'ship', 'delivery',
                'adjustment_out', 'transfer_out',
                'consume', 'production_out', 'write_off', 'scrap'
            ) THEN -ABS(t.quantity)
            ELSE t.quantity
        END AS signed_qty,
        t.unit_cost,
        t.transaction_date
    FROM inventory_transactions t
    JOIN inventory inv ON inv.id = t.inventory_id
    WHERE t.tenant_id = $1
      AND t.transaction_date <= $2
      AND ($3::uuid IS NULL OR inv.organization_id = $3)
      AND ($4::uuid IS NULL OR inv.warehouse_id  = $4)
      AND ($5::uuid IS NULL OR inv.product_id    = $5)
),
agg AS (
    SELECT
        product_id,
        warehouse_id,
        SUM(signed_qty)                                                     AS quantity_at_date,
        MAX(transaction_date)                                                AS last_txn_date,
        CASE
            WHEN SUM(CASE WHEN signed_qty > 0 THEN signed_qty ELSE 0 END) > 0
            THEN SUM(CASE WHEN signed_qty > 0 THEN signed_qty * COALESCE(unit_cost, 0) ELSE 0 END)
                 / SUM(CASE WHEN signed_qty > 0 THEN signed_qty ELSE 0 END)
            ELSE 0
        END AS wac_unit_cost
    FROM ledger
    GROUP BY product_id, warehouse_id
)
SELECT
    p.id                                                                      AS product_id,
    COALESCE(p.code, '')                                                      AS product_code,
    p.name                                                                    AS product_name,
    (p.deleted_at IS NOT NULL)                                                AS is_deleted,
    w.id                                                                      AS warehouse_id,
    w.name                                                                    AS warehouse_name,
    COALESCE(a.quantity_at_date, 0)                                           AS quantity,
    COALESCE(NULLIF(a.wac_unit_cost, 0), p.cost_price, 0)                     AS unit_cost,
    COALESCE(a.quantity_at_date, 0)
        * COALESCE(NULLIF(a.wac_unit_cost, 0), p.cost_price, 0)               AS total_value,
    -- products.list_price is the canonical sales-price column (see
    -- migration 002_erp_modules.sql and 171_product_organization_settings).
    -- There is no unit_price column on this table.
    COALESCE(p.list_price, 0)                                                 AS current_sales_price,
    a.last_txn_date
FROM agg a
JOIN products   p ON p.id = a.product_id
JOIN warehouses w ON w.id = a.warehouse_id
WHERE ($6 = TRUE OR p.deleted_at IS NULL)
ORDER BY p.name ASC, w.name ASC
`

// GetInventoryTurnover returns a per-(product, warehouse) turnover sheet for a
// date range: opening balance (everything before date_from), kirim/chiqim that
// happened during the period, and the closing balance + weighted-average cost
// as of date_to. It replays the inventory_transactions ledger exactly like
// GetStockAtDate, but splits the running total at the period boundary so the
// user sees how much came in and went out — not just the end snapshot.
//
// Query params:
//
//	date_from        — YYYY-MM-DD (required) period start, inclusive
//	date_to          — YYYY-MM-DD (required) period end, inclusive
//	warehouse_id     — uuid, filter to a single warehouse
//	product_id       — uuid, filter to a single product
//	include_deleted  — bool, default true (show SKUs deleted since)
func (h *Handler) GetInventoryTurnover(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	fromStr := strings.TrimSpace(c.Query("date_from"))
	toStr := strings.TrimSpace(c.Query("date_to"))
	if fromStr == "" || toStr == "" {
		response.BadRequest(c, "date_from and date_to query params are required (YYYY-MM-DD)")
		return
	}
	// date_from is the START of its day; date_to is the END of its day. That
	// makes the period [from 00:00:00, to 23:59:59] fully inclusive and the
	// opening balance everything strictly before date_from.
	fromTime, err := parseDayStart(fromStr)
	if err != nil {
		response.BadRequest(c, "Invalid date_from format — use YYYY-MM-DD")
		return
	}
	toTime, err := parseAsOfDate(toStr)
	if err != nil {
		response.BadRequest(c, "Invalid date_to format — use YYYY-MM-DD")
		return
	}
	if toTime.Before(fromTime) {
		response.BadRequest(c, "date_to must not be earlier than date_from")
		return
	}

	// Optional filters. SQL uses NULL-aware predicates so untyped nils are
	// treated as "no filter".
	var orgArg, whArg, prodArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgArg = orgID
	}
	if v := strings.TrimSpace(c.Query("warehouse_id")); v != "" {
		whArg = v
	}
	if v := strings.TrimSpace(c.Query("product_id")); v != "" {
		prodArg = v
	}
	includeDeleted := !strings.EqualFold(c.DefaultQuery("include_deleted", "true"), "false")

	rows, err := h.db.Query(stockTurnoverQuery,
		tenantID, fromTime, toTime, orgArg, whArg, prodArg, includeDeleted)
	if err != nil {
		h.log.Error("GetInventoryTurnover: query failed", "error", err)
		response.InternalError(c, "Failed to compute inventory turnover")
		return
	}
	defer rows.Close()

	type TurnoverRow struct {
		ProductID         uuid.UUID  `json:"product_id"`
		ProductCode       string     `json:"product_code"`
		ProductName       string     `json:"product_name"`
		IsDeleted         bool       `json:"is_deleted"`
		WarehouseID       uuid.UUID  `json:"warehouse_id"`
		WarehouseName     string     `json:"warehouse_name"`
		OpeningQty        float64    `json:"opening_qty"`
		InQty             float64    `json:"in_qty"`
		InValue           float64    `json:"in_value"`
		OutQty            float64    `json:"out_qty"`
		OutValue          float64    `json:"out_value"`
		ClosingQty        float64    `json:"closing_qty"`
		UnitCost          float64    `json:"unit_cost"`
		ClosingValue      float64    `json:"closing_value"`
		CurrentSalesPrice float64    `json:"current_sales_price"`
		LastTxnDate       *time.Time `json:"last_txn_date,omitempty"`
	}

	out := make([]TurnoverRow, 0, 256)
	var totInQty, totInVal, totOutQty, totOutVal, totClosingVal float64
	for rows.Next() {
		var r TurnoverRow
		var last sql.NullTime
		if err := rows.Scan(
			&r.ProductID, &r.ProductCode, &r.ProductName, &r.IsDeleted,
			&r.WarehouseID, &r.WarehouseName,
			&r.OpeningQty, &r.InQty, &r.InValue, &r.OutQty, &r.OutValue,
			&r.ClosingQty, &r.UnitCost, &r.ClosingValue,
			&r.CurrentSalesPrice, &last,
		); err != nil {
			h.log.Error("GetInventoryTurnover: scan failed", "error", err)
			continue
		}
		if last.Valid {
			r.LastTxnDate = &last.Time
		}
		totInQty += r.InQty
		totInVal += r.InValue
		totOutQty += r.OutQty
		totOutVal += r.OutValue
		totClosingVal += r.ClosingValue
		out = append(out, r)
	}

	response.Success(c, gin.H{
		"date_from":       fromTime,
		"date_to":         toTime,
		"warehouse_id":    whArg,
		"product_id":      prodArg,
		"include_deleted": includeDeleted,
		"rows":            out,
		"totals": gin.H{
			"products":      len(out),
			"in_qty":        totInQty,
			"in_value":      totInVal,
			"out_qty":       totOutQty,
			"out_value":     totOutVal,
			"closing_value": totClosingVal,
		},
	})
}

// parseDayStart accepts "YYYY-MM-DD" and returns 00:00:00 UTC of that day so a
// transaction stamped at 00:05 on date_from still falls inside the period.
// Falls back to RFC3339 for an exact timestamp passed by automation.
func parseDayStart(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return time.Parse(time.RFC3339, s)
}

// stockTurnoverQuery — see GetInventoryTurnover above.
// Param order:  $1 tenantID  $2 date_from(start)  $3 date_to(end)
//
//	$4 orgID      $5 warehouseID        $6 productID
//	$7 include_deleted
const stockTurnoverQuery = `
WITH ledger AS (
    SELECT
        inv.product_id,
        inv.warehouse_id,
        CASE
            WHEN t.transaction_type IN (
                'issue', 'sale', 'ship', 'delivery',
                'adjustment_out', 'transfer_out',
                'consume', 'production_out', 'write_off', 'scrap'
            ) THEN -ABS(t.quantity)
            ELSE t.quantity
        END AS signed_qty,
        t.unit_cost,
        t.transaction_date
    FROM inventory_transactions t
    JOIN inventory inv ON inv.id = t.inventory_id
    WHERE t.tenant_id = $1
      AND t.transaction_date <= $3
      AND ($4::uuid IS NULL OR inv.organization_id = $4)
      AND ($5::uuid IS NULL OR inv.warehouse_id  = $5)
      AND ($6::uuid IS NULL OR inv.product_id    = $6)
),
agg AS (
    SELECT
        product_id,
        warehouse_id,
        SUM(CASE WHEN transaction_date <  $2 THEN signed_qty ELSE 0 END)                      AS opening_qty,
        SUM(CASE WHEN transaction_date >= $2 AND signed_qty > 0 THEN signed_qty ELSE 0 END)   AS in_qty,
        SUM(CASE WHEN transaction_date >= $2 AND signed_qty > 0 THEN signed_qty * COALESCE(unit_cost, 0) ELSE 0 END)  AS in_value,
        SUM(CASE WHEN transaction_date >= $2 AND signed_qty < 0 THEN -signed_qty ELSE 0 END)  AS out_qty,
        SUM(CASE WHEN transaction_date >= $2 AND signed_qty < 0 THEN -signed_qty * COALESCE(unit_cost, 0) ELSE 0 END) AS out_value,
        SUM(signed_qty)                                                                       AS closing_qty,
        MAX(transaction_date)                                                                 AS last_txn_date,
        CASE
            WHEN SUM(CASE WHEN signed_qty > 0 THEN signed_qty ELSE 0 END) > 0
            THEN SUM(CASE WHEN signed_qty > 0 THEN signed_qty * COALESCE(unit_cost, 0) ELSE 0 END)
                 / SUM(CASE WHEN signed_qty > 0 THEN signed_qty ELSE 0 END)
            ELSE 0
        END AS wac_unit_cost
    FROM ledger
    GROUP BY product_id, warehouse_id
)
SELECT
    p.id                                                                      AS product_id,
    COALESCE(p.code, '')                                                      AS product_code,
    p.name                                                                    AS product_name,
    (p.deleted_at IS NOT NULL)                                                AS is_deleted,
    w.id                                                                      AS warehouse_id,
    w.name                                                                    AS warehouse_name,
    COALESCE(a.opening_qty, 0)                                                AS opening_qty,
    COALESCE(a.in_qty, 0)                                                     AS in_qty,
    COALESCE(a.in_value, 0)                                                   AS in_value,
    COALESCE(a.out_qty, 0)                                                    AS out_qty,
    COALESCE(a.out_value, 0)                                                  AS out_value,
    COALESCE(a.closing_qty, 0)                                                AS closing_qty,
    COALESCE(NULLIF(a.wac_unit_cost, 0), p.cost_price, 0)                     AS unit_cost,
    COALESCE(a.closing_qty, 0)
        * COALESCE(NULLIF(a.wac_unit_cost, 0), p.cost_price, 0)               AS closing_value,
    COALESCE(p.list_price, 0)                                                 AS current_sales_price,
    a.last_txn_date
FROM agg a
JOIN products   p ON p.id = a.product_id
JOIN warehouses w ON w.id = a.warehouse_id
WHERE ($7 = TRUE OR p.deleted_at IS NULL)
  -- Drop rows that had no opening, no movement, and no closing — pure noise.
  AND (a.opening_qty <> 0 OR a.in_qty <> 0 OR a.out_qty <> 0 OR a.closing_qty <> 0)
ORDER BY p.name ASC, w.name ASC
`
