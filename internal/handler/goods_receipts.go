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

// GR Status constants
const (
	GRStatusDraft     = "draft"
	GRStatusPending   = "pending"
	GRStatusCompleted = "completed"
	GRStatusCancelled = "cancelled"
)

// Quality Status constants
const (
	QualityStatusPending = "pending"
	QualityStatusPassed  = "passed"
	QualityStatusFailed  = "failed"
	QualityStatusPartial = "partial"
)

// ListGoodsReceipts returns all goods receipts for the tenant
func (h *Handler) ListGoodsReceipts(c *gin.Context) {
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
		SELECT id, tenant_id, gr_number, purchase_order_id, po_number,
			   supplier_id, supplier_name, receipt_date, received_by,
			   warehouse_id, warehouse_name, status, quality_status,
			   total_quantity, accepted_quantity, rejected_quantity,
			   delivery_note, vehicle_number, driver_name, inspected_by,
			   inspected_at, inspection_notes, notes, created_at, updated_at
		FROM goods_receipts
		WHERE tenant_id = $1 AND deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM goods_receipts WHERE tenant_id = $1 AND deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by PO
	if poID := c.Query("purchase_order_id"); poID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND purchase_order_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND purchase_order_id = $%d", argCount)
		args = append(args, poID)
	}

	// Filter by supplier
	if supplierID := c.Query("supplier_id"); supplierID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND supplier_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND supplier_id = $%d", argCount)
		args = append(args, supplierID)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(gr_number) LIKE $%d OR LOWER(supplier_name) LIKE $%d OR LOWER(po_number) LIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(gr_number) LIKE $%d OR LOWER(supplier_name) LIKE $%d OR LOWER(po_number) LIKE $%d)", argCount, argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count goods receipts")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		response.InternalError(c, "Failed to fetch goods receipts: "+err.Error())
		return
	}
	defer rows.Close()

	var receipts []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan, purchaseOrderID, supplierID uuid.UUID
		var grNumber, poNumber, supplierName, status, qualityStatus string
		var receivedBy, warehouseName, deliveryNote, vehicleNumber, driverName sql.NullString
		var inspectedBy, inspectionNotes, notes sql.NullString
		var warehouseID sql.NullString
		var receiptDate time.Time
		var inspectedAt sql.NullTime
		var totalQty, acceptedQty, rejectedQty float64
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &tenantIDScan, &grNumber, &purchaseOrderID, &poNumber,
			&supplierID, &supplierName, &receiptDate, &receivedBy,
			&warehouseID, &warehouseName, &status, &qualityStatus,
			&totalQty, &acceptedQty, &rejectedQty,
			&deliveryNote, &vehicleNumber, &driverName, &inspectedBy,
			&inspectedAt, &inspectionNotes, &notes, &createdAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		gr := map[string]interface{}{
			"id":                id.String(),
			"tenant_id":         tenantIDScan.String(),
			"gr_number":         grNumber,
			"purchase_order_id": purchaseOrderID.String(),
			"po_number":         poNumber,
			"supplier_id":       supplierID.String(),
			"supplier_name":     supplierName,
			"receipt_date":      receiptDate.Format("2006-01-02"),
			"status":            status,
			"quality_status":    qualityStatus,
			"total_quantity":    totalQty,
			"accepted_quantity": acceptedQty,
			"rejected_quantity": rejectedQty,
			"created_at":        createdAt,
			"updated_at":        updatedAt,
		}

		if receivedBy.Valid {
			gr["received_by"] = receivedBy.String
		}
		if warehouseID.Valid {
			gr["warehouse_id"] = warehouseID.String
		}
		if warehouseName.Valid {
			gr["warehouse_name"] = warehouseName.String
		}
		if deliveryNote.Valid {
			gr["delivery_note"] = deliveryNote.String
		}
		if vehicleNumber.Valid {
			gr["vehicle_number"] = vehicleNumber.String
		}
		if driverName.Valid {
			gr["driver_name"] = driverName.String
		}
		if inspectedBy.Valid {
			gr["inspected_by"] = inspectedBy.String
		}
		if inspectedAt.Valid {
			gr["inspected_at"] = inspectedAt.Time
		}
		if inspectionNotes.Valid {
			gr["inspection_notes"] = inspectionNotes.String
		}
		if notes.Valid {
			gr["notes"] = notes.String
		}

		receipts = append(receipts, gr)
	}

	response.Paginated(c, receipts, page, pageSize, total)
}

// CreateGoodsReceipt creates a new goods receipt
func (h *Handler) CreateGoodsReceipt(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	var input struct {
		PurchaseOrderID string `json:"purchase_order_id" binding:"required"`
		ReceiptDate     string `json:"receipt_date,omitempty"`
		ReceivedBy      string `json:"received_by,omitempty"`
		WarehouseID     string `json:"warehouse_id,omitempty"`
		WarehouseName   string `json:"warehouse_name,omitempty"`
		DeliveryNote    string `json:"delivery_note,omitempty"`
		VehicleNumber   string `json:"vehicle_number,omitempty"`
		DriverName      string `json:"driver_name,omitempty"`
		Notes           string `json:"notes,omitempty"`
		Lines           []struct {
			POLineID         string  `json:"po_line_id,omitempty"`
			ProductID        string  `json:"product_id,omitempty"`
			ProductName      string  `json:"product_name" binding:"required"`
			ProductCode      string  `json:"product_code,omitempty"`
			OrderedQuantity  float64 `json:"ordered_quantity"`
			ReceivedQuantity float64 `json:"received_quantity" binding:"required"`
			Unit             string  `json:"unit,omitempty"`
			UnitPrice        float64 `json:"unit_price,omitempty"`
			BatchNumber      string  `json:"batch_number,omitempty"`
			ExpiryDate       string  `json:"expiry_date,omitempty"`
			SerialNumbers    string  `json:"serial_numbers,omitempty"`
			StorageLocation  string  `json:"storage_location,omitempty"`
			Notes            string  `json:"notes,omitempty"`
		} `json:"lines" binding:"required,min=1"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	poID, err := uuid.Parse(input.PurchaseOrderID)
	if err != nil {
		response.BadRequest(c, "Invalid purchase order ID")
		return
	}

	// Get PO info
	var poNumber string
	var supplierID uuid.UUID
	var supplierName string
	err = h.db.QueryRow(`
		SELECT order_number, vendor_id,
			COALESCE((SELECT name FROM contacts WHERE id = vendor_id LIMIT 1), '') as supplier_name
		FROM purchase_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, poID, tenantID).Scan(&poNumber, &supplierID, &supplierName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase order")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch purchase order: "+err.Error())
		return
	}

	// Generate GR number
	var count int
	h.db.QueryRow("SELECT COUNT(*) FROM goods_receipts WHERE tenant_id = $1", tenantID).Scan(&count)
	grNumber := fmt.Sprintf("GR-%s-%04d", time.Now().Format("200601"), count+1)

	grID := uuid.New()
	now := time.Now()

	// Parse receipt date
	receiptDate := now
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

	// Insert goods receipt
	query := `
		INSERT INTO goods_receipts (
			id, tenant_id, gr_number, purchase_order_id, po_number,
			supplier_id, supplier_name, receipt_date, received_by,
			warehouse_id, warehouse_name, status, quality_status,
			delivery_note, vehicle_number, driver_name, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`

	_, err = h.db.Exec(query,
		grID, tenantID, grNumber, poID, poNumber,
		supplierID, supplierName, receiptDate, input.ReceivedBy,
		warehouseID, input.WarehouseName, GRStatusDraft, QualityStatusPending,
		input.DeliveryNote, input.VehicleNumber, input.DriverName, input.Notes,
		now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create goods receipt: "+err.Error())
		return
	}

	// Insert lines
	lineQuery := `
		INSERT INTO goods_receipt_lines (
			id, goods_receipt_id, po_line_id, product_id, product_name,
			product_code, ordered_quantity, received_quantity, unit, unit_price,
			quality_status, batch_number, expiry_date, serial_numbers,
			storage_location, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`

	var totalQty float64
	for _, lineInput := range input.Lines {
		lineID := uuid.New()

		var productID, poLineID *uuid.UUID
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

		h.db.Exec(lineQuery,
			lineID, grID, poLineID, productID, lineInput.ProductName,
			lineInput.ProductCode, lineInput.OrderedQuantity, lineInput.ReceivedQuantity, unit, lineInput.UnitPrice,
			QualityStatusPending, lineInput.BatchNumber, expiryDate, lineInput.SerialNumbers,
			lineInput.StorageLocation, lineInput.Notes, now, now,
		)

		totalQty += lineInput.ReceivedQuantity
	}

	// Update total quantity
	h.db.Exec("UPDATE goods_receipts SET total_quantity = $1, updated_at = $2 WHERE id = $3", totalQty, now, grID)

	// Return created receipt
	grResponse := map[string]interface{}{
		"id":                grID.String(),
		"tenant_id":         tenantID.String(),
		"gr_number":         grNumber,
		"purchase_order_id": poID.String(),
		"po_number":         poNumber,
		"supplier_id":       supplierID.String(),
		"supplier_name":     supplierName,
		"receipt_date":      receiptDate.Format("2006-01-02"),
		"status":            GRStatusDraft,
		"quality_status":    QualityStatusPending,
		"total_quantity":    totalQty,
		"created_at":        now,
	}

	if input.ReceivedBy != "" {
		grResponse["received_by"] = input.ReceivedBy
	}
	if warehouseID != nil {
		grResponse["warehouse_id"] = warehouseID.String()
	}
	if input.WarehouseName != "" {
		grResponse["warehouse_name"] = input.WarehouseName
	}

	response.Created(c, grResponse)
}

// GetGoodsReceipt returns a single goods receipt
func (h *Handler) GetGoodsReceipt(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	grID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid goods receipt ID")
		return
	}

	// Get receipt
	query := `
		SELECT id, tenant_id, gr_number, purchase_order_id, po_number,
			   supplier_id, supplier_name, receipt_date, received_by,
			   warehouse_id, warehouse_name, status, quality_status,
			   total_quantity, accepted_quantity, rejected_quantity,
			   delivery_note, vehicle_number, driver_name, inspected_by,
			   inspected_at, inspection_notes, notes, created_at, updated_at
		FROM goods_receipts
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`

	var id, tenantIDScan, purchaseOrderID, supplierID uuid.UUID
	var grNumber, poNumber, supplierName, status, qualityStatus string
	var receivedBy, warehouseName, deliveryNote, vehicleNumber, driverName sql.NullString
	var inspectedBy, inspectionNotes, notes sql.NullString
	var warehouseID sql.NullString
	var receiptDate time.Time
	var inspectedAt sql.NullTime
	var totalQty, acceptedQty, rejectedQty float64
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(query, grID, tenantID).Scan(
		&id, &tenantIDScan, &grNumber, &purchaseOrderID, &poNumber,
		&supplierID, &supplierName, &receiptDate, &receivedBy,
		&warehouseID, &warehouseName, &status, &qualityStatus,
		&totalQty, &acceptedQty, &rejectedQty,
		&deliveryNote, &vehicleNumber, &driverName, &inspectedBy,
		&inspectedAt, &inspectionNotes, &notes, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Goods receipt")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch goods receipt: "+err.Error())
		return
	}

	gr := map[string]interface{}{
		"id":                id.String(),
		"tenant_id":         tenantIDScan.String(),
		"gr_number":         grNumber,
		"purchase_order_id": purchaseOrderID.String(),
		"po_number":         poNumber,
		"supplier_id":       supplierID.String(),
		"supplier_name":     supplierName,
		"receipt_date":      receiptDate.Format("2006-01-02"),
		"status":            status,
		"quality_status":    qualityStatus,
		"total_quantity":    totalQty,
		"accepted_quantity": acceptedQty,
		"rejected_quantity": rejectedQty,
		"created_at":        createdAt,
		"updated_at":        updatedAt,
	}

	if receivedBy.Valid {
		gr["received_by"] = receivedBy.String
	}
	if warehouseID.Valid {
		gr["warehouse_id"] = warehouseID.String
	}
	if warehouseName.Valid {
		gr["warehouse_name"] = warehouseName.String
	}
	if deliveryNote.Valid {
		gr["delivery_note"] = deliveryNote.String
	}
	if vehicleNumber.Valid {
		gr["vehicle_number"] = vehicleNumber.String
	}
	if driverName.Valid {
		gr["driver_name"] = driverName.String
	}
	if inspectedBy.Valid {
		gr["inspected_by"] = inspectedBy.String
	}
	if inspectedAt.Valid {
		gr["inspected_at"] = inspectedAt.Time
	}
	if inspectionNotes.Valid {
		gr["inspection_notes"] = inspectionNotes.String
	}
	if notes.Valid {
		gr["notes"] = notes.String
	}

	// Get lines
	linesQuery := `
		SELECT id, po_line_id, product_id, product_name, product_code,
			   ordered_quantity, received_quantity, unit, unit_price,
			   quality_status, accepted_quantity, rejected_quantity,
			   rejection_reason, batch_number, expiry_date, serial_numbers,
			   storage_location, notes
		FROM goods_receipt_lines
		WHERE goods_receipt_id = $1
		ORDER BY created_at`

	linesRows, err := h.db.Query(linesQuery, grID)
	if err == nil {
		defer linesRows.Close()
		var lines []map[string]interface{}
		for linesRows.Next() {
			var lineID uuid.UUID
			var poLineID, productID sql.NullString
			var productName, unit, lineQualityStatus string
			var productCode, rejectionReason, batchNumber, serialNumbers, storageLocation, lineNotes sql.NullString
			var orderedQty, receivedQty, lineUnitPrice, lineAcceptedQty, lineRejectedQty float64
			var expiryDate sql.NullTime

			err := linesRows.Scan(
				&lineID, &poLineID, &productID, &productName, &productCode,
				&orderedQty, &receivedQty, &unit, &lineUnitPrice,
				&lineQualityStatus, &lineAcceptedQty, &lineRejectedQty,
				&rejectionReason, &batchNumber, &expiryDate, &serialNumbers,
				&storageLocation, &lineNotes,
			)
			if err != nil {
				continue
			}

			line := map[string]interface{}{
				"id":                lineID.String(),
				"product_name":      productName,
				"ordered_quantity":  orderedQty,
				"received_quantity": receivedQty,
				"unit":              unit,
				"unit_price":        lineUnitPrice,
				"quality_status":    lineQualityStatus,
				"accepted_quantity": lineAcceptedQty,
				"rejected_quantity": lineRejectedQty,
			}

			if poLineID.Valid {
				line["po_line_id"] = poLineID.String
			}
			if productID.Valid {
				line["product_id"] = productID.String
			}
			if productCode.Valid {
				line["product_code"] = productCode.String
			}
			if rejectionReason.Valid {
				line["rejection_reason"] = rejectionReason.String
			}
			if batchNumber.Valid {
				line["batch_number"] = batchNumber.String
			}
			if expiryDate.Valid {
				line["expiry_date"] = expiryDate.Time.Format("2006-01-02")
			}
			if serialNumbers.Valid {
				line["serial_numbers"] = serialNumbers.String
			}
			if storageLocation.Valid {
				line["storage_location"] = storageLocation.String
			}
			if lineNotes.Valid {
				line["notes"] = lineNotes.String
			}

			lines = append(lines, line)
		}
		gr["lines"] = lines
	}

	response.Success(c, gr)
}

// DeleteGoodsReceipt soft deletes a goods receipt
func (h *Handler) DeleteGoodsReceipt(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	grID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid goods receipt ID")
		return
	}

	// Check status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM goods_receipts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", grID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Goods receipt")
		return
	}
	if currentStatus != GRStatusDraft {
		response.BadRequest(c, "Only draft goods receipts can be deleted")
		return
	}

	result, err := h.db.Exec(
		"UPDATE goods_receipts SET deleted_at = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL",
		time.Now(), time.Now(), grID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete goods receipt")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Goods receipt")
		return
	}

	response.NoContent(c)
}

// InspectGoodsReceipt performs quality inspection on a goods receipt
func (h *Handler) InspectGoodsReceipt(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	grID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid goods receipt ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM goods_receipts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", grID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Goods receipt")
		return
	}
	if currentStatus == GRStatusCompleted || currentStatus == GRStatusCancelled {
		response.BadRequest(c, "Cannot inspect completed or cancelled goods receipt")
		return
	}

	var input struct {
		InspectedBy     string `json:"inspected_by,omitempty"`
		InspectionNotes string `json:"inspection_notes,omitempty"`
		Lines           []struct {
			LineID           string  `json:"line_id" binding:"required"`
			AcceptedQuantity float64 `json:"accepted_quantity"`
			RejectedQuantity float64 `json:"rejected_quantity"`
			RejectionReason  string  `json:"rejection_reason,omitempty"`
		} `json:"lines" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	now := time.Now()
	var totalAccepted, totalRejected float64
	var allPassed, allFailed bool = true, true

	// Update line inspections
	for _, lineInspection := range input.Lines {
		lineID, err := uuid.Parse(lineInspection.LineID)
		if err != nil {
			continue
		}

		lineQualityStatus := QualityStatusPassed
		if lineInspection.RejectedQuantity > 0 {
			if lineInspection.AcceptedQuantity > 0 {
				lineQualityStatus = QualityStatusPartial
				allPassed = false
			} else {
				lineQualityStatus = QualityStatusFailed
				allPassed = false
			}
		}
		if lineInspection.AcceptedQuantity > 0 {
			allFailed = false
		}

		h.db.Exec(`
			UPDATE goods_receipt_lines
			SET accepted_quantity = $1, rejected_quantity = $2, quality_status = $3, rejection_reason = $4, updated_at = $5
			WHERE id = $6`,
			lineInspection.AcceptedQuantity, lineInspection.RejectedQuantity, lineQualityStatus, lineInspection.RejectionReason, now, lineID,
		)

		totalAccepted += lineInspection.AcceptedQuantity
		totalRejected += lineInspection.RejectedQuantity
	}

	// Determine overall quality status
	overallQuality := QualityStatusPartial
	if allPassed {
		overallQuality = QualityStatusPassed
	} else if allFailed {
		overallQuality = QualityStatusFailed
	}

	// Update receipt
	h.db.Exec(`
		UPDATE goods_receipts
		SET status = $1, quality_status = $2, inspected_by = $3, inspected_at = $4,
			inspection_notes = $5, accepted_quantity = $6, rejected_quantity = $7, updated_at = $8
		WHERE id = $9 AND tenant_id = $10`,
		GRStatusPending, overallQuality, input.InspectedBy, now,
		input.InspectionNotes, totalAccepted, totalRejected, now,
		grID, tenantID,
	)

	h.GetGoodsReceipt(c)
}

// CompleteGoodsReceipt completes a goods receipt and updates PO
func (h *Handler) CompleteGoodsReceipt(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	grID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid goods receipt ID")
		return
	}

	// Get receipt with status check
	var currentStatus, qualityStatus string
	var purchaseOrderID uuid.UUID
	err = h.db.QueryRow(`
		SELECT status, quality_status, purchase_order_id
		FROM goods_receipts
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, grID, tenantID).Scan(&currentStatus, &qualityStatus, &purchaseOrderID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Goods receipt")
		return
	}
	if currentStatus == GRStatusCompleted {
		response.BadRequest(c, "Goods receipt already completed")
		return
	}
	if qualityStatus == QualityStatusPending {
		response.BadRequest(c, "Quality inspection required before completion")
		return
	}

	now := time.Now()

	// Update receipt status
	_, err = h.db.Exec("UPDATE goods_receipts SET status = $1, updated_at = $2 WHERE id = $3", GRStatusCompleted, now, grID)
	if err != nil {
		response.InternalError(c, "Failed to complete goods receipt")
		return
	}

	// Get GR lines and update PO received quantities
	grLinesQuery := `
		SELECT po_line_id, accepted_quantity
		FROM goods_receipt_lines
		WHERE goods_receipt_id = $1 AND po_line_id IS NOT NULL`

	linesRows, err := h.db.Query(grLinesQuery, grID)
	if err == nil {
		defer linesRows.Close()
		for linesRows.Next() {
			var poLineID uuid.UUID
			var acceptedQty float64
			if err := linesRows.Scan(&poLineID, &acceptedQty); err != nil {
				continue
			}

			// Update PO line received quantity
			h.db.Exec(`
				UPDATE purchase_order_lines
				SET quantity_received = COALESCE(quantity_received, 0) + $1, updated_at = $2
				WHERE id = $3`,
				acceptedQty, now, poLineID,
			)
		}
	}

	// Check if PO is fully received
	var allReceived bool = true
	poLinesQuery := `
		SELECT quantity, COALESCE(quantity_received, 0)
		FROM purchase_order_lines
		WHERE purchase_order_id = $1`

	poLinesRows, err := h.db.Query(poLinesQuery, purchaseOrderID)
	if err == nil {
		defer poLinesRows.Close()
		for poLinesRows.Next() {
			var ordered, received float64
			if err := poLinesRows.Scan(&ordered, &received); err != nil {
				continue
			}
			if received < ordered {
				allReceived = false
				break
			}
		}
	}

	// Update PO status
	newPOStatus := "partial"
	if allReceived {
		newPOStatus = "received"
	}
	h.db.Exec("UPDATE purchase_orders SET status = $1, updated_at = $2 WHERE id = $3", newPOStatus, now, purchaseOrderID)

	h.GetGoodsReceipt(c)
}

// CancelGoodsReceipt cancels a goods receipt
func (h *Handler) CancelGoodsReceipt(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	grID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid goods receipt ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM goods_receipts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", grID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Goods receipt")
		return
	}
	if currentStatus == GRStatusCompleted {
		response.BadRequest(c, "Cannot cancel completed goods receipt")
		return
	}

	_, err = h.db.Exec("UPDATE goods_receipts SET status = $1, updated_at = $2 WHERE id = $3", GRStatusCancelled, time.Now(), grID)
	if err != nil {
		response.InternalError(c, "Failed to cancel goods receipt")
		return
	}

	h.GetGoodsReceipt(c)
}
