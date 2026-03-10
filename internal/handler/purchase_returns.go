package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Return Status constants
const (
	ReturnStatusDraft           = "draft"
	ReturnStatusPendingApproval = "pending_approval"
	ReturnStatusApproved        = "approved"
	ReturnStatusRejected        = "rejected"
	ReturnStatusShipped         = "shipped"
	ReturnStatusReceived        = "received"
	ReturnStatusCredited        = "credited"
	ReturnStatusCancelled       = "cancelled"
)

// ListPurchaseReturns returns all purchase returns for the tenant
func (h *Handler) ListPurchaseReturns(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	// Pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Build query
	baseQuery := `
		SELECT id, tenant_id, return_number, purchase_order_id, po_number,
			   goods_receipt_id, gr_number, supplier_id, supplier_name,
			   return_date, requested_by, status, return_reason, reason_description,
			   total_quantity, total_value, shipping_method, tracking_number,
			   shipped_date, received_date, approved_by, approved_at,
			   rejection_reason, credit_note_number, credit_note_date, credit_amount,
			   notes, created_at, updated_at
		FROM purchase_returns
		WHERE tenant_id = $1 AND deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM purchase_returns WHERE tenant_id = $1 AND deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by supplier
	if supplierID := c.Query("supplier_id"); supplierID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND supplier_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND supplier_id = $%d", argCount)
		args = append(args, supplierID)
	}

	// Filter by reason
	if reason := c.Query("return_reason"); reason != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND return_reason = $%d", argCount)
		countQuery += fmt.Sprintf(" AND return_reason = $%d", argCount)
		args = append(args, reason)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(return_number) LIKE $%d OR LOWER(supplier_name) LIKE $%d OR LOWER(po_number) LIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(return_number) LIKE $%d OR LOWER(supplier_name) LIKE $%d OR LOWER(po_number) LIKE $%d)", argCount, argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count purchase returns")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to fetch purchase returns", "error", err)
		response.InternalError(c, "Failed to fetch purchase returns")
		return
	}
	defer rows.Close()

	var returns []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan, supplierID uuid.UUID
		var returnNumber, supplierName, status, returnReason string
		var poID, grID sql.NullString
		var poNumber, grNumber, requestedBy, reasonDescription sql.NullString
		var shippingMethod, trackingNumber, approvedBy, rejectionReason sql.NullString
		var creditNoteNumber, notes sql.NullString
		var returnDate time.Time
		var shippedDate, receivedDate, approvedAt, creditNoteDate sql.NullTime
		var totalQty, totalValue, creditAmount float64
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &tenantIDScan, &returnNumber, &poID, &poNumber,
			&grID, &grNumber, &supplierID, &supplierName,
			&returnDate, &requestedBy, &status, &returnReason, &reasonDescription,
			&totalQty, &totalValue, &shippingMethod, &trackingNumber,
			&shippedDate, &receivedDate, &approvedBy, &approvedAt,
			&rejectionReason, &creditNoteNumber, &creditNoteDate, &creditAmount,
			&notes, &createdAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		ret := map[string]interface{}{
			"id":             id.String(),
			"tenant_id":      tenantIDScan.String(),
			"return_number":  returnNumber,
			"supplier_id":    supplierID.String(),
			"supplier_name":  supplierName,
			"return_date":    returnDate.Format("2006-01-02"),
			"status":         status,
			"return_reason":  returnReason,
			"total_quantity": totalQty,
			"total_value":    totalValue,
			"credit_amount":  creditAmount,
			"created_at":     createdAt,
			"updated_at":     updatedAt,
		}

		if poID.Valid {
			ret["purchase_order_id"] = poID.String
		}
		if poNumber.Valid {
			ret["po_number"] = poNumber.String
		}
		if grID.Valid {
			ret["goods_receipt_id"] = grID.String
		}
		if grNumber.Valid {
			ret["gr_number"] = grNumber.String
		}
		if requestedBy.Valid {
			ret["requested_by"] = requestedBy.String
		}
		if reasonDescription.Valid {
			ret["reason_description"] = reasonDescription.String
		}
		if shippingMethod.Valid {
			ret["shipping_method"] = shippingMethod.String
		}
		if trackingNumber.Valid {
			ret["tracking_number"] = trackingNumber.String
		}
		if shippedDate.Valid {
			ret["shipped_date"] = shippedDate.Time.Format("2006-01-02")
		}
		if receivedDate.Valid {
			ret["received_date"] = receivedDate.Time.Format("2006-01-02")
		}
		if approvedBy.Valid {
			ret["approved_by"] = approvedBy.String
		}
		if approvedAt.Valid {
			ret["approved_at"] = approvedAt.Time
		}
		if rejectionReason.Valid {
			ret["rejection_reason"] = rejectionReason.String
		}
		if creditNoteNumber.Valid {
			ret["credit_note_number"] = creditNoteNumber.String
		}
		if creditNoteDate.Valid {
			ret["credit_note_date"] = creditNoteDate.Time.Format("2006-01-02")
		}
		if notes.Valid {
			ret["notes"] = notes.String
		}

		returns = append(returns, ret)
	}

	response.Paginated(c, returns, page, pageSize, total)
}

// CreatePurchaseReturn creates a new purchase return
func (h *Handler) CreatePurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	// Get organization ID from middleware header
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var input struct {
		SupplierID        string `json:"supplier_id" binding:"required"`
		PurchaseOrderID   string `json:"purchase_order_id,omitempty"`
		GoodsReceiptID    string `json:"goods_receipt_id,omitempty"`
		ReturnDate        string `json:"return_date,omitempty"`
		RequestedBy       string `json:"requested_by,omitempty"`
		ReturnReason      string `json:"return_reason" binding:"required"`
		ReasonDescription string `json:"reason_description,omitempty"`
		ShippingMethod    string `json:"shipping_method,omitempty"`
		Notes             string `json:"notes,omitempty"`
		Lines             []struct {
			POLineID       string  `json:"po_line_id,omitempty"`
			GRLineID       string  `json:"gr_line_id,omitempty"`
			ProductID      string  `json:"product_id,omitempty"`
			ProductName    string  `json:"product_name" binding:"required"`
			ProductCode    string  `json:"product_code,omitempty"`
			ReturnQuantity float64 `json:"return_quantity" binding:"required"`
			Unit           string  `json:"unit,omitempty"`
			UnitPrice      float64 `json:"unit_price,omitempty"`
			ReturnReason   string  `json:"return_reason,omitempty"`
			ReasonDetails  string  `json:"reason_details,omitempty"`
			BatchNumber    string  `json:"batch_number,omitempty"`
			SerialNumbers  string  `json:"serial_numbers,omitempty"`
			Condition      string  `json:"condition,omitempty"`
			Notes          string  `json:"notes,omitempty"`
		} `json:"lines" binding:"required,min=1"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	supplierID, err := uuid.Parse(input.SupplierID)
	if err != nil {
		response.BadRequest(c, "Invalid supplier ID")
		return
	}

	// Get supplier info
	var supplierName string
	err = h.db.QueryRow("SELECT name FROM contacts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", supplierID, tenantID).Scan(&supplierName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Supplier")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch supplier")
		return
	}

	// Generate return number
	var count int
	h.db.QueryRow("SELECT COUNT(*) FROM purchase_returns WHERE tenant_id = $1", tenantID).Scan(&count)
	returnNumber := fmt.Sprintf("PR%05d", count+1)

	returnID := uuid.New()
	now := time.Now()

	// Parse return date
	returnDate := now
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
			h.db.QueryRow("SELECT order_number FROM purchase_orders WHERE id = $1", pid).Scan(&poNumber)
		}
	}

	var grID *uuid.UUID
	var grNumber string
	if input.GoodsReceiptID != "" {
		if gid, err := uuid.Parse(input.GoodsReceiptID); err == nil {
			grID = &gid
			h.db.QueryRow("SELECT gr_number FROM goods_receipts WHERE id = $1", gid).Scan(&grNumber)
		}
	}

	// Insert purchase return
	query := `
		INSERT INTO purchase_returns (
			id, tenant_id, organization_id, return_number, purchase_order_id, po_number,
			goods_receipt_id, gr_number, supplier_id, supplier_name,
			return_date, requested_by, status, return_reason, reason_description,
			shipping_method, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`

	_, err = h.db.Exec(query,
		returnID, tenantID, orgIDPtr, returnNumber, poID, poNumber,
		grID, grNumber, supplierID, supplierName,
		returnDate, input.RequestedBy, ReturnStatusDraft, input.ReturnReason, input.ReasonDescription,
		input.ShippingMethod, input.Notes, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create purchase return", "error", err)
		response.InternalError(c, "Failed to create purchase return")
		return
	}

	// Insert lines
	lineQuery := `
		INSERT INTO purchase_return_lines (
			id, return_id, po_line_id, gr_line_id, product_id, product_name,
			product_code, return_quantity, unit, unit_price, total_price,
			return_reason, reason_details, batch_number, serial_numbers,
			condition, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`

	var totalQty, totalValue float64
	for _, lineInput := range input.Lines {
		lineID := uuid.New()

		var productID, poLineID, grLineID *uuid.UUID
		if lineInput.ProductID != "" {
			if pid, err := uuid.Parse(lineInput.ProductID); err == nil {
				productID = &pid
			}
		}
		if lineInput.POLineID != "" {
			if lid, err := uuid.Parse(lineInput.POLineID); err == nil {
				poLineID = &lid
			}
		}
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

		h.db.Exec(lineQuery,
			lineID, returnID, poLineID, grLineID, productID, lineInput.ProductName,
			lineInput.ProductCode, lineInput.ReturnQuantity, unit, lineInput.UnitPrice, totalPrice,
			returnReason, lineInput.ReasonDetails, lineInput.BatchNumber, lineInput.SerialNumbers,
			lineInput.Condition, lineInput.Notes, now, now,
		)

		totalQty += lineInput.ReturnQuantity
		totalValue += totalPrice
	}

	// Update totals
	h.db.Exec("UPDATE purchase_returns SET total_quantity = $1, total_value = $2, updated_at = $3 WHERE id = $4", totalQty, totalValue, now, returnID)

	// Return created return
	retResponse := map[string]interface{}{
		"id":             returnID.String(),
		"tenant_id":      tenantID.String(),
		"return_number":  returnNumber,
		"supplier_id":    supplierID.String(),
		"supplier_name":  supplierName,
		"return_date":    returnDate.Format("2006-01-02"),
		"status":         ReturnStatusDraft,
		"return_reason":  input.ReturnReason,
		"total_quantity": totalQty,
		"total_value":    totalValue,
		"created_at":     now,
	}

	if poID != nil {
		retResponse["purchase_order_id"] = poID.String()
		retResponse["po_number"] = poNumber
	}
	if grID != nil {
		retResponse["goods_receipt_id"] = grID.String()
		retResponse["gr_number"] = grNumber
	}
	if input.RequestedBy != "" {
		retResponse["requested_by"] = input.RequestedBy
	}

	response.Created(c, retResponse)
}

// GetPurchaseReturn returns a single purchase return
func (h *Handler) GetPurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	// Get return
	query := `
		SELECT id, tenant_id, return_number, purchase_order_id, po_number,
			   goods_receipt_id, gr_number, supplier_id, supplier_name,
			   return_date, requested_by, status, return_reason, reason_description,
			   total_quantity, total_value, shipping_method, tracking_number,
			   shipped_date, received_date, approved_by, approved_at,
			   rejection_reason, credit_note_number, credit_note_date, credit_amount,
			   notes, created_at, updated_at
		FROM purchase_returns
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`

	var id, tenantIDScan, supplierID uuid.UUID
	var returnNumber, supplierName, status, returnReason string
	var poID, grID sql.NullString
	var poNumber, grNumber, requestedBy, reasonDescription sql.NullString
	var shippingMethod, trackingNumber, approvedBy, rejectionReason sql.NullString
	var creditNoteNumber, notes sql.NullString
	var returnDate time.Time
	var shippedDate, receivedDate, approvedAt, creditNoteDate sql.NullTime
	var totalQty, totalValue, creditAmount float64
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(query, returnID, tenantID).Scan(
		&id, &tenantIDScan, &returnNumber, &poID, &poNumber,
		&grID, &grNumber, &supplierID, &supplierName,
		&returnDate, &requestedBy, &status, &returnReason, &reasonDescription,
		&totalQty, &totalValue, &shippingMethod, &trackingNumber,
		&shippedDate, &receivedDate, &approvedBy, &approvedAt,
		&rejectionReason, &creditNoteNumber, &creditNoteDate, &creditAmount,
		&notes, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch purchase return", "error", err)
		response.InternalError(c, "Failed to fetch purchase return")
		return
	}

	ret := map[string]interface{}{
		"id":             id.String(),
		"tenant_id":      tenantIDScan.String(),
		"return_number":  returnNumber,
		"supplier_id":    supplierID.String(),
		"supplier_name":  supplierName,
		"return_date":    returnDate.Format("2006-01-02"),
		"status":         status,
		"return_reason":  returnReason,
		"total_quantity": totalQty,
		"total_value":    totalValue,
		"credit_amount":  creditAmount,
		"created_at":     createdAt,
		"updated_at":     updatedAt,
	}

	if poID.Valid {
		ret["purchase_order_id"] = poID.String
	}
	if poNumber.Valid {
		ret["po_number"] = poNumber.String
	}
	if grID.Valid {
		ret["goods_receipt_id"] = grID.String
	}
	if grNumber.Valid {
		ret["gr_number"] = grNumber.String
	}
	if requestedBy.Valid {
		ret["requested_by"] = requestedBy.String
	}
	if reasonDescription.Valid {
		ret["reason_description"] = reasonDescription.String
	}
	if shippingMethod.Valid {
		ret["shipping_method"] = shippingMethod.String
	}
	if trackingNumber.Valid {
		ret["tracking_number"] = trackingNumber.String
	}
	if shippedDate.Valid {
		ret["shipped_date"] = shippedDate.Time.Format("2006-01-02")
	}
	if receivedDate.Valid {
		ret["received_date"] = receivedDate.Time.Format("2006-01-02")
	}
	if approvedBy.Valid {
		ret["approved_by"] = approvedBy.String
	}
	if approvedAt.Valid {
		ret["approved_at"] = approvedAt.Time
	}
	if rejectionReason.Valid {
		ret["rejection_reason"] = rejectionReason.String
	}
	if creditNoteNumber.Valid {
		ret["credit_note_number"] = creditNoteNumber.String
	}
	if creditNoteDate.Valid {
		ret["credit_note_date"] = creditNoteDate.Time.Format("2006-01-02")
	}
	if notes.Valid {
		ret["notes"] = notes.String
	}

	// Get lines
	linesQuery := `
		SELECT id, po_line_id, gr_line_id, product_id, product_name, product_code,
			   return_quantity, unit, unit_price, total_price, return_reason,
			   reason_details, batch_number, serial_numbers, condition,
			   received_back, credit_applied, notes
		FROM purchase_return_lines
		WHERE return_id = $1
		ORDER BY created_at`

	linesRows, err := h.db.Query(linesQuery, returnID)
	if err == nil {
		defer linesRows.Close()
		var lines []map[string]interface{}
		for linesRows.Next() {
			var lineID uuid.UUID
			var poLineID, grLineID, productID sql.NullString
			var productName, unit, lineReturnReason string
			var productCode, reasonDetails, batchNumber, serialNumbers, lineCondition, lineNotes sql.NullString
			var returnQty, lineUnitPrice, lineTotalPrice float64
			var receivedBack, creditApplied bool

			err := linesRows.Scan(
				&lineID, &poLineID, &grLineID, &productID, &productName, &productCode,
				&returnQty, &unit, &lineUnitPrice, &lineTotalPrice, &lineReturnReason,
				&reasonDetails, &batchNumber, &serialNumbers, &lineCondition,
				&receivedBack, &creditApplied, &lineNotes,
			)
			if err != nil {
				continue
			}

			line := map[string]interface{}{
				"id":              lineID.String(),
				"product_name":    productName,
				"return_quantity": returnQty,
				"unit":            unit,
				"unit_price":      lineUnitPrice,
				"total_price":     lineTotalPrice,
				"return_reason":   lineReturnReason,
				"received_back":   receivedBack,
				"credit_applied":  creditApplied,
			}

			if poLineID.Valid {
				line["po_line_id"] = poLineID.String
			}
			if grLineID.Valid {
				line["gr_line_id"] = grLineID.String
			}
			if productID.Valid {
				line["product_id"] = productID.String
			}
			if productCode.Valid {
				line["product_code"] = productCode.String
			}
			if reasonDetails.Valid {
				line["reason_details"] = reasonDetails.String
			}
			if batchNumber.Valid {
				line["batch_number"] = batchNumber.String
			}
			if serialNumbers.Valid {
				line["serial_numbers"] = serialNumbers.String
			}
			if lineCondition.Valid {
				line["condition"] = lineCondition.String
			}
			if lineNotes.Valid {
				line["notes"] = lineNotes.String
			}

			lines = append(lines, line)
		}
		ret["lines"] = lines
	}

	response.Success(c, ret)
}

// DeletePurchaseReturn soft deletes a purchase return
func (h *Handler) DeletePurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	// Check status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", returnID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}
	if currentStatus != ReturnStatusDraft && currentStatus != ReturnStatusRejected {
		response.BadRequest(c, "Only draft or rejected returns can be deleted")
		return
	}

	result, err := h.db.Exec(
		"UPDATE purchase_returns SET deleted_at = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL",
		time.Now(), time.Now(), returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete purchase return")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Purchase return")
		return
	}

	response.NoContent(c)
}

// SubmitPurchaseReturn submits a return for approval
func (h *Handler) SubmitPurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", returnID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}
	if currentStatus != ReturnStatusDraft {
		response.BadRequest(c, "Only draft returns can be submitted")
		return
	}

	_, err = h.db.Exec(
		"UPDATE purchase_returns SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		ReturnStatusPendingApproval, time.Now(), returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to submit purchase return")
		return
	}

	h.GetPurchaseReturn(c)
}

// ApprovePurchaseReturn approves a return
func (h *Handler) ApprovePurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", returnID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}
	if currentStatus != ReturnStatusPendingApproval {
		response.BadRequest(c, "Only pending returns can be approved")
		return
	}

	var input struct {
		ApprovedBy string `json:"approved_by,omitempty"`
	}
	c.ShouldBindJSON(&input)

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE purchase_returns SET status = $1, approved_by = $2, approved_at = $3, updated_at = $4 WHERE id = $5 AND tenant_id = $6",
		ReturnStatusApproved, input.ApprovedBy, now, now, returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to approve purchase return")
		return
	}

	h.GetPurchaseReturn(c)
}

// RejectPurchaseReturn rejects a return
func (h *Handler) RejectPurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", returnID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}
	if currentStatus != ReturnStatusPendingApproval {
		response.BadRequest(c, "Only pending returns can be rejected")
		return
	}

	var input struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	_, err = h.db.Exec(
		"UPDATE purchase_returns SET status = $1, rejection_reason = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		ReturnStatusRejected, input.Reason, time.Now(), returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to reject purchase return")
		return
	}

	h.GetPurchaseReturn(c)
}

// ShipPurchaseReturn marks a return as shipped
// This also DECREASES inventory (products leave warehouse to go back to supplier)
func (h *Handler) ShipPurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", returnID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}
	if currentStatus != ReturnStatusApproved {
		response.BadRequest(c, "Only approved returns can be shipped")
		return
	}

	var input struct {
		ShippingMethod string `json:"shipping_method,omitempty"`
		TrackingNumber string `json:"tracking_number,omitempty"`
		ShippedDate    string `json:"shipped_date,omitempty"`
	}
	c.ShouldBindJSON(&input)

	shippedDate := time.Now()
	if input.ShippedDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ShippedDate); err == nil {
			shippedDate = parsed
		}
	}

	now := time.Now()

	// Get return lines for inventory update
	linesQuery := `
		SELECT product_id, return_quantity, unit_price
		FROM purchase_return_lines
		WHERE return_id = $1`

	rows, err := h.db.Query(linesQuery, returnID)
	if err != nil {
		h.log.Error("Failed to get purchase return lines", "error", err)
	} else {
		defer rows.Close()

		for rows.Next() {
			var productID sql.NullString
			var returnQty, unitPrice float64

			if err := rows.Scan(&productID, &returnQty, &unitPrice); err != nil {
				continue
			}

			if !productID.Valid || returnQty <= 0 {
				continue
			}

			pid, err := uuid.Parse(productID.String)
			if err != nil {
				continue
			}

			// Find inventory record for this product
			var inventoryID uuid.UUID
			var currentQty float64

			err = h.db.QueryRow(`
				SELECT id, quantity_on_hand FROM inventory
				WHERE tenant_id = $1 AND product_id = $2
				LIMIT 1`,
				tenantID, pid,
			).Scan(&inventoryID, &currentQty)

			if err == sql.ErrNoRows {
				// No inventory record - can't decrease what doesn't exist
				h.log.Warn("No inventory record found for purchase return item", "product_id", pid)
				continue
			} else if err != nil {
				h.log.Error("Failed to get inventory for purchase return", "error", err)
				continue
			}

			// Check if we have enough stock
			if currentQty < returnQty {
				h.log.Warn("Insufficient stock for purchase return", "product_id", pid, "current", currentQty, "return_qty", returnQty)
				// Continue anyway - in practice returns should not be approved without stock
			}

			// Update inventory - DECREASE quantity (sending back to supplier)
			// Note: quantity_available is a generated column, only update quantity_on_hand
			_, err = h.db.Exec(`
				UPDATE inventory SET quantity_on_hand = quantity_on_hand - $1, updated_at = $2
				WHERE id = $3`,
				returnQty, now, inventoryID,
			)
			if err != nil {
				h.log.Error("Failed to update inventory for purchase return", "error", err, "inventory_id", inventoryID)
				continue
			}

			// Create inventory transaction for audit trail
			txID := uuid.New()
			h.db.Exec(`
				INSERT INTO inventory_transactions (
					id, tenant_id, inventory_id, transaction_type, quantity,
					unit_cost, total_cost, reference_type, reference_id,
					reason, transaction_date, created_at
				) VALUES ($1, $2, $3, 'issue', $4, $5, $6, 'purchase_return', $7, 'Purchase Return - Return to Supplier', $8, $8)
			`, txID, tenantID, inventoryID, -returnQty, unitPrice, returnQty*unitPrice, returnID, now)
		}
	}

	// Get return details for debit note
	var returnNumber, supplierName string
	var supplierID uuid.UUID
	var totalValue float64
	h.db.QueryRow(`
		SELECT return_number, supplier_id, supplier_name, total_value
		FROM purchase_returns WHERE id = $1 AND tenant_id = $2`,
		returnID, tenantID,
	).Scan(&returnNumber, &supplierID, &supplierName, &totalValue)

	// Update return status
	_, err = h.db.Exec(`
		UPDATE purchase_returns
		SET status = $1, shipping_method = $2, tracking_number = $3, shipped_date = $4, updated_at = $5
		WHERE id = $6 AND tenant_id = $7`,
		ReturnStatusShipped, input.ShippingMethod, input.TrackingNumber, shippedDate, now, returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to ship purchase return")
		return
	}

	// =====================================================
	// CREATE DEBIT NOTE (Journal Entry) - Reduces AP
	// When we return goods to supplier:
	// Debit: Accounts Payable (reduce what we owe supplier)
	// Credit: Inventory or Purchase Expense (reduce inventory value)
	// =====================================================

	// Get organization_id from linked PO for correct account lookup
	var prOrgID *uuid.UUID
	h.db.QueryRow(`SELECT po.organization_id FROM purchase_orders po
		JOIN purchase_returns pr ON pr.purchase_order_id = po.id
		WHERE pr.id = $1`, returnID).Scan(&prOrgID)

	// Get accounts — lookup by name first, then code fallback
	apAccountID := findAccount(h.db, tenantID, prOrgID, "accounts payable", "2000")
	inventoryAccountID := findAccount(h.db, tenantID, prOrgID, "inventory", "1300")

	// Get journal (use GENERAL or PURCHASE journal)
	var journalID uuid.UUID
	h.db.QueryRow("SELECT id FROM journals WHERE tenant_id = $1 AND code IN ('PURCH', 'PURCHASE', 'PUR', 'GENERAL', 'GEN') AND deleted_at IS NULL ORDER BY CASE code WHEN 'PURCH' THEN 0 WHEN 'PURCHASE' THEN 1 WHEN 'PUR' THEN 2 ELSE 3 END LIMIT 1", tenantID).Scan(&journalID)

	if apAccountID != uuid.Nil && inventoryAccountID != uuid.Nil && journalID != uuid.Nil && totalValue > 0 {
		// Generate entry number
		entryNumber := fmt.Sprintf("DN-%s-%s", now.Format("20060102"), uuid.New().String()[:6])

		// Create journal entry (Debit Note)
		entryID := uuid.New()
		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, total_debit, total_credit, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'posted', $13, $14)`,
			entryID, tenantID, prOrgID, journalID, entryNumber, now, returnNumber,
			fmt.Sprintf("Debit Note for Purchase Return %s - %s", returnNumber, supplierName),
			"purchase_return", returnID, totalValue, totalValue, now, now,
		)
		if err != nil {
			h.log.Error("Failed to create debit note journal entry", "error", err)
		} else {
			// Line 1: Debit Accounts Payable (reduce what we owe)
			line1ID := uuid.New()
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, account_id, contact_id, description, debit_amount, credit_amount, line_number, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				line1ID, entryID, apAccountID, supplierID, "Purchase Return - AP Reduction", totalValue, 0, 1, now,
			)

			// Line 2: Credit Inventory (reduce inventory value)
			line2ID := uuid.New()
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				line2ID, entryID, inventoryAccountID, "Purchase Return - Inventory Reduction", 0, totalValue, 2, now,
			)

			// Update account balances
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalValue, now, apAccountID)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalValue, now, inventoryAccountID)

			h.log.Info("Debit Note created for purchase return", "return_id", returnID, "entry_number", entryNumber, "amount", totalValue)
		}
	} else {
		h.log.Warn("Could not create debit note - missing accounts or journal", "ap_account", apAccountID, "inventory_account", inventoryAccountID, "journal", journalID)
	}

	h.log.Info("Purchase return shipped with inventory update", "return_id", returnID)

	h.GetPurchaseReturn(c)
}

// ReceivePurchaseReturn marks return as received by supplier
func (h *Handler) ReceivePurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", returnID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}
	if currentStatus != ReturnStatusShipped {
		response.BadRequest(c, "Only shipped returns can be marked as received")
		return
	}

	var input struct {
		ReceivedDate string `json:"received_date,omitempty"`
	}
	c.ShouldBindJSON(&input)

	receivedDate := time.Now()
	if input.ReceivedDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ReceivedDate); err == nil {
			receivedDate = parsed
		}
	}

	now := time.Now()

	// Mark all lines as received back
	h.db.Exec("UPDATE purchase_return_lines SET received_back = true, updated_at = $1 WHERE return_id = $2", now, returnID)

	// Update return status
	_, err = h.db.Exec(
		"UPDATE purchase_returns SET status = $1, received_date = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		ReturnStatusReceived, receivedDate, now, returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to receive purchase return")
		return
	}

	h.GetPurchaseReturn(c)
}

// ApplyCreditNote applies a credit note to a return
func (h *Handler) ApplyCreditNote(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", returnID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}
	if currentStatus != ReturnStatusReceived {
		response.BadRequest(c, "Credit can only be applied to received returns")
		return
	}

	var input struct {
		CreditNoteNumber string  `json:"credit_note_number" binding:"required"`
		CreditNoteDate   string  `json:"credit_note_date,omitempty"`
		CreditAmount     float64 `json:"credit_amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	creditDate := time.Now()
	if input.CreditNoteDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.CreditNoteDate); err == nil {
			creditDate = parsed
		}
	}

	now := time.Now()

	// Mark all lines as credit applied
	h.db.Exec("UPDATE purchase_return_lines SET credit_applied = true, updated_at = $1 WHERE return_id = $2", now, returnID)

	// Update return with credit info
	_, err = h.db.Exec(`
		UPDATE purchase_returns
		SET status = $1, credit_note_number = $2, credit_note_date = $3, credit_amount = $4, updated_at = $5
		WHERE id = $6 AND tenant_id = $7`,
		ReturnStatusCredited, input.CreditNoteNumber, creditDate, input.CreditAmount, now, returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to apply credit note")
		return
	}

	h.GetPurchaseReturn(c)
}

// CancelPurchaseReturn cancels a return
func (h *Handler) CancelPurchaseReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid purchase return ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", returnID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase return")
		return
	}

	// Cannot cancel if already shipped or completed
	if currentStatus == ReturnStatusShipped ||
		currentStatus == ReturnStatusReceived ||
		currentStatus == ReturnStatusCredited {
		response.BadRequest(c, "Cannot cancel shipped or completed returns")
		return
	}

	_, err = h.db.Exec(
		"UPDATE purchase_returns SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		ReturnStatusCancelled, time.Now(), returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to cancel purchase return")
		return
	}

	h.GetPurchaseReturn(c)
}
