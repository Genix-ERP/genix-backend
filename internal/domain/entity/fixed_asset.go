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
	Location               *string      `json:"location,omitempty" db:"location"`
	CustodianID            *uuid.UUID   `json:"custodian_id,omitempty" db:"custodian_id"`
	CustodianName          *string      `json:"custodian_name,omitempty" db:"custodian_name"`
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
	DisposalReason string  `json:"reason,omitempty"`
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
	Location                string    `json:"location,omitempty"`
	CustodianID             string    `json:"custodian_id,omitempty"`
	CustodianName           string    `json:"custodian_name,omitempty"`
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
	ID                      uuid.UUID `json:"id"`
	Code                    string    `json:"code"`
	Name                    string    `json:"name"`
	Description             string    `json:"description,omitempty"`
	DepreciationMethod      string    `json:"depreciation_method"`
	DefaultUsefulLifeMonths int       `json:"default_useful_life_months"`
	IsActive                bool      `json:"is_active"`
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
	return resp
}
