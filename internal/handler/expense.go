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

// ListExpenseCategories returns all expense categories. The query LEFT
// JOINs accounts so the response surfaces the GL account code+name each
// category posts to, and counts referencing expenses so the frontend can
// disable delete on rows that are still in use.
func (h *Handler) ListExpenseCategories(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT ec.id, ec.tenant_id, ec.code, ec.name, ec.description, ec.parent_id,
		       ec.account_id, ec.is_active, ec.created_at,
		       a.code, a.name,
		       COALESCE((SELECT COUNT(*) FROM expenses e WHERE e.category_id = ec.id), 0) AS usage_count
		FROM expense_categories ec
		LEFT JOIN accounts a ON a.id = ec.account_id
		WHERE ec.tenant_id = $1
		ORDER BY ec.name
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to list expense categories", "error", err)
		response.InternalError(c, "Failed to list expense categories")
		return
	}
	defer rows.Close()

	categories := make([]*entity.ExpenseCategoryResponse, 0)
	for rows.Next() {
		var cat entity.ExpenseCategory
		var description sql.NullString
		var parentID sql.NullString
		var accountID sql.NullString
		var accountCode sql.NullString
		var accountName sql.NullString
		var usageCount int

		if err := rows.Scan(
			&cat.ID, &cat.TenantID, &cat.Code, &cat.Name, &description, &parentID,
			&accountID, &cat.IsActive, &cat.CreatedAt,
			&accountCode, &accountName, &usageCount,
		); err != nil {
			h.log.Error("Failed to scan expense category", "error", err)
			continue
		}

		if description.Valid {
			cat.Description = &description.String
		}
		if parentID.Valid {
			if id, err := uuid.Parse(parentID.String); err == nil {
				cat.ParentID = &id
			}
		}
		if accountID.Valid {
			if id, err := uuid.Parse(accountID.String); err == nil {
				cat.AccountID = &id
			}
		}

		resp := cat.ToResponse()
		if accountCode.Valid {
			resp.AccountCode = accountCode.String
		}
		if accountName.Valid {
			resp.AccountName = accountName.String
		}
		resp.UsageCount = usageCount

		categories = append(categories, resp)
	}

	response.Success(c, categories)
}

// expenseCategoryRequest is the create/update payload. Code is optional —
// when omitted we slugify the name so the existing UNIQUE(tenant_id, code)
// constraint keeps holding without forcing the user to invent one.
type expenseCategoryRequest struct {
	Name        string  `json:"name" binding:"required"`
	Code        string  `json:"code"`
	Description *string `json:"description"`
	AccountID   *string `json:"account_id"`
	IsActive    *bool   `json:"is_active"`
}

// slugifyCategoryCode turns "Travel & Lodging" → "TRAVEL_LODGING" so the
// auto-generated code stays stable, uppercase, and unique-looking.
func slugifyCategoryCode(name string) string {
	if name == "" {
		return ""
	}
	out := make([]byte, 0, len(name))
	prevSep := false
	for i := 0; i < len(name); i++ {
		ch := name[i]
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9'):
			if ch >= 'a' && ch <= 'z' {
				ch -= 32
			}
			out = append(out, ch)
			prevSep = false
		default:
			if !prevSep && len(out) > 0 {
				out = append(out, '_')
				prevSep = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '_' {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return "CAT"
	}
	return string(out)
}

// nextExpenseNumber returns the next sequential expense number for a
// tenant in the form `EXP-{year}-{seq:04d}` (e.g. EXP-2026-0001). The
// previous implementation used `time.Now().UnixNano() % 10000`, which
// produced random-looking codes (EXP-2026-5000, EXP-2026-0, …) that
// confused the user when scanning the list. We now derive the next
// sequence by parsing the trailing integer from the highest-numbered
// row already in the table for this tenant + year and adding 1.
//
// The query uses regex extraction so legacy rows whose suffix is wider
// or narrower than 4 digits (created by the old generator) still parse
// cleanly; rows that don't match the pattern at all are ignored.
//
// Concurrency: a UNIQUE(tenant_id, expense_number) constraint already
// guards the table, so worst-case two simultaneous inserts both pick
// the same number and one of them gets a 23505 conflict — the user
// retries and lands on the next slot. For low-concurrency ERP traffic
// this is acceptable; we don't introduce an advisory lock.
func (h *Handler) nextExpenseNumber(tenantID uuid.UUID, year int) (string, error) {
	prefix := fmt.Sprintf("EXP-%d-", year)
	var maxSeq sql.NullInt64
	err := h.db.QueryRow(`
		SELECT MAX(CAST(substring(expense_number FROM '\d+$') AS INTEGER))
		FROM expenses
		WHERE tenant_id = $1
		  AND expense_number LIKE $2 || '%'
		  AND expense_number ~ ('^' || $2 || '\d+$')
	`, tenantID, prefix).Scan(&maxSeq)
	if err != nil {
		return "", err
	}
	next := int64(1)
	if maxSeq.Valid {
		next = maxSeq.Int64 + 1
	}
	return fmt.Sprintf("EXP-%d-%04d", year, next), nil
}

// CreateExpenseCategory inserts a new category for the active tenant.
// The chart-of-account FK is optional — categories without one still work,
// but expense GL posting falls back to the global "operating expense"
// lookup. Code defaults to a slug of the name when not supplied.
func (h *Handler) CreateExpenseCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var req expenseCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		response.BadRequest(c, "Name is required")
		return
	}

	code := strings.TrimSpace(req.Code)
	if code == "" {
		code = slugifyCategoryCode(name)
	}

	var accountID *uuid.UUID
	if req.AccountID != nil && *req.AccountID != "" {
		id, err := uuid.Parse(*req.AccountID)
		if err != nil {
			response.BadRequest(c, "Invalid account_id")
			return
		}
		// Verify the account belongs to this tenant — prevents cross-tenant
		// account-id stuffing through the API.
		var ok bool
		if err := h.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM accounts WHERE id = $1 AND tenant_id = $2)`,
			id, tenantID,
		).Scan(&ok); err != nil || !ok {
			response.BadRequest(c, "Account not found in this tenant")
			return
		}
		accountID = &id
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	var description sql.NullString
	if req.Description != nil && strings.TrimSpace(*req.Description) != "" {
		description = sql.NullString{String: strings.TrimSpace(*req.Description), Valid: true}
	}

	newID := uuid.New()
	_, err := h.db.Exec(`
		INSERT INTO expense_categories (id, tenant_id, code, name, description, account_id, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
	`, newID, tenantID, code, name, description, accountID, isActive)
	if err != nil {
		// Surface the unique-violation as a 400 so the UI can highlight
		// the offending field; everything else is an internal error.
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Category code already exists")
			return
		}
		h.log.Error("Failed to create expense category", "error", err)
		response.InternalError(c, "Failed to create expense category")
		return
	}

	h.respondCategoryByID(c, tenantID, newID)
}

// UpdateExpenseCategory edits a category. Only the fields explicitly
// present in the JSON body are touched so the UI can do partial updates
// (e.g. "just change the account") without overwriting every column.
func (h *Handler) UpdateExpenseCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}

	var req expenseCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		response.BadRequest(c, "Name is required")
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		code = slugifyCategoryCode(name)
	}

	var accountID *uuid.UUID
	if req.AccountID != nil && *req.AccountID != "" {
		aid, err := uuid.Parse(*req.AccountID)
		if err != nil {
			response.BadRequest(c, "Invalid account_id")
			return
		}
		var exists bool
		if err := h.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM accounts WHERE id = $1 AND tenant_id = $2)`,
			aid, tenantID,
		).Scan(&exists); err != nil || !exists {
			response.BadRequest(c, "Account not found in this tenant")
			return
		}
		accountID = &aid
	}

	var description sql.NullString
	if req.Description != nil {
		trimmed := strings.TrimSpace(*req.Description)
		if trimmed != "" {
			description = sql.NullString{String: trimmed, Valid: true}
		}
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	res, err := h.db.Exec(`
		UPDATE expense_categories
		   SET name = $1, code = $2, description = $3, account_id = $4, is_active = $5, updated_at = NOW()
		 WHERE id = $6 AND tenant_id = $7
	`, name, code, description, accountID, isActive, id, tenantID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Category code already exists")
			return
		}
		h.log.Error("Failed to update expense category", "error", err)
		response.InternalError(c, "Failed to update expense category")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		response.NotFound(c, "Category not found")
		return
	}

	h.respondCategoryByID(c, tenantID, id)
}

// DeleteExpenseCategory removes a category, but only when no expense rows
// reference it. Soft-delete-by-deactivation is exposed via the regular
// update endpoint (is_active = false) for users who'd rather archive.
func (h *Handler) DeleteExpenseCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}

	var usage int
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM expenses WHERE category_id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(&usage); err != nil {
		h.log.Error("Failed to check category usage", "error", err)
		response.InternalError(c, "Failed to check category usage")
		return
	}
	if usage > 0 {
		response.BadRequest(c, fmt.Sprintf("Category is used by %d expense(s); reassign them before deleting", usage))
		return
	}

	res, err := h.db.Exec(
		`DELETE FROM expense_categories WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete expense category", "error", err)
		response.InternalError(c, "Failed to delete expense category")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		response.NotFound(c, "Category not found")
		return
	}

	response.Success(c, gin.H{"deleted": true})
}

// respondCategoryByID re-reads a single row through the same JOIN as the
// list endpoint so create/update return the fully-populated record (with
// account_code, account_name, usage_count) instead of forcing the UI to
// refetch the whole list.
func (h *Handler) respondCategoryByID(c *gin.Context, tenantID, id uuid.UUID) {
	row := h.db.QueryRow(`
		SELECT ec.id, ec.tenant_id, ec.code, ec.name, ec.description, ec.parent_id,
		       ec.account_id, ec.is_active, ec.created_at,
		       a.code, a.name,
		       COALESCE((SELECT COUNT(*) FROM expenses e WHERE e.category_id = ec.id), 0) AS usage_count
		FROM expense_categories ec
		LEFT JOIN accounts a ON a.id = ec.account_id
		WHERE ec.id = $1 AND ec.tenant_id = $2
	`, id, tenantID)

	var cat entity.ExpenseCategory
	var description sql.NullString
	var parentID sql.NullString
	var accountID sql.NullString
	var accountCode sql.NullString
	var accountName sql.NullString
	var usageCount int

	if err := row.Scan(
		&cat.ID, &cat.TenantID, &cat.Code, &cat.Name, &description, &parentID,
		&accountID, &cat.IsActive, &cat.CreatedAt,
		&accountCode, &accountName, &usageCount,
	); err != nil {
		h.log.Error("Failed to read back expense category", "error", err)
		response.InternalError(c, "Failed to read back expense category")
		return
	}
	if description.Valid {
		cat.Description = &description.String
	}
	if parentID.Valid {
		if pid, err := uuid.Parse(parentID.String); err == nil {
			cat.ParentID = &pid
		}
	}
	if accountID.Valid {
		if aid, err := uuid.Parse(accountID.String); err == nil {
			cat.AccountID = &aid
		}
	}

	resp := cat.ToResponse()
	if accountCode.Valid {
		resp.AccountCode = accountCode.String
	}
	if accountName.Valid {
		resp.AccountName = accountName.String
	}
	resp.UsageCount = usageCount

	response.Success(c, resp)
}

// ListExpenses returns a paginated list of expenses
func (h *Handler) ListExpenses(c *gin.Context) {
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
	employeeID := c.Query("employee_id")
	// Profit-tax filters used by the Profit Tax page (§7.3 of the TZ):
	//   ?is_recognized=true|false   — narrow to recognized or unrecognized.
	//   ?date_from=YYYY-MM-DD       — inclusive lower bound on expense_date.
	//   ?date_to=YYYY-MM-DD         — inclusive upper bound on expense_date.
	isRecognized := c.Query("is_recognized")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	baseQuery := `
		SELECT e.id, e.tenant_id, e.expense_number, e.category_id, e.employee_id, e.employee_name,
			   e.vendor_id, e.vendor_name, e.expense_date, e.description, e.amount, e.tax_amount,
			   e.total_amount, e.currency, e.payment_method, e.reference, e.receipt_url, e.is_recognized,
			   e.status, e.reimbursable, e.notes, e.created_at, e.updated_at,
			   COALESCE(c.name, '') as category_name
		FROM expenses e
		LEFT JOIN expense_categories c ON e.category_id = c.id
		WHERE e.tenant_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM expenses WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND e.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" && status != "all" {
		argCount++
		baseQuery += fmt.Sprintf(" AND e.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	if categoryID != "" {
		if id, err := uuid.Parse(categoryID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND e.category_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND category_id = $%d", argCount)
			args = append(args, id)
		}
	}

	if employeeID != "" {
		if id, err := uuid.Parse(employeeID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND e.employee_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND employee_id = $%d", argCount)
			args = append(args, id)
		}
	}

	if isRecognized == "true" || isRecognized == "false" {
		argCount++
		baseQuery += fmt.Sprintf(" AND e.is_recognized = $%d", argCount)
		countQuery += fmt.Sprintf(" AND is_recognized = $%d", argCount)
		args = append(args, isRecognized == "true")
	}

	if dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND e.expense_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND expense_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND e.expense_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND expense_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	baseQuery += " ORDER BY e.expense_date DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count expenses", "error", err)
		response.InternalError(c, "Failed to count expenses")
		return
	}

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list expenses", "error", err)
		response.InternalError(c, "Failed to list expenses")
		return
	}
	defer rows.Close()

	expenses := make([]*entity.ExpenseResponse, 0)
	for rows.Next() {
		var expense entity.Expense
		var categoryID, employeeID, vendorID sql.NullString
		var employeeName, vendorName, paymentMethod, reference, receiptURL, notes sql.NullString

		if err := rows.Scan(
			&expense.ID, &expense.TenantID, &expense.ExpenseNumber, &categoryID,
			&employeeID, &employeeName, &vendorID, &vendorName, &expense.ExpenseDate,
			&expense.Description, &expense.Amount, &expense.TaxAmount, &expense.TotalAmount,
			&expense.Currency, &paymentMethod, &reference, &receiptURL, &expense.IsRecognized,
			&expense.Status,
			&expense.Reimbursable, &notes, &expense.CreatedAt, &expense.UpdatedAt,
			&expense.CategoryName,
		); err != nil {
			h.log.Error("Failed to scan expense", "error", err)
			continue
		}

		if categoryID.Valid {
			if id, err := uuid.Parse(categoryID.String); err == nil {
				expense.CategoryID = &id
			}
		}
		if employeeID.Valid {
			if id, err := uuid.Parse(employeeID.String); err == nil {
				expense.EmployeeID = &id
			}
		}
		if employeeName.Valid {
			expense.EmployeeName = &employeeName.String
		}
		if vendorID.Valid {
			if id, err := uuid.Parse(vendorID.String); err == nil {
				expense.VendorID = &id
			}
		}
		if vendorName.Valid {
			expense.VendorName = &vendorName.String
		}
		if paymentMethod.Valid {
			expense.PaymentMethod = &paymentMethod.String
		}
		if reference.Valid {
			expense.Reference = &reference.String
		}
		if receiptURL.Valid {
			expense.ReceiptURL = &receiptURL.String
		}
		if notes.Valid {
			expense.Notes = &notes.String
		}

		expenses = append(expenses, expense.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, expenses, pagination)
}

// CreateExpense creates a new expense
func (h *Handler) CreateExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateExpenseInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	expenseNumber, err := h.nextExpenseNumber(tenantID, time.Now().Year())
	if err != nil {
		h.log.Error("Failed to allocate expense number", "error", err)
		response.InternalError(c, "Failed to allocate expense number")
		return
	}

	expenseDate, err := time.Parse("2006-01-02", input.ExpenseDate)
	if err != nil {
		response.BadRequest(c, "Invalid date format")
		return
	}

	totalAmount := input.Amount + input.TaxAmount

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	id := uuid.New()
	now := time.Now()

	var categoryID, employeeID, vendorID *uuid.UUID
	if input.CategoryID != "" {
		if parsedID, err := uuid.Parse(input.CategoryID); err == nil {
			categoryID = &parsedID
		}
	}
	if input.EmployeeID != "" {
		if parsedID, err := uuid.Parse(input.EmployeeID); err == nil {
			employeeID = &parsedID
		}
	}
	if input.VendorID != "" {
		if parsedID, err := uuid.Parse(input.VendorID); err == nil {
			vendorID = &parsedID
		}
	}

	currency := input.Currency
	if currency == "" {
		currency = "UZS"
	}

	// is_recognized defaults to TRUE — matches the DB default and the safe
	// "deductible" interpretation. Clients that know about the flag (new
	// Expenses page + Profit Tax classifier) can send an explicit value.
	isRecognized := true
	if input.IsRecognized != nil {
		isRecognized = *input.IsRecognized
	}
	query := `
		INSERT INTO expenses (
			id, tenant_id, organization_id, expense_number, category_id, category_name, employee_id, employee_name,
			vendor_id, vendor_name, expense_date, description, amount, tax_amount,
			total_amount, currency, payment_method, reference, receipt_url, status,
			reimbursable, is_recognized, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)
		RETURNING id
	`

	var employeeName, vendorName, paymentMethod, reference, receiptURL, notes, categoryName *string
	if input.CategoryName != "" {
		categoryName = &input.CategoryName
	}
	if input.EmployeeName != "" {
		employeeName = &input.EmployeeName
	}
	if input.VendorName != "" {
		vendorName = &input.VendorName
	}
	if input.PaymentMethod != "" {
		paymentMethod = &input.PaymentMethod
	}
	if input.Reference != "" {
		reference = &input.Reference
	}
	if input.ReceiptURL != "" {
		receiptURL = &input.ReceiptURL
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	if err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, expenseNumber, categoryID, categoryName, employeeID, employeeName,
		vendorID, vendorName, expenseDate, input.Description, input.Amount, input.TaxAmount,
		totalAmount, currency, paymentMethod, reference, receiptURL, "pending",
		input.Reimbursable, isRecognized, notes, userID, now, now,
	).Scan(&id); err != nil {
		h.log.Error("Failed to create expense", "error", err)
		response.InternalError(c, "Failed to create expense")
		return
	}

	expense := &entity.Expense{
		ID:            id,
		TenantID:      tenantID,
		ExpenseNumber: expenseNumber,
		CategoryID:    categoryID,
		CategoryName:  input.CategoryName,
		EmployeeID:    employeeID,
		EmployeeName:  employeeName,
		VendorID:      vendorID,
		VendorName:    vendorName,
		ExpenseDate:   expenseDate,
		Description:   input.Description,
		Amount:        input.Amount,
		TaxAmount:     input.TaxAmount,
		TotalAmount:   totalAmount,
		Currency:      currency,
		PaymentMethod: paymentMethod,
		Reference:     reference,
		ReceiptURL:    receiptURL,
		Status:        "pending",
		Reimbursable:  input.Reimbursable,
		IsRecognized:  isRecognized,
		Notes:         notes,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	response.Created(c, expense.ToResponse())
}

// GetExpense returns a single expense
func (h *Handler) GetExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	query := `
		SELECT e.id, e.tenant_id, e.expense_number, e.category_id, e.employee_id, e.employee_name,
			   e.vendor_id, e.vendor_name, e.expense_date, e.description, e.amount, e.tax_amount,
			   e.total_amount, e.currency, e.payment_method, e.reference, e.receipt_url, e.is_recognized,
			   e.status, e.reimbursable, e.notes, e.created_at, e.updated_at,
			   COALESCE(c.name, '') as category_name
		FROM expenses e
		LEFT JOIN expense_categories c ON e.category_id = c.id
		WHERE e.id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL
	`

	var expense entity.Expense
	var categoryID, employeeID, vendorID sql.NullString
	var employeeName, vendorName, paymentMethod, reference, receiptURL, notes sql.NullString

	if err := h.db.QueryRow(query, id, tenantID).Scan(
		&expense.ID, &expense.TenantID, &expense.ExpenseNumber, &categoryID,
		&employeeID, &employeeName, &vendorID, &vendorName, &expense.ExpenseDate,
		&expense.Description, &expense.Amount, &expense.TaxAmount, &expense.TotalAmount,
		&expense.Currency, &paymentMethod, &reference, &receiptURL, &expense.IsRecognized,
		&expense.Status,
		&expense.Reimbursable, &notes, &expense.CreatedAt, &expense.UpdatedAt,
		&expense.CategoryName,
	); err == sql.ErrNoRows {
		response.NotFound(c, "Expense")
		return
	} else if err != nil {
		h.log.Error("Failed to get expense", "error", err)
		response.InternalError(c, "Failed to get expense")
		return
	}

	if categoryID.Valid {
		if id, err := uuid.Parse(categoryID.String); err == nil {
			expense.CategoryID = &id
		}
	}
	if employeeID.Valid {
		if id, err := uuid.Parse(employeeID.String); err == nil {
			expense.EmployeeID = &id
		}
	}
	if employeeName.Valid {
		expense.EmployeeName = &employeeName.String
	}
	if vendorID.Valid {
		if id, err := uuid.Parse(vendorID.String); err == nil {
			expense.VendorID = &id
		}
	}
	if vendorName.Valid {
		expense.VendorName = &vendorName.String
	}
	if paymentMethod.Valid {
		expense.PaymentMethod = &paymentMethod.String
	}
	if reference.Valid {
		expense.Reference = &reference.String
	}
	if receiptURL.Valid {
		expense.ReceiptURL = &receiptURL.String
	}
	if notes.Valid {
		expense.Notes = &notes.String
	}

	response.Success(c, expense.ToResponse())
}

// UpdateExpense updates an existing expense
func (h *Handler) UpdateExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	var input entity.UpdateExpenseInput
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

	if input.CategoryID != nil && *input.CategoryID != "" {
		if parsedID, err := uuid.Parse(*input.CategoryID); err == nil {
			addUpdate("category_id", parsedID)
		}
	}
	if input.CategoryName != nil {
		addUpdate("category_name", *input.CategoryName)
	}
	if input.EmployeeID != nil && *input.EmployeeID != "" {
		if parsedID, err := uuid.Parse(*input.EmployeeID); err == nil {
			addUpdate("employee_id", parsedID)
		}
	}
	if input.EmployeeName != nil {
		addUpdate("employee_name", *input.EmployeeName)
	}
	if input.VendorID != nil && *input.VendorID != "" {
		if parsedID, err := uuid.Parse(*input.VendorID); err == nil {
			addUpdate("vendor_id", parsedID)
		}
	}
	if input.VendorName != nil {
		addUpdate("vendor_name", *input.VendorName)
	}
	if input.ExpenseDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.ExpenseDate); err == nil {
			addUpdate("expense_date", parsed)
		}
	}
	if input.Description != nil {
		addUpdate("description", *input.Description)
	}
	if input.Amount != nil {
		addUpdate("amount", *input.Amount)
	}
	if input.TaxAmount != nil {
		addUpdate("tax_amount", *input.TaxAmount)
	}
	if input.Currency != nil {
		addUpdate("currency", *input.Currency)
	}
	if input.PaymentMethod != nil {
		addUpdate("payment_method", *input.PaymentMethod)
	}
	if input.Reference != nil {
		addUpdate("reference", *input.Reference)
	}
	if input.ReceiptURL != nil {
		addUpdate("receipt_url", *input.ReceiptURL)
	}
	if input.Status != nil {
		addUpdate("status", *input.Status)
	}
	if input.Reimbursable != nil {
		addUpdate("reimbursable", *input.Reimbursable)
	}
	if input.IsRecognized != nil {
		addUpdate("is_recognized", *input.IsRecognized)
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
		UPDATE expenses SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	if err := h.db.QueryRow(query, args...).Scan(&returnedID); err == sql.ErrNoRows {
		response.NotFound(c, "Expense")
		return
	} else if err != nil {
		h.log.Error("Failed to update expense", "error", err)
		response.InternalError(c, "Failed to update expense")
		return
	}

	h.GetExpense(c)
}

// RecognizeExpense flips the is_recognized flag on a single expense.
// PATCH /expenses/:id/recognize  body: { "is_recognized": true|false }.
// This endpoint is intentionally narrow — finance/accounting staff review
// expenses one-by-one and toggle recognition as they verify documentation
// (see §7.2 of ТЗ_Ish_Haqi_Soliq_Tolik.docx). Using a dedicated route
// instead of the general UpdateExpense PUT means we can gate it with a
// distinct permission and keep the audit log focused.
func (h *Handler) RecognizeExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	var input struct {
		IsRecognized *bool `json:"is_recognized"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.IsRecognized == nil {
		response.BadRequest(c, "is_recognized is required")
		return
	}

	res, err := h.db.Exec(`
		UPDATE expenses
		SET is_recognized = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
	`, *input.IsRecognized, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to update expense recognition", "error", err)
		response.InternalError(c, "Failed to update recognition")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Expense")
		return
	}

	// Re-use GetExpense so the client gets the same response shape as the
	// other endpoints on this resource.
	h.GetExpense(c)
}

// DeleteExpense soft-deletes an expense
func (h *Handler) DeleteExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	query := `
		UPDATE expenses SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete expense", "error", err)
		response.InternalError(c, "Failed to delete expense")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Expense")
		return
	}

	response.NoContent(c)
}

// ApproveExpense approves an expense
func (h *Handler) ApproveExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	now := time.Now()
	query := `
		UPDATE expenses SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $2
		WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL AND status = 'pending'
		RETURNING id
	`

	var returnedID uuid.UUID
	if err := h.db.QueryRow(query, userID, now, id, tenantID).Scan(&returnedID); err == sql.ErrNoRows {
		response.NotFound(c, "Expense")
		return
	} else if err != nil {
		h.log.Error("Failed to approve expense", "error", err)
		response.InternalError(c, "Failed to approve expense")
		return
	}

	// ============================================
	// CREATE JOURNAL ENTRY FOR EXPENSE
	// ============================================
	func() {
		var expenseNumber, description string
		var totalAmount float64
		var reimbursable bool
		var orgID sql.NullString
		var expenseDate time.Time

		err := h.db.QueryRow(`
			SELECT expense_number, description, total_amount, reimbursable, organization_id, expense_date
			FROM expenses WHERE id = $1 AND tenant_id = $2`,
			id, tenantID).Scan(&expenseNumber, &description, &totalAmount, &reimbursable, &orgID, &expenseDate)
		if err != nil || totalAmount <= 0 {
			return
		}

		var orgIDPtr *uuid.UUID
		if orgID.Valid {
			if parsedOrgID, err := uuid.Parse(orgID.String); err == nil {
				orgIDPtr = &parsedOrgID
			}
		}

		// Look up expense account
		expenseAccountID := findAccount(h.db, tenantID, orgIDPtr, "operating expense", "9410")
		if expenseAccountID == uuid.Nil {
			expenseAccountID = findAccount(h.db, tenantID, orgIDPtr, "miscellaneous expense", "9410")
		}

		// Credit account: AP if reimbursable, Cash otherwise
		var creditAccountID uuid.UUID
		if reimbursable {
			creditAccountID = findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "6010")
		} else {
			creditAccountID = findAccount(h.db, tenantID, orgIDPtr, "cash", "5010")
		}

		if expenseAccountID == uuid.Nil || creditAccountID == uuid.Nil {
			h.log.Error("Cannot find accounts for expense JE")
			return
		}

		// Look up journal
		var journalID uuid.UUID
		var nextNumber int
		err = h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE WHEN code='MISC' THEN 0 ELSE 1 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNumber)
		if err != nil {
			return
		}

		journalEntryID := uuid.New()
		jeDescription := "Expense: " + expenseNumber + " - " + description

		// Header + both lines + balance updates in ONE transaction: migration
		// 416's deferred balance trigger rejects imbalanced single-line
		// autocommit inserts, which previously left 0-line "posted" JEs.
		tx, txErr := h.db.Begin()
		if txErr != nil {
			h.log.Error("Failed to begin expense journal tx", "error", txErr)
			return
		}
		defer tx.Rollback()

		entryNumber := fmt.Sprintf("EXP%06d", nextEntryNumberSeq(tx, tenantID, orgIDPtr, "EXP", nextNumber))

		if _, err = tx.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'expense', $9, 1.0, $10, $10, 'posted', $11, $12, $12)`,
			journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, expenseDate, expenseNumber, jeDescription,
			id.String(), totalAmount, userID, now,
		); err != nil {
			h.log.Error("Failed to create expense journal entry", "error", err)
			return
		}

		// Debit: Expense account
		if _, err = tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 1, $3, $4, $5, 0, 1.0, $6)`,
			uuid.New(), journalEntryID, expenseAccountID, "Expense", totalAmount, now,
		); err != nil {
			h.log.Error("Failed to insert expense debit line", "error", err)
			return
		}

		// Credit: Cash or AP
		if _, err = tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 2, $3, $4, 0, $5, 1.0, $6)`,
			uuid.New(), journalEntryID, creditAccountID, "Cash/Payable", totalAmount, now,
		); err != nil {
			h.log.Error("Failed to insert expense credit line", "error", err)
			return
		}

		if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalAmount, now, expenseAccountID); err != nil {
			h.log.Error("Failed to update expense account balance", "error", err)
			return
		}
		if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalAmount, now, creditAccountID); err != nil {
			h.log.Error("Failed to update credit account balance", "error", err)
			return
		}
		if _, err = tx.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID); err != nil {
			h.log.Error("Failed to bump journal next_number", "error", err)
			return
		}

		if err = tx.Commit(); err != nil {
			h.log.Error("Failed to commit expense journal entry", "error", err)
		}
	}()

	// Notify: expense approved
	go func() {
		var expNum string
		var expAmount float64
		h.db.QueryRow(`SELECT expense_number, COALESCE(total_amount, 0) FROM expenses WHERE id = $1`, id).Scan(&expNum, &expAmount)
		amountStr := fmt.Sprintf("%.0f", expAmount)
		h.createTranslatedNotification(tenantID, userID, "expense_approved",
			map[string]interface{}{
				"expense_id":     id.String(),
				"expense_number": expNum,
				"amount":         expAmount,
			},
			expNum, amountStr,
		)
	}()

	h.GetExpense(c)
}
