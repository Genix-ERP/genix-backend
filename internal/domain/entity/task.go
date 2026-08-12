package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// TaskType represents the type of task
type TaskType string

const (
	TaskTypeGeneral  TaskType = "general"
	TaskTypeFollowUp TaskType = "follow_up"
	TaskTypeCall     TaskType = "call"
	TaskTypeEmail    TaskType = "email"
	TaskTypeMeeting  TaskType = "meeting"
	TaskTypeReview   TaskType = "review"
)

// TaskPriority represents the priority of a task
type TaskPriority string

const (
	TaskPriorityLow    TaskPriority = "low"
	TaskPriorityMedium TaskPriority = "medium"
	TaskPriorityHigh   TaskPriority = "high"
	TaskPriorityUrgent TaskPriority = "urgent"
)

// TaskStatus represents the status of a task
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusCancelled  TaskStatus = "cancelled"
	TaskStatusDeferred   TaskStatus = "deferred"
)

// Task represents a CRM task
type Task struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	TenantID          uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Title             string          `json:"title" db:"title"`
	Description       *string         `json:"description,omitempty" db:"description"`
	LeadID            *uuid.UUID      `json:"lead_id,omitempty" db:"lead_id"`
	ContactID         *uuid.UUID      `json:"contact_id,omitempty" db:"contact_id"`
	OpportunityID     *uuid.UUID      `json:"opportunity_id,omitempty" db:"opportunity_id"`
	ActivityID        *uuid.UUID      `json:"activity_id,omitempty" db:"activity_id"`
	TaskType          TaskType        `json:"task_type" db:"task_type"`
	Priority          TaskPriority    `json:"priority" db:"priority"`
	Status            TaskStatus      `json:"status" db:"status"`
	DueDate           *time.Time      `json:"due_date,omitempty" db:"due_date"`
	DueTime           *string         `json:"due_time,omitempty" db:"due_time"`
	CompletedAt       *time.Time      `json:"completed_at,omitempty" db:"completed_at"`
	AssignedTo        *uuid.UUID      `json:"assigned_to,omitempty" db:"assigned_to"`
	AssignedBy        *uuid.UUID      `json:"assigned_by,omitempty" db:"assigned_by"`
	ProgressPercent   int             `json:"progress_percent" db:"progress_percent"`
	EstimatedHours    *float64        `json:"estimated_hours,omitempty" db:"estimated_hours"`
	ActualHours       *float64        `json:"actual_hours,omitempty" db:"actual_hours"`
	IsRecurring       bool            `json:"is_recurring" db:"is_recurring"`
	RecurrencePattern *string         `json:"recurrence_pattern,omitempty" db:"recurrence_pattern"`
	RecurrenceEndDate *time.Time      `json:"recurrence_end_date,omitempty" db:"recurrence_end_date"`
	ParentTaskID      *uuid.UUID      `json:"parent_task_id,omitempty" db:"parent_task_id"`
	Tags              json.RawMessage `json:"tags" db:"tags" swaggertype:"object"`
	CustomFields      json.RawMessage `json:"custom_fields" db:"custom_fields" swaggertype:"object"`
	CreatedBy         *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt         sql.NullTime    `json:"-" db:"deleted_at"`

	// Populated from joins
	LeadName        *string `json:"lead_name,omitempty" db:"lead_name"`
	ContactName     *string `json:"contact_name,omitempty" db:"contact_name"`
	OpportunityName *string `json:"opportunity_name,omitempty" db:"opportunity_name"`
	AssignedToName  *string `json:"assigned_to_name,omitempty" db:"assigned_to_name"`
	AssignedByName  *string `json:"assigned_by_name,omitempty" db:"assigned_by_name"`
}

// CreateTaskInput represents input for creating a task
type CreateTaskInput struct {
	Title             string   `json:"title" binding:"required,min=1,max=255"`
	Description       string   `json:"description,omitempty"`
	LeadID            string   `json:"lead_id,omitempty"`
	ContactID         string   `json:"contact_id,omitempty"`
	OpportunityID     string   `json:"opportunity_id,omitempty"`
	ActivityID        string   `json:"activity_id,omitempty"`
	TaskType          string   `json:"task_type,omitempty"`
	Priority          string   `json:"priority,omitempty"`
	Status            string   `json:"status,omitempty"`
	DueDate           string   `json:"due_date,omitempty"`
	DueTime           string   `json:"due_time,omitempty"`
	AssignedTo        string   `json:"assigned_to,omitempty"`
	EstimatedHours    *float64 `json:"estimated_hours,omitempty"`
	IsRecurring       bool     `json:"is_recurring,omitempty"`
	RecurrencePattern string   `json:"recurrence_pattern,omitempty"`
	RecurrenceEndDate string   `json:"recurrence_end_date,omitempty"`
	ParentTaskID      string   `json:"parent_task_id,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

// UpdateTaskInput represents input for updating a task
type UpdateTaskInput struct {
	Title             *string  `json:"title,omitempty"`
	Description       *string  `json:"description,omitempty"`
	LeadID            *string  `json:"lead_id,omitempty"`
	ContactID         *string  `json:"contact_id,omitempty"`
	OpportunityID     *string  `json:"opportunity_id,omitempty"`
	TaskType          *string  `json:"task_type,omitempty"`
	Priority          *string  `json:"priority,omitempty"`
	Status            *string  `json:"status,omitempty"`
	DueDate           *string  `json:"due_date,omitempty"`
	DueTime           *string  `json:"due_time,omitempty"`
	AssignedTo        *string  `json:"assigned_to,omitempty"`
	ProgressPercent   *int     `json:"progress_percent,omitempty"`
	EstimatedHours    *float64 `json:"estimated_hours,omitempty"`
	ActualHours       *float64 `json:"actual_hours,omitempty"`
	IsRecurring       *bool    `json:"is_recurring,omitempty"`
	RecurrencePattern *string  `json:"recurrence_pattern,omitempty"`
	RecurrenceEndDate *string  `json:"recurrence_end_date,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

// TaskListFilter represents filters for listing tasks
type TaskListFilter struct {
	Search           string `form:"search"`
	TaskType         string `form:"task_type"`
	Status           string `form:"status"`
	Priority         string `form:"priority"`
	LeadID           string `form:"lead_id"`
	ContactID        string `form:"contact_id"`
	OpportunityID    string `form:"opportunity_id"`
	AssignedTo       string `form:"assigned_to"`
	DueDateFrom      string `form:"due_date_from"`
	DueDateTo        string `form:"due_date_to"`
	IncludeCompleted bool   `form:"include_completed"`
	OverdueOnly      bool   `form:"overdue_only"`
}

// TaskResponse represents the API response for a task
type TaskResponse struct {
	ID                uuid.UUID    `json:"id"`
	Title             string       `json:"title"`
	Description       *string      `json:"description,omitempty"`
	LeadID            *uuid.UUID   `json:"lead_id,omitempty"`
	LeadName          *string      `json:"lead_name,omitempty"`
	ContactID         *uuid.UUID   `json:"contact_id,omitempty"`
	ContactName       *string      `json:"contact_name,omitempty"`
	OpportunityID     *uuid.UUID   `json:"opportunity_id,omitempty"`
	OpportunityName   *string      `json:"opportunity_name,omitempty"`
	TaskType          TaskType     `json:"task_type"`
	Priority          TaskPriority `json:"priority"`
	Status            TaskStatus   `json:"status"`
	DueDate           *time.Time   `json:"due_date,omitempty"`
	DueTime           *string      `json:"due_time,omitempty"`
	CompletedAt       *time.Time   `json:"completed_at,omitempty"`
	AssignedTo        *uuid.UUID   `json:"assigned_to,omitempty"`
	AssignedToName    *string      `json:"assigned_to_name,omitempty"`
	AssignedBy        *uuid.UUID   `json:"assigned_by,omitempty"`
	AssignedByName    *string      `json:"assigned_by_name,omitempty"`
	ProgressPercent   int          `json:"progress_percent"`
	EstimatedHours    *float64     `json:"estimated_hours,omitempty"`
	ActualHours       *float64     `json:"actual_hours,omitempty"`
	IsRecurring       bool         `json:"is_recurring"`
	RecurrencePattern *string      `json:"recurrence_pattern,omitempty"`
	Tags              []string     `json:"tags"`
	IsOverdue         bool         `json:"is_overdue"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// TaskStats represents task statistics
type TaskStats struct {
	TotalTasks      int            `json:"total_tasks"`
	PendingTasks    int            `json:"pending_tasks"`
	InProgressTasks int            `json:"in_progress_tasks"`
	CompletedTasks  int            `json:"completed_tasks"`
	OverdueTasks    int            `json:"overdue_tasks"`
	DueToday        int            `json:"due_today"`
	DueThisWeek     int            `json:"due_this_week"`
	ByPriority      map[string]int `json:"by_priority"`
	ByType          map[string]int `json:"by_type"`
	CompletionRate  float64        `json:"completion_rate"`
}

// TaskKanbanColumn represents a column in task kanban view
type TaskKanbanColumn struct {
	Status string         `json:"status"`
	Name   string         `json:"name"`
	Count  int            `json:"count"`
	Tasks  []TaskResponse `json:"tasks"`
}
