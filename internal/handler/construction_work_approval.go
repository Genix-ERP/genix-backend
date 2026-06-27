package handler

import (
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// WORK APPROVAL WORKFLOW (Bosqichlar v2)
// =====================================================
//
// Per construction_module_v2.html, every "work" (= one row in
// construction_estimate_line that lives under a stage / sub-stage)
// flows through five states:
//
//   pending → in_progress → submitted → confirmed_supervisor → confirmed_engineer
//
// Three roles drive the transitions:
//
//   • foreman (прораб)     — enters done_quantity, then "submits" the work.
//   • supervisor (технадзор) — confirms or rejects submitted works.
//   • engineer (гл. инженер) — finalises confirmed-by-supervisor works.
//                              Final confirmation LOCKS the row.
//
// Roles live in `construction_project_team.role` (existing column,
// migration 047 + extensions). The role string is matched
// case-insensitively against {foreman, supervisor, engineer}; any other
// value (including legacy free-form roles) gets no workflow permissions.
//
// Status, done_quantity, and reviewer fields live on
// construction_estimate_line (migration 353).
//
// Every successful transition writes a `qty_change` / role-specific row
// to construction_smeta_audit so the Jurnal tab carries the trail.

// =====================================================
// HELPERS
// =====================================================

// resolveProjectRole returns the canonical workflow role for the user on
// a given project: "foreman" | "supervisor" | "engineer" | "" (none).
//
// Lookup order:
//  1. construction_project_team — the per-project assignment.
//  2. tenant_settings.construction.roles.{foreman,supervisor,engineer}_user_id —
//     the tenant-wide defaults set in Settings → Construction. These let
//     a small company assign the three roles once and have them apply to
//     every project without populating the project team for each one.
//
// First non-empty match wins.
func (h *Handler) resolveProjectRole(tenantID, userID uuid.UUID, projectID int64) string {
	roles := h.resolveProjectRoles(tenantID, userID, projectID)
	if len(roles) > 0 {
		return roles[0]
	}
	return ""
}

// resolveProjectRoles returns EVERY workflow role the user holds on the
// project. Unlike resolveProjectRole (which returns a single priority-
// resolved role for display defaults / budget hiding), this merges the
// per-project team rows AND the tenant-settings role lists, so a user
// assigned to more than one role (e.g. both supervisor and engineer) gets
// all of them. Deduped, returned in canonical order (foreman, supervisor,
// engineer). An EMPTY slice means "no role assigned" — callers treat that
// as tenant-admin/demo and skip the role gate.
func (h *Handler) resolveProjectRoles(tenantID, userID uuid.UUID, projectID int64) []string {
	if userID == uuid.Nil {
		return nil
	}

	// Identities to match against. CRITICAL: `users.id` (auth, what
	// GetUserID returns) and `employees.id` are DIFFERENT id spaces, and the
	// construction role lists + team rows key off the EMPLOYEE id. The
	// authoritative link is `users.employee_id → employees.id`
	// (employees.user_id is frequently NULL, so the reverse lookup misses).
	// We therefore match against the user id, the user's linked employee id,
	// AND — as a fallback — any employee row that points back at this user.
	idents := []string{userID.String()}
	employeeID := uuid.Nil
	var linkedEmp uuid.NullUUID
	if err := h.db.QueryRow(
		`SELECT employee_id FROM users WHERE id = $1 AND tenant_id = $2`, userID, tenantID,
	).Scan(&linkedEmp); err == nil && linkedEmp.Valid && linkedEmp.UUID != uuid.Nil {
		employeeID = linkedEmp.UUID
		idents = append(idents, employeeID.String())
	}
	if rows, err := h.db.Query(
		`SELECT id FROM employees WHERE user_id = $1 AND tenant_id = $2`, userID, tenantID,
	); err == nil {
		for rows.Next() {
			var eid uuid.UUID
			if rows.Scan(&eid) == nil && eid != uuid.Nil {
				idents = append(idents, eid.String())
			}
		}
		rows.Close()
	}

	set := map[string]bool{}
	mark := func(raw string) {
		switch normaliseRole(raw) {
		case "foreman":
			set["foreman"] = true
		case "supervisor":
			set["supervisor"] = true
		case "engineer":
			set["engineer"] = true
		}
	}

	// Step 1: per-project team roles — match by employee_id OR the employee's
	// linked user_id, so it works regardless of which id the row was keyed on.
	if rows, err := h.db.Query(`
		SELECT pt.role
		FROM construction_project_team pt
		LEFT JOIN employees e ON e.id = pt.employee_id
		WHERE pt.tenant_id = $1 AND pt.project_id = $2
		  AND COALESCE(pt.status, 'active') = 'active'
		  AND (pt.employee_id = $3 OR pt.employee_id = $4 OR e.user_id = $3)
	`, tenantID, projectID, userID, employeeID); err == nil {
		for rows.Next() {
			var raw sql.NullString
			if rows.Scan(&raw) == nil && raw.Valid {
				mark(raw.String)
			}
		}
		rows.Close()
	}

	// Step 2: tenant-wide settings (legacy single id OR new *_user_ids array).
	var foremanID, supervisorID, engineerID sql.NullString
	var foremanIDs, supervisorIDs, engineerIDs []byte
	_ = h.db.QueryRow(`
		SELECT
		    settings->'construction'->'roles'->>'foreman_user_id',
		    settings->'construction'->'roles'->>'supervisor_user_id',
		    settings->'construction'->'roles'->>'engineer_user_id',
		    COALESCE(settings->'construction'->'roles'->'foreman_user_ids',    '[]'::jsonb),
		    COALESCE(settings->'construction'->'roles'->'supervisor_user_ids', '[]'::jsonb),
		    COALESCE(settings->'construction'->'roles'->'engineer_user_ids',   '[]'::jsonb)
		FROM tenant_settings
		WHERE tenant_id = $1
	`, tenantID).Scan(&foremanID, &supervisorID, &engineerID,
		&foremanIDs, &supervisorIDs, &engineerIDs)

	// inList: does the single legacy id OR the JSON array contain ANY of the
	// caller's identities (user id or employee id)?
	inList := func(single sql.NullString, arr []byte) bool {
		for _, id := range idents {
			if single.Valid && single.String == id {
				return true
			}
			if jsonArrayContains(arr, id) {
				return true
			}
		}
		return false
	}
	if inList(foremanID, foremanIDs) {
		set["foreman"] = true
	}
	if inList(supervisorID, supervisorIDs) {
		set["supervisor"] = true
	}
	if inList(engineerID, engineerIDs) {
		set["engineer"] = true
	}

	out := make([]string, 0, 3)
	for _, r := range []string{"foreman", "supervisor", "engineer"} {
		if set[r] {
			out = append(out, r)
		}
	}
	return out
}

// roleSetHas reports whether the resolved role set contains target.
func roleSetHas(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}

// jsonArrayContains reports whether the given JSON-encoded string array
// contains target. Returns false on parse error or non-array input —
// missing/malformed settings fall back to the legacy single-user path.
func jsonArrayContains(raw []byte, target string) bool {
	if len(raw) == 0 || target == "" {
		return false
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return false
	}
	for _, s := range arr {
		if s == target {
			return true
		}
	}
	return false
}

// normaliseRole maps the various free-form role strings stored on
// construction_project_team.role onto the three canonical workflow keys.
// Lowercased + trimmed against an allowlist of common Uzbek/Russian/English
// labels so existing data continues to work without a backfill migration.
func normaliseRole(s string) string {
	x := ""
	for _, ch := range s {
		switch {
		case ch >= 'A' && ch <= 'Z':
			x += string(ch + 32)
		case ch == ' ' || ch == '\t' || ch == '\n':
			// drop whitespace
		default:
			x += string(ch)
		}
	}
	switch x {
	case "foreman", "прораб", "proraq", "qurilishboshchisi", "ustaqurilishchi":
		return "foreman"
	case "supervisor", "технадзор", "tehnadzor", "texnadzor", "nazoratchi":
		return "supervisor"
	case "engineer", "инженер", "главныйинженер", "глинженер",
		"bosh-injener", "boshinjener", "muhandis", "boshmuhandis":
		return "engineer"
	}
	return x // returned as-is so the handler can decide it doesn't match
}

// loadWorkContext fetches the metadata needed to authorise + execute a
// transition: the estimate id, project id, current status, plan + done
// quantity, and the line's display name.
type workCtx struct {
	LineID         int64
	EstimateID     int64
	ProjectID      int64
	Status         string
	PlanQty        float64
	DoneQty        float64
	Name           string
}

func (h *Handler) loadWorkContext(tenantID uuid.UUID, lineID int64) (*workCtx, error) {
	var w workCtx
	w.LineID = lineID
	err := h.db.QueryRow(`
		SELECT l.estimate_id, e.project_id,
		       COALESCE(l.approval_status, 'pending'),
		       COALESCE(l.quantity, 0),
		       COALESCE(l.done_quantity, 0),
		       COALESCE(l.name, '')
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE l.id = $1 AND l.tenant_id = $2
	`, lineID, tenantID).Scan(&w.EstimateID, &w.ProjectID, &w.Status, &w.PlanQty, &w.DoneQty, &w.Name)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// =====================================================
// FOREMAN ACTIONS
// =====================================================

// UpdateWorkDoneQuantity — POST /construction/works/:id/done-quantity
// Body: { done_quantity: number }
//
// Foreman-only. Allowed while status is pending or in_progress. Auto-
// transitions: 0 → pending, >0 → in_progress.
func (h *Handler) UpdateWorkDoneQuantity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	userName := c.GetString("user_name")

	lineID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid work id")
		return
	}
	var body struct {
		DoneQuantity float64 `json:"done_quantity"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	ctx, err := h.loadWorkContext(tenantID, lineID)
	if err != nil {
		response.NotFound(c, "Work not found")
		return
	}

	// Authorisation. Treat tenant admins (role "") as foreman so a
	// freshly-imported project where roles haven't been assigned yet
	// can still be exercised by the project owner.
	roles := h.resolveProjectRoles(tenantID, userID, ctx.ProjectID)
	if len(roles) > 0 && !roleSetHas(roles, "foreman") {
		response.Forbidden(c, "Only the project foreman can update done quantity")
		return
	}
	if ctx.Status != "pending" && ctx.Status != "in_progress" {
		response.BadRequest(c, "Work is locked or already submitted; cannot edit done quantity")
		return
	}

	// Clamp ≥ 0. The previous "done ≤ plan" ceiling was dropped because
	// template-mode imports leave plan=0 and the user's authoritative
	// number IS the BAJARILDI value — capping at plan would freeze the
	// foreman at 0. The Smeta boshqaruvi ISH HAJMI mirror below keeps
	// the two screens in sync regardless of which one was edited last.
	//
	// NOTE on semantics (migration 419 / multi-iteration Forma 2):
	// `body.DoneQuantity` now represents this OPEN iteration's
	// period contribution, not the cumulative. The wire field name
	// stays `done_quantity` so the frontend doesn't need to flip its
	// payload shape, but conceptually:
	//
	//   period_fakt (this iteration)         = body.DoneQuantity
	//   line.done_quantity (cumulative)      = Σ period_fakt across iters
	//
	// Legacy behaviour is preserved because the migration 419 backfill
	// stamped iteration #1.period_fakt = old done_quantity for every
	// line, and there's exactly one open iter per project. Until the
	// first freeze, this endpoint behaves identically to before.
	periodFakt := body.DoneQuantity
	if periodFakt < 0 {
		periodFakt = 0
	}

	// Resolve the open iteration for this project. If none exists
	// (shouldn't happen post-backfill, but defence in depth), bootstrap
	// one so the write doesn't silently no-op.
	var openIterID int64
	err = h.db.QueryRow(`
		SELECT id FROM construction_form2_iteration
		WHERE project_id = $1 AND tenant_id = $2 AND status = 'open'
	`, ctx.ProjectID, tenantID).Scan(&openIterID)
	if err == sql.ErrNoRows {
		if insertErr := h.db.QueryRow(`
			INSERT INTO construction_form2_iteration
			    (tenant_id, project_id, iteration_seq, status, opened_at, opened_by)
			VALUES ($1, $2, 1, 'open', NOW(), $3)
			RETURNING id
		`, tenantID, ctx.ProjectID, userID).Scan(&openIterID); insertErr != nil {
			h.log.Error("Failed to seed open iteration on fakt write", "error", insertErr)
			response.InternalError(c, "Failed to update done quantity")
			return
		}
	} else if err != nil {
		h.log.Error("Failed to resolve open iteration", "error", err)
		response.InternalError(c, "Failed to update done quantity")
		return
	}

	// Upsert this line's period_fakt for the open iteration. Migration
	// 419 pre-creates iteration_line rows on freeze, but legacy
	// estimate_lines created BEFORE 419 ran on this DB, or new lines
	// inserted into an existing project after iteration #N opened, may
	// not have a row yet — INSERT … ON CONFLICT covers both.
	if _, err := h.db.Exec(`
		INSERT INTO construction_form2_iteration_line
		    (iteration_id, estimate_line_id, period_fakt, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (iteration_id, estimate_line_id)
		DO UPDATE SET period_fakt = EXCLUDED.period_fakt,
		              updated_at  = NOW()
	`, openIterID, lineID, periodFakt); err != nil {
		h.log.Error("Failed to write period_fakt", "error", err, "line_id", lineID, "iter_id", openIterID)
		response.InternalError(c, "Failed to update done quantity")
		return
	}

	// Recompute the cumulative done_quantity = Σ period_fakt across
	// every iteration for this line. Frozen iters contribute their
	// historical numbers, the open iter contributes what we just wrote.
	var done float64
	if err := h.db.QueryRow(`
		SELECT COALESCE(SUM(period_fakt), 0)
		FROM construction_form2_iteration_line
		WHERE estimate_line_id = $1
	`, lineID).Scan(&done); err != nil {
		h.log.Error("Failed to recompute cumulative done_quantity", "error", err)
		response.InternalError(c, "Failed to update done quantity")
		return
	}

	newStatus := "pending"
	if done > 0 {
		newStatus = "in_progress"
	}

	if _, err := h.db.Exec(`
		UPDATE construction_estimate_line
		SET done_quantity   = $1,
		    approval_status = $2,
		    updated_date    = NOW()
		WHERE id = $3 AND tenant_id = $4
	`, done, newStatus, lineID, tenantID); err != nil {
		h.log.Error("Failed to update work done quantity", "error", err)
		response.InternalError(c, "Failed to update done quantity")
		return
	}

	// Mirror BAJARILDI → ISH HAJMI for parent works so the Smeta
	// boshqaruvi tab and the Bosqichlar tab show the same number for
	// the same work. Then cascade the new plan to non-override
	// sub-lines (sub.quantity = parent.quantity × norm_rate) — same
	// math as UpdateEstimateLine. We only mirror on top-level rows
	// (parent_line_id IS NULL) to avoid stomping on a sub-line's own
	// quantity, which is computed from its parent's plan.
	if _, err := h.db.Exec(`
		UPDATE construction_estimate_line
		SET quantity     = $1,
		    total_amount = $1 * COALESCE(unit_rate, 0),
		    updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3 AND parent_line_id IS NULL
	`, done, lineID, tenantID); err != nil {
		h.log.Error("Failed to mirror done_quantity to plan quantity",
			"error", err, "line_id", lineID)
	}
	if _, err := h.db.Exec(`
		UPDATE construction_estimate_line c
		SET quantity     = $1 * COALESCE(c.norm_rate, 0),
		    total_amount = ($1 * COALESCE(c.norm_rate, 0)) * COALESCE(c.unit_rate, 0),
		    updated_date = NOW()
		WHERE c.parent_line_id = $2
		  AND COALESCE(c.quantity_override, FALSE) = FALSE
	`, done, lineID); err != nil {
		h.log.Error("Failed to cascade plan quantity to children",
			"error", err, "parent_line_id", lineID)
	}

	// If this work belongs to a subcontractor's estimate, propagate the
	// aggregated progress onto the matching work in the project's own
	// (in-house) estimate so the consolidated project view stays in sync.
	h.syncSubcontractorWorkToProject(tenantID, ctx.ProjectID, lineID)

	h.logSmetaAudit(tenantID, ctx.ProjectID, &ctx.EstimateID, "qty_change", ctx.Name, &lineID,
		strconv.FormatFloat(ctx.DoneQty, 'f', -1, 64),
		strconv.FormatFloat(done, 'f', -1, 64),
		"Bajarilgan hajm yangilandi", userID, userName)

	response.Success(c, gin.H{"done_quantity": done, "approval_status": newStatus, "quantity": done})
}

// syncSubcontractorWorkToProject mirrors a subcontractor work's progress onto
// the matching work in the project's own (in-house) estimate. Matching is by
// section (parent_item_number) + name + uom within the same building. The
// project work's done becomes the SUM of every subcontractor's done for that
// work, so multiple subcontractors splitting one project work aggregate
// correctly. No-op when the edited line isn't a subcontractor work or has no
// in-house counterpart.
func (h *Handler) syncSubcontractorWorkToProject(tenantID uuid.UUID, projectID, lineID int64) {
	var isSub bool
	var buildingID sql.NullInt64
	var section, name, uom string
	err := h.db.QueryRow(`
		SELECT (e.subcontract_id IS NOT NULL), e.building_id,
		       TRIM(COALESCE(l.parent_item_number, '')), TRIM(COALESCE(l.name, '')), TRIM(COALESCE(l.uom, ''))
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id AND e.tenant_id = l.tenant_id
		WHERE l.id = $1 AND l.tenant_id = $2
		  AND l.parent_line_id IS NULL AND COALESCE(l.resource_type, '') = ''
	`, lineID, tenantID).Scan(&isSub, &buildingID, &section, &name, &uom)
	if err != nil || !isSub {
		return // not a subcontractor work (or not found) — nothing to sync
	}

	// Sum done across ALL subcontractor edinich works matching the key, then
	// write it onto the matching project (in-house) edinich work(s).
	rows, err := h.db.Query(`
		WITH sub_sum AS (
			SELECT COALESCE(SUM(l.done_quantity), 0) AS total
			FROM construction_estimate_line l
			JOIN construction_estimate e ON e.id = l.estimate_id AND e.tenant_id = l.tenant_id
			WHERE e.tenant_id = $1 AND e.project_id = $2
			  AND e.subcontract_id IS NOT NULL
			  AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
			  AND e.building_id IS NOT DISTINCT FROM $3
			  AND l.parent_line_id IS NULL AND COALESCE(l.resource_type, '') = ''
			  AND TRIM(COALESCE(l.parent_item_number, '')) = $4
			  AND LOWER(TRIM(COALESCE(l.name, ''))) = LOWER($5)
			  AND TRIM(COALESCE(l.uom, '')) = $6
		)
		UPDATE construction_estimate_line p
		SET done_quantity   = (SELECT total FROM sub_sum),
		    quantity        = (SELECT total FROM sub_sum),
		    total_amount    = (SELECT total FROM sub_sum) * COALESCE(p.unit_rate, 0),
		    approval_status = CASE WHEN (SELECT total FROM sub_sum) > 0 THEN 'in_progress' ELSE 'pending' END,
		    updated_date    = NOW()
		FROM construction_estimate e
		WHERE p.estimate_id = e.id AND p.tenant_id = $1 AND e.tenant_id = $1
		  AND e.project_id = $2
		  AND e.subcontract_id IS NULL
		  AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
		  AND e.building_id IS NOT DISTINCT FROM $3
		  AND p.parent_line_id IS NULL AND COALESCE(p.resource_type, '') = ''
		  AND TRIM(COALESCE(p.parent_item_number, '')) = $4
		  AND LOWER(TRIM(COALESCE(p.name, ''))) = LOWER($5)
		  AND TRIM(COALESCE(p.uom, '')) = $6
		RETURNING p.id, p.done_quantity
	`, tenantID, projectID, buildingID, section, name, uom)
	if err != nil {
		h.log.Error("Failed to sync subcontractor done to project", "error", err, "line_id", lineID)
		return
	}
	defer rows.Close()
	type pl struct {
		id   int64
		done float64
	}
	var updated []pl
	for rows.Next() {
		var x pl
		if rows.Scan(&x.id, &x.done) == nil {
			updated = append(updated, x)
		}
	}
	rows.Close()
	// Cascade each updated project parent's new plan to its non-override
	// sub-lines (resource qty = parent.done × norm_rate).
	for _, x := range updated {
		if _, err := h.db.Exec(`
			UPDATE construction_estimate_line c
			SET quantity     = $1 * COALESCE(c.norm_rate, 0),
			    total_amount = ($1 * COALESCE(c.norm_rate, 0)) * COALESCE(c.unit_rate, 0),
			    updated_date = NOW()
			WHERE c.parent_line_id = $2
			  AND COALESCE(c.quantity_override, FALSE) = FALSE
		`, x.done, x.id); err != nil {
			h.log.Error("Failed to cascade project plan to children", "error", err, "parent_line_id", x.id)
		}
	}
}

// SubmitWork — POST /construction/works/:id/submit
// Foreman moves work in_progress → submitted.
func (h *Handler) SubmitWork(c *gin.Context) {
	h.transitionWork(c, "submitted",
		[]string{"in_progress", "pending"},
		"foreman",
		"submitted_at = NOW(), submitted_by = $userID",
		"Ish texnadzorga yuborildi",
	)
}

// BulkSubmitWorks — POST /construction/works/bulk-submit
// Body: { work_ids: [int64, ...] }
// Foreman moves every supplied work pending/in_progress → submitted (skipping
// any that don't have done_qty > 0). Idempotent for already-submitted rows.
func (h *Handler) BulkSubmitWorks(c *gin.Context) {
	h.bulkWorksTransition(c, "submitted",
		[]string{"pending", "in_progress"},
		"foreman",
		"done_quantity > 0",
		"submitted_at = NOW(), submitted_by = $1",
		"Bosqich texnadzorga yuborildi",
	)
}

// =====================================================
// SUPERVISOR ACTIONS
// =====================================================

// ConfirmWorkSupervisor — POST /construction/works/:id/confirm-supervisor
func (h *Handler) ConfirmWorkSupervisor(c *gin.Context) {
	h.transitionWork(c, "confirmed_supervisor",
		[]string{"submitted"},
		"supervisor",
		"confirmed_supervisor_at = NOW(), confirmed_supervisor_by = $userID, rejection_note = NULL",
		"Texnadzor tasdiqladi",
	)
}

// RejectWorkSupervisor — POST /construction/works/:id/reject-supervisor
// Returns the work back to in_progress so the foreman can fix it.
func (h *Handler) RejectWorkSupervisor(c *gin.Context) {
	h.transitionWork(c, "in_progress",
		[]string{"submitted"},
		"supervisor",
		"submitted_at = NULL, submitted_by = NULL, rejection_note = $note",
		"Texnadzor qaytardi",
	)
}

// BulkConfirmSupervisor — POST /construction/works/bulk-confirm-supervisor
func (h *Handler) BulkConfirmSupervisor(c *gin.Context) {
	h.bulkWorksTransition(c, "confirmed_supervisor",
		[]string{"submitted"},
		"supervisor",
		"",
		"confirmed_supervisor_at = NOW(), confirmed_supervisor_by = $1, rejection_note = NULL",
		"Texnadzor barchasini tasdiqladi",
	)
}

// =====================================================
// ENGINEER ACTIONS
// =====================================================

// ConfirmWorkEngineer — POST /construction/works/:id/confirm-engineer
// Final confirmation. Locks the row.
func (h *Handler) ConfirmWorkEngineer(c *gin.Context) {
	h.transitionWork(c, "confirmed_engineer",
		[]string{"confirmed_supervisor"},
		"engineer",
		"confirmed_engineer_at = NOW(), confirmed_engineer_by = $userID, rejection_note = NULL",
		"Bosh muhandis yakunladi (LOCKED)",
	)
}

// RejectWorkEngineer — POST /construction/works/:id/reject-engineer
// Returns the work back to submitted so the supervisor reviews again.
func (h *Handler) RejectWorkEngineer(c *gin.Context) {
	h.transitionWork(c, "submitted",
		[]string{"confirmed_supervisor"},
		"engineer",
		"confirmed_supervisor_at = NULL, confirmed_supervisor_by = NULL, rejection_note = $note",
		"Bosh muhandis qaytardi",
	)
}

// BulkConfirmEngineer — POST /construction/works/bulk-confirm-engineer
func (h *Handler) BulkConfirmEngineer(c *gin.Context) {
	h.bulkWorksTransition(c, "confirmed_engineer",
		[]string{"confirmed_supervisor"},
		"engineer",
		"",
		"confirmed_engineer_at = NOW(), confirmed_engineer_by = $1, rejection_note = NULL",
		"Bosh muhandis barchasini yakunladi",
	)
}

// =====================================================
// CORE TRANSITION ENGINE
// =====================================================

// transitionWork is the shared engine for every per-work status change.
// Validates role, current status, then runs the UPDATE with the
// caller-supplied `extraSet` clause (which may reference $userID and
// $note). Writes one audit row using the supplied description.
func (h *Handler) transitionWork(
	c *gin.Context,
	newStatus string,
	allowedFrom []string,
	requiredRole string,
	extraSet string,
	auditDescription string,
) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	userName := c.GetString("user_name")

	lineID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid work id")
		return
	}

	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)

	ctx, err := h.loadWorkContext(tenantID, lineID)
	if err != nil {
		response.NotFound(c, "Work not found")
		return
	}

	// Authorisation — the user may act if ANY of their assigned roles is the
	// one this transition requires. Tenant admins (empty set) are permitted
	// as a fallback — same logic as UpdateWorkDoneQuantity.
	roles := h.resolveProjectRoles(tenantID, userID, ctx.ProjectID)
	if len(roles) > 0 && !roleSetHas(roles, requiredRole) {
		response.Forbidden(c, "Action not allowed for your project role")
		return
	}

	allowed := false
	for _, s := range allowedFrom {
		if ctx.Status == s {
			allowed = true
			break
		}
	}
	if !allowed {
		response.BadRequest(c, "Work is in '"+ctx.Status+"' state; this action is not valid here")
		return
	}

	// Build the SQL using positional placeholders. extraSet may reference
	// $userID (uuid) and $note (string) — substitute carefully so we don't
	// have to use a templating engine.
	args := []interface{}{newStatus, lineID, tenantID}
	q := "UPDATE construction_estimate_line SET approval_status = $1"

	// extraSet substitution — replace $userID and $note with placeholders.
	es := ""
	for i := 0; i < len(extraSet); i++ {
		if i+7 <= len(extraSet) && extraSet[i:i+7] == "$userID" {
			args = append(args, uuidArg(userID))
			es += "$" + strconv.Itoa(len(args))
			i += 6
			continue
		}
		if i+5 <= len(extraSet) && extraSet[i:i+5] == "$note" {
			args = append(args, body.Note)
			es += "$" + strconv.Itoa(len(args))
			i += 4
			continue
		}
		es += string(extraSet[i])
	}
	if es != "" {
		q += ", " + es
	}
	q += ", updated_date = NOW() WHERE id = $2 AND tenant_id = $3"

	if _, err := h.db.Exec(q, args...); err != nil {
		h.log.Error("Failed to transition work", "error", err, "to", newStatus)
		response.InternalError(c, "Failed to update status")
		return
	}

	desc := auditDescription
	if body.Note != "" {
		desc += " — " + body.Note
	}
	h.logSmetaAudit(tenantID, ctx.ProjectID, &ctx.EstimateID, "qty_change", ctx.Name, &lineID,
		ctx.Status, newStatus, desc, userID, userName)

	// ── Material reservation side-effects ────────────────────────────
	// Status update succeeded — wire the new state into the warehouse.
	// All three helpers are best-effort (they log warnings but don't
	// surface errors here) so a missing product / warehouse never blocks
	// the approval workflow itself.
	orgID, _ := middleware.GetOrganizationID(c)
	switch newStatus {
	case "submitted":
		// Foreman → supervisor. Create pending reservations sized as
		// done_quantity × subline.norm_rate.
		h.reserveMaterialsForWork(tenantID, orgID, userID, ctx.ProjectID, lineID)
	case "confirmed_engineer":
		// Engineer finalises. Approve every pending reservation, deduct
		// from quantity_on_hand (allowed to go negative).
		h.finaliseMaterialsForWork(tenantID, userID, ctx.ProjectID, lineID)
	case "in_progress":
		// Reverted from submitted → in_progress (supervisor rejected).
		// Release the reserved quantity so the warehouse balance
		// returns to its pre-submit value.
		if ctx.Status == "submitted" {
			h.cancelMaterialsForWork(tenantID, lineID)
		}
	}

	response.Success(c, gin.H{
		"id":              lineID,
		"approval_status": newStatus,
	})
}

// uuidArg coerces uuid.Nil → nil so the column can be set NULL via the
// same code path that writes a real uuid.
func uuidArg(u uuid.UUID) interface{} {
	if u == uuid.Nil {
		return nil
	}
	return u
}

// bulkWorksTransition is the engine behind every "Все работы → ..." /
// "Подтвердить все" / "Финально подтвердить все" button.
//
// Frontend collects the relevant work IDs (typically every work currently
// rendered under one stage card) and POSTs them as { work_ids }. The
// handler advances each ID that's currently in `allowedFrom` and matches
// `extraWhere` to `newStatus`, ignoring rows that don't qualify.
//
// `extraSet` uses normal Postgres positional placeholders ($1 = user
// uuid); no template substitution.
func (h *Handler) bulkWorksTransition(
	c *gin.Context,
	newStatus string,
	allowedFrom []string,
	requiredRole string,
	extraWhere string,
	extraSet string,
	auditDescription string,
) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	userName := c.GetString("user_name")

	var body struct {
		WorkIDs []int64 `json:"work_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.WorkIDs) == 0 {
		response.BadRequest(c, "work_ids list is required")
		return
	}

	// Resolve every supplied work's project so we can role-check.
	// All works in a single bulk call must belong to the same project;
	// the frontend always sends one stage's worth at a time.
	var projectID int64
	if err := h.db.QueryRow(`
		SELECT DISTINCT e.project_id
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		WHERE el.tenant_id = $1 AND el.id = ANY($2::bigint[])
		LIMIT 1
	`, tenantID, int64SliceArg(body.WorkIDs)).Scan(&projectID); err != nil {
		response.NotFound(c, "No matching works found")
		return
	}

	roles := h.resolveProjectRoles(tenantID, userID, projectID)
	if len(roles) > 0 && !roleSetHas(roles, requiredRole) {
		response.Forbidden(c, "Action not allowed for your project role")
		return
	}

	// Build the IN-list of allowed source statuses.
	statusListQ := ""
	args := []interface{}{uuidArg(userID), newStatus, tenantID, int64SliceArg(body.WorkIDs)}
	for i, s := range allowedFrom {
		if i > 0 {
			statusListQ += ","
		}
		args = append(args, s)
		statusListQ += "$" + strconv.Itoa(len(args))
	}

	q := "UPDATE construction_estimate_line SET approval_status = $2"
	if extraSet != "" {
		q += ", " + extraSet
	}
	q += `, updated_date = NOW()
		WHERE tenant_id = $3
		  AND id = ANY($4::bigint[])
		  AND approval_status IN (` + statusListQ + `)`
	if extraWhere != "" {
		q += " AND " + extraWhere
	}

	res, err := h.db.Exec(q, args...)
	if err != nil {
		h.log.Error("Failed to bulk-transition works", "error", err, "to", newStatus)
		response.InternalError(c, "Failed to update works")
		return
	}
	updated, _ := res.RowsAffected()

	h.logSmetaAudit(tenantID, projectID, nil, "qty_change", "", nil,
		"", strconv.FormatInt(updated, 10), auditDescription,
		userID, userName)

	// ── Material reservation side-effects (bulk path) ───────────────
	// Re-query which works actually carry the new status and run the
	// matching warehouse step on each. This is the bulk twin of the
	// per-work hook in transitionWork. Same best-effort semantics:
	// failures are logged, never surfaced to the caller.
	if newStatus == "submitted" || newStatus == "confirmed_engineer" || newStatus == "in_progress" {
		orgID, _ := middleware.GetOrganizationID(c)
		idRows, qErr := h.db.Query(`
			SELECT id FROM construction_estimate_line
			WHERE tenant_id = $1
			  AND id = ANY($2::bigint[])
			  AND approval_status = $3
		`, tenantID, int64SliceArg(body.WorkIDs), newStatus)
		if qErr == nil {
			var ids []int64
			for idRows.Next() {
				var wid int64
				if scanErr := idRows.Scan(&wid); scanErr == nil {
					ids = append(ids, wid)
				}
			}
			idRows.Close()
			for _, wid := range ids {
				switch newStatus {
				case "submitted":
					h.reserveMaterialsForWork(tenantID, orgID, userID, projectID, wid)
				case "confirmed_engineer":
					h.finaliseMaterialsForWork(tenantID, userID, projectID, wid)
				case "in_progress":
					h.cancelMaterialsForWork(tenantID, wid)
				}
			}
		} else {
			h.log.Error("bulkWorksTransition: failed to re-query updated work ids",
				"error", qErr, "to", newStatus)
		}
	}

	response.Success(c, gin.H{
		"updated":         updated,
		"approval_status": newStatus,
	})
}

// int64SliceArg renders an []int64 as a Postgres bigint[] literal so it
// can be passed to ANY($N::bigint[]) without an extra dependency on pq.Array.
func int64SliceArg(xs []int64) string {
	out := "{"
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += strconv.FormatInt(x, 10)
	}
	out += "}"
	return out
}

// =====================================================
// READ: who am I on this project?
// =====================================================

// GetMyProjectRole — GET /construction/projects/:id/my-role
// Returns the caller's normalised workflow role for the project, plus a
// flag telling the frontend if the role was assigned at all (so it can
// fall back to the "tenant admin" demo behaviour).
func (h *Handler) GetMyProjectRole(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project id")
		return
	}
	roles := h.resolveProjectRoles(tenantID, userID, projectID)
	primary := ""
	if len(roles) > 0 {
		primary = roles[0]
	}
	// `role` stays the single primary for backward-compat; `roles` is the
	// full set the frontend uses to decide whether to show the role switcher
	// and which roles to offer.
	response.Success(c, gin.H{
		"role":     primary,
		"roles":    roles,
		"assigned": len(roles) > 0,
	})
}
