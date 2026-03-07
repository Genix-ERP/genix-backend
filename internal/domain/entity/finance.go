package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// =====================================================
// ACCOUNT TYPES
// =====================================================

// AccountType represents account classification
type AccountType struct {
	ID            uuid.UUID `json:"id" db:"id"`
	Code          string    `json:"code" db:"code"`
	Name          string    `json:"name" db:"name"`
	Category      string    `json:"category" db:"category"`             // asset, liability, equity, revenue, expense
	NormalBalance string    `json:"normal_balance" db:"normal_balance"` // debit, credit
	IsSystem      bool      `json:"is_system" db:"is_system"`
	DisplayOrder  int       `json:"display_order" db:"display_order"`
}

// =====================================================
// ACCOUNTS (CHART OF ACCOUNTS)
// =====================================================

// Account represents a chart of accounts entry
type Account struct {
	ID               uuid.UUID    `json:"id" db:"id"`
	TenantID         uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID   *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	ParentID         *uuid.UUID   `json:"parent_id,omitempty" db:"parent_id"`
	AccountTypeID    uuid.UUID    `json:"account_type_id" db:"account_type_id"`
	Code             string       `json:"code" db:"code"`
	Name             string       `json:"name" db:"name"`
	NameUz           *string      `json:"name_uz,omitempty" db:"name_uz"`
	NameEn           *string      `json:"name_en,omitempty" db:"name_en"`
	Description      *string      `json:"description,omitempty" db:"description"`
	CurrencyID       *uuid.UUID   `json:"currency_id,omitempty" db:"currency_id"`
	IsBankAccount    bool         `json:"is_bank_account" db:"is_bank_account"`
	BankDetails      *string      `json:"bank_details,omitempty" db:"bank_details"` // JSON
	IsControlAccount bool         `json:"is_control_account" db:"is_control_account"`
	IsReconcilable   bool         `json:"is_reconcilable" db:"is_reconcilable"`
	BudgetTracking   bool         `json:"budget_tracking" db:"budget_tracking"`
	CurrentBalance   float64      `json:"current_balance" db:"current_balance"`
	OpeningBalance   float64      `json:"opening_balance" db:"opening_balance"`
	IsActive         bool         `json:"is_active" db:"is_active"`
	CreatedAt        time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt        sql.NullTime `json:"-" db:"deleted_at"`

	// Relationships (loaded separately)
	AccountType *AccountType `json:"account_type,omitempty"`
	Children    []Account    `json:"children,omitempty"`
	Parent      *Account     `json:"parent,omitempty"`
}

// AccountResponse is the API response format for Account
type AccountResponse struct {
	ID               uuid.UUID        `json:"id"`
	ParentID         *uuid.UUID       `json:"parent_id,omitempty"`
	AccountTypeID    uuid.UUID        `json:"account_type_id"`
	Code             string           `json:"code"`
	Name             string           `json:"name"`
	NameUz           *string          `json:"name_uz,omitempty"`
	NameEn           *string          `json:"name_en,omitempty"`
	Description      *string          `json:"description,omitempty"`
	Category         string           `json:"category,omitempty"`
	NormalBalance    string           `json:"normal_balance,omitempty"`
	IsBankAccount    bool             `json:"is_bank_account"`
	IsControlAccount bool             `json:"is_control_account"`
	IsReconcilable   bool             `json:"is_reconcilable"`
	BudgetTracking   bool             `json:"budget_tracking"`
	CurrentBalance   float64          `json:"current_balance"`
	OpeningBalance   float64          `json:"opening_balance"`
	IsActive         bool             `json:"is_active"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	AccountType      *AccountType     `json:"account_type,omitempty"`
	Children         []AccountResponse `json:"children,omitempty"`
}

// ToResponse converts Account to AccountResponse
func (a *Account) ToResponse() *AccountResponse {
	resp := &AccountResponse{
		ID:               a.ID,
		ParentID:         a.ParentID,
		AccountTypeID:    a.AccountTypeID,
		Code:             a.Code,
		Name:             a.Name,
		NameUz:           a.NameUz,
		NameEn:           a.NameEn,
		Description:      a.Description,
		IsBankAccount:    a.IsBankAccount,
		IsControlAccount: a.IsControlAccount,
		IsReconcilable:   a.IsReconcilable,
		BudgetTracking:   a.BudgetTracking,
		CurrentBalance:   a.CurrentBalance,
		OpeningBalance:   a.OpeningBalance,
		IsActive:         a.IsActive,
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
		AccountType:      a.AccountType,
	}
	if a.AccountType != nil {
		resp.Category = a.AccountType.Category
		resp.NormalBalance = a.AccountType.NormalBalance
	}
	return resp
}

// CreateAccountInput is the input for creating an account
type CreateAccountInput struct {
	ParentID         *string `json:"parent_id"`
	AccountTypeID    string  `json:"account_type_id" binding:"required"`
	Code             string  `json:"code" binding:"required,min=1,max=50"`
	Name             string  `json:"name" binding:"required,min=1,max=255"`
	Description      string  `json:"description"`
	CurrencyID       *string `json:"currency_id"`
	IsBankAccount    bool    `json:"is_bank_account"`
	IsReconcilable   bool    `json:"is_reconcilable"`
	IsControlAccount bool    `json:"is_control_account"`
	BudgetTracking   bool    `json:"budget_tracking"`
	OpeningBalance   float64 `json:"opening_balance"`
}

// UpdateAccountInput is the input for updating an account
type UpdateAccountInput struct {
	ParentID         *string  `json:"parent_id"`
	Name             *string  `json:"name"`
	Description      *string  `json:"description"`
	IsBankAccount    *bool    `json:"is_bank_account"`
	IsReconcilable   *bool    `json:"is_reconcilable"`
	IsControlAccount *bool    `json:"is_control_account"`
	BudgetTracking   *bool    `json:"budget_tracking"`
	IsActive         *bool    `json:"is_active"`
}

// AccountListFilter is the filter for listing accounts
type AccountListFilter struct {
	Search          string `form:"search"`
	Category        string `form:"category"` // asset, liability, equity, revenue, expense
	AccountTypeID   string `form:"account_type_id"`
	ParentID        string `form:"parent_id"`
	IsBankAccount   *bool  `form:"is_bank_account"`
	IsActive        *bool  `form:"is_active"`
	IncludeInactive bool   `form:"include_inactive"`
	Flat            bool   `form:"flat"` // Return flat list vs tree structure
}

// =====================================================
// JOURNAL ENTRIES
// =====================================================

// Journal represents a journal type
type Journal struct {
	ID                     uuid.UUID  `json:"id" db:"id"`
	TenantID               uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code                   string     `json:"code" db:"code"`
	Name                   string     `json:"name" db:"name"`
	Type                   string     `json:"type" db:"type"` // general, sales, purchase, cash, bank, miscellaneous
	Description            *string    `json:"description,omitempty" db:"description"`
	AutoSequence           bool       `json:"auto_sequence" db:"auto_sequence"`
	NextNumber             int        `json:"next_number" db:"next_number"`
	NumberPrefix           *string    `json:"number_prefix,omitempty" db:"number_prefix"`
	ShortCode              string     `json:"short_code" db:"short_code"`
	Currency               string     `json:"currency" db:"currency"`
	BankAccountID          *uuid.UUID `json:"bank_account_id,omitempty" db:"bank_account_id"`
	SuspenseAccountID      *uuid.UUID `json:"suspense_account_id,omitempty" db:"suspense_account_id"`
	ProfitAccountID        *uuid.UUID `json:"profit_account_id,omitempty" db:"profit_account_id"`
	LossAccountID          *uuid.UUID `json:"loss_account_id,omitempty" db:"loss_account_id"`
	OrganizationID         *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`
	DefaultDebitAccountID  *uuid.UUID `json:"default_debit_account_id,omitempty" db:"default_debit_account_id"`
	DefaultCreditAccountID *uuid.UUID `json:"default_credit_account_id,omitempty" db:"default_credit_account_id"`
	IsActive               bool       `json:"is_active" db:"is_active"`
	CreatedAt              time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at" db:"updated_at"`
}

// JournalPaymentMethod links a payment method to a journal with direction
type JournalPaymentMethod struct {
	ID                   uuid.UUID  `json:"id" db:"id"`
	TenantID             uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	JournalID            uuid.UUID  `json:"journal_id" db:"journal_id"`
	PaymentMethodID      uuid.UUID  `json:"payment_method_id" db:"payment_method_id"`
	Direction            string     `json:"direction" db:"direction"` // inbound, outbound
	Name                 string     `json:"name" db:"name"`
	OutstandingAccountID *uuid.UUID `json:"outstanding_account_id,omitempty" db:"outstanding_account_id"`
	IsActive             bool       `json:"is_active" db:"is_active"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
}

// CreateJournalInput is the input for creating a journal
type CreateJournalInput struct {
	Code                   string  `json:"code" binding:"required,min=1,max=20"`
	Name                   string  `json:"name" binding:"required,min=1,max=100"`
	Type                   string  `json:"type" binding:"required,oneof=general sales purchase cash bank miscellaneous"`
	Description            string  `json:"description"`
	DefaultDebitAccountID  *string `json:"default_debit_account_id"`
	DefaultCreditAccountID *string `json:"default_credit_account_id"`
	AutoSequence           bool    `json:"auto_sequence"`
	NumberPrefix           string  `json:"number_prefix"`
	ShortCode              string  `json:"short_code"`
	Currency               string  `json:"currency"`
	BankAccountID          *string `json:"bank_account_id"`
	SuspenseAccountID      *string `json:"suspense_account_id"`
	ProfitAccountID        *string `json:"profit_account_id"`
	LossAccountID          *string `json:"loss_account_id"`
}

// UpdateJournalInput is the input for updating a journal
type UpdateJournalInput struct {
	Name                   *string `json:"name"`
	Description            *string `json:"description"`
	DefaultDebitAccountID  *string `json:"default_debit_account_id"`
	DefaultCreditAccountID *string `json:"default_credit_account_id"`
	AutoSequence           *bool   `json:"auto_sequence"`
	NumberPrefix           *string `json:"number_prefix"`
	IsActive               *bool   `json:"is_active"`
	ShortCode              *string `json:"short_code"`
	Currency               *string `json:"currency"`
	BankAccountID          *string `json:"bank_account_id"`
	SuspenseAccountID      *string `json:"suspense_account_id"`
	ProfitAccountID        *string `json:"profit_account_id"`
	LossAccountID          *string `json:"loss_account_id"`
}

// JournalEntry represents a journal entry header
type JournalEntry struct {
	ID              uuid.UUID    `json:"id" db:"id"`
	TenantID        uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID  *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	JournalID       uuid.UUID    `json:"journal_id" db:"journal_id"`
	FiscalPeriodID  *uuid.UUID   `json:"fiscal_period_id,omitempty" db:"fiscal_period_id"`
	EntryNumber     string       `json:"entry_number" db:"entry_number"`
	EntryDate       time.Time    `json:"entry_date" db:"entry_date"`
	Reference       *string      `json:"reference,omitempty" db:"reference"`
	Description     *string      `json:"description,omitempty" db:"description"`
	SourceType      *string      `json:"source_type,omitempty" db:"source_type"` // manual, invoice, payment, adjustment
	SourceID        *uuid.UUID   `json:"source_id,omitempty" db:"source_id"`
	CurrencyID      *uuid.UUID   `json:"currency_id,omitempty" db:"currency_id"`
	ExchangeRate    float64      `json:"exchange_rate" db:"exchange_rate"`
	TotalDebit      float64      `json:"total_debit" db:"total_debit"`
	TotalCredit     float64      `json:"total_credit" db:"total_credit"`
	Status          string       `json:"status" db:"status"` // draft, posted, cancelled
	PostedAt        *time.Time   `json:"posted_at,omitempty" db:"posted_at"`
	PostedBy        *uuid.UUID   `json:"posted_by,omitempty" db:"posted_by"`
	ReversedEntryID *uuid.UUID   `json:"reversed_entry_id,omitempty" db:"reversed_entry_id"`
	IsReversal      bool         `json:"is_reversal" db:"is_reversal"`
	ReversalOfID    *uuid.UUID   `json:"reversal_of_id,omitempty" db:"reversal_of_id"`
	ReversalReason  *string      `json:"reversal_reason,omitempty" db:"reversal_reason"`
	CancelledAt     *time.Time   `json:"cancelled_at,omitempty" db:"cancelled_at"`
	Tags            []string     `json:"tags,omitempty" db:"tags"`
	CreatedBy       *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime `json:"-" db:"deleted_at"`

	// Relationships
	Lines   []JournalEntryLine `json:"lines,omitempty"`
	Journal *Journal           `json:"journal,omitempty"`
}

// JournalEntryLine represents a journal entry line
type JournalEntryLine struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	JournalEntryID uuid.UUID  `json:"journal_entry_id" db:"journal_entry_id"`
	LineNumber     int        `json:"line_number" db:"line_number"`
	AccountID      uuid.UUID  `json:"account_id" db:"account_id"`
	ContactID      *uuid.UUID `json:"contact_id,omitempty" db:"contact_id"`
	Description    *string    `json:"description,omitempty" db:"description"`
	DebitAmount    float64    `json:"debit_amount" db:"debit_amount"`
	CreditAmount   float64    `json:"credit_amount" db:"credit_amount"`
	CurrencyID     *uuid.UUID `json:"currency_id,omitempty" db:"currency_id"`
	CurrencyAmount *float64   `json:"currency_amount,omitempty" db:"currency_amount"`
	ExchangeRate   float64    `json:"exchange_rate" db:"exchange_rate"`
	TaxID          *uuid.UUID `json:"tax_id,omitempty" db:"tax_id"`
	TaxAmount      float64    `json:"tax_amount" db:"tax_amount"`
	Reconciled     bool       `json:"reconciled" db:"reconciled"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`

	// Relationships
	Account *Account `json:"account,omitempty"`
}

// JournalEntryResponse is the API response format
type JournalEntryResponse struct {
	ID              uuid.UUID            `json:"id"`
	JournalID       uuid.UUID            `json:"journal_id"`
	EntryNumber     string               `json:"entry_number"`
	EntryDate       string               `json:"entry_date"`
	Reference       *string              `json:"reference,omitempty"`
	Description     *string              `json:"description,omitempty"`
	SourceType      *string              `json:"source_type,omitempty"`
	TotalDebit      float64              `json:"total_debit"`
	TotalCredit     float64              `json:"total_credit"`
	Status          string               `json:"status"`
	PostedAt        *time.Time           `json:"posted_at,omitempty"`
	ReversedEntryID *uuid.UUID           `json:"reversed_entry_id,omitempty"`
	IsReversal      bool                 `json:"is_reversal"`
	ReversalOfID    *uuid.UUID           `json:"reversal_of_id,omitempty"`
	ReversalReason  *string              `json:"reversal_reason,omitempty"`
	Tags            []string             `json:"tags,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	Lines           []JournalEntryLine   `json:"lines,omitempty"`
	Journal         *Journal             `json:"journal,omitempty"`
}

// ToResponse converts JournalEntry to JournalEntryResponse
func (je *JournalEntry) ToResponse() *JournalEntryResponse {
	return &JournalEntryResponse{
		ID:              je.ID,
		JournalID:       je.JournalID,
		EntryNumber:     je.EntryNumber,
		EntryDate:       je.EntryDate.Format("2006-01-02"),
		Reference:       je.Reference,
		Description:     je.Description,
		SourceType:      je.SourceType,
		TotalDebit:      je.TotalDebit,
		TotalCredit:     je.TotalCredit,
		Status:          je.Status,
		PostedAt:        je.PostedAt,
		ReversedEntryID: je.ReversedEntryID,
		IsReversal:      je.IsReversal,
		ReversalOfID:    je.ReversalOfID,
		ReversalReason:  je.ReversalReason,
		Tags:            je.Tags,
		CreatedAt:       je.CreatedAt,
		Lines:           je.Lines,
		Journal:         je.Journal,
	}
}

// CreateJournalEntryInput is the input for creating a journal entry
type CreateJournalEntryInput struct {
	JournalID      string                        `json:"journal_id" binding:"required"`
	OrganizationID string                        `json:"organization_id"`
	EntryDate      string                        `json:"entry_date" binding:"required"`
	Reference      string                        `json:"reference"`
	Description    string                        `json:"description"`
	Tags           []string                      `json:"tags"`
	CurrencyID     *string                       `json:"currency_id"`
	ExchangeRate   float64                       `json:"exchange_rate"`
	Lines          []CreateJournalEntryLineInput `json:"lines" binding:"required,min=2"`
}

// CreateJournalEntryLineInput is the input for a journal entry line
type CreateJournalEntryLineInput struct {
	AccountID    string  `json:"account_id" binding:"required"`
	ContactID    *string `json:"contact_id"`
	Description  string  `json:"description"`
	DebitAmount  float64 `json:"debit_amount"`
	CreditAmount float64 `json:"credit_amount"`
	TaxID        *string `json:"tax_id"`
}

// ReverseJournalEntryInput is the input for reversing a journal entry
type ReverseJournalEntryInput struct {
	Date   string `json:"date"`   // optional reversal date (YYYY-MM-DD), defaults to today
	Reason string `json:"reason"` // reason for reversal
}

// JournalEntryListFilter is the filter for listing journal entries
type JournalEntryListFilter struct {
	Search    string `form:"search"`
	JournalID string `form:"journal_id"`
	Status    string `form:"status"`
	DateFrom  string `form:"date_from"`
	DateTo    string `form:"date_to"`
	AccountID string `form:"account_id"`
}

// =====================================================
// PAYMENTS
// =====================================================

// Payment represents a payment record
type Payment struct {
	ID              uuid.UUID    `json:"id" db:"id"`
	TenantID        uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID  *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	PaymentNumber   string       `json:"payment_number" db:"payment_number"`
	Type            string       `json:"type" db:"type"` // receipt, payment
	ContactID       uuid.UUID    `json:"contact_id" db:"contact_id"`
	PaymentMethodID *uuid.UUID   `json:"payment_method_id,omitempty" db:"payment_method_id"`
	BankAccountID   *uuid.UUID   `json:"bank_account_id,omitempty" db:"bank_account_id"`
	PaymentDate     time.Time    `json:"payment_date" db:"payment_date"`
	Amount          float64      `json:"amount" db:"amount"`
	CurrencyID      *uuid.UUID   `json:"currency_id,omitempty" db:"currency_id"`
	ExchangeRate    float64      `json:"exchange_rate" db:"exchange_rate"`
	Reference       *string      `json:"reference,omitempty" db:"reference"`
	Notes           *string      `json:"notes,omitempty" db:"notes"`
	Status          string       `json:"status" db:"status"` // draft, confirmed, cancelled
	JournalEntryID  *uuid.UUID   `json:"journal_entry_id,omitempty" db:"journal_entry_id"`
	ApprovedBy      *uuid.UUID   `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt      *time.Time   `json:"approved_at,omitempty" db:"approved_at"`
	CreatedBy       *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime `json:"-" db:"deleted_at"`

	// Relationships
	Allocations []PaymentAllocation `json:"allocations,omitempty"`
	Contact     *Contact            `json:"contact,omitempty"`
}

// PaymentAllocation represents allocation of payment to invoices
type PaymentAllocation struct {
	ID           uuid.UUID `json:"id" db:"id"`
	PaymentID    uuid.UUID `json:"payment_id" db:"payment_id"`
	DocumentType string    `json:"document_type" db:"document_type"` // sales_invoice, purchase_invoice
	DocumentID   uuid.UUID `json:"document_id" db:"document_id"`
	Amount       float64   `json:"amount" db:"amount"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// PaymentResponse is the API response format
type PaymentResponse struct {
	ID              uuid.UUID           `json:"id"`
	PaymentNumber   string              `json:"payment_number"`
	Type            string              `json:"type"`
	ContactID       uuid.UUID           `json:"contact_id"`
	ContactName     string              `json:"contact_name,omitempty"`
	PaymentDate     string              `json:"payment_date"`
	Amount          float64             `json:"amount"`
	Status          string              `json:"status"`
	Reference       *string             `json:"reference,omitempty"`
	Notes           *string             `json:"notes,omitempty"`
	JournalID       string              `json:"journal_id,omitempty"`
	JournalName     string              `json:"journal_name,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	Allocations     []PaymentAllocation `json:"allocations,omitempty"`
}

// ToResponse converts Payment to PaymentResponse
func (p *Payment) ToResponse() *PaymentResponse {
	resp := &PaymentResponse{
		ID:            p.ID,
		PaymentNumber: p.PaymentNumber,
		Type:          p.Type,
		ContactID:     p.ContactID,
		PaymentDate:   p.PaymentDate.Format("2006-01-02"),
		Amount:        p.Amount,
		Status:        p.Status,
		Reference:     p.Reference,
		Notes:         p.Notes,
		CreatedAt:     p.CreatedAt,
		Allocations:   p.Allocations,
	}
	if p.Contact != nil {
		resp.ContactName = p.Contact.Name
	}
	return resp
}

// CreatePaymentInput is the input for creating a payment
type CreatePaymentInput struct {
	Type            string                   `json:"type" binding:"required,oneof=receipt payment"`
	ContactID       string                   `json:"contact_id" binding:"required"`
	PaymentMethodID *string                  `json:"payment_method_id"`
	BankAccountID   *string                  `json:"bank_account_id"`
	JournalID       *string                  `json:"journal_id"`
	PaymentDate     string                   `json:"payment_date" binding:"required"`
	Amount          float64                  `json:"amount" binding:"required,gt=0"`
	CurrencyID      *string                  `json:"currency_id"`
	ExchangeRate    float64                  `json:"exchange_rate"`
	Reference       string                   `json:"reference"`
	Notes           string                   `json:"notes"`
	Allocations     []PaymentAllocationInput `json:"allocations"`
}

// PaymentAllocationInput is the input for a payment allocation
type PaymentAllocationInput struct {
	DocumentType string  `json:"document_type" binding:"required,oneof=sales_invoice purchase_invoice"`
	DocumentID   string  `json:"document_id" binding:"required"`
	Amount       float64 `json:"amount" binding:"required,gt=0"`
}

// PaymentListFilter is the filter for listing payments
type PaymentListFilter struct {
	Search    string `form:"search"`
	Type      string `form:"type"` // receipt, payment
	Status    string `form:"status"`
	ContactID string `form:"contact_id"`
	DateFrom  string `form:"date_from"`
	DateTo    string `form:"date_to"`
}

// =====================================================
// TAX RATES
// =====================================================

// TaxRate represents a tax configuration
type TaxRate struct {
	ID            uuid.UUID    `json:"id" db:"id"`
	TenantID      uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	Code          string       `json:"code" db:"code"`
	Name          string       `json:"name" db:"name"`
	Description   *string      `json:"description,omitempty" db:"description"`
	Rate          float64      `json:"rate" db:"rate"`
	Type          string       `json:"type" db:"type"`         // percentage, fixed
	TaxType       string       `json:"tax_type" db:"tax_type"` // sales, purchase
	TaxAccountID  *uuid.UUID   `json:"tax_account_id,omitempty" db:"tax_account_id"`
	IsCompound    bool         `json:"is_compound" db:"is_compound"`
	IsRecoverable bool         `json:"is_recoverable" db:"is_recoverable"`
	PriceInclude  bool         `json:"price_include" db:"price_include"`
	IsActive      bool         `json:"is_active" db:"is_active"`
	CreatedAt     time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt     sql.NullTime `json:"-" db:"deleted_at"`
}

// CreateTaxRateInput is the input for creating a tax rate
type CreateTaxRateInput struct {
	Code          string  `json:"code" binding:"required,min=1,max=20"`
	Name          string  `json:"name" binding:"required,min=1,max=100"`
	Description   string  `json:"description"`
	Rate          float64 `json:"rate" binding:"required,gte=0"`
	Type          string  `json:"type" binding:"required,oneof=percentage fixed"`
	TaxType       string  `json:"tax_type" binding:"omitempty,oneof=sales purchase"`
	TaxAccountID  *string `json:"tax_account_id"`
	IsCompound    bool    `json:"is_compound"`
	IsRecoverable bool    `json:"is_recoverable"`
	PriceInclude  bool    `json:"price_include"`
}

// UpdateTaxRateInput is the input for updating a tax rate
type UpdateTaxRateInput struct {
	Name          *string  `json:"name"`
	Description   *string  `json:"description"`
	Rate          *float64 `json:"rate"`
	TaxType       *string  `json:"tax_type"`
	TaxAccountID  *string  `json:"tax_account_id"`
	IsCompound    *bool    `json:"is_compound"`
	IsRecoverable *bool    `json:"is_recoverable"`
	PriceInclude  *bool    `json:"price_include"`
	IsActive      *bool    `json:"is_active"`
}

// =====================================================
// CURRENCIES
// =====================================================

// Currency represents a currency (referenced from 001_core_schema.sql)
type Currency struct {
	ID             uuid.UUID `json:"id" db:"id"`
	Code           string    `json:"code" db:"code"`
	Name           string    `json:"name" db:"name"`
	Symbol         string    `json:"symbol" db:"symbol"`
	DecimalPlaces  int       `json:"decimal_places" db:"decimal_places"`
	IsBaseCurrency bool      `json:"is_base_currency" db:"is_base_currency"`
	IsActive       bool      `json:"is_active" db:"is_active"`
}

// CreateCurrencyInput is the input for creating a currency
type CreateCurrencyInput struct {
	Code           string `json:"code" binding:"required,min=3,max=3"`
	Name           string `json:"name" binding:"required,min=1,max=100"`
	Symbol         string `json:"symbol" binding:"required,min=1,max=10"`
	DecimalPlaces  int    `json:"decimal_places"`
	IsBaseCurrency bool   `json:"is_base_currency"`
}

// UpdateCurrencyInput is the input for updating a currency
type UpdateCurrencyInput struct {
	Name           *string `json:"name"`
	Symbol         *string `json:"symbol"`
	DecimalPlaces  *int    `json:"decimal_places"`
	IsBaseCurrency *bool   `json:"is_base_currency"`
	IsActive       *bool   `json:"is_active"`
}

// ExchangeRate represents an exchange rate
type ExchangeRate struct {
	ID             uuid.UUID `json:"id" db:"id"`
	TenantID       uuid.UUID `json:"tenant_id" db:"tenant_id"`
	FromCurrencyID uuid.UUID `json:"from_currency_id" db:"from_currency_id"`
	ToCurrencyID   uuid.UUID `json:"to_currency_id" db:"to_currency_id"`
	Rate           float64   `json:"rate" db:"rate"`
	EffectiveDate  time.Time `json:"effective_date" db:"effective_date"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// =====================================================
// BANK ACCOUNTS
// =====================================================

// BankAccount represents a bank account for the company
type BankAccount struct {
	ID              uuid.UUID    `json:"id" db:"id"`
	TenantID        uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID  *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	Name            string       `json:"name" db:"name"`
	BankName        string       `json:"bank_name" db:"bank_name"`
	AccountNumber   string       `json:"account_number" db:"account_number"`
	Currency        string       `json:"currency" db:"currency"`
	AccountType     string       `json:"account_type" db:"account_type"` // checking, savings, etc.
	Balance         float64      `json:"balance" db:"balance"`
	IsActive        bool         `json:"is_active" db:"is_active"`
	LastReconciled  *time.Time   `json:"last_reconciled,omitempty" db:"last_reconciled"`
	AccountID       *uuid.UUID   `json:"account_id,omitempty" db:"account_id"` // Link to chart of accounts
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime `json:"-" db:"deleted_at"`
}

// CreateBankAccountInput is the input for creating a bank account
type CreateBankAccountInput struct {
	Name          string  `json:"name" binding:"required,min=1,max=255"`
	BankName      string  `json:"bank_name" binding:"required,min=1,max=255"`
	AccountNumber string  `json:"account_number" binding:"required,min=1,max=50"`
	Currency      string  `json:"currency" binding:"required,min=1,max=10"`
	AccountType   string  `json:"account_type" binding:"required,oneof=checking savings money_market certificate"`
	Balance       float64 `json:"balance"`
	AccountID     *string `json:"account_id"`
}

// UpdateBankAccountInput is the input for updating a bank account
type UpdateBankAccountInput struct {
	Name          *string  `json:"name"`
	BankName      *string  `json:"bank_name"`
	AccountNumber *string  `json:"account_number"`
	Currency      *string  `json:"currency"`
	AccountType   *string  `json:"account_type"`
	Balance       *float64 `json:"balance"`
	IsActive      *bool    `json:"is_active"`
	AccountID     *string  `json:"account_id"`
}

// BankAccountListFilter is the filter for listing bank accounts
type BankAccountListFilter struct {
	Search      string `form:"search"`
	Currency    string `form:"currency"`
	AccountType string `form:"account_type"`
	IsActive    *bool  `form:"is_active"`
}

// =====================================================
// BANK TRANSACTIONS
// =====================================================

// BankTransaction represents a bank transaction for reconciliation
type BankTransaction struct {
	ID                    uuid.UUID  `json:"id" db:"id"`
	TenantID              uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	BankAccountID         uuid.UUID  `json:"bank_account_id" db:"bank_account_id"`
	TransactionDate       time.Time  `json:"transaction_date" db:"transaction_date"`
	ValueDate             *time.Time `json:"value_date,omitempty" db:"value_date"`
	Reference             string     `json:"reference" db:"reference"`
	Description           string     `json:"description" db:"description"`
	Amount                float64    `json:"amount" db:"amount"`
	BalanceAfter          *float64   `json:"balance_after,omitempty" db:"balance_after"`
	TransactionType       string     `json:"type" db:"transaction_type"` // debit, credit
	Status                string     `json:"status" db:"status"`         // unmatched, matched, reconciled
	MatchedJournalEntryID *uuid.UUID `json:"matched_journal_entry_id,omitempty" db:"matched_journal_entry_id"`
	IsReconciled          bool       `json:"is_reconciled" db:"-"` // Computed from status
	ReconciledDate        *time.Time `json:"reconciled_date,omitempty" db:"-"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at" db:"updated_at"`
}

// CreateBankTransactionInput is the input for creating a bank transaction
type CreateBankTransactionInput struct {
	TransactionDate string  `json:"transaction_date" binding:"required"`
	Reference       string  `json:"reference"`
	Description     string  `json:"description" binding:"required"`
	Amount          float64 `json:"amount" binding:"required,gt=0"`
	Type            string  `json:"type" binding:"required,oneof=debit credit"`
}

// BankTransactionListFilter is the filter for listing bank transactions
type BankTransactionListFilter struct {
	Search   string `form:"search"`
	Type     string `form:"type"` // debit, credit
	Status   string `form:"status"`
	DateFrom string `form:"date_from"`
	DateTo   string `form:"date_to"`
}

// =====================================================
// CASH TRANSACTIONS (Kassa)
// =====================================================

// CashTransaction represents a cash register transaction
type CashTransaction struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	TenantID        uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	TransactionDate time.Time  `json:"transaction_date" db:"transaction_date"`
	Type            string     `json:"type" db:"transaction_type"` // income, expense, transfer
	Amount          float64    `json:"amount" db:"amount"`
	Currency        string     `json:"currency" db:"currency"`
	Description     string     `json:"description" db:"description"`
	Category        string     `json:"category" db:"category"`
	Reference       string     `json:"reference" db:"reference"`
	Cashier         string     `json:"cashier" db:"cashier"`
	Status          string     `json:"status" db:"status"` // draft, posted
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
}

// CreateCashTransactionInput is the input for creating a cash transaction
type CreateCashTransactionInput struct {
	TransactionDate string  `json:"transaction_date" binding:"required"`
	Type            string  `json:"type" binding:"required,oneof=income expense transfer"`
	Amount          float64 `json:"amount" binding:"required,gt=0"`
	Currency        string  `json:"currency"`
	Description     string  `json:"description" binding:"required"`
	Category        string  `json:"category"`
	Reference       string  `json:"reference"`
	Cashier         string  `json:"cashier"`
}

// UpdateCashTransactionInput is the input for updating a cash transaction
type UpdateCashTransactionInput struct {
	TransactionDate *string  `json:"transaction_date"`
	Type            *string  `json:"type"`
	Amount          *float64 `json:"amount"`
	Currency        *string  `json:"currency"`
	Description     *string  `json:"description"`
	Category        *string  `json:"category"`
	Reference       *string  `json:"reference"`
	Cashier         *string  `json:"cashier"`
}

// CashTransactionListFilter is the filter for listing cash transactions
type CashTransactionListFilter struct {
	Search   string `form:"search"`
	Type     string `form:"type"` // income, expense, transfer
	Category string `form:"category"`
	DateFrom string `form:"date_from"`
	DateTo   string `form:"date_to"`
}

// =====================================================
// FINANCIAL REPORTS
// =====================================================

// TrialBalanceReport represents trial balance data
type TrialBalanceReport struct {
	AsOfDate    string                  `json:"as_of_date"`
	TotalDebit  float64                 `json:"total_debit"`
	TotalCredit float64                 `json:"total_credit"`
	IsBalanced  bool                    `json:"is_balanced"`
	Accounts    []TrialBalanceAccount   `json:"accounts"`
}

// TrialBalanceAccount represents a single account in trial balance
type TrialBalanceAccount struct {
	AccountID     uuid.UUID `json:"account_id"`
	AccountCode   string    `json:"account_code"`
	AccountName   string    `json:"account_name"`
	Category      string    `json:"category"`
	DebitBalance  float64   `json:"debit_balance"`
	CreditBalance float64   `json:"credit_balance"`
}

// BalanceSheetReport represents balance sheet data
type BalanceSheetReport struct {
	AsOfDate        string              `json:"as_of_date"`
	TotalAssets     float64             `json:"total_assets"`
	TotalLiabilities float64            `json:"total_liabilities"`
	TotalEquity     float64             `json:"total_equity"`
	Assets          []BalanceSheetSection `json:"assets"`
	Liabilities     []BalanceSheetSection `json:"liabilities"`
	Equity          []BalanceSheetSection `json:"equity"`
}

// BalanceSheetSection represents a section in balance sheet
type BalanceSheetSection struct {
	Category string                `json:"category"`
	Total    float64               `json:"total"`
	Accounts []BalanceSheetAccount `json:"accounts"`
}

// BalanceSheetAccount represents an account in balance sheet
type BalanceSheetAccount struct {
	AccountID   uuid.UUID `json:"account_id"`
	AccountCode string    `json:"account_code"`
	AccountName string    `json:"account_name"`
	Balance     float64   `json:"balance"`
}

// IncomeStatementReport represents income statement (P&L) data
type IncomeStatementReport struct {
	PeriodFrom       string                    `json:"period_from"`
	PeriodTo         string                    `json:"period_to"`
	TotalRevenue     float64                   `json:"total_revenue"`
	TotalExpenses    float64                   `json:"total_expenses"`
	GrossProfit      float64                   `json:"gross_profit"`
	OperatingProfit  float64                   `json:"operating_profit"`
	NetIncome        float64                   `json:"net_income"`
	Revenue          []IncomeStatementSection  `json:"revenue"`
	CostOfSales      []IncomeStatementSection  `json:"cost_of_sales"`
	OperatingExpenses []IncomeStatementSection `json:"operating_expenses"`
	OtherIncome      []IncomeStatementSection  `json:"other_income"`
	OtherExpenses    []IncomeStatementSection  `json:"other_expenses"`
}

// IncomeStatementSection represents a section in income statement
type IncomeStatementSection struct {
	AccountID   uuid.UUID `json:"account_id"`
	AccountCode string    `json:"account_code"`
	AccountName string    `json:"account_name"`
	Amount      float64   `json:"amount"`
}

// GeneralLedgerReport represents general ledger data
type GeneralLedgerReport struct {
	PeriodFrom string                  `json:"period_from"`
	PeriodTo   string                  `json:"period_to"`
	Accounts   []GeneralLedgerAccount  `json:"accounts"`
}

// GeneralLedgerAccount represents an account in general ledger
type GeneralLedgerAccount struct {
	AccountID      uuid.UUID                `json:"account_id"`
	AccountCode    string                   `json:"account_code"`
	AccountName    string                   `json:"account_name"`
	OpeningBalance float64                  `json:"opening_balance"`
	TotalDebit     float64                  `json:"total_debit"`
	TotalCredit    float64                  `json:"total_credit"`
	ClosingBalance float64                  `json:"closing_balance"`
	Transactions   []GeneralLedgerTransaction `json:"transactions"`
}

// GeneralLedgerTransaction represents a transaction in general ledger
type GeneralLedgerTransaction struct {
	Date           string  `json:"date"`
	EntryNumber    string  `json:"entry_number"`
	Description    string  `json:"description"`
	Reference      string  `json:"reference"`
	DebitAmount    float64 `json:"debit_amount"`
	CreditAmount   float64 `json:"credit_amount"`
	RunningBalance float64 `json:"running_balance"`
}

// AgingReport represents aging report data (AR/AP)
type AgingReport struct {
	AsOfDate     string          `json:"as_of_date"`
	ReportType   string          `json:"report_type"` // receivables, payables
	TotalAmount  float64         `json:"total_amount"`
	CurrentTotal float64         `json:"current_total"`
	Days1To30    float64         `json:"days_1_to_30"`
	Days31To60   float64         `json:"days_31_to_60"`
	Days61To90   float64         `json:"days_61_to_90"`
	Over90Days   float64         `json:"over_90_days"`
	Contacts     []AgingContact  `json:"contacts"`
}

// AgingContact represents a contact in aging report
type AgingContact struct {
	ContactID    uuid.UUID       `json:"contact_id"`
	ContactName  string          `json:"contact_name"`
	TotalAmount  float64         `json:"total_amount"`
	Current      float64         `json:"current"`
	Days1To30    float64         `json:"days_1_to_30"`
	Days31To60   float64         `json:"days_31_to_60"`
	Days61To90   float64         `json:"days_61_to_90"`
	Over90Days   float64         `json:"over_90_days"`
	Invoices     []AgingInvoice  `json:"invoices,omitempty"`
}

// AgingInvoice represents an invoice in aging report
type AgingInvoice struct {
	InvoiceID     uuid.UUID `json:"invoice_id"`
	InvoiceNumber string    `json:"invoice_number"`
	InvoiceDate   string    `json:"invoice_date"`
	DueDate       string    `json:"due_date"`
	TotalAmount   float64   `json:"total_amount"`
	AmountDue     float64   `json:"amount_due"`
	DaysOverdue   int       `json:"days_overdue"`
	AgingBucket   string    `json:"aging_bucket"`
}

// CashFlowReport represents cash flow statement data
type CashFlowReport struct {
	PeriodFrom            string                 `json:"period_from"`
	PeriodTo              string                 `json:"period_to"`
	OpeningCashBalance    float64                `json:"opening_cash_balance"`
	ClosingCashBalance    float64                `json:"closing_cash_balance"`
	NetCashChange         float64                `json:"net_cash_change"`
	OperatingActivities   CashFlowSection        `json:"operating_activities"`
	InvestingActivities   CashFlowSection        `json:"investing_activities"`
	FinancingActivities   CashFlowSection        `json:"financing_activities"`
}

// CashFlowSection represents a section in cash flow statement
type CashFlowSection struct {
	Total float64           `json:"total"`
	Items []CashFlowItem    `json:"items"`
}

// CashFlowItem represents an item in cash flow section
type CashFlowItem struct {
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
}

// =====================================================
// FISCAL YEARS & PERIODS
// =====================================================

// FiscalYear represents a fiscal year
type FiscalYear struct {
	ID             uuid.UUID    `json:"id" db:"id"`
	TenantID       uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	Code           string       `json:"code" db:"code"`
	Name           string       `json:"name" db:"name"`
	StartDate      time.Time    `json:"start_date" db:"start_date"`
	EndDate        time.Time    `json:"end_date" db:"end_date"`
	Status         string       `json:"status" db:"status"` // open, closed, locked
	CreatedAt      time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at" db:"updated_at"`

	// Relationships (loaded separately)
	Periods []FiscalPeriod `json:"periods,omitempty"`
}

// FiscalPeriod represents a fiscal period within a fiscal year
type FiscalPeriod struct {
	ID            uuid.UUID `json:"id" db:"id"`
	FiscalYearID  uuid.UUID `json:"fiscal_year_id" db:"fiscal_year_id"`
	Code          string    `json:"code" db:"code"`
	Name          string    `json:"name" db:"name"`
	PeriodNumber  int       `json:"period_number" db:"period_number"`
	StartDate     time.Time `json:"start_date" db:"start_date"`
	EndDate       time.Time `json:"end_date" db:"end_date"`
	Status        string    `json:"status" db:"status"` // open, closed, locked
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// ==============================================
// BUDGETS
// ==============================================

// Budget represents a financial budget
type Budget struct {
	ID             uuid.UUID    `json:"id" db:"id"`
	TenantID       uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	FiscalYearID   uuid.UUID    `json:"fiscal_year_id" db:"fiscal_year_id"`
	Code           string       `json:"code" db:"code"`
	Name           string       `json:"name" db:"name"`
	Description    *string      `json:"description,omitempty" db:"description"`
	BudgetType     string       `json:"budget_type" db:"budget_type"` // expense, revenue, combined, cashflow, investment
	TotalAmount    float64      `json:"total_amount" db:"total_amount"`
	Status         string       `json:"status" db:"status"` // draft, active, closed
	ApprovedBy     *uuid.UUID   `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt     *time.Time   `json:"approved_at,omitempty" db:"approved_at"`
	CreatedBy      *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt      time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt      sql.NullTime `json:"-" db:"deleted_at"`

	StartDate        *string  `json:"start_date,omitempty"`
	EndDate          *string  `json:"end_date,omitempty"`
	WarningThreshold float64  `json:"warning_threshold"`

	// New spec fields
	Approach              string     `json:"approach" db:"approach"`                               // fixed, flexible, zero_based, rolling
	Breakdown             string     `json:"breakdown" db:"breakdown"`                             // monthly, weekly, none
	OverspendPolicy       string     `json:"overspend_policy" db:"overspend_policy"`               // warn, require_approval, block
	ApprovalStatus        *string    `json:"approval_status,omitempty" db:"approval_status"`       // pending, approved, rejected
	RollingHorizonMonths  int        `json:"rolling_horizon_months" db:"rolling_horizon_months"`
	AutoExtend            bool       `json:"auto_extend" db:"auto_extend"`
	ResponsibleUserID     *uuid.UUID `json:"responsible_user_id,omitempty" db:"responsible_user_id"`
	DepartmentID          *uuid.UUID `json:"department_id,omitempty" db:"department_id"`
	SubmittedBy           *uuid.UUID `json:"submitted_by,omitempty" db:"submitted_by"`
	SubmittedAt           *time.Time `json:"submitted_at,omitempty" db:"submitted_at"`
	RejectionReason       *string    `json:"rejection_reason,omitempty" db:"rejection_reason"`

	// Relationships
	Lines []BudgetLine `json:"lines,omitempty"`
}

// BudgetLine represents a line item in a budget
type BudgetLine struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	BudgetID        uuid.UUID  `json:"budget_id" db:"budget_id"`
	AccountID       uuid.UUID  `json:"account_id" db:"account_id"`
	AccountName     string     `json:"account_name,omitempty"`
	AccountCode     string     `json:"account_code,omitempty"`
	FiscalPeriodID  *uuid.UUID `json:"fiscal_period_id,omitempty" db:"fiscal_period_id"`
	DepartmentID    *uuid.UUID `json:"department_id,omitempty" db:"department_id"`
	BudgetedAmount  float64    `json:"budgeted_amount" db:"budgeted_amount"`
	ActualAmount    float64    `json:"actual_amount" db:"actual_amount"`
	Variance        float64    `json:"variance" db:"variance"` // computed: budgeted - actual
	Notes           *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`

	// Monthly period breakdown
	Period1  float64 `json:"period_1" db:"period_1"`
	Period2  float64 `json:"period_2" db:"period_2"`
	Period3  float64 `json:"period_3" db:"period_3"`
	Period4  float64 `json:"period_4" db:"period_4"`
	Period5  float64 `json:"period_5" db:"period_5"`
	Period6  float64 `json:"period_6" db:"period_6"`
	Period7  float64 `json:"period_7" db:"period_7"`
	Period8  float64 `json:"period_8" db:"period_8"`
	Period9  float64 `json:"period_9" db:"period_9"`
	Period10 float64 `json:"period_10" db:"period_10"`
	Period11 float64 `json:"period_11" db:"period_11"`
	Period12 float64 `json:"period_12" db:"period_12"`

	// Flexible budget formula
	FormulaType  *string  `json:"formula_type,omitempty" db:"formula_type"`
	FormulaValue *float64 `json:"formula_value,omitempty" db:"formula_value"`
	FormulaCap   *float64 `json:"formula_cap,omitempty" db:"formula_cap"`

	// Zero-based justification
	Justification *string `json:"justification,omitempty" db:"justification"`

	// Line grouping
	LineType     string  `json:"line_type" db:"line_type"`       // revenue, expense, investment
	CategoryName *string `json:"category_name,omitempty" db:"category_name"`
}

// BudgetCategory represents a user-visible budget category linked to accounts
type BudgetCategory struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	TenantID      uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Name          string     `json:"name" db:"name"`
	ParentID      *uuid.UUID `json:"parent_id,omitempty" db:"parent_id"`
	LineType      string     `json:"line_type" db:"line_type"` // revenue, expense, investment
	IsActive      bool       `json:"is_active" db:"is_active"`
	SortOrder     int        `json:"sort_order" db:"sort_order"`
	DefaultFormula *string   `json:"default_formula,omitempty" db:"default_formula"`
	AccountIDs    []string   `json:"account_ids,omitempty"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}
