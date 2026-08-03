package handler

// File: buxgalteriya_payroll_tt.go
//
// TT Ish haqi moduli (v1.1, 2026-04-20) — integration layer.
//
// The existing payroll handler (payroll.go) implements a richer gross/tax/net
// model with loan integration. The TT describes a simpler advance-and-remainder
// model with day-of-month payment tracking. This file layers the TT flow on
// top of the existing tables — it doesn't replace anything.
//
// Endpoints added here:
//   GET  /payroll/settings                             — TT §2.2
//   PUT  /payroll/settings                             — TT §2.2
//   POST /payroll/periods/current-or-create            — TT §2.3 auto-create
//   POST /payroll/entries/:id/advance-paid             — TT §2.4
//   POST /payroll/entries/:id/remainder-paid           — TT §2.4
//   GET  /payroll/export                               — TT §2.8 backup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// ---------------------------------------------------------------------------
// Settings (TT §2.2)
// ---------------------------------------------------------------------------

// getOrInitPayrollSettings returns the tenant's payroll settings, inserting a
// default row (40% / so'm / '') if none exists yet.
func (h *Handler) getOrInitPayrollSettings(tenantID uuid.UUID) (*entity.PayrollSettings, error) {
	var s entity.PayrollSettings
	err := h.db.QueryRow(`
		SELECT tenant_id, advance_percent, currency, company_name, created_at, updated_at
		FROM payroll_settings WHERE tenant_id = $1
	`, tenantID).Scan(&s.TenantID, &s.AdvancePercent, &s.Currency, &s.CompanyName, &s.CreatedAt, &s.UpdatedAt)
	if err == nil {
		return &s, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Insert default row
	now := time.Now()
	_, err = h.db.Exec(`
		INSERT INTO payroll_settings (tenant_id, advance_percent, currency, company_name, created_at, updated_at)
		VALUES ($1, 40, 'so''m', '', $2, $2)
		ON CONFLICT (tenant_id) DO NOTHING
	`, tenantID, now)
	if err != nil {
		return nil, err
	}
	return &entity.PayrollSettings{
		TenantID:       tenantID,
		AdvancePercent: 40,
		Currency:       "so'm",
		CompanyName:    "",
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// GetPayrollSettings — GET /payroll/settings
func (h *Handler) GetPayrollSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	s, err := h.getOrInitPayrollSettings(tenantID)
	if err != nil {
		h.log.Error("GetPayrollSettings", "error", err)
		response.InternalError(c, "Failed to load payroll settings")
		return
	}
	response.Success(c, s)
}

// UpdatePayrollSettings — PUT /payroll/settings
func (h *Handler) UpdatePayrollSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var in entity.UpdatePayrollSettingsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	// Ensure the row exists, then apply partial updates.
	if _, err := h.getOrInitPayrollSettings(tenantID); err != nil {
		response.InternalError(c, "Failed to load settings")
		return
	}
	args := []interface{}{tenantID}
	set := ""
	if in.AdvancePercent != nil {
		args = append(args, *in.AdvancePercent)
		set += fmt.Sprintf(", advance_percent = $%d", len(args))
	}
	if in.Currency != nil {
		args = append(args, *in.Currency)
		set += fmt.Sprintf(", currency = $%d", len(args))
	}
	if in.CompanyName != nil {
		args = append(args, *in.CompanyName)
		set += fmt.Sprintf(", company_name = $%d", len(args))
	}
	if set != "" {
		_, err := h.db.Exec(
			"UPDATE payroll_settings SET updated_at = NOW()"+set+" WHERE tenant_id = $1",
			args...,
		)
		if err != nil {
			h.log.Error("UpdatePayrollSettings", "error", err)
			response.InternalError(c, "Failed to update settings")
			return
		}
	}
	s, _ := h.getOrInitPayrollSettings(tenantID)
	response.Success(c, s)
}

// ---------------------------------------------------------------------------
// Auto-create current-month payroll (TT §2.3)
// ---------------------------------------------------------------------------

// GetOrCreateCurrentMonthPayroll returns the payroll period for a given
// month (YYYY-MM in `?month=`; defaults to today's month). Creates the period
// + entries for every active employee if none exists. TT §2.3.
func (h *Handler) GetOrCreateCurrentMonthPayroll(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	// Active company scope. The frontend sets X-Organization-ID via
	// the global apiClient interceptor; we honour it here so multi-
	// company tenants don't smush every employee into one period.
	// When absent (admin contexts, single-company setups), we fall
	// back to the legacy tenant-wide path with organization_id NULL.
	var orgIDPtr *uuid.UUID
	if oid, oOk := middleware.GetOrganizationID(c); oOk && oid != uuid.Nil {
		v := oid
		orgIDPtr = &v
	}

	monthStr := c.Query("month")
	if monthStr == "" {
		monthStr = time.Now().Format("2006-01")
	}
	monthStart, err := time.Parse("2006-01", monthStr)
	if err != nil {
		response.BadRequest(c, "Invalid month (expected YYYY-MM)")
		return
	}
	monthEnd := monthStart.AddDate(0, 1, -1)
	periodCode := monthStr // YYYY-MM

	// Existing-period lookup: scoped to the active org. NULL-org rows
	// belong to the legacy tenant-wide pool and only match when no
	// active org is set.
	var periodID uuid.UUID
	if orgIDPtr != nil {
		err = h.db.QueryRow(`
			SELECT id FROM payroll_periods
			WHERE tenant_id = $1 AND organization_id = $2
			  AND period_code = $3 AND deleted_at IS NULL
			LIMIT 1
		`, tenantID, *orgIDPtr, periodCode).Scan(&periodID)
	} else {
		err = h.db.QueryRow(`
			SELECT id FROM payroll_periods
			WHERE tenant_id = $1 AND organization_id IS NULL
			  AND period_code = $2 AND deleted_at IS NULL
			LIMIT 1
		`, tenantID, periodCode).Scan(&periodID)
	}

	if err == nil {
		h.respondPayrollPeriodWithEntries(c, tenantID, periodID, false)
		return
	}
	if err != sql.ErrNoRows {
		h.log.Error("GetOrCreateCurrentMonthPayroll: lookup failed", "error", err)
		response.InternalError(c, "Failed to look up period")
		return
	}

	// Ensure there are employees first (TT §2.3: "Agar xodimlar bo'lmasa —
	// avval xodim qo'shish haqida xabar beradi."). Active employees
	// belong to the org via either employees.organization_id (the
	// primary org) or the employee_organizations junction (extra
	// assignments). Filter once, here, so the count and the actual
	// SELECT below agree on which rows are eligible.
	empBaseFilter := `tenant_id = $1
		  AND deleted_at IS NULL
		  AND (status IS NULL OR status IN ('active', 'working'))`
	empArgs := []interface{}{tenantID}
	if orgIDPtr != nil {
		empBaseFilter += `
		  AND (organization_id = $2
		       OR id IN (
		           SELECT employee_id FROM employee_organizations
		           WHERE tenant_id = $1 AND organization_id = $2
		       ))`
		empArgs = append(empArgs, *orgIDPtr)
	}
	var empCount int
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM employees WHERE `+empBaseFilter,
		empArgs...,
	).Scan(&empCount); err != nil {
		h.log.Error("GetOrCreateCurrentMonthPayroll: count failed", "error", err)
		empCount = 0
	}
	if empCount == 0 {
		response.BadRequest(c, "Avval xodimlarni qo'shing — qaydnoma yaratish mumkin emas")
		return
	}

	settings, err := h.getOrInitPayrollSettings(tenantID)
	if err != nil {
		response.InternalError(c, "Failed to load settings")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	periodID = uuid.New()
	now := time.Now()
	periodName := monthStart.Format("January 2006")
	_, err = tx.Exec(`
		INSERT INTO payroll_periods (
			id, tenant_id, organization_id, period_code, period_name,
			start_date, end_date, pay_date,
			status, total_gross, total_deductions, total_net, employee_count,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, 'draft', 0, 0, 0, 0, $8, $9, $9)
	`, periodID, tenantID, orgIDPtr, periodCode, periodName,
		monthStart, monthEnd, userID, now)
	if err != nil {
		h.log.Error("Auto-create payroll period failed", "error", err)
		response.InternalError(c, "Failed to create period")
		return
	}

	// Collect all eligible employees first, then insert — pq doesn't
	// allow executing new statements on a tx while rows are still
	// open on the same connection (surfaces as "unexpected Parse
	// response 'D'"). Same status + org filter as the count above so
	// the two queries can never disagree on eligibility.
	type empRow struct {
		id       uuid.UUID
		fullName string
		position string
		salary   float64
	}
	selectQuery := `
		SELECT id,
		       COALESCE(first_name, '') || ' ' || COALESCE(last_name, '') AS full_name,
		       COALESCE(job_title, ''),
		       COALESCE(base_salary, 0)
		FROM employees
		WHERE ` + empBaseFilter
	rows, err := tx.Query(selectQuery, empArgs...)
	if err != nil {
		response.InternalError(c, "Failed to load employees")
		return
	}
	var emps []empRow
	for rows.Next() {
		var e empRow
		if err := rows.Scan(&e.id, &e.fullName, &e.position, &e.salary); err != nil {
			continue
		}
		emps = append(emps, e)
	}
	rows.Close()

	// Same tax engine as CreatePayrollEntry / CalculateAllPayroll: with no
	// active tenant taxes the result is net == gross (the original TT simple
	// model); with configured taxes all three creation paths now agree.
	created := 0
	var totalGross, totalDeductions, totalNet float64
	type entryTaxes struct {
		entryID uuid.UUID
		applied []entity.PayrollEntryTax
	}
	var taxWrites []entryTaxes
	for _, e := range emps {
		advance := math.Round(e.salary * settings.AdvancePercent / 100)
		remainder := e.salary - advance

		applied, taxTotals, tErr := h.computeEmployeeTaxesForEntry(tenantID, e.salary, 0, 0, 0, nil)
		if tErr != nil {
			h.log.Warn("TT auto-create: tax compute failed, entry gets no taxes", "employee", e.id, "error", tErr)
			applied, taxTotals = nil, payrollTaxTotals{}
		}
		var incomeTax, socialSecurity, pension float64
		if len(applied) > 0 {
			incomeTax, socialSecurity, pension = legacyBucketsFromTaxes(applied)
		}
		employeeTax := taxTotals.Employee
		netSalary := e.salary - employeeTax

		entryID := uuid.New()
		_, err := tx.Exec(`
			INSERT INTO payroll_entries (
				id, tenant_id, organization_id, payroll_period_id, employee_id, employee_name,
				position_snapshot,
				base_salary, gross_salary,
				income_tax, social_security, pension, total_deductions, net_salary,
				advance_amount, remainder_amount, advance_percent_used,
				advance_paid, remainder_paid,
				payment_method, status, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $8,
				$9, $10, $11, $12, $13,
				$14, $15, $16,
				false, false,
				'bank_transfer', 'pending', $17, $17
			)
		`, entryID, tenantID, orgIDPtr, periodID, e.id, e.fullName, e.position, e.salary,
			incomeTax, socialSecurity, pension, employeeTax, netSalary,
			advance, remainder, settings.AdvancePercent, now)
		if err != nil {
			h.log.Error("Auto-create entry failed", "error", err, "employee", e.id)
			continue
		}
		created++
		totalGross += e.salary
		totalDeductions += employeeTax
		totalNet += netSalary
		if len(applied) > 0 {
			taxWrites = append(taxWrites, entryTaxes{entryID: entryID, applied: applied})
		}
	}

	// Update period totals
	_, _ = tx.Exec(`
		UPDATE payroll_periods SET total_gross = $1, total_deductions = $2, total_net = $3, employee_count = $4, updated_at = NOW()
		WHERE id = $5
	`, totalGross, totalDeductions, totalNet, created, periodID)

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
	}

	// Snapshot applied taxes (uses h.db, so after commit — entries exist now).
	for _, w := range taxWrites {
		if err := h.writePayrollEntryTaxes(tenantID, orgIDPtr, w.entryID, w.applied); err != nil {
			h.log.Error("TT auto-create: tax snapshot write failed", "entry", w.entryID, "error", err)
		}
	}

	h.respondPayrollPeriodWithEntries(c, tenantID, periodID, true)
}

func (h *Handler) respondPayrollPeriodWithEntries(c *gin.Context, tenantID, periodID uuid.UUID, created bool) {
	// Reuse the existing list logic but fetch period first.
	var p entity.PayrollPeriod
	var notes sql.NullString
	err := h.db.QueryRow(`
		SELECT id, tenant_id, period_code, period_name, start_date, end_date, pay_date,
		       status, total_gross, total_deductions, total_net, employee_count,
		       notes, created_at, updated_at
		FROM payroll_periods WHERE id = $1 AND tenant_id = $2
	`, periodID, tenantID).Scan(
		&p.ID, &p.TenantID, &p.PeriodCode, &p.PeriodName, &p.StartDate, &p.EndDate, &p.PayDate,
		&p.Status, &p.TotalGross, &p.TotalDeductions, &p.TotalNet, &p.EmployeeCount,
		&notes, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		response.InternalError(c, "Failed to load period")
		return
	}
	if notes.Valid {
		p.Notes = &notes.String
	}
	response.Success(c, gin.H{
		"period":          p.ToResponse(),
		"newly_created":   created,
	})
}

// ---------------------------------------------------------------------------
// Mark advance / remainder paid (TT §2.4)
// ---------------------------------------------------------------------------

// MarkAdvancePaid — POST /payroll/entries/:id/advance-paid
func (h *Handler) MarkAdvancePaid(c *gin.Context) {
	h.markPaidFlag(c, "advance_paid", "advance_paid_day")
}

// MarkRemainderPaid — POST /payroll/entries/:id/remainder-paid
func (h *Handler) MarkRemainderPaid(c *gin.Context) {
	h.markPaidFlag(c, "remainder_paid", "remainder_paid_day")
}

func (h *Handler) markPaidFlag(c *gin.Context, flagCol, dayCol string) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	idStr := c.Param("id")
	entryID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid entry id")
		return
	}
	var in entity.MarkPaidInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid body")
		return
	}

	// Look up the entry's period to clamp day to the period's month length,
	// plus everything the GL leg below needs.
	var periodStart time.Time
	var periodID uuid.UUID
	var periodName, employeeName, paymentMethod string
	var curPaid bool
	var advanceAmt, remainderAmt float64
	var orgIDStr sql.NullString
	err = h.db.QueryRow(fmt.Sprintf(`
		SELECT p.start_date, p.id, p.period_name, e.employee_name,
		       COALESCE(e.payment_method, 'cash'), COALESCE(e.%s, false),
		       COALESCE(e.advance_amount, 0), COALESCE(e.remainder_amount, 0), e.organization_id
		FROM payroll_entries e
		JOIN payroll_periods p ON p.id = e.payroll_period_id
		WHERE e.id = $1 AND e.tenant_id = $2
	`, flagCol), entryID, tenantID).Scan(&periodStart, &periodID, &periodName, &employeeName,
		&paymentMethod, &curPaid, &advanceAmt, &remainderAmt, &orgIDStr)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payroll entry")
		return
	}
	if err != nil {
		response.InternalError(c, "Lookup failed")
		return
	}
	var orgIDPtr *uuid.UUID
	if orgIDStr.Valid {
		if parsed, perr := uuid.Parse(orgIDStr.String); perr == nil {
			orgIDPtr = &parsed
		}
	}
	kind := "advance"
	amount := advanceAmt
	if flagCol == "remainder_paid" {
		kind = "remainder"
		amount = remainderAmt
	}

	// Month length — last day of period's month
	lastDay := periodStart.AddDate(0, 1, -periodStart.Day()+0).Day()
	// Clearer: first of period's month + 1 month - 1 day
	firstOfMonth := time.Date(periodStart.Year(), periodStart.Month(), 1, 0, 0, 0, 0, periodStart.Location())
	lastDay = firstOfMonth.AddDate(0, 1, -1).Day()

	var dayVal interface{}
	if in.Paid {
		var day int
		if in.Day != nil && *in.Day > 0 {
			day = *in.Day
		} else {
			// Default to today's day-of-month (clamped). If the current month
			// differs from the payroll period month, fall back to the period's
			// last day so the number is always valid.
			now := time.Now()
			if now.Year() == periodStart.Year() && now.Month() == periodStart.Month() {
				day = now.Day()
			} else {
				day = lastDay
			}
		}
		if day < 1 {
			day = 1
		}
		if day > lastDay {
			day = lastDay
		}
		dayVal = day
	} else {
		dayVal = nil
	}

	// Flag flip + GL leg in ONE transaction: marking a salary leg paid is a
	// cash outflow and must hit the books (same standard as expenses' /pay);
	// un-marking reverses the posted entry. A GL failure fails the request
	// and the flag stays untouched.
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("markPaidFlag: begin tx", "error", txErr)
		response.InternalError(c, "Failed to update")
		return
	}
	defer tx.Rollback()

	if in.Paid && !curPaid && amount > 0 {
		if msg := h.postTTSalaryPaymentJE(tx, tenantID, orgIDPtr, userID, entryID, periodID, kind, employeeName, paymentMethod, periodName, amount); msg != "" {
			response.BadRequest(c, msg)
			return
		}
	} else if !in.Paid && curPaid {
		if msg := h.reverseTTSalaryPaymentJE(tx, tenantID, orgIDPtr, userID, entryID, kind); msg != "" {
			response.BadRequest(c, msg)
			return
		}
	}

	query := fmt.Sprintf(`
		UPDATE payroll_entries
		SET %s = $1, %s = $2, updated_at = NOW()
		WHERE id = $3 AND tenant_id = $4
	`, flagCol, dayCol)
	_, err = tx.Exec(query, in.Paid, dayVal, entryID, tenantID)
	if err != nil {
		h.log.Error("markPaidFlag", "error", err)
		response.InternalError(c, "Failed to update")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("markPaidFlag: commit", "error", err)
		response.InternalError(c, "Failed to update")
		return
	}

	// Return the fresh row so the frontend can patch state.
	row := h.db.QueryRow(`
		SELECT COALESCE(advance_paid, false), advance_paid_day,
		       COALESCE(remainder_paid, false), remainder_paid_day
		FROM payroll_entries WHERE id = $1
	`, entryID)
	var ap, rp bool
	var apd, rpd sql.NullInt64
	_ = row.Scan(&ap, &apd, &rp, &rpd)
	out := gin.H{
		"id":             entryID,
		"advance_paid":   ap,
		"remainder_paid": rp,
	}
	if apd.Valid {
		d := int(apd.Int64)
		out["advance_paid_day"] = d
	}
	if rpd.Valid {
		d := int(rpd.Int64)
		out["remainder_paid_day"] = d
	}
	response.Success(c, out)
}

// ---------------------------------------------------------------------------
// Backup export (TT §2.8)
// ---------------------------------------------------------------------------

// ExportPayrollBackup streams a JSON dump of settings + employees + all
// payrolls (with entries). The file is suitable for archive and reimport.
func (h *Handler) ExportPayrollBackup(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	settings, _ := h.getOrInitPayrollSettings(tenantID)

	empRows, _ := h.db.Query(`
		SELECT id, COALESCE(first_name,'') || ' ' || COALESCE(last_name,'') AS full_name,
		       COALESCE(job_title,''), COALESCE(base_salary, 0)
		FROM employees WHERE tenant_id = $1
	`, tenantID)
	type empOut struct {
		ID       uuid.UUID `json:"id"`
		FullName string    `json:"full_name"`
		Position string    `json:"position"`
		Salary   float64   `json:"salary"`
	}
	var employees []empOut
	if empRows != nil {
		for empRows.Next() {
			var e empOut
			if err := empRows.Scan(&e.ID, &e.FullName, &e.Position, &e.Salary); err == nil {
				employees = append(employees, e)
			}
		}
		empRows.Close()
	}

	periodRows, _ := h.db.Query(`
		SELECT id, period_code, period_name, start_date, end_date, pay_date, status,
		       total_gross, total_net, employee_count, created_at
		FROM payroll_periods
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY start_date DESC
	`, tenantID)
	type periodOut struct {
		ID            uuid.UUID          `json:"id"`
		PeriodCode    string             `json:"period_code"`
		PeriodName    string             `json:"period_name"`
		StartDate     string             `json:"start_date"`
		EndDate       string             `json:"end_date"`
		PayDate       string             `json:"pay_date"`
		Status        string             `json:"status"`
		TotalGross    float64            `json:"total_gross"`
		TotalNet      float64            `json:"total_net"`
		EmployeeCount int                `json:"employee_count"`
		CreatedAt     time.Time          `json:"created_at"`
		Entries       []gin.H            `json:"entries"`
	}
	var periods []periodOut
	if periodRows != nil {
		for periodRows.Next() {
			var p periodOut
			var s, e, pay time.Time
			if err := periodRows.Scan(&p.ID, &p.PeriodCode, &p.PeriodName,
				&s, &e, &pay, &p.Status, &p.TotalGross, &p.TotalNet,
				&p.EmployeeCount, &p.CreatedAt); err == nil {
				p.StartDate = s.Format("2006-01-02")
				p.EndDate = e.Format("2006-01-02")
				p.PayDate = pay.Format("2006-01-02")
				periods = append(periods, p)
			}
		}
		periodRows.Close()
	}

	// Attach entries to each period
	for i := range periods {
		eRows, _ := h.db.Query(`
			SELECT id, employee_id, employee_name, COALESCE(position_snapshot,''),
			       base_salary, advance_amount, remainder_amount,
			       COALESCE(advance_paid, false), advance_paid_day,
			       COALESCE(remainder_paid, false), remainder_paid_day
			FROM payroll_entries
			WHERE payroll_period_id = $1 AND tenant_id = $2
			ORDER BY employee_name
		`, periods[i].ID, tenantID)
		if eRows == nil {
			continue
		}
		for eRows.Next() {
			var id, empID uuid.UUID
			var name, pos string
			var salary, adv, rem float64
			var ap, rp bool
			var apd, rpd sql.NullInt64
			if err := eRows.Scan(&id, &empID, &name, &pos, &salary, &adv, &rem,
				&ap, &apd, &rp, &rpd); err == nil {
				row := gin.H{
					"id":                 id,
					"employee_id":        empID,
					"employee_name":      name,
					"position_snapshot":  pos,
					"salary":             salary,
					"advance_amount":     adv,
					"remainder_amount":   rem,
					"advance_paid":       ap,
					"remainder_paid":     rp,
				}
				if apd.Valid {
					row["advance_paid_day"] = int(apd.Int64)
				}
				if rpd.Valid {
					row["remainder_paid_day"] = int(rpd.Int64)
				}
				periods[i].Entries = append(periods[i].Entries, row)
			}
		}
		eRows.Close()
	}

	payload := gin.H{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"settings":    settings,
		"employees":   employees,
		"periods":     periods,
	}

	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		response.InternalError(c, "Failed to serialize export")
		return
	}

	filename := fmt.Sprintf("payroll_backup_%s.json", time.Now().Format("20060102_150405"))
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write(body)
}

// ---------------------------------------------------------------------------
// GL wiring for TT advance/remainder payments
// ---------------------------------------------------------------------------

// postTTSalaryPaymentJE posts the cash leg for a TT advance/remainder payment
// inside the caller's transaction. Returns a user-facing error message, or ""
// on success.
//
// Debit side depends on whether the period was processed (accrued):
//   - accrual JE exists  -> Dt 6710 Wages Payable (liability cleared)
//   - no accrual (pure TT simple mode) -> Dt 9420 Salary Expense (cash-basis)
//
// Credit side: 5110 bank for card/bank_transfer, else 5010 kassa. Same
// insufficient-balance guard as expenses' /pay.
func (h *Handler) postTTSalaryPaymentJE(tx *sql.Tx, tenantID uuid.UUID, orgIDPtr *uuid.UUID, userID uuid.UUID, entryID, periodID uuid.UUID, kind, employeeName, paymentMethod, periodName string, amount float64) string {
	// Idempotency: one active (non-reversed) JE per entry+leg.
	var active int
	_ = tx.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE tenant_id = $1 AND source_type = 'payroll_payment' AND source_id = $2
		  AND reference = $3 AND status = 'posted' AND reversed_entry_id IS NULL AND deleted_at IS NULL
	`, tenantID, entryID.String(), kind).Scan(&active)
	if active > 0 {
		return ""
	}

	// Debit account: 6710 if the period is accrued, else 9420 (simple mode).
	var accrued int
	_ = tx.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE tenant_id = $1 AND source_type = 'payroll' AND source_id = $2 AND status = 'posted' AND deleted_at IS NULL
	`, tenantID, periodID.String).Scan(&accrued)

	var debitAcct uuid.UUID
	var debitDesc string
	if accrued > 0 {
		debitAcct = findAccount(tx, tenantID, orgIDPtr, "wages payable", "6710")
		debitDesc = "Wages Payable"
	} else {
		debitAcct = findAccount(tx, tenantID, orgIDPtr, "salaries", "9420")
		if debitAcct == uuid.Nil {
			debitAcct = findAccount(tx, tenantID, orgIDPtr, "salary", "9420")
		}
		debitDesc = "Salary Expense"
	}
	if debitAcct == uuid.Nil {
		return "Ish haqi schyoti topilmadi — hisoblar rejasini tekshiring (9420/6710)"
	}

	var creditAcct uuid.UUID
	var creditDesc string
	if paymentMethod == "card" || paymentMethod == "bank_transfer" {
		creditAcct = findAccount(tx, tenantID, orgIDPtr, "bank account", "5110")
		creditDesc = "Bank Account"
	} else {
		creditAcct = findAccount(tx, tenantID, orgIDPtr, "cash", "5010")
		if creditAcct == uuid.Nil {
			creditAcct = findAccount(tx, tenantID, orgIDPtr, "kassa", "5010")
		}
		creditDesc = "Cash"
	}
	if creditAcct == uuid.Nil {
		return "To'lov hisobi topilmadi — kassa/bank schyotini sozlang (5010/5110)"
	}

	// Balance guard (same standard as expenses' /pay).
	var balance float64
	_ = tx.QueryRow(`SELECT COALESCE(current_balance, 0) FROM accounts WHERE id = $1`, creditAcct).Scan(&balance)
	if balance < amount {
		return fmt.Sprintf("Hisobda mablag' yetarli emas: mavjud %.0f, to'lov %.0f", balance, amount)
	}

	var journalID uuid.UUID
	var nextNumber int
	_ = tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1) FROM journals
		WHERE tenant_id = $1 AND COALESCE(is_payroll_journal, false) = true
		  AND COALESCE(is_active, true) = true AND deleted_at IS NULL
		LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)
	if journalID == uuid.Nil {
		_ = tx.QueryRow(`
			SELECT id, COALESCE(next_number, 1) FROM journals
			WHERE tenant_id = $1 AND code IN ('PAYROLL','MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE code WHEN 'PAYROLL' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNumber)
	}
	if journalID == uuid.Nil {
		return "Jurnal topilmadi — PAYROLL jurnalini sozlang"
	}

	now := time.Now()
	jeID := uuid.New()
	var orgVal interface{}
	if orgIDPtr != nil {
		orgVal = *orgIDPtr
	}
	entryNumber := fmt.Sprintf("PAYPMT%05d", nextEntryNumberSeq(tx, tenantID, orgVal, "PAYPMT", nextNumber))
	kindLabel := "avans"
	if kind == "remainder" {
		kindLabel = "qoldiq"
	}
	description := fmt.Sprintf("Ish haqi to'lovi (%s): %s — %s", kindLabel, employeeName, periodName)

	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date,
			reference, description, source_type, source_id, exchange_rate,
			total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'payroll_payment',$9,1.0,$10,$10,'posted',$11,$12,$12)`,
		jeID, tenantID, orgIDPtr, journalID, entryNumber, now,
		kind, description, entryID.String(), amount, userID, now); err != nil {
		h.log.Error("postTTSalaryPaymentJE: header insert", "error", err)
		return "To'lov yozuvini yaratib bo'lmadi"
	}

	// Resolve employee_id for the 6710 subkonto requirement (TT §4.5).
	var employeeID uuid.UUID
	_ = tx.QueryRow(`SELECT employee_id FROM payroll_entries WHERE id = $1 AND tenant_id = $2`, entryID, tenantID).Scan(&employeeID)
	var empVal interface{}
	if employeeID != uuid.Nil {
		empVal = employeeID
	}

	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, employee_id, created_at)
		VALUES ($1,$2,$3,$4,$5,0,1,$6,$7)`,
		uuid.New(), jeID, debitAcct, debitDesc, amount, empVal, now); err != nil {
		h.log.Error("postTTSalaryPaymentJE: debit line", "error", err)
		return "To'lov yozuvini yaratib bo'lmadi"
	}
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, amount, now, debitAcct); err != nil {
		h.log.Error("postTTSalaryPaymentJE: debit balance", "error", err)
		return "To'lov yozuvini yaratib bo'lmadi"
	}

	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, employee_id, created_at)
		VALUES ($1,$2,$3,$4,0,$5,2,$6,$7)`,
		uuid.New(), jeID, creditAcct, creditDesc, amount, empVal, now); err != nil {
		h.log.Error("postTTSalaryPaymentJE: credit line", "error", err)
		return "To'lov yozuvini yaratib bo'lmadi"
	}
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, amount, now, creditAcct); err != nil {
		h.log.Error("postTTSalaryPaymentJE: credit balance", "error", err)
		return "To'lov yozuvini yaratib bo'lmadi"
	}

	if _, err := tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID); err != nil {
		h.log.Error("postTTSalaryPaymentJE: bump journal", "error", err)
		return "To'lov yozuvini yaratib bo'lmadi"
	}
	return ""
}

// reverseTTSalaryPaymentJE reverses the active payment JE for an entry+leg
// when the paid flag is un-checked. Posts an immediate posted reversal (swap
// debit/credit) and links it via reversed_entry_id. No active JE -> no-op
// (legacy rows marked paid before GL wiring existed).
func (h *Handler) reverseTTSalaryPaymentJE(tx *sql.Tx, tenantID uuid.UUID, orgIDPtr *uuid.UUID, userID uuid.UUID, entryID uuid.UUID, kind string) string {
	var origID, journalID uuid.UUID
	var origNumber string
	err := tx.QueryRow(`
		SELECT id, entry_number, journal_id FROM journal_entries
		WHERE tenant_id = $1 AND source_type = 'payroll_payment' AND source_id = $2
		  AND reference = $3 AND status = 'posted' AND reversed_entry_id IS NULL AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, tenantID, entryID.String(), kind).Scan(&origID, &origNumber, &journalID)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		h.log.Error("reverseTTSalaryPaymentJE: lookup", "error", err)
		return "To'lov yozuvini topib bo'lmadi"
	}

	now := time.Now()
	reversalID := uuid.New()
	var orgVal interface{}
	if orgIDPtr != nil {
		orgVal = *orgIDPtr
	}
	var nextNumber int
	_ = tx.QueryRow(`SELECT COALESCE(next_number, 1) FROM journals WHERE id = $1`, journalID).Scan(&nextNumber)
	reversalNumber := fmt.Sprintf("PAYPMT%05d", nextEntryNumberSeq(tx, tenantID, orgVal, "PAYPMT", nextNumber))

	var totalDebit, totalCredit float64
	_ = tx.QueryRow(`SELECT total_debit, total_credit FROM journal_entries WHERE id = $1`, origID).Scan(&totalDebit, &totalCredit)

	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date,
			reference, description, source_type, source_id, exchange_rate,
			total_debit, total_credit, status, is_reversal, reversal_of_id,
			created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'reversal',$9,1.0,$10,$11,'posted',true,$12,$13,$14,$14)`,
		reversalID, tenantID, orgIDPtr, journalID, reversalNumber, now,
		"REV-"+origNumber, "Teskari: "+origNumber, entryID.String(),
		totalCredit, totalDebit, origID, userID, now); err != nil {
		h.log.Error("reverseTTSalaryPaymentJE: header insert", "error", err)
		return "Teskari yozuvni yaratib bo'lmadi"
	}

	rows, err := tx.Query(`
		SELECT account_id, description, debit_amount, credit_amount, employee_id
		FROM journal_entry_lines WHERE journal_entry_id = $1 ORDER BY line_number`, origID)
	if err != nil {
		h.log.Error("reverseTTSalaryPaymentJE: lines query", "error", err)
		return "Teskari yozuvni yaratib bo'lmadi"
	}
	type revLine struct {
		accountID uuid.UUID
		desc      sql.NullString
		debit     float64
		credit    float64
		empID     sql.NullString
	}
	var origLines []revLine
	for rows.Next() {
		var l revLine
		if err := rows.Scan(&l.accountID, &l.desc, &l.debit, &l.credit, &l.empID); err == nil {
			origLines = append(origLines, l)
		}
	}
	rows.Close()

	for i, l := range origLines {
		var empVal interface{}
		if l.empID.Valid {
			empVal = l.empID.String
		}
		// Swap debit and credit
		if _, err := tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, employee_id, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			uuid.New(), reversalID, l.accountID, l.desc.String, l.credit, l.debit, i+1, empVal, now); err != nil {
			h.log.Error("reverseTTSalaryPaymentJE: line insert", "error", err)
			return "Teskari yozuvni yaratib bo'lmadi"
		}
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, l.credit-l.debit, now, l.accountID); err != nil {
			h.log.Error("reverseTTSalaryPaymentJE: balance", "error", err)
			return "Teskari yozuvni yaratib bo'lmadi"
		}
	}

	if _, err := tx.Exec(`UPDATE journal_entries SET reversed_entry_id = $1, updated_at = $2 WHERE id = $3`, reversalID, now, origID); err != nil {
		h.log.Error("reverseTTSalaryPaymentJE: mark reversed", "error", err)
		return "Teskari yozuvni yaratib bo'lmadi"
	}
	if _, err := tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID); err != nil {
		h.log.Error("reverseTTSalaryPaymentJE: bump journal", "error", err)
		return "Teskari yozuvni yaratib bo'lmadi"
	}

	// Counter-reclass (moliya audit §2.3). If this payment debited 9420
	// (unaccrued avans) and the period was accrued AFTERWARDS, ProcessPayroll
	// posted a reclass (Dt 6710 / Kt 9420) that included this payment's
	// amount. Reversing only the payment would leave that reclass dangling —
	// expense understated by the avans amount. Post a proportional counter
	// entry to keep the reclass in step with the payments that still exist.
	var was9420 bool
	for _, l := range origLines {
		var code string
		_ = tx.QueryRow(`SELECT code FROM accounts WHERE id = $1`, l.accountID).Scan(&code)
		if l.debit > 0 && code == "9420" {
			was9420 = true
			break
		}
	}
	if was9420 {
		var periodID sql.NullString
		_ = tx.QueryRow(`SELECT payroll_period_id::text FROM payroll_entries WHERE id = $1 AND tenant_id = $2`, entryID, tenantID).Scan(&periodID)
		if periodID.Valid {
			var reclassID uuid.UUID
			var payableAcct, salaryAcct uuid.UUID
			err := tx.QueryRow(`
				SELECT je.id,
				       (SELECT l.account_id FROM journal_entry_lines l WHERE l.journal_entry_id = je.id AND l.debit_amount > 0 LIMIT 1),
				       (SELECT l.account_id FROM journal_entry_lines l WHERE l.journal_entry_id = je.id AND l.credit_amount > 0 LIMIT 1)
				FROM journal_entries je
				WHERE je.tenant_id = $1 AND je.source_type = 'payroll_avans_reclass' AND je.source_id = $2
				  AND je.status = 'posted' AND je.deleted_at IS NULL
				ORDER BY je.created_at DESC LIMIT 1`, tenantID, periodID.String).Scan(&reclassID, &payableAcct, &salaryAcct)
			if err == nil && payableAcct != uuid.Nil && salaryAcct != uuid.Nil {
				counterID := uuid.New()
				counterNumber := fmt.Sprintf("PAYRCL%05d", nextEntryNumberSeq(tx, tenantID, orgVal, "PAYRCL", 1))
				if _, err := tx.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number, entry_date,
						reference, description, source_type, source_id, exchange_rate,
						total_debit, total_credit, status, created_by, created_at, updated_at
					) VALUES ($1,$2,$3,$4,$5,$6,'counter',$7,'payroll_avans_reclass',$8,1.0,$9,$9,'posted',$10,$11,$11)`,
					counterID, tenantID, orgIDPtr, journalID, counterNumber, now,
					"Avans reklass teskarisi: "+origNumber, periodID.String, totalDebit, userID, now); err != nil {
					h.log.Error("reverseTTSalaryPaymentJE: counter-reclass header", "error", err)
					return "Teskari yozuvni yaratib bo'lmadi"
				}
				if _, err := tx.Exec(`
					INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
					VALUES ($1,$2,$3,'Salary Expense (reclass restored)',$4,0,1,$5)`,
					uuid.New(), counterID, salaryAcct, totalDebit, now); err != nil {
					h.log.Error("reverseTTSalaryPaymentJE: counter-reclass debit", "error", err)
					return "Teskari yozuvni yaratib bo'lmadi"
				}
				if _, err := tx.Exec(`
					INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
					VALUES ($1,$2,$3,'Wages Payable (reclass restored)',0,$4,2,$5)`,
					uuid.New(), counterID, payableAcct, totalDebit, now); err != nil {
					h.log.Error("reverseTTSalaryPaymentJE: counter-reclass credit", "error", err)
					return "Teskari yozuvni yaratib bo'lmadi"
				}
				if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalDebit, now, salaryAcct); err != nil {
					h.log.Error("reverseTTSalaryPaymentJE: counter-reclass salary balance", "error", err)
					return "Teskari yozuvni yaratib bo'lmadi"
				}
				if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalDebit, now, payableAcct); err != nil {
					h.log.Error("reverseTTSalaryPaymentJE: counter-reclass payable balance", "error", err)
					return "Teskari yozuvni yaratib bo'lmadi"
				}
			}
		}
	}
	return ""
}
