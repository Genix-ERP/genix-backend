package handler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// =====================================================
// PRODUCT ATTRIBUTES
// =====================================================

type ProductAttribute struct {
	ID            uuid.UUID        `json:"id"`
	TenantID      uuid.UUID        `json:"tenant_id"`
	Name          string           `json:"name"`
	Code          *string          `json:"code"`
	DisplayType   string           `json:"display_type"`
	CreateVariant bool             `json:"create_variant"`
	SortOrder     int              `json:"sort_order"`
	IsActive      bool             `json:"is_active"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Values        []AttributeValue `json:"values,omitempty"`
}

type AttributeValue struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	AttributeID uuid.UUID `json:"attribute_id"`
	Name        string    `json:"name"`
	Code        *string   `json:"code"`
	HTMLColor   *string   `json:"html_color"`
	SortOrder   int       `json:"sort_order"`
	PriceExtra  float64   `json:"price_extra"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListProductAttributes returns all product attributes for a tenant
func (h *Handler) ListProductAttributes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, tenant_id, name, code, display_type, create_variant, sort_order, is_active, created_at, updated_at
		FROM product_attributes
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY sort_order, name
	`, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to fetch product attributes")
		return
	}
	defer rows.Close()

	// make(...) not a nil slice: a nil slice marshals as `null`, not `[]`, so a
	// tenant with no attributes broke clients doing `data as List`.
	attributes := make([]ProductAttribute, 0)
	attrIDs := []uuid.UUID{}
	for rows.Next() {
		var attr ProductAttribute
		err := rows.Scan(&attr.ID, &attr.TenantID, &attr.Name, &attr.Code, &attr.DisplayType,
			&attr.CreateVariant, &attr.SortOrder, &attr.IsActive, &attr.CreatedAt, &attr.UpdatedAt)
		if err != nil {
			continue
		}
		attr.Values = []AttributeValue{}
		attributes = append(attributes, attr)
		attrIDs = append(attrIDs, attr.ID)
	}
	rows.Close()

	// One query for every attribute's values instead of one per attribute
	// (N attributes used to cost N+1 round trips).
	if len(attrIDs) > 0 {
		byAttr := map[uuid.UUID][]AttributeValue{}
		if vr, verr := h.db.Query(`
			SELECT id, tenant_id, attribute_id, name, code, html_color, sort_order, COALESCE(price_extra, 0), is_active, created_at
			FROM product_attribute_values
			WHERE attribute_id = ANY($1) AND deleted_at IS NULL
			ORDER BY sort_order, name
		`, pq.Array(attrIDs)); verr == nil {
			for vr.Next() {
				var val AttributeValue
				if vr.Scan(&val.ID, &val.TenantID, &val.AttributeID, &val.Name, &val.Code,
					&val.HTMLColor, &val.SortOrder, &val.PriceExtra, &val.IsActive, &val.CreatedAt) == nil {
					byAttr[val.AttributeID] = append(byAttr[val.AttributeID], val)
				}
			}
			vr.Close()
		}
		for i := range attributes {
			if v, ok := byAttr[attributes[i].ID]; ok {
				attributes[i].Values = v
			}
		}
	}

	response.Success(c, attributes)
}

// CreateProductAttribute creates a new product attribute
func (h *Handler) CreateProductAttribute(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var input struct {
		Name          string  `json:"name" binding:"required"`
		Code          *string `json:"code"`
		DisplayType   string  `json:"display_type"`
		CreateVariant bool    `json:"create_variant"`
		SortOrder     int     `json:"sort_order"`
		Values        []struct {
			Name      string  `json:"name"`
			Code      *string `json:"code"`
			HTMLColor *string `json:"html_color"`
			SortOrder int     `json:"sort_order"`
		} `json:"values"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.DisplayType == "" {
		input.DisplayType = "select"
	}

	now := time.Now()
	attrID := uuid.New()

	_, err := h.db.Exec(`
		INSERT INTO product_attributes (id, tenant_id, name, code, display_type, create_variant, sort_order, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true, $8, $8)
	`, attrID, tenantID, input.Name, input.Code, input.DisplayType, input.CreateVariant, input.SortOrder, now)
	if err != nil {
		h.log.Error("Failed to create product attribute", "error", err)
		response.InternalError(c, "Failed to create product attribute")
		return
	}

	// Create attribute values
	for i, val := range input.Values {
		_, err = h.db.Exec(`
			INSERT INTO product_attribute_values (id, tenant_id, attribute_id, name, code, html_color, sort_order, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, true, $8, $8)
		`, uuid.New(), tenantID, attrID, val.Name, val.Code, val.HTMLColor, i, now)
		if err != nil {
			// Log but continue
			fmt.Printf("Failed to create attribute value: %v\n", err)
		}
	}

	response.Success(c, map[string]interface{}{
		"id":      attrID,
		"message": "Product attribute created successfully",
	})
}

// UpdateProductAttribute updates a product attribute
func (h *Handler) UpdateProductAttribute(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	attrID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid attribute ID")
		return
	}

	var input struct {
		Name          string  `json:"name"`
		Code          *string `json:"code"`
		DisplayType   string  `json:"display_type"`
		CreateVariant *bool   `json:"create_variant"`
		SortOrder     *int    `json:"sort_order"`
		IsActive      *bool   `json:"is_active"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE product_attributes
		SET name = COALESCE(NULLIF($1, ''), name),
			code = COALESCE($2, code),
			display_type = COALESCE(NULLIF($3, ''), display_type),
			create_variant = COALESCE($4, create_variant),
			sort_order = COALESCE($5, sort_order),
			is_active = COALESCE($6, is_active),
			updated_at = $7
		WHERE id = $8 AND tenant_id = $9 AND deleted_at IS NULL
	`, input.Name, input.Code, input.DisplayType, input.CreateVariant, input.SortOrder, input.IsActive, now, attrID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to update product attribute")
		return
	}

	response.Success(c, map[string]string{"message": "Product attribute updated successfully"})
}

// DeleteProductAttribute soft deletes a product attribute
func (h *Handler) DeleteProductAttribute(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	attrID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid attribute ID")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE product_attributes SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3
	`, now, attrID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete product attribute")
		return
	}

	response.Success(c, map[string]string{"message": "Product attribute deleted successfully"})
}

// =====================================================
// ATTRIBUTE VALUES
// =====================================================

// CreateAttributeValue adds a value to an attribute
func (h *Handler) CreateAttributeValue(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	attrID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid attribute ID")
		return
	}

	var input struct {
		Name       string   `json:"name" binding:"required"`
		Code       *string  `json:"code"`
		HTMLColor  *string  `json:"html_color"`
		SortOrder  int      `json:"sort_order"`
		PriceExtra *float64 `json:"price_extra"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	valueID := uuid.New()

	// Default price_extra to 0 if not provided
	priceExtra := 0.0
	if input.PriceExtra != nil {
		priceExtra = *input.PriceExtra
	}

	_, err = h.db.Exec(`
		INSERT INTO product_attribute_values (id, tenant_id, attribute_id, name, code, html_color, sort_order, price_extra, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, $9, $9)
	`, valueID, tenantID, attrID, input.Name, input.Code, input.HTMLColor, input.SortOrder, priceExtra, now)
	if err != nil {
		h.log.Error("Failed to create attribute value", "error", err)
		response.InternalError(c, "Failed to create attribute value")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":      valueID,
		"message": "Attribute value created successfully",
	})
}

// DeleteAttributeValue soft deletes an attribute value
func (h *Handler) DeleteAttributeValue(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	valueID, err := uuid.Parse(c.Param("valueId"))
	if err != nil {
		response.BadRequest(c, "Invalid value ID")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE product_attribute_values SET deleted_at = $1
		WHERE id = $2 AND tenant_id = $3
	`, now, valueID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete attribute value")
		return
	}

	response.Success(c, map[string]string{"message": "Attribute value deleted successfully"})
}

// =====================================================
// PRODUCT VARIANTS
// =====================================================

type ProductVariant struct {
	ID                uuid.UUID              `json:"id"`
	TenantID          uuid.UUID              `json:"tenant_id"`
	ProductID         uuid.UUID              `json:"product_id"`
	ProductName       string                 `json:"product_name"`
	SKU               *string                `json:"sku"`
	Barcode           *string                `json:"barcode"`
	InternalReference *string                `json:"internal_reference"`
	CostPrice         *float64               `json:"cost_price"`
	ListPrice         *float64               `json:"list_price"`
	Weight            *float64               `json:"weight"`
	Volume            *float64               `json:"volume"`
	IsActive          bool                   `json:"is_active"`
	VariantName       string                 `json:"variant_name"`
	DisplayName       string                 `json:"display_name"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	Attributes        []VariantAttributeInfo `json:"attributes,omitempty"`
	StockQuantity     float64                `json:"stock_quantity"`
}

type VariantAttributeInfo struct {
	AttributeID   uuid.UUID `json:"attribute_id"`
	AttributeName string    `json:"attribute_name"`
	ValueID       uuid.UUID `json:"value_id"`
	ValueName     string    `json:"value_name"`
	HTMLColor     *string   `json:"html_color"`
}

// ListProductVariants returns all variants for a product or all variants
func (h *Handler) ListProductVariants(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	productID := c.Query("product_id")

	baseQuery := `
		SELECT pv.id, pv.tenant_id, pv.product_id, p.name as product_name,
			   pv.sku, pv.barcode, pv.internal_reference, pv.cost_price, pv.list_price,
			   pv.weight, pv.volume, pv.is_active, pv.variant_name, pv.display_name,
			   pv.created_at, pv.updated_at,
			   COALESCE((SELECT SUM(i.quantity_on_hand) FROM inventory i WHERE i.variant_id = pv.id), 0) as stock_qty
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE pv.tenant_id = $1 AND pv.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	whereExtra := ""

	// Filter by organization via products table (skip when filtering by specific product)
	if productID == "" {
		if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
			args = append(args, orgID)
			whereExtra += fmt.Sprintf(" AND p.origin_organization_id = $%d", len(args))
		}
	}

	orderBy := ""
	if productID != "" {
		args = append(args, productID)
		whereExtra += fmt.Sprintf(" AND pv.product_id = $%d", len(args))
		orderBy = " ORDER BY pv.variant_name, pv.id ASC"
	} else {
		orderBy = " ORDER BY p.name, pv.variant_name, pv.id ASC"
	}

	// Search server-side: the clients used to filter what they had already
	// loaded, so a paginated list only ever searched the rows on the current
	// page and the same query gave different answers on web and mobile.
	// Appended to whereExtra before filterArgCount is taken, so the count query
	// below counts the same filtered set.
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		args = append(args, "%"+search+"%")
		whereExtra += fmt.Sprintf(
			` AND (pv.display_name ILIKE $%d OR pv.sku ILIKE $%d OR pv.barcode ILIKE $%d)`,
			len(args), len(args), len(args))
	}

	baseQuery += whereExtra
	baseQuery += orderBy

	// Number of args that belong to the WHERE/filter clause (excludes LIMIT/OFFSET).
	filterArgCount := len(args)

	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		baseQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", filterArgCount+1, filterArgCount+2)
		args = append(args, pageSize, offset)
	}

	var rows *sql.Rows
	var err error
	rows, err = h.db.Query(baseQuery, args...)
	if err != nil {
		response.InternalError(c, "Failed to fetch product variants")
		return
	}
	defer rows.Close()

	var variants []ProductVariant
	for rows.Next() {
		var v ProductVariant
		err := rows.Scan(&v.ID, &v.TenantID, &v.ProductID, &v.ProductName,
			&v.SKU, &v.Barcode, &v.InternalReference, &v.CostPrice, &v.ListPrice,
			&v.Weight, &v.Volume, &v.IsActive, &v.VariantName, &v.DisplayName,
			&v.CreatedAt, &v.UpdatedAt, &v.StockQuantity)
		if err != nil {
			continue
		}

		// Fetch attribute values for this variant
		attrRows, err := h.db.Query(`
			SELECT pa.id, pa.name, pav.id, pav.name, pav.html_color
			FROM product_variant_attribute_values pvav
			JOIN product_template_attribute_values ptav ON ptav.id = pvav.template_attribute_value_id
			JOIN product_attribute_values pav ON pav.id = ptav.attribute_value_id
			JOIN product_template_attributes pta ON pta.id = ptav.product_template_attribute_id
			JOIN product_attributes pa ON pa.id = pta.attribute_id
			WHERE pvav.variant_id = $1
			ORDER BY pa.sort_order, pa.name
		`, v.ID)
		if err == nil {
			for attrRows.Next() {
				var attr VariantAttributeInfo
				if err := attrRows.Scan(&attr.AttributeID, &attr.AttributeName, &attr.ValueID, &attr.ValueName, &attr.HTMLColor); err == nil {
					v.Attributes = append(v.Attributes, attr)
				}
			}
			attrRows.Close()
		}

		variants = append(variants, v)
	}

	if !paginate {
		response.Success(c, variants)
		return
	}

	var total int
	countQuery := `
		SELECT COUNT(*)
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE pv.tenant_id = $1 AND pv.deleted_at IS NULL` + whereExtra
	_ = h.db.QueryRow(countQuery, args[:filterArgCount]...).Scan(&total)
	response.Paginated(c, variants, page, pageSize, total)
}

// GetProductVariantStats returns the counters behind the Variants tab cards.
// They were derived client-side from the variants already loaded, so against a
// paginated list "products with variants" only ever counted the first page —
// any tenant past 20 variants saw a number that was simply wrong.
func (h *Handler) GetProductVariantStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	query := `
		SELECT COUNT(*) AS total_variants,
		       COUNT(DISTINCT pv.product_id) AS products_with_variants
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE pv.tenant_id = $1 AND pv.deleted_at IS NULL AND p.deleted_at IS NULL
	`
	args := []interface{}{tenantID}

	// Same org scoping as ListProductVariants applies without product_id, so
	// the cards agree with the list they sit above.
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		args = append(args, orgID)
		query += fmt.Sprintf(" AND p.origin_organization_id = $%d", len(args))
	}

	var totalVariants, productsWithVariants int
	if err := h.db.QueryRow(query, args...).Scan(&totalVariants, &productsWithVariants); err != nil {
		h.log.Error("Failed to load product variant stats", "error", err)
		response.InternalError(c, "Failed to load product variant stats")
		return
	}

	// Attributes are tenant-wide, not org-scoped — matching ListProductAttributes.
	var totalAttributes int
	if err := h.db.QueryRow(`
		SELECT COUNT(*) FROM product_attributes
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`, tenantID).Scan(&totalAttributes); err != nil {
		h.log.Error("Failed to count product attributes", "error", err)
		response.InternalError(c, "Failed to load product variant stats")
		return
	}

	response.Success(c, map[string]interface{}{
		"total_variants":         totalVariants,
		"products_with_variants": productsWithVariants,
		"total_attributes":       totalAttributes,
	})
}

// GetProductVariant returns a single variant
func (h *Handler) GetProductVariant(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	variantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid variant ID")
		return
	}

	var v ProductVariant
	err = h.db.QueryRow(`
		SELECT pv.id, pv.tenant_id, pv.product_id, p.name as product_name,
			   pv.sku, pv.barcode, pv.internal_reference, pv.cost_price, pv.list_price,
			   pv.weight, pv.volume, pv.is_active, pv.variant_name, pv.display_name,
			   pv.created_at, pv.updated_at,
			   COALESCE((SELECT SUM(i.quantity_on_hand) FROM inventory i WHERE i.variant_id = pv.id), 0) as stock_qty
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE pv.id = $1 AND pv.tenant_id = $2 AND pv.deleted_at IS NULL
	`, variantID, tenantID).Scan(&v.ID, &v.TenantID, &v.ProductID, &v.ProductName,
		&v.SKU, &v.Barcode, &v.InternalReference, &v.CostPrice, &v.ListPrice,
		&v.Weight, &v.Volume, &v.IsActive, &v.VariantName, &v.DisplayName,
		&v.CreatedAt, &v.UpdatedAt, &v.StockQuantity)
	if err != nil {
		response.NotFound(c, "Variant")
		return
	}

	response.Success(c, v)
}

// refreshProductHasVariants recomputes products.has_variants from
// product_variants. Create and generate used to write a literal `true` and
// delete wrote nothing at all, so the flag drifted from the table in both
// directions — and AdjustInventory (inventory.go) reads it to decide whether a
// variant_id is required, which left products with no variants left
// un-adjustable. Deriving it from the table keeps all three call sites on one
// source of truth.
//
// Still outside the caller's transaction, so a failure here is logged rather
// than surfaced: the variant write itself has already succeeded and must not be
// reported as failed. Migration 492 repairs any flag that drifts this way.
func (h *Handler) refreshProductHasVariants(productID, tenantID uuid.UUID) {
	if _, err := h.db.Exec(`
		UPDATE products p SET has_variants = EXISTS (
			SELECT 1 FROM product_variants pv
			WHERE pv.product_id = p.id AND pv.deleted_at IS NULL
		)
		WHERE p.id = $1 AND p.tenant_id = $2
	`, productID, tenantID); err != nil {
		h.log.Error("Failed to refresh has_variants", "product_id", productID, "error", err)
	}
}

// CreateProductVariant creates a new product variant
func (h *Handler) CreateProductVariant(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var input struct {
		ProductID         string   `json:"product_id" binding:"required"`
		SKU               *string  `json:"sku"`
		Barcode           *string  `json:"barcode"`
		InternalReference *string  `json:"internal_reference"`
		CostPrice         *float64 `json:"cost_price"`
		ListPrice         *float64 `json:"list_price"`
		Weight            *float64 `json:"weight"`
		Volume            *float64 `json:"volume"`
		AttributeValueIDs []string `json:"attribute_value_ids"` // IDs of product_template_attribute_values
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Get product name
	var productName string
	err = h.db.QueryRow("SELECT name FROM products WHERE id = $1 AND tenant_id = $2", productID, tenantID).Scan(&productName)
	if err != nil {
		response.NotFound(c, "Product")
		return
	}

	// Build variant name from attribute values
	var valueNames []string
	for _, valIDStr := range input.AttributeValueIDs {
		valID, err := uuid.Parse(valIDStr)
		if err != nil {
			continue
		}
		var valName string
		err = h.db.QueryRow(`
			SELECT pav.name
			FROM product_template_attribute_values ptav
			JOIN product_attribute_values pav ON pav.id = ptav.attribute_value_id
			WHERE ptav.id = $1
		`, valID).Scan(&valName)
		if err == nil {
			valueNames = append(valueNames, valName)
		}
	}

	variantName := strings.Join(valueNames, " / ")
	displayName := productName
	if variantName != "" {
		displayName = fmt.Sprintf("%s (%s)", productName, variantName)
	}

	now := time.Now()
	variantID := uuid.New()

	_, err = h.db.Exec(`
		INSERT INTO product_variants (id, tenant_id, product_id, sku, barcode, internal_reference,
			cost_price, list_price, weight, volume, is_active, variant_name, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true, $11, $12, $13, $13)
	`, variantID, tenantID, productID, input.SKU, input.Barcode, input.InternalReference,
		input.CostPrice, input.ListPrice, input.Weight, input.Volume, variantName, displayName, now)
	if err != nil {
		h.log.Error("Failed to create product variant", "error", err)
		response.InternalError(c, "Failed to create product variant")
		return
	}

	// Link attribute values to variant
	for _, valIDStr := range input.AttributeValueIDs {
		valID, err := uuid.Parse(valIDStr)
		if err != nil {
			continue
		}
		if _, execErr := h.db.Exec(`
			INSERT INTO product_variant_attribute_values (id, variant_id, template_attribute_value_id, created_at)
			VALUES ($1, $2, $3, $4)
		`, uuid.New(), variantID, valID, now); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT product_variant_attribute_values", "error", execErr)
		}
	}

	h.refreshProductHasVariants(productID, tenantID)

	response.Success(c, map[string]interface{}{
		"id":           variantID,
		"variant_name": variantName,
		"display_name": displayName,
		"message":      "Product variant created successfully",
	})
}

// UpdateProductVariant updates a product variant
func (h *Handler) UpdateProductVariant(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	variantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid variant ID")
		return
	}

	var input struct {
		SKU               *string  `json:"sku"`
		Barcode           *string  `json:"barcode"`
		InternalReference *string  `json:"internal_reference"`
		CostPrice         *float64 `json:"cost_price"`
		ListPrice         *float64 `json:"list_price"`
		Weight            *float64 `json:"weight"`
		Volume            *float64 `json:"volume"`
		IsActive          *bool    `json:"is_active"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE product_variants
		SET sku = COALESCE($1, sku),
			barcode = COALESCE($2, barcode),
			internal_reference = COALESCE($3, internal_reference),
			cost_price = COALESCE($4, cost_price),
			list_price = COALESCE($5, list_price),
			weight = COALESCE($6, weight),
			volume = COALESCE($7, volume),
			is_active = COALESCE($8, is_active),
			updated_at = $9
		WHERE id = $10 AND tenant_id = $11 AND deleted_at IS NULL
	`, input.SKU, input.Barcode, input.InternalReference, input.CostPrice, input.ListPrice,
		input.Weight, input.Volume, input.IsActive, now, variantID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to update product variant")
		return
	}

	response.Success(c, map[string]string{"message": "Product variant updated successfully"})
}

// DeleteProductVariant soft deletes a product variant
func (h *Handler) DeleteProductVariant(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	variantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid variant ID")
		return
	}

	// RETURNING gives us the owning product without a second lookup, so the
	// flag can be recomputed below.
	now := time.Now()
	var productID uuid.UUID
	err = h.db.QueryRow(`
		UPDATE product_variants SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3
		RETURNING product_id
	`, now, variantID, tenantID).Scan(&productID)
	if err != nil && err != sql.ErrNoRows {
		response.InternalError(c, "Failed to delete product variant")
		return
	}

	// Deleting the last variant has to clear has_variants as well, otherwise
	// AdjustInventory (inventory.go) keeps demanding a variant_id for a product
	// that has none left to choose from. sql.ErrNoRows means the statement
	// matched nothing (unknown id, or another tenant's variant) — the response
	// stays a success as it always has, there is simply nothing to recompute.
	if err == nil {
		h.refreshProductHasVariants(productID, tenantID)
	}

	response.Success(c, map[string]string{"message": "Product variant deleted successfully"})
}

// =====================================================
// GENERATE VARIANTS
// =====================================================

// GenerateProductVariants auto-generates all variant combinations for a product
func (h *Handler) GenerateProductVariants(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var input struct {
		ProductID string `json:"product_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Get product info
	var productName string
	var baseCost, basePrice sql.NullFloat64
	err = h.db.QueryRow(`
		SELECT name, cost_price, list_price FROM products WHERE id = $1 AND tenant_id = $2
	`, productID, tenantID).Scan(&productName, &baseCost, &basePrice)
	if err != nil {
		response.NotFound(c, "Product")
		return
	}

	// Get all template attribute values for this product
	rows, err := h.db.Query(`
		SELECT ptav.id, pta.attribute_id, pa.name as attr_name, pav.name as val_name, COALESCE(ptav.price_extra, 0)
		FROM product_template_attribute_values ptav
		JOIN product_template_attributes pta ON pta.id = ptav.product_template_attribute_id
		JOIN product_attributes pa ON pa.id = pta.attribute_id
		JOIN product_attribute_values pav ON pav.id = ptav.attribute_value_id
		WHERE pta.product_id = $1 AND ptav.is_active = true
		ORDER BY pa.sort_order, pav.sort_order
	`, productID)
	if err != nil {
		response.InternalError(c, "Failed to fetch attribute values")
		return
	}
	defer rows.Close()

	// Group values by attribute
	type AttrValue struct {
		PTAV_ID    uuid.UUID
		AttrID     uuid.UUID
		AttrName   string
		ValName    string
		PriceExtra float64
	}

	attrValuesMap := make(map[uuid.UUID][]AttrValue)
	attrOrder := []uuid.UUID{}
	for rows.Next() {
		var av AttrValue
		if err := rows.Scan(&av.PTAV_ID, &av.AttrID, &av.AttrName, &av.ValName, &av.PriceExtra); err != nil {
			continue
		}
		if _, exists := attrValuesMap[av.AttrID]; !exists {
			attrOrder = append(attrOrder, av.AttrID)
		}
		attrValuesMap[av.AttrID] = append(attrValuesMap[av.AttrID], av)
	}

	if len(attrOrder) == 0 {
		response.BadRequest(c, "No attribute values configured for this product")
		return
	}

	// Generate all combinations
	var combinations [][]AttrValue
	combinations = append(combinations, []AttrValue{})

	for _, attrID := range attrOrder {
		values := attrValuesMap[attrID]
		var newCombinations [][]AttrValue
		for _, combo := range combinations {
			for _, val := range values {
				newCombo := make([]AttrValue, len(combo))
				copy(newCombo, combo)
				newCombo = append(newCombo, val)
				newCombinations = append(newCombinations, newCombo)
			}
		}
		combinations = newCombinations
	}

	// Create variants for each combination
	now := time.Now()
	createdCount := 0

	for _, combo := range combinations {
		var valueNames []string
		var ptavIDs []uuid.UUID
		var extraPrice float64

		for _, av := range combo {
			valueNames = append(valueNames, av.ValName)
			ptavIDs = append(ptavIDs, av.PTAV_ID)
			extraPrice += av.PriceExtra
		}

		variantName := strings.Join(valueNames, " / ")
		displayName := fmt.Sprintf("%s (%s)", productName, variantName)

		// Check if variant with same combination exists
		var existingID uuid.UUID
		err := h.db.QueryRow(`
			SELECT pv.id FROM product_variants pv
			WHERE pv.product_id = $1 AND pv.variant_name = $2 AND pv.deleted_at IS NULL
		`, productID, variantName).Scan(&existingID)
		if err == nil {
			// Already exists, skip
			continue
		}

		variantID := uuid.New()

		// Calculate prices
		var costPrice, listPrice *float64
		if baseCost.Valid {
			cost := baseCost.Float64
			costPrice = &cost
		}
		if basePrice.Valid {
			price := basePrice.Float64 + extraPrice
			listPrice = &price
		}

		_, err = h.db.Exec(`
			INSERT INTO product_variants (id, tenant_id, product_id, cost_price, list_price,
				is_active, variant_name, display_name, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, true, $6, $7, $8, $8)
		`, variantID, tenantID, productID, costPrice, listPrice, variantName, displayName, now)
		if err != nil {
			continue
		}

		// Link attribute values
		for _, ptavID := range ptavIDs {
			if _, execErr := h.db.Exec(`
				INSERT INTO product_variant_attribute_values (id, variant_id, template_attribute_value_id, created_at)
				VALUES ($1, $2, $3, $4)
			`, uuid.New(), variantID, ptavID, now); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "INSERT product_variant_attribute_values", "error", execErr)
			}
		}

		createdCount++
	}

	// Update product has_variants flag
	if createdCount > 0 {
		h.refreshProductHasVariants(productID, tenantID)
	}

	response.Success(c, map[string]interface{}{
		"created_count":      createdCount,
		"total_combinations": len(combinations),
		"message":            fmt.Sprintf("Generated %d variants", createdCount),
	})
}

// =====================================================
// PRODUCT TEMPLATE ATTRIBUTES (link attributes to products)
// =====================================================

// AddProductAttribute links an attribute to a product template
func (h *Handler) AddProductAttribute(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var input struct {
		ProductID   string   `json:"product_id" binding:"required"`
		AttributeID string   `json:"attribute_id" binding:"required"`
		ValueIDs    []string `json:"value_ids"` // attribute value IDs to enable
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	attributeID, err := uuid.Parse(input.AttributeID)
	if err != nil {
		response.BadRequest(c, "Invalid attribute ID")
		return
	}

	now := time.Now()
	ptaID := uuid.New()

	// Create product_template_attribute link
	_, err = h.db.Exec(`
		INSERT INTO product_template_attributes (id, tenant_id, product_id, attribute_id, sort_order, created_at)
		VALUES ($1, $2, $3, $4, 0, $5)
		ON CONFLICT (product_id, attribute_id) DO NOTHING
	`, ptaID, tenantID, productID, attributeID, now)
	if err != nil {
		h.log.Error("Failed to add attribute to product", "error", err)
		response.InternalError(c, "Failed to add attribute to product")
		return
	}

	// Get the actual PTA ID (might have existed)
	h.db.QueryRow(`
		SELECT id FROM product_template_attributes WHERE product_id = $1 AND attribute_id = $2
	`, productID, attributeID).Scan(&ptaID)

	// Add product_template_attribute_values with price_extra from attribute value
	for _, valIDStr := range input.ValueIDs {
		valID, err := uuid.Parse(valIDStr)
		if err != nil {
			continue
		}
		// Get default price_extra from the attribute value
		var priceExtra float64
		h.db.QueryRow(`SELECT COALESCE(price_extra, 0) FROM product_attribute_values WHERE id = $1`, valID).Scan(&priceExtra)

		if _, execErr := h.db.Exec(`
			INSERT INTO product_template_attribute_values (id, tenant_id, product_template_attribute_id, attribute_value_id, price_extra, is_active, created_at)
			VALUES ($1, $2, $3, $4, $5, true, $6)
			ON CONFLICT (product_template_attribute_id, attribute_value_id) DO NOTHING
		`, uuid.New(), tenantID, ptaID, valID, priceExtra, now); execErr != nil {

			h.log.Error("write failed (was silently discarded)", "stmt", "INSERT product_template_attribute_values", "error", execErr)

		}
	}

	response.Success(c, map[string]interface{}{
		"id":      ptaID,
		"message": "Attribute added to product successfully",
	})
}

// GetProductTemplateAttributes returns attributes configured for a product
func (h *Handler) GetProductTemplateAttributes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	productID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT pta.id, pa.id, pa.name, pa.display_type
		FROM product_template_attributes pta
		JOIN product_attributes pa ON pa.id = pta.attribute_id
		WHERE pta.product_id = $1 AND pta.tenant_id = $2 AND pa.deleted_at IS NULL
		ORDER BY pta.sort_order, pa.name
	`, productID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to fetch product attributes")
		return
	}
	defer rows.Close()

	type ValueInfo struct {
		PTAVID     uuid.UUID `json:"ptav_id"`
		ValueID    uuid.UUID `json:"value_id"`
		ValueName  string    `json:"value_name"`
		HTMLColor  *string   `json:"html_color"`
		PriceExtra float64   `json:"price_extra"`
		IsActive   bool      `json:"is_active"`
	}

	type ProductAttrResult struct {
		PTAID         uuid.UUID   `json:"pta_id"`
		AttributeID   uuid.UUID   `json:"attribute_id"`
		AttributeName string      `json:"attribute_name"`
		DisplayType   string      `json:"display_type"`
		Values        []ValueInfo `json:"values"`
	}

	var results []ProductAttrResult
	for rows.Next() {
		var r ProductAttrResult
		if err := rows.Scan(&r.PTAID, &r.AttributeID, &r.AttributeName, &r.DisplayType); err != nil {
			continue
		}

		// Get values for this attribute
		valRows, err := h.db.Query(`
			SELECT ptav.id, pav.id, pav.name, pav.html_color, ptav.price_extra, ptav.is_active
			FROM product_template_attribute_values ptav
			JOIN product_attribute_values pav ON pav.id = ptav.attribute_value_id
			WHERE ptav.product_template_attribute_id = $1 AND pav.deleted_at IS NULL
			ORDER BY pav.sort_order, pav.name
		`, r.PTAID)
		if err == nil {
			for valRows.Next() {
				var v ValueInfo
				if err := valRows.Scan(&v.PTAVID, &v.ValueID, &v.ValueName, &v.HTMLColor, &v.PriceExtra, &v.IsActive); err == nil {
					r.Values = append(r.Values, v)
				}
			}
			valRows.Close()
		}

		results = append(results, r)
	}

	response.Success(c, results)
}
