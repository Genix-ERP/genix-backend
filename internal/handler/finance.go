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
			   a.code, a.name, a.description, a.currency_id, a.is_bank_account,
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
		searchFilter := fmt.Sprintf(" AND (a.code ILIKE $%d OR a.name ILIKE $%d)", argCount, argCount)
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
		var orgID, parentID, currencyID, description sql.NullString
		var typeCode, typeName, typeCategory, normalBalance string

		err := rows.Scan(
			&acc.ID, &acc.TenantID, &orgID, &parentID, &acc.AccountTypeID,
			&acc.Code, &acc.Name, &description, &currencyID, &acc.IsBankAccount,
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

// GetAccount returns a single account by ID
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
			   a.code, a.name, a.description, a.currency_id, a.is_bank_account,
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
	var orgID, parentID, currencyID, description sql.NullString
	var typeCode, typeName, typeCategory, normalBalance string

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&acc.ID, &acc.TenantID, &orgID, &parentID, &acc.AccountTypeID,
		&acc.Code, &acc.Name, &description, &currencyID, &acc.IsBankAccount,
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

	acc.AccountType = &entity.AccountType{
		Code:          typeCode,
		Name:          typeName,
		Category:      typeCategory,
		NormalBalance: normalBalance,
	}

	response.Success(c, acc.ToResponse())
}

// UpdateAccount updates an existing account
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

// DeleteAccount soft-deletes an account
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

// GetAccountTransactions returns transactions for an account
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

// ListJournalEntries returns a paginated list of journal entries
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

// ListJournals returns all journals for the tenant
func (h *Handler) ListJournals(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, code, name, type,
			COALESCE(description, ''),
			COALESCE(auto_sequence, false),
			COALESCE(next_number, 1),
			COALESCE(number_prefix, ''),
			COALESCE(is_active, true),
			created_at,
			COALESCE(updated_at, created_at)
		FROM journals
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY code ASC
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to query journals", "error", err)
		response.InternalError(c, "Failed to fetch journals")
		return
	}
	defer rows.Close()

	type JournalResponse struct {
		ID           uuid.UUID `json:"id"`
		Code         string    `json:"code"`
		Name         string    `json:"name"`
		Type         string    `json:"type"`
		Description  string    `json:"description,omitempty"`
		AutoSequence bool      `json:"auto_sequence"`
		NextNumber   int       `json:"next_number"`
		NumberPrefix string    `json:"number_prefix,omitempty"`
		IsActive     bool      `json:"is_active"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
	}

	journals := make([]JournalResponse, 0)
	for rows.Next() {
		var j JournalResponse
		if err := rows.Scan(&j.ID, &j.Code, &j.Name, &j.Type, &j.Description, &j.AutoSequence, &j.NextNumber, &j.NumberPrefix, &j.IsActive, &j.CreatedAt, &j.UpdatedAt); err != nil {
			h.log.Error("Failed to scan journal", "error", err)
			continue
		}
		journals = append(journals, j)
	}

	response.Success(c, journals)
}

// GetJournal returns a single journal by ID
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
		IsActive               bool       `json:"is_active"`
		CreatedAt              time.Time  `json:"created_at"`
		UpdatedAt              time.Time  `json:"updated_at"`
	}

	var j JournalResponse
	err = h.db.QueryRow(`
		SELECT id, code, name, type,
			COALESCE(description, ''),
			default_debit_account_id,
			default_credit_account_id,
			COALESCE(auto_sequence, false),
			COALESCE(next_number, 1),
			COALESCE(number_prefix, ''),
			COALESCE(is_active, true),
			created_at,
			COALESCE(updated_at, created_at)
		FROM journals
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&j.ID, &j.Code, &j.Name, &j.Type, &j.Description,
		&j.DefaultDebitAccountID, &j.DefaultCreditAccountID,
		&j.AutoSequence, &j.NextNumber, &j.NumberPrefix, &j.IsActive,
		&j.CreatedAt, &j.UpdatedAt)

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

// CreateJournal creates a new journal
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

	// Check for duplicate code
	var exists bool
	err := h.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM journals WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)
	`, tenantID, input.Code).Scan(&exists)
	if err != nil {
		h.log.Error("Failed to check journal code", "error", err)
		response.InternalError(c, "Failed to create journal")
		return
	}
	if exists {
		response.Conflict(c, "A journal with this code already exists")
		return
	}

	journalID := uuid.New()
	now := time.Now()

	// Convert empty string pointers to nil for nullable UUID columns
	var debitAccID, creditAccID interface{}
	if input.DefaultDebitAccountID != nil && *input.DefaultDebitAccountID != "" {
		debitAccID = *input.DefaultDebitAccountID
	}
	if input.DefaultCreditAccountID != nil && *input.DefaultCreditAccountID != "" {
		creditAccID = *input.DefaultCreditAccountID
	}

	_, err = h.db.Exec(`
		INSERT INTO journals (id, tenant_id, code, name, type, description,
			default_debit_account_id, default_credit_account_id,
			auto_sequence, next_number, number_prefix, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10, true, $11, $11)
	`, journalID, tenantID, input.Code, input.Name, input.Type, input.Description,
		debitAccID, creditAccID,
		input.AutoSequence, input.NumberPrefix, now)

	if err != nil {
		h.log.Error("Failed to create journal", "error", err)
		response.InternalError(c, "Failed to create journal")
		return
	}

	c.Params = append(c.Params, gin.Param{Key: "id", Value: journalID.String()})
	h.GetJournal(c)
}

// UpdateJournal updates an existing journal
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

// DeleteJournal soft-deletes a journal (blocks if it has entries)
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

// CreateJournalEntry creates a new journal entry
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

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, entryDate); errMsg != "" {
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

	// Generate entry number
	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}
	entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

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

	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, id, tenantID, orgID, journalID, entryNumber, entryDate, reference, description,
		sourceType, exchangeRate, totalDebit, totalCredit, "draft", userID, now, now)

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

// GetJournalEntry returns a single journal entry with lines
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
			   je.status, je.posted_at, je.created_at, je.updated_at,
			   j.code as journal_code, j.name as journal_name
		FROM journal_entries je
		JOIN journals j ON je.journal_id = j.id
		WHERE je.id = $1 AND je.tenant_id = $2 AND je.deleted_at IS NULL
	`

	var je entity.JournalEntry
	var ref, desc, sourceType sql.NullString
	var postedAt sql.NullTime
	var journalCode, journalName string

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&je.ID, &je.TenantID, &je.JournalID, &je.EntryNumber, &je.EntryDate,
		&ref, &desc, &sourceType, &je.TotalDebit, &je.TotalCredit,
		&je.Status, &postedAt, &je.CreatedAt, &je.UpdatedAt,
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

// PostJournalEntry posts a journal entry (updates account balances)
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

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, entryDate); errMsg != "" {
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

// ReverseJournalEntry creates a reversal entry
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

	// Get original entry
	var status, entryNumber string
	var journalID uuid.UUID
	var organizationID *uuid.UUID
	var entryDate time.Time
	var totalDebit, totalCredit float64
	var ref, desc sql.NullString

	err = h.db.QueryRow(`
		SELECT status, journal_id, organization_id, entry_number, entry_date, reference, description, total_debit, total_credit
		FROM journal_entries
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, originalID, tenantID).Scan(&status, &journalID, &organizationID, &entryNumber, &entryDate, &ref, &desc, &totalDebit, &totalCredit)

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

	// Get next entry number
	var nextNumber int
	var numberPrefix sql.NullString
	h.db.QueryRow("SELECT COALESCE(next_number, 1), number_prefix FROM journals WHERE id = $1", journalID).Scan(&nextNumber, &numberPrefix)

	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}
	reversalNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

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

	description := fmt.Sprintf("Reversal of %s", entryNumber)
	reference := "REV-" + entryNumber

	// Create reversal entry (swap debit/credit)
	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, total_debit, total_credit, status, posted_at, posted_by,
			reversal_of, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`, reversalID, tenantID, organizationID, journalID, reversalNumber, now, reference, description,
		"reversal", totalCredit, totalDebit, "posted", now, userID,
		originalID, userID, now, now)

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

	// Update account balances (reverse the original posting)
	for _, ol := range origLines {
		// Get normal balance
		var normalBalance string
		tx.QueryRow(`
			SELECT at.normal_balance FROM accounts a
			JOIN account_types at ON a.account_type_id = at.id
			WHERE a.id = $1
		`, ol.accountID).Scan(&normalBalance)

		var balanceChange float64
		if normalBalance == "debit" {
			balanceChange = ol.creditAmount - ol.debitAmount // Reverse
		} else {
			balanceChange = ol.debitAmount - ol.creditAmount // Reverse
		}

		_, err = tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`,
			balanceChange, now, ol.accountID)
		if err != nil {
			h.log.Error("Failed to update account balance", "error", err)
			response.InternalError(c, "Failed to reverse journal entry")
			return
		}
	}

	// Mark original entry as reversed
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

// =====================================================
// PAYMENT HANDLERS
// =====================================================

// ListPayments returns a paginated list of payments
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
			   c.name as contact_name
		FROM payments p
		JOIN contacts c ON p.contact_id = c.id
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
		var ref, notes sql.NullString
		var contactName string

		err := rows.Scan(
			&p.ID, &p.PaymentNumber, &p.Type, &p.ContactID, &p.PaymentDate, &p.Amount,
			&p.Status, &ref, &notes, &p.CreatedAt, &contactName,
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
		payments = append(payments, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, payments, pagination)
}

// CreatePayment creates a new payment
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
	var lastNum int
	prefix := "PAY"
	if input.Type == "receipt" {
		prefix = "REC"
	}
	h.db.QueryRow(`
		SELECT COALESCE(MAX(CAST(SUBSTRING(payment_number FROM '[0-9]+$') AS INTEGER)), 0)
		FROM payments WHERE tenant_id = $1 AND type = $2
	`, tenantID, input.Type).Scan(&lastNum)
	paymentNumber := fmt.Sprintf("%s-%06d", prefix, lastNum+1)

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

	query := `
		INSERT INTO payments (
			id, tenant_id, organization_id, payment_number, type, contact_id, payment_date, amount,
			exchange_rate, reference, notes, status, bank_account_id, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`

	_, err = h.db.Exec(query,
		id, tenantID, orgIDPtr, paymentNumber, input.Type, contactID, paymentDate, input.Amount,
		exchangeRate, reference, notes, "draft", bankAccountID, userID, now, now)

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

// GetPayment returns a single payment
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

// ConfirmPayment confirms a payment and creates journal entry
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
	var orgID, paymentMethodID, bankAccountIDStr sql.NullString
	var paymentDate time.Time
	err = h.db.QueryRow(`
		SELECT status, type, amount, contact_id, organization_id, payment_method_id, payment_date, payment_number, bank_account_id
		FROM payments
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&status, &paymentType, &amount, &contactID, &orgID, &paymentMethodID, &paymentDate, &paymentNumber, &bankAccountIDStr)

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

	// Update allocated sales invoices (if any)
	_, err = tx.Exec(`
		UPDATE sales_invoices SET
			amount_paid = amount_paid + pa.amount,
			status = CASE WHEN amount_paid + pa.amount >= total_amount THEN 'paid' ELSE 'partial' END,
			updated_at = $1
		FROM payment_allocations pa
		WHERE pa.payment_id = $2 AND pa.document_type = 'sales_invoice' AND sales_invoices.id = pa.document_id
	`, now, id)
	if err != nil {
		h.log.Warn("Failed to update sales invoice amounts", "error", err)
	}

	// Update allocated purchase invoices (if any)
	_, err = tx.Exec(`
		UPDATE purchase_invoices SET
			amount_paid = amount_paid + pa.amount,
			status = CASE WHEN amount_paid + pa.amount >= total_amount THEN 'paid' ELSE 'partial' END,
			updated_at = $1
		FROM payment_allocations pa
		WHERE pa.payment_id = $2 AND pa.document_type = 'purchase_invoice' AND purchase_invoices.id = pa.document_id
	`, now, id)
	if err != nil {
		h.log.Warn("Failed to update purchase invoice amounts", "error", err)
	}

	// --- Create journal entry for the payment ---
	// Determine cash/bank account: first try stored bank_account_id, then payment method, then fallback
	var cashAccountID uuid.UUID
	if bankAccountIDStr.Valid {
		cashAccountID, _ = uuid.Parse(bankAccountIDStr.String)
	}
	if cashAccountID == uuid.Nil && paymentMethodID.Valid {
		_ = tx.QueryRow(
			`SELECT account_id FROM payment_methods WHERE id = $1 AND tenant_id = $2`,
			paymentMethodID.String, tenantID,
		).Scan(&cashAccountID)
	}
	if cashAccountID == uuid.Nil {
		// Fallback: look up by name
		cashAccountID = findAccount(tx, tenantID, orgIDPtr, "bank account", "1100")
		if cashAccountID == uuid.Nil {
			cashAccountID = findAccount(tx, tenantID, orgIDPtr, "cash", "1000")
		}
	}

	// Determine the counterpart account (AR or AP) based on payment type
	var counterAccountID uuid.UUID
	var journalCode, sourceType, debitDesc, creditDesc string

	if paymentType == "receipt" {
		// Inbound: customer pays us → Debit Cash, Credit AR
		counterAccountID = findAccount(tx, tenantID, orgIDPtr, "accounts receivable", "1200")
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

	if cashAccountID != uuid.Nil && counterAccountID != uuid.Nil {
		// Get journal
		var journalID uuid.UUID
		var nextNumber int
		var numberPrefix sql.NullString
		err = tx.QueryRow(`
			SELECT id, COALESCE(next_number, 1), number_prefix
			FROM journals WHERE tenant_id = $1 AND (code = $2 OR code = 'GENERAL') AND deleted_at IS NULL
			ORDER BY CASE WHEN code = $2 THEN 0 ELSE 1 END LIMIT 1`,
			tenantID, journalCode,
		).Scan(&journalID, &nextNumber, &numberPrefix)

		if err == nil && journalID != uuid.Nil {
			prefix := ""
			if numberPrefix.Valid {
				prefix = numberPrefix.String
			}
			entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

			description := fmt.Sprintf("Payment %s confirmed", paymentNumber)
			journalEntryID := uuid.New()

			_, err = tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
				journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, paymentDate, paymentNumber, description,
				sourceType, id.String(), 1.0, amount, amount, userID, now, now,
			)

			if err != nil {
				h.log.Error("Failed to create payment journal entry", "error", err)
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

// ListTaxRates returns all tax rates
func (h *Handler) ListTaxRates(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	includeInactive := c.Query("include_inactive") == "true"

	query := `
		SELECT id, tenant_id, code, name, description, rate, type, tax_type,
			   tax_account_id, is_compound, is_recoverable, is_active, created_at, updated_at
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
			&taxAccID, &tr.IsCompound, &tr.IsRecoverable, &tr.IsActive, &tr.CreatedAt, &tr.UpdatedAt,
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

// CreateTaxRate creates a new tax rate
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
			tax_account_id, is_compound, is_recoverable, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, id, tenantID, input.Code, input.Name, description, input.Rate, input.Type, taxType,
		taxAccountID, input.IsCompound, input.IsRecoverable, true, now, now)

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
		IsActive:      true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	response.Created(c, tr)
}

// GetTaxRate returns a single tax rate
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
			   tax_account_id, is_compound, is_recoverable, is_active, created_at, updated_at
		FROM tax_rates
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(
		&tr.ID, &tr.TenantID, &tr.Code, &tr.Name, &desc, &tr.Rate, &tr.Type, &taxType,
		&taxAccID, &tr.IsCompound, &tr.IsRecoverable, &tr.IsActive, &tr.CreatedAt, &tr.UpdatedAt,
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

// UpdateTaxRate updates a tax rate
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

// DeleteTaxRate deletes a tax rate
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

// ListCurrencies returns all currencies
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

// GetCurrency returns a single currency by code
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

// CreateCurrency creates a new currency
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

// UpdateCurrency updates an existing currency
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

// DeleteCurrency deletes a currency (soft delete by setting is_active = false)
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

// GetExchangeRate returns the exchange rate for a currency
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

// SetExchangeRate creates or updates an exchange rate for a currency
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

// ListExchangeRates returns all exchange rates for the tenant
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

// ListBankAccounts returns all bank accounts for the tenant
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

// GetBankAccount returns a single bank account by ID
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

// CreateBankAccount creates a new bank account
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

// UpdateBankAccount updates an existing bank account
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

// DeleteBankAccount soft deletes a bank account
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

// ListBankTransactions returns all transactions for a bank account
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

// CreateBankTransaction creates a new bank transaction
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

// ReconcileBankTransaction marks a transaction as reconciled
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

// ListBankReconciliations returns all reconciliation sessions for a bank account
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

// CreateBankReconciliation starts a new reconciliation session
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

// GetBankReconciliation returns a specific reconciliation with its items
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

// UpdateBankReconciliation updates reconciliation and marks items as cleared
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

// CompleteBankReconciliation finalizes the reconciliation
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

	// Check if reconciliation is balanced (difference should be 0 or within tolerance)
	if difference != nil && *difference != 0 {
		// Allow small tolerance (0.01)
		if *difference > 0.01 || *difference < -0.01 {
			response.BadRequest(c, fmt.Sprintf("Reconciliation has a difference of %.2f. Please resolve before completing.", *difference))
			return
		}
	}

	now := time.Now()

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

	response.Success(c, gin.H{
		"message":      "Reconciliation completed successfully",
		"completed_at": now,
	})
}

// DeleteBankReconciliation deletes a draft reconciliation
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

// ImportBankStatement imports bank transactions from CSV
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

// ListCashTransactions lists all cash transactions
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

// GetCashTransaction gets a single cash transaction
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

// CreateCashTransaction creates a new cash transaction
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

// UpdateCashTransaction updates an existing cash transaction
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

// DeleteCashTransaction deletes a cash transaction
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

// ListFiscalYears lists all fiscal years
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

// GetFiscalYear gets a fiscal year by ID
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

// CreateFiscalYear creates a new fiscal year
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

// UpdateFiscalYear updates a fiscal year
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

// CloseFiscalYear closes a fiscal year
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

// DeleteFiscalYear deletes a fiscal year
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

// ListFiscalPeriods lists all fiscal periods
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

// CreateFiscalPeriod creates a new fiscal period
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

// BatchCreateFiscalPeriods creates multiple fiscal periods at once
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

// CloseFiscalPeriod closes a fiscal period
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

// ReopenFiscalPeriod reopens a fiscal period
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

// ==================== BUDGETS ====================

type CreateBudgetInput struct {
	OrganizationID *string  `json:"organization_id"`
	FiscalYearID   string   `json:"fiscal_year_id" binding:"required"`
	Code           string   `json:"code" binding:"required"`
	Name           string   `json:"name" binding:"required"`
	Description    *string  `json:"description"`
	BudgetType     string   `json:"budget_type"` // expense, revenue, combined
	TotalAmount    float64  `json:"total_amount"`
	Status         string   `json:"status"`
	Lines          []CreateBudgetLineInput `json:"lines"`
}

type CreateBudgetLineInput struct {
	AccountID      string   `json:"account_id" binding:"required"`
	FiscalPeriodID *string  `json:"fiscal_period_id"`
	DepartmentID   *string  `json:"department_id"`
	BudgetedAmount float64  `json:"budgeted_amount" binding:"required"`
	ActualAmount   float64  `json:"actual_amount"`
	Notes          *string  `json:"notes"`
}

// ListBudgets retrieves all budgets for the tenant
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
		       fy.start_date, fy.end_date
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

		err := rows.Scan(
			&b.ID, &b.TenantID, &orgID, &b.FiscalYearID, &b.Code, &b.Name, &desc,
			&b.BudgetType, &b.TotalAmount, &b.Status, &approvedBy, &approvedAt,
			&createdBy, &b.CreatedAt, &b.UpdatedAt,
			&startDate, &endDate,
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

		budgets = append(budgets, &b)
	}

	response.Success(c, budgets)
}

// GetBudget retrieves a single budget by ID
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

	err := h.db.QueryRow(`
		SELECT id, tenant_id, organization_id, fiscal_year_id, code, name, description,
		       budget_type, total_amount, status, approved_by, approved_at,
		       created_by, created_at, updated_at
		FROM budgets
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(
		&b.ID, &b.TenantID, &orgID, &b.FiscalYearID, &b.Code, &b.Name, &desc,
		&b.BudgetType, &b.TotalAmount, &b.Status, &approvedBy, &approvedAt,
		&createdBy, &b.CreatedAt, &b.UpdatedAt,
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

	// Load budget lines
	lineRows, err := h.db.Query(`
		SELECT id, budget_id, account_id, fiscal_period_id, department_id,
		       budgeted_amount, actual_amount, variance, notes, created_at, updated_at
		FROM budget_lines
		WHERE budget_id = $1
		ORDER BY created_at
	`, b.ID)

	if err == nil {
		defer lineRows.Close()
		lines := make([]entity.BudgetLine, 0)

		for lineRows.Next() {
			var line entity.BudgetLine
			var fiscalPeriodID, deptID, notes sql.NullString

			err := lineRows.Scan(
				&line.ID, &line.BudgetID, &line.AccountID, &fiscalPeriodID, &deptID,
				&line.BudgetedAmount, &line.ActualAmount, &line.Variance, &notes,
				&line.CreatedAt, &line.UpdatedAt,
			)
			if err == nil {
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

// CreateBudget creates a new budget
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

	budgetType := input.BudgetType
	if budgetType == "" {
		budgetType = "expense"
	}

	status := input.Status
	if status == "" {
		status = "draft"
	}

	_, err = h.db.Exec(`
		INSERT INTO budgets (id, tenant_id, organization_id, fiscal_year_id, code, name, description,
		                    budget_type, total_amount, status, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, id, tenantID, orgID, fiscalYearID, input.Code, input.Name, input.Description,
		budgetType, input.TotalAmount, status, userID, now, now)

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

	// Fetch created budget
	var b entity.Budget
	var orgIDStr, desc sql.NullString

	err = h.db.QueryRow(`
		SELECT id, tenant_id, organization_id, fiscal_year_id, code, name, description,
		       budget_type, total_amount, status, created_by, created_at, updated_at
		FROM budgets
		WHERE id = $1
	`, id).Scan(
		&b.ID, &b.TenantID, &orgIDStr, &b.FiscalYearID, &b.Code, &b.Name, &desc,
		&b.BudgetType, &b.TotalAmount, &b.Status, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt,
	)

	if orgIDStr.Valid {
		oid, _ := uuid.Parse(orgIDStr.String)
		b.OrganizationID = &oid
	}
	if desc.Valid {
		b.Description = &desc.String
	}

	response.Success(c, b)
}

// UpdateBudget updates an existing budget
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

	result, err := h.db.Exec(`
		UPDATE budgets
		SET code = $1, name = $2, description = $3, budget_type = $4, total_amount = $5, updated_at = $6
		WHERE id = $7 AND tenant_id = $8 AND deleted_at IS NULL
	`, input.Code, input.Name, input.Description, input.BudgetType, input.TotalAmount, now, id, tenantID)

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

	// Fetch updated budget
	var b entity.Budget
	var orgIDStr, desc sql.NullString

	err = h.db.QueryRow(`
		SELECT id, tenant_id, organization_id, fiscal_year_id, code, name, description,
		       budget_type, total_amount, status, created_at, updated_at
		FROM budgets
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&b.ID, &b.TenantID, &orgIDStr, &b.FiscalYearID, &b.Code, &b.Name, &desc,
		&b.BudgetType, &b.TotalAmount, &b.Status, &b.CreatedAt, &b.UpdatedAt,
	)

	if orgIDStr.Valid {
		oid, _ := uuid.Parse(orgIDStr.String)
		b.OrganizationID = &oid
	}
	if desc.Valid {
		b.Description = &desc.String
	}

	response.Success(c, b)
}

// DeleteBudget soft deletes a budget
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

// ActivateBudget activates a budget
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

// ListBudgetLines retrieves budget lines
func (h *Handler) ListBudgetLines(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	budgetID := c.Query("budget_id")

	query := `
		SELECT bl.id, bl.budget_id, bl.account_id, bl.fiscal_period_id, bl.department_id,
		       bl.budgeted_amount, bl.actual_amount, bl.variance, bl.notes, bl.created_at, bl.updated_at
		FROM budget_lines bl
		JOIN budgets b ON bl.budget_id = b.id
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

		err := rows.Scan(
			&line.ID, &line.BudgetID, &line.AccountID, &fiscalPeriodID, &deptID,
			&line.BudgetedAmount, &line.ActualAmount, &line.Variance, &notes,
			&line.CreatedAt, &line.UpdatedAt,
		)
		if err != nil {
			continue
		}

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

// CreateBudgetLine creates a new budget line
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
	err = h.db.QueryRow("SELECT tenant_id FROM budgets WHERE id = $1", input.AccountID).Scan(&budgetTenantID)
	if err != nil || budgetTenantID != tenantID {
		response.BadRequest(c, "Invalid budget")
		return
	}

	_, err = h.db.Exec(`
		INSERT INTO budget_lines (id, budget_id, account_id, fiscal_period_id, department_id,
		                         budgeted_amount, actual_amount, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, id, input.AccountID, accountID, fiscalPeriodID, deptID,
		input.BudgetedAmount, input.ActualAmount, input.Notes, now, now)

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

// UpdateBudgetLine updates an existing budget line
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

// DeleteBudgetLine deletes a budget line
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

// ListRecurringJournalTemplates returns all recurring journal templates
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

// GetRecurringJournalTemplate returns a single template with lines
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

// CreateRecurringJournalTemplate creates a new recurring template
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

// UpdateRecurringJournalTemplate updates a template
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

// DeleteRecurringJournalTemplate soft-deletes a template
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

// GenerateRecurringJournalEntry manually generates an entry from a template
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

// GetPendingRecurringEntries returns templates that are due for generation
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
