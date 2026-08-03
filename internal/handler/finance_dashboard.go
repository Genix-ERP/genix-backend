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
//   - cash:              balance per CASH-type account as of period_to,
//                        plus a daily cumulative series for the period
//   - monthly:           income vs expense per month across the period
//   - expense_breakdown: category-enriched — expense-sourced JE lines are
//                        labelled by their Xarajatlar category (join via
//                        source_id), everything else by GL account name,
//                        so history posted into the 9410 fallback still
//                        splits into real categories
//   - receivables/payables: open invoice totals (invoice minus payments —
//                        never a manually maintained balance)
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
	cashAccounts := make([]cashAccount, 0, 4)
	var cashTotal float64
	cashArgs := []interface{}{tenantID, periodTo}
	cashOrgFilter := ""
	if orgScoped {
		cashOrgFilter = " AND je.organization_id = $3"
		cashArgs = append(cashArgs, orgID)
	}
	cashRows, err := h.db.Query(`
		SELECT a.code, COALESCE(a.name_uz, a.name), COALESCE(SUM(l.debit_amount - l.credit_amount), 0) AS bal
		FROM accounts a
		JOIN account_types at ON at.id = a.account_type_id AND at.code = 'CASH'
		LEFT JOIN (
			journal_entry_lines l
			JOIN journal_entries je ON je.id = l.journal_entry_id
				AND je.status = 'posted' AND je.deleted_at IS NULL
				AND je.entry_date <= $2`+cashOrgFilter+`
		) ON l.account_id = a.id
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true AND a.is_leaf = true
		GROUP BY a.code, COALESCE(a.name_uz, a.name)
		HAVING COALESCE(SUM(l.debit_amount - l.credit_amount), 0) <> 0
		ORDER BY a.code
	`, cashArgs...)
	if err == nil {
		for cashRows.Next() {
			var ca cashAccount
			if cashRows.Scan(&ca.Code, &ca.Name, &ca.Balance) == nil {
				cashAccounts = append(cashAccounts, ca)
				cashTotal += ca.Balance
			}
		}
		cashRows.Close()
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
	csRows, err := h.db.Query(`
		WITH daily AS (
			SELECT je.entry_date::date AS d, SUM(l.debit_amount - l.credit_amount) AS delta
			FROM journal_entry_lines l
			JOIN journal_entries je ON je.id = l.journal_entry_id
				AND je.status = 'posted' AND je.deleted_at IS NULL
				AND je.entry_date <= $3`+orgFilterJE+`
			JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
			JOIN account_types at ON at.id = a.account_type_id AND at.code = 'CASH'
			GROUP BY 1
		), cum AS (
			SELECT d, SUM(delta) OVER (ORDER BY d) AS bal FROM daily
		)
		SELECT to_char(d, 'YYYY-MM-DD'), bal FROM cum WHERE d >= $2 ORDER BY d
	`, jeArgs()...)
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
	var arTotal, arOverdue float64
	var arPartners, arOverduePartners int
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(amount_due), 0),
		       COUNT(DISTINCT customer_id),
		       COALESCE(SUM(CASE WHEN due_date < CURRENT_DATE THEN amount_due ELSE 0 END), 0),
		       COUNT(DISTINCT CASE WHEN due_date < CURRENT_DATE THEN customer_id END)
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND status NOT IN ('draft', 'cancelled', 'void')
		  AND amount_due > 0`+orgFilterInv+`
	`, invArgs...).Scan(&arTotal, &arPartners, &arOverdue, &arOverduePartners)

	var apTotal, apOverdue float64
	var apPartners int
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(amount_due), 0),
		       COUNT(DISTINCT vendor_id),
		       COALESCE(SUM(CASE WHEN due_date < CURRENT_DATE THEN amount_due ELSE 0 END), 0)
		FROM purchase_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND status NOT IN ('draft', 'cancelled', 'void')
		  AND amount_due > 0`+orgFilterInv+`
	`, invArgs...).Scan(&apTotal, &apPartners, &apOverdue)

	response.Success(c, gin.H{
		"period_from":       periodFrom,
		"period_to":         periodTo,
		"total_income":      totalIncome,
		"total_expense":     totalExpense,
		"net_result":        totalIncome - totalExpense,
		"cash_balance":      cashTotal,
		"cash_accounts":     cashAccounts,
		"monthly":           monthly,
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
		},
	})
}
