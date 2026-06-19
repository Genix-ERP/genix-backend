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
// SMETA AUDIT LOG (Smeta boshqaruvi → Jurnal tab)
// =====================================================
//
// Per-estimate audit log. Every meaningful mutation done from the
// Smeta boshqaruvi UI writes one row here. The frontend "Jurnal" tab
// renders the rows newest-first with action icons + filters so the
// foreman can answer "who changed what, when?"
//
// Action enum values are documented in migration 352. Examples:
//   qty_change, price_change, mat_type, subwork_add, subwork_del,
//   res_add, res_del, reset_qty, reset_qty_all, reset_price,
//   reset_price_all, form_save, form_delete, other_pct, use_vat.

// SmetaAuditEntry mirrors a row in construction_smeta_audit.
type SmetaAuditEntry struct {
	ID          int64      `json:"id"`
	TenantID    uuid.UUID  `json:"tenant_id"`
	ProjectID   int64      `json:"project_id"`
	EstimateID  *int64     `json:"estimate_id"`
	Action      string     `json:"action"`
	Target      string     `json:"target"`
	LineID      *int64     `json:"line_id"`
	FromValue   string     `json:"from_value"`
	ToValue     string     `json:"to_value"`
	Description string     `json:"description"`
	UserID      *uuid.UUID `json:"user_id"`
	UserName    string     `json:"user_name"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ListSmetaAudit returns the audit log for an estimate (or, if estimate_id
// query param is omitted, for an entire project). Newest first.
//
// Routes:
//   GET /construction/estimates/:id/audit
//   GET /construction/projects/:id/smeta-audit
func (h *Handler) ListSmetaAudit(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	action := c.Query("action") // optional filter

	q := `
		SELECT id, tenant_id, project_id, estimate_id,
		       action, COALESCE(target, ''), line_id,
		       COALESCE(from_value, ''), COALESCE(to_value, ''),
		       COALESCE(description, ''),
		       user_id, COALESCE(user_name, ''),
		       created_at
		FROM construction_smeta_audit
		WHERE tenant_id = $1 AND estimate_id = $2
	`
	args := []interface{}{tenantID, estimateID}
	if action != "" {
		q += " AND action = $3"
		args = append(args, action)
	}
	q += " ORDER BY created_at DESC LIMIT " + strconv.Itoa(limit)

	rows, err := h.db.Query(q, args...)
	if err != nil {
		h.log.Error("Failed to list smeta audit", "error", err)
		response.InternalError(c, "Failed to list audit log")
		return
	}
	defer rows.Close()

	out := []SmetaAuditEntry{}
	for rows.Next() {
		var e SmetaAuditEntry
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.ProjectID, &e.EstimateID,
			&e.Action, &e.Target, &e.LineID,
			&e.FromValue, &e.ToValue,
			&e.Description,
			&e.UserID, &e.UserName,
			&e.CreatedAt,
		); err != nil {
			h.log.Error("Failed to scan smeta audit row", "error", err)
			continue
		}
		out = append(out, e)
	}
	response.Success(c, out)
}

// ListProjectSmetaAudit returns the audit log for an entire project across
// every estimate. Used by reports / aggregate views.
//
// Route: GET /construction/projects/:id/smeta-audit
func (h *Handler) ListProjectSmetaAudit(c *gin.Context) {
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

	// Pagination — default 20 per page (the Jurnal scrolls page-by-page),
	// max 200. `page` is 1-based.
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	action := c.Query("action") // optional filter, matches the per-estimate endpoint

	// Shared WHERE clause for the count + page queries.
	where := "WHERE tenant_id = $1 AND project_id = $2"
	args := []interface{}{tenantID, projectID}
	if action != "" {
		where += " AND action = $3"
		args = append(args, action)
	}

	var total int
	if err := h.db.QueryRow(
		"SELECT COUNT(*) FROM construction_smeta_audit "+where, args...,
	).Scan(&total); err != nil {
		h.log.Error("Failed to count project smeta audit", "error", err)
		response.InternalError(c, "Failed to list audit log")
		return
	}

	q := `
		SELECT id, tenant_id, project_id, estimate_id,
		       action, COALESCE(target, ''), line_id,
		       COALESCE(from_value, ''), COALESCE(to_value, ''),
		       COALESCE(description, ''),
		       user_id, COALESCE(user_name, ''),
		       created_at
		FROM construction_smeta_audit
		` + where + `
		ORDER BY created_at DESC
		LIMIT ` + strconv.Itoa(pageSize) + ` OFFSET ` + strconv.Itoa(offset) + `
	`

	rows, err := h.db.Query(q, args...)
	if err != nil {
		h.log.Error("Failed to list project smeta audit", "error", err)
		response.InternalError(c, "Failed to list audit log")
		return
	}
	defer rows.Close()

	out := []SmetaAuditEntry{}
	for rows.Next() {
		var e SmetaAuditEntry
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.ProjectID, &e.EstimateID,
			&e.Action, &e.Target, &e.LineID,
			&e.FromValue, &e.ToValue,
			&e.Description,
			&e.UserID, &e.UserName,
			&e.CreatedAt,
		); err != nil {
			continue
		}
		out = append(out, e)
	}
	response.Paginated(c, out, page, pageSize, total)
}

// =====================================================
// AUDIT WRITE HELPER
// =====================================================

// logSmetaAudit inserts one row into construction_smeta_audit. Best-effort:
// audit failures never break the originating mutation, so callers ignore the
// returned error. Any user-name fallbacks happen here.
//
// estimateID is *int64 because some events (project-level price reset, e.g.)
// are not scoped to a single estimate.
func (h *Handler) logSmetaAudit(
	tenantID uuid.UUID,
	projectID int64,
	estimateID *int64,
	action string,
	target string,
	lineID *int64,
	fromValue string,
	toValue string,
	description string,
	userID uuid.UUID,
	userName string,
) {
	var userIDVal interface{}
	if userID != uuid.Nil {
		userIDVal = userID
	}
	if userName == "" {
		// Fallback: try to resolve from users table.
		_ = h.db.QueryRow(
			`SELECT COALESCE(first_name || ' ' || last_name, '') FROM users WHERE id = $1`,
			userIDVal,
		).Scan(&userName)
		if userName == "" {
			userName = "User"
		}
	}

	_, err := h.db.Exec(`
		INSERT INTO construction_smeta_audit (
		    tenant_id, project_id, estimate_id,
		    action, target, line_id,
		    from_value, to_value, description,
		    user_id, user_name, created_at
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, NULLIF($7, ''),
		          NULLIF($8, ''), NULLIF($9, ''), $10, $11, NOW())
	`,
		tenantID, projectID, estimateID,
		action, target, lineID,
		fromValue, toValue, description,
		userIDVal, userName,
	)
	if err != nil {
		// Don't fail the calling mutation, but log it.
		h.log.Error("Failed to write smeta audit", "error", err, "action", action)
	}
}
