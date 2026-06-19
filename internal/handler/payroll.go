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

// ListPayrollPeriods returns a paginated list of payroll periods
func (h *Handler) ListPayrollPeriods(c *gin.Context) {
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

	baseQuery := `
		SELECT pp.id, pp.tenant_id, pp.period_code, pp.period_name, pp.start_date, pp.end_date, pp.pay_date,
			   pp.status,
			   COALESCE((SELECT SUM(pe.gross_salary) FROM payroll_entries pe WHERE pe.payroll_period_id = pp.id AND pe.deleted_at IS NULL), pp.total_gross) as total_gross,
			   COALESCE((SELECT SUM(pe.total_deductions) FROM payroll_entries pe WHERE pe.payroll_period_id = pp.id AND pe.deleted_at IS NULL), pp.total_deductions) as total_deductions,
			   COALESCE((SELECT SUM(pe.net_salary) FROM payroll_entries pe WHERE pe.payroll_period_id = pp.id AND pe.deleted_at IS NULL), pp.total_net) as total_net,
			   COALESCE((SELECT COUNT(*) FROM payroll_entries pe WHERE pe.payroll_period_id = pp.id AND pe.deleted_at IS NULL), pp.employee_count) as employee_count,
			   pp.notes, pp.created_at,
			   (SELECT pe.employee_name FROM payroll_entries pe WHERE pe.payroll_period_id = pp.id AND pe.deleted_at IS NULL LIMIT 1) as first_employee_name,
			   (SELECT pe.employee_id FROM payroll_entries pe WHERE pe.payroll_period_id = pp.id AND pe.deleted_at IS NULL LIMIT 1) as first_employee_id
		FROM payroll_periods pp
		WHERE pp.tenant_id = $1 AND pp.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM payroll_periods WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND pp.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" && status != "all" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pp.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	baseQuery += " ORDER BY pp.start_date DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count payroll periods", "error", err)
		response.InternalError(c, "Failed to count payroll periods")
		return
	}

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list payroll periods", "error", err)
		response.InternalError(c, "Failed to list payroll periods")
		return
	}
	defer rows.Close()

	periods := make([]*entity.PayrollPeriodResponse, 0)
	for rows.Next() {
		var period entity.PayrollPeriod
		var notes, firstEmployeeName, firstEmployeeID sql.NullString

		if err := rows.Scan(
			&period.ID, &period.TenantID, &period.PeriodCode, &period.PeriodName,
			&period.StartDate, &period.EndDate, &period.PayDate, &period.Status,
			&period.TotalGross, &period.TotalDeductions, &period.TotalNet,
			&period.EmployeeCount, &notes, &period.CreatedAt, &firstEmployeeName,
			&firstEmployeeID,
		); err != nil {
			h.log.Error("Failed to scan payroll period", "error", err)
			continue
		}

		if notes.Valid {
			period.Notes = &notes.String
		}

		resp := period.ToResponse()
		// If there's only one employee, include their name and ID
		if period.EmployeeCount == 1 {
			if firstEmployeeName.Valid {
				resp.EmployeeName = firstEmployeeName.String
			}
			if firstEmployeeID.Valid {
				empUUID, err := uuid.Parse(firstEmployeeID.String)
				if err == nil {
					resp.EmployeeID = &empUUID
				}
			}
		}
		periods = append(periods, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, periods, pagination)
}

// CreatePayrollPeriod creates a new payroll period
func (h *Handler) CreatePayrollPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreatePayrollPeriodInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	periodCode := input.PeriodCode
	if periodCode == "" {
		var seq int
		h.db.QueryRow(`SELECT COUNT(*) + 1 FROM payroll_periods WHERE tenant_id = $1`, tenantID).Scan(&seq)
		periodCode = fmt.Sprintf("PAY-%d-%05d", time.Now().Year(), seq)
	}

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

	payDate, err := time.Parse("2006-01-02", input.PayDate)
	if err != nil {
		response.BadRequest(c, "Invalid pay_date format")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO payroll_periods (
			id, tenant_id, organization_id, period_code, period_name, start_date, end_date, pay_date,
			status, total_gross, total_deductions, total_net, employee_count, notes,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id
	`

	var notes *string
	if input.Notes != "" {
		notes = &input.Notes
	}

	if err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, periodCode, input.PeriodName, startDate, endDate, payDate,
		"draft", 0, 0, 0, 0, notes, userID, now, now,
	).Scan(&id); err != nil {
		h.log.Error("Failed to create payroll period", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Payroll period with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create payroll period")
		return
	}

	period := &entity.PayrollPeriod{
		ID:         id,
		TenantID:   tenantID,
		PeriodCode: periodCode,
		PeriodName: input.PeriodName,
		StartDate:  startDate,
		EndDate:    endDate,
		PayDate:    payDate,
		Status:     "draft",
		Notes:      notes,
		CreatedAt:  now,
	}

	response.Created(c, period.ToResponse())
}

// GetPayrollPeriod returns a single payroll period
func (h *Handler) GetPayrollPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid payroll period ID")
		return
	}

	query := `
		SELECT id, tenant_id, period_code, period_name, start_date, end_date, pay_date,
			   status, total_gross, total_deductions, total_net, employee_count, notes, created_at
		FROM payroll_periods
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var period entity.PayrollPeriod
	var notes sql.NullString

	if err := h.db.QueryRow(query, id, tenantID).Scan(
		&period.ID, &period.TenantID, &period.PeriodCode, &period.PeriodName,
		&period.StartDate, &period.EndDate, &period.PayDate, &period.Status,
		&period.TotalGross, &period.TotalDeductions, &period.TotalNet,
		&period.EmployeeCount, &notes, &period.CreatedAt,
	); err == sql.ErrNoRows {
		response.NotFound(c, "Payroll period")
		return
	} else if err != nil {
		h.log.Error("Failed to get payroll period", "error", err)
		response.InternalError(c, "Failed to get payroll period")
		return
	}

	if notes.Valid {
		period.Notes = &notes.String
	}

	response.Success(c, period.ToResponse())
}

// UpdatePayrollPeriod updates a payroll period
func (h *Handler) UpdatePayrollPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid payroll period ID")
		return
	}

	var input entity.UpdatePayrollPeriodInput
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

	if input.PeriodName != nil {
		addUpdate("period_name", *input.PeriodName)
	}
	if input.StartDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.StartDate); err == nil {
			addUpdate("start_date", parsed)
		}
	}
	if input.EndDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.EndDate); err == nil {
			addUpdate("end_date", parsed)
		}
	}
	if input.PayDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.PayDate); err == nil {
			addUpdate("pay_date", parsed)
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
		UPDATE payroll_periods SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	if err := h.db.QueryRow(query, args...).Scan(&returnedID); err == sql.ErrNoRows {
		response.NotFound(c, "Payroll period")
		return
	} else if err != nil {
		h.log.Error("Failed to update payroll period", "error", err)
		response.InternalError(c, "Failed to update payroll period")
		return
	}

	// If marking as paid, create payment journal entry (Dt: Wages Payable / Kt: Cash or Bank)
	if input.Status != nil && *input.Status == "paid" {
		userID, _ := middleware.GetUserID(c)
		orgID, _ := middleware.GetOrganizationID(c)
		now := time.Now()

		var periodName string
		var orgIDStr sql.NullString
		var totalNet float64
		h.db.QueryRow(`SELECT period_name, organization_id, COALESCE(total_net, 0) FROM payroll_periods WHERE id = $1`, id).Scan(&periodName, &orgIDStr, &totalNet)
		if orgIDStr.Valid {
			if parsed, err2 := uuid.Parse(orgIDStr.String); err2 == nil {
				orgID = parsed
			}
		}

		var orgIDPtr *uuid.UUID
		if orgID != uuid.Nil {
			orgIDPtr = &orgID
		}

		if totalNet > 0 {
			// Determine payment account based on payment method
			paymentMethod := "cash"
			if input.PaymentMethod != nil {
				paymentMethod = *input.PaymentMethod
			}

			var paymentAcct uuid.UUID
			var paymentAcctDesc string
			if paymentMethod == "card" || paymentMethod == "bank_transfer" {
				paymentAcct = findAccount(h.db, tenantID, orgIDPtr, "bank account", "5110")
				if paymentAcct == uuid.Nil {
					paymentAcct = findAccount(h.db, tenantID, orgIDPtr, "bank account", "5110")
				}
				paymentAcctDesc = "Bank Account"
			} else {
				paymentAcct = findAccount(h.db, tenantID, orgIDPtr, "cash", "5010")
				if paymentAcct == uuid.Nil {
					paymentAcct = findAccount(h.db, tenantID, orgIDPtr, "kassa", "5010")
				}
				paymentAcctDesc = "Cash"
			}

			// Wages payable account (liability cleared)
			wagesPayableAcct := findAccount(h.db, tenantID, orgIDPtr, "wages payable", "6710")
			if wagesPayableAcct == uuid.Nil {
				wagesPayableAcct = findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "6010")
			}

			if paymentAcct != uuid.Nil && wagesPayableAcct != uuid.Nil {
				var journalID uuid.UUID
				var nextNumber int
				// Try journal marked as payroll journal first
				h.db.QueryRow(`
					SELECT id, COALESCE(next_number, 1) FROM journals
					WHERE tenant_id = $1 AND COALESCE(is_payroll_journal, false) = true
					  AND COALESCE(is_active, true) = true AND deleted_at IS NULL
					LIMIT 1`,
					tenantID).Scan(&journalID, &nextNumber)
				// Fallback to legacy PAYROLL/MISC/GENERAL code lookup
				if journalID == uuid.Nil {
					h.db.QueryRow(`
						SELECT id, COALESCE(next_number, 1) FROM journals
						WHERE tenant_id = $1 AND code IN ('PAYROLL','MISC','GENERAL') AND deleted_at IS NULL
						ORDER BY CASE code WHEN 'PAYROLL' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
						tenantID).Scan(&journalID, &nextNumber)
				}

				if journalID != uuid.Nil {
					jeID := uuid.New()
					entryNumber := fmt.Sprintf("PAYPMT%05d", nextNumber)
					description := fmt.Sprintf("Salary Payment: %s (%s)", periodName, paymentMethod)

					h.db.Exec(`
						INSERT INTO journal_entries (
							id, tenant_id, organization_id, journal_id, entry_number, entry_date,
							description, source_type, source_id, exchange_rate,
							total_debit, total_credit, status, created_by, created_at, updated_at
						) VALUES ($1,$2,$3,$4,$5,$6,$7,'payroll_payment',$8,1.0,$9,$9,'posted',$10,$11,$11)`,
						jeID, tenantID, orgIDPtr, journalID, entryNumber, now,
						description, id.String(), totalNet, userID, now)

					// Dt: Wages Payable (liability cleared)
					h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
						VALUES ($1,$2,$3,'Wages Payable',$4,0,1,$5)`,
						uuid.New(), jeID, wagesPayableAcct, totalNet, now)
					h.db.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalNet, now, wagesPayableAcct)

					// Kt: Cash or Bank (money goes out)
					h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
						VALUES ($1,$2,$3,$4,0,$5,2,$6)`,
						uuid.New(), jeID, paymentAcct, paymentAcctDesc, totalNet, now)
					h.db.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalNet, now, paymentAcct)

					h.db.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID)
				}
			}
		}
	}

	h.GetPayrollPeriod(c)
}

// DeletePayrollPeriod soft-deletes a payroll period
func (h *Handler) DeletePayrollPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid payroll period ID")
		return
	}

	query := `
		UPDATE payroll_periods SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete payroll period", "error", err)
		response.InternalError(c, "Failed to delete payroll period")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Payroll period")
		return
	}

	response.NoContent(c)
}

// ListPayrollEntries returns payroll entries for a period
func (h *Handler) ListPayrollEntries(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodID := c.Param("id")
	id, err := uuid.Parse(periodID)
	if err != nil {
		response.BadRequest(c, "Invalid payroll period ID")
		return
	}

	query := `
		SELECT id, tenant_id, payroll_period_id, employee_id, employee_name,
		       COALESCE(position_snapshot, ''),
		       base_salary,
			   overtime_hours, overtime_amount, bonus, allowances, gross_salary, income_tax,
			   social_security, pension, other_deductions, total_deductions, net_salary,
			   COALESCE(advance_amount, 0), COALESCE(remainder_amount, 0),
			   COALESCE(advance_paid, false), advance_paid_day,
			   COALESCE(remainder_paid, false), remainder_paid_day,
			   COALESCE(advance_percent_used, 40),
			   payment_method, bank_account, status, notes, created_at
		FROM payroll_entries
		WHERE payroll_period_id = $1 AND tenant_id = $2
		ORDER BY employee_name
	`

	args := []interface{}{id, tenantID}
	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list payroll entries", "error", err)
		response.InternalError(c, "Failed to list payroll entries")
		return
	}
	defer rows.Close()

	entries := make([]*entity.PayrollEntryResponse, 0)
	for rows.Next() {
		var entry entity.PayrollEntry
		var bankAccount, notes sql.NullString
		var advanceDay, remainderDay sql.NullInt64

		if err := rows.Scan(
			&entry.ID, &entry.TenantID, &entry.PayrollPeriodID, &entry.EmployeeID,
			&entry.EmployeeName, &entry.PositionSnapshot,
			&entry.BaseSalary, &entry.OvertimeHours, &entry.OvertimeAmount,
			&entry.Bonus, &entry.Allowances, &entry.GrossSalary, &entry.IncomeTax,
			&entry.SocialSecurity, &entry.Pension, &entry.OtherDeductions, &entry.TotalDeductions,
			&entry.NetSalary,
			&entry.AdvanceAmount, &entry.RemainderAmount,
			&entry.AdvancePaid, &advanceDay,
			&entry.RemainderPaid, &remainderDay,
			&entry.AdvancePercentUsed,
			&entry.PaymentMethod, &bankAccount, &entry.Status, &notes, &entry.CreatedAt,
		); err != nil {
			h.log.Error("Failed to scan payroll entry", "error", err)
			continue
		}

		if bankAccount.Valid {
			entry.BankAccount = &bankAccount.String
		}
		if notes.Valid {
			entry.Notes = &notes.String
		}
		if advanceDay.Valid {
			d := int(advanceDay.Int64)
			entry.AdvancePaidDay = &d
		}
		if remainderDay.Valid {
			d := int(remainderDay.Int64)
			entry.RemainderPaidDay = &d
		}

		entries = append(entries, entry.ToResponse())
	}

	if !paginate {
		response.Success(c, entries)
		return
	}

	var total int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM payroll_entries WHERE payroll_period_id = $1 AND tenant_id = $2`, id, tenantID).Scan(&total)
	response.Paginated(c, entries, page, pageSize, total)
}

// CreatePayrollEntry creates a payroll entry
func (h *Handler) CreatePayrollEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodID := c.Param("id")
	payrollPeriodID, err := uuid.Parse(periodID)
	if err != nil {
		response.BadRequest(c, "Invalid payroll period ID")
		return
	}

	var input entity.CreatePayrollEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Get employee name + job_title (TT §2.1 snapshot for immutability)
	var employeeName, positionSnapshot string
	if err := h.db.QueryRow(
		`SELECT CONCAT(first_name, ' ', last_name), COALESCE(job_title, '')
		 FROM employees WHERE id = $1 AND tenant_id = $2`,
		employeeID, tenantID,
	).Scan(&employeeName, &positionSnapshot); err != nil {
		response.BadRequest(c, "Employee not found")
		return
	}

	// Calculate gross salary
	grossSalary := input.BaseSalary + input.OvertimeAmount + input.Bonus + input.Allowances

	// Compute configurable employee taxes (migration 330). When the tenant has active
	// employee_taxes configured, those drive income_tax / social_security / pension;
	// the legacy per-field values from input are ignored. If no active taxes exist,
	// we fall back to the legacy fields for backward compatibility.
	appliedTaxes, taxTotals, tErr := h.computeEmployeeTaxesForEntry(
		tenantID, input.BaseSalary, input.OvertimeAmount, input.Bonus, input.Allowances,
		input.ExcludedTaxIDs,
	)
	if tErr != nil {
		h.log.Error("Failed to compute employee taxes for payroll entry", "error", tErr)
	}

	var effectiveIncomeTax, effectiveSocialSecurity, effectivePension float64
	if len(appliedTaxes) > 0 {
		// Bucket by code so legacy columns still show sensible values for reports
		// that haven't yet been migrated to read from payroll_entry_taxes.
		effectiveIncomeTax, effectiveSocialSecurity, effectivePension = legacyBucketsFromTaxes(appliedTaxes)
	} else {
		effectiveIncomeTax = input.IncomeTax
		effectiveSocialSecurity = input.SocialSecurity
		effectivePension = input.Pension
	}

	totalEmployeeTax := taxTotals.Employee
	if len(appliedTaxes) == 0 {
		totalEmployeeTax = effectiveIncomeTax + effectiveSocialSecurity + effectivePension
	}
	totalDeductions := totalEmployeeTax + input.OtherDeductions
	netSalary := grossSalary - totalDeductions

	// TT §2.3.2: advance = base_salary × advance_percent / 100 (rounded); remainder = base_salary − advance.
	// Per-entry override wins; else use tenant payroll_settings.advance_percent; else default 40.
	advancePct := input.AdvancePercent
	if advancePct <= 0 || advancePct > 100 {
		settings, sErr := h.getOrInitPayrollSettings(tenantID)
		if sErr == nil && settings != nil {
			advancePct = settings.AdvancePercent
		}
	}
	if advancePct <= 0 || advancePct > 100 {
		advancePct = 40
	}
	advanceAmount := math.Round(input.BaseSalary * advancePct / 100)
	remainderAmount := input.BaseSalary - advanceAmount

	orgID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	id := uuid.New()
	now := time.Now()

	paymentMethod := input.PaymentMethod
	if paymentMethod == "" {
		paymentMethod = "bank_transfer"
	}

	query := `
		INSERT INTO payroll_entries (
			id, tenant_id, organization_id, payroll_period_id, employee_id, employee_name,
			position_snapshot, base_salary,
			overtime_hours, overtime_amount, bonus, allowances, gross_salary, income_tax,
			social_security, pension, other_deductions, total_deductions, net_salary,
			advance_amount, remainder_amount, advance_percent_used, advance_paid, remainder_paid,
			payment_method, bank_account, status, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		          $15, $16, $17, $18, $19,
		          $20, $21, $22, false, false,
		          $23, $24, $25, $26, $27, $27)
		RETURNING id
	`

	var bankAccount, notes *string
	if input.BankAccount != "" {
		bankAccount = &input.BankAccount
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	if err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, payrollPeriodID, employeeID, employeeName,
		positionSnapshot, input.BaseSalary,
		input.OvertimeHours, input.OvertimeAmount, input.Bonus, input.Allowances, grossSalary,
		effectiveIncomeTax, effectiveSocialSecurity, effectivePension, input.OtherDeductions, totalDeductions,
		netSalary, advanceAmount, remainderAmount, advancePct,
		paymentMethod, bankAccount, "pending", notes, now,
	).Scan(&id); err != nil {
		h.log.Error("Failed to create payroll entry", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Payroll entry for this employee already exists in this period")
			return
		}
		response.InternalError(c, "Failed to create payroll entry")
		return
	}

	// Persist the employee-tax snapshot rows for this entry (migration 330).
	if len(appliedTaxes) > 0 {
		if err := h.writePayrollEntryTaxes(tenantID, orgIDPtr, id, appliedTaxes); err != nil {
			h.log.Error("Failed to persist payroll entry taxes", "error", err)
			// Not fatal — the entry was created. Admin can re-save to retry.
		}
	}

	// Process partial deductions if deduction_percent is specified
	if input.DeductionPercent > 0 && input.DeductionPercent < 100 && input.OtherDeductions > 0 {
		// Split pending deductions: mark percentage portion as linked to this payroll
		dedRows, dedErr := h.db.Query(`
			SELECT id, amount, reason, source_type, source_id
			FROM employee_deductions
			WHERE employee_id=$1 AND tenant_id=$2 AND status='pending'
			ORDER BY created_at
		`, employeeID, tenantID)
		if dedErr == nil {
			type dedItem struct {
				ID         uuid.UUID
				Amount     float64
				Reason     string
				SourceType string
				SourceID   *uuid.UUID
			}
			var deds []dedItem
			for dedRows.Next() {
				var d dedItem
				var sourceID sql.NullString
				if err := dedRows.Scan(&d.ID, &d.Amount, &d.Reason, &d.SourceType, &sourceID); err != nil {
					continue
				}
				if sourceID.Valid {
					sid, _ := uuid.Parse(sourceID.String)
					d.SourceID = &sid
				}
				deds = append(deds, d)
			}
			dedRows.Close()

			for _, d := range deds {
				deductedAmount := math.Round(d.Amount * input.DeductionPercent / 100)
				remainingAmount := d.Amount - deductedAmount

				if remainingAmount > 0 {
					// Reduce original deduction to the portion that will be deducted
					h.db.Exec(`UPDATE employee_deductions SET amount=$1, updated_at=$2 WHERE id=$3`, deductedAmount, now, d.ID)

					// Create new pending deduction for the remainder
					h.db.Exec(`
						INSERT INTO employee_deductions (id, tenant_id, organization_id, employee_id, amount, reason, source_type, source_id, status, created_by, created_at, updated_at)
						VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10, $10)
					`, uuid.New(), tenantID, orgIDPtr, employeeID, remainingAmount, d.Reason+" (qoldiq)", d.SourceType, d.SourceID, userID, now)
				}
			}
		}
	}

	// Update period totals
	h.updatePayrollPeriodTotals(payrollPeriodID, tenantID)

	entry := &entity.PayrollEntry{
		ID:              id,
		TenantID:        tenantID,
		PayrollPeriodID: payrollPeriodID,
		EmployeeID:      employeeID,
		EmployeeName:    employeeName,
		BaseSalary:      input.BaseSalary,
		OvertimeHours:   input.OvertimeHours,
		OvertimeAmount:  input.OvertimeAmount,
		Bonus:           input.Bonus,
		Allowances:      input.Allowances,
		GrossSalary:     grossSalary,
		IncomeTax:       effectiveIncomeTax,
		SocialSecurity:  effectiveSocialSecurity,
		Pension:         effectivePension,
		OtherDeductions: input.OtherDeductions,
		TotalDeductions: totalDeductions,
		NetSalary:       netSalary,
		PaymentMethod:   paymentMethod,
		BankAccount:     bankAccount,
		Status:          "pending",
		Notes:           notes,
		CreatedAt:       now,
	}

	response.Created(c, entry.ToResponse())
}

// legacyBucketsFromTaxes distributes the applied employee-paid tax amounts into
// the legacy (income_tax / social_security / pension) buckets so that older
// reports and screens that read those columns still show meaningful numbers.
// Unknown codes fall into income_tax. Employer-paid taxes are NOT bucketed here
// because they don't reduce the employee's net pay.
func legacyBucketsFromTaxes(applied []entity.PayrollEntryTax) (income, social, pension float64) {
	for _, t := range applied {
		if t.PayerSnapshot != "employee" {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(t.TaxCodeSnapshot))
		switch code {
		case "INPS", "SOC", "SS", "SOCIAL", "SOCIAL_SECURITY":
			social += t.Amount
		case "PENSION", "PF", "PENSIYA":
			pension += t.Amount
		default:
			income += t.Amount
		}
	}
	return
}

// updatePayrollPeriodTotals recalculates and updates period totals
func (h *Handler) updatePayrollPeriodTotals(periodID, tenantID uuid.UUID) {
	query := `
		UPDATE payroll_periods SET
			total_gross = COALESCE((SELECT SUM(gross_salary) FROM payroll_entries WHERE payroll_period_id = $1), 0),
			total_deductions = COALESCE((SELECT SUM(total_deductions) FROM payroll_entries WHERE payroll_period_id = $1), 0),
			total_net = COALESCE((SELECT SUM(net_salary) FROM payroll_entries WHERE payroll_period_id = $1), 0),
			employee_count = (SELECT COUNT(*) FROM payroll_entries WHERE payroll_period_id = $1),
			updated_at = $2
		WHERE id = $1 AND tenant_id = $3
	`
	h.db.Exec(query, periodID, time.Now(), tenantID)
}

// UpdatePayrollEntry updates a single payroll entry for a given period.
// All numeric fields are pointer-nullable — only provided fields are changed.
// Gross, total_deductions, net_salary, advance_amount and remainder_amount are
// recomputed server-side after the overlay. Entries in status 'paid' or 'approved'
// cannot be edited (TT §2.1 immutability once processed).
func (h *Handler) UpdatePayrollEntry(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payroll period ID")
		return
	}
	entryID, err := uuid.Parse(c.Param("eid"))
	if err != nil {
		response.BadRequest(c, "Invalid payroll entry ID")
		return
	}

	var input entity.UpdatePayrollEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid update payroll entry input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Load existing entry
	var (
		curBaseSalary, curOvertimeHours, curOvertimeAmount, curBonus, curAllowances float64
		curIncomeTax, curSocialSecurity, curPension, curOtherDeductions             float64
		curAdvancePercentUsed                                                       float64
		curPaymentMethod, curStatus                                                 string
		curBankAccount, curNotes                                                    sql.NullString
	)
	err = h.db.QueryRow(`
		SELECT base_salary, overtime_hours, overtime_amount, bonus, allowances,
		       income_tax, social_security, pension, other_deductions,
		       COALESCE(advance_percent_used, 40),
		       payment_method, status, bank_account, notes
		FROM payroll_entries
		WHERE id = $1 AND payroll_period_id = $2 AND tenant_id = $3
	`, entryID, periodID, tenantID).Scan(
		&curBaseSalary, &curOvertimeHours, &curOvertimeAmount, &curBonus, &curAllowances,
		&curIncomeTax, &curSocialSecurity, &curPension, &curOtherDeductions,
		&curAdvancePercentUsed,
		&curPaymentMethod, &curStatus, &curBankAccount, &curNotes,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payroll entry not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to load payroll entry", "error", err)
		response.InternalError(c, "Failed to load payroll entry")
		return
	}

	// Block edits on entries that have already been processed
	if curStatus == "paid" || curStatus == "approved" {
		response.BadRequest(c, "Cannot edit a payroll entry that has already been approved or paid")
		return
	}

	// Overlay provided fields
	if input.BaseSalary != nil {
		curBaseSalary = *input.BaseSalary
	}
	if input.OvertimeHours != nil {
		curOvertimeHours = *input.OvertimeHours
	}
	if input.OvertimeAmount != nil {
		curOvertimeAmount = *input.OvertimeAmount
	}
	if input.Bonus != nil {
		curBonus = *input.Bonus
	}
	if input.Allowances != nil {
		curAllowances = *input.Allowances
	}
	if input.IncomeTax != nil {
		curIncomeTax = *input.IncomeTax
	}
	if input.SocialSecurity != nil {
		curSocialSecurity = *input.SocialSecurity
	}
	if input.Pension != nil {
		curPension = *input.Pension
	}
	if input.OtherDeductions != nil {
		curOtherDeductions = *input.OtherDeductions
	}
	if input.PaymentMethod != nil && *input.PaymentMethod != "" {
		curPaymentMethod = *input.PaymentMethod
	}
	if input.Status != nil && *input.Status != "" {
		curStatus = *input.Status
	}
	if input.BankAccount != nil {
		if *input.BankAccount == "" {
			curBankAccount = sql.NullString{Valid: false}
		} else {
			curBankAccount = sql.NullString{String: *input.BankAccount, Valid: true}
		}
	}
	if input.Notes != nil {
		if *input.Notes == "" {
			curNotes = sql.NullString{Valid: false}
		} else {
			curNotes = sql.NullString{String: *input.Notes, Valid: true}
		}
	}

	// Recompute derivatives — prefer the new employee-tax catalog when it's
	// populated for this tenant. When the client has supplied a new exclusion
	// list, rebuild the snapshot rows. Otherwise replay the same catalog but
	// keep the previously-excluded taxes excluded.
	grossSalary := curBaseSalary + curOvertimeAmount + curBonus + curAllowances

	var excluded []uuid.UUID
	if input.ExcludedTaxIDs != nil {
		excluded = *input.ExcludedTaxIDs
	} else {
		// Replay previous selection: compute the complement of what's currently stored.
		if existing, _ := h.listPayrollEntryTaxes(tenantID, entryID); len(existing) > 0 {
			applied := make(map[uuid.UUID]bool, len(existing))
			for _, r := range existing {
				if r.TaxID != nil {
					applied[*r.TaxID] = true
				}
			}
			catalog, _ := h.listActiveEmployeeTaxesForTenant(tenantID)
			for _, t := range catalog {
				if !applied[t.ID] {
					excluded = append(excluded, t.ID)
				}
			}
		}
	}

	appliedTaxes, taxTotals, tErr := h.computeEmployeeTaxesForEntry(
		tenantID, curBaseSalary, curOvertimeAmount, curBonus, curAllowances, excluded,
	)
	if tErr != nil {
		h.log.Error("Failed to compute employee taxes in update", "error", tErr)
	}

	if len(appliedTaxes) > 0 {
		curIncomeTax, curSocialSecurity, curPension = legacyBucketsFromTaxes(appliedTaxes)
	}

	totalDeductions := curIncomeTax + curSocialSecurity + curPension + curOtherDeductions
	if len(appliedTaxes) > 0 {
		totalDeductions = taxTotals.Employee + curOtherDeductions
	}
	netSalary := grossSalary - totalDeductions
	advancePct := curAdvancePercentUsed
	if advancePct <= 0 || advancePct > 100 {
		advancePct = 40
	}
	advanceAmount := math.Round(curBaseSalary * advancePct / 100)
	remainderAmount := curBaseSalary - advanceAmount

	now := time.Now()
	var bankPtr, notesPtr interface{}
	if curBankAccount.Valid {
		bankPtr = curBankAccount.String
	} else {
		bankPtr = nil
	}
	if curNotes.Valid {
		notesPtr = curNotes.String
	} else {
		notesPtr = nil
	}

	_, err = h.db.Exec(`
		UPDATE payroll_entries SET
			base_salary = $1,
			overtime_hours = $2,
			overtime_amount = $3,
			bonus = $4,
			allowances = $5,
			gross_salary = $6,
			income_tax = $7,
			social_security = $8,
			pension = $9,
			other_deductions = $10,
			total_deductions = $11,
			net_salary = $12,
			advance_amount = $13,
			remainder_amount = $14,
			payment_method = $15,
			status = $16,
			bank_account = $17,
			notes = $18,
			updated_at = $19
		WHERE id = $20 AND payroll_period_id = $21 AND tenant_id = $22
	`,
		curBaseSalary, curOvertimeHours, curOvertimeAmount, curBonus, curAllowances,
		grossSalary,
		curIncomeTax, curSocialSecurity, curPension, curOtherDeductions, totalDeductions,
		netSalary,
		advanceAmount, remainderAmount,
		curPaymentMethod, curStatus, bankPtr, notesPtr, now,
		entryID, periodID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update payroll entry", "error", err)
		response.InternalError(c, "Failed to update payroll entry")
		return
	}

	// Rewrite the employee-tax snapshot for this entry based on the newly-computed
	// applied list. When the tenant has no active employee_taxes, `appliedTaxes` is
	// empty and we simply clear any stale rows.
	{
		orgID, _ := middleware.GetOrganizationID(c)
		var orgIDPtr *uuid.UUID
		if orgID != uuid.Nil {
			orgIDPtr = &orgID
		}
		if err := h.writePayrollEntryTaxes(tenantID, orgIDPtr, entryID, appliedTaxes); err != nil {
			h.log.Error("Failed to persist payroll entry taxes", "error", err)
		}
	}

	// Recalc period totals
	h.updatePayrollPeriodTotals(periodID, tenantID)

	// Re-read full row and return a response
	var entry entity.PayrollEntry
	var bankAccount, notes sql.NullString
	var advanceDay, remainderDay sql.NullInt64
	if err := h.db.QueryRow(`
		SELECT id, tenant_id, payroll_period_id, employee_id, employee_name,
		       COALESCE(position_snapshot, ''),
		       base_salary, overtime_hours, overtime_amount, bonus, allowances,
		       gross_salary, income_tax, social_security, pension, other_deductions,
		       total_deductions, net_salary,
		       COALESCE(advance_amount, 0), COALESCE(remainder_amount, 0),
		       COALESCE(advance_paid, false), advance_paid_day,
		       COALESCE(remainder_paid, false), remainder_paid_day,
		       COALESCE(advance_percent_used, 40),
		       payment_method, bank_account, status, notes, created_at
		FROM payroll_entries
		WHERE id = $1 AND tenant_id = $2
	`, entryID, tenantID).Scan(
		&entry.ID, &entry.TenantID, &entry.PayrollPeriodID, &entry.EmployeeID,
		&entry.EmployeeName, &entry.PositionSnapshot,
		&entry.BaseSalary, &entry.OvertimeHours, &entry.OvertimeAmount,
		&entry.Bonus, &entry.Allowances, &entry.GrossSalary, &entry.IncomeTax,
		&entry.SocialSecurity, &entry.Pension, &entry.OtherDeductions, &entry.TotalDeductions,
		&entry.NetSalary,
		&entry.AdvanceAmount, &entry.RemainderAmount,
		&entry.AdvancePaid, &advanceDay,
		&entry.RemainderPaid, &remainderDay,
		&entry.AdvancePercentUsed,
		&entry.PaymentMethod, &bankAccount, &entry.Status, &notes, &entry.CreatedAt,
	); err != nil {
		h.log.Error("Failed to re-read payroll entry after update", "error", err)
		response.InternalError(c, "Failed to load updated payroll entry")
		return
	}

	if bankAccount.Valid {
		entry.BankAccount = &bankAccount.String
	}
	if notes.Valid {
		entry.Notes = &notes.String
	}
	if advanceDay.Valid {
		d := int(advanceDay.Int64)
		entry.AdvancePaidDay = &d
	}
	if remainderDay.Valid {
		d := int(remainderDay.Int64)
		entry.RemainderPaidDay = &d
	}

	response.Success(c, entry.ToResponse())
}

// ProcessPayroll processes payroll for a period (approves all entries)
func (h *Handler) ProcessPayroll(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	periodID := c.Param("id")
	id, err := uuid.Parse(periodID)
	if err != nil {
		response.BadRequest(c, "Invalid payroll period ID")
		return
	}

	now := time.Now()

	// Update all entries to approved
	entryQuery := `
		UPDATE payroll_entries SET status = 'approved', updated_at = $1
		WHERE payroll_period_id = $2 AND tenant_id = $3 AND status = 'pending'
	`
	if _, err := h.db.Exec(entryQuery, now, id, tenantID); err != nil {
		h.log.Error("Failed to process payroll entries", "error", err)
		response.InternalError(c, "Failed to process payroll")
		return
	}

	// Update period status
	periodQuery := `
		UPDATE payroll_periods SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $2
		WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
	`
	if _, err := h.db.Exec(periodQuery, userID, now, id, tenantID); err != nil {
		h.log.Error("Failed to update payroll period status", "error", err)
		response.InternalError(c, "Failed to process payroll")
		return
	}

	// ============================================
	// CREATE JOURNAL ENTRY FOR PAYROLL
	// ============================================
	func() {
		// Get period details and total gross salary
		var periodName string
		var orgID sql.NullString
		h.db.QueryRow(`SELECT name, organization_id FROM payroll_periods WHERE id = $1`, id).Scan(&periodName, &orgID)

		var totalGross float64
		h.db.QueryRow(`
			SELECT COALESCE(SUM(gross_salary), 0)
			FROM payroll_entries WHERE payroll_period_id = $1 AND tenant_id = $2`,
			id, tenantID).Scan(&totalGross)

		if totalGross <= 0 {
			return
		}

		var orgIDPtr *uuid.UUID
		if orgID.Valid {
			if parsedOrgID, err := uuid.Parse(orgID.String); err == nil {
				orgIDPtr = &parsedOrgID
			}
		}

		// Look up journal
		var journalID uuid.UUID
		var nextNumber int
		err := h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('PAYROLL','MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE code WHEN 'PAYROLL' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNumber)
		if err != nil {
			return
		}

		entryNumber := fmt.Sprintf("PAY%06d", nextNumber)
		journalEntryID := uuid.New()
		description := "Payroll: " + periodName

		// Debit: Salary Expense
		salaryAcct := findAccount(h.db, tenantID, orgIDPtr, "salaries", "9420")
		if salaryAcct == uuid.Nil {
			salaryAcct = findAccount(h.db, tenantID, orgIDPtr, "salary", "9420")
		}
		// Credit: AP / Wages Payable
		payableAcct := findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "6010")
		if payableAcct == uuid.Nil {
			payableAcct = findAccount(h.db, tenantID, orgIDPtr, "cash", "5010")
		}

		if salaryAcct == uuid.Nil || payableAcct == uuid.Nil {
			return
		}

		// Aggregate employee-tax amounts by account (migration 330). When the period
		// has configured taxes, we split the credit side of the journal into:
		//   Cr Wages Payable  = Net pay actually paid to employees
		//   Cr Tax Liability  = Per-tax credit to the configured liability account
		//   Dr Tax Expense    = Per-tax debit for employer-paid taxes (adds to total debit)
		type acctBucket struct {
			amount      float64
			description string
		}
		taxLiabilityByAcct := map[uuid.UUID]*acctBucket{} // Cr (both employee and employer taxes land here)
		taxExpenseByAcct := map[uuid.UUID]*acctBucket{}   // Dr (employer-paid only)
		totalEmployeeTaxWithdrawn := 0.0                  // reduces Wages Payable
		totalEmployerTaxExpense := 0.0                    // adds to total_debit / total_credit

		taxRows, _ := h.db.Query(`
			SELECT
				pet.payer_snapshot,
				pet.amount,
				pet.account_id_snapshot,
				pet.expense_account_id_snapshot,
				pet.tax_code_snapshot,
				pet.tax_name_snapshot
			FROM payroll_entry_taxes pet
			JOIN payroll_entries pe ON pe.id = pet.payroll_entry_id
			WHERE pe.payroll_period_id = $1 AND pet.tenant_id = $2
		`, id, tenantID)
		if taxRows != nil {
			for taxRows.Next() {
				var payer, taxCode, taxName string
				var amount float64
				var acctID, expAcctID sql.NullString
				if err := taxRows.Scan(&payer, &amount, &acctID, &expAcctID, &taxCode, &taxName); err != nil {
					continue
				}
				if amount <= 0 {
					continue
				}
				// Credit side — tax liability account (always required)
				if acctID.Valid {
					if u, perr := uuid.Parse(acctID.String); perr == nil {
						b := taxLiabilityByAcct[u]
						if b == nil {
							b = &acctBucket{description: fmt.Sprintf("Tax liability: %s", taxName)}
							taxLiabilityByAcct[u] = b
						}
						b.amount += amount
					}
				}
				if payer == "employer" {
					totalEmployerTaxExpense += amount
					if expAcctID.Valid {
						if u, perr := uuid.Parse(expAcctID.String); perr == nil {
							b := taxExpenseByAcct[u]
							if b == nil {
								b = &acctBucket{description: fmt.Sprintf("Tax expense: %s", taxName)}
								taxExpenseByAcct[u] = b
							}
							b.amount += amount
						}
					}
				} else {
					totalEmployeeTaxWithdrawn += amount
				}
				_ = taxCode
			}
			taxRows.Close()
		}

		// Debit totals = gross salary (to salary expense) + employer-paid tax expenses
		// Credit totals = wages payable (net) + tax liability credits
		totalDebit := totalGross + totalEmployerTaxExpense
		totalCredit := totalDebit // balanced by construction

		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'payroll', $9, 1.0, $10, $11, 'posted', $12, $13, $13)`,
			journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, now, periodName, description,
			id.String(), totalDebit, totalCredit, userID, now,
		)
		if err != nil {
			h.log.Error("Failed to create payroll journal entry", "error", err)
			return
		}

		lineNo := 1

		// Dr: Salary Expense
		h.db.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, 0, 1.0, $7)`,
			uuid.New(), journalEntryID, lineNo, salaryAcct, "Salary Expense", totalGross, now,
		)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalGross, now, salaryAcct)
		lineNo++

		// Dr: Employer tax expense(s)
		for acctID, b := range taxExpenseByAcct {
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, 0, 1.0, $7)`,
				uuid.New(), journalEntryID, lineNo, acctID, b.description, b.amount, now,
			)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", b.amount, now, acctID)
			lineNo++
		}

		// Cr: Wages Payable (net pay to employees, after employee-paid taxes withheld)
		netPayable := totalGross - totalEmployeeTaxWithdrawn
		if netPayable > 0 {
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, 0, $6, 1.0, $7)`,
				uuid.New(), journalEntryID, lineNo, payableAcct, "Wages Payable", netPayable, now,
			)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", netPayable, now, payableAcct)
			lineNo++
		}

		// Cr: Tax Liability account(s) — one line per distinct liability account
		for acctID, b := range taxLiabilityByAcct {
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, 0, $6, 1.0, $7)`,
				uuid.New(), journalEntryID, lineNo, acctID, b.description, b.amount, now,
			)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", b.amount, now, acctID)
			lineNo++
		}

		h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
	}()

	response.Success(c, gin.H{"message": "Payroll processed successfully"})
}

// CalculateSalaryWithDeductions calculates salary for an employee including pending deductions
func (h *Handler) CalculateSalaryWithDeductions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Get employee info
	var firstName, lastName string
	var baseSalary float64
	err = h.db.QueryRow(`
		SELECT first_name, COALESCE(last_name, ''), COALESCE(base_salary, 0)
		FROM employees WHERE id=$1 AND tenant_id=$2
	`, employeeID, tenantID).Scan(&firstName, &lastName, &baseSalary)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Employee")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to query employee")
		return
	}

	employeeName := strings.TrimSpace(firstName + " " + lastName)

	// Get pending deductions
	rows, err := h.db.Query(`
		SELECT id, amount, reason, source_type
		FROM employee_deductions
		WHERE employee_id=$1 AND tenant_id=$2 AND status='pending'
		ORDER BY created_at
	`, employeeID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to query deductions")
		return
	}
	defer rows.Close()

	type deductionItem struct {
		ID         uuid.UUID `json:"id"`
		Amount     float64   `json:"amount"`
		Reason     string    `json:"reason"`
		SourceType string    `json:"source_type"`
	}

	deductions := make([]deductionItem, 0)
	totalDeduction := 0.0
	for rows.Next() {
		var d deductionItem
		if err := rows.Scan(&d.ID, &d.Amount, &d.Reason, &d.SourceType); err != nil {
			continue
		}
		deductions = append(deductions, d)
		totalDeduction += d.Amount
	}

	netSalary := baseSalary - totalDeduction
	if netSalary < 0 {
		netSalary = 0
	}

	response.Success(c, gin.H{
		"employee_id":     employeeID,
		"employee_name":   employeeName,
		"base_salary":     baseSalary,
		"deductions":      deductions,
		"total_deduction": totalDeduction,
		"net_salary":      netSalary,
	})
}

// ConfirmSalaryPayment confirms payment and marks deductions as deducted
func (h *Handler) ConfirmSalaryPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	entryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payroll entry ID")
		return
	}

	// Parse optional deduction_percent from body
	var body struct {
		DeductionPercent *float64 `json:"deduction_percent"`
	}
	c.ShouldBindJSON(&body)
	deductionPercent := 100.0
	if body.DeductionPercent != nil && *body.DeductionPercent >= 0 && *body.DeductionPercent <= 100 {
		deductionPercent = *body.DeductionPercent
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	// Get the payroll entry
	var employeeID uuid.UUID
	var status string
	var netSalary float64
	err = h.db.QueryRow(`
		SELECT employee_id, status, net_salary FROM payroll_entries
		WHERE id=$1 AND tenant_id=$2
	`, entryID, tenantID).Scan(&employeeID, &status, &netSalary)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payroll entry")
		return
	}
	if status == "paid" {
		response.BadRequest(c, "Bu to'lov allaqachon tasdiqlangan")
		return
	}

	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Mark payroll entry as paid
	_, err = tx.Exec(`
		UPDATE payroll_entries SET status='paid', updated_at=$1 WHERE id=$2
	`, now, entryID)
	if err != nil {
		response.InternalError(c, "Failed to update entry")
		return
	}

	// Handle deductions with percentage support
	totalDeducted := 0.0
	if deductionPercent >= 100 {
		// Full deduction: mark all pending as deducted
		_, err = tx.Exec(`
			UPDATE employee_deductions SET status='deducted', payroll_entry_id=$1, deducted_at=$2, updated_at=$2
			WHERE employee_id=$3 AND tenant_id=$4 AND status='pending'
		`, entryID, now, employeeID, tenantID)
		if err != nil {
			response.InternalError(c, "Failed to update deductions")
			return
		}
		tx.QueryRow(`
			SELECT COALESCE(SUM(amount), 0) FROM employee_deductions
			WHERE payroll_entry_id=$1 AND status='deducted'
		`, entryID).Scan(&totalDeducted)
	} else if deductionPercent > 0 {
		// Partial deduction: split each pending deduction
		rows, err := tx.Query(`
			SELECT id, amount, reason, source_type, source_id
			FROM employee_deductions
			WHERE employee_id=$1 AND tenant_id=$2 AND status='pending'
			ORDER BY created_at
		`, employeeID, tenantID)
		if err != nil {
			response.InternalError(c, "Failed to query deductions")
			return
		}
		type pendingDed struct {
			ID         uuid.UUID
			Amount     float64
			Reason     string
			SourceType string
			SourceID   *uuid.UUID
		}
		var pending []pendingDed
		for rows.Next() {
			var d pendingDed
			var sourceID sql.NullString
			if err := rows.Scan(&d.ID, &d.Amount, &d.Reason, &d.SourceType, &sourceID); err != nil {
				continue
			}
			if sourceID.Valid {
				sid, _ := uuid.Parse(sourceID.String)
				d.SourceID = &sid
			}
			pending = append(pending, d)
		}
		rows.Close()

		for _, d := range pending {
			deductedAmount := math.Round(d.Amount * deductionPercent / 100)
			remainingAmount := d.Amount - deductedAmount
			totalDeducted += deductedAmount

			// Update original: reduce amount to deducted portion and mark as deducted
			_, err = tx.Exec(`
				UPDATE employee_deductions SET amount=$1, status='deducted', payroll_entry_id=$2, deducted_at=$3, updated_at=$3
				WHERE id=$4
			`, deductedAmount, entryID, now, d.ID)
			if err != nil {
				response.InternalError(c, "Failed to update deduction")
				return
			}

			// Create new pending deduction for the remaining amount
			if remainingAmount > 0 {
				var sourceIDPtr *uuid.UUID
				if d.SourceID != nil {
					sourceIDPtr = d.SourceID
				}
				var orgIDPtr *uuid.UUID
				if orgID != uuid.Nil {
					orgIDPtr = &orgID
				}
				_, err = tx.Exec(`
					INSERT INTO employee_deductions (id, tenant_id, organization_id, employee_id, amount, reason, source_type, source_id, status, created_by, created_at, updated_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10, $10)
				`, uuid.New(), tenantID, orgIDPtr, employeeID, remainingAmount, d.Reason+" (qoldiq)", d.SourceType, sourceIDPtr, userID, now)
				if err != nil {
					h.log.Error("Failed to create remaining deduction", "error", err)
				}
			}
		}
	}
	// else deductionPercent == 0: no deductions applied

	if totalDeducted > 0 {
		var orgIDPtr *uuid.UUID
		if orgID != uuid.Nil {
			orgIDPtr = &orgID
		}

		// Find journal
		var journalID uuid.UUID
		var nextNumber int
		err := h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1) FROM journals
			WHERE tenant_id=$1 AND code IN ('MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE WHEN code='MISC' THEN 0 ELSE 1 END LIMIT 1
		`, tenantID).Scan(&journalID, &nextNumber)

		if err == nil && journalID != uuid.Nil {
			jeID := uuid.New()
			entryNumber := fmt.Sprintf("DED%06d", nextNumber)
			description := fmt.Sprintf("Ish haqi kamomad ushlab qolish: %s", entryID.String()[:8])

			tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date,
					description, source_type, source_id, status, total_debit, total_credit,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 'salary_deduction', $8, 'posted', $9, $9, $10, $11, $11)
			`, jeID, tenantID, orgIDPtr, journalID, entryNumber, now,
				description, entryID.String(), totalDeducted, userID, now)

			// Dt 6710 — Salary expense
			salaryAcct := findAccount(h.db, tenantID, orgIDPtr, "ish haqi", "6710")
			if salaryAcct == uuid.Nil {
				salaryAcct = findAccount(h.db, tenantID, orgIDPtr, "salary", "6710")
			}
			// Kt 4730 — Employee receivable (deduction)
			deductAcct := findAccount(h.db, tenantID, orgIDPtr, "xodimdan undirish", "4730")
			if deductAcct == uuid.Nil {
				deductAcct = findAccount(h.db, tenantID, orgIDPtr, "employee receivable", "4730")
			}

			if salaryAcct != uuid.Nil && deductAcct != uuid.Nil {
				// TT §4.5 — 6710 and 4730 both require xodim (employee) subkonto
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, employee_id, description, debit_amount, credit_amount, exchange_rate, amount_base, analytics_json, line_number, created_at)
					VALUES ($1, $2, $3, $4, 'Ish haqi xarajat', $5, 0, 1.0, $5, '{}'::jsonb, 1, $6)`,
					uuid.New(), jeID, salaryAcct, employeeID, totalDeducted, now)
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, employee_id, description, debit_amount, credit_amount, exchange_rate, amount_base, analytics_json, line_number, created_at)
					VALUES ($1, $2, $3, $4, 'Kamomad ushlab qolish', 0, $5, 1.0, $5, '{}'::jsonb, 2, $6)`,
					uuid.New(), jeID, deductAcct, employeeID, totalDeducted, now)

				tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalDeducted, now, salaryAcct)
				tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalDeducted, now, deductAcct)
			}

			h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
		}
	}

	if err = tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
	}

	// In-app notification: salary confirmed
	go func() {
		var empName string
		var netSalary float64
		h.db.QueryRow(`
			SELECT CONCAT(e.first_name, ' ', e.last_name), COALESCE(pe.net_salary, 0)
			FROM payroll_entries pe JOIN employees e ON pe.employee_id = e.id
			WHERE pe.id = $1`, entryID).Scan(&empName, &netSalary)
		salaryStr := fmt.Sprintf("%.0f", netSalary)
		// employee_name added for web re-render; additive.
		h.createTranslatedNotification(tenantID, userID, "salary_confirmed",
			map[string]interface{}{
				"entry_id":      entryID.String(),
				"employee_id":   employeeID.String(),
				"employee_name": empName,
				"net_salary":    netSalary,
			},
			salaryStr, empName,
		)
	}()

	// SMS: Ish haqi chiqarilganda
	go func() {
		var phone string
		var grossSalary, totalDed, netSal float64
		var loanDeduction float64
		h.db.QueryRow(`
			SELECT COALESCE(e.phone, ''),
				COALESCE(pe.gross_salary, 0), COALESCE(pe.total_deductions, 0), COALESCE(pe.net_salary, 0)
			FROM payroll_entries pe
			JOIN employees e ON pe.employee_id = e.id
			WHERE pe.id = $1 AND pe.tenant_id = $2
		`, entryID, tenantID).Scan(&phone, &grossSalary, &totalDed, &netSal)
		if phone != "" {
			// Check if employee has active loan payment deduction
			h.db.QueryRow(`SELECT COALESCE(monthly_payment, 0) FROM employee_loans WHERE employee_id = $1 AND tenant_id = $2 AND status = 'active' AND deleted_at IS NULL LIMIT 1`,
				employeeID, tenantID).Scan(&loanDeduction)
			// Check remaining loan
			var loanRemaining float64
			h.db.QueryRow(`SELECT COALESCE(remaining_amount, 0) FROM employee_loans WHERE employee_id = $1 AND tenant_id = $2 AND status = 'active' AND deleted_at IS NULL LIMIT 1`,
				employeeID, tenantID).Scan(&loanRemaining)

			msg := fmt.Sprintf("Ish haqi: Hisoblangan: %s. ", formatSMSAmount(grossSalary))
			if loanDeduction > 0 {
				msg += fmt.Sprintf("Qarz ushildi: -%s. ", formatSMSAmount(loanDeduction))
			}
			if totalDed > 0 {
				msg += fmt.Sprintf("Ushlanmalar: -%s. ", formatSMSAmount(totalDed))
			}
			msg += fmt.Sprintf("Qo'lga olasiz: %s.", formatSMSAmount(netSal))
			if loanRemaining > 0 {
				msg += fmt.Sprintf(" Qarz qoldi: %s.", formatSMSAmount(loanRemaining))
			}
			if err := h.smsService.Send(phone, msg); err != nil {
				h.log.Error("Failed to send payroll SMS", "error", err, "phone", phone)
			}
		}
	}()

	response.Success(c, gin.H{
		"success":        true,
		"message":        "To'lov tasdiqlandi",
		"total_deducted": totalDeducted,
	})
}

// formatSMSAmount formats a number with space-separated thousands for SMS
func formatSMSAmount(n float64) string {
	s := fmt.Sprintf("%.0f", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ' ')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// ConfirmSalaryPaymentByEntry is a route adapter that reads :eid param instead of :id
func (h *Handler) ConfirmSalaryPaymentByEntry(c *gin.Context) {
	eid := c.Param("eid")
	c.Params = append(c.Params, gin.Param{Key: "id", Value: eid})
	h.ConfirmSalaryPayment(c)
}
