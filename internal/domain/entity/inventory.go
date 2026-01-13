package entity

import (
	"time"

	"github.com/google/uuid"
)

// Warehouse represents a warehouse
type Warehouse struct {
	ID             uuid.UUID    `json:"id" db:"id"`
	TenantID       uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID   `json:"organization_id,omitempty" db:"organization_id"`
	Code           string       `json:"code" db:"code"`
	Name           string       `json:"name" db:"name"`
	Address        *Address     `json:"address,omitempty" db:"address"`
	ManagerID      *uuid.UUID   `json:"manager_id,omitempty" db:"manager_id"`
	IsDefault      bool         `json:"is_default" db:"is_default"`
	IsActive       bool         `json:"is_active" db:"is_active"`
	CreatedAt      time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at" db:"updated_at"`

	// Relationships
	Locations []WarehouseLocation `json:"locations,omitempty"`
}

// WarehouseLocationType represents the type of warehouse location
type WarehouseLocationType string

const (
	LocationTypeStorage   WarehouseLocationType = "storage"
	LocationTypeReceiving WarehouseLocationType = "receiving"
	LocationTypeShipping  WarehouseLocationType = "shipping"
	LocationTypeStaging   WarehouseLocationType = "staging"
)

// WarehouseLocation represents a location within a warehouse
type WarehouseLocation struct {
	ID          uuid.UUID             `json:"id" db:"id"`
	WarehouseID uuid.UUID             `json:"warehouse_id" db:"warehouse_id"`
	ParentID    *uuid.UUID            `json:"parent_id,omitempty" db:"parent_id"`
	Code        string                `json:"code" db:"code"`
	Name        string                `json:"name" db:"name"`
	Type        WarehouseLocationType `json:"type" db:"type"`
	Aisle       *string               `json:"aisle,omitempty" db:"aisle"`
	Rack        *string               `json:"rack,omitempty" db:"rack"`
	Shelf       *string               `json:"shelf,omitempty" db:"shelf"`
	Bin         *string               `json:"bin,omitempty" db:"bin"`
	Capacity    *float64              `json:"capacity,omitempty" db:"capacity"`
	IsActive    bool                  `json:"is_active" db:"is_active"`
	CreatedAt   time.Time             `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at" db:"updated_at"`
}

// Inventory represents inventory at a specific location
type Inventory struct {
	ID                uuid.UUID    `json:"id" db:"id"`
	TenantID          uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	ProductID         uuid.UUID    `json:"product_id" db:"product_id"`
	WarehouseID       uuid.UUID    `json:"warehouse_id" db:"warehouse_id"`
	LocationID        *uuid.UUID   `json:"location_id,omitempty" db:"location_id"`
	LotNumber         *string      `json:"lot_number,omitempty" db:"lot_number"`
	SerialNumber      *string      `json:"serial_number,omitempty" db:"serial_number"`
	ExpiryDate        *time.Time   `json:"expiry_date,omitempty" db:"expiry_date"`
	QuantityOnHand    float64      `json:"quantity_on_hand" db:"quantity_on_hand"`
	QuantityReserved  float64      `json:"quantity_reserved" db:"quantity_reserved"`
	QuantityAvailable float64      `json:"quantity_available" db:"quantity_available"`
	UnitCost          float64      `json:"unit_cost" db:"unit_cost"`
	TotalValue        float64      `json:"total_value" db:"total_value"`
	LastCountDate     *time.Time   `json:"last_count_date,omitempty" db:"last_count_date"`
	LastMovementDate  *time.Time   `json:"last_movement_date,omitempty" db:"last_movement_date"`
	CreatedAt         time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at" db:"updated_at"`

	// Relationships
	Product   *Product   `json:"product,omitempty"`
	Warehouse *Warehouse `json:"warehouse,omitempty"`
}

// TransactionType represents the type of inventory transaction
type TransactionType string

const (
	TransactionTypeReceipt    TransactionType = "receipt"
	TransactionTypeIssue      TransactionType = "issue"
	TransactionTypeTransfer   TransactionType = "transfer"
	TransactionTypeAdjustment TransactionType = "adjustment"
	TransactionTypeCount      TransactionType = "count"
)

// InventoryTransaction represents an inventory movement
type InventoryTransaction struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	TenantID        uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	InventoryID     uuid.UUID       `json:"inventory_id" db:"inventory_id"`
	TransactionType TransactionType `json:"transaction_type" db:"transaction_type"`
	ReferenceType   *string         `json:"reference_type,omitempty" db:"reference_type"`
	ReferenceID     *uuid.UUID      `json:"reference_id,omitempty" db:"reference_id"`
	Quantity        float64         `json:"quantity" db:"quantity"`
	UnitCost        *float64        `json:"unit_cost,omitempty" db:"unit_cost"`
	TotalCost       *float64        `json:"total_cost,omitempty" db:"total_cost"`
	FromWarehouseID *uuid.UUID      `json:"from_warehouse_id,omitempty" db:"from_warehouse_id"`
	ToWarehouseID   *uuid.UUID      `json:"to_warehouse_id,omitempty" db:"to_warehouse_id"`
	FromLocationID  *uuid.UUID      `json:"from_location_id,omitempty" db:"from_location_id"`
	ToLocationID    *uuid.UUID      `json:"to_location_id,omitempty" db:"to_location_id"`
	Reason          *string         `json:"reason,omitempty" db:"reason"`
	Notes           *string         `json:"notes,omitempty" db:"notes"`
	TransactionDate time.Time       `json:"transaction_date" db:"transaction_date"`
	CreatedBy       *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
}

// CreateWarehouseInput represents input for creating a warehouse
type CreateWarehouseInput struct {
	Code      string   `json:"code" binding:"required,min=1,max=50"`
	Name      string   `json:"name" binding:"required,min=1,max=255"`
	Address   *Address `json:"address,omitempty"`
	ManagerID string   `json:"manager_id,omitempty"`
	IsDefault bool     `json:"is_default,omitempty"`
}

// UpdateWarehouseInput represents input for updating a warehouse
type UpdateWarehouseInput struct {
	Name      *string  `json:"name,omitempty"`
	Address   *Address `json:"address,omitempty"`
	ManagerID *string  `json:"manager_id,omitempty"`
	IsDefault *bool    `json:"is_default,omitempty"`
	IsActive  *bool    `json:"is_active,omitempty"`
}

// CreateWarehouseLocationInput represents input for creating a warehouse location
type CreateWarehouseLocationInput struct {
	WarehouseID string                `json:"warehouse_id" binding:"required"`
	ParentID    string                `json:"parent_id,omitempty"`
	Code        string                `json:"code" binding:"required,min=1,max=50"`
	Name        string                `json:"name" binding:"required,min=1,max=255"`
	Type        WarehouseLocationType `json:"type,omitempty"`
	Aisle       string                `json:"aisle,omitempty"`
	Rack        string                `json:"rack,omitempty"`
	Shelf       string                `json:"shelf,omitempty"`
	Bin         string                `json:"bin,omitempty"`
	Capacity    *float64              `json:"capacity,omitempty"`
}

// InventoryAdjustmentInput represents input for inventory adjustment
type InventoryAdjustmentInput struct {
	ProductID    string  `json:"product_id" binding:"required"`
	WarehouseID  string  `json:"warehouse_id" binding:"required"`
	LocationID   string  `json:"location_id,omitempty"`
	LotNumber    string  `json:"lot_number,omitempty"`
	SerialNumber string  `json:"serial_number,omitempty"`
	Quantity     float64 `json:"quantity" binding:"required"`
	UnitCost     float64 `json:"unit_cost,omitempty"`
	Reason       string  `json:"reason" binding:"required"`
	Notes        string  `json:"notes,omitempty"`
}

// InventoryTransferInput represents input for inventory transfer
type InventoryTransferInput struct {
	ProductID       string  `json:"product_id" binding:"required"`
	FromWarehouseID string  `json:"from_warehouse_id" binding:"required"`
	ToWarehouseID   string  `json:"to_warehouse_id" binding:"required"`
	FromLocationID  string  `json:"from_location_id,omitempty"`
	ToLocationID    string  `json:"to_location_id,omitempty"`
	Quantity        float64 `json:"quantity" binding:"required,gt=0"`
	Notes           string  `json:"notes,omitempty"`
}

// InventoryListFilter represents filters for listing inventory
type InventoryListFilter struct {
	Search      string  `form:"search"`
	ProductID   string  `form:"product_id"`
	WarehouseID string  `form:"warehouse_id"`
	LocationID  string  `form:"location_id"`
	LowStock    *bool   `form:"low_stock"`
	OutOfStock  *bool   `form:"out_of_stock"`
	Expiring    *bool   `form:"expiring"`
	ExpiryDays  int     `form:"expiry_days"`
}

// InventorySummary represents a summary of inventory for a product
type InventorySummary struct {
	ProductID         uuid.UUID `json:"product_id"`
	ProductCode       string    `json:"product_code"`
	ProductName       string    `json:"product_name"`
	TotalOnHand       float64   `json:"total_on_hand"`
	TotalReserved     float64   `json:"total_reserved"`
	TotalAvailable    float64   `json:"total_available"`
	TotalValue        float64   `json:"total_value"`
	WarehouseCount    int       `json:"warehouse_count"`
	MinStockLevel     float64   `json:"min_stock_level"`
	ReorderPoint      float64   `json:"reorder_point"`
	NeedsReorder      bool      `json:"needs_reorder"`
}

// StockMovementReport represents a stock movement report entry
type StockMovementReport struct {
	Date              time.Time       `json:"date"`
	ProductID         uuid.UUID       `json:"product_id"`
	ProductCode       string          `json:"product_code"`
	ProductName       string          `json:"product_name"`
	TransactionType   TransactionType `json:"transaction_type"`
	ReferenceType     *string         `json:"reference_type,omitempty"`
	ReferenceNumber   *string         `json:"reference_number,omitempty"`
	WarehouseName     string          `json:"warehouse_name"`
	QuantityIn        float64         `json:"quantity_in"`
	QuantityOut       float64         `json:"quantity_out"`
	Balance           float64         `json:"balance"`
	UnitCost          float64         `json:"unit_cost"`
	TotalValue        float64         `json:"total_value"`
}

// WarehouseListFilter represents filters for listing warehouses
type WarehouseListFilter struct {
	Search   string `form:"search"`
	IsActive *bool  `form:"is_active"`
}

// InventoryValuationReport represents inventory valuation
type InventoryValuationReport struct {
	ProductID         uuid.UUID  `json:"product_id"`
	ProductCode       string     `json:"product_code"`
	ProductName       string     `json:"product_name"`
	CategoryName      string     `json:"category_name"`
	QuantityOnHand    float64    `json:"quantity_on_hand"`
	AverageCost       float64    `json:"average_cost"`
	TotalValue        float64    `json:"total_value"`
	LastPurchasePrice float64    `json:"last_purchase_price"`
	LastPurchaseDate  *time.Time `json:"last_purchase_date,omitempty"`
}

// =====================================================
// INVENTORY LOTS (for FIFO/LIFO tracking)
// =====================================================

// InventoryLot represents a batch/lot of inventory
type InventoryLot struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	TenantID          uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProductID         uuid.UUID  `json:"product_id" db:"product_id"`
	WarehouseID       uuid.UUID  `json:"warehouse_id" db:"warehouse_id"`
	LocationID        *uuid.UUID `json:"location_id,omitempty" db:"location_id"`
	LotNumber         string     `json:"lot_number" db:"lot_number"`
	SerialNumber      *string    `json:"serial_number,omitempty" db:"serial_number"`
	ReceivedDate      time.Time  `json:"received_date" db:"received_date"`
	ExpiryDate        *time.Time `json:"expiry_date,omitempty" db:"expiry_date"`
	ManufactureDate   *time.Time `json:"manufacture_date,omitempty" db:"manufacture_date"`
	InitialQuantity   float64    `json:"initial_quantity" db:"initial_quantity"`
	RemainingQuantity float64    `json:"remaining_quantity" db:"remaining_quantity"`
	UnitCost          float64    `json:"unit_cost" db:"unit_cost"`
	VendorID          *uuid.UUID `json:"vendor_id,omitempty" db:"vendor_id"`
	PurchaseOrderID   *uuid.UUID `json:"purchase_order_id,omitempty" db:"purchase_order_id"`
	Status            string     `json:"status" db:"status"` // available, reserved, depleted, expired, quarantine
	Notes             *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Product   *Product   `json:"product,omitempty"`
	Warehouse *Warehouse `json:"warehouse,omitempty"`
}

// CreateInventoryLotInput represents input for creating an inventory lot
type CreateInventoryLotInput struct {
	ProductID       string  `json:"product_id" binding:"required"`
	WarehouseID     string  `json:"warehouse_id" binding:"required"`
	LocationID      string  `json:"location_id,omitempty"`
	LotNumber       string  `json:"lot_number" binding:"required"`
	SerialNumber    string  `json:"serial_number,omitempty"`
	ReceivedDate    string  `json:"received_date" binding:"required"`
	ExpiryDate      string  `json:"expiry_date,omitempty"`
	ManufactureDate string  `json:"manufacture_date,omitempty"`
	Quantity        float64 `json:"quantity" binding:"required,gt=0"`
	UnitCost        float64 `json:"unit_cost" binding:"required,gte=0"`
	VendorID        string  `json:"vendor_id,omitempty"`
	PurchaseOrderID string  `json:"purchase_order_id,omitempty"`
	Notes           string  `json:"notes,omitempty"`
}

// InventoryLotListFilter represents filters for listing inventory lots
type InventoryLotListFilter struct {
	ProductID   string `form:"product_id"`
	WarehouseID string `form:"warehouse_id"`
	Status      string `form:"status"`
	Expiring    bool   `form:"expiring"`
	ExpiryDays  int    `form:"expiry_days"`
}

// =====================================================
// TRANSFER ORDERS (Inter-warehouse transfers)
// =====================================================

// TransferOrder represents an inter-warehouse transfer
type TransferOrder struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	TenantID        uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	TransferNumber  string     `json:"transfer_number" db:"transfer_number"`
	FromWarehouseID uuid.UUID  `json:"from_warehouse_id" db:"from_warehouse_id"`
	ToWarehouseID   uuid.UUID  `json:"to_warehouse_id" db:"to_warehouse_id"`
	TransferDate    time.Time  `json:"transfer_date" db:"transfer_date"`
	ExpectedArrival *time.Time `json:"expected_arrival,omitempty" db:"expected_arrival"`
	ActualArrival   *time.Time `json:"actual_arrival,omitempty" db:"actual_arrival"`
	Status          string     `json:"status" db:"status"` // draft, pending, approved, in_transit, received, cancelled
	Priority        string     `json:"priority" db:"priority"` // low, normal, high, urgent
	Notes           *string    `json:"notes,omitempty" db:"notes"`
	Reason          *string    `json:"reason,omitempty" db:"reason"`
	CreatedBy       *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	ApprovedBy      *uuid.UUID `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt      *time.Time `json:"approved_at,omitempty" db:"approved_at"`
	ShippedBy       *uuid.UUID `json:"shipped_by,omitempty" db:"shipped_by"`
	ShippedAt       *time.Time `json:"shipped_at,omitempty" db:"shipped_at"`
	ReceivedBy      *uuid.UUID `json:"received_by,omitempty" db:"received_by"`
	ReceivedAt      *time.Time `json:"received_at,omitempty" db:"received_at"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	FromWarehouse *Warehouse           `json:"from_warehouse,omitempty"`
	ToWarehouse   *Warehouse           `json:"to_warehouse,omitempty"`
	Lines         []TransferOrderLine  `json:"lines,omitempty"`
}

// TransferOrderLine represents a line item in a transfer order
type TransferOrderLine struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	TransferOrderID  uuid.UUID  `json:"transfer_order_id" db:"transfer_order_id"`
	ProductID        uuid.UUID  `json:"product_id" db:"product_id"`
	FromLocationID   *uuid.UUID `json:"from_location_id,omitempty" db:"from_location_id"`
	ToLocationID     *uuid.UUID `json:"to_location_id,omitempty" db:"to_location_id"`
	LotID            *uuid.UUID `json:"lot_id,omitempty" db:"lot_id"`
	LotNumber        *string    `json:"lot_number,omitempty" db:"lot_number"`
	SerialNumber     *string    `json:"serial_number,omitempty" db:"serial_number"`
	QuantityRequested float64   `json:"quantity_requested" db:"quantity_requested"`
	QuantityShipped  float64    `json:"quantity_shipped" db:"quantity_shipped"`
	QuantityReceived float64    `json:"quantity_received" db:"quantity_received"`
	UnitCost         *float64   `json:"unit_cost,omitempty" db:"unit_cost"`
	Notes            *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Product *Product `json:"product,omitempty"`
}

// TransferOrderResponse is the API response format
type TransferOrderResponse struct {
	ID                uuid.UUID           `json:"id"`
	TransferNumber    string              `json:"transfer_number"`
	FromWarehouseID   uuid.UUID           `json:"from_warehouse_id"`
	FromWarehouseName string              `json:"from_warehouse_name,omitempty"`
	ToWarehouseID     uuid.UUID           `json:"to_warehouse_id"`
	ToWarehouseName   string              `json:"to_warehouse_name,omitempty"`
	TransferDate      string              `json:"transfer_date"`
	ExpectedArrival   *string             `json:"expected_arrival,omitempty"`
	Status            string              `json:"status"`
	Priority          string              `json:"priority"`
	Notes             *string             `json:"notes,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
	Lines             []TransferOrderLine `json:"lines,omitempty"`
}

// CreateTransferOrderInput represents input for creating a transfer order
type CreateTransferOrderInput struct {
	FromWarehouseID string                          `json:"from_warehouse_id" binding:"required"`
	ToWarehouseID   string                          `json:"to_warehouse_id" binding:"required"`
	TransferDate    string                          `json:"transfer_date" binding:"required"`
	ExpectedArrival string                          `json:"expected_arrival,omitempty"`
	Priority        string                          `json:"priority,omitempty"`
	Notes           string                          `json:"notes,omitempty"`
	Reason          string                          `json:"reason,omitempty"`
	Lines           []CreateTransferOrderLineInput  `json:"lines" binding:"required,min=1"`
}

// CreateTransferOrderLineInput represents a line item in transfer order creation
type CreateTransferOrderLineInput struct {
	ProductID      string  `json:"product_id" binding:"required"`
	FromLocationID string  `json:"from_location_id,omitempty"`
	ToLocationID   string  `json:"to_location_id,omitempty"`
	LotNumber      string  `json:"lot_number,omitempty"`
	SerialNumber   string  `json:"serial_number,omitempty"`
	Quantity       float64 `json:"quantity" binding:"required,gt=0"`
	Notes          string  `json:"notes,omitempty"`
}

// TransferOrderListFilter represents filters for listing transfer orders
type TransferOrderListFilter struct {
	Search          string `form:"search"`
	FromWarehouseID string `form:"from_warehouse_id"`
	ToWarehouseID   string `form:"to_warehouse_id"`
	Status          string `form:"status"`
	DateFrom        string `form:"date_from"`
	DateTo          string `form:"date_to"`
}

// =====================================================
// STOCK COUNTS (Inventory counting sessions)
// =====================================================

// StockCount represents an inventory counting session
type StockCount struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	TenantID            uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	WarehouseID         uuid.UUID  `json:"warehouse_id" db:"warehouse_id"`
	CountNumber         string     `json:"count_number" db:"count_number"`
	CountType           string     `json:"count_type" db:"count_type"` // full, cycle, spot
	CountDate           time.Time  `json:"count_date" db:"count_date"`
	Status              string     `json:"status" db:"status"` // draft, in_progress, completed, cancelled, approved
	Notes               *string    `json:"notes,omitempty" db:"notes"`
	StartedAt           *time.Time `json:"started_at,omitempty" db:"started_at"`
	StartedBy           *uuid.UUID `json:"started_by,omitempty" db:"started_by"`
	CompletedAt         *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CompletedBy         *uuid.UUID `json:"completed_by,omitempty" db:"completed_by"`
	ApprovedAt          *time.Time `json:"approved_at,omitempty" db:"approved_at"`
	ApprovedBy          *uuid.UUID `json:"approved_by,omitempty" db:"approved_by"`
	TotalSystemValue    float64    `json:"total_system_value" db:"total_system_value"`
	TotalCountedValue   float64    `json:"total_counted_value" db:"total_counted_value"`
	TotalVarianceValue  float64    `json:"total_variance_value" db:"total_variance_value"`
	AdjustmentJournalID *uuid.UUID `json:"adjustment_journal_id,omitempty" db:"adjustment_journal_id"`
	CreatedBy           *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Warehouse *Warehouse       `json:"warehouse,omitempty"`
	Lines     []StockCountLine `json:"lines,omitempty"`
}

// StockCountLine represents a line item in a stock count
type StockCountLine struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	StockCountID    uuid.UUID  `json:"stock_count_id" db:"stock_count_id"`
	ProductID       uuid.UUID  `json:"product_id" db:"product_id"`
	LocationID      *uuid.UUID `json:"location_id,omitempty" db:"location_id"`
	LotID           *uuid.UUID `json:"lot_id,omitempty" db:"lot_id"`
	LotNumber       *string    `json:"lot_number,omitempty" db:"lot_number"`
	SerialNumber    *string    `json:"serial_number,omitempty" db:"serial_number"`
	SystemQuantity  float64    `json:"system_quantity" db:"system_quantity"`
	CountedQuantity *float64   `json:"counted_quantity,omitempty" db:"counted_quantity"`
	VarianceQuantity float64   `json:"variance_quantity" db:"variance_quantity"`
	UnitCost        *float64   `json:"unit_cost,omitempty" db:"unit_cost"`
	SystemValue     *float64   `json:"system_value,omitempty" db:"system_value"`
	CountedValue    *float64   `json:"counted_value,omitempty" db:"counted_value"`
	VarianceValue   *float64   `json:"variance_value,omitempty" db:"variance_value"`
	Status          string     `json:"status" db:"status"` // pending, counted, verified, adjusted
	CountedBy       *uuid.UUID `json:"counted_by,omitempty" db:"counted_by"`
	CountedAt       *time.Time `json:"counted_at,omitempty" db:"counted_at"`
	VerifiedBy      *uuid.UUID `json:"verified_by,omitempty" db:"verified_by"`
	VerifiedAt      *time.Time `json:"verified_at,omitempty" db:"verified_at"`
	Notes           *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Product *Product `json:"product,omitempty"`
}

// CreateStockCountInput represents input for creating a stock count
type CreateStockCountInput struct {
	WarehouseID string `json:"warehouse_id" binding:"required"`
	CountType   string `json:"count_type,omitempty"` // full, cycle, spot
	CountDate   string `json:"count_date" binding:"required"`
	Notes       string `json:"notes,omitempty"`
}

// RecordCountLineInput represents input for recording a count
type RecordCountLineInput struct {
	ProductID       string  `json:"product_id" binding:"required"`
	LocationID      string  `json:"location_id,omitempty"`
	LotNumber       string  `json:"lot_number,omitempty"`
	SerialNumber    string  `json:"serial_number,omitempty"`
	CountedQuantity float64 `json:"counted_quantity" binding:"required,gte=0"`
	Notes           string  `json:"notes,omitempty"`
}

// StockCountListFilter represents filters for listing stock counts
type StockCountListFilter struct {
	WarehouseID string `form:"warehouse_id"`
	Status      string `form:"status"`
	CountType   string `form:"count_type"`
	DateFrom    string `form:"date_from"`
	DateTo      string `form:"date_to"`
}

// =====================================================
// REORDER ALERTS
// =====================================================

// ReorderAlert represents a low stock or reorder notification
type ReorderAlert struct {
	ID                   uuid.UUID  `json:"id" db:"id"`
	TenantID             uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProductID            uuid.UUID  `json:"product_id" db:"product_id"`
	WarehouseID          *uuid.UUID `json:"warehouse_id,omitempty" db:"warehouse_id"`
	AlertType            string     `json:"alert_type" db:"alert_type"` // low_stock, out_of_stock, expiring, overstock
	CurrentQuantity      float64    `json:"current_quantity" db:"current_quantity"`
	ThresholdQuantity    float64    `json:"threshold_quantity" db:"threshold_quantity"`
	SuggestedOrderQty    float64    `json:"suggested_order_qty" db:"suggested_order_qty"`
	ExpiryDate           *time.Time `json:"expiry_date,omitempty" db:"expiry_date"`
	DaysUntilExpiry      *int       `json:"days_until_expiry,omitempty" db:"days_until_expiry"`
	Priority             string     `json:"priority" db:"priority"` // low, medium, high, critical
	Status               string     `json:"status" db:"status"` // active, acknowledged, resolved, ignored, snoozed
	SnoozedUntil         *time.Time `json:"snoozed_until,omitempty" db:"snoozed_until"`
	AcknowledgedBy       *uuid.UUID `json:"acknowledged_by,omitempty" db:"acknowledged_by"`
	AcknowledgedAt       *time.Time `json:"acknowledged_at,omitempty" db:"acknowledged_at"`
	ResolvedAt           *time.Time `json:"resolved_at,omitempty" db:"resolved_at"`
	ResolvedBy           *uuid.UUID `json:"resolved_by,omitempty" db:"resolved_by"`
	ResolutionNotes      *string    `json:"resolution_notes,omitempty" db:"resolution_notes"`
	RelatedPurchaseOrderID *uuid.UUID `json:"related_purchase_order_id,omitempty" db:"related_purchase_order_id"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Product   *Product   `json:"product,omitempty"`
	Warehouse *Warehouse `json:"warehouse,omitempty"`
}

// ReorderAlertResponse is the API response format
type ReorderAlertResponse struct {
	ID                uuid.UUID `json:"id"`
	ProductID         uuid.UUID `json:"product_id"`
	ProductCode       string    `json:"product_code,omitempty"`
	ProductName       string    `json:"product_name,omitempty"`
	WarehouseID       *uuid.UUID `json:"warehouse_id,omitempty"`
	WarehouseName     string    `json:"warehouse_name,omitempty"`
	AlertType         string    `json:"alert_type"`
	CurrentQuantity   float64   `json:"current_quantity"`
	ThresholdQuantity float64   `json:"threshold_quantity"`
	SuggestedOrderQty float64   `json:"suggested_order_qty"`
	Priority          string    `json:"priority"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
}

// ReorderAlertListFilter represents filters for listing reorder alerts
type ReorderAlertListFilter struct {
	ProductID   string `form:"product_id"`
	WarehouseID string `form:"warehouse_id"`
	AlertType   string `form:"alert_type"`
	Priority    string `form:"priority"`
	Status      string `form:"status"`
}

// AcknowledgeAlertInput represents input for acknowledging an alert
type AcknowledgeAlertInput struct {
	Notes string `json:"notes,omitempty"`
}

// ResolveAlertInput represents input for resolving an alert
type ResolveAlertInput struct {
	ResolutionNotes      string `json:"resolution_notes,omitempty"`
	RelatedPurchaseOrderID string `json:"related_purchase_order_id,omitempty"`
}
