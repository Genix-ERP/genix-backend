package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// PayrollPeriod represents a payroll period
type PayrollPeriod struct {
	ID              uuid.UUID    `json:"id" db:"id"`
	TenantID        uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	PeriodCode      string       `json:"period_code" db:"period_code"`
	PeriodName      string       `json:"period_name" db:"period_name"`
	StartDate       time.Time    `json:"start_date" db:"start_date"`
	EndDate         time.Time    `json:"end_date" db:"end_date"`
	PayDate         time.Time    `json:"pay_date" db:"pay_date"`
	Status          string       `json:"status" db:"status"`
	TotalGross      float64      `json:"total_gross" db:"total_gross"`
	TotalDeductions float64      `json:"total_deductions" db:"total_deductions"`
	TotalNet        float64      `json:"total_net" db:"total_net"`
	EmployeeCount   int          `json:"employee_count" db:"employee_count"`
	Notes           *string      `json:"notes,omitempty" db:"notes"`
	ApprovedBy      *uuid.UUID   `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt      *time.Time   `json:"approved_at,omitempty" db:"approved_at"`
	CreatedBy       *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime `json:"-" db:"deleted_at"`
}

// PayrollEntry represents a single employee's payroll entry
type PayrollEntry struct {
	ID              uuid.UUID `json:"id" db:"id"`
	TenantID        uuid.UUID `json:"tenant_id" db:"tenant_id"`
	PayrollPeriodID uuid.UUID `json:"payroll_period_id" db:"payroll_period_id"`
	EmployeeID      uuid.UUID `json:"employee_id" db:"employee_id"`
	EmployeeName    string    `json:"employee_name" db:"employee_name"`
	BaseSalary      float64   `json:"base_salary" db:"base_salary"`
	OvertimeHours   float64   `json:"overtime_hours" db:"overtime_hours"`
	OvertimeAmount  float64   `json:"overtime_amount" db:"overtime_amount"`
	Bonus           float64   `json:"bonus" db:"bonus"`
	Allowances      float64   `json:"allowances" db:"allowances"`
	GrossSalary     float64   `json:"gross_salary" db:"gross_salary"`
	IncomeTax       float64   `json:"income_tax" db:"income_tax"`
	SocialSecurity  float64   `json:"social_security" db:"social_security"`
	Pension         float64   `json:"pension" db:"pension"`
	OtherDeductions float64   `json:"other_deductions" db:"other_deductions"`
	TotalDeductions float64   `json:"total_deductions" db:"total_deductions"`
	NetSalary       float64   `json:"net_salary" db:"net_salary"`
	PaymentMethod   string    `json:"payment_method" db:"payment_method"`
	BankAccount     *string   `json:"bank_account,omitempty" db:"bank_account"`
	Status          string    `json:"status" db:"status"`
	Notes           *string   `json:"notes,omitempty" db:"notes"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// CreatePayrollPeriodInput represents input for creating a payroll period
type CreatePayrollPeriodInput struct {
	PeriodCode string `json:"period_code"`
	PeriodName string `json:"period_name" binding:"required"`
	StartDate  string `json:"start_date" binding:"required"`
	EndDate    string `json:"end_date" binding:"required"`
	PayDate    string `json:"pay_date" binding:"required"`
	Notes      string `json:"notes,omitempty"`
}

// UpdatePayrollPeriodInput represents input for updating a payroll period
type UpdatePayrollPeriodInput struct {
	PeriodName    *string `json:"period_name,omitempty"`
	StartDate     *string `json:"start_date,omitempty"`
	EndDate       *string `json:"end_date,omitempty"`
	PayDate       *string `json:"pay_date,omitempty"`
	Status        *string `json:"status,omitempty"`
	Notes         *string `json:"notes,omitempty"`
	PaymentMethod *string `json:"payment_method,omitempty"`
}

// CreatePayrollEntryInput represents input for creating a payroll entry
type CreatePayrollEntryInput struct {
	EmployeeID       string  `json:"employee_id" binding:"required"`
	BaseSalary       float64 `json:"base_salary"`
	OvertimeHours    float64 `json:"overtime_hours"`
	OvertimeAmount   float64 `json:"overtime_amount"`
	Bonus            float64 `json:"bonus"`
	Allowances       float64 `json:"allowances"`
	IncomeTax        float64 `json:"income_tax"`
	SocialSecurity   float64 `json:"social_security"`
	Pension          float64 `json:"pension"`
	OtherDeductions  float64 `json:"other_deductions"`
	DeductionPercent float64 `json:"deduction_percent"`
	PaymentMethod    string  `json:"payment_method"`
	BankAccount      string  `json:"bank_account,omitempty"`
	Notes            string  `json:"notes,omitempty"`
}

// UpdatePayrollEntryInput represents input for updating a payroll entry
type UpdatePayrollEntryInput struct {
	BaseSalary      *float64 `json:"base_salary,omitempty"`
	OvertimeHours   *float64 `json:"overtime_hours,omitempty"`
	OvertimeAmount  *float64 `json:"overtime_amount,omitempty"`
	Bonus           *float64 `json:"bonus,omitempty"`
	Allowances      *float64 `json:"allowances,omitempty"`
	IncomeTax       *float64 `json:"income_tax,omitempty"`
	SocialSecurity  *float64 `json:"social_security,omitempty"`
	Pension         *float64 `json:"pension,omitempty"`
	OtherDeductions *float64 `json:"other_deductions,omitempty"`
	PaymentMethod   *string  `json:"payment_method,omitempty"`
	BankAccount     *string  `json:"bank_account,omitempty"`
	Status          *string  `json:"status,omitempty"`
	Notes           *string  `json:"notes,omitempty"`
}

// PayrollPeriodResponse represents the API response for a payroll period
type PayrollPeriodResponse struct {
	ID              uuid.UUID  `json:"id"`
	PeriodCode      string     `json:"period_code"`
	PeriodName      string     `json:"period_name"`
	EmployeeID      *uuid.UUID `json:"employee_id,omitempty"`   // Populated when there's only one employee
	EmployeeName    string     `json:"employee_name,omitempty"` // Populated when there's only one employee
	StartDate       string     `json:"start_date"`
	EndDate         string     `json:"end_date"`
	PayDate         string     `json:"pay_date"`
	Status          string     `json:"status"`
	TotalGross      float64    `json:"total_gross"`
	TotalDeductions float64    `json:"total_deductions"`
	TotalNet        float64    `json:"total_net"`
	EmployeeCount   int        `json:"employee_count"`
	Notes           string     `json:"notes,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// ToResponse converts PayrollPeriod to PayrollPeriodResponse
func (p *PayrollPeriod) ToResponse() *PayrollPeriodResponse {
	resp := &PayrollPeriodResponse{
		ID:              p.ID,
		PeriodCode:      p.PeriodCode,
		PeriodName:      p.PeriodName,
		StartDate:       p.StartDate.Format("2006-01-02"),
		EndDate:         p.EndDate.Format("2006-01-02"),
		PayDate:         p.PayDate.Format("2006-01-02"),
		Status:          p.Status,
		TotalGross:      p.TotalGross,
		TotalDeductions: p.TotalDeductions,
		TotalNet:        p.TotalNet,
		EmployeeCount:   p.EmployeeCount,
		CreatedAt:       p.CreatedAt,
	}
	if p.Notes != nil {
		resp.Notes = *p.Notes
	}
	return resp
}

// PayrollEntryResponse represents the API response for a payroll entry
type PayrollEntryResponse struct {
	ID              uuid.UUID `json:"id"`
	PayrollPeriodID uuid.UUID `json:"payroll_period_id"`
	EmployeeID      uuid.UUID `json:"employee_id"`
	EmployeeName    string    `json:"employee_name"`
	BaseSalary      float64   `json:"base_salary"`
	OvertimeHours   float64   `json:"overtime_hours"`
	OvertimeAmount  float64   `json:"overtime_amount"`
	Bonus           float64   `json:"bonus"`
	Allowances      float64   `json:"allowances"`
	GrossSalary     float64   `json:"gross_salary"`
	IncomeTax       float64   `json:"income_tax"`
	SocialSecurity  float64   `json:"social_security"`
	Pension         float64   `json:"pension"`
	OtherDeductions float64   `json:"other_deductions"`
	TotalDeductions float64   `json:"total_deductions"`
	NetSalary       float64   `json:"net_salary"`
	PaymentMethod   string    `json:"payment_method"`
	BankAccount     string    `json:"bank_account,omitempty"`
	Status          string    `json:"status"`
	Notes           string    `json:"notes,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// ToResponse converts PayrollEntry to PayrollEntryResponse
func (e *PayrollEntry) ToResponse() *PayrollEntryResponse {
	resp := &PayrollEntryResponse{
		ID:              e.ID,
		PayrollPeriodID: e.PayrollPeriodID,
		EmployeeID:      e.EmployeeID,
		EmployeeName:    e.EmployeeName,
		BaseSalary:      e.BaseSalary,
		OvertimeHours:   e.OvertimeHours,
		OvertimeAmount:  e.OvertimeAmount,
		Bonus:           e.Bonus,
		Allowances:      e.Allowances,
		GrossSalary:     e.GrossSalary,
		IncomeTax:       e.IncomeTax,
		SocialSecurity:  e.SocialSecurity,
		Pension:         e.Pension,
		OtherDeductions: e.OtherDeductions,
		TotalDeductions: e.TotalDeductions,
		NetSalary:       e.NetSalary,
		PaymentMethod:   e.PaymentMethod,
		Status:          e.Status,
		CreatedAt:       e.CreatedAt,
	}
	if e.BankAccount != nil {
		resp.BankAccount = *e.BankAccount
	}
	if e.Notes != nil {
		resp.Notes = *e.Notes
	}
	return resp
}
