package entity

import (
	"time"

	"github.com/google/uuid"
)

type TenderNotification struct {
	ID        uuid.UUID              `json:"id" db:"id"`
	UserID    uuid.UUID              `json:"user_id" db:"user_id"`
	Type      string                 `json:"type" db:"type"`
	Title     string                 `json:"title" db:"title"`
	Message   string                 `json:"message" db:"message"`
	Data      map[string]interface{} `json:"data" db:"data"`
	IsRead    bool                   `json:"is_read" db:"is_read"`
	ReadAt    *time.Time             `json:"read_at" db:"read_at"`
	CreatedAt time.Time              `json:"created_at" db:"created_at"`
}

type NotificationResponse struct {
	ID        uuid.UUID              `json:"id"`
	Type      string                 `json:"type"`
	Title     string                 `json:"title"`
	Message   string                 `json:"message"`
	Data      map[string]interface{} `json:"data"`
	IsRead    bool                   `json:"is_read"`
	CreatedAt time.Time              `json:"created_at"`
}

const (
	NotifNewBid           = "new_bid"
	NotifBidAccepted      = "bid_accepted"
	NotifBidRejected      = "bid_rejected"
	NotifTenderDeadline   = "tender_deadline"
	NotifNewTender        = "new_tender"
	NotifTenderCompleted  = "tender_completed"
)
