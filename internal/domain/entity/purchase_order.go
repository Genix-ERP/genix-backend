package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PurchaseOrderStatus represents the status of a purchase order
type PurchaseOrderStatus string

const (
	POStatusDraft           PurchaseOrderStatus = "draft"
	POStatusPendingApproval PurchaseOrderStatus = "pending_approval"
	POStatusApproved        PurchaseOrderStatus = "approved"
	POStatusOrdered         PurchaseOrderStatus = "ordered"
	POStatusPartial         PurchaseOrderStatus = "partial"
	POStatusReceived        PurchaseOrderStatus = "received"
	POStatusCancelled       PurchaseOrderStatus = "cancelled"
)

// PurchaseOrder represents a purchase order
type PurchaseOrder struct {
	ID              uuid.UUID           `json:"id" db:"id"`
	TenantID        uuid.UUID           `json:"tenant_id" db:"tenant_id"`
	OrderNumber     string              `json:"order_number" db:"order_number"`
	VendorID        uuid.UUID           `json:"vendor_id" db:"vendor_id"`
	ContactPersonID *uuid.UUID          `json:"contact_person_id,omitempty" db:"contact_person_id"`
	OrderDate       time.Time           `json:"order_date" db:"order_date"`
	ExpectedDate    *time.Time          `json:"expected_date,omitempty" db:"expected_date"`
	CurrencyID      *uuid.UUID          `json:"currency_id,omitempty" db:"currency_id"`
	ExchangeRate    float64             `json:"exchange_rate" db:"exchange_rate"`
	Subtotal        float64             `json:"subtotal" db:"subtotal"`
	DiscountAmount  float64             `json:"discount_amount" db:"discount_amount"`
	TaxAmount       float64             `json:"tax_amount" db:"tax_amount"`
	ShippingAmount  float64             `json:"shipping_amount" db:"shipping_amount"`
	TotalAmount     float64             `json:"total_amount" db:"total_amount"`
	Status          PurchaseOrderStatus `json:"status" db:"status"`
	PaymentStatus   PaymentStatus       `json:"payment_status" db:"payment_status"`
	PaymentTerms    *int                `json:"payment_terms,omitempty" db:"payment_terms"`
	References      json.RawMessage     `json:"references" db:"references"`
	VendorReference *string             `json:"vendor_reference,omitempty" db:"vendor_reference"`
	Notes           *string             `json:"notes,omitempty" db:"notes"`
	InternalNotes   *string             `json:"internal_notes,omitempty" db:"internal_notes"`
	WarehouseID      *uuid.UUID          `json:"warehouse_id,omitempty" db:"warehouse_id"`
	VehicleNumber    *string             `json:"vehicle_number,omitempty" db:"vehicle_number"`
	RequiresShipping bool                `json:"requires_shipping" db:"requires_shipping"`
	RequestedBy      *uuid.UUID          `json:"requested_by,omitempty" db:"requested_by"`
	ApprovedBy      *uuid.UUID          `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt      *time.Time          `json:"approved_at,omitempty" db:"approved_at"`
	CreatedAt       time.Time           `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime        `json:"-" db:"deleted_at"`

	// Relationships
	VendorName string              `json:"vendor_name,omitempty"`
	Lines      []PurchaseOrderLine `json:"lines,omitempty"`
}

// PurchaseOrderLine represents a line item in a purchase order
type PurchaseOrderLine struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	PurchaseOrderID  uuid.UUID  `json:"purchase_order_id" db:"purchase_order_id"`
	LineNumber       int        `json:"line_number" db:"line_number"`
	ProductID        *uuid.UUID `json:"product_id,omitempty" db:"product_id"`
	Description      string     `json:"description" db:"description"`
	Quantity         float64    `json:"quantity" db:"quantity"`
	UnitID           *uuid.UUID `json:"unit_id,omitempty" db:"unit_id"`
	UnitPrice        float64    `json:"unit_price" db:"unit_price"`
	DiscountAmount   float64    `json:"discount_amount" db:"discount_amount"`
	TaxID            *uuid.UUID `json:"tax_id,omitempty" db:"tax_id"`
	TaxAmount        float64    `json:"tax_amount" db:"tax_amount"`
	LineTotal        float64    `json:"line_total" db:"line_total"`
	QuantityReceived float64    `json:"quantity_received" db:"quantity_received"`
	QuantityInvoiced float64    `json:"quantity_invoiced" db:"quantity_invoiced"`
	WarehouseID      *uuid.UUID `json:"warehouse_id,omitempty" db:"warehouse_id"`
	Notes            *string    `json:"notes,omitempty" db:"notes"`
	PackagingID      *uuid.UUID `json:"packaging_id,omitempty" db:"packaging_id"`
	PackagingQty     *float64   `json:"packaging_qty,omitempty" db:"packaging_qty"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`

	// Computed
	ProductName string `json:"product_name,omitempty"`
	UnitName    string `json:"unit_name,omitempty"`
	AltName     string `json:"alt_name,omitempty"` // counterparty's product name (cross-org search_key match)

	// Relationships
	Packaging *ProductPackaging `json:"packaging,omitempty"`
}

// CreatePurchaseOrderInput represents input for creating a purchase order
type CreatePurchaseOrderInput struct {
	OrderNumber     string                         `json:"order_number,omitempty"`
	VendorID        string                         `json:"vendor_id" binding:"required"`
	ContactPersonID string                         `json:"contact_person_id,omitempty"`
	OrderDate       string                         `json:"order_date,omitempty"`
	ExpectedDate    string                         `json:"expected_date,omitempty"`
	CurrencyID      string                         `json:"currency_id,omitempty"`
	ExchangeRate    float64                        `json:"exchange_rate,omitempty"`
	PaymentTerms    string                         `json:"payment_terms,omitempty"`
	VendorReference string                         `json:"vendor_reference,omitempty"`
	Notes           string                         `json:"notes,omitempty"`
	InternalNotes   string                         `json:"internal_notes,omitempty"`
	WarehouseID      string                         `json:"warehouse_id,omitempty"`
	VehicleNumber    string                         `json:"vehicle_number,omitempty"`
	RequiresShipping *bool                          `json:"requires_shipping,omitempty"`
	ShippingAmount   float64                        `json:"shipping_amount,omitempty"`
	Subtotal         float64                        `json:"subtotal,omitempty"`
	TaxAmount        float64                        `json:"tax_amount,omitempty"`
	TotalAmount      float64                        `json:"total_amount,omitempty"`
	Lines            []CreatePurchaseOrderLineInput `json:"lines" binding:"required,min=1"`
}

// CreatePurchaseOrderLineInput represents input for a line item
type CreatePurchaseOrderLineInput struct {
	ProductID      string   `json:"product_id,omitempty"`
	Description    string   `json:"description" binding:"required"`
	Quantity       float64  `json:"quantity" binding:"required,gt=0"`
	UnitID         string   `json:"unit_id,omitempty"`
	UnitPrice      float64  `json:"unit_price" binding:"required,gte=0"`
	DiscountAmount float64  `json:"discount_amount,omitempty"`
	TaxID          string   `json:"tax_id,omitempty"`
	TaxPercent     float64  `json:"tax_percent,omitempty"`
	WarehouseID    string   `json:"warehouse_id,omitempty"`
	Notes          string   `json:"notes,omitempty"`
	PackagingID    string   `json:"packaging_id,omitempty"`
	PackagingQty   *float64 `json:"packaging_qty,omitempty"`
}

// UpdatePurchaseOrderInput represents input for updating a purchase order
type UpdatePurchaseOrderInput struct {
	VendorID        *string                        `json:"vendor_id,omitempty"`
	ContactPersonID *string                        `json:"contact_person_id,omitempty"`
	ExpectedDate    *string                        `json:"expected_date,omitempty"`
	PaymentTerms    *string                        `json:"payment_terms,omitempty"`
	VendorReference *string                        `json:"vendor_reference,omitempty"`
	Notes           *string                        `json:"notes,omitempty"`
	InternalNotes   *string                        `json:"internal_notes,omitempty"`
	WarehouseID      *string                        `json:"warehouse_id,omitempty"`
	VehicleNumber    *string                        `json:"vehicle_number,omitempty"`
	RequiresShipping *bool                          `json:"requires_shipping,omitempty"`
	ShippingAmount   *float64                       `json:"shipping_amount,omitempty"`
	Status           *string                        `json:"status,omitempty"`
	Lines           []CreatePurchaseOrderLineInput `json:"lines,omitempty"`
}

// ReceivePurchaseOrderInput represents input for receiving a purchase order
type ReceivePurchaseOrderInput struct {
	Lines []ReceiveLineInput `json:"lines" binding:"required,min=1"`
	Notes string             `json:"notes,omitempty"`
}

// ReceiveLineInput represents input for receiving a line
type ReceiveLineInput struct {
	LineID           string  `json:"line_id" binding:"required"`
	QuantityReceived float64 `json:"quantity_received" binding:"required,gt=0"`
}

// PurchaseOrderListFilter represents filters for listing purchase orders
type PurchaseOrderListFilter struct {
	Search        string              `form:"search"`
	Status        PurchaseOrderStatus `form:"status"`
	PaymentStatus PaymentStatus       `form:"payment_status"`
	VendorID      string              `form:"vendor_id"`
	DateFrom      string              `form:"date_from"`
	DateTo        string              `form:"date_to"`
}

// PurchaseOrderResponse represents the API response for a purchase order
type PurchaseOrderResponse struct {
	ID              uuid.UUID           `json:"id"`
	OrderNumber     string              `json:"order_number"`
	VendorID        uuid.UUID           `json:"vendor_id"`
	VendorName      string              `json:"vendor_name"`
	ContactPersonID *uuid.UUID          `json:"contact_person_id,omitempty"`
	OrderDate       time.Time           `json:"order_date"`
	ExpectedDate    *time.Time          `json:"expected_date,omitempty"`
	Subtotal        float64             `json:"subtotal"`
	DiscountAmount  float64             `json:"discount_amount"`
	TaxAmount       float64             `json:"tax_amount"`
	ShippingAmount  float64             `json:"shipping_amount"`
	TotalAmount     float64             `json:"total_amount"`
	Status          PurchaseOrderStatus `json:"status"`
	PaymentStatus   PaymentStatus       `json:"payment_status"`
	PaymentTerms    *int                `json:"payment_terms,omitempty"`
	VendorReference  *string             `json:"vendor_reference,omitempty"`
	Notes            *string             `json:"notes,omitempty"`
	VehicleNumber    *string             `json:"vehicle_number,omitempty"`
	RequiresShipping bool                `json:"requires_shipping"`
	Lines            []PurchaseOrderLine `json:"lines,omitempty"`
	ApprovedAt       *time.Time          `json:"approved_at,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

// CreateDebitNoteInput represents input for creating a debit note from a purchase invoice
type CreateDebitNoteInput struct {
	Reason        string               `json:"reason" binding:"required"`
	DebitNoteDate string               `json:"debit_note_date"` // defaults to today
	Lines         []DebitNoteLineInput `json:"lines"`           // optional: partial debit; if empty, full reversal
}

// DebitNoteLineInput represents a line in a partial debit note
type DebitNoteLineInput struct {
	ProductID   *string `json:"product_id"`
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity" binding:"required,gt=0"`
	UnitPrice   float64 `json:"unit_price" binding:"required,gte=0"`
	TaxID       *string `json:"tax_id"`
}
