package entity

import (
	"time"

	"github.com/google/uuid"
)

// =====================================================
// INTERCOMPANY TRANSFER STATUS
// =====================================================

type IntercompanyTransferStatus string

const (
	ICTransferStatusDraft           IntercompanyTransferStatus = "draft"
	ICTransferStatusPendingApproval IntercompanyTransferStatus = "pending_approval"
	ICTransferStatusApproved        IntercompanyTransferStatus = "approved"
	ICTransferStatusInTransit       IntercompanyTransferStatus = "in_transit"
	ICTransferStatusReceived        IntercompanyTransferStatus = "received"
	ICTransferStatusInvoiced        IntercompanyTransferStatus = "invoiced"
	ICTransferStatusSettled         IntercompanyTransferStatus = "settled"
	ICTransferStatusCancelled       IntercompanyTransferStatus = "cancelled"
)

type IntercompanyTransferType string

const (
	ICTransferTypeProduct IntercompanyTransferType = "product"
	ICTransferTypeMoney   IntercompanyTransferType = "money"
)

type IntercompanyPricingMethod string

const (
	ICPricingCost       IntercompanyPricingMethod = "cost"
	ICPricingMarkup     IntercompanyPricingMethod = "markup"
	ICPricingListPrice  IntercompanyPricingMethod = "list_price"
	ICPricingNegotiated IntercompanyPricingMethod = "negotiated"
)

// =====================================================
// INTERCOMPANY TRANSFER (Product Transfers)
// =====================================================

// IntercompanyTransfer represents a product transfer between companies
type IntercompanyTransfer struct {
	ID             uuid.UUID                  `json:"id" db:"id"`
	TenantID       uuid.UUID                  `json:"tenant_id" db:"tenant_id"`
	TransferNumber string                     `json:"transfer_number" db:"transfer_number"`
	TransferType   IntercompanyTransferType   `json:"transfer_type" db:"transfer_type"`

	// Organizations
	FromOrganizationID uuid.UUID `json:"from_organization_id" db:"from_organization_id"`
	ToOrganizationID   uuid.UUID `json:"to_organization_id" db:"to_organization_id"`

	// Warehouses
	FromWarehouseID *uuid.UUID `json:"from_warehouse_id,omitempty" db:"from_warehouse_id"`
	ToWarehouseID   *uuid.UUID `json:"to_warehouse_id,omitempty" db:"to_warehouse_id"`

	// Dates
	TransferDate time.Time  `json:"transfer_date" db:"transfer_date"`
	ExpectedDate *time.Time `json:"expected_date,omitempty" db:"expected_date"`
	ReceivedDate *time.Time `json:"received_date,omitempty" db:"received_date"`

	// Status
	Status IntercompanyTransferStatus `json:"status" db:"status"`

	// Pricing
	PricingMethod IntercompanyPricingMethod `json:"pricing_method" db:"pricing_method"`
	MarkupPercent float64                   `json:"markup_percent" db:"markup_percent"`

	// Currency
	CurrencyID   *uuid.UUID `json:"currency_id,omitempty" db:"currency_id"`
	ExchangeRate float64    `json:"exchange_rate" db:"exchange_rate"`

	// Totals
	TotalAmount   float64 `json:"total_amount" db:"total_amount"`
	TotalQuantity float64 `json:"total_quantity" db:"total_quantity"`

	// Related documents
	TransferOrderID *uuid.UUID `json:"transfer_order_id,omitempty" db:"transfer_order_id"`

	// Invoicing
	FromOrgSalesInvoiceID    *uuid.UUID `json:"from_org_sales_invoice_id,omitempty" db:"from_org_sales_invoice_id"`
	ToOrgPurchaseInvoiceID   *uuid.UUID `json:"to_org_purchase_invoice_id,omitempty" db:"to_org_purchase_invoice_id"`

	// Notes
	Reason        *string `json:"reason,omitempty" db:"reason"`
	Notes         *string `json:"notes,omitempty" db:"notes"`
	InternalNotes *string `json:"internal_notes,omitempty" db:"internal_notes"`

	// Audit
	CreatedBy  *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	ApprovedBy *uuid.UUID `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt *time.Time `json:"approved_at,omitempty" db:"approved_at"`
	ShippedBy  *uuid.UUID `json:"shipped_by,omitempty" db:"shipped_by"`
	ShippedAt  *time.Time `json:"shipped_at,omitempty" db:"shipped_at"`
	ReceivedBy *uuid.UUID `json:"received_by,omitempty" db:"received_by"`
	ReceivedAt *time.Time `json:"received_at,omitempty" db:"received_at"`

	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"-" db:"deleted_at"`

	// Relationships
	Lines            []IntercompanyTransferLine `json:"lines,omitempty"`
	FromOrganization *Organization              `json:"from_organization,omitempty"`
	ToOrganization   *Organization              `json:"to_organization,omitempty"`
	FromWarehouse    *Warehouse                 `json:"from_warehouse,omitempty"`
	ToWarehouse      *Warehouse                 `json:"to_warehouse,omitempty"`
}

// IntercompanyTransferLine represents a line item in an IC transfer
type IntercompanyTransferLine struct {
	ID                    uuid.UUID  `json:"id" db:"id"`
	IntercompanyTransferID uuid.UUID `json:"intercompany_transfer_id" db:"intercompany_transfer_id"`

	// Product
	ProductID        uuid.UUID  `json:"product_id" db:"product_id"`
	ProductVariantID *uuid.UUID `json:"product_variant_id,omitempty" db:"product_variant_id"`

	// Locations
	FromLocationID *uuid.UUID `json:"from_location_id,omitempty" db:"from_location_id"`
	ToLocationID   *uuid.UUID `json:"to_location_id,omitempty" db:"to_location_id"`

	// Lot tracking
	LotID        *uuid.UUID `json:"lot_id,omitempty" db:"lot_id"`
	LotNumber    *string    `json:"lot_number,omitempty" db:"lot_number"`
	SerialNumber *string    `json:"serial_number,omitempty" db:"serial_number"`

	// Quantities
	QuantityRequested float64 `json:"quantity_requested" db:"quantity_requested"`
	QuantityShipped   float64 `json:"quantity_shipped" db:"quantity_shipped"`
	QuantityReceived  float64 `json:"quantity_received" db:"quantity_received"`

	// Pricing
	UnitCost      float64 `json:"unit_cost" db:"unit_cost"`
	TransferPrice float64 `json:"transfer_price" db:"transfer_price"`
	LineTotal     float64 `json:"line_total" db:"line_total"`

	Notes *string `json:"notes,omitempty" db:"notes"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// Relationships
	Product *Product `json:"product,omitempty"`
}

// =====================================================
// INTERCOMPANY PAYMENT (Money Transfers)
// =====================================================

type IntercompanyPaymentType string

const (
	ICPaymentTypeTransfer   IntercompanyPaymentType = "transfer"
	ICPaymentTypeSettlement IntercompanyPaymentType = "settlement"
	ICPaymentTypeAdvance    IntercompanyPaymentType = "advance"
)

type IntercompanyPaymentStatus string

const (
	ICPaymentStatusDraft           IntercompanyPaymentStatus = "draft"
	ICPaymentStatusPendingApproval IntercompanyPaymentStatus = "pending_approval"
	ICPaymentStatusApproved        IntercompanyPaymentStatus = "approved"
	ICPaymentStatusCompleted       IntercompanyPaymentStatus = "completed"
	ICPaymentStatusCancelled       IntercompanyPaymentStatus = "cancelled"
)

// IntercompanyPayment represents a money transfer between companies
type IntercompanyPayment struct {
	ID            uuid.UUID               `json:"id" db:"id"`
	TenantID      uuid.UUID               `json:"tenant_id" db:"tenant_id"`
	PaymentNumber string                  `json:"payment_number" db:"payment_number"`
	PaymentType   IntercompanyPaymentType `json:"payment_type" db:"payment_type"`

	// Organizations
	FromOrganizationID uuid.UUID `json:"from_organization_id" db:"from_organization_id"`
	ToOrganizationID   uuid.UUID `json:"to_organization_id" db:"to_organization_id"`

	// Amount
	Amount             float64    `json:"amount" db:"amount"`
	CurrencyID         *uuid.UUID `json:"currency_id,omitempty" db:"currency_id"`
	ExchangeRate       float64    `json:"exchange_rate" db:"exchange_rate"`
	BaseCurrencyAmount *float64   `json:"base_currency_amount,omitempty" db:"base_currency_amount"`

	// Date
	PaymentDate time.Time `json:"payment_date" db:"payment_date"`

	// Status
	Status IntercompanyPaymentStatus `json:"status" db:"status"`

	// Payment method
	PaymentMethod *string `json:"payment_method,omitempty" db:"payment_method"`

	// Bank accounts
	FromBankAccountID *uuid.UUID `json:"from_bank_account_id,omitempty" db:"from_bank_account_id"`
	ToBankAccountID   *uuid.UUID `json:"to_bank_account_id,omitempty" db:"to_bank_account_id"`

	// Reference
	Reference *string `json:"reference,omitempty" db:"reference"`

	// Related documents
	RelatedTransferID *uuid.UUID `json:"related_transfer_id,omitempty" db:"related_transfer_id"`

	// Journal entries
	FromOrgJournalEntryID *uuid.UUID `json:"from_org_journal_entry_id,omitempty" db:"from_org_journal_entry_id"`
	ToOrgJournalEntryID   *uuid.UUID `json:"to_org_journal_entry_id,omitempty" db:"to_org_journal_entry_id"`

	// Notes
	Description *string `json:"description,omitempty" db:"description"`
	Notes       *string `json:"notes,omitempty" db:"notes"`

	// Audit
	CreatedBy   *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	ApprovedBy  *uuid.UUID `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty" db:"approved_at"`
	CompletedBy *uuid.UUID `json:"completed_by,omitempty" db:"completed_by"`
	CompletedAt *time.Time `json:"completed_at,omitempty" db:"completed_at"`

	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"-" db:"deleted_at"`

	// Relationships
	FromOrganization *Organization `json:"from_organization,omitempty"`
	ToOrganization   *Organization `json:"to_organization,omitempty"`
}

// =====================================================
// INTERCOMPANY ACCOUNTS (AR/AP Tracking)
// =====================================================

type IntercompanyAccountType string

const (
	ICAccountTypeReceivable IntercompanyAccountType = "receivable"
	ICAccountTypePayable    IntercompanyAccountType = "payable"
)

// IntercompanyAccount represents AR/AP relationship between two organizations
type IntercompanyAccount struct {
	ID                    uuid.UUID               `json:"id" db:"id"`
	TenantID              uuid.UUID               `json:"tenant_id" db:"tenant_id"`
	OrganizationID        uuid.UUID               `json:"organization_id" db:"organization_id"`
	PartnerOrganizationID uuid.UUID               `json:"partner_organization_id" db:"partner_organization_id"`
	AccountType           IntercompanyAccountType `json:"account_type" db:"account_type"`
	CurrencyID            *uuid.UUID              `json:"currency_id,omitempty" db:"currency_id"`
	Balance               float64                 `json:"balance" db:"balance"`
	LastReconciledAt      *time.Time              `json:"last_reconciled_at,omitempty" db:"last_reconciled_at"`
	LastReconciledBy      *uuid.UUID              `json:"last_reconciled_by,omitempty" db:"last_reconciled_by"`
	CreatedAt             time.Time               `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time               `json:"updated_at" db:"updated_at"`

	// Relationships
	Organization        *Organization `json:"organization,omitempty"`
	PartnerOrganization *Organization `json:"partner_organization,omitempty"`
}

// IntercompanyAccountTransaction represents a transaction on an IC account
type IntercompanyAccountTransaction struct {
	ID                    uuid.UUID  `json:"id" db:"id"`
	IntercompanyAccountID uuid.UUID  `json:"intercompany_account_id" db:"intercompany_account_id"`
	TransactionType       string     `json:"transaction_type" db:"transaction_type"`
	ReferenceType         *string    `json:"reference_type,omitempty" db:"reference_type"`
	ReferenceID           *uuid.UUID `json:"reference_id,omitempty" db:"reference_id"`
	Amount                float64    `json:"amount" db:"amount"`
	BalanceAfter          float64    `json:"balance_after" db:"balance_after"`
	TransactionDate       time.Time  `json:"transaction_date" db:"transaction_date"`
	Description           *string    `json:"description,omitempty" db:"description"`
	CreatedBy             *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
}

// =====================================================
// INPUT/OUTPUT TYPES
// =====================================================

// CreateIntercompanyTransferInput is input for creating an IC transfer
type CreateIntercompanyTransferInput struct {
	FromOrganizationID string  `json:"from_organization_id" binding:"required"`
	ToOrganizationID   string  `json:"to_organization_id" binding:"required"`
	FromWarehouseID    string  `json:"from_warehouse_id" binding:"required"`
	ToWarehouseID      string  `json:"to_warehouse_id" binding:"required"`
	TransferDate       string  `json:"transfer_date" binding:"required"`
	ExpectedDate       string  `json:"expected_date,omitempty"`
	PricingMethod      string  `json:"pricing_method,omitempty"`
	MarkupPercent      float64 `json:"markup_percent,omitempty"`
	CurrencyID         string  `json:"currency_id,omitempty"`
	Reason             string  `json:"reason,omitempty"`
	Notes              string  `json:"notes,omitempty"`
	Lines              []CreateIntercompanyTransferLineInput `json:"lines" binding:"required,min=1"`
}

// CreateIntercompanyTransferLineInput is input for creating an IC transfer line
type CreateIntercompanyTransferLineInput struct {
	ProductID      string  `json:"product_id" binding:"required"`
	FromLocationID string  `json:"from_location_id,omitempty"`
	ToLocationID   string  `json:"to_location_id,omitempty"`
	LotNumber      string  `json:"lot_number,omitempty"`
	SerialNumber   string  `json:"serial_number,omitempty"`
	Quantity       float64 `json:"quantity" binding:"required,gt=0"`
	TransferPrice  float64 `json:"transfer_price,omitempty"`
	Notes          string  `json:"notes,omitempty"`
}

// UpdateIntercompanyTransferInput is input for updating an IC transfer
type UpdateIntercompanyTransferInput struct {
	FromWarehouseID *string  `json:"from_warehouse_id,omitempty"`
	ToWarehouseID   *string  `json:"to_warehouse_id,omitempty"`
	TransferDate    *string  `json:"transfer_date,omitempty"`
	ExpectedDate    *string  `json:"expected_date,omitempty"`
	PricingMethod   *string  `json:"pricing_method,omitempty"`
	MarkupPercent   *float64 `json:"markup_percent,omitempty"`
	Reason          *string  `json:"reason,omitempty"`
	Notes           *string  `json:"notes,omitempty"`
}

// IntercompanyTransferListFilter is the filter for listing IC transfers
type IntercompanyTransferListFilter struct {
	Search             string `form:"search"`
	FromOrganizationID string `form:"from_organization_id"`
	ToOrganizationID   string `form:"to_organization_id"`
	Status             string `form:"status"`
	TransferType       string `form:"transfer_type"`
	DateFrom           string `form:"date_from"`
	DateTo             string `form:"date_to"`
}

// CreateIntercompanyPaymentInput is input for creating an IC payment
type CreateIntercompanyPaymentInput struct {
	FromOrganizationID string  `json:"from_organization_id" binding:"required"`
	ToOrganizationID   string  `json:"to_organization_id" binding:"required"`
	PaymentType        string  `json:"payment_type,omitempty"`
	Amount             float64 `json:"amount" binding:"required,gt=0"`
	CurrencyID         string  `json:"currency_id,omitempty"`
	PaymentDate        string  `json:"payment_date" binding:"required"`
	PaymentMethod      string  `json:"payment_method,omitempty"`
	FromBankAccountID  string  `json:"from_bank_account_id,omitempty"`
	ToBankAccountID    string  `json:"to_bank_account_id,omitempty"`
	Reference          string  `json:"reference,omitempty"`
	RelatedTransferID  string  `json:"related_transfer_id,omitempty"`
	Description        string  `json:"description,omitempty"`
	Notes              string  `json:"notes,omitempty"`
}

// UpdateIntercompanyPaymentInput is input for updating an IC payment
type UpdateIntercompanyPaymentInput struct {
	Amount            *float64 `json:"amount,omitempty"`
	PaymentDate       *string  `json:"payment_date,omitempty"`
	PaymentMethod     *string  `json:"payment_method,omitempty"`
	FromBankAccountID *string  `json:"from_bank_account_id,omitempty"`
	ToBankAccountID   *string  `json:"to_bank_account_id,omitempty"`
	Reference         *string  `json:"reference,omitempty"`
	Description       *string  `json:"description,omitempty"`
	Notes             *string  `json:"notes,omitempty"`
}

// IntercompanyPaymentListFilter is the filter for listing IC payments
type IntercompanyPaymentListFilter struct {
	Search             string `form:"search"`
	FromOrganizationID string `form:"from_organization_id"`
	ToOrganizationID   string `form:"to_organization_id"`
	Status             string `form:"status"`
	PaymentType        string `form:"payment_type"`
	DateFrom           string `form:"date_from"`
	DateTo             string `form:"date_to"`
}

// =====================================================
// RESPONSE TYPES
// =====================================================

// IntercompanyTransferResponse is the API response format
type IntercompanyTransferResponse struct {
	ID                   uuid.UUID                    `json:"id"`
	TransferNumber       string                       `json:"transfer_number"`
	TransferType         string                       `json:"transfer_type"`
	FromOrganizationID   uuid.UUID                    `json:"from_organization_id"`
	FromOrganizationName string                       `json:"from_organization_name,omitempty"`
	ToOrganizationID     uuid.UUID                    `json:"to_organization_id"`
	ToOrganizationName   string                       `json:"to_organization_name,omitempty"`
	FromWarehouseID      *uuid.UUID                   `json:"from_warehouse_id,omitempty"`
	FromWarehouseName    string                       `json:"from_warehouse_name,omitempty"`
	ToWarehouseID        *uuid.UUID                   `json:"to_warehouse_id,omitempty"`
	ToWarehouseName      string                       `json:"to_warehouse_name,omitempty"`
	TransferDate         string                       `json:"transfer_date"`
	ExpectedDate         *string                      `json:"expected_date,omitempty"`
	ReceivedDate         *string                      `json:"received_date,omitempty"`
	Status               string                       `json:"status"`
	PricingMethod        string                       `json:"pricing_method"`
	MarkupPercent        float64                      `json:"markup_percent"`
	TotalAmount          float64                      `json:"total_amount"`
	TotalQuantity        float64                      `json:"total_quantity"`
	Reason               *string                      `json:"reason,omitempty"`
	Notes                *string                      `json:"notes,omitempty"`
	Lines                []IntercompanyTransferLineResponse `json:"lines,omitempty"`
	CreatedAt            time.Time                    `json:"created_at"`
}

// IntercompanyTransferLineResponse is the API response for a line
type IntercompanyTransferLineResponse struct {
	ID                uuid.UUID  `json:"id"`
	ProductID         uuid.UUID  `json:"product_id"`
	ProductCode       string     `json:"product_code,omitempty"`
	ProductName       string     `json:"product_name,omitempty"`
	LotNumber         *string    `json:"lot_number,omitempty"`
	QuantityRequested float64    `json:"quantity_requested"`
	QuantityShipped   float64    `json:"quantity_shipped"`
	QuantityReceived  float64    `json:"quantity_received"`
	UnitCost          float64    `json:"unit_cost"`
	TransferPrice     float64    `json:"transfer_price"`
	LineTotal         float64    `json:"line_total"`
	Notes             *string    `json:"notes,omitempty"`
}

// IntercompanyPaymentResponse is the API response format
type IntercompanyPaymentResponse struct {
	ID                   uuid.UUID `json:"id"`
	PaymentNumber        string    `json:"payment_number"`
	PaymentType          string    `json:"payment_type"`
	FromOrganizationID   uuid.UUID `json:"from_organization_id"`
	FromOrganizationName string    `json:"from_organization_name,omitempty"`
	ToOrganizationID     uuid.UUID `json:"to_organization_id"`
	ToOrganizationName   string    `json:"to_organization_name,omitempty"`
	Amount               float64   `json:"amount"`
	CurrencyID           *uuid.UUID `json:"currency_id,omitempty"`
	CurrencyCode         string    `json:"currency_code,omitempty"`
	PaymentDate          string    `json:"payment_date"`
	Status               string    `json:"status"`
	PaymentMethod        *string   `json:"payment_method,omitempty"`
	Reference            *string   `json:"reference,omitempty"`
	Description          *string   `json:"description,omitempty"`
	Notes                *string   `json:"notes,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

// IntercompanyAccountResponse is the API response for IC accounts
type IntercompanyAccountResponse struct {
	ID                      uuid.UUID `json:"id"`
	OrganizationID          uuid.UUID `json:"organization_id"`
	OrganizationName        string    `json:"organization_name,omitempty"`
	PartnerOrganizationID   uuid.UUID `json:"partner_organization_id"`
	PartnerOrganizationName string    `json:"partner_organization_name,omitempty"`
	AccountType             string    `json:"account_type"`
	Balance                 float64   `json:"balance"`
	CurrencyCode            string    `json:"currency_code,omitempty"`
	LastReconciledAt        *time.Time `json:"last_reconciled_at,omitempty"`
}

// IntercompanyBalanceSummary summarizes balances between organizations
type IntercompanyBalanceSummary struct {
	OrganizationID          uuid.UUID `json:"organization_id"`
	OrganizationName        string    `json:"organization_name"`
	PartnerOrganizationID   uuid.UUID `json:"partner_organization_id"`
	PartnerOrganizationName string    `json:"partner_organization_name"`
	Receivable              float64   `json:"receivable"`
	Payable                 float64   `json:"payable"`
	NetBalance              float64   `json:"net_balance"` // Positive = they owe us
}
