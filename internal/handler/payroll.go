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
			   (SELECT pe.employee_name FROM payroll_entries pe WHERE pe.payroll_period_id = pp.id AND pe.deleted_at IS NULL LIMIT 1) as first_employee_name
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
		var notes, firstEmployeeName sql.NullString

		if err := rows.Scan(
			&period.ID, &period.TenantID, &period.PeriodCode, &period.PeriodName,
			&period.StartDate, &period.EndDate, &period.PayDate, &period.Status,
			&period.TotalGross, &period.TotalDeductions, &period.TotalNet,
			&period.EmployeeCount, &notes, &period.CreatedAt, &firstEmployeeName,
		); err != nil {
			h.log.Error("Failed to scan payroll period", "error", err)
			continue
		}

		if notes.Valid {
			period.Notes = &notes.String
		}

		resp := period.ToResponse()
		// If there's only one employee, include their name
		if firstEmployeeName.Valid && period.EmployeeCount == 1 {
			resp.EmployeeName = firstEmployeeName.String
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
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	periodCode := input.PeriodCode
	if periodCode == "" {
		periodCode = fmt.Sprintf("PAY-%d-%02d", time.Now().Year(), time.Now().Month())
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
		SELECT id, tenant_id, payroll_period_id, employee_id, employee_name, base_salary,
			   overtime_hours, overtime_amount, bonus, allowances, gross_salary, income_tax,
			   social_security, pension, other_deductions, total_deductions, net_salary,
			   payment_method, bank_account, status, notes, created_at
		FROM payroll_entries
		WHERE payroll_period_id = $1 AND tenant_id = $2
		ORDER BY employee_name
	`

	rows, err := h.db.Query(query, id, tenantID)
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

		if err := rows.Scan(
			&entry.ID, &entry.TenantID, &entry.PayrollPeriodID, &entry.EmployeeID,
			&entry.EmployeeName, &entry.BaseSalary, &entry.OvertimeHours, &entry.OvertimeAmount,
			&entry.Bonus, &entry.Allowances, &entry.GrossSalary, &entry.IncomeTax,
			&entry.SocialSecurity, &entry.Pension, &entry.OtherDeductions, &entry.TotalDeductions,
			&entry.NetSalary, &entry.PaymentMethod, &bankAccount, &entry.Status, &notes, &entry.CreatedAt,
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

		entries = append(entries, entry.ToResponse())
	}

	response.Success(c, entries)
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
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Get employee name
	var employeeName string
	if err := h.db.QueryRow("SELECT CONCAT(first_name, ' ', last_name) FROM employees WHERE id = $1 AND tenant_id = $2", employeeID, tenantID).Scan(&employeeName); err != nil {
		response.BadRequest(c, "Employee not found")
		return
	}

	// Calculate gross and net salary
	grossSalary := input.BaseSalary + input.OvertimeAmount + input.Bonus + input.Allowances
	totalDeductions := input.IncomeTax + input.SocialSecurity + input.Pension + input.OtherDeductions
	netSalary := grossSalary - totalDeductions

	orgID, _ := middleware.GetOrganizationID(c)
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
			id, tenant_id, organization_id, payroll_period_id, employee_id, employee_name, base_salary,
			overtime_hours, overtime_amount, bonus, allowances, gross_salary, income_tax,
			social_security, pension, other_deductions, total_deductions, net_salary,
			payment_method, bank_account, status, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
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
		id, tenantID, orgIDPtr, payrollPeriodID, employeeID, employeeName, input.BaseSalary,
		input.OvertimeHours, input.OvertimeAmount, input.Bonus, input.Allowances, grossSalary,
		input.IncomeTax, input.SocialSecurity, input.Pension, input.OtherDeductions, totalDeductions,
		netSalary, paymentMethod, bankAccount, "pending", notes, now, now,
	).Scan(&id); err != nil {
		h.log.Error("Failed to create payroll entry", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Payroll entry for this employee already exists in this period")
			return
		}
		response.InternalError(c, "Failed to create payroll entry")
		return
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
		IncomeTax:       input.IncomeTax,
		SocialSecurity:  input.SocialSecurity,
		Pension:         input.Pension,
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

	response.Success(c, gin.H{"message": "Payroll processed successfully"})
}
