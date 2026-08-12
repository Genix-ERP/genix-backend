package entity

import (
	"time"

	"github.com/google/uuid"
)

type Tender struct {
	ID              uuid.UUID    `json:"id" db:"id"`
	TenantID        *uuid.UUID   `json:"tenant_id" db:"tenant_id"`
	BuyerID         uuid.UUID    `json:"buyer_id" db:"buyer_id"`
	Title           string       `json:"title" db:"title"`
	Description     string       `json:"description" db:"description"`
	Status          string       `json:"status" db:"status"`
	TenderType      string       `json:"tender_type" db:"tender_type"`
	RegionID        *uuid.UUID   `json:"region_id" db:"region_id"`
	DeliveryAddress string       `json:"delivery_address" db:"delivery_address"`
	Deadline        time.Time    `json:"deadline" db:"deadline"`
	DeliveryDate    *time.Time   `json:"delivery_date" db:"delivery_date"`
	Currency        string       `json:"currency" db:"currency"`
	Attachment      string       `json:"attachment" db:"attachment"`
	BidCount        int          `json:"bid_count" db:"bid_count"`
	SelectedBidID   *uuid.UUID   `json:"selected_bid_id" db:"selected_bid_id"`
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt       *time.Time   `json:"deleted_at,omitempty" db:"deleted_at"`
	Items           []TenderItem `json:"items,omitempty" db:"-"`
}

type TenderItem struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	TenderID   uuid.UUID  `json:"tender_id" db:"tender_id"`
	CategoryID *uuid.UUID `json:"category_id" db:"category_id"`
	Name       string     `json:"name" db:"name"`
	Quantity   float64    `json:"quantity" db:"quantity"`
	Unit       string     `json:"unit" db:"unit"`
	Specs      string     `json:"specs" db:"specs"`
	SortOrder  int        `json:"sort_order" db:"sort_order"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
}

type CreateTenderInput struct {
	Title           string                  `json:"title" binding:"required"`
	Description     string                  `json:"description"`
	TenderType      string                  `json:"tender_type" binding:"required,oneof=open closed"`
	RegionID        *uuid.UUID              `json:"region_id"`
	DeliveryAddress string                  `json:"delivery_address"`
	Deadline        string                  `json:"deadline" binding:"required"`
	DeliveryDate    string                  `json:"delivery_date"`
	Currency        string                  `json:"currency" binding:"required,oneof=UZS USD"`
	Status          string                  `json:"status"`
	Items           []CreateTenderItemInput `json:"items" binding:"required,min=1,dive"`
}

type CreateTenderItemInput struct {
	CategoryID *uuid.UUID `json:"category_id"`
	Name       string     `json:"name" binding:"required"`
	Quantity   float64    `json:"quantity" binding:"required,gt=0"`
	Unit       string     `json:"unit" binding:"required"`
	Specs      string     `json:"specs"`
}

type UpdateTenderInput struct {
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	RegionID        *uuid.UUID `json:"region_id"`
	DeliveryAddress string     `json:"delivery_address"`
	Deadline        string     `json:"deadline"`
	DeliveryDate    string     `json:"delivery_date"`
	Currency        string     `json:"currency"`
	Status          string     `json:"status"`
}

type TenderResponse struct {
	ID              uuid.UUID            `json:"id"`
	BuyerID         uuid.UUID            `json:"buyer_id"`
	BuyerName       string               `json:"buyer_name"`
	Title           string               `json:"title"`
	Description     string               `json:"description"`
	Status          string               `json:"status"`
	TenderType      string               `json:"tender_type"`
	RegionID        *uuid.UUID           `json:"region_id"`
	RegionName      string               `json:"region_name,omitempty"`
	DeliveryAddress string               `json:"delivery_address"`
	Deadline        time.Time            `json:"deadline"`
	DeliveryDate    *time.Time           `json:"delivery_date"`
	Currency        string               `json:"currency"`
	Attachment      string               `json:"attachment"`
	BidCount        int                  `json:"bid_count"`
	SelectedBidID   *uuid.UUID           `json:"selected_bid_id"`
	Items           []TenderItemResponse `json:"items,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
}

type TenderItemResponse struct {
	ID           uuid.UUID  `json:"id"`
	CategoryID   *uuid.UUID `json:"category_id"`
	CategoryName string     `json:"category_name,omitempty"`
	Name         string     `json:"name"`
	Quantity     float64    `json:"quantity"`
	Unit         string     `json:"unit"`
	Specs        string     `json:"specs"`
}

type TenderListParams struct {
	Page       int        `form:"page"`
	PageSize   int        `form:"page_size"`
	Status     string     `form:"status"`
	RegionID   *uuid.UUID `form:"region_id"`
	CategoryID *uuid.UUID `form:"category_id"`
	Search     string     `form:"search"`
	Ordering   string     `form:"ordering"`
}
