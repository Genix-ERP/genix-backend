package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ListTenderProducts lists products in the catalog with filtering
func (h *Handler) ListTenderProducts(c *gin.Context) {
	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	categoryID := c.Query("category_id")
	regionID := c.Query("region_id")
	search := c.Query("search")
	ordering := c.DefaultQuery("ordering", "-created_at")
	minPrice := c.Query("min_price")
	maxPrice := c.Query("max_price")

	pagination := entity.NewPagination(page, limit)

	// Count
	countQuery := `SELECT COUNT(*) FROM tender_products p WHERE p.deleted_at IS NULL AND p.is_active = true`
	args := []interface{}{}
	argIdx := 1

	if categoryID != "" {
		countQuery += fmt.Sprintf(" AND p.category_id = $%d", argIdx)
		args = append(args, categoryID)
		argIdx++
	}
	if search != "" {
		countQuery += fmt.Sprintf(" AND (p.name ILIKE $%d OR p.name_ru ILIKE $%d OR p.description ILIKE $%d)", argIdx, argIdx, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}
	if minPrice != "" {
		countQuery += fmt.Sprintf(" AND p.price >= $%d", argIdx)
		args = append(args, minPrice)
		argIdx++
	}
	if maxPrice != "" {
		countQuery += fmt.Sprintf(" AND p.price <= $%d", argIdx)
		args = append(args, maxPrice)
		argIdx++
	}
	if regionID != "" {
		countQuery += fmt.Sprintf(" AND $%d = ANY(p.delivery_regions)", argIdx)
		args = append(args, regionID)
		argIdx++
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)
	pagination.Calculate(total)

	// Main query
	query := `
		SELECT p.id, p.supplier_id, p.category_id, p.name, p.name_ru, p.description,
		       p.unit, p.price, p.wholesale_price, p.wholesale_min_qty, p.currency,
		       p.availability, p.delivery_days, p.images, p.is_active, p.view_count, p.created_at,
		       COALESCE(cp.company_name, '') as supplier_name,
		       COALESCE(cp.rating, 0) as supplier_rating,
		       COALESCE(cat.name, '') as category_name
		FROM tender_products p
		LEFT JOIN tender_company_profiles cp ON cp.user_id = p.supplier_id AND cp.deleted_at IS NULL
		LEFT JOIN tender_categories cat ON cat.id = p.category_id
		WHERE p.deleted_at IS NULL AND p.is_active = true
	`
	qArgs := []interface{}{}
	qIdx := 1

	if categoryID != "" {
		query += fmt.Sprintf(" AND p.category_id = $%d", qIdx)
		qArgs = append(qArgs, categoryID)
		qIdx++
	}
	if search != "" {
		query += fmt.Sprintf(" AND (p.name ILIKE $%d OR p.name_ru ILIKE $%d OR p.description ILIKE $%d)", qIdx, qIdx, qIdx)
		qArgs = append(qArgs, "%"+search+"%")
		qIdx++
	}
	if minPrice != "" {
		query += fmt.Sprintf(" AND p.price >= $%d", qIdx)
		qArgs = append(qArgs, minPrice)
		qIdx++
	}
	if maxPrice != "" {
		query += fmt.Sprintf(" AND p.price <= $%d", qIdx)
		qArgs = append(qArgs, maxPrice)
		qIdx++
	}
	if regionID != "" {
		query += fmt.Sprintf(" AND $%d = ANY(p.delivery_regions)", qIdx)
		qArgs = append(qArgs, regionID)
		qIdx++
	}

	switch ordering {
	case "price":
		query += " ORDER BY p.price ASC"
	case "-price":
		query += " ORDER BY p.price DESC"
	case "rating":
		query += " ORDER BY cp.rating DESC"
	case "created_at":
		query += " ORDER BY p.created_at ASC"
	default:
		query += " ORDER BY p.created_at DESC"
	}

	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", qIdx, qIdx+1)
	qArgs = append(qArgs, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, qArgs...)
	if err != nil {
		h.log.Error("Failed to list products", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var products []entity.TenderProductResponse
	for rows.Next() {
		var p entity.TenderProductResponse
		var catID sql.NullString
		var catName sql.NullString
		var images pq.StringArray

		err := rows.Scan(
			&p.ID, &p.SupplierID, &catID, &p.Name, &p.NameRu, &p.Description,
			&p.Unit, &p.Price, &p.WholesalePrice, &p.WholesaleMinQty, &p.Currency,
			&p.Availability, &p.DeliveryDays, &images, &p.IsActive, &p.ViewCount, &p.CreatedAt,
			&p.SupplierName, &p.SupplierRating, &catName,
		)
		if err != nil {
			h.log.Error("Failed to scan product", "error", err)
			continue
		}

		if catID.Valid {
			parsed, _ := uuid.Parse(catID.String)
			p.CategoryID = &parsed
		}
		if catName.Valid {
			p.CategoryName = catName.String
		}
		p.Images = []string(images)

		products = append(products, p)
	}

	response.SuccessWithPagination(c, products, pagination)
}

// GetTenderProduct retrieves a single product by ID
func (h *Handler) GetTenderProduct(c *gin.Context) {
	productID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	query := `
		SELECT p.id, p.supplier_id, p.category_id, p.name, p.name_ru, p.description,
		       p.unit, p.price, p.wholesale_price, p.wholesale_min_qty, p.currency,
		       p.availability, p.delivery_days, p.images, p.certificates, p.specs,
		       p.is_active, p.view_count, p.created_at,
		       COALESCE(cp.company_name, '') as supplier_name,
		       COALESCE(cp.rating, 0) as supplier_rating,
		       COALESCE(cat.name, '') as category_name
		FROM tender_products p
		LEFT JOIN tender_company_profiles cp ON cp.user_id = p.supplier_id AND cp.deleted_at IS NULL
		LEFT JOIN tender_categories cat ON cat.id = p.category_id
		WHERE p.id = $1 AND p.deleted_at IS NULL
	`

	var p entity.TenderProductResponse
	var catID, catName sql.NullString
	var images, certificates pq.StringArray
	var specsJSON sql.NullString

	err = h.db.QueryRow(query, productID).Scan(
		&p.ID, &p.SupplierID, &catID, &p.Name, &p.NameRu, &p.Description,
		&p.Unit, &p.Price, &p.WholesalePrice, &p.WholesaleMinQty, &p.Currency,
		&p.Availability, &p.DeliveryDays, &images, &certificates, &specsJSON,
		&p.IsActive, &p.ViewCount, &p.CreatedAt,
		&p.SupplierName, &p.SupplierRating, &catName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Product not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get product", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if catID.Valid {
		parsed, _ := uuid.Parse(catID.String)
		p.CategoryID = &parsed
	}
	if catName.Valid {
		p.CategoryName = catName.String
	}
	p.Images = []string(images)
	p.Certificates = []string(certificates)
	if specsJSON.Valid {
		json.Unmarshal([]byte(specsJSON.String), &p.Specs)
	}

	// Increment view count
	h.db.Exec(`UPDATE tender_products SET view_count = view_count + 1 WHERE id = $1`, productID)

	response.Success(c, p)
}

// CreateTenderProduct creates a new product (Supplier only)
func (h *Handler) CreateTenderProduct(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var input entity.TenderCreateProductInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	productID := uuid.New()

	specsJSON, _ := json.Marshal(input.Specs)
	if input.Specs == nil {
		specsJSON = []byte("{}")
	}

	imagesArr := pq.StringArray(input.Images)
	if input.Images == nil {
		imagesArr = pq.StringArray{}
	}
	certsArr := pq.StringArray(input.Certificates)
	if input.Certificates == nil {
		certsArr = pq.StringArray{}
	}
	regionsArr := make(pq.StringArray, len(input.DeliveryRegions))
	for i, r := range input.DeliveryRegions {
		regionsArr[i] = r.String()
	}

	_, err := h.db.Exec(`
		INSERT INTO tender_products (id, supplier_id, category_id, name, name_ru, description,
		                             unit, price, wholesale_price, wholesale_min_qty, currency,
		                             availability, delivery_days, delivery_regions, images, certificates, specs)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, productID, userID, input.CategoryID, input.Name, input.NameRu, input.Description,
		input.Unit, input.Price, input.WholesalePrice, input.WholesaleMinQty, input.Currency,
		input.Availability, input.DeliveryDays, regionsArr, imagesArr, certsArr, string(specsJSON))
	if err != nil {
		h.log.Error("Failed to create product", "error", err)
		response.InternalServerError(c, "Failed to create product")
		return
	}

	response.Created(c, map[string]interface{}{"id": productID})
}

// UpdateTenderProduct updates a product (owner only)
func (h *Handler) UpdateTenderProduct(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	productID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Check ownership
	var ownerID uuid.UUID
	err = h.db.QueryRow(`SELECT supplier_id FROM tender_products WHERE id = $1 AND deleted_at IS NULL`, productID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Product not found")
		return
	}
	if ownerID != userID {
		response.Forbidden(c, "Only the product owner can update it")
		return
	}

	var input entity.TenderUpdateProductInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build dynamic update
	query := "UPDATE tender_products SET updated_at = NOW()"
	args := []interface{}{}
	argIdx := 1

	if input.Name != "" {
		query += fmt.Sprintf(", name = $%d", argIdx)
		args = append(args, input.Name)
		argIdx++
	}
	if input.NameRu != "" {
		query += fmt.Sprintf(", name_ru = $%d", argIdx)
		args = append(args, input.NameRu)
		argIdx++
	}
	if input.Description != "" {
		query += fmt.Sprintf(", description = $%d", argIdx)
		args = append(args, input.Description)
		argIdx++
	}
	if input.Unit != "" {
		query += fmt.Sprintf(", unit = $%d", argIdx)
		args = append(args, input.Unit)
		argIdx++
	}
	if input.Price > 0 {
		query += fmt.Sprintf(", price = $%d", argIdx)
		args = append(args, input.Price)
		argIdx++
	}
	if input.WholesalePrice > 0 {
		query += fmt.Sprintf(", wholesale_price = $%d", argIdx)
		args = append(args, input.WholesalePrice)
		argIdx++
	}
	if input.Currency != "" {
		query += fmt.Sprintf(", currency = $%d", argIdx)
		args = append(args, input.Currency)
		argIdx++
	}
	if input.Availability != "" {
		query += fmt.Sprintf(", availability = $%d", argIdx)
		args = append(args, input.Availability)
		argIdx++
	}
	if input.DeliveryDays > 0 {
		query += fmt.Sprintf(", delivery_days = $%d", argIdx)
		args = append(args, input.DeliveryDays)
		argIdx++
	}
	if input.CategoryID != nil {
		query += fmt.Sprintf(", category_id = $%d", argIdx)
		args = append(args, input.CategoryID)
		argIdx++
	}
	if input.Images != nil {
		query += fmt.Sprintf(", images = $%d", argIdx)
		args = append(args, pq.StringArray(input.Images))
		argIdx++
	}
	if input.Certificates != nil {
		query += fmt.Sprintf(", certificates = $%d", argIdx)
		args = append(args, pq.StringArray(input.Certificates))
		argIdx++
	}
	if input.Specs != nil {
		specsJSON, _ := json.Marshal(input.Specs)
		query += fmt.Sprintf(", specs = $%d", argIdx)
		args = append(args, string(specsJSON))
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d AND deleted_at IS NULL", argIdx)
	args = append(args, productID)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update product", "error", err)
		response.InternalServerError(c, "Failed to update product")
		return
	}

	response.Success(c, map[string]interface{}{"id": productID, "message": "Product updated"})
}

// DeleteTenderProduct soft-deletes a product (owner only)
func (h *Handler) DeleteTenderProduct(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	productID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	var ownerID uuid.UUID
	err = h.db.QueryRow(`SELECT supplier_id FROM tender_products WHERE id = $1 AND deleted_at IS NULL`, productID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Product not found")
		return
	}
	if ownerID != userID {
		response.Forbidden(c, "Only the product owner can delete it")
		return
	}

	_, err = h.db.Exec(`UPDATE tender_products SET deleted_at = NOW() WHERE id = $1`, productID)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}

	response.NoContent(c)
}

// GetMyTenderProducts lists products owned by the current supplier
func (h *Handler) GetMyTenderProducts(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)

	pagination := entity.NewPagination(page, limit)

	var total int
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_products WHERE supplier_id = $1 AND deleted_at IS NULL`, userID).Scan(&total)
	pagination.Calculate(total)

	rows, err := h.db.Query(`
		SELECT p.id, p.supplier_id, p.category_id, p.name, p.name_ru, p.description,
		       p.unit, p.price, p.wholesale_price, p.wholesale_min_qty, p.currency,
		       p.availability, p.delivery_days, p.images, p.is_active, p.view_count, p.created_at,
		       COALESCE(cat.name, '') as category_name
		FROM tender_products p
		LEFT JOIN tender_categories cat ON cat.id = p.category_id
		WHERE p.supplier_id = $1 AND p.deleted_at IS NULL
		ORDER BY p.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, pagination.Limit, pagination.Offset())
	if err != nil {
		h.log.Error("Failed to list my products", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var products []entity.TenderProductResponse
	for rows.Next() {
		var p entity.TenderProductResponse
		var catID, catName sql.NullString
		var images pq.StringArray

		err := rows.Scan(
			&p.ID, &p.SupplierID, &catID, &p.Name, &p.NameRu, &p.Description,
			&p.Unit, &p.Price, &p.WholesalePrice, &p.WholesaleMinQty, &p.Currency,
			&p.Availability, &p.DeliveryDays, &images, &p.IsActive, &p.ViewCount, &p.CreatedAt,
			&catName,
		)
		if err != nil {
			continue
		}

		if catID.Valid {
			parsed, _ := uuid.Parse(catID.String)
			p.CategoryID = &parsed
		}
		if catName.Valid {
			p.CategoryName = catName.String
		}
		p.Images = []string(images)

		products = append(products, p)
	}

	response.SuccessWithPagination(c, products, pagination)
}

// ListTenderCategories returns the category tree
func (h *Handler) ListTenderCategories(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT id, parent_id, name, name_ru, slug, icon, level
		FROM tender_categories
		WHERE deleted_at IS NULL AND is_active = true
		ORDER BY sort_order ASC, name ASC
	`)
	if err != nil {
		h.log.Error("Failed to list categories", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var allCategories []entity.TenderCategoryResponse
	for rows.Next() {
		var cat entity.TenderCategoryResponse
		var parentID sql.NullString

		err := rows.Scan(&cat.ID, &parentID, &cat.Name, &cat.NameRu, &cat.Slug, &cat.Icon, &cat.Level)
		if err != nil {
			continue
		}
		if parentID.Valid {
			parsed, _ := uuid.Parse(parentID.String)
			cat.ParentID = &parsed
		}
		allCategories = append(allCategories, cat)
	}

	// Build tree structure
	tree := buildCategoryTree(allCategories, nil)
	response.Success(c, tree)
}

// buildCategoryTree recursively builds category hierarchy
func buildCategoryTree(categories []entity.TenderCategoryResponse, parentID *uuid.UUID) []entity.TenderCategoryResponse {
	var result []entity.TenderCategoryResponse
	for _, cat := range categories {
		isMatch := false
		if parentID == nil && cat.ParentID == nil {
			isMatch = true
		} else if parentID != nil && cat.ParentID != nil && *parentID == *cat.ParentID {
			isMatch = true
		}

		if isMatch {
			children := buildCategoryTree(categories, &cat.ID)
			if len(children) > 0 {
				cat.Children = children
			}
			result = append(result, cat)
		}
	}
	return result
}
