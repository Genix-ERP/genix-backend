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
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	status := c.Query("status")
	source := c.Query("source")
	assignedTo := c.Query("assigned_to")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// Build query
	baseQuery := `
		SELECT l.id, l.tenant_id, l.contact_name, l.company_name,
			   l.email, l.phone, l.status, l.source, l.notes,
			   l.expected_value, l.assigned_to, l.converted_to,
			   l.converted_at, l.created_at, l.updated_at,
			   u.first_name || ' ' || u.last_name as assigned_to_name
		FROM leads l
		LEFT JOIN users u ON l.assigned_to = u.id
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM leads l WHERE l.tenant_id = $1 AND l.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
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
		var assignedTo, convertedTo sql.NullString
		var convertedAt sql.NullTime
		var assignedToName sql.NullString

		err := rows.Scan(
			&l.ID, &l.TenantID, &l.ContactName, &companyName,
			&l.Email, &phone, &l.Status, &l.Source, &notes,
			&expectedValue, &assignedTo, &convertedTo,
			&convertedAt, &l.CreatedAt, &l.UpdatedAt,
			&assignedToName,
		)
		if err != nil {
			h.log.Error("Failed to scan lead", "error", err)
			continue
		}

		resp := &entity.LeadResponse{
			ID:          l.ID,
			ContactName: l.ContactName,
			Email:       l.Email,
			Status:      l.Status,
			Source:      l.Source,
			CreatedAt:   l.CreatedAt,
			UpdatedAt:   l.UpdatedAt,
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
		if assignedTo.Valid {
			aid, _ := uuid.Parse(assignedTo.String)
			resp.AssignedTo = &aid
		}
		if assignedToName.Valid {
			resp.AssignedToName = &assignedToName.String
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
			if phone != "" {
				argIdx++
				conditions = append(conditions, fmt.Sprintf("phone = $%d", argIdx))
				args = append(args, phone)
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
				if phone != "" && ph == phone {
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
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse optional UUID for assigned_to
	var assignedTo *uuid.UUID
	if input.AssignedTo != "" {
		aid, err := uuid.Parse(input.AssignedTo)
		if err == nil {
			assignedTo = &aid
		}
	}

	id := uuid.New()
	now := time.Now()

	// Get organization ID
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Set defaults
	status := entity.LeadStatusNew
	if input.Status != "" {
		status = input.Status
	}

	source := entity.LeadSourceWebsite
	if input.Source != "" {
		source = input.Source
	}

	// Check for duplicates before creating
	if enabled, checkFields := h.getDuplicateDetectionSettings(tenantID); enabled {
		duplicates := h.checkLeadDuplicates(tenantID, input.Email, input.Phone, input.CompanyName, checkFields)
		if len(duplicates) > 0 {
			response.ConflictWithData(c, "DUPLICATE_DETECTED", "Potential duplicate lead(s) found", map[string]interface{}{
				"duplicates": duplicates,
			})
			return
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
			expected_value, assigned_to,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, input.ContactName, companyName,
		input.Email, phone, status, source, notes,
		input.ExpectedValue, assignedTo,
		userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create lead", "error", err)
		response.InternalError(c, "Failed to create lead")
		return
	}

	resp := &entity.LeadResponse{
		ID:            id,
		ContactName:   input.ContactName,
		CompanyName:   companyName,
		Email:         input.Email,
		Phone:         phone,
		Status:        status,
		Source:        source,
		Notes:         notes,
		ExpectedValue: input.ExpectedValue,
		AssignedTo:    assignedTo,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Trigger workflow rules for new lead
	go h.EvaluateWorkflowRules(tenantID, "lead.created", map[string]interface{}{
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
			   l.expected_value, l.assigned_to, l.converted_to,
			   l.converted_at, l.created_at, l.updated_at,
			   u.first_name || ' ' || u.last_name as assigned_to_name
		FROM leads l
		LEFT JOIN users u ON l.assigned_to = u.id
		WHERE l.id = $1 AND l.tenant_id = $2 AND l.deleted_at IS NULL
	`

	var l entity.Lead
	var companyName, phone, notes sql.NullString
	var expectedValue sql.NullFloat64
	var assignedTo, convertedTo sql.NullString
	var convertedAt sql.NullTime
	var assignedToName sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&l.ID, &l.TenantID, &l.ContactName, &companyName,
		&l.Email, &phone, &l.Status, &l.Source, &notes,
		&expectedValue, &assignedTo, &convertedTo,
		&convertedAt, &l.CreatedAt, &l.UpdatedAt,
		&assignedToName,
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
		ID:          l.ID,
		ContactName: l.ContactName,
		Email:       l.Email,
		Status:      l.Status,
		Source:      l.Source,
		CreatedAt:   l.CreatedAt,
		UpdatedAt:   l.UpdatedAt,
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
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

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
			COUNT(*) FILTER (WHERE status = 'new') as new_leads,
			COUNT(*) FILTER (WHERE status = 'contacted') as contacted_leads,
			COUNT(*) FILTER (WHERE status = 'in_progress') as in_progress_leads,
			COUNT(*) FILTER (WHERE status = 'qualified') as qualified_leads,
			COUNT(*) FILTER (WHERE status = 'lost') as lost_leads,
			COALESCE(
				COUNT(*) FILTER (WHERE status = 'qualified')::float /
				NULLIF(COUNT(*), 0) * 100,
				0
			) as conversion_rate,
			COALESCE(SUM(expected_value) FILTER (WHERE status != 'lost'), 0) as total_value
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
		&stats.NewLeads,
		&stats.ContactedLeads,
		&stats.InProgressLeads,
		&stats.QualifiedLeads,
		&stats.LostLeads,
		&stats.ConversionRate,
		&stats.TotalValue,
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
	CreateContact     bool   `json:"create_contact"`
	CreateOpportunity bool   `json:"create_opportunity"`
	ContactType       string `json:"contact_type,omitempty"`       // customer, vendor, partner
	OpportunityName   string `json:"opportunity_name,omitempty"`
	ExpectedRevenue   float64 `json:"expected_revenue,omitempty"`
	ExpectedCloseDate string `json:"expected_close_date,omitempty"`
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
		response.BadRequest(c, "Invalid input: "+err.Error())
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

	// Get organization ID
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
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

		err = tx.QueryRow(oppQuery,
			newOpportunityID, tenantID, orgIDPtr, oppName, oppCode, contactID, id,
			"qualification", 10.0, expectedRevenue, "USD",
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

	// Update lead as converted
	updateQuery := `
		UPDATE leads
		SET status = 'qualified', converted_to = $1, converted_at = $2, updated_at = $2
		WHERE id = $3 AND tenant_id = $4
	`

	convertedTo := contactID
	if convertedTo == nil {
		convertedTo = opportunityID
	}

	_, err = tx.Exec(updateQuery, convertedTo, now, id, tenantID)
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
