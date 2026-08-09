package handler

import (
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// PROGRESS v2 — yagona progress-dvigatel (qurilish-v2/conventions.md §2).
//
// Modulda 5 xil progress-formula yashagan (audit 2026-08-09): kanonik F1 —
// qiymat-vaznli tayyorlik (construction_stats.go bilan AYNAN bir xil CTE):
//   SUM(total_amount × LEAST(done/plan_qty, 1)) / SUM(total_amount) × 100
// plan_qty = imported → original → quantity. Bu endpoint Umumiy ko'rinish,
// Bosqichlar sarlavhasi va boshqa har qanday vidjet uchun BITTA manba:
// loyiha %, bosqich kesimi, kalendar-elapsed, byudjet-mini (reja = smeta,
// fakt = approved CEL — real pul), muddati o'tgan ishlar soni.
// =====================================================

// GetProjectProgressV2 — GET /construction/projects/:id/progress
func (h *Handler) GetProjectProgressV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var plannedStart, plannedEnd nullableTime
	var contractAmount nullableFloat64
	if err := h.db.QueryRow(`
		SELECT planned_start_date, planned_end_date, contract_amount
		FROM construction_projects
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&plannedStart, &plannedEnd, &contractAmount); err != nil {
		response.NotFound(c, "Project not found")
		return
	}

	// Bosqich kesimi — F1 ni bo'lim (parent_item_number bargi) bo'yicha
	// guruhlab; loyiha % = xuddi shu satrlarning jamlanmasi (bir xil manba,
	// bir xil natija — divergensiya matematik jihatdan mumkin emas).
	rows, err := h.db.Query(`
		SELECT COALESCE(NULLIF(TRIM(regexp_replace(COALESCE(el.parent_item_number, ''), '^.*›\s*', '')), ''), 'Boshqalar') AS stage,
		       COUNT(*) AS works_total,
		       COUNT(*) FILTER (WHERE COALESCE(el.approval_status, 'pending') = 'confirmed_engineer') AS works_confirmed,
		       COUNT(*) FILTER (WHERE COALESCE(el.done_quantity, 0) > 0) AS works_started,
		       COUNT(*) FILTER (WHERE el.sched_end IS NOT NULL AND el.sched_end < CURRENT_DATE
		                          AND COALESCE(el.done_quantity, 0) < `+workQtyCase+`) AS works_overdue,
		       COALESCE(SUM(el.total_amount), 0) AS plan_amount,
		       COALESCE(SUM(COALESCE(el.total_amount, 0) * LEAST(COALESCE(el.done_quantity, 0) / NULLIF(`+workQtyCase+`, 0), 1)), 0) AS done_amount
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE el.tenant_id = $1 AND e.project_id = $2 AND `+workRowFilter+`
		GROUP BY 1
		ORDER BY MIN(el.sort_order), MIN(el.id)
	`, tenantID, projectID)
	if err != nil {
		h.log.Error("progress v2: stage query failed", "error", err)
		response.InternalError(c, "Failed to compute progress")
		return
	}
	defer rows.Close()

	stages := []map[string]interface{}{}
	var totalPlan, totalDone float64
	var worksTotal, worksConfirmed, worksOverdue int
	for rows.Next() {
		var stage string
		var wTotal, wConfirmed, wStarted, wOverdue int
		var planAmt, doneAmt float64
		if err := rows.Scan(&stage, &wTotal, &wConfirmed, &wStarted, &wOverdue, &planAmt, &doneAmt); err != nil {
			continue
		}
		pct := 0.0
		if planAmt > 0 {
			pct = doneAmt / planAmt * 100
		}
		stages = append(stages, map[string]interface{}{
			"name":            stage,
			"pct":             round2(pct),
			"works_total":     wTotal,
			"works_confirmed": wConfirmed,
			"works_started":   wStarted,
			"works_overdue":   wOverdue,
			"plan_amount":     planAmt,
		})
		totalPlan += planAmt
		totalDone += doneAmt
		worksTotal += wTotal
		worksConfirmed += wConfirmed
		worksOverdue += wOverdue
	}

	projectPct := 0.0
	if totalPlan > 0 {
		projectPct = totalDone / totalPlan * 100
	}

	// Kalendar: o'tgan vaqt % va qolgan kunlar (rejaviy oynadan).
	elapsedPct := -1.0
	daysLeft := 0
	hasDates := plannedStart.valid && plannedEnd.valid
	if hasDates {
		total := plannedEnd.time.Sub(plannedStart.time).Hours() / 24
		elapsed := time.Since(plannedStart.time).Hours() / 24
		if total > 0 {
			elapsedPct = elapsed / total * 100
			if elapsedPct < 0 {
				elapsedPct = 0
			}
			if elapsedPct > 100 {
				elapsedPct = 100
			}
		}
		daysLeft = int(time.Until(plannedEnd.time).Hours() / 24)
	}

	// Byudjet-mini: reja = smeta ishlar jami; fakt = approved CEL (real pul).
	var actualCost float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(amount), 0)
		FROM construction_expense_lines
		WHERE project_id = $1 AND tenant_id = $2 AND status = 'approved' AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&actualCost)

	out := map[string]interface{}{
		"project_pct":     round2(projectPct),
		"stages":          stages,
		"works_total":     worksTotal,
		"works_confirmed": worksConfirmed,
		"works_overdue":   worksOverdue,
		"smeta_total":     totalPlan,
		"actual_cost":     actualCost,
		"contract_amount": contractAmount.float64Val(),
		"planned_start":   plannedStart.stringVal(),
		"planned_end":     plannedEnd.stringVal(),
		"days_left":       daysLeft,
		"updated_at":      time.Now().Format(time.RFC3339),
	}
	if elapsedPct >= 0 {
		out["elapsed_pct"] = round2(elapsedPct)
	}
	response.Success(c, out)
}
