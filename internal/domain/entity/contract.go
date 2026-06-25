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
	ContractTypeFixed   ContractType = "fixed"
	ContractTypeAnnual  ContractType = "annual"
	ContractTypeMonthly ContractType = "monthly"
	ContractTypeProject ContractType = "project"
)

// Contract represents a procurement contract
type Contract struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	TenantID        uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	ContractNumber  string          `json:"contract_number" db:"contract_number"`
	Title           string          `json:"title" db:"title"`
	PartyType       string          `json:"party_type" db:"party_type"`
	VendorID        *uuid.UUID      `json:"vendor_id,omitempty" db:"vendor_id"`
	AssetID         *uuid.UUID      `json:"asset_id,omitempty" db:"asset_id"`
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
	CustomFields    json.RawMessage `json:"custom_fields" db:"custom_fields" swaggertype:"object"`
	CreatedBy       *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime    `json:"-" db:"deleted_at"`

	// Computed
	VendorName   string `json:"vendor_name,omitempty"`
	AssetName    string `json:"asset_name,omitempty"`
	DaysToExpiry int    `json:"days_to_expiry,omitempty"`
}

// CreateContractInput represents input for creating a contract
type CreateContractInput struct {
	Title           string  `json:"title" binding:"required"`
	PartyType       string  `json:"party_type,omitempty"` // customer, vendor, partner, lease (default vendor)
	VendorID        string  `json:"vendor_id,omitempty"`  // counterparty contact (customer/vendor/partner; optional lessor for lease)
	AssetID         string  `json:"asset_id,omitempty"`   // required for lease
	ContractType    string  `json:"contract_type" binding:"required"`
	StartDate       string  `json:"start_date" binding:"required"`
	EndDate         string  `json:"end_date,omitempty"`
	Value           float64 `json:"value" binding:"required,gte=0"`
	CurrencyID      string  `json:"currency_id,omitempty"`
	Terms           string  `json:"terms,omitempty"`
	Description     string  `json:"description,omitempty"`
	AutoRenewal     bool    `json:"auto_renewal,omitempty"`
	RenewalTermDays int     `json:"renewal_term_days,omitempty"`
	Notes           string  `json:"notes,omitempty"`
}

// UpdateContractInput represents input for updating a contract
type UpdateContractInput struct {
	Title           *string  `json:"title,omitempty"`
	PartyType       *string  `json:"party_type,omitempty"`
	VendorID        *string  `json:"vendor_id,omitempty"`
	AssetID         *string  `json:"asset_id,omitempty"`
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
	PartyType       string         `json:"party_type"`
	VendorID        *uuid.UUID     `json:"vendor_id,omitempty"`
	VendorName      string         `json:"vendor_name"`
	AssetID         *uuid.UUID     `json:"asset_id,omitempty"`
	AssetName       string         `json:"asset_name,omitempty"`
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
	AttachmentCount int            `json:"attachment_count"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}
