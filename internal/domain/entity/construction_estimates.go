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
	ID        int64     `json:"id" db:"id"`
	TenantID  uuid.UUID `json:"tenant_id" db:"tenant_id"`
	ProjectID int64     `json:"project_id" db:"project_id"`
	Version   int       `json:"version" db:"version"`
	Name      string    `json:"name" db:"name"`
	State     string    `json:"state" db:"state"`
	IsCurrent bool      `json:"is_current" db:"is_current"`

	OverheadPct float64 `json:"overhead_pct" db:"overhead_pct"`
	ProfitPct   float64 `json:"profit_pct" db:"profit_pct"`
	VatPct      float64 `json:"vat_pct" db:"vat_pct"`

	AmountDirect float64 `json:"amount_direct" db:"amount_direct"`
	AmountTotal  float64 `json:"amount_total" db:"amount_total"`

	ApprovedBy   uuid.NullUUID `json:"approved_by" db:"approved_by"`
	ApprovedDate sql.NullTime  `json:"approved_date" db:"approved_date"`

	CreatedBy   uuid.NullUUID `json:"created_by" db:"created_by"`
	CreatedDate time.Time     `json:"created_date" db:"created_date"`
	UpdatedDate time.Time     `json:"updated_date" db:"updated_date"`

	// Computed
	LinesCount   int    `json:"lines_count,omitempty" db:"lines_count"`
	ApprovedName string `json:"approved_name,omitempty" db:"approved_name"`
	CreatedName  string `json:"created_name,omitempty" db:"created_name"`
}

func (e ConstructionEstimate) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID           int64       `json:"id"`
		TenantID     uuid.UUID   `json:"tenant_id"`
		ProjectID    int64       `json:"project_id"`
		Version      int         `json:"version"`
		Name         string      `json:"name"`
		State        string      `json:"state"`
		IsCurrent    bool        `json:"is_current"`
		OverheadPct  float64     `json:"overhead_pct"`
		ProfitPct    float64     `json:"profit_pct"`
		VatPct       float64     `json:"vat_pct"`
		AmountDirect float64     `json:"amount_direct"`
		AmountTotal  float64     `json:"amount_total"`
		ApprovedBy   interface{} `json:"approved_by"`
		ApprovedDate interface{} `json:"approved_date"`
		CreatedBy    interface{} `json:"created_by"`
		CreatedDate  time.Time   `json:"created_date"`
		UpdatedDate  time.Time   `json:"updated_date"`
		LinesCount   int         `json:"lines_count,omitempty"`
		ApprovedName string      `json:"approved_name,omitempty"`
		CreatedName  string      `json:"created_name,omitempty"`
	}{
		ID:           e.ID,
		TenantID:     e.TenantID,
		ProjectID:    e.ProjectID,
		Version:      e.Version,
		Name:         e.Name,
		State:        e.State,
		IsCurrent:    e.IsCurrent,
		OverheadPct:  e.OverheadPct,
		ProfitPct:    e.ProfitPct,
		VatPct:       e.VatPct,
		AmountDirect: e.AmountDirect,
		AmountTotal:  e.AmountTotal,
		ApprovedBy:   nullUUIDValue(e.ApprovedBy),
		ApprovedDate: nullTimeValue(e.ApprovedDate),
		CreatedBy:    nullUUIDValue(e.CreatedBy),
		CreatedDate:  e.CreatedDate,
		UpdatedDate:  e.UpdatedDate,
		LinesCount:   e.LinesCount,
		ApprovedName: e.ApprovedName,
		CreatedName:  e.CreatedName,
	})
}

type CreateEstimateInput struct {
	Name        string  `json:"name" binding:"required"`
	OverheadPct float64 `json:"overhead_pct"`
	ProfitPct   float64 `json:"profit_pct"`
	VatPct      float64 `json:"vat_pct"`
}

type UpdateEstimateInput struct {
	Name        *string  `json:"name"`
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
		SortOrder     int         `json:"sort_order"`
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
		SortOrder:     l.SortOrder,
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
	SortOrder     int     `json:"sort_order"`
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
}

// =====================================================
// CONSTRUCTION DAILY LOG (WBS-linked progress)
// =====================================================

type ConstructionDailyLog struct {
	ID         int64         `json:"id" db:"id"`
	TenantID   uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	ProjectID  int64         `json:"project_id" db:"project_id"`
	BuildingID sql.NullInt64 `json:"building_id" db:"building_id"`
	WBSID      int64         `json:"wbs_id" db:"wbs_id"`

	Date          time.Time `json:"date" db:"date"`
	QuantityDone  float64   `json:"quantity_done" db:"quantity_done"`
	UOM           string    `json:"uom" db:"uom"`
	CumulativeQty float64   `json:"cumulative_qty" db:"cumulative_qty"`

	WorkersCount int            `json:"workers_count" db:"workers_count"`
	Weather      sql.NullString `json:"weather" db:"weather"`
	Description  sql.NullString `json:"description" db:"description"`
	Issues       sql.NullString `json:"issues" db:"issues"`

	ReportedBy uuid.NullUUID `json:"reported_by" db:"reported_by"`

	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	WBSCode      string `json:"wbs_code,omitempty" db:"wbs_code"`
	WBSName      string `json:"wbs_name,omitempty" db:"wbs_name"`
	BuildingName string `json:"building_name,omitempty" db:"building_name"`
	ReportedName string `json:"reported_name,omitempty" db:"reported_name"`
}

func (d ConstructionDailyLog) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID            int64       `json:"id"`
		TenantID      uuid.UUID   `json:"tenant_id"`
		ProjectID     int64       `json:"project_id"`
		BuildingID    interface{} `json:"building_id"`
		WBSID         int64       `json:"wbs_id"`
		Date          time.Time   `json:"date"`
		QuantityDone  float64     `json:"quantity_done"`
		UOM           string      `json:"uom"`
		CumulativeQty float64     `json:"cumulative_qty"`
		WorkersCount  int         `json:"workers_count"`
		Weather       interface{} `json:"weather"`
		Description   interface{} `json:"description"`
		Issues        interface{} `json:"issues"`
		ReportedBy    interface{} `json:"reported_by"`
		CreatedDate   time.Time   `json:"created_date"`
		UpdatedDate   time.Time   `json:"updated_date"`
		WBSCode       string      `json:"wbs_code,omitempty"`
		WBSName       string      `json:"wbs_name,omitempty"`
		BuildingName  string      `json:"building_name,omitempty"`
		ReportedName  string      `json:"reported_name,omitempty"`
	}{
		ID:            d.ID,
		TenantID:      d.TenantID,
		ProjectID:     d.ProjectID,
		BuildingID:    nullInt64Value(d.BuildingID),
		WBSID:         d.WBSID,
		Date:          d.Date,
		QuantityDone:  d.QuantityDone,
		UOM:           d.UOM,
		CumulativeQty: d.CumulativeQty,
		WorkersCount:  d.WorkersCount,
		Weather:       nullStringValue(d.Weather),
		Description:   nullStringValue(d.Description),
		Issues:        nullStringValue(d.Issues),
		ReportedBy:    nullUUIDValue(d.ReportedBy),
		CreatedDate:   d.CreatedDate,
		UpdatedDate:   d.UpdatedDate,
		WBSCode:       d.WBSCode,
		WBSName:       d.WBSName,
		BuildingName:  d.BuildingName,
		ReportedName:  d.ReportedName,
	})
}

type CreateDailyLogInput struct {
	BuildingID   int64   `json:"building_id"`
	WBSID        int64   `json:"wbs_id" binding:"required"`
	Date         string  `json:"date" binding:"required"`
	QuantityDone float64 `json:"quantity_done" binding:"required"`
	UOM          string  `json:"uom"`
	WorkersCount int     `json:"workers_count"`
	Weather      string  `json:"weather"`
	Description  string  `json:"description"`
	Issues       string  `json:"issues"`
}

type UpdateDailyLogInput struct {
	BuildingID   *int64   `json:"building_id"`
	WBSID        *int64   `json:"wbs_id"`
	Date         *string  `json:"date"`
	QuantityDone *float64 `json:"quantity_done"`
	UOM          *string  `json:"uom"`
	WorkersCount *int     `json:"workers_count"`
	Weather      *string  `json:"weather"`
	Description  *string  `json:"description"`
	Issues       *string  `json:"issues"`
}
