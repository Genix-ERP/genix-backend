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
		       cp.status,
		       COALESCE((
		         SELECT CASE
		                  WHEN SUM(w.cost) > 0 THEN ROUND(SUM(w.cost * w.ratio) / SUM(w.cost) * 100, 2)
		                  WHEN COUNT(*) > 0   THEN ROUND(AVG(w.ratio) * 100, 2)
		                  ELSE NULL
		                END
		         FROM (
		           SELECT COALESCE(l.total_amount, 0) AS cost,
		                  LEAST(
		                    CASE WHEN COALESCE(NULLIF(l.imported_quantity,0), NULLIF(l.original_quantity,0), l.quantity) > 0
		                         THEN l.done_quantity::numeric / COALESCE(NULLIF(l.imported_quantity,0), NULLIF(l.original_quantity,0), l.quantity)
		                         ELSE 0 END, 1) AS ratio
		           FROM construction_estimate_line l
		           JOIN construction_estimate e ON e.id = l.estimate_id AND e.tenant_id = l.tenant_id
		           WHERE e.project_id = cp.id AND l.tenant_id = cp.tenant_id
		             AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
		             AND COALESCE(l.resource_type, '') = '' AND COALESCE(l.parent_line_id, 0) = 0
		         ) w
		       ), cp.progress_percent) AS progress_percent,
		       cp.project_manager_id, cp.chief_engineer_id,
		       cp.warehouse_id,
		       cp.crm_project_id,
		       cp.created_by, cp.created_date, cp.updated_date,
		       COALESCE(pm.first_name || ' ' || pm.last_name, '') as project_manager_name,
		       COALESCE(ce.first_name || ' ' || ce.last_name, '') as chief_engineer_name,
		       COALESCE(w.name, '') as warehouse_name,
		       COALESCE((SELECT COUNT(*) FROM smeta_sections WHERE project_id = cp.id), 0) as sections_count,
		       COALESCE((SELECT SUM(total_cost) FROM smeta_sections WHERE project_id = cp.id), 0) as total_smeta,
		       COALESCE((SELECT COUNT(*) FROM project_files WHERE project_id = cp.id), 0) as files_count
		FROM construction_projects cp
		LEFT JOIN employees pm ON pm.id = cp.project_manager_id
		LEFT JOIN employees ce ON ce.id = cp.chief_engineer_id
		LEFT JOIN warehouses w ON w.id = cp.warehouse_id AND w.tenant_id = cp.tenant_id
		WHERE cp.tenant_id = $1 AND cp.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM construction_projects WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	activeOrgID, hasOrg := middleware.GetOrganizationID(c)
	if hasOrg && activeOrgID != uuid.Nil {
		argCount++
		// Show projects the active company OWNS, plus projects where it is a
		// linked subcontractor (Company B working on Company A's project).
		baseQuery += fmt.Sprintf(` AND (cp.organization_id IS NULL OR cp.organization_id = $%d OR EXISTS (
			SELECT 1 FROM construction_subcontract sc
			WHERE sc.tenant_id = cp.tenant_id AND sc.project_id = cp.id
			  AND sc.subcontractor_organization_id = $%d))`, argCount, argCount)
		countQuery += fmt.Sprintf(` AND (organization_id IS NULL OR organization_id = $%d OR EXISTS (
			SELECT 1 FROM construction_subcontract sc
			WHERE sc.tenant_id = construction_projects.tenant_id AND sc.project_id = construction_projects.id
			  AND sc.subcontractor_organization_id = $%d))`, argCount, argCount)
		args = append(args, activeOrgID)
	}

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
			&p.WarehouseID,
			&p.CRMProjectID,
			&p.CreatedBy, &p.CreatedDate, &p.UpdatedDate,
			&p.ProjectManagerName, &p.ChiefEngineerName,
			&p.WarehouseName,
			&p.SectionsCount, &p.TotalSmeta, &p.FilesCount,
		); err != nil {
			h.log.Error("Failed to scan construction project", "error", err)
			continue
		}
		// owner vs subcontractor (drives the "Subpudratchi" badge + scoping).
		p.ViewerRole = "owner"
		// Only a project explicitly owned by a DIFFERENT org is shown as
		// subcontracted. Legacy/unassigned projects (NULL org) stay "owner"
		// so they don't get a stray "Subpudratchi" badge.
		if hasOrg && activeOrgID != uuid.Nil && p.OrganizationID.Valid && p.OrganizationID.UUID != activeOrgID {
			p.ViewerRole = "subcontractor"
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
		       cp.status,
		       COALESCE((
		         SELECT CASE
		                  WHEN SUM(w.cost) > 0 THEN ROUND(SUM(w.cost * w.ratio) / SUM(w.cost) * 100, 2)
		                  WHEN COUNT(*) > 0   THEN ROUND(AVG(w.ratio) * 100, 2)
		                  ELSE NULL
		                END
		         FROM (
		           SELECT COALESCE(l.total_amount, 0) AS cost,
		                  LEAST(
		                    CASE WHEN COALESCE(NULLIF(l.imported_quantity,0), NULLIF(l.original_quantity,0), l.quantity) > 0
		                         THEN l.done_quantity::numeric / COALESCE(NULLIF(l.imported_quantity,0), NULLIF(l.original_quantity,0), l.quantity)
		                         ELSE 0 END, 1) AS ratio
		           FROM construction_estimate_line l
		           JOIN construction_estimate e ON e.id = l.estimate_id AND e.tenant_id = l.tenant_id
		           WHERE e.project_id = cp.id AND l.tenant_id = cp.tenant_id
		             AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
		             AND COALESCE(l.resource_type, '') = '' AND COALESCE(l.parent_line_id, 0) = 0
		         ) w
		       ), cp.progress_percent) AS progress_percent,
		       cp.project_manager_id, cp.chief_engineer_id,
		       cp.warehouse_id,
		       cp.crm_project_id,
		       cp.created_by, cp.created_date, cp.updated_date,
		       COALESCE(pm.first_name || ' ' || pm.last_name, '') as project_manager_name,
		       COALESCE(ce.first_name || ' ' || ce.last_name, '') as chief_engineer_name,
		       COALESCE(w.name, '') as warehouse_name,
		       COALESCE((SELECT COUNT(*) FROM smeta_sections WHERE project_id = cp.id), 0) as sections_count,
		       COALESCE((SELECT SUM(total_cost) FROM smeta_sections WHERE project_id = cp.id), 0) as total_smeta,
		       COALESCE((SELECT COUNT(*) FROM project_files WHERE project_id = cp.id), 0) as files_count
		FROM construction_projects cp
		LEFT JOIN employees pm ON pm.id = cp.project_manager_id
		LEFT JOIN employees ce ON ce.id = cp.chief_engineer_id
		LEFT JOIN warehouses w ON w.id = cp.warehouse_id AND w.tenant_id = cp.tenant_id
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
		&p.WarehouseID,
		&p.CRMProjectID,
		&p.CreatedBy, &p.CreatedDate, &p.UpdatedDate,
		&p.ProjectManagerName, &p.ChiefEngineerName,
		&p.WarehouseName,
		&p.SectionsCount, &p.TotalSmeta, &p.FilesCount,
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

	// Mark whether the active company owns this project or is only a
	// subcontractor on it (drives the badge + scoped working in the UI).
	p.ViewerRole = "owner"
	if aOrg, aOk := middleware.GetOrganizationID(c); aOk && aOrg != uuid.Nil &&
		p.OrganizationID.Valid && p.OrganizationID.UUID != aOrg {
		var isSub bool
		_ = h.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM construction_subcontract
			WHERE tenant_id = $1 AND project_id = $2 AND subcontractor_organization_id = $3)`,
			tenantID, id, aOrg).Scan(&isSub)
		if isSub {
			p.ViewerRole = "subcontractor"
		}
	}

	// Load Forma 2 / Forma 3 client banking & legal identity fields
	var clientAddress, clientBankName, clientBankAccount string
	var clientMFO, clientSTIR, clientOKONH string
	var contractNumber, objectFullName string
	var clientDirectorName, clientChiefAccName string
	_ = h.db.QueryRow(`
		SELECT COALESCE(client_address, ''), COALESCE(client_bank_name, ''), COALESCE(client_bank_account, ''),
		       COALESCE(client_mfo, ''), COALESCE(client_stir, ''), COALESCE(client_okonh, ''),
		       COALESCE(contract_number, ''), COALESCE(object_full_name, ''),
		       COALESCE(client_director_name, ''), COALESCE(client_chief_accountant_name, '')
		FROM construction_projects WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&clientAddress, &clientBankName, &clientBankAccount,
		&clientMFO, &clientSTIR, &clientOKONH,
		&contractNumber, &objectFullName,
		&clientDirectorName, &clientChiefAccName,
	)

	// Build response by marshalling project + new fields into a single map
	projBytes, _ := json.Marshal(p)
	var projMap map[string]interface{}
	_ = json.Unmarshal(projBytes, &projMap)
	if projMap == nil {
		projMap = map[string]interface{}{}
	}
	projMap["client_address"] = clientAddress
	projMap["client_bank_name"] = clientBankName
	projMap["client_bank_account"] = clientBankAccount
	projMap["client_mfo"] = clientMFO
	projMap["client_stir"] = clientSTIR
	projMap["client_okonh"] = clientOKONH
	projMap["contract_number"] = contractNumber
	projMap["object_full_name"] = objectFullName
	projMap["client_director_name"] = clientDirectorName
	projMap["client_chief_accountant_name"] = clientChiefAccName

	// warehouse_name is already populated by the SELECT/MarshalJSON path
	// (LEFT JOIN warehouses w). No extra lookup needed here.

	response.Success(c, projMap)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Auto-generate code if not provided
	if req.Code == "" {
		req.Code = fmt.Sprintf("PRJ-%d", time.Now().UnixMilli())
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
			status, project_manager_id, chief_engineer_id, warehouse_id,
			created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, NOW(), NOW())
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
		nullUUID(req.WarehouseID),
		userID,
	).Scan(&projectID, &createdDate)
	if err != nil {
		h.log.Error("Failed to create construction project", "error", err)
		response.InternalError(c, "Failed to create project")
		return
	}

	// Persist Forma 2 / Forma 3 client banking & legal identity fields
	_, _ = h.db.Exec(`
		UPDATE construction_projects SET
			client_address = $1,
			client_bank_name = $2,
			client_bank_account = $3,
			client_mfo = $4,
			client_stir = $5,
			client_okonh = $6,
			contract_number = $7,
			object_full_name = $8,
			client_director_name = $9,
			client_chief_accountant_name = $10,
			updated_date = NOW()
		WHERE id = $11 AND tenant_id = $12
	`,
		nullString(req.ClientAddress),
		nullString(req.ClientBankName),
		nullString(req.ClientBankAccount),
		nullString(req.ClientMFO),
		nullString(req.ClientSTIR),
		nullString(req.ClientOKONH),
		nullString(req.ContractNumber),
		nullString(req.ObjectFullName),
		nullString(req.ClientDirectorName),
		nullString(req.ClientChiefAccountantName),
		projectID, tenantID,
	)

	// Auto-create analytic account for the project (best-effort)
	go func() {
		analyticID := uuid.New()
		var orgVal interface{}
		if orgID != uuid.Nil {
			orgVal = orgID
		}
		_, insErr := h.db.Exec(`
			INSERT INTO accounts (id, tenant_id, organization_id, code, name, description,
			                      is_active, current_balance, opening_balance, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, 0, 0, NOW(), NOW())
			ON CONFLICT DO NOTHING
		`, analyticID, tenantID, orgVal,
			fmt.Sprintf("CONST-%s", req.Code),
			fmt.Sprintf("Qurilish: %s", req.Name),
			fmt.Sprintf("Analytic account for construction project %s", req.Code),
		)
		if insErr != nil {
			h.log.Error("Failed to create analytic account for project", "error", insErr, "project_id", projectID)
			return
		}
		_, _ = h.db.Exec(`UPDATE construction_projects SET analytic_account_id = $1 WHERE id = $2`, analyticID, projectID)
	}()

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

	organizationID, _ := middleware.GetOrganizationID(c)

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req entity.UpdateConstructionProjectInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Load previous status (needed for completion journal entry)
	var previousStatus string
	_ = h.db.QueryRow(`SELECT COALESCE(status,'draft') FROM construction_projects WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&previousStatus)

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
	// Default warehouse. Pointer-typed so that an absent key leaves the
	// column alone, and an explicit "" clears it (NULL = revert to
	// auto-pick). nullUUID handles both cases — empty string parses to
	// uuid.Nil which it represents as nil.
	if req.WarehouseID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("warehouse_id = $%d", argCount))
		args = append(args, nullUUID(*req.WarehouseID))
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

	// Forma 2 / Forma 3 client banking & legal identity
	if req.ClientAddress != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_address = $%d", argCount))
		args = append(args, nullString(*req.ClientAddress))
	}
	if req.ClientBankName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_bank_name = $%d", argCount))
		args = append(args, nullString(*req.ClientBankName))
	}
	if req.ClientBankAccount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_bank_account = $%d", argCount))
		args = append(args, nullString(*req.ClientBankAccount))
	}
	if req.ClientMFO != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_mfo = $%d", argCount))
		args = append(args, nullString(*req.ClientMFO))
	}
	if req.ClientSTIR != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_stir = $%d", argCount))
		args = append(args, nullString(*req.ClientSTIR))
	}
	if req.ClientOKONH != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_okonh = $%d", argCount))
		args = append(args, nullString(*req.ClientOKONH))
	}
	if req.ContractNumber != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("contract_number = $%d", argCount))
		args = append(args, nullString(*req.ContractNumber))
	}
	if req.ObjectFullName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("object_full_name = $%d", argCount))
		args = append(args, nullString(*req.ObjectFullName))
	}
	if req.ClientDirectorName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_director_name = $%d", argCount))
		args = append(args, nullString(*req.ClientDirectorName))
	}
	if req.ClientChiefAccountantName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("client_chief_accountant_name = $%d", argCount))
		args = append(args, nullString(*req.ClientChiefAccountantName))
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

	// On project completion: create journal entry Dt 0100 (Fixed Assets) / Kt 0810 (Capital Investments)
	if req.Status != nil && *req.Status == "completed" && previousStatus != "completed" {
		go h.createProjectCompletionJournalEntry(tenantID, organizationID, id)
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

	// Cost breakdown from current estimate
	var materialCost, laborCost, equipmentCost float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(el.material_rate * el.quantity), 0),
		       COALESCE(SUM(el.labor_rate * el.quantity), 0),
		       COALESCE(SUM(el.equipment_rate * el.quantity), 0)
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		WHERE e.project_id = $1 AND e.tenant_id = $2 AND e.is_current = true
	`, id, tenantID).Scan(&materialCost, &laborCost, &equipmentCost)

	var subcontractCost, otherCost float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(CASE WHEN cc.name ILIKE '%pudrat%' OR cc.name ILIKE '%subcontract%' THEN el.amount ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN cc.name NOT ILIKE '%pudrat%' AND cc.name NOT ILIKE '%subcontract%' AND cc.name NOT ILIKE '%material%' AND cc.name NOT ILIKE '%ish haqi%' AND cc.name NOT ILIKE '%texnika%' THEN el.amount ELSE 0 END), 0)
		FROM construction_expense_lines el
		LEFT JOIN construction_cost_categories cc ON cc.id = el.cost_category_id
		WHERE el.project_id = $1 AND el.tenant_id = $2 AND el.status = 'approved' AND el.deleted_at IS NULL
	`, id, tenantID).Scan(&subcontractCost, &otherCost)

	dashboard["cost_breakdown"] = map[string]interface{}{
		"material":    materialCost,
		"labor":       laborCost,
		"equipment":   equipmentCost,
		"subcontract": subcontractCost,
		"other":       otherCost,
	}

	// Recent activity
	activityRows, err := h.db.Query(`
		SELECT a.action_type, a.description, a.created_at,
		       COALESCE(u.first_name || ' ' || u.last_name, '') as user_name
		FROM construction_activity_log a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.project_id = $1 AND a.tenant_id = $2
		ORDER BY a.created_at DESC LIMIT 5
	`, id, tenantID)
	recentActivity := []map[string]interface{}{}
	if err == nil {
		defer activityRows.Close()
		for activityRows.Next() {
			var actionType, description, userName string
			var createdAt time.Time
			if err := activityRows.Scan(&actionType, &description, &createdAt, &userName); err == nil {
				recentActivity = append(recentActivity, map[string]interface{}{
					"type": actionType,
					"text": description,
					"user": userName,
					"date": createdAt,
				})
			}
		}
	}
	dashboard["recent_activity"] = recentActivity

	// WBS stats
	var wbsTotal, wbsCompleted int
	h.db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE progress >= 100)
		FROM construction_wbs
		WHERE project_id = $1 AND tenant_id = $2 AND is_active = true
	`, id, tenantID).Scan(&wbsTotal, &wbsCompleted)
	dashboard["wbs_total"] = wbsTotal
	dashboard["wbs_completed"] = wbsCompleted

	// Plan progress (time-based)
	var plannedStart, plannedEnd sql.NullTime
	h.db.QueryRow(`
		SELECT planned_start_date, planned_end_date
		FROM construction_projects WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(&plannedStart, &plannedEnd)

	var progressPlan float64
	if plannedStart.Valid && plannedEnd.Valid {
		totalDays := plannedEnd.Time.Sub(plannedStart.Time).Hours() / 24
		elapsedDays := time.Since(plannedStart.Time).Hours() / 24
		if totalDays > 0 {
			progressPlan = (elapsedDays / totalDays) * 100
			if progressPlan > 100 {
				progressPlan = 100
			}
			if progressPlan < 0 {
				progressPlan = 0
			}
		}
	}
	dashboard["progress_plan"] = progressPlan
	dashboard["behind_schedule"] = progressPlan - progressPercent.Float64

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

	// files_count joins building_files so the UI can render the count badge
	// on the "Fayllar" button without a per-building extra round-trip.
	//
	// Note: we intentionally don't filter building_files by tenant_id inside
	// the subquery. construction_buildings.tenant_id is UUID but
	// building_files.tenant_id is VARCHAR(100) (see migrations 112 / 253),
	// so reusing $2 in both contexts tripped Postgres' parameter type
	// inference and made the whole query fail (returning an empty buildings
	// list, which also left the estimates panel stuck on "Avval bino
	// tanlang"). Tenant scoping is already guaranteed by the outer
	// b.tenant_id = $2 filter plus the FK link f.building_id = b.id.
	query := `
		SELECT b.id, b.tenant_id, b.project_id, b.code, b.name, b.description,
		       b.building_type, b.building_purpose, b.floors_count, b.floors_underground,
		       b.total_area, b.living_area, b.non_living_area,
		       b.apartments_count, b.commercial_units_count, b.parking_spots,
		       b.estimated_cost, b.actual_cost, COALESCE(b.currency, 'UZS'),
		       b.planned_start_date, b.planned_end_date, b.actual_start_date, b.actual_end_date,
		       COALESCE(b.status, 'draft'), b.progress_percent, COALESCE(b.gps_coordinates::text, '{}')::text, b.location_description,
		       COALESCE(b.sort_order, 0), b.created_date, b.updated_date,
		       b.crm_block_id, b.current_crm_stage, b.crm_auto_sync,
		       0 as sections_count,
		       0.0 as total_smeta,
		       COALESCE(f.cnt, 0) AS files_count
		FROM construction_buildings b
		LEFT JOIN (
		    SELECT building_id, COUNT(*) AS cnt
		    FROM building_files
		    GROUP BY building_id
		) f ON f.building_id = b.id
		WHERE b.project_id = $1 AND b.tenant_id = $2
	`

	queryArgs := []interface{}{projectID, tenantID}

	// Block-level scoping for cross-company subcontractors: when the active
	// company is a subcontractor on this project (not the owner) AND its
	// subcontracts assign specific blocks, restrict the list to those blocks.
	// If it has no block assignments at all, it sees every block (fallback).
	if activeOrg, hasOrg := middleware.GetOrganizationID(c); hasOrg && activeOrg != uuid.Nil {
		var ownerOrg uuid.NullUUID
		_ = h.db.QueryRow(`SELECT organization_id FROM construction_projects WHERE id = $1 AND tenant_id = $2`,
			projectID, tenantID).Scan(&ownerOrg)
		if ownerOrg.Valid && ownerOrg.UUID != activeOrg {
			queryArgs = append(queryArgs, activeOrg)
			query += `
		AND (
		    NOT EXISTS (
		        SELECT 1 FROM construction_subcontract_buildings sb
		        JOIN construction_subcontract s ON s.id = sb.subcontract_id
		        WHERE s.tenant_id = b.tenant_id AND s.project_id = b.project_id
		          AND s.subcontractor_organization_id = $3
		    )
		    OR b.id IN (
		        SELECT sb.building_id FROM construction_subcontract_buildings sb
		        JOIN construction_subcontract s ON s.id = sb.subcontract_id
		        WHERE s.tenant_id = b.tenant_id AND s.subcontractor_organization_id = $3
		    )
		)`
		}
	}

	query += "\n\t\tORDER BY COALESCE(b.sort_order, 0), b.code"

	rows, err := h.db.Query(query, queryArgs...)
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
			&b.CRMBlockID, &b.CurrentCRMStage, &b.CRMAutoSync,
			&b.SectionsCount, &b.TotalSmeta, &b.FilesCount,
		); err != nil {
			h.log.Error("Failed to scan building", "error", err, "project_id", projectID)
			continue
		}
		b.GpsCoordinates = json.RawMessage(gpsCoordinates)
		b.SubcontractorOrgIDs = []string{}
		buildings = append(buildings, b)
	}

	// Attach each block's assigned subcontractor companies (org-backed links).
	if len(buildings) > 0 {
		idx := make(map[int64]int, len(buildings))
		for i := range buildings {
			idx[buildings[i].ID] = i
		}
		linkRows, lerr := h.db.Query(`
			SELECT sb.building_id, s.subcontractor_organization_id::text
			FROM construction_subcontract_buildings sb
			JOIN construction_subcontract s ON s.id = sb.subcontract_id
			WHERE s.tenant_id = $1 AND s.project_id = $2
			  AND s.subcontractor_organization_id IS NOT NULL`, tenantID, projectID)
		if lerr == nil {
			defer linkRows.Close()
			for linkRows.Next() {
				var bID int64
				var orgID string
				if err := linkRows.Scan(&bID, &orgID); err != nil {
					continue
				}
				if i, ok := idx[bID]; ok {
					buildings[i].SubcontractorOrgIDs = append(buildings[i].SubcontractorOrgIDs, orgID)
				}
			}
		}
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

	// Block management is owner-only — a subcontractor company on this project
	// may not add blocks.
	if !h.isProjectOwnerOrg(c, tenantID, projectID) {
		response.Forbidden(c, "Bloklarni faqat bosh tashkilot boshqaradi")
		return
	}

	var req entity.CreateConstructionBuildingInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// Resolve a unique code. The frontend auto-generates a code from
	// the building name (e.g. "Block 2" → "BLOCK_2") which is
	// deterministic — meaning two buildings with the same name in the
	// same project would collide on `construction_buildings_project_id_code_key`
	// and the user used to see a generic 500. Now we retry with a
	// numeric suffix until we find a free code (BLOCK_2, BLOCK_2-2,
	// BLOCK_2-3, ...). 50 attempts is more than enough for any sane
	// project; if exhausted we fall back to a UUID-suffixed code so
	// the request still succeeds rather than 500-ing.
	baseCode := strings.TrimSpace(req.Code)
	if baseCode == "" {
		baseCode = "BUILDING"
	}
	tryCode := baseCode

	var buildingID int64
	var createdDate time.Time

	const maxAttempts = 50
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = h.db.QueryRow(query,
			tenantID, projectID, tryCode, req.Name, nullString(req.Description),
			nullString(req.BuildingType), nullString(req.BuildingPurpose),
			nullInt32(int32(req.FloorsCount)), nullInt32(int32(req.FloorsUnderground)),
			nullFloat64(req.TotalArea), nullFloat64(req.LivingArea), nullFloat64(req.NonLivingArea),
			nullInt32(int32(req.ApartmentsCount)), nullInt32(int32(req.CommercialUnitsCount)), nullInt32(int32(req.ParkingSpots)),
			nullFloat64(req.EstimatedCost),
			plannedStart, plannedEnd,
			req.SortOrder,
		).Scan(&buildingID, &createdDate)
		if lastErr == nil {
			break // success
		}
		// 23505 is Postgres' unique_violation. Anything else is fatal —
		// fall through to the error path below.
		if pqErr, ok := lastErr.(*pq.Error); ok && pqErr.Code == "23505" {
			tryCode = fmt.Sprintf("%s-%d", baseCode, attempt+1)
			continue
		}
		break
	}
	if lastErr != nil {
		h.log.Error("Failed to create building", "error", lastErr, "last_tried_code", tryCode)
		response.InternalError(c, "Failed to create building")
		return
	}
	// Surface the resolved code back to the client so the frontend can
	// show "Created as BLOCK_2-3" if the original collided.
	req.Code = tryCode

	// Update project buildings count
	h.db.Exec(`UPDATE construction_projects SET buildings_count = buildings_count + 1, updated_date = NOW() WHERE id = $1`, projectID)

	// Assign subcontractor companies to the new block (owner-controlled).
	if len(req.SubcontractorOrgIDs) > 0 {
		h.setBuildingSubcontractorOrgs(tenantID, projectID, buildingID, req.SubcontractorOrgIDs)
	}

	response.Created(c, map[string]interface{}{
		"id":           buildingID,
		"code":         req.Code,
		"name":         req.Name,
		"created_date": createdDate,
	})
}

// isProjectOwnerOrg reports whether the active organization owns the project
// (or there is no org context / the project is unassigned). Used to keep block
// management — including block↔subcontractor assignment — owner-only, so a
// subcontractor company can't assign itself to blocks it shouldn't work.
func (h *Handler) isProjectOwnerOrg(c *gin.Context, tenantID uuid.UUID, projectID int64) bool {
	activeOrg, hasOrg := middleware.GetOrganizationID(c)
	if !hasOrg || activeOrg == uuid.Nil {
		return true
	}
	var ownerOrg uuid.NullUUID
	if err := h.db.QueryRow(`SELECT organization_id FROM construction_projects WHERE id = $1 AND tenant_id = $2`,
		projectID, tenantID).Scan(&ownerOrg); err != nil {
		return true
	}
	return !ownerOrg.Valid || ownerOrg.UUID == activeOrg
}

// setBuildingSubcontractorOrgs assigns subcontractor COMPANIES to a block. For
// each company it find-or-creates a subcontract on this project (so imports
// have a subcontract to attach to), then links the block to those subcontracts.
// Only org-backed (internal) links are reconciled — external subcontract↔block
// links are left untouched.
func (h *Handler) setBuildingSubcontractorOrgs(tenantID uuid.UUID, projectID, buildingID int64, orgIDs []string) {
	targetSubIDs := map[int64]bool{}
	for _, raw := range orgIDs {
		orgID, perr := uuid.Parse(raw)
		if perr != nil {
			continue
		}
		// Must be a real organization of this tenant.
		var orgName string
		if err := h.db.QueryRow(`SELECT name FROM organizations WHERE id = $1 AND tenant_id = $2`,
			orgID, tenantID).Scan(&orgName); err != nil {
			continue
		}
		// Find-or-create the subcontract for this company on this project.
		var subID int64
		err := h.db.QueryRow(`SELECT id FROM construction_subcontract
			WHERE tenant_id = $1 AND project_id = $2 AND subcontractor_organization_id = $3
			ORDER BY id LIMIT 1`, tenantID, projectID, orgID).Scan(&subID)
		if err != nil {
			if cerr := h.db.QueryRow(`INSERT INTO construction_subcontract
				(tenant_id, project_id, subcontractor_organization_id, name, partner_name, state, created_date, updated_date)
				VALUES ($1, $2, $3, $4, $4, 'draft', NOW(), NOW()) RETURNING id`,
				tenantID, projectID, orgID, orgName).Scan(&subID); cerr != nil {
				continue
			}
		}
		targetSubIDs[subID] = true
	}

	// Collect existing internal (org-backed) links for this block.
	var existing []int64
	if rows, qerr := h.db.Query(`
		SELECT sb.subcontract_id
		FROM construction_subcontract_buildings sb
		JOIN construction_subcontract s ON s.id = sb.subcontract_id
		WHERE sb.building_id = $1 AND s.subcontractor_organization_id IS NOT NULL`, buildingID); qerr == nil {
		for rows.Next() {
			var sid int64
			if rows.Scan(&sid) == nil {
				existing = append(existing, sid)
			}
		}
		rows.Close()
	}

	// Remove internal links that are no longer selected.
	for _, sid := range existing {
		if !targetSubIDs[sid] {
			h.db.Exec(`DELETE FROM construction_subcontract_buildings WHERE building_id = $1 AND subcontract_id = $2`,
				buildingID, sid)
		}
	}
	// Insert the selected links.
	for sid := range targetSubIDs {
		h.db.Exec(`INSERT INTO construction_subcontract_buildings (subcontract_id, building_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, sid, buildingID)
	}
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

	// Resolve the owning project so we can enforce owner-only block management.
	var ownerProjectID int64
	if err := h.db.QueryRow(`SELECT project_id FROM construction_buildings WHERE id = $1 AND tenant_id = $2`,
		buildingID, tenantID).Scan(&ownerProjectID); err != nil {
		response.NotFound(c, "Building not found")
		return
	}
	if !h.isProjectOwnerOrg(c, tenantID, ownerProjectID) {
		response.Forbidden(c, "Bloklarni faqat bosh tashkilot boshqaradi")
		return
	}

	var req entity.UpdateConstructionBuildingInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	if len(updates) == 0 && req.SubcontractorOrgIDs == nil {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Apply column updates only when there are any (a subcontractor-only edit
	// touches just the junction table below).
	if len(updates) > 0 {
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
	}

	// Replace the block's subcontractor-company assignments when provided.
	if req.SubcontractorOrgIDs != nil {
		h.setBuildingSubcontractorOrgs(tenantID, ownerProjectID, buildingID, *req.SubcontractorOrgIDs)
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
		ORDER BY ss.sort_order, ss.code`
	paginate, page, pageSize, offset := optPagination(c)
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
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

	if !paginate {
		response.Success(c, sections)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM smeta_sections ss WHERE ss.project_id = $1 AND ss.tenant_id = $2`, projectID, tenantID).Scan(&total)
	response.Paginated(c, sections, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Auto-generate code if not provided
	if req.Code == "" {
		req.Code = fmt.Sprintf("SEC-%d", time.Now().UnixMilli())
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		ORDER BY si.sort_order, si.code`
	paginate, page, pageSize, offset := optPagination(c)
	args := []interface{}{sectionID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
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

	if !paginate {
		response.Success(c, items)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM smeta_items si WHERE si.section_id = $1 AND si.tenant_id = $2`, sectionID, tenantID).Scan(&total)
	response.Paginated(c, items, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

// bytesToRawJSON returns nil if bytes is empty/nil, otherwise json.RawMessage to embed as JSON
func bytesToRawJSON(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
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
		       COALESCE(c.name, o.name, '') as vendor_name
		FROM construction_project_vendors pv
		LEFT JOIN contacts c ON c.id = pv.vendor_id
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	var vendorUUID uuid.UUID

	// vendor_id is required — must reference an existing supplier (contact) or organization
	if req.VendorID == "" {
		response.BadRequest(c, "vendor_id is required")
		return
	}

	vendorUUID, err = uuid.Parse(req.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor ID format")
		return
	}

	// Check contacts table first (Procurement suppliers), then organizations as fallback
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND tenant_id = $2)", vendorUUID, tenantID).Scan(&exists)
	if err != nil || !exists {
		err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1 AND tenant_id = $2)", vendorUUID, tenantID).Scan(&exists)
		if err != nil || !exists {
			response.BadRequest(c, "Supplier not found")
			return
		}
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
		"id":        vendorRecordID,
		"vendor_id": vendorUUID,
		"message":   "Vendor added to project successfully",
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		ORDER BY pr.report_date DESC, pr.created_date DESC`
	paginate, page, pageSize, offset := optPagination(c)
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
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

	if !paginate {
		response.Success(c, reports)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM construction_photo_reports pr WHERE pr.project_id = $1 AND pr.tenant_id = $2`, projectID, tenantID).Scan(&total)
	response.Paginated(c, reports, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
			tenant_id, project_id, smeta_item_id, section_id, building_id,
			report_date, report_type, title, description, location_description,
			gps_latitude, gps_longitude, weather, temperature,
			photos, reported_by, review_status, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 'pending', NOW(), NOW())
		RETURNING id
	`

	var reportID int64
	err = h.db.QueryRow(query,
		tenantID, projectID, nullInt64(req.SmetaItemID), nullInt64(req.SectionID), nullInt64(req.BuildingID),
		reportDate, nullString(req.ReportType), nullString(req.Title), nullString(req.Description), nullString(req.LocationDescription),
		nullFloat64(req.GpsLatitude), nullFloat64(req.GpsLongitude), nullString(req.Weather), nullFloat64(req.Temperature),
		photosJSON, nil, // photos, reported_by
	).Scan(&reportID)
	if err != nil {
		h.log.Error("Failed to create photo report", "error", err)
		response.InternalError(c, "Failed to create photo report")
		return
	}

	// Fire-and-forget push to the Yuksalish CRM. Enqueue is a no-op if the
	// building isn't linked or CRM env isn't configured, so it's safe to
	// call unconditionally here. On failure it lands in crm_sync_queue and
	// the background worker retries it.
	if h.crmSync != nil {
		h.crmSync.Enqueue(c.Request.Context(), tenantID, reportID)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		       COALESCE(dr.photos, '[]'::jsonb),
		       dr.reported_by, dr.verified_by, dr.verification_status,
		       dr.created_date, dr.updated_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as reporter_name,
		       COALESCE(v.first_name || ' ' || v.last_name, '') as verifier_name
		FROM construction_daily_reports dr
		LEFT JOIN employees e ON e.id = dr.reported_by
		LEFT JOIN employees v ON v.id = dr.verified_by
		WHERE dr.project_id = $1 AND dr.tenant_id = $2
		ORDER BY dr.report_date DESC`
	paginate, page, pageSize, offset := optPagination(c)
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
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
		var workersDetails, equipmentUsed, materialsReceived, visitors, photos []byte
		var reportedBy, verifiedBy sql.NullString
		var createdDate, updatedDate time.Time
		var reporterName, verifierName string

		if err := rows.Scan(
			&id, &tenantIDVal, &projectIDVal, &reportDate,
			&weatherMorning, &weatherAfternoon, &tempMin, &tempMax,
			&workSummary, &issuesEncountered, &safetyNotes,
			&workersCount, &workersDetails, &equipmentUsed, &materialsReceived, &visitors,
			&photos,
			&reportedBy, &verifiedBy, &verificationStatus,
			&createdDate, &updatedDate,
			&reporterName, &verifierName,
		); err != nil {
			h.log.Error("Failed to scan daily report", "error", err, "id", id)
			continue
		}
		h.log.Info("Scanned daily report", "id", id, "date", reportDate)

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
			"workers_details":     bytesToRawJSON(workersDetails),
			"equipment_used":      bytesToRawJSON(equipmentUsed),
			"materials_received":  bytesToRawJSON(materialsReceived),
			"visitors":            bytesToRawJSON(visitors),
			"photos":              bytesToRawJSON(photos),
			"reported_by":         nullStringValue(reportedBy),
			"verified_by":         nullStringValue(verifiedBy),
			"verification_status": nullStringValue(verificationStatus),
			"created_date":        createdDate,
			"updated_date":        updatedDate,
			"reporter_name":       reporterName,
			"verifier_name":       verifierName,
		})
	}

	h.log.Info("Daily reports result", "project_id", projectID, "count", len(reports))
	if !paginate {
		response.Success(c, reports)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM construction_daily_reports dr WHERE dr.project_id = $1 AND dr.tenant_id = $2`, projectID, tenantID).Scan(&total)
	response.Paginated(c, reports, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	reportDate, _ := time.Parse("2006-01-02", req.ReportDate)

	query := `
		INSERT INTO construction_daily_reports (
			tenant_id, project_id, report_date,
			weather_morning, weather_afternoon, temperature_min, temperature_max,
			work_summary, issues_encountered, safety_notes,
			workers_count, workers_details, equipment_used, materials_received, visitors,
			photos, reported_by, verification_status, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, 'pending', NOW(), NOW())
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
	photosJSON := "[]"
	if len(req.Photos) > 0 {
		photosBytes, _ := json.Marshal(req.Photos)
		photosJSON = string(photosBytes)
	}

	var reportID int64
	err = h.db.QueryRow(query,
		tenantID, projectID, reportDate,
		nullString(req.WeatherMorning), nullString(req.WeatherAfternoon), nullFloat64(req.TemperatureMin), nullFloat64(req.TemperatureMax),
		nullString(req.WorkSummary), nullString(req.IssuesEncountered), nullString(req.SafetyNotes),
		req.WorkersCount, workersDetailsJSON, equipmentUsedJSON, materialsReceivedJSON, visitorsJSON,
		photosJSON,
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
		ID                 int64           `json:"id"`
		TenantID           uuid.UUID       `json:"tenant_id"`
		ProjectID          int64           `json:"project_id"`
		ReportDate         time.Time       `json:"report_date"`
		WeatherMorning     sql.NullString  `json:"weather_morning"`
		WeatherAfternoon   sql.NullString  `json:"weather_afternoon"`
		TemperatureMin     sql.NullFloat64 `json:"temperature_min"`
		TemperatureMax     sql.NullFloat64 `json:"temperature_max"`
		WorkSummary        sql.NullString  `json:"work_summary"`
		IssuesEncountered  sql.NullString  `json:"issues_encountered"`
		SafetyNotes        sql.NullString  `json:"safety_notes"`
		WorkersCount       sql.NullInt64   `json:"workers_count"`
		WorkersDetails     sql.NullString  `json:"workers_details"`
		EquipmentUsed      sql.NullString  `json:"equipment_used"`
		MaterialsReceived  sql.NullString  `json:"materials_received"`
		VerificationStatus sql.NullString  `json:"verification_status"`
		VerifiedBy         sql.NullInt64   `json:"verified_by"`
		VerifiedAt         sql.NullTime    `json:"verified_at"`
		VerifierNotes      sql.NullString  `json:"verifier_notes"`
		CreatedDate        time.Time       `json:"created_date"`
		UpdatedDate        time.Time       `json:"updated_date"`
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
		ReportDate        string                   `json:"report_date"`
		WeatherMorning    string                   `json:"weather_morning"`
		WeatherAfternoon  string                   `json:"weather_afternoon"`
		TemperatureMin    float64                  `json:"temperature_min"`
		TemperatureMax    float64                  `json:"temperature_max"`
		WorkSummary       string                   `json:"work_summary"`
		IssuesEncountered string                   `json:"issues_encountered"`
		SafetyNotes       string                   `json:"safety_notes"`
		WorkersCount      int                      `json:"workers_count"`
		WorkersDetails    string                   `json:"workers_details"`
		EquipmentUsed     string                   `json:"equipment_used"`
		MaterialsReceived string                   `json:"materials_received"`
		Photos            []map[string]interface{} `json:"photos"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		    photos = $13::jsonb,
		    updated_date = NOW()
		WHERE id = $14 AND tenant_id = $15
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
	photosJSON := "[]"
	if len(req.Photos) > 0 {
		photosBytes, err := json.Marshal(req.Photos)
		if err == nil {
			photosJSON = string(photosBytes)
		}
	}

	result, err := h.db.Exec(query,
		req.ReportDate, nullString(req.WeatherMorning), nullString(req.WeatherAfternoon),
		req.TemperatureMin, req.TemperatureMax,
		nullString(req.WorkSummary), nullString(req.IssuesEncountered), nullString(req.SafetyNotes),
		req.WorkersCount, workersDetailsJSON, equipmentUsedJSON, materialsReceivedJSON,
		photosJSON, reportID, tenantID,
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
		       COALESCE(a.first_name || ' ' || a.last_name, '') as approver_name,
		       mr.stock_operation_id,
		       COALESCE(so.name, '') as delivery_name,
		       COALESCE(so.state, '') as delivery_state,
		       COALESCE(mr.bill_subcontractor, false),
		       mr.subcontract_id,
		       COALESCE(sc.name, '') as subcontract_name,
		       COALESCE(mr.building_id, 0),
		       COALESCE(bld.name, '') as building_name
		FROM construction_material_requests mr
		LEFT JOIN employees e ON e.id = mr.requested_by
		LEFT JOIN employees a ON a.id = mr.approved_by
		LEFT JOIN stock_operations so ON so.id = mr.stock_operation_id
		LEFT JOIN construction_subcontract sc ON sc.id = mr.subcontract_id
		LEFT JOIN construction_buildings bld ON bld.id = mr.building_id
		WHERE mr.project_id = $1 AND mr.tenant_id = $2
		ORDER BY mr.request_date DESC`
	paginate, page, pageSize, offset := optPagination(c)
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
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
		var items json.RawMessage
		var createdDate, updatedDate time.Time
		var requesterName, approverName string
		var stockOperationID uuid.NullUUID
		var deliveryName, deliveryState string
		var billSubcontractor bool
		var subcontractID sql.NullInt64
		var subcontractName string
		var buildingID int64
		var buildingName string

		if err := rows.Scan(
			&id, &tenantIDVal, &projectIDVal,
			&requestNumber, &requestDate, &requiredDate,
			&requestedBy, &items, &status,
			&approvedBy, &approvalDate, &approvalNotes,
			&fulfilledDate, &fulfillmentNotes, &purchaseOrderID,
			&notes, &createdDate, &updatedDate,
			&requesterName, &approverName,
			&stockOperationID, &deliveryName, &deliveryState,
			&billSubcontractor, &subcontractID, &subcontractName,
			&buildingID, &buildingName,
		); err != nil {
			h.log.Error("Failed to scan material request", "error", err)
			continue
		}

		requests = append(requests, map[string]interface{}{
			"id":                 id,
			"tenant_id":          tenantIDVal,
			"project_id":         projectIDVal,
			"request_number":     nullStringValue(requestNumber),
			"request_date":       requestDate,
			"required_date":      nullTimeValue(requiredDate),
			"requested_by":       nullUUIDValue(requestedBy),
			"items":              items,
			"status":             nullStringValue(status),
			"approved_by":        nullUUIDValue(approvedBy),
			"approval_date":      nullTimeValue(approvalDate),
			"approval_notes":     nullStringValue(approvalNotes),
			"fulfilled_date":     nullTimeValue(fulfilledDate),
			"fulfillment_notes":  nullStringValue(fulfillmentNotes),
			"purchase_order_id":  nullUUIDValue(purchaseOrderID),
			"notes":              nullStringValue(notes),
			"created_date":       createdDate,
			"updated_date":       updatedDate,
			"requester_name":     requesterName,
			"approver_name":      approverName,
			"stock_operation_id": nullUUIDValue(stockOperationID),
			"delivery_name":      deliveryName,
			"delivery_state":     deliveryState,
			"bill_subcontractor": billSubcontractor,
			"subcontract_id":     nullInt64Value(subcontractID),
			"subcontract_name":   subcontractName,
			"building_id":        buildingID,
			"building_name":      buildingName,
		})
	}

	if !paginate {
		response.Success(c, requests)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM construction_material_requests mr WHERE mr.project_id = $1 AND mr.tenant_id = $2`, projectID, tenantID).Scan(&total)
	response.Paginated(c, requests, page, pageSize, total)
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

	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req struct {
		RequestDate       string      `json:"request_date" binding:"required"`
		RequiredDate      string      `json:"required_date"`
		Items             interface{} `json:"items"`
		Notes             string      `json:"notes"`
		BillSubcontractor bool        `json:"bill_subcontractor"`
		SubcontractID     int64       `json:"subcontract_id"`
		BuildingID        int64       `json:"building_id"`
		// StageID lets the caller attribute the auto-generated expense
		// line to a specific section. Optional — when omitted the
		// expense lands as project-wide and falls into the synthetic
		// "(Boshqalar)" row in the Byudjet breakdown. Existing callers
		// that don't set it keep working.
		StageID int64 `json:"stage_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Auto-generate request number: MR-{projectID}-{sequence}
	var seqNum int64
	h.db.QueryRow(`SELECT nextval('construction_material_request_seq')`).Scan(&seqNum)
	requestNumber := fmt.Sprintf("MR-%d-%04d", projectID, seqNum)

	requestDate, _ := time.Parse("2006-01-02", req.RequestDate)
	var requiredDate interface{}
	if req.RequiredDate != "" {
		t, _ := time.Parse("2006-01-02", req.RequiredDate)
		requiredDate = t
	}

	// Serialize items to JSON
	itemsJSON := "[]"
	if req.Items != nil {
		if b, err := json.Marshal(req.Items); err == nil {
			itemsJSON = string(b)
		}
	}

	var subcontractID interface{}
	if req.SubcontractID > 0 {
		subcontractID = req.SubcontractID
	}
	var buildingID interface{}
	if req.BuildingID > 0 {
		buildingID = req.BuildingID
	}

	query := `
		INSERT INTO construction_material_requests (
			tenant_id, project_id, request_number, request_date, required_date,
			items, notes, status, bill_subcontractor, subcontract_id, building_id, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, 'draft', $8, $9, $10, NOW(), NOW())
		RETURNING id
	`

	var requestID int64
	err = h.db.QueryRow(query,
		tenantID, projectID, requestNumber, requestDate, requiredDate,
		itemsJSON, nullString(req.Notes), req.BillSubcontractor, subcontractID, buildingID,
	).Scan(&requestID)
	if err != nil {
		h.log.Error("Failed to create material request", "error", err)
		response.InternalError(c, "Failed to create material request")
		return
	}

	// Auto-create a delivery stock operation for this material request
	var stockOpID *uuid.UUID
	stockOpID = h.createDeliveryForMaterialRequest(tenantID, organizationID, requestID, requestNumber, itemsJSON)
	if stockOpID != nil {
		h.db.Exec(`UPDATE construction_material_requests SET stock_operation_id = $1 WHERE id = $2 AND tenant_id = $3`,
			stockOpID, requestID, tenantID)
	}

	// Auto-create expense line for material request
	{
		var totalAmount float64
		var itemDescriptions []string
		if items, ok := req.Items.([]interface{}); ok {
			for _, item := range items {
				if m, ok := item.(map[string]interface{}); ok {
					qty, _ := m["quantity"].(float64)
					cost, _ := m["unit_cost"].(float64)
					totalAmount += qty * cost
					if pname, ok := m["product_name"].(string); ok && pname != "" {
						itemDescriptions = append(itemDescriptions, pname)
					}
				}
			}
		}
		if totalAmount > 0 {
			expDesc := fmt.Sprintf("Material so'rovi: %s", requestNumber)
			if len(itemDescriptions) > 0 {
				expDesc += " (" + strings.Join(itemDescriptions, ", ") + ")"
			}

			// Optional stage_id from the request body — pass through to
			// the expense INSERT so the Byudjet breakdown can attribute
			// the row to a section. NULLIF(0,0)=NULL keeps backward-
			// compatible behaviour when the caller doesn't supply one.
			var stageIDInsert interface{}
			if req.StageID > 0 {
				stageIDInsert = req.StageID
			}
			if req.SubcontractID > 0 {
				// Billed to subcontractor — supplier_name = subcontractor partner_name
				var scPartnerName string
				h.db.QueryRow(`SELECT COALESCE(partner_name, name) FROM construction_subcontract WHERE id = $1`, req.SubcontractID).Scan(&scPartnerName)
				h.db.Exec(`
					INSERT INTO construction_expense_lines (
						tenant_id, organization_id, project_id, stage_id, expense_date, description,
						amount, currency_code, subcontract_id, material_request_id,
						supplier_name, status, created_by, created_at, updated_at
					) VALUES ($1, $2, $3, $11, $4, $5, $6, 'UZS', $7, $8, $9, 'draft', $10, NOW(), NOW())
				`, tenantID, organizationID, projectID, requestDate, expDesc,
					totalAmount, req.SubcontractID, requestID, scPartnerName, userID, stageIDInsert)
			} else {
				// No subcontractor — supplier is the company itself
				var orgName string
				h.db.QueryRow(`SELECT COALESCE(name, '') FROM organizations WHERE id = $1`, organizationID).Scan(&orgName)
				h.db.Exec(`
					INSERT INTO construction_expense_lines (
						tenant_id, organization_id, project_id, stage_id, expense_date, description,
						amount, currency_code, material_request_id,
						supplier_name, status, created_by, created_at, updated_at
					) VALUES ($1, $2, $3, $10, $4, $5, $6, 'UZS', $7, $8, 'draft', $9, NOW(), NOW())
				`, tenantID, organizationID, projectID, requestDate, expDesc,
					totalAmount, requestID, orgName, userID, stageIDInsert)
			}
		}
	}

	response.Created(c, map[string]interface{}{
		"id":                 requestID,
		"request_number":     requestNumber,
		"stock_operation_id": stockOpID,
		"message":            "Material request created successfully",
	})
}

// createDeliveryForMaterialRequest auto-creates a delivery stock operation for a material request.
// Returns the stock operation UUID or nil on failure.
func (h *Handler) createDeliveryForMaterialRequest(tenantID uuid.UUID, organizationID uuid.UUID, requestID int64, requestNumber string, itemsJSON string) *uuid.UUID {
	// Find the first delivery operation type for this tenant
	var opTypeID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM warehouse_operation_types
		WHERE tenant_id = $1 AND operation_type = 'outgoing' AND is_active = true
		ORDER BY sequence LIMIT 1
	`, tenantID).Scan(&opTypeID)
	if err != nil {
		h.log.Error("No delivery operation type found for tenant", "error", err, "tenant_id", tenantID)
		return nil
	}

	// Count steps for this operation type
	var totalSteps int
	h.db.QueryRow("SELECT COUNT(*) FROM operation_type_steps WHERE operation_type_id = $1 AND tenant_id = $2", opTypeID, tenantID).Scan(&totalSteps)
	if totalSteps == 0 {
		totalSteps = 1
	}

	opID := uuid.New()
	name := h.nextStockOperationName(tenantID, "delivery")
	now := time.Now()

	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}

	_, err = h.db.Exec(`
		INSERT INTO stock_operations (
			id, tenant_id, organization_id, name, operation_type_id, direction,
			date, source_document, source_type,
			state, current_step, total_steps, priority,
			note, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,'delivery',$6,$7,'material_request','draft',1,$8,'normal',$9,$10,$10)
	`,
		opID, tenantID, orgIDPtr, name, opTypeID,
		now, requestNumber, totalSteps,
		fmt.Sprintf("Auto-created from Material Request %s", requestNumber), now,
	)
	if err != nil {
		h.log.Error("Failed to create delivery stock operation for material request", "error", err)
		return nil
	}

	// Parse items and create operation lines
	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(itemsJSON), &items); err == nil {
		for _, item := range items {
			productIDStr, _ := item["product_id"].(string)
			if productIDStr == "" {
				continue
			}
			productID, err := uuid.Parse(productIDStr)
			if err != nil {
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
				h.db.QueryRow(`SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1`, productID).Scan(&unitCost)
			}

			uom, _ := item["unit_name"].(string)
			if uom == "" {
				h.db.QueryRow(`SELECT COALESCE(unit_name, 'unit') FROM products WHERE id = $1`, productID).Scan(&uom)
			}
			if uom == "" {
				uom = "unit"
			}

			h.db.Exec(`
				INSERT INTO stock_operation_lines (
					id, tenant_id, operation_id, product_id,
					expected_qty, done_qty, uom, unit_price,
					quality_status, created_at, updated_at
				) VALUES (uuid_generate_v4(),$1,$2,$3,$4,$4,$5,$6,'good',$7,$7)
			`, tenantID, opID, productID, qty, uom, unitCost, now)
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
	`, opTypeID, tenantID).Scan(&firstStep.ID, &firstStep.Name)
	if err == nil {
		firstStepName = firstStep.Name
	}

	h.db.Exec(`
		INSERT INTO stock_operation_step_log (
			id, tenant_id, operation_id, step_id, step_sequence, step_name, state, created_at
		) VALUES (uuid_generate_v4(),$1,$2,$3,1,$4,'ready',$5)
	`, tenantID, opID, firstStep.ID, firstStepName, now)

	return &opID
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
		RequestDate  string      `json:"request_date"`
		RequiredDate string      `json:"required_date"`
		Items        interface{} `json:"items"`
		Notes        string      `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Serialize items to JSON
	itemsJSON := "[]"
	if req.Items != nil {
		if b, err := json.Marshal(req.Items); err == nil {
			itemsJSON = string(b)
		}
	}

	query := `
		UPDATE construction_material_requests
		SET request_date = COALESCE(NULLIF($1, '')::date, request_date),
		    required_date = NULLIF($2, '')::date,
		    items = $3::jsonb,
		    notes = $4,
		    updated_date = NOW()
		WHERE id = $5 AND tenant_id = $6
	`

	result, err := h.db.Exec(query,
		req.RequestDate, req.RequiredDate,
		itemsJSON, nullString(req.Notes), requestID, tenantID,
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

// ApproveMaterialRequest confirms a material request, decreases inventory, and records a construction expense
// ApproveMaterialRequest godoc
// @Summary Approve material request
// @Description Approve a material request: decreases inventory quantities and records an expense in the construction budget
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
// @Router /construction/material-requests/{id}/approve [put]
func (h *Handler) ApproveMaterialRequest(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	// Resolve employee ID for approved_by (FK references employees, not users)
	var approverEmployeeID *uuid.UUID
	if userID != uuid.Nil {
		var empID uuid.UUID
		if err := h.db.QueryRow(`SELECT id FROM employees WHERE user_id = $1 AND tenant_id = $2 LIMIT 1`, userID, tenantID).Scan(&empID); err == nil {
			approverEmployeeID = &empID
		}
	}

	requestID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}

	// Check if material request has a linked stock operation — approval must go through Stock Operations
	var stockOpID uuid.NullUUID
	h.db.QueryRow(`SELECT stock_operation_id FROM construction_material_requests WHERE id = $1 AND tenant_id = $2`, requestID, tenantID).Scan(&stockOpID)
	if stockOpID.Valid && stockOpID.UUID != uuid.Nil {
		response.BadRequest(c, "This material request has a linked delivery. Please confirm it through Stock Operations.")
		return
	}

	// Load the material request
	var projectID int64
	var itemsRaw []byte
	var status string
	var expenseRecorded bool
	var mrBuildingID sql.NullInt64
	err = h.db.QueryRow(`
		SELECT project_id, items, status, COALESCE(expense_recorded, false), building_id
		FROM construction_material_requests
		WHERE id = $1 AND tenant_id = $2
	`, requestID, tenantID).Scan(&projectID, &itemsRaw, &status, &expenseRecorded, &mrBuildingID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to load material request", "error", err)
		response.InternalError(c, "Failed to load material request")
		return
	}

	if status == "approved" {
		response.BadRequest(c, "Material request is already approved")
		return
	}

	// Parse items JSON
	var items []map[string]interface{}
	if err := json.Unmarshal(itemsRaw, &items); err != nil || len(items) == 0 {
		response.BadRequest(c, "Material request has no items")
		return
	}

	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to approve material request")
		return
	}
	defer tx.Rollback()

	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}

	// Find expense/COGS account and inventory journal
	expenseAcct := findAccount(tx, tenantID, orgIDPtr, "construction expense", "9610")
	if expenseAcct == uuid.Nil {
		expenseAcct = findAccount(tx, tenantID, orgIDPtr, "cost of goods", "9110")
	}
	if expenseAcct == uuid.Nil {
		expenseAcct = findAccount(tx, tenantID, orgIDPtr, "expense", "9420")
	}

	var journalID uuid.UUID
	var nextNumber int
	tx.QueryRow(`SELECT id, next_number FROM journals WHERE tenant_id = $1 AND code IN ('CONST','STOCK','MISC','GENERAL') AND deleted_at IS NULL ORDER BY CASE code WHEN 'CONST' THEN 0 WHEN 'STOCK' THEN 1 WHEN 'MISC' THEN 2 ELSE 3 END LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)

	var totalExpense float64

	// Project's chosen default warehouse (migration 365). When set, it
	// becomes the second-priority fallback for items that don't supply
	// their own warehouse_id — preferred over the legacy "highest stock
	// anywhere" auto-pick. Same priority chain as reserveMaterialsForWork
	// so material requests and YAKUNIY confirmations both target the
	// same site warehouse.
	var projectWarehouseID uuid.NullUUID
	tx.QueryRow(`
		SELECT warehouse_id
		FROM construction_projects
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&projectWarehouseID)

	for index, item := range items {
		// Extract fields from item
		productIDStr, _ := item["product_id"].(string)
		variantIDStr, _ := item["variant_id"].(string)
		warehouseIDStr, _ := item["warehouse_id"].(string)

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

		if productIDStr == "" || qty <= 0 {
			continue
		}

		productID, err := uuid.Parse(productIDStr)
		if err != nil {
			continue
		}

		var variantID *uuid.UUID
		if variantIDStr != "" {
			vid, err := uuid.Parse(variantIDStr)
			if err == nil {
				variantID = &vid
			}
		}

		var warehouseID uuid.UUID
		if warehouseIDStr != "" {
			warehouseID, _ = uuid.Parse(warehouseIDStr)
		}
		// Project default takes precedence over the legacy "highest stock"
		// auto-pick. Only consulted when the item itself didn't carry an
		// explicit warehouse_id, preserving the per-line override.
		if warehouseID == uuid.Nil && projectWarehouseID.Valid {
			warehouseID = projectWarehouseID.UUID
		}
		// If still unresolved, find best warehouse for this product/variant
		if warehouseID == uuid.Nil {
			if variantID != nil {
				tx.QueryRow(`SELECT warehouse_id FROM inventory WHERE tenant_id = $1 AND product_id = $2 AND variant_id = $3 AND quantity_on_hand > 0 ORDER BY quantity_on_hand DESC LIMIT 1`, tenantID, productID, variantID).Scan(&warehouseID)
			}
			if warehouseID == uuid.Nil {
				tx.QueryRow(`SELECT warehouse_id FROM inventory WHERE tenant_id = $1 AND product_id = $2 AND quantity_on_hand > 0 ORDER BY quantity_on_hand DESC LIMIT 1`, tenantID, productID).Scan(&warehouseID)
			}
		}

		// Get unit_cost: prefer item value, then variant cost_price, then inventory unit_cost, then product cost_price
		if unitCost == 0 && variantID != nil {
			tx.QueryRow(`SELECT COALESCE(cost_price, 0) FROM product_variants WHERE id = $1`, variantID).Scan(&unitCost)
		}
		if unitCost == 0 {
			if variantID != nil {
				tx.QueryRow(`SELECT COALESCE(unit_cost, 0) FROM inventory WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3 AND variant_id = $4 LIMIT 1`, tenantID, productID, warehouseID, variantID).Scan(&unitCost)
			} else {
				tx.QueryRow(`SELECT COALESCE(unit_cost, 0) FROM inventory WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3 LIMIT 1`, tenantID, productID, warehouseID).Scan(&unitCost)
			}
		}
		if unitCost == 0 {
			tx.QueryRow(`SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1`, productID).Scan(&unitCost)
		}

		lineCost := qty * unitCost
		totalExpense += lineCost

		if warehouseID == uuid.Nil {
			continue
		}

		// Decrease inventory quantity_on_hand (variant-aware)
		if variantID != nil {
			_, err = tx.Exec(`
				UPDATE inventory
				SET quantity_on_hand = quantity_on_hand - $1,
				    last_movement_date = $2,
				    updated_at = $2
				WHERE tenant_id = $3 AND product_id = $4 AND warehouse_id = $5 AND variant_id = $6
				  AND quantity_on_hand >= $1
			`, qty, now, tenantID, productID, warehouseID, variantID)
		} else {
			_, err = tx.Exec(`
				UPDATE inventory
				SET quantity_on_hand = quantity_on_hand - $1,
				    last_movement_date = $2,
				    updated_at = $2
				WHERE tenant_id = $3 AND product_id = $4 AND warehouse_id = $5
				  AND COALESCE(variant_id::text,'') = ''
				  AND quantity_on_hand >= $1
			`, qty, now, tenantID, productID, warehouseID)
		}
		if err != nil {
			h.log.Error("Failed to decrease inventory", "error", err, "product_id", productIDStr)
		}

		// Record inventory transaction (best-effort, use savepoint)
		savepointName := fmt.Sprintf("sp_inv_%d", index)
		tx.Exec(`SAVEPOINT ` + savepointName)
		invTxID := uuid.New()
		var invErr error
		if variantID != nil {
			_, invErr = tx.Exec(`
				INSERT INTO inventory_transactions (
					id, tenant_id, organization_id, inventory_id, transaction_type, quantity,
					unit_cost, total_cost, reason, notes, transaction_date, created_by, created_at, variant_id
				)
				SELECT $1, $2, $3, i.id, 'issue', $4, $5, $6,
				       'construction_material_request', $7, $8, $9, $8, $10
				FROM inventory i
				WHERE i.tenant_id = $2 AND i.product_id = $11 AND i.warehouse_id = $12 AND i.variant_id = $10
				LIMIT 1
			`, invTxID, tenantID, organizationID, -qty, unitCost, lineCost,
				fmt.Sprintf("Material Request #%d", requestID), now, userID, variantID, productID, warehouseID)
		} else {
			_, invErr = tx.Exec(`
				INSERT INTO inventory_transactions (
					id, tenant_id, organization_id, inventory_id, transaction_type, quantity,
					unit_cost, total_cost, reason, notes, transaction_date, created_by, created_at
				)
				SELECT $1, $2, $3, i.id, 'issue', $4, $5, $6,
				       'construction_material_request', $7, $8, $9, $8
				FROM inventory i
				WHERE i.tenant_id = $2 AND i.product_id = $10 AND i.warehouse_id = $11
				  AND COALESCE(i.variant_id::text,'') = ''
				LIMIT 1
			`, invTxID, tenantID, organizationID, -qty, unitCost, lineCost,
				fmt.Sprintf("Material Request #%d", requestID), now, userID, productID, warehouseID)
		}
		if invErr != nil {
			h.log.Error("inventory_transaction insert failed (rolled back to savepoint)", "error", invErr)
			tx.Exec(`ROLLBACK TO SAVEPOINT ` + savepointName)
		} else {
			tx.Exec(`RELEASE SAVEPOINT ` + savepointName)
		}

		// Create journal entries (best-effort, use savepoint)
		if expenseAcct != uuid.Nil && journalID != uuid.Nil && lineCost > 0 {
			ca := getCategoryAccounts(tx, tenantID, orgIDPtr, productID)
			if ca.StockValuationAccountID != uuid.Nil {
				jSavepoint := fmt.Sprintf("sp_je_%d", index)
				tx.Exec(`SAVEPOINT ` + jSavepoint)

				entryID := uuid.New()
				entryNumber := fmt.Sprintf("MR%06d", nextNumber)
				nextNumber++

				_, jeErr := tx.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number,
						entry_date, description, source_type, source_id, status, total_debit, total_credit,
						created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, 'material_request', $8, 'posted', $9, $9, $10, $10)
				`, entryID, tenantID, organizationID, journalID, entryNumber,
					now, fmt.Sprintf("Construction Material Request #%d", requestID),
					nil, lineCost, now)

				if jeErr != nil {
					h.log.Error("journal_entry insert failed (rolled back to savepoint)", "error", jeErr)
					tx.Exec(`ROLLBACK TO SAVEPOINT ` + jSavepoint)
				} else {
					debitLineID := uuid.New()
					tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, $5, 0, 1, $6)`,
						debitLineID, entryID, expenseAcct, "Construction Material Expense", lineCost, now)
					creditLineID := uuid.New()
					tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, 0, $5, 2, $6)`,
						creditLineID, entryID, ca.StockValuationAccountID, "Stock Issued for Construction", lineCost, now)
					tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, lineCost, now, expenseAcct)
					tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, lineCost, now, ca.StockValuationAccountID)
					tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID)
					tx.Exec(`RELEASE SAVEPOINT ` + jSavepoint)
				}
			}
		}
	}

	// Upsert project materials list — track what has been approved for this project (best-effort)
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
		productName, _ := item["product_name"].(string)
		uom, _ := item["unit_name"].(string)
		// Fallback: look up product name and uom from products table
		if productName == "" {
			productID2, parseErr := uuid.Parse(productIDStr)
			if parseErr == nil {
				var dbName, dbUom string
				tx.QueryRow(`SELECT COALESCE(name,''), COALESCE(unit_name,'') FROM products WHERE id = $1 LIMIT 1`, productID2).Scan(&dbName, &dbUom)
				if dbName != "" {
					productName = dbName
				}
				if uom == "" && dbUom != "" {
					uom = dbUom
				}
			}
		}
		tx.Exec(`SAVEPOINT sp_pm`)
		_, pmErr := tx.Exec(`
			INSERT INTO construction_project_materials
				(tenant_id, project_id, product_id, product_name, uom, approved_quantity, unit_cost, created_date, updated_date)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			ON CONFLICT (tenant_id, project_id, product_id) DO UPDATE
				SET approved_quantity = construction_project_materials.approved_quantity + EXCLUDED.approved_quantity,
				    unit_cost = EXCLUDED.unit_cost,
				    product_name = CASE WHEN EXCLUDED.product_name != '' THEN EXCLUDED.product_name ELSE construction_project_materials.product_name END,
				    uom = CASE WHEN EXCLUDED.uom != '' THEN EXCLUDED.uom ELSE construction_project_materials.uom END,
				    updated_date = EXCLUDED.updated_date
		`, tenantID, projectID, productIDStr, productName, uom, qty, unitCost, now)
		if pmErr != nil {
			h.log.Error("project_materials upsert failed (rolled back to savepoint)", "error", pmErr)
			tx.Exec(`ROLLBACK TO SAVEPOINT sp_pm`)
		} else {
			tx.Exec(`RELEASE SAVEPOINT sp_pm`)
		}
	}

	// Auto-create material usage records (best-effort)
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
		if qty <= 0 {
			continue
		}
		productName, _ := item["product_name"].(string)
		uom, _ := item["unit_name"].(string)
		if productName == "" {
			pid, _ := uuid.Parse(productIDStr)
			var dbName, dbUom string
			tx.QueryRow(`SELECT COALESCE(name,''), COALESCE(unit_name,'') FROM products WHERE id = $1 LIMIT 1`, pid).Scan(&dbName, &dbUom)
			if dbName != "" {
				productName = dbName
			}
			if uom == "" && dbUom != "" {
				uom = dbUom
			}
		}
		var bldID interface{}
		if mrBuildingID.Valid && mrBuildingID.Int64 > 0 {
			bldID = mrBuildingID.Int64
		}
		tx.Exec(`SAVEPOINT sp_mu`)
		_, muErr := tx.Exec(`
			INSERT INTO construction_material_usage (
				tenant_id, project_id, building_id, product_id, product_name, uom,
				quantity_used, usage_date, recorded_by, notes, created_date, updated_date
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
		`, tenantID, projectID, bldID, productIDStr, productName, uom,
			qty, now.Format("2006-01-02"), approverEmployeeID,
			fmt.Sprintf("Material so'rovi #%d dan avtomatik qo'shildi", requestID))
		if muErr != nil {
			h.log.Error("material_usage insert failed (rolled back to savepoint)", "error", muErr)
			tx.Exec(`ROLLBACK TO SAVEPOINT sp_mu`)
		} else {
			tx.Exec(`RELEASE SAVEPOINT sp_mu`)
		}
	}

	// Record construction cost tracking entry (best-effort)
	if totalExpense > 0 {
		tx.Exec(`SAVEPOINT sp_cost`)
		_, ctErr := tx.Exec(`
			INSERT INTO construction_cost_tracking (
				tenant_id, project_id, tracking_date, actual_cost, notes, created_date
			) VALUES ($1, $2, $3, $4, $5, NOW())
		`, tenantID, projectID, now.Format("2006-01-02"), totalExpense,
			fmt.Sprintf("Material Request #%d approved", requestID))
		if ctErr != nil {
			h.log.Error("cost_tracking insert failed (rolled back to savepoint)", "error", ctErr)
			tx.Exec(`ROLLBACK TO SAVEPOINT sp_cost`)
		} else {
			tx.Exec(`RELEASE SAVEPOINT sp_cost`)
		}
	}

	// Auto-create expense lines for each material item + journal entries for category account
	if totalExpense > 0 {
		// Find "materials" cost category and its assigned account
		var materialsCatID sql.NullInt64
		var catDebitAccountID uuid.UUID
		tx.QueryRow(`SELECT id, COALESCE(default_debit_account_id, '00000000-0000-0000-0000-000000000000') FROM construction_cost_categories WHERE tenant_id = $1 AND code = 'materials' AND is_active = true LIMIT 1`, tenantID).Scan(&materialsCatID, &catDebitAccountID)

		// Resolve credit account (cash/bank fallback)
		var creditAccountID uuid.UUID
		creditAccountID = findAccount(tx, tenantID, orgIDPtr, "kassa", "5010")
		if creditAccountID == uuid.Nil {
			creditAccountID = findAccount(tx, tenantID, orgIDPtr, "cash", "5010")
		}

		// Get construction journal for journal entries
		constJournalID := h.ensureConstructionJournal(tenantID, orgIDPtr)

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
				tx.QueryRow(`SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1`, pid).Scan(&unitCost)
			}
			lineCost := qty * unitCost
			if lineCost <= 0 {
				continue
			}

			productName, _ := item["product_name"].(string)
			uom, _ := item["unit_name"].(string)
			if productName == "" {
				pid, _ := uuid.Parse(productIDStr)
				tx.QueryRow(`SELECT COALESCE(name,''), COALESCE(unit_name,'') FROM products WHERE id = $1`, pid).Scan(&productName, &uom)
			}

			description := fmt.Sprintf("Material Request #%d: %s", requestID, productName)

			expLineID := uuid.New()
			tx.Exec(`SAVEPOINT sp_exp_line`)
			_, elErr := tx.Exec(`
				INSERT INTO construction_expense_lines (
					id, tenant_id, organization_id, project_id, cost_category_id,
					expense_date, description, product_id, quantity, uom, unit_price,
					amount, currency_code,
					document_url, status, created_by, created_at, updated_at
				) VALUES (
					$1, $2, $3, $4, $5,
					$6, $7, $8, $9, $10, $11,
					$12, 'UZS',
					'', 'approved', $13, $14, $14
				)
			`,
				expLineID, tenantID, orgIDPtr, projectID, materialsCatID,
				now.Format("2006-01-02"), description,
				nullUUIDFromVal(productIDStr), qty, uom, unitCost,
				lineCost, userID, now,
			)
			if elErr != nil {
				h.log.Error("expense_line insert failed", "error", elErr)
				tx.Exec(`ROLLBACK TO SAVEPOINT sp_exp_line`)
			} else {
				tx.Exec(`RELEASE SAVEPOINT sp_exp_line`)
			}

			// Create journal entry to record in category's account
			if catDebitAccountID != uuid.Nil && creditAccountID != uuid.Nil && constJournalID != uuid.Nil && lineCost > 0 {
				tx.Exec(`SAVEPOINT sp_je_cat`)
				jeNum := h.getNextJournalNumber(tx, constJournalID)
				entryID := uuid.New()
				entryNumber := fmt.Sprintf("CE%06d", jeNum)

				_, jeErr := tx.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number,
						entry_date, description, source_type, source_id,
						status, total_debit, total_credit, created_at, updated_at
					) VALUES ($1,$2,$3,$4,$5,$6,$7,'construction_expense',NULL,'posted',$8,$8,$9,$9)
				`, entryID, tenantID, organizationID, constJournalID, entryNumber,
					now, fmt.Sprintf("Construction Expense: %s", description),
					lineCost, now)

				if jeErr != nil {
					h.log.Error("category journal entry failed", "error", jeErr)
					tx.Exec(`ROLLBACK TO SAVEPOINT sp_je_cat`)
				} else {
					// Debit: category's expense account
					tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,$5,0,1,$6)`,
						uuid.New(), entryID, catDebitAccountID, description, lineCost, now)
					// Credit: cash/bank account
					tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,0,$5,2,$6)`,
						uuid.New(), entryID, creditAccountID, description, lineCost, now)
					// Update account balances
					tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, lineCost, now, catDebitAccountID)
					tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, lineCost, now, creditAccountID)
					tx.Exec(`RELEASE SAVEPOINT sp_je_cat`)
				}
			}
		}
	}

	// Mark request as approved
	_, err = tx.Exec(`
		UPDATE construction_material_requests
		SET status = 'approved',
		    approved_by = $1,
		    approval_date = $2,
		    expense_recorded = true,
		    updated_date = $2
		WHERE id = $3 AND tenant_id = $4
	`, approverEmployeeID, now, requestID, tenantID)
	if err != nil {
		h.log.Error("Failed to approve material request", "error", err)
		response.InternalError(c, "Failed to approve material request")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to approve material request")
		return
	}

	response.Success(c, map[string]interface{}{
		"message":       "Material request approved successfully",
		"total_expense": totalExpense,
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
		ORDER BY d.delivery_date DESC`
	paginate, page, pageSize, offset := optPagination(c)
	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
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

	if !paginate {
		response.Success(c, deliveries)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM construction_material_deliveries d WHERE d.project_id = $1 AND d.tenant_id = $2`, projectID, tenantID).Scan(&total)
	response.Paginated(c, deliveries, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	paginate, page, pageSize, offset := optPagination(c)

	query := `
		SELECT pt.id, pt.project_id, pt.employee_id, pt.role, pt.responsibilities,
		       pt.start_date, pt.end_date, pt.is_active, pt.created_date,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as employee_name,
		       COALESCE(e.job_title, '') as position,
		       COALESCE(e.phone, '') as phone,
		       COALESCE(e.email, '') as email,
		       COALESCE(pt.status, CASE WHEN pt.is_active THEN 'active' ELSE 'inactive' END) as status,
		       COALESCE(pt.notes, '') as notes
		FROM construction_project_team pt
		JOIN employees e ON e.id = pt.employee_id
		JOIN construction_projects cp ON cp.id = pt.project_id
		WHERE pt.project_id = $1 AND cp.tenant_id = $2
		ORDER BY pt.created_date DESC
	`

	args := []interface{}{projectID, tenantID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}
	rows, err := h.db.Query(query, args...)
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
		var role, employeeName, position, phone, email, status, notes string
		var responsibilities sql.NullString
		var startDate, endDate sql.NullTime
		var isActive bool
		var createdDate time.Time

		if err := rows.Scan(
			&id, &projectIDVal, &employeeID, &role, &responsibilities,
			&startDate, &endDate, &isActive, &createdDate,
			&employeeName, &position, &phone, &email,
			&status, &notes,
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
			"status":           status,
			"notes":            notes,
		})
	}

	if !paginate {
		response.Success(c, members)
		return
	}
	var total int
	_ = h.db.QueryRow(`
		SELECT COUNT(*)
		FROM construction_project_team pt
		JOIN construction_projects cp ON cp.id = pt.project_id
		WHERE pt.project_id = $1 AND cp.tenant_id = $2
	`, projectID, tenantID).Scan(&total)
	response.Paginated(c, members, page, pageSize, total)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

// UpdateConstructionTeamMember updates a team member's role, WBS, or building assignment
func (h *Handler) UpdateConstructionTeamMember(c *gin.Context) {
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

	var req struct {
		Role             *string `json:"role"`
		Responsibilities *string `json:"responsibilities"`
		WbsID            *int64  `json:"wbs_id"`
		BuildingID       *int64  `json:"building_id"`
		StartDate        *string `json:"start_date"`
		EndDate          *string `json:"end_date"`
		IsActive         *bool   `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.Role != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("role = $%d", argCount))
		args = append(args, *req.Role)
	}
	if req.Responsibilities != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("responsibilities = $%d", argCount))
		args = append(args, nullString(*req.Responsibilities))
	}
	if req.WbsID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("wbs_id = $%d", argCount))
		if *req.WbsID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.WbsID)
		}
	}
	if req.BuildingID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("building_id = $%d", argCount))
		if *req.BuildingID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.BuildingID)
		}
	}
	if req.StartDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("start_date = $%d", argCount))
		if *req.StartDate == "" {
			args = append(args, nil)
		} else {
			t, _ := time.Parse("2006-01-02", *req.StartDate)
			args = append(args, t)
		}
	}
	if req.EndDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("end_date = $%d", argCount))
		if *req.EndDate == "" {
			args = append(args, nil)
		} else {
			t, _ := time.Parse("2006-01-02", *req.EndDate)
			args = append(args, t)
		}
	}
	if req.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *req.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	args = append(args, memberID)
	argCount++
	args = append(args, projectID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		`UPDATE construction_project_team SET %s WHERE id = $%d AND project_id = $%d AND EXISTS (SELECT 1 FROM construction_projects WHERE id = $%d AND tenant_id = $%d)`,
		strings.Join(updates, ", "), argCount-2, argCount-1, argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update team member", "error", err)
		response.InternalError(c, "Failed to update team member")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Team member not found")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      memberID,
		"message": "Team member updated successfully",
	})
}

// TransferTeamMember transfers a team member from one construction project to another
func (h *Handler) TransferTeamMember(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

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

	var req struct {
		TargetProjectID int64  `json:"target_project_id" binding:"required"`
		Notes           string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input: target_project_id is required")
		return
	}

	// 1. Get the team member from source project (verify exists, tenant matches)
	var employeeID uuid.UUID
	var role, memberStatus string
	var responsibilities sql.NullString
	var employeeName string
	err = h.db.QueryRow(`
		SELECT pt.employee_id, pt.role, COALESCE(pt.status, CASE WHEN pt.is_active THEN 'active' ELSE 'inactive' END),
		       pt.responsibilities,
		       COALESCE(e.first_name || ' ' || e.last_name, '') as employee_name
		FROM construction_project_team pt
		JOIN employees e ON e.id = pt.employee_id
		JOIN construction_projects cp ON cp.id = pt.project_id
		WHERE pt.id = $1 AND pt.project_id = $2 AND cp.tenant_id = $3
	`, memberID, projectID, tenantID).Scan(&employeeID, &role, &memberStatus, &responsibilities, &employeeName)
	if err != nil {
		response.NotFound(c, "Team member not found")
		return
	}

	// 3. Verify member isn't already transferred
	if memberStatus == "transferred" {
		response.BadRequest(c, "Team member is already transferred")
		return
	}

	// 2. Verify target project exists and belongs to same tenant
	var targetProjectName string
	err = h.db.QueryRow(`
		SELECT name FROM construction_projects WHERE id = $1 AND tenant_id = $2
	`, req.TargetProjectID, tenantID).Scan(&targetProjectName)
	if err != nil {
		response.NotFound(c, "Target project not found")
		return
	}

	// Get source project name
	var sourceProjectName string
	err = h.db.QueryRow(`
		SELECT name FROM construction_projects WHERE id = $1 AND tenant_id = $2
	`, projectID, tenantID).Scan(&sourceProjectName)
	if err != nil {
		response.NotFound(c, "Source project not found")
		return
	}

	// 4. Update source member: SET status = 'transferred'
	transferNote := fmt.Sprintf("Ko'chirildi: %s", targetProjectName)
	_, err = h.db.Exec(`
		UPDATE construction_project_team
		SET status = 'transferred', is_active = false, notes = $1
		WHERE id = $2 AND project_id = $3
	`, transferNote, memberID, projectID)
	if err != nil {
		h.log.Error("Failed to update source team member", "error", err)
		response.InternalError(c, "Failed to transfer team member")
		return
	}

	// 5. Insert new member in target project
	targetNote := fmt.Sprintf("%s dan ko'chirildi", sourceProjectName)
	var newMemberID int64
	err = h.db.QueryRow(`
		INSERT INTO construction_project_team (
			project_id, employee_id, role, responsibilities, status, is_active, notes, created_date
		) VALUES ($1, $2, $3, $4, 'active', true, $5, NOW())
		ON CONFLICT (project_id, employee_id, role) DO UPDATE
		SET status = 'active', is_active = true, notes = EXCLUDED.notes
		RETURNING id
	`, req.TargetProjectID, employeeID, role, responsibilities, targetNote).Scan(&newMemberID)
	if err != nil {
		h.log.Error("Failed to create target team member", "error", err)
		response.InternalError(c, "Failed to create team member in target project")
		return
	}

	// 6. Log activity in both projects
	h.logConstructionActivity(tenantID, projectID, userID, "team_transfer",
		fmt.Sprintf("%s %s ga ko'chirildi", employeeName, targetProjectName), "TeamMember", memberID)
	h.logConstructionActivity(tenantID, req.TargetProjectID, userID, "team_transfer",
		fmt.Sprintf("%s %s dan ko'chirildi", employeeName, sourceProjectName), "TeamMember", newMemberID)

	// 7. Return success with both member IDs
	response.Success(c, map[string]interface{}{
		"message":           "Team member transferred successfully",
		"source_member_id":  memberID,
		"target_member_id":  newMemberID,
		"target_project_id": req.TargetProjectID,
	})
}

// =====================================================
// PROJECT COMPLETION (COMMISSION) HANDLER
// =====================================================

// CommissionProject marks a project as completed and creates Dt 0100 / Kt 0810 journal entry
func (h *Handler) CommissionProject(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req struct {
		CommissionDate      string `json:"commission_date"`
		FixedAssetAccountID string `json:"fixed_asset_account_id"`
	}
	_ = c.ShouldBindJSON(&req)

	// Load project
	var projStatus string
	var analyticAccID *uuid.UUID
	err = h.db.QueryRow(`
		SELECT COALESCE(status,'draft'), analytic_account_id
		FROM construction_projects WHERE id = $1 AND tenant_id = $2
	`, projectID, tenantID).Scan(&projStatus, &analyticAccID)
	if err != nil {
		response.NotFound(c, "Project not found")
		return
	}
	if projStatus == "completed" {
		response.BadRequest(c, "Project is already completed")
		return
	}

	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}

	// Calculate total approved WIP expenses
	var totalWIP float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(amount), 0)
		FROM construction_expense_lines
		WHERE project_id = $1 AND tenant_id = $2 AND status = 'approved' AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&totalWIP)

	// Resolve accounts
	wipAcct := h.getConstructionMappedAccount(tenantID, orgIDPtr, "wip_0810", "tugallanmagan qurilish", "0810")
	faAcct := uuid.Nil
	if req.FixedAssetAccountID != "" {
		faAcct, _ = uuid.Parse(req.FixedAssetAccountID)
	}
	if faAcct == uuid.Nil {
		faAcct = h.getConstructionMappedAccount(tenantID, orgIDPtr, "fixed_assets_0100", "asosiy vositalar", "0100")
	}

	commissionDate := req.CommissionDate
	if commissionDate == "" {
		commissionDate = time.Now().Format("2006-01-02")
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to commission project")
		return
	}
	defer tx.Rollback()

	now := time.Now()
	var commissionJEID *uuid.UUID

	// Create Dt Fixed Assets / Kt WIP journal entry (best-effort)
	if totalWIP > 0 && faAcct != uuid.Nil && wipAcct != uuid.Nil {
		journalID := h.ensureConstructionJournal(tenantID, orgIDPtr)
		if journalID != uuid.Nil {
			tx.Exec(`SAVEPOINT sp_commission_je`)
			nextNum := h.getNextJournalNumber(tx, journalID)
			entryID := uuid.New()
			entryNumber := fmt.Sprintf("COMM%06d", nextNum)
			_, jeErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number,
					entry_date, description, source_type, source_id,
					status, total_debit, total_credit, created_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,'project_commission',NULL,'posted',$8,$8,$9,$9)
			`, entryID, tenantID, organizationID, journalID, entryNumber,
				now, fmt.Sprintf("Foydalanishga topshirish: project #%d", projectID),
				totalWIP, now)
			if jeErr != nil {
				h.log.Error("Commission journal entry failed", "error", jeErr)
				tx.Exec(`ROLLBACK TO SAVEPOINT sp_commission_je`)
			} else {
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,$5,0,1,$6)`,
					uuid.New(), entryID, faAcct, "Asset capitalization", totalWIP, now)
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,0,$5,2,$6)`,
					uuid.New(), entryID, wipAcct, "WIP cleared", totalWIP, now)
				tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalWIP, now, faAcct)
				tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalWIP, now, wipAcct)
				tx.Exec(`RELEASE SAVEPOINT sp_commission_je`)
				commissionJEID = &entryID
			}
		}
	}

	// Mark project as completed
	faAcctVal := interface{}(nil)
	if faAcct != uuid.Nil {
		faAcctVal = faAcct
	}
	_, err = tx.Exec(`
		UPDATE construction_projects
		SET status = 'completed',
		    commission_date = $1,
		    fixed_asset_account_id = $2,
		    commission_journal_entry_id = $3,
		    updated_date = NOW()
		WHERE id = $4 AND tenant_id = $5
	`, commissionDate, faAcctVal, commissionJEID, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to update project status", "error", err)
		response.InternalError(c, "Failed to commission project")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commission project")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "project", fmt.Sprintf("Loyiha foydalanishga topshirildi (WIP: %.2f)", totalWIP), "Project", projectID)

	result := map[string]interface{}{
		"message":         "Project commissioned successfully",
		"total_wip":       totalWIP,
		"commission_date": commissionDate,
	}
	if commissionJEID != nil {
		result["journal_entry_id"] = commissionJEID.String()
	}
	response.Success(c, result)
}

// =====================================================
// PORTFOLIO DASHBOARD
// =====================================================

// GetConstructionPortfolioDashboard returns cross-project KPIs and chart data
func (h *Handler) GetConstructionPortfolioDashboard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// KPIs
	var totalProjects, activeProjects, completedProjects int
	var totalBudget, totalActual float64
	_ = h.db.QueryRow(`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status NOT IN ('completed','cancelled')),
			COUNT(*) FILTER (WHERE status = 'completed'),
			COALESCE(SUM(contract_amount), 0)
		FROM construction_projects
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`, tenantID).Scan(&totalProjects, &activeProjects, &completedProjects, &totalBudget)

	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(el.amount), 0)
		FROM construction_expense_lines el
		WHERE el.tenant_id = $1 AND el.status = 'approved' AND el.deleted_at IS NULL
	`, tenantID).Scan(&totalActual)

	var expensesThisMonth int
	_ = h.db.QueryRow(`
		SELECT COUNT(*)
		FROM construction_expense_lines
		WHERE tenant_id = $1 AND status = 'approved' AND deleted_at IS NULL
		  AND DATE_TRUNC('month', expense_date) = DATE_TRUNC('month', CURRENT_DATE)
	`, tenantID).Scan(&expensesThisMonth)

	// Chart data: budget vs actual per project
	perProjectRows, err := h.db.Query(`
		SELECT p.id, p.name, COALESCE(p.contract_amount, 0),
		       COALESCE(SUM(CASE WHEN el.status='approved' AND el.deleted_at IS NULL THEN el.amount ELSE 0 END), 0)
		FROM construction_projects p
		LEFT JOIN construction_expense_lines el ON el.project_id = p.id AND el.tenant_id = p.tenant_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		GROUP BY p.id, p.name, p.contract_amount
		ORDER BY p.id DESC
		LIMIT 20
	`, tenantID)

	type ProjectBar struct {
		ID     int64   `json:"id"`
		Name   string  `json:"name"`
		Budget float64 `json:"budget"`
		Actual float64 `json:"actual"`
	}
	perProject := []ProjectBar{}
	if err == nil {
		defer perProjectRows.Close()
		for perProjectRows.Next() {
			var pb ProjectBar
			if err := perProjectRows.Scan(&pb.ID, &pb.Name, &pb.Budget, &pb.Actual); err == nil {
				perProject = append(perProject, pb)
			}
		}
	}

	// Chart data: expenses by category
	byCategoryRows, _ := h.db.Query(`
		SELECT COALESCE(cat.name, 'Uncategorized'), COALESCE(SUM(el.amount), 0)
		FROM construction_expense_lines el
		LEFT JOIN construction_cost_categories cat ON cat.id = el.cost_category_id
		WHERE el.tenant_id = $1 AND el.status = 'approved' AND el.deleted_at IS NULL
		GROUP BY cat.name
		ORDER BY SUM(el.amount) DESC
	`, tenantID)

	type CategorySlice struct {
		Category string  `json:"category"`
		Total    float64 `json:"total"`
	}
	byCategory := []CategorySlice{}
	if byCategoryRows != nil {
		defer byCategoryRows.Close()
		for byCategoryRows.Next() {
			var cs CategorySlice
			if err := byCategoryRows.Scan(&cs.Category, &cs.Total); err == nil {
				byCategory = append(byCategory, cs)
			}
		}
	}

	// Chart data: monthly expense dynamics (last 12 months)
	monthlyRows, _ := h.db.Query(`
		SELECT TO_CHAR(DATE_TRUNC('month', expense_date), 'YYYY-MM') AS month,
		       COALESCE(SUM(amount), 0)
		FROM construction_expense_lines
		WHERE tenant_id = $1 AND status = 'approved' AND deleted_at IS NULL
		  AND expense_date >= CURRENT_DATE - INTERVAL '12 months'
		GROUP BY DATE_TRUNC('month', expense_date)
		ORDER BY DATE_TRUNC('month', expense_date) ASC
	`, tenantID)

	type MonthlyData struct {
		Month string  `json:"month"`
		Total float64 `json:"total"`
	}
	monthly := []MonthlyData{}
	if monthlyRows != nil {
		defer monthlyRows.Close()
		for monthlyRows.Next() {
			var md MonthlyData
			if err := monthlyRows.Scan(&md.Month, &md.Total); err == nil {
				monthly = append(monthly, md)
			}
		}
	}

	var budgetDeviationPct float64
	if totalBudget > 0 {
		budgetDeviationPct = round2(((totalActual - totalBudget) / totalBudget) * 100)
	}

	response.Success(c, map[string]interface{}{
		"kpis": map[string]interface{}{
			"total_projects":       totalProjects,
			"active_projects":      activeProjects,
			"completed_projects":   completedProjects,
			"total_budget":         totalBudget,
			"total_actual":         totalActual,
			"total_variance":       totalBudget - totalActual,
			"budget_deviation_pct": budgetDeviationPct,
			"expenses_this_month":  expensesThisMonth,
		},
		"charts": map[string]interface{}{
			"per_project": perProject,
			"by_category": byCategory,
			"monthly":     monthly,
		},
	})
}

// createProjectCompletionJournalEntry creates a journal entry on project completion:
// Dt 0100 (Fixed Assets / Asosiy vositalar) / Kt 0810 (Capital Investments / Tugallanmagan qurilish)
func (h *Handler) createProjectCompletionJournalEntry(tenantID, organizationID uuid.UUID, projectID int64) {
	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}

	// Get project name and total actual cost (amount to capitalize)
	var projectName string
	var totalActualCost float64
	err := h.db.QueryRow(`
		SELECT COALESCE(p.name, ''), COALESCE(exp.total, 0)
		FROM construction_projects p
		LEFT JOIN LATERAL (
			SELECT SUM(el.amount) as total
			FROM construction_expense_lines el
			WHERE el.project_id = p.id AND el.tenant_id = p.tenant_id AND el.status = 'approved' AND el.deleted_at IS NULL
		) exp ON true
		WHERE p.id = $1 AND p.tenant_id = $2
	`, projectID, tenantID).Scan(&projectName, &totalActualCost)
	if err != nil || totalActualCost <= 0 {
		h.log.Error("Skipping completion journal entry: no costs or project not found", "project_id", projectID, "error", err)
		return
	}

	// Resolve accounts: Dt 0100 (Fixed Assets), Kt 0810 (WIP / Capital Investments)
	fixedAssetAcct := h.getConstructionMappedAccount(tenantID, orgIDPtr, "fixed_assets_0100", "asosiy vosita", "0100")
	if fixedAssetAcct == uuid.Nil {
		fixedAssetAcct = findAccount(h.db, tenantID, orgIDPtr, "asosiy vosita", "0100")
	}
	wipAcct := h.getConstructionMappedAccount(tenantID, orgIDPtr, "wip_0810", "tugallanmagan qurilish", "0810")
	if wipAcct == uuid.Nil {
		wipAcct = findAccount(h.db, tenantID, orgIDPtr, "tugallanmagan qurilish", "0810")
	}
	if fixedAssetAcct == uuid.Nil || wipAcct == uuid.Nil {
		h.log.Error("Skipping completion journal entry: accounts 0100 or 0810 not found", "project_id", projectID)
		return
	}

	journalID := h.ensureConstructionJournal(tenantID, orgIDPtr)
	if journalID == uuid.Nil {
		h.log.Error("Skipping completion journal entry: no journal", "project_id", projectID)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin tx for completion JE", "error", err)
		return
	}
	defer tx.Rollback()

	now := time.Now()
	nextNum := h.getNextJournalNumber(tx, journalID)
	entryID := uuid.New()
	entryNumber := fmt.Sprintf("CC%06d", nextNum)
	description := fmt.Sprintf("Qurilish tugallandi: %s — Asosiy vositalarga kiritish", projectName)

	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number,
			entry_date, description, source_type, source_id,
			status, total_debit, total_credit, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'construction_completion',NULL,'posted',$8,$8,$9,$9)
	`, entryID, tenantID, organizationID, journalID, entryNumber,
		now, description, totalActualCost, now)
	if err != nil {
		h.log.Error("Failed to create completion journal entry", "error", err)
		return
	}

	// Debit 0100 (Fixed Assets)
	_, err = tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
		VALUES ($1,$2,$3,$4,$5,0,1,$6)`,
		uuid.New(), entryID, fixedAssetAcct, description, totalActualCost, now)
	if err != nil {
		h.log.Error("Failed to create debit line for completion JE", "error", err)
		return
	}

	// Credit 0810 (Capital Investments / WIP)
	_, err = tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
		VALUES ($1,$2,$3,$4,0,$5,2,$6)`,
		uuid.New(), entryID, wipAcct, description, totalActualCost, now)
	if err != nil {
		h.log.Error("Failed to create credit line for completion JE", "error", err)
		return
	}

	// Update account balances
	tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalActualCost, now, fixedAssetAcct)
	tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalActualCost, now, wipAcct)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit completion journal entry", "error", err)
		return
	}

	h.log.Info("Created completion journal entry", "project_id", projectID, "entry_id", entryID, "amount", totalActualCost)
}

// ──────────────────────────────────────────────────────────────────────────────
// Building Files
// ──────────────────────────────────────────────────────────────────────────────

// ListBuildingFiles returns all files for a building
func (h *Handler) ListBuildingFiles(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant ID required")
		return
	}

	buildingID, err := strconv.Atoi(c.Param("building_id"))
	if err != nil {
		response.BadRequest(c, "Invalid building ID")
		return
	}

	paginate, page, pageSize, offset := optPagination(c)
	query := `
		SELECT id, building_id, file_id, file_url, filename, file_size, mime_type, description, created_at, created_by
		FROM building_files
		WHERE tenant_id = $1 AND building_id = $2
		ORDER BY created_at DESC`
	args := []interface{}{tenantID, buildingID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}
	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list building files", "error", err)
		response.InternalServerError(c, "Failed to list files")
		return
	}
	defer rows.Close()

	type BuildingFile struct {
		ID          int    `json:"id"`
		BuildingID  int    `json:"building_id"`
		FileID      string `json:"file_id"`
		FileURL     string `json:"file_url"`
		Filename    string `json:"filename"`
		FileSize    int64  `json:"file_size"`
		MimeType    string `json:"mime_type"`
		Description string `json:"description"`
		CreatedAt   string `json:"created_at"`
		CreatedBy   string `json:"created_by"`
	}

	var files []BuildingFile
	for rows.Next() {
		var f BuildingFile
		var createdAt time.Time
		if err := rows.Scan(&f.ID, &f.BuildingID, &f.FileID, &f.FileURL, &f.Filename, &f.FileSize, &f.MimeType, &f.Description, &createdAt, &f.CreatedBy); err != nil {
			continue
		}
		f.CreatedAt = createdAt.Format(time.RFC3339)
		files = append(files, f)
	}

	if files == nil {
		files = []BuildingFile{}
	}
	if !paginate {
		response.Success(c, files)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM building_files WHERE tenant_id = $1 AND building_id = $2`, tenantID, buildingID).Scan(&total)
	response.Paginated(c, files, page, pageSize, total)
}

// CreateBuildingFile adds a file reference to a building
func (h *Handler) CreateBuildingFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant ID required")
		return
	}

	buildingID, err := strconv.Atoi(c.Param("building_id"))
	if err != nil {
		response.BadRequest(c, "Invalid building ID")
		return
	}

	var input struct {
		FileID      string `json:"file_id" binding:"required"`
		FileURL     string `json:"file_url" binding:"required"`
		Filename    string `json:"filename" binding:"required"`
		FileSize    int64  `json:"file_size"`
		MimeType    string `json:"mime_type"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get user email from context
	createdBy := ""
	if email, exists := c.Get("user_email"); exists {
		createdBy = email.(string)
	}

	var id int
	err = h.db.QueryRow(`
		INSERT INTO building_files (tenant_id, building_id, file_id, file_url, filename, file_size, mime_type, description, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, tenantID, buildingID, input.FileID, input.FileURL, input.Filename, input.FileSize, input.MimeType, input.Description, createdBy).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create building file", "error", err)
		response.InternalServerError(c, "Failed to save file")
		return
	}

	response.Created(c, gin.H{
		"id":          id,
		"building_id": buildingID,
		"file_id":     input.FileID,
		"file_url":    input.FileURL,
		"filename":    input.Filename,
		"file_size":   input.FileSize,
		"mime_type":   input.MimeType,
		"description": input.Description,
		"created_by":  createdBy,
	})
}

// DeleteBuildingFile removes a file reference from a building
func (h *Handler) DeleteBuildingFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant ID required")
		return
	}

	fileID, err := strconv.Atoi(c.Param("file_id"))
	if err != nil {
		response.BadRequest(c, "Invalid file ID")
		return
	}

	result, err := h.db.Exec(`
		DELETE FROM building_files WHERE id = $1 AND tenant_id = $2
	`, fileID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete building file", "error", err)
		response.InternalServerError(c, "Failed to delete file")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "File")
		return
	}

	response.NoContent(c)
}

// ──────────────────────────────────────────────────────────────────────────────
// Project Files
// ──────────────────────────────────────────────────────────────────────────────

// ListProjectFiles returns all files for a project
func (h *Handler) ListProjectFiles(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant ID required")
		return
	}

	projectID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	paginate, page, pageSize, offset := optPagination(c)
	query := `
		SELECT id, project_id, file_id, file_url, filename, file_size, mime_type, description, created_at, created_by
		FROM project_files
		WHERE tenant_id = $1 AND project_id = $2
		ORDER BY created_at DESC`
	args := []interface{}{tenantID, projectID}
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}
	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list project files", "error", err)
		response.InternalServerError(c, "Failed to list files")
		return
	}
	defer rows.Close()

	type ProjectFile struct {
		ID          int    `json:"id"`
		ProjectID   int    `json:"project_id"`
		FileID      string `json:"file_id"`
		FileURL     string `json:"file_url"`
		Filename    string `json:"filename"`
		FileSize    int64  `json:"file_size"`
		MimeType    string `json:"mime_type"`
		Description string `json:"description"`
		CreatedAt   string `json:"created_at"`
		CreatedBy   string `json:"created_by"`
	}

	var files []ProjectFile
	for rows.Next() {
		var f ProjectFile
		var createdAt time.Time
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.FileID, &f.FileURL, &f.Filename, &f.FileSize, &f.MimeType, &f.Description, &createdAt, &f.CreatedBy); err != nil {
			continue
		}
		f.CreatedAt = createdAt.Format(time.RFC3339)
		files = append(files, f)
	}

	if files == nil {
		files = []ProjectFile{}
	}
	if !paginate {
		response.Success(c, files)
		return
	}
	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM project_files WHERE tenant_id = $1 AND project_id = $2`, tenantID, projectID).Scan(&total)
	response.Paginated(c, files, page, pageSize, total)
}

// CreateProjectFile adds a file reference to a project
func (h *Handler) CreateProjectFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant ID required")
		return
	}

	projectID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var input struct {
		FileID      string `json:"file_id" binding:"required"`
		FileURL     string `json:"file_url" binding:"required"`
		Filename    string `json:"filename" binding:"required"`
		FileSize    int64  `json:"file_size"`
		MimeType    string `json:"mime_type"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get user email from context
	createdBy := ""
	if email, exists := c.Get("user_email"); exists {
		createdBy = email.(string)
	}

	var id int
	err = h.db.QueryRow(`
		INSERT INTO project_files (tenant_id, project_id, file_id, file_url, filename, file_size, mime_type, description, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, tenantID, projectID, input.FileID, input.FileURL, input.Filename, input.FileSize, input.MimeType, input.Description, createdBy).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create project file", "error", err)
		response.InternalServerError(c, "Failed to save file")
		return
	}

	response.Created(c, gin.H{
		"id":          id,
		"project_id":  projectID,
		"file_id":     input.FileID,
		"file_url":    input.FileURL,
		"filename":    input.Filename,
		"file_size":   input.FileSize,
		"mime_type":   input.MimeType,
		"description": input.Description,
		"created_by":  createdBy,
	})
}

// DeleteProjectFile removes a file reference from a project
func (h *Handler) DeleteProjectFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant ID required")
		return
	}

	fileID, err := strconv.Atoi(c.Param("file_id"))
	if err != nil {
		response.BadRequest(c, "Invalid file ID")
		return
	}

	result, err := h.db.Exec(`
		DELETE FROM project_files WHERE id = $1 AND tenant_id = $2
	`, fileID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete project file", "error", err)
		response.InternalServerError(c, "Failed to delete file")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "File")
		return
	}

	response.NoContent(c)
}

// =====================================================
// BUILDING ESTIMATE CLONE
// =====================================================

// CloneBuildingEstimates copies every construction_estimate row (with every
// construction_estimate_line under it) from a source building into the
// target building referenced in the URL. Same project, same tenant.
//
// Why this exists
// ---------------
// Projects routinely have several blocks of the same building "type"
// (Тип 1 / Тип 2 in the screenshots — 1А, 1Б, 1В all share Тип 1). The
// smeta is identical between siblings, but the existing flow forces the
// user to re-import the XLSX into each block, which is slow and easy to
// get wrong. This endpoint clones the source block's full estimate
// universe — every estimate (ВОР, Единич, Ресурс) and every line under
// them — into the target with one click from the block card's "..." menu.
//
// Safety guarantees
// -----------------
//   - Refuses if the target block already has any estimates. The block
//     card UI greys out the menu entry in that case; this is the
//     server-side enforcement so a race doesn't silently overwrite
//     in-progress FAKT data.
//   - Source ≠ target.
//   - Both blocks must belong to the same project / tenant.
//   - Wrapped in a single transaction — either the whole clone lands or
//     nothing does. No half-cloned blocks.
//
// What gets reset (vs. preserved)
// -------------------------------
//
//	Preserved on every line: name, uom, quantity, rates (material / labor /
//	  equipment), unit_rate, total_amount, code, item_number, resource_type,
//	  parent_item_number, parent_line_id (REMAPPED to the new line id),
//	  norm_rate, subline_seq, quantity_override, material_type,
//	  original_quantity, original_unit_rate, imported_quantity,
//	  imported_total, sort_order, is_manual, wbs_id.
//
//	Reset to defaults: actual_amount, done_quantity, approval_status,
//	  period_fakt, created_date, updated_date. The clone is a fresh build —
//	  FAKT progress on the source must not bleed into the target's
//	  dashboard, Form 2, or Reja vs Fakt summary. Matches the user's
//	  explicit decision to NOT clone FAKT data when this feature was
//	  scoped.
//
// What is NOT cloned
// ------------------
//   - construction_stages (Bosqichlar). The user opted out of including
//     stages — they want the target to start with the imported estimate
//     content only.
//   - construction_smeta_audit, resource_topups, resource_price_history.
//     These are operational history on the source estimate's lines and
//     don't apply to the target. They'll get fresh rows once the user
//     starts editing.
//   - construction_form2_iteration*. Iterations are FAKT snapshots; same
//     "fresh build" reasoning as above.
//
// POST /construction/projects/:id/buildings/:building_id/clone-estimates
//
// The :building_id in the URL is the SOURCE — the handler creates a brand
// new construction_buildings row called "<source name> Copy" (or " Copy
// (2)", " Copy (3)", … on subsequent duplications) in the same project,
// inherits all of the source's physical / financial / timeline metadata
// EXCEPT actuals (progress, actual_cost, actual dates — those belong to
// the source's history), then copies every estimate, line, stage, and
// file across in the same transaction. Net effect of one button-press:
// a new sibling block appears in the project with identical smeta and
// drawings to the source, ready for the user to start work.
//
// 200: { new_building: { id, name, code, … }, estimates_created,
//
//	lines_created, stages_created, files_created }
//
// (The body parameter `source_building_id` and the old "fail if target
// not empty" check from the previous implementation are no longer
// relevant — the target is always a fresh row.)
func (h *Handler) CloneBuildingEstimates(c *gin.Context) {
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
	sourceBuildingID, err := strconv.ParseInt(c.Param("building_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid source building ID")
		return
	}

	// Load the source building's metadata so the duplicate carries the
	// same building_type / floors / area / etc. — these are "what the
	// block IS" attributes and would normally have to be re-entered by
	// hand. We deliberately do NOT copy actuals (actual_cost,
	// progress_percent, actual_start_date, actual_end_date) — those belong
	// to the source's history and would mis-report progress on the
	// duplicate.
	var src struct {
		Name                 string
		Code                 string
		Description          sql.NullString
		BuildingType         sql.NullString
		BuildingPurpose      sql.NullString
		FloorsCount          sql.NullInt64
		FloorsUnderground    sql.NullInt64
		TotalArea            sql.NullFloat64
		LivingArea           sql.NullFloat64
		NonLivingArea        sql.NullFloat64
		ApartmentsCount      sql.NullInt64
		CommercialUnitsCount sql.NullInt64
		ParkingSpots         sql.NullInt64
		EstimatedCost        sql.NullFloat64
		Currency             sql.NullString
		PlannedStart         sql.NullTime
		PlannedEnd           sql.NullTime
		SortOrder            sql.NullInt64
	}
	if err := h.db.QueryRow(`
		SELECT name, code, description, building_type, building_purpose,
		       floors_count, floors_underground,
		       total_area, living_area, non_living_area,
		       apartments_count, commercial_units_count, parking_spots,
		       estimated_cost, currency,
		       planned_start_date, planned_end_date, sort_order
		FROM construction_buildings
		WHERE id = $1 AND project_id = $2 AND tenant_id = $3
	`, sourceBuildingID, projectID, tenantID).Scan(
		&src.Name, &src.Code, &src.Description,
		&src.BuildingType, &src.BuildingPurpose,
		&src.FloorsCount, &src.FloorsUnderground,
		&src.TotalArea, &src.LivingArea, &src.NonLivingArea,
		&src.ApartmentsCount, &src.CommercialUnitsCount, &src.ParkingSpots,
		&src.EstimatedCost, &src.Currency,
		&src.PlannedStart, &src.PlannedEnd, &src.SortOrder,
	); err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Source building")
			return
		}
		h.log.Error("CloneBuildingEstimates: load source failed", "error", err)
		response.InternalError(c, "Failed to read source building")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("CloneBuildingEstimates: begin tx failed", "error", err)
		response.InternalError(c, "Could not start clone transaction")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 0a. Create the duplicate building first — the rest of the clone
	//     logic INSERTs into construction_estimate, construction_stages
	//     and building_files using `targetBuildingID` as the foreign key,
	//     so it has to exist before any of that fires. Generated name is
	//     "<source name> Copy"; on subsequent duplications it becomes
	//     " Copy (2)", " Copy (3)", etc. Same retry-suffix pattern for
	//     the code (the existing CreateConstructionBuilding handler uses
	//     this to dodge the unique (project_id, code) index when the
	//     auto-generated code collides). actuals (progress_percent,
	//     actual_cost, actual_*_date) intentionally left at defaults so
	//     dashboards don't mis-report the duplicate as already in flight.
	targetBuildingID, newName, newCode, createErr := h.createDuplicateBuildingTx(
		tx, tenantID, projectID, src.Name, src.Code,
		src.Description, src.BuildingType, src.BuildingPurpose,
		src.FloorsCount, src.FloorsUnderground,
		src.TotalArea, src.LivingArea, src.NonLivingArea,
		src.ApartmentsCount, src.CommercialUnitsCount, src.ParkingSpots,
		src.EstimatedCost, src.Currency,
		src.PlannedStart, src.PlannedEnd, src.SortOrder,
	)
	if createErr != nil {
		h.log.Error("CloneBuildingEstimates: create duplicate failed", "error", createErr)
		response.InternalError(c, "Failed to create duplicate building")
		return
	}

	// 0. Pull and clone construction_stages for the source block. Stages
	//    are independent of estimate_line in the schema (lines reference
	//    stages by name via parent_item_number, not by id) so the order
	//    relative to the estimate clone doesn't matter — we just do them
	//    inside the same transaction. The user explicitly asked for
	//    stages to ride along with the estimate clone because import
	//    creates both together: an Excel smeta import populates lines
	//    AND auto-creates stages with matching names, and the Bosqichlar
	//    tab needs the construction_stages row in addition to the line
	//    bucket. Without this step the target block would render the
	//    sections derived from line names but lose:
	//      - empty user-added stages (no lines, only construction_stages row)
	//      - stage metadata (planned_start/end, planned_budget,
	//        requires_f19, notes)
	//      - the construction_stages.id needed by Forma 19 acts, daily
	//        logs, sub-stage materials, sub-stage equipment, etc.
	//
	// Preserved per-stage: name, stage_order, planned_budget,
	// planned_start, planned_end, notes, requires_f19.
	// Reset to defaults: status='not_started', actual_start=NULL,
	// actual_end=NULL — same "fresh build, no FAKT bleed" reasoning as
	// the estimate-line reset above.
	stageRows, err := tx.Query(`
		SELECT id, name, COALESCE(stage_order, 0),
		       COALESCE(planned_budget, 0),
		       planned_start, planned_end, notes,
		       COALESCE(requires_f19, FALSE)
		FROM construction_stages
		WHERE project_id = $1 AND tenant_id = $2 AND building_id = $3
		ORDER BY COALESCE(stage_order, 0) ASC, id ASC
	`, projectID, tenantID, sourceBuildingID)
	if err != nil {
		h.log.Error("CloneBuildingEstimates: list source stages failed", "error", err)
		response.InternalError(c, "Failed to list source stages")
		return
	}
	type srcStage struct {
		ID            int64
		Name          string
		StageOrder    int
		PlannedBudget float64
		PlannedStart  sql.NullTime
		PlannedEnd    sql.NullTime
		Notes         sql.NullString
		RequiresF19   bool
	}
	var srcStages []srcStage
	for stageRows.Next() {
		var s srcStage
		if err := stageRows.Scan(
			&s.ID, &s.Name, &s.StageOrder, &s.PlannedBudget,
			&s.PlannedStart, &s.PlannedEnd, &s.Notes, &s.RequiresF19,
		); err != nil {
			stageRows.Close()
			h.log.Error("CloneBuildingEstimates: scan source stage failed", "error", err)
			response.InternalError(c, "Failed to read source stages")
			return
		}
		srcStages = append(srcStages, s)
	}
	stageRows.Close()

	stagesCreated := 0
	for _, ss := range srcStages {
		if _, err := tx.Exec(`
			INSERT INTO construction_stages (
				tenant_id, project_id, building_id, name, stage_order,
				status, planned_budget, planned_start, planned_end,
				actual_start, actual_end, notes, requires_f19,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5,
				'not_started', $6, $7, $8,
				NULL, NULL, $9, $10,
				NOW(), NOW()
			)
		`,
			tenantID, projectID, targetBuildingID, ss.Name, ss.StageOrder,
			ss.PlannedBudget, ss.PlannedStart, ss.PlannedEnd,
			ss.Notes, ss.RequiresF19,
		); err != nil {
			h.log.Error("CloneBuildingEstimates: insert clone stage failed",
				"error", err, "source_stage_id", ss.ID)
			response.InternalError(c, "Failed to create cloned stage")
			return
		}
		stagesCreated++
	}

	// 1. Pull every estimate on the source.
	estRows, err := tx.Query(`
		SELECT id, version, name, COALESCE(state, 'draft'), COALESCE(is_current, false),
		       COALESCE(overhead_pct, 0), COALESCE(profit_pct, 0), COALESCE(vat_pct, 0),
		       source_type, subcontract_id
		FROM construction_estimate
		WHERE building_id = $1 AND project_id = $2 AND tenant_id = $3
		ORDER BY id ASC
	`, sourceBuildingID, projectID, tenantID)
	if err != nil {
		h.log.Error("CloneBuildingEstimates: list source estimates failed", "error", err)
		response.InternalError(c, "Failed to list source estimates")
		return
	}

	type srcEst struct {
		ID            int64
		Version       int
		Name          string
		State         string
		IsCurrent     bool
		OverheadPct   float64
		ProfitPct     float64
		VatPct        float64
		SourceType    sql.NullString
		SubcontractID sql.NullInt64
	}
	var srcEstimates []srcEst
	for estRows.Next() {
		var e srcEst
		if err := estRows.Scan(
			&e.ID, &e.Version, &e.Name, &e.State, &e.IsCurrent,
			&e.OverheadPct, &e.ProfitPct, &e.VatPct,
			&e.SourceType, &e.SubcontractID,
		); err != nil {
			estRows.Close()
			h.log.Error("CloneBuildingEstimates: scan source estimate failed", "error", err)
			response.InternalError(c, "Failed to read source estimates")
			return
		}
		srcEstimates = append(srcEstimates, e)
	}
	estRows.Close()

	// 0b. Pull and clone building_files for the source block. Files live
	//     in object storage at file_url; both blocks can safely reference
	//     the same blob — Delete is per-row, not per-blob, so when the
	//     user deletes a cloned file from the target it doesn't touch the
	//     source's row. Cloning the URL keeps the target's "Fayllar 2"
	//     badge populated immediately, which is what the user expects
	//     when blocks of the same TIP share architectural drawings.
	//
	// Preserved per-file: file_id, file_url, filename, file_size,
	// mime_type, description.
	// Reset: created_by (the user doing the clone), created_at (NOW).
	fileRows, err := tx.Query(`
		SELECT id, file_id, file_url, filename, COALESCE(file_size, 0),
		       COALESCE(mime_type, ''), COALESCE(description, '')
		FROM building_files
		WHERE tenant_id = $1 AND building_id = $2
		ORDER BY created_at ASC, id ASC
	`, tenantID, sourceBuildingID)
	if err != nil {
		h.log.Error("CloneBuildingEstimates: list source files failed", "error", err)
		response.InternalError(c, "Failed to list source files")
		return
	}
	type srcFile struct {
		ID          int64
		FileID      string
		FileURL     string
		Filename    string
		FileSize    int64
		MimeType    string
		Description string
	}
	var srcFiles []srcFile
	for fileRows.Next() {
		var f srcFile
		if err := fileRows.Scan(
			&f.ID, &f.FileID, &f.FileURL, &f.Filename,
			&f.FileSize, &f.MimeType, &f.Description,
		); err != nil {
			fileRows.Close()
			h.log.Error("CloneBuildingEstimates: scan source file failed", "error", err)
			response.InternalError(c, "Failed to read source files")
			return
		}
		srcFiles = append(srcFiles, f)
	}
	fileRows.Close()

	// Get the cloning user's email for created_by — matches the format
	// CreateBuildingFile writes (the column is VARCHAR storing email).
	clonedByEmail := ""
	if email, exists := c.Get("user_email"); exists {
		if s, ok := email.(string); ok {
			clonedByEmail = s
		}
	}

	filesCreated := 0
	for _, sf := range srcFiles {
		if _, err := tx.Exec(`
			INSERT INTO building_files (
				tenant_id, building_id, file_id, file_url, filename,
				file_size, mime_type, description, created_by, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		`,
			tenantID, targetBuildingID, sf.FileID, sf.FileURL, sf.Filename,
			sf.FileSize, sf.MimeType, sf.Description, clonedByEmail,
		); err != nil {
			h.log.Error("CloneBuildingEstimates: insert clone file failed",
				"error", err, "source_file_id", sf.ID)
			response.InternalError(c, "Failed to create cloned file")
			return
		}
		filesCreated++
	}

	// Refuse only when ALL source pools are empty. A block with stages
	// or files but no estimates is rare but legal (user pre-created
	// Bosqichlar or uploaded drawings before importing the smeta) —
	// still worth cloning.
	if len(srcEstimates) == 0 && len(srcStages) == 0 && len(srcFiles) == 0 {
		c.JSON(400, gin.H{
			"success": false,
			"error":   "Source building has no estimates, stages or files to clone",
			"code":    "SOURCE_EMPTY",
		})
		return
	}

	userID, _ := middleware.GetUserID(c)

	estimatesCreated := 0
	linesCreated := 0

	// Allocate fresh version numbers for the clones. The unique constraint
	// idx_construction_estimate_version is on (project_id, version), so
	// re-using the source's version (e.g. v86) collides with the existing
	// row. Read MAX(version) once and increment per clone — assigning
	// monotonically increasing versions inside the transaction so they
	// don't collide with each other either.
	var nextVersion int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM construction_estimate WHERE project_id = $1 AND tenant_id = $2`,
		projectID, tenantID,
	).Scan(&nextVersion); err != nil {
		h.log.Error("CloneBuildingEstimates: max version lookup failed", "error", err)
		response.InternalError(c, "Failed to allocate clone version")
		return
	}

	// The TARGET block's currently-open Forma 2 iteration. We seed period_fakt
	// rows for the cloned lines into it so the Bosqichlar BAJARILDI input
	// shows the carried-over progress (that input reads the open iteration's
	// period_fakt; the progress bar/status read done_quantity on the line).
	// Iterations are per-block (migration 451), so the cloned lines (which live
	// on targetBuildingID) must seed that block's iteration — not the project's.
	var openIterID sql.NullInt64
	_ = tx.QueryRow(`
		SELECT id FROM construction_form2_iteration
		WHERE project_id = $1 AND tenant_id = $2 AND building_id = $3 AND status = 'open'
		LIMIT 1
	`, projectID, tenantID, targetBuildingID).Scan(&openIterID)

	// 2. For each source estimate, create a fresh estimate row on the
	//    target building, then clone all of its lines. is_current is
	//    deliberately set to FALSE on every clone — the source's "v86 is
	//    the active єдинич" flag belongs to the source's version line,
	//    and forcing it onto the clone would silently demote whichever
	//    project-wide row currently carries it. The user can mark the
	//    clone active afterwards if they want.
	for _, se := range srcEstimates {
		nextVersion++
		var newEstID int64
		if err := tx.QueryRow(`
			INSERT INTO construction_estimate (
				tenant_id, project_id, building_id, version, name, state, is_current,
				overhead_pct, profit_pct, vat_pct,
				source_type, subcontract_id, created_by, created_date, updated_date
			) VALUES ($1, $2, $3, $4, $5, $6, false, $7, $8, $9, $10, $11, $12, NOW(), NOW())
			RETURNING id
		`,
			tenantID, projectID, targetBuildingID, nextVersion, se.Name, se.State,
			se.OverheadPct, se.ProfitPct, se.VatPct,
			se.SourceType, se.SubcontractID, userID,
		).Scan(&newEstID); err != nil {
			h.log.Error("CloneBuildingEstimates: insert clone estimate failed", "error", err, "source_estimate_id", se.ID)
			response.InternalError(c, "Failed to create cloned estimate")
			return
		}
		estimatesCreated++

		// Pull lines parent-first so child lines find their remapped
		// parent in idMap. parent_line_id NULL → top-level work, sorted
		// ahead of sub-lines by the CASE/ORDER below.
		lineRows, err := tx.Query(`
			SELECT id, wbs_id, name, COALESCE(uom, ''), COALESCE(quantity, 0),
			       COALESCE(material_rate, 0), COALESCE(labor_rate, 0), COALESCE(equipment_rate, 0),
			       COALESCE(unit_rate, 0), COALESCE(total_amount, 0),
			       COALESCE(code, ''), COALESCE(item_number, ''),
			       COALESCE(resource_type, ''), COALESCE(parent_item_number, ''),
			       parent_line_id, COALESCE(norm_rate, 0), COALESCE(subline_seq, 0),
			       COALESCE(quantity_override, FALSE),
			       COALESCE(material_type, 'standard'),
			       COALESCE(original_quantity, 0), COALESCE(original_unit_rate, 0),
			       imported_quantity, imported_total,
			       COALESCE(sort_order, 0), COALESCE(is_manual, FALSE),
			       COALESCE(done_quantity, 0)
			FROM construction_estimate_line
			WHERE estimate_id = $1 AND tenant_id = $2
			ORDER BY (CASE WHEN parent_line_id IS NULL THEN 0 ELSE 1 END) ASC,
			         COALESCE(sort_order, 0) ASC,
			         id ASC
		`, se.ID, tenantID)
		if err != nil {
			h.log.Error("CloneBuildingEstimates: list source lines failed", "error", err, "source_estimate_id", se.ID)
			response.InternalError(c, "Failed to list source lines")
			return
		}

		type srcLine struct {
			ID               int64
			WBSID            sql.NullInt64
			Name, UOM        string
			Quantity         float64
			MaterialRate     float64
			LaborRate        float64
			EquipmentRate    float64
			UnitRate         float64
			TotalAmount      float64
			Code             string
			ItemNumber       string
			ResourceType     string
			ParentItemNumber string
			ParentLineID     sql.NullInt64
			NormRate         float64
			SublineSeq       int
			QuantityOverride bool
			MaterialType     string
			OriginalQuantity float64
			OriginalUnitRate float64
			ImportedQuantity sql.NullFloat64
			ImportedTotal    sql.NullFloat64
			SortOrder        int
			IsManual         bool
			DoneQuantity     float64
		}
		var srcLines []srcLine
		for lineRows.Next() {
			var l srcLine
			if err := lineRows.Scan(
				&l.ID, &l.WBSID, &l.Name, &l.UOM, &l.Quantity,
				&l.MaterialRate, &l.LaborRate, &l.EquipmentRate,
				&l.UnitRate, &l.TotalAmount,
				&l.Code, &l.ItemNumber, &l.ResourceType, &l.ParentItemNumber,
				&l.ParentLineID, &l.NormRate, &l.SublineSeq,
				&l.QuantityOverride, &l.MaterialType,
				&l.OriginalQuantity, &l.OriginalUnitRate,
				&l.ImportedQuantity, &l.ImportedTotal,
				&l.SortOrder, &l.IsManual, &l.DoneQuantity,
			); err != nil {
				lineRows.Close()
				h.log.Error("CloneBuildingEstimates: scan source line failed", "error", err)
				response.InternalError(c, "Failed to read source lines")
				return
			}
			srcLines = append(srcLines, l)
		}
		lineRows.Close()

		// Old line id → new line id. Used to remap parent_line_id on
		// child rows so resource sub-lines still attach to the right
		// parent work in the clone. Order above guarantees parents are
		// inserted (and added to the map) before their children.
		idMap := make(map[int64]int64)

		for _, sl := range srcLines {
			var newParent sql.NullInt64
			if sl.ParentLineID.Valid {
				if mapped, ok := idMap[sl.ParentLineID.Int64]; ok {
					newParent = sql.NullInt64{Int64: mapped, Valid: true}
				}
				// If the parent didn't make it into idMap (orphaned row —
				// shouldn't happen in practice, but defensive), leave the
				// new line with NULL parent rather than fail the whole
				// clone. The line will render as top-level on the target;
				// not perfect, but better than silently breaking the user's
				// one-click "make sibling block match" expectation.
			}

			// Carry over progress. The HOLAT/approval workflow state is
			// reset to a clean "pending / in_progress" derived from the
			// done quantity (so a cloned, partly-done work reads JARAYONDA
			// and stays editable) rather than copying submitted/confirmed
			// signatures, which would clone into a locked state with no
			// reviewer context.
			approvalStatus := "pending"
			if sl.DoneQuantity > 0 {
				approvalStatus = "in_progress"
			}

			var newLineID int64
			if err := tx.QueryRow(`
				INSERT INTO construction_estimate_line (
					tenant_id, estimate_id, wbs_id, name, uom, quantity,
					material_rate, labor_rate, equipment_rate, unit_rate, total_amount,
					code, item_number, resource_type, parent_item_number,
					parent_line_id, norm_rate, subline_seq, quantity_override, material_type,
					original_quantity, original_unit_rate, imported_quantity, imported_total,
					sort_order, is_manual, done_quantity, approval_status, created_date, updated_date
				) VALUES (
					$1, $2, $3, $4, $5, $6,
					$7, $8, $9, $10, $11,
					$12, $13, $14, $15,
					$16, $17, $18, $19, $20,
					$21, $22, $23, $24,
					$25, $26, $27, $28, NOW(), NOW()
				)
				RETURNING id
			`,
				tenantID, newEstID, sl.WBSID, sl.Name, sl.UOM, sl.Quantity,
				sl.MaterialRate, sl.LaborRate, sl.EquipmentRate, sl.UnitRate, sl.TotalAmount,
				sl.Code, sl.ItemNumber, sl.ResourceType, sl.ParentItemNumber,
				newParent, sl.NormRate, sl.SublineSeq, sl.QuantityOverride, sl.MaterialType,
				sl.OriginalQuantity, sl.OriginalUnitRate, sl.ImportedQuantity, sl.ImportedTotal,
				sl.SortOrder, sl.IsManual, sl.DoneQuantity, approvalStatus,
			).Scan(&newLineID); err != nil {
				h.log.Error("CloneBuildingEstimates: insert clone line failed",
					"error", err, "source_line_id", sl.ID, "new_estimate_id", newEstID)
				response.InternalError(c, "Failed to create cloned line")
				return
			}
			idMap[sl.ID] = newLineID
			linesCreated++

			// Seed the open Forma 2 iteration with this line's period_fakt so
			// the BAJARILDI input shows the carried-over progress. The clone
			// has no frozen-iteration history, so its entire done quantity
			// lives in the current open iteration — keeping the invariant
			// done_quantity = Σ period_fakt intact.
			if openIterID.Valid && sl.DoneQuantity != 0 {
				if _, err := tx.Exec(`
					INSERT INTO construction_form2_iteration_line (iteration_id, estimate_line_id, period_fakt)
					VALUES ($1, $2, $3)
					ON CONFLICT (iteration_id, estimate_line_id) DO UPDATE SET period_fakt = EXCLUDED.period_fakt
				`, openIterID.Int64, newLineID, sl.DoneQuantity); err != nil {
					h.log.Error("CloneBuildingEstimates: seed iteration line failed",
						"error", err, "new_line_id", newLineID)
					response.InternalError(c, "Failed to seed cloned progress")
					return
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("CloneBuildingEstimates: commit failed", "error", err)
		response.InternalError(c, "Failed to commit clone")
		return
	}
	committed = true

	// Bump the project's denormalised building count so the UI badge
	// updates without a separate round-trip. Best-effort; failure to
	// update the cache shouldn't break the duplication (the buildings
	// list will recompute it on next load anyway).
	_, _ = h.db.Exec(`
		UPDATE construction_projects
		SET buildings_count = buildings_count + 1, updated_date = NOW()
		WHERE id = $1
	`, projectID)

	response.Success(c, gin.H{
		"message":            "Building duplicated",
		"estimates_created":  estimatesCreated,
		"lines_created":      linesCreated,
		"stages_created":     stagesCreated,
		"files_created":      filesCreated,
		"source_building_id": sourceBuildingID,
		"new_building": gin.H{
			"id":   targetBuildingID,
			"name": newName,
			"code": newCode,
		},
	})
}

// createDuplicateBuildingTx inserts a new construction_buildings row
// inside an existing transaction, named "<sourceName> Copy" (or " Copy
// (N)" on subsequent duplications). Returns the new building id, name,
// and code. Mirrors the unique-code retry pattern used by
// CreateConstructionBuilding so a collision on
// (project_id, code) doesn't 500 the duplicate.
func (h *Handler) createDuplicateBuildingTx(
	tx *sql.Tx,
	tenantID uuid.UUID, projectID int64,
	srcName, srcCode string,
	description, buildingType, buildingPurpose sql.NullString,
	floorsCount, floorsUnderground sql.NullInt64,
	totalArea, livingArea, nonLivingArea sql.NullFloat64,
	apartmentsCount, commercialUnitsCount, parkingSpots sql.NullInt64,
	estimatedCost sql.NullFloat64,
	currency sql.NullString,
	plannedStart, plannedEnd sql.NullTime,
	sortOrder sql.NullInt64,
) (newID int64, finalName string, finalCode string, err error) {
	// Pick a base name. " Copy" first, then " Copy (2)" / " Copy (3)" if
	// names already exist. We check up-front against the existing names
	// to avoid hammering the unique index with retries (name has no
	// uniqueness constraint, but readability matters — the UI would show
	// two blocks both literally called "Block 1 Copy" which is confusing).
	tryName := srcName + " Copy"
	for n := 2; n <= 50; n++ {
		var exists bool
		if err = tx.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM construction_buildings
				WHERE project_id = $1 AND tenant_id = $2 AND name = $3
			)
		`, projectID, tenantID, tryName).Scan(&exists); err != nil {
			return 0, "", "", err
		}
		if !exists {
			break
		}
		tryName = fmt.Sprintf("%s Copy (%d)", srcName, n)
	}

	// Pick a free code by PRE-SCANNING for collisions on (project_id, code),
	// same as the name dedup above. We must NOT rely on catching the unique
	// 23505 from a failed INSERT and retrying: inside a transaction the first
	// failed statement aborts the WHOLE tx ("current transaction is aborted,
	// commands ignored until end of transaction block"), so a retry INSERT
	// would always fail. That bug surfaced as "Failed to create duplicate
	// building" on the 2nd+ duplicate of the same source (its "<code>-COPY"
	// already existed). A SELECT EXISTS check doesn't error, so it's safe.
	baseCode := strings.TrimSpace(srcCode)
	if baseCode == "" {
		baseCode = "BUILDING"
	}
	baseCode = baseCode + "-COPY"
	tryCode := baseCode
	for n := 2; n <= 200; n++ {
		var exists bool
		if err = tx.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM construction_buildings
				WHERE project_id = $1 AND tenant_id = $2 AND code = $3
			)
		`, projectID, tenantID, tryCode).Scan(&exists); err != nil {
			return 0, "", "", err
		}
		if !exists {
			break
		}
		tryCode = fmt.Sprintf("%s-%d", baseCode, n)
	}

	const query = `
		INSERT INTO construction_buildings (
			tenant_id, project_id, code, name, description,
			building_type, building_purpose, floors_count, floors_underground,
			total_area, living_area, non_living_area,
			apartments_count, commercial_units_count, parking_spots,
			estimated_cost, currency,
			planned_start_date, planned_end_date,
			status, sort_order, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
		          $10, $11, $12, $13, $14, $15,
		          $16, COALESCE($17, 'UZS'),
		          $18, $19,
		          'draft', $20, NOW(), NOW())
		RETURNING id
	`
	if err = tx.QueryRow(query,
		tenantID, projectID, tryCode, tryName, description,
		buildingType, buildingPurpose, floorsCount, floorsUnderground,
		totalArea, livingArea, nonLivingArea,
		apartmentsCount, commercialUnitsCount, parkingSpots,
		estimatedCost, currency,
		plannedStart, plannedEnd,
		sortOrder,
	).Scan(&newID); err != nil {
		return 0, "", "", err
	}
	return newID, tryName, tryCode, nil
}
