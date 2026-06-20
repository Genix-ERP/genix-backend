package entity

import (
	"time"

	"github.com/google/uuid"
)

// Goods Receipt Status
type GRStatus string

const (
	GRStatusDraft     GRStatus = "draft"
	GRStatusPending   GRStatus = "pending"   // Pending quality inspection
	GRStatusCompleted GRStatus = "completed"
	GRStatusCancelled GRStatus = "cancelled"
)

// Quality Status
type QualityStatus string

const (
	QualityStatusPending  QualityStatus = "pending"
	QualityStatusPassed   QualityStatus = "passed"
	QualityStatusFailed   QualityStatus = "failed"
	QualityStatusPartial  QualityStatus = "partial"
)

// GoodsReceipt represents a goods receipt document
type GoodsReceipt struct {
	ID                uuid.UUID     `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TenantID          uuid.UUID     `json:"tenant_id" gorm:"type:uuid;not null;index"`
	GRNumber          string        `json:"gr_number" gorm:"type:varchar(50);not null"`
	PurchaseOrderID   uuid.UUID     `json:"purchase_order_id" gorm:"type:uuid;not null;index"`
	PONumber          string        `json:"po_number" gorm:"type:varchar(50)"`
	SupplierID        uuid.UUID     `json:"supplier_id" gorm:"type:uuid;not null"`
	SupplierName      string        `json:"supplier_name" gorm:"type:varchar(255)"`
	ReceiptDate       time.Time     `json:"receipt_date" gorm:"type:date;not null"`
	ReceivedBy        string        `json:"received_by" gorm:"type:varchar(255);not null"`
	ReceivedByID      *uuid.UUID    `json:"received_by_id" gorm:"type:uuid"`
	WarehouseID       *uuid.UUID    `json:"warehouse_id" gorm:"type:uuid"`
	WarehouseName     string        `json:"warehouse_name" gorm:"type:varchar(255)"`
	Status            GRStatus      `json:"status" gorm:"type:varchar(30);default:'draft'"`
	QualityStatus     QualityStatus `json:"quality_status" gorm:"type:varchar(30);default:'pending'"`
	DeliveryNote      string        `json:"delivery_note" gorm:"type:varchar(100)"`
	VehicleNumber     string        `json:"vehicle_number" gorm:"type:varchar(50)"`
	DriverName        string        `json:"driver_name" gorm:"type:varchar(255)"`
	Notes             string        `json:"notes" gorm:"type:text"`
	InspectionNotes   string        `json:"inspection_notes" gorm:"type:text"`
	InspectedBy       string        `json:"inspected_by" gorm:"type:varchar(255)"`
	InspectedAt       *time.Time    `json:"inspected_at"`
	TotalQuantity     float64       `json:"total_quantity" gorm:"type:decimal(15,3);default:0"`
	AcceptedQuantity  float64       `json:"accepted_quantity" gorm:"type:decimal(15,3);default:0"`
	RejectedQuantity  float64       `json:"rejected_quantity" gorm:"type:decimal(15,3);default:0"`
	CreatedAt         time.Time     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt         time.Time     `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt         *time.Time    `json:"deleted_at" gorm:"index"`

	// Relations
	Lines []GoodsReceiptLine `json:"lines,omitempty" gorm:"foreignKey:GoodsReceiptID"`
}

// GoodsReceiptLine represents a line item in goods receipt
type GoodsReceiptLine struct {
	ID               uuid.UUID     `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	GoodsReceiptID   uuid.UUID     `json:"goods_receipt_id" gorm:"type:uuid;not null;index"`
	POLineID         *uuid.UUID    `json:"po_line_id" gorm:"type:uuid"`
	ProductID        *uuid.UUID    `json:"product_id" gorm:"type:uuid"`
	ProductName      string        `json:"product_name" gorm:"type:varchar(255);not null"`
	ProductCode      string        `json:"product_code" gorm:"type:varchar(100)"`
	OrderedQuantity  float64       `json:"ordered_quantity" gorm:"type:decimal(15,3);not null"`
	ReceivedQuantity float64       `json:"received_quantity" gorm:"type:decimal(15,3);not null"`
	AcceptedQuantity float64       `json:"accepted_quantity" gorm:"type:decimal(15,3);default:0"`
	RejectedQuantity float64       `json:"rejected_quantity" gorm:"type:decimal(15,3);default:0"`
	Unit             string        `json:"unit" gorm:"type:varchar(20);default:'pcs'"`
	UnitPrice        float64       `json:"unit_price" gorm:"type:decimal(15,2)"`
	QualityStatus    QualityStatus `json:"quality_status" gorm:"type:varchar(30);default:'pending'"`
	RejectionReason  string        `json:"rejection_reason" gorm:"type:text"`
	BatchNumber      string        `json:"batch_number" gorm:"type:varchar(100)"`
	ExpiryDate       *time.Time    `json:"expiry_date" gorm:"type:date"`
	SerialNumbers    string        `json:"serial_numbers" gorm:"type:text"`
	StorageLocation  string        `json:"storage_location" gorm:"type:varchar(100)"`
	Notes            string        `json:"notes" gorm:"type:text"`
	CreatedAt        time.Time     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time     `json:"updated_at" gorm:"autoUpdateTime"`
}

// Input/Output structs
type CreateGRInput struct {
	PurchaseOrderID string             `json:"purchase_order_id" binding:"required"`
	ReceiptDate     string             `json:"receipt_date"`
	ReceivedBy      string             `json:"received_by" binding:"required"`
	WarehouseID     string             `json:"warehouse_id"`
	WarehouseName   string             `json:"warehouse_name"`
	DeliveryNote    string             `json:"delivery_note"`
	VehicleNumber   string             `json:"vehicle_number"`
	DriverName      string             `json:"driver_name"`
	Notes           string             `json:"notes"`
	Lines           []CreateGRLineInput `json:"lines" binding:"required,min=1"`
}

type CreateGRLineInput struct {
	POLineID         string  `json:"po_line_id"`
	ProductID        string  `json:"product_id"`
	ProductName      string  `json:"product_name" binding:"required"`
	ProductCode      string  `json:"product_code"`
	OrderedQuantity  float64 `json:"ordered_quantity"`
	ReceivedQuantity float64 `json:"received_quantity" binding:"required,gt=0"`
	Unit             string  `json:"unit"`
	UnitPrice        float64 `json:"unit_price"`
	BatchNumber      string  `json:"batch_number"`
	ExpiryDate       string  `json:"expiry_date"`
	SerialNumbers    string  `json:"serial_numbers"`
	StorageLocation  string  `json:"storage_location"`
	Notes            string  `json:"notes"`
}

type InspectGRInput struct {
	InspectedBy     string                 `json:"inspected_by" binding:"required"`
	InspectionNotes string                 `json:"inspection_notes"`
	Lines           []InspectGRLineInput   `json:"lines" binding:"required,min=1"`
}

type InspectGRLineInput struct {
	LineID           string  `json:"line_id" binding:"required"`
	AcceptedQuantity float64 `json:"accepted_quantity" binding:"required,gte=0"`
	RejectedQuantity float64 `json:"rejected_quantity" binding:"gte=0"`
	RejectionReason  string  `json:"rejection_reason"`
}

type CompleteGRInput struct {
	CompletedBy string `json:"completed_by" binding:"required"`
}
