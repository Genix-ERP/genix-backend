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

// =====================================================
// ACCOUNT TYPE HANDLERS
// =====================================================

// ListAccountTypes returns all account types
func (h *Handler) ListAccountTypes(c *gin.Context) {
	query := `
		SELECT id, code, name, category, normal_balance, is_system, display_order
		FROM account_types
		ORDER BY display_order ASC, name ASC
	`

	rows, err := h.db.Query(query)
	if err != nil {
		h.log.Error("Failed to list account types", "error", err)
		response.InternalError(c, "Failed to list account types")
		return
	}
	defer rows.Close()

	types := make([]*entity.AccountType, 0)
	for rows.Next() {
		var at entity.AccountType
		err := rows.Scan(&at.ID, &at.Code, &at.Name, &at.Category, &at.NormalBalance, &at.IsSystem, &at.DisplayOrder)
		if err != nil {
			h.log.Error("Failed to scan account type", "error", err)
			continue
		}
		types = append(types, &at)
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

	// Check for duplicate code
	var codeExists bool
	err = h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM accounts WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)",
		tenantID, input.Code,
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
			id, tenant_id, parent_id, account_type_id, code, name, description,
			is_bank_account, is_control_account, is_reconcilable, budget_tracking,
			opening_balance, current_balance, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id
	`

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	err = h.db.QueryRow(query,
		id, tenantID, parentID, accountTypeID, input.Code, input.Name, description,
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

	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, journal_id, entry_number, entry_date, reference, description,
			source_type, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, id, tenantID, journalID, entryNumber, entryDate, reference, description,
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
	err = h.db.QueryRow(`
		SELECT status, total_debit, total_credit FROM journal_entries
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&status, &totalDebit, &totalCredit)

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
	var entryDate time.Time
	var totalDebit, totalCredit float64
	var ref, desc sql.NullString

	err = h.db.QueryRow(`
		SELECT status, journal_id, entry_number, entry_date, reference, description, total_debit, total_credit
		FROM journal_entries
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, originalID, tenantID).Scan(&status, &journalID, &entryNumber, &entryDate, &ref, &desc, &totalDebit, &totalCredit)

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
			id, tenant_id, journal_id, entry_number, entry_date, reference, description,
			source_type, total_debit, total_credit, status, posted_at, posted_by,
			reversal_of, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, reversalID, tenantID, journalID, reversalNumber, now, reference, description,
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

	query := `
		INSERT INTO payments (
			id, tenant_id, payment_number, type, contact_id, payment_date, amount,
			exchange_rate, reference, notes, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`

	_, err = h.db.Exec(query,
		id, tenantID, paymentNumber, input.Type, contactID, paymentDate, input.Amount,
		exchangeRate, reference, notes, "draft", userID, now, now)

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

	// Get payment
	var status, paymentType string
	var amount float64
	err = h.db.QueryRow(`
		SELECT status, type, amount FROM payments
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&status, &paymentType, &amount)

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

	now := time.Now()

	// Update payment status
	_, err = h.db.Exec(`
		UPDATE payments SET status = 'confirmed', approved_by = $1, approved_at = $2, updated_at = $2
		WHERE id = $3
	`, userID, now, id)

	if err != nil {
		h.log.Error("Failed to confirm payment", "error", err)
		response.InternalError(c, "Failed to confirm payment")
		return
	}

	// Update allocated invoices (if any)
	_, err = h.db.Exec(`
		UPDATE sales_invoices SET
			amount_paid = amount_paid + pa.amount,
			status = CASE WHEN amount_paid + pa.amount >= total_amount THEN 'paid' ELSE 'partial' END,
			updated_at = $1
		FROM payment_allocations pa
		WHERE pa.payment_id = $2 AND pa.document_type = 'sales_invoice' AND sales_invoices.id = pa.document_id
	`, now, id)

	if err != nil {
		h.log.Warn("Failed to update invoice amounts", "error", err)
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
		SELECT id, tenant_id, code, name, description, rate, type,
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
		var desc, taxAccID sql.NullString

		err := rows.Scan(
			&tr.ID, &tr.TenantID, &tr.Code, &tr.Name, &desc, &tr.Rate, &tr.Type,
			&taxAccID, &tr.IsCompound, &tr.IsRecoverable, &tr.IsActive, &tr.CreatedAt, &tr.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if desc.Valid {
			tr.Description = &desc.String
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

	_, err := h.db.Exec(`
		INSERT INTO tax_rates (id, tenant_id, code, name, description, rate, type,
			tax_account_id, is_compound, is_recoverable, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, id, tenantID, input.Code, input.Name, description, input.Rate, input.Type,
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
	var desc, taxAccID sql.NullString

	err = h.db.QueryRow(`
		SELECT id, tenant_id, code, name, description, rate, type,
			   tax_account_id, is_compound, is_recoverable, is_active, created_at, updated_at
		FROM tax_rates
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(
		&tr.ID, &tr.TenantID, &tr.Code, &tr.Name, &desc, &tr.Rate, &tr.Type,
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
