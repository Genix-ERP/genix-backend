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
	ReimbursedDate *time.Time  `json:"reimbursed_date,omitempty" db:"reimbursed_date"`
	ApprovedBy    *uuid.UUID   `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt    *time.Time   `json:"approved_at,omitempty" db:"approved_at"`
	Notes         *string      `json:"notes,omitempty" db:"notes"`
	CreatedBy     *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt     time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt     sql.NullTime `json:"-" db:"deleted_at"`

	// Computed fields
	CategoryName string `json:"category_name,omitempty"`
}

// CreateExpenseInput represents input for creating an expense
type CreateExpenseInput struct {
	CategoryID    string  `json:"category_id,omitempty"`
	CategoryName  string  `json:"category,omitempty"` // Allow passing category name
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
	Notes         string  `json:"notes,omitempty"`
}

// UpdateExpenseInput represents input for updating an expense
type UpdateExpenseInput struct {
	CategoryID    *string  `json:"category_id,omitempty"`
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
	Status        *string  `json:"status,omitempty"`
	Reimbursable  *bool    `json:"reimbursable,omitempty"`
	Notes         *string  `json:"notes,omitempty"`
}

// ExpenseResponse represents the API response for an expense
type ExpenseResponse struct {
	ID            uuid.UUID `json:"id"`
	ExpenseNumber string    `json:"expense_number"`
	CategoryID    string    `json:"category_id,omitempty"`
	CategoryName  string    `json:"category,omitempty"`
	EmployeeID    string    `json:"employee_id,omitempty"`
	EmployeeName  string    `json:"employee_name,omitempty"`
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
	Notes         string    `json:"notes,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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
		CategoryName:  e.CategoryName,
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

	return resp
}

// ExpenseCategoryResponse represents the API response for an expense category
type ExpenseCategoryResponse struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	ParentID    string    `json:"parent_id,omitempty"`
	IsActive    bool      `json:"is_active"`
}

// ToResponse converts ExpenseCategory to ExpenseCategoryResponse
func (c *ExpenseCategory) ToResponse() *ExpenseCategoryResponse {
	resp := &ExpenseCategoryResponse{
		ID:       c.ID,
		Code:     c.Code,
		Name:     c.Name,
		IsActive: c.IsActive,
	}
	if c.Description != nil {
		resp.Description = *c.Description
	}
	if c.ParentID != nil {
		resp.ParentID = c.ParentID.String()
	}
	return resp
}
