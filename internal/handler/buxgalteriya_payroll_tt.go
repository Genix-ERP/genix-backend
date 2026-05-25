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

	created := 0
	var totalSalary float64
	for _, e := range emps {
		advance := math.Round(e.salary * settings.AdvancePercent / 100)
		remainder := e.salary - advance

		_, err := tx.Exec(`
			INSERT INTO payroll_entries (
				id, tenant_id, organization_id, payroll_period_id, employee_id, employee_name,
				position_snapshot,
				base_salary, gross_salary, net_salary,
				advance_amount, remainder_amount, advance_percent_used,
				advance_paid, remainder_paid,
				payment_method, status, created_at, updated_at
			) VALUES (
				gen_random_uuid(), $1, $2, $3, $4, $5,
				$6, $7, $7, $7, $8, $9, $10,
				false, false,
				'bank_transfer', 'pending', $11, $11
			)
		`, tenantID, orgIDPtr, periodID, e.id, e.fullName, e.position, e.salary,
			advance, remainder, settings.AdvancePercent, now)
		if err != nil {
			h.log.Error("Auto-create entry failed", "error", err, "employee", e.id)
			continue
		}
		created++
		totalSalary += e.salary
	}

	// Update period totals
	_, _ = tx.Exec(`
		UPDATE payroll_periods SET total_gross = $1, total_net = $1, employee_count = $2, updated_at = NOW()
		WHERE id = $3
	`, totalSalary, created, periodID)

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
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

	// Look up the entry's period to clamp day to the period's month length.
	var periodStart time.Time
	err = h.db.QueryRow(`
		SELECT p.start_date FROM payroll_entries e
		JOIN payroll_periods p ON p.id = e.payroll_period_id
		WHERE e.id = $1 AND e.tenant_id = $2
	`, entryID, tenantID).Scan(&periodStart)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payroll entry")
		return
	}
	if err != nil {
		response.InternalError(c, "Lookup failed")
		return
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

	query := fmt.Sprintf(`
		UPDATE payroll_entries
		SET %s = $1, %s = $2, updated_at = NOW()
		WHERE id = $3 AND tenant_id = $4
	`, flagCol, dayCol)
	_, err = h.db.Exec(query, in.Paid, dayVal, entryID, tenantID)
	if err != nil {
		h.log.Error("markPaidFlag", "error", err)
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
