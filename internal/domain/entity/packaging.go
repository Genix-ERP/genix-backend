package entity

import (
	"time"

	"github.com/google/uuid"
)

// =====================================================
// PRODUCT PACKAGING (Odoo-style product packagings)
// =====================================================

// ProductPackaging represents how a product can be packaged (6-pack, case, etc.)
type ProductPackaging struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	TenantID  uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProductID uuid.UUID  `json:"product_id" db:"product_id"`
	Name      string     `json:"name" db:"name"`
	Qty       float64    `json:"qty" db:"qty"`
	Barcode   *string    `json:"barcode,omitempty" db:"barcode"`
	Sales     bool       `json:"sales" db:"sales"`
	Purchase  bool       `json:"purchase" db:"purchase"`
	IsActive  bool       `json:"is_active" db:"is_active"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"-" db:"deleted_at"`

	// Relationships
	Product *Product `json:"product,omitempty"`
}

func (ProductPackaging) TableName() string {
	return "product_packagings"
}

// CreateProductPackagingInput represents input for creating a product packaging
type CreateProductPackagingInput struct {
	ProductID string  `json:"product_id" binding:"required"`
	Name      string  `json:"name" binding:"required,min=1,max=100"`
	Qty       float64 `json:"qty" binding:"required,gt=0"`
	Barcode   string  `json:"barcode,omitempty"`
	Sales     *bool   `json:"sales,omitempty"`
	Purchase  *bool   `json:"purchase,omitempty"`
}

// UpdateProductPackagingInput represents input for updating a product packaging
type UpdateProductPackagingInput struct {
	Name     *string  `json:"name,omitempty"`
	Qty      *float64 `json:"qty,omitempty"`
	Barcode  *string  `json:"barcode,omitempty"`
	Sales    *bool    `json:"sales,omitempty"`
	Purchase *bool    `json:"purchase,omitempty"`
	IsActive *bool    `json:"is_active,omitempty"`
}

// ProductPackagingListFilter represents filters for listing product packagings
type ProductPackagingListFilter struct {
	ProductID string `form:"product_id"`
	Sales     *bool  `form:"sales"`
	Purchase  *bool  `form:"purchase"`
	IsActive  *bool  `form:"is_active"`
	Search    string `form:"search"`
}

// ProductPackagingResponse represents the API response for a product packaging
type ProductPackagingResponse struct {
	ID          uuid.UUID `json:"id"`
	ProductID   uuid.UUID `json:"product_id"`
	ProductCode string    `json:"product_code,omitempty"`
	ProductName string    `json:"product_name,omitempty"`
	Name        string    `json:"name"`
	Qty         float64   `json:"qty"`
	Barcode     *string   `json:"barcode,omitempty"`
	Sales       bool      `json:"sales"`
	Purchase    bool      `json:"purchase"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}

// =====================================================
// PACKAGE TYPES (box sizes, pallets, containers)
// =====================================================

// PackageUse represents how a package is used
type PackageUse string

const (
	PackageUseReusable   PackageUse = "reusable"
	PackageUseDisposable PackageUse = "disposable"
)

// PackageType represents a type of physical package (box size)
type PackageType struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	TenantID   uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Name       string     `json:"name" db:"name"`
	Code       *string    `json:"code,omitempty" db:"code"`
	LengthMM   *int       `json:"length_mm,omitempty" db:"length_mm"`
	WidthMM    *int       `json:"width_mm,omitempty" db:"width_mm"`
	HeightMM   *int       `json:"height_mm,omitempty" db:"height_mm"`
	MaxWeight  *float64   `json:"max_weight,omitempty" db:"max_weight"`
	Barcode    *string    `json:"barcode,omitempty" db:"barcode"`
	PackageUse PackageUse `json:"package_use" db:"package_use"`
	CarrierID  *uuid.UUID `json:"carrier_id,omitempty" db:"carrier_id"`
	IsActive   bool       `json:"is_active" db:"is_active"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt  *time.Time `json:"-" db:"deleted_at"`
}

func (PackageType) TableName() string {
	return "package_types"
}

// CreatePackageTypeInput represents input for creating a package type
type CreatePackageTypeInput struct {
	Name       string  `json:"name" binding:"required,min=1,max=100"`
	Code       string  `json:"code,omitempty"`
	LengthMM   int     `json:"length_mm,omitempty"`
	WidthMM    int     `json:"width_mm,omitempty"`
	HeightMM   int     `json:"height_mm,omitempty"`
	MaxWeight  float64 `json:"max_weight,omitempty"`
	Barcode    string  `json:"barcode,omitempty"`
	PackageUse string  `json:"package_use,omitempty"` // reusable, disposable
	CarrierID  string  `json:"carrier_id,omitempty"`
}

// UpdatePackageTypeInput represents input for updating a package type
type UpdatePackageTypeInput struct {
	Name       *string  `json:"name,omitempty"`
	Code       *string  `json:"code,omitempty"`
	LengthMM   *int     `json:"length_mm,omitempty"`
	WidthMM    *int     `json:"width_mm,omitempty"`
	HeightMM   *int     `json:"height_mm,omitempty"`
	MaxWeight  *float64 `json:"max_weight,omitempty"`
	Barcode    *string  `json:"barcode,omitempty"`
	PackageUse *string  `json:"package_use,omitempty"`
	CarrierID  *string  `json:"carrier_id,omitempty"`
	IsActive   *bool    `json:"is_active,omitempty"`
}

// PackageTypeListFilter represents filters for listing package types
type PackageTypeListFilter struct {
	Search     string `form:"search"`
	PackageUse string `form:"package_use"`
	IsActive   *bool  `form:"is_active"`
}

// PackageTypeResponse represents the API response for a package type
type PackageTypeResponse struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Code        *string    `json:"code,omitempty"`
	LengthMM    *int       `json:"length_mm,omitempty"`
	WidthMM     *int       `json:"width_mm,omitempty"`
	HeightMM    *int       `json:"height_mm,omitempty"`
	MaxWeight   *float64   `json:"max_weight,omitempty"`
	Barcode     *string    `json:"barcode,omitempty"`
	PackageUse  string     `json:"package_use"`
	CarrierID   *uuid.UUID `json:"carrier_id,omitempty"`
	CarrierName string     `json:"carrier_name,omitempty"`
	Dimensions  string     `json:"dimensions,omitempty"` // Formatted: "100x50x30 mm"
	IsActive    bool       `json:"is_active"`
	CreatedAt   time.Time  `json:"created_at"`
}

// =====================================================
// PACKAGES (actual physical packages in warehouse)
// =====================================================

// Package represents a physical package in the warehouse
type Package struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Name           string     `json:"name" db:"name"`
	PackageTypeID  *uuid.UUID `json:"package_type_id,omitempty" db:"package_type_id"`
	ShippingWeight *float64   `json:"shipping_weight,omitempty" db:"shipping_weight"`
	LocationID     *uuid.UUID `json:"location_id,omitempty" db:"location_id"`
	PickingID      *uuid.UUID `json:"picking_id,omitempty" db:"picking_id"`
	PackDate       time.Time  `json:"pack_date" db:"pack_date"`
	IsActive       bool       `json:"is_active" db:"is_active"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	PackageType *PackageType     `json:"package_type,omitempty"`
	Contents    []PackageContent `json:"contents,omitempty"`
}

func (Package) TableName() string {
	return "packages"
}

// PackageContent represents products inside a package
type PackageContent struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	PackageID uuid.UUID  `json:"package_id" db:"package_id"`
	ProductID uuid.UUID  `json:"product_id" db:"product_id"`
	Quantity  float64    `json:"quantity" db:"quantity"`
	LotID     *uuid.UUID `json:"lot_id,omitempty" db:"lot_id"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`

	// Relationships
	Product *Product `json:"product,omitempty"`
}

func (PackageContent) TableName() string {
	return "package_contents"
}

// CreatePackageInput represents input for creating a package
type CreatePackageInput struct {
	PackageTypeID  string   `json:"package_type_id,omitempty"`
	LocationID     string   `json:"location_id,omitempty"`
	ShippingWeight *float64 `json:"shipping_weight,omitempty"`
}

// UpdatePackageInput represents input for updating a package
type UpdatePackageInput struct {
	PackageTypeID  *string  `json:"package_type_id,omitempty"`
	ShippingWeight *float64 `json:"shipping_weight,omitempty"`
	LocationID     *string  `json:"location_id,omitempty"`
	IsActive       *bool    `json:"is_active,omitempty"`
}

// AddPackageContentInput represents input for adding content to a package
type AddPackageContentInput struct {
	ProductID string  `json:"product_id" binding:"required"`
	Quantity  float64 `json:"quantity" binding:"required,gt=0"`
	LotID     string  `json:"lot_id,omitempty"`
}

// PackageListFilter represents filters for listing packages
type PackageListFilter struct {
	Search        string `form:"search"`
	PackageTypeID string `form:"package_type_id"`
	LocationID    string `form:"location_id"`
	IsActive      *bool  `form:"is_active"`
}

// PackageResponse represents the API response for a package
type PackageResponse struct {
	ID              uuid.UUID                `json:"id"`
	Name            string                   `json:"name"`
	PackageTypeID   *uuid.UUID               `json:"package_type_id,omitempty"`
	PackageTypeName string                   `json:"package_type_name,omitempty"`
	ShippingWeight  *float64                 `json:"shipping_weight,omitempty"`
	LocationID      *uuid.UUID               `json:"location_id,omitempty"`
	LocationName    string                   `json:"location_name,omitempty"`
	PackDate        time.Time                `json:"pack_date"`
	IsActive        bool                     `json:"is_active"`
	ItemCount       int                      `json:"item_count"`
	Contents        []PackageContentResponse `json:"contents,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
}

// PackageContentResponse represents the API response for package content
type PackageContentResponse struct {
	ID          uuid.UUID  `json:"id"`
	ProductID   uuid.UUID  `json:"product_id"`
	ProductCode string     `json:"product_code,omitempty"`
	ProductName string     `json:"product_name,omitempty"`
	Quantity    float64    `json:"quantity"`
	LotID       *uuid.UUID `json:"lot_id,omitempty"`
	LotNumber   string     `json:"lot_number,omitempty"`
}
