package handler

import (
	"database/sql"
	"fmt"
	"strconv"
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
		       COALESCE(pm.name, '') as project_manager_name,
		       COALESCE(ce.name, '') as chief_engineer_name,
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
		       COALESCE(pm.name, '') as project_manager_name,
		       COALESCE(ce.name, '') as chief_engineer_name,
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
// SMETA SECTION HANDLERS
// =====================================================

// ListSmetaSections returns smeta sections for a project
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
