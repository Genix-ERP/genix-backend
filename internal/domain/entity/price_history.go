package entity

import (
	"time"

	"github.com/google/uuid"
)

// PriceHistory represents a supplier price history record
type PriceHistory struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	TenantID      uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	ProductID     uuid.UUID  `json:"product_id" db:"product_id"`
	VendorID      uuid.UUID  `json:"vendor_id" db:"vendor_id"`
	UnitPrice     float64    `json:"unit_price" db:"unit_price"`
	CurrencyID    *uuid.UUID `json:"currency_id,omitempty" db:"currency_id"`
	EffectiveDate time.Time  `json:"effective_date" db:"effective_date"`
	MinQuantity   float64    `json:"min_quantity" db:"min_quantity"`
	Notes         *string    `json:"notes,omitempty" db:"notes"`
	Source        *string    `json:"source,omitempty" db:"source"`
	SourceID      *uuid.UUID `json:"source_id,omitempty" db:"source_id"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`

	// Computed fields
	ProductName string `json:"product_name,omitempty"`
	VendorName  string `json:"vendor_name,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

// CreatePriceHistoryInput represents input for creating a price history record
type CreatePriceHistoryInput struct {
	ProductID     string  `json:"product_id" binding:"required"`
	VendorID      string  `json:"vendor_id" binding:"required"`
	UnitPrice     float64 `json:"unit_price" binding:"required,gt=0"`
	Currency      string  `json:"currency,omitempty"`
	EffectiveDate string  `json:"effective_date,omitempty"`
	MinQuantity   float64 `json:"min_quantity,omitempty"`
	Notes         string  `json:"notes,omitempty"`
	Source        string  `json:"source,omitempty"`
}

// PriceHistoryListFilter represents filters for listing price history
type PriceHistoryListFilter struct {
	ProductID string `form:"product_id"`
	VendorID  string `form:"vendor_id"`
	DateFrom  string `form:"date_from"`
	DateTo    string `form:"date_to"`
}

// PriceHistoryResponse represents the API response for price history
type PriceHistoryResponse struct {
	ID            uuid.UUID  `json:"id"`
	ProductID     uuid.UUID  `json:"product_id"`
	ProductName   string     `json:"product_name"`
	VendorID      uuid.UUID  `json:"vendor_id"`
	VendorName    string     `json:"vendor_name"`
	UnitPrice     float64    `json:"unit_price"`
	Currency      string     `json:"currency"`
	EffectiveDate time.Time  `json:"effective_date"`
	MinQuantity   float64    `json:"min_quantity"`
	Notes         *string    `json:"notes,omitempty"`
	Source        *string    `json:"source,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// PriceHistoryGroupedResponse represents price history grouped by product and supplier
type PriceHistoryGroupedResponse struct {
	ID           string                `json:"id"`
	ProductID    uuid.UUID             `json:"product_id"`
	ProductName  string                `json:"product_name"`
	SupplierID   uuid.UUID             `json:"supplier_id"`
	SupplierName string                `json:"supplier_name"`
	Prices       []PriceHistoryEntry   `json:"prices"`
}

// PriceHistoryEntry represents a single price entry with date
type PriceHistoryEntry struct {
	Date     string  `json:"date"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}
