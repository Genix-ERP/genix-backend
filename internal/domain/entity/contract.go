package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Contract represents a supplier/vendor contract
type Contract struct {
	ID                 uuid.UUID    `json:"id" db:"id"`
	TenantID           uuid.UUID    `json:"tenant_id" db:"tenant_id"`
	ContractNumber     string       `json:"contract_number" db:"contract_number"`
	SupplierID         *uuid.UUID   `json:"supplier_id,omitempty" db:"supplier_id"`
	SupplierName       string       `json:"supplier_name" db:"supplier_name"`
	Title              string       `json:"title" db:"title"`
	Description        *string      `json:"description,omitempty" db:"description"`
	ContractType       string       `json:"contract_type" db:"contract_type"`
	StartDate          time.Time    `json:"start_date" db:"start_date"`
	EndDate            time.Time    `json:"end_date" db:"end_date"`
	Value              float64      `json:"value" db:"value"`
	Currency           string       `json:"currency" db:"currency"`
	PaymentTerms       *string      `json:"payment_terms,omitempty" db:"payment_terms"`
	Terms              *string      `json:"terms,omitempty" db:"terms"`
	AutoRenew          bool         `json:"auto_renew" db:"auto_renew"`
	RenewalNoticeDays  int          `json:"renewal_notice_days" db:"renewal_notice_days"`
	Status             string       `json:"status" db:"status"`
	CreatedBy          *uuid.UUID   `json:"created_by,omitempty" db:"created_by"`
	CreatedAt          time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt          sql.NullTime `json:"-" db:"deleted_at"`
}

// CreateContractInput represents input for creating a contract
type CreateContractInput struct {
	ContractNumber    string  `json:"contract_number"`
	SupplierID        string  `json:"supplier_id,omitempty"`
	SupplierName      string  `json:"supplier_name" binding:"required"`
	Title             string  `json:"title" binding:"required"`
	Description       string  `json:"description,omitempty"`
	ContractType      string  `json:"type" binding:"required"`
	StartDate         string  `json:"start_date" binding:"required"`
	EndDate           string  `json:"end_date" binding:"required"`
	Value             float64 `json:"value"`
	Currency          string  `json:"currency"`
	PaymentTerms      string  `json:"payment_terms,omitempty"`
	Terms             string  `json:"terms,omitempty"`
	AutoRenew         bool    `json:"auto_renew"`
	RenewalNoticeDays int     `json:"renewal_notice_days"`
}

// UpdateContractInput represents input for updating a contract
type UpdateContractInput struct {
	SupplierID        *string  `json:"supplier_id,omitempty"`
	SupplierName      *string  `json:"supplier_name,omitempty"`
	Title             *string  `json:"title,omitempty"`
	Description       *string  `json:"description,omitempty"`
	ContractType      *string  `json:"type,omitempty"`
	StartDate         *string  `json:"start_date,omitempty"`
	EndDate           *string  `json:"end_date,omitempty"`
	Value             *float64 `json:"value,omitempty"`
	Currency          *string  `json:"currency,omitempty"`
	PaymentTerms      *string  `json:"payment_terms,omitempty"`
	Terms             *string  `json:"terms,omitempty"`
	AutoRenew         *bool    `json:"auto_renew,omitempty"`
	RenewalNoticeDays *int     `json:"renewal_notice_days,omitempty"`
	Status            *string  `json:"status,omitempty"`
}

// ContractResponse represents the API response for a contract
type ContractResponse struct {
	ID                uuid.UUID `json:"id"`
	ContractNumber    string    `json:"contract_number"`
	SupplierID        string    `json:"supplier_id,omitempty"`
	SupplierName      string    `json:"supplier_name"`
	Title             string    `json:"title"`
	Description       string    `json:"description,omitempty"`
	ContractType      string    `json:"type"`
	StartDate         string    `json:"start_date"`
	EndDate           string    `json:"end_date"`
	Value             float64   `json:"value"`
	Currency          string    `json:"currency"`
	PaymentTerms      string    `json:"payment_terms,omitempty"`
	Terms             string    `json:"terms,omitempty"`
	AutoRenew         bool      `json:"auto_renew"`
	RenewalNoticeDays int       `json:"renewal_notice_days"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ToResponse converts Contract to ContractResponse
func (c *Contract) ToResponse() *ContractResponse {
	resp := &ContractResponse{
		ID:                c.ID,
		ContractNumber:    c.ContractNumber,
		SupplierName:      c.SupplierName,
		Title:             c.Title,
		ContractType:      c.ContractType,
		StartDate:         c.StartDate.Format("2006-01-02"),
		EndDate:           c.EndDate.Format("2006-01-02"),
		Value:             c.Value,
		Currency:          c.Currency,
		AutoRenew:         c.AutoRenew,
		RenewalNoticeDays: c.RenewalNoticeDays,
		Status:            c.Status,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
	}

	if c.SupplierID != nil {
		resp.SupplierID = c.SupplierID.String()
	}
	if c.Description != nil {
		resp.Description = *c.Description
	}
	if c.PaymentTerms != nil {
		resp.PaymentTerms = *c.PaymentTerms
	}
	if c.Terms != nil {
		resp.Terms = *c.Terms
	}

	return resp
}
