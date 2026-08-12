package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// AttendanceRecord represents an employee attendance record
type AttendanceRecord struct {
	ID            uuid.UUID    `json:"id" db:"id"`
	TenantID      uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	EmployeeID    uuid.UUID    `json:"employee_id" db:"employee_id"`
	EmployeeName  *string      `json:"employee_name,omitempty" db:"employee_name"`
	Department    *string      `json:"department,omitempty" db:"department"`
	Date          time.Time    `json:"date" db:"date"`
	ClockIn       *time.Time   `json:"clock_in,omitempty" db:"clock_in"`
	ClockOut      *time.Time   `json:"clock_out,omitempty" db:"clock_out"`
	Status        string       `json:"status" db:"status"`                 // present, absent, late, half_day, on_leave
	BreakDuration int          `json:"break_duration" db:"break_duration"` // in minutes
	Notes         *string      `json:"notes,omitempty" db:"notes"`
	CreatedAt     time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt     sql.NullTime `json:"-" db:"deleted_at"`
}

// CreateAttendanceRecordInput represents input for creating an attendance record
type CreateAttendanceRecordInput struct {
	EmployeeID    string `json:"employee_id" binding:"required"`
	EmployeeName  string `json:"employee_name,omitempty"`
	Department    string `json:"department,omitempty"`
	Date          string `json:"date" binding:"required"`   // YYYY-MM-DD format
	ClockIn       string `json:"clock_in,omitempty"`        // ISO8601 format
	ClockOut      string `json:"clock_out,omitempty"`       // ISO8601 format
	Status        string `json:"status" binding:"required"` // present, absent, late, half_day, on_leave
	BreakDuration int    `json:"break_duration"`
	Notes         string `json:"notes,omitempty"`
}

// UpdateAttendanceRecordInput represents input for updating an attendance record
type UpdateAttendanceRecordInput struct {
	EmployeeName  *string `json:"employee_name,omitempty"`
	Department    *string `json:"department,omitempty"`
	Date          *string `json:"date,omitempty"`
	ClockIn       *string `json:"clock_in,omitempty"`
	ClockOut      *string `json:"clock_out,omitempty"`
	Status        *string `json:"status,omitempty"`
	BreakDuration *int    `json:"break_duration,omitempty"`
	Notes         *string `json:"notes,omitempty"`
}

// AttendanceRecordResponse represents the API response for an attendance record
type AttendanceRecordResponse struct {
	ID            string  `json:"id"`
	EmployeeID    string  `json:"employee_id"`
	EmployeeName  *string `json:"employee_name,omitempty"`
	Department    *string `json:"department,omitempty"`
	Date          string  `json:"date"`
	ClockIn       *string `json:"clock_in,omitempty"`
	ClockOut      *string `json:"clock_out,omitempty"`
	Status        string  `json:"status"`
	BreakDuration int     `json:"break_duration"`
	Notes         *string `json:"notes,omitempty"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// ToResponse converts an AttendanceRecord to AttendanceRecordResponse
func (a *AttendanceRecord) ToResponse() *AttendanceRecordResponse {
	resp := &AttendanceRecordResponse{
		ID:            a.ID.String(),
		EmployeeID:    a.EmployeeID.String(),
		EmployeeName:  a.EmployeeName,
		Department:    a.Department,
		Date:          a.Date.Format("2006-01-02"),
		Status:        a.Status,
		BreakDuration: a.BreakDuration,
		Notes:         a.Notes,
		CreatedAt:     a.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     a.UpdatedAt.Format(time.RFC3339),
	}

	if a.ClockIn != nil {
		clockInStr := a.ClockIn.Format(time.RFC3339)
		resp.ClockIn = &clockInStr
	}

	if a.ClockOut != nil {
		clockOutStr := a.ClockOut.Format(time.RFC3339)
		resp.ClockOut = &clockOutStr
	}

	return resp
}
