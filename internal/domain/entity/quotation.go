package entity

import (
	"time"

	"github.com/google/uuid"
)

// Quotation represents a sales quotation/proposal
type Quotation struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	TenantID        uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	QuotationNumber string     `json:"quotation_number" db:"quotation_number"`
	CustomerID      *uuid.UUID `json:"customer_id,omitempty" db:"customer_id"`
	CustomerName    string     `json:"customer_name" db:"customer_name"`
	ContactPerson   *string    `json:"contact_person,omitempty" db:"contact_person"`
	Email           *string    `json:"email,omitempty" db:"email"`
	ValidUntil      *time.Time `json:"valid_until,omitempty" db:"valid_until"`

	Subtotal        float64 `json:"subtotal" db:"subtotal"`
	DiscountPercent float64 `json:"discount_percent" db:"discount_percent"`
	DiscountAmount  float64 `json:"discount_amount" db:"discount_amount"`
	TaxPercent      float64 `json:"tax_percent" db:"tax_percent"`
	TaxAmount       float64 `json:"tax_amount" db:"tax_amount"`
	TotalAmount     float64 `json:"total_amount" db:"total_amount"`

	Status           string     `json:"status" db:"status"`
	ConvertedToOrder *string    `json:"converted_to_order,omitempty" db:"converted_to_order"`
	SalesOrderID     *uuid.UUID `json:"sales_order_id,omitempty" db:"sales_order_id"`

	Notes     *string    `json:"notes,omitempty" db:"notes"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`

	// Related data (not in DB)
	Items []QuotationItem `json:"items,omitempty"`
}

// QuotationItem represents a line item in a quotation
type QuotationItem struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	QuotationID uuid.UUID  `json:"quotation_id" db:"quotation_id"`
	ProductID   *uuid.UUID `json:"product_id,omitempty" db:"product_id"`
	ProductName string     `json:"product_name" db:"product_name"`
	Quantity    float64    `json:"quantity" db:"quantity"`
	UnitPrice   float64    `json:"unit_price" db:"unit_price"`
	Total       float64    `json:"total" db:"total"`
	Notes       *string    `json:"notes,omitempty" db:"notes"`
	SortOrder   int        `json:"sort_order" db:"sort_order"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// CreateQuotationInput represents input for creating a quotation
type CreateQuotationInput struct {
	CustomerID      string              `json:"customer_id,omitempty"`
	CustomerName    string              `json:"customer_name"`
	ContactPerson   string              `json:"contact_person,omitempty"`
	Email           string              `json:"email,omitempty"`
	ValidUntil      string              `json:"valid_until,omitempty"`
	DiscountPercent float64             `json:"discount_percent"`
	TaxPercent      float64             `json:"tax_percent"`
	Notes           string              `json:"notes,omitempty"`
	Items           []QuotationItemInput `json:"items"`
}

// UpdateQuotationInput represents input for updating a quotation
type UpdateQuotationInput struct {
	CustomerID      *string              `json:"customer_id,omitempty"`
	CustomerName    *string              `json:"customer_name,omitempty"`
	ContactPerson   *string              `json:"contact_person,omitempty"`
	Email           *string              `json:"email,omitempty"`
	ValidUntil      *string              `json:"valid_until,omitempty"`
	DiscountPercent *float64             `json:"discount_percent,omitempty"`
	TaxPercent      *float64             `json:"tax_percent,omitempty"`
	Status          *string              `json:"status,omitempty"`
	Notes           *string              `json:"notes,omitempty"`
	Items           *[]QuotationItemInput `json:"items,omitempty"`
}

// QuotationItemInput represents input for a quotation line item
type QuotationItemInput struct {
	ProductID   string  `json:"product_id,omitempty"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

// QuotationResponse represents the API response for a quotation
type QuotationResponse struct {
	ID              uuid.UUID              `json:"id"`
	QuotationNumber string                 `json:"quotation_number"`
	CustomerID      string                 `json:"customer_id,omitempty"`
	CustomerName    string                 `json:"customer_name"`
	ContactPerson   string                 `json:"contact_person,omitempty"`
	Email           string                 `json:"email,omitempty"`
	ValidUntil      string                 `json:"valid_until,omitempty"`
	Subtotal        float64                `json:"subtotal"`
	DiscountPercent float64                `json:"discount_percent"`
	DiscountAmount  float64                `json:"discount_amount"`
	TaxPercent      float64                `json:"tax_percent"`
	TaxAmount       float64                `json:"tax_amount"`
	TotalAmount     float64                `json:"total_amount"`
	Status          string                 `json:"status"`
	ConvertedToOrder string                `json:"converted_to_order,omitempty"`
	Notes           string                 `json:"notes,omitempty"`
	Items           []QuotationItemResponse `json:"items"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// QuotationItemResponse represents the API response for a quotation item
type QuotationItemResponse struct {
	ID          uuid.UUID `json:"id"`
	ProductID   string    `json:"product_id,omitempty"`
	ProductName string    `json:"product_name"`
	Quantity    float64   `json:"quantity"`
	UnitPrice   float64   `json:"unit_price"`
	Total       float64   `json:"total"`
}

// ToResponse converts Quotation to QuotationResponse
func (q *Quotation) ToResponse() *QuotationResponse {
	resp := &QuotationResponse{
		ID:              q.ID,
		QuotationNumber: q.QuotationNumber,
		CustomerName:    q.CustomerName,
		Subtotal:        q.Subtotal,
		DiscountPercent: q.DiscountPercent,
		DiscountAmount:  q.DiscountAmount,
		TaxPercent:      q.TaxPercent,
		TaxAmount:       q.TaxAmount,
		TotalAmount:     q.TotalAmount,
		Status:          q.Status,
		Items:           make([]QuotationItemResponse, 0),
		CreatedAt:       q.CreatedAt,
		UpdatedAt:       q.UpdatedAt,
	}

	if q.CustomerID != nil {
		resp.CustomerID = q.CustomerID.String()
	}
	if q.ContactPerson != nil {
		resp.ContactPerson = *q.ContactPerson
	}
	if q.Email != nil {
		resp.Email = *q.Email
	}
	if q.ValidUntil != nil {
		resp.ValidUntil = q.ValidUntil.Format("2006-01-02")
	}
	if q.ConvertedToOrder != nil {
		resp.ConvertedToOrder = *q.ConvertedToOrder
	}
	if q.Notes != nil {
		resp.Notes = *q.Notes
	}

	for _, item := range q.Items {
		itemResp := QuotationItemResponse{
			ID:          item.ID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
			Total:       item.Total,
		}
		if item.ProductID != nil {
			itemResp.ProductID = item.ProductID.String()
		}
		resp.Items = append(resp.Items, itemResp)
	}

	return resp
}
