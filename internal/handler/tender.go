package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// parseFlexibleTime parses datetime strings in various formats from frontend inputs
func parseFlexibleTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse datetime: %s", s)
}

// ListTenders lists active tenders with filtering and pagination
func (h *Handler) ListTenders(c *gin.Context) {
	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	status := c.Query("status")
	regionID := c.Query("region_id")
	categoryID := c.Query("category_id")
	search := c.Query("search")
	ordering := c.DefaultQuery("ordering", "-created_at")

	pagination := entity.NewPagination(page, limit)

	// Build count query
	countQuery := `SELECT COUNT(*) FROM tender_tenders t WHERE t.deleted_at IS NULL`
	args := []interface{}{}
	argIdx := 1

	if status != "" {
		countQuery += fmt.Sprintf(" AND t.status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	} else {
		countQuery += fmt.Sprintf(" AND t.status = $%d", argIdx)
		args = append(args, "active")
		argIdx++
	}

	if regionID != "" {
		countQuery += fmt.Sprintf(" AND t.region_id = $%d", argIdx)
		args = append(args, regionID)
		argIdx++
	}

	if categoryID != "" {
		countQuery += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM tender_items ti WHERE ti.tender_id = t.id AND ti.category_id = $%d)`, argIdx)
		args = append(args, categoryID)
		argIdx++
	}

	if search != "" {
		countQuery += fmt.Sprintf(" AND (t.title ILIKE $%d OR t.description ILIKE $%d)", argIdx, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)
	pagination.Calculate(total)

	// Build main query
	query := `
		SELECT t.id, t.buyer_id, t.title, t.description, t.status, t.tender_type,
		       t.region_id, t.delivery_address, t.deadline, t.delivery_date,
		       t.currency, t.attachment, t.bid_count, t.created_at,
		       COALESCE(cp.company_name, '') as buyer_name,
		       COALESCE(r.name, '') as region_name
		FROM tender_tenders t
		LEFT JOIN tender_company_profiles cp ON cp.user_id = t.buyer_id AND cp.deleted_at IS NULL
		LEFT JOIN tender_regions r ON r.id = t.region_id
		WHERE t.deleted_at IS NULL
	`

	queryArgs := []interface{}{}
	qArgIdx := 1

	if status != "" {
		query += fmt.Sprintf(" AND t.status = $%d", qArgIdx)
		queryArgs = append(queryArgs, status)
		qArgIdx++
	} else {
		query += fmt.Sprintf(" AND t.status = $%d", qArgIdx)
		queryArgs = append(queryArgs, "active")
		qArgIdx++
	}

	if regionID != "" {
		query += fmt.Sprintf(" AND t.region_id = $%d", qArgIdx)
		queryArgs = append(queryArgs, regionID)
		qArgIdx++
	}

	if categoryID != "" {
		query += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM tender_items ti WHERE ti.tender_id = t.id AND ti.category_id = $%d)`, qArgIdx)
		queryArgs = append(queryArgs, categoryID)
		qArgIdx++
	}

	if search != "" {
		query += fmt.Sprintf(" AND (t.title ILIKE $%d OR t.description ILIKE $%d)", qArgIdx, qArgIdx)
		queryArgs = append(queryArgs, "%"+search+"%")
		qArgIdx++
	}

	// Ordering
	switch ordering {
	case "deadline":
		query += " ORDER BY t.deadline ASC"
	case "-deadline":
		query += " ORDER BY t.deadline DESC"
	case "bid_count":
		query += " ORDER BY t.bid_count DESC"
	case "created_at":
		query += " ORDER BY t.created_at ASC"
	default:
		query += " ORDER BY t.created_at DESC"
	}

	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", qArgIdx, qArgIdx+1)
	queryArgs = append(queryArgs, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, queryArgs...)
	if err != nil {
		h.log.Error("Failed to list tenders", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var tenders []entity.TenderResponse
	for rows.Next() {
		var t entity.TenderResponse
		var regionID sql.NullString
		var regionName sql.NullString
		var deliveryDate sql.NullTime
		var attachment sql.NullString

		err := rows.Scan(
			&t.ID, &t.BuyerID, &t.Title, &t.Description, &t.Status, &t.TenderType,
			&regionID, &t.DeliveryAddress, &t.Deadline, &deliveryDate,
			&t.Currency, &attachment, &t.BidCount, &t.CreatedAt,
			&t.BuyerName, &regionName,
		)
		if err != nil {
			h.log.Error("Failed to scan tender", "error", err)
			continue
		}

		if regionID.Valid {
			parsed, _ := uuid.Parse(regionID.String)
			t.RegionID = &parsed
		}
		if regionName.Valid {
			t.RegionName = regionName.String
		}
		if deliveryDate.Valid {
			t.DeliveryDate = &deliveryDate.Time
		}
		if attachment.Valid {
			t.Attachment = attachment.String
		}

		// Fetch items for this tender
		itemRows, itemErr := h.db.Query(`
			SELECT ti.id, ti.category_id, ti.name, ti.quantity, ti.unit, COALESCE(ti.specs, ''),
			       COALESCE(c.name, '') as category_name
			FROM tender_items ti
			LEFT JOIN tender_categories c ON c.id = ti.category_id
			WHERE ti.tender_id = $1
			ORDER BY ti.created_at ASC
		`, t.ID)
		if itemErr == nil {
			for itemRows.Next() {
				var item entity.TenderItemResponse
				var catID sql.NullString
				if err := itemRows.Scan(&item.ID, &catID, &item.Name, &item.Quantity, &item.Unit, &item.Specs, &item.CategoryName); err == nil {
					if catID.Valid {
						parsed, _ := uuid.Parse(catID.String)
						item.CategoryID = &parsed
					}
					t.Items = append(t.Items, item)
				}
			}
			itemRows.Close()
		}

		tenders = append(tenders, t)
	}

	response.SuccessWithPagination(c, tenders, pagination)
}

// GetTender retrieves a single tender with its items
func (h *Handler) GetTender(c *gin.Context) {
	tenderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tender ID")
		return
	}

	// Get tender
	query := `
		SELECT t.id, t.buyer_id, t.title, t.description, t.status, t.tender_type,
		       t.region_id, t.delivery_address, t.deadline, t.delivery_date,
		       t.currency, t.attachment, t.bid_count, t.selected_bid_id, t.created_at,
		       COALESCE(cp.company_name, '') as buyer_name,
		       COALESCE(r.name, '') as region_name
		FROM tender_tenders t
		LEFT JOIN tender_company_profiles cp ON cp.user_id = t.buyer_id AND cp.deleted_at IS NULL
		LEFT JOIN tender_regions r ON r.id = t.region_id
		WHERE t.id = $1 AND t.deleted_at IS NULL
	`

	var t entity.TenderResponse
	var regionID, attachment sql.NullString
	var regionName sql.NullString
	var deliveryDate sql.NullTime
	var selectedBidID sql.NullString

	err = h.db.QueryRow(query, tenderID).Scan(
		&t.ID, &t.BuyerID, &t.Title, &t.Description, &t.Status, &t.TenderType,
		&regionID, &t.DeliveryAddress, &t.Deadline, &deliveryDate,
		&t.Currency, &attachment, &t.BidCount, &selectedBidID, &t.CreatedAt,
		&t.BuyerName, &regionName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tender not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get tender", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if regionID.Valid {
		parsed, _ := uuid.Parse(regionID.String)
		t.RegionID = &parsed
	}
	if regionName.Valid {
		t.RegionName = regionName.String
	}
	if deliveryDate.Valid {
		t.DeliveryDate = &deliveryDate.Time
	}
	if attachment.Valid {
		t.Attachment = attachment.String
	}
	if selectedBidID.Valid {
		parsed, _ := uuid.Parse(selectedBidID.String)
		t.SelectedBidID = &parsed
	}

	// Get tender items
	itemRows, err := h.db.Query(`
		SELECT ti.id, ti.category_id, ti.name, ti.quantity, ti.unit, ti.specs,
		       COALESCE(c.name, '') as category_name
		FROM tender_items ti
		LEFT JOIN tender_categories c ON c.id = ti.category_id
		WHERE ti.tender_id = $1
		ORDER BY ti.sort_order ASC
	`, tenderID)
	if err == nil {
		defer itemRows.Close()
		for itemRows.Next() {
			var item entity.TenderItemResponse
			var catID sql.NullString
			var catName sql.NullString

			if err := itemRows.Scan(&item.ID, &catID, &item.Name, &item.Quantity, &item.Unit, &item.Specs, &catName); err == nil {
				if catID.Valid {
					parsed, _ := uuid.Parse(catID.String)
					item.CategoryID = &parsed
				}
				if catName.Valid {
					item.CategoryName = catName.String
				}
				t.Items = append(t.Items, item)
			}
		}
	}

	response.Success(c, t)
}

// CreateTender creates a new tender (Buyer only)
func (h *Handler) CreateTender(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var input entity.CreateTenderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid tender input", "error", err)
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Set default status
	if input.Status == "" {
		input.Status = "active"
	}

	// Parse deadline
	deadline, err := parseFlexibleTime(input.Deadline)
	if err != nil {
		response.BadRequest(c, "Invalid deadline format")
		return
	}
	if deadline.Before(time.Now()) {
		response.BadRequest(c, "Deadline must be in the future")
		return
	}

	// Parse optional delivery date
	var deliveryDate *time.Time
	if input.DeliveryDate != "" {
		dd, err := parseFlexibleTime(input.DeliveryDate)
		if err == nil {
			deliveryDate = &dd
		}
	}

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer tx.Rollback()

	// Insert tender
	tenderID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO tender_tenders (id, buyer_id, title, description, status, tender_type,
		                            region_id, delivery_address, deadline, delivery_date, currency, attachment)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, tenderID, userID, input.Title, input.Description, input.Status, input.TenderType,
		input.RegionID, input.DeliveryAddress, deadline, deliveryDate, input.Currency, "")
	if err != nil {
		h.log.Error("Failed to create tender", "error", err)
		response.InternalServerError(c, "Failed to create tender")
		return
	}

	// Insert tender items
	for i, item := range input.Items {
		itemID := uuid.New()
		_, err = tx.Exec(`
			INSERT INTO tender_items (id, tender_id, category_id, name, quantity, unit, specs, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, itemID, tenderID, item.CategoryID, item.Name, item.Quantity, item.Unit, item.Specs, i+1)
		if err != nil {
			h.log.Error("Failed to create tender item", "error", err)
			response.InternalServerError(c, "Failed to create tender item")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Return the created tender
	c.Params = append(c.Params, gin.Param{Key: "id", Value: tenderID.String()})
	h.GetTender(c)
}

// UpdateTender updates an existing tender (owner only, draft status)
func (h *Handler) UpdateTender(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	tenderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tender ID")
		return
	}

	// Check ownership
	var buyerID uuid.UUID
	var status string
	err = h.db.QueryRow(`SELECT buyer_id, status FROM tender_tenders WHERE id = $1 AND deleted_at IS NULL`, tenderID).Scan(&buyerID, &status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tender not found")
		return
	}
	if err != nil {
		response.InternalServerError(c, "")
		return
	}

	if buyerID != userID {
		response.Forbidden(c, "Only the tender owner can update it")
		return
	}

	if status != "draft" && status != "active" {
		response.BadRequest(c, "Only draft or active tenders can be updated")
		return
	}

	var input entity.UpdateTenderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build dynamic update
	query := "UPDATE tender_tenders SET updated_at = NOW()"
	args := []interface{}{}
	argIdx := 1

	if input.Title != "" {
		query += fmt.Sprintf(", title = $%d", argIdx)
		args = append(args, input.Title)
		argIdx++
	}
	if input.Description != "" {
		query += fmt.Sprintf(", description = $%d", argIdx)
		args = append(args, input.Description)
		argIdx++
	}
	if input.RegionID != nil {
		query += fmt.Sprintf(", region_id = $%d", argIdx)
		args = append(args, input.RegionID)
		argIdx++
	}
	if input.DeliveryAddress != "" {
		query += fmt.Sprintf(", delivery_address = $%d", argIdx)
		args = append(args, input.DeliveryAddress)
		argIdx++
	}
	if input.Deadline != "" {
		if dl, err := parseFlexibleTime(input.Deadline); err == nil {
			query += fmt.Sprintf(", deadline = $%d", argIdx)
			args = append(args, dl)
			argIdx++
		}
	}
	if input.DeliveryDate != "" {
		if dd, err := parseFlexibleTime(input.DeliveryDate); err == nil {
			query += fmt.Sprintf(", delivery_date = $%d", argIdx)
			args = append(args, dd)
			argIdx++
		}
	}
	if input.Currency != "" {
		query += fmt.Sprintf(", currency = $%d", argIdx)
		args = append(args, input.Currency)
		argIdx++
	}
	if input.Status != "" {
		query += fmt.Sprintf(", status = $%d", argIdx)
		args = append(args, input.Status)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d AND deleted_at IS NULL", argIdx)
	args = append(args, tenderID)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update tender", "error", err)
		response.InternalServerError(c, "Failed to update tender")
		return
	}

	h.GetTender(c)
}

// DeleteTender soft-deletes a tender (owner only, draft status)
func (h *Handler) DeleteTender(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	tenderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tender ID")
		return
	}

	var buyerID uuid.UUID
	var status string
	err = h.db.QueryRow(`SELECT buyer_id, status FROM tender_tenders WHERE id = $1 AND deleted_at IS NULL`, tenderID).Scan(&buyerID, &status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tender not found")
		return
	}
	if buyerID != userID {
		response.Forbidden(c, "Only the tender owner can delete it")
		return
	}
	if status != "draft" {
		response.BadRequest(c, "Only draft tenders can be deleted")
		return
	}

	_, err = h.db.Exec(`UPDATE tender_tenders SET deleted_at = NOW() WHERE id = $1`, tenderID)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}

	response.NoContent(c)
}

// GetMyTenders lists tenders created by the current buyer
func (h *Handler) GetMyTenders(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	status := c.Query("status")

	pagination := entity.NewPagination(page, limit)

	countQuery := `SELECT COUNT(*) FROM tender_tenders WHERE buyer_id = $1 AND deleted_at IS NULL`
	countArgs := []interface{}{userID}
	if status != "" {
		countQuery += " AND status = $2"
		countArgs = append(countArgs, status)
	}

	var total int
	h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	pagination.Calculate(total)

	query := `
		SELECT t.id, t.buyer_id, t.title, t.description, t.status, t.tender_type,
		       t.region_id, t.delivery_address, t.deadline, t.delivery_date,
		       t.currency, t.attachment, t.bid_count, t.created_at,
		       COALESCE(r.name, '') as region_name
		FROM tender_tenders t
		LEFT JOIN tender_regions r ON r.id = t.region_id
		WHERE t.buyer_id = $1 AND t.deleted_at IS NULL
	`
	queryArgs := []interface{}{userID}
	argIdx := 2

	if status != "" {
		query += fmt.Sprintf(" AND t.status = $%d", argIdx)
		queryArgs = append(queryArgs, status)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY t.created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	queryArgs = append(queryArgs, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, queryArgs...)
	if err != nil {
		h.log.Error("Failed to list my tenders", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var tenders []entity.TenderResponse
	for rows.Next() {
		var t entity.TenderResponse
		var regionID, attachment sql.NullString
		var regionName sql.NullString
		var deliveryDate sql.NullTime

		err := rows.Scan(
			&t.ID, &t.BuyerID, &t.Title, &t.Description, &t.Status, &t.TenderType,
			&regionID, &t.DeliveryAddress, &t.Deadline, &deliveryDate,
			&t.Currency, &attachment, &t.BidCount, &t.CreatedAt,
			&regionName,
		)
		if err != nil {
			continue
		}

		if regionID.Valid {
			parsed, _ := uuid.Parse(regionID.String)
			t.RegionID = &parsed
		}
		if regionName.Valid {
			t.RegionName = regionName.String
		}
		if deliveryDate.Valid {
			t.DeliveryDate = &deliveryDate.Time
		}
		if attachment.Valid {
			t.Attachment = attachment.String
		}

		// Fetch items for this tender
		itemRows, itemErr := h.db.Query(`
			SELECT ti.id, ti.category_id, ti.name, ti.quantity, ti.unit, COALESCE(ti.specs, ''),
			       COALESCE(c.name, '') as category_name
			FROM tender_items ti
			LEFT JOIN tender_categories c ON c.id = ti.category_id
			WHERE ti.tender_id = $1
			ORDER BY ti.created_at ASC
		`, t.ID)
		if itemErr == nil {
			for itemRows.Next() {
				var item entity.TenderItemResponse
				var catID sql.NullString
				if err := itemRows.Scan(&item.ID, &catID, &item.Name, &item.Quantity, &item.Unit, &item.Specs, &item.CategoryName); err == nil {
					if catID.Valid {
						parsed, _ := uuid.Parse(catID.String)
						item.CategoryID = &parsed
					}
					t.Items = append(t.Items, item)
				}
			}
			itemRows.Close()
		}

		tenders = append(tenders, t)
	}

	response.SuccessWithPagination(c, tenders, pagination)
}

// GetTenderBids lists all bids for a tender (Buyer who owns tender)
func (h *Handler) GetTenderBids(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	tenderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tender ID")
		return
	}

	// Check ownership
	var buyerID uuid.UUID
	err = h.db.QueryRow(`SELECT buyer_id FROM tender_tenders WHERE id = $1 AND deleted_at IS NULL`, tenderID).Scan(&buyerID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tender not found")
		return
	}
	if buyerID != userID {
		response.Forbidden(c, "Only the tender owner can view bids")
		return
	}

	rows, err := h.db.Query(`
		SELECT b.id, b.tender_id, b.supplier_id, b.total_price, b.currency,
		       b.delivery_days, b.status, b.note, b.attachment, b.rejection_reason, b.created_at,
		       COALESCE(cp.company_name, '') as supplier_name,
		       COALESCE(cp.phone, COALESCE(u.phone, '')) as supplier_phone,
		       COALESCE(u.email, '') as supplier_email,
		       COALESCE(cp.rating, 0) as supplier_rating
		FROM tender_bids b
		LEFT JOIN tender_company_profiles cp ON cp.id = b.supplier_id AND cp.deleted_at IS NULL
		LEFT JOIN tender_users u ON u.id = cp.user_id
		WHERE b.tender_id = $1
		ORDER BY b.total_price ASC
	`, tenderID)
	if err != nil {
		h.log.Error("Failed to list bids", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var bids []entity.BidResponse
	for rows.Next() {
		var b entity.BidResponse
		var attachment, rejectionReason sql.NullString

		err := rows.Scan(
			&b.ID, &b.TenderID, &b.SupplierID, &b.TotalPrice, &b.Currency,
			&b.DeliveryDays, &b.Status, &b.Note, &attachment, &rejectionReason, &b.CreatedAt,
			&b.SupplierName, &b.SupplierPhone, &b.SupplierEmail, &b.SupplierRating,
		)
		if err != nil {
			continue
		}
		if attachment.Valid {
			b.Attachment = attachment.String
		}
		if rejectionReason.Valid {
			b.RejectionReason = rejectionReason.String
		}

		// Get bid items
		itemRows, err := h.db.Query(`
			SELECT bi.id, bi.tender_item_id, bi.unit_price, bi.total_price, bi.note,
			       COALESCE(ti.name, '') as tender_item_name
			FROM tender_bid_items bi
			LEFT JOIN tender_items ti ON ti.id = bi.tender_item_id
			WHERE bi.bid_id = $1
		`, b.ID)
		if err == nil {
			defer itemRows.Close()
			for itemRows.Next() {
				var item entity.BidItemResponse
				if err := itemRows.Scan(&item.ID, &item.TenderItemID, &item.UnitPrice, &item.TotalPrice, &item.Note, &item.TenderItemName); err == nil {
					b.Items = append(b.Items, item)
				}
			}
		}

		bids = append(bids, b)
	}

	response.Success(c, bids)
}

// SubmitBid creates a bid for a tender (Supplier only)
func (h *Handler) SubmitBid(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	tenderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tender ID")
		return
	}

	// Check tender exists and is active
	var tenderStatus string
	var tenderDeadline time.Time
	err = h.db.QueryRow(`SELECT status, deadline FROM tender_tenders WHERE id = $1 AND deleted_at IS NULL`, tenderID).Scan(&tenderStatus, &tenderDeadline)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tender not found")
		return
	}
	if tenderStatus != "active" {
		response.BadRequest(c, "Tender is not active")
		return
	}
	if time.Now().After(tenderDeadline) {
		response.BadRequest(c, "Tender deadline has passed")
		return
	}

	// Look up the supplier's company profile ID
	var supplierProfileID uuid.UUID
	err = h.db.QueryRow(`SELECT id FROM tender_company_profiles WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&supplierProfileID)
	if err == sql.ErrNoRows {
		// Auto-create company profile from user registration data
		_, createErr := h.db.Exec(`
			INSERT INTO tender_company_profiles (id, user_id, role, company_name, inn, phone, region_id)
			SELECT u.id, u.id, u.role, COALESCE(NULLIF(u.company_name, ''), u.full_name), COALESCE(u.inn, ''), COALESCE(u.phone, ''), u.region_id
			FROM tender_users u WHERE u.id = $1
		`, userID)
		if createErr != nil {
			h.log.Error("Failed to auto-create company profile", "error", createErr)
			response.BadRequest(c, "Company profile could not be created. Please fill your Company Profile first.")
			return
		}
		// Re-fetch the newly created profile ID
		err = h.db.QueryRow(`SELECT id FROM tender_company_profiles WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&supplierProfileID)
		if err != nil {
			h.log.Error("Failed to look up auto-created supplier profile", "error", err)
			response.InternalServerError(c, "")
			return
		}
	} else if err != nil {
		h.log.Error("Failed to look up supplier profile", "error", err)
		response.InternalServerError(c, "")
		return
	}

	// Check supplier hasn't already submitted a bid
	var existingBidCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_bids WHERE tender_id = $1 AND supplier_id = $2`, tenderID, supplierProfileID).Scan(&existingBidCount)
	if existingBidCount > 0 {
		response.BadRequest(c, "You have already submitted a bid for this tender")
		return
	}

	var input entity.CreateBidInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalServerError(c, "")
		return
	}
	defer tx.Rollback()

	bidID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO tender_bids (id, tender_id, supplier_id, total_price, currency, delivery_days, status, note)
		VALUES ($1, $2, $3, $4, (SELECT currency FROM tender_tenders WHERE id = $2), $5, 'pending', $6)
	`, bidID, tenderID, supplierProfileID, input.TotalPrice, input.DeliveryDays, input.Note)
	if err != nil {
		h.log.Error("Failed to create bid", "error", err)
		response.InternalServerError(c, "Failed to create bid")
		return
	}

	// Insert bid items
	for _, item := range input.Items {
		itemID := uuid.New()
		totalPrice := item.UnitPrice * 1 // Will be calculated with quantity from tender_item
		_, err = tx.Exec(`
			INSERT INTO tender_bid_items (id, bid_id, tender_item_id, unit_price, total_price, note)
			VALUES ($1, $2, $3, $4, $4 * COALESCE((SELECT quantity FROM tender_items WHERE id = $3), 1), $5)
		`, itemID, bidID, item.TenderItemID, item.UnitPrice, item.Note)
		if err != nil {
			h.log.Error("Failed to create bid item", "error", err, "total_price", totalPrice)
			response.InternalServerError(c, "Failed to create bid item")
			return
		}
	}

	// Update bid count on tender
	tx.Exec(`UPDATE tender_tenders SET bid_count = bid_count + 1 WHERE id = $1`, tenderID)

	if err = tx.Commit(); err != nil {
		response.InternalServerError(c, "")
		return
	}

	// Create notification for buyer
	var buyerUserID uuid.UUID
	h.db.QueryRow(`SELECT buyer_id FROM tender_tenders WHERE id = $1`, tenderID).Scan(&buyerUserID)
	h.createTenderNotification(buyerUserID, entity.NotifNewBid, "Yangi taklif", "Tenderingizga yangi taklif keldi", map[string]interface{}{
		"tender_id": tenderID.String(),
		"bid_id":    bidID.String(),
	})

	response.Created(c, map[string]interface{}{"id": bidID})
}

// UpdateBid updates an existing bid (Supplier, before deadline)
func (h *Handler) UpdateBid(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	tenderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tender ID")
		return
	}

	bidID, err := uuid.Parse(c.Param("bid_id"))
	if err != nil {
		response.BadRequest(c, "Invalid bid ID")
		return
	}

	// Check bid ownership and tender deadline
	var bidSupplierUserID uuid.UUID
	var bidStatus string
	var deadline time.Time
	err = h.db.QueryRow(`
		SELECT cp.user_id, b.status, t.deadline
		FROM tender_bids b
		JOIN tender_tenders t ON t.id = b.tender_id
		JOIN tender_company_profiles cp ON cp.id = b.supplier_id
		WHERE b.id = $1 AND b.tender_id = $2
	`, bidID, tenderID).Scan(&bidSupplierUserID, &bidStatus, &deadline)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Bid not found")
		return
	}
	if bidSupplierUserID != userID {
		response.Forbidden(c, "Only the bid owner can update it")
		return
	}
	if bidStatus != "pending" {
		response.BadRequest(c, "Only pending bids can be updated")
		return
	}
	if time.Now().After(deadline) {
		response.BadRequest(c, "Tender deadline has passed")
		return
	}

	var input entity.UpdateBidInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	query := "UPDATE tender_bids SET updated_at = NOW()"
	args := []interface{}{}
	argIdx := 1

	if input.TotalPrice > 0 {
		query += fmt.Sprintf(", total_price = $%d", argIdx)
		args = append(args, input.TotalPrice)
		argIdx++
	}
	if input.DeliveryDays > 0 {
		query += fmt.Sprintf(", delivery_days = $%d", argIdx)
		args = append(args, input.DeliveryDays)
		argIdx++
	}
	if input.Note != "" {
		query += fmt.Sprintf(", note = $%d", argIdx)
		args = append(args, input.Note)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, bidID)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		response.InternalServerError(c, "Failed to update bid")
		return
	}

	response.Success(c, map[string]interface{}{"id": bidID, "message": "Bid updated"})
}

// AcceptBid accepts a bid and rejects all others (Buyer only)
func (h *Handler) AcceptBid(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	tenderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tender ID")
		return
	}

	bidID, err := uuid.Parse(c.Param("bid_id"))
	if err != nil {
		response.BadRequest(c, "Invalid bid ID")
		return
	}

	// Check tender ownership
	var buyerID uuid.UUID
	var tenderStatus string
	err = h.db.QueryRow(`SELECT buyer_id, status FROM tender_tenders WHERE id = $1 AND deleted_at IS NULL`, tenderID).Scan(&buyerID, &tenderStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tender not found")
		return
	}
	if buyerID != userID {
		response.Forbidden(c, "Only the tender owner can accept bids")
		return
	}
	if tenderStatus != "active" {
		response.BadRequest(c, "Tender is not active")
		return
	}

	var input entity.AcceptBidInput
	c.ShouldBindJSON(&input)

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalServerError(c, "")
		return
	}
	defer tx.Rollback()

	// Accept selected bid
	tx.Exec(`UPDATE tender_bids SET status = 'accepted', updated_at = NOW() WHERE id = $1`, bidID)

	// Reject all other bids
	tx.Exec(`UPDATE tender_bids SET status = 'rejected', rejection_reason = $1, updated_at = NOW() WHERE tender_id = $2 AND id != $3`,
		input.Reason, tenderID, bidID)

	// Update tender status
	tx.Exec(`UPDATE tender_tenders SET status = 'completed', selected_bid_id = $1, updated_at = NOW() WHERE id = $2`, bidID, tenderID)

	if err = tx.Commit(); err != nil {
		response.InternalServerError(c, "")
		return
	}

	// Notify winning supplier (look up user_id from company profile)
	var winnerUserID uuid.UUID
	h.db.QueryRow(`SELECT cp.user_id FROM tender_bids b JOIN tender_company_profiles cp ON cp.id = b.supplier_id WHERE b.id = $1`, bidID).Scan(&winnerUserID)
	h.createTenderNotification(winnerUserID, entity.NotifBidAccepted, "Taklif qabul qilindi!", "Sizning taklifingiz qabul qilindi", map[string]interface{}{
		"tender_id": tenderID.String(),
		"bid_id":    bidID.String(),
	})

	// Notify rejected suppliers (look up user_id from company profile)
	rejectedRows, _ := h.db.Query(`SELECT cp.user_id FROM tender_bids b JOIN tender_company_profiles cp ON cp.id = b.supplier_id WHERE b.tender_id = $1 AND b.id != $2`, tenderID, bidID)
	if rejectedRows != nil {
		defer rejectedRows.Close()
		for rejectedRows.Next() {
			var rejectedUserID uuid.UUID
			if rejectedRows.Scan(&rejectedUserID) == nil {
				h.createTenderNotification(rejectedUserID, entity.NotifBidRejected, "Taklif rad etildi", "Sizning taklifingiz qabul qilinmadi", map[string]interface{}{
					"tender_id": tenderID.String(),
				})
			}
		}
	}

	response.Success(c, map[string]interface{}{"message": "Bid accepted", "bid_id": bidID})
}

// GetMyBids lists bids submitted by the current supplier
func (h *Handler) GetMyBids(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	// Look up the supplier's company profile ID
	var supplierProfileID uuid.UUID
	err := h.db.QueryRow(`SELECT id FROM tender_company_profiles WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&supplierProfileID)
	if err == sql.ErrNoRows {
		// No company profile — return empty list
		response.Success(c, map[string]interface{}{
			"bids":       []interface{}{},
			"pagination": entity.NewPagination(1, 20),
		})
		return
	}
	if err != nil {
		h.log.Error("Failed to look up supplier profile", "error", err)
		response.InternalServerError(c, "")
		return
	}

	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	status := c.Query("status")

	pagination := entity.NewPagination(page, limit)

	countQuery := `SELECT COUNT(*) FROM tender_bids WHERE supplier_id = $1`
	countArgs := []interface{}{supplierProfileID}
	if status != "" {
		countQuery += " AND status = $2"
		countArgs = append(countArgs, status)
	}

	var total int
	h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	pagination.Calculate(total)

	query := `
		SELECT b.id, b.tender_id, b.supplier_id, b.total_price, b.currency,
		       b.delivery_days, b.status, b.note, b.attachment, b.rejection_reason, b.created_at,
		       COALESCE(t.title, '') as tender_title
		FROM tender_bids b
		LEFT JOIN tender_tenders t ON t.id = b.tender_id
		WHERE b.supplier_id = $1
	`
	queryArgs := []interface{}{supplierProfileID}
	argIdx := 2

	if status != "" {
		query += fmt.Sprintf(" AND b.status = $%d", argIdx)
		queryArgs = append(queryArgs, status)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY b.created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	queryArgs = append(queryArgs, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, queryArgs...)
	if err != nil {
		h.log.Error("Failed to list my bids", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	type BidWithTender struct {
		entity.BidResponse
		TenderTitle string `json:"tender_title"`
	}

	var bids []BidWithTender
	for rows.Next() {
		var b BidWithTender
		var attachment, rejectionReason sql.NullString

		err := rows.Scan(
			&b.ID, &b.TenderID, &b.SupplierID, &b.TotalPrice, &b.Currency,
			&b.DeliveryDays, &b.Status, &b.Note, &attachment, &rejectionReason, &b.CreatedAt,
			&b.TenderTitle,
		)
		if err != nil {
			continue
		}
		if attachment.Valid {
			b.Attachment = attachment.String
		}
		if rejectionReason.Valid {
			b.RejectionReason = rejectionReason.String
		}

		bids = append(bids, b)
	}

	response.SuccessWithPagination(c, bids, pagination)
}

// createTenderNotification helper to insert a notification
func (h *Handler) createTenderNotification(userID uuid.UUID, notifType, title, message string, data map[string]interface{}) {
	notifID := uuid.New()
	_, err := h.db.Exec(`
		INSERT INTO tender_notifications (id, user_id, type, title, message, data)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, notifID, userID, notifType, title, message, "{}") // simplified: data as empty JSON
	if err != nil {
		h.log.Error("Failed to create notification", "error", err)
	}
}
