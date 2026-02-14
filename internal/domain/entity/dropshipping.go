package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Dropship Order Statuses
const (
	DropshipStatusPending      = "pending"
	DropshipStatusPOCreated    = "po_created"
	DropshipStatusSentToVendor = "sent_to_vendor"
	DropshipStatusShipped      = "shipped"
	DropshipStatusDelivered    = "delivered"
	DropshipStatusCancelled    = "cancelled"
)

// Dropship Line Statuses
const (
	DropshipLineStatusPending   = "pending"
	DropshipLineStatusOrdered   = "ordered"
	DropshipLineStatusShipped   = "shipped"
	DropshipLineStatusDelivered = "delivered"
	DropshipLineStatusCancelled = "cancelled"
)

// DropshipOrder represents a dropship order linking sales order to vendor PO
type DropshipOrder struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	TenantID            uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	SalesOrderID        uuid.UUID       `json:"sales_order_id" db:"sales_order_id"`
	SalesOrderNumber    string          `json:"sales_order_number" db:"sales_order_number"`
	PurchaseOrderID     *uuid.UUID      `json:"purchase_order_id,omitempty" db:"purchase_order_id"`
	PurchaseOrderNumber string          `json:"purchase_order_number,omitempty" db:"purchase_order_number"`
	VendorID            uuid.UUID       `json:"vendor_id" db:"vendor_id"`
	VendorName          string          `json:"vendor_name" db:"vendor_name"`
	CustomerID          uuid.UUID       `json:"customer_id" db:"customer_id"`
	CustomerName        string          `json:"customer_name" db:"customer_name"`
	ShippingAddress     json.RawMessage `json:"shipping_address" db:"shipping_address" swaggertype:"object"`
	Status              string          `json:"status" db:"status"`
	ShippingMethod      *string         `json:"shipping_method,omitempty" db:"shipping_method"`
	TrackingNumber      *string         `json:"tracking_number,omitempty" db:"tracking_number"`
	Carrier             *string         `json:"carrier,omitempty" db:"carrier"`
	EstimatedDelivery   *time.Time      `json:"estimated_delivery,omitempty" db:"estimated_delivery"`
	ShippedDate         *time.Time      `json:"shipped_date,omitempty" db:"shipped_date"`
	DeliveredDate       *time.Time      `json:"delivered_date,omitempty" db:"delivered_date"`
	Subtotal            float64         `json:"subtotal" db:"subtotal"`
	ShippingCost        float64         `json:"shipping_cost" db:"shipping_cost"`
	TotalCost           float64         `json:"total_cost" db:"total_cost"`
	Margin              float64         `json:"margin" db:"margin"`
	MarginPercent       float64         `json:"margin_percent" db:"margin_percent"`
	VendorNotes         *string         `json:"vendor_notes,omitempty" db:"vendor_notes"`
	InternalNotes       *string         `json:"internal_notes,omitempty" db:"internal_notes"`
	CustomerNotes       *string         `json:"customer_notes,omitempty" db:"customer_notes"`
	CreatedBy           *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`

	// Relationships
	Lines []DropshipOrderLine `json:"lines,omitempty"`
}

// DropshipOrderLine represents a line item in a dropship order
type DropshipOrderLine struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	DropshipOrderID     uuid.UUID  `json:"dropship_order_id" db:"dropship_order_id"`
	SalesOrderLineID    uuid.UUID  `json:"sales_order_line_id" db:"sales_order_line_id"`
	PurchaseOrderLineID *uuid.UUID `json:"purchase_order_line_id,omitempty" db:"purchase_order_line_id"`
	ProductID           *uuid.UUID `json:"product_id,omitempty" db:"product_id"`
	ProductName         string     `json:"product_name" db:"product_name"`
	SKU                 *string    `json:"sku,omitempty" db:"sku"`
	Description         *string    `json:"description,omitempty" db:"description"`
	Quantity            float64    `json:"quantity" db:"quantity"`
	QuantityShipped     float64    `json:"quantity_shipped" db:"quantity_shipped"`
	QuantityDelivered   float64    `json:"quantity_delivered" db:"quantity_delivered"`
	VendorPrice         float64    `json:"vendor_price" db:"vendor_price"`
	SalePrice           float64    `json:"sale_price" db:"sale_price"`
	LineCost            float64    `json:"line_cost" db:"line_cost"`
	LineRevenue         float64    `json:"line_revenue" db:"line_revenue"`
	LineMargin          float64    `json:"line_margin" db:"line_margin"`
	Status              string     `json:"status" db:"status"`
	Notes               *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
}

// DropshipVendorSettings represents vendor-specific dropshipping configuration
type DropshipVendorSettings struct {
	ID                    uuid.UUID  `json:"id" db:"id"`
	TenantID              uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	VendorID              uuid.UUID  `json:"vendor_id" db:"vendor_id"`
	IsDropshipEnabled     bool       `json:"is_dropship_enabled" db:"is_dropship_enabled"`
	AutoSendOrders        bool       `json:"auto_send_orders" db:"auto_send_orders"`
	NotificationEmail     *string    `json:"notification_email,omitempty" db:"notification_email"`
	OrderFormat           string     `json:"order_format" db:"order_format"`
	DefaultLeadTimeDays   int        `json:"default_lead_time_days" db:"default_lead_time_days"`
	ShipsDirectToCustomer bool       `json:"ships_direct_to_customer" db:"ships_direct_to_customer"`
	ProvidesTracking      bool       `json:"provides_tracking" db:"provides_tracking"`
	MarkupType            string     `json:"markup_type" db:"markup_type"`
	DefaultMarkup         float64    `json:"default_markup" db:"default_markup"`
	MinOrderValue         *float64   `json:"min_order_value,omitempty" db:"min_order_value"`
	MinOrderQuantity      *int       `json:"min_order_quantity,omitempty" db:"min_order_quantity"`
	APIEndpoint           *string    `json:"api_endpoint,omitempty" db:"api_endpoint"`
	Notes                 *string    `json:"notes,omitempty" db:"notes"`
	IsActive              bool       `json:"is_active" db:"is_active"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at" db:"updated_at"`

	// Computed/joined fields
	VendorName string `json:"vendor_name,omitempty"`
}

// DropshipProductVendor represents a vendor that can dropship a specific product
type DropshipProductVendor struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	TenantID     uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProductID    uuid.UUID  `json:"product_id" db:"product_id"`
	VendorID     uuid.UUID  `json:"vendor_id" db:"vendor_id"`
	VendorPrice  *float64   `json:"vendor_price,omitempty" db:"vendor_price"`
	Currency     string     `json:"currency" db:"currency"`
	LeadTimeDays int        `json:"lead_time_days" db:"lead_time_days"`
	IsAvailable  bool       `json:"is_available" db:"is_available"`
	MinOrderQty  float64    `json:"min_order_qty" db:"min_order_qty"`
	Priority     int        `json:"priority" db:"priority"`
	VendorSKU    *string    `json:"vendor_sku,omitempty" db:"vendor_sku"`
	IsActive     bool       `json:"is_active" db:"is_active"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`

	// Computed/joined fields
	ProductName string `json:"product_name,omitempty"`
	VendorName  string `json:"vendor_name,omitempty"`
}

// CreateDropshipOrderInput represents input for creating a dropship order
type CreateDropshipOrderInput struct {
	SalesOrderID    string                         `json:"sales_order_id" binding:"required"`
	VendorID        string                         `json:"vendor_id" binding:"required"`
	ShippingMethod  string                         `json:"shipping_method,omitempty"`
	VendorNotes     string                         `json:"vendor_notes,omitempty"`
	InternalNotes   string                         `json:"internal_notes,omitempty"`
	Lines           []CreateDropshipOrderLineInput `json:"lines" binding:"required,min=1"`
	CreatePO        bool                           `json:"create_po"` // Auto-create purchase order
}

// CreateDropshipOrderLineInput represents input for a dropship order line
type CreateDropshipOrderLineInput struct {
	SalesOrderLineID string  `json:"sales_order_line_id" binding:"required"`
	ProductID        string  `json:"product_id,omitempty"`
	Quantity         float64 `json:"quantity" binding:"required,gt=0"`
	VendorPrice      float64 `json:"vendor_price" binding:"required,gte=0"`
	Notes            string  `json:"notes,omitempty"`
}

// UpdateDropshipOrderInput represents input for updating a dropship order
type UpdateDropshipOrderInput struct {
	Status           *string  `json:"status,omitempty"`
	ShippingMethod   *string  `json:"shipping_method,omitempty"`
	TrackingNumber   *string  `json:"tracking_number,omitempty"`
	Carrier          *string  `json:"carrier,omitempty"`
	EstimatedDelivery *string `json:"estimated_delivery,omitempty"`
	VendorNotes      *string  `json:"vendor_notes,omitempty"`
	InternalNotes    *string  `json:"internal_notes,omitempty"`
	ShippingCost     *float64 `json:"shipping_cost,omitempty"`
}

// MarkShippedInput represents input for marking a dropship order as shipped
type MarkShippedInput struct {
	TrackingNumber    string `json:"tracking_number,omitempty"`
	Carrier           string `json:"carrier,omitempty"`
	EstimatedDelivery string `json:"estimated_delivery,omitempty"`
	Notes             string `json:"notes,omitempty"`
}

// CreateDropshipVendorSettingsInput represents input for creating vendor settings
type CreateDropshipVendorSettingsInput struct {
	VendorID              string   `json:"vendor_id" binding:"required"`
	IsDropshipEnabled     *bool    `json:"is_dropship_enabled,omitempty"`
	AutoSendOrders        *bool    `json:"auto_send_orders,omitempty"`
	NotificationEmail     string   `json:"notification_email,omitempty"`
	OrderFormat           string   `json:"order_format,omitempty"`
	DefaultLeadTimeDays   *int     `json:"default_lead_time_days,omitempty"`
	ShipsDirectToCustomer *bool    `json:"ships_direct_to_customer,omitempty"`
	ProvidesTracking      *bool    `json:"provides_tracking,omitempty"`
	MarkupType            string   `json:"markup_type,omitempty"`
	DefaultMarkup         *float64 `json:"default_markup,omitempty"`
	MinOrderValue         *float64 `json:"min_order_value,omitempty"`
	MinOrderQuantity      *int     `json:"min_order_quantity,omitempty"`
	Notes                 string   `json:"notes,omitempty"`
}

// UpdateDropshipVendorSettingsInput represents input for updating vendor settings
type UpdateDropshipVendorSettingsInput struct {
	IsDropshipEnabled     *bool    `json:"is_dropship_enabled,omitempty"`
	AutoSendOrders        *bool    `json:"auto_send_orders,omitempty"`
	NotificationEmail     *string  `json:"notification_email,omitempty"`
	OrderFormat           *string  `json:"order_format,omitempty"`
	DefaultLeadTimeDays   *int     `json:"default_lead_time_days,omitempty"`
	ShipsDirectToCustomer *bool    `json:"ships_direct_to_customer,omitempty"`
	ProvidesTracking      *bool    `json:"provides_tracking,omitempty"`
	MarkupType            *string  `json:"markup_type,omitempty"`
	DefaultMarkup         *float64 `json:"default_markup,omitempty"`
	MinOrderValue         *float64 `json:"min_order_value,omitempty"`
	MinOrderQuantity      *int     `json:"min_order_quantity,omitempty"`
	Notes                 *string  `json:"notes,omitempty"`
	IsActive              *bool    `json:"is_active,omitempty"`
}

// CreateDropshipProductVendorInput represents input for linking product to dropship vendor
type CreateDropshipProductVendorInput struct {
	ProductID    string   `json:"product_id" binding:"required"`
	VendorID     string   `json:"vendor_id" binding:"required"`
	VendorPrice  *float64 `json:"vendor_price,omitempty"`
	Currency     string   `json:"currency,omitempty"`
	LeadTimeDays *int     `json:"lead_time_days,omitempty"`
	MinOrderQty  *float64 `json:"min_order_qty,omitempty"`
	Priority     *int     `json:"priority,omitempty"`
	VendorSKU    string   `json:"vendor_sku,omitempty"`
}

// UpdateDropshipProductVendorInput represents input for updating product-vendor link
type UpdateDropshipProductVendorInput struct {
	VendorPrice  *float64 `json:"vendor_price,omitempty"`
	Currency     *string  `json:"currency,omitempty"`
	LeadTimeDays *int     `json:"lead_time_days,omitempty"`
	IsAvailable  *bool    `json:"is_available,omitempty"`
	MinOrderQty  *float64 `json:"min_order_qty,omitempty"`
	Priority     *int     `json:"priority,omitempty"`
	VendorSKU    *string  `json:"vendor_sku,omitempty"`
	IsActive     *bool    `json:"is_active,omitempty"`
}

// DropshipOrderListFilter represents filters for listing dropship orders
type DropshipOrderListFilter struct {
	Search        string `form:"search"`
	Status        string `form:"status"`
	VendorID      string `form:"vendor_id"`
	CustomerID    string `form:"customer_id"`
	SalesOrderID  string `form:"sales_order_id"`
	DateFrom      string `form:"date_from"`
	DateTo        string `form:"date_to"`
}

// DropshipStats represents dropshipping statistics
type DropshipStats struct {
	TotalOrders     int     `json:"total_orders"`
	PendingOrders   int     `json:"pending_orders"`
	ShippedOrders   int     `json:"shipped_orders"`
	DeliveredOrders int     `json:"delivered_orders"`
	TotalRevenue    float64 `json:"total_revenue"`
	TotalCost       float64 `json:"total_cost"`
	TotalMargin     float64 `json:"total_margin"`
	AvgMarginPct    float64 `json:"avg_margin_pct"`
}

// DropshipableProduct represents a product that can be dropshipped with vendor info
type DropshipableProduct struct {
	ProductID           uuid.UUID  `json:"product_id"`
	ProductName         string     `json:"product_name"`
	SKU                 *string    `json:"sku,omitempty"`
	IsDropshippable     bool       `json:"is_dropshippable"`
	DropshipOnly        bool       `json:"dropship_only"`
	DefaultVendorID     *uuid.UUID `json:"default_vendor_id,omitempty"`
	DefaultVendorName   string     `json:"default_vendor_name,omitempty"`
	AvailableVendors    int        `json:"available_vendors"`
	LowestVendorPrice   *float64   `json:"lowest_vendor_price,omitempty"`
	FastestLeadTimeDays *int       `json:"fastest_lead_time_days,omitempty"`
}
