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
	GRStatusDraft      = "draft"
	GRStatusPending    = "pending"
	GRStatusInspecting = "inspecting"
	GRStatusCompleted  = "completed"
	GRStatusCancelled  = "cancelled"
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
		h.log.Error("Failed to fetch goods receipts", "error", err)
		response.InternalError(c, "Failed to fetch goods receipts")
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
		ReceivedBy      string `json:"received_by" binding:"required"`
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
		h.log.Error("Invalid input for goods receipt", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Log input for debugging
	fmt.Printf("CreateGoodsReceipt input: PO=%s, ReceivedBy=%s, Lines=%d\n", input.PurchaseOrderID, input.ReceivedBy, len(input.Lines))

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
		fmt.Printf("Error fetching PO: %v\n", err)
		h.log.Error("Failed to fetch purchase order", "error", err)
		response.InternalError(c, "Failed to fetch purchase order")
		return
	}
	fmt.Printf("PO found: number=%s, supplierID=%s, supplierName=%s\n", poNumber, supplierID, supplierName)

	// Generate GR number
	var count int
	h.db.QueryRow("SELECT COUNT(*) FROM goods_receipts WHERE tenant_id = $1", tenantID).Scan(&count)
	grNumber := fmt.Sprintf("GR%05d", count+1)

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

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Insert goods receipt
	query := `
		INSERT INTO goods_receipts (
			id, tenant_id, organization_id, gr_number, purchase_order_id, po_number,
			supplier_id, supplier_name, receipt_date, received_by,
			warehouse_id, warehouse_name, status, quality_status,
			delivery_note, vehicle_number, driver_name, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`

	fmt.Printf("Inserting GR: id=%s, warehouseID=%v, receivedBy=%s\n", grID, warehouseID, input.ReceivedBy)
	_, err = h.db.Exec(query,
		grID, tenantID, orgIDPtr, grNumber, poID, poNumber,
		supplierID, supplierName, receiptDate, input.ReceivedBy,
		warehouseID, input.WarehouseName, GRStatusPending, QualityStatusPending,
		input.DeliveryNote, input.VehicleNumber, input.DriverName, input.Notes,
		now, now,
	)
	if err != nil {
		fmt.Printf("Error inserting GR: %v\n", err)
		h.log.Error("Failed to create goods receipt", "error", err)
		response.InternalError(c, "Failed to create goods receipt")
		return
	}
	fmt.Printf("GR created successfully: %s\n", grID)

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
		h.log.Error("Failed to fetch goods receipt", "error", err)
		response.InternalError(c, "Failed to fetch goods receipt")
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
		h.log.Error("Invalid input for quality inspection", "error", err)
		response.BadRequest(c, "Invalid input")
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

	// Update receipt - set status to "inspecting" (ready to be completed)
	h.db.Exec(`
		UPDATE goods_receipts
		SET status = $1, quality_status = $2, inspected_by = $3, inspected_at = $4,
			inspection_notes = $5, accepted_quantity = $6, rejected_quantity = $7, updated_at = $8
		WHERE id = $9 AND tenant_id = $10`,
		GRStatusInspecting, overallQuality, input.InspectedBy, now,
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

	// ============================================
	// UPDATE INVENTORY FROM GOODS RECEIPT
	// ============================================

	// Get warehouse ID from the goods receipt
	var grWarehouseID sql.NullString
	h.db.QueryRow("SELECT warehouse_id FROM goods_receipts WHERE id = $1", grID).Scan(&grWarehouseID)

	// Get GR lines with product info for inventory update (including batch/expiry for lot creation)
	grLinesForInventory, err := h.db.Query(`
		SELECT grl.product_id, grl.accepted_quantity, grl.unit_price,
		       COALESCE(grl.batch_number, ''), grl.expiry_date
		FROM goods_receipt_lines grl
		WHERE grl.goods_receipt_id = $1 AND grl.product_id IS NOT NULL AND grl.accepted_quantity > 0
	`, grID)

	if err == nil {
		defer grLinesForInventory.Close()

		for grLinesForInventory.Next() {
			var productID uuid.UUID
			var acceptedQty, unitPrice float64
			var batchNumber string
			var expiryDate sql.NullTime

			if err := grLinesForInventory.Scan(&productID, &acceptedQty, &unitPrice, &batchNumber, &expiryDate); err != nil {
				continue
			}

			// Skip if no warehouse specified
			if !grWarehouseID.Valid || grWarehouseID.String == "" {
				continue
			}

			warehouseID, err := uuid.Parse(grWarehouseID.String)
			if err != nil {
				continue
			}

			// 1. Get or create inventory record for this product+warehouse
			var inventoryID uuid.UUID
			err = h.db.QueryRow(`
				SELECT id FROM inventory
				WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
			`, tenantID, productID, warehouseID).Scan(&inventoryID)

			if err == sql.ErrNoRows {
				// Create new inventory record (quantity_available and total_value are generated columns)
				inventoryID = uuid.New()
				h.db.Exec(`
					INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at)
					VALUES ($1, $2, $3, $4, 0, 0, $5, $6, $6)
				`, inventoryID, tenantID, productID, warehouseID, unitPrice, now)
			}

			// 2. Update inventory quantity
			// Note: quantity_available and total_value are GENERATED columns - don't update them directly
			_, err = h.db.Exec(`
				UPDATE inventory
				SET quantity_on_hand = quantity_on_hand + $1,
					unit_cost = $2,
					last_movement_date = $3,
					updated_at = $3
				WHERE id = $4
			`, acceptedQty, unitPrice, now, inventoryID)
			if err != nil {
				h.log.Error("Failed to update inventory", "error", err, "inventory_id", inventoryID)
			}

			// 3. Create inventory transaction for audit trail
			txID := uuid.New()
			h.db.Exec(`
				INSERT INTO inventory_transactions (
					id, tenant_id, inventory_id, transaction_type, quantity,
					unit_cost, total_cost, reference_type, reference_id,
					reason, transaction_date, created_at
				) VALUES ($1, $2, $3, 'receipt', $4, $5, $6, 'goods_receipt', $7, 'Goods Receipt', $8, $8)
			`, txID, tenantID, inventoryID, acceptedQty, unitPrice, acceptedQty*unitPrice, grID, now)

			// 4. Auto-create inventory lot record
			lotNumber := batchNumber
			if lotNumber == "" {
				lotNumber = h.generateLotNumber(tenantID)
			}
			lotID := uuid.New()
			var expDate *time.Time
			if expiryDate.Valid {
				expDate = &expiryDate.Time
			}
			// Get vendor from PO
			var vendorID *uuid.UUID
			var poID *uuid.UUID
			if purchaseOrderID != uuid.Nil {
				poID = &purchaseOrderID
				var vid uuid.UUID
				if err := h.db.QueryRow("SELECT vendor_id FROM purchase_orders WHERE id = $1", purchaseOrderID).Scan(&vid); err == nil {
					vendorID = &vid
				}
			}
			h.db.Exec(`
				INSERT INTO inventory_lots (
					id, tenant_id, product_id, warehouse_id, lot_number,
					received_date, expiry_date, initial_quantity, remaining_quantity,
					unit_cost, vendor_id, purchase_order_id, status, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9, $10, $11, 'available', $12, $12)
			`, lotID, tenantID, productID, warehouseID, lotNumber,
				now, expDate, acceptedQty, unitPrice, vendorID, poID, now)
		}
	}

	// ============================================
	// CREATE JOURNAL ENTRY FOR GOODS RECEIPT
	// ============================================
	func() {
		// Get GR details for JE
		var grNumber string
		var grOrgID sql.NullString
		var receiptDate time.Time
		h.db.QueryRow(`SELECT gr_number, organization_id, receipt_date FROM goods_receipts WHERE id = $1`, grID).Scan(&grNumber, &grOrgID, &receiptDate)

		var orgIDPtr *uuid.UUID
		if grOrgID.Valid {
			if parsedOrgID, err := uuid.Parse(grOrgID.String); err == nil {
				orgIDPtr = &parsedOrgID
			}
		}

		// Get GR lines for JE amounts (per product for category accounts)
		type grJELine struct {
			productID uuid.UUID
			amount    float64
		}
		var jeLines []grJELine
		var totalAmount float64

		jeLineRows, err := h.db.Query(`
			SELECT product_id, accepted_quantity, unit_price
			FROM goods_receipt_lines
			WHERE goods_receipt_id = $1 AND product_id IS NOT NULL AND accepted_quantity > 0
		`, grID)
		if err == nil {
			defer jeLineRows.Close()
			for jeLineRows.Next() {
				var productID uuid.UUID
				var qty, price float64
				if err := jeLineRows.Scan(&productID, &qty, &price); err == nil {
					amt := qty * price
					jeLines = append(jeLines, grJELine{productID: productID, amount: amt})
					totalAmount += amt
				}
			}
		}

		if totalAmount <= 0 || len(jeLines) == 0 {
			return
		}

		// Look up journal
		var journalID uuid.UUID
		var nextNumber int
		err = h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('STOCK','INVENTORY','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'INVENTORY' THEN 1 ELSE 2 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNumber)
		if err != nil {
			h.log.Error("No journal found for goods receipt JE", "error", err)
			return
		}

		entryNumber := fmt.Sprintf("GR%06d", nextNumber)
		journalEntryID := uuid.New()
		description := "Goods Receipt: " + grNumber

		// Insert journal entry
		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'goods_receipt', $9, 1.0, $10, $10, 'posted', $11, $11)`,
			journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, receiptDate, grNumber, description,
			grID.String(), totalAmount, now,
		)
		if err != nil {
			h.log.Error("Failed to create GR journal entry", "error", err)
			return
		}

		// Insert JE lines per product (debit stock valuation, credit stock interim receipt)
		lineNumber := 1
		for _, line := range jeLines {
			ca := getCategoryAccounts(h.db, tenantID, orgIDPtr, line.productID)
			// Prefer inventory-type account (1310/1330/1340), fallback to category valuation, then generic 1300
			invAcct := getInventoryAccountByType(h.db, tenantID, orgIDPtr, line.productID)
			var debitAcct uuid.UUID
			if invAcct != uuid.Nil {
				debitAcct = invAcct
			} else {
				debitAcct = ca.StockValuationAccountID
			}
			creditAcct := ca.StockInputAccountID

			if debitAcct == uuid.Nil {
				debitAcct = findAccount(h.db, tenantID, orgIDPtr, "inventory", "1300")
			}
			if creditAcct == uuid.Nil {
				creditAcct = findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "2000")
			}
			if debitAcct == uuid.Nil || creditAcct == uuid.Nil {
				continue
			}

			// Debit: Stock Valuation
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, 0, 1.0, $7)`,
				uuid.New(), journalEntryID, lineNumber, debitAcct, "Stock Valuation", line.amount, now,
			)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", line.amount, now, debitAcct)
			lineNumber++

			// Credit: Accounts Payable / Stock Interim Receipt
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, 0, $6, 1.0, $7)`,
				uuid.New(), journalEntryID, lineNumber, creditAcct, "Accounts Payable", line.amount, now,
			)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", line.amount, now, creditAcct)
			lineNumber++
		}

		// Update journal next_number
		h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
	}()

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
