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
// OPPORTUNITY HANDLERS
// =====================================================

// ListOpportunities returns a paginated list of opportunities
func (h *Handler) ListOpportunities(c *gin.Context) {
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
	stage := c.Query("stage")
	contactID := c.Query("contact_id")
	assignedTo := c.Query("assigned_to")
	priority := c.Query("priority")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	includeClosed := c.Query("include_closed") == "true"

	// Build query
	baseQuery := `
		SELECT o.id, o.tenant_id, o.name, o.code, o.contact_id, o.lead_id,
			   o.stage_id, o.stage, o.probability, o.expected_revenue,
			   o.actual_revenue, o.currency, o.expected_close_date,
			   o.actual_close_date, o.source, o.priority, o.assigned_to,
			   o.description, o.next_step, o.next_step_date, o.lost_reason,
			   o.competitor, o.tags, o.created_at, o.updated_at,
			   c.name as contact_name,
			   u.first_name || ' ' || u.last_name as assigned_to_name,
			   ps.name as stage_name
		FROM opportunities o
		LEFT JOIN contacts c ON o.contact_id = c.id
		LEFT JOIN users u ON o.assigned_to = u.id
		LEFT JOIN pipeline_stages ps ON o.stage_id = ps.id
		WHERE o.tenant_id = $1 AND o.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM opportunities o WHERE o.tenant_id = $1 AND o.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND o.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND o.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if !includeClosed {
		baseQuery += " AND o.stage NOT IN ('closed_won', 'closed_lost')"
		countQuery += " AND o.stage NOT IN ('closed_won', 'closed_lost')"
	}

	if stage != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND o.stage = $%d", argCount)
		countQuery += fmt.Sprintf(" AND o.stage = $%d", argCount)
		args = append(args, stage)
	}

	if contactID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND o.contact_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND o.contact_id = $%d", argCount)
		args = append(args, contactID)
	}

	if assignedTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND o.assigned_to = $%d", argCount)
		countQuery += fmt.Sprintf(" AND o.assigned_to = $%d", argCount)
		args = append(args, assignedTo)
	}

	if priority != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND o.priority = $%d", argCount)
		countQuery += fmt.Sprintf(" AND o.priority = $%d", argCount)
		args = append(args, priority)
	}

	if dateFrom != "" {
		if t, err := time.Parse("2006-01-02", dateFrom); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND o.expected_close_date >= $%d", argCount)
			countQuery += fmt.Sprintf(" AND o.expected_close_date >= $%d", argCount)
			args = append(args, t)
		}
	}

	if dateTo != "" {
		if t, err := time.Parse("2006-01-02", dateTo); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND o.expected_close_date <= $%d", argCount)
			countQuery += fmt.Sprintf(" AND o.expected_close_date <= $%d", argCount)
			args = append(args, t)
		}
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (o.name ILIKE $%d OR o.code ILIKE $%d OR c.name ILIKE $%d)", argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count opportunities", "error", err)
		response.InternalError(c, "Failed to list opportunities")
		return
	}

	// Add ordering and pagination
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "DESC")
	if sortOrder != "ASC" && sortOrder != "DESC" {
		sortOrder = "DESC"
	}
	allowedSorts := map[string]bool{
		"created_at": true, "expected_close_date": true, "expected_revenue": true,
		"probability": true, "name": true, "stage": true,
	}
	if !allowedSorts[sortBy] {
		sortBy = "created_at"
	}
	baseQuery += fmt.Sprintf(" ORDER BY o.%s %s", sortBy, sortOrder)
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list opportunities", "error", err)
		response.InternalError(c, "Failed to list opportunities")
		return
	}
	defer rows.Close()

	opportunities := make([]*entity.OpportunityResponse, 0)
	for rows.Next() {
		var o entity.Opportunity
		var code, source, description, nextStep, lostReason, competitor sql.NullString
		var contactID, leadID, stageID, assignedTo sql.NullString
		var actualRevenue sql.NullFloat64
		var expectedCloseDate, actualCloseDate, nextStepDate sql.NullTime
		var contactName, assignedToName, stageName sql.NullString
		var tags []byte

		err := rows.Scan(
			&o.ID, &o.TenantID, &o.Name, &code, &contactID, &leadID,
			&stageID, &o.Stage, &o.Probability, &o.ExpectedRevenue,
			&actualRevenue, &o.Currency, &expectedCloseDate,
			&actualCloseDate, &source, &o.Priority, &assignedTo,
			&description, &nextStep, &nextStepDate, &lostReason,
			&competitor, &tags, &o.CreatedAt, &o.UpdatedAt,
			&contactName, &assignedToName, &stageName,
		)
		if err != nil {
			h.log.Error("Failed to scan opportunity", "error", err)
			continue
		}

		resp := &entity.OpportunityResponse{
			ID:              o.ID,
			Name:            o.Name,
			Stage:           o.Stage,
			Probability:     o.Probability,
			ExpectedRevenue: o.ExpectedRevenue,
			Currency:        o.Currency,
			Priority:        o.Priority,
			Tags:            []string{},
			CreatedAt:       o.CreatedAt,
			UpdatedAt:       o.UpdatedAt,
		}

		if code.Valid {
			resp.Code = &code.String
		}
		if contactID.Valid {
			cid, _ := uuid.Parse(contactID.String)
			resp.ContactID = &cid
		}
		if contactName.Valid {
			resp.ContactName = &contactName.String
		}
		if leadID.Valid {
			lid, _ := uuid.Parse(leadID.String)
			resp.LeadID = &lid
		}
		if stageID.Valid {
			sid, _ := uuid.Parse(stageID.String)
			resp.StageID = &sid
		}
		if stageName.Valid {
			resp.StageName = &stageName.String
		}
		if actualRevenue.Valid {
			resp.ActualRevenue = &actualRevenue.Float64
		}
		if expectedCloseDate.Valid {
			resp.ExpectedCloseDate = &expectedCloseDate.Time
		}
		if actualCloseDate.Valid {
			resp.ActualCloseDate = &actualCloseDate.Time
		}
		if source.Valid {
			resp.Source = &source.String
		}
		if assignedTo.Valid {
			aid, _ := uuid.Parse(assignedTo.String)
			resp.AssignedTo = &aid
		}
		if assignedToName.Valid {
			resp.AssignedToName = &assignedToName.String
		}
		if description.Valid {
			resp.Description = &description.String
		}
		if nextStep.Valid {
			resp.NextStep = &nextStep.String
		}
		if nextStepDate.Valid {
			resp.NextStepDate = &nextStepDate.Time
		}
		if lostReason.Valid {
			resp.LostReason = &lostReason.String
		}
		if len(tags) > 0 {
			json.Unmarshal(tags, &resp.Tags)
		}

		opportunities = append(opportunities, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, opportunities, pagination)
}

// CreateOpportunity creates a new opportunity
func (h *Handler) CreateOpportunity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateOpportunityInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Generate code if not provided
	code := input.Code
	if code == "" {
		code = fmt.Sprintf("OPP-%s", id.String()[:8])
	}

	// Parse optional UUIDs
	var contactID, leadID, stageID, assignedTo *uuid.UUID
	if input.ContactID != "" {
		cid, err := uuid.Parse(input.ContactID)
		if err == nil {
			contactID = &cid
		}
	}
	if input.LeadID != "" {
		lid, err := uuid.Parse(input.LeadID)
		if err == nil {
			leadID = &lid
		}
	}
	if input.StageID != "" {
		sid, err := uuid.Parse(input.StageID)
		if err == nil {
			stageID = &sid
		}
	}
	if input.AssignedTo != "" {
		aid, err := uuid.Parse(input.AssignedTo)
		if err == nil {
			assignedTo = &aid
		}
	}

	// Parse dates
	var expectedCloseDate, nextStepDate *time.Time
	if input.ExpectedCloseDate != "" {
		if t, err := time.Parse("2006-01-02", input.ExpectedCloseDate); err == nil {
			expectedCloseDate = &t
		}
	}
	if input.NextStepDate != "" {
		if t, err := time.Parse("2006-01-02", input.NextStepDate); err == nil {
			nextStepDate = &t
		}
	}

	// Set defaults
	stage := entity.OpportunityStageQualification
	if input.Stage != "" {
		stage = entity.OpportunityStage(input.Stage)
	}

	priority := entity.OpportunityPriorityMedium
	if input.Priority != "" {
		priority = entity.OpportunityPriority(input.Priority)
	}

	currency := "USD"
	if input.Currency != "" {
		currency = input.Currency
	}

	// Serialize tags
	tags, _ := json.Marshal(input.Tags)
	if input.Tags == nil {
		tags = []byte("[]")
	}

	// Prepare optional strings
	var source, description, nextStep *string
	if input.Source != "" {
		source = &input.Source
	}
	if input.Description != "" {
		description = &input.Description
	}
	if input.NextStep != "" {
		nextStep = &input.NextStep
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO opportunities (
			id, tenant_id, organization_id, name, code, contact_id, lead_id,
			stage_id, stage, probability, expected_revenue,
			currency, expected_close_date, source, priority,
			assigned_to, description, next_step, next_step_date,
			tags, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, input.Name, code, contactID, leadID,
		stageID, stage, input.Probability, input.ExpectedRevenue,
		currency, expectedCloseDate, source, priority,
		assignedTo, description, nextStep, nextStepDate,
		tags, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create opportunity", "error", err)
		response.InternalError(c, "Failed to create opportunity")
		return
	}

	resp := &entity.OpportunityResponse{
		ID:                id,
		Name:              input.Name,
		Code:              &code,
		ContactID:         contactID,
		LeadID:            leadID,
		StageID:           stageID,
		Stage:             stage,
		Probability:       input.Probability,
		ExpectedRevenue:   input.ExpectedRevenue,
		Currency:          currency,
		ExpectedCloseDate: expectedCloseDate,
		Source:            source,
		Priority:          priority,
		AssignedTo:        assignedTo,
		Description:       description,
		NextStep:          nextStep,
		NextStepDate:      nextStepDate,
		Tags:              input.Tags,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	response.Created(c, resp)
}

// GetOpportunity returns a single opportunity by ID
func (h *Handler) GetOpportunity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid opportunity ID")
		return
	}

	query := `
		SELECT o.id, o.tenant_id, o.name, o.code, o.contact_id, o.lead_id,
			   o.stage_id, o.stage, o.probability, o.expected_revenue,
			   o.actual_revenue, o.currency, o.expected_close_date,
			   o.actual_close_date, o.source, o.priority, o.assigned_to,
			   o.description, o.next_step, o.next_step_date, o.lost_reason,
			   o.competitor, o.tags, o.created_at, o.updated_at,
			   c.name as contact_name,
			   u.first_name || ' ' || u.last_name as assigned_to_name,
			   ps.name as stage_name
		FROM opportunities o
		LEFT JOIN contacts c ON o.contact_id = c.id
		LEFT JOIN users u ON o.assigned_to = u.id
		LEFT JOIN pipeline_stages ps ON o.stage_id = ps.id
		WHERE o.id = $1 AND o.tenant_id = $2 AND o.deleted_at IS NULL
	`

	var o entity.Opportunity
	var code, source, description, nextStep, lostReason, competitor sql.NullString
	var contactID, leadID, stageID, assignedTo sql.NullString
	var actualRevenue sql.NullFloat64
	var expectedCloseDate, actualCloseDate, nextStepDate sql.NullTime
	var contactName, assignedToName, stageName sql.NullString
	var tags []byte

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&o.ID, &o.TenantID, &o.Name, &code, &contactID, &leadID,
		&stageID, &o.Stage, &o.Probability, &o.ExpectedRevenue,
		&actualRevenue, &o.Currency, &expectedCloseDate,
		&actualCloseDate, &source, &o.Priority, &assignedTo,
		&description, &nextStep, &nextStepDate, &lostReason,
		&competitor, &tags, &o.CreatedAt, &o.UpdatedAt,
		&contactName, &assignedToName, &stageName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Opportunity")
		return
	}
	if err != nil {
		h.log.Error("Failed to get opportunity", "error", err)
		response.InternalError(c, "Failed to get opportunity")
		return
	}

	resp := &entity.OpportunityResponse{
		ID:              o.ID,
		Name:            o.Name,
		Stage:           o.Stage,
		Probability:     o.Probability,
		ExpectedRevenue: o.ExpectedRevenue,
		Currency:        o.Currency,
		Priority:        o.Priority,
		Tags:            []string{},
		CreatedAt:       o.CreatedAt,
		UpdatedAt:       o.UpdatedAt,
	}

	if code.Valid {
		resp.Code = &code.String
	}
	if contactID.Valid {
		cid, _ := uuid.Parse(contactID.String)
		resp.ContactID = &cid
	}
	if contactName.Valid {
		resp.ContactName = &contactName.String
	}
	if leadID.Valid {
		lid, _ := uuid.Parse(leadID.String)
		resp.LeadID = &lid
	}
	if stageID.Valid {
		sid, _ := uuid.Parse(stageID.String)
		resp.StageID = &sid
	}
	if stageName.Valid {
		resp.StageName = &stageName.String
	}
	if actualRevenue.Valid {
		resp.ActualRevenue = &actualRevenue.Float64
	}
	if expectedCloseDate.Valid {
		resp.ExpectedCloseDate = &expectedCloseDate.Time
	}
	if actualCloseDate.Valid {
		resp.ActualCloseDate = &actualCloseDate.Time
	}
	if source.Valid {
		resp.Source = &source.String
	}
	if assignedTo.Valid {
		aid, _ := uuid.Parse(assignedTo.String)
		resp.AssignedTo = &aid
	}
	if assignedToName.Valid {
		resp.AssignedToName = &assignedToName.String
	}
	if description.Valid {
		resp.Description = &description.String
	}
	if nextStep.Valid {
		resp.NextStep = &nextStep.String
	}
	if nextStepDate.Valid {
		resp.NextStepDate = &nextStepDate.Time
	}
	if lostReason.Valid {
		resp.LostReason = &lostReason.String
	}
	if len(tags) > 0 {
		json.Unmarshal(tags, &resp.Tags)
	}

	response.Success(c, resp)
}

// UpdateOpportunity updates an existing opportunity
func (h *Handler) UpdateOpportunity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid opportunity ID")
		return
	}

	var input entity.UpdateOpportunityInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.ContactID != nil {
		argCount++
		if *input.ContactID == "" {
			updates = append(updates, fmt.Sprintf("contact_id = $%d", argCount))
			args = append(args, nil)
		} else {
			cid, err := uuid.Parse(*input.ContactID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("contact_id = $%d", argCount))
				args = append(args, cid)
			}
		}
	}
	if input.StageID != nil {
		argCount++
		if *input.StageID == "" {
			updates = append(updates, fmt.Sprintf("stage_id = $%d", argCount))
			args = append(args, nil)
		} else {
			sid, err := uuid.Parse(*input.StageID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("stage_id = $%d", argCount))
				args = append(args, sid)
			}
		}
	}
	if input.Stage != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("stage = $%d", argCount))
		args = append(args, *input.Stage)

		// If closed_won, set actual_close_date
		if *input.Stage == "closed_won" || *input.Stage == "closed_lost" {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_close_date = $%d", argCount))
			args = append(args, time.Now())
		}
	}
	if input.Probability != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("probability = $%d", argCount))
		args = append(args, *input.Probability)
	}
	if input.ExpectedRevenue != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("expected_revenue = $%d", argCount))
		args = append(args, *input.ExpectedRevenue)
	}
	if input.ActualRevenue != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("actual_revenue = $%d", argCount))
		args = append(args, *input.ActualRevenue)
	}
	if input.Currency != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("currency = $%d", argCount))
		args = append(args, *input.Currency)
	}
	if input.ExpectedCloseDate != nil {
		argCount++
		if *input.ExpectedCloseDate == "" {
			updates = append(updates, fmt.Sprintf("expected_close_date = $%d", argCount))
			args = append(args, nil)
		} else {
			if t, err := time.Parse("2006-01-02", *input.ExpectedCloseDate); err == nil {
				updates = append(updates, fmt.Sprintf("expected_close_date = $%d", argCount))
				args = append(args, t)
			}
		}
	}
	if input.Source != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("source = $%d", argCount))
		args = append(args, *input.Source)
	}
	if input.Priority != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("priority = $%d", argCount))
		args = append(args, *input.Priority)
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
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.NextStep != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("next_step = $%d", argCount))
		args = append(args, *input.NextStep)
	}
	if input.NextStepDate != nil {
		argCount++
		if *input.NextStepDate == "" {
			updates = append(updates, fmt.Sprintf("next_step_date = $%d", argCount))
			args = append(args, nil)
		} else {
			if t, err := time.Parse("2006-01-02", *input.NextStepDate); err == nil {
				updates = append(updates, fmt.Sprintf("next_step_date = $%d", argCount))
				args = append(args, t)
			}
		}
	}
	if input.LostReason != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("lost_reason = $%d", argCount))
		args = append(args, *input.LostReason)
	}
	if input.Competitor != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("competitor = $%d", argCount))
		args = append(args, *input.Competitor)
	}
	if input.Tags != nil {
		argCount++
		tagsJSON, _ := json.Marshal(input.Tags)
		updates = append(updates, fmt.Sprintf("tags = $%d", argCount))
		args = append(args, tagsJSON)
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
		"UPDATE opportunities SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update opportunity", "error", err)
		response.InternalError(c, "Failed to update opportunity")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Opportunity")
		return
	}

	response.Success(c, gin.H{"message": "Opportunity updated successfully"})
}

// DeleteOpportunity soft deletes an opportunity
func (h *Handler) DeleteOpportunity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid opportunity ID")
		return
	}

	query := `
		UPDATE opportunities
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete opportunity", "error", err)
		response.InternalError(c, "Failed to delete opportunity")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Opportunity")
		return
	}

	response.Success(c, gin.H{"message": "Opportunity deleted successfully"})
}

// GetOpportunityStats returns aggregated opportunity statistics
func (h *Handler) GetOpportunityStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT
			COUNT(*) as total_opportunities,
			COALESCE(SUM(expected_revenue), 0) as total_value,
			COALESCE(SUM(expected_revenue * probability / 100), 0) as weighted_value,
			COUNT(*) FILTER (WHERE stage = 'closed_won') as won_count,
			COALESCE(SUM(expected_revenue) FILTER (WHERE stage = 'closed_won'), 0) as won_value,
			COUNT(*) FILTER (WHERE stage = 'closed_lost') as lost_count,
			COALESCE(SUM(expected_revenue) FILTER (WHERE stage = 'closed_lost'), 0) as lost_value,
			COUNT(*) FILTER (WHERE stage NOT IN ('closed_won', 'closed_lost')) as open_count,
			COALESCE(SUM(expected_revenue) FILTER (WHERE stage NOT IN ('closed_won', 'closed_lost')), 0) as open_value,
			COALESCE(
				COUNT(*) FILTER (WHERE stage = 'closed_won')::float /
				NULLIF(COUNT(*) FILTER (WHERE stage IN ('closed_won', 'closed_lost')), 0) * 100,
				0
			) as win_rate,
			COALESCE(AVG(expected_revenue), 0) as average_deal_size,
			COALESCE(
				AVG(EXTRACT(DAY FROM (actual_close_date - created_at::date))) FILTER (WHERE stage = 'closed_won'),
				0
			)::int as average_sales_cycle
		FROM opportunities
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`

	var stats entity.OpportunityStats
	err := h.db.QueryRow(query, tenantID).Scan(
		&stats.TotalOpportunities,
		&stats.TotalValue,
		&stats.WeightedValue,
		&stats.WonCount,
		&stats.WonValue,
		&stats.LostCount,
		&stats.LostValue,
		&stats.OpenCount,
		&stats.OpenValue,
		&stats.WinRate,
		&stats.AverageDealSize,
		&stats.AverageSalesCycle,
	)

	if err != nil {
		h.log.Error("Failed to get opportunity stats", "error", err)
		response.InternalError(c, "Failed to get opportunity statistics")
		return
	}

	response.Success(c, stats)
}

// GetOpportunitiesByStage returns opportunities grouped by stage
func (h *Handler) GetOpportunitiesByStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT
			o.stage,
			COALESCE(ps.name, INITCAP(REPLACE(o.stage::text, '_', ' '))) as stage_name,
			COUNT(*) as count,
			COALESCE(SUM(o.expected_revenue), 0) as total_value,
			COALESCE(AVG(o.probability), 0) as probability
		FROM opportunities o
		LEFT JOIN pipeline_stages ps ON o.stage_id = ps.id
		WHERE o.tenant_id = $1 AND o.deleted_at IS NULL
		GROUP BY o.stage, ps.name, ps.sequence
		ORDER BY COALESCE(ps.sequence,
			CASE o.stage
				WHEN 'qualification' THEN 1
				WHEN 'needs_analysis' THEN 2
				WHEN 'proposal' THEN 3
				WHEN 'negotiation' THEN 4
				WHEN 'closed_won' THEN 5
				WHEN 'closed_lost' THEN 6
			END)
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to get opportunities by stage", "error", err)
		response.InternalError(c, "Failed to get opportunities by stage")
		return
	}
	defer rows.Close()

	stages := make([]*entity.OpportunityByStage, 0)
	for rows.Next() {
		var s entity.OpportunityByStage
		err := rows.Scan(&s.Stage, &s.StageName, &s.Count, &s.TotalValue, &s.Probability)
		if err != nil {
			h.log.Error("Failed to scan stage", "error", err)
			continue
		}
		stages = append(stages, &s)
	}

	response.Success(c, stages)
}

// =====================================================
// PIPELINE STAGES HANDLERS
// =====================================================

// ListPipelineStages returns all pipeline stages
func (h *Handler) ListPipelineStages(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	pipelineType := c.DefaultQuery("type", "opportunity")
	args := []interface{}{tenantID, pipelineType}
	argCount := 2

	query := `
		SELECT id, tenant_id, name, custom_name, code, sequence, probability,
			   is_won, is_lost, color, is_active,
			   COALESCE(pipeline_type, 'opportunity') as pipeline_type,
			   organization_id,
			   created_at, updated_at
		FROM pipeline_stages
		WHERE tenant_id = $1 AND COALESCE(pipeline_type, 'opportunity') = $2
	`

	// Per-organization scope (user request: "stage created in one
	// company should not be in second"). When X-Organization-ID is
	// present, return THIS org's stages PLUS any legacy tenant-wide
	// stages (organization_id IS NULL). Including the NULL rows is
	// what preserves the data created before this scoping was
	// introduced — without it, a user who upgraded suddenly saw an
	// empty pipeline (and the frontend's seed-defaults auto-created
	// fresh ones, orphaning their existing leads). New stages
	// created from now on get stamped with the active org and stay
	// invisible to other companies in the same tenant.
	orgID, orgOk := middleware.GetOrganizationID(c)
	if orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND (organization_id = $%d OR organization_id IS NULL)", argCount)
		args = append(args, orgID)
	}
	query += " ORDER BY sequence ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list pipeline stages", "error", err)
		response.InternalError(c, "Failed to list pipeline stages")
		return
	}
	defer rows.Close()

	stages := make([]*entity.PipelineStage, 0)
	for rows.Next() {
		var s entity.PipelineStage
		err := rows.Scan(
			&s.ID, &s.TenantID, &s.Name, &s.CustomName, &s.Code, &s.Sequence,
			&s.Probability, &s.IsWon, &s.IsLost, &s.Color,
			&s.IsActive, &s.PipelineType, &s.OrganizationID,
			&s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan pipeline stage", "error", err)
			continue
		}
		stages = append(stages, &s)
	}

	response.Success(c, stages)
}

// CreatePipelineStage creates a new pipeline stage
func (h *Handler) CreatePipelineStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreatePipelineStageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()

	color := input.Color
	if color == "" {
		color = "#3B82F6"
	}

	pipelineType := input.PipelineType
	if pipelineType == "" {
		pipelineType = "opportunity"
	}

	// Pipeline stages are now per-organization (per user request).
	// Stamp the active company id from X-Organization-ID so each
	// company keeps its own pipeline. Falls back to NULL when no org
	// is active — those legacy rows remain accessible only when the
	// list endpoint is called without an org context.
	var orgIDPtr *uuid.UUID
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		o := orgID
		orgIDPtr = &o
	}

	query := `
		INSERT INTO pipeline_stages (
			id, tenant_id, name, code, sequence, probability,
			is_won, is_lost, color, is_active, pipeline_type, organization_id,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, $10, $11, $12, $13)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, input.Name, input.Code, input.Sequence,
		input.Probability, input.IsWon, input.IsLost, color,
		pipelineType, orgIDPtr, now, now,
	).Scan(&id)

	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			response.BadRequest(c, "Pipeline stage code already exists")
			return
		}
		h.log.Error("Failed to create pipeline stage", "error", err)
		response.InternalError(c, "Failed to create pipeline stage")
		return
	}

	stage := &entity.PipelineStage{
		ID:             id,
		TenantID:       tenantID,
		Name:           input.Name,
		Code:           input.Code,
		Sequence:       input.Sequence,
		Probability:    input.Probability,
		IsWon:          input.IsWon,
		IsLost:         input.IsLost,
		Color:          color,
		IsActive:       true,
		PipelineType:   pipelineType,
		OrganizationID: orgIDPtr,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	response.Created(c, stage)
}

// UpdatePipelineStage updates an existing pipeline stage
func (h *Handler) UpdatePipelineStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid pipeline stage ID")
		return
	}

	var input entity.UpdatePipelineStageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
		// When name is updated, also update custom_name.
		// custom_name is set for default stages (user override), nil/NULL for non-default stages.
		argCount++
		updates = append(updates, fmt.Sprintf("custom_name = $%d", argCount))
		if input.CustomName != nil {
			args = append(args, *input.CustomName)
		} else {
			args = append(args, nil)
		}
	}
	if input.Sequence != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("sequence = $%d", argCount))
		args = append(args, *input.Sequence)
	}
	if input.Probability != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("probability = $%d", argCount))
		args = append(args, *input.Probability)
	}
	if input.IsWon != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_won = $%d", argCount))
		args = append(args, *input.IsWon)
	}
	if input.IsLost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_lost = $%d", argCount))
		args = append(args, *input.IsLost)
	}
	if input.Color != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("color = $%d", argCount))
		args = append(args, *input.Color)
	}
	if input.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE conditions — scoped by tenant AND, when an active
	// organization is set, the stage must belong to that org OR be a
	// legacy tenant-wide row (organization_id IS NULL). The NULL-row
	// allowance keeps users able to edit pre-scoping stages that
	// were never stamped with an org id; without it, their legacy
	// pipeline would render read-only.
	argCount++
	args = append(args, id)
	idArg := argCount
	argCount++
	args = append(args, tenantID)
	tenantArg := argCount

	whereClause := fmt.Sprintf("id = $%d AND tenant_id = $%d", idArg, tenantArg)
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		args = append(args, orgID)
		whereClause += fmt.Sprintf(" AND (organization_id = $%d OR organization_id IS NULL)", argCount)
	}

	query := fmt.Sprintf(
		"UPDATE pipeline_stages SET %s WHERE %s",
		strings.Join(updates, ", "), whereClause,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update pipeline stage", "error", err)
		response.InternalError(c, "Failed to update pipeline stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Pipeline stage")
		return
	}

	response.Success(c, gin.H{"message": "Pipeline stage updated successfully"})
}

// DeletePipelineStage deletes a pipeline stage
func (h *Handler) DeletePipelineStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid pipeline stage ID")
		return
	}

	// Check if stage has opportunities
	var count int
	err = h.db.QueryRow(
		"SELECT COUNT(*) FROM opportunities WHERE stage_id = $1 AND deleted_at IS NULL",
		id,
	).Scan(&count)
	if err == nil && count > 0 {
		response.BadRequest(c, "Cannot delete pipeline stage with existing opportunities")
		return
	}

	query := `DELETE FROM pipeline_stages WHERE id = $1 AND tenant_id = $2`
	args := []interface{}{id, tenantID}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		// Same rationale as the update path: allow deleting either
		// the active org's stages or the legacy NULL-org rows that
		// every company shared before scoping was added.
		query += " AND (organization_id = $3 OR organization_id IS NULL)"
		args = append(args, orgID)
	}

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to delete pipeline stage", "error", err)
		response.InternalError(c, "Failed to delete pipeline stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Pipeline stage")
		return
	}

	response.Success(c, gin.H{"message": "Pipeline stage deleted successfully"})
}
