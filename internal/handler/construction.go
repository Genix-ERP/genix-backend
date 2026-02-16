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
// CONSTRUCTION PROJECT HANDLERS
// =====================================================

// ListConstructionProjects returns a paginated list of construction projects
// ListConstructionProjects godoc
// @Summary List construction projects
// @Description Get a paginated list of construction projects
// @Tags Construction
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param status query string false "Filter by status"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects [get]
func (h *Handler) ListConstructionProjects(c *gin.Context) {
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
	if limit < 1 || limit > 100 {
		limit = 50
	}

	// Parse filters
	status := c.Query("status")
	search := c.Query("search")
	projectType := c.Query("project_type")

	// Build query
	baseQuery := `
		SELECT cp.id, cp.tenant_id, cp.organization_id, cp.code, cp.name, cp.description,
		       cp.address, cp.city, cp.district, cp.region,
		       cp.client_name, cp.client_contact, cp.client_phone,
		       cp.project_type, cp.building_type, cp.total_area, cp.floors_count,
		       cp.contract_amount, cp.currency,
		       cp.contract_date, cp.planned_start_date, cp.planned_end_date,
		       cp.actual_start_date, cp.actual_end_date,
		       cp.status, cp.progress_percent,
		       cp.project_manager_id, cp.chief_engineer_id,
		       cp.created_by, cp.created_date, cp.updated_date,
		       COALESCE(pm.first_name || ' ' || pm.last_name, '') as project_manager_name,
		       COALESCE(ce.first_name || ' ' || ce.last_name, '') as chief_engineer_name,
		       COALESCE((SELECT COUNT(*) FROM smeta_sections WHERE project_id = cp.id), 0) as sections_count,
		       COALESCE((SELECT SUM(total_cost) FROM smeta_sections WHERE project_id = cp.id), 0) as total_smeta
		FROM construction_projects cp
		LEFT JOIN employees pm ON pm.id = cp.project_manager_id
		LEFT JOIN employees ce ON ce.id = cp.chief_engineer_id
		WHERE cp.tenant_id = $1 AND cp.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM construction_projects WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND cp.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	if projectType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND cp.project_type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND project_type = $%d", argCount)
		args = append(args, projectType)
	}

	if search != "" {
		argCount++
		searchPattern := "%" + search + "%"
		baseQuery += fmt.Sprintf(" AND (cp.code ILIKE $%d OR cp.name ILIKE $%d OR cp.client_name ILIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (code ILIKE $%d OR name ILIKE $%d OR client_name ILIKE $%d)", argCount, argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count construction projects", "error", err)
		response.InternalError(c, "Failed to count projects")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY cp.created_date DESC"
	pagination := entity.NewPagination(page, limit)
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", pagination.Limit, pagination.Offset())

	// Get projects
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to query construction projects", "error", err)
		response.InternalError(c, "Failed to query projects")
		return
	}
	defer rows.Close()

	projects := []entity.ConstructionProject{}
	for rows.Next() {
		var p entity.ConstructionProject
		if err := rows.Scan(
			&p.ID, &p.TenantID, &p.OrganizationID, &p.Code, &p.Name, &p.Description,
			&p.Address, &p.City, &p.District, &p.Region,
			&p.ClientName, &p.ClientContact, &p.ClientPhone,
			&p.ProjectType, &p.BuildingType, &p.TotalArea, &p.FloorsCount,
			&p.ContractAmount, &p.Currency,
			&p.ContractDate, &p.PlannedStartDate, &p.PlannedEndDate,
			&p.ActualStartDate, &p.ActualEndDate,
			&p.Status, &p.ProgressPercent,
			&p.ProjectManagerID, &p.ChiefEngineerID,
			&p.CreatedBy, &p.CreatedDate, &p.UpdatedDate,
			&p.ProjectManagerName, &p.ChiefEngineerName,
			&p.SectionsCount, &p.TotalSmeta,
		); err != nil {
			h.log.Error("Failed to scan construction project", "error", err)
			continue
		}
		projects = append(projects, p)
	}

	pagination.Calculate(total)
	response.SuccessWithPagination(c, projects, pagination)
}

// GetConstructionProject returns a single construction project by ID
// GetConstructionProject godoc
// @Summary Get construction project by ID
// @Description Get detailed information about a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id} [get]
func (h *Handler) GetConstructionProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT cp.id, cp.tenant_id, cp.organization_id, cp.code, cp.name, cp.description,
		       cp.address, cp.city, cp.district, cp.region, cp.coordinates,
		       cp.client_name, cp.client_contact, cp.client_phone,
		       cp.project_type, cp.building_type, cp.total_area, cp.floors_count,
		       cp.contract_amount, cp.currency,
		       cp.contract_date, cp.planned_start_date, cp.planned_end_date,
		       cp.actual_start_date, cp.actual_end_date,
		       cp.status, cp.progress_percent,
		       cp.project_manager_id, cp.chief_engineer_id,
		       cp.created_by, cp.created_date, cp.updated_date,
		       COALESCE(pm.first_name || ' ' || pm.last_name, '') as project_manager_name,
		       COALESCE(ce.first_name || ' ' || ce.last_name, '') as chief_engineer_name,
		       COALESCE((SELECT COUNT(*) FROM smeta_sections WHERE project_id = cp.id), 0) as sections_count,
		       COALESCE((SELECT SUM(total_cost) FROM smeta_sections WHERE project_id = cp.id), 0) as total_smeta
		FROM construction_projects cp
		LEFT JOIN employees pm ON pm.id = cp.project_manager_id
		LEFT JOIN employees ce ON ce.id = cp.chief_engineer_id
		WHERE cp.id = $1 AND cp.tenant_id = $2 AND cp.deleted_at IS NULL
	`

	var p entity.ConstructionProject
	err = h.db.QueryRow(query, id, tenantID).Scan(
		&p.ID, &p.TenantID, &p.OrganizationID, &p.Code, &p.Name, &p.Description,
		&p.Address, &p.City, &p.District, &p.Region, &p.Coordinates,
		&p.ClientName, &p.ClientContact, &p.ClientPhone,
		&p.ProjectType, &p.BuildingType, &p.TotalArea, &p.FloorsCount,
		&p.ContractAmount, &p.Currency,
		&p.ContractDate, &p.PlannedStartDate, &p.PlannedEndDate,
		&p.ActualStartDate, &p.ActualEndDate,
		&p.Status, &p.ProgressPercent,
		&p.ProjectManagerID, &p.ChiefEngineerID,
		&p.CreatedBy, &p.CreatedDate, &p.UpdatedDate,
		&p.ProjectManagerName, &p.ChiefEngineerName,
		&p.SectionsCount, &p.TotalSmeta,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Project not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to query construction project", "error", err)
		response.InternalError(c, "Failed to query project")
		return
	}

	response.Success(c, p)
}

// CreateConstructionProject creates a new construction project
// CreateConstructionProject godoc
// @Summary Create construction project
// @Description Create a new construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param project body entity.CreateConstructionProjectInput true "Project data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects [post]
func (h *Handler) CreateConstructionProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	var req entity.CreateConstructionProjectInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Set default currency if not provided
	currency := req.Currency
	if currency == "" {
		currency = "UZS"
	}

	query := `
		INSERT INTO construction_projects (
			tenant_id, organization_id, code, name, description,
			address, city, district, region,
			client_name, client_contact, client_phone,
			project_type, building_type, total_area, floors_count,
			contract_amount, currency,
			contract_date, planned_start_date, planned_end_date,
			status, project_manager_id, chief_engineer_id,
			created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, NOW(), NOW())
		RETURNING id, created_date
	`

	var contractDate, plannedStart, plannedEnd *time.Time
	if req.ContractDate != "" {
		t, _ := time.Parse("2006-01-02", req.ContractDate)
		contractDate = &t
	}
	if req.PlannedStartDate != "" {
		t, _ := time.Parse("2006-01-02", req.PlannedStartDate)
		plannedStart = &t
	}
	if req.PlannedEndDate != "" {
		t, _ := time.Parse("2006-01-02", req.PlannedEndDate)
		plannedEnd = &t
	}

	var projectID int64
	var createdDate time.Time
	err := h.db.QueryRow(query,
		tenantID, orgID, req.Code, req.Name, nullString(req.Description),
		nullString(req.Address), nullString(req.City), nullString(req.District), nullString(req.Region),
		nullString(req.ClientName), nullString(req.ClientContact), nullString(req.ClientPhone),
		nullString(req.ProjectType), nullString(req.BuildingType), nullFloat64(req.TotalArea), nullInt32(int32(req.FloorsCount)),
		nullFloat64(req.ContractAmount), currency,
		contractDate, plannedStart, plannedEnd,
		"draft", nullUUID(req.ProjectManagerID), nullUUID(req.ChiefEngineerID),
		userID,
	).Scan(&projectID, &createdDate)
	if err != nil {
		h.log.Error("Failed to create construction project", "error", err)
		response.InternalError(c, "Failed to create project")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":           projectID,
		"code":         req.Code,
		"name":         req.Name,
		"status":       "draft",
		"created_date": createdDate,
	})
}

// UpdateConstructionProject updates a construction project
// UpdateConstructionProject godoc
// @Summary Update construction project
// @Description Update an existing construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param project body entity.UpdateConstructionProjectInput true "Project data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id} [put]
func (h *Handler) UpdateConstructionProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req entity.UpdateConstructionProjectInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *req.Name)
	}
	if req.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *req.Description)
	}
	if req.Address != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("address = $%d", argCount))
		args = append(args, *req.Address)
	}
	if req.City != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("city = $%d", argCount))
		args = append(args, *req.City)
	}
	if req.ClientName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_name = $%d", argCount))
		args = append(args, *req.ClientName)
	}
	if req.ProjectType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("project_type = $%d", argCount))
		args = append(args, *req.ProjectType)
	}
	if req.ContractAmount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("contract_amount = $%d", argCount))
		args = append(args, *req.ContractAmount)
	}
	if req.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *req.Status)
	}
	if req.ProgressPercent != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("progress_percent = $%d", argCount))
		args = append(args, *req.ProgressPercent)
	}
	if req.ProjectManagerID != nil && *req.ProjectManagerID != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("project_manager_id = $%d", argCount))
		parsed, _ := uuid.Parse(*req.ProjectManagerID)
		args = append(args, parsed)
	}
	if req.ChiefEngineerID != nil && *req.ChiefEngineerID != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("chief_engineer_id = $%d", argCount))
		parsed, _ := uuid.Parse(*req.ChiefEngineerID)
		args = append(args, parsed)
	}
	if req.PlannedStartDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("planned_start_date = $%d", argCount))
		t, _ := time.Parse("2006-01-02", *req.PlannedStartDate)
		args = append(args, t)
	}
	if req.PlannedEndDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("planned_end_date = $%d", argCount))
		t, _ := time.Parse("2006-01-02", *req.PlannedEndDate)
		args = append(args, t)
	}
	if req.ActualStartDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("actual_start_date = $%d", argCount))
		t, _ := time.Parse("2006-01-02", *req.ActualStartDate)
		args = append(args, t)
	}
	if req.ActualEndDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("actual_end_date = $%d", argCount))
		t, _ := time.Parse("2006-01-02", *req.ActualEndDate)
		args = append(args, t)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_date
	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE conditions
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE construction_projects SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		joinStrings(updates, ", "), argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update construction project", "error", err)
		response.InternalError(c, "Failed to update project")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Project not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      id,
		"message": "Project updated successfully",
	})
}

// DeleteConstructionProject soft deletes a construction project
// DeleteConstructionProject godoc
// @Summary Delete construction project
// @Description Soft delete a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id} [delete]
func (h *Handler) DeleteConstructionProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `UPDATE construction_projects SET deleted_at = NOW() WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`
	result, err := h.db.Exec(query, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete construction project", "error", err)
		response.InternalError(c, "Failed to delete project")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Project not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Project deleted successfully",
	})
}

// GetConstructionProjectDashboard returns dashboard statistics for a project
// GetConstructionProjectDashboard godoc
// @Summary Get project dashboard
// @Description Get dashboard statistics and summary for a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/dashboard [get]
func (h *Handler) GetConstructionProjectDashboard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	dashboard := map[string]interface{}{}

	// Get project basic info
	var projectName, status string
	var progressPercent sql.NullFloat64
	var contractAmount sql.NullFloat64
	err = h.db.QueryRow(`
		SELECT name, status, progress_percent, contract_amount
		FROM construction_projects WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&projectName, &status, &progressPercent, &contractAmount)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Project not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get project info", "error", err)
		response.InternalError(c, "Failed to get project info")
		return
	}

	dashboard["project_name"] = projectName
	dashboard["status"] = status
	dashboard["progress_percent"] = progressPercent.Float64
	dashboard["contract_amount"] = contractAmount.Float64

	// Get smeta summary
	var totalSections, totalItems int
	var totalSmetaCost float64
	h.db.QueryRow(`
		SELECT COUNT(*) FROM smeta_sections WHERE project_id = $1
	`, id).Scan(&totalSections)
	h.db.QueryRow(`
		SELECT COUNT(*) FROM smeta_items si
		JOIN smeta_sections ss ON si.section_id = ss.id
		WHERE ss.project_id = $1
	`, id).Scan(&totalItems)
	h.db.QueryRow(`
		SELECT COALESCE(SUM(total_cost), 0) FROM smeta_sections WHERE project_id = $1
	`, id).Scan(&totalSmetaCost)

	dashboard["smeta"] = map[string]interface{}{
		"sections_count": totalSections,
		"items_count":    totalItems,
		"total_cost":     totalSmetaCost,
	}

	// Get vendor summary
	var vendorCount int
	var totalOrdered, totalReceived, totalPaid float64
	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_ordered), 0), COALESCE(SUM(total_received), 0), COALESCE(SUM(total_paid), 0)
		FROM construction_project_vendors WHERE project_id = $1
	`, id).Scan(&vendorCount, &totalOrdered, &totalReceived, &totalPaid)

	dashboard["vendors"] = map[string]interface{}{
		"count":          vendorCount,
		"total_ordered":  totalOrdered,
		"total_received": totalReceived,
		"total_paid":     totalPaid,
	}

	// Get recent photo reports count
	var photoReportsCount int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM construction_photo_reports
		WHERE project_id = $1 AND report_date >= NOW() - INTERVAL '30 days'
	`, id).Scan(&photoReportsCount)

	dashboard["photo_reports_30d"] = photoReportsCount

	response.Success(c, dashboard)
}

// =====================================================
// BUILDING HANDLERS
// =====================================================

// ListConstructionBuildings returns buildings for a project
// ListConstructionBuildings godoc
// @Summary List construction buildings
// @Description Get a list of buildings for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/buildings [get]
func (h *Handler) ListConstructionBuildings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT b.id, b.tenant_id, b.project_id, b.code, b.name, b.description,
		       b.building_type, b.building_purpose, b.floors_count, b.floors_underground,
		       b.total_area, b.living_area, b.non_living_area,
		       b.apartments_count, b.commercial_units_count, b.parking_spots,
		       b.estimated_cost, b.actual_cost, COALESCE(b.currency, 'UZS'),
		       b.planned_start_date, b.planned_end_date, b.actual_start_date, b.actual_end_date,
		       COALESCE(b.status, 'draft'), b.progress_percent, COALESCE(b.gps_coordinates::text, '{}')::text, b.location_description,
		       COALESCE(b.sort_order, 0), b.created_date, b.updated_date,
		       0 as sections_count,
		       0.0 as total_smeta
		FROM construction_buildings b
		WHERE b.project_id = $1 AND b.tenant_id = $2
		ORDER BY COALESCE(b.sort_order, 0), b.code
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query buildings", "error", err)
		response.InternalError(c, "Failed to query buildings")
		return
	}
	defer rows.Close()

	buildings := []entity.ConstructionBuilding{}
	for rows.Next() {
		var b entity.ConstructionBuilding
		var gpsCoordinates string
		if err := rows.Scan(
			&b.ID, &b.TenantID, &b.ProjectID, &b.Code, &b.Name, &b.Description,
			&b.BuildingType, &b.BuildingPurpose, &b.FloorsCount, &b.FloorsUnderground,
			&b.TotalArea, &b.LivingArea, &b.NonLivingArea,
			&b.ApartmentsCount, &b.CommercialUnitsCount, &b.ParkingSpots,
			&b.EstimatedCost, &b.ActualCost, &b.Currency,
			&b.PlannedStartDate, &b.PlannedEndDate, &b.ActualStartDate, &b.ActualEndDate,
			&b.Status, &b.ProgressPercent, &gpsCoordinates, &b.LocationDescription,
			&b.SortOrder, &b.CreatedDate, &b.UpdatedDate,
			&b.SectionsCount, &b.TotalSmeta,
		); err != nil {
			h.log.Error("Failed to scan building", "error", err, "project_id", projectID)
			continue
		}
		b.GpsCoordinates = json.RawMessage(gpsCoordinates)
		buildings = append(buildings, b)
	}

	response.Success(c, buildings)
}

// CreateConstructionBuilding creates a new building
// CreateConstructionBuilding godoc
// @Summary Create construction building
// @Description Create a new building for a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param building body entity.CreateConstructionBuildingInput true "Building data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/buildings [post]
func (h *Handler) CreateConstructionBuilding(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req entity.CreateConstructionBuildingInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var plannedStart, plannedEnd interface{}
	if req.PlannedStartDate != "" {
		t, _ := time.Parse("2006-01-02", req.PlannedStartDate)
		plannedStart = t
	}
	if req.PlannedEndDate != "" {
		t, _ := time.Parse("2006-01-02", req.PlannedEndDate)
		plannedEnd = t
	}

	query := `
		INSERT INTO construction_buildings (
			tenant_id, project_id, code, name, description,
			building_type, building_purpose, floors_count, floors_underground,
			total_area, living_area, non_living_area,
			apartments_count, commercial_units_count, parking_spots,
			estimated_cost, currency,
			planned_start_date, planned_end_date,
			status, sort_order, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 'UZS', $17, $18, 'draft', $19, NOW(), NOW())
		RETURNING id, created_date
	`

	var buildingID int64
	var createdDate time.Time
	err = h.db.QueryRow(query,
		tenantID, projectID, req.Code, req.Name, nullString(req.Description),
		nullString(req.BuildingType), nullString(req.BuildingPurpose),
		nullInt32(int32(req.FloorsCount)), nullInt32(int32(req.FloorsUnderground)),
		nullFloat64(req.TotalArea), nullFloat64(req.LivingArea), nullFloat64(req.NonLivingArea),
		nullInt32(int32(req.ApartmentsCount)), nullInt32(int32(req.CommercialUnitsCount)), nullInt32(int32(req.ParkingSpots)),
		nullFloat64(req.EstimatedCost),
		plannedStart, plannedEnd,
		req.SortOrder,
	).Scan(&buildingID, &createdDate)
	if err != nil {
		h.log.Error("Failed to create building", "error", err)
		response.InternalError(c, "Failed to create building")
		return
	}

	// Update project buildings count
	h.db.Exec(`UPDATE construction_projects SET buildings_count = buildings_count + 1, updated_date = NOW() WHERE id = $1`, projectID)

	response.Created(c, map[string]interface{}{
		"id":           buildingID,
		"code":         req.Code,
		"name":         req.Name,
		"created_date": createdDate,
	})
}

// UpdateConstructionBuilding updates a building
// UpdateConstructionBuilding godoc
// @Summary Update construction building
// @Description Update an existing building in a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param building_id path int true "Building ID"
// @Param building body entity.UpdateConstructionBuildingInput true "Building data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/buildings/{building_id} [put]
func (h *Handler) UpdateConstructionBuilding(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	buildingID, err := strconv.ParseInt(c.Param("building_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid building ID")
		return
	}

	var req entity.UpdateConstructionBuildingInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *req.Name)
	}
	if req.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *req.Description)
	}
	if req.BuildingType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("building_type = $%d", argCount))
		args = append(args, *req.BuildingType)
	}
	if req.BuildingPurpose != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("building_purpose = $%d", argCount))
		args = append(args, *req.BuildingPurpose)
	}
	if req.FloorsCount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("floors_count = $%d", argCount))
		args = append(args, *req.FloorsCount)
	}
	if req.TotalArea != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("total_area = $%d", argCount))
		args = append(args, *req.TotalArea)
	}
	if req.ApartmentsCount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("apartments_count = $%d", argCount))
		args = append(args, *req.ApartmentsCount)
	}
	if req.EstimatedCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("estimated_cost = $%d", argCount))
		args = append(args, *req.EstimatedCost)
	}
	if req.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *req.Status)
	}
	if req.ProgressPercent != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("progress_percent = $%d", argCount))
		args = append(args, *req.ProgressPercent)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = NOW()"))
	args = append(args, buildingID)
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE construction_buildings
		SET %s
		WHERE id = $%d AND tenant_id = $%d
	`, strings.Join(updates, ", "), argCount, argCount+1)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update building", "error", err)
		response.InternalError(c, "Failed to update building")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Building not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Building updated successfully"})
}

// DeleteConstructionBuilding deletes a building
// DeleteConstructionBuilding godoc
// @Summary Delete construction building
// @Description Delete a building from a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param building_id path int true "Building ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/buildings/{building_id} [delete]
func (h *Handler) DeleteConstructionBuilding(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	buildingID, err := strconv.ParseInt(c.Param("building_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid building ID")
		return
	}

	result, err := h.db.Exec(`DELETE FROM construction_buildings WHERE id = $1 AND tenant_id = $2`, buildingID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete building", "error", err)
		response.InternalError(c, "Failed to delete building")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Building not found")
		return
	}

	// Update project buildings count
	h.db.Exec(`UPDATE construction_projects SET buildings_count = GREATEST(0, buildings_count - 1), updated_date = NOW() WHERE id = $1`, projectID)

	response.Success(c, map[string]interface{}{"message": "Building deleted successfully"})
}

// =====================================================
// SMETA SECTION HANDLERS
// =====================================================

// ListSmetaSections returns smeta sections for a project
// ListSmetaSections godoc
// @Summary List smeta sections
// @Description Get a list of smeta sections for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/smeta/sections [get]
func (h *Handler) ListSmetaSections(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT ss.id, ss.tenant_id, ss.project_id, ss.parent_id, ss.code, ss.name, ss.name_uz,
		       ss.description, ss.total_labor_hours, ss.total_labor_cost,
		       ss.total_material_cost, ss.total_equipment_cost, ss.total_overhead_cost, ss.total_cost,
		       ss.sort_order, ss.status, ss.created_date, ss.updated_date,
		       COALESCE((SELECT COUNT(*) FROM smeta_items WHERE section_id = ss.id), 0) as items_count
		FROM smeta_sections ss
		WHERE ss.project_id = $1 AND ss.tenant_id = $2
		ORDER BY ss.sort_order, ss.code
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query smeta sections", "error", err)
		response.InternalError(c, "Failed to query sections")
		return
	}
	defer rows.Close()

	sections := []entity.SmetaSection{}
	for rows.Next() {
		var s entity.SmetaSection
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.ProjectID, &s.ParentID, &s.Code, &s.Name, &s.NameUz,
			&s.Description, &s.TotalLaborHours, &s.TotalLaborCost,
			&s.TotalMaterialCost, &s.TotalEquipmentCost, &s.TotalOverheadCost, &s.TotalCost,
			&s.SortOrder, &s.Status, &s.CreatedDate, &s.UpdatedDate,
			&s.ItemsCount,
		); err != nil {
			h.log.Error("Failed to scan smeta section", "error", err)
			continue
		}
		sections = append(sections, s)
	}

	response.Success(c, sections)
}

// CreateSmetaSection creates a new smeta section
// CreateSmetaSection godoc
// @Summary Create smeta section
// @Description Create a new smeta section for a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param section body entity.CreateSmetaSectionInput true "Section data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/smeta/sections [post]
func (h *Handler) CreateSmetaSection(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req entity.CreateSmetaSectionInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	query := `
		INSERT INTO smeta_sections (
			tenant_id, project_id, parent_id, code, name, name_uz, description,
			sort_order, status, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'draft', NOW(), NOW())
		RETURNING id, created_date
	`

	var sectionID int64
	var createdDate time.Time
	err = h.db.QueryRow(query,
		tenantID, projectID, nullInt64(req.ParentID), req.Code, req.Name,
		nullString(req.NameUz), nullString(req.Description), req.SortOrder,
	).Scan(&sectionID, &createdDate)
	if err != nil {
		h.log.Error("Failed to create smeta section", "error", err)
		response.InternalError(c, "Failed to create section")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":           sectionID,
		"code":         req.Code,
		"name":         req.Name,
		"created_date": createdDate,
	})
}

// UpdateSmetaSection updates a smeta section
// UpdateSmetaSection godoc
// @Summary Update smeta section
// @Description Update an existing smeta section
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Section ID"
// @Param section body entity.UpdateSmetaSectionInput true "Section data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/smeta/sections/{id} [put]
func (h *Handler) UpdateSmetaSection(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid section ID")
		return
	}

	var req entity.UpdateSmetaSectionInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *req.Name)
	}
	if req.NameUz != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name_uz = $%d", argCount))
		args = append(args, *req.NameUz)
	}
	if req.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *req.Description)
	}
	if req.SortOrder != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("sort_order = $%d", argCount))
		args = append(args, *req.SortOrder)
	}
	if req.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *req.Status)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE smeta_sections SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(updates, ", "), argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update smeta section", "error", err)
		response.InternalError(c, "Failed to update section")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Section not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      id,
		"message": "Section updated successfully",
	})
}

// DeleteSmetaSection deletes a smeta section
// DeleteSmetaSection godoc
// @Summary Delete smeta section
// @Description Delete a smeta section
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Section ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/smeta/sections/{id} [delete]
func (h *Handler) DeleteSmetaSection(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid section ID")
		return
	}

	query := `DELETE FROM smeta_sections WHERE id = $1 AND tenant_id = $2`
	result, err := h.db.Exec(query, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete smeta section", "error", err)
		response.InternalError(c, "Failed to delete section")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Section not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Section deleted successfully",
	})
}

// =====================================================
// SMETA ITEM HANDLERS
// =====================================================

// ListSmetaItems returns smeta items for a section
// ListSmetaItems godoc
// @Summary List smeta items
// @Description Get a list of smeta items for a specific section
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Section ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/smeta/sections/{id}/items [get]
func (h *Handler) ListSmetaItems(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	sectionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid section ID")
		return
	}

	query := `
		SELECT si.id, si.tenant_id, si.section_id, si.code, si.snip_code, si.name, si.name_uz,
		       si.unit, si.quantity, si.completed_quantity, si.unit_price, si.total_price,
		       si.labor_hours, si.labor_cost, si.material_cost, si.equipment_cost,
		       si.transport_cost, si.overhead_cost,
		       si.status, si.progress_percent, si.notes, si.sort_order,
		       si.created_date, si.updated_date,
		       ss.name as section_name
		FROM smeta_items si
		JOIN smeta_sections ss ON si.section_id = ss.id
		WHERE si.section_id = $1 AND si.tenant_id = $2
		ORDER BY si.sort_order, si.code
	`

	rows, err := h.db.Query(query, sectionID, tenantID)
	if err != nil {
		h.log.Error("Failed to query smeta items", "error", err)
		response.InternalError(c, "Failed to query items")
		return
	}
	defer rows.Close()

	items := []entity.SmetaItem{}
	for rows.Next() {
		var item entity.SmetaItem
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.SectionID, &item.Code, &item.SnipCode, &item.Name, &item.NameUz,
			&item.Unit, &item.Quantity, &item.CompletedQuantity, &item.UnitPrice, &item.TotalPrice,
			&item.LaborHours, &item.LaborCost, &item.MaterialCost, &item.EquipmentCost,
			&item.TransportCost, &item.OverheadCost,
			&item.Status, &item.ProgressPercent, &item.Notes, &item.SortOrder,
			&item.CreatedDate, &item.UpdatedDate,
			&item.SectionName,
		); err != nil {
			h.log.Error("Failed to scan smeta item", "error", err)
			continue
		}
		items = append(items, item)
	}

	response.Success(c, items)
}

// CreateSmetaItem creates a new smeta item
// CreateSmetaItem godoc
// @Summary Create smeta item
// @Description Create a new smeta item for a section
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Section ID"
// @Param item body entity.CreateSmetaItemInput true "Item data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/smeta/sections/{id}/items [post]
func (h *Handler) CreateSmetaItem(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	sectionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid section ID")
		return
	}

	var req entity.CreateSmetaItemInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Calculate total price
	totalPrice := req.Quantity * req.UnitPrice

	query := `
		INSERT INTO smeta_items (
			tenant_id, section_id, code, snip_code, name, name_uz, unit,
			quantity, unit_price, total_price,
			labor_hours, labor_cost, material_cost, equipment_cost, transport_cost, overhead_cost,
			notes, sort_order, status, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, 'pending', NOW(), NOW())
		RETURNING id, created_date
	`

	var itemID int64
	var createdDate time.Time
	err = h.db.QueryRow(query,
		tenantID, sectionID, nullString(req.Code), nullString(req.SnipCode), req.Name, nullString(req.NameUz), req.Unit,
		req.Quantity, nullFloat64(req.UnitPrice), nullFloat64(totalPrice),
		req.LaborHours, req.LaborCost, req.MaterialCost, req.EquipmentCost, req.TransportCost, req.OverheadCost,
		nullString(req.Notes), req.SortOrder,
	).Scan(&itemID, &createdDate)
	if err != nil {
		h.log.Error("Failed to create smeta item", "error", err)
		response.InternalError(c, "Failed to create item")
		return
	}

	// Update section totals
	h.updateSmetaSectionTotals(sectionID)

	response.Success(c, map[string]interface{}{
		"id":           itemID,
		"name":         req.Name,
		"quantity":     req.Quantity,
		"total_price":  totalPrice,
		"created_date": createdDate,
	})
}

// UpdateSmetaItem updates a smeta item
// UpdateSmetaItem godoc
// @Summary Update smeta item
// @Description Update an existing smeta item
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Item ID"
// @Param item body entity.UpdateSmetaItemInput true "Item data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/smeta/items/{id} [put]
func (h *Handler) UpdateSmetaItem(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid item ID")
		return
	}

	var req entity.UpdateSmetaItemInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Get current section ID for updating totals
	var sectionID int64
	h.db.QueryRow("SELECT section_id FROM smeta_items WHERE id = $1", id).Scan(&sectionID)

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *req.Name)
	}
	if req.Quantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("quantity = $%d", argCount))
		args = append(args, *req.Quantity)
	}
	if req.CompletedQuantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("completed_quantity = $%d", argCount))
		args = append(args, *req.CompletedQuantity)
	}
	if req.UnitPrice != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("unit_price = $%d", argCount))
		args = append(args, *req.UnitPrice)
	}
	if req.LaborCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("labor_cost = $%d", argCount))
		args = append(args, *req.LaborCost)
	}
	if req.MaterialCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("material_cost = $%d", argCount))
		args = append(args, *req.MaterialCost)
	}
	if req.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *req.Status)
	}
	if req.ProgressPercent != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("progress_percent = $%d", argCount))
		args = append(args, *req.ProgressPercent)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE smeta_items SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(updates, ", "), argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update smeta item", "error", err)
		response.InternalError(c, "Failed to update item")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Item not found")
		return
	}

	// Update section totals
	if sectionID > 0 {
		h.updateSmetaSectionTotals(sectionID)
	}

	response.Success(c, map[string]interface{}{
		"id":      id,
		"message": "Item updated successfully",
	})
}

// DeleteSmetaItem deletes a smeta item
// DeleteSmetaItem godoc
// @Summary Delete smeta item
// @Description Delete a smeta item
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Item ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/smeta/items/{id} [delete]
func (h *Handler) DeleteSmetaItem(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid item ID")
		return
	}

	// Get section ID before deleting
	var sectionID int64
	h.db.QueryRow("SELECT section_id FROM smeta_items WHERE id = $1", id).Scan(&sectionID)

	query := `DELETE FROM smeta_items WHERE id = $1 AND tenant_id = $2`
	result, err := h.db.Exec(query, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete smeta item", "error", err)
		response.InternalError(c, "Failed to delete item")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Item not found")
		return
	}

	// Update section totals
	if sectionID > 0 {
		h.updateSmetaSectionTotals(sectionID)
	}

	response.Success(c, map[string]interface{}{
		"message": "Item deleted successfully",
	})
}

// updateSmetaSectionTotals recalculates section totals from items
func (h *Handler) updateSmetaSectionTotals(sectionID int64) {
	query := `
		UPDATE smeta_sections SET
			total_labor_hours = COALESCE((SELECT SUM(labor_hours) FROM smeta_items WHERE section_id = $1), 0),
			total_labor_cost = COALESCE((SELECT SUM(labor_cost) FROM smeta_items WHERE section_id = $1), 0),
			total_material_cost = COALESCE((SELECT SUM(material_cost) FROM smeta_items WHERE section_id = $1), 0),
			total_equipment_cost = COALESCE((SELECT SUM(equipment_cost) FROM smeta_items WHERE section_id = $1), 0),
			total_overhead_cost = COALESCE((SELECT SUM(overhead_cost) FROM smeta_items WHERE section_id = $1), 0),
			total_cost = COALESCE((SELECT SUM(COALESCE(total_price, 0)) FROM smeta_items WHERE section_id = $1), 0),
			updated_date = NOW()
		WHERE id = $1
	`
	h.db.Exec(query, sectionID)
}


// Helper functions for extracting values from sql.Null* types
func nullStringValue(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

func nullFloat64Value(nf sql.NullFloat64) interface{} {
	if nf.Valid {
		return nf.Float64
	}
	return nil
}

func nullInt64Value(ni sql.NullInt64) interface{} {
	if ni.Valid {
		return ni.Int64
	}
	return nil
}

func nullTimeValue(nt sql.NullTime) interface{} {
	if nt.Valid {
		return nt.Time
	}
	return nil
}

func nullUUIDValue(nu uuid.NullUUID) interface{} {
	if nu.Valid {
		return nu.UUID
	}
	return nil
}

// Helper functions for creating sql.Null* types
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt64(i int64) sql.NullInt64 {
	if i == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: i, Valid: true}
}

func nullInt32(i int32) sql.NullInt32 {
	if i == 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: i, Valid: true}
}

func nullFloat64(f float64) sql.NullFloat64 {
	if f == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}

func nullUUID(s string) uuid.NullUUID {
	if s == "" {
		return uuid.NullUUID{}
	}
	parsed, err := uuid.Parse(s)
	if err != nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: parsed, Valid: true}
}

// =====================================================
// PROJECT VENDORS HANDLERS
// =====================================================

// ListProjectVendors returns vendors for a project
// ListProjectVendors godoc
// @Summary List project vendors
// @Description Get a list of vendors for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/vendors [get]
func (h *Handler) ListProjectVendors(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT pv.id, pv.tenant_id, pv.project_id, pv.vendor_id,
		       pv.contract_number, pv.contract_date, pv.contract_amount, COALESCE(pv.currency, 'UZS'),
		       pv.vendor_type, pv.work_scope, pv.smeta_sections,
		       pv.contact_person, pv.contact_phone, pv.contact_email,
		       pv.status, pv.total_ordered, pv.total_received, pv.total_invoiced, pv.total_paid, pv.balance_due,
		       pv.start_date, pv.end_date, pv.notes,
		       pv.created_date, pv.updated_date,
		       COALESCE(o.name, '') as vendor_name
		FROM construction_project_vendors pv
		LEFT JOIN organizations o ON o.id = pv.vendor_id
		WHERE pv.project_id = $1 AND pv.tenant_id = $2
		ORDER BY pv.created_date DESC
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query project vendors", "error", err)
		response.InternalError(c, "Failed to query vendors")
		return
	}
	defer rows.Close()

	vendors := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var tenantIDVal, vendorID uuid.UUID
		var contractNumber, workScope, contactPerson, contactPhone, contactEmail, status, notes, vendorName sql.NullString
		var vendorType, currency string
		var contractDate, startDate, endDate sql.NullTime
		var contractAmount, totalOrdered, totalReceived, totalInvoiced, totalPaid, balanceDue sql.NullFloat64
		var smetaSections []byte
		var createdDate, updatedDate time.Time

		if err := rows.Scan(
			&id, &tenantIDVal, &projectIDVal, &vendorID,
			&contractNumber, &contractDate, &contractAmount, &currency,
			&vendorType, &workScope, &smetaSections,
			&contactPerson, &contactPhone, &contactEmail,
			&status, &totalOrdered, &totalReceived, &totalInvoiced, &totalPaid, &balanceDue,
			&startDate, &endDate, &notes,
			&createdDate, &updatedDate,
			&vendorName,
		); err != nil {
			h.log.Error("Failed to scan vendor", "error", err)
			continue
		}

		vendors = append(vendors, map[string]interface{}{
			"id":              id,
			"tenant_id":       tenantIDVal,
			"project_id":      projectIDVal,
			"vendor_id":       vendorID,
			"contract_number": nullStringValue(contractNumber),
			"contract_date":   nullTimeValue(contractDate),
			"contract_amount": nullFloat64Value(contractAmount),
			"currency":        currency,
			"vendor_type":     vendorType,
			"work_scope":      nullStringValue(workScope),
			"contact_person":  nullStringValue(contactPerson),
			"contact_phone":   nullStringValue(contactPhone),
			"contact_email":   nullStringValue(contactEmail),
			"status":          nullStringValue(status),
			"total_ordered":   nullFloat64Value(totalOrdered),
			"total_received":  nullFloat64Value(totalReceived),
			"total_invoiced":  nullFloat64Value(totalInvoiced),
			"total_paid":      nullFloat64Value(totalPaid),
			"balance_due":     nullFloat64Value(balanceDue),
			"start_date":      nullTimeValue(startDate),
			"end_date":        nullTimeValue(endDate),
			"notes":           nullStringValue(notes),
			"created_date":    createdDate,
			"updated_date":    updatedDate,
			"vendor_name":     vendorName.String,
		})
	}

	response.Success(c, vendors)
}

// CreateProjectVendor adds a vendor to a project
// CreateProjectVendor godoc
// @Summary Create project vendor
// @Description Add a vendor to a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param vendor body entity.CreateProjectVendorInput true "Vendor data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/vendors [post]
func (h *Handler) CreateProjectVendor(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req entity.CreateProjectVendorInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var vendorUUID uuid.UUID

	// Check if vendor_id is provided (existing organization)
	if req.VendorID != "" {
		vendorUUID, err = uuid.Parse(req.VendorID)
		if err != nil {
			response.BadRequest(c, "Invalid vendor ID format")
			return
		}
		// Verify the organization exists
		var exists bool
		err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1 AND tenant_id = $2)", vendorUUID, tenantID).Scan(&exists)
		if err != nil || !exists {
			response.BadRequest(c, "Vendor organization not found")
			return
		}
	} else if req.VendorName != "" {
		// Create a new organization for this vendor
		vendorUUID = uuid.New()
		now := time.Now()

		// Determine organization type based on vendor type
		orgType := "supplier"
		if req.VendorType == "subcontractor" {
			orgType = "subcontractor"
		} else if req.VendorType == "consultant" {
			orgType = "service_provider"
		}

		// Generate a unique code for the organization
		vendorCode := fmt.Sprintf("VND-%s", strings.ToUpper(vendorUUID.String()[:8]))

		// Build contact info JSON
		contactInfo := map[string]string{}
		if req.ContactPerson != "" {
			contactInfo["contact_person"] = req.ContactPerson
		}
		if req.ContactPhone != "" {
			contactInfo["phone"] = req.ContactPhone
		}
		if req.ContactEmail != "" {
			contactInfo["email"] = req.ContactEmail
		}
		contactInfoJSON, _ := json.Marshal(contactInfo)

		createOrgQuery := `
			INSERT INTO organizations (
				id, tenant_id, code, name, type, contact_info,
				country, currency, is_active, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, 'UZ', 'UZS', true, $7, $8)
		`
		_, err = h.db.Exec(createOrgQuery, vendorUUID, tenantID, vendorCode, req.VendorName, orgType, contactInfoJSON, now, now)
		if err != nil {
			h.log.Error("Failed to create vendor organization", "error", err)
			response.InternalError(c, "Failed to create vendor organization")
			return
		}
	} else {
		response.BadRequest(c, "Either vendor_id or vendor_name must be provided")
		return
	}

	var contractDate interface{}
	if req.ContractDate != "" {
		t, _ := time.Parse("2006-01-02", req.ContractDate)
		contractDate = t
	}

	var startDate, endDate interface{}
	if req.StartDate != "" {
		t, _ := time.Parse("2006-01-02", req.StartDate)
		startDate = t
	}
	if req.EndDate != "" {
		t, _ := time.Parse("2006-01-02", req.EndDate)
		endDate = t
	}

	query := `
		INSERT INTO construction_project_vendors (
			tenant_id, project_id, vendor_id,
			contract_number, contract_date, contract_amount, currency,
			vendor_type, work_scope, contact_person, contact_phone, contact_email,
			start_date, end_date, notes, status, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, COALESCE(NULLIF($7, ''), 'UZS'), $8, $9, $10, $11, $12, $13, $14, $15, 'active', NOW(), NOW())
		RETURNING id
	`

	var vendorRecordID int64
	err = h.db.QueryRow(query,
		tenantID, projectID, vendorUUID,
		nullString(req.ContractNumber), contractDate, nullFloat64(req.ContractAmount), req.Currency,
		req.VendorType, nullString(req.WorkScope), nullString(req.ContactPerson), nullString(req.ContactPhone), nullString(req.ContactEmail),
		startDate, endDate, nullString(req.Notes),
	).Scan(&vendorRecordID)
	if err != nil {
		h.log.Error("Failed to create project vendor", "error", err)
		response.InternalError(c, "Failed to add vendor to project")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":         vendorRecordID,
		"vendor_id":  vendorUUID,
		"message":    "Vendor added to project successfully",
	})
}

// UpdateProjectVendor updates a vendor assignment
// UpdateProjectVendor godoc
// @Summary Update project vendor
// @Description Update a vendor assignment in a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Vendor Record ID"
// @Param vendor body entity.CreateProjectVendorInput true "Vendor data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/vendors/{id} [put]
func (h *Handler) UpdateProjectVendor(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	vendorRecordID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid vendor record ID")
		return
	}

	var req entity.CreateProjectVendorInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Verify the vendor record exists and belongs to this tenant
	var existingProjectID int64
	err = h.db.QueryRow(
		"SELECT project_id FROM construction_project_vendors WHERE id = $1 AND tenant_id = $2",
		vendorRecordID, tenantID,
	).Scan(&existingProjectID)
	if err != nil {
		response.NotFound(c, "Vendor record not found")
		return
	}

	var contractDate interface{}
	if req.ContractDate != "" {
		t, _ := time.Parse("2006-01-02", req.ContractDate)
		contractDate = t
	}

	var startDate, endDate interface{}
	if req.StartDate != "" {
		t, _ := time.Parse("2006-01-02", req.StartDate)
		startDate = t
	}
	if req.EndDate != "" {
		t, _ := time.Parse("2006-01-02", req.EndDate)
		endDate = t
	}

	query := `
		UPDATE construction_project_vendors SET
			contract_number = COALESCE($1, contract_number),
			contract_date = COALESCE($2, contract_date),
			contract_amount = COALESCE($3, contract_amount),
			currency = COALESCE(NULLIF($4, ''), currency),
			vendor_type = COALESCE(NULLIF($5, ''), vendor_type),
			work_scope = COALESCE($6, work_scope),
			contact_person = COALESCE($7, contact_person),
			contact_phone = COALESCE($8, contact_phone),
			contact_email = COALESCE($9, contact_email),
			start_date = $10,
			end_date = $11,
			notes = COALESCE($12, notes),
			updated_date = NOW()
		WHERE id = $13 AND tenant_id = $14
	`

	_, err = h.db.Exec(query,
		nullString(req.ContractNumber), contractDate, nullFloat64(req.ContractAmount), req.Currency,
		req.VendorType, nullString(req.WorkScope), nullString(req.ContactPerson),
		nullString(req.ContactPhone), nullString(req.ContactEmail),
		startDate, endDate, nullString(req.Notes),
		vendorRecordID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update project vendor", "error", err)
		response.InternalError(c, "Failed to update vendor")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      vendorRecordID,
		"message": "Vendor updated successfully",
	})
}

// DeleteProjectVendor removes a vendor from a project
// DeleteProjectVendor godoc
// @Summary Delete project vendor
// @Description Remove a vendor from a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Vendor Record ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/vendors/{id} [delete]
func (h *Handler) DeleteProjectVendor(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	vendorRecordID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid vendor record ID")
		return
	}

	// Verify the vendor record exists and belongs to this tenant
	var exists bool
	err = h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM construction_project_vendors WHERE id = $1 AND tenant_id = $2)",
		vendorRecordID, tenantID,
	).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Vendor record not found")
		return
	}

	_, err = h.db.Exec("DELETE FROM construction_project_vendors WHERE id = $1 AND tenant_id = $2", vendorRecordID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete project vendor", "error", err)
		response.InternalError(c, "Failed to remove vendor from project")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Vendor removed from project successfully",
	})
}

// =====================================================
// PHOTO REPORTS HANDLERS
// =====================================================

// ListPhotoReports returns photo reports for a project
// ListPhotoReports godoc
// @Summary List photo reports
// @Description Get a list of photo reports for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/photo-reports [get]
func (h *Handler) ListPhotoReports(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT pr.id, pr.tenant_id, pr.project_id, pr.smeta_item_id, pr.section_id,
		       pr.report_date, pr.report_type, pr.title, pr.description, pr.location_description,
		       pr.gps_latitude, pr.gps_longitude, pr.weather, pr.temperature,
		       pr.photos, pr.reported_by, pr.reviewed_by, pr.review_date, pr.review_status, pr.review_notes,
		       pr.created_date, pr.updated_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as reporter_name,
		       COALESCE(r.first_name || ' ' || r.last_name, '') as reviewer_name
		FROM construction_photo_reports pr
		LEFT JOIN employees e ON e.id = pr.reported_by
		LEFT JOIN employees r ON r.id = pr.reviewed_by
		WHERE pr.project_id = $1 AND pr.tenant_id = $2
		ORDER BY pr.report_date DESC, pr.created_date DESC
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query photo reports", "error", err)
		response.InternalError(c, "Failed to query photo reports")
		return
	}
	defer rows.Close()

	reports := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var tenantIDVal uuid.UUID
		var smetaItemID, sectionID sql.NullInt64
		var reportDate time.Time
		var reportType, title, description, locationDescription, weather, reviewStatus, reviewNotes sql.NullString
		var gpsLat, gpsLong, temperature sql.NullFloat64
		var photosBytes []byte
		var reportedBy, reviewedBy uuid.NullUUID
		var reviewDate sql.NullTime
		var createdDate, updatedDate time.Time
		var reporterName, reviewerName string

		if err := rows.Scan(
			&id, &tenantIDVal, &projectIDVal, &smetaItemID, &sectionID,
			&reportDate, &reportType, &title, &description, &locationDescription,
			&gpsLat, &gpsLong, &weather, &temperature,
			&photosBytes, &reportedBy, &reviewedBy, &reviewDate, &reviewStatus, &reviewNotes,
			&createdDate, &updatedDate,
			&reporterName, &reviewerName,
		); err != nil {
			h.log.Error("Failed to scan photo report", "error", err)
			continue
		}

		// Parse photos JSON bytes into proper array
		var photos []map[string]interface{}
		if len(photosBytes) > 0 {
			json.Unmarshal(photosBytes, &photos)
		}
		if photos == nil {
			photos = []map[string]interface{}{}
		}

		reports = append(reports, map[string]interface{}{
			"id":                   id,
			"tenant_id":            tenantIDVal,
			"project_id":           projectIDVal,
			"smeta_item_id":        nullInt64Value(smetaItemID),
			"section_id":           nullInt64Value(sectionID),
			"report_date":          reportDate,
			"report_type":          nullStringValue(reportType),
			"title":                nullStringValue(title),
			"description":          nullStringValue(description),
			"location_description": nullStringValue(locationDescription),
			"gps_latitude":         nullFloat64Value(gpsLat),
			"gps_longitude":        nullFloat64Value(gpsLong),
			"weather":              nullStringValue(weather),
			"temperature":          nullFloat64Value(temperature),
			"photos":               photos,
			"reported_by":          nullUUIDValue(reportedBy),
			"reviewed_by":          nullUUIDValue(reviewedBy),
			"review_date":          nullTimeValue(reviewDate),
			"review_status":        nullStringValue(reviewStatus),
			"review_notes":         nullStringValue(reviewNotes),
			"created_date":         createdDate,
			"updated_date":         updatedDate,
			"reporter_name":        reporterName,
			"reviewer_name":        reviewerName,
		})
	}

	response.Success(c, reports)
}

// CreatePhotoReport creates a new photo report
// CreatePhotoReport godoc
// @Summary Create photo report
// @Description Create a new photo report for a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param report body entity.CreatePhotoReportInput true "Photo report data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/photo-reports [post]
func (h *Handler) CreatePhotoReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req entity.CreatePhotoReportInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	reportDate, _ := time.Parse("2006-01-02", req.ReportDate)

	// Convert photos to JSON
	photosJSON := "[]"
	if len(req.Photos) > 0 {
		photosBytes, err := json.Marshal(req.Photos)
		if err == nil {
			photosJSON = string(photosBytes)
		}
	}

	query := `
		INSERT INTO construction_photo_reports (
			tenant_id, project_id, smeta_item_id, section_id,
			report_date, report_type, title, description, location_description,
			gps_latitude, gps_longitude, weather, temperature,
			photos, reported_by, review_status, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'pending', NOW(), NOW())
		RETURNING id
	`

	var reportID int64
	err = h.db.QueryRow(query,
		tenantID, projectID, nullInt64(req.SmetaItemID), nullInt64(req.SectionID),
		reportDate, nullString(req.ReportType), nullString(req.Title), nullString(req.Description), nullString(req.LocationDescription),
		nullFloat64(req.GpsLatitude), nullFloat64(req.GpsLongitude), nullString(req.Weather), nullFloat64(req.Temperature),
		photosJSON, nil, // photos, reported_by
	).Scan(&reportID)
	if err != nil {
		h.log.Error("Failed to create photo report", "error", err)
		response.InternalError(c, "Failed to create photo report")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":      reportID,
		"message": "Photo report created successfully",
	})
}

// GetPhotoReport returns a single photo report
// GetPhotoReport godoc
// @Summary Get photo report
// @Description Get detailed information about a specific photo report
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Report ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/photo-reports/{id} [get]
func (h *Handler) GetPhotoReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid report ID")
		return
	}

	query := `
		SELECT pr.id, pr.tenant_id, pr.project_id, pr.smeta_item_id, pr.section_id,
		       pr.report_date, pr.report_type, pr.title, pr.description, pr.location_description,
		       pr.gps_latitude, pr.gps_longitude, pr.weather, pr.temperature,
		       pr.photos, pr.reported_by, pr.review_status, pr.reviewed_by, pr.review_date, pr.review_notes,
		       pr.created_date, pr.updated_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as reporter_name
		FROM construction_photo_reports pr
		LEFT JOIN employees e ON e.id = pr.reported_by
		WHERE pr.id = $1 AND pr.tenant_id = $2
	`

	var report entity.ConstructionPhotoReport
	var photosJSON sql.NullString
	err = h.db.QueryRow(query, reportID, tenantID).Scan(
		&report.ID, &report.TenantID, &report.ProjectID, &report.SmetaItemID, &report.SectionID,
		&report.ReportDate, &report.ReportType, &report.Title, &report.Description, &report.LocationDescription,
		&report.GpsLatitude, &report.GpsLongitude, &report.Weather, &report.Temperature,
		&photosJSON, &report.ReportedBy, &report.ReviewStatus, &report.ReviewedBy, &report.ReviewDate, &report.ReviewNotes,
		&report.CreatedDate, &report.UpdatedDate,
		&report.ReporterName,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Photo report not found")
			return
		}
		h.log.Error("Failed to get photo report", "error", err)
		response.InternalError(c, "Failed to get photo report")
		return
	}

	// Parse photos JSON
	if photosJSON.Valid && photosJSON.String != "" {
		json.Unmarshal([]byte(photosJSON.String), &report.Photos)
	}

	response.Success(c, report)
}

// UpdatePhotoReport updates a photo report
// UpdatePhotoReport godoc
// @Summary Update photo report
// @Description Update an existing photo report
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Report ID"
// @Param report body entity.CreatePhotoReportInput true "Photo report data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/photo-reports/{id} [put]
func (h *Handler) UpdatePhotoReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid report ID")
		return
	}

	var req entity.CreatePhotoReportInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	reportDate, _ := time.Parse("2006-01-02", req.ReportDate)

	// Convert photos to JSON
	photosJSON := "[]"
	if len(req.Photos) > 0 {
		photosBytes, err := json.Marshal(req.Photos)
		if err == nil {
			photosJSON = string(photosBytes)
		}
	}

	query := `
		UPDATE construction_photo_reports SET
			report_date = $1, report_type = $2, title = $3, description = $4,
			location_description = $5, weather = $6, temperature = $7, photos = $8,
			updated_date = NOW()
		WHERE id = $9 AND tenant_id = $10
	`

	result, err := h.db.Exec(query,
		reportDate, nullString(req.ReportType), nullString(req.Title), nullString(req.Description),
		nullString(req.LocationDescription), nullString(req.Weather), nullFloat64(req.Temperature), photosJSON,
		reportID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update photo report", "error", err)
		response.InternalError(c, "Failed to update photo report")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Photo report not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      reportID,
		"message": "Photo report updated successfully",
	})
}

// DeletePhotoReport deletes a photo report
// DeletePhotoReport godoc
// @Summary Delete photo report
// @Description Delete a photo report
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Report ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/photo-reports/{id} [delete]
func (h *Handler) DeletePhotoReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid report ID")
		return
	}

	query := `DELETE FROM construction_photo_reports WHERE id = $1 AND tenant_id = $2`
	result, err := h.db.Exec(query, reportID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete photo report", "error", err)
		response.InternalError(c, "Failed to delete photo report")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Photo report not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Photo report deleted successfully",
	})
}

// =====================================================
// DAILY REPORTS HANDLERS
// =====================================================

// ListDailyReports returns daily reports for a project
// ListDailyReports godoc
// @Summary List daily reports
// @Description Get a list of daily reports for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/daily-reports [get]
func (h *Handler) ListDailyReports(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT dr.id, dr.tenant_id, dr.project_id, dr.report_date,
		       dr.weather_morning, dr.weather_afternoon, dr.temperature_min, dr.temperature_max,
		       dr.work_summary, dr.issues_encountered, dr.safety_notes,
		       dr.workers_count, dr.workers_details, dr.equipment_used, dr.materials_received, dr.visitors,
		       dr.reported_by, dr.verified_by, dr.verification_status,
		       dr.created_date, dr.updated_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as reporter_name,
		       COALESCE(v.first_name || ' ' || v.last_name, '') as verifier_name
		FROM construction_daily_reports dr
		LEFT JOIN employees e ON e.id = dr.reported_by
		LEFT JOIN employees v ON v.id = dr.verified_by
		WHERE dr.project_id = $1 AND dr.tenant_id = $2
		ORDER BY dr.report_date DESC
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query daily reports", "error", err)
		response.InternalError(c, "Failed to query daily reports")
		return
	}
	defer rows.Close()

	reports := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var tenantIDVal uuid.UUID
		var reportDate time.Time
		var weatherMorning, weatherAfternoon, workSummary, issuesEncountered, safetyNotes, verificationStatus sql.NullString
		var tempMin, tempMax sql.NullFloat64
		var workersCount int
		var workersDetails, equipmentUsed, materialsReceived, visitors []byte
		var reportedBy, verifiedBy uuid.NullUUID
		var createdDate, updatedDate time.Time
		var reporterName, verifierName string

		if err := rows.Scan(
			&id, &tenantIDVal, &projectIDVal, &reportDate,
			&weatherMorning, &weatherAfternoon, &tempMin, &tempMax,
			&workSummary, &issuesEncountered, &safetyNotes,
			&workersCount, &workersDetails, &equipmentUsed, &materialsReceived, &visitors,
			&reportedBy, &verifiedBy, &verificationStatus,
			&createdDate, &updatedDate,
			&reporterName, &verifierName,
		); err != nil {
			h.log.Error("Failed to scan daily report", "error", err)
			continue
		}

		reports = append(reports, map[string]interface{}{
			"id":                  id,
			"tenant_id":           tenantIDVal,
			"project_id":          projectIDVal,
			"report_date":         reportDate,
			"weather_morning":     nullStringValue(weatherMorning),
			"weather_afternoon":   nullStringValue(weatherAfternoon),
			"temperature_min":     nullFloat64Value(tempMin),
			"temperature_max":     nullFloat64Value(tempMax),
			"work_summary":        nullStringValue(workSummary),
			"issues_encountered":  nullStringValue(issuesEncountered),
			"safety_notes":        nullStringValue(safetyNotes),
			"workers_count":       workersCount,
			"workers_details":     workersDetails,
			"equipment_used":      equipmentUsed,
			"materials_received":  materialsReceived,
			"visitors":            visitors,
			"reported_by":         nullUUIDValue(reportedBy),
			"verified_by":         nullUUIDValue(verifiedBy),
			"verification_status": nullStringValue(verificationStatus),
			"created_date":        createdDate,
			"updated_date":        updatedDate,
			"reporter_name":       reporterName,
			"verifier_name":       verifierName,
		})
	}

	response.Success(c, reports)
}

// CreateDailyReport creates a new daily report
// CreateDailyReport godoc
// @Summary Create daily report
// @Description Create a new daily report for a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param report body entity.CreateDailyReportInput true "Daily report data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/daily-reports [post]
func (h *Handler) CreateDailyReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req entity.CreateDailyReportInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	reportDate, _ := time.Parse("2006-01-02", req.ReportDate)

	query := `
		INSERT INTO construction_daily_reports (
			tenant_id, project_id, report_date,
			weather_morning, weather_afternoon, temperature_min, temperature_max,
			work_summary, issues_encountered, safety_notes,
			workers_count, workers_details, equipment_used, materials_received, visitors,
			reported_by, verification_status, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 'pending', NOW(), NOW())
		RETURNING id
	`

	// Convert string fields to JSON for JSONB columns (empty array if not provided)
	workersDetailsJSON := "[]"
	if req.WorkersDetails != "" {
		workersDetailsJSON = fmt.Sprintf(`[{"details": %q}]`, req.WorkersDetails)
	}
	equipmentUsedJSON := "[]"
	if req.EquipmentUsed != "" {
		equipmentUsedJSON = fmt.Sprintf(`[{"equipment": %q}]`, req.EquipmentUsed)
	}
	materialsReceivedJSON := "[]"
	if req.MaterialsReceived != "" {
		materialsReceivedJSON = fmt.Sprintf(`[{"materials": %q}]`, req.MaterialsReceived)
	}
	visitorsJSON := "[]"
	if req.Visitors != "" {
		visitorsJSON = fmt.Sprintf(`[{"visitor": %q}]`, req.Visitors)
	}

	var reportID int64
	err = h.db.QueryRow(query,
		tenantID, projectID, reportDate,
		nullString(req.WeatherMorning), nullString(req.WeatherAfternoon), nullFloat64(req.TemperatureMin), nullFloat64(req.TemperatureMax),
		nullString(req.WorkSummary), nullString(req.IssuesEncountered), nullString(req.SafetyNotes),
		req.WorkersCount, workersDetailsJSON, equipmentUsedJSON, materialsReceivedJSON, visitorsJSON,
		nil, // reported_by
	).Scan(&reportID)
	if err != nil {
		h.log.Error("Failed to create daily report", "error", err)
		response.InternalError(c, "Failed to create daily report")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":      reportID,
		"message": "Daily report created successfully",
	})
}

// GetDailyReport returns a single daily report
// GetDailyReport godoc
// @Summary Get daily report
// @Description Get detailed information about a specific daily report
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Report ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/daily-reports/{id} [get]
func (h *Handler) GetDailyReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid report ID")
		return
	}

	query := `
		SELECT id, tenant_id, project_id, report_date,
		       weather_morning, weather_afternoon, temperature_min, temperature_max,
		       work_summary, issues_encountered, safety_notes,
		       workers_count, workers_details, equipment_used, materials_received,
		       verification_status, verified_by, verified_at, verifier_notes,
		       created_date, updated_date
		FROM construction_daily_reports
		WHERE id = $1 AND tenant_id = $2
	`

	var report struct {
		ID                 int64          `json:"id"`
		TenantID           uuid.UUID      `json:"tenant_id"`
		ProjectID          int64          `json:"project_id"`
		ReportDate         time.Time      `json:"report_date"`
		WeatherMorning     sql.NullString `json:"weather_morning"`
		WeatherAfternoon   sql.NullString `json:"weather_afternoon"`
		TemperatureMin     sql.NullFloat64 `json:"temperature_min"`
		TemperatureMax     sql.NullFloat64 `json:"temperature_max"`
		WorkSummary        sql.NullString `json:"work_summary"`
		IssuesEncountered  sql.NullString `json:"issues_encountered"`
		SafetyNotes        sql.NullString `json:"safety_notes"`
		WorkersCount       sql.NullInt64  `json:"workers_count"`
		WorkersDetails     sql.NullString `json:"workers_details"`
		EquipmentUsed      sql.NullString `json:"equipment_used"`
		MaterialsReceived  sql.NullString `json:"materials_received"`
		VerificationStatus sql.NullString `json:"verification_status"`
		VerifiedBy         sql.NullInt64  `json:"verified_by"`
		VerifiedAt         sql.NullTime   `json:"verified_at"`
		VerifierNotes      sql.NullString `json:"verifier_notes"`
		CreatedDate        time.Time      `json:"created_date"`
		UpdatedDate        time.Time      `json:"updated_date"`
	}

	err = h.db.QueryRow(query, reportID, tenantID).Scan(
		&report.ID, &report.TenantID, &report.ProjectID, &report.ReportDate,
		&report.WeatherMorning, &report.WeatherAfternoon, &report.TemperatureMin, &report.TemperatureMax,
		&report.WorkSummary, &report.IssuesEncountered, &report.SafetyNotes,
		&report.WorkersCount, &report.WorkersDetails, &report.EquipmentUsed, &report.MaterialsReceived,
		&report.VerificationStatus, &report.VerifiedBy, &report.VerifiedAt, &report.VerifierNotes,
		&report.CreatedDate, &report.UpdatedDate,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Daily report not found")
			return
		}
		h.log.Error("Failed to get daily report", "error", err)
		response.InternalError(c, "Failed to get daily report")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":                  report.ID,
		"tenant_id":           report.TenantID,
		"project_id":          report.ProjectID,
		"report_date":         report.ReportDate.Format("2006-01-02"),
		"weather_morning":     report.WeatherMorning.String,
		"weather_afternoon":   report.WeatherAfternoon.String,
		"temperature_min":     report.TemperatureMin.Float64,
		"temperature_max":     report.TemperatureMax.Float64,
		"work_summary":        report.WorkSummary.String,
		"issues_encountered":  report.IssuesEncountered.String,
		"safety_notes":        report.SafetyNotes.String,
		"workers_count":       report.WorkersCount.Int64,
		"workers_details":     report.WorkersDetails.String,
		"equipment_used":      report.EquipmentUsed.String,
		"materials_received":  report.MaterialsReceived.String,
		"verification_status": report.VerificationStatus.String,
		"created_date":        report.CreatedDate,
		"updated_date":        report.UpdatedDate,
	})
}

// UpdateDailyReport updates a daily report
// UpdateDailyReport godoc
// @Summary Update daily report
// @Description Update an existing daily report
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Report ID"
// @Param report body object true "Daily report data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/daily-reports/{id} [put]
func (h *Handler) UpdateDailyReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid report ID")
		return
	}

	var req struct {
		ReportDate        string  `json:"report_date"`
		WeatherMorning    string  `json:"weather_morning"`
		WeatherAfternoon  string  `json:"weather_afternoon"`
		TemperatureMin    float64 `json:"temperature_min"`
		TemperatureMax    float64 `json:"temperature_max"`
		WorkSummary       string  `json:"work_summary"`
		IssuesEncountered string  `json:"issues_encountered"`
		SafetyNotes       string  `json:"safety_notes"`
		WorkersCount      int     `json:"workers_count"`
		WorkersDetails    string  `json:"workers_details"`
		EquipmentUsed     string  `json:"equipment_used"`
		MaterialsReceived string  `json:"materials_received"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	query := `
		UPDATE construction_daily_reports
		SET report_date = COALESCE(NULLIF($1, '')::date, report_date),
		    weather_morning = $2,
		    weather_afternoon = $3,
		    temperature_min = $4,
		    temperature_max = $5,
		    work_summary = $6,
		    issues_encountered = $7,
		    safety_notes = $8,
		    workers_count = $9,
		    workers_details = COALESCE(NULLIF($10, ''), '[]')::jsonb,
		    equipment_used = COALESCE(NULLIF($11, ''), '[]')::jsonb,
		    materials_received = COALESCE(NULLIF($12, ''), '[]')::jsonb,
		    updated_date = NOW()
		WHERE id = $13 AND tenant_id = $14
	`

	// Convert string fields to JSON for JSONB columns
	workersDetailsJSON := "[]"
	if req.WorkersDetails != "" {
		workersDetailsJSON = fmt.Sprintf(`[{"details": %q}]`, req.WorkersDetails)
	}
	equipmentUsedJSON := "[]"
	if req.EquipmentUsed != "" {
		equipmentUsedJSON = fmt.Sprintf(`[{"equipment": %q}]`, req.EquipmentUsed)
	}
	materialsReceivedJSON := "[]"
	if req.MaterialsReceived != "" {
		materialsReceivedJSON = fmt.Sprintf(`[{"materials": %q}]`, req.MaterialsReceived)
	}

	result, err := h.db.Exec(query,
		req.ReportDate, nullString(req.WeatherMorning), nullString(req.WeatherAfternoon),
		req.TemperatureMin, req.TemperatureMax,
		nullString(req.WorkSummary), nullString(req.IssuesEncountered), nullString(req.SafetyNotes),
		req.WorkersCount, workersDetailsJSON, equipmentUsedJSON, materialsReceivedJSON,
		reportID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update daily report", "error", err)
		response.InternalError(c, "Failed to update daily report")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Daily report not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Daily report updated successfully",
	})
}

// DeleteDailyReport deletes a daily report
// DeleteDailyReport godoc
// @Summary Delete daily report
// @Description Delete a daily report
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Report ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/daily-reports/{id} [delete]
func (h *Handler) DeleteDailyReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid report ID")
		return
	}

	query := `DELETE FROM construction_daily_reports WHERE id = $1 AND tenant_id = $2`
	result, err := h.db.Exec(query, reportID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete daily report", "error", err)
		response.InternalError(c, "Failed to delete daily report")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Daily report not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Daily report deleted successfully",
	})
}

// =====================================================
// MATERIAL REQUESTS HANDLERS
// =====================================================

// ListMaterialRequests returns material requests for a project
// ListMaterialRequests godoc
// @Summary List material requests
// @Description Get a list of material requests for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/material-requests [get]
func (h *Handler) ListMaterialRequests(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT mr.id, mr.tenant_id, mr.project_id,
		       mr.request_number, mr.request_date, mr.required_date,
		       mr.requested_by, mr.items, mr.status,
		       mr.approved_by, mr.approval_date, mr.approval_notes,
		       mr.fulfilled_date, mr.fulfillment_notes, mr.purchase_order_id,
		       mr.notes, mr.created_date, mr.updated_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as requester_name,
		       COALESCE(a.first_name || ' ' || a.last_name, '') as approver_name
		FROM construction_material_requests mr
		LEFT JOIN employees e ON e.id = mr.requested_by
		LEFT JOIN employees a ON a.id = mr.approved_by
		WHERE mr.project_id = $1 AND mr.tenant_id = $2
		ORDER BY mr.request_date DESC
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query material requests", "error", err)
		response.InternalError(c, "Failed to query material requests")
		return
	}
	defer rows.Close()

	requests := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var tenantIDVal uuid.UUID
		var requestNumber, status, approvalNotes, fulfillmentNotes, notes sql.NullString
		var requestDate time.Time
		var requiredDate, fulfilledDate sql.NullTime
		var requestedBy, approvedBy uuid.NullUUID
		var purchaseOrderID uuid.NullUUID
		var approvalDate sql.NullTime
		var items []byte
		var createdDate, updatedDate time.Time
		var requesterName, approverName string

		if err := rows.Scan(
			&id, &tenantIDVal, &projectIDVal,
			&requestNumber, &requestDate, &requiredDate,
			&requestedBy, &items, &status,
			&approvedBy, &approvalDate, &approvalNotes,
			&fulfilledDate, &fulfillmentNotes, &purchaseOrderID,
			&notes, &createdDate, &updatedDate,
			&requesterName, &approverName,
		); err != nil {
			h.log.Error("Failed to scan material request", "error", err)
			continue
		}

		requests = append(requests, map[string]interface{}{
			"id":                id,
			"tenant_id":         tenantIDVal,
			"project_id":        projectIDVal,
			"request_number":    nullStringValue(requestNumber),
			"request_date":      requestDate,
			"required_date":     nullTimeValue(requiredDate),
			"requested_by":      nullUUIDValue(requestedBy),
			"items":             items,
			"status":            nullStringValue(status),
			"approved_by":       nullUUIDValue(approvedBy),
			"approval_date":     nullTimeValue(approvalDate),
			"approval_notes":    nullStringValue(approvalNotes),
			"fulfilled_date":    nullTimeValue(fulfilledDate),
			"fulfillment_notes": nullStringValue(fulfillmentNotes),
			"purchase_order_id": nullUUIDValue(purchaseOrderID),
			"notes":             nullStringValue(notes),
			"created_date":      createdDate,
			"updated_date":      updatedDate,
			"requester_name":    requesterName,
			"approver_name":     approverName,
		})
	}

	response.Success(c, requests)
}

// CreateMaterialRequest creates a new material request
// CreateMaterialRequest godoc
// @Summary Create material request
// @Description Create a new material request for a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param request body object true "Material request data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/material-requests [post]
func (h *Handler) CreateMaterialRequest(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req struct {
		RequestNumber string `json:"request_number" binding:"required"`
		RequestDate   string `json:"request_date" binding:"required"`
		RequiredDate  string `json:"required_date"`
		Items         string `json:"items"`
		Notes         string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	requestDate, _ := time.Parse("2006-01-02", req.RequestDate)
	var requiredDate interface{}
	if req.RequiredDate != "" {
		t, _ := time.Parse("2006-01-02", req.RequiredDate)
		requiredDate = t
	}

	query := `
		INSERT INTO construction_material_requests (
			tenant_id, project_id, request_number, request_date, required_date,
			items, notes, status, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, COALESCE(NULLIF($6, ''), '[]')::jsonb, $7, 'draft', NOW(), NOW())
		RETURNING id
	`

	var requestID int64
	err = h.db.QueryRow(query,
		tenantID, projectID, req.RequestNumber, requestDate, requiredDate,
		req.Items, nullString(req.Notes),
	).Scan(&requestID)
	if err != nil {
		h.log.Error("Failed to create material request", "error", err)
		response.InternalError(c, "Failed to create material request")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":      requestID,
		"message": "Material request created successfully",
	})
}

// UpdateMaterialRequest updates a material request
// UpdateMaterialRequest godoc
// @Summary Update material request
// @Description Update an existing material request
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Request ID"
// @Param request body object true "Material request data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/material-requests/{id} [put]
func (h *Handler) UpdateMaterialRequest(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	requestID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}

	var req struct {
		RequestNumber string `json:"request_number"`
		RequestDate   string `json:"request_date"`
		RequiredDate  string `json:"required_date"`
		Items         string `json:"items"`
		Notes         string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	query := `
		UPDATE construction_material_requests
		SET request_number = COALESCE(NULLIF($1, ''), request_number),
		    request_date = COALESCE(NULLIF($2, '')::date, request_date),
		    required_date = NULLIF($3, '')::date,
		    notes = $4,
		    updated_date = NOW()
		WHERE id = $5 AND tenant_id = $6
	`

	result, err := h.db.Exec(query,
		req.RequestNumber, req.RequestDate, req.RequiredDate,
		nullString(req.Notes), requestID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update material request", "error", err)
		response.InternalError(c, "Failed to update material request")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Material request not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Material request updated successfully",
	})
}

// DeleteMaterialRequest deletes a material request
// DeleteMaterialRequest godoc
// @Summary Delete material request
// @Description Delete a material request
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Request ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/material-requests/{id} [delete]
func (h *Handler) DeleteMaterialRequest(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	requestID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}

	query := `DELETE FROM construction_material_requests WHERE id = $1 AND tenant_id = $2`
	result, err := h.db.Exec(query, requestID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete material request", "error", err)
		response.InternalError(c, "Failed to delete material request")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Material request not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Material request deleted successfully",
	})
}

// =====================================================
// DELIVERIES HANDLERS
// =====================================================

// ListDeliveries returns material deliveries for a project
// ListDeliveries godoc
// @Summary List deliveries
// @Description Get a list of material deliveries for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/deliveries [get]
func (h *Handler) ListDeliveries(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT d.id, d.tenant_id, d.project_id, d.vendor_id,
		       d.delivery_number, d.delivery_date, d.purchase_order_id, d.goods_receipt_id,
		       d.vehicle_number, d.driver_name, d.waybill_number,
		       d.items, d.total_amount,
		       d.received_by, d.received_date,
		       d.quality_status, d.quality_notes, d.quality_checked_by,
		       d.photos, d.status, d.notes,
		       d.created_date, d.updated_date,
		       COALESCE(o.name, '') as vendor_name,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as receiver_name
		FROM construction_material_deliveries d
		LEFT JOIN construction_project_vendors pv ON pv.id = d.vendor_id
		LEFT JOIN organizations o ON o.id = pv.vendor_id
		LEFT JOIN employees e ON e.id = d.received_by
		WHERE d.project_id = $1 AND d.tenant_id = $2
		ORDER BY d.delivery_date DESC
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query deliveries", "error", err)
		response.InternalError(c, "Failed to query deliveries")
		return
	}
	defer rows.Close()

	deliveries := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal, vendorID int64
		var tenantIDVal uuid.UUID
		var deliveryNumber, vehicleNumber, driverName, waybillNumber, qualityStatus, qualityNotes, status, notes sql.NullString
		var deliveryDate time.Time
		var purchaseOrderID, goodsReceiptID, receivedBy, qualityCheckedBy uuid.NullUUID
		var totalAmount sql.NullFloat64
		var receivedDate sql.NullTime
		var items, photos []byte
		var createdDate, updatedDate time.Time
		var vendorName, receiverName string

		if err := rows.Scan(
			&id, &tenantIDVal, &projectIDVal, &vendorID,
			&deliveryNumber, &deliveryDate, &purchaseOrderID, &goodsReceiptID,
			&vehicleNumber, &driverName, &waybillNumber,
			&items, &totalAmount,
			&receivedBy, &receivedDate,
			&qualityStatus, &qualityNotes, &qualityCheckedBy,
			&photos, &status, &notes,
			&createdDate, &updatedDate,
			&vendorName, &receiverName,
		); err != nil {
			h.log.Error("Failed to scan delivery", "error", err)
			continue
		}

		deliveries = append(deliveries, map[string]interface{}{
			"id":                 id,
			"tenant_id":          tenantIDVal,
			"project_id":         projectIDVal,
			"vendor_id":          vendorID,
			"delivery_number":    nullStringValue(deliveryNumber),
			"delivery_date":      deliveryDate,
			"purchase_order_id":  nullUUIDValue(purchaseOrderID),
			"goods_receipt_id":   nullUUIDValue(goodsReceiptID),
			"vehicle_number":     nullStringValue(vehicleNumber),
			"driver_name":        nullStringValue(driverName),
			"waybill_number":     nullStringValue(waybillNumber),
			"items":              items,
			"total_amount":       nullFloat64Value(totalAmount),
			"received_by":        nullUUIDValue(receivedBy),
			"received_date":      nullTimeValue(receivedDate),
			"quality_status":     nullStringValue(qualityStatus),
			"quality_notes":      nullStringValue(qualityNotes),
			"quality_checked_by": nullUUIDValue(qualityCheckedBy),
			"photos":             photos,
			"status":             nullStringValue(status),
			"notes":              nullStringValue(notes),
			"created_date":       createdDate,
			"updated_date":       updatedDate,
			"vendor_name":        vendorName,
			"receiver_name":      receiverName,
		})
	}

	response.Success(c, deliveries)
}

// =====================================================
// SITE WAREHOUSES HANDLERS
// =====================================================

// ListSiteWarehouses returns site warehouses for a project
// ListSiteWarehouses godoc
// @Summary List site warehouses
// @Description Get a list of site warehouses for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/site-warehouses [get]
func (h *Handler) ListSiteWarehouses(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT sw.id, sw.tenant_id, sw.project_id, sw.warehouse_id,
		       sw.name, sw.location_description, sw.gps_coordinates,
		       sw.warehouse_keeper_id, sw.total_area, sw.covered_area,
		       sw.is_active, sw.notes, sw.created_date, sw.updated_date,
		       COALESCE(w.name, '') as warehouse_name,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as keeper_name
		FROM construction_site_warehouses sw
		LEFT JOIN warehouses w ON w.id = sw.warehouse_id
		LEFT JOIN employees e ON e.id = sw.warehouse_keeper_id
		WHERE sw.project_id = $1 AND sw.tenant_id = $2
		ORDER BY sw.name
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query site warehouses", "error", err)
		response.InternalError(c, "Failed to query site warehouses")
		return
	}
	defer rows.Close()

	warehouses := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var tenantIDVal uuid.UUID
		var warehouseID uuid.NullUUID
		var name string
		var locationDescription, notes sql.NullString
		var gpsCoordinates []byte
		var warehouseKeeperID uuid.NullUUID
		var totalArea, coveredArea sql.NullFloat64
		var isActive bool
		var createdDate, updatedDate time.Time
		var warehouseName, keeperName string

		if err := rows.Scan(
			&id, &tenantIDVal, &projectIDVal, &warehouseID,
			&name, &locationDescription, &gpsCoordinates,
			&warehouseKeeperID, &totalArea, &coveredArea,
			&isActive, &notes, &createdDate, &updatedDate,
			&warehouseName, &keeperName,
		); err != nil {
			h.log.Error("Failed to scan site warehouse", "error", err)
			continue
		}

		warehouses = append(warehouses, map[string]interface{}{
			"id":                   id,
			"tenant_id":            tenantIDVal,
			"project_id":           projectIDVal,
			"warehouse_id":         nullUUIDValue(warehouseID),
			"name":                 name,
			"location_description": nullStringValue(locationDescription),
			"gps_coordinates":      gpsCoordinates,
			"warehouse_keeper_id":  nullUUIDValue(warehouseKeeperID),
			"total_area":           nullFloat64Value(totalArea),
			"covered_area":         nullFloat64Value(coveredArea),
			"is_active":            isActive,
			"notes":                nullStringValue(notes),
			"created_date":         createdDate,
			"updated_date":         updatedDate,
			"warehouse_name":       warehouseName,
			"keeper_name":          keeperName,
		})
	}

	response.Success(c, warehouses)
}

// CreateSiteWarehouse creates a new site warehouse
// CreateSiteWarehouse godoc
// @Summary Create site warehouse
// @Description Create a new site warehouse for a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param warehouse body entity.CreateSiteWarehouseInput true "Site warehouse data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/site-warehouses [post]
func (h *Handler) CreateSiteWarehouse(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req entity.CreateSiteWarehouseInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	query := `
		INSERT INTO construction_site_warehouses (
			tenant_id, project_id, name, location_description, gps_coordinates,
			warehouse_keeper_id, total_area, covered_area, notes, is_active, created_date, updated_date
		) VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, ''), '{}')::jsonb, $6, $7, $8, $9, true, NOW(), NOW())
		RETURNING id
	`

	var warehouseID int64
	err = h.db.QueryRow(query,
		tenantID, projectID, req.Name, nullString(req.LocationDescription), req.GpsCoordinates,
		nil, nullFloat64(req.TotalArea), nullFloat64(req.CoveredArea), nullString(req.Notes),
	).Scan(&warehouseID)
	if err != nil {
		h.log.Error("Failed to create site warehouse", "error", err)
		response.InternalError(c, "Failed to create site warehouse")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":      warehouseID,
		"message": "Site warehouse created successfully",
	})
}

// =====================================================
// CONSTRUCTION PROJECT TEAM MEMBER HANDLERS
// =====================================================

// ListConstructionTeamMembers returns team members for a construction project
// ListConstructionTeamMembers godoc
// @Summary List construction team members
// @Description Get a list of team members for a specific construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/team-members [get]
func (h *Handler) ListConstructionTeamMembers(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT pt.id, pt.project_id, pt.employee_id, pt.role, pt.responsibilities,
		       pt.start_date, pt.end_date, pt.is_active, pt.created_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as employee_name,
		       COALESCE(e.job_title, '') as position,
		       COALESCE(e.phone, '') as phone,
		       COALESCE(e.email, '') as email
		FROM construction_project_team pt
		JOIN employees e ON e.id = pt.employee_id
		JOIN construction_projects cp ON cp.id = pt.project_id
		WHERE pt.project_id = $1 AND cp.tenant_id = $2
		ORDER BY pt.created_date DESC
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query project team members", "error", err)
		response.InternalError(c, "Failed to query team members")
		return
	}
	defer rows.Close()

	members := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var employeeID uuid.UUID
		var role, employeeName, position, phone, email string
		var responsibilities sql.NullString
		var startDate, endDate sql.NullTime
		var isActive bool
		var createdDate time.Time

		if err := rows.Scan(
			&id, &projectIDVal, &employeeID, &role, &responsibilities,
			&startDate, &endDate, &isActive, &createdDate,
			&employeeName, &position, &phone, &email,
		); err != nil {
			h.log.Error("Failed to scan team member", "error", err)
			continue
		}

		members = append(members, map[string]interface{}{
			"id":               id,
			"project_id":       projectIDVal,
			"employee_id":      employeeID,
			"role":             role,
			"responsibilities": nullStringValue(responsibilities),
			"start_date":       nullTimeValue(startDate),
			"end_date":         nullTimeValue(endDate),
			"is_active":        isActive,
			"created_date":     createdDate,
			"employee_name":    employeeName,
			"position":         position,
			"phone":            phone,
			"email":            email,
		})
	}

	response.Success(c, members)
}

// CreateConstructionTeamMember adds a team member to a construction project
// CreateConstructionTeamMember godoc
// @Summary Create construction team member
// @Description Add a team member to a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param member body entity.CreateConstructionTeamMemberInput true "Team member data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/team-members [post]
func (h *Handler) CreateConstructionTeamMember(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	// Verify project belongs to tenant
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM construction_projects WHERE id = $1 AND tenant_id = $2)", projectID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Project not found")
		return
	}

	var req entity.CreateConstructionTeamMemberInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Parse employee UUID
	employeeUUID, err := uuid.Parse(req.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Verify employee exists
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)", employeeUUID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Employee not found")
		return
	}

	// Parse dates
	var startDate, endDate sql.NullTime
	if req.StartDate != "" {
		if t, err := time.Parse("2006-01-02", req.StartDate); err == nil {
			startDate = sql.NullTime{Time: t, Valid: true}
		}
	}
	if req.EndDate != "" {
		if t, err := time.Parse("2006-01-02", req.EndDate); err == nil {
			endDate = sql.NullTime{Time: t, Valid: true}
		}
	}

	query := `
		INSERT INTO construction_project_team (
			project_id, employee_id, role, responsibilities, start_date, end_date, is_active, created_date
		) VALUES ($1, $2, $3, $4, $5, $6, true, NOW())
		ON CONFLICT (project_id, employee_id, role) DO UPDATE
		SET responsibilities = EXCLUDED.responsibilities,
		    start_date = EXCLUDED.start_date,
		    end_date = EXCLUDED.end_date,
		    is_active = true
		RETURNING id
	`

	var memberID int64
	err = h.db.QueryRow(query,
		projectID, employeeUUID, req.Role, nullString(req.Responsibilities), startDate, endDate,
	).Scan(&memberID)
	if err != nil {
		h.log.Error("Failed to create team member", "error", err)
		response.InternalError(c, "Failed to add team member")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":      memberID,
		"message": "Team member added successfully",
	})
}

// DeleteConstructionTeamMember removes a team member from a construction project
// DeleteConstructionTeamMember godoc
// @Summary Delete construction team member
// @Description Remove a team member from a construction project
// @Tags Construction
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param memberId path int true "Member ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /construction/projects/{id}/team-members/{memberId} [delete]
func (h *Handler) DeleteConstructionTeamMember(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	memberID, err := strconv.ParseInt(c.Param("memberId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid member ID")
		return
	}

	// Verify project belongs to tenant and delete
	result, err := h.db.Exec(`
		DELETE FROM construction_project_team
		WHERE id = $1 AND project_id = $2
		AND EXISTS (SELECT 1 FROM construction_projects WHERE id = $2 AND tenant_id = $3)
	`, memberID, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete team member", "error", err)
		response.InternalError(c, "Failed to remove team member")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Team member not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"message": "Team member removed successfully",
	})
}
