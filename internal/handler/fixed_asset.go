package handler

import (
	"database/sql"
	"fmt"

	"math"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// scanner is an interface for both sql.Row and sql.Rows
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanFixedAsset scans a fixed asset row into an entity
func scanFixedAsset(row scanner, h *Handler) *entity.FixedAsset {
	var asset entity.FixedAsset
	var description, categoryID, categoryName, serialNumber, location, custodianID, custodianName sql.NullString
	var supplierID, supplierName, documentNumber, paymentMethod sql.NullString
	var warrantyExpiry, disposalDate, lastDeprDate, documentDate sql.NullTime
	var bookValue, disposalAmount, currentValue, monthlyDepr, totalPaid sql.NullFloat64
	var remainingMonths sql.NullInt32
	var disposalReason, notes sql.NullString

	if err := row.Scan(
		&asset.ID, &asset.TenantID, &asset.AssetCode, &asset.Name, &description,
		&categoryID, &categoryName, &serialNumber, &asset.AcquisitionDate,
		&asset.AcquisitionCost, &asset.SalvageValue, &asset.UsefulLifeMonths,
		&asset.DepreciationMethod, &asset.AccumulatedDepreciation, &bookValue,
		&currentValue, &remainingMonths, &monthlyDepr, &lastDeprDate,
		&location, &custodianID, &custodianName,
		&supplierID, &supplierName, &documentNumber, &documentDate, &paymentMethod, &totalPaid,
		&warrantyExpiry, &asset.Status,
		&disposalDate, &disposalAmount, &disposalReason, &notes,
		&asset.CreatedAt, &asset.UpdatedAt,
	); err != nil {
		if h != nil {
			h.log.Error("Failed to scan fixed asset", "error", err)
		}
		return nil
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
	if supplierID.Valid {
		if id, err := uuid.Parse(supplierID.String); err == nil {
			asset.SupplierID = &id
		}
	}
	if supplierName.Valid {
		asset.SupplierName = &supplierName.String
	}
	if documentNumber.Valid {
		asset.DocumentNumber = &documentNumber.String
	}
	if documentDate.Valid {
		asset.DocumentDate = &documentDate.Time
	}
	if paymentMethod.Valid {
		asset.PaymentMethod = &paymentMethod.String
	}
	if totalPaid.Valid {
		asset.TotalPaid = &totalPaid.Float64
	}
	if warrantyExpiry.Valid {
		asset.WarrantyExpiry = &warrantyExpiry.Time
	}
	if bookValue.Valid {
		asset.BookValue = &bookValue.Float64
	}
	if currentValue.Valid {
		asset.CurrentValue = &currentValue.Float64
	}
	if remainingMonths.Valid {
		rm := int(remainingMonths.Int32)
		asset.RemainingMonths = &rm
	}
	if monthlyDepr.Valid {
		asset.MonthlyDepr = &monthlyDepr.Float64
	}
	if lastDeprDate.Valid {
		asset.LastDeprDate = &lastDeprDate.Time
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

	return &asset
}

// ListAssetCategories returns all asset categories
func (h *Handler) ListAssetCategories(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, code, name, description, depreciation_method, default_useful_life_months, is_active,
			   asset_account_id, depreciation_account_id, expense_account_id
		FROM asset_categories
		WHERE tenant_id = $1
	`
	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	query += " ORDER BY name"

	rows, err := h.db.Query(query, args...)
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
		var assetAcctID, deprAcctID, expAcctID *uuid.UUID

		if err := rows.Scan(
			&cat.ID, &cat.TenantID, &cat.Code, &cat.Name, &description,
			&cat.DepreciationMethod, &cat.DefaultUsefulLifeMonths, &cat.IsActive,
			&assetAcctID, &deprAcctID, &expAcctID,
		); err != nil {
			h.log.Error("Failed to scan asset category", "error", err)
			continue
		}

		if description.Valid {
			cat.Description = &description.String
		}
		cat.AssetAccountID = assetAcctID
		cat.DepreciationAccountID = deprAcctID
		cat.ExpenseAccountID = expAcctID

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
			   depreciation_method, accumulated_depreciation, book_value,
			   current_value, remaining_months, monthly_depr, last_depr_date,
			   location, custodian_id, custodian_name,
			   supplier_id, supplier_name, document_number, document_date, payment_method, total_paid,
			   warranty_expiry, status,
			   disposal_date, disposal_amount, disposal_reason, notes, created_at, updated_at
		FROM fixed_assets
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM fixed_assets WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

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
		asset := scanFixedAsset(rows, h)
		if asset != nil {
			assets = append(assets, asset.ToResponse())
		}
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Auto-generate sequential asset code: AV-001, AV-002, ...
	var maxCode int
	h.db.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTRING(asset_code FROM 4) AS INTEGER)), 0) FROM fixed_assets WHERE tenant_id = $1 AND asset_code ~ '^AV-[0-9]+$'", tenantID).Scan(&maxCode)
	assetCode := fmt.Sprintf("AV-%03d", maxCode+1)

	acquisitionDate, err := time.Parse("2006-01-02", input.AcquisitionDate)
	if err != nil {
		response.BadRequest(c, "Invalid acquisition_date format")
		return
	}

	bookValue := input.AcquisitionCost - input.SalvageValue
	currentValue := bookValue
	remainingMonths := input.UsefulLifeMonths
	var monthlyDepr float64
	if remainingMonths > 0 {
		monthlyDepr = (input.AcquisitionCost - input.SalvageValue) / float64(remainingMonths)
	}

	id := uuid.New()
	now := time.Now()

	// Get organization ID
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var categoryID, custodianID *uuid.UUID
	var categoryName *string
	var catAssetAcctID, catDeprAcctID, catExpenseAcctID *uuid.UUID
	if input.CategoryID != "" {
		if parsedID, err := uuid.Parse(input.CategoryID); err == nil {
			categoryID = &parsedID
			// Get category name and default account IDs
			var name string
			var assetAcct, deprAcct, expAcct *uuid.UUID
			if err := h.db.QueryRow("SELECT name, asset_account_id, depreciation_account_id, expense_account_id FROM asset_categories WHERE id = $1", parsedID).Scan(&name, &assetAcct, &deprAcct, &expAcct); err == nil {
				categoryName = &name
				catAssetAcctID = assetAcct
				catDeprAcctID = deprAcct
				catExpenseAcctID = expAcct
			}
		}
	} else if input.Category != "" {
		categoryName = &input.Category
	}
	_, _ = catDeprAcctID, catExpenseAcctID // used in depreciation context, available for future use
	if input.CustodianID != "" {
		if parsedID, err := uuid.Parse(input.CustodianID); err == nil {
			custodianID = &parsedID
		}
	}

	query := `
		INSERT INTO fixed_assets (
			id, tenant_id, organization_id, asset_code, name, description, category_id, category_name,
			serial_number, acquisition_date, acquisition_cost, salvage_value, useful_life_months,
			depreciation_method, accumulated_depreciation, book_value,
			current_value, remaining_months, monthly_depr,
			location, custodian_id, custodian_name,
			supplier_id, supplier_name, document_number, document_date, payment_method, total_paid,
			warranty_expiry, status, notes,
			created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34)
		RETURNING id
	`

	var description, serialNumber, location, custodianName, notes *string
	var supplierNamePtr, documentNumber *string
	var warrantyExpiry, documentDate *time.Time
	var paymentMethod *string
	var totalPaid float64

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
	var supplierID *uuid.UUID
	if input.SupplierID != "" {
		if parsedID, err := uuid.Parse(input.SupplierID); err == nil {
			supplierID = &parsedID
		}
	}
	if input.SupplierName != "" {
		supplierNamePtr = &input.SupplierName
	}
	if input.DocumentNumber != "" {
		documentNumber = &input.DocumentNumber
	}
	if input.DocumentDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.DocumentDate); err == nil {
			documentDate = &parsed
		}
	}
	pm := input.PaymentMethod
	if pm == "" {
		pm = "bank"
	}
	paymentMethod = &pm
	// For cash/bank, total_paid = acquisition_cost; for credit, total_paid = 0
	if pm != "credit" {
		totalPaid = input.AcquisitionCost
	}

	if err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, assetCode, input.Name, description, categoryID, categoryName,
		serialNumber, acquisitionDate, input.AcquisitionCost, input.SalvageValue, input.UsefulLifeMonths,
		input.DepreciationMethod, 0, bookValue,
		currentValue, remainingMonths, monthlyDepr,
		location, custodianID, custodianName,
		supplierID, supplierNamePtr, documentNumber, documentDate, paymentMethod, totalPaid,
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

	// ============================================
	// CREATE JOURNAL ENTRY FOR FIXED ASSET ACQUISITION
	// ============================================
	func() {
		if input.AcquisitionCost <= 0 {
			return
		}

		var journalID uuid.UUID
		var nextNumber int
		err := h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('ASSET','MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE code WHEN 'ASSET' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNumber)
		if err != nil {
			return
		}

		// Debit: Fixed Asset account — use category default if available, else fallback to 1500
		var faAcct uuid.UUID
		if catAssetAcctID != nil {
			faAcct = *catAssetAcctID
		} else {
			faAcct = findAccount(h.db, tenantID, orgIDPtr, "fixed assets", "0100")
			if faAcct == uuid.Nil {
				faAcct = findAccount(h.db, tenantID, orgIDPtr, "fixed asset", "0100")
			}
		}
		if faAcct == uuid.Nil {
			return
		}

		// Credit account depends on payment method
		var creditAcct uuid.UUID
		var creditDesc string
		switch pm {
		case "credit":
			// Dt 1500 Fixed Assets / Kt 6010 Accounts Payable
			creditAcct = findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "6010")
			if creditAcct == uuid.Nil {
				creditAcct = findAccount(h.db, tenantID, orgIDPtr, "payable", "6010")
			}
			creditDesc = "Accounts Payable"
		case "bank":
			creditAcct = findAccount(h.db, tenantID, orgIDPtr, "bank account", "5110")
			creditDesc = "Bank"
		default: // cash
			creditAcct = findAccount(h.db, tenantID, orgIDPtr, "cash", "5010")
			if creditAcct == uuid.Nil {
				creditAcct = findAccount(h.db, tenantID, orgIDPtr, "bank account", "5110")
			}
			creditDesc = "Cash"
		}
		if creditAcct == uuid.Nil {
			return
		}

		entryNumber := fmt.Sprintf("FA%06d", nextNumber)
		journalEntryID := uuid.New()
		jeDescription := "Fixed Asset Acquisition: " + assetCode + " - " + input.Name

		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'fixed_asset', $9, 1.0, $10, $10, 'posted', $11, $12, $12)`,
			journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, acquisitionDate, assetCode, jeDescription,
			id.String(), input.AcquisitionCost, userID, now,
		)
		if err != nil {
			h.log.Error("Failed to create fixed asset journal entry", "error", err)
			return
		}

		// Debit: Fixed Asset
		h.db.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 1, $3, $4, $5, 0, 1.0, $6)`,
			uuid.New(), journalEntryID, faAcct, "Fixed Asset", input.AcquisitionCost, now,
		)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", input.AcquisitionCost, now, faAcct)

		// Credit: Cash/Bank/AP
		h.db.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 2, $3, $4, 0, $5, 1.0, $6)`,
			uuid.New(), journalEntryID, creditAcct, creditDesc, input.AcquisitionCost, now,
		)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", input.AcquisitionCost, now, creditAcct)

		h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
	}()

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
		CurrentValue:            &currentValue,
		RemainingMonths:         &remainingMonths,
		MonthlyDepr:             &monthlyDepr,
		Location:                location,
		CustodianID:             custodianID,
		CustodianName:           custodianName,
		SupplierID:              supplierID,
		SupplierName:            supplierNamePtr,
		DocumentNumber:          documentNumber,
		DocumentDate:            documentDate,
		PaymentMethod:           paymentMethod,
		TotalPaid:               &totalPaid,
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
			   depreciation_method, accumulated_depreciation, book_value,
			   current_value, remaining_months, monthly_depr, last_depr_date,
			   location, custodian_id, custodian_name,
			   supplier_id, supplier_name, document_number, document_date, payment_method, total_paid,
			   warranty_expiry, status,
			   disposal_date, disposal_amount, disposal_reason, notes, created_at, updated_at
		FROM fixed_assets
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	row := h.db.QueryRow(query, id, tenantID)
	asset := scanFixedAsset(row, h)
	if asset == nil {
		response.NotFound(c, "Fixed asset")
		return
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

// DisposeFixedAsset disposes a fixed asset with proper journal entries
func (h *Handler) DisposeFixedAsset(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	var input entity.DisposeAssetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	validReasons := map[string]bool{"sold": true, "destroyed": true, "expired": true, "stolen": true}
	if !validReasons[input.DisposalReason] {
		response.BadRequest(c, "Reason must be: sold, destroyed, expired, stolen")
		return
	}

	disposalDate, err := time.Parse("2006-01-02", input.DisposalDate)
	if err != nil {
		response.BadRequest(c, "Invalid disposal_date format")
		return
	}

	// Get asset details for journal entries
	var assetName string
	var acquisitionCost, accumDepr, curValue float64
	err = h.db.QueryRow(`
		SELECT name, acquisition_cost, accumulated_depreciation,
			   COALESCE(current_value, acquisition_cost - accumulated_depreciation)
		FROM fixed_assets WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND status = 'active'`,
		id, tenantID).Scan(&assetName, &acquisitionCost, &accumDepr, &curValue)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Active fixed asset")
		return
	} else if err != nil {
		h.log.Error("Failed to get asset for disposal", "error", err)
		response.InternalError(c, "Failed to get asset")
		return
	}

	now := time.Now()
	disposalReason := &input.DisposalReason

	// Update asset status
	_, err = h.db.Exec(`
		UPDATE fixed_assets SET
			status = 'disposed', disposal_date = $1, disposal_amount = $2,
			disposal_reason = $3, remaining_months = 0, updated_at = $4
		WHERE id = $5 AND tenant_id = $6`,
		disposalDate, input.DisposalAmount, disposalReason, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to dispose fixed asset", "error", err)
		response.InternalError(c, "Failed to dispose fixed asset")
		return
	}

	// Create journal entries for disposal
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	func() {
		var journalID uuid.UUID
		var nextNum int
		if err := h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('ASSET','MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE code WHEN 'ASSET' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNum); err != nil {
			return
		}

		faAcct := findAccount(h.db, tenantID, orgIDPtr, "fixed assets", "0100")
		if faAcct == uuid.Nil {
			faAcct = findAccount(h.db, tenantID, orgIDPtr, "fixed asset", "0100")
		}
		accumDeprAcct := findAccount(h.db, tenantID, orgIDPtr, "accumulated depreciation", "0200")

		if faAcct == uuid.Nil {
			return
		}

		jeID := uuid.New()
		entryNumber := fmt.Sprintf("DSP%06d", nextNum)
		desc := fmt.Sprintf("Asset Disposal (%s): %s", input.DisposalReason, assetName)
		lineNum := 0

		_, err := h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'disposal',$9,1.0,$10,$10,'posted',$11,$12,$12)`,
			jeID, tenantID, orgIDPtr, journalID, entryNumber, disposalDate, id.String(), desc,
			id.String(), acquisitionCost, userID, now,
		)
		if err != nil {
			return
		}

		// Dt: Accumulated Depreciation (clear it out)
		if accumDeprAcct != uuid.Nil && accumDepr > 0 {
			lineNum++
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1,$2,$3,$4,'Accumulated Depreciation',$5,0,1.0,$6)`, uuid.New(), jeID, lineNum, accumDeprAcct, accumDepr, now)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", accumDepr, now, accumDeprAcct)
		}

		if input.DisposalReason == "sold" && input.DisposalAmount > 0 {
			// Dt: Cash/Bank for sale proceeds
			var cashAcct uuid.UUID
			cashAcct = findAccount(h.db, tenantID, orgIDPtr, "cash", "5010")
			if cashAcct == uuid.Nil {
				cashAcct = findAccount(h.db, tenantID, orgIDPtr, "bank account", "5110")
			}
			if cashAcct != uuid.Nil {
				lineNum++
				h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
					VALUES ($1,$2,$3,$4,'Sale Proceeds',$5,0,1.0,$6)`, uuid.New(), jeID, lineNum, cashAcct, input.DisposalAmount, now)
				h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", input.DisposalAmount, now, cashAcct)
			}

			// If sold at loss, debit loss account
			bookVal := acquisitionCost - accumDepr
			if input.DisposalAmount < bookVal {
				lossAmt := bookVal - input.DisposalAmount
				lossAcct := findAccount(h.db, tenantID, orgIDPtr, "loss on disposal", "9500")
				if lossAcct == uuid.Nil {
					lossAcct = findAccount(h.db, tenantID, orgIDPtr, "other expense", "9400")
				}
				if lossAcct != uuid.Nil {
					lineNum++
					h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
						VALUES ($1,$2,$3,$4,'Loss on Disposal',$5,0,1.0,$6)`, uuid.New(), jeID, lineNum, lossAcct, lossAmt, now)
					h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", lossAmt, now, lossAcct)
				}
			} else if input.DisposalAmount > bookVal {
				// Gain on disposal
				gainAmt := input.DisposalAmount - bookVal
				gainAcct := findAccount(h.db, tenantID, orgIDPtr, "gain on disposal", "9100")
				if gainAcct == uuid.Nil {
					gainAcct = findAccount(h.db, tenantID, orgIDPtr, "other income", "9000")
				}
				if gainAcct != uuid.Nil {
					lineNum++
					h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
						VALUES ($1,$2,$3,$4,'Gain on Disposal',0,$5,1.0,$6)`, uuid.New(), jeID, lineNum, gainAcct, gainAmt, now)
					h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", gainAmt, now, gainAcct)
				}
			}
		} else {
			// destroyed/expired/stolen — write off remaining book value as loss
			bookVal := acquisitionCost - accumDepr
			if bookVal > 0 {
				lossAcct := findAccount(h.db, tenantID, orgIDPtr, "loss on disposal", "9500")
				if lossAcct == uuid.Nil {
					lossAcct = findAccount(h.db, tenantID, orgIDPtr, "other expense", "9400")
				}
				if lossAcct != uuid.Nil {
					lineNum++
					h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
						VALUES ($1,$2,$3,$4,'Write-off Loss',$5,0,1.0,$6)`, uuid.New(), jeID, lineNum, lossAcct, bookVal, now)
					h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", bookVal, now, lossAcct)
				}
			}
		}

		// Kt: Fixed Asset (remove from books)
		lineNum++
		h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1,$2,$3,$4,'Fixed Asset',0,$5,1.0,$6)`, uuid.New(), jeID, lineNum, faAcct, acquisitionCost, now)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", acquisitionCost, now, faAcct)

		h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
	}()

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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get all active assets
	assetsQuery := `
		SELECT id, acquisition_cost, salvage_value, useful_life_months, depreciation_method,
			   accumulated_depreciation, COALESCE(current_value, acquisition_cost - accumulated_depreciation),
			   COALESCE(remaining_months, useful_life_months), COALESCE(monthly_depr, 0)
		FROM fixed_assets
		WHERE tenant_id = $1 AND status = 'active' AND deleted_at IS NULL
	`
	assetsArgs := []interface{}{tenantID}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
		assetsQuery += " AND organization_id = $2"
		assetsArgs = append(assetsArgs, orgID)
	}

	rows, err := h.db.Query(assetsQuery, assetsArgs...)
	if err != nil {
		h.log.Error("Failed to get assets for depreciation", "error", err)
		response.InternalError(c, "Failed to run depreciation")
		return
	}
	defer rows.Close()

	now := time.Now()
	// Depreciation is dated to the LAST DAY of the requested period, not today,
	// so a back-run (e.g. period "2025-12" processed in 2026) books the expense
	// into the correct accounting month. Accept both "2006-01" and "2006-1".
	deprecationDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if pt, perr := time.Parse("2006-01", input.Period); perr == nil {
		deprecationDate = pt.AddDate(0, 1, -1)
	} else if pt2, perr2 := time.Parse("2006-1", input.Period); perr2 == nil {
		deprecationDate = pt2.AddDate(0, 1, -1)
	}
	newEntries := make([]*entity.DepreciationEntryResponse, 0)

	// Look up accounts for journal entries: Dt 6500 Depr Expense / Kt 1510 Accum Depr
	deprExpenseAcct := findAccount(h.db, tenantID, orgIDPtr, "depreciation expense", "9470")
	accumDeprAcct := findAccount(h.db, tenantID, orgIDPtr, "accumulated depreciation", "0200")
	var journalID uuid.UUID
	var nextNumber int
	_ = h.db.QueryRow(`
		SELECT id, COALESCE(next_number, 1)
		FROM journals WHERE tenant_id = $1 AND code IN ('ASSET','MISC','GENERAL') AND deleted_at IS NULL
		ORDER BY CASE code WHEN 'ASSET' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
		tenantID).Scan(&journalID, &nextNumber)

	for rows.Next() {
		var assetID uuid.UUID
		var acquisitionCost, salvageValue, accumulatedDepreciation, curValue, monthlyDeprAmt float64
		var usefulLifeMonths, remMonths int
		var depreciationMethod string

		if err := rows.Scan(&assetID, &acquisitionCost, &salvageValue, &usefulLifeMonths,
			&depreciationMethod, &accumulatedDepreciation, &curValue, &remMonths, &monthlyDeprAmt); err != nil {
			h.log.Error("Failed to scan asset", "error", err)
			continue
		}

		// Skip if no remaining months
		if remMonths <= 0 {
			continue
		}

		// Check if depreciation already exists for this period
		var existingID uuid.UUID
		checkQuery := "SELECT id FROM depreciation_entries WHERE asset_id = $1 AND period = $2"
		if err := h.db.QueryRow(checkQuery, assetID, input.Period).Scan(&existingID); err == nil {
			continue
		}

		// Calculate depreciation using stored monthly_depr or recalculate
		depreciableAmount := acquisitionCost - salvageValue
		remainingValue := depreciableAmount - accumulatedDepreciation
		if remainingValue <= 0 {
			continue
		}

		// Compute by the asset's method. Declining-balance methods apply the
		// rate to net book value (cost − accumulated), not the depreciable base,
		// and must be recomputed each period — so they cannot use the stored
		// straight-line monthly_depr (that made every method behave straight-line).
		var depAmount float64
		nbv := acquisitionCost - accumulatedDepreciation
		switch depreciationMethod {
		case "declining_balance":
			depAmount = nbv * (1.0 / float64(usefulLifeMonths))
		case "double_declining":
			depAmount = nbv * (2.0 / float64(usefulLifeMonths))
		case "straight_line":
			depAmount = depreciableAmount / float64(usefulLifeMonths)
		default:
			if monthlyDeprAmt > 0 {
				depAmount = monthlyDeprAmt
			} else {
				depAmount = depreciableAmount / float64(usefulLifeMonths)
			}
		}

		// Cap depreciation at remaining value
		if depAmount > remainingValue {
			depAmount = remainingValue
		}

		newAccumulated := accumulatedDepreciation + depAmount
		newBookValue := acquisitionCost - newAccumulated
		newRemaining := remMonths - 1

		// Insert depreciation entry
		entryID := uuid.New()
		insertQuery := `
			INSERT INTO depreciation_entries (
				id, tenant_id, organization_id, asset_id, period, depreciation_date, depreciation_amount,
				accumulated_total, book_value_after, depreciation_method, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`
		if _, err := h.db.Exec(insertQuery, entryID, tenantID, orgIDPtr, assetID, input.Period, deprecationDate, depAmount, newAccumulated, newBookValue, depreciationMethod, now); err != nil {
			h.log.Error("Failed to insert depreciation entry", "error", err)
			continue
		}

		// Update asset: accumulated_depreciation, book_value, current_value, remaining_months, last_depr_date
		newCurrentValue := curValue - depAmount
		var newMonthlyDepr float64
		if newRemaining > 0 {
			newMonthlyDepr = newCurrentValue / float64(newRemaining)
		}

		updateQuery := `
			UPDATE fixed_assets SET
				accumulated_depreciation = $1, book_value = $2,
				current_value = $3, remaining_months = $4, monthly_depr = $5, last_depr_date = $6,
				updated_at = $7
			WHERE id = $8
		`
		if _, err := h.db.Exec(updateQuery, newAccumulated, newBookValue, newCurrentValue, newRemaining, newMonthlyDepr, deprecationDate, now, assetID); err != nil {
			h.log.Error("Failed to update asset depreciation", "error", err)
		}

		// Create journal entry: Dt Depreciation Expense / Kt Accumulated Depreciation.
		// Header + lines + balances in ONE tx (migration 416 rejects imbalanced
		// single-line autocommit inserts, leaving 0-line "posted" JEs otherwise).
		if deprExpenseAcct != uuid.Nil && accumDeprAcct != uuid.Nil && journalID != uuid.Nil {
			jeID := uuid.New()
			entryNumber := fmt.Sprintf("DEP%06d", nextNumber)
			nextNumber++

			if tx, txErr := h.db.Begin(); txErr != nil {
				h.log.Error("Failed to begin depreciation journal tx", "error", txErr)
			} else {
				committed := false
				func() {
					defer func() {
						if !committed {
							tx.Rollback()
						}
					}()
					if _, e := tx.Exec(`
						INSERT INTO journal_entries (
							id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
							source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'depreciation', $9, 1.0, $10, $10, 'posted', $11, $11)`,
						jeID, tenantID, orgIDPtr, journalID, entryNumber, deprecationDate,
						input.Period, fmt.Sprintf("Depreciation %s", input.Period),
						assetID.String(), depAmount, now); e != nil {
						h.log.Error("Failed to create depreciation journal entry", "error", e)
						return
					}
					if _, e := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
						VALUES ($1, $2, 1, $3, 'Depreciation Expense', $4, 0, 1.0, $5)`, uuid.New(), jeID, deprExpenseAcct, depAmount, now); e != nil {
						h.log.Error("Failed to insert depreciation expense line", "error", e)
						return
					}
					if _, e := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
						VALUES ($1, $2, 2, $3, 'Accumulated Depreciation', 0, $4, 1.0, $5)`, uuid.New(), jeID, accumDeprAcct, depAmount, now); e != nil {
						h.log.Error("Failed to insert accumulated depreciation line", "error", e)
						return
					}
					// Debit-positive convention (migration 407): expense (debit) +,
					// accumulated depreciation (credit-normal contra-asset) −.
					if _, e := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", depAmount, now, deprExpenseAcct); e != nil {
						h.log.Error("Failed to update depreciation expense balance", "error", e)
						return
					}
					if _, e := tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", depAmount, now, accumDeprAcct); e != nil {
						h.log.Error("Failed to update accumulated depreciation balance", "error", e)
						return
					}
					if _, e := tx.Exec("UPDATE depreciation_entries SET journal_entry_id = $1 WHERE id = $2", jeID, entryID); e != nil {
						h.log.Error("Failed to link depreciation journal", "error", e)
						return
					}
					if e := tx.Commit(); e != nil {
						h.log.Error("Failed to commit depreciation journal entry", "error", e)
					} else {
						committed = true
					}
				}()
			}
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

	// Update journal next_number
	if journalID != uuid.Nil {
		h.db.Exec("UPDATE journals SET next_number = $1, updated_at = $2 WHERE id = $3", nextNumber, now, journalID)
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

// RecordMaintenance records a maintenance event for a fixed asset
func (h *Handler) RecordMaintenance(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	assetIDStr := c.Param("id")
	assetID, err := uuid.Parse(assetIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	var input entity.RecordMaintenanceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	serviceDate, err := time.Parse("2006-01-02", input.ServiceDate)
	if err != nil {
		response.BadRequest(c, "Invalid service_date format")
		return
	}

	// Validate maintenance_type
	validTypes := map[string]bool{"regular_to": true, "capital_repair": true, "modernization": true, "minor_repair": true, "transfer": true}
	if !validTypes[input.MaintenanceType] {
		response.BadRequest(c, "Invalid maintenance_type. Must be: regular_to, capital_repair, modernization, minor_repair, transfer")
		return
	}

	// Get current asset state
	var curValue float64
	var remMonths, usefulLifeMonths int
	var monthlyDeprAmt float64
	err = h.db.QueryRow(`
		SELECT COALESCE(current_value, acquisition_cost - accumulated_depreciation),
			   COALESCE(remaining_months, useful_life_months),
			   useful_life_months,
			   COALESCE(monthly_depr, 0)
		FROM fixed_assets WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND status = 'active'`,
		assetID, tenantID).Scan(&curValue, &remMonths, &usefulLifeMonths, &monthlyDeprAmt)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Active fixed asset")
		return
	} else if err != nil {
		h.log.Error("Failed to get asset for maintenance", "error", err)
		response.InternalError(c, "Failed to get asset")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Build maintenance record
	m := &entity.AssetMaintenance{
		ID:                uuid.New(),
		TenantID:          tenantID,
		OrganizationID:    orgIDPtr,
		AssetID:           assetID,
		MaintenanceType:   input.MaintenanceType,
		ServiceDate:       serviceDate,
		Cost:              input.Cost,
		ValueBefore:       curValue,
		UsefulLifeBefore:  remMonths,
		MonthlyDeprBefore: monthlyDeprAmt,
		CreatedBy:         &userID,
	}

	if input.Description != "" {
		m.Description = &input.Description
	}
	if input.PerformedBy != "" {
		m.PerformedBy = &input.PerformedBy
	}
	if input.DocumentNumber != "" {
		m.DocumentNumber = &input.DocumentNumber
	}
	if input.NextServiceDate != "" {
		if nsd, err := time.Parse("2006-01-02", input.NextServiceDate); err == nil {
			m.NextServiceDate = &nsd
		}
	}

	// Calculate after-values based on maintenance type
	newValue := curValue
	newRemaining := remMonths
	newUsefulLife := usefulLifeMonths

	switch input.MaintenanceType {
	case "regular_to":
		// Muddat uzayadi — oylik amortizatsiya kamayadi
		m.LifeExtensionMonths = input.ExtensionMonths
		m.ValueIncrease = 0
		newRemaining += input.ExtensionMonths
		newUsefulLife += input.ExtensionMonths

	case "capital_repair":
		// Qiymat ortadi — oylik amortizatsiya ortadi
		m.LifeExtensionMonths = 0
		m.ValueIncrease = input.Cost
		newValue += input.Cost

	case "modernization":
		// Ikkalasi ham o'zgaradi
		m.LifeExtensionMonths = input.ExtensionMonths
		m.ValueIncrease = input.Cost
		newValue += input.Cost
		newRemaining += input.ExtensionMonths
		newUsefulLife += input.ExtensionMonths

	case "minor_repair", "transfer":
		// Hech narsa o'zgarmaydi — faqat xarajat
		m.LifeExtensionMonths = 0
		m.ValueIncrease = 0
	}

	// Calculate new monthly depreciation
	var newMonthly float64
	if newRemaining > 0 {
		newMonthly = newValue / float64(newRemaining)
	}

	m.ValueAfter = newValue
	m.UsefulLifeAfter = newRemaining
	m.MonthlyDeprAfter = math.Round(newMonthly*100) / 100

	now := time.Now()
	m.CreatedAt = now

	// Insert maintenance record
	_, err = h.db.Exec(`
		INSERT INTO asset_maintenance (
			id, tenant_id, organization_id, asset_id, maintenance_type, service_date, cost,
			value_before, useful_life_before, monthly_depr_before,
			value_after, useful_life_after, monthly_depr_after,
			life_extension_months, value_increase,
			description, performed_by, document_number, next_service_date, created_by, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		m.ID, m.TenantID, m.OrganizationID, m.AssetID, m.MaintenanceType, m.ServiceDate, m.Cost,
		m.ValueBefore, m.UsefulLifeBefore, m.MonthlyDeprBefore,
		m.ValueAfter, m.UsefulLifeAfter, m.MonthlyDeprAfter,
		m.LifeExtensionMonths, m.ValueIncrease,
		m.Description, m.PerformedBy, m.DocumentNumber, m.NextServiceDate, m.CreatedBy, m.CreatedAt,
	)
	if err != nil {
		h.log.Error("Failed to insert maintenance record", "error", err)
		response.InternalError(c, "Failed to record maintenance")
		return
	}

	// Update asset with new values (skip for minor_repair)
	if input.MaintenanceType != "minor_repair" && input.MaintenanceType != "transfer" {
		_, err = h.db.Exec(`
			UPDATE fixed_assets SET
				current_value = $1, remaining_months = $2, monthly_depr = $3,
				useful_life_months = $4, updated_at = $5
			WHERE id = $6`,
			newValue, newRemaining, m.MonthlyDeprAfter, newUsefulLife, now, assetID)
		if err != nil {
			h.log.Error("Failed to update asset after maintenance", "error", err)
		}
	}

	// Create journal entry for maintenance cost
	if input.Cost > 0 {
		func() {
			var journalID uuid.UUID
			var nextNum int
			if err := h.db.QueryRow(`
				SELECT id, COALESCE(next_number, 1)
				FROM journals WHERE tenant_id = $1 AND code IN ('ASSET','MISC','GENERAL') AND deleted_at IS NULL
				ORDER BY CASE code WHEN 'ASSET' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
				tenantID).Scan(&journalID, &nextNum); err != nil {
				return
			}

			// Payment account (cash/bank)
			payAcct := findAccount(h.db, tenantID, orgIDPtr, "cash", "5010")
			if input.PaymentAccountCode == "5110" {
				payAcct = findAccount(h.db, tenantID, orgIDPtr, "bank account", "5110")
			}
			if payAcct == uuid.Nil {
				return
			}

			var debitAcct uuid.UUID
			var debitDesc string
			switch input.MaintenanceType {
			case "capital_repair", "modernization":
				// Dt 1500 Fixed Assets (capitalized)
				debitAcct = findAccount(h.db, tenantID, orgIDPtr, "fixed assets", "0100")
				if debitAcct == uuid.Nil {
					debitAcct = findAccount(h.db, tenantID, orgIDPtr, "fixed asset", "0100")
				}
				debitDesc = "Capital Repair / Modernization"
			default:
				// Dt 6900 Misc Expense (or 7110)
				debitAcct = findAccount(h.db, tenantID, orgIDPtr, "repair", "9410")
				if debitAcct == uuid.Nil {
					debitAcct = findAccount(h.db, tenantID, orgIDPtr, "misc expense", "9620")
				}
				debitDesc = "Maintenance Expense"
			}
			if debitAcct == uuid.Nil {
				return
			}

			jeID := uuid.New()
			entryNumber := fmt.Sprintf("MNT%06d", nextNum)
			_, err := h.db.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'maintenance',$9,1.0,$10,$10,'posted',$11,$11)`,
				jeID, tenantID, orgIDPtr, journalID, entryNumber, serviceDate,
				m.DocumentNumber, fmt.Sprintf("Maintenance: %s", input.MaintenanceType),
				assetID.String(), input.Cost, now,
			)
			if err != nil {
				return
			}

			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1,$2,1,$3,$4,$5,0,1.0,$6)`, uuid.New(), jeID, debitAcct, debitDesc, input.Cost, now)
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1,$2,2,$3,'Cash/Bank',0,$4,1.0,$5)`, uuid.New(), jeID, payAcct, input.Cost, now)

			h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", input.Cost, now, debitAcct)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", input.Cost, now, payAcct)
			h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
		}()
	}

	response.Created(c, m.ToMaintenanceResponse())
}

// ListMaintenanceHistory returns maintenance history for a fixed asset
func (h *Handler) ListMaintenanceHistory(c *gin.Context) {
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

	rows, err := h.db.Query(`
		SELECT id, tenant_id, organization_id, asset_id, maintenance_type, service_date, cost,
			   value_before, useful_life_before, monthly_depr_before,
			   value_after, useful_life_after, monthly_depr_after,
			   life_extension_months, value_increase,
			   description, performed_by, document_number, created_by, created_at
		FROM asset_maintenance
		WHERE asset_id = $1 AND tenant_id = $2
		ORDER BY service_date DESC`, assetID, tenantID)
	if err != nil {
		h.log.Error("Failed to list maintenance history", "error", err)
		response.InternalError(c, "Failed to list maintenance history")
		return
	}
	defer rows.Close()

	records := make([]*entity.AssetMaintenanceResponse, 0)
	for rows.Next() {
		var m entity.AssetMaintenance
		var orgID, createdBy sql.NullString
		var description, performedBy, documentNumber sql.NullString

		if err := rows.Scan(
			&m.ID, &m.TenantID, &orgID, &m.AssetID, &m.MaintenanceType, &m.ServiceDate, &m.Cost,
			&m.ValueBefore, &m.UsefulLifeBefore, &m.MonthlyDeprBefore,
			&m.ValueAfter, &m.UsefulLifeAfter, &m.MonthlyDeprAfter,
			&m.LifeExtensionMonths, &m.ValueIncrease,
			&description, &performedBy, &documentNumber, &createdBy, &m.CreatedAt,
		); err != nil {
			h.log.Error("Failed to scan maintenance record", "error", err)
			continue
		}

		if description.Valid {
			m.Description = &description.String
		}
		if performedBy.Valid {
			m.PerformedBy = &performedBy.String
		}
		if documentNumber.Valid {
			m.DocumentNumber = &documentNumber.String
		}

		records = append(records, m.ToMaintenanceResponse())
	}

	response.Success(c, records)
}

// RecordAssetPayment records a credit installment payment for a fixed asset
func (h *Handler) RecordAssetPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	var input entity.RecordAssetPaymentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.Method != "cash" && input.Method != "bank" {
		response.BadRequest(c, "Method must be 'cash' or 'bank'")
		return
	}

	paidAt, err := time.Parse("2006-01-02", input.PaidAt)
	if err != nil {
		response.BadRequest(c, "Invalid paid_at format")
		return
	}

	// Verify asset exists and is credit
	var assetName, pmMethod string
	var acqCost, curTotalPaid float64
	err = h.db.QueryRow(`
		SELECT name, COALESCE(payment_method,'cash'), acquisition_cost, COALESCE(total_paid,0)
		FROM fixed_assets WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		assetID, tenantID).Scan(&assetName, &pmMethod, &acqCost, &curTotalPaid)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Fixed asset")
		return
	} else if err != nil {
		h.log.Error("Failed to get asset for payment", "error", err)
		response.InternalError(c, "Failed to get asset")
		return
	}
	if pmMethod != "credit" {
		response.BadRequest(c, "Asset is not purchased on credit")
		return
	}
	remaining := acqCost - curTotalPaid
	if input.Amount > remaining+0.01 {
		response.BadRequest(c, fmt.Sprintf("Payment amount %.2f exceeds remaining debt %.2f", input.Amount, remaining))
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	now := time.Now()
	paymentID := uuid.New()
	var note *string
	if input.Note != "" {
		note = &input.Note
	}

	// Journal entry: Dt 2000 AP / Kt 1000 or 1010 Cash/Bank
	var journalEntryID *uuid.UUID
	func() {
		var journalID uuid.UUID
		var nextNum int
		if err := h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('ASSET','MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE code WHEN 'ASSET' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNum); err != nil {
			return
		}

		apAcct := findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "6010")
		if apAcct == uuid.Nil {
			apAcct = findAccount(h.db, tenantID, orgIDPtr, "payable", "6010")
		}
		var cashAcct uuid.UUID
		if input.Method == "bank" {
			cashAcct = findAccount(h.db, tenantID, orgIDPtr, "bank account", "5110")
		} else {
			cashAcct = findAccount(h.db, tenantID, orgIDPtr, "cash", "5010")
		}
		if apAcct == uuid.Nil || cashAcct == uuid.Nil {
			return
		}

		jeID := uuid.New()
		journalEntryID = &jeID
		entryNumber := fmt.Sprintf("APY%06d", nextNum)
		desc := fmt.Sprintf("Asset Payment: %s", assetName)

		h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'asset_payment',$9,1.0,$10,$10,'posted',$11,$12,$12)`,
			jeID, tenantID, orgIDPtr, journalID, entryNumber, paidAt, assetID.String(), desc,
			paymentID.String(), input.Amount, userID, now,
		)
		h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1,$2,1,$3,'Accounts Payable',$4,0,1.0,$5)`, uuid.New(), jeID, apAcct, input.Amount, now)
		h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1,$2,2,$3,'Cash/Bank',0,$4,1.0,$5)`, uuid.New(), jeID, cashAcct, input.Amount, now)

		h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", input.Amount, now, apAcct)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", input.Amount, now, cashAcct)
		h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
	}()

	// Insert payment record
	_, err = h.db.Exec(`
		INSERT INTO asset_payments (id, tenant_id, organization_id, asset_id, amount, method, paid_at, note, journal_entry_id, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		paymentID, tenantID, orgIDPtr, assetID, input.Amount, input.Method, paidAt, note, journalEntryID, userID, now,
	)
	if err != nil {
		h.log.Error("Failed to insert asset payment", "error", err)
		response.InternalError(c, "Failed to record payment")
		return
	}

	// Update total_paid on asset
	h.db.Exec("UPDATE fixed_assets SET total_paid = COALESCE(total_paid,0) + $1, updated_at = $2 WHERE id = $3", input.Amount, now, assetID)

	payment := &entity.AssetPayment{
		ID:        paymentID,
		TenantID:  tenantID,
		AssetID:   assetID,
		Amount:    input.Amount,
		Method:    input.Method,
		PaidAt:    paidAt,
		Note:      note,
		CreatedAt: now,
	}
	response.Created(c, payment.ToPaymentResponse())
}

// ListAssetPayments returns payment history for a credit-purchased asset
func (h *Handler) ListAssetPayments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid asset ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, tenant_id, organization_id, asset_id, amount, method, paid_at, note, journal_entry_id, created_by, created_at
		FROM asset_payments
		WHERE asset_id = $1 AND tenant_id = $2
		ORDER BY paid_at DESC`, assetID, tenantID)
	if err != nil {
		h.log.Error("Failed to list asset payments", "error", err)
		response.InternalError(c, "Failed to list payments")
		return
	}
	defer rows.Close()

	payments := make([]*entity.AssetPaymentResponse, 0)
	for rows.Next() {
		var p entity.AssetPayment
		var orgID, journalEntryID, createdBy, noteStr sql.NullString

		if err := rows.Scan(&p.ID, &p.TenantID, &orgID, &p.AssetID, &p.Amount, &p.Method, &p.PaidAt, &noteStr, &journalEntryID, &createdBy, &p.CreatedAt); err != nil {
			h.log.Error("Failed to scan asset payment", "error", err)
			continue
		}
		if noteStr.Valid {
			p.Note = &noteStr.String
		}
		payments = append(payments, p.ToPaymentResponse())
	}

	response.Success(c, payments)
}

// CreateAssetCategory creates a new asset category
func (h *Handler) CreateAssetCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateAssetCategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	id := uuid.New()
	now := time.Now()

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	_, err := h.db.Exec(`
		INSERT INTO asset_categories (id, tenant_id, organization_id, code, name, description, depreciation_method, default_useful_life_months, is_active, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true,$9,$9)`,
		id, tenantID, orgIDPtr, input.Code, input.Name, description, input.DepreciationMethod, input.DefaultUsefulLifeMonths, now,
	)
	if err != nil {
		h.log.Error("Failed to create asset category", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Category with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create asset category")
		return
	}

	cat := &entity.AssetCategory{
		ID:                      id,
		TenantID:                tenantID,
		Code:                    input.Code,
		Name:                    input.Name,
		Description:             description,
		DepreciationMethod:      input.DepreciationMethod,
		DefaultUsefulLifeMonths: input.DefaultUsefulLifeMonths,
		IsActive:                true,
	}
	response.Created(c, cat.ToResponse())
}

// UpdateAssetCategory updates an existing asset category
func (h *Handler) UpdateAssetCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	catID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	var input entity.CreateAssetCategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	result, err := h.db.Exec(`
		UPDATE asset_categories SET name = $1, code = $2, description = $3, depreciation_method = $4,
			default_useful_life_months = $5, updated_at = $6
		WHERE id = $7 AND tenant_id = $8`,
		input.Name, input.Code, description, input.DepreciationMethod, input.DefaultUsefulLifeMonths, now, catID, tenantID)
	if err != nil {
		h.log.Error("Failed to update asset category", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Category with this code already exists")
			return
		}
		response.InternalError(c, "Failed to update asset category")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		response.NotFound(c, "Asset category")
		return
	}

	response.Success(c, gin.H{"message": "Category updated"})
}

// DeleteAssetCategory deletes (deactivates) an asset category
func (h *Handler) DeleteAssetCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	catID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	// Check if any active assets use this category
	var assetCount int
	h.db.QueryRow("SELECT COUNT(*) FROM fixed_assets WHERE category_id = $1 AND tenant_id = $2 AND status = 'active' AND deleted_at IS NULL", catID, tenantID).Scan(&assetCount)
	if assetCount > 0 {
		response.BadRequest(c, fmt.Sprintf("Cannot delete category: %d active assets are using it", assetCount))
		return
	}

	result, err := h.db.Exec("UPDATE asset_categories SET is_active = false, updated_at = $1 WHERE id = $2 AND tenant_id = $3", time.Now(), catID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete asset category", "error", err)
		response.InternalError(c, "Failed to delete asset category")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		response.NotFound(c, "Asset category")
		return
	}

	response.Success(c, gin.H{"message": "Category deleted"})
}

// GetAssetDashboard returns summary statistics for fixed assets
func (h *Handler) GetAssetDashboard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	args := []interface{}{tenantID}
	orgFilter := ""
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgFilter = " AND organization_id = $2"
		args = append(args, orgID)
	}

	var totalAssets, activeAssets, disposedAssets int
	var totalAcquisitionCost, totalCurrentValue, totalAccumDepr float64
	var creditAssetsDebt float64

	h.db.QueryRow(fmt.Sprintf(`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'active'),
			COUNT(*) FILTER (WHERE status = 'disposed'),
			COALESCE(SUM(acquisition_cost),0),
			COALESCE(SUM(CASE WHEN status='active' THEN COALESCE(current_value, acquisition_cost - accumulated_depreciation) ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='active' THEN accumulated_depreciation ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN payment_method='credit' AND status='active' THEN acquisition_cost - COALESCE(total_paid,0) ELSE 0 END),0)
		FROM fixed_assets WHERE tenant_id = $1 AND deleted_at IS NULL%s`, orgFilter),
		args...).Scan(&totalAssets, &activeAssets, &disposedAssets, &totalAcquisitionCost, &totalCurrentValue, &totalAccumDepr, &creditAssetsDebt)

	// This month's total depreciation (sum of monthly_depr for active assets)
	var thisMonthDepr float64
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COALESCE(SUM(COALESCE(monthly_depr, 0)), 0)
		FROM fixed_assets WHERE tenant_id = $1 AND status = 'active' AND deleted_at IS NULL AND remaining_months > 0%s`, orgFilter),
		args...).Scan(&thisMonthDepr)

	response.Success(c, gin.H{
		"total_assets":             totalAssets,
		"active_assets":            activeAssets,
		"disposed_assets":          disposedAssets,
		"total_acquisition_cost":   totalAcquisitionCost,
		"total_current_value":      totalCurrentValue,
		"total_accum_depreciation": totalAccumDepr,
		"credit_remaining_debt":    creditAssetsDebt,
		"this_month_depreciation":  thisMonthDepr,
	})
}
