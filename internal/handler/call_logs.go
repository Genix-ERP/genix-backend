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
// CALL LOG HANDLERS
// =====================================================

// ListCallLogs returns a paginated list of call logs
func (h *Handler) ListCallLogs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	contactID := c.Query("contact_id")
	callType := c.Query("call_type")
	callOutcome := c.Query("call_outcome")
	agentID := c.Query("agent_id")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// Build query
	baseQuery := `
		SELECT cl.id, cl.tenant_id, cl.contact_id, cl.contact_person_id,
			   cl.caller_number, cl.receiver_number, cl.call_type,
			   cl.call_start_time, cl.call_end_time, cl.call_duration,
			   cl.call_outcome, cl.recording_url, cl.notes,
			   cl.sentiment_score, cl.summary, cl.pbx_call_id,
			   cl.agent_id, cl.created_at, cl.updated_at,
			   c.name as contact_name,
			   u.first_name || ' ' || u.last_name as agent_name
		FROM call_logs cl
		LEFT JOIN contacts c ON cl.contact_id = c.id
		LEFT JOIN users u ON cl.agent_id = u.id
		WHERE cl.tenant_id = $1 AND cl.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM call_logs cl WHERE cl.tenant_id = $1 AND cl.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND cl.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND cl.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if contactID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND cl.contact_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND cl.contact_id = $%d", argCount)
		args = append(args, contactID)
	}

	if callType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND cl.call_type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND cl.call_type = $%d", argCount)
		args = append(args, callType)
	}

	if callOutcome != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND cl.call_outcome = $%d", argCount)
		countQuery += fmt.Sprintf(" AND cl.call_outcome = $%d", argCount)
		args = append(args, callOutcome)
	}

	if agentID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND cl.agent_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND cl.agent_id = $%d", argCount)
		args = append(args, agentID)
	}

	if dateFrom != "" {
		if t, err := time.Parse(time.RFC3339, dateFrom); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND cl.call_start_time >= $%d", argCount)
			countQuery += fmt.Sprintf(" AND cl.call_start_time >= $%d", argCount)
			args = append(args, t)
		}
	}

	if dateTo != "" {
		if t, err := time.Parse(time.RFC3339, dateTo); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND cl.call_start_time <= $%d", argCount)
			countQuery += fmt.Sprintf(" AND cl.call_start_time <= $%d", argCount)
			args = append(args, t)
		}
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (cl.caller_number ILIKE $%d OR cl.receiver_number ILIKE $%d OR cl.notes ILIKE $%d OR c.name ILIKE $%d)", argCount, argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count call logs", "error", err)
		response.InternalError(c, "Failed to list call logs")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY cl.call_start_time DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list call logs", "error", err)
		response.InternalError(c, "Failed to list call logs")
		return
	}
	defer rows.Close()

	callLogs := make([]*entity.CallLogResponse, 0)
	for rows.Next() {
		var cl entity.CallLog
		var contactID, contactPersonID, agentID sql.NullString
		var receiverNumber, recordingURL, notes, summary, pbxCallID sql.NullString
		var callEndTime sql.NullTime
		var sentimentScore sql.NullFloat64
		var contactName, agentName sql.NullString

		err := rows.Scan(
			&cl.ID, &cl.TenantID, &contactID, &contactPersonID,
			&cl.CallerNumber, &receiverNumber, &cl.CallType,
			&cl.CallStartTime, &callEndTime, &cl.CallDuration,
			&cl.CallOutcome, &recordingURL, &notes,
			&sentimentScore, &summary, &pbxCallID,
			&agentID, &cl.CreatedAt, &cl.UpdatedAt,
			&contactName, &agentName,
		)
		if err != nil {
			h.log.Error("Failed to scan call log", "error", err)
			continue
		}

		resp := &entity.CallLogResponse{
			ID:            cl.ID,
			CallerNumber:  cl.CallerNumber,
			CallType:      cl.CallType,
			CallStartTime: cl.CallStartTime,
			CallDuration:  cl.CallDuration,
			CallOutcome:   cl.CallOutcome,
			CreatedAt:     cl.CreatedAt,
		}

		if contactID.Valid {
			cid, _ := uuid.Parse(contactID.String)
			resp.ContactID = &cid
		}
		if receiverNumber.Valid {
			resp.ReceiverNumber = receiverNumber.String
		}
		if callEndTime.Valid {
			resp.CallEndTime = &callEndTime.Time
		}
		if recordingURL.Valid {
			resp.RecordingURL = &recordingURL.String
		}
		if notes.Valid {
			resp.Notes = &notes.String
		}
		if summary.Valid {
			resp.Summary = &summary.String
		}
		if sentimentScore.Valid {
			resp.SentimentScore = &sentimentScore.Float64
		}
		if agentID.Valid {
			aid, _ := uuid.Parse(agentID.String)
			resp.AgentID = &aid
		}
		if contactName.Valid {
			resp.ContactName = &contactName.String
		}
		if agentName.Valid {
			resp.AgentName = &agentName.String
		}

		callLogs = append(callLogs, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, callLogs, pagination)
}

// CreateCallLog creates a new call log
func (h *Handler) CreateCallLog(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateCallLogInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse optional UUIDs
	var contactID, contactPersonID *uuid.UUID
	if input.ContactID != "" {
		cid, err := uuid.Parse(input.ContactID)
		if err == nil {
			contactID = &cid
		}
	}
	if input.ContactPersonID != "" {
		cpid, err := uuid.Parse(input.ContactPersonID)
		if err == nil {
			contactPersonID = &cpid
		}
	}

	id := uuid.New()
	now := time.Now()

	// Set defaults
	callStartTime := now
	if input.CallStartTime != nil {
		callStartTime = *input.CallStartTime
	}

	callOutcome := entity.CallOutcomeInitiated
	if input.CallOutcome != "" {
		callOutcome = input.CallOutcome
	}

	// Prepare optional strings
	var receiverNumber, recordingURL, notes, summary, pbxCallID *string
	if input.ReceiverNumber != "" {
		receiverNumber = &input.ReceiverNumber
	}
	if input.RecordingURL != "" {
		recordingURL = &input.RecordingURL
	}
	if input.Notes != "" {
		notes = &input.Notes
	}
	if input.Summary != "" {
		summary = &input.Summary
	}
	if input.PBXCallID != "" {
		pbxCallID = &input.PBXCallID
	}

	query := `
		INSERT INTO call_logs (
			id, tenant_id, contact_id, contact_person_id,
			caller_number, receiver_number, call_type,
			call_start_time, call_end_time, call_duration,
			call_outcome, recording_url, notes,
			sentiment_score, summary, pbx_call_id,
			agent_id, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, contactID, contactPersonID,
		input.CallerNumber, receiverNumber, input.CallType,
		callStartTime, input.CallEndTime, input.CallDuration,
		callOutcome, recordingURL, notes,
		input.SentimentScore, summary, pbxCallID,
		userID, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create call log", "error", err)
		response.InternalError(c, "Failed to create call log")
		return
	}

	resp := &entity.CallLogResponse{
		ID:             id,
		ContactID:      contactID,
		CallerNumber:   input.CallerNumber,
		ReceiverNumber: input.ReceiverNumber,
		CallType:       input.CallType,
		CallStartTime:  callStartTime,
		CallEndTime:    input.CallEndTime,
		CallDuration:   input.CallDuration,
		CallOutcome:    callOutcome,
		RecordingURL:   recordingURL,
		Notes:          notes,
		SentimentScore: input.SentimentScore,
		Summary:        summary,
		AgentID:        &userID,
		CreatedAt:      now,
	}

	response.Created(c, resp)
}

// GetCallLog returns a single call log by ID
func (h *Handler) GetCallLog(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid call log ID")
		return
	}

	query := `
		SELECT cl.id, cl.tenant_id, cl.contact_id, cl.contact_person_id,
			   cl.caller_number, cl.receiver_number, cl.call_type,
			   cl.call_start_time, cl.call_end_time, cl.call_duration,
			   cl.call_outcome, cl.recording_url, cl.notes,
			   cl.sentiment_score, cl.summary, cl.pbx_call_id,
			   cl.agent_id, cl.created_at, cl.updated_at,
			   c.name as contact_name,
			   u.first_name || ' ' || u.last_name as agent_name
		FROM call_logs cl
		LEFT JOIN contacts c ON cl.contact_id = c.id
		LEFT JOIN users u ON cl.agent_id = u.id
		WHERE cl.id = $1 AND cl.tenant_id = $2 AND cl.deleted_at IS NULL
	`

	var cl entity.CallLog
	var contactID, contactPersonID, agentID sql.NullString
	var receiverNumber, recordingURL, notes, summary, pbxCallID sql.NullString
	var callEndTime sql.NullTime
	var sentimentScore sql.NullFloat64
	var contactName, agentName sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&cl.ID, &cl.TenantID, &contactID, &contactPersonID,
		&cl.CallerNumber, &receiverNumber, &cl.CallType,
		&cl.CallStartTime, &callEndTime, &cl.CallDuration,
		&cl.CallOutcome, &recordingURL, &notes,
		&sentimentScore, &summary, &pbxCallID,
		&agentID, &cl.CreatedAt, &cl.UpdatedAt,
		&contactName, &agentName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Call log")
		return
	}
	if err != nil {
		h.log.Error("Failed to get call log", "error", err)
		response.InternalError(c, "Failed to get call log")
		return
	}

	resp := &entity.CallLogResponse{
		ID:            cl.ID,
		CallerNumber:  cl.CallerNumber,
		CallType:      cl.CallType,
		CallStartTime: cl.CallStartTime,
		CallDuration:  cl.CallDuration,
		CallOutcome:   cl.CallOutcome,
		CreatedAt:     cl.CreatedAt,
	}

	if contactID.Valid {
		cid, _ := uuid.Parse(contactID.String)
		resp.ContactID = &cid
	}
	if receiverNumber.Valid {
		resp.ReceiverNumber = receiverNumber.String
	}
	if callEndTime.Valid {
		resp.CallEndTime = &callEndTime.Time
	}
	if recordingURL.Valid {
		resp.RecordingURL = &recordingURL.String
	}
	if notes.Valid {
		resp.Notes = &notes.String
	}
	if summary.Valid {
		resp.Summary = &summary.String
	}
	if sentimentScore.Valid {
		resp.SentimentScore = &sentimentScore.Float64
	}
	if agentID.Valid {
		aid, _ := uuid.Parse(agentID.String)
		resp.AgentID = &aid
	}
	if contactName.Valid {
		resp.ContactName = &contactName.String
	}
	if agentName.Valid {
		resp.AgentName = &agentName.String
	}

	response.Success(c, resp)
}

// UpdateCallLog updates an existing call log
func (h *Handler) UpdateCallLog(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid call log ID")
		return
	}

	var input entity.UpdateCallLogInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.CallEndTime != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("call_end_time = $%d", argCount))
		args = append(args, *input.CallEndTime)
	}
	if input.CallDuration != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("call_duration = $%d", argCount))
		args = append(args, *input.CallDuration)
	}
	if input.CallOutcome != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("call_outcome = $%d", argCount))
		args = append(args, *input.CallOutcome)
	}
	if input.RecordingURL != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("recording_url = $%d", argCount))
		args = append(args, *input.RecordingURL)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.SentimentScore != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("sentiment_score = $%d", argCount))
		args = append(args, *input.SentimentScore)
	}
	if input.Summary != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("summary = $%d", argCount))
		args = append(args, *input.Summary)
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
		"UPDATE call_logs SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update call log", "error", err)
		response.InternalError(c, "Failed to update call log")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Call log")
		return
	}

	response.Success(c, gin.H{"message": "Call log updated successfully"})
}

// DeleteCallLog soft deletes a call log
func (h *Handler) DeleteCallLog(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid call log ID")
		return
	}

	query := `
		UPDATE call_logs
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete call log", "error", err)
		response.InternalError(c, "Failed to delete call log")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Call log")
		return
	}

	response.Success(c, gin.H{"message": "Call log deleted successfully"})
}

// GetCallLogStats returns aggregated call statistics
func (h *Handler) GetCallLogStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse date range filters
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	query := `
		SELECT
			COUNT(*) as total_calls,
			COUNT(*) FILTER (WHERE call_type = 'inbound') as inbound_calls,
			COUNT(*) FILTER (WHERE call_type = 'outbound') as outbound_calls,
			COUNT(*) FILTER (WHERE call_type = 'missed') as missed_calls,
			COALESCE(SUM(call_duration), 0) as total_duration,
			COALESCE(AVG(call_duration), 0) as avg_duration,
			COALESCE(
				COUNT(*) FILTER (WHERE call_outcome = 'answered' OR call_outcome = 'completed')::float /
				NULLIF(COUNT(*) FILTER (WHERE call_type != 'missed'), 0) * 100,
				0
			) as answer_rate,
			COALESCE(AVG(sentiment_score), 0) as avg_sentiment
		FROM call_logs
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if dateFrom != "" {
		if t, err := time.Parse(time.RFC3339, dateFrom); err == nil {
			argCount++
			query = strings.Replace(query, "WHERE tenant_id = $1", fmt.Sprintf("WHERE tenant_id = $1 AND call_start_time >= $%d", argCount), 1)
			args = append(args, t)
		}
	}

	if dateTo != "" {
		if t, err := time.Parse(time.RFC3339, dateTo); err == nil {
			argCount++
			query += fmt.Sprintf(" AND call_start_time <= $%d", argCount)
			args = append(args, t)
		}
	}

	var stats entity.CallLogStats
	err := h.db.QueryRow(query, args...).Scan(
		&stats.TotalCalls,
		&stats.InboundCalls,
		&stats.OutboundCalls,
		&stats.MissedCalls,
		&stats.TotalDuration,
		&stats.AvgDuration,
		&stats.AnswerRate,
		&stats.AvgSentiment,
	)

	if err != nil {
		h.log.Error("Failed to get call log stats", "error", err)
		response.InternalError(c, "Failed to get call statistics")
		return
	}

	response.Success(c, stats)
}
