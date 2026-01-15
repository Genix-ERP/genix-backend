package handler

import (
	"database/sql"
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

// ListAssetCategories returns all asset categories
func (h *Handler) ListAssetCategories(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, code, name, description, depreciation_method, default_useful_life_months, is_active
		FROM asset_categories
		WHERE tenant_id = $1
		ORDER BY name
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to list asset categories", "error", err)
		response.InternalError(c, "Failed to list asset categories")
		return
	}
	defer rows.Close()

	categories := make([]*entity.AssetCategoryResponse, 0)
	for rows.Next() {
		var cat entity.AssetCategory
		var description sql.NullString

		if err := rows.Scan(
			&cat.ID, &cat.TenantID, &cat.Code, &cat.Name, &description,
			&cat.DepreciationMethod, &cat.DefaultUsefulLifeMonths, &cat.IsActive,
		); err != nil {
			h.log.Error("Failed to scan asset category", "error", err)
			continue
		}

		if description.Valid {
			cat.Description = &description.String
		}

		categories = append(categories, cat.ToResponse())
	}

	response.Success(c, categories)
}

// ListFixedAssets returns a paginated list of fixed assets
func (h *Handler) ListFixedAssets(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	status := c.Query("status")
	categoryID := c.Query("category_id")

	baseQuery := `
		SELECT id, tenant_id, asset_code, name, description, category_id, category_name,
			   serial_number, acquisition_date, acquisition_cost, salvage_value, useful_life_months,
			   depreciation_method, accumulated_depreciation, book_value, location,
			   custodian_id, custodian_name, warranty_expiry, status,
			   disposal_date, disposal_amount, disposal_reason, notes, created_at, updated_at
		FROM fixed_assets
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM fixed_assets WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if status != "" && status != "all" {
		argCount++
		baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	if categoryID != "" {
		if id, err := uuid.Parse(categoryID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND category_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND category_id = $%d", argCount)
			args = append(args, id)
		}
	}

	baseQuery += " ORDER BY acquisition_date DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count fixed assets", "error", err)
		response.InternalError(c, "Failed to count fixed assets")
		return
	}

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list fixed assets", "error", err)
		response.InternalError(c, "Failed to list fixed assets")
		return
	}
	defer rows.Close()

	assets := make([]*entity.FixedAssetResponse, 0)
	for rows.Next() {
		var asset entity.FixedAsset
		var description, categoryID, categoryName, serialNumber, location, custodianID, custodianName sql.NullString
		var warrantyExpiry, disposalDate sql.NullTime
		var bookValue, disposalAmount sql.NullFloat64
		var disposalReason, notes sql.NullString

		if err := rows.Scan(
			&asset.ID, &asset.TenantID, &asset.AssetCode, &asset.Name, &description,
			&categoryID, &categoryName, &serialNumber, &asset.AcquisitionDate,
			&asset.AcquisitionCost, &asset.SalvageValue, &asset.UsefulLifeMonths,
			&asset.DepreciationMethod, &asset.AccumulatedDepreciation, &bookValue, &location,
			&custodianID, &custodianName, &warrantyExpiry, &asset.Status,
			&disposalDate, &disposalAmount, &disposalReason, &notes,
			&asset.CreatedAt, &asset.UpdatedAt,
		); err != nil {
			h.log.Error("Failed to scan fixed asset", "error", err)
			continue
		}

		if description.Valid {
			asset.Description = &description.String
		}
		if categoryID.Valid {
			if id, err := uuid.Parse(categoryID.String); err == nil {
				asset.CategoryID = &id
			}
		}
		if categoryName.Valid {
			asset.CategoryName = &categoryName.String
		}
		if serialNumber.Valid {
			asset.SerialNumber = &serialNumber.String
		}
		if location.Valid {
			asset.Location = &location.String
		}
		if custodianID.Valid {
			if id, err := uuid.Parse(custodianID.String); err == nil {
				asset.CustodianID = &id
			}
		}
		if custodianName.Valid {
			asset.CustodianName = &custodianName.String
		}
		if warrantyExpiry.Valid {
			asset.WarrantyExpiry = &warrantyExpiry.Time
		}
		if bookValue.Valid {
			asset.BookValue = &bookValue.Float64
		}
		if disposalDate.Valid {
			asset.DisposalDate = &disposalDate.Time
		}
		if disposalAmount.Valid {
			asset.DisposalAmount = &disposalAmount.Float64
		}
		if disposalReason.Valid {
			asset.DisposalReason = &disposalReason.String
		}
		if notes.Valid {
			asset.Notes = &notes.String
		}

		assets = append(assets, asset.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, assets, pagination)
}

// CreateFixedAsset creates a new fixed asset
func (h *Handler) CreateFixedAsset(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateFixedAssetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	assetCode := input.AssetCode
	if assetCode == "" {
		assetCode = fmt.Sprintf("FA-%d", time.Now().UnixNano()%100000)
	}

	acquisitionDate, err := time.Parse("2006-01-02", input.AcquisitionDate)
	if err != nil {
		response.BadRequest(c, "Invalid acquisition_date format")
		return
	}

	bookValue := input.AcquisitionCost - input.SalvageValue

	id := uuid.New()
	now := time.Now()

	var categoryID, custodianID *uuid.UUID
	var categoryName *string
	if input.CategoryID != "" {
		if parsedID, err := uuid.Parse(input.CategoryID); err == nil {
			categoryID = &parsedID
			// Get category name
			var name string
			if err := h.db.QueryRow("SELECT name FROM asset_categories WHERE id = $1", parsedID).Scan(&name); err == nil {
				categoryName = &name
			}
		}
	} else if input.Category != "" {
		categoryName = &input.Category
	}
	if input.CustodianID != "" {
		if parsedID, err := uuid.Parse(input.CustodianID); err == nil {
			custodianID = &parsedID
		}
	}

	query := `
		INSERT INTO fixed_assets (
			id, tenant_id, asset_code, name, description, category_id, category_name,
			serial_number, acquisition_date, acquisition_cost, salvage_value, useful_life_months,
			depreciation_method, accumulated_depreciation, book_value, location,
			custodian_id, custodian_name, warranty_expiry, status, notes,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		RETURNING id
	`

	var description, serialNumber, location, custodianName, notes *string
	var warrantyExpiry *time.Time

	if input.Description != "" {
		description = &input.Description
	}
	if input.SerialNumber != "" {
		serialNumber = &input.SerialNumber
	}
	if input.Location != "" {
		location = &input.Location
	}
	if input.CustodianName != "" {
		custodianName = &input.CustodianName
	}
	if input.WarrantyExpiry != "" {
		if parsed, err := time.Parse("2006-01-02", input.WarrantyExpiry); err == nil {
			warrantyExpiry = &parsed
		}
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	if err := h.db.QueryRow(query,
		id, tenantID, assetCode, input.Name, description, categoryID, categoryName,
		serialNumber, acquisitionDate, input.AcquisitionCost, input.SalvageValue, input.UsefulLifeMonths,
		input.DepreciationMethod, 0, bookValue, location, custodianID, custodianName,
		warrantyExpiry, "active", notes, userID, now, now,
	).Scan(&id); err != nil {
		h.log.Error("Failed to create fixed asset", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Asset with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create fixed asset")
		return
	}

	asset := &entity.FixedAsset{
		ID:                      id,
		TenantID:                tenantID,
		AssetCode:               assetCode,
		Name:                    input.Name,
		Description:             description,
		CategoryID:              categoryID,
		CategoryName:            categoryName,
		SerialNumber:            serialNumber,
		AcquisitionDate:         acquisitionDate,
		AcquisitionCost:         input.AcquisitionCost,
		SalvageValue:            input.SalvageValue,
		UsefulLifeMonths:        input.UsefulLifeMonths,
		DepreciationMethod:      input.DepreciationMethod,
		AccumulatedDepreciation: 0,
		BookValue:               &bookValue,
		Location:                location,
		CustodianID:             custodianID,
		CustodianName:           custodianName,
		WarrantyExpiry:          warrantyExpiry,
		Status:                  "active",
		Notes:                   notes,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	response.Created(c, asset.ToResponse())
}

// GetFixedAsset returns a single fixed asset
func (h *Handler) GetFixedAsset(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	query := `
		SELECT id, tenant_id, asset_code, name, description, category_id, category_name,
			   serial_number, acquisition_date, acquisition_cost, salvage_value, useful_life_months,
			   depreciation_method, accumulated_depreciation, book_value, location,
			   custodian_id, custodian_name, warranty_expiry, status,
			   disposal_date, disposal_amount, disposal_reason, notes, created_at, updated_at
		FROM fixed_assets
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var asset entity.FixedAsset
	var description, categoryID, categoryName, serialNumber, location, custodianID, custodianName sql.NullString
	var warrantyExpiry, disposalDate sql.NullTime
	var bookValue, disposalAmount sql.NullFloat64
	var disposalReason, notes sql.NullString

	if err := h.db.QueryRow(query, id, tenantID).Scan(
		&asset.ID, &asset.TenantID, &asset.AssetCode, &asset.Name, &description,
		&categoryID, &categoryName, &serialNumber, &asset.AcquisitionDate,
		&asset.AcquisitionCost, &asset.SalvageValue, &asset.UsefulLifeMonths,
		&asset.DepreciationMethod, &asset.AccumulatedDepreciation, &bookValue, &location,
		&custodianID, &custodianName, &warrantyExpiry, &asset.Status,
		&disposalDate, &disposalAmount, &disposalReason, &notes,
		&asset.CreatedAt, &asset.UpdatedAt,
	); err == sql.ErrNoRows {
		response.NotFound(c, "Fixed asset")
		return
	} else if err != nil {
		h.log.Error("Failed to get fixed asset", "error", err)
		response.InternalError(c, "Failed to get fixed asset")
		return
	}

	if description.Valid {
		asset.Description = &description.String
	}
	if categoryID.Valid {
		if id, err := uuid.Parse(categoryID.String); err == nil {
			asset.CategoryID = &id
		}
	}
	if categoryName.Valid {
		asset.CategoryName = &categoryName.String
	}
	if serialNumber.Valid {
		asset.SerialNumber = &serialNumber.String
	}
	if location.Valid {
		asset.Location = &location.String
	}
	if custodianID.Valid {
		if id, err := uuid.Parse(custodianID.String); err == nil {
			asset.CustodianID = &id
		}
	}
	if custodianName.Valid {
		asset.CustodianName = &custodianName.String
	}
	if warrantyExpiry.Valid {
		asset.WarrantyExpiry = &warrantyExpiry.Time
	}
	if bookValue.Valid {
		asset.BookValue = &bookValue.Float64
	}
	if disposalDate.Valid {
		asset.DisposalDate = &disposalDate.Time
	}
	if disposalAmount.Valid {
		asset.DisposalAmount = &disposalAmount.Float64
	}
	if disposalReason.Valid {
		asset.DisposalReason = &disposalReason.String
	}
	if notes.Valid {
		asset.Notes = &notes.String
	}

	response.Success(c, asset.ToResponse())
}

// UpdateFixedAsset updates an existing fixed asset
func (h *Handler) UpdateFixedAsset(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	var input entity.UpdateFixedAssetInput
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

	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.Description != nil {
		addUpdate("description", *input.Description)
	}
	if input.CategoryID != nil && *input.CategoryID != "" {
		if parsedID, err := uuid.Parse(*input.CategoryID); err == nil {
			addUpdate("category_id", parsedID)
		}
	}
	if input.SerialNumber != nil {
		addUpdate("serial_number", *input.SerialNumber)
	}
	if input.AcquisitionDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.AcquisitionDate); err == nil {
			addUpdate("acquisition_date", parsed)
		}
	}
	if input.AcquisitionCost != nil {
		addUpdate("acquisition_cost", *input.AcquisitionCost)
	}
	if input.SalvageValue != nil {
		addUpdate("salvage_value", *input.SalvageValue)
	}
	if input.UsefulLifeMonths != nil {
		addUpdate("useful_life_months", *input.UsefulLifeMonths)
	}
	if input.DepreciationMethod != nil {
		addUpdate("depreciation_method", *input.DepreciationMethod)
	}
	if input.Location != nil {
		addUpdate("location", *input.Location)
	}
	if input.CustodianID != nil && *input.CustodianID != "" {
		if parsedID, err := uuid.Parse(*input.CustodianID); err == nil {
			addUpdate("custodian_id", parsedID)
		}
	}
	if input.CustodianName != nil {
		addUpdate("custodian_name", *input.CustodianName)
	}
	if input.WarrantyExpiry != nil {
		if parsed, err := time.Parse("2006-01-02", *input.WarrantyExpiry); err == nil {
			addUpdate("warranty_expiry", parsed)
		}
	}
	if input.Status != nil {
		addUpdate("status", *input.Status)
	}
	if input.Notes != nil {
		addUpdate("notes", *input.Notes)
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
		UPDATE fixed_assets SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	if err := h.db.QueryRow(query, args...).Scan(&returnedID); err == sql.ErrNoRows {
		response.NotFound(c, "Fixed asset")
		return
	} else if err != nil {
		h.log.Error("Failed to update fixed asset", "error", err)
		response.InternalError(c, "Failed to update fixed asset")
		return
	}

	h.GetFixedAsset(c)
}

// DeleteFixedAsset soft-deletes a fixed asset
func (h *Handler) DeleteFixedAsset(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	query := `
		UPDATE fixed_assets SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete fixed asset", "error", err)
		response.InternalError(c, "Failed to delete fixed asset")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Fixed asset")
		return
	}

	response.NoContent(c)
}

// DisposeFixedAsset disposes a fixed asset
func (h *Handler) DisposeFixedAsset(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	var input entity.DisposeAssetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	disposalDate, err := time.Parse("2006-01-02", input.DisposalDate)
	if err != nil {
		response.BadRequest(c, "Invalid disposal_date format")
		return
	}

	query := `
		UPDATE fixed_assets SET
			status = 'disposed',
			disposal_date = $1,
			disposal_amount = $2,
			disposal_reason = $3,
			updated_at = $4
		WHERE id = $5 AND tenant_id = $6 AND deleted_at IS NULL
		RETURNING id
	`

	var disposalReason *string
	if input.DisposalReason != "" {
		disposalReason = &input.DisposalReason
	}

	var returnedID uuid.UUID
	if err := h.db.QueryRow(query, disposalDate, input.DisposalAmount, disposalReason, time.Now(), id, tenantID).Scan(&returnedID); err == sql.ErrNoRows {
		response.NotFound(c, "Fixed asset")
		return
	} else if err != nil {
		h.log.Error("Failed to dispose fixed asset", "error", err)
		response.InternalError(c, "Failed to dispose fixed asset")
		return
	}

	h.GetFixedAsset(c)
}

// RunDepreciation runs depreciation for all active assets for a given period
func (h *Handler) RunDepreciation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateDepreciationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Get all active assets
	assetsQuery := `
		SELECT id, acquisition_cost, salvage_value, useful_life_months, depreciation_method, accumulated_depreciation
		FROM fixed_assets
		WHERE tenant_id = $1 AND status = 'active' AND deleted_at IS NULL
	`

	rows, err := h.db.Query(assetsQuery, tenantID)
	if err != nil {
		h.log.Error("Failed to get assets for depreciation", "error", err)
		response.InternalError(c, "Failed to run depreciation")
		return
	}
	defer rows.Close()

	now := time.Now()
	deprecationDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	newEntries := make([]*entity.DepreciationEntryResponse, 0)

	for rows.Next() {
		var assetID uuid.UUID
		var acquisitionCost, salvageValue, accumulatedDepreciation float64
		var usefulLifeMonths int
		var depreciationMethod string

		if err := rows.Scan(&assetID, &acquisitionCost, &salvageValue, &usefulLifeMonths, &depreciationMethod, &accumulatedDepreciation); err != nil {
			h.log.Error("Failed to scan asset", "error", err)
			continue
		}

		// Check if depreciation already exists for this period
		var existingID uuid.UUID
		checkQuery := "SELECT id FROM depreciation_entries WHERE asset_id = $1 AND period = $2"
		if err := h.db.QueryRow(checkQuery, assetID, input.Period).Scan(&existingID); err == nil {
			// Already exists, skip
			continue
		}

		// Calculate depreciation
		depreciableAmount := acquisitionCost - salvageValue
		remainingValue := depreciableAmount - accumulatedDepreciation
		if remainingValue <= 0 {
			continue
		}

		var depAmount float64
		switch depreciationMethod {
		case "straight_line":
			depAmount = depreciableAmount / float64(usefulLifeMonths)
		case "declining_balance":
			rate := 1.0 / float64(usefulLifeMonths)
			currentValue := acquisitionCost - accumulatedDepreciation
			depAmount = currentValue * rate
		case "double_declining":
			rate := 2.0 / float64(usefulLifeMonths)
			currentValue := acquisitionCost - accumulatedDepreciation
			depAmount = currentValue * rate
		default:
			depAmount = depreciableAmount / float64(usefulLifeMonths)
		}

		// Cap depreciation at remaining value
		if depAmount > remainingValue {
			depAmount = remainingValue
		}

		newAccumulated := accumulatedDepreciation + depAmount
		newBookValue := acquisitionCost - newAccumulated

		// Insert depreciation entry
		entryID := uuid.New()
		insertQuery := `
			INSERT INTO depreciation_entries (
				id, tenant_id, asset_id, period, depreciation_date, depreciation_amount,
				accumulated_total, book_value_after, depreciation_method, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`
		if _, err := h.db.Exec(insertQuery, entryID, tenantID, assetID, input.Period, deprecationDate, depAmount, newAccumulated, newBookValue, depreciationMethod, now); err != nil {
			h.log.Error("Failed to insert depreciation entry", "error", err)
			continue
		}

		// Update asset accumulated depreciation
		updateQuery := `
			UPDATE fixed_assets SET accumulated_depreciation = $1, book_value = $2, updated_at = $3
			WHERE id = $4
		`
		if _, err := h.db.Exec(updateQuery, newAccumulated, newBookValue, now, assetID); err != nil {
			h.log.Error("Failed to update asset depreciation", "error", err)
		}

		entry := &entity.DepreciationEntry{
			ID:                 entryID,
			TenantID:           tenantID,
			AssetID:            assetID,
			Period:             input.Period,
			DepreciationDate:   deprecationDate,
			DepreciationAmount: depAmount,
			AccumulatedTotal:   newAccumulated,
			BookValueAfter:     newBookValue,
			DepreciationMethod: depreciationMethod,
			CreatedAt:          now,
		}
		newEntries = append(newEntries, entry.ToResponse())
	}

	response.Success(c, gin.H{
		"message": fmt.Sprintf("Depreciation run completed for period %s", input.Period),
		"entries": newEntries,
	})
}

// GetDepreciationEntries returns depreciation entries for an asset
func (h *Handler) GetDepreciationEntries(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	assetIDStr := c.Param("id")
	assetID, err := uuid.Parse(assetIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	query := `
		SELECT id, tenant_id, asset_id, period, depreciation_date, depreciation_amount,
			   accumulated_total, book_value_after, depreciation_method, created_at
		FROM depreciation_entries
		WHERE asset_id = $1 AND tenant_id = $2
		ORDER BY depreciation_date DESC
	`

	rows, err := h.db.Query(query, assetID, tenantID)
	if err != nil {
		h.log.Error("Failed to list depreciation entries", "error", err)
		response.InternalError(c, "Failed to list depreciation entries")
		return
	}
	defer rows.Close()

	entries := make([]*entity.DepreciationEntryResponse, 0)
	for rows.Next() {
		var entry entity.DepreciationEntry
		var method sql.NullString

		if err := rows.Scan(
			&entry.ID, &entry.TenantID, &entry.AssetID, &entry.Period,
			&entry.DepreciationDate, &entry.DepreciationAmount,
			&entry.AccumulatedTotal, &entry.BookValueAfter, &method, &entry.CreatedAt,
		); err != nil {
			h.log.Error("Failed to scan depreciation entry", "error", err)
			continue
		}

		if method.Valid {
			entry.DepreciationMethod = method.String
		}

		entries = append(entries, entry.ToResponse())
	}

	response.Success(c, entries)
}
