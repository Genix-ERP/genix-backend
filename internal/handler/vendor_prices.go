package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// VendorPriceResponse represents a vendor price record
type VendorPriceResponse struct {
	ID           uuid.UUID  `json:"id"`
	VendorID     uuid.UUID  `json:"vendor_id"`
	VendorName   string     `json:"vendor_name"`
	ProductID    uuid.UUID  `json:"product_id"`
	ProductName  string     `json:"product_name"`
	Price        float64    `json:"price"`
	Currency     string     `json:"currency"`
	MinQuantity  float64    `json:"min_quantity"`
	LeadTimeDays int        `json:"lead_time_days"`
	ValidFrom    *time.Time `json:"valid_from,omitempty"`
	ValidUntil   *time.Time `json:"valid_until,omitempty"`
	Notes        *string    `json:"notes,omitempty"`
	IsActive     bool       `json:"is_active"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// CreateVendorPriceInput represents input for creating a vendor price
type CreateVendorPriceInput struct {
	VendorID     string  `json:"vendor_id" binding:"required"`
	ProductID    string  `json:"product_id" binding:"required"`
	Price        float64 `json:"price" binding:"required"`
	Currency     string  `json:"currency"`
	MinQuantity  float64 `json:"min_quantity"`
	LeadTimeDays int     `json:"lead_time_days"`
	ValidFrom    string  `json:"valid_from"`
	ValidUntil   string  `json:"valid_until"`
	Notes        string  `json:"notes"`
	IsActive     *bool   `json:"is_active"`
}

// UpdateVendorPriceInput represents input for updating a vendor price
type UpdateVendorPriceInput struct {
	Price        *float64 `json:"price"`
	Currency     string   `json:"currency"`
	MinQuantity  *float64 `json:"min_quantity"`
	LeadTimeDays *int     `json:"lead_time_days"`
	ValidFrom    string   `json:"valid_from"`
	ValidUntil   string   `json:"valid_until"`
	Notes        string   `json:"notes"`
	IsActive     *bool    `json:"is_active"`
}

// ListVendorPrices returns a paginated list of vendor prices
func (h *Handler) ListVendorPrices(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := (page - 1) * limit

	vendorID := c.Query("vendor_id")
	productID := c.Query("product_id")
	activeOnly := c.Query("active") == "true"

	baseQuery := `
		SELECT vp.id, vp.vendor_id, COALESCE(v.name, '') as vendor_name,
			   vp.product_id, COALESCE(p.name, '') as product_name,
			   vp.price, COALESCE(vp.currency, 'UZS') as currency,
			   COALESCE(vp.min_quantity, 1) as min_quantity,
			   COALESCE(vp.lead_time_days, 0) as lead_time_days,
			   vp.valid_from, vp.valid_until, vp.notes, vp.is_active,
			   vp.created_at, vp.updated_at
		FROM vendor_prices vp
		LEFT JOIN contacts v ON vp.vendor_id = v.id
		LEFT JOIN products p ON vp.product_id = p.id
		WHERE vp.tenant_id = $1 AND vp.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM vendor_prices vp WHERE vp.tenant_id = $1 AND vp.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND vp.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND vp.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if vendorID != "" {
		if vid, err := uuid.Parse(vendorID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND vp.vendor_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND vp.vendor_id = $%d", argCount)
			args = append(args, vid)
		}
	}

	if productID != "" {
		if pid, err := uuid.Parse(productID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND vp.product_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND vp.product_id = $%d", argCount)
			args = append(args, pid)
		}
	}

	if activeOnly {
		baseQuery += " AND vp.is_active = true"
		countQuery += " AND vp.is_active = true"
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count vendor prices", "error", err)
		response.InternalError(c, "Failed to list vendor prices")
		return
	}

	baseQuery += " ORDER BY v.name, p.name, vp.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list vendor prices", "error", err)
		response.InternalError(c, "Failed to list vendor prices")
		return
	}
	defer rows.Close()

	records := make([]*VendorPriceResponse, 0)
	for rows.Next() {
		var record VendorPriceResponse
		var notes sql.NullString
		var validFrom, validUntil sql.NullTime

		err := rows.Scan(
			&record.ID, &record.VendorID, &record.VendorName,
			&record.ProductID, &record.ProductName,
			&record.Price, &record.Currency,
			&record.MinQuantity, &record.LeadTimeDays,
			&validFrom, &validUntil, &notes, &record.IsActive,
			&record.CreatedAt, &record.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan vendor price", "error", err)
			continue
		}

		if notes.Valid {
			record.Notes = &notes.String
		}
		if validFrom.Valid {
			record.ValidFrom = &validFrom.Time
		}
		if validUntil.Valid {
			record.ValidUntil = &validUntil.Time
		}

		records = append(records, &record)
	}

	// `total` is computed above and used to be thrown away, so the client got a
	// bare array with no way to know a page 2 existed.
	response.Paginated(c, records, page, limit, total)
}

// GetVendorPrice returns a single vendor price record
func (h *Handler) GetVendorPrice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid vendor price ID")
		return
	}

	var record VendorPriceResponse
	var notes sql.NullString
	var validFrom, validUntil sql.NullTime

	err = h.db.QueryRow(`
		SELECT vp.id, vp.vendor_id, COALESCE(v.name, '') as vendor_name,
			   vp.product_id, COALESCE(p.name, '') as product_name,
			   vp.price, COALESCE(vp.currency, 'UZS') as currency,
			   COALESCE(vp.min_quantity, 1) as min_quantity,
			   COALESCE(vp.lead_time_days, 0) as lead_time_days,
			   vp.valid_from, vp.valid_until, vp.notes, vp.is_active,
			   vp.created_at, vp.updated_at
		FROM vendor_prices vp
		LEFT JOIN contacts v ON vp.vendor_id = v.id
		LEFT JOIN products p ON vp.product_id = p.id
		WHERE vp.id = $1 AND vp.tenant_id = $2 AND vp.deleted_at IS NULL
	`, id, tenantID).Scan(
		&record.ID, &record.VendorID, &record.VendorName,
		&record.ProductID, &record.ProductName,
		&record.Price, &record.Currency,
		&record.MinQuantity, &record.LeadTimeDays,
		&validFrom, &validUntil, &notes, &record.IsActive,
		&record.CreatedAt, &record.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Vendor price not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get vendor price", "error", err)
		response.InternalError(c, "Failed to get vendor price")
		return
	}

	if notes.Valid {
		record.Notes = &notes.String
	}
	if validFrom.Valid {
		record.ValidFrom = &validFrom.Time
	}
	if validUntil.Valid {
		record.ValidUntil = &validUntil.Time
	}

	response.Success(c, record)
}

// CreateVendorPrice creates a new vendor price record
func (h *Handler) CreateVendorPrice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input CreateVendorPriceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor_id")
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product_id")
		return
	}

	// Verify vendor exists
	var vendorExists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND type = 'vendor')", vendorID).Scan(&vendorExists)
	if err != nil || !vendorExists {
		response.NotFound(c, "Vendor not found")
		return
	}

	// Verify product exists
	var productExists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM products WHERE id = $1 AND tenant_id = $2)", productID, tenantID).Scan(&productExists)
	if err != nil || !productExists {
		response.NotFound(c, "Product not found")
		return
	}

	id := uuid.New()
	now := time.Now()

	currency := input.Currency
	if currency == "" {
		currency = "UZS"
	}

	minQuantity := input.MinQuantity
	if minQuantity <= 0 {
		minQuantity = 1
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	var validFrom, validUntil *time.Time
	if input.ValidFrom != "" {
		if parsed, err := time.Parse("2006-01-02", input.ValidFrom); err == nil {
			validFrom = &parsed
		}
	}
	if input.ValidUntil != "" {
		if parsed, err := time.Parse("2006-01-02", input.ValidUntil); err == nil {
			validUntil = &parsed
		}
	}

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO vendor_prices
		(id, tenant_id, organization_id, vendor_id, product_id, price, currency, min_quantity, lead_time_days,
		 valid_from, valid_until, notes, is_active, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15)
	`

	_, err = h.db.Exec(query, id, tenantID, orgIDPtr, vendorID, productID, input.Price, currency,
		minQuantity, input.LeadTimeDays, validFrom, validUntil, notes, isActive, userID, now)
	if err != nil {
		h.log.Error("Failed to create vendor price", "error", err)
		response.InternalError(c, "Failed to create vendor price")
		return
	}

	// Fetch the created record
	var record VendorPriceResponse
	var notesResp sql.NullString
	var validFromResp, validUntilResp sql.NullTime

	err = h.db.QueryRow(`
		SELECT vp.id, vp.vendor_id, COALESCE(v.name, '') as vendor_name,
			   vp.product_id, COALESCE(p.name, '') as product_name,
			   vp.price, COALESCE(vp.currency, 'UZS') as currency,
			   COALESCE(vp.min_quantity, 1) as min_quantity,
			   COALESCE(vp.lead_time_days, 0) as lead_time_days,
			   vp.valid_from, vp.valid_until, vp.notes, vp.is_active,
			   vp.created_at, vp.updated_at
		FROM vendor_prices vp
		LEFT JOIN contacts v ON vp.vendor_id = v.id
		LEFT JOIN products p ON vp.product_id = p.id
		WHERE vp.id = $1
	`, id).Scan(
		&record.ID, &record.VendorID, &record.VendorName,
		&record.ProductID, &record.ProductName,
		&record.Price, &record.Currency,
		&record.MinQuantity, &record.LeadTimeDays,
		&validFromResp, &validUntilResp, &notesResp, &record.IsActive,
		&record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		h.log.Error("Failed to fetch created vendor price", "error", err)
		response.Created(c, gin.H{"id": id, "message": "Vendor price created successfully"})
		return
	}

	if notesResp.Valid {
		record.Notes = &notesResp.String
	}
	if validFromResp.Valid {
		record.ValidFrom = &validFromResp.Time
	}
	if validUntilResp.Valid {
		record.ValidUntil = &validUntilResp.Time
	}

	response.Created(c, record)
}

// UpdateVendorPrice updates a vendor price record
func (h *Handler) UpdateVendorPrice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid vendor price ID")
		return
	}

	var input UpdateVendorPriceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Price != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("price = $%d", argCount))
		args = append(args, *input.Price)
	}

	if input.Currency != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("currency = $%d", argCount))
		args = append(args, input.Currency)
	}

	if input.MinQuantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("min_quantity = $%d", argCount))
		args = append(args, *input.MinQuantity)
	}

	if input.LeadTimeDays != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("lead_time_days = $%d", argCount))
		args = append(args, *input.LeadTimeDays)
	}

	if input.ValidFrom != "" {
		argCount++
		if parsed, err := time.Parse("2006-01-02", input.ValidFrom); err == nil {
			updates = append(updates, fmt.Sprintf("valid_from = $%d", argCount))
			args = append(args, parsed)
		}
	}

	if input.ValidUntil != "" {
		argCount++
		if parsed, err := time.Parse("2006-01-02", input.ValidUntil); err == nil {
			updates = append(updates, fmt.Sprintf("valid_until = $%d", argCount))
			args = append(args, parsed)
		}
	}

	if input.Notes != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, input.Notes)
	}

	if input.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause parameters
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE vendor_prices SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		joinStrings(updates, ", "), argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update vendor price", "error", err)
		response.InternalError(c, "Failed to update vendor price")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Vendor price not found")
		return
	}

	// Fetch updated record
	var record VendorPriceResponse
	var notes sql.NullString
	var validFrom, validUntil sql.NullTime

	err = h.db.QueryRow(`
		SELECT vp.id, vp.vendor_id, COALESCE(v.name, '') as vendor_name,
			   vp.product_id, COALESCE(p.name, '') as product_name,
			   vp.price, COALESCE(vp.currency, 'UZS') as currency,
			   COALESCE(vp.min_quantity, 1) as min_quantity,
			   COALESCE(vp.lead_time_days, 0) as lead_time_days,
			   vp.valid_from, vp.valid_until, vp.notes, vp.is_active,
			   vp.created_at, vp.updated_at
		FROM vendor_prices vp
		LEFT JOIN contacts v ON vp.vendor_id = v.id
		LEFT JOIN products p ON vp.product_id = p.id
		WHERE vp.id = $1
	`, id).Scan(
		&record.ID, &record.VendorID, &record.VendorName,
		&record.ProductID, &record.ProductName,
		&record.Price, &record.Currency,
		&record.MinQuantity, &record.LeadTimeDays,
		&validFrom, &validUntil, &notes, &record.IsActive,
		&record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		response.Success(c, gin.H{"message": "Vendor price updated successfully"})
		return
	}

	if notes.Valid {
		record.Notes = &notes.String
	}
	if validFrom.Valid {
		record.ValidFrom = &validFrom.Time
	}
	if validUntil.Valid {
		record.ValidUntil = &validUntil.Time
	}

	response.Success(c, record)
}

// DeleteVendorPrice soft-deletes a vendor price record
func (h *Handler) DeleteVendorPrice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid vendor price ID")
		return
	}

	result, err := h.db.Exec("UPDATE vendor_prices SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete vendor price", "error", err)
		response.InternalError(c, "Failed to delete vendor price")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Vendor price not found")
		return
	}

	response.Success(c, gin.H{"message": "Vendor price deleted successfully"})
}

// LookupVendorPrice looks up price for a specific vendor+product combination
// GET /api/v1/vendor-prices/lookup?vendor_id=xxx&product_id=xxx
func (h *Handler) LookupVendorPrice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	vendorIDStr := c.Query("vendor_id")
	productIDStr := c.Query("product_id")

	if vendorIDStr == "" || productIDStr == "" {
		response.BadRequest(c, "vendor_id and product_id are required")
		return
	}

	vendorID, err := uuid.Parse(vendorIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid vendor_id")
		return
	}

	productID, err := uuid.Parse(productIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid product_id")
		return
	}

	// Look for active price that is currently valid (within valid_from/valid_until range)
	var record VendorPriceResponse
	var notes sql.NullString
	var validFrom, validUntil sql.NullTime

	err = h.db.QueryRow(`
		SELECT vp.id, vp.vendor_id, COALESCE(v.name, '') as vendor_name,
			   vp.product_id, COALESCE(p.name, '') as product_name,
			   vp.price, COALESCE(vp.currency, 'UZS') as currency,
			   COALESCE(vp.min_quantity, 1) as min_quantity,
			   COALESCE(vp.lead_time_days, 0) as lead_time_days,
			   vp.valid_from, vp.valid_until, vp.notes, vp.is_active,
			   vp.created_at, vp.updated_at
		FROM vendor_prices vp
		LEFT JOIN contacts v ON vp.vendor_id = v.id
		LEFT JOIN products p ON vp.product_id = p.id
		WHERE vp.tenant_id = $1
		  AND vp.vendor_id = $2
		  AND vp.product_id = $3
		  AND vp.is_active = true
		  AND vp.deleted_at IS NULL
		  AND (vp.valid_from IS NULL OR vp.valid_from <= CURRENT_DATE)
		  AND (vp.valid_until IS NULL OR vp.valid_until >= CURRENT_DATE)
		ORDER BY vp.min_quantity ASC, vp.created_at DESC
		LIMIT 1
	`, tenantID, vendorID, productID).Scan(
		&record.ID, &record.VendorID, &record.VendorName,
		&record.ProductID, &record.ProductName,
		&record.Price, &record.Currency,
		&record.MinQuantity, &record.LeadTimeDays,
		&validFrom, &validUntil, &notes, &record.IsActive,
		&record.CreatedAt, &record.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "No price found for this vendor and product")
		return
	}
	if err != nil {
		h.log.Error("Failed to lookup vendor price", "error", err)
		response.InternalError(c, "Failed to lookup vendor price")
		return
	}

	if notes.Valid {
		record.Notes = &notes.String
	}
	if validFrom.Valid {
		record.ValidFrom = &validFrom.Time
	}
	if validUntil.Valid {
		record.ValidUntil = &validUntil.Time
	}

	response.Success(c, record)
}
