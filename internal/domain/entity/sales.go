package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// OrderStatus represents the status of a sales order
type OrderStatus string

const (
	OrderStatusDraft      OrderStatus = "draft"
	OrderStatusConfirmed  OrderStatus = "confirmed"
	OrderStatusProcessing OrderStatus = "processing"
	OrderStatusShipped    OrderStatus = "shipped"
	OrderStatusDelivered  OrderStatus = "delivered"
	OrderStatusCancelled  OrderStatus = "cancelled"
)

// PaymentStatus represents the payment status
type PaymentStatus string

const (
	PaymentStatusUnpaid  PaymentStatus = "unpaid"
	PaymentStatusPartial PaymentStatus = "partial"
	PaymentStatusPaid    PaymentStatus = "paid"
)

// InvoiceStatus represents the status of an invoice
type InvoiceStatus string

const (
	InvoiceStatusDraft     InvoiceStatus = "draft"
	InvoiceStatusSent      InvoiceStatus = "sent"
	InvoiceStatusPartial   InvoiceStatus = "partial"
	InvoiceStatusPaid      InvoiceStatus = "paid"
	InvoiceStatusOverdue   InvoiceStatus = "overdue"
	InvoiceStatusCancelled InvoiceStatus = "cancelled"
)

// SalesOrder represents a sales order
type SalesOrder struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	TenantID        uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	OrganizationID  *uuid.UUID      `json:"organization_id,omitempty" db:"organization_id"`
	OrderNumber     string          `json:"order_number" db:"order_number"`
	CustomerID      uuid.UUID       `json:"customer_id" db:"customer_id"`
	ContactPersonID *uuid.UUID      `json:"contact_person_id,omitempty" db:"contact_person_id"`
	OrderDate       time.Time       `json:"order_date" db:"order_date"`
	ExpectedDate    *time.Time      `json:"expected_date,omitempty" db:"expected_date"`
	BillingAddress  json.RawMessage `json:"billing_address" db:"billing_address"`
	ShippingAddress json.RawMessage `json:"shipping_address" db:"shipping_address"`
	CurrencyID      *uuid.UUID      `json:"currency_id,omitempty" db:"currency_id"`
	ExchangeRate    float64         `json:"exchange_rate" db:"exchange_rate"`
	Subtotal        float64         `json:"subtotal" db:"subtotal"`
	DiscountType    *string         `json:"discount_type,omitempty" db:"discount_type"`
	DiscountValue   float64         `json:"discount_value" db:"discount_value"`
	DiscountAmount  float64         `json:"discount_amount" db:"discount_amount"`
	TaxAmount       float64         `json:"tax_amount" db:"tax_amount"`
	ShippingAmount  float64         `json:"shipping_amount" db:"shipping_amount"`
	TotalAmount     float64         `json:"total_amount" db:"total_amount"`
	Status          OrderStatus     `json:"status" db:"status"`
	PaymentStatus   PaymentStatus   `json:"payment_status" db:"payment_status"`
	PaymentTerms    int             `json:"payment_terms" db:"payment_terms"`
	Reference       *string         `json:"reference,omitempty" db:"reference"`
	PONumber        *string         `json:"po_number,omitempty" db:"po_number"`
	Notes           *string         `json:"notes,omitempty" db:"notes"`
	InternalNotes   *string         `json:"internal_notes,omitempty" db:"internal_notes"`
	WarehouseID     *uuid.UUID      `json:"warehouse_id,omitempty" db:"warehouse_id"`
	SalesRepID      *uuid.UUID      `json:"sales_rep_id,omitempty" db:"sales_rep_id"`
	ApprovedBy      *uuid.UUID      `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt      *time.Time      `json:"approved_at,omitempty" db:"approved_at"`
	CreatedBy       *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime    `json:"-" db:"deleted_at"`

	// Relationships
	Customer *Contact          `json:"customer,omitempty"`
	Lines    []SalesOrderLine  `json:"lines,omitempty"`
}

// SalesOrderLine represents a line item in a sales order
type SalesOrderLine struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	SalesOrderID      uuid.UUID  `json:"sales_order_id" db:"sales_order_id"`
	LineNumber        int        `json:"line_number" db:"line_number"`
	ProductID         uuid.UUID  `json:"product_id" db:"product_id"`
	Description       *string    `json:"description,omitempty" db:"description"`
	Quantity          float64    `json:"quantity" db:"quantity"`
	UnitID            *uuid.UUID `json:"unit_id,omitempty" db:"unit_id"`
	UnitPrice         float64    `json:"unit_price" db:"unit_price"`
	DiscountType      *string    `json:"discount_type,omitempty" db:"discount_type"`
	DiscountValue     float64    `json:"discount_value" db:"discount_value"`
	DiscountAmount    float64    `json:"discount_amount" db:"discount_amount"`
	TaxID             *uuid.UUID `json:"tax_id,omitempty" db:"tax_id"`
	TaxAmount         float64    `json:"tax_amount" db:"tax_amount"`
	LineTotal         float64    `json:"line_total" db:"line_total"`
	QuantityDelivered float64    `json:"quantity_delivered" db:"quantity_delivered"`
	QuantityInvoiced  float64    `json:"quantity_invoiced" db:"quantity_invoiced"`
	WarehouseID       *uuid.UUID `json:"warehouse_id,omitempty" db:"warehouse_id"`
	Notes             *string    `json:"notes,omitempty" db:"notes"`
	PackagingID       *uuid.UUID `json:"packaging_id,omitempty" db:"packaging_id"`
	PackagingQty      *float64   `json:"packaging_qty,omitempty" db:"packaging_qty"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Product   *Product          `json:"product,omitempty"`
	Packaging *ProductPackaging `json:"packaging,omitempty"`
}

// SalesInvoice represents a sales invoice
type SalesInvoice struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	TenantID        uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	OrganizationID  *uuid.UUID      `json:"organization_id,omitempty" db:"organization_id"`
	InvoiceNumber   string          `json:"invoice_number" db:"invoice_number"`
	CustomerID      uuid.UUID       `json:"customer_id" db:"customer_id"`
	SalesOrderID    *uuid.UUID      `json:"sales_order_id,omitempty" db:"sales_order_id"`
	InvoiceDate     time.Time       `json:"invoice_date" db:"invoice_date"`
	DueDate         time.Time       `json:"due_date" db:"due_date"`
	BillingAddress  json.RawMessage `json:"billing_address" db:"billing_address"`
	ShippingAddress json.RawMessage `json:"shipping_address" db:"shipping_address"`
	CurrencyID      *uuid.UUID      `json:"currency_id,omitempty" db:"currency_id"`
	ExchangeRate    float64         `json:"exchange_rate" db:"exchange_rate"`
	Subtotal        float64         `json:"subtotal" db:"subtotal"`
	DiscountAmount  float64         `json:"discount_amount" db:"discount_amount"`
	TaxAmount       float64         `json:"tax_amount" db:"tax_amount"`
	TotalAmount     float64         `json:"total_amount" db:"total_amount"`
	AmountPaid      float64         `json:"amount_paid" db:"amount_paid"`
	AmountDue       float64         `json:"amount_due" db:"amount_due"`
	Status          InvoiceStatus   `json:"status" db:"status"`
	Reference       *string         `json:"reference,omitempty" db:"reference"`
	PONumber        *string         `json:"po_number,omitempty" db:"po_number"`
	Notes           *string         `json:"notes,omitempty" db:"notes"`
	TermsConditions *string         `json:"terms_conditions,omitempty" db:"terms_conditions"`
	JournalEntryID  *uuid.UUID      `json:"journal_entry_id,omitempty" db:"journal_entry_id"`
	SentAt          *time.Time      `json:"sent_at,omitempty" db:"sent_at"`
	ViewedAt        *time.Time      `json:"viewed_at,omitempty" db:"viewed_at"`
	CreatedBy       *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime    `json:"-" db:"deleted_at"`

	// Relationships
	Customer   *Contact             `json:"customer,omitempty"`
	SalesOrder *SalesOrder          `json:"sales_order,omitempty"`
	Lines      []SalesInvoiceLine   `json:"lines,omitempty"`
}

// SalesInvoiceLine represents a line item in a sales invoice
type SalesInvoiceLine struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	SalesInvoiceID   uuid.UUID  `json:"sales_invoice_id" db:"sales_invoice_id"`
	SalesOrderLineID *uuid.UUID `json:"sales_order_line_id,omitempty" db:"sales_order_line_id"`
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
	AccountID        *uuid.UUID `json:"account_id,omitempty" db:"account_id"`
	PackagingID      *uuid.UUID `json:"packaging_id,omitempty" db:"packaging_id"`
	PackagingQty     *float64   `json:"packaging_qty,omitempty" db:"packaging_qty"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`

	// Relationships
	Product   *Product          `json:"product,omitempty"`
	Packaging *ProductPackaging `json:"packaging,omitempty"`
}

// CreateSalesOrderInput represents input for creating a sales order
type CreateSalesOrderInput struct {
	CustomerID      string                      `json:"customer_id" binding:"required"`
	ContactPersonID string                      `json:"contact_person_id,omitempty"`
	OrderDate       string                      `json:"order_date" binding:"required"`
	ExpectedDate    string                      `json:"expected_date,omitempty"`
	BillingAddress  *Address                    `json:"billing_address,omitempty"`
	ShippingAddress *Address                    `json:"shipping_address,omitempty"`
	CurrencyID      string                      `json:"currency_id,omitempty"`
	DiscountType    string                      `json:"discount_type,omitempty"`
	DiscountValue   float64                     `json:"discount_value,omitempty"`
	ShippingAmount  float64                     `json:"shipping_amount,omitempty"`
	PaymentTerms    int                         `json:"payment_terms,omitempty"`
	Reference       string                      `json:"reference,omitempty"`
	PONumber        string                      `json:"po_number,omitempty"`
	Notes           string                      `json:"notes,omitempty"`
	InternalNotes   string                      `json:"internal_notes,omitempty"`
	WarehouseID     string                      `json:"warehouse_id,omitempty"`
	SalesRepID      string                      `json:"sales_rep_id,omitempty"`
	Lines           []CreateSalesOrderLineInput `json:"lines" binding:"required,min=1"`
}

// CreateSalesOrderLineInput represents input for creating a sales order line
type CreateSalesOrderLineInput struct {
	ProductID     string   `json:"product_id" binding:"required"`
	Description   string   `json:"description,omitempty"`
	Quantity      float64  `json:"quantity" binding:"required,gt=0"`
	UnitID        string   `json:"unit_id,omitempty"`
	UnitPrice     float64  `json:"unit_price" binding:"required,gte=0"`
	DiscountType  string   `json:"discount_type,omitempty"`
	DiscountValue float64  `json:"discount_value,omitempty"`
	TaxID         string   `json:"tax_id,omitempty"`
	WarehouseID   string   `json:"warehouse_id,omitempty"`
	Notes         string   `json:"notes,omitempty"`
	PackagingID   string   `json:"packaging_id,omitempty"`
	PackagingQty  *float64 `json:"packaging_qty,omitempty"`
}

// UpdateSalesOrderInput represents input for updating a sales order
type UpdateSalesOrderInput struct {
	ContactPersonID *string  `json:"contact_person_id,omitempty"`
	ExpectedDate    *string  `json:"expected_date,omitempty"`
	BillingAddress  *Address `json:"billing_address,omitempty"`
	ShippingAddress *Address `json:"shipping_address,omitempty"`
	DiscountType    *string  `json:"discount_type,omitempty"`
	DiscountValue   *float64 `json:"discount_value,omitempty"`
	ShippingAmount  *float64 `json:"shipping_amount,omitempty"`
	PaymentTerms    *int     `json:"payment_terms,omitempty"`
	Reference       *string  `json:"reference,omitempty"`
	PONumber        *string  `json:"po_number,omitempty"`
	Notes           *string  `json:"notes,omitempty"`
	InternalNotes   *string  `json:"internal_notes,omitempty"`
	WarehouseID     *string  `json:"warehouse_id,omitempty"`
	Carrier         *string  `json:"carrier,omitempty"`
	SalesRepID      *string  `json:"sales_rep_id,omitempty"`
	Status          *string  `json:"status,omitempty"`
	PaymentStatus   *string  `json:"payment_status,omitempty"`
}

// SalesOrderListFilter represents filters for listing sales orders
type SalesOrderListFilter struct {
	Search        string        `form:"search"`
	CustomerID    string        `form:"customer_id"`
	Status        OrderStatus   `form:"status"`
	PaymentStatus PaymentStatus `form:"payment_status"`
	DateFrom      string        `form:"date_from"`
	DateTo        string        `form:"date_to"`
	MinAmount     *float64      `form:"min_amount"`
	MaxAmount     *float64      `form:"max_amount"`
	SalesRepID    string        `form:"sales_rep_id"`
}

// CreateSalesInvoiceInput represents input for creating a sales invoice
type CreateSalesInvoiceInput struct {
	CustomerID      string                        `json:"customer_id" binding:"required"`
	SalesOrderID    string                        `json:"sales_order_id,omitempty"`
	InvoiceDate     string                        `json:"invoice_date" binding:"required"`
	DueDate         string                        `json:"due_date" binding:"required"`
	BillingAddress  *Address                      `json:"billing_address,omitempty"`
	ShippingAddress *Address                      `json:"shipping_address,omitempty"`
	CurrencyID      string                        `json:"currency_id,omitempty"`
	Reference       string                        `json:"reference,omitempty"`
	PONumber        string                        `json:"po_number,omitempty"`
	Notes           string                        `json:"notes,omitempty"`
	TermsConditions string                        `json:"terms_conditions,omitempty"`
	Lines           []CreateSalesInvoiceLineInput `json:"lines" binding:"required,min=1"`
}

// CreateSalesInvoiceLineInput represents input for creating an invoice line
type CreateSalesInvoiceLineInput struct {
	SalesOrderLineID string  `json:"sales_order_line_id,omitempty"`
	ProductID        string  `json:"product_id,omitempty"`
	Description      string  `json:"description" binding:"required"`
	Quantity         float64 `json:"quantity" binding:"required,gt=0"`
	UnitID           string  `json:"unit_id,omitempty"`
	UnitPrice        float64 `json:"unit_price" binding:"required,gte=0"`
	DiscountAmount   float64 `json:"discount_amount,omitempty"`
	TaxID            string  `json:"tax_id,omitempty"`
	AccountID        string  `json:"account_id,omitempty"`
}

// SalesInvoiceListFilter represents filters for listing invoices
type SalesInvoiceListFilter struct {
	Search     string        `form:"search"`
	CustomerID string        `form:"customer_id"`
	Status     InvoiceStatus `form:"status"`
	DateFrom   string        `form:"date_from"`
	DateTo     string        `form:"date_to"`
	DueFrom    string        `form:"due_from"`
	DueTo      string        `form:"due_to"`
	MinAmount  *float64      `form:"min_amount"`
	MaxAmount  *float64      `form:"max_amount"`
	Overdue    *bool         `form:"overdue"`
}
