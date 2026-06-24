package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Project represents a project
type Project struct {
	ID           uuid.UUID    `json:"id" db:"id"`
	TenantID     uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	ProjectCode  string       `json:"project_code" db:"project_code"`
	ProjectName  string       `json:"project_name" db:"project_name"`
	Description  *string      `json:"description,omitempty" db:"description"`
	ClientID     *uuid.UUID   `json:"client_id,omitempty" db:"client_id"`
	ClientName   *string      `json:"client_name,omitempty" db:"client_name"`
	ManagerID    *uuid.UUID   `json:"manager_id,omitempty" db:"manager_id"`
	ManagerName  *string      `json:"manager_name,omitempty" db:"manager_name"`
	StartDate    time.Time    `json:"start_date" db:"start_date"`
	EndDate      *time.Time   `json:"end_date,omitempty" db:"end_date"`
	Budget       float64      `json:"budget" db:"budget"`
	Spent        float64      `json:"spent" db:"spent"`
	BillingType  string       `json:"billing_type" db:"billing_type"`
	HourlyRate   float64      `json:"hourly_rate" db:"hourly_rate"`
	Currency     string       `json:"currency" db:"currency"`
	Priority     string       `json:"priority" db:"priority"`
	Status       string       `json:"status" db:"status"`
	Progress     int          `json:"progress" db:"progress"`
	Notes        *string      `json:"notes,omitempty" db:"notes"`
	CreatedBy    *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt    time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt    sql.NullTime `json:"-" db:"deleted_at"`

	// Computed fields
	TeamMembers []string `json:"team_members,omitempty"`
	TotalHours  float64  `json:"total_hours"`
}

// ProjectTask represents a task within a project
type ProjectTask struct {
	ID             uuid.UUID    `json:"id" db:"id"`
	TenantID       uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	ProjectID      uuid.UUID    `json:"project_id" db:"project_id"`
	ParentID       *uuid.UUID   `json:"parent_id,omitempty" db:"parent_id"`
	MilestoneID    *uuid.UUID   `json:"milestone_id,omitempty" db:"milestone_id"`
	TaskNumber     string       `json:"task_number" db:"task_number"`
	Title          string       `json:"title" db:"title"`
	Description    *string      `json:"description,omitempty" db:"description"`
	AssigneeID     *uuid.UUID   `json:"assignee_id,omitempty" db:"assignee_id"`
	AssigneeName   *string      `json:"assignee_name,omitempty" db:"assignee_name"`
	Priority       string       `json:"priority" db:"priority"`
	Status         string       `json:"status" db:"status"`
	DueDate        *time.Time   `json:"due_date,omitempty" db:"due_date"`
	EstimatedHours float64      `json:"estimated_hours" db:"estimated_hours"`
	ActualHours    float64      `json:"actual_hours" db:"actual_hours"`
	CreatedBy      *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt      time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at" db:"updated_at"`
	NoteCount      int          `json:"note_count" db:"note_count"`
	DeletedAt      sql.NullTime `json:"-" db:"deleted_at"`
}

// ProjectMilestone represents a milestone within a project
type ProjectMilestone struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	TenantID      uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProjectID     uuid.UUID  `json:"project_id" db:"project_id"`
	Title         string     `json:"title" db:"title"`
	Description   *string    `json:"description,omitempty" db:"description"`
	DueDate       time.Time  `json:"due_date" db:"due_date"`
	Status        string     `json:"status" db:"status"`
	CompletedDate *time.Time `json:"completed_date,omitempty" db:"completed_date"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`

	// Computed fields
	Deliverables []string `json:"deliverables,omitempty"`
}

// TimeEntry represents a time tracking entry
type TimeEntry struct {
	ID           uuid.UUID    `json:"id" db:"id"`
	TenantID     uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	ProjectID    uuid.UUID    `json:"project_id" db:"project_id"`
	TaskID       *uuid.UUID   `json:"task_id,omitempty" db:"task_id"`
	EmployeeID   uuid.UUID    `json:"employee_id" db:"employee_id"`
	EmployeeName string       `json:"employee_name" db:"employee_name"`
	EntryDate    time.Time    `json:"entry_date" db:"entry_date"`
	Hours        float64      `json:"hours" db:"hours"`
	Description  *string      `json:"description,omitempty" db:"description"`
	Billable     bool         `json:"billable" db:"billable"`
	HourlyRate   *float64     `json:"hourly_rate,omitempty" db:"hourly_rate"`
	Amount       *float64     `json:"amount,omitempty" db:"amount"`
	Status       string       `json:"status" db:"status"`
	ApprovedBy   *uuid.UUID   `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt   *time.Time   `json:"approved_at,omitempty" db:"approved_at"`
	CreatedAt    time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt    sql.NullTime `json:"-" db:"deleted_at"`
}

// ProjectExpense represents an expense logged against a project
type ProjectExpense struct {
	ID                uuid.UUID    `json:"id" db:"id"`
	TenantID          uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	ProjectID         uuid.UUID    `json:"project_id" db:"project_id"`
	ExpenseNumber     string       `json:"expense_number" db:"expense_number"`
	Category          *string      `json:"category,omitempty" db:"category"`
	Description       string       `json:"description" db:"description"`
	Amount            float64      `json:"amount" db:"amount"`
	Currency          string       `json:"currency" db:"currency"`
	ExpenseDate       time.Time    `json:"expense_date" db:"expense_date"`
	EmployeeID        *uuid.UUID   `json:"employee_id,omitempty" db:"employee_id"`
	EmployeeName      *string      `json:"employee_name,omitempty" db:"employee_name"`
	VendorID          *uuid.UUID   `json:"vendor_id,omitempty" db:"vendor_id"`
	VendorName        *string      `json:"vendor_name,omitempty" db:"vendor_name"`
	PurchaseInvoiceID *uuid.UUID   `json:"purchase_invoice_id,omitempty" db:"purchase_invoice_id"`
	ReceiptURL        *string      `json:"receipt_url,omitempty" db:"receipt_url"`
	Billable          bool         `json:"billable" db:"billable"`
	Status            string       `json:"status" db:"status"`
	ApprovedBy        *uuid.UUID   `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt        *time.Time   `json:"approved_at,omitempty" db:"approved_at"`
	Notes             *string      `json:"notes,omitempty" db:"notes"`
	CreatedBy         *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt         time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt         sql.NullTime `json:"-" db:"deleted_at"`
}

// CreateProjectInput represents input for creating a project
type CreateProjectInput struct {
	ProjectCode string   `json:"project_code"`
	ProjectName string   `json:"project_name" binding:"required"`
	Description string   `json:"description,omitempty"`
	ClientID    string   `json:"client_id,omitempty"`
	ClientName  string   `json:"client_name,omitempty"`
	ManagerID   string   `json:"manager_id,omitempty"`
	ManagerName string   `json:"manager_name,omitempty"`
	StartDate   string   `json:"start_date" binding:"required"`
	EndDate     string   `json:"end_date,omitempty"`
	Budget      float64  `json:"budget"`
	BillingType string   `json:"billing_type"`
	HourlyRate  float64  `json:"hourly_rate"`
	Currency    string   `json:"currency"`
	Priority    string   `json:"priority"`
	TeamMembers []string `json:"team_members,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}

// UpdateProjectInput represents input for updating a project
type UpdateProjectInput struct {
	ProjectName *string  `json:"project_name,omitempty"`
	Description *string  `json:"description,omitempty"`
	ClientID    *string  `json:"client_id,omitempty"`
	ClientName  *string  `json:"client_name,omitempty"`
	ManagerID   *string  `json:"manager_id,omitempty"`
	ManagerName *string  `json:"manager_name,omitempty"`
	StartDate   *string  `json:"start_date,omitempty"`
	EndDate     *string  `json:"end_date,omitempty"`
	Budget      *float64 `json:"budget,omitempty"`
	Spent       *float64 `json:"spent,omitempty"`
	BillingType *string  `json:"billing_type,omitempty"`
	HourlyRate  *float64 `json:"hourly_rate,omitempty"`
	Currency    *string  `json:"currency,omitempty"`
	Priority    *string  `json:"priority,omitempty"`
	Status      *string  `json:"status,omitempty"`
	Progress    *int     `json:"progress,omitempty"`
	Notes       *string  `json:"notes,omitempty"`
}

// CreateProjectTaskInput represents input for creating a project task
type CreateProjectTaskInput struct {
	TaskNumber     string  `json:"task_number"`
	Title          string  `json:"title" binding:"required"`
	Description    string  `json:"description,omitempty"`
	AssigneeID     string  `json:"assignee_id,omitempty"`
	AssigneeName   string  `json:"assignee_name,omitempty"`
	MilestoneID    string  `json:"milestone_id,omitempty"`
	Priority       string  `json:"priority"`
	DueDate        string  `json:"due_date,omitempty"`
	EstimatedHours float64 `json:"estimated_hours"`
}

// UpdateProjectTaskInput represents input for updating a project task
type UpdateProjectTaskInput struct {
	Title          *string  `json:"title,omitempty"`
	Description    *string  `json:"description,omitempty"`
	AssigneeID     *string  `json:"assignee_id,omitempty"`
	AssigneeName   *string  `json:"assignee_name,omitempty"`
	MilestoneID    *string  `json:"milestone_id,omitempty"`
	Priority       *string  `json:"priority,omitempty"`
	Status         *string  `json:"status,omitempty"`
	DueDate        *string  `json:"due_date,omitempty"`
	EstimatedHours *float64 `json:"estimated_hours,omitempty"`
	ActualHours    *float64 `json:"actual_hours,omitempty"`
}

// ProjectTaskStage represents a kanban column / stage for a project's tasks
type ProjectTaskStage struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	ProjectID uuid.UUID `json:"project_id"`
	StageKey  string    `json:"stage_key"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	Position  int       `json:"position"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateProjectStageInput represents input for creating a task stage
type CreateProjectStageInput struct {
	Name  string `json:"name" binding:"required"`
	Color string `json:"color,omitempty"`
}

// UpdateProjectStageInput represents input for updating a task stage
type UpdateProjectStageInput struct {
	Name     *string `json:"name,omitempty"`
	Color    *string `json:"color,omitempty"`
	Position *int    `json:"position,omitempty"`
}

// ProjectMilestoneSubstage represents a sub-step under a milestone
type ProjectMilestoneSubstage struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	ProjectID   uuid.UUID `json:"project_id"`
	MilestoneID uuid.UUID `json:"milestone_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	DueDate     string    `json:"due_date,omitempty"`
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateSubstageInput represents input for creating a substage
type CreateSubstageInput struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
}

// UpdateSubstageInput represents input for updating a substage
type UpdateSubstageInput struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	DueDate     *string `json:"due_date,omitempty"`
}

// ProjectTaskNote represents a note/comment left on a task
type ProjectTaskNote struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      uuid.UUID  `json:"tenant_id"`
	ProjectID     uuid.UUID  `json:"project_id"`
	TaskID        uuid.UUID  `json:"task_id"`
	Note          string     `json:"note"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
	CreatedByName string     `json:"created_by_name"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CreateTaskNoteInput represents input for creating a task note
type CreateTaskNoteInput struct {
	Note          string `json:"note" binding:"required"`
	CreatedByName string `json:"created_by_name,omitempty"`
}

// CreateMilestoneInput represents input for creating a milestone
type CreateMilestoneInput struct {
	Title        string   `json:"title" binding:"required"`
	Description  string   `json:"description,omitempty"`
	DueDate      string   `json:"due_date" binding:"required"`
	Deliverables []string `json:"deliverables,omitempty"`
}

// UpdateMilestoneInput represents input for updating a milestone
type UpdateMilestoneInput struct {
	Title        *string  `json:"title,omitempty"`
	Description  *string  `json:"description,omitempty"`
	DueDate      *string  `json:"due_date,omitempty"`
	Status       *string  `json:"status,omitempty"`
	Deliverables []string `json:"deliverables,omitempty"`
}

// CreateTimeEntryInput represents input for creating a time entry
type CreateTimeEntryInput struct {
	TaskID       string  `json:"task_id,omitempty"`
	EmployeeID   string  `json:"employee_id" binding:"required"`
	EmployeeName string  `json:"employee_name,omitempty"`
	EntryDate    string  `json:"date" binding:"required"`
	Hours        float64 `json:"hours" binding:"required"`
	Description  string  `json:"description,omitempty"`
	Billable     bool    `json:"billable"`
	HourlyRate   float64 `json:"hourly_rate,omitempty"`
}

// UpdateTimeEntryInput represents input for updating a time entry
type UpdateTimeEntryInput struct {
	TaskID      *string  `json:"task_id,omitempty"`
	EntryDate   *string  `json:"date,omitempty"`
	Hours       *float64 `json:"hours,omitempty"`
	Description *string  `json:"description,omitempty"`
	Billable    *bool    `json:"billable,omitempty"`
	HourlyRate  *float64 `json:"hourly_rate,omitempty"`
	Status      *string  `json:"status,omitempty"`
}

// CreateProjectExpenseInput represents input for creating a project expense
type CreateProjectExpenseInput struct {
	Category     string  `json:"category,omitempty"`
	Description  string  `json:"description" binding:"required"`
	Amount       float64 `json:"amount" binding:"required"`
	Currency     string  `json:"currency,omitempty"`
	ExpenseDate  string  `json:"expense_date" binding:"required"`
	EmployeeID   string  `json:"employee_id,omitempty"`
	EmployeeName string  `json:"employee_name,omitempty"`
	VendorID     string  `json:"vendor_id,omitempty"`
	VendorName   string  `json:"vendor_name,omitempty"`
	ReceiptURL   string  `json:"receipt_url,omitempty"`
	Billable     bool    `json:"billable"`
	Notes        string  `json:"notes,omitempty"`
}

// UpdateProjectExpenseInput represents input for updating a project expense
type UpdateProjectExpenseInput struct {
	Category     *string  `json:"category,omitempty"`
	Description  *string  `json:"description,omitempty"`
	Amount       *float64 `json:"amount,omitempty"`
	Currency     *string  `json:"currency,omitempty"`
	ExpenseDate  *string  `json:"expense_date,omitempty"`
	EmployeeID   *string  `json:"employee_id,omitempty"`
	EmployeeName *string  `json:"employee_name,omitempty"`
	VendorName   *string  `json:"vendor_name,omitempty"`
	ReceiptURL   *string  `json:"receipt_url,omitempty"`
	Billable     *bool    `json:"billable,omitempty"`
	Status       *string  `json:"status,omitempty"`
	Notes        *string  `json:"notes,omitempty"`
}

// ProjectResponse represents the API response for a project
type ProjectResponse struct {
	ID           uuid.UUID `json:"id"`
	ProjectCode  string    `json:"project_code"`
	ProjectName  string    `json:"project_name"`
	Description  string    `json:"description,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientName   string    `json:"client_name,omitempty"`
	ManagerID    string    `json:"manager_id,omitempty"`
	ManagerName  string    `json:"manager_name,omitempty"`
	StartDate    string    `json:"start_date"`
	EndDate      string    `json:"end_date,omitempty"`
	Budget       float64   `json:"budget"`
	Spent        float64   `json:"spent"`
	TotalHours   float64   `json:"total_hours"`
	BillingType  string    `json:"billing_type"`
	HourlyRate   float64   `json:"hourly_rate"`
	Currency     string    `json:"currency"`
	Priority     string    `json:"priority"`
	Status       string    `json:"status"`
	Progress     int       `json:"progress"`
	TeamMembers  []string  `json:"team_members,omitempty"`
	Notes        string    `json:"notes,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ToResponse converts Project to ProjectResponse
func (p *Project) ToResponse() *ProjectResponse {
	resp := &ProjectResponse{
		ID:          p.ID,
		ProjectCode: p.ProjectCode,
		ProjectName: p.ProjectName,
		StartDate:   p.StartDate.Format("2006-01-02"),
		Budget:      p.Budget,
		Spent:       p.Spent,
		TotalHours:  p.TotalHours,
		BillingType: p.BillingType,
		HourlyRate:  p.HourlyRate,
		Currency:    p.Currency,
		Priority:    p.Priority,
		Status:      p.Status,
		Progress:    p.Progress,
		TeamMembers: p.TeamMembers,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}

	if p.Description != nil {
		resp.Description = *p.Description
	}
	if p.ClientID != nil {
		resp.ClientID = p.ClientID.String()
	}
	if p.ClientName != nil {
		resp.ClientName = *p.ClientName
	}
	if p.ManagerID != nil {
		resp.ManagerID = p.ManagerID.String()
	}
	if p.ManagerName != nil {
		resp.ManagerName = *p.ManagerName
	}
	if p.EndDate != nil {
		resp.EndDate = p.EndDate.Format("2006-01-02")
	}
	if p.Notes != nil {
		resp.Notes = *p.Notes
	}

	return resp
}

// ProjectTaskResponse represents the API response for a project task
type ProjectTaskResponse struct {
	ID             uuid.UUID `json:"id"`
	ProjectID      uuid.UUID `json:"project_id"`
	TaskNumber     string    `json:"task_number"`
	Title          string    `json:"title"`
	Description    string    `json:"description,omitempty"`
	AssigneeID     string    `json:"assignee_id,omitempty"`
	AssigneeName   string    `json:"assignee_name,omitempty"`
	MilestoneID    string    `json:"milestone_id,omitempty"`
	Priority       string    `json:"priority"`
	Status         string    `json:"status"`
	DueDate        string    `json:"due_date,omitempty"`
	EstimatedHours float64   `json:"estimated_hours"`
	ActualHours    float64   `json:"actual_hours"`
	NoteCount      int       `json:"note_count"`
	CreatedAt      time.Time `json:"created_at"`
}

// ToResponse converts ProjectTask to ProjectTaskResponse
func (t *ProjectTask) ToResponse() *ProjectTaskResponse {
	resp := &ProjectTaskResponse{
		ID:             t.ID,
		ProjectID:      t.ProjectID,
		TaskNumber:     t.TaskNumber,
		Title:          t.Title,
		Priority:       t.Priority,
		Status:         t.Status,
		EstimatedHours: t.EstimatedHours,
		ActualHours:    t.ActualHours,
		NoteCount:      t.NoteCount,
		CreatedAt:      t.CreatedAt,
	}

	if t.Description != nil {
		resp.Description = *t.Description
	}
	if t.AssigneeID != nil {
		resp.AssigneeID = t.AssigneeID.String()
	}
	if t.AssigneeName != nil {
		resp.AssigneeName = *t.AssigneeName
	}
	if t.MilestoneID != nil {
		resp.MilestoneID = t.MilestoneID.String()
	}
	if t.DueDate != nil {
		resp.DueDate = t.DueDate.Format("2006-01-02")
	}

	return resp
}

// MilestoneResponse represents the API response for a milestone
type MilestoneResponse struct {
	ID            uuid.UUID `json:"id"`
	ProjectID     uuid.UUID `json:"project_id"`
	Title         string    `json:"title"`
	Description   string    `json:"description,omitempty"`
	DueDate       string    `json:"due_date"`
	Status        string    `json:"status"`
	CompletedDate string    `json:"completed_date,omitempty"`
	Deliverables  []string  `json:"deliverables,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ToResponse converts ProjectMilestone to MilestoneResponse
func (m *ProjectMilestone) ToResponse() *MilestoneResponse {
	resp := &MilestoneResponse{
		ID:           m.ID,
		ProjectID:    m.ProjectID,
		Title:        m.Title,
		DueDate:      m.DueDate.Format("2006-01-02"),
		Status:       m.Status,
		Deliverables: m.Deliverables,
		CreatedAt:    m.CreatedAt,
	}

	if m.Description != nil {
		resp.Description = *m.Description
	}
	if m.CompletedDate != nil {
		resp.CompletedDate = m.CompletedDate.Format("2006-01-02")
	}

	return resp
}

// TimeEntryResponse represents the API response for a time entry
type TimeEntryResponse struct {
	ID           uuid.UUID `json:"id"`
	ProjectID    uuid.UUID `json:"project_id"`
	TaskID       string    `json:"task_id,omitempty"`
	EmployeeID   uuid.UUID `json:"employee_id"`
	EmployeeName string    `json:"employee_name"`
	EntryDate    string    `json:"date"`
	Hours        float64   `json:"hours"`
	Description  string    `json:"description,omitempty"`
	Billable     bool      `json:"billable"`
	HourlyRate   float64   `json:"hourly_rate,omitempty"`
	Amount       float64   `json:"amount,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// ToResponse converts TimeEntry to TimeEntryResponse
func (e *TimeEntry) ToResponse() *TimeEntryResponse {
	resp := &TimeEntryResponse{
		ID:           e.ID,
		ProjectID:    e.ProjectID,
		EmployeeID:   e.EmployeeID,
		EmployeeName: e.EmployeeName,
		EntryDate:    e.EntryDate.Format("2006-01-02"),
		Hours:        e.Hours,
		Billable:     e.Billable,
		Status:       e.Status,
		CreatedAt:    e.CreatedAt,
	}

	if e.TaskID != nil {
		resp.TaskID = e.TaskID.String()
	}
	if e.Description != nil {
		resp.Description = *e.Description
	}
	if e.HourlyRate != nil {
		resp.HourlyRate = *e.HourlyRate
	}
	if e.Amount != nil {
		resp.Amount = *e.Amount
	}

	return resp
}

// ProjectExpenseResponse represents the API response for a project expense
type ProjectExpenseResponse struct {
	ID                uuid.UUID `json:"id"`
	ProjectID         uuid.UUID `json:"project_id"`
	ExpenseNumber     string    `json:"expense_number"`
	Category          string    `json:"category,omitempty"`
	Description       string    `json:"description"`
	Amount            float64   `json:"amount"`
	Currency          string    `json:"currency"`
	ExpenseDate       string    `json:"expense_date"`
	EmployeeID        string    `json:"employee_id,omitempty"`
	EmployeeName      string    `json:"employee_name,omitempty"`
	VendorID          string    `json:"vendor_id,omitempty"`
	VendorName        string    `json:"vendor_name,omitempty"`
	PurchaseInvoiceID string    `json:"purchase_invoice_id,omitempty"`
	ReceiptURL        string    `json:"receipt_url,omitempty"`
	Billable          bool      `json:"billable"`
	Status            string    `json:"status"`
	Notes             string    `json:"notes,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// ToResponse converts ProjectExpense to ProjectExpenseResponse
func (pe *ProjectExpense) ToResponse() *ProjectExpenseResponse {
	resp := &ProjectExpenseResponse{
		ID:            pe.ID,
		ProjectID:     pe.ProjectID,
		ExpenseNumber: pe.ExpenseNumber,
		Description:   pe.Description,
		Amount:        pe.Amount,
		Currency:      pe.Currency,
		ExpenseDate:   pe.ExpenseDate.Format("2006-01-02"),
		Billable:      pe.Billable,
		Status:        pe.Status,
		CreatedAt:     pe.CreatedAt,
	}

	if pe.Category != nil {
		resp.Category = *pe.Category
	}
	if pe.EmployeeID != nil {
		resp.EmployeeID = pe.EmployeeID.String()
	}
	if pe.EmployeeName != nil {
		resp.EmployeeName = *pe.EmployeeName
	}
	if pe.VendorID != nil {
		resp.VendorID = pe.VendorID.String()
	}
	if pe.VendorName != nil {
		resp.VendorName = *pe.VendorName
	}
	if pe.PurchaseInvoiceID != nil {
		resp.PurchaseInvoiceID = pe.PurchaseInvoiceID.String()
	}
	if pe.ReceiptURL != nil {
		resp.ReceiptURL = *pe.ReceiptURL
	}
	if pe.Notes != nil {
		resp.Notes = *pe.Notes
	}

	return resp
}

// ProjectTeamMember represents a team member assigned to a project
type ProjectTeamMember struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	TenantID          uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProjectID         uuid.UUID  `json:"project_id" db:"project_id"`
	EmployeeID        uuid.UUID  `json:"employee_id" db:"employee_id"`
	EmployeeName      string     `json:"employee_name" db:"employee_name"`
	Role              *string    `json:"role,omitempty" db:"role"`
	AllocationPercent int        `json:"allocation_percent" db:"allocation_percent"`
	StartDate         *time.Time `json:"start_date,omitempty" db:"start_date"`
	EndDate           *time.Time `json:"end_date,omitempty" db:"end_date"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
}

// CreateTeamMemberInput represents input for adding a team member
type CreateTeamMemberInput struct {
	EmployeeID        string `json:"employee_id" binding:"required"`
	EmployeeName      string `json:"employee_name"`
	Role              string `json:"role,omitempty"`
	AllocationPercent int    `json:"allocation_percent"`
	StartDate         string `json:"start_date,omitempty"`
	EndDate           string `json:"end_date,omitempty"`
}

// ProjectTeamMemberResponse represents the API response for a team member
type ProjectTeamMemberResponse struct {
	ID                uuid.UUID `json:"id"`
	ProjectID         uuid.UUID `json:"project_id"`
	EmployeeID        string    `json:"employee_id"`
	EmployeeName      string    `json:"employee_name"`
	Role              string    `json:"role,omitempty"`
	AllocationPercent int       `json:"allocation_percent"`
	StartDate         string    `json:"start_date,omitempty"`
	EndDate           string    `json:"end_date,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// ToResponse converts ProjectTeamMember to ProjectTeamMemberResponse
func (tm *ProjectTeamMember) ToResponse() *ProjectTeamMemberResponse {
	resp := &ProjectTeamMemberResponse{
		ID:                tm.ID,
		ProjectID:         tm.ProjectID,
		EmployeeID:        tm.EmployeeID.String(),
		EmployeeName:      tm.EmployeeName,
		AllocationPercent: tm.AllocationPercent,
		CreatedAt:         tm.CreatedAt,
	}

	if tm.Role != nil {
		resp.Role = *tm.Role
	}
	if tm.StartDate != nil {
		resp.StartDate = tm.StartDate.Format("2006-01-02")
	}
	if tm.EndDate != nil {
		resp.EndDate = tm.EndDate.Format("2006-01-02")
	}

	return resp
}
