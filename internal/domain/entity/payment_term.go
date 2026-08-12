package entity

import (
	"time"

	"github.com/google/uuid"
)

// PaymentTerm represents configurable payment terms
type PaymentTerm struct {
	ID                 uuid.UUID  `json:"id" db:"id"`
	TenantID           uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code               string     `json:"code" db:"code"`
	Name               string     `json:"name" db:"name"`
	Description        *string    `json:"description,omitempty" db:"description"`
	TermType           string     `json:"term_type" db:"term_type"` // fixed, end_of_month, end_of_next_month, immediate
	DueDays            int        `json:"due_days" db:"due_days"`
	HasEarlyDiscount   bool       `json:"has_early_discount" db:"has_early_discount"`
	DiscountPercentage float64    `json:"discount_percentage" db:"discount_percentage"`
	DiscountDays       int        `json:"discount_days" db:"discount_days"`
	EndOfMonthDay      *int       `json:"end_of_month_day,omitempty" db:"end_of_month_day"`
	DisplayOrder       int        `json:"display_order" db:"display_order"`
	IsDefault          bool       `json:"is_default" db:"is_default"`
	IsActive           bool       `json:"is_active" db:"is_active"`
	CreatedBy          *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}

// PaymentTermInput is used for creating/updating payment terms
type PaymentTermInput struct {
	Code               string  `json:"code" binding:"required,max=30"`
	Name               string  `json:"name" binding:"required,max=100"`
	Description        *string `json:"description,omitempty"`
	TermType           string  `json:"term_type" binding:"required,oneof=fixed end_of_month end_of_next_month immediate"`
	DueDays            int     `json:"due_days" binding:"min=0,max=365"`
	HasEarlyDiscount   bool    `json:"has_early_discount"`
	DiscountPercentage float64 `json:"discount_percentage" binding:"min=0,max=100"`
	DiscountDays       int     `json:"discount_days" binding:"min=0,max=365"`
	EndOfMonthDay      *int    `json:"end_of_month_day,omitempty"`
	DisplayOrder       int     `json:"display_order"`
	IsDefault          bool    `json:"is_default"`
	IsActive           bool    `json:"is_active"`
}

// PaymentTermListItem is a simplified view for dropdowns
type PaymentTermListItem struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	DueDays     int       `json:"due_days"`
	IsDefault   bool      `json:"is_default"`
	Description *string   `json:"description,omitempty"`
}

// PaymentTermStats contains statistics about payment terms usage
type PaymentTermStats struct {
	TotalTerms      int `json:"total_terms"`
	ActiveTerms     int `json:"active_terms"`
	TermsWithOrders int `json:"terms_with_orders"`
}

// Term type constants
const (
	TermTypeFixed          = "fixed"
	TermTypeEndOfMonth     = "end_of_month"
	TermTypeEndOfNextMonth = "end_of_next_month"
	TermTypeImmediate      = "immediate"
)

// CalculateDueDate calculates the due date based on the payment term
func (pt *PaymentTerm) CalculateDueDate(invoiceDate time.Time) time.Time {
	switch pt.TermType {
	case TermTypeImmediate:
		return invoiceDate
	case TermTypeFixed:
		return invoiceDate.AddDate(0, 0, pt.DueDays)
	case TermTypeEndOfMonth:
		// Get last day of current month
		year, month, _ := invoiceDate.Date()
		firstOfNextMonth := time.Date(year, month+1, 1, 0, 0, 0, 0, invoiceDate.Location())
		endOfMonth := firstOfNextMonth.AddDate(0, 0, -1)
		// Add additional days if specified
		if pt.DueDays > 0 {
			return endOfMonth.AddDate(0, 0, pt.DueDays)
		}
		return endOfMonth
	case TermTypeEndOfNextMonth:
		// Get last day of next month
		year, month, _ := invoiceDate.Date()
		firstOfMonthAfterNext := time.Date(year, month+2, 1, 0, 0, 0, 0, invoiceDate.Location())
		endOfNextMonth := firstOfMonthAfterNext.AddDate(0, 0, -1)
		// Add additional days if specified
		if pt.DueDays > 0 {
			return endOfNextMonth.AddDate(0, 0, pt.DueDays)
		}
		return endOfNextMonth
	default:
		return invoiceDate.AddDate(0, 0, pt.DueDays)
	}
}

// CalculateEarlyDiscountDate calculates the early payment discount deadline
func (pt *PaymentTerm) CalculateEarlyDiscountDate(invoiceDate time.Time) *time.Time {
	if !pt.HasEarlyDiscount || pt.DiscountDays <= 0 {
		return nil
	}
	discountDate := invoiceDate.AddDate(0, 0, pt.DiscountDays)
	return &discountDate
}

// CalculateDiscountAmount calculates the early payment discount amount
func (pt *PaymentTerm) CalculateDiscountAmount(totalAmount float64) float64 {
	if !pt.HasEarlyDiscount || pt.DiscountPercentage <= 0 {
		return 0
	}
	return totalAmount * (pt.DiscountPercentage / 100)
}

// FormatDisplayName returns a formatted display name for the term
func (pt *PaymentTerm) FormatDisplayName() string {
	if pt.HasEarlyDiscount {
		return pt.Name // e.g., "2/10 Net 30"
	}
	return pt.Name
}
