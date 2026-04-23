package handler

import (
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Monthly payroll vedomost  (TZ §7.4 + §10 report #1 — "Маош ведомости")
//
// Returns a pivoted, ready-to-export view of a single payroll period:
//
//   {
//     "period":      { id, code, name, start_date, end_date, status },
//     "tax_columns": [ { code, name, rate, payer, base_type }, … ],
//     "rows": [
//       { entry_id, employee_name, position,
//         base_salary, overtime_amount, bonus, allowances,
//         fot (= gross_salary),
//         total_employee_tax, total_employer_tax,
//         netto, employer_cost,
//         taxes: { NDFL: 2400000, PROFSOYUZ: 200000, … }
//       }, …
//     ],
//     "totals": { fot, total_employee_tax, total_employer_tax, netto,
//                 employer_cost, taxes: { NDFL: …, … } }
//   }
//
// Tax columns are discovered from whatever payroll_entry_taxes snapshots
// actually exist for this period (one DISTINCT query), so the vedomost
// naturally follows whichever tax rows the admin enabled in Settings →
// Finance — no hardcoded list of NDFL/Profsoyuz/INPS/SOC_TAX here.
//
// Route: GET /payroll-periods/:id/vedomost
// ─────────────────────────────────────────────────────────────────────────────
func (h *Handler) GetPayrollVedomost(c *gin.Context) {
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

	// ───────── 1. Period header ─────────
	type periodHeader struct {
		ID        uuid.UUID `json:"id"`
		Code      string    `json:"code"`
		Name      string    `json:"name"`
		StartDate string    `json:"start_date"`
		EndDate   string    `json:"end_date"`
		PayDate   *string   `json:"pay_date,omitempty"`
		Status    string    `json:"status"`
	}
	var ph periodHeader
	var payDate *string
	err = h.db.QueryRow(`
		SELECT id, period_code, period_name, start_date, end_date, pay_date, status
		FROM payroll_periods
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, periodID, tenantID).Scan(&ph.ID, &ph.Code, &ph.Name, &ph.StartDate, &ph.EndDate, &payDate, &ph.Status)
	if err != nil {
		h.log.Error("Failed to load payroll period", "error", err)
		response.NotFound(c, "Payroll period")
		return
	}
	ph.PayDate = payDate

	// ───────── 2. Discover the tax columns applied in this period ─────────
	type taxCol struct {
		Code     string  `json:"code"`
		Name     string  `json:"name"`
		Rate     float64 `json:"rate"`
		Payer    string  `json:"payer"`
		BaseType string  `json:"base_type"`
	}
	colRows, err := h.db.Query(`
		SELECT DISTINCT ON (pet.tax_code_snapshot)
		       pet.tax_code_snapshot,
		       pet.tax_name_snapshot,
		       pet.rate_snapshot,
		       pet.payer_snapshot,
		       pet.base_type_snapshot
		FROM payroll_entry_taxes pet
		JOIN payroll_entries pe ON pe.id = pet.payroll_entry_id
		WHERE pe.payroll_period_id = $1 AND pet.tenant_id = $2
		ORDER BY pet.tax_code_snapshot,
		         CASE pet.payer_snapshot WHEN 'employee' THEN 0 ELSE 1 END
	`, periodID, tenantID)
	if err != nil {
		h.log.Error("Failed to discover vedomost tax columns", "error", err)
		response.InternalError(c, "Failed to build vedomost")
		return
	}
	columns := make([]taxCol, 0)
	for colRows.Next() {
		var c0 taxCol
		if err := colRows.Scan(&c0.Code, &c0.Name, &c0.Rate, &c0.Payer, &c0.BaseType); err == nil {
			columns = append(columns, c0)
		}
	}
	colRows.Close()

	// ───────── 3. One row per payroll entry with its tax breakdown ─────────
	// We do the N:1 fan-out in one query by LEFT-JOINing entries with their
	// tax snapshots and bucket into a map keyed by (entry_id, tax_code).
	type entryRow struct {
		EntryID          uuid.UUID          `json:"entry_id"`
		EmployeeName     string             `json:"employee_name"`
		Position         string             `json:"position"`
		BaseSalary       float64            `json:"base_salary"`
		OvertimeAmount   float64            `json:"overtime_amount"`
		Bonus            float64            `json:"bonus"`
		Allowances       float64            `json:"allowances"`
		FOT              float64            `json:"fot"`
		TotalEmployeeTax float64            `json:"total_employee_tax"`
		TotalEmployerTax float64            `json:"total_employer_tax"`
		Netto            float64            `json:"netto"`
		EmployerCost     float64            `json:"employer_cost"`
		Status           string             `json:"status"`
		Taxes            map[string]float64 `json:"taxes"`
	}
	entryRows, err := h.db.Query(`
		SELECT pe.id, pe.employee_name, COALESCE(pe.position_snapshot, ''),
		       pe.base_salary, COALESCE(pe.overtime_amount, 0),
		       COALESCE(pe.bonus, 0), COALESCE(pe.allowances, 0),
		       pe.gross_salary, pe.net_salary, pe.status
		FROM payroll_entries pe
		WHERE pe.payroll_period_id = $1 AND pe.tenant_id = $2 AND pe.deleted_at IS NULL
		ORDER BY pe.employee_name
	`, periodID, tenantID)
	if err != nil {
		h.log.Error("Failed to load vedomost entries", "error", err)
		response.InternalError(c, "Failed to build vedomost")
		return
	}
	defer entryRows.Close()

	rows := make([]entryRow, 0)
	entryIndex := make(map[uuid.UUID]int) // entry_id → index into rows[]
	for entryRows.Next() {
		var r entryRow
		if err := entryRows.Scan(
			&r.EntryID, &r.EmployeeName, &r.Position,
			&r.BaseSalary, &r.OvertimeAmount, &r.Bonus, &r.Allowances,
			&r.FOT, &r.Netto, &r.Status,
		); err != nil {
			continue
		}
		r.Taxes = make(map[string]float64, len(columns))
		for _, col := range columns {
			r.Taxes[col.Code] = 0
		}
		entryIndex[r.EntryID] = len(rows)
		rows = append(rows, r)
	}

	// Fan-in the tax amounts per entry/code.
	if len(entryIndex) > 0 {
		taxRows, err := h.db.Query(`
			SELECT pet.payroll_entry_id, pet.tax_code_snapshot, pet.payer_snapshot,
			       pet.amount
			FROM payroll_entry_taxes pet
			JOIN payroll_entries pe ON pe.id = pet.payroll_entry_id
			WHERE pe.payroll_period_id = $1 AND pet.tenant_id = $2
		`, periodID, tenantID)
		if err == nil {
			for taxRows.Next() {
				var entryID uuid.UUID
				var code, payer string
				var amount float64
				if err := taxRows.Scan(&entryID, &code, &payer, &amount); err != nil {
					continue
				}
				idx, ok := entryIndex[entryID]
				if !ok {
					continue
				}
				rows[idx].Taxes[code] += amount
				if payer == "employer" {
					rows[idx].TotalEmployerTax += amount
				} else {
					rows[idx].TotalEmployeeTax += amount
				}
			}
			taxRows.Close()
		}
	}

	// Derived totals per row.
	for i := range rows {
		rows[i].EmployerCost = rows[i].FOT + rows[i].TotalEmployerTax
	}

	// ───────── 4. Period totals (sum of row values) ─────────
	type vedomostTotals struct {
		FOT              float64            `json:"fot"`
		BaseSalary       float64            `json:"base_salary"`
		OvertimeAmount   float64            `json:"overtime_amount"`
		Bonus            float64            `json:"bonus"`
		Allowances       float64            `json:"allowances"`
		TotalEmployeeTax float64            `json:"total_employee_tax"`
		TotalEmployerTax float64            `json:"total_employer_tax"`
		Netto            float64            `json:"netto"`
		EmployerCost     float64            `json:"employer_cost"`
		Taxes            map[string]float64 `json:"taxes"`
	}
	totals := vedomostTotals{Taxes: map[string]float64{}}
	for _, col := range columns {
		totals.Taxes[col.Code] = 0
	}
	for _, r := range rows {
		totals.FOT += r.FOT
		totals.BaseSalary += r.BaseSalary
		totals.OvertimeAmount += r.OvertimeAmount
		totals.Bonus += r.Bonus
		totals.Allowances += r.Allowances
		totals.TotalEmployeeTax += r.TotalEmployeeTax
		totals.TotalEmployerTax += r.TotalEmployerTax
		totals.Netto += r.Netto
		totals.EmployerCost += r.EmployerCost
		for code, amount := range r.Taxes {
			totals.Taxes[code] += amount
		}
	}

	response.Success(c, gin.H{
		"period":         ph,
		"tax_columns":    columns,
		"rows":           rows,
		"totals":         totals,
		"employee_count": len(rows),
	})
}
