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
			   (SELECT COUNT(*) FROM rfq_invitations WHERE rfq_id = r.id) as invitation_count,
			   (SELECT COUNT(*) FROM rfq_responses WHERE rfq_id = r.id) as response_count,
			   r.created_at, r.updated_at
		FROM rfqs r
		LEFT JOIN contacts c ON r.winner_id = c.id
		WHERE r.tenant_id = $1 AND r.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM rfqs r WHERE r.tenant_id = $1 AND r.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND r.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND r.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

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

		var invitationCount int
		err := rows.Scan(
			&rfq.ID, &rfq.RFQNumber, &rfq.Title, &description, &rfq.Status,
			&deadline, &terms, &notes, &winnerID,
			&rfq.WinnerName, &invitationCount, &rfq.ResponseCount,
			&rfq.CreatedAt, &rfq.UpdatedAt,
		)
		rfq.InvitationCount = invitationCount
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

	// Parse deadline - default to 30 days from now if not provided
	var deadline time.Time
	if input.Deadline != "" {
		if dl, err := time.Parse("2006-01-02", input.Deadline); err == nil {
			deadline = dl
		} else {
			deadline = now.AddDate(0, 0, 30) // default 30 days
		}
	} else {
		deadline = now.AddDate(0, 0, 30) // default 30 days
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
	// issue_date is the date RFQ was created, deadline is the response deadline
	issueDate := now.Format("2006-01-02")

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO rfqs (id, tenant_id, organization_id, rfq_number, title, description, status, issue_date, deadline, terms, notes, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`

	_, err = tx.Exec(query,
		id, tenantID, orgIDPtr, rfqNumber, input.Title, description, entity.RFQStatusDraft, issueDate, deadline, terms, notes, userID, now, now,
	)
	if err != nil {
		h.log.Error("Failed to insert RFQ", "error", err)
		response.InternalError(c, "Failed to create RFQ")
		return
	}

	// Insert items
	items := make([]entity.RFQItem, 0, len(input.Items))
	for i, item := range input.Items {
		itemID := uuid.New()
		lineNumber := i + 1

		var productID *uuid.UUID
		if item.ProductID != "" {
			if pid, err := uuid.Parse(item.ProductID); err == nil {
				productID = &pid
			}
		}

		// unit is stored as string (not unit_id) in migration 015
		unit := item.UnitID // Use unit_id as unit string if provided
		if unit == "" {
			unit = "pcs" // default
		}

		var specs *string
		if item.Specs != "" {
			specs = &item.Specs
		}

		_, err = tx.Exec(`
			INSERT INTO rfq_items (id, tenant_id, rfq_id, line_number, product_id, description, quantity, unit, specifications, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, itemID, tenantID, id, lineNumber, productID, item.Description, item.Quantity, unit, specs, now, now)
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
			Specs:       specs,
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
			INSERT INTO rfq_invitations (id, tenant_id, rfq_id, vendor_id, invited_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, inviteID, tenantID, id, vendorID, now, now, now)
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
		Deadline:    &deadline,
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
		SELECT i.id, i.rfq_id, i.product_id, i.description, i.quantity, i.unit, i.specifications,
			   COALESCE(p.name, '') as product_name,
			   i.created_at, i.updated_at
		FROM rfq_items i
		LEFT JOIN products p ON i.product_id = p.id
		WHERE i.rfq_id = $1
	`, id)
	if err == nil {
		defer itemRows.Close()
		rfq.Items = make([]entity.RFQItem, 0)
		for itemRows.Next() {
			var item entity.RFQItem
			var productID sql.NullString
			var unit, specs sql.NullString

			itemRows.Scan(
				&item.ID, &item.RFQID, &productID, &item.Description, &item.Quantity, &unit, &specs,
				&item.ProductName, &item.CreatedAt, &item.UpdatedAt,
			)

			if productID.Valid {
				if pid, err := uuid.Parse(productID.String); err == nil {
					item.ProductID = &pid
				}
			}
			if unit.Valid {
				item.UnitName = unit.String
			}
			if specs.Valid {
				item.Specs = &specs.String
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
		INSERT INTO rfq_responses (id, tenant_id, rfq_id, vendor_id, total_amount, lead_time_days, valid_until, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (rfq_id, vendor_id) DO UPDATE SET
			total_amount = EXCLUDED.total_amount,
			lead_time_days = EXCLUDED.lead_time_days,
			valid_until = EXCLUDED.valid_until,
			notes = EXCLUDED.notes,
			updated_at = EXCLUDED.updated_at
	`, responseID, tenantID, rfqID, vendorID, totalAmount, input.LeadTimeDays, validUntil, notes, now, now)
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

		var itemNotes *string
		if item.Notes != "" {
			itemNotes = &item.Notes
		}

		_, err = tx.Exec(`
			INSERT INTO rfq_response_items (id, tenant_id, response_id, rfq_item_id, unit_price, quantity, notes, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (response_id, rfq_item_id) DO UPDATE SET
				unit_price = EXCLUDED.unit_price,
				quantity = EXCLUDED.quantity,
				notes = EXCLUDED.notes,
				updated_at = EXCLUDED.updated_at
		`, uuid.New(), tenantID, responseID, itemID, item.UnitPrice, qty, itemNotes, now, now)
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

// ConvertRFQToPO creates a Purchase Order from a closed RFQ with a selected winner
func (h *Handler) ConvertRFQToPO(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	rfqID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid RFQ ID")
		return
	}

	// Get RFQ details
	var rfq struct {
		ID          uuid.UUID
		RFQNumber   string
		Title       string
		Status      string
		WinnerID    sql.NullString
		Description sql.NullString
		Notes       sql.NullString
	}

	err = h.db.QueryRow(`
		SELECT id, rfq_number, title, status, winner_id, description, notes
		FROM rfqs
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, rfqID, tenantID).Scan(
		&rfq.ID, &rfq.RFQNumber, &rfq.Title, &rfq.Status,
		&rfq.WinnerID, &rfq.Description, &rfq.Notes,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "RFQ")
		return
	}
	if err != nil {
		h.log.Error("Failed to get RFQ", "error", err)
		response.InternalError(c, "Failed to convert RFQ to PO")
		return
	}

	// Validate RFQ status
	if rfq.Status != string(entity.RFQStatusClosed) {
		response.BadRequest(c, "Only closed RFQs with a selected winner can be converted to PO")
		return
	}

	if !rfq.WinnerID.Valid {
		response.BadRequest(c, "RFQ has no selected winner")
		return
	}

	winnerID, err := uuid.Parse(rfq.WinnerID.String)
	if err != nil {
		response.BadRequest(c, "Invalid winner ID")
		return
	}

	// Check if a PO already exists for this RFQ
	var existingPOCount int
	h.db.QueryRow("SELECT COUNT(*) FROM purchase_orders WHERE rfq_id = $1 AND tenant_id = $2 AND deleted_at IS NULL", rfqID, tenantID).Scan(&existingPOCount)
	if existingPOCount > 0 {
		response.BadRequest(c, "A Purchase Order already exists for this RFQ")
		return
	}

	// Get winning response details
	var winningResponse struct {
		ID           uuid.UUID
		TotalAmount  float64
		LeadTimeDays sql.NullInt64
		Notes        sql.NullString
	}

	err = h.db.QueryRow(`
		SELECT id, total_amount, lead_time_days, notes
		FROM rfq_responses
		WHERE rfq_id = $1 AND vendor_id = $2 AND is_winner = true
	`, rfqID, winnerID).Scan(
		&winningResponse.ID, &winningResponse.TotalAmount,
		&winningResponse.LeadTimeDays, &winningResponse.Notes,
	)
	if err != nil && err != sql.ErrNoRows {
		h.log.Error("Failed to get winning response", "error", err)
	}

	// Get RFQ items with response prices
	type rfqItemWithPrice struct {
		ProductID   sql.NullString
		Description string
		Quantity    float64
		Unit        string
		UnitPrice   float64
	}

	rows, err := h.db.Query(`
		SELECT i.product_id, i.description, i.quantity, COALESCE(i.unit, 'pcs'),
			   COALESCE(ri.unit_price, 0)
		FROM rfq_items i
		LEFT JOIN rfq_response_items ri ON ri.item_id = i.id AND ri.response_id = $2
		WHERE i.rfq_id = $1
		ORDER BY i.line_number
	`, rfqID, winningResponse.ID)
	if err != nil {
		h.log.Error("Failed to get RFQ items", "error", err)
		response.InternalError(c, "Failed to convert RFQ to PO")
		return
	}
	defer rows.Close()

	var items []rfqItemWithPrice
	for rows.Next() {
		var item rfqItemWithPrice
		if err := rows.Scan(&item.ProductID, &item.Description, &item.Quantity, &item.Unit, &item.UnitPrice); err != nil {
			h.log.Error("Failed to scan RFQ item", "error", err)
			continue
		}
		items = append(items, item)
	}

	if len(items) == 0 {
		response.BadRequest(c, "RFQ has no items")
		return
	}

	// Get vendor name
	var vendorName string
	h.db.QueryRow("SELECT name FROM contacts WHERE id = $1", winnerID).Scan(&vendorName)

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to convert RFQ to PO")
		return
	}
	defer tx.Rollback()

	now := time.Now()
	poID := uuid.New()

	// Generate PO number
	var poCount int
	h.db.QueryRow("SELECT COUNT(*) FROM purchase_orders WHERE tenant_id = $1", tenantID).Scan(&poCount)
	poNumber := fmt.Sprintf("PO-%s-%04d", now.Format("2006"), poCount+1)

	// Calculate expected delivery date based on lead time
	expectedDate := now.AddDate(0, 0, 7) // default 7 days
	if winningResponse.LeadTimeDays.Valid && winningResponse.LeadTimeDays.Int64 > 0 {
		expectedDate = now.AddDate(0, 0, int(winningResponse.LeadTimeDays.Int64))
	}

	// Calculate totals
	var subtotal float64
	for _, item := range items {
		subtotal += item.Quantity * item.UnitPrice
	}

	// Build notes for PO
	poNotes := fmt.Sprintf("Created from RFQ: %s", rfq.RFQNumber)
	if rfq.Notes.Valid && rfq.Notes.String != "" {
		poNotes += "\n\nRFQ Notes: " + rfq.Notes.String
	}
	if winningResponse.Notes.Valid && winningResponse.Notes.String != "" {
		poNotes += "\n\nVendor Notes: " + winningResponse.Notes.String
	}

	// Insert Purchase Order
	_, err = tx.Exec(`
		INSERT INTO purchase_orders (
			id, tenant_id, order_number, vendor_id, vendor_name,
			order_date, expected_date, subtotal, total_amount,
			status, payment_status, notes, rfq_id,
			requested_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13,
			$14, $15, $16
		)
	`, poID, tenantID, poNumber, winnerID, vendorName,
		now, expectedDate, subtotal, subtotal,
		"draft", "unpaid", poNotes, rfqID,
		userID, now, now)
	if err != nil {
		h.log.Error("Failed to create PO", "error", err)
		response.InternalError(c, "Failed to create Purchase Order")
		return
	}

	// Insert PO lines
	for i, item := range items {
		lineID := uuid.New()
		lineNumber := i + 1
		lineTotal := item.Quantity * item.UnitPrice

		var productID *uuid.UUID
		if item.ProductID.Valid {
			if pid, err := uuid.Parse(item.ProductID.String); err == nil {
				productID = &pid
			}
		}

		// Get product name if product_id exists
		productName := item.Description
		if productID != nil {
			var pName string
			if err := h.db.QueryRow("SELECT name FROM products WHERE id = $1", productID).Scan(&pName); err == nil && pName != "" {
				productName = pName
			}
		}

		_, err = tx.Exec(`
			INSERT INTO purchase_order_lines (
				id, tenant_id, purchase_order_id, line_number,
				product_id, product_name, quantity, unit_price, total_price,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4,
				$5, $6, $7, $8, $9,
				$10, $11
			)
		`, lineID, tenantID, poID, lineNumber,
			productID, productName, item.Quantity, item.UnitPrice, lineTotal,
			now, now)
		if err != nil {
			h.log.Error("Failed to create PO line", "error", err)
			response.InternalError(c, "Failed to create Purchase Order lines")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to convert RFQ to PO")
		return
	}

	response.Created(c, gin.H{
		"message":          "Purchase Order created successfully from RFQ",
		"purchase_order_id": poID,
		"order_number":      poNumber,
		"rfq_number":        rfq.RFQNumber,
		"vendor_name":       vendorName,
		"total_amount":      subtotal,
		"expected_date":     expectedDate.Format("2006-01-02"),
	})
}
