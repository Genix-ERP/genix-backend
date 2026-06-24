package handler

import (
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListTaskNotes returns the notes for a task, newest first.
func (h *Handler) ListTaskNotes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	taskID, err := uuid.Parse(c.Param("taskId"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, tenant_id, project_id, task_id, note, created_by, COALESCE(created_by_name, ''), created_at
		FROM project_task_notes
		WHERE task_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC`, taskID, tenantID)
	if err != nil {
		h.log.Error("Failed to fetch task notes", "error", err)
		response.InternalError(c, "Failed to fetch notes")
		return
	}
	defer rows.Close()

	notes := []entity.ProjectTaskNote{}
	for rows.Next() {
		var n entity.ProjectTaskNote
		var createdBy *uuid.UUID
		if err := rows.Scan(&n.ID, &n.TenantID, &n.ProjectID, &n.TaskID, &n.Note, &createdBy, &n.CreatedByName, &n.CreatedAt); err != nil {
			continue
		}
		n.CreatedBy = createdBy
		notes = append(notes, n)
	}

	response.Success(c, notes)
}

// CreateTaskNote adds a note to a task.
func (h *Handler) CreateTaskNote(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}
	taskID, err := uuid.Parse(c.Param("taskId"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	var input entity.CreateTaskNoteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	userID, _ := middleware.GetUserID(c)
	var createdBy interface{}
	if userID != (uuid.UUID{}) {
		createdBy = userID
	}

	// Prefer an explicit display name; otherwise fall back to the user's record.
	name := input.CreatedByName
	if name == "" && userID != (uuid.UUID{}) {
		var first, last string
		h.db.QueryRow(`SELECT COALESCE(first_name, ''), COALESCE(last_name, '') FROM users WHERE id = $1`, userID).Scan(&first, &last)
		name = first
		if last != "" {
			if name != "" {
				name += " "
			}
			name += last
		}
	}

	id := uuid.New()
	now := time.Now()
	_, err = h.db.Exec(`
		INSERT INTO project_task_notes (id, tenant_id, project_id, task_id, note, created_by, created_by_name, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, tenantID, projectID, taskID, input.Note, createdBy, name, now)
	if err != nil {
		h.log.Error("Failed to create task note", "error", err)
		response.InternalError(c, "Failed to create note")
		return
	}

	resp := entity.ProjectTaskNote{
		ID: id, TenantID: tenantID, ProjectID: projectID, TaskID: taskID,
		Note: input.Note, CreatedByName: name, CreatedAt: now,
	}
	if userID != (uuid.UUID{}) {
		resp.CreatedBy = &userID
	}
	response.Created(c, resp)
}
