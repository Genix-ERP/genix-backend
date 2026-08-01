package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// ExpenseCategory represents an expense category
type ExpenseCategory struct {
	ID          uuid.UUID    `json:"id" db:"id"`
	TenantID    uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	Code        string       `json:"code" db:"code"`
	Name        string       `json:"name" db:"name"`
	Description *string      `json:"description,omitempty" db:"description"`
	ParentID    *uuid.UUID   `json:"parent_id,omitempty" db:"parent_id"`
	AccountID   *uuid.UUID   `json:"account_id,omitempty" db:"account_id"`
	IsActive    bool         `json:"is_active" db:"is_active"`
	Color       string       `json:"color" db:"color"`
	Icon        string       `json:"icon" db:"icon"`
	Position    int          `json:"position" db:"position"`
	CreatedAt   time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at" db:"updated_at"`
}

// Expense represents an expense record
type Expense struct {
	ID            uuid.UUID    `json:"id" db:"id"`
	TenantID      uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	ExpenseNumber string       `json:"expense_number" db:"expense_number"`
	CategoryID    *uuid.UUID   `json:"category_id,omitempty" db:"category_id"`
	EmployeeID    *uuid.UUID   `json:"employee_id,omitempty" db:"employee_id"`
	EmployeeName  *string      `json:"employee_name,omitempty" db:"employee_name"`
	VendorID      *uuid.UUID   `json:"vendor_id,omitempty" db:"vendor_id"`
	VendorName    *string      `json:"vendor_name,omitempty" db:"vendor_name"`
	ExpenseDate   time.Time    `json:"expense_date" db:"expense_date"`
	Description   string       `json:"description" db:"description"`
	Amount        float64      `json:"amount" db:"amount"`
	TaxAmount     float64      `json:"tax_amount" db:"tax_amount"`
	TotalAmount   float64      `json:"total_amount" db:"total_amount"`
	Currency      string       `json:"currency" db:"currency"`
	PaymentMethod *string      `json:"payment_method,omitempty" db:"payment_method"`
	Reference     *string      `json:"reference,omitempty" db:"reference"`
	ReceiptURL    *string      `json:"receipt_url,omitempty" db:"receipt_url"`
	Status        string       `json:"status" db:"status"`
	Reimbursable  bool         `json:"reimbursable" db:"reimbursable"`
	// IsRecognized drives whether this expense is deducted from the
	// profit-tax base. Non-recognized expenses (fines, undocumented
	// spending, dividends, etc.) still count in the accounting profit but
	// are excluded from the tax base — see migration 336 and §6 of
	// ТЗ_Ish_Haqi_Soliq_Tolik.docx.
	IsRecognized   bool        `json:"is_recognized" db:"is_recognized"`
	ReimbursedDate *time.Time  `json:"reimbursed_date,omitempty" db:"reimbursed_date"`
	ApprovedBy    *uuid.UUID   `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt    *time.Time   `json:"approved_at,omitempty" db:"approved_at"`
	// Lifecycle v2 (migration 444)
	SubmittedAt      *time.Time `json:"submitted_at,omitempty" db:"submitted_at"`
	RejectedBy       *uuid.UUID `json:"rejected_by,omitempty" db:"rejected_by"`
	RejectedAt       *time.Time `json:"rejected_at,omitempty" db:"rejected_at"`
	RejectionReason  *string    `json:"rejection_reason,omitempty" db:"rejection_reason"`
	PaidAt           *time.Time `json:"paid_at,omitempty" db:"paid_at"`
	PaidBy           *uuid.UUID `json:"paid_by,omitempty" db:"paid_by"`
	PaymentAccountID *uuid.UUID `json:"payment_account_id,omitempty" db:"payment_account_id"`
	JournalEntryID   *uuid.UUID `json:"journal_entry_id,omitempty" db:"journal_entry_id"`
	Notes         *string      `json:"notes,omitempty" db:"notes"`
	CreatedBy     *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt     time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt     sql.NullTime `json:"-" db:"deleted_at"`

	// Computed fields
	CategoryName  string `json:"category_name,omitempty"`
	CategoryColor string `json:"category_color,omitempty"`
	CategoryIcon  string `json:"category_icon,omitempty"`
}

// CreateExpenseInput represents input for creating an expense.
// v2: CategoryID is validated as required in the handler (kept optional
// here so the error message is friendlier than gin's binding error).
// Status may be "draft" or "submitted" (default "submitted").
type CreateExpenseInput struct {
	CategoryID    string  `json:"category_id,omitempty"`
	CategoryName  string  `json:"category,omitempty"` // Allow passing category name
	Status        string  `json:"status,omitempty"`
	EmployeeID    string  `json:"employee_id,omitempty"`
	EmployeeName  string  `json:"employee_name,omitempty"`
	VendorID      string  `json:"vendor_id,omitempty"`
	VendorName    string  `json:"vendor_name,omitempty"`
	ExpenseDate   string  `json:"date" binding:"required"`
	Description   string  `json:"description" binding:"required"`
	Amount        float64 `json:"amount" binding:"required"`
	TaxAmount     float64 `json:"tax_amount"`
	Currency      string  `json:"currency"`
	PaymentMethod string  `json:"payment_method,omitempty"`
	Reference     string  `json:"reference,omitempty"`
	ReceiptURL    string  `json:"receipt_url,omitempty"`
	Reimbursable  bool    `json:"reimbursable"`
	// Defaults to TRUE (matches the DB default) so callers that don't
	// know about recognition get the safe "deductible" semantics.
	IsRecognized  *bool   `json:"is_recognized,omitempty"`
	Notes         string  `json:"notes,omitempty"`
}

// UpdateExpenseInput represents input for updating an expense.
// v2: Status was removed on purpose — lifecycle transitions go through
// the dedicated /submit, /approve, /reject, /pay endpoints so the
// server-side rules can't be bypassed with a bare PUT.
type UpdateExpenseInput struct {
	CategoryID    *string  `json:"category_id,omitempty"`
	CategoryName  *string  `json:"category,omitempty"`
	EmployeeID    *string  `json:"employee_id,omitempty"`
	EmployeeName  *string  `json:"employee_name,omitempty"`
	VendorID      *string  `json:"vendor_id,omitempty"`
	VendorName    *string  `json:"vendor_name,omitempty"`
	ExpenseDate   *string  `json:"date,omitempty"`
	Description   *string  `json:"description,omitempty"`
	Amount        *float64 `json:"amount,omitempty"`
	TaxAmount     *float64 `json:"tax_amount,omitempty"`
	Currency      *string  `json:"currency,omitempty"`
	PaymentMethod *string  `json:"payment_method,omitempty"`
	Reference     *string  `json:"reference,omitempty"`
	ReceiptURL    *string  `json:"receipt_url,omitempty"`
	Reimbursable  *bool    `json:"reimbursable,omitempty"`
	IsRecognized  *bool    `json:"is_recognized,omitempty"`
	Notes         *string  `json:"notes,omitempty"`
}

// ExpenseResponse represents the API response for an expense.
// v2 note: "category" and "employee_name" are ALWAYS serialized (no
// omitempty) — the old omitempty made empty categories vanish from the
// JSON, which the frontend then coerced to a fake "Boshqa" category
// (the one-slice donut bug, audit §2.1).
type ExpenseResponse struct {
	ID            uuid.UUID `json:"id"`
	ExpenseNumber string    `json:"expense_number"`
	CategoryID    string    `json:"category_id,omitempty"`
	CategoryName  string    `json:"category"`
	CategoryColor string    `json:"category_color,omitempty"`
	CategoryIcon  string    `json:"category_icon,omitempty"`
	EmployeeID    string    `json:"employee_id,omitempty"`
	EmployeeName  string    `json:"employee_name"`
	VendorID      string    `json:"vendor_id,omitempty"`
	VendorName    string    `json:"vendor_name,omitempty"`
	ExpenseDate   string    `json:"date"`
	Description   string    `json:"description"`
	Amount        float64   `json:"amount"`
	TaxAmount     float64   `json:"tax_amount"`
	TotalAmount   float64   `json:"total_amount"`
	Currency      string    `json:"currency"`
	PaymentMethod string    `json:"payment_method,omitempty"`
	Reference     string    `json:"reference,omitempty"`
	ReceiptURL    string    `json:"receipt_url,omitempty"`
	Status        string    `json:"status"`
	Reimbursable  bool      `json:"reimbursable"`
	IsRecognized  bool      `json:"is_recognized"`
	Notes         string    `json:"notes,omitempty"`
	// Lifecycle v2
	SubmittedAt        *time.Time `json:"submitted_at,omitempty"`
	ApprovedAt         *time.Time `json:"approved_at,omitempty"`
	RejectedAt         *time.Time `json:"rejected_at,omitempty"`
	RejectionReason    string     `json:"rejection_reason,omitempty"`
	PaidAt             *time.Time `json:"paid_at,omitempty"`
	PaymentAccountID   string     `json:"payment_account_id,omitempty"`
	PaymentAccountName string     `json:"payment_account_name,omitempty"`
	JournalEntryID     string     `json:"journal_entry_id,omitempty"`
	JournalEntryNumber string     `json:"journal_entry_number,omitempty"`
	CreatedBy          string     `json:"created_by,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// ToResponse converts Expense to ExpenseResponse
func (e *Expense) ToResponse() *ExpenseResponse {
	resp := &ExpenseResponse{
		ID:            e.ID,
		ExpenseNumber: e.ExpenseNumber,
		ExpenseDate:   e.ExpenseDate.Format("2006-01-02"),
		Description:   e.Description,
		Amount:        e.Amount,
		TaxAmount:     e.TaxAmount,
		TotalAmount:   e.TotalAmount,
		Currency:      e.Currency,
		Status:        e.Status,
		Reimbursable:  e.Reimbursable,
		IsRecognized:  e.IsRecognized,
		CategoryName:  e.CategoryName,
		CategoryColor: e.CategoryColor,
		CategoryIcon:  e.CategoryIcon,
		SubmittedAt:   e.SubmittedAt,
		ApprovedAt:    e.ApprovedAt,
		RejectedAt:    e.RejectedAt,
		PaidAt:        e.PaidAt,
		CreatedAt:     e.CreatedAt,
		UpdatedAt:     e.UpdatedAt,
	}

	if e.CategoryID != nil {
		resp.CategoryID = e.CategoryID.String()
	}
	if e.EmployeeID != nil {
		resp.EmployeeID = e.EmployeeID.String()
	}
	if e.EmployeeName != nil {
		resp.EmployeeName = *e.EmployeeName
	}
	if e.VendorID != nil {
		resp.VendorID = e.VendorID.String()
	}
	if e.VendorName != nil {
		resp.VendorName = *e.VendorName
	}
	if e.PaymentMethod != nil {
		resp.PaymentMethod = *e.PaymentMethod
	}
	if e.Reference != nil {
		resp.Reference = *e.Reference
	}
	if e.ReceiptURL != nil {
		resp.ReceiptURL = *e.ReceiptURL
	}
	if e.Notes != nil {
		resp.Notes = *e.Notes
	}
	if e.RejectionReason != nil {
		resp.RejectionReason = *e.RejectionReason
	}
	if e.PaymentAccountID != nil {
		resp.PaymentAccountID = e.PaymentAccountID.String()
	}
	if e.JournalEntryID != nil {
		resp.JournalEntryID = e.JournalEntryID.String()
	}
	if e.CreatedBy != nil {
		resp.CreatedBy = e.CreatedBy.String()
	}

	return resp
}

// ExpenseCategoryResponse represents the API response for an expense category.
// AccountID + AccountCode + AccountName surface the chart-of-account that
// expenses in this category are posted against, so the frontend can render
// "Travel → 9410 — Operating Expense" without a second roundtrip.
type ExpenseCategoryResponse struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	ParentID    string    `json:"parent_id,omitempty"`
	AccountID   string    `json:"account_id,omitempty"`
	AccountCode string    `json:"account_code,omitempty"`
	AccountName string    `json:"account_name,omitempty"`
	IsActive    bool      `json:"is_active"`
	Color       string    `json:"color,omitempty"`
	Icon        string    `json:"icon,omitempty"`
	Position    int       `json:"position"`
	UsageCount  int       `json:"usage_count"`
}

// ToResponse converts ExpenseCategory to ExpenseCategoryResponse
func (c *ExpenseCategory) ToResponse() *ExpenseCategoryResponse {
	resp := &ExpenseCategoryResponse{
		ID:       c.ID,
		Code:     c.Code,
		Name:     c.Name,
		IsActive: c.IsActive,
		Color:    c.Color,
		Icon:     c.Icon,
		Position: c.Position,
	}
	if c.Description != nil {
		resp.Description = *c.Description
	}
	if c.ParentID != nil {
		resp.ParentID = c.ParentID.String()
	}
	if c.AccountID != nil {
		resp.AccountID = c.AccountID.String()
	}
	return resp
}
