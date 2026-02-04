package entity

import (
	"time"

	"github.com/google/uuid"
)

// QuotationTemplate represents a reusable quotation blueprint
type QuotationTemplate struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	TenantID            uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Name                string     `json:"name" db:"name"`
	Code                *string    `json:"code,omitempty" db:"code"`
	Description         *string    `json:"description,omitempty" db:"description"`
	ValidityDays        int        `json:"validity_days" db:"validity_days"`
	PaymentTermID       *uuid.UUID `json:"payment_term_id,omitempty" db:"payment_term_id"`
	PricelistID         *uuid.UUID `json:"pricelist_id,omitempty" db:"pricelist_id"`
	TermsAndConditions  *string    `json:"terms_and_conditions,omitempty" db:"terms_and_conditions"`
	HeaderNote          *string    `json:"header_note,omitempty" db:"header_note"`
	FooterNote          *string    `json:"footer_note,omitempty" db:"footer_note"`
	RequireSignature    bool       `json:"require_signature" db:"require_signature"`
	RequirePayment      bool       `json:"require_payment" db:"require_payment"`
	PaymentPercentage   float64    `json:"payment_percentage" db:"payment_percentage"`
	ShowLineSubtotals   bool       `json:"show_line_subtotals" db:"show_line_subtotals"`
	ShowLineDiscounts   bool       `json:"show_line_discounts" db:"show_line_discounts"`
	GroupBySection      bool       `json:"group_by_section" db:"group_by_section"`
	IsActive            bool       `json:"is_active" db:"is_active"`
	CreatedBy           *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	PaymentTerm *PaymentTerm                `json:"payment_term,omitempty"`
	Pricelist   *Pricelist                  `json:"pricelist,omitempty"`
	Sections    []QuotationTemplateSection  `json:"sections,omitempty"`
	Lines       []QuotationTemplateLine     `json:"lines,omitempty"`
	Optionals   []QuotationTemplateOptional `json:"optionals,omitempty"`
}

// QuotationTemplateSection represents a section for grouping lines
type QuotationTemplateSection struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	TemplateID  uuid.UUID  `json:"template_id" db:"template_id"`
	Name        string     `json:"name" db:"name"`
	Description *string    `json:"description,omitempty" db:"description"`
	Sequence    int        `json:"sequence" db:"sequence"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// QuotationTemplateLine represents a product line in a template
type QuotationTemplateLine struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	TemplateID    uuid.UUID  `json:"template_id" db:"template_id"`
	SectionID     *uuid.UUID `json:"section_id,omitempty" db:"section_id"`
	ProductID     *uuid.UUID `json:"product_id,omitempty" db:"product_id"`
	ProductName   *string    `json:"product_name,omitempty" db:"product_name"`
	Description   *string    `json:"description,omitempty" db:"description"`
	Quantity      float64    `json:"quantity" db:"quantity"`
	UnitID        *uuid.UUID `json:"unit_id,omitempty" db:"unit_id"`
	UnitPrice     *float64   `json:"unit_price,omitempty" db:"unit_price"`
	DiscountType  *string    `json:"discount_type,omitempty" db:"discount_type"`
	DiscountValue float64    `json:"discount_value" db:"discount_value"`
	Sequence      int        `json:"sequence" db:"sequence"`
	DisplayType   string     `json:"display_type" db:"display_type"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Product *Product                  `json:"product,omitempty"`
	Section *QuotationTemplateSection `json:"section,omitempty"`
}

// QuotationTemplateOptional represents an optional upsell product
type QuotationTemplateOptional struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	TemplateID    uuid.UUID  `json:"template_id" db:"template_id"`
	ProductID     *uuid.UUID `json:"product_id,omitempty" db:"product_id"`
	ProductName   *string    `json:"product_name,omitempty" db:"product_name"`
	Description   *string    `json:"description,omitempty" db:"description"`
	Quantity      float64    `json:"quantity" db:"quantity"`
	UnitID        *uuid.UUID `json:"unit_id,omitempty" db:"unit_id"`
	UnitPrice     *float64   `json:"unit_price,omitempty" db:"unit_price"`
	DiscountType  *string    `json:"discount_type,omitempty" db:"discount_type"`
	DiscountValue float64    `json:"discount_value" db:"discount_value"`
	Sequence      int        `json:"sequence" db:"sequence"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Product *Product `json:"product,omitempty"`
}

// CreateQuotationTemplateInput represents input for creating a template
type CreateQuotationTemplateInput struct {
	Name               string                          `json:"name" binding:"required"`
	Code               string                          `json:"code,omitempty"`
	Description        string                          `json:"description,omitempty"`
	ValidityDays       int                             `json:"validity_days,omitempty"`
	PaymentTermID      string                          `json:"payment_term_id,omitempty"`
	PricelistID        string                          `json:"pricelist_id,omitempty"`
	TermsAndConditions string                          `json:"terms_and_conditions,omitempty"`
	HeaderNote         string                          `json:"header_note,omitempty"`
	FooterNote         string                          `json:"footer_note,omitempty"`
	RequireSignature   bool                            `json:"require_signature,omitempty"`
	RequirePayment     bool                            `json:"require_payment,omitempty"`
	PaymentPercentage  float64                         `json:"payment_percentage,omitempty"`
	ShowLineSubtotals  *bool                           `json:"show_line_subtotals,omitempty"`
	ShowLineDiscounts  *bool                           `json:"show_line_discounts,omitempty"`
	GroupBySection     *bool                           `json:"group_by_section,omitempty"`
	IsActive           *bool                           `json:"is_active,omitempty"`
	Sections           []TemplateSectionInput          `json:"sections,omitempty"`
	Lines              []TemplateLineInput             `json:"lines,omitempty"`
	Optionals          []TemplateOptionalInput         `json:"optionals,omitempty"`
}

// UpdateQuotationTemplateInput represents input for updating a template
type UpdateQuotationTemplateInput struct {
	Name               *string                 `json:"name,omitempty"`
	Code               *string                 `json:"code,omitempty"`
	Description        *string                 `json:"description,omitempty"`
	ValidityDays       *int                    `json:"validity_days,omitempty"`
	PaymentTermID      *string                 `json:"payment_term_id,omitempty"`
	PricelistID        *string                 `json:"pricelist_id,omitempty"`
	TermsAndConditions *string                 `json:"terms_and_conditions,omitempty"`
	HeaderNote         *string                 `json:"header_note,omitempty"`
	FooterNote         *string                 `json:"footer_note,omitempty"`
	RequireSignature   *bool                   `json:"require_signature,omitempty"`
	RequirePayment     *bool                   `json:"require_payment,omitempty"`
	PaymentPercentage  *float64                `json:"payment_percentage,omitempty"`
	ShowLineSubtotals  *bool                   `json:"show_line_subtotals,omitempty"`
	ShowLineDiscounts  *bool                   `json:"show_line_discounts,omitempty"`
	GroupBySection     *bool                   `json:"group_by_section,omitempty"`
	IsActive           *bool                   `json:"is_active,omitempty"`
	Sections           []TemplateSectionInput  `json:"sections,omitempty"`
	Lines              []TemplateLineInput     `json:"lines,omitempty"`
	Optionals          []TemplateOptionalInput `json:"optionals,omitempty"`
}

// TemplateSectionInput represents input for a template section
type TemplateSectionInput struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name" binding:"required"`
	Description string `json:"description,omitempty"`
	Sequence    int    `json:"sequence,omitempty"`
}

// TemplateLineInput represents input for a template line
type TemplateLineInput struct {
	ID            string   `json:"id,omitempty"`
	SectionID     string   `json:"section_id,omitempty"`
	ProductID     string   `json:"product_id,omitempty"`
	ProductName   string   `json:"product_name,omitempty"`
	Description   string   `json:"description,omitempty"`
	Quantity      float64  `json:"quantity,omitempty"`
	UnitID        string   `json:"unit_id,omitempty"`
	UnitPrice     *float64 `json:"unit_price,omitempty"`
	DiscountType  string   `json:"discount_type,omitempty"`
	DiscountValue float64  `json:"discount_value,omitempty"`
	Sequence      int      `json:"sequence,omitempty"`
	DisplayType   string   `json:"display_type,omitempty"`
}

// TemplateOptionalInput represents input for an optional product
type TemplateOptionalInput struct {
	ID            string   `json:"id,omitempty"`
	ProductID     string   `json:"product_id,omitempty"`
	ProductName   string   `json:"product_name,omitempty"`
	Description   string   `json:"description,omitempty"`
	Quantity      float64  `json:"quantity,omitempty"`
	UnitID        string   `json:"unit_id,omitempty"`
	UnitPrice     *float64 `json:"unit_price,omitempty"`
	DiscountType  string   `json:"discount_type,omitempty"`
	DiscountValue float64  `json:"discount_value,omitempty"`
	Sequence      int      `json:"sequence,omitempty"`
}

// QuotationTemplateListFilter represents filters for listing templates
type QuotationTemplateListFilter struct {
	Search   string `form:"search"`
	IsActive *bool  `form:"is_active"`
}

// QuotationTemplateResponse represents a template with computed fields
type QuotationTemplateResponse struct {
	QuotationTemplate
	LineCount     int     `json:"line_count"`
	OptionalCount int     `json:"optional_count"`
	TotalAmount   float64 `json:"total_amount"`
}

// ApplyTemplateInput represents input for applying a template to create a quotation
type ApplyTemplateInput struct {
	TemplateID        string   `json:"template_id" binding:"required"`
	CustomerID        string   `json:"customer_id" binding:"required"`
	IncludeOptionals  []string `json:"include_optionals,omitempty"`
	ExpirationDate    string   `json:"expiration_date,omitempty"`
	Notes             string   `json:"notes,omitempty"`
}
