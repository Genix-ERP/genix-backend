package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// =====================================================
// FORMA 2 ITERATIONS — multi-run Forma 2 series
// =====================================================
//
// Schema: migration 419_construction_form2_iteration.sql
//
// A project's Forma 2 is built up over MULTIPLE submissions: the foreman
// reports work done this month, freezes that as "Forma 2 #1", and starts
// fresh on #2 next month. Each estimate_line's `done_quantity` stays the
// CUMULATIVE total (Σ period_fakt across all iterations); the per-
// iteration contribution lives in construction_form2_iteration_line so we
// can replay history and so the Bosqichlar tab can render frozen iters
// as read-only with that period's numbers.
//
// Invariants enforced here:
//   * Exactly one open iteration per project at a time (DB partial unique
//     index uq_form2_iter_one_open_per_project + SELECT … FOR UPDATE in
//     the freeze handler).
//   * Freezing also writes a construction_form2_snapshot row in the same
//     transaction so the Smeta boshqaruvi → Formalar tarixi list and the
//     iteration tab strip never disagree about "which Forma 2s exist".
//   * The new open iteration gets a row pre-created in
//     construction_form2_iteration_line for every estimate_line in the
//     project, period_fakt = 0. Pre-creating (vs lazy on first edit)
//     means the fakt write path is always an UPDATE, never INSERT —
//     no race against double-clicks adding the same (iter, line).

// Form2Iteration mirrors a row in construction_form2_iteration.
type Form2Iteration struct {
	ID           int64      `json:"id"`
	TenantID     uuid.UUID  `json:"tenant_id"`
	ProjectID    int64      `json:"project_id"`
	IterationSeq int        `json:"iteration_seq"`
	Status       string     `json:"status"`
	SnapshotID   *int64     `json:"snapshot_id,omitempty"`
	OpenedAt     time.Time  `json:"opened_at"`
	OpenedBy     *uuid.UUID `json:"opened_by,omitempty"`
	FrozenAt     *time.Time `json:"frozen_at,omitempty"`
	FrozenBy     *uuid.UUID `json:"frozen_by,omitempty"`
}

// Form2IterationLine is one row from construction_form2_iteration_line.
type Form2IterationLine struct {
	IterationID    int64   `json:"iteration_id"`
	EstimateLineID int64   `json:"estimate_line_id"`
	PeriodFakt     float64 `json:"period_fakt"`
}

// ListForm2Iterations returns every iteration for a project, oldest first
// so the frontend can render the tab strip "Forma 2 #1, #2, #3 (joriy)".
//
// Route: GET /construction/projects/:id/form2-iterations
func (h *Handler) ListForm2Iterations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project id")
		return
	}

	// Opt-in paging, selector-first like the buildings list: mobile renders
	// these as a tab strip ("Forma 2 #1, #2, #3 (joriy)"), so a truncated array
	// silently removes tabs rather than adding a second page.
	query := `
		SELECT id, tenant_id, project_id, iteration_seq, status,
		       snapshot_id, opened_at, opened_by, frozen_at, frozen_by
		FROM construction_form2_iteration
		WHERE project_id = $1 AND tenant_id = $2
		ORDER BY iteration_seq ASC, id ASC
	`
	args := []interface{}{projectID, tenantID}
	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list form2 iterations", "error", err, "project_id", projectID)
		response.InternalError(c, "Failed to list iterations")
		return
	}
	defer rows.Close()

	out := make([]Form2Iteration, 0, 4)
	for rows.Next() {
		var it Form2Iteration
		if scanErr := rows.Scan(
			&it.ID, &it.TenantID, &it.ProjectID, &it.IterationSeq, &it.Status,
			&it.SnapshotID, &it.OpenedAt, &it.OpenedBy, &it.FrozenAt, &it.FrozenBy,
		); scanErr != nil {
			h.log.Error("Failed to scan form2 iteration", "error", scanErr)
			response.InternalError(c, "Failed to list iterations")
			return
		}
		out = append(out, it)
	}

	if !paginate {
		response.Success(c, out)
		return
	}

	total := 0
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_form2_iteration WHERE project_id = $1 AND tenant_id = $2`,
		projectID, tenantID,
	).Scan(&total); err != nil {
		h.log.Error("Failed to count form2 iterations", "error", err)
		total = len(out)
	}
	response.Paginated(c, out, page, pageSize, total)
}

// GetForm2IterationLines returns every line's period_fakt for one
// iteration, used by the Bosqichlar tab to render that tab's numbers.
//
// Route: GET /construction/projects/:project_id/form2-iterations/:iter_id/lines
func (h *Handler) GetForm2IterationLines(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project id")
		return
	}
	iterID, err := strconv.ParseInt(c.Param("iter_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid iteration id")
		return
	}

	// Verify the iteration belongs to this tenant + project before
	// dumping its lines (defence against guessable IDs).
	var ownerProject int64
	if err := h.db.QueryRow(`
		SELECT project_id FROM construction_form2_iteration
		WHERE id = $1 AND tenant_id = $2
	`, iterID, tenantID).Scan(&ownerProject); err != nil {
		response.NotFound(c, "Iteration not found")
		return
	}
	if ownerProject != projectID {
		response.BadRequest(c, "Iteration does not belong to this project")
		return
	}

	// line_ids narrows the response to the lines the caller actually renders.
	// Mobile has been sending this parameter all along (smeta_management_service
	// listForm2IterationLines) and it was silently ignored, so the Bosqichlar
	// tab pulled every line of the iteration to display a handful.
	linesQuery := `
		SELECT iteration_id, estimate_line_id, period_fakt
		FROM construction_form2_iteration_line
		WHERE iteration_id = $1
	`
	args := []interface{}{iterID}
	countWhere := `WHERE iteration_id = $1`
	countArgs := []interface{}{iterID}

	if raw := strings.TrimSpace(c.Query("line_ids")); raw != "" {
		lineIDs := make([]int64, 0, 16)
		for _, tok := range strings.Split(raw, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			id, convErr := strconv.ParseInt(tok, 10, 64)
			if convErr != nil {
				response.BadRequest(c, "Invalid line_ids")
				return
			}
			lineIDs = append(lineIDs, id)
		}
		// An explicitly-empty list means "no lines", not "all lines" — falling
		// back to the full dump would be the opposite of what was asked.
		if len(lineIDs) == 0 {
			response.Success(c, make([]Form2IterationLine, 0))
			return
		}
		args = append(args, pq.Array(lineIDs))
		countArgs = append(countArgs, pq.Array(lineIDs))
		linesQuery += " AND estimate_line_id = ANY($2)"
		countWhere += " AND estimate_line_id = ANY($2)"
	}

	// Deterministic order so LIMIT/OFFSET pages cannot repeat or skip rows.
	linesQuery += " ORDER BY estimate_line_id ASC"

	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		linesQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(linesQuery, args...)
	if err != nil {
		h.log.Error("Failed to list iteration lines", "error", err, "iter_id", iterID)
		response.InternalError(c, "Failed to list iteration lines")
		return
	}
	defer rows.Close()

	out := make([]Form2IterationLine, 0, 128)
	for rows.Next() {
		var l Form2IterationLine
		if scanErr := rows.Scan(&l.IterationID, &l.EstimateLineID, &l.PeriodFakt); scanErr != nil {
			h.log.Error("Failed to scan iteration line", "error", scanErr)
			response.InternalError(c, "Failed to list iteration lines")
			return
		}
		out = append(out, l)
	}

	if !paginate {
		response.Success(c, out)
		return
	}

	total := 0
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_form2_iteration_line `+countWhere, countArgs...,
	).Scan(&total); err != nil {
		h.log.Error("Failed to count iteration lines", "error", err)
		total = len(out)
	}
	response.Paginated(c, out, page, pageSize, total)
}

// CreateForm2IterationInput optionally carries the snapshot payload
// (period dates, totals, lines JSONB) so the freeze action can hand
// CreateForm2Snapshot the same shape the manual "Save snapshot" button
// would have. All fields are optional — sensible defaults are picked
// from the project when they're absent (period = today, totals
// recomputed from the estimate's current state by the snapshot writer).
type CreateForm2IterationInput struct {
	// Optional — the estimate this snapshot belongs to. When omitted we
	// pick the first estimate in the project so legacy single-estimate
	// projects keep working without the frontend having to plumb it
	// through.
	EstimateID int64 `json:"estimate_id"`

	// Snapshot fields, mirror CreateForm2SnapshotInput.
	PeriodFrom        *string         `json:"period_from"`
	PeriodTo          *string         `json:"period_to"`
	OtherCostsPct     float64         `json:"other_costs_pct"`
	UseVat            *bool           `json:"use_vat"`
	TotalWithVat      float64         `json:"total_with_vat"`
	TotalWithoutVat   float64         `json:"total_without_vat"`
	ConstructionTotal float64         `json:"construction_total"`
	EquipmentTotal    float64         `json:"equipment_total"`
	ActNumber         string          `json:"act_number"`
	SnapshotData      json.RawMessage `json:"snapshot_data"`
}

// CreateForm2Iteration freezes the current open iteration, writes a
// snapshot row alongside it, then opens iteration N+1 with period_fakt = 0
// on every estimate_line in the project — all in one transaction.
//
// Route: POST /construction/projects/:id/form2-iterations
//
// Concurrency: takes SELECT … FOR UPDATE on the open iteration row.
// Combined with the partial unique index uq_form2_iter_one_open_per_project
// this means two simultaneous freeze requests serialise — the second one
// finds the previously-open iteration already frozen and re-reads the
// new open as its target (or simply returns the iteration created by
// the first request, depending on timing).
func (h *Handler) CreateForm2Iteration(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	userName := c.GetString("user_name")

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project id")
		return
	}

	// Confirm tenant ownership of the project before doing anything else.
	var ownerTenant uuid.UUID
	if err := h.db.QueryRow(
		`SELECT tenant_id FROM construction_projects WHERE id = $1`, projectID,
	).Scan(&ownerTenant); err != nil {
		response.NotFound(c, "Project not found")
		return
	}
	if ownerTenant != tenantID {
		response.NotFound(c, "Project not found")
		return
	}

	var in CreateForm2IterationInput
	if err := c.ShouldBindJSON(&in); err != nil {
		// The body is optional — an empty POST is "just freeze and
		// open next", which is a valid use case. Treat parse failure
		// on an empty body as an empty body.
		in = CreateForm2IterationInput{}
	}

	// Default snapshot fields.
	useVat := true
	if in.UseVat != nil {
		useVat = *in.UseVat
	}
	if len(in.SnapshotData) == 0 {
		in.SnapshotData = json.RawMessage("{}")
	}
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

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to open freeze tx", "error", err)
		response.InternalError(c, "Failed to freeze iteration")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. Lock the open iteration. Serialises concurrent freeze requests.
	var openIterID int64
	var openIterSeq int
	if err := tx.QueryRow(`
		SELECT id, iteration_seq
		FROM construction_form2_iteration
		WHERE project_id = $1 AND tenant_id = $2 AND status = 'open'
		FOR UPDATE
	`, projectID, tenantID).Scan(&openIterID, &openIterSeq); err != nil {
		if err == sql.ErrNoRows {
			// No open iteration — shouldn't happen post-backfill, but
			// be defensive. Create iteration #1 as 'open' so the next
			// caller can freeze it.
			seq := 1
			var newID int64
			if insertErr := tx.QueryRow(`
				INSERT INTO construction_form2_iteration
				    (tenant_id, project_id, iteration_seq, status, opened_at, opened_by)
				VALUES ($1, $2, $3, 'open', NOW(), $4)
				RETURNING id
			`, tenantID, projectID, seq, createdByVal).Scan(&newID); insertErr != nil {
				h.log.Error("Failed to seed open iteration", "error", insertErr)
				response.InternalError(c, "Failed to freeze iteration")
				return
			}
			if err := h.bootstrapIterationLines(tx, projectID, newID); err != nil {
				h.log.Error("Failed to bootstrap iteration lines", "error", err)
				response.InternalError(c, "Failed to freeze iteration")
				return
			}
			if err := tx.Commit(); err != nil {
				h.log.Error("Failed to commit seed-open tx", "error", err)
				response.InternalError(c, "Failed to freeze iteration")
				return
			}
			committed = true
			response.Created(c, gin.H{
				"id":            newID,
				"iteration_seq": seq,
				"status":        "open",
				"note":          "no open iteration existed; seeded one. Try again to actually freeze.",
			})
			return
		}
		h.log.Error("Failed to lock open iteration", "error", err, "project_id", projectID)
		response.InternalError(c, "Failed to freeze iteration")
		return
	}

	// 2. Resolve estimate_id for the snapshot row. The snapshot table
	// is per-estimate (migration 351), so we have to pick one. Caller
	// can pin it explicitly; otherwise default to the project's first
	// estimate (legacy single-estimate setup).
	var estimateID int64
	if in.EstimateID > 0 {
		estimateID = in.EstimateID
	} else {
		if err := tx.QueryRow(`
			SELECT id FROM construction_estimate
			WHERE project_id = $1 AND tenant_id = $2
			ORDER BY id ASC
			LIMIT 1
		`, projectID, tenantID).Scan(&estimateID); err != nil {
			h.log.Error("Failed to resolve estimate for snapshot", "error", err, "project_id", projectID)
			response.InternalError(c, "Failed to freeze iteration: no estimate in this project")
			return
		}
	}

	// 3. Write the snapshot row that this freeze produces. Matches the
	// shape CreateForm2Snapshot writes so Formalar tarixi reads it the
	// same way.
	var snapshotID int64
	if err := tx.QueryRow(`
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
		RETURNING id
	`,
		tenantID, projectID, estimateID,
		periodFrom, periodTo,
		in.OtherCostsPct, useVat,
		in.TotalWithVat, in.TotalWithoutVat,
		in.ConstructionTotal, in.EquipmentTotal,
		in.ActNumber, []byte(in.SnapshotData),
		createdByVal,
	).Scan(&snapshotID); err != nil {
		h.log.Error("Failed to write freeze snapshot", "error", err)
		response.InternalError(c, "Failed to freeze iteration")
		return
	}

	// 4. Flip the open iteration to frozen and link its snapshot.
	if _, err := tx.Exec(`
		UPDATE construction_form2_iteration
		SET status      = 'frozen',
		    frozen_at   = NOW(),
		    frozen_by   = $1,
		    snapshot_id = $2
		WHERE id = $3
	`, createdByVal, snapshotID, openIterID); err != nil {
		h.log.Error("Failed to flip iteration to frozen", "error", err, "iter_id", openIterID)
		response.InternalError(c, "Failed to freeze iteration")
		return
	}

	// 5. Open the next iteration.
	newSeq := openIterSeq + 1
	var newIterID int64
	if err := tx.QueryRow(`
		INSERT INTO construction_form2_iteration
		    (tenant_id, project_id, iteration_seq, status, opened_at, opened_by)
		VALUES ($1, $2, $3, 'open', NOW(), $4)
		RETURNING id
	`, tenantID, projectID, newSeq, createdByVal).Scan(&newIterID); err != nil {
		h.log.Error("Failed to open next iteration", "error", err)
		response.InternalError(c, "Failed to freeze iteration")
		return
	}

	// 6. Seed iteration_line rows for every estimate_line in the project
	// with period_fakt = 0. Per the user's "carry-over: all" decision,
	// lines already at 100% reja are still included — they'll appear
	// read-only-ish in the new tab (cumulative bar full) but the
	// foreman can still see them.
	if err := h.bootstrapIterationLines(tx, projectID, newIterID); err != nil {
		h.log.Error("Failed to bootstrap new iteration lines", "error", err)
		response.InternalError(c, "Failed to freeze iteration")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit freeze tx", "error", err)
		response.InternalError(c, "Failed to freeze iteration")
		return
	}
	committed = true

	// Audit — uses the same form_save action so existing Jurnal filters
	// keep working, with a distinguishing description.
	h.logSmetaAudit(tenantID, projectID, &estimateID, "form_save", in.ActNumber, nil,
		strconv.Itoa(openIterSeq), strconv.Itoa(newSeq),
		"Forma 2 muzlatildi va keyingi iteratsiya ochildi", userID, userName)

	response.Created(c, gin.H{
		"frozen_iteration_id":  openIterID,
		"frozen_iteration_seq": openIterSeq,
		"snapshot_id":          snapshotID,
		"new_iteration_id":     newIterID,
		"new_iteration_seq":    newSeq,
	})
}

// DeleteForm2Iteration deletes the current OPEN (joriy) Forma 2 and re-opens
// the frozen iteration immediately before it. This is the "delete the joriy
// and unfreeze the one before it" action: the user wants to discard the
// current empty period and resume editing the previous Forma 2.
//
// Route: DELETE /construction/projects/:id/form2-iterations/:iter_id
//
//	(:iter_id must be the open iteration)
//
// Deleting it:
//  1. removes the open iteration (along with its iteration_line rows, via
//     FK cascade),
//  2. flips the previous frozen iteration (seq − 1) back to 'open' (clears
//     frozen_at/by + snapshot_id), and
//  3. deletes the construction_form2_snapshot row that froze it, so Formalar
//     tarixi loses the corresponding entry.
//
// The "exactly one open iteration per project" invariant is preserved: we
// delete one open and re-open one frozen in the same transaction.
//
// Guards:
//   - the target must be the open iteration (you can't delete a frozen one
//     directly — delete the joriy to roll the chain back),
//   - there must be a frozen predecessor to unfreeze (can't delete the very
//     first iteration when nothing is frozen), and
//   - if the open iteration already has period_fakt entered on any line we
//     refuse (409) so the foreman's new-period work isn't silently discarded.
func (h *Handler) DeleteForm2Iteration(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	userName := c.GetString("user_name")

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project id")
		return
	}
	iterID, err := strconv.ParseInt(c.Param("iter_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid iteration id")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to open unfreeze tx", "error", err)
		response.InternalError(c, "Failed to delete iteration")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. Load + lock the target iteration. Must be the OPEN (joriy) one and
	//    belong to this tenant+project.
	var (
		openIterSeq  int
		targetStatus string
	)
	if err := tx.QueryRow(`
		SELECT iteration_seq, status
		FROM construction_form2_iteration
		WHERE id = $1 AND project_id = $2 AND tenant_id = $3
		FOR UPDATE
	`, iterID, projectID, tenantID).Scan(&openIterSeq, &targetStatus); err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Iteration not found")
			return
		}
		h.log.Error("Failed to lock target iteration", "error", err, "iter_id", iterID)
		response.InternalError(c, "Failed to delete iteration")
		return
	}
	if targetStatus != "open" {
		response.BadRequest(c, "Only the current (joriy) Forma 2 can be deleted")
		return
	}

	// 2. Lock the frozen predecessor (seq − 1) we'll re-open. Its existence is
	//    what makes the joriy deletable — without it there's nothing to roll
	//    back into (the very first iteration can't be deleted this way).
	var (
		prevIterID int64
		prevSeq    int
		snapshotID sql.NullInt64
	)
	if err := tx.QueryRow(`
		SELECT id, iteration_seq, snapshot_id
		FROM construction_form2_iteration
		WHERE project_id = $1 AND tenant_id = $2 AND status = 'frozen'
		      AND iteration_seq = $3
		FOR UPDATE
	`, projectID, tenantID, openIterSeq-1).Scan(&prevIterID, &prevSeq, &snapshotID); err != nil {
		if err == sql.ErrNoRows {
			response.BadRequest(c, "No frozen Forma 2 to unfreeze")
			return
		}
		h.log.Error("Failed to lock previous iteration", "error", err, "project_id", projectID)
		response.InternalError(c, "Failed to delete iteration")
		return
	}

	// 3. Refuse if the open iteration already carries entered work — deleting
	//    it would throw that away.
	var enteredLines int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM construction_form2_iteration_line
		WHERE iteration_id = $1 AND period_fakt <> 0
	`, iterID).Scan(&enteredLines); err != nil {
		h.log.Error("Failed to check open iteration fakt", "error", err, "iter_id", iterID)
		response.InternalError(c, "Failed to delete iteration")
		return
	}
	if enteredLines > 0 {
		response.Conflict(c, "Joriy Forma 2 da kiritilgan hajmlar bor — avval ularni tozalang")
		return
	}

	// 4. Delete the open iteration (cascade removes its iteration_line rows).
	if _, err := tx.Exec(`
		DELETE FROM construction_form2_iteration WHERE id = $1
	`, iterID); err != nil {
		h.log.Error("Failed to delete open iteration", "error", err, "iter_id", iterID)
		response.InternalError(c, "Failed to delete iteration")
		return
	}

	// 5. Re-open the frozen predecessor.
	if _, err := tx.Exec(`
		UPDATE construction_form2_iteration
		SET status      = 'open',
		    frozen_at   = NULL,
		    frozen_by   = NULL,
		    snapshot_id = NULL
		WHERE id = $1
	`, prevIterID); err != nil {
		h.log.Error("Failed to re-open iteration", "error", err, "iter_id", prevIterID)
		response.InternalError(c, "Failed to delete iteration")
		return
	}

	// 6. Drop the snapshot the freeze wrote so Formalar tarixi stays in sync.
	if snapshotID.Valid {
		if _, err := tx.Exec(`
			DELETE FROM construction_form2_snapshot
			WHERE id = $1 AND tenant_id = $2
		`, snapshotID.Int64, tenantID); err != nil {
			h.log.Error("Failed to delete freeze snapshot", "error", err, "snapshot_id", snapshotID.Int64)
			response.InternalError(c, "Failed to delete iteration")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit unfreeze tx", "error", err)
		response.InternalError(c, "Failed to delete iteration")
		return
	}
	committed = true

	// Audit — reuse form_delete so existing Jurnal filters keep working.
	h.logSmetaAudit(tenantID, projectID, nil, "form_delete", "", nil,
		strconv.Itoa(openIterSeq), strconv.Itoa(prevSeq),
		"Joriy Forma 2 o'chirildi va oldingisi muzlatishdan chiqarildi", userID, userName)

	response.Success(c, gin.H{
		"reopened_iteration_id":  prevIterID,
		"reopened_iteration_seq": prevSeq,
		"deleted_iteration_id":   iterID,
		"deleted_iteration_seq":  openIterSeq,
	})
}

// bootstrapIterationLines inserts a period_fakt = 0 row for every
// estimate_line in the project's estimates. Used by CreateForm2Iteration
// (new iteration) and by the defensive "no open iteration" recovery
// branch. Idempotent via ON CONFLICT.
func (h *Handler) bootstrapIterationLines(tx *sql.Tx, projectID int64, iterID int64) error {
	_, err := tx.Exec(`
		INSERT INTO construction_form2_iteration_line (iteration_id, estimate_line_id, period_fakt)
		SELECT $1, el.id, 0
		FROM construction_estimate          e
		JOIN construction_estimate_line     el ON el.estimate_id = e.id
		WHERE e.project_id = $2
		ON CONFLICT (iteration_id, estimate_line_id) DO NOTHING
	`, iterID, projectID)
	return err
}
