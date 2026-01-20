package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// =====================================================
// CARGO SHIPMENT ENTITIES
// =====================================================

// CargoShipment represents a shipment in the cargo module
type CargoShipment struct {
	ID                int64          `json:"id" db:"id"`
	TenantID          uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	TrackingNumber    string         `json:"tracking_number" db:"tracking_number"`
	SupplierCountry   string         `json:"supplier_country" db:"supplier_country"`
	SupplierCompany   sql.NullString `json:"supplier_company" db:"supplier_company"`
	ExpectedDate      sql.NullTime   `json:"expected_date" db:"expected_date"`
	ActualArrivalDate sql.NullTime   `json:"actual_arrival_date" db:"actual_arrival_date"`
	Status            string         `json:"status" db:"status"` // ordered, in_transit, in_customs, received, distributed
	TransportCost     float64        `json:"transport_cost" db:"transport_cost"`
	CustomsCost       float64        `json:"customs_cost" db:"customs_cost"`
	InsuranceCost     float64        `json:"insurance_cost" db:"insurance_cost"`
	OtherCost         float64        `json:"other_cost" db:"other_cost"`
	TotalCost         float64        `json:"total_cost" db:"total_cost"`
	Notes             sql.NullString `json:"notes" db:"notes"`
	CreatedBy         uuid.NullUUID  `json:"created_by" db:"created_by"`
	CreatedDate       time.Time      `json:"created_date" db:"created_date"`
	UpdatedDate       time.Time      `json:"updated_date" db:"updated_date"`

	// Related entities (not in DB)
	Items         []CargoShipmentItem          `json:"items,omitempty" db:"-"`
	StatusHistory []CargoShipmentStatusHistory `json:"status_history,omitempty" db:"-"`
	Distributions []CargoDistribution          `json:"distributions,omitempty" db:"-"`
}

// CargoShipmentItem represents an item in a shipment
type CargoShipmentItem struct {
	ID          int64          `json:"id" db:"id"`
	ShipmentID  int64          `json:"shipment_id" db:"shipment_id"`
	ItemName    string         `json:"item_name" db:"item_name"`
	Quantity    float64        `json:"quantity" db:"quantity"`
	UnitPrice   float64        `json:"unit_price" db:"unit_price"`
	Currency    string         `json:"currency" db:"currency"` // USD, UZS, EUR
	TotalPrice  float64        `json:"total_price" db:"total_price"`
	HSCode      sql.NullString `json:"hs_code" db:"hs_code"`
	Description sql.NullString `json:"description" db:"description"`
	CreatedDate time.Time      `json:"created_date" db:"created_date"`
	UpdatedDate time.Time      `json:"updated_date" db:"updated_date"`
}

// CargoShipmentStatusHistory represents status change history
type CargoShipmentStatusHistory struct {
	ID          int64          `json:"id" db:"id"`
	ShipmentID  int64          `json:"shipment_id" db:"shipment_id"`
	Status      string         `json:"status" db:"status"`
	Note        sql.NullString `json:"note" db:"note"`
	Location    sql.NullString `json:"location" db:"location"`
	ChangedBy   uuid.NullUUID  `json:"changed_by" db:"changed_by"`
	ChangedDate time.Time      `json:"changed_date" db:"changed_date"`
}

// =====================================================
// CARGO DISTRIBUTION ENTITIES
// =====================================================

// CargoDistribution represents distribution of goods to B2B/B2C companies
type CargoDistribution struct {
	ID                   int64          `json:"id" db:"id"`
	ShipmentID           int64          `json:"shipment_id" db:"shipment_id"`
	RecipientTenantID    uuid.NullUUID  `json:"recipient_tenant_id" db:"recipient_tenant_id"`
	RecipientCompanyName string         `json:"recipient_company_name" db:"recipient_company_name"`
	RecipientCompanyType string         `json:"recipient_company_type" db:"recipient_company_type"` // B2B, B2C
	DistributionDate     time.Time      `json:"distribution_date" db:"distribution_date"`
	TotalItemsCost       float64        `json:"total_items_cost" db:"total_items_cost"`
	AllocatedCosts       float64        `json:"allocated_costs" db:"allocated_costs"`
	TotalCost            float64        `json:"total_cost" db:"total_cost"`
	InvoiceNumber        sql.NullString `json:"invoice_number" db:"invoice_number"`
	WaybillNumber        sql.NullString `json:"waybill_number" db:"waybill_number"`
	Notes                sql.NullString `json:"notes" db:"notes"`
	CreatedBy            uuid.NullUUID  `json:"created_by" db:"created_by"`
	CreatedDate          time.Time      `json:"created_date" db:"created_date"`

	// Related entities (not in DB)
	Items []CargoDistributionItem `json:"items,omitempty" db:"-"`
}

// CargoDistributionItem represents an item in a distribution
type CargoDistributionItem struct {
	ID              int64     `json:"id" db:"id"`
	DistributionID  int64     `json:"distribution_id" db:"distribution_id"`
	ShipmentItemID  int64     `json:"shipment_item_id" db:"shipment_item_id"`
	Quantity        float64   `json:"quantity" db:"quantity"`
	UnitCost        float64   `json:"unit_cost" db:"unit_cost"`
	TotalCost       float64   `json:"total_cost" db:"total_cost"`
	CreatedDate     time.Time `json:"created_date" db:"created_date"`
}

// =====================================================
// CARGO CASH ENTITIES
// =====================================================

// CargoCashTransaction represents a cash transaction
type CargoCashTransaction struct {
	ID               int64          `json:"id" db:"id"`
	TenantID         uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	TransactionType  string         `json:"transaction_type" db:"transaction_type"` // income, expense
	Amount           float64        `json:"amount" db:"amount"`
	Currency         string         `json:"currency" db:"currency"` // USD, UZS, EUR
	Category         string         `json:"category" db:"category"`  // transport, customs, insurance, payment_from_b2b, etc.
	ShipmentID       sql.NullInt64  `json:"shipment_id" db:"shipment_id"`
	DistributionID   sql.NullInt64  `json:"distribution_id" db:"distribution_id"`
	RelatedTenantID  uuid.NullUUID  `json:"related_tenant_id" db:"related_tenant_id"`
	Description      sql.NullString `json:"description" db:"description"`
	ReferenceNumber  sql.NullString `json:"reference_number" db:"reference_number"`
	TransactionDate  time.Time      `json:"transaction_date" db:"transaction_date"`
	CreatedBy        uuid.NullUUID  `json:"created_by" db:"created_by"`
	CreatedDate      time.Time      `json:"created_date" db:"created_date"`
}

// CargoCompanyAccount represents B2B/B2C company account balances
type CargoCompanyAccount struct {
	ID                  int64          `json:"id" db:"id"`
	CargoCompanyID      int64          `json:"cargo_company_id" db:"cargo_company_id"`
	RelatedCompanyID    int64          `json:"related_company_id" db:"related_company_id"`
	CompanyType         string         `json:"company_type" db:"company_type"` // B2B, B2C
	TotalDebt           float64        `json:"total_debt" db:"total_debt"`
	TotalCredit         float64        `json:"total_credit" db:"total_credit"`
	Balance             float64        `json:"balance" db:"balance"`
	Currency            string         `json:"currency" db:"currency"`
	LastTransactionDate sql.NullTime   `json:"last_transaction_date" db:"last_transaction_date"`
	CreatedDate         time.Time      `json:"created_date" db:"created_date"`
	UpdatedDate         time.Time      `json:"updated_date" db:"updated_date"`
}

// =====================================================
// REQUEST/RESPONSE TYPES
// =====================================================

// CreateCargoShipmentRequest represents request to create a shipment
type CreateCargoShipmentRequest struct {
	TrackingNumber  string                      `json:"tracking_number" binding:"required"`
	SupplierCountry string                      `json:"supplier_country"`
	SupplierCompany string                      `json:"supplier_company"`
	ExpectedDate    *time.Time                  `json:"expected_date"`
	TransportCost   float64                     `json:"transport_cost"`
	CustomsCost     float64                     `json:"customs_cost"`
	InsuranceCost   float64                     `json:"insurance_cost"`
	OtherCost       float64                     `json:"other_cost"`
	Notes           string                      `json:"notes"`
	Items           []CreateShipmentItemRequest `json:"items" binding:"required,min=1"`
}

// CreateShipmentItemRequest represents request to create a shipment item
type CreateShipmentItemRequest struct {
	ItemName    string  `json:"item_name" binding:"required"`
	Quantity    float64 `json:"quantity" binding:"required,gt=0"`
	UnitPrice   float64 `json:"unit_price" binding:"required,gt=0"`
	Currency    string  `json:"currency" binding:"required,oneof=USD UZS EUR"`
	HSCode      string  `json:"hs_code"`
	Description string  `json:"description"`
}

// UpdateShipmentStatusRequest represents request to update shipment status
type UpdateShipmentStatusRequest struct {
	Status   string `json:"status" binding:"required,oneof=ordered in_transit in_customs received distributed"`
	Note     string `json:"note"`
	Location string `json:"location"`
}

// CreateDistributionRequest represents request to create a distribution
type CreateDistributionRequest struct {
	RecipientTenantID    *uuid.UUID                      `json:"recipient_tenant_id"`
	RecipientCompanyName string                          `json:"recipient_company_name" binding:"required"`
	RecipientCompanyType string                          `json:"recipient_company_type" binding:"required,oneof=B2B B2C"`
	InvoiceNumber        string                          `json:"invoice_number"`
	WaybillNumber        string                          `json:"waybill_number"`
	Notes                string                          `json:"notes"`
	Items                []CreateDistributionItemRequest `json:"items" binding:"required,min=1"`
}

// CreateDistributionItemRequest represents request to create a distribution item
type CreateDistributionItemRequest struct {
	ShipmentItemID int64   `json:"shipment_item_id" binding:"required"`
	Quantity       float64 `json:"quantity" binding:"required,gt=0"`
	UnitCost       float64 `json:"unit_cost" binding:"required,gt=0"`
}

// CreateCashTransactionRequest represents request to create a cash transaction
type CreateCashTransactionRequest struct {
	TransactionType string     `json:"transaction_type" binding:"required,oneof=income expense"`
	Amount          float64    `json:"amount" binding:"required,gt=0"`
	Currency        string     `json:"currency" binding:"required,oneof=USD UZS EUR"`
	Category        string     `json:"category" binding:"required"`
	ShipmentID      *int64     `json:"shipment_id"`
	DistributionID  *int64     `json:"distribution_id"`
	RelatedTenantID *uuid.UUID `json:"related_tenant_id"`
	Description     string     `json:"description"`
	ReferenceNumber string     `json:"reference_number"`
	TransactionDate *time.Time `json:"transaction_date"`
}

// CargoCashSummary represents cash register summary
type CargoCashSummary struct {
	UZSBalance   float64                `json:"uzs_balance"`
	USDBalance   float64                `json:"usd_balance"`
	Transactions []CargoCashTransaction `json:"transactions"`
}
