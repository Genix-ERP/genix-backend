package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION ESTIMATE (Versioned Smeta)
// =====================================================

type ConstructionEstimate struct {
	ID         int64         `json:"id" db:"id"`
	TenantID   uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	ProjectID  int64         `json:"project_id" db:"project_id"`
	BuildingID sql.NullInt64 `json:"building_id" db:"building_id"`
	Version    int           `json:"version" db:"version"`
	Name       string        `json:"name" db:"name"`
	State      string        `json:"state" db:"state"`
	IsCurrent  bool          `json:"is_current" db:"is_current"`

	OverheadPct float64 `json:"overhead_pct" db:"overhead_pct"`
	ProfitPct   float64 `json:"profit_pct" db:"profit_pct"`
	VatPct      float64 `json:"vat_pct" db:"vat_pct"`

	AmountDirect float64 `json:"amount_direct" db:"amount_direct"`
	AmountTotal  float64 `json:"amount_total" db:"amount_total"`

	ApprovedBy   uuid.NullUUID `json:"approved_by" db:"approved_by"`
	ApprovedDate sql.NullTime  `json:"approved_date" db:"approved_date"`

	SourceType string `json:"source_type" db:"source_type"`

	SubcontractID sql.NullInt64 `json:"subcontract_id" db:"subcontract_id"`

	CreatedBy   uuid.NullUUID `json:"created_by" db:"created_by"`
	CreatedDate time.Time     `json:"created_date" db:"created_date"`
	UpdatedDate time.Time     `json:"updated_date" db:"updated_date"`

	// Computed
	LinesCount   int    `json:"lines_count,omitempty" db:"lines_count"`
	ApprovedName string `json:"approved_name,omitempty" db:"approved_name"`
	CreatedName  string `json:"created_name,omitempty" db:"created_name"`
	BuildingName    string `json:"building_name,omitempty" db:"building_name"`
	SubcontractName string `json:"subcontract_name,omitempty" db:"subcontract_name"`
}

func (e ConstructionEstimate) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID           int64       `json:"id"`
		TenantID     uuid.UUID   `json:"tenant_id"`
		ProjectID    int64       `json:"project_id"`
		BuildingID   interface{} `json:"building_id"`
		Version      int         `json:"version"`
		Name         string      `json:"name"`
		State        string      `json:"state"`
		IsCurrent    bool        `json:"is_current"`
		OverheadPct  float64     `json:"overhead_pct"`
		ProfitPct    float64     `json:"profit_pct"`
		VatPct       float64     `json:"vat_pct"`
		AmountDirect float64     `json:"amount_direct"`
		AmountTotal    float64     `json:"amount_total"`
		SourceType     string      `json:"source_type"`
		SubcontractID  interface{} `json:"subcontract_id"`
		ApprovedBy     interface{} `json:"approved_by"`
		ApprovedDate interface{} `json:"approved_date"`
		CreatedBy    interface{} `json:"created_by"`
		CreatedDate  time.Time   `json:"created_date"`
		UpdatedDate  time.Time   `json:"updated_date"`
		LinesCount   int         `json:"lines_count,omitempty"`
		ApprovedName string      `json:"approved_name,omitempty"`
		CreatedName     string      `json:"created_name,omitempty"`
		BuildingName    string      `json:"building_name,omitempty"`
		SubcontractName string      `json:"subcontract_name,omitempty"`
	}{
		ID:           e.ID,
		TenantID:     e.TenantID,
		ProjectID:    e.ProjectID,
		BuildingID:   nullInt64Value(e.BuildingID),
		Version:      e.Version,
		Name:         e.Name,
		State:        e.State,
		IsCurrent:    e.IsCurrent,
		OverheadPct:  e.OverheadPct,
		ProfitPct:    e.ProfitPct,
		VatPct:       e.VatPct,
		AmountDirect: e.AmountDirect,
		AmountTotal:    e.AmountTotal,
		SourceType:     e.SourceType,
		SubcontractID:  nullInt64Value(e.SubcontractID),
		ApprovedBy:     nullUUIDValue(e.ApprovedBy),
		ApprovedDate: nullTimeValue(e.ApprovedDate),
		CreatedBy:    nullUUIDValue(e.CreatedBy),
		CreatedDate:  e.CreatedDate,
		UpdatedDate:  e.UpdatedDate,
		LinesCount:   e.LinesCount,
		ApprovedName: e.ApprovedName,
		CreatedName:     e.CreatedName,
		BuildingName:    e.BuildingName,
		SubcontractName: e.SubcontractName,
	})
}

type CreateEstimateInput struct {
	Name          string  `json:"name" binding:"required"`
	BuildingID    int64   `json:"building_id"`
	OverheadPct   float64 `json:"overhead_pct"`
	ProfitPct     float64 `json:"profit_pct"`
	VatPct        float64 `json:"vat_pct"`
	SourceType    string  `json:"source_type"`
	SubcontractID int64   `json:"subcontract_id"`
}

type UpdateEstimateInput struct {
	Name        *string  `json:"name"`
	BuildingID  *int64   `json:"building_id"`
	OverheadPct *float64 `json:"overhead_pct"`
	ProfitPct   *float64 `json:"profit_pct"`
	VatPct      *float64 `json:"vat_pct"`
}

// =====================================================
// CONSTRUCTION ESTIMATE LINE
// =====================================================

type ConstructionEstimateLine struct {
	ID         int64         `json:"id" db:"id"`
	TenantID   uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	EstimateID int64         `json:"estimate_id" db:"estimate_id"`
	WBSID      sql.NullInt64 `json:"wbs_id" db:"wbs_id"`

	Name     string  `json:"name" db:"name"`
	UOM      string  `json:"uom" db:"uom"`
	Quantity float64 `json:"quantity" db:"quantity"`

	MaterialRate  float64 `json:"material_rate" db:"material_rate"`
	LaborRate     float64 `json:"labor_rate" db:"labor_rate"`
	EquipmentRate float64 `json:"equipment_rate" db:"equipment_rate"`

	UnitRate    float64 `json:"unit_rate" db:"unit_rate"`
	TotalAmount float64 `json:"total_amount" db:"total_amount"`

	ActualAmount float64 `json:"actual_amount" db:"actual_amount"`

	Code             string `json:"code" db:"code"`
	ItemNumber       string `json:"item_number" db:"item_number"`
	ResourceType     string `json:"resource_type" db:"resource_type"`
	ParentItemNumber string `json:"parent_item_number" db:"parent_item_number"`

	// Sub-line (подкатор) support — migration 332.
	// When ParentLineID is set, this row is a resource breakdown of its parent.
	// NormRate is the ШРНК норма; sub-line quantity is stored as
	// parent.quantity × NormRate (denormalized on write).
	ParentLineID sql.NullInt64 `json:"parent_line_id" db:"parent_line_id"`
	NormRate     float64       `json:"norm_rate" db:"norm_rate"`
	SublineSeq   int           `json:"subline_seq" db:"subline_seq"`

	SortOrder   int       `json:"sort_order" db:"sort_order"`
	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	WBSCode string `json:"wbs_code,omitempty" db:"wbs_code"`
	WBSName string `json:"wbs_name,omitempty" db:"wbs_name"`
}

func (l ConstructionEstimateLine) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID            int64       `json:"id"`
		TenantID      uuid.UUID   `json:"tenant_id"`
		EstimateID    int64       `json:"estimate_id"`
		WBSID         interface{} `json:"wbs_id"`
		Name          string      `json:"name"`
		UOM           string      `json:"uom"`
		Quantity      float64     `json:"quantity"`
		MaterialRate  float64     `json:"material_rate"`
		LaborRate     float64     `json:"labor_rate"`
		EquipmentRate float64     `json:"equipment_rate"`
		UnitRate      float64     `json:"unit_rate"`
		TotalAmount   float64     `json:"total_amount"`
		ActualAmount  float64     `json:"actual_amount"`
		Code             string      `json:"code"`
		ItemNumber       string      `json:"item_number"`
		ResourceType     string      `json:"resource_type"`
		ParentItemNumber string      `json:"parent_item_number"`
		ParentLineID     interface{} `json:"parent_line_id"`
		NormRate         float64     `json:"norm_rate"`
		SublineSeq       int         `json:"subline_seq"`
		SortOrder        int         `json:"sort_order"`
		CreatedDate   time.Time   `json:"created_date"`
		UpdatedDate   time.Time   `json:"updated_date"`
		WBSCode       string      `json:"wbs_code,omitempty"`
		WBSName       string      `json:"wbs_name,omitempty"`
	}{
		ID:            l.ID,
		TenantID:      l.TenantID,
		EstimateID:    l.EstimateID,
		WBSID:         nullInt64Value(l.WBSID),
		Name:          l.Name,
		UOM:           l.UOM,
		Quantity:      l.Quantity,
		MaterialRate:  l.MaterialRate,
		LaborRate:     l.LaborRate,
		EquipmentRate: l.EquipmentRate,
		UnitRate:      l.UnitRate,
		TotalAmount:   l.TotalAmount,
		ActualAmount:  l.ActualAmount,
		Code:             l.Code,
		ItemNumber:       l.ItemNumber,
		ResourceType:     l.ResourceType,
		ParentItemNumber: l.ParentItemNumber,
		ParentLineID:     nullInt64Value(l.ParentLineID),
		NormRate:         l.NormRate,
		SublineSeq:       l.SublineSeq,
		SortOrder:        l.SortOrder,
		CreatedDate:   l.CreatedDate,
		UpdatedDate:   l.UpdatedDate,
		WBSCode:       l.WBSCode,
		WBSName:       l.WBSName,
	})
}

type CreateEstimateLineInput struct {
	WBSID         int64   `json:"wbs_id"`
	Name          string  `json:"name" binding:"required"`
	UOM           string  `json:"uom"`
	Quantity      float64 `json:"quantity"`
	MaterialRate  float64 `json:"material_rate"`
	LaborRate     float64 `json:"labor_rate"`
	EquipmentRate float64 `json:"equipment_rate"`
	Code             string `json:"code"`
	ItemNumber       string `json:"item_number"`
	ResourceType     string `json:"resource_type"`
	ParentItemNumber string `json:"parent_item_number"`
	SortOrder        int    `json:"sort_order"`

	// Sub-line support (migration 332). When ParentLineID != 0, the server:
	//   - looks up the parent
	//   - auto-generates ItemNumber = "{parent.item_number}-{next_subline_seq}"
	//     if the caller didn't supply one
	//   - computes Quantity = parent.quantity × NormRate
	ParentLineID int64   `json:"parent_line_id"`
	NormRate     float64 `json:"norm_rate"`
	UnitPrice    float64 `json:"unit_price"` // convenience — mapped to the rate column that matches resource_type
}

type BulkCreateEstimateLinesInput struct {
	Lines      []CreateEstimateLineInput `json:"lines" binding:"required"`
	Replace    bool                      `json:"replace"`
	SourceType string                    `json:"source_type"`
	// SourceFileName identifies the Excel file the user uploaded. Used by
	// autoCreateForma2FromEstimate to dedupe Forma 2 drafts: multiple
	// estimate types extracted from the SAME file produce ONE merged
	// Forma 2, while imports from different files stay separate. See
	// migration 339 for the matching column on construction_act.
	SourceFileName string `json:"source_file_name"`
}

type UpdateEstimateLineInput struct {
	WBSID         *int64   `json:"wbs_id"`
	Name          *string  `json:"name"`
	UOM           *string  `json:"uom"`
	Quantity      *float64 `json:"quantity"`
	MaterialRate  *float64 `json:"material_rate"`
	LaborRate     *float64 `json:"labor_rate"`
	EquipmentRate *float64 `json:"equipment_rate"`
	SortOrder     *int     `json:"sort_order"`
	ActualAmount  *float64 `json:"actual_amount"`

	// Optional edits to line metadata (used by the full edit modal).
	Code         *string `json:"code"`
	ItemNumber   *string `json:"item_number"`
	ResourceType *string `json:"resource_type"`

	// Sub-line fields. When NormRate is supplied for a row that has a parent,
	// Quantity is re-derived from parent.quantity × NormRate.
	NormRate  *float64 `json:"norm_rate"`
	UnitPrice *float64 `json:"unit_price"`
}

// =====================================================
// CONSTRUCTION DAILY LOG (WBS-linked progress)
// =====================================================

type ConstructionDailyLog struct {
	ID         int64         `json:"id" db:"id"`
	TenantID   uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	ProjectID  int64         `json:"project_id" db:"project_id"`
	BuildingID sql.NullInt64 `json:"building_id" db:"building_id"`
	StageID    sql.NullInt64 `json:"stage_id" db:"stage_id"`

	Date    time.Time      `json:"date" db:"date"`
	EndDate sql.NullString `json:"end_date" db:"end_date"`

	WorkersCount   int            `json:"workers_count" db:"workers_count"`
	ExpectedBudget float64        `json:"expected_budget" db:"expected_budget"`
	Weather        sql.NullString `json:"weather" db:"weather"`
	Description    sql.NullString `json:"description" db:"description"`
	Issues         sql.NullString `json:"issues" db:"issues"`

	ReportedBy uuid.NullUUID `json:"reported_by" db:"reported_by"`

	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	BuildingName       string  `json:"building_name,omitempty" db:"building_name"`
	StageName          string  `json:"stage_name,omitempty" db:"stage_name"`
	StageProgress      float64 `json:"stage_progress" db:"stage_progress"`
	StagePlannedBudget float64 `json:"stage_planned_budget" db:"stage_planned_budget"`
	StageMaterialTotal float64 `json:"stage_material_total" db:"stage_material_total"`
	ReportedName       string  `json:"reported_name,omitempty" db:"reported_name"`
}

func (d ConstructionDailyLog) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID             int64       `json:"id"`
		TenantID       uuid.UUID   `json:"tenant_id"`
		ProjectID      int64       `json:"project_id"`
		BuildingID     interface{} `json:"building_id"`
		StageID        interface{} `json:"stage_id"`
		Date           time.Time   `json:"date"`
		EndDate        interface{} `json:"end_date"`
		WorkersCount   int         `json:"workers_count"`
		ExpectedBudget float64     `json:"expected_budget"`
		Weather        interface{} `json:"weather"`
		Description    interface{} `json:"description"`
		Issues         interface{} `json:"issues"`
		ReportedBy     interface{} `json:"reported_by"`
		CreatedDate    time.Time   `json:"created_date"`
		UpdatedDate    time.Time   `json:"updated_date"`
		BuildingName       string      `json:"building_name,omitempty"`
		StageName          string      `json:"stage_name,omitempty"`
		StageProgress      float64     `json:"stage_progress"`
		StagePlannedBudget float64     `json:"stage_planned_budget"`
		StageMaterialTotal float64     `json:"stage_material_total"`
		ReportedName       string      `json:"reported_name,omitempty"`
	}{
		ID:                 d.ID,
		TenantID:           d.TenantID,
		ProjectID:          d.ProjectID,
		BuildingID:         nullInt64Value(d.BuildingID),
		StageID:            nullInt64Value(d.StageID),
		Date:               d.Date,
		EndDate:            nullStringValue(d.EndDate),
		WorkersCount:       d.WorkersCount,
		ExpectedBudget:     d.ExpectedBudget,
		Weather:            nullStringValue(d.Weather),
		Description:        nullStringValue(d.Description),
		Issues:             nullStringValue(d.Issues),
		ReportedBy:         nullUUIDValue(d.ReportedBy),
		CreatedDate:        d.CreatedDate,
		UpdatedDate:        d.UpdatedDate,
		BuildingName:       d.BuildingName,
		StageName:          d.StageName,
		StageProgress:      d.StageProgress,
		StagePlannedBudget: d.StagePlannedBudget,
		StageMaterialTotal: d.StageMaterialTotal,
		ReportedName:       d.ReportedName,
	})
}

type CreateDailyLogInput struct {
	BuildingID     int64   `json:"building_id"`
	StageID        int64   `json:"stage_id"`
	Date           string  `json:"date" binding:"required"`
	EndDate        string  `json:"end_date"`
	WorkersCount   int     `json:"workers_count"`
	ExpectedBudget float64 `json:"expected_budget"`
	Weather        string  `json:"weather"`
	Description    string  `json:"description"`
	Issues         string  `json:"issues"`
}

type UpdateDailyLogInput struct {
	BuildingID     *int64   `json:"building_id"`
	StageID        *int64   `json:"stage_id"`
	Date           *string  `json:"date"`
	EndDate        *string  `json:"end_date"`
	WorkersCount   *int     `json:"workers_count"`
	ExpectedBudget *float64 `json:"expected_budget"`
	Weather        *string  `json:"weather"`
	Description    *string  `json:"description"`
	Issues         *string  `json:"issues"`
}

// =====================================================
// CONSTRUCTION ESTIMATE SUMMARY (Свод cross-tab)
// =====================================================

type ConstructionEstimateSummary struct {
	ID             int64     `json:"id" db:"id"`
	TenantID       uuid.UUID `json:"tenant_id" db:"tenant_id"`
	ProjectID      int64     `json:"project_id" db:"project_id"`
	BatchID        string    `json:"batch_id" db:"batch_id"`
	RowNumber      int       `json:"row_number" db:"row_number"`
	CategoryName   string    `json:"category_name" db:"category_name"`
	BuildingColumn string    `json:"building_column" db:"building_column"`
	Amount         float64   `json:"amount" db:"amount"`
	CreatedDate    time.Time `json:"created_date" db:"created_date"`
}

type EstimateSummaryRowInput struct {
	RowNumber      int     `json:"row_number"`
	CategoryName   string  `json:"category_name" binding:"required"`
	BuildingColumn string  `json:"building_column" binding:"required"`
	Amount         float64 `json:"amount"`
}

type BulkCreateEstimateSummaryInput struct {
	Rows []EstimateSummaryRowInput `json:"rows" binding:"required"`
}
