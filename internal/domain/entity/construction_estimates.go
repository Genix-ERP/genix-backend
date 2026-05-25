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
	//
	// QuantityOverride (migration 342) switches the sub-line into MANUAL mode:
	// when TRUE, Quantity is what the user entered directly (e.g. "10 hours"
	// for a machine) and is NOT re-derived from parent.quantity × NormRate.
	// NormRate remains persisted for reference.
	//
	// MaterialType (migration 347) classifies a `resource_type='material'`
	// row for the Form 2 transport+storage overhead engine per
	// Госкомархитектстрой Письмо № 352/11-05. One of:
	//   standard | equipment | cable | metal | import
	// Default 'standard'. No-op for non-material rows.
	ParentLineID     sql.NullInt64 `json:"parent_line_id" db:"parent_line_id"`
	NormRate         float64       `json:"norm_rate" db:"norm_rate"`
	SublineSeq       int           `json:"subline_seq" db:"subline_seq"`
	QuantityOverride bool          `json:"quantity_override" db:"quantity_override"`
	MaterialType     string        `json:"material_type" db:"material_type"`

	// OriginalQuantity / OriginalUnitRate (migration 349) capture the row's
	// values at first INSERT and never change after that. The Smeta UI
	// surfaces them as "Reset to original" affordances on each line.
	OriginalQuantity float64 `json:"original_quantity" db:"original_quantity"`
	OriginalUnitRate float64 `json:"original_unit_rate" db:"original_unit_rate"`

	// ImportedQuantity / ImportedTotal (migration 400) preserve the
	// Ресурс XLSX file's "Количество" and "Сметная стоимость в базисном
	// уровне" columns verbatim. These are DISPLAY-ONLY — the rest of the
	// system (cost cascade, NORMA pill, FAKT ledger, Reja vs Fakt budget
	// summary, top-ups, approval workflow) deliberately ignores them so
	// existing business logic is not affected. Nullable on the DB side
	// because non-resurs imports and rows that predate the migration
	// leave them empty. The MarshalJSON below emits the numeric value or
	// `null` (never 0) so the frontend can tell "no value imported" apart
	// from "imported zero" and render an em-dash accordingly.
	ImportedQuantity sql.NullFloat64 `json:"-" db:"imported_quantity"`
	ImportedTotal    sql.NullFloat64 `json:"-" db:"imported_total"`

	// v2 Bosqichlar workflow (migration 353).
	ApprovalStatus string  `json:"approval_status" db:"approval_status"`
	DoneQuantity   float64 `json:"done_quantity" db:"done_quantity"`

	SortOrder   int       `json:"sort_order" db:"sort_order"`
	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed
	WBSCode string `json:"wbs_code,omitempty" db:"wbs_code"`
	WBSName string `json:"wbs_name,omitempty" db:"wbs_name"`

	// Resource top-ups (migration 358) — additional purchases for this
	// resource sub-line, populated by getEstimateLines via a secondary
	// query. Empty for non-resource rows.
	Topups []ResourceTopup `json:"topups,omitempty"`
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
		QuantityOverride bool        `json:"quantity_override"`
		MaterialType     string      `json:"material_type"`
		OriginalQuantity float64     `json:"original_quantity"`
		OriginalUnitRate float64     `json:"original_unit_rate"`
		ImportedQuantity interface{} `json:"imported_quantity"`
		ImportedTotal    interface{} `json:"imported_total"`
		ApprovalStatus   string      `json:"approval_status"`
		DoneQuantity     float64     `json:"done_quantity"`
		SortOrder        int         `json:"sort_order"`
		CreatedDate   time.Time       `json:"created_date"`
		UpdatedDate   time.Time       `json:"updated_date"`
		WBSCode       string          `json:"wbs_code,omitempty"`
		WBSName       string          `json:"wbs_name,omitempty"`
		Topups        []ResourceTopup `json:"topups,omitempty"`
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
		QuantityOverride: l.QuantityOverride,
		MaterialType:     l.MaterialType,
		OriginalQuantity: l.OriginalQuantity,
		OriginalUnitRate: l.OriginalUnitRate,
		// Emit numeric value when the row carries an imported figure,
		// `null` when it doesn't. The frontend distinguishes "no imported
		// value" (em-dash) from "imported zero" via this distinction.
		ImportedQuantity: nullFloat64Value(l.ImportedQuantity),
		ImportedTotal:    nullFloat64Value(l.ImportedTotal),
		ApprovalStatus:   l.ApprovalStatus,
		DoneQuantity:     l.DoneQuantity,
		SortOrder:        l.SortOrder,
		CreatedDate:   l.CreatedDate,
		UpdatedDate:   l.UpdatedDate,
		WBSCode:       l.WBSCode,
		WBSName:       l.WBSName,
		Topups:        l.Topups,
	})
}

// =====================================================
// CONSTRUCTION RESOURCE TOPUP (migration 358)
// =====================================================
//
// A top-up represents an additional purchase for an estimate resource
// line — typically when the originally planned quantity ran short and
// the supplier's price has since changed. The smeta plan stays
// untouched (immutable for audit) and the line's effective total
// becomes plan + Σ(topup.extra_quantity × topup.new_price).

type ResourceTopup struct {
	ID             int64         `json:"id" db:"id"`
	TenantID       uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	EstimateLineID int64         `json:"estimate_line_id" db:"estimate_line_id"`
	ExtraQuantity  float64       `json:"extra_quantity" db:"extra_quantity"`
	NewPrice       float64       `json:"new_price" db:"new_price"`
	OrderedAt      time.Time     `json:"ordered_at" db:"ordered_at"`
	Note           string        `json:"note" db:"note"`
	CreatedBy      uuid.NullUUID `json:"created_by" db:"created_by"`
	CreatedDate    time.Time     `json:"created_date" db:"created_date"`
}

func (t ResourceTopup) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID             int64       `json:"id"`
		TenantID       uuid.UUID   `json:"tenant_id"`
		EstimateLineID int64       `json:"estimate_line_id"`
		ExtraQuantity  float64     `json:"extra_quantity"`
		NewPrice       float64     `json:"new_price"`
		OrderedAt      string      `json:"ordered_at"`
		Note           string      `json:"note"`
		CreatedBy      interface{} `json:"created_by"`
		CreatedDate    time.Time   `json:"created_date"`
	}{
		ID:             t.ID,
		TenantID:       t.TenantID,
		EstimateLineID: t.EstimateLineID,
		ExtraQuantity:  t.ExtraQuantity,
		NewPrice:       t.NewPrice,
		OrderedAt:      t.OrderedAt.Format("2006-01-02"),
		Note:           t.Note,
		CreatedBy:      nullUUIDValue(t.CreatedBy),
		CreatedDate:    t.CreatedDate,
	})
}

type CreateResourceTopupInput struct {
	ExtraQuantity float64 `json:"extra_quantity" binding:"required"`
	NewPrice      float64 `json:"new_price" binding:"required"`
	OrderedAt     string  `json:"ordered_at"`
	Note          string  `json:"note"`
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
	// MaterialType (migration 350) — sub-bucket for resource_type='material'.
	// Allowed values: 'standard' | 'equipment' | 'cable' | 'metal' | 'import'.
	// Empty string falls back to 'standard' on insert.
	MaterialType     string `json:"material_type"`
	ParentItemNumber string `json:"parent_item_number"`
	SortOrder        int    `json:"sort_order"`

	// OriginalQuantity (migration 349 anchor). When provided, the bulk
	// insert writes it directly — bypassing the trigger that would
	// otherwise default to Quantity. Used by Единич/Ресурс template
	// imports so the planned norma from the source file survives even
	// though the live Quantity ledger column is forced to 0.
	// *float64 so callers can omit (NULL → trigger fills from Quantity)
	// without colliding with the explicit-zero case.
	OriginalQuantity *float64 `json:"original_quantity"`

	// Sub-line support (migration 332). When ParentLineID != 0, the server:
	//   - looks up the parent
	//   - auto-generates ItemNumber = "{parent.item_number}-{next_subline_seq}"
	//     if the caller didn't supply one
	//   - computes Quantity = parent.quantity × NormRate  UNLESS the client
	//     set QuantityOverride = true, in which case the user-supplied
	//     Quantity is persisted as-is (migration 342).
	ParentLineID     int64   `json:"parent_line_id"`
	NormRate         float64 `json:"norm_rate"`
	UnitPrice        float64 `json:"unit_price"` // convenience — mapped to the rate column that matches resource_type
	QuantityOverride bool    `json:"quantity_override"`

	// ImportedQuantity / ImportedTotal (migration 400). Verbatim "Количество"
	// and "Сметная стоимость" totals from the imported Ресурс XLSX — stored
	// for display only and ignored by every calc path. Pointers so callers
	// can omit them (NULL on insert) without colliding with the explicit-zero
	// case (a row whose file figure is genuinely 0).
	ImportedQuantity *float64 `json:"imported_quantity"`
	ImportedTotal    *float64 `json:"imported_total"`
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
	// Imported budget totals captured from the Ресурс sheet's bottom
	// summary block (parseResurs in SmetaImportModal.jsx). Persisted onto
	// construction_estimate via migration 369 so the Reja vs Fakt page
	// can show the file's grand total instead of summing per-line plans.
	//
	// BudgetTotal     = ИТОГО ПРЯМЫЕ ЗАТРАТЫ (the headline figure)
	// MaterialBudget  = ИТОГО ПО СТРОИТЕЛЬНЫМ МАТЕРИАЛАМ
	// TransportBudget = ТРАНСПОРТНЫЕ РАСХОДЫ НА МАТЕРИАЛЫ (project-wide)
	//
	// All three are optional — non-Ресурс imports leave them at 0 and the
	// handler preserves the previous values rather than zeroing them out.
	BudgetTotal     float64 `json:"budget_total"`
	MaterialBudget  float64 `json:"material_budget"`
	TransportBudget float64 `json:"transport_budget"`
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

	// OriginalQuantity — the smeta-anchored "NORMA" pill shown on each
	// work card (migration 349). Defaults to the row's first Quantity at
	// import time and the UI usually treats it as immutable. The full
	// edit modal exposes it as a Norma input so the user can correct an
	// imported figure when the smeta itself was wrong.
	OriginalQuantity *float64 `json:"original_quantity"`

	// Optional edits to line metadata (used by the full edit modal).
	Code         *string `json:"code"`
	ItemNumber   *string `json:"item_number"`
	ResourceType *string `json:"resource_type"`

	// Sub-line fields. When NormRate is supplied for a row that has a parent,
	// Quantity is re-derived from parent.quantity × NormRate — UNLESS
	// QuantityOverride is true on the row (or is being set to true in this
	// request), in which case the user-supplied Quantity wins (migration 342).
	NormRate         *float64 `json:"norm_rate"`
	UnitPrice        *float64 `json:"unit_price"`
	QuantityOverride *bool    `json:"quantity_override"`
	MaterialType     *string  `json:"material_type"` // standard|equipment|cable|metal|import
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
