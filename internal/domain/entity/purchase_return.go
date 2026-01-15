package entity

import (
	"time"

	"github.com/google/uuid"
)

// Purchase Return Status
type ReturnStatus string

const (
	ReturnStatusDraft           ReturnStatus = "draft"
	ReturnStatusPendingApproval ReturnStatus = "pending_approval"
	ReturnStatusApproved        ReturnStatus = "approved"
	ReturnStatusShipped         ReturnStatus = "shipped"
	ReturnStatusReceived        ReturnStatus = "received" // Received by supplier
	ReturnStatusCredited        ReturnStatus = "credited" // Credit note received
	ReturnStatusRejected        ReturnStatus = "rejected"
	ReturnStatusCancelled       ReturnStatus = "cancelled"
)

// Return Reason
type ReturnReason string

const (
	ReturnReasonDefective      ReturnReason = "defective"
	ReturnReasonWrongItem      ReturnReason = "wrong_item"
	ReturnReasonDamaged        ReturnReason = "damaged"
	ReturnReasonQualityIssue   ReturnReason = "quality_issue"
	ReturnReasonOverSupply     ReturnReason = "over_supply"
	ReturnReasonExpired        ReturnReason = "expired"
	ReturnReasonNotAsOrdered   ReturnReason = "not_as_ordered"
	ReturnReasonOther          ReturnReason = "other"
)

// PurchaseReturn represents a purchase return/RMA document
type PurchaseReturn struct {
	ID                uuid.UUID    `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TenantID          uuid.UUID    `json:"tenant_id" gorm:"type:uuid;not null;index"`
	ReturnNumber      string       `json:"return_number" gorm:"type:varchar(50);not null"`
	PurchaseOrderID   *uuid.UUID   `json:"purchase_order_id" gorm:"type:uuid;index"`
	PONumber          string       `json:"po_number" gorm:"type:varchar(50)"`
	GoodsReceiptID    *uuid.UUID   `json:"goods_receipt_id" gorm:"type:uuid;index"`
	GRNumber          string       `json:"gr_number" gorm:"type:varchar(50)"`
	SupplierID        uuid.UUID    `json:"supplier_id" gorm:"type:uuid;not null;index"`
	SupplierName      string       `json:"supplier_name" gorm:"type:varchar(255)"`
	ReturnDate        time.Time    `json:"return_date" gorm:"type:date;not null"`
	RequestedBy       string       `json:"requested_by" gorm:"type:varchar(255);not null"`
	RequestedByID     *uuid.UUID   `json:"requested_by_id" gorm:"type:uuid"`
	Status            ReturnStatus `json:"status" gorm:"type:varchar(30);default:'draft'"`
	ReturnReason      ReturnReason `json:"return_reason" gorm:"type:varchar(50);not null"`
	ReasonDescription string       `json:"reason_description" gorm:"type:text"`
	TotalQuantity     float64      `json:"total_quantity" gorm:"type:decimal(15,3);default:0"`
	TotalValue        float64      `json:"total_value" gorm:"type:decimal(15,2);default:0"`
	ShippingMethod    string       `json:"shipping_method" gorm:"type:varchar(100)"`
	TrackingNumber    string       `json:"tracking_number" gorm:"type:varchar(100)"`
	ShippedDate       *time.Time   `json:"shipped_date" gorm:"type:date"`
	ReceivedDate      *time.Time   `json:"received_date" gorm:"type:date"`
	CreditNoteNumber  string       `json:"credit_note_number" gorm:"type:varchar(50)"`
	CreditNoteDate    *time.Time   `json:"credit_note_date" gorm:"type:date"`
	CreditAmount      float64      `json:"credit_amount" gorm:"type:decimal(15,2);default:0"`
	ApprovedBy        string       `json:"approved_by" gorm:"type:varchar(255)"`
	ApprovedByID      *uuid.UUID   `json:"approved_by_id" gorm:"type:uuid"`
	ApprovedAt        *time.Time   `json:"approved_at"`
	RejectionReason   string       `json:"rejection_reason" gorm:"type:text"`
	Notes             string       `json:"notes" gorm:"type:text"`
	CreatedAt         time.Time    `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt         time.Time    `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt         *time.Time   `json:"deleted_at" gorm:"index"`

	// Relations
	Lines []PurchaseReturnLine `json:"lines,omitempty" gorm:"foreignKey:ReturnID"`
}

func (PurchaseReturn) TableName() string {
	return "purchase_returns"
}

// PurchaseReturnLine represents a line item in purchase return
type PurchaseReturnLine struct {
	ID              uuid.UUID    `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	ReturnID        uuid.UUID    `json:"return_id" gorm:"type:uuid;not null;index"`
	POLineID        *uuid.UUID   `json:"po_line_id" gorm:"type:uuid"`
	GRLineID        *uuid.UUID   `json:"gr_line_id" gorm:"type:uuid"`
	ProductID       *uuid.UUID   `json:"product_id" gorm:"type:uuid"`
	ProductName     string       `json:"product_name" gorm:"type:varchar(255);not null"`
	ProductCode     string       `json:"product_code" gorm:"type:varchar(100)"`
	ReturnQuantity  float64      `json:"return_quantity" gorm:"type:decimal(15,3);not null"`
	Unit            string       `json:"unit" gorm:"type:varchar(20);default:'pcs'"`
	UnitPrice       float64      `json:"unit_price" gorm:"type:decimal(15,2)"`
	TotalPrice      float64      `json:"total_price" gorm:"type:decimal(15,2)"`
	ReturnReason    ReturnReason `json:"return_reason" gorm:"type:varchar(50)"`
	ReasonDetails   string       `json:"reason_details" gorm:"type:text"`
	BatchNumber     string       `json:"batch_number" gorm:"type:varchar(100)"`
	SerialNumbers   string       `json:"serial_numbers" gorm:"type:text"`
	Condition       string       `json:"condition" gorm:"type:varchar(50)"` // new, used, damaged
	Photos          string       `json:"photos" gorm:"type:text"`           // JSON array of photo URLs
	ReceivedBack    bool         `json:"received_back" gorm:"default:false"`
	CreditApplied   bool         `json:"credit_applied" gorm:"default:false"`
	Notes           string       `json:"notes" gorm:"type:text"`
	CreatedAt       time.Time    `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time    `json:"updated_at" gorm:"autoUpdateTime"`
}

func (PurchaseReturnLine) TableName() string {
	return "purchase_return_lines"
}

// Input/Output structs
type CreateReturnInput struct {
	PurchaseOrderID   string                   `json:"purchase_order_id"`
	GoodsReceiptID    string                   `json:"goods_receipt_id"`
	SupplierID        string                   `json:"supplier_id" binding:"required"`
	ReturnDate        string                   `json:"return_date"`
	RequestedBy       string                   `json:"requested_by" binding:"required"`
	ReturnReason      ReturnReason             `json:"return_reason" binding:"required"`
	ReasonDescription string                   `json:"reason_description"`
	ShippingMethod    string                   `json:"shipping_method"`
	Notes             string                   `json:"notes"`
	Lines             []CreateReturnLineInput  `json:"lines" binding:"required,min=1"`
}

type CreateReturnLineInput struct {
	POLineID       string       `json:"po_line_id"`
	GRLineID       string       `json:"gr_line_id"`
	ProductID      string       `json:"product_id"`
	ProductName    string       `json:"product_name" binding:"required"`
	ProductCode    string       `json:"product_code"`
	ReturnQuantity float64      `json:"return_quantity" binding:"required,gt=0"`
	Unit           string       `json:"unit"`
	UnitPrice      float64      `json:"unit_price"`
	ReturnReason   ReturnReason `json:"return_reason"`
	ReasonDetails  string       `json:"reason_details"`
	BatchNumber    string       `json:"batch_number"`
	SerialNumbers  string       `json:"serial_numbers"`
	Condition      string       `json:"condition"`
	Notes          string       `json:"notes"`
}

type ApproveReturnInput struct {
	ApprovedBy string `json:"approved_by" binding:"required"`
}

type RejectReturnInput struct {
	RejectedBy string `json:"rejected_by" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
}

type ShipReturnInput struct {
	ShippingMethod string `json:"shipping_method"`
	TrackingNumber string `json:"tracking_number"`
	ShippedDate    string `json:"shipped_date"`
}

type ReceiveReturnInput struct {
	ReceivedDate string `json:"received_date"`
}

type ApplyCreditInput struct {
	CreditNoteNumber string  `json:"credit_note_number" binding:"required"`
	CreditNoteDate   string  `json:"credit_note_date"`
	CreditAmount     float64 `json:"credit_amount" binding:"required,gt=0"`
}
