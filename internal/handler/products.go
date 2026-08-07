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
	"github.com/lib/pq"
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
	if limit < 1 {
		limit = 20
	}
	if limit > 5000 {
		limit = 5000
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	categoryID := c.Query("category_id")
	productType := c.Query("type")
	inventoryType := c.Query("inventory_type")
	warehouseID := c.Query("warehouse_id") // optional: only return products that have at least one inventory row in this warehouse
	includeInactive := c.Query("include_inactive") == "true"

	// Build query - products are shared across orgs, org-specific data comes from product_organization_settings
	orgID, _ := middleware.GetOrganizationID(c)

	baseQuery := `
		SELECT p.id, p.tenant_id, p.category_id, p.type, p.code, p.sku, p.barcode, p.search_key,
			   p.name, p.description, p.short_description, p.unit_id,
			   COALESCE(NULLIF(pos.cost_price, 0), p.cost_price) as cost_price,
			   COALESCE(NULLIF(pos.list_price, 0), p.list_price) as list_price,
			   COALESCE(NULLIF(pos.min_price, 0), p.min_price) as min_price,
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
			   COALESCE(p.is_manufacturable, false) as is_manufacturable,
			   COALESCE(p.auto_manufacture, false) as auto_manufacture,
			   COALESCE(p.has_variants, false) as has_variants,
			   COALESCE(p.has_delivery, false) as has_delivery,
			   COALESCE(p.delivery_price, 0) as delivery_price,
			   p.weight, p.length, p.width, p.height,
			   p.is_active, p.tags, COALESCE(p.image_url, '') as image_url,
			   COALESCE(p.inventory_type, 'trade') as inventory_type,
			   p.created_at, p.updated_at,
			   pc.code as category_code, pc.name as category_name,
			   COALESCE(u.name, '') as unit_name, COALESCE(u.code, '') as unit_code,
			   p.purchase_unit_id, COALESCE(pu.name, '') as purchase_unit_name,
			   p.sales_unit_id, COALESCE(su.name, '') as sales_unit_name
		FROM products p
		LEFT JOIN product_categories pc ON p.category_id = pc.id
		LEFT JOIN units_of_measure u ON p.unit_id = u.id
		LEFT JOIN units_of_measure pu ON p.purchase_unit_id = pu.id
		LEFT JOIN units_of_measure su ON p.sales_unit_id = su.id
	`
	args := []interface{}{tenantID}
	countArgs := []interface{}{tenantID}
	argCount := 1
	countArgCount := 1

	// INNER JOIN org-specific settings so products only appear in companies they belong to
	if orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(`
		INNER JOIN product_organization_settings pos ON pos.product_id = p.id AND pos.organization_id = $%d`, argCount)
		args = append(args, orgID)

		countArgCount++
		countArgs = append(countArgs, orgID)
	} else {
		baseQuery += `
		LEFT JOIN product_organization_settings pos ON false`
	}
	baseQuery += `
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
	`

	var countQuery string
	if orgID != uuid.Nil {
		countQuery = fmt.Sprintf(`SELECT COUNT(*) FROM products p INNER JOIN product_organization_settings pos ON pos.product_id = p.id AND pos.organization_id = $%d WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`, countArgCount)
	} else {
		countQuery = `SELECT COUNT(*) FROM products p WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`
	}

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

	if inventoryType != "" {
		argCount++
		countArgCount++
		baseQuery += fmt.Sprintf(" AND COALESCE(p.inventory_type, 'trade') = $%d", argCount)
		countQuery += fmt.Sprintf(" AND COALESCE(p.inventory_type, 'trade') = $%d", countArgCount)
		args = append(args, inventoryType)
		countArgs = append(countArgs, inventoryType)
	}

	// Warehouse filter: restricts the result to products that have at least one
	// inventory row in the requested warehouse. The previous frontend
	// implementation page-filtered client-side, which silently dropped any
	// product not on the current 20-row page — making the warehouse filter
	// look broken to users. Validating as a UUID first because warehouse_id
	// is a UUID column in postgres and a malformed value would otherwise
	// surface as a 500 from `EXISTS (... WHERE warehouse_id = 'abc')`.
	if warehouseID != "" {
		if _, parseErr := uuid.Parse(warehouseID); parseErr == nil {
			argCount++
			countArgCount++
			// Tie the EXISTS subquery to the same tenant ($1) so we don't
			// leak data across tenants if a UUID collision ever occurred.
			baseQuery += fmt.Sprintf(` AND EXISTS (
				SELECT 1 FROM inventory inv
				WHERE inv.product_id = p.id
				  AND inv.warehouse_id = $%d
				  AND inv.tenant_id = $1
			)`, argCount)
			countQuery += fmt.Sprintf(` AND EXISTS (
				SELECT 1 FROM inventory inv
				WHERE inv.product_id = p.id
				  AND inv.warehouse_id = $%d
				  AND inv.tenant_id = $1
			)`, countArgCount)
			args = append(args, warehouseID)
			countArgs = append(countArgs, warehouseID)
		}
	}

	if search != "" {
		argCount++
		countArgCount++
		baseQuery += fmt.Sprintf(" AND (p.code ILIKE $%d OR p.name ILIKE $%d OR p.sku ILIKE $%d OR p.barcode ILIKE $%d OR p.search_key ILIKE $%d)", argCount, argCount, argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (p.code ILIKE $%d OR p.name ILIKE $%d OR p.sku ILIKE $%d OR p.barcode ILIKE $%d OR p.search_key ILIKE $%d)", countArgCount, countArgCount, countArgCount, countArgCount, countArgCount)
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
	baseQuery += " ORDER BY p.name ASC"
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
		var categoryID, sku, barcode, searchKey, desc, shortDesc, unitID sql.NullString
		var categoryCode, categoryName sql.NullString
		var tags json.RawMessage
		var imageURL string
		var inventoryType string
		var unitName, unitCode string
		var purchaseUnitID sql.NullString
		var purchaseUnitName string
		var salesUnitID sql.NullString
		var salesUnitName string
		var weight, length, width, height sql.NullFloat64

		err := rows.Scan(
			&p.ID, &p.TenantID, &categoryID, &p.Type, &p.Code, &sku, &barcode, &searchKey,
			&p.Name, &desc, &shortDesc, &unitID,
			&p.CostPrice, &p.ListPrice, &p.MinPrice,
			&p.IsStockable, &p.TrackInventory, &p.MinStockLevel,
			&p.ReorderPoint, &p.ReorderQuantity, &p.LeadTimeDays,
			&p.IsPurchasable, &p.IsSellable,
			&p.CanBeSold, &p.CanBePurchased, &p.AvailableInPOS,
			&p.CanBeExpensed, &p.CanBeRented, &p.CanBeSubcontracted,
			&p.IsOverheadExpense, &p.IsManufacturable, &p.AutoManufacture, &p.HasVariants, &p.HasDelivery, &p.DeliveryPrice,
			&weight, &length, &width, &height,
			&p.IsActive, &tags, &imageURL,
			&inventoryType,
			&p.CreatedAt, &p.UpdatedAt,
			&categoryCode, &categoryName,
			&unitName, &unitCode,
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
		if searchKey.Valid {
			sk := searchKey.String
			p.SearchKey = &sk
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
			SearchKey:         p.SearchKey,
			Name:              p.Name,
			Description:       p.Description,
			UnitID:            parsedUnitID,
			UnitName:          unitName,
			UnitCode:          unitCode,
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
			IsOverheadExpense:  p.IsOverheadExpense,
			IsManufacturable:   p.IsManufacturable,
			AutoManufacture:    p.AutoManufacture,
			HasVariants:        p.HasVariants,
			HasDelivery:        p.HasDelivery,
			DeliveryPrice:      p.DeliveryPrice,
			IsActive:           p.IsActive,
			ImageURL:           imageURL,
			InventoryType:      inventoryType,
			CreatedAt:          p.CreatedAt,
			UpdatedAt:          p.UpdatedAt,
		}

		if categoryCode.Valid && categoryName.Valid {
			resp.Category = &entity.ProductCategory{
				Code: categoryCode.String,
				Name: categoryName.String,
			}
		}

		if weight.Valid { v := weight.Float64; resp.Weight = &v }
		if length.Valid { v := length.Float64; resp.Length = &v }
		if width.Valid  { v := width.Float64;  resp.Width  = &v }
		if height.Valid { v := height.Float64; resp.Height = &v }

		// Parse tags
		if len(tags) > 0 {
			json.Unmarshal(tags, &resp.Tags)
		}
		if resp.Tags == nil {
			resp.Tags = []string{}
		}

		resp.OrganizationIDs = []uuid.UUID{}
		products = append(products, resp)
	}

	// Batch-load organization_ids for all products in this page (avoids N+1)
	if len(products) > 0 {
		productIDs := make([]uuid.UUID, len(products))
		productIndex := make(map[uuid.UUID]*entity.ProductResponse, len(products))
		for i, p := range products {
			productIDs[i] = p.ID
			productIndex[p.ID] = p
		}
		orgRows, orgErr := h.db.Query(`
			SELECT product_id, organization_id FROM product_organization_settings
			WHERE tenant_id = $1 AND product_id = ANY($2)
		`, tenantID, pq.Array(productIDs))
		if orgErr == nil {
			defer orgRows.Close()
			for orgRows.Next() {
				var pid, oid uuid.UUID
				if err := orgRows.Scan(&pid, &oid); err == nil {
					if p, ok := productIndex[pid]; ok {
						p.OrganizationIDs = append(p.OrganizationIDs, oid)
					}
				}
			}
		} else {
			h.log.Error("Failed to load product organization_ids", "error", orgErr)
		}
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check barcode uniqueness (global standard for product identification)
	if input.Barcode != "" {
		var barcodeExists bool
		if err := h.db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM products WHERE tenant_id = $1 AND barcode = $2 AND deleted_at IS NULL)",
			tenantID, input.Barcode,
		).Scan(&barcodeExists); err != nil {
			h.log.Error("Failed to check barcode", "error", err)
			response.InternalError(c, "Failed to create product")
			return
		}
		if barcodeExists {
			response.Conflict(c, "Product with this barcode already exists")
			return
		}
	}

	// Check SKU uniqueness (company-internal identifier)
	if input.SKU != "" {
		var skuExists bool
		if err := h.db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM products WHERE tenant_id = $1 AND sku = $2 AND deleted_at IS NULL)",
			tenantID, input.SKU,
		).Scan(&skuExists); err != nil {
			h.log.Error("Failed to check SKU", "error", err)
			response.InternalError(c, "Failed to create product")
			return
		}
		if skuExists {
			response.Conflict(c, "Product with this SKU already exists")
			return
		}
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
	hasDelivery := false
	if input.HasDelivery != nil {
		hasDelivery = *input.HasDelivery
	}

	// Default inventory_type to 'trade', or 'service' if type is 'service'
	inventoryType := "trade"
	if input.InventoryType != "" {
		inventoryType = input.InventoryType
	} else if input.Type == "service" {
		inventoryType = "service"
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

	// search_key: either passed explicitly (user typed / clicked
	// Generate in the form), or inherited from an existing same-name
	// product in the tenant (any organisation). If neither applies,
	// leave it NULL — keys are set deliberately by the manufacturing
	// side, and construction-side products pick them up only via
	// name match. Do NOT auto-generate here.
	searchKey := strings.TrimSpace(input.SearchKey)
	if searchKey == "" {
		searchKey = h.lookupSearchKeyForName(tenantID, input.Name)
	}
	var searchKeyPtr *string
	if searchKey != "" {
		searchKeyPtr = &searchKey
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
			id, tenant_id, origin_organization_id, category_id, type, code, sku, barcode, search_key, name, description, short_description,
			unit_id, purchase_unit_id, sales_unit_id, cost_price, list_price, min_price, currency_id,
			is_stockable, track_inventory, min_stock_level, reorder_point, reorder_quantity,
			is_purchasable, is_sellable, can_be_sold, can_be_purchased, available_in_pos,
			can_be_expensed, can_be_rented, can_be_subcontracted, is_overhead_expense,
			is_manufacturable, auto_manufacture,
			has_delivery, delivery_price,
			is_active, tags, image_url, inventory_type, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39, $40, $41, $42, $43, $44)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, categoryID, input.Type, input.Code, sku, barcode, searchKeyPtr, input.Name, description, shortDescription,
		unitID, purchaseUnitID, salesUnitID, input.CostPrice, input.ListPrice, input.MinPrice, currencyID,
		isStockable, trackInventory, input.MinStockLevel, input.ReorderPoint, input.ReorderQuantity,
		isPurchasable, isSellable, canBeSold, canBePurchased, availableInPOS,
		canBeExpensed, canBeRented, canBeSubcontracted, isOverheadExpense,
		isManufacturable, autoManufacture,
		hasDelivery, input.DeliveryPrice,
		true, tagsJSON, imageURL, inventoryType, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create product", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Product with duplicate barcode or SKU already exists")
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
		InventoryType:      inventoryType,
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
		SELECT p.id, p.tenant_id, p.category_id, p.type, p.code, p.sku, p.barcode, p.search_key,
			   p.name, p.description, p.short_description, p.unit_id,
			   COALESCE(NULLIF(pos.cost_price, 0), p.cost_price) as cost_price,
			   COALESCE(NULLIF(pos.list_price, 0), p.list_price) as list_price,
			   COALESCE(NULLIF(pos.min_price, 0), p.min_price) as min_price,
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
			   COALESCE(p.is_manufacturable, false) as is_manufacturable,
			   COALESCE(p.auto_manufacture, false) as auto_manufacture,
			   COALESCE(p.has_variants, false) as has_variants,
			   COALESCE(p.has_delivery, false) as has_delivery,
			   COALESCE(p.delivery_price, 0) as delivery_price,
			   p.weight, p.length, p.width, p.height,
			   p.is_active, p.tags,
			   COALESCE(p.image_url, '') as image_url,
			   COALESCE(p.inventory_type, 'trade') as inventory_type,
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
	var categoryIDStr, sku, barcode, searchKey, desc, shortDesc, unitID sql.NullString
	var categoryIDRel, categoryCode, categoryName sql.NullString
	var tags json.RawMessage
	var imageURL string
	var inventoryType string
	var weight, length, width, height sql.NullFloat64

	err = h.db.QueryRow(query, queryArgs...).Scan(
		&p.ID, &p.TenantID, &categoryIDStr, &p.Type, &p.Code, &sku, &barcode, &searchKey,
		&p.Name, &desc, &shortDesc, &unitID,
		&p.CostPrice, &p.ListPrice, &p.MinPrice,
		&p.IsStockable, &p.TrackInventory, &p.MinStockLevel,
		&p.ReorderPoint, &p.ReorderQuantity, &p.LeadTimeDays,
		&p.IsPurchasable, &p.IsSellable,
		&p.CanBeSold, &p.CanBePurchased, &p.AvailableInPOS,
		&p.CanBeExpensed, &p.CanBeRented, &p.CanBeSubcontracted,
		&p.IsOverheadExpense, &p.IsManufacturable, &p.AutoManufacture, &p.HasVariants, &p.HasDelivery, &p.DeliveryPrice,
		&weight, &length, &width, &height,
		&p.IsActive, &tags, &imageURL,
		&inventoryType,
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
		ID:                 p.ID,
		Type:               p.Type,
		Code:               p.Code,
		Name:               p.Name,
		CostPrice:          p.CostPrice,
		ListPrice:          p.ListPrice,
		IsStockable:        p.IsStockable,
		TrackInventory:     p.TrackInventory,
		MinStockLevel:      p.MinStockLevel,
		IsPurchasable:      p.IsPurchasable,
		IsSellable:         p.IsSellable,
		CanBeSold:          p.CanBeSold,
		CanBePurchased:     p.CanBePurchased,
		AvailableInPOS:     p.AvailableInPOS,
		CanBeExpensed:      p.CanBeExpensed,
		CanBeRented:        p.CanBeRented,
		CanBeSubcontracted: p.CanBeSubcontracted,
		IsOverheadExpense:  p.IsOverheadExpense,
		IsManufacturable:   p.IsManufacturable,
		AutoManufacture:    p.AutoManufacture,
		HasVariants:        p.HasVariants,
		HasDelivery:        p.HasDelivery,
		DeliveryPrice:      p.DeliveryPrice,
		IsActive:           p.IsActive,
		ImageURL:           imageURL,
		InventoryType:      inventoryType,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
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
	if searchKey.Valid {
		sk := searchKey.String
		resp.SearchKey = &sk
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

	if weight.Valid { v := weight.Float64; resp.Weight = &v }
	if length.Valid { v := length.Float64; resp.Length = &v }
	if width.Valid  { v := width.Float64;  resp.Width  = &v }
	if height.Valid { v := height.Float64; resp.Height = &v }

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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
	if input.SearchKey != nil {
		addUpdate("search_key", *input.SearchKey)
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
	if input.HasDelivery != nil {
		addUpdate("has_delivery", *input.HasDelivery)
	}
	if input.DeliveryPrice != nil {
		addUpdate("delivery_price", *input.DeliveryPrice)
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
	if input.InventoryType != nil {
		addUpdate("inventory_type", *input.InventoryType)
	}

	// Resolve UOM: prefer inventory_uom code (user just changed it), fallback to unit_id UUID
	if input.InventoryUOM != nil && *input.InventoryUOM != "" {
		if resolved := h.resolveUOMCode(tenantID, *input.InventoryUOM); resolved != nil {
			addUpdate("unit_id", *resolved)
		}
	} else if input.UnitID != nil && *input.UnitID != "" {
		if uid, parseErr := uuid.Parse(*input.UnitID); parseErr == nil {
			addUpdate("unit_id", uid)
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

		// DIAGNOSTIC: log the SET clause and key input values so we can
		// see whether list_price/cost_price are actually being included.
		// If the production payload has list_price=770000 but the SET
		// clause doesn't mention list_price, it means the JSON didn't
		// bind into input.ListPrice for some reason.
		var inCost, inList interface{} = "<nil>", "<nil>"
		if input.CostPrice != nil { inCost = *input.CostPrice }
		if input.ListPrice != nil { inList = *input.ListPrice }
		h.log.Info("UpdateProduct: products UPDATE",
			"product_id", id.String(),
			"input_cost_price", inCost,
			"input_list_price", inList,
			"organization_ids", input.OrganizationIDs,
			"set_clause", strings.Join(updates, ", "),
		)

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

	// Upsert org-specific settings.
	//
	// Build the list of orgs that need their pos row updated. Previously
	// we only wrote to pos for the request's active orgID (from the
	// header). That caused a subtle data bug: when the user's active
	// session is org A but the product is assigned only to org B,
	// pos[A] got the new price, then the org-sync block below deleted
	// pos[A] (A not in the assignment list) and left pos[B] untouched
	// with stale prices. The displayed value (COALESCE(pos.list_price,
	// p.list_price) for the viewing org) stayed at the old number.
	//
	// Fix: write the new prices to pos for every org the product is
	// currently assigned to, so prices propagate uniformly. Falls back
	// to the header orgID when the request didn't supply assignments.
	if hasOrgUpdates {
		orgsToWrite := make([]uuid.UUID, 0, len(input.OrganizationIDs)+1)
		for _, oidStr := range input.OrganizationIDs {
			if parsedOrgID, parseErr := uuid.Parse(oidStr); parseErr == nil {
				orgsToWrite = append(orgsToWrite, parsedOrgID)
			}
		}
		if len(orgsToWrite) == 0 && orgID != uuid.Nil {
			orgsToWrite = append(orgsToWrite, orgID)
		}

		for _, targetOrgID := range orgsToWrite {
			// DIAGNOSTIC: log what we're about to upsert. The per-org pos
			// row's list_price has been mysteriously stuck at the old
			// value despite this branch existing, so prove what gets
			// passed to Postgres. Remove once the price-stuck bug is
			// understood.
			var costVal, listVal interface{} = "<nil>", "<nil>"
			if orgUpdates.costPrice != nil { costVal = *orgUpdates.costPrice }
			if orgUpdates.listPrice != nil { listVal = *orgUpdates.listPrice }
			h.log.Info("UpdateProduct: pos upsert",
				"product_id", id.String(),
				"target_org_id", targetOrgID.String(),
				"cost_price_arg", costVal,
				"list_price_arg", listVal,
			)

			res, execErr := h.db.Exec(`
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
			`, tenantID, id, targetOrgID,
				orgUpdates.costPrice, orgUpdates.listPrice, orgUpdates.minPrice,
				orgUpdates.minStockLevel, orgUpdates.reorderPoint, orgUpdates.reorderQuantity)
			if execErr != nil {
				h.log.Error("Failed to upsert product org settings, trying plain UPDATE as fallback",
					"error", execErr, "product_id", id.String(), "org_id", targetOrgID.String())
				// Upsert failed (likely INSERT part). Try a plain UPDATE on existing row.
				_, fallbackErr := h.db.Exec(`
					UPDATE product_organization_settings SET
						cost_price  = COALESCE($3, cost_price),
						list_price  = COALESCE($4, list_price),
						min_price   = COALESCE($5, min_price),
						updated_at  = NOW()
					WHERE product_id = $1 AND organization_id = $2
				`, id, targetOrgID,
					orgUpdates.costPrice, orgUpdates.listPrice, orgUpdates.minPrice)
				if fallbackErr != nil {
					h.log.Error("Fallback UPDATE also failed", "error", fallbackErr)
				} else {
					h.log.Info("Fallback UPDATE succeeded for pos row")
				}
			} else {
				rows, _ := res.RowsAffected()
				h.log.Info("UpdateProduct: pos upsert result", "org_id", targetOrgID.String(), "rows_affected", rows)
			}

			// Verify what's actually stored after the upsert. If the
			// SELECT here returns the new value but the user still sees
			// the old, the issue is read-side (caching / replica lag).
			// If the SELECT returns the OLD value, the upsert never
			// applied — points at SQL-level issue we can dig into.
			var verifyList float64
			verifyErr := h.db.QueryRow(
				`SELECT list_price FROM product_organization_settings
				 WHERE product_id = $1 AND organization_id = $2`,
				id, targetOrgID,
			).Scan(&verifyList)
			h.log.Info("UpdateProduct: pos verify",
				"org_id", targetOrgID.String(),
				"list_price_after_upsert", verifyList,
				"verify_err", verifyErr,
			)
		}
	}

	// Sync organization assignments without destroying existing per-org
	// pricing. The old code DELETE'd every product_organization_settings
	// row and reinserted them with hard-coded zeros — which wiped the
	// cost_price/list_price the user had just set (the per-org upsert
	// above ran first, then this block deleted its work). Now we only
	// delete rows for orgs that were removed from the list, and INSERT
	// ON CONFLICT DO NOTHING preserves prices for orgs still selected.
	if len(input.OrganizationIDs) > 0 {
		orgIDsToKeep := make([]uuid.UUID, 0, len(input.OrganizationIDs))
		for _, oid := range input.OrganizationIDs {
			if parsedOrgID, parseErr := uuid.Parse(oid); parseErr == nil {
				orgIDsToKeep = append(orgIDsToKeep, parsedOrgID)
			}
		}

		h.db.Exec(`
			DELETE FROM product_organization_settings
			WHERE product_id = $1 AND tenant_id = $2
			  AND organization_id <> ALL($3)
		`, id, tenantID, pq.Array(orgIDsToKeep))

		for _, parsedOrgID := range orgIDsToKeep {
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

	// Check for existing inventory. The previous check was `quantity_on_hand > 0`,
	// which let products with NEGATIVE stock slip through (e.g., when construction
	// consumption ran ahead of procurement). Soft-deleting such a product orphaned
	// the negative inventory row — invisible to the products list (it filters by
	// deleted_at IS NULL) but still dragging warehouse totals down because the
	// inventory listing did not. Use `<> 0` so any non-zero balance (positive OR
	// negative) blocks the delete and forces reconciliation first.
	var hasInventory bool
	h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM inventory WHERE product_id = $1 AND quantity_on_hand <> 0)
	`, id).Scan(&hasInventory)

	if hasInventory {
		response.BadRequest(c, "Cannot delete product with non-zero inventory (positive or negative). Reconcile stock first, then set the product to inactive.")
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
			COALESCE(cos.stock_output_account_id, pc.stock_output_account_id) as stock_output_account_id,
			COALESCE(pcnt.product_count, 0) as product_count
		FROM product_categories pc
		-- product_count was a correlated COUNT over products evaluated once per
		-- category row: O(categories x products) on a screen that loads on every
		-- catalogue visit. One grouped pass instead.
		LEFT JOIN (
		    SELECT category_id, COUNT(*) AS product_count
		    FROM products
		    WHERE tenant_id = $1 AND deleted_at IS NULL
		    GROUP BY category_id
		) pcnt ON pcnt.category_id = pc.id
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

	if orgID != uuid.Nil {
		query += fmt.Sprintf(` AND (pc.origin_organization_id = $%d OR pc.origin_organization_id IS NULL)`, len(args))
	}

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
			&cat.IncomeAccountID, &cat.ExpenseAccountID, &cat.StockValuationAccountID, &cat.StockInputAccountID, &cat.StockOutputAccountID,
			&cat.ProductCount)
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

// =====================================================
// BULK PRODUCT IMPORT
// =====================================================
//
// Background:
//   The previous import flow on Products.jsx did `await createProduct(...)`
//   in a sequential `for` loop, one HTTP call per row. For a 690-row
//   xlsx that's ~2-3 minutes of UI freeze, and a single mid-loop failure
//   (slug collision, missing UOM, FK violation) bails the whole import
//   with a single terse error in the modal — leaving partial state in
//   the DB and no per-row diagnostics for the admin.
//
// This bulk endpoint:
//   * Accepts an array of product inputs in one request.
//   * Pre-resolves category names → category_id (case-insensitive) using
//     a single tenant-wide lookup, so the frontend doesn't have to.
//   * Skips rows that would collide on (tenant, code) / (tenant, barcode) /
//     (tenant, sku) instead of failing the whole import.
//   * Inserts in batches of 200 rows per multi-row INSERT, then a single
//     batched INSERT into product_organization_settings for visibility.
//   * Returns a per-row outcome list so the frontend can show
//     "X imported, Y skipped, Z failed" with the offending names.
//
// Permissions: same as POST /products — gated by `inventory:product:create`
// at the route registration site.

type BulkProductInput struct {
	// When ID is non-empty the row is processed as an UPDATE rather
	// than a CREATE. Set by the export → edit → re-import round-trip
	// flow; allows users to edit a single column on N rows in Excel
	// and re-upload without creating duplicates.
	ID string `json:"id,omitempty"`

	Name        string   `json:"name"`
	Code        string   `json:"code,omitempty"`
	Barcode     string   `json:"barcode,omitempty"`
	SKU         string   `json:"sku,omitempty"`
	CategoryID  string   `json:"category_id,omitempty"`
	Category    string   `json:"category,omitempty"` // optional name fallback when ID isn't known
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Type        string   `json:"type,omitempty"`
	CostPrice   float64  `json:"cost_price"`
	ListPrice   float64  `json:"list_price"`
	IsActive    *bool    `json:"is_active,omitempty"`

	// Pointer-typed extension fields. Used by the UPDATE branch to
	// distinguish "field absent in JSON" (nil → don't touch the
	// column) from "field set to empty string / 0" (which would
	// blank existing values). For CREATE rows these may all be nil
	// and the backend uses the column defaults from the schema.
	WholesalePrice       *float64 `json:"wholesale_price,omitempty"`
	MinPrice             *float64 `json:"min_price,omitempty"`
	DeliveryPrice        *float64 `json:"delivery_price,omitempty"`
	Brand                *string  `json:"brand,omitempty"`
	Manufacturer         *string  `json:"manufacturer,omitempty"`
	ModelNumber          *string  `json:"model_number,omitempty"`
	UPC                  *string  `json:"upc,omitempty"`
	EAN                  *string  `json:"ean,omitempty"`
	ISBN                 *string  `json:"isbn,omitempty"`
	MPN                  *string  `json:"mpn,omitempty"`
	HSCode               *string  `json:"hs_code,omitempty"`
	CountryOfOrigin      *string  `json:"country_of_origin,omitempty"`
	SearchKey            *string  `json:"search_key,omitempty"`
	InventoryType        *string  `json:"inventory_type,omitempty"`
	StorageConditions    *string  `json:"storage_conditions,omitempty"`
	SupplierSKU          *string  `json:"supplier_sku,omitempty"`
	Weight               *float64 `json:"weight,omitempty"`
	Length               *float64 `json:"length,omitempty"`
	Width                *float64 `json:"width,omitempty"`
	Height               *float64 `json:"height,omitempty"`
	MinStockLevel        *float64 `json:"min_stock_level,omitempty"`
	ReorderPoint         *float64 `json:"reorder_point,omitempty"`
	ReorderQuantity      *float64 `json:"reorder_quantity,omitempty"`
	WarrantyMonths       *int     `json:"warranty_months,omitempty"`
	LeadTimeDays         *int     `json:"lead_time_days,omitempty"`
	CustomerLeadTimeDays *int     `json:"customer_lead_time_days,omitempty"`
	ShelfLifeDays        *int     `json:"shelf_life_days,omitempty"`
}

type BulkCreateProductsInput struct {
	Products        []BulkProductInput `json:"products" binding:"required,min=1"`
	OrganizationIDs []string           `json:"organization_ids,omitempty"`
}

type BulkProductOutcome struct {
	Row     int    `json:"row"`           // 1-based row index in the request
	Name    string `json:"name"`
	Status  string `json:"status"`        // "created" | "skipped" | "failed"
	Reason  string `json:"reason,omitempty"`
	ID      string `json:"id,omitempty"`
}

// slugifyProductCode mirrors the simple slugifier the frontend uses so a
// row that omits `code` gets a deterministic auto-generated value:
//   row.name.toUpperCase().replace(/\s+/g, '-').substring(0, 50).
func slugifyProductCode(name string) string {
	upper := strings.ToUpper(strings.TrimSpace(name))
	// collapse whitespace runs to a single dash
	parts := strings.Fields(upper)
	joined := strings.Join(parts, "-")
	if len([]rune(joined)) > 50 {
		joined = string([]rune(joined)[:50])
	}
	return joined
}

// BulkCreateProducts inserts many products in one request. Each row is
// processed independently with the same SQL the single-product
// CreateProduct handler uses, so behaviour is identical to "click New
// Product 690 times" — just compressed into one HTTP round-trip with a
// per-row outcome list.
//
// Decisions deliberately made simple:
//   - No multi-row INSERT batching. We do one INSERT per product. Slower
//     by a constant factor but trivially debuggable and matches the
//     working single-product code path byte-for-byte.
//   - Each row is wrapped in its own scope so a failure on row N
//     doesn't roll back rows 1..N-1.
//   - If a row's name already exists in the tenant we DON'T create a
//     duplicate; we just add a product_organization_settings link to
//     the requested orgs (idempotent via ON CONFLICT DO NOTHING). This
//     means re-importing the same xlsx in a new active company makes
//     the existing products visible there too — which is what users
//     actually want.
func (h *Handler) BulkCreateProducts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	var input BulkCreateProductsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid bulk product input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Resolve target organization IDs: explicit list takes priority,
	// otherwise fall back to the X-Organization-ID header (matches the
	// single-product behaviour).
	targetOrgIDs := make([]uuid.UUID, 0, len(input.OrganizationIDs)+1)
	seenOrgs := make(map[uuid.UUID]bool)
	for _, raw := range input.OrganizationIDs {
		if parsed, err := uuid.Parse(raw); err == nil && !seenOrgs[parsed] {
			targetOrgIDs = append(targetOrgIDs, parsed)
			seenOrgs[parsed] = true
		}
	}
	if len(targetOrgIDs) == 0 && orgID != uuid.Nil {
		targetOrgIDs = append(targetOrgIDs, orgID)
		seenOrgs[orgID] = true
	}

	// Pre-fetch tenant-scoped lookup data once instead of per-row.
	categoryNameToID := make(map[string]uuid.UUID)
	if catRows, err := h.db.Query(
		`SELECT id, name FROM product_categories
		  WHERE tenant_id = $1 AND deleted_at IS NULL`,
		tenantID,
	); err == nil {
		for catRows.Next() {
			var cid uuid.UUID
			var cname string
			if scanErr := catRows.Scan(&cid, &cname); scanErr == nil {
				categoryNameToID[strings.ToLower(strings.TrimSpace(cname))] = cid
			}
		}
		catRows.Close()
	}

	// Collect every distinct category name referenced by the import that
	// isn't already in the tenant — auto-create them so users don't have
	// to create categories manually before importing. Code is a slug
	// of the name, capped to fit product_categories.code's length.
	missingCats := make(map[string]bool)
	for _, p := range input.Products {
		raw := strings.TrimSpace(p.Category)
		if raw == "" {
			continue
		}
		if _, ok := categoryNameToID[strings.ToLower(raw)]; ok {
			continue
		}
		missingCats[raw] = true
	}
	for catName := range missingCats {
		newCatID := uuid.New()
		// Slugified code from the name; product_categories has the same
		// VARCHAR(50) constraint as products.code (migration 002).
		catCode := slugifyProductCode(catName)
		if catCode == "" {
			catCode = "CAT-" + newCatID.String()[:8]
		}
		// Insert with a unique-suffix retry on collision so two imports
		// in different runs don't fight over the same code slug.
		var inserted bool
		for attempt := 0; attempt < 50 && !inserted; attempt++ {
			tryCode := catCode
			if attempt > 0 {
				suffix := fmt.Sprintf("-%d", attempt+1)
				room := 50 - len([]rune(suffix))
				if room < 1 {
					break
				}
				baseRunes := []rune(catCode)
				if len(baseRunes) > room {
					baseRunes = baseRunes[:room]
				}
				tryCode = string(baseRunes) + suffix
			}
			_, err := h.db.Exec(`
				INSERT INTO product_categories (
					id, tenant_id, code, name, is_active, created_at, updated_at
				) VALUES ($1, $2, $3, $4, true, NOW(), NOW())
			`, newCatID, tenantID, tryCode, catName)
			if err == nil {
				inserted = true
			} else if !strings.Contains(err.Error(), "duplicate") {
				h.log.Warn("bulk products: failed to auto-create category",
					"error", err, "name", catName, "code", tryCode)
				break
			}
		}
		if inserted {
			categoryNameToID[strings.ToLower(catName)] = newCatID
		}
	}

	// Pre-fetch ALL products in the tenant — including soft-deleted ones.
	// The `UNIQUE(tenant_id, code)` constraint on products is NOT partial,
	// so soft-deleted rows still occupy the (tenant_id, code) slot. If we
	// ignored them in this map, my handler would think a new INSERT is
	// safe but Postgres would reject it as a duplicate key. We track the
	// soft-deleted state per row so the per-row logic below can RESTORE
	// the deleted product on a name match instead of trying to insert
	// over its zombie code.
	type existingRow struct {
		id        uuid.UUID
		deleted   bool
	}
	existingNamesLower := make(map[string]existingRow) // name(lower) → row
	existingCodes := make(map[string]bool)
	if dRows, err := h.db.Query(
		`SELECT id, code, name, (deleted_at IS NOT NULL) AS deleted
		   FROM products
		  WHERE tenant_id = $1`,
		tenantID,
	); err == nil {
		for dRows.Next() {
			var pid uuid.UUID
			var code, name string
			var deleted bool
			if scanErr := dRows.Scan(&pid, &code, &name, &deleted); scanErr == nil {
				if code != "" {
					existingCodes[code] = true
				}
				if name != "" {
					existingNamesLower[strings.ToLower(strings.TrimSpace(name))] = existingRow{id: pid, deleted: deleted}
				}
			}
		}
		dRows.Close()
	}

	const maxCodeLen = 50
	truncateRunes := func(s string, n int) string {
		r := []rune(s)
		if len(r) > n {
			return string(r[:n])
		}
		return s
	}

	// linkProductToOrgs writes product_organization_settings rows for
	// every targetOrgID. Uses ON CONFLICT DO NOTHING so calling it on
	// an already-linked (product, org) is a safe no-op.
	linkProductToOrgs := func(productID uuid.UUID, costPrice, listPrice float64) error {
		if len(targetOrgIDs) == 0 {
			return nil
		}
		for _, oid := range targetOrgIDs {
			if _, err := h.db.Exec(`
				INSERT INTO product_organization_settings (
					tenant_id, product_id, organization_id,
					cost_price, list_price, min_price,
					min_stock_level, reorder_point, reorder_quantity
				) VALUES ($1, $2, $3, $4, $5, 0, 0, 0, 0)
				ON CONFLICT (product_id, organization_id) DO NOTHING
			`, tenantID, productID, oid, costPrice, listPrice); err != nil {
				return err
			}
		}
		return nil
	}

	outcomes := make([]BulkProductOutcome, 0, len(input.Products))
	now := time.Now()

	var origOrgPtr *uuid.UUID
	if orgID != uuid.Nil {
		origOrgPtr = &orgID
	}

	// Default UOM for bulk-imported rows. The Excel template doesn't expose
	// UOM columns, so without this every imported product would land with
	// unit_id = NULL, breaking downstream UI (PO modal, BOM, etc.) that
	// expects to show the unit next to quantity. Looked up once per request.
	defaultUnitID := h.resolveUOMCode(tenantID, "unit")

	for i, p := range input.Products {
		rowNum := i + 1

		// ── UPDATE branch ──────────────────────────────────────────────
		// When a row carries an ID we treat it as an edit of an
		// existing product (the round-trip Export → Edit in Excel →
		// Re-import flow). We only write the columns the caller
		// actually populated — empty strings and nil pointers leave
		// the existing column value untouched. This makes "edit one
		// column on N rows" safe even when the export omits or blanks
		// other columns.
		if strings.TrimSpace(p.ID) != "" {
			productID, parseErr := uuid.Parse(strings.TrimSpace(p.ID))
			if parseErr != nil {
				outcomes = append(outcomes, BulkProductOutcome{
					Row: rowNum, Name: p.Name,
					Status: "failed", Reason: "invalid id (not a UUID)",
				})
				continue
			}
			// Verify the product exists for this tenant. Tenant scope
			// is critical — without it a user could overwrite another
			// tenant's product by guessing a UUID.
			var existingName string
			err := h.db.QueryRow(
				`SELECT name FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
				productID, tenantID,
			).Scan(&existingName)
			if err == sql.ErrNoRows {
				outcomes = append(outcomes, BulkProductOutcome{
					Row: rowNum, Name: p.Name,
					Status: "skipped", Reason: "product not found for tenant",
					ID: productID.String(),
				})
				continue
			}
			if err != nil {
				outcomes = append(outcomes, BulkProductOutcome{
					Row: rowNum, Name: p.Name,
					Status: "failed", Reason: "lookup failed: " + err.Error(),
					ID: productID.String(),
				})
				continue
			}

			// Build a dynamic UPDATE from whichever fields are present.
			// Strings: non-empty values are written. Pointer fields:
			// non-nil pointers are written (even pointing to "" or 0,
			// because the frontend explicitly sent that — frontend
			// strips truly absent values before sending).
			sets := []string{}
			args := []interface{}{}
			argN := 1
			addStr := func(col, val string) {
				if val == "" { return }
				sets = append(sets, fmt.Sprintf("%s = $%d", col, argN))
				args = append(args, val); argN++
			}
			addStrPtr := func(col string, v *string) {
				if v == nil { return }
				sets = append(sets, fmt.Sprintf("%s = $%d", col, argN))
				args = append(args, *v); argN++
			}
			addF64Ptr := func(col string, v *float64) {
				if v == nil { return }
				sets = append(sets, fmt.Sprintf("%s = $%d", col, argN))
				args = append(args, *v); argN++
			}
			addIntPtr := func(col string, v *int) {
				if v == nil { return }
				sets = append(sets, fmt.Sprintf("%s = $%d", col, argN))
				args = append(args, *v); argN++
			}

			// Plain strings — only when non-empty so blank Excel cells
			// don't wipe existing values.
			addStr("name", strings.TrimSpace(p.Name))
			addStr("barcode", strings.TrimSpace(p.Barcode))
			addStr("sku", strings.TrimSpace(p.SKU))
			addStr("description", strings.TrimSpace(p.Description))
			addStr("type", strings.TrimSpace(p.Type))

			// Category by ID OR by name. If ID is given, use it; else
			// look up by name and use the matching id.
			if catID := strings.TrimSpace(p.CategoryID); catID != "" {
				if cid, err := uuid.Parse(catID); err == nil {
					sets = append(sets, fmt.Sprintf("category_id = $%d", argN))
					args = append(args, cid); argN++
				}
			} else if catName := strings.TrimSpace(p.Category); catName != "" {
				var cid uuid.UUID
				if err := h.db.QueryRow(
					`SELECT id FROM product_categories
					 WHERE tenant_id = $1 AND LOWER(name) = LOWER($2)
					 LIMIT 1`,
					tenantID, catName,
				).Scan(&cid); err == nil {
					sets = append(sets, fmt.Sprintf("category_id = $%d", argN))
					args = append(args, cid); argN++
				}
			}

			// Tags — only update if a non-nil slice was sent. Note:
			// the frontend sends `undefined` (omits) on update rows
			// when no tags column was present; an explicit empty
			// array would clear them. Tags are stored as JSON in
			// the products table, mirroring CreateProduct (line 525).
			if p.Tags != nil {
				if tagsJSON, jerr := json.Marshal(p.Tags); jerr == nil {
					sets = append(sets, fmt.Sprintf("tags = $%d", argN))
					args = append(args, tagsJSON); argN++
				}
			}

			// is_active is a pointer so we can distinguish absent vs false.
			if p.IsActive != nil {
				sets = append(sets, fmt.Sprintf("is_active = $%d", argN))
				args = append(args, *p.IsActive); argN++
			}

			// Cost / list price — non-pointer floats. Only write if
			// the value is greater than zero (treating "0" as "user
			// didn't fill this in"). Users who legitimately want to
			// set a price to 0 can use the single-product edit form.
			if p.CostPrice > 0 {
				sets = append(sets, fmt.Sprintf("cost_price = $%d", argN))
				args = append(args, p.CostPrice); argN++
			}
			if p.ListPrice > 0 {
				sets = append(sets, fmt.Sprintf("list_price = $%d", argN))
				args = append(args, p.ListPrice); argN++
			}

			// Pointer-typed extension fields.
			addF64Ptr("wholesale_price", p.WholesalePrice)
			addF64Ptr("min_price", p.MinPrice)
			addF64Ptr("delivery_price", p.DeliveryPrice)
			addStrPtr("brand", p.Brand)
			addStrPtr("manufacturer", p.Manufacturer)
			addStrPtr("model_number", p.ModelNumber)
			addStrPtr("upc", p.UPC)
			addStrPtr("ean", p.EAN)
			addStrPtr("isbn", p.ISBN)
			addStrPtr("mpn", p.MPN)
			addStrPtr("hs_code", p.HSCode)
			addStrPtr("country_of_origin", p.CountryOfOrigin)
			addStrPtr("search_key", p.SearchKey)
			addStrPtr("inventory_type", p.InventoryType)
			addStrPtr("storage_conditions", p.StorageConditions)
			addStrPtr("supplier_sku", p.SupplierSKU)
			addF64Ptr("weight", p.Weight)
			addF64Ptr("length", p.Length)
			addF64Ptr("width", p.Width)
			addF64Ptr("height", p.Height)
			addF64Ptr("min_stock_level", p.MinStockLevel)
			addF64Ptr("reorder_point", p.ReorderPoint)
			addF64Ptr("reorder_quantity", p.ReorderQuantity)
			addIntPtr("warranty_months", p.WarrantyMonths)
			addIntPtr("lead_time_days", p.LeadTimeDays)
			addIntPtr("customer_lead_time_days", p.CustomerLeadTimeDays)
			addIntPtr("shelf_life_days", p.ShelfLifeDays)

			if len(sets) == 0 {
				outcomes = append(outcomes, BulkProductOutcome{
					Row: rowNum, Name: existingName,
					Status: "skipped", Reason: "no fields to update",
					ID: productID.String(),
				})
				continue
			}

			// Always bump updated_at, then add the WHERE args.
			sets = append(sets, fmt.Sprintf("updated_at = $%d", argN))
			args = append(args, time.Now()); argN++
			args = append(args, productID, tenantID)

			query := fmt.Sprintf(
				"UPDATE products SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
				strings.Join(sets, ", "), argN, argN+1,
			)
			res, err := h.db.Exec(query, args...)
			if err != nil {
				outcomes = append(outcomes, BulkProductOutcome{
					Row: rowNum, Name: existingName,
					Status: "failed", Reason: "update failed: " + err.Error(),
					ID: productID.String(),
				})
				continue
			}

			// Diagnostic: log which columns were SET on this row plus
			// rows-affected. Without this, "20 updated" toasts that
			// don't reflect on the UI are impossible to debug because
			// we can't tell whether the SET clause actually included
			// the user's edit or whether the WHERE matched.
			rowsAffected, _ := res.RowsAffected()
			h.log.Info("BulkCreateProducts: row updated",
				"row", rowNum,
				"product_id", productID.String(),
				"sets", strings.Join(sets, ", "),
				"cost_price", p.CostPrice,
				"list_price", p.ListPrice,
				"rows_affected", rowsAffected,
			)
			if rowsAffected == 0 {
				// SET clauses were valid but WHERE didn't match — most
				// likely a tenant_id mismatch (export from one tenant,
				// re-imported into another). Surface as failed so the
				// user sees something actually went wrong instead of
				// the cosmetic "updated" status.
				outcomes = append(outcomes, BulkProductOutcome{
					Row: rowNum, Name: existingName,
					Status: "failed", Reason: "update affected 0 rows (tenant or id mismatch)",
					ID: productID.String(),
				})
				continue
			}

			// Per-org price overrides: the products list view reads
			// COALESCE(pos.cost_price, p.cost_price) and same for
			// list_price (see GetProducts query line 74-75). If we
			// only touch the `products` table and a non-NULL row
			// already exists in product_organization_settings for
			// this product+org, the list view keeps showing the OLD
			// price because COALESCE picks pos's non-null value over
			// the products table's freshly-updated value.
			//
			// Fix: also upsert pos for each target org, mirroring the
			// single-product UpdateProduct flow (line 1052+). We only
			// write fields the user actually populated in this row —
			// COALESCE($n, existing) preserves untouched columns.
			if orgID != uuid.Nil && (p.CostPrice > 0 || p.ListPrice > 0 || p.MinPrice != nil ||
				p.MinStockLevel != nil || p.ReorderPoint != nil || p.ReorderQuantity != nil) {
				var costArg, listArg, minPriceArg, minStockArg, reorderPtArg, reorderQtyArg interface{}
				if p.CostPrice > 0 { costArg = p.CostPrice }
				if p.ListPrice > 0 { listArg = p.ListPrice }
				if p.MinPrice != nil { minPriceArg = *p.MinPrice }
				if p.MinStockLevel != nil { minStockArg = *p.MinStockLevel }
				if p.ReorderPoint != nil { reorderPtArg = *p.ReorderPoint }
				if p.ReorderQuantity != nil { reorderQtyArg = *p.ReorderQuantity }

				if _, posErr := h.db.Exec(`
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
				`, tenantID, productID, orgID,
					costArg, listArg, minPriceArg,
					minStockArg, reorderPtArg, reorderQtyArg); posErr != nil {
					// Non-fatal: products table got the new value, only
					// the per-org override failed. Log so the operator
					// can investigate but don't fail the row.
					h.log.Warn("BulkCreateProducts: failed to upsert per-org overrides",
						"error", posErr, "product_id", productID.String(), "org_id", orgID.String())
				}
			}

			outcomes = append(outcomes, BulkProductOutcome{
				Row: rowNum, Name: existingName,
				Status: "updated", ID: productID.String(),
			})
			continue
		}

		name := strings.TrimSpace(p.Name)
		if name == "" {
			outcomes = append(outcomes, BulkProductOutcome{
				Row: rowNum, Name: p.Name,
				Status: "skipped", Reason: "name is empty",
			})
			continue
		}

		// If this name already exists in the tenant (active OR soft-
		// deleted), don't try to INSERT — instead restore + link.
		// Soft-deleted matches happen when an admin deleted products and
		// is now re-importing the same xlsx; the user's mental model is
		// "I want these products back". Restore them by clearing
		// deleted_at, refresh cost/price, then link to the active org(s).
		if existingMatch, exists := existingNamesLower[strings.ToLower(name)]; exists {
			if existingMatch.deleted {
				if _, restoreErr := h.db.Exec(`
					UPDATE products
					   SET deleted_at = NULL,
					       cost_price = $2,
					       list_price = $3,
					       is_active  = true,
					       updated_at = NOW()
					 WHERE id = $1
				`, existingMatch.id, p.CostPrice, p.ListPrice); restoreErr != nil {
					outcomes = append(outcomes, BulkProductOutcome{
						Row: rowNum, Name: name,
						Status: "failed", Reason: "restore deleted product failed: " + restoreErr.Error(),
					})
					continue
				}
			}
			if linkErr := linkProductToOrgs(existingMatch.id, p.CostPrice, p.ListPrice); linkErr != nil {
				outcomes = append(outcomes, BulkProductOutcome{
					Row: rowNum, Name: name,
					Status: "failed", Reason: "link existing product failed: " + linkErr.Error(),
				})
				continue
			}
			reason := "linked existing product to active company"
			if existingMatch.deleted {
				reason = "restored soft-deleted product and linked to active company"
			}
			outcomes = append(outcomes, BulkProductOutcome{
				Row: rowNum, Name: name,
				Status: "created", // user-facing: "now visible in this company"
				Reason: reason,
				ID:     existingMatch.id.String(),
			})
			// Mark as no-longer-deleted so a later row with the same
			// name in the same batch goes through the "active" path.
			existingMatch.deleted = false
			existingNamesLower[strings.ToLower(name)] = existingMatch
			continue
		}

		// Generate a unique code.
		code := strings.TrimSpace(p.Code)
		if code == "" {
			code = slugifyProductCode(name)
		}
		code = truncateRunes(code, maxCodeLen)
		if existingCodes[code] {
			base := code
			found := false
			for attempt := 2; attempt < 1000; attempt++ {
				suffix := fmt.Sprintf("-%d", attempt)
				baseRoom := maxCodeLen - len([]rune(suffix))
				if baseRoom < 1 {
					break
				}
				candidate := truncateRunes(base, baseRoom) + suffix
				if !existingCodes[candidate] {
					code = candidate
					found = true
					break
				}
			}
			if !found {
				outcomes = append(outcomes, BulkProductOutcome{
					Row: rowNum, Name: name,
					Status: "skipped", Reason: "could not generate a unique code under 50 chars",
				})
				continue
			}
		}

		// Resolve category by id or by name.
		var categoryID *uuid.UUID
		if p.CategoryID != "" {
			if cid, err := uuid.Parse(p.CategoryID); err == nil {
				categoryID = &cid
			}
		}
		if categoryID == nil && p.Category != "" {
			if cid, ok := categoryNameToID[strings.ToLower(strings.TrimSpace(p.Category))]; ok {
				categoryID = &cid
			}
		}

		tagsJSON := []byte("[]")
		if len(p.Tags) > 0 {
			if encoded, err := json.Marshal(p.Tags); err == nil {
				tagsJSON = encoded
			}
		}

		var description *string
		if d := strings.TrimSpace(p.Description); d != "" {
			description = &d
		}

		var barcodePtr *string
		if b := strings.TrimSpace(p.Barcode); b != "" {
			barcodePtr = &b
		}
		var skuPtr *string
		if s := strings.TrimSpace(p.SKU); s != "" {
			skuPtr = &s
		}

		pType := strings.TrimSpace(p.Type)
		if pType == "" {
			pType = "product"
		}
		isActive := true
		if p.IsActive != nil {
			isActive = *p.IsActive
		}

		newID := uuid.New()

		// Per-row INSERT — same SQL shape as the single-product
		// CreateProduct handler, so behaviour matches exactly. All other
		// columns (is_stockable, track_inventory, is_purchasable,
		// is_sellable, can_be_*, inventory_type, etc.) inherit their
		// table-level defaults, which is what CreateProduct does by
		// default too when the corresponding pointer fields are nil.
		_, err := h.db.Exec(`
			INSERT INTO products (
				id, tenant_id, origin_organization_id, category_id, type,
				code, sku, barcode, name, description,
				unit_id, purchase_unit_id, sales_unit_id,
				cost_price, list_price, is_active, tags,
				created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $19)
		`,
			newID, tenantID, origOrgPtr, categoryID, pType,
			code, skuPtr, barcodePtr, name, description,
			defaultUnitID, defaultUnitID, defaultUnitID,
			p.CostPrice, p.ListPrice, isActive, tagsJSON,
			userID, now,
		)
		if err != nil {
			outcomes = append(outcomes, BulkProductOutcome{
				Row: rowNum, Name: name,
				Status: "failed", Reason: err.Error(),
			})
			continue
		}

		// Mark this name/code as taken so subsequent rows in the same
		// batch with the same name link to it instead of trying to
		// create a duplicate.
		existingCodes[code] = true
		existingNamesLower[strings.ToLower(name)] = existingRow{id: newID, deleted: false}

		// Link to active org(s) — same call CreateProduct makes when
		// `organization_ids` is provided.
		if linkErr := linkProductToOrgs(newID, p.CostPrice, p.ListPrice); linkErr != nil {
			h.log.Warn("bulk products: failed to link new product to org",
				"error", linkErr, "product_id", newID, "name", name)
			// The product itself was created; we still report success.
			// Next time the user re-imports we'll repair the link
			// because the name will match and we'll go through the
			// link-existing path.
		}

		outcomes = append(outcomes, BulkProductOutcome{
			Row: rowNum, Name: name,
			Status: "created",
			ID:     newID.String(),
		})
	}

	created, skipped, failed := 0, 0, 0
	for _, oc := range outcomes {
		switch oc.Status {
		case "created":
			created++
		case "skipped":
			skipped++
		case "failed":
			failed++
		}
	}

	// On any failures, log the first few examples to the backend log so
	// the operator can diagnose without digging into the response body.
	if failed > 0 {
		examples := make([]map[string]string, 0, 5)
		for _, oc := range outcomes {
			if oc.Status == "failed" {
				examples = append(examples, map[string]string{
					"row":    fmt.Sprintf("%d", oc.Row),
					"name":   oc.Name,
					"reason": oc.Reason,
				})
				if len(examples) >= 5 {
					break
				}
			}
		}
		h.log.Warn("BulkCreateProducts: rows failed",
			"created", created, "skipped", skipped, "failed", failed,
			"total", len(input.Products),
			"examples", examples,
		)
	}

	response.Success(c, gin.H{
		"created":  created,
		"skipped":  skipped,
		"failed":   failed,
		"total":    len(input.Products),
		"outcomes": outcomes,
	})
}
