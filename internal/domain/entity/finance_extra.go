package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// ============================================
// CASH REGISTER (Kassa)
// ============================================

type CashRegister struct {
	ID             uuid.UUID    `json:"id" db:"id"`
	TenantID       uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	Name           string       `json:"name" db:"name"`
	Code           string       `json:"code" db:"code"`
	Currency       string       `json:"currency" db:"currency"`
	LimitAmount    float64      `json:"limit_amount" db:"limit_amount"`
	CurrentBalance float64      `json:"current_balance" db:"current_balance"`
	IsActive       bool         `json:"is_active" db:"is_active"`
	CreatedBy      *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt      time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt      sql.NullTime `json:"-" db:"deleted_at"`
}

type CreateCashRegisterInput struct {
	Name        string  `json:"name" binding:"required,min=1,max=255"`
	Code        string  `json:"code" binding:"required,min=1,max=50"`
	Currency    string  `json:"currency" binding:"required"`
	LimitAmount float64 `json:"limit_amount"`
}

// ============================================
// CASH ORDER (PKO / RKO)
// ============================================

type CashOrder struct {
	ID             uuid.UUID    `json:"id" db:"id"`
	TenantID       uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	CashRegisterID uuid.UUID    `json:"cash_register_id" db:"cash_register_id"`
	OrderNumber    string       `json:"order_number" db:"order_number"`
	OrderType      string       `json:"order_type" db:"order_type"` // pko, rko
	OrderDate      string       `json:"order_date" db:"order_date"`
	Amount         float64      `json:"amount" db:"amount"`
	Currency       string       `json:"currency" db:"currency"`
	PartnerID      *uuid.UUID   `json:"partner_id,omitempty" db:"partner_id"`
	PartnerName    string       `json:"partner_name,omitempty" db:"-"`
	AccountID      *uuid.UUID   `json:"account_id,omitempty" db:"account_id"`
	AccountCode    string       `json:"account_code,omitempty" db:"-"`
	Description    string       `json:"description" db:"description"`
	CashierID      *uuid.UUID   `json:"cashier_id,omitempty" db:"cashier_id"`
	JournalEntryID *uuid.UUID   `json:"journal_entry_id,omitempty" db:"journal_entry_id"`
	Status         string       `json:"status" db:"status"`
	CreatedBy      *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt      time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt      sql.NullTime `json:"-" db:"deleted_at"`
}

type CreateCashOrderInput struct {
	CashRegisterID string  `json:"cash_register_id" binding:"required"`
	OrderType      string  `json:"order_type" binding:"required,oneof=pko rko"`
	OrderDate      string  `json:"order_date" binding:"required"`
	Amount         float64 `json:"amount" binding:"required,gt=0"`
	Currency       string  `json:"currency"`
	PartnerID      string  `json:"partner_id"`
	AccountID      string  `json:"account_id"`
	Description    string  `json:"description" binding:"required"`
	CashierID      string  `json:"cashier_id"`
}

// ============================================
// CASH BOOK ENTRY (Kassa kitob)
// ============================================

type CashBookEntry struct {
	ID             uuid.UUID `json:"id" db:"id"`
	TenantID       uuid.UUID `json:"tenant_id" db:"tenant_id"`
	CashRegisterID uuid.UUID `json:"cash_register_id" db:"cash_register_id"`
	EntryDate      string    `json:"entry_date" db:"entry_date"`
	OpeningBalance float64   `json:"opening_balance" db:"opening_balance"`
	TotalIncome    float64   `json:"total_income" db:"total_income"`
	TotalExpense   float64   `json:"total_expense" db:"total_expense"`
	ClosingBalance float64   `json:"closing_balance" db:"closing_balance"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// ============================================
// EXCHANGE RATE DIFFERENCE (Kurs farqi)
// ============================================

type ExchangeDiff struct {
	ID             uuid.UUID    `json:"id" db:"id"`
	TenantID       uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	CurrencyID     uuid.UUID    `json:"currency_id" db:"currency_id"`
	CurrencyCode   string       `json:"currency_code,omitempty" db:"-"`
	AmountUZS      float64      `json:"amount_uzs" db:"amount_uzs"`
	DiffType       string       `json:"diff_type" db:"diff_type"` // positive, negative
	PeriodStart    string       `json:"period_start" db:"period_start"`
	PeriodEnd      string       `json:"period_end" db:"period_end"`
	JournalEntryID *uuid.UUID   `json:"journal_entry_id,omitempty" db:"journal_entry_id"`
	Description    string       `json:"description,omitempty" db:"description"`
	CreatedBy      *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt      time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt      sql.NullTime `json:"-" db:"deleted_at"`
}

type RevalueInput struct {
	PeriodEnd string `json:"period_end" binding:"required"`
}

type SyncRatesInput struct {
	Date string `json:"date"`
}

// ============================================
// RECONCILIATION ACT (Akt sverka)
// ============================================

type ReconciliationAct struct {
	ID                uuid.UUID    `json:"id" db:"id"`
	TenantID          uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID    *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	PartnerID         uuid.UUID    `json:"partner_id" db:"partner_id"`
	PartnerName       string       `json:"partner_name,omitempty" db:"-"`
	PeriodStart       string       `json:"period_start" db:"period_start"`
	PeriodEnd         string       `json:"period_end" db:"period_end"`
	OpeningBalance    float64      `json:"opening_balance" db:"opening_balance"`
	OurDebitTotal     float64      `json:"our_debit_total" db:"our_debit_total"`
	OurCreditTotal    float64      `json:"our_credit_total" db:"our_credit_total"`
	OurBalance        float64      `json:"our_balance" db:"our_balance"`
	PartnerDebitTotal float64      `json:"partner_debit_total" db:"partner_debit_total"`
	PartnerCreditTotal float64     `json:"partner_credit_total" db:"partner_credit_total"`
	PartnerBalance    float64      `json:"partner_balance" db:"partner_balance"`
	Difference        float64      `json:"difference" db:"difference"`
	Status            string       `json:"status" db:"status"`
	Notes             string       `json:"notes,omitempty" db:"notes"`
	ConfirmedAt       *time.Time   `json:"confirmed_at,omitempty" db:"confirmed_at"`
	ConfirmedBy       *uuid.UUID   `json:"confirmed_by,omitempty" db:"confirmed_by"`
	CreatedBy         *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt         time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt         sql.NullTime `json:"-" db:"deleted_at"`
	Lines             []ReconciliationLine `json:"lines,omitempty" db:"-"`
}

type ReconciliationLine struct {
	ID            uuid.UUID `json:"id" db:"id"`
	ActID         uuid.UUID `json:"act_id" db:"act_id"`
	LineDate      string    `json:"line_date" db:"line_date"`
	Document      string    `json:"document" db:"document"`
	Description   string    `json:"description,omitempty" db:"description"`
	OurDebit      float64   `json:"our_debit" db:"our_debit"`
	OurCredit     float64   `json:"our_credit" db:"our_credit"`
	PartnerDebit  float64   `json:"partner_debit" db:"partner_debit"`
	PartnerCredit float64   `json:"partner_credit" db:"partner_credit"`
	LineNumber    int       `json:"line_number" db:"line_number"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

type CreateReconciliationActInput struct {
	PartnerID   string `json:"partner_id" binding:"required"`
	PeriodStart string `json:"period_start" binding:"required"`
	PeriodEnd   string `json:"period_end" binding:"required"`
	Notes       string `json:"notes"`
}

type BulkGenerateReconciliationInput struct {
	PeriodStart string `json:"period_start" binding:"required"`
	PeriodEnd   string `json:"period_end" binding:"required"`
}
