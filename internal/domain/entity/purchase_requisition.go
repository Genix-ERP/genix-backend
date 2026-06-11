package entity

import (
	"time"

	"github.com/google/uuid"
)

// Purchase Requisition Status
type PRStatus string

const (
	PRStatusDraft           PRStatus = "draft"
	PRStatusPendingApproval PRStatus = "pending_approval"
	PRStatusApproved        PRStatus = "approved"
	PRStatusRejected        PRStatus = "rejected"
	PRStatusConverted       PRStatus = "converted" // Converted to PO
	PRStatusCancelled       PRStatus = "cancelled"
)

// Purchase Requisition Priority
type PRPriority string

const (
	PRPriorityLow    PRPriority = "low"
	PRPriorityMedium PRPriority = "medium"
	PRPriorityHigh   PRPriority = "high"
	PRPriorityUrgent PRPriority = "urgent"
)

// PurchaseRequisition represents an internal purchase request
type PurchaseRequisition struct {
	ID              uuid.UUID  `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TenantID        uuid.UUID  `json:"tenant_id" gorm:"type:uuid;not null;index"`
	PRNumber        string     `json:"pr_number" gorm:"type:varchar(50);not null"`
	RequestedBy     string     `json:"requested_by" gorm:"type:varchar(255);not null"`
	RequestedByID   *uuid.UUID `json:"requested_by_id" gorm:"type:uuid"`
	Department      string     `json:"department" gorm:"type:varchar(100)"`
	RequestDate     time.Time  `json:"request_date" gorm:"type:date;not null"`
	RequiredDate    *time.Time `json:"required_date" gorm:"type:date"`
	Status          PRStatus   `json:"status" gorm:"type:varchar(30);default:'draft'"`
	Priority        PRPriority `json:"priority" gorm:"type:varchar(20);default:'medium'"`
	Purpose         string     `json:"purpose" gorm:"type:text"`
	Notes           string     `json:"notes" gorm:"type:text"`
	TotalAmount     float64    `json:"total_amount" gorm:"type:decimal(15,2);default:0"`
	ApprovedBy      string     `json:"approved_by" gorm:"type:varchar(255)"`
	ApprovedByID    *uuid.UUID `json:"approved_by_id" gorm:"type:uuid"`
	ApprovedAt      *time.Time `json:"approved_at"`
	RejectionReason string     `json:"rejection_reason" gorm:"type:text"`
	ConvertedToPO   *uuid.UUID `json:"converted_to_po" gorm:"type:uuid"`
	CreatedAt       time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt       *time.Time `json:"deleted_at" gorm:"index"`

	// Relations
	Lines []PurchaseRequisitionLine `json:"lines,omitempty" gorm:"foreignKey:RequisitionID"`
}

// PurchaseRequisitionLine represents a line item in a purchase requisition
type PurchaseRequisitionLine struct {
	ID               uuid.UUID  `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	RequisitionID    uuid.UUID  `json:"requisition_id" gorm:"type:uuid;not null;index"`
	ProductID        *uuid.UUID `json:"product_id" gorm:"type:uuid"`
	ProductName      string     `json:"product_name" gorm:"type:varchar(255);not null"`
	ProductCode      string     `json:"product_code" gorm:"type:varchar(100)"`
	Description      string     `json:"description" gorm:"type:text"`
	Quantity         float64    `json:"quantity" gorm:"type:decimal(15,3);not null"`
	Unit             string     `json:"unit" gorm:"type:varchar(20);default:'pcs'"`
	EstimatedPrice   float64    `json:"estimated_price" gorm:"type:decimal(15,2)"`
	TotalPrice       float64    `json:"total_price" gorm:"type:decimal(15,2)"`
	PreferredVendor  *uuid.UUID `json:"preferred_vendor" gorm:"type:uuid"`
	VendorName       string     `json:"vendor_name" gorm:"type:varchar(255)"`
	Specifications   string     `json:"specifications" gorm:"type:text"`
	ApprovedQuantity float64    `json:"approved_quantity" gorm:"type:decimal(15,3)"`
	CreatedAt        time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

// Input/Output structs
type CreatePRInput struct {
	RequestedBy  string                `json:"requested_by" binding:"required"`
	Department   string                `json:"department"`
	RequestDate  string                `json:"request_date"`
	RequiredDate string                `json:"required_date"`
	Priority     PRPriority            `json:"priority"`
	Purpose      string                `json:"purpose"`
	Notes        string                `json:"notes"`
	Lines        []CreatePRLineInput   `json:"lines"`
}

type CreatePRLineInput struct {
	ProductID       string  `json:"product_id"`
	ProductName     string  `json:"product_name" binding:"required"`
	ProductCode     string  `json:"product_code"`
	Description     string  `json:"description"`
	Quantity        float64 `json:"quantity" binding:"required,gt=0"`
	Unit            string  `json:"unit"`
	EstimatedPrice  float64 `json:"estimated_price"`
	PreferredVendor string  `json:"preferred_vendor"`
	VendorName      string  `json:"vendor_name"`
	Specifications  string  `json:"specifications"`
}

type UpdatePRInput struct {
	RequestedBy  string              `json:"requested_by"`
	Department   string              `json:"department"`
	RequiredDate string              `json:"required_date"`
	Priority     PRPriority          `json:"priority"`
	Purpose      string              `json:"purpose"`
	Notes        string              `json:"notes"`
	Lines        []CreatePRLineInput `json:"lines"`
}

type ApprovePRInput struct {
	ApprovedBy string                  `json:"approved_by" binding:"required"`
	Lines      []ApprovePRLineInput    `json:"lines"`
}

type ApprovePRLineInput struct {
	LineID           string  `json:"line_id" binding:"required"`
	ApprovedQuantity float64 `json:"approved_quantity" binding:"required,gte=0"`
}

type RejectPRInput struct {
	RejectedBy string `json:"rejected_by" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
}

type ConvertPRToPOInput struct {
	SupplierID   string `json:"supplier_id" binding:"required"`
	PaymentTerms string `json:"payment_terms"`
}
