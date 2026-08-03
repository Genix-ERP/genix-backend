package handler

// GET /construction/projects/stats — the single call behind the Qurilish
// portfolio page (docs/construction-roadmap.md quick win). Same contract
// family as GET /purchase-orders/stats and GET /inventory/stats: one
// org-aware request instead of client-side hydration, and — unlike the
// legacy GET /construction/dashboard — it honours the organization header.
//
// Conventions fixed in one place:
//   - Status counts cover every live status (soft-deleted rows excluded).
//   - "Fakt" (actual spend) comes from APPROVED construction_expense_lines
//     — the module's single per-object cost register (Dr 0810 by default).
//   - "Kechikkan" = planned_end_date in the past while the project is not
//     completed/cancelled — the same rule the portfolio card uses.
//   - per_project.readiness_pct is the cost-weighted readiness over the
//     project's edinich estimate works (resource_type='' AND parent_line_id=0,
//     plan qty priority imported → original → quantity) — identical math to
//     GetConstructionStagesOverview, lifted to portfolio level so cards can
//     show a COMPUTED progress instead of the dead manual progress_percent.

import (
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) GetConstructionProjectStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

	// Period: [from, to] on expense_date; defaults to the current month.
	nowT := time.Now()
	from := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location())
	to := nowT
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			to = t.Add(24*time.Hour - time.Nanosecond) // inclusive end of day
		}
	}

	// ── Status counts + contract total + overdue ────────────────────────
	byStatus := map[string]int{}
	totalProjects := 0
	var contractTotal float64
	overdueProjects := 0
	if rows, err := h.db.Query(`
		SELECT COALESCE(status, 'draft'),
		       COUNT(*),
		       COALESCE(SUM(contract_amount), 0),
		       COUNT(*) FILTER (
		           WHERE planned_end_date IS NOT NULL
		             AND planned_end_date < CURRENT_DATE
		             AND COALESCE(status, '') NOT IN ('completed', 'cancelled'))
		FROM construction_projects
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND ($2::uuid IS NULL OR organization_id = $2)
		GROUP BY 1
	`, tenantID, orgArg); err == nil {
		for rows.Next() {
			var status string
			var cnt, overdue int
			var sum float64
			if rows.Scan(&status, &cnt, &sum, &overdue) == nil {
				byStatus[status] = cnt
				totalProjects += cnt
				contractTotal += sum
				overdueProjects += overdue
			}
		}
		rows.Close()
	} else {
		h.log.Error("construction stats: status counts failed", "error", err)
	}

	// ── Actual spend from the object-cost register ──────────────────────
	var actualTotal, actualPeriod float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(cel.amount), 0),
		       COALESCE(SUM(cel.amount) FILTER (
		           WHERE cel.expense_date >= $2 AND cel.expense_date <= $3), 0)
		FROM construction_expense_lines cel
		JOIN construction_projects p ON p.id = cel.project_id
		WHERE cel.tenant_id = $1 AND cel.status = 'approved' AND cel.deleted_at IS NULL
		  AND p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND ($4::uuid IS NULL OR p.organization_id = $4)
	`, tenantID, from, to, orgArg).Scan(&actualTotal, &actualPeriod)

	// ── Spend dynamics, last 6 months ───────────────────────────────────
	type monthPoint struct {
		Month string  `json:"month"`
		Value float64 `json:"value"`
	}
	byMonth := map[string]float64{}
	if rows, err := h.db.Query(`
		SELECT to_char(date_trunc('month', cel.expense_date), 'YYYY-MM'),
		       COALESCE(SUM(cel.amount), 0)
		FROM construction_expense_lines cel
		JOIN construction_projects p ON p.id = cel.project_id
		WHERE cel.tenant_id = $1 AND cel.status = 'approved' AND cel.deleted_at IS NULL
		  AND cel.expense_date >= date_trunc('month', CURRENT_DATE) - INTERVAL '5 months'
		  AND p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND ($2::uuid IS NULL OR p.organization_id = $2)
		GROUP BY 1
	`, tenantID, orgArg); err == nil {
		for rows.Next() {
			var m string
			var v float64
			if rows.Scan(&m, &v) == nil {
				byMonth[m] = v
			}
		}
		rows.Close()
	}
	monthly := make([]monthPoint, 0, 6)
	cur := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location()).AddDate(0, -5, 0)
	for i := 0; i < 6; i++ {
		key := cur.Format("2006-01")
		monthly = append(monthly, monthPoint{Month: key, Value: byMonth[key]})
		cur = cur.AddDate(0, 1, 0)
	}

	// ── Per-project: computed readiness + actual spend (card enrichment) ─
	type perProject struct {
		ID           int64   `json:"id"`
		ReadinessPct float64 `json:"readiness_pct"`
		ActualAmount float64 `json:"actual_amount"`
	}
	perByID := map[int64]*perProject{}
	if rows, err := h.db.Query(`
		WITH works AS (
			SELECT e.project_id,
			       COALESCE(el.total_amount, 0)  AS total_amount,
			       COALESCE(el.done_quantity, 0) AS done_quantity,
			       CASE
			           WHEN COALESCE(el.imported_quantity, 0) > 0 THEN el.imported_quantity
			           WHEN COALESCE(el.original_quantity, 0) > 0 THEN el.original_quantity
			           ELSE COALESCE(el.quantity, 0)
			       END AS q
			FROM construction_estimate_line el
			JOIN construction_estimate e
			  ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
			WHERE el.tenant_id = $1
			  AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
			  AND COALESCE(el.resource_type, '') = ''
			  AND COALESCE(el.parent_line_id, 0) = 0
		)
		SELECT w.project_id,
		       CASE WHEN SUM(w.total_amount) > 0
		            THEN SUM(w.total_amount * LEAST(w.done_quantity / NULLIF(w.q, 0), 1))
		                 / SUM(w.total_amount) * 100
		            ELSE 0 END
		FROM works w
		JOIN construction_projects p ON p.id = w.project_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND ($2::uuid IS NULL OR p.organization_id = $2)
		GROUP BY w.project_id
		LIMIT 500
	`, tenantID, orgArg); err == nil {
		for rows.Next() {
			var id int64
			var pct float64
			if rows.Scan(&id, &pct) == nil {
				perByID[id] = &perProject{ID: id, ReadinessPct: pct}
			}
		}
		rows.Close()
	} else {
		h.log.Error("construction stats: readiness failed", "error", err)
	}
	if rows, err := h.db.Query(`
		SELECT cel.project_id, COALESCE(SUM(cel.amount), 0)
		FROM construction_expense_lines cel
		JOIN construction_projects p ON p.id = cel.project_id
		WHERE cel.tenant_id = $1 AND cel.status = 'approved' AND cel.deleted_at IS NULL
		  AND p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND ($2::uuid IS NULL OR p.organization_id = $2)
		GROUP BY cel.project_id
		LIMIT 500
	`, tenantID, orgArg); err == nil {
		for rows.Next() {
			var id int64
			var amt float64
			if rows.Scan(&id, &amt) == nil {
				if pp, okPP := perByID[id]; okPP {
					pp.ActualAmount = amt
				} else {
					perByID[id] = &perProject{ID: id, ActualAmount: amt}
				}
			}
		}
		rows.Close()
	}
	per := make([]perProject, 0, len(perByID))
	for _, pp := range perByID {
		per = append(per, *pp)
	}

	response.Success(c, gin.H{
		"totals": gin.H{
			"total_projects":   totalProjects,
			"by_status":        byStatus,
			"contract_total":   contractTotal,
			"actual_total":     actualTotal,
			"actual_period":    actualPeriod,
			"overdue_projects": overdueProjects,
		},
		"period":         gin.H{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")},
		"monthly_series": monthly,
		"per_project":    per,
	})
}
