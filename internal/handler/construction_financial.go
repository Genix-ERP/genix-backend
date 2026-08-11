package handler

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION FINANCIAL ANALYSIS HANDLERS
// =====================================================

// GetProjectPnL returns P&L analysis for a construction project
func (h *Handler) GetProjectPnL(c *gin.Context) {
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

	// Pul-hisobot — sof-prorab rolga 403 (conventions §4).
	if h.denyPriceRestricted(c, tenantID, projectID) {
		return
	}

	// Get project financial basics
	var contractAmount, progressPercent sql.NullFloat64
	h.db.QueryRow(`
		SELECT contract_amount, progress_percent
		FROM construction_projects WHERE id = $1 AND tenant_id = $2
	`, projectID, tenantID).Scan(&contractAmount, &progressPercent)

	// Get current estimate total
	var estimateAmount float64
	h.db.QueryRow(`
		SELECT COALESCE(amount_total, 0)
		FROM construction_estimate
		WHERE project_id = $1 AND tenant_id = $2 AND is_current = true
	`, projectID, tenantID).Scan(&estimateAmount)

	// Get actual costs by category
	var materialCost, laborCost, equipmentCost, subcontractCost, otherCost float64
	h.db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN cc.name ILIKE '%material%' THEN el.amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cc.name ILIKE '%ish haqi%' OR cc.name ILIKE '%labor%' THEN el.amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cc.name ILIKE '%texnika%' OR cc.name ILIKE '%equipment%' THEN el.amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cc.name ILIKE '%pudrat%' OR cc.name ILIKE '%subcontract%' THEN el.amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cc.name NOT ILIKE '%material%' AND cc.name NOT ILIKE '%ish haqi%' AND cc.name NOT ILIKE '%labor%' AND cc.name NOT ILIKE '%texnika%' AND cc.name NOT ILIKE '%equipment%' AND cc.name NOT ILIKE '%pudrat%' AND cc.name NOT ILIKE '%subcontract%' THEN el.amount ELSE 0 END), 0)
		FROM construction_expense_lines el
		LEFT JOIN construction_cost_categories cc ON cc.id = el.cost_category_id
		WHERE el.project_id = $1 AND el.tenant_id = $2 AND el.status = 'approved' AND el.deleted_at IS NULL
	`, projectID, tenantID).Scan(&materialCost, &laborCost, &equipmentCost, &subcontractCost, &otherCost)

	// Add approved KS-2 acts to subcontract cost. Since migration 466 an
	// approved sub-act also writes its own construction_expense_lines row
	// (expense_line_id back-link) — those are already inside the CEL sum
	// above, so only count acts WITHOUT a CEL bridge here (double-count fix,
	// docs/qurilish-audit.md §2).
	var ks2Total float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(amount_total), 0)
		FROM construction_act
		WHERE project_id = $1 AND tenant_id = $2 AND act_type = 'ks2' AND state = 'approved'
		  AND expense_line_id IS NULL
	`, projectID, tenantID).Scan(&ks2Total)
	subcontractCost += ks2Total

	actualCost := materialCost + laborCost + equipmentCost + subcontractCost + otherCost

	// Calculate forecasts
	progress := progressPercent.Float64
	var estimatedAtCompletion float64
	if progress > 0 {
		estimatedAtCompletion = (actualCost / progress) * 100
	} else {
		estimatedAtCompletion = estimateAmount
	}

	contract := contractAmount.Float64
	profitForecast := contract - estimatedAtCompletion
	var marginPct float64
	if contract > 0 {
		marginPct = (profitForecast / contract) * 100
	}

	remainingBudget := estimateAmount - actualCost

	response.Success(c, map[string]interface{}{
		"contract_amount":         contract,
		"estimate_amount":         estimateAmount,
		"actual_cost":             actualCost,
		"remaining_budget":        remainingBudget,
		"estimated_at_completion": estimatedAtCompletion,
		"profit_forecast":         profitForecast,
		"margin_pct":              round2(marginPct),
		"progress_percent":        progress,
		"cost_breakdown": map[string]interface{}{
			"material":    materialCost,
			"labor":       laborCost,
			"equipment":   equipmentCost,
			"subcontract": subcontractCost,
			"other":       otherCost,
		},
	})
}

// GetBudgetVsActualByWBS returns budget vs actual comparison per WBS item
func (h *Handler) GetBudgetVsActualByWBS(c *gin.Context) {
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

	// Pul-hisobot — sof-prorab rolga 403 (conventions §4).
	if h.denyPriceRestricted(c, tenantID, projectID) {
		return
	}

	query := `
		SELECT w.id, w.code, w.name, w.budget_amount,
		       COALESCE(el_total.planned, 0) as planned_from_estimate,
		       COALESCE(exp_total.actual, 0) as actual_cost
		FROM construction_wbs w
		LEFT JOIN LATERAL (
			SELECT SUM(el.total_amount) as planned
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE el.wbs_id = w.id AND e.is_current = true AND e.tenant_id = $2
		) el_total ON true
		LEFT JOIN LATERAL (
			SELECT SUM(ex.amount) as actual
			FROM construction_expense_lines ex
			WHERE ex.wbs_id = w.id AND ex.status = 'approved' AND ex.deleted_at IS NULL AND ex.tenant_id = $2
		) exp_total ON true
		WHERE w.project_id = $1 AND w.tenant_id = $2 AND w.is_active = true
		ORDER BY w.sort_order, w.code
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to get budget vs actual", "error", err)
		response.InternalError(c, "Failed to get budget vs actual")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var code, name string
		var budgetAmount, planned, actual float64

		if err := rows.Scan(&id, &code, &name, &budgetAmount, &planned, &actual); err != nil {
			h.log.Error("Failed to scan budget vs actual", "error", err)
			continue
		}

		budget := budgetAmount
		if budget == 0 {
			budget = planned
		}

		variance := budget - actual
		var variancePct float64
		if budget > 0 {
			variancePct = (variance / budget) * 100
		}

		var status string
		if budget > 0 {
			usage := (actual / budget) * 100
			if usage > 100 {
				status = "over_budget"
			} else if usage > 90 {
				status = "warning"
			} else {
				status = "normal"
			}
		} else {
			status = "normal"
		}

		items = append(items, map[string]interface{}{
			"wbs_id":       id,
			"code":         code,
			"name":         name,
			"planned":      budget,
			"actual":       actual,
			"variance":     variance,
			"variance_pct": round2(variancePct),
			"status":       status,
		})
	}

	response.Success(c, items)
}

// GetCostTrend returns monthly cost data for trend analysis
func (h *Handler) GetCostTrend(c *gin.Context) {
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

	// Pul-hisobot — sof-prorab rolga 403 (conventions §4).
	if h.denyPriceRestricted(c, tenantID, projectID) {
		return
	}

	query := `
		SELECT DATE_TRUNC('month', el.expense_date) as month,
		       SUM(el.amount) as total,
		       SUM(SUM(el.amount)) OVER (ORDER BY DATE_TRUNC('month', el.expense_date)) as cumulative
		FROM construction_expense_lines el
		WHERE el.project_id = $1 AND el.tenant_id = $2 AND el.status = 'approved' AND el.deleted_at IS NULL
		GROUP BY DATE_TRUNC('month', el.expense_date)
		ORDER BY month
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to get cost trend", "error", err)
		response.InternalError(c, "Failed to get cost trend")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var month time.Time
		var total, cumulative float64

		if err := rows.Scan(&month, &total, &cumulative); err != nil {
			h.log.Error("Failed to scan cost trend", "error", err)
			continue
		}

		items = append(items, map[string]interface{}{
			"month":      month.Format("2006-01"),
			"total":      total,
			"cumulative": cumulative,
		})
	}

	response.Success(c, items)
}

// GetPaymentSchedule returns subcontractor payment milestones
func (h *Handler) GetPaymentSchedule(c *gin.Context) {
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

	// Pul-hisobot — sof-prorab rolga 403 (conventions §4).
	if h.denyPriceRestricted(c, tenantID, projectID) {
		return
	}

	query := `
		SELECT s.id, s.name as subcontract_name,
		       COALESCE(p.name, '') as partner_name,
		       s.amount as contract_amount,
		       COALESCE(ks2.approved_total, 0) as completed_amount,
		       COALESCE(ks3.paid_total, 0) as paid_amount,
		       s.state
		FROM construction_subcontract s
		LEFT JOIN organizations p ON p.id = s.partner_id
		LEFT JOIN LATERAL (
			SELECT SUM(amount_total) as approved_total
			FROM construction_act
			WHERE subcontract_id = s.id AND act_type = 'ks2' AND state = 'approved'
		) ks2 ON true
		LEFT JOIN LATERAL (
			SELECT SUM(amount_total) as paid_total
			FROM construction_act
			WHERE subcontract_id = s.id AND act_type = 'ks3' AND state = 'approved'
		) ks3 ON true
		WHERE s.project_id = $1 AND s.tenant_id = $2
		ORDER BY s.created_date
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to get payment schedule", "error", err)
		response.InternalError(c, "Failed to get payment schedule")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var subName, partnerName, state string
		var contractAmount, completedAmount, paidAmount float64

		if err := rows.Scan(&id, &subName, &partnerName, &contractAmount,
			&completedAmount, &paidAmount, &state); err != nil {
			h.log.Error("Failed to scan payment schedule", "error", err)
			continue
		}

		outstanding := completedAmount - paidAmount
		if outstanding < 0 {
			outstanding = 0
		}

		items = append(items, map[string]interface{}{
			"subcontract_id":   id,
			"subcontract_name": subName,
			"partner_name":     partnerName,
			"contract_amount":  contractAmount,
			"completed_amount": completedAmount,
			"paid_amount":      paidAmount,
			"outstanding":      outstanding,
			"state":            state,
		})
	}

	response.Success(c, items)
}
