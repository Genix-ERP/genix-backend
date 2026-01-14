package handler

import (
	"database/sql"
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

	// Set defaults
	status := entity.LeadStatusNew
	if input.Status != "" {
		status = input.Status
	}

	source := entity.LeadSourceWebsite
	if input.Source != "" {
		source = input.Source
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
			id, tenant_id, contact_name, company_name,
			email, phone, status, source, notes,
			expected_value, assigned_to,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, input.ContactName, companyName,
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

	var stats entity.LeadStats
	err := h.db.QueryRow(query, tenantID).Scan(
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
