package entity

import (
	"time"

	"github.com/google/uuid"
)

// ContractStatus represents the lifecycle status of a contract.
// Qoralama → Kelishuvda → Imzolashda → Amalda → Yakunlangan / Bekor qilingan,
// plus Muddati o'tgan set by the scheduler.
type ContractStatus string

const (
	ContractStatusDraft       ContractStatus = "draft"       // Qoralama
	ContractStatusNegotiation ContractStatus = "negotiation" // Kelishuvda
	ContractStatusSigning     ContractStatus = "signing"     // Imzolashda
	ContractStatusActive      ContractStatus = "active"      // Amalda
	ContractStatusCompleted   ContractStatus = "completed"   // Yakunlangan
	ContractStatusCancelled   ContractStatus = "cancelled"   // Bekor qilingan
	ContractStatusExpired     ContractStatus = "expired"     // Muddati o'tgan
)

// ContractTransitions defines the allowed server-side status transitions.
// Expired is only entered by the scheduler; from there a contract can be
// re-activated (prolonged via amendment) or closed out.
var ContractTransitions = map[ContractStatus][]ContractStatus{
	ContractStatusDraft:       {ContractStatusNegotiation, ContractStatusSigning, ContractStatusActive, ContractStatusCancelled},
	ContractStatusNegotiation: {ContractStatusDraft, ContractStatusSigning, ContractStatusActive, ContractStatusCancelled},
	ContractStatusSigning:     {ContractStatusNegotiation, ContractStatusActive, ContractStatusCancelled},
	ContractStatusActive:      {ContractStatusCompleted, ContractStatusCancelled},
	ContractStatusExpired:     {ContractStatusActive, ContractStatusCompleted, ContractStatusCancelled},
	ContractStatusCompleted:   {},
	ContractStatusCancelled:   {ContractStatusDraft},
}

// CanTransition reports whether from → to is an allowed status change.
func CanTransition(from, to ContractStatus) bool {
	for _, s := range ContractTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// ContractDirection: does money come in (kirim) or go out (chiqim).
type ContractDirection string

const (
	ContractDirectionIncome  ContractDirection = "income"  // kirim — customer pays us
	ContractDirectionExpense ContractDirection = "expense" // chiqim — we pay the vendor
)

// ContractType represents the type of contract
type ContractType string

const (
	ContractTypeFixed   ContractType = "fixed"
	ContractTypeAnnual  ContractType = "annual"
	ContractTypeMonthly ContractType = "monthly"
	ContractTypeProject ContractType = "project"
)

// CreateContractInput represents input for creating a contract.
// vendor_id is the counterparty (contacts table) regardless of direction.
type CreateContractInput struct {
	ContractNumber        string  `json:"contract_number,omitempty"` // empty = auto-generate
	Title                 string  `json:"title" binding:"required"`
	VendorID              string  `json:"vendor_id" binding:"required"`
	Direction             string  `json:"direction,omitempty"` // income | expense (default expense)
	ContractType          string  `json:"contract_type,omitempty"`
	StartDate             string  `json:"start_date" binding:"required"`
	EndDate               string  `json:"end_date,omitempty"` // empty = muddatsiz
	SignedDate            string  `json:"signed_date,omitempty"`
	Value                 float64 `json:"value" binding:"gte=0"`
	Currency              string  `json:"currency,omitempty"`
	CurrencyID            string  `json:"currency_id,omitempty"`
	Terms                 string  `json:"terms,omitempty"`
	Description           string  `json:"description,omitempty"`
	AutoRenewal           bool    `json:"auto_renewal,omitempty"`
	RenewalTermDays       int     `json:"renewal_term_days,omitempty"`
	Notes                 string  `json:"notes,omitempty"`
	ResponsibleEmployeeID string  `json:"responsible_employee_id,omitempty"`
}

// UpdateContractInput represents input for updating a contract.
// Status is deliberately absent — status moves only through the
// validated transition endpoint.
type UpdateContractInput struct {
	ContractNumber        *string  `json:"contract_number,omitempty"`
	Title                 *string  `json:"title,omitempty"`
	VendorID              *string  `json:"vendor_id,omitempty"`
	Direction             *string  `json:"direction,omitempty"`
	ContractType          *string  `json:"contract_type,omitempty"`
	StartDate             *string  `json:"start_date,omitempty"`
	EndDate               *string  `json:"end_date,omitempty"` // "" clears (muddatsiz)
	SignedDate            *string  `json:"signed_date,omitempty"`
	Value                 *float64 `json:"value,omitempty"`
	Currency              *string  `json:"currency,omitempty"`
	Terms                 *string  `json:"terms,omitempty"`
	Description           *string  `json:"description,omitempty"`
	AutoRenewal           *bool    `json:"auto_renewal,omitempty"`
	RenewalTermDays       *int     `json:"renewal_term_days,omitempty"`
	Notes                 *string  `json:"notes,omitempty"`
	ResponsibleEmployeeID *string  `json:"responsible_employee_id,omitempty"` // "" clears
}

// ContractStatusInput is the body of the transition endpoint.
type ContractStatusInput struct {
	Status string `json:"status" binding:"required"`
}

// ContractResponse represents the API response for a contract.
type ContractResponse struct {
	ID                      uuid.UUID         `json:"id"`
	ContractNumber          string            `json:"contract_number"`
	Title                   string            `json:"title"`
	VendorID                *uuid.UUID        `json:"vendor_id,omitempty"`
	VendorName              string            `json:"vendor_name"`
	Direction               ContractDirection `json:"direction"`
	ContractType            ContractType      `json:"contract_type"`
	Status                  ContractStatus    `json:"status"`
	AllowedTransitions      []ContractStatus  `json:"allowed_transitions"`
	StartDate               time.Time         `json:"start_date"`
	EndDate                 *time.Time        `json:"end_date,omitempty"`
	SignedDate              *time.Time        `json:"signed_date,omitempty"`
	Value                   float64           `json:"value"`
	EffectiveAmount         float64           `json:"effective_amount"` // value + Σ amendment deltas
	PaidTotal               float64           `json:"paid_total"`
	Outstanding             float64           `json:"outstanding"`
	Currency                string            `json:"currency"`
	Terms                   *string           `json:"terms,omitempty"`
	Description             *string           `json:"description,omitempty"`
	AutoRenewal             bool              `json:"auto_renewal"`
	RenewalTermDays         int               `json:"renewal_term_days"`
	Notes                   *string           `json:"notes,omitempty"`
	ResponsibleEmployeeID   *uuid.UUID        `json:"responsible_employee_id,omitempty"`
	ResponsibleEmployeeName string            `json:"responsible_employee_name,omitempty"`
	DaysToExpiry            *int              `json:"days_to_expiry,omitempty"` // nil = muddatsiz
	// days_until_expiry is the same value under the name the mobile client
	// reads. Both are emitted because days_to_expiry already ships and the web
	// may bind to it; one of the two would silently blank a client.
	DaysUntilExpiry *int `json:"days_until_expiry,omitempty"`
	// EffectiveStatus applies "active + past end_date reads as expired". Status
	// keeps the stored value.
	EffectiveStatus string     `json:"effective_status"`
	AmendmentCount  int        `json:"amendment_count"`
	FileCount       int        `json:"file_count"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
	CreatedBy       *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ContractAmendment — ilova / qo'shimcha kelishuv (child of a contract).
type ContractAmendment struct {
	ID          uuid.UUID  `json:"id"`
	ContractID  uuid.UUID  `json:"contract_id"`
	Number      string     `json:"number"`
	Date        time.Time  `json:"date"`
	AmountDelta *float64   `json:"amount_delta,omitempty"`
	Description *string    `json:"description,omitempty"`
	FileID      *string    `json:"file_id,omitempty"`
	FileName    *string    `json:"file_name,omitempty"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ContractFile — one immutable version of the contract document.
type ContractFile struct {
	ID           uuid.UUID  `json:"id"`
	ContractID   uuid.UUID  `json:"contract_id"`
	Version      int        `json:"version"`
	FileID       string     `json:"file_id"`
	OriginalName string     `json:"original_name"`
	FileSize     int64      `json:"file_size"`
	MimeType     *string    `json:"mime_type,omitempty"`
	HasAISummary bool       `json:"has_ai_summary"`
	UploadedBy   *uuid.UUID `json:"uploaded_by,omitempty"`
	UploadedAt   time.Time  `json:"uploaded_at"`
}

// ContractLink — polymorphic link to another module's record.
type ContractLink struct {
	ID           uuid.UUID `json:"id"`
	ContractID   uuid.UUID `json:"contract_id"`
	LinkedModule string    `json:"linked_module"` // crm_deal | construction_object | purchase_order | sale_order
	LinkedID     string    `json:"linked_id"`
	LinkedTitle  string    `json:"linked_title,omitempty"` // resolved display name
	CreatedAt    time.Time `json:"created_at"`
}

// ContractInvoiceRow — one invoice in the contract's payments section.
type ContractInvoiceRow struct {
	ID            uuid.UUID `json:"id"`
	Kind          string    `json:"kind"` // sales | purchase
	InvoiceNumber string    `json:"invoice_number"`
	InvoiceDate   time.Time `json:"invoice_date"`
	DueDate       time.Time `json:"due_date"`
	TotalAmount   float64   `json:"total_amount"`
	AmountPaid    float64   `json:"amount_paid"`
	AmountDue     float64   `json:"amount_due"`
	Status        string    `json:"status"`
}

// ContractStats — stat-card row for the registry page.
type ContractStats struct {
	Total            int     `json:"total"`
	Active           int     `json:"active"`
	ExpiringSoon     int     `json:"expiring_soon"` // active, ends within 30 days
	ActiveTotalValue float64 `json:"active_total_value"`
	Outstanding      float64 `json:"outstanding"`
}
