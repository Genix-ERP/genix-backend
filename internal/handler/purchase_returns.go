package handler

import (
	"fmt"
	"net/http"
	"time"

	"genix-backend/internal/domain/entity"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListPurchaseReturns returns all purchase returns for the tenant
func (h *Handler) ListPurchaseReturns(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var returns []entity.PurchaseReturn
	query := h.db.Where("tenant_id = ? AND deleted_at IS NULL", tenantID).
		Order("created_at DESC")

	// Filter by status
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}

	// Filter by supplier
	if supplierID := c.Query("supplier_id"); supplierID != "" {
		query = query.Where("supplier_id = ?", supplierID)
	}

	// Filter by reason
	if reason := c.Query("return_reason"); reason != "" {
		query = query.Where("return_reason = ?", reason)
	}

	if err := query.Preload("Lines").Find(&returns).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch purchase returns"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": returns})
}

// CreatePurchaseReturn creates a new purchase return
func (h *Handler) CreatePurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var input entity.CreateReturnInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	supplierID, err := uuid.Parse(input.SupplierID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid supplier ID"})
		return
	}

	// Get supplier info
	var supplier entity.Contact
	if err := h.db.Where("id = ? AND tenant_id = ?", supplierID, tenantID).First(&supplier).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Supplier not found"})
		return
	}

	// Generate return number
	var count int64
	h.db.Model(&entity.PurchaseReturn{}).Where("tenant_id = ?", tenantID).Count(&count)
	returnNumber := fmt.Sprintf("RET-%s-%04d", time.Now().Format("200601"), count+1)

	// Parse return date
	returnDate := time.Now()
	if input.ReturnDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ReturnDate); err == nil {
			returnDate = parsed
		}
	}

	var poID *uuid.UUID
	var poNumber string
	if input.PurchaseOrderID != "" {
		if pid, err := uuid.Parse(input.PurchaseOrderID); err == nil {
			poID = &pid
			var po entity.PurchaseOrder
			if err := h.db.Where("id = ?", pid).First(&po).Error; err == nil {
				poNumber = po.PONumber
			}
		}
	}

	var grID *uuid.UUID
	var grNumber string
	if input.GoodsReceiptID != "" {
		if gid, err := uuid.Parse(input.GoodsReceiptID); err == nil {
			grID = &gid
			var gr entity.GoodsReceipt
			if err := h.db.Where("id = ?", gid).First(&gr).Error; err == nil {
				grNumber = gr.GRNumber
			}
		}
	}

	tx := h.db.Begin()

	purchaseReturn := entity.PurchaseReturn{
		TenantID:          tenantID,
		ReturnNumber:      returnNumber,
		PurchaseOrderID:   poID,
		PONumber:          poNumber,
		GoodsReceiptID:    grID,
		GRNumber:          grNumber,
		SupplierID:        supplierID,
		SupplierName:      supplier.Name,
		ReturnDate:        returnDate,
		RequestedBy:       input.RequestedBy,
		Status:            entity.ReturnStatusDraft,
		ReturnReason:      input.ReturnReason,
		ReasonDescription: input.ReasonDescription,
		ShippingMethod:    input.ShippingMethod,
		Notes:             input.Notes,
	}

	if err := tx.Create(&purchaseReturn).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create purchase return"})
		return
	}

	// Create lines
	var totalQty, totalValue float64
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

		var grLineID *uuid.UUID
		if lineInput.GRLineID != "" {
			if lid, err := uuid.Parse(lineInput.GRLineID); err == nil {
				grLineID = &lid
			}
		}

		unit := lineInput.Unit
		if unit == "" {
			unit = "pcs"
		}

		totalPrice := lineInput.ReturnQuantity * lineInput.UnitPrice
		returnReason := lineInput.ReturnReason
		if returnReason == "" {
			returnReason = input.ReturnReason
		}

		line := entity.PurchaseReturnLine{
			ReturnID:       purchaseReturn.ID,
			POLineID:       poLineID,
			GRLineID:       grLineID,
			ProductID:      productID,
			ProductName:    lineInput.ProductName,
			ProductCode:    lineInput.ProductCode,
			ReturnQuantity: lineInput.ReturnQuantity,
			Unit:           unit,
			UnitPrice:      lineInput.UnitPrice,
			TotalPrice:     totalPrice,
			ReturnReason:   returnReason,
			ReasonDetails:  lineInput.ReasonDetails,
			BatchNumber:    lineInput.BatchNumber,
			SerialNumbers:  lineInput.SerialNumbers,
			Condition:      lineInput.Condition,
			Notes:          lineInput.Notes,
		}

		if err := tx.Create(&line).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create return line"})
			return
		}

		totalQty += lineInput.ReturnQuantity
		totalValue += totalPrice
	}

	// Update totals
	if err := tx.Model(&purchaseReturn).Updates(map[string]interface{}{
		"total_quantity": totalQty,
		"total_value":    totalValue,
	}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update totals"})
		return
	}

	tx.Commit()

	h.db.Preload("Lines").First(&purchaseReturn, purchaseReturn.ID)
	c.JSON(http.StatusCreated, gin.H{"data": purchaseReturn})
}

// GetPurchaseReturn returns a single purchase return
func (h *Handler) GetPurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		Preload("Lines").First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": purchaseReturn})
}

// DeletePurchaseReturn soft deletes a purchase return
func (h *Handler) DeletePurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	// Only draft or rejected can be deleted
	if purchaseReturn.Status != entity.ReturnStatusDraft && purchaseReturn.Status != entity.ReturnStatusRejected {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only draft or rejected returns can be deleted"})
		return
	}

	now := time.Now()
	h.db.Model(&purchaseReturn).Update("deleted_at", now)

	c.JSON(http.StatusOK, gin.H{"message": "Purchase return deleted"})
}

// SubmitPurchaseReturn submits a return for approval
func (h *Handler) SubmitPurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	if purchaseReturn.Status != entity.ReturnStatusDraft {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only draft returns can be submitted"})
		return
	}

	h.db.Model(&purchaseReturn).Update("status", entity.ReturnStatusPendingApproval)

	h.db.Preload("Lines").First(&purchaseReturn, returnID)
	c.JSON(http.StatusOK, gin.H{"data": purchaseReturn})
}

// ApprovePurchaseReturn approves a return
func (h *Handler) ApprovePurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	if purchaseReturn.Status != entity.ReturnStatusPendingApproval {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only pending returns can be approved"})
		return
	}

	var input entity.ApproveReturnInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()
	updates := map[string]interface{}{
		"status":      entity.ReturnStatusApproved,
		"approved_by": input.ApprovedBy,
		"approved_at": now,
	}

	h.db.Model(&purchaseReturn).Updates(updates)

	h.db.Preload("Lines").First(&purchaseReturn, returnID)
	c.JSON(http.StatusOK, gin.H{"data": purchaseReturn})
}

// RejectPurchaseReturn rejects a return
func (h *Handler) RejectPurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	if purchaseReturn.Status != entity.ReturnStatusPendingApproval {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only pending returns can be rejected"})
		return
	}

	var input entity.RejectReturnInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{
		"status":           entity.ReturnStatusRejected,
		"rejection_reason": input.Reason,
	}

	h.db.Model(&purchaseReturn).Updates(updates)

	h.db.Preload("Lines").First(&purchaseReturn, returnID)
	c.JSON(http.StatusOK, gin.H{"data": purchaseReturn})
}

// ShipPurchaseReturn marks a return as shipped
func (h *Handler) ShipPurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	if purchaseReturn.Status != entity.ReturnStatusApproved {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only approved returns can be shipped"})
		return
	}

	var input entity.ShipReturnInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	shippedDate := time.Now()
	if input.ShippedDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ShippedDate); err == nil {
			shippedDate = parsed
		}
	}

	updates := map[string]interface{}{
		"status":          entity.ReturnStatusShipped,
		"shipping_method": input.ShippingMethod,
		"tracking_number": input.TrackingNumber,
		"shipped_date":    shippedDate,
	}

	h.db.Model(&purchaseReturn).Updates(updates)

	h.db.Preload("Lines").First(&purchaseReturn, returnID)
	c.JSON(http.StatusOK, gin.H{"data": purchaseReturn})
}

// ReceivePurchaseReturn marks return as received by supplier
func (h *Handler) ReceivePurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	if purchaseReturn.Status != entity.ReturnStatusShipped {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only shipped returns can be marked as received"})
		return
	}

	var input entity.ReceiveReturnInput
	c.ShouldBindJSON(&input)

	receivedDate := time.Now()
	if input.ReceivedDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ReceivedDate); err == nil {
			receivedDate = parsed
		}
	}

	updates := map[string]interface{}{
		"status":        entity.ReturnStatusReceived,
		"received_date": receivedDate,
	}

	// Mark all lines as received back
	h.db.Model(&entity.PurchaseReturnLine{}).Where("return_id = ?", returnID).Update("received_back", true)

	h.db.Model(&purchaseReturn).Updates(updates)

	h.db.Preload("Lines").First(&purchaseReturn, returnID)
	c.JSON(http.StatusOK, gin.H{"data": purchaseReturn})
}

// ApplyCreditNote applies a credit note to a return
func (h *Handler) ApplyCreditNote(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	if purchaseReturn.Status != entity.ReturnStatusReceived {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Credit can only be applied to received returns"})
		return
	}

	var input entity.ApplyCreditInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	creditDate := time.Now()
	if input.CreditNoteDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.CreditNoteDate); err == nil {
			creditDate = parsed
		}
	}

	updates := map[string]interface{}{
		"status":             entity.ReturnStatusCredited,
		"credit_note_number": input.CreditNoteNumber,
		"credit_note_date":   creditDate,
		"credit_amount":      input.CreditAmount,
	}

	// Mark all lines as credit applied
	h.db.Model(&entity.PurchaseReturnLine{}).Where("return_id = ?", returnID).Update("credit_applied", true)

	h.db.Model(&purchaseReturn).Updates(updates)

	h.db.Preload("Lines").First(&purchaseReturn, returnID)
	c.JSON(http.StatusOK, gin.H{"data": purchaseReturn})
}

// CancelPurchaseReturn cancels a return
func (h *Handler) CancelPurchaseReturn(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	returnID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid purchase return ID"})
		return
	}

	var purchaseReturn entity.PurchaseReturn
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", returnID, tenantID).
		First(&purchaseReturn).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase return not found"})
		return
	}

	// Cannot cancel if already shipped or completed
	if purchaseReturn.Status == entity.ReturnStatusShipped ||
		purchaseReturn.Status == entity.ReturnStatusReceived ||
		purchaseReturn.Status == entity.ReturnStatusCredited {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot cancel shipped or completed returns"})
		return
	}

	h.db.Model(&purchaseReturn).Update("status", entity.ReturnStatusCancelled)

	h.db.Preload("Lines").First(&purchaseReturn, returnID)
	c.JSON(http.StatusOK, gin.H{"data": purchaseReturn})
}
