package handler

import (
	"database/sql"
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
	if userID == uuid.Nil {
		return ""
	}

	// Step 1: per-project team role.
	var rawRole sql.NullString
	_ = h.db.QueryRow(`
		SELECT role
		FROM construction_project_team
		WHERE tenant_id = $1 AND project_id = $2 AND employee_id = $3
		  AND COALESCE(status, 'active') = 'active'
		ORDER BY created_date DESC
		LIMIT 1
	`, tenantID, projectID, userID).Scan(&rawRole)
	if rawRole.Valid {
		switch normaliseRole(rawRole.String) {
		case "foreman":
			return "foreman"
		case "supervisor":
			return "supervisor"
		case "engineer":
			return "engineer"
		}
	}

	// Step 2: tenant-wide settings fallback. The three role IDs are
	// stored under tenant_settings.settings JSONB at:
	//   .construction.roles.foreman_user_id
	//   .construction.roles.supervisor_user_id
	//   .construction.roles.engineer_user_id
	//
	// We query all three in one round-trip and match against userID.
	var foremanID, supervisorID, engineerID sql.NullString
	_ = h.db.QueryRow(`
		SELECT
		    settings->'construction'->'roles'->>'foreman_user_id'    AS foreman_id,
		    settings->'construction'->'roles'->>'supervisor_user_id' AS supervisor_id,
		    settings->'construction'->'roles'->>'engineer_user_id'   AS engineer_id
		FROM tenant_settings
		WHERE tenant_id = $1
	`, tenantID).Scan(&foremanID, &supervisorID, &engineerID)

	uid := userID.String()
	if foremanID.Valid && foremanID.String == uid {
		return "foreman"
	}
	if supervisorID.Valid && supervisorID.String == uid {
		return "supervisor"
	}
	if engineerID.Valid && engineerID.String == uid {
		return "engineer"
	}

	return ""
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
	role := h.resolveProjectRole(tenantID, userID, ctx.ProjectID)
	if role != "foreman" && role != "" {
		response.Forbidden(c, "Only the project foreman can update done quantity")
		return
	}
	if ctx.Status != "pending" && ctx.Status != "in_progress" {
		response.BadRequest(c, "Work is locked or already submitted; cannot edit done quantity")
		return
	}

	// Clamp 0 ≤ done ≤ plan.
	done := body.DoneQuantity
	if done < 0 {
		done = 0
	}
	if done > ctx.PlanQty && ctx.PlanQty > 0 {
		done = ctx.PlanQty
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

	h.logSmetaAudit(tenantID, ctx.ProjectID, &ctx.EstimateID, "qty_change", ctx.Name, &lineID,
		strconv.FormatFloat(ctx.DoneQty, 'f', -1, 64),
		strconv.FormatFloat(done, 'f', -1, 64),
		"Bajarilgan hajm yangilandi", userID, userName)

	response.Success(c, gin.H{"done_quantity": done, "approval_status": newStatus})
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

	// Authorisation.
	role := h.resolveProjectRole(tenantID, userID, ctx.ProjectID)
	if role != requiredRole && role != "" {
		// Tenant admin (no team-role assigned) is permitted as a
		// fallback — same logic as UpdateWorkDoneQuantity.
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

	role := h.resolveProjectRole(tenantID, userID, projectID)
	if role != requiredRole && role != "" {
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
	role := h.resolveProjectRole(tenantID, userID, projectID)
	response.Success(c, gin.H{
		"role":      role,
		"assigned":  role != "",
	})
}
