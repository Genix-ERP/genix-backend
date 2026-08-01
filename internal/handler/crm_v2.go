package handler

// CRM v2 (see docs/crm-audit.md): single-entity pipeline where the lead IS
// the deal. This file holds everything added by the rebuild:
//   - pipelines CRUD (several funnels per org) + server-side default seeding
//   - batched stage reorder (replaces the N-PUT frontend loop)
//   - lost_reasons CRUD (+ lazy default seed)
//   - lead lifecycle: move / won / lost / reopen with stage history, audit
//     rows and workflow events; the win flow creates-or-links the unified
//     partner (contacts) with normalized-phone dedupe
//   - lead timeline (stage history ∪ audit ∪ activities ∪ calls) and linked
//     Vazifalar tasks
//   - the four Hisobotlar endpoints (funnel / sources / managers / loss reasons)
//   - the lead.stale scheduler scan

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// normalizePhoneDigits strips everything but digits and keeps the last 9 —
// Uzbek numbers without the country code — so `+998 90 123 45 67`,
// `901234567` and `90-123-45-67` all dedupe to the same key. Matches the
// idx_leads_phone_digits / idx_contacts_phone_digits expression indexes (446).
func normalizePhoneDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if len(d) > 9 {
		d = d[len(d)-9:]
	}
	return d
}

// crmResolveOrg returns the request org, falling back to the caller's primary
// org (same rationale as CreateLead: never leave records org-less).
func (h *Handler) crmResolveOrg(c *gin.Context, tenantID uuid.UUID) *uuid.UUID {
	if orgID, ok := middleware.GetOrganizationID(c); ok && orgID != uuid.Nil {
		o := orgID
		return &o
	}
	userID, _ := middleware.GetUserID(c)
	if userID == uuid.Nil {
		return nil
	}
	var fallbackOrg uuid.UUID
	if err := h.db.QueryRow(`
		SELECT eo.organization_id
		FROM employee_organizations eo
		JOIN employees e ON e.id = eo.employee_id
		WHERE e.user_id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL
		ORDER BY eo.is_primary DESC, eo.created_at ASC
		LIMIT 1
	`, userID, tenantID).Scan(&fallbackOrg); err == nil && fallbackOrg != uuid.Nil {
		return &fallbackOrg
	}
	return nil
}

type dbExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// recordLeadStageChange appends a lead_stage_history row (the funnel/cycle
// source of truth).
func (h *Handler) recordLeadStageChange(db dbExecer, tenantID, leadID uuid.UUID, fromStage, toStage *uuid.UUID, userID uuid.UUID) {
	var changedBy interface{}
	if userID != uuid.Nil {
		changedBy = userID
	}
	if _, err := db.Exec(`
		INSERT INTO lead_stage_history (id, tenant_id, lead_id, from_stage_id, to_stage_id, changed_by, changed_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`, uuid.New(), tenantID, leadID, fromStage, toStage, changedBy); err != nil {
		h.log.Error("Failed to record lead stage change", "error", err, "lead_id", leadID)
	}
}

// writeLeadAudit writes a lead audit_logs row from old/new value maps.
func (h *Handler) writeLeadAudit(tenantID, userID, leadID uuid.UUID, oldValues, newValues map[string]interface{}) {
	oldJSON, _ := json.Marshal(oldValues)
	newJSON, _ := json.Marshal(newValues)
	if _, err := h.db.Exec(`
		INSERT INTO audit_logs (id, tenant_id, user_id, action, entity_type, entity_id, old_values, new_values, created_at)
		VALUES ($1, $2, $3, 'update', 'lead', $4, $5, $6, NOW())
	`, uuid.New(), tenantID, userID, leadID, oldJSON, newJSON); err != nil {
		h.log.Error("Failed to write lead audit log", "error", err, "lead_id", leadID)
	}
}

// crmStaleDays reads tenant_settings.settings->'crm'->>'stale_days' (default 7).
func (h *Handler) crmStaleDays(tenantID uuid.UUID) int {
	var days sql.NullInt64
	err := h.db.QueryRow(`
		SELECT NULLIF(settings->'crm'->>'stale_days', '')::int
		FROM tenant_settings WHERE tenant_id = $1
	`, tenantID).Scan(&days)
	if err != nil || !days.Valid || days.Int64 < 1 {
		return 7
	}
	return int(days.Int64)
}

// defaultLeadStageSeed is the server-side stage set for a fresh pipeline.
// Display names come from i18n by code; `name` is the fallback.
var defaultLeadStageSeed = []struct {
	Name  string
	Code  string
	Prob  float64
	Won   bool
	Lost  bool
	Color string
}{
	{"New", "new", 10, false, false, "blue"},
	{"Contacted", "contacted", 30, false, false, "amber"},
	{"In Progress", "in_progress", 50, false, false, "purple"},
	{"Negotiation", "qualified", 60, false, false, "green"},
	{"Won", "won", 100, true, false, "emerald"},
	{"Lost", "lost", 0, false, true, "red"},
}

// seedDefaultPipeline creates the default pipeline + stages for an org that
// has none (new orgs created after migration 446 — replaces the old
// frontend-driven seeding in LeadsKanban).
func (h *Handler) seedDefaultPipeline(tenantID uuid.UUID, orgID *uuid.UUID) (uuid.UUID, error) {
	tx, err := h.db.Begin()
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback()

	pipelineID := uuid.New()
	if err := tx.QueryRow(`
		INSERT INTO pipelines (id, tenant_id, organization_id, name, is_default)
		VALUES ($1, $2, $3, 'Savdo voronkasi', true)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, pipelineID, tenantID, orgID).Scan(&pipelineID); err != nil {
		if err == sql.ErrNoRows { // concurrent seed won the race
			return uuid.Nil, err
		}
		return uuid.Nil, err
	}
	for i, s := range defaultLeadStageSeed {
		if _, err := tx.Exec(`
			INSERT INTO pipeline_stages (id, tenant_id, name, code, sequence, probability, is_won, is_lost, color, is_active, pipeline_type, organization_id, pipeline_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, 'lead', $10, $11, NOW(), NOW())
			ON CONFLICT DO NOTHING
		`, uuid.New(), tenantID, s.Name, s.Code, i, s.Prob, s.Won, s.Lost, s.Color, orgID, pipelineID); err != nil {
			return uuid.Nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return uuid.Nil, err
	}
	return pipelineID, nil
}

// ─── pipelines ──────────────────────────────────────────────────────────────

// ListPipelines returns the org's pipelines with nested stages, seeding the
// default pipeline on first use.
func (h *Handler) ListPipelines(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID := h.crmResolveOrg(c, tenantID)

	load := func() ([]map[string]interface{}, error) {
		rows, err := h.db.Query(`
			SELECT p.id, p.name, p.is_default, p.is_active, p.organization_id, p.created_at
			FROM pipelines p
			WHERE p.tenant_id = $1 AND p.is_active = true
			  AND ($2::uuid IS NULL OR p.organization_id = $2 OR p.organization_id IS NULL)
			ORDER BY p.is_default DESC, p.created_at ASC
		`, tenantID, orgID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		list := []map[string]interface{}{}
		for rows.Next() {
			var id uuid.UUID
			var name string
			var isDefault, isActive bool
			var pOrg *uuid.UUID
			var createdAt time.Time
			if err := rows.Scan(&id, &name, &isDefault, &isActive, &pOrg, &createdAt); err != nil {
				continue
			}
			list = append(list, map[string]interface{}{
				"id": id, "name": name, "is_default": isDefault, "is_active": isActive,
				"organization_id": pOrg, "created_at": createdAt, "stages": []interface{}{},
			})
		}
		return list, rows.Err()
	}

	pipelines, err := load()
	if err != nil {
		h.log.Error("Failed to list pipelines", "error", err)
		response.InternalError(c, "Failed to list pipelines")
		return
	}
	if len(pipelines) == 0 {
		if _, err := h.seedDefaultPipeline(tenantID, orgID); err != nil {
			h.log.Error("Failed to seed default pipeline", "error", err)
		}
		if pipelines, err = load(); err != nil {
			response.InternalError(c, "Failed to list pipelines")
			return
		}
	}

	// attach stages
	ids := make([]interface{}, 0, len(pipelines))
	idx := map[uuid.UUID]int{}
	ph := make([]string, 0, len(pipelines))
	for i, p := range pipelines {
		id := p["id"].(uuid.UUID)
		ids = append(ids, id)
		idx[id] = i
		ph = append(ph, fmt.Sprintf("$%d", i+1))
	}
	rows, err := h.db.Query(`
		SELECT id, pipeline_id, name, custom_name, code, sequence, probability, is_won, is_lost, color, is_active
		FROM pipeline_stages
		WHERE pipeline_id IN (`+strings.Join(ph, ",")+`) AND is_active = true
		ORDER BY sequence ASC
	`, ids...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, pid uuid.UUID
			var name, code, color string
			var customName sql.NullString
			var sequence int
			var probability float64
			var isWon, isLost, isActive bool
			if err := rows.Scan(&id, &pid, &name, &customName, &code, &sequence, &probability, &isWon, &isLost, &color, &isActive); err != nil {
				continue
			}
			stage := map[string]interface{}{
				"id": id, "pipeline_id": pid, "name": name, "code": code,
				"sequence": sequence, "probability": probability,
				"is_won": isWon, "is_lost": isLost, "color": color, "is_active": isActive,
			}
			if customName.Valid {
				stage["custom_name"] = customName.String
			}
			if i, ok := idx[pid]; ok {
				pipelines[i]["stages"] = append(pipelines[i]["stages"].([]interface{}), stage)
			}
		}
	}

	response.Success(c, pipelines)
}

// CreatePipeline creates an additional funnel for the org.
func (h *Handler) CreatePipeline(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var input struct {
		Name       string `json:"name" binding:"required,min=1,max=150"`
		SeedStages bool   `json:"seed_stages"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	orgID := h.crmResolveOrg(c, tenantID)
	userID, _ := middleware.GetUserID(c)

	id := uuid.New()
	if err := h.db.QueryRow(`
		INSERT INTO pipelines (id, tenant_id, organization_id, name, is_default, created_by)
		VALUES ($1, $2, $3, $4, false, $5)
		RETURNING id
	`, id, tenantID, orgID, input.Name, userID).Scan(&id); err != nil {
		h.log.Error("Failed to create pipeline", "error", err)
		response.InternalError(c, "Failed to create pipeline")
		return
	}
	if input.SeedStages {
		for i, s := range defaultLeadStageSeed {
			// per-pipeline stage codes must stay unique per (tenant, type, org);
			// suffix additional pipelines' codes with a short pipeline marker
			code := s.Code + "_" + id.String()[:4]
			h.db.Exec(`
				INSERT INTO pipeline_stages (id, tenant_id, name, code, sequence, probability, is_won, is_lost, color, is_active, pipeline_type, organization_id, pipeline_id, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, 'lead', $10, $11, NOW(), NOW())
				ON CONFLICT DO NOTHING
			`, uuid.New(), tenantID, s.Name, code, i, s.Prob, s.Won, s.Lost, s.Color, orgID, id)
		}
	}
	response.Created(c, gin.H{"id": id, "name": input.Name})
}

// UpdatePipeline renames / re-defaults a pipeline.
func (h *Handler) UpdatePipeline(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid pipeline ID")
		return
	}
	var input struct {
		Name      *string `json:"name,omitempty"`
		IsDefault *bool   `json:"is_default,omitempty"`
		IsActive  *bool   `json:"is_active,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if input.IsDefault != nil && *input.IsDefault {
		// single default per (tenant, org) — clear siblings first
		h.db.Exec(`
			UPDATE pipelines SET is_default = false
			WHERE tenant_id = $1 AND is_default = true
			  AND organization_id IS NOT DISTINCT FROM (SELECT organization_id FROM pipelines WHERE id = $2)
		`, tenantID, id)
	}
	updates := []string{"updated_at = NOW()"}
	args := []interface{}{}
	n := 0
	add := func(expr string, v interface{}) {
		n++
		updates = append(updates, fmt.Sprintf(expr, n))
		args = append(args, v)
	}
	if input.Name != nil {
		add("name = $%d", *input.Name)
	}
	if input.IsDefault != nil {
		add("is_default = $%d", *input.IsDefault)
	}
	if input.IsActive != nil {
		add("is_active = $%d", *input.IsActive)
	}
	args = append(args, id, tenantID)
	res, err := h.db.Exec(fmt.Sprintf(`UPDATE pipelines SET %s WHERE id = $%d AND tenant_id = $%d`,
		strings.Join(updates, ", "), n+1, n+2), args...)
	if err != nil {
		h.log.Error("Failed to update pipeline", "error", err)
		response.InternalError(c, "Failed to update pipeline")
		return
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		response.NotFound(c, "Pipeline")
		return
	}
	response.Success(c, gin.H{"message": "Pipeline updated"})
}

// DeletePipeline removes an empty non-default pipeline (its stages cascade).
func (h *Handler) DeletePipeline(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid pipeline ID")
		return
	}
	var leadCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM leads WHERE pipeline_id = $1 AND deleted_at IS NULL`, id).Scan(&leadCount)
	if leadCount > 0 {
		response.ConflictWithData(c, "PIPELINE_NOT_EMPTY", "Pipeline has leads", gin.H{"lead_count": leadCount})
		return
	}
	var isDefault bool
	if err := h.db.QueryRow(`SELECT is_default FROM pipelines WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&isDefault); err != nil {
		response.NotFound(c, "Pipeline")
		return
	}
	if isDefault {
		response.BadRequest(c, "Cannot delete the default pipeline")
		return
	}
	h.db.Exec(`DELETE FROM pipeline_stages WHERE pipeline_id = $1 AND tenant_id = $2`, id, tenantID)
	res, err := h.db.Exec(`DELETE FROM pipelines WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete pipeline", "error", err)
		response.InternalError(c, "Failed to delete pipeline")
		return
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		response.NotFound(c, "Pipeline")
		return
	}
	response.Success(c, gin.H{"message": "Pipeline deleted"})
}

// ReorderPipelineStages persists a full column order in one call (the old UI
// fired one PUT per stage).
func (h *Handler) ReorderPipelineStages(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var input struct {
		StageIDs []string `json:"stage_ids" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to reorder stages")
		return
	}
	defer tx.Rollback()
	for i, s := range input.StageIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			response.BadRequest(c, "Invalid stage ID")
			return
		}
		if _, err := tx.Exec(`UPDATE pipeline_stages SET sequence = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3`, i, id, tenantID); err != nil {
			h.log.Error("Failed to reorder stage", "error", err)
			response.InternalError(c, "Failed to reorder stages")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to reorder stages")
		return
	}
	response.Success(c, gin.H{"message": "Stages reordered"})
}

// ─── lost reasons ───────────────────────────────────────────────────────────

var defaultLostReasons = []string{"Narx qimmat", "Raqobatchini tanladi", "Javob bermadi", "Ehtiyoj yo'q", "Boshqa"}

// ListLostReasons returns the tenant's loss-reason catalog, seeding defaults
// on first use (new tenants created after migration 446).
func (h *Handler) ListLostReasons(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	load := func() ([]map[string]interface{}, error) {
		rows, err := h.db.Query(`
			SELECT id, name, position, is_active FROM lost_reasons
			WHERE tenant_id = $1 AND is_active = true ORDER BY position ASC, name ASC
		`, tenantID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		list := []map[string]interface{}{}
		for rows.Next() {
			var id uuid.UUID
			var name string
			var position int
			var isActive bool
			if err := rows.Scan(&id, &name, &position, &isActive); err != nil {
				continue
			}
			list = append(list, map[string]interface{}{"id": id, "name": name, "position": position, "is_active": isActive})
		}
		return list, rows.Err()
	}
	reasons, err := load()
	if err != nil {
		h.log.Error("Failed to list lost reasons", "error", err)
		response.InternalError(c, "Failed to list lost reasons")
		return
	}
	if len(reasons) == 0 {
		for i, name := range defaultLostReasons {
			h.db.Exec(`INSERT INTO lost_reasons (id, tenant_id, name, position) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
				uuid.New(), tenantID, name, i)
		}
		if reasons, err = load(); err != nil {
			response.InternalError(c, "Failed to list lost reasons")
			return
		}
	}
	response.Success(c, reasons)
}

// CreateLostReason adds a reason to the tenant catalog.
func (h *Handler) CreateLostReason(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var input struct {
		Name     string `json:"name" binding:"required,min=1,max=150"`
		Position *int   `json:"position,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	pos := 0
	if input.Position != nil {
		pos = *input.Position
	} else {
		h.db.QueryRow(`SELECT COALESCE(MAX(position)+1, 0) FROM lost_reasons WHERE tenant_id = $1`, tenantID).Scan(&pos)
	}
	id := uuid.New()
	if err := h.db.QueryRow(`
		INSERT INTO lost_reasons (id, tenant_id, name, position)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, name) DO UPDATE SET is_active = true, updated_at = NOW()
		RETURNING id
	`, id, tenantID, input.Name, pos).Scan(&id); err != nil {
		h.log.Error("Failed to create lost reason", "error", err)
		response.InternalError(c, "Failed to create lost reason")
		return
	}
	response.Created(c, gin.H{"id": id, "name": input.Name, "position": pos})
}

// UpdateLostReason renames/repositions a reason.
func (h *Handler) UpdateLostReason(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}
	var input struct {
		Name     *string `json:"name,omitempty"`
		Position *int    `json:"position,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	updates := []string{"updated_at = NOW()"}
	args := []interface{}{}
	n := 0
	if input.Name != nil {
		n++
		updates = append(updates, fmt.Sprintf("name = $%d", n))
		args = append(args, *input.Name)
	}
	if input.Position != nil {
		n++
		updates = append(updates, fmt.Sprintf("position = $%d", n))
		args = append(args, *input.Position)
	}
	args = append(args, id, tenantID)
	res, err := h.db.Exec(fmt.Sprintf(`UPDATE lost_reasons SET %s WHERE id = $%d AND tenant_id = $%d`,
		strings.Join(updates, ", "), n+1, n+2), args...)
	if err != nil {
		h.log.Error("Failed to update lost reason", "error", err)
		response.InternalError(c, "Failed to update lost reason")
		return
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		response.NotFound(c, "Lost reason")
		return
	}
	response.Success(c, gin.H{"message": "Lost reason updated"})
}

// DeleteLostReason soft-disables a reason (historical leads keep the FK).
func (h *Handler) DeleteLostReason(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}
	res, err := h.db.Exec(`UPDATE lost_reasons SET is_active = false, updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete lost reason")
		return
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		response.NotFound(c, "Lost reason")
		return
	}
	response.Success(c, gin.H{"message": "Lost reason deleted"})
}

// ─── lead lifecycle ─────────────────────────────────────────────────────────

type crmLeadRow struct {
	ID          uuid.UUID
	ContactName string
	CompanyName sql.NullString
	Email       sql.NullString
	Phone       sql.NullString
	Amount      sql.NullFloat64
	Currency    string
	PipelineID  *uuid.UUID
	StageID     *uuid.UUID
	StageCode   sql.NullString
	PartnerID   *uuid.UUID
	OrgID       *uuid.UUID
	WonAt       sql.NullTime
	LostAt      sql.NullTime
}

func (h *Handler) loadLeadForTransition(tenantID, id uuid.UUID) (*crmLeadRow, error) {
	var l crmLeadRow
	err := h.db.QueryRow(`
		SELECT l.id, l.contact_name, l.company_name, l.email, l.phone,
		       l.expected_value, COALESCE(l.currency, 'UZS'), l.pipeline_id, l.stage_id, ps.code,
		       l.partner_id, l.organization_id, l.won_at, l.lost_at
		FROM leads l
		LEFT JOIN pipeline_stages ps ON ps.id = l.stage_id
		WHERE l.id = $1 AND l.tenant_id = $2 AND l.deleted_at IS NULL
	`, id, tenantID).Scan(&l.ID, &l.ContactName, &l.CompanyName, &l.Email, &l.Phone,
		&l.Amount, &l.Currency, &l.PipelineID, &l.StageID, &l.StageCode,
		&l.PartnerID, &l.OrgID, &l.WonAt, &l.LostAt)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// leadTerminalStage finds the won/lost stage of the lead's pipeline (falling
// back to the org default pipeline for legacy rows without pipeline_id).
func (h *Handler) leadTerminalStage(tenantID uuid.UUID, l *crmLeadRow, won bool) (stageID uuid.UUID, code string, err error) {
	col := "is_won"
	if !won {
		col = "is_lost"
	}
	if l.PipelineID != nil {
		err = h.db.QueryRow(`
			SELECT id, code FROM pipeline_stages
			WHERE pipeline_id = $1 AND `+col+` = true AND is_active = true
			ORDER BY sequence LIMIT 1
		`, *l.PipelineID).Scan(&stageID, &code)
		if err == nil {
			return stageID, code, nil
		}
	}
	err = h.db.QueryRow(`
		SELECT ps.id, ps.code FROM pipeline_stages ps
		WHERE ps.tenant_id = $1 AND ps.pipeline_type = 'lead' AND ps.`+col+` = true AND ps.is_active = true
		  AND ps.organization_id IS NOT DISTINCT FROM $2
		ORDER BY ps.sequence LIMIT 1
	`, tenantID, l.OrgID).Scan(&stageID, &code)
	return stageID, code, err
}

// MoveLead moves a lead to an open stage. Terminal stages are rejected with a
// typed 409 so the UI opens the win/loss flow instead — the server never lets
// a drag silently create an unreasoned loss or a partner-less win.
func (h *Handler) MoveLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}
	var input struct {
		StageID string `json:"stage_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "stage_id is required")
		return
	}
	stageID, err := uuid.Parse(input.StageID)
	if err != nil {
		response.BadRequest(c, "Invalid stage ID")
		return
	}

	lead, err := h.loadLeadForTransition(tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Lead")
		return
	}
	if err != nil {
		h.log.Error("Failed to load lead", "error", err)
		response.InternalError(c, "Failed to move lead")
		return
	}

	var stage struct {
		Code       string
		Name       string
		IsWon      bool
		IsLost     bool
		PipelineID *uuid.UUID
	}
	err = h.db.QueryRow(`
		SELECT code, COALESCE(custom_name, name), is_won, is_lost, pipeline_id
		FROM pipeline_stages WHERE id = $1 AND tenant_id = $2 AND pipeline_type = 'lead'
	`, stageID, tenantID).Scan(&stage.Code, &stage.Name, &stage.IsWon, &stage.IsLost, &stage.PipelineID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stage")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to move lead")
		return
	}
	if stage.IsWon {
		response.ConflictWithData(c, "WON_FLOW_REQUIRED", "Use the win flow for the won stage", gin.H{"stage_id": stageID})
		return
	}
	if stage.IsLost {
		response.ConflictWithData(c, "LOST_REASON_REQUIRED", "A loss reason is required for the lost stage", gin.H{"stage_id": stageID})
		return
	}
	if lead.StageID != nil && *lead.StageID == stageID {
		response.Success(c, gin.H{"message": "Lead already in stage"})
		return
	}

	oldCode := lead.StageCode.String
	reopened := lead.WonAt.Valid || lead.LostAt.Valid
	pipelineID := stage.PipelineID
	if pipelineID == nil {
		pipelineID = lead.PipelineID
	}
	_, err = h.db.Exec(`
		UPDATE leads SET stage_id = $1, pipeline_id = COALESCE($2, pipeline_id), status = $3,
		       won_at = NULL, lost_at = NULL, lost_reason_id = NULL, lost_note = NULL,
		       last_activity_at = NOW(), updated_at = NOW()
		WHERE id = $4 AND tenant_id = $5 AND deleted_at IS NULL
	`, stageID, pipelineID, stage.Code, id, tenantID)
	if err != nil {
		h.log.Error("Failed to move lead", "error", err)
		response.InternalError(c, "Failed to move lead")
		return
	}

	h.recordLeadStageChange(h.db, tenantID, id, lead.StageID, &stageID, userID)
	h.writeLeadAudit(tenantID, userID, id,
		map[string]interface{}{"status": oldCode},
		map[string]interface{}{"status": stage.Code})
	h.EmitWorkflowEvent(tenantID, "lead.status_changed", map[string]interface{}{
		"record_id":    id.String(),
		"contact_name": lead.ContactName,
		"old_status":   oldCode,
		"new_status":   stage.Code,
		"stage_name":   stage.Name,
	})

	response.Success(c, gin.H{"message": "Lead moved", "stage_id": stageID, "status": stage.Code, "reopened": reopened})
}

// WinLeadInput controls the win flow's partner handoff.
type WinLeadInput struct {
	PartnerID   string `json:"partner_id,omitempty"`   // link an existing contact
	PartnerType string `json:"partner_type,omitempty"` // default customer
}

// WinLead moves a lead to the won stage and creates-or-links the unified
// partner (contacts) — dedupe by normalized phone, then email. This replaces
// the buggy half of ConvertLead that wrote an opportunity UUID into the
// contacts FK.
func (h *Handler) WinLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}
	var input WinLeadInput
	if err := c.ShouldBindJSON(&input); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid input")
		return
	}

	lead, err := h.loadLeadForTransition(tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Lead")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to win lead")
		return
	}
	if lead.WonAt.Valid {
		response.Conflict(c, "Lead is already won")
		return
	}

	wonStageID, wonCode, err := h.leadTerminalStage(tenantID, lead, true)
	if err != nil {
		h.log.Error("No won stage found for lead pipeline", "error", err, "lead_id", id)
		response.InternalError(c, "Pipeline has no won stage")
		return
	}

	now := time.Now()
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to win lead")
		return
	}
	defer tx.Rollback()

	// ── resolve the partner ──
	var partnerID uuid.UUID
	partnerCreated := false
	partnerName := ""
	switch {
	case input.PartnerID != "":
		pid, perr := uuid.Parse(input.PartnerID)
		if perr != nil {
			response.BadRequest(c, "Invalid partner ID")
			return
		}
		if err := tx.QueryRow(`SELECT id, name FROM contacts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, pid, tenantID).Scan(&partnerID, &partnerName); err != nil {
			response.NotFound(c, "Partner")
			return
		}
	case lead.PartnerID != nil:
		partnerID = *lead.PartnerID
		tx.QueryRow(`SELECT name FROM contacts WHERE id = $1`, partnerID).Scan(&partnerName)
	default:
		// dedupe: normalized phone first, then email
		digits := ""
		if lead.Phone.Valid {
			digits = normalizePhoneDigits(lead.Phone.String)
		}
		if len(digits) >= 7 {
			tx.QueryRow(`
				SELECT id, name FROM contacts
				WHERE tenant_id = $1 AND deleted_at IS NULL
				  AND RIGHT(REGEXP_REPLACE(COALESCE(phone, ''), '[^0-9]', '', 'g'), 9) = $2
				ORDER BY created_at LIMIT 1
			`, tenantID, digits).Scan(&partnerID, &partnerName)
		}
		if partnerID == uuid.Nil && lead.Email.Valid && lead.Email.String != "" {
			tx.QueryRow(`
				SELECT id, name FROM contacts
				WHERE tenant_id = $1 AND deleted_at IS NULL AND LOWER(email) = LOWER($2)
				ORDER BY created_at LIMIT 1
			`, tenantID, lead.Email.String).Scan(&partnerID, &partnerName)
		}
		if partnerID == uuid.Nil {
			partnerID = uuid.New()
			partnerCreated = true
			partnerType := input.PartnerType
			if partnerType == "" {
				partnerType = "customer"
			}
			partnerName = lead.ContactName
			if lead.CompanyName.Valid && lead.CompanyName.String != "" {
				partnerName = lead.CompanyName.String
			}
			var phone, email interface{}
			if lead.Phone.Valid {
				phone = lead.Phone.String
			}
			if lead.Email.Valid && lead.Email.String != "" {
				email = lead.Email.String
			}
			if err := tx.QueryRow(`
				INSERT INTO contacts (id, tenant_id, organization_id, type, code, name, email, phone, is_active, created_by, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, $9, $10, $10)
				RETURNING id
			`, partnerID, tenantID, lead.OrgID, partnerType, fmt.Sprintf("C-%s", partnerID.String()[:8]),
				partnerName, email, phone, userID, now).Scan(&partnerID); err != nil {
				h.log.Error("Failed to create partner from lead", "error", err)
				response.InternalError(c, "Failed to create partner")
				return
			}
			if lead.CompanyName.Valid && lead.CompanyName.String != "" && lead.ContactName != "" {
				parts := strings.SplitN(lead.ContactName, " ", 2)
				lastName := ""
				if len(parts) > 1 {
					lastName = parts[1]
				}
				tx.Exec(`
					INSERT INTO contact_persons (id, contact_id, first_name, last_name, email, phone, is_primary, created_at, updated_at)
					VALUES ($1, $2, $3, $4, $5, $6, true, $7, $7)
				`, uuid.New(), partnerID, parts[0], lastName, lead.Email.String, phone, now)
			}
		}
	}

	if _, err := tx.Exec(`
		UPDATE leads SET stage_id = $1, status = $2, won_at = $3,
		       partner_id = $4, converted_to = $4, converted_at = COALESCE(converted_at, $3),
		       lost_at = NULL, lost_reason_id = NULL, lost_note = NULL,
		       last_activity_at = $3, updated_at = $3
		WHERE id = $5 AND tenant_id = $6 AND deleted_at IS NULL
	`, wonStageID, wonCode, now, partnerID, id, tenantID); err != nil {
		h.log.Error("Failed to mark lead won", "error", err)
		response.InternalError(c, "Failed to win lead")
		return
	}
	h.recordLeadStageChange(tx, tenantID, id, lead.StageID, &wonStageID, userID)

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to win lead")
		return
	}

	h.writeLeadAudit(tenantID, userID, id,
		map[string]interface{}{"status": lead.StageCode.String},
		map[string]interface{}{"status": wonCode, "partner_id": partnerID.String()})

	amount := 0.0
	if lead.Amount.Valid {
		amount = lead.Amount.Float64
	}
	h.EmitWorkflowEvent(tenantID, "lead.won", map[string]interface{}{
		"record_id":    id.String(),
		"contact_name": lead.ContactName,
		"company_name": lead.CompanyName.String,
		"amount":       amount,
		"currency":     lead.Currency,
		"partner_id":   partnerID.String(),
	})

	response.Success(c, gin.H{
		"message":         "Lead won",
		"lead_id":         id,
		"stage_id":        wonStageID,
		"partner_id":      partnerID,
		"partner_name":    partnerName,
		"partner_created": partnerCreated,
	})
}

// LoseLeadInput requires a reason — an unreasoned loss is not accepted.
type LoseLeadInput struct {
	LostReasonID string `json:"lost_reason_id" binding:"required"`
	Note         string `json:"note,omitempty"`
}

// LoseLead moves a lead to the lost stage with a mandatory reason.
func (h *Handler) LoseLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}
	var input LoseLeadInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "lost_reason_id is required")
		return
	}
	reasonID, err := uuid.Parse(input.LostReasonID)
	if err != nil {
		response.BadRequest(c, "Invalid lost reason ID")
		return
	}
	var reasonName string
	if err := h.db.QueryRow(`SELECT name FROM lost_reasons WHERE id = $1 AND tenant_id = $2`, reasonID, tenantID).Scan(&reasonName); err != nil {
		response.NotFound(c, "Lost reason")
		return
	}

	lead, err := h.loadLeadForTransition(tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Lead")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to update lead")
		return
	}
	if lead.LostAt.Valid {
		response.Conflict(c, "Lead is already lost")
		return
	}

	lostStageID, lostCode, err := h.leadTerminalStage(tenantID, lead, false)
	if err != nil {
		h.log.Error("No lost stage found for lead pipeline", "error", err, "lead_id", id)
		response.InternalError(c, "Pipeline has no lost stage")
		return
	}

	var note interface{}
	if input.Note != "" {
		note = input.Note
	}
	if _, err := h.db.Exec(`
		UPDATE leads SET stage_id = $1, status = $2, lost_at = NOW(), lost_reason_id = $3, lost_note = $4,
		       won_at = NULL, last_activity_at = NOW(), updated_at = NOW()
		WHERE id = $5 AND tenant_id = $6 AND deleted_at IS NULL
	`, lostStageID, lostCode, reasonID, note, id, tenantID); err != nil {
		h.log.Error("Failed to mark lead lost", "error", err)
		response.InternalError(c, "Failed to update lead")
		return
	}
	h.recordLeadStageChange(h.db, tenantID, id, lead.StageID, &lostStageID, userID)
	h.writeLeadAudit(tenantID, userID, id,
		map[string]interface{}{"status": lead.StageCode.String},
		map[string]interface{}{"status": lostCode, "lost_reason": reasonName})

	amount := 0.0
	if lead.Amount.Valid {
		amount = lead.Amount.Float64
	}
	h.EmitWorkflowEvent(tenantID, "lead.lost", map[string]interface{}{
		"record_id":    id.String(),
		"contact_name": lead.ContactName,
		"company_name": lead.CompanyName.String,
		"amount":       amount,
		"currency":     lead.Currency,
		"lost_reason":  reasonName,
	})

	response.Success(c, gin.H{"message": "Lead lost", "lead_id": id, "stage_id": lostStageID, "lost_reason": reasonName})
}

// ─── timeline & tasks ───────────────────────────────────────────────────────

// GetLeadTimeline merges stage history, field-change audit rows, activities
// and calls into one chronological feed.
func (h *Handler) GetLeadTimeline(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}
	// existence check (tenant-scoped)
	var exists bool
	if err := h.db.QueryRow(`SELECT true FROM leads WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&exists); err != nil {
		response.NotFound(c, "Lead")
		return
	}

	type item struct {
		Type string                 `json:"type"`
		At   time.Time              `json:"at"`
		By   string                 `json:"by,omitempty"`
		Data map[string]interface{} `json:"data"`
	}
	items := []item{}

	// stage changes
	rows, err := h.db.Query(`
		SELECT h.changed_at,
		       COALESCE(TRIM(u.first_name || ' ' || u.last_name), ''),
		       COALESCE(fs.custom_name, fs.name, ''), COALESCE(fs.code, ''),
		       COALESCE(ts.custom_name, ts.name, ''), COALESCE(ts.code, '')
		FROM lead_stage_history h
		LEFT JOIN users u ON u.id = h.changed_by
		LEFT JOIN pipeline_stages fs ON fs.id = h.from_stage_id
		LEFT JOIN pipeline_stages ts ON ts.id = h.to_stage_id
		WHERE h.lead_id = $1 AND h.tenant_id = $2
		ORDER BY h.changed_at DESC LIMIT 100
	`, id, tenantID)
	if err == nil {
		for rows.Next() {
			var at time.Time
			var by, fromName, fromCode, toName, toCode string
			if rows.Scan(&at, &by, &fromName, &fromCode, &toName, &toCode) == nil {
				items = append(items, item{Type: "stage_change", At: at, By: by, Data: map[string]interface{}{
					"from_stage": fromName, "from_code": fromCode, "to_stage": toName, "to_code": toCode,
				}})
			}
		}
		rows.Close()
	}

	// field changes (skip pure status changes — stage history covers them)
	rows, err = h.db.Query(`
		SELECT al.created_at, COALESCE(TRIM(u.first_name || ' ' || u.last_name), COALESCE(u.email, '')),
		       COALESCE(al.old_values, '{}'), COALESCE(al.new_values, '{}')
		FROM audit_logs al
		LEFT JOIN users u ON u.id = al.user_id
		WHERE al.tenant_id = $1 AND al.entity_type = 'lead' AND al.entity_id = $2
		ORDER BY al.created_at DESC LIMIT 100
	`, tenantID, id)
	if err == nil {
		for rows.Next() {
			var at time.Time
			var by, oldV, newV string
			if rows.Scan(&at, &by, &oldV, &newV) == nil {
				var oldM, newM map[string]interface{}
				json.Unmarshal([]byte(oldV), &oldM)
				json.Unmarshal([]byte(newV), &newM)
				delete(oldM, "status")
				delete(newM, "status")
				if len(newM) == 0 {
					continue
				}
				items = append(items, item{Type: "field_change", At: at, By: by, Data: map[string]interface{}{
					"old": oldM, "new": newM,
				}})
			}
		}
		rows.Close()
	}

	// activities (notes, follow-ups, meetings)
	rows, err = h.db.Query(`
		SELECT a.created_at, COALESCE(TRIM(u.first_name || ' ' || u.last_name), ''),
		       a.id, a.activity_type, COALESCE(a.subject, ''), COALESCE(a.description, ''),
		       a.status, a.start_datetime
		FROM activities a
		LEFT JOIN users u ON u.id = a.created_by
		WHERE a.tenant_id = $1 AND a.lead_id = $2 AND a.deleted_at IS NULL
		ORDER BY a.created_at DESC LIMIT 100
	`, tenantID, id)
	if err == nil {
		for rows.Next() {
			var at time.Time
			var by, actType, subject, description, status string
			var actID uuid.UUID
			var startAt sql.NullTime
			if rows.Scan(&at, &by, &actID, &actType, &subject, &description, &status, &startAt) == nil {
				data := map[string]interface{}{
					"id": actID, "activity_type": actType, "subject": subject,
					"description": description, "status": status,
				}
				if startAt.Valid {
					data["start_datetime"] = startAt.Time
				}
				items = append(items, item{Type: "activity", At: at, By: by, Data: data})
			}
		}
		rows.Close()
	}

	// calls
	rows, err = h.db.Query(`
		SELECT cl.created_at, COALESCE(TRIM(u.first_name || ' ' || u.last_name), ''),
		       cl.id, cl.call_type, COALESCE(cl.call_duration, 0), COALESCE(cl.call_outcome, ''),
		       COALESCE(cl.recording_url, ''), COALESCE(cl.notes, '')
		FROM call_logs cl
		LEFT JOIN users u ON u.id = cl.agent_id
		WHERE cl.tenant_id = $1 AND cl.lead_id = $2 AND cl.deleted_at IS NULL
		ORDER BY cl.created_at DESC LIMIT 100
	`, tenantID, id)
	if err == nil {
		for rows.Next() {
			var at time.Time
			var by, callType, outcome, recordingURL, notes string
			var callID uuid.UUID
			var duration int
			if rows.Scan(&at, &by, &callID, &callType, &duration, &outcome, &recordingURL, &notes) == nil {
				items = append(items, item{Type: "call", At: at, By: by, Data: map[string]interface{}{
					"id": callID, "call_type": callType, "duration": duration,
					"outcome": outcome, "recording_url": recordingURL, "notes": notes,
				}})
			}
		}
		rows.Close()
	}

	// newest first
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].At.After(items[j-1].At); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	if len(items) > 150 {
		items = items[:150]
	}
	response.Success(c, items)
}

// ListLeadTasks returns Vazifalar tasks linked to the lead via task_links.
func (h *Handler) ListLeadTasks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}
	rows, err := h.db.Query(`
		SELECT t.id, t.board_id, b.name, t.title, t.priority, t.due_date, t.completed_at,
		       COALESCE((SELECT STRING_AGG(TRIM(e.first_name || ' ' || e.last_name), ', ')
		                 FROM task_assignees ta JOIN employees e ON e.id = ta.employee_id
		                 WHERE ta.task_id = t.id), '')
		FROM task_links tl
		JOIN tasks t ON t.id = tl.task_id
		JOIN task_boards b ON b.id = t.board_id
		WHERE tl.tenant_id = $1 AND tl.linked_module = 'crm_lead' AND tl.linked_id = $2
		  AND t.archived_at IS NULL
		ORDER BY t.completed_at NULLS FIRST, t.due_date NULLS LAST, t.created_at DESC
		LIMIT 100
	`, tenantID, id.String())
	if err != nil {
		h.log.Error("Failed to list lead tasks", "error", err)
		response.InternalError(c, "Failed to list lead tasks")
		return
	}
	defer rows.Close()
	tasks := []map[string]interface{}{}
	for rows.Next() {
		var taskID, boardID uuid.UUID
		var boardName, title, priority, assignees string
		var dueDate, completedAt sql.NullTime
		if rows.Scan(&taskID, &boardID, &boardName, &title, &priority, &dueDate, &completedAt, &assignees) != nil {
			continue
		}
		t := map[string]interface{}{
			"id": taskID, "board_id": boardID, "board_name": boardName,
			"title": title, "priority": priority, "assignees": assignees,
			"completed": completedAt.Valid,
		}
		if dueDate.Valid {
			t["due_date"] = dueDate.Time
		}
		if completedAt.Valid {
			t["completed_at"] = completedAt.Time
		}
		tasks = append(tasks, t)
	}
	response.Success(c, tasks)
}

// ─── Hisobotlar (the four honest reports) ───────────────────────────────────

// crmReportFilters parses common report query params.
func crmReportFilters(c *gin.Context) (from, to time.Time) {
	from = time.Now().AddDate(0, -3, 0)
	to = time.Now()
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			from = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			to = t.AddDate(0, 0, 1) // inclusive end day
		}
	}
	return from, to
}

// GetCRMFunnelReport — Voronka: per open stage of a pipeline, how many leads
// created in the period reached it (stage history max-sequence semantics),
// plus current occupancy and won/lost totals.
func (h *Handler) GetCRMFunnelReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID := h.crmResolveOrg(c, tenantID)
	from, to := crmReportFilters(c)

	var pipelineID uuid.UUID
	if v := c.Query("pipeline_id"); v != "" {
		pid, err := uuid.Parse(v)
		if err != nil {
			response.BadRequest(c, "Invalid pipeline ID")
			return
		}
		pipelineID = pid
	} else {
		err := h.db.QueryRow(`
			SELECT id FROM pipelines WHERE tenant_id = $1 AND is_default AND is_active
			  AND organization_id IS NOT DISTINCT FROM $2 LIMIT 1
		`, tenantID, orgID).Scan(&pipelineID)
		if err != nil {
			response.Success(c, gin.H{"stages": []interface{}{}, "totals": gin.H{}})
			return
		}
	}

	rows, err := h.db.Query(`
		WITH cohort AS (
			SELECT l.id, l.expected_value, l.won_at, l.lost_at
			FROM leads l
			WHERE l.tenant_id = $1 AND l.deleted_at IS NULL AND l.pipeline_id = $2
			  AND l.created_at >= $3 AND l.created_at < $4
			  AND ($5::uuid IS NULL OR l.organization_id = $5)
		),
		reached AS (
			SELECT h.lead_id, MAX(ps.sequence) AS max_seq
			FROM lead_stage_history h
			JOIN pipeline_stages ps ON ps.id = h.to_stage_id AND NOT ps.is_lost
			JOIN cohort ch ON ch.id = h.lead_id
			GROUP BY h.lead_id
		)
		SELECT s.id, COALESCE(s.custom_name, s.name), s.code, s.sequence, s.color, s.is_won,
		       (SELECT COUNT(*) FROM reached r WHERE r.max_seq >= s.sequence),
		       (SELECT COUNT(*) FROM leads l2 WHERE l2.stage_id = s.id AND l2.deleted_at IS NULL),
		       COALESCE((SELECT SUM(l2.expected_value) FROM leads l2 WHERE l2.stage_id = s.id AND l2.deleted_at IS NULL), 0)
		FROM pipeline_stages s
		WHERE s.pipeline_id = $2 AND s.is_active AND NOT s.is_lost
		ORDER BY s.sequence
	`, tenantID, pipelineID, from, to, orgID)
	if err != nil {
		h.log.Error("Failed funnel report", "error", err)
		response.InternalError(c, "Failed to build funnel report")
		return
	}
	defer rows.Close()

	stages := []map[string]interface{}{}
	for rows.Next() {
		var sid uuid.UUID
		var name, code, color string
		var sequence, reachedCount, currentCount int
		var isWon bool
		var currentValue float64
		if rows.Scan(&sid, &name, &code, &sequence, &color, &isWon, &reachedCount, &currentCount, &currentValue) != nil {
			continue
		}
		stages = append(stages, map[string]interface{}{
			"stage_id": sid, "name": name, "code": code, "sequence": sequence, "color": color,
			"is_won": isWon, "reached": reachedCount, "current": currentCount, "current_value": currentValue,
		})
	}

	var created, won, lost int
	var wonValue, lostValue float64
	h.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE won_at IS NOT NULL),
		       COUNT(*) FILTER (WHERE lost_at IS NOT NULL),
		       COALESCE(SUM(expected_value) FILTER (WHERE won_at IS NOT NULL), 0),
		       COALESCE(SUM(expected_value) FILTER (WHERE lost_at IS NOT NULL), 0)
		FROM leads
		WHERE tenant_id = $1 AND deleted_at IS NULL AND pipeline_id = $2
		  AND created_at >= $3 AND created_at < $4
		  AND ($5::uuid IS NULL OR organization_id = $5)
	`, tenantID, pipelineID, from, to, orgID).Scan(&created, &won, &lost, &wonValue, &lostValue)

	response.Success(c, gin.H{
		"pipeline_id": pipelineID,
		"stages":      stages,
		"totals": gin.H{
			"created": created, "won": won, "lost": lost,
			"won_value": wonValue, "lost_value": lostValue,
		},
	})
}

// GetCRMSourcesReport — Manbalar: leads & win rate by source.
func (h *Handler) GetCRMSourcesReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID := h.crmResolveOrg(c, tenantID)
	from, to := crmReportFilters(c)

	rows, err := h.db.Query(`
		SELECT COALESCE(NULLIF(source, ''), 'other'),
		       COUNT(*),
		       COUNT(*) FILTER (WHERE won_at IS NOT NULL),
		       COUNT(*) FILTER (WHERE lost_at IS NOT NULL),
		       COALESCE(SUM(expected_value) FILTER (WHERE won_at IS NOT NULL), 0),
		       COALESCE(SUM(expected_value) FILTER (WHERE won_at IS NULL AND lost_at IS NULL), 0)
		FROM leads
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND created_at >= $2 AND created_at < $3
		  AND ($4::uuid IS NULL OR organization_id = $4)
		GROUP BY 1 ORDER BY 2 DESC
	`, tenantID, from, to, orgID)
	if err != nil {
		h.log.Error("Failed sources report", "error", err)
		response.InternalError(c, "Failed to build sources report")
		return
	}
	defer rows.Close()

	list := []map[string]interface{}{}
	for rows.Next() {
		var source string
		var total, won, lost int
		var wonValue, openValue float64
		if rows.Scan(&source, &total, &won, &lost, &wonValue, &openValue) != nil {
			continue
		}
		winRate := 0.0
		if won+lost > 0 {
			winRate = float64(won) / float64(won+lost) * 100
		}
		list = append(list, map[string]interface{}{
			"source": source, "total": total, "won": won, "lost": lost,
			"won_value": wonValue, "open_value": openValue, "win_rate": winRate,
		})
	}
	response.Success(c, list)
}

// GetCRMManagersReport — Menejerlar: per responsible employee.
func (h *Handler) GetCRMManagersReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID := h.crmResolveOrg(c, tenantID)
	from, to := crmReportFilters(c)

	rows, err := h.db.Query(`
		SELECT COALESCE(l.responsible_employee_id::text, ''),
		       COALESCE(TRIM(e.first_name || ' ' || e.last_name), ''),
		       COUNT(*),
		       COUNT(*) FILTER (WHERE l.won_at IS NOT NULL),
		       COUNT(*) FILTER (WHERE l.lost_at IS NOT NULL),
		       COUNT(*) FILTER (WHERE l.won_at IS NULL AND l.lost_at IS NULL),
		       COALESCE(SUM(l.expected_value) FILTER (WHERE l.won_at IS NOT NULL), 0),
		       COALESCE(SUM(l.expected_value) FILTER (WHERE l.won_at IS NULL AND l.lost_at IS NULL), 0),
		       COALESCE(AVG(EXTRACT(EPOCH FROM (l.won_at - l.created_at)) / 86400) FILTER (WHERE l.won_at IS NOT NULL), 0)
		FROM leads l
		LEFT JOIN employees e ON e.id = l.responsible_employee_id
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL
		  AND l.created_at >= $2 AND l.created_at < $3
		  AND ($4::uuid IS NULL OR l.organization_id = $4)
		GROUP BY 1, 2 ORDER BY 7 DESC, 3 DESC
	`, tenantID, from, to, orgID)
	if err != nil {
		h.log.Error("Failed managers report", "error", err)
		response.InternalError(c, "Failed to build managers report")
		return
	}
	defer rows.Close()

	list := []map[string]interface{}{}
	for rows.Next() {
		var employeeID, name string
		var total, won, lost, open int
		var wonValue, openValue, avgCycleDays float64
		if rows.Scan(&employeeID, &name, &total, &won, &lost, &open, &wonValue, &openValue, &avgCycleDays) != nil {
			continue
		}
		winRate := 0.0
		if won+lost > 0 {
			winRate = float64(won) / float64(won+lost) * 100
		}
		list = append(list, map[string]interface{}{
			"employee_id": employeeID, "name": name,
			"total": total, "won": won, "lost": lost, "open": open,
			"won_value": wonValue, "open_value": openValue,
			"win_rate": winRate, "avg_cycle_days": avgCycleDays,
		})
	}
	response.Success(c, list)
}

// GetCRMLossReasonsReport — Yo'qotish sabablari (period on lost_at).
func (h *Handler) GetCRMLossReasonsReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID := h.crmResolveOrg(c, tenantID)
	from, to := crmReportFilters(c)

	rows, err := h.db.Query(`
		SELECT COALESCE(r.name, ''),
		       COUNT(*),
		       COALESCE(SUM(l.expected_value), 0)
		FROM leads l
		LEFT JOIN lost_reasons r ON r.id = l.lost_reason_id
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL AND l.lost_at IS NOT NULL
		  AND l.lost_at >= $2 AND l.lost_at < $3
		  AND ($4::uuid IS NULL OR l.organization_id = $4)
		GROUP BY 1 ORDER BY 2 DESC
	`, tenantID, from, to, orgID)
	if err != nil {
		h.log.Error("Failed loss reasons report", "error", err)
		response.InternalError(c, "Failed to build loss reasons report")
		return
	}
	defer rows.Close()

	list := []map[string]interface{}{}
	total := 0
	for rows.Next() {
		var name string
		var count int
		var value float64
		if rows.Scan(&name, &count, &value) != nil {
			continue
		}
		total += count
		list = append(list, map[string]interface{}{"reason": name, "count": count, "value": value})
	}
	for _, r := range list {
		if total > 0 {
			r["share"] = float64(r["count"].(int)) / float64(total) * 100
		} else {
			r["share"] = 0.0
		}
	}
	response.Success(c, gin.H{"reasons": list, "total_lost": total})
}

// ─── scheduler: lead.stale ──────────────────────────────────────────────────

// checkStaleLeads emits lead.stale for open leads whose last_activity_at is
// older than the tenant's stale threshold. The marker key includes the
// last-activity date, so a lead that gets touched and goes quiet again
// re-fires for the new stale window.
func (h *Handler) checkStaleLeads(tenantID uuid.UUID) {
	days := h.crmStaleDays(tenantID)
	rows, err := h.db.Query(`
		SELECT l.id, l.contact_name, COALESCE(l.company_name, ''),
		       COALESCE(l.expected_value, 0), COALESCE(l.currency, 'UZS'),
		       COALESCE(ps.code, l.status::text), l.last_activity_at
		FROM leads l
		LEFT JOIN pipeline_stages ps ON ps.id = l.stage_id
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL
		  AND l.won_at IS NULL AND l.lost_at IS NULL
		  AND l.last_activity_at < NOW() - make_interval(days => $2)
		LIMIT 500
	`, tenantID, days)
	if err != nil {
		h.log.Error("Failed to check stale leads", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var leadID uuid.UUID
		var contactName, companyName, stageCode string
		var amount float64
		var currency string
		var lastActivity sql.NullTime
		if rows.Scan(&leadID, &contactName, &companyName, &amount, &currency, &stageCode, &lastActivity) != nil {
			continue
		}
		lastStr := ""
		staleDays := days
		if lastActivity.Valid {
			lastStr = lastActivity.Time.Format("2006-01-02")
			staleDays = int(time.Since(lastActivity.Time).Hours() / 24)
		}
		h.runWorkflowEvent(workflowEventCtx{
			TenantID: tenantID,
			Event:    "lead.stale",
			Data: map[string]interface{}{
				"record_id":    leadID.String(),
				"contact_name": contactName,
				"company_name": companyName,
				"amount":       amount,
				"currency":     currency,
				"stage":        stageCode,
				"stale_days":   staleDays,
			},
			DedupeKey: leadID.String() + ":" + lastStr,
			Cooldown:  0, // one-shot per stale window
		})
	}
}
