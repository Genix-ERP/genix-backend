package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// EmployeeContract represents an employee contract (HR contracts, not procurement contracts)
type EmployeeContract struct {
	ID              uuid.UUID    `json:"id" db:"id"`
	TenantID        uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	EmployeeID      uuid.UUID    `json:"employee_id" db:"employee_id"`
	EmployeeName    *string      `json:"employee_name,omitempty" db:"employee_name"`
	JobTitle        *string      `json:"job_title,omitempty" db:"job_title"`
	Department      *string      `json:"department,omitempty" db:"department"`
	ContractType    string       `json:"contract_type" db:"contract_type"` // permanent, fixed_term, probation, contractor, part_time
	StartDate       time.Time    `json:"start_date" db:"start_date"`
	EndDate         *time.Time   `json:"end_date,omitempty" db:"end_date"`
	Salary          float64      `json:"salary" db:"salary"`
	Currency        string       `json:"currency" db:"currency"` // UZS, USD, etc.
	WorkingHours    int          `json:"working_hours" db:"working_hours"` // hours per week
	ProbationPeriod int          `json:"probation_period" db:"probation_period"` // months
	NoticePeriod    int          `json:"notice_period" db:"notice_period"` // days
	Benefits        *string      `json:"benefits,omitempty" db:"benefits"` // JSON or text
	Terms           *string      `json:"terms,omitempty" db:"terms"` // Contract terms
	Status          string       `json:"status" db:"status"` // active, draft, terminated, expired
	SignedDate      *time.Time   `json:"signed_date,omitempty" db:"signed_date"`
	TerminationDate *time.Time   `json:"termination_date,omitempty" db:"termination_date"`
	TerminationNote *string      `json:"termination_note,omitempty" db:"termination_note"`
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime `json:"-" db:"deleted_at"`
}

// CreateEmployeeContractInput represents input for creating an employee contract
type CreateEmployeeContractInput struct {
	EmployeeID      string  `json:"employee_id" binding:"required"`
	EmployeeName    string  `json:"employee_name,omitempty"`
	JobTitle        string  `json:"job_title,omitempty"`
	Department      string  `json:"department,omitempty"`
	ContractType    string  `json:"contract_type" binding:"required"`
	StartDate       string  `json:"start_date" binding:"required"` // YYYY-MM-DD
	EndDate         string  `json:"end_date,omitempty"`
	Salary          float64 `json:"salary" binding:"required"`
	Currency        string  `json:"currency"`
	WorkingHours    int     `json:"working_hours"`
	ProbationPeriod int     `json:"probation_period"`
	NoticePeriod    int     `json:"notice_period"`
	Benefits        string  `json:"benefits,omitempty"`
	Terms           string  `json:"terms,omitempty"`
	Status          string  `json:"status"`
	SignedDate      string  `json:"signed_date,omitempty"`
}

// UpdateEmployeeContractInput represents input for updating an employee contract
type UpdateEmployeeContractInput struct {
	EmployeeName      *string  `json:"employee_name,omitempty"`
	JobTitle          *string  `json:"job_title,omitempty"`
	Department        *string  `json:"department,omitempty"`
	ContractType      *string  `json:"contract_type,omitempty"`
	StartDate         *string  `json:"start_date,omitempty"`
	EndDate           *string  `json:"end_date,omitempty"`
	Salary            *float64 `json:"salary,omitempty"`
	Currency          *string  `json:"currency,omitempty"`
	WorkingHours      *int     `json:"working_hours,omitempty"`
	ProbationPeriod   *int     `json:"probation_period,omitempty"`
	NoticePeriod      *int     `json:"notice_period,omitempty"`
	Benefits          *string  `json:"benefits,omitempty"`
	Terms             *string  `json:"terms,omitempty"`
	Status            *string  `json:"status,omitempty"`
	SignedDate        *string  `json:"signed_date,omitempty"`
	TerminationDate   *string  `json:"termination_date,omitempty"`
	TerminationNote   *string  `json:"termination_note,omitempty"`
}

// EmployeeContractResponse represents the API response for an employee contract
type EmployeeContractResponse struct {
	ID              string  `json:"id"`
	EmployeeID      string  `json:"employee_id"`
	EmployeeName    *string `json:"employee_name,omitempty"`
	JobTitle        *string `json:"job_title,omitempty"`
	Department      *string `json:"department,omitempty"`
	ContractType    string  `json:"contract_type"`
	StartDate       string  `json:"start_date"`
	EndDate         *string `json:"end_date,omitempty"`
	Salary          float64 `json:"salary"`
	Currency        string  `json:"currency"`
	WorkingHours    int     `json:"working_hours"`
	ProbationPeriod int     `json:"probation_period"`
	NoticePeriod    int     `json:"notice_period"`
	Benefits        *string `json:"benefits,omitempty"`
	Terms           *string `json:"terms,omitempty"`
	Status          string  `json:"status"`
	SignedDate      *string `json:"signed_date,omitempty"`
	TerminationDate *string `json:"termination_date,omitempty"`
	TerminationNote *string `json:"termination_note,omitempty"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// ToResponse converts an EmployeeContract to EmployeeContractResponse
func (e *EmployeeContract) ToResponse() *EmployeeContractResponse {
	resp := &EmployeeContractResponse{
		ID:              e.ID.String(),
		EmployeeID:      e.EmployeeID.String(),
		EmployeeName:    e.EmployeeName,
		JobTitle:        e.JobTitle,
		Department:      e.Department,
		ContractType:    e.ContractType,
		StartDate:       e.StartDate.Format("2006-01-02"),
		Salary:          e.Salary,
		Currency:        e.Currency,
		WorkingHours:    e.WorkingHours,
		ProbationPeriod: e.ProbationPeriod,
		NoticePeriod:    e.NoticePeriod,
		Benefits:        e.Benefits,
		Terms:           e.Terms,
		Status:          e.Status,
		CreatedAt:       e.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       e.UpdatedAt.Format(time.RFC3339),
	}

	if e.EndDate != nil {
		endDateStr := e.EndDate.Format("2006-01-02")
		resp.EndDate = &endDateStr
	}

	if e.SignedDate != nil {
		signedDateStr := e.SignedDate.Format("2006-01-02")
		resp.SignedDate = &signedDateStr
	}

	if e.TerminationDate != nil {
		terminationDateStr := e.TerminationDate.Format("2006-01-02")
		resp.TerminationDate = &terminationDateStr
	}

	if e.TerminationNote != nil {
		resp.TerminationNote = e.TerminationNote
	}

	return resp
}
