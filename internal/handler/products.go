package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// PRODUCT HANDLERS
// =====================================================

// resolveUOMCode looks up a units_of_measure UUID by code string (e.g. "kg", "unit", "m").
// The frontend stores UOM as string codes; this resolves them to proper FK UUIDs.
func (h *Handler) resolveUOMCode(tenantID uuid.UUID, code string) *uuid.UUID {
	if code == "" {
		return nil
	}
	var id uuid.UUID
	err := h.db.QueryRow(
		"SELECT id FROM units_of_measure WHERE tenant_id = $1 AND (code ILIKE $2 OR name ILIKE $2) AND is_active = true LIMIT 1",
		tenantID, code,
	).Scan(&id)
	if err != nil {
		return nil
	}
	return &id
}

// ListProducts returns a paginated list of products
func (h *Handler) ListProducts(c *gin.Context) {
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
	categoryID := c.Query("category_id")
	productType := c.Query("type")
	includeInactive := c.Query("include_inactive") == "true"

	// Build query - products are shared across orgs, org-specific data comes from product_organization_settings
	orgID, _ := middleware.GetOrganizationID(c)

	baseQuery := `
		SELECT p.id, p.tenant_id, p.category_id, p.type, p.code, p.sku, p.barcode,
			   p.name, p.description, p.short_description, p.unit_id,
			   COALESCE(pos.cost_price, p.cost_price) as cost_price,
			   COALESCE(pos.list_price, p.list_price) as list_price,
			   COALESCE(pos.min_price, p.min_price) as min_price,
			   p.is_stockable, p.track_inventory,
			   COALESCE(pos.min_stock_level, p.min_stock_level) as min_stock_level,
			   COALESCE(pos.reorder_point, p.reorder_point) as reorder_point,
			   COALESCE(pos.reorder_quantity, p.reorder_quantity) as reorder_quantity,
			   p.lead_time_days,
			   p.is_purchasable, p.is_sellable,
			   COALESCE(p.can_be_sold, p.is_sellable) as can_be_sold,
			   COALESCE(p.can_be_purchased, p.is_purchasable) as can_be_purchased,
			   COALESCE(p.available_in_pos, false) as available_in_pos,
			   COALESCE(p.can_be_expensed, false) as can_be_expensed,
			   COALESCE(p.can_be_rented, false) as can_be_rented,
			   COALESCE(p.can_be_subcontracted, false) as can_be_subcontracted,
			   COALESCE(p.is_overhead_expense, false) as is_overhead_expense,
			   COALESCE(p.has_variants, false) as has_variants,
			   p.is_active, p.tags, COALESCE(p.image_url, '') as image_url,
			   p.created_at, p.updated_at,
			   pc.code as category_code, pc.name as category_name,
			   COALESCE(u.name, '') as unit_name,
			   p.purchase_unit_id, COALESCE(pu.name, '') as purchase_unit_name,
			   p.sales_unit_id, COALESCE(su.name, '') as sales_unit_name
		FROM products p
		LEFT JOIN product_categories pc ON p.category_id = pc.id
		LEFT JOIN units_of_measure u ON p.unit_id = u.id
		LEFT JOIN units_of_measure pu ON p.purchase_unit_id = pu.id
		LEFT JOIN units_of_measure su ON p.sales_unit_id = su.id
	`
	countQuery := `SELECT COUNT(*) FROM products p WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`

	args := []interface{}{tenantID}
	countArgs := []interface{}{tenantID}
	argCount := 1
	countArgCount := 1

	// JOIN org-specific settings - INNER JOIN to only show products assigned to current org
	if orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(`
		INNER JOIN product_organization_settings pos ON pos.product_id = p.id AND pos.organization_id = $%d`, argCount)
		args = append(args, orgID)

		countArgCount++
		countQuery = `SELECT COUNT(*) FROM products p INNER JOIN product_organization_settings pos ON pos.product_id = p.id AND pos.organization_id = $` + fmt.Sprintf("%d", countArgCount) + ` WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`
		countArgs = append(countArgs, orgID)
	} else {
		baseQuery += `
		LEFT JOIN product_organization_settings pos ON false`
	}
	baseQuery += `
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
	`

	if !includeInactive {
		baseQuery += " AND p.is_active = true"
		countQuery += " AND p.is_active = true"
	}

	if categoryID != "" {
		argCount++
		countArgCount++
		baseQuery += fmt.Sprintf(" AND p.category_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.category_id = $%d", countArgCount)
		args = append(args, categoryID)
		countArgs = append(countArgs, categoryID)
	}

	if productType != "" {
		argCount++
		countArgCount++
		baseQuery += fmt.Sprintf(" AND p.type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.type = $%d", countArgCount)
		args = append(args, productType)
		countArgs = append(countArgs, productType)
	}

	if search != "" {
		argCount++
		countArgCount++
		baseQuery += fmt.Sprintf(" AND (p.code ILIKE $%d OR p.name ILIKE $%d OR p.sku ILIKE $%d OR p.barcode ILIKE $%d)", argCount, argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (p.code ILIKE $%d OR p.name ILIKE $%d OR p.sku ILIKE $%d OR p.barcode ILIKE $%d)", countArgCount, countArgCount, countArgCount, countArgCount)
		args = append(args, "%"+search+"%")
		countArgs = append(countArgs, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count products", "error", err)
		response.InternalError(c, "Failed to list products")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY p.code ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list products", "error", err)
		response.InternalError(c, "Failed to list products")
		return
	}
	defer rows.Close()

	products := make([]*entity.ProductResponse, 0)
	for rows.Next() {
		var p entity.Product
		var categoryID, sku, barcode, desc, shortDesc, unitID sql.NullString
		var categoryCode, categoryName sql.NullString
		var tags json.RawMessage
		var imageURL string
		var unitName string
		var purchaseUnitID sql.NullString
		var purchaseUnitName string
		var salesUnitID sql.NullString
		var salesUnitName string

		err := rows.Scan(
			&p.ID, &p.TenantID, &categoryID, &p.Type, &p.Code, &sku, &barcode,
			&p.Name, &desc, &shortDesc, &unitID,
			&p.CostPrice, &p.ListPrice, &p.MinPrice,
			&p.IsStockable, &p.TrackInventory, &p.MinStockLevel,
			&p.ReorderPoint, &p.ReorderQuantity, &p.LeadTimeDays,
			&p.IsPurchasable, &p.IsSellable,
			&p.CanBeSold, &p.CanBePurchased, &p.AvailableInPOS,
			&p.CanBeExpensed, &p.CanBeRented, &p.CanBeSubcontracted,
			&p.IsOverheadExpense, &p.HasVariants, &p.IsActive, &tags, &imageURL,
			&p.CreatedAt, &p.UpdatedAt,
			&categoryCode, &categoryName,
			&unitName,
			&purchaseUnitID, &purchaseUnitName,
			&salesUnitID, &salesUnitName,
		)
		if err != nil {
			h.log.Error("Failed to scan product", "error", err)
			continue
		}

		if categoryID.Valid {
			cid, _ := uuid.Parse(categoryID.String)
			p.CategoryID = &cid
		}
		if sku.Valid {
			p.SKU = &sku.String
		}
		if barcode.Valid {
			p.Barcode = &barcode.String
		}
		if desc.Valid {
			p.Description = &desc.String
		}
		if shortDesc.Valid {
			p.ShortDescription = &shortDesc.String
		}

		var parsedUnitID, parsedPurchaseUnitID, parsedSalesUnitID *uuid.UUID
		if unitID.Valid {
			uid, _ := uuid.Parse(unitID.String)
			parsedUnitID = &uid
		}
		if purchaseUnitID.Valid {
			puid, _ := uuid.Parse(purchaseUnitID.String)
			parsedPurchaseUnitID = &puid
		}
		if salesUnitID.Valid {
			suid, _ := uuid.Parse(salesUnitID.String)
			parsedSalesUnitID = &suid
		}

		resp := &entity.ProductResponse{
			ID:                 p.ID,
			CategoryID:        p.CategoryID,
			Type:              p.Type,
			Code:              p.Code,
			SKU:               p.SKU,
			Barcode:           p.Barcode,
			Name:              p.Name,
			Description:       p.Description,
			UnitID:            parsedUnitID,
			UnitName:          unitName,
			PurchaseUnitID:    parsedPurchaseUnitID,
			PurchaseUnitName:  purchaseUnitName,
			SalesUnitID:       parsedSalesUnitID,
			SalesUnitName:     salesUnitName,
			CostPrice:         p.CostPrice,
			ListPrice:         p.ListPrice,
			IsStockable:       p.IsStockable,
			TrackInventory:    p.TrackInventory,
			MinStockLevel:     p.MinStockLevel,
			IsPurchasable:     p.IsPurchasable,
			IsSellable:        p.IsSellable,
			CanBeSold:         p.CanBeSold,
			CanBePurchased:    p.CanBePurchased,
			AvailableInPOS:    p.AvailableInPOS,
			CanBeExpensed:     p.CanBeExpensed,
			CanBeRented:       p.CanBeRented,
			CanBeSubcontracted: p.CanBeSubcontracted,
			IsOverheadExpense: p.IsOverheadExpense,
			HasVariants:       p.HasVariants,
			IsActive:          p.IsActive,
			ImageURL:          imageURL,
			CreatedAt:         p.CreatedAt,
			UpdatedAt:         p.UpdatedAt,
		}

		if categoryCode.Valid && categoryName.Valid {
			resp.Category = &entity.ProductCategory{
				Code: categoryCode.String,
				Name: categoryName.String,
			}
		}

		// Parse tags
		if len(tags) > 0 {
			json.Unmarshal(tags, &resp.Tags)
		}
		if resp.Tags == nil {
			resp.Tags = []string{}
		}

		products = append(products, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, products, pagination)
}

// CreateProduct creates a new product
func (h *Handler) CreateProduct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateProductInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check for duplicate code
	var codeExists bool
	err := h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM products WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)",
		tenantID, input.Code,
	).Scan(&codeExists)
	if err != nil {
		h.log.Error("Failed to check product code", "error", err)
		response.InternalError(c, "Failed to create product")
		return
	}
	if codeExists {
		response.Conflict(c, "Product with this code already exists")
		return
	}

	// Parse optional UUIDs
	var categoryID, unitID, currencyID *uuid.UUID
	var purchaseUnitID, salesUnitID *uuid.UUID
	if input.CategoryID != "" {
		cid, err := uuid.Parse(input.CategoryID)
		if err == nil {
			categoryID = &cid
		}
	}
	if input.UnitID != "" {
		uid, err := uuid.Parse(input.UnitID)
		if err == nil {
			unitID = &uid
		}
	}
	if input.CurrencyID != "" {
		cuid, err := uuid.Parse(input.CurrencyID)
		if err == nil {
			currencyID = &cuid
		}
	}

	// Resolve UOM string codes (e.g. "kg", "unit") to UUID from units_of_measure table
	if input.InventoryUOM != "" && unitID == nil {
		unitID = h.resolveUOMCode(tenantID, input.InventoryUOM)
	}
	if input.PurchaseUOM != "" {
		purchaseUnitID = h.resolveUOMCode(tenantID, input.PurchaseUOM)
	}
	if input.SalesUOM != "" {
		salesUnitID = h.resolveUOMCode(tenantID, input.SalesUOM)
	}

	// Set defaults
	isStockable := true
	if input.IsStockable != nil {
		isStockable = *input.IsStockable
	}
	trackInventory := true
	if input.TrackInventory != nil {
		trackInventory = *input.TrackInventory
	}
	isPurchasable := true
	if input.IsPurchasable != nil {
		isPurchasable = *input.IsPurchasable
	}
	isSellable := true
	if input.IsSellable != nil {
		isSellable = *input.IsSellable
	}
	// Module visibility defaults
	canBeSold := isSellable
	if input.CanBeSold != nil {
		canBeSold = *input.CanBeSold
	}
	canBePurchased := isPurchasable
	if input.CanBePurchased != nil {
		canBePurchased = *input.CanBePurchased
	}
	availableInPOS := false
	if input.AvailableInPOS != nil {
		availableInPOS = *input.AvailableInPOS
	}
	canBeExpensed := false
	if input.CanBeExpensed != nil {
		canBeExpensed = *input.CanBeExpensed
	}
	canBeRented := false
	if input.CanBeRented != nil {
		canBeRented = *input.CanBeRented
	}
	canBeSubcontracted := false
	if input.CanBeSubcontracted != nil {
		canBeSubcontracted = *input.CanBeSubcontracted
	}
	isOverheadExpense := false
	if input.IsOverheadExpense != nil {
		isOverheadExpense = *input.IsOverheadExpense
	}
	isManufacturable := false
	if input.IsManufacturable != nil {
		isManufacturable = *input.IsManufacturable
	}
	autoManufacture := false
	if input.AutoManufacture != nil {
		autoManufacture = *input.AutoManufacture
	}

	id := uuid.New()
	now := time.Now()

	// Prepare optional strings
	var sku, barcode, description, shortDescription *string
	if input.SKU != "" {
		sku = &input.SKU
	}
	if input.Barcode != "" {
		barcode = &input.Barcode
	}
	if input.Description != "" {
		description = &input.Description
	}
	if input.ShortDescription != "" {
		shortDescription = &input.ShortDescription
	}

	// Serialize tags
	var tagsJSON []byte
	if len(input.Tags) > 0 {
		tagsJSON, _ = json.Marshal(input.Tags)
	} else {
		tagsJSON = []byte("[]")
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var imageURL *string
	if input.ImageURL != "" {
		imageURL = &input.ImageURL
	}

	query := `
		INSERT INTO products (
			id, tenant_id, origin_organization_id, category_id, type, code, sku, barcode, name, description, short_description,
			unit_id, purchase_unit_id, sales_unit_id, cost_price, list_price, min_price, currency_id,
			is_stockable, track_inventory, min_stock_level, reorder_point, reorder_quantity,
			is_purchasable, is_sellable, can_be_sold, can_be_purchased, available_in_pos,
			can_be_expensed, can_be_rented, can_be_subcontracted, is_overhead_expense,
			is_manufacturable, auto_manufacture,
			is_active, tags, image_url, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39, $40)
		RETURNING id
	`

	err = h.db.QueryRow(query,
		id, tenantID, orgIDPtr, categoryID, input.Type, input.Code, sku, barcode, input.Name, description, shortDescription,
		unitID, purchaseUnitID, salesUnitID, input.CostPrice, input.ListPrice, input.MinPrice, currencyID,
		isStockable, trackInventory, input.MinStockLevel, input.ReorderPoint, input.ReorderQuantity,
		isPurchasable, isSellable, canBeSold, canBePurchased, availableInPOS,
		canBeExpensed, canBeRented, canBeSubcontracted, isOverheadExpense,
		isManufacturable, autoManufacture,
		true, tagsJSON, imageURL, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create product", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Product with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create product")
		return
	}

	// Create org-specific settings for selected organizations
	if len(input.OrganizationIDs) > 0 {
		for _, oid := range input.OrganizationIDs {
			parsedOrgID, parseErr := uuid.Parse(oid)
			if parseErr != nil {
				continue
			}
			_, err = h.db.Exec(`
				INSERT INTO product_organization_settings (
					tenant_id, product_id, organization_id,
					cost_price, list_price, min_price,
					min_stock_level, reorder_point, reorder_quantity
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (product_id, organization_id) DO NOTHING
			`, tenantID, id, parsedOrgID,
				input.CostPrice, input.ListPrice, input.MinPrice,
				input.MinStockLevel, input.ReorderPoint, input.ReorderQuantity)
			if err != nil {
				h.log.Error("Failed to create product org settings", "error", err, "org_id", oid)
			}
		}
	} else if orgID != uuid.Nil {
		// Fallback: if no orgs selected, use the creating organization
		_, err = h.db.Exec(`
			INSERT INTO product_organization_settings (
				tenant_id, product_id, organization_id,
				cost_price, list_price, min_price,
				min_stock_level, reorder_point, reorder_quantity
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (product_id, organization_id) DO NOTHING
		`, tenantID, id, orgID,
			input.CostPrice, input.ListPrice, input.MinPrice,
			input.MinStockLevel, input.ReorderPoint, input.ReorderQuantity)
		if err != nil {
			h.log.Error("Failed to create product org settings", "error", err)
		}
	}

	resp := &entity.ProductResponse{
		ID:                 id,
		CategoryID:         categoryID,
		Type:               input.Type,
		Code:               input.Code,
		SKU:                sku,
		Barcode:            barcode,
		Name:               input.Name,
		Description:        description,
		CostPrice:          input.CostPrice,
		ListPrice:          input.ListPrice,
		IsStockable:        isStockable,
		TrackInventory:     trackInventory,
		MinStockLevel:      input.MinStockLevel,
		IsPurchasable:      isPurchasable,
		IsSellable:         isSellable,
		CanBeSold:          canBeSold,
		CanBePurchased:     canBePurchased,
		AvailableInPOS:     availableInPOS,
		CanBeExpensed:      canBeExpensed,
		CanBeRented:        canBeRented,
		CanBeSubcontracted: canBeSubcontracted,
		IsOverheadExpense:  isOverheadExpense,
		IsActive:           true,
		Tags:               input.Tags,
		ImageURL:           input.ImageURL,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	response.Created(c, resp)
}

// GetProduct returns a single product by ID
func (h *Handler) GetProduct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Get org-specific settings overlay
	orgID, _ := middleware.GetOrganizationID(c)

	query := `
		SELECT p.id, p.tenant_id, p.category_id, p.type, p.code, p.sku, p.barcode,
			   p.name, p.description, p.short_description, p.unit_id,
			   COALESCE(pos.cost_price, p.cost_price) as cost_price,
			   COALESCE(pos.list_price, p.list_price) as list_price,
			   COALESCE(pos.min_price, p.min_price) as min_price,
			   p.is_stockable, p.track_inventory,
			   COALESCE(pos.min_stock_level, p.min_stock_level) as min_stock_level,
			   COALESCE(pos.reorder_point, p.reorder_point) as reorder_point,
			   COALESCE(pos.reorder_quantity, p.reorder_quantity) as reorder_quantity,
			   p.lead_time_days,
			   p.is_purchasable, p.is_sellable, p.is_active, p.tags,
			   COALESCE(p.image_url, '') as image_url,
			   p.created_at, p.updated_at,
			   pc.id as category_id_rel, pc.code as category_code, pc.name as category_name
		FROM products p
		LEFT JOIN product_categories pc ON p.category_id = pc.id
	`
	queryArgs := []interface{}{id, tenantID}
	if orgID != uuid.Nil {
		query += ` LEFT JOIN product_organization_settings pos ON pos.product_id = p.id AND pos.organization_id = $3`
		queryArgs = append(queryArgs, orgID)
	} else {
		query += ` LEFT JOIN product_organization_settings pos ON false`
	}
	query += `
		WHERE p.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL
	`

	var p entity.Product
	var categoryIDStr, sku, barcode, desc, shortDesc, unitID sql.NullString
	var categoryIDRel, categoryCode, categoryName sql.NullString
	var tags json.RawMessage
	var imageURL string

	err = h.db.QueryRow(query, queryArgs...).Scan(
		&p.ID, &p.TenantID, &categoryIDStr, &p.Type, &p.Code, &sku, &barcode,
		&p.Name, &desc, &shortDesc, &unitID,
		&p.CostPrice, &p.ListPrice, &p.MinPrice,
		&p.IsStockable, &p.TrackInventory, &p.MinStockLevel,
		&p.ReorderPoint, &p.ReorderQuantity, &p.LeadTimeDays,
		&p.IsPurchasable, &p.IsSellable, &p.IsActive, &tags, &imageURL,
		&p.CreatedAt, &p.UpdatedAt,
		&categoryIDRel, &categoryCode, &categoryName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Product")
		return
	}
	if err != nil {
		h.log.Error("Failed to get product", "error", err)
		response.InternalError(c, "Failed to get product")
		return
	}

	resp := &entity.ProductResponse{
		ID:             p.ID,
		Type:           p.Type,
		Code:           p.Code,
		Name:           p.Name,
		CostPrice:      p.CostPrice,
		ListPrice:      p.ListPrice,
		IsStockable:    p.IsStockable,
		TrackInventory: p.TrackInventory,
		MinStockLevel:  p.MinStockLevel,
		IsPurchasable:  p.IsPurchasable,
		IsSellable:     p.IsSellable,
		IsActive:       p.IsActive,
		ImageURL:       imageURL,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}

	if categoryIDStr.Valid {
		cid, _ := uuid.Parse(categoryIDStr.String)
		resp.CategoryID = &cid
	}
	if sku.Valid {
		resp.SKU = &sku.String
	}
	if barcode.Valid {
		resp.Barcode = &barcode.String
	}
	if desc.Valid {
		resp.Description = &desc.String
	}
	if shortDesc.Valid {
		resp.ShortDescription = &shortDesc.String
	}

	if categoryIDRel.Valid && categoryCode.Valid && categoryName.Valid {
		catID, _ := uuid.Parse(categoryIDRel.String)
		resp.Category = &entity.ProductCategory{
			ID:   catID,
			Code: categoryCode.String,
			Name: categoryName.String,
		}
	}

	if len(tags) > 0 {
		json.Unmarshal(tags, &resp.Tags)
	}
	if resp.Tags == nil {
		resp.Tags = []string{}
	}

	// Load organization IDs for this product
	orgRows, orgErr := h.db.Query(`
		SELECT organization_id FROM product_organization_settings
		WHERE product_id = $1 AND tenant_id = $2
	`, id, tenantID)
	if orgErr == nil {
		defer orgRows.Close()
		for orgRows.Next() {
			var oid uuid.UUID
			if orgRows.Scan(&oid) == nil {
				resp.OrganizationIDs = append(resp.OrganizationIDs, oid)
			}
		}
	}
	if resp.OrganizationIDs == nil {
		resp.OrganizationIDs = []uuid.UUID{}
	}

	response.Success(c, resp)
}

// UpdateProduct updates an existing product
func (h *Handler) UpdateProduct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	var input entity.UpdateProductInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)

	// Build dynamic update for shared product fields
	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	// Org-specific fields to upsert into product_organization_settings
	type orgSettings struct {
		costPrice       *float64
		listPrice       *float64
		minPrice        *float64
		minStockLevel   *float64
		reorderPoint    *float64
		reorderQuantity *float64
	}
	var orgUpdates orgSettings
	hasOrgUpdates := false

	if input.CategoryID != nil {
		if *input.CategoryID == "" {
			addUpdate("category_id", nil)
		} else {
			cid, _ := uuid.Parse(*input.CategoryID)
			addUpdate("category_id", cid)
		}
	}
	if input.SKU != nil {
		addUpdate("sku", *input.SKU)
	}
	if input.Barcode != nil {
		addUpdate("barcode", *input.Barcode)
	}
	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.Description != nil {
		addUpdate("description", *input.Description)
	}
	if input.ShortDescription != nil {
		addUpdate("short_description", *input.ShortDescription)
	}
	// Pricing → org-specific + fallback on products table
	if input.CostPrice != nil {
		addUpdate("cost_price", *input.CostPrice)
		orgUpdates.costPrice = input.CostPrice
		hasOrgUpdates = true
	}
	if input.ListPrice != nil {
		addUpdate("list_price", *input.ListPrice)
		orgUpdates.listPrice = input.ListPrice
		hasOrgUpdates = true
	}
	if input.MinPrice != nil {
		addUpdate("min_price", *input.MinPrice)
		orgUpdates.minPrice = input.MinPrice
		hasOrgUpdates = true
	}
	if input.IsStockable != nil {
		addUpdate("is_stockable", *input.IsStockable)
	}
	if input.TrackInventory != nil {
		addUpdate("track_inventory", *input.TrackInventory)
	}
	// Inventory settings → org-specific + fallback on products table
	if input.MinStockLevel != nil {
		addUpdate("min_stock_level", *input.MinStockLevel)
		orgUpdates.minStockLevel = input.MinStockLevel
		hasOrgUpdates = true
	}
	if input.ReorderPoint != nil {
		addUpdate("reorder_point", *input.ReorderPoint)
		orgUpdates.reorderPoint = input.ReorderPoint
		hasOrgUpdates = true
	}
	if input.ReorderQuantity != nil {
		addUpdate("reorder_quantity", *input.ReorderQuantity)
		orgUpdates.reorderQuantity = input.ReorderQuantity
		hasOrgUpdates = true
	}
	if input.IsPurchasable != nil {
		addUpdate("is_purchasable", *input.IsPurchasable)
	}
	if input.IsSellable != nil {
		addUpdate("is_sellable", *input.IsSellable)
	}
	if input.CanBeSold != nil {
		addUpdate("can_be_sold", *input.CanBeSold)
	}
	if input.CanBePurchased != nil {
		addUpdate("can_be_purchased", *input.CanBePurchased)
	}
	if input.AvailableInPOS != nil {
		addUpdate("available_in_pos", *input.AvailableInPOS)
	}
	if input.CanBeExpensed != nil {
		addUpdate("can_be_expensed", *input.CanBeExpensed)
	}
	if input.CanBeRented != nil {
		addUpdate("can_be_rented", *input.CanBeRented)
	}
	if input.CanBeSubcontracted != nil {
		addUpdate("can_be_subcontracted", *input.CanBeSubcontracted)
	}
	if input.IsOverheadExpense != nil {
		addUpdate("is_overhead_expense", *input.IsOverheadExpense)
	}
	if input.IsManufacturable != nil {
		addUpdate("is_manufacturable", *input.IsManufacturable)
	}
	if input.AutoManufacture != nil {
		addUpdate("auto_manufacture", *input.AutoManufacture)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
	}
	if input.Tags != nil {
		tagsJSON, _ := json.Marshal(input.Tags)
		addUpdate("tags", tagsJSON)
	}
	if input.ImageURL != nil {
		addUpdate("image_url", *input.ImageURL)
	}

	// Resolve UOM string codes to UUID references
	if input.InventoryUOM != nil && *input.InventoryUOM != "" {
		if resolved := h.resolveUOMCode(tenantID, *input.InventoryUOM); resolved != nil {
			addUpdate("unit_id", *resolved)
		}
	}
	if input.PurchaseUOM != nil && *input.PurchaseUOM != "" {
		if resolved := h.resolveUOMCode(tenantID, *input.PurchaseUOM); resolved != nil {
			addUpdate("purchase_unit_id", *resolved)
		}
	}
	if input.SalesUOM != nil && *input.SalesUOM != "" {
		if resolved := h.resolveUOMCode(tenantID, *input.SalesUOM); resolved != nil {
			addUpdate("sales_unit_id", *resolved)
		}
	}

	if len(updates) == 0 && !hasOrgUpdates {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Update shared product fields
	if len(updates) > 0 {
		addUpdate("updated_at", time.Now())

		argCount++
		args = append(args, id)
		argCount++
		args = append(args, tenantID)

		query := fmt.Sprintf(`
			UPDATE products SET %s
			WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
			RETURNING id
		`, strings.Join(updates, ", "), argCount-1, argCount)

		var returnedID uuid.UUID
		err = h.db.QueryRow(query, args...).Scan(&returnedID)
		if err == sql.ErrNoRows {
			response.NotFound(c, "Product")
			return
		}
		if err != nil {
			h.log.Error("Failed to update product", "error", err)
			response.InternalError(c, "Failed to update product")
			return
		}
	}

	// Upsert org-specific settings
	if hasOrgUpdates && orgID != uuid.Nil {
		_, err = h.db.Exec(`
			INSERT INTO product_organization_settings (tenant_id, product_id, organization_id,
				cost_price, list_price, min_price, min_stock_level, reorder_point, reorder_quantity)
			VALUES ($1, $2, $3,
				COALESCE($4, 0), COALESCE($5, 0), COALESCE($6, 0),
				COALESCE($7, 0), COALESCE($8, 0), COALESCE($9, 0))
			ON CONFLICT (product_id, organization_id) DO UPDATE SET
				cost_price = COALESCE($4, product_organization_settings.cost_price),
				list_price = COALESCE($5, product_organization_settings.list_price),
				min_price = COALESCE($6, product_organization_settings.min_price),
				min_stock_level = COALESCE($7, product_organization_settings.min_stock_level),
				reorder_point = COALESCE($8, product_organization_settings.reorder_point),
				reorder_quantity = COALESCE($9, product_organization_settings.reorder_quantity),
				updated_at = NOW()
		`, tenantID, id, orgID,
			orgUpdates.costPrice, orgUpdates.listPrice, orgUpdates.minPrice,
			orgUpdates.minStockLevel, orgUpdates.reorderPoint, orgUpdates.reorderQuantity)
		if err != nil {
			h.log.Error("Failed to upsert product org settings", "error", err)
		}
	}

	// Update organization assignments if provided
	if len(input.OrganizationIDs) > 0 {
		// Delete existing org assignments
		h.db.Exec(`DELETE FROM product_organization_settings WHERE product_id = $1 AND tenant_id = $2`, id, tenantID)

		// Re-create for selected orgs
		for _, oid := range input.OrganizationIDs {
			parsedOrgID, parseErr := uuid.Parse(oid)
			if parseErr != nil {
				continue
			}
			h.db.Exec(`
				INSERT INTO product_organization_settings (
					tenant_id, product_id, organization_id,
					cost_price, list_price, min_price,
					min_stock_level, reorder_point, reorder_quantity
				) VALUES ($1, $2, $3, 0, 0, 0, 0, 0, 0)
				ON CONFLICT (product_id, organization_id) DO NOTHING
			`, tenantID, id, parsedOrgID)
		}
	}

	// Return updated product
	h.GetProduct(c)
}

// DeleteProduct soft-deletes a product
func (h *Handler) DeleteProduct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Check for existing inventory
	var hasInventory bool
	h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM inventory WHERE product_id = $1 AND quantity_on_hand > 0)
	`, id).Scan(&hasInventory)

	if hasInventory {
		response.BadRequest(c, "Cannot delete product with existing inventory. Set to inactive instead.")
		return
	}

	query := `
		UPDATE products SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete product", "error", err)
		response.InternalError(c, "Failed to delete product")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Product")
		return
	}

	response.NoContent(c)
}

// =====================================================
// PRODUCT CATEGORY HANDLERS
// =====================================================

// ListProductCategories returns all product categories
func (h *Handler) ListProductCategories(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	includeInactive := c.Query("include_inactive") == "true"
	flat := c.Query("flat") == "true"

	// Categories are shared across orgs; account mappings come from category_organization_settings
	orgID, _ := middleware.GetOrganizationID(c)

	query := `
		SELECT pc.id, pc.tenant_id, pc.parent_id, pc.code, pc.name, pc.description, pc.is_active, pc.created_at, pc.updated_at,
			COALESCE(cos.income_account_id, pc.income_account_id) as income_account_id,
			COALESCE(cos.expense_account_id, pc.expense_account_id) as expense_account_id,
			COALESCE(cos.stock_valuation_account_id, pc.stock_valuation_account_id) as stock_valuation_account_id,
			COALESCE(cos.stock_input_account_id, pc.stock_input_account_id) as stock_input_account_id,
			COALESCE(cos.stock_output_account_id, pc.stock_output_account_id) as stock_output_account_id
		FROM product_categories pc
	`
	args := []interface{}{tenantID}

	if orgID != uuid.Nil {
		query += fmt.Sprintf(` LEFT JOIN category_organization_settings cos ON cos.category_id = pc.id AND cos.organization_id = $%d`, len(args)+1)
		args = append(args, orgID)
	} else {
		query += ` LEFT JOIN category_organization_settings cos ON false`
	}

	query += `
		WHERE pc.tenant_id = $1 AND pc.deleted_at IS NULL
	`

	if !includeInactive {
		query += " AND pc.is_active = true"
	}

	query += " ORDER BY pc.code ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list product categories", "error", err)
		response.InternalError(c, "Failed to list product categories")
		return
	}
	defer rows.Close()

	categories := make([]*entity.ProductCategory, 0)
	categoryMap := make(map[uuid.UUID]*entity.ProductCategory)

	for rows.Next() {
		var cat entity.ProductCategory
		var parentID, desc sql.NullString

		err := rows.Scan(&cat.ID, &cat.TenantID, &parentID, &cat.Code, &cat.Name, &desc, &cat.IsActive, &cat.CreatedAt, &cat.UpdatedAt,
			&cat.IncomeAccountID, &cat.ExpenseAccountID, &cat.StockValuationAccountID, &cat.StockInputAccountID, &cat.StockOutputAccountID)
		if err != nil {
			continue
		}

		if parentID.Valid {
			pid, _ := uuid.Parse(parentID.String)
			cat.ParentID = &pid
		}
		if desc.Valid {
			cat.Description = &desc.String
		}

		cat.Children = []entity.ProductCategory{}
		categories = append(categories, &cat)
		categoryMap[cat.ID] = &cat
	}

	if flat {
		response.Success(c, categories)
		return
	}

	// Build tree structure
	rootCategories := make([]*entity.ProductCategory, 0)
	for _, cat := range categories {
		if cat.ParentID == nil {
			rootCategories = append(rootCategories, cat)
		} else {
			if parent, ok := categoryMap[*cat.ParentID]; ok {
				parent.Children = append(parent.Children, *cat)
			}
		}
	}

	response.Success(c, rootCategories)
}

// CreateProductCategory creates a new product category
func (h *Handler) CreateProductCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	type Input struct {
		ParentID               string  `json:"parent_id,omitempty"`
		Code                   string  `json:"code" binding:"required,min=1,max=50"`
		Name                   string  `json:"name" binding:"required,min=1,max=255"`
		Description            string  `json:"description,omitempty"`
		IncomeAccountID        *string `json:"income_account_id,omitempty"`
		ExpenseAccountID       *string `json:"expense_account_id,omitempty"`
		StockValuationAccountID *string `json:"stock_valuation_account_id,omitempty"`
		StockInputAccountID    *string `json:"stock_input_account_id,omitempty"`
		StockOutputAccountID   *string `json:"stock_output_account_id,omitempty"`
	}

	var input Input
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check for duplicate code within tenant (categories are shared)
	orgID, _ := middleware.GetOrganizationID(c)
	var exists bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM product_categories WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)",
		tenantID, input.Code).Scan(&exists)
	if exists {
		response.Conflict(c, "Category with this code already exists")
		return
	}

	var parentID *uuid.UUID
	if input.ParentID != "" {
		pid, err := uuid.Parse(input.ParentID)
		if err == nil {
			parentID = &pid
		}
	}

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	// Parse account IDs
	parseOptionalUUID := func(s *string) *uuid.UUID {
		if s == nil || *s == "" {
			return nil
		}
		parsed, err := uuid.Parse(*s)
		if err != nil || parsed == uuid.Nil {
			return nil
		}
		return &parsed
	}
	incomeAcctID := parseOptionalUUID(input.IncomeAccountID)
	expenseAcctID := parseOptionalUUID(input.ExpenseAccountID)
	stockValAcctID := parseOptionalUUID(input.StockValuationAccountID)
	stockInAcctID := parseOptionalUUID(input.StockInputAccountID)
	stockOutAcctID := parseOptionalUUID(input.StockOutputAccountID)

	id := uuid.New()
	now := time.Now()

	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	_, err := h.db.Exec(`
		INSERT INTO product_categories (
			id, tenant_id, origin_organization_id, parent_id, code, name, description, is_active,
			income_account_id, expense_account_id, stock_valuation_account_id, stock_input_account_id, stock_output_account_id,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, id, tenantID, orgIDPtr, parentID, input.Code, input.Name, description, true,
		incomeAcctID, expenseAcctID, stockValAcctID, stockInAcctID, stockOutAcctID,
		now, now)

	if err != nil {
		h.log.Error("Failed to create category", "error", err)
		response.InternalError(c, "Failed to create category")
		return
	}

	// Create org-specific account settings
	if orgID != uuid.Nil && (incomeAcctID != nil || expenseAcctID != nil || stockValAcctID != nil || stockInAcctID != nil || stockOutAcctID != nil) {
		_, err = h.db.Exec(`
			INSERT INTO category_organization_settings (
				tenant_id, category_id, organization_id,
				income_account_id, expense_account_id,
				stock_valuation_account_id, stock_input_account_id, stock_output_account_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (category_id, organization_id) DO NOTHING
		`, tenantID, id, orgID,
			incomeAcctID, expenseAcctID, stockValAcctID, stockInAcctID, stockOutAcctID)
		if err != nil {
			h.log.Error("Failed to create category org settings", "error", err)
		}
	}

	cat := &entity.ProductCategory{
		ID:                      id,
		TenantID:                tenantID,
		ParentID:                parentID,
		Code:                    input.Code,
		Name:                    input.Name,
		Description:             description,
		IsActive:                true,
		CreatedAt:               now,
		UpdatedAt:               now,
		IncomeAccountID:         incomeAcctID,
		ExpenseAccountID:        expenseAcctID,
		StockValuationAccountID: stockValAcctID,
		StockInputAccountID:     stockInAcctID,
		StockOutputAccountID:    stockOutAcctID,
	}

	response.Created(c, cat)
}

// GetProductCategory returns a single category
func (h *Handler) GetProductCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	var cat entity.ProductCategory
	var parentID, desc sql.NullString

	// Get org-specific account settings overlay
	orgID, _ := middleware.GetOrganizationID(c)

	getCatQuery := `
		SELECT pc.id, pc.tenant_id, pc.parent_id, pc.code, pc.name, pc.description, pc.is_active, pc.created_at, pc.updated_at,
			COALESCE(cos.income_account_id, pc.income_account_id) as income_account_id,
			COALESCE(cos.expense_account_id, pc.expense_account_id) as expense_account_id,
			COALESCE(cos.stock_valuation_account_id, pc.stock_valuation_account_id) as stock_valuation_account_id,
			COALESCE(cos.stock_input_account_id, pc.stock_input_account_id) as stock_input_account_id,
			COALESCE(cos.stock_output_account_id, pc.stock_output_account_id) as stock_output_account_id
		FROM product_categories pc
	`
	getCatArgs := []interface{}{id, tenantID}
	if orgID != uuid.Nil {
		getCatQuery += ` LEFT JOIN category_organization_settings cos ON cos.category_id = pc.id AND cos.organization_id = $3`
		getCatArgs = append(getCatArgs, orgID)
	} else {
		getCatQuery += ` LEFT JOIN category_organization_settings cos ON false`
	}
	getCatQuery += ` WHERE pc.id = $1 AND pc.tenant_id = $2 AND pc.deleted_at IS NULL`

	err = h.db.QueryRow(getCatQuery, getCatArgs...).Scan(&cat.ID, &cat.TenantID, &parentID, &cat.Code, &cat.Name, &desc, &cat.IsActive, &cat.CreatedAt, &cat.UpdatedAt,
		&cat.IncomeAccountID, &cat.ExpenseAccountID, &cat.StockValuationAccountID, &cat.StockInputAccountID, &cat.StockOutputAccountID)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Category")
		return
	}
	if err != nil {
		h.log.Error("Failed to get category", "error", err)
		response.InternalError(c, "Failed to get category")
		return
	}

	if parentID.Valid {
		pid, _ := uuid.Parse(parentID.String)
		cat.ParentID = &pid
	}
	if desc.Valid {
		cat.Description = &desc.String
	}

	response.Success(c, cat)
}

// UpdateProductCategory updates a category
func (h *Handler) UpdateProductCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	type Input struct {
		Name                    *string `json:"name,omitempty"`
		Description             *string `json:"description,omitempty"`
		ParentID                *string `json:"parent_id,omitempty"`
		IsActive                *bool   `json:"is_active,omitempty"`
		IncomeAccountID         *string `json:"income_account_id"`
		ExpenseAccountID        *string `json:"expense_account_id"`
		StockValuationAccountID *string `json:"stock_valuation_account_id"`
		StockInputAccountID     *string `json:"stock_input_account_id"`
		StockOutputAccountID    *string `json:"stock_output_account_id"`
	}

	var input Input
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	// Helper to parse optional UUID, empty string sets to NULL
	parseOptionalUUID := func(s *string) (*uuid.UUID, bool) {
		if s == nil {
			return nil, false // field not provided
		}
		if *s == "" {
			return nil, true // explicitly set to null
		}
		parsed, err := uuid.Parse(*s)
		if err != nil || parsed == uuid.Nil {
			return nil, true
		}
		return &parsed, true
	}

	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.Description != nil {
		addUpdate("description", *input.Description)
	}
	if input.ParentID != nil {
		if *input.ParentID == "" {
			addUpdate("parent_id", nil)
		} else {
			pid, _ := uuid.Parse(*input.ParentID)
			addUpdate("parent_id", pid)
		}
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
	}

	// Account fields - update both on category (fallback) and org settings
	orgID, _ := middleware.GetOrganizationID(c)
	hasAccountUpdates := false
	var incomeAcct, expenseAcct, stockValAcct, stockInAcct, stockOutAcct *uuid.UUID

	if val, provided := parseOptionalUUID(input.IncomeAccountID); provided {
		addUpdate("income_account_id", val)
		incomeAcct = val
		hasAccountUpdates = true
	}
	if val, provided := parseOptionalUUID(input.ExpenseAccountID); provided {
		addUpdate("expense_account_id", val)
		expenseAcct = val
		hasAccountUpdates = true
	}
	if val, provided := parseOptionalUUID(input.StockValuationAccountID); provided {
		addUpdate("stock_valuation_account_id", val)
		stockValAcct = val
		hasAccountUpdates = true
	}
	if val, provided := parseOptionalUUID(input.StockInputAccountID); provided {
		addUpdate("stock_input_account_id", val)
		stockInAcct = val
		hasAccountUpdates = true
	}
	if val, provided := parseOptionalUUID(input.StockOutputAccountID); provided {
		addUpdate("stock_output_account_id", val)
		stockOutAcct = val
		hasAccountUpdates = true
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addUpdate("updated_at", time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE product_categories SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Category")
		return
	}
	if err != nil {
		h.log.Error("Failed to update category", "error", err)
		response.InternalError(c, "Failed to update category")
		return
	}

	// Upsert org-specific account settings
	if hasAccountUpdates && orgID != uuid.Nil {
		_, err = h.db.Exec(`
			INSERT INTO category_organization_settings (
				tenant_id, category_id, organization_id,
				income_account_id, expense_account_id,
				stock_valuation_account_id, stock_input_account_id, stock_output_account_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (category_id, organization_id) DO UPDATE SET
				income_account_id = COALESCE($4, category_organization_settings.income_account_id),
				expense_account_id = COALESCE($5, category_organization_settings.expense_account_id),
				stock_valuation_account_id = COALESCE($6, category_organization_settings.stock_valuation_account_id),
				stock_input_account_id = COALESCE($7, category_organization_settings.stock_input_account_id),
				stock_output_account_id = COALESCE($8, category_organization_settings.stock_output_account_id),
				updated_at = NOW()
		`, tenantID, id, orgID, incomeAcct, expenseAcct, stockValAcct, stockInAcct, stockOutAcct)
		if err != nil {
			h.log.Error("Failed to upsert category org settings", "error", err)
		}
	}

	h.GetProductCategory(c)
}

// DeleteProductCategory deletes a category
func (h *Handler) DeleteProductCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	// Check for products using this category
	var hasProducts bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM products WHERE category_id = $1 AND deleted_at IS NULL)", id).Scan(&hasProducts)
	if hasProducts {
		response.BadRequest(c, "Cannot delete category with associated products")
		return
	}

	// Check for child categories
	var hasChildren bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM product_categories WHERE parent_id = $1 AND deleted_at IS NULL)", id).Scan(&hasChildren)
	if hasChildren {
		response.BadRequest(c, "Cannot delete category with child categories")
		return
	}

	result, err := h.db.Exec(`
		UPDATE product_categories SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, time.Now(), id, tenantID)

	if err != nil {
		h.log.Error("Failed to delete category", "error", err)
		response.InternalError(c, "Failed to delete category")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Category")
		return
	}

	response.NoContent(c)
}
