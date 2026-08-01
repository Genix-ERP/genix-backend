package handler

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Vazifalar (task management) — boards, columns, stats, my-tasks.
// Task-level endpoints live in board_tasks.go.

// defaultTaskColumns are created for every new board unless the client sends
// custom starter columns. Names are tenant-editable data afterwards.
var defaultTaskColumns = []entity.CreateTaskColumnInput{
	{Name: "Yangi", Color: "gray"},
	{Name: "Jarayonda", Color: "blue"},
	{Name: "Tekshiruvda", Color: "orange"},
	{Name: "Bajarildi", Color: "green", IsDoneColumn: true},
}

// ── Shared helpers ──────────────────────────────────────────────────────────

// taskBoardOwned verifies the board exists and belongs to the tenant.
func (h *Handler) taskBoardOwned(tenantID, boardID uuid.UUID) bool {
	var one int
	err := h.db.QueryRow(`SELECT 1 FROM task_boards WHERE id = $1 AND tenant_id = $2`, boardID, tenantID).Scan(&one)
	return err == nil
}

// taskActor returns the current user's id and display name for activity rows.
func (h *Handler) taskActor(c *gin.Context) (uuid.UUID, string) {
	userID, _ := middleware.GetUserID(c)
	name := ""
	if userID != uuid.Nil {
		_ = h.db.QueryRow(`SELECT TRIM(first_name || ' ' || last_name) FROM users WHERE id = $1`, userID).Scan(&name)
	}
	return userID, name
}

// logTaskActivity best-effort inserts one audit-trail row for a task.
func (h *Handler) logTaskActivity(tenantID, taskID, actorID uuid.UUID, actorName, action string, oldVal, newVal map[string]interface{}) {
	oldJSON, newJSON := "null", "null"
	if oldVal != nil {
		if b, err := json.Marshal(oldVal); err == nil {
			oldJSON = string(b)
		}
	}
	if newVal != nil {
		if b, err := json.Marshal(newVal); err == nil {
			newJSON = string(b)
		}
	}
	var actor interface{}
	if actorID != uuid.Nil {
		actor = actorID
	}
	if _, err := h.db.Exec(`
		INSERT INTO task_activity (tenant_id, task_id, actor_id, actor_name, action, old_value, new_value)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb)
	`, tenantID, taskID, actor, actorName, action, oldJSON, newJSON); err != nil {
		h.log.Error("task activity insert failed", "error", err, "task_id", taskID.String())
	}
}

// notifyTaskAssigned sends the task_assigned notification to every employee in
// employeeIDs that is linked to a user account (skips the actor themself).
func (h *Handler) notifyTaskAssigned(tenantID uuid.UUID, employeeIDs []uuid.UUID, actorUserID uuid.UUID, taskID, boardID uuid.UUID, taskTitle, boardName string) {
	for _, empID := range employeeIDs {
		var recipient uuid.UUID
		err := h.db.QueryRow(`
			SELECT COALESCE(e.user_id, '00000000-0000-0000-0000-000000000000')
			FROM employees e WHERE e.id = $1 AND e.tenant_id = $2
		`, empID, tenantID).Scan(&recipient)
		if err != nil || recipient == uuid.Nil || recipient == actorUserID {
			continue
		}
		h.createTranslatedNotification(tenantID, recipient, "task_assigned",
			map[string]interface{}{
				"task_id":    taskID.String(),
				"board_id":   boardID.String(),
				"task_title": taskTitle,
				"board_name": boardName,
			},
			taskTitle, boardName,
		)
	}
}

func scanTaskColumn(rows *sql.Rows) (entity.TaskColumn, error) {
	var col entity.TaskColumn
	err := rows.Scan(&col.ID, &col.BoardID, &col.Name, &col.Color, &col.Position,
		&col.IsDoneColumn, &col.WipLimit, &col.TaskCount)
	return col, err
}

// ── Boards ──────────────────────────────────────────────────────────────────

// ListTaskBoards returns the tenant's boards with card counts
func (h *Handler) ListTaskBoards(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT b.id, b.tenant_id, b.organization_id, b.name, b.description, b.color, b.icon,
		       b.created_by, b.archived_at, b.created_at, b.updated_at,
		       (SELECT COUNT(*) FROM tasks t WHERE t.board_id = b.id AND t.archived_at IS NULL) AS task_count,
		       (SELECT COUNT(*) FROM tasks t WHERE t.board_id = b.id AND t.archived_at IS NULL AND t.completed_at IS NOT NULL) AS done_count,
		       (SELECT COUNT(*) FROM tasks t WHERE t.board_id = b.id AND t.archived_at IS NULL AND t.completed_at IS NULL AND t.due_date < CURRENT_DATE) AS overdue_count,
		       (SELECT COUNT(*) FROM tasks t WHERE t.board_id = b.id AND t.archived_at IS NULL AND t.completed_at IS NULL
		          AND EXISTS (SELECT 1 FROM task_assignees ta WHERE ta.task_id = t.id)
		          AND NOT EXISTS (
		              SELECT 1 FROM task_assignees ta
		              JOIN employees e ON e.id = ta.employee_id
		              WHERE ta.task_id = t.id AND e.deleted_at IS NULL AND e.status <> 'terminated'
		          )) AS orphaned_count
		FROM task_boards b
		WHERE b.tenant_id = $1`
	args := []interface{}{tenantID}

	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		query += ` AND (b.organization_id = $2 OR b.organization_id IS NULL)`
		args = append(args, orgID)
	}
	if c.Query("include_archived") != "true" {
		query += ` AND b.archived_at IS NULL`
	}
	query += ` ORDER BY b.created_at DESC`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("list task boards failed", "error", err)
		response.InternalError(c, "Failed to list boards")
		return
	}
	defer rows.Close()

	boards := []entity.TaskBoard{}
	for rows.Next() {
		var b entity.TaskBoard
		if err := rows.Scan(&b.ID, &b.TenantID, &b.OrganizationID, &b.Name, &b.Description,
			&b.Color, &b.Icon, &b.CreatedBy, &b.ArchivedAt, &b.CreatedAt, &b.UpdatedAt,
			&b.TaskCount, &b.DoneCount, &b.OverdueCount, &b.OrphanedCount); err != nil {
			h.log.Error("scan task board failed", "error", err)
			response.InternalError(c, "Failed to list boards")
			return
		}
		boards = append(boards, b)
	}
	response.Success(c, boards)
}

// CreateTaskBoard creates a board together with its starter columns
func (h *Handler) CreateTaskBoard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input entity.CreateTaskBoardInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	if input.Color == "" {
		input.Color = "blue"
	}
	if input.Icon == "" {
		input.Icon = "kanban"
	}
	columns := input.Columns
	if len(columns) == 0 {
		columns = defaultTaskColumns
	}

	var orgID interface{}
	if oid, okOrg := middleware.GetOrganizationID(c); okOrg && oid != uuid.Nil {
		orgID = oid
	}
	var createdBy interface{}
	if userID != uuid.Nil {
		createdBy = userID
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create board")
		return
	}
	defer tx.Rollback()

	boardID := uuid.New()
	now := time.Now()
	if _, err := tx.Exec(`
		INSERT INTO task_boards (id, tenant_id, organization_id, name, description, color, icon, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $9)
	`, boardID, tenantID, orgID, input.Name, input.Description, input.Color, input.Icon, createdBy, now); err != nil {
		h.log.Error("create task board failed", "error", err)
		response.InternalError(c, "Failed to create board")
		return
	}

	doneSeen := false
	for i, col := range columns {
		color := col.Color
		if color == "" {
			color = "gray"
		}
		isDone := col.IsDoneColumn && !doneSeen
		if isDone {
			doneSeen = true
		}
		if _, err := tx.Exec(`
			INSERT INTO task_columns (tenant_id, board_id, name, color, position, is_done_column, wip_limit)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, tenantID, boardID, col.Name, color, i, isDone, col.WipLimit); err != nil {
			h.log.Error("create board column failed", "error", err)
			response.InternalError(c, "Failed to create board")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to create board")
		return
	}

	c.Params = append(c.Params, gin.Param{Key: "id", Value: boardID.String()})
	h.GetTaskBoard(c)
}

// GetTaskBoard returns the full board payload: board, columns and enriched tasks
func (h *Handler) GetTaskBoard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid board ID")
		return
	}

	var b entity.TaskBoard
	err = h.db.QueryRow(`
		SELECT b.id, b.tenant_id, b.organization_id, b.name, b.description, b.color, b.icon,
		       b.created_by, b.archived_at, b.created_at, b.updated_at,
		       (SELECT COUNT(*) FROM tasks t WHERE t.board_id = b.id AND t.archived_at IS NULL),
		       (SELECT COUNT(*) FROM tasks t WHERE t.board_id = b.id AND t.archived_at IS NULL AND t.completed_at IS NOT NULL),
		       (SELECT COUNT(*) FROM tasks t WHERE t.board_id = b.id AND t.archived_at IS NULL AND t.completed_at IS NULL AND t.due_date < CURRENT_DATE),
		       (SELECT COUNT(*) FROM tasks t WHERE t.board_id = b.id AND t.archived_at IS NULL AND t.completed_at IS NULL
		          AND EXISTS (SELECT 1 FROM task_assignees ta WHERE ta.task_id = t.id)
		          AND NOT EXISTS (
		              SELECT 1 FROM task_assignees ta
		              JOIN employees e ON e.id = ta.employee_id
		              WHERE ta.task_id = t.id AND e.deleted_at IS NULL AND e.status <> 'terminated'
		          ))
		FROM task_boards b
		WHERE b.id = $1 AND b.tenant_id = $2
	`, boardID, tenantID).Scan(&b.ID, &b.TenantID, &b.OrganizationID, &b.Name, &b.Description,
		&b.Color, &b.Icon, &b.CreatedBy, &b.ArchivedAt, &b.CreatedAt, &b.UpdatedAt,
		&b.TaskCount, &b.DoneCount, &b.OverdueCount, &b.OrphanedCount)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Board")
		return
	}
	if err != nil {
		h.log.Error("get task board failed", "error", err)
		response.InternalError(c, "Failed to load board")
		return
	}

	columns, err := h.loadBoardColumns(boardID)
	if err != nil {
		h.log.Error("load board columns failed", "error", err)
		response.InternalError(c, "Failed to load board")
		return
	}

	tasks, err := h.loadBoardTasks(tenantID, boardID, "")
	if err != nil {
		h.log.Error("load board tasks failed", "error", err)
		response.InternalError(c, "Failed to load board")
		return
	}

	response.Success(c, gin.H{
		"board":   b,
		"columns": columns,
		"tasks":   tasks,
	})
}

func (h *Handler) loadBoardColumns(boardID uuid.UUID) ([]entity.TaskColumn, error) {
	rows, err := h.db.Query(`
		SELECT c.id, c.board_id, c.name, c.color, c.position, c.is_done_column, c.wip_limit,
		       (SELECT COUNT(*) FROM tasks t WHERE t.column_id = c.id AND t.archived_at IS NULL)
		FROM task_columns c
		WHERE c.board_id = $1
		ORDER BY c.position, c.created_at
	`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := []entity.TaskColumn{}
	for rows.Next() {
		col, err := scanTaskColumn(rows)
		if err != nil {
			return nil, err
		}
		columns = append(columns, col)
	}
	return columns, rows.Err()
}

// loadBoardTasks loads enriched tasks for a board. extraWhere may add filters
// (already-sanitized SQL, positional args continue from $3).
func (h *Handler) loadBoardTasks(tenantID, boardID uuid.UUID, extraWhere string, extraArgs ...interface{}) ([]entity.BoardTask, error) {
	query := `
		SELECT t.id, t.board_id, t.column_id, t.title, t.description, t.priority,
		       t.due_date, t.start_date, t.position, t.completed_at, t.archived_at,
		       t.created_by, t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id),
		       (SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id AND ci.is_done),
		       (SELECT COUNT(*) FROM task_comments tc WHERE tc.task_id = t.id),
		       (SELECT COUNT(*) FROM attachments a WHERE a.entity_type = 'task' AND a.entity_id = t.id AND a.deleted_at IS NULL),
		       (t.due_date IS NOT NULL AND t.due_date < CURRENT_DATE AND t.completed_at IS NULL)
		FROM tasks t
		WHERE t.tenant_id = $1 AND t.board_id = $2 AND t.archived_at IS NULL`
	args := []interface{}{tenantID, boardID}
	if extraWhere != "" {
		query += " " + extraWhere
		args = append(args, extraArgs...)
	}
	query += ` ORDER BY t.position`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []entity.BoardTask{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var t entity.BoardTask
		if err := rows.Scan(&t.ID, &t.BoardID, &t.ColumnID, &t.Title, &t.Description, &t.Priority,
			&t.DueDate, &t.StartDate, &t.Position, &t.CompletedAt, &t.ArchivedAt,
			&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
			&t.ChecklistTotal, &t.ChecklistDone, &t.CommentCount, &t.AttachmentCount, &t.IsOverdue); err != nil {
			return nil, err
		}
		t.Assignees = []entity.TaskAssigneeInfo{}
		index[t.ID] = len(tasks)
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Assignees for all loaded tasks in one pass
	aRows, err := h.db.Query(`
		SELECT ta.task_id, e.id, e.user_id, TRIM(e.first_name || ' ' || e.last_name),
		       COALESCE(jp.name, e.job_title, ''), COALESCE(d.name, ''),
		       (e.deleted_at IS NULL AND e.status <> 'terminated')
		FROM task_assignees ta
		JOIN employees e ON e.id = ta.employee_id
		LEFT JOIN job_positions jp ON jp.id = e.job_position_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE ta.tenant_id = $1
		  AND ta.task_id IN (SELECT id FROM tasks WHERE board_id = $2)
		ORDER BY ta.created_at
	`, tenantID, boardID)
	if err != nil {
		return nil, err
	}
	defer aRows.Close()

	for aRows.Next() {
		var taskID uuid.UUID
		var info entity.TaskAssigneeInfo
		if err := aRows.Scan(&taskID, &info.EmployeeID, &info.UserID, &info.Name, &info.JobTitle, &info.Department, &info.IsActive); err != nil {
			return nil, err
		}
		if i, okIdx := index[taskID]; okIdx {
			tasks[i].Assignees = append(tasks[i].Assignees, info)
		}
	}
	return tasks, aRows.Err()
}

// UpdateTaskBoard updates board settings; `archived` toggles archived_at
func (h *Handler) UpdateTaskBoard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid board ID")
		return
	}
	if !h.taskBoardOwned(tenantID, boardID) {
		response.NotFound(c, "Board")
		return
	}

	var input entity.UpdateTaskBoardInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	set := "updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{boardID, tenantID}
	n := 2
	add := func(expr string, v interface{}) {
		n++
		set += ", " + expr + " = $" + itoa(n)
		args = append(args, v)
	}
	if input.Name != nil {
		if *input.Name == "" {
			response.BadRequest(c, "Board name cannot be empty")
			return
		}
		add("name", *input.Name)
	}
	if input.Description != nil {
		add("description", nullIfEmpty(*input.Description))
	}
	if input.Color != nil {
		add("color", *input.Color)
	}
	if input.Icon != nil {
		add("icon", *input.Icon)
	}
	if input.Archived != nil {
		if *input.Archived {
			set += ", archived_at = COALESCE(archived_at, CURRENT_TIMESTAMP)"
		} else {
			set += ", archived_at = NULL"
		}
	}

	if _, err := h.db.Exec(`UPDATE task_boards SET `+set+` WHERE id = $1 AND tenant_id = $2`, args...); err != nil {
		h.log.Error("update task board failed", "error", err)
		response.InternalError(c, "Failed to update board")
		return
	}
	h.GetTaskBoard(c)
}

// DeleteTaskBoard permanently removes a board and everything on it
func (h *Handler) DeleteTaskBoard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid board ID")
		return
	}
	res, err := h.db.Exec(`DELETE FROM task_boards WHERE id = $1 AND tenant_id = $2`, boardID, tenantID)
	if err != nil {
		h.log.Error("delete task board failed", "error", err)
		response.InternalError(c, "Failed to delete board")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Board")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ── Columns ─────────────────────────────────────────────────────────────────

// CreateTaskColumn appends a column to the board
func (h *Handler) CreateTaskColumn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid board ID")
		return
	}
	if !h.taskBoardOwned(tenantID, boardID) {
		response.NotFound(c, "Board")
		return
	}

	var input entity.CreateTaskColumnInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	if input.Color == "" {
		input.Color = "gray"
	}
	if input.WipLimit != nil && *input.WipLimit <= 0 {
		response.BadRequest(c, "WIP limit must be positive")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create column")
		return
	}
	defer tx.Rollback()

	if input.IsDoneColumn {
		if _, err := tx.Exec(`UPDATE task_columns SET is_done_column = false WHERE board_id = $1`, boardID); err != nil {
			response.InternalError(c, "Failed to create column")
			return
		}
	}

	columnID := uuid.New()
	if _, err := tx.Exec(`
		INSERT INTO task_columns (id, tenant_id, board_id, name, color, position, is_done_column, wip_limit)
		SELECT $1, $2, $3, $4, $5, COALESCE(MAX(position) + 1, 0), $6, $7
		FROM task_columns WHERE board_id = $3
	`, columnID, tenantID, boardID, input.Name, input.Color, input.IsDoneColumn, input.WipLimit); err != nil {
		h.log.Error("create task column failed", "error", err)
		response.InternalError(c, "Failed to create column")
		return
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to create column")
		return
	}

	columns, err := h.loadBoardColumns(boardID)
	if err != nil {
		response.InternalError(c, "Failed to load columns")
		return
	}
	response.Success(c, gin.H{"columns": columns, "created_id": columnID})
}

// UpdateTaskColumn renames/recolors a column, toggles done flag, sets WIP limit
func (h *Handler) UpdateTaskColumn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid board ID")
		return
	}
	columnID, err := uuid.Parse(c.Param("columnId"))
	if err != nil {
		response.BadRequest(c, "Invalid column ID")
		return
	}

	var input entity.UpdateTaskColumnInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	if input.WipLimit != nil && *input.WipLimit <= 0 {
		response.BadRequest(c, "WIP limit must be positive")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to update column")
		return
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`
		SELECT 1 FROM task_columns WHERE id = $1 AND board_id = $2 AND tenant_id = $3
	`, columnID, boardID, tenantID).Scan(&exists); err != nil {
		response.NotFound(c, "Column")
		return
	}

	if input.IsDoneColumn != nil && *input.IsDoneColumn {
		if _, err := tx.Exec(`UPDATE task_columns SET is_done_column = false WHERE board_id = $1 AND id <> $2`, boardID, columnID); err != nil {
			response.InternalError(c, "Failed to update column")
			return
		}
	}

	set := "updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{columnID, boardID}
	n := 2
	add := func(expr string, v interface{}) {
		n++
		set += ", " + expr + " = $" + itoa(n)
		args = append(args, v)
	}
	if input.Name != nil {
		if *input.Name == "" {
			response.BadRequest(c, "Column name cannot be empty")
			return
		}
		add("name", *input.Name)
	}
	if input.Color != nil {
		add("color", *input.Color)
	}
	if input.IsDoneColumn != nil {
		add("is_done_column", *input.IsDoneColumn)
	}
	if input.RemoveWipLimit {
		set += ", wip_limit = NULL"
	} else if input.WipLimit != nil {
		add("wip_limit", *input.WipLimit)
	}

	if _, err := tx.Exec(`UPDATE task_columns SET `+set+` WHERE id = $1 AND board_id = $2`, args...); err != nil {
		h.log.Error("update task column failed", "error", err)
		response.InternalError(c, "Failed to update column")
		return
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to update column")
		return
	}

	columns, err := h.loadBoardColumns(boardID)
	if err != nil {
		response.InternalError(c, "Failed to load columns")
		return
	}
	response.Success(c, gin.H{"columns": columns})
}

// ReorderTaskColumns applies a full new column order for the board
func (h *Handler) ReorderTaskColumns(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid board ID")
		return
	}
	if !h.taskBoardOwned(tenantID, boardID) {
		response.NotFound(c, "Board")
		return
	}

	var input entity.ReorderTaskColumnsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to reorder columns")
		return
	}
	defer tx.Rollback()

	for i, idStr := range input.ColumnIDs {
		colID, err := uuid.Parse(idStr)
		if err != nil {
			response.BadRequest(c, "Invalid column ID: "+idStr)
			return
		}
		res, err := tx.Exec(`
			UPDATE task_columns SET position = $1, updated_at = CURRENT_TIMESTAMP
			WHERE id = $2 AND board_id = $3 AND tenant_id = $4
		`, i, colID, boardID, tenantID)
		if err != nil {
			response.InternalError(c, "Failed to reorder columns")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			response.BadRequest(c, "Column does not belong to this board: "+idStr)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to reorder columns")
		return
	}

	columns, err := h.loadBoardColumns(boardID)
	if err != nil {
		response.InternalError(c, "Failed to load columns")
		return
	}
	response.Success(c, gin.H{"columns": columns})
}

// DeleteTaskColumn removes a column; when it still holds tasks the caller must
// pass ?move_to=<columnId> naming the destination column.
func (h *Handler) DeleteTaskColumn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	boardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid board ID")
		return
	}
	columnID, err := uuid.Parse(c.Param("columnId"))
	if err != nil {
		response.BadRequest(c, "Invalid column ID")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to delete column")
		return
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`
		SELECT 1 FROM task_columns WHERE id = $1 AND board_id = $2 AND tenant_id = $3
	`, columnID, boardID, tenantID).Scan(&exists); err != nil {
		response.NotFound(c, "Column")
		return
	}

	var taskCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE column_id = $1`, columnID).Scan(&taskCount); err != nil {
		response.InternalError(c, "Failed to delete column")
		return
	}

	if taskCount > 0 {
		moveToStr := c.Query("move_to")
		if moveToStr == "" {
			response.Conflict(c, "Column has tasks; pass move_to=<columnId> to relocate them")
			return
		}
		moveTo, err := uuid.Parse(moveToStr)
		if err != nil || moveTo == columnID {
			response.BadRequest(c, "Invalid move_to column ID")
			return
		}
		var targetIsDone bool
		if err := tx.QueryRow(`
			SELECT is_done_column FROM task_columns WHERE id = $1 AND board_id = $2 AND tenant_id = $3
		`, moveTo, boardID, tenantID).Scan(&targetIsDone); err != nil {
			response.BadRequest(c, "move_to column does not belong to this board")
			return
		}

		// Append the orphaned tasks to the end of the destination column,
		// keeping their relative order; done-column semantics apply.
		if _, err := tx.Exec(`
			WITH base AS (
				SELECT COALESCE(MAX(position) + 1, 0) AS p FROM tasks WHERE column_id = $1
			), moved AS (
				SELECT id, ROW_NUMBER() OVER (ORDER BY position) AS rn FROM tasks WHERE column_id = $2
			)
			UPDATE tasks SET
				column_id = $1,
				position = base.p + moved.rn - 1,
				completed_at = CASE
					WHEN $3::boolean THEN COALESCE(completed_at, CURRENT_TIMESTAMP)
					ELSE NULL
				END,
				updated_at = CURRENT_TIMESTAMP
			FROM moved, base
			WHERE tasks.id = moved.id
		`, moveTo, columnID, targetIsDone); err != nil {
			h.log.Error("relocate column tasks failed", "error", err)
			response.InternalError(c, "Failed to move tasks")
			return
		}
	}

	if _, err := tx.Exec(`DELETE FROM task_columns WHERE id = $1`, columnID); err != nil {
		response.InternalError(c, "Failed to delete column")
		return
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to delete column")
		return
	}

	columns, err := h.loadBoardColumns(boardID)
	if err != nil {
		response.InternalError(c, "Failed to load columns")
		return
	}
	response.Success(c, gin.H{"columns": columns})
}

// ── Stats / cross-board views ───────────────────────────────────────────────

// GetTaskModuleStats backs the four stats cards at the top of the module.
// completed_pct_month = tasks completed this month ÷ (completed this month +
// currently open) — "of everything on the table this month, how much is done".
func (h *Handler) GetTaskModuleStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var s entity.TaskModuleStats
	err := h.db.QueryRow(`
		SELECT
			COUNT(*) FILTER (WHERE t.archived_at IS NULL),
			COUNT(*) FILTER (WHERE t.archived_at IS NULL AND t.completed_at IS NULL),
			COUNT(*) FILTER (WHERE t.archived_at IS NULL AND t.completed_at IS NULL AND t.due_date < CURRENT_DATE),
			COUNT(*) FILTER (WHERE t.completed_at >= date_trunc('month', CURRENT_DATE))
		FROM tasks t
		JOIN task_boards b ON b.id = t.board_id
		WHERE t.tenant_id = $1 AND b.archived_at IS NULL
	`, tenantID).Scan(&s.TotalTasks, &s.ActiveTasks, &s.OverdueTasks, &s.CompletedThisMonth)
	if err != nil {
		h.log.Error("task stats failed", "error", err)
		response.InternalError(c, "Failed to load stats")
		return
	}
	if s.CompletedThisMonth+s.ActiveTasks > 0 {
		s.CompletedPctMonth = float64(s.CompletedThisMonth) * 100 / float64(s.CompletedThisMonth+s.ActiveTasks)
	}
	response.Success(c, s)
}

// currentEmployeeID resolves the calling user to their HR employee record.
func (h *Handler) currentEmployeeID(tenantID, userID uuid.UUID) (uuid.UUID, bool) {
	var empID uuid.UUID
	err := h.db.QueryRow(`
		SELECT e.id FROM employees e
		WHERE e.tenant_id = $1 AND e.deleted_at IS NULL
		  AND (e.user_id = $2 OR e.id = (SELECT employee_id FROM users WHERE id = $2))
		LIMIT 1
	`, tenantID, userID).Scan(&empID)
	return empID, err == nil
}

// ListMyTasks returns the calling user's open tasks across all boards
func (h *Handler) ListMyTasks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	empID, found := h.currentEmployeeID(tenantID, userID)
	if !found {
		// User isn't linked to an HR employee — nothing can be assigned to them.
		response.Success(c, []entity.MyTask{})
		return
	}
	tasks, err := h.employeeOpenTasks(tenantID, empID)
	if err != nil {
		h.log.Error("my tasks failed", "error", err)
		response.InternalError(c, "Failed to load tasks")
		return
	}
	response.Success(c, tasks)
}

// ListEmployeeTasks returns an employee's open tasks (HR profile "Vazifalar" tab)
func (h *Handler) ListEmployeeTasks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	empID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}
	tasks, err := h.employeeOpenTasks(tenantID, empID)
	if err != nil {
		h.log.Error("employee tasks failed", "error", err)
		response.InternalError(c, "Failed to load tasks")
		return
	}
	response.Success(c, tasks)
}

func (h *Handler) employeeOpenTasks(tenantID, employeeID uuid.UUID) ([]entity.MyTask, error) {
	rows, err := h.db.Query(`
		SELECT t.id, t.board_id, t.column_id, t.title, t.description, t.priority,
		       t.due_date, t.start_date, t.position, t.completed_at, t.archived_at,
		       t.created_by, t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id),
		       (SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id AND ci.is_done),
		       (SELECT COUNT(*) FROM task_comments tc WHERE tc.task_id = t.id),
		       (SELECT COUNT(*) FROM attachments a WHERE a.entity_type = 'task' AND a.entity_id = t.id AND a.deleted_at IS NULL),
		       (t.due_date IS NOT NULL AND t.due_date < CURRENT_DATE AND t.completed_at IS NULL),
		       b.name, col.name, col.is_done_column
		FROM tasks t
		JOIN task_assignees ta ON ta.task_id = t.id AND ta.employee_id = $2
		JOIN task_boards b ON b.id = t.board_id
		JOIN task_columns col ON col.id = t.column_id
		WHERE t.tenant_id = $1
		  AND t.archived_at IS NULL
		  AND t.completed_at IS NULL
		  AND b.archived_at IS NULL
		ORDER BY t.due_date ASC NULLS LAST,
		         CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END
	`, tenantID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []entity.MyTask{}
	for rows.Next() {
		var t entity.MyTask
		if err := rows.Scan(&t.ID, &t.BoardID, &t.ColumnID, &t.Title, &t.Description, &t.Priority,
			&t.DueDate, &t.StartDate, &t.Position, &t.CompletedAt, &t.ArchivedAt,
			&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
			&t.ChecklistTotal, &t.ChecklistDone, &t.CommentCount, &t.AttachmentCount, &t.IsOverdue,
			&t.BoardName, &t.ColumnName, &t.IsDoneColumn); err != nil {
			return nil, err
		}
		t.Assignees = []entity.TaskAssigneeInfo{}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
