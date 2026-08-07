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
// ACTIVITY HANDLERS
// =====================================================

// ListActivities returns a paginated list of activities
func (h *Handler) ListActivities(c *gin.Context) {
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
	activityType := c.Query("activity_type")
	status := c.Query("status")
	leadID := c.Query("lead_id")
	contactID := c.Query("contact_id")
	opportunityID := c.Query("opportunity_id")
	assignedTo := c.Query("assigned_to")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// Build query
	baseQuery := `
		SELECT a.id, a.tenant_id, a.activity_type, a.subject, a.description,
			   a.related_type, a.related_id, a.lead_id, a.contact_id, a.opportunity_id,
			   a.start_datetime, a.end_datetime, a.duration_minutes, a.location,
			   a.is_all_day, a.status, a.outcome, a.outcome_notes, a.assigned_to,
			   a.attendees, a.reminder_datetime, a.reminder_sent, a.priority,
			   a.is_private, a.tags, a.created_at, a.updated_at,
			   l.contact_name as lead_name,
			   ct.name as contact_name,
			   o.name as opportunity_name,
			   u.first_name || ' ' || u.last_name as assigned_to_name
		FROM activities a
		LEFT JOIN leads l ON a.lead_id = l.id
		LEFT JOIN contacts ct ON a.contact_id = ct.id
		LEFT JOIN opportunities o ON a.opportunity_id = o.id
		LEFT JOIN users u ON a.assigned_to = u.id
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM activities a WHERE a.tenant_id = $1 AND a.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if activityType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND a.activity_type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND a.activity_type = $%d", argCount)
		args = append(args, activityType)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND a.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND a.status = $%d", argCount)
		args = append(args, status)
	}

	if leadID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND a.lead_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND a.lead_id = $%d", argCount)
		args = append(args, leadID)
	}

	if contactID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND a.contact_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND a.contact_id = $%d", argCount)
		args = append(args, contactID)
	}

	if opportunityID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND a.opportunity_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND a.opportunity_id = $%d", argCount)
		args = append(args, opportunityID)
	}

	if assignedTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND a.assigned_to = $%d", argCount)
		countQuery += fmt.Sprintf(" AND a.assigned_to = $%d", argCount)
		args = append(args, assignedTo)
	}

	if dateFrom != "" {
		if t, err := time.Parse("2006-01-02", dateFrom); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND a.start_datetime >= $%d", argCount)
			countQuery += fmt.Sprintf(" AND a.start_datetime >= $%d", argCount)
			args = append(args, t)
		}
	}

	if dateTo != "" {
		if t, err := time.Parse("2006-01-02", dateTo); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND a.start_datetime <= $%d", argCount)
			countQuery += fmt.Sprintf(" AND a.start_datetime <= $%d", argCount)
			args = append(args, t.Add(24*time.Hour))
		}
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (a.subject ILIKE $%d OR a.description ILIKE $%d)", argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count activities", "error", err)
		response.InternalError(c, "Failed to list activities")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY COALESCE(a.start_datetime, a.created_at) DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list activities", "error", err)
		response.InternalError(c, "Failed to list activities")
		return
	}
	defer rows.Close()

	activities := make([]*entity.ActivityResponse, 0)
	for rows.Next() {
		var a entity.Activity
		var description, relatedType, location, outcomeNotes sql.NullString
		var relatedID, leadID, contactID, opportunityID, assignedTo sql.NullString
		var outcome sql.NullString
		var durationMinutes sql.NullInt64
		var startDatetime, endDatetime, reminderDatetime sql.NullTime
		var leadName, contactName, opportunityName, assignedToName sql.NullString
		var attendees, tags []byte

		err := rows.Scan(
			&a.ID, &a.TenantID, &a.ActivityType, &a.Subject, &description,
			&relatedType, &relatedID, &leadID, &contactID, &opportunityID,
			&startDatetime, &endDatetime, &durationMinutes, &location,
			&a.IsAllDay, &a.Status, &outcome, &outcomeNotes, &assignedTo,
			&attendees, &reminderDatetime, &a.ReminderSent, &a.Priority,
			&a.IsPrivate, &tags, &a.CreatedAt, &a.UpdatedAt,
			&leadName, &contactName, &opportunityName, &assignedToName,
		)
		if err != nil {
			h.log.Error("Failed to scan activity", "error", err)
			continue
		}

		resp := &entity.ActivityResponse{
			ID:           a.ID,
			ActivityType: a.ActivityType,
			Subject:      a.Subject,
			IsAllDay:     a.IsAllDay,
			Status:       a.Status,
			Priority:     a.Priority,
			IsPrivate:    a.IsPrivate,
			Attendees:    []entity.ActivityAttendee{},
			Tags:         []string{},
			CreatedAt:    a.CreatedAt,
			UpdatedAt:    a.UpdatedAt,
		}

		if description.Valid {
			resp.Description = &description.String
		}
		if leadID.Valid {
			lid, _ := uuid.Parse(leadID.String)
			resp.LeadID = &lid
		}
		if leadName.Valid {
			resp.LeadName = &leadName.String
		}
		if contactID.Valid {
			cid, _ := uuid.Parse(contactID.String)
			resp.ContactID = &cid
		}
		if contactName.Valid {
			resp.ContactName = &contactName.String
		}
		if opportunityID.Valid {
			oid, _ := uuid.Parse(opportunityID.String)
			resp.OpportunityID = &oid
		}
		if opportunityName.Valid {
			resp.OpportunityName = &opportunityName.String
		}
		if startDatetime.Valid {
			resp.StartDatetime = &startDatetime.Time
		}
		if endDatetime.Valid {
			resp.EndDatetime = &endDatetime.Time
		}
		if durationMinutes.Valid {
			dm := int(durationMinutes.Int64)
			resp.DurationMinutes = &dm
		}
		if location.Valid {
			resp.Location = &location.String
		}
		if outcome.Valid {
			o := entity.ActivityOutcome(outcome.String)
			resp.Outcome = &o
		}
		if outcomeNotes.Valid {
			resp.OutcomeNotes = &outcomeNotes.String
		}
		if assignedTo.Valid {
			aid, _ := uuid.Parse(assignedTo.String)
			resp.AssignedTo = &aid
		}
		if assignedToName.Valid {
			resp.AssignedToName = &assignedToName.String
		}
		if reminderDatetime.Valid {
			resp.ReminderDatetime = &reminderDatetime.Time
		}
		if len(attendees) > 0 {
			json.Unmarshal(attendees, &resp.Attendees)
		}
		if len(tags) > 0 {
			json.Unmarshal(tags, &resp.Tags)
		}

		activities = append(activities, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, activities, pagination)
}

// CreateActivity creates a new activity
func (h *Handler) CreateActivity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateActivityInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Parse optional UUIDs
	var leadID, contactID, opportunityID, assignedTo, relatedID *uuid.UUID
	if input.LeadID != "" {
		lid, err := uuid.Parse(input.LeadID)
		if err == nil {
			leadID = &lid
		}
	}
	if input.ContactID != "" {
		cid, err := uuid.Parse(input.ContactID)
		if err == nil {
			contactID = &cid
		}
	}
	if input.OpportunityID != "" {
		oid, err := uuid.Parse(input.OpportunityID)
		if err == nil {
			opportunityID = &oid
		}
	}
	if input.AssignedTo != "" {
		aid, err := uuid.Parse(input.AssignedTo)
		if err == nil {
			assignedTo = &aid
		}
	}
	if input.RelatedID != "" {
		rid, err := uuid.Parse(input.RelatedID)
		if err == nil {
			relatedID = &rid
		}
	}

	// Parse dates
	var startDatetime, endDatetime, reminderDatetime *time.Time
	if input.StartDatetime != "" {
		if t, err := time.Parse(time.RFC3339, input.StartDatetime); err == nil {
			startDatetime = &t
		}
	}
	if input.EndDatetime != "" {
		if t, err := time.Parse(time.RFC3339, input.EndDatetime); err == nil {
			endDatetime = &t
		}
	}
	if input.ReminderDatetime != "" {
		if t, err := time.Parse(time.RFC3339, input.ReminderDatetime); err == nil {
			reminderDatetime = &t
		}
	}

	// Set defaults
	status := entity.ActivityStatusPlanned
	if input.Status != "" {
		status = entity.ActivityStatus(input.Status)
	}

	priority := "medium"
	if input.Priority != "" {
		priority = input.Priority
	}

	// Serialize JSON fields
	attendees, _ := json.Marshal(input.Attendees)
	if input.Attendees == nil {
		attendees = []byte("[]")
	}
	tags, _ := json.Marshal(input.Tags)
	if input.Tags == nil {
		tags = []byte("[]")
	}

	// Prepare optional strings
	var description, relatedType, location *string
	if input.Description != "" {
		description = &input.Description
	}
	if input.RelatedType != "" {
		relatedType = &input.RelatedType
	}
	if input.Location != "" {
		location = &input.Location
	}

	query := `
		INSERT INTO activities (
			id, tenant_id, activity_type, subject, description,
			related_type, related_id, lead_id, contact_id, opportunity_id,
			start_datetime, end_datetime, duration_minutes, location,
			is_all_day, status, assigned_to, attendees, reminder_datetime,
			priority, is_private, tags, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, input.ActivityType, input.Subject, description,
		relatedType, relatedID, leadID, contactID, opportunityID,
		startDatetime, endDatetime, input.DurationMinutes, location,
		input.IsAllDay, status, assignedTo, attendees, reminderDatetime,
		priority, input.IsPrivate, tags, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create activity", "error", err)
		response.InternalError(c, "Failed to create activity")
		return
	}

	resp := &entity.ActivityResponse{
		ID:               id,
		ActivityType:     entity.ActivityType(input.ActivityType),
		Subject:          input.Subject,
		Description:      description,
		LeadID:           leadID,
		ContactID:        contactID,
		OpportunityID:    opportunityID,
		StartDatetime:    startDatetime,
		EndDatetime:      endDatetime,
		DurationMinutes:  input.DurationMinutes,
		Location:         location,
		IsAllDay:         input.IsAllDay,
		Status:           status,
		AssignedTo:       assignedTo,
		Attendees:        input.Attendees,
		ReminderDatetime: reminderDatetime,
		Priority:         priority,
		IsPrivate:        input.IsPrivate,
		Tags:             input.Tags,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	response.Created(c, resp)
}

// GetActivity returns a single activity by ID
func (h *Handler) GetActivity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid activity ID")
		return
	}

	query := `
		SELECT a.id, a.tenant_id, a.activity_type, a.subject, a.description,
			   a.related_type, a.related_id, a.lead_id, a.contact_id, a.opportunity_id,
			   a.start_datetime, a.end_datetime, a.duration_minutes, a.location,
			   a.is_all_day, a.status, a.outcome, a.outcome_notes, a.assigned_to,
			   a.attendees, a.reminder_datetime, a.reminder_sent, a.priority,
			   a.is_private, a.tags, a.created_at, a.updated_at,
			   l.contact_name as lead_name,
			   ct.name as contact_name,
			   o.name as opportunity_name,
			   u.first_name || ' ' || u.last_name as assigned_to_name
		FROM activities a
		LEFT JOIN leads l ON a.lead_id = l.id
		LEFT JOIN contacts ct ON a.contact_id = ct.id
		LEFT JOIN opportunities o ON a.opportunity_id = o.id
		LEFT JOIN users u ON a.assigned_to = u.id
		WHERE a.id = $1 AND a.tenant_id = $2 AND a.deleted_at IS NULL
	`

	var a entity.Activity
	var description, relatedType, location, outcomeNotes sql.NullString
	var relatedID, leadID, contactID, opportunityID, assignedTo sql.NullString
	var outcome sql.NullString
	var durationMinutes sql.NullInt64
	var startDatetime, endDatetime, reminderDatetime sql.NullTime
	var leadName, contactName, opportunityName, assignedToName sql.NullString
	var attendees, tags []byte

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&a.ID, &a.TenantID, &a.ActivityType, &a.Subject, &description,
		&relatedType, &relatedID, &leadID, &contactID, &opportunityID,
		&startDatetime, &endDatetime, &durationMinutes, &location,
		&a.IsAllDay, &a.Status, &outcome, &outcomeNotes, &assignedTo,
		&attendees, &reminderDatetime, &a.ReminderSent, &a.Priority,
		&a.IsPrivate, &tags, &a.CreatedAt, &a.UpdatedAt,
		&leadName, &contactName, &opportunityName, &assignedToName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Activity")
		return
	}
	if err != nil {
		h.log.Error("Failed to get activity", "error", err)
		response.InternalError(c, "Failed to get activity")
		return
	}

	resp := &entity.ActivityResponse{
		ID:           a.ID,
		ActivityType: a.ActivityType,
		Subject:      a.Subject,
		IsAllDay:     a.IsAllDay,
		Status:       a.Status,
		Priority:     a.Priority,
		IsPrivate:    a.IsPrivate,
		Attendees:    []entity.ActivityAttendee{},
		Tags:         []string{},
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
	}

	if description.Valid {
		resp.Description = &description.String
	}
	if leadID.Valid {
		lid, _ := uuid.Parse(leadID.String)
		resp.LeadID = &lid
	}
	if leadName.Valid {
		resp.LeadName = &leadName.String
	}
	if contactID.Valid {
		cid, _ := uuid.Parse(contactID.String)
		resp.ContactID = &cid
	}
	if contactName.Valid {
		resp.ContactName = &contactName.String
	}
	if opportunityID.Valid {
		oid, _ := uuid.Parse(opportunityID.String)
		resp.OpportunityID = &oid
	}
	if opportunityName.Valid {
		resp.OpportunityName = &opportunityName.String
	}
	if startDatetime.Valid {
		resp.StartDatetime = &startDatetime.Time
	}
	if endDatetime.Valid {
		resp.EndDatetime = &endDatetime.Time
	}
	if durationMinutes.Valid {
		dm := int(durationMinutes.Int64)
		resp.DurationMinutes = &dm
	}
	if location.Valid {
		resp.Location = &location.String
	}
	if outcome.Valid {
		o := entity.ActivityOutcome(outcome.String)
		resp.Outcome = &o
	}
	if outcomeNotes.Valid {
		resp.OutcomeNotes = &outcomeNotes.String
	}
	if assignedTo.Valid {
		aid, _ := uuid.Parse(assignedTo.String)
		resp.AssignedTo = &aid
	}
	if assignedToName.Valid {
		resp.AssignedToName = &assignedToName.String
	}
	if reminderDatetime.Valid {
		resp.ReminderDatetime = &reminderDatetime.Time
	}
	if len(attendees) > 0 {
		json.Unmarshal(attendees, &resp.Attendees)
	}
	if len(tags) > 0 {
		json.Unmarshal(tags, &resp.Tags)
	}

	response.Success(c, resp)
}

// UpdateActivity updates an existing activity
func (h *Handler) UpdateActivity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid activity ID")
		return
	}

	var input entity.UpdateActivityInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.ActivityType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("activity_type = $%d", argCount))
		args = append(args, *input.ActivityType)
	}
	if input.Subject != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("subject = $%d", argCount))
		args = append(args, *input.Subject)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.LeadID != nil {
		argCount++
		if *input.LeadID == "" {
			updates = append(updates, fmt.Sprintf("lead_id = $%d", argCount))
			args = append(args, nil)
		} else {
			lid, err := uuid.Parse(*input.LeadID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("lead_id = $%d", argCount))
				args = append(args, lid)
			}
		}
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
	if input.OpportunityID != nil {
		argCount++
		if *input.OpportunityID == "" {
			updates = append(updates, fmt.Sprintf("opportunity_id = $%d", argCount))
			args = append(args, nil)
		} else {
			oid, err := uuid.Parse(*input.OpportunityID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("opportunity_id = $%d", argCount))
				args = append(args, oid)
			}
		}
	}
	if input.StartDatetime != nil {
		argCount++
		if *input.StartDatetime == "" {
			updates = append(updates, fmt.Sprintf("start_datetime = $%d", argCount))
			args = append(args, nil)
		} else {
			if t, err := time.Parse(time.RFC3339, *input.StartDatetime); err == nil {
				updates = append(updates, fmt.Sprintf("start_datetime = $%d", argCount))
				args = append(args, t)
			}
		}
	}
	if input.EndDatetime != nil {
		argCount++
		if *input.EndDatetime == "" {
			updates = append(updates, fmt.Sprintf("end_datetime = $%d", argCount))
			args = append(args, nil)
		} else {
			if t, err := time.Parse(time.RFC3339, *input.EndDatetime); err == nil {
				updates = append(updates, fmt.Sprintf("end_datetime = $%d", argCount))
				args = append(args, t)
			}
		}
	}
	if input.DurationMinutes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("duration_minutes = $%d", argCount))
		args = append(args, *input.DurationMinutes)
	}
	if input.Location != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("location = $%d", argCount))
		args = append(args, *input.Location)
	}
	if input.IsAllDay != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_all_day = $%d", argCount))
		args = append(args, *input.IsAllDay)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)

		// If completed, set outcome
		if *input.Status == "completed" && input.Outcome == nil {
			argCount++
			updates = append(updates, fmt.Sprintf("outcome = $%d", argCount))
			args = append(args, "successful")
		}
	}
	if input.Outcome != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("outcome = $%d", argCount))
		args = append(args, *input.Outcome)
	}
	if input.OutcomeNotes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("outcome_notes = $%d", argCount))
		args = append(args, *input.OutcomeNotes)
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
	if input.Attendees != nil {
		argCount++
		attendeesJSON, _ := json.Marshal(input.Attendees)
		updates = append(updates, fmt.Sprintf("attendees = $%d", argCount))
		args = append(args, attendeesJSON)
	}
	if input.ReminderDatetime != nil {
		argCount++
		if *input.ReminderDatetime == "" {
			updates = append(updates, fmt.Sprintf("reminder_datetime = $%d", argCount))
			args = append(args, nil)
		} else {
			if t, err := time.Parse(time.RFC3339, *input.ReminderDatetime); err == nil {
				updates = append(updates, fmt.Sprintf("reminder_datetime = $%d", argCount))
				args = append(args, t)
			}
		}
	}
	if input.Priority != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("priority = $%d", argCount))
		args = append(args, *input.Priority)
	}
	if input.IsPrivate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_private = $%d", argCount))
		args = append(args, *input.IsPrivate)
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
		"UPDATE activities SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update activity", "error", err)
		response.InternalError(c, "Failed to update activity")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Activity")
		return
	}

	response.Success(c, gin.H{"message": "Activity updated successfully"})
}

// DeleteActivity soft deletes an activity
func (h *Handler) DeleteActivity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid activity ID")
		return
	}

	query := `
		UPDATE activities
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete activity", "error", err)
		response.InternalError(c, "Failed to delete activity")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Activity")
		return
	}

	response.Success(c, gin.H{"message": "Activity deleted successfully"})
}

// GetActivityStats returns activity statistics
func (h *Handler) GetActivityStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT
			COUNT(*) as total_activities,
			COUNT(*) FILTER (WHERE status = 'completed' AND DATE(updated_at) = CURRENT_DATE) as completed_today,
			COUNT(*) FILTER (WHERE status = 'planned' AND DATE(start_datetime) = CURRENT_DATE) as planned_today,
			COUNT(*) FILTER (WHERE status = 'planned' AND start_datetime < NOW()) as overdue_count
		FROM activities
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`

	var stats entity.ActivityStats
	err := h.db.QueryRow(query, tenantID).Scan(
		&stats.TotalActivities,
		&stats.CompletedToday,
		&stats.PlannedToday,
		&stats.OverdueCount,
	)

	if err != nil {
		h.log.Error("Failed to get activity stats", "error", err)
		response.InternalError(c, "Failed to get activity statistics")
		return
	}

	// Get by type
	typeQuery := `
		SELECT activity_type, COUNT(*)
		FROM activities
		WHERE tenant_id = $1 AND deleted_at IS NULL
		GROUP BY activity_type
	`
	typeRows, err := h.db.Query(typeQuery, tenantID)
	if err == nil {
		defer typeRows.Close()
		stats.ByType = make(map[string]int)
		for typeRows.Next() {
			var actType string
			var count int
			if typeRows.Scan(&actType, &count) == nil {
				stats.ByType[actType] = count
			}
		}
	}

	// Get by status
	statusQuery := `
		SELECT status, COUNT(*)
		FROM activities
		WHERE tenant_id = $1 AND deleted_at IS NULL
		GROUP BY status
	`
	statusRows, err := h.db.Query(statusQuery, tenantID)
	if err == nil {
		defer statusRows.Close()
		stats.ByStatus = make(map[string]int)
		for statusRows.Next() {
			var status string
			var count int
			if statusRows.Scan(&status, &count) == nil {
				stats.ByStatus[status] = count
			}
		}
	}

	response.Success(c, stats)
}

// GetActivityTimeline returns activities for a related record in timeline format
func (h *Handler) GetActivityTimeline(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	relatedType := c.Query("related_type") // lead, contact, opportunity
	relatedID := c.Query("related_id")

	if relatedType == "" || relatedID == "" {
		response.BadRequest(c, "related_type and related_id are required")
		return
	}

	// Validate related_id is a valid UUID
	rid, err := uuid.Parse(relatedID)
	if err != nil {
		response.BadRequest(c, "Invalid related_id")
		return
	}

	// Build query based on related type
	var condition string
	switch relatedType {
	case "lead":
		condition = "a.lead_id = $2"
	case "contact":
		condition = "a.contact_id = $2"
	case "opportunity":
		condition = "a.opportunity_id = $2"
	default:
		response.BadRequest(c, "Invalid related_type. Must be lead, contact, or opportunity")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit > 100 {
		limit = 100
	}

	query := fmt.Sprintf(`
		SELECT a.id, a.activity_type, a.subject, a.description, a.status,
			   COALESCE(a.start_datetime, a.created_at) as datetime,
			   a.related_type, a.related_id, a.created_by,
			   u.first_name || ' ' || u.last_name as created_by_name
		FROM activities a
		LEFT JOIN users u ON a.created_by = u.id
		WHERE a.tenant_id = $1 AND %s AND a.deleted_at IS NULL
		ORDER BY COALESCE(a.start_datetime, a.created_at) DESC
		LIMIT %d
	`, condition, limit)

	rows, err := h.db.Query(query, tenantID, rid)
	if err != nil {
		h.log.Error("Failed to get activity timeline", "error", err)
		response.InternalError(c, "Failed to get activity timeline")
		return
	}
	defer rows.Close()

	timeline := make([]*entity.ActivityTimeline, 0)
	for rows.Next() {
		var t entity.ActivityTimeline
		var description, relatedType, createdByName sql.NullString
		var relatedID, createdBy sql.NullString

		err := rows.Scan(
			&t.ID, &t.ActivityType, &t.Subject, &description, &t.Status,
			&t.Datetime, &relatedType, &relatedID, &createdBy, &createdByName,
		)
		if err != nil {
			h.log.Error("Failed to scan timeline activity", "error", err)
			continue
		}

		if description.Valid {
			t.Description = &description.String
		}
		if relatedType.Valid {
			t.RelatedType = &relatedType.String
		}
		if relatedID.Valid {
			rid, _ := uuid.Parse(relatedID.String)
			t.RelatedID = &rid
		}
		if createdBy.Valid {
			cid, _ := uuid.Parse(createdBy.String)
			t.CreatedBy = &cid
		}
		if createdByName.Valid {
			t.CreatedByName = &createdByName.String
		}

		timeline = append(timeline, &t)
	}

	response.Success(c, timeline)
}
