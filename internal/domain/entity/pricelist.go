package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PricelistType represents the type of pricelist
type PricelistType string

const (
	PricelistTypeSale     PricelistType = "sale"
	PricelistTypePurchase PricelistType = "purchase"
)

// AppliedOn represents what the pricelist item applies to
type AppliedOn string

const (
	AppliedOnAllProducts     AppliedOn = "all_products"
	AppliedOnProductCategory AppliedOn = "product_category"
	AppliedOnProduct         AppliedOn = "product"
	AppliedOnProductVariant  AppliedOn = "product_variant"
)

// ComputePrice represents how the price is computed
type ComputePrice string

const (
	ComputePriceFixed      ComputePrice = "fixed_price"
	ComputePricePercentage ComputePrice = "percentage"
	ComputePriceFormula    ComputePrice = "formula"
)

// PriceBase represents the base price for formula computation
type PriceBase string

const (
	PriceBaseListPrice      PriceBase = "list_price"
	PriceBaseCostPrice      PriceBase = "cost_price"
	PriceBaseOtherPricelist PriceBase = "other_pricelist"
)

// Pricelist represents a pricing strategy
type Pricelist struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	TenantID     uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Name         string          `json:"name" db:"name"`
	Code         *string         `json:"code,omitempty" db:"code"`
	Description  *string         `json:"description,omitempty" db:"description"`
	CurrencyID   *uuid.UUID      `json:"currency_id,omitempty" db:"currency_id"`
	Type         PricelistType   `json:"pricelist_type" db:"pricelist_type"`
	StartDate    *time.Time      `json:"start_date,omitempty" db:"start_date"`
	EndDate      *time.Time      `json:"end_date,omitempty" db:"end_date"`
	Priority     int             `json:"priority" db:"priority"`
	ShowDiscount bool            `json:"show_discount" db:"show_discount"`
	CountryCodes json.RawMessage `json:"country_codes" db:"country_codes"`
	IsActive     bool            `json:"is_active" db:"is_active"`
	IsDefault    bool            `json:"is_default" db:"is_default"`
	CreatedBy    *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`

	// Relationships
	Currency *Currency       `json:"currency,omitempty"`
	Items    []PricelistItem `json:"items,omitempty"`
}

// PricelistItem represents a rule/item in a pricelist
type PricelistItem struct {
	ID              uuid.UUID    `json:"id" db:"id"`
	PricelistID     uuid.UUID    `json:"pricelist_id" db:"pricelist_id"`
	AppliedOn       AppliedOn    `json:"applied_on" db:"applied_on"`
	ProductID       *uuid.UUID   `json:"product_id,omitempty" db:"product_id"`
	CategoryID      *uuid.UUID   `json:"category_id,omitempty" db:"category_id"`
	VariantID       *uuid.UUID   `json:"variant_id,omitempty" db:"variant_id"`
	MinQuantity     float64      `json:"min_quantity" db:"min_quantity"`
	ComputePrice    ComputePrice `json:"compute_price" db:"compute_price"`
	FixedPrice      *float64     `json:"fixed_price,omitempty" db:"fixed_price"`
	PercentPrice    float64      `json:"percent_price" db:"percent_price"`
	PriceBase       PriceBase    `json:"price_base" db:"price_base"`
	BasePricelistID *uuid.UUID   `json:"base_pricelist_id,omitempty" db:"base_pricelist_id"`
	PriceDiscount   float64      `json:"price_discount" db:"price_discount"`
	PriceSurcharge  float64      `json:"price_surcharge" db:"price_surcharge"`
	PriceRound      float64      `json:"price_round" db:"price_round"`
	PriceMinMargin  float64      `json:"price_min_margin" db:"price_min_margin"`
	PriceMaxMargin  float64      `json:"price_max_margin" db:"price_max_margin"`
	DateStart       *time.Time   `json:"date_start,omitempty" db:"date_start"`
	DateEnd         *time.Time   `json:"date_end,omitempty" db:"date_end"`
	Sequence        int          `json:"sequence" db:"sequence"`
	IsActive        bool         `json:"is_active" db:"is_active"`
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`

	// Relationships
	Product  *Product         `json:"product,omitempty"`
	Category *ProductCategory `json:"category,omitempty"`
}

// CustomerPricelist links a customer to a pricelist
type CustomerPricelist struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	TenantID    uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	CustomerID  uuid.UUID  `json:"customer_id" db:"customer_id"`
	PricelistID uuid.UUID  `json:"pricelist_id" db:"pricelist_id"`
	Priority    int        `json:"priority" db:"priority"`
	StartDate   *time.Time `json:"start_date,omitempty" db:"start_date"`
	EndDate     *time.Time `json:"end_date,omitempty" db:"end_date"`
	IsActive    bool       `json:"is_active" db:"is_active"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`

	// Relationships
	Customer  *Contact   `json:"customer,omitempty"`
	Pricelist *Pricelist `json:"pricelist,omitempty"`
}

// CreatePricelistInput represents input for creating a pricelist
type CreatePricelistInput struct {
	Name         string              `json:"name" binding:"required"`
	Code         string              `json:"code,omitempty"`
	Description  string              `json:"description,omitempty"`
	CurrencyID   string              `json:"currency_id,omitempty"`
	Type         string              `json:"pricelist_type,omitempty"`
	StartDate    string              `json:"start_date,omitempty"`
	EndDate      string              `json:"end_date,omitempty"`
	Priority     int                 `json:"priority,omitempty"`
	ShowDiscount *bool               `json:"show_discount,omitempty"`
	CountryCodes []string            `json:"country_codes,omitempty"`
	IsActive     *bool               `json:"is_active,omitempty"`
	IsDefault    bool                `json:"is_default,omitempty"`
	Items        []PricelistItemInput `json:"items,omitempty"`
}

// UpdatePricelistInput represents input for updating a pricelist
type UpdatePricelistInput struct {
	Name         *string             `json:"name,omitempty"`
	Code         *string             `json:"code,omitempty"`
	Description  *string             `json:"description,omitempty"`
	CurrencyID   *string             `json:"currency_id,omitempty"`
	Type         *string             `json:"pricelist_type,omitempty"`
	StartDate    *string             `json:"start_date,omitempty"`
	EndDate      *string             `json:"end_date,omitempty"`
	Priority     *int                `json:"priority,omitempty"`
	ShowDiscount *bool               `json:"show_discount,omitempty"`
	CountryCodes []string            `json:"country_codes,omitempty"`
	IsActive     *bool               `json:"is_active,omitempty"`
	IsDefault    *bool               `json:"is_default,omitempty"`
	Items        []PricelistItemInput `json:"items,omitempty"`
}

// PricelistItemInput represents input for creating/updating a pricelist item
type PricelistItemInput struct {
	ID              string   `json:"id,omitempty"`
	AppliedOn       string   `json:"applied_on,omitempty"`
	ProductID       string   `json:"product_id,omitempty"`
	CategoryID      string   `json:"category_id,omitempty"`
	VariantID       string   `json:"variant_id,omitempty"`
	MinQuantity     float64  `json:"min_quantity,omitempty"`
	ComputePrice    string   `json:"compute_price,omitempty"`
	FixedPrice      *float64 `json:"fixed_price,omitempty"`
	PercentPrice    float64  `json:"percent_price,omitempty"`
	PriceBase       string   `json:"price_base,omitempty"`
	BasePricelistID string   `json:"base_pricelist_id,omitempty"`
	PriceDiscount   float64  `json:"price_discount,omitempty"`
	PriceSurcharge  float64  `json:"price_surcharge,omitempty"`
	PriceRound      float64  `json:"price_round,omitempty"`
	PriceMinMargin  float64  `json:"price_min_margin,omitempty"`
	PriceMaxMargin  float64  `json:"price_max_margin,omitempty"`
	DateStart       string   `json:"date_start,omitempty"`
	DateEnd         string   `json:"date_end,omitempty"`
	Sequence        int      `json:"sequence,omitempty"`
	IsActive        *bool    `json:"is_active,omitempty"`
}

// PricelistListFilter represents filters for listing pricelists
type PricelistListFilter struct {
	Search   string `form:"search"`
	Type     string `form:"pricelist_type"`
	IsActive *bool  `form:"is_active"`
}

// GetPriceInput represents input for getting a computed price
type GetPriceInput struct {
	ProductID   string  `json:"product_id" binding:"required"`
	VariantID   string  `json:"variant_id,omitempty"`
	PricelistID string  `json:"pricelist_id,omitempty"`
	CustomerID  string  `json:"customer_id,omitempty"`
	Quantity    float64 `json:"quantity,omitempty"`
	Date        string  `json:"date,omitempty"`
}

// GetPriceOutput represents the computed price result
type GetPriceOutput struct {
	ProductID       uuid.UUID  `json:"product_id"`
	VariantID       *uuid.UUID `json:"variant_id,omitempty"`
	PricelistID     *uuid.UUID `json:"pricelist_id,omitempty"`
	PricelistName   string     `json:"pricelist_name,omitempty"`
	OriginalPrice   float64    `json:"original_price"`
	ComputedPrice   float64    `json:"computed_price"`
	DiscountPercent float64    `json:"discount_percent,omitempty"`
	DiscountAmount  float64    `json:"discount_amount,omitempty"`
	RuleApplied     string     `json:"rule_applied,omitempty"`
	MinQuantity     float64    `json:"min_quantity,omitempty"`
}

// GetBulkPriceInput represents input for getting prices for multiple products
type GetBulkPriceInput struct {
	ProductIDs  []string `json:"product_ids" binding:"required"`
	PricelistID string   `json:"pricelist_id,omitempty"`
	CustomerID  string   `json:"customer_id,omitempty"`
	Quantity    float64  `json:"quantity,omitempty"`
	Date        string   `json:"date,omitempty"`
}

// PricelistResponse represents a pricelist with additional computed fields
type PricelistResponse struct {
	Pricelist
	ItemCount    int    `json:"item_count"`
	CurrencyCode string `json:"currency_code,omitempty"`
}

// CustomerPricelistInput represents input for assigning a pricelist to a customer
type CustomerPricelistInput struct {
	CustomerID  string `json:"customer_id" binding:"required"`
	PricelistID string `json:"pricelist_id" binding:"required"`
	Priority    int    `json:"priority,omitempty"`
	StartDate   string `json:"start_date,omitempty"`
	EndDate     string `json:"end_date,omitempty"`
	IsActive    *bool  `json:"is_active,omitempty"`
}

// Nullable types for database scanning
type NullablePricelistItem struct {
	PricelistItem
	ProductID       sql.NullString
	CategoryID      sql.NullString
	VariantID       sql.NullString
	FixedPrice      sql.NullFloat64
	BasePricelistID sql.NullString
	DateStart       sql.NullTime
	DateEnd         sql.NullTime
}
