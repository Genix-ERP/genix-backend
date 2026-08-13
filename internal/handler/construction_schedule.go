package handler

import (
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
// ISH GRAFIGI (work schedule / Gantt) — S8 phase 1.
//
// Smeta va grafik BITTA ish ro'yxatining ikki ko'rinishi (Gectaro modeli):
// sana maydonlari construction_estimate_line ustida (migration 471), ish =
// resource_type='' AND parent_line_id=0, source_type='edinich'. Progress
// saqlanmaydi — done_quantity fakt-daftaridan hisoblanadi. Bog'liqliklar
// faqat FS + lag (construction_work_dependencies), yozishda sikl BFS bilan
// rad etiladi. Siljitish semantikasi ASAP: predecessor surilganda successor
// faqat `succ.start >= pred.end + lag + 1` sharti buzilsa oldinga suriladi
// (davomiylik saqlanadi), hammasi bitta tranzaksiyada.
// =====================================================

const schedDateLayout = "2006-01-02"

// workQtyCase mirrors the stage-works quantity resolution: ВОР import wins,
// then the NORMA anchor, then the live quantity ledger.
const workQtyCase = `CASE
	WHEN COALESCE(el.imported_quantity, 0) > 0 THEN el.imported_quantity
	WHEN COALESCE(el.original_quantity, 0) > 0 THEN el.original_quantity
	ELSE COALESCE(el.quantity, 0)
END`

// workRowFilter selects top-level work rows in the project's edinich
// estimates — keep in sync with construction_stages.go GetConstructionStageWorks.
//
// `subcontract_id IS NULL` keeps the grafik on the SAME estimate set the
// Smetalar ro'yxati shows: ListEstimates hides subcontractor estimates behind
// an explicit `scope=subcontract`, but a subcontract estimate is a copy of the
// main one carrying the same source_type='edinich' work rows. Without this
// clause the Gantt drew every subcontracted work twice — once from the
// project's own smeta, once from the subcontractor's copy — with nothing on
// the bar to tell them apart.
const workRowFilter = `LOWER(COALESCE(e.source_type, '')) = 'edinich'
	AND e.subcontract_id IS NULL
	AND COALESCE(el.resource_type, '') = ''
	AND COALESCE(el.parent_line_id, 0) = 0`

func sectionLeaf(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, " › ")
	return strings.TrimSpace(parts[len(parts)-1])
}

func parseSchedDate(s string) (time.Time, bool) {
	t, err := time.Parse(schedDateLayout, s)
	return t, err == nil
}

// GetWorkSchedule — GET /construction/projects/:id/schedule?building_id=
// Works (scheduled + unscheduled) + FS dependencies + project date frame.
func (h *Handler) GetWorkSchedule(c *gin.Context) {
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

	var plannedStart, plannedEnd nullableTime
	err = h.db.QueryRow(`
		SELECT planned_start_date, planned_end_date
		FROM construction_projects
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&plannedStart, &plannedEnd)
	if err != nil {
		response.NotFound(c, "Project not found")
		return
	}

	where := `el.tenant_id = $1 AND e.project_id = $2 AND ` + workRowFilter
	args := []interface{}{tenantID, projectID}
	if b := c.Query("building_id"); b != "" {
		bID, bErr := strconv.ParseInt(b, 10, 64)
		if bErr != nil {
			response.BadRequest(c, "Invalid building_id")
			return
		}
		where += ` AND e.building_id = $3`
		args = append(args, bID)
	}

	rows, err := h.db.Query(`
		SELECT el.id, el.estimate_id, COALESCE(el.item_number, ''), COALESCE(el.name, ''),
		       COALESCE(el.parent_item_number, ''), COALESCE(el.uom, ''),
		       `+workQtyCase+` AS quantity,
		       COALESCE(el.done_quantity, 0),
		       COALESCE(el.unit_rate, 0), COALESCE(el.total_amount, 0),
		       el.sched_start, el.sched_end, el.baseline_start, el.baseline_end,
		       COALESCE(el.approval_status, 'pending'),
		       COALESCE(e.building_id, 0), COALESCE(b.name, '')
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		LEFT JOIN construction_buildings b ON b.id = e.building_id
		WHERE `+where+`
		ORDER BY el.sort_order ASC, el.id ASC
	`, args...)
	if err != nil {
		h.log.Error("work schedule: failed to list works", "error", err)
		response.InternalError(c, "Failed to load work schedule")
		return
	}
	defer rows.Close()

	// Sof-prorab pul ko'rmaydi — qiymat-vazn o'rniga frontend miqdorga tushadi.
	priceHidden := h.constructionPriceRestricted(c, tenantID, projectID)

	works := []map[string]interface{}{}
	for rows.Next() {
		var id, estimateID, buildingID int64
		var itemNumber, name, sectionPath, uom, approvalStatus, buildingName string
		var quantity, doneQty, unitRate, totalAmount float64
		var schedStart, schedEnd, baseStart, baseEnd nullableTime
		if err := rows.Scan(&id, &estimateID, &itemNumber, &name, &sectionPath, &uom,
			&quantity, &doneQty, &unitRate, &totalAmount,
			&schedStart, &schedEnd, &baseStart, &baseEnd, &approvalStatus,
			&buildingID, &buildingName); err != nil {
			h.log.Error("work schedule: scan failed", "error", err)
			continue
		}
		if priceHidden {
			unitRate, totalAmount = 0, 0
		}
		progress := 0.0
		if quantity > 0 {
			progress = doneQty / quantity * 100
			if progress > 100 {
				progress = 100
			}
		}
		works = append(works, map[string]interface{}{
			"id":          id,
			"estimate_id": estimateID,
			// Blocks are usually clones of one another, so the same work name
			// repeats once per block. Ship the block so the grafik can label
			// and filter the bars instead of showing N identical rows.
			"building_id":     buildingID,
			"building_name":   buildingName,
			"item_number":     itemNumber,
			"name":            name,
			"section":         sectionLeaf(sectionPath),
			"section_path":    sectionPath,
			"uom":             uom,
			"quantity":        quantity,
			"done_quantity":   doneQty,
			"progress_pct":    round2(progress),
			"unit_rate":       unitRate,
			"total_amount":    totalAmount,
			"sched_start":     schedStart.stringVal(),
			"sched_end":       schedEnd.stringVal(),
			"baseline_start":  baseStart.stringVal(),
			"baseline_end":    baseEnd.stringVal(),
			"approval_status": approvalStatus,
		})
	}

	deps := []map[string]interface{}{}
	depRows, err := h.db.Query(`
		SELECT id, predecessor_line_id, successor_line_id, lag_days
		FROM construction_work_dependencies
		WHERE tenant_id = $1 AND project_id = $2
		ORDER BY id
	`, tenantID, projectID)
	if err != nil {
		h.log.Error("work schedule: failed to list dependencies", "error", err)
	} else {
		defer depRows.Close()
		for depRows.Next() {
			var id, pred, succ int64
			var lag int
			if err := depRows.Scan(&id, &pred, &succ, &lag); err == nil {
				deps = append(deps, map[string]interface{}{
					"id":                  id,
					"predecessor_line_id": pred,
					"successor_line_id":   succ,
					"lag_days":            lag,
				})
			}
		}
	}

	response.Success(c, map[string]interface{}{
		"works":        works,
		"dependencies": deps,
		"price_hidden": priceHidden,
		"project": map[string]interface{}{
			"planned_start_date": plannedStart.stringVal(),
			"planned_end_date":   plannedEnd.stringVal(),
		},
	})
}

// schedWork is the in-memory shape used by the propagation pass.
type schedWork struct {
	ID    int64
	Start time.Time
	End   time.Time
	Has   bool
}

type schedDep struct {
	Pred int64
	Succ int64
	Lag  int
}

// propagateFS repairs FS constraints after `movedID` got new dates: walks the
// dependency graph and pushes every successor whose start violates
// `pred.end + lag + 1` forward, keeping durations. Returns changed ids in a
// deterministic order. The dependency table is acyclic by construction
// (CreateWorkDependency rejects cycles) — the pass cap is belt-and-braces.
func propagateFS(works map[int64]*schedWork, deps []schedDep) []int64 {
	changedSet := map[int64]bool{}
	var changedOrder []int64
	for pass := 0; pass <= len(deps); pass++ {
		moved := false
		for _, d := range deps {
			pred, okP := works[d.Pred]
			succ, okS := works[d.Succ]
			if !okP || !okS || !pred.Has || !succ.Has {
				continue
			}
			reqStart := pred.End.AddDate(0, 0, d.Lag+1)
			if succ.Start.Before(reqStart) {
				delta := int(reqStart.Sub(succ.Start).Hours() / 24)
				succ.Start = succ.Start.AddDate(0, 0, delta)
				succ.End = succ.End.AddDate(0, 0, delta)
				if !changedSet[d.Succ] {
					changedSet[d.Succ] = true
					changedOrder = append(changedOrder, d.Succ)
				}
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	return changedOrder
}

// UpdateWorkSchedule — PUT /construction/works/:id/schedule
// Body: { sched_start, sched_end } (both YYYY-MM-DD, or both null to clear).
// Applies the change + FS propagation in ONE transaction; returns every
// changed work with prev dates so the client gets a single undo step.
func (h *Handler) UpdateWorkSchedule(c *gin.Context) {
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
		SchedStart *string `json:"sched_start"`
		SchedEnd   *string `json:"sched_end"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	clearing := body.SchedStart == nil && body.SchedEnd == nil
	var newStart, newEnd time.Time
	if !clearing {
		if body.SchedStart == nil || body.SchedEnd == nil {
			response.BadRequest(c, "sched_start and sched_end must be set together")
			return
		}
		var okS, okE bool
		newStart, okS = parseSchedDate(*body.SchedStart)
		newEnd, okE = parseSchedDate(*body.SchedEnd)
		if !okS || !okE {
			response.BadRequest(c, "Dates must be YYYY-MM-DD")
			return
		}
		if newEnd.Before(newStart) {
			response.BadRequest(c, "sched_end must be on or after sched_start")
			return
		}
	}

	// The line must be a top-level work of an edinich estimate; grab its
	// project + current dates + name in one shot.
	var projectID, estimateID int64
	var workName string
	var prevStart, prevEnd nullableTime
	err = h.db.QueryRow(`
		SELECT e.project_id, el.estimate_id, COALESCE(el.name, ''), el.sched_start, el.sched_end
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE el.id = $1 AND el.tenant_id = $2 AND `+workRowFilter+`
	`, lineID, tenantID).Scan(&projectID, &estimateID, &workName, &prevStart, &prevEnd)
	if err != nil {
		response.NotFound(c, "Work not found")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	if clearing {
		// Sanasi olib tashlangan ish yana "rejalashtirilmagan" holatga qaytadi —
		// schedule_source ham 'none' bo'lishi kerak, aks holda sanasiz ish
		// "qo'lda qo'yilgan" bo'lib qolib, avtoreja unga tegmay ketardi.
		if _, err := tx.Exec(`
			UPDATE construction_estimate_line
			SET sched_start = NULL, sched_end = NULL, schedule_source = 'none', updated_date = NOW()
			WHERE id = $1 AND tenant_id = $2
		`, lineID, tenantID); err != nil {
			h.log.Error("work schedule: clear failed", "error", err)
			response.InternalError(c, "Failed to update schedule")
			return
		}
		if err := tx.Commit(); err != nil {
			response.InternalError(c, "Failed to commit")
			return
		}
		response.Success(c, map[string]interface{}{"updated": []map[string]interface{}{{
			"id": lineID, "sched_start": nil, "sched_end": nil,
			"prev_start": prevStart.stringVal(), "prev_end": prevEnd.stringVal(),
		}}})
		return
	}

	// Bu yagona joy — foydalanuvchi bar'ni sudrab yoki sanani yozib o'zgartirgan
	// joy — schedule_source ni 'manual' qiladi (TZ §0.3: qo'lda qo'yilgan sana
	// daxlsiz). Ilgari hech qaysi qo'l yo'li buni yozmasdi, shuning uchun
	// avtoreja bir marta yurgizilgach, prorabning qo'l bilan qo'ygan sanasini
	// keyingi yugurish jimgina qaytarib tashlardi.
	//
	// Quyidagi propagatsiya (FS kaskadi) ataylab 'manual' qilinmaydi: u
	// foydalanuvchining qarori emas, uning harakatidan kelib chiqqan natija —
	// aks holda bitta sudrash butun zanjirni abadiy muzlatib qo'yardi.
	if _, err := tx.Exec(`
		UPDATE construction_estimate_line
		SET sched_start = $1, sched_end = $2, schedule_source = 'manual', updated_date = NOW()
		WHERE id = $3 AND tenant_id = $4
	`, newStart, newEnd, lineID, tenantID); err != nil {
		h.log.Error("work schedule: update failed", "error", err)
		response.InternalError(c, "Failed to update schedule")
		return
	}

	// Snapshot every scheduled work of the project + the dependency list,
	// then run the constraint-repair pass in memory.
	works := map[int64]*schedWork{}
	prevDates := map[int64][2]interface{}{}
	wRows, err := tx.Query(`
		SELECT el.id, el.sched_start, el.sched_end
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE el.tenant_id = $1 AND e.project_id = $2 AND `+workRowFilter+`
		FOR UPDATE OF el
	`, tenantID, projectID)
	if err != nil {
		h.log.Error("work schedule: snapshot failed", "error", err)
		response.InternalError(c, "Failed to update schedule")
		return
	}
	for wRows.Next() {
		var id int64
		var s, e nullableTime
		if err := wRows.Scan(&id, &s, &e); err != nil {
			continue
		}
		w := &schedWork{ID: id, Has: s.valid && e.valid}
		if w.Has {
			w.Start, w.End = s.time, e.time
		}
		works[id] = w
		prevDates[id] = [2]interface{}{s.stringVal(), e.stringVal()}
	}
	wRows.Close()

	var deps []schedDep
	dRows, err := tx.Query(`
		SELECT predecessor_line_id, successor_line_id, lag_days
		FROM construction_work_dependencies
		WHERE tenant_id = $1 AND project_id = $2
		ORDER BY id
	`, tenantID, projectID)
	if err != nil {
		h.log.Error("work schedule: deps load failed", "error", err)
		response.InternalError(c, "Failed to update schedule")
		return
	}
	for dRows.Next() {
		var d schedDep
		if err := dRows.Scan(&d.Pred, &d.Succ, &d.Lag); err == nil {
			deps = append(deps, d)
		}
	}
	dRows.Close()

	changed := propagateFS(works, deps)
	for _, id := range changed {
		w := works[id]
		if _, err := tx.Exec(`
			UPDATE construction_estimate_line
			SET sched_start = $1, sched_end = $2, updated_date = NOW()
			WHERE id = $3 AND tenant_id = $4
		`, w.Start, w.End, id, tenantID); err != nil {
			h.log.Error("work schedule: propagate update failed", "error", err, "line", id)
			response.InternalError(c, "Failed to update schedule")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
	}

	h.logSmetaAudit(tenantID, projectID, &estimateID, "sched_change", workName, &lineID,
		strFromVal(prevStart.stringVal())+" – "+strFromVal(prevEnd.stringVal()),
		newStart.Format(schedDateLayout)+" – "+newEnd.Format(schedDateLayout),
		"Ish grafigi: sana o'zgartirildi", userID, userName)

	updated := []map[string]interface{}{{
		"id":          lineID,
		"sched_start": newStart.Format(schedDateLayout),
		"sched_end":   newEnd.Format(schedDateLayout),
		"prev_start":  prevStart.stringVal(),
		"prev_end":    prevEnd.stringVal(),
	}}
	for _, id := range changed {
		if id == lineID {
			continue
		}
		w := works[id]
		prev := prevDates[id]
		updated = append(updated, map[string]interface{}{
			"id":          id,
			"sched_start": w.Start.Format(schedDateLayout),
			"sched_end":   w.End.Format(schedDateLayout),
			"prev_start":  prev[0],
			"prev_end":    prev[1],
		})
	}
	response.Success(c, map[string]interface{}{"updated": updated})
}

func strFromVal(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "—"
}

// BulkUpdateWorkSchedule — POST /construction/projects/:id/schedule/bulk
// Sets dates on many works WITHOUT propagation (undo restore / "add to
// schedule" defaults). All lines must be works of this project.
func (h *Handler) BulkUpdateWorkSchedule(c *gin.Context) {
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
	var body struct {
		Items []struct {
			LineID     int64   `json:"line_id"`
			SchedStart *string `json:"sched_start"`
			SchedEnd   *string `json:"sched_end"`
		} `json:"items"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Items) == 0 {
		response.BadRequest(c, "items is required")
		return
	}
	if len(body.Items) > 500 {
		response.BadRequest(c, "Too many items (max 500)")
		return
	}

	ids := make([]int64, 0, len(body.Items))
	for _, it := range body.Items {
		ids = append(ids, it.LineID)
	}
	var validCount int
	if err := h.db.QueryRow(`
		SELECT COUNT(*)
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE el.tenant_id = $1 AND e.project_id = $2 AND el.id = ANY($3) AND `+workRowFilter+`
	`, tenantID, projectID, pq.Array(ids)).Scan(&validCount); err != nil {
		h.log.Error("work schedule bulk: validation failed", "error", err)
		response.InternalError(c, "Failed to validate works")
		return
	}
	if validCount != len(ids) {
		response.BadRequest(c, "Some lines are not works of this project")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	for _, it := range body.Items {
		if it.SchedStart == nil && it.SchedEnd == nil {
			// Sanasiz ish 'none' ga qaytadi — yuqoridagi bir qatorli
			// tozalash bilan bir xil sabab.
			//
			// Sana YOZILADIGAN tarmoq (pastda) ataylab schedule_source ga
			// tegmaydi: bu endpoint'ni "Hammasini grafikka qo'shish",
			// "Grafikka qo'shish" va bekor qilish chaqiradi — ular tizim
			// bergan standart sanalar, prorabning tanlovi emas. Ularni
			// 'manual' qilish minglab ishni avtorejadan abadiy chiqarib
			// yuborardi.
			if _, err := tx.Exec(`
				UPDATE construction_estimate_line
				SET sched_start = NULL, sched_end = NULL, schedule_source = 'none', updated_date = NOW()
				WHERE id = $1 AND tenant_id = $2
			`, it.LineID, tenantID); err != nil {
				response.InternalError(c, "Failed to update schedule")
				return
			}
			continue
		}
		if it.SchedStart == nil || it.SchedEnd == nil {
			response.BadRequest(c, "sched_start and sched_end must be set together")
			return
		}
		s, okS := parseSchedDate(*it.SchedStart)
		e, okE := parseSchedDate(*it.SchedEnd)
		if !okS || !okE || e.Before(s) {
			response.BadRequest(c, "Invalid date range for line "+strconv.FormatInt(it.LineID, 10))
			return
		}
		if _, err := tx.Exec(`
			UPDATE construction_estimate_line
			SET sched_start = $1, sched_end = $2, updated_date = NOW()
			WHERE id = $3 AND tenant_id = $4
		`, s, e, it.LineID, tenantID); err != nil {
			response.InternalError(c, "Failed to update schedule")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
	}
	response.Success(c, map[string]interface{}{"updated": len(body.Items)})
}

// FreezeScheduleBaseline — POST /construction/projects/:id/schedule/baseline
// Copies sched_* → baseline_* for every scheduled work (ghost bars / plan-
// vs-fakt vaqt tahlili). Re-freezing overwrites the previous baseline.
func (h *Handler) FreezeScheduleBaseline(c *gin.Context) {
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
	res, err := h.db.Exec(`
		UPDATE construction_estimate_line el
		SET baseline_start = el.sched_start, baseline_end = el.sched_end, updated_date = NOW()
		FROM construction_estimate e
		WHERE e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		  AND el.tenant_id = $1 AND e.project_id = $2
		  AND el.sched_start IS NOT NULL AND el.sched_end IS NOT NULL
		  AND `+workRowFilter+`
	`, tenantID, projectID)
	if err != nil {
		h.log.Error("work schedule: baseline freeze failed", "error", err)
		response.InternalError(c, "Failed to freeze baseline")
		return
	}
	n, _ := res.RowsAffected()
	response.Success(c, map[string]interface{}{"frozen": n})
}

// CreateWorkDependency — POST /construction/projects/:id/dependencies
// FS + lag only (v1). Rejects self-links, duplicates and cycles (BFS over
// the existing edges: adding P→S is a cycle iff S already reaches P).
func (h *Handler) CreateWorkDependency(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}
	var body struct {
		PredecessorLineID int64 `json:"predecessor_line_id"`
		SuccessorLineID   int64 `json:"successor_line_id"`
		LagDays           int   `json:"lag_days"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if body.PredecessorLineID == body.SuccessorLineID {
		response.BadRequest(c, "A work cannot depend on itself")
		return
	}
	if body.LagDays < 0 || body.LagDays > 365 {
		response.BadRequest(c, "lag_days must be between 0 and 365")
		return
	}

	var validCount int
	if err := h.db.QueryRow(`
		SELECT COUNT(*)
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE el.tenant_id = $1 AND e.project_id = $2
		  AND el.id = ANY($3) AND `+workRowFilter+`
	`, tenantID, projectID, pq.Array([]int64{body.PredecessorLineID, body.SuccessorLineID})).Scan(&validCount); err != nil {
		h.log.Error("work dependency: validation failed", "error", err)
		response.InternalError(c, "Failed to validate works")
		return
	}
	if validCount != 2 {
		response.BadRequest(c, "Both lines must be works of this project")
		return
	}

	// Cycle check: BFS from the successor along existing pred→succ edges;
	// reaching the predecessor means the new edge would close a loop.
	adj := map[int64][]int64{}
	dRows, err := h.db.Query(`
		SELECT predecessor_line_id, successor_line_id
		FROM construction_work_dependencies
		WHERE tenant_id = $1 AND project_id = $2
	`, tenantID, projectID)
	if err != nil {
		h.log.Error("work dependency: graph load failed", "error", err)
		response.InternalError(c, "Failed to check dependencies")
		return
	}
	for dRows.Next() {
		var p, s int64
		if err := dRows.Scan(&p, &s); err == nil {
			adj[p] = append(adj[p], s)
		}
	}
	dRows.Close()

	queue := []int64{body.SuccessorLineID}
	seen := map[int64]bool{body.SuccessorLineID: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == body.PredecessorLineID {
			response.BadRequest(c, "Dependency would create a cycle")
			return
		}
		for _, nxt := range adj[cur] {
			if !seen[nxt] {
				seen[nxt] = true
				queue = append(queue, nxt)
			}
		}
	}

	var depID int64
	err = h.db.QueryRow(`
		INSERT INTO construction_work_dependencies
			(tenant_id, project_id, predecessor_line_id, successor_line_id, lag_days, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, tenantID, projectID, body.PredecessorLineID, body.SuccessorLineID, body.LagDays, uuidArg(userID)).Scan(&depID)
	if err != nil {
		if strings.Contains(err.Error(), "uq_work_dependency") {
			response.BadRequest(c, "This dependency already exists")
			return
		}
		h.log.Error("work dependency: insert failed", "error", err)
		response.InternalError(c, "Failed to create dependency")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":                  depID,
		"predecessor_line_id": body.PredecessorLineID,
		"successor_line_id":   body.SuccessorLineID,
		"lag_days":            body.LagDays,
	})
}

// DeleteWorkDependency — DELETE /construction/schedule-dependencies/:id
func (h *Handler) DeleteWorkDependency(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	depID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid dependency ID")
		return
	}
	res, err := h.db.Exec(`
		DELETE FROM construction_work_dependencies WHERE id = $1 AND tenant_id = $2
	`, depID, tenantID)
	if err != nil {
		h.log.Error("work dependency: delete failed", "error", err)
		response.InternalError(c, "Failed to delete dependency")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Dependency not found")
		return
	}
	response.Success(c, map[string]interface{}{"deleted": true})
}
