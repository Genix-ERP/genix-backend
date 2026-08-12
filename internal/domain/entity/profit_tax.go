package entity

import (
	"time"

	"github.com/google/uuid"
)

// ProfitTaxCalc is a persisted snapshot of a profit-tax calculation for a
// given period. See migration 337 and §6 of ТЗ_Ish_Haqi_Soliq_Tolik.docx.
//
// The live GET /profit-tax endpoint re-computes the numbers from the
// current expenses table on every read; this struct is what POST
// /profit-tax/snapshot writes to freeze a period for reporting.
type ProfitTaxCalc struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`

	PeriodType  string    `json:"period_type" db:"period_type"`   // month|quarter|year
	PeriodKey   string    `json:"period_key" db:"period_key"`     // '2026-04' | '2026-Q2' | '2026'
	PeriodStart time.Time `json:"period_start" db:"period_start"` // inclusive
	PeriodEnd   time.Time `json:"period_end" db:"period_end"`     // inclusive

	Income          float64 `json:"income" db:"income"`
	RecognizedExp   float64 `json:"recognized_exp" db:"recognized_exp"`
	UnrecognizedExp float64 `json:"unrecognized_exp" db:"unrecognized_exp"`

	AccountingProfit float64 `json:"accounting_profit" db:"accounting_profit"`
	TaxBase          float64 `json:"tax_base" db:"tax_base"`
	TaxAmount        float64 `json:"tax_amount" db:"tax_amount"`
	NetProfit        float64 `json:"net_profit" db:"net_profit"`

	RateSnapshot float64 `json:"rate_snapshot" db:"rate_snapshot"` // percent, e.g. 15.00

	Notes     *string    `json:"notes,omitempty" db:"notes"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
}

// ProfitTaxCalcResult is the live-computed shape returned by GET
// /profit-tax?period_type=…&period_key=…  It isn't persisted; it shares
// the same numeric fields as ProfitTaxCalc so the frontend can render a
// single set of cards regardless of whether the period has been snapshotted.
type ProfitTaxCalcResult struct {
	PeriodType  string `json:"period_type"`
	PeriodKey   string `json:"period_key"`
	PeriodStart string `json:"period_start"` // yyyy-mm-dd
	PeriodEnd   string `json:"period_end"`   // yyyy-mm-dd

	Income          float64 `json:"income"`
	RecognizedExp   float64 `json:"recognized_exp"`
	UnrecognizedExp float64 `json:"unrecognized_exp"`
	TotalExpenses   float64 `json:"total_expenses"` // recognized + unrecognized

	AccountingProfit float64 `json:"accounting_profit"` // income − (recognized + unrecognized)
	TaxBase          float64 `json:"tax_base"`          // income − recognized  ← key difference
	TaxAmount        float64 `json:"tax_amount"`        // tax_base × rate/100
	NetProfit        float64 `json:"net_profit"`        // accounting_profit − tax_amount

	Rate float64 `json:"rate"` // percent currently in effect (default 15)

	// Set when a snapshot exists for this period — frontend can show a
	// "closed" badge and disable recalculation.
	Snapshotted bool       `json:"snapshotted"`
	SnapshotID  *uuid.UUID `json:"snapshot_id,omitempty"`
}
