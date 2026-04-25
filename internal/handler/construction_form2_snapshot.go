package handler

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// FORMA 2 SNAPSHOT (Smeta boshqaruvi → Tarix tab)
// =====================================================
//
// Saved versions of a Forma 2 (KS-2) document for an estimate. Each snapshot
// captures:
//   * the period (date range) the report covers
//   * other_costs_pct + use_vat in effect at save time
//   * pre-computed totals so the History list renders without unpacking JSON
//   * the full JSON state of the lines + summary (so re-opening the snapshot
//     reproduces the document even if the underlying estimate later changes)
//
// Schema lives in migration 351_construction_form2_snapshot.sql.

// Form2Snapshot mirrors a row in construction_form2_snapshot.
type Form2Snapshot struct {
	ID                int64           `json:"id"`
	TenantID          uuid.UUID       `json:"tenant_id"`
	ProjectID         int64           `json:"project_id"`
	EstimateID        int64           `json:"estimate_id"`
	PeriodFrom        *time.Time      `json:"period_from"`
	PeriodTo          *time.Time      `json:"period_to"`
	OtherCostsPct     float64         `json:"other_costs_pct"`
	UseVat            bool            `json:"use_vat"`
	TotalWithVat      float64         `json:"total_with_vat"`
	TotalWithoutVat   float64         `json:"total_without_vat"`
	ConstructionTotal float64         `json:"construction_total"`
	EquipmentTotal    float64         `json:"equipment_total"`
	ActNumber         string          `json:"act_number"`
	SnapshotData      json.RawMessage `json:"snapshot_data"`
	CreatedBy         *uuid.UUID      `json:"created_by"`
	CreatedByName     string          `json:"created_by_name"`
	CreatedAt         time.Time       `json:"created_at"`
}

// CreateForm2SnapshotInput is the request body for POST .../snapshots.
type CreateForm2SnapshotInput struct {
	PeriodFrom        *string         `json:"period_from"`        // ISO date "YYYY-MM-DD" or null
	PeriodTo          *string         `json:"period_to"`          //
	OtherCostsPct     float64         `json:"other_costs_pct"`
	UseVat            *bool           `json:"use_vat"`
	TotalWithVat      float64         `json:"total_with_vat"`
	TotalWithoutVat   float64         `json:"total_without_vat"`
	ConstructionTotal float64         `json:"construction_total"`
	EquipmentTotal    float64         `json:"equipment_total"`
	ActNumber         string          `json:"act_number"`
	SnapshotData      json.RawMessage `json:"snapshot_data"`
}

// ListForm2Snapshots returns every saved snapshot for an estimate, newest first.
//
// Route: GET /construction/estimates/:id/form2-snapshots
func (h *Handler) ListForm2Snapshots(c *gin.Context) {
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

	rows, err := h.db.Query(`
		SELECT s.id, s.tenant_id, s.project_id, s.estimate_id,
		       s.period_from, s.period_to,
		       s.other_costs_pct, s.use_vat,
		       s.total_with_vat, s.total_without_vat,
		       s.construction_total, s.equipment_total,
		       COALESCE(s.act_number, ''),
		       s.created_by, s.created_at,
		       COALESCE(u.first_name || ' ' || u.last_name, '') AS created_name
		FROM construction_form2_snapshot s
		LEFT JOIN users u ON u.id = s.created_by
		WHERE s.estimate_id = $1 AND s.tenant_id = $2
		ORDER BY s.created_at DESC
	`, estimateID, tenantID)
	if err != nil {
		h.log.Error("Failed to list form2 snapshots", "error", err)
		response.InternalError(c, "Failed to list snapshots")
		return
	}
	defer rows.Close()

	out := []Form2Snapshot{}
	for rows.Next() {
		var s Form2Snapshot
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.ProjectID, &s.EstimateID,
			&s.PeriodFrom, &s.PeriodTo,
			&s.OtherCostsPct, &s.UseVat,
			&s.TotalWithVat, &s.TotalWithoutVat,
			&s.ConstructionTotal, &s.EquipmentTotal,
			&s.ActNumber,
			&s.CreatedBy, &s.CreatedAt,
			&s.CreatedByName,
		); err != nil {
			h.log.Error("Failed to scan form2 snapshot row", "error", err)
			continue
		}
		// SnapshotData omitted from list endpoint to keep the payload small.
		// Frontend requests it via GetForm2Snapshot when re-opening a row.
		s.SnapshotData = json.RawMessage("null")
		out = append(out, s)
	}

	response.Success(c, out)
}

// GetForm2Snapshot returns a single snapshot including the full JSON payload.
//
// Route: GET /construction/form2-snapshots/:snapshot_id
func (h *Handler) GetForm2Snapshot(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	snapshotID, err := strconv.ParseInt(c.Param("snapshot_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid snapshot ID")
		return
	}

	var s Form2Snapshot
	var dataStr string
	err = h.db.QueryRow(`
		SELECT s.id, s.tenant_id, s.project_id, s.estimate_id,
		       s.period_from, s.period_to,
		       s.other_costs_pct, s.use_vat,
		       s.total_with_vat, s.total_without_vat,
		       s.construction_total, s.equipment_total,
		       COALESCE(s.act_number, ''),
		       COALESCE(s.snapshot_data::text, '{}'),
		       s.created_by, s.created_at,
		       COALESCE(u.first_name || ' ' || u.last_name, '') AS created_name
		FROM construction_form2_snapshot s
		LEFT JOIN users u ON u.id = s.created_by
		WHERE s.id = $1 AND s.tenant_id = $2
	`, snapshotID, tenantID).Scan(
		&s.ID, &s.TenantID, &s.ProjectID, &s.EstimateID,
		&s.PeriodFrom, &s.PeriodTo,
		&s.OtherCostsPct, &s.UseVat,
		&s.TotalWithVat, &s.TotalWithoutVat,
		&s.ConstructionTotal, &s.EquipmentTotal,
		&s.ActNumber,
		&dataStr,
		&s.CreatedBy, &s.CreatedAt,
		&s.CreatedByName,
	)
	if err != nil {
		response.NotFound(c, "Snapshot not found")
		return
	}
	s.SnapshotData = json.RawMessage(dataStr)
	response.Success(c, s)
}

// CreateForm2Snapshot saves a new immutable snapshot of the current Forma 2
// state for an estimate. Also writes a `form_save` row to construction_smeta_audit.
//
// Route: POST /construction/estimates/:id/form2-snapshots
func (h *Handler) CreateForm2Snapshot(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	userName := c.GetString("user_name")

	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	// Resolve project_id from the estimate (and confirm tenant ownership).
	var projectID int64
	err = h.db.QueryRow(
		`SELECT project_id FROM construction_estimate WHERE id = $1 AND tenant_id = $2`,
		estimateID, tenantID,
	).Scan(&projectID)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}

	var in CreateForm2SnapshotInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Defaults
	useVat := true
	if in.UseVat != nil {
		useVat = *in.UseVat
	}

	// Empty/nil JSONB defaults to {}.
	if len(in.SnapshotData) == 0 {
		in.SnapshotData = json.RawMessage("{}")
	}

	// Normalise dates: empty string → NULL.
	var periodFrom, periodTo interface{}
	if in.PeriodFrom != nil && *in.PeriodFrom != "" {
		periodFrom = *in.PeriodFrom
	}
	if in.PeriodTo != nil && *in.PeriodTo != "" {
		periodTo = *in.PeriodTo
	}

	var createdByVal interface{}
	if userID != uuid.Nil {
		createdByVal = userID
	}

	var snapshotID int64
	var createdAt time.Time
	err = h.db.QueryRow(`
		INSERT INTO construction_form2_snapshot (
		    tenant_id, project_id, estimate_id,
		    period_from, period_to,
		    other_costs_pct, use_vat,
		    total_with_vat, total_without_vat,
		    construction_total, equipment_total,
		    act_number, snapshot_data,
		    created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, ''),
		          COALESCE($13::jsonb, '{}'::jsonb), $14, NOW())
		RETURNING id, created_at
	`,
		tenantID, projectID, estimateID,
		periodFrom, periodTo,
		in.OtherCostsPct, useVat,
		in.TotalWithVat, in.TotalWithoutVat,
		in.ConstructionTotal, in.EquipmentTotal,
		in.ActNumber, []byte(in.SnapshotData),
		createdByVal,
	).Scan(&snapshotID, &createdAt)
	if err != nil {
		h.log.Error("Failed to create form2 snapshot", "error", err)
		response.InternalError(c, "Failed to save snapshot")
		return
	}

	// Audit: form_save event.
	desc := "Forma 2 saqlandi"
	if in.ActNumber != "" {
		desc = "Forma 2 saqlandi (Akt #" + in.ActNumber + ")"
	}
	h.logSmetaAudit(tenantID, projectID, &estimateID, "form_save", in.ActNumber, nil,
		"", strconv.FormatInt(snapshotID, 10), desc, userID, userName)

	response.Created(c, gin.H{
		"id":         snapshotID,
		"created_at": createdAt,
	})
}

// DeleteForm2Snapshot removes a saved snapshot. Snapshots themselves are
// immutable; deletion is allowed so foremen can prune obsolete drafts.
//
// Route: DELETE /construction/form2-snapshots/:snapshot_id
func (h *Handler) DeleteForm2Snapshot(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	userName := c.GetString("user_name")

	snapshotID, err := strconv.ParseInt(c.Param("snapshot_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid snapshot ID")
		return
	}

	// Capture context for the audit row before we drop the row.
	var (
		projectID  int64
		estimateID int64
		actNumber  sql.NullString
	)
	err = h.db.QueryRow(`
		SELECT project_id, estimate_id, act_number
		FROM construction_form2_snapshot
		WHERE id = $1 AND tenant_id = $2
	`, snapshotID, tenantID).Scan(&projectID, &estimateID, &actNumber)
	if err != nil {
		response.NotFound(c, "Snapshot not found")
		return
	}

	if _, err := h.db.Exec(
		`DELETE FROM construction_form2_snapshot WHERE id = $1 AND tenant_id = $2`,
		snapshotID, tenantID,
	); err != nil {
		h.log.Error("Failed to delete form2 snapshot", "error", err)
		response.InternalError(c, "Failed to delete snapshot")
		return
	}

	h.logSmetaAudit(tenantID, projectID, &estimateID, "form_delete", actNumber.String, nil,
		strconv.FormatInt(snapshotID, 10), "", "Forma 2 o'chirildi",
		userID, userName)

	response.Success(c, gin.H{"id": snapshotID, "deleted": true})
}
