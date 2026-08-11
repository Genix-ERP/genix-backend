package handler

import (
	"database/sql"
	"math"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// payroll_bulk.go — bulk-calculate payroll entries for every active
// employee at once. TZ §7.1: "Бир вақтда барча ходимлар ҳисобланади
// (calculate-all)" — explicitly listed as a functional requirement, and
// the spec's API table at §9.3 names the route POST /api/payroll/calculate-all.
//
// Behaviour:
//   * Lists active employees in the tenant who have a positive base_salary
//     (FOT). Inactive / FOT-zero employees are skipped — adding a zero
//     entry would just clutter the period.
//   * For each, ONLY creates an entry if one doesn't already exist in this
//     period (idempotent: re-clicking the button doesn't overwrite manual
//     edits made to existing entries).
//   * Each entry is computed via the SAME helper that the per-employee
//     CreatePayrollEntry uses (computeEmployeeTaxesForEntry), so all
//     payroll-entry-tax snapshot rows are written too — keeps the tax
//     report aggregator consistent.
//
// Route: POST /payroll-periods/:id/calculate-all

// CalculateAllPayroll bulk-creates payroll entries for the given period.
func (h *Handler) CalculateAllPayroll(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payroll period ID")
		return
	}

	// Verify the period exists and is editable. payroll_periods.status
	// is one of: draft, processing, approved, paid, cancelled. Bulk
	// calculation is only allowed for draft and processing — once a period
	// is approved/paid the entries are locked.
	var status sql.NullString
	var periodOrgStr sql.NullString
	if err := h.db.QueryRow(`
		SELECT COALESCE(status, 'draft'), organization_id
		FROM payroll_periods
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, periodID, tenantID).Scan(&status, &periodOrgStr); err != nil {
		response.NotFound(c, "Payroll period not found")
		return
	}
	st := strings.ToLower(strings.TrimSpace(status.String))
	if st == "approved" || st == "paid" || st == "cancelled" {
		response.BadRequest(c, "Period is already approved/paid; cannot recalculate")
		return
	}

	// The period's own organization drives both the employee filter and the
	// org stamped on created entries — in a multi-company tenant the caller's
	// active org may differ from the period's.
	if periodOrgStr.Valid {
		if parsed, perr := uuid.Parse(periodOrgStr.String); perr == nil {
			orgID = parsed
		}
	}

	// Pull every active employee with a positive base_salary. Column
	// names match the existing per-entry handler (CreatePayrollEntry):
	// `base_salary` (numeric), `job_title` (text snapshot), and the
	// status filter `status='active'`.
	type empRow struct {
		ID         uuid.UUID
		FirstName  string
		LastName   string
		JobTitle   sql.NullString
		BaseSalary float64
	}
	// Same org filter as the TT auto-create flow: primary org column OR the
	// employee_organizations junction. Without it a multi-company tenant
	// pulls every company's employees into one company's period.
	empQuery := `
		SELECT e.id,
		       COALESCE(e.first_name, ''),
		       COALESCE(e.last_name, ''),
		       e.job_title,
		       COALESCE(e.base_salary, 0)
		FROM employees e
		WHERE e.tenant_id = $1
		  AND COALESCE(e.status, 'active') = 'active'
		  AND e.deleted_at IS NULL
		  AND COALESCE(e.base_salary, 0) > 0`
	empArgs := []interface{}{tenantID}
	if orgID != uuid.Nil {
		empQuery += `
		  AND (e.organization_id = $2
		       OR e.id IN (
		           SELECT employee_id FROM employee_organizations
		           WHERE tenant_id = $1 AND organization_id = $2
		       ))`
		empArgs = append(empArgs, orgID)
	}
	empQuery += `
		ORDER BY e.last_name, e.first_name`
	rows, err := h.db.Query(empQuery, empArgs...)
	if err != nil {
		h.log.Error("Failed to list employees for bulk payroll calc", "error", err)
		response.InternalError(c, "Failed to list employees")
		return
	}
	defer rows.Close()

	employees := []empRow{}
	for rows.Next() {
		var e empRow
		if err := rows.Scan(&e.ID, &e.FirstName, &e.LastName, &e.JobTitle, &e.BaseSalary); err != nil {
			continue
		}
		employees = append(employees, e)
	}

	// Existing entry employee_ids — used to make the operation idempotent.
	existing := map[uuid.UUID]bool{}
	exRows, _ := h.db.Query(`
		SELECT employee_id FROM payroll_entries
		WHERE payroll_period_id = $1 AND tenant_id = $2
	`, periodID, tenantID)
	if exRows != nil {
		for exRows.Next() {
			var eid uuid.UUID
			if err := exRows.Scan(&eid); err == nil {
				existing[eid] = true
			}
		}
		exRows.Close()
	}

	// Bulk loop. We bypass HTTP-handler boilerplate by going straight to
	// the helper that already powers the per-entry endpoint, so any future
	// improvements to per-entry math (rounding tweaks, new TZ tax rows)
	// flow through automatically.
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}
	now := time.Now()

	// Advance split percentage from tenant payroll settings (fallback 40).
	advancePct := 40.0
	if settings, sErr := h.getOrInitPayrollSettings(tenantID); sErr == nil && settings != nil &&
		settings.AdvancePercent > 0 && settings.AdvancePercent <= 100 {
		advancePct = settings.AdvancePercent
	}

	createdIDs := []uuid.UUID{}
	skippedExisting := 0
	failed := 0

	for _, e := range employees {
		if existing[e.ID] {
			skippedExisting++
			continue
		}

		positionSnapshot := ""
		if e.JobTitle.Valid {
			positionSnapshot = e.JobTitle.String
		}
		employeeName := strings.TrimSpace(e.FirstName + " " + e.LastName)

		// Compute taxes via the same helper CreatePayrollEntry uses.
		appliedTaxes, taxTotals, tErr := h.computeEmployeeTaxesForEntry(
			tenantID, e.BaseSalary, 0, 0, 0, nil,
		)
		if tErr != nil {
			h.log.Warn("Tax compute failed for employee — skipping", "employee_id", e.ID, "error", tErr)
			failed++
			continue
		}

		var effectiveIncomeTax, effectiveSocialSecurity, effectivePension float64
		totalEmployeeTax := taxTotals.Employee
		if len(appliedTaxes) > 0 {
			effectiveIncomeTax, effectiveSocialSecurity, effectivePension = legacyBucketsFromTaxes(appliedTaxes)
		}

		grossSalary := e.BaseSalary
		netSalary := grossSalary - totalEmployeeTax

		// TT §2.3.2 advance split from tenant settings — this path used to
		// force 0/100%, diverging from the per-entry and TT-create paths.
		advanceAmount := math.Round(e.BaseSalary * advancePct / 100)
		remainderAmount := e.BaseSalary - advanceAmount

		entryID := uuid.New()
		if _, err := h.db.Exec(`
			INSERT INTO payroll_entries (
				id, tenant_id, organization_id, payroll_period_id, employee_id, employee_name,
				position_snapshot, base_salary,
				overtime_hours, overtime_amount, bonus, allowances, gross_salary, income_tax,
				social_security, pension, other_deductions, total_deductions, net_salary,
				advance_amount, remainder_amount, advance_percent_used, advance_paid, remainder_paid,
				payment_method, status, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8,
				0, 0, 0, 0, $8, $9,
				$10, $11, 0, $12, $13,
				$14, $15, $16, FALSE, FALSE,
				'bank_transfer', 'pending', $17, $17
			)
		`, entryID, tenantID, orgIDPtr, periodID, e.ID, employeeName,
			positionSnapshot, grossSalary,
			effectiveIncomeTax, effectiveSocialSecurity, effectivePension, totalEmployeeTax, netSalary,
			advanceAmount, remainderAmount, advancePct,
			now,
		); err != nil {
			h.log.Warn("Failed to insert bulk payroll entry — skipping", "employee_id", e.ID, "error", err)
			failed++
			continue
		}

		// Persist the tax snapshot rows so the period vedomost + tax-summary
		// aggregator see this entry's per-tax breakdown.
		if err := h.writePayrollEntryTaxes(tenantID, orgIDPtr, entryID, appliedTaxes); err != nil {
			h.log.Warn("Failed to write payroll_entry_taxes snapshot", "entry_id", entryID, "error", err)
			// Don't roll back the entry — the legacy income_tax columns are
			// still populated and the report will fall back to those.
		}

		createdIDs = append(createdIDs, entryID)
		_ = userID // userID currently unused; kept for future audit fields
	}

	response.Success(c, gin.H{
		"period_id":        periodID,
		"employees_total":  len(employees),
		"created":          len(createdIDs),
		"skipped_existing": skippedExisting,
		"failed":           failed,
		"entry_ids":        createdIDs,
	})
}
