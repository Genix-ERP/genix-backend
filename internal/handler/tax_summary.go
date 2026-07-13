package handler

import (
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// tax_summary.go — Umumiy soliq jamlanmasi (§10 of ТЗ_Ish_Haqi_Soliq_Tolik.docx).
//
// Single aggregator endpoint that returns the 8 TZ-listed taxes in one
// shot for a given period, so the director-level "all taxes this month"
// dashboard doesn't need to fan out 5 separate frontend calls.
//
// Groups (TZ §1.2):
//   Удержания     — NDFL 12%, Profsoyuz 1%, INPS 0.1%        (from payroll)
//   Отчисления    — ESP/SOC_TAX 12%                          (from payroll)
//   Фаолиятдан    — NDS 12%, Foyda 15%, Aylanma 4%, Dividend 5%
//                   (from the 4 calculators shipped earlier)
//
// Rate resolution everywhere goes through getCompanyTaxRatePct /
// the payroll snapshot, so changing a rate in Kompaniya soliqlari settings
// immediately moves every number on the summary page (TZ §11.9).
//
// Dividend is intentionally NOT included as an aggregate (it's a
// per-distribution number, not a period total). Its current rate is
// surfaced so the UI can render "Dividend: rate 5%, calculator →" as a
// link to the per-distribution helper.
//
// Route: GET /tax-summary/combined?period_type=month|quarter|year&period_key=...

// GetTaxSummaryCombined returns the all-in-one snapshot.
func (h *Handler) GetTaxSummaryCombined(c *gin.Context) {
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

	// Optional manual income override for the Foyda leg — when omitted,
	// we pull revenue from the ledger like GetProfitTaxRevenue does so
	// the summary stays meaningful even with zero user input.
	manualIncome, _ := strconv.ParseFloat(c.Query("income"), 64)

	// ── 1. Payroll-side withholdings + employer contributions ──
	// Sum payroll_entry_taxes (the snapshot table introduced by
	// migration 330). Grouped by tax code so the UI can label each bar.
	type payrollBucket struct {
		Code    string  `json:"code"`
		Name    string  `json:"name"`
		Rate    float64 `json:"rate"`
		Amount  float64 `json:"amount"`
		Payer   string  `json:"payer"` // "employee" | "employer"
	}
	payrollBuckets := []payrollBucket{}
	var totalEmployeeWithhold, totalEmployerContrib float64
	rows, err := h.db.Query(`
		SELECT
		  pet.tax_code_snapshot,
		  COALESCE(NULLIF(pet.tax_name_snapshot, ''), pet.tax_code_snapshot) AS name,
		  COALESCE(pet.rate_snapshot, 0),
		  COALESCE(SUM(pet.amount), 0) AS total_amount,
		  COALESCE(MIN(pet.payer_snapshot), '')
		FROM payroll_entry_taxes pet
		JOIN payroll_entries pe ON pe.id = pet.payroll_entry_id
		JOIN payroll_periods pp ON pp.id = pe.payroll_period_id
		WHERE pet.tenant_id = $1
		  AND pp.end_date   >= $2::date
		  AND pp.start_date <= $3::date
		GROUP BY pet.tax_code_snapshot, name, pet.rate_snapshot
		ORDER BY pet.tax_code_snapshot ASC
	`, tenantID, start, end)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b payrollBucket
			if err := rows.Scan(&b.Code, &b.Name, &b.Rate, &b.Amount, &b.Payer); err != nil {
				continue
			}
			if b.Payer == "employer" {
				totalEmployerContrib += b.Amount
			} else {
				totalEmployeeWithhold += b.Amount
			}
			payrollBuckets = append(payrollBuckets, b)
		}
	}

	// ── 2. NDS (sales) — realizatsiya / zachet / balansi ──
	ndsRate := h.getCompanyTaxRatePct(tenantID, "sales", defaultNdsRatePct)
	var salesTax, purchaseTax float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(tax_amount), 0) FROM sales_invoices
		WHERE tenant_id=$1 AND invoice_date BETWEEN $2 AND $3
		  AND status NOT IN ('draft','cancelled') AND deleted_at IS NULL
	`, tenantID, start, end).Scan(&salesTax)
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(tax_amount), 0) FROM purchase_invoices
		WHERE tenant_id=$1 AND invoice_date BETWEEN $2 AND $3
		  AND status NOT IN ('draft','cancelled') AND deleted_at IS NULL
	`, tenantID, start, end).Scan(&purchaseTax)
	ndsBalance := round2(salesTax - purchaseTax)

	// ── 3. Foyda tax (profit) — solik bazasi × rate ──
	profitRate := h.getCompanyTaxRatePct(tenantID, "profit", defaultProfitTaxRatePct)
	orgID, _ := middleware.GetOrganizationID(c)
	recognized, unrecognized, _ := h.computeProfitTaxExpenses(tenantID, orgID, start, end)

	income := manualIncome
	if income <= 0 {
		// Pull from ledger revenue credits the same way GetProfitTaxRevenue does.
		_ = h.db.QueryRow(`
			SELECT COALESCE(SUM(jel.credit_amount) - SUM(jel.debit_amount), 0)
			FROM journal_entry_lines jel
			JOIN journal_entries je    ON je.id  = jel.journal_entry_id
			JOIN accounts a            ON a.id   = jel.account_id
			JOIN account_types at      ON at.id  = a.account_type_id
			WHERE je.tenant_id = $1
			  AND at.category  = 'revenue'
			  AND je.status    = 'posted'
			  AND je.deleted_at IS NULL
			  AND je.entry_date BETWEEN $2::date AND $3::date
		`, tenantID, start.Format("2006-01-02"), end.Format("2006-01-02")).Scan(&income)
	}
	taxBase := income - recognized
	if taxBase < 0 {
		taxBase = 0
	}
	profitTaxAmount := round2(taxBase * profitRate / 100)

	// ── 4. Aylanma tax (turnover) — income × rate ──
	turnoverRate := h.getCompanyTaxRatePct(tenantID, "turnover", defaultTurnoverTaxRatePct)
	turnoverAmount := round2(income * turnoverRate / 100)
	regimeConflict := h.hasActiveCompanyTaxRate(tenantID, "sales", nil) &&
		h.hasActiveCompanyTaxRate(tenantID, "turnover", nil)

	// ── 5. Dividend — rate only; per-distribution calculator runs separately ──
	dividendRate := h.getCompanyTaxRatePct(tenantID, "dividend", defaultDividendRatePct)

	// ── Totals the director cares about ──
	companyTaxTotal := ndsBalance + profitTaxAmount + totalEmployerContrib
	if ndsBalance < 0 {
		// NDS credit balance doesn't count as payable in the director total.
		companyTaxTotal -= ndsBalance
	}

	response.Success(c, gin.H{
		"period_type":  periodType,
		"period_key":   periodKey,
		"period_start": start.Format("2006-01-02"),
		"period_end":   end.Format("2006-01-02"),

		"payroll": gin.H{
			"buckets":            payrollBuckets,
			"employee_withhold":  round2(totalEmployeeWithhold),
			"employer_contrib":   round2(totalEmployerContrib),
		},

		"nds": gin.H{
			"rate":          ndsRate,
			"realizatsiya":  round2(salesTax),
			"zachet":        round2(purchaseTax),
			"balansi":       ndsBalance,
			"payable":       ndsBalance > 0,
		},

		"profit": gin.H{
			"rate":             profitRate,
			"income":           round2(income),
			"recognized":       round2(recognized),
			"unrecognized":     round2(unrecognized),
			"tax_base":         round2(taxBase),
			"tax_amount":       profitTaxAmount,
			"income_source":    ternaryString(manualIncome > 0, "manual", "ledger"),
		},

		"turnover": gin.H{
			"rate":       turnoverRate,
			"turnover":   round2(income),
			"tax_amount": turnoverAmount,
		},

		"dividend": gin.H{
			"rate": dividendRate,
			// per-distribution: use POST /dividend-tax/compute or the
			// dividend-distributions flow.
		},

		"regime_conflict": regimeConflict,

		// Director headline: total tax owed by the company to the budget for
		// the period. Employee withholdings are also paid BY the company but
		// conceptually they're the employee's tax (just routed through us),
		// so we report them separately.
		"company_tax_total": round2(companyTaxTotal),
	})
}

func ternaryString(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
