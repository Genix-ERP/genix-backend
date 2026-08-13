package handler

import (
	"math"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// GetFinanceKPIs serves the Moliya v2 business-KPI layer in ONE call
// (docs/moliya-v2/conventions.md §3 — single source, Direktor paneli and
// Moliya consume this endpoint, no parallel math). Everything derives from
// the posted ledger (and open invoices for DSO/DPO/overdue), scoped by
// tenant/org and the requested period, defaulting to the current month.
//
// Every KPI is returned as { value, inputs } so the UI can render the
// formula; a tripped guard yields value = JSON null — never 0, ∞ or NaN
// (renders as "Ma'lumot yetarli emas").
func (h *Handler) GetFinanceKPIs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	const dateFmt = "2006-01-02"
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	to := now
	if s := c.Query("period_from"); s != "" {
		p, err := time.ParseInLocation(dateFmt, s, now.Location())
		if err != nil {
			response.BadRequest(c, "Invalid period_from")
			return
		}
		from = p
	}
	if s := c.Query("period_to"); s != "" {
		p, err := time.ParseInLocation(dateFmt, s, now.Location())
		if err != nil {
			response.BadRequest(c, "Invalid period_to")
			return
		}
		to = p
	}
	if to.Before(from) {
		response.BadRequest(c, "period_to is before period_from")
		return
	}

	// Previous period = same length window immediately before `from`
	// (conventions §3); balance-sheet opening snapshot = from − 1 day.
	periodDays := int(to.Sub(from).Hours()/24) + 1
	prevTo := from.AddDate(0, 0, -1)
	prevFrom := prevTo.AddDate(0, 0, -(periodDays - 1))
	periodFrom := from.Format(dateFmt)
	periodTo := to.Format(dateFmt)
	prevFromStr := prevFrom.Format(dateFmt)
	prevToStr := prevTo.Format(dateFmt)
	bsOpen := prevTo.Format(dateFmt) // = period_from − 1 day

	// Optional org scoping, same convention as GetFinanceDashboard.
	var orgID uuid.UUID
	orgScoped := false
	if oid, orgOk := middleware.GetOrganizationID(c); orgOk && oid != uuid.Nil {
		orgID = oid
		orgScoped = true
	}

	// ── Query helpers (posted ledger only, tenant-scoped via accounts) ──

	// Period P&L: revenue / total expense / COGS (9100–9199, Sotilgan
	// mahsulot tannarxi) / davr xarajatlari (94xx).
	plAt := func(fromS, toS string) (revenue, expense, cogs, opex94 float64) {
		args := []interface{}{tenantID, fromS, toS}
		filter := ""
		if orgScoped {
			filter = " AND je.organization_id = $4"
			args = append(args, orgID)
		}
		_ = h.db.QueryRow(`
			SELECT
				COALESCE(SUM(CASE WHEN at.category = 'revenue' THEN l.credit_amount - l.debit_amount END), 0),
				COALESCE(SUM(CASE WHEN at.category = 'expense' THEN l.debit_amount - l.credit_amount END), 0),
				COALESCE(SUM(CASE WHEN at.category = 'expense' AND a.code >= '9100' AND a.code < '9200' THEN l.debit_amount - l.credit_amount END), 0),
				COALESCE(SUM(CASE WHEN at.category = 'expense' AND a.code >= '9400' AND a.code < '9500' THEN l.debit_amount - l.credit_amount END), 0)
			FROM journal_entry_lines l
			JOIN journal_entries je ON je.id = l.journal_entry_id
				AND je.status = 'posted' AND je.deleted_at IS NULL
				AND je.entry_date >= $2 AND je.entry_date <= $3`+filter+`
			JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
			JOIN account_types at ON at.id = a.account_type_id
			WHERE at.category IN ('revenue', 'expense')
		`, args...).Scan(&revenue, &expense, &cogs, &opex94)
		return
	}

	// Balance sheet as of a date, from cumulative ledger balances.
	// Assets INCLUDE contra_asset netted in (0200/0490/4910 carry credit
	// balances, so summing debit−credit nets them naturally).
	//
	// Current-asset predicate (verified against the demo chart): asset or
	// contra_asset accounts whose code starts with 1/2/4/5 — 1xxx TMZ,
	// 2xxx tugallanmagan ishlab chiqarish (2510 is category 'expense' so
	// the category filter already excludes it), 4xxx debitorlik (4910
	// contra netted in), 5xxx pul mablag'lari. 0xxx fixed assets and 3xxx
	// kelgusi davr xarajatlari stay out per conventions §3. Current
	// liabilities = liability accounts 6xxx (7xxx long-term excluded).
	bsAt := func(asOf string) (assets, liabilities, currentAssets, currentLiabilities, cash float64) {
		args := []interface{}{tenantID, asOf}
		filter := ""
		if orgScoped {
			filter = " AND je.organization_id = $3"
			args = append(args, orgID)
		}
		// Cash bucket uses the canonical predicate (cash_engine.go) — the
		// bare at.code='CASH' check missed custom bank accounts flagged
		// is_bank_account whose type isn't CASH (moliya chuqur audit
		// 2026-08-13).
		_ = h.db.QueryRow(`
			SELECT
				COALESCE(SUM(CASE WHEN at.category IN ('asset', 'contra_asset') THEN l.debit_amount - l.credit_amount END), 0),
				COALESCE(SUM(CASE WHEN at.category = 'liability' THEN l.credit_amount - l.debit_amount END), 0),
				COALESCE(SUM(CASE WHEN at.category IN ('asset', 'contra_asset') AND left(a.code, 1) IN ('1', '2', '4', '5') THEN l.debit_amount - l.credit_amount END), 0),
				COALESCE(SUM(CASE WHEN at.category = 'liability' AND left(a.code, 1) = '6' THEN l.credit_amount - l.debit_amount END), 0),
				COALESCE(SUM(CASE WHEN `+cashAccountPredicate("a", "at")+` THEN l.debit_amount - l.credit_amount END), 0)
			FROM journal_entry_lines l
			JOIN journal_entries je ON je.id = l.journal_entry_id
				AND je.status = 'posted' AND je.deleted_at IS NULL
				AND je.entry_date <= $2`+filter+`
			JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
			JOIN account_types at ON at.id = a.account_type_id
		`, args...).Scan(&assets, &liabilities, &currentAssets, &currentLiabilities, &cash)
		return
	}

	revenue, expense, cogs, opex94 := plAt(periodFrom, periodTo)
	prevRevenue, _, _, _ := plAt(prevFromStr, prevToStr)
	netResult := revenue - expense

	assetsBegin, liabBegin, _, _, _ := bsAt(bsOpen)
	assetsEnd, liabEnd, currentAssets, currentLiabilities, cash := bsAt(periodTo)

	// The cash_balance KPI must equal the dashboard's cash card, so it comes
	// from the one cash engine (cashBalancesAsOf): opening_balance counted,
	// organizations scoped on the ACCOUNT. bsAt's line-driven variant stays
	// for the balance-sheet ratios, but the headline cash number is engine's.
	if _, engineCash, cashErr := h.cashBalancesAsOf(tenantID, orgID, periodTo); cashErr == nil {
		cash = engineCash
	}
	equityBegin := assetsBegin - liabBegin
	equityEnd := assetsEnd - liabEnd
	avgAssets := (assetsBegin + assetsEnd) / 2
	avgEquity := (equityBegin + equityEnd) / 2

	// ── Cash runway: net cash flow per month over the 3 full calendar
	//    months strictly before the month containing period_to; average
	//    outflow over net-negative months only. ─────────────────────────
	cfTo := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, to.Location())
	cfFrom := cfTo.AddDate(0, -3, 0)
	cfArgs := []interface{}{tenantID, cfFrom.Format(dateFmt), cfTo.Format(dateFmt)}
	cfOrgFilter := ""
	if orgScoped {
		cfOrgFilter = " AND je.organization_id = $4"
		cfArgs = append(cfArgs, orgID)
	}
	negMonths := 0
	negOutflow := 0.0
	cfRows, err := h.db.Query(`
		SELECT to_char(je.entry_date, 'YYYY-MM'), SUM(l.debit_amount - l.credit_amount)
		FROM journal_entry_lines l
		JOIN journal_entries je ON je.id = l.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date >= $2 AND je.entry_date < $3`+cfOrgFilter+`
		JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
		JOIN account_types at ON at.id = a.account_type_id
		WHERE `+cashAccountPredicate("a", "at")+`
		GROUP BY 1
	`, cfArgs...)
	if err == nil {
		for cfRows.Next() {
			var ym string
			var flow float64
			if cfRows.Scan(&ym, &flow) == nil && flow < 0 {
				negMonths++
				negOutflow += -flow
			}
		}
		cfRows.Close()
	}

	// ── AR / AP snapshots from open-invoice ledgers (amount_due of
	//    invoices issued on/before the date) + period purchases. ────────
	invArgs := []interface{}{tenantID, bsOpen, periodTo, periodFrom}
	invOrgFilter := ""
	if orgScoped {
		invOrgFilter = " AND organization_id = $5"
		invArgs = append(invArgs, orgID)
	}
	var arBegin, arEnd, apBegin, apEnd, purchases float64
	_ = h.db.QueryRow(`
		SELECT
			(SELECT COALESCE(SUM(amount_due), 0) FROM sales_invoices
				WHERE tenant_id = $1 AND deleted_at IS NULL
				  AND status NOT IN ('draft', 'cancelled', 'void')
				  AND invoice_date <= $2`+invOrgFilter+`),
			(SELECT COALESCE(SUM(amount_due), 0) FROM sales_invoices
				WHERE tenant_id = $1 AND deleted_at IS NULL
				  AND status NOT IN ('draft', 'cancelled', 'void')
				  AND invoice_date <= $3`+invOrgFilter+`),
			(SELECT COALESCE(SUM(amount_due), 0) FROM purchase_invoices
				WHERE tenant_id = $1 AND deleted_at IS NULL
				  AND status NOT IN ('draft', 'cancelled', 'void')
				  AND invoice_date <= $2`+invOrgFilter+`),
			(SELECT COALESCE(SUM(amount_due), 0) FROM purchase_invoices
				WHERE tenant_id = $1 AND deleted_at IS NULL
				  AND status NOT IN ('draft', 'cancelled', 'void')
				  AND invoice_date <= $3`+invOrgFilter+`),
			(SELECT COALESCE(SUM(total_amount), 0) FROM purchase_invoices
				WHERE tenant_id = $1 AND deleted_at IS NULL
				  AND status NOT IN ('draft', 'cancelled', 'void')
				  AND invoice_date >= $4 AND invoice_date <= $3`+invOrgFilter+`)
	`, invArgs...).Scan(&arBegin, &arEnd, &apBegin, &apEnd, &purchases)
	avgAR := (arBegin + arEnd) / 2
	avgAP := (apBegin + apEnd) / 2

	// Overdue share of open AR — same predicate as the finance-dashboard
	// receivables card (overdue-ness is only knowable as of today).
	odArgs := []interface{}{tenantID}
	odOrgFilter := ""
	if orgScoped {
		odOrgFilter = " AND organization_id = $2"
		odArgs = append(odArgs, orgID)
	}
	var arOpenTotal, arOpenOverdue float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(amount_due), 0),
		       COALESCE(SUM(CASE WHEN due_date < CURRENT_DATE THEN amount_due END), 0)
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND status NOT IN ('draft', 'cancelled', 'void')
		  AND amount_due > 0`+odOrgFilter+`
	`, odArgs...).Scan(&arOpenTotal, &arOpenOverdue)

	// ── Assemble { value, inputs } per KPI; nil value ⇒ JSON null. ─────
	r2 := func(v float64) float64 { return math.Round(v*100) / 100 }
	val := func(v float64) *float64 {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		v = r2(v)
		return &v
	}
	kpi := func(value *float64, inputs gin.H) gin.H {
		return gin.H{"value": value, "inputs": inputs}
	}

	var grossMargin, operatingMargin, netMargin, dso *float64
	if revenue != 0 {
		grossMargin = val((revenue - cogs) / revenue * 100)
		operatingMargin = val((revenue - cogs - opex94) / revenue * 100)
		netMargin = val(netResult / revenue * 100)
		dso = val(avgAR / revenue * float64(periodDays))
	}
	var roa, roe *float64
	if avgAssets > 0 {
		roa = val(netResult / avgAssets * 100)
	}
	if avgEquity > 0 {
		roe = val(netResult / avgEquity * 100)
	}
	var currentRatio *float64
	if currentLiabilities != 0 {
		currentRatio = val(currentAssets / currentLiabilities)
	}
	var runway *float64
	if negMonths > 0 {
		runway = val(cash / (negOutflow / float64(negMonths)))
	}
	var dpo *float64
	if purchases != 0 {
		dpo = val(avgAP / purchases * float64(periodDays))
	}
	var overdueARPct *float64
	if arOpenTotal != 0 {
		overdueARPct = val(arOpenOverdue / arOpenTotal * 100)
	}
	var revenueGrowth *float64
	if prevRevenue != 0 {
		revenueGrowth = val((revenue - prevRevenue) / prevRevenue * 100)
	}

	response.Success(c, gin.H{
		"kpis": gin.H{
			"gross_margin": kpi(grossMargin, gin.H{
				"revenue": r2(revenue), "cogs": r2(cogs),
			}),
			"operating_margin": kpi(operatingMargin, gin.H{
				"revenue": r2(revenue), "cogs": r2(cogs), "period_expenses": r2(opex94),
			}),
			"net_margin": kpi(netMargin, gin.H{
				"revenue": r2(revenue), "net_result": r2(netResult),
			}),
			"roa": kpi(roa, gin.H{
				"net_result": r2(netResult), "assets_begin": r2(assetsBegin),
				"assets_end": r2(assetsEnd), "avg_assets": r2(avgAssets),
			}),
			"roe": kpi(roe, gin.H{
				"net_result": r2(netResult), "equity_begin": r2(equityBegin),
				"equity_end": r2(equityEnd), "avg_equity": r2(avgEquity),
			}),
			"equity": kpi(val(equityEnd), gin.H{
				"assets": r2(assetsEnd), "liabilities": r2(liabEnd), "as_of": periodTo,
			}),
			"working_capital": kpi(val(currentAssets-currentLiabilities), gin.H{
				"current_assets": r2(currentAssets), "current_liabilities": r2(currentLiabilities),
			}),
			"current_ratio": kpi(currentRatio, gin.H{
				"current_assets": r2(currentAssets), "current_liabilities": r2(currentLiabilities),
			}),
			"cash_balance": kpi(val(cash), gin.H{
				"as_of": periodTo,
			}),
			"cash_runway_months": kpi(runway, gin.H{
				"cash": r2(cash), "negative_months": negMonths,
				"avg_monthly_outflow": func() interface{} {
					if negMonths == 0 {
						return nil
					}
					return r2(negOutflow / float64(negMonths))
				}(),
				"window_from": cfFrom.Format(dateFmt), "window_to": cfTo.Format(dateFmt),
			}),
			"dso": kpi(dso, gin.H{
				"ar_begin": r2(arBegin), "ar_end": r2(arEnd), "avg_ar": r2(avgAR),
				"revenue": r2(revenue), "period_days": periodDays,
			}),
			"dpo": kpi(dpo, gin.H{
				"ap_begin": r2(apBegin), "ap_end": r2(apEnd), "avg_ap": r2(avgAP),
				"purchases": r2(purchases), "period_days": periodDays,
			}),
			"overdue_ar_pct": kpi(overdueARPct, gin.H{
				"overdue_ar": r2(arOpenOverdue), "total_ar": r2(arOpenTotal),
			}),
			"revenue_growth": kpi(revenueGrowth, gin.H{
				"revenue": r2(revenue), "prev_revenue": r2(prevRevenue),
			}),
		},
		"meta": gin.H{
			"period_from": periodFrom,
			"period_to":   periodTo,
			"prev_from":   prevFromStr,
			"prev_to":     prevToStr,
		},
	})
}
