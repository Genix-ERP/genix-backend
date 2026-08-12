package entity

import (
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// EmployeeTax — catalog of configurable employee taxes (per tenant).
// Admin-maintained from Settings → Finance → Employee Taxes.
// Migration 330.
// ─────────────────────────────────────────────────────────────────────────────

type EmployeeTax struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`

	Code        string `json:"code" db:"code"`
	Name        string `json:"name" db:"name"`
	Description string `json:"description,omitempty" db:"description"`

	// Percent (e.g., 12 means 12%)
	Rate float64 `json:"rate" db:"rate"`

	// 'base_salary' | 'gross' | 'taxable'
	BaseType string `json:"base_type" db:"base_type"`

	// 'employee' | 'employer'
	Payer string `json:"payer" db:"payer"`

	// Liability account credited when the tax is posted.
	AccountID *uuid.UUID `json:"account_id,omitempty" db:"account_id"`

	// Expense account debited when payer='employer'. Ignored for employee-paid taxes.
	ExpenseAccountID *uuid.UUID `json:"expense_account_id,omitempty" db:"expense_account_id"`

	IsActive  bool `json:"is_active" db:"is_active"`
	SortOrder int  `json:"sort_order" db:"sort_order"`

	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"-" db:"deleted_at"`

	// Enrichment (filled in ListEmployeeTaxes): display name + code of the linked account.
	AccountCode        *string `json:"account_code,omitempty" db:"-"`
	AccountName        *string `json:"account_name,omitempty" db:"-"`
	ExpenseAccountCode *string `json:"expense_account_code,omitempty" db:"-"`
	ExpenseAccountName *string `json:"expense_account_name,omitempty" db:"-"`
}

// CreateEmployeeTaxInput is the payload for POST /employee-taxes
type CreateEmployeeTaxInput struct {
	Code             string     `json:"code" binding:"required"`
	Name             string     `json:"name" binding:"required"`
	Description      string     `json:"description,omitempty"`
	Rate             float64    `json:"rate" binding:"required"`
	BaseType         string     `json:"base_type"` // default 'gross'
	Payer            string     `json:"payer"`     // default 'employee'
	AccountID        *uuid.UUID `json:"account_id,omitempty"`
	ExpenseAccountID *uuid.UUID `json:"expense_account_id,omitempty"`
	IsActive         *bool      `json:"is_active,omitempty"`
	SortOrder        *int       `json:"sort_order,omitempty"`
}

// UpdateEmployeeTaxInput is the payload for PUT /employee-taxes/:id.
// All fields optional — only provided fields are updated.
type UpdateEmployeeTaxInput struct {
	Code             *string    `json:"code,omitempty"`
	Name             *string    `json:"name,omitempty"`
	Description      *string    `json:"description,omitempty"`
	Rate             *float64   `json:"rate,omitempty"`
	BaseType         *string    `json:"base_type,omitempty"`
	Payer            *string    `json:"payer,omitempty"`
	AccountID        *uuid.UUID `json:"account_id,omitempty"`
	ClearAccount     bool       `json:"clear_account,omitempty"`
	ExpenseAccountID *uuid.UUID `json:"expense_account_id,omitempty"`
	ClearExpenseAcct bool       `json:"clear_expense_account,omitempty"`
	IsActive         *bool      `json:"is_active,omitempty"`
	SortOrder        *int       `json:"sort_order,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// PayrollEntryTax — snapshot row created when a payroll entry is written.
// Immutable once the payroll period is posted.
// ─────────────────────────────────────────────────────────────────────────────

type PayrollEntryTax struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`
	PayrollEntryID uuid.UUID  `json:"payroll_entry_id" db:"payroll_entry_id"`
	TaxID          *uuid.UUID `json:"tax_id,omitempty" db:"tax_id"`

	TaxCodeSnapshot  string  `json:"tax_code" db:"tax_code_snapshot"`
	TaxNameSnapshot  string  `json:"tax_name" db:"tax_name_snapshot"`
	RateSnapshot     float64 `json:"rate" db:"rate_snapshot"`
	BaseTypeSnapshot string  `json:"base_type" db:"base_type_snapshot"`
	PayerSnapshot    string  `json:"payer" db:"payer_snapshot"`

	BaseAmount float64 `json:"base_amount" db:"base_amount"`
	Amount     float64 `json:"amount" db:"amount"`

	AccountIDSnapshot        *uuid.UUID `json:"account_id,omitempty" db:"account_id_snapshot"`
	ExpenseAccountIDSnapshot *uuid.UUID `json:"expense_account_id,omitempty" db:"expense_account_id_snapshot"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// PayrollEntryTaxInput — client tells the server how to apply taxes to one entry.
// Leave ExcludedTaxIDs empty to apply every active tax as configured by the tenant.
type PayrollEntryTaxInput struct {
	ExcludedTaxIDs []uuid.UUID `json:"excluded_tax_ids,omitempty"`
}

// EmployeeTaxSummaryRow — one row in the tax-reports → employee-taxes section.
type EmployeeTaxSummaryRow struct {
	TaxCode     string  `json:"tax_code"`
	TaxName     string  `json:"tax_name"`
	Payer       string  `json:"payer"`
	EntryCount  int     `json:"entry_count"`
	TotalBase   float64 `json:"total_base"`
	TotalAmount float64 `json:"total_amount"`
}
