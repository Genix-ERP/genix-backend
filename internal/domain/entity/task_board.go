package entity

import (
	"time"

	"github.com/google/uuid"
)

// ── Vazifalar (task management) ─────────────────────────────────────────────
// Note: `Task` is already taken by the CRM tasks module (task.go), so the
// task-management card type is named BoardTask.

// TaskBoard represents a kanban board
type TaskBoard struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	Name           string     `json:"name"`
	Description    *string    `json:"description,omitempty"`
	Color          string     `json:"color"`
	Icon           string     `json:"icon"`
	CreatedBy      *uuid.UUID `json:"created_by,omitempty"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	// Computed
	TaskCount     int `json:"task_count"`
	DoneCount     int `json:"done_count"`
	OverdueCount  int `json:"overdue_count"`
	OrphanedCount int `json:"orphaned_count"` // open tasks whose every assignee left the company
}

// TaskColumn represents a board column (fully dynamic per board)
type TaskColumn struct {
	ID           uuid.UUID `json:"id"`
	BoardID      uuid.UUID `json:"board_id"`
	Name         string    `json:"name"`
	Color        string    `json:"color"`
	Position     int       `json:"position"`
	IsDoneColumn bool      `json:"is_done_column"`
	WipLimit     *int      `json:"wip_limit,omitempty"`

	// Computed
	TaskCount int `json:"task_count"`
}

// TaskAssigneeInfo is the employee summary shown on cards and pickers
type TaskAssigneeInfo struct {
	EmployeeID uuid.UUID  `json:"employee_id"`
	UserID     *uuid.UUID `json:"user_id,omitempty"` // linked login, used for @mentions
	Name       string     `json:"name"`
	JobTitle   string     `json:"job_title,omitempty"`
	Department string     `json:"department,omitempty"`
	IsActive   bool       `json:"is_active"` // false when terminated or soft-deleted in HR
}

// BoardTask represents a task card
type BoardTask struct {
	ID          uuid.UUID  `json:"id"`
	BoardID     uuid.UUID  `json:"board_id"`
	ColumnID    uuid.UUID  `json:"column_id"`
	Title       string     `json:"title"`
	Description *string    `json:"description,omitempty"`
	Priority    string     `json:"priority"`
	DueDate     *time.Time `json:"due_date,omitempty"`
	StartDate   *time.Time `json:"start_date,omitempty"`
	Position    int        `json:"position"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Computed
	Assignees       []TaskAssigneeInfo `json:"assignees"`
	ChecklistTotal  int                `json:"checklist_total"`
	ChecklistDone   int                `json:"checklist_done"`
	CommentCount    int                `json:"comment_count"`
	AttachmentCount int                `json:"attachment_count"`
	IsOverdue       bool               `json:"is_overdue"`
}

// TaskChecklistItem represents one checklist row on a task
type TaskChecklistItem struct {
	ID       uuid.UUID `json:"id"`
	TaskID   uuid.UUID `json:"task_id"`
	Title    string    `json:"title"`
	IsDone   bool      `json:"is_done"`
	Position int       `json:"position"`
}

// TaskComment represents a comment on a task
type TaskComment struct {
	ID         uuid.UUID   `json:"id"`
	TaskID     uuid.UUID   `json:"task_id"`
	AuthorID   *uuid.UUID  `json:"author_id,omitempty"`
	AuthorName string      `json:"author_name"`
	Body       string      `json:"body"`
	Mentions   interface{} `json:"mentions,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// TaskActivityEntry is one audit-trail row on a task
type TaskActivityEntry struct {
	ID        int64       `json:"id"`
	TaskID    uuid.UUID   `json:"task_id"`
	ActorID   *uuid.UUID  `json:"actor_id,omitempty"`
	ActorName string      `json:"actor_name"`
	Action    string      `json:"action"`
	OldValue  interface{} `json:"old_value,omitempty"`
	NewValue  interface{} `json:"new_value,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}

// TaskLink represents a scaffolded cross-module link
type TaskLink struct {
	ID           uuid.UUID `json:"id"`
	TaskID       uuid.UUID `json:"task_id"`
	LinkedModule string    `json:"linked_module"`
	LinkedID     string    `json:"linked_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// TaskAttachment mirrors an `attachments` row with entity_type='task'
type TaskAttachment struct {
	ID           uuid.UUID  `json:"id"`
	TaskID       uuid.UUID  `json:"task_id"`
	FileName     string     `json:"file_name"`
	OriginalName string     `json:"original_name"`
	MimeType     string     `json:"mime_type"`
	FileSize     int64      `json:"file_size"`
	URL          string     `json:"url"`
	UploadedBy   *uuid.UUID `json:"uploaded_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// MyTask is a BoardTask enriched with board/column context for cross-board lists
type MyTask struct {
	BoardTask
	BoardName    string `json:"board_name"`
	ColumnName   string `json:"column_name"`
	IsDoneColumn bool   `json:"is_done_column"`
}

// TaskModuleStats backs the four stats cards at the top of the module
type TaskModuleStats struct {
	TotalTasks         int     `json:"total_tasks"`
	ActiveTasks        int     `json:"active_tasks"`
	OverdueTasks       int     `json:"overdue_tasks"`
	CompletedPctMonth  float64 `json:"completed_pct_month"` // % of this month's tasks that are done
	CompletedThisMonth int     `json:"completed_this_month"`
}

// ── Inputs ──────────────────────────────────────────────────────────────────

type CreateTaskBoardInput struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	Icon        string `json:"icon,omitempty"`
	// Optional custom starter columns; when empty the 4 defaults are created.
	Columns []CreateTaskColumnInput `json:"columns,omitempty"`
}

type UpdateTaskBoardInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty"`
	Icon        *string `json:"icon,omitempty"`
	Archived    *bool   `json:"archived,omitempty"`
}

type CreateTaskColumnInput struct {
	Name         string `json:"name" binding:"required"`
	Color        string `json:"color,omitempty"`
	IsDoneColumn bool   `json:"is_done_column,omitempty"`
	WipLimit     *int   `json:"wip_limit,omitempty"`
}

type UpdateTaskColumnInput struct {
	Name           *string `json:"name,omitempty"`
	Color          *string `json:"color,omitempty"`
	IsDoneColumn   *bool   `json:"is_done_column,omitempty"`
	WipLimit       *int    `json:"wip_limit,omitempty"`
	RemoveWipLimit bool    `json:"remove_wip_limit,omitempty"`
}

type ReorderTaskColumnsInput struct {
	ColumnIDs []string `json:"column_ids" binding:"required"`
}

type CreateBoardTaskInput struct {
	Title       string   `json:"title" binding:"required"`
	Description string   `json:"description,omitempty"`
	ColumnID    string   `json:"column_id,omitempty"` // defaults to the board's first column
	Priority    string   `json:"priority,omitempty"`
	DueDate     string   `json:"due_date,omitempty"`
	StartDate   string   `json:"start_date,omitempty"`
	AssigneeIDs []string `json:"assignee_ids,omitempty"`
}

type UpdateBoardTaskInput struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Priority    *string `json:"priority,omitempty"`
	DueDate     *string `json:"due_date,omitempty"`   // "" clears
	StartDate   *string `json:"start_date,omitempty"` // "" clears
	Archived    *bool   `json:"archived,omitempty"`
}

type MoveBoardTaskInput struct {
	ColumnID string `json:"column_id" binding:"required"`
	Position int    `json:"position"`
}

type SetTaskAssigneesInput struct {
	EmployeeIDs []string `json:"employee_ids"`
}

type CreateChecklistItemInput struct {
	Title string `json:"title" binding:"required"`
}

type UpdateChecklistItemInput struct {
	Title    *string `json:"title,omitempty"`
	IsDone   *bool   `json:"is_done,omitempty"`
	Position *int    `json:"position,omitempty"`
}

type TaskMention struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type CreateTaskCommentInput struct {
	Body     string        `json:"body" binding:"required"`
	Mentions []TaskMention `json:"mentions,omitempty"`
}

type CreateTaskLinkInput struct {
	LinkedModule string `json:"linked_module" binding:"required"`
	LinkedID     string `json:"linked_id" binding:"required"`
}
