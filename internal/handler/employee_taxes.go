package handler

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// employee_taxes.go — CRUD + preview + aggregate report for configurable employee taxes.
// See migration 330 for schema, and payroll.go for how the applied rows interact with
// the payroll flow.

// ─────────────────────────────────────────────────────────────────────────────
// Shared helpers
// ─────────────────────────────────────────────────────────────────────────────

var validTaxBaseTypes = map[string]bool{
	"base_salary": true,
	"gross":       true,
	"taxable":     true,
}

var validTaxPayers = map[string]bool{
	"employee": true,
	"employer": true,
}

func normalizeEmployeeTaxBaseType(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "gross"
	}
	if !validTaxBaseTypes[v] {
		return "gross"
	}
	return v
}

func normalizeEmployeeTaxPayer(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "employee"
	}
	if !validTaxPayers[v] {
		return "employee"
	}
	return v
}

// selectEmployeeTaxCols is shared by list and read queries.
// Joins accounts twice for enrichment.
const selectEmployeeTaxCols = `
	et.id, et.tenant_id, et.organization_id,
	et.code, et.name, COALESCE(et.description, ''),
	et.rate, et.base_type, et.payer,
	et.account_id, et.expense_account_id,
	et.is_active, et.sort_order,
	et.created_by, et.created_at, et.updated_at,
	a1.code AS account_code_en, a1.name AS account_name_en,
	a2.code AS expense_account_code_en, a2.name AS expense_account_name_en
`

const fromEmployeeTaxJoins = `
	FROM employee_taxes et
	LEFT JOIN accounts a1 ON a1.id = et.account_id
	LEFT JOIN accounts a2 ON a2.id = et.expense_account_id
`

func scanEmployeeTax(row interface {
	Scan(dest ...interface{}) error
}) (*entity.EmployeeTax, error) {
	var et entity.EmployeeTax
	var orgID, accountID, expenseAcctID, createdBy sql.NullString
	var accountCode, accountName, expenseAccountCode, expenseAccountName sql.NullString

	if err := row.Scan(
		&et.ID, &et.TenantID, &orgID,
		&et.Code, &et.Name, &et.Description,
		&et.Rate, &et.BaseType, &et.Payer,
		&accountID, &expenseAcctID,
		&et.IsActive, &et.SortOrder,
		&createdBy, &et.CreatedAt, &et.UpdatedAt,
		&accountCode, &accountName,
		&expenseAccountCode, &expenseAccountName,
	); err != nil {
		return nil, err
	}

	if orgID.Valid {
		if u, err := uuid.Parse(orgID.String); err == nil {
			et.OrganizationID = &u
		}
	}
	if accountID.Valid {
		if u, err := uuid.Parse(accountID.String); err == nil {
			et.AccountID = &u
		}
	}
	if expenseAcctID.Valid {
		if u, err := uuid.Parse(expenseAcctID.String); err == nil {
			et.ExpenseAccountID = &u
		}
	}
	if createdBy.Valid {
		if u, err := uuid.Parse(createdBy.String); err == nil {
			et.CreatedBy = &u
		}
	}
	if accountCode.Valid {
		s := accountCode.String
		et.AccountCode = &s
	}
	if accountName.Valid {
		s := accountName.String
		et.AccountName = &s
	}
	if expenseAccountCode.Valid {
		s := expenseAccountCode.String
		et.ExpenseAccountCode = &s
	}
	if expenseAccountName.Valid {
		s := expenseAccountName.String
		et.ExpenseAccountName = &s
	}
	return &et, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// List
// ─────────────────────────────────────────────────────────────────────────────

// ListEmployeeTaxes GET /employee-taxes?only_active=true
func (h *Handler) ListEmployeeTaxes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	onlyActive := strings.ToLower(c.Query("only_active")) == "true"

	where := "et.tenant_id = $1 AND et.deleted_at IS NULL"
	args := []interface{}{tenantID}
	if onlyActive {
		where += " AND et.is_active = TRUE"
	}

	query := fmt.Sprintf(`
		SELECT %s %s
		WHERE %s
		ORDER BY et.sort_order ASC, et.code ASC
	`, selectEmployeeTaxCols, fromEmployeeTaxJoins, where)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list employee taxes", "error", err)
		response.InternalError(c, "Failed to list employee taxes")
		return
	}
	defer rows.Close()

	out := make([]*entity.EmployeeTax, 0)
	for rows.Next() {
		et, err := scanEmployeeTax(rows)
		if err != nil {
			h.log.Error("Failed to scan employee tax", "error", err)
			continue
		}
		out = append(out, et)
	}

	response.Success(c, out)
}

// ─────────────────────────────────────────────────────────────────────────────
// Create
// ─────────────────────────────────────────────────────────────────────────────

// CreateEmployeeTax POST /employee-taxes
func (h *Handler) CreateEmployeeTax(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateEmployeeTaxInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	if input.Code == "" || input.Name == "" {
		response.BadRequest(c, "code and name are required")
		return
	}
	if input.Rate < 0 || input.Rate > 100 {
		response.BadRequest(c, "rate must be between 0 and 100")
		return
	}

	baseType := normalizeEmployeeTaxBaseType(input.BaseType)
	payer := normalizeEmployeeTaxPayer(input.Payer)

	// Duplicate check (case-insensitive)
	var existingCount int
	if err := h.db.QueryRow(`
		SELECT COUNT(*) FROM employee_taxes
		WHERE tenant_id = $1 AND LOWER(code) = LOWER($2) AND deleted_at IS NULL
	`, tenantID, input.Code).Scan(&existingCount); err == nil && existingCount > 0 {
		response.Conflict(c, "A tax with this code already exists")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}
	sortOrder := 0
	if input.SortOrder != nil {
		sortOrder = *input.SortOrder
	}

	id := uuid.New()
	now := time.Now()

	_, err := h.db.Exec(`
		INSERT INTO employee_taxes (
			id, tenant_id, organization_id,
			code, name, description,
			rate, base_type, payer,
			account_id, expense_account_id,
			is_active, sort_order,
			created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3,
			$4, $5, $6,
			$7, $8, $9,
			$10, $11,
			$12, $13,
			$14, $15, $15
		)
	`,
		id, tenantID, orgIDPtr,
		input.Code, input.Name, input.Description,
		input.Rate, baseType, payer,
		input.AccountID, input.ExpenseAccountID,
		isActive, sortOrder,
		userID, now,
	)
	if err != nil {
		h.log.Error("Failed to create employee tax", "error", err)
		response.InternalError(c, "Failed to create employee tax")
		return
	}

	et, err := h.getEmployeeTaxByID(tenantID, id)
	if err != nil {
		h.log.Error("Failed to read back employee tax", "error", err)
		response.InternalError(c, "Failed to read back employee tax")
		return
	}
	response.Created(c, et)
}

// ─────────────────────────────────────────────────────────────────────────────
// Update
// ─────────────────────────────────────────────────────────────────────────────

// UpdateEmployeeTax PUT /employee-taxes/:id
func (h *Handler) UpdateEmployeeTax(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tax ID")
		return
	}

	var input entity.UpdateEmployeeTaxInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Load current row
	cur, err := h.getEmployeeTaxByID(tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Employee tax not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load employee tax")
		return
	}

	// Overlay
	if input.Code != nil {
		v := strings.TrimSpace(*input.Code)
		if v == "" {
			response.BadRequest(c, "code cannot be empty")
			return
		}
		// duplicate check
		var cnt int
		if err := h.db.QueryRow(`
			SELECT COUNT(*) FROM employee_taxes
			WHERE tenant_id = $1 AND LOWER(code) = LOWER($2) AND id != $3 AND deleted_at IS NULL
		`, tenantID, v, id).Scan(&cnt); err == nil && cnt > 0 {
			response.Conflict(c, "A tax with this code already exists")
			return
		}
		cur.Code = v
	}
	if input.Name != nil {
		v := strings.TrimSpace(*input.Name)
		if v == "" {
			response.BadRequest(c, "name cannot be empty")
			return
		}
		cur.Name = v
	}
	if input.Description != nil {
		cur.Description = *input.Description
	}
	if input.Rate != nil {
		if *input.Rate < 0 || *input.Rate > 100 {
			response.BadRequest(c, "rate must be between 0 and 100")
			return
		}
		cur.Rate = *input.Rate
	}
	if input.BaseType != nil {
		cur.BaseType = normalizeEmployeeTaxBaseType(*input.BaseType)
	}
	if input.Payer != nil {
		cur.Payer = normalizeEmployeeTaxPayer(*input.Payer)
	}
	if input.ClearAccount {
		cur.AccountID = nil
	} else if input.AccountID != nil {
		cur.AccountID = input.AccountID
	}
	if input.ClearExpenseAcct {
		cur.ExpenseAccountID = nil
	} else if input.ExpenseAccountID != nil {
		cur.ExpenseAccountID = input.ExpenseAccountID
	}
	if input.IsActive != nil {
		cur.IsActive = *input.IsActive
	}
	if input.SortOrder != nil {
		cur.SortOrder = *input.SortOrder
	}

	now := time.Now()
	if _, err := h.db.Exec(`
		UPDATE employee_taxes SET
			code = $1, name = $2, description = $3,
			rate = $4, base_type = $5, payer = $6,
			account_id = $7, expense_account_id = $8,
			is_active = $9, sort_order = $10,
			updated_at = $11
		WHERE id = $12 AND tenant_id = $13 AND deleted_at IS NULL
	`,
		cur.Code, cur.Name, cur.Description,
		cur.Rate, cur.BaseType, cur.Payer,
		cur.AccountID, cur.ExpenseAccountID,
		cur.IsActive, cur.SortOrder,
		now,
		id, tenantID,
	); err != nil {
		h.log.Error("Failed to update employee tax", "error", err)
		response.InternalError(c, "Failed to update employee tax")
		return
	}

	et, err := h.getEmployeeTaxByID(tenantID, id)
	if err != nil {
		response.InternalError(c, "Failed to read back employee tax")
		return
	}
	response.Success(c, et)
}

// ─────────────────────────────────────────────────────────────────────────────
// Delete (soft)
// ─────────────────────────────────────────────────────────────────────────────

// DeleteEmployeeTax DELETE /employee-taxes/:id
func (h *Handler) DeleteEmployeeTax(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid tax ID")
		return
	}

	now := time.Now()
	res, err := h.db.Exec(`
		UPDATE employee_taxes SET deleted_at = $1, is_active = FALSE, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, now, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete employee tax", "error", err)
		response.InternalError(c, "Failed to delete employee tax")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		response.NotFound(c, "Employee tax not found")
		return
	}
	response.Success(c, gin.H{"deleted": true, "id": id})
}

// ─────────────────────────────────────────────────────────────────────────────
// Preview — what would be applied for a given base/overtime/bonus/allowances?
// ─────────────────────────────────────────────────────────────────────────────

type previewPayrollTaxesInput struct {
	BaseSalary     float64     `json:"base_salary"`
	OvertimeAmount float64     `json:"overtime_amount"`
	Bonus          float64     `json:"bonus"`
	Allowances     float64     `json:"allowances"`
	ExcludedTaxIDs []uuid.UUID `json:"excluded_tax_ids,omitempty"`
}

// PreviewPayrollTaxes POST /employee-taxes/preview
// Returns the taxes that would be applied to a hypothetical payroll entry. Used by
// the create/edit payroll modal to show real-time totals before saving.
func (h *Handler) PreviewPayrollTaxes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input previewPayrollTaxesInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	applied, totals, err := h.computeEmployeeTaxesForEntry(
		tenantID, input.BaseSalary, input.OvertimeAmount, input.Bonus, input.Allowances,
		input.ExcludedTaxIDs,
	)
	if err != nil {
		h.log.Error("Failed to preview payroll taxes", "error", err)
		response.InternalError(c, "Failed to preview payroll taxes")
		return
	}

	response.Success(c, gin.H{
		"applied":        applied,
		"total_employee": totals.Employee,
		"total_employer": totals.Employer,
		"gross":          input.BaseSalary + input.OvertimeAmount + input.Bonus + input.Allowances,
		"net":            math.Round((input.BaseSalary + input.OvertimeAmount + input.Bonus + input.Allowances) - totals.Employee),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Tax-reports section — aggregated employee taxes by code, for a date range.
// ─────────────────────────────────────────────────────────────────────────────

// GetEmployeeTaxReport GET /tax-reports/employee-taxes?start_date=...&end_date=...
// Aggregated by tax code within the date range, using payroll_entries.created_at
// as the period anchor.
func (h *Handler) GetEmployeeTaxReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	hasDates := startDate != "" && endDate != ""

	where := "pet.tenant_id = $1"
	args := []interface{}{tenantID}
	argIdx := 2
	if hasDates {
		where += fmt.Sprintf(" AND pe.created_at >= $%d AND pe.created_at <= $%d", argIdx, argIdx+1)
		args = append(args, startDate, endDate+" 23:59:59")
		argIdx += 2
	}

	query := fmt.Sprintf(`
		SELECT
			pet.tax_code_snapshot,
			MAX(pet.tax_name_snapshot),
			pet.payer_snapshot,
			COUNT(*)::int,
			COALESCE(SUM(pet.base_amount), 0),
			COALESCE(SUM(pet.amount), 0)
		FROM payroll_entry_taxes pet
		JOIN payroll_entries pe ON pe.id = pet.payroll_entry_id
		WHERE %s
		GROUP BY pet.tax_code_snapshot, pet.payer_snapshot
		ORDER BY pet.tax_code_snapshot
	`, where)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to aggregate employee taxes", "error", err)
		response.InternalError(c, "Failed to aggregate employee taxes")
		return
	}
	defer rows.Close()

	var totalEmployee, totalEmployer float64
	out := make([]entity.EmployeeTaxSummaryRow, 0)
	for rows.Next() {
		var row entity.EmployeeTaxSummaryRow
		if err := rows.Scan(&row.TaxCode, &row.TaxName, &row.Payer, &row.EntryCount, &row.TotalBase, &row.TotalAmount); err != nil {
			continue
		}
		if row.Payer == "employer" {
			totalEmployer += row.TotalAmount
		} else {
			totalEmployee += row.TotalAmount
		}
		out = append(out, row)
	}

	// Subtract recorded payments to get the "pending" balance per tax.
	// A payment is recorded via POST /employee-taxes/payments and creates
	// a journal entry that debits the liability account; this query is
	// the report-side complement that shows users how much of each tax
	// is still owed for the same period window.
	paidByCode := map[string]float64{}
	{
		paidWhere := "tenant_id = $1 AND deleted_at IS NULL"
		paidArgs := []interface{}{tenantID}
		paidIdx := 2
		if hasDates {
			paidWhere += fmt.Sprintf(" AND period_start <= $%d AND period_end >= $%d",
				paidIdx, paidIdx+1)
			paidArgs = append(paidArgs, endDate, startDate)
		}
		paidRows, perr := h.db.Query(`
			SELECT tax_code, COALESCE(SUM(amount), 0)
			FROM employee_tax_payments
			WHERE `+paidWhere+`
			GROUP BY tax_code
		`, paidArgs...)
		if perr != nil {
			h.log.Error("Failed to aggregate employee_tax_payments", "error", perr)
		} else {
			defer paidRows.Close()
			for paidRows.Next() {
				var code string
				var amt float64
				if scanErr := paidRows.Scan(&code, &amt); scanErr == nil {
					paidByCode[code] = amt
				}
			}
		}
	}

	totalPaid := 0.0
	totalPending := 0.0
	enriched := make([]map[string]interface{}, 0, len(out))
	for _, r := range out {
		paid := paidByCode[r.TaxCode]
		pending := r.TotalAmount - paid
		if pending < 0 {
			pending = 0
		}
		totalPaid += paid
		totalPending += pending
		enriched = append(enriched, map[string]interface{}{
			"tax_code":     r.TaxCode,
			"tax_name":     r.TaxName,
			"payer":        r.Payer,
			"entry_count":  r.EntryCount,
			"total_base":   r.TotalBase,
			"total_amount": r.TotalAmount,
			"paid_amount":  paid,
			"pending":      pending,
		})
	}

	response.Success(c, gin.H{
		"rows":           enriched,
		"total_employee": totalEmployee,
		"total_employer": totalEmployer,
		"total":          totalEmployee + totalEmployer,
		"total_paid":     totalPaid,
		"total_pending":  totalPending,
		"period": gin.H{
			"start_date": startDate,
			"end_date":   endDate,
		},
	})
}

// ─────────────────────────────────────────────────────────────────────
// Tax payment recording — POST /employee-taxes/payments (migration 360)
//
// Closes a tax-liability balance for a (tax, period) pair. Side-effects:
//   1. Inserts an employee_tax_payments row with the amount + reference
//   2. Creates a journal entry: Dr (tax liability account) Cr (cash/bank)
//   3. Updates the running pending balance the report query subtracts
//      (no separate state needed — the report just sums payments live).
// ─────────────────────────────────────────────────────────────────────

type recordEmployeeTaxPaymentInput struct {
	TaxCode       string  `json:"tax_code" binding:"required"`
	PeriodStart   string  `json:"period_start" binding:"required"` // YYYY-MM-DD
	PeriodEnd     string  `json:"period_end" binding:"required"`
	Amount        float64 `json:"amount" binding:"required,gt=0"`
	PaymentMethod string  `json:"payment_method"`
	BankAccountID string  `json:"bank_account_id"` // optional: Cr account override
	Note          string  `json:"note"`
}

// RecordEmployeeTaxPayment POST /employee-taxes/payments
func (h *Handler) RecordEmployeeTaxPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var orgIDPtr *uuid.UUID
	if oid, oOk := middleware.GetOrganizationID(c); oOk && oid != uuid.Nil {
		v := oid
		orgIDPtr = &v
	}

	var in recordEmployeeTaxPaymentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	periodStart, err := time.Parse("2006-01-02", in.PeriodStart)
	if err != nil {
		response.BadRequest(c, "Invalid period_start")
		return
	}
	periodEnd, err := time.Parse("2006-01-02", in.PeriodEnd)
	if err != nil {
		response.BadRequest(c, "Invalid period_end")
		return
	}

	// Look up the tax catalog row to grab the canonical name + the GL
	// liability account to debit. If the operator deactivated / renamed
	// the tax we still have payroll_entry_taxes snapshots to fall back
	// on for the display name + account.
	var taxID sql.NullString
	var taxName string
	var payer string
	var liabilityAcctID sql.NullString
	err = h.db.QueryRow(`
		SELECT id::text, name, payer, COALESCE(account_id::text, '')
		FROM employee_taxes
		WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL
		ORDER BY (organization_id IS NULL) ASC
		LIMIT 1
	`, tenantID, in.TaxCode).Scan(&taxID, &taxName, &payer, &liabilityAcctID)
	if err == sql.ErrNoRows {
		// Fall back: derive from the most recent snapshot.
		err = h.db.QueryRow(`
			SELECT MAX(tax_name_snapshot), MAX(payer_snapshot),
			       COALESCE(MAX(account_id_snapshot::text), '')
			FROM payroll_entry_taxes
			WHERE tenant_id = $1 AND tax_code_snapshot = $2
		`, tenantID, in.TaxCode).Scan(&taxName, &payer, &liabilityAcctID)
	}
	if err != nil {
		h.log.Error("Failed to resolve tax for payment", "error", err, "code", in.TaxCode)
		response.BadRequest(c, "Unknown tax code")
		return
	}
	if !liabilityAcctID.Valid || liabilityAcctID.String == "" {
		response.BadRequest(c,
			"Tax has no liability GL account configured — open Settings → Employee taxes")
		return
	}
	liabilityUUID, _ := uuid.Parse(liabilityAcctID.String)

	// Pick the credit-side account (cash/bank). Caller can override
	// with bank_account_id; otherwise pick the first cash/bank account
	// for this tenant.
	var creditAcctID uuid.UUID
	if in.BankAccountID != "" {
		if id, perr := uuid.Parse(in.BankAccountID); perr == nil {
			creditAcctID = id
		}
	}
	if creditAcctID == uuid.Nil {
		// Pick the first bank/cash account in the chart. Uzbek 21-сон
		// БҲМС standard codes: 5110 = расчётный счёт (bank), 5010 =
		// касса (cash). Sort puts the bank account first when both
		// exist, falls back to anything flagged is_bank_account.
		var idStr string
		_ = h.db.QueryRow(`
			SELECT id::text FROM accounts
			WHERE tenant_id = $1
			  AND deleted_at IS NULL
			  AND (is_bank_account = true OR code IN ('5010', '5110'))
			ORDER BY (code = '5110') DESC,
			         (is_bank_account = true) DESC,
			         code ASC
			LIMIT 1
		`, tenantID).Scan(&idStr)
		if idStr != "" {
			creditAcctID, _ = uuid.Parse(idStr)
		}
	}
	if creditAcctID == uuid.Nil {
		response.BadRequest(c,
			"No cash / bank GL account found — create one or pass bank_account_id")
		return
	}

	// Resolve the destination journal — prefer the dedicated payroll
	// journal, fall back to the GENERAL / MISC journal if the tenant
	// hasn't configured a payroll-specific one yet. Same lookup pattern
	// payroll.go uses for the period-level entry.
	var journalID uuid.UUID
	var nextNumber int
	h.db.QueryRow(`
		SELECT id, COALESCE(next_number, 1) FROM journals
		WHERE tenant_id = $1 AND COALESCE(is_payroll_journal, false) = true
		  AND COALESCE(is_active, true) = true AND deleted_at IS NULL
		LIMIT 1
	`, tenantID).Scan(&journalID, &nextNumber)
	if journalID == uuid.Nil {
		h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1) FROM journals
			WHERE tenant_id = $1 AND code IN ('PAYROLL','MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE code WHEN 'PAYROLL' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END
			LIMIT 1
		`, tenantID).Scan(&journalID, &nextNumber)
	}
	if journalID == uuid.Nil {
		response.BadRequest(c,
			"No journal found — create a PAYROLL or GENERAL journal in Accounting → Journals")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Allocate journal entry id + number first so we can FK the payment row.
	// Entry numbers are unique per (tenant, org) — derive the suffix with
	// nextEntryNumberSeq instead of a timestamp.
	jeID := uuid.New()
	now := time.Now()
	entryNumber := fmt.Sprintf("PTX%06d", nextEntryNumberSeq(tx, tenantID, orgIDPtr, "PTX", nextNumber))
	desc := fmt.Sprintf("%s ushlash to'lovi (%s — %s)",
		taxName, in.PeriodStart, in.PeriodEnd)
	_, err = tx.Exec(`
		INSERT INTO journal_entries (
		    id, tenant_id, organization_id, journal_id, entry_number, entry_date,
		    description, source_type, source_id, exchange_rate,
		    total_debit, total_credit, status, created_by,
		    created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'employee_tax_payment', $1, 1.0,
		          $8, $8, 'posted', $9, $10, $10)
	`, jeID, tenantID, orgIDPtr, journalID, entryNumber, now, desc, in.Amount, userID, now)
	if err != nil {
		h.log.Error("Failed to insert tax-payment journal entry", "error", err)
		response.InternalError(c, "Failed to create journal entry")
		return
	}

	// Dr liability (clear the credit balance), Cr cash/bank. The columns
	// match the schema in migration 002 (debit_amount/credit_amount,
	// line_number — no tenant_id on line rows).
	_, err = tx.Exec(`
		INSERT INTO journal_entry_lines
		    (id, journal_entry_id, line_number, account_id, description,
		     debit_amount, credit_amount, exchange_rate, created_at)
		VALUES (gen_random_uuid(), $1, 1, $2, $3, $4, 0, 1.0, $5)
	`, jeID, liabilityUUID, desc, in.Amount, now)
	if err != nil {
		h.log.Error("Failed to insert debit line", "error", err)
		response.InternalError(c, "Failed to insert journal line")
		return
	}
	_, err = tx.Exec(`
		INSERT INTO journal_entry_lines
		    (id, journal_entry_id, line_number, account_id, description,
		     debit_amount, credit_amount, exchange_rate, created_at)
		VALUES (gen_random_uuid(), $1, 2, $2, $3, 0, $4, 1.0, $5)
	`, jeID, creditAcctID, desc, in.Amount, now)
	if err != nil {
		h.log.Error("Failed to insert credit line", "error", err)
		response.InternalError(c, "Failed to insert journal line")
		return
	}

	// Mirror the lines into account balances (debit +, credit −) and bump
	// the journal counter — all inside the same tx as the JE.
	_, err = tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`,
		in.Amount, now, liabilityUUID)
	if err != nil {
		h.log.Error("Failed to update tax liability balance", "error", err)
		response.InternalError(c, "Failed to update account balance")
		return
	}
	_, err = tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`,
		in.Amount, now, creditAcctID)
	if err != nil {
		h.log.Error("Failed to update cash/bank balance", "error", err)
		response.InternalError(c, "Failed to update account balance")
		return
	}
	_, err = tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID)
	if err != nil {
		h.log.Error("Failed to bump journal next_number for tax payment", "error", err)
		response.InternalError(c, "Failed to update journal")
		return
	}

	// Record the payment row.
	var taxIDPtr *uuid.UUID
	if taxID.Valid && taxID.String != "" {
		if v, perr := uuid.Parse(taxID.String); perr == nil {
			taxIDPtr = &v
		}
	}
	paymentID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO employee_tax_payments (
		    id, tenant_id, organization_id, tax_id,
		    tax_code, tax_name, payer,
		    period_start, period_end, amount,
		    journal_entry_id, paid_at, paid_by,
		    payment_method, note,
		    created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NULLIF($14, ''), NULLIF($15, ''), $16, $16)
	`, paymentID, tenantID, orgIDPtr, taxIDPtr,
		in.TaxCode, taxName, payer,
		periodStart, periodEnd, in.Amount,
		jeID, now, userID,
		in.PaymentMethod, in.Note,
		now)
	if err != nil {
		h.log.Error("Failed to insert tax payment", "error", err)
		response.InternalError(c, "Failed to record payment")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
	}

	response.Success(c, gin.H{
		"id":               paymentID,
		"tax_code":         in.TaxCode,
		"amount":           in.Amount,
		"journal_entry_id": jeID,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// getEmployeeTaxByID loads a single enriched row.
func (h *Handler) getEmployeeTaxByID(tenantID, id uuid.UUID) (*entity.EmployeeTax, error) {
	query := fmt.Sprintf(`
		SELECT %s %s
		WHERE et.id = $1 AND et.tenant_id = $2 AND et.deleted_at IS NULL
	`, selectEmployeeTaxCols, fromEmployeeTaxJoins)
	return scanEmployeeTax(h.db.QueryRow(query, id, tenantID))
}

// listActiveEmployeeTaxesForTenant returns active tax catalog in sort order.
func (h *Handler) listActiveEmployeeTaxesForTenant(tenantID uuid.UUID) ([]*entity.EmployeeTax, error) {
	query := fmt.Sprintf(`
		SELECT %s %s
		WHERE et.tenant_id = $1 AND et.deleted_at IS NULL AND et.is_active = TRUE
		ORDER BY et.sort_order ASC, et.code ASC
	`, selectEmployeeTaxCols, fromEmployeeTaxJoins)

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*entity.EmployeeTax, 0)
	for rows.Next() {
		et, err := scanEmployeeTax(rows)
		if err != nil {
			continue
		}
		out = append(out, et)
	}
	return out, nil
}

type payrollTaxTotals struct {
	Employee float64
	Employer float64
}

// computeEmployeeTaxesForEntry returns what would be applied to a payroll entry
// given its base_salary/overtime/bonus/allowances and any excluded tax IDs.
// The returned slice is intended to be written into payroll_entry_taxes.
// Rounding: amounts are rounded to the nearest so'm (2dp for amounts but rounded to integer per TT §4).
func (h *Handler) computeEmployeeTaxesForEntry(
	tenantID uuid.UUID,
	baseSalary, overtimeAmount, bonus, allowances float64,
	excludedIDs []uuid.UUID,
) ([]entity.PayrollEntryTax, payrollTaxTotals, error) {
	totals := payrollTaxTotals{}
	gross := baseSalary + overtimeAmount + bonus + allowances

	excludedSet := make(map[uuid.UUID]bool, len(excludedIDs))
	for _, id := range excludedIDs {
		excludedSet[id] = true
	}

	taxes, err := h.listActiveEmployeeTaxesForTenant(tenantID)
	if err != nil {
		return nil, totals, err
	}

	applied := make([]entity.PayrollEntryTax, 0, len(taxes))
	runningEmployee := 0.0

	for _, tax := range taxes {
		if excludedSet[tax.ID] {
			continue
		}

		var base float64
		switch tax.BaseType {
		case "base_salary":
			base = baseSalary
		case "taxable":
			base = gross - runningEmployee
			if base < 0 {
				base = 0
			}
		default: // "gross"
			base = gross
		}

		amount := math.Round(base * tax.Rate / 100)
		if amount < 0 {
			amount = 0
		}

		row := entity.PayrollEntryTax{
			TaxID:                    &tax.ID,
			TaxCodeSnapshot:          tax.Code,
			TaxNameSnapshot:          tax.Name,
			RateSnapshot:             tax.Rate,
			BaseTypeSnapshot:         tax.BaseType,
			PayerSnapshot:            tax.Payer,
			BaseAmount:               base,
			Amount:                   amount,
			AccountIDSnapshot:        tax.AccountID,
			ExpenseAccountIDSnapshot: tax.ExpenseAccountID,
		}
		applied = append(applied, row)

		if tax.Payer == "employer" {
			totals.Employer += amount
		} else {
			totals.Employee += amount
			runningEmployee += amount
		}
	}

	return applied, totals, nil
}

// writePayrollEntryTaxes replaces all tax rows for a given entry with the supplied list.
// Used by both CreatePayrollEntry and UpdatePayrollEntry after computing applicable taxes.
func (h *Handler) writePayrollEntryTaxes(
	tenantID uuid.UUID, orgID *uuid.UUID,
	entryID uuid.UUID, applied []entity.PayrollEntryTax,
) error {
	// Wipe existing rows for this entry (fresh snapshot on every save)
	if _, err := h.db.Exec(
		`DELETE FROM payroll_entry_taxes WHERE payroll_entry_id = $1 AND tenant_id = $2`,
		entryID, tenantID,
	); err != nil {
		return err
	}

	now := time.Now()
	for _, row := range applied {
		_, err := h.db.Exec(`
			INSERT INTO payroll_entry_taxes (
				id, tenant_id, organization_id, payroll_entry_id, tax_id,
				tax_code_snapshot, tax_name_snapshot,
				rate_snapshot, base_type_snapshot, payer_snapshot,
				base_amount, amount,
				account_id_snapshot, expense_account_id_snapshot,
				created_at
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7,
				$8, $9, $10,
				$11, $12,
				$13, $14,
				$15
			)
		`,
			uuid.New(), tenantID, orgID, entryID, row.TaxID,
			row.TaxCodeSnapshot, row.TaxNameSnapshot,
			row.RateSnapshot, row.BaseTypeSnapshot, row.PayerSnapshot,
			row.BaseAmount, row.Amount,
			row.AccountIDSnapshot, row.ExpenseAccountIDSnapshot,
			now,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// listPayrollEntryTaxes returns all applied-tax rows for one entry.
func (h *Handler) listPayrollEntryTaxes(tenantID, entryID uuid.UUID) ([]entity.PayrollEntryTax, error) {
	rows, err := h.db.Query(`
		SELECT
			id, tenant_id, organization_id, payroll_entry_id, tax_id,
			tax_code_snapshot, tax_name_snapshot,
			rate_snapshot, base_type_snapshot, payer_snapshot,
			base_amount, amount,
			account_id_snapshot, expense_account_id_snapshot,
			created_at
		FROM payroll_entry_taxes
		WHERE tenant_id = $1 AND payroll_entry_id = $2
		ORDER BY created_at
	`, tenantID, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]entity.PayrollEntryTax, 0)
	for rows.Next() {
		var row entity.PayrollEntryTax
		var orgID, taxID, acctID, expAcctID sql.NullString
		if err := rows.Scan(
			&row.ID, &row.TenantID, &orgID, &row.PayrollEntryID, &taxID,
			&row.TaxCodeSnapshot, &row.TaxNameSnapshot,
			&row.RateSnapshot, &row.BaseTypeSnapshot, &row.PayerSnapshot,
			&row.BaseAmount, &row.Amount,
			&acctID, &expAcctID,
			&row.CreatedAt,
		); err != nil {
			continue
		}
		if orgID.Valid {
			if u, err := uuid.Parse(orgID.String); err == nil {
				row.OrganizationID = &u
			}
		}
		if taxID.Valid {
			if u, err := uuid.Parse(taxID.String); err == nil {
				row.TaxID = &u
			}
		}
		if acctID.Valid {
			if u, err := uuid.Parse(acctID.String); err == nil {
				row.AccountIDSnapshot = &u
			}
		}
		if expAcctID.Valid {
			if u, err := uuid.Parse(expAcctID.String); err == nil {
				row.ExpenseAccountIDSnapshot = &u
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// GetPayrollEntryTaxes GET /payroll-periods/:id/entries/:eid/taxes
// Returns the list of applied tax rows for a given payroll entry.
func (h *Handler) GetPayrollEntryTaxes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	entryID, err := uuid.Parse(c.Param("eid"))
	if err != nil {
		response.BadRequest(c, "Invalid payroll entry ID")
		return
	}
	rows, err := h.listPayrollEntryTaxes(tenantID, entryID)
	if err != nil {
		h.log.Error("Failed to load payroll entry taxes", "error", err)
		response.InternalError(c, "Failed to load payroll entry taxes")
		return
	}
	response.Success(c, rows)
}
