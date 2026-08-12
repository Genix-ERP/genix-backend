package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Employee represents an employee in the HR system
type Employee struct {
	ID                uuid.UUID    `json:"id" db:"id"`
	TenantID          uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	UserID            *uuid.UUID   `json:"user_id,omitempty" db:"user_id"`
	OrganizationID    *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	DepartmentID      *uuid.UUID   `json:"department_id,omitempty" db:"department_id"`
	JobPositionID     *uuid.UUID   `json:"job_position_id,omitempty" db:"job_position_id"`
	EmployeeNumber    string       `json:"employee_number" db:"employee_number"`
	FirstName         string       `json:"first_name" db:"first_name"`
	LastName          string       `json:"last_name" db:"last_name"`
	MiddleName        *string      `json:"middle_name,omitempty" db:"middle_name"`
	Email             *string      `json:"email,omitempty" db:"email"`
	Phone             *string      `json:"phone,omitempty" db:"phone"`
	Mobile            *string      `json:"mobile,omitempty" db:"mobile"`
	DateOfBirth       *time.Time   `json:"date_of_birth,omitempty" db:"date_of_birth"`
	Gender            *string      `json:"gender,omitempty" db:"gender"`
	Nationality       *string      `json:"nationality,omitempty" db:"nationality"`
	MaritalStatus     *string      `json:"marital_status,omitempty" db:"marital_status"`
	EmploymentType    *string      `json:"employment_type,omitempty" db:"employment_type"`
	JobTitle          *string      `json:"job_title,omitempty" db:"job_title"`
	JobGrade          *string      `json:"job_grade,omitempty" db:"job_grade"`
	HireDate          time.Time    `json:"hire_date" db:"hire_date"`
	ProbationEndDate  *time.Time   `json:"probation_end_date,omitempty" db:"probation_end_date"`
	TerminationDate   *time.Time   `json:"termination_date,omitempty" db:"termination_date"`
	TerminationReason *string      `json:"termination_reason,omitempty" db:"termination_reason"`
	ManagerID         *uuid.UUID   `json:"manager_id,omitempty" db:"manager_id"`
	BaseSalary        *float64     `json:"base_salary,omitempty" db:"base_salary"`
	SalaryCurrencyID  *uuid.UUID   `json:"salary_currency_id,omitempty" db:"salary_currency_id"`
	PayFrequency      *string      `json:"pay_frequency,omitempty" db:"pay_frequency"`
	Status            string       `json:"status" db:"status"`
	Permission        *string      `json:"permission,omitempty" db:"permission"`
	Notes             *string      `json:"notes,omitempty" db:"notes"`
	CreatedAt         time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt         sql.NullTime `json:"-" db:"deleted_at"`

	// Computed fields (not stored in DB)
	FullName         string  `json:"full_name,omitempty"`
	PerformanceScore float64 `json:"performance_score,omitempty"`
	TurnoverRisk     string  `json:"turnover_risk,omitempty"`
	Department       string  `json:"department,omitempty"`
	JobPositionName  string  `json:"job_position_name,omitempty"`
}

// GetFullName returns the employee's full name
func (e *Employee) GetFullName() string {
	if e.MiddleName != nil && *e.MiddleName != "" {
		return e.FirstName + " " + *e.MiddleName + " " + e.LastName
	}
	return e.FirstName + " " + e.LastName
}

// CreateEmployeeInput represents input for creating an employee
type CreateEmployeeInput struct {
	EmployeeNumber   string  `json:"employee_number"`
	FirstName        string  `json:"first_name"`
	LastName         string  `json:"last_name"`
	MiddleName       string  `json:"middle_name,omitempty"`
	FullName         string  `json:"full_name,omitempty"` // Alternative: full_name splits into first/last
	Email            string  `json:"email,omitempty"`
	Phone            string  `json:"phone,omitempty"`
	Mobile           string  `json:"mobile,omitempty"`
	DateOfBirth      string  `json:"date_of_birth,omitempty"`
	Gender           string  `json:"gender,omitempty"`
	EmploymentType   string  `json:"employment_type,omitempty"`
	JobTitle         string  `json:"job_title,omitempty"`
	JobPositionID    string  `json:"job_position_id,omitempty"`
	HireDate         string  `json:"hire_date,omitempty"`
	BaseSalary       float64 `json:"salary,omitempty"`
	Status           string  `json:"status,omitempty"`
	Permission       string  `json:"permission,omitempty"`
	Department       string  `json:"department,omitempty"`
	DepartmentID     string  `json:"department_id,omitempty"`
	PerformanceScore float64 `json:"performance_score,omitempty"`
	TurnoverRisk     string  `json:"turnover_risk,omitempty"`
	Notes            string  `json:"notes,omitempty"`
}

// UpdateEmployeeInput represents input for updating an employee
type UpdateEmployeeInput struct {
	FirstName         *string  `json:"first_name,omitempty"`
	LastName          *string  `json:"last_name,omitempty"`
	MiddleName        *string  `json:"middle_name,omitempty"`
	FullName          *string  `json:"full_name,omitempty"`
	Email             *string  `json:"email,omitempty"`
	Phone             *string  `json:"phone,omitempty"`
	Mobile            *string  `json:"mobile,omitempty"`
	DateOfBirth       *string  `json:"date_of_birth,omitempty"`
	Gender            *string  `json:"gender,omitempty"`
	EmploymentType    *string  `json:"employment_type,omitempty"`
	JobTitle          *string  `json:"job_title,omitempty"`
	JobPositionID     *string  `json:"job_position_id,omitempty"`
	HireDate          *string  `json:"hire_date,omitempty"`
	BaseSalary        *float64 `json:"salary,omitempty"`
	Status            *string  `json:"status,omitempty"`
	Permission        *string  `json:"permission,omitempty"`
	Department        *string  `json:"department,omitempty"`
	DepartmentID      *string  `json:"department_id,omitempty"`
	PerformanceScore  *float64 `json:"performance_score,omitempty"`
	TurnoverRisk      *string  `json:"turnover_risk,omitempty"`
	Notes             *string  `json:"notes,omitempty"`
	TerminationDate   *string  `json:"termination_date,omitempty"`
	TerminationReason *string  `json:"termination_reason,omitempty"`
}

// EmployeeListFilter represents filters for listing employees
type EmployeeListFilter struct {
	Search         string `form:"search"`
	Status         string `form:"status"`
	Department     string `form:"department"`
	EmploymentType string `form:"employment_type"`
}

// EmployeeResponse represents the API response for an employee
type EmployeeResponse struct {
	ID               uuid.UUID `json:"id"`
	UserID           string    `json:"user_id,omitempty"`
	EmployeeNumber   string    `json:"employee_number"`
	FullName         string    `json:"full_name"`
	FirstName        string    `json:"first_name"`
	LastName         string    `json:"last_name"`
	Email            string    `json:"email,omitempty"`
	Phone            string    `json:"phone,omitempty"`
	JobTitle         string    `json:"job_title,omitempty"`
	JobPositionID    string    `json:"job_position_id,omitempty"`
	JobPositionName  string    `json:"job_position_name,omitempty"`
	DepartmentID     string    `json:"department_id,omitempty"`
	Department       string    `json:"department,omitempty"`
	HireDate         string    `json:"hire_date"`
	Status           string    `json:"status"`
	Permission       string    `json:"permission,omitempty"`
	BaseSalary       float64   `json:"salary,omitempty"`
	PerformanceScore float64   `json:"performance_score"`
	TurnoverRisk     string    `json:"turnover_risk"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ToResponse converts Employee to EmployeeResponse
func (e *Employee) ToResponse() *EmployeeResponse {
	resp := &EmployeeResponse{
		ID:               e.ID,
		EmployeeNumber:   e.EmployeeNumber,
		FullName:         e.GetFullName(),
		FirstName:        e.FirstName,
		LastName:         e.LastName,
		HireDate:         e.HireDate.Format("2006-01-02"),
		Status:           e.Status,
		PerformanceScore: e.PerformanceScore,
		TurnoverRisk:     e.TurnoverRisk,
		Department:       e.Department,
		JobPositionName:  e.JobPositionName,
		JobPositionID: func() string {
			if e.JobPositionID != nil {
				return e.JobPositionID.String()
			}
			return ""
		}(),
		DepartmentID: func() string {
			if e.DepartmentID != nil {
				return e.DepartmentID.String()
			}
			return ""
		}(),
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}

	if e.UserID != nil {
		resp.UserID = e.UserID.String()
	}
	if e.Email != nil {
		resp.Email = *e.Email
	}
	if e.Phone != nil {
		resp.Phone = *e.Phone
	}
	if e.JobTitle != nil {
		resp.JobTitle = *e.JobTitle
	}
	if e.BaseSalary != nil {
		resp.BaseSalary = *e.BaseSalary
	}
	if e.Permission != nil {
		resp.Permission = *e.Permission
	}

	return resp
}

// EmployeeListResponse represents paginated employee list response
type EmployeeListResponse struct {
	Data       []*EmployeeResponse `json:"data"`
	Pagination *Pagination         `json:"pagination"`
}
