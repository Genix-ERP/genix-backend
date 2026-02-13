package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// INTERCOMPANY TRANSFER HANDLERS
// =====================================================

// ListIntercompanyTransfers returns a paginated list of IC transfers
func (h *Handler) ListIntercompanyTransfers(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	fromOrgID := c.Query("from_organization_id")
	toOrgID := c.Query("to_organization_id")
	status := c.Query("status")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// Build query
	baseQuery := `
		SELECT ict.id, ict.tenant_id, ict.transfer_number, ict.transfer_type,
			   ict.from_organization_id, ict.to_organization_id,
			   ict.from_warehouse_id, ict.to_warehouse_id,
			   ict.transfer_date, ict.expected_date, ict.received_date,
			   ict.status, ict.pricing_method, ict.markup_percent,
			   ict.total_amount, ict.total_quantity,
			   ict.reason, ict.notes, ict.created_at,
			   from_org.name as from_organization_name,
			   to_org.name as to_organization_name,
			   from_wh.name as from_warehouse_name,
			   to_wh.name as to_warehouse_name
		FROM intercompany_transfers ict
		JOIN organizations from_org ON ict.from_organization_id = from_org.id
		JOIN organizations to_org ON ict.to_organization_id = to_org.id
		LEFT JOIN warehouses from_wh ON ict.from_warehouse_id = from_wh.id
		LEFT JOIN warehouses to_wh ON ict.to_warehouse_id = to_wh.id
		WHERE ict.tenant_id = $1 AND ict.deleted_at IS NULL
	`
	countQuery := `
		SELECT COUNT(*) FROM intercompany_transfers ict
		WHERE ict.tenant_id = $1 AND ict.deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	argCount := 1

	// Note: IC transfers are cross-organization, so we don't filter by current org
	// Instead, we show transfers where user's org is either from or to

	if fromOrgID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND ict.from_organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND ict.from_organization_id = $%d", argCount)
		args = append(args, fromOrgID)
	}

	if toOrgID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND ict.to_organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND ict.to_organization_id = $%d", argCount)
		args = append(args, toOrgID)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND ict.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND ict.status = $%d", argCount)
		args = append(args, status)
	}

	if dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND ict.transfer_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND ict.transfer_date >= $%d", argCount)
		args = append(args, dateFrom)
	}

	if dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND ict.transfer_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND ict.transfer_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (ict.transfer_number ILIKE $%d OR from_org.name ILIKE $%d OR to_org.name ILIKE $%d)", argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += fmt.Sprintf(" AND (ict.transfer_number ILIKE $%d)", argCount)
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count IC transfers", "error", err)
		response.InternalError(c, "Failed to list transfers")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY ict.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list IC transfers", "error", err)
		response.InternalError(c, "Failed to list transfers")
		return
	}
	defer rows.Close()

	transfers := make([]*entity.IntercompanyTransferResponse, 0)
	for rows.Next() {
		var t entity.IntercompanyTransferResponse
		var fromWarehouseID, toWarehouseID sql.NullString
		var fromWarehouseName, toWarehouseName sql.NullString
		var expectedDate, receivedDate sql.NullTime
		var reason, notes sql.NullString
		var transferDate time.Time

		err := rows.Scan(
			&t.ID, &tenantID, &t.TransferNumber, &t.TransferType,
			&t.FromOrganizationID, &t.ToOrganizationID,
			&fromWarehouseID, &toWarehouseID,
			&transferDate, &expectedDate, &receivedDate,
			&t.Status, &t.PricingMethod, &t.MarkupPercent,
			&t.TotalAmount, &t.TotalQuantity,
			&reason, &notes, &t.CreatedAt,
			&t.FromOrganizationName, &t.ToOrganizationName,
			&fromWarehouseName, &toWarehouseName,
		)
		if err != nil {
			h.log.Error("Failed to scan IC transfer", "error", err)
			continue
		}

		t.TransferDate = transferDate.Format("2006-01-02")
		if expectedDate.Valid {
			ed := expectedDate.Time.Format("2006-01-02")
			t.ExpectedDate = &ed
		}
		if receivedDate.Valid {
			rd := receivedDate.Time.Format("2006-01-02")
			t.ReceivedDate = &rd
		}
		if fromWarehouseID.Valid {
			fwid := uuid.MustParse(fromWarehouseID.String)
			t.FromWarehouseID = &fwid
		}
		if toWarehouseID.Valid {
			twid := uuid.MustParse(toWarehouseID.String)
			t.ToWarehouseID = &twid
		}
		if fromWarehouseName.Valid {
			t.FromWarehouseName = fromWarehouseName.String
		}
		if toWarehouseName.Valid {
			t.ToWarehouseName = toWarehouseName.String
		}
		if reason.Valid {
			t.Reason = &reason.String
		}
		if notes.Valid {
			t.Notes = &notes.String
		}

		transfers = append(transfers, &t)
	}

	response.Success(c, gin.H{
		"data":  transfers,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// CreateIntercompanyTransfer creates a new IC transfer
func (h *Handler) CreateIntercompanyTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateIntercompanyTransferInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Validate organizations exist and are different
	fromOrgID, err := uuid.Parse(input.FromOrganizationID)
	if err != nil {
		response.BadRequest(c, "Invalid from organization ID")
		return
	}
	toOrgID, err := uuid.Parse(input.ToOrganizationID)
	if err != nil {
		response.BadRequest(c, "Invalid to organization ID")
		return
	}

	if fromOrgID == toOrgID {
		response.BadRequest(c, "From and To organizations must be different")
		return
	}

	fromWarehouseID, err := uuid.Parse(input.FromWarehouseID)
	if err != nil {
		response.BadRequest(c, "Invalid from warehouse ID")
		return
	}
	toWarehouseID, err := uuid.Parse(input.ToWarehouseID)
	if err != nil {
		response.BadRequest(c, "Invalid to warehouse ID")
		return
	}

	transferDate, err := time.Parse("2006-01-02", input.TransferDate)
	if err != nil {
		response.BadRequest(c, "Invalid transfer date format (use YYYY-MM-DD)")
		return
	}

	// Generate transfer number
	var nextNum int
	err = h.db.QueryRow("SELECT nextval('intercompany_transfer_seq')").Scan(&nextNum)
	if err != nil {
		h.log.Error("Failed to get next transfer number", "error", err)
		response.InternalError(c, "Failed to generate transfer number")
		return
	}
	transferNumber := fmt.Sprintf("ICT-%06d", nextNum)

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create transfer")
		return
	}
	defer tx.Rollback()

	// Insert transfer
	transferID := uuid.New()
	pricingMethod := "cost"
	if input.PricingMethod != "" {
		pricingMethod = input.PricingMethod
	}

	var expectedDate interface{}
	if input.ExpectedDate != "" {
		ed, err := time.Parse("2006-01-02", input.ExpectedDate)
		if err == nil {
			expectedDate = ed
		}
	}

	var currencyID interface{}
	if input.CurrencyID != "" {
		cid, err := uuid.Parse(input.CurrencyID)
		if err == nil {
			currencyID = cid
		}
	}

	_, err = tx.Exec(`
		INSERT INTO intercompany_transfers (
			id, tenant_id, transfer_number, transfer_type,
			from_organization_id, to_organization_id,
			from_warehouse_id, to_warehouse_id,
			transfer_date, expected_date,
			status, pricing_method, markup_percent,
			currency_id, reason, notes, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`,
		transferID, tenantID, transferNumber, "product",
		fromOrgID, toOrgID,
		fromWarehouseID, toWarehouseID,
		transferDate, expectedDate,
		"draft", pricingMethod, input.MarkupPercent,
		currencyID, nilIfEmpty(input.Reason), nilIfEmpty(input.Notes), userID,
	)
	if err != nil {
		h.log.Error("Failed to insert IC transfer", "error", err)
		response.InternalError(c, "Failed to create transfer")
		return
	}

	// Insert lines
	var totalAmount, totalQuantity float64
	for _, line := range input.Lines {
		productID, err := uuid.Parse(line.ProductID)
		if err != nil {
			response.BadRequest(c, "Invalid product ID in line")
			return
		}

		// Get product cost if transfer price not specified
		var unitCost float64
		err = h.db.QueryRow(`
			SELECT COALESCE(
				(SELECT unit_cost FROM inventory WHERE product_id = $1 AND warehouse_id = $2 LIMIT 1),
				(SELECT cost_price FROM products WHERE id = $1),
				0
			)
		`, productID, fromWarehouseID).Scan(&unitCost)
		if err != nil {
			unitCost = 0
		}

		transferPrice := line.TransferPrice
		if transferPrice == 0 {
			// Calculate based on pricing method
			switch pricingMethod {
			case "cost":
				transferPrice = unitCost
			case "markup":
				transferPrice = unitCost * (1 + input.MarkupPercent/100)
			default:
				transferPrice = unitCost
			}
		}

		lineTotal := transferPrice * line.Quantity

		var fromLocID, toLocID interface{}
		if line.FromLocationID != "" {
			flid, _ := uuid.Parse(line.FromLocationID)
			fromLocID = flid
		}
		if line.ToLocationID != "" {
			tlid, _ := uuid.Parse(line.ToLocationID)
			toLocID = tlid
		}

		_, err = tx.Exec(`
			INSERT INTO intercompany_transfer_lines (
				id, intercompany_transfer_id, product_id,
				from_location_id, to_location_id,
				lot_number, serial_number,
				quantity_requested, unit_cost, transfer_price, line_total, notes
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`,
			uuid.New(), transferID, productID,
			fromLocID, toLocID,
			nilIfEmpty(line.LotNumber), nilIfEmpty(line.SerialNumber),
			line.Quantity, unitCost, transferPrice, lineTotal, nilIfEmpty(line.Notes),
		)
		if err != nil {
			h.log.Error("Failed to insert IC transfer line", "error", err)
			response.InternalError(c, "Failed to create transfer line")
			return
		}

		totalAmount += lineTotal
		totalQuantity += line.Quantity
	}

	// Update totals
	_, err = tx.Exec(`
		UPDATE intercompany_transfers
		SET total_amount = $1, total_quantity = $2
		WHERE id = $3
	`, totalAmount, totalQuantity, transferID)
	if err != nil {
		h.log.Error("Failed to update IC transfer totals", "error", err)
		response.InternalError(c, "Failed to create transfer")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create transfer")
		return
	}

	response.Created(c, gin.H{
		"id":              transferID,
		"transfer_number": transferNumber,
		"status":          "draft",
	})
}

// GetIntercompanyTransfer returns a single IC transfer with lines
func (h *Handler) GetIntercompanyTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid transfer ID")
		return
	}

	// Get transfer
	var t entity.IntercompanyTransferResponse
	var fromWarehouseID, toWarehouseID sql.NullString
	var fromWarehouseName, toWarehouseName sql.NullString
	var expectedDate, receivedDate sql.NullTime
	var reason, notes sql.NullString
	var transferDate time.Time

	err = h.db.QueryRow(`
		SELECT ict.id, ict.transfer_number, ict.transfer_type,
			   ict.from_organization_id, ict.to_organization_id,
			   ict.from_warehouse_id, ict.to_warehouse_id,
			   ict.transfer_date, ict.expected_date, ict.received_date,
			   ict.status, ict.pricing_method, ict.markup_percent,
			   ict.total_amount, ict.total_quantity,
			   ict.reason, ict.notes, ict.created_at,
			   from_org.name as from_organization_name,
			   to_org.name as to_organization_name,
			   from_wh.name as from_warehouse_name,
			   to_wh.name as to_warehouse_name
		FROM intercompany_transfers ict
		JOIN organizations from_org ON ict.from_organization_id = from_org.id
		JOIN organizations to_org ON ict.to_organization_id = to_org.id
		LEFT JOIN warehouses from_wh ON ict.from_warehouse_id = from_wh.id
		LEFT JOIN warehouses to_wh ON ict.to_warehouse_id = to_wh.id
		WHERE ict.id = $1 AND ict.tenant_id = $2 AND ict.deleted_at IS NULL
	`, id, tenantID).Scan(
		&t.ID, &t.TransferNumber, &t.TransferType,
		&t.FromOrganizationID, &t.ToOrganizationID,
		&fromWarehouseID, &toWarehouseID,
		&transferDate, &expectedDate, &receivedDate,
		&t.Status, &t.PricingMethod, &t.MarkupPercent,
		&t.TotalAmount, &t.TotalQuantity,
		&reason, &notes, &t.CreatedAt,
		&t.FromOrganizationName, &t.ToOrganizationName,
		&fromWarehouseName, &toWarehouseName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transfer not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC transfer", "error", err)
		response.InternalError(c, "Failed to get transfer")
		return
	}

	t.TransferDate = transferDate.Format("2006-01-02")
	if expectedDate.Valid {
		ed := expectedDate.Time.Format("2006-01-02")
		t.ExpectedDate = &ed
	}
	if receivedDate.Valid {
		rd := receivedDate.Time.Format("2006-01-02")
		t.ReceivedDate = &rd
	}
	if fromWarehouseID.Valid {
		fwid := uuid.MustParse(fromWarehouseID.String)
		t.FromWarehouseID = &fwid
	}
	if toWarehouseID.Valid {
		twid := uuid.MustParse(toWarehouseID.String)
		t.ToWarehouseID = &twid
	}
	if fromWarehouseName.Valid {
		t.FromWarehouseName = fromWarehouseName.String
	}
	if toWarehouseName.Valid {
		t.ToWarehouseName = toWarehouseName.String
	}
	if reason.Valid {
		t.Reason = &reason.String
	}
	if notes.Valid {
		t.Notes = &notes.String
	}

	// Get lines
	rows, err := h.db.Query(`
		SELECT l.id, l.product_id, p.code, p.name,
			   l.lot_number, l.quantity_requested, l.quantity_shipped, l.quantity_received,
			   l.unit_cost, l.transfer_price, l.line_total, l.notes
		FROM intercompany_transfer_lines l
		JOIN products p ON l.product_id = p.id
		WHERE l.intercompany_transfer_id = $1
		ORDER BY l.created_at
	`, id)
	if err != nil {
		h.log.Error("Failed to get IC transfer lines", "error", err)
	} else {
		defer rows.Close()
		lines := make([]entity.IntercompanyTransferLineResponse, 0)
		for rows.Next() {
			var line entity.IntercompanyTransferLineResponse
			var lotNumber, lineNotes sql.NullString
			err := rows.Scan(
				&line.ID, &line.ProductID, &line.ProductCode, &line.ProductName,
				&lotNumber, &line.QuantityRequested, &line.QuantityShipped, &line.QuantityReceived,
				&line.UnitCost, &line.TransferPrice, &line.LineTotal, &lineNotes,
			)
			if err != nil {
				continue
			}
			if lotNumber.Valid {
				line.LotNumber = &lotNumber.String
			}
			if lineNotes.Valid {
				line.Notes = &lineNotes.String
			}
			lines = append(lines, line)
		}
		t.Lines = lines
	}

	response.Success(c, t)
}

// UpdateIntercompanyTransfer updates an IC transfer (only in draft status)
func (h *Handler) UpdateIntercompanyTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid transfer ID")
		return
	}

	var input entity.UpdateIntercompanyTransferInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow(`
		SELECT status FROM intercompany_transfers
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transfer not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC transfer status", "error", err)
		response.InternalError(c, "Failed to update transfer")
		return
	}

	if currentStatus != "draft" {
		response.BadRequest(c, "Can only update transfers in draft status")
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.TransferDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("transfer_date = $%d", argCount))
		args = append(args, *input.TransferDate)
	}
	if input.ExpectedDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("expected_date = $%d", argCount))
		args = append(args, *input.ExpectedDate)
	}
	if input.PricingMethod != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("pricing_method = $%d", argCount))
		args = append(args, *input.PricingMethod)
	}
	if input.MarkupPercent != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("markup_percent = $%d", argCount))
		args = append(args, *input.MarkupPercent)
	}
	if input.Reason != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("reason = $%d", argCount))
		args = append(args, *input.Reason)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	updates = append(updates, "updated_at = CURRENT_TIMESTAMP")
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE intercompany_transfers SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update IC transfer", "error", err)
		response.InternalError(c, "Failed to update transfer")
		return
	}

	response.Success(c, gin.H{"message": "Transfer updated successfully"})
}

// DeleteIntercompanyTransfer soft deletes an IC transfer (only in draft status)
func (h *Handler) DeleteIntercompanyTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid transfer ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow(`
		SELECT status FROM intercompany_transfers
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transfer not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC transfer status", "error", err)
		response.InternalError(c, "Failed to delete transfer")
		return
	}

	if currentStatus != "draft" {
		response.BadRequest(c, "Can only delete transfers in draft status")
		return
	}

	_, err = h.db.Exec(`
		UPDATE intercompany_transfers
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete IC transfer", "error", err)
		response.InternalError(c, "Failed to delete transfer")
		return
	}

	response.Success(c, gin.H{"message": "Transfer deleted successfully"})
}

// ApproveIntercompanyTransfer approves an IC transfer
func (h *Handler) ApproveIntercompanyTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid transfer ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow(`
		SELECT status FROM intercompany_transfers
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transfer not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC transfer status", "error", err)
		response.InternalError(c, "Failed to approve transfer")
		return
	}

	if currentStatus != "draft" && currentStatus != "pending_approval" {
		response.BadRequest(c, "Can only approve transfers in draft or pending_approval status")
		return
	}

	_, err = h.db.Exec(`
		UPDATE intercompany_transfers
		SET status = 'approved', approved_by = $1, approved_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND tenant_id = $3
	`, userID, id, tenantID)
	if err != nil {
		h.log.Error("Failed to approve IC transfer", "error", err)
		response.InternalError(c, "Failed to approve transfer")
		return
	}

	response.Success(c, gin.H{"message": "Transfer approved successfully", "status": "approved"})
}

// ShipIntercompanyTransfer marks transfer as shipped
func (h *Handler) ShipIntercompanyTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid transfer ID")
		return
	}

	// Check current status and get warehouse info
	var currentStatus string
	var fromWarehouseID uuid.UUID
	err = h.db.QueryRow(`
		SELECT status, from_warehouse_id FROM intercompany_transfers
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus, &fromWarehouseID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transfer not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC transfer status", "error", err)
		response.InternalError(c, "Failed to ship transfer")
		return
	}

	if currentStatus != "approved" {
		response.BadRequest(c, "Can only ship approved transfers")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to ship transfer")
		return
	}
	defer tx.Rollback()

	// Update shipped quantities to match requested
	_, err = tx.Exec(`
		UPDATE intercompany_transfer_lines
		SET quantity_shipped = quantity_requested, updated_at = CURRENT_TIMESTAMP
		WHERE intercompany_transfer_id = $1
	`, id)
	if err != nil {
		h.log.Error("Failed to update shipped quantities", "error", err)
		response.InternalError(c, "Failed to ship transfer")
		return
	}

	// Deduct inventory from source warehouse
	rows, err := tx.Query(`
		SELECT product_id, quantity_requested FROM intercompany_transfer_lines
		WHERE intercompany_transfer_id = $1
	`, id)
	if err != nil {
		h.log.Error("Failed to get transfer lines", "error", err)
		response.InternalError(c, "Failed to ship transfer")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var productID uuid.UUID
		var quantity float64
		if err := rows.Scan(&productID, &quantity); err != nil {
			continue
		}

		// Deduct from inventory
		_, err = tx.Exec(`
			UPDATE inventory
			SET quantity_on_hand = quantity_on_hand - $1,
				quantity_available = quantity_available - $1,
				last_movement_date = CURRENT_TIMESTAMP,
				updated_at = CURRENT_TIMESTAMP
			WHERE product_id = $2 AND warehouse_id = $3
		`, quantity, productID, fromWarehouseID)
		if err != nil {
			h.log.Error("Failed to deduct inventory", "error", err, "product_id", productID)
		}

		// Create inventory transaction
		_, err = tx.Exec(`
			INSERT INTO inventory_transactions (
				id, tenant_id, inventory_id, transaction_type,
				reference_type, reference_id, quantity,
				from_warehouse_id, reason, transaction_date, created_by
			)
			SELECT gen_random_uuid(), $1, i.id, 'issue',
				'intercompany_transfer', $2, $3,
				$4, 'Inter-company transfer shipment', CURRENT_TIMESTAMP, $5
			FROM inventory i
			WHERE i.product_id = $6 AND i.warehouse_id = $4
			LIMIT 1
		`, tenantID, id, quantity, fromWarehouseID, userID, productID)
		if err != nil {
			h.log.Error("Failed to create inventory transaction", "error", err)
		}
	}

	// Update transfer status
	_, err = tx.Exec(`
		UPDATE intercompany_transfers
		SET status = 'in_transit', shipped_by = $1, shipped_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND tenant_id = $3
	`, userID, id, tenantID)
	if err != nil {
		h.log.Error("Failed to update IC transfer status", "error", err)
		response.InternalError(c, "Failed to ship transfer")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to ship transfer")
		return
	}

	response.Success(c, gin.H{"message": "Transfer shipped successfully", "status": "in_transit"})
}

// ReceiveIntercompanyTransfer marks transfer as received and adds inventory
func (h *Handler) ReceiveIntercompanyTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid transfer ID")
		return
	}

	// Check current status and get warehouse info
	var currentStatus string
	var toWarehouseID uuid.UUID
	var toOrgID uuid.UUID
	err = h.db.QueryRow(`
		SELECT status, to_warehouse_id, to_organization_id FROM intercompany_transfers
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus, &toWarehouseID, &toOrgID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transfer not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC transfer status", "error", err)
		response.InternalError(c, "Failed to receive transfer")
		return
	}

	if currentStatus != "in_transit" {
		response.BadRequest(c, "Can only receive in_transit transfers")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to receive transfer")
		return
	}
	defer tx.Rollback()

	// Update received quantities to match shipped
	_, err = tx.Exec(`
		UPDATE intercompany_transfer_lines
		SET quantity_received = quantity_shipped, updated_at = CURRENT_TIMESTAMP
		WHERE intercompany_transfer_id = $1
	`, id)
	if err != nil {
		h.log.Error("Failed to update received quantities", "error", err)
		response.InternalError(c, "Failed to receive transfer")
		return
	}

	// Add inventory to destination warehouse
	rows, err := tx.Query(`
		SELECT product_id, quantity_shipped, transfer_price FROM intercompany_transfer_lines
		WHERE intercompany_transfer_id = $1
	`, id)
	if err != nil {
		h.log.Error("Failed to get transfer lines", "error", err)
		response.InternalError(c, "Failed to receive transfer")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var productID uuid.UUID
		var quantity, transferPrice float64
		if err := rows.Scan(&productID, &quantity, &transferPrice); err != nil {
			continue
		}

		// Check if inventory record exists
		var existingID uuid.UUID
		err = tx.QueryRow(`
			SELECT id FROM inventory
			WHERE product_id = $1 AND warehouse_id = $2
		`, productID, toWarehouseID).Scan(&existingID)

		if err == sql.ErrNoRows {
			// Create new inventory record
			_, err = tx.Exec(`
				INSERT INTO inventory (
					id, tenant_id, product_id, warehouse_id,
					quantity_on_hand, quantity_available, unit_cost, total_value,
					last_movement_date
				) VALUES ($1, $2, $3, $4, $5, $5, $6, $7, CURRENT_TIMESTAMP)
			`, uuid.New(), tenantID, productID, toWarehouseID, quantity, transferPrice, quantity*transferPrice)
		} else if err == nil {
			// Update existing inventory record
			_, err = tx.Exec(`
				UPDATE inventory
				SET quantity_on_hand = quantity_on_hand + $1,
					quantity_available = quantity_available + $1,
					total_value = total_value + $2,
					last_movement_date = CURRENT_TIMESTAMP,
					updated_at = CURRENT_TIMESTAMP
				WHERE id = $3
			`, quantity, quantity*transferPrice, existingID)
		}
		if err != nil {
			h.log.Error("Failed to add inventory", "error", err, "product_id", productID)
		}

		// Create inventory transaction
		_, err = tx.Exec(`
			INSERT INTO inventory_transactions (
				id, tenant_id, inventory_id, transaction_type,
				reference_type, reference_id, quantity,
				to_warehouse_id, reason, transaction_date, created_by
			)
			SELECT gen_random_uuid(), $1, i.id, 'receipt',
				'intercompany_transfer', $2, $3,
				$4, 'Inter-company transfer receipt', CURRENT_TIMESTAMP, $5
			FROM inventory i
			WHERE i.product_id = $6 AND i.warehouse_id = $4
			LIMIT 1
		`, tenantID, id, quantity, toWarehouseID, userID, productID)
		if err != nil {
			h.log.Error("Failed to create inventory transaction", "error", err)
		}
	}

	// Update transfer status
	_, err = tx.Exec(`
		UPDATE intercompany_transfers
		SET status = 'received', received_by = $1, received_at = CURRENT_TIMESTAMP,
			received_date = CURRENT_DATE, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND tenant_id = $3
	`, userID, id, tenantID)
	if err != nil {
		h.log.Error("Failed to update IC transfer status", "error", err)
		response.InternalError(c, "Failed to receive transfer")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to receive transfer")
		return
	}

	response.Success(c, gin.H{"message": "Transfer received successfully", "status": "received"})
}

// =====================================================
// INTERCOMPANY PAYMENT HANDLERS
// =====================================================

// ListIntercompanyPayments returns a paginated list of IC payments
func (h *Handler) ListIntercompanyPayments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	// Parse filters
	fromOrgID := c.Query("from_organization_id")
	toOrgID := c.Query("to_organization_id")
	status := c.Query("status")

	// Build query
	baseQuery := `
		SELECT icp.id, icp.payment_number, icp.payment_type,
			   icp.from_organization_id, icp.to_organization_id,
			   icp.amount, icp.currency_id, icp.payment_date, icp.status,
			   icp.payment_method, icp.reference, icp.description, icp.notes, icp.created_at,
			   from_org.name as from_organization_name,
			   to_org.name as to_organization_name,
			   cur.code as currency_code
		FROM intercompany_payments icp
		JOIN organizations from_org ON icp.from_organization_id = from_org.id
		JOIN organizations to_org ON icp.to_organization_id = to_org.id
		LEFT JOIN currencies cur ON icp.currency_id = cur.id
		WHERE icp.tenant_id = $1 AND icp.deleted_at IS NULL
	`
	countQuery := `
		SELECT COUNT(*) FROM intercompany_payments icp
		WHERE icp.tenant_id = $1 AND icp.deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	argCount := 1

	if fromOrgID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND icp.from_organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND icp.from_organization_id = $%d", argCount)
		args = append(args, fromOrgID)
	}

	if toOrgID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND icp.to_organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND icp.to_organization_id = $%d", argCount)
		args = append(args, toOrgID)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND icp.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND icp.status = $%d", argCount)
		args = append(args, status)
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count IC payments", "error", err)
		response.InternalError(c, "Failed to list payments")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY icp.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list IC payments", "error", err)
		response.InternalError(c, "Failed to list payments")
		return
	}
	defer rows.Close()

	payments := make([]*entity.IntercompanyPaymentResponse, 0)
	for rows.Next() {
		var p entity.IntercompanyPaymentResponse
		var currencyID sql.NullString
		var currencyCode, paymentMethod, reference, description, notes sql.NullString
		var paymentDate time.Time

		err := rows.Scan(
			&p.ID, &p.PaymentNumber, &p.PaymentType,
			&p.FromOrganizationID, &p.ToOrganizationID,
			&p.Amount, &currencyID, &paymentDate, &p.Status,
			&paymentMethod, &reference, &description, &notes, &p.CreatedAt,
			&p.FromOrganizationName, &p.ToOrganizationName,
			&currencyCode,
		)
		if err != nil {
			h.log.Error("Failed to scan IC payment", "error", err)
			continue
		}

		p.PaymentDate = paymentDate.Format("2006-01-02")
		if currencyID.Valid {
			cid := uuid.MustParse(currencyID.String)
			p.CurrencyID = &cid
		}
		if currencyCode.Valid {
			p.CurrencyCode = currencyCode.String
		}
		if paymentMethod.Valid {
			p.PaymentMethod = &paymentMethod.String
		}
		if reference.Valid {
			p.Reference = &reference.String
		}
		if description.Valid {
			p.Description = &description.String
		}
		if notes.Valid {
			p.Notes = &notes.String
		}

		payments = append(payments, &p)
	}

	response.Success(c, gin.H{
		"data":  payments,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// CreateIntercompanyPayment creates a new IC payment
func (h *Handler) CreateIntercompanyPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateIntercompanyPaymentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Validate organizations
	fromOrgID, err := uuid.Parse(input.FromOrganizationID)
	if err != nil {
		response.BadRequest(c, "Invalid from organization ID")
		return
	}
	toOrgID, err := uuid.Parse(input.ToOrganizationID)
	if err != nil {
		response.BadRequest(c, "Invalid to organization ID")
		return
	}

	if fromOrgID == toOrgID {
		response.BadRequest(c, "From and To organizations must be different")
		return
	}

	paymentDate, err := time.Parse("2006-01-02", input.PaymentDate)
	if err != nil {
		response.BadRequest(c, "Invalid payment date format (use YYYY-MM-DD)")
		return
	}

	// Generate payment number
	var nextNum int
	err = h.db.QueryRow("SELECT nextval('intercompany_payment_seq')").Scan(&nextNum)
	if err != nil {
		h.log.Error("Failed to get next payment number", "error", err)
		response.InternalError(c, "Failed to generate payment number")
		return
	}
	paymentNumber := fmt.Sprintf("ICP-%06d", nextNum)

	paymentType := "transfer"
	if input.PaymentType != "" {
		paymentType = input.PaymentType
	}

	var currencyID interface{}
	if input.CurrencyID != "" {
		cid, err := uuid.Parse(input.CurrencyID)
		if err == nil {
			currencyID = cid
		}
	}

	var relatedTransferID interface{}
	if input.RelatedTransferID != "" {
		rtid, err := uuid.Parse(input.RelatedTransferID)
		if err == nil {
			relatedTransferID = rtid
		}
	}

	paymentID := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO intercompany_payments (
			id, tenant_id, payment_number, payment_type,
			from_organization_id, to_organization_id,
			amount, currency_id, payment_date, status,
			payment_method, from_bank_account_id, to_bank_account_id,
			reference, related_transfer_id, description, notes, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`,
		paymentID, tenantID, paymentNumber, paymentType,
		fromOrgID, toOrgID,
		input.Amount, currencyID, paymentDate, "draft",
		nilIfEmpty(input.PaymentMethod), nilIfEmpty(input.FromBankAccountID), nilIfEmpty(input.ToBankAccountID),
		nilIfEmpty(input.Reference), relatedTransferID, nilIfEmpty(input.Description), nilIfEmpty(input.Notes), userID,
	)
	if err != nil {
		h.log.Error("Failed to create IC payment", "error", err)
		response.InternalError(c, "Failed to create payment")
		return
	}

	response.Created(c, gin.H{
		"id":             paymentID,
		"payment_number": paymentNumber,
		"status":         "draft",
	})
}

// GetIntercompanyPayment returns a single IC payment
func (h *Handler) GetIntercompanyPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payment ID")
		return
	}

	var p entity.IntercompanyPaymentResponse
	var currencyID sql.NullString
	var currencyCode, paymentMethod, reference, description, notes sql.NullString
	var paymentDate time.Time

	err = h.db.QueryRow(`
		SELECT icp.id, icp.payment_number, icp.payment_type,
			   icp.from_organization_id, icp.to_organization_id,
			   icp.amount, icp.currency_id, icp.payment_date, icp.status,
			   icp.payment_method, icp.reference, icp.description, icp.notes, icp.created_at,
			   from_org.name as from_organization_name,
			   to_org.name as to_organization_name,
			   cur.code as currency_code
		FROM intercompany_payments icp
		JOIN organizations from_org ON icp.from_organization_id = from_org.id
		JOIN organizations to_org ON icp.to_organization_id = to_org.id
		LEFT JOIN currencies cur ON icp.currency_id = cur.id
		WHERE icp.id = $1 AND icp.tenant_id = $2 AND icp.deleted_at IS NULL
	`, id, tenantID).Scan(
		&p.ID, &p.PaymentNumber, &p.PaymentType,
		&p.FromOrganizationID, &p.ToOrganizationID,
		&p.Amount, &currencyID, &paymentDate, &p.Status,
		&paymentMethod, &reference, &description, &notes, &p.CreatedAt,
		&p.FromOrganizationName, &p.ToOrganizationName,
		&currencyCode,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payment not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC payment", "error", err)
		response.InternalError(c, "Failed to get payment")
		return
	}

	p.PaymentDate = paymentDate.Format("2006-01-02")
	if currencyID.Valid {
		cid := uuid.MustParse(currencyID.String)
		p.CurrencyID = &cid
	}
	if currencyCode.Valid {
		p.CurrencyCode = currencyCode.String
	}
	if paymentMethod.Valid {
		p.PaymentMethod = &paymentMethod.String
	}
	if reference.Valid {
		p.Reference = &reference.String
	}
	if description.Valid {
		p.Description = &description.String
	}
	if notes.Valid {
		p.Notes = &notes.String
	}

	response.Success(c, p)
}

// ApproveIntercompanyPayment approves and completes an IC payment
func (h *Handler) ApproveIntercompanyPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payment ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow(`
		SELECT status FROM intercompany_payments
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payment not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC payment status", "error", err)
		response.InternalError(c, "Failed to approve payment")
		return
	}

	if currentStatus != "draft" && currentStatus != "pending_approval" {
		response.BadRequest(c, "Can only approve payments in draft or pending_approval status")
		return
	}

	_, err = h.db.Exec(`
		UPDATE intercompany_payments
		SET status = 'completed', approved_by = $1, approved_at = CURRENT_TIMESTAMP,
			completed_by = $1, completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND tenant_id = $3
	`, userID, id, tenantID)
	if err != nil {
		h.log.Error("Failed to approve IC payment", "error", err)
		response.InternalError(c, "Failed to approve payment")
		return
	}

	response.Success(c, gin.H{"message": "Payment approved and completed", "status": "completed"})
}

// DeleteIntercompanyPayment soft deletes an IC payment (only in draft status)
func (h *Handler) DeleteIntercompanyPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payment ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow(`
		SELECT status FROM intercompany_payments
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payment not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get IC payment status", "error", err)
		response.InternalError(c, "Failed to delete payment")
		return
	}

	if currentStatus != "draft" {
		response.BadRequest(c, "Can only delete payments in draft status")
		return
	}

	_, err = h.db.Exec(`
		UPDATE intercompany_payments
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete IC payment", "error", err)
		response.InternalError(c, "Failed to delete payment")
		return
	}

	response.Success(c, gin.H{"message": "Payment deleted successfully"})
}

// =====================================================
// INTERCOMPANY BALANCE HANDLERS
// =====================================================

// GetIntercompanyBalances returns balances between organizations
func (h *Handler) GetIntercompanyBalances(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID := c.Query("organization_id")

	query := `
		SELECT
			ica.organization_id,
			org.name as organization_name,
			ica.partner_organization_id,
			partner.name as partner_organization_name,
			ica.account_type,
			ica.balance
		FROM intercompany_accounts ica
		JOIN organizations org ON ica.organization_id = org.id
		JOIN organizations partner ON ica.partner_organization_id = partner.id
		WHERE ica.tenant_id = $1
	`

	args := []interface{}{tenantID}
	if orgID != "" {
		query += " AND (ica.organization_id = $2 OR ica.partner_organization_id = $2)"
		args = append(args, orgID)
	}

	query += " ORDER BY org.name, partner.name"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to get IC balances", "error", err)
		response.InternalError(c, "Failed to get balances")
		return
	}
	defer rows.Close()

	type OrgPair struct {
		OrgID        uuid.UUID
		OrgName      string
		PartnerID    uuid.UUID
		PartnerName  string
		Receivable   float64
		Payable      float64
	}

	// Group by org pair
	pairMap := make(map[string]*OrgPair)
	for rows.Next() {
		var orgID, partnerID uuid.UUID
		var orgName, partnerName, accountType string
		var balance float64

		if err := rows.Scan(&orgID, &orgName, &partnerID, &partnerName, &accountType, &balance); err != nil {
			continue
		}

		key := orgID.String() + "-" + partnerID.String()
		pair, exists := pairMap[key]
		if !exists {
			pair = &OrgPair{
				OrgID:       orgID,
				OrgName:     orgName,
				PartnerID:   partnerID,
				PartnerName: partnerName,
			}
			pairMap[key] = pair
		}

		if accountType == "receivable" {
			pair.Receivable = balance
		} else if accountType == "payable" {
			pair.Payable = balance
		}
	}

	// Convert to summary list
	summaries := make([]entity.IntercompanyBalanceSummary, 0)
	for _, pair := range pairMap {
		summaries = append(summaries, entity.IntercompanyBalanceSummary{
			OrganizationID:          pair.OrgID,
			OrganizationName:        pair.OrgName,
			PartnerOrganizationID:   pair.PartnerID,
			PartnerOrganizationName: pair.PartnerName,
			Receivable:              pair.Receivable,
			Payable:                 pair.Payable,
			NetBalance:              pair.Receivable - pair.Payable,
		})
	}

	response.Success(c, gin.H{"data": summaries})
}

// Helper functions
func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
