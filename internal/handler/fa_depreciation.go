package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// Fixed Assets v1 — depreciation run engine (§6, §7, §8).
// ============================================================================

func faRound(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}

func faLastDayOf(period string) time.Time {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return time.Now()
	}
	return t.AddDate(0, 1, -1)
}

func (h *Handler) faRounding(tenantID uuid.UUID) int {
	r := 2
	h.db.QueryRow(`SELECT rounding FROM fa_settings WHERE tenant_id=$1`, tenantID).Scan(&r)
	if r <= 0 {
		r = 2
	}
	return r
}

// faAccrualAmount is the straight-line monthly accrual (§6.2). The FINAL
// scheduled period (priorPeriods == useful_life-1) trues-up to the exact
// remainder so accumulated equals the depreciable base after useful_life
// periods — that's what makes the 12,000,000/36 tail land on 0. Earlier periods
// use the rounded monthly, clamped so accumulated never exceeds the base (§8.2).
func faAccrualAmount(cost, salvage, accumulated float64, usefulLifeMonths, priorPeriods, rounding int) float64 {
	if usefulLifeMonths <= 0 {
		return 0
	}
	base := cost - salvage
	remaining := faRound(base-accumulated, rounding)
	if remaining <= 0 {
		return 0
	}
	if priorPeriods >= usefulLifeMonths-1 {
		return remaining // last scheduled month absorbs the rounding tail
	}
	monthly := faRound(base/float64(usefulLifeMonths), rounding)
	if monthly > remaining {
		monthly = remaining
	}
	return monthly
}

// faPeriodOpen returns "" when the period is open, else a message (§8.7).
func (h *Handler) faPeriodOpen(tenantID uuid.UUID, period string) string {
	d := faLastDayOf(period)
	if m := h.checkLockDate(tenantID, d); m != "" {
		return m
	}
	if m := h.checkPeriodLock(tenantID, d); m != "" {
		return m
	}
	return ""
}

// ---------------------------------------------------------------------------
// Create / recompute the draft run (§7). Shared by the HTTP handler and cron.
// ---------------------------------------------------------------------------

type faSkip struct {
	AssetID         string `json:"asset_id"`
	InventoryNumber string `json:"inventory_number"`
	Reason          string `json:"reason"`
}

// createDepreciationRun builds (or replaces) the draft run for a period. Returns
// the run id, or ("", code, msg) on a hard failure.
func (h *Handler) createDepreciationRun(tenantID, userID uuid.UUID, period string) (uuid.UUID, string, string) {
	if msg := h.faPeriodOpen(tenantID, period); msg != "" {
		return uuid.Nil, "PERIOD_CLOSED", msg
	}

	// One LIVE run per period (456: reversed runs stay as audit history and do
	// not block a re-run). Posted -> conflict; draft -> replace.
	var existingID uuid.UUID
	var existingStatus string
	if h.db.QueryRow(`SELECT id, status FROM fa_depreciation_runs
		WHERE tenant_id=$1 AND period=$2 AND status <> 'reversed'
		ORDER BY created_at DESC LIMIT 1`, tenantID, period).
		Scan(&existingID, &existingStatus) == nil {
		if existingStatus == "posted" {
			return uuid.Nil, "RUN_ALREADY_POSTED", "Bu davr uchun reglament allaqachon o'tkazilgan. Avval revers qiling."
		}
		if existingStatus == "draft" {
			if _, execErr := h.db.Exec(`DELETE FROM fa_depreciation_entries WHERE run_id=$1`, existingID); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "DELETE fa_depreciation_entries", "error", execErr)
			}
			if _, execErr := h.db.Exec(`DELETE FROM fa_depreciation_runs WHERE id=$1`, existingID); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "DELETE fa_depreciation_runs", "error", execErr)
			}
		}
	}

	rounding := h.faRounding(tenantID)
	runID := uuid.New()
	var uid interface{}
	if userID != uuid.Nil {
		uid = userID
	}
	if _, err := h.db.Exec(`INSERT INTO fa_depreciation_runs (id, tenant_id, period, status, created_by, created_at) VALUES ($1,$2,$3,'draft',$4,NOW())`,
		runID, tenantID, period, uid); err != nil {
		return uuid.Nil, "SAVE_FAILED", "Reglamentni yaratib bo'lmadi"
	}

	// Candidate assets, joined to their effective accounts.
	rows, err := h.db.Query(`
		SELECT a.id, a.inventory_number, a.cost, a.salvage_value, a.useful_life_months, a.accumulated_depreciation,
		       c.depreciable, c.depreciation_account, d.expense_account,
		       a.depreciation_account_override, a.expense_account_override,
		       to_char(a.commissioning_date, 'YYYY-MM')
		FROM fa_assets a
		JOIN fa_categories  c ON c.id = a.category_id
		JOIN fa_departments d ON d.id = a.department_id
		WHERE a.tenant_id=$1 AND a.deleted_at IS NULL AND a.status='in_service'`, tenantID)
	if err != nil {
		return uuid.Nil, "QUERY_FAILED", "Aktivlarni o'qib bo'lmadi"
	}
	defer rows.Close()

	skipped := []faSkip{}
	for rows.Next() {
		var assetID uuid.UUID
		var inv string
		var cost, salvage, accumulated float64
		var life int
		var depreciable bool
		var catDepr, deptExp, ovDepr, ovExp, commPeriod sql.NullString
		if rows.Scan(&assetID, &inv, &cost, &salvage, &life, &accumulated, &depreciable, &catDepr, &deptExp, &ovDepr, &ovExp, &commPeriod) != nil {
			continue
		}

		// §7 selection filters.
		if !commPeriod.Valid || commPeriod.String >= period {
			continue // not yet in service by the start of this period
		}
		if !depreciable {
			continue // e.g. land
		}

		// Already has an active entry for the period (e.g. disposal top-up)?
		var already bool
		h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM fa_depreciation_entries WHERE tenant_id=$1 AND asset_id=$2 AND period=$3 AND status='active')`,
			tenantID, assetID, period).Scan(&already)
		if already {
			continue
		}

		// §8.6 — no silent fallback: missing config => skip with a reason.
		debitAcct := firstNonEmpty(ovExp, deptExp)
		creditAcct := firstNonEmpty(ovDepr, catDepr)
		if debitAcct == "" || creditAcct == "" {
			skipped = append(skipped, faSkip{assetID.String(), inv, "MAPPING_MISSING"})
			continue
		}

		var priorPeriods int
		h.db.QueryRow(`SELECT COUNT(*) FROM fa_depreciation_entries WHERE asset_id=$1 AND tenant_id=$2 AND status='active'`, assetID, tenantID).Scan(&priorPeriods)

		amount := faAccrualAmount(cost, salvage, accumulated, life, priorPeriods, rounding)
		if amount <= 0 {
			continue // fully depreciated
		}

		if _, err := h.db.Exec(`
			INSERT INTO fa_depreciation_entries (tenant_id, run_id, asset_id, period, amount, debit_account, credit_account, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'active')
			ON CONFLICT (tenant_id, asset_id, period) WHERE status='active' DO NOTHING`,
			tenantID, runID, assetID, period, amount, debitAcct, creditAcct); err != nil {
			skipped = append(skipped, faSkip{assetID.String(), inv, "INSERT_FAILED"})
		}
	}

	sb, _ := json.Marshal(skipped)
	if _, execErr := h.db.Exec(`UPDATE fa_depreciation_runs SET skipped=$1::jsonb WHERE id=$2`, string(sb), runID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE fa_depreciation_runs", "error", execErr)
	}
	return runID, "", ""
}

// CreateDepreciationRunHandler — button + API entry point (§7, §9).
// POST /api/v1/depreciation/runs  { period }
func (h *Handler) CreateDepreciationRunHandler(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		faErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Tenant topilmadi", "Тенант не найден")
		return
	}
	userID, _ := middleware.GetUserID(c)
	var in struct {
		Period string `json:"period"`
	}
	_ = c.ShouldBindJSON(&in)
	period := in.Period
	if _, err := time.Parse("2006-01", period); err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_PERIOD", "Davr formati YYYY-MM bo'lishi kerak", "Формат периода YYYY-MM")
		return
	}

	runID, code, msg := h.createDepreciationRun(tenantID, userID, period)
	if code != "" {
		status := http.StatusBadRequest
		if code == "RUN_ALREADY_POSTED" {
			status = http.StatusConflict
		}
		faErr(c, status, code, msg, msg)
		return
	}

	// Auto-post if configured, else leave the draft for review (§7).
	var autoPost bool
	h.db.QueryRow(`SELECT auto_post FROM fa_settings WHERE tenant_id=$1`, tenantID).Scan(&autoPost)
	if autoPost {
		if pcode, pmsg := h.postDepreciationRun(tenantID, userID, runID); pcode != "" {
			// The draft still exists; report why posting didn't happen.
			faErr(c, http.StatusBadRequest, pcode, pmsg, pmsg)
			return
		}
	}
	h.getRunSummary(c, tenantID, runID)
}

// GetDepreciationRun — lines, totals by account pair, skipped (§7, §9).
// GET /api/v1/depreciation/runs/:id
func (h *Handler) GetDepreciationRun(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	h.getRunSummary(c, tenantID, runID)
}

func (h *Handler) getRunSummary(c *gin.Context, tenantID, runID uuid.UUID) {
	var period, status string
	var skippedJSON string
	if err := h.db.QueryRow(`SELECT period, status, skipped::text FROM fa_depreciation_runs WHERE id=$1 AND tenant_id=$2`,
		runID, tenantID).Scan(&period, &status, &skippedJSON); err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Reglament topilmadi", "Регламент не найден")
		return
	}
	type line struct {
		AssetID   string  `json:"asset_id"`
		Inventory string  `json:"inventory_number"`
		Name      string  `json:"name"`
		Amount    float64 `json:"amount"`
		Debit     string  `json:"debit_account"`
		Credit    string  `json:"credit_account"`
	}
	lines := []line{}
	totals := map[string]float64{}
	var grand float64
	rows, _ := h.db.Query(`
		SELECT e.asset_id, a.inventory_number, a.name, e.amount, e.debit_account, e.credit_account
		FROM fa_depreciation_entries e JOIN fa_assets a ON a.id=e.asset_id
		WHERE e.run_id=$1 AND e.status='active' ORDER BY a.inventory_number`, runID)
	if rows != nil {
		for rows.Next() {
			var l line
			if rows.Scan(&l.AssetID, &l.Inventory, &l.Name, &l.Amount, &l.Debit, &l.Credit) == nil {
				lines = append(lines, l)
				totals[l.Debit+" / "+l.Credit] += l.Amount
				grand += l.Amount
			}
		}
		rows.Close()
	}
	var skipped []faSkip
	json.Unmarshal([]byte(skippedJSON), &skipped)
	faOK(c, gin.H{"id": runID, "period": period, "status": status, "lines": lines,
		"totals_by_pair": totals, "grand_total": grand, "skipped": skipped})
}

// ExcludeFromRun — pull one asset out of a draft into skipped (§7).
// POST /api/v1/depreciation/runs/:id/exclude  { asset_id, comment }
func (h *Handler) ExcludeFromRun(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	var in struct {
		AssetID string `json:"asset_id"`
		Comment string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Comment == "" {
		faErr(c, http.StatusBadRequest, "COMMENT_REQUIRED", "Izoh majburiy", "Комментарий обязателен")
		return
	}
	assetID, err := uuid.Parse(in.AssetID)
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Aktiv ID noto'g'ri", "Некорректный ID актива")
		return
	}
	var status string
	if h.db.QueryRow(`SELECT status FROM fa_depreciation_runs WHERE id=$1 AND tenant_id=$2`, runID, tenantID).Scan(&status) != nil || status != "draft" {
		faErr(c, http.StatusBadRequest, "INVALID_STATUS", "Faqat qoralama reglamentni tahrirlash mumkin", "Редактировать можно только черновик")
		return
	}
	var inv string
	h.db.QueryRow(`SELECT inventory_number FROM fa_assets WHERE id=$1 AND tenant_id=$2`, assetID, tenantID).Scan(&inv)
	if _, execErr := h.db.Exec(`DELETE FROM fa_depreciation_entries WHERE run_id=$1 AND asset_id=$2 AND tenant_id=$3 AND status='active'`, runID, assetID, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "DELETE fa_depreciation_entries", "error", execErr)
	}
	// Append to skipped.
	var skippedJSON string
	h.db.QueryRow(`SELECT skipped::text FROM fa_depreciation_runs WHERE id=$1`, runID).Scan(&skippedJSON)
	var skipped []faSkip
	json.Unmarshal([]byte(skippedJSON), &skipped)
	skipped = append(skipped, faSkip{assetID.String(), inv, "manual_exclude: " + in.Comment})
	sb, _ := json.Marshal(skipped)
	if _, execErr := h.db.Exec(`UPDATE fa_depreciation_runs SET skipped=$1::jsonb WHERE id=$2`, string(sb), runID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE fa_depreciation_runs", "error", execErr)
	}
	faOK(c, gin.H{"message": "Aktiv chiqarildi"})
}

// postDepreciationRun posts a draft run: one grouped JE, links entries, updates
// accumulated in the same tx (§7). Returns ("", "") on success.
func (h *Handler) postDepreciationRun(tenantID, userID uuid.UUID, runID uuid.UUID) (string, string) {
	var period, status string
	if h.db.QueryRow(`SELECT period, status FROM fa_depreciation_runs WHERE id=$1 AND tenant_id=$2`, runID, tenantID).
		Scan(&period, &status) != nil {
		return "NOT_FOUND", "Reglament topilmadi"
	}
	if status != "draft" {
		return "INVALID_STATUS", "Faqat qoralamani o'tkazish mumkin"
	}
	if msg := h.faPeriodOpen(tenantID, period); msg != "" {
		return "PERIOD_CLOSED", msg
	}

	// Group active entries by (debit, credit).
	type grp struct{ debit, credit string }
	sums := map[grp]float64{}
	type ent struct {
		id     uuid.UUID
		asset  uuid.UUID
		amount float64
	}
	var entries []ent
	rows, err := h.db.Query(`SELECT id, asset_id, amount, debit_account, credit_account FROM fa_depreciation_entries WHERE run_id=$1 AND status='active'`, runID)
	if err != nil {
		return "QUERY_FAILED", "Xatolik"
	}
	for rows.Next() {
		var e ent
		var d, cr string
		if rows.Scan(&e.id, &e.asset, &e.amount, &d, &cr) == nil {
			sums[grp{d, cr}] += e.amount
			entries = append(entries, e)
		}
	}
	rows.Close()
	if len(entries) == 0 {
		return "EMPTY_RUN", "O'tkaziladigan yozuv yo'q"
	}

	tx, err := h.db.Begin()
	if err != nil {
		return "TX_FAILED", "Xatolik"
	}
	defer tx.Rollback()

	journalID, nextNum, prefix := faGetJournal(tx, tenantID)
	lines := make([]faJELine, 0, len(sums)*2)
	for g, amt := range sums {
		lines = append(lines,
			faJELine{Code: g.debit, Debit: faRound(amt, 2), Desc: "Amortizatsiya xarajati"},
			faJELine{Code: g.credit, Credit: faRound(amt, 2), Desc: "Yig'ilgan amortizatsiya"})
	}
	jeID, err := h.faPostJournal(tx, tenantID, nil, journalID,
		fmt.Sprintf("%s%06d", prefix, nextNum), "DEP-"+period, "Amortizatsiya reglamenti "+period, "depreciation_run", runID, faLastDayOf(period), lines)
	if err != nil {
		return "POSTING_FAILED", err.Error()
	}
	faBumpJournalNumber(tx, journalID, nextNum+1)

	for _, e := range entries {
		if _, err := tx.Exec(`UPDATE fa_depreciation_entries SET journal_entry_id=$1 WHERE id=$2`, jeID, e.id); err != nil {
			return "SAVE_FAILED", "Xatolik"
		}
		if _, err := tx.Exec(`UPDATE fa_assets SET accumulated_depreciation = accumulated_depreciation + $1, updated_at=NOW() WHERE id=$2`, e.amount, e.asset); err != nil {
			return "SAVE_FAILED", "Xatolik"
		}
	}
	var uid interface{}
	if userID != uuid.Nil {
		uid = userID
	}
	if _, err := tx.Exec(`UPDATE fa_depreciation_runs SET status='posted', posted_by=$1, posted_at=NOW(), journal_entry_id=$2 WHERE id=$3`, uid, jeID, runID); err != nil {
		return "SAVE_FAILED", "Xatolik"
	}
	if err := tx.Commit(); err != nil {
		return "TX_FAILED", "Xatolik"
	}

	var grand float64
	for _, e := range entries {
		grand += e.amount
	}
	h.EmitWorkflowEvent(tenantID, "assets.depreciation_posted", map[string]interface{}{
		"record_id": runID.String(), "period": period, "total_amount": faRound(grand, 2), "asset_count": len(entries),
	})
	// assets.fully_depreciated — fires exactly once: only for assets this run's
	// entry pushed onto the salvage floor (before the entry they were below it).
	frows, err := h.db.Query(`
		SELECT a.id, a.inventory_number, a.name, a.cost - a.salvage_value
		FROM fa_assets a
		JOIN fa_depreciation_entries e ON e.asset_id = a.id AND e.run_id = $1 AND e.status = 'active'
		WHERE a.tenant_id = $2
		  AND a.accumulated_depreciation >= a.cost - a.salvage_value - 0.005
		  AND a.accumulated_depreciation - e.amount < a.cost - a.salvage_value - 0.005`, runID, tenantID)
	if err == nil {
		for frows.Next() {
			var id uuid.UUID
			var inv, name string
			var base float64
			if frows.Scan(&id, &inv, &name, &base) == nil {
				h.EmitWorkflowEvent(tenantID, "assets.fully_depreciated", map[string]interface{}{
					"record_id": id.String(), "inventory_number": inv, "asset_name": name, "depreciable_base": faRound(base, 2),
				})
			}
		}
		frows.Close()
	}
	return "", ""
}

// ListDepreciationRuns returns the tenant's run journal (newest first) plus
// gap detection: past months since the first commissioning that have no
// posted run (§ Amortizatsiya tab; audit "unposted-gap warning").
// GET /api/v1/depreciation/runs
func (h *Handler) ListDepreciationRuns(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		faErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Tenant topilmadi", "Тенант не найден")
		return
	}
	type run struct {
		ID             string     `json:"id"`
		Period         string     `json:"period"`
		Status         string     `json:"status"`
		Total          float64    `json:"total"`
		LineCount      int        `json:"line_count"`
		SkippedCount   int        `json:"skipped_count"`
		CreatedAt      time.Time  `json:"created_at"`
		PostedAt       *time.Time `json:"posted_at"`
		PostedByName   string     `json:"posted_by_name"`
		JournalEntryID *string    `json:"journal_entry_id"`
	}
	out := []run{}
	rows, err := h.db.Query(`
		SELECT r.id, r.period, r.status,
		       COALESCE((SELECT SUM(e.amount) FROM fa_depreciation_entries e WHERE e.run_id = r.id AND e.status='active'), 0),
		       COALESCE((SELECT COUNT(*) FROM fa_depreciation_entries e WHERE e.run_id = r.id AND e.status='active'), 0),
		       COALESCE(jsonb_array_length(r.skipped), 0),
		       r.created_at, r.posted_at, r.journal_entry_id::text,
		       COALESCE(u.first_name || ' ' || u.last_name, '')
		FROM fa_depreciation_runs r
		LEFT JOIN users u ON u.id = r.posted_by
		WHERE r.tenant_id = $1
		ORDER BY r.period DESC`, tenantID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "QUERY_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer rows.Close()
	postedPeriods := map[string]bool{}
	for rows.Next() {
		var r run
		var postedAt sql.NullTime
		var jeID sql.NullString
		if rows.Scan(&r.ID, &r.Period, &r.Status, &r.Total, &r.LineCount, &r.SkippedCount,
			&r.CreatedAt, &postedAt, &jeID, &r.PostedByName) == nil {
			r.Period = strings.TrimSpace(r.Period)
			if postedAt.Valid {
				r.PostedAt = &postedAt.Time
			}
			if jeID.Valid && jeID.String != "" {
				r.JournalEntryID = &jeID.String
			}
			if r.Status == "posted" {
				postedPeriods[r.Period] = true
			}
			out = append(out, r)
		}
	}

	// Gaps: months from (first commissioning month + 1) through last month with
	// no posted run. Capped at 24 entries so an old tenant doesn't get a wall.
	gaps := []string{}
	var firstComm sql.NullString
	h.db.QueryRow(`SELECT to_char(MIN(commissioning_date), 'YYYY-MM') FROM fa_assets
		WHERE tenant_id=$1 AND deleted_at IS NULL AND commissioning_date IS NOT NULL`, tenantID).Scan(&firstComm)
	if firstComm.Valid && firstComm.String != "" {
		if start, e := time.Parse("2006-01", firstComm.String); e == nil {
			p := start.AddDate(0, 1, 0) // depreciation starts the month after commissioning
			lastClosed := time.Now().AddDate(0, -1, 0).Format("2006-01")
			for len(gaps) < 24 {
				period := p.Format("2006-01")
				if period > lastClosed {
					break
				}
				if !postedPeriods[period] {
					gaps = append(gaps, period)
				}
				p = p.AddDate(0, 1, 0)
			}
		}
	}
	faOK(c, gin.H{"runs": out, "unposted_gaps": gaps, "suggested_period": time.Now().AddDate(0, -1, 0).Format("2006-01")})
}

// PostDepreciationRun — POST /api/v1/depreciation/runs/:id/post
func (h *Handler) PostDepreciationRun(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	if code, msg := h.postDepreciationRun(tenantID, userID, runID); code != "" {
		status := http.StatusBadRequest
		faErr(c, status, code, msg, msg)
		return
	}
	h.getRunSummary(c, tenantID, runID)
}

// ReverseDepreciationRun — storno in an open period; entries flagged reversed so
// the period frees up for a re-run (§7, §8.7).
// POST /api/v1/depreciation/runs/:id/reverse
func (h *Handler) ReverseDepreciationRun(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	var period, status string
	var runJE uuid.NullUUID
	if h.db.QueryRow(`SELECT period, status, journal_entry_id FROM fa_depreciation_runs WHERE id=$1 AND tenant_id=$2`, runID, tenantID).Scan(&period, &status, &runJE) != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Reglament topilmadi", "Регламент не найден")
		return
	}
	if status != "posted" {
		faErr(c, http.StatusBadRequest, "INVALID_STATUS", "Faqat o'tkazilgan reglamentni revers qilish mumkin", "Реверсировать можно только проведённый")
		return
	}
	// A run without a JE has nothing in the GL to offset — a storno here would
	// corrupt the 02xx contra-asset balances (audit D-1, migration 475).
	if !runJE.Valid {
		faErr(c, http.StatusBadRequest, "NO_JOURNAL_ENTRY", "Bu reglament GLga o'tkazilmagan — revers qilib bo'lmaydi", "Регламент не проведён в ГК — реверс невозможен")
		return
	}
	if msg := h.faPeriodOpen(tenantID, period); msg != "" {
		faErr(c, http.StatusBadRequest, "PERIOD_CLOSED", msg, msg)
		return
	}

	type grp struct{ debit, credit string }
	sums := map[grp]float64{}
	type ent struct {
		id     uuid.UUID
		asset  uuid.UUID
		amount float64
	}
	var entries []ent
	rows, _ := h.db.Query(`SELECT id, asset_id, amount, debit_account, credit_account FROM fa_depreciation_entries WHERE run_id=$1 AND status='active'`, runID)
	if rows != nil {
		for rows.Next() {
			var e ent
			var d, cr string
			if rows.Scan(&e.id, &e.asset, &e.amount, &d, &cr) == nil {
				sums[grp{d, cr}] += e.amount
				entries = append(entries, e)
			}
		}
		rows.Close()
	}

	tx, err := h.db.Begin()
	if err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer tx.Rollback()
	journalID, nextNum, prefix := faGetJournal(tx, tenantID)
	// Reversing lines: swap debit/credit.
	lines := make([]faJELine, 0, len(sums)*2)
	for g, amt := range sums {
		lines = append(lines,
			faJELine{Code: g.credit, Debit: faRound(amt, 2), Desc: "Reversal: yig'ilgan amortizatsiya"},
			faJELine{Code: g.debit, Credit: faRound(amt, 2), Desc: "Reversal: amortizatsiya xarajati"})
	}
	var revJE uuid.UUID
	if len(lines) > 0 {
		revJE, err = h.faPostJournal(tx, tenantID, nil, journalID,
			fmt.Sprintf("%s%06d", prefix, nextNum), "DEP-"+period+"-REV", "Amortizatsiya reversi "+period, "depreciation_reversal", runID, faLastDayOf(period), lines)
		if err != nil {
			faErr(c, http.StatusBadRequest, "POSTING_FAILED", err.Error(), err.Error())
			return
		}
		faBumpJournalNumber(tx, journalID, nextNum+1)
	}
	for _, e := range entries {
		tx.Exec(`UPDATE fa_depreciation_entries SET status='reversed' WHERE id=$1`, e.id)
		tx.Exec(`UPDATE fa_assets SET accumulated_depreciation = accumulated_depreciation - $1, updated_at=NOW() WHERE id=$2`, e.amount, e.asset)
	}
	tx.Exec(`UPDATE fa_depreciation_runs SET status='reversed', reversed_at=NOW(), reversal_journal_entry_id=$1 WHERE id=$2`, revJE, runID)
	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	faOK(c, gin.H{"id": runID, "status": "reversed"})
}

// RunDepreciationCron fires hourly and, on the 1st of the month, creates the run
// for the month that just ended for every cron-enabled tenant — same code path
// as the button (§7). Auto-posts when the tenant enabled it.
func (h *Handler) RunDepreciationCron(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		time.Sleep(3 * time.Minute)
		h.depreciationCronTick()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.depreciationCronTick()
			}
		}
	}()
}

func (h *Handler) depreciationCronTick() {
	now := time.Now()
	if now.Day() != 1 {
		return
	}
	period := now.AddDate(0, 0, -1).Format("2006-01") // the month that just ended
	rows, err := h.db.Query(`SELECT tenant_id, auto_post FROM fa_settings WHERE cron_enabled = true`)
	if err != nil {
		return
	}
	type t struct {
		id   uuid.UUID
		auto bool
	}
	var tenants []t
	for rows.Next() {
		var x t
		if rows.Scan(&x.id, &x.auto) == nil {
			tenants = append(tenants, x)
		}
	}
	rows.Close()

	for _, tn := range tenants {
		runID, code, _ := h.createDepreciationRun(tn.id, uuid.Nil, period)
		if code != "" {
			continue // already posted, closed period, etc.
		}
		if tn.auto {
			h.postDepreciationRun(tn.id, uuid.Nil, runID)
		}
		h.log.Info("depreciation cron", "tenant", tn.id, "period", period, "auto_post", tn.auto)
	}
}

func firstNonEmpty(a, b sql.NullString) string {
	if a.Valid && a.String != "" {
		return a.String
	}
	if b.Valid {
		return b.String
	}
	return ""
}
