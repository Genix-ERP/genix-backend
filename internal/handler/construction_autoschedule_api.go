package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// Avtomatik rejalashtirish — HTTP qatlami (TZ §6, §9).
// Oldindan ko'rish HECH NARSA yozmaydi; qo'llash — bitta tranzaksiya +
// schedule_run yozuvi (orqaga qaytarish uchun diff bilan).
// =====================================================

type autoScheduleInput struct {
	StartDate     string  `json:"start_date"` // YYYY-MM-DD
	ParallelLimit int     `json:"parallel_limit"`
	CrewSize      int     `json:"crew_size"`
	HoursPerShift float64 `json:"hours_per_shift"`
	Shifts        int     `json:"shifts"`
	WorkdaysMask  int     `json:"workdays_mask"`
	Scope         string  `json:"scope"`   // unplanned | all | overdue | section
	Section       string  `json:"section"` // scope=section uchun
	SaveParams    bool    `json:"save_params"`
}

// resolveParams — saqlangan loyiha parametrlari ustiga so'rov qiymatlarini qo'yadi.
func (h *Handler) resolveParams(tenantID uuid.UUID, projectID int64, in autoScheduleInput) schedParams {
	p := h.loadSchedParams(tenantID, projectID)
	if in.ParallelLimit > 0 {
		p.ParallelLimit = in.ParallelLimit
	}
	if in.CrewSize > 0 {
		p.CrewSize = in.CrewSize
	}
	if in.HoursPerShift > 0 {
		p.HoursPerShift = in.HoursPerShift
	}
	if in.Shifts > 0 {
		p.Shifts = in.Shifts
	}
	if in.WorkdaysMask > 0 && in.WorkdaysMask <= 127 {
		p.WorkdaysMask = in.WorkdaysMask
	}
	if in.StartDate != "" {
		if d, err := time.Parse(schedDateLayout, in.StartDate); err == nil {
			p.StartDate = &d
		}
	}
	return p
}

func scopeOrDefault(s string) string {
	switch s {
	case "unplanned", "all", "overdue", "section":
		return s
	}
	return "unplanned"
}

// PreviewAutoSchedule — deltalar, konfliktlar va loyiha tugashini qaytaradi.
// HECH NARSA YOZMAYDI (TZ §4, §6.2).
// POST /construction/projects/:id/schedule/auto/preview
func (h *Handler) PreviewAutoSchedule(c *gin.Context) {
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
	var in autoScheduleInput
	_ = c.ShouldBindJSON(&in)

	p := h.resolveParams(tenantID, projectID, in)
	res, err := h.runAutoSchedule(tenantID, projectID, p, scopeOrDefault(in.Scope), in.Section)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	normMissing := 0
	for _, w := range res.Works {
		if w.DurSource == "default" {
			normMissing++
		}
	}
	response.Success(c, gin.H{
		"server_today":   res.ServerToday.Format(schedDateLayout),
		"params":         res.Params,
		"scope":          scopeOrDefault(in.Scope),
		"total_works":    len(res.Works),
		"affected_count": len(res.Deltas),
		"norm_missing":   normMissing,
		"project_end":    fmtDate(res.ProjectEnd),
		"deltas":         res.Deltas,
		"conflicts":      res.Conflicts,
		"auto_dep_count": len(res.AutoDeps),
	})
}

// ApplyAutoSchedule — hisobni qo'llaydi: sanalar, auto-bog'liqliklar va
// schedule_run yozuvi bitta tranzaksiyada (TZ §4, §6.5).
// POST /construction/projects/:id/schedule/auto/apply
func (h *Handler) ApplyAutoSchedule(c *gin.Context) {
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
	var in autoScheduleInput
	_ = c.ShouldBindJSON(&in)

	p := h.resolveParams(tenantID, projectID, in)
	scope := scopeOrDefault(in.Scope)
	res, err := h.runAutoSchedule(tenantID, projectID, p, scope, in.Section)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(res.Deltas) == 0 {
		response.Success(c, gin.H{"message": "O'zgarish yo'q", "affected_count": 0,
			"server_today": res.ServerToday.Format(schedDateLayout)})
		return
	}

	// Loyihaning avvalgi tugashi (diff hisobotida ko'rsatiladi).
	var endBefore *time.Time
	for _, w := range res.Works {
		if w.End != nil && (endBefore == nil || w.End.After(*endBefore)) {
			t := *w.End
			endBefore = &t
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// 1. Auto-bog'liqliklarni qayta quramiz (manual'lar tegilmaydi, TZ §3.3).
	if _, err := tx.Exec(`DELETE FROM construction_work_dependencies
		WHERE tenant_id = $1 AND project_id = $2 AND COALESCE(dep_source,'manual') = 'auto'`,
		tenantID, projectID); err != nil {
		response.InternalError(c, "Failed to reset auto dependencies")
		return
	}
	for _, d := range res.AutoDeps {
		if _, err := tx.Exec(`
			INSERT INTO construction_work_dependencies
				(tenant_id, project_id, predecessor_line_id, successor_line_id, lag_days, dep_type, dep_source, created_by)
			VALUES ($1,$2,$3,$4,$5,'FS','auto',$6)
			ON CONFLICT (predecessor_line_id, successor_line_id) DO NOTHING`,
			tenantID, projectID, d.Pred, d.Succ, d.Lag, userID); err != nil {
			response.InternalError(c, "Failed to write auto dependencies")
			return
		}
	}

	// 2. Sanalar + davomiylik. Daxlsiz qatorlar deltaga tushmagan, shuning uchun
	//    bu yerda faqat avtomat hisoblaganlari yoziladi.
	byID := map[int64]*autoWork{}
	for _, w := range res.Works {
		byID[w.ID] = w
	}
	for _, d := range res.Deltas {
		w := byID[d.LineID]
		if w == nil {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE construction_estimate_line
			SET sched_start = $1, sched_end = $2, duration_days = $3, duration_source = $4,
			    norm_snapshot = $5::jsonb, schedule_source = 'auto', updated_date = NOW()
			WHERE id = $6 AND tenant_id = $7`,
			w.newStart, w.newEnd, w.Duration, w.DurSource, string(w.NormSnap), w.ID, tenantID); err != nil {
			response.InternalError(c, "Failed to write schedule dates")
			return
		}
	}

	// 3. Yurgizish jurnali — orqaga qaytarish uchun diff bilan.
	diffJSON, _ := json.Marshal(res.Deltas)
	paramsJSON, _ := json.Marshal(res.Params)
	var runID int64
	if err := tx.QueryRow(`
		INSERT INTO construction_schedule_run
			(tenant_id, project_id, run_kind, params, scope, affected_count,
			 project_end_before, project_end_after, diff_snapshot, created_by)
		VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9::jsonb,$10) RETURNING id`,
		tenantID, projectID, "auto", string(paramsJSON), scope, len(res.Deltas),
		endBefore, res.ProjectEnd, string(diffJSON), userID).Scan(&runID); err != nil {
		response.InternalError(c, "Failed to log schedule run")
		return
	}

	// 4. Parametrlarni saqlash (vizardda "eslab qol").
	if in.SaveParams {
		if _, err := tx.Exec(`
			INSERT INTO construction_schedule_params
				(project_id, tenant_id, start_date, parallel_limit, crew_size, hours_per_shift, shifts, workdays_mask, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
			ON CONFLICT (project_id) DO UPDATE SET
				start_date = EXCLUDED.start_date, parallel_limit = EXCLUDED.parallel_limit,
				crew_size = EXCLUDED.crew_size, hours_per_shift = EXCLUDED.hours_per_shift,
				shifts = EXCLUDED.shifts, workdays_mask = EXCLUDED.workdays_mask, updated_at = NOW()`,
			projectID, tenantID, p.StartDate, p.ParallelLimit, p.CrewSize, p.HoursPerShift,
			p.Shifts, p.WorkdaysMask); err != nil {
			response.InternalError(c, "Failed to save schedule params")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to apply schedule")
		return
	}

	response.Success(c, gin.H{
		"message":        "Grafik rejalashtirildi",
		"run_id":         runID,
		"affected_count": len(res.Deltas),
		"project_end":    fmtDate(res.ProjectEnd),
		"conflicts":      res.Conflicts,
		"server_today":   res.ServerToday.Format(schedDateLayout),
	})
}

// ListScheduleRuns — yurgizishlar tarixi (TZ §6.5).
// GET /construction/projects/:id/schedule/runs
func (h *Handler) ListScheduleRuns(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}
	rows, err := h.db.Query(`
		SELECT r.id, r.run_kind, r.scope, r.affected_count, r.project_end_before, r.project_end_after,
		       r.created_date, r.undone_at, COALESCE(u.first_name || ' ' || u.last_name, '')
		FROM construction_schedule_run r
		LEFT JOIN users u ON u.id = r.created_by
		WHERE r.tenant_id = $1 AND r.project_id = $2
		ORDER BY r.created_date DESC LIMIT 50`, tenantID, projectID)
	if err != nil {
		response.InternalError(c, "Failed to load runs")
		return
	}
	defer rows.Close()

	out := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var kind, scope, author string
		var affected int
		var endBefore, endAfter, undoneAt nullableTime
		var created time.Time
		if rows.Scan(&id, &kind, &scope, &affected, &endBefore, &endAfter, &created, &undoneAt, &author) != nil {
			continue
		}
		item := map[string]interface{}{
			"id": id, "run_kind": kind, "scope": scope, "affected_count": affected,
			"created_date": created, "author": author, "undone": undoneAt.valid,
		}
		if endBefore.valid {
			item["project_end_before"] = endBefore.time.Format(schedDateLayout)
		}
		if endAfter.valid {
			item["project_end_after"] = endAfter.time.Format(schedDateLayout)
		}
		out = append(out, item)
	}
	response.Success(c, out)
}

// UndoScheduleRun — yurgizishni orqaga qaytarish: diff_snapshot'dan avvalgi
// sanalar va manbalarni tiklaydi (TZ §6.5 — asosiy saqlagich).
// POST /construction/schedule-runs/:runId/undo
func (h *Handler) UndoScheduleRun(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	runID, err := strconv.ParseInt(c.Param("runId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid run ID")
		return
	}

	var diffJSON string
	var undoneAt nullableTime
	if err := h.db.QueryRow(`
		SELECT diff_snapshot::text, undone_at FROM construction_schedule_run
		WHERE id = $1 AND tenant_id = $2`, runID, tenantID).Scan(&diffJSON, &undoneAt); err != nil {
		response.NotFound(c, "Schedule run")
		return
	}
	if undoneAt.valid {
		response.BadRequest(c, "Bu yurgizish allaqachon orqaga qaytarilgan")
		return
	}
	var deltas []schedDelta
	if err := json.Unmarshal([]byte(diffJSON), &deltas); err != nil {
		response.InternalError(c, "Failed to read run diff")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	for _, d := range deltas {
		var startBefore, endBefore interface{}
		if d.StartBefore != "" {
			if t, e := time.Parse(schedDateLayout, d.StartBefore); e == nil {
				startBefore = t
			}
		}
		if d.EndBefore != "" {
			if t, e := time.Parse(schedDateLayout, d.EndBefore); e == nil {
				endBefore = t
			}
		}
		src := d.SourceBefore
		if src == "" {
			src = "none"
		}
		if _, err := tx.Exec(`
			UPDATE construction_estimate_line
			SET sched_start = $1, sched_end = $2, schedule_source = $3, duration_days = NULLIF($4, 0), updated_date = NOW()
			WHERE id = $5 AND tenant_id = $6`,
			startBefore, endBefore, src, d.DurBefore, d.LineID, tenantID); err != nil {
			response.InternalError(c, "Failed to restore dates")
			return
		}
	}
	if _, err := tx.Exec(`UPDATE construction_schedule_run SET undone_at = NOW() WHERE id = $1`, runID); err != nil {
		response.InternalError(c, "Failed to mark run undone")
		return
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to undo run")
		return
	}
	response.Success(c, gin.H{"message": "Yurgizish orqaga qaytarildi", "restored_count": len(deltas)})
}

// GetScheduleParams — vizard uchun joriy parametrlar + server sanasi.
// GET /construction/projects/:id/schedule/params
func (h *Handler) GetScheduleParams(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}
	p := h.loadSchedParams(tenantID, projectID)

	// Rejalashtirilmagan ishlar soni — tugma yonidagi beyj bilan bir xil manba.
	var total, unplanned int
	h.db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE el.sched_start IS NULL OR el.sched_end IS NULL)
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE el.tenant_id = $1 AND e.project_id = $2 AND `+workRowFilter,
		tenantID, projectID).Scan(&total, &unplanned)

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"params": p, "total_works": total, "unplanned": unplanned,
		"server_today": h.serverToday().Format(schedDateLayout),
	}})
}

// UpdateScheduleParams — parametrlarni saqlash.
// PUT /construction/projects/:id/schedule/params
func (h *Handler) UpdateScheduleParams(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}
	var in autoScheduleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	p := h.resolveParams(tenantID, projectID, in)
	if _, err := h.db.Exec(`
		INSERT INTO construction_schedule_params
			(project_id, tenant_id, start_date, parallel_limit, crew_size, hours_per_shift, shifts, workdays_mask, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (project_id) DO UPDATE SET
			start_date = EXCLUDED.start_date, parallel_limit = EXCLUDED.parallel_limit,
			crew_size = EXCLUDED.crew_size, hours_per_shift = EXCLUDED.hours_per_shift,
			shifts = EXCLUDED.shifts, workdays_mask = EXCLUDED.workdays_mask, updated_at = NOW()`,
		projectID, tenantID, p.StartDate, p.ParallelLimit, p.CrewSize, p.HoursPerShift,
		p.Shifts, p.WorkdaysMask); err != nil {
		response.InternalError(c, "Failed to save params")
		return
	}
	response.Success(c, gin.H{"message": "Parametrlar saqlandi", "params": p})
}
