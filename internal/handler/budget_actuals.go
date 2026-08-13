package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// One definition of a budget line's ACTUAL spend.
//
// There is no `actual` column anywhere — it is derived from posted journal
// entries inside the budget's date window. That expression was written out
// twice, in ListBudgetLines and GetBudgetPlanVsActual, and the two had already
// drifted: plan-vs-actual omitted `je.deleted_at IS NULL`, so it counted
// soft-deleted journal entries and reported a LARGER actual than /budget-lines
// for the same budget the moment anything had been deleted. Both now embed this
// constant, so they cannot disagree again.
//
// The revenue/expense sign split is deliberate and must be kept: revenue lines
// are credit-normal, and a single `debit - credit` made every revenue actual
// negative, so every revenue budget looked "critical".
//
// CONTRACT: the embedding query must have `bl` (budget_lines), `b` (budgets)
// and `fy` (fiscal_years) in scope. Keeping the date window as
// COALESCE(b.start_date, fy.start_date) rather than bound parameters is what
// lets the same text drop into a per-budget lateral and a per-line select
// unchanged.
const budgetActualSQL = `
	COALESCE((
		SELECT CASE
		    WHEN COALESCE(bl.line_type, b.budget_type) = 'revenue'
		         THEN SUM(jel.credit_amount) - SUM(jel.debit_amount)
		    ELSE SUM(jel.debit_amount) - SUM(jel.credit_amount)
		END
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE jel.account_id = bl.account_id
		  AND je.tenant_id   = b.tenant_id
		  AND je.status      = 'posted'
		  AND je.deleted_at IS NULL
		  AND je.entry_date >= COALESCE(b.start_date, fy.start_date)
		  AND je.entry_date <= COALESCE(b.end_date, fy.end_date)
	), 0)`

// budgetRollupSQL rolls every line of one budget into planned/actual pairs,
// split by side (expense vs revenue) plus the combined legacy pair.
//
// A LATERAL over budget_lines, not a per-row query in a Go loop: the loop shape
// is what made GetGeneralLedger cost 163 round-trips for 162 accounts, and this
// list is paginated so the rollup rides along with the page already being read.
//
// The side split exists because summing the two directions into one "actual"
// is meaningless for control: an expense actual is money SPENT, a revenue
// actual is money EARNED, and their sum flagged budgets as "overspent" the
// moment revenue merely beat its plan. Overspend must be judged on the expense
// side alone (matching checkBudgetLineExceeded, which skips revenue lines).
//
// `planned` stays 0 for a budget with no lines. It is deliberately NOT
// substituted with b.total_amount here — callers that want that fallback apply
// it themselves, so "no lines" stays distinguishable from "lines summing to
// zero" in the raw number.
const budgetRollupSQL = `
	LEFT JOIN LATERAL (
		SELECT COALESCE(SUM(bl.budgeted_amount), 0) AS planned,
		       COALESCE(SUM(` + budgetActualSQL + `), 0) AS actual,
		       COALESCE(SUM(bl.budgeted_amount) FILTER (WHERE COALESCE(bl.line_type, 'expense') <> 'revenue'), 0) AS planned_expense,
		       COALESCE(SUM(` + budgetActualSQL + `) FILTER (WHERE COALESCE(bl.line_type, 'expense') <> 'revenue'), 0) AS actual_expense,
		       COALESCE(SUM(bl.budgeted_amount) FILTER (WHERE COALESCE(bl.line_type, 'expense') = 'revenue'), 0) AS planned_revenue,
		       COALESCE(SUM(` + budgetActualSQL + `) FILTER (WHERE COALESCE(bl.line_type, 'expense') = 'revenue'), 0) AS actual_revenue
		FROM budget_lines bl
		WHERE bl.budget_id = b.id
	) roll ON TRUE`

// GetBudgetsSummary godoc
// @Summary Budget totals
// @Description Planned vs actual across every budget, for the summary cards
// @Tags Finance - Budgets
// @Produce json
// @Param fiscal_year_id query string false "Filter by fiscal year"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /finance/budgets/summary [get]
func (h *Handler) GetBudgetsSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// The same filters ListBudgets accepts, so the cards always describe the
	// rows underneath them. page/page_size are ignored on purpose — a summary
	// that paginated would be answering a different question than it appears to.
	args := []interface{}{tenantID}
	where := " WHERE b.tenant_id = $1 AND b.deleted_at IS NULL"
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		args = append(args, orgID)
		where += fmt.Sprintf(" AND b.organization_id = $%d", len(args))
	}
	if v := c.Query("fiscal_year_id"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND b.fiscal_year_id = $%d", len(args))
	}

	var out struct {
		ActiveBudgets    int `json:"active_budgets"`
		OverspentBudgets int `json:"overspent_budgets"`
		// Side-split figures — what the UI cards show. Spending control lives
		// on the expense side; revenue is a separate achievement metric, and
		// mixing the two directions into one number is what used to flag
		// over-performing revenue as an overspend.
		PlannedExpense float64 `json:"planned_expense"`
		ActualExpense  float64 `json:"actual_expense"`
		PlannedRevenue float64 `json:"planned_revenue"`
		ActualRevenue  float64 `json:"actual_revenue"`
		// Legacy combined pair, kept for callers scanning the old shape.
		TotalPlanned float64 `json:"total_planned"`
		TotalActual  float64 `json:"total_actual"`
		Variance     float64 `json:"variance"`
	}

	// One query over the same rollup the list uses. The total_amount fallback
	// for line-less budgets is applied once, in the CTE, on the side the
	// budget's own type names — so the overspent test and the planned totals
	// cannot disagree about which number a line-less budget has.
	err := h.db.QueryRow(`
		WITH rolled AS (
			SELECT b.status,
			       CASE WHEN roll.planned = 0 AND b.budget_type <> 'revenue'
			            THEN COALESCE(b.total_amount, 0)
			            ELSE roll.planned_expense END AS eff_planned_expense,
			       CASE WHEN roll.planned = 0 AND b.budget_type = 'revenue'
			            THEN COALESCE(b.total_amount, 0)
			            ELSE roll.planned_revenue END AS eff_planned_revenue,
			       roll.actual_expense, roll.actual_revenue
			FROM budgets b
			LEFT JOIN fiscal_years fy ON fy.id = b.fiscal_year_id`+budgetRollupSQL+where+`
		)
		SELECT
			COUNT(*) FILTER (WHERE status = 'active'),
			COUNT(*) FILTER (WHERE status = 'active'
			                   AND eff_planned_expense > 0
			                   AND actual_expense > eff_planned_expense),
			COALESCE(SUM(eff_planned_expense), 0),
			COALESCE(SUM(actual_expense), 0),
			COALESCE(SUM(eff_planned_revenue), 0),
			COALESCE(SUM(actual_revenue), 0)
		FROM rolled`, args...).Scan(
		&out.ActiveBudgets, &out.OverspentBudgets,
		&out.PlannedExpense, &out.ActualExpense,
		&out.PlannedRevenue, &out.ActualRevenue)
	if err != nil {
		h.log.Error("Failed to build budgets summary", "error", err)
		response.InternalError(c, "Failed to build budgets summary")
		return
	}
	out.TotalPlanned = out.PlannedExpense + out.PlannedRevenue
	out.TotalActual = out.ActualExpense + out.ActualRevenue
	out.Variance = out.PlannedExpense - out.ActualExpense

	response.Success(c, out)
}
