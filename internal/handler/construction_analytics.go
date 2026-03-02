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
