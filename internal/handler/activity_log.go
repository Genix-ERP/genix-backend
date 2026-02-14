package handler

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// ACTIVITY LOG TYPES
// =====================================================

type ActivityLog struct {
	ID           int64             `json:"id" db:"id"`
	TenantID     uuid.UUID         `json:"tenant_id" db:"tenant_id"`
	ModelName    string            `json:"model_name" db:"model_name"`
	RecordID     int64             `json:"record_id" db:"record_id"`
	ActivityType string            `json:"activity_type" db:"activity_type"`
	Title        string            `json:"title" db:"title"`
	Description  string            `json:"description" db:"description"`
	FieldChanges json.RawMessage   `json:"field_changes" db:"field_changes"`
	OldStatus    string            `json:"old_status" db:"old_status"`
	NewStatus    string            `json:"new_status" db:"new_status"`
	UserID       *uuid.UUID        `json:"user_id" db:"user_id"`
	UserName     string            `json:"user_name" db:"user_name"`
	CreatedAt    time.Time         `json:"created_at" db:"created_at"`
	RelatedModel string            `json:"related_model" db:"related_model"`
	RelatedID    int64             `json:"related_id" db:"related_id"`
}

type RecordComment struct {
	ID          int64           `json:"id" db:"id"`
	TenantID    uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	ModelName   string          `json:"model_name" db:"model_name"`
	RecordID    int64           `json:"record_id" db:"record_id"`
	Content     string          `json:"content" db:"content"`
	ParentID    *int64          `json:"parent_id" db:"parent_id"`
	Mentions    json.RawMessage `json:"mentions" db:"mentions"`
	Attachments json.RawMessage `json:"attachments" db:"attachments"`
	UserID      *uuid.UUID      `json:"user_id" db:"user_id"`
	UserName    string          `json:"user_name" db:"user_name"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
	IsDeleted   bool            `json:"is_deleted" db:"is_deleted"`
	Replies     []RecordComment `json:"replies,omitempty"`
}

type ScheduledActivity struct {
	ID           int64      `json:"id" db:"id"`
	TenantID     uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ModelName    string     `json:"model_name" db:"model_name"`
	RecordID     int64      `json:"record_id" db:"record_id"`
	ActivityType string     `json:"activity_type" db:"activity_type"`
	Summary      string     `json:"summary" db:"summary"`
	Note         string     `json:"note" db:"note"`
	DueDate      time.Time  `json:"due_date" db:"due_date"`
	DueTime      *string    `json:"due_time" db:"due_time"`
	AssignedTo   *uuid.UUID `json:"assigned_to" db:"assigned_to"`
	AssignedBy   *uuid.UUID `json:"assigned_by" db:"assigned_by"`
	Status       string     `json:"status" db:"status"`
	CompletedAt  *time.Time `json:"completed_at" db:"completed_at"`
	CompletedBy  *uuid.UUID `json:"completed_by" db:"completed_by"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
	// Computed
	AssignedToName string `json:"assigned_to_name,omitempty" db:"assigned_to_name"`
}

// =====================================================
// ACTIVITY LOG HANDLERS
// =====================================================

// ListActivityLogs returns activity logs for a specific record
func (h *Handler) ListActivityLogs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	modelName := c.Param("model")
	recordID, err := strconv.ParseInt(c.Param("record_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid record ID")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	query := `
		SELECT id, tenant_id, model_name, record_id, activity_type,
		       COALESCE(title, '') as title, COALESCE(description, '') as description,
		       COALESCE(field_changes, '{}') as field_changes,
		       COALESCE(old_status, '') as old_status, COALESCE(new_status, '') as new_status,
		       user_id, COALESCE(user_name, '') as user_name, created_at,
		       COALESCE(related_model, '') as related_model, COALESCE(related_id, 0) as related_id
		FROM activity_logs
		WHERE tenant_id = $1 AND model_name = $2 AND record_id = $3
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5
	`

	rows, err := h.db.Query(query, tenantID, modelName, recordID, limit, offset)
	if err != nil {
		h.log.Error("Failed to list activity logs", "error", err)
		response.InternalError(c, "Failed to list activity logs")
		return
	}
	defer rows.Close()

	var logs []ActivityLog
	for rows.Next() {
		var log ActivityLog
		err := rows.Scan(
			&log.ID, &log.TenantID, &log.ModelName, &log.RecordID, &log.ActivityType,
			&log.Title, &log.Description, &log.FieldChanges,
			&log.OldStatus, &log.NewStatus,
			&log.UserID, &log.UserName, &log.CreatedAt,
			&log.RelatedModel, &log.RelatedID,
		)
		if err != nil {
			h.log.Error("Failed to scan activity log", "error", err)
			continue
		}
		logs = append(logs, log)
	}

	if logs == nil {
		logs = []ActivityLog{}
	}

	response.Success(c, logs)
}

// LogActivity creates a new activity log entry (internal helper, can also be called via API)
func (h *Handler) LogActivity(tenantID uuid.UUID, modelName string, recordID int64, activityType, title, description string, fieldChanges map[string]interface{}, oldStatus, newStatus string, userID *uuid.UUID, userName string) error {
	var fieldChangesJSON []byte
	if fieldChanges != nil {
		fieldChangesJSON, _ = json.Marshal(fieldChanges)
	}

	query := `
		INSERT INTO activity_logs (
			tenant_id, model_name, record_id, activity_type,
			title, description, field_changes,
			old_status, new_status, user_id, user_name, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7::jsonb, '{}'), $8, $9, $10, $11, NOW())
	`

	_, err := h.db.Exec(query,
		tenantID, modelName, recordID, activityType,
		nullString(title), nullString(description), fieldChangesJSON,
		nullString(oldStatus), nullString(newStatus), userID, userName,
	)
	return err
}

// =====================================================
// COMMENTS HANDLERS
// =====================================================

// ListComments returns comments for a specific record
func (h *Handler) ListComments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	modelName := c.Param("model")
	recordID, err := strconv.ParseInt(c.Param("record_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid record ID")
		return
	}

	query := `
		SELECT id, tenant_id, model_name, record_id, content, parent_id,
		       COALESCE(mentions, '[]') as mentions,
		       COALESCE(attachments, '[]') as attachments,
		       user_id, COALESCE(user_name, '') as user_name,
		       created_at, updated_at, is_deleted
		FROM record_comments
		WHERE tenant_id = $1 AND model_name = $2 AND record_id = $3 AND is_deleted = FALSE
		ORDER BY created_at ASC
	`

	rows, err := h.db.Query(query, tenantID, modelName, recordID)
	if err != nil {
		h.log.Error("Failed to list comments", "error", err)
		response.InternalError(c, "Failed to list comments")
		return
	}
	defer rows.Close()

	var comments []RecordComment
	for rows.Next() {
		var comment RecordComment
		err := rows.Scan(
			&comment.ID, &comment.TenantID, &comment.ModelName, &comment.RecordID,
			&comment.Content, &comment.ParentID, &comment.Mentions, &comment.Attachments,
			&comment.UserID, &comment.UserName, &comment.CreatedAt, &comment.UpdatedAt, &comment.IsDeleted,
		)
		if err != nil {
			h.log.Error("Failed to scan comment", "error", err)
			continue
		}
		comments = append(comments, comment)
	}

	// Build threaded structure
	commentMap := make(map[int64]*RecordComment)
	var rootComments []RecordComment

	for i := range comments {
		commentMap[comments[i].ID] = &comments[i]
	}

	for i := range comments {
		if comments[i].ParentID != nil {
			parent, exists := commentMap[*comments[i].ParentID]
			if exists {
				parent.Replies = append(parent.Replies, comments[i])
			}
		} else {
			rootComments = append(rootComments, comments[i])
		}
	}

	if rootComments == nil {
		rootComments = []RecordComment{}
	}

	response.Success(c, rootComments)
}

// CreateComment adds a new comment to a record
func (h *Handler) CreateComment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userIDVal, userOk := middleware.GetUserID(c)
	var userID *uuid.UUID
	if userOk {
		userID = &userIDVal
	}
	userName := c.GetString("user_name")
	if userName == "" {
		userName = "User"
	}

	modelName := c.Param("model")
	recordID, err := strconv.ParseInt(c.Param("record_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid record ID")
		return
	}

	var req struct {
		Content   string   `json:"content" binding:"required"`
		ParentID  *int64   `json:"parent_id"`
		Mentions  []string `json:"mentions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var mentionsJSON []byte
	if req.Mentions != nil {
		mentionsJSON, _ = json.Marshal(req.Mentions)
	}

	query := `
		INSERT INTO record_comments (
			tenant_id, model_name, record_id, content, parent_id,
			mentions, user_id, user_name, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, COALESCE($6::jsonb, '[]'), $7, $8, NOW(), NOW())
		RETURNING id, created_at
	`

	var commentID int64
	var createdAt time.Time
	err = h.db.QueryRow(query,
		tenantID, modelName, recordID, req.Content, req.ParentID,
		mentionsJSON, userID, userName,
	).Scan(&commentID, &createdAt)
	if err != nil {
		h.log.Error("Failed to create comment", "error", err)
		response.InternalError(c, "Failed to create comment")
		return
	}

	// Log the comment activity
	h.LogActivity(tenantID, modelName, recordID, "comment", "New comment added", req.Content, nil, "", "", userID, userName)

	response.Created(c, map[string]interface{}{
		"id":         commentID,
		"content":    req.Content,
		"user_name":  userName,
		"created_at": createdAt,
	})
}

// DeleteComment soft-deletes a comment
func (h *Handler) DeleteComment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	commentID, err := strconv.ParseInt(c.Param("comment_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid comment ID")
		return
	}

	query := `UPDATE record_comments SET is_deleted = TRUE, updated_at = NOW() WHERE id = $1 AND tenant_id = $2`
	_, err = h.db.Exec(query, commentID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete comment", "error", err)
		response.InternalError(c, "Failed to delete comment")
		return
	}

	response.Success(c, map[string]string{"message": "Comment deleted"})
}

// =====================================================
// SCHEDULED ACTIVITIES HANDLERS
// =====================================================

// ListScheduledActivities returns scheduled activities for a record
func (h *Handler) ListScheduledActivities(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	modelName := c.Param("model")
	recordID, err := strconv.ParseInt(c.Param("record_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid record ID")
		return
	}

	query := `
		SELECT sa.id, sa.tenant_id, sa.model_name, sa.record_id,
		       sa.activity_type, sa.summary, COALESCE(sa.note, '') as note,
		       sa.due_date, sa.due_time,
		       sa.assigned_to, sa.assigned_by, sa.status,
		       sa.completed_at, sa.completed_by,
		       sa.created_at, sa.updated_at,
		       COALESCE(u.first_name || ' ' || u.last_name, '') as assigned_to_name
		FROM scheduled_activities sa
		LEFT JOIN users u ON u.id = sa.assigned_to
		WHERE sa.tenant_id = $1 AND sa.model_name = $2 AND sa.record_id = $3
		ORDER BY sa.due_date ASC, sa.status ASC
	`

	rows, err := h.db.Query(query, tenantID, modelName, recordID)
	if err != nil {
		h.log.Error("Failed to list scheduled activities", "error", err)
		response.InternalError(c, "Failed to list scheduled activities")
		return
	}
	defer rows.Close()

	var activities []ScheduledActivity
	for rows.Next() {
		var activity ScheduledActivity
		err := rows.Scan(
			&activity.ID, &activity.TenantID, &activity.ModelName, &activity.RecordID,
			&activity.ActivityType, &activity.Summary, &activity.Note,
			&activity.DueDate, &activity.DueTime,
			&activity.AssignedTo, &activity.AssignedBy, &activity.Status,
			&activity.CompletedAt, &activity.CompletedBy,
			&activity.CreatedAt, &activity.UpdatedAt,
			&activity.AssignedToName,
		)
		if err != nil {
			h.log.Error("Failed to scan scheduled activity", "error", err)
			continue
		}
		activities = append(activities, activity)
	}

	if activities == nil {
		activities = []ScheduledActivity{}
	}

	response.Success(c, activities)
}

// CreateScheduledActivity creates a new scheduled activity
func (h *Handler) CreateScheduledActivity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userIDVal, userOk := middleware.GetUserID(c)
	var userID *uuid.UUID
	if userOk {
		userID = &userIDVal
	}

	modelName := c.Param("model")
	recordID, err := strconv.ParseInt(c.Param("record_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid record ID")
		return
	}

	var req struct {
		ActivityType string `json:"activity_type" binding:"required"`
		Summary      string `json:"summary" binding:"required"`
		Note         string `json:"note"`
		DueDate      string `json:"due_date" binding:"required"`
		DueTime      string `json:"due_time"`
		AssignedTo   string `json:"assigned_to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	dueDate, _ := time.Parse("2006-01-02", req.DueDate)

	var assignedTo *uuid.UUID
	if req.AssignedTo != "" {
		id, err := uuid.Parse(req.AssignedTo)
		if err == nil {
			assignedTo = &id
		}
	}

	var dueTime interface{}
	if req.DueTime != "" {
		dueTime = req.DueTime
	}

	query := `
		INSERT INTO scheduled_activities (
			tenant_id, model_name, record_id,
			activity_type, summary, note,
			due_date, due_time, assigned_to, assigned_by,
			status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', NOW(), NOW())
		RETURNING id
	`

	var activityID int64
	err = h.db.QueryRow(query,
		tenantID, modelName, recordID,
		req.ActivityType, req.Summary, nullString(req.Note),
		dueDate, dueTime, assignedTo, userID,
	).Scan(&activityID)
	if err != nil {
		h.log.Error("Failed to create scheduled activity", "error", err)
		response.InternalError(c, "Failed to create scheduled activity")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":      activityID,
		"message": "Activity scheduled successfully",
	})
}

// CompleteScheduledActivity marks an activity as done
func (h *Handler) CompleteScheduledActivity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userIDVal, userOk := middleware.GetUserID(c)
	var userID *uuid.UUID
	if userOk {
		userID = &userIDVal
	}

	activityID, err := strconv.ParseInt(c.Param("activity_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid activity ID")
		return
	}

	query := `
		UPDATE scheduled_activities
		SET status = 'done', completed_at = NOW(), completed_by = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3
	`
	_, err = h.db.Exec(query, userID, activityID, tenantID)
	if err != nil {
		h.log.Error("Failed to complete activity", "error", err)
		response.InternalError(c, "Failed to complete activity")
		return
	}

	response.Success(c, map[string]string{"message": "Activity marked as done"})
}

// ListMyActivities returns all pending activities assigned to the current user
func (h *Handler) ListMyActivities(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, userOk := middleware.GetUserID(c)
	if !userOk || userID == uuid.Nil {
		response.Unauthorized(c, "User not found")
		return
	}

	status := c.DefaultQuery("status", "pending")

	query := `
		SELECT sa.id, sa.tenant_id, sa.model_name, sa.record_id,
		       sa.activity_type, sa.summary, COALESCE(sa.note, '') as note,
		       sa.due_date, sa.due_time,
		       sa.assigned_to, sa.assigned_by, sa.status,
		       sa.completed_at, sa.completed_by,
		       sa.created_at, sa.updated_at,
		       '' as assigned_to_name
		FROM scheduled_activities sa
		WHERE sa.tenant_id = $1 AND sa.assigned_to = $2 AND sa.status = $3
		ORDER BY sa.due_date ASC
		LIMIT 100
	`

	rows, err := h.db.Query(query, tenantID, userID, status)
	if err != nil {
		h.log.Error("Failed to list my activities", "error", err)
		response.InternalError(c, "Failed to list activities")
		return
	}
	defer rows.Close()

	var activities []ScheduledActivity
	for rows.Next() {
		var activity ScheduledActivity
		err := rows.Scan(
			&activity.ID, &activity.TenantID, &activity.ModelName, &activity.RecordID,
			&activity.ActivityType, &activity.Summary, &activity.Note,
			&activity.DueDate, &activity.DueTime,
			&activity.AssignedTo, &activity.AssignedBy, &activity.Status,
			&activity.CompletedAt, &activity.CompletedBy,
			&activity.CreatedAt, &activity.UpdatedAt,
			&activity.AssignedToName,
		)
		if err != nil {
			h.log.Error("Failed to scan activity", "error", err)
			continue
		}
		activities = append(activities, activity)
	}

	if activities == nil {
		activities = []ScheduledActivity{}
	}

	response.Success(c, activities)
}
