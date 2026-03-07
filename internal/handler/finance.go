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
	"github.com/lib/pq"
)

// nullIfEmpty returns nil for empty strings, otherwise returns the string pointer
// This is used for optional nullable database columns
func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// =====================================================
// ACCOUNT TYPE HANDLERS
// =====================================================

// ListAccountTypes returns all account types
// ListAccountTypes godoc
// @Summary List all account types
// @Description Get a list of all available account types
// @Tags Finance - Accounts
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/account-types [get]
func (h *Handler) ListAccountTypes(c *gin.Context) {
	query := `
		SELECT id, code, name, category, normal_balance, COALESCE(is_system, true)
		FROM account_types
		ORDER BY code ASC
	`

	rows, err := h.db.Query(query)
	if err != nil {
		h.log.Error("Failed to list account types", "error", err)
		response.InternalError(c, "Failed to list account types")
		return
	}
	defer rows.Close()

	type AccountTypeResponse struct {
		ID            uuid.UUID `json:"id"`
		Code          string    `json:"code"`
		Name          string    `json:"name"`
		Category      string    `json:"category"`
		NormalBalance string    `json:"normal_balance"`
		IsSystem      bool      `json:"is_system"`
	}

	types := make([]AccountTypeResponse, 0)
	for rows.Next() {
		var at AccountTypeResponse
		err := rows.Scan(&at.ID, &at.Code, &at.Name, &at.Category, &at.NormalBalance, &at.IsSystem)
		if err != nil {
			h.log.Error("Failed to scan account type", "error", err)
			continue
		}
		types = append(types, at)
	}

	response.Success(c, types)
}

// =====================================================
// ACCOUNT HANDLERS (CHART OF ACCOUNTS)
// =====================================================

// ListAccounts returns a paginated list of accounts
// ListAccounts godoc
// @Summary List all chart of accounts
// @Description Get a paginated list of all accounts in the chart of accounts
// @Tags Finance - Accounts
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param account_type query string false "Filter by account type"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/accounts [get]
func (h *Handler) ListAccounts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	category := c.Query("category")
	accountTypeID := c.Query("account_type_id")
	organizationID := c.Query("organization_id")
	includeInactive := c.Query("include_inactive") == "true"
	_ = c.Query("flat") // Reserved for future hierarchical view

	// Build query
	baseQuery := `
		SELECT a.id, a.tenant_id, a.organization_id, a.parent_id, a.account_type_id,
			   a.code, a.name, a.name_uz, a.name_en, a.description, a.currency_id, a.is_bank_account,
			   a.is_control_account, a.is_reconcilable,
			   COALESCE(a.budget_tracking, false) as budget_tracking,
			   a.current_balance, a.opening_balance, a.is_active,
			   a.created_at, a.updated_at,
			   at.code as type_code, at.name as type_name, at.category, at.normal_balance
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM accounts a JOIN account_types at ON a.account_type_id = at.id WHERE a.tenant_id = $1 AND a.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization - prefer middleware header, fallback to query param
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND a.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND a.organization_id = $%d", argCount)
		args = append(args, orgID)
	} else if organizationID != "" {
		orgID, err := uuid.Parse(organizationID)
		if err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND a.organization_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND a.organization_id = $%d", argCount)
			args = append(args, orgID)
		}
	}

	if !includeInactive {
		baseQuery += " AND a.is_active = true"
		countQuery += " AND a.is_active = true"
	}

	if category != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND at.category = $%d", argCount)
		countQuery += fmt.Sprintf(" AND at.category = $%d", argCount)
		args = append(args, category)
	}

	if accountTypeID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND a.account_type_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND a.account_type_id = $%d", argCount)
		args = append(args, accountTypeID)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (a.code ILIKE $%d OR a.name ILIKE $%d OR a.name_uz ILIKE $%d)", argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count accounts", "error", err)
		response.InternalError(c, "Failed to count accounts")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY a.code ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list accounts", "error", err)
		response.InternalError(c, "Failed to list accounts")
		return
	}
	defer rows.Close()

	accounts := make([]*entity.AccountResponse, 0)
	for rows.Next() {
		var acc entity.Account
		var orgID, parentID, currencyID, description, nameUz, nameEn sql.NullString
		var typeCode, typeName, typeCategory, normalBalance string

		err := rows.Scan(
			&acc.ID, &acc.TenantID, &orgID, &parentID, &acc.AccountTypeID,
			&acc.Code, &acc.Name, &nameUz, &nameEn, &description, &currencyID, &acc.IsBankAccount,
			&acc.IsControlAccount, &acc.IsReconcilable, &acc.BudgetTracking,
			&acc.CurrentBalance, &acc.OpeningBalance, &acc.IsActive,
			&acc.CreatedAt, &acc.UpdatedAt,
			&typeCode, &typeName, &typeCategory, &normalBalance,
		)
		if err != nil {
			h.log.Error("Failed to scan account", "error", err)
			continue
		}

		if parentID.Valid {
			pid, _ := uuid.Parse(parentID.String)
			acc.ParentID = &pid
		}
		if description.Valid {
			acc.Description = &description.String
		}
		if nameUz.Valid {
			acc.NameUz = &nameUz.String
		}
		if nameEn.Valid {
			acc.NameEn = &nameEn.String
		}

		acc.AccountType = &entity.AccountType{
			Code:          typeCode,
			Name:          typeName,
			Category:      typeCategory,
			NormalBalance: normalBalance,
		}

		accounts = append(accounts, acc.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, accounts, pagination)
}

// CreateAccount creates a new account
// CreateAccount godoc
// @Summary Create a new account
// @Description Create a new account in the chart of accounts
// @Tags Finance - Accounts
// @Accept json
// @Produce json
// @Param account body entity.CreateAccountInput true "Account details"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/accounts [post]
func (h *Handler) CreateAccount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateAccountInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Get organization ID
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Validate account type exists
	accountTypeID, err := uuid.Parse(input.AccountTypeID)
	if err != nil {
		response.BadRequest(c, "Invalid account type ID")
		return
	}

	var typeExists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM account_types WHERE id = $1)", accountTypeID).Scan(&typeExists)
	if err != nil || !typeExists {
		response.BadRequest(c, "Account type not found")
		return
	}

	// Check for duplicate code (org-scoped)
	var codeExists bool
	err = h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND code = $3 AND deleted_at IS NULL)",
		tenantID, orgIDPtr, input.Code,
	).Scan(&codeExists)
	if err != nil {
		h.log.Error("Failed to check account code", "error", err)
		response.InternalError(c, "Failed to create account")
		return
	}
	if codeExists {
		response.Conflict(c, "Account with this code already exists")
		return
	}

	// Parse parent ID if provided
	var parentID *uuid.UUID
	if input.ParentID != nil && *input.ParentID != "" {
		pid, err := uuid.Parse(*input.ParentID)
		if err != nil {
			response.BadRequest(c, "Invalid parent account ID")
			return
		}
		parentID = &pid
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO accounts (
			id, tenant_id, organization_id, parent_id, account_type_id, code, name, description,
			is_bank_account, is_control_account, is_reconcilable, budget_tracking,
			opening_balance, current_balance, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id
	`

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	err = h.db.QueryRow(query,
		id, tenantID, orgIDPtr, parentID, accountTypeID, input.Code, input.Name, description,
		input.IsBankAccount, input.IsControlAccount, input.IsReconcilable, input.BudgetTracking,
		input.OpeningBalance, input.OpeningBalance, true, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create account", "error", err)
		response.InternalError(c, "Failed to create account")
		return
	}

	// Fetch the created account
	acc := &entity.Account{
		ID:               id,
		TenantID:         tenantID,
		ParentID:         parentID,
		AccountTypeID:    accountTypeID,
		Code:             input.Code,
		Name:             input.Name,
		Description:      description,
		IsBankAccount:    input.IsBankAccount,
		IsControlAccount: input.IsControlAccount,
		IsReconcilable:   input.IsReconcilable,
		BudgetTracking:   input.BudgetTracking,
		OpeningBalance:   input.OpeningBalance,
		CurrentBalance:   input.OpeningBalance,
		IsActive:         true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	response.Created(c, acc.ToResponse())
}

// GetNextAccountCode godoc
// @Summary Get next available account code
// @Description Returns the next available account code for a given account type
// @Tags Finance - Accounts
// @Accept json
// @Produce json
// @Param account_type_id query string true "Account Type ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Security BearerAuth
// @Router /finance/accounts/next-code [get]
func (h *Handler) GetNextAccountCode(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	accountTypeID := c.Query("account_type_id")
	if accountTypeID == "" {
		response.BadRequest(c, "account_type_id is required")
		return
	}

	// Get category from account_type
	var category string
	err := h.db.QueryRow("SELECT category FROM account_types WHERE id = $1", accountTypeID).Scan(&category)
	if err != nil {
		response.BadRequest(c, "Invalid account type")
		return
	}

	// Map category to code range
	var rangeStart, rangeEnd int
	switch category {
	case "asset":
		rangeStart, rangeEnd = 1000, 1999
	case "liability":
		rangeStart, rangeEnd = 2000, 2999
	case "equity":
		rangeStart, rangeEnd = 3000, 3999
	case "revenue":
		rangeStart, rangeEnd = 4000, 4999
	case "expense":
		rangeStart, rangeEnd = 5000, 6999
	default:
		rangeStart, rangeEnd = 1000, 9999
	}

	// Find max numeric code in range
	var maxCode sql.NullInt64
	h.db.QueryRow(`
		SELECT MAX(CAST(code AS INTEGER))
		FROM accounts
		WHERE tenant_id = $1 AND organization_id IS NOT DISTINCT FROM $2
			AND code ~ '^\d+$'
			AND CAST(code AS INTEGER) BETWEEN $3 AND $4
			AND deleted_at IS NULL
	`, tenantID, orgIDPtr, rangeStart, rangeEnd).Scan(&maxCode)

	nextCode := rangeStart
	if maxCode.Valid {
		nextCode = int(maxCode.Int64) + 10 // Odoo-style: increment by 10
		if nextCode > rangeEnd {
			nextCode = int(maxCode.Int64) + 1
		}
	}

	response.Success(c, gin.H{"code": fmt.Sprintf("%d", nextCode)})
}

// GetAccount godoc
// @Summary Get account by ID
// @Description Get detailed information about a specific account
// @Tags Finance - Accounts
// @Accept json
// @Produce json
// @Param id path string true "Account ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/accounts/{id} [get]
func (h *Handler) GetAccount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	query := `
		SELECT a.id, a.tenant_id, a.organization_id, a.parent_id, a.account_type_id,
			   a.code, a.name, a.name_uz, a.name_en, a.description, a.currency_id, a.is_bank_account,
			   a.is_control_account, a.is_reconcilable,
			   COALESCE(a.budget_tracking, false) as budget_tracking,
			   a.current_balance, a.opening_balance, a.is_active,
			   a.created_at, a.updated_at,
			   at.code as type_code, at.name as type_name, at.category, at.normal_balance
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		WHERE a.id = $1 AND a.tenant_id = $2 AND a.deleted_at IS NULL
	`

	var acc entity.Account
	var orgID, parentID, currencyID, description, nameUz, nameEn sql.NullString
	var typeCode, typeName, typeCategory, normalBalance string

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&acc.ID, &acc.TenantID, &orgID, &parentID, &acc.AccountTypeID,
		&acc.Code, &acc.Name, &nameUz, &nameEn, &description, &currencyID, &acc.IsBankAccount,
		&acc.IsControlAccount, &acc.IsReconcilable, &acc.BudgetTracking,
		&acc.CurrentBalance, &acc.OpeningBalance, &acc.IsActive,
		&acc.CreatedAt, &acc.UpdatedAt,
		&typeCode, &typeName, &typeCategory, &normalBalance,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Account")
		return
	}
	if err != nil {
		h.log.Error("Failed to get account", "error", err)
		response.InternalError(c, "Failed to get account")
		return
	}

	if parentID.Valid {
		pid, _ := uuid.Parse(parentID.String)
		acc.ParentID = &pid
	}
	if description.Valid {
		acc.Description = &description.String
	}
	if nameUz.Valid {
		acc.NameUz = &nameUz.String
	}
	if nameEn.Valid {
		acc.NameEn = &nameEn.String
	}

	acc.AccountType = &entity.AccountType{
		Code:          typeCode,
		Name:          typeName,
		Category:      typeCategory,
		NormalBalance: normalBalance,
	}

	response.Success(c, acc.ToResponse())
}

// UpdateAccount godoc
// @Summary Update an account
// @Description Update an existing account's information
// @Tags Finance - Accounts
// @Accept json
// @Produce json
// @Param id path string true "Account ID"
// @Param body body entity.UpdateAccountInput true "Account update data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/accounts/{id} [put]
func (h *Handler) UpdateAccount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var input entity.UpdateAccountInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build dynamic update
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
	if input.IsBankAccount != nil {
		addUpdate("is_bank_account", *input.IsBankAccount)
	}
	if input.IsReconcilable != nil {
		addUpdate("is_reconcilable", *input.IsReconcilable)
	}
	if input.IsControlAccount != nil {
		addUpdate("is_control_account", *input.IsControlAccount)
	}
	if input.BudgetTracking != nil {
		addUpdate("budget_tracking", *input.BudgetTracking)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
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
		UPDATE accounts SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Account")
		return
	}
	if err != nil {
		h.log.Error("Failed to update account", "error", err)
		response.InternalError(c, "Failed to update account")
		return
	}

	// Return updated account
	h.GetAccount(c)
}

// DeleteAccount godoc
// @Summary Delete an account
// @Description Soft-delete an account from the chart of accounts
// @Tags Finance - Accounts
// @Accept json
// @Produce json
// @Param id path string true "Account ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/accounts/{id} [delete]
func (h *Handler) DeleteAccount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	// Check if account has transactions
	var hasTransactions bool
	err = h.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM journal_entry_lines jel
			JOIN journal_entries je ON jel.journal_entry_id = je.id
			WHERE jel.account_id = $1 AND je.deleted_at IS NULL
		)
	`, id).Scan(&hasTransactions)
	if err != nil {
		h.log.Error("Failed to check account transactions", "error", err)
		response.InternalError(c, "Failed to delete account")
		return
	}
	if hasTransactions {
		response.BadRequest(c, "Cannot delete account with existing transactions")
		return
	}

	query := `
		UPDATE accounts SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete account", "error", err)
		response.InternalError(c, "Failed to delete account")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Account")
		return
	}

	response.NoContent(c)
}

// GetAccountTransactions godoc
// @Summary Get account transactions
// @Description Get a paginated list of all transactions for a specific account
// @Tags Finance - Accounts
// @Accept json
// @Produce json
// @Param id path string true "Account ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param start_date query string false "Start date filter (YYYY-MM-DD)"
// @Param end_date query string false "End date filter (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/accounts/{id}/transactions [get]
func (h *Handler) GetAccountTransactions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	accountID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Parse date filters
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// Build query
	baseQuery := `
		SELECT je.id, je.entry_number, je.entry_date, je.reference, je.description, je.status,
			   jel.line_number, jel.description as line_description,
			   jel.debit_amount, jel.credit_amount
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE jel.account_id = $1 AND je.tenant_id = $2 AND je.deleted_at IS NULL
	`
	countQuery := `
		SELECT COUNT(*) FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE jel.account_id = $1 AND je.tenant_id = $2 AND je.deleted_at IS NULL
	`

	args := []interface{}{accountID, tenantID}
	argCount := 2

	if dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND je.entry_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND je.entry_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND je.entry_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND je.entry_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Get count
	var total int
	err = h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count transactions", "error", err)
		response.InternalError(c, "Failed to get transactions")
		return
	}

	baseQuery += " ORDER BY je.entry_date DESC, je.entry_number DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list transactions", "error", err)
		response.InternalError(c, "Failed to get transactions")
		return
	}
	defer rows.Close()

	type Transaction struct {
		JournalEntryID  uuid.UUID `json:"journal_entry_id"`
		EntryNumber     string    `json:"entry_number"`
		EntryDate       time.Time `json:"entry_date"`
		Reference       *string   `json:"reference,omitempty"`
		Description     *string   `json:"description,omitempty"`
		Status          string    `json:"status"`
		LineNumber      int       `json:"line_number"`
		LineDescription *string   `json:"line_description,omitempty"`
		DebitAmount     float64   `json:"debit_amount"`
		CreditAmount    float64   `json:"credit_amount"`
	}

	transactions := make([]Transaction, 0)
	for rows.Next() {
		var t Transaction
		var ref, desc, lineDesc sql.NullString
		err := rows.Scan(&t.JournalEntryID, &t.EntryNumber, &t.EntryDate, &ref, &desc, &t.Status,
			&t.LineNumber, &lineDesc, &t.DebitAmount, &t.CreditAmount)
		if err != nil {
			continue
		}
		if ref.Valid {
			t.Reference = &ref.String
		}
		if desc.Valid {
			t.Description = &desc.String
		}
		if lineDesc.Valid {
			t.LineDescription = &lineDesc.String
		}
		transactions = append(transactions, t)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, transactions, pagination)
}

// =====================================================
// JOURNAL ENTRY HANDLERS
// =====================================================

// ListJournalEntries godoc
// @Summary List journal entries
// @Description Get a paginated list of all journal entries
// @Tags Finance - Journal Entries
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param status query string false "Filter by status (draft, posted, reversed)"
// @Param start_date query string false "Start date filter (YYYY-MM-DD)"
// @Param end_date query string false "End date filter (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journal-entries [get]
func (h *Handler) ListJournalEntries(c *gin.Context) {
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
	journalID := c.Query("journal_id")
	status := c.Query("status")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// Build query
	baseQuery := `
		SELECT je.id, je.tenant_id, je.journal_id, je.entry_number, je.entry_date,
			   je.reference, je.description, je.source_type, je.total_debit, je.total_credit,
			   je.status, je.posted_at, je.created_at, je.updated_at,
			   j.code as journal_code, j.name as journal_name
		FROM journal_entries je
		JOIN journals j ON je.journal_id = j.id
		WHERE je.tenant_id = $1 AND je.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM journal_entries je WHERE je.tenant_id = $1 AND je.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND je.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND je.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if journalID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND je.journal_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND je.journal_id = $%d", argCount)
		args = append(args, journalID)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND je.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND je.status = $%d", argCount)
		args = append(args, status)
	}

	if dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND je.entry_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND je.entry_date >= $%d", argCount)
		args = append(args, dateFrom)
	}

	if dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND je.entry_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND je.entry_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (je.entry_number ILIKE $%d OR je.reference ILIKE $%d OR je.description ILIKE $%d)", argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count journal entries", "error", err)
		response.InternalError(c, "Failed to list journal entries")
		return
	}

	baseQuery += " ORDER BY je.entry_date DESC, je.entry_number DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list journal entries", "error", err)
		response.InternalError(c, "Failed to list journal entries")
		return
	}
	defer rows.Close()

	entries := make([]*entity.JournalEntryResponse, 0)
	for rows.Next() {
		var je entity.JournalEntry
		var ref, desc, sourceType sql.NullString
		var postedAt sql.NullTime
		var journalCode, journalName string

		err := rows.Scan(
			&je.ID, &je.TenantID, &je.JournalID, &je.EntryNumber, &je.EntryDate,
			&ref, &desc, &sourceType, &je.TotalDebit, &je.TotalCredit,
			&je.Status, &postedAt, &je.CreatedAt, &je.UpdatedAt,
			&journalCode, &journalName,
		)
		if err != nil {
			h.log.Error("Failed to scan journal entry", "error", err)
			continue
		}

		if ref.Valid {
			je.Reference = &ref.String
		}
		if desc.Valid {
			je.Description = &desc.String
		}
		if sourceType.Valid {
			je.SourceType = &sourceType.String
		}
		if postedAt.Valid {
			je.PostedAt = &postedAt.Time
		}

		je.Journal = &entity.Journal{
			Code: journalCode,
			Name: journalName,
		}

		entries = append(entries, je.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, entries, pagination)
}

// ListJournals godoc
// @Summary List all journals
// @Description Get a list of all journals for the tenant
// @Tags Finance - Journals
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journals [get]
func (h *Handler) ListJournals(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT j.id, j.code, j.name, j.type,
			COALESCE(j.description, ''),
			j.default_debit_account_id,
			j.default_credit_account_id,
			COALESCE(j.auto_sequence, false),
			COALESCE(j.next_number, 1),
			COALESCE(j.number_prefix, ''),
			COALESCE(j.short_code, ''),
			COALESCE(j.currency, ''),
			j.bank_account_id,
			j.suspense_account_id,
			j.profit_account_id,
			j.loss_account_id,
			COALESCE(j.is_active, true),
			j.created_at,
			COALESCE(j.updated_at, j.created_at)
		FROM journals j
		WHERE j.tenant_id = $1 AND j.deleted_at IS NULL
		ORDER BY j.code ASC
	`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization (also include journals with NULL organization_id as they belong to all orgs in the tenant)
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query = strings.Replace(query, "ORDER BY", fmt.Sprintf("AND (j.organization_id = $%d OR j.organization_id IS NULL) ORDER BY", argCount), 1)
		args = append(args, orgID)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query journals", "error", err)
		response.InternalError(c, "Failed to fetch journals")
		return
	}
	defer rows.Close()

	type JournalResponse struct {
		ID                     uuid.UUID  `json:"id"`
		Code                   string     `json:"code"`
		Name                   string     `json:"name"`
		Type                   string     `json:"type"`
		Description            string     `json:"description,omitempty"`
		DefaultDebitAccountID  *uuid.UUID `json:"default_debit_account_id,omitempty"`
		DefaultCreditAccountID *uuid.UUID `json:"default_credit_account_id,omitempty"`
		AutoSequence           bool       `json:"auto_sequence"`
		NextNumber             int        `json:"next_number"`
		NumberPrefix           string     `json:"number_prefix,omitempty"`
		ShortCode              string     `json:"short_code"`
		Currency               string     `json:"currency"`
		BankAccountID          *uuid.UUID `json:"bank_account_id,omitempty"`
		SuspenseAccountID      *uuid.UUID `json:"suspense_account_id,omitempty"`
		ProfitAccountID        *uuid.UUID `json:"profit_account_id,omitempty"`
		LossAccountID          *uuid.UUID `json:"loss_account_id,omitempty"`
		IsActive               bool       `json:"is_active"`
		CreatedAt              time.Time  `json:"created_at"`
		UpdatedAt              time.Time  `json:"updated_at"`
	}

	journals := make([]JournalResponse, 0)
	for rows.Next() {
		var j JournalResponse
		if err := rows.Scan(&j.ID, &j.Code, &j.Name, &j.Type, &j.Description,
			&j.DefaultDebitAccountID, &j.DefaultCreditAccountID,
			&j.AutoSequence, &j.NextNumber, &j.NumberPrefix,
			&j.ShortCode, &j.Currency,
			&j.BankAccountID, &j.SuspenseAccountID, &j.ProfitAccountID, &j.LossAccountID,
			&j.IsActive, &j.CreatedAt, &j.UpdatedAt); err != nil {
			h.log.Error("Failed to scan journal", "error", err)
			continue
		}
		journals = append(journals, j)
	}

	response.Success(c, journals)
}

// GetJournal godoc
// @Summary Get journal by ID
// @Description Get detailed information about a specific journal
// @Tags Finance - Journals
// @Accept json
// @Produce json
// @Param id path string true "Journal ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journals/{id} [get]
func (h *Handler) GetJournal(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal ID")
		return
	}

	var j struct {
		ID                     uuid.UUID  `json:"id"`
		Code                   string     `json:"code"`
		Name                   string     `json:"name"`
		Type                   string     `json:"type"`
		Description            string     `json:"description"`
		DefaultDebitAccountID  *uuid.UUID `json:"default_debit_account_id,omitempty"`
		DefaultCreditAccountID *uuid.UUID `json:"default_credit_account_id,omitempty"`
		AutoSequence           bool       `json:"auto_sequence"`
		NextNumber             int        `json:"next_number"`
		NumberPrefix           string     `json:"number_prefix"`
		ShortCode              string     `json:"short_code"`
		Currency               string     `json:"currency"`
		BankAccountID          *uuid.UUID `json:"bank_account_id,omitempty"`
		SuspenseAccountID      *uuid.UUID `json:"suspense_account_id,omitempty"`
		ProfitAccountID        *uuid.UUID `json:"profit_account_id,omitempty"`
		LossAccountID          *uuid.UUID `json:"loss_account_id,omitempty"`
		IsActive               bool       `json:"is_active"`
		CreatedAt              time.Time  `json:"created_at"`
		UpdatedAt              time.Time  `json:"updated_at"`
		// Enriched names from JOINs
		BankAccountName     string `json:"bank_account_name"`
		SuspenseAccountName string `json:"suspense_account_name"`
		ProfitAccountName   string `json:"profit_account_name"`
		LossAccountName     string `json:"loss_account_name"`
		DebitAccountName    string `json:"debit_account_name"`
		CreditAccountName   string `json:"credit_account_name"`
		EntryCount          int    `json:"entry_count"`
	}

	err = h.db.QueryRow(`
		SELECT j.id, j.code, j.name, j.type,
			COALESCE(j.description, ''),
			j.default_debit_account_id,
			j.default_credit_account_id,
			COALESCE(j.auto_sequence, false),
			COALESCE(j.next_number, 1),
			COALESCE(j.number_prefix, ''),
			COALESCE(j.short_code, ''),
			COALESCE(j.currency, ''),
			j.bank_account_id,
			j.suspense_account_id,
			j.profit_account_id,
			j.loss_account_id,
			COALESCE(j.is_active, true),
			j.created_at,
			COALESCE(j.updated_at, j.created_at),
			COALESCE(ba.bank_name || ' - ' || ba.account_number, ''),
			COALESCE(sa.code || ' ' || sa.name, ''),
			COALESCE(pa.code || ' ' || pa.name, ''),
			COALESCE(la.code || ' ' || la.name, ''),
			COALESCE(da.code || ' ' || da.name, ''),
			COALESCE(ca.code || ' ' || ca.name, ''),
			(SELECT COUNT(*) FROM journal_entries WHERE journal_id = j.id AND deleted_at IS NULL)
		FROM journals j
		LEFT JOIN bank_accounts ba ON j.bank_account_id = ba.id
		LEFT JOIN accounts sa ON j.suspense_account_id = sa.id
		LEFT JOIN accounts pa ON j.profit_account_id = pa.id
		LEFT JOIN accounts la ON j.loss_account_id = la.id
		LEFT JOIN accounts da ON j.default_debit_account_id = da.id
		LEFT JOIN accounts ca ON j.default_credit_account_id = ca.id
		WHERE j.id = $1 AND j.tenant_id = $2 AND j.deleted_at IS NULL
	`, id, tenantID).Scan(&j.ID, &j.Code, &j.Name, &j.Type, &j.Description,
		&j.DefaultDebitAccountID, &j.DefaultCreditAccountID,
		&j.AutoSequence, &j.NextNumber, &j.NumberPrefix,
		&j.ShortCode, &j.Currency,
		&j.BankAccountID, &j.SuspenseAccountID, &j.ProfitAccountID, &j.LossAccountID,
		&j.IsActive, &j.CreatedAt, &j.UpdatedAt,
		&j.BankAccountName, &j.SuspenseAccountName, &j.ProfitAccountName, &j.LossAccountName,
		&j.DebitAccountName, &j.CreditAccountName,
		&j.EntryCount)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Journal")
		return
	}
	if err != nil {
		h.log.Error("Failed to get journal", "error", err)
		response.InternalError(c, "Failed to fetch journal")
		return
	}

	response.Success(c, j)
}

// CreateJournal godoc
// @Summary Create a new journal
// @Description Create a new journal for organizing entries
// @Tags Finance - Journals
// @Accept json
// @Produce json
// @Param body body entity.CreateJournalInput true "Journal creation data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journals [post]
func (h *Handler) CreateJournal(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateJournalInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Auto-generate code from name if empty
	if strings.TrimSpace(input.Code) == "" && strings.TrimSpace(input.Name) != "" {
		input.Code = strings.ToUpper(strings.TrimSpace(input.Name))
		// Keep only A-Z, 0-9, spaces; replace spaces with underscore
		var cleaned []rune
		for _, r := range input.Code {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' {
				cleaned = append(cleaned, r)
			}
		}
		input.Code = strings.ReplaceAll(strings.TrimSpace(string(cleaned)), " ", "_")
		if len(input.Code) > 20 {
			input.Code = input.Code[:20]
		}
		if input.Code == "" {
			input.Code = "JRN"
		}
	}

	// Check for duplicate code and auto-suffix if needed
	var err error
	baseCode := input.Code
	for attempt := 0; attempt < 100; attempt++ {
		var exists bool
		err = h.db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM journals WHERE tenant_id = $1 AND code = $2)
		`, tenantID, input.Code).Scan(&exists)
		if err != nil {
			h.log.Error("Failed to check journal code", "error", err)
			response.InternalError(c, "Failed to create journal")
			return
		}
		if !exists {
			break
		}
		suffix := fmt.Sprintf("_%d", attempt+1)
		maxBase := 20 - len(suffix)
		if maxBase < 0 {
			maxBase = 0
		}
		bc := baseCode
		if len(bc) > maxBase {
			bc = bc[:maxBase]
		}
		input.Code = bc + suffix
	}

	journalID := uuid.New()
	now := time.Now()

	nullIfEmpty := func(s *string) interface{} {
		if s == nil || *s == "" {
			return nil
		}
		return *s
	}

	var debitAccID, creditAccID interface{}
	if input.DefaultDebitAccountID != nil && *input.DefaultDebitAccountID != "" {
		debitAccID = *input.DefaultDebitAccountID
	}
	if input.DefaultCreditAccountID != nil && *input.DefaultCreditAccountID != "" {
		creditAccID = *input.DefaultCreditAccountID
	}

	// Get organization ID from context
	var orgID interface{}
	if oid, oidOk := middleware.GetOrganizationID(c); oidOk && oid != uuid.Nil {
		orgID = oid
	}

	shortCode := input.ShortCode
	if shortCode == "" && len(input.Code) >= 3 {
		shortCode = input.Code[:3]
	}

	_, err = h.db.Exec(`
		INSERT INTO journals (id, tenant_id, code, name, type, description,
			default_debit_account_id, default_credit_account_id,
			auto_sequence, next_number, number_prefix,
			short_code, currency,
			bank_account_id, suspense_account_id, profit_account_id, loss_account_id,
			organization_id, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10, $11, $12, $13, $14, $15, $16, $17, true, $18, $18)
	`, journalID, tenantID, input.Code, input.Name, input.Type, input.Description,
		debitAccID, creditAccID,
		input.AutoSequence, input.NumberPrefix,
		shortCode, input.Currency,
		nullIfEmpty(input.BankAccountID), nullIfEmpty(input.SuspenseAccountID),
		nullIfEmpty(input.ProfitAccountID), nullIfEmpty(input.LossAccountID),
		orgID, now)

	if err != nil {
		h.log.Error("Failed to create journal", "error", err)
		response.InternalError(c, "Failed to create journal")
		return
	}

	c.Params = append(c.Params, gin.Param{Key: "id", Value: journalID.String()})
	h.GetJournal(c)
}

// UpdateJournal godoc
// @Summary Update a journal
// @Description Update an existing journal's information
// @Tags Finance - Journals
// @Accept json
// @Produce json
// @Param id path string true "Journal ID"
// @Param body body entity.UpdateJournalInput true "Journal update data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journals/{id} [put]
func (h *Handler) UpdateJournal(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal ID")
		return
	}

	var input entity.UpdateJournalInput
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
	if input.DefaultDebitAccountID != nil {
		if *input.DefaultDebitAccountID == "" {
			addUpdate("default_debit_account_id", nil)
		} else {
			addUpdate("default_debit_account_id", *input.DefaultDebitAccountID)
		}
	}
	if input.DefaultCreditAccountID != nil {
		if *input.DefaultCreditAccountID == "" {
			addUpdate("default_credit_account_id", nil)
		} else {
			addUpdate("default_credit_account_id", *input.DefaultCreditAccountID)
		}
	}
	if input.AutoSequence != nil {
		addUpdate("auto_sequence", *input.AutoSequence)
	}
	if input.NumberPrefix != nil {
		addUpdate("number_prefix", *input.NumberPrefix)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
	}
	if input.ShortCode != nil {
		addUpdate("short_code", *input.ShortCode)
	}
	if input.Currency != nil {
		addUpdate("currency", *input.Currency)
	}
	if input.BankAccountID != nil {
		if *input.BankAccountID == "" {
			addUpdate("bank_account_id", nil)
		} else {
			addUpdate("bank_account_id", *input.BankAccountID)
		}
	}
	if input.SuspenseAccountID != nil {
		if *input.SuspenseAccountID == "" {
			addUpdate("suspense_account_id", nil)
		} else {
			addUpdate("suspense_account_id", *input.SuspenseAccountID)
		}
	}
	if input.ProfitAccountID != nil {
		if *input.ProfitAccountID == "" {
			addUpdate("profit_account_id", nil)
		} else {
			addUpdate("profit_account_id", *input.ProfitAccountID)
		}
	}
	if input.LossAccountID != nil {
		if *input.LossAccountID == "" {
			addUpdate("loss_account_id", nil)
		} else {
			addUpdate("loss_account_id", *input.LossAccountID)
		}
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
		UPDATE journals SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Journal")
		return
	}
	if err != nil {
		h.log.Error("Failed to update journal", "error", err)
		response.InternalError(c, "Failed to update journal")
		return
	}

	h.GetJournal(c)
}

// DeleteJournal godoc
// @Summary Delete a journal
// @Description Soft-delete a journal (blocked if it has existing entries)
// @Tags Finance - Journals
// @Accept json
// @Produce json
// @Param id path string true "Journal ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journals/{id} [delete]
func (h *Handler) DeleteJournal(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal ID")
		return
	}

	// Check if journal has entries
	var entryCount int
	err = h.db.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE journal_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&entryCount)
	if err != nil {
		h.log.Error("Failed to check journal entries", "error", err)
		response.InternalError(c, "Failed to delete journal")
		return
	}
	if entryCount > 0 {
		response.Conflict(c, "Cannot delete journal with existing entries")
		return
	}

	result, err := h.db.Exec(`
		UPDATE journals SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, time.Now(), id, tenantID)

	if err != nil {
		h.log.Error("Failed to delete journal", "error", err)
		response.InternalError(c, "Failed to delete journal")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Journal")
		return
	}

	response.NoContent(c)
}

// ListPaymentMethods returns all payment methods for the tenant
func (h *Handler) ListPaymentMethods(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, code, name, type, COALESCE(is_active, true)
		FROM payment_methods
		WHERE tenant_id = $1
		ORDER BY name ASC
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to list payment methods", "error", err)
		response.InternalError(c, "Failed to fetch payment methods")
		return
	}
	defer rows.Close()

	type PM struct {
		ID       uuid.UUID `json:"id"`
		Code     string    `json:"code"`
		Name     string    `json:"name"`
		Type     string    `json:"type"`
		IsActive bool      `json:"is_active"`
	}
	result := make([]PM, 0)
	for rows.Next() {
		var pm PM
		if err := rows.Scan(&pm.ID, &pm.Code, &pm.Name, &pm.Type, &pm.IsActive); err != nil {
			continue
		}
		result = append(result, pm)
	}
	response.Success(c, result)
}

// ListJournalPaymentMethods returns payment methods linked to a journal
func (h *Handler) ListJournalPaymentMethods(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	journalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid journal ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT jpm.id, jpm.payment_method_id, jpm.direction, jpm.name,
			jpm.outstanding_account_id, COALESCE(jpm.is_active, true), jpm.created_at,
			pm.code as pm_code, pm.name as pm_name,
			COALESCE(a.code || ' ' || a.name, '') as account_name
		FROM journal_payment_methods jpm
		JOIN payment_methods pm ON jpm.payment_method_id = pm.id
		LEFT JOIN accounts a ON jpm.outstanding_account_id = a.id
		WHERE jpm.journal_id = $1 AND jpm.tenant_id = $2
		ORDER BY jpm.direction, jpm.created_at
	`, journalID, tenantID)
	if err != nil {
		h.log.Error("Failed to list journal payment methods", "error", err)
		response.InternalError(c, "Failed to fetch journal payment methods")
		return
	}
	defer rows.Close()

	type JPM struct {
		ID                   uuid.UUID  `json:"id"`
		PaymentMethodID      uuid.UUID  `json:"payment_method_id"`
		Direction            string     `json:"direction"`
		Name                 string     `json:"name"`
		OutstandingAccountID *uuid.UUID `json:"outstanding_account_id,omitempty"`
		IsActive             bool       `json:"is_active"`
		CreatedAt            time.Time  `json:"created_at"`
		PMCode               string     `json:"pm_code"`
		PMName               string     `json:"pm_name"`
		AccountName          string     `json:"account_name"`
	}
	result := make([]JPM, 0)
	for rows.Next() {
		var jpm JPM
		if err := rows.Scan(&jpm.ID, &jpm.PaymentMethodID, &jpm.Direction, &jpm.Name,
			&jpm.OutstandingAccountID, &jpm.IsActive, &jpm.CreatedAt,
			&jpm.PMCode, &jpm.PMName, &jpm.AccountName); err != nil {
			continue
		}
		result = append(result, jpm)
	}
	response.Success(c, result)
}

// AddJournalPaymentMethod links a payment method to a journal
func (h *Handler) AddJournalPaymentMethod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	journalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid journal ID")
		return
	}

	var input struct {
		PaymentMethodID      string `json:"payment_method_id" binding:"required"`
		Direction            string `json:"direction" binding:"required,oneof=inbound outbound"`
		Name                 string `json:"name"`
		OutstandingAccountID string `json:"outstanding_account_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	id := uuid.New()
	var outAccID interface{}
	if input.OutstandingAccountID != "" {
		outAccID = input.OutstandingAccountID
	}

	_, err = h.db.Exec(`
		INSERT INTO journal_payment_methods (id, tenant_id, journal_id, payment_method_id, direction, name, outstanding_account_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (journal_id, payment_method_id, direction) DO UPDATE SET
			name = EXCLUDED.name, outstanding_account_id = EXCLUDED.outstanding_account_id, is_active = true
	`, id, tenantID, journalID, input.PaymentMethodID, input.Direction, input.Name, outAccID)
	if err != nil {
		h.log.Error("Failed to add journal payment method", "error", err)
		response.InternalError(c, "Failed to add payment method")
		return
	}

	h.ListJournalPaymentMethods(c)
}

// RemoveJournalPaymentMethod removes a payment method from a journal
func (h *Handler) RemoveJournalPaymentMethod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	pmID, err := uuid.Parse(c.Param("pmId"))
	if err != nil {
		response.BadRequest(c, "Invalid payment method ID")
		return
	}

	result, err := h.db.Exec(`
		DELETE FROM journal_payment_methods WHERE id = $1 AND tenant_id = $2
	`, pmID, tenantID)
	if err != nil {
		h.log.Error("Failed to remove journal payment method", "error", err)
		response.InternalError(c, "Failed to remove payment method")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Journal payment method")
		return
	}

	response.NoContent(c)
}

// UpdateJournalPaymentMethod updates the outstanding account on a journal payment method
func (h *Handler) UpdateJournalPaymentMethod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	pmID, err := uuid.Parse(c.Param("pmId"))
	if err != nil {
		response.BadRequest(c, "Invalid payment method ID")
		return
	}

	var input struct {
		OutstandingAccountID *string `json:"outstanding_account_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	var outAccID interface{}
	if input.OutstandingAccountID != nil && *input.OutstandingAccountID != "" {
		outAccID = *input.OutstandingAccountID
	}

	_, err = h.db.Exec(`
		UPDATE journal_payment_methods SET outstanding_account_id = $1
		WHERE id = $2 AND tenant_id = $3
	`, outAccID, pmID, tenantID)
	if err != nil {
		h.log.Error("Failed to update journal payment method", "error", err)
		response.InternalError(c, "Failed to update payment method")
		return
	}

	response.Success(c, gin.H{"message": "Updated"})
}

// CreateJournalEntry creates a new journal entry
// CreateJournalEntry godoc
// @Summary Create a new journal entry
// @Description Create a new journal entry with debit and credit lines
// @Tags Finance - Journal Entries
// @Accept json
// @Produce json
// @Param entry body entity.CreateJournalEntryInput true "Journal entry details"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journal-entries [post]
func (h *Handler) CreateJournalEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateJournalEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Validate description is required
	if strings.TrimSpace(input.Description) == "" {
		response.BadRequest(c, "Description is required")
		return
	}

	// Validate journal exists
	journalID, err := uuid.Parse(input.JournalID)
	if err != nil {
		response.BadRequest(c, "Invalid journal ID")
		return
	}

	var journalExists bool
	var nextNumber int
	var numberPrefix sql.NullString
	err = h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM journals WHERE id = $1 AND tenant_id = $2), COALESCE(next_number, 1), number_prefix FROM journals WHERE id = $1 AND tenant_id = $2",
		journalID, tenantID,
	).Scan(&journalExists, &nextNumber, &numberPrefix)
	if err != nil || !journalExists {
		response.BadRequest(c, "Journal not found")
		return
	}

	// Parse entry date
	entryDate, err := time.Parse("2006-01-02", input.EntryDate)
	if err != nil {
		response.BadRequest(c, "Invalid entry date format (use YYYY-MM-DD)")
		return
	}

	// Check lock date and period lock
	if errMsg := h.checkLockDate(tenantID, entryDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}
	if errMsg := h.checkPeriodLock(tenantID, entryDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	// Validate lines - debits must equal credits
	var totalDebit, totalCredit float64
	for _, line := range input.Lines {
		totalDebit += line.DebitAmount
		totalCredit += line.CreditAmount

		// Each line must have either debit or credit (not both, not neither)
		if line.DebitAmount > 0 && line.CreditAmount > 0 {
			response.BadRequest(c, "A line cannot have both debit and credit amounts")
			return
		}
		if line.DebitAmount <= 0 && line.CreditAmount <= 0 {
			response.BadRequest(c, "Each line must have either a debit or credit amount")
			return
		}
	}

	// Check if balanced (with small tolerance for floating point)
	if math.Abs(totalDebit-totalCredit) > 0.001 {
		response.BadRequest(c, fmt.Sprintf("Journal entry is not balanced. Debits: %.2f, Credits: %.2f", totalDebit, totalCredit))
		return
	}

	// Resolve organization ID early (needed for entry number generation)
	var orgID *uuid.UUID
	if input.OrganizationID != "" {
		parsed, parseErr := uuid.Parse(input.OrganizationID)
		if parseErr == nil {
			orgID = &parsed
		}
	}
	// Fallback to middleware header if not provided in body
	if orgID == nil {
		if headerOrgID, orgOk := middleware.GetOrganizationID(c); orgOk && headerOrgID != uuid.Nil {
			orgID = &headerOrgID
		}
	}

	// Generate entry number: PREFIX-YYYY-NNNN format
	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}

	year := entryDate.Year()
	yearPrefix := fmt.Sprintf("%s-%d-", prefix, year)

	var maxNumber int
	_ = h.db.QueryRow(
		`SELECT COALESCE(MAX(
			CAST(SUBSTRING(entry_number FROM '[0-9]+$') AS INTEGER)
		), 0) FROM journal_entries WHERE tenant_id = $1 AND journal_id = $2 AND entry_number LIKE $3 AND deleted_at IS NULL`,
		tenantID, journalID, yearPrefix+"%",
	).Scan(&maxNumber)

	actualNext := maxNumber + 1
	if nextNumber > actualNext {
		actualNext = nextNumber
	}
	entryNumber := fmt.Sprintf("%s%04d", yearPrefix, actualNext)

	id := uuid.New()
	now := time.Now()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create journal entry")
		return
	}
	defer tx.Rollback()

	// Insert journal entry header
	var description, reference *string
	if input.Description != "" {
		description = &input.Description
	}
	if input.Reference != "" {
		reference = &input.Reference
	}

	exchangeRate := input.ExchangeRate
	if exchangeRate <= 0 {
		exchangeRate = 1.0
	}

	sourceType := "manual"

	// Convert tags to PostgreSQL array
	var tags []string
	if len(input.Tags) > 0 {
		tags = input.Tags
	}

	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			tags, source_type, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, id, tenantID, orgID, journalID, entryNumber, entryDate, reference, description,
		pq.Array(tags), sourceType, exchangeRate, totalDebit, totalCredit, "draft", userID, now, now)

	if err != nil {
		h.log.Error("Failed to create journal entry", "error", err)
		response.InternalError(c, "Failed to create journal entry")
		return
	}

	// Insert journal entry lines
	for i, line := range input.Lines {
		lineID := uuid.New()
		accountID, err := uuid.Parse(line.AccountID)
		if err != nil {
			response.BadRequest(c, fmt.Sprintf("Invalid account ID in line %d", i+1))
			return
		}

		var lineDesc *string
		if line.Description != "" {
			lineDesc = &line.Description
		}

		var contactID *uuid.UUID
		if line.ContactID != nil && *line.ContactID != "" {
			cid, _ := uuid.Parse(*line.ContactID)
			contactID = &cid
		}

		_, err = tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, contact_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, lineID, id, i+1, accountID, contactID, lineDesc,
			line.DebitAmount, line.CreditAmount, exchangeRate, now)

		if err != nil {
			h.log.Error("Failed to create journal entry line", "error", err)
			response.InternalError(c, "Failed to create journal entry")
			return
		}
	}

	// Update journal next number
	_, err = tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", journalID)
	if err != nil {
		h.log.Error("Failed to update journal number", "error", err)
		response.InternalError(c, "Failed to create journal entry")
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create journal entry")
		return
	}

	// Return created entry
	entry := &entity.JournalEntry{
		ID:          id,
		TenantID:    tenantID,
		JournalID:   journalID,
		EntryNumber: entryNumber,
		EntryDate:   entryDate,
		Reference:   reference,
		Description: description,
		TotalDebit:  totalDebit,
		TotalCredit: totalCredit,
		Status:      "draft",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	response.Created(c, entry.ToResponse())
}

// GetJournalEntry godoc
// @Summary Get journal entry by ID
// @Description Get detailed information about a specific journal entry including all lines
// @Tags Finance - Journal Entries
// @Accept json
// @Produce json
// @Param id path string true "Journal Entry ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journal-entries/{id} [get]
func (h *Handler) GetJournalEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal entry ID")
		return
	}

	// Get header
	query := `
		SELECT je.id, je.tenant_id, je.journal_id, je.entry_number, je.entry_date,
			   je.reference, je.description, je.source_type, je.total_debit, je.total_credit,
			   je.status, je.posted_at, je.reversed_entry_id, je.is_reversal, je.reversal_of_id,
			   je.reversal_reason, je.tags, je.created_at, je.updated_at,
			   j.code as journal_code, j.name as journal_name
		FROM journal_entries je
		JOIN journals j ON je.journal_id = j.id
		WHERE je.id = $1 AND je.tenant_id = $2 AND je.deleted_at IS NULL
	`

	var je entity.JournalEntry
	var ref, desc, sourceType, reversalReason sql.NullString
	var postedAt sql.NullTime
	var journalCode, journalName string
	var tags []string

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&je.ID, &je.TenantID, &je.JournalID, &je.EntryNumber, &je.EntryDate,
		&ref, &desc, &sourceType, &je.TotalDebit, &je.TotalCredit,
		&je.Status, &postedAt, &je.ReversedEntryID, &je.IsReversal, &je.ReversalOfID,
		&reversalReason, pq.Array(&tags), &je.CreatedAt, &je.UpdatedAt,
		&journalCode, &journalName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Journal entry")
		return
	}
	if err != nil {
		h.log.Error("Failed to get journal entry", "error", err)
		response.InternalError(c, "Failed to get journal entry")
		return
	}

	if ref.Valid {
		je.Reference = &ref.String
	}
	if desc.Valid {
		je.Description = &desc.String
	}
	if sourceType.Valid {
		je.SourceType = &sourceType.String
	}
	if postedAt.Valid {
		je.PostedAt = &postedAt.Time
	}
	if reversalReason.Valid {
		je.ReversalReason = &reversalReason.String
	}
	je.Tags = tags

	je.Journal = &entity.Journal{
		Code: journalCode,
		Name: journalName,
	}

	// Get lines
	linesQuery := `
		SELECT jel.id, jel.journal_entry_id, jel.line_number, jel.account_id, jel.contact_id,
			   jel.description, jel.debit_amount, jel.credit_amount, jel.exchange_rate,
			   jel.reconciled, jel.created_at,
			   a.code as account_code, a.name as account_name
		FROM journal_entry_lines jel
		JOIN accounts a ON jel.account_id = a.id
		WHERE jel.journal_entry_id = $1
		ORDER BY jel.line_number ASC
	`

	rows, err := h.db.Query(linesQuery, id)
	if err != nil {
		h.log.Error("Failed to get journal entry lines", "error", err)
		response.InternalError(c, "Failed to get journal entry")
		return
	}
	defer rows.Close()

	je.Lines = make([]entity.JournalEntryLine, 0)
	for rows.Next() {
		var line entity.JournalEntryLine
		var contactID, lineDesc sql.NullString
		var accountCode, accountName string

		err := rows.Scan(
			&line.ID, &line.JournalEntryID, &line.LineNumber, &line.AccountID, &contactID,
			&lineDesc, &line.DebitAmount, &line.CreditAmount, &line.ExchangeRate,
			&line.Reconciled, &line.CreatedAt,
			&accountCode, &accountName,
		)
		if err != nil {
			continue
		}

		if contactID.Valid {
			cid, _ := uuid.Parse(contactID.String)
			line.ContactID = &cid
		}
		if lineDesc.Valid {
			line.Description = &lineDesc.String
		}

		line.Account = &entity.Account{
			ID:   line.AccountID,
			Code: accountCode,
			Name: accountName,
		}

		je.Lines = append(je.Lines, line)
	}

	response.Success(c, je.ToResponse())
}

// PostJournalEntry godoc
// @Summary Post a journal entry
// @Description Post a journal entry to update account balances
// @Tags Finance - Journal Entries
// @Accept json
// @Produce json
// @Param id path string true "Journal Entry ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journal-entries/{id}/post [post]
func (h *Handler) PostJournalEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal entry ID")
		return
	}

	// Get entry and verify status
	var status string
	var totalDebit, totalCredit float64
	var entryDate time.Time
	err = h.db.QueryRow(`
		SELECT status, total_debit, total_credit, entry_date FROM journal_entries
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&status, &totalDebit, &totalCredit, &entryDate)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Journal entry")
		return
	}
	if err != nil {
		h.log.Error("Failed to get journal entry", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}

	if status != "draft" {
		response.BadRequest(c, "Only draft entries can be posted")
		return
	}

	// Check lock date and period lock
	if errMsg := h.checkLockDate(tenantID, entryDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}
	if errMsg := h.checkPeriodLock(tenantID, entryDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	// Verify balanced
	if math.Abs(totalDebit-totalCredit) > 0.001 {
		response.BadRequest(c, "Journal entry is not balanced")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}
	defer tx.Rollback()

	// Get lines and update account balances
	rows, err := tx.Query(`
		SELECT jel.account_id, jel.debit_amount, jel.credit_amount, at.normal_balance
		FROM journal_entry_lines jel
		JOIN accounts a ON jel.account_id = a.id
		JOIN account_types at ON a.account_type_id = at.id
		WHERE jel.journal_entry_id = $1
	`, id)
	if err != nil {
		h.log.Error("Failed to get journal lines", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}

	type lineUpdate struct {
		accountID     uuid.UUID
		debitAmount   float64
		creditAmount  float64
		normalBalance string
	}

	lines := make([]lineUpdate, 0)
	for rows.Next() {
		var l lineUpdate
		if err := rows.Scan(&l.accountID, &l.debitAmount, &l.creditAmount, &l.normalBalance); err != nil {
			continue
		}
		lines = append(lines, l)
	}
	rows.Close()

	// Update each account balance
	for _, line := range lines {
		var balanceChange float64
		if line.normalBalance == "debit" {
			balanceChange = line.debitAmount - line.creditAmount
		} else {
			balanceChange = line.creditAmount - line.debitAmount
		}

		_, err = tx.Exec(`
			UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2
			WHERE id = $3
		`, balanceChange, time.Now(), line.accountID)

		if err != nil {
			h.log.Error("Failed to update account balance", "error", err, "account_id", line.accountID)
			response.InternalError(c, "Failed to post journal entry")
			return
		}
	}

	// Validate: Cash and Bank accounts must not go negative after posting
	for _, line := range lines {
		var newBalance float64
		var accountCode string
		err = tx.QueryRow(`SELECT current_balance, code FROM accounts WHERE id = $1`, line.accountID).Scan(&newBalance, &accountCode)
		if err != nil {
			continue
		}
		// Block negative balances for cash (1000) and bank (1010, 1100) accounts
		if (accountCode == "1000" || accountCode == "1010" || accountCode == "1100") && newBalance < -0.001 {
			response.BadRequest(c, fmt.Sprintf("Account %s balance cannot be negative (would be %.2f)", accountCode, newBalance))
			return
		}
	}

	// Update entry status
	now := time.Now()
	_, err = tx.Exec(`
		UPDATE journal_entries SET status = 'posted', posted_at = $1, posted_by = $2, updated_at = $1
		WHERE id = $3
	`, now, userID, id)

	if err != nil {
		h.log.Error("Failed to update entry status", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}

	// Commit
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}

	response.Success(c, gin.H{"message": "Journal entry posted successfully", "posted_at": now})
}

// ReverseJournalEntry godoc
// @Summary Reverse a journal entry
// @Description Create a reversal entry for a posted journal entry
// @Tags Finance - Journal Entries
// @Accept json
// @Produce json
// @Param id path string true "Journal Entry ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/journal-entries/{id}/reverse [post]
func (h *Handler) ReverseJournalEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	originalID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal entry ID")
		return
	}

	// Parse optional reversal input (date, reason)
	var reverseInput entity.ReverseJournalEntryInput
	_ = c.ShouldBindJSON(&reverseInput) // optional body

	// Get original entry
	var status, entryNumber string
	var journalID uuid.UUID
	var organizationID *uuid.UUID
	var entryDate time.Time
	var totalDebit, totalCredit float64
	var ref, desc sql.NullString
	var reversedEntryID *uuid.UUID

	err = h.db.QueryRow(`
		SELECT status, journal_id, organization_id, entry_number, entry_date, reference, description, total_debit, total_credit, reversed_entry_id
		FROM journal_entries
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, originalID, tenantID).Scan(&status, &journalID, &organizationID, &entryNumber, &entryDate, &ref, &desc, &totalDebit, &totalCredit, &reversedEntryID)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Journal entry")
		return
	}
	if err != nil {
		h.log.Error("Failed to get journal entry", "error", err)
		response.InternalError(c, "Failed to reverse journal entry")
		return
	}

	if status != "posted" {
		response.BadRequest(c, "Only posted entries can be reversed")
		return
	}

	// Check if already reversed
	if reversedEntryID != nil {
		response.BadRequest(c, "This entry has already been reversed")
		return
	}

	// Determine reversal date
	reversalDate := time.Now()
	if reverseInput.Date != "" {
		parsed, parseErr := time.Parse("2006-01-02", reverseInput.Date)
		if parseErr != nil {
			response.BadRequest(c, "Invalid reversal date format (use YYYY-MM-DD)")
			return
		}
		reversalDate = parsed
	}

	// Check lock date and period lock for reversal date
	if errMsg := h.checkLockDate(tenantID, reversalDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}
	if errMsg := h.checkPeriodLock(tenantID, reversalDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	// Get next entry number
	var nextNumber int
	var numberPrefix sql.NullString
	h.db.QueryRow("SELECT COALESCE(next_number, 1), number_prefix FROM journals WHERE id = $1", journalID).Scan(&nextNumber, &numberPrefix)

	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}

	// Generate reversal number: PREFIX-YYYY-NNNN format
	reversalYear := time.Now().Year()
	yearPrefix := fmt.Sprintf("%s-%d-", prefix, reversalYear)

	var maxNumber int
	_ = h.db.QueryRow(
		`SELECT COALESCE(MAX(
			CAST(SUBSTRING(entry_number FROM '[0-9]+$') AS INTEGER)
		), 0) FROM journal_entries WHERE tenant_id = $1 AND journal_id = $2 AND entry_number LIKE $3 AND deleted_at IS NULL`,
		tenantID, journalID, yearPrefix+"%",
	).Scan(&maxNumber)

	actualNext := maxNumber + 1
	if nextNumber > actualNext {
		actualNext = nextNumber
	}
	reversalNumber := fmt.Sprintf("%s%04d", yearPrefix, actualNext)

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to reverse journal entry")
		return
	}
	defer tx.Rollback()

	reversalID := uuid.New()
	now := time.Now()

	description := fmt.Sprintf("Teskari: %s", entryNumber)
	reference := "REV-" + entryNumber

	var reversalReason *string
	if reverseInput.Reason != "" {
		reversalReason = &reverseInput.Reason
	}

	// Create reversal entry as draft (swap debit/credit)
	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, total_debit, total_credit, status,
			is_reversal, reversal_of_id, reversal_reason,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`, reversalID, tenantID, organizationID, journalID, reversalNumber, reversalDate, reference, description,
		"reversal", totalCredit, totalDebit, "draft",
		true, originalID, reversalReason,
		userID, now, now)

	if err != nil {
		h.log.Error("Failed to create reversal entry", "error", err)
		response.InternalError(c, "Failed to reverse journal entry")
		return
	}

	// Get original lines and create reversed lines
	rows, err := tx.Query(`
		SELECT account_id, contact_id, description, debit_amount, credit_amount, exchange_rate
		FROM journal_entry_lines WHERE journal_entry_id = $1 ORDER BY line_number
	`, originalID)
	if err != nil {
		h.log.Error("Failed to get original lines", "error", err)
		response.InternalError(c, "Failed to reverse journal entry")
		return
	}

	lineNum := 1
	type origLine struct {
		accountID    uuid.UUID
		contactID    *uuid.UUID
		description  *string
		debitAmount  float64
		creditAmount float64
		exchangeRate float64
	}
	origLines := make([]origLine, 0)

	for rows.Next() {
		var l origLine
		var cid, ldesc sql.NullString
		if err := rows.Scan(&l.accountID, &cid, &ldesc, &l.debitAmount, &l.creditAmount, &l.exchangeRate); err != nil {
			continue
		}
		if cid.Valid {
			c, _ := uuid.Parse(cid.String)
			l.contactID = &c
		}
		if ldesc.Valid {
			l.description = &ldesc.String
		}
		origLines = append(origLines, l)
	}
	rows.Close()

	for _, ol := range origLines {
		lineID := uuid.New()
		// Swap debit and credit
		_, err = tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, contact_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, lineID, reversalID, lineNum, ol.accountID, ol.contactID, ol.description,
			ol.creditAmount, ol.debitAmount, ol.exchangeRate, now) // Note: swapped!

		if err != nil {
			h.log.Error("Failed to create reversal line", "error", err)
			response.InternalError(c, "Failed to reverse journal entry")
			return
		}
		lineNum++
	}

	// Mark original entry as reversed (balance changes happen when reversal is posted)
	_, err = tx.Exec(`UPDATE journal_entries SET reversed_entry_id = $1, updated_at = $2 WHERE id = $3`,
		reversalID, now, originalID)
	if err != nil {
		h.log.Error("Failed to mark original as reversed", "error", err)
		response.InternalError(c, "Failed to reverse journal entry")
		return
	}

	// Update journal next number
	_, err = tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", journalID)
	if err != nil {
		h.log.Error("Failed to update journal number", "error", err)
	}

	// Commit
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to reverse journal entry")
		return
	}

	response.Created(c, gin.H{
		"message":           "Journal entry reversed successfully",
		"reversal_entry_id": reversalID,
		"reversal_number":   reversalNumber,
	})
}

// UpdateJournalEntry updates a draft journal entry
// @Summary Update a draft journal entry
// @Tags Finance - Journal Entries
// @Param id path string true "Journal Entry ID"
// @Router /finance/journal-entries/{id} [put]
func (h *Handler) UpdateJournalEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal entry ID")
		return
	}

	// Check entry exists and is draft
	var status string
	err = h.db.QueryRow(`SELECT status FROM journal_entries WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Journal entry")
		return
	}
	if err != nil {
		h.log.Error("Failed to get journal entry", "error", err)
		response.InternalError(c, "Failed to update journal entry")
		return
	}
	if status != "draft" {
		response.BadRequest(c, "Only draft entries can be edited")
		return
	}

	var input entity.CreateJournalEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	if strings.TrimSpace(input.Description) == "" {
		response.BadRequest(c, "Description is required")
		return
	}

	entryDate, err := time.Parse("2006-01-02", input.EntryDate)
	if err != nil {
		response.BadRequest(c, "Invalid entry date format (use YYYY-MM-DD)")
		return
	}

	if errMsg := h.checkLockDate(tenantID, entryDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}
	if errMsg := h.checkPeriodLock(tenantID, entryDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	// Validate lines
	var totalDebit, totalCredit float64
	for _, line := range input.Lines {
		totalDebit += line.DebitAmount
		totalCredit += line.CreditAmount
		if line.DebitAmount > 0 && line.CreditAmount > 0 {
			response.BadRequest(c, "A line cannot have both debit and credit amounts")
			return
		}
		if line.DebitAmount <= 0 && line.CreditAmount <= 0 {
			response.BadRequest(c, "Each line must have either a debit or credit amount")
			return
		}
	}
	if math.Abs(totalDebit-totalCredit) > 0.001 {
		response.BadRequest(c, fmt.Sprintf("Journal entry is not balanced. Debits: %.2f, Credits: %.2f", totalDebit, totalCredit))
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to update journal entry")
		return
	}
	defer tx.Rollback()

	now := time.Now()
	var description, reference *string
	if input.Description != "" {
		description = &input.Description
	}
	if input.Reference != "" {
		reference = &input.Reference
	}

	exchangeRate := input.ExchangeRate
	if exchangeRate <= 0 {
		exchangeRate = 1.0
	}

	var tags []string
	if len(input.Tags) > 0 {
		tags = input.Tags
	}

	// Update header
	_, err = tx.Exec(`
		UPDATE journal_entries SET entry_date = $1, reference = $2, description = $3,
		exchange_rate = $4, total_debit = $5, total_credit = $6, tags = $7, updated_at = $8
		WHERE id = $9 AND tenant_id = $10
	`, entryDate, reference, description, exchangeRate, totalDebit, totalCredit, pq.Array(tags), now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to update journal entry", "error", err)
		response.InternalError(c, "Failed to update journal entry")
		return
	}

	// Delete old lines and insert new ones
	_, err = tx.Exec(`DELETE FROM journal_entry_lines WHERE journal_entry_id = $1`, id)
	if err != nil {
		h.log.Error("Failed to delete old lines", "error", err)
		response.InternalError(c, "Failed to update journal entry")
		return
	}

	for i, line := range input.Lines {
		lineID := uuid.New()
		accountID, err := uuid.Parse(line.AccountID)
		if err != nil {
			response.BadRequest(c, fmt.Sprintf("Invalid account ID in line %d", i+1))
			return
		}
		var lineDesc *string
		if line.Description != "" {
			lineDesc = &line.Description
		}
		var contactID *uuid.UUID
		if line.ContactID != nil && *line.ContactID != "" {
			cid, _ := uuid.Parse(*line.ContactID)
			contactID = &cid
		}
		_, err = tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, contact_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, lineID, id, i+1, accountID, contactID, lineDesc, line.DebitAmount, line.CreditAmount, exchangeRate, now)
		if err != nil {
			h.log.Error("Failed to create journal entry line", "error", err)
			response.InternalError(c, "Failed to update journal entry")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to update journal entry")
		return
	}

	_ = userID
	response.Success(c, gin.H{"message": "Journal entry updated successfully"})
}

// DeleteJournalEntry deletes a draft journal entry
// @Summary Delete a draft journal entry
// @Tags Finance - Journal Entries
// @Param id path string true "Journal Entry ID"
// @Router /finance/journal-entries/{id} [delete]
func (h *Handler) DeleteJournalEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal entry ID")
		return
	}

	var status string
	err = h.db.QueryRow(`SELECT status FROM journal_entries WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Journal entry")
		return
	}
	if err != nil {
		h.log.Error("Failed to get journal entry", "error", err)
		response.InternalError(c, "Failed to delete journal entry")
		return
	}
	if status != "draft" {
		response.BadRequest(c, "Only draft entries can be deleted")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`UPDATE journal_entries SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3`, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete journal entry", "error", err)
		response.InternalError(c, "Failed to delete journal entry")
		return
	}

	response.Success(c, gin.H{"message": "Journal entry deleted successfully"})
}

// CancelJournalEntry cancels a draft journal entry
// @Summary Cancel a draft journal entry
// @Tags Finance - Journal Entries
// @Param id path string true "Journal Entry ID"
// @Router /finance/journal-entries/{id}/cancel [post]
func (h *Handler) CancelJournalEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid journal entry ID")
		return
	}

	var status string
	err = h.db.QueryRow(`SELECT status FROM journal_entries WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Journal entry")
		return
	}
	if err != nil {
		h.log.Error("Failed to get journal entry", "error", err)
		response.InternalError(c, "Failed to cancel journal entry")
		return
	}
	if status != "draft" {
		response.BadRequest(c, "Only draft entries can be cancelled")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`UPDATE journal_entries SET status = 'cancelled', cancelled_at = $1, updated_at = $1 WHERE id = $2 AND tenant_id = $3`, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to cancel journal entry", "error", err)
		response.InternalError(c, "Failed to cancel journal entry")
		return
	}

	response.Success(c, gin.H{"message": "Journal entry cancelled successfully"})
}

// =====================================================
// PAYMENT HANDLERS
// =====================================================

// ListPayments godoc
// @Summary List payments
// @Description Get a paginated list of all payments
// @Tags Finance - Payments
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param status query string false "Filter by status (pending, confirmed, cancelled)"
// @Param payment_type query string false "Filter by payment type (customer, vendor)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/payments [get]
func (h *Handler) ListPayments(c *gin.Context) {
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
	paymentType := c.Query("type")
	status := c.Query("status")
	contactID := c.Query("contact_id")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	baseQuery := `
		SELECT p.id, p.payment_number, p.type, p.contact_id, p.payment_date, p.amount,
			   p.status, p.reference, p.notes, p.created_at,
			   c.name as contact_name, p.journal_id, COALESCE(j.name, '') as journal_name
		FROM payments p
		JOIN contacts c ON p.contact_id = c.id
		LEFT JOIN journals j ON p.journal_id = j.id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM payments p WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if paymentType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.type = $%d", argCount)
		args = append(args, paymentType)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.status = $%d", argCount)
		args = append(args, status)
	}

	if contactID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.contact_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.contact_id = $%d", argCount)
		args = append(args, contactID)
	}

	if dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.payment_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.payment_date >= $%d", argCount)
		args = append(args, dateFrom)
	}

	if dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.payment_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.payment_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count payments", "error", err)
		response.InternalError(c, "Failed to list payments")
		return
	}

	baseQuery += " ORDER BY p.payment_date DESC, p.payment_number DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list payments", "error", err)
		response.InternalError(c, "Failed to list payments")
		return
	}
	defer rows.Close()

	payments := make([]*entity.PaymentResponse, 0)
	for rows.Next() {
		var p entity.Payment
		var ref, notes, journalIDStr sql.NullString
		var contactName, journalName string

		err := rows.Scan(
			&p.ID, &p.PaymentNumber, &p.Type, &p.ContactID, &p.PaymentDate, &p.Amount,
			&p.Status, &ref, &notes, &p.CreatedAt, &contactName, &journalIDStr, &journalName,
		)
		if err != nil {
			continue
		}

		if ref.Valid {
			p.Reference = &ref.String
		}
		if notes.Valid {
			p.Notes = &notes.String
		}

		resp := p.ToResponse()
		resp.ContactName = contactName
		resp.JournalName = journalName
		if journalIDStr.Valid {
			resp.JournalID = journalIDStr.String
		}
		payments = append(payments, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, payments, pagination)
}

// CreatePayment godoc
// @Summary Create a new payment
// @Description Create a new payment record
// @Tags Finance - Payments
// @Accept json
// @Produce json
// @Param body body entity.CreatePaymentInput true "Payment creation data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/payments [post]
func (h *Handler) CreatePayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var input entity.CreatePaymentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	contactID, err := uuid.Parse(input.ContactID)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	// Validate contact exists
	var contactExists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND tenant_id = $2)", contactID, tenantID).Scan(&contactExists)
	if !contactExists {
		response.BadRequest(c, "Contact not found")
		return
	}

	paymentDate, err := time.Parse("2006-01-02", input.PaymentDate)
	if err != nil {
		response.BadRequest(c, "Invalid payment date format")
		return
	}

	// Generate payment number
	prefix := "PAY"
	if input.Type == "receipt" {
		prefix = "REC"
	}

	id := uuid.New()
	now := time.Now()

	var reference, notes *string
	if input.Reference != "" {
		reference = &input.Reference
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	exchangeRate := input.ExchangeRate
	if exchangeRate <= 0 {
		exchangeRate = 1.0
	}

	var bankAccountID *uuid.UUID
	if input.BankAccountID != nil && *input.BankAccountID != "" {
		parsed, _ := uuid.Parse(*input.BankAccountID)
		if parsed != uuid.Nil {
			bankAccountID = &parsed
		}
	}

	var journalIDPtr *uuid.UUID
	if input.JournalID != nil && *input.JournalID != "" {
		parsed, _ := uuid.Parse(*input.JournalID)
		if parsed != uuid.Nil {
			journalIDPtr = &parsed
		}
	}

	query := `
		INSERT INTO payments (
			id, tenant_id, organization_id, payment_number, type, contact_id, payment_date, amount,
			exchange_rate, reference, notes, status, bank_account_id, journal_id, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`

	// Try inserting with incrementing payment number, retry on duplicate
	var paymentNumber string
	for attempt := 0; attempt < 5; attempt++ {
		var lastNum int
		if orgIDPtr != nil {
			h.db.QueryRow(`
				SELECT COALESCE(MAX(CAST(SUBSTRING(payment_number FROM '[0-9]+$') AS INTEGER)), 0)
				FROM payments WHERE tenant_id = $1 AND type = $2 AND organization_id = $3 AND payment_number ~ ('^' || $4 || '-[0-9]+$')
			`, tenantID, input.Type, *orgIDPtr, prefix).Scan(&lastNum)
		} else {
			h.db.QueryRow(`
				SELECT COALESCE(MAX(CAST(SUBSTRING(payment_number FROM '[0-9]+$') AS INTEGER)), 0)
				FROM payments WHERE tenant_id = $1 AND type = $2 AND organization_id IS NULL AND payment_number ~ ('^' || $3 || '-[0-9]+$')
			`, tenantID, input.Type, prefix).Scan(&lastNum)
		}
		paymentNumber = fmt.Sprintf("%s-%06d", prefix, lastNum+1+attempt)

		_, err = h.db.Exec(query,
			id, tenantID, orgIDPtr, paymentNumber, input.Type, contactID, paymentDate, input.Amount,
			exchangeRate, reference, notes, "draft", bankAccountID, journalIDPtr, userID, now, now)

		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			break
		}
	}

	if err != nil {
		h.log.Error("Failed to create payment", "error", err)
		response.InternalError(c, "Failed to create payment")
		return
	}

	// Handle allocations if provided
	if len(input.Allocations) > 0 {
		for _, alloc := range input.Allocations {
			allocID := uuid.New()
			docID, _ := uuid.Parse(alloc.DocumentID)

			_, err = h.db.Exec(`
				INSERT INTO payment_allocations (id, payment_id, document_type, document_id, amount, created_at)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, allocID, id, alloc.DocumentType, docID, alloc.Amount, now)

			if err != nil {
				h.log.Error("Failed to create payment allocation", "error", err)
			}
		}
	}

	// Fetch contact name for response
	var contactName string
	h.db.QueryRow("SELECT name FROM contacts WHERE id = $1", contactID).Scan(&contactName)

	payment := &entity.Payment{
		ID:            id,
		TenantID:      tenantID,
		PaymentNumber: paymentNumber,
		Type:          input.Type,
		ContactID:     contactID,
		PaymentDate:   paymentDate,
		Amount:        input.Amount,
		Reference:     reference,
		Notes:         notes,
		Status:        "draft",
		CreatedAt:     now,
		UpdatedAt:     now,
		Contact:       &entity.Contact{Name: contactName},
	}

	response.Created(c, payment.ToResponse())
}

// GetPayment godoc
// @Summary Get payment by ID
// @Description Get detailed information about a specific payment
// @Tags Finance - Payments
// @Accept json
// @Produce json
// @Param id path string true "Payment ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/payments/{id} [get]
func (h *Handler) GetPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid payment ID")
		return
	}

	query := `
		SELECT p.id, p.payment_number, p.type, p.contact_id, p.payment_date, p.amount,
			   p.status, p.reference, p.notes, p.created_at,
			   c.name as contact_name
		FROM payments p
		JOIN contacts c ON p.contact_id = c.id
		WHERE p.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL
	`

	var p entity.Payment
	var ref, notes sql.NullString
	var contactName string

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&p.ID, &p.PaymentNumber, &p.Type, &p.ContactID, &p.PaymentDate, &p.Amount,
		&p.Status, &ref, &notes, &p.CreatedAt, &contactName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Payment")
		return
	}
	if err != nil {
		h.log.Error("Failed to get payment", "error", err)
		response.InternalError(c, "Failed to get payment")
		return
	}

	if ref.Valid {
		p.Reference = &ref.String
	}
	if notes.Valid {
		p.Notes = &notes.String
	}

	// Get allocations
	allocRows, err := h.db.Query(`
		SELECT id, document_type, document_id, amount, created_at
		FROM payment_allocations WHERE payment_id = $1
	`, id)
	if err == nil {
		defer allocRows.Close()
		p.Allocations = make([]entity.PaymentAllocation, 0)
		for allocRows.Next() {
			var a entity.PaymentAllocation
			allocRows.Scan(&a.ID, &a.DocumentType, &a.DocumentID, &a.Amount, &a.CreatedAt)
			a.PaymentID = id
			p.Allocations = append(p.Allocations, a)
		}
	}

	resp := p.ToResponse()
	resp.ContactName = contactName

	response.Success(c, resp)
}

// ConfirmPayment godoc
// @Summary Confirm a payment
// @Description Confirm a payment and create corresponding journal entry
// @Tags Finance - Payments
// @Accept json
// @Produce json
// @Param id path string true "Payment ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/payments/{id}/confirm [post]
func (h *Handler) ConfirmPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid payment ID")
		return
	}

	// Get payment details
	var status, paymentType, paymentNumber string
	var amount float64
	var contactID uuid.UUID
	var orgID, paymentMethodID, bankAccountIDStr, storedJournalID sql.NullString
	var paymentDate time.Time
	err = h.db.QueryRow(`
		SELECT status, type, amount, contact_id, organization_id, payment_method_id, payment_date, payment_number, bank_account_id, journal_id
		FROM payments
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&status, &paymentType, &amount, &contactID, &orgID, &paymentMethodID, &paymentDate, &paymentNumber, &bankAccountIDStr, &storedJournalID)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Payment")
		return
	}
	if err != nil {
		h.log.Error("Failed to get payment", "error", err)
		response.InternalError(c, "Failed to confirm payment")
		return
	}

	if status != "draft" {
		response.BadRequest(c, "Only draft payments can be confirmed")
		return
	}

	var orgIDPtr *uuid.UUID
	if orgID.Valid {
		parsed, _ := uuid.Parse(orgID.String)
		if parsed != uuid.Nil {
			orgIDPtr = &parsed
		}
	}

	now := time.Now()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Update payment status
	_, err = tx.Exec(`
		UPDATE payments SET status = 'confirmed', approved_by = $1, approved_at = $2, updated_at = $2
		WHERE id = $3
	`, userID, now, id)

	if err != nil {
		h.log.Error("Failed to confirm payment", "error", err)
		response.InternalError(c, "Failed to confirm payment")
		return
	}

	// Update allocated invoices/bills from payment_allocations
	// Collect allocations first, then close rows before executing updates
	// (lib/pq does not support interleaved queries on the same connection)
	type allocation struct {
		ID     uuid.UUID
		DocType string
		DocID  uuid.UUID
		Amount float64
	}
	var allocations []allocation

	allocRows, allocErr := tx.Query(
		`SELECT id, document_type, document_id, amount FROM payment_allocations WHERE payment_id = $1`,
		id,
	)
	if allocErr != nil {
		h.log.Error("Failed to query payment allocations", "error", allocErr, "payment_id", id)
	} else {
		for allocRows.Next() {
			var a allocation
			if scanErr := allocRows.Scan(&a.ID, &a.DocType, &a.DocID, &a.Amount); scanErr != nil {
				h.log.Error("Failed to scan payment allocation", "error", scanErr)
				continue
			}
			allocations = append(allocations, a)
		}
		allocRows.Close()
	}

	for _, a := range allocations {
		h.log.Info("Processing payment allocation", "alloc_id", a.ID, "doc_type", a.DocType, "doc_id", a.DocID, "amount", a.Amount)

		if a.DocType == "sales_invoice" {
			res, updErr := tx.Exec(`
				UPDATE sales_invoices SET
					amount_paid = amount_paid + $1,
					status = CASE WHEN amount_paid + $1 >= total_amount THEN 'paid' ELSE 'partial' END,
					updated_at = $2
				WHERE id = $3
			`, a.Amount, now, a.DocID)
			if updErr != nil {
				h.log.Error("Failed to update sales invoice amount_paid", "error", updErr, "invoice_id", a.DocID, "amount", a.Amount)
			} else if rows, _ := res.RowsAffected(); rows == 0 {
				h.log.Warn("Sales invoice not found for allocation", "invoice_id", a.DocID)
			} else {
				h.log.Info("Updated sales invoice amount_paid", "invoice_id", a.DocID, "added", a.Amount, "rows", rows)
			}
		} else if a.DocType == "purchase_invoice" {
			res, updErr := tx.Exec(`
				UPDATE purchase_invoices SET
					amount_paid = amount_paid + $1,
					status = CASE WHEN amount_paid + $1 >= total_amount THEN 'paid' ELSE 'partial' END,
					payment_status = CASE WHEN amount_paid + $1 >= total_amount THEN 'paid' ELSE 'partial' END,
					updated_at = $2
				WHERE id = $3
			`, a.Amount, now, a.DocID)
			if updErr != nil {
				h.log.Error("Failed to update purchase invoice amount_paid", "error", updErr, "invoice_id", a.DocID, "amount", a.Amount)
			} else if rows, _ := res.RowsAffected(); rows == 0 {
				h.log.Warn("Purchase invoice not found for allocation", "invoice_id", a.DocID)
			} else {
				h.log.Info("Updated purchase invoice amount_paid", "invoice_id", a.DocID, "added", a.Amount, "rows", rows)
			}
		}
	}

	// --- Create journal entry for the payment ---
	// Determine cash/bank account: stored bank_account_id → journal default account → payment method → name fallback
	var cashAccountID uuid.UUID
	if bankAccountIDStr.Valid {
		cashAccountID, _ = uuid.Parse(bankAccountIDStr.String)
	}
	if cashAccountID == uuid.Nil && storedJournalID.Valid {
		// Try the selected journal's default debit account (bank/cash journals typically use a single default account)
		_ = tx.QueryRow(
			`SELECT COALESCE(default_debit_account_id, default_credit_account_id) FROM journals WHERE id = $1 AND tenant_id = $2`,
			storedJournalID.String, tenantID,
		).Scan(&cashAccountID)
	}
	if cashAccountID == uuid.Nil && paymentMethodID.Valid {
		_ = tx.QueryRow(
			`SELECT account_id FROM payment_methods WHERE id = $1 AND tenant_id = $2`,
			paymentMethodID.String, tenantID,
		).Scan(&cashAccountID)
	}
	if cashAccountID == uuid.Nil {
		// Fallback: look up by name
		cashAccountID = findAccount(tx, tenantID, orgIDPtr, "bank account", "1010")
		if cashAccountID == uuid.Nil {
			cashAccountID = findAccount(tx, tenantID, orgIDPtr, "cash", "1000")
		}
	}

	// Determine the counterpart account (AR or AP) based on payment type
	var counterAccountID uuid.UUID
	var journalCode, sourceType, debitDesc, creditDesc string

	if paymentType == "receipt" {
		// Inbound: customer pays us → Debit Cash, Credit AR
		counterAccountID = findAccount(tx, tenantID, orgIDPtr, "accounts receivable", "1100")
		journalCode = "CASH_RECEIPTS"
		sourceType = "payment_receipt"
		debitDesc = "Cash Receipt"
		creditDesc = "Accounts Receivable"
	} else {
		// Outbound: we pay vendor → Debit AP, Credit Cash
		counterAccountID = findAccount(tx, tenantID, orgIDPtr, "accounts payable", "2000")
		journalCode = "CASH_DISBURSEMENTS"
		sourceType = "payment"
		debitDesc = "Accounts Payable"
		creditDesc = "Cash/Bank"
	}

	// --- Create journal entry for the payment (inside a SAVEPOINT so failures don't abort the tx) ---
	if cashAccountID != uuid.Nil && counterAccountID != uuid.Nil {
		// Get journal — prefer stored journal_id from payment, fall back to code-based lookup
		var journalID uuid.UUID
		var nextNumber int
		var numberPrefix sql.NullString

		if storedJournalID.Valid {
			_ = tx.QueryRow(`
				SELECT id, COALESCE(next_number, 1), number_prefix
				FROM journals WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
				storedJournalID.String, tenantID,
			).Scan(&journalID, &nextNumber, &numberPrefix)
		}
		if journalID == uuid.Nil {
			_ = tx.QueryRow(`
				SELECT id, COALESCE(next_number, 1), number_prefix
				FROM journals WHERE tenant_id = $1 AND (code = $2 OR code = 'GENERAL') AND deleted_at IS NULL
				ORDER BY CASE WHEN code = $2 THEN 0 ELSE 1 END LIMIT 1`,
				tenantID, journalCode,
			).Scan(&journalID, &nextNumber, &numberPrefix)
		}

		if journalID != uuid.Nil {
			// Use a SAVEPOINT so that a JE failure doesn't poison the entire transaction
			tx.Exec("SAVEPOINT create_payment_je")

			prefix := ""
			if numberPrefix.Valid {
				prefix = numberPrefix.String
			}

			// Use journal-scoped max filtered by prefix to avoid date-embedded entry numbers
			var maxNum int
			if prefix != "" {
				_ = tx.QueryRow(
					"SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(entry_number, '[^0-9]', '', 'g') AS BIGINT)), 0) FROM journal_entries WHERE tenant_id = $1 AND journal_id = $2 AND entry_number LIKE $3 AND deleted_at IS NULL",
					tenantID, journalID, prefix+"%",
				).Scan(&maxNum)
			} else {
				_ = tx.QueryRow(
					"SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(entry_number, '[^0-9]', '', 'g') AS BIGINT)), 0) FROM journal_entries WHERE tenant_id = $1 AND journal_id = $2 AND deleted_at IS NULL",
					tenantID, journalID,
				).Scan(&maxNum)
			}
			actualNum := maxNum + 1
			if nextNumber > actualNum {
				actualNum = nextNumber
			}
			entryNumber := fmt.Sprintf("%s%06d", prefix, actualNum)

			description := fmt.Sprintf("Payment %s confirmed", paymentNumber)
			journalEntryID := uuid.New()

			_, jeErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
				journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, paymentDate, paymentNumber, description,
				sourceType, id.String(), 1.0, amount, amount, userID, now, now,
			)

			if jeErr != nil {
				h.log.Error("Failed to create payment journal entry, rolling back savepoint", "error", jeErr)
				tx.Exec("ROLLBACK TO SAVEPOINT create_payment_je")
			} else {
				if paymentType == "receipt" {
					// Receipt: Debit Cash, Credit AR
					line1ID := uuid.New()
					tx.Exec(`
						INSERT INTO journal_entry_lines (
							id, journal_entry_id, line_number, account_id, contact_id, description,
							debit_amount, credit_amount, exchange_rate, created_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
						line1ID, journalEntryID, 1, cashAccountID, contactID, debitDesc,
						amount, 0.0, 1.0, now,
					)
					line2ID := uuid.New()
					tx.Exec(`
						INSERT INTO journal_entry_lines (
							id, journal_entry_id, line_number, account_id, contact_id, description,
							debit_amount, credit_amount, exchange_rate, created_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
						line2ID, journalEntryID, 2, counterAccountID, contactID, creditDesc,
						0.0, amount, 1.0, now,
					)

					// Update account balances
					// Cash: debit-normal, debit increases balance
					tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", amount, now, cashAccountID)
					// AR: debit-normal, credit decreases balance
					tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", amount, now, counterAccountID)
				} else {
					// Payment: Debit AP, Credit Cash
					line1ID := uuid.New()
					tx.Exec(`
						INSERT INTO journal_entry_lines (
							id, journal_entry_id, line_number, account_id, contact_id, description,
							debit_amount, credit_amount, exchange_rate, created_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
						line1ID, journalEntryID, 1, counterAccountID, contactID, debitDesc,
						amount, 0.0, 1.0, now,
					)
					line2ID := uuid.New()
					tx.Exec(`
						INSERT INTO journal_entry_lines (
							id, journal_entry_id, line_number, account_id, contact_id, description,
							debit_amount, credit_amount, exchange_rate, created_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
						line2ID, journalEntryID, 2, cashAccountID, contactID, creditDesc,
						0.0, amount, 1.0, now,
					)

					// Update account balances
					// AP: credit-normal, debit decreases balance (we're paying off liability)
					tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", amount, now, counterAccountID)
					// Cash: debit-normal, credit decreases balance
					tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", amount, now, cashAccountID)
				}

				// Update journal next number
				tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", journalID)

				// Link journal entry to payment
				tx.Exec("UPDATE payments SET journal_entry_id = $1 WHERE id = $2", journalEntryID, id)

				tx.Exec("RELEASE SAVEPOINT create_payment_je")
			}
		} else {
			h.log.Warn("No suitable journal found for payment GL entry", "code", journalCode)
		}
	} else {
		h.log.Warn("Cannot create payment journal entry: missing accounts",
			"has_cash_account", cashAccountID != uuid.Nil,
			"has_counter_account", counterAccountID != uuid.Nil,
			"payment_type", paymentType)
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit payment confirmation", "error", err)
		response.InternalError(c, "Failed to confirm payment")
		return
	}

	response.Success(c, gin.H{"message": "Payment confirmed successfully", "confirmed_at": now})
}

// =====================================================
// TAX RATE HANDLERS
// =====================================================

// ListTaxRates godoc
// @Summary List tax rates
// @Description Get a list of all tax rates
// @Tags Finance - Tax Rates
// @Accept json
// @Produce json
// @Param include_inactive query bool false "Include inactive tax rates"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/tax-rates [get]
func (h *Handler) ListTaxRates(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	includeInactive := c.Query("include_inactive") == "true"

	query := `
		SELECT id, tenant_id, code, name, description, rate, type, tax_type,
			   tax_account_id, is_compound, is_recoverable, COALESCE(price_include, false), is_active, created_at, updated_at
		FROM tax_rates
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	args := []interface{}{tenantID}

	if !includeInactive {
		query += " AND is_active = true"
	}

	query += " ORDER BY code ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list tax rates", "error", err)
		response.InternalError(c, "Failed to list tax rates")
		return
	}
	defer rows.Close()

	rates := make([]*entity.TaxRate, 0)
	for rows.Next() {
		var tr entity.TaxRate
		var desc, taxAccID, taxType sql.NullString

		err := rows.Scan(
			&tr.ID, &tr.TenantID, &tr.Code, &tr.Name, &desc, &tr.Rate, &tr.Type, &taxType,
			&taxAccID, &tr.IsCompound, &tr.IsRecoverable, &tr.PriceInclude, &tr.IsActive, &tr.CreatedAt, &tr.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if desc.Valid {
			tr.Description = &desc.String
		}
		if taxType.Valid {
			tr.TaxType = taxType.String
		} else {
			tr.TaxType = "sales" // Default
		}
		if taxAccID.Valid {
			tid, _ := uuid.Parse(taxAccID.String)
			tr.TaxAccountID = &tid
		}

		rates = append(rates, &tr)
	}

	response.Success(c, rates)
}

// CreateTaxRate godoc
// @Summary Create a new tax rate
// @Description Create a new tax rate configuration
// @Tags Finance - Tax Rates
// @Accept json
// @Produce json
// @Param body body entity.CreateTaxRateInput true "Tax rate creation data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 409 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/tax-rates [post]
func (h *Handler) CreateTaxRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateTaxRateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check for duplicate code
	var exists bool
	h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM tax_rates WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)",
		tenantID, input.Code).Scan(&exists)
	if exists {
		response.Conflict(c, "Tax rate with this code already exists")
		return
	}

	id := uuid.New()
	now := time.Now()

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	var taxAccountID *uuid.UUID
	if input.TaxAccountID != nil && *input.TaxAccountID != "" {
		tid, _ := uuid.Parse(*input.TaxAccountID)
		taxAccountID = &tid
	}

	// Default tax_type to 'sales' if not provided
	taxType := input.TaxType
	if taxType == "" {
		taxType = "sales"
	}

	_, err := h.db.Exec(`
		INSERT INTO tax_rates (id, tenant_id, code, name, description, rate, type, tax_type,
			tax_account_id, is_compound, is_recoverable, price_include, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, id, tenantID, input.Code, input.Name, description, input.Rate, input.Type, taxType,
		taxAccountID, input.IsCompound, input.IsRecoverable, input.PriceInclude, true, now, now)

	if err != nil {
		h.log.Error("Failed to create tax rate", "error", err)
		response.InternalError(c, "Failed to create tax rate")
		return
	}

	tr := &entity.TaxRate{
		ID:            id,
		TenantID:      tenantID,
		Code:          input.Code,
		Name:          input.Name,
		Description:   description,
		Rate:          input.Rate,
		Type:          input.Type,
		TaxType:       taxType,
		TaxAccountID:  taxAccountID,
		IsCompound:    input.IsCompound,
		IsRecoverable: input.IsRecoverable,
		PriceInclude:  input.PriceInclude,
		IsActive:      true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	response.Created(c, tr)
}

// GetTaxRate godoc
// @Summary Get tax rate by ID
// @Description Get detailed information about a specific tax rate
// @Tags Finance - Tax Rates
// @Accept json
// @Produce json
// @Param id path string true "Tax Rate ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/tax-rates/{id} [get]
func (h *Handler) GetTaxRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid tax rate ID")
		return
	}

	var tr entity.TaxRate
	var desc, taxAccID, taxType sql.NullString

	err = h.db.QueryRow(`
		SELECT id, tenant_id, code, name, description, rate, type, tax_type,
			   tax_account_id, is_compound, is_recoverable, COALESCE(price_include, false), is_active, created_at, updated_at
		FROM tax_rates
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(
		&tr.ID, &tr.TenantID, &tr.Code, &tr.Name, &desc, &tr.Rate, &tr.Type, &taxType,
		&taxAccID, &tr.IsCompound, &tr.IsRecoverable, &tr.PriceInclude, &tr.IsActive, &tr.CreatedAt, &tr.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Tax rate")
		return
	}
	if err != nil {
		h.log.Error("Failed to get tax rate", "error", err)
		response.InternalError(c, "Failed to get tax rate")
		return
	}

	if desc.Valid {
		tr.Description = &desc.String
	}
	if taxType.Valid {
		tr.TaxType = taxType.String
	} else {
		tr.TaxType = "sales"
	}
	if taxAccID.Valid {
		tid, _ := uuid.Parse(taxAccID.String)
		tr.TaxAccountID = &tid
	}

	response.Success(c, tr)
}

// UpdateTaxRate godoc
// @Summary Update a tax rate
// @Description Update an existing tax rate's information
// @Tags Finance - Tax Rates
// @Accept json
// @Produce json
// @Param id path string true "Tax Rate ID"
// @Param body body entity.UpdateTaxRateInput true "Tax rate update data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/tax-rates/{id} [put]
func (h *Handler) UpdateTaxRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid tax rate ID")
		return
	}

	var input entity.UpdateTaxRateInput
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
	if input.Rate != nil {
		addUpdate("rate", *input.Rate)
	}
	if input.TaxType != nil {
		addUpdate("tax_type", *input.TaxType)
	}
	if input.IsCompound != nil {
		addUpdate("is_compound", *input.IsCompound)
	}
	if input.IsRecoverable != nil {
		addUpdate("is_recoverable", *input.IsRecoverable)
	}
	if input.PriceInclude != nil {
		addUpdate("price_include", *input.PriceInclude)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
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
		UPDATE tax_rates SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tax rate")
		return
	}
	if err != nil {
		h.log.Error("Failed to update tax rate", "error", err)
		response.InternalError(c, "Failed to update tax rate")
		return
	}

	h.GetTaxRate(c)
}

// DeleteTaxRate godoc
// @Summary Delete a tax rate
// @Description Soft-delete a tax rate
// @Tags Finance - Tax Rates
// @Accept json
// @Produce json
// @Param id path string true "Tax Rate ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/tax-rates/{id} [delete]
func (h *Handler) DeleteTaxRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid tax rate ID")
		return
	}

	result, err := h.db.Exec(`
		UPDATE tax_rates SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, time.Now(), id, tenantID)

	if err != nil {
		h.log.Error("Failed to delete tax rate", "error", err)
		response.InternalError(c, "Failed to delete tax rate")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Tax rate")
		return
	}

	response.NoContent(c)
}

// =====================================================
// CURRENCY HANDLERS
// =====================================================

// ListCurrencies godoc
// @Summary List all currencies
// @Description Get a list of all active currencies
// @Tags Finance - Currencies
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/currencies [get]
func (h *Handler) ListCurrencies(c *gin.Context) {
	query := `
		SELECT id, code, name, symbol, decimal_places, is_base_currency, is_active
		FROM currencies
		WHERE is_active = true
		ORDER BY is_base_currency DESC, code ASC
	`

	rows, err := h.db.Query(query)
	if err != nil {
		h.log.Error("Failed to list currencies", "error", err)
		response.InternalError(c, "Failed to list currencies")
		return
	}
	defer rows.Close()

	currencies := make([]*entity.Currency, 0)
	for rows.Next() {
		var cur entity.Currency
		err := rows.Scan(&cur.ID, &cur.Code, &cur.Name, &cur.Symbol, &cur.DecimalPlaces, &cur.IsBaseCurrency, &cur.IsActive)
		if err != nil {
			continue
		}
		currencies = append(currencies, &cur)
	}

	response.Success(c, currencies)
}

// GetCurrency godoc
// @Summary Get currency by code
// @Description Get detailed information about a specific currency
// @Tags Finance - Currencies
// @Accept json
// @Produce json
// @Param code path string true "Currency Code (e.g., USD, EUR)"
// @Success 200 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/currencies/{code} [get]
func (h *Handler) GetCurrency(c *gin.Context) {
	code := c.Param("code")

	var cur entity.Currency
	err := h.db.QueryRow(`
		SELECT id, code, name, symbol, decimal_places, is_base_currency, is_active
		FROM currencies WHERE code = $1
	`, code).Scan(&cur.ID, &cur.Code, &cur.Name, &cur.Symbol, &cur.DecimalPlaces, &cur.IsBaseCurrency, &cur.IsActive)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Currency")
		return
	}
	if err != nil {
		h.log.Error("Failed to get currency", "error", err)
		response.InternalError(c, "Failed to get currency")
		return
	}

	response.Success(c, cur)
}

// CreateCurrency godoc
// @Summary Create a new currency
// @Description Create a new currency configuration
// @Tags Finance - Currencies
// @Accept json
// @Produce json
// @Param body body entity.CreateCurrencyInput true "Currency creation data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 409 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/currencies [post]
func (h *Handler) CreateCurrency(c *gin.Context) {
	var input entity.CreateCurrencyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if currency code already exists (including inactive ones)
	var existingID uuid.UUID
	var isActive bool
	err := h.db.QueryRow("SELECT id, is_active FROM currencies WHERE code = $1", input.Code).Scan(&existingID, &isActive)

	if err == nil {
		// Currency exists
		if isActive {
			response.BadRequest(c, "Currency with this code already exists")
			return
		}

		// Currency exists but is inactive - reactivate it with new data
		if input.IsBaseCurrency {
			h.db.Exec("UPDATE currencies SET is_base_currency = false WHERE is_base_currency = true")
		}

		_, err = h.db.Exec(`
			UPDATE currencies
			SET name = $1, symbol = $2, decimal_places = $3, is_base_currency = $4, is_active = true
			WHERE id = $5
		`, input.Name, input.Symbol, input.DecimalPlaces, input.IsBaseCurrency, existingID)

		if err != nil {
			h.log.Error("Failed to reactivate currency", "error", err)
			response.InternalError(c, "Failed to create currency")
			return
		}

		cur := entity.Currency{
			ID:             existingID,
			Code:           input.Code,
			Name:           input.Name,
			Symbol:         input.Symbol,
			DecimalPlaces:  input.DecimalPlaces,
			IsBaseCurrency: input.IsBaseCurrency,
			IsActive:       true,
		}

		response.Created(c, cur)
		return
	}

	// Currency doesn't exist - create new one
	if input.IsBaseCurrency {
		h.db.Exec("UPDATE currencies SET is_base_currency = false WHERE is_base_currency = true")
	}

	id := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO currencies (id, code, name, symbol, decimal_places, is_base_currency, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, true)
	`, id, input.Code, input.Name, input.Symbol, input.DecimalPlaces, input.IsBaseCurrency)

	if err != nil {
		h.log.Error("Failed to create currency", "error", err)
		response.InternalError(c, "Failed to create currency")
		return
	}

	cur := entity.Currency{
		ID:             id,
		Code:           input.Code,
		Name:           input.Name,
		Symbol:         input.Symbol,
		DecimalPlaces:  input.DecimalPlaces,
		IsBaseCurrency: input.IsBaseCurrency,
		IsActive:       true,
	}

	response.Created(c, cur)
}

// UpdateCurrency godoc
// @Summary Update a currency
// @Description Update an existing currency's information
// @Tags Finance - Currencies
// @Accept json
// @Produce json
// @Param code path string true "Currency Code"
// @Param body body entity.UpdateCurrencyInput true "Currency update data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/currencies/{code} [put]
func (h *Handler) UpdateCurrency(c *gin.Context) {
	code := c.Param("code")

	var input entity.UpdateCurrencyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if currency exists
	var cur entity.Currency
	err := h.db.QueryRow(`
		SELECT id, code, name, symbol, decimal_places, is_base_currency, is_active
		FROM currencies WHERE code = $1
	`, code).Scan(&cur.ID, &cur.Code, &cur.Name, &cur.Symbol, &cur.DecimalPlaces, &cur.IsBaseCurrency, &cur.IsActive)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Currency")
		return
	}
	if err != nil {
		h.log.Error("Failed to get currency", "error", err)
		response.InternalError(c, "Failed to update currency")
		return
	}

	// If setting as base currency, unset other base currencies
	if input.IsBaseCurrency != nil && *input.IsBaseCurrency {
		h.db.Exec("UPDATE currencies SET is_base_currency = false WHERE is_base_currency = true AND code != $1", code)
	}

	// Update fields
	if input.Name != nil {
		cur.Name = *input.Name
	}
	if input.Symbol != nil {
		cur.Symbol = *input.Symbol
	}
	if input.DecimalPlaces != nil {
		cur.DecimalPlaces = *input.DecimalPlaces
	}
	if input.IsBaseCurrency != nil {
		cur.IsBaseCurrency = *input.IsBaseCurrency
	}
	if input.IsActive != nil {
		cur.IsActive = *input.IsActive
	}

	_, err = h.db.Exec(`
		UPDATE currencies SET name = $1, symbol = $2, decimal_places = $3, is_base_currency = $4, is_active = $5
		WHERE code = $6
	`, cur.Name, cur.Symbol, cur.DecimalPlaces, cur.IsBaseCurrency, cur.IsActive, code)

	if err != nil {
		h.log.Error("Failed to update currency", "error", err)
		response.InternalError(c, "Failed to update currency")
		return
	}

	response.Success(c, cur)
}

// DeleteCurrency godoc
// @Summary Delete a currency
// @Description Soft-delete a currency by setting is_active to false
// @Tags Finance - Currencies
// @Accept json
// @Produce json
// @Param code path string true "Currency Code"
// @Success 200 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/currencies/{code} [delete]
func (h *Handler) DeleteCurrency(c *gin.Context) {
	code := c.Param("code")

	// Check if currency is base currency
	var isBase bool
	err := h.db.QueryRow("SELECT is_base_currency FROM currencies WHERE code = $1", code).Scan(&isBase)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Currency")
		return
	}
	if err != nil {
		h.log.Error("Failed to get currency", "error", err)
		response.InternalError(c, "Failed to delete currency")
		return
	}

	if isBase {
		response.BadRequest(c, "Cannot delete base currency")
		return
	}

	// Soft delete by setting is_active = false
	_, err = h.db.Exec("UPDATE currencies SET is_active = false WHERE code = $1", code)
	if err != nil {
		h.log.Error("Failed to delete currency", "error", err)
		response.InternalError(c, "Failed to delete currency")
		return
	}

	response.Success(c, gin.H{"message": "Currency deleted successfully"})
}

// GetExchangeRate godoc
// @Summary Get exchange rate
// @Description Get the current exchange rate for a specific currency
// @Tags Finance - Exchange Rates
// @Accept json
// @Produce json
// @Param code path string true "Currency Code"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/exchange-rates/{code} [get]
func (h *Handler) GetExchangeRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	code := c.Param("code")
	date := c.Query("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	// Get currency ID
	var currencyID uuid.UUID
	err := h.db.QueryRow("SELECT id FROM currencies WHERE code = $1", code).Scan(&currencyID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Currency")
		return
	}
	if err != nil {
		h.log.Error("Failed to get currency", "error", err)
		response.InternalError(c, "Failed to get exchange rate")
		return
	}

	// Get base currency
	var baseCurrencyID uuid.UUID
	h.db.QueryRow("SELECT id FROM currencies WHERE is_base_currency = true LIMIT 1").Scan(&baseCurrencyID)

	// Get latest rate on or before date
	var rate float64
	var effectiveDate time.Time
	err = h.db.QueryRow(`
		SELECT rate, effective_date FROM exchange_rates
		WHERE tenant_id = $1 AND from_currency_id = $2 AND to_currency_id = $3 AND effective_date <= $4
		ORDER BY effective_date DESC LIMIT 1
	`, tenantID, currencyID, baseCurrencyID, date).Scan(&rate, &effectiveDate)

	if err == sql.ErrNoRows {
		// Return default rate of 1.0 if no rate found
		rate = 1.0
		effectiveDate = time.Now()
	} else if err != nil {
		h.log.Error("Failed to get exchange rate", "error", err)
		response.InternalError(c, "Failed to get exchange rate")
		return
	}

	response.Success(c, gin.H{
		"currency_code":  code,
		"rate":           rate,
		"effective_date": effectiveDate.Format("2006-01-02"),
	})
}

// SetExchangeRate godoc
// @Summary Set exchange rate
// @Description Create or update an exchange rate for a specific currency
// @Tags Finance - Exchange Rates
// @Accept json
// @Produce json
// @Param code path string true "Currency Code"
// @Param body body object true "Exchange rate data with rate and effective_date fields"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/exchange-rates/{code} [post]
func (h *Handler) SetExchangeRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	code := c.Param("code")

	var input struct {
		Rate   float64 `json:"rate" binding:"required,gt=0"`
		Date   string  `json:"date"`
		Source string  `json:"source"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Default date to today
	date := input.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	// Default source to manual
	source := input.Source
	if source == "" {
		source = "manual"
	}

	// Get currency ID
	var currencyID uuid.UUID
	err := h.db.QueryRow("SELECT id FROM currencies WHERE code = $1", code).Scan(&currencyID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Currency")
		return
	}
	if err != nil {
		h.log.Error("Failed to get currency", "error", err)
		response.InternalError(c, "Failed to set exchange rate")
		return
	}

	// Get base currency (or use a default like UZS)
	var baseCurrencyID uuid.UUID
	err = h.db.QueryRow("SELECT id FROM currencies WHERE is_base_currency = true LIMIT 1").Scan(&baseCurrencyID)
	if err == sql.ErrNoRows {
		// If no base currency set, try to use UZS
		err = h.db.QueryRow("SELECT id FROM currencies WHERE code = 'UZS' LIMIT 1").Scan(&baseCurrencyID)
		if err != nil {
			// Use first currency as base
			err = h.db.QueryRow("SELECT id FROM currencies LIMIT 1").Scan(&baseCurrencyID)
		}
	}
	if err != nil {
		h.log.Error("Failed to get base currency", "error", err)
		response.InternalError(c, "Failed to set exchange rate - no base currency")
		return
	}

	// Check if rate exists for this date
	var existingID uuid.UUID
	err = h.db.QueryRow(`
		SELECT id FROM exchange_rates
		WHERE tenant_id = $1 AND from_currency_id = $2 AND to_currency_id = $3 AND effective_date = $4
	`, tenantID, currencyID, baseCurrencyID, date).Scan(&existingID)

	if err == nil {
		// Update existing rate
		_, err = h.db.Exec(`
			UPDATE exchange_rates SET rate = $1, source = $2 WHERE id = $3
		`, input.Rate, source, existingID)
		if err != nil {
			h.log.Error("Failed to update exchange rate", "error", err)
			response.InternalError(c, "Failed to update exchange rate")
			return
		}
	} else if err == sql.ErrNoRows {
		// Create new rate
		id := uuid.New()
		_, err = h.db.Exec(`
			INSERT INTO exchange_rates (id, tenant_id, from_currency_id, to_currency_id, rate, effective_date, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, id, tenantID, currencyID, baseCurrencyID, input.Rate, date, source)
		if err != nil {
			h.log.Error("Failed to create exchange rate", "error", err)
			response.InternalError(c, "Failed to create exchange rate")
			return
		}
	} else {
		h.log.Error("Failed to check existing rate", "error", err)
		response.InternalError(c, "Failed to set exchange rate")
		return
	}

	response.Created(c, gin.H{
		"currency_code":  code,
		"rate":           input.Rate,
		"effective_date": date,
		"source":         source,
	})
}

// ListExchangeRates godoc
// @Summary List exchange rates
// @Description Get a list of all exchange rates for the tenant
// @Tags Finance - Exchange Rates
// @Accept json
// @Produce json
// @Param date_from query string false "Date from (YYYY-MM-DD)"
// @Param date_to query string false "Date to (YYYY-MM-DD)"
// @Param currency query string false "Filter by currency code"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/exchange-rates [get]
func (h *Handler) ListExchangeRates(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	currencyCode := c.Query("currency")

	query := `
		SELECT er.id, er.rate, er.effective_date, er.source, er.created_at,
		       fc.code as from_currency, tc.code as to_currency
		FROM exchange_rates er
		JOIN currencies fc ON er.from_currency_id = fc.id
		JOIN currencies tc ON er.to_currency_id = tc.id
		WHERE er.tenant_id = $1
	`
	args := []interface{}{tenantID}
	argIndex := 2

	if dateFrom != "" {
		query += fmt.Sprintf(" AND er.effective_date >= $%d", argIndex)
		args = append(args, dateFrom)
		argIndex++
	}

	if dateTo != "" {
		query += fmt.Sprintf(" AND er.effective_date <= $%d", argIndex)
		args = append(args, dateTo)
		argIndex++
	}

	if currencyCode != "" {
		query += fmt.Sprintf(" AND fc.code = $%d", argIndex)
		args = append(args, currencyCode)
		argIndex++
	}

	query += " ORDER BY er.effective_date DESC, fc.code ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list exchange rates", "error", err)
		response.InternalError(c, "Failed to list exchange rates")
		return
	}
	defer rows.Close()

	type ExchangeRateResponse struct {
		ID            uuid.UUID `json:"id"`
		FromCurrency  string    `json:"from_currency"`
		ToCurrency    string    `json:"to_currency"`
		Rate          float64   `json:"rate"`
		EffectiveDate string    `json:"effective_date"`
		Source        string    `json:"source"`
		CreatedAt     time.Time `json:"created_at"`
	}

	rates := make([]ExchangeRateResponse, 0)
	for rows.Next() {
		var r ExchangeRateResponse
		var effectiveDate time.Time
		var source sql.NullString
		err := rows.Scan(&r.ID, &r.Rate, &effectiveDate, &source, &r.CreatedAt, &r.FromCurrency, &r.ToCurrency)
		if err != nil {
			continue
		}
		r.EffectiveDate = effectiveDate.Format("2006-01-02")
		r.Source = source.String
		if r.Source == "" {
			r.Source = "manual"
		}
		rates = append(rates, r)
	}

	response.Success(c, rates)
}

// =====================================================
// BANK ACCOUNTS
// =====================================================

// ListBankAccounts godoc
// @Summary List bank accounts
// @Description Get a list of all bank accounts for the tenant
// @Tags Finance - Bank Accounts
// @Accept json
// @Produce json
// @Param is_active query bool false "Filter by active status"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-accounts [get]
func (h *Handler) ListBankAccounts(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	var filter entity.BankAccountListFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	query := `
		SELECT id, tenant_id, organization_id, COALESCE(name, bank_name) as name, bank_name,
		       COALESCE(account_number, '') as account_number,
		       COALESCE(currency, 'UZS') as currency, COALESCE(account_type, 'checking') as account_type,
		       COALESCE(balance, 0) as balance, COALESCE(is_active, true) as is_active,
		       last_reconciled,
		       account_id, created_at, updated_at
		FROM bank_accounts
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argIndex := 2

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += fmt.Sprintf(" AND organization_id = $%d", argIndex)
		args = append(args, orgID)
		argIndex++
	}

	if filter.Search != "" {
		query += fmt.Sprintf(" AND (COALESCE(name, bank_name) ILIKE $%d OR bank_name ILIKE $%d OR account_number ILIKE $%d)", argIndex, argIndex, argIndex)
		args = append(args, "%"+filter.Search+"%")
		argIndex++
	}

	if filter.Currency != "" {
		query += fmt.Sprintf(" AND COALESCE(currency, 'UZS') = $%d", argIndex)
		args = append(args, filter.Currency)
		argIndex++
	}

	if filter.AccountType != "" {
		query += fmt.Sprintf(" AND COALESCE(account_type, 'checking') = $%d", argIndex)
		args = append(args, filter.AccountType)
		argIndex++
	}

	if filter.IsActive != nil {
		query += fmt.Sprintf(" AND COALESCE(is_active, true) = $%d", argIndex)
		args = append(args, *filter.IsActive)
		argIndex++
	}

	query += " ORDER BY COALESCE(name, bank_name) ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list bank accounts", "error", err)
		response.InternalError(c, "Failed to list bank accounts")
		return
	}
	defer rows.Close()

	var accounts []entity.BankAccount
	for rows.Next() {
		var acc entity.BankAccount
		var organizationID, accountID sql.NullString
		var lastReconciled sql.NullTime
		var name, accountNumber sql.NullString

		err := rows.Scan(
			&acc.ID, &acc.TenantID, &organizationID, &name, &acc.BankName,
			&accountNumber, &acc.Currency, &acc.AccountType, &acc.Balance,
			&acc.IsActive, &lastReconciled, &accountID, &acc.CreatedAt, &acc.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan bank account", "error", err)
			continue
		}

		if name.Valid {
			acc.Name = name.String
		} else {
			acc.Name = acc.BankName
		}
		if accountNumber.Valid {
			acc.AccountNumber = accountNumber.String
		}
		if organizationID.Valid {
			orgID, _ := uuid.Parse(organizationID.String)
			acc.OrganizationID = &orgID
		}
		if accountID.Valid {
			accID, _ := uuid.Parse(accountID.String)
			acc.AccountID = &accID
		}
		if lastReconciled.Valid {
			acc.LastReconciled = &lastReconciled.Time
		}

		accounts = append(accounts, acc)
	}

	response.Success(c, accounts)
}

// GetBankAccount godoc
// @Summary Get bank account by ID
// @Description Get detailed information about a specific bank account
// @Tags Finance - Bank Accounts
// @Accept json
// @Produce json
// @Param id path string true "Bank Account ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-accounts/{id} [get]
func (h *Handler) GetBankAccount(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Bank account ID is required")
		return
	}

	var acc entity.BankAccount
	var organizationID, accountID sql.NullString
	var lastReconciled sql.NullTime
	var name, accountNumber sql.NullString

	err := h.db.QueryRow(`
		SELECT id, tenant_id, organization_id, COALESCE(name, bank_name) as name, bank_name,
		       COALESCE(account_number, '') as account_number,
		       COALESCE(currency, 'UZS') as currency, COALESCE(account_type, 'checking') as account_type,
		       COALESCE(balance, 0) as balance, COALESCE(is_active, true) as is_active,
		       COALESCE(last_reconciled, last_reconciled_date) as last_reconciled,
		       account_id, created_at, updated_at
		FROM bank_accounts
		WHERE id = $1 AND tenant_id = $2 AND (deleted_at IS NULL OR deleted_at IS NULL)
	`, id, tenantID).Scan(
		&acc.ID, &acc.TenantID, &organizationID, &name, &acc.BankName,
		&accountNumber, &acc.Currency, &acc.AccountType, &acc.Balance,
		&acc.IsActive, &lastReconciled, &accountID, &acc.CreatedAt, &acc.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Bank account not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get bank account", "error", err)
		response.InternalError(c, "Failed to get bank account")
		return
	}

	if name.Valid {
		acc.Name = name.String
	} else {
		acc.Name = acc.BankName
	}
	if accountNumber.Valid {
		acc.AccountNumber = accountNumber.String
	}
	if organizationID.Valid {
		orgID, _ := uuid.Parse(organizationID.String)
		acc.OrganizationID = &orgID
	}
	if accountID.Valid {
		accID, _ := uuid.Parse(accountID.String)
		acc.AccountID = &accID
	}
	if lastReconciled.Valid {
		acc.LastReconciled = &lastReconciled.Time
	}

	response.Success(c, acc)
}

// CreateBankAccount godoc
// @Summary Create a new bank account
// @Description Create a new bank account record
// @Tags Finance - Bank Accounts
// @Accept json
// @Produce json
// @Param body body entity.CreateBankAccountInput true "Bank account creation data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 409 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-accounts [post]
func (h *Handler) CreateBankAccount(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	var input entity.CreateBankAccountInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check for duplicate account number
	var exists bool
	err := h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM bank_accounts WHERE tenant_id = $1 AND account_number = $2 AND deleted_at IS NULL)
	`, tenantID, input.AccountNumber).Scan(&exists)
	if err != nil {
		h.log.Error("Failed to check duplicate account number", "error", err)
		response.InternalError(c, "Failed to create bank account")
		return
	}
	if exists {
		response.BadRequest(c, "Account number already exists")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Get organization ID from middleware header
	var orgIDPtr *uuid.UUID
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var accountID *uuid.UUID
	if input.AccountID != nil && *input.AccountID != "" {
		accID, err := uuid.Parse(*input.AccountID)
		if err == nil {
			accountID = &accID
		}
	}

	_, err = h.db.Exec(`
		INSERT INTO bank_accounts (id, tenant_id, organization_id, name, bank_name, account_number, currency,
		                           account_type, balance, is_active, account_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, $10, $11, $11)
	`, id, tenantID, orgIDPtr, input.Name, input.BankName, input.AccountNumber, input.Currency,
		input.AccountType, input.Balance, accountID, now)

	if err != nil {
		h.log.Error("Failed to create bank account", "error", err)
		response.InternalError(c, "Failed to create bank account")
		return
	}

	acc := entity.BankAccount{
		ID:            id,
		TenantID:      uuid.MustParse(tenantID),
		Name:          input.Name,
		BankName:      input.BankName,
		AccountNumber: input.AccountNumber,
		Currency:      input.Currency,
		AccountType:   input.AccountType,
		Balance:       input.Balance,
		IsActive:      true,
		AccountID:     accountID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	response.Created(c, acc)
}

// UpdateBankAccount godoc
// @Summary Update a bank account
// @Description Update an existing bank account's information
// @Tags Finance - Bank Accounts
// @Accept json
// @Produce json
// @Param id path string true "Bank Account ID"
// @Param body body entity.UpdateBankAccountInput true "Bank account update data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-accounts/{id} [put]
func (h *Handler) UpdateBankAccount(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Bank account ID is required")
		return
	}

	var input entity.UpdateBankAccountInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check if bank account exists
	var exists bool
	err := h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM bank_accounts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)
	`, id, tenantID).Scan(&exists)
	if err != nil {
		h.log.Error("Failed to check bank account existence", "error", err)
		response.InternalError(c, "Failed to update bank account")
		return
	}
	if !exists {
		response.NotFound(c, "Bank account not found")
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if input.Name != nil {
		updates = append(updates, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, *input.Name)
		argIndex++
	}
	if input.BankName != nil {
		updates = append(updates, fmt.Sprintf("bank_name = $%d", argIndex))
		args = append(args, *input.BankName)
		argIndex++
	}
	if input.AccountNumber != nil {
		// Check for duplicate account number
		var duplicateExists bool
		err := h.db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM bank_accounts WHERE tenant_id = $1 AND account_number = $2 AND id != $3 AND deleted_at IS NULL)
		`, tenantID, *input.AccountNumber, id).Scan(&duplicateExists)
		if err != nil {
			h.log.Error("Failed to check duplicate account number", "error", err)
			response.InternalError(c, "Failed to update bank account")
			return
		}
		if duplicateExists {
			response.BadRequest(c, "Account number already exists")
			return
		}
		updates = append(updates, fmt.Sprintf("account_number = $%d", argIndex))
		args = append(args, *input.AccountNumber)
		argIndex++
	}
	if input.Currency != nil {
		updates = append(updates, fmt.Sprintf("currency = $%d", argIndex))
		args = append(args, *input.Currency)
		argIndex++
	}
	if input.AccountType != nil {
		updates = append(updates, fmt.Sprintf("account_type = $%d", argIndex))
		args = append(args, *input.AccountType)
		argIndex++
	}
	if input.Balance != nil {
		updates = append(updates, fmt.Sprintf("balance = $%d", argIndex))
		args = append(args, *input.Balance)
		argIndex++
	}
	if input.IsActive != nil {
		updates = append(updates, fmt.Sprintf("is_active = $%d", argIndex))
		args = append(args, *input.IsActive)
		argIndex++
	}
	if input.AccountID != nil {
		if *input.AccountID == "" {
			updates = append(updates, fmt.Sprintf("account_id = $%d", argIndex))
			args = append(args, nil)
		} else {
			accID, err := uuid.Parse(*input.AccountID)
			if err == nil {
				updates = append(updates, fmt.Sprintf("account_id = $%d", argIndex))
				args = append(args, accID)
			}
		}
		argIndex++
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	args = append(args, id, tenantID)
	query := fmt.Sprintf(`
		UPDATE bank_accounts SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
	`, strings.Join(updates, ", "), argIndex, argIndex+1)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update bank account", "error", err)
		response.InternalError(c, "Failed to update bank account")
		return
	}

	// Return updated bank account
	h.GetBankAccount(c)
}

// DeleteBankAccount godoc
// @Summary Delete a bank account
// @Description Soft-delete a bank account
// @Tags Finance - Bank Accounts
// @Accept json
// @Produce json
// @Param id path string true "Bank Account ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-accounts/{id} [delete]
func (h *Handler) DeleteBankAccount(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Bank account ID is required")
		return
	}

	result, err := h.db.Exec(`
		UPDATE bank_accounts SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete bank account", "error", err)
		response.InternalError(c, "Failed to delete bank account")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Bank account not found")
		return
	}

	response.Success(c, gin.H{"message": "Bank account deleted successfully"})
}

// =====================================================
// BANK TRANSACTIONS
// =====================================================

// ListBankTransactions godoc
// @Summary List bank transactions
// @Description Get a list of all transactions for bank accounts
// @Tags Finance - Bank Transactions
// @Accept json
// @Produce json
// @Param bank_account_id query string false "Filter by bank account ID"
// @Param status query string false "Filter by reconciliation status"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-transactions [get]
func (h *Handler) ListBankTransactions(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	bankAccountID := c.Param("id")
	if bankAccountID == "" {
		response.BadRequest(c, "Bank account ID is required")
		return
	}

	var filter entity.BankTransactionListFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	query := `
		SELECT id, tenant_id, bank_account_id, transaction_date, value_date,
		       COALESCE(reference, '') as reference, COALESCE(description, '') as description,
		       amount, balance_after, transaction_type, COALESCE(status, 'unmatched') as status,
		       matched_journal_entry_id, created_at, updated_at
		FROM bank_transactions
		WHERE tenant_id = $1 AND bank_account_id = $2
	`
	args := []interface{}{tenantID, bankAccountID}
	argIndex := 3

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += fmt.Sprintf(" AND organization_id = $%d", argIndex)
		args = append(args, orgID)
		argIndex++
	}

	if filter.Search != "" {
		query += fmt.Sprintf(" AND (reference ILIKE $%d OR description ILIKE $%d)", argIndex, argIndex)
		args = append(args, "%"+filter.Search+"%")
		argIndex++
	}

	if filter.Type != "" {
		query += fmt.Sprintf(" AND transaction_type = $%d", argIndex)
		args = append(args, filter.Type)
		argIndex++
	}

	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, filter.Status)
		argIndex++
	}

	if filter.DateFrom != "" {
		query += fmt.Sprintf(" AND transaction_date >= $%d", argIndex)
		args = append(args, filter.DateFrom)
		argIndex++
	}

	if filter.DateTo != "" {
		query += fmt.Sprintf(" AND transaction_date <= $%d", argIndex)
		args = append(args, filter.DateTo)
		argIndex++
	}

	query += " ORDER BY transaction_date DESC, created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list bank transactions", "error", err)
		response.InternalError(c, "Failed to list bank transactions")
		return
	}
	defer rows.Close()

	var transactions []entity.BankTransaction
	for rows.Next() {
		var t entity.BankTransaction
		var valueDate sql.NullTime
		var balanceAfter sql.NullFloat64
		var matchedJournalEntryID sql.NullString

		err := rows.Scan(
			&t.ID, &t.TenantID, &t.BankAccountID, &t.TransactionDate, &valueDate,
			&t.Reference, &t.Description, &t.Amount, &balanceAfter, &t.TransactionType,
			&t.Status, &matchedJournalEntryID, &t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan bank transaction", "error", err)
			continue
		}

		if valueDate.Valid {
			t.ValueDate = &valueDate.Time
		}
		if balanceAfter.Valid {
			t.BalanceAfter = &balanceAfter.Float64
		}
		if matchedJournalEntryID.Valid {
			entryID, _ := uuid.Parse(matchedJournalEntryID.String)
			t.MatchedJournalEntryID = &entryID
		}
		t.IsReconciled = t.Status == "reconciled"

		transactions = append(transactions, t)
	}

	response.Success(c, transactions)
}

// CreateBankTransaction godoc
// @Summary Create a new bank transaction
// @Description Create a new bank transaction record
// @Tags Finance - Bank Transactions
// @Accept json
// @Produce json
// @Param id path string true "Bank Account ID"
// @Param body body entity.CreateBankTransactionInput true "Bank transaction creation data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-accounts/{id}/transactions [post]
func (h *Handler) CreateBankTransaction(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	bankAccountID := c.Param("id")
	if bankAccountID == "" {
		response.BadRequest(c, "Bank account ID is required")
		return
	}

	var input entity.CreateBankTransactionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse transaction date
	transactionDate, err := time.Parse("2006-01-02", input.TransactionDate)
	if err != nil {
		response.BadRequest(c, "Invalid transaction date format")
		return
	}

	// Verify bank account exists
	var exists bool
	err = h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM bank_accounts WHERE id = $1 AND tenant_id = $2 AND (deleted_at IS NULL OR deleted_at IS NULL))
	`, bankAccountID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Bank account not found")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Get organization ID from middleware header
	var orgIDPtr *uuid.UUID
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	_, err = h.db.Exec(`
		INSERT INTO bank_transactions (id, tenant_id, organization_id, bank_account_id, transaction_date, reference,
		                               description, amount, transaction_type, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'unmatched', $10, $10)
	`, id, tenantID, orgIDPtr, bankAccountID, transactionDate, input.Reference, input.Description,
		input.Amount, input.Type, now)

	if err != nil {
		h.log.Error("Failed to create bank transaction", "error", err)
		response.InternalError(c, "Failed to create bank transaction")
		return
	}

	// Update bank account balance
	balanceChange := input.Amount
	if input.Type == "debit" {
		balanceChange = -balanceChange
	}
	_, err = h.db.Exec(`
		UPDATE bank_accounts SET balance = COALESCE(balance, 0) + $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4
	`, balanceChange, now, bankAccountID, tenantID)
	if err != nil {
		h.log.Warn("Failed to update bank account balance", "error", err)
	}

	bankAccID, _ := uuid.Parse(bankAccountID)
	t := entity.BankTransaction{
		ID:              id,
		TenantID:        uuid.MustParse(tenantID),
		BankAccountID:   bankAccID,
		TransactionDate: transactionDate,
		Reference:       input.Reference,
		Description:     input.Description,
		Amount:          input.Amount,
		TransactionType: input.Type,
		Status:          "unmatched",
		IsReconciled:    false,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	response.Created(c, t)
}

// ReconcileBankTransaction godoc
// @Summary Reconcile a bank transaction
// @Description Mark a bank transaction as reconciled
// @Tags Finance - Bank Transactions
// @Accept json
// @Produce json
// @Param id path string true "Bank Account ID"
// @Param transactionId path string true "Transaction ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-accounts/{id}/transactions/{transactionId}/reconcile [post]
func (h *Handler) ReconcileBankTransaction(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	bankAccountID := c.Param("id")
	transactionID := c.Param("transactionId")
	if bankAccountID == "" || transactionID == "" {
		response.BadRequest(c, "Bank account ID and transaction ID are required")
		return
	}

	now := time.Now()
	result, err := h.db.Exec(`
		UPDATE bank_transactions SET status = 'reconciled', updated_at = $1
		WHERE id = $2 AND bank_account_id = $3 AND tenant_id = $4
	`, now, transactionID, bankAccountID, tenantID)

	if err != nil {
		h.log.Error("Failed to reconcile bank transaction", "error", err)
		response.InternalError(c, "Failed to reconcile bank transaction")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Bank transaction not found")
		return
	}

	// Update last reconciled date on bank account
	h.db.Exec(`
		UPDATE bank_accounts SET last_reconciled = $1, updated_at = $1 WHERE id = $2 AND tenant_id = $3
	`, now, bankAccountID, tenantID)

	response.Success(c, gin.H{"message": "Transaction reconciled successfully"})
}

// =====================================================
// BANK RECONCILIATION WORKFLOW
// =====================================================

// ListBankReconciliations godoc
// @Summary List bank reconciliations
// @Description Get a list of all bank reconciliation sessions
// @Tags Finance - Bank Reconciliations
// @Accept json
// @Produce json
// @Param bank_account_id query string false "Filter by bank account ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-reconciliations [get]
func (h *Handler) ListBankReconciliations(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	bankAccountID := c.Param("id")
	if bankAccountID == "" {
		response.BadRequest(c, "Bank account ID is required")
		return
	}

	query := `
		SELECT br.id, br.bank_account_id, br.statement_date, br.statement_ending_balance,
		       br.book_balance, br.reconciled_balance, br.difference, br.status,
		       br.completed_at, br.completed_by, br.notes, br.created_at,
		       COALESCE(u.first_name || ' ' || u.last_name, '') as completed_by_name
		FROM bank_reconciliations br
		LEFT JOIN users u ON br.completed_by = u.id
		WHERE br.tenant_id = $1 AND br.bank_account_id = $2
		ORDER BY br.statement_date DESC, br.created_at DESC
	`

	rows, err := h.db.Query(query, tenantID, bankAccountID)
	if err != nil {
		h.log.Error("Failed to list bank reconciliations", "error", err)
		response.InternalError(c, "Failed to list bank reconciliations")
		return
	}
	defer rows.Close()

	type Reconciliation struct {
		ID                    uuid.UUID  `json:"id"`
		BankAccountID         uuid.UUID  `json:"bank_account_id"`
		StatementDate         string     `json:"statement_date"`
		StatementEndingBalance float64   `json:"statement_ending_balance"`
		BookBalance           float64    `json:"book_balance"`
		ReconciledBalance     *float64   `json:"reconciled_balance"`
		Difference            *float64   `json:"difference"`
		Status                string     `json:"status"`
		CompletedAt           *time.Time `json:"completed_at"`
		CompletedBy           *uuid.UUID `json:"completed_by"`
		CompletedByName       string     `json:"completed_by_name"`
		Notes                 *string    `json:"notes"`
		CreatedAt             time.Time  `json:"created_at"`
	}

	reconciliations := make([]Reconciliation, 0)
	for rows.Next() {
		var r Reconciliation
		var statementDate time.Time
		err := rows.Scan(&r.ID, &r.BankAccountID, &statementDate, &r.StatementEndingBalance,
			&r.BookBalance, &r.ReconciledBalance, &r.Difference, &r.Status,
			&r.CompletedAt, &r.CompletedBy, &r.Notes, &r.CreatedAt, &r.CompletedByName)
		if err != nil {
			continue
		}
		r.StatementDate = statementDate.Format("2006-01-02")
		reconciliations = append(reconciliations, r)
	}

	response.Success(c, reconciliations)
}

// CreateBankReconciliation godoc
// @Summary Create a bank reconciliation
// @Description Start a new bank reconciliation session
// @Tags Finance - Bank Reconciliations
// @Accept json
// @Produce json
// @Param statement_date body string true "Statement date"
// @Param statement_ending_balance body number true "Statement ending balance"
// @Param notes body string false "Notes"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-reconciliations [post]
func (h *Handler) CreateBankReconciliation(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	bankAccountID := c.Param("id")
	if bankAccountID == "" {
		response.BadRequest(c, "Bank account ID is required")
		return
	}

	var input struct {
		StatementDate          string  `json:"statement_date" binding:"required"`
		StatementEndingBalance float64 `json:"statement_ending_balance" binding:"required"`
		Notes                  string  `json:"notes"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check for existing draft reconciliation
	var existingID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM bank_reconciliations
		WHERE tenant_id = $1 AND bank_account_id = $2 AND status = 'draft'
		LIMIT 1
	`, tenantID, bankAccountID).Scan(&existingID)

	if err == nil {
		response.BadRequest(c, "A draft reconciliation already exists for this bank account. Please complete or delete it first.")
		return
	}

	// Calculate book balance (sum of all journal entry lines for the linked account)
	var bookBalance float64
	err = h.db.QueryRow(`
		SELECT COALESCE(SUM(jel.debit_amount) - SUM(jel.credit_amount), 0)
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		JOIN bank_accounts ba ON jel.account_id = ba.account_id
		WHERE ba.id = $1 AND ba.tenant_id = $2
		  AND je.status = 'posted' AND je.deleted_at IS NULL
		  AND je.entry_date <= $3
	`, bankAccountID, tenantID, input.StatementDate).Scan(&bookBalance)

	if err != nil {
		// Fallback to bank account balance
		h.db.QueryRow(`SELECT COALESCE(balance, 0) FROM bank_accounts WHERE id = $1 AND tenant_id = $2`,
			bankAccountID, tenantID).Scan(&bookBalance)
	}

	// Get organization ID from middleware header
	var orgIDPtr *uuid.UUID
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Create reconciliation
	var reconciliationID uuid.UUID
	err = h.db.QueryRow(`
		INSERT INTO bank_reconciliations (tenant_id, organization_id, bank_account_id, statement_date, statement_ending_balance, book_balance, status, notes)
		VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7)
		RETURNING id
	`, tenantID, orgIDPtr, bankAccountID, input.StatementDate, input.StatementEndingBalance, bookBalance, input.Notes).Scan(&reconciliationID)

	if err != nil {
		h.log.Error("Failed to create bank reconciliation", "error", err)
		response.InternalError(c, "Failed to create bank reconciliation")
		return
	}

	response.Success(c, gin.H{
		"id":                       reconciliationID,
		"bank_account_id":          bankAccountID,
		"statement_date":           input.StatementDate,
		"statement_ending_balance": input.StatementEndingBalance,
		"book_balance":             bookBalance,
		"status":                   "draft",
	})
}

// GetBankReconciliation godoc
// @Summary Get bank reconciliation by ID
// @Description Get detailed information about a specific bank reconciliation with its items
// @Tags Finance - Bank Reconciliations
// @Accept json
// @Produce json
// @Param id path string true "Reconciliation ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-reconciliations/{id} [get]
func (h *Handler) GetBankReconciliation(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	reconciliationID := c.Param("reconciliationId")
	if reconciliationID == "" {
		response.BadRequest(c, "Reconciliation ID is required")
		return
	}

	// Get reconciliation details
	var r struct {
		ID                     uuid.UUID  `json:"id"`
		BankAccountID          uuid.UUID  `json:"bank_account_id"`
		StatementDate          string     `json:"statement_date"`
		StatementEndingBalance float64    `json:"statement_ending_balance"`
		BookBalance            float64    `json:"book_balance"`
		ReconciledBalance      *float64   `json:"reconciled_balance"`
		Difference             *float64   `json:"difference"`
		Status                 string     `json:"status"`
		CompletedAt            *time.Time `json:"completed_at"`
		Notes                  *string    `json:"notes"`
		CreatedAt              time.Time  `json:"created_at"`
	}

	var statementDate time.Time
	err := h.db.QueryRow(`
		SELECT id, bank_account_id, statement_date, statement_ending_balance, book_balance,
		       reconciled_balance, difference, status, completed_at, notes, created_at
		FROM bank_reconciliations
		WHERE id = $1 AND tenant_id = $2
	`, reconciliationID, tenantID).Scan(&r.ID, &r.BankAccountID, &statementDate, &r.StatementEndingBalance,
		&r.BookBalance, &r.ReconciledBalance, &r.Difference, &r.Status, &r.CompletedAt, &r.Notes, &r.CreatedAt)

	if err != nil {
		response.NotFound(c, "Reconciliation not found")
		return
	}
	r.StatementDate = statementDate.Format("2006-01-02")

	// Get unreconciled bank transactions
	bankTxRows, err := h.db.Query(`
		SELECT id, transaction_date, reference, description, amount, transaction_type,
		       COALESCE(is_reconciled, false) as is_reconciled, status
		FROM bank_transactions
		WHERE bank_account_id = $1 AND tenant_id = $2
		  AND transaction_date <= $3
		  AND (is_reconciled = false OR is_reconciled IS NULL OR reconciliation_id = $4)
		ORDER BY transaction_date, created_at
	`, r.BankAccountID, tenantID, r.StatementDate, reconciliationID)

	if err != nil {
		h.log.Error("Failed to get bank transactions", "error", err)
	}

	type BankTransaction struct {
		ID              uuid.UUID `json:"id"`
		TransactionDate string    `json:"transaction_date"`
		Reference       *string   `json:"reference"`
		Description     *string   `json:"description"`
		Amount          float64   `json:"amount"`
		TransactionType string    `json:"transaction_type"`
		IsReconciled    bool      `json:"is_reconciled"`
		Status          string    `json:"status"`
	}

	bankTransactions := make([]BankTransaction, 0)
	if bankTxRows != nil {
		defer bankTxRows.Close()
		for bankTxRows.Next() {
			var tx BankTransaction
			var txDate time.Time
			bankTxRows.Scan(&tx.ID, &txDate, &tx.Reference, &tx.Description, &tx.Amount,
				&tx.TransactionType, &tx.IsReconciled, &tx.Status)
			tx.TransactionDate = txDate.Format("2006-01-02")
			bankTransactions = append(bankTransactions, tx)
		}
	}

	// Get unreconciled journal entries for the bank account
	jeRows, err := h.db.Query(`
		SELECT jel.id, je.entry_date, je.entry_number, je.description,
		       jel.debit_amount, jel.credit_amount, jel.reconciled
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		JOIN bank_accounts ba ON jel.account_id = ba.account_id
		WHERE ba.id = $1 AND ba.tenant_id = $2
		  AND je.status = 'posted' AND je.deleted_at IS NULL
		  AND je.entry_date <= $3
		  AND (jel.reconciled = false OR jel.reconciled IS NULL)
		ORDER BY je.entry_date, je.entry_number
	`, r.BankAccountID, tenantID, r.StatementDate)

	if err != nil {
		h.log.Error("Failed to get journal entries", "error", err)
	}

	type JournalEntryLine struct {
		ID           uuid.UUID `json:"id"`
		EntryDate    string    `json:"entry_date"`
		EntryNumber  string    `json:"entry_number"`
		Description  *string   `json:"description"`
		DebitAmount  float64   `json:"debit_amount"`
		CreditAmount float64   `json:"credit_amount"`
		Amount       float64   `json:"amount"` // Net amount (debit - credit)
		IsReconciled bool      `json:"is_reconciled"`
	}

	journalEntries := make([]JournalEntryLine, 0)
	if jeRows != nil {
		defer jeRows.Close()
		for jeRows.Next() {
			var je JournalEntryLine
			var entryDate time.Time
			jeRows.Scan(&je.ID, &entryDate, &je.EntryNumber, &je.Description,
				&je.DebitAmount, &je.CreditAmount, &je.IsReconciled)
			je.EntryDate = entryDate.Format("2006-01-02")
			je.Amount = je.DebitAmount - je.CreditAmount
			journalEntries = append(journalEntries, je)
		}
	}

	// Calculate totals
	var clearedBankTotal, clearedBookTotal float64
	for _, tx := range bankTransactions {
		if tx.IsReconciled || tx.Status == "reconciled" {
			if tx.TransactionType == "credit" {
				clearedBankTotal += tx.Amount
			} else {
				clearedBankTotal -= tx.Amount
			}
		}
	}

	response.Success(c, gin.H{
		"reconciliation":    r,
		"bank_transactions": bankTransactions,
		"journal_entries":   journalEntries,
		"summary": gin.H{
			"statement_balance": r.StatementEndingBalance,
			"book_balance":      r.BookBalance,
			"cleared_bank":      clearedBankTotal,
			"cleared_book":      clearedBookTotal,
			"difference":        r.StatementEndingBalance - r.BookBalance,
		},
	})
}

// UpdateBankReconciliation godoc
// @Summary Update bank reconciliation
// @Description Update a bank reconciliation session and mark items as cleared
// @Tags Finance - Bank Reconciliations
// @Accept json
// @Produce json
// @Param id path string true "Reconciliation ID"
// @Param notes body string false "Notes"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-reconciliations/{id} [put]
func (h *Handler) UpdateBankReconciliation(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	reconciliationID := c.Param("reconciliationId")
	if reconciliationID == "" {
		response.BadRequest(c, "Reconciliation ID is required")
		return
	}

	var input struct {
		ClearedBankTransactions []string `json:"cleared_bank_transactions"` // IDs of cleared bank transactions
		ClearedJournalEntries   []string `json:"cleared_journal_entries"`   // IDs of cleared journal entry lines
		Notes                   string   `json:"notes"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check reconciliation exists and is draft
	var status string
	err := h.db.QueryRow(`SELECT status FROM bank_reconciliations WHERE id = $1 AND tenant_id = $2`,
		reconciliationID, tenantID).Scan(&status)
	if err != nil {
		response.NotFound(c, "Reconciliation not found")
		return
	}
	if status != "draft" {
		response.BadRequest(c, "Cannot update a completed reconciliation")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	now := time.Now()

	// Reset all bank transactions for this reconciliation
	_, err = tx.Exec(`
		UPDATE bank_transactions
		SET is_reconciled = false, reconciliation_id = NULL, reconciled_date = NULL, updated_at = $1
		WHERE reconciliation_id = $2
	`, now, reconciliationID)
	if err != nil {
		h.log.Error("Failed to reset bank transactions", "error", err)
	}

	// Mark selected bank transactions as cleared
	for _, txID := range input.ClearedBankTransactions {
		_, err = tx.Exec(`
			UPDATE bank_transactions
			SET is_reconciled = true, reconciliation_id = $1, reconciled_date = $2, status = 'reconciled', updated_at = $2
			WHERE id = $3 AND tenant_id = $4
		`, reconciliationID, now, txID, tenantID)
		if err != nil {
			h.log.Error("Failed to update bank transaction", "error", err, "txID", txID)
		}
	}

	// Reset journal entry lines
	_, err = tx.Exec(`
		UPDATE journal_entry_lines jel
		SET reconciled = false, reconciled_at = NULL
		FROM bank_reconciliation_items bri
		WHERE bri.journal_entry_line_id = jel.id AND bri.reconciliation_id = $1
	`, reconciliationID)

	// Mark selected journal entry lines as reconciled
	for _, jelID := range input.ClearedJournalEntries {
		_, err = tx.Exec(`
			UPDATE journal_entry_lines
			SET reconciled = true, reconciled_at = $1
			WHERE id = $2
		`, now, jelID)
		if err != nil {
			h.log.Error("Failed to update journal entry line", "error", err, "jelID", jelID)
		}
	}

	// Calculate reconciled balance
	var clearedBankTotal float64
	err = tx.QueryRow(`
		SELECT COALESCE(SUM(CASE WHEN transaction_type = 'credit' THEN amount ELSE -amount END), 0)
		FROM bank_transactions
		WHERE reconciliation_id = $1 AND is_reconciled = true
	`, reconciliationID).Scan(&clearedBankTotal)

	var clearedBookTotal float64
	for _, jelID := range input.ClearedJournalEntries {
		var debit, credit float64
		tx.QueryRow(`SELECT COALESCE(debit_amount, 0), COALESCE(credit_amount, 0) FROM journal_entry_lines WHERE id = $1`, jelID).Scan(&debit, &credit)
		clearedBookTotal += debit - credit
	}

	// Update reconciliation with calculated values
	_, err = tx.Exec(`
		UPDATE bank_reconciliations
		SET reconciled_balance = $1, difference = statement_ending_balance - $1, notes = $2, updated_at = $3
		WHERE id = $4
	`, clearedBookTotal, input.Notes, now, reconciliationID)

	if err != nil {
		h.log.Error("Failed to update reconciliation", "error", err)
		response.InternalError(c, "Failed to update reconciliation")
		return
	}

	if err = tx.Commit(); err != nil {
		response.InternalError(c, "Failed to save changes")
		return
	}

	response.Success(c, gin.H{
		"message":           "Reconciliation updated",
		"cleared_bank":      len(input.ClearedBankTransactions),
		"cleared_journal":   len(input.ClearedJournalEntries),
		"reconciled_balance": clearedBookTotal,
	})
}

// CompleteBankReconciliation godoc
// @Summary Complete bank reconciliation
// @Description Finalize and complete a bank reconciliation session
// @Tags Finance - Bank Reconciliations
// @Accept json
// @Produce json
// @Param id path string true "Reconciliation ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-reconciliations/{id}/complete [post]
func (h *Handler) CompleteBankReconciliation(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	userID := c.GetString("user_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	reconciliationID := c.Param("reconciliationId")
	if reconciliationID == "" {
		response.BadRequest(c, "Reconciliation ID is required")
		return
	}

	// Get reconciliation
	var bankAccountID uuid.UUID
	var statementDate time.Time
	var statementBalance, bookBalance float64
	var difference *float64
	var status string

	err := h.db.QueryRow(`
		SELECT bank_account_id, statement_date, statement_ending_balance, book_balance, difference, status
		FROM bank_reconciliations WHERE id = $1 AND tenant_id = $2
	`, reconciliationID, tenantID).Scan(&bankAccountID, &statementDate, &statementBalance, &bookBalance, &difference, &status)

	if err != nil {
		response.NotFound(c, "Reconciliation not found")
		return
	}

	if status != "draft" {
		response.BadRequest(c, "Reconciliation is already completed")
		return
	}

	// Check if reconciliation is balanced (difference should be 0 or within write-off tolerance)
	const writeOffTolerance = 1000.0 // Maximum auto write-off amount (1,000 UZS)
	var reconDifference float64
	if difference != nil {
		reconDifference = *difference
	}
	if reconDifference > writeOffTolerance || reconDifference < -writeOffTolerance {
		response.BadRequest(c, fmt.Sprintf("Reconciliation has a difference of %.2f which exceeds the write-off tolerance (%.0f). Please resolve before completing.", reconDifference, writeOffTolerance))
		return
	}

	now := time.Now()
	tenantUUID, _ := uuid.Parse(tenantID)

	// Complete the reconciliation
	_, err = h.db.Exec(`
		UPDATE bank_reconciliations
		SET status = 'completed', completed_at = $1, completed_by = $2, updated_at = $1
		WHERE id = $3
	`, now, userID, reconciliationID)

	if err != nil {
		h.log.Error("Failed to complete reconciliation", "error", err)
		response.InternalError(c, "Failed to complete reconciliation")
		return
	}

	// Update bank account last reconciled
	h.db.Exec(`
		UPDATE bank_accounts
		SET last_reconciled = $1, last_reconciled_balance = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5
	`, statementDate, statementBalance, now, bankAccountID, tenantID)

	// Create clearing entries for outstanding accounts (2-step payment posting)
	h.createOutstandingClearingEntries(tenantID, userID, bankAccountID, reconciliationID, statementDate, now)

	// Auto write-off small reconciliation difference
	if reconDifference != 0 && (reconDifference <= writeOffTolerance && reconDifference >= -writeOffTolerance) {
		h.createReconciliationWriteOff(tenantID, tenantUUID, userID, bankAccountID, reconciliationID, reconDifference, statementDate, now)
	}

	response.Success(c, gin.H{
		"message":      "Reconciliation completed successfully",
		"completed_at": now,
		"write_off":    reconDifference,
	})
}

// createOutstandingClearingEntries creates GL entries to clear outstanding receipt/payment
// accounts to the actual bank account when a bank reconciliation is completed.
// This is the second step of the 2-step payment posting:
// Step 1 (on payment): DR Outstanding Receipts / CR AR (or DR AP / CR Outstanding Payments)
// Step 2 (on reconciliation): DR Bank / CR Outstanding Receipts (or DR Outstanding Payments / CR Bank)
//
// On completion, the current balance of the outstanding accounts is transferred to the bank.
func (h *Handler) createOutstandingClearingEntries(tenantID, userID string, bankAccountID uuid.UUID, reconciliationID string, statementDate time.Time, now time.Time) {
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		h.log.Error("Invalid tenant ID for clearing entries", "error", err)
		return
	}

	// Get the bank account's linked GL account and organization
	var bankGLAccountID uuid.UUID
	var orgID *uuid.UUID
	err = h.db.QueryRow(`
		SELECT account_id, organization_id FROM bank_accounts WHERE id = $1 AND tenant_id = $2
	`, bankAccountID, tenantID).Scan(&bankGLAccountID, &orgID)
	if err != nil || bankGLAccountID == uuid.Nil {
		h.log.Debug("Bank account has no linked GL account, skipping clearing entries")
		return
	}

	// Find outstanding accounts
	outReceiptsID := findAccount(h.db, tenantUUID, orgID, "outstanding receipts", "1150")
	outPaymentsID := findAccount(h.db, tenantUUID, orgID, "outstanding payments", "1160")

	if outReceiptsID == uuid.Nil && outPaymentsID == uuid.Nil {
		return // No outstanding accounts configured, nothing to clear
	}

	// Get the current balance of outstanding accounts
	// Outstanding Receipts: debit-normal, positive balance = payments awaiting clearing
	// Outstanding Payments: debit-normal, negative balance (credit) = payments awaiting clearing
	var receiptsBalance, paymentsBalance float64

	if outReceiptsID != uuid.Nil {
		h.db.QueryRow(`SELECT COALESCE(current_balance, 0) FROM accounts WHERE id = $1`, outReceiptsID).Scan(&receiptsBalance)
	}
	if outPaymentsID != uuid.Nil {
		h.db.QueryRow(`SELECT COALESCE(current_balance, 0) FROM accounts WHERE id = $1`, outPaymentsID).Scan(&paymentsBalance)
	}

	// Nothing to clear if both balances are zero
	if receiptsBalance < 0.01 && paymentsBalance > -0.01 {
		return
	}

	// Get a journal for the clearing entry
	var journalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString
	err = h.db.QueryRow(`
		SELECT id, COALESCE(next_number, 1), number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('CASH_RECEIPTS', 'CASH', 'GENERAL') AND deleted_at IS NULL LIMIT 1
	`, tenantID).Scan(&journalID, &nextNumber, &numberPrefix)
	if err != nil || journalID == uuid.Nil {
		h.log.Error("No journal found for clearing entries", "error", err)
		return
	}

	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}

	// Create clearing entry for Outstanding Receipts → Bank
	// DR Bank, CR Outstanding Receipts
	if receiptsBalance > 0.01 {
		entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)
		journalEntryID := uuid.New()
		description := fmt.Sprintf("Bank reconciliation clearing - receipts (%s)", statementDate.Format("2006-01-02"))

		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
			journalEntryID, tenantID, orgID, journalID, entryNumber, statementDate, reconciliationID, description,
			"bank_reconciliation", reconciliationID, 1.0, receiptsBalance, receiptsBalance, userID, now, now,
		)

		if err != nil {
			h.log.Error("Failed to create receipts clearing journal entry", "error", err)
		} else {
			// Line 1: Debit Bank
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), journalEntryID, 1, bankGLAccountID, "Bank - Cleared Receipts",
				receiptsBalance, 0.0, 1.0, now,
			)
			// Line 2: Credit Outstanding Receipts
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), journalEntryID, 2, outReceiptsID, "Outstanding Receipts - Cleared",
				0.0, receiptsBalance, 1.0, now,
			)

			// Update account balances
			h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", receiptsBalance, now, bankGLAccountID)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", receiptsBalance, now, outReceiptsID)

			nextNumber++
			h.db.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", journalID)
		}
	}

	// Create clearing entry for Outstanding Payments → Bank
	// DR Outstanding Payments, CR Bank
	// Outstanding Payments has a negative current_balance (credit balance) when payments are pending
	absPayments := -paymentsBalance // Make positive for the entry amounts
	if absPayments > 0.01 {
		entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)
		journalEntryID := uuid.New()
		description := fmt.Sprintf("Bank reconciliation clearing - payments (%s)", statementDate.Format("2006-01-02"))

		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
			journalEntryID, tenantID, orgID, journalID, entryNumber, statementDate, reconciliationID, description,
			"bank_reconciliation", reconciliationID, 1.0, absPayments, absPayments, userID, now, now,
		)

		if err != nil {
			h.log.Error("Failed to create payments clearing journal entry", "error", err)
		} else {
			// Line 1: Debit Outstanding Payments (clears the credit balance)
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), journalEntryID, 1, outPaymentsID, "Outstanding Payments - Cleared",
				absPayments, 0.0, 1.0, now,
			)
			// Line 2: Credit Bank
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), journalEntryID, 2, bankGLAccountID, "Bank - Cleared Payments",
				0.0, absPayments, 1.0, now,
			)

			// Update account balances
			h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", absPayments, now, outPaymentsID)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", absPayments, now, bankGLAccountID)

			h.db.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", journalID)
		}
	}
}

// createReconciliationWriteOff posts a GL entry to write off small reconciliation differences.
// Positive difference (statement > book): DR Bank, CR Other Income (bank has more)
// Negative difference (book > statement): DR Bank Charges, CR Bank (bank has less)
func (h *Handler) createReconciliationWriteOff(tenantID string, tenantUUID uuid.UUID, userID string, bankAccountID uuid.UUID, reconciliationID string, difference float64, statementDate time.Time, now time.Time) {
	// Get the bank account's linked GL account and organization
	var bankGLAccountID uuid.UUID
	var orgID *uuid.UUID
	err := h.db.QueryRow(`
		SELECT account_id, organization_id FROM bank_accounts WHERE id = $1 AND tenant_id = $2
	`, bankAccountID, tenantID).Scan(&bankGLAccountID, &orgID)
	if err != nil || bankGLAccountID == uuid.Nil {
		return
	}

	// Get a journal
	var journalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString
	err = h.db.QueryRow(`
		SELECT id, COALESCE(next_number, 1), number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('GENERAL', 'CASH_RECEIPTS', 'CASH') AND deleted_at IS NULL LIMIT 1
	`, tenantID).Scan(&journalID, &nextNumber, &numberPrefix)
	if err != nil || journalID == uuid.Nil {
		return
	}

	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}
	entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

	absDiff := difference
	if absDiff < 0 {
		absDiff = -absDiff
	}

	journalEntryID := uuid.New()
	description := fmt.Sprintf("Reconciliation write-off (%.2f) - %s", difference, statementDate.Format("2006-01-02"))

	_, err = h.db.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
		journalEntryID, tenantID, orgID, journalID, entryNumber, statementDate, reconciliationID, description,
		"bank_reconciliation_writeoff", reconciliationID, 1.0, absDiff, absDiff, userID, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create reconciliation write-off entry", "error", err)
		return
	}

	if difference > 0 {
		// Statement > book: bank has more than expected (e.g., interest earned)
		// DR Bank, CR Other Income
		otherIncomeID := findAccount(h.db, tenantUUID, orgID, "other income", "4900")
		if otherIncomeID == uuid.Nil {
			return
		}
		h.db.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1, $2, 1, $3, 'Bank - Reconciliation Adjustment', $4, 0, 1.0, $5)`,
			uuid.New(), journalEntryID, bankGLAccountID, absDiff, now)
		h.db.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1, $2, 2, $3, 'Reconciliation Write-off', 0, $4, 1.0, $5)`,
			uuid.New(), journalEntryID, otherIncomeID, absDiff, now)
		// Update balances
		h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", absDiff, now, bankGLAccountID)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", absDiff, now, otherIncomeID)
	} else {
		// Book > statement: bank has less than expected (e.g., bank charges)
		// DR Bank Charges, CR Bank
		bankChargesID := findAccount(h.db, tenantUUID, orgID, "bank charges", "7100")
		if bankChargesID == uuid.Nil {
			bankChargesID = findAccount(h.db, tenantUUID, orgID, "payment difference write-off", "6950")
		}
		if bankChargesID == uuid.Nil {
			return
		}
		h.db.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1, $2, 1, $3, 'Bank Charges - Reconciliation', $4, 0, 1.0, $5)`,
			uuid.New(), journalEntryID, bankChargesID, absDiff, now)
		h.db.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1, $2, 2, $3, 'Bank - Reconciliation Adjustment', 0, $4, 1.0, $5)`,
			uuid.New(), journalEntryID, bankGLAccountID, absDiff, now)
		// Update balances
		h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", absDiff, now, bankChargesID)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", absDiff, now, bankGLAccountID)
	}

	h.db.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", journalID)
}

// DeleteBankReconciliation godoc
// @Summary Delete bank reconciliation
// @Description Delete a draft bank reconciliation session
// @Tags Finance - Bank Reconciliations
// @Accept json
// @Produce json
// @Param id path string true "Reconciliation ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-reconciliations/{id} [delete]
func (h *Handler) DeleteBankReconciliation(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	reconciliationID := c.Param("reconciliationId")
	if reconciliationID == "" {
		response.BadRequest(c, "Reconciliation ID is required")
		return
	}

	// Check if draft
	var status string
	err := h.db.QueryRow(`SELECT status FROM bank_reconciliations WHERE id = $1 AND tenant_id = $2`,
		reconciliationID, tenantID).Scan(&status)
	if err != nil {
		response.NotFound(c, "Reconciliation not found")
		return
	}
	if status != "draft" {
		response.BadRequest(c, "Cannot delete a completed reconciliation")
		return
	}

	// Reset related bank transactions
	h.db.Exec(`
		UPDATE bank_transactions
		SET is_reconciled = false, reconciliation_id = NULL, reconciled_date = NULL, status = 'unmatched'
		WHERE reconciliation_id = $1
	`, reconciliationID)

	// Delete reconciliation items
	h.db.Exec(`DELETE FROM bank_reconciliation_items WHERE reconciliation_id = $1`, reconciliationID)

	// Delete reconciliation
	_, err = h.db.Exec(`DELETE FROM bank_reconciliations WHERE id = $1 AND tenant_id = $2`, reconciliationID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete reconciliation", "error", err)
		response.InternalError(c, "Failed to delete reconciliation")
		return
	}

	response.Success(c, gin.H{"message": "Reconciliation deleted"})
}

// ImportBankStatement godoc
// @Summary Import bank statement
// @Description Import bank transactions from a CSV file
// @Tags Finance - Bank Transactions
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "CSV file containing bank statement"
// @Param bank_account_id formData string true "Bank Account ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-statements/import [post]
func (h *Handler) ImportBankStatement(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	bankAccountID := c.Param("id")
	if bankAccountID == "" {
		response.BadRequest(c, "Bank account ID is required")
		return
	}

	var input struct {
		Transactions []struct {
			TransactionDate string  `json:"transaction_date" binding:"required"`
			Reference       string  `json:"reference"`
			Description     string  `json:"description"`
			Amount          float64 `json:"amount" binding:"required"`
			TransactionType string  `json:"transaction_type"` // credit or debit
		} `json:"transactions" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if len(input.Transactions) == 0 {
		response.BadRequest(c, "No transactions provided")
		return
	}

	importBatchID := uuid.New().String()
	now := time.Now()
	imported := 0
	duplicates := 0

	for _, t := range input.Transactions {
		// Determine transaction type if not provided
		txType := t.TransactionType
		if txType == "" {
			if t.Amount >= 0 {
				txType = "credit"
			} else {
				txType = "debit"
			}
		}

		amount := t.Amount
		if amount < 0 {
			amount = -amount
		}

		// Check for duplicate (same date, amount, reference)
		var exists bool
		h.db.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM bank_transactions
				WHERE bank_account_id = $1 AND transaction_date = $2 AND amount = $3
				  AND COALESCE(reference, '') = $4
			)
		`, bankAccountID, t.TransactionDate, amount, t.Reference).Scan(&exists)

		if exists {
			duplicates++
			continue
		}

		// Insert transaction
		_, err := h.db.Exec(`
			INSERT INTO bank_transactions (tenant_id, bank_account_id, transaction_date, reference, description,
			                               amount, transaction_type, status, import_batch_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'unmatched', $8, $9, $9)
		`, tenantID, bankAccountID, t.TransactionDate, t.Reference, t.Description, amount, txType, importBatchID, now)

		if err != nil {
			h.log.Error("Failed to import transaction", "error", err)
			continue
		}
		imported++
	}

	response.Success(c, gin.H{
		"message":         fmt.Sprintf("Imported %d transactions", imported),
		"imported":        imported,
		"duplicates":      duplicates,
		"import_batch_id": importBatchID,
	})
}

// =====================================================
// CASH TRANSACTIONS (Kassa)
// =====================================================

// ListCashTransactions godoc
// @Summary List cash transactions
// @Description Get a list of all cash transactions
// @Tags Finance - Cash Transactions
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/cash-transactions [get]
func (h *Handler) ListCashTransactions(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	var filter entity.CashTransactionListFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	query := `
		SELECT id, tenant_id, transaction_date, transaction_type, amount,
		       COALESCE(currency, 'UZS') as currency, COALESCE(description, '') as description,
		       COALESCE(category, '') as category, COALESCE(reference, '') as reference,
		       COALESCE(cashier, '') as cashier, COALESCE(status, 'posted') as status,
		       created_at, updated_at
		FROM cash_transactions
		WHERE tenant_id = $1
	`
	args := []interface{}{tenantID}
	argIndex := 2

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += fmt.Sprintf(" AND organization_id = $%d", argIndex)
		args = append(args, orgID)
		argIndex++
	}

	if filter.Search != "" {
		query += fmt.Sprintf(" AND (description ILIKE $%d OR reference ILIKE $%d OR cashier ILIKE $%d)", argIndex, argIndex, argIndex)
		args = append(args, "%"+filter.Search+"%")
		argIndex++
	}

	if filter.Type != "" {
		query += fmt.Sprintf(" AND transaction_type = $%d", argIndex)
		args = append(args, filter.Type)
		argIndex++
	}

	if filter.Category != "" {
		query += fmt.Sprintf(" AND category = $%d", argIndex)
		args = append(args, filter.Category)
		argIndex++
	}

	if filter.DateFrom != "" {
		query += fmt.Sprintf(" AND transaction_date >= $%d", argIndex)
		args = append(args, filter.DateFrom)
		argIndex++
	}

	if filter.DateTo != "" {
		query += fmt.Sprintf(" AND transaction_date <= $%d", argIndex)
		args = append(args, filter.DateTo)
		argIndex++
	}

	query += " ORDER BY transaction_date DESC, created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list cash transactions", "error", err)
		response.InternalError(c, "Failed to list cash transactions")
		return
	}
	defer rows.Close()

	var transactions []entity.CashTransaction
	for rows.Next() {
		var t entity.CashTransaction
		err := rows.Scan(
			&t.ID, &t.TenantID, &t.TransactionDate, &t.Type, &t.Amount,
			&t.Currency, &t.Description, &t.Category, &t.Reference,
			&t.Cashier, &t.Status, &t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan cash transaction", "error", err)
			continue
		}
		transactions = append(transactions, t)
	}

	response.Success(c, transactions)
}

// GetCashTransaction godoc
// @Summary Get cash transaction by ID
// @Description Get detailed information about a specific cash transaction
// @Tags Finance - Cash Transactions
// @Accept json
// @Produce json
// @Param id path string true "Cash Transaction ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/cash-transactions/{id} [get]
func (h *Handler) GetCashTransaction(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Cash transaction ID is required")
		return
	}

	var t entity.CashTransaction
	err := h.db.QueryRow(`
		SELECT id, tenant_id, transaction_date, transaction_type, amount,
		       COALESCE(currency, 'UZS') as currency, COALESCE(description, '') as description,
		       COALESCE(category, '') as category, COALESCE(reference, '') as reference,
		       COALESCE(cashier, '') as cashier, COALESCE(status, 'posted') as status,
		       created_at, updated_at
		FROM cash_transactions
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&t.ID, &t.TenantID, &t.TransactionDate, &t.Type, &t.Amount,
		&t.Currency, &t.Description, &t.Category, &t.Reference,
		&t.Cashier, &t.Status, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Cash transaction not found")
			return
		}
		h.log.Error("Failed to get cash transaction", "error", err)
		response.InternalError(c, "Failed to get cash transaction")
		return
	}

	response.Success(c, t)
}

// CreateCashTransaction godoc
// @Summary Create a new cash transaction
// @Description Create a new cash transaction record
// @Tags Finance - Cash Transactions
// @Accept json
// @Produce json
// @Param body body entity.CreateCashTransactionInput true "Cash transaction creation data"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/cash-transactions [post]
func (h *Handler) CreateCashTransaction(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	var input entity.CreateCashTransactionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse transaction date
	transactionDate, err := time.Parse("2006-01-02", input.TransactionDate)
	if err != nil {
		response.BadRequest(c, "Invalid transaction date format. Use YYYY-MM-DD")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Get organization ID from middleware header
	var orgIDPtr *uuid.UUID
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Generate transaction number
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM cash_transactions WHERE tenant_id = $1`, tenantID).Scan(&count)
	transactionNumber := fmt.Sprintf("CASH-%s-%04d", time.Now().Format("2006"), count+1)

	// Default currency if not provided
	currency := input.Currency
	if currency == "" {
		currency = "UZS"
	}

	_, err = h.db.Exec(`
		INSERT INTO cash_transactions (id, tenant_id, organization_id, transaction_number, transaction_date, transaction_type,
		                               amount, currency, description, category, reference, cashier, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'posted', $13, $13)
	`, id, tenantID, orgIDPtr, transactionNumber, transactionDate, input.Type, input.Amount, currency,
		input.Description, input.Category, input.Reference, input.Cashier, now)

	if err != nil {
		h.log.Error("Failed to create cash transaction", "error", err)
		response.InternalError(c, "Failed to create cash transaction")
		return
	}

	t := entity.CashTransaction{
		ID:              id,
		TenantID:        uuid.MustParse(tenantID),
		TransactionDate: transactionDate,
		Type:            input.Type,
		Amount:          input.Amount,
		Currency:        currency,
		Description:     input.Description,
		Category:        input.Category,
		Reference:       input.Reference,
		Cashier:         input.Cashier,
		Status:          "posted",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	response.Created(c, t)
}

// UpdateCashTransaction godoc
// @Summary Update a cash transaction
// @Description Update an existing cash transaction's information
// @Tags Finance - Cash Transactions
// @Accept json
// @Produce json
// @Param id path string true "Cash Transaction ID"
// @Param body body entity.UpdateCashTransactionInput true "Cash transaction update data"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/cash-transactions/{id} [put]
func (h *Handler) UpdateCashTransaction(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Cash transaction ID is required")
		return
	}

	var input entity.UpdateCashTransactionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if input.TransactionDate != nil {
		transactionDate, err := time.Parse("2006-01-02", *input.TransactionDate)
		if err != nil {
			response.BadRequest(c, "Invalid transaction date format")
			return
		}
		updates = append(updates, fmt.Sprintf("transaction_date = $%d", argIndex))
		args = append(args, transactionDate)
		argIndex++
	}
	if input.Type != nil {
		updates = append(updates, fmt.Sprintf("transaction_type = $%d", argIndex))
		args = append(args, *input.Type)
		argIndex++
	}
	if input.Amount != nil {
		updates = append(updates, fmt.Sprintf("amount = $%d", argIndex))
		args = append(args, *input.Amount)
		argIndex++
	}
	if input.Currency != nil {
		updates = append(updates, fmt.Sprintf("currency = $%d", argIndex))
		args = append(args, *input.Currency)
		argIndex++
	}
	if input.Description != nil {
		updates = append(updates, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, *input.Description)
		argIndex++
	}
	if input.Category != nil {
		updates = append(updates, fmt.Sprintf("category = $%d", argIndex))
		args = append(args, *input.Category)
		argIndex++
	}
	if input.Reference != nil {
		updates = append(updates, fmt.Sprintf("reference = $%d", argIndex))
		args = append(args, *input.Reference)
		argIndex++
	}
	if input.Cashier != nil {
		updates = append(updates, fmt.Sprintf("cashier = $%d", argIndex))
		args = append(args, *input.Cashier)
		argIndex++
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	now := time.Now()
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, now)
	argIndex++

	args = append(args, id, tenantID)
	query := fmt.Sprintf(`UPDATE cash_transactions SET %s WHERE id = $%d AND tenant_id = $%d`,
		strings.Join(updates, ", "), argIndex, argIndex+1)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update cash transaction", "error", err)
		response.InternalError(c, "Failed to update cash transaction")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Cash transaction not found")
		return
	}

	// Fetch updated transaction
	var t entity.CashTransaction
	h.db.QueryRow(`
		SELECT id, tenant_id, transaction_date, transaction_type, amount,
		       COALESCE(currency, 'UZS') as currency, COALESCE(description, '') as description,
		       COALESCE(category, '') as category, COALESCE(reference, '') as reference,
		       COALESCE(cashier, '') as cashier, COALESCE(status, 'posted') as status,
		       created_at, updated_at
		FROM cash_transactions
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&t.ID, &t.TenantID, &t.TransactionDate, &t.Type, &t.Amount,
		&t.Currency, &t.Description, &t.Category, &t.Reference,
		&t.Cashier, &t.Status, &t.CreatedAt, &t.UpdatedAt,
	)

	response.Success(c, t)
}

// DeleteCashTransaction godoc
// @Summary Delete a cash transaction
// @Description Soft-delete a cash transaction
// @Tags Finance - Cash Transactions
// @Accept json
// @Produce json
// @Param id path string true "Cash Transaction ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/cash-transactions/{id} [delete]
func (h *Handler) DeleteCashTransaction(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Cash transaction ID is required")
		return
	}

	result, err := h.db.Exec(`DELETE FROM cash_transactions WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete cash transaction", "error", err)
		response.InternalError(c, "Failed to delete cash transaction")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Cash transaction not found")
		return
	}

	response.Success(c, gin.H{"message": "Cash transaction deleted successfully"})
}

// =====================================================
// FISCAL YEARS
// =====================================================

// ListFiscalYears godoc
// @Summary List fiscal years
// @Description Get a list of all fiscal years for the tenant
// @Tags Finance - Fiscal Years
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-years [get]
func (h *Handler) ListFiscalYears(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, organization_id, code, name, start_date, end_date, status, created_at, updated_at
		FROM fiscal_years
		WHERE tenant_id = $1
	`

	args := []interface{}{tenantID}
	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND organization_id = $2"
		args = append(args, orgID)
	}

	query += " ORDER BY start_date DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list fiscal years", "error", err)
		response.InternalError(c, "Failed to list fiscal years")
		return
	}
	defer rows.Close()

	fiscalYears := make([]*entity.FiscalYear, 0)
	for rows.Next() {
		var fy entity.FiscalYear
		var orgID sql.NullString

		err := rows.Scan(
			&fy.ID, &fy.TenantID, &orgID, &fy.Code, &fy.Name, &fy.StartDate, &fy.EndDate,
			&fy.Status, &fy.CreatedAt, &fy.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if orgID.Valid {
			oid, _ := uuid.Parse(orgID.String)
			fy.OrganizationID = &oid
		}

		// Load periods for this fiscal year
		periodsQuery := `
			SELECT id, fiscal_year_id, code, name, period_number, start_date, end_date, status, created_at, updated_at
			FROM fiscal_periods
			WHERE fiscal_year_id = $1
			ORDER BY period_number ASC
		`
		periodRows, err := h.db.Query(periodsQuery, fy.ID)
		if err == nil {
			periods := make([]entity.FiscalPeriod, 0)
			for periodRows.Next() {
				var fp entity.FiscalPeriod
				err := periodRows.Scan(
					&fp.ID, &fp.FiscalYearID, &fp.Code, &fp.Name, &fp.PeriodNumber,
					&fp.StartDate, &fp.EndDate, &fp.Status, &fp.CreatedAt, &fp.UpdatedAt,
				)
				if err == nil {
					periods = append(periods, fp)
				}
			}
			periodRows.Close()
			fy.Periods = periods
		}

		fiscalYears = append(fiscalYears, &fy)
	}

	response.Success(c, fiscalYears)
}

// GetFiscalYear godoc
// @Summary Get fiscal year by ID
// @Description Get detailed information about a specific fiscal year
// @Tags Finance - Fiscal Years
// @Accept json
// @Produce json
// @Param id path string true "Fiscal Year ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-years/{id} [get]
func (h *Handler) GetFiscalYear(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Fiscal year ID is required")
		return
	}

	var fy entity.FiscalYear
	var orgID sql.NullString

	err := h.db.QueryRow(`
		SELECT id, tenant_id, organization_id, code, name, start_date, end_date, status, created_at, updated_at
		FROM fiscal_years
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&fy.ID, &fy.TenantID, &orgID, &fy.Code, &fy.Name, &fy.StartDate, &fy.EndDate,
		&fy.Status, &fy.CreatedAt, &fy.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Fiscal year not found")
		} else {
			h.log.Error("Failed to get fiscal year", "error", err)
			response.InternalError(c, "Failed to get fiscal year")
		}
		return
	}

	if orgID.Valid {
		oid, _ := uuid.Parse(orgID.String)
		fy.OrganizationID = &oid
	}

	// Load periods
	periodsQuery := `
		SELECT id, fiscal_year_id, code, name, period_number, start_date, end_date, status, created_at, updated_at
		FROM fiscal_periods
		WHERE fiscal_year_id = $1
		ORDER BY period_number ASC
	`
	periodRows, err := h.db.Query(periodsQuery, fy.ID)
	if err == nil {
		periods := make([]entity.FiscalPeriod, 0)
		for periodRows.Next() {
			var fp entity.FiscalPeriod
			err := periodRows.Scan(
				&fp.ID, &fp.FiscalYearID, &fp.Code, &fp.Name, &fp.PeriodNumber,
				&fp.StartDate, &fp.EndDate, &fp.Status, &fp.CreatedAt, &fp.UpdatedAt,
			)
			if err == nil {
				periods = append(periods, fp)
			}
		}
		periodRows.Close()
		fy.Periods = periods
	}

	response.Success(c, fy)
}

// CreateFiscalYearInput represents the input for creating a fiscal year
type CreateFiscalYearInput struct {
	Name           string    `json:"name" binding:"required"`
	Code           string    `json:"code"`
	StartDate      string    `json:"start_date" binding:"required"`
	EndDate        string    `json:"end_date" binding:"required"`
	OrganizationID *string   `json:"organization_id,omitempty"`
	AutoGenerate   bool      `json:"auto_generate"`
	PeriodType     string    `json:"period_type"` // monthly, quarterly
}

// CreateFiscalYear godoc
// @Summary Create a new fiscal year
// @Description Create a new fiscal year for the organization
// @Tags Finance - Fiscal Years
// @Accept json
// @Produce json
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-years [post]
func (h *Handler) CreateFiscalYear(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input CreateFiscalYearInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse dates
	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format")
		return
	}

	endDate, err := time.Parse("2006-01-02", input.EndDate)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format")
		return
	}

	// Validate date range
	if endDate.Before(startDate) {
		response.BadRequest(c, "End date must be after start date")
		return
	}

	id := uuid.New()
	now := time.Now()

	var orgID *uuid.UUID
	if input.OrganizationID != nil && *input.OrganizationID != "" {
		oid, _ := uuid.Parse(*input.OrganizationID)
		orgID = &oid
	}
	// Fallback to middleware header if not provided in body
	if orgID == nil {
		if headerOrgID, orgOk := middleware.GetOrganizationID(c); orgOk && headerOrgID != uuid.Nil {
			orgID = &headerOrgID
		}
	}

	// Use code from input or generate from name
	code := input.Code
	if code == "" {
		code = input.Name
	}

	_, err = h.db.Exec(`
		INSERT INTO fiscal_years (id, tenant_id, organization_id, code, name, start_date, end_date, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, id, tenantID, orgID, code, input.Name, startDate, endDate, "open", now, now)

	if err != nil {
		h.log.Error("Failed to create fiscal year", "error", err)
		response.InternalError(c, "Failed to create fiscal year")
		return
	}

	fy := &entity.FiscalYear{
		ID:             id,
		TenantID:       tenantID,
		OrganizationID: orgID,
		Name:           input.Name,
		Code:           code,
		StartDate:      startDate,
		EndDate:        endDate,
		Status:         "open",
		CreatedAt:      now,
		UpdatedAt:      now,
		Periods:        []entity.FiscalPeriod{},
	}

	response.Success(c, fy)
}

// UpdateFiscalYear godoc
// @Summary Update a fiscal year
// @Description Update an existing fiscal year's information
// @Tags Finance - Fiscal Years
// @Accept json
// @Produce json
// @Param id path string true "Fiscal Year ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-years/{id} [put]
func (h *Handler) UpdateFiscalYear(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Fiscal year ID is required")
		return
	}

	var input CreateFiscalYearInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Parse dates
	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format")
		return
	}

	endDate, err := time.Parse("2006-01-02", input.EndDate)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format")
		return
	}

	now := time.Now()

	var orgID *uuid.UUID
	if input.OrganizationID != nil && *input.OrganizationID != "" {
		oid, _ := uuid.Parse(*input.OrganizationID)
		orgID = &oid
	}

	// Use code from input or generate from name
	code := input.Code
	if code == "" {
		code = input.Name
	}

	result, err := h.db.Exec(`
		UPDATE fiscal_years
		SET code = $1, name = $2, start_date = $3, end_date = $4, organization_id = $5, updated_at = $6
		WHERE id = $7 AND tenant_id = $8
	`, code, input.Name, startDate, endDate, orgID, now, id, tenantID)

	if err != nil {
		h.log.Error("Failed to update fiscal year", "error", err)
		response.InternalError(c, "Failed to update fiscal year")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Fiscal year not found")
		return
	}

	// Fetch updated fiscal year
	var fy entity.FiscalYear
	var orgIDStr sql.NullString

	err = h.db.QueryRow(`
		SELECT id, tenant_id, organization_id, code, name, start_date, end_date, status, created_at, updated_at
		FROM fiscal_years
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&fy.ID, &fy.TenantID, &orgIDStr, &fy.Code, &fy.Name, &fy.StartDate, &fy.EndDate,
		&fy.Status, &fy.CreatedAt, &fy.UpdatedAt,
	)

	if orgIDStr.Valid {
		oid, _ := uuid.Parse(orgIDStr.String)
		fy.OrganizationID = &oid
	}

	response.Success(c, fy)
}

// CloseFiscalYear godoc
// @Summary Close a fiscal year
// @Description Close a fiscal year and prevent further modifications
// @Tags Finance - Fiscal Years
// @Accept json
// @Produce json
// @Param id path string true "Fiscal Year ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-years/{id}/close [post]
func (h *Handler) CloseFiscalYear(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Fiscal year ID is required")
		return
	}

	now := time.Now()

	// Close the fiscal year
	_, err := h.db.Exec(`
		UPDATE fiscal_years
		SET status = 'closed', updated_at = $1
		WHERE id = $2 AND tenant_id = $3
	`, now, id, tenantID)

	if err != nil {
		h.log.Error("Failed to close fiscal year", "error", err)
		response.InternalError(c, "Failed to close fiscal year")
		return
	}

	// Close all periods of this fiscal year
	_, err = h.db.Exec(`
		UPDATE fiscal_periods
		SET status = 'closed', updated_at = $1
		WHERE fiscal_year_id = $2
	`, now, id)

	if err != nil {
		h.log.Error("Failed to close fiscal periods", "error", err)
	}

	response.Success(c, gin.H{"message": "Fiscal year closed successfully"})
}

// DeleteFiscalYear godoc
// @Summary Delete a fiscal year
// @Description Soft-delete a fiscal year
// @Tags Finance - Fiscal Years
// @Accept json
// @Produce json
// @Param id path string true "Fiscal Year ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-years/{id} [delete]
func (h *Handler) DeleteFiscalYear(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Fiscal year ID is required")
		return
	}

	// Delete periods first
	_, err := h.db.Exec(`DELETE FROM fiscal_periods WHERE fiscal_year_id = $1`, id)
	if err != nil {
		h.log.Error("Failed to delete fiscal periods", "error", err)
		response.InternalError(c, "Failed to delete fiscal year")
		return
	}

	// Delete fiscal year
	result, err := h.db.Exec(`DELETE FROM fiscal_years WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete fiscal year", "error", err)
		response.InternalError(c, "Failed to delete fiscal year")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Fiscal year not found")
		return
	}

	response.Success(c, gin.H{"message": "Fiscal year deleted successfully"})
}

// =====================================================
// FISCAL PERIODS
// =====================================================

// ListFiscalPeriods godoc
// @Summary List fiscal periods
// @Description Get a list of all fiscal periods for a fiscal year
// @Tags Finance - Fiscal Periods
// @Accept json
// @Produce json
// @Param fiscal_year_id query string false "Filter by fiscal year ID"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-periods [get]
func (h *Handler) ListFiscalPeriods(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	fiscalYearID := c.Query("fiscal_year_id")

	query := `
		SELECT fp.id, fp.fiscal_year_id, fp.code, fp.name, fp.period_number, fp.start_date, fp.end_date, fp.status, fp.created_at, fp.updated_at
		FROM fiscal_periods fp
		JOIN fiscal_years fy ON fp.fiscal_year_id = fy.id
		WHERE fy.tenant_id = $1
	`

	args := []interface{}{tenantID}

	if fiscalYearID != "" {
		query += " AND fp.fiscal_year_id = $2"
		args = append(args, fiscalYearID)
	}

	query += " ORDER BY fp.period_number ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list fiscal periods", "error", err)
		response.InternalError(c, "Failed to list fiscal periods")
		return
	}
	defer rows.Close()

	periods := make([]*entity.FiscalPeriod, 0)
	for rows.Next() {
		var fp entity.FiscalPeriod
		err := rows.Scan(
			&fp.ID, &fp.FiscalYearID, &fp.Code, &fp.Name, &fp.PeriodNumber,
			&fp.StartDate, &fp.EndDate, &fp.Status, &fp.CreatedAt, &fp.UpdatedAt,
		)
		if err != nil {
			continue
		}

		periods = append(periods, &fp)
	}

	response.Success(c, periods)
}

// CreateFiscalPeriodInput represents the input for creating a fiscal period
type CreateFiscalPeriodInput struct {
	FiscalYearID string `json:"fiscal_year_id" binding:"required"`
	Code         string `json:"code" binding:"required"`
	Name         string `json:"name" binding:"required"`
	PeriodNumber int    `json:"period_number" binding:"required"`
	StartDate    string `json:"start_date" binding:"required"`
	EndDate      string `json:"end_date" binding:"required"`
}

// CreateFiscalPeriod godoc
// @Summary Create a new fiscal period
// @Description Create a new fiscal period within a fiscal year
// @Tags Finance - Fiscal Periods
// @Accept json
// @Produce json
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-periods [post]
func (h *Handler) CreateFiscalPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input CreateFiscalPeriodInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Verify fiscal year exists and belongs to tenant
	var fyTenantID uuid.UUID
	err := h.db.QueryRow("SELECT tenant_id FROM fiscal_years WHERE id = $1", input.FiscalYearID).Scan(&fyTenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Fiscal year not found")
		} else {
			response.InternalError(c, "Failed to verify fiscal year")
		}
		return
	}

	if fyTenantID != tenantID {
		response.Unauthorized(c, "Fiscal year does not belong to your tenant")
		return
	}

	// Parse dates
	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format")
		return
	}

	endDate, err := time.Parse("2006-01-02", input.EndDate)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format")
		return
	}

	id := uuid.New()
	now := time.Now()

	_, err = h.db.Exec(`
		INSERT INTO fiscal_periods (id, fiscal_year_id, code, name, period_number, start_date, end_date, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, id, input.FiscalYearID, input.Code, input.Name, input.PeriodNumber, startDate, endDate, "open", now, now)

	if err != nil {
		h.log.Error("Failed to create fiscal period", "error", err)
		response.InternalError(c, "Failed to create fiscal period")
		return
	}

	fyID, _ := uuid.Parse(input.FiscalYearID)

	fp := &entity.FiscalPeriod{
		ID:           id,
		FiscalYearID: fyID,
		Code:         input.Code,
		Name:         input.Name,
		PeriodNumber: input.PeriodNumber,
		StartDate:    startDate,
		EndDate:      endDate,
		Status:       "open",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	response.Success(c, fp)
}

// BatchCreateFiscalPeriodsInput represents the input for batch creating fiscal periods
type BatchCreateFiscalPeriodsInput struct {
	Periods []CreateFiscalPeriodInput `json:"periods" binding:"required"`
}

// BatchCreateFiscalPeriods godoc
// @Summary Batch create fiscal periods
// @Description Create multiple fiscal periods at once for a fiscal year
// @Tags Finance - Fiscal Periods
// @Accept json
// @Produce json
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-periods/batch [post]
func (h *Handler) BatchCreateFiscalPeriods(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input BatchCreateFiscalPeriodsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	createdPeriods := make([]*entity.FiscalPeriod, 0)

	for _, periodInput := range input.Periods {
		// Verify fiscal year exists
		var fyTenantID uuid.UUID
		err := h.db.QueryRow("SELECT tenant_id FROM fiscal_years WHERE id = $1", periodInput.FiscalYearID).Scan(&fyTenantID)
		if err != nil {
			continue
		}

		if fyTenantID != tenantID {
			continue
		}

		// Parse dates
		startDate, err := time.Parse("2006-01-02", periodInput.StartDate)
		if err != nil {
			continue
		}

		endDate, err := time.Parse("2006-01-02", periodInput.EndDate)
		if err != nil {
			continue
		}

		id := uuid.New()
		now := time.Now()

		_, err = h.db.Exec(`
			INSERT INTO fiscal_periods (id, fiscal_year_id, code, name, period_number, start_date, end_date, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, id, periodInput.FiscalYearID, periodInput.Code, periodInput.Name, periodInput.PeriodNumber, startDate, endDate, "open", now, now)

		if err != nil {
			h.log.Error("Failed to create fiscal period", "error", err)
			continue
		}

		fyID, _ := uuid.Parse(periodInput.FiscalYearID)

		fp := &entity.FiscalPeriod{
			ID:           id,
			FiscalYearID: fyID,
			Code:         periodInput.Code,
			Name:         periodInput.Name,
			PeriodNumber: periodInput.PeriodNumber,
			StartDate:    startDate,
			EndDate:      endDate,
			Status:       "open",
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		createdPeriods = append(createdPeriods, fp)
	}

	response.Success(c, createdPeriods)
}

// CloseFiscalPeriod godoc
// @Summary Close a fiscal period
// @Description Close a fiscal period and prevent further modifications
// @Tags Finance - Fiscal Periods
// @Accept json
// @Produce json
// @Param id path string true "Fiscal Period ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-periods/{id}/close [post]
func (h *Handler) CloseFiscalPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Fiscal period ID is required")
		return
	}

	now := time.Now()

	// Verify the period belongs to the tenant
	var fyTenantID uuid.UUID
	err := h.db.QueryRow(`
		SELECT fy.tenant_id
		FROM fiscal_periods fp
		JOIN fiscal_years fy ON fp.fiscal_year_id = fy.id
		WHERE fp.id = $1
	`, id).Scan(&fyTenantID)

	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Fiscal period not found")
		} else {
			response.InternalError(c, "Failed to verify fiscal period")
		}
		return
	}

	if fyTenantID != tenantID {
		response.Unauthorized(c, "Fiscal period does not belong to your tenant")
		return
	}

	_, err = h.db.Exec(`
		UPDATE fiscal_periods
		SET status = 'closed', updated_at = $1
		WHERE id = $2
	`, now, id)

	if err != nil {
		h.log.Error("Failed to close fiscal period", "error", err)
		response.InternalError(c, "Failed to close fiscal period")
		return
	}

	response.Success(c, gin.H{"message": "Fiscal period closed successfully"})
}

// ReopenFiscalPeriod godoc
// @Summary Reopen a fiscal period
// @Description Reopen a closed fiscal period to allow modifications
// @Tags Finance - Fiscal Periods
// @Accept json
// @Produce json
// @Param id path string true "Fiscal Period ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/fiscal-periods/{id}/reopen [post]
func (h *Handler) ReopenFiscalPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Fiscal period ID is required")
		return
	}

	now := time.Now()

	// Verify the period belongs to the tenant
	var fyTenantID uuid.UUID
	err := h.db.QueryRow(`
		SELECT fy.tenant_id
		FROM fiscal_periods fp
		JOIN fiscal_years fy ON fp.fiscal_year_id = fy.id
		WHERE fp.id = $1
	`, id).Scan(&fyTenantID)

	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Fiscal period not found")
		} else {
			response.InternalError(c, "Failed to verify fiscal period")
		}
		return
	}

	if fyTenantID != tenantID {
		response.Unauthorized(c, "Fiscal period does not belong to your tenant")
		return
	}

	_, err = h.db.Exec(`
		UPDATE fiscal_periods
		SET status = 'open', updated_at = $1
		WHERE id = $2
	`, now, id)

	if err != nil {
		h.log.Error("Failed to reopen fiscal period", "error", err)
		response.InternalError(c, "Failed to reopen fiscal period")
		return
	}

	response.Success(c, gin.H{"message": "Fiscal period reopened successfully"})
}

// LockFiscalPeriod locks a fiscal period (prevents journal entries)
// @Summary Lock a fiscal period
// @Tags Finance - Fiscal Periods
// @Param id path string true "Fiscal Period ID"
// @Router /finance/fiscal-periods/{id}/lock [post]
func (h *Handler) LockFiscalPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Fiscal period ID is required")
		return
	}

	var fyTenantID uuid.UUID
	err := h.db.QueryRow(`
		SELECT fy.tenant_id FROM fiscal_periods fp
		JOIN fiscal_years fy ON fp.fiscal_year_id = fy.id WHERE fp.id = $1
	`, id).Scan(&fyTenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Fiscal period not found")
		} else {
			response.InternalError(c, "Failed to verify fiscal period")
		}
		return
	}
	if fyTenantID != tenantID {
		response.Unauthorized(c, "Fiscal period does not belong to your tenant")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE fiscal_periods SET status = 'locked', locked_by = $1, locked_at = $2, updated_at = $2 WHERE id = $3
	`, userID, now, id)
	if err != nil {
		h.log.Error("Failed to lock fiscal period", "error", err)
		response.InternalError(c, "Failed to lock fiscal period")
		return
	}

	response.Success(c, gin.H{"message": "Fiscal period locked successfully"})
}

// UnlockFiscalPeriod unlocks a locked fiscal period
// @Summary Unlock a fiscal period
// @Tags Finance - Fiscal Periods
// @Param id path string true "Fiscal Period ID"
// @Router /finance/fiscal-periods/{id}/unlock [post]
func (h *Handler) UnlockFiscalPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Fiscal period ID is required")
		return
	}

	var fyTenantID uuid.UUID
	err := h.db.QueryRow(`
		SELECT fy.tenant_id FROM fiscal_periods fp
		JOIN fiscal_years fy ON fp.fiscal_year_id = fy.id WHERE fp.id = $1
	`, id).Scan(&fyTenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Fiscal period not found")
		} else {
			response.InternalError(c, "Failed to verify fiscal period")
		}
		return
	}
	if fyTenantID != tenantID {
		response.Unauthorized(c, "Fiscal period does not belong to your tenant")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE fiscal_periods SET status = 'open', locked_by = NULL, locked_at = NULL, updated_at = $1 WHERE id = $2
	`, now, id)
	if err != nil {
		h.log.Error("Failed to unlock fiscal period", "error", err)
		response.InternalError(c, "Failed to unlock fiscal period")
		return
	}

	response.Success(c, gin.H{"message": "Fiscal period unlocked successfully"})
}

// ==================== BUDGETS ====================

type CreateBudgetInput struct {
	OrganizationID   *string  `json:"organization_id"`
	FiscalYearID     string   `json:"fiscal_year_id" binding:"required"`
	Code             string   `json:"code" binding:"required"`
	Name             string   `json:"name" binding:"required"`
	Description      *string  `json:"description"`
	BudgetType       string   `json:"budget_type"` // expense, revenue, combined
	TotalAmount      float64  `json:"total_amount"`
	Status           string   `json:"status"`
	StartDate        *string  `json:"start_date"`
	EndDate          *string  `json:"end_date"`
	WarningThreshold *float64 `json:"warning_threshold"`
	Lines            []CreateBudgetLineInput `json:"lines"`
}

type CreateBudgetLineInput struct {
	BudgetID       string   `json:"budget_id" binding:"required"`
	AccountID      string   `json:"account_id" binding:"required"`
	FiscalPeriodID *string  `json:"fiscal_period_id"`
	DepartmentID   *string  `json:"department_id"`
	BudgetedAmount float64  `json:"budgeted_amount"`
	PlannedAmount  float64  `json:"planned_amount"` // alias from frontend
	ActualAmount   float64  `json:"actual_amount"`
	Notes          *string  `json:"notes"`
}

// ListBudgets godoc
// @Summary List budgets
// @Description Get a list of all budgets for the tenant
// @Tags Finance - Budgets
// @Accept json
// @Produce json
// @Param fiscal_year_id query string false "Filter by fiscal year ID"
// @Param is_active query bool false "Filter by active status"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budgets [get]
func (h *Handler) ListBudgets(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	fiscalYearID := c.Query("fiscal_year_id")

	query := `
		SELECT b.id, b.tenant_id, b.organization_id, b.fiscal_year_id, b.code, b.name, b.description,
		       b.budget_type, b.total_amount, b.status, b.approved_by, b.approved_at,
		       b.created_by, b.created_at, b.updated_at,
		       COALESCE(b.start_date, fy.start_date), COALESCE(b.end_date, fy.end_date),
		       COALESCE(b.warning_threshold, 80)
		FROM budgets b
		LEFT JOIN fiscal_years fy ON fy.id = b.fiscal_year_id
		WHERE b.tenant_id = $1 AND b.deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND b.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if fiscalYearID != "" {
		argCount++
		query += fmt.Sprintf(" AND b.fiscal_year_id = $%d", argCount)
		args = append(args, fiscalYearID)
	}

	query += " ORDER BY b.created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list budgets", "error", err)
		response.InternalError(c, "Failed to list budgets")
		return
	}
	defer rows.Close()

	budgets := make([]*entity.Budget, 0)
	for rows.Next() {
		var b entity.Budget
		var orgID, desc, approvedBy, createdBy sql.NullString
		var approvedAt sql.NullTime
		var startDate, endDate sql.NullString
		var warningThreshold float64

		err := rows.Scan(
			&b.ID, &b.TenantID, &orgID, &b.FiscalYearID, &b.Code, &b.Name, &desc,
			&b.BudgetType, &b.TotalAmount, &b.Status, &approvedBy, &approvedAt,
			&createdBy, &b.CreatedAt, &b.UpdatedAt,
			&startDate, &endDate, &warningThreshold,
		)
		if err != nil {
			continue
		}

		if orgID.Valid {
			oid, _ := uuid.Parse(orgID.String)
			b.OrganizationID = &oid
		}
		if desc.Valid {
			b.Description = &desc.String
		}
		if approvedBy.Valid {
			aid, _ := uuid.Parse(approvedBy.String)
			b.ApprovedBy = &aid
		}
		if approvedAt.Valid {
			b.ApprovedAt = &approvedAt.Time
		}
		if createdBy.Valid {
			cid, _ := uuid.Parse(createdBy.String)
			b.CreatedBy = &cid
		}
		if startDate.Valid {
			b.StartDate = &startDate.String
		}
		if endDate.Valid {
			b.EndDate = &endDate.String
		}
		b.WarningThreshold = warningThreshold

		budgets = append(budgets, &b)
	}

	response.Success(c, budgets)
}

// GetBudget godoc
// @Summary Get budget by ID
// @Description Get detailed information about a specific budget including its lines
// @Tags Finance - Budgets
// @Accept json
// @Produce json
// @Param id path string true "Budget ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budgets/{id} [get]
func (h *Handler) GetBudget(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Budget ID is required")
		return
	}

	var b entity.Budget
	var orgID, desc, approvedBy, createdBy sql.NullString
	var approvedAt sql.NullTime
	var startDate, endDate sql.NullString

	err := h.db.QueryRow(`
		SELECT b.id, b.tenant_id, b.organization_id, b.fiscal_year_id, b.code, b.name, b.description,
		       b.budget_type, b.total_amount, b.status, b.approved_by, b.approved_at,
		       b.created_by, b.created_at, b.updated_at,
		       COALESCE(b.start_date, fy.start_date), COALESCE(b.end_date, fy.end_date),
		       COALESCE(b.warning_threshold, 80)
		FROM budgets b
		LEFT JOIN fiscal_years fy ON b.fiscal_year_id = fy.id
		WHERE b.id = $1 AND b.tenant_id = $2 AND b.deleted_at IS NULL
	`, id, tenantID).Scan(
		&b.ID, &b.TenantID, &orgID, &b.FiscalYearID, &b.Code, &b.Name, &desc,
		&b.BudgetType, &b.TotalAmount, &b.Status, &approvedBy, &approvedAt,
		&createdBy, &b.CreatedAt, &b.UpdatedAt,
		&startDate, &endDate, &b.WarningThreshold,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Budget not found")
		} else {
			h.log.Error("Failed to get budget", "error", err)
			response.InternalError(c, "Failed to get budget")
		}
		return
	}

	if orgID.Valid {
		oid, _ := uuid.Parse(orgID.String)
		b.OrganizationID = &oid
	}
	if desc.Valid {
		b.Description = &desc.String
	}
	if approvedBy.Valid {
		aid, _ := uuid.Parse(approvedBy.String)
		b.ApprovedBy = &aid
	}
	if approvedAt.Valid {
		b.ApprovedAt = &approvedAt.Time
	}
	if createdBy.Valid {
		cid, _ := uuid.Parse(createdBy.String)
		b.CreatedBy = &cid
	}
	if startDate.Valid {
		b.StartDate = &startDate.String
	}
	if endDate.Valid {
		b.EndDate = &endDate.String
	}

	// Load budget lines with computed actual amounts from journal entries
	lineRows, err := h.db.Query(`
		SELECT bl.id, bl.budget_id, bl.account_id, COALESCE(a.name, '') as account_name, COALESCE(a.code, '') as account_code,
		       bl.fiscal_period_id, bl.department_id,
		       bl.budgeted_amount,
		       COALESCE((
		           SELECT CASE
		               WHEN $2 = 'revenue' THEN SUM(jel.credit_amount) - SUM(jel.debit_amount)
		               ELSE SUM(jel.debit_amount) - SUM(jel.credit_amount)
		           END
		           FROM journal_entry_lines jel
		           JOIN journal_entries je ON jel.journal_entry_id = je.id
		           WHERE jel.account_id = bl.account_id
		             AND je.tenant_id = $3
		             AND je.status = 'posted'
		             AND je.deleted_at IS NULL
		             AND je.entry_date >= COALESCE($4::date, '1900-01-01')
		             AND je.entry_date <= COALESCE($5::date, '2999-12-31')
		       ), 0) as computed_actual,
		       bl.notes, bl.created_at, bl.updated_at
		FROM budget_lines bl
		LEFT JOIN accounts a ON bl.account_id = a.id
		WHERE bl.budget_id = $1
		ORDER BY bl.created_at
	`, b.ID, b.BudgetType, tenantID, b.StartDate, b.EndDate)

	if err == nil {
		defer lineRows.Close()
		lines := make([]entity.BudgetLine, 0)

		for lineRows.Next() {
			var line entity.BudgetLine
			var fiscalPeriodID, deptID, notes sql.NullString
			var computedActual float64

			err := lineRows.Scan(
				&line.ID, &line.BudgetID, &line.AccountID, &line.AccountName, &line.AccountCode,
				&fiscalPeriodID, &deptID,
				&line.BudgetedAmount, &computedActual,
				&notes, &line.CreatedAt, &line.UpdatedAt,
			)
			if err == nil {
				line.ActualAmount = computedActual
				line.Variance = line.BudgetedAmount - line.ActualAmount
				if fiscalPeriodID.Valid {
					fpid, _ := uuid.Parse(fiscalPeriodID.String)
					line.FiscalPeriodID = &fpid
				}
				if deptID.Valid {
					did, _ := uuid.Parse(deptID.String)
					line.DepartmentID = &did
				}
				if notes.Valid {
					line.Notes = &notes.String
				}
				lines = append(lines, line)
			}
		}
		b.Lines = lines
	}

	response.Success(c, b)
}

// CreateBudget godoc
// @Summary Create a new budget
// @Description Create a new budget for a fiscal year
// @Tags Finance - Budgets
// @Accept json
// @Produce json
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budgets [post]
func (h *Handler) CreateBudget(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input CreateBudgetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	id := uuid.New()
	now := time.Now()

	var orgID *uuid.UUID
	if input.OrganizationID != nil && *input.OrganizationID != "" {
		oid, _ := uuid.Parse(*input.OrganizationID)
		orgID = &oid
	}
	// Fallback to middleware header if not provided in body
	if orgID == nil {
		if headerOrgID, orgOk := middleware.GetOrganizationID(c); orgOk && headerOrgID != uuid.Nil {
			orgID = &headerOrgID
		}
	}

	fiscalYearID, err := uuid.Parse(input.FiscalYearID)
	if err != nil {
		response.BadRequest(c, "Invalid fiscal year ID")
		return
	}

	if input.TotalAmount <= 0 {
		response.BadRequest(c, "Budget total amount must be greater than zero")
		return
	}

	budgetType := input.BudgetType
	if budgetType == "" {
		budgetType = "expense"
	}

	status := input.Status
	if status == "" {
		status = "draft"
	}

	// Parse start/end dates — fall back to fiscal year dates
	var startDate, endDate *string
	if input.StartDate != nil && *input.StartDate != "" {
		startDate = input.StartDate
	}
	if input.EndDate != nil && *input.EndDate != "" {
		endDate = input.EndDate
	}
	// If no dates provided, fill from fiscal year
	if startDate == nil || endDate == nil {
		var fyStart, fyEnd sql.NullString
		h.db.QueryRow("SELECT start_date, end_date FROM fiscal_years WHERE id = $1", fiscalYearID).Scan(&fyStart, &fyEnd)
		if startDate == nil && fyStart.Valid {
			startDate = &fyStart.String
		}
		if endDate == nil && fyEnd.Valid {
			endDate = &fyEnd.String
		}
	}

	warningThreshold := 80.0
	if input.WarningThreshold != nil {
		warningThreshold = *input.WarningThreshold
	}

	_, err = h.db.Exec(`
		INSERT INTO budgets (id, tenant_id, organization_id, fiscal_year_id, code, name, description,
		                    budget_type, total_amount, status, start_date, end_date, warning_threshold,
		                    created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, id, tenantID, orgID, fiscalYearID, input.Code, input.Name, input.Description,
		budgetType, input.TotalAmount, status, startDate, endDate, warningThreshold, userID, now, now)

	if err != nil {
		h.log.Error("Failed to create budget", "error", err)
		response.InternalError(c, "Failed to create budget")
		return
	}

	// Create budget lines if provided
	if len(input.Lines) > 0 {
		for _, lineInput := range input.Lines {
			accountID, _ := uuid.Parse(lineInput.AccountID)
			lineID := uuid.New()

			var fiscalPeriodID, deptID *uuid.UUID
			if lineInput.FiscalPeriodID != nil && *lineInput.FiscalPeriodID != "" {
				fpid, _ := uuid.Parse(*lineInput.FiscalPeriodID)
				fiscalPeriodID = &fpid
			}
			if lineInput.DepartmentID != nil && *lineInput.DepartmentID != "" {
				did, _ := uuid.Parse(*lineInput.DepartmentID)
				deptID = &did
			}

			_, err = h.db.Exec(`
				INSERT INTO budget_lines (id, budget_id, account_id, fiscal_period_id, department_id,
				                         budgeted_amount, actual_amount, notes, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			`, lineID, id, accountID, fiscalPeriodID, deptID,
				lineInput.BudgetedAmount, lineInput.ActualAmount, lineInput.Notes, now, now)

			if err != nil {
				h.log.Error("Failed to create budget line", "error", err)
			}
		}
	}

	// Fetch created budget with start_date, end_date, warning_threshold
	var b entity.Budget
	var orgIDStr, desc sql.NullString
	var fetchStartDate, fetchEndDate sql.NullString
	var fetchWarningThreshold float64

	err = h.db.QueryRow(`
		SELECT b.id, b.tenant_id, b.organization_id, b.fiscal_year_id, b.code, b.name, b.description,
		       b.budget_type, b.total_amount, b.status, b.created_by, b.created_at, b.updated_at,
		       COALESCE(b.start_date, fy.start_date), COALESCE(b.end_date, fy.end_date),
		       COALESCE(b.warning_threshold, 80)
		FROM budgets b
		LEFT JOIN fiscal_years fy ON fy.id = b.fiscal_year_id
		WHERE b.id = $1
	`, id).Scan(
		&b.ID, &b.TenantID, &orgIDStr, &b.FiscalYearID, &b.Code, &b.Name, &desc,
		&b.BudgetType, &b.TotalAmount, &b.Status, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt,
		&fetchStartDate, &fetchEndDate, &fetchWarningThreshold,
	)

	if orgIDStr.Valid {
		oid, _ := uuid.Parse(orgIDStr.String)
		b.OrganizationID = &oid
	}
	if desc.Valid {
		b.Description = &desc.String
	}
	if fetchStartDate.Valid {
		b.StartDate = &fetchStartDate.String
	}
	if fetchEndDate.Valid {
		b.EndDate = &fetchEndDate.String
	}
	b.WarningThreshold = fetchWarningThreshold

	response.Success(c, b)
}

// UpdateBudget godoc
// @Summary Update a budget
// @Description Update an existing budget's information
// @Tags Finance - Budgets
// @Accept json
// @Produce json
// @Param id path string true "Budget ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budgets/{id} [put]
func (h *Handler) UpdateBudget(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Budget ID is required")
		return
	}

	var input CreateBudgetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	now := time.Now()

	// Determine status - keep existing if not provided
	status := input.Status
	if status == "" {
		status = "draft"
	}

	// Convert empty string dates to nil so COALESCE keeps existing values
	var startDate, endDate *string
	if input.StartDate != nil && *input.StartDate != "" {
		startDate = input.StartDate
	}
	if input.EndDate != nil && *input.EndDate != "" {
		endDate = input.EndDate
	}

	result, err := h.db.Exec(`
		UPDATE budgets
		SET code = $1, name = $2, description = $3, budget_type = $4, total_amount = $5,
		    start_date = COALESCE($6, start_date), end_date = COALESCE($7, end_date),
		    warning_threshold = COALESCE($8, warning_threshold),
		    status = $9, fiscal_year_id = $10,
		    updated_at = $11
		WHERE id = $12 AND tenant_id = $13 AND deleted_at IS NULL
	`, input.Code, input.Name, input.Description, input.BudgetType, input.TotalAmount,
		startDate, endDate, input.WarningThreshold,
		status, input.FiscalYearID, now, id, tenantID)

	if err != nil {
		h.log.Error("Failed to update budget", "error", err)
		response.InternalError(c, "Failed to update budget")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Budget not found")
		return
	}

	// Fetch updated budget with start_date, end_date, warning_threshold
	var b entity.Budget
	var orgIDStr, desc sql.NullString
	var fetchedStart, fetchedEnd sql.NullString
	var fetchedThreshold float64

	err = h.db.QueryRow(`
		SELECT b.id, b.tenant_id, b.organization_id, b.fiscal_year_id, b.code, b.name, b.description,
		       b.budget_type, b.total_amount, b.status, b.created_at, b.updated_at,
		       COALESCE(b.start_date, fy.start_date), COALESCE(b.end_date, fy.end_date),
		       COALESCE(b.warning_threshold, 80)
		FROM budgets b
		LEFT JOIN fiscal_years fy ON fy.id = b.fiscal_year_id
		WHERE b.id = $1 AND b.tenant_id = $2
	`, id, tenantID).Scan(
		&b.ID, &b.TenantID, &orgIDStr, &b.FiscalYearID, &b.Code, &b.Name, &desc,
		&b.BudgetType, &b.TotalAmount, &b.Status, &b.CreatedAt, &b.UpdatedAt,
		&fetchedStart, &fetchedEnd, &fetchedThreshold,
	)

	if err != nil {
		h.log.Error("Failed to fetch updated budget", "error", err)
		response.InternalError(c, "Failed to fetch updated budget")
		return
	}

	if orgIDStr.Valid {
		oid, _ := uuid.Parse(orgIDStr.String)
		b.OrganizationID = &oid
	}
	if desc.Valid {
		b.Description = &desc.String
	}
	if fetchedStart.Valid {
		b.StartDate = &fetchedStart.String
	}
	if fetchedEnd.Valid {
		b.EndDate = &fetchedEnd.String
	}
	b.WarningThreshold = fetchedThreshold

	response.Success(c, b)
}

// DeleteBudget godoc
// @Summary Delete a budget
// @Description Soft-delete a budget
// @Tags Finance - Budgets
// @Accept json
// @Produce json
// @Param id path string true "Budget ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budgets/{id} [delete]
func (h *Handler) DeleteBudget(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Budget ID is required")
		return
	}

	result, err := h.db.Exec(`
		UPDATE budgets
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID)

	if err != nil {
		h.log.Error("Failed to delete budget", "error", err)
		response.InternalError(c, "Failed to delete budget")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Budget not found")
		return
	}

	response.Success(c, gin.H{"message": "Budget deleted successfully"})
}

// ActivateBudget godoc
// @Summary Activate a budget
// @Description Activate a budget to make it the active budget for the fiscal year
// @Tags Finance - Budgets
// @Accept json
// @Produce json
// @Param id path string true "Budget ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budgets/{id}/activate [post]
func (h *Handler) ActivateBudget(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Budget ID is required")
		return
	}

	now := time.Now()

	result, err := h.db.Exec(`
		UPDATE budgets
		SET status = 'active', approved_by = $1, approved_at = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5 AND deleted_at IS NULL
	`, userID, now, now, id, tenantID)

	if err != nil {
		h.log.Error("Failed to activate budget", "error", err)
		response.InternalError(c, "Failed to activate budget")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Budget not found")
		return
	}

	response.Success(c, gin.H{"message": "Budget activated successfully"})
}

// ==================== BUDGET LINES ====================

// ListBudgetLines godoc
// @Summary List budget lines
// @Description Get a list of budget lines for a specific budget
// @Tags Finance - Budget Lines
// @Accept json
// @Produce json
// @Param budget_id query string true "Budget ID"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budget-lines [get]
func (h *Handler) ListBudgetLines(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	budgetID := c.Query("budget_id")

	// Query budget lines with account info and computed actual amounts from journal entries
	// actual_amount = SUM of debit for expense accounts, SUM of credit for revenue accounts
	// within the budget's date range
	query := `
		SELECT bl.id, bl.budget_id, bl.account_id, COALESCE(a.name, '') as account_name, COALESCE(a.code, '') as account_code,
		       bl.fiscal_period_id, bl.department_id,
		       bl.budgeted_amount,
		       COALESCE((
		           SELECT CASE
		               WHEN b.budget_type = 'revenue' THEN SUM(jel.credit_amount) - SUM(jel.debit_amount)
		               ELSE SUM(jel.debit_amount) - SUM(jel.credit_amount)
		           END
		           FROM journal_entry_lines jel
		           JOIN journal_entries je ON jel.journal_entry_id = je.id
		           WHERE jel.account_id = bl.account_id
		             AND je.tenant_id = b.tenant_id
		             AND je.status = 'posted'
		             AND je.deleted_at IS NULL
		             AND je.entry_date >= COALESCE(b.start_date, fy.start_date)
		             AND je.entry_date <= COALESCE(b.end_date, fy.end_date)
		       ), 0) as computed_actual,
		       bl.notes, bl.created_at, bl.updated_at
		FROM budget_lines bl
		JOIN budgets b ON bl.budget_id = b.id
		LEFT JOIN accounts a ON bl.account_id = a.id
		LEFT JOIN fiscal_years fy ON b.fiscal_year_id = fy.id
		WHERE b.tenant_id = $1
	`

	args := []interface{}{tenantID}
	if budgetID != "" {
		query += " AND bl.budget_id = $2"
		args = append(args, budgetID)
	}

	query += " ORDER BY bl.created_at"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list budget lines", "error", err)
		response.InternalError(c, "Failed to list budget lines")
		return
	}
	defer rows.Close()

	lines := make([]*entity.BudgetLine, 0)
	for rows.Next() {
		var line entity.BudgetLine
		var fiscalPeriodID, deptID, notes sql.NullString
		var computedActual float64

		err := rows.Scan(
			&line.ID, &line.BudgetID, &line.AccountID, &line.AccountName, &line.AccountCode,
			&fiscalPeriodID, &deptID,
			&line.BudgetedAmount, &computedActual,
			&notes, &line.CreatedAt, &line.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan budget line", "error", err)
			continue
		}

		// Use computed actual from journal entries
		line.ActualAmount = computedActual
		line.Variance = line.BudgetedAmount - line.ActualAmount

		if fiscalPeriodID.Valid {
			fpid, _ := uuid.Parse(fiscalPeriodID.String)
			line.FiscalPeriodID = &fpid
		}
		if deptID.Valid {
			did, _ := uuid.Parse(deptID.String)
			line.DepartmentID = &did
		}
		if notes.Valid {
			line.Notes = &notes.String
		}

		lines = append(lines, &line)
	}

	response.Success(c, lines)
}

// CreateBudgetLine godoc
// @Summary Create a new budget line
// @Description Create a new budget line for a budget
// @Tags Finance - Budget Lines
// @Accept json
// @Produce json
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budget-lines [post]
func (h *Handler) CreateBudgetLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input CreateBudgetLineInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	budgetID, err := uuid.Parse(input.BudgetID)
	if err != nil {
		response.BadRequest(c, "Invalid budget ID")
		return
	}

	accountID, err := uuid.Parse(input.AccountID)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	id := uuid.New()
	now := time.Now()

	var fiscalPeriodID, deptID *uuid.UUID
	if input.FiscalPeriodID != nil && *input.FiscalPeriodID != "" {
		fpid, _ := uuid.Parse(*input.FiscalPeriodID)
		fiscalPeriodID = &fpid
	}
	if input.DepartmentID != nil && *input.DepartmentID != "" {
		did, _ := uuid.Parse(*input.DepartmentID)
		deptID = &did
	}

	// Verify budget exists and belongs to tenant
	var budgetTenantID uuid.UUID
	err = h.db.QueryRow("SELECT tenant_id FROM budgets WHERE id = $1", budgetID).Scan(&budgetTenantID)
	if err != nil || budgetTenantID != tenantID {
		response.BadRequest(c, "Invalid budget")
		return
	}

	// Accept either budgeted_amount or planned_amount from frontend
	budgetedAmount := input.BudgetedAmount
	if budgetedAmount == 0 && input.PlannedAmount > 0 {
		budgetedAmount = input.PlannedAmount
	}

	_, err = h.db.Exec(`
		INSERT INTO budget_lines (id, budget_id, account_id, fiscal_period_id, department_id,
		                         budgeted_amount, actual_amount, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, id, budgetID, accountID, fiscalPeriodID, deptID,
		budgetedAmount, input.ActualAmount, input.Notes, now, now)

	if err != nil {
		h.log.Error("Failed to create budget line", "error", err)
		response.InternalError(c, "Failed to create budget line")
		return
	}

	// Fetch created line
	var line entity.BudgetLine
	var fpIDStr, deptIDStr, notesStr sql.NullString

	err = h.db.QueryRow(`
		SELECT id, budget_id, account_id, fiscal_period_id, department_id,
		       budgeted_amount, actual_amount, variance, notes, created_at, updated_at
		FROM budget_lines
		WHERE id = $1
	`, id).Scan(
		&line.ID, &line.BudgetID, &line.AccountID, &fpIDStr, &deptIDStr,
		&line.BudgetedAmount, &line.ActualAmount, &line.Variance, &notesStr,
		&line.CreatedAt, &line.UpdatedAt,
	)

	if fpIDStr.Valid {
		fpid, _ := uuid.Parse(fpIDStr.String)
		line.FiscalPeriodID = &fpid
	}
	if deptIDStr.Valid {
		did, _ := uuid.Parse(deptIDStr.String)
		line.DepartmentID = &did
	}
	if notesStr.Valid {
		line.Notes = &notesStr.String
	}

	response.Success(c, line)
}

// UpdateBudgetLine godoc
// @Summary Update a budget line
// @Description Update an existing budget line's information
// @Tags Finance - Budget Lines
// @Accept json
// @Produce json
// @Param id path string true "Budget Line ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budget-lines/{id} [put]
func (h *Handler) UpdateBudgetLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Budget line ID is required")
		return
	}

	var input CreateBudgetLineInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	now := time.Now()

	// Verify budget line belongs to tenant
	var budgetTenantID uuid.UUID
	err := h.db.QueryRow(`
		SELECT b.tenant_id FROM budget_lines bl
		JOIN budgets b ON bl.budget_id = b.id
		WHERE bl.id = $1
	`, id).Scan(&budgetTenantID)

	if err != nil || budgetTenantID != tenantID {
		response.NotFound(c, "Budget line not found")
		return
	}

	result, err := h.db.Exec(`
		UPDATE budget_lines
		SET budgeted_amount = $1, actual_amount = $2, notes = $3, updated_at = $4
		WHERE id = $5
	`, input.BudgetedAmount, input.ActualAmount, input.Notes, now, id)

	if err != nil {
		h.log.Error("Failed to update budget line", "error", err)
		response.InternalError(c, "Failed to update budget line")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Budget line not found")
		return
	}

	// Fetch updated line
	var line entity.BudgetLine
	var fpIDStr, deptIDStr, notesStr sql.NullString

	err = h.db.QueryRow(`
		SELECT id, budget_id, account_id, fiscal_period_id, department_id,
		       budgeted_amount, actual_amount, variance, notes, created_at, updated_at
		FROM budget_lines
		WHERE id = $1
	`, id).Scan(
		&line.ID, &line.BudgetID, &line.AccountID, &fpIDStr, &deptIDStr,
		&line.BudgetedAmount, &line.ActualAmount, &line.Variance, &notesStr,
		&line.CreatedAt, &line.UpdatedAt,
	)

	if fpIDStr.Valid {
		fpid, _ := uuid.Parse(fpIDStr.String)
		line.FiscalPeriodID = &fpid
	}
	if deptIDStr.Valid {
		did, _ := uuid.Parse(deptIDStr.String)
		line.DepartmentID = &did
	}
	if notesStr.Valid {
		line.Notes = &notesStr.String
	}

	response.Success(c, line)
}

// DeleteBudgetLine godoc
// @Summary Delete a budget line
// @Description Soft-delete a budget line
// @Tags Finance - Budget Lines
// @Accept json
// @Produce json
// @Param id path string true "Budget Line ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/budget-lines/{id} [delete]
func (h *Handler) DeleteBudgetLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id := c.Param("id")
	if id == "" {
		response.BadRequest(c, "Budget line ID is required")
		return
	}

	// Verify budget line belongs to tenant
	var budgetTenantID uuid.UUID
	err := h.db.QueryRow(`
		SELECT b.tenant_id FROM budget_lines bl
		JOIN budgets b ON bl.budget_id = b.id
		WHERE bl.id = $1
	`, id).Scan(&budgetTenantID)

	if err != nil || budgetTenantID != tenantID {
		response.NotFound(c, "Budget line not found")
		return
	}

	result, err := h.db.Exec("DELETE FROM budget_lines WHERE id = $1", id)

	if err != nil {
		h.log.Error("Failed to delete budget line", "error", err)
		response.InternalError(c, "Failed to delete budget line")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Budget line not found")
		return
	}

	response.Success(c, gin.H{"message": "Budget line deleted successfully"})
}

// =====================================================
// RECURRING JOURNAL ENTRIES
// =====================================================

// ListRecurringJournalTemplates godoc
// @Summary List recurring journal templates
// @Description Get a list of all recurring journal entry templates
// @Tags Finance - Recurring Journals
// @Accept json
// @Produce json
// @Param is_active query bool false "Filter by active status"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/recurring-journal-templates [get]
func (h *Handler) ListRecurringJournalTemplates(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT rjt.id, rjt.name, rjt.description, rjt.frequency, rjt.interval_count,
		       rjt.start_date, rjt.end_date, rjt.next_run_date, rjt.last_run_date,
		       rjt.total_debit, rjt.total_credit, rjt.is_active, rjt.auto_post,
		       rjt.created_at, j.name as journal_name,
		       (SELECT COUNT(*) FROM recurring_journal_log WHERE template_id = rjt.id) as generated_count
		FROM recurring_journal_templates rjt
		JOIN journals j ON rjt.journal_id = j.id
		WHERE rjt.tenant_id = $1 AND rjt.deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND rjt.organization_id = $2"
		args = append(args, orgID)
	}

	query += " ORDER BY rjt.name"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list recurring journal templates", "error", err)
		response.InternalError(c, "Failed to list templates")
		return
	}
	defer rows.Close()

	type Template struct {
		ID             uuid.UUID `json:"id"`
		Name           string    `json:"name"`
		Description    *string   `json:"description"`
		Frequency      string    `json:"frequency"`
		IntervalCount  int       `json:"interval_count"`
		StartDate      string    `json:"start_date"`
		EndDate        *string   `json:"end_date"`
		NextRunDate    string    `json:"next_run_date"`
		LastRunDate    *string   `json:"last_run_date"`
		TotalDebit     float64   `json:"total_debit"`
		TotalCredit    float64   `json:"total_credit"`
		IsActive       bool      `json:"is_active"`
		AutoPost       bool      `json:"auto_post"`
		CreatedAt      time.Time `json:"created_at"`
		JournalName    string    `json:"journal_name"`
		GeneratedCount int       `json:"generated_count"`
	}

	templates := make([]Template, 0)
	for rows.Next() {
		var t Template
		var startDate, nextRunDate time.Time
		var endDate, lastRunDate *time.Time

		err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Frequency, &t.IntervalCount,
			&startDate, &endDate, &nextRunDate, &lastRunDate,
			&t.TotalDebit, &t.TotalCredit, &t.IsActive, &t.AutoPost,
			&t.CreatedAt, &t.JournalName, &t.GeneratedCount)
		if err != nil {
			continue
		}

		t.StartDate = startDate.Format("2006-01-02")
		t.NextRunDate = nextRunDate.Format("2006-01-02")
		if endDate != nil {
			s := endDate.Format("2006-01-02")
			t.EndDate = &s
		}
		if lastRunDate != nil {
			s := lastRunDate.Format("2006-01-02")
			t.LastRunDate = &s
		}

		templates = append(templates, t)
	}

	response.Success(c, templates)
}

// GetRecurringJournalTemplate godoc
// @Summary Get recurring journal template by ID
// @Description Get detailed information about a specific recurring journal template with its lines
// @Tags Finance - Recurring Journals
// @Accept json
// @Produce json
// @Param id path string true "Template ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/recurring-journal-templates/{id} [get]
func (h *Handler) GetRecurringJournalTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	templateID := c.Param("id")
	if templateID == "" {
		response.BadRequest(c, "Template ID is required")
		return
	}

	// Get template
	var t struct {
		ID            uuid.UUID `json:"id"`
		Name          string    `json:"name"`
		Description   *string   `json:"description"`
		Reference     *string   `json:"reference"`
		JournalID     uuid.UUID `json:"journal_id"`
		JournalName   string    `json:"journal_name"`
		Frequency     string    `json:"frequency"`
		IntervalCount int       `json:"interval_count"`
		DayOfMonth    *int      `json:"day_of_month"`
		DayOfWeek     *int      `json:"day_of_week"`
		MonthOfYear   *int      `json:"month_of_year"`
		StartDate     string    `json:"start_date"`
		EndDate       *string   `json:"end_date"`
		NextRunDate   string    `json:"next_run_date"`
		LastRunDate   *string   `json:"last_run_date"`
		TotalDebit    float64   `json:"total_debit"`
		TotalCredit   float64   `json:"total_credit"`
		IsActive      bool      `json:"is_active"`
		AutoPost      bool      `json:"auto_post"`
		CreatedAt     time.Time `json:"created_at"`
	}

	var startDate, nextRunDate time.Time
	var endDate, lastRunDate *time.Time

	err := h.db.QueryRow(`
		SELECT rjt.id, rjt.name, rjt.description, rjt.reference, rjt.journal_id, j.name,
		       rjt.frequency, rjt.interval_count, rjt.day_of_month, rjt.day_of_week, rjt.month_of_year,
		       rjt.start_date, rjt.end_date, rjt.next_run_date, rjt.last_run_date,
		       rjt.total_debit, rjt.total_credit, rjt.is_active, rjt.auto_post, rjt.created_at
		FROM recurring_journal_templates rjt
		JOIN journals j ON rjt.journal_id = j.id
		WHERE rjt.id = $1 AND rjt.tenant_id = $2 AND rjt.deleted_at IS NULL
	`, templateID, tenantID).Scan(&t.ID, &t.Name, &t.Description, &t.Reference, &t.JournalID, &t.JournalName,
		&t.Frequency, &t.IntervalCount, &t.DayOfMonth, &t.DayOfWeek, &t.MonthOfYear,
		&startDate, &endDate, &nextRunDate, &lastRunDate,
		&t.TotalDebit, &t.TotalCredit, &t.IsActive, &t.AutoPost, &t.CreatedAt)

	if err != nil {
		response.NotFound(c, "Template not found")
		return
	}

	t.StartDate = startDate.Format("2006-01-02")
	t.NextRunDate = nextRunDate.Format("2006-01-02")
	if endDate != nil {
		s := endDate.Format("2006-01-02")
		t.EndDate = &s
	}
	if lastRunDate != nil {
		s := lastRunDate.Format("2006-01-02")
		t.LastRunDate = &s
	}

	// Get lines
	lineRows, err := h.db.Query(`
		SELECT rjtl.id, rjtl.line_number, rjtl.account_id, a.code, a.name,
		       rjtl.description, rjtl.debit_amount, rjtl.credit_amount, rjtl.contact_id
		FROM recurring_journal_template_lines rjtl
		JOIN accounts a ON rjtl.account_id = a.id
		WHERE rjtl.template_id = $1
		ORDER BY rjtl.line_number
	`, templateID)

	type Line struct {
		ID           uuid.UUID  `json:"id"`
		LineNumber   int        `json:"line_number"`
		AccountID    uuid.UUID  `json:"account_id"`
		AccountCode  string     `json:"account_code"`
		AccountName  string     `json:"account_name"`
		Description  *string    `json:"description"`
		DebitAmount  float64    `json:"debit_amount"`
		CreditAmount float64    `json:"credit_amount"`
		ContactID    *uuid.UUID `json:"contact_id"`
	}

	lines := make([]Line, 0)
	if err == nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var l Line
			lineRows.Scan(&l.ID, &l.LineNumber, &l.AccountID, &l.AccountCode, &l.AccountName,
				&l.Description, &l.DebitAmount, &l.CreditAmount, &l.ContactID)
			lines = append(lines, l)
		}
	}

	// Get generation log
	logRows, err := h.db.Query(`
		SELECT rjl.id, rjl.journal_entry_id, je.entry_number, rjl.generated_for_date, rjl.generated_at
		FROM recurring_journal_log rjl
		JOIN journal_entries je ON rjl.journal_entry_id = je.id
		WHERE rjl.template_id = $1
		ORDER BY rjl.generated_for_date DESC
		LIMIT 20
	`, templateID)

	type LogEntry struct {
		ID               uuid.UUID `json:"id"`
		JournalEntryID   uuid.UUID `json:"journal_entry_id"`
		EntryNumber      string    `json:"entry_number"`
		GeneratedForDate string    `json:"generated_for_date"`
		GeneratedAt      time.Time `json:"generated_at"`
	}

	logs := make([]LogEntry, 0)
	if err == nil {
		defer logRows.Close()
		for logRows.Next() {
			var l LogEntry
			var genForDate time.Time
			logRows.Scan(&l.ID, &l.JournalEntryID, &l.EntryNumber, &genForDate, &l.GeneratedAt)
			l.GeneratedForDate = genForDate.Format("2006-01-02")
			logs = append(logs, l)
		}
	}

	response.Success(c, gin.H{
		"template": t,
		"lines":    lines,
		"log":      logs,
	})
}

// CreateRecurringJournalTemplate godoc
// @Summary Create a recurring journal template
// @Description Create a new recurring journal entry template
// @Tags Finance - Recurring Journals
// @Accept json
// @Produce json
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/recurring-journal-templates [post]
func (h *Handler) CreateRecurringJournalTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID := c.GetString("user_id")
	orgID := c.GetString("organization_id")

	var input struct {
		Name          string `json:"name" binding:"required"`
		Description   string `json:"description"`
		Reference     string `json:"reference"`
		JournalID     string `json:"journal_id" binding:"required"`
		Frequency     string `json:"frequency" binding:"required"`
		IntervalCount int    `json:"interval_count"`
		DayOfMonth    *int   `json:"day_of_month"`
		DayOfWeek     *int   `json:"day_of_week"`
		MonthOfYear   *int   `json:"month_of_year"`
		StartDate     string `json:"start_date" binding:"required"`
		EndDate       string `json:"end_date"`
		AutoPost      bool   `json:"auto_post"`
		Lines         []struct {
			AccountID    string  `json:"account_id" binding:"required"`
			Description  string  `json:"description"`
			DebitAmount  float64 `json:"debit_amount"`
			CreditAmount float64 `json:"credit_amount"`
			ContactID    string  `json:"contact_id"`
		} `json:"lines" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	validFrequencies := map[string]bool{"daily": true, "weekly": true, "monthly": true, "quarterly": true, "yearly": true}
	if !validFrequencies[input.Frequency] {
		response.BadRequest(c, "Invalid frequency. Must be: daily, weekly, monthly, quarterly, yearly")
		return
	}

	if len(input.Lines) == 0 {
		response.BadRequest(c, "At least one line is required")
		return
	}

	var totalDebit, totalCredit float64
	for _, line := range input.Lines {
		totalDebit += line.DebitAmount
		totalCredit += line.CreditAmount
	}
	if math.Abs(totalDebit-totalCredit) > 0.01 {
		response.BadRequest(c, "Debits and credits must be equal")
		return
	}

	if input.IntervalCount <= 0 {
		input.IntervalCount = 1
	}

	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start date format")
		return
	}
	nextRunDate := startDate

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	var templateID uuid.UUID
	err = tx.QueryRow(`
		INSERT INTO recurring_journal_templates (
			tenant_id, organization_id, journal_id, name, description, reference,
			frequency, interval_count, day_of_month, day_of_week, month_of_year,
			start_date, end_date, next_run_date, total_debit, total_credit,
			is_active, auto_post, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING id
	`, tenantID, nullIfEmpty(orgID), input.JournalID, input.Name, nullIfEmpty(input.Description), nullIfEmpty(input.Reference),
		input.Frequency, input.IntervalCount, input.DayOfMonth, input.DayOfWeek, input.MonthOfYear,
		input.StartDate, nullIfEmpty(input.EndDate), nextRunDate, totalDebit, totalCredit,
		true, input.AutoPost, nullIfEmpty(userID)).Scan(&templateID)

	if err != nil {
		h.log.Error("Failed to create recurring template", "error", err)
		response.InternalError(c, "Failed to create template")
		return
	}

	for i, line := range input.Lines {
		_, err = tx.Exec(`
			INSERT INTO recurring_journal_template_lines (
				template_id, line_number, account_id, description, debit_amount, credit_amount, contact_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, templateID, i+1, line.AccountID, nullIfEmpty(line.Description), line.DebitAmount, line.CreditAmount, nullIfEmpty(line.ContactID))

		if err != nil {
			h.log.Error("Failed to create template line", "error", err)
			response.InternalError(c, "Failed to create template line")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		response.InternalError(c, "Failed to save template")
		return
	}

	response.Success(c, gin.H{
		"id":      templateID,
		"message": "Recurring template created successfully",
	})
}

// UpdateRecurringJournalTemplate godoc
// @Summary Update a recurring journal template
// @Description Update an existing recurring journal entry template
// @Tags Finance - Recurring Journals
// @Accept json
// @Produce json
// @Param id path string true "Template ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/recurring-journal-templates/{id} [put]
func (h *Handler) UpdateRecurringJournalTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	templateID := c.Param("id")
	if templateID == "" {
		response.BadRequest(c, "Template ID is required")
		return
	}

	var input struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		Frequency     string `json:"frequency"`
		IntervalCount int    `json:"interval_count"`
		StartDate     string `json:"start_date"`
		EndDate       string `json:"end_date"`
		NextRunDate   string `json:"next_run_date"`
		DayOfMonth    *int   `json:"day_of_month"`
		DayOfWeek     *int   `json:"day_of_week"`
		MonthOfYear   *int   `json:"month_of_year"`
		IsActive      *bool  `json:"is_active"`
		AutoPost      *bool  `json:"auto_post"`
		Lines         []struct {
			AccountID    string  `json:"account_id" binding:"required"`
			Description  string  `json:"description"`
			DebitAmount  float64 `json:"debit_amount"`
			CreditAmount float64 `json:"credit_amount"`
			ContactID    string  `json:"contact_id"`
		} `json:"lines"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var exists bool
	h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM recurring_journal_templates WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
		templateID, tenantID).Scan(&exists)
	if !exists {
		response.NotFound(c, "Template not found")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	updates := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argCount := 1

	if input.Name != "" {
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, input.Name)
		argCount++
	}
	if input.Description != "" {
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, input.Description)
		argCount++
	}
	if input.Frequency != "" {
		updates = append(updates, fmt.Sprintf("frequency = $%d", argCount))
		args = append(args, input.Frequency)
		argCount++
	}
	if input.IntervalCount > 0 {
		updates = append(updates, fmt.Sprintf("interval_count = $%d", argCount))
		args = append(args, input.IntervalCount)
		argCount++
	}
	if input.IsActive != nil {
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
		argCount++
	}
	if input.AutoPost != nil {
		updates = append(updates, fmt.Sprintf("auto_post = $%d", argCount))
		args = append(args, *input.AutoPost)
		argCount++
	}
	if input.StartDate != "" {
		startDate, err := time.Parse("2006-01-02", input.StartDate)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
			return
		}
		updates = append(updates, fmt.Sprintf("start_date = $%d", argCount))
		args = append(args, startDate)
		argCount++
		// If next_run_date is not explicitly provided, update it to the new start_date
		if input.NextRunDate == "" {
			updates = append(updates, fmt.Sprintf("next_run_date = $%d", argCount))
			args = append(args, startDate)
			argCount++
		}
	}
	if input.NextRunDate != "" {
		nextRunDate, err := time.Parse("2006-01-02", input.NextRunDate)
		if err != nil {
			response.BadRequest(c, "Invalid next_run_date format, use YYYY-MM-DD")
			return
		}
		updates = append(updates, fmt.Sprintf("next_run_date = $%d", argCount))
		args = append(args, nextRunDate)
		argCount++
	}
	if input.EndDate != "" {
		endDate, err := time.Parse("2006-01-02", input.EndDate)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
			return
		}
		updates = append(updates, fmt.Sprintf("end_date = $%d", argCount))
		args = append(args, endDate)
		argCount++
	}
	if input.DayOfMonth != nil {
		updates = append(updates, fmt.Sprintf("day_of_month = $%d", argCount))
		args = append(args, *input.DayOfMonth)
		argCount++
	}
	if input.DayOfWeek != nil {
		updates = append(updates, fmt.Sprintf("day_of_week = $%d", argCount))
		args = append(args, *input.DayOfWeek)
		argCount++
	}
	if input.MonthOfYear != nil {
		updates = append(updates, fmt.Sprintf("month_of_year = $%d", argCount))
		args = append(args, *input.MonthOfYear)
		argCount++
	}

	args = append(args, templateID, tenantID)
	query := fmt.Sprintf("UPDATE recurring_journal_templates SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount, argCount+1)

	_, err = tx.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update template", "error", err)
		response.InternalError(c, "Failed to update template")
		return
	}

	if len(input.Lines) > 0 {
		var totalDebit, totalCredit float64
		for _, line := range input.Lines {
			totalDebit += line.DebitAmount
			totalCredit += line.CreditAmount
		}
		if math.Abs(totalDebit-totalCredit) > 0.01 {
			response.BadRequest(c, "Debits and credits must be equal")
			return
		}

		tx.Exec("DELETE FROM recurring_journal_template_lines WHERE template_id = $1", templateID)

		for i, line := range input.Lines {
			_, err = tx.Exec(`
				INSERT INTO recurring_journal_template_lines (
					template_id, line_number, account_id, description, debit_amount, credit_amount, contact_id
				) VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, templateID, i+1, line.AccountID, nullIfEmpty(line.Description), line.DebitAmount, line.CreditAmount, nullIfEmpty(line.ContactID))

			if err != nil {
				h.log.Error("Failed to update template line", "error", err)
				response.InternalError(c, "Failed to update template line")
				return
			}
		}

		tx.Exec("UPDATE recurring_journal_templates SET total_debit = $1, total_credit = $2 WHERE id = $3",
			totalDebit, totalCredit, templateID)
	}

	if err = tx.Commit(); err != nil {
		response.InternalError(c, "Failed to save changes")
		return
	}

	response.Success(c, gin.H{"message": "Template updated successfully"})
}

// DeleteRecurringJournalTemplate godoc
// @Summary Delete a recurring journal template
// @Description Soft-delete a recurring journal entry template
// @Tags Finance - Recurring Journals
// @Accept json
// @Produce json
// @Param id path string true "Template ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/recurring-journal-templates/{id} [delete]
func (h *Handler) DeleteRecurringJournalTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	templateID := c.Param("id")
	if templateID == "" {
		response.BadRequest(c, "Template ID is required")
		return
	}

	result, err := h.db.Exec(`
		UPDATE recurring_journal_templates SET deleted_at = NOW(), is_active = false
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, templateID, tenantID)

	if err != nil {
		h.log.Error("Failed to delete template", "error", err)
		response.InternalError(c, "Failed to delete template")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Template not found")
		return
	}

	response.Success(c, gin.H{"message": "Template deleted successfully"})
}

// GenerateRecurringJournalEntry godoc
// @Summary Generate journal entry from template
// @Description Manually generate a journal entry from a recurring journal template
// @Tags Finance - Recurring Journals
// @Accept json
// @Produce json
// @Param id path string true "Template ID"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/recurring-journal-templates/{id}/generate [post]
func (h *Handler) GenerateRecurringJournalEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID := c.GetString("user_id")
	templateID := c.Param("id")
	if templateID == "" {
		response.BadRequest(c, "Template ID is required")
		return
	}

	var input struct {
		EntryDate string `json:"entry_date"`
	}
	c.ShouldBindJSON(&input)

	entryDate := time.Now()
	if input.EntryDate != "" {
		var err error
		entryDate, err = time.Parse("2006-01-02", input.EntryDate)
		if err != nil {
			response.BadRequest(c, "Invalid entry date format")
			return
		}
	}

	var template struct {
		ID          uuid.UUID
		OrgID       *uuid.UUID
		JournalID   uuid.UUID
		Name        string
		Description *string
		Reference   *string
		TotalDebit  float64
		TotalCredit float64
		AutoPost    bool
	}

	err := h.db.QueryRow(`
		SELECT id, organization_id, journal_id, name, description, reference, total_debit, total_credit, auto_post
		FROM recurring_journal_templates
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND is_active = true
	`, templateID, tenantID).Scan(&template.ID, &template.OrgID, &template.JournalID, &template.Name,
		&template.Description, &template.Reference, &template.TotalDebit, &template.TotalCredit, &template.AutoPost)

	if err != nil {
		response.NotFound(c, "Template not found or inactive")
		return
	}

	var alreadyGenerated bool
	h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM recurring_journal_log WHERE template_id = $1 AND generated_for_date = $2)`,
		templateID, entryDate.Format("2006-01-02")).Scan(&alreadyGenerated)
	if alreadyGenerated {
		response.BadRequest(c, "Entry already generated for this date")
		return
	}

	lineRows, err := h.db.Query(`
		SELECT account_id, description, debit_amount, credit_amount, contact_id
		FROM recurring_journal_template_lines
		WHERE template_id = $1
		ORDER BY line_number
	`, templateID)
	if err != nil {
		response.InternalError(c, "Failed to get template lines")
		return
	}
	defer lineRows.Close()

	type Line struct {
		AccountID    uuid.UUID
		Description  *string
		DebitAmount  float64
		CreditAmount float64
		ContactID    *uuid.UUID
	}
	lines := make([]Line, 0)
	for lineRows.Next() {
		var l Line
		lineRows.Scan(&l.AccountID, &l.Description, &l.DebitAmount, &l.CreditAmount, &l.ContactID)
		lines = append(lines, l)
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	var entryNumber string
	var count int
	tx.QueryRow(`SELECT COUNT(*) FROM journal_entries WHERE tenant_id = $1 AND entry_date = $2`, tenantID, entryDate.Format("2006-01-02")).Scan(&count)
	entryNumber = fmt.Sprintf("JE-%s-%04d", entryDate.Format("20060102"), count+1)

	status := "draft"
	if template.AutoPost {
		status = "posted"
	}

	description := template.Name
	if template.Description != nil {
		description = *template.Description
	}

	var journalEntryID uuid.UUID
	err = tx.QueryRow(`
		INSERT INTO journal_entries (
			tenant_id, organization_id, journal_id, entry_number, entry_date,
			description, reference, status, total_debit, total_credit, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id
	`, tenantID, template.OrgID, template.JournalID, entryNumber, entryDate.Format("2006-01-02"),
		description, template.Reference, status, template.TotalDebit, template.TotalCredit, nullIfEmpty(userID)).Scan(&journalEntryID)

	if err != nil {
		h.log.Error("Failed to create journal entry", "error", err)
		response.InternalError(c, "Failed to create journal entry")
		return
	}

	for i, line := range lines {
		_, err = tx.Exec(`
			INSERT INTO journal_entry_lines (
				journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, contact_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, journalEntryID, i+1, line.AccountID, line.Description, line.DebitAmount, line.CreditAmount, line.ContactID)

		if err != nil {
			h.log.Error("Failed to create journal entry line", "error", err)
			response.InternalError(c, "Failed to create journal entry line")
			return
		}
	}

	tx.Exec(`
		INSERT INTO recurring_journal_log (template_id, journal_entry_id, generated_for_date)
		VALUES ($1, $2, $3)
	`, templateID, journalEntryID, entryDate.Format("2006-01-02"))

	tx.Exec(`UPDATE recurring_journal_templates SET last_run_date = $1, updated_at = NOW() WHERE id = $2`,
		entryDate.Format("2006-01-02"), templateID)

	if err = tx.Commit(); err != nil {
		response.InternalError(c, "Failed to save entry")
		return
	}

	response.Success(c, gin.H{
		"message":          "Journal entry generated successfully",
		"journal_entry_id": journalEntryID,
		"entry_number":     entryNumber,
		"status":           status,
	})
}

// GetPendingRecurringEntries godoc
// @Summary Get pending recurring entries
// @Description Get a list of recurring journal templates that are due for generation
// @Tags Finance - Recurring Journals
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/recurring-journal-templates/pending [get]
func (h *Handler) GetPendingRecurringEntries(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	today := time.Now().Format("2006-01-02")

	query := `
		SELECT rjt.id, rjt.name, rjt.frequency, rjt.next_run_date, rjt.total_debit, j.name as journal_name
		FROM recurring_journal_templates rjt
		JOIN journals j ON rjt.journal_id = j.id
		WHERE rjt.tenant_id = $1 AND rjt.deleted_at IS NULL AND rjt.is_active = true
		  AND rjt.next_run_date <= $2
		  AND (rjt.end_date IS NULL OR rjt.end_date >= $2)
		ORDER BY rjt.next_run_date
	`

	rows, err := h.db.Query(query, tenantID, today)
	if err != nil {
		h.log.Error("Failed to get pending entries", "error", err)
		response.InternalError(c, "Failed to get pending entries")
		return
	}
	defer rows.Close()

	type PendingEntry struct {
		ID          uuid.UUID `json:"id"`
		Name        string    `json:"name"`
		Frequency   string    `json:"frequency"`
		NextRunDate string    `json:"next_run_date"`
		TotalDebit  float64   `json:"total_debit"`
		JournalName string    `json:"journal_name"`
	}

	pending := make([]PendingEntry, 0)
	for rows.Next() {
		var p PendingEntry
		var nextRunDate time.Time
		rows.Scan(&p.ID, &p.Name, &p.Frequency, &nextRunDate, &p.TotalDebit, &p.JournalName)
		p.NextRunDate = nextRunDate.Format("2006-01-02")
		pending = append(pending, p)
	}

	response.Success(c, gin.H{
		"pending": pending,
		"count":   len(pending),
	})
}

// =====================================================
// BUDGET CASH FLOW (BDDS) ENDPOINTS
// =====================================================

// GetBudgetCashFlow returns real-time cash position and 30-day forecast
func (h *Handler) GetBudgetCashFlow(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// 1. Current cash balances per account
	accountRows, err := h.db.Query(`
		SELECT a.id, a.code, a.name, a.currency_code,
		       COALESCE(SUM(jl.debit_amount - jl.credit_amount), 0) as balance
		FROM accounts a
		LEFT JOIN journal_entry_lines jl ON jl.account_id = a.id
		LEFT JOIN journal_entries je ON je.id = jl.journal_entry_id AND je.status = 'posted' AND je.tenant_id = $1
		WHERE a.tenant_id = $1 AND a.account_type IN ('asset', 'cash', 'bank')
		  AND a.code LIKE '1%'
		  AND a.is_active = true
		GROUP BY a.id, a.code, a.name, a.currency_code
		HAVING COALESCE(SUM(jl.debit_amount - jl.credit_amount), 0) != 0
		   OR a.code LIKE '101%' OR a.code LIKE '102%'
		ORDER BY a.code
	`, tenantID)

	type AccountBalance struct {
		ID           string  `json:"id"`
		Code         string  `json:"code"`
		Name         string  `json:"name"`
		Currency     string  `json:"currency"`
		Balance      float64 `json:"balance"`
		IsNegative   bool    `json:"is_negative"`
	}

	accountBalances := make([]AccountBalance, 0)
	var totalCash float64

	if err == nil {
		defer accountRows.Close()
		for accountRows.Next() {
			var ab AccountBalance
			var currency sql.NullString
			accountRows.Scan(&ab.ID, &ab.Code, &ab.Name, &currency, &ab.Balance)
			if currency.Valid {
				ab.Currency = currency.String
			} else {
				ab.Currency = "UZS"
			}
			ab.IsNegative = ab.Balance < 0
			totalCash += ab.Balance
			accountBalances = append(accountBalances, ab)
		}
	}

	// 2. Expected receipts (unpaid customer invoices due in next 30 days)
	receiptRows, err := h.db.Query(`
		SELECT ci.id, ci.invoice_number, c.name as counterparty,
		       (ci.total_amount - COALESCE(ci.amount_paid, 0)) as amount,
		       ci.due_date,
		       CASE WHEN ci.due_date < CURRENT_DATE THEN 'overdue'
		            WHEN ci.due_date <= CURRENT_DATE + 7 THEN 'due_soon'
		            ELSE 'upcoming' END as urgency
		FROM customer_invoices ci
		JOIN contacts c ON c.id = ci.customer_id
		WHERE ci.tenant_id = $1
		  AND ci.status IN ('sent', 'partial')
		  AND ci.due_date <= CURRENT_DATE + 30
		ORDER BY ci.due_date ASC
		LIMIT 50
	`, tenantID)

	type ReceivableItem struct {
		ID           string  `json:"id"`
		Reference    string  `json:"reference"`
		Counterparty string  `json:"counterparty"`
		Amount       float64 `json:"amount"`
		DueDate      string  `json:"due_date"`
		Urgency      string  `json:"urgency"`
		Type         string  `json:"type"`
	}

	receivables := make([]ReceivableItem, 0)
	var totalReceivables float64

	if err == nil {
		defer receiptRows.Close()
		for receiptRows.Next() {
			var r ReceivableItem
			var dueDate time.Time
			receiptRows.Scan(&r.ID, &r.Reference, &r.Counterparty, &r.Amount, &dueDate, &r.Urgency)
			r.DueDate = dueDate.Format("2006-01-02")
			r.Type = "invoice"
			totalReceivables += r.Amount
			receivables = append(receivables, r)
		}
	}

	// 3. Expected payments (unpaid vendor bills due in next 30 days)
	payableRows, err := h.db.Query(`
		SELECT vb.id, vb.bill_number, c.name as counterparty,
		       (vb.total_amount - COALESCE(vb.amount_paid, 0)) as amount,
		       vb.due_date,
		       CASE WHEN vb.due_date < CURRENT_DATE THEN 'overdue'
		            WHEN vb.due_date <= CURRENT_DATE + 7 THEN 'due_soon'
		            ELSE 'upcoming' END as urgency
		FROM vendor_bills vb
		JOIN contacts c ON c.id = vb.vendor_id
		WHERE vb.tenant_id = $1
		  AND vb.status IN ('received', 'partial')
		  AND vb.due_date <= CURRENT_DATE + 30
		ORDER BY vb.due_date ASC
		LIMIT 50
	`, tenantID)

	type PayableItem struct {
		ID           string  `json:"id"`
		Reference    string  `json:"reference"`
		Counterparty string  `json:"counterparty"`
		Amount       float64 `json:"amount"`
		DueDate      string  `json:"due_date"`
		Urgency      string  `json:"urgency"`
		CanPostpone  bool    `json:"can_postpone"`
		Priority     string  `json:"priority"`
	}

	payables := make([]PayableItem, 0)
	var totalPayables float64

	if err == nil {
		defer payableRows.Close()
		for payableRows.Next() {
			var p PayableItem
			var dueDate time.Time
			payableRows.Scan(&p.ID, &p.Reference, &p.Counterparty, &p.Amount, &dueDate, &p.Urgency)
			p.DueDate = dueDate.Format("2006-01-02")
			p.CanPostpone = true // Vendor bills can typically be negotiated
			p.Priority = "medium"
			totalPayables += p.Amount
			payables = append(payables, p)
		}
	}

	// 4. Build 30-day payment calendar
	type CalendarDay struct {
		Date           string  `json:"date"`
		Inflows        float64 `json:"inflows"`
		Outflows       float64 `json:"outflows"`
		RunningBalance float64 `json:"running_balance"`
		IsNegative     bool    `json:"is_negative"`
		Events         []map[string]interface{} `json:"events,omitempty"`
	}

	calendarMap := make(map[string]*CalendarDay)
	runningBalance := totalCash

	// Add receivables to calendar
	for _, r := range receivables {
		if _, exists := calendarMap[r.DueDate]; !exists {
			calendarMap[r.DueDate] = &CalendarDay{Date: r.DueDate}
		}
		calendarMap[r.DueDate].Inflows += r.Amount
		calendarMap[r.DueDate].Events = append(calendarMap[r.DueDate].Events, map[string]interface{}{
			"type":         "inflow",
			"description":  r.Counterparty + ": " + r.Reference,
			"amount":       r.Amount,
			"counterparty": r.Counterparty,
		})
	}

	// Add payables to calendar
	for _, p := range payables {
		if _, exists := calendarMap[p.DueDate]; !exists {
			calendarMap[p.DueDate] = &CalendarDay{Date: p.DueDate}
		}
		calendarMap[p.DueDate].Outflows += p.Amount
		calendarMap[p.DueDate].Events = append(calendarMap[p.DueDate].Events, map[string]interface{}{
			"type":         "outflow",
			"description":  p.Counterparty + ": " + p.Reference,
			"amount":       p.Amount,
			"counterparty": p.Counterparty,
		})
	}

	// Build sorted calendar for next 30 days
	calendar := make([]CalendarDay, 0)
	for i := 0; i <= 30; i++ {
		date := time.Now().AddDate(0, 0, i).Format("2006-01-02")
		day := CalendarDay{Date: date, RunningBalance: runningBalance}
		if d, exists := calendarMap[date]; exists {
			day.Inflows = d.Inflows
			day.Outflows = d.Outflows
			day.Events = d.Events
		}
		runningBalance = runningBalance + day.Inflows - day.Outflows
		day.RunningBalance = runningBalance
		day.IsNegative = runningBalance < 0
		calendar = append(calendar, day)
	}

	response.Success(c, gin.H{
		"account_balances": accountBalances,
		"total_cash":       totalCash,
		"receivables":      receivables,
		"payables":         payables,
		"total_receivables": totalReceivables,
		"total_payables":    totalPayables,
		"net_position":     totalCash + totalReceivables - totalPayables,
		"calendar":         calendar,
		"summary": gin.H{
			"current_balance":     totalCash,
			"expected_inflows":    totalReceivables,
			"expected_outflows":   totalPayables,
			"forecast_30d":        totalCash + totalReceivables - totalPayables,
			"has_negative_accounts": func() bool {
				for _, ab := range accountBalances {
					if ab.IsNegative {
						return true
					}
				}
				return false
			}(),
		},
	})
}

// GetBudgetPlanVsActual returns plan vs actual comparison for a budget
func (h *Handler) GetBudgetPlanVsActual(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	budgetID := c.Query("budget_id")
	groupBy := c.DefaultQuery("group_by", "category") // category, department, month

	if budgetID == "" {
		response.BadRequest(c, "budget_id is required")
		return
	}

	// Get budget details
	var budget struct {
		ID        string  `json:"id"`
		Name      string  `json:"name"`
		StartDate string  `json:"start_date"`
		EndDate   string  `json:"end_date"`
		Type      string  `json:"type"`
	}
	h.db.QueryRow(`
		SELECT b.id::text, b.name,
		       COALESCE(b.start_date, fy.start_date::text),
		       COALESCE(b.end_date, fy.end_date::text),
		       b.budget_type
		FROM budgets b
		LEFT JOIN fiscal_years fy ON fy.id = b.fiscal_year_id
		WHERE b.id = $1 AND b.tenant_id = $2
	`, budgetID, tenantID).Scan(&budget.ID, &budget.Name, &budget.StartDate, &budget.EndDate, &budget.Type)

	// Get budget lines with actual amounts from journal entries
	rows, err := h.db.Query(`
		SELECT
			bl.id,
			COALESCE(bl.category_name, a.name) as category,
			a.code as account_code,
			a.name as account_name,
			COALESCE(bl.line_type, 'expense') as line_type,
			COALESCE(bl.budgeted_amount, 0) as planned,
			COALESCE(
				(SELECT SUM(jl.debit_amount - jl.credit_amount)
				 FROM journal_entry_lines jl
				 JOIN journal_entries je ON je.id = jl.journal_entry_id
				 WHERE jl.account_id = bl.account_id
				   AND je.tenant_id = $2
				   AND je.status = 'posted'
				   AND je.entry_date >= $3::date
				   AND je.entry_date <= $4::date), 0
			) as actual
		FROM budget_lines bl
		JOIN accounts a ON a.id = bl.account_id
		WHERE bl.budget_id = $1
		ORDER BY bl.line_type DESC, a.code
	`, budgetID, tenantID, budget.StartDate, budget.EndDate)

	if err != nil {
		h.log.Error("Failed to get plan vs actual", "error", err)
		response.InternalError(c, "Failed to get plan vs actual")
		return
	}
	defer rows.Close()

	type LineItem struct {
		ID          string  `json:"id"`
		Category    string  `json:"category"`
		AccountCode string  `json:"account_code"`
		AccountName string  `json:"account_name"`
		LineType    string  `json:"line_type"`
		Planned     float64 `json:"planned"`
		Actual      float64 `json:"actual"`
		Variance    float64 `json:"variance"`
		VariancePct float64 `json:"variance_pct"`
		Status      string  `json:"status"` // ok, warning, overspent
	}

	items := make([]LineItem, 0)
	var totalPlannedRevenue, totalActualRevenue float64
	var totalPlannedExpense, totalActualExpense float64

	for rows.Next() {
		var item LineItem
		rows.Scan(&item.ID, &item.Category, &item.AccountCode, &item.AccountName,
			&item.LineType, &item.Planned, &item.Actual)

		item.Variance = item.Planned - item.Actual
		if item.Planned != 0 {
			item.VariancePct = (item.Variance / item.Planned) * 100
		}

		usagePct := 0.0
		if item.Planned > 0 {
			usagePct = (item.Actual / item.Planned) * 100
		}

		if item.LineType == "revenue" {
			totalPlannedRevenue += item.Planned
			totalActualRevenue += item.Actual
			if usagePct >= 90 {
				item.Status = "ok"
			} else if usagePct >= 70 {
				item.Status = "warning"
			} else {
				item.Status = "critical"
			}
		} else {
			totalPlannedExpense += item.Planned
			totalActualExpense += item.Actual
			if usagePct > 100 {
				item.Status = "overspent"
			} else if usagePct >= 80 {
				item.Status = "warning"
			} else {
				item.Status = "ok"
			}
		}

		items = append(items, item)
	}

	_ = groupBy // TODO: implement groupBy logic

	response.Success(c, gin.H{
		"budget":    budget,
		"items":     items,
		"totals": gin.H{
			"planned_revenue":  totalPlannedRevenue,
			"actual_revenue":   totalActualRevenue,
			"planned_expense":  totalPlannedExpense,
			"actual_expense":   totalActualExpense,
			"planned_profit":   totalPlannedRevenue - totalPlannedExpense,
			"actual_profit":    totalActualRevenue - totalActualExpense,
			"revenue_variance": totalPlannedRevenue - totalActualRevenue,
			"expense_variance": totalPlannedExpense - totalActualExpense,
		},
	})
}

// SubmitBudgetForApproval submits a budget for approval
func (h *Handler) SubmitBudgetForApproval(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	budgetID := c.Param("id")
	userID, _ := middleware.GetUserID(c)

	_, err := h.db.Exec(`
		UPDATE budgets
		SET approval_status = 'pending', submitted_by = $1, submitted_at = NOW(), updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3 AND status = 'draft'
	`, userID, budgetID, tenantID)

	if err != nil {
		h.log.Error("Failed to submit budget for approval", "error", err)
		response.InternalError(c, "Failed to submit budget for approval")
		return
	}

	response.Success(c, gin.H{"message": "Budget submitted for approval"})
}

// ApproveBudget approves a budget
func (h *Handler) ApproveBudget(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	budgetID := c.Param("id")
	userID, _ := middleware.GetUserID(c)

	_, err := h.db.Exec(`
		UPDATE budgets
		SET approval_status = 'approved', status = 'active',
		    approved_by = $1, approved_at = NOW(), updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3 AND approval_status = 'pending'
	`, userID, budgetID, tenantID)

	if err != nil {
		h.log.Error("Failed to approve budget", "error", err)
		response.InternalError(c, "Failed to approve budget")
		return
	}

	response.Success(c, gin.H{"message": "Budget approved and activated"})
}

// RejectBudget rejects a budget with a reason
func (h *Handler) RejectBudget(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	budgetID := c.Param("id")

	var input struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&input)

	_, err := h.db.Exec(`
		UPDATE budgets
		SET approval_status = 'rejected', rejection_reason = $1,
		    status = 'draft', updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3 AND approval_status = 'pending'
	`, input.Reason, budgetID, tenantID)

	if err != nil {
		h.log.Error("Failed to reject budget", "error", err)
		response.InternalError(c, "Failed to reject budget")
		return
	}

	response.Success(c, gin.H{"message": "Budget rejected"})
}
