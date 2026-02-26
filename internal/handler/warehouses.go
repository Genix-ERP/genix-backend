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
	"github.com/lib/pq"
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
			   w.manager_id, w.is_default, w.is_active, w.reception_steps, w.delivery_steps,
			   w.created_at, w.updated_at,
			   e.first_name as manager_first_name, e.last_name as manager_last_name
		FROM warehouses w
		LEFT JOIN employees e ON w.manager_id = e.id
		WHERE w.tenant_id = $1 AND w.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM warehouses w WHERE w.tenant_id = $1 AND w.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND w.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND w.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

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

	type LocationResponse struct {
		ID          uuid.UUID                    `json:"id"`
		WarehouseID uuid.UUID                    `json:"warehouse_id"`
		ParentID    *uuid.UUID                   `json:"parent_id,omitempty"`
		Code        string                       `json:"code"`
		Name        string                       `json:"name"`
		Type        entity.WarehouseLocationType `json:"type"`
		Aisle       *string                      `json:"aisle,omitempty"`
		Rack        *string                      `json:"rack,omitempty"`
		Shelf       *string                      `json:"shelf,omitempty"`
		Bin         *string                      `json:"bin,omitempty"`
		Capacity    *float64                     `json:"capacity,omitempty"`
		IsActive    bool                         `json:"is_active"`
		CreatedAt   time.Time                    `json:"created_at"`
		UpdatedAt   time.Time                    `json:"updated_at"`
	}

	type WarehouseResponse struct {
		ID             uuid.UUID           `json:"id"`
		TenantID       uuid.UUID           `json:"tenant_id"`
		OrganizationID *uuid.UUID          `json:"organization_id,omitempty"`
		Code           string              `json:"code"`
		Name           string              `json:"name"`
		Address        *entity.Address     `json:"address,omitempty"`
		ManagerID      *uuid.UUID          `json:"manager_id,omitempty"`
		ManagerName    string              `json:"manager_name,omitempty"`
		IsDefault      bool                `json:"is_default"`
		IsActive       bool                `json:"is_active"`
		ReceptionSteps int                 `json:"reception_steps"`
		DeliverySteps  int                 `json:"delivery_steps"`
		Locations      []*LocationResponse `json:"locations"`
		CreatedAt      time.Time           `json:"created_at"`
		UpdatedAt      time.Time           `json:"updated_at"`
	}

	warehouses := make([]*WarehouseResponse, 0)
	for rows.Next() {
		var w entity.Warehouse
		var orgID, managerID sql.NullString
		var addressJSON []byte
		var managerFirstName, managerLastName sql.NullString
		var receptionSteps, deliverySteps sql.NullInt64

		err := rows.Scan(
			&w.ID, &w.TenantID, &orgID, &w.Code, &w.Name, &addressJSON,
			&managerID, &w.IsDefault, &w.IsActive, &receptionSteps, &deliverySteps,
			&w.CreatedAt, &w.UpdatedAt,
			&managerFirstName, &managerLastName,
		)
		if err != nil {
			h.log.Error("Failed to scan warehouse", "error", err)
			continue
		}

		resp := &WarehouseResponse{
			ID:             w.ID,
			TenantID:       w.TenantID,
			Code:           w.Code,
			Name:           w.Name,
			IsDefault:      w.IsDefault,
			IsActive:       w.IsActive,
			ReceptionSteps: int(receptionSteps.Int64),
			DeliverySteps:  int(deliverySteps.Int64),
			Locations:      make([]*LocationResponse, 0),
			CreatedAt:      w.CreatedAt,
			UpdatedAt:      w.UpdatedAt,
		}
		// Default to 1-step if not set
		if resp.ReceptionSteps == 0 {
			resp.ReceptionSteps = 1
		}
		if resp.DeliverySteps == 0 {
			resp.DeliverySteps = 1
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

	// Fetch locations for all warehouses
	if len(warehouses) > 0 {
		warehouseIDs := make([]uuid.UUID, len(warehouses))
		warehouseMap := make(map[uuid.UUID]*WarehouseResponse)
		for i, w := range warehouses {
			warehouseIDs[i] = w.ID
			warehouseMap[w.ID] = w
		}

		locQuery := `
			SELECT id, warehouse_id, parent_id, code, name, type, aisle, rack, shelf, bin, capacity, is_active, created_at, updated_at
			FROM warehouse_locations
			WHERE warehouse_id = ANY($1) AND deleted_at IS NULL
			ORDER BY code ASC
		`
		locRows, err := h.db.Query(locQuery, pq.Array(warehouseIDs))
		if err != nil {
			h.log.Error("Failed to fetch warehouse locations", "error", err)
		} else {
			defer locRows.Close()
			for locRows.Next() {
				var loc LocationResponse
				var parentID sql.NullString
				var aisle, rack, shelf, bin sql.NullString
				var capacity sql.NullFloat64

				err := locRows.Scan(
					&loc.ID, &loc.WarehouseID, &parentID, &loc.Code, &loc.Name, &loc.Type,
					&aisle, &rack, &shelf, &bin, &capacity, &loc.IsActive,
					&loc.CreatedAt, &loc.UpdatedAt,
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

				if wh, ok := warehouseMap[loc.WarehouseID]; ok {
					wh.Locations = append(wh.Locations, &loc)
				}
			}
		}

		// Log total locations found
		totalLocs := 0
		for _, w := range warehouses {
			totalLocs += len(w.Locations)
		}
		h.log.Info("Warehouses with locations", "warehouse_count", len(warehouses), "total_locations", totalLocs)
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

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)

	// Check for duplicate code within same organization
	var codeExists bool
	var err error
	if orgID != uuid.Nil {
		err = h.db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM warehouses WHERE tenant_id = $1 AND organization_id = $2 AND code = $3 AND deleted_at IS NULL)",
			tenantID, orgID, input.Code,
		).Scan(&codeExists)
	} else {
		err = h.db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM warehouses WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)",
			tenantID, input.Code,
		).Scan(&codeExists)
	}
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

	// Default step values (1-step operations)
	receptionSteps := input.ReceptionSteps
	if receptionSteps < 1 || receptionSteps > 3 {
		receptionSteps = 1
	}
	deliverySteps := input.DeliverySteps
	if deliverySteps < 1 || deliverySteps > 3 {
		deliverySteps = 1
	}

	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO warehouses (id, tenant_id, organization_id, code, name, address, manager_id, is_default, is_active, reception_steps, delivery_steps, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id
	`

	err = h.db.QueryRow(query,
		id, tenantID, orgIDPtr, input.Code, input.Name, addressJSON, managerID, input.IsDefault, true, receptionSteps, deliverySteps, now, now,
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

	// Create default operation types for the warehouse
	h.createDefaultOperationTypes(tenantID, id, input.Code)

	resp := &entity.Warehouse{
		ID:             id,
		TenantID:       tenantID,
		Code:           input.Code,
		Name:           input.Name,
		Address:        input.Address,
		ManagerID:      managerID,
		IsDefault:      input.IsDefault,
		IsActive:       true,
		ReceptionSteps: receptionSteps,
		DeliverySteps:  deliverySteps,
		CreatedAt:      now,
		UpdatedAt:      now,
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
			   w.manager_id, w.is_default, w.is_active, w.reception_steps, w.delivery_steps,
			   w.created_at, w.updated_at,
			   e.first_name as manager_first_name, e.last_name as manager_last_name
		FROM warehouses w
		LEFT JOIN employees e ON w.manager_id = e.id
		WHERE w.id = $1 AND w.tenant_id = $2 AND w.deleted_at IS NULL
	`

	var w entity.Warehouse
	var orgID, managerID sql.NullString
	var addressJSON []byte
	var managerFirstName, managerLastName sql.NullString
	var receptionSteps, deliverySteps sql.NullInt64

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&w.ID, &w.TenantID, &orgID, &w.Code, &w.Name, &addressJSON,
		&managerID, &w.IsDefault, &w.IsActive, &receptionSteps, &deliverySteps,
		&w.CreatedAt, &w.UpdatedAt,
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
		ID             uuid.UUID                  `json:"id"`
		TenantID       uuid.UUID                  `json:"tenant_id"`
		OrganizationID *uuid.UUID                 `json:"organization_id,omitempty"`
		Code           string                     `json:"code"`
		Name           string                     `json:"name"`
		Address        *entity.Address            `json:"address,omitempty"`
		ManagerID      *uuid.UUID                 `json:"manager_id,omitempty"`
		ManagerName    string                     `json:"manager_name,omitempty"`
		IsDefault      bool                       `json:"is_default"`
		IsActive       bool                       `json:"is_active"`
		ReceptionSteps int                        `json:"reception_steps"`
		DeliverySteps  int                        `json:"delivery_steps"`
		CreatedAt      time.Time                  `json:"created_at"`
		UpdatedAt      time.Time                  `json:"updated_at"`
		Locations      []entity.WarehouseLocation `json:"locations,omitempty"`
	}

	// Default step values
	respReceptionSteps := int(receptionSteps.Int64)
	if respReceptionSteps == 0 {
		respReceptionSteps = 1
	}
	respDeliverySteps := int(deliverySteps.Int64)
	if respDeliverySteps == 0 {
		respDeliverySteps = 1
	}

	resp := &WarehouseDetailResponse{
		ID:             w.ID,
		TenantID:       w.TenantID,
		Code:           w.Code,
		Name:           w.Name,
		IsDefault:      w.IsDefault,
		IsActive:       w.IsActive,
		ReceptionSteps: respReceptionSteps,
		DeliverySteps:  respDeliverySteps,
		CreatedAt:      w.CreatedAt,
		UpdatedAt:      w.UpdatedAt,
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
	if input.ReceptionSteps != nil {
		steps := *input.ReceptionSteps
		if steps < 1 || steps > 3 {
			response.BadRequest(c, "Reception steps must be 1, 2, or 3")
			return
		}
		addUpdate("reception_steps", steps)
	}
	if input.DeliverySteps != nil {
		steps := *input.DeliverySteps
		if steps < 1 || steps > 3 {
			response.BadRequest(c, "Delivery steps must be 1, 2, or 3")
			return
		}
		addUpdate("delivery_steps", steps)
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

// ListAllLocations returns all locations across all warehouses (like Odoo's Locations menu)
func (h *Handler) ListAllLocations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse filters
	locationType := c.Query("type")
	warehouseID := c.Query("warehouse_id")
	search := c.Query("search")
	showInactive := c.Query("include_inactive") == "true"

	query := `
		SELECT wl.id, wl.warehouse_id, wl.parent_id, wl.code, wl.name, wl.type,
			   wl.aisle, wl.rack, wl.shelf, wl.bin, wl.capacity, wl.is_active,
			   wl.created_at, wl.updated_at,
			   w.code as warehouse_code, w.name as warehouse_name
		FROM warehouse_locations wl
		JOIN warehouses w ON wl.warehouse_id = w.id
		WHERE w.tenant_id = $1 AND wl.deleted_at IS NULL AND w.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization (via warehouse)
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND w.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if !showInactive {
		query += " AND wl.is_active = true"
	}

	if locationType != "" {
		argCount++
		query += fmt.Sprintf(" AND wl.type = $%d", argCount)
		args = append(args, locationType)
	}

	if warehouseID != "" {
		wID, err := uuid.Parse(warehouseID)
		if err == nil {
			argCount++
			query += fmt.Sprintf(" AND wl.warehouse_id = $%d", argCount)
			args = append(args, wID)
		}
	}

	if search != "" {
		argCount++
		query += fmt.Sprintf(" AND (wl.code ILIKE $%d OR wl.name ILIKE $%d)", argCount, argCount)
		args = append(args, "%"+search+"%")
	}

	query += " ORDER BY w.code ASC, wl.code ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list all locations", "error", err)
		response.InternalError(c, "Failed to list locations")
		return
	}
	defer rows.Close()

	type LocationWithWarehouse struct {
		entity.WarehouseLocation
		WarehouseCode string `json:"warehouse_code"`
		WarehouseName string `json:"warehouse_name"`
		IsEmpty       bool   `json:"is_empty"`
	}

	locations := make([]*LocationWithWarehouse, 0)
	locationIDs := make([]uuid.UUID, 0)

	for rows.Next() {
		var loc LocationWithWarehouse
		var parentID, aisle, rack, shelf, bin sql.NullString
		var capacity sql.NullFloat64

		err := rows.Scan(
			&loc.ID, &loc.WarehouseID, &parentID, &loc.Code, &loc.Name, &loc.Type,
			&aisle, &rack, &shelf, &bin, &capacity, &loc.IsActive,
			&loc.CreatedAt, &loc.UpdatedAt,
			&loc.WarehouseCode, &loc.WarehouseName,
		)
		if err != nil {
			h.log.Error("Failed to scan location", "error", err)
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
		locationIDs = append(locationIDs, loc.ID)
	}

	// Check which locations are empty (no inventory)
	if len(locationIDs) > 0 {
		emptyQuery := `
			SELECT location_id, COALESCE(SUM(quantity_on_hand), 0) as total_qty
			FROM inventory
			WHERE location_id = ANY($1)
			GROUP BY location_id
		`
		emptyRows, err := h.db.Query(emptyQuery, locationIDs)
		if err == nil {
			defer emptyRows.Close()
			inventoryMap := make(map[uuid.UUID]float64)
			for emptyRows.Next() {
				var locID uuid.UUID
				var qty float64
				emptyRows.Scan(&locID, &qty)
				inventoryMap[locID] = qty
			}
			for _, loc := range locations {
				qty, exists := inventoryMap[loc.ID]
				loc.IsEmpty = !exists || qty == 0
			}
		}
	}

	response.Success(c, locations)
}

// =====================================================
// WAREHOUSE OPERATION TYPE HANDLERS
// =====================================================

// ListOperationTypes returns operation types for a warehouse or all warehouses
func (h *Handler) ListOperationTypes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	warehouseIDStr := c.Query("warehouse_id")
	showInactive := c.Query("include_inactive") == "true"

	query := `
		SELECT ot.id, ot.warehouse_id, ot.code, ot.name, ot.type, ot.sequence,
			   ot.color, ot.show_operations,
			   ot.count_picking_ready, ot.count_picking_late,
			   ot.count_picking_waiting, ot.count_picking_backorders,
			   ot.is_active, ot.created_at,
			   w.name as warehouse_name
		FROM warehouse_operation_types ot
		JOIN warehouses w ON ot.warehouse_id = w.id
		WHERE ot.tenant_id = $1 AND ot.deleted_at IS NULL AND w.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND ot.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if !showInactive {
		query += " AND ot.is_active = true"
	}

	if warehouseIDStr != "" {
		warehouseID, err := uuid.Parse(warehouseIDStr)
		if err == nil {
			argCount++
			query += fmt.Sprintf(" AND ot.warehouse_id = $%d", argCount)
			args = append(args, warehouseID)
		}
	}

	query += " ORDER BY w.code ASC, ot.sequence ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list operation types", "error", err)
		response.InternalError(c, "Failed to list operation types")
		return
	}
	defer rows.Close()

	operationTypes := make([]*entity.OperationTypeResponse, 0)
	for rows.Next() {
		var ot entity.OperationTypeResponse
		var warehouseName sql.NullString

		err := rows.Scan(
			&ot.ID, &ot.WarehouseID, &ot.Code, &ot.Name, &ot.Type, &ot.Sequence,
			&ot.Color, &ot.ShowOperations,
			&ot.CountPickingReady, &ot.CountPickingLate,
			&ot.CountPickingWaiting, &ot.CountPickingBackorders,
			&ot.IsActive, &ot.CreatedAt,
			&warehouseName,
		)
		if err != nil {
			h.log.Error("Failed to scan operation type", "error", err)
			continue
		}

		if warehouseName.Valid {
			ot.WarehouseName = warehouseName.String
		}

		operationTypes = append(operationTypes, &ot)
	}

	response.Success(c, operationTypes)
}

// GetOperationType returns a single operation type by ID with its stock pickings
func (h *Handler) GetOperationType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid operation type ID")
		return
	}

	// Get the operation type
	var ot entity.OperationTypeResponse
	var warehouseName sql.NullString

	err = h.db.QueryRow(`
		SELECT ot.id, ot.warehouse_id, ot.code, ot.name, ot.type, ot.sequence,
			   ot.color, ot.show_operations,
			   ot.count_picking_ready, ot.count_picking_late,
			   ot.count_picking_waiting, ot.count_picking_backorders,
			   ot.is_active, ot.created_at,
			   w.name as warehouse_name
		FROM warehouse_operation_types ot
		JOIN warehouses w ON ot.warehouse_id = w.id
		WHERE ot.id = $1 AND ot.tenant_id = $2 AND ot.deleted_at IS NULL
	`, id, tenantID).Scan(
		&ot.ID, &ot.WarehouseID, &ot.Code, &ot.Name, &ot.Type, &ot.Sequence,
		&ot.Color, &ot.ShowOperations,
		&ot.CountPickingReady, &ot.CountPickingLate,
		&ot.CountPickingWaiting, &ot.CountPickingBackorders,
		&ot.IsActive, &ot.CreatedAt,
		&warehouseName,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Operation type")
			return
		}
		h.log.Error("Failed to get operation type", "error", err)
		response.InternalError(c, "Failed to get operation type")
		return
	}

	if warehouseName.Valid {
		ot.WarehouseName = warehouseName.String
	}

	// Get stock pickings for this operation type
	pickingRows, err := h.db.Query(`
		SELECT sp.id, sp.name, sp.origin, sp.state, sp.priority,
			   sp.scheduled_date, sp.date_done, sp.note,
			   sp.user_id, sp.created_at,
			   COALESCE(u.email, '') as user_email,
			   COALESCE(e.first_name || ' ' || e.last_name, u.email, '') as user_name
		FROM stock_pickings sp
		LEFT JOIN users u ON sp.user_id = u.id
		LEFT JOIN employees e ON e.user_id = sp.user_id AND e.tenant_id = sp.tenant_id
		WHERE sp.operation_type_id = $1 AND sp.tenant_id = $2
			AND (sp.deleted_at IS NULL)
		ORDER BY sp.scheduled_date DESC NULLS LAST, sp.created_at DESC
		LIMIT 100
	`, id, tenantID)

	type StockPickingResponse struct {
		ID            uuid.UUID  `json:"id"`
		Name          string     `json:"name"`
		Origin        *string    `json:"origin,omitempty"`
		State         string     `json:"state"`
		Priority      string     `json:"priority"`
		ScheduledDate *time.Time `json:"scheduled_date,omitempty"`
		DateDone      *time.Time `json:"date_done,omitempty"`
		Note          *string    `json:"note,omitempty"`
		UserID        *uuid.UUID `json:"user_id,omitempty"`
		UserEmail     string     `json:"user_email,omitempty"`
		UserName      string     `json:"user_name,omitempty"`
		CreatedAt     time.Time  `json:"created_at"`
	}

	pickings := make([]StockPickingResponse, 0)
	if err == nil {
		defer pickingRows.Close()
		for pickingRows.Next() {
			var p StockPickingResponse
			if scanErr := pickingRows.Scan(
				&p.ID, &p.Name, &p.Origin, &p.State, &p.Priority,
				&p.ScheduledDate, &p.DateDone, &p.Note,
				&p.UserID, &p.CreatedAt,
				&p.UserEmail, &p.UserName,
			); scanErr != nil {
				h.log.Error("Failed to scan stock picking", "error", scanErr)
				continue
			}
			pickings = append(pickings, p)
		}
	}

	result := map[string]interface{}{
		"operation_type": ot,
		"pickings":       pickings,
	}

	response.Success(c, result)
}

// GetWarehouseOperationTypes returns operation types for a specific warehouse
func (h *Handler) GetWarehouseOperationTypes(c *gin.Context) {
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

	query := `
		SELECT id, warehouse_id, code, name, type, sequence,
			   color, show_operations,
			   count_picking_ready, count_picking_late,
			   count_picking_waiting, count_picking_backorders,
			   is_active, created_at
		FROM warehouse_operation_types
		WHERE warehouse_id = $1 AND deleted_at IS NULL
		ORDER BY sequence ASC
	`

	rows, err := h.db.Query(query, warehouseID)
	if err != nil {
		h.log.Error("Failed to list warehouse operation types", "error", err)
		response.InternalError(c, "Failed to list operation types")
		return
	}
	defer rows.Close()

	operationTypes := make([]*entity.OperationTypeResponse, 0)
	for rows.Next() {
		var ot entity.OperationTypeResponse

		err := rows.Scan(
			&ot.ID, &ot.WarehouseID, &ot.Code, &ot.Name, &ot.Type, &ot.Sequence,
			&ot.Color, &ot.ShowOperations,
			&ot.CountPickingReady, &ot.CountPickingLate,
			&ot.CountPickingWaiting, &ot.CountPickingBackorders,
			&ot.IsActive, &ot.CreatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan operation type", "error", err)
			continue
		}

		operationTypes = append(operationTypes, &ot)
	}

	response.Success(c, operationTypes)
}

// CreateOperationType creates a new operation type for a warehouse
func (h *Handler) CreateOperationType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateOperationTypeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	warehouseID, err := uuid.Parse(input.WarehouseID)
	if err != nil {
		response.BadRequest(c, "Invalid warehouse ID")
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

	// Check for duplicate code
	var codeExists bool
	h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM warehouse_operation_types WHERE warehouse_id = $1 AND code = $2 AND deleted_at IS NULL)",
		warehouseID, input.Code,
	).Scan(&codeExists)
	if codeExists {
		response.Conflict(c, "Operation type with this code already exists in this warehouse")
		return
	}

	// Set defaults
	operationType := "internal" // operation_type column: incoming, outgoing, internal
	if input.OperationType != "" {
		operationType = input.OperationType
	}
	opType := "custom" // type column: custom type name
	if input.Type != "" {
		opType = input.Type
	}
	sequence := input.Sequence
	if sequence == 0 {
		sequence = 10
	}
	color := input.Color
	if color == "" {
		color = "#6366f1"
	}
	showOperations := true
	if input.ShowOperations != nil {
		showOperations = *input.ShowOperations
	}
	barcodeEnabled := true
	if input.BarcodeEnabled != nil {
		barcodeEnabled = *input.BarcodeEnabled
	}
	createBackorder := true
	if input.CreateBackorder != nil {
		createBackorder = *input.CreateBackorder
	}
	reservationMethod := "at_confirm"
	if input.ReservationMethod != "" {
		reservationMethod = input.ReservationMethod
	}

	// Parse optional location IDs
	var srcLocationID, destLocationID *uuid.UUID
	if input.DefaultLocationSrcID != "" {
		sid, err := uuid.Parse(input.DefaultLocationSrcID)
		if err == nil {
			srcLocationID = &sid
		}
	}
	if input.DefaultLocationDestID != "" {
		did, err := uuid.Parse(input.DefaultLocationDestID)
		if err == nil {
			destLocationID = &did
		}
	}

	id := uuid.New()
	now := time.Now()

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO warehouse_operation_types (
			id, tenant_id, organization_id, warehouse_id, code, name, operation_type, type, sequence,
			default_location_src_id, default_location_dest_id,
			show_operations, barcode_enabled, create_backorder,
			reservation_method, color, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING id
	`

	err = h.db.QueryRow(query,
		id, tenantID, orgIDPtr, warehouseID, input.Code, input.Name, operationType, opType, sequence,
		srcLocationID, destLocationID,
		showOperations, barcodeEnabled, createBackorder,
		reservationMethod, color, true, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create operation type", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Operation type with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create operation type")
		return
	}

	resp := &entity.OperationTypeResponse{
		ID:             id,
		WarehouseID:    warehouseID,
		Code:           input.Code,
		Name:           input.Name,
		Type:           opType,
		Sequence:       sequence,
		Color:          color,
		ShowOperations: showOperations,
		IsActive:       true,
		CreatedAt:      now,
	}

	response.Created(c, resp)
}

// UpdateOperationType updates an existing operation type
func (h *Handler) UpdateOperationType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid operation type ID")
		return
	}

	var input entity.UpdateOperationTypeInput
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
	if input.Sequence != nil {
		addUpdate("sequence", *input.Sequence)
	}
	if input.DefaultLocationSrcID != nil {
		if *input.DefaultLocationSrcID == "" {
			addUpdate("default_location_src_id", nil)
		} else {
			sid, _ := uuid.Parse(*input.DefaultLocationSrcID)
			addUpdate("default_location_src_id", sid)
		}
	}
	if input.DefaultLocationDestID != nil {
		if *input.DefaultLocationDestID == "" {
			addUpdate("default_location_dest_id", nil)
		} else {
			did, _ := uuid.Parse(*input.DefaultLocationDestID)
			addUpdate("default_location_dest_id", did)
		}
	}
	if input.ShowOperations != nil {
		addUpdate("show_operations", *input.ShowOperations)
	}
	if input.ShowReserved != nil {
		addUpdate("show_reserved", *input.ShowReserved)
	}
	if input.BarcodeEnabled != nil {
		addUpdate("barcode_enabled", *input.BarcodeEnabled)
	}
	if input.CreateBackorder != nil {
		addUpdate("create_backorder", *input.CreateBackorder)
	}
	if input.ReservationMethod != nil {
		addUpdate("reservation_method", *input.ReservationMethod)
	}
	if input.ReturnPickingTypeID != nil {
		if *input.ReturnPickingTypeID == "" {
			addUpdate("return_picking_type_id", nil)
		} else {
			rid, _ := uuid.Parse(*input.ReturnPickingTypeID)
			addUpdate("return_picking_type_id", rid)
		}
	}
	if input.Color != nil {
		addUpdate("color", *input.Color)
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
		UPDATE warehouse_operation_types SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id, warehouse_id, code, name, type, sequence, color, show_operations, is_active, created_at
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var ot entity.OperationTypeResponse
	err = h.db.QueryRow(query, args...).Scan(
		&ot.ID, &ot.WarehouseID, &ot.Code, &ot.Name, &ot.Type,
		&ot.Sequence, &ot.Color, &ot.ShowOperations, &ot.IsActive, &ot.CreatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Operation type")
		return
	}
	if err != nil {
		h.log.Error("Failed to update operation type", "error", err)
		response.InternalError(c, "Failed to update operation type")
		return
	}

	response.Success(c, ot)
}

// DeleteOperationType soft-deletes an operation type
func (h *Handler) DeleteOperationType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid operation type ID")
		return
	}

	// Check if it's a default type (receipt, internal, delivery, pos)
	var opType string
	err = h.db.QueryRow(
		"SELECT type FROM warehouse_operation_types WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		id, tenantID,
	).Scan(&opType)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Operation type")
		return
	}

	if opType != "custom" {
		response.BadRequest(c, "Cannot delete default operation types. You can deactivate them instead.")
		return
	}

	result, err := h.db.Exec(`
		UPDATE warehouse_operation_types SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, time.Now(), id, tenantID)

	if err != nil {
		h.log.Error("Failed to delete operation type", "error", err)
		response.InternalError(c, "Failed to delete operation type")
		return
	}

	rowsCount, _ := result.RowsAffected()
	if rowsCount == 0 {
		response.NotFound(c, "Operation type")
		return
	}

	response.NoContent(c)
}

// createDefaultOperationTypes creates default operation types for a warehouse
func (h *Handler) createDefaultOperationTypes(tenantID, warehouseID uuid.UUID, warehouseCode string) error {
	now := time.Now()

	// Default operation types like Odoo
	// operation_type must be one of: 'incoming', 'outgoing', 'internal'
	defaults := []struct {
		code          string
		name          string
		operationType string // incoming, outgoing, internal (for the operation_type column)
		opType        string // custom type name (for the type column)
		sequence      int
		color         string
	}{
		{warehouseCode + "/IN", "Receipts", "incoming", "receipt", 1, "#22c55e"},
		{warehouseCode + "/INT", "Internal Transfers", "internal", "internal", 2, "#3b82f6"},
		{warehouseCode + "/OUT", "Delivery Orders", "outgoing", "delivery", 3, "#f97316"},
		{warehouseCode + "/POS", "PoS Orders", "outgoing", "pos", 4, "#a855f7"},
	}

	for _, d := range defaults {
		id := uuid.New()
		_, err := h.db.Exec(`
			INSERT INTO warehouse_operation_types (
				id, tenant_id, warehouse_id, code, name, operation_type, type, sequence, color,
				show_operations, show_reserved, barcode_enabled, create_backorder,
				reservation_method, is_active, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
			ON CONFLICT (warehouse_id, code) DO NOTHING
		`,
			id, tenantID, warehouseID, d.code, d.name, d.operationType, d.opType, d.sequence, d.color,
			true, true, true, true,
			"at_confirm", true, now, now,
		)
		if err != nil {
			h.log.Error("Failed to create default operation type", "error", err, "code", d.code)
		}
	}

	return nil
}
