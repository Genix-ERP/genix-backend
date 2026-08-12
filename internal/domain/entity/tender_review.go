package entity

import (
	"time"

	"github.com/google/uuid"
)

type TenderReview struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	TenderID            *uuid.UUID `json:"tender_id" db:"tender_id"`
	ReviewerID          uuid.UUID  `json:"reviewer_id" db:"reviewer_id"`
	SupplierID          uuid.UUID  `json:"supplier_id" db:"supplier_id"`
	QualityRating       int        `json:"quality_rating" db:"quality_rating"`
	PriceRating         int        `json:"price_rating" db:"price_rating"`
	DeliveryRating      int        `json:"delivery_rating" db:"delivery_rating"`
	CommunicationRating int        `json:"communication_rating" db:"communication_rating"`
	OverallRating       float64    `json:"overall_rating" db:"overall_rating"`
	Comment             string     `json:"comment" db:"comment"`
	IsVisible           bool       `json:"is_visible" db:"is_visible"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
}

type CreateReviewInput struct {
	TenderID            *uuid.UUID `json:"tender_id"`
	QualityRating       int        `json:"quality_rating" binding:"required,min=1,max=5"`
	PriceRating         int        `json:"price_rating" binding:"required,min=1,max=5"`
	DeliveryRating      int        `json:"delivery_rating" binding:"required,min=1,max=5"`
	CommunicationRating int        `json:"communication_rating" binding:"required,min=1,max=5"`
	Comment             string     `json:"comment"`
}

type ReviewResponse struct {
	ID                  uuid.UUID  `json:"id"`
	TenderID            *uuid.UUID `json:"tender_id"`
	ReviewerID          uuid.UUID  `json:"reviewer_id"`
	ReviewerName        string     `json:"reviewer_name"`
	QualityRating       int        `json:"quality_rating"`
	PriceRating         int        `json:"price_rating"`
	DeliveryRating      int        `json:"delivery_rating"`
	CommunicationRating int        `json:"communication_rating"`
	OverallRating       float64    `json:"overall_rating"`
	Comment             string     `json:"comment"`
	CreatedAt           time.Time  `json:"created_at"`
}
