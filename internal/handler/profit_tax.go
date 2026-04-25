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

// ─────────────────────────────────────────────────────────────────────────────
// Profit-tax calculation
//
// Two endpoints:
//   GET  /profit-tax?period_type=month|quarter|year&period_key=YYYY-MM|YYYY-Qn|YYYY
//                  &income=<optional_override>
//     → recomputes from live expenses, returns ProfitTaxCalcResult.
//
//   POST /profit-tax/snapshot   body: same params + optional notes
//     → persists the current result into profit_tax_calc, upserting on
//       (tenant_id, period_type, period_key).
//
//   GET  /profit-tax/snapshots?year=YYYY
//     → lists persisted snapshots for the audit trail.
//
// Formula (see §6.4 of ТЗ_Ish_Haqi_Soliq_Tolik.docx):
//     accounting_profit = income − (recognized + unrecognized)
//     tax_base          = income − recognized          ← key difference!
//     tax_amount        = tax_base × rate/100
//     net_profit        = accounting_profit − tax_amount
// ─────────────────────────────────────────────────────────────────────────────

const defaultProfitTaxRatePct = 15.0 // §5 of the TZ

// parsePeriod resolves (period_type, period_key) into a concrete date range
// (both ends inclusive). Returns an error for malformed input.
func parseProfitTaxPeriod(periodType, periodKey string) (time.Time, time.Time, error) {
	switch periodType {
	case "month":
		// YYYY-MM
		t, err := time.Parse("2006-01", periodKey)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("month period_key must be YYYY-MM, got %q", periodKey)
		}
		start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, -1)
		return start, end, nil
	case "quarter":
		// YYYY-Qn, n ∈ 1..4
		parts := strings.Split(strings.ToUpper(periodKey), "-Q")
		if len(parts) != 2 {
			return time.Time{}, time.Time{}, fmt.Errorf("quarter period_key must be YYYY-Qn, got %q", periodKey)
		}
		year, err := strconv.Atoi(parts[0])
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid year in %q", periodKey)
		}
		q, err := strconv.Atoi(parts[1])
		if err != nil || q < 1 || q > 4 {
			return time.Time{}, time.Time{}, fmt.Errorf("quarter must be 1..4, got %q", periodKey)
		}
		startMonth := time.Month((q-1)*3 + 1)
		start := time.Date(year, startMonth, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 3, -1)
		return start, end, nil
	case "year":
		// YYYY
		year, err := strconv.Atoi(periodKey)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("year period_key must be YYYY, got %q", periodKey)
		}
		start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
		return start, end, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("period_type must be month|quarter|year, got %q", periodType)
	}
}

// round2 rounds to two decimal places — all money values are reported at
// this precision to match the rest of the payroll module.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// computeProfitTaxExpenses sums recognized and unrecognized expenses for
// the given tenant + period + optional organization filter.
func (h *Handler) computeProfitTaxExpenses(tenantID uuid.UUID, orgID uuid.UUID, start, end time.Time) (recognized, unrecognized float64, err error) {
	// Organization scoping: optional — when orgID is uuid.Nil we sum across
	// all of the tenant's orgs, which matches the payroll/expenses list
	// behavior when no org header is set.
	args := []interface{}{tenantID, start, end}
	orgClause := ""
	if orgID != uuid.Nil {
		args = append(args, orgID)
		orgClause = " AND organization_id = $4"
	}
	q := fmt.Sprintf(`
		SELECT
		  COALESCE(SUM(CASE WHEN is_recognized      THEN total_amount ELSE 0 END), 0) AS recognized,
		  COALESCE(SUM(CASE WHEN NOT is_recognized  THEN total_amount ELSE 0 END), 0) AS unrecognized
		FROM expenses
		WHERE tenant_id   = $1
		  AND deleted_at  IS NULL
		  AND expense_date BETWEEN $2 AND $3
		  %s
	`, orgClause)
	if err := h.db.QueryRow(q, args...).Scan(&recognized, &unrecognized); err != nil {
		return 0, 0, err
	}
	return recognized, unrecognized, nil
}

// lookupProfitTaxSnapshot returns the existing snapshot id (if any) for
// a given period so GetProfitTax can set the `snapshotted` flag.
func (h *Handler) lookupProfitTaxSnapshot(tenantID uuid.UUID, periodType, periodKey string) *uuid.UUID {
	var id uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM profit_tax_calc
		WHERE tenant_id = $1 AND period_type = $2 AND period_key = $3
		LIMIT 1
	`, tenantID, periodType, periodKey).Scan(&id)
	if err == sql.ErrNoRows || err != nil {
		return nil
	}
	return &id
}

// GetProfitTax computes and returns the live profit-tax numbers for a
// period. Income is NOT yet wired to the revenue ledger — it must be
// passed via the ?income= query string (accountants enter the revenue
// number manually for now; a follow-up task will pull it from journal
// entries against revenue accounts).
func (h *Handler) GetProfitTax(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodType := c.DefaultQuery("period_type", "month")
	periodKey := c.Query("period_key")
	if periodKey == "" {
		// Default to current month so the UI can render something even
		// before the user has typed a period.
		periodKey = time.Now().UTC().Format("2006-01")
	}

	start, end, err := parseProfitTaxPeriod(periodType, periodKey)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	income, _ := strconv.ParseFloat(c.Query("income"), 64) // 0 if unset

	// Rate resolution order (TZ §11.9 — "Soliq stavkasi o'zgardi → barcha
	// hisoblar yangilanadi"):
	//   1. explicit ?rate= override (admin dry-run tool)
	//   2. company_tax_rates row with applies_to='profit' and is_active
	//   3. the TZ-canonical 15% default
	rate := h.getCompanyTaxRatePct(tenantID, "profit", defaultProfitTaxRatePct)
	if r, err := strconv.ParseFloat(c.Query("rate"), 64); err == nil && r > 0 {
		rate = r
	}

	orgID, _ := middleware.GetOrganizationID(c)
	recognized, unrecognized, err := h.computeProfitTaxExpenses(tenantID, orgID, start, end)
	if err != nil {
		h.log.Error("Failed to compute profit-tax expenses", "error", err)
		response.InternalError(c, "Failed to compute profit-tax")
		return
	}

	total := recognized + unrecognized
	accountingProfit := income - total
	taxBase := income - recognized // key difference — unrecognized NOT deducted
	taxAmount := round2(taxBase * rate / 100)
	netProfit := accountingProfit - taxAmount

	snap := h.lookupProfitTaxSnapshot(tenantID, periodType, periodKey)

	result := entity.ProfitTaxCalcResult{
		PeriodType:       periodType,
		PeriodKey:        periodKey,
		PeriodStart:      start.Format("2006-01-02"),
		PeriodEnd:        end.Format("2006-01-02"),
		Income:           round2(income),
		RecognizedExp:    round2(recognized),
		UnrecognizedExp:  round2(unrecognized),
		TotalExpenses:    round2(total),
		AccountingProfit: round2(accountingProfit),
		TaxBase:          round2(taxBase),
		TaxAmount:        taxAmount,
		NetProfit:        round2(netProfit),
		Rate:             rate,
		Snapshotted:      snap != nil,
		SnapshotID:       snap,
	}
	response.Success(c, result)
}

// SnapshotProfitTax freezes the current computed values into
// profit_tax_calc. Upserts on (tenant_id, period_type, period_key) — the
// admin may re-snapshot after correcting mistakes.
func (h *Handler) SnapshotProfitTax(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input struct {
		PeriodType string   `json:"period_type" binding:"required"`
		PeriodKey  string   `json:"period_key"  binding:"required"`
		Income     float64  `json:"income"`
		Rate       *float64 `json:"rate"` // nil → default 15
		Notes      *string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "period_type and period_key are required")
		return
	}

	start, end, err := parseProfitTaxPeriod(input.PeriodType, input.PeriodKey)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Same rate resolution as GetProfitTax: explicit override → settings → 15%.
	rate := h.getCompanyTaxRatePct(tenantID, "profit", defaultProfitTaxRatePct)
	if input.Rate != nil && *input.Rate > 0 {
		rate = *input.Rate
	}

	orgID, _ := middleware.GetOrganizationID(c)
	recognized, unrecognized, err := h.computeProfitTaxExpenses(tenantID, orgID, start, end)
	if err != nil {
		h.log.Error("Failed to compute profit-tax expenses", "error", err)
		response.InternalError(c, "Failed to compute profit-tax")
		return
	}

	total := recognized + unrecognized
	accountingProfit := input.Income - total
	taxBase := input.Income - recognized
	taxAmount := round2(taxBase * rate / 100)
	netProfit := accountingProfit - taxAmount

	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		o := orgID
		orgIDPtr = &o
	}

	id := uuid.New()
	now := time.Now()
	err = h.db.QueryRow(`
		INSERT INTO profit_tax_calc (
		  id, tenant_id, organization_id,
		  period_type, period_key, period_start, period_end,
		  income, recognized_exp, unrecognized_exp,
		  accounting_profit, tax_base, tax_amount, net_profit,
		  rate_snapshot, notes, created_by, created_at, updated_at
		)
		VALUES ($1,$2,$3, $4,$5,$6,$7, $8,$9,$10, $11,$12,$13,$14, $15,$16,$17,$18,$18)
		ON CONFLICT (tenant_id, period_type, period_key)
		DO UPDATE SET
		  organization_id   = EXCLUDED.organization_id,
		  period_start      = EXCLUDED.period_start,
		  period_end        = EXCLUDED.period_end,
		  income            = EXCLUDED.income,
		  recognized_exp    = EXCLUDED.recognized_exp,
		  unrecognized_exp  = EXCLUDED.unrecognized_exp,
		  accounting_profit = EXCLUDED.accounting_profit,
		  tax_base          = EXCLUDED.tax_base,
		  tax_amount        = EXCLUDED.tax_amount,
		  net_profit        = EXCLUDED.net_profit,
		  rate_snapshot     = EXCLUDED.rate_snapshot,
		  notes             = EXCLUDED.notes,
		  updated_at        = EXCLUDED.updated_at
		RETURNING id
	`,
		id, tenantID, orgIDPtr,
		input.PeriodType, input.PeriodKey, start, end,
		round2(input.Income), round2(recognized), round2(unrecognized),
		round2(accountingProfit), round2(taxBase), taxAmount, round2(netProfit),
		rate, input.Notes, userID, now,
	).Scan(&id)
	if err != nil {
		h.log.Error("Failed to snapshot profit-tax", "error", err)
		response.InternalError(c, "Failed to snapshot profit-tax")
		return
	}

	response.Success(c, gin.H{
		"id":                id,
		"period_type":       input.PeriodType,
		"period_key":        input.PeriodKey,
		"income":            round2(input.Income),
		"recognized_exp":    round2(recognized),
		"unrecognized_exp":  round2(unrecognized),
		"accounting_profit": round2(accountingProfit),
		"tax_base":          round2(taxBase),
		"tax_amount":        taxAmount,
		"net_profit":        round2(netProfit),
		"rate_snapshot":     rate,
	})
}

// GetProfitTaxRevenue auto-calculates a period's revenue from the
// general ledger — sum(credit_amount − debit_amount) across all posted
// journal_entry_lines that hit a revenue-category account within the
// period window. This replaces the manual income entry on the Profit
// Tax page (§6.1 of ТЗ_Ish_Haqi_Soliq_Tolik.docx) when the accountant
// wants to pull the number straight from the books.
//
// GET /profit-tax/revenue?period_type=month|quarter|year&period_key=YYYY-MM|…
func (h *Handler) GetProfitTaxRevenue(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodType := c.DefaultQuery("period_type", "month")
	periodKey := c.Query("period_key")
	if periodKey == "" {
		periodKey = time.Now().UTC().Format("2006-01")
	}

	start, end, err := parseProfitTaxPeriod(periodType, periodKey)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Revenue = credits net of debits on 'revenue'-category accounts for
	// posted journal entries in the window. This mirrors how the finance
	// module computes income elsewhere; we keep the join through
	// account_types so the query honours however the tenant labelled
	// their chart of accounts.
	var income float64
	var entryCount int64
	err = h.db.QueryRow(`
		SELECT
		  COALESCE(SUM(jel.credit_amount) - SUM(jel.debit_amount), 0) AS income,
		  COUNT(*)                                                    AS n
		FROM journal_entry_lines jel
		JOIN journal_entries je    ON je.id  = jel.journal_entry_id
		JOIN accounts a            ON a.id   = jel.account_id
		JOIN account_types at      ON at.id  = a.account_type_id
		WHERE je.tenant_id    = $1
		  AND at.category     = 'revenue'
		  AND je.status       = 'posted'
		  AND je.deleted_at   IS NULL
		  AND je.entry_date  >= $2::date
		  AND je.entry_date  <= $3::date
	`, tenantID, start.Format("2006-01-02"), end.Format("2006-01-02")).Scan(&income, &entryCount)
	if err != nil {
		h.log.Error("Failed to compute profit-tax revenue", "error", err)
		response.InternalError(c, "Failed to compute revenue")
		return
	}

	response.Success(c, gin.H{
		"period_type":  periodType,
		"period_key":   periodKey,
		"period_start": start.Format("2006-01-02"),
		"period_end":   end.Format("2006-01-02"),
		"income":       round2(income),
		"source":       "ledger",
		"entry_count":  entryCount,
	})
}

// ListProfitTaxSnapshots returns persisted snapshots for a year.
// GET /profit-tax/snapshots?year=YYYY
func (h *Handler) ListProfitTaxSnapshots(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	year := c.Query("year")
	args := []interface{}{tenantID}
	yearClause := ""
	if year != "" {
		// Match on leading YYYY of period_key — works for '2026-04',
		// '2026-Q2', and '2026' alike.
		args = append(args, year+"%")
		yearClause = " AND period_key LIKE $2"
	}

	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT id, period_type, period_key, period_start, period_end,
		       income, recognized_exp, unrecognized_exp,
		       accounting_profit, tax_base, tax_amount, net_profit,
		       rate_snapshot, COALESCE(notes, ''), created_at, updated_at
		FROM profit_tax_calc
		WHERE tenant_id = $1
		%s
		ORDER BY period_start DESC
	`, yearClause), args...)
	if err != nil {
		h.log.Error("Failed to list profit-tax snapshots", "error", err)
		response.InternalError(c, "Failed to list snapshots")
		return
	}
	defer rows.Close()

	type row struct {
		ID               uuid.UUID `json:"id"`
		PeriodType       string    `json:"period_type"`
		PeriodKey        string    `json:"period_key"`
		PeriodStart      string    `json:"period_start"`
		PeriodEnd        string    `json:"period_end"`
		Income           float64   `json:"income"`
		RecognizedExp    float64   `json:"recognized_exp"`
		UnrecognizedExp  float64   `json:"unrecognized_exp"`
		AccountingProfit float64   `json:"accounting_profit"`
		TaxBase          float64   `json:"tax_base"`
		TaxAmount        float64   `json:"tax_amount"`
		NetProfit        float64   `json:"net_profit"`
		Rate             float64   `json:"rate_snapshot"`
		Notes            string    `json:"notes"`
		CreatedAt        time.Time `json:"created_at"`
		UpdatedAt        time.Time `json:"updated_at"`
	}

	out := make([]row, 0)
	for rows.Next() {
		var r row
		var ps, pe time.Time
		if err := rows.Scan(
			&r.ID, &r.PeriodType, &r.PeriodKey, &ps, &pe,
			&r.Income, &r.RecognizedExp, &r.UnrecognizedExp,
			&r.AccountingProfit, &r.TaxBase, &r.TaxAmount, &r.NetProfit,
			&r.Rate, &r.Notes, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			h.log.Error("Failed to scan profit-tax snapshot", "error", err)
			continue
		}
		r.PeriodStart = ps.Format("2006-01-02")
		r.PeriodEnd = pe.Format("2006-01-02")
		out = append(out, r)
	}
	response.Success(c, out)
}
