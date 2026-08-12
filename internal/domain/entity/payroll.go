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
	// TT §2.1 immutability: job title snapshot at entry-creation time
	PositionSnapshot string  `json:"position_snapshot" db:"position_snapshot"`
	BaseSalary       float64 `json:"base_salary" db:"base_salary"`
	OvertimeHours    float64 `json:"overtime_hours" db:"overtime_hours"`
	OvertimeAmount   float64 `json:"overtime_amount" db:"overtime_amount"`
	Bonus            float64 `json:"bonus" db:"bonus"`
	Allowances       float64 `json:"allowances" db:"allowances"`
	GrossSalary      float64 `json:"gross_salary" db:"gross_salary"`
	IncomeTax        float64 `json:"income_tax" db:"income_tax"`
	SocialSecurity   float64 `json:"social_security" db:"social_security"`
	Pension          float64 `json:"pension" db:"pension"`
	OtherDeductions  float64 `json:"other_deductions" db:"other_deductions"`
	TotalDeductions  float64 `json:"total_deductions" db:"total_deductions"`
	NetSalary        float64 `json:"net_salary" db:"net_salary"`
	// TT §2.3.2 / §2.4: simple advance + remainder model with day-of-month tracking.
	// Coexists with the richer gross/tax/net fields above.
	AdvanceAmount      float64   `json:"advance_amount" db:"advance_amount"`
	RemainderAmount    float64   `json:"remainder_amount" db:"remainder_amount"`
	AdvancePaid        bool      `json:"advance_paid" db:"advance_paid"`
	AdvancePaidDay     *int      `json:"advance_paid_day,omitempty" db:"advance_paid_day"`
	RemainderPaid      bool      `json:"remainder_paid" db:"remainder_paid"`
	RemainderPaidDay   *int      `json:"remainder_paid_day,omitempty" db:"remainder_paid_day"`
	AdvancePercentUsed float64   `json:"advance_percent_used" db:"advance_percent_used"`
	PaymentMethod      string    `json:"payment_method" db:"payment_method"`
	BankAccount        *string   `json:"bank_account,omitempty" db:"bank_account"`
	Status             string    `json:"status" db:"status"`
	Notes              *string   `json:"notes,omitempty" db:"notes"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at" db:"updated_at"`
}

// PayrollSettings — TT §2.2 global payroll configuration.
type PayrollSettings struct {
	TenantID       uuid.UUID `json:"tenant_id" db:"tenant_id"`
	AdvancePercent float64   `json:"advance_percent" db:"advance_percent"`
	Currency       string    `json:"currency" db:"currency"`
	CompanyName    string    `json:"company_name" db:"company_name"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// UpdatePayrollSettingsInput — API input for PUT /payroll/settings.
type UpdatePayrollSettingsInput struct {
	AdvancePercent *float64 `json:"advance_percent" binding:"omitempty,gte=0,lte=100"`
	Currency       *string  `json:"currency"`
	CompanyName    *string  `json:"company_name"`
}

// MarkPaidInput — API input for mark-advance-paid / mark-remainder-paid.
// If Day is nil the server uses the current day-of-month (clamped to the
// period's month length). If Paid is false the Day is cleared.
type MarkPaidInput struct {
	Paid bool `json:"paid"`
	Day  *int `json:"day"` // 1..31, optional
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
	// Optional per-entry override; when 0 or absent the server uses
	// payroll_settings.advance_percent (TT §2.2).
	AdvancePercent float64 `json:"advance_percent"`
	PaymentMethod  string  `json:"payment_method"`
	BankAccount    string  `json:"bank_account,omitempty"`
	Notes          string  `json:"notes,omitempty"`

	// Employee-tax system (migration 330). When the tenant has active employee_taxes
	// configured, these drive income_tax / social_security / pension. Fields above
	// that are labelled as individual taxes are ignored in that mode.
	// ExcludedTaxIDs allows the user to opt out of specific configured taxes per entry
	// (clicking the X next to a tax in the create-payroll modal).
	ExcludedTaxIDs []uuid.UUID `json:"excluded_tax_ids,omitempty"`
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

	// Opt-out list — same semantics as CreatePayrollEntryInput.ExcludedTaxIDs.
	// When nil, the existing tax selection for this entry is preserved.
	// When set (even to an empty slice), the existing payroll_entry_taxes rows are
	// rebuilt from the active catalog minus the IDs in this list.
	ExcludedTaxIDs *[]uuid.UUID `json:"excluded_tax_ids,omitempty"`
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

	// First-entry TT payment state, populated only for single-employee
	// periods so the period list can drive the advance/remainder toggles
	// (they operate on payroll_entries, not periods).
	FirstEntryID     *uuid.UUID `json:"first_entry_id,omitempty"`
	AdvancePaid      *bool      `json:"advance_paid,omitempty"`
	AdvancePaidDay   *int       `json:"advance_paid_day,omitempty"`
	RemainderPaid    *bool      `json:"remainder_paid,omitempty"`
	RemainderPaidDay *int       `json:"remainder_paid_day,omitempty"`
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
	ID               uuid.UUID `json:"id"`
	PayrollPeriodID  uuid.UUID `json:"payroll_period_id"`
	EmployeeID       uuid.UUID `json:"employee_id"`
	EmployeeName     string    `json:"employee_name"`
	PositionSnapshot string    `json:"position_snapshot"`
	BaseSalary       float64   `json:"base_salary"`
	OvertimeHours    float64   `json:"overtime_hours"`
	OvertimeAmount   float64   `json:"overtime_amount"`
	Bonus            float64   `json:"bonus"`
	Allowances       float64   `json:"allowances"`
	GrossSalary      float64   `json:"gross_salary"`
	IncomeTax        float64   `json:"income_tax"`
	SocialSecurity   float64   `json:"social_security"`
	Pension          float64   `json:"pension"`
	OtherDeductions  float64   `json:"other_deductions"`
	TotalDeductions  float64   `json:"total_deductions"`
	NetSalary        float64   `json:"net_salary"`
	// TT advance/remainder + day-of-month tracking
	AdvanceAmount      float64   `json:"advance_amount"`
	RemainderAmount    float64   `json:"remainder_amount"`
	AdvancePaid        bool      `json:"advance_paid"`
	AdvancePaidDay     *int      `json:"advance_paid_day,omitempty"`
	RemainderPaid      bool      `json:"remainder_paid"`
	RemainderPaidDay   *int      `json:"remainder_paid_day,omitempty"`
	AdvancePercentUsed float64   `json:"advance_percent_used"`
	PaymentMethod      string    `json:"payment_method"`
	BankAccount        string    `json:"bank_account,omitempty"`
	Status             string    `json:"status"`
	Notes              string    `json:"notes,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// ToResponse converts PayrollEntry to PayrollEntryResponse
func (e *PayrollEntry) ToResponse() *PayrollEntryResponse {
	resp := &PayrollEntryResponse{
		ID:                 e.ID,
		PayrollPeriodID:    e.PayrollPeriodID,
		EmployeeID:         e.EmployeeID,
		EmployeeName:       e.EmployeeName,
		PositionSnapshot:   e.PositionSnapshot,
		BaseSalary:         e.BaseSalary,
		OvertimeHours:      e.OvertimeHours,
		OvertimeAmount:     e.OvertimeAmount,
		Bonus:              e.Bonus,
		Allowances:         e.Allowances,
		GrossSalary:        e.GrossSalary,
		IncomeTax:          e.IncomeTax,
		SocialSecurity:     e.SocialSecurity,
		Pension:            e.Pension,
		OtherDeductions:    e.OtherDeductions,
		TotalDeductions:    e.TotalDeductions,
		NetSalary:          e.NetSalary,
		AdvanceAmount:      e.AdvanceAmount,
		RemainderAmount:    e.RemainderAmount,
		AdvancePaid:        e.AdvancePaid,
		AdvancePaidDay:     e.AdvancePaidDay,
		RemainderPaid:      e.RemainderPaid,
		RemainderPaidDay:   e.RemainderPaidDay,
		AdvancePercentUsed: e.AdvancePercentUsed,
		PaymentMethod:      e.PaymentMethod,
		Status:             e.Status,
		CreatedAt:          e.CreatedAt,
	}
	if e.BankAccount != nil {
		resp.BankAccount = *e.BankAccount
	}
	if e.Notes != nil {
		resp.Notes = *e.Notes
	}
	return resp
}
