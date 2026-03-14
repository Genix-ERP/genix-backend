package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION ACTIVITY LOG HANDLERS
// =====================================================

// ListConstructionActivityLog returns paginated activity log for a project
func (h *Handler) ListConstructionActivityLog(c *gin.Context) {
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

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Optional filters
	actionType := c.Query("action_type")

	// Count query
	countQuery := `SELECT COUNT(*) FROM construction_activity_log WHERE project_id = $1 AND tenant_id = $2`
	countArgs := []interface{}{projectID, tenantID}
	countArgN := 2

	if actionType != "" {
		countArgN++
		countQuery += fmt.Sprintf(" AND action_type = $%d", countArgN)
		countArgs = append(countArgs, actionType)
	}

	var total int
	if err := h.db.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		h.log.Error("Failed to count activity logs", "error", err)
		response.InternalError(c, "Failed to count activity logs")
		return
	}

	// Data query
	query := `
		SELECT a.id, a.tenant_id, a.project_id, a.user_id,
		       a.action_type, a.description, a.related_model, a.related_id,
		       a.metadata, a.created_at,
		       COALESCE(u.first_name || ' ' || u.last_name, '') as user_name
		FROM construction_activity_log a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.project_id = $1 AND a.tenant_id = $2
	`

	args := []interface{}{projectID, tenantID}
	argCount := 2

	if actionType != "" {
		argCount++
		query += fmt.Sprintf(" AND a.action_type = $%d", argCount)
		args = append(args, actionType)
	}

	query += fmt.Sprintf(" ORDER BY a.created_at DESC LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list activity logs", "error", err)
		response.InternalError(c, "Failed to list activity logs")
		return
	}
	defer rows.Close()

	items := []entity.ConstructionActivityLog{}
	for rows.Next() {
		var item entity.ConstructionActivityLog
		var metadata sql.NullString
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.ProjectID, &item.UserID,
			&item.ActionType, &item.Description, &item.RelatedModel, &item.RelatedID,
			&metadata, &item.CreatedAt,
			&item.UserName,
		); err != nil {
			h.log.Error("Failed to scan activity log", "error", err)
			continue
		}
		if metadata.Valid {
			item.Metadata = json.RawMessage(metadata.String)
		}
		items = append(items, item)
	}

	response.Paginated(c, items, page, limit, total)
}

// logConstructionActivity creates an activity log entry (internal helper)
func (h *Handler) logConstructionActivity(tenantID uuid.UUID, projectID int64, userID uuid.UUID, actionType, description, relatedModel string, relatedID int64) {
	_, err := h.db.Exec(
		`INSERT INTO construction_activity_log (tenant_id, project_id, user_id, action_type, description, related_model, related_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`,
		tenantID, projectID, userID, actionType, description, relatedModel, relatedID,
	)
	if err != nil {
		h.log.Error("Failed to log construction activity", "error", err, "project_id", projectID, "action", actionType)
	}
}

// =====================================================
// SMETA VS FACT ANALYTICS (Section 8 of TZ)
// =====================================================

// GetSmetaVsFact returns estimate vs actual comparison data from signed Forma 2 acts
func (h *Handler) GetSmetaVsFact(c *gin.Context) {
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

	stageFilter := c.Query("stage_id")
	statusFilter := c.Query("status") // over, warn, ok, zero
	searchFilter := c.Query("search")

	// Find the current/approved estimate for this project
	var estimateID int64
	err = h.db.QueryRow(`
		SELECT id FROM construction_estimate
		WHERE project_id = $1 AND tenant_id = $2 AND state = 'approved'
		ORDER BY created_date DESC LIMIT 1
	`, projectID, tenantID).Scan(&estimateID)
	if err != nil {
		// Try any estimate
		err = h.db.QueryRow(`
			SELECT id FROM construction_estimate
			WHERE project_id = $1 AND tenant_id = $2
			ORDER BY created_date DESC LIMIT 1
		`, projectID, tenantID).Scan(&estimateID)
		if err != nil {
			response.Success(c, map[string]interface{}{
				"summary":  map[string]interface{}{"total_smeta": 0, "total_fact": 0, "completion_pct": 0, "delta": 0, "overrun_count": 0, "not_started_count": 0},
				"sections": []interface{}{},
			})
			return
		}
	}

	// Query estimate lines with actual quantities from signed KS-2
	query := `
		SELECT el.id, el.name, el.uom, COALESCE(el.quantity, 0) as qty_smeta,
		       el.unit_rate, COALESCE(el.total_amount, 0) as smeta_amount,
		       COALESCE(fact.qty_fact, 0) as qty_fact,
		       COALESCE(fact.fact_amount, 0) as fact_amount,
		       el.sort_order,
		       COALESCE(e.name, 'Прочее') as section_name
		FROM construction_estimate_line el
		LEFT JOIN (
			SELECT al.estimate_line_id,
			       SUM(al.quantity) as qty_fact,
			       SUM(al.total_amount) as fact_amount
			FROM construction_act_line al
			JOIN construction_act a ON a.id = al.act_id
			WHERE a.act_type = 'ks2' AND a.state = 'signed'
			  AND a.project_id = $2 AND a.tenant_id = $3
			GROUP BY al.estimate_line_id
		) fact ON fact.estimate_line_id = el.id
		LEFT JOIN construction_estimate e ON e.id = el.estimate_id
		WHERE el.estimate_id = $1
	`
	args := []interface{}{estimateID, projectID, tenantID}
	argCount := 3

	if stageFilter != "" {
		// Filter by stage via WBS linkage if available
		argCount++
		query += fmt.Sprintf(` AND el.wbs_id IN (SELECT w.id FROM construction_wbs w WHERE w.stage_id = $%d)`, argCount)
		stageID, _ := strconv.ParseInt(stageFilter, 10, 64)
		args = append(args, stageID)
	}
	if searchFilter != "" {
		argCount++
		query += fmt.Sprintf(` AND LOWER(el.name) LIKE LOWER('%%' || $%d || '%%')`, argCount)
		args = append(args, searchFilter)
	}

	query += " ORDER BY el.sort_order, el.id"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to get smeta vs fact", "error", err)
		response.InternalError(c, "Failed to get smeta vs fact data")
		return
	}
	defer rows.Close()

	type item struct {
		ID            int64   `json:"smeta_item_id"`
		Name          string  `json:"name"`
		UOM           string  `json:"unit"`
		QtySmeta      float64 `json:"qty_smeta"`
		UnitPrice     float64 `json:"unit_price"`
		SmetaAmount   float64 `json:"smeta_sum"`
		QtyFact       float64 `json:"qty_fact"`
		FactAmount    float64 `json:"fact_sum"`
		CompletionPct float64 `json:"completion_pct"`
		Status        string  `json:"status"`
		Delta         float64 `json:"delta"`
		SectionName   string  `json:"-"`
	}

	var allItems []item
	var totalSmeta, totalFact float64
	var overrunCount, notStartedCount int

	for rows.Next() {
		var it item
		var sortOrder int
		if rows.Scan(&it.ID, &it.Name, &it.UOM, &it.QtySmeta, &it.UnitPrice,
			&it.SmetaAmount, &it.QtyFact, &it.FactAmount, &sortOrder, &it.SectionName) != nil {
			continue
		}

		// Calculate completion and status
		if it.QtySmeta > 0 {
			it.CompletionPct = (it.QtyFact / it.QtySmeta) * 100
		}
		it.Delta = it.FactAmount - it.SmetaAmount

		if it.QtyFact == 0 {
			it.Status = "not_started"
			notStartedCount++
		} else if it.QtyFact > it.QtySmeta*1.05 {
			it.Status = "overrun"
			overrunCount++
		} else if it.QtyFact > it.QtySmeta*0.9 {
			it.Status = "warn"
		} else if it.QtyFact == it.QtySmeta {
			it.Status = "completed"
		} else {
			it.Status = "ok"
		}

		// Apply status filter
		if statusFilter != "" {
			if statusFilter == "over" && it.Status != "overrun" {
				continue
			}
			if statusFilter == "warn" && it.Status != "warn" {
				continue
			}
			if statusFilter == "ok" && it.Status != "ok" && it.Status != "completed" {
				continue
			}
			if statusFilter == "zero" && it.Status != "not_started" {
				continue
			}
		}

		totalSmeta += it.SmetaAmount
		totalFact += it.FactAmount
		allItems = append(allItems, it)
	}

	// Group by section
	sectionMap := make(map[string][]map[string]interface{})
	sectionOrder := []string{}
	for _, it := range allItems {
		if _, exists := sectionMap[it.SectionName]; !exists {
			sectionOrder = append(sectionOrder, it.SectionName)
		}
		sectionMap[it.SectionName] = append(sectionMap[it.SectionName], map[string]interface{}{
			"smeta_item_id":  it.ID,
			"name":           it.Name,
			"unit":           it.UOM,
			"qty_smeta":      it.QtySmeta,
			"unit_price":     it.UnitPrice,
			"smeta_sum":      it.SmetaAmount,
			"qty_fact":       it.QtyFact,
			"fact_sum":       it.FactAmount,
			"completion_pct": it.CompletionPct,
			"status":         it.Status,
			"delta":          it.Delta,
		})
	}

	sections := []map[string]interface{}{}
	for _, secName := range sectionOrder {
		sections = append(sections, map[string]interface{}{
			"section_name": secName,
			"items":        sectionMap[secName],
		})
	}

	var completionPct float64
	if totalSmeta > 0 {
		completionPct = (totalFact / totalSmeta) * 100
	}

	response.Success(c, map[string]interface{}{
		"summary": map[string]interface{}{
			"total_smeta":       totalSmeta,
			"total_fact":        totalFact,
			"completion_pct":    completionPct,
			"delta":             totalFact - totalSmeta,
			"overrun_count":     overrunCount,
			"not_started_count": notStartedCount,
		},
		"sections": sections,
	})
}
