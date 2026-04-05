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

// ListExpenseCategories returns all expense categories
func (h *Handler) ListExpenseCategories(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, code, name, description, parent_id, is_active, created_at
		FROM expense_categories
		WHERE tenant_id = $1
		ORDER BY name
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

		if err := rows.Scan(
			&cat.ID, &cat.TenantID, &cat.Code, &cat.Name, &description, &parentID, &cat.IsActive, &cat.CreatedAt,
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

		categories = append(categories, cat.ToResponse())
	}

	response.Success(c, categories)
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

	baseQuery := `
		SELECT e.id, e.tenant_id, e.expense_number, e.category_id, e.employee_id, e.employee_name,
			   e.vendor_id, e.vendor_name, e.expense_date, e.description, e.amount, e.tax_amount,
			   e.total_amount, e.currency, e.payment_method, e.reference, e.receipt_url,
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
			&expense.Currency, &paymentMethod, &reference, &receiptURL, &expense.Status,
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

	expenseNumber := fmt.Sprintf("EXP-%d-%d", time.Now().Year(), time.Now().UnixNano()%10000)

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

	query := `
		INSERT INTO expenses (
			id, tenant_id, organization_id, expense_number, category_id, category_name, employee_id, employee_name,
			vendor_id, vendor_name, expense_date, description, amount, tax_amount,
			total_amount, currency, payment_method, reference, receipt_url, status,
			reimbursable, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)
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
		input.Reimbursable, notes, userID, now, now,
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
			   e.total_amount, e.currency, e.payment_method, e.reference, e.receipt_url,
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
		&expense.Currency, &paymentMethod, &reference, &receiptURL, &expense.Status,
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
		expenseAccountID := findAccount(h.db, tenantID, orgIDPtr, "operating expense", "6900")
		if expenseAccountID == uuid.Nil {
			expenseAccountID = findAccount(h.db, tenantID, orgIDPtr, "miscellaneous expense", "6900")
		}

		// Credit account: AP if reimbursable, Cash otherwise
		var creditAccountID uuid.UUID
		if reimbursable {
			creditAccountID = findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "2000")
		} else {
			creditAccountID = findAccount(h.db, tenantID, orgIDPtr, "cash", "1000")
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

		entryNumber := fmt.Sprintf("EXP%06d", nextNumber)
		journalEntryID := uuid.New()
		jeDescription := "Xarajat: " + expenseNumber + " - " + description

		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'expense', $9, 1.0, $10, $10, 'posted', $11, $12, $12)`,
			journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, expenseDate, expenseNumber, jeDescription,
			id.String(), totalAmount, userID, now,
		)
		if err != nil {
			h.log.Error("Failed to create expense journal entry", "error", err)
			return
		}

		// Debit: Expense account
		h.db.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 1, $3, $4, $5, 0, 1.0, $6)`,
			uuid.New(), journalEntryID, expenseAccountID, "Expense", totalAmount, now,
		)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalAmount, now, expenseAccountID)

		// Credit: Cash or AP
		h.db.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 2, $3, $4, 0, $5, 1.0, $6)`,
			uuid.New(), journalEntryID, creditAccountID, "Cash/Payable", totalAmount, now,
		)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalAmount, now, creditAccountID)

		h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
	}()

	h.GetExpense(c)
}
