package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// AssetCategory represents a fixed asset category
type AssetCategory struct {
	ID                     uuid.UUID  `json:"id" db:"id"`
	TenantID               uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code                   string     `json:"code" db:"code"`
	Name                   string     `json:"name" db:"name"`
	Description            *string    `json:"description,omitempty" db:"description"`
	DepreciationMethod     string     `json:"depreciation_method" db:"depreciation_method"`
	DefaultUsefulLifeMonths int       `json:"default_useful_life_months" db:"default_useful_life_months"`
	AssetAccountID         *uuid.UUID `json:"asset_account_id,omitempty" db:"asset_account_id"`
	DepreciationAccountID  *uuid.UUID `json:"depreciation_account_id,omitempty" db:"depreciation_account_id"`
	ExpenseAccountID       *uuid.UUID `json:"expense_account_id,omitempty" db:"expense_account_id"`
	IsActive               bool       `json:"is_active" db:"is_active"`
	CreatedAt              time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at" db:"updated_at"`
}

// FixedAsset represents a fixed asset
type FixedAsset struct {
	ID                     uuid.UUID    `json:"id" db:"id"`
	TenantID               uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	AssetCode              string       `json:"asset_code" db:"asset_code"`
	Name                   string       `json:"name" db:"name"`
	Description            *string      `json:"description,omitempty" db:"description"`
	CategoryID             *uuid.UUID   `json:"category_id,omitempty" db:"category_id"`
	CategoryName           *string      `json:"category_name,omitempty" db:"category_name"`
	SerialNumber           *string      `json:"serial_number,omitempty" db:"serial_number"`
	AcquisitionDate        time.Time    `json:"acquisition_date" db:"acquisition_date"`
	AcquisitionCost        float64      `json:"acquisition_cost" db:"acquisition_cost"`
	SalvageValue           float64      `json:"salvage_value" db:"salvage_value"`
	UsefulLifeMonths       int          `json:"useful_life_months" db:"useful_life_months"`
	DepreciationMethod     string       `json:"depreciation_method" db:"depreciation_method"`
	AccumulatedDepreciation float64     `json:"accumulated_depreciation" db:"accumulated_depreciation"`
	BookValue              *float64     `json:"book_value,omitempty" db:"book_value"`
	CurrentValue           *float64     `json:"current_value,omitempty" db:"current_value"`
	RemainingMonths        *int         `json:"remaining_months,omitempty" db:"remaining_months"`
	MonthlyDepr            *float64     `json:"monthly_depr,omitempty" db:"monthly_depr"`
	LastDeprDate           *time.Time   `json:"last_depr_date,omitempty" db:"last_depr_date"`
	Location               *string      `json:"location,omitempty" db:"location"`
	CustodianID            *uuid.UUID   `json:"custodian_id,omitempty" db:"custodian_id"`
	CustodianName          *string      `json:"custodian_name,omitempty" db:"custodian_name"`
	SupplierID             *uuid.UUID   `json:"supplier_id,omitempty" db:"supplier_id"`
	SupplierName           *string      `json:"supplier_name,omitempty" db:"supplier_name"`
	DocumentNumber         *string      `json:"document_number,omitempty" db:"document_number"`
	DocumentDate           *time.Time   `json:"document_date,omitempty" db:"document_date"`
	PaymentMethod          *string      `json:"payment_method,omitempty" db:"payment_method"`
	TotalPaid              *float64     `json:"total_paid,omitempty" db:"total_paid"`
	WarrantyExpiry         *time.Time   `json:"warranty_expiry,omitempty" db:"warranty_expiry"`
	Status                 string       `json:"status" db:"status"`
	DisposalDate           *time.Time   `json:"disposal_date,omitempty" db:"disposal_date"`
	DisposalAmount         *float64     `json:"disposal_amount,omitempty" db:"disposal_amount"`
	DisposalReason         *string      `json:"disposal_reason,omitempty" db:"disposal_reason"`
	Notes                  *string      `json:"notes,omitempty" db:"notes"`
	CreatedBy              *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt              time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt              sql.NullTime `json:"-" db:"deleted_at"`
}

// DepreciationEntry represents a depreciation entry for an asset
type DepreciationEntry struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	TenantID            uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	AssetID             uuid.UUID  `json:"asset_id" db:"asset_id"`
	Period              string     `json:"period" db:"period"`
	DepreciationDate    time.Time  `json:"depreciation_date" db:"depreciation_date"`
	DepreciationAmount  float64    `json:"depreciation_amount" db:"depreciation_amount"`
	AccumulatedTotal    float64    `json:"accumulated_total" db:"accumulated_total"`
	BookValueAfter      float64    `json:"book_value_after" db:"book_value_after"`
	DepreciationMethod  string     `json:"depreciation_method" db:"depreciation_method"`
	JournalEntryID      *uuid.UUID `json:"journal_entry_id,omitempty" db:"journal_entry_id"`
	Notes               *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
}

// CreateFixedAssetInput represents input for creating a fixed asset
type CreateFixedAssetInput struct {
	AssetCode          string  `json:"code"`
	Name               string  `json:"name" binding:"required"`
	Description        string  `json:"description,omitempty"`
	CategoryID         string  `json:"category_id,omitempty"`
	Category           string  `json:"category,omitempty"` // Allow passing category name
	SerialNumber       string  `json:"serial_number,omitempty"`
	AcquisitionDate    string  `json:"acquisition_date" binding:"required"`
	AcquisitionCost    float64 `json:"acquisition_cost" binding:"required"`
	SalvageValue       float64 `json:"salvage_value"`
	UsefulLifeMonths   int     `json:"useful_life_months" binding:"required"`
	DepreciationMethod string  `json:"depreciation_method" binding:"required"`
	Location           string  `json:"location,omitempty"`
	CustodianID        string  `json:"custodian_id,omitempty"`
	CustodianName      string  `json:"custodian_name,omitempty"`
	WarrantyExpiry     string  `json:"warranty_expiry,omitempty"`
	Notes              string  `json:"notes,omitempty"`
	SupplierID         string  `json:"supplier_id,omitempty"`
	SupplierName       string  `json:"supplier_name,omitempty"`
	DocumentNumber     string  `json:"document_number,omitempty"`
	DocumentDate       string  `json:"document_date,omitempty"`
	PaymentMethod      string  `json:"payment_method,omitempty"` // cash, bank, credit
}

// UpdateFixedAssetInput represents input for updating a fixed asset
type UpdateFixedAssetInput struct {
	Name               *string  `json:"name,omitempty"`
	Description        *string  `json:"description,omitempty"`
	CategoryID         *string  `json:"category_id,omitempty"`
	SerialNumber       *string  `json:"serial_number,omitempty"`
	AcquisitionDate    *string  `json:"acquisition_date,omitempty"`
	AcquisitionCost    *float64 `json:"acquisition_cost,omitempty"`
	SalvageValue       *float64 `json:"salvage_value,omitempty"`
	UsefulLifeMonths   *int     `json:"useful_life_months,omitempty"`
	DepreciationMethod *string  `json:"depreciation_method,omitempty"`
	Location           *string  `json:"location,omitempty"`
	CustodianID        *string  `json:"custodian_id,omitempty"`
	CustodianName      *string  `json:"custodian_name,omitempty"`
	WarrantyExpiry     *string  `json:"warranty_expiry,omitempty"`
	Status             *string  `json:"status,omitempty"`
	Notes              *string  `json:"notes,omitempty"`
}

// DisposeAssetInput represents input for disposing an asset
type DisposeAssetInput struct {
	DisposalDate   string  `json:"disposal_date" binding:"required"`
	DisposalAmount float64 `json:"disposal_amount"`
	DisposalReason string  `json:"reason" binding:"required"` // sold, destroyed, expired, stolen
}

// CreateDepreciationInput represents input for creating depreciation entries
type CreateDepreciationInput struct {
	Period string `json:"period" binding:"required"` // YYYY-MM format
}

// FixedAssetResponse represents the API response for a fixed asset
type FixedAssetResponse struct {
	ID                      uuid.UUID `json:"id"`
	AssetCode               string    `json:"code"`
	Name                    string    `json:"name"`
	Description             string    `json:"description,omitempty"`
	CategoryID              string    `json:"category_id,omitempty"`
	CategoryName            string    `json:"category,omitempty"`
	SerialNumber            string    `json:"serial_number,omitempty"`
	AcquisitionDate         string    `json:"acquisition_date"`
	AcquisitionCost         float64   `json:"acquisition_cost"`
	SalvageValue            float64   `json:"salvage_value"`
	UsefulLifeMonths        int       `json:"useful_life_months"`
	DepreciationMethod      string    `json:"depreciation_method"`
	AccumulatedDepreciation float64   `json:"accumulated_depreciation"`
	BookValue               float64   `json:"book_value"`
	CurrentValue            float64   `json:"current_value"`
	RemainingMonths         int       `json:"remaining_months"`
	MonthlyDepr             float64   `json:"monthly_depr"`
	LastDeprDate            string    `json:"last_depr_date,omitempty"`
	Location                string    `json:"location,omitempty"`
	CustodianID             string    `json:"custodian_id,omitempty"`
	CustodianName           string    `json:"custodian_name,omitempty"`
	SupplierID              string    `json:"supplier_id,omitempty"`
	SupplierName            string    `json:"supplier_name,omitempty"`
	DocumentNumber          string    `json:"document_number,omitempty"`
	DocumentDate            string    `json:"document_date,omitempty"`
	PaymentMethod           string    `json:"payment_method,omitempty"`
	TotalPaid               float64   `json:"total_paid"`
	RemainingDebt           float64   `json:"remaining_debt"`
	WarrantyExpiry          string    `json:"warranty_expiry,omitempty"`
	Status                  string    `json:"status"`
	DisposalDate            string    `json:"disposal_date,omitempty"`
	DisposalAmount          float64   `json:"disposal_amount,omitempty"`
	DisposalReason          string    `json:"disposal_reason,omitempty"`
	Notes                   string    `json:"notes,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// ToResponse converts FixedAsset to FixedAssetResponse
func (a *FixedAsset) ToResponse() *FixedAssetResponse {
	resp := &FixedAssetResponse{
		ID:                      a.ID,
		AssetCode:               a.AssetCode,
		Name:                    a.Name,
		AcquisitionDate:         a.AcquisitionDate.Format("2006-01-02"),
		AcquisitionCost:         a.AcquisitionCost,
		SalvageValue:            a.SalvageValue,
		UsefulLifeMonths:        a.UsefulLifeMonths,
		DepreciationMethod:      a.DepreciationMethod,
		AccumulatedDepreciation: a.AccumulatedDepreciation,
		BookValue:               a.AcquisitionCost - a.AccumulatedDepreciation,
		Status:                  a.Status,
		CreatedAt:               a.CreatedAt,
		UpdatedAt:               a.UpdatedAt,
	}

	if a.BookValue != nil {
		resp.BookValue = *a.BookValue
	}
	if a.CurrentValue != nil {
		resp.CurrentValue = *a.CurrentValue
	} else {
		resp.CurrentValue = resp.BookValue
	}
	if a.RemainingMonths != nil {
		resp.RemainingMonths = *a.RemainingMonths
	}
	if a.MonthlyDepr != nil {
		resp.MonthlyDepr = *a.MonthlyDepr
	}
	if a.LastDeprDate != nil {
		resp.LastDeprDate = a.LastDeprDate.Format("2006-01-02")
	}
	if a.Description != nil {
		resp.Description = *a.Description
	}
	if a.CategoryID != nil {
		resp.CategoryID = a.CategoryID.String()
	}
	if a.CategoryName != nil {
		resp.CategoryName = *a.CategoryName
	}
	if a.SerialNumber != nil {
		resp.SerialNumber = *a.SerialNumber
	}
	if a.Location != nil {
		resp.Location = *a.Location
	}
	if a.CustodianID != nil {
		resp.CustodianID = a.CustodianID.String()
	}
	if a.CustodianName != nil {
		resp.CustodianName = *a.CustodianName
	}
	if a.SupplierID != nil {
		resp.SupplierID = a.SupplierID.String()
	}
	if a.SupplierName != nil {
		resp.SupplierName = *a.SupplierName
	}
	if a.DocumentNumber != nil {
		resp.DocumentNumber = *a.DocumentNumber
	}
	if a.DocumentDate != nil {
		resp.DocumentDate = a.DocumentDate.Format("2006-01-02")
	}
	if a.PaymentMethod != nil {
		resp.PaymentMethod = *a.PaymentMethod
	}
	if a.TotalPaid != nil {
		resp.TotalPaid = *a.TotalPaid
	}
	if resp.PaymentMethod == "credit" {
		resp.RemainingDebt = a.AcquisitionCost - resp.TotalPaid
		if resp.RemainingDebt < 0 {
			resp.RemainingDebt = 0
		}
	}
	if a.WarrantyExpiry != nil {
		resp.WarrantyExpiry = a.WarrantyExpiry.Format("2006-01-02")
	}
	if a.DisposalDate != nil {
		resp.DisposalDate = a.DisposalDate.Format("2006-01-02")
	}
	if a.DisposalAmount != nil {
		resp.DisposalAmount = *a.DisposalAmount
	}
	if a.DisposalReason != nil {
		resp.DisposalReason = *a.DisposalReason
	}
	if a.Notes != nil {
		resp.Notes = *a.Notes
	}

	return resp
}

// DepreciationEntryResponse represents the API response for a depreciation entry
type DepreciationEntryResponse struct {
	ID                 uuid.UUID `json:"id"`
	AssetID            uuid.UUID `json:"asset_id"`
	Period             string    `json:"period"`
	DepreciationDate   string    `json:"depreciation_date"`
	DepreciationAmount float64   `json:"amount"`
	AccumulatedTotal   float64   `json:"accumulated_total"`
	BookValueAfter     float64   `json:"book_value_after"`
	DepreciationMethod string    `json:"method"`
	CreatedAt          time.Time `json:"created_at"`
}

// ToResponse converts DepreciationEntry to DepreciationEntryResponse
func (d *DepreciationEntry) ToResponse() *DepreciationEntryResponse {
	return &DepreciationEntryResponse{
		ID:                 d.ID,
		AssetID:            d.AssetID,
		Period:             d.Period,
		DepreciationDate:   d.DepreciationDate.Format("2006-01-02"),
		DepreciationAmount: d.DepreciationAmount,
		AccumulatedTotal:   d.AccumulatedTotal,
		BookValueAfter:     d.BookValueAfter,
		DepreciationMethod: d.DepreciationMethod,
		CreatedAt:          d.CreatedAt,
	}
}

// AssetCategoryResponse represents the API response for an asset category
type AssetCategoryResponse struct {
	ID                      uuid.UUID  `json:"id"`
	Code                    string     `json:"code"`
	Name                    string     `json:"name"`
	Description             string     `json:"description,omitempty"`
	DepreciationMethod      string     `json:"depreciation_method"`
	DefaultUsefulLifeMonths int        `json:"default_useful_life_months"`
	AssetAccountID          *uuid.UUID `json:"asset_account_id,omitempty"`
	DepreciationAccountID   *uuid.UUID `json:"depreciation_account_id,omitempty"`
	ExpenseAccountID        *uuid.UUID `json:"expense_account_id,omitempty"`
	IsActive                bool       `json:"is_active"`
}

// ToResponse converts AssetCategory to AssetCategoryResponse
func (c *AssetCategory) ToResponse() *AssetCategoryResponse {
	resp := &AssetCategoryResponse{
		ID:                      c.ID,
		Code:                    c.Code,
		Name:                    c.Name,
		DepreciationMethod:      c.DepreciationMethod,
		DefaultUsefulLifeMonths: c.DefaultUsefulLifeMonths,
		IsActive:                c.IsActive,
	}
	if c.Description != nil {
		resp.Description = *c.Description
	}
	resp.AssetAccountID = c.AssetAccountID
	resp.DepreciationAccountID = c.DepreciationAccountID
	resp.ExpenseAccountID = c.ExpenseAccountID
	return resp
}

// AssetMaintenance represents a maintenance record for a fixed asset
type AssetMaintenance struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	TenantID            uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID      *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`
	AssetID             uuid.UUID  `json:"asset_id" db:"asset_id"`
	MaintenanceType     string     `json:"maintenance_type" db:"maintenance_type"`
	ServiceDate         time.Time  `json:"service_date" db:"service_date"`
	Cost                float64    `json:"cost" db:"cost"`
	ValueBefore         float64    `json:"value_before" db:"value_before"`
	UsefulLifeBefore    int        `json:"useful_life_before" db:"useful_life_before"`
	MonthlyDeprBefore   float64    `json:"monthly_depr_before" db:"monthly_depr_before"`
	ValueAfter          float64    `json:"value_after" db:"value_after"`
	UsefulLifeAfter     int        `json:"useful_life_after" db:"useful_life_after"`
	MonthlyDeprAfter    float64    `json:"monthly_depr_after" db:"monthly_depr_after"`
	LifeExtensionMonths int        `json:"life_extension_months" db:"life_extension_months"`
	ValueIncrease       float64    `json:"value_increase" db:"value_increase"`
	Description         *string    `json:"description,omitempty" db:"description"`
	PerformedBy         *string    `json:"performed_by,omitempty" db:"performed_by"`
	DocumentNumber      *string    `json:"document_number,omitempty" db:"document_number"`
	NextServiceDate     *time.Time `json:"next_service_date,omitempty" db:"next_service_date"`
	CreatedBy           *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
}

// RecordMaintenanceInput represents input for recording asset maintenance
type RecordMaintenanceInput struct {
	MaintenanceType    string  `json:"maintenance_type" binding:"required"`
	ServiceDate        string  `json:"service_date" binding:"required"`
	Cost               float64 `json:"cost"`
	ExtensionMonths    int     `json:"extension_months"`
	Description        string  `json:"description"`
	PerformedBy        string  `json:"performed_by"`
	DocumentNumber     string  `json:"document_number"`
	PaymentAccountCode string  `json:"payment_account_code"`
	NextServiceDate    string  `json:"next_service_date"`
}

// AssetMaintenanceResponse represents the API response for asset maintenance
type AssetMaintenanceResponse struct {
	ID                  uuid.UUID `json:"id"`
	AssetID             uuid.UUID `json:"asset_id"`
	MaintenanceType     string    `json:"maintenance_type"`
	ServiceDate         string    `json:"service_date"`
	Cost                float64   `json:"cost"`
	ValueBefore         float64   `json:"value_before"`
	UsefulLifeBefore    int       `json:"useful_life_before"`
	MonthlyDeprBefore   float64   `json:"monthly_depr_before"`
	ValueAfter          float64   `json:"value_after"`
	UsefulLifeAfter     int       `json:"useful_life_after"`
	MonthlyDeprAfter    float64   `json:"monthly_depr_after"`
	LifeExtensionMonths int       `json:"life_extension_months"`
	ValueIncrease       float64   `json:"value_increase"`
	Description         string    `json:"description,omitempty"`
	PerformedBy         string    `json:"performed_by,omitempty"`
	DocumentNumber      string    `json:"document_number,omitempty"`
	NextServiceDate     string    `json:"next_service_date,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// ToMaintenanceResponse converts AssetMaintenance to AssetMaintenanceResponse
func (m *AssetMaintenance) ToMaintenanceResponse() *AssetMaintenanceResponse {
	resp := &AssetMaintenanceResponse{
		ID:                  m.ID,
		AssetID:             m.AssetID,
		MaintenanceType:     m.MaintenanceType,
		ServiceDate:         m.ServiceDate.Format("2006-01-02"),
		Cost:                m.Cost,
		ValueBefore:         m.ValueBefore,
		UsefulLifeBefore:    m.UsefulLifeBefore,
		MonthlyDeprBefore:   m.MonthlyDeprBefore,
		ValueAfter:          m.ValueAfter,
		UsefulLifeAfter:     m.UsefulLifeAfter,
		MonthlyDeprAfter:    m.MonthlyDeprAfter,
		LifeExtensionMonths: m.LifeExtensionMonths,
		ValueIncrease:       m.ValueIncrease,
		CreatedAt:           m.CreatedAt,
	}
	if m.Description != nil {
		resp.Description = *m.Description
	}
	if m.PerformedBy != nil {
		resp.PerformedBy = *m.PerformedBy
	}
	if m.DocumentNumber != nil {
		resp.DocumentNumber = *m.DocumentNumber
	}
	if m.NextServiceDate != nil {
		resp.NextServiceDate = m.NextServiceDate.Format("2006-01-02")
	}
	return resp
}

// AssetPayment represents a credit installment payment for a fixed asset
type AssetPayment struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`
	AssetID        uuid.UUID  `json:"asset_id" db:"asset_id"`
	Amount         float64    `json:"amount" db:"amount"`
	Method         string     `json:"method" db:"method"` // cash, bank
	PaidAt         time.Time  `json:"paid_at" db:"paid_at"`
	Note           *string    `json:"note,omitempty" db:"note"`
	JournalEntryID *uuid.UUID `json:"journal_entry_id,omitempty" db:"journal_entry_id"`
	CreatedBy      *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// RecordAssetPaymentInput represents input for recording an asset payment
type RecordAssetPaymentInput struct {
	Amount float64 `json:"amount" binding:"required"`
	Method string  `json:"method" binding:"required"` // cash, bank
	PaidAt string  `json:"paid_at" binding:"required"`
	Note   string  `json:"note,omitempty"`
}

// AssetPaymentResponse represents the API response for an asset payment
type AssetPaymentResponse struct {
	ID        uuid.UUID `json:"id"`
	AssetID   uuid.UUID `json:"asset_id"`
	Amount    float64   `json:"amount"`
	Method    string    `json:"method"`
	PaidAt    string    `json:"paid_at"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ToPaymentResponse converts AssetPayment to AssetPaymentResponse
func (p *AssetPayment) ToPaymentResponse() *AssetPaymentResponse {
	resp := &AssetPaymentResponse{
		ID:        p.ID,
		AssetID:   p.AssetID,
		Amount:    p.Amount,
		Method:    p.Method,
		PaidAt:    p.PaidAt.Format("2006-01-02"),
		CreatedAt: p.CreatedAt,
	}
	if p.Note != nil {
		resp.Note = *p.Note
	}
	return resp
}

// CreateAssetCategoryInput represents input for creating an asset category
type CreateAssetCategoryInput struct {
	Code                    string `json:"code" binding:"required"`
	Name                    string `json:"name" binding:"required"`
	Description             string `json:"description,omitempty"`
	DepreciationMethod      string `json:"depreciation_method" binding:"required"`
	DefaultUsefulLifeMonths int    `json:"default_useful_life_months" binding:"required"`
}
