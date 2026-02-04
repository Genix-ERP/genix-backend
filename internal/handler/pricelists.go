package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// PRICELIST HANDLERS
// =====================================================

// ListPricelists returns all pricelists for the tenant
func (h *Handler) ListPricelists(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var filter entity.PricelistListFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid filter parameters")
		return
	}

	query := `
		SELECT p.id, p.tenant_id, p.name, p.code, p.description, p.currency_id,
		       p.pricelist_type, p.start_date, p.end_date, p.priority,
		       p.show_discount, p.country_codes, p.is_active, p.is_default,
		       p.created_by, p.created_at, p.updated_at,
		       c.code as currency_code,
		       (SELECT COUNT(*) FROM pricelist_items WHERE pricelist_id = p.id) as item_count
		FROM pricelists p
		LEFT JOIN currencies c ON p.currency_id = c.id
		WHERE p.tenant_id = $1
	`
	args := []interface{}{tenantID}
	argCount := 1

	if filter.Search != "" {
		argCount++
		query += fmt.Sprintf(" AND (p.name ILIKE $%d OR p.code ILIKE $%d)", argCount, argCount)
		args = append(args, "%"+filter.Search+"%")
	}

	if filter.Type != "" {
		argCount++
		query += fmt.Sprintf(" AND p.pricelist_type = $%d", argCount)
		args = append(args, filter.Type)
	}

	if filter.IsActive != nil {
		argCount++
		query += fmt.Sprintf(" AND p.is_active = $%d", argCount)
		args = append(args, *filter.IsActive)
	}

	query += " ORDER BY p.is_default DESC, p.priority ASC, p.name ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query pricelists", "error", err)
		response.InternalServerError(c, "Failed to query pricelists")
		return
	}
	defer rows.Close()

	var pricelists []entity.PricelistResponse
	for rows.Next() {
		var p entity.PricelistResponse
		var code, description, currencyCode sql.NullString
		var currencyID, createdBy sql.NullString
		var startDate, endDate sql.NullTime

		if err := rows.Scan(
			&p.ID, &p.TenantID, &p.Name, &code, &description, &currencyID,
			&p.Type, &startDate, &endDate, &p.Priority,
			&p.ShowDiscount, &p.CountryCodes, &p.IsActive, &p.IsDefault,
			&createdBy, &p.CreatedAt, &p.UpdatedAt,
			&currencyCode, &p.ItemCount,
		); err != nil {
			h.log.Error("Failed to scan pricelist", "error", err)
			continue
		}

		if code.Valid {
			p.Code = &code.String
		}
		if description.Valid {
			p.Description = &description.String
		}
		if currencyID.Valid {
			uid, _ := uuid.Parse(currencyID.String)
			p.CurrencyID = &uid
		}
		if startDate.Valid {
			p.StartDate = &startDate.Time
		}
		if endDate.Valid {
			p.EndDate = &endDate.Time
		}
		if createdBy.Valid {
			uid, _ := uuid.Parse(createdBy.String)
			p.CreatedBy = &uid
		}
		if currencyCode.Valid {
			p.CurrencyCode = currencyCode.String
		}

		pricelists = append(pricelists, p)
	}

	response.Success(c, pricelists)
}

// GetPricelist returns a single pricelist with its items
func (h *Handler) GetPricelist(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	pricelistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid pricelist ID")
		return
	}

	// Get pricelist
	query := `
		SELECT p.id, p.tenant_id, p.name, p.code, p.description, p.currency_id,
		       p.pricelist_type, p.start_date, p.end_date, p.priority,
		       p.show_discount, p.country_codes, p.is_active, p.is_default,
		       p.created_by, p.created_at, p.updated_at
		FROM pricelists p
		WHERE p.id = $1 AND p.tenant_id = $2
	`

	var p entity.Pricelist
	var code, description sql.NullString
	var currencyID, createdBy sql.NullString
	var startDate, endDate sql.NullTime

	err = h.db.QueryRow(query, pricelistID, tenantID).Scan(
		&p.ID, &p.TenantID, &p.Name, &code, &description, &currencyID,
		&p.Type, &startDate, &endDate, &p.Priority,
		&p.ShowDiscount, &p.CountryCodes, &p.IsActive, &p.IsDefault,
		&createdBy, &p.CreatedAt, &p.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Pricelist not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get pricelist", "error", err)
		response.InternalServerError(c, "Failed to get pricelist")
		return
	}

	if code.Valid {
		p.Code = &code.String
	}
	if description.Valid {
		p.Description = &description.String
	}
	if currencyID.Valid {
		uid, _ := uuid.Parse(currencyID.String)
		p.CurrencyID = &uid
	}
	if startDate.Valid {
		p.StartDate = &startDate.Time
	}
	if endDate.Valid {
		p.EndDate = &endDate.Time
	}
	if createdBy.Valid {
		uid, _ := uuid.Parse(createdBy.String)
		p.CreatedBy = &uid
	}

	// Get pricelist items
	itemsQuery := `
		SELECT pi.id, pi.pricelist_id, pi.applied_on, pi.product_id, pi.category_id,
		       pi.variant_id, pi.min_quantity, pi.compute_price, pi.fixed_price,
		       pi.percent_price, pi.price_base, pi.base_pricelist_id,
		       pi.price_discount, pi.price_surcharge, pi.price_round,
		       pi.price_min_margin, pi.price_max_margin,
		       pi.date_start, pi.date_end, pi.sequence, pi.is_active,
		       pi.created_at, pi.updated_at,
		       pr.name as product_name, pc.name as category_name
		FROM pricelist_items pi
		LEFT JOIN products pr ON pi.product_id = pr.id
		LEFT JOIN product_categories pc ON pi.category_id = pc.id
		WHERE pi.pricelist_id = $1
		ORDER BY pi.sequence ASC, pi.min_quantity ASC
	`

	itemRows, err := h.db.Query(itemsQuery, pricelistID)
	if err != nil {
		h.log.Error("Failed to get pricelist items", "error", err)
		response.InternalServerError(c, "Failed to get pricelist items")
		return
	}
	defer itemRows.Close()

	var items []entity.PricelistItem
	for itemRows.Next() {
		var item entity.PricelistItem
		var productID, categoryID, variantID, basePricelistID sql.NullString
		var fixedPrice sql.NullFloat64
		var dateStart, dateEnd sql.NullTime
		var productName, categoryName sql.NullString

		if err := itemRows.Scan(
			&item.ID, &item.PricelistID, &item.AppliedOn, &productID, &categoryID,
			&variantID, &item.MinQuantity, &item.ComputePrice, &fixedPrice,
			&item.PercentPrice, &item.PriceBase, &basePricelistID,
			&item.PriceDiscount, &item.PriceSurcharge, &item.PriceRound,
			&item.PriceMinMargin, &item.PriceMaxMargin,
			&dateStart, &dateEnd, &item.Sequence, &item.IsActive,
			&item.CreatedAt, &item.UpdatedAt,
			&productName, &categoryName,
		); err != nil {
			h.log.Error("Failed to scan pricelist item", "error", err)
			continue
		}

		if productID.Valid {
			uid, _ := uuid.Parse(productID.String)
			item.ProductID = &uid
			if productName.Valid {
				item.Product = &entity.Product{Name: productName.String}
			}
		}
		if categoryID.Valid {
			uid, _ := uuid.Parse(categoryID.String)
			item.CategoryID = &uid
			if categoryName.Valid {
				item.Category = &entity.ProductCategory{Name: categoryName.String}
			}
		}
		if variantID.Valid {
			uid, _ := uuid.Parse(variantID.String)
			item.VariantID = &uid
		}
		if basePricelistID.Valid {
			uid, _ := uuid.Parse(basePricelistID.String)
			item.BasePricelistID = &uid
		}
		if fixedPrice.Valid {
			item.FixedPrice = &fixedPrice.Float64
		}
		if dateStart.Valid {
			item.DateStart = &dateStart.Time
		}
		if dateEnd.Valid {
			item.DateEnd = &dateEnd.Time
		}

		items = append(items, item)
	}

	p.Items = items
	response.Success(c, p)
}

// CreatePricelist creates a new pricelist
func (h *Handler) CreatePricelist(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreatePricelistInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalServerError(c, "Failed to create pricelist")
		return
	}
	defer tx.Rollback()

	// If setting as default, unset other defaults
	if input.IsDefault {
		_, err = tx.Exec(
			"UPDATE pricelists SET is_default = false WHERE tenant_id = $1 AND is_default = true",
			tenantID,
		)
		if err != nil {
			h.log.Error("Failed to unset default pricelist", "error", err)
			response.InternalServerError(c, "Failed to create pricelist")
			return
		}
	}

	// Set defaults
	pricelistType := entity.PricelistTypeSale
	if input.Type != "" {
		pricelistType = entity.PricelistType(input.Type)
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	showDiscount := true
	if input.ShowDiscount != nil {
		showDiscount = *input.ShowDiscount
	}

	priority := 100
	if input.Priority > 0 {
		priority = input.Priority
	}

	countryCodes, _ := json.Marshal(input.CountryCodes)

	// Insert pricelist
	query := `
		INSERT INTO pricelists (
			tenant_id, name, code, description, currency_id, pricelist_type,
			start_date, end_date, priority, show_discount, country_codes,
			is_active, is_default, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, created_at, updated_at
	`

	var pricelistID uuid.UUID
	var createdAt, updatedAt time.Time
	var currencyID *uuid.UUID
	var startDate, endDate *time.Time

	if input.CurrencyID != "" {
		uid, _ := uuid.Parse(input.CurrencyID)
		currencyID = &uid
	}
	if input.StartDate != "" {
		t, _ := time.Parse("2006-01-02", input.StartDate)
		startDate = &t
	}
	if input.EndDate != "" {
		t, _ := time.Parse("2006-01-02", input.EndDate)
		endDate = &t
	}

	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		userIDPtr = &userID
	}

	err = tx.QueryRow(
		query,
		tenantID, input.Name,
		sql.NullString{String: input.Code, Valid: input.Code != ""},
		sql.NullString{String: input.Description, Valid: input.Description != ""},
		currencyID, pricelistType, startDate, endDate,
		priority, showDiscount, countryCodes, isActive, input.IsDefault, userIDPtr,
	).Scan(&pricelistID, &createdAt, &updatedAt)

	if err != nil {
		h.log.Error("Failed to create pricelist", "error", err)
		response.InternalServerError(c, "Failed to create pricelist")
		return
	}

	// Insert pricelist items
	for _, item := range input.Items {
		if err := h.insertPricelistItem(tx, pricelistID, item); err != nil {
			h.log.Error("Failed to create pricelist item", "error", err)
			response.InternalServerError(c, "Failed to create pricelist items")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "Failed to create pricelist")
		return
	}

	response.Created(c, gin.H{
		"id":         pricelistID,
		"created_at": createdAt,
		"message":    "Pricelist created successfully",
	})
}

// UpdatePricelist updates an existing pricelist
func (h *Handler) UpdatePricelist(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	pricelistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid pricelist ID")
		return
	}

	var input entity.UpdatePricelistInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalServerError(c, "Failed to update pricelist")
		return
	}
	defer tx.Rollback()

	// If setting as default, unset other defaults
	if input.IsDefault != nil && *input.IsDefault {
		_, err = tx.Exec(
			"UPDATE pricelists SET is_default = false WHERE tenant_id = $1 AND is_default = true AND id != $2",
			tenantID, pricelistID,
		)
		if err != nil {
			h.log.Error("Failed to unset default pricelist", "error", err)
			response.InternalServerError(c, "Failed to update pricelist")
			return
		}
	}

	// Build update query dynamically
	query := "UPDATE pricelists SET updated_at = NOW()"
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		query += fmt.Sprintf(", name = $%d", argCount)
		args = append(args, *input.Name)
	}
	if input.Code != nil {
		argCount++
		query += fmt.Sprintf(", code = $%d", argCount)
		args = append(args, *input.Code)
	}
	if input.Description != nil {
		argCount++
		query += fmt.Sprintf(", description = $%d", argCount)
		args = append(args, *input.Description)
	}
	if input.CurrencyID != nil {
		argCount++
		if *input.CurrencyID == "" {
			query += fmt.Sprintf(", currency_id = $%d", argCount)
			args = append(args, nil)
		} else {
			query += fmt.Sprintf(", currency_id = $%d", argCount)
			uid, _ := uuid.Parse(*input.CurrencyID)
			args = append(args, uid)
		}
	}
	if input.Type != nil {
		argCount++
		query += fmt.Sprintf(", pricelist_type = $%d", argCount)
		args = append(args, *input.Type)
	}
	if input.StartDate != nil {
		argCount++
		if *input.StartDate == "" {
			query += fmt.Sprintf(", start_date = $%d", argCount)
			args = append(args, nil)
		} else {
			query += fmt.Sprintf(", start_date = $%d", argCount)
			t, _ := time.Parse("2006-01-02", *input.StartDate)
			args = append(args, t)
		}
	}
	if input.EndDate != nil {
		argCount++
		if *input.EndDate == "" {
			query += fmt.Sprintf(", end_date = $%d", argCount)
			args = append(args, nil)
		} else {
			query += fmt.Sprintf(", end_date = $%d", argCount)
			t, _ := time.Parse("2006-01-02", *input.EndDate)
			args = append(args, t)
		}
	}
	if input.Priority != nil {
		argCount++
		query += fmt.Sprintf(", priority = $%d", argCount)
		args = append(args, *input.Priority)
	}
	if input.ShowDiscount != nil {
		argCount++
		query += fmt.Sprintf(", show_discount = $%d", argCount)
		args = append(args, *input.ShowDiscount)
	}
	if input.CountryCodes != nil {
		argCount++
		query += fmt.Sprintf(", country_codes = $%d", argCount)
		countryCodes, _ := json.Marshal(input.CountryCodes)
		args = append(args, countryCodes)
	}
	if input.IsActive != nil {
		argCount++
		query += fmt.Sprintf(", is_active = $%d", argCount)
		args = append(args, *input.IsActive)
	}
	if input.IsDefault != nil {
		argCount++
		query += fmt.Sprintf(", is_default = $%d", argCount)
		args = append(args, *input.IsDefault)
	}

	argCount++
	query += fmt.Sprintf(" WHERE id = $%d", argCount)
	args = append(args, pricelistID)

	argCount++
	query += fmt.Sprintf(" AND tenant_id = $%d", argCount)
	args = append(args, tenantID)

	result, err := tx.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update pricelist", "error", err)
		response.InternalServerError(c, "Failed to update pricelist")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Pricelist not found")
		return
	}

	// Update items if provided
	if input.Items != nil {
		// Delete existing items
		_, err = tx.Exec("DELETE FROM pricelist_items WHERE pricelist_id = $1", pricelistID)
		if err != nil {
			h.log.Error("Failed to delete existing items", "error", err)
			response.InternalServerError(c, "Failed to update pricelist items")
			return
		}

		// Insert new items
		for _, item := range input.Items {
			if err := h.insertPricelistItem(tx, pricelistID, item); err != nil {
				h.log.Error("Failed to create pricelist item", "error", err)
				response.InternalServerError(c, "Failed to update pricelist items")
				return
			}
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "Failed to update pricelist")
		return
	}

	response.Success(c, gin.H{"message": "Pricelist updated successfully"})
}

// DeletePricelist deletes a pricelist
func (h *Handler) DeletePricelist(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	pricelistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid pricelist ID")
		return
	}

	// Check if it's the default pricelist
	var isDefault bool
	err = h.db.QueryRow(
		"SELECT is_default FROM pricelists WHERE id = $1 AND tenant_id = $2",
		pricelistID, tenantID,
	).Scan(&isDefault)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Pricelist not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to check pricelist", "error", err)
		response.InternalServerError(c, "Failed to delete pricelist")
		return
	}

	if isDefault {
		response.BadRequest(c, "Cannot delete the default pricelist")
		return
	}

	// Delete pricelist (items will be cascade deleted)
	result, err := h.db.Exec(
		"DELETE FROM pricelists WHERE id = $1 AND tenant_id = $2",
		pricelistID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete pricelist", "error", err)
		response.InternalServerError(c, "Failed to delete pricelist")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Pricelist not found")
		return
	}

	response.Success(c, gin.H{"message": "Pricelist deleted successfully"})
}

// GetProductPrice computes the price for a product based on pricelist rules
func (h *Handler) GetProductPrice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.GetPriceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	quantity := input.Quantity
	if quantity <= 0 {
		quantity = 1
	}

	date := time.Now()
	if input.Date != "" {
		date, _ = time.Parse("2006-01-02", input.Date)
	}

	// Get product base price
	var listPrice, costPrice float64
	var categoryID sql.NullString
	err = h.db.QueryRow(
		"SELECT list_price, cost_price, category_id FROM products WHERE id = $1 AND tenant_id = $2",
		productID, tenantID,
	).Scan(&listPrice, &costPrice, &categoryID)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Product not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get product", "error", err)
		response.InternalServerError(c, "Failed to get product price")
		return
	}

	// Determine which pricelist to use
	var pricelistID uuid.UUID
	var pricelistName string

	if input.PricelistID != "" {
		pricelistID, _ = uuid.Parse(input.PricelistID)
	} else if input.CustomerID != "" {
		// Get customer's default pricelist
		customerID, _ := uuid.Parse(input.CustomerID)
		var plID sql.NullString
		h.db.QueryRow(
			"SELECT pricelist_id FROM contacts WHERE id = $1 AND tenant_id = $2",
			customerID, tenantID,
		).Scan(&plID)
		if plID.Valid {
			pricelistID, _ = uuid.Parse(plID.String)
		}
	}

	// If no pricelist found, get default
	if pricelistID == uuid.Nil {
		h.db.QueryRow(
			"SELECT id, name FROM pricelists WHERE tenant_id = $1 AND is_default = true AND is_active = true",
			tenantID,
		).Scan(&pricelistID, &pricelistName)
	} else {
		h.db.QueryRow(
			"SELECT name FROM pricelists WHERE id = $1",
			pricelistID,
		).Scan(&pricelistName)
	}

	// If still no pricelist, return list price
	if pricelistID == uuid.Nil {
		response.Success(c, entity.GetPriceOutput{
			ProductID:     productID,
			OriginalPrice: listPrice,
			ComputedPrice: listPrice,
		})
		return
	}

	// Find matching pricelist items
	computedPrice, ruleApplied, minQty := h.computePriceFromPricelist(
		pricelistID, productID, categoryID, quantity, date, listPrice, costPrice, tenantID,
	)

	discountAmount := listPrice - computedPrice
	discountPercent := 0.0
	if listPrice > 0 {
		discountPercent = (discountAmount / listPrice) * 100
	}

	response.Success(c, entity.GetPriceOutput{
		ProductID:       productID,
		PricelistID:     &pricelistID,
		PricelistName:   pricelistName,
		OriginalPrice:   listPrice,
		ComputedPrice:   computedPrice,
		DiscountPercent: discountPercent,
		DiscountAmount:  discountAmount,
		RuleApplied:     ruleApplied,
		MinQuantity:     minQty,
	})
}

// GetBulkProductPrices computes prices for multiple products
func (h *Handler) GetBulkProductPrices(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.GetBulkPriceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	quantity := input.Quantity
	if quantity <= 0 {
		quantity = 1
	}

	date := time.Now()
	if input.Date != "" {
		date, _ = time.Parse("2006-01-02", input.Date)
	}

	// Determine pricelist
	var pricelistID uuid.UUID
	if input.PricelistID != "" {
		pricelistID, _ = uuid.Parse(input.PricelistID)
	} else if input.CustomerID != "" {
		customerID, _ := uuid.Parse(input.CustomerID)
		var plID sql.NullString
		h.db.QueryRow(
			"SELECT pricelist_id FROM contacts WHERE id = $1 AND tenant_id = $2",
			customerID, tenantID,
		).Scan(&plID)
		if plID.Valid {
			pricelistID, _ = uuid.Parse(plID.String)
		}
	}

	if pricelistID == uuid.Nil {
		h.db.QueryRow(
			"SELECT id FROM pricelists WHERE tenant_id = $1 AND is_default = true AND is_active = true",
			tenantID,
		).Scan(&pricelistID)
	}

	var results []entity.GetPriceOutput

	for _, pidStr := range input.ProductIDs {
		productID, err := uuid.Parse(pidStr)
		if err != nil {
			continue
		}

		var listPrice, costPrice float64
		var categoryID sql.NullString
		err = h.db.QueryRow(
			"SELECT list_price, cost_price, category_id FROM products WHERE id = $1 AND tenant_id = $2",
			productID, tenantID,
		).Scan(&listPrice, &costPrice, &categoryID)

		if err != nil {
			continue
		}

		computedPrice := listPrice
		ruleApplied := ""
		minQty := 1.0

		if pricelistID != uuid.Nil {
			computedPrice, ruleApplied, minQty = h.computePriceFromPricelist(
				pricelistID, productID, categoryID, quantity, date, listPrice, costPrice, tenantID,
			)
		}

		discountAmount := listPrice - computedPrice
		discountPercent := 0.0
		if listPrice > 0 {
			discountPercent = (discountAmount / listPrice) * 100
		}

		results = append(results, entity.GetPriceOutput{
			ProductID:       productID,
			PricelistID:     &pricelistID,
			OriginalPrice:   listPrice,
			ComputedPrice:   computedPrice,
			DiscountPercent: discountPercent,
			DiscountAmount:  discountAmount,
			RuleApplied:     ruleApplied,
			MinQuantity:     minQty,
		})
	}

	response.Success(c, results)
}

// Helper function to insert a pricelist item
func (h *Handler) insertPricelistItem(tx *sql.Tx, pricelistID uuid.UUID, item entity.PricelistItemInput) error {
	appliedOn := entity.AppliedOnAllProducts
	if item.AppliedOn != "" {
		appliedOn = entity.AppliedOn(item.AppliedOn)
	}

	computePrice := entity.ComputePriceFixed
	if item.ComputePrice != "" {
		computePrice = entity.ComputePrice(item.ComputePrice)
	}

	priceBase := entity.PriceBaseListPrice
	if item.PriceBase != "" {
		priceBase = entity.PriceBase(item.PriceBase)
	}

	isActive := true
	if item.IsActive != nil {
		isActive = *item.IsActive
	}

	minQuantity := 1.0
	if item.MinQuantity > 0 {
		minQuantity = item.MinQuantity
	}

	priceRound := 0.01
	if item.PriceRound > 0 {
		priceRound = item.PriceRound
	}

	sequence := 10
	if item.Sequence > 0 {
		sequence = item.Sequence
	}

	query := `
		INSERT INTO pricelist_items (
			pricelist_id, applied_on, product_id, category_id, variant_id,
			min_quantity, compute_price, fixed_price, percent_price,
			price_base, base_pricelist_id, price_discount, price_surcharge,
			price_round, price_min_margin, price_max_margin,
			date_start, date_end, sequence, is_active
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	`

	var productID, categoryID, variantID, basePricelistID *uuid.UUID
	var dateStart, dateEnd *time.Time

	if item.ProductID != "" {
		uid, _ := uuid.Parse(item.ProductID)
		productID = &uid
	}
	if item.CategoryID != "" {
		uid, _ := uuid.Parse(item.CategoryID)
		categoryID = &uid
	}
	if item.VariantID != "" {
		uid, _ := uuid.Parse(item.VariantID)
		variantID = &uid
	}
	if item.BasePricelistID != "" {
		uid, _ := uuid.Parse(item.BasePricelistID)
		basePricelistID = &uid
	}
	if item.DateStart != "" {
		t, _ := time.Parse("2006-01-02", item.DateStart)
		dateStart = &t
	}
	if item.DateEnd != "" {
		t, _ := time.Parse("2006-01-02", item.DateEnd)
		dateEnd = &t
	}

	_, err := tx.Exec(
		query,
		pricelistID, appliedOn, productID, categoryID, variantID,
		minQuantity, computePrice, item.FixedPrice, item.PercentPrice,
		priceBase, basePricelistID, item.PriceDiscount, item.PriceSurcharge,
		priceRound, item.PriceMinMargin, item.PriceMaxMargin,
		dateStart, dateEnd, sequence, isActive,
	)

	return err
}

// Helper function to compute price from pricelist
func (h *Handler) computePriceFromPricelist(
	pricelistID, productID uuid.UUID,
	categoryID sql.NullString,
	quantity float64,
	date time.Time,
	listPrice, costPrice float64,
	tenantID uuid.UUID,
) (float64, string, float64) {
	// Query for matching pricelist items, ordered by specificity
	query := `
		SELECT pi.id, pi.applied_on, pi.min_quantity, pi.compute_price,
		       pi.fixed_price, pi.percent_price, pi.price_base,
		       pi.price_discount, pi.price_surcharge, pi.price_round,
		       pi.price_min_margin, pi.price_max_margin
		FROM pricelist_items pi
		WHERE pi.pricelist_id = $1
		  AND pi.is_active = true
		  AND pi.min_quantity <= $2
		  AND (pi.date_start IS NULL OR pi.date_start <= $3)
		  AND (pi.date_end IS NULL OR pi.date_end >= $3)
		  AND (
		      pi.applied_on = 'all_products'
		      OR (pi.applied_on = 'product' AND pi.product_id = $4)
		      OR (pi.applied_on = 'product_category' AND pi.category_id = $5)
		  )
		ORDER BY
		  CASE pi.applied_on
		    WHEN 'product' THEN 1
		    WHEN 'product_variant' THEN 2
		    WHEN 'product_category' THEN 3
		    WHEN 'all_products' THEN 4
		  END,
		  pi.min_quantity DESC,
		  pi.sequence ASC
		LIMIT 1
	`

	var catID *uuid.UUID
	if categoryID.Valid {
		uid, _ := uuid.Parse(categoryID.String)
		catID = &uid
	}

	var itemID uuid.UUID
	var appliedOn string
	var minQty float64
	var computePrice string
	var fixedPrice sql.NullFloat64
	var percentPrice, priceDiscount, priceSurcharge, priceRound float64
	var priceBase string
	var priceMinMargin, priceMaxMargin float64

	err := h.db.QueryRow(
		query, pricelistID, quantity, date, productID, catID,
	).Scan(
		&itemID, &appliedOn, &minQty, &computePrice,
		&fixedPrice, &percentPrice, &priceBase,
		&priceDiscount, &priceSurcharge, &priceRound,
		&priceMinMargin, &priceMaxMargin,
	)

	if err != nil {
		// No matching rule, return list price
		return listPrice, "", 1
	}

	var computedPrice float64
	ruleApplied := appliedOn

	switch computePrice {
	case "fixed_price":
		if fixedPrice.Valid {
			computedPrice = fixedPrice.Float64
		} else {
			computedPrice = listPrice
		}
		ruleApplied = "fixed_price"

	case "percentage":
		// Percentage discount from list price
		computedPrice = listPrice * (1 - percentPrice/100)
		ruleApplied = fmt.Sprintf("%.1f%% discount", percentPrice)

	case "formula":
		// Get base price
		basePrice := listPrice
		if priceBase == "cost_price" {
			basePrice = costPrice
		}

		// Apply discount
		computedPrice = basePrice * (1 - priceDiscount/100)

		// Apply surcharge
		computedPrice += priceSurcharge

		// Apply rounding
		if priceRound > 0 {
			computedPrice = math.Round(computedPrice/priceRound) * priceRound
		}

		// Apply margin constraints
		if priceMinMargin > 0 && costPrice > 0 {
			minPrice := costPrice * (1 + priceMinMargin/100)
			if computedPrice < minPrice {
				computedPrice = minPrice
			}
		}
		if priceMaxMargin > 0 && costPrice > 0 {
			maxPrice := costPrice * (1 + priceMaxMargin/100)
			if computedPrice > maxPrice {
				computedPrice = maxPrice
			}
		}

		ruleApplied = "formula"

	default:
		computedPrice = listPrice
	}

	// Ensure price is not negative
	if computedPrice < 0 {
		computedPrice = 0
	}

	return computedPrice, ruleApplied, minQty
}

// GetDefaultPricelist returns the default pricelist for the tenant
func (h *Handler) GetDefaultPricelist(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, name, code, pricelist_type, is_active
		FROM pricelists
		WHERE tenant_id = $1 AND is_default = true
		LIMIT 1
	`

	var p struct {
		ID       uuid.UUID `json:"id"`
		Name     string    `json:"name"`
		Code     *string   `json:"code"`
		Type     string    `json:"pricelist_type"`
		IsActive bool      `json:"is_active"`
	}

	var code sql.NullString
	err := h.db.QueryRow(query, tenantID).Scan(&p.ID, &p.Name, &code, &p.Type, &p.IsActive)

	if err == sql.ErrNoRows {
		response.NotFound(c, "No default pricelist found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get default pricelist", "error", err)
		response.InternalServerError(c, "Failed to get default pricelist")
		return
	}

	if code.Valid {
		p.Code = &code.String
	}

	response.Success(c, p)
}

// SetDefaultPricelist sets a pricelist as the default
func (h *Handler) SetDefaultPricelist(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	pricelistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid pricelist ID")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalServerError(c, "Failed to set default pricelist")
		return
	}
	defer tx.Rollback()

	// Unset current default
	_, err = tx.Exec(
		"UPDATE pricelists SET is_default = false WHERE tenant_id = $1 AND is_default = true",
		tenantID,
	)
	if err != nil {
		h.log.Error("Failed to unset default", "error", err)
		response.InternalServerError(c, "Failed to set default pricelist")
		return
	}

	// Set new default
	result, err := tx.Exec(
		"UPDATE pricelists SET is_default = true WHERE id = $1 AND tenant_id = $2",
		pricelistID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to set default", "error", err)
		response.InternalServerError(c, "Failed to set default pricelist")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Pricelist not found")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit", "error", err)
		response.InternalServerError(c, "Failed to set default pricelist")
		return
	}

	response.Success(c, gin.H{"message": "Default pricelist updated successfully"})
}

// AssignCustomerPricelist assigns a pricelist to a customer
func (h *Handler) AssignCustomerPricelist(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CustomerPricelistInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	customerID, err := uuid.Parse(input.CustomerID)
	if err != nil {
		response.BadRequest(c, "Invalid customer ID")
		return
	}

	pricelistID, err := uuid.Parse(input.PricelistID)
	if err != nil {
		response.BadRequest(c, "Invalid pricelist ID")
		return
	}

	// Update customer's default pricelist
	_, err = h.db.Exec(
		"UPDATE contacts SET pricelist_id = $1 WHERE id = $2 AND tenant_id = $3",
		pricelistID, customerID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to assign pricelist to customer", "error", err)
		response.InternalServerError(c, "Failed to assign pricelist")
		return
	}

	response.Success(c, gin.H{"message": "Pricelist assigned to customer successfully"})
}
