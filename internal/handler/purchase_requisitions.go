package handler

import (
	"fmt"
	"net/http"
	"time"

	"genix-backend/internal/domain/entity"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListPurchaseRequisitions returns all purchase requisitions for the tenant
func (h *Handler) ListPurchaseRequisitions(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var requisitions []entity.PurchaseRequisition
	query := h.db.Where("tenant_id = ? AND deleted_at IS NULL", tenantID).
		Order("created_at DESC")

	// Filter by status
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}

	// Filter by department
	if dept := c.Query("department"); dept != "" {
		query = query.Where("department = ?", dept)
	}

	// Filter by priority
	if priority := c.Query("priority"); priority != "" {
		query = query.Where("priority = ?", priority)
	}

	if err := query.Preload("Lines").Find(&requisitions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch purchase requisitions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": requisitions})
}

// CreatePurchaseRequisition creates a new purchase requisition
func (h *Handler) CreatePurchaseRequisition(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var input entity.CreatePRInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Generate PR number
	var count int64
	h.db.Model(&entity.PurchaseRequisition{}).Where("tenant_id = ?", tenantID).Count(&count)
	prNumber := fmt.Sprintf("PR-%s-%04d", time.Now().Format("200601"), count+1)

	// Parse dates
	requestDate := time.Now()
	if input.RequestDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.RequestDate); err == nil {
			requestDate = parsed
		}
	}

	var requiredDate *time.Time
	if input.RequiredDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.RequiredDate); err == nil {
			requiredDate = &parsed
		}
	}

	priority := input.Priority
	if priority == "" {
		priority = entity.PRPriorityMedium
	}

	// Start transaction
	tx := h.db.Begin()

	requisition := entity.PurchaseRequisition{
		TenantID:     tenantID,
		PRNumber:     prNumber,
		RequestedBy:  input.RequestedBy,
		Department:   input.Department,
		RequestDate:  requestDate,
		RequiredDate: requiredDate,
		Status:       entity.PRStatusDraft,
		Priority:     priority,
		Purpose:      input.Purpose,
		Notes:        input.Notes,
	}

	if err := tx.Create(&requisition).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create purchase requisition"})
		return
	}

	// Create lines
	var totalAmount float64
	for _, lineInput := range input.Lines {
		var productID *uuid.UUID
		if lineInput.ProductID != "" {
			if pid, err := uuid.Parse(lineInput.ProductID); err == nil {
				productID = &pid
			}
		}

		var preferredVendor *uuid.UUID
		if lineInput.PreferredVendor != "" {
			if vid, err := uuid.Parse(lineInput.PreferredVendor); err == nil {
				preferredVendor = &vid
			}
		}

		unit := lineInput.Unit
		if unit == "" {
			unit = "pcs"
		}

		totalPrice := lineInput.Quantity * lineInput.EstimatedPrice

		line := entity.PurchaseRequisitionLine{
			RequisitionID:   requisition.ID,
			ProductID:       productID,
			ProductName:     lineInput.ProductName,
			ProductCode:     lineInput.ProductCode,
			Description:     lineInput.Description,
			Quantity:        lineInput.Quantity,
			Unit:            unit,
			EstimatedPrice:  lineInput.EstimatedPrice,
			TotalPrice:      totalPrice,
			PreferredVendor: preferredVendor,
			VendorName:      lineInput.VendorName,
			Specifications:  lineInput.Specifications,
		}

		if err := tx.Create(&line).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create requisition line"})
			return
		}

		totalAmount += totalPrice
	}

	// Update total amount
	if err := tx.Model(&requisition).Update("total_amount", totalAmount).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update total amount"})
		return
	}

	tx.Commit()

	// Fetch with lines
	h.db.Preload("Lines").First(&requisition, requisition.ID)

	c.JSON(http.StatusCreated, gin.H{"data": requisition})
}

// GetPurchaseRequisition returns a single purchase requisition
func (h *Handler) GetPurchaseRequisition(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	prID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid requisition ID"})
		return
	}

	var requisition entity.PurchaseRequisition
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", prID, tenantID).
		Preload("Lines").First(&requisition).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase requisition not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": requisition})
}

// UpdatePurchaseRequisition updates a purchase requisition
func (h *Handler) UpdatePurchaseRequisition(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	prID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid requisition ID"})
		return
	}

	var requisition entity.PurchaseRequisition
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", prID, tenantID).
		First(&requisition).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase requisition not found"})
		return
	}

	// Only draft requisitions can be updated
	if requisition.Status != entity.PRStatusDraft {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only draft requisitions can be updated"})
		return
	}

	var input entity.UpdatePRInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx := h.db.Begin()

	// Update fields
	updates := map[string]interface{}{}
	if input.RequestedBy != "" {
		updates["requested_by"] = input.RequestedBy
	}
	if input.Department != "" {
		updates["department"] = input.Department
	}
	if input.RequiredDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.RequiredDate); err == nil {
			updates["required_date"] = parsed
		}
	}
	if input.Priority != "" {
		updates["priority"] = input.Priority
	}
	if input.Purpose != "" {
		updates["purpose"] = input.Purpose
	}
	if input.Notes != "" {
		updates["notes"] = input.Notes
	}

	if err := tx.Model(&requisition).Updates(updates).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update requisition"})
		return
	}

	// Update lines if provided
	if len(input.Lines) > 0 {
		// Delete existing lines
		if err := tx.Where("requisition_id = ?", prID).Delete(&entity.PurchaseRequisitionLine{}).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update lines"})
			return
		}

		// Create new lines
		var totalAmount float64
		for _, lineInput := range input.Lines {
			var productID *uuid.UUID
			if lineInput.ProductID != "" {
				if pid, err := uuid.Parse(lineInput.ProductID); err == nil {
					productID = &pid
				}
			}

			var preferredVendor *uuid.UUID
			if lineInput.PreferredVendor != "" {
				if vid, err := uuid.Parse(lineInput.PreferredVendor); err == nil {
					preferredVendor = &vid
				}
			}

			unit := lineInput.Unit
			if unit == "" {
				unit = "pcs"
			}

			totalPrice := lineInput.Quantity * lineInput.EstimatedPrice

			line := entity.PurchaseRequisitionLine{
				RequisitionID:   prID,
				ProductID:       productID,
				ProductName:     lineInput.ProductName,
				ProductCode:     lineInput.ProductCode,
				Description:     lineInput.Description,
				Quantity:        lineInput.Quantity,
				Unit:            unit,
				EstimatedPrice:  lineInput.EstimatedPrice,
				TotalPrice:      totalPrice,
				PreferredVendor: preferredVendor,
				VendorName:      lineInput.VendorName,
				Specifications:  lineInput.Specifications,
			}

			if err := tx.Create(&line).Error; err != nil {
				tx.Rollback()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create line"})
				return
			}

			totalAmount += totalPrice
		}

		if err := tx.Model(&requisition).Update("total_amount", totalAmount).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update total"})
			return
		}
	}

	tx.Commit()

	h.db.Preload("Lines").First(&requisition, prID)
	c.JSON(http.StatusOK, gin.H{"data": requisition})
}

// DeletePurchaseRequisition soft deletes a purchase requisition
func (h *Handler) DeletePurchaseRequisition(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	prID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid requisition ID"})
		return
	}

	var requisition entity.PurchaseRequisition
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", prID, tenantID).
		First(&requisition).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase requisition not found"})
		return
	}

	// Only draft or rejected can be deleted
	if requisition.Status != entity.PRStatusDraft && requisition.Status != entity.PRStatusRejected {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only draft or rejected requisitions can be deleted"})
		return
	}

	now := time.Now()
	h.db.Model(&requisition).Update("deleted_at", now)

	c.JSON(http.StatusOK, gin.H{"message": "Purchase requisition deleted"})
}

// SubmitPurchaseRequisition submits a PR for approval
func (h *Handler) SubmitPurchaseRequisition(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	prID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid requisition ID"})
		return
	}

	var requisition entity.PurchaseRequisition
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", prID, tenantID).
		First(&requisition).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase requisition not found"})
		return
	}

	if requisition.Status != entity.PRStatusDraft {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only draft requisitions can be submitted"})
		return
	}

	h.db.Model(&requisition).Update("status", entity.PRStatusPendingApproval)

	h.db.Preload("Lines").First(&requisition, prID)
	c.JSON(http.StatusOK, gin.H{"data": requisition})
}

// ApprovePurchaseRequisition approves a PR
func (h *Handler) ApprovePurchaseRequisition(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	prID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid requisition ID"})
		return
	}

	var requisition entity.PurchaseRequisition
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", prID, tenantID).
		Preload("Lines").First(&requisition).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase requisition not found"})
		return
	}

	if requisition.Status != entity.PRStatusPendingApproval {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only pending requisitions can be approved"})
		return
	}

	var input entity.ApprovePRInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()
	updates := map[string]interface{}{
		"status":      entity.PRStatusApproved,
		"approved_by": input.ApprovedBy,
		"approved_at": now,
	}

	tx := h.db.Begin()

	if err := tx.Model(&requisition).Updates(updates).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to approve requisition"})
		return
	}

	// Update approved quantities if provided
	for _, lineApproval := range input.Lines {
		lineID, err := uuid.Parse(lineApproval.LineID)
		if err != nil {
			continue
		}
		tx.Model(&entity.PurchaseRequisitionLine{}).
			Where("id = ?", lineID).
			Update("approved_quantity", lineApproval.ApprovedQuantity)
	}

	tx.Commit()

	h.db.Preload("Lines").First(&requisition, prID)
	c.JSON(http.StatusOK, gin.H{"data": requisition})
}

// RejectPurchaseRequisition rejects a PR
func (h *Handler) RejectPurchaseRequisition(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	prID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid requisition ID"})
		return
	}

	var requisition entity.PurchaseRequisition
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", prID, tenantID).
		First(&requisition).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase requisition not found"})
		return
	}

	if requisition.Status != entity.PRStatusPendingApproval {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only pending requisitions can be rejected"})
		return
	}

	var input entity.RejectPRInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{
		"status":           entity.PRStatusRejected,
		"rejection_reason": input.Reason,
	}

	h.db.Model(&requisition).Updates(updates)

	h.db.Preload("Lines").First(&requisition, prID)
	c.JSON(http.StatusOK, gin.H{"data": requisition})
}

// ConvertPRToPO converts an approved PR to a Purchase Order
func (h *Handler) ConvertPRToPO(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	prID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid requisition ID"})
		return
	}

	var requisition entity.PurchaseRequisition
	if err := h.db.Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", prID, tenantID).
		Preload("Lines").First(&requisition).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Purchase requisition not found"})
		return
	}

	if requisition.Status != entity.PRStatusApproved {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only approved requisitions can be converted to PO"})
		return
	}

	var input entity.ConvertPRToPOInput
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

	tx := h.db.Begin()

	// Generate PO number
	var poCount int64
	h.db.Model(&entity.PurchaseOrder{}).Where("tenant_id = ?", tenantID).Count(&poCount)
	poNumber := fmt.Sprintf("PO-%s-%04d", time.Now().Format("200601"), poCount+1)

	paymentTerms := input.PaymentTerms
	if paymentTerms == "" {
		paymentTerms = "net_30"
	}

	// Create Purchase Order
	po := entity.PurchaseOrder{
		TenantID:     tenantID,
		PONumber:     poNumber,
		SupplierID:   supplierID,
		SupplierName: supplier.Name,
		OrderDate:    time.Now(),
		Status:       entity.POStatusDraft,
		PaymentTerms: paymentTerms,
		TotalAmount:  requisition.TotalAmount,
	}

	if err := tx.Create(&po).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create purchase order"})
		return
	}

	// Create PO lines from PR lines
	for _, prLine := range requisition.Lines {
		qty := prLine.Quantity
		if prLine.ApprovedQuantity > 0 {
			qty = prLine.ApprovedQuantity
		}

		poLine := entity.PurchaseOrderLine{
			PurchaseOrderID: po.ID,
			ProductID:       prLine.ProductID,
			ProductName:     prLine.ProductName,
			ProductCode:     prLine.ProductCode,
			Description:     prLine.Description,
			Quantity:        qty,
			Unit:            prLine.Unit,
			UnitPrice:       prLine.EstimatedPrice,
			TotalPrice:      qty * prLine.EstimatedPrice,
		}

		if err := tx.Create(&poLine).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create PO line"})
			return
		}
	}

	// Update PR status
	if err := tx.Model(&requisition).Updates(map[string]interface{}{
		"status":          entity.PRStatusConverted,
		"converted_to_po": po.ID,
	}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update requisition"})
		return
	}

	tx.Commit()

	h.db.Preload("Lines").First(&po, po.ID)
	c.JSON(http.StatusOK, gin.H{"data": po})
}
