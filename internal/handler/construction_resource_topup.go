package handler

import (
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION RESOURCE TOP-UP HANDLERS  (migration 358)
// =====================================================
//
// Top-ups capture additional purchases for a smeta resource sub-line
// when the foreman runs short and re-orders at a (typically different)
// price. The original smeta plan stays untouched — we record each
// top-up as its own row in construction_resource_topup so the
// effective cost becomes:
//
//     line.quantity × line.unit_rate                       (planned)
//   + Σ (topup.extra_quantity × topup.new_price)           (top-ups)
//
// Routes (registered in handler.go):
//   POST   /construction/estimates/:id/lines/:line_id/topups
//   DELETE /construction/estimates/:id/lines/:line_id/topups/:topup_id

// CreateResourceTopup appends a new top-up to an estimate resource line.
// The estimate must be in 'draft' state; only resource sub-lines (rows
// with parent_line_id set, or with resource_type ∈ material/labor/equipment)
// are valid targets.
func (h *Handler) CreateResourceTopup(c *gin.Context) {
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
	lineID, err := strconv.ParseInt(c.Param("line_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}

	var req entity.CreateResourceTopupInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if req.ExtraQuantity <= 0 {
		response.BadRequest(c, "extra_quantity must be > 0")
		return
	}
	if req.NewPrice < 0 {
		response.BadRequest(c, "new_price must be >= 0")
		return
	}

	// Validate the line: must belong to the estimate, the estimate must
	// be draft, and the line should be a resource sub-line.
	var (
		state        string
		projectID    int64
		resourceType string
		lineName     string
	)
	err = h.db.QueryRow(`
		SELECT e.state, e.project_id,
		       COALESCE(l.resource_type, ''), COALESCE(l.name, '')
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE l.id = $1 AND l.estimate_id = $2 AND l.tenant_id = $3
	`, lineID, estimateID, tenantID).Scan(&state, &projectID, &resourceType, &lineName)
	if err != nil {
		response.NotFound(c, "Estimate line not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	// Parse ordered_at — accept either ISO date or empty (DB defaults to today).
	var orderedAt interface{}
	if s := strings.TrimSpace(req.OrderedAt); s != "" {
		t, parseErr := time.Parse("2006-01-02", s)
		if parseErr != nil {
			response.BadRequest(c, "ordered_at must be YYYY-MM-DD")
			return
		}
		orderedAt = t
	}

	// Identify the actor for the audit / created_by fields.
	userID, _ := middleware.GetUserID(c)
	var createdByVal interface{}
	if userID != uuid.Nil {
		createdByVal = userID
	}

	var topup entity.ResourceTopup
	if orderedAt == nil {
		err = h.db.QueryRow(`
			INSERT INTO construction_resource_topup (
			    tenant_id, estimate_line_id,
			    extra_quantity, new_price,
			    note, created_by
			) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)
			RETURNING id, tenant_id, estimate_line_id,
			          extra_quantity, new_price, ordered_at,
			          COALESCE(note, ''), created_by, created_date
		`, tenantID, lineID, req.ExtraQuantity, req.NewPrice,
			strings.TrimSpace(req.Note), createdByVal,
		).Scan(
			&topup.ID, &topup.TenantID, &topup.EstimateLineID,
			&topup.ExtraQuantity, &topup.NewPrice, &topup.OrderedAt,
			&topup.Note, &topup.CreatedBy, &topup.CreatedDate,
		)
	} else {
		err = h.db.QueryRow(`
			INSERT INTO construction_resource_topup (
			    tenant_id, estimate_line_id,
			    extra_quantity, new_price, ordered_at,
			    note, created_by
			) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)
			RETURNING id, tenant_id, estimate_line_id,
			          extra_quantity, new_price, ordered_at,
			          COALESCE(note, ''), created_by, created_date
		`, tenantID, lineID, req.ExtraQuantity, req.NewPrice, orderedAt,
			strings.TrimSpace(req.Note), createdByVal,
		).Scan(
			&topup.ID, &topup.TenantID, &topup.EstimateLineID,
			&topup.ExtraQuantity, &topup.NewPrice, &topup.OrderedAt,
			&topup.Note, &topup.CreatedBy, &topup.CreatedDate,
		)
	}
	if err != nil {
		h.log.Error("Failed to insert resource topup", "error", err,
			"estimate_id", estimateID, "line_id", lineID)
		response.InternalError(c, "Failed to create top-up")
		return
	}

	// Roll the new top-up into the estimate's amount_total (recalc reads
	// the topup table directly — see recalculateEstimateTotals).
	h.recalculateEstimateTotals(estimateID)

	// Audit trail. Use existing 'res_add' style for the line's audit feed.
	userName := c.GetString("user_name")
	toVal := strconv.FormatFloat(req.ExtraQuantity, 'f', -1, 64) + " × " +
		strconv.FormatFloat(req.NewPrice, 'f', -1, 64)
	desc := lineName + " uchun qo'shimcha buyurtma: " + toVal
	if topup.Note != "" {
		desc += " (" + topup.Note + ")"
	}
	h.logSmetaAudit(tenantID, projectID, &estimateID, "topup_add", "topup",
		&lineID, "", toVal, desc, userID, userName)

	response.Success(c, topup)
}

// DeleteResourceTopup removes a top-up and recomputes the estimate totals.
func (h *Handler) DeleteResourceTopup(c *gin.Context) {
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
	lineID, err := strconv.ParseInt(c.Param("line_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}
	topupID, err := strconv.ParseInt(c.Param("topup_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid top-up ID")
		return
	}

	// Confirm the estimate is editable, fetch values for audit before delete.
	var (
		state         string
		projectID     int64
		extraQuantity float64
		newPrice      float64
		lineName      string
	)
	err = h.db.QueryRow(`
		SELECT e.state, e.project_id,
		       t.extra_quantity, t.new_price,
		       COALESCE(l.name, '')
		FROM construction_resource_topup t
		JOIN construction_estimate_line l ON l.id = t.estimate_line_id
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE t.id = $1 AND t.estimate_line_id = $2
		  AND l.estimate_id = $3 AND t.tenant_id = $4
	`, topupID, lineID, estimateID, tenantID).Scan(
		&state, &projectID, &extraQuantity, &newPrice, &lineName,
	)
	if err != nil {
		response.NotFound(c, "Top-up not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	res, err := h.db.Exec(`
		DELETE FROM construction_resource_topup
		WHERE id = $1 AND estimate_line_id = $2 AND tenant_id = $3
	`, topupID, lineID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete resource topup", "error", err)
		response.InternalError(c, "Failed to delete top-up")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Top-up not found")
		return
	}

	h.recalculateEstimateTotals(estimateID)

	userID, _ := middleware.GetUserID(c)
	userName := c.GetString("user_name")
	fromVal := strconv.FormatFloat(extraQuantity, 'f', -1, 64) + " × " +
		strconv.FormatFloat(newPrice, 'f', -1, 64)
	desc := lineName + " uchun qo'shimcha buyurtma o'chirildi: " + fromVal
	h.logSmetaAudit(tenantID, projectID, &estimateID, "topup_del", "topup",
		&lineID, fromVal, "", desc, userID, userName)

	response.Success(c, gin.H{"deleted": topupID})
}
