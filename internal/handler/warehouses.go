package handler

import (
	"database/sql"
	"encoding/json"
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
// WAREHOUSE HANDLERS
// =====================================================

// ListWarehouses returns a paginated list of warehouses
func (h *Handler) ListWarehouses(c *gin.Context) {
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
	includeInactive := c.Query("include_inactive") == "true"

	// Build query
	baseQuery := `
		SELECT w.id, w.tenant_id, w.organization_id, w.code, w.name, w.address,
			   w.manager_id, w.is_default, w.is_active, w.created_at, w.updated_at,
			   e.first_name as manager_first_name, e.last_name as manager_last_name
		FROM warehouses w
		LEFT JOIN employees e ON w.manager_id = e.id
		WHERE w.tenant_id = $1 AND w.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM warehouses w WHERE w.tenant_id = $1 AND w.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if !includeInactive {
		baseQuery += " AND w.is_active = true"
		countQuery += " AND w.is_active = true"
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (w.code ILIKE $%d OR w.name ILIKE $%d)", argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count warehouses", "error", err)
		response.InternalError(c, "Failed to list warehouses")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY w.is_default DESC, w.code ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list warehouses", "error", err)
		response.InternalError(c, "Failed to list warehouses")
		return
	}
	defer rows.Close()

	type WarehouseResponse struct {
		ID             uuid.UUID  `json:"id"`
		TenantID       uuid.UUID  `json:"tenant_id"`
		OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
		Code           string     `json:"code"`
		Name           string     `json:"name"`
		Address        *entity.Address `json:"address,omitempty"`
		ManagerID      *uuid.UUID `json:"manager_id,omitempty"`
		ManagerName    string     `json:"manager_name,omitempty"`
		IsDefault      bool       `json:"is_default"`
		IsActive       bool       `json:"is_active"`
		CreatedAt      time.Time  `json:"created_at"`
		UpdatedAt      time.Time  `json:"updated_at"`
	}

	warehouses := make([]*WarehouseResponse, 0)
	for rows.Next() {
		var w entity.Warehouse
		var orgID, managerID sql.NullString
		var addressJSON []byte
		var managerFirstName, managerLastName sql.NullString

		err := rows.Scan(
			&w.ID, &w.TenantID, &orgID, &w.Code, &w.Name, &addressJSON,
			&managerID, &w.IsDefault, &w.IsActive, &w.CreatedAt, &w.UpdatedAt,
			&managerFirstName, &managerLastName,
		)
		if err != nil {
			h.log.Error("Failed to scan warehouse", "error", err)
			continue
		}

		resp := &WarehouseResponse{
			ID:        w.ID,
			TenantID:  w.TenantID,
			Code:      w.Code,
			Name:      w.Name,
			IsDefault: w.IsDefault,
			IsActive:  w.IsActive,
			CreatedAt: w.CreatedAt,
			UpdatedAt: w.UpdatedAt,
		}

		if orgID.Valid {
			oid, _ := uuid.Parse(orgID.String)
			resp.OrganizationID = &oid
		}
		if managerID.Valid {
			mid, _ := uuid.Parse(managerID.String)
			resp.ManagerID = &mid
		}
		if managerFirstName.Valid || managerLastName.Valid {
			resp.ManagerName = strings.TrimSpace(managerFirstName.String + " " + managerLastName.String)
		}
		if len(addressJSON) > 0 && string(addressJSON) != "null" {
			var addr entity.Address
			if json.Unmarshal(addressJSON, &addr) == nil {
				resp.Address = &addr
			}
		}

		warehouses = append(warehouses, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, warehouses, pagination)
}

// CreateWarehouse creates a new warehouse
func (h *Handler) CreateWarehouse(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateWarehouseInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check for duplicate code
	var codeExists bool
	err := h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM warehouses WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)",
		tenantID, input.Code,
	).Scan(&codeExists)
	if err != nil {
		h.log.Error("Failed to check warehouse code", "error", err)
		response.InternalError(c, "Failed to create warehouse")
		return
	}
	if codeExists {
		response.Conflict(c, "Warehouse with this code already exists")
		return
	}

	// Parse optional manager ID
	var managerID *uuid.UUID
	if input.ManagerID != "" {
		mid, err := uuid.Parse(input.ManagerID)
		if err == nil {
			managerID = &mid
		}
	}

	// Serialize address
	var addressJSON []byte
	if input.Address != nil {
		addressJSON, _ = json.Marshal(input.Address)
	}

	id := uuid.New()
	now := time.Now()

	// If this is default, unset other defaults first
	if input.IsDefault {
		_, err := h.db.Exec(
			"UPDATE warehouses SET is_default = false WHERE tenant_id = $1 AND is_default = true",
			tenantID,
		)
		if err != nil {
			h.log.Error("Failed to unset default warehouses", "error", err)
		}
	}

	query := `
		INSERT INTO warehouses (id, tenant_id, code, name, address, manager_id, is_default, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`

	err = h.db.QueryRow(query,
		id, tenantID, input.Code, input.Name, addressJSON, managerID, input.IsDefault, true, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create warehouse", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Warehouse with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create warehouse")
		return
	}

	resp := &entity.Warehouse{
		ID:        id,
		TenantID:  tenantID,
		Code:      input.Code,
		Name:      input.Name,
		Address:   input.Address,
		ManagerID: managerID,
		IsDefault: input.IsDefault,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	response.Created(c, resp)
}

// GetWarehouse returns a single warehouse by ID
func (h *Handler) GetWarehouse(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	query := `
		SELECT w.id, w.tenant_id, w.organization_id, w.code, w.name, w.address,
			   w.manager_id, w.is_default, w.is_active, w.created_at, w.updated_at,
			   e.first_name as manager_first_name, e.last_name as manager_last_name
		FROM warehouses w
		LEFT JOIN employees e ON w.manager_id = e.id
		WHERE w.id = $1 AND w.tenant_id = $2 AND w.deleted_at IS NULL
	`

	var w entity.Warehouse
	var orgID, managerID sql.NullString
	var addressJSON []byte
	var managerFirstName, managerLastName sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&w.ID, &w.TenantID, &orgID, &w.Code, &w.Name, &addressJSON,
		&managerID, &w.IsDefault, &w.IsActive, &w.CreatedAt, &w.UpdatedAt,
		&managerFirstName, &managerLastName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Warehouse")
		return
	}
	if err != nil {
		h.log.Error("Failed to get warehouse", "error", err)
		response.InternalError(c, "Failed to get warehouse")
		return
	}

	type WarehouseDetailResponse struct {
		ID             uuid.UUID              `json:"id"`
		TenantID       uuid.UUID              `json:"tenant_id"`
		OrganizationID *uuid.UUID             `json:"organization_id,omitempty"`
		Code           string                 `json:"code"`
		Name           string                 `json:"name"`
		Address        *entity.Address        `json:"address,omitempty"`
		ManagerID      *uuid.UUID             `json:"manager_id,omitempty"`
		ManagerName    string                 `json:"manager_name,omitempty"`
		IsDefault      bool                   `json:"is_default"`
		IsActive       bool                   `json:"is_active"`
		CreatedAt      time.Time              `json:"created_at"`
		UpdatedAt      time.Time              `json:"updated_at"`
		Locations      []entity.WarehouseLocation `json:"locations,omitempty"`
	}

	resp := &WarehouseDetailResponse{
		ID:        w.ID,
		TenantID:  w.TenantID,
		Code:      w.Code,
		Name:      w.Name,
		IsDefault: w.IsDefault,
		IsActive:  w.IsActive,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
	}

	if orgID.Valid {
		oid, _ := uuid.Parse(orgID.String)
		resp.OrganizationID = &oid
	}
	if managerID.Valid {
		mid, _ := uuid.Parse(managerID.String)
		resp.ManagerID = &mid
	}
	if managerFirstName.Valid || managerLastName.Valid {
		resp.ManagerName = strings.TrimSpace(managerFirstName.String + " " + managerLastName.String)
	}
	if len(addressJSON) > 0 && string(addressJSON) != "null" {
		var addr entity.Address
		if json.Unmarshal(addressJSON, &addr) == nil {
			resp.Address = &addr
		}
	}

	// Fetch locations
	locQuery := `
		SELECT id, warehouse_id, parent_id, code, name, type, aisle, rack, shelf, bin, capacity, is_active, created_at, updated_at
		FROM warehouse_locations
		WHERE warehouse_id = $1 AND deleted_at IS NULL
		ORDER BY code ASC
	`
	locRows, err := h.db.Query(locQuery, id)
	if err == nil {
		defer locRows.Close()
		locations := make([]entity.WarehouseLocation, 0)
		for locRows.Next() {
			var loc entity.WarehouseLocation
			var parentID, aisle, rack, shelf, bin sql.NullString
			var capacity sql.NullFloat64

			err := locRows.Scan(
				&loc.ID, &loc.WarehouseID, &parentID, &loc.Code, &loc.Name, &loc.Type,
				&aisle, &rack, &shelf, &bin, &capacity, &loc.IsActive, &loc.CreatedAt, &loc.UpdatedAt,
			)
			if err != nil {
				continue
			}

			if parentID.Valid {
				pid, _ := uuid.Parse(parentID.String)
				loc.ParentID = &pid
			}
			if aisle.Valid {
				loc.Aisle = &aisle.String
			}
			if rack.Valid {
				loc.Rack = &rack.String
			}
			if shelf.Valid {
				loc.Shelf = &shelf.String
			}
			if bin.Valid {
				loc.Bin = &bin.String
			}
			if capacity.Valid {
				loc.Capacity = &capacity.Float64
			}

			locations = append(locations, loc)
		}
		resp.Locations = locations
	}

	response.Success(c, resp)
}

// UpdateWarehouse updates an existing warehouse
func (h *Handler) UpdateWarehouse(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	var input entity.UpdateWarehouseInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build dynamic update
	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.Address != nil {
		addressJSON, _ := json.Marshal(input.Address)
		addUpdate("address", addressJSON)
	}
	if input.ManagerID != nil {
		if *input.ManagerID == "" {
			addUpdate("manager_id", nil)
		} else {
			mid, _ := uuid.Parse(*input.ManagerID)
			addUpdate("manager_id", mid)
		}
	}
	if input.IsDefault != nil {
		// If setting as default, unset other defaults first
		if *input.IsDefault {
			h.db.Exec(
				"UPDATE warehouses SET is_default = false WHERE tenant_id = $1 AND is_default = true AND id != $2",
				tenantID, id,
			)
		}
		addUpdate("is_default", *input.IsDefault)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addUpdate("updated_at", time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE warehouses SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Warehouse")
		return
	}
	if err != nil {
		h.log.Error("Failed to update warehouse", "error", err)
		response.InternalError(c, "Failed to update warehouse")
		return
	}

	// Return updated warehouse
	h.GetWarehouse(c)
}

// DeleteWarehouse soft-deletes a warehouse
func (h *Handler) DeleteWarehouse(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	// Check for existing inventory
	var hasInventory bool
	h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM inventory WHERE warehouse_id = $1 AND quantity_on_hand > 0)
	`, id).Scan(&hasInventory)

	if hasInventory {
		response.BadRequest(c, "Cannot delete warehouse with existing inventory. Set to inactive instead.")
		return
	}

	// Check if this is the only warehouse
	var warehouseCount int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL
	`, tenantID).Scan(&warehouseCount)

	if warehouseCount <= 1 {
		response.BadRequest(c, "Cannot delete the only warehouse. Create another warehouse first.")
		return
	}

	query := `
		UPDATE warehouses SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete warehouse", "error", err)
		response.InternalError(c, "Failed to delete warehouse")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Warehouse")
		return
	}

	// Also soft-delete associated locations
	h.db.Exec(`
		UPDATE warehouse_locations SET deleted_at = $1, updated_at = $1
		WHERE warehouse_id = $2 AND deleted_at IS NULL
	`, time.Now(), id)

	response.NoContent(c)
}

// =====================================================
// WAREHOUSE LOCATION HANDLERS
// =====================================================

// ListWarehouseLocations returns locations for a warehouse
func (h *Handler) ListWarehouseLocations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	warehouseIDStr := c.Param("id")
	warehouseID, err := uuid.Parse(warehouseIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	// Verify warehouse belongs to tenant
	var exists bool
	h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		warehouseID, tenantID,
	).Scan(&exists)
	if !exists {
		response.NotFound(c, "Warehouse")
		return
	}

	includeInactive := c.Query("include_inactive") == "true"
	locationType := c.Query("type")
	flat := c.Query("flat") == "true"

	query := `
		SELECT id, warehouse_id, parent_id, code, name, type, aisle, rack, shelf, bin, capacity, is_active, created_at, updated_at
		FROM warehouse_locations
		WHERE warehouse_id = $1 AND deleted_at IS NULL
	`
	args := []interface{}{warehouseID}
	argCount := 1

	if !includeInactive {
		query += " AND is_active = true"
	}

	if locationType != "" {
		argCount++
		query += fmt.Sprintf(" AND type = $%d", argCount)
		args = append(args, locationType)
	}

	query += " ORDER BY code ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list warehouse locations", "error", err)
		response.InternalError(c, "Failed to list warehouse locations")
		return
	}
	defer rows.Close()

	locations := make([]*entity.WarehouseLocation, 0)
	locationMap := make(map[uuid.UUID]*entity.WarehouseLocation)

	for rows.Next() {
		var loc entity.WarehouseLocation
		var parentID, aisle, rack, shelf, bin sql.NullString
		var capacity sql.NullFloat64

		err := rows.Scan(
			&loc.ID, &loc.WarehouseID, &parentID, &loc.Code, &loc.Name, &loc.Type,
			&aisle, &rack, &shelf, &bin, &capacity, &loc.IsActive, &loc.CreatedAt, &loc.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if parentID.Valid {
			pid, _ := uuid.Parse(parentID.String)
			loc.ParentID = &pid
		}
		if aisle.Valid {
			loc.Aisle = &aisle.String
		}
		if rack.Valid {
			loc.Rack = &rack.String
		}
		if shelf.Valid {
			loc.Shelf = &shelf.String
		}
		if bin.Valid {
			loc.Bin = &bin.String
		}
		if capacity.Valid {
			loc.Capacity = &capacity.Float64
		}

		locations = append(locations, &loc)
		locationMap[loc.ID] = &loc
	}

	if flat {
		response.Success(c, locations)
		return
	}

	// Build tree structure
	type LocationNode struct {
		entity.WarehouseLocation
		Children []*LocationNode `json:"children,omitempty"`
	}

	nodeMap := make(map[uuid.UUID]*LocationNode)
	for _, loc := range locations {
		node := &LocationNode{
			WarehouseLocation: *loc,
			Children:          make([]*LocationNode, 0),
		}
		nodeMap[loc.ID] = node
	}

	rootLocations := make([]*LocationNode, 0)
	for _, node := range nodeMap {
		if node.ParentID == nil {
			rootLocations = append(rootLocations, node)
		} else {
			if parent, ok := nodeMap[*node.ParentID]; ok {
				parent.Children = append(parent.Children, node)
			}
		}
	}

	response.Success(c, rootLocations)
}

// CreateWarehouseLocation creates a new location in a warehouse
func (h *Handler) CreateWarehouseLocation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	warehouseIDStr := c.Param("id")
	warehouseID, err := uuid.Parse(warehouseIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	// Verify warehouse belongs to tenant
	var exists bool
	h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		warehouseID, tenantID,
	).Scan(&exists)
	if !exists {
		response.NotFound(c, "Warehouse")
		return
	}

	type Input struct {
		ParentID string                       `json:"parent_id,omitempty"`
		Code     string                       `json:"code" binding:"required,min=1,max=50"`
		Name     string                       `json:"name" binding:"required,min=1,max=255"`
		Type     entity.WarehouseLocationType `json:"type,omitempty"`
		Aisle    string                       `json:"aisle,omitempty"`
		Rack     string                       `json:"rack,omitempty"`
		Shelf    string                       `json:"shelf,omitempty"`
		Bin      string                       `json:"bin,omitempty"`
		Capacity *float64                     `json:"capacity,omitempty"`
	}

	var input Input
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check for duplicate code within warehouse
	var codeExists bool
	h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM warehouse_locations WHERE warehouse_id = $1 AND code = $2 AND deleted_at IS NULL)",
		warehouseID, input.Code,
	).Scan(&codeExists)
	if codeExists {
		response.Conflict(c, "Location with this code already exists in this warehouse")
		return
	}

	// Default type
	if input.Type == "" {
		input.Type = entity.LocationTypeStorage
	}

	// Parse optional parent ID
	var parentID *uuid.UUID
	if input.ParentID != "" {
		pid, err := uuid.Parse(input.ParentID)
		if err == nil {
			parentID = &pid
		}
	}

	// Prepare optional fields
	var aisle, rack, shelf, bin *string
	if input.Aisle != "" {
		aisle = &input.Aisle
	}
	if input.Rack != "" {
		rack = &input.Rack
	}
	if input.Shelf != "" {
		shelf = &input.Shelf
	}
	if input.Bin != "" {
		bin = &input.Bin
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO warehouse_locations (id, warehouse_id, parent_id, code, name, type, aisle, rack, shelf, bin, capacity, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id
	`

	err = h.db.QueryRow(query,
		id, warehouseID, parentID, input.Code, input.Name, input.Type,
		aisle, rack, shelf, bin, input.Capacity, true, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create warehouse location", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Location with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create warehouse location")
		return
	}

	loc := &entity.WarehouseLocation{
		ID:          id,
		WarehouseID: warehouseID,
		ParentID:    parentID,
		Code:        input.Code,
		Name:        input.Name,
		Type:        input.Type,
		Aisle:       aisle,
		Rack:        rack,
		Shelf:       shelf,
		Bin:         bin,
		Capacity:    input.Capacity,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	response.Created(c, loc)
}

// UpdateWarehouseLocation updates a warehouse location
func (h *Handler) UpdateWarehouseLocation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	warehouseIDStr := c.Param("id")
	warehouseID, err := uuid.Parse(warehouseIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	locationIDStr := c.Param("locationId")
	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid location ID")
		return
	}

	// Verify warehouse belongs to tenant
	var exists bool
	h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		warehouseID, tenantID,
	).Scan(&exists)
	if !exists {
		response.NotFound(c, "Warehouse")
		return
	}

	type Input struct {
		Name     *string                       `json:"name,omitempty"`
		Type     *entity.WarehouseLocationType `json:"type,omitempty"`
		ParentID *string                       `json:"parent_id,omitempty"`
		Aisle    *string                       `json:"aisle,omitempty"`
		Rack     *string                       `json:"rack,omitempty"`
		Shelf    *string                       `json:"shelf,omitempty"`
		Bin      *string                       `json:"bin,omitempty"`
		Capacity *float64                      `json:"capacity,omitempty"`
		IsActive *bool                         `json:"is_active,omitempty"`
	}

	var input Input
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.Type != nil {
		addUpdate("type", *input.Type)
	}
	if input.ParentID != nil {
		if *input.ParentID == "" {
			addUpdate("parent_id", nil)
		} else {
			pid, _ := uuid.Parse(*input.ParentID)
			addUpdate("parent_id", pid)
		}
	}
	if input.Aisle != nil {
		addUpdate("aisle", *input.Aisle)
	}
	if input.Rack != nil {
		addUpdate("rack", *input.Rack)
	}
	if input.Shelf != nil {
		addUpdate("shelf", *input.Shelf)
	}
	if input.Bin != nil {
		addUpdate("bin", *input.Bin)
	}
	if input.Capacity != nil {
		addUpdate("capacity", *input.Capacity)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addUpdate("updated_at", time.Now())

	argCount++
	args = append(args, locationID)
	argCount++
	args = append(args, warehouseID)

	query := fmt.Sprintf(`
		UPDATE warehouse_locations SET %s
		WHERE id = $%d AND warehouse_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Location")
		return
	}
	if err != nil {
		h.log.Error("Failed to update warehouse location", "error", err)
		response.InternalError(c, "Failed to update warehouse location")
		return
	}

	// Fetch and return updated location
	var loc entity.WarehouseLocation
	var parentID, aisle, rack, shelf, bin sql.NullString
	var capacity sql.NullFloat64

	h.db.QueryRow(`
		SELECT id, warehouse_id, parent_id, code, name, type, aisle, rack, shelf, bin, capacity, is_active, created_at, updated_at
		FROM warehouse_locations WHERE id = $1
	`, returnedID).Scan(
		&loc.ID, &loc.WarehouseID, &parentID, &loc.Code, &loc.Name, &loc.Type,
		&aisle, &rack, &shelf, &bin, &capacity, &loc.IsActive, &loc.CreatedAt, &loc.UpdatedAt,
	)

	if parentID.Valid {
		pid, _ := uuid.Parse(parentID.String)
		loc.ParentID = &pid
	}
	if aisle.Valid {
		loc.Aisle = &aisle.String
	}
	if rack.Valid {
		loc.Rack = &rack.String
	}
	if shelf.Valid {
		loc.Shelf = &shelf.String
	}
	if bin.Valid {
		loc.Bin = &bin.String
	}
	if capacity.Valid {
		loc.Capacity = &capacity.Float64
	}

	response.Success(c, loc)
}

// DeleteWarehouseLocation soft-deletes a warehouse location
func (h *Handler) DeleteWarehouseLocation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	warehouseIDStr := c.Param("id")
	warehouseID, err := uuid.Parse(warehouseIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
		return
	}

	locationIDStr := c.Param("locationId")
	locationID, err := uuid.Parse(locationIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid location ID")
		return
	}

	// Verify warehouse belongs to tenant
	var exists bool
	h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		warehouseID, tenantID,
	).Scan(&exists)
	if !exists {
		response.NotFound(c, "Warehouse")
		return
	}

	// Check for inventory at this location
	var hasInventory bool
	h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM inventory WHERE location_id = $1 AND quantity_on_hand > 0)
	`, locationID).Scan(&hasInventory)

	if hasInventory {
		response.BadRequest(c, "Cannot delete location with existing inventory. Move inventory first.")
		return
	}

	// Check for child locations
	var hasChildren bool
	h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM warehouse_locations WHERE parent_id = $1 AND deleted_at IS NULL)
	`, locationID).Scan(&hasChildren)

	if hasChildren {
		response.BadRequest(c, "Cannot delete location with child locations. Delete child locations first.")
		return
	}

	result, err := h.db.Exec(`
		UPDATE warehouse_locations SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND warehouse_id = $3 AND deleted_at IS NULL
	`, time.Now(), locationID, warehouseID)

	if err != nil {
		h.log.Error("Failed to delete warehouse location", "error", err)
		response.InternalError(c, "Failed to delete warehouse location")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Location")
		return
	}

	response.NoContent(c)
}
