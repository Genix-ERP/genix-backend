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

// ─────────────────────────────────────────────────────────────────────────
// Xarajatlar (Expenses) v2 — lifecycle with server-side transitions.
//
//	draft → submitted → approved → paid
//	              └───→ rejected (reason required) → submitted (resubmit)
//
// The GL posting happens at PAY time (PayExpense), not at approval — see
// docs/xarajatlar-audit.md §2.6 for why the old approve-time posting was
// wrong (cash credited before payment, AP never settled). Migration 444
// added the CHECK backstop, the lifecycle columns and the category seed.
// ─────────────────────────────────────────────────────────────────────────

// expenseEditableStatuses are the states in which PUT /expenses/:id may
// modify the record. approved/paid rows are immutable through the generic
// update path (recognition still flips via PATCH /recognize).
var expenseEditableStatuses = map[string]bool{
	"draft":     true,
	"submitted": true,
	"rejected":  true,
}

// defaultExpenseCategories mirrors the seed in migration 444 so tenants
// created after the migration get the same starter set (lazy-seeded on
// first ListExpenseCategories call — same pattern as
// seedDefaultCostCategories in construction_settings.go).
var defaultExpenseCategories = []struct {
	Code, Name, Description, Color, Icon string
	Position                             int
}{
	{"TRANSPORT", "Transport", "Yoqilg'i, taksi, yo'l xarajatlari", "#185FA5", "Car", 1},
	{"IJARA", "Ijara", "Ofis va ombor ijarasi", "#534AB7", "Building2", 2},
	{"KOMMUNAL", "Kommunal", "Elektr, suv, gaz, internet, aloqa", "#1D9E75", "Zap", 3},
	{"OFIS", "Ofis xarajatlari", "Kanselyariya, jihozlar, xo'jalik buyumlari", "#EF9F27", "Briefcase", 4},
	{"SAFAR", "Safar", "Xizmat safari, mehmonxona, chipta", "#0E9AA7", "Plane", 5},
	{"REKLAMA", "Reklama", "Marketing va reklama xarajatlari", "#D9534F", "Megaphone", 6},
	{"MATERIAL", "Materiallar", "Xom ashyo va materiallar", "#8A6D3B", "Package", 7},
	{"BOSHQA", "Boshqa", "Boshqa turdagi xarajatlar", "#888780", "MoreHorizontal", 8},
}

func (h *Handler) seedDefaultExpenseCategories(tenantID uuid.UUID) {
	var count int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM expense_categories WHERE tenant_id = $1`, tenantID).Scan(&count)
	if count > 0 {
		return
	}
	for _, d := range defaultExpenseCategories {
		_, err := h.db.Exec(`
			INSERT INTO expense_categories (tenant_id, code, name, description, is_active, color, icon, position, created_at, updated_at)
			VALUES ($1, $2, $3, $4, true, $5, $6, $7, NOW(), NOW())
			ON CONFLICT (tenant_id, code) DO NOTHING
		`, tenantID, d.Code, d.Name, d.Description, d.Color, d.Icon, d.Position)
		if err != nil {
			_ = err // best-effort
		}
	}
}

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

	// Tenants created before migration 444 got seeded by the migration;
	// tenants created after it get the starter set here.
	h.seedDefaultExpenseCategories(tenantID)

	query := `
		SELECT ec.id, ec.tenant_id, ec.code, ec.name, ec.description, ec.parent_id,
		       ec.account_id, ec.is_active, ec.created_at,
		       COALESCE(ec.color, ''), COALESCE(ec.icon, ''), COALESCE(ec.position, 0),
		       a.code, a.name,
		       COALESCE((SELECT COUNT(*) FROM expenses e
		                 WHERE e.category_id = ec.id AND e.tenant_id = ec.tenant_id
		                   AND e.deleted_at IS NULL), 0) AS usage_count
		FROM expense_categories ec
		LEFT JOIN accounts a ON a.id = ec.account_id
		WHERE ec.tenant_id = $1
		ORDER BY COALESCE(ec.position, 0), ec.name
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
			&cat.Color, &cat.Icon, &cat.Position,
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
	Color       *string `json:"color"`
	Icon        *string `json:"icon"`
	Position    *int    `json:"position"`
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
// tenant in the form `EXP-{year}-{seq:04d}` (e.g. EXP-2026-0001).
//
// The query uses regex extraction so legacy rows whose suffix is wider
// or narrower than 4 digits still parse cleanly; rows that don't match
// the pattern at all are ignored.
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

	color := ""
	if req.Color != nil {
		color = strings.TrimSpace(*req.Color)
	}
	icon := ""
	if req.Icon != nil {
		icon = strings.TrimSpace(*req.Icon)
	}
	position := 100
	if req.Position != nil {
		position = *req.Position
	}

	newID := uuid.New()
	_, err := h.db.Exec(`
		INSERT INTO expense_categories (id, tenant_id, code, name, description, account_id, is_active, color, icon, position, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, NOW(), NOW())
	`, newID, tenantID, code, name, description, accountID, isActive, color, icon, position)
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
		   SET name = $1, code = $2, description = $3, account_id = $4, is_active = $5,
		       color = COALESCE(NULLIF($6, ''), color),
		       icon = COALESCE(NULLIF($7, ''), icon),
		       position = COALESCE($8, position),
		       updated_at = NOW()
		 WHERE id = $9 AND tenant_id = $10
	`, name, code, description, accountID, isActive,
		strings.TrimSpace(valueOrEmpty(req.Color)), strings.TrimSpace(valueOrEmpty(req.Icon)), req.Position,
		id, tenantID)
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

func valueOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
		`SELECT COUNT(*) FROM expenses WHERE category_id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
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
		       COALESCE(ec.color, ''), COALESCE(ec.icon, ''), COALESCE(ec.position, 0),
		       a.code, a.name,
		       COALESCE((SELECT COUNT(*) FROM expenses e
		                 WHERE e.category_id = ec.id AND e.tenant_id = ec.tenant_id
		                   AND e.deleted_at IS NULL), 0) AS usage_count
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
		&cat.Color, &cat.Icon, &cat.Position,
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

// expenseSelectColumns is shared between ListExpenses and GetExpense so
// the scan code stays in one shape. Requires aliases: e = expenses,
// c = expense_categories.
const expenseSelectColumns = `
	e.id, e.tenant_id, e.expense_number, e.category_id, e.employee_id, e.employee_name,
	e.vendor_id, e.vendor_name, e.expense_date, e.description, e.amount, e.tax_amount,
	e.total_amount, e.currency, e.payment_method, e.reference, e.receipt_url, e.is_recognized,
	e.status, e.reimbursable, e.notes, e.created_at, e.updated_at,
	e.submitted_at, e.approved_at, e.rejected_at, e.rejection_reason, e.paid_at,
	e.payment_account_id, e.journal_entry_id, e.created_by,
	COALESCE(c.name, '') AS category_name, COALESCE(c.color, '') AS category_color, COALESCE(c.icon, '') AS category_icon
`

// scanExpenseRow scans one row produced by expenseSelectColumns.
func scanExpenseRow(scan func(dest ...interface{}) error) (*entity.Expense, error) {
	var expense entity.Expense
	var categoryID, employeeID, vendorID sql.NullString
	var employeeName, vendorName, paymentMethod, reference, receiptURL, notes sql.NullString
	var submittedAt, approvedAt, rejectedAt, paidAt sql.NullTime
	var rejectionReason sql.NullString
	var paymentAccountID, journalEntryID, createdBy sql.NullString

	if err := scan(
		&expense.ID, &expense.TenantID, &expense.ExpenseNumber, &categoryID,
		&employeeID, &employeeName, &vendorID, &vendorName, &expense.ExpenseDate,
		&expense.Description, &expense.Amount, &expense.TaxAmount, &expense.TotalAmount,
		&expense.Currency, &paymentMethod, &reference, &receiptURL, &expense.IsRecognized,
		&expense.Status, &expense.Reimbursable, &notes, &expense.CreatedAt, &expense.UpdatedAt,
		&submittedAt, &approvedAt, &rejectedAt, &rejectionReason, &paidAt,
		&paymentAccountID, &journalEntryID, &createdBy,
		&expense.CategoryName, &expense.CategoryColor, &expense.CategoryIcon,
	); err != nil {
		return nil, err
	}

	parseUUID := func(ns sql.NullString) *uuid.UUID {
		if !ns.Valid {
			return nil
		}
		if id, err := uuid.Parse(ns.String); err == nil {
			return &id
		}
		return nil
	}
	parseStr := func(ns sql.NullString) *string {
		if !ns.Valid {
			return nil
		}
		s := ns.String
		return &s
	}
	parseTime := func(nt sql.NullTime) *time.Time {
		if !nt.Valid {
			return nil
		}
		t := nt.Time
		return &t
	}

	expense.CategoryID = parseUUID(categoryID)
	expense.EmployeeID = parseUUID(employeeID)
	expense.VendorID = parseUUID(vendorID)
	expense.EmployeeName = parseStr(employeeName)
	expense.VendorName = parseStr(vendorName)
	expense.PaymentMethod = parseStr(paymentMethod)
	expense.Reference = parseStr(reference)
	expense.ReceiptURL = parseStr(receiptURL)
	expense.Notes = parseStr(notes)
	expense.SubmittedAt = parseTime(submittedAt)
	expense.ApprovedAt = parseTime(approvedAt)
	expense.RejectedAt = parseTime(rejectedAt)
	expense.RejectionReason = parseStr(rejectionReason)
	expense.PaidAt = parseTime(paidAt)
	expense.PaymentAccountID = parseUUID(paymentAccountID)
	expense.JournalEntryID = parseUUID(journalEntryID)
	expense.CreatedBy = parseUUID(createdBy)

	return &expense, nil
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
	if limit < 1 || limit > 500 {
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
	search := strings.TrimSpace(c.Query("q"))
	// Polymorphic-link filters: ?linked_module=construction_object&linked_id=42
	// let the Qurilish/Shartnomalar/CRM pages list the expenses attached to
	// one of their records.
	linkedModule := c.Query("linked_module")
	linkedID := c.Query("linked_id")

	where := ` WHERE e.tenant_id = $1 AND e.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		where += fmt.Sprintf(" AND e.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" && status != "all" {
		argCount++
		where += fmt.Sprintf(" AND e.status = $%d", argCount)
		args = append(args, status)
	}

	if categoryID != "" {
		if id, err := uuid.Parse(categoryID); err == nil {
			argCount++
			where += fmt.Sprintf(" AND e.category_id = $%d", argCount)
			args = append(args, id)
		}
	}

	if employeeID != "" {
		if id, err := uuid.Parse(employeeID); err == nil {
			argCount++
			where += fmt.Sprintf(" AND e.employee_id = $%d", argCount)
			args = append(args, id)
		}
	}

	if isRecognized == "true" || isRecognized == "false" {
		argCount++
		where += fmt.Sprintf(" AND e.is_recognized = $%d", argCount)
		args = append(args, isRecognized == "true")
	}

	if dateFrom != "" {
		argCount++
		where += fmt.Sprintf(" AND e.expense_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		argCount++
		where += fmt.Sprintf(" AND e.expense_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	if search != "" {
		argCount++
		where += fmt.Sprintf(` AND (e.expense_number ILIKE $%d OR e.description ILIKE $%d
			OR COALESCE(e.employee_name, '') ILIKE $%d OR COALESCE(e.vendor_name, '') ILIKE $%d)`,
			argCount, argCount, argCount, argCount)
		args = append(args, "%"+search+"%")
	}

	if linkedModule != "" && linkedID != "" {
		argCount++
		linkedModuleArg := argCount
		argCount++
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM expense_links el
			WHERE el.expense_id = e.id AND el.linked_module = $%d AND el.linked_id = $%d)`,
			linkedModuleArg, argCount)
		args = append(args, linkedModule, linkedID)
	}

	countQuery := `SELECT COUNT(*) FROM expenses e` + where
	baseQuery := `SELECT ` + expenseSelectColumns + `
		FROM expenses e
		LEFT JOIN expense_categories c ON e.category_id = c.id` + where +
		fmt.Sprintf(" ORDER BY e.expense_date DESC, e.created_at DESC LIMIT %d OFFSET %d", limit, offset)

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
		expense, err := scanExpenseRow(rows.Scan)
		if err != nil {
			h.log.Error("Failed to scan expense", "error", err)
			continue
		}
		expenses = append(expenses, expense.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, expenses, pagination)
}

// GetExpenseStats returns whole-tenant aggregates for the Xarajatlar page:
// per-status counts+amounts, the by-category breakdown and a monthly series.
// The old UI computed these client-side over the first 20 rows, which is
// why "To'lov kutilmoqda: 18" could disagree with everything else on screen.
//
//	GET /expenses/stats?date_from=&date_to=
//
// date_from/date_to bound the status and category aggregates; the monthly
// series is always the last 6 calendar months so the trend chart stays
// meaningful regardless of the active filter.
func (h *Handler) GetExpenseStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	where := ` WHERE e.tenant_id = $1 AND e.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		where += fmt.Sprintf(" AND e.organization_id = $%d", argCount)
		args = append(args, orgID)
	}
	orgArgs := make([]interface{}, len(args))
	copy(orgArgs, args)
	orgWhere := where

	if dateFrom != "" {
		argCount++
		where += fmt.Sprintf(" AND e.expense_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		argCount++
		where += fmt.Sprintf(" AND e.expense_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	type bucket struct {
		Count  int     `json:"count"`
		Amount float64 `json:"amount"`
	}

	byStatus := map[string]*bucket{}
	rows, err := h.db.Query(`
		SELECT e.status, COUNT(*), COALESCE(SUM(e.total_amount), 0)
		FROM expenses e`+where+` GROUP BY e.status`, args...)
	if err != nil {
		h.log.Error("Failed to aggregate expense stats", "error", err)
		response.InternalError(c, "Failed to aggregate expense stats")
		return
	}
	for rows.Next() {
		var st string
		var b bucket
		if err := rows.Scan(&st, &b.Count, &b.Amount); err == nil {
			byStatus[st] = &b
		}
	}
	rows.Close()

	get := func(st string) bucket {
		if b, ok := byStatus[st]; ok {
			return *b
		}
		return bucket{}
	}
	// "Jami xarajatlar" counts real spending: submitted + approved + paid.
	// Drafts aren't commitments yet; rejected/cancelled never happened.
	total := bucket{}
	for _, st := range []string{"submitted", "approved", "paid"} {
		b := get(st)
		total.Count += b.Count
		total.Amount += b.Amount
	}

	// By-category (same statuses as the total, so donut == tiles)
	type catRow struct {
		CategoryID string  `json:"category_id"`
		Name       string  `json:"name"`
		Color      string  `json:"color"`
		Count      int     `json:"count"`
		Amount     float64 `json:"amount"`
	}
	byCategory := make([]catRow, 0)
	catRows, err := h.db.Query(`
		SELECT COALESCE(c.id::text, ''), COALESCE(c.name, ''), COALESCE(c.color, ''),
		       COUNT(*), COALESCE(SUM(e.total_amount), 0) AS amt
		FROM expenses e
		LEFT JOIN expense_categories c ON e.category_id = c.id`+where+`
		  AND e.status IN ('submitted', 'approved', 'paid')
		GROUP BY c.id, c.name, c.color
		ORDER BY amt DESC`, args...)
	if err == nil {
		for catRows.Next() {
			var r catRow
			if err := catRows.Scan(&r.CategoryID, &r.Name, &r.Color, &r.Count, &r.Amount); err == nil {
				byCategory = append(byCategory, r)
			}
		}
		catRows.Close()
	}

	// Monthly series: fixed last-6-months window (ignores date filter).
	type monthRow struct {
		Month  string  `json:"month"`
		Amount float64 `json:"amount"`
	}
	byMonth := make([]monthRow, 0, 6)
	monthRows, err := h.db.Query(`
		SELECT to_char(date_trunc('month', e.expense_date), 'YYYY-MM') AS m,
		       COALESCE(SUM(e.total_amount), 0)
		FROM expenses e`+orgWhere+`
		  AND e.status IN ('submitted', 'approved', 'paid')
		  AND e.expense_date >= date_trunc('month', CURRENT_DATE) - INTERVAL '5 months'
		  AND e.expense_date < date_trunc('month', CURRENT_DATE) + INTERVAL '1 month'
		GROUP BY 1 ORDER BY 1`, orgArgs...)
	if err == nil {
		for monthRows.Next() {
			var r monthRow
			if err := monthRows.Scan(&r.Month, &r.Amount); err == nil {
				byMonth = append(byMonth, r)
			}
		}
		monthRows.Close()
	}

	response.Success(c, gin.H{
		"total":            total,
		"draft":            get("draft"),
		"pending_approval": get("submitted"),
		"pending_payment":  get("approved"),
		"paid":             get("paid"),
		"rejected":         get("rejected"),
		"by_category":      byCategory,
		"by_month":         byMonth,
	})
}

// CreateExpense creates a new expense.
// Server-side requirements (v2): category_id is mandatory (this is what
// keeps the category donut meaningful) and the expense must resolve to an
// employee — either an explicit employee_id, a unique employee_name match,
// or the employee record linked to the calling user.
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

	if input.Amount <= 0 {
		response.BadRequest(c, "Amount must be positive")
		return
	}

	status := input.Status
	if status == "" {
		status = "submitted"
	}
	if status != "draft" && status != "submitted" {
		response.BadRequest(c, "Status must be 'draft' or 'submitted'")
		return
	}

	expenseDate, err := time.Parse("2006-01-02", input.ExpenseDate)
	if err != nil {
		response.BadRequest(c, "Invalid date format")
		return
	}

	// Category is required and must belong to the tenant.
	if input.CategoryID == "" {
		response.BadRequest(c, "category_id is required")
		return
	}
	catID, err := uuid.Parse(input.CategoryID)
	if err != nil {
		response.BadRequest(c, "Invalid category_id")
		return
	}
	var categoryName string
	if err := h.db.QueryRow(
		`SELECT name FROM expense_categories WHERE id = $1 AND tenant_id = $2`,
		catID, tenantID,
	).Scan(&categoryName); err != nil {
		response.BadRequest(c, "Category not found in this tenant")
		return
	}

	// Resolve the employee: explicit id → unique name match → current user.
	employeeID, employeeName, empErr := h.resolveExpenseEmployee(tenantID, userID, input.EmployeeID, input.EmployeeName)
	if empErr != "" {
		response.BadRequest(c, empErr)
		return
	}

	var vendorID *uuid.UUID
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

	expenseNumber, err := h.nextExpenseNumber(tenantID, time.Now().Year())
	if err != nil {
		h.log.Error("Failed to allocate expense number", "error", err)
		response.InternalError(c, "Failed to allocate expense number")
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
	var submittedAt *time.Time
	if status == "submitted" {
		submittedAt = &now
	}

	var vendorName, paymentMethod, reference, receiptURL, notes *string
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

	query := `
		INSERT INTO expenses (
			id, tenant_id, organization_id, expense_number, category_id, category_name, employee_id, employee_name,
			vendor_id, vendor_name, expense_date, description, amount, tax_amount,
			total_amount, currency, payment_method, reference, receipt_url, status, submitted_at,
			reimbursable, is_recognized, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $26)
		RETURNING id
	`

	if err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, expenseNumber, catID, categoryName, employeeID, employeeName,
		vendorID, vendorName, expenseDate, input.Description, input.Amount, input.TaxAmount,
		totalAmount, currency, paymentMethod, reference, receiptURL, status, submittedAt,
		input.Reimbursable, isRecognized, notes, userID, now,
	).Scan(&id); err != nil {
		h.log.Error("Failed to create expense", "error", err)
		response.InternalError(c, "Failed to create expense")
		return
	}

	if status == "submitted" {
		h.EmitWorkflowEvent(tenantID, "expenses.submitted", map[string]interface{}{
			"record_id":      id.String(),
			"expense_number": expenseNumber,
			"employee_name":  employeeName,
			"category_name":  categoryName,
			"total_amount":   totalAmount,
		})
	}

	h.respondExpenseByID(c, tenantID, id, http201)
}

// http201/http200 pick the response wrapper for respondExpenseByID.
const (
	http200 = 200
	http201 = 201
)

// respondExpenseByID re-reads one expense through the same JOIN shape as
// ListExpenses (plus payment-account/JE decorations) so every mutation
// returns the full record.
func (h *Handler) respondExpenseByID(c *gin.Context, tenantID, id uuid.UUID, code int) {
	query := `SELECT ` + expenseSelectColumns + `
		FROM expenses e
		LEFT JOIN expense_categories c ON e.category_id = c.id
		WHERE e.id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL`

	expense, err := scanExpenseRow(h.db.QueryRow(query, id, tenantID).Scan)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Expense")
		return
	} else if err != nil {
		h.log.Error("Failed to get expense", "error", err)
		response.InternalError(c, "Failed to get expense")
		return
	}

	resp := expense.ToResponse()

	// Decorate with payment-account and JE names for the detail panel.
	if expense.PaymentAccountID != nil {
		var acctCode, acctName string
		if err := h.db.QueryRow(`SELECT code, name FROM accounts WHERE id = $1`, *expense.PaymentAccountID).Scan(&acctCode, &acctName); err == nil {
			resp.PaymentAccountName = strings.TrimSpace(acctCode + " " + acctName)
		}
	}
	if expense.JournalEntryID != nil {
		var entryNumber string
		if err := h.db.QueryRow(`SELECT entry_number FROM journal_entries WHERE id = $1`, *expense.JournalEntryID).Scan(&entryNumber); err == nil {
			resp.JournalEntryNumber = entryNumber
		}
	}

	if w, exists := c.Get("gl_warning"); exists {
		if warn, ok := w.(string); ok && warn != "" {
			if code == http201 {
				response.Created(c, struct {
					*entity.ExpenseResponse
					GLWarning string `json:"gl_warning"`
				}{resp, warn})
			} else {
				response.Success(c, struct {
					*entity.ExpenseResponse
					GLWarning string `json:"gl_warning"`
				}{resp, warn})
			}
			return
		}
	}

	if code == http201 {
		response.Created(c, resp)
		return
	}
	response.Success(c, resp)
}

// resolveExpenseEmployee resolves who the expense belongs to. Returns a
// human-readable error string ("" = ok). Preference order: explicit
// employee_id → unique employee_name match → the employee row linked to
// the calling user.
func (h *Handler) resolveExpenseEmployee(tenantID, userID uuid.UUID, employeeIDStr, employeeNameStr string) (*uuid.UUID, string, string) {
	if employeeIDStr != "" {
		id, err := uuid.Parse(employeeIDStr)
		if err != nil {
			return nil, "", "Invalid employee_id"
		}
		var first, last string
		if err := h.db.QueryRow(
			`SELECT first_name, last_name FROM employees WHERE id = $1 AND tenant_id = $2`,
			id, tenantID,
		).Scan(&first, &last); err != nil {
			return nil, "", "Employee not found in this tenant"
		}
		return &id, strings.TrimSpace(first + " " + last), ""
	}

	if name := strings.TrimSpace(employeeNameStr); name != "" {
		var id uuid.UUID
		var n int
		err := h.db.QueryRow(`
			SELECT MIN(id::text)::uuid, COUNT(*) FROM employees
			WHERE tenant_id = $1
			  AND (lower(btrim(first_name || ' ' || last_name)) = lower($2)
			    OR lower(btrim(last_name || ' ' || first_name)) = lower($2))
		`, tenantID, strings.ToLower(name)).Scan(&id, &n)
		if err == nil && n == 1 {
			return &id, name, ""
		}
		// Ambiguous or unknown name: keep the snapshot but no FK. Legacy
		// API callers (imports, integrations) still work; the UI always
		// sends employee_id.
		return nil, name, ""
	}

	if userID != uuid.Nil {
		var id uuid.UUID
		var first, last string
		if err := h.db.QueryRow(
			`SELECT id, first_name, last_name FROM employees WHERE user_id = $1 AND tenant_id = $2 LIMIT 1`,
			userID, tenantID,
		).Scan(&id, &first, &last); err == nil {
			return &id, strings.TrimSpace(first + " " + last), ""
		}
	}

	return nil, "", "employee_id is required (no employee record is linked to your user)"
}

// GetExpense returns a single expense
func (h *Handler) GetExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	if !h.expenseInOrgScope(c, tenantID, id) {
		response.NotFound(c, "Expense")
		return
	}

	h.respondExpenseByID(c, tenantID, id, http200)
}

// expenseInOrgScope enforces the X-Organization-ID header on single-record
// endpoints: when the caller is scoped to an organization, records of other
// organizations 404 (previously only ListExpenses filtered by org — see
// audit §2.7).
func (h *Handler) expenseInOrgScope(c *gin.Context, tenantID, id uuid.UUID) bool {
	orgID, orgOk := middleware.GetOrganizationID(c)
	if !orgOk || orgID == uuid.Nil {
		return true
	}
	var matches bool
	if err := h.db.QueryRow(`
		SELECT COALESCE(organization_id = $1, false) FROM expenses
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, orgID, id, tenantID).Scan(&matches); err != nil {
		return false
	}
	return matches
}

// UpdateExpense updates an existing expense. Status is deliberately NOT
// updatable here — transitions go through /submit, /approve, /reject,
// /pay so the server-side rules can't be bypassed. Only draft, submitted
// and rejected expenses are editable.
func (h *Handler) UpdateExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
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

	if !h.expenseInOrgScope(c, tenantID, id) {
		response.NotFound(c, "Expense")
		return
	}

	var currentStatus string
	if err := h.db.QueryRow(
		`SELECT status FROM expenses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		id, tenantID,
	).Scan(&currentStatus); err != nil {
		response.NotFound(c, "Expense")
		return
	}
	if !expenseEditableStatuses[currentStatus] {
		response.BadRequest(c, fmt.Sprintf("Expense in status '%s' cannot be edited", currentStatus))
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
			var categoryName string
			if err := h.db.QueryRow(
				`SELECT name FROM expense_categories WHERE id = $1 AND tenant_id = $2`,
				parsedID, tenantID,
			).Scan(&categoryName); err != nil {
				response.BadRequest(c, "Category not found in this tenant")
				return
			}
			addUpdate("category_id", parsedID)
			addUpdate("category_name", categoryName)
		}
	}
	if input.EmployeeID != nil && *input.EmployeeID != "" {
		if parsedID, err := uuid.Parse(*input.EmployeeID); err == nil {
			var first, last string
			if err := h.db.QueryRow(
				`SELECT first_name, last_name FROM employees WHERE id = $1 AND tenant_id = $2`,
				parsedID, tenantID,
			).Scan(&first, &last); err != nil {
				response.BadRequest(c, "Employee not found in this tenant")
				return
			}
			addUpdate("employee_id", parsedID)
			addUpdate("employee_name", strings.TrimSpace(first+" "+last))
		}
	} else if input.EmployeeName != nil {
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

	var newAmount, newTax *float64
	if input.Amount != nil {
		if *input.Amount <= 0 {
			response.BadRequest(c, "Amount must be positive")
			return
		}
		addUpdate("amount", *input.Amount)
		newAmount = input.Amount
	}
	if input.TaxAmount != nil {
		addUpdate("tax_amount", *input.TaxAmount)
		newTax = input.TaxAmount
	}
	if newAmount != nil || newTax != nil {
		// Recompute total from whichever halves changed.
		var curAmount, curTax float64
		_ = h.db.QueryRow(`SELECT amount, tax_amount FROM expenses WHERE id = $1`, id).Scan(&curAmount, &curTax)
		a, t := curAmount, curTax
		if newAmount != nil {
			a = *newAmount
		}
		if newTax != nil {
			t = *newTax
		}
		addUpdate("total_amount", a+t)
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

	h.respondExpenseByID(c, tenantID, id, http200)
}

// RecognizeExpense flips the is_recognized flag on a single expense.
// PATCH /expenses/:id/recognize  body: { "is_recognized": true|false }.
// This endpoint is intentionally narrow — finance/accounting staff review
// expenses one-by-one and toggle recognition as they verify documentation
// (see §7.2 of ТЗ_Ish_Haqi_Soliq_Tolik.docx). Using a dedicated route
// instead of the general UpdateExpense PUT means we can gate it with a
// distinct permission and keep the audit log focused. Recognition stays
// editable in every status (it's a tax classification, not a lifecycle
// step).
func (h *Handler) RecognizeExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
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

	if !h.expenseInOrgScope(c, tenantID, id) {
		response.NotFound(c, "Expense")
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

	h.respondExpenseByID(c, tenantID, id, http200)
}

// DeleteExpense soft-deletes an expense. Only draft, rejected and
// cancelled expenses can be deleted: submitted ones must be rejected
// first (so the decision is on record), and approved/paid ones are part
// of the financial trail.
func (h *Handler) DeleteExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	if !h.expenseInOrgScope(c, tenantID, id) {
		response.NotFound(c, "Expense")
		return
	}

	query := `
		UPDATE expenses SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
		  AND status IN ('draft', 'rejected', 'cancelled')
		RETURNING id
	`

	var returnedID uuid.UUID
	if err := h.db.QueryRow(query, time.Now(), id, tenantID).Scan(&returnedID); err == sql.ErrNoRows {
		// Distinguish "not found" from "wrong status" for a usable error.
		var status string
		if err := h.db.QueryRow(
			`SELECT status FROM expenses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
			id, tenantID,
		).Scan(&status); err == nil {
			response.BadRequest(c, fmt.Sprintf("Expense in status '%s' cannot be deleted", status))
			return
		}
		response.NotFound(c, "Expense")
		return
	} else if err != nil {
		h.log.Error("Failed to delete expense", "error", err)
		response.InternalError(c, "Failed to delete expense")
		return
	}

	response.NoContent(c)
}

// SubmitExpense moves a draft (or rejected — resubmission after fixing)
// expense into the approval queue.
// POST /expenses/:id/submit
func (h *Handler) SubmitExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	if !h.expenseInOrgScope(c, tenantID, id) {
		response.NotFound(c, "Expense")
		return
	}

	now := time.Now()
	var expenseNumber, employeeName, categoryName string
	var totalAmount float64
	err = h.db.QueryRow(`
		UPDATE expenses e SET status = 'submitted', submitted_at = $1, updated_at = $1,
		       rejected_by = NULL, rejected_at = NULL, rejection_reason = NULL
		WHERE e.id = $2 AND e.tenant_id = $3 AND e.deleted_at IS NULL
		  AND e.status IN ('draft', 'rejected')
		RETURNING e.expense_number, COALESCE(e.employee_name, ''), COALESCE(e.category_name, ''), e.total_amount
	`, now, id, tenantID).Scan(&expenseNumber, &employeeName, &categoryName, &totalAmount)
	if err == sql.ErrNoRows {
		h.respondExpenseTransitionConflict(c, tenantID, id, "draft/rejected")
		return
	} else if err != nil {
		h.log.Error("Failed to submit expense", "error", err)
		response.InternalError(c, "Failed to submit expense")
		return
	}

	h.EmitWorkflowEvent(tenantID, "expenses.submitted", map[string]interface{}{
		"record_id":      id.String(),
		"expense_number": expenseNumber,
		"employee_name":  employeeName,
		"category_name":  categoryName,
		"total_amount":   totalAmount,
	})

	h.respondExpenseByID(c, tenantID, id, http200)
}

// respondExpenseTransitionConflict reports why a transition UPDATE matched
// no rows: missing record vs wrong current status.
func (h *Handler) respondExpenseTransitionConflict(c *gin.Context, tenantID, id uuid.UUID, expected string) {
	var status string
	if err := h.db.QueryRow(
		`SELECT status FROM expenses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		id, tenantID,
	).Scan(&status); err == nil {
		response.BadRequest(c, fmt.Sprintf("Invalid transition: expense is '%s', expected %s", status, expected))
		return
	}
	response.NotFound(c, "Expense")
}

// ApproveExpense approves a submitted expense. v2: approval no longer
// posts a journal entry — the GL posting moved to PayExpense, where the
// money actually leaves. See docs/xarajatlar-audit.md §2.6.
// POST /expenses/:id/approve
func (h *Handler) ApproveExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	if !h.expenseInOrgScope(c, tenantID, id) {
		response.NotFound(c, "Expense")
		return
	}

	now := time.Now()
	var expenseNumber, employeeName, categoryName string
	var totalAmount float64
	var createdBy sql.NullString
	err = h.db.QueryRow(`
		UPDATE expenses SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $2
		WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL AND status = 'submitted'
		RETURNING expense_number, COALESCE(employee_name, ''), COALESCE(category_name, ''), total_amount, created_by::text
	`, userID, now, id, tenantID).Scan(&expenseNumber, &employeeName, &categoryName, &totalAmount, &createdBy)
	if err == sql.ErrNoRows {
		h.respondExpenseTransitionConflict(c, tenantID, id, "'submitted'")
		return
	} else if err != nil {
		h.log.Error("Failed to approve expense", "error", err)
		response.InternalError(c, "Failed to approve expense")
		return
	}

	// Notify the creator (not the approver — they know what they just did).
	h.notifyExpenseActor(tenantID, createdBy, userID, "expense_approved", id, expenseNumber, totalAmount)

	h.EmitWorkflowEvent(tenantID, "expenses.approved", map[string]interface{}{
		"record_id":      id.String(),
		"expense_number": expenseNumber,
		"employee_name":  employeeName,
		"category_name":  categoryName,
		"total_amount":   totalAmount,
	})

	h.respondExpenseByID(c, tenantID, id, http200)
}

// notifyExpenseActor sends a translated notification about an expense to
// its creator; falls back to the acting user when the creator is unknown.
func (h *Handler) notifyExpenseActor(tenantID uuid.UUID, createdBy sql.NullString, actor uuid.UUID, notifType string, id uuid.UUID, expenseNumber string, amount float64) {
	target := actor
	if createdBy.Valid {
		if creatorID, err := uuid.Parse(createdBy.String); err == nil && creatorID != uuid.Nil {
			target = creatorID
		}
	}
	if target == uuid.Nil {
		return
	}
	go h.createTranslatedNotification(tenantID, target, notifType,
		map[string]interface{}{
			"expense_id":     id.String(),
			"expense_number": expenseNumber,
			"amount":         amount,
		},
		expenseNumber, fmt.Sprintf("%.0f", amount),
	)
}

// RejectExpense rejects a submitted expense with a mandatory reason.
// POST /expenses/:id/reject  body: { "reason": "..." }
func (h *Handler) RejectExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	var input struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Reason) == "" {
		response.BadRequest(c, "Rejection reason is required")
		return
	}

	if !h.expenseInOrgScope(c, tenantID, id) {
		response.NotFound(c, "Expense")
		return
	}

	now := time.Now()
	var expenseNumber, employeeName string
	var totalAmount float64
	var createdBy sql.NullString
	err = h.db.QueryRow(`
		UPDATE expenses SET status = 'rejected', rejected_by = $1, rejected_at = $2,
		       rejection_reason = $3, updated_at = $2
		WHERE id = $4 AND tenant_id = $5 AND deleted_at IS NULL AND status = 'submitted'
		RETURNING expense_number, COALESCE(employee_name, ''), total_amount, created_by::text
	`, userID, now, strings.TrimSpace(input.Reason), id, tenantID).Scan(&expenseNumber, &employeeName, &totalAmount, &createdBy)
	if err == sql.ErrNoRows {
		h.respondExpenseTransitionConflict(c, tenantID, id, "'submitted'")
		return
	} else if err != nil {
		h.log.Error("Failed to reject expense", "error", err)
		response.InternalError(c, "Failed to reject expense")
		return
	}

	h.notifyExpenseActor(tenantID, createdBy, userID, "expense_rejected", id, expenseNumber, totalAmount)

	h.EmitWorkflowEvent(tenantID, "expenses.rejected", map[string]interface{}{
		"record_id":      id.String(),
		"expense_number": expenseNumber,
		"employee_name":  employeeName,
		"total_amount":   totalAmount,
		"reason":         strings.TrimSpace(input.Reason),
	})

	h.respondExpenseByID(c, tenantID, id, http200)
}

// PayExpense marks an approved expense as paid and posts the journal
// entry in the same transaction:
//
//	Dt  category's GL account (resolved to a leaf; fallback 9410)
//	Kt  the paying kassa/bank account (request; fallback 5010)
//
// POST /expenses/:id/pay
// body: { "payment_account_id": "...", "payment_method": "cash|bank|card", "paid_date": "YYYY-MM-DD" } — all optional.
//
// Unlike the old approve-time posting, a failure here fails the whole
// request: the expense stays 'approved' and nothing half-posts (the
// migration-416 deferred balance trigger plus the single tx guarantee
// header+lines land together or not at all).
func (h *Handler) PayExpense(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	var input struct {
		PaymentAccountID string `json:"payment_account_id"`
		PaymentMethod    string `json:"payment_method"`
		PaidDate         string `json:"paid_date"`
	}
	// Body is optional — an empty POST pays from the default kassa.
	_ = c.ShouldBindJSON(&input)

	if !h.expenseInOrgScope(c, tenantID, id) {
		response.NotFound(c, "Expense")
		return
	}

	var expenseNumber, description string
	var totalAmount float64
	var status string
	var categoryID sql.NullString
	var orgID sql.NullString
	var createdBy sql.NullString
	var employeeName, categoryName sql.NullString
	if err := h.db.QueryRow(`
		SELECT expense_number, description, total_amount, status, category_id::text,
		       organization_id::text, created_by::text, employee_name, category_name
		FROM expenses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&expenseNumber, &description, &totalAmount, &status,
		&categoryID, &orgID, &createdBy, &employeeName, &categoryName); err != nil {
		response.NotFound(c, "Expense")
		return
	}
	if status != "approved" {
		response.BadRequest(c, fmt.Sprintf("Invalid transition: expense is '%s', expected 'approved'", status))
		return
	}
	if totalAmount <= 0 {
		response.BadRequest(c, "Expense amount must be positive to pay")
		return
	}

	var orgIDPtr *uuid.UUID
	if orgID.Valid {
		if parsed, err := uuid.Parse(orgID.String); err == nil {
			orgIDPtr = &parsed
		}
	}

	paidDate := time.Now()
	if input.PaidDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.PaidDate); err == nil {
			paidDate = parsed
		} else {
			response.BadRequest(c, "Invalid paid_date format")
			return
		}
	}

	now := time.Now()

	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("Failed to begin expense payment tx", "error", txErr)
		response.InternalError(c, "Failed to pay expense")
		return
	}
	defer tx.Rollback()

	// Debit: the category's configured GL account, walked down to a leaf
	// (TT §4.2 forbids posting to group accounts); fallback operating
	// expenses 9410.
	var expenseAccountID uuid.UUID
	if categoryID.Valid {
		var catAccount sql.NullString
		_ = tx.QueryRow(`SELECT account_id::text FROM expense_categories WHERE id = $1 AND tenant_id = $2`,
			categoryID.String, tenantID).Scan(&catAccount)
		if catAccount.Valid {
			if aid, err := uuid.Parse(catAccount.String); err == nil {
				expenseAccountID = resolveLeafAccount(tx, aid)
			}
		}
	}
	if expenseAccountID == uuid.Nil {
		expenseAccountID = findAccount(tx, tenantID, orgIDPtr, "operating expense", "9410")
	}
	if expenseAccountID == uuid.Nil {
		expenseAccountID = findAccount(tx, tenantID, orgIDPtr, "miscellaneous expense", "9410")
	}

	// Credit: the paying account — explicit choice from the pay dialog,
	// else the tenant's kassa.
	var creditAccountID uuid.UUID
	if input.PaymentAccountID != "" {
		aid, err := uuid.Parse(input.PaymentAccountID)
		if err != nil {
			response.BadRequest(c, "Invalid payment_account_id")
			return
		}
		var belongs bool
		if err := tx.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM accounts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
			aid, tenantID,
		).Scan(&belongs); err != nil || !belongs {
			response.BadRequest(c, "Payment account not found in this tenant")
			return
		}
		creditAccountID = resolveLeafAccount(tx, aid)
	} else {
		creditAccountID = findAccount(tx, tenantID, orgIDPtr, "kassa", "5010")
		if creditAccountID == uuid.Nil {
			creditAccountID = findAccount(tx, tenantID, orgIDPtr, "cash", "5010")
		}
	}

	if expenseAccountID == uuid.Nil || creditAccountID == uuid.Nil {
		response.BadRequest(c, "Cannot resolve GL accounts for payment (expense 9410 or kassa/bank 5010)")
		return
	}

	// Balance guard for outflows — same rule as ConfirmPayment.
	var available float64
	if err := tx.QueryRow(`SELECT COALESCE(current_balance, 0) FROM accounts WHERE id = $1`, creditAccountID).Scan(&available); err == nil {
		if available < totalAmount {
			response.BadRequest(c, fmt.Sprintf("Hisobda mablag' yetarli emas: mavjud %.2f, kerak %.2f", available, totalAmount))
			return
		}
	}

	// Journal: MISC first, GENERAL fallback (same choice the old posting
	// used, so expense entries stay in one journal across the migration).
	var journalID uuid.UUID
	var nextNumber int
	if err := tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1)
		FROM journals WHERE tenant_id = $1 AND code IN ('MISC','GENERAL') AND deleted_at IS NULL
		ORDER BY CASE WHEN code='MISC' THEN 0 ELSE 1 END LIMIT 1`,
		tenantID).Scan(&journalID, &nextNumber); err != nil {
		response.BadRequest(c, "No MISC/GENERAL journal found for this tenant")
		return
	}

	journalEntryID := uuid.New()
	jeDescription := "Expense paid: " + expenseNumber + " - " + description
	entryNumber := fmt.Sprintf("EXP%06d", nextEntryNumberSeq(tx, tenantID, orgIDPtr, "EXP", nextNumber))

	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'expense', $9, 1.0, $10, $10, 'posted', $11, $12, $12)`,
		journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, paidDate, expenseNumber, jeDescription,
		id.String(), totalAmount, userID, now,
	); err != nil {
		h.log.Error("Failed to create expense payment JE", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}

	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (
			id, journal_entry_id, line_number, account_id, description,
			debit_amount, credit_amount, exchange_rate, created_at
		) VALUES ($1, $2, 1, $3, $4, $5, 0, 1.0, $6)`,
		uuid.New(), journalEntryID, expenseAccountID, "Expense", totalAmount, now,
	); err != nil {
		h.log.Error("Failed to insert expense debit line", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}

	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (
			id, journal_entry_id, line_number, account_id, description,
			debit_amount, credit_amount, exchange_rate, created_at
		) VALUES ($1, $2, 2, $3, $4, 0, $5, 1.0, $6)`,
		uuid.New(), journalEntryID, creditAccountID, "Cash/Bank payment", totalAmount, now,
	); err != nil {
		h.log.Error("Failed to insert expense credit line", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}

	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`,
		totalAmount, now, expenseAccountID); err != nil {
		h.log.Error("Failed to update expense account balance", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`,
		totalAmount, now, creditAccountID); err != nil {
		h.log.Error("Failed to update credit account balance", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}
	if _, err := tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID); err != nil {
		h.log.Error("Failed to bump journal next_number", "error", err)
		response.InternalError(c, "Failed to post journal entry")
		return
	}

	paymentMethod := strings.TrimSpace(input.PaymentMethod)
	res, err := tx.Exec(`
		UPDATE expenses SET status = 'paid', paid_at = $1, paid_by = $2,
		       payment_account_id = $3, journal_entry_id = $4,
		       payment_method = COALESCE(NULLIF($5, ''), payment_method),
		       updated_at = $6
		WHERE id = $7 AND tenant_id = $8 AND deleted_at IS NULL AND status = 'approved'
	`, paidDate, userID, creditAccountID, journalEntryID, paymentMethod, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to mark expense paid", "error", err)
		response.InternalError(c, "Failed to pay expense")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Raced with another payer — roll everything back.
		response.BadRequest(c, "Expense is no longer in 'approved' status")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit expense payment", "error", err)
		response.InternalError(c, "Failed to pay expense")
		return
	}

	h.notifyExpenseActor(tenantID, createdBy, userID, "expense_paid", id, expenseNumber, totalAmount)

	h.EmitWorkflowEvent(tenantID, "expenses.paid", map[string]interface{}{
		"record_id":      id.String(),
		"expense_number": expenseNumber,
		"employee_name":  employeeName.String,
		"category_name":  categoryName.String,
		"total_amount":   totalAmount,
	})

	h.respondExpenseByID(c, tenantID, id, http200)
}

// ─────────────────────────────────────────────────────────────────────────
// Polymorphic links: expense ↔ construction object / contract / CRM deal
// ─────────────────────────────────────────────────────────────────────────

var expenseLinkModules = map[string]bool{
	"construction_object": true,
	"contract":            true,
	"crm_deal":            true,
}

// ListExpenseLinks returns the links of one expense.
// GET /expenses/:id/links
func (h *Handler) ListExpenseLinks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT el.id, el.linked_module, el.linked_id, el.created_at
		FROM expense_links el
		JOIN expenses e ON e.id = el.expense_id
		WHERE el.expense_id = $1 AND el.tenant_id = $2 AND e.deleted_at IS NULL
		ORDER BY el.created_at
	`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to list expense links", "error", err)
		response.InternalError(c, "Failed to list expense links")
		return
	}
	defer rows.Close()

	type linkRow struct {
		ID           uuid.UUID `json:"id"`
		LinkedModule string    `json:"linked_module"`
		LinkedID     string    `json:"linked_id"`
		CreatedAt    time.Time `json:"created_at"`
	}
	links := make([]linkRow, 0)
	for rows.Next() {
		var l linkRow
		if err := rows.Scan(&l.ID, &l.LinkedModule, &l.LinkedID, &l.CreatedAt); err == nil {
			links = append(links, l)
		}
	}
	response.Success(c, links)
}

// CreateExpenseLink attaches an expense to a construction object, contract
// or CRM deal.
// POST /expenses/:id/links  body: { "linked_module": "...", "linked_id": "..." }
func (h *Handler) CreateExpenseLink(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}

	var input struct {
		LinkedModule string `json:"linked_module"`
		LinkedID     string `json:"linked_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.LinkedModule == "" || input.LinkedID == "" {
		response.BadRequest(c, "linked_module and linked_id are required")
		return
	}
	if !expenseLinkModules[input.LinkedModule] {
		response.BadRequest(c, "linked_module must be one of: construction_object, contract, crm_deal")
		return
	}

	var exists bool
	if err := h.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM expenses WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
		id, tenantID,
	).Scan(&exists); err != nil || !exists {
		response.NotFound(c, "Expense")
		return
	}

	linkID := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO expense_links (id, tenant_id, expense_id, linked_module, linked_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (expense_id, linked_module, linked_id) DO NOTHING
	`, linkID, tenantID, id, input.LinkedModule, input.LinkedID, userID); err != nil {
		h.log.Error("Failed to create expense link", "error", err)
		response.InternalError(c, "Failed to create expense link")
		return
	}

	response.Created(c, gin.H{
		"id":            linkID,
		"linked_module": input.LinkedModule,
		"linked_id":     input.LinkedID,
	})
}

// DeleteExpenseLink removes a link.
// DELETE /expenses/:id/links/:linkId
func (h *Handler) DeleteExpenseLink(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid expense ID")
		return
	}
	linkID, err := uuid.Parse(c.Param("linkId"))
	if err != nil {
		response.BadRequest(c, "Invalid link ID")
		return
	}

	res, err := h.db.Exec(
		`DELETE FROM expense_links WHERE id = $1 AND expense_id = $2 AND tenant_id = $3`,
		linkID, id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete expense link", "error", err)
		response.InternalError(c, "Failed to delete expense link")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Link")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
