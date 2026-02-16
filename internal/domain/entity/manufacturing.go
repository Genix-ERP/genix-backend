package entity

import (
	"time"

	"github.com/google/uuid"
)

// =====================================================
// WORK CENTER ENTITIES
// =====================================================

type WorkCenter struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	TenantID            uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code                string     `json:"code" db:"code"`
	Name                string     `json:"name" db:"name"`
	Description         *string    `json:"description,omitempty" db:"description"`
	WarehouseID         *uuid.UUID `json:"warehouse_id,omitempty" db:"warehouse_id"`
	Department          *string    `json:"department,omitempty" db:"department"`
	CapacityPerHour     float64    `json:"capacity_per_hour" db:"capacity_per_hour"`
	EfficiencyFactor    float64    `json:"efficiency_factor" db:"efficiency_factor"`
	OEETarget           float64    `json:"oee_target" db:"oee_target"`
	WorkingHoursPerDay  float64    `json:"working_hours_per_day" db:"working_hours_per_day"`
	HourlyCost          float64    `json:"hourly_cost" db:"hourly_cost"`
	SetupCost           float64    `json:"setup_cost" db:"setup_cost"`
	OverheadCost        float64    `json:"overhead_cost" db:"overhead_cost"`
	Currency            string     `json:"currency" db:"currency"`
	Status              string     `json:"status" db:"status"`
	IsAvailable         bool       `json:"is_available" db:"is_available"`
	NextMaintenanceDate *time.Time `json:"next_maintenance_date,omitempty" db:"next_maintenance_date"`
	LastMaintenanceDate *time.Time `json:"last_maintenance_date,omitempty" db:"last_maintenance_date"`
	TotalJobsCompleted  int        `json:"total_jobs_completed" db:"total_jobs_completed"`
	TotalHoursWorked    float64    `json:"total_hours_worked" db:"total_hours_worked"`
	CurrentUtilization  float64    `json:"current_utilization" db:"current_utilization"`
	Notes               *string    `json:"notes,omitempty" db:"notes"`
	CreatedBy           *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type WorkCenterInput struct {
	Code                string     `json:"code" binding:"required"`
	Name                string     `json:"name" binding:"required"`
	Description         *string    `json:"description,omitempty"`
	WarehouseID         *uuid.UUID `json:"warehouse_id,omitempty"`
	Department          *string    `json:"department,omitempty"`
	CapacityPerHour     *float64   `json:"capacity_per_hour,omitempty"`
	EfficiencyFactor    *float64   `json:"efficiency_factor,omitempty"`
	OEETarget           *float64   `json:"oee_target,omitempty"`
	WorkingHoursPerDay  *float64   `json:"working_hours_per_day,omitempty"`
	HourlyCost          *float64   `json:"hourly_cost,omitempty"`
	SetupCost           *float64   `json:"setup_cost,omitempty"`
	OverheadCost        *float64   `json:"overhead_cost,omitempty"`
	Currency            *string    `json:"currency,omitempty"`
	Status              *string    `json:"status,omitempty"`
	IsAvailable         *bool      `json:"is_available,omitempty"`
	NextMaintenanceDate *string    `json:"next_maintenance_date,omitempty"`
	Notes               *string    `json:"notes,omitempty"`
}

type WorkCenterResponse struct {
	ID                  uuid.UUID  `json:"id"`
	Code                string     `json:"code"`
	Name                string     `json:"name"`
	Description         *string    `json:"description,omitempty"`
	WarehouseID         *uuid.UUID `json:"warehouse_id,omitempty"`
	WarehouseName       *string    `json:"warehouse_name,omitempty"`
	Department          *string    `json:"department,omitempty"`
	CapacityPerHour     float64    `json:"capacity_per_hour"`
	EfficiencyFactor    float64    `json:"efficiency_factor"`
	OEETarget           float64    `json:"oee_target"`
	WorkingHoursPerDay  float64    `json:"working_hours_per_day"`
	HourlyCost          float64    `json:"hourly_cost"`
	SetupCost           float64    `json:"setup_cost"`
	OverheadCost        float64    `json:"overhead_cost"`
	Currency            string     `json:"currency"`
	Status              string     `json:"status"`
	IsAvailable         bool       `json:"is_available"`
	NextMaintenanceDate *string    `json:"next_maintenance_date,omitempty"`
	LastMaintenanceDate *string    `json:"last_maintenance_date,omitempty"`
	TotalJobsCompleted  int        `json:"total_jobs_completed"`
	TotalHoursWorked    float64    `json:"total_hours_worked"`
	CurrentUtilization  float64    `json:"current_utilization"`
	Notes               *string    `json:"notes,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type WorkCenterFilter struct {
	Status      *string    `form:"status"`
	IsAvailable *bool      `form:"is_available"`
	WarehouseID *uuid.UUID `form:"warehouse_id"`
	Department  *string    `form:"department"`
	Search      *string    `form:"search"`
	Page        int        `form:"page"`
	Limit       int        `form:"limit"`
	SortBy      string     `form:"sort_by"`
	SortOrder   string     `form:"sort_order"`
}

// =====================================================
// PRODUCTION ORDER ENTITIES
// =====================================================

type ProductionOrder struct {
	ID                   uuid.UUID  `json:"id" db:"id"`
	TenantID             uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code                 string     `json:"code" db:"code"`
	Name                 *string    `json:"name,omitempty" db:"name"`
	ProductID            uuid.UUID  `json:"product_id" db:"product_id"`
	BOMID                *uuid.UUID `json:"bom_id,omitempty" db:"bom_id"`
	QuantityPlanned      float64    `json:"quantity_planned" db:"quantity_planned"`
	QuantityProduced     float64    `json:"quantity_produced" db:"quantity_produced"`
	QuantityScrapped     float64    `json:"quantity_scrapped" db:"quantity_scrapped"`
	UOM                  string     `json:"uom" db:"uom"`
	// Manufacturing-specific fields
	MoldCount            int        `json:"mold_count" db:"mold_count"`
	Shift                *string    `json:"shift,omitempty" db:"shift"`
	CurrentStage         string     `json:"current_stage" db:"current_stage"`
	PackageCount         int        `json:"package_count" db:"package_count"`
	GoodQuantity         float64    `json:"good_quantity" db:"good_quantity"`
	RejectQuantity       float64    `json:"reject_quantity" db:"reject_quantity"`
	// Schedule fields
	ScheduledStart       *time.Time `json:"scheduled_start,omitempty" db:"scheduled_start"`
	ScheduledEnd         *time.Time `json:"scheduled_end,omitempty" db:"scheduled_end"`
	ActualStart          *time.Time `json:"actual_start,omitempty" db:"actual_start"`
	ActualEnd            *time.Time `json:"actual_end,omitempty" db:"actual_end"`
	Priority             int        `json:"priority" db:"priority"`
	Status               string     `json:"status" db:"status"`
	ProgressPercent      float64    `json:"progress_percent" db:"progress_percent"`
	SourceType           *string    `json:"source_type,omitempty" db:"source_type"`
	SourceID             *uuid.UUID `json:"source_id,omitempty" db:"source_id"`
	SalesOrderID         *uuid.UUID `json:"sales_order_id,omitempty" db:"sales_order_id"`
	CustomerID           *uuid.UUID `json:"customer_id,omitempty" db:"customer_id"`
	WarehouseID          *uuid.UUID `json:"warehouse_id,omitempty" db:"warehouse_id"`
	LocationID           *uuid.UUID `json:"location_id,omitempty" db:"location_id"`
	PlannedCost          float64    `json:"planned_cost" db:"planned_cost"`
	ActualCost           float64    `json:"actual_cost" db:"actual_cost"`
	MaterialCost         float64    `json:"material_cost" db:"material_cost"`
	LaborCost            float64    `json:"labor_cost" db:"labor_cost"`
	OverheadCost         float64    `json:"overhead_cost" db:"overhead_cost"`
	Currency             string     `json:"currency" db:"currency"`
	AssignedTo           *uuid.UUID `json:"assigned_to,omitempty" db:"assigned_to"`
	WorkCenterID         *uuid.UUID `json:"work_center_id,omitempty" db:"work_center_id"`
	RequiresQualityCheck bool       `json:"requires_quality_check" db:"requires_quality_check"`
	QualityStatus        *string    `json:"quality_status,omitempty" db:"quality_status"`
	Notes                *string    `json:"notes,omitempty" db:"notes"`
	InternalNotes        *string    `json:"internal_notes,omitempty" db:"internal_notes"`
	Tags                 []string   `json:"tags" db:"tags"`
	CreatedBy            *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	ConfirmedBy          *uuid.UUID `json:"confirmed_by,omitempty" db:"confirmed_by"`
	ConfirmedAt          *time.Time `json:"confirmed_at,omitempty" db:"confirmed_at"`
	CompletedBy          *uuid.UUID `json:"completed_by,omitempty" db:"completed_by"`
	CompletedAt          *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt            *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type ProductionOrderInput struct {
	Name                 *string    `json:"name,omitempty"`
	ProductID            uuid.UUID  `json:"product_id" binding:"required"`
	BOMID                *uuid.UUID `json:"bom_id,omitempty"`
	QuantityPlanned      float64    `json:"quantity_planned" binding:"required,gt=0"`
	UOM                  string     `json:"uom" binding:"required"`
	// Manufacturing-specific fields
	MoldCount            *int       `json:"mold_count,omitempty"`
	Shift                *string    `json:"shift,omitempty"`
	// Schedule fields
	ScheduledStart       *string    `json:"scheduled_start,omitempty"`
	ScheduledEnd         *string    `json:"scheduled_end,omitempty"`
	Priority             *int       `json:"priority,omitempty"`
	SourceType           *string    `json:"source_type,omitempty"`
	SourceID             *uuid.UUID `json:"source_id,omitempty"`
	SalesOrderID         *uuid.UUID `json:"sales_order_id,omitempty"`
	CustomerID           *uuid.UUID `json:"customer_id,omitempty"`
	WarehouseID          *uuid.UUID `json:"warehouse_id,omitempty"`
	LocationID           *uuid.UUID `json:"location_id,omitempty"`
	AssignedTo           *uuid.UUID `json:"assigned_to,omitempty"`
	WorkCenterID         *uuid.UUID `json:"work_center_id,omitempty"`
	RequiresQualityCheck *bool      `json:"requires_quality_check,omitempty"`
	Notes                *string    `json:"notes,omitempty"`
	Tags                 []string   `json:"tags,omitempty"`
}

type ProductionOrderUpdateInput struct {
	Name                 *string    `json:"name,omitempty"`
	BOMID                *uuid.UUID `json:"bom_id,omitempty"`
	QuantityPlanned      *float64   `json:"quantity_planned,omitempty"`
	ScheduledStart       *string    `json:"scheduled_start,omitempty"`
	ScheduledEnd         *string    `json:"scheduled_end,omitempty"`
	Priority             *int       `json:"priority,omitempty"`
	AssignedTo           *uuid.UUID `json:"assigned_to,omitempty"`
	WorkCenterID         *uuid.UUID `json:"work_center_id,omitempty"`
	RequiresQualityCheck *bool      `json:"requires_quality_check,omitempty"`
	Notes                *string    `json:"notes,omitempty"`
	Tags                 []string   `json:"tags,omitempty"`
	// Manufacturing-specific fields
	MoldCount            *int       `json:"mold_count,omitempty"`
	Shift                *string    `json:"shift,omitempty"`
	CurrentStage         *string    `json:"current_stage,omitempty"`
	PackageCount         *int       `json:"package_count,omitempty"`
	GoodQuantity         *float64   `json:"good_quantity,omitempty"`
	RejectQuantity       *float64   `json:"reject_quantity,omitempty"`
}

type ProductionOrderResponse struct {
	ID                   uuid.UUID   `json:"id"`
	Code                 string      `json:"code"`
	Name                 *string     `json:"name,omitempty"`
	ProductID            uuid.UUID   `json:"product_id"`
	ProductName          string      `json:"product_name"`
	ProductCode          string      `json:"product_code"`
	BOMID                *uuid.UUID                `json:"bom_id,omitempty"`
	BOMName              *string                   `json:"bom_name,omitempty"`
	BOMOperations        []map[string]interface{}  `json:"bom_operations,omitempty"`
	QuantityPlanned      float64                   `json:"quantity_planned"`
	QuantityProduced     float64     `json:"quantity_produced"`
	QuantityScrapped     float64     `json:"quantity_scrapped"`
	QuantityRemaining    float64     `json:"quantity_remaining"`
	UOM                  string      `json:"uom"`
	// Manufacturing-specific fields
	MoldCount            int         `json:"mold_count"`
	Shift                *string     `json:"shift,omitempty"`
	CurrentStage         string      `json:"current_stage"`
	PackageCount         int         `json:"package_count"`
	GoodQuantity         float64     `json:"good_quantity"`
	RejectQuantity       float64     `json:"reject_quantity"`
	// Schedule fields
	ScheduledStart       *string     `json:"scheduled_start,omitempty"`
	ScheduledEnd         *string     `json:"scheduled_end,omitempty"`
	ActualStart          *string     `json:"actual_start,omitempty"`
	ActualEnd            *string     `json:"actual_end,omitempty"`
	Priority             int         `json:"priority"`
	Status               string      `json:"status"`
	ProgressPercent      float64     `json:"progress_percent"`
	SourceType           *string     `json:"source_type,omitempty"`
	WarehouseID          *uuid.UUID  `json:"warehouse_id,omitempty"`
	WarehouseName        *string     `json:"warehouse_name,omitempty"`
	PlannedCost          float64     `json:"planned_cost"`
	ActualCost           float64     `json:"actual_cost"`
	MaterialCost         float64     `json:"material_cost"`
	LaborCost            float64     `json:"labor_cost"`
	OverheadCost         float64     `json:"overhead_cost"`
	Currency             string      `json:"currency"`
	AssignedTo           *uuid.UUID  `json:"assigned_to,omitempty"`
	AssignedToName       *string     `json:"assigned_to_name,omitempty"`
	WorkCenterID         *uuid.UUID  `json:"work_center_id,omitempty"`
	WorkCenterName       *string     `json:"work_center_name,omitempty"`
	RequiresQualityCheck bool        `json:"requires_quality_check"`
	QualityStatus        *string     `json:"quality_status,omitempty"`
	Notes                *string     `json:"notes,omitempty"`
	Tags                 []string    `json:"tags"`
	WorkOrders           []WorkOrder `json:"work_orders,omitempty"`
	Stages               []ProductionOrderStageResponse `json:"stages,omitempty"`
	CreatedBy            *uuid.UUID  `json:"created_by,omitempty"`
	CreatedByName        *string     `json:"created_by_name,omitempty"`
	ConfirmedAt          *string     `json:"confirmed_at,omitempty"`
	CompletedAt          *string     `json:"completed_at,omitempty"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
}

type ProductionOrderFilter struct {
	Status       *string    `form:"status"`
	ProductID    *uuid.UUID `form:"product_id"`
	WorkCenterID *uuid.UUID `form:"work_center_id"`
	AssignedTo   *uuid.UUID `form:"assigned_to"`
	Priority     *int       `form:"priority"`
	DateFrom     *string    `form:"date_from"`
	DateTo       *string    `form:"date_to"`
	Search       *string    `form:"search"`
	Page         int        `form:"page"`
	Limit        int        `form:"limit"`
	SortBy       string     `form:"sort_by"`
	SortOrder    string     `form:"sort_order"`
}

// =====================================================
// WORK ORDER ENTITIES (Old Schema from migration 010)
// =====================================================

type WorkOrder struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	TenantID            uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProductionOrderID   uuid.UUID  `json:"production_order_id" db:"production_order_id"`
	Code                string     `json:"code" db:"code"`
	Name                *string    `json:"name,omitempty" db:"name"`
	Sequence            int        `json:"sequence" db:"sequence"`
	OperationID         *uuid.UUID `json:"operation_id,omitempty" db:"operation_id"`
	WorkCenterID        *uuid.UUID `json:"work_center_id,omitempty" db:"work_center_id"`
	WorkCenterName      *string    `json:"work_center_name,omitempty" db:"-"` // Joined field
	QuantityToProduce   float64    `json:"quantity_to_produce" db:"quantity_to_produce"`
	QuantityProduced    float64    `json:"quantity_produced" db:"quantity_produced"`
	QuantityScrapped    float64    `json:"quantity_scrapped" db:"quantity_scrapped"`
	UOM                 string     `json:"uom" db:"uom"`
	PlannedDurationHrs  float64    `json:"planned_duration_hours" db:"planned_duration_hours"`
	ActualDurationHrs   float64    `json:"actual_duration_hours" db:"actual_duration_hours"`
	SetupTimeHrs        float64    `json:"setup_time_hours" db:"setup_time_hours"`
	ScheduledStart      *time.Time `json:"scheduled_start,omitempty" db:"scheduled_start"`
	ScheduledEnd        *time.Time `json:"scheduled_end,omitempty" db:"scheduled_end"`
	ActualStart         *time.Time `json:"actual_start,omitempty" db:"actual_start"`
	ActualEnd           *time.Time `json:"actual_end,omitempty" db:"actual_end"`
	Status              string     `json:"status" db:"status"`
	ProgressPercent     float64    `json:"progress_percent" db:"progress_percent"`
	PlannedCost         float64    `json:"planned_cost" db:"planned_cost"`
	ActualCost          float64    `json:"actual_cost" db:"actual_cost"`
	LaborCost           float64    `json:"labor_cost" db:"labor_cost"`
	MachineCost         float64    `json:"machine_cost" db:"machine_cost"`
	AssignedTo          *uuid.UUID `json:"assigned_to,omitempty" db:"assigned_to"`
	Instructions        *string    `json:"instructions,omitempty" db:"instructions"`
	Notes               *string    `json:"notes,omitempty" db:"notes"`
	CreatedBy           *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	StartedBy           *uuid.UUID `json:"started_by,omitempty" db:"started_by"`
	CompletedBy         *uuid.UUID `json:"completed_by,omitempty" db:"completed_by"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

// =====================================================
// QUALITY CHECK ENTITIES
// =====================================================

type QualityCheck struct {
	ID                    uuid.UUID  `json:"id" db:"id"`
	TenantID              uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code                  string     `json:"code" db:"code"`
	QualityControlPointID *uuid.UUID `json:"quality_control_point_id,omitempty" db:"quality_control_point_id"`
	ProductionOrderID     *uuid.UUID `json:"production_order_id,omitempty" db:"production_order_id"`
	WorkOrderID           *uuid.UUID `json:"work_order_id,omitempty" db:"work_order_id"`
	ProductID             *uuid.UUID `json:"product_id,omitempty" db:"product_id"`
	LotNumber             *string    `json:"lot_number,omitempty" db:"lot_number"`
	InspectionDate        time.Time  `json:"inspection_date" db:"inspection_date"`
	InspectorID           *uuid.UUID `json:"inspector_id,omitempty" db:"inspector_id"`
	InspectorName         *string    `json:"inspector_name,omitempty" db:"inspector_name"`
	QuantityInspected     float64    `json:"quantity_inspected" db:"quantity_inspected"`
	QuantityPassed        float64    `json:"quantity_passed" db:"quantity_passed"`
	QuantityFailed        float64    `json:"quantity_failed" db:"quantity_failed"`
	Result                string     `json:"result" db:"result"`
	MeasuredValue         *float64   `json:"measured_value,omitempty" db:"measured_value"`
	MeasurementUnit       *string    `json:"measurement_unit,omitempty" db:"measurement_unit"`
	PassRate              *float64   `json:"pass_rate,omitempty" db:"pass_rate"`
	DefectType            *string    `json:"defect_type,omitempty" db:"defect_type"`
	DefectCategory        *string    `json:"defect_category,omitempty" db:"defect_category"`
	ActionTaken           *string    `json:"action_taken,omitempty" db:"action_taken"`
	ReworkOrderID         *uuid.UUID `json:"rework_order_id,omitempty" db:"rework_order_id"`
	ScrapOrderID          *uuid.UUID `json:"scrap_order_id,omitempty" db:"scrap_order_id"`
	Notes                 *string    `json:"notes,omitempty" db:"notes"`
	FailureReason         *string    `json:"failure_reason,omitempty" db:"failure_reason"`
	CorrectiveAction      *string    `json:"corrective_action,omitempty" db:"corrective_action"`
	Attachments           []string   `json:"attachments" db:"attachments"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt             *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type QualityCheckInput struct {
	QualityControlPointID *uuid.UUID `json:"quality_control_point_id,omitempty"`
	ProductionOrderID     *uuid.UUID `json:"production_order_id,omitempty"`
	WorkOrderID           *uuid.UUID `json:"work_order_id,omitempty"`
	ProductID             *uuid.UUID `json:"product_id,omitempty"`
	LotNumber             *string    `json:"lot_number,omitempty"`
	InspectionDate        *string    `json:"inspection_date,omitempty"`
	QuantityInspected     float64    `json:"quantity_inspected" binding:"required,gt=0"`
	QuantityPassed        float64    `json:"quantity_passed"`
	QuantityFailed        float64    `json:"quantity_failed"`
	Result                *string    `json:"result,omitempty"`
	MeasuredValue         *float64   `json:"measured_value,omitempty"`
	MeasurementUnit       *string    `json:"measurement_unit,omitempty"`
	DefectType            *string    `json:"defect_type,omitempty"`
	DefectCategory        *string    `json:"defect_category,omitempty"`
	ActionTaken           *string    `json:"action_taken,omitempty"`
	Notes                 *string    `json:"notes,omitempty"`
	FailureReason         *string    `json:"failure_reason,omitempty"`
	CorrectiveAction      *string    `json:"corrective_action,omitempty"`
	Attachments           []string   `json:"attachments,omitempty"`
}

type QualityCheckResponse struct {
	ID                    uuid.UUID  `json:"id"`
	Code                  string     `json:"code"`
	QualityControlPointID *uuid.UUID `json:"quality_control_point_id,omitempty"`
	ProductionOrderID     *uuid.UUID `json:"production_order_id,omitempty"`
	ProductionOrderCode   *string    `json:"production_order_code,omitempty"`
	WorkOrderID           *uuid.UUID `json:"work_order_id,omitempty"`
	WorkOrderCode         *string    `json:"work_order_code,omitempty"`
	ProductID             *uuid.UUID `json:"product_id,omitempty"`
	ProductName           *string    `json:"product_name,omitempty"`
	ProductCode           *string    `json:"product_code,omitempty"`
	LotNumber             *string    `json:"lot_number,omitempty"`
	InspectionDate        string     `json:"inspection_date"`
	InspectorID           *uuid.UUID `json:"inspector_id,omitempty"`
	InspectorName         *string    `json:"inspector_name,omitempty"`
	QuantityInspected     float64    `json:"quantity_inspected"`
	QuantityPassed        float64    `json:"quantity_passed"`
	QuantityFailed        float64    `json:"quantity_failed"`
	Result                string     `json:"result"`
	MeasuredValue         *float64   `json:"measured_value,omitempty"`
	MeasurementUnit       *string    `json:"measurement_unit,omitempty"`
	PassRate              float64    `json:"pass_rate"`
	DefectType            *string    `json:"defect_type,omitempty"`
	DefectCategory        *string    `json:"defect_category,omitempty"`
	ActionTaken           *string    `json:"action_taken,omitempty"`
	Notes                 *string    `json:"notes,omitempty"`
	FailureReason         *string    `json:"failure_reason,omitempty"`
	CorrectiveAction      *string    `json:"corrective_action,omitempty"`
	Attachments           []string   `json:"attachments"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type QualityCheckFilter struct {
	ProductionOrderID *uuid.UUID `form:"production_order_id"`
	WorkOrderID       *uuid.UUID `form:"work_order_id"`
	ProductID         *uuid.UUID `form:"product_id"`
	Result            *string    `form:"result"`
	InspectorID       *uuid.UUID `form:"inspector_id"`
	DateFrom          *string    `form:"date_from"`
	DateTo            *string    `form:"date_to"`
	Page              int        `form:"page"`
	Limit             int        `form:"limit"`
	SortBy            string     `form:"sort_by"`
	SortOrder         string     `form:"sort_order"`
}

// =====================================================
// QUALITY DEFECT ENTITIES
// =====================================================

type QualityDefect struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	TenantID      uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code          string     `json:"code" db:"code"`
	Name          string     `json:"name" db:"name"`
	Description   *string    `json:"description,omitempty" db:"description"`
	Category      *string    `json:"category,omitempty" db:"category"`
	Severity      string     `json:"severity" db:"severity"`
	DefaultAction string     `json:"default_action" db:"default_action"`
	IsActive      bool       `json:"is_active" db:"is_active"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type QualityDefectInput struct {
	Code          string  `json:"code" binding:"required"`
	Name          string  `json:"name" binding:"required"`
	Description   *string `json:"description,omitempty"`
	Category      *string `json:"category,omitempty"`
	Severity      *string `json:"severity,omitempty"`
	DefaultAction *string `json:"default_action,omitempty"`
	IsActive      *bool   `json:"is_active,omitempty"`
}

// =====================================================
// MRP ENTITIES
// =====================================================

type MRPDemand struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	TenantID         uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProductID        uuid.UUID  `json:"product_id" db:"product_id"`
	SourceType       string     `json:"source_type" db:"source_type"`
	SourceID         *uuid.UUID `json:"source_id,omitempty" db:"source_id"`
	SourceReference  *string    `json:"source_reference,omitempty" db:"source_reference"`
	QuantityRequired float64    `json:"quantity_required" db:"quantity_required"`
	QuantityReserved float64    `json:"quantity_reserved" db:"quantity_reserved"`
	QuantityFulfill  float64    `json:"quantity_fulfilled" db:"quantity_fulfilled"`
	UOM              string     `json:"uom" db:"uom"`
	RequiredDate     time.Time  `json:"required_date" db:"required_date"`
	Priority         int        `json:"priority" db:"priority"`
	WarehouseID      *uuid.UUID `json:"warehouse_id,omitempty" db:"warehouse_id"`
	Status           string     `json:"status" db:"status"`
	SupplyType       *string    `json:"supply_type,omitempty" db:"supply_type"`
	SupplyID         *uuid.UUID `json:"supply_id,omitempty" db:"supply_id"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
}

type MRPRecommendation struct {
	ID                 uuid.UUID  `json:"id" db:"id"`
	TenantID           uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProductID          uuid.UUID  `json:"product_id" db:"product_id"`
	RecommendationType string     `json:"recommendation_type" db:"recommendation_type"`
	Quantity           float64    `json:"quantity" db:"quantity"`
	UOM                string     `json:"uom" db:"uom"`
	RecommendedDate    time.Time  `json:"recommended_date" db:"recommended_date"`
	DueDate            *time.Time `json:"due_date,omitempty" db:"due_date"`
	DemandID           *uuid.UUID `json:"demand_id,omitempty" db:"demand_id"`
	Priority           int        `json:"priority" db:"priority"`
	Urgency            string     `json:"urgency" db:"urgency"`
	Status             string     `json:"status" db:"status"`
	ExecutedType       *string    `json:"executed_type,omitempty" db:"executed_type"`
	ExecutedID         *uuid.UUID `json:"executed_id,omitempty" db:"executed_id"`
	ExecutedAt         *time.Time `json:"executed_at,omitempty" db:"executed_at"`
	ExecutedBy         *uuid.UUID `json:"executed_by,omitempty" db:"executed_by"`
	Reason             *string    `json:"reason,omitempty" db:"reason"`
	Notes              *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}

type MRPRecommendationResponse struct {
	ID                 uuid.UUID  `json:"id"`
	ProductID          uuid.UUID  `json:"product_id"`
	ProductName        string     `json:"product_name"`
	ProductCode        string     `json:"product_code"`
	RecommendationType string     `json:"recommendation_type"`
	Quantity           float64    `json:"quantity"`
	UOM                string     `json:"uom"`
	RecommendedDate    string     `json:"recommended_date"`
	DueDate            *string    `json:"due_date,omitempty"`
	Priority           int        `json:"priority"`
	Urgency            string     `json:"urgency"`
	Status             string     `json:"status"`
	Reason             *string    `json:"reason,omitempty"`
	Notes              *string    `json:"notes,omitempty"`
	CurrentStock       float64    `json:"current_stock"`
	OnOrderQty         float64    `json:"on_order_qty"`
	InProductionQty    float64    `json:"in_production_qty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// =====================================================
// MANUFACTURING STATISTICS
// =====================================================

type ManufacturingStats struct {
	// Production Orders
	TotalProductionOrders   int     `json:"total_production_orders"`
	DraftOrders             int     `json:"draft_orders"`
	ConfirmedOrders         int     `json:"confirmed_orders"`
	InProgressOrders        int     `json:"in_progress_orders"`
	CompletedOrders         int     `json:"completed_orders"`
	OverdueOrders           int     `json:"overdue_orders"`
	CompletionRate          float64 `json:"completion_rate"`
	AverageLeadTimeDays     float64 `json:"average_lead_time_days"`

	// Work Centers
	TotalWorkCenters        int     `json:"total_work_centers"`
	ActiveWorkCenters       int     `json:"active_work_centers"`
	AverageUtilization      float64 `json:"average_utilization"`
	AverageOEE              float64 `json:"average_oee"`

	// Quality
	TotalQualityChecks      int     `json:"total_quality_checks"`
	PassedChecks            int     `json:"passed_checks"`
	FailedChecks            int     `json:"failed_checks"`
	OverallPassRate         float64 `json:"overall_pass_rate"`
	TotalDefects            int     `json:"total_defects"`

	// Efficiency
	TotalUnitsProduced      float64 `json:"total_units_produced"`
	TotalUnitsScrapped      float64 `json:"total_units_scrapped"`
	ScrapRate               float64 `json:"scrap_rate"`
	PlannedVsActualCost     float64 `json:"planned_vs_actual_cost"`
}

type WorkCenterStats struct {
	WorkCenterID       uuid.UUID `json:"work_center_id"`
	WorkCenterName     string    `json:"work_center_name"`
	TotalOrders        int       `json:"total_orders"`
	CompletedOrders    int       `json:"completed_orders"`
	TotalHoursWorked   float64   `json:"total_hours_worked"`
	Utilization        float64   `json:"utilization"`
	OEE                float64   `json:"oee"`
	Availability       float64   `json:"availability"`
	Performance        float64   `json:"performance"`
	Quality            float64   `json:"quality"`
}

type ProductionScheduleItem struct {
	ID              uuid.UUID `json:"id"`
	Code            string    `json:"code"`
	ProductName     string    `json:"product_name"`
	WorkCenterName  *string   `json:"work_center_name,omitempty"`
	QuantityPlanned float64   `json:"quantity_planned"`
	Status          string    `json:"status"`
	ScheduledStart  *string   `json:"scheduled_start,omitempty"`
	ScheduledEnd    *string   `json:"scheduled_end,omitempty"`
	Priority        int       `json:"priority"`
	ProgressPercent float64   `json:"progress_percent"`
}

// =====================================================
// PRODUCTION ORDER STAGE ENTITIES
// =====================================================

// ProductionOrderStage tracks individual manufacturing stages
type ProductionOrderStage struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	TenantID          uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID    *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`
	ProductionOrderID uuid.UUID  `json:"production_order_id" db:"production_order_id"`
	StageName         string     `json:"stage_name" db:"stage_name"`
	Sequence          int        `json:"sequence" db:"sequence"`
	Status            string     `json:"status" db:"status"`
	StartedAt         *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	OperatorID        *uuid.UUID `json:"operator_id,omitempty" db:"operator_id"`
	OperatorName      *string    `json:"operator_name,omitempty" db:"operator_name"`
	DurationMinutes   int        `json:"duration_minutes" db:"duration_minutes"`
	Notes             *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type ProductionOrderStageInput struct {
	StageName    string     `json:"stage_name" binding:"required"`
	Sequence     *int       `json:"sequence,omitempty"`
	Status       *string    `json:"status,omitempty"`
	OperatorID   *uuid.UUID `json:"operator_id,omitempty"`
	OperatorName *string    `json:"operator_name,omitempty"`
	Notes        *string    `json:"notes,omitempty"`
}

type ProductionOrderStageResponse struct {
	ID                uuid.UUID `json:"id"`
	ProductionOrderID uuid.UUID `json:"production_order_id"`
	StageName         string    `json:"stage_name"`
	Sequence          int       `json:"sequence"`
	Status            string    `json:"status"`
	StartedAt         *string   `json:"started_at,omitempty"`
	CompletedAt       *string   `json:"completed_at,omitempty"`
	OperatorID        *uuid.UUID `json:"operator_id,omitempty"`
	OperatorName      *string   `json:"operator_name,omitempty"`
	DurationMinutes   int       `json:"duration_minutes"`
	Notes             *string   `json:"notes,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// ProductionOutput tracks output quantities at each stage
type ProductionOutput struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	TenantID          uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID    *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`
	ProductionOrderID uuid.UUID  `json:"production_order_id" db:"production_order_id"`
	StageID           *uuid.UUID `json:"stage_id,omitempty" db:"stage_id"`
	OutputType        string     `json:"output_type" db:"output_type"`
	Quantity          float64    `json:"quantity" db:"quantity"`
	UOM               string     `json:"uom" db:"uom"`
	PackageCount      int        `json:"package_count" db:"package_count"`
	Notes             *string    `json:"notes,omitempty" db:"notes"`
	RecordedBy        *uuid.UUID `json:"recorded_by,omitempty" db:"recorded_by"`
	RecordedAt        time.Time  `json:"recorded_at" db:"recorded_at"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

type ProductionOutputInput struct {
	StageID      *uuid.UUID `json:"stage_id,omitempty"`
	OutputType   string     `json:"output_type" binding:"required"`
	Quantity     float64    `json:"quantity" binding:"required,gt=0"`
	UOM          *string    `json:"uom,omitempty"`
	PackageCount *int       `json:"package_count,omitempty"`
	Notes        *string    `json:"notes,omitempty"`
}

type ProductionOutputResponse struct {
	ID                uuid.UUID `json:"id"`
	ProductionOrderID uuid.UUID `json:"production_order_id"`
	StageID           *uuid.UUID `json:"stage_id,omitempty"`
	StageName         *string   `json:"stage_name,omitempty"`
	OutputType        string    `json:"output_type"`
	Quantity          float64   `json:"quantity"`
	UOM               string    `json:"uom"`
	PackageCount      int       `json:"package_count"`
	Notes             *string   `json:"notes,omitempty"`
	RecordedBy        *uuid.UUID `json:"recorded_by,omitempty"`
	RecordedByName    *string   `json:"recorded_by_name,omitempty"`
	RecordedAt        string    `json:"recorded_at"`
	CreatedAt         time.Time `json:"created_at"`
}

// Manufacturing stage constants
const (
	StageDraft    = "draft"
	StageMixing   = "mixing"
	StageRising   = "rising"
	StageDrying   = "drying"
	StageCutting  = "cutting"
	StagePacking  = "packing"
	StageDone     = "done"
)

// Shift constants
const (
	ShiftDay   = "day"
	ShiftNight = "night"
)

// Output type constants
const (
	OutputTypeGood   = "good"
	OutputTypeReject = "reject"
	OutputTypeScrap  = "scrap"
	OutputTypeRework = "rework"
)
