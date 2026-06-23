package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ContactType represents the type of contact
type ContactType string

const (
	ContactTypeCustomer ContactType = "customer"
	ContactTypeVendor   ContactType = "vendor"
	ContactTypeBoth     ContactType = "both"
	ContactTypePartner  ContactType = "partner"
)

// Contact represents a customer, vendor, or partner
type Contact struct {
	ID                 uuid.UUID       `json:"id" db:"id"`
	TenantID           uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	OrganizationID     *uuid.UUID      `json:"organization_id,omitempty" db:"organization_id"`
	Type               ContactType     `json:"type" db:"type"`
	Code               string          `json:"code" db:"code"`
	Name               string          `json:"name" db:"name"`
	LegalName          *string         `json:"legal_name,omitempty" db:"legal_name"`
	TaxID              *string         `json:"tax_id,omitempty" db:"tax_id"`
	RegistrationNumber *string         `json:"registration_number,omitempty" db:"registration_number"`
	Industry           *string         `json:"industry,omitempty" db:"industry"`
	Website            *string         `json:"website,omitempty" db:"website"`
	Email              *string         `json:"email,omitempty" db:"email"`
	Phone              *string         `json:"phone,omitempty" db:"phone"`
	Fax                *string         `json:"fax,omitempty" db:"fax"`
	ContactPerson      *string         `json:"contact_person,omitempty" db:"contact_person"`
	BillingAddress     json.RawMessage `json:"billing_address" db:"billing_address" swaggertype:"object"`
	ShippingAddress    json.RawMessage `json:"shipping_address" db:"shipping_address" swaggertype:"object"`
	PaymentTerms       int             `json:"payment_terms" db:"payment_terms"`
	CreditLimit        float64         `json:"credit_limit" db:"credit_limit"`
	CurrentBalance     float64         `json:"current_balance" db:"current_balance"`
	CurrencyID         *uuid.UUID      `json:"currency_id,omitempty" db:"currency_id"`
	TaxExempt          bool            `json:"tax_exempt" db:"tax_exempt"`
	Tags               json.RawMessage `json:"tags" db:"tags" swaggertype:"object"`
	Notes              *string         `json:"notes,omitempty" db:"notes"`
	ExpectedRevenue    *float64        `json:"expected_revenue,omitempty" db:"expected_revenue"`
	CustomFields       json.RawMessage `json:"custom_fields" db:"custom_fields" swaggertype:"object"`
	IsActive           bool            `json:"is_active" db:"is_active"`
	CreatedBy          *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt          sql.NullTime    `json:"-" db:"deleted_at"`

	// Relationships
	ContactPersons []ContactPerson `json:"contact_persons,omitempty"`
}

// ContactPerson represents a contact person for a contact
type ContactPerson struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	ContactID  uuid.UUID  `json:"contact_id" db:"contact_id"`
	FirstName  string     `json:"first_name" db:"first_name"`
	LastName   string     `json:"last_name" db:"last_name"`
	Title      *string    `json:"title,omitempty" db:"title"`
	Email      *string    `json:"email,omitempty" db:"email"`
	Phone      *string    `json:"phone,omitempty" db:"phone"`
	Mobile     *string    `json:"mobile,omitempty" db:"mobile"`
	IsPrimary  bool       `json:"is_primary" db:"is_primary"`
	Department *string    `json:"department,omitempty" db:"department"`
	Notes      *string    `json:"notes,omitempty" db:"notes"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
}

// FullName returns the contact person's full name
func (cp *ContactPerson) FullName() string {
	return cp.FirstName + " " + cp.LastName
}

// CreateContactInput represents input for creating a contact
type CreateContactInput struct {
	Type               ContactType `json:"type" binding:"required,oneof=customer vendor both partner"`
	Code               string      `json:"code" binding:"required,min=1,max=50"`
	Name               string      `json:"name" binding:"required,min=1,max=255"`
	LegalName          string      `json:"legal_name,omitempty"`
	TaxID              string      `json:"tax_id,omitempty"`
	RegistrationNumber string      `json:"registration_number,omitempty"`
	Industry           string      `json:"industry,omitempty"`
	Website            string      `json:"website,omitempty"`
	Email              string      `json:"email,omitempty" binding:"omitempty,email"`
	Phone              string      `json:"phone,omitempty"`
	Fax                string      `json:"fax,omitempty"`
	ContactPerson      string      `json:"contact_person,omitempty"`
	BillingAddress     *Address    `json:"billing_address,omitempty"`
	ShippingAddress    *Address    `json:"shipping_address,omitempty"`
	PaymentTerms       int         `json:"payment_terms,omitempty"`
	CreditLimit        float64     `json:"credit_limit,omitempty"`
	CurrencyID         string      `json:"currency_id,omitempty"`
	TaxExempt          bool                   `json:"tax_exempt,omitempty"`
	Tags               []string               `json:"tags,omitempty"`
	Notes              string                 `json:"notes,omitempty"`
	ExpectedRevenue    *float64               `json:"expected_revenue,omitempty"`
	CustomFields       map[string]interface{} `json:"custom_fields,omitempty"`
	DefaultReceivableAccountID string         `json:"default_receivable_account_id,omitempty"`
	DefaultPayableAccountID    string         `json:"default_payable_account_id,omitempty"`
	ContactPersons     []CreateContactPersonInput `json:"contact_persons,omitempty"`
}

// UpdateContactInput represents input for updating a contact
type UpdateContactInput struct {
	Name               *string                `json:"name,omitempty"`
	LegalName          *string                `json:"legal_name,omitempty"`
	TaxID              *string                `json:"tax_id,omitempty"`
	RegistrationNumber *string                `json:"registration_number,omitempty"`
	Industry           *string                `json:"industry,omitempty"`
	Website            *string                `json:"website,omitempty"`
	Email              *string                `json:"email,omitempty"`
	Phone              *string                `json:"phone,omitempty"`
	Fax                *string                `json:"fax,omitempty"`
	ContactPerson      *string                `json:"contact_person,omitempty"`
	BillingAddress     *Address               `json:"billing_address,omitempty"`
	ShippingAddress    *Address               `json:"shipping_address,omitempty"`
	PaymentTerms       *int                   `json:"payment_terms,omitempty"`
	CreditLimit        *float64               `json:"credit_limit,omitempty"`
	TaxExempt          *bool                  `json:"tax_exempt,omitempty"`
	Tags               []string               `json:"tags,omitempty"`
	Notes              *string                `json:"notes,omitempty"`
	ExpectedRevenue    *float64               `json:"expected_revenue,omitempty"`
	CustomFields       map[string]interface{} `json:"custom_fields,omitempty"`
	IsActive                   *bool                  `json:"is_active,omitempty"`
	DefaultReceivableAccountID *string                `json:"default_receivable_account_id,omitempty"`
	DefaultPayableAccountID    *string                `json:"default_payable_account_id,omitempty"`
}

// CreateContactPersonInput represents input for creating a contact person
type CreateContactPersonInput struct {
	FirstName  string `json:"first_name" binding:"required,min=1,max=100"`
	LastName   string `json:"last_name" binding:"required,min=1,max=100"`
	Title      string `json:"title,omitempty"`
	Email      string `json:"email,omitempty" binding:"omitempty,email"`
	Phone      string `json:"phone,omitempty"`
	Mobile     string `json:"mobile,omitempty"`
	IsPrimary  bool   `json:"is_primary,omitempty"`
	Department string `json:"department,omitempty"`
	Notes      string `json:"notes,omitempty"`
}

// ContactListFilter represents filters for listing contacts
type ContactListFilter struct {
	Search      string      `form:"search"`
	Type        ContactType `form:"type"`
	IsActive    *bool       `form:"is_active"`
	Industry    string      `form:"industry"`
	MinBalance  *float64    `form:"min_balance"`
	MaxBalance  *float64    `form:"max_balance"`
	HasOverdue  *bool       `form:"has_overdue"`
}

// ContactResponse represents the API response for a contact
type ContactResponse struct {
	ID                 uuid.UUID              `json:"id"`
	Type               ContactType            `json:"type"`
	Code               string                 `json:"code"`
	Name               string                 `json:"name"`
	LegalName          *string                `json:"legal_name,omitempty"`
	TaxID              *string                `json:"tax_id,omitempty"`
	Industry           *string                `json:"industry,omitempty"`
	Email              *string                `json:"email,omitempty"`
	Phone              *string                `json:"phone,omitempty"`
	ContactPerson      *string                `json:"contact_person,omitempty"`
	BillingAddress     *Address               `json:"billing_address,omitempty"`
	ShippingAddress    *Address               `json:"shipping_address,omitempty"`
	PaymentTerms       int                    `json:"payment_terms"`
	CreditLimit        float64                `json:"credit_limit"`
	CurrentBalance     float64                `json:"current_balance"`
	TaxExempt          bool                   `json:"tax_exempt"`
	Tags               []string               `json:"tags"`
	Notes              *string                `json:"notes,omitempty"`
	ExpectedRevenue    *float64               `json:"expected_revenue,omitempty"`
	CustomFields       map[string]interface{} `json:"custom_fields,omitempty"`
	IsActive           bool                   `json:"is_active"`
	Rating             *float64               `json:"rating,omitempty"`
	RatingCount        *int                   `json:"rating_count,omitempty"`
	SourceOrganizationID       *string               `json:"source_organization_id,omitempty"`
	DefaultReceivableAccountID *string              `json:"default_receivable_account_id,omitempty"`
	DefaultPayableAccountID    *string              `json:"default_payable_account_id,omitempty"`
	ContactPersons     []ContactPerson        `json:"contact_persons,omitempty"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}
