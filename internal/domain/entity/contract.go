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

package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ContractStatus represents the status of a contract
type ContractStatus string

const (
	ContractStatusDraft      ContractStatus = "draft"
	ContractStatusActive     ContractStatus = "active"
	ContractStatusExpired    ContractStatus = "expired"
	ContractStatusTerminated ContractStatus = "terminated"
)

// ContractType represents the type of contract
type ContractType string

const (
	ContractTypeFixed    ContractType = "fixed"
	ContractTypeAnnual   ContractType = "annual"
	ContractTypeMonthly  ContractType = "monthly"
	ContractTypeProject  ContractType = "project"
)

// Contract represents a procurement contract
type Contract struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	TenantID        uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	ContractNumber  string          `json:"contract_number" db:"contract_number"`
	Title           string          `json:"title" db:"title"`
	VendorID        uuid.UUID       `json:"vendor_id" db:"vendor_id"`
	ContractType    ContractType    `json:"contract_type" db:"contract_type"`
	Status          ContractStatus  `json:"status" db:"status"`
	StartDate       time.Time       `json:"start_date" db:"start_date"`
	EndDate         *time.Time      `json:"end_date,omitempty" db:"end_date"`
	Value           float64         `json:"value" db:"value"`
	CurrencyID      *uuid.UUID      `json:"currency_id,omitempty" db:"currency_id"`
	Terms           *string         `json:"terms,omitempty" db:"terms"`
	Description     *string         `json:"description,omitempty" db:"description"`
	AutoRenewal     bool            `json:"auto_renewal" db:"auto_renewal"`
	RenewalTermDays int             `json:"renewal_term_days" db:"renewal_term_days"`
	Notes           *string         `json:"notes,omitempty" db:"notes"`
	DocumentURL     *string         `json:"document_url,omitempty" db:"document_url"`
	CustomFields    json.RawMessage `json:"custom_fields" db:"custom_fields"`
	CreatedBy       *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime    `json:"-" db:"deleted_at"`

	// Computed
	VendorName   string `json:"vendor_name,omitempty"`
	DaysToExpiry int    `json:"days_to_expiry,omitempty"`
}

// CreateContractInput represents input for creating a contract
type CreateContractInput struct {
	Title           string `json:"title" binding:"required"`
	VendorID        string `json:"vendor_id" binding:"required"`
	ContractType    string `json:"contract_type" binding:"required"`
	StartDate       string `json:"start_date" binding:"required"`
	EndDate         string `json:"end_date,omitempty"`
	Value           float64 `json:"value" binding:"required,gte=0"`
	CurrencyID      string `json:"currency_id,omitempty"`
	Terms           string `json:"terms,omitempty"`
	Description     string `json:"description,omitempty"`
	AutoRenewal     bool   `json:"auto_renewal,omitempty"`
	RenewalTermDays int    `json:"renewal_term_days,omitempty"`
	Notes           string `json:"notes,omitempty"`
}

// UpdateContractInput represents input for updating a contract
type UpdateContractInput struct {
	Title           *string  `json:"title,omitempty"`
	ContractType    *string  `json:"contract_type,omitempty"`
	EndDate         *string  `json:"end_date,omitempty"`
	Value           *float64 `json:"value,omitempty"`
	Terms           *string  `json:"terms,omitempty"`
	Description     *string  `json:"description,omitempty"`
	AutoRenewal     *bool    `json:"auto_renewal,omitempty"`
	RenewalTermDays *int     `json:"renewal_term_days,omitempty"`
	Notes           *string  `json:"notes,omitempty"`
	Status          *string  `json:"status,omitempty"`
}

// ContractListFilter represents filters for listing contracts
type ContractListFilter struct {
	Search       string         `form:"search"`
	Status       ContractStatus `form:"status"`
	ContractType ContractType   `form:"contract_type"`
	VendorID     string         `form:"vendor_id"`
	ExpiringSoon bool           `form:"expiring_soon"`
}

// ContractResponse represents the API response for a contract
type ContractResponse struct {
	ID              uuid.UUID      `json:"id"`
	ContractNumber  string         `json:"contract_number"`
	Title           string         `json:"title"`
	VendorID        uuid.UUID      `json:"vendor_id"`
	VendorName      string         `json:"vendor_name"`
	ContractType    ContractType   `json:"contract_type"`
	Status          ContractStatus `json:"status"`
	StartDate       time.Time      `json:"start_date"`
	EndDate         *time.Time     `json:"end_date,omitempty"`
	Value           float64        `json:"value"`
	Terms           *string        `json:"terms,omitempty"`
	Description     *string        `json:"description,omitempty"`
	AutoRenewal     bool           `json:"auto_renewal"`
	RenewalTermDays int            `json:"renewal_term_days"`
	Notes           *string        `json:"notes,omitempty"`
	DaysToExpiry    int            `json:"days_to_expiry"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}
