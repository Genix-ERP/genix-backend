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
// PRODUCT PACKAGING HANDLERS
// =====================================================

// ListProductPackagings returns a list of product packagings
func (h *Handler) ListProductPackagings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Parse filters
	productID := c.Query("product_id")
	search := c.Query("search")
	salesOnly := c.Query("sales") == "true"
	purchaseOnly := c.Query("purchase") == "true"

	// Build query
	baseQuery := `
		SELECT pp.id, pp.tenant_id, pp.product_id, pp.name, pp.qty, pp.barcode,
			   pp.sales, pp.purchase, pp.is_active, pp.created_at, pp.updated_at,
			   p.code as product_code, p.name as product_name
		FROM product_packagings pp
		JOIN products p ON pp.product_id = p.id
		WHERE pp.tenant_id = $1 AND pp.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM product_packagings pp WHERE pp.tenant_id = $1 AND pp.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if productID != "" {
		argCount++
		productFilter := fmt.Sprintf(" AND pp.product_id = $%d", argCount)
		baseQuery += productFilter
		countQuery += productFilter
		args = append(args, productID)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (pp.name ILIKE $%d OR pp.barcode ILIKE $%d OR p.name ILIKE $%d)", argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	if salesOnly {
		baseQuery += " AND pp.sales = true"
		countQuery += " AND pp.sales = true"
	}

	if purchaseOnly {
		baseQuery += " AND pp.purchase = true"
		countQuery += " AND pp.purchase = true"
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count product packagings", "error", err)
		response.InternalError(c, "Failed to list product packagings")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY p.name ASC, pp.qty ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list product packagings", "error", err)
		response.InternalError(c, "Failed to list product packagings")
		return
	}
	defer rows.Close()

	packagings := make([]entity.ProductPackagingResponse, 0)
	for rows.Next() {
		var pp entity.ProductPackaging
		var barcode sql.NullString
		var productCode, productName string

		err := rows.Scan(
			&pp.ID, &pp.TenantID, &pp.ProductID, &pp.Name, &pp.Qty, &barcode,
			&pp.Sales, &pp.Purchase, &pp.IsActive, &pp.CreatedAt, &pp.UpdatedAt,
			&productCode, &productName,
		)
		if err != nil {
			h.log.Error("Failed to scan product packaging", "error", err)
			continue
		}

		resp := entity.ProductPackagingResponse{
			ID:          pp.ID,
			ProductID:   pp.ProductID,
			ProductCode: productCode,
			ProductName: productName,
			Name:        pp.Name,
			Qty:         pp.Qty,
			Sales:       pp.Sales,
			Purchase:    pp.Purchase,
			IsActive:    pp.IsActive,
			CreatedAt:   pp.CreatedAt,
		}
		if barcode.Valid {
			resp.Barcode = &barcode.String
		}

		packagings = append(packagings, resp)
	}

	response.Paginated(c, packagings, page, limit, total)
}

// ListProductPackagingsByProduct returns packagings for a specific product
func (h *Handler) ListProductPackagingsByProduct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	productID := c.Param("id")
	if productID == "" {
		response.BadRequest(c, "Product ID is required")
		return
	}

	query := `
		SELECT id, tenant_id, product_id, name, qty, barcode, sales, purchase, is_active, created_at, updated_at
		FROM product_packagings
		WHERE tenant_id = $1 AND product_id = $2 AND deleted_at IS NULL
		ORDER BY qty ASC
	`

	rows, err := h.db.Query(query, tenantID, productID)
	if err != nil {
		h.log.Error("Failed to list product packagings", "error", err)
		response.InternalError(c, "Failed to list product packagings")
		return
	}
	defer rows.Close()

	packagings := make([]entity.ProductPackagingResponse, 0)
	for rows.Next() {
		var pp entity.ProductPackaging
		var barcode sql.NullString

		err := rows.Scan(
			&pp.ID, &pp.TenantID, &pp.ProductID, &pp.Name, &pp.Qty, &barcode,
			&pp.Sales, &pp.Purchase, &pp.IsActive, &pp.CreatedAt, &pp.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan product packaging", "error", err)
			continue
		}

		resp := entity.ProductPackagingResponse{
			ID:        pp.ID,
			ProductID: pp.ProductID,
			Name:      pp.Name,
			Qty:       pp.Qty,
			Sales:     pp.Sales,
			Purchase:  pp.Purchase,
			IsActive:  pp.IsActive,
			CreatedAt: pp.CreatedAt,
		}
		if barcode.Valid {
			resp.Barcode = &barcode.String
		}

		packagings = append(packagings, resp)
	}

	response.Success(c, packagings)
}

// CreateProductPackaging creates a new product packaging
func (h *Handler) CreateProductPackaging(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateProductPackagingInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse product ID
	productUUID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Verify product exists
	var productExists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)", productUUID, tenantID).Scan(&productExists)
	if err != nil || !productExists {
		response.NotFound(c, "Product not found")
		return
	}

	// Set defaults
	sales := true
	purchase := true
	if input.Sales != nil {
		sales = *input.Sales
	}
	if input.Purchase != nil {
		purchase = *input.Purchase
	}

	// Insert packaging
	query := `
		INSERT INTO product_packagings (tenant_id, product_id, name, qty, barcode, sales, purchase, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true, NOW(), NOW())
		RETURNING id, created_at
	`

	var barcode *string
	if input.Barcode != "" {
		barcode = &input.Barcode
	}

	var id uuid.UUID
	var createdAt time.Time
	err = h.db.QueryRow(query, tenantID, productUUID, input.Name, input.Qty, barcode, sales, purchase).Scan(&id, &createdAt)
	if err != nil {
		h.log.Error("Failed to create product packaging", "error", err)
		response.InternalError(c, "Failed to create product packaging")
		return
	}

	resp := entity.ProductPackagingResponse{
		ID:        id,
		ProductID: productUUID,
		Name:      input.Name,
		Qty:       input.Qty,
		Barcode:   barcode,
		Sales:     sales,
		Purchase:  purchase,
		IsActive:  true,
		CreatedAt: createdAt,
	}

	response.Created(c, resp)
}

// CreateProductPackagingForProduct creates packaging for a specific product
func (h *Handler) CreateProductPackagingForProduct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	productID := c.Param("id")
	if productID == "" {
		response.BadRequest(c, "Product ID is required")
		return
	}

	var input entity.CreateProductPackagingInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Override product ID from URL
	input.ProductID = productID

	// Parse product ID
	productUUID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Verify product exists
	var productExists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM products WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)", productUUID, tenantID).Scan(&productExists)
	if err != nil || !productExists {
		response.NotFound(c, "Product not found")
		return
	}

	// Set defaults
	sales := true
	purchase := true
	if input.Sales != nil {
		sales = *input.Sales
	}
	if input.Purchase != nil {
		purchase = *input.Purchase
	}

	// Insert packaging
	query := `
		INSERT INTO product_packagings (tenant_id, product_id, name, qty, barcode, sales, purchase, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true, NOW(), NOW())
		RETURNING id, created_at
	`

	var barcode *string
	if input.Barcode != "" {
		barcode = &input.Barcode
	}

	var id uuid.UUID
	var createdAt time.Time
	err = h.db.QueryRow(query, tenantID, productUUID, input.Name, input.Qty, barcode, sales, purchase).Scan(&id, &createdAt)
	if err != nil {
		h.log.Error("Failed to create product packaging", "error", err)
		response.InternalError(c, "Failed to create product packaging")
		return
	}

	resp := entity.ProductPackagingResponse{
		ID:        id,
		ProductID: productUUID,
		Name:      input.Name,
		Qty:       input.Qty,
		Barcode:   barcode,
		Sales:     sales,
		Purchase:  purchase,
		IsActive:  true,
		CreatedAt: createdAt,
	}

	response.Created(c, resp)
}

// GetProductPackaging returns a single product packaging
func (h *Handler) GetProductPackaging(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Packaging ID is required")
		return
	}

	query := `
		SELECT pp.id, pp.tenant_id, pp.product_id, pp.name, pp.qty, pp.barcode,
			   pp.sales, pp.purchase, pp.is_active, pp.created_at, pp.updated_at,
			   p.code as product_code, p.name as product_name
		FROM product_packagings pp
		JOIN products p ON pp.product_id = p.id
		WHERE pp.id = $1 AND pp.tenant_id = $2 AND pp.deleted_at IS NULL
	`

	var pp entity.ProductPackaging
	var barcode sql.NullString
	var productCode, productName string

	err := h.db.QueryRow(query, id, tenantID).Scan(
		&pp.ID, &pp.TenantID, &pp.ProductID, &pp.Name, &pp.Qty, &barcode,
		&pp.Sales, &pp.Purchase, &pp.IsActive, &pp.CreatedAt, &pp.UpdatedAt,
		&productCode, &productName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Product packaging not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get product packaging", "error", err)
		response.InternalError(c, "Failed to get product packaging")
		return
	}

	resp := entity.ProductPackagingResponse{
		ID:          pp.ID,
		ProductID:   pp.ProductID,
		ProductCode: productCode,
		ProductName: productName,
		Name:        pp.Name,
		Qty:         pp.Qty,
		Sales:       pp.Sales,
		Purchase:    pp.Purchase,
		IsActive:    pp.IsActive,
		CreatedAt:   pp.CreatedAt,
	}
	if barcode.Valid {
		resp.Barcode = &barcode.String
	}

	response.Success(c, resp)
}

// UpdateProductPackaging updates a product packaging
func (h *Handler) UpdateProductPackaging(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Packaging ID is required")
		return
	}

	var input entity.UpdateProductPackagingInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.Qty != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("qty = $%d", argCount))
		args = append(args, *input.Qty)
	}
	if input.Barcode != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("barcode = $%d", argCount))
		args = append(args, *input.Barcode)
	}
	if input.Sales != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("sales = $%d", argCount))
		args = append(args, *input.Sales)
	}
	if input.Purchase != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("purchase = $%d", argCount))
		args = append(args, *input.Purchase)
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
	updates = append(updates, "updated_at = NOW()")

	// Add WHERE clause args
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE product_packagings
		SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, joinStrings(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err := h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Product packaging not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to update product packaging", "error", err)
		response.InternalError(c, "Failed to update product packaging")
		return
	}

	response.Success(c, map[string]interface{}{"id": returnedID, "message": "Product packaging updated successfully"})
}

// DeleteProductPackaging soft deletes a product packaging
func (h *Handler) DeleteProductPackaging(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Packaging ID is required")
		return
	}

	query := `UPDATE product_packagings SET deleted_at = NOW() WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`
	result, err := h.db.Exec(query, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete product packaging", "error", err)
		response.InternalError(c, "Failed to delete product packaging")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Product packaging not found")
		return
	}

	response.Success(c, map[string]string{"message": "Product packaging deleted successfully"})
}

// =====================================================
// PACKAGE TYPE HANDLERS
// =====================================================

// ListPackageTypes returns a list of package types
func (h *Handler) ListPackageTypes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	packageUse := c.Query("package_use")

	// Build query
	baseQuery := `
		SELECT pt.id, pt.tenant_id, pt.name, pt.code, pt.length_mm, pt.width_mm, pt.height_mm,
			   pt.max_weight, pt.barcode, pt.package_use, pt.carrier_id, pt.is_active,
			   pt.created_at, pt.updated_at
		FROM package_types pt
		WHERE pt.tenant_id = $1 AND pt.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM package_types pt WHERE pt.tenant_id = $1 AND pt.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (pt.name ILIKE $%d OR pt.code ILIKE $%d)", argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	if packageUse != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pt.package_use = $%d", argCount)
		countQuery += fmt.Sprintf(" AND pt.package_use = $%d", argCount)
		args = append(args, packageUse)
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count package types", "error", err)
		response.InternalError(c, "Failed to list package types")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY pt.name ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list package types", "error", err)
		response.InternalError(c, "Failed to list package types")
		return
	}
	defer rows.Close()

	packageTypes := make([]entity.PackageTypeResponse, 0)
	for rows.Next() {
		var pt entity.PackageType
		var code, barcode sql.NullString
		var lengthMM, widthMM, heightMM sql.NullInt64
		var maxWeight sql.NullFloat64
		var carrierID sql.NullString

		err := rows.Scan(
			&pt.ID, &pt.TenantID, &pt.Name, &code, &lengthMM, &widthMM, &heightMM,
			&maxWeight, &barcode, &pt.PackageUse, &carrierID, &pt.IsActive,
			&pt.CreatedAt, &pt.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan package type", "error", err)
			continue
		}

		resp := entity.PackageTypeResponse{
			ID:         pt.ID,
			Name:       pt.Name,
			PackageUse: string(pt.PackageUse),
			IsActive:   pt.IsActive,
			CreatedAt:  pt.CreatedAt,
		}

		if code.Valid {
			resp.Code = &code.String
		}
		if barcode.Valid {
			resp.Barcode = &barcode.String
		}
		if lengthMM.Valid {
			l := int(lengthMM.Int64)
			resp.LengthMM = &l
		}
		if widthMM.Valid {
			w := int(widthMM.Int64)
			resp.WidthMM = &w
		}
		if heightMM.Valid {
			h := int(heightMM.Int64)
			resp.HeightMM = &h
		}
		if maxWeight.Valid {
			resp.MaxWeight = &maxWeight.Float64
		}
		if carrierID.Valid {
			cid, _ := uuid.Parse(carrierID.String)
			resp.CarrierID = &cid
		}

		// Format dimensions
		if lengthMM.Valid && widthMM.Valid && heightMM.Valid {
			resp.Dimensions = fmt.Sprintf("%d×%d×%d mm", lengthMM.Int64, widthMM.Int64, heightMM.Int64)
		}

		packageTypes = append(packageTypes, resp)
	}

	response.Paginated(c, packageTypes, page, limit, total)
}

// CreatePackageType creates a new package type
func (h *Handler) CreatePackageType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreatePackageTypeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Set defaults
	packageUse := "disposable"
	if input.PackageUse != "" {
		packageUse = input.PackageUse
	}

	query := `
		INSERT INTO package_types (tenant_id, name, code, length_mm, width_mm, height_mm, max_weight, barcode, package_use, carrier_id, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true, NOW(), NOW())
		RETURNING id, created_at
	`

	var code, barcode, carrierID *string
	var lengthMM, widthMM, heightMM *int
	var maxWeight *float64

	if input.Code != "" {
		code = &input.Code
	}
	if input.Barcode != "" {
		barcode = &input.Barcode
	}
	if input.CarrierID != "" {
		carrierID = &input.CarrierID
	}
	if input.LengthMM > 0 {
		lengthMM = &input.LengthMM
	}
	if input.WidthMM > 0 {
		widthMM = &input.WidthMM
	}
	if input.HeightMM > 0 {
		heightMM = &input.HeightMM
	}
	if input.MaxWeight > 0 {
		maxWeight = &input.MaxWeight
	}

	var id uuid.UUID
	var createdAt time.Time
	err := h.db.QueryRow(query, tenantID, input.Name, code, lengthMM, widthMM, heightMM, maxWeight, barcode, packageUse, carrierID).Scan(&id, &createdAt)
	if err != nil {
		h.log.Error("Failed to create package type", "error", err)
		response.InternalError(c, "Failed to create package type")
		return
	}

	resp := entity.PackageTypeResponse{
		ID:         id,
		Name:       input.Name,
		Code:       code,
		LengthMM:   lengthMM,
		WidthMM:    widthMM,
		HeightMM:   heightMM,
		MaxWeight:  maxWeight,
		Barcode:    barcode,
		PackageUse: packageUse,
		IsActive:   true,
		CreatedAt:  createdAt,
	}

	response.Created(c, resp)
}

// GetPackageType returns a single package type
func (h *Handler) GetPackageType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Package type ID is required")
		return
	}

	query := `
		SELECT id, tenant_id, name, code, length_mm, width_mm, height_mm,
			   max_weight, barcode, package_use, carrier_id, is_active, created_at, updated_at
		FROM package_types
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var pt entity.PackageType
	var code, barcode sql.NullString
	var lengthMM, widthMM, heightMM sql.NullInt64
	var maxWeight sql.NullFloat64
	var carrierID sql.NullString

	err := h.db.QueryRow(query, id, tenantID).Scan(
		&pt.ID, &pt.TenantID, &pt.Name, &code, &lengthMM, &widthMM, &heightMM,
		&maxWeight, &barcode, &pt.PackageUse, &carrierID, &pt.IsActive, &pt.CreatedAt, &pt.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Package type not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get package type", "error", err)
		response.InternalError(c, "Failed to get package type")
		return
	}

	resp := entity.PackageTypeResponse{
		ID:         pt.ID,
		Name:       pt.Name,
		PackageUse: string(pt.PackageUse),
		IsActive:   pt.IsActive,
		CreatedAt:  pt.CreatedAt,
	}

	if code.Valid {
		resp.Code = &code.String
	}
	if barcode.Valid {
		resp.Barcode = &barcode.String
	}
	if lengthMM.Valid {
		l := int(lengthMM.Int64)
		resp.LengthMM = &l
	}
	if widthMM.Valid {
		w := int(widthMM.Int64)
		resp.WidthMM = &w
	}
	if heightMM.Valid {
		h := int(heightMM.Int64)
		resp.HeightMM = &h
	}
	if maxWeight.Valid {
		resp.MaxWeight = &maxWeight.Float64
	}
	if carrierID.Valid {
		cid, _ := uuid.Parse(carrierID.String)
		resp.CarrierID = &cid
	}

	response.Success(c, resp)
}

// UpdatePackageType updates a package type
func (h *Handler) UpdatePackageType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Package type ID is required")
		return
	}

	var input entity.UpdatePackageTypeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.Code != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("code = $%d", argCount))
		args = append(args, *input.Code)
	}
	if input.LengthMM != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("length_mm = $%d", argCount))
		args = append(args, *input.LengthMM)
	}
	if input.WidthMM != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("width_mm = $%d", argCount))
		args = append(args, *input.WidthMM)
	}
	if input.HeightMM != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("height_mm = $%d", argCount))
		args = append(args, *input.HeightMM)
	}
	if input.MaxWeight != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("max_weight = $%d", argCount))
		args = append(args, *input.MaxWeight)
	}
	if input.Barcode != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("barcode = $%d", argCount))
		args = append(args, *input.Barcode)
	}
	if input.PackageUse != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("package_use = $%d", argCount))
		args = append(args, *input.PackageUse)
	}
	if input.CarrierID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("carrier_id = $%d", argCount))
		args = append(args, *input.CarrierID)
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

	updates = append(updates, "updated_at = NOW()")

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE package_types
		SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, joinStrings(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err := h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Package type not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to update package type", "error", err)
		response.InternalError(c, "Failed to update package type")
		return
	}

	response.Success(c, map[string]interface{}{"id": returnedID, "message": "Package type updated successfully"})
}

// DeletePackageType soft deletes a package type
func (h *Handler) DeletePackageType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Package type ID is required")
		return
	}

	query := `UPDATE package_types SET deleted_at = NOW() WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`
	result, err := h.db.Exec(query, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete package type", "error", err)
		response.InternalError(c, "Failed to delete package type")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Package type not found")
		return
	}

	response.Success(c, map[string]string{"message": "Package type deleted successfully"})
}

// =====================================================
// PACKAGE HANDLERS
// =====================================================

// ListPackages returns a list of packages
func (h *Handler) ListPackages(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	packageTypeID := c.Query("package_type_id")
	locationID := c.Query("location_id")

	// Build query
	baseQuery := `
		SELECT p.id, p.tenant_id, p.name, p.package_type_id, p.shipping_weight,
			   p.location_id, p.picking_id, p.pack_date, p.is_active, p.created_at, p.updated_at,
			   pt.name as package_type_name,
			   wl.name as location_name,
			   (SELECT COUNT(*) FROM package_contents pc WHERE pc.package_id = p.id) as item_count
		FROM packages p
		LEFT JOIN package_types pt ON p.package_type_id = pt.id
		LEFT JOIN warehouse_locations wl ON p.location_id = wl.id
		WHERE p.tenant_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM packages p WHERE p.tenant_id = $1`

	args := []interface{}{tenantID}
	argCount := 1

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND p.name ILIKE $%d", argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	if packageTypeID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.package_type_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.package_type_id = $%d", argCount)
		args = append(args, packageTypeID)
	}

	if locationID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.location_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.location_id = $%d", argCount)
		args = append(args, locationID)
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count packages", "error", err)
		response.InternalError(c, "Failed to list packages")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY p.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list packages", "error", err)
		response.InternalError(c, "Failed to list packages")
		return
	}
	defer rows.Close()

	packages := make([]entity.PackageResponse, 0)
	for rows.Next() {
		var pkg entity.Package
		var packageTypeID, locationID, pickingID sql.NullString
		var shippingWeight sql.NullFloat64
		var packageTypeName, locationName sql.NullString
		var itemCount int

		err := rows.Scan(
			&pkg.ID, &pkg.TenantID, &pkg.Name, &packageTypeID, &shippingWeight,
			&locationID, &pickingID, &pkg.PackDate, &pkg.IsActive, &pkg.CreatedAt, &pkg.UpdatedAt,
			&packageTypeName, &locationName, &itemCount,
		)
		if err != nil {
			h.log.Error("Failed to scan package", "error", err)
			continue
		}

		resp := entity.PackageResponse{
			ID:        pkg.ID,
			Name:      pkg.Name,
			PackDate:  pkg.PackDate,
			IsActive:  pkg.IsActive,
			ItemCount: itemCount,
			CreatedAt: pkg.CreatedAt,
		}

		if packageTypeID.Valid {
			ptid, _ := uuid.Parse(packageTypeID.String)
			resp.PackageTypeID = &ptid
		}
		if packageTypeName.Valid {
			resp.PackageTypeName = packageTypeName.String
		}
		if shippingWeight.Valid {
			resp.ShippingWeight = &shippingWeight.Float64
		}
		if locationID.Valid {
			lid, _ := uuid.Parse(locationID.String)
			resp.LocationID = &lid
		}
		if locationName.Valid {
			resp.LocationName = locationName.String
		}

		packages = append(packages, resp)
	}

	response.Paginated(c, packages, page, limit, total)
}

// CreatePackage creates a new package
func (h *Handler) CreatePackage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreatePackageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		// Allow empty body - create package with just auto-generated name
	}

	// Generate package name
	var packageName string
	err := h.db.QueryRow("SELECT generate_package_name($1)", tenantID).Scan(&packageName)
	if err != nil {
		// Fallback to timestamp-based name
		packageName = fmt.Sprintf("PACK%d", time.Now().Unix())
	}

	query := `
		INSERT INTO packages (tenant_id, name, package_type_id, location_id, shipping_weight, pack_date, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), true, NOW(), NOW())
		RETURNING id, pack_date, created_at
	`

	var packageTypeID, locationID *string
	if input.PackageTypeID != "" {
		packageTypeID = &input.PackageTypeID
	}
	if input.LocationID != "" {
		locationID = &input.LocationID
	}

	var id uuid.UUID
	var packDate, createdAt time.Time
	err = h.db.QueryRow(query, tenantID, packageName, packageTypeID, locationID, input.ShippingWeight).Scan(&id, &packDate, &createdAt)
	if err != nil {
		h.log.Error("Failed to create package", "error", err)
		response.InternalError(c, "Failed to create package")
		return
	}

	resp := entity.PackageResponse{
		ID:             id,
		Name:           packageName,
		ShippingWeight: input.ShippingWeight,
		PackDate:       packDate,
		IsActive:       true,
		ItemCount:      0,
		CreatedAt:      createdAt,
	}

	if packageTypeID != nil {
		ptid, _ := uuid.Parse(*packageTypeID)
		resp.PackageTypeID = &ptid
	}
	if locationID != nil {
		lid, _ := uuid.Parse(*locationID)
		resp.LocationID = &lid
	}

	response.Created(c, resp)
}

// GetPackage returns a single package with its contents
func (h *Handler) GetPackage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Package ID is required")
		return
	}

	// Get package
	query := `
		SELECT p.id, p.tenant_id, p.name, p.package_type_id, p.shipping_weight,
			   p.location_id, p.picking_id, p.pack_date, p.is_active, p.created_at, p.updated_at,
			   pt.name as package_type_name,
			   wl.name as location_name
		FROM packages p
		LEFT JOIN package_types pt ON p.package_type_id = pt.id
		LEFT JOIN warehouse_locations wl ON p.location_id = wl.id
		WHERE p.id = $1 AND p.tenant_id = $2
	`

	var pkg entity.Package
	var packageTypeID, locationID, pickingID sql.NullString
	var shippingWeight sql.NullFloat64
	var packageTypeName, locationName sql.NullString

	err := h.db.QueryRow(query, id, tenantID).Scan(
		&pkg.ID, &pkg.TenantID, &pkg.Name, &packageTypeID, &shippingWeight,
		&locationID, &pickingID, &pkg.PackDate, &pkg.IsActive, &pkg.CreatedAt, &pkg.UpdatedAt,
		&packageTypeName, &locationName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Package not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get package", "error", err)
		response.InternalError(c, "Failed to get package")
		return
	}

	resp := entity.PackageResponse{
		ID:        pkg.ID,
		Name:      pkg.Name,
		PackDate:  pkg.PackDate,
		IsActive:  pkg.IsActive,
		CreatedAt: pkg.CreatedAt,
	}

	if packageTypeID.Valid {
		ptid, _ := uuid.Parse(packageTypeID.String)
		resp.PackageTypeID = &ptid
	}
	if packageTypeName.Valid {
		resp.PackageTypeName = packageTypeName.String
	}
	if shippingWeight.Valid {
		resp.ShippingWeight = &shippingWeight.Float64
	}
	if locationID.Valid {
		lid, _ := uuid.Parse(locationID.String)
		resp.LocationID = &lid
	}
	if locationName.Valid {
		resp.LocationName = locationName.String
	}

	// Get package contents
	contentQuery := `
		SELECT pc.id, pc.product_id, pc.quantity, pc.lot_id, pc.created_at,
			   p.code as product_code, p.name as product_name
		FROM package_contents pc
		JOIN products p ON pc.product_id = p.id
		WHERE pc.package_id = $1
	`

	rows, err := h.db.Query(contentQuery, id)
	if err != nil {
		h.log.Error("Failed to get package contents", "error", err)
	} else {
		defer rows.Close()
		contents := make([]entity.PackageContentResponse, 0)
		for rows.Next() {
			var content entity.PackageContent
			var lotID sql.NullString
			var productCode, productName string

			err := rows.Scan(&content.ID, &content.ProductID, &content.Quantity, &lotID, &content.CreatedAt, &productCode, &productName)
			if err != nil {
				continue
			}

			contentResp := entity.PackageContentResponse{
				ID:          content.ID,
				ProductID:   content.ProductID,
				ProductCode: productCode,
				ProductName: productName,
				Quantity:    content.Quantity,
			}
			if lotID.Valid {
				lid, _ := uuid.Parse(lotID.String)
				contentResp.LotID = &lid
			}
			contents = append(contents, contentResp)
		}
		resp.Contents = contents
		resp.ItemCount = len(contents)
	}

	response.Success(c, resp)
}

// UpdatePackage updates a package
func (h *Handler) UpdatePackage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Package ID is required")
		return
	}

	var input entity.UpdatePackageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.PackageTypeID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("package_type_id = $%d", argCount))
		args = append(args, *input.PackageTypeID)
	}
	if input.ShippingWeight != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("shipping_weight = $%d", argCount))
		args = append(args, *input.ShippingWeight)
	}
	if input.LocationID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("location_id = $%d", argCount))
		args = append(args, *input.LocationID)
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

	updates = append(updates, "updated_at = NOW()")

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE packages
		SET %s
		WHERE id = $%d AND tenant_id = $%d
		RETURNING id
	`, joinStrings(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err := h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Package not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to update package", "error", err)
		response.InternalError(c, "Failed to update package")
		return
	}

	response.Success(c, map[string]interface{}{"id": returnedID, "message": "Package updated successfully"})
}

// DeletePackage deletes a package
func (h *Handler) DeletePackage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Package ID is required")
		return
	}

	// Delete contents first
	h.db.Exec("DELETE FROM package_contents WHERE package_id = $1", id)

	// Delete package
	query := `DELETE FROM packages WHERE id = $1 AND tenant_id = $2`
	result, err := h.db.Exec(query, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete package", "error", err)
		response.InternalError(c, "Failed to delete package")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Package not found")
		return
	}

	response.Success(c, map[string]string{"message": "Package deleted successfully"})
}

// AddPackageContent adds a product to a package
func (h *Handler) AddPackageContent(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	packageID := c.Param("id")
	if packageID == "" {
		response.BadRequest(c, "Package ID is required")
		return
	}

	var input entity.AddPackageContentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Verify package exists
	var packageExists bool
	err := h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM packages WHERE id = $1 AND tenant_id = $2)", packageID, tenantID).Scan(&packageExists)
	if err != nil || !packageExists {
		response.NotFound(c, "Package not found")
		return
	}

	query := `
		INSERT INTO package_contents (package_id, product_id, quantity, lot_id, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id, created_at
	`

	var lotID *string
	if input.LotID != "" {
		lotID = &input.LotID
	}

	var id uuid.UUID
	var createdAt time.Time
	err = h.db.QueryRow(query, packageID, input.ProductID, input.Quantity, lotID).Scan(&id, &createdAt)
	if err != nil {
		h.log.Error("Failed to add package content", "error", err)
		response.InternalError(c, "Failed to add package content")
		return
	}

	productUUID, _ := uuid.Parse(input.ProductID)
	resp := entity.PackageContentResponse{
		ID:        id,
		ProductID: productUUID,
		Quantity:  input.Quantity,
	}

	response.Created(c, resp)
}

// RemovePackageContent removes a product from a package
func (h *Handler) RemovePackageContent(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	packageID := c.Param("id")
	contentID := c.Param("contentId")

	if packageID == "" || contentID == "" {
		response.BadRequest(c, "Package ID and Content ID are required")
		return
	}

	// Verify package belongs to tenant
	var packageExists bool
	err := h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM packages WHERE id = $1 AND tenant_id = $2)", packageID, tenantID).Scan(&packageExists)
	if err != nil || !packageExists {
		response.NotFound(c, "Package not found")
		return
	}

	query := `DELETE FROM package_contents WHERE id = $1 AND package_id = $2`
	result, err := h.db.Exec(query, contentID, packageID)
	if err != nil {
		h.log.Error("Failed to remove package content", "error", err)
		response.InternalError(c, "Failed to remove package content")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Package content not found")
		return
	}

	response.Success(c, map[string]string{"message": "Package content removed successfully"})
}
