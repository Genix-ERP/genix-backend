package entity

import (
	"time"

	"github.com/google/uuid"
)

type TenderBid struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	TenderID        uuid.UUID  `json:"tender_id" db:"tender_id"`
	SupplierID      uuid.UUID  `json:"supplier_id" db:"supplier_id"`
	TotalPrice      float64    `json:"total_price" db:"total_price"`
	Currency        string     `json:"currency" db:"currency"`
	DeliveryDays    int        `json:"delivery_days" db:"delivery_days"`
	Status          string     `json:"status" db:"status"`
	Note            string     `json:"note" db:"note"`
	Attachment      string     `json:"attachment" db:"attachment"`
	RejectionReason string     `json:"rejection_reason" db:"rejection_reason"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
	Items           []TenderBidItem `json:"items,omitempty" db:"-"`
}

type TenderBidItem struct {
	ID           uuid.UUID `json:"id" db:"id"`
	BidID        uuid.UUID `json:"bid_id" db:"bid_id"`
	TenderItemID uuid.UUID `json:"tender_item_id" db:"tender_item_id"`
	UnitPrice    float64   `json:"unit_price" db:"unit_price"`
	TotalPrice   float64   `json:"total_price" db:"total_price"`
	Note         string    `json:"note" db:"note"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

type CreateBidInput struct {
	TotalPrice   float64              `json:"total_price" binding:"required,gt=0"`
	DeliveryDays int                  `json:"delivery_days" binding:"required,gt=0"`
	Note         string               `json:"note"`
	Items        []CreateBidItemInput `json:"items" binding:"required,min=1"`
}

type CreateBidItemInput struct {
	TenderItemID uuid.UUID `json:"tender_item_id" binding:"required"`
	UnitPrice    float64   `json:"unit_price" binding:"required,gt=0"`
	Note         string    `json:"note"`
}

type UpdateBidInput struct {
	TotalPrice   float64              `json:"total_price"`
	DeliveryDays int                  `json:"delivery_days"`
	Note         string               `json:"note"`
	Items        []CreateBidItemInput `json:"items"`
}

type AcceptBidInput struct {
	Reason string `json:"reason"`
}

type BidResponse struct {
	ID              uuid.UUID          `json:"id"`
	TenderID        uuid.UUID          `json:"tender_id"`
	SupplierID      uuid.UUID          `json:"supplier_id"`
	SupplierName    string             `json:"supplier_name"`
	SupplierPhone   string             `json:"supplier_phone"`
	SupplierEmail   string             `json:"supplier_email"`
	SupplierRating  float64            `json:"supplier_rating"`
	TotalPrice      float64            `json:"total_price"`
	Currency        string             `json:"currency"`
	DeliveryDays    int                `json:"delivery_days"`
	Status          string             `json:"status"`
	Note            string             `json:"note"`
	Attachment      string             `json:"attachment"`
	RejectionReason string             `json:"rejection_reason,omitempty"`
	Items           []BidItemResponse  `json:"items,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
}

type BidItemResponse struct {
	ID             uuid.UUID `json:"id"`
	TenderItemID   uuid.UUID `json:"tender_item_id"`
	TenderItemName string    `json:"tender_item_name"`
	UnitPrice      float64   `json:"unit_price"`
	TotalPrice     float64   `json:"total_price"`
	Note           string    `json:"note"`
}
