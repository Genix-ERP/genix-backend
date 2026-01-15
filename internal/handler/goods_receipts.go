package handler

import (
	"fmt"
	"net/http"
	"time"

	"genix-backend/internal/domain/entity"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListGoodsReceipts returns all goods receipts for the tenant
func (h *Handler) ListGoodsReceipts(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var receipts []entity.GoodsReceipt
	query := h.db.Where("tenant_id = ? AND deleted_at IS NULL", tenantID).
		Order("created_at DESC")

	// Filter by status
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}

	// Filter by PO
	if poID := c.Query("purchase_order_id"); poID != "" {
		query = query.Where("purchase_order_id = ?", poID)
	}

	// Filter by supplier
	if supplierID := c.Query("supplier_id"); supplierID != "" {
		query = query.Where("supplier_id = ?", supplierID)
	}

	if err := query.Preload("Lines").Find(&receipts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch goods receipts"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": receipts})
}

// CreateGoodsReceipt creates a new goods receipt
func (h *Handler) CreateGoodsReceipt(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var input entity.CreateGRInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	poID, err := uuid.Parse(input.PurchaseOrderID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase order ID"})
		return
	}

	// Get PO info
	var po entity.PurchaseOrder
	if err := h.db.Where("id = ? AND tenant_id = ?", poID, tenantID).First(&po).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase order not found"})
		return
	}

	// Generate GR number
	var count int64
	h.db.Model(&entity.GoodsReceipt{}).Where("tenant_id = ?", tenantID).Count(&count)
	grNumber := fmt.Sprintf("GR-%s-%04d", time.Now().Format("200601"), count+1)

	// Parse receipt date
	receiptDate := time.Now()
	if input.ReceiptDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ReceiptDate); err == nil {
			receiptDate = parsed
		}
	}

	var warehouseID *uuid.UUID
	if input.WarehouseID != "" {
		if wid, err := uuid.Parse(input.WarehouseID); err == nil {
			warehouseID = &wid
		}
	}

	tx := h.db.Begin()

	receipt := entity.GoodsReceipt{
		TenantID:        tenantID,
		GRNumber:        grNumber,
		PurchaseOrderID: poID,
		PONumber:        po.PONumber,
		SupplierID:      po.SupplierID,
		SupplierName:    po.SupplierName,
		ReceiptDate:     receiptDate,
		ReceivedBy:      input.ReceivedBy,
		WarehouseID:     warehouseID,
		WarehouseName:   input.WarehouseName,
		Status:          entity.GRStatusDraft,
		QualityStatus:   entity.QualityStatusPending,
		DeliveryNote:    input.DeliveryNote,
		VehicleNumber:   input.VehicleNumber,
		DriverName:      input.DriverName,
		Notes:           input.Notes,
	}

	if err := tx.Create(&receipt).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create goods receipt"})
		return
	}

	// Create lines
	var totalQty float64
	for _, lineInput := range input.Lines {
		var productID *uuid.UUID
		if lineInput.ProductID != "" {
			if pid, err := uuid.Parse(lineInput.ProductID); err == nil {
				productID = &pid
			}
		}

		var poLineID *uuid.UUID
		if lineInput.POLineID != "" {
			if lid, err := uuid.Parse(lineInput.POLineID); err == nil {
				poLineID = &lid
			}
		}

		var expiryDate *time.Time
		if lineInput.ExpiryDate != "" {
			if parsed, err := time.Parse("2006-01-02", lineInput.ExpiryDate); err == nil {
				expiryDate = &parsed
			}
		}

		unit := lineInput.Unit
		if unit == "" {
			unit = "pcs"
		}

		line := entity.GoodsReceiptLine{
			GoodsReceiptID:   receipt.ID,
			POLineID:         poLineID,
			ProductID:        productID,
			ProductName:      lineInput.ProductName,
			ProductCode:      lineInput.ProductCode,
			OrderedQuantity:  lineInput.OrderedQuantity,
			ReceivedQuantity: lineInput.ReceivedQuantity,
			Unit:             unit,
			UnitPrice:        lineInput.UnitPrice,
			QualityStatus:    entity.QualityStatusPending,
			BatchNumber:      lineInput.BatchNumber,
			ExpiryDate:       expiryDate,
			SerialNumbers:    lineInput.SerialNumbers,
			StorageLocation:  lineInput.StorageLocation,
			Notes:            lineInput.Notes,
		}

		if err := tx.Create(&line).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create receipt line"})
			return
		}

		totalQty += lineInput.ReceivedQuantity
	}

	// Update total quantity
	if err := tx.Model(&receipt).Update("total_quantity", totalQty).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update total quantity"})
		return
	}

	tx.Commit()

	h.db.Preload("Lines").First(&receipt, receipt.ID)
	c.JSON(http.StatusCreated, gin.H{"data": receipt})
}

// GetGoodsReceipt returns a single goods receipt
func (h *Handler) GetGoodsReceipt(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	grID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goods receipt ID"})
		return
	}

	var receipt entity.GoodsReceipt
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", grID, tenantID).
		Preload("Lines").First(&receipt).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Goods receipt not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": receipt})
}

// DeleteGoodsReceipt soft deletes a goods receipt
func (h *Handler) DeleteGoodsReceipt(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	grID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goods receipt ID"})
		return
	}

	var receipt entity.GoodsReceipt
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", grID, tenantID).
		First(&receipt).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Goods receipt not found"})
		return
	}

	// Only draft can be deleted
	if receipt.Status != entity.GRStatusDraft {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only draft goods receipts can be deleted"})
		return
	}

	now := time.Now()
	h.db.Model(&receipt).Update("deleted_at", now)

	c.JSON(http.StatusOK, gin.H{"message": "Goods receipt deleted"})
}

// InspectGoodsReceipt performs quality inspection on a goods receipt
func (h *Handler) InspectGoodsReceipt(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	grID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goods receipt ID"})
		return
	}

	var receipt entity.GoodsReceipt
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", grID, tenantID).
		Preload("Lines").First(&receipt).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Goods receipt not found"})
		return
	}

	if receipt.Status == entity.GRStatusCompleted || receipt.Status == entity.GRStatusCancelled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot inspect completed or cancelled goods receipt"})
		return
	}

	var input entity.InspectGRInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx := h.db.Begin()

	now := time.Now()
	var totalAccepted, totalRejected float64
	var allPassed, allFailed bool = true, true

	// Update line inspections
	for _, lineInspection := range input.Lines {
		lineID, err := uuid.Parse(lineInspection.LineID)
		if err != nil {
			continue
		}

		qualityStatus := entity.QualityStatusPassed
		if lineInspection.RejectedQuantity > 0 {
			if lineInspection.AcceptedQuantity > 0 {
				qualityStatus = entity.QualityStatusPartial
				allPassed = false
			} else {
				qualityStatus = entity.QualityStatusFailed
				allPassed = false
			}
		}
		if lineInspection.AcceptedQuantity > 0 {
			allFailed = false
		}

		updates := map[string]interface{}{
			"accepted_quantity": lineInspection.AcceptedQuantity,
			"rejected_quantity": lineInspection.RejectedQuantity,
			"quality_status":    qualityStatus,
			"rejection_reason":  lineInspection.RejectionReason,
		}

		if err := tx.Model(&entity.GoodsReceiptLine{}).Where("id = ?", lineID).Updates(updates).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update line inspection"})
			return
		}

		totalAccepted += lineInspection.AcceptedQuantity
		totalRejected += lineInspection.RejectedQuantity
	}

	// Determine overall quality status
	overallQuality := entity.QualityStatusPartial
	if allPassed {
		overallQuality = entity.QualityStatusPassed
	} else if allFailed {
		overallQuality = entity.QualityStatusFailed
	}

	// Update receipt
	receiptUpdates := map[string]interface{}{
		"status":            entity.GRStatusPending,
		"quality_status":    overallQuality,
		"inspected_by":      input.InspectedBy,
		"inspected_at":      now,
		"inspection_notes":  input.InspectionNotes,
		"accepted_quantity": totalAccepted,
		"rejected_quantity": totalRejected,
	}

	if err := tx.Model(&receipt).Updates(receiptUpdates).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update receipt inspection"})
		return
	}

	tx.Commit()

	h.db.Preload("Lines").First(&receipt, grID)
	c.JSON(http.StatusOK, gin.H{"data": receipt})
}

// CompleteGoodsReceipt completes a goods receipt and updates PO
func (h *Handler) CompleteGoodsReceipt(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	grID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goods receipt ID"})
		return
	}

	var receipt entity.GoodsReceipt
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", grID, tenantID).
		Preload("Lines").First(&receipt).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Goods receipt not found"})
		return
	}

	if receipt.Status == entity.GRStatusCompleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Goods receipt already completed"})
		return
	}

	if receipt.QualityStatus == entity.QualityStatusPending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Quality inspection required before completion"})
		return
	}

	tx := h.db.Begin()

	// Update receipt status
	if err := tx.Model(&receipt).Update("status", entity.GRStatusCompleted).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to complete goods receipt"})
		return
	}

	// Update PO received quantities
	var po entity.PurchaseOrder
	if err := tx.Where("id = ?", receipt.PurchaseOrderID).Preload("Lines").First(&po).Error; err == nil {
		// Calculate total received for PO
		var totalPOReceived float64
		for _, grLine := range receipt.Lines {
			if grLine.POLineID != nil {
				// Update PO line received quantity
				var poLine entity.PurchaseOrderLine
				if err := tx.Where("id = ?", grLine.POLineID).First(&poLine).Error; err == nil {
					newReceived := poLine.QuantityReceived + grLine.AcceptedQuantity
					tx.Model(&poLine).Update("quantity_received", newReceived)
				}
			}
			totalPOReceived += grLine.AcceptedQuantity
		}

		// Check if PO is fully received
		var allReceived bool = true
		var poLines []entity.PurchaseOrderLine
		tx.Where("purchase_order_id = ?", po.ID).Find(&poLines)
		for _, line := range poLines {
			if line.QuantityReceived < line.Quantity {
				allReceived = false
				break
			}
		}

		// Update PO status
		newStatus := entity.POStatusPartial
		if allReceived {
			newStatus = entity.POStatusReceived
		}
		tx.Model(&po).Update("status", newStatus)
	}

	tx.Commit()

	h.db.Preload("Lines").First(&receipt, grID)
	c.JSON(http.StatusOK, gin.H{"data": receipt})
}

// CancelGoodsReceipt cancels a goods receipt
func (h *Handler) CancelGoodsReceipt(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	grID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goods receipt ID"})
		return
	}

	var receipt entity.GoodsReceipt
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", grID, tenantID).
		First(&receipt).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Goods receipt not found"})
		return
	}

	if receipt.Status == entity.GRStatusCompleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot cancel completed goods receipt"})
		return
	}

	h.db.Model(&receipt).Update("status", entity.GRStatusCancelled)

	h.db.Preload("Lines").First(&receipt, grID)
	c.JSON(http.StatusOK, gin.H{"data": receipt})
}
