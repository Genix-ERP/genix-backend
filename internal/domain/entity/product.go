package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ProductType represents the type of product
type ProductType string

const (
	ProductTypeProduct ProductType = "product"
	ProductTypeService ProductType = "service"
	ProductTypeBundle  ProductType = "bundle"
)

// ProductCategory represents a product category
type ProductCategory struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	TenantID    uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	ParentID    *uuid.UUID      `json:"parent_id,omitempty" db:"parent_id"`
	Code        string          `json:"code" db:"code"`
	Name        string          `json:"name" db:"name"`
	Description *string         `json:"description,omitempty" db:"description"`
	Attributes  json.RawMessage `json:"attributes" db:"attributes" swaggertype:"object"`
	IsActive    bool            `json:"is_active" db:"is_active"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`

	// Category GL accounts (Odoo-style)
	IncomeAccountID         *uuid.UUID `json:"income_account_id,omitempty" db:"income_account_id"`
	ExpenseAccountID        *uuid.UUID `json:"expense_account_id,omitempty" db:"expense_account_id"`
	StockValuationAccountID *uuid.UUID `json:"stock_valuation_account_id,omitempty" db:"stock_valuation_account_id"`
	StockInputAccountID     *uuid.UUID `json:"stock_input_account_id,omitempty" db:"stock_input_account_id"`
	StockOutputAccountID    *uuid.UUID `json:"stock_output_account_id,omitempty" db:"stock_output_account_id"`

	// Tenant-wide product count (not org-scoped). Matches what the
	// delete-category guard actually checks, so the UI count is
	// consistent with whether delete will be refused.
	ProductCount int `json:"product_count"`

	// Relationships
	Parent   *ProductCategory  `json:"parent,omitempty"`
	Children []ProductCategory `json:"children,omitempty"`
}

// UnitOfMeasure represents a unit of measure
type UnitOfMeasure struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	TenantID         uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code             string     `json:"code" db:"code"`
	Name             string     `json:"name" db:"name"`
	Category         *string    `json:"category,omitempty" db:"category"`
	BaseUnitID       *uuid.UUID `json:"base_unit_id,omitempty" db:"base_unit_id"`
	ConversionFactor float64    `json:"conversion_factor" db:"conversion_factor"`
	IsActive         bool       `json:"is_active" db:"is_active"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
}

// Product represents a product or service
type Product struct {
	ID                 uuid.UUID       `json:"id" db:"id"`
	TenantID           uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	CategoryID         *uuid.UUID      `json:"category_id,omitempty" db:"category_id"`
	Type               ProductType     `json:"type" db:"type"`
	InventoryType      string          `json:"inventory_type" db:"inventory_type"`
	Code               string          `json:"code" db:"code"`
	SKU                *string         `json:"sku,omitempty" db:"sku"`
	Barcode            *string         `json:"barcode,omitempty" db:"barcode"`
	SearchKey          *string         `json:"search_key,omitempty" db:"search_key"`
	Name               string          `json:"name" db:"name"`
	Description        *string         `json:"description,omitempty" db:"description"`
	ShortDescription   *string         `json:"short_description,omitempty" db:"short_description"`
	UnitID             *uuid.UUID      `json:"unit_id,omitempty" db:"unit_id"`
	PurchaseUnitID     *uuid.UUID      `json:"purchase_unit_id,omitempty" db:"purchase_unit_id"`
	SalesUnitID        *uuid.UUID      `json:"sales_unit_id,omitempty" db:"sales_unit_id"`
	CostPrice          float64         `json:"cost_price" db:"cost_price"`
	ListPrice          float64         `json:"list_price" db:"list_price"`
	MinPrice           float64         `json:"min_price" db:"min_price"`
	CurrencyID         *uuid.UUID      `json:"currency_id,omitempty" db:"currency_id"`
	IsStockable        bool            `json:"is_stockable" db:"is_stockable"`
	TrackInventory     bool            `json:"track_inventory" db:"track_inventory"`
	MinStockLevel      float64         `json:"min_stock_level" db:"min_stock_level"`
	MaxStockLevel      *float64        `json:"max_stock_level,omitempty" db:"max_stock_level"`
	ReorderPoint       float64         `json:"reorder_point" db:"reorder_point"`
	ReorderQuantity    float64         `json:"reorder_quantity" db:"reorder_quantity"`
	LeadTimeDays       int             `json:"lead_time_days" db:"lead_time_days"`
	SalesAccountID     *uuid.UUID      `json:"sales_account_id,omitempty" db:"sales_account_id"`
	PurchaseAccountID  *uuid.UUID      `json:"purchase_account_id,omitempty" db:"purchase_account_id"`
	InventoryAccountID *uuid.UUID      `json:"inventory_account_id,omitempty" db:"inventory_account_id"`
	COGSAccountID      *uuid.UUID      `json:"cogs_account_id,omitempty" db:"cogs_account_id"`
	SalesTaxID         *uuid.UUID      `json:"sales_tax_id,omitempty" db:"sales_tax_id"`
	PurchaseTaxID      *uuid.UUID      `json:"purchase_tax_id,omitempty" db:"purchase_tax_id"`
	Weight             *float64        `json:"weight,omitempty" db:"weight"`
	WeightUnit         *string         `json:"weight_unit,omitempty" db:"weight_unit"`
	Length             *float64        `json:"length,omitempty" db:"length"`
	Width              *float64        `json:"width,omitempty" db:"width"`
	Height             *float64        `json:"height,omitempty" db:"height"`
	DimensionUnit      *string         `json:"dimension_unit,omitempty" db:"dimension_unit"`
	Images             json.RawMessage `json:"images" db:"images" swaggertype:"object"`
	Attributes         json.RawMessage `json:"attributes" db:"attributes" swaggertype:"object"`
	Tags               json.RawMessage `json:"tags" db:"tags" swaggertype:"object"`
	Notes              *string         `json:"notes,omitempty" db:"notes"`
	IsPurchasable      bool            `json:"is_purchasable" db:"is_purchasable"`
	IsSellable         bool            `json:"is_sellable" db:"is_sellable"`
	// Module visibility fields (Odoo-style)
	CanBeSold          bool         `json:"can_be_sold" db:"can_be_sold"`
	CanBePurchased     bool         `json:"can_be_purchased" db:"can_be_purchased"`
	AvailableInPOS     bool         `json:"available_in_pos" db:"available_in_pos"`
	CanBeExpensed      bool         `json:"can_be_expensed" db:"can_be_expensed"`
	CanBeRented        bool         `json:"can_be_rented" db:"can_be_rented"`
	CanBeSubcontracted bool         `json:"can_be_subcontracted" db:"can_be_subcontracted"`
	IsOverheadExpense  bool         `json:"is_overhead_expense" db:"is_overhead_expense"`
	IsManufacturable   bool         `json:"is_manufacturable" db:"is_manufacturable"`
	AutoManufacture    bool         `json:"auto_manufacture" db:"auto_manufacture"`
	HasVariants        bool         `json:"has_variants" db:"has_variants"`
	HasDelivery        bool         `json:"has_delivery" db:"has_delivery"`
	DeliveryPrice      float64      `json:"delivery_price" db:"delivery_price"`
	IsActive           bool         `json:"is_active" db:"is_active"`
	CreatedBy          *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt          time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt          sql.NullTime `json:"-" db:"deleted_at"`

	// Relationships
	Category     *ProductCategory `json:"category,omitempty"`
	Unit         *UnitOfMeasure   `json:"unit,omitempty"`
	PurchaseUnit *UnitOfMeasure   `json:"purchase_unit,omitempty"`
	SalesUnit    *UnitOfMeasure   `json:"sales_unit,omitempty"`
}

// CreateProductInput represents input for creating a product
type CreateProductInput struct {
	CategoryID       string      `json:"category_id,omitempty"`
	Type             ProductType `json:"type" binding:"required,oneof=product service bundle"`
	InventoryType    string      `json:"inventory_type,omitempty"`
	Code             string      `json:"code" binding:"required,min=1,max=50"`
	SKU              string      `json:"sku,omitempty"`
	Barcode          string      `json:"barcode,omitempty"`
	SearchKey        string      `json:"search_key,omitempty"`
	Name             string      `json:"name" binding:"required,min=1,max=255"`
	Description      string      `json:"description,omitempty"`
	ShortDescription string      `json:"short_description,omitempty"`
	UnitID           string      `json:"unit_id,omitempty"`
	InventoryUOM     string      `json:"inventory_uom,omitempty"` // string UOM code from frontend (e.g. "kg", "unit")
	SalesUOM         string      `json:"sales_uom,omitempty"`     // string UOM code for sales
	PurchaseUOM      string      `json:"purchase_uom,omitempty"`  // string UOM code for purchase
	CostPrice        float64     `json:"cost_price"`
	ListPrice        float64     `json:"list_price"`
	MinPrice         float64     `json:"min_price"`
	CurrencyID       string      `json:"currency_id,omitempty"`
	IsStockable      *bool       `json:"is_stockable,omitempty"`
	TrackInventory   *bool       `json:"track_inventory,omitempty"`
	MinStockLevel    float64     `json:"min_stock_level"`
	ReorderPoint     float64     `json:"reorder_point"`
	ReorderQuantity  float64     `json:"reorder_quantity"`
	IsPurchasable    *bool       `json:"is_purchasable,omitempty"`
	IsSellable       *bool       `json:"is_sellable,omitempty"`
	// Module visibility fields
	CanBeSold          *bool    `json:"can_be_sold,omitempty"`
	CanBePurchased     *bool    `json:"can_be_purchased,omitempty"`
	AvailableInPOS     *bool    `json:"available_in_pos,omitempty"`
	CanBeExpensed      *bool    `json:"can_be_expensed,omitempty"`
	CanBeRented        *bool    `json:"can_be_rented,omitempty"`
	CanBeSubcontracted *bool    `json:"can_be_subcontracted,omitempty"`
	IsOverheadExpense  *bool    `json:"is_overhead_expense,omitempty"`
	IsManufacturable   *bool    `json:"is_manufacturable,omitempty"`
	AutoManufacture    *bool    `json:"auto_manufacture,omitempty"`
	HasDelivery        *bool    `json:"has_delivery,omitempty"`
	DeliveryPrice      float64  `json:"delivery_price"`
	OrganizationIDs    []string `json:"organization_ids,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	ImageURL           string   `json:"image_url,omitempty"`
}

// UpdateProductInput represents input for updating a product
type UpdateProductInput struct {
	CategoryID       *string  `json:"category_id,omitempty"`
	SKU              *string  `json:"sku,omitempty"`
	Barcode          *string  `json:"barcode,omitempty"`
	SearchKey        *string  `json:"search_key,omitempty"`
	Name             *string  `json:"name,omitempty"`
	Description      *string  `json:"description,omitempty"`
	ShortDescription *string  `json:"short_description,omitempty"`
	UnitID           *string  `json:"unit_id,omitempty"`
	InventoryUOM     *string  `json:"inventory_uom,omitempty"`
	SalesUOM         *string  `json:"sales_uom,omitempty"`
	PurchaseUOM      *string  `json:"purchase_uom,omitempty"`
	CostPrice        *float64 `json:"cost_price,omitempty"`
	ListPrice        *float64 `json:"list_price,omitempty"`
	MinPrice         *float64 `json:"min_price,omitempty"`
	IsStockable      *bool    `json:"is_stockable,omitempty"`
	TrackInventory   *bool    `json:"track_inventory,omitempty"`
	MinStockLevel    *float64 `json:"min_stock_level,omitempty"`
	ReorderPoint     *float64 `json:"reorder_point,omitempty"`
	ReorderQuantity  *float64 `json:"reorder_quantity,omitempty"`
	IsPurchasable    *bool    `json:"is_purchasable,omitempty"`
	IsSellable       *bool    `json:"is_sellable,omitempty"`
	// Module visibility fields
	CanBeSold          *bool    `json:"can_be_sold,omitempty"`
	CanBePurchased     *bool    `json:"can_be_purchased,omitempty"`
	AvailableInPOS     *bool    `json:"available_in_pos,omitempty"`
	CanBeExpensed      *bool    `json:"can_be_expensed,omitempty"`
	CanBeRented        *bool    `json:"can_be_rented,omitempty"`
	CanBeSubcontracted *bool    `json:"can_be_subcontracted,omitempty"`
	IsOverheadExpense  *bool    `json:"is_overhead_expense,omitempty"`
	IsManufacturable   *bool    `json:"is_manufacturable,omitempty"`
	AutoManufacture    *bool    `json:"auto_manufacture,omitempty"`
	HasDelivery        *bool    `json:"has_delivery,omitempty"`
	DeliveryPrice      *float64 `json:"delivery_price,omitempty"`
	InventoryType      *string  `json:"inventory_type,omitempty"`
	OrganizationIDs    []string `json:"organization_ids,omitempty"`
	IsActive           *bool    `json:"is_active,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	ImageURL           *string  `json:"image_url,omitempty"`
}

// ProductListFilter represents filters for listing products
type ProductListFilter struct {
	Search     string      `form:"search"`
	CategoryID string      `form:"category_id"`
	Type       ProductType `form:"type"`
	IsActive   *bool       `form:"is_active"`
	MinPrice   *float64    `form:"min_price"`
	MaxPrice   *float64    `form:"max_price"`
	InStock    *bool       `form:"in_stock"`
}

// ProductResponse represents the API response for a product
type ProductResponse struct {
	ID               uuid.UUID        `json:"id"`
	CategoryID       *uuid.UUID       `json:"category_id,omitempty"`
	Category         *ProductCategory `json:"category,omitempty"`
	Type             ProductType      `json:"type"`
	InventoryType    string           `json:"inventory_type"`
	Code             string           `json:"code"`
	SKU              *string          `json:"sku,omitempty"`
	Barcode          *string          `json:"barcode,omitempty"`
	SearchKey        *string          `json:"search_key,omitempty"`
	Name             string           `json:"name"`
	Description      *string          `json:"description,omitempty"`
	ShortDescription *string          `json:"short_description,omitempty"`
	UnitID           *uuid.UUID       `json:"unit_id,omitempty"`
	UnitName         string           `json:"unit_name,omitempty"`
	UnitCode         string           `json:"unit_code,omitempty"`
	PurchaseUnitID   *uuid.UUID       `json:"purchase_unit_id,omitempty"`
	PurchaseUnitName string           `json:"purchase_unit_name,omitempty"`
	SalesUnitID      *uuid.UUID       `json:"sales_unit_id,omitempty"`
	SalesUnitName    string           `json:"sales_unit_name,omitempty"`
	CostPrice        float64          `json:"cost_price"`
	ListPrice        float64          `json:"list_price"`
	IsStockable      bool             `json:"is_stockable"`
	TrackInventory   bool             `json:"track_inventory"`
	MinStockLevel    float64          `json:"min_stock_level"`
	IsPurchasable    bool             `json:"is_purchasable"`
	IsSellable       bool             `json:"is_sellable"`
	// Module visibility fields
	CanBeSold          bool        `json:"can_be_sold"`
	CanBePurchased     bool        `json:"can_be_purchased"`
	AvailableInPOS     bool        `json:"available_in_pos"`
	CanBeExpensed      bool        `json:"can_be_expensed"`
	CanBeRented        bool        `json:"can_be_rented"`
	CanBeSubcontracted bool        `json:"can_be_subcontracted"`
	IsOverheadExpense  bool        `json:"is_overhead_expense"`
	IsManufacturable   bool        `json:"is_manufacturable"`
	AutoManufacture    bool        `json:"auto_manufacture"`
	HasVariants        bool        `json:"has_variants"`
	HasDelivery        bool        `json:"has_delivery"`
	DeliveryPrice      float64     `json:"delivery_price"`
	Weight             *float64    `json:"weight,omitempty"`
	Length             *float64    `json:"length,omitempty"`
	Width              *float64    `json:"width,omitempty"`
	Height             *float64    `json:"height,omitempty"`
	IsActive           bool        `json:"is_active"`
	Tags               []string    `json:"tags"`
	ImageURL           string      `json:"image_url"`
	OrganizationIDs    []uuid.UUID `json:"organization_ids"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
}
