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
	// Odoo-style warehouse operation steps
	// 1 = Direct, 2 = 2-step, 3 = 3-step
	ReceptionSteps int       `json:"reception_steps" db:"reception_steps"` // 1=Receive to stock, 2=Input+Stock, 3=Input+QC+Stock
	DeliverySteps  int       `json:"delivery_steps" db:"delivery_steps"`   // 1=Deliver from stock, 2=Pick+Ship, 3=Pick+Pack+Ship
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`

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
	Code           string   `json:"code" binding:"required,min=1,max=50"`
	Name           string   `json:"name" binding:"required,min=1,max=255"`
	Address        *Address `json:"address,omitempty"`
	ManagerID      string   `json:"manager_id,omitempty"`
	IsDefault      bool     `json:"is_default,omitempty"`
	ReceptionSteps int      `json:"reception_steps,omitempty"` // 1=Direct, 2=Input+Stock, 3=Input+QC+Stock
	DeliverySteps  int      `json:"delivery_steps,omitempty"`  // 1=Direct, 2=Pick+Ship, 3=Pick+Pack+Ship
}

// UpdateWarehouseInput represents input for updating a warehouse
type UpdateWarehouseInput struct {
	Name           *string  `json:"name,omitempty"`
	Address        *Address `json:"address,omitempty"`
	ManagerID      *string  `json:"manager_id,omitempty"`
	IsDefault      *bool    `json:"is_default,omitempty"`
	IsActive       *bool    `json:"is_active,omitempty"`
	ReceptionSteps *int     `json:"reception_steps,omitempty"` // 1=Direct, 2=Input+Stock, 3=Input+QC+Stock
	DeliverySteps  *int     `json:"delivery_steps,omitempty"`  // 1=Direct, 2=Pick+Ship, 3=Pick+Pack+Ship
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
	VariantID    string  `json:"variant_id,omitempty"`
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

// =====================================================
// WAREHOUSE OPERATION TYPES (Odoo-style)
// =====================================================

// OperationTypeCode represents the type of warehouse operation
type OperationTypeCode string

const (
	OperationTypeReceipt  OperationTypeCode = "receipt"
	OperationTypeInternal OperationTypeCode = "internal"
	OperationTypeDelivery OperationTypeCode = "delivery"
	OperationTypePOS      OperationTypeCode = "pos"
	OperationTypeCustom   OperationTypeCode = "custom"
)

// WarehouseOperationType represents a type of warehouse operation
type WarehouseOperationType struct {
	ID                     uuid.UUID         `json:"id" db:"id"`
	TenantID               uuid.UUID         `json:"tenant_id" db:"tenant_id"`
	WarehouseID            uuid.UUID         `json:"warehouse_id" db:"warehouse_id"`
	Code                   string            `json:"code" db:"code"`
	Name                   string            `json:"name" db:"name"`
	Type                   OperationTypeCode `json:"type" db:"type"`
	Sequence               int               `json:"sequence" db:"sequence"`
	DefaultLocationSrcID   *uuid.UUID        `json:"default_location_src_id,omitempty" db:"default_location_src_id"`
	DefaultLocationDestID  *uuid.UUID        `json:"default_location_dest_id,omitempty" db:"default_location_dest_id"`
	ShowOperations         bool              `json:"show_operations" db:"show_operations"`
	ShowReserved           bool              `json:"show_reserved" db:"show_reserved"`
	BarcodeEnabled         bool              `json:"barcode_enabled" db:"barcode_enabled"`
	CreateBackorder        bool              `json:"create_backorder" db:"create_backorder"`
	ReservationMethod      string            `json:"reservation_method" db:"reservation_method"`
	ReturnPickingTypeID    *uuid.UUID        `json:"return_picking_type_id,omitempty" db:"return_picking_type_id"`
	Color                  string            `json:"color" db:"color"`
	CountPickingReady      int               `json:"count_picking_ready" db:"count_picking_ready"`
	CountPickingLate       int               `json:"count_picking_late" db:"count_picking_late"`
	CountPickingWaiting    int               `json:"count_picking_waiting" db:"count_picking_waiting"`
	CountPickingBackorders int               `json:"count_picking_backorders" db:"count_picking_backorders"`
	IsActive               bool              `json:"is_active" db:"is_active"`
	CreatedAt              time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at" db:"updated_at"`

	// Relationships
	Warehouse *Warehouse `json:"warehouse,omitempty"`
}

// CreateOperationTypeInput represents input for creating an operation type
type CreateOperationTypeInput struct {
	WarehouseID           string `json:"warehouse_id" binding:"required"`
	Code                  string `json:"code" binding:"required,min=1,max=50"`
	Name                  string `json:"name" binding:"required,min=1,max=255"`
	OperationType         string `json:"operation_type,omitempty"` // incoming, outgoing, internal
	Type                  string `json:"type,omitempty"`           // custom type name
	Sequence              int    `json:"sequence,omitempty"`
	DefaultLocationSrcID  string `json:"default_location_src_id,omitempty"`
	DefaultLocationDestID string `json:"default_location_dest_id,omitempty"`
	ShowOperations        *bool  `json:"show_operations,omitempty"`
	BarcodeEnabled        *bool  `json:"barcode_enabled,omitempty"`
	CreateBackorder       *bool  `json:"create_backorder,omitempty"`
	ReservationMethod     string `json:"reservation_method,omitempty"`
	Color                 string `json:"color,omitempty"`
}

// UpdateOperationTypeInput represents input for updating an operation type
type UpdateOperationTypeInput struct {
	Name                  *string `json:"name,omitempty"`
	Sequence              *int    `json:"sequence,omitempty"`
	DefaultLocationSrcID  *string `json:"default_location_src_id,omitempty"`
	DefaultLocationDestID *string `json:"default_location_dest_id,omitempty"`
	ShowOperations        *bool   `json:"show_operations,omitempty"`
	ShowReserved          *bool   `json:"show_reserved,omitempty"`
	BarcodeEnabled        *bool   `json:"barcode_enabled,omitempty"`
	CreateBackorder       *bool   `json:"create_backorder,omitempty"`
	ReservationMethod     *string `json:"reservation_method,omitempty"`
	ReturnPickingTypeID   *string `json:"return_picking_type_id,omitempty"`
	Color                 *string `json:"color,omitempty"`
	IsActive              *bool   `json:"is_active,omitempty"`
}

// OperationTypeResponse represents the API response for an operation type
type OperationTypeResponse struct {
	ID                     uuid.UUID `json:"id"`
	WarehouseID            uuid.UUID `json:"warehouse_id"`
	WarehouseName          string    `json:"warehouse_name,omitempty"`
	Code                   string    `json:"code"`
	Name                   string    `json:"name"`
	Type                   string    `json:"type"`
	Sequence               int       `json:"sequence"`
	Color                  string    `json:"color"`
	ShowOperations         bool      `json:"show_operations"`
	CountPickingReady      int       `json:"count_picking_ready"`
	CountPickingLate       int       `json:"count_picking_late"`
	CountPickingWaiting    int       `json:"count_picking_waiting"`
	CountPickingBackorders int       `json:"count_picking_backorders"`
	IsActive               bool      `json:"is_active"`
	CreatedAt              time.Time `json:"created_at"`
}

// ─── Stock Operations TT entities ─────────────────────────────────────────────

// OperationTypeStep represents a step within a configurable operation type
type OperationTypeStep struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenant_id"`
	OperationTypeID   uuid.UUID  `json:"operation_type_id"`
	Sequence          int        `json:"sequence"`
	Name              string     `json:"name"`
	SourceLocationID  *uuid.UUID `json:"source_location_id,omitempty"`
	DestLocationID    *uuid.UUID `json:"dest_location_id,omitempty"`
	ResponsibleRole   string     `json:"responsible_role,omitempty"`
	RequiresApproval  bool       `json:"requires_approval"`
	ApprovalRole      string     `json:"approval_role,omitempty"`
	AutoProceed       bool       `json:"auto_proceed"`
	MaxDurationHours  *float64   `json:"max_duration_hours,omitempty"`
	OnTimeout         string     `json:"on_timeout"`
	NotifyUsers       []string   `json:"notify_users"`
	RequiredDocuments []string   `json:"required_documents"`
	Instructions      string     `json:"instructions,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// StockOperation represents a stock operation (receipt, delivery, internal transfer, write-off)
type StockOperation struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        uuid.UUID  `json:"tenant_id"`
	Name            string     `json:"name"`
	OperationTypeID uuid.UUID  `json:"operation_type_id"`
	Direction       string     `json:"direction"` // receipt, delivery, internal, write_off
	Date            time.Time  `json:"date"`
	ScheduledDate   *time.Time `json:"scheduled_date,omitempty"`
	PartnerID       *uuid.UUID `json:"partner_id,omitempty"`
	PartnerName     string     `json:"partner_name,omitempty"`
	SourceDocument  string     `json:"source_document,omitempty"`
	SourceLocationID *uuid.UUID `json:"source_location_id,omitempty"`
	DestLocationID  *uuid.UUID `json:"dest_location_id,omitempty"`
	State           string     `json:"state"` // draft, in_progress, waiting, done, cancelled
	CurrentStep     int        `json:"current_step"`
	TotalSteps      int        `json:"total_steps"`
	Priority        string     `json:"priority"`
	BackorderID     *uuid.UUID `json:"backorder_id,omitempty"`
	ResponsibleID   *uuid.UUID `json:"responsible_id,omitempty"`
	Note            string     `json:"note,omitempty"`
	WriteOffReason  string     `json:"write_off_reason,omitempty"`
	Lines           []StockOperationLine    `json:"lines,omitempty"`
	StepLogs        []StockOperationStepLog `json:"step_logs,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// StockOperationLine represents a product row in a stock operation
type StockOperationLine struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	OperationID      uuid.UUID  `json:"operation_id"`
	ProductID        uuid.UUID  `json:"product_id"`
	ProductName      string     `json:"product_name,omitempty"`
	ProductCode      string     `json:"product_code,omitempty"`
	ExpectedQty      float64    `json:"expected_qty"`
	DoneQty          float64    `json:"done_qty"`
	UOM              string     `json:"uom"`
	UnitPrice        *float64   `json:"unit_price,omitempty"`
	LotID            *uuid.UUID `json:"lot_id,omitempty"`
	LotNumber        string     `json:"lot_number,omitempty"`
	ExpiryDate       *string    `json:"expiry_date,omitempty"`
	QualityStatus    string     `json:"quality_status"` // good, defective, rejected
	SourceLocationID *uuid.UUID `json:"source_location_id,omitempty"`
	DestLocationID   *uuid.UUID `json:"dest_location_id,omitempty"`
	WriteOffReason   string     `json:"write_off_reason,omitempty"`
	Note             string     `json:"note,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// StockOperationStepLog records the execution of each step
type StockOperationStepLog struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        uuid.UUID  `json:"tenant_id"`
	OperationID     uuid.UUID  `json:"operation_id"`
	StepID          *uuid.UUID `json:"step_id,omitempty"`
	StepSequence    int        `json:"step_sequence"`
	StepName        string     `json:"step_name"`
	State           string     `json:"state"` // pending, ready, in_progress, awaiting_approval, completed, rejected
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	StartedBy       *uuid.UUID `json:"started_by,omitempty"`
	CompletedBy     *uuid.UUID `json:"completed_by,omitempty"`
	ApprovedBy      *uuid.UUID `json:"approved_by,omitempty"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	Documents       []string   `json:"documents"`
	Note            string     `json:"note,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// CreateStockOperationInput is the input for creating a stock operation
type CreateStockOperationInput struct {
	OperationTypeID  string                       `json:"operation_type_id" binding:"required"`
	Direction        string                       `json:"direction" binding:"required"`
	ScheduledDate    *string                      `json:"scheduled_date,omitempty"`
	PartnerID        string                       `json:"partner_id,omitempty"`
	SourceDocument   string                       `json:"source_document,omitempty"`
	SourceLocationID string                       `json:"source_location_id,omitempty"`
	DestLocationID   string                       `json:"dest_location_id,omitempty"`
	Priority         string                       `json:"priority,omitempty"`
	Note             string                       `json:"note,omitempty"`
	WriteOffReason   string                       `json:"write_off_reason,omitempty"`
	Lines            []CreateStockOperationLine   `json:"lines,omitempty"`
}

// CreateStockOperationLine is the input for a single product line
type CreateStockOperationLine struct {
	ProductID        string   `json:"product_id" binding:"required"`
	ExpectedQty      float64  `json:"expected_qty"`
	DoneQty          float64  `json:"done_qty"`
	UOM              string   `json:"uom,omitempty"`
	UnitPrice        *float64 `json:"unit_price,omitempty"`
	LotNumber        string   `json:"lot_number,omitempty"`
	ExpiryDate       string   `json:"expiry_date,omitempty"`
	QualityStatus    string   `json:"quality_status,omitempty"`
	SourceLocationID string   `json:"source_location_id,omitempty"`
	DestLocationID   string   `json:"dest_location_id,omitempty"`
	WriteOffReason   string   `json:"write_off_reason,omitempty"`
	Note             string   `json:"note,omitempty"`
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
	WarehouseID      string   `json:"warehouse_id" binding:"required"`
	CountType        string   `json:"count_type,omitempty"` // full, cycle, spot
	CountDate        string   `json:"count_date" binding:"required"`
	Notes            string   `json:"notes,omitempty"`
	CountedByName    string   `json:"counted_by,omitempty"`
	SelectedProducts []string `json:"selected_products,omitempty"`
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

// =====================================================
// BILL OF MATERIALS (BOM)
// =====================================================

// BOMType represents the type of BOM
type BOMType string

const (
	BOMTypeManufacturing BOMType = "manufacturing"
	BOMTypeKit           BOMType = "kit"
	BOMTypePhantom       BOMType = "phantom"
)

// ProductBOM represents a Bill of Materials header
type ProductBOM struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	TenantID      uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code          string     `json:"code" db:"code"`
	Name          string     `json:"name" db:"name"`
	ProductID     uuid.UUID  `json:"product_id" db:"product_id"`
	BOMType       BOMType    `json:"bom_type" db:"bom_type"`
	Quantity      float64    `json:"quantity" db:"quantity"`
	Version       int        `json:"version" db:"version"`
	IsActive      bool       `json:"is_active" db:"is_active"`
	IsDefault     bool       `json:"is_default" db:"is_default"`
	EffectiveDate *time.Time `json:"effective_date,omitempty" db:"effective_date"`
	ExpiryDate    *time.Time `json:"expiry_date,omitempty" db:"expiry_date"`
	Notes         *string    `json:"notes,omitempty" db:"notes"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt     *time.Time `json:"-" db:"deleted_at"`

	// Relationships
	Product    *Product       `json:"product,omitempty"`
	Lines      []BOMLine      `json:"lines,omitempty"`
	Operations []BOMOperation `json:"operations,omitempty"`
}

// BOMLine represents a component in a BOM
type BOMLine struct {
	ID                   uuid.UUID  `json:"id" db:"id"`
	BOMID                uuid.UUID  `json:"bom_id" db:"bom_id"`
	LineNumber           int        `json:"line_number" db:"line_number"`
	ComponentID          uuid.UUID  `json:"component_id" db:"component_id"`
	Quantity             float64    `json:"quantity" db:"quantity"`
	UnitID               *uuid.UUID `json:"unit_id,omitempty" db:"unit_id"`
	UnitOfMeasure        string     `json:"unit_of_measure" db:"unit_of_measure"`
	ScrapPercent         float64    `json:"scrap_percent" db:"scrap_percent"`
	IsOptional           bool       `json:"is_optional" db:"is_optional"`
	SubstituteComponentID *uuid.UUID `json:"substitute_component_id,omitempty" db:"substitute_component_id"`
	OperationSequence    int        `json:"operation_sequence" db:"operation_sequence"`
	Notes                *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Component           *Product `json:"component,omitempty"`
	SubstituteComponent *Product `json:"substitute_component,omitempty"`
}

// BOMOperation represents a manufacturing operation in a BOM
type BOMOperation struct {
	ID               uuid.UUID `json:"id" db:"id"`
	BOMID            uuid.UUID `json:"bom_id" db:"bom_id"`
	Sequence         int       `json:"sequence" db:"sequence"`
	OperationName    string    `json:"operation_name" db:"operation_name"`
	WorkCenter       *string   `json:"work_center,omitempty" db:"work_center"`
	SetupTimeMinutes float64   `json:"setup_time_minutes" db:"setup_time_minutes"`
	RunTimeMinutes   float64   `json:"run_time_minutes" db:"run_time_minutes"`
	LaborCost        float64   `json:"labor_cost" db:"labor_cost"`
	OverheadCost     float64   `json:"overhead_cost" db:"overhead_cost"`
	Notes            *string   `json:"notes,omitempty" db:"notes"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

// CreateBOMInput represents input for creating a BOM
type CreateBOMInput struct {
	Code          string              `json:"code" binding:"required,min=1,max=50"`
	Name          string              `json:"name" binding:"required,min=1,max=255"`
	ProductID     string              `json:"product_id" binding:"required"`
	BOMType       string              `json:"bom_type,omitempty"`
	Quantity      float64             `json:"quantity,omitempty"`
	IsDefault     bool                `json:"is_default,omitempty"`
	EffectiveDate string              `json:"effective_date,omitempty"`
	ExpiryDate    string              `json:"expiry_date,omitempty"`
	Notes         string              `json:"notes,omitempty"`
	Lines         []CreateBOMLineInput `json:"lines,omitempty"`
}

// UpdateBOMInput represents input for updating a BOM
type UpdateBOMInput struct {
	Name          *string `json:"name,omitempty"`
	BOMType       *string `json:"bom_type,omitempty"`
	Quantity      *float64 `json:"quantity,omitempty"`
	IsActive      *bool   `json:"is_active,omitempty"`
	IsDefault     *bool   `json:"is_default,omitempty"`
	EffectiveDate *string `json:"effective_date,omitempty"`
	ExpiryDate    *string `json:"expiry_date,omitempty"`
	Notes         *string `json:"notes,omitempty"`
}

// CreateBOMLineInput represents input for creating a BOM line
type CreateBOMLineInput struct {
	ComponentID          string  `json:"component_id" binding:"required"`
	Quantity             float64 `json:"quantity" binding:"required,gt=0"`
	UnitOfMeasure        string  `json:"unit_of_measure,omitempty"`
	ScrapPercent         float64 `json:"scrap_percent,omitempty"`
	IsOptional           bool    `json:"is_optional,omitempty"`
	SubstituteComponentID string  `json:"substitute_component_id,omitempty"`
	OperationSequence    int     `json:"operation_sequence,omitempty"`
	Notes                string  `json:"notes,omitempty"`
}

// UpdateBOMLineInput represents input for updating a BOM line
type UpdateBOMLineInput struct {
	Quantity             *float64 `json:"quantity,omitempty"`
	UnitOfMeasure        *string  `json:"unit_of_measure,omitempty"`
	ScrapPercent         *float64 `json:"scrap_percent,omitempty"`
	IsOptional           *bool    `json:"is_optional,omitempty"`
	SubstituteComponentID *string  `json:"substitute_component_id,omitempty"`
	OperationSequence    *int     `json:"operation_sequence,omitempty"`
	Notes                *string  `json:"notes,omitempty"`
}

// BOMListFilter represents filters for listing BOMs
type BOMListFilter struct {
	Search    string `form:"search"`
	ProductID string `form:"product_id"`
	BOMType   string `form:"bom_type"`
	IsActive  *bool  `form:"is_active"`
}

// BOMResponse represents the API response for a BOM
type BOMResponse struct {
	ID            uuid.UUID           `json:"id"`
	Code          string              `json:"code"`
	Name          string              `json:"name"`
	ProductID     uuid.UUID           `json:"product_id"`
	ProductCode   string              `json:"product_code,omitempty"`
	ProductName   string              `json:"product_name,omitempty"`
	BOMType       string              `json:"bom_type"`
	Quantity      float64             `json:"quantity"`
	Version       int                 `json:"version"`
	IsActive      bool                `json:"is_active"`
	IsDefault     bool                `json:"is_default"`
	EffectiveDate *string             `json:"effective_date,omitempty"`
	ExpiryDate    *string             `json:"expiry_date,omitempty"`
	TotalCost     float64             `json:"total_cost"`
	LineCount     int                 `json:"line_count"`
	Lines         []BOMLineResponse   `json:"lines,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
}

// BOMLineResponse represents the API response for a BOM line
type BOMLineResponse struct {
	ID               uuid.UUID `json:"id"`
	LineNumber       int       `json:"line_number"`
	ComponentID      uuid.UUID `json:"component_id"`
	ComponentCode    string    `json:"component_code,omitempty"`
	ComponentName    string    `json:"component_name,omitempty"`
	Quantity         float64   `json:"quantity"`
	UnitOfMeasure    string    `json:"unit_of_measure"`
	ScrapPercent     float64   `json:"scrap_percent"`
	IsOptional       bool      `json:"is_optional"`
	UnitCost         float64   `json:"unit_cost"`
	TotalCost        float64   `json:"total_cost"`
}

// =====================================================
// SCRAP MANAGEMENT
// =====================================================

// ScrapReason represents a predefined scrap reason
type ScrapReason struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	TenantID         uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code             string     `json:"code" db:"code"`
	Name             string     `json:"name" db:"name"`
	Description      *string    `json:"description,omitempty" db:"description"`
	IsActive         bool       `json:"is_active" db:"is_active"`
	RequiresApproval bool       `json:"requires_approval" db:"requires_approval"`
	AccountID        *uuid.UUID `json:"account_id,omitempty" db:"account_id"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
}

// ScrapOrderStatus represents the status of a scrap order
type ScrapOrderStatus string

const (
	ScrapOrderStatusDraft           ScrapOrderStatus = "draft"
	ScrapOrderStatusPendingApproval ScrapOrderStatus = "pending_approval"
	ScrapOrderStatusApproved        ScrapOrderStatus = "approved"
	ScrapOrderStatusCompleted       ScrapOrderStatus = "completed"
	ScrapOrderStatusCancelled       ScrapOrderStatus = "cancelled"
)

// ScrapOrder represents a scrap/write-off order
type ScrapOrder struct {
	ID                     uuid.UUID        `json:"id" db:"id"`
	TenantID               uuid.UUID        `json:"tenant_id" db:"tenant_id"`
	ScrapNumber            string           `json:"scrap_number" db:"scrap_number"`
	ProductID              uuid.UUID        `json:"product_id" db:"product_id"`
	WarehouseID            uuid.UUID        `json:"warehouse_id" db:"warehouse_id"`
	LocationID             *uuid.UUID       `json:"location_id,omitempty" db:"location_id"`
	LotID                  *uuid.UUID       `json:"lot_id,omitempty" db:"lot_id"`
	Quantity               float64          `json:"quantity" db:"quantity"`
	UnitCost               *float64         `json:"unit_cost,omitempty" db:"unit_cost"`
	TotalCost              *float64         `json:"total_cost,omitempty" db:"total_cost"`
	ScrapReasonID          *uuid.UUID       `json:"scrap_reason_id,omitempty" db:"scrap_reason_id"`
	Reason                 *string          `json:"reason,omitempty" db:"reason"`
	ReasonNotes            *string          `json:"reason_notes,omitempty" db:"reason_notes"`
	ScrapDate              time.Time        `json:"scrap_date" db:"scrap_date"`
	Status                 ScrapOrderStatus `json:"status" db:"status"`
	ScrappedBy             *uuid.UUID       `json:"scrapped_by,omitempty" db:"scrapped_by"`
	ApprovedBy             *uuid.UUID       `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt             *time.Time       `json:"approved_at,omitempty" db:"approved_at"`
	CompletedAt            *time.Time       `json:"completed_at,omitempty" db:"completed_at"`
	JournalEntryID         *uuid.UUID       `json:"journal_entry_id,omitempty" db:"journal_entry_id"`
	InventoryTransactionID *uuid.UUID       `json:"inventory_transaction_id,omitempty" db:"inventory_transaction_id"`
	Notes                  *string          `json:"notes,omitempty" db:"notes"`
	CreatedAt              time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at" db:"updated_at"`
	DeletedAt              *time.Time       `json:"-" db:"deleted_at"`

	// Relationships
	Product     *Product     `json:"product,omitempty"`
	Warehouse   *Warehouse   `json:"warehouse,omitempty"`
	ScrapReason *ScrapReason `json:"scrap_reason,omitempty"`
}

// CreateScrapReasonInput represents input for creating a scrap reason
type CreateScrapReasonInput struct {
	Code             string `json:"code" binding:"required,min=1,max=50"`
	Name             string `json:"name" binding:"required,min=1,max=255"`
	Description      string `json:"description,omitempty"`
	RequiresApproval bool   `json:"requires_approval,omitempty"`
	AccountID        string `json:"account_id,omitempty"`
}

// UpdateScrapReasonInput represents input for updating a scrap reason
type UpdateScrapReasonInput struct {
	Name             *string `json:"name,omitempty"`
	Description      *string `json:"description,omitempty"`
	IsActive         *bool   `json:"is_active,omitempty"`
	RequiresApproval *bool   `json:"requires_approval,omitempty"`
	AccountID        *string `json:"account_id,omitempty"`
}

// CreateScrapOrderInput represents input for creating a scrap order
type CreateScrapOrderInput struct {
	ProductID     string  `json:"product_id" binding:"required"`
	WarehouseID   string  `json:"warehouse_id" binding:"required"`
	LocationID    string  `json:"location_id,omitempty"`
	LotID         string  `json:"lot_id,omitempty"`
	Quantity      float64 `json:"quantity" binding:"required,gt=0"`
	UnitCost      float64 `json:"unit_cost,omitempty"`
	ScrapReasonID string  `json:"scrap_reason_id,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	ReasonNotes   string  `json:"reason_notes,omitempty"`
	ScrapDate     string  `json:"scrap_date" binding:"required"`
	Notes         string  `json:"notes,omitempty"`
}

// UpdateScrapOrderInput represents input for updating a scrap order
type UpdateScrapOrderInput struct {
	Quantity      *float64 `json:"quantity,omitempty"`
	UnitCost      *float64 `json:"unit_cost,omitempty"`
	ScrapReasonID *string  `json:"scrap_reason_id,omitempty"`
	Reason        *string  `json:"reason,omitempty"`
	ReasonNotes   *string  `json:"reason_notes,omitempty"`
	Notes         *string  `json:"notes,omitempty"`
}

// ScrapOrderListFilter represents filters for listing scrap orders
type ScrapOrderListFilter struct {
	Search      string `form:"search"`
	ProductID   string `form:"product_id"`
	WarehouseID string `form:"warehouse_id"`
	Status      string `form:"status"`
	Reason      string `form:"reason"`
	DateFrom    string `form:"date_from"`
	DateTo      string `form:"date_to"`
}

// ScrapOrderResponse represents the API response for a scrap order
type ScrapOrderResponse struct {
	ID            uuid.UUID `json:"id"`
	ScrapNumber   string    `json:"scrap_number"`
	ProductID     uuid.UUID `json:"product_id"`
	ProductCode   string    `json:"product_code,omitempty"`
	ProductName   string    `json:"product_name,omitempty"`
	WarehouseID   uuid.UUID `json:"warehouse_id"`
	WarehouseName string    `json:"warehouse_name,omitempty"`
	Quantity      float64   `json:"quantity"`
	UnitCost      float64   `json:"unit_cost"`
	TotalCost     float64   `json:"total_cost"`
	Reason        string    `json:"reason,omitempty"`
	ReasonNotes   string    `json:"reason_notes,omitempty"`
	ScrapDate     string    `json:"scrap_date"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// ScrapSummary represents a summary of scrap activity
type ScrapSummary struct {
	TotalScrapOrders   int     `json:"total_scrap_orders"`
	TotalQuantity      float64 `json:"total_quantity"`
	TotalValue         float64 `json:"total_value"`
	PendingApproval    int     `json:"pending_approval"`
	ByReason           map[string]float64 `json:"by_reason"`
}

// =====================================================
// REORDER RULES
// =====================================================

// ReorderTriggerType represents how reorder is triggered
type ReorderTriggerType string

const (
	ReorderTriggerMinQty   ReorderTriggerType = "min_qty"
	ReorderTriggerForecast ReorderTriggerType = "forecast"
	ReorderTriggerSchedule ReorderTriggerType = "schedule"
)

// ReorderRule represents automated reorder configuration
type ReorderRule struct {
	ID                uuid.UUID          `json:"id" db:"id"`
	TenantID          uuid.UUID          `json:"tenant_id" db:"tenant_id"`
	ProductID         uuid.UUID          `json:"product_id" db:"product_id"`
	WarehouseID       *uuid.UUID         `json:"warehouse_id,omitempty" db:"warehouse_id"`
	MinQty            float64            `json:"min_qty" db:"min_qty"`
	MaxQty            *float64           `json:"max_qty,omitempty" db:"max_qty"`
	ReorderQty        float64            `json:"reorder_qty" db:"reorder_qty"`
	TriggerType       ReorderTriggerType `json:"trigger_type" db:"trigger_type"`
	PreferredVendorID *uuid.UUID         `json:"preferred_vendor_id,omitempty" db:"preferred_vendor_id"`
	LeadTimeDays      int                `json:"lead_time_days" db:"lead_time_days"`
	SafetyStock       float64            `json:"safety_stock" db:"safety_stock"`
	AutoCreatePO      bool               `json:"auto_create_po" db:"auto_create_po"`
	IsActive          bool               `json:"is_active" db:"is_active"`
	LastTriggeredAt   *time.Time         `json:"last_triggered_at,omitempty" db:"last_triggered_at"`
	Notes             *string            `json:"notes,omitempty" db:"notes"`
	CreatedBy         *uuid.UUID         `json:"created_by,omitempty" db:"created_by"`
	CreatedAt         time.Time          `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at" db:"updated_at"`

	// Relationships
	Product         *Product   `json:"product,omitempty"`
	Warehouse       *Warehouse `json:"warehouse,omitempty"`
	PreferredVendor *Contact   `json:"preferred_vendor,omitempty"`
}

// CreateReorderRuleInput represents input for creating a reorder rule
type CreateReorderRuleInput struct {
	ProductID         string  `json:"product_id" binding:"required"`
	WarehouseID       string  `json:"warehouse_id,omitempty"`
	MinQty            float64 `json:"min_qty" binding:"required,gte=0"`
	MaxQty            float64 `json:"max_qty,omitempty"`
	ReorderQty        float64 `json:"reorder_qty" binding:"required,gt=0"`
	TriggerType       string  `json:"trigger_type,omitempty"`
	PreferredVendorID string  `json:"preferred_vendor_id,omitempty"`
	LeadTimeDays      int     `json:"lead_time_days,omitempty"`
	SafetyStock       float64 `json:"safety_stock,omitempty"`
	AutoCreatePO      bool    `json:"auto_create_po,omitempty"`
	Notes             string  `json:"notes,omitempty"`
}

// UpdateReorderRuleInput represents input for updating a reorder rule
type UpdateReorderRuleInput struct {
	MinQty            *float64 `json:"min_qty,omitempty"`
	MaxQty            *float64 `json:"max_qty,omitempty"`
	ReorderQty        *float64 `json:"reorder_qty,omitempty"`
	TriggerType       *string  `json:"trigger_type,omitempty"`
	PreferredVendorID *string  `json:"preferred_vendor_id,omitempty"`
	LeadTimeDays      *int     `json:"lead_time_days,omitempty"`
	SafetyStock       *float64 `json:"safety_stock,omitempty"`
	AutoCreatePO      *bool    `json:"auto_create_po,omitempty"`
	IsActive          *bool    `json:"is_active,omitempty"`
	Notes             *string  `json:"notes,omitempty"`
}

// ReorderRuleListFilter represents filters for listing reorder rules
type ReorderRuleListFilter struct {
	Search      string `form:"search"`
	ProductID   string `form:"product_id"`
	WarehouseID string `form:"warehouse_id"`
	TriggerType string `form:"trigger_type"`
	IsActive    *bool  `form:"is_active"`
}

// ReorderRuleResponse represents the API response for a reorder rule
type ReorderRuleResponse struct {
	ID                 uuid.UUID  `json:"id"`
	ProductID          uuid.UUID  `json:"product_id"`
	ProductCode        string     `json:"product_code,omitempty"`
	ProductName        string     `json:"product_name,omitempty"`
	WarehouseID        *uuid.UUID `json:"warehouse_id,omitempty"`
	WarehouseName      string     `json:"warehouse_name,omitempty"`
	MinQty             float64    `json:"min_qty"`
	MaxQty             *float64   `json:"max_qty,omitempty"`
	ReorderQty         float64    `json:"reorder_qty"`
	TriggerType        string     `json:"trigger_type"`
	PreferredVendorID  *uuid.UUID `json:"preferred_vendor_id,omitempty"`
	PreferredVendorName string    `json:"preferred_vendor_name,omitempty"`
	LeadTimeDays       int        `json:"lead_time_days"`
	SafetyStock        float64    `json:"safety_stock"`
	AutoCreatePO       bool       `json:"auto_create_po"`
	IsActive           bool       `json:"is_active"`
	CurrentStock       float64    `json:"current_stock,omitempty"`
	NeedsReorder       bool       `json:"needs_reorder,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}
