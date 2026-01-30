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
// SUPPLIER PRICE HISTORY HANDLERS
// =====================================================

// ListPriceHistory returns a paginated list of price history records
func (h *Handler) ListPriceHistory(c *gin.Context) {
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

	productID := c.Query("product_id")
	vendorID := c.Query("vendor_id")
	grouped := c.Query("grouped") == "true"

	if grouped {
		h.listPriceHistoryGrouped(c, tenantID, productID, vendorID)
		return
	}

	baseQuery := `
		SELECT ph.id, ph.product_id, p.name as product_name,
			   ph.vendor_id, v.name as vendor_name,
			   ph.unit_price, COALESCE(cur.code, 'UZS') as currency,
			   ph.effective_date, ph.min_quantity, ph.notes, ph.source,
			   ph.created_at
		FROM supplier_price_history ph
		LEFT JOIN products p ON ph.product_id = p.id
		LEFT JOIN contacts v ON ph.vendor_id = v.id
		LEFT JOIN currencies cur ON ph.currency_id = cur.id
		WHERE ph.tenant_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM supplier_price_history ph WHERE ph.tenant_id = $1`

	args := []interface{}{tenantID}
	argCount := 1

	if productID != "" {
		if pid, err := uuid.Parse(productID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND ph.product_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND ph.product_id = $%d", argCount)
			args = append(args, pid)
		}
	}

	if vendorID != "" {
		if vid, err := uuid.Parse(vendorID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND ph.vendor_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND ph.vendor_id = $%d", argCount)
			args = append(args, vid)
		}
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count price history", "error", err)
		response.InternalError(c, "Failed to list price history")
		return
	}

	baseQuery += " ORDER BY ph.effective_date DESC, ph.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list price history", "error", err)
		response.InternalError(c, "Failed to list price history")
		return
	}
	defer rows.Close()

	records := make([]*entity.PriceHistoryResponse, 0)
	for rows.Next() {
		var record entity.PriceHistoryResponse
		var notes, source sql.NullString

		err := rows.Scan(
			&record.ID, &record.ProductID, &record.ProductName,
			&record.VendorID, &record.VendorName,
			&record.UnitPrice, &record.Currency,
			&record.EffectiveDate, &record.MinQuantity, &notes, &source,
			&record.CreatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan price history", "error", err)
			continue
		}

		if notes.Valid {
			record.Notes = &notes.String
		}
		if source.Valid {
			record.Source = &source.String
		}

		records = append(records, &record)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, records, pagination)
}

// listPriceHistoryGrouped returns price history grouped by product and supplier
func (h *Handler) listPriceHistoryGrouped(c *gin.Context, tenantID uuid.UUID, productID, vendorID string) {
	query := `
		SELECT ph.product_id, p.name as product_name,
			   ph.vendor_id, v.name as vendor_name,
			   ph.effective_date, ph.unit_price, COALESCE(cur.code, 'UZS') as currency
		FROM supplier_price_history ph
		LEFT JOIN products p ON ph.product_id = p.id
		LEFT JOIN contacts v ON ph.vendor_id = v.id
		LEFT JOIN currencies cur ON ph.currency_id = cur.id
		WHERE ph.tenant_id = $1
	`
	args := []interface{}{tenantID}
	argCount := 1

	if productID != "" {
		if pid, err := uuid.Parse(productID); err == nil {
			argCount++
			query += fmt.Sprintf(" AND ph.product_id = $%d", argCount)
			args = append(args, pid)
		}
	}

	if vendorID != "" {
		if vid, err := uuid.Parse(vendorID); err == nil {
			argCount++
			query += fmt.Sprintf(" AND ph.vendor_id = $%d", argCount)
			args = append(args, vid)
		}
	}

	query += " ORDER BY p.name, v.name, ph.effective_date"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list grouped price history", "error", err)
		response.InternalError(c, "Failed to list price history")
		return
	}
	defer rows.Close()

	// Group by product + supplier
	groupMap := make(map[string]*entity.PriceHistoryGroupedResponse)

	for rows.Next() {
		var productID, vendorID uuid.UUID
		var productName, vendorName, currency string
		var effectiveDate time.Time
		var unitPrice float64

		err := rows.Scan(&productID, &productName, &vendorID, &vendorName, &effectiveDate, &unitPrice, &currency)
		if err != nil {
			h.log.Error("Failed to scan grouped price history", "error", err)
			continue
		}

		key := productID.String() + "-" + vendorID.String()
		if _, exists := groupMap[key]; !exists {
			groupMap[key] = &entity.PriceHistoryGroupedResponse{
				ID:           key,
				ProductID:    productID,
				ProductName:  productName,
				SupplierID:   vendorID,
				SupplierName: vendorName,
				Prices:       make([]entity.PriceHistoryEntry, 0),
			}
		}

		groupMap[key].Prices = append(groupMap[key].Prices, entity.PriceHistoryEntry{
			Date:     effectiveDate.Format("2006-01-02"),
			Price:    unitPrice,
			Currency: currency,
		})
	}

	// Convert map to slice
	result := make([]*entity.PriceHistoryGroupedResponse, 0, len(groupMap))
	for _, group := range groupMap {
		result = append(result, group)
	}

	response.Success(c, result)
}

// CreatePriceHistory creates a new price history record
func (h *Handler) CreatePriceHistory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreatePriceHistoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	productID, err := uuid.Parse(input.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product_id")
		return
	}

	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor_id")
		return
	}

	// Verify product exists
	var productExists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM products WHERE id = $1 AND tenant_id = $2)", productID, tenantID).Scan(&productExists)
	if err != nil || !productExists {
		response.NotFound(c, "Product not found")
		return
	}

	// Verify vendor exists
	var vendorExists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND type = 'vendor')", vendorID).Scan(&vendorExists)
	if err != nil || !vendorExists {
		response.NotFound(c, "Vendor not found")
		return
	}

	id := uuid.New()
	effectiveDate := time.Now()
	if input.EffectiveDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.EffectiveDate); err == nil {
			effectiveDate = parsed
		}
	}

	minQuantity := input.MinQuantity
	if minQuantity <= 0 {
		minQuantity = 1
	}

	// Get currency_id if currency code provided
	var currencyID *uuid.UUID
	if input.Currency != "" {
		var cid uuid.UUID
		err = h.db.QueryRow("SELECT id FROM currencies WHERE code = $1", input.Currency).Scan(&cid)
		if err == nil {
			currencyID = &cid
		}
	}

	source := "manual"
	if input.Source != "" {
		source = input.Source
	}

	query := `
		INSERT INTO supplier_price_history
		(id, tenant_id, product_id, vendor_id, unit_price, currency_id, effective_date, min_quantity, notes, source, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	_, err = h.db.Exec(query, id, tenantID, productID, vendorID, input.UnitPrice, currencyID, effectiveDate, minQuantity, notes, source, time.Now())
	if err != nil {
		h.log.Error("Failed to create price history", "error", err)
		response.InternalError(c, "Failed to create price history")
		return
	}

	// Fetch the created record with product and vendor names
	var record entity.PriceHistoryResponse
	var notesResp, sourceResp sql.NullString
	err = h.db.QueryRow(`
		SELECT ph.id, ph.product_id, p.name as product_name,
			   ph.vendor_id, v.name as vendor_name,
			   ph.unit_price, COALESCE(cur.code, 'UZS') as currency,
			   ph.effective_date, ph.min_quantity, ph.notes, ph.source,
			   ph.created_at
		FROM supplier_price_history ph
		LEFT JOIN products p ON ph.product_id = p.id
		LEFT JOIN contacts v ON ph.vendor_id = v.id
		LEFT JOIN currencies cur ON ph.currency_id = cur.id
		WHERE ph.id = $1
	`, id).Scan(
		&record.ID, &record.ProductID, &record.ProductName,
		&record.VendorID, &record.VendorName,
		&record.UnitPrice, &record.Currency,
		&record.EffectiveDate, &record.MinQuantity, &notesResp, &sourceResp,
		&record.CreatedAt,
	)
	if err != nil {
		h.log.Error("Failed to fetch created price history", "error", err)
		response.Created(c, gin.H{"id": id, "message": "Price history created successfully"})
		return
	}

	if notesResp.Valid {
		record.Notes = &notesResp.String
	}
	if sourceResp.Valid {
		record.Source = &sourceResp.String
	}

	response.Created(c, record)
}

// GetPriceHistory returns a single price history record
func (h *Handler) GetPriceHistory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid price history ID")
		return
	}

	var record entity.PriceHistoryResponse
	var notes, source sql.NullString

	err = h.db.QueryRow(`
		SELECT ph.id, ph.product_id, p.name as product_name,
			   ph.vendor_id, v.name as vendor_name,
			   ph.unit_price, COALESCE(cur.code, 'UZS') as currency,
			   ph.effective_date, ph.min_quantity, ph.notes, ph.source,
			   ph.created_at
		FROM supplier_price_history ph
		LEFT JOIN products p ON ph.product_id = p.id
		LEFT JOIN contacts v ON ph.vendor_id = v.id
		LEFT JOIN currencies cur ON ph.currency_id = cur.id
		WHERE ph.id = $1 AND ph.tenant_id = $2
	`, id, tenantID).Scan(
		&record.ID, &record.ProductID, &record.ProductName,
		&record.VendorID, &record.VendorName,
		&record.UnitPrice, &record.Currency,
		&record.EffectiveDate, &record.MinQuantity, &notes, &source,
		&record.CreatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Price history not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get price history", "error", err)
		response.InternalError(c, "Failed to get price history")
		return
	}

	if notes.Valid {
		record.Notes = &notes.String
	}
	if source.Valid {
		record.Source = &source.String
	}

	response.Success(c, record)
}

// DeletePriceHistory deletes a price history record
func (h *Handler) DeletePriceHistory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid price history ID")
		return
	}

	result, err := h.db.Exec("DELETE FROM supplier_price_history WHERE id = $1 AND tenant_id = $2", id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete price history", "error", err)
		response.InternalError(c, "Failed to delete price history")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Price history not found")
		return
	}

	response.Success(c, gin.H{"message": "Price history deleted successfully"})
}
