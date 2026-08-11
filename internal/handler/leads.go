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
// LEAD HANDLERS
// =====================================================

// ListLeads returns a paginated list of leads
func (h *Handler) ListLeads(c *gin.Context) {
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
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	status := c.Query("status")
	source := c.Query("source")
	assignedTo := c.Query("assigned_to")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	stageID := c.Query("stage_id")
	pipelineID := c.Query("pipeline_id")
	responsibleID := c.Query("responsible_employee_id")
	openOnly := c.Query("open") == "1" || c.Query("open") == "true"

	// Build query
	baseQuery := `
		SELECT l.id, l.tenant_id, l.contact_name, l.company_name,
			   l.email, l.phone, l.status, l.source, l.notes,
			   l.expected_value, COALESCE(l.currency, 'UZS'),
			   l.pipeline_id, l.stage_id, ps.code, COALESCE(ps.custom_name, ps.name),
			   l.responsible_employee_id, TRIM(e.first_name || ' ' || e.last_name),
			   l.partner_id, ct.name,
			   l.lost_reason_id, lr.name, l.lost_note,
			   l.won_at, l.lost_at, l.last_activity_at,
			   l.assigned_to, l.converted_to,
			   l.converted_at, l.created_at, l.updated_at,
			   u.first_name || ' ' || u.last_name as assigned_to_name,
			   COALESCE(tk.open_tasks, 0),
			   al.modifier_name, al.modified_at
		FROM leads l
		LEFT JOIN users u ON l.assigned_to = u.id
		LEFT JOIN pipeline_stages ps ON ps.id = l.stage_id
		LEFT JOIN employees e ON e.id = l.responsible_employee_id
		LEFT JOIN contacts ct ON ct.id = l.partner_id
		LEFT JOIN lost_reasons lr ON lr.id = l.lost_reason_id
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS open_tasks
			FROM task_links tl
			JOIN tasks t ON t.id = tl.task_id
			WHERE tl.linked_module = 'crm_lead' AND tl.linked_id = l.id::text
			  AND t.completed_at IS NULL AND t.archived_at IS NULL
		) tk ON true
		LEFT JOIN LATERAL (
			SELECT au.first_name || ' ' || au.last_name as modifier_name, a.created_at as modified_at
			FROM audit_logs a
			LEFT JOIN users au ON a.user_id = au.id
			WHERE a.entity_type = 'lead' AND a.entity_id = l.id AND a.action = 'update'
			ORDER BY a.created_at DESC
			LIMIT 1
		) al ON true
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM leads l WHERE l.tenant_id = $1 AND l.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Strict org filter: leads belong to one company and only that
	// company's teammates see them. CreateLead/ConvertLead always
	// populate organization_id (with a primary-org fallback when no
	// X-Organization-ID header is supplied), and a one-time
	// backfill migration handles any legacy NULL-org rows. So a
	// strict equality filter here can't leave leads invisible.
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.status = $%d", argCount)
		args = append(args, status)
	}

	if source != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.source = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.source = $%d", argCount)
		args = append(args, source)
	}

	if assignedTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.assigned_to = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.assigned_to = $%d", argCount)
		args = append(args, assignedTo)
	}

	if stageID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.stage_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.stage_id = $%d", argCount)
		args = append(args, stageID)
	}

	if pipelineID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.pipeline_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.pipeline_id = $%d", argCount)
		args = append(args, pipelineID)
	}

	if responsibleID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.responsible_employee_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.responsible_employee_id = $%d", argCount)
		args = append(args, responsibleID)
	}

	if openOnly {
		baseQuery += " AND l.won_at IS NULL AND l.lost_at IS NULL"
		countQuery += " AND l.won_at IS NULL AND l.lost_at IS NULL"
	}

	if dateFrom != "" {
		if t, err := time.Parse(time.RFC3339, dateFrom); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND l.created_at >= $%d", argCount)
			countQuery += fmt.Sprintf(" AND l.created_at >= $%d", argCount)
			args = append(args, t)
		}
	}

	if dateTo != "" {
		if t, err := time.Parse(time.RFC3339, dateTo); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND l.created_at <= $%d", argCount)
			countQuery += fmt.Sprintf(" AND l.created_at <= $%d", argCount)
			args = append(args, t)
		}
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (l.contact_name ILIKE $%d OR l.company_name ILIKE $%d OR l.email ILIKE $%d OR l.phone ILIKE $%d)", argCount, argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count leads", "error", err)
		response.InternalError(c, "Failed to list leads")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY l.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list leads", "error", err)
		response.InternalError(c, "Failed to list leads")
		return
	}
	defer rows.Close()

	leads := make([]*entity.LeadResponse, 0)
	for rows.Next() {
		var l entity.Lead
		var companyName, phone, notes sql.NullString
		var expectedValue sql.NullFloat64
		var currency string
		var pipelineID, leadStageID, responsibleID, partnerID, lostReasonID *uuid.UUID
		var stageCode, stageName, responsibleName, partnerName, lostReasonName, lostNote sql.NullString
		var wonAt, lostAt, lastActivityAt sql.NullTime
		var assignedTo, convertedTo sql.NullString
		var convertedAt sql.NullTime
		var assignedToName sql.NullString
		var openTaskCount int
		var lastModifiedBy sql.NullString
		var lastModifiedAt sql.NullTime

		err := rows.Scan(
			&l.ID, &l.TenantID, &l.ContactName, &companyName,
			&l.Email, &phone, &l.Status, &l.Source, &notes,
			&expectedValue, &currency,
			&pipelineID, &leadStageID, &stageCode, &stageName,
			&responsibleID, &responsibleName,
			&partnerID, &partnerName,
			&lostReasonID, &lostReasonName, &lostNote,
			&wonAt, &lostAt, &lastActivityAt,
			&assignedTo, &convertedTo,
			&convertedAt, &l.CreatedAt, &l.UpdatedAt,
			&assignedToName,
			&openTaskCount,
			&lastModifiedBy, &lastModifiedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan lead", "error", err)
			continue
		}

		resp := &entity.LeadResponse{
			ID:                    l.ID,
			ContactName:           l.ContactName,
			Email:                 l.Email,
			Status:                l.Status,
			Source:                l.Source,
			Currency:              currency,
			PipelineID:            pipelineID,
			StageID:               leadStageID,
			ResponsibleEmployeeID: responsibleID,
			PartnerID:             partnerID,
			LostReasonID:          lostReasonID,
			OpenTaskCount:         openTaskCount,
			CreatedAt:             l.CreatedAt,
			UpdatedAt:             l.UpdatedAt,
		}

		if companyName.Valid {
			resp.CompanyName = &companyName.String
		}
		if phone.Valid {
			resp.Phone = &phone.String
		}
		if notes.Valid {
			resp.Notes = &notes.String
		}
		if expectedValue.Valid {
			resp.ExpectedValue = &expectedValue.Float64
		}
		if stageCode.Valid {
			resp.StageCode = &stageCode.String
		}
		if stageName.Valid {
			resp.StageName = &stageName.String
		}
		if responsibleName.Valid && responsibleName.String != "" {
			resp.ResponsibleName = &responsibleName.String
		}
		if partnerName.Valid {
			resp.PartnerName = &partnerName.String
		}
		if lostReasonName.Valid {
			resp.LostReasonName = &lostReasonName.String
		}
		if lostNote.Valid {
			resp.LostNote = &lostNote.String
		}
		if wonAt.Valid {
			t := wonAt.Time
			resp.WonAt = &t
		}
		if lostAt.Valid {
			t := lostAt.Time
			resp.LostAt = &t
		}
		if lastActivityAt.Valid {
			t := lastActivityAt.Time
			resp.LastActivityAt = &t
		}
		if assignedTo.Valid {
			aid, _ := uuid.Parse(assignedTo.String)
			resp.AssignedTo = &aid
		}
		if assignedToName.Valid {
			resp.AssignedToName = &assignedToName.String
		}
		if lastModifiedBy.Valid {
			resp.LastModifiedBy = &lastModifiedBy.String
		}
		if lastModifiedAt.Valid {
			t := lastModifiedAt.Time
			resp.LastModifiedAt = &t
		}

		leads = append(leads, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, leads, pagination)
}

// getDuplicateDetectionSettings reads CRM duplicate detection settings from tenant_settings
func (h *Handler) getDuplicateDetectionSettings(tenantID uuid.UUID) (enabled bool, checkFields []string) {
	var settingsJSON []byte
	err := h.db.QueryRow("SELECT settings FROM tenant_settings WHERE tenant_id = $1", tenantID).Scan(&settingsJSON)
	if err != nil {
		return false, nil
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		return false, nil
	}

	crm, ok := settings["crm"].(map[string]interface{})
	if !ok {
		return false, nil
	}

	dd, ok := crm["duplicate_detection"].(map[string]interface{})
	if !ok {
		return false, nil
	}

	isEnabled, _ := dd["enabled"].(bool)
	if !isEnabled {
		return false, nil
	}

	fields, ok := dd["check_fields"].([]interface{})
	if !ok || len(fields) == 0 {
		return true, []string{"email", "phone"}
	}

	var result []string
	for _, f := range fields {
		if s, ok := f.(string); ok {
			result = append(result, s)
		}
	}
	return true, result
}

// checkLeadDuplicates checks for duplicate leads based on configured fields
func (h *Handler) checkLeadDuplicates(tenantID uuid.UUID, email, phone, companyName string, checkFields []string) []map[string]interface{} {
	var conditions []string
	var args []interface{}
	args = append(args, tenantID)
	argIdx := 1

	for _, field := range checkFields {
		switch field {
		case "email":
			if email != "" {
				argIdx++
				conditions = append(conditions, fmt.Sprintf("LOWER(email) = LOWER($%d)", argIdx))
				args = append(args, email)
			}
		case "phone":
			// normalized match (last 9 digits) — `+998 90 123 45 67` and
			// `901234567` are the same lead; uses idx_leads_phone_digits
			if digits := normalizePhoneDigits(phone); len(digits) >= 7 {
				argIdx++
				conditions = append(conditions, fmt.Sprintf("RIGHT(REGEXP_REPLACE(COALESCE(phone, ''), '[^0-9]', '', 'g'), 9) = $%d", argIdx))
				args = append(args, digits)
			}
		case "company_name":
			if companyName != "" {
				argIdx++
				conditions = append(conditions, fmt.Sprintf("LOWER(company_name) = LOWER($%d)", argIdx))
				args = append(args, companyName)
			}
		}
	}

	if len(conditions) == 0 {
		return nil
	}

	query := fmt.Sprintf(`
		SELECT id, contact_name, COALESCE(email, '') as email, COALESCE(phone, '') as phone, COALESCE(company_name, '') as company_name
		FROM leads
		WHERE tenant_id = $1 AND deleted_at IS NULL AND (%s)
		LIMIT 5
	`, strings.Join(conditions, " OR "))

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to check lead duplicates", "error", err)
		return nil
	}
	defer rows.Close()

	var duplicates []map[string]interface{}
	for rows.Next() {
		var id, name, eMail, ph, company string
		if err := rows.Scan(&id, &name, &eMail, &ph, &company); err != nil {
			continue
		}

		// Determine which fields matched
		var matchedFields []string
		for _, field := range checkFields {
			switch field {
			case "email":
				if email != "" && strings.EqualFold(eMail, email) {
					matchedFields = append(matchedFields, "email")
				}
			case "phone":
				if d := normalizePhoneDigits(phone); len(d) >= 7 && d == normalizePhoneDigits(ph) {
					matchedFields = append(matchedFields, "phone")
				}
			case "company_name":
				if companyName != "" && strings.EqualFold(company, companyName) {
					matchedFields = append(matchedFields, "company_name")
				}
			}
		}

		duplicates = append(duplicates, map[string]interface{}{
			"id":             id,
			"name":           name,
			"email":          eMail,
			"phone":          ph,
			"company_name":   company,
			"matched_fields": matchedFields,
		})
	}

	return duplicates
}

// checkContactDuplicates checks for duplicate contacts based on configured fields
func (h *Handler) checkContactDuplicates(tenantID uuid.UUID, email, phone, name string, checkFields []string) []map[string]interface{} {
	var conditions []string
	var args []interface{}
	args = append(args, tenantID)
	argIdx := 1

	for _, field := range checkFields {
		switch field {
		case "email":
			if email != "" {
				argIdx++
				conditions = append(conditions, fmt.Sprintf("LOWER(email) = LOWER($%d)", argIdx))
				args = append(args, email)
			}
		case "phone":
			if phone != "" {
				argIdx++
				conditions = append(conditions, fmt.Sprintf("phone = $%d", argIdx))
				args = append(args, phone)
			}
		case "company_name":
			if name != "" {
				argIdx++
				conditions = append(conditions, fmt.Sprintf("LOWER(name) = LOWER($%d)", argIdx))
				args = append(args, name)
			}
		}
	}

	if len(conditions) == 0 {
		return nil
	}

	query := fmt.Sprintf(`
		SELECT id, name, COALESCE(email, '') as email, COALESCE(phone, '') as phone
		FROM contacts
		WHERE tenant_id = $1 AND deleted_at IS NULL AND (%s)
		LIMIT 5
	`, strings.Join(conditions, " OR "))

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to check contact duplicates", "error", err)
		return nil
	}
	defer rows.Close()

	var duplicates []map[string]interface{}
	for rows.Next() {
		var id, cName, eMail, ph string
		if err := rows.Scan(&id, &cName, &eMail, &ph); err != nil {
			continue
		}

		var matchedFields []string
		for _, field := range checkFields {
			switch field {
			case "email":
				if email != "" && strings.EqualFold(eMail, email) {
					matchedFields = append(matchedFields, "email")
				}
			case "phone":
				if phone != "" && ph == phone {
					matchedFields = append(matchedFields, "phone")
				}
			case "company_name":
				if name != "" && strings.EqualFold(cName, name) {
					matchedFields = append(matchedFields, "company_name")
				}
			}
		}

		duplicates = append(duplicates, map[string]interface{}{
			"id":             id,
			"name":           cName,
			"email":          eMail,
			"phone":          ph,
			"matched_fields": matchedFields,
		})
	}

	return duplicates
}

// CreateLead creates a new lead
func (h *Handler) CreateLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateLeadInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse optional UUID for assigned_to; default to current user
	var assignedTo *uuid.UUID
	if input.AssignedTo != "" {
		aid, err := uuid.Parse(input.AssignedTo)
		if err == nil {
			assignedTo = &aid
		}
	}
	if assignedTo == nil && userID != uuid.Nil {
		assignedTo = &userID
	}

	id := uuid.New()
	now := time.Now()

	// Get organization ID. Fall back to the creator's primary org via
	// employees → employee_organizations when the request didn't carry
	// an active-org header (admin without a switched company) —
	// otherwise the lead (or converted record) ends up with
	// organization_id = NULL and is visible only to admins and the
	// creator, confusing teammates in the same company.
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	} else if userID != uuid.Nil {
		var fallbackOrg uuid.UUID
		if err := h.db.QueryRow(`
			SELECT eo.organization_id
			FROM employee_organizations eo
			JOIN employees e ON e.id = eo.employee_id
			WHERE e.user_id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL
			ORDER BY eo.is_primary DESC, eo.created_at ASC
			LIMIT 1
		`, userID, tenantID).Scan(&fallbackOrg); err == nil && fallbackOrg != uuid.Nil {
			orgIDPtr = &fallbackOrg
		}
	}

	source := entity.LeadSourceWebsite
	if input.Source != "" {
		source = input.Source
	}

	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if currency == "" {
		currency = "UZS"
	}

	// Check for duplicates before creating (normalized phone — see
	// checkLeadDuplicates)
	if enabled, checkFields := h.getDuplicateDetectionSettings(tenantID); enabled {
		duplicates := h.checkLeadDuplicates(tenantID, input.Email, input.Phone, input.CompanyName, checkFields)
		if len(duplicates) > 0 {
			response.ConflictWithData(c, "DUPLICATE_DETECTED", "Potential duplicate lead(s) found", map[string]interface{}{
				"duplicates": duplicates,
			})
			return
		}
	}

	// Resolve the stage: explicit stage_id, else the first open stage of the
	// org's default (or requested) pipeline. status mirrors the stage code.
	var stagePtr, pipelinePtr *uuid.UUID
	status := entity.LeadStatusNew
	if input.StageID != "" {
		if sid, err := uuid.Parse(input.StageID); err == nil {
			var code string
			var pid *uuid.UUID
			if err := h.db.QueryRow(`
				SELECT code, pipeline_id FROM pipeline_stages
				WHERE id = $1 AND tenant_id = $2 AND pipeline_type = 'lead'
			`, sid, tenantID).Scan(&code, &pid); err == nil {
				stagePtr = &sid
				pipelinePtr = pid
				status = entity.LeadStatus(code)
			}
		}
	}
	if stagePtr == nil {
		q := `
			SELECT ps.id, ps.code, ps.pipeline_id
			FROM pipeline_stages ps
			JOIN pipelines p ON p.id = ps.pipeline_id
			WHERE ps.tenant_id = $1 AND ps.pipeline_type = 'lead' AND ps.is_active
			  AND NOT ps.is_won AND NOT ps.is_lost
			  AND p.organization_id IS NOT DISTINCT FROM $2`
		args := []interface{}{tenantID, orgIDPtr}
		if input.PipelineID != "" {
			if pid, err := uuid.Parse(input.PipelineID); err == nil {
				q += " AND p.id = $3"
				args = append(args, pid)
			}
		} else {
			q += " AND p.is_default"
		}
		q += " ORDER BY ps.sequence LIMIT 1"
		var sid uuid.UUID
		var code string
		var pid *uuid.UUID
		if err := h.db.QueryRow(q, args...).Scan(&sid, &code, &pid); err == nil {
			stagePtr = &sid
			pipelinePtr = pid
			status = entity.LeadStatus(code)
		} else if input.Status != "" {
			status = input.Status
		}
	}

	// Responsible: explicit employee, else the creator's employee record.
	var responsiblePtr *uuid.UUID
	if input.ResponsibleEmployeeID != "" {
		if eid, err := uuid.Parse(input.ResponsibleEmployeeID); err == nil {
			responsiblePtr = &eid
		}
	}
	if responsiblePtr == nil && userID != uuid.Nil {
		var eid uuid.UUID
		if err := h.db.QueryRow(`SELECT employee_id FROM users WHERE id = $1 AND employee_id IS NOT NULL`, userID).Scan(&eid); err == nil {
			responsiblePtr = &eid
		}
	}

	// Prepare optional strings
	var companyName, phone, notes *string
	if input.CompanyName != "" {
		companyName = &input.CompanyName
	}
	if input.Phone != "" {
		phone = &input.Phone
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	query := `
		INSERT INTO leads (
			id, tenant_id, organization_id, contact_name, company_name,
			email, phone, status, source, notes,
			expected_value, currency, pipeline_id, stage_id, responsible_employee_id,
			assigned_to, last_activity_at,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, input.ContactName, companyName,
		input.Email, phone, status, source, notes,
		input.ExpectedValue, currency, pipelinePtr, stagePtr, responsiblePtr,
		assignedTo, now,
		userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create lead", "error", err)
		response.InternalError(c, "Failed to create lead")
		return
	}

	if stagePtr != nil {
		h.recordLeadStageChange(h.db, tenantID, id, nil, stagePtr, userID)
	}

	resp := &entity.LeadResponse{
		ID:                    id,
		ContactName:           input.ContactName,
		CompanyName:           companyName,
		Email:                 input.Email,
		Phone:                 phone,
		Status:                status,
		Source:                source,
		Notes:                 notes,
		ExpectedValue:         input.ExpectedValue,
		Currency:              currency,
		PipelineID:            pipelinePtr,
		StageID:               stagePtr,
		ResponsibleEmployeeID: responsiblePtr,
		AssignedTo:            assignedTo,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	// Trigger workflow rules for new lead
	h.EmitWorkflowEvent(tenantID, "lead.created", map[string]interface{}{
		"record_id":      id.String(),
		"contact_name":   input.ContactName,
		"company_name":   input.CompanyName,
		"email":          input.Email,
		"source":         source,
		"expected_value": input.ExpectedValue,
	})

	response.Created(c, resp)
}

// GetLead returns a single lead by ID
func (h *Handler) GetLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}

	query := `
		SELECT l.id, l.tenant_id, l.contact_name, l.company_name,
			   l.email, l.phone, l.status, l.source, l.notes,
			   l.expected_value, COALESCE(l.currency, 'UZS'),
			   l.pipeline_id, l.stage_id, ps.code, COALESCE(ps.custom_name, ps.name),
			   l.responsible_employee_id, TRIM(e.first_name || ' ' || e.last_name),
			   l.partner_id, ct.name,
			   l.lost_reason_id, lr.name, l.lost_note,
			   l.won_at, l.lost_at, l.last_activity_at,
			   l.assigned_to, l.converted_to,
			   l.converted_at, l.created_at, l.updated_at,
			   u.first_name || ' ' || u.last_name as assigned_to_name,
			   COALESCE((SELECT COUNT(*) FROM task_links tl JOIN tasks t ON t.id = tl.task_id
			             WHERE tl.linked_module = 'crm_lead' AND tl.linked_id = l.id::text
			               AND t.completed_at IS NULL AND t.archived_at IS NULL), 0)
		FROM leads l
		LEFT JOIN users u ON l.assigned_to = u.id
		LEFT JOIN pipeline_stages ps ON ps.id = l.stage_id
		LEFT JOIN employees e ON e.id = l.responsible_employee_id
		LEFT JOIN contacts ct ON ct.id = l.partner_id
		LEFT JOIN lost_reasons lr ON lr.id = l.lost_reason_id
		WHERE l.id = $1 AND l.tenant_id = $2 AND l.deleted_at IS NULL
	`

	var l entity.Lead
	var companyName, phone, notes sql.NullString
	var expectedValue sql.NullFloat64
	var currency string
	var pipelineID, leadStageID, responsibleID, partnerID, lostReasonID *uuid.UUID
	var stageCode, stageName, responsibleName, partnerName, lostReasonName, lostNote sql.NullString
	var wonAt, lostAt, lastActivityAt sql.NullTime
	var assignedTo, convertedTo sql.NullString
	var convertedAt sql.NullTime
	var assignedToName sql.NullString
	var openTaskCount int

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&l.ID, &l.TenantID, &l.ContactName, &companyName,
		&l.Email, &phone, &l.Status, &l.Source, &notes,
		&expectedValue, &currency,
		&pipelineID, &leadStageID, &stageCode, &stageName,
		&responsibleID, &responsibleName,
		&partnerID, &partnerName,
		&lostReasonID, &lostReasonName, &lostNote,
		&wonAt, &lostAt, &lastActivityAt,
		&assignedTo, &convertedTo,
		&convertedAt, &l.CreatedAt, &l.UpdatedAt,
		&assignedToName,
		&openTaskCount,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Lead")
		return
	}
	if err != nil {
		h.log.Error("Failed to get lead", "error", err)
		response.InternalError(c, "Failed to get lead")
		return
	}

	resp := &entity.LeadResponse{
		ID:                    l.ID,
		ContactName:           l.ContactName,
		Email:                 l.Email,
		Status:                l.Status,
		Source:                l.Source,
		Currency:              currency,
		PipelineID:            pipelineID,
		StageID:               leadStageID,
		ResponsibleEmployeeID: responsibleID,
		PartnerID:             partnerID,
		LostReasonID:          lostReasonID,
		OpenTaskCount:         openTaskCount,
		CreatedAt:             l.CreatedAt,
		UpdatedAt:             l.UpdatedAt,
	}

	if companyName.Valid {
		resp.CompanyName = &companyName.String
	}
	if phone.Valid {
		resp.Phone = &phone.String
	}
	if notes.Valid {
		resp.Notes = &notes.String
	}
	if expectedValue.Valid {
		resp.ExpectedValue = &expectedValue.Float64
	}
	if stageCode.Valid {
		resp.StageCode = &stageCode.String
	}
	if stageName.Valid {
		resp.StageName = &stageName.String
	}
	if responsibleName.Valid && responsibleName.String != "" {
		resp.ResponsibleName = &responsibleName.String
	}
	if partnerName.Valid {
		resp.PartnerName = &partnerName.String
	}
	if lostReasonName.Valid {
		resp.LostReasonName = &lostReasonName.String
	}
	if lostNote.Valid {
		resp.LostNote = &lostNote.String
	}
	if wonAt.Valid {
		t := wonAt.Time
		resp.WonAt = &t
	}
	if lostAt.Valid {
		t := lostAt.Time
		resp.LostAt = &t
	}
	if lastActivityAt.Valid {
		t := lastActivityAt.Time
		resp.LastActivityAt = &t
	}
	if assignedTo.Valid {
		aid, _ := uuid.Parse(assignedTo.String)
		resp.AssignedTo = &aid
	}
	if assignedToName.Valid {
		resp.AssignedToName = &assignedToName.String
	}

	response.Success(c, resp)
}

// UpdateLead updates an existing lead
func (h *Handler) UpdateLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}

	var input entity.UpdateLeadInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Fetch old values for audit log
	var oldContactName, oldCompanyName, oldEmail, oldPhone, oldStatus, oldSource, oldNotes sql.NullString
	var oldAssignedTo sql.NullString
	var oldExpectedValue sql.NullFloat64
	var oldStageID *uuid.UUID
	h.db.QueryRow(`
		SELECT contact_name, company_name, email, phone, status, source, notes, assigned_to, expected_value, stage_id
		FROM leads WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&oldContactName, &oldCompanyName, &oldEmail, &oldPhone, &oldStatus, &oldSource, &oldNotes, &oldAssignedTo, &oldExpectedValue, &oldStageID)

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.ContactName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("contact_name = $%d", argCount))
		args = append(args, *input.ContactName)
	}
	if input.CompanyName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("company_name = $%d", argCount))
		args = append(args, *input.CompanyName)
	}
	if input.Email != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("email = $%d", argCount))
		args = append(args, *input.Email)
	}
	if input.Phone != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("phone = $%d", argCount))
		args = append(args, *input.Phone)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
		// keep stage_id in sync when a legacy client writes status directly
		// (the board uses POST /leads/:id/move; this covers old consumers)
		var sid uuid.UUID
		if err := h.db.QueryRow(`
			SELECT ps.id FROM pipeline_stages ps
			JOIN leads l ON l.tenant_id = ps.tenant_id
			WHERE l.id = $1 AND ps.tenant_id = $2 AND ps.pipeline_type = 'lead'
			  AND ps.code = $3
			  AND (ps.pipeline_id = l.pipeline_id OR l.pipeline_id IS NULL)
			ORDER BY (ps.pipeline_id = l.pipeline_id) DESC NULLS LAST LIMIT 1
		`, id, tenantID, string(*input.Status)).Scan(&sid); err == nil {
			argCount++
			updates = append(updates, fmt.Sprintf("stage_id = $%d", argCount))
			args = append(args, sid)
		}
	}
	if input.Source != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("source = $%d", argCount))
		args = append(args, *input.Source)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.ExpectedValue != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("expected_value = $%d", argCount))
		args = append(args, *input.ExpectedValue)
	}
	if input.Currency != nil && *input.Currency != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("currency = $%d", argCount))
		args = append(args, strings.ToUpper(strings.TrimSpace(*input.Currency)))
	}
	if input.ResponsibleEmployeeID != nil {
		argCount++
		if *input.ResponsibleEmployeeID == "" {
			updates = append(updates, fmt.Sprintf("responsible_employee_id = $%d", argCount))
			args = append(args, nil)
		} else if eid, err := uuid.Parse(*input.ResponsibleEmployeeID); err == nil {
			updates = append(updates, fmt.Sprintf("responsible_employee_id = $%d", argCount))
			args = append(args, eid)
		} else {
			argCount--
		}
	}
	if input.AssignedTo != nil {
		argCount++
		if *input.AssignedTo == "" {
			updates = append(updates, fmt.Sprintf("assigned_to = $%d", argCount))
			args = append(args, nil)
		} else {
			aid, err := uuid.Parse(*input.AssignedTo)
			if err == nil {
				updates = append(updates, fmt.Sprintf("assigned_to = $%d", argCount))
				args = append(args, aid)
			}
		}
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Any edit counts as activity (rotting badge & stale scanner read this)
	updates = append(updates, "last_activity_at = NOW()")

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE conditions
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE leads SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update lead", "error", err)
		response.InternalError(c, "Failed to update lead")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Lead")
		return
	}

	// Trigger workflow rules on stage/status change
	if input.Status != nil && oldStatus.String != string(*input.Status) {
		contactName := oldContactName.String
		if input.ContactName != nil {
			contactName = *input.ContactName
		}
		h.EmitWorkflowEvent(tenantID, "lead.status_changed", map[string]interface{}{
			"record_id":    id.String(),
			"contact_name": contactName,
			"old_status":   oldStatus.String,
			"new_status":   string(*input.Status),
		})
		// mirror into stage history for funnel/cycle reports
		var newStageID *uuid.UUID
		h.db.QueryRow(`SELECT stage_id FROM leads WHERE id = $1`, id).Scan(&newStageID)
		if newStageID != nil && (oldStageID == nil || *oldStageID != *newStageID) {
			actorID, _ := middleware.GetUserID(c)
			h.recordLeadStageChange(h.db, tenantID, id, oldStageID, newStageID, actorID)
		}
	}

	// Write audit log for changed fields
	oldValues := map[string]interface{}{}
	newValues := map[string]interface{}{}

	if input.ContactName != nil && oldContactName.String != *input.ContactName {
		oldValues["contact_name"] = oldContactName.String
		newValues["contact_name"] = *input.ContactName
	}
	if input.CompanyName != nil && oldCompanyName.String != *input.CompanyName {
		oldValues["company_name"] = oldCompanyName.String
		newValues["company_name"] = *input.CompanyName
	}
	if input.Email != nil && oldEmail.String != *input.Email {
		oldValues["email"] = oldEmail.String
		newValues["email"] = *input.Email
	}
	if input.Phone != nil && oldPhone.String != *input.Phone {
		oldValues["phone"] = oldPhone.String
		newValues["phone"] = *input.Phone
	}
	if input.Status != nil && oldStatus.String != string(*input.Status) {
		oldValues["status"] = oldStatus.String
		newValues["status"] = *input.Status
	}
	if input.Source != nil && oldSource.String != string(*input.Source) {
		oldValues["source"] = oldSource.String
		newValues["source"] = *input.Source
	}
	if input.Notes != nil && oldNotes.String != *input.Notes {
		oldValues["notes"] = oldNotes.String
		newValues["notes"] = *input.Notes
	}
	if input.AssignedTo != nil && oldAssignedTo.String != *input.AssignedTo {
		oldValues["assigned_to"] = oldAssignedTo.String
		newValues["assigned_to"] = *input.AssignedTo
	}
	if input.ExpectedValue != nil {
		oldVal := float64(0)
		if oldExpectedValue.Valid {
			oldVal = oldExpectedValue.Float64
		}
		if oldVal != *input.ExpectedValue {
			oldValues["expected_value"] = oldVal
			newValues["expected_value"] = *input.ExpectedValue
		}
	}

	if len(oldValues) > 0 {
		userID, _ := middleware.GetUserID(c)
		oldJSON, _ := json.Marshal(oldValues)
		newJSON, _ := json.Marshal(newValues)
		if _, execErr := h.db.Exec(`
			INSERT INTO audit_logs (id, tenant_id, user_id, action, entity_type, entity_id, old_values, new_values, created_at)
			VALUES ($1, $2, $3, 'update', 'lead', $4, $5, $6, $7)
		`, uuid.New(), tenantID, userID, id, oldJSON, newJSON, time.Now()); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT audit_logs", "error", execErr)
		}
	}

	response.Success(c, gin.H{"message": "Lead updated successfully"})
}

// DeleteLead soft deletes a lead
func (h *Handler) DeleteLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}

	query := `
		UPDATE leads
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete lead", "error", err)
		response.InternalError(c, "Failed to delete lead")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Lead")
		return
	}

	response.Success(c, gin.H{"message": "Lead deleted successfully"})
}

// GetLeadStats returns aggregated lead statistics
func (h *Handler) GetLeadStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT
			COUNT(*) as total_leads,
			COUNT(*) FILTER (WHERE won_at IS NULL AND lost_at IS NULL) as open_leads,
			COALESCE(SUM(expected_value) FILTER (WHERE won_at IS NULL AND lost_at IS NULL), 0) as open_value,
			COUNT(*) FILTER (WHERE won_at IS NOT NULL) as won_leads,
			COUNT(*) FILTER (WHERE lost_at IS NOT NULL) as lost_leads,
			COUNT(*) FILTER (WHERE won_at >= date_trunc('month', CURRENT_DATE)) as won_this_month,
			COALESCE(SUM(expected_value) FILTER (WHERE won_at >= date_trunc('month', CURRENT_DATE)), 0) as won_value_month,
			COALESCE(
				COUNT(*) FILTER (WHERE won_at IS NOT NULL)::float /
				NULLIF(COUNT(*) FILTER (WHERE won_at IS NOT NULL OR lost_at IS NOT NULL), 0) * 100,
				0
			) as conversion_rate,
			COALESCE(
				COUNT(*) FILTER (WHERE won_at >= date_trunc('month', CURRENT_DATE))::float /
				NULLIF(COUNT(*) FILTER (WHERE won_at >= date_trunc('month', CURRENT_DATE)
				                            OR lost_at >= date_trunc('month', CURRENT_DATE)), 0) * 100,
				0
			) as conversion_month,
			COALESCE(AVG(expected_value) FILTER (WHERE won_at IS NOT NULL AND expected_value > 0), 0) as avg_deal_size,
			COALESCE(SUM(expected_value) FILTER (WHERE lost_at IS NULL), 0) as total_value,
			COUNT(*) FILTER (WHERE status = 'new') as new_leads,
			COUNT(*) FILTER (WHERE status = 'contacted') as contacted_leads,
			COUNT(*) FILTER (WHERE status = 'in_progress') as in_progress_leads,
			COUNT(*) FILTER (WHERE status = 'qualified') as qualified_leads
		FROM leads
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND organization_id = $2"
		args = append(args, orgID)
	}

	var stats entity.LeadStats
	err := h.db.QueryRow(query, args...).Scan(
		&stats.TotalLeads,
		&stats.OpenLeads,
		&stats.OpenValue,
		&stats.WonLeads,
		&stats.LostLeads,
		&stats.WonThisMonth,
		&stats.WonValueMonth,
		&stats.ConversionRate,
		&stats.ConversionMonth,
		&stats.AvgDealSize,
		&stats.TotalValue,
		&stats.NewLeads,
		&stats.ContactedLeads,
		&stats.InProgressLeads,
		&stats.QualifiedLeads,
	)

	if err != nil {
		h.log.Error("Failed to get lead stats", "error", err)
		response.InternalError(c, "Failed to get lead statistics")
		return
	}

	response.Success(c, stats)
}

// ConvertLeadInput represents input for converting a lead
type ConvertLeadInput struct {
	CreateContact     bool    `json:"create_contact"`
	CreateOpportunity bool    `json:"create_opportunity"`
	ContactType       string  `json:"contact_type,omitempty"` // customer, vendor, partner
	OpportunityName   string  `json:"opportunity_name,omitempty"`
	ExpectedRevenue   float64 `json:"expected_revenue,omitempty"`
	ExpectedCloseDate string  `json:"expected_close_date,omitempty"`
}

// ConvertLead converts a lead to a contact and/or opportunity
func (h *Handler) ConvertLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}

	var input ConvertLeadInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if !input.CreateContact && !input.CreateOpportunity {
		response.BadRequest(c, "At least one of create_contact or create_opportunity must be true")
		return
	}

	// Get the lead
	leadQuery := `
		SELECT id, contact_name, company_name, email, phone, expected_value, assigned_to
		FROM leads
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND converted_to IS NULL
	`

	var lead struct {
		ID            uuid.UUID
		ContactName   string
		CompanyName   sql.NullString
		Email         string
		Phone         sql.NullString
		ExpectedValue sql.NullFloat64
		AssignedTo    sql.NullString
	}

	err = h.db.QueryRow(leadQuery, id, tenantID).Scan(
		&lead.ID, &lead.ContactName, &lead.CompanyName, &lead.Email,
		&lead.Phone, &lead.ExpectedValue, &lead.AssignedTo,
	)

	if err == sql.ErrNoRows {
		response.BadRequest(c, "Lead not found or already converted")
		return
	}
	if err != nil {
		h.log.Error("Failed to get lead for conversion", "error", err)
		response.InternalError(c, "Failed to convert lead")
		return
	}

	now := time.Now()

	// Get organization ID. Fall back to the creator's primary org via
	// employees → employee_organizations when the request didn't carry
	// an active-org header (admin without a switched company) —
	// otherwise the lead (or converted record) ends up with
	// organization_id = NULL and is visible only to admins and the
	// creator, confusing teammates in the same company.
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	} else if userID != uuid.Nil {
		var fallbackOrg uuid.UUID
		if err := h.db.QueryRow(`
			SELECT eo.organization_id
			FROM employee_organizations eo
			JOIN employees e ON e.id = eo.employee_id
			WHERE e.user_id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL
			ORDER BY eo.is_primary DESC, eo.created_at ASC
			LIMIT 1
		`, userID, tenantID).Scan(&fallbackOrg); err == nil && fallbackOrg != uuid.Nil {
			orgIDPtr = &fallbackOrg
		}
	}

	result := map[string]interface{}{
		"lead_id": id,
		"message": "Lead converted successfully",
	}

	var contactID *uuid.UUID
	var opportunityID *uuid.UUID

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalError(c, "Failed to convert lead")
		return
	}
	defer tx.Rollback()

	// Create contact if requested
	if input.CreateContact {
		newContactID := uuid.New()
		contactType := "customer"
		if input.ContactType != "" {
			contactType = input.ContactType
		}

		// Generate contact code
		contactCode := fmt.Sprintf("C-%s", newContactID.String()[:8])

		// Use company name or contact name as the contact name
		contactName := lead.ContactName
		if lead.CompanyName.Valid && lead.CompanyName.String != "" {
			contactName = lead.CompanyName.String
		}

		contactQuery := `
			INSERT INTO contacts (
				id, tenant_id, organization_id, type, code, name, email, phone,
				is_active, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, $9, $10, $11)
			RETURNING id
		`

		var phone *string
		if lead.Phone.Valid {
			phone = &lead.Phone.String
		}

		err = tx.QueryRow(contactQuery,
			newContactID, tenantID, orgIDPtr, contactType, contactCode, contactName,
			lead.Email, phone, userID, now, now,
		).Scan(&newContactID)

		if err != nil {
			h.log.Error("Failed to create contact from lead", "error", err)
			response.InternalError(c, "Failed to create contact")
			return
		}

		contactID = &newContactID
		result["contact_id"] = newContactID

		// If contact has a person name different from company, add as contact person
		if lead.CompanyName.Valid && lead.CompanyName.String != "" && lead.ContactName != "" {
			parts := strings.SplitN(lead.ContactName, " ", 2)
			firstName := parts[0]
			lastName := ""
			if len(parts) > 1 {
				lastName = parts[1]
			}

			personQuery := `
				INSERT INTO contact_persons (
					id, contact_id, first_name, last_name, email, phone, is_primary, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, true, $7, $8)
			`
			_, err = tx.Exec(personQuery, uuid.New(), newContactID, firstName, lastName, lead.Email, phone, now, now)
			if err != nil {
				h.log.Error("Failed to create contact person", "error", err)
				// Don't fail the whole operation for this
			}
		}
	}

	// Create opportunity if requested
	if input.CreateOpportunity {
		newOpportunityID := uuid.New()
		oppCode := fmt.Sprintf("OPP-%s", newOpportunityID.String()[:8])

		oppName := input.OpportunityName
		if oppName == "" {
			if lead.CompanyName.Valid && lead.CompanyName.String != "" {
				oppName = fmt.Sprintf("Opportunity - %s", lead.CompanyName.String)
			} else {
				oppName = fmt.Sprintf("Opportunity - %s", lead.ContactName)
			}
		}

		expectedRevenue := input.ExpectedRevenue
		if expectedRevenue == 0 && lead.ExpectedValue.Valid {
			expectedRevenue = lead.ExpectedValue.Float64
		}

		var expectedCloseDate *time.Time
		if input.ExpectedCloseDate != "" {
			if t, err := time.Parse("2006-01-02", input.ExpectedCloseDate); err == nil {
				expectedCloseDate = &t
			}
		}

		var assignedTo *uuid.UUID
		if lead.AssignedTo.Valid {
			aid, _ := uuid.Parse(lead.AssignedTo.String)
			assignedTo = &aid
		}

		oppQuery := `
			INSERT INTO opportunities (
				id, tenant_id, organization_id, name, code, contact_id, lead_id,
				stage, probability, expected_revenue, currency,
				expected_close_date, source, priority, assigned_to,
				tags, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
			RETURNING id
		`

		// currency follows the lead (UZS-denominated ERP; the old code
		// hardcoded USD)
		var leadCurrency string
		if err := tx.QueryRow(`SELECT COALESCE(currency, 'UZS') FROM leads WHERE id = $1`, id).Scan(&leadCurrency); err != nil || leadCurrency == "" {
			leadCurrency = "UZS"
		}

		err = tx.QueryRow(oppQuery,
			newOpportunityID, tenantID, orgIDPtr, oppName, oppCode, contactID, id,
			"qualification", 10.0, expectedRevenue, leadCurrency,
			expectedCloseDate, "lead_conversion", "medium", assignedTo,
			[]byte("[]"), userID, now, now,
		).Scan(&newOpportunityID)

		if err != nil {
			h.log.Error("Failed to create opportunity from lead", "error", err)
			response.InternalError(c, "Failed to create opportunity")
			return
		}

		opportunityID = &newOpportunityID
		result["opportunity_id"] = newOpportunityID
	}

	// Update lead as converted. converted_to is an FK to contacts(id) —
	// it must stay NULL when only an opportunity was created (the old code
	// wrote the opportunity UUID here, corrupting the reference).
	_ = opportunityID
	updateQuery := `
		UPDATE leads
		SET status = 'qualified', converted_to = $1, partner_id = COALESCE(partner_id, $1),
		    converted_at = $2, last_activity_at = $2, updated_at = $2
		WHERE id = $3 AND tenant_id = $4
	`

	_, err = tx.Exec(updateQuery, contactID, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to update lead conversion status", "error", err)
		response.InternalError(c, "Failed to update lead")
		return
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to convert lead")
		return
	}

	response.Success(c, result)
}

// GetLeadAuditLogs returns audit logs for a specific lead
func (h *Handler) GetLeadAuditLogs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT al.id, al.user_id, COALESCE(u.email, '') as user_email,
		       COALESCE(u.first_name || ' ' || u.last_name, u.email, '') as user_name,
		       al.action, al.old_values, al.new_values, al.created_at
		FROM audit_logs al
		LEFT JOIN users u ON al.user_id = u.id
		WHERE al.tenant_id = $1 AND al.entity_type = 'lead' AND al.entity_id = $2
		ORDER BY al.created_at DESC
		LIMIT 50
	`, tenantID, id)
	if err != nil {
		h.log.Error("Failed to get lead audit logs", "error", err)
		response.InternalError(c, "Failed to get audit logs")
		return
	}
	defer rows.Close()

	logs := make([]map[string]interface{}, 0)
	for rows.Next() {
		var logID, userID uuid.UUID
		var userEmail, userName, action string
		var oldValues, newValues sql.NullString
		var createdAt time.Time

		if err := rows.Scan(&logID, &userID, &userEmail, &userName, &action, &oldValues, &newValues, &createdAt); err != nil {
			continue
		}
		logs = append(logs, map[string]interface{}{
			"id":         logID,
			"user_id":    userID,
			"user_email": userEmail,
			"user_name":  strings.TrimSpace(userName),
			"action":     action,
			"old_values": oldValues.String,
			"new_values": newValues.String,
			"created_at": createdAt,
		})
	}

	response.Success(c, logs)
}

// PublicCreateLead creates a lead from an external website form (no auth required).
// POST /api/v1/public/leads
func (h *Handler) PublicCreateLead(c *gin.Context) {
	var input struct {
		TenantCode  string `json:"tenant_code" binding:"required"`
		ContactName string `json:"contact_name" binding:"required"`
		Email       string `json:"email"`
		Phone       string `json:"phone"`
		CompanyName string `json:"company_name"`
		Notes       string `json:"notes"`
		Source      string `json:"source"`
		PageURL     string `json:"page_url"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"success": false, "error": "tenant_code and contact_name are required"})
		return
	}

	if input.Email == "" && input.Phone == "" {
		c.JSON(400, gin.H{"success": false, "error": "email or phone is required"})
		return
	}

	// Look up tenant by code
	var tenantID uuid.UUID
	var tenantActive bool
	err := h.db.QueryRow(`
		SELECT id, is_active FROM tenants WHERE code = $1 AND deleted_at IS NULL
	`, input.TenantCode).Scan(&tenantID, &tenantActive)
	if err != nil {
		c.JSON(404, gin.H{"success": false, "error": "Company not found"})
		return
	}
	if !tenantActive {
		c.JSON(403, gin.H{"success": false, "error": "Company subscription inactive"})
		return
	}

	// Find the tenant owner to assign the lead to
	var assignedTo uuid.UUID
	h.db.QueryRow(`
		SELECT u.id FROM users u
		LEFT JOIN user_roles ur ON u.id = ur.user_id
		LEFT JOIN roles r ON ur.role_id = r.id
		WHERE u.tenant_id = $1 AND u.deleted_at IS NULL
		AND (u.is_system_admin = true OR r.code = 'owner')
		LIMIT 1
	`, tenantID).Scan(&assignedTo)

	// Get organization ID
	var orgID *uuid.UUID
	var oid uuid.UUID
	if h.db.QueryRow(`SELECT id FROM organizations WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&oid) == nil {
		orgID = &oid
	}

	source := input.Source
	if source == "" {
		source = "website"
	}

	notes := input.Notes
	if input.PageURL != "" {
		if notes != "" {
			notes += "\n"
		}
		notes += "Page: " + input.PageURL
	}

	leadID := uuid.New()
	now := time.Now()

	// land in the first open stage of the org's default pipeline
	var stagePtr, pipelinePtr *uuid.UUID
	{
		var sid uuid.UUID
		var pid *uuid.UUID
		if err := h.db.QueryRow(`
			SELECT ps.id, ps.pipeline_id
			FROM pipeline_stages ps
			JOIN pipelines p ON p.id = ps.pipeline_id AND p.is_default
			WHERE ps.tenant_id = $1 AND ps.pipeline_type = 'lead' AND ps.is_active
			  AND NOT ps.is_won AND NOT ps.is_lost
			  AND p.organization_id IS NOT DISTINCT FROM $2
			ORDER BY ps.sequence LIMIT 1
		`, tenantID, orgID).Scan(&sid, &pid); err == nil {
			stagePtr = &sid
			pipelinePtr = pid
		}
	}

	_, err = h.db.Exec(`
		INSERT INTO leads (id, tenant_id, organization_id, contact_name, company_name, email, phone,
			status, source, notes, assigned_to, pipeline_id, stage_id, last_activity_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'new', $8, $9, $10, $11, $12, $13, $13, $13)
	`, leadID, tenantID, orgID, input.ContactName, input.CompanyName, input.Email, input.Phone,
		source, notes, assignedTo, pipelinePtr, stagePtr, now)
	if err != nil {
		h.log.Error("PublicCreateLead: failed to create lead", "error", err)
		c.JSON(500, gin.H{"success": false, "error": "Failed to create lead"})
		return
	}

	if stagePtr != nil {
		h.recordLeadStageChange(h.db, tenantID, leadID, nil, stagePtr, uuid.Nil)
	}

	// Website leads must trigger the same automations as manual ones —
	// the old handler skipped this, so auto-assign/notify rules never ran
	// for the highest-volume entry point.
	h.EmitWorkflowEvent(tenantID, "lead.created", map[string]interface{}{
		"record_id":    leadID.String(),
		"contact_name": input.ContactName,
		"company_name": input.CompanyName,
		"email":        input.Email,
		"source":       source,
	})

	h.log.Info("PublicCreateLead: lead created from website", "tenant_code", input.TenantCode, "lead_id", leadID, "email", input.Email)
	c.JSON(200, gin.H{"success": true, "lead_id": leadID})
}
