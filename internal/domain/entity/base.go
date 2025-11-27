package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// BaseEntity contains common fields for all entities
type BaseEntity struct {
	ID        uuid.UUID    `json:"id" db:"id"`
	CreatedAt time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt time.Time    `json:"updated_at" db:"updated_at"`
	DeletedAt sql.NullTime `json:"-" db:"deleted_at"`
}

// TenantEntity extends BaseEntity with tenant support
type TenantEntity struct {
	BaseEntity
	TenantID uuid.UUID `json:"tenant_id" db:"tenant_id"`
}

// AuditableEntity extends TenantEntity with audit fields
type AuditableEntity struct {
	TenantEntity
	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
}

// NewID generates a new UUID
func NewID() uuid.UUID {
	return uuid.New()
}

// ParseID parses a string into a UUID
func ParseID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

// NullUUID represents a nullable UUID
type NullUUID struct {
	UUID  uuid.UUID
	Valid bool
}

// Scan implements the sql.Scanner interface
func (n *NullUUID) Scan(value interface{}) error {
	if value == nil {
		n.UUID, n.Valid = uuid.UUID{}, false
		return nil
	}
	n.Valid = true
	return n.UUID.Scan(value)
}

// Pagination represents pagination parameters
type Pagination struct {
	Page     int `json:"page" form:"page"`
	Limit    int `json:"limit" form:"limit"`
	Total    int `json:"total"`
	Pages    int `json:"pages"`
	HasNext  bool `json:"has_next"`
	HasPrev  bool `json:"has_prev"`
}

// NewPagination creates a new pagination with defaults
func NewPagination(page, limit int) *Pagination {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return &Pagination{
		Page:  page,
		Limit: limit,
	}
}

// Offset calculates the offset for SQL queries
func (p *Pagination) Offset() int {
	return (p.Page - 1) * p.Limit
}

// Calculate calculates pagination metadata
func (p *Pagination) Calculate(total int) {
	p.Total = total
	p.Pages = (total + p.Limit - 1) / p.Limit
	p.HasNext = p.Page < p.Pages
	p.HasPrev = p.Page > 1
}

// SortOrder represents sort direction
type SortOrder string

const (
	SortAsc  SortOrder = "ASC"
	SortDesc SortOrder = "DESC"
)

// Sort represents sorting parameters
type Sort struct {
	Field string    `json:"field" form:"sort_by"`
	Order SortOrder `json:"order" form:"sort_order"`
}

// ListParams combines pagination and sorting
type ListParams struct {
	Pagination *Pagination
	Sort       *Sort
	Search     string `form:"search"`
	Filters    map[string]interface{}
}

// NewListParams creates new list parameters with defaults
func NewListParams(page, limit int, sortField string, sortOrder SortOrder) *ListParams {
	return &ListParams{
		Pagination: NewPagination(page, limit),
		Sort: &Sort{
			Field: sortField,
			Order: sortOrder,
		},
		Filters: make(map[string]interface{}),
	}
}

// Address represents a physical address
type Address struct {
	Street1    string `json:"street1,omitempty"`
	Street2    string `json:"street2,omitempty"`
	City       string `json:"city,omitempty"`
	State      string `json:"state,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"`
}

// ContactInfo represents contact information
type ContactInfo struct {
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Fax     string `json:"fax,omitempty"`
	Website string `json:"website,omitempty"`
}

// Money represents a monetary value with currency
type Money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// NewMoney creates a new Money value
func NewMoney(amount float64, currency string) Money {
	return Money{Amount: amount, Currency: currency}
}

// Add adds two Money values
func (m Money) Add(other Money) Money {
	if m.Currency != other.Currency {
		panic("cannot add different currencies")
	}
	return Money{Amount: m.Amount + other.Amount, Currency: m.Currency}
}

// Subtract subtracts two Money values
func (m Money) Subtract(other Money) Money {
	if m.Currency != other.Currency {
		panic("cannot subtract different currencies")
	}
	return Money{Amount: m.Amount - other.Amount, Currency: m.Currency}
}

// Multiply multiplies Money by a factor
func (m Money) Multiply(factor float64) Money {
	return Money{Amount: m.Amount * factor, Currency: m.Currency}
}

// DateRange represents a date range
type DateRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Contains checks if a date is within the range
func (dr DateRange) Contains(date time.Time) bool {
	return !date.Before(dr.From) && !date.After(dr.To)
}
