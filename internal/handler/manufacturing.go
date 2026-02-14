package handler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// =====================================================
// WORK CENTER HANDLERS
// =====================================================

// ListWorkCenters godoc
// @Summary List work centers
// @Description Get a paginated list of work centers with filtering options
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param status query string false "Filter by status"
// @Param is_available query boolean false "Filter by availability"
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Param search query string false "Search by name or code"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param sort_by query string false "Sort by field" default(name)
// @Param sort_order query string false "Sort order (asc/desc)" default(asc)
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers [get]
func (h *Handler) ListWorkCenters(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var filter entity.WorkCenterFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid query parameters")
		return
	}

	// Set defaults
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.SortBy == "" {
		filter.SortBy = "name"
	}
	if filter.SortOrder == "" {
		filter.SortOrder = "asc"
	}

	// Build query
	baseQuery := `
		SELECT wc.id, wc.code, wc.name, wc.description, wc.warehouse_id, w.name as warehouse_name,
			   wc.department, wc.capacity_per_hour, wc.efficiency_factor, wc.oee_target,
			   wc.working_hours_per_day, wc.hourly_cost, wc.setup_cost, wc.overhead_cost,
			   wc.currency, wc.status, wc.is_available, wc.next_maintenance_date,
			   wc.last_maintenance_date, wc.total_jobs_completed, wc.total_hours_worked,
			   wc.current_utilization, wc.notes, wc.created_at, wc.updated_at
		FROM work_centers wc
		LEFT JOIN warehouses w ON wc.warehouse_id = w.id
		WHERE wc.tenant_id = $1 AND wc.deleted_at IS NULL
	`

	countQuery := `SELECT COUNT(*) FROM work_centers wc WHERE wc.tenant_id = $1 AND wc.deleted_at IS NULL`
	args := []interface{}{tenantID}
	countArgs := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND wc.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND wc.organization_id = $%d", argCount)
		args = append(args, orgID)
		countArgs = append(countArgs, orgID)
	}

	// Apply filters
	if filter.Status != nil && *filter.Status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND wc.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND wc.status = $%d", argCount)
		args = append(args, *filter.Status)
		countArgs = append(countArgs, *filter.Status)
	}

	if filter.IsAvailable != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND wc.is_available = $%d", argCount)
		countQuery += fmt.Sprintf(" AND wc.is_available = $%d", argCount)
		args = append(args, *filter.IsAvailable)
		countArgs = append(countArgs, *filter.IsAvailable)
	}

	if filter.WarehouseID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND wc.warehouse_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND wc.warehouse_id = $%d", argCount)
		args = append(args, *filter.WarehouseID)
		countArgs = append(countArgs, *filter.WarehouseID)
	}

	if filter.Search != nil && *filter.Search != "" {
		argCount++
		searchPattern := "%" + *filter.Search + "%"
		baseQuery += fmt.Sprintf(" AND (wc.name ILIKE $%d OR wc.code ILIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (wc.name ILIKE $%d OR wc.code ILIKE $%d)", argCount, argCount)
		args = append(args, searchPattern)
		countArgs = append(countArgs, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count work centers", "error", err)
		response.InternalError(c, "Failed to retrieve work centers")
		return
	}

	// Sorting
	validSortColumns := map[string]string{
		"name":         "wc.name",
		"code":         "wc.code",
		"status":       "wc.status",
		"utilization":  "wc.current_utilization",
		"created_at":   "wc.created_at",
	}
	sortColumn := validSortColumns[filter.SortBy]
	if sortColumn == "" {
		sortColumn = "wc.name"
	}
	sortOrder := "ASC"
	if strings.ToLower(filter.SortOrder) == "desc" {
		sortOrder = "DESC"
	}
	baseQuery += fmt.Sprintf(" ORDER BY %s %s", sortColumn, sortOrder)

	// Pagination
	offset := (filter.Page - 1) * filter.Limit
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.Limit, offset)

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list work centers", "error", err)
		response.InternalError(c, "Failed to retrieve work centers")
		return
	}
	defer rows.Close()

	workCenters := []entity.WorkCenterResponse{}
	for rows.Next() {
		var wc entity.WorkCenterResponse
		var warehouseName sql.NullString
		var nextMaint, lastMaint sql.NullTime

		err := rows.Scan(
			&wc.ID, &wc.Code, &wc.Name, &wc.Description, &wc.WarehouseID, &warehouseName,
			&wc.Department, &wc.CapacityPerHour, &wc.EfficiencyFactor, &wc.OEETarget,
			&wc.WorkingHoursPerDay, &wc.HourlyCost, &wc.SetupCost, &wc.OverheadCost,
			&wc.Currency, &wc.Status, &wc.IsAvailable, &nextMaint,
			&lastMaint, &wc.TotalJobsCompleted, &wc.TotalHoursWorked,
			&wc.CurrentUtilization, &wc.Notes, &wc.CreatedAt, &wc.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan work center", "error", err)
			continue
		}

		if warehouseName.Valid {
			wc.WarehouseName = &warehouseName.String
		}
		if nextMaint.Valid {
			s := nextMaint.Time.Format("2006-01-02")
			wc.NextMaintenanceDate = &s
		}
		if lastMaint.Valid {
			s := lastMaint.Time.Format("2006-01-02")
			wc.LastMaintenanceDate = &s
		}

		workCenters = append(workCenters, wc)
	}

	pagination := entity.NewPagination(filter.Page, filter.Limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, workCenters, pagination)
}

// GetWorkCenter godoc
// @Summary Get work center
// @Description Get a single work center by ID
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Work Center ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers/{id} [get]
func (h *Handler) GetWorkCenter(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	query := `
		SELECT wc.id, wc.code, wc.name, wc.description, wc.warehouse_id, w.name as warehouse_name,
			   wc.department, wc.capacity_per_hour, wc.efficiency_factor, wc.oee_target,
			   wc.working_hours_per_day, wc.hourly_cost, wc.setup_cost, wc.overhead_cost,
			   wc.currency, wc.status, wc.is_available, wc.next_maintenance_date,
			   wc.last_maintenance_date, wc.total_jobs_completed, wc.total_hours_worked,
			   wc.current_utilization, wc.notes, wc.created_at, wc.updated_at
		FROM work_centers wc
		LEFT JOIN warehouses w ON wc.warehouse_id = w.id
		WHERE wc.id = $1 AND wc.tenant_id = $2 AND wc.deleted_at IS NULL
	`

	var wc entity.WorkCenterResponse
	var warehouseName sql.NullString
	var nextMaint, lastMaint sql.NullTime

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&wc.ID, &wc.Code, &wc.Name, &wc.Description, &wc.WarehouseID, &warehouseName,
		&wc.Department, &wc.CapacityPerHour, &wc.EfficiencyFactor, &wc.OEETarget,
		&wc.WorkingHoursPerDay, &wc.HourlyCost, &wc.SetupCost, &wc.OverheadCost,
		&wc.Currency, &wc.Status, &wc.IsAvailable, &nextMaint,
		&lastMaint, &wc.TotalJobsCompleted, &wc.TotalHoursWorked,
		&wc.CurrentUtilization, &wc.Notes, &wc.CreatedAt, &wc.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Work center not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get work center", "error", err)
		response.InternalError(c, "Failed to retrieve work center")
		return
	}

	if warehouseName.Valid {
		wc.WarehouseName = &warehouseName.String
	}
	if nextMaint.Valid {
		s := nextMaint.Time.Format("2006-01-02")
		wc.NextMaintenanceDate = &s
	}
	if lastMaint.Valid {
		s := lastMaint.Time.Format("2006-01-02")
		wc.LastMaintenanceDate = &s
	}

	response.Success(c, wc)
}

// CreateWorkCenter godoc
// @Summary Create work center
// @Description Create a new work center
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param input body entity.WorkCenterInput true "Work center input"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers [post]
func (h *Handler) CreateWorkCenter(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.WorkCenterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Set defaults
	capacityPerHour := 1.0
	if input.CapacityPerHour != nil {
		capacityPerHour = *input.CapacityPerHour
	}
	efficiencyFactor := 100.0
	if input.EfficiencyFactor != nil {
		efficiencyFactor = *input.EfficiencyFactor
	}
	oeeTarget := 85.0
	if input.OEETarget != nil {
		oeeTarget = *input.OEETarget
	}
	workingHours := 8.0
	if input.WorkingHoursPerDay != nil {
		workingHours = *input.WorkingHoursPerDay
	}
	hourlyCost := 0.0
	if input.HourlyCost != nil {
		hourlyCost = *input.HourlyCost
	}
	setupCost := 0.0
	if input.SetupCost != nil {
		setupCost = *input.SetupCost
	}
	overheadCost := 0.0
	if input.OverheadCost != nil {
		overheadCost = *input.OverheadCost
	}
	currency := "USD"
	if input.Currency != nil {
		currency = *input.Currency
	}
	status := "active"
	if input.Status != nil {
		status = *input.Status
	}
	isAvailable := true
	if input.IsAvailable != nil {
		isAvailable = *input.IsAvailable
	}

	var nextMaintDate *time.Time
	if input.NextMaintenanceDate != nil {
		t, err := time.Parse("2006-01-02", *input.NextMaintenanceDate)
		if err == nil {
			nextMaintDate = &t
		}
	}

	now := time.Now()
	id := uuid.New()

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO work_centers (
			id, tenant_id, organization_id, code, name, description, warehouse_id, department,
			capacity_per_hour, efficiency_factor, oee_target, working_hours_per_day,
			hourly_cost, setup_cost, overhead_cost, currency, status, is_available,
			next_maintenance_date, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, input.Code, input.Name, input.Description, input.WarehouseID,
		input.Department, capacityPerHour, efficiencyFactor, oeeTarget, workingHours,
		hourlyCost, setupCost, overheadCost, currency, status, isAvailable,
		nextMaintDate, input.Notes, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create work center", "error", err)
		if strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Work center code already exists")
			return
		}
		response.InternalError(c, "Failed to create work center")
		return
	}

	resp := &entity.WorkCenterResponse{
		ID:                 id,
		Code:               input.Code,
		Name:               input.Name,
		Description:        input.Description,
		WarehouseID:        input.WarehouseID,
		Department:         input.Department,
		CapacityPerHour:    capacityPerHour,
		EfficiencyFactor:   efficiencyFactor,
		OEETarget:          oeeTarget,
		WorkingHoursPerDay: workingHours,
		HourlyCost:         hourlyCost,
		SetupCost:          setupCost,
		OverheadCost:       overheadCost,
		Currency:           currency,
		Status:             status,
		IsAvailable:        isAvailable,
		Notes:              input.Notes,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if input.NextMaintenanceDate != nil {
		resp.NextMaintenanceDate = input.NextMaintenanceDate
	}

	response.Created(c, resp)
}

// UpdateWorkCenter godoc
// @Summary Update work center
// @Description Update an existing work center
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Work Center ID"
// @Param input body entity.WorkCenterInput true "Work center input"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers/{id} [put]
func (h *Handler) UpdateWorkCenter(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	var input entity.WorkCenterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Code != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("code = $%d", argCount))
		args = append(args, input.Code)
	}
	if input.Name != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, input.Name)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.WarehouseID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("warehouse_id = $%d", argCount))
		args = append(args, *input.WarehouseID)
	}
	if input.Department != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("department = $%d", argCount))
		args = append(args, *input.Department)
	}
	if input.CapacityPerHour != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("capacity_per_hour = $%d", argCount))
		args = append(args, *input.CapacityPerHour)
	}
	if input.EfficiencyFactor != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("efficiency_factor = $%d", argCount))
		args = append(args, *input.EfficiencyFactor)
	}
	if input.OEETarget != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("oee_target = $%d", argCount))
		args = append(args, *input.OEETarget)
	}
	if input.WorkingHoursPerDay != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("working_hours_per_day = $%d", argCount))
		args = append(args, *input.WorkingHoursPerDay)
	}
	if input.HourlyCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("hourly_cost = $%d", argCount))
		args = append(args, *input.HourlyCost)
	}
	if input.SetupCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("setup_cost = $%d", argCount))
		args = append(args, *input.SetupCost)
	}
	if input.OverheadCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("overhead_cost = $%d", argCount))
		args = append(args, *input.OverheadCost)
	}
	if input.Currency != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("currency = $%d", argCount))
		args = append(args, *input.Currency)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.IsAvailable != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_available = $%d", argCount))
		args = append(args, *input.IsAvailable)
	}
	if input.NextMaintenanceDate != nil {
		argCount++
		t, _ := time.Parse("2006-01-02", *input.NextMaintenanceDate)
		updates = append(updates, fmt.Sprintf("next_maintenance_date = $%d", argCount))
		args = append(args, t)
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

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE work_centers SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update work center", "error", err)
		response.InternalError(c, "Failed to update work center")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Work center not found")
		return
	}

	// Fetch and return updated work center
	h.GetWorkCenter(c)
}

// DeleteWorkCenter godoc
// @Summary Delete work center
// @Description Soft delete a work center
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Work Center ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers/{id} [delete]
func (h *Handler) DeleteWorkCenter(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	query := `UPDATE work_centers SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL`
	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete work center", "error", err)
		response.InternalError(c, "Failed to delete work center")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Work center not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Work center deleted successfully"})
}

// =====================================================
// PRODUCTION ORDER HANDLERS
// =====================================================

// ListProductionOrders returns all production orders with filtering
// ListProductionOrders godoc
// @Summary List production orders
// @Description Get a paginated list of production/manufacturing orders
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param status query string false "Filter by status"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders [get]
func (h *Handler) ListProductionOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var filter entity.ProductionOrderFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid query parameters")
		return
	}

	// Set defaults
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.SortBy == "" {
		filter.SortBy = "created_at"
	}
	if filter.SortOrder == "" {
		filter.SortOrder = "desc"
	}

	baseQuery := `
		SELECT po.id, po.code, po.name, po.product_id, p.name as product_name, p.code as product_code,
			   po.bom_id, b.name as bom_name, po.quantity_planned, po.quantity_produced, po.quantity_scrapped,
			   po.uom, po.mold_count, po.shift, po.current_stage, po.package_count, po.good_quantity, po.reject_quantity,
			   po.scheduled_start, po.scheduled_end, po.actual_start, po.actual_end,
			   po.priority, po.status, po.progress_percent, po.source_type, po.warehouse_id,
			   w.name as warehouse_name, po.planned_cost, po.actual_cost, po.material_cost,
			   po.labor_cost, po.overhead_cost, po.currency, po.assigned_to, u.first_name || ' ' || u.last_name as assigned_to_name,
			   po.work_center_id, wc.name as work_center_name, po.requires_quality_check, po.quality_status,
			   po.notes, po.tags, po.created_by, po.confirmed_at, po.completed_at, po.created_at, po.updated_at
		FROM production_orders po
		LEFT JOIN products p ON po.product_id = p.id
		LEFT JOIN product_boms b ON po.bom_id = b.id
		LEFT JOIN warehouses w ON po.warehouse_id = w.id
		LEFT JOIN users u ON po.assigned_to = u.id
		LEFT JOIN work_centers wc ON po.work_center_id = wc.id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
	`

	countQuery := `SELECT COUNT(*) FROM production_orders po WHERE po.tenant_id = $1 AND po.deleted_at IS NULL`
	args := []interface{}{tenantID}
	countArgs := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.organization_id = $%d", argCount)
		args = append(args, orgID)
		countArgs = append(countArgs, orgID)
	}

	// Apply filters
	if filter.Status != nil && *filter.Status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.status = $%d", argCount)
		args = append(args, *filter.Status)
		countArgs = append(countArgs, *filter.Status)
	}

	if filter.ProductID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.product_id = $%d", argCount)
		args = append(args, *filter.ProductID)
		countArgs = append(countArgs, *filter.ProductID)
	}

	if filter.WorkCenterID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.work_center_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.work_center_id = $%d", argCount)
		args = append(args, *filter.WorkCenterID)
		countArgs = append(countArgs, *filter.WorkCenterID)
	}

	if filter.AssignedTo != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.assigned_to = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.assigned_to = $%d", argCount)
		args = append(args, *filter.AssignedTo)
		countArgs = append(countArgs, *filter.AssignedTo)
	}

	if filter.Priority != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.priority = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.priority = $%d", argCount)
		args = append(args, *filter.Priority)
		countArgs = append(countArgs, *filter.Priority)
	}

	if filter.DateFrom != nil && *filter.DateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.scheduled_start >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.scheduled_start >= $%d", argCount)
		args = append(args, *filter.DateFrom)
		countArgs = append(countArgs, *filter.DateFrom)
	}

	if filter.DateTo != nil && *filter.DateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.scheduled_end <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.scheduled_end <= $%d", argCount)
		args = append(args, *filter.DateTo)
		countArgs = append(countArgs, *filter.DateTo)
	}

	if filter.Search != nil && *filter.Search != "" {
		argCount++
		searchPattern := "%" + *filter.Search + "%"
		baseQuery += fmt.Sprintf(" AND (po.code ILIKE $%d OR po.name ILIKE $%d OR p.name ILIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (po.code ILIKE $%d OR po.name ILIKE $%d)", argCount, argCount)
		args = append(args, searchPattern)
		countArgs = append(countArgs, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count production orders", "error", err)
		response.InternalError(c, "Failed to retrieve production orders")
		return
	}

	// Sorting
	validSortColumns := map[string]string{
		"code":           "po.code",
		"status":         "po.status",
		"priority":       "po.priority",
		"scheduled_start": "po.scheduled_start",
		"created_at":     "po.created_at",
	}
	sortColumn := validSortColumns[filter.SortBy]
	if sortColumn == "" {
		sortColumn = "po.created_at"
	}
	sortOrder := "ASC"
	if strings.ToLower(filter.SortOrder) == "desc" {
		sortOrder = "DESC"
	}
	baseQuery += fmt.Sprintf(" ORDER BY %s %s", sortColumn, sortOrder)

	// Pagination
	offset := (filter.Page - 1) * filter.Limit
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.Limit, offset)

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list production orders", "error", err)
		response.InternalError(c, "Failed to retrieve production orders")
		return
	}
	defer rows.Close()

	orders := []entity.ProductionOrderResponse{}
	for rows.Next() {
		var po entity.ProductionOrderResponse
		var bomName, warehouseName, assignedToName, workCenterName, shift sql.NullString
		var scheduledStart, scheduledEnd, actualStart, actualEnd, confirmedAt, completedAt sql.NullTime
		var tags []byte

		err := rows.Scan(
			&po.ID, &po.Code, &po.Name, &po.ProductID, &po.ProductName, &po.ProductCode,
			&po.BOMID, &bomName, &po.QuantityPlanned, &po.QuantityProduced, &po.QuantityScrapped,
			&po.UOM, &po.MoldCount, &shift, &po.CurrentStage, &po.PackageCount, &po.GoodQuantity, &po.RejectQuantity,
			&scheduledStart, &scheduledEnd, &actualStart, &actualEnd,
			&po.Priority, &po.Status, &po.ProgressPercent, &po.SourceType, &po.WarehouseID,
			&warehouseName, &po.PlannedCost, &po.ActualCost, &po.MaterialCost,
			&po.LaborCost, &po.OverheadCost, &po.Currency, &po.AssignedTo, &assignedToName,
			&po.WorkCenterID, &workCenterName, &po.RequiresQualityCheck, &po.QualityStatus,
			&po.Notes, &tags, &po.CreatedBy, &confirmedAt, &completedAt, &po.CreatedAt, &po.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan production order", "error", err)
			continue
		}

		if bomName.Valid {
			po.BOMName = &bomName.String
		}
		if warehouseName.Valid {
			po.WarehouseName = &warehouseName.String
		}
		if assignedToName.Valid {
			po.AssignedToName = &assignedToName.String
		}
		if workCenterName.Valid {
			po.WorkCenterName = &workCenterName.String
		}
		if shift.Valid {
			po.Shift = &shift.String
		}
		if scheduledStart.Valid {
			s := scheduledStart.Time.Format("2006-01-02")
			po.ScheduledStart = &s
		}
		if scheduledEnd.Valid {
			s := scheduledEnd.Time.Format("2006-01-02")
			po.ScheduledEnd = &s
		}
		if actualStart.Valid {
			s := actualStart.Time.Format(time.RFC3339)
			po.ActualStart = &s
		}
		if actualEnd.Valid {
			s := actualEnd.Time.Format(time.RFC3339)
			po.ActualEnd = &s
		}
		if confirmedAt.Valid {
			s := confirmedAt.Time.Format(time.RFC3339)
			po.ConfirmedAt = &s
		}
		if completedAt.Valid {
			s := completedAt.Time.Format(time.RFC3339)
			po.CompletedAt = &s
		}

		po.QuantityRemaining = po.QuantityPlanned - po.QuantityProduced - po.QuantityScrapped
		po.Tags = []string{}

		orders = append(orders, po)
	}

	pagination := entity.NewPagination(filter.Page, filter.Limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, orders, pagination)
}

// GetProductionOrder godoc
// @Summary Get production order
// @Description Get a single production order by ID
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id} [get]
func (h *Handler) GetProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	query := `
		SELECT po.id, po.code, po.name, po.product_id, p.name as product_name, p.code as product_code,
			   po.bom_id, b.name as bom_name, po.quantity_planned, po.quantity_produced, po.quantity_scrapped,
			   po.uom, po.mold_count, po.shift, po.current_stage, po.package_count, po.good_quantity, po.reject_quantity,
			   po.scheduled_start, po.scheduled_end, po.actual_start, po.actual_end,
			   po.priority, po.status, po.progress_percent, po.source_type, po.warehouse_id,
			   w.name as warehouse_name, po.planned_cost, po.actual_cost, po.material_cost,
			   po.labor_cost, po.overhead_cost, po.currency, po.assigned_to, u.first_name || ' ' || u.last_name as assigned_to_name,
			   po.work_center_id, wc.name as work_center_name, po.requires_quality_check, po.quality_status,
			   po.notes, po.tags, po.created_by, cu.first_name || ' ' || cu.last_name as created_by_name,
			   po.confirmed_at, po.completed_at, po.created_at, po.updated_at
		FROM production_orders po
		LEFT JOIN products p ON po.product_id = p.id
		LEFT JOIN product_boms b ON po.bom_id = b.id
		LEFT JOIN warehouses w ON po.warehouse_id = w.id
		LEFT JOIN users u ON po.assigned_to = u.id
		LEFT JOIN work_centers wc ON po.work_center_id = wc.id
		LEFT JOIN users cu ON po.created_by = cu.id
		WHERE po.id = $1 AND po.tenant_id = $2 AND po.deleted_at IS NULL
	`

	var po entity.ProductionOrderResponse
	var bomName, warehouseName, assignedToName, workCenterName, createdByName, shift sql.NullString
	var scheduledStart, scheduledEnd, actualStart, actualEnd, confirmedAt, completedAt sql.NullTime
	var tags []byte

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&po.ID, &po.Code, &po.Name, &po.ProductID, &po.ProductName, &po.ProductCode,
		&po.BOMID, &bomName, &po.QuantityPlanned, &po.QuantityProduced, &po.QuantityScrapped,
		&po.UOM, &po.MoldCount, &shift, &po.CurrentStage, &po.PackageCount, &po.GoodQuantity, &po.RejectQuantity,
		&scheduledStart, &scheduledEnd, &actualStart, &actualEnd,
		&po.Priority, &po.Status, &po.ProgressPercent, &po.SourceType, &po.WarehouseID,
		&warehouseName, &po.PlannedCost, &po.ActualCost, &po.MaterialCost,
		&po.LaborCost, &po.OverheadCost, &po.Currency, &po.AssignedTo, &assignedToName,
		&po.WorkCenterID, &workCenterName, &po.RequiresQualityCheck, &po.QualityStatus,
		&po.Notes, &tags, &po.CreatedBy, &createdByName,
		&confirmedAt, &completedAt, &po.CreatedAt, &po.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get production order", "error", err)
		response.InternalError(c, "Failed to retrieve production order")
		return
	}

	if bomName.Valid {
		po.BOMName = &bomName.String
	}
	if warehouseName.Valid {
		po.WarehouseName = &warehouseName.String
	}
	if assignedToName.Valid {
		po.AssignedToName = &assignedToName.String
	}
	if workCenterName.Valid {
		po.WorkCenterName = &workCenterName.String
	}
	if createdByName.Valid {
		po.CreatedByName = &createdByName.String
	}
	if shift.Valid {
		po.Shift = &shift.String
	}
	if scheduledStart.Valid {
		s := scheduledStart.Time.Format("2006-01-02")
		po.ScheduledStart = &s
	}
	if scheduledEnd.Valid {
		s := scheduledEnd.Time.Format("2006-01-02")
		po.ScheduledEnd = &s
	}
	if actualStart.Valid {
		s := actualStart.Time.Format(time.RFC3339)
		po.ActualStart = &s
	}
	if actualEnd.Valid {
		s := actualEnd.Time.Format(time.RFC3339)
		po.ActualEnd = &s
	}
	if confirmedAt.Valid {
		s := confirmedAt.Time.Format(time.RFC3339)
		po.ConfirmedAt = &s
	}
	if completedAt.Valid {
		s := completedAt.Time.Format(time.RFC3339)
		po.CompletedAt = &s
	}

	po.QuantityRemaining = po.QuantityPlanned - po.QuantityProduced - po.QuantityScrapped
	po.Tags = []string{}

	// If order is confirmed or beyond, fetch associated work orders
	if po.Status != "draft" {
		woQuery := `
			SELECT wo.id, wo.code, wo.name, wo.sequence, wo.work_center_id, wc.name as work_center_name,
				   wo.quantity_to_produce, wo.quantity_produced, wo.quantity_scrapped, wo.uom,
				   wo.planned_duration_hours, wo.actual_duration_hours, wo.setup_time_hours,
				   wo.planned_cost, wo.actual_cost, wo.labor_cost, wo.machine_cost,
				   wo.status, wo.progress_percent, wo.instructions, wo.notes, wo.created_at
			FROM work_orders wo
			LEFT JOIN work_centers wc ON wo.work_center_id = wc.id
			WHERE wo.production_order_id = $1 AND wo.tenant_id = $2 AND wo.deleted_at IS NULL
			ORDER BY wo.sequence ASC
		`
		woRows, err := h.db.Query(woQuery, id, tenantID)
		if err == nil {
			defer woRows.Close()
			for woRows.Next() {
				var wo entity.WorkOrder
				var wcID *uuid.UUID
				var wcName sql.NullString
				var instructions, notes sql.NullString

				err := woRows.Scan(
					&wo.ID, &wo.Code, &wo.Name, &wo.Sequence, &wcID, &wcName,
					&wo.QuantityToProduce, &wo.QuantityProduced, &wo.QuantityScrapped, &wo.UOM,
					&wo.PlannedDurationHrs, &wo.ActualDurationHrs, &wo.SetupTimeHrs,
					&wo.PlannedCost, &wo.ActualCost, &wo.LaborCost, &wo.MachineCost,
					&wo.Status, &wo.ProgressPercent, &instructions, &notes, &wo.CreatedAt,
				)
				if err != nil {
					h.log.Error("Failed to scan work order", "error", err)
					continue
				}
				wo.ProductionOrderID = id
				wo.TenantID = tenantID
				if wcID != nil {
					wo.WorkCenterID = wcID
				}
				if wcName.Valid {
					wo.WorkCenterName = &wcName.String
				}
				if instructions.Valid {
					wo.Instructions = &instructions.String
				}
				if notes.Valid {
					wo.Notes = &notes.String
				}
				po.WorkOrders = append(po.WorkOrders, wo)
			}
		}
	}

	response.Success(c, po)
}

// CreateProductionOrder godoc
// @Summary Create production order
// @Description Create a new production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param input body entity.ProductionOrderInput true "Production order input"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders [post]
func (h *Handler) CreateProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.ProductionOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Generate code
	now := time.Now()
	id := uuid.New()
	code := fmt.Sprintf("MO-%s", id.String()[:8])

	// Set defaults
	priority := 5
	if input.Priority != nil {
		priority = *input.Priority
	}
	requiresQC := false
	if input.RequiresQualityCheck != nil {
		requiresQC = *input.RequiresQualityCheck
	}

	var scheduledStart, scheduledEnd *time.Time
	if input.ScheduledStart != nil {
		t, err := time.Parse("2006-01-02", *input.ScheduledStart)
		if err == nil {
			scheduledStart = &t
		}
	}
	if input.ScheduledEnd != nil {
		t, err := time.Parse("2006-01-02", *input.ScheduledEnd)
		if err == nil {
			scheduledEnd = &t
		}
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Manufacturing-specific fields
	moldCount := 0
	if input.MoldCount != nil {
		moldCount = *input.MoldCount
	}

	query := `
		INSERT INTO production_orders (
			id, tenant_id, organization_id, code, name, product_id, bom_id, quantity_planned, uom,
			mold_count, shift, current_stage,
			scheduled_start, scheduled_end, priority, status, source_type, source_id,
			sales_order_id, customer_id, warehouse_id, location_id, assigned_to,
			work_center_id, requires_quality_check, notes, tags, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'draft', $12, $13, $14, 'draft', $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28)
		RETURNING id
	`

	tags := []byte("[]")
	if len(input.Tags) > 0 {
		tags = []byte(fmt.Sprintf(`["%s"]`, strings.Join(input.Tags, `","`)))
	}

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, code, input.Name, input.ProductID, input.BOMID, input.QuantityPlanned, input.UOM,
		moldCount, input.Shift,
		scheduledStart, scheduledEnd, priority, input.SourceType, input.SourceID,
		input.SalesOrderID, input.CustomerID, input.WarehouseID, input.LocationID, input.AssignedTo,
		input.WorkCenterID, requiresQC, input.Notes, tags, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create production order", "error", err)
		response.InternalError(c, "Failed to create production order")
		return
	}

	// Return created order
	c.Params = append(c.Params, gin.Param{Key: "id", Value: id.String()})
	h.GetProductionOrder(c)
}

// UpdateProductionOrder godoc
// @Summary Update production order
// @Description Update an existing production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Param input body entity.ProductionOrderInput true "Production order input"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id} [put]
func (h *Handler) UpdateProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	var input entity.ProductionOrderUpdateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.BOMID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("bom_id = $%d", argCount))
		args = append(args, *input.BOMID)
	}
	if input.QuantityPlanned != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("quantity_planned = $%d", argCount))
		args = append(args, *input.QuantityPlanned)
	}
	if input.ScheduledStart != nil {
		argCount++
		t, _ := time.Parse("2006-01-02", *input.ScheduledStart)
		updates = append(updates, fmt.Sprintf("scheduled_start = $%d", argCount))
		args = append(args, t)
	}
	if input.ScheduledEnd != nil {
		argCount++
		t, _ := time.Parse("2006-01-02", *input.ScheduledEnd)
		updates = append(updates, fmt.Sprintf("scheduled_end = $%d", argCount))
		args = append(args, t)
	}
	if input.Priority != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("priority = $%d", argCount))
		args = append(args, *input.Priority)
	}
	if input.AssignedTo != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("assigned_to = $%d", argCount))
		args = append(args, *input.AssignedTo)
	}
	if input.WorkCenterID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("work_center_id = $%d", argCount))
		args = append(args, *input.WorkCenterID)
	}
	if input.RequiresQualityCheck != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("requires_quality_check = $%d", argCount))
		args = append(args, *input.RequiresQualityCheck)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if len(input.Tags) > 0 {
		argCount++
		tags := []byte(fmt.Sprintf(`["%s"]`, strings.Join(input.Tags, `","`)))
		updates = append(updates, fmt.Sprintf("tags = $%d", argCount))
		args = append(args, tags)
	}

	// Manufacturing-specific fields
	if input.MoldCount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("mold_count = $%d", argCount))
		args = append(args, *input.MoldCount)
	}
	if input.Shift != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("shift = $%d", argCount))
		args = append(args, *input.Shift)
	}
	if input.CurrentStage != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("current_stage = $%d", argCount))
		args = append(args, *input.CurrentStage)
	}
	if input.PackageCount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("package_count = $%d", argCount))
		args = append(args, *input.PackageCount)
	}
	if input.GoodQuantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("good_quantity = $%d", argCount))
		args = append(args, *input.GoodQuantity)
	}
	if input.RejectQuantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("reject_quantity = $%d", argCount))
		args = append(args, *input.RejectQuantity)
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
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	// Allow updates for draft, confirmed, and in_progress status (for manufacturing stage tracking)
	query := fmt.Sprintf(
		"UPDATE production_orders SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL AND status IN ('draft', 'confirmed', 'in_progress')",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update production order", "error", err)
		response.InternalError(c, "Failed to update production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or cannot be updated")
		return
	}

	h.GetProductionOrder(c)
}

// DeleteProductionOrder godoc
// @Summary Delete production order
// @Description Soft delete a production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id} [delete]
func (h *Handler) DeleteProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	query := `UPDATE production_orders SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status = 'draft'`
	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete production order", "error", err)
		response.InternalError(c, "Failed to delete production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or cannot be deleted")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Production order deleted successfully"})
}

// ConfirmProductionOrder godoc
// @Summary Confirm production order
// @Description Confirm a draft production order, calculate costs and generate work orders
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/confirm [post]
func (h *Handler) ConfirmProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to confirm production order")
		return
	}
	defer tx.Rollback()

	// Get the production order details (including BOM ID and quantity)
	var bomID *uuid.UUID
	var quantityPlanned float64
	var workCenterID *uuid.UUID
	var uom string
	var productName string
	var orgID *uuid.UUID

	poQuery := `
		SELECT po.bom_id, po.quantity_planned, po.work_center_id, po.uom, po.organization_id, p.name as product_name
		FROM production_orders po
		LEFT JOIN products p ON p.id = po.product_id
		WHERE po.id = $1 AND po.tenant_id = $2 AND po.deleted_at IS NULL AND po.status = 'draft'
	`
	err = tx.QueryRow(poQuery, id, tenantID).Scan(&bomID, &quantityPlanned, &workCenterID, &uom, &orgID, &productName)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Production order not found or not in draft status")
			return
		}
		h.log.Error("Failed to get production order", "error", err)
		response.InternalError(c, "Failed to confirm production order")
		return
	}

	var totalPlannedCost float64 = 0
	var totalLaborCost float64 = 0
	var totalOverheadCost float64 = 0

	// If BOM exists, calculate costs and generate work orders from BOM operations
	if bomID != nil {
		// Get BOM operations with work center costs
		opsQuery := `
			SELECT
				bo.id, bo.sequence, bo.operation_name, bo.work_center_id,
				bo.setup_time_minutes, bo.run_time_minutes,
				bo.labor_cost, bo.overhead_cost, bo.notes,
				COALESCE(wc.hourly_cost, 0) as wc_hourly_cost,
				COALESCE(wc.setup_cost, 0) as wc_setup_cost,
				COALESCE(wc.overhead_cost, 0) as wc_overhead_cost
			FROM bom_operations bo
			LEFT JOIN work_centers wc ON wc.id = bo.work_center_id AND wc.deleted_at IS NULL
			WHERE bo.bom_id = $1
			ORDER BY bo.sequence ASC
		`
		rows, err := tx.Query(opsQuery, bomID)
		if err != nil {
			h.log.Error("Failed to get BOM operations", "error", err)
			response.InternalError(c, "Failed to confirm production order")
			return
		}
		defer rows.Close()

		now := time.Now()

		for rows.Next() {
			var opID uuid.UUID
			var sequence int
			var operationName string
			var opWorkCenterID *uuid.UUID
			var setupTimeMinutes, runTimeMinutes float64
			var laborCost, overheadCost float64
			var notes *string
			var wcHourlyCost, wcSetupCost, wcOverheadCost float64

			err := rows.Scan(&opID, &sequence, &operationName, &opWorkCenterID,
				&setupTimeMinutes, &runTimeMinutes,
				&laborCost, &overheadCost, &notes,
				&wcHourlyCost, &wcSetupCost, &wcOverheadCost)
			if err != nil {
				h.log.Error("Failed to scan BOM operation", "error", err)
				continue
			}

			// Calculate time for this operation
			// Total time = setup time + (run time per unit * quantity)
			totalTimeMinutes := setupTimeMinutes + (runTimeMinutes * quantityPlanned)
			totalTimeHours := totalTimeMinutes / 60.0

			// Calculate costs for this operation
			// Option 1: Use labor_cost and overhead_cost from bom_operations if set
			// Option 2: Use work center hourly_cost if bom_operations costs are 0
			var opLaborCost float64
			var opOverheadCost float64

			if laborCost > 0 {
				opLaborCost = laborCost * quantityPlanned
			} else if wcHourlyCost > 0 {
				opLaborCost = totalTimeHours * wcHourlyCost
			}

			if overheadCost > 0 {
				opOverheadCost = overheadCost * quantityPlanned
			} else if wcOverheadCost > 0 {
				opOverheadCost = totalTimeHours * wcOverheadCost
			}

			// Add setup cost from work center
			machineCost := wcSetupCost + (totalTimeHours * wcHourlyCost)

			totalLaborCost += opLaborCost
			totalOverheadCost += opOverheadCost
			totalPlannedCost += opLaborCost + opOverheadCost + machineCost

			// Use operation's work center if available, otherwise fall back to PO's work center
			effectiveWorkCenterID := opWorkCenterID
			if effectiveWorkCenterID == nil {
				effectiveWorkCenterID = workCenterID
			}

			// Generate work order for this operation
			woID := uuid.New()
			woCode := fmt.Sprintf("WO-%s-%d", id.String()[:8], sequence)
			woName := fmt.Sprintf("%s - %s", productName, operationName)

			woQuery := `
				INSERT INTO work_orders (
					id, tenant_id, production_order_id, code, name,
					sequence, operation_id, work_center_id,
					quantity_to_produce, uom,
					planned_duration_hours, setup_time_hours,
					planned_cost, labor_cost, machine_cost,
					status, instructions, notes,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'pending', $16, $17, $18, $19, $19)
			`

			var instructions *string
			if notes != nil && *notes != "" {
				instructions = notes
			}

			_, err = tx.Exec(woQuery,
				woID, tenantID, id, woCode, woName,
				sequence, opID, effectiveWorkCenterID,
				quantityPlanned, uom,
				totalTimeHours, setupTimeMinutes/60.0,
				opLaborCost+machineCost, opLaborCost, machineCost,
				instructions, notes,
				userID, now,
			)
			if err != nil {
				h.log.Error("Failed to create work order", "error", err, "operation", operationName)
				response.InternalError(c, "Failed to create work orders")
				return
			}
		}

		if err := rows.Err(); err != nil {
			h.log.Error("Error iterating BOM operations", "error", err)
			response.InternalError(c, "Failed to confirm production order")
			return
		}
	}

	// Update production order with calculated costs and confirm
	now := time.Now()
	updateQuery := `
		UPDATE production_orders
		SET status = 'confirmed',
			confirmed_by = $1,
			confirmed_at = $2,
			planned_cost = $3,
			labor_cost = $4,
			overhead_cost = $5,
			updated_at = $2
		WHERE id = $6 AND tenant_id = $7 AND deleted_at IS NULL AND status = 'draft'
	`

	result, err := tx.Exec(updateQuery, userID, now, totalPlannedCost, totalLaborCost, totalOverheadCost, id, tenantID)
	if err != nil {
		h.log.Error("Failed to confirm production order", "error", err)
		response.InternalError(c, "Failed to confirm production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or not in draft status")
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to confirm production order")
		return
	}

	h.GetProductionOrder(c)
}

// StartProductionOrder godoc
// @Summary Start production order
// @Description Start a confirmed production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/start [post]
func (h *Handler) StartProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	now := time.Now()
	query := `
		UPDATE production_orders
		SET status = 'in_progress', actual_start = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status IN ('confirmed', 'ready', 'paused')
	`

	result, err := h.db.Exec(query, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to start production order", "error", err)
		response.InternalError(c, "Failed to start production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or not in valid status")
		return
	}

	h.GetProductionOrder(c)
}

// PauseProductionOrder godoc
// @Summary Pause production order
// @Description Pause an in-progress production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/pause [post]
func (h *Handler) PauseProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	now := time.Now()
	query := `
		UPDATE production_orders
		SET status = 'paused', updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status = 'in_progress'
	`

	result, err := h.db.Exec(query, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to pause production order", "error", err)
		response.InternalError(c, "Failed to pause production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or not in progress")
		return
	}

	h.GetProductionOrder(c)
}

// CompleteProductionOrder godoc
// @Summary Complete production order
// @Description Complete a production order and update inventory
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/complete [post]
func (h *Handler) CompleteProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	// Optional: accept quantity produced
	var input struct {
		QuantityProduced float64 `json:"quantity_produced"`
		QuantityScrapped float64 `json:"quantity_scrapped"`
	}
	c.ShouldBindJSON(&input)

	now := time.Now()

	// Update production order
	query := `
		UPDATE production_orders
		SET status = 'completed', actual_end = $1, completed_by = $2, completed_at = $1, updated_at = $1,
			progress_percent = 100
	`
	args := []interface{}{now, userID}
	argCount := 2

	if input.QuantityProduced > 0 {
		argCount++
		query += fmt.Sprintf(", quantity_produced = $%d", argCount)
		args = append(args, input.QuantityProduced)
	}
	if input.QuantityScrapped > 0 {
		argCount++
		query += fmt.Sprintf(", quantity_scrapped = $%d", argCount)
		args = append(args, input.QuantityScrapped)
	}

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query += fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL AND status = 'in_progress'", argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to complete production order", "error", err)
		response.InternalError(c, "Failed to complete production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or not in progress")
		return
	}

	// --- Inventory integration: add produced goods & consume materials ---
	var productID uuid.UUID
	var bomID *uuid.UUID
	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	var qtyProduced, qtyPlanned float64

	err = h.db.QueryRow(`
		SELECT product_id, bom_id, warehouse_id, organization_id,
		       COALESCE(quantity_produced, quantity_planned), quantity_planned
		FROM production_orders WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(&productID, &bomID, &warehouseID, &organizationID, &qtyProduced, &qtyPlanned)
	if err != nil {
		h.log.Error("Failed to fetch production order for inventory", "error", err)
		h.GetProductionOrder(c)
		return
	}

	if warehouseID == nil {
		h.log.Warn("Production order has no warehouse, skipping inventory update", "order_id", id)
		h.GetProductionOrder(c)
		return
	}

	producedQty := qtyProduced
	if input.QuantityProduced > 0 {
		producedQty = input.QuantityProduced
	}

	var unitCost float64
	h.db.QueryRow("SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1 AND tenant_id = $2", productID, tenantID).Scan(&unitCost)

	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("Failed to start inventory transaction", "error", txErr)
		h.GetProductionOrder(c)
		return
	}
	defer tx.Rollback()

	// Add finished product to inventory
	var invID uuid.UUID
	err = tx.QueryRow(`
		SELECT id FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND lot_number IS NULL AND serial_number IS NULL AND variant_id IS NULL
	`, tenantID, productID, warehouseID).Scan(&invID)

	if err == sql.ErrNoRows {
		invID = uuid.New()
		_, err = tx.Exec(`
			INSERT INTO inventory (
				id, tenant_id, organization_id, product_id, warehouse_id,
				quantity_on_hand, quantity_reserved, unit_cost, last_movement_date, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, 0, $7, $8, $8, $8)
		`, invID, tenantID, organizationID, productID, warehouseID, producedQty, unitCost, now)
	} else if err == nil {
		_, err = tx.Exec(`
			UPDATE inventory SET quantity_on_hand = quantity_on_hand + $1, last_movement_date = $2, updated_at = $2
			WHERE id = $3
		`, producedQty, now, invID)
	}
	if err != nil {
		h.log.Error("Failed to update finished product inventory", "error", err)
		h.GetProductionOrder(c)
		return
	}

	// Create receipt transaction for finished product
	_, err = tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, organization_id, inventory_id, transaction_type,
			reference_type, reference_id, quantity, unit_cost, total_cost,
			reason, notes, transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $13)
	`, uuid.New(), tenantID, organizationID, invID, entity.TransactionTypeReceipt,
		"production_order", id, producedQty, unitCost, producedQty*unitCost,
		"production_complete", "Auto-generated from production order completion", now, userID)
	if err != nil {
		h.log.Error("Failed to create receipt transaction", "error", err)
		h.GetProductionOrder(c)
		return
	}

	// Consume BOM components
	if bomID != nil {
		type bomComponent struct {
			ComponentID  uuid.UUID
			Quantity     float64
			ScrapPercent float64
			BOMOutputQty float64
		}

		compRows, compErr := tx.Query(`
			SELECT bl.component_id, bl.quantity, COALESCE(bl.scrap_percent, 0), pb.quantity
			FROM bom_lines bl
			JOIN product_boms pb ON pb.id = bl.bom_id
			WHERE bl.bom_id = $1
		`, bomID)
		if compErr == nil {
			var components []bomComponent
			for compRows.Next() {
				var comp bomComponent
				if scanErr := compRows.Scan(&comp.ComponentID, &comp.Quantity, &comp.ScrapPercent, &comp.BOMOutputQty); scanErr == nil {
					components = append(components, comp)
				}
			}
			compRows.Close()

			for _, comp := range components {
				consumption := comp.Quantity * (producedQty / comp.BOMOutputQty) * (1 + comp.ScrapPercent/100)

				var compInvID uuid.UUID
				compErr := tx.QueryRow(`
					SELECT id FROM inventory
					WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
					AND lot_number IS NULL AND serial_number IS NULL AND variant_id IS NULL
				`, tenantID, comp.ComponentID, warehouseID).Scan(&compInvID)

				if compErr != nil {
					h.log.Warn("Component not found in inventory, skipping consumption", "component_id", comp.ComponentID)
					continue
				}

				_, _ = tx.Exec(`
					UPDATE inventory SET quantity_on_hand = quantity_on_hand - $1, last_movement_date = $2, updated_at = $2
					WHERE id = $3
				`, consumption, now, compInvID)

				var compCost float64
				h.db.QueryRow("SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1", comp.ComponentID).Scan(&compCost)

				_, _ = tx.Exec(`
					INSERT INTO inventory_transactions (
						id, tenant_id, organization_id, inventory_id, transaction_type,
						reference_type, reference_id, quantity, unit_cost, total_cost,
						reason, notes, transaction_date, created_by, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $13)
				`, uuid.New(), tenantID, organizationID, compInvID, entity.TransactionTypeIssue,
					"production_order", id, -consumption, compCost, consumption*compCost,
					"material_consumption", "Auto-consumed for production order", now, userID)
			}
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("Failed to commit inventory transaction", "error", commitErr)
	}

	// ============================================
	// CREATE JOURNAL ENTRY: Debit Stock Valuation (per category), Credit Manufacturing Expense
	// ============================================
	totalCost := producedQty * unitCost
	if totalCost > 0 {
		// Use category accounts for the finished product
		ca := getCategoryAccounts(h.db, tenantID, organizationID, productID)
		inventoryAccountID := ca.StockValuationAccountID

		// Credit side: manufacturing expense / COGS
		cogsAccountID := ca.ExpenseAccountID
		if cogsAccountID == uuid.Nil {
			cogsAccountID = findAccount(h.db, tenantID, organizationID, "manufacturing", "5100")
		}
		if cogsAccountID == uuid.Nil {
			cogsAccountID = findAccount(h.db, tenantID, organizationID, "cost of production", "5000")
		}

		if inventoryAccountID != uuid.Nil && cogsAccountID != uuid.Nil {
			// Find manufacturing or general journal
			var journalID uuid.UUID
			var nextNumber int
			err := h.db.QueryRow(`
				SELECT id, next_number FROM journals
				WHERE tenant_id = $1 AND type = 'general' AND is_active = true
				ORDER BY created_at ASC LIMIT 1
			`, tenantID).Scan(&journalID, &nextNumber)

			if err == nil && journalID != uuid.Nil {
				// Get production order number
				var poNumber string
				h.db.QueryRow(`SELECT order_number FROM production_orders WHERE id = $1`, id).Scan(&poNumber)

				// Get product name
				var productName string
				h.db.QueryRow(`SELECT name FROM products WHERE id = $1`, productID).Scan(&productName)

				entryID := uuid.New()
				entryNumber := fmt.Sprintf("MFG%06d", nextNumber)
				description := fmt.Sprintf("Production Order %s completed - %s (qty: %.2f)", poNumber, productName, producedQty)

				// Create journal entry
				h.db.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number,
						entry_date, description, status, total_debit, total_credit,
						created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, 'posted', $8, $8, $9, $9)
				`, entryID, tenantID, organizationID, journalID, entryNumber,
					now, description, totalCost, now)

				// Debit: Inventory (finished goods added)
				debitLineID := uuid.New()
				h.db.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, account_id, description,
						debit_amount, credit_amount, line_number, created_at
					) VALUES ($1, $2, $3, $4, $5, 0, 1, $6)
				`, debitLineID, entryID, inventoryAccountID, description, totalCost, now)

				// Credit: COGS / Manufacturing Expense (cost of production)
				creditLineID := uuid.New()
				h.db.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, account_id, description,
						debit_amount, credit_amount, line_number, created_at
					) VALUES ($1, $2, $3, $4, 0, $5, 2, $6)
				`, creditLineID, entryID, cogsAccountID, description, totalCost, now)

				// Update account balances
				h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalCost, now, inventoryAccountID)
				h.db.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalCost, now, cogsAccountID)

				// Update journal next_number
				h.db.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID)

				h.log.Info("Journal entry created for production order completion", "entry_id", entryID, "amount", totalCost)
			}
		}
	}

	h.GetProductionOrder(c)
}

// CancelProductionOrder godoc
// @Summary Cancel production order
// @Description Cancel a production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/cancel [post]
func (h *Handler) CancelProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	now := time.Now()
	query := `
		UPDATE production_orders
		SET status = 'cancelled', updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status NOT IN ('completed', 'closed', 'cancelled')
	`

	result, err := h.db.Exec(query, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to cancel production order", "error", err)
		response.InternalError(c, "Failed to cancel production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or cannot be cancelled")
		return
	}

	h.GetProductionOrder(c)
}

// RecordProduction godoc
// @Summary Record production
// @Description Record production output for an order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Param input body entity.ProductionOutputInput true "Production record input"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/record [post]
func (h *Handler) RecordProduction(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	var input struct {
		QuantityProduced float64 `json:"quantity_produced" binding:"required,gt=0"`
		QuantityScrapped float64 `json:"quantity_scrapped"`
		Notes            string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	now := time.Now()

	// Get current quantities and planned
	var quantityPlanned, currentProduced, currentScrapped float64
	checkQuery := `
		SELECT quantity_planned, quantity_produced, quantity_scrapped
		FROM production_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND status = 'in_progress'
	`
	err = h.db.QueryRow(checkQuery, id, tenantID).Scan(&quantityPlanned, &currentProduced, &currentScrapped)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found or not in progress")
		return
	}
	if err != nil {
		h.log.Error("Failed to get production order", "error", err)
		response.InternalError(c, "Failed to record production")
		return
	}

	newProduced := currentProduced + input.QuantityProduced
	newScrapped := currentScrapped + input.QuantityScrapped
	progressPercent := (newProduced / quantityPlanned) * 100
	if progressPercent > 100 {
		progressPercent = 100
	}

	updateQuery := `
		UPDATE production_orders
		SET quantity_produced = $1, quantity_scrapped = $2, progress_percent = $3, updated_at = $4
		WHERE id = $5 AND tenant_id = $6
	`

	_, err = h.db.Exec(updateQuery, newProduced, newScrapped, progressPercent, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to update production quantities", "error", err)
		response.InternalError(c, "Failed to record production")
		return
	}

	h.GetProductionOrder(c)
}

// GetProductionSchedule godoc
// @Summary Get production schedule
// @Description Get scheduled production orders for calendar view
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param date_from query string false "Start date (YYYY-MM-DD)"
// @Param date_to query string false "End date (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/schedule [get]
func (h *Handler) GetProductionSchedule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	if dateFrom == "" {
		dateFrom = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	}
	if dateTo == "" {
		dateTo = time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	}

	query := `
		SELECT po.id, po.code, p.name as product_name, wc.name as work_center_name,
			   po.quantity_planned, po.status, po.scheduled_start, po.scheduled_end,
			   po.priority, po.progress_percent
		FROM production_orders po
		LEFT JOIN products p ON po.product_id = p.id
		LEFT JOIN work_centers wc ON po.work_center_id = wc.id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		AND po.status NOT IN ('cancelled', 'closed')
		AND (
			(po.scheduled_start >= $2 AND po.scheduled_start <= $3)
			OR (po.scheduled_end >= $2 AND po.scheduled_end <= $3)
			OR (po.scheduled_start <= $2 AND po.scheduled_end >= $3)
		)
	`
	schedArgs := []interface{}{tenantID, dateFrom, dateTo}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND po.organization_id = $4"
		schedArgs = append(schedArgs, orgID)
	}
	query += " ORDER BY po.scheduled_start ASC, po.priority ASC"

	rows, err := h.db.Query(query, schedArgs...)
	if err != nil {
		h.log.Error("Failed to get production schedule", "error", err)
		response.InternalError(c, "Failed to retrieve production schedule")
		return
	}
	defer rows.Close()

	schedule := []entity.ProductionScheduleItem{}
	for rows.Next() {
		var item entity.ProductionScheduleItem
		var wcName sql.NullString
		var scheduledStart, scheduledEnd sql.NullTime

		err := rows.Scan(
			&item.ID, &item.Code, &item.ProductName, &wcName,
			&item.QuantityPlanned, &item.Status, &scheduledStart, &scheduledEnd,
			&item.Priority, &item.ProgressPercent,
		)
		if err != nil {
			continue
		}

		if wcName.Valid {
			item.WorkCenterName = &wcName.String
		}
		if scheduledStart.Valid {
			s := scheduledStart.Time.Format("2006-01-02")
			item.ScheduledStart = &s
		}
		if scheduledEnd.Valid {
			s := scheduledEnd.Time.Format("2006-01-02")
			item.ScheduledEnd = &s
		}

		schedule = append(schedule, item)
	}

	response.Success(c, schedule)
}

// GetManufacturingStats godoc
// @Summary Get manufacturing statistics
// @Description Get manufacturing dashboard statistics
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/stats [get]
func (h *Handler) GetManufacturingStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	stats := entity.ManufacturingStats{}

	// Build org filter
	statsArgs := []interface{}{tenantID}
	orgFilter := ""
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgFilter = " AND organization_id = $2"
		statsArgs = append(statsArgs, orgID)
	}

	// Production order stats
	orderStatsQuery := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'draft') as draft,
			COUNT(*) FILTER (WHERE status = 'confirmed') as confirmed,
			COUNT(*) FILTER (WHERE status = 'in_progress') as in_progress,
			COUNT(*) FILTER (WHERE status = 'completed') as completed,
			COUNT(*) FILTER (WHERE status NOT IN ('completed', 'cancelled', 'closed') AND scheduled_end < NOW()) as overdue,
			COALESCE(SUM(quantity_produced), 0) as total_produced,
			COALESCE(SUM(quantity_scrapped), 0) as total_scrapped
		FROM production_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL
	` + orgFilter

	var totalProduced, totalScrapped float64
	err := h.db.QueryRow(orderStatsQuery, statsArgs...).Scan(
		&stats.TotalProductionOrders, &stats.DraftOrders, &stats.ConfirmedOrders,
		&stats.InProgressOrders, &stats.CompletedOrders, &stats.OverdueOrders,
		&totalProduced, &totalScrapped,
	)
	if err != nil {
		h.log.Error("Failed to get production order stats", "error", err)
	}

	stats.TotalUnitsProduced = totalProduced
	stats.TotalUnitsScrapped = totalScrapped
	if totalProduced+totalScrapped > 0 {
		stats.ScrapRate = (totalScrapped / (totalProduced + totalScrapped)) * 100
	}
	if stats.TotalProductionOrders > 0 {
		stats.CompletionRate = float64(stats.CompletedOrders) / float64(stats.TotalProductionOrders) * 100
	}

	// Work center stats
	wcStatsQuery := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'active') as active,
			COALESCE(AVG(current_utilization), 0) as avg_utilization
		FROM work_centers
		WHERE tenant_id = $1 AND deleted_at IS NULL
	` + orgFilter
	err = h.db.QueryRow(wcStatsQuery, statsArgs...).Scan(
		&stats.TotalWorkCenters, &stats.ActiveWorkCenters, &stats.AverageUtilization,
	)
	if err != nil {
		h.log.Error("Failed to get work center stats", "error", err)
	}

	// Quality stats
	qcStatsQuery := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE result = 'passed') as passed,
			COUNT(*) FILTER (WHERE result = 'failed') as failed
		FROM quality_checks
		WHERE tenant_id = $1 AND deleted_at IS NULL
	` + orgFilter
	err = h.db.QueryRow(qcStatsQuery, statsArgs...).Scan(
		&stats.TotalQualityChecks, &stats.PassedChecks, &stats.FailedChecks,
	)
	if err != nil {
		h.log.Error("Failed to get quality check stats", "error", err)
	}

	if stats.TotalQualityChecks > 0 {
		stats.OverallPassRate = float64(stats.PassedChecks) / float64(stats.TotalQualityChecks) * 100
	}

	response.Success(c, stats)
}

// NOTE: Work Order handlers (ListWorkOrders, GetWorkOrder, CreateWorkOrder, StartWorkOrder,
// CompleteWorkOrder, RecordWorkOrderTime, PauseWorkOrder) are defined in work_orders.go

// =====================================================
// QUALITY CHECK HANDLERS
// =====================================================

// ListQualityChecks godoc
// @Summary List quality checks
// @Description Get a paginated list of quality checks with filtering options
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param result query string false "Filter by result"
// @Param production_order_id query string false "Filter by production order ID"
// @Param inspector_id query string false "Filter by inspector ID"
// @Param date_from query string false "Filter by date from"
// @Param date_to query string false "Filter by date to"
// @Param search query string false "Search by reference number"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param sort_by query string false "Sort by field" default(inspection_date)
// @Param sort_order query string false "Sort order (asc/desc)" default(desc)
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/quality-checks [get]
func (h *Handler) ListQualityChecks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var filter entity.QualityCheckFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid query parameters")
		return
	}

	// Set defaults
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.SortBy == "" {
		filter.SortBy = "inspection_date"
	}
	if filter.SortOrder == "" {
		filter.SortOrder = "desc"
	}

	baseQuery := `
		SELECT qc.id, qc.code, qc.quality_control_point_id, qc.production_order_id, po.code as po_code,
			   qc.work_order_id, wo.code as wo_code, qc.product_id, p.name as product_name, p.code as product_code,
			   qc.lot_number, qc.inspection_date, qc.inspector_id, qc.inspector_name,
			   qc.quantity_inspected, qc.quantity_passed, qc.quantity_failed, qc.result,
			   qc.measured_value, qc.measurement_unit, qc.pass_rate, qc.defect_type, qc.defect_category,
			   qc.action_taken, qc.notes, qc.failure_reason, qc.corrective_action, qc.attachments,
			   qc.created_at, qc.updated_at
		FROM quality_checks qc
		LEFT JOIN production_orders po ON qc.production_order_id = po.id
		LEFT JOIN work_orders wo ON qc.work_order_id = wo.id
		LEFT JOIN products p ON qc.product_id = p.id
		WHERE qc.tenant_id = $1 AND qc.deleted_at IS NULL
	`

	countQuery := `SELECT COUNT(*) FROM quality_checks qc WHERE qc.tenant_id = $1 AND qc.deleted_at IS NULL`
	args := []interface{}{tenantID}
	countArgs := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND qc.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND qc.organization_id = $%d", argCount)
		args = append(args, orgID)
		countArgs = append(countArgs, orgID)
	}

	// Apply filters
	if filter.ProductionOrderID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND qc.production_order_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND qc.production_order_id = $%d", argCount)
		args = append(args, *filter.ProductionOrderID)
		countArgs = append(countArgs, *filter.ProductionOrderID)
	}

	if filter.WorkOrderID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND qc.work_order_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND qc.work_order_id = $%d", argCount)
		args = append(args, *filter.WorkOrderID)
		countArgs = append(countArgs, *filter.WorkOrderID)
	}

	if filter.ProductID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND qc.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND qc.product_id = $%d", argCount)
		args = append(args, *filter.ProductID)
		countArgs = append(countArgs, *filter.ProductID)
	}

	if filter.Result != nil && *filter.Result != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND qc.result = $%d", argCount)
		countQuery += fmt.Sprintf(" AND qc.result = $%d", argCount)
		args = append(args, *filter.Result)
		countArgs = append(countArgs, *filter.Result)
	}

	if filter.DateFrom != nil && *filter.DateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND qc.inspection_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND qc.inspection_date >= $%d", argCount)
		args = append(args, *filter.DateFrom)
		countArgs = append(countArgs, *filter.DateFrom)
	}

	if filter.DateTo != nil && *filter.DateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND qc.inspection_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND qc.inspection_date <= $%d", argCount)
		args = append(args, *filter.DateTo)
		countArgs = append(countArgs, *filter.DateTo)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count quality checks", "error", err)
		response.InternalError(c, "Failed to retrieve quality checks")
		return
	}

	// Sorting and pagination
	validSortColumns := map[string]string{
		"inspection_date": "qc.inspection_date",
		"result":          "qc.result",
		"created_at":      "qc.created_at",
	}
	sortColumn := validSortColumns[filter.SortBy]
	if sortColumn == "" {
		sortColumn = "qc.inspection_date"
	}
	sortOrder := "ASC"
	if strings.ToLower(filter.SortOrder) == "desc" {
		sortOrder = "DESC"
	}
	baseQuery += fmt.Sprintf(" ORDER BY %s %s", sortColumn, sortOrder)

	offset := (filter.Page - 1) * filter.Limit
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.Limit, offset)

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list quality checks", "error", err)
		response.InternalError(c, "Failed to retrieve quality checks")
		return
	}
	defer rows.Close()

	// Initialize as empty array (never nil) to ensure JSON marshals to [] not null
	qualityChecks := make([]entity.QualityCheckResponse, 0)
	for rows.Next() {
		var qc entity.QualityCheckResponse
		var poCode, woCode, productName, productCode sql.NullString
		var passRate sql.NullFloat64
		var attachments []byte

		err := rows.Scan(
			&qc.ID, &qc.Code, &qc.QualityControlPointID, &qc.ProductionOrderID, &poCode,
			&qc.WorkOrderID, &woCode, &qc.ProductID, &productName, &productCode,
			&qc.LotNumber, &qc.InspectionDate, &qc.InspectorID, &qc.InspectorName,
			&qc.QuantityInspected, &qc.QuantityPassed, &qc.QuantityFailed, &qc.Result,
			&qc.MeasuredValue, &qc.MeasurementUnit, &passRate, &qc.DefectType, &qc.DefectCategory,
			&qc.ActionTaken, &qc.Notes, &qc.FailureReason, &qc.CorrectiveAction, &attachments,
			&qc.CreatedAt, &qc.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan quality check", "error", err)
			continue
		}

		if poCode.Valid {
			qc.ProductionOrderCode = &poCode.String
		}
		if woCode.Valid {
			qc.WorkOrderCode = &woCode.String
		}
		if productName.Valid {
			qc.ProductName = &productName.String
		}
		if productCode.Valid {
			qc.ProductCode = &productCode.String
		}
		if passRate.Valid {
			qc.PassRate = passRate.Float64
		} else if qc.QuantityInspected > 0 {
			qc.PassRate = (qc.QuantityPassed / qc.QuantityInspected) * 100
		}
		qc.Attachments = []string{}

		qualityChecks = append(qualityChecks, qc)
	}

	// Ensure we always return an array, never nil
	if qualityChecks == nil {
		qualityChecks = make([]entity.QualityCheckResponse, 0)
	}

	pagination := entity.NewPagination(filter.Page, filter.Limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, qualityChecks, pagination)
}

// GetQualityCheck godoc
// @Summary Get quality check
// @Description Get a single quality check by ID
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Quality Check ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/quality-checks/{id} [get]
func (h *Handler) GetQualityCheck(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid quality check ID")
		return
	}

	query := `
		SELECT qc.id, qc.code, qc.quality_control_point_id, qc.production_order_id, po.code as po_code,
			   qc.work_order_id, wo.code as wo_code, qc.product_id, p.name as product_name, p.code as product_code,
			   qc.lot_number, qc.inspection_date, qc.inspector_id, qc.inspector_name,
			   qc.quantity_inspected, qc.quantity_passed, qc.quantity_failed, qc.result,
			   qc.measured_value, qc.measurement_unit, qc.pass_rate, qc.defect_type, qc.defect_category,
			   qc.action_taken, qc.notes, qc.failure_reason, qc.corrective_action, qc.attachments,
			   qc.created_at, qc.updated_at
		FROM quality_checks qc
		LEFT JOIN production_orders po ON qc.production_order_id = po.id
		LEFT JOIN work_orders wo ON qc.work_order_id = wo.id
		LEFT JOIN products p ON qc.product_id = p.id
		WHERE qc.id = $1 AND qc.tenant_id = $2 AND qc.deleted_at IS NULL
	`

	var qc entity.QualityCheckResponse
	var poCode, woCode, productName, productCode sql.NullString
	var passRate sql.NullFloat64
	var attachments []byte

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&qc.ID, &qc.Code, &qc.QualityControlPointID, &qc.ProductionOrderID, &poCode,
		&qc.WorkOrderID, &woCode, &qc.ProductID, &productName, &productCode,
		&qc.LotNumber, &qc.InspectionDate, &qc.InspectorID, &qc.InspectorName,
		&qc.QuantityInspected, &qc.QuantityPassed, &qc.QuantityFailed, &qc.Result,
		&qc.MeasuredValue, &qc.MeasurementUnit, &passRate, &qc.DefectType, &qc.DefectCategory,
		&qc.ActionTaken, &qc.Notes, &qc.FailureReason, &qc.CorrectiveAction, &attachments,
		&qc.CreatedAt, &qc.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Quality check not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get quality check", "error", err)
		response.InternalError(c, "Failed to retrieve quality check")
		return
	}

	if poCode.Valid {
		qc.ProductionOrderCode = &poCode.String
	}
	if woCode.Valid {
		qc.WorkOrderCode = &woCode.String
	}
	if productName.Valid {
		qc.ProductName = &productName.String
	}
	if productCode.Valid {
		qc.ProductCode = &productCode.String
	}
	if passRate.Valid {
		qc.PassRate = passRate.Float64
	} else if qc.QuantityInspected > 0 {
		qc.PassRate = (qc.QuantityPassed / qc.QuantityInspected) * 100
	}
	qc.Attachments = []string{}

	response.Success(c, qc)
}

// CreateQualityCheck godoc
// @Summary Create quality check
// @Description Create a new quality check
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param input body entity.QualityCheckInput true "Quality check input"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/quality-checks [post]
func (h *Handler) CreateQualityCheck(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.QualityCheckInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Calculate result and pass rate
	result := "pending"
	if input.Result != nil {
		result = *input.Result
	} else if input.QuantityFailed > 0 {
		if input.QuantityPassed == 0 {
			result = "failed"
		} else {
			result = "partial"
		}
	} else if input.QuantityPassed > 0 {
		result = "passed"
	}

	passRate := 0.0
	if input.QuantityInspected > 0 {
		passRate = (input.QuantityPassed / input.QuantityInspected) * 100
	}

	now := time.Now()
	inspectionDate := now
	if input.InspectionDate != nil {
		t, err := time.Parse(time.RFC3339, *input.InspectionDate)
		if err == nil {
			inspectionDate = t
		}
	}

	id := uuid.New()
	code := fmt.Sprintf("QC-%s", id.String()[:8])

	// Get inspector name
	var inspectorName *string
	if userID != uuid.Nil {
		var name string
		h.db.QueryRow("SELECT first_name || ' ' || last_name FROM users WHERE id = $1", userID).Scan(&name)
		if name != "" {
			inspectorName = &name
		}
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO quality_checks (
			id, tenant_id, organization_id, code, quality_control_point_id, production_order_id, work_order_id,
			product_id, lot_number, inspection_date, inspector_id, inspector_name,
			quantity_inspected, quantity_passed, quantity_failed, result, measured_value,
			measurement_unit, pass_rate, defect_type, defect_category, action_taken,
			notes, failure_reason, corrective_action, attachments, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28)
		RETURNING id
	`

	attachments := []byte("[]")

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, code, input.QualityControlPointID, input.ProductionOrderID, input.WorkOrderID,
		input.ProductID, input.LotNumber, inspectionDate, userID, inspectorName,
		input.QuantityInspected, input.QuantityPassed, input.QuantityFailed, result, input.MeasuredValue,
		input.MeasurementUnit, passRate, input.DefectType, input.DefectCategory, input.ActionTaken,
		input.Notes, input.FailureReason, input.CorrectiveAction, attachments, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create quality check", "error", err)
		response.InternalError(c, "Failed to create quality check")
		return
	}

	// If linked to production order and failed, update quality status
	if input.ProductionOrderID != nil && result == "failed" {
		h.db.Exec(
			"UPDATE production_orders SET quality_status = 'failed', updated_at = NOW() WHERE id = $1 AND tenant_id = $2",
			*input.ProductionOrderID, tenantID,
		)
	} else if input.ProductionOrderID != nil && result == "passed" {
		h.db.Exec(
			"UPDATE production_orders SET quality_status = 'passed', updated_at = NOW() WHERE id = $1 AND tenant_id = $2 AND (quality_status IS NULL OR quality_status = 'pending')",
			*input.ProductionOrderID, tenantID,
		)
	}

	c.Params = append(c.Params, gin.Param{Key: "id", Value: id.String()})
	h.GetQualityCheck(c)
}

// GetQualityStats godoc
// @Summary Get quality statistics
// @Description Get quality statistics for a date range
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param date_from query string false "Start date (YYYY-MM-DD)"
// @Param date_to query string false "End date (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/quality-stats [get]
func (h *Handler) GetQualityStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	query := `
		SELECT
			COUNT(*) as total_checks,
			COUNT(*) FILTER (WHERE result = 'passed') as passed,
			COUNT(*) FILTER (WHERE result = 'failed') as failed,
			COUNT(*) FILTER (WHERE result = 'partial') as partial,
			COALESCE(SUM(quantity_inspected), 0) as total_inspected,
			COALESCE(SUM(quantity_passed), 0) as total_passed,
			COALESCE(SUM(quantity_failed), 0) as total_failed,
			COALESCE(AVG(pass_rate), 0) as avg_pass_rate
		FROM quality_checks
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	argCount := 1

	if dateFrom != "" {
		argCount++
		query += fmt.Sprintf(" AND inspection_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		argCount++
		query += fmt.Sprintf(" AND inspection_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	var stats struct {
		TotalChecks     int     `json:"total_checks"`
		Passed          int     `json:"passed"`
		Failed          int     `json:"failed"`
		Partial         int     `json:"partial"`
		TotalInspected  float64 `json:"total_inspected"`
		TotalPassed     float64 `json:"total_passed"`
		TotalFailed     float64 `json:"total_failed"`
		AveragePassRate float64 `json:"average_pass_rate"`
	}

	err := h.db.QueryRow(query, args...).Scan(
		&stats.TotalChecks, &stats.Passed, &stats.Failed, &stats.Partial,
		&stats.TotalInspected, &stats.TotalPassed, &stats.TotalFailed, &stats.AveragePassRate,
	)
	if err != nil {
		h.log.Error("Failed to get quality stats", "error", err)
		response.InternalError(c, "Failed to retrieve quality statistics")
		return
	}

	// Get top defects
	defectsQuery := `
		SELECT defect_type, COUNT(*) as count
		FROM quality_checks
		WHERE tenant_id = $1 AND deleted_at IS NULL AND defect_type IS NOT NULL
		GROUP BY defect_type
		ORDER BY count DESC
		LIMIT 5
	`
	rows, err := h.db.Query(defectsQuery, tenantID)
	if err != nil {
		h.log.Error("Failed to query top defects", "error", err)
		// Continue with empty defects rather than failing the whole request
		response.Success(c, map[string]interface{}{
			"summary":     stats,
			"top_defects": []map[string]interface{}{},
		})
		return
	}
	defer rows.Close()

	topDefects := make([]map[string]interface{}, 0)
	for rows.Next() {
		var defectType string
		var count int
		if err := rows.Scan(&defectType, &count); err != nil {
			h.log.Error("Failed to scan defect", "error", err)
			continue
		}
		topDefects = append(topDefects, map[string]interface{}{
			"defect_type": defectType,
			"count":       count,
		})
	}

	// Ensure we always return an array, even if empty
	if topDefects == nil {
		topDefects = make([]map[string]interface{}, 0)
	}

	response.Success(c, map[string]interface{}{
		"summary":     stats,
		"top_defects": topDefects,
	})
}

// ListQualityDefects godoc
// @Summary List quality defects
// @Description Get all quality defect types
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/quality-defects [get]
func (h *Handler) ListQualityDefects(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, code, name, description, category, severity, default_action, is_active, created_at, updated_at
		FROM quality_defects
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY name ASC
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to list quality defects", "error", err)
		response.InternalError(c, "Failed to retrieve quality defects")
		return
	}
	defer rows.Close()

	// Initialize as empty array to ensure JSON marshals to [] not null
	defects := make([]entity.QualityDefect, 0)
	for rows.Next() {
		var d entity.QualityDefect
		err := rows.Scan(&d.ID, &d.Code, &d.Name, &d.Description, &d.Category, &d.Severity, &d.DefaultAction, &d.IsActive, &d.CreatedAt, &d.UpdatedAt)
		if err != nil {
			h.log.Error("Failed to scan quality defect", "error", err)
			continue
		}
		d.TenantID = tenantID
		defects = append(defects, d)
	}

	// Ensure we always return an array, never nil
	if defects == nil {
		defects = make([]entity.QualityDefect, 0)
	}

	response.Success(c, defects)
}

// CreateQualityDefect godoc
// @Summary Create quality defect
// @Description Create a new quality defect type
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param input body entity.QualityDefectInput true "Quality defect input"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/quality-defects [post]
func (h *Handler) CreateQualityDefect(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.QualityDefectInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	severity := "minor"
	if input.Severity != nil {
		severity = *input.Severity
	}
	defaultAction := "rework"
	if input.DefaultAction != nil {
		defaultAction = *input.DefaultAction
	}
	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO quality_defects (id, tenant_id, code, name, description, category, severity, default_action, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := h.db.Exec(query, id, tenantID, input.Code, input.Name, input.Description, input.Category, severity, defaultAction, isActive, now, now)
	if err != nil {
		h.log.Error("Failed to create quality defect", "error", err)
		if strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Defect code already exists")
			return
		}
		response.InternalError(c, "Failed to create quality defect")
		return
	}

	response.Created(c, entity.QualityDefect{
		ID:            id,
		TenantID:      tenantID,
		Code:          input.Code,
		Name:          input.Name,
		Description:   input.Description,
		Category:      input.Category,
		Severity:      severity,
		DefaultAction: defaultAction,
		IsActive:      isActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	})
}
