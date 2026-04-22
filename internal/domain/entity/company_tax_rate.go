package entity

import (
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// CompanyTaxRate — catalog of activity-level tax rates (per tenant).
// Separate from EmployeeTax because these taxes aren't applied per payroll
// entry (they're applied to sales/profit/turnover/dividends) and have
// different semantics (no `payer`/`base_type`, different default accounts).
// Admin-maintained from Settings → Finance → Company Tax Rates.
// Migration 340.
// ─────────────────────────────────────────────────────────────────────────────

type CompanyTaxRate struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`

	Code        string `json:"code" db:"code"`
	Name        string `json:"name" db:"name"`
	Description string `json:"description,omitempty" db:"description"`

	// Percent (e.g., 12 means 12%).
	Rate float64 `json:"rate" db:"rate"`

	// 'sales' | 'profit' | 'turnover' | 'dividend' | 'other'
	// See migration 340 for the CHECK constraint.
	AppliesTo string `json:"applies_to" db:"applies_to"`

	// Liability account credited when the tax is posted. NULL until admin pins it.
	AccountID *uuid.UUID `json:"account_id,omitempty" db:"account_id"`

	IsActive  bool `json:"is_active" db:"is_active"`
	SortOrder int  `json:"sort_order" db:"sort_order"`

	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"-" db:"deleted_at"`

	// Enrichment (ListCompanyTaxRates): display name + code of the linked account.
	AccountCode *string `json:"account_code,omitempty" db:"-"`
	AccountName *string `json:"account_name,omitempty" db:"-"`
}

// CreateCompanyTaxRateInput is the payload for POST /company-tax-rates
type CreateCompanyTaxRateInput struct {
	Code        string     `json:"code" binding:"required"`
	Name        string     `json:"name" binding:"required"`
	Description string     `json:"description,omitempty"`
	Rate        float64    `json:"rate" binding:"required"`
	AppliesTo   string     `json:"applies_to" binding:"required"` // sales|profit|turnover|dividend|other
	AccountID   *uuid.UUID `json:"account_id,omitempty"`
	IsActive    *bool      `json:"is_active,omitempty"`
	SortOrder   *int       `json:"sort_order,omitempty"`
}

// UpdateCompanyTaxRateInput is the payload for PUT /company-tax-rates/:id.
// All fields optional — only provided fields are updated.
type UpdateCompanyTaxRateInput struct {
	Code         *string    `json:"code,omitempty"`
	Name         *string    `json:"name,omitempty"`
	Description  *string    `json:"description,omitempty"`
	Rate         *float64   `json:"rate,omitempty"`
	AppliesTo    *string    `json:"applies_to,omitempty"`
	AccountID    *uuid.UUID `json:"account_id,omitempty"`
	ClearAccount bool       `json:"clear_account,omitempty"`
	IsActive     *bool      `json:"is_active,omitempty"`
	SortOrder    *int       `json:"sort_order,omitempty"`
}
