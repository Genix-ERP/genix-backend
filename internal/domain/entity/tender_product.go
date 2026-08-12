package entity

import (
	"time"

	"github.com/google/uuid"
)

type TenderProduct struct {
	ID              uuid.UUID              `json:"id" db:"id"`
	TenantID        *uuid.UUID             `json:"tenant_id" db:"tenant_id"`
	SupplierID      uuid.UUID              `json:"supplier_id" db:"supplier_id"`
	CategoryID      *uuid.UUID             `json:"category_id" db:"category_id"`
	Name            string                 `json:"name" db:"name"`
	NameRu          string                 `json:"name_ru" db:"name_ru"`
	Description     string                 `json:"description" db:"description"`
	Unit            string                 `json:"unit" db:"unit"`
	Price           float64                `json:"price" db:"price"`
	WholesalePrice  float64                `json:"wholesale_price" db:"wholesale_price"`
	WholesaleMinQty float64                `json:"wholesale_min_qty" db:"wholesale_min_qty"`
	Currency        string                 `json:"currency" db:"currency"`
	Availability    string                 `json:"availability" db:"availability"`
	DeliveryDays    int                    `json:"delivery_days" db:"delivery_days"`
	DeliveryRegions []uuid.UUID            `json:"delivery_regions" db:"delivery_regions"`
	Images          []string               `json:"images" db:"images"`
	Certificates    []string               `json:"certificates" db:"certificates"`
	Specs           map[string]interface{} `json:"specs" db:"specs"`
	IsActive        bool                   `json:"is_active" db:"is_active"`
	ViewCount       int                    `json:"view_count" db:"view_count"`
	CreatedAt       time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at" db:"updated_at"`
	DeletedAt       *time.Time             `json:"deleted_at,omitempty" db:"deleted_at"`
}

type TenderCreateProductInput struct {
	CategoryID      *uuid.UUID             `json:"category_id"`
	Name            string                 `json:"name" binding:"required"`
	NameRu          string                 `json:"name_ru"`
	Description     string                 `json:"description"`
	Unit            string                 `json:"unit" binding:"required"`
	Price           float64                `json:"price" binding:"required,gt=0"`
	WholesalePrice  float64                `json:"wholesale_price"`
	WholesaleMinQty float64                `json:"wholesale_min_qty"`
	Currency        string                 `json:"currency" binding:"required,oneof=UZS USD"`
	Availability    string                 `json:"availability" binding:"required,oneof=available on_order unavailable"`
	DeliveryDays    int                    `json:"delivery_days"`
	DeliveryRegions []uuid.UUID            `json:"delivery_regions"`
	Images          []string               `json:"images"`
	Certificates    []string               `json:"certificates"`
	Specs           map[string]interface{} `json:"specs"`
}

type TenderUpdateProductInput struct {
	CategoryID      *uuid.UUID             `json:"category_id"`
	Name            string                 `json:"name"`
	NameRu          string                 `json:"name_ru"`
	Description     string                 `json:"description"`
	Unit            string                 `json:"unit"`
	Price           float64                `json:"price"`
	WholesalePrice  float64                `json:"wholesale_price"`
	WholesaleMinQty float64                `json:"wholesale_min_qty"`
	Currency        string                 `json:"currency"`
	Availability    string                 `json:"availability"`
	DeliveryDays    int                    `json:"delivery_days"`
	DeliveryRegions []uuid.UUID            `json:"delivery_regions"`
	Images          []string               `json:"images"`
	Certificates    []string               `json:"certificates"`
	Specs           map[string]interface{} `json:"specs"`
	IsActive        bool                   `json:"is_active"`
}

type TenderProductResponse struct {
	ID              uuid.UUID              `json:"id"`
	SupplierID      uuid.UUID              `json:"supplier_id"`
	SupplierName    string                 `json:"supplier_name"`
	SupplierRating  float64                `json:"supplier_rating"`
	CategoryID      *uuid.UUID             `json:"category_id"`
	CategoryName    string                 `json:"category_name,omitempty"`
	Name            string                 `json:"name"`
	NameRu          string                 `json:"name_ru"`
	Description     string                 `json:"description"`
	Unit            string                 `json:"unit"`
	Price           float64                `json:"price"`
	WholesalePrice  float64                `json:"wholesale_price"`
	WholesaleMinQty float64                `json:"wholesale_min_qty"`
	Currency        string                 `json:"currency"`
	Availability    string                 `json:"availability"`
	DeliveryDays    int                    `json:"delivery_days"`
	Images          []string               `json:"images"`
	Certificates    []string               `json:"certificates"`
	Specs           map[string]interface{} `json:"specs"`
	IsActive        bool                   `json:"is_active"`
	ViewCount       int                    `json:"view_count"`
	CreatedAt       time.Time              `json:"created_at"`
}

type ProductListParams struct {
	Page       int        `form:"page"`
	PageSize   int        `form:"page_size"`
	CategoryID *uuid.UUID `form:"category_id"`
	RegionID   *uuid.UUID `form:"region_id"`
	MinPrice   float64    `form:"min_price"`
	MaxPrice   float64    `form:"max_price"`
	Search     string     `form:"search"`
	Ordering   string     `form:"ordering"`
}
