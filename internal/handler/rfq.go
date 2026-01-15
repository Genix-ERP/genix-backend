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
// RFQ (REQUEST FOR QUOTATION) HANDLERS
// =====================================================

// ListRFQs returns a paginated list of RFQs
func (h *Handler) ListRFQs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	search := c.Query("search")
	status := c.Query("status")

	baseQuery := `
		SELECT r.id, r.rfq_number, r.title, r.description, r.status,
			   r.deadline, r.terms, r.notes, r.winner_id,
			   COALESCE(c.name, '') as winner_name,
			   (SELECT COUNT(*) FROM rfq_responses WHERE rfq_id = r.id) as response_count,
			   r.created_at, r.updated_at
		FROM rfqs r
		LEFT JOIN contacts c ON r.winner_id = c.id
		WHERE r.tenant_id = $1 AND r.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM rfqs r WHERE r.tenant_id = $1 AND r.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND r.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND r.status = $%d", argCount)
		args = append(args, status)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (r.rfq_number ILIKE $%d OR r.title ILIKE $%d)", argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count RFQs", "error", err)
		response.InternalError(c, "Failed to list RFQs")
		return
	}

	baseQuery += " ORDER BY r.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list RFQs", "error", err)
		response.InternalError(c, "Failed to list RFQs")
		return
	}
	defer rows.Close()

	rfqs := make([]*entity.RFQAPIResponse, 0)
	for rows.Next() {
		var rfq entity.RFQAPIResponse
		var description, terms, notes sql.NullString
		var deadline sql.NullTime
		var winnerID sql.NullString

		err := rows.Scan(
			&rfq.ID, &rfq.RFQNumber, &rfq.Title, &description, &rfq.Status,
			&deadline, &terms, &notes, &winnerID,
			&rfq.WinnerName, &rfq.ResponseCount,
			&rfq.CreatedAt, &rfq.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan RFQ", "error", err)
			continue
		}

		if description.Valid {
			rfq.Description = &description.String
		}
		if deadline.Valid {
			rfq.Deadline = &deadline.Time
		}
		if terms.Valid {
			rfq.Terms = &terms.String
		}
		if notes.Valid {
			rfq.Notes = &notes.String
		}
		if winnerID.Valid {
			if wid, err := uuid.Parse(winnerID.String); err == nil {
				rfq.WinnerID = &wid
			}
		}

		rfqs = append(rfqs, &rfq)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, rfqs, pagination)
}

// CreateRFQ creates a new RFQ
func (h *Handler) CreateRFQ(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateRFQInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	id := uuid.New()
	now := time.Now()

	// Generate RFQ number
	var count int
	h.db.QueryRow("SELECT COUNT(*) FROM rfqs WHERE tenant_id = $1", tenantID).Scan(&count)
	rfqNumber := fmt.Sprintf("RFQ-%s-%04d", now.Format("2006"), count+1)

	// Parse deadline
	var deadline *time.Time
	if input.Deadline != "" {
		if dl, err := time.Parse("2006-01-02", input.Deadline); err == nil {
			deadline = &dl
		}
	}

	// Prepare optional strings
	var description, terms, notes *string
	if input.Description != "" {
		description = &input.Description
	}
	if input.Terms != "" {
		terms = &input.Terms
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create RFQ")
		return
	}
	defer tx.Rollback()

	// Insert RFQ
	query := `
		INSERT INTO rfqs (id, tenant_id, rfq_number, title, description, status, deadline, terms, notes, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err = tx.Exec(query,
		id, tenantID, rfqNumber, input.Title, description, entity.RFQStatusDraft, deadline, terms, notes, userID, now, now,
	)
	if err != nil {
		h.log.Error("Failed to insert RFQ", "error", err)
		response.InternalError(c, "Failed to create RFQ")
		return
	}

	// Insert items
	items := make([]entity.RFQItem, 0, len(input.Items))
	for _, item := range input.Items {
		itemID := uuid.New()

		var productID, unitID *uuid.UUID
		if item.ProductID != "" {
			if pid, err := uuid.Parse(item.ProductID); err == nil {
				productID = &pid
			}
		}
		if item.UnitID != "" {
			if uid, err := uuid.Parse(item.UnitID); err == nil {
				unitID = &uid
			}
		}

		var specs, itemNotes *string
		if item.Specs != "" {
			specs = &item.Specs
		}
		if item.Notes != "" {
			itemNotes = &item.Notes
		}

		_, err = tx.Exec(`
			INSERT INTO rfq_items (id, rfq_id, product_id, description, quantity, unit_id, specs, notes, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, itemID, id, productID, item.Description, item.Quantity, unitID, specs, itemNotes, now, now)
		if err != nil {
			h.log.Error("Failed to insert RFQ item", "error", err)
			response.InternalError(c, "Failed to create RFQ")
			return
		}

		items = append(items, entity.RFQItem{
			ID:          itemID,
			RFQID:       id,
			ProductID:   productID,
			Description: item.Description,
			Quantity:    item.Quantity,
			UnitID:      unitID,
			Specs:       specs,
			Notes:       itemNotes,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}

	// Insert vendor invitations
	for _, vendorIDStr := range input.VendorIDs {
		vendorID, err := uuid.Parse(vendorIDStr)
		if err != nil {
			continue
		}

		inviteID := uuid.New()
		_, err = tx.Exec(`
			INSERT INTO rfq_invitations (id, rfq_id, vendor_id, invited_at)
			VALUES ($1, $2, $3, $4)
		`, inviteID, id, vendorID, now)
		if err != nil {
			h.log.Error("Failed to insert RFQ invitation", "error", err)
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create RFQ")
		return
	}

	resp := &entity.RFQAPIResponse{
		ID:          id,
		RFQNumber:   rfqNumber,
		Title:       input.Title,
		Description: description,
		Status:      entity.RFQStatusDraft,
		Deadline:    deadline,
		Terms:       terms,
		Notes:       notes,
		Items:       items,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	response.Created(c, resp)
}

// GetRFQ returns a single RFQ by ID
func (h *Handler) GetRFQ(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid RFQ ID")
		return
	}

	query := `
		SELECT r.id, r.rfq_number, r.title, r.description, r.status,
			   r.deadline, r.terms, r.notes, r.winner_id,
			   COALESCE(c.name, '') as winner_name,
			   r.created_at, r.updated_at
		FROM rfqs r
		LEFT JOIN contacts c ON r.winner_id = c.id
		WHERE r.id = $1 AND r.tenant_id = $2 AND r.deleted_at IS NULL
	`

	var rfq entity.RFQAPIResponse
	var description, terms, notes sql.NullString
	var deadline sql.NullTime
	var winnerID sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&rfq.ID, &rfq.RFQNumber, &rfq.Title, &description, &rfq.Status,
		&deadline, &terms, &notes, &winnerID,
		&rfq.WinnerName, &rfq.CreatedAt, &rfq.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "RFQ")
		return
	}
	if err != nil {
		h.log.Error("Failed to get RFQ", "error", err)
		response.InternalError(c, "Failed to get RFQ")
		return
	}

	if description.Valid {
		rfq.Description = &description.String
	}
	if deadline.Valid {
		rfq.Deadline = &deadline.Time
	}
	if terms.Valid {
		rfq.Terms = &terms.String
	}
	if notes.Valid {
		rfq.Notes = &notes.String
	}
	if winnerID.Valid {
		if wid, err := uuid.Parse(winnerID.String); err == nil {
			rfq.WinnerID = &wid
		}
	}

	// Get items
	itemRows, err := h.db.Query(`
		SELECT i.id, i.rfq_id, i.product_id, i.description, i.quantity, i.unit_id, i.specs, i.notes,
			   COALESCE(p.name, '') as product_name, COALESCE(u.name, '') as unit_name,
			   i.created_at, i.updated_at
		FROM rfq_items i
		LEFT JOIN products p ON i.product_id = p.id
		LEFT JOIN units_of_measure u ON i.unit_id = u.id
		WHERE i.rfq_id = $1
	`, id)
	if err == nil {
		defer itemRows.Close()
		rfq.Items = make([]entity.RFQItem, 0)
		for itemRows.Next() {
			var item entity.RFQItem
			var productID, unitID sql.NullString
			var specs, itemNotes sql.NullString

			itemRows.Scan(
				&item.ID, &item.RFQID, &productID, &item.Description, &item.Quantity, &unitID, &specs, &itemNotes,
				&item.ProductName, &item.UnitName, &item.CreatedAt, &item.UpdatedAt,
			)

			if productID.Valid {
				if pid, err := uuid.Parse(productID.String); err == nil {
					item.ProductID = &pid
				}
			}
			if unitID.Valid {
				if uid, err := uuid.Parse(unitID.String); err == nil {
					item.UnitID = &uid
				}
			}
			if specs.Valid {
				item.Specs = &specs.String
			}
			if itemNotes.Valid {
				item.Notes = &itemNotes.String
			}

			rfq.Items = append(rfq.Items, item)
		}
	}

	// Get responses
	respRows, err := h.db.Query(`
		SELECT r.id, r.rfq_id, r.vendor_id, COALESCE(c.name, '') as vendor_name,
			   r.total_amount, r.lead_time_days, r.valid_until, r.notes, r.is_winner,
			   r.created_at, r.updated_at
		FROM rfq_responses r
		LEFT JOIN contacts c ON r.vendor_id = c.id
		WHERE r.rfq_id = $1
	`, id)
	if err == nil {
		defer respRows.Close()
		rfq.Responses = make([]entity.RFQResponse, 0)
		for respRows.Next() {
			var resp entity.RFQResponse
			var respNotes sql.NullString
			var validUntil sql.NullTime

			respRows.Scan(
				&resp.ID, &resp.RFQID, &resp.VendorID, &resp.VendorName,
				&resp.TotalAmount, &resp.LeadTimeDays, &validUntil, &respNotes, &resp.IsWinner,
				&resp.CreatedAt, &resp.UpdatedAt,
			)

			if validUntil.Valid {
				resp.ValidUntil = &validUntil.Time
			}
			if respNotes.Valid {
				resp.Notes = &respNotes.String
			}

			rfq.Responses = append(rfq.Responses, resp)
		}
		rfq.ResponseCount = len(rfq.Responses)
	}

	response.Success(c, rfq)
}

// UpdateRFQ updates an existing RFQ
func (h *Handler) UpdateRFQ(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid RFQ ID")
		return
	}

	// Check if RFQ exists and is editable
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM rfqs WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "RFQ")
		return
	}
	if err != nil {
		h.log.Error("Failed to check RFQ", "error", err)
		response.InternalError(c, "Failed to update RFQ")
		return
	}

	if currentStatus != string(entity.RFQStatusDraft) {
		response.BadRequest(c, "Only draft RFQs can be edited")
		return
	}

	var input map[string]interface{}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	_, err = h.db.Exec("UPDATE rfqs SET updated_at = $1 WHERE id = $2", time.Now(), id)
	if err != nil {
		h.log.Error("Failed to update RFQ", "error", err)
		response.InternalError(c, "Failed to update RFQ")
		return
	}

	response.Success(c, gin.H{"message": "RFQ updated successfully"})
}

// DeleteRFQ soft deletes an RFQ
func (h *Handler) DeleteRFQ(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid RFQ ID")
		return
	}

	// Check status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM rfqs WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "RFQ")
		return
	}

	if currentStatus != string(entity.RFQStatusDraft) && currentStatus != string(entity.RFQStatusCancelled) {
		response.BadRequest(c, "Only draft or cancelled RFQs can be deleted")
		return
	}

	_, err = h.db.Exec("UPDATE rfqs SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND tenant_id = $3", time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete RFQ", "error", err)
		response.InternalError(c, "Failed to delete RFQ")
		return
	}

	response.NoContent(c)
}

// SubmitRFQResponse submits a vendor response to an RFQ
func (h *Handler) SubmitRFQResponse(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	rfqID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid RFQ ID")
		return
	}

	var input entity.SubmitRFQResponseInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor ID")
		return
	}

	// Check RFQ is open
	var rfqStatus string
	err = h.db.QueryRow("SELECT status FROM rfqs WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", rfqID, tenantID).Scan(&rfqStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "RFQ")
		return
	}
	if rfqStatus != string(entity.RFQStatusOpen) {
		response.BadRequest(c, "RFQ is not open for responses")
		return
	}

	now := time.Now()
	responseID := uuid.New()

	// Parse valid until
	var validUntil *time.Time
	if input.ValidUntil != "" {
		if vu, err := time.Parse("2006-01-02", input.ValidUntil); err == nil {
			validUntil = &vu
		}
	}

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to submit response")
		return
	}
	defer tx.Rollback()

	// Calculate total amount
	var totalAmount float64
	for _, item := range input.Items {
		// Get item quantity
		var qty float64
		h.db.QueryRow("SELECT quantity FROM rfq_items WHERE id = $1", item.ItemID).Scan(&qty)
		totalAmount += item.UnitPrice * qty
	}

	// Insert response
	_, err = tx.Exec(`
		INSERT INTO rfq_responses (id, rfq_id, vendor_id, total_amount, lead_time_days, valid_until, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (rfq_id, vendor_id) DO UPDATE SET
			total_amount = EXCLUDED.total_amount,
			lead_time_days = EXCLUDED.lead_time_days,
			valid_until = EXCLUDED.valid_until,
			notes = EXCLUDED.notes,
			updated_at = EXCLUDED.updated_at
	`, responseID, rfqID, vendorID, totalAmount, input.LeadTimeDays, validUntil, notes, now, now)
	if err != nil {
		h.log.Error("Failed to insert response", "error", err)
		response.InternalError(c, "Failed to submit response")
		return
	}

	// Insert response items
	for _, item := range input.Items {
		itemID, err := uuid.Parse(item.ItemID)
		if err != nil {
			continue
		}

		// Get item quantity
		var qty float64
		h.db.QueryRow("SELECT quantity FROM rfq_items WHERE id = $1", itemID).Scan(&qty)
		totalPrice := item.UnitPrice * qty

		var itemNotes *string
		if item.Notes != "" {
			itemNotes = &item.Notes
		}

		_, err = tx.Exec(`
			INSERT INTO rfq_response_items (id, response_id, item_id, unit_price, total_price, notes)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (response_id, item_id) DO UPDATE SET
				unit_price = EXCLUDED.unit_price,
				total_price = EXCLUDED.total_price,
				notes = EXCLUDED.notes
		`, uuid.New(), responseID, itemID, item.UnitPrice, totalPrice, itemNotes)
		if err != nil {
			h.log.Error("Failed to insert response item", "error", err)
		}
	}

	// Update invitation
	_, err = tx.Exec("UPDATE rfq_invitations SET responded_at = $1 WHERE rfq_id = $2 AND vendor_id = $3", now, rfqID, vendorID)
	if err != nil {
		h.log.Error("Failed to update invitation", "error", err)
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to submit response")
		return
	}

	response.Success(c, gin.H{"message": "Response submitted successfully", "total_amount": totalAmount})
}

// SelectRFQWinner selects a winner for an RFQ
func (h *Handler) SelectRFQWinner(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	rfqID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid RFQ ID")
		return
	}

	var input struct {
		ResponseID string `json:"response_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	responseID, err := uuid.Parse(input.ResponseID)
	if err != nil {
		response.BadRequest(c, "Invalid response ID")
		return
	}

	// Get vendor ID from response
	var vendorID uuid.UUID
	err = h.db.QueryRow("SELECT vendor_id FROM rfq_responses WHERE id = $1 AND rfq_id = $2", responseID, rfqID).Scan(&vendorID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Response")
		return
	}
	if err != nil {
		h.log.Error("Failed to get response", "error", err)
		response.InternalError(c, "Failed to select winner")
		return
	}

	now := time.Now()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to select winner")
		return
	}
	defer tx.Rollback()

	// Reset all winners
	_, err = tx.Exec("UPDATE rfq_responses SET is_winner = false WHERE rfq_id = $1", rfqID)
	if err != nil {
		h.log.Error("Failed to reset winners", "error", err)
		response.InternalError(c, "Failed to select winner")
		return
	}

	// Set new winner
	_, err = tx.Exec("UPDATE rfq_responses SET is_winner = true WHERE id = $1", responseID)
	if err != nil {
		h.log.Error("Failed to set winner", "error", err)
		response.InternalError(c, "Failed to select winner")
		return
	}

	// Update RFQ
	_, err = tx.Exec("UPDATE rfqs SET winner_id = $1, status = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		vendorID, entity.RFQStatusClosed, now, rfqID, tenantID)
	if err != nil {
		h.log.Error("Failed to update RFQ", "error", err)
		response.InternalError(c, "Failed to select winner")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to select winner")
		return
	}

	response.Success(c, gin.H{"message": "Winner selected successfully", "winner_id": vendorID})
}

// OpenRFQ opens an RFQ for responses
func (h *Handler) OpenRFQ(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid RFQ ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM rfqs WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "RFQ")
		return
	}

	if currentStatus != string(entity.RFQStatusDraft) {
		response.BadRequest(c, "Only draft RFQs can be opened")
		return
	}

	_, err = h.db.Exec("UPDATE rfqs SET status = $1, updated_at = $2 WHERE id = $3", entity.RFQStatusOpen, time.Now(), id)
	if err != nil {
		h.log.Error("Failed to open RFQ", "error", err)
		response.InternalError(c, "Failed to open RFQ")
		return
	}

	response.Success(c, gin.H{"message": "RFQ opened successfully", "status": entity.RFQStatusOpen})
}
