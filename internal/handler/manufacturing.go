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

	// Fetch BOM operations if BOM exists
	if po.BOMID != nil {
		bomOpsQuery := `
			SELECT id, sequence, operation_name, work_center_id, setup_time_minutes,
				   run_time_minutes, labor_cost, overhead_cost, notes
			FROM bom_operations
			WHERE bom_id = $1
			ORDER BY sequence ASC
		`
		bomOpsRows, err := h.db.Query(bomOpsQuery, *po.BOMID)
		if err == nil {
			defer bomOpsRows.Close()
			po.BOMOperations = []map[string]interface{}{}
			for bomOpsRows.Next() {
				var opID uuid.UUID
				var sequence int
				var operationName string
				var workCenterID *uuid.UUID
				var setupTime, runTime, laborCost, overheadCost float64
				var notes sql.NullString

				err := bomOpsRows.Scan(&opID, &sequence, &operationName, &workCenterID,
					&setupTime, &runTime, &laborCost, &overheadCost, &notes)
				if err == nil {
					op := map[string]interface{}{
						"id":                 opID,
						"sequence":           sequence,
						"name":               operationName,
						"operation_name":     operationName,
						"work_center_id":     workCenterID,
						"setup_time_minutes": setupTime,
						"run_time_minutes":   runTime,
						"labor_cost":         laborCost,
						"overhead_cost":      overheadCost,
					}
					if notes.Valid {
						op["notes"] = notes.String
					}
					po.BOMOperations = append(po.BOMOperations, op)
				}
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

	now := time.Now()
	query := `UPDATE production_orders SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status IN ('draft', 'cancelled', 'completed')`
	result, err := h.db.Exec(query, now, id, tenantID)
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

	// Cascade soft-delete all work orders so they disappear from Shop Floor Control
	h.db.Exec(`UPDATE work_orders SET deleted_at = $1 WHERE production_order_id = $2 AND tenant_id = $3 AND deleted_at IS NULL`, now, id, tenantID)

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

	userID, userIDExists := middleware.GetUserID(c)
	var createdByID interface{}
	if userIDExists && userID != uuid.Nil {
		createdByID = userID
	} else {
		createdByID = nil
	}

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
		// Step 1: Read all BOM operations into a slice first
		// (lib/pq doesn't support executing queries while iterating rows on the same transaction)
		type bomOp struct {
			ID              uuid.UUID
			Sequence        int
			OperationName   string
			WorkCenterID    *uuid.UUID
			SetupTime       float64
			RunTime         float64
			LaborCost       float64
			OverheadCost    float64
			Notes           *string
			WCHourlyCost    float64
			WCSetupCost     float64
			WCOverheadCost  float64
		}

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

		var operations []bomOp
		for rows.Next() {
			var op bomOp
			err := rows.Scan(&op.ID, &op.Sequence, &op.OperationName, &op.WorkCenterID,
				&op.SetupTime, &op.RunTime,
				&op.LaborCost, &op.OverheadCost, &op.Notes,
				&op.WCHourlyCost, &op.WCSetupCost, &op.WCOverheadCost)
			if err != nil {
				h.log.Error("Failed to scan BOM operation", "error", err)
				continue
			}
			operations = append(operations, op)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			h.log.Error("Error iterating BOM operations", "error", err)
			response.InternalError(c, "Failed to confirm production order")
			return
		}

		// Step 2: Now create work orders from the collected operations
		now := time.Now()
		for _, op := range operations {
			totalTimeMinutes := op.SetupTime + (op.RunTime * quantityPlanned)
			totalTimeHours := totalTimeMinutes / 60.0

			var opLaborCost float64
			var opOverheadCost float64

			if op.LaborCost > 0 {
				opLaborCost = op.LaborCost * quantityPlanned
			} else if op.WCHourlyCost > 0 {
				opLaborCost = totalTimeHours * op.WCHourlyCost
			}

			if op.OverheadCost > 0 {
				opOverheadCost = op.OverheadCost * quantityPlanned
			} else if op.WCOverheadCost > 0 {
				opOverheadCost = totalTimeHours * op.WCOverheadCost
			}

			machineCost := op.WCSetupCost + (totalTimeHours * op.WCHourlyCost)

			totalLaborCost += opLaborCost
			totalOverheadCost += opOverheadCost
			totalPlannedCost += opLaborCost + opOverheadCost + machineCost

			effectiveWorkCenterID := op.WorkCenterID
			if effectiveWorkCenterID == nil {
				effectiveWorkCenterID = workCenterID
			}

			woID := uuid.New()
			woCode := fmt.Sprintf("WO-%s-%d", id.String()[:8], op.Sequence)
			woName := fmt.Sprintf("%s - %s", productName, op.OperationName)

			woQuery := `
				INSERT INTO work_orders (
					id, tenant_id, production_order_id, code, name,
					sequence, operation_id, work_center_id,
					quantity_to_produce, uom,
					planned_duration_hours, setup_time_hours,
					planned_cost, labor_cost, machine_cost,
					status, instructions, notes,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'pending', $16, $17, $18, $19, $20)
			`

			var instructions *string
			if op.Notes != nil && *op.Notes != "" {
				instructions = op.Notes
			}

			_, err = tx.Exec(woQuery,
				woID, tenantID, id, woCode, woName,
				op.Sequence, op.ID, effectiveWorkCenterID,
				quantityPlanned, uom,
				totalTimeHours, op.SetupTime/60.0,
				opLaborCost+machineCost, opLaborCost, machineCost,
				instructions, op.Notes,
				createdByID, now, now,
			)
			if err != nil {
				h.log.Error("Failed to create work order", "error", err, "operation", op.OperationName)
				response.InternalError(c, fmt.Sprintf("Failed to create work orders: %v", err))
				return
			}
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
			current_stage = 'draft',
			updated_at = $2
		WHERE id = $6 AND tenant_id = $7 AND deleted_at IS NULL AND status = 'draft'
	`

	result, err := tx.Exec(updateQuery, createdByID, now, totalPlannedCost, totalLaborCost, totalOverheadCost, id, tenantID)
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

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	now := time.Now()

	// Get the first BOM operation stage name for current_stage
	firstStage := "in_progress"
	var stageBomID *uuid.UUID
	h.db.QueryRow(`SELECT bom_id FROM production_orders WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&stageBomID)
	if stageBomID != nil {
		var firstSeq int
		seqErr := h.db.QueryRow(`
			SELECT sequence FROM bom_operations WHERE bom_id = $1 ORDER BY sequence ASC LIMIT 1
		`, stageBomID).Scan(&firstSeq)
		if seqErr == nil {
			firstStage = fmt.Sprintf("op_%d", firstSeq)
		}
	}

	query := `
		UPDATE production_orders
		SET status = 'in_progress', actual_start = $1, current_stage = $2, updated_at = $1
		WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL AND status IN ('confirmed', 'ready', 'paused')
	`

	result, err := h.db.Exec(query, now, firstStage, id, tenantID)
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

	// --- Create work orders from BOM if none exist (for orders confirmed before the fix) ---
	var woCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM work_orders WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&woCount)
	if woCount == 0 {
		// Fetch BOM info and create work orders
		var poBomID *uuid.UUID
		var poOrgID *uuid.UUID
		var poQty float64
		fetchErr := h.db.QueryRow(`SELECT bom_id, organization_id, quantity_planned FROM production_orders WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&poBomID, &poOrgID, &poQty)
		if fetchErr == nil && poBomID != nil {
			orgVal := uuid.Nil
			if poOrgID != nil {
				orgVal = *poOrgID
			}
			if woErr := h.CreateWorkOrdersFromBOM(id, *poBomID, tenantID, orgVal, poQty, userID); woErr != nil {
				h.log.Error("Failed to create work orders on start", "error", woErr)
			} else {
				h.log.Info("Created work orders for production order on start", "order_id", id)
			}
		}
	}

	// --- Auto-start the first pending work order ---
	_, _ = h.db.Exec(`
		UPDATE work_orders
		SET status = 'in_progress', actual_start = $1, started_by = $2
		WHERE id = (
			SELECT id FROM work_orders
			WHERE production_order_id = $3 AND tenant_id = $4 AND deleted_at IS NULL
				AND status IN ('pending', 'ready')
			ORDER BY sequence ASC LIMIT 1
		)
	`, now, userID, id, tenantID)

	// --- Consume BOM components from inventory when production starts ---
	var productID uuid.UUID
	var bomID *uuid.UUID
	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	var qtyPlanned float64

	err = h.db.QueryRow(`
		SELECT product_id, bom_id, warehouse_id, organization_id, quantity_planned
		FROM production_orders WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(&productID, &bomID, &warehouseID, &organizationID, &qtyPlanned)
	if err != nil {
		h.log.Error("Failed to fetch production order for material consumption", "error", err)
		h.GetProductionOrder(c)
		return
	}

	// Auto-assign first warehouse if none set on the production order
	if warehouseID == nil {
		var firstWH uuid.UUID
		if h.db.QueryRow(`SELECT id FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&firstWH) == nil {
			warehouseID = &firstWH
			h.db.Exec(`UPDATE production_orders SET warehouse_id = $1 WHERE id = $2 AND tenant_id = $3`, firstWH, id, tenantID)
		}
	}

	// Only consume if we have a BOM and warehouse
	if bomID != nil && warehouseID != nil {
		// Check if materials were already consumed (prevent double-deduction on pause/resume)
		var existingConsumption int
		h.db.QueryRow(`
			SELECT COUNT(*) FROM inventory_transactions
			WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2
			AND transaction_type = $3
		`, tenantID, id, entity.TransactionTypeIssue).Scan(&existingConsumption)

		if existingConsumption == 0 {
			tx, txErr := h.db.Begin()
			if txErr != nil {
				h.log.Error("Failed to start material consumption transaction", "error", txErr)
				h.GetProductionOrder(c)
				return
			}
			defer tx.Rollback()

			// Read all BOM components first
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
			if compErr != nil {
				h.log.Error("Failed to fetch BOM components", "error", compErr)
				h.GetProductionOrder(c)
				return
			}

			var components []bomComponent
			for compRows.Next() {
				var comp bomComponent
				if scanErr := compRows.Scan(&comp.ComponentID, &comp.Quantity, &comp.ScrapPercent, &comp.BOMOutputQty); scanErr == nil {
					components = append(components, comp)
				}
			}
			compRows.Close()

			// Deduct each component from inventory
			for _, comp := range components {
				consumption := comp.Quantity * (qtyPlanned / comp.BOMOutputQty) * (1 + comp.ScrapPercent/100)

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
					"material_consumption", "Materials consumed at production start", now, userID)
			}

			if commitErr := tx.Commit(); commitErr != nil {
				h.log.Error("Failed to commit material consumption", "error", commitErr)
			} else {
				h.log.Info("Materials consumed for production order start", "order_id", id, "components", len(components))
			}
		}
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

	// --- Pause all in-progress work orders for this production order ---
	h.db.Exec(`
		UPDATE work_orders
		SET status = 'paused', updated_at = $1
		WHERE production_order_id = $2 AND tenant_id = $3
			AND deleted_at IS NULL AND status = 'in_progress'
	`, now, id, tenantID)

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

	// Optional: accept quantity produced and output details
	var input struct {
		QuantityProduced float64  `json:"quantity_produced"`
		QuantityScrapped float64  `json:"quantity_scrapped"`
		GoodQuantity     *float64 `json:"good_quantity"`
		RejectQuantity   *float64 `json:"reject_quantity"`
		PackageCount     *int     `json:"package_count"`
	}
	c.ShouldBindJSON(&input)

	now := time.Now()

	// Update production order
	query := `
		UPDATE production_orders
		SET status = 'completed', actual_end = $1, completed_by = $2, completed_at = $1, updated_at = $1,
			progress_percent = 100, current_stage = 'done'
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
	if input.GoodQuantity != nil {
		argCount++
		query += fmt.Sprintf(", good_quantity = $%d", argCount)
		args = append(args, *input.GoodQuantity)
		// Also set quantity_produced from good_quantity if not explicitly provided
		if input.QuantityProduced == 0 {
			argCount++
			query += fmt.Sprintf(", quantity_produced = $%d", argCount)
			args = append(args, *input.GoodQuantity)
		}
	}
	if input.RejectQuantity != nil {
		argCount++
		query += fmt.Sprintf(", reject_quantity = $%d", argCount)
		args = append(args, *input.RejectQuantity)
	}
	if input.PackageCount != nil {
		argCount++
		query += fmt.Sprintf(", package_count = $%d", argCount)
		args = append(args, *input.PackageCount)
	}

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query += fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL AND status IN ('in_progress', 'completed')", argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to complete production order", "error", err)
		response.InternalError(c, "Failed to complete production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or not in valid state")
		return
	}

	// --- Complete all remaining work orders for this production order ---
	h.db.Exec(`
		UPDATE work_orders
		SET status = 'completed', actual_end = $1
		WHERE production_order_id = $2 AND tenant_id = $3
			AND deleted_at IS NULL AND status NOT IN ('completed', 'done', 'cancelled')
	`, now, id, tenantID)

	// --- Inventory integration: add produced goods & consume materials ---
	// Skip if inventory was already updated for this production order (prevent double-add)
	// Check if finished goods were already added (prevent double-add on re-completion)
	// Only check for Receipt transactions - Issue transactions are from material consumption at start
	var existingReceiptCount int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2
		AND transaction_type = $3
	`, tenantID, id, entity.TransactionTypeReceipt).Scan(&existingReceiptCount)
	if existingReceiptCount > 0 {
		h.log.Info("Finished goods already added for production order, skipping", "order_id", id)
		h.GetProductionOrder(c)
		return
	}

	var productID uuid.UUID
	var bomID *uuid.UUID
	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	var qtyProduced, qtyPlanned float64

	err = h.db.QueryRow(`
		SELECT product_id, bom_id, warehouse_id, organization_id,
		       CASE WHEN COALESCE(quantity_produced, 0) > 0 THEN quantity_produced ELSE quantity_planned END,
		       quantity_planned
		FROM production_orders WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(&productID, &bomID, &warehouseID, &organizationID, &qtyProduced, &qtyPlanned)
	if err != nil {
		h.log.Error("Failed to fetch production order for inventory", "error", err)
		h.GetProductionOrder(c)
		return
	}

	if warehouseID == nil {
		var firstWH uuid.UUID
		if h.db.QueryRow(`SELECT id FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&firstWH) == nil {
			warehouseID = &firstWH
			h.db.Exec(`UPDATE production_orders SET warehouse_id = $1 WHERE id = $2 AND tenant_id = $3`, firstWH, id, tenantID)
		} else {
			h.log.Warn("Production order has no warehouse and no warehouses exist, skipping inventory update", "order_id", id)
			h.GetProductionOrder(c)
			return
		}
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

	// Note: BOM component consumption is handled in StartProductionOrder (when production begins)

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
				h.db.QueryRow(`SELECT code FROM production_orders WHERE id = $1`, id).Scan(&poNumber)

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

	// --- Cancel all remaining work orders for this production order ---
	h.db.Exec(`
		UPDATE work_orders
		SET status = 'cancelled', updated_at = $1
		WHERE production_order_id = $2 AND tenant_id = $3
			AND deleted_at IS NULL AND status NOT IN ('completed', 'done', 'cancelled')
	`, now, id, tenantID)

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
// EQUIPMENT HANDLERS
// =====================================================

func (h *Handler) ListEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	search := c.Query("search")
	status := c.Query("status")
	workCenterID := c.Query("work_center_id")

	query := `
		SELECT e.id, e.code, e.name, e.description, e.equipment_type, e.category,
			   e.work_center_id, wc.name as work_center_name,
			   e.manufacturer, e.model, e.serial_number,
			   e.purchase_date, e.warranty_expiry, e.status,
			   e.last_maintenance_date, e.next_maintenance_date, e.maintenance_interval_days,
			   e.purchase_cost, e.current_value, e.hourly_rate, e.notes,
			   e.created_at, e.updated_at
		FROM manufacturing_equipment e
		LEFT JOIN work_centers wc ON wc.id = e.work_center_id AND wc.deleted_at IS NULL
		WHERE e.tenant_id = $1 AND e.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	if search != "" {
		argCount++
		query += fmt.Sprintf(" AND (e.name ILIKE $%d OR e.code ILIKE $%d OR e.serial_number ILIKE $%d)", argCount, argCount, argCount)
		args = append(args, "%"+search+"%")
	}
	if status != "" {
		argCount++
		query += fmt.Sprintf(" AND e.status = $%d", argCount)
		args = append(args, status)
	}
	if workCenterID != "" {
		argCount++
		query += fmt.Sprintf(" AND e.work_center_id = $%d", argCount)
		args = append(args, workCenterID)
	}
	query += " ORDER BY e.name ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list equipment", "error", err)
		response.InternalError(c, "Failed to list equipment")
		return
	}
	defer rows.Close()

	type EquipmentItem struct {
		ID                     uuid.UUID  `json:"id"`
		Code                   string     `json:"code"`
		Name                   string     `json:"name"`
		Description            *string    `json:"description,omitempty"`
		EquipmentType          string     `json:"equipment_type"`
		Category               *string    `json:"category,omitempty"`
		WorkCenterID           *uuid.UUID `json:"work_center_id,omitempty"`
		WorkCenterName         *string    `json:"work_center_name,omitempty"`
		Manufacturer           *string    `json:"manufacturer,omitempty"`
		Model                  *string    `json:"model,omitempty"`
		SerialNumber           *string    `json:"serial_number,omitempty"`
		PurchaseDate           *string    `json:"purchase_date,omitempty"`
		WarrantyExpiry         *string    `json:"warranty_expiry,omitempty"`
		Status                 string     `json:"status"`
		LastMaintenanceDate    *string    `json:"last_maintenance_date,omitempty"`
		NextMaintenanceDate    *string    `json:"next_maintenance_date,omitempty"`
		MaintenanceIntervalDays *int      `json:"maintenance_interval_days,omitempty"`
		PurchaseCost           float64    `json:"purchase_cost"`
		CurrentValue           float64    `json:"current_value"`
		HourlyRate             float64    `json:"hourly_rate"`
		Notes                  *string    `json:"notes,omitempty"`
		CreatedAt              time.Time  `json:"created_at"`
		UpdatedAt              time.Time  `json:"updated_at"`
	}

	equipment := []EquipmentItem{}
	for rows.Next() {
		var e EquipmentItem
		var description, category, manufacturer, model, serialNumber, notes, workCenterName sql.NullString
		var workCenterID sql.NullString
		var purchaseDate, warrantyExpiry, lastMaint, nextMaint sql.NullTime
		var maintInterval sql.NullInt64

		err := rows.Scan(
			&e.ID, &e.Code, &e.Name, &description, &e.EquipmentType, &category,
			&workCenterID, &workCenterName,
			&manufacturer, &model, &serialNumber,
			&purchaseDate, &warrantyExpiry, &e.Status,
			&lastMaint, &nextMaint, &maintInterval,
			&e.PurchaseCost, &e.CurrentValue, &e.HourlyRate, &notes,
			&e.CreatedAt, &e.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan equipment", "error", err)
			continue
		}
		if description.Valid { e.Description = &description.String }
		if category.Valid { e.Category = &category.String }
		if workCenterID.Valid {
			id, _ := uuid.Parse(workCenterID.String)
			e.WorkCenterID = &id
		}
		if workCenterName.Valid { e.WorkCenterName = &workCenterName.String }
		if manufacturer.Valid { e.Manufacturer = &manufacturer.String }
		if model.Valid { e.Model = &model.String }
		if serialNumber.Valid { e.SerialNumber = &serialNumber.String }
		if notes.Valid { e.Notes = &notes.String }
		if purchaseDate.Valid { s := purchaseDate.Time.Format("2006-01-02"); e.PurchaseDate = &s }
		if warrantyExpiry.Valid { s := warrantyExpiry.Time.Format("2006-01-02"); e.WarrantyExpiry = &s }
		if lastMaint.Valid { s := lastMaint.Time.Format("2006-01-02"); e.LastMaintenanceDate = &s }
		if nextMaint.Valid { s := nextMaint.Time.Format("2006-01-02"); e.NextMaintenanceDate = &s }
		if maintInterval.Valid { v := int(maintInterval.Int64); e.MaintenanceIntervalDays = &v }
		equipment = append(equipment, e)
	}

	response.Success(c, equipment)
}

func (h *Handler) CreateEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input struct {
		Code                    string  `json:"code"`
		Name                    string  `json:"name" binding:"required"`
		Description             *string `json:"description"`
		EquipmentType           string  `json:"equipment_type"`
		Category                *string `json:"category"`
		WorkCenterID            *string `json:"work_center_id"`
		Manufacturer            *string `json:"manufacturer"`
		Model                   *string `json:"model"`
		SerialNumber            *string `json:"serial_number"`
		PurchaseDate            *string `json:"purchase_date"`
		WarrantyExpiry          *string `json:"warranty_expiry"`
		Status                  string  `json:"status"`
		MaintenanceIntervalDays *int    `json:"maintenance_interval_days"`
		PurchaseCost            float64 `json:"purchase_cost"`
		CurrentValue            float64 `json:"current_value"`
		HourlyRate              float64 `json:"hourly_rate"`
		Notes                   *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	if input.Code == "" {
		input.Code = fmt.Sprintf("EQ-%d", time.Now().UnixMilli())
	}
	if input.EquipmentType == "" {
		input.EquipmentType = "machine"
	}
	if input.Status == "" {
		input.Status = "operational"
	}

	var workCenterID *uuid.UUID
	if input.WorkCenterID != nil && *input.WorkCenterID != "" {
		id, err := uuid.Parse(*input.WorkCenterID)
		if err == nil {
			workCenterID = &id
		}
	}

	var purchaseDate, warrantyExpiry interface{}
	if input.PurchaseDate != nil && *input.PurchaseDate != "" {
		purchaseDate = *input.PurchaseDate
	}
	if input.WarrantyExpiry != nil && *input.WarrantyExpiry != "" {
		warrantyExpiry = *input.WarrantyExpiry
	}

	id := uuid.New()
	now := time.Now()

	_, err := h.db.Exec(`
		INSERT INTO manufacturing_equipment (
			id, tenant_id, code, name, description, equipment_type, category,
			work_center_id, manufacturer, model, serial_number,
			purchase_date, warranty_expiry, status,
			maintenance_interval_days, purchase_cost, current_value, hourly_rate,
			notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
	`, id, tenantID, input.Code, input.Name, input.Description, input.EquipmentType, input.Category,
		workCenterID, input.Manufacturer, input.Model, input.SerialNumber,
		purchaseDate, warrantyExpiry, input.Status,
		input.MaintenanceIntervalDays, input.PurchaseCost, input.CurrentValue, input.HourlyRate,
		input.Notes, userID, now, now)
	if err != nil {
		h.log.Error("Failed to create equipment", "error", err)
		if strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Equipment code already exists")
			return
		}
		response.InternalError(c, "Failed to create equipment")
		return
	}

	response.Created(c, gin.H{"id": id, "code": input.Code, "name": input.Name, "status": input.Status})
}

func (h *Handler) UpdateEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid equipment ID")
		return
	}

	var input struct {
		Name                    *string  `json:"name"`
		Description             *string  `json:"description"`
		EquipmentType           *string  `json:"equipment_type"`
		Category                *string  `json:"category"`
		WorkCenterID            *string  `json:"work_center_id"`
		Manufacturer            *string  `json:"manufacturer"`
		Model                   *string  `json:"model"`
		SerialNumber            *string  `json:"serial_number"`
		PurchaseDate            *string  `json:"purchase_date"`
		WarrantyExpiry          *string  `json:"warranty_expiry"`
		Status                  *string  `json:"status"`
		MaintenanceIntervalDays *int     `json:"maintenance_interval_days"`
		PurchaseCost            *float64 `json:"purchase_cost"`
		CurrentValue            *float64 `json:"current_value"`
		HourlyRate              *float64 `json:"hourly_rate"`
		Notes                   *string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil { argCount++; updates = append(updates, fmt.Sprintf("name = $%d", argCount)); args = append(args, *input.Name) }
	if input.Description != nil { argCount++; updates = append(updates, fmt.Sprintf("description = $%d", argCount)); args = append(args, *input.Description) }
	if input.EquipmentType != nil { argCount++; updates = append(updates, fmt.Sprintf("equipment_type = $%d", argCount)); args = append(args, *input.EquipmentType) }
	if input.Category != nil { argCount++; updates = append(updates, fmt.Sprintf("category = $%d", argCount)); args = append(args, *input.Category) }
	if input.WorkCenterID != nil {
		if *input.WorkCenterID == "" {
			argCount++; updates = append(updates, fmt.Sprintf("work_center_id = $%d", argCount)); args = append(args, nil)
		} else if id, err := uuid.Parse(*input.WorkCenterID); err == nil {
			argCount++; updates = append(updates, fmt.Sprintf("work_center_id = $%d", argCount)); args = append(args, id)
		}
	}
	if input.Manufacturer != nil { argCount++; updates = append(updates, fmt.Sprintf("manufacturer = $%d", argCount)); args = append(args, *input.Manufacturer) }
	if input.Model != nil { argCount++; updates = append(updates, fmt.Sprintf("model = $%d", argCount)); args = append(args, *input.Model) }
	if input.SerialNumber != nil { argCount++; updates = append(updates, fmt.Sprintf("serial_number = $%d", argCount)); args = append(args, *input.SerialNumber) }
	if input.Status != nil { argCount++; updates = append(updates, fmt.Sprintf("status = $%d", argCount)); args = append(args, *input.Status) }
	if input.PurchaseCost != nil { argCount++; updates = append(updates, fmt.Sprintf("purchase_cost = $%d", argCount)); args = append(args, *input.PurchaseCost) }
	if input.CurrentValue != nil { argCount++; updates = append(updates, fmt.Sprintf("current_value = $%d", argCount)); args = append(args, *input.CurrentValue) }
	if input.HourlyRate != nil { argCount++; updates = append(updates, fmt.Sprintf("hourly_rate = $%d", argCount)); args = append(args, *input.HourlyRate) }
	if input.MaintenanceIntervalDays != nil { argCount++; updates = append(updates, fmt.Sprintf("maintenance_interval_days = $%d", argCount)); args = append(args, *input.MaintenanceIntervalDays) }
	if input.Notes != nil { argCount++; updates = append(updates, fmt.Sprintf("notes = $%d", argCount)); args = append(args, *input.Notes) }

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++; updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount)); args = append(args, time.Now())
	argCount++; args = append(args, equipID)
	argCount++; args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE manufacturing_equipment SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)
	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update equipment", "error", err)
		response.InternalError(c, "Failed to update equipment")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Equipment not found")
		return
	}
	response.Success(c, gin.H{"message": "Equipment updated successfully"})
}

func (h *Handler) DeleteEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid equipment ID")
		return
	}

	result, err := h.db.Exec(
		"UPDATE manufacturing_equipment SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), equipID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete equipment")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Equipment not found")
		return
	}
	response.Success(c, gin.H{"message": "Equipment deleted"})
}

func (h *Handler) ListMaintenanceTasks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID := c.Param("id")

	rows, err := h.db.Query(`
		SELECT m.id, m.equipment_id, m.work_center_id, m.maintenance_type,
			   m.scheduled_date, m.actual_date, m.duration_hours,
			   m.description, m.work_performed, m.status,
			   m.labor_cost, m.parts_cost, m.total_cost, m.notes,
			   m.created_at, m.updated_at
		FROM equipment_maintenance m
		WHERE m.tenant_id = $1 AND m.equipment_id = $2
		ORDER BY m.scheduled_date DESC
	`, tenantID, equipID)
	if err != nil {
		h.log.Error("Failed to list maintenance tasks", "error", err)
		response.InternalError(c, "Failed to list maintenance tasks")
		return
	}
	defer rows.Close()

	type MaintenanceTask struct {
		ID              uuid.UUID  `json:"id"`
		EquipmentID     uuid.UUID  `json:"equipment_id"`
		WorkCenterID    *uuid.UUID `json:"work_center_id,omitempty"`
		MaintenanceType string     `json:"maintenance_type"`
		ScheduledDate   *string    `json:"scheduled_date,omitempty"`
		ActualDate      *string    `json:"actual_date,omitempty"`
		DurationHours   float64    `json:"duration_hours"`
		Description     *string    `json:"description,omitempty"`
		WorkPerformed   *string    `json:"work_performed,omitempty"`
		Status          string     `json:"status"`
		LaborCost       float64    `json:"labor_cost"`
		PartsCost       float64    `json:"parts_cost"`
		TotalCost       float64    `json:"total_cost"`
		Notes           *string    `json:"notes,omitempty"`
		CreatedAt       time.Time  `json:"created_at"`
		UpdatedAt       time.Time  `json:"updated_at"`
	}

	tasks := []MaintenanceTask{}
	for rows.Next() {
		var t MaintenanceTask
		var workCenterID sql.NullString
		var scheduledDate, actualDate sql.NullTime
		var description, workPerformed, notes sql.NullString

		err := rows.Scan(
			&t.ID, &t.EquipmentID, &workCenterID, &t.MaintenanceType,
			&scheduledDate, &actualDate, &t.DurationHours,
			&description, &workPerformed, &t.Status,
			&t.LaborCost, &t.PartsCost, &t.TotalCost, &notes,
			&t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan maintenance task", "error", err)
			continue
		}
		if workCenterID.Valid { id, _ := uuid.Parse(workCenterID.String); t.WorkCenterID = &id }
		if scheduledDate.Valid { s := scheduledDate.Time.Format("2006-01-02"); t.ScheduledDate = &s }
		if actualDate.Valid { s := actualDate.Time.Format("2006-01-02"); t.ActualDate = &s }
		if description.Valid { t.Description = &description.String }
		if workPerformed.Valid { t.WorkPerformed = &workPerformed.String }
		if notes.Valid { t.Notes = &notes.String }
		tasks = append(tasks, t)
	}

	response.Success(c, tasks)
}

func (h *Handler) CreateMaintenanceTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipIDStr := c.Param("id")
	equipID, err := uuid.Parse(equipIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid equipment ID")
		return
	}

	var input struct {
		MaintenanceType string  `json:"maintenance_type" binding:"required"`
		ScheduledDate   string  `json:"scheduled_date" binding:"required"`
		Description     *string `json:"description"`
		DurationHours   float64 `json:"duration_hours"`
		LaborCost       float64 `json:"labor_cost"`
		PartsCost       float64 `json:"parts_cost"`
		Notes           *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	id := uuid.New()
	now := time.Now()
	totalCost := input.LaborCost + input.PartsCost

	_, err = h.db.Exec(`
		INSERT INTO equipment_maintenance (
			id, tenant_id, equipment_id, maintenance_type,
			scheduled_date, duration_hours, description,
			status, labor_cost, parts_cost, total_cost, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'scheduled', $8, $9, $10, $11, $12, $12)
	`, id, tenantID, equipID, input.MaintenanceType,
		input.ScheduledDate, input.DurationHours, input.Description,
		input.LaborCost, input.PartsCost, totalCost, input.Notes, now)
	if err != nil {
		h.log.Error("Failed to create maintenance task", "error", err)
		response.InternalError(c, "Failed to create maintenance task")
		return
	}

	// Update next_maintenance_date on the equipment
	h.db.Exec("UPDATE manufacturing_equipment SET next_maintenance_date = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		input.ScheduledDate, now, equipID, tenantID)

	response.Created(c, gin.H{"id": id, "status": "scheduled"})
}

func (h *Handler) CompleteMaintenanceTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID := c.Param("id")
	taskID, err := uuid.Parse(c.Param("task_id"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	var input struct {
		WorkPerformed string  `json:"work_performed"`
		DurationHours float64 `json:"duration_hours"`
		LaborCost     float64 `json:"labor_cost"`
		PartsCost     float64 `json:"parts_cost"`
		Notes         *string `json:"notes"`
	}
	c.ShouldBindJSON(&input)

	now := time.Now()
	totalCost := input.LaborCost + input.PartsCost
	today := now.Format("2006-01-02")

	result, err := h.db.Exec(`
		UPDATE equipment_maintenance
		SET status = 'completed', actual_date = $1, work_performed = $2,
			duration_hours = $3, labor_cost = $4, parts_cost = $5, total_cost = $6,
			notes = $7, updated_at = $8
		WHERE id = $9 AND tenant_id = $10 AND equipment_id = $11
	`, today, input.WorkPerformed, input.DurationHours,
		input.LaborCost, input.PartsCost, totalCost, input.Notes, now,
		taskID, tenantID, equipID)
	if err != nil {
		h.log.Error("Failed to complete maintenance task", "error", err)
		response.InternalError(c, "Failed to complete maintenance task")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Maintenance task not found")
		return
	}

	// Update last_maintenance_date on equipment
	h.db.Exec("UPDATE manufacturing_equipment SET last_maintenance_date = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		today, now, equipID, tenantID)

	response.Success(c, gin.H{"message": "Maintenance task completed"})
}

func (h *Handler) UpdateMaintenanceTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID := c.Param("id")
	taskID, err := uuid.Parse(c.Param("task_id"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	var input struct {
		MaintenanceType *string  `json:"maintenance_type"`
		ScheduledDate   *string  `json:"scheduled_date"`
		DurationHours   *float64 `json:"duration_hours"`
		Description     *string  `json:"description"`
		Notes           *string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.MaintenanceType != nil { argCount++; updates = append(updates, fmt.Sprintf("maintenance_type = $%d", argCount)); args = append(args, *input.MaintenanceType) }
	if input.ScheduledDate != nil { argCount++; updates = append(updates, fmt.Sprintf("scheduled_date = $%d", argCount)); args = append(args, *input.ScheduledDate) }
	if input.DurationHours != nil { argCount++; updates = append(updates, fmt.Sprintf("duration_hours = $%d", argCount)); args = append(args, *input.DurationHours) }
	if input.Description != nil { argCount++; updates = append(updates, fmt.Sprintf("description = $%d", argCount)); args = append(args, *input.Description) }
	if input.Notes != nil { argCount++; updates = append(updates, fmt.Sprintf("notes = $%d", argCount)); args = append(args, *input.Notes) }

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++; updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount)); args = append(args, time.Now())
	argCount++; args = append(args, taskID)
	argCount++; args = append(args, tenantID)
	argCount++; args = append(args, equipID)

	query := fmt.Sprintf(
		"UPDATE equipment_maintenance SET %s WHERE id = $%d AND tenant_id = $%d AND equipment_id = $%d",
		strings.Join(updates, ", "), argCount-2, argCount-1, argCount,
	)
	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update maintenance task", "error", err)
		response.InternalError(c, "Failed to update maintenance task")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Maintenance task not found")
		return
	}
	response.Success(c, gin.H{"message": "Maintenance task updated"})
}
