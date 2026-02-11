package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION PROJECT
// =====================================================

// ConstructionProject represents a construction project
type ConstructionProject struct {
	ID             int64          `json:"id" db:"id"`
	TenantID       uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	OrganizationID uuid.NullUUID  `json:"organization_id" db:"organization_id"`
	Code           string         `json:"code" db:"code"`
	Name           string         `json:"name" db:"name"`
	Description    sql.NullString `json:"description" db:"description"`

	// Location
	Address     sql.NullString `json:"address" db:"address"`
	City        sql.NullString `json:"city" db:"city"`
	District    sql.NullString `json:"district" db:"district"`
	Region      sql.NullString `json:"region" db:"region"`
	Coordinates json.RawMessage `json:"coordinates" db:"coordinates"`

	// Client Info
	ClientName    sql.NullString `json:"client_name" db:"client_name"`
	ClientContact sql.NullString `json:"client_contact" db:"client_contact"`
	ClientPhone   sql.NullString `json:"client_phone" db:"client_phone"`

	// Project Details
	ProjectType  sql.NullString  `json:"project_type" db:"project_type"`
	BuildingType sql.NullString  `json:"building_type" db:"building_type"`
	TotalArea    sql.NullFloat64 `json:"total_area" db:"total_area"`
	FloorsCount  sql.NullInt32   `json:"floors_count" db:"floors_count"`

	// Financial
	ContractAmount sql.NullFloat64 `json:"contract_amount" db:"contract_amount"`
	Currency       string          `json:"currency" db:"currency"`

	// Dates
	ContractDate     sql.NullTime `json:"contract_date" db:"contract_date"`
	PlannedStartDate sql.NullTime `json:"planned_start_date" db:"planned_start_date"`
	PlannedEndDate   sql.NullTime `json:"planned_end_date" db:"planned_end_date"`
	ActualStartDate  sql.NullTime `json:"actual_start_date" db:"actual_start_date"`
	ActualEndDate    sql.NullTime `json:"actual_end_date" db:"actual_end_date"`

	// Status
	Status          string          `json:"status" db:"status"`
	ProgressPercent sql.NullFloat64 `json:"progress_percent" db:"progress_percent"`

	// Responsible persons
	ProjectManagerID uuid.NullUUID `json:"project_manager_id" db:"project_manager_id"`
	ChiefEngineerID  uuid.NullUUID `json:"chief_engineer_id" db:"chief_engineer_id"`

	// Metadata
	CreatedBy   *uuid.UUID   `json:"created_by" db:"created_by"`
	CreatedDate time.Time    `json:"created_date" db:"created_date"`
	UpdatedDate time.Time    `json:"updated_date" db:"updated_date"`
	DeletedAt   sql.NullTime `json:"-" db:"deleted_at"`

	// Computed fields (not in DB)
	ProjectManagerName string `json:"project_manager_name,omitempty" db:"project_manager_name"`
	ChiefEngineerName  string `json:"chief_engineer_name,omitempty" db:"chief_engineer_name"`
	SectionsCount      int    `json:"sections_count,omitempty" db:"sections_count"`
	TotalSmeta         float64 `json:"total_smeta,omitempty" db:"total_smeta"`
}

// CreateConstructionProjectInput represents input for creating a construction project
type CreateConstructionProjectInput struct {
	Code             string  `json:"code" binding:"required"`
	Name             string  `json:"name" binding:"required"`
	Description      string  `json:"description"`
	Address          string  `json:"address"`
	City             string  `json:"city"`
	District         string  `json:"district"`
	Region           string  `json:"region"`
	Coordinates      string  `json:"coordinates"`
	ClientName       string  `json:"client_name"`
	ClientContact    string  `json:"client_contact"`
	ClientPhone      string  `json:"client_phone"`
	ProjectType      string  `json:"project_type"`
	BuildingType     string  `json:"building_type"`
	TotalArea        float64 `json:"total_area"`
	FloorsCount      int     `json:"floors_count"`
	ContractAmount   float64 `json:"contract_amount"`
	Currency         string  `json:"currency"`
	ContractDate     string  `json:"contract_date"`
	PlannedStartDate string  `json:"planned_start_date"`
	PlannedEndDate   string  `json:"planned_end_date"`
	ProjectManagerID string  `json:"project_manager_id"`
	ChiefEngineerID  string  `json:"chief_engineer_id"`
}

// UpdateConstructionProjectInput represents input for updating a construction project
type UpdateConstructionProjectInput struct {
	Name             *string  `json:"name"`
	Description      *string  `json:"description"`
	Address          *string  `json:"address"`
	City             *string  `json:"city"`
	District         *string  `json:"district"`
	Region           *string  `json:"region"`
	ClientName       *string  `json:"client_name"`
	ClientContact    *string  `json:"client_contact"`
	ClientPhone      *string  `json:"client_phone"`
	ProjectType      *string  `json:"project_type"`
	BuildingType     *string  `json:"building_type"`
	TotalArea        *float64 `json:"total_area"`
	FloorsCount      *int     `json:"floors_count"`
	ContractAmount   *float64 `json:"contract_amount"`
	Currency         *string  `json:"currency"`
	ContractDate     *string  `json:"contract_date"`
	PlannedStartDate *string  `json:"planned_start_date"`
	PlannedEndDate   *string  `json:"planned_end_date"`
	ActualStartDate  *string  `json:"actual_start_date"`
	ActualEndDate    *string  `json:"actual_end_date"`
	Status           *string  `json:"status"`
	ProgressPercent  *float64 `json:"progress_percent"`
	ProjectManagerID *string  `json:"project_manager_id"`
	ChiefEngineerID  *string  `json:"chief_engineer_id"`
}

// =====================================================
// CONSTRUCTION BUILDING/BLOCK
// =====================================================

// ConstructionBuilding represents a building/block within a construction project
type ConstructionBuilding struct {
	ID        int64     `json:"id" db:"id"`
	TenantID  uuid.UUID `json:"tenant_id" db:"tenant_id"`
	ProjectID int64     `json:"project_id" db:"project_id"`

	Code        string         `json:"code" db:"code"`
	Name        string         `json:"name" db:"name"`
	Description sql.NullString `json:"description" db:"description"`

	// Building type
	BuildingType    sql.NullString `json:"building_type" db:"building_type"`
	BuildingPurpose sql.NullString `json:"building_purpose" db:"building_purpose"`

	// Physical characteristics
	FloorsCount       sql.NullInt32   `json:"floors_count" db:"floors_count"`
	FloorsUnderground sql.NullInt32   `json:"floors_underground" db:"floors_underground"`
	TotalArea         sql.NullFloat64 `json:"total_area" db:"total_area"`
	LivingArea        sql.NullFloat64 `json:"living_area" db:"living_area"`
	NonLivingArea     sql.NullFloat64 `json:"non_living_area" db:"non_living_area"`

	// Units info
	ApartmentsCount      sql.NullInt32 `json:"apartments_count" db:"apartments_count"`
	CommercialUnitsCount sql.NullInt32 `json:"commercial_units_count" db:"commercial_units_count"`
	ParkingSpots         sql.NullInt32 `json:"parking_spots" db:"parking_spots"`

	// Financial
	EstimatedCost sql.NullFloat64 `json:"estimated_cost" db:"estimated_cost"`
	ActualCost    sql.NullFloat64 `json:"actual_cost" db:"actual_cost"`
	Currency      string          `json:"currency" db:"currency"`

	// Timeline
	PlannedStartDate sql.NullTime `json:"planned_start_date" db:"planned_start_date"`
	PlannedEndDate   sql.NullTime `json:"planned_end_date" db:"planned_end_date"`
	ActualStartDate  sql.NullTime `json:"actual_start_date" db:"actual_start_date"`
	ActualEndDate    sql.NullTime `json:"actual_end_date" db:"actual_end_date"`

	// Status
	Status          string          `json:"status" db:"status"`
	ProgressPercent sql.NullFloat64 `json:"progress_percent" db:"progress_percent"`

	// Location
	GpsCoordinates      json.RawMessage `json:"gps_coordinates" db:"gps_coordinates"`
	LocationDescription sql.NullString  `json:"location_description" db:"location_description"`

	SortOrder   int       `json:"sort_order" db:"sort_order"`
	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	SectionsCount int     `json:"sections_count,omitempty" db:"sections_count"`
	TotalSmeta    float64 `json:"total_smeta,omitempty" db:"total_smeta"`
}

// CreateConstructionBuildingInput represents input for creating a building
type CreateConstructionBuildingInput struct {
	Code                 string  `json:"code" binding:"required"`
	Name                 string  `json:"name" binding:"required"`
	Description          string  `json:"description"`
	BuildingType         string  `json:"building_type"`
	BuildingPurpose      string  `json:"building_purpose"`
	FloorsCount          int     `json:"floors_count"`
	FloorsUnderground    int     `json:"floors_underground"`
	TotalArea            float64 `json:"total_area"`
	LivingArea           float64 `json:"living_area"`
	NonLivingArea        float64 `json:"non_living_area"`
	ApartmentsCount      int     `json:"apartments_count"`
	CommercialUnitsCount int     `json:"commercial_units_count"`
	ParkingSpots         int     `json:"parking_spots"`
	EstimatedCost        float64 `json:"estimated_cost"`
	PlannedStartDate     string  `json:"planned_start_date"`
	PlannedEndDate       string  `json:"planned_end_date"`
	SortOrder            int     `json:"sort_order"`
}

// UpdateConstructionBuildingInput represents input for updating a building
type UpdateConstructionBuildingInput struct {
	Name                 *string  `json:"name"`
	Description          *string  `json:"description"`
	BuildingType         *string  `json:"building_type"`
	BuildingPurpose      *string  `json:"building_purpose"`
	FloorsCount          *int     `json:"floors_count"`
	FloorsUnderground    *int     `json:"floors_underground"`
	TotalArea            *float64 `json:"total_area"`
	LivingArea           *float64 `json:"living_area"`
	NonLivingArea        *float64 `json:"non_living_area"`
	ApartmentsCount      *int     `json:"apartments_count"`
	CommercialUnitsCount *int     `json:"commercial_units_count"`
	ParkingSpots         *int     `json:"parking_spots"`
	EstimatedCost        *float64 `json:"estimated_cost"`
	PlannedStartDate     *string  `json:"planned_start_date"`
	PlannedEndDate       *string  `json:"planned_end_date"`
	ActualStartDate      *string  `json:"actual_start_date"`
	ActualEndDate        *string  `json:"actual_end_date"`
	Status               *string  `json:"status"`
	ProgressPercent      *float64 `json:"progress_percent"`
	SortOrder            *int     `json:"sort_order"`
}

// =====================================================
// SMETA SECTION
// =====================================================

// SmetaSection represents a section in smeta (estimate)
type SmetaSection struct {
	ID         int64          `json:"id" db:"id"`
	TenantID   uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	ProjectID  int64          `json:"project_id" db:"project_id"`
	BuildingID sql.NullInt64  `json:"building_id" db:"building_id"`
	ParentID   sql.NullInt64  `json:"parent_id" db:"parent_id"`
	Code       string         `json:"code" db:"code"`
	Name       string         `json:"name" db:"name"`
	NameUz     sql.NullString `json:"name_uz" db:"name_uz"`
	Description sql.NullString `json:"description" db:"description"`

	// Calculated totals
	TotalLaborHours    float64 `json:"total_labor_hours" db:"total_labor_hours"`
	TotalLaborCost     float64 `json:"total_labor_cost" db:"total_labor_cost"`
	TotalMaterialCost  float64 `json:"total_material_cost" db:"total_material_cost"`
	TotalEquipmentCost float64 `json:"total_equipment_cost" db:"total_equipment_cost"`
	TotalOverheadCost  float64 `json:"total_overhead_cost" db:"total_overhead_cost"`
	TotalCost          float64 `json:"total_cost" db:"total_cost"`

	SortOrder   int       `json:"sort_order" db:"sort_order"`
	Status      string    `json:"status" db:"status"`
	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	ItemsCount int `json:"items_count,omitempty" db:"items_count"`
}

// CreateSmetaSectionInput represents input for creating a smeta section
type CreateSmetaSectionInput struct {
	Code        string `json:"code" binding:"required"`
	Name        string `json:"name" binding:"required"`
	NameUz      string `json:"name_uz"`
	Description string `json:"description"`
	ParentID    int64  `json:"parent_id"`
	SortOrder   int    `json:"sort_order"`
}

// UpdateSmetaSectionInput represents input for updating a smeta section
type UpdateSmetaSectionInput struct {
	Name        *string `json:"name"`
	NameUz      *string `json:"name_uz"`
	Description *string `json:"description"`
	SortOrder   *int    `json:"sort_order"`
	Status      *string `json:"status"`
}

// =====================================================
// SMETA ITEM
// =====================================================

// SmetaItem represents an individual work item in smeta
type SmetaItem struct {
	ID        int64          `json:"id" db:"id"`
	TenantID  uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	SectionID int64          `json:"section_id" db:"section_id"`
	Code      sql.NullString `json:"code" db:"code"`
	SnipCode  sql.NullString `json:"snip_code" db:"snip_code"`
	Name      string         `json:"name" db:"name"`
	NameUz    sql.NullString `json:"name_uz" db:"name_uz"`
	Unit      string         `json:"unit" db:"unit"`

	// Quantities
	Quantity          float64 `json:"quantity" db:"quantity"`
	CompletedQuantity float64 `json:"completed_quantity" db:"completed_quantity"`

	// Pricing
	UnitPrice  sql.NullFloat64 `json:"unit_price" db:"unit_price"`
	TotalPrice sql.NullFloat64 `json:"total_price" db:"total_price"`

	// Cost breakdown
	LaborHours    float64 `json:"labor_hours" db:"labor_hours"`
	LaborCost     float64 `json:"labor_cost" db:"labor_cost"`
	MaterialCost  float64 `json:"material_cost" db:"material_cost"`
	EquipmentCost float64 `json:"equipment_cost" db:"equipment_cost"`
	TransportCost float64 `json:"transport_cost" db:"transport_cost"`
	OverheadCost  float64 `json:"overhead_cost" db:"overhead_cost"`

	// Status
	Status          string          `json:"status" db:"status"`
	ProgressPercent sql.NullFloat64 `json:"progress_percent" db:"progress_percent"`

	Notes         sql.NullString  `json:"notes" db:"notes"`
	TechnicalSpecs json.RawMessage `json:"technical_specs" db:"technical_specs"`
	SortOrder     int             `json:"sort_order" db:"sort_order"`
	CreatedDate   time.Time       `json:"created_date" db:"created_date"`
	UpdatedDate   time.Time       `json:"updated_date" db:"updated_date"`

	// Computed
	SectionName string `json:"section_name,omitempty" db:"section_name"`
}

// CreateSmetaItemInput represents input for creating a smeta item
type CreateSmetaItemInput struct {
	Code          string  `json:"code"`
	SnipCode      string  `json:"snip_code"`
	Name          string  `json:"name" binding:"required"`
	NameUz        string  `json:"name_uz"`
	Unit          string  `json:"unit" binding:"required"`
	Quantity      float64 `json:"quantity" binding:"required"`
	UnitPrice     float64 `json:"unit_price"`
	LaborHours    float64 `json:"labor_hours"`
	LaborCost     float64 `json:"labor_cost"`
	MaterialCost  float64 `json:"material_cost"`
	EquipmentCost float64 `json:"equipment_cost"`
	TransportCost float64 `json:"transport_cost"`
	OverheadCost  float64 `json:"overhead_cost"`
	Notes         string  `json:"notes"`
	SortOrder     int     `json:"sort_order"`
}

// UpdateSmetaItemInput represents input for updating a smeta item
type UpdateSmetaItemInput struct {
	Code              *string  `json:"code"`
	SnipCode          *string  `json:"snip_code"`
	Name              *string  `json:"name"`
	NameUz            *string  `json:"name_uz"`
	Unit              *string  `json:"unit"`
	Quantity          *float64 `json:"quantity"`
	CompletedQuantity *float64 `json:"completed_quantity"`
	UnitPrice         *float64 `json:"unit_price"`
	LaborHours        *float64 `json:"labor_hours"`
	LaborCost         *float64 `json:"labor_cost"`
	MaterialCost      *float64 `json:"material_cost"`
	EquipmentCost     *float64 `json:"equipment_cost"`
	TransportCost     *float64 `json:"transport_cost"`
	OverheadCost      *float64 `json:"overhead_cost"`
	Status            *string  `json:"status"`
	ProgressPercent   *float64 `json:"progress_percent"`
	Notes             *string  `json:"notes"`
	SortOrder         *int     `json:"sort_order"`
}

// =====================================================
// SMETA RESOURCE
// =====================================================

// SmetaResource represents a resource (material, labor, equipment) for a smeta item
type SmetaResource struct {
	ID           int64          `json:"id" db:"id"`
	TenantID     uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	SmetaItemID  int64          `json:"smeta_item_id" db:"smeta_item_id"`
	ResourceType string         `json:"resource_type" db:"resource_type"`
	Code         sql.NullString `json:"code" db:"code"`
	Name         string         `json:"name" db:"name"`
	NameUz       sql.NullString `json:"name_uz" db:"name_uz"`
	ProductID    uuid.NullUUID  `json:"product_id" db:"product_id"`
	Unit         string         `json:"unit" db:"unit"`

	QuantityPerUnit  sql.NullFloat64 `json:"quantity_per_unit" db:"quantity_per_unit"`
	TotalQuantity    sql.NullFloat64 `json:"total_quantity" db:"total_quantity"`
	ConsumedQuantity float64         `json:"consumed_quantity" db:"consumed_quantity"`

	UnitPrice  sql.NullFloat64 `json:"unit_price" db:"unit_price"`
	TotalPrice sql.NullFloat64 `json:"total_price" db:"total_price"`

	Notes       sql.NullString `json:"notes" db:"notes"`
	CreatedDate time.Time      `json:"created_date" db:"created_date"`
	UpdatedDate time.Time      `json:"updated_date" db:"updated_date"`

	// Computed
	ProductName string `json:"product_name,omitempty" db:"product_name"`
}

// CreateSmetaResourceInput represents input for creating a smeta resource
type CreateSmetaResourceInput struct {
	ResourceType    string  `json:"resource_type" binding:"required"`
	Code            string  `json:"code"`
	Name            string  `json:"name" binding:"required"`
	NameUz          string  `json:"name_uz"`
	ProductID       int64   `json:"product_id"`
	Unit            string  `json:"unit" binding:"required"`
	QuantityPerUnit float64 `json:"quantity_per_unit"`
	TotalQuantity   float64 `json:"total_quantity"`
	UnitPrice       float64 `json:"unit_price"`
	Notes           string  `json:"notes"`
}

// =====================================================
// WORK PROGRESS (KS-2)
// =====================================================

// ConstructionWorkProgress represents completed work progress (KS-2 style)
type ConstructionWorkProgress struct {
	ID          int64          `json:"id" db:"id"`
	TenantID    uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	ProjectID   int64          `json:"project_id" db:"project_id"`
	SmetaItemID int64          `json:"smeta_item_id" db:"smeta_item_id"`

	ReportNumber sql.NullString `json:"report_number" db:"report_number"`
	ReportDate   time.Time      `json:"report_date" db:"report_date"`
	PeriodStart  sql.NullTime   `json:"period_start" db:"period_start"`
	PeriodEnd    sql.NullTime   `json:"period_end" db:"period_end"`

	QuantityCompleted float64         `json:"quantity_completed" db:"quantity_completed"`
	UnitPrice         sql.NullFloat64 `json:"unit_price" db:"unit_price"`
	TotalAmount       sql.NullFloat64 `json:"total_amount" db:"total_amount"`

	VerifiedBy        uuid.NullUUID  `json:"verified_by" db:"verified_by"`
	VerificationDate  sql.NullTime   `json:"verification_date" db:"verification_date"`
	VerificationNotes sql.NullString `json:"verification_notes" db:"verification_notes"`

	Status      string     `json:"status" db:"status"`
	CreatedBy   *uuid.UUID `json:"created_by" db:"created_by"`
	CreatedDate time.Time  `json:"created_date" db:"created_date"`
	UpdatedDate time.Time  `json:"updated_date" db:"updated_date"`

	// Computed
	SmetaItemName  string `json:"smeta_item_name,omitempty" db:"smeta_item_name"`
	VerifierName   string `json:"verifier_name,omitempty" db:"verifier_name"`
}

// CreateWorkProgressInput represents input for creating work progress
type CreateWorkProgressInput struct {
	SmetaItemID       int64   `json:"smeta_item_id" binding:"required"`
	ReportNumber      string  `json:"report_number"`
	ReportDate        string  `json:"report_date" binding:"required"`
	PeriodStart       string  `json:"period_start"`
	PeriodEnd         string  `json:"period_end"`
	QuantityCompleted float64 `json:"quantity_completed" binding:"required"`
	UnitPrice         float64 `json:"unit_price"`
}

// =====================================================
// PHOTO REPORT
// =====================================================

// ConstructionPhotoReport represents a photo report from construction site
type ConstructionPhotoReport struct {
	ID          int64         `json:"id" db:"id"`
	TenantID    uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	ProjectID   int64         `json:"project_id" db:"project_id"`
	SmetaItemID sql.NullInt64 `json:"smeta_item_id" db:"smeta_item_id"`
	SectionID   sql.NullInt64 `json:"section_id" db:"section_id"`

	ReportDate          time.Time       `json:"report_date" db:"report_date"`
	ReportType          sql.NullString  `json:"report_type" db:"report_type"`
	Title               sql.NullString  `json:"title" db:"title"`
	Description         sql.NullString  `json:"description" db:"description"`
	LocationDescription sql.NullString  `json:"location_description" db:"location_description"`
	GpsLatitude         sql.NullFloat64 `json:"gps_latitude" db:"gps_latitude"`
	GpsLongitude        sql.NullFloat64 `json:"gps_longitude" db:"gps_longitude"`
	Weather             sql.NullString  `json:"weather" db:"weather"`
	Temperature         sql.NullFloat64 `json:"temperature" db:"temperature"`

	Photos json.RawMessage `json:"photos" db:"photos"`

	ReportedBy   uuid.NullUUID  `json:"reported_by" db:"reported_by"`
	ReviewedBy   uuid.NullUUID  `json:"reviewed_by" db:"reviewed_by"`
	ReviewDate   sql.NullTime   `json:"review_date" db:"review_date"`
	ReviewStatus string         `json:"review_status" db:"review_status"`
	ReviewNotes  sql.NullString `json:"review_notes" db:"review_notes"`

	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	ReporterName string `json:"reporter_name,omitempty" db:"reporter_name"`
	ReviewerName string `json:"reviewer_name,omitempty" db:"reviewer_name"`
}

// CreatePhotoReportInput represents input for creating a photo report
type CreatePhotoReportInput struct {
	SmetaItemID         int64    `json:"smeta_item_id"`
	SectionID           int64    `json:"section_id"`
	ReportDate          string   `json:"report_date" binding:"required"`
	ReportType          string   `json:"report_type"`
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	LocationDescription string   `json:"location_description"`
	GpsLatitude         float64  `json:"gps_latitude"`
	GpsLongitude        float64  `json:"gps_longitude"`
	Weather             string   `json:"weather"`
	Temperature         float64  `json:"temperature"`
	Photos              []string `json:"photos"`
	ReportedBy          int64    `json:"reported_by"`
}

// =====================================================
// DAILY REPORT
// =====================================================

// ConstructionDailyReport represents a daily construction report
type ConstructionDailyReport struct {
	ID        int64     `json:"id" db:"id"`
	TenantID  uuid.UUID `json:"tenant_id" db:"tenant_id"`
	ProjectID int64     `json:"project_id" db:"project_id"`

	ReportDate        time.Time       `json:"report_date" db:"report_date"`
	WeatherMorning    sql.NullString  `json:"weather_morning" db:"weather_morning"`
	WeatherAfternoon  sql.NullString  `json:"weather_afternoon" db:"weather_afternoon"`
	TemperatureMin    sql.NullFloat64 `json:"temperature_min" db:"temperature_min"`
	TemperatureMax    sql.NullFloat64 `json:"temperature_max" db:"temperature_max"`

	WorkSummary       sql.NullString `json:"work_summary" db:"work_summary"`
	IssuesEncountered sql.NullString `json:"issues_encountered" db:"issues_encountered"`
	SafetyNotes       sql.NullString `json:"safety_notes" db:"safety_notes"`

	WorkersCount    int             `json:"workers_count" db:"workers_count"`
	WorkersDetails  json.RawMessage `json:"workers_details" db:"workers_details"`
	EquipmentUsed   json.RawMessage `json:"equipment_used" db:"equipment_used"`
	MaterialsReceived json.RawMessage `json:"materials_received" db:"materials_received"`
	Visitors        json.RawMessage `json:"visitors" db:"visitors"`

	ReportedBy         uuid.NullUUID `json:"reported_by" db:"reported_by"`
	VerifiedBy         uuid.NullUUID `json:"verified_by" db:"verified_by"`
	VerificationStatus string        `json:"verification_status" db:"verification_status"`

	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	ReporterName string `json:"reporter_name,omitempty" db:"reporter_name"`
	VerifierName string `json:"verifier_name,omitempty" db:"verifier_name"`
}

// CreateDailyReportInput represents input for creating a daily report
type CreateDailyReportInput struct {
	ReportDate        string `json:"report_date" binding:"required"`
	WeatherMorning    string `json:"weather_morning"`
	WeatherAfternoon  string `json:"weather_afternoon"`
	TemperatureMin    float64 `json:"temperature_min"`
	TemperatureMax    float64 `json:"temperature_max"`
	WorkSummary       string `json:"work_summary"`
	IssuesEncountered string `json:"issues_encountered"`
	SafetyNotes       string `json:"safety_notes"`
	WorkersCount      int    `json:"workers_count"`
	WorkersDetails    string `json:"workers_details"`
	EquipmentUsed     string `json:"equipment_used"`
	MaterialsReceived string `json:"materials_received"`
	Visitors          string `json:"visitors"`
	ReportedBy        int64  `json:"reported_by"`
}

// =====================================================
// PROJECT VENDOR
// =====================================================

// ConstructionProjectVendor represents a vendor/subcontractor on a project
type ConstructionProjectVendor struct {
	ID        int64     `json:"id" db:"id"`
	TenantID  uuid.UUID `json:"tenant_id" db:"tenant_id"`
	ProjectID int64     `json:"project_id" db:"project_id"`
	VendorID  int64     `json:"vendor_id" db:"vendor_id"`

	ContractNumber sql.NullString  `json:"contract_number" db:"contract_number"`
	ContractDate   sql.NullTime    `json:"contract_date" db:"contract_date"`
	ContractAmount sql.NullFloat64 `json:"contract_amount" db:"contract_amount"`
	Currency       string          `json:"currency" db:"currency"`

	VendorType    string          `json:"vendor_type" db:"vendor_type"`
	WorkScope     sql.NullString  `json:"work_scope" db:"work_scope"`
	SmetaSections json.RawMessage `json:"smeta_sections" db:"smeta_sections"`

	ContactPerson sql.NullString `json:"contact_person" db:"contact_person"`
	ContactPhone  sql.NullString `json:"contact_phone" db:"contact_phone"`
	ContactEmail  sql.NullString `json:"contact_email" db:"contact_email"`

	Status        string  `json:"status" db:"status"`
	TotalOrdered  float64 `json:"total_ordered" db:"total_ordered"`
	TotalReceived float64 `json:"total_received" db:"total_received"`
	TotalInvoiced float64 `json:"total_invoiced" db:"total_invoiced"`
	TotalPaid     float64 `json:"total_paid" db:"total_paid"`
	BalanceDue    float64 `json:"balance_due" db:"balance_due"`

	StartDate sql.NullTime   `json:"start_date" db:"start_date"`
	EndDate   sql.NullTime   `json:"end_date" db:"end_date"`
	Notes     sql.NullString `json:"notes" db:"notes"`

	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	VendorName string `json:"vendor_name,omitempty" db:"vendor_name"`
}

// CreateProjectVendorInput represents input for adding a vendor to a project
type CreateProjectVendorInput struct {
	VendorID       int64   `json:"vendor_id" binding:"required"`
	ContractNumber string  `json:"contract_number"`
	ContractDate   string  `json:"contract_date"`
	ContractAmount float64 `json:"contract_amount"`
	Currency       string  `json:"currency"`
	VendorType     string  `json:"vendor_type" binding:"required"`
	WorkScope      string  `json:"work_scope"`
	ContactPerson  string  `json:"contact_person"`
	ContactPhone   string  `json:"contact_phone"`
	ContactEmail   string  `json:"contact_email"`
	StartDate      string  `json:"start_date"`
	EndDate        string  `json:"end_date"`
	Notes          string  `json:"notes"`
}

// =====================================================
// MATERIAL DELIVERY
// =====================================================

// ConstructionMaterialDelivery represents a material delivery to construction site
type ConstructionMaterialDelivery struct {
	ID        int64     `json:"id" db:"id"`
	TenantID  uuid.UUID `json:"tenant_id" db:"tenant_id"`
	ProjectID int64     `json:"project_id" db:"project_id"`
	VendorID  int64     `json:"vendor_id" db:"vendor_id"`

	DeliveryNumber  string    `json:"delivery_number" db:"delivery_number"`
	DeliveryDate    time.Time `json:"delivery_date" db:"delivery_date"`
	PurchaseOrderID uuid.NullUUID `json:"purchase_order_id" db:"purchase_order_id"`
	GoodsReceiptID  uuid.NullUUID `json:"goods_receipt_id" db:"goods_receipt_id"`

	VehicleNumber sql.NullString `json:"vehicle_number" db:"vehicle_number"`
	DriverName    sql.NullString `json:"driver_name" db:"driver_name"`
	WaybillNumber sql.NullString `json:"waybill_number" db:"waybill_number"`

	Items       json.RawMessage `json:"items" db:"items"`
	TotalAmount sql.NullFloat64 `json:"total_amount" db:"total_amount"`

	ReceivedBy   uuid.NullUUID `json:"received_by" db:"received_by"`
	ReceivedDate sql.NullTime  `json:"received_date" db:"received_date"`

	QualityStatus    string         `json:"quality_status" db:"quality_status"`
	QualityNotes     sql.NullString `json:"quality_notes" db:"quality_notes"`
	QualityCheckedBy uuid.NullUUID  `json:"quality_checked_by" db:"quality_checked_by"`

	Photos json.RawMessage `json:"photos" db:"photos"`
	Status string          `json:"status" db:"status"`
	Notes  sql.NullString  `json:"notes" db:"notes"`

	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	VendorName   string `json:"vendor_name,omitempty" db:"vendor_name"`
	ReceiverName string `json:"receiver_name,omitempty" db:"receiver_name"`
}

// CreateMaterialDeliveryInput represents input for creating a material delivery
type CreateMaterialDeliveryInput struct {
	VendorID        int64   `json:"vendor_id" binding:"required"`
	DeliveryNumber  string  `json:"delivery_number" binding:"required"`
	DeliveryDate    string  `json:"delivery_date" binding:"required"`
	PurchaseOrderID int64   `json:"purchase_order_id"`
	VehicleNumber   string  `json:"vehicle_number"`
	DriverName      string  `json:"driver_name"`
	WaybillNumber   string  `json:"waybill_number"`
	Items           string  `json:"items"`
	TotalAmount     float64 `json:"total_amount"`
	Notes           string  `json:"notes"`
}

// =====================================================
// SITE WAREHOUSE
// =====================================================

// ConstructionSiteWarehouse represents a warehouse at construction site
type ConstructionSiteWarehouse struct {
	ID          int64         `json:"id" db:"id"`
	TenantID    uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	ProjectID   int64         `json:"project_id" db:"project_id"`
	WarehouseID uuid.NullUUID `json:"warehouse_id" db:"warehouse_id"`

	Name                string          `json:"name" db:"name"`
	LocationDescription sql.NullString  `json:"location_description" db:"location_description"`
	GpsCoordinates      json.RawMessage `json:"gps_coordinates" db:"gps_coordinates"`

	WarehouseKeeperID uuid.NullUUID   `json:"warehouse_keeper_id" db:"warehouse_keeper_id"`
	TotalArea         sql.NullFloat64 `json:"total_area" db:"total_area"`
	CoveredArea       sql.NullFloat64 `json:"covered_area" db:"covered_area"`

	IsActive bool           `json:"is_active" db:"is_active"`
	Notes    sql.NullString `json:"notes" db:"notes"`

	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	WarehouseName string `json:"warehouse_name,omitempty" db:"warehouse_name"`
	KeeperName    string `json:"keeper_name,omitempty" db:"keeper_name"`
}

// CreateSiteWarehouseInput represents input for creating a site warehouse
type CreateSiteWarehouseInput struct {
	WarehouseID         int64   `json:"warehouse_id"`
	Name                string  `json:"name" binding:"required"`
	LocationDescription string  `json:"location_description"`
	GpsCoordinates      string  `json:"gps_coordinates"`
	WarehouseKeeperID   int64   `json:"warehouse_keeper_id"`
	TotalArea           float64 `json:"total_area"`
	CoveredArea         float64 `json:"covered_area"`
	Notes               string  `json:"notes"`
}
