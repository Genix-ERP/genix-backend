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
	ProductID       uuid.UUID `json:"product_id"`
	ProductCode     string    `json:"product_code"`
	ProductName     string    `json:"product_name"`
	CategoryName    string    `json:"category_name"`
	QuantityOnHand  float64   `json:"quantity_on_hand"`
	AverageCost     float64   `json:"average_cost"`
	TotalValue      float64   `json:"total_value"`
	LastPurchasePrice float64 `json:"last_purchase_price"`
	LastPurchaseDate  *time.Time `json:"last_purchase_date,omitempty"`
}
