package entity

import (
	"time"

	"github.com/google/uuid"
)

// =====================================================
// WORK ORDER STATUS
// =====================================================

type WorkOrderStatus string

const (
	WorkOrderStatusDraft      WorkOrderStatus = "draft"
	WorkOrderStatusWaiting    WorkOrderStatus = "waiting"
	WorkOrderStatusReady      WorkOrderStatus = "ready"
	WorkOrderStatusInProgress WorkOrderStatus = "in_progress"
	WorkOrderStatusDone       WorkOrderStatus = "done"
	WorkOrderStatusCancelled  WorkOrderStatus = "cancelled"
)

// NOTE: WorkOrder struct is defined in manufacturing.go (old schema from migration 010)
// This file contains supplementary types for the work_orders handler

// WorkOrderTimeLog tracks time spent on work orders
type WorkOrderTimeLog struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	TenantID    uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	WorkOrderID uuid.UUID  `json:"work_order_id" db:"work_order_id"`

	StartTime       time.Time  `json:"start_time" db:"start_time"`
	EndTime         *time.Time `json:"end_time,omitempty" db:"end_time"`
	DurationMinutes *int       `json:"duration_minutes,omitempty" db:"duration_minutes"`

	LogType          string  `json:"log_type" db:"log_type"` // setup, production, pause, cleanup
	QuantityProduced float64 `json:"quantity_produced" db:"quantity_produced"`

	UserID   *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	UserName string     `json:"user_name,omitempty" db:"user_name"`

	ScrapQuantity float64 `json:"scrap_quantity" db:"scrap_quantity"`
	ScrapReason   *string `json:"scrap_reason,omitempty" db:"scrap_reason"`

	Notes     *string   `json:"notes,omitempty" db:"notes"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// =====================================================
// MANUFACTURING TRANSFER
// =====================================================

type ManufacturingTransferType string

const (
	MfgTransferPickComponents   ManufacturingTransferType = "pick_components"
	MfgTransferStoreFinished    ManufacturingTransferType = "store_finished"
)

type ManufacturingTransferStatus string

const (
	MfgTransferStatusDraft     ManufacturingTransferStatus = "draft"
	MfgTransferStatusWaiting   ManufacturingTransferStatus = "waiting"
	MfgTransferStatusReady     ManufacturingTransferStatus = "ready"
	MfgTransferStatusDone      ManufacturingTransferStatus = "done"
	MfgTransferStatusCancelled ManufacturingTransferStatus = "cancelled"
)

// ManufacturingTransfer represents an inventory transfer for manufacturing
type ManufacturingTransfer struct {
	ID               uuid.UUID                   `json:"id" db:"id"`
	TenantID         uuid.UUID                   `json:"tenant_id" db:"tenant_id"`
	OrganizationID   *uuid.UUID                  `json:"organization_id,omitempty" db:"organization_id"`
	ProductionOrderID uuid.UUID                  `json:"production_order_id" db:"production_order_id"`

	TransferNumber string                    `json:"transfer_number" db:"transfer_number"`
	TransferType   ManufacturingTransferType `json:"transfer_type" db:"transfer_type"`

	SourceLocationID      *uuid.UUID `json:"source_location_id,omitempty" db:"source_location_id"`
	DestinationLocationID *uuid.UUID `json:"destination_location_id,omitempty" db:"destination_location_id"`
	WarehouseID           *uuid.UUID `json:"warehouse_id,omitempty" db:"warehouse_id"`

	Status ManufacturingTransferStatus `json:"status" db:"status"`

	ScheduledDate *time.Time `json:"scheduled_date,omitempty" db:"scheduled_date"`
	DoneDate      *time.Time `json:"done_date,omitempty" db:"done_date"`

	ProcessedBy *uuid.UUID `json:"processed_by,omitempty" db:"processed_by"`

	Notes *string `json:"notes,omitempty" db:"notes"`

	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"-" db:"deleted_at"`

	// Relationships
	Lines                     []ManufacturingTransferLine `json:"lines,omitempty"`
	ProductionOrderNumber     string                      `json:"production_order_number,omitempty"`
	SourceLocationName        string                      `json:"source_location_name,omitempty"`
	DestinationLocationName   string                      `json:"destination_location_name,omitempty"`
	WarehouseName             string                      `json:"warehouse_name,omitempty"`
}

// ManufacturingTransferLine represents a line in a manufacturing transfer
type ManufacturingTransferLine struct {
	ID                      uuid.UUID  `json:"id" db:"id"`
	ManufacturingTransferID uuid.UUID  `json:"manufacturing_transfer_id" db:"manufacturing_transfer_id"`

	ProductID        uuid.UUID  `json:"product_id" db:"product_id"`
	ProductVariantID *uuid.UUID `json:"product_variant_id,omitempty" db:"product_variant_id"`

	QuantityDemanded float64 `json:"quantity_demanded" db:"quantity_demanded"`
	QuantityDone     float64 `json:"quantity_done" db:"quantity_done"`
	UOM              string  `json:"uom,omitempty" db:"uom"`

	LotID        *uuid.UUID `json:"lot_id,omitempty" db:"lot_id"`
	LotNumber    *string    `json:"lot_number,omitempty" db:"lot_number"`
	SerialNumber *string    `json:"serial_number,omitempty" db:"serial_number"`

	SourceLocationID      *uuid.UUID `json:"source_location_id,omitempty" db:"source_location_id"`
	DestinationLocationID *uuid.UUID `json:"destination_location_id,omitempty" db:"destination_location_id"`

	Notes *string `json:"notes,omitempty" db:"notes"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// Relationships for display
	ProductName   string `json:"product_name,omitempty"`
	ProductCode   string `json:"product_code,omitempty"`
}

// =====================================================
// INPUT/OUTPUT TYPES
// =====================================================

// CreateWorkOrderInput for creating work orders
type CreateWorkOrderInput struct {
	ProductionOrderID       string  `json:"production_order_id" binding:"required"`
	Name                    string  `json:"name" binding:"required"`
	Sequence                int     `json:"sequence"`
	BOMOperationID          string  `json:"bom_operation_id,omitempty"`
	OperationName           string  `json:"operation_name,omitempty"`
	WorkCenterID            string  `json:"work_center_id,omitempty"`
	QuantityToProduce       float64 `json:"quantity_to_produce"`
	ExpectedDurationMinutes int     `json:"expected_duration_minutes"`
	SetupTimeMinutes        int     `json:"setup_time_minutes"`
	ScheduledStart          string  `json:"scheduled_start,omitempty"`
	ScheduledEnd            string  `json:"scheduled_end,omitempty"`
	QualityCheckRequired    bool    `json:"quality_check_required"`
	Instructions            string  `json:"instructions,omitempty"`
	Notes                   string  `json:"notes,omitempty"`
}

// UpdateWorkOrderInput for updating work orders
type UpdateWorkOrderInput struct {
	Name                    *string  `json:"name,omitempty"`
	Sequence                *int     `json:"sequence,omitempty"`
	WorkCenterID            *string  `json:"work_center_id,omitempty"`
	QuantityToProduce       *float64 `json:"quantity_to_produce,omitempty"`
	QuantityProduced        *float64 `json:"quantity_produced,omitempty"`
	ExpectedDurationMinutes *int     `json:"expected_duration_minutes,omitempty"`
	SetupTimeMinutes        *int     `json:"setup_time_minutes,omitempty"`
	ActualDurationMinutes   *int     `json:"actual_duration_minutes,omitempty"`
	ScheduledStart          *string  `json:"scheduled_start,omitempty"`
	ScheduledEnd            *string  `json:"scheduled_end,omitempty"`
	OperatorID              *string  `json:"operator_id,omitempty"`
	QualityCheckRequired    *bool    `json:"quality_check_required,omitempty"`
	QualityCheckPassed      *bool    `json:"quality_check_passed,omitempty"`
	Instructions            *string  `json:"instructions,omitempty"`
	Notes                   *string  `json:"notes,omitempty"`
}

// WorkOrderListFilter for filtering work orders
type WorkOrderListFilter struct {
	ProductionOrderID string `form:"production_order_id"`
	WorkCenterID      string `form:"work_center_id"`
	Status            string `form:"status"`
	OperatorID        string `form:"operator_id"`
	DateFrom          string `form:"date_from"`
	DateTo            string `form:"date_to"`
}

// StartWorkOrderInput for starting a work order
type StartWorkOrderInput struct {
	OperatorID string `json:"operator_id,omitempty"`
	Notes      string `json:"notes,omitempty"`
}

// CompleteWorkOrderInput for completing a work order
type CompleteWorkOrderInput struct {
	QuantityProduced   float64 `json:"quantity_produced" binding:"required"`
	ScrapQuantity      float64 `json:"scrap_quantity,omitempty"`
	ScrapReason        string  `json:"scrap_reason,omitempty"`
	ShortfallReason    string  `json:"shortfall_reason,omitempty"`
	QualityCheckPassed *bool   `json:"quality_check_passed,omitempty"`
	Notes              string  `json:"notes,omitempty"`
}

// RecordTimeInput for recording time on work order
type RecordTimeInput struct {
	LogType          string  `json:"log_type" binding:"required"` // setup, production, pause, cleanup
	DurationMinutes  int     `json:"duration_minutes" binding:"required"`
	QuantityProduced float64 `json:"quantity_produced,omitempty"`
	ScrapQuantity    float64 `json:"scrap_quantity,omitempty"`
	ScrapReason      string  `json:"scrap_reason,omitempty"`
	Notes            string  `json:"notes,omitempty"`
}

// RecordWorkOrderTimeInput for recording time with start/end times
type RecordWorkOrderTimeInput struct {
	StartTime        *time.Time `json:"start_time,omitempty"`
	EndTime          *time.Time `json:"end_time,omitempty"`
	LogType          string     `json:"log_type"` // setup, production, pause, cleanup
	QuantityProduced float64    `json:"quantity_produced,omitempty"`
	ScrapQuantity    float64    `json:"scrap_quantity,omitempty"`
	ScrapReason      string     `json:"scrap_reason,omitempty"`
	Notes            string     `json:"notes,omitempty"`
}

// ValidateTransferInput for validating manufacturing transfers
type ValidateTransferInput struct {
	Lines []ValidateTransferLineInput `json:"lines"`
}

type ValidateTransferLineInput struct {
	LineID       string  `json:"line_id" binding:"required"`
	QuantityDone float64 `json:"quantity_done" binding:"required"`
	LotNumber    string  `json:"lot_number,omitempty"`
	SerialNumber string  `json:"serial_number,omitempty"`
}

// =====================================================
// RESPONSE TYPES
// =====================================================

// WorkOrderResponse for API responses
type WorkOrderResponse struct {
	ID                      uuid.UUID  `json:"id"`
	WorkOrderNumber         string     `json:"work_order_number"`
	Name                    string     `json:"name"`
	Sequence                int        `json:"sequence"`
	ProductionOrderID       uuid.UUID  `json:"production_order_id"`
	ProductionOrderNumber   string     `json:"production_order_number,omitempty"`
	ProductName             string     `json:"product_name,omitempty"`
	OperationName           string     `json:"operation_name,omitempty"`
	WorkCenterID            *uuid.UUID `json:"work_center_id,omitempty"`
	WorkCenterName          string     `json:"work_center_name,omitempty"`
	QuantityToProduce       float64    `json:"quantity_to_produce"`
	QuantityProduced        float64    `json:"quantity_produced"`
	Progress                float64    `json:"progress"` // percentage
	ExpectedDurationMinutes int        `json:"expected_duration_minutes"`
	SetupTimeMinutes        int        `json:"setup_time_minutes"`
	ActualDurationMinutes   int        `json:"actual_duration_minutes"`
	ScheduledStart          *time.Time `json:"scheduled_start,omitempty"`
	ScheduledEnd            *time.Time `json:"scheduled_end,omitempty"`
	ActualStart             *time.Time `json:"actual_start,omitempty"`
	ActualEnd               *time.Time `json:"actual_end,omitempty"`
	Status                  string     `json:"status"`
	StatusLabel             string     `json:"status_label"`
	OperatorID              *uuid.UUID `json:"operator_id,omitempty"`
	OperatorName            string     `json:"operator_name,omitempty"`
	QualityCheckRequired    bool       `json:"quality_check_required"`
	QualityCheckPassed      *bool      `json:"quality_check_passed,omitempty"`
	Instructions            *string    `json:"instructions,omitempty"`
	Notes                   *string    `json:"notes,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
}

// ManufacturingTransferResponse for API responses
type ManufacturingTransferResponse struct {
	ID                      uuid.UUID                          `json:"id"`
	TransferNumber          string                             `json:"transfer_number"`
	TransferType            string                             `json:"transfer_type"`
	TransferTypeLabel       string                             `json:"transfer_type_label"`
	ProductionOrderID       uuid.UUID                          `json:"production_order_id"`
	ProductionOrderNumber   string                             `json:"production_order_number,omitempty"`
	SourceLocationID        *uuid.UUID                         `json:"source_location_id,omitempty"`
	SourceLocationName      string                             `json:"source_location_name,omitempty"`
	DestinationLocationID   *uuid.UUID                         `json:"destination_location_id,omitempty"`
	DestinationLocationName string                             `json:"destination_location_name,omitempty"`
	WarehouseID             *uuid.UUID                         `json:"warehouse_id,omitempty"`
	WarehouseName           string                             `json:"warehouse_name,omitempty"`
	Status                  string                             `json:"status"`
	StatusLabel             string                             `json:"status_label"`
	ScheduledDate           *time.Time                         `json:"scheduled_date,omitempty"`
	DoneDate                *time.Time                         `json:"done_date,omitempty"`
	Lines                   []ManufacturingTransferLineResponse `json:"lines,omitempty"`
	CreatedAt               time.Time                          `json:"created_at"`
}

type ManufacturingTransferLineResponse struct {
	ID                      uuid.UUID  `json:"id"`
	ProductID               uuid.UUID  `json:"product_id"`
	ProductName             string     `json:"product_name"`
	ProductCode             string     `json:"product_code,omitempty"`
	QuantityDemanded        float64    `json:"quantity_demanded"`
	QuantityDone            float64    `json:"quantity_done"`
	UOM                     string     `json:"uom,omitempty"`
	LotNumber               *string    `json:"lot_number,omitempty"`
	SerialNumber            *string    `json:"serial_number,omitempty"`
	SourceLocationName      string     `json:"source_location_name,omitempty"`
	DestinationLocationName string     `json:"destination_location_name,omitempty"`
}

// =====================================================
// LEGACY INPUT/FILTER TYPES (for manufacturing.go compatibility)
// =====================================================

// WorkOrderInput for creating work orders (legacy API)
type WorkOrderInput struct {
	ProductionOrderID  uuid.UUID  `json:"production_order_id" binding:"required"`
	Name               *string    `json:"name,omitempty"`
	Sequence           *int       `json:"sequence,omitempty"`
	OperationID        *uuid.UUID `json:"operation_id,omitempty"`
	WorkCenterID       *uuid.UUID `json:"work_center_id,omitempty"`
	QuantityToProduce  float64    `json:"quantity_to_produce" binding:"required,gt=0"`
	UOM                string     `json:"uom" binding:"required"`
	PlannedDurationHrs *float64   `json:"planned_duration_hours,omitempty"`
	SetupTimeHrs       *float64   `json:"setup_time_hours,omitempty"`
	ScheduledStart     *string    `json:"scheduled_start,omitempty"`
	ScheduledEnd       *string    `json:"scheduled_end,omitempty"`
	AssignedTo         *uuid.UUID `json:"assigned_to,omitempty"`
	Instructions       *string    `json:"instructions,omitempty"`
	Notes              *string    `json:"notes,omitempty"`
}

// WorkOrderFilter for filtering work orders
type WorkOrderFilter struct {
	ProductionOrderID *uuid.UUID `form:"production_order_id"`
	WorkCenterID      *uuid.UUID `form:"work_center_id"`
	Status            *string    `form:"status"`
	AssignedTo        *uuid.UUID `form:"assigned_to"`
	DateFrom          *string    `form:"date_from"`
	DateTo            *string    `form:"date_to"`
	Page              int        `form:"page"`
	Limit             int        `form:"limit"`
	SortBy            string     `form:"sort_by"`
	SortOrder         string     `form:"sort_order"`
}

// =====================================================
// WORK ORDER MATERIALS
// =====================================================

type WorkOrderMaterial struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	TenantID          uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	WorkOrderID       uuid.UUID  `json:"work_order_id" db:"work_order_id"`
	ProductionOrderID uuid.UUID  `json:"production_order_id" db:"production_order_id"`
	ProductID         uuid.UUID  `json:"product_id" db:"product_id"`
	ProductName       string     `json:"product_name" db:"product_name"`
	Quantity          float64    `json:"quantity" db:"quantity"`
	UOM               string     `json:"uom" db:"uom"`
	UnitCost          float64    `json:"unit_cost" db:"unit_cost"`
	TotalCost         float64    `json:"total_cost" db:"total_cost"`
	Notes             *string    `json:"notes,omitempty" db:"notes"`
	CreatedBy         *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
}

type WorkOrderMaterialInput struct {
	ProductID uuid.UUID `json:"product_id" binding:"required"`
	Quantity  float64   `json:"quantity" binding:"required"`
	UOM       string    `json:"uom"`
	UnitCost  float64   `json:"unit_cost"`
	Notes     *string   `json:"notes,omitempty"`
}

type WorkOrderMaterialResponse struct {
	ID          uuid.UUID `json:"id"`
	ProductID   uuid.UUID `json:"product_id"`
	ProductName string    `json:"product_name"`
	Quantity    float64   `json:"quantity"`
	UOM         string    `json:"uom"`
	UnitCost    float64   `json:"unit_cost"`
	TotalCost   float64   `json:"total_cost"`
	Notes       *string   `json:"notes,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
