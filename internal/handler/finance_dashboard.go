package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// GetFinanceDashboard serves the Moliya "Asosiy panel" in ONE call
// (docs/moliya-audit.md §2.1 — the old dashboard made 7 income-statement
// requests and pinned the period to all-time). Everything derives from the
// posted ledger, scoped by tenant/org and the requested period:
//
//   - totals:            income / expense / net for the period
//   - cash:              balance per cash/bank account as of period_to, plus a
//     daily cumulative series over the same account set.
//     Both use the shared definition in cash_balance.go —
//     the one /reports/cash-flow also calls, so the web card,
//     its chart, and the mobile card cannot disagree.
//   - monthly:           income vs expense per month across the period
//   - trend:             income/expense/net per month, rolling last 6 full
//     months + current month-to-date, independent of the
//     requested period (conventions.md §4)
//   - expense_breakdown: category-enriched — expense-sourced JE lines are
//     labelled by their Xarajatlar category (join via
//     source_id), everything else by GL account name,
//     so history posted into the 9410 fallback still
//     splits into real categories
//   - receivables/payables: open invoice totals (invoice minus payments —
//     never a manually maintained balance)
//
// Defaults to the current month when no period is given.
func (h *Handler) GetFinanceDashboard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	now := time.Now()
	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")
	if periodFrom == "" {
		periodFrom = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	}
	if periodTo == "" {
		periodTo = now.Format("2006-01-02")
	}

	// Optional org scoping, same convention as GetIncomeStatement.
	var orgID uuid.UUID
	orgScoped := false
	if oid, orgOk := middleware.GetOrganizationID(c); orgOk && oid != uuid.Nil {
		orgID = oid
		orgScoped = true
	}
	orgFilterJE := ""
	orgFilterInv := ""
	if orgScoped {
		orgFilterJE = " AND je.organization_id = $4"
		orgFilterInv = " AND organization_id = $2"
	}

	jeArgs := func(extra ...interface{}) []interface{} {
		args := []interface{}{tenantID, periodFrom, periodTo}
		if orgScoped {
			args = append(args, orgID)
		}
		return append(args, extra...)
	}

	// ── 1. Period totals ────────────────────────────────────────────────
	var totalIncome, totalExpense float64
	_ = h.db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN at.category = 'revenue' THEN l.credit_amount - l.debit_amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN at.category = 'expense' THEN l.debit_amount - l.credit_amount ELSE 0 END), 0)
		FROM journal_entry_lines l
		JOIN journal_entries je ON je.id = l.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date >= $2 AND je.entry_date <= $3`+orgFilterJE+`
		JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
		JOIN account_types at ON at.id = a.account_type_id
		WHERE at.category IN ('revenue', 'expense')
	`, jeArgs()...).Scan(&totalIncome, &totalExpense)

	// ── 2. Cash position as of period_to (per account + total) ──────────
	type cashAccount struct {
		Code    string  `json:"code"`
		Name    string  `json:"name"`
		Balance float64 `json:"balance"`
	}
	// Shared with /reports/cash-flow via cashBalancesAsOf (cash_balance.go) —
	// the two used to compute this independently and disagreed for the same
	// tenant and period. What changes here versus the old inline query: bank
	// accounts join the set, accounts.opening_balance now counts, and orgs are
	// scoped on the account rather than the journal entry. The figure will move
	// for tenants that used opening balances or have bank-flagged accounts that
	// are not typed CASH; that is the correction, not a regression.
	cashScopeOrg := uuid.Nil
	if orgScoped {
		cashScopeOrg = orgID
	}
	cashAccounts := make([]cashAccount, 0, 4)
	balances, cashTotal, err := h.cashBalancesAsOf(tenantID, cashScopeOrg, periodTo)
	if err != nil {
		h.log.Error("Failed to load cash position", "error", err)
	}
	// Merge accounts sharing a code before display. Codes are unique per
	// (tenant, organization), so a tenant-wide (unscoped) request legitimately
	// returns one 5010 per organization — and the web card keys these rows by
	// code (FinanceDashboard.jsx), so emitting duplicates would collide. The
	// helper stays honest and returns them separately; folding them is this
	// card's concern. cashTotal is unaffected either way.
	byCode := make(map[string]int, len(balances))
	for _, b := range balances {
		// Zero-balance accounts stay hidden from the per-account list, as
		// before. cashTotal is computed over the whole set, so hiding them
		// cannot make the rows and the total disagree.
		if b.Balance == 0 {
			continue
		}
		if i, seen := byCode[b.Code]; seen {
			cashAccounts[i].Balance += b.Balance
			continue
		}
		byCode[b.Code] = len(cashAccounts)
		cashAccounts = append(cashAccounts, cashAccount{Code: b.Code, Name: b.Name, Balance: b.Balance})
	}

	// ── 3. Monthly income vs expense across the period ──────────────────
	type monthPoint struct {
		Month   string  `json:"month"`
		Income  float64 `json:"income"`
		Expense float64 `json:"expense"`
	}
	monthly := make([]monthPoint, 0, 12)
	monthRows, err := h.db.Query(`
		SELECT to_char(je.entry_date, 'YYYY-MM') AS ym,
			COALESCE(SUM(CASE WHEN at.category = 'revenue' THEN l.credit_amount - l.debit_amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN at.category = 'expense' THEN l.debit_amount - l.credit_amount ELSE 0 END), 0)
		FROM journal_entry_lines l
		JOIN journal_entries je ON je.id = l.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date >= $2 AND je.entry_date <= $3`+orgFilterJE+`
		JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
		JOIN account_types at ON at.id = a.account_type_id
		WHERE at.category IN ('revenue', 'expense')
		GROUP BY 1 ORDER BY 1
	`, jeArgs()...)
	if err == nil {
		for monthRows.Next() {
			var mp monthPoint
			if monthRows.Scan(&mp.Month, &mp.Income, &mp.Expense) == nil {
				monthly = append(monthly, mp)
			}
		}
		monthRows.Close()
	}

	// ── 3b. Rolling trend: last 6 FULL months + current month-to-date,
	//      INDEPENDENT of the requested period — the trend chart keeps a
	//      rolling window while the cards obey the period filter (fixes
	//      "chart shows one month" when the period is a single month).
	//      Months with no postings are zero-filled for a stable axis. ────
	type trendPoint struct {
		Month   string  `json:"month"`
		Income  float64 `json:"income"`
		Expense float64 `json:"expense"`
		Net     float64 `json:"net"`
	}
	trendStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -6, 0)
	trend := make([]trendPoint, 0, 7)
	trendIdx := make(map[string]int, 7)
	for i := 0; i < 7; i++ {
		ym := trendStart.AddDate(0, i, 0).Format("2006-01")
		trendIdx[ym] = len(trend)
		trend = append(trend, trendPoint{Month: ym})
	}
	trendArgs := []interface{}{tenantID, trendStart.Format("2006-01-02"), now.Format("2006-01-02")}
	if orgScoped {
		trendArgs = append(trendArgs, orgID)
	}
	trendRows, err := h.db.Query(`
		SELECT to_char(je.entry_date, 'YYYY-MM') AS ym,
			COALESCE(SUM(CASE WHEN at.category = 'revenue' THEN l.credit_amount - l.debit_amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN at.category = 'expense' THEN l.debit_amount - l.credit_amount ELSE 0 END), 0)
		FROM journal_entry_lines l
		JOIN journal_entries je ON je.id = l.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date >= $2 AND je.entry_date <= $3`+orgFilterJE+`
		JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
		JOIN account_types at ON at.id = a.account_type_id
		WHERE at.category IN ('revenue', 'expense')
		GROUP BY 1 ORDER BY 1
	`, trendArgs...)
	if err == nil {
		for trendRows.Next() {
			var ym string
			var income, expense float64
			if trendRows.Scan(&ym, &income, &expense) == nil {
				if i, ok := trendIdx[ym]; ok {
					trend[i].Income = income
					trend[i].Expense = expense
					trend[i].Net = income - expense
				}
			}
		}
		trendRows.Close()
	}

	// ── 4. Expense breakdown, category-enriched ─────────────────────────
	type breakdownItem struct {
		Label  string  `json:"label"`
		Amount float64 `json:"amount"`
	}
	breakdown := make([]breakdownItem, 0, 12)
	bdRows, err := h.db.Query(`
		SELECT COALESCE(ec.name, COALESCE(a.name_uz, a.name)) AS label,
		       SUM(l.debit_amount - l.credit_amount) AS amount
		FROM journal_entry_lines l
		JOIN journal_entries je ON je.id = l.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date >= $2 AND je.entry_date <= $3`+orgFilterJE+`
		JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
		JOIN account_types at ON at.id = a.account_type_id AND at.category = 'expense'
		LEFT JOIN expenses e ON je.source_type = 'expense' AND e.id = je.source_id
		LEFT JOIN expense_categories ec ON ec.id = e.category_id
		GROUP BY 1
		HAVING SUM(l.debit_amount - l.credit_amount) > 0
		ORDER BY 2 DESC
	`, jeArgs()...)
	if err == nil {
		for bdRows.Next() {
			var bi breakdownItem
			if bdRows.Scan(&bi.Label, &bi.Amount) == nil {
				breakdown = append(breakdown, bi)
			}
		}
		bdRows.Close()
	}

	// ── 5. Daily cash-balance series (cumulative over all history, shown
	//      for the requested window so the line starts at the true opening
	//      balance, not zero) ─────────────────────────────────────────────
	type cashPoint struct {
		Date    string  `json:"date"`
		Balance float64 `json:"balance"`
	}
	cashSeries := make([]cashPoint, 0, 62)
	// Built on the SAME account set and the SAME formula as the cash KPI above
	// (cashAccountSetSQL / cashBalancesAsOf in cash_balance.go), so the last
	// point of this line equals the "Pul qoldig'i" figure on the same card.
	// It previously used a third definition of its own — CASH-typed accounts
	// only, with no is_leaf filter (so parent rollups double-counted their
	// children), no is_active filter, and no opening_balance — which is why the
	// chart and the number it sits under could not be reconciled. The comment
	// above claimed the line "starts at the true opening balance"; it did not,
	// because accounts.opening_balance was never in the query.
	csOrgFilter := ""
	csArgs := []interface{}{tenantID, periodFrom, periodTo}
	if orgScoped {
		csArgs = append(csArgs, orgID)
		csOrgFilter = " AND a.organization_id = $4"
	}
	csRows, err := h.db.Query(`
		WITH acct AS (
			SELECT a.id, a.opening_balance
			FROM accounts a
			JOIN account_types at ON at.id = a.account_type_id
			WHERE`+cashAccountSetSQL+csOrgFilter+`
		), base AS (
			SELECT COALESCE(SUM(opening_balance), 0) AS ob FROM acct
		), daily AS (
			SELECT je.entry_date::date AS d, SUM(l.debit_amount - l.credit_amount) AS delta
			FROM journal_entry_lines l
			JOIN journal_entries je ON je.id = l.journal_entry_id
				AND je.status = 'posted' AND je.deleted_at IS NULL
				AND je.entry_date <= $3
			JOIN acct ON acct.id = l.account_id
			GROUP BY 1
		), cum AS (
			SELECT d, SUM(delta) OVER (ORDER BY d) AS running FROM daily
		)
		SELECT to_char(cum.d, 'YYYY-MM-DD'), base.ob + cum.running
		FROM cum CROSS JOIN base
		WHERE cum.d >= $2
		ORDER BY cum.d
	`, csArgs...)
	if err == nil {
		for csRows.Next() {
			var cp cashPoint
			if csRows.Scan(&cp.Date, &cp.Balance) == nil {
				cashSeries = append(cashSeries, cp)
			}
		}
		csRows.Close()
	}

	// ── 6. Open AR / AP (invoice minus payments, live) ──────────────────
	invArgs := []interface{}{tenantID}
	if orgScoped {
		invArgs = append(invArgs, orgID)
	}
	// The filter is on the PARTNER's net balance, not on each invoice.
	//
	// `AND amount_due > 0` per row looks like "only the ones still owed", but
	// amount_due is generated as total_amount - amount_paid and nothing caps
	// amount_paid (sales_invoices.go, purchase_invoices.go and
	// payments_reconcile.go all do `amount_paid = amount_paid + $1`). An
	// overpaid invoice therefore has a NEGATIVE amount_due and the row is
	// discarded outright — so a customer who overpaid invoice A and still owes
	// on invoice B was reported at B's full value, with A's credit nowhere.
	// The card overstated the debt, and did so silently.
	//
	// Grouping by partner first and filtering on the total also makes the
	// count mean what the label says. COUNT(DISTINCT customer_id) over
	// invoice rows counts partners who have at least one qualifying INVOICE,
	// which is not the same set the sum is taken over — "(2 mijoz)" beside a
	// figure covering a different two.
	//
	// The 0.005 epsilon matches the `due` definition in payments_reconcile.go
	// rather than testing > 0 on a float sum.
	var arTotal, arOverdue float64
	var arPartners, arOverduePartners int
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(due) FILTER (WHERE due > 0.005), 0),
		       COUNT(*) FILTER (WHERE due > 0.005),
		       COALESCE(SUM(overdue) FILTER (WHERE overdue > 0.005), 0),
		       COUNT(*) FILTER (WHERE overdue > 0.005)
		FROM (`+partnerNetDue("sales_invoices", "customer_id", "", orgFilterInv)+`) p
	`, invArgs...).Scan(&arTotal, &arPartners, &arOverdue, &arOverduePartners)

	var apTotal, apOverdue float64
	var apPartners, apOverduePartners int
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(due) FILTER (WHERE due > 0.005), 0),
		       COUNT(*) FILTER (WHERE due > 0.005),
		       COALESCE(SUM(overdue) FILTER (WHERE overdue > 0.005), 0),
		       COUNT(*) FILTER (WHERE overdue > 0.005)
		FROM (`+partnerNetDue("purchase_invoices", "vendor_id", "", orgFilterInv)+`) p
	`, invArgs...).Scan(&apTotal, &apPartners, &apOverdue, &apOverduePartners)

	response.Success(c, gin.H{
		"period_from":       periodFrom,
		"period_to":         periodTo,
		"total_income":      totalIncome,
		"total_expense":     totalExpense,
		"net_result":        totalIncome - totalExpense,
		"cash_balance":      cashTotal,
		"cash_accounts":     cashAccounts,
		"monthly":           monthly,
		"trend":             trend,
		"expense_breakdown": breakdown,
		"cash_series":       cashSeries,
		"receivables": gin.H{
			"total":            arTotal,
			"partners":         arPartners,
			"overdue":          arOverdue,
			"overdue_partners": arOverduePartners,
		},
		"payables": gin.H{
			"total":    apTotal,
			"partners": apPartners,
			"overdue":  apOverdue,
			// New field, symmetric with receivables. Additive: no client reads
			// it yet, so nothing changes until one chooses to.
			"overdue_partners": apOverduePartners,
		},
	})
}
