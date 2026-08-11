package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Vazifalar (task management) — task-level endpoints.
// Boards/columns/stats live in task_boards.go.

var boardTaskPriorities = map[string]bool{"low": true, "normal": true, "high": true, "urgent": true}

// parseBoardScope pulls tenant + board from the request and verifies ownership.
func (h *Handler) parseBoardScope(c *gin.Context) (tenantID, boardID uuid.UUID, ok bool) {
	tenantID, tok := middleware.GetTenantID(c)
	if !tok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return tenantID, boardID, false
	}
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid board ID")
		return tenantID, boardID, false
	}
	if !h.taskBoardOwned(tenantID, boardID) {
		response.NotFound(c, "Board")
		return tenantID, boardID, false
	}
	return tenantID, boardID, true
}

// parseTaskScope additionally resolves and verifies the task.
func (h *Handler) parseTaskScope(c *gin.Context) (tenantID, boardID, taskID uuid.UUID, ok bool) {
	tenantID, boardID, sok := h.parseBoardScope(c)
	if !sok {
		return tenantID, boardID, taskID, false
	}
	taskID, err := uuid.Parse(c.Param("taskId"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return tenantID, boardID, taskID, false
	}
	var one int
	if err := h.db.QueryRow(`
		SELECT 1 FROM tasks WHERE id = $1 AND board_id = $2 AND tenant_id = $3
	`, taskID, boardID, tenantID).Scan(&one); err != nil {
		response.NotFound(c, "Task")
		return tenantID, boardID, taskID, false
	}
	return tenantID, boardID, taskID, true
}

func parseDateInput(s string) (interface{}, error) {
	if s == "" {
		return nil, nil
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ── Tasks ───────────────────────────────────────────────────────────────────

// ListBoardTasks is the list-view endpoint with server-side filters
func (h *Handler) ListBoardTasks(c *gin.Context) {
	tenantID, boardID, ok := h.parseBoardScope(c)
	if !ok {
		return
	}

	extra := ""
	args := []interface{}{}
	n := 2
	add := func(cond string, v interface{}) {
		n++
		extra += " AND " + cond + " $" + itoa(n)
		args = append(args, v)
	}

	if v := c.Query("column_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			response.BadRequest(c, "Invalid column_id")
			return
		}
		add("t.column_id =", id)
	}
	if v := c.Query("priority"); v != "" {
		if !boardTaskPriorities[v] {
			response.BadRequest(c, "Invalid priority")
			return
		}
		add("t.priority =", v)
	}
	if v := c.Query("assignee_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			response.BadRequest(c, "Invalid assignee_id")
			return
		}
		add("t.id IN (SELECT task_id FROM task_assignees WHERE employee_id =", id)
		extra += ")"
	}
	if v := c.Query("search"); v != "" {
		add("t.title ILIKE", "%"+v+"%")
	}
	if c.Query("overdue") == "true" {
		extra += " AND t.due_date < CURRENT_DATE AND t.completed_at IS NULL"
	}
	switch c.Query("state") {
	case "open":
		extra += " AND t.completed_at IS NULL"
	case "done":
		extra += " AND t.completed_at IS NOT NULL"
	}

	tasks, err := h.loadBoardTasks(tenantID, boardID, extra, args...)
	if err != nil {
		h.log.Error("list board tasks failed", "error", err)
		response.InternalError(c, "Failed to list tasks")
		return
	}
	response.Success(c, tasks)
}

// CreateBoardTask creates a task at the end of a column
func (h *Handler) CreateBoardTask(c *gin.Context) {
	tenantID, boardID, ok := h.parseBoardScope(c)
	if !ok {
		return
	}
	actorID, actorName := h.taskActor(c)

	var input entity.CreateBoardTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	if input.Priority == "" {
		input.Priority = "normal"
	}
	if !boardTaskPriorities[input.Priority] {
		response.BadRequest(c, "Invalid priority (low|normal|high|urgent)")
		return
	}
	dueDate, err := parseDateInput(input.DueDate)
	if err != nil {
		response.BadRequest(c, "Invalid due_date (expected YYYY-MM-DD)")
		return
	}
	startDate, err := parseDateInput(input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date (expected YYYY-MM-DD)")
		return
	}

	// Resolve target column: explicit or the board's first
	var columnID uuid.UUID
	var isDone bool
	if input.ColumnID != "" {
		colID, err := uuid.Parse(input.ColumnID)
		if err != nil {
			response.BadRequest(c, "Invalid column_id")
			return
		}
		if err := h.db.QueryRow(`
			SELECT id, is_done_column FROM task_columns WHERE id = $1 AND board_id = $2 AND tenant_id = $3
		`, colID, boardID, tenantID).Scan(&columnID, &isDone); err != nil {
			response.BadRequest(c, "Column does not belong to this board")
			return
		}
	} else {
		if err := h.db.QueryRow(`
			SELECT id, is_done_column FROM task_columns WHERE board_id = $1 ORDER BY position LIMIT 1
		`, boardID).Scan(&columnID, &isDone); err != nil {
			response.BadRequest(c, "Board has no columns")
			return
		}
	}

	// Validate assignees up front (all must be live employees of this tenant)
	assignees := []uuid.UUID{}
	for _, idStr := range input.AssigneeIDs {
		empID, err := uuid.Parse(idStr)
		if err != nil {
			response.BadRequest(c, "Invalid assignee id: "+idStr)
			return
		}
		var one int
		if err := h.db.QueryRow(`
			SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		`, empID, tenantID).Scan(&one); err != nil {
			response.BadRequest(c, "Assignee is not an employee of this company: "+idStr)
			return
		}
		assignees = append(assignees, empID)
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create task")
		return
	}
	defer tx.Rollback()

	taskID := uuid.New()
	var createdBy interface{}
	if actorID != uuid.Nil {
		createdBy = actorID
	}
	var completedAt interface{}
	if isDone {
		completedAt = time.Now()
	}
	if _, err := tx.Exec(`
		INSERT INTO tasks (id, tenant_id, board_id, column_id, title, description, priority,
		                   due_date, start_date, position, completed_at, created_by)
		SELECT $1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, COALESCE(MAX(position) + 1, 0), $10, $11
		FROM tasks WHERE column_id = $4
	`, taskID, tenantID, boardID, columnID, input.Title, input.Description, input.Priority,
		dueDate, startDate, completedAt, createdBy); err != nil {
		h.log.Error("create task failed", "error", err)
		response.InternalError(c, "Failed to create task")
		return
	}

	for _, empID := range assignees {
		if _, err := tx.Exec(`
			INSERT INTO task_assignees (task_id, employee_id, tenant_id, assigned_by)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING
		`, taskID, empID, tenantID, createdBy); err != nil {
			response.InternalError(c, "Failed to assign task")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to create task")
		return
	}

	h.logTaskActivity(tenantID, taskID, actorID, actorName, "created",
		nil, map[string]interface{}{"title": input.Title})

	if len(assignees) > 0 {
		var boardName string
		_ = h.db.QueryRow(`SELECT name FROM task_boards WHERE id = $1`, boardID).Scan(&boardName)
		go h.notifyTaskAssigned(tenantID, assignees, actorID, taskID, boardID, input.Title, boardName)
		h.EmitWorkflowEvent(tenantID, "task.assigned", map[string]interface{}{
			"record_id":  taskID.String(),
			"task_title": input.Title,
			"board_id":   boardID.String(),
			"board_name": boardName,
			"priority":   input.Priority,
		})
	}

	task, err := h.loadTaskDetail(tenantID, boardID, taskID)
	if err != nil {
		response.InternalError(c, "Failed to load task")
		return
	}
	response.Created(c, task)
}

// loadTaskDetail returns the full detail payload of one task
func (h *Handler) loadTaskDetail(tenantID, boardID, taskID uuid.UUID) (gin.H, error) {
	var t entity.BoardTask
	err := h.db.QueryRow(`
		SELECT t.id, t.board_id, t.column_id, t.title, t.description, t.priority,
		       t.due_date, t.start_date, t.position, t.completed_at, t.archived_at,
		       t.created_by, t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id),
		       (SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id AND ci.is_done),
		       (SELECT COUNT(*) FROM task_comments tc WHERE tc.task_id = t.id),
		       (SELECT COUNT(*) FROM attachments a WHERE a.entity_type = 'task' AND a.entity_id = t.id AND a.deleted_at IS NULL),
		       (t.due_date IS NOT NULL AND t.due_date < CURRENT_DATE AND t.completed_at IS NULL)
		FROM tasks t
		WHERE t.id = $1 AND t.board_id = $2 AND t.tenant_id = $3
	`, taskID, boardID, tenantID).Scan(&t.ID, &t.BoardID, &t.ColumnID, &t.Title, &t.Description, &t.Priority,
		&t.DueDate, &t.StartDate, &t.Position, &t.CompletedAt, &t.ArchivedAt,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
		&t.ChecklistTotal, &t.ChecklistDone, &t.CommentCount, &t.AttachmentCount, &t.IsOverdue)
	if err != nil {
		return nil, err
	}
	t.Assignees = []entity.TaskAssigneeInfo{}

	aRows, err := h.db.Query(`
		SELECT e.id, e.user_id, TRIM(e.first_name || ' ' || e.last_name),
		       COALESCE(jp.name, e.job_title, ''), COALESCE(d.name, ''),
		       (e.deleted_at IS NULL AND e.status <> 'terminated')
		FROM task_assignees ta
		JOIN employees e ON e.id = ta.employee_id
		LEFT JOIN job_positions jp ON jp.id = e.job_position_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE ta.task_id = $1
		ORDER BY ta.created_at
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer aRows.Close()
	for aRows.Next() {
		var info entity.TaskAssigneeInfo
		if err := aRows.Scan(&info.EmployeeID, &info.UserID, &info.Name, &info.JobTitle, &info.Department, &info.IsActive); err != nil {
			return nil, err
		}
		t.Assignees = append(t.Assignees, info)
	}

	checklist := []entity.TaskChecklistItem{}
	cRows, err := h.db.Query(`
		SELECT id, task_id, title, is_done, position FROM task_checklist_items
		WHERE task_id = $1 ORDER BY position, created_at
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer cRows.Close()
	for cRows.Next() {
		var item entity.TaskChecklistItem
		if err := cRows.Scan(&item.ID, &item.TaskID, &item.Title, &item.IsDone, &item.Position); err != nil {
			return nil, err
		}
		checklist = append(checklist, item)
	}

	comments, err := h.loadTaskComments(taskID)
	if err != nil {
		return nil, err
	}

	attachments, err := h.loadTaskAttachments(tenantID, taskID)
	if err != nil {
		return nil, err
	}

	links := []entity.TaskLink{}
	lRows, err := h.db.Query(`
		SELECT id, task_id, linked_module, linked_id, created_at FROM task_links
		WHERE task_id = $1 ORDER BY created_at
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer lRows.Close()
	for lRows.Next() {
		var l entity.TaskLink
		if err := lRows.Scan(&l.ID, &l.TaskID, &l.LinkedModule, &l.LinkedID, &l.CreatedAt); err != nil {
			return nil, err
		}
		links = append(links, l)
	}

	return gin.H{
		"task":        t,
		"checklist":   checklist,
		"comments":    comments,
		"attachments": attachments,
		"links":       links,
	}, nil
}

// GetBoardTask returns full task detail (side panel payload)
func (h *Handler) GetBoardTask(c *gin.Context) {
	tenantID, boardID, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	detail, err := h.loadTaskDetail(tenantID, boardID, taskID)
	if err != nil {
		h.log.Error("get task failed", "error", err)
		response.InternalError(c, "Failed to load task")
		return
	}
	response.Success(c, detail)
}

// UpdateBoardTask updates task fields; `archived` toggles archived_at
func (h *Handler) UpdateBoardTask(c *gin.Context) {
	tenantID, boardID, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	actorID, actorName := h.taskActor(c)

	var input entity.UpdateBoardTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	changes := map[string]interface{}{}
	set := "updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{taskID}
	n := 1
	add := func(field string, v interface{}) {
		n++
		set += ", " + field + " = $" + itoa(n)
		args = append(args, v)
		changes[field] = v
	}

	if input.Title != nil {
		if *input.Title == "" {
			response.BadRequest(c, "Title cannot be empty")
			return
		}
		add("title", *input.Title)
	}
	if input.Description != nil {
		add("description", nullIfEmpty(*input.Description))
	}
	if input.Priority != nil {
		if !boardTaskPriorities[*input.Priority] {
			response.BadRequest(c, "Invalid priority (low|normal|high|urgent)")
			return
		}
		add("priority", *input.Priority)
	}
	if input.DueDate != nil {
		d, err := parseDateInput(*input.DueDate)
		if err != nil {
			response.BadRequest(c, "Invalid due_date (expected YYYY-MM-DD)")
			return
		}
		add("due_date", d)
		// A changed deadline re-arms the one-shot overdue notification.
		set += ", overdue_notified_at = NULL"
	}
	if input.StartDate != nil {
		d, err := parseDateInput(*input.StartDate)
		if err != nil {
			response.BadRequest(c, "Invalid start_date (expected YYYY-MM-DD)")
			return
		}
		add("start_date", d)
	}
	if input.Archived != nil {
		if *input.Archived {
			set += ", archived_at = COALESCE(archived_at, CURRENT_TIMESTAMP)"
			changes["archived"] = true
		} else {
			set += ", archived_at = NULL"
			changes["archived"] = false
		}
	}

	if _, err := h.db.Exec(`UPDATE tasks SET `+set+` WHERE id = $1`, args...); err != nil {
		h.log.Error("update task failed", "error", err)
		response.InternalError(c, "Failed to update task")
		return
	}

	if len(changes) > 0 {
		action := "updated"
		if a, has := changes["archived"]; has && len(changes) == 1 {
			if a == true {
				action = "archived"
			} else {
				action = "unarchived"
			}
		}
		h.logTaskActivity(tenantID, taskID, actorID, actorName, action, nil, changes)
	}

	detail, err := h.loadTaskDetail(tenantID, boardID, taskID)
	if err != nil {
		response.InternalError(c, "Failed to load task")
		return
	}
	response.Success(c, detail)
}

// MoveBoardTask moves/reorders a card. Dropping into the done column stamps
// completed_at; moving out clears it.
func (h *Handler) MoveBoardTask(c *gin.Context) {
	tenantID, boardID, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	actorID, actorName := h.taskActor(c)

	var input entity.MoveBoardTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	newColID, err := uuid.Parse(input.ColumnID)
	if err != nil {
		response.BadRequest(c, "Invalid column_id")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to move task")
		return
	}
	defer tx.Rollback()

	var oldColID uuid.UUID
	var oldPos int
	var completedAt sql.NullTime
	if err := tx.QueryRow(`
		SELECT column_id, position, completed_at FROM tasks
		WHERE id = $1 AND board_id = $2 AND tenant_id = $3 FOR UPDATE
	`, taskID, boardID, tenantID).Scan(&oldColID, &oldPos, &completedAt); err != nil {
		response.NotFound(c, "Task")
		return
	}

	var targetDone bool
	var targetName string
	var wipLimit sql.NullInt64
	if err := tx.QueryRow(`
		SELECT is_done_column, name, wip_limit FROM task_columns
		WHERE id = $1 AND board_id = $2 AND tenant_id = $3
	`, newColID, boardID, tenantID).Scan(&targetDone, &targetName, &wipLimit); err != nil {
		response.BadRequest(c, "Column does not belong to this board")
		return
	}

	// Clamp requested position into the valid range for the target column
	var targetCount int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM tasks WHERE column_id = $1 AND id <> $2
	`, newColID, taskID).Scan(&targetCount); err != nil {
		response.InternalError(c, "Failed to move task")
		return
	}
	newPos := input.Position
	if newPos < 0 {
		newPos = 0
	}
	if newPos > targetCount {
		newPos = targetCount
	}

	if oldColID == newColID {
		if newPos != oldPos {
			if newPos > oldPos {
				if _, err := tx.Exec(`
					UPDATE tasks SET position = position - 1
					WHERE column_id = $1 AND position > $2 AND position <= $3
				`, oldColID, oldPos, newPos); err != nil {
					response.InternalError(c, "Failed to move task")
					return
				}
			} else {
				if _, err := tx.Exec(`
					UPDATE tasks SET position = position + 1
					WHERE column_id = $1 AND position >= $2 AND position < $3
				`, oldColID, newPos, oldPos); err != nil {
					response.InternalError(c, "Failed to move task")
					return
				}
			}
			if _, err := tx.Exec(`
				UPDATE tasks SET position = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2
			`, newPos, taskID); err != nil {
				response.InternalError(c, "Failed to move task")
				return
			}
		}
	} else {
		if _, err := tx.Exec(`
			UPDATE tasks SET position = position - 1 WHERE column_id = $1 AND position > $2
		`, oldColID, oldPos); err != nil {
			response.InternalError(c, "Failed to move task")
			return
		}
		if _, err := tx.Exec(`
			UPDATE tasks SET position = position + 1 WHERE column_id = $1 AND position >= $2
		`, newColID, newPos); err != nil {
			response.InternalError(c, "Failed to move task")
			return
		}
		if _, err := tx.Exec(`
			UPDATE tasks SET column_id = $1, position = $2,
			       completed_at = CASE WHEN $3::boolean THEN CURRENT_TIMESTAMP ELSE NULL END,
			       updated_at = CURRENT_TIMESTAMP
			WHERE id = $4
		`, newColID, newPos, targetDone, taskID); err != nil {
			response.InternalError(c, "Failed to move task")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to move task")
		return
	}

	if oldColID != newColID {
		var oldName string
		_ = h.db.QueryRow(`SELECT name FROM task_columns WHERE id = $1`, oldColID).Scan(&oldName)
		h.logTaskActivity(tenantID, taskID, actorID, actorName, "moved",
			map[string]interface{}{"column": oldName},
			map[string]interface{}{"column": targetName})
		if targetDone && !completedAt.Valid {
			h.logTaskActivity(tenantID, taskID, actorID, actorName, "completed", nil, nil)
		}
		if !targetDone && completedAt.Valid {
			h.logTaskActivity(tenantID, taskID, actorID, actorName, "reopened", nil, nil)
		}

		var taskTitle string
		_ = h.db.QueryRow(`SELECT title FROM tasks WHERE id = $1`, taskID).Scan(&taskTitle)
		h.EmitWorkflowEvent(tenantID, "task.status_changed", map[string]interface{}{
			"record_id":    taskID.String(),
			"task_title":   taskTitle,
			"board_id":     boardID.String(),
			"old_column":   oldName,
			"new_column":   targetName,
			"is_completed": targetDone,
		})
	}

	wipExceeded := wipLimit.Valid && int64(targetCount+1) > wipLimit.Int64
	detail, err := h.loadTaskDetail(tenantID, boardID, taskID)
	if err != nil {
		response.InternalError(c, "Failed to load task")
		return
	}
	detail["wip_exceeded"] = wipExceeded
	response.Success(c, detail)
}

// DeleteBoardTask permanently removes a task
func (h *Handler) DeleteBoardTask(c *gin.Context) {
	tenantID, boardID, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to delete task")
		return
	}
	defer tx.Rollback()

	var colID uuid.UUID
	var pos int
	if err := tx.QueryRow(`
		SELECT column_id, position FROM tasks WHERE id = $1 AND board_id = $2 AND tenant_id = $3 FOR UPDATE
	`, taskID, boardID, tenantID).Scan(&colID, &pos); err != nil {
		response.NotFound(c, "Task")
		return
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE id = $1`, taskID); err != nil {
		response.InternalError(c, "Failed to delete task")
		return
	}
	if _, err := tx.Exec(`
		UPDATE tasks SET position = position - 1 WHERE column_id = $1 AND position > $2
	`, colID, pos); err != nil {
		response.InternalError(c, "Failed to delete task")
		return
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to delete task")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ── Assignees ───────────────────────────────────────────────────────────────

// SetTaskAssignees replaces the task's assignee set; newly added people are notified
func (h *Handler) SetTaskAssignees(c *gin.Context) {
	tenantID, boardID, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	actorID, actorName := h.taskActor(c)

	var input entity.SetTaskAssigneesInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	wanted := []uuid.UUID{}
	for _, idStr := range input.EmployeeIDs {
		empID, err := uuid.Parse(idStr)
		if err != nil {
			response.BadRequest(c, "Invalid employee id: "+idStr)
			return
		}
		var one int
		if err := h.db.QueryRow(`
			SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		`, empID, tenantID).Scan(&one); err != nil {
			response.BadRequest(c, "Not an employee of this company: "+idStr)
			return
		}
		wanted = append(wanted, empID)
	}

	current := map[uuid.UUID]bool{}
	rows, err := h.db.Query(`SELECT employee_id FROM task_assignees WHERE task_id = $1`, taskID)
	if err != nil {
		response.InternalError(c, "Failed to update assignees")
		return
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			current[id] = true
		}
	}
	rows.Close()

	wantedSet := map[uuid.UUID]bool{}
	added := []uuid.UUID{}
	for _, id := range wanted {
		wantedSet[id] = true
		if !current[id] {
			added = append(added, id)
		}
	}
	removed := []uuid.UUID{}
	for id := range current {
		if !wantedSet[id] {
			removed = append(removed, id)
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to update assignees")
		return
	}
	defer tx.Rollback()

	var assignedBy interface{}
	if actorID != uuid.Nil {
		assignedBy = actorID
	}
	for _, id := range added {
		if _, err := tx.Exec(`
			INSERT INTO task_assignees (task_id, employee_id, tenant_id, assigned_by)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING
		`, taskID, id, tenantID, assignedBy); err != nil {
			response.InternalError(c, "Failed to update assignees")
			return
		}
	}
	for _, id := range removed {
		if _, err := tx.Exec(`
			DELETE FROM task_assignees WHERE task_id = $1 AND employee_id = $2
		`, taskID, id); err != nil {
			response.InternalError(c, "Failed to update assignees")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to update assignees")
		return
	}

	if len(added) > 0 || len(removed) > 0 {
		h.logTaskActivity(tenantID, taskID, actorID, actorName, "assigned",
			map[string]interface{}{"removed": len(removed)},
			map[string]interface{}{"added": len(added)})
	}
	if len(added) > 0 {
		var taskTitle, boardName string
		_ = h.db.QueryRow(`SELECT title FROM tasks WHERE id = $1`, taskID).Scan(&taskTitle)
		_ = h.db.QueryRow(`SELECT name FROM task_boards WHERE id = $1`, boardID).Scan(&boardName)
		go h.notifyTaskAssigned(tenantID, added, actorID, taskID, boardID, taskTitle, boardName)
		h.EmitWorkflowEvent(tenantID, "task.assigned", map[string]interface{}{
			"record_id":  taskID.String(),
			"task_title": taskTitle,
			"board_id":   boardID.String(),
			"board_name": boardName,
		})
	}

	detail, err := h.loadTaskDetail(tenantID, boardID, taskID)
	if err != nil {
		response.InternalError(c, "Failed to load task")
		return
	}
	response.Success(c, detail)
}

// ── Checklist ───────────────────────────────────────────────────────────────

func (h *Handler) CreateChecklistItem(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	var input entity.CreateChecklistItemInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	itemID := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO task_checklist_items (id, tenant_id, task_id, title, position)
		SELECT $1, $2, $3, $4, COALESCE(MAX(position) + 1, 0)
		FROM task_checklist_items WHERE task_id = $3
	`, itemID, tenantID, taskID, input.Title); err != nil {
		h.log.Error("create checklist item failed", "error", err)
		response.InternalError(c, "Failed to add checklist item")
		return
	}
	response.Created(c, gin.H{"id": itemID})
}

func (h *Handler) UpdateChecklistItem(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		response.BadRequest(c, "Invalid item ID")
		return
	}
	var input entity.UpdateChecklistItemInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	set := "updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{itemID, taskID, tenantID}
	n := 3
	add := func(field string, v interface{}) {
		n++
		set += ", " + field + " = $" + itoa(n)
		args = append(args, v)
	}
	if input.Title != nil {
		if *input.Title == "" {
			response.BadRequest(c, "Title cannot be empty")
			return
		}
		add("title", *input.Title)
	}
	if input.IsDone != nil {
		add("is_done", *input.IsDone)
	}
	if input.Position != nil {
		add("position", *input.Position)
	}

	res, err := h.db.Exec(`
		UPDATE task_checklist_items SET `+set+` WHERE id = $1 AND task_id = $2 AND tenant_id = $3
	`, args...)
	if err != nil {
		response.InternalError(c, "Failed to update checklist item")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Checklist item")
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func (h *Handler) DeleteChecklistItem(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		response.BadRequest(c, "Invalid item ID")
		return
	}
	res, err := h.db.Exec(`
		DELETE FROM task_checklist_items WHERE id = $1 AND task_id = $2 AND tenant_id = $3
	`, itemID, taskID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete checklist item")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Checklist item")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ── Comments ────────────────────────────────────────────────────────────────

func (h *Handler) loadTaskComments(taskID uuid.UUID) ([]entity.TaskComment, error) {
	rows, err := h.db.Query(`
		SELECT id, task_id, author_id, COALESCE(author_name, ''), body, mentions, created_at, updated_at
		FROM task_comments WHERE task_id = $1 ORDER BY created_at
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	comments := []entity.TaskComment{}
	for rows.Next() {
		var cm entity.TaskComment
		var mentionsRaw []byte
		if err := rows.Scan(&cm.ID, &cm.TaskID, &cm.AuthorID, &cm.AuthorName, &cm.Body, &mentionsRaw, &cm.CreatedAt, &cm.UpdatedAt); err != nil {
			return nil, err
		}
		if len(mentionsRaw) > 0 {
			var m interface{}
			if err := json.Unmarshal(mentionsRaw, &m); err == nil {
				cm.Mentions = m
			}
		}
		comments = append(comments, cm)
	}
	return comments, rows.Err()
}

func (h *Handler) ListTaskComments(c *gin.Context) {
	_, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	comments, err := h.loadTaskComments(taskID)
	if err != nil {
		response.InternalError(c, "Failed to load comments")
		return
	}
	response.Success(c, comments)
}

func (h *Handler) CreateTaskComment(c *gin.Context) {
	tenantID, boardID, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	actorID, actorName := h.taskActor(c)

	var input entity.CreateTaskCommentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	mentionsJSON := "[]"
	if len(input.Mentions) > 0 {
		if b, err := json.Marshal(input.Mentions); err == nil {
			mentionsJSON = string(b)
		}
	}
	var author interface{}
	if actorID != uuid.Nil {
		author = actorID
	}
	commentID := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO task_comments (id, tenant_id, task_id, author_id, author_name, body, mentions)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
	`, commentID, tenantID, taskID, author, actorName, input.Body, mentionsJSON); err != nil {
		h.log.Error("create comment failed", "error", err)
		response.InternalError(c, "Failed to add comment")
		return
	}

	h.logTaskActivity(tenantID, taskID, actorID, actorName, "commented", nil, nil)

	// Mention notifications (verified against this tenant, actor excluded)
	if len(input.Mentions) > 0 {
		var taskTitle string
		_ = h.db.QueryRow(`SELECT title FROM tasks WHERE id = $1`, taskID).Scan(&taskTitle)
		for _, m := range input.Mentions {
			mentionedID, err := uuid.Parse(m.UserID)
			if err != nil || mentionedID == actorID {
				continue
			}
			var one int
			if err := h.db.QueryRow(`
				SELECT 1 FROM users WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
			`, mentionedID, tenantID).Scan(&one); err != nil {
				continue
			}
			h.createTranslatedNotification(tenantID, mentionedID, "task_comment_mention",
				map[string]interface{}{
					"task_id":    taskID.String(),
					"board_id":   boardID.String(),
					"actor_name": actorName,
					"task_title": taskTitle,
				},
				actorName, taskTitle,
			)
		}
	}

	comments, err := h.loadTaskComments(taskID)
	if err != nil {
		response.InternalError(c, "Failed to load comments")
		return
	}
	response.Created(c, comments)
}

// DeleteTaskComment — author can always delete their own; others need tasks:task:delete
func (h *Handler) DeleteTaskComment(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		response.BadRequest(c, "Invalid comment ID")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var authorID sql.NullString
	if err := h.db.QueryRow(`
		SELECT COALESCE(author_id::text, '') FROM task_comments
		WHERE id = $1 AND task_id = $2 AND tenant_id = $3
	`, commentID, taskID, tenantID).Scan(&authorID); err != nil {
		response.NotFound(c, "Comment")
		return
	}
	isAuthor := authorID.String != "" && authorID.String == userID.String()
	if !isAuthor && h.perm != nil && !h.perm.Can(c, "tasks", "task", "delete") {
		response.Forbidden(c, "Missing required permission: tasks:task:delete")
		return
	}

	if _, err := h.db.Exec(`DELETE FROM task_comments WHERE id = $1`, commentID); err != nil {
		response.InternalError(c, "Failed to delete comment")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ── Attachments (shared `attachments` table, entity_type='task') ────────────

func (h *Handler) loadTaskAttachments(tenantID, taskID uuid.UUID) ([]entity.TaskAttachment, error) {
	rows, err := h.db.Query(`
		SELECT id, file_name, original_name, mime_type, file_size, uploaded_by, created_at
		FROM attachments
		WHERE tenant_id = $1 AND entity_type = 'task' AND entity_id = $2 AND deleted_at IS NULL
		ORDER BY created_at
	`, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []entity.TaskAttachment{}
	for rows.Next() {
		var a entity.TaskAttachment
		if err := rows.Scan(&a.ID, &a.FileName, &a.OriginalName, &a.MimeType, &a.FileSize, &a.UploadedBy, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.TaskID = taskID
		a.URL = "/api/v1/files/" + a.FileName
		items = append(items, a)
	}
	return items, rows.Err()
}

func (h *Handler) UploadTaskAttachment(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	userID, _ := middleware.GetUserID(c)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "No file provided")
		return
	}
	defer file.Close()

	if header.Size > h.config.Storage.MaxFileSize {
		response.BadRequest(c, fmt.Sprintf("File size exceeds maximum of %d bytes", h.config.Storage.MaxFileSize))
		return
	}

	buffer := make([]byte, 512)
	file.Read(buffer)
	mimeType := http.DetectContentType(buffer)
	file.Seek(0, 0)

	// storedName doubles as the public file id served by GET /files/:id
	randomBytes := make([]byte, 16)
	rand.Read(randomBytes)
	storedName := hex.EncodeToString(randomBytes) + filepath.Ext(header.Filename)

	content, err := io.ReadAll(file)
	if err != nil {
		response.InternalError(c, "Failed to read file")
		return
	}
	if err := h.insertUploadedFile(storedName, header.Filename, mimeType, int64(len(content)), content, tenantID, userID); err != nil {
		h.log.Error("store task attachment failed", "error", err)
		response.InternalError(c, "Failed to save file")
		return
	}

	attachID := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO attachments (id, tenant_id, uploaded_by, entity_type, entity_id,
			file_name, original_name, mime_type, file_size, storage_path)
		VALUES ($1, $2, $3, 'task', $4, $5, $6, $7, $8, $9)
	`, attachID, tenantID, userID, taskID, storedName, header.Filename, mimeType, header.Size, storedName); err != nil {
		h.log.Error("save task attachment record failed", "error", err)
		response.InternalError(c, "Failed to save attachment")
		return
	}

	actorID, actorName := h.taskActor(c)
	h.logTaskActivity(tenantID, taskID, actorID, actorName, "attachment_added",
		nil, map[string]interface{}{"file": header.Filename})

	attachments, err := h.loadTaskAttachments(tenantID, taskID)
	if err != nil {
		response.InternalError(c, "Failed to load attachments")
		return
	}
	response.Created(c, attachments)
}

func (h *Handler) DeleteTaskAttachment(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	attachID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		response.BadRequest(c, "Invalid attachment ID")
		return
	}

	// storage_path holds the uploaded_files id — clean up the backing row too
	var storagePath string
	if err := h.db.QueryRow(`
		SELECT storage_path FROM attachments
		WHERE id = $1 AND tenant_id = $2 AND entity_type = 'task' AND entity_id = $3
	`, attachID, tenantID, taskID).Scan(&storagePath); err != nil {
		response.NotFound(c, "Attachment")
		return
	}
	if _, err := h.db.Exec(`DELETE FROM attachments WHERE id = $1 AND tenant_id = $2`, attachID, tenantID); err != nil {
		response.InternalError(c, "Failed to delete attachment")
		return
	}
	if storagePath != "" {
		if _, execErr := h.db.Exec(`DELETE FROM uploaded_files WHERE id = $1`, storagePath); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "DELETE uploaded_files", "error", execErr)
		}
	}
	response.Success(c, gin.H{"deleted": true})
}

// ── Activity ────────────────────────────────────────────────────────────────

func (h *Handler) ListTaskActivity(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	rows, err := h.db.Query(`
		SELECT id, task_id, actor_id, COALESCE(actor_name, ''), action, old_value, new_value, created_at
		FROM task_activity
		WHERE tenant_id = $1 AND task_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 200
	`, tenantID, taskID)
	if err != nil {
		response.InternalError(c, "Failed to load activity")
		return
	}
	defer rows.Close()

	entries := []entity.TaskActivityEntry{}
	for rows.Next() {
		var e entity.TaskActivityEntry
		var oldRaw, newRaw []byte
		if err := rows.Scan(&e.ID, &e.TaskID, &e.ActorID, &e.ActorName, &e.Action, &oldRaw, &newRaw, &e.CreatedAt); err != nil {
			response.InternalError(c, "Failed to load activity")
			return
		}
		if len(oldRaw) > 0 {
			var v interface{}
			if json.Unmarshal(oldRaw, &v) == nil && v != nil {
				e.OldValue = v
			}
		}
		if len(newRaw) > 0 {
			var v interface{}
			if json.Unmarshal(newRaw, &v) == nil && v != nil {
				e.NewValue = v
			}
		}
		entries = append(entries, e)
	}
	response.Success(c, entries)
}

// ── Links (scaffold) ────────────────────────────────────────────────────────

func (h *Handler) CreateTaskLink(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	var input entity.CreateTaskLinkInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	userID, _ := middleware.GetUserID(c)
	var createdBy interface{}
	if userID != uuid.Nil {
		createdBy = userID
	}
	linkID := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO task_links (id, tenant_id, task_id, linked_module, linked_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, linked_module, linked_id) DO NOTHING
	`, linkID, tenantID, taskID, input.LinkedModule, input.LinkedID, createdBy); err != nil {
		response.InternalError(c, "Failed to link record")
		return
	}
	response.Created(c, gin.H{"id": linkID})
}

func (h *Handler) DeleteTaskLink(c *gin.Context) {
	tenantID, _, taskID, ok := h.parseTaskScope(c)
	if !ok {
		return
	}
	linkID, err := uuid.Parse(c.Param("linkId"))
	if err != nil {
		response.BadRequest(c, "Invalid link ID")
		return
	}
	res, err := h.db.Exec(`
		DELETE FROM task_links WHERE id = $1 AND task_id = $2 AND tenant_id = $3
	`, linkID, taskID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to unlink record")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Link")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
