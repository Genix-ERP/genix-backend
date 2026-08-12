package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// LeaveRequest represents an employee leave request
type LeaveRequest struct {
	ID            uuid.UUID    `json:"id" db:"id"`
	TenantID      uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	EmployeeID    uuid.UUID    `json:"employee_id" db:"employee_id"`
	EmployeeName  *string      `json:"employee_name,omitempty" db:"employee_name"`
	LeaveType     string       `json:"leave_type" db:"leave_type"` // annual, sick, personal, unpaid, etc.
	StartDate     time.Time    `json:"start_date" db:"start_date"`
	EndDate       time.Time    `json:"end_date" db:"end_date"`
	Days          float64      `json:"days" db:"days"` // Can be 0.5 for half day
	Reason        string       `json:"reason" db:"reason"`
	Status        string       `json:"status" db:"status"` // pending, approved, rejected
	HalfDay       bool         `json:"half_day" db:"half_day"`
	HalfDayPeriod *string      `json:"half_day_period,omitempty" db:"half_day_period"` // morning, afternoon
	ApprovedBy    *uuid.UUID   `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt    *time.Time   `json:"approved_at,omitempty" db:"approved_at"`
	RejectionNote *string      `json:"rejection_note,omitempty" db:"rejection_note"`
	CreatedAt     time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt     sql.NullTime `json:"-" db:"deleted_at"`
}

// LeaveBalance represents an employee's leave balance
type LeaveBalance struct {
	ID         uuid.UUID `json:"id" db:"id"`
	TenantID   uuid.UUID `json:"tenant_id" db:"tenant_id"`
	EmployeeID uuid.UUID `json:"employee_id" db:"employee_id"`

	// Annual leave
	AnnualTotal     float64 `json:"annual_total" db:"annual_total"`
	AnnualUsed      float64 `json:"annual_used" db:"annual_used"`
	AnnualPending   float64 `json:"annual_pending" db:"annual_pending"`
	AnnualRemaining float64 `json:"annual_remaining" db:"annual_remaining"`

	// Sick leave
	SickTotal     float64 `json:"sick_total" db:"sick_total"`
	SickUsed      float64 `json:"sick_used" db:"sick_used"`
	SickPending   float64 `json:"sick_pending" db:"sick_pending"`
	SickRemaining float64 `json:"sick_remaining" db:"sick_remaining"`

	// Personal leave
	PersonalTotal     float64 `json:"personal_total" db:"personal_total"`
	PersonalUsed      float64 `json:"personal_used" db:"personal_used"`
	PersonalPending   float64 `json:"personal_pending" db:"personal_pending"`
	PersonalRemaining float64 `json:"personal_remaining" db:"personal_remaining"`

	// Unpaid leave
	UnpaidUsed float64 `json:"unpaid_used" db:"unpaid_used"`

	Year      int          `json:"year" db:"year"` // Fiscal year
	CreatedAt time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt sql.NullTime `json:"-" db:"deleted_at"`
}

// CreateLeaveRequestInput represents input for creating a leave request
type CreateLeaveRequestInput struct {
	EmployeeID    string  `json:"employee_id" binding:"required"`
	EmployeeName  string  `json:"employee_name,omitempty"`
	LeaveType     string  `json:"leave_type" binding:"required"`
	StartDate     string  `json:"start_date" binding:"required"` // YYYY-MM-DD
	EndDate       string  `json:"end_date" binding:"required"`   // YYYY-MM-DD
	Days          float64 `json:"days" binding:"required"`
	Reason        string  `json:"reason" binding:"required"`
	HalfDay       bool    `json:"half_day"`
	HalfDayPeriod string  `json:"half_day_period,omitempty"`
}

// UpdateLeaveRequestInput represents input for updating a leave request
type UpdateLeaveRequestInput struct {
	LeaveType     *string  `json:"leave_type,omitempty"`
	StartDate     *string  `json:"start_date,omitempty"`
	EndDate       *string  `json:"end_date,omitempty"`
	Days          *float64 `json:"days,omitempty"`
	Reason        *string  `json:"reason,omitempty"`
	Status        *string  `json:"status,omitempty"`
	HalfDay       *bool    `json:"half_day,omitempty"`
	HalfDayPeriod *string  `json:"half_day_period,omitempty"`
	RejectionNote *string  `json:"rejection_note,omitempty"`
}

// CreateLeaveBalanceInput represents input for creating a leave balance
type CreateLeaveBalanceInput struct {
	EmployeeID    string  `json:"employee_id" binding:"required"`
	AnnualTotal   float64 `json:"annual_total"`
	SickTotal     float64 `json:"sick_total"`
	PersonalTotal float64 `json:"personal_total"`
	Year          int     `json:"year"`
}

// UpdateLeaveBalanceInput represents input for updating a leave balance
type UpdateLeaveBalanceInput struct {
	AnnualTotal       *float64 `json:"annual_total,omitempty"`
	AnnualUsed        *float64 `json:"annual_used,omitempty"`
	AnnualPending     *float64 `json:"annual_pending,omitempty"`
	AnnualRemaining   *float64 `json:"annual_remaining,omitempty"`
	SickTotal         *float64 `json:"sick_total,omitempty"`
	SickUsed          *float64 `json:"sick_used,omitempty"`
	SickPending       *float64 `json:"sick_pending,omitempty"`
	SickRemaining     *float64 `json:"sick_remaining,omitempty"`
	PersonalTotal     *float64 `json:"personal_total,omitempty"`
	PersonalUsed      *float64 `json:"personal_used,omitempty"`
	PersonalPending   *float64 `json:"personal_pending,omitempty"`
	PersonalRemaining *float64 `json:"personal_remaining,omitempty"`
	UnpaidUsed        *float64 `json:"unpaid_used,omitempty"`
}

// LeaveRequestResponse represents the API response for a leave request
type LeaveRequestResponse struct {
	ID            string  `json:"id"`
	EmployeeID    string  `json:"employee_id"`
	EmployeeName  *string `json:"employee_name,omitempty"`
	LeaveType     string  `json:"leave_type"`
	StartDate     string  `json:"start_date"`
	EndDate       string  `json:"end_date"`
	Days          float64 `json:"days"`
	Reason        string  `json:"reason"`
	Status        string  `json:"status"`
	HalfDay       bool    `json:"half_day"`
	HalfDayPeriod *string `json:"half_day_period,omitempty"`
	ApprovedBy    *string `json:"approved_by,omitempty"`
	ApprovedAt    *string `json:"approved_at,omitempty"`
	RejectionNote *string `json:"rejection_note,omitempty"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// LeaveBalanceResponse represents the API response for leave balance
type LeaveBalanceResponse struct {
	ID         string                   `json:"id"`
	EmployeeID string                   `json:"employee_id"`
	Annual     LeaveBalanceTypeResponse `json:"annual"`
	Sick       LeaveBalanceTypeResponse `json:"sick"`
	Personal   LeaveBalanceTypeResponse `json:"personal"`
	UnpaidUsed float64                  `json:"unpaid_used"`
	Year       int                      `json:"year"`
	CreatedAt  string                   `json:"created_at"`
	UpdatedAt  string                   `json:"updated_at"`
}

// LeaveBalanceTypeResponse represents balance for a specific leave type
type LeaveBalanceTypeResponse struct {
	Total     float64 `json:"total"`
	Used      float64 `json:"used"`
	Pending   float64 `json:"pending"`
	Remaining float64 `json:"remaining"`
}

// ToResponse converts a LeaveRequest to LeaveRequestResponse
func (l *LeaveRequest) ToResponse() *LeaveRequestResponse {
	resp := &LeaveRequestResponse{
		ID:            l.ID.String(),
		EmployeeID:    l.EmployeeID.String(),
		EmployeeName:  l.EmployeeName,
		LeaveType:     l.LeaveType,
		StartDate:     l.StartDate.Format("2006-01-02"),
		EndDate:       l.EndDate.Format("2006-01-02"),
		Days:          l.Days,
		Reason:        l.Reason,
		Status:        l.Status,
		HalfDay:       l.HalfDay,
		HalfDayPeriod: l.HalfDayPeriod,
		RejectionNote: l.RejectionNote,
		CreatedAt:     l.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     l.UpdatedAt.Format(time.RFC3339),
	}

	if l.ApprovedBy != nil {
		approvedByStr := l.ApprovedBy.String()
		resp.ApprovedBy = &approvedByStr
	}

	if l.ApprovedAt != nil {
		approvedAtStr := l.ApprovedAt.Format(time.RFC3339)
		resp.ApprovedAt = &approvedAtStr
	}

	return resp
}

// ToResponse converts a LeaveBalance to LeaveBalanceResponse
func (l *LeaveBalance) ToResponse() *LeaveBalanceResponse {
	return &LeaveBalanceResponse{
		ID:         l.ID.String(),
		EmployeeID: l.EmployeeID.String(),
		Annual: LeaveBalanceTypeResponse{
			Total:     l.AnnualTotal,
			Used:      l.AnnualUsed,
			Pending:   l.AnnualPending,
			Remaining: l.AnnualRemaining,
		},
		Sick: LeaveBalanceTypeResponse{
			Total:     l.SickTotal,
			Used:      l.SickUsed,
			Pending:   l.SickPending,
			Remaining: l.SickRemaining,
		},
		Personal: LeaveBalanceTypeResponse{
			Total:     l.PersonalTotal,
			Used:      l.PersonalUsed,
			Pending:   l.PersonalPending,
			Remaining: l.PersonalRemaining,
		},
		UnpaidUsed: l.UnpaidUsed,
		Year:       l.Year,
		CreatedAt:  l.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  l.UpdatedAt.Format(time.RFC3339),
	}
}
