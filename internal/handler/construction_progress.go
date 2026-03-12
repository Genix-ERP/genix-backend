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
// CONSTRUCTION PROGRESS VISUALIZATION HANDLERS
// =====================================================

// GetProgressSummary returns WBS-level progress comparison (plan vs fact)
func (h *Handler) GetProgressSummary(c *gin.Context) {
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

	buildingIDStr := c.Query("building_id")

	query := `
		SELECT w.id, w.code, w.name, w.parent_id, w.building_id,
		       w.progress as fact_pct,
		       w.budget_amount,
		       w.date_start_plan, w.date_end_plan,
		       w.date_start_actual, w.date_end_actual,
		       COALESCE(b.name, '') as building_name
		FROM construction_wbs w
		LEFT JOIN construction_buildings b ON b.id = w.building_id
		WHERE w.project_id = $1 AND w.tenant_id = $2 AND w.is_active = true
	`
	args := []interface{}{projectID, tenantID}
	argCount := 2

	if buildingIDStr != "" {
		argCount++
		query += " AND w.building_id = $" + strconv.Itoa(argCount)
		bID, _ := strconv.ParseInt(buildingIDStr, 10, 64)
		args = append(args, bID)
	}

	query += " ORDER BY w.sort_order, w.code"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to get progress summary", "error", err)
		response.InternalError(c, "Failed to get progress summary")
		return
	}
	defer rows.Close()

	now := time.Now()
	items := []map[string]interface{}{}

	for rows.Next() {
		var id int64
		var code, name string
		var parentID, buildingID nullableInt64
		var factPct, budgetAmount nullableFloat64
		var dateStartPlan, dateEndPlan, dateStartActual, dateEndActual nullableTime
		var buildingName string

		if err := rows.Scan(
			&id, &code, &name, &parentID, &buildingID,
			&factPct, &budgetAmount,
			&dateStartPlan, &dateEndPlan, &dateStartActual, &dateEndActual,
			&buildingName,
		); err != nil {
			h.log.Error("Failed to scan progress item", "error", err)
			continue
		}

		// Calculate plan progress based on time
		var planPct float64
		if dateStartPlan.valid && dateEndPlan.valid {
			totalDays := dateEndPlan.time.Sub(dateStartPlan.time).Hours() / 24
			elapsedDays := now.Sub(dateStartPlan.time).Hours() / 24
			if totalDays > 0 {
				planPct = (elapsedDays / totalDays) * 100
				if planPct > 100 {
					planPct = 100
				}
				if planPct < 0 {
					planPct = 0
				}
			}
		}

		fact := factPct.float64Val()
		gap := fact - planPct

		var status string
		if fact >= 100 {
			status = "completed"
		} else if dateEndPlan.valid && now.After(dateEndPlan.time) && fact < 100 {
			status = "behind"
		} else if gap >= -5 {
			status = "on_track"
		} else {
			status = "behind"
		}

		item := map[string]interface{}{
			"id":            id,
			"code":          code,
			"name":          name,
			"parent_id":     parentID.interfaceVal(),
			"building_id":   buildingID.interfaceVal(),
			"building_name": buildingName,
			"plan_pct":      round2(planPct),
			"fact_pct":      round2(fact),
			"gap":           round2(gap),
			"budget_amount": budgetAmount.float64Val(),
			"status":        status,
			"date_start_plan": dateStartPlan.stringVal(),
			"date_end_plan":   dateEndPlan.stringVal(),
			"date_start_actual": dateStartActual.stringVal(),
			"date_end_actual":   dateEndActual.stringVal(),
		}
		items = append(items, item)
	}

	response.Success(c, items)
}

// GetGanttData returns WBS data formatted for Gantt chart rendering
func (h *Handler) GetGanttData(c *gin.Context) {
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

	query := `
		SELECT w.id, w.code, w.name, w.parent_id,
		       w.date_start_plan, w.date_end_plan,
		       w.date_start_actual, w.date_end_actual,
		       w.progress, w.budget_amount,
		       COALESCE(b.name, '') as building_name
		FROM construction_wbs w
		LEFT JOIN construction_buildings b ON b.id = w.building_id
		WHERE w.project_id = $1 AND w.tenant_id = $2 AND w.is_active = true
		ORDER BY w.sort_order, w.code
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to get Gantt data", "error", err)
		response.InternalError(c, "Failed to get Gantt data")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var code, name string
		var parentID nullableInt64
		var dateStartPlan, dateEndPlan, dateStartActual, dateEndActual nullableTime
		var progress, budgetAmount nullableFloat64
		var buildingName string

		if err := rows.Scan(
			&id, &code, &name, &parentID,
			&dateStartPlan, &dateEndPlan, &dateStartActual, &dateEndActual,
			&progress, &budgetAmount, &buildingName,
		); err != nil {
			h.log.Error("Failed to scan Gantt item", "error", err)
			continue
		}

		items = append(items, map[string]interface{}{
			"id":                id,
			"code":              code,
			"name":              name,
			"parent_id":         parentID.interfaceVal(),
			"date_start_plan":   dateStartPlan.stringVal(),
			"date_end_plan":     dateEndPlan.stringVal(),
			"date_start_actual": dateStartActual.stringVal(),
			"date_end_actual":   dateEndActual.stringVal(),
			"progress":          progress.float64Val(),
			"budget_amount":     budgetAmount.float64Val(),
			"building_name":     buildingName,
		})
	}

	response.Success(c, items)
}

// GetDailyLogSummary returns WBS-level aggregated progress from daily logs
func (h *Handler) GetDailyLogSummary(c *gin.Context) {
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

	query := `
		SELECT w.id, w.code, w.name, w.progress,
		       COALESCE(dl.total_done, 0) as total_done,
		       COALESCE(dl.days_logged, 0) as days_logged,
		       dl.last_log_date,
		       COALESCE(el.smeta_qty, 0) as smeta_qty
		FROM construction_wbs w
		LEFT JOIN LATERAL (
			SELECT SUM(quantity_done) as total_done,
			       COUNT(DISTINCT date) as days_logged,
			       MAX(date) as last_log_date
			FROM construction_daily_log
			WHERE wbs_id = w.id AND tenant_id = $2
		) dl ON true
		LEFT JOIN LATERAL (
			SELECT SUM(el.quantity) as smeta_qty
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE el.wbs_id = w.id AND e.is_current = true AND e.tenant_id = $2
		) el ON true
		WHERE w.project_id = $1 AND w.tenant_id = $2 AND w.is_active = true
		ORDER BY w.sort_order, w.code
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to get daily log summary", "error", err)
		response.InternalError(c, "Failed to get daily log summary")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var code, name string
		var progress nullableFloat64
		var totalDone float64
		var daysLogged int
		var lastLogDate nullableTime
		var smetaQty float64

		if err := rows.Scan(
			&id, &code, &name, &progress,
			&totalDone, &daysLogged, &lastLogDate, &smetaQty,
		); err != nil {
			h.log.Error("Failed to scan daily log summary", "error", err)
			continue
		}

		items = append(items, map[string]interface{}{
			"wbs_id":        id,
			"code":          code,
			"name":          name,
			"progress":      progress.float64Val(),
			"total_done":    totalDone,
			"days_logged":   daysLogged,
			"last_log_date": lastLogDate.stringVal(),
			"smeta_qty":     smetaQty,
		})
	}

	response.Success(c, items)
}

// =====================================================
// HELPER TYPES for nullable scanning
// =====================================================

type nullableInt64 struct {
	val   int64
	valid bool
}

func (n *nullableInt64) Scan(src interface{}) error {
	if src == nil {
		n.valid = false
		return nil
	}
	n.valid = true
	switch v := src.(type) {
	case int64:
		n.val = v
	default:
		n.valid = false
	}
	return nil
}

func (n nullableInt64) interfaceVal() interface{} {
	if n.valid {
		return n.val
	}
	return nil
}

type nullableFloat64 struct {
	val   float64
	valid bool
}

func (n *nullableFloat64) Scan(src interface{}) error {
	if src == nil {
		n.valid = false
		return nil
	}
	n.valid = true
	switch v := src.(type) {
	case float64:
		n.val = v
	case int64:
		n.val = float64(v)
	default:
		n.valid = false
	}
	return nil
}

func (n nullableFloat64) float64Val() float64 {
	if n.valid {
		return n.val
	}
	return 0
}

type nullableTime struct {
	time  time.Time
	valid bool
}

func (n *nullableTime) Scan(src interface{}) error {
	if src == nil {
		n.valid = false
		return nil
	}
	n.valid = true
	switch v := src.(type) {
	case time.Time:
		n.time = v
	default:
		n.valid = false
	}
	return nil
}

func (n nullableTime) stringVal() interface{} {
	if n.valid {
		return n.time.Format("2006-01-02")
	}
	return nil
}

func round2(f float64) float64 {
	return float64(int(f*100)) / 100
}
