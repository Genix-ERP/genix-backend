package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ─── Reja vs Fakt response cache ─────────────────────────────────────────
//
// The full Reja vs Fakt computation walks the entire project's
// construction_estimate_line tree (parent works + every child resource
// + every sub-stage) and runs correlated SUMs on every parent. On real
// 10+ block projects this reaches the gateway timeout even with the
// indexes from migration 374, especially in the "Hammasi" view that
// can't be building-scoped.
//
// We cache the rendered response body keyed by the query signature so
// repeated views from the same dashboard hit memory instead of the DB.
// TTL is short enough that staleness is invisible to the user (15s ≈
// the time between dashboard renders) but long enough to absorb the
// burst of requests every browser makes when the user navigates here
// (the React tab can fire 3-4 requests in flight while it's deciding
// which filter to settle on).
//
// Invalidation: TTL-only. The data churn comes from YAKUNIY confirms
// and quantity edits, both of which are deliberate user actions where
// a 15s lag before the page reflects the change is acceptable.
//
// Cache key includes every parameter that influences the response:
// tenant, project, building, status, stage filter.
type rejaFaktCacheEntry struct {
	body      []byte
	expiresAt time.Time
}

var rejaFaktCache sync.Map // map[string]*rejaFaktCacheEntry

const rejaFaktCacheTTL = 15 * time.Second

func rejaFaktCacheKey(tenantID uuid.UUID, projectID int64, buildingFilter, statusFilter, stageFilter string, page, limit int) string {
	return fmt.Sprintf("%s|%d|%s|%s|%s|%d|%d",
		tenantID.String(), projectID, buildingFilter, statusFilter, stageFilter, page, limit)
}

// stageNameFromPath extracts the human-facing stage name from a row's
// parent_item_number field. Estimate imports store the section path in the
// SmetaImportModal hierarchy format ("СЕКЦИЯ №1 › ФУНДАМЕНТЫ"); the bit we
// want for the Reja vs Fakt aggregation is the LAST segment, since the
// section prefix ("СЕКЦИЯ №…") is just a regulatory grouping that's the
// same for the whole estimate. Falls back to the bare value when the
// hierarchy delimiter isn't present (legacy / hand-typed rows).
func stageNameFromPath(parentItemNumber string) string {
	const delim = " › "
	s := strings.TrimSpace(parentItemNumber)
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, delim); i >= 0 {
		return strings.TrimSpace(s[i+len(delim):])
	}
	return s
}

// =====================================================
// REJA VS FAKT (Plan vs Fact) HANDLERS
// =====================================================

// GetRejaFakt returns full plan vs fact data for a project — stages, sub-stages, materials, equipment
func (h *Handler) GetRejaFakt(c *gin.Context) {
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

	// Pul-hisobot — sof-prorab rolga 403 (conventions §4).
	if h.denyPriceRestricted(c, tenantID, projectID) {
		return
	}

	// Optional filters
	stageFilter := c.Query("stage_id")
	statusFilter := c.Query("status")
	buildingFilter := c.Query("building_id")

	// Optional, opt-in pagination of the top-level stages (mirrors
	// construction_stages.go). Mobile sends page+limit so the per-block
	// payload stays small; when NEITHER is present we return the whole block
	// exactly as before, so the web client (client-side slicing) is unaffected.
	//
	// The summary stays BLOCK-WIDE: we still load, assemble and total every
	// stage in the block, and only the returned `stages` array is sliced to
	// the page. This guarantees the summary cards keep equalling the sum of
	// all rows (the plan/fact resolution is intricate; recomputing it over a
	// page would drift), at the cost of a block-wide assembly per request —
	// the 15s response cache absorbs repeat reads.
	pageStr := c.Query("page")
	limitStr := c.Query("limit")
	paginate := pageStr != "" || limitStr != ""
	page, limit := 1, 20
	if v, e := strconv.Atoi(pageStr); e == nil && v > 0 {
		page = v
	}
	if v, e := strconv.Atoi(limitStr); e == nil && v > 0 {
		if v > 100 {
			v = 100
		}
		limit = v
	}
	offset := (page - 1) * limit

	// Cache check — return memoised JSON when fresh. Skip when the
	// request carries a `?refresh=1` flag so the user can force-bust
	// the cache after a YAKUNIY confirm or qty edit if they don't
	// want to wait the 15s TTL.
	cacheKey := rejaFaktCacheKey(tenantID, projectID, buildingFilter, statusFilter, stageFilter, page, limit)
	if c.Query("refresh") != "1" {
		if v, ok := rejaFaktCache.Load(cacheKey); ok {
			entry := v.(*rejaFaktCacheEntry)
			if time.Now().Before(entry.expiresAt) {
				c.Data(http.StatusOK, "application/json", entry.body)
				return
			}
			// Expired — drop it so we don't accumulate stale entries
			// for projects that aren't being viewed anymore.
			rejaFaktCache.Delete(cacheKey)
		}
	}

	// 1. Load stages
	type Stage struct {
		ID            int64   `json:"id"`
		Name          string  `json:"name"`
		StageOrder    int     `json:"stage_order"`
		Status        string  `json:"status"`
		PlannedBudget float64 `json:"planned_budget"`
		PlannedStart  *string `json:"planned_start"`
		PlannedEnd    *string `json:"planned_end"`
	}

	stageQuery := `
		SELECT id, name, stage_order, status, planned_budget, planned_start, planned_end
		FROM construction_stages
		WHERE project_id = $1 AND tenant_id = $2
	`
	stageArgs := []interface{}{projectID, tenantID}
	argN := 3

	if stageFilter != "" {
		stageQuery += ` AND id = $` + strconv.Itoa(argN)
		sid, _ := strconv.ParseInt(stageFilter, 10, 64)
		stageArgs = append(stageArgs, sid)
		argN++
	}
	if statusFilter != "" {
		stageQuery += ` AND status = $` + strconv.Itoa(argN)
		stageArgs = append(stageArgs, statusFilter)
		argN++
	}
	if buildingFilter != "" {
		// Scope stages to one building/block. `building_id` was added by
		// migration 333 — nullable, so project-wide stages simply won't match.
		stageQuery += ` AND building_id = $` + strconv.Itoa(argN)
		bid, _ := strconv.ParseInt(buildingFilter, 10, 64)
		stageArgs = append(stageArgs, bid)
		argN++
	}
	// Order sections to match Bosqichlar. StagesTabV2 groups works by
	// parent_item_number directly off the estimate, so a section's
	// "first appearance" in the estimate (lowest sort_order among lines
	// that reference it) defines its position. We mirror that here so
	// users see ЗЕМЛЯННЫЕ РАБОТЫ → ФУНДАМЕНТЫ → … in the same order on
	// both tabs, regardless of when the construction_stages rows were
	// created. Falls back to stage_order/id when no estimate line
	// references the section (manual stages, legacy data).
	stageQuery += `
		ORDER BY
		    (SELECT MIN(el.sort_order)
		       FROM construction_estimate_line el
		       JOIN construction_estimate e ON e.id = el.estimate_id
		       WHERE el.tenant_id = construction_stages.tenant_id
		         AND e.project_id = construction_stages.project_id
		         AND COALESCE(el.parent_item_number, '') = construction_stages.name
		    ) ASC NULLS LAST,
		    stage_order ASC,
		    id ASC`

	stageRows, err := h.db.Query(stageQuery, stageArgs...)
	if err != nil {
		h.log.Error("Failed to load stages for reja-fakt", "error", err)
		response.InternalError(c, "Failed to load data")
		return
	}
	defer stageRows.Close()

	var stageIDs []int64
	stagesMap := map[int64]Stage{}
	var stagesOrder []int64
	for stageRows.Next() {
		var s Stage
		if err := stageRows.Scan(&s.ID, &s.Name, &s.StageOrder, &s.Status, &s.PlannedBudget, &s.PlannedStart, &s.PlannedEnd); err != nil {
			continue
		}
		stageIDs = append(stageIDs, s.ID)
		stagesMap[s.ID] = s
		stagesOrder = append(stagesOrder, s.ID)
	}

	if len(stageIDs) == 0 {
		emptyPayload := map[string]interface{}{
			"stages": []interface{}{},
			"summary": map[string]interface{}{
				"material_plan_total": 0, "material_fact_total": 0,
				"equipment_plan_total": 0, "equipment_fact_total": 0,
				"plan_total": 0, "fact_total": 0, "difference": 0,
				"completion_pct": 0,
			},
		}
		if paginate {
			emptyPayload["meta"] = map[string]interface{}{
				"page": page, "limit": limit, "total": 0,
				"total_pages": 0, "has_next": false, "has_prev": page > 1,
			}
		}
		// Cache empty results too — projects with no stages render
		// repeatedly while the user explores filters and shouldn't
		// re-query the DB on every render.
		cacheRejaFaktResponse(cacheKey, emptyPayload)
		response.Success(c, emptyPayload)
		return
	}

	// 2. Load sub-stages for these stages
	type SubStage struct {
		ID       int64  `json:"id"`
		StageID  int64  `json:"stage_id"`
		Name     string `json:"name"`
		SubOrder int    `json:"sub_order"`
		Status   string `json:"status"`
		// Estimate-line derived fields. Used as plan/fact when the
		// project does not maintain construction_sub_stage_materials /
		// _equipment rows (the v2 estimate-driven workflow). Bosqichlar
		// computes plan/fact the same way, so the two tabs agree.
		ELQuantity     sql.NullFloat64
		ELDoneQuantity sql.NullFloat64
		ELUnitRate     sql.NullFloat64
		ELTotalAmount  sql.NullFloat64
		ELSubDerived   sql.NullFloat64
	}

	// Order matches the Bosqichlar (StagesTabV2) tab. Bosqichlar reads
	// directly from construction_estimate_line and renders works in
	// estimate `sort_order` (the same order the user imported them in).
	// construction_sub_stages.name mirrors estimate_line.name and the
	// parent stage.name mirrors estimate_line.parent_item_number, so we
	// can resolve each sub-stage's "natural" order via that name pair
	// against any of the project's edinich estimate lines. Falling back
	// to ss.sub_order keeps the row visible if no matching estimate
	// line exists (e.g. user-renamed sub-stages, manually-added rows).
	//
	// We also pull plan/fact-shaped fields off the matched estimate
	// line so the assembly step downstream can fall back to estimate-
	// derived totals when sub_stage_materials/_equipment are empty.
	subRows, err := h.db.Query(`
		SELECT ss.id, ss.stage_id, ss.name, ss.sub_order, ss.status,
		       el.quantity, el.done_quantity, el.unit_rate, el.total_amount, el.sub_derived
		FROM construction_sub_stages ss
		JOIN construction_stages s ON s.id = ss.stage_id AND s.tenant_id = ss.tenant_id
		LEFT JOIN LATERAL (
			SELECT
			    el.sort_order,
			    el.item_number,
			    -- original_quantity / original_unit_rate are the anchor values
			    -- written on first INSERT and never updated; they survive the
			    -- v2 done_quantity↔quantity sync that otherwise overwrites
			    -- el.quantity, making plan==fact.
			    COALESCE(el.original_quantity,  el.quantity, 0)  AS quantity,
			    el.done_quantity,
			    COALESCE(el.original_unit_rate, el.unit_rate, 0) AS unit_rate,
			    el.total_amount,
			    -- Materialised column (migration 375) — was a per-row
			    -- correlated SUM that quadratically blew up runtime.
			    COALESCE(el.sub_derived, 0) AS sub_derived
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE el.tenant_id = ss.tenant_id
			  AND e.project_id = s.project_id
			  AND COALESCE(el.parent_item_number, '') = s.name
			  AND el.name = ss.name
			ORDER BY el.sort_order ASC, el.id ASC
			LIMIT 1
		) el ON TRUE
		WHERE ss.tenant_id = $1 AND ss.stage_id = ANY($2)
		ORDER BY el.sort_order ASC NULLS LAST,
		         el.item_number ASC NULLS LAST,
		         ss.sub_order ASC,
		         ss.id ASC
	`, tenantID, pq.Array(stageIDs))
	if err != nil {
		h.log.Error("Failed to load sub-stages", "error", err)
		response.InternalError(c, "Failed to load data")
		return
	}
	defer subRows.Close()

	var subStageIDs []int64
	subStagesMap := map[int64][]SubStage{} // stageID -> []SubStage
	for subRows.Next() {
		var ss SubStage
		if err := subRows.Scan(
			&ss.ID, &ss.StageID, &ss.Name, &ss.SubOrder, &ss.Status,
			&ss.ELQuantity, &ss.ELDoneQuantity, &ss.ELUnitRate, &ss.ELTotalAmount, &ss.ELSubDerived,
		); err != nil {
			continue
		}
		subStageIDs = append(subStageIDs, ss.ID)
		subStagesMap[ss.StageID] = append(subStagesMap[ss.StageID], ss)
	}

	// 3. Load materials for all sub-stages
	type MaterialRow struct {
		ID           int64   `json:"id"`
		SubStageID   int64   `json:"sub_stage_id"`
		ProductID    *string `json:"product_id"`
		ProductName  string  `json:"product_name"`
		UOM          string  `json:"uom"`
		PlanQuantity float64 `json:"plan_quantity"`
		FactQuantity float64 `json:"fact_quantity"`
		UnitCost     float64 `json:"unit_cost"`
		PlanAmount   float64 `json:"plan_amount"`
		FactAmount   float64 `json:"fact_amount"`
		Difference   float64 `json:"difference"`
		Note         *string `json:"note"`
	}

	materialsMap := map[int64][]MaterialRow{} // subStageID -> materials
	if len(subStageIDs) > 0 {
		matRows, err := h.db.Query(`
			SELECT id, sub_stage_id, product_id, product_name, uom,
			       COALESCE(plan_quantity, quantity) as plan_quantity,
			       fact_quantity, unit_cost, note
			FROM construction_sub_stage_materials
			WHERE tenant_id = $1 AND sub_stage_id = ANY($2)
			ORDER BY id ASC
		`, tenantID, pq.Array(subStageIDs))
		if err == nil {
			defer matRows.Close()
			for matRows.Next() {
				var m MaterialRow
				if err := matRows.Scan(&m.ID, &m.SubStageID, &m.ProductID, &m.ProductName, &m.UOM,
					&m.PlanQuantity, &m.FactQuantity, &m.UnitCost, &m.Note); err != nil {
					continue
				}
				m.PlanAmount = m.PlanQuantity * m.UnitCost
				m.FactAmount = m.FactQuantity * m.UnitCost
				m.Difference = m.PlanAmount - m.FactAmount
				materialsMap[m.SubStageID] = append(materialsMap[m.SubStageID], m)
			}
		}
	}

	// 4. Load equipment for all sub-stages
	type EquipmentRow struct {
		ID           int64   `json:"id"`
		SubStageID   int64   `json:"sub_stage_id"`
		Name         string  `json:"name"`
		WorkUnit     string  `json:"work_unit"`
		PlanQuantity float64 `json:"plan_quantity"`
		FactQuantity float64 `json:"fact_quantity"`
		UnitPrice    float64 `json:"unit_price"`
		PlanAmount   float64 `json:"plan_amount"`
		FactAmount   float64 `json:"fact_amount"`
		Difference   float64 `json:"difference"`
		Note         *string `json:"note"`
	}

	equipmentMap := map[int64][]EquipmentRow{} // subStageID -> equipment
	if len(subStageIDs) > 0 {
		eqRows, err := h.db.Query(`
			SELECT id, sub_stage_id, name, work_unit, plan_quantity, fact_quantity, unit_price, COALESCE(note, '')
			FROM construction_sub_stage_equipment
			WHERE tenant_id = $1 AND sub_stage_id = ANY($2)
			ORDER BY id ASC
		`, tenantID, pq.Array(subStageIDs))
		if err == nil {
			defer eqRows.Close()
			for eqRows.Next() {
				var e EquipmentRow
				var note string
				if err := eqRows.Scan(&e.ID, &e.SubStageID, &e.Name, &e.WorkUnit,
					&e.PlanQuantity, &e.FactQuantity, &e.UnitPrice, &note); err != nil {
					continue
				}
				if note != "" {
					e.Note = &note
				}
				e.PlanAmount = e.PlanQuantity * e.UnitPrice
				e.FactAmount = e.FactQuantity * e.UnitPrice
				e.Difference = e.PlanAmount - e.FactAmount
				equipmentMap[e.SubStageID] = append(equipmentMap[e.SubStageID], e)
			}
		}
	}

	// 4b. Collect raw work + sub-resource data from construction_estimate_line.
	//
	// Background: foremen do their day-to-day work (entering done quantity,
	// completing stages) on the Bosqichlar tab, which writes to
	// construction_estimate_line. The legacy construction_sub_stage_*
	// tables this handler was originally built around are mostly empty
	// for newer projects, so without this side-channel Reja vs Fakt would
	// keep reporting 0/0/0 even after a stage is fully YAKUNIY in
	// Bosqichlar.
	//
	// Match logic: each top-level work line carries a parent_item_number
	// like "СЕКЦИЯ №1 › ФУНДАМЕНТЫ". stageNameFromPath() pulls the last
	// segment ("ФУНДАМЕНТЫ") which matches construction_stages.name set
	// by SmetaImportModal during import.
	//
	// We collect raw rows here and project them into virtual SubStageResult
	// rows below (after the SubStageResult/MaterialRow/EquipmentRow types
	// are declared in step 5). Each top-level work becomes one virtual
	// sub-stage with id = -line.id so it never collides with a real
	// sub-stage id; its priced labour / machine / material sub-rows fan
	// out into the Equipment and Materials lists the frontend already
	// renders.
	type workMeta struct {
		id         int64
		sortOrder  int
		itemNumber string
		name       string
		uom        string
		section    string
		parentQty  float64
		parentDone float64
		plan       float64
		fact       float64
		status     string
	}
	type subResRow struct {
		parentID     int64
		name         string
		uom          string
		resourceType string
		normRate     float64
		ownQty       float64
		unitRate     float64
	}
	workByID := map[int64]workMeta{}
	subsByWork := map[int64][]subResRow{}
	{
		estimateScopeQ := `
			SELECT id FROM construction_estimate
			WHERE project_id = $1 AND tenant_id = $2
		`
		estimateScopeArgs := []interface{}{projectID, tenantID}
		if buildingFilter != "" {
			bid, _ := strconv.ParseInt(buildingFilter, 10, 64)
			estimateScopeQ += ` AND building_id = $3`
			estimateScopeArgs = append(estimateScopeArgs, bid)
		}

		// Pull every top-level work line in scope plus the data we need
		// to price them. The sub_derived_rate subquery sums
		// Σ(sub.unit_rate × sub.norm_rate) over the line's resource
		// sub-rows so works whose own unit_rate is zero (common for
		// ВОР-imported parents) still contribute — same fallback the
		// Bosqichlar tab uses on the frontend.
		// quantity vs original_quantity (migration 349):
		//   `quantity` gets mirrored from done_quantity by the v2 sync
		//   (Smeta ↔ Bosqichlar mirror task), so reading it here gives
		//   plan == fact for every work the foreman has touched.
		//   `original_quantity` is the value at first INSERT — never
		//   updated post-insert, so it survives every UI-driven sync.
		//   That's the "Reja" Bosqichlar shows in the REJA column.
		// We coalesce to quantity for legacy rows that pre-date 349 and
		// somehow still carry NULL anchors (the trigger handles new
		// inserts but a manual SQL backfill could have missed older
		// data).
		// quantity / done_quantity / unit_rate frequently live in
		// DIFFERENT estimates for v2 projects:
		//   • ВОР carries the planned project quantity (rate=0)
		//   • Единич carries the per-unit price (quantity=0 — template)
		// Picking any single row gives 0 for one of the three factors,
		// so the synthetic projection collapses to 0/0 even though
		// Bosqichlar shows real numbers (Bosqichlar resolves the
		// pieces manually). We pre-aggregate the best non-zero value
		// per (work_name, section_path) across the WHOLE project in a
		// single CTE pass — no correlated subqueries, so this scales
		// linearly with line count instead of N×4 like the previous
		// implementation that timed the page out.
		//
		// The aggregated row is project-wide (not building-scoped) so
		// cross-block fallbacks still work: a Block 2 Единич with no
		// rate can pick up the rate from the same work in any block
		// that does have it. The dedup-within-synthetic-loop further
		// up ensures only one row per (name, section) renders, so the
		// merged MAX values are what actually drives plan/fact.
		// Section path normalization: imports sometimes carry the full
		// hierarchy ("СЕКЦИЯ №1 › ФУНДАМЕНТЫ") and sometimes just the
		// leaf ("ФУНДАМЕНТЫ"). The user's project 1 has both shapes
		// across different Единич imports for the same work, which
		// would split the GROUP BY into separate buckets and prevent
		// the merge. We extract just the leaf — everything after the
		// last "›" — which matches what the frontend renders as the
		// section heading. Both shapes collapse into one bucket so a
		// work can pull its qty from one estimate and its sub-derived
		// price from another.
		const sectionLeafExpr = `regexp_replace(COALESCE(l.parent_item_number, ''), '^.*›\s*', '')`
		// per_line carries enough context to decide which row a value
		// "really" came from. The Smeta↔Bosqichlar sync (task #85)
		// overwrites `quantity` with `done_quantity` when the foreman
		// types Bajarildi, which destroys the planned value — so we
		// can't just MAX the columns. Instead we tag each row with its
		// estimate's source_type so the aggregation can prefer ВОР's
		// quantity (sticky, never synced) over Единич's quantity (which
		// might already be corrupted to match done_quantity).
		// per_line scope: when a building is filtered, restrict the CTE
		// to that building's estimates. Without this restriction the
		// CTE scans every parent line across every block in the
		// project, runs a correlated SUM per row, and the production
		// gateway times out at 60s on real-world projects (10+ block
		// projects with 3000+ lines per block = 30K+ correlated
		// subquery executions). The trade-off is that the cross-block
		// rate-fallback (a Block 2 line picking up a unit_rate from the
		// same work in Block 3) only fires when the user views the
		// "Hammasi" tab; that's an acceptable price for a 10x speedup
		// on per-block views, which is what users open 99% of the time.
		perLineBuildingFilter := ""
		if buildingFilter != "" {
			perLineBuildingFilter = " AND e.building_id = $3"
		}
		linesQ := `
			WITH per_line AS (
			    SELECT
			        l.id,
			        l.tenant_id,
			        l.estimate_id,
			        l.name,
			        ` + sectionLeafExpr + ` AS section_leaf,
			        LOWER(COALESCE(e.source_type, ''))  AS source_type,
			        COALESCE(l.original_quantity, 0)    AS row_orig_qty,
			        COALESCE(l.quantity, 0)             AS row_qty,
			        COALESCE(l.done_quantity, 0)        AS row_done,
			        COALESCE(l.original_unit_rate, 0)   AS row_orig_rate,
			        COALESCE(l.unit_rate, 0)            AS row_rate,
			        -- Materialised sub_derived column (migration 375)
			        -- replaces a correlated subquery that was the
			        -- single biggest perf hotspot — running it for
			        -- every parent on every render meant 10K+ subquery
			        -- executions per page on real projects. The column
			        -- is kept fresh by trigger.
			        COALESCE(l.sub_derived, 0)          AS row_sub_derived
			    FROM construction_estimate_line l
			    JOIN construction_estimate e
			      ON e.id = l.estimate_id AND e.tenant_id = l.tenant_id
			    WHERE e.project_id = $1
			      AND l.tenant_id = $2
			      AND COALESCE(l.resource_type, '') = ''
			      AND COALESCE(l.parent_line_id, 0) = 0
			      ` + perLineBuildingFilter + `
			),
			-- Plan / fact / rate are sourced INDEPENDENTLY across all
			-- rows in (name, section_leaf):
			--   plan_qty  → ВОР's quantity (canonical) →
			--               row_orig_qty (anchor) →
			--               row_qty from rows where done == 0 (not yet synced)
			--   done      → MAX(done_quantity) — straightforward, foreman's input
			--   rate      → MAX(unit_rate or original_unit_rate) →
			--               MAX(sub_derived_rate) when own rate is 0
			-- COALESCE-of-FILTER is much cheaper than correlated
			-- subqueries — single GROUP BY pass handles everything.
			agg AS (
			    SELECT
			        name AS work_name,
			        section_leaf,
			        COALESCE(
			            MAX(row_qty)      FILTER (WHERE source_type = 'vor' AND row_qty > 0),
			            MAX(row_orig_qty) FILTER (WHERE row_orig_qty > 0),
			            MAX(row_qty)      FILTER (WHERE row_done = 0 AND row_qty > 0),
			            0
			        ) AS plan_qty,
			        COALESCE(MAX(row_done) FILTER (WHERE row_done > 0), 0) AS done_max,
			        COALESCE(
			            MAX(GREATEST(row_orig_rate, row_rate))
			                FILTER (WHERE row_orig_rate > 0 OR row_rate > 0),
			            MAX(row_sub_derived) FILTER (WHERE row_sub_derived > 0),
			            0
			        ) AS rate_max,
			        COALESCE(MAX(row_sub_derived) FILTER (WHERE row_sub_derived > 0), 0) AS sub_derived_max
			    FROM per_line
			    GROUP BY name, section_leaf
			)
			SELECT
				l.id,
				COALESCE(l.sort_order, 0)          AS sort_order,
				COALESCE(l.item_number, '')        AS item_number,
				COALESCE(l.name, '')               AS name,
				COALESCE(l.uom, '')                AS uom,
				COALESCE(l.parent_item_number, '') AS parent_item_number,
				COALESCE(l.total_amount, 0)        AS total_amount,
				COALESCE(agg.plan_qty, 0)          AS quantity,
				COALESCE(agg.done_max, 0)          AS done_quantity,
				COALESCE(agg.rate_max, 0)          AS unit_rate,
				COALESCE(l.approval_status, '')    AS approval_status,
				COALESCE(agg.sub_derived_max, 0)   AS sub_derived_rate
			FROM construction_estimate_line l
			LEFT JOIN agg
			  ON agg.work_name    = l.name
			 AND agg.section_leaf = ` + sectionLeafExpr + `
			WHERE l.tenant_id = $2
			  AND l.estimate_id IN (` + estimateScopeQ + `)
			  AND COALESCE(l.resource_type, '') = ''
			  AND COALESCE(l.parent_line_id, 0) = 0
		`
		// Arg order: $1=projectID, $2=tenantID, optional $3=buildingID.
		lineRows, lineErr := h.db.Query(linesQ, estimateScopeArgs...)
		if lineErr == nil {
			func() {
				defer lineRows.Close()
				for lineRows.Next() {
					var (
						lineID                                                        int64
						lineSortOrder                                                 int
						lineItemNumber, lineName, lineUOM, parentItem, approvalStatus string
						totalAmt, qty, doneQty, unitRate, subDerived                  float64
					)
					if err := lineRows.Scan(&lineID, &lineSortOrder, &lineItemNumber, &lineName, &lineUOM, &parentItem,
						&totalAmt, &qty, &doneQty, &unitRate, &approvalStatus, &subDerived); err != nil {
						continue
					}
					name := stageNameFromPath(parentItem)
					if name == "" {
						continue
					}
					// Effective per-unit rate. Prefer stored unit_rate; if
					// it's zero but total_amount is set we can back-compute
					// total_amount / qty; finally fall back to the
					// sub-resource derived rate. The last fallback is what
					// makes the texnadzor-bug test case work — parent rows
					// with no own price but priced sub-resources.
					rate := unitRate
					if rate <= 0 && qty > 0 && totalAmt > 0 {
						rate = totalAmt / qty
					}
					if rate <= 0 {
						rate = subDerived
					}
					// Plan is always rate × qty to match Bosqichlar's REJA
					// JAMI column exactly. We used to fall back to a
					// stored total_amount, but in practice that column
					// drifts (gets re-saved as rate × done after a YAKUNIY
					// confirm in some legacy paths), which made Reja vs
					// Fakt show equal plan/fact even when Bosqichlar showed
					// them apart. Recomputing here is cheap and bulletproof.
					plan := rate * qty
					// Use the raw done quantity — over-completion (done >
					// qty) MUST surface in the fact column. Capping made
					// the parent FAKT JAMI silently equal REJA JAMI when
					// the foreman over-recorded, hiding the discrepancy
					// (user-reported "REJA va BAJARILDI har xil bo'lsa
					// ham jami bir xil" bug).
					done := doneQty
					fact := rate * done

					workByID[lineID] = workMeta{
						id:         lineID,
						sortOrder:  lineSortOrder,
						itemNumber: lineItemNumber,
						name:       lineName,
						uom:        lineUOM,
						section:    name,
						parentQty:  qty,
						parentDone: done,
						plan:       plan,
						fact:       fact,
						status:     approvalStatus,
					}
				}
			}()
		} else {
			h.log.Error("Failed to aggregate estimate lines for reja-fakt", "error", lineErr)
		}

		// Pull every priced sub-resource of the works we just collected.
		if len(workByID) > 0 {
			parentIDs := make([]int64, 0, len(workByID))
			for id := range workByID {
				parentIDs = append(parentIDs, id)
			}
			subQ := `
				SELECT
					parent_line_id,
					COALESCE(name, ''),
					COALESCE(uom, ''),
					COALESCE(resource_type, ''),
					COALESCE(norm_rate, 0),
					COALESCE(quantity, 0),
					COALESCE(unit_rate, 0)
				FROM construction_estimate_line
				WHERE tenant_id = $1
				  AND parent_line_id = ANY($2)
				  AND COALESCE(resource_type, '') <> ''
			`
			subRows, subErr := h.db.Query(subQ, tenantID, pq.Array(parentIDs))
			if subErr == nil {
				func() {
					defer subRows.Close()
					for subRows.Next() {
						var s subResRow
						if err := subRows.Scan(
							&s.parentID, &s.name, &s.uom, &s.resourceType,
							&s.normRate, &s.ownQty, &s.unitRate,
						); err != nil {
							continue
						}
						subsByWork[s.parentID] = append(subsByWork[s.parentID], s)
					}
				}()
			}
		}
	}

	// 5. Build response
	type SubStageResult struct {
		SubStage
		Materials         []MaterialRow  `json:"materials"`
		Equipment         []EquipmentRow `json:"equipment"`
		MaterialPlanTotal float64        `json:"material_plan_total"`
		MaterialFactTotal float64        `json:"material_fact_total"`
		EquipPlanTotal    float64        `json:"equip_plan_total"`
		EquipFactTotal    float64        `json:"equip_fact_total"`
		PlanTotal         float64        `json:"plan_total"`
		FactTotal         float64        `json:"fact_total"`
		Difference        float64        `json:"difference"`
	}

	type StageResult struct {
		Stage
		SubStages         []SubStageResult `json:"sub_stages"`
		MaterialPlanTotal float64          `json:"material_plan_total"`
		MaterialFactTotal float64          `json:"material_fact_total"`
		EquipPlanTotal    float64          `json:"equip_plan_total"`
		EquipFactTotal    float64          `json:"equip_fact_total"`
		PlanTotal         float64          `json:"plan_total"`
		FactTotal         float64          `json:"fact_total"`
		Difference        float64          `json:"difference"`
	}

	var totalMaterialPlan, totalMaterialFact float64
	var totalEquipPlan, totalEquipFact float64

	stages := []StageResult{}
	for _, sid := range stagesOrder {
		stg := stagesMap[sid]
		sr := StageResult{Stage: stg}

		subs := subStagesMap[sid]
		if subs == nil {
			subs = []SubStage{}
		}

		sr.SubStages = []SubStageResult{}

		// Estimate-line work projection FIRST. Bosqichlar is the source
		// of truth for v2 projects — works come from
		// construction_estimate_line sorted by sort_order/id (the same
		// order foremen see when they confirm работы). We render those
		// works in this exact order, then below add any legacy real
		// construction_sub_stages rows whose names don't match (so
		// stale, manually-renamed, or ad-hoc sub-stages don't disappear).
		//
		// Materials = sub-rows with resource_type='material'.
		// Equipment = labour + machine sub-rows. The page labels this
		// table "Equipment" — semantically a bit loose but the existing
		// UI already groups labour with equipment, and splitting them
		// would need a third panel that doesn't exist on the frontend yet.
		// Match works to this stage by either the stage's full name OR
		// its leaf segment. Stages can be stored either way:
		//   • Full path: "СЕКЦИЯ №5 › ЗЕМЛЯННЫЕ РАБОТЫ" (auto-create
		//     from finaliseMaterialsForWork writes the full
		//     parent_item_number).
		//   • Leaf only: "ЗЕМЛЯННЫЕ РАБОТЫ" (legacy single-block
		//     projects, hand-created stages).
		// workMeta.section is ALWAYS the leaf (set via stageNameFromPath
		// when the row was scanned). Without the dual comparison below,
		// projects whose stages carry the full path got an empty
		// stageWorkIDs for every stage — the response then had every
		// stage's sub_stages = [] and totals = 0. Exactly the bug user
		// reported as "every section, every column showing 0".
		stageLeaf := stageNameFromPath(sr.Name)
		stageWorkIDs := make([]int64, 0, len(workByID))
		for id, w := range workByID {
			if w.section == sr.Name || w.section == stageLeaf {
				stageWorkIDs = append(stageWorkIDs, id)
			}
		}
		// Sort by the estimate line's sort_order (Bosqichlar's display
		// order) → item_number (covers ties from the same imported
		// section) → id (final stable tiebreaker for legacy rows
		// without sort_order/item_number).
		sort.Slice(stageWorkIDs, func(i, j int) bool {
			a := workByID[stageWorkIDs[i]]
			b := workByID[stageWorkIDs[j]]
			if a.sortOrder != b.sortOrder {
				return a.sortOrder < b.sortOrder
			}
			if a.itemNumber != b.itemNumber {
				return a.itemNumber < b.itemNumber
			}
			return stageWorkIDs[i] < stageWorkIDs[j]
		})

		// Track work names projected from estimate lines so we can drop
		// matching real sub-stages below. Without this the UI shows two
		// rows for the same work — synthetic with the right numbers,
		// real with 0/0 because the legacy materials/equipment tables
		// are empty for v2 projects.
		projectedNames := make(map[string]bool, len(stageWorkIDs))
		// Synthetic-row id counter — gives every projected MaterialRow /
		// EquipmentRow a unique negative id so React's `key={mat.id}` /
		// `key={eq.id}` lookups don't collide with each other or with
		// real rows from the legacy construction_sub_stage_* tables.
		var synthRowID int64 = -1
		for _, wid := range stageWorkIDs {
			w := workByID[wid]
			// Skip if we've already projected this work name in this
			// section. The project may carry multiple Единич estimates
			// (re-imports never delete the old ones) which produces
			// duplicate workByID entries with identical (section, name)
			// pairs — without this guard each duplicate renders as a
			// separate row in Reja vs Fakt. Sort order above ensures
			// the surviving row is the one with the lowest sort_order /
			// item_number / id, so the user sees the canonical entry.
			nameKey := strings.ToLower(strings.TrimSpace(w.name))
			if projectedNames[nameKey] {
				continue
			}
			// Normalize the work's approval_status (pending /
			// in_progress / submitted / confirmed_supervisor /
			// confirmed_engineer) into the simpler not_started /
			// in_progress / completed scheme the section pill uses.
			// Without this the row would render an empty / unknown
			// pill for "submitted" or "confirmed_supervisor", since
			// the frontend's STATUS_COLORS map only has the simple keys.
			workDisplayStatus := "not_started"
			switch w.status {
			case "confirmed_engineer":
				workDisplayStatus = "completed"
			case "in_progress", "submitted", "confirmed_supervisor":
				workDisplayStatus = "in_progress"
			default:
				if w.parentDone > 0 {
					workDisplayStatus = "in_progress"
				}
			}
			vsub := SubStageResult{
				SubStage: SubStage{
					ID:       -w.id, // negative = synthetic, won't collide with real sub_stage ids
					StageID:  sr.ID,
					Name:     w.name,
					SubOrder: 0,
					Status:   workDisplayStatus,
				},
				Materials: []MaterialRow{},
				Equipment: []EquipmentRow{},
			}
			for _, s := range subsByWork[w.id] {
				// Plan qty for the resource = parent.qty × norm (or its
				// own stored quantity if norm is missing). Fact qty
				// scales by parent.done so it matches the expandable
				// resource table in Bosqichlar.
				planQty := s.normRate * w.parentQty
				if planQty <= 0 {
					planQty = s.ownQty
				}
				factQty := 0.0
				if s.normRate > 0 {
					factQty = s.normRate * w.parentDone
				} else if w.parentQty > 0 {
					factQty = s.ownQty * (w.parentDone / w.parentQty)
				}
				planAmt := planQty * s.unitRate
				factAmt := factQty * s.unitRate
				rt := strings.ToLower(strings.TrimSpace(s.resourceType))
				if rt == "material" || rt == "materialy" || rt == "mat" {
					vsub.Materials = append(vsub.Materials, MaterialRow{
						ID:           synthRowID,
						SubStageID:   -w.id,
						ProductName:  s.name,
						UOM:          s.uom,
						PlanQuantity: planQty,
						FactQuantity: factQty,
						UnitCost:     s.unitRate,
						PlanAmount:   planAmt,
						FactAmount:   factAmt,
						Difference:   planAmt - factAmt,
					})
					vsub.MaterialPlanTotal += planAmt
					vsub.MaterialFactTotal += factAmt
				} else {
					vsub.Equipment = append(vsub.Equipment, EquipmentRow{
						ID:           synthRowID,
						SubStageID:   -w.id,
						Name:         s.name,
						WorkUnit:     s.uom,
						PlanQuantity: planQty,
						FactQuantity: factQty,
						UnitPrice:    s.unitRate,
						PlanAmount:   planAmt,
						FactAmount:   factAmt,
						Difference:   planAmt - factAmt,
					})
					vsub.EquipPlanTotal += planAmt
					vsub.EquipFactTotal += factAmt
				}
				synthRowID--
			}
			// Lines without any priced sub-resources still need to show
			// sensible Reja / Fakt — fall back to the work-level totals
			// so the row isn't a confusing 0/0 next to a non-zero Bosqich
			// jami.
			//
			// Two fallback triggers:
			//   1. NO sub-resource rows at all (the original case).
			//   2. Sub-resource rows EXIST but their PlanAmount + FactAmount
			//      all sum to 0. This happens when the Единич template
			//      import emits children at unit_rate=0 (price normally
			//      propagates from the Ресурс estimate via
			//      propagateResursPricesForProject; if propagation hasn't
			//      run, or the Ресурс side has no matching name, the
			//      children stay at 0 even though the parent work itself
			//      has a non-zero rate from agg.rate_max). Without this
			//      second branch, the section row showed Reja=0/Fakt=0
			//      while Bosqichlar showed real numbers — exactly the
			//      bug the user reported as "Reja va Fakt section is not
			//      showing".
			noPricedSubs := len(vsub.Materials) == 0 && len(vsub.Equipment) == 0
			zeroSubAmounts := vsub.MaterialPlanTotal == 0 && vsub.EquipPlanTotal == 0 &&
				vsub.MaterialFactTotal == 0 && vsub.EquipFactTotal == 0
			if (noPricedSubs || zeroSubAmounts) && (w.plan > 0 || w.fact > 0) {
				vsub.MaterialPlanTotal = w.plan
				vsub.MaterialFactTotal = w.fact
			}
			vsub.PlanTotal = vsub.MaterialPlanTotal + vsub.EquipPlanTotal
			vsub.FactTotal = vsub.MaterialFactTotal + vsub.EquipFactTotal
			vsub.Difference = vsub.PlanTotal - vsub.FactTotal

			sr.SubStages = append(sr.SubStages, vsub)
			sr.MaterialPlanTotal += vsub.MaterialPlanTotal
			sr.MaterialFactTotal += vsub.MaterialFactTotal
			sr.EquipPlanTotal += vsub.EquipPlanTotal
			sr.EquipFactTotal += vsub.EquipFactTotal

			// Mark this name as projected so the legacy real-sub-stages
			// loop below skips it. Same case-insensitive trim treatment
			// used for matching elsewhere.
			projectedNames[strings.ToLower(strings.TrimSpace(w.name))] = true
		}

		// Real construction_sub_stages rows that DIDN'T match any
		// estimate-line work in this stage. Skipped entirely when the
		// section already has estimate-line projections, because the
		// project is on the v2 workflow and any leftover rows in
		// construction_sub_stages are stale (re-imports, old auto-
		// creates from migration 363, etc.). Keeping them produced
		// duplicate listings whose order didn't match Bosqichlar
		// — the issue the user flagged as "ordering is still not
		// fixed". Only fall through to the legacy data path when
		// the section has NO estimate-line works at all (genuine
		// v1-only project).
		if len(stageWorkIDs) > 0 {
			subs = nil
		}
		for _, sub := range subs {
			if projectedNames[strings.ToLower(strings.TrimSpace(sub.Name))] {
				continue
			}
			ssr := SubStageResult{SubStage: sub}

			mats := materialsMap[sub.ID]
			if mats == nil {
				mats = []MaterialRow{}
			}
			ssr.Materials = mats

			eqs := equipmentMap[sub.ID]
			if eqs == nil {
				eqs = []EquipmentRow{}
			}
			ssr.Equipment = eqs

			for _, m := range mats {
				ssr.MaterialPlanTotal += m.PlanAmount
				ssr.MaterialFactTotal += m.FactAmount
			}
			for _, e := range eqs {
				ssr.EquipPlanTotal += e.PlanAmount
				ssr.EquipFactTotal += e.FactAmount
			}
			ssr.PlanTotal = ssr.MaterialPlanTotal + ssr.EquipPlanTotal
			ssr.FactTotal = ssr.MaterialFactTotal + ssr.EquipFactTotal

			// Estimate-line fallback if the legacy materials/equipment
			// tables happen to be empty even when the sub-stage was
			// manually linked to an estimate line via name.
			if ssr.PlanTotal == 0 && ssr.FactTotal == 0 {
				qty := 0.0
				if sub.ELQuantity.Valid {
					qty = sub.ELQuantity.Float64
				}
				done := 0.0
				if sub.ELDoneQuantity.Valid {
					done = sub.ELDoneQuantity.Float64
				}
				rate := 0.0
				switch {
				case sub.ELUnitRate.Valid && sub.ELUnitRate.Float64 > 0:
					rate = sub.ELUnitRate.Float64
				case sub.ELTotalAmount.Valid && sub.ELTotalAmount.Float64 > 0 && qty > 0:
					rate = sub.ELTotalAmount.Float64 / qty
				case sub.ELSubDerived.Valid && sub.ELSubDerived.Float64 > 0:
					rate = sub.ELSubDerived.Float64
				}
				if rate > 0 {
					ssr.MaterialPlanTotal = qty * rate
					ssr.MaterialFactTotal = done * rate
					ssr.PlanTotal = ssr.MaterialPlanTotal
					ssr.FactTotal = ssr.MaterialFactTotal
				}
			}
			ssr.Difference = ssr.PlanTotal - ssr.FactTotal

			sr.SubStages = append(sr.SubStages, ssr)
			sr.MaterialPlanTotal += ssr.MaterialPlanTotal
			sr.MaterialFactTotal += ssr.MaterialFactTotal
			sr.EquipPlanTotal += ssr.EquipPlanTotal
			sr.EquipFactTotal += ssr.EquipFactTotal
		}

		sr.PlanTotal = sr.MaterialPlanTotal + sr.EquipPlanTotal
		sr.FactTotal = sr.MaterialFactTotal + sr.EquipFactTotal
		sr.Difference = sr.PlanTotal - sr.FactTotal

		// Derive the section's display status from the works in it
		// rather than from the stored construction_stages.status (which
		// nobody reliably updates — that column was set once at section
		// auto-create and never moves). Rule:
		//   • any work past `pending` (in_progress / submitted /
		//     confirmed_supervisor / confirmed_engineer) OR with a
		//     non-zero done_quantity → section is `in_progress`
		//   • all works confirmed_engineer (locked) AND >=1 work →
		//     section is `completed`
		//   • else → keep stored status (typically `not_started`).
		//
		// Mirrors what Bosqichlar shows (the "JARAYONDA / TUGALLANGAN /
		// BOSHLANMAGAN" pill comes from the same derivation) so the two
		// pages agree.
		{
			anyStarted := false
			allConfirmed := true
			workCount := 0
			for _, wid := range stageWorkIDs {
				w := workByID[wid]
				workCount++
				if w.parentDone > 0 {
					anyStarted = true
				}
				if w.status != "" && w.status != "pending" {
					anyStarted = true
				}
				if w.status != "confirmed_engineer" {
					allConfirmed = false
				}
			}
			if workCount > 0 && allConfirmed {
				sr.Status = "completed"
			} else if anyStarted {
				sr.Status = "in_progress"
			}
			// else: leave whatever stg.Status was (usually "not_started").
		}

		totalMaterialPlan += sr.MaterialPlanTotal
		totalMaterialFact += sr.MaterialFactTotal
		totalEquipPlan += sr.EquipPlanTotal
		totalEquipFact += sr.EquipFactTotal

		stages = append(stages, sr)
	}

	planTotal := totalMaterialPlan + totalEquipPlan
	factTotal := totalMaterialFact + totalEquipFact

	// Imported-budget lookup (migration 369). The Ресурс Excel sheet
	// carries the canonical project totals at the bottom (ИТОГО ПРЯМЫЕ
	// ЗАТРАТЫ etc.) — we surfaced them onto construction_estimate during
	// import and now sum them across the project (or the selected
	// building) so the Reja vs Fakt page can show the file's grand total
	// instead of a per-line derivation.
	//
	// Match condition: only Ресурс estimates count, scoped to the same
	// building filter the rest of this handler uses. Edinich/ВОР imports
	// leave budget_total at 0 so they don't double-count here.
	var importedBudgetTotal, importedMaterialBudget, importedTransportBudget float64
	{
		budgetQuery := `
			SELECT COALESCE(SUM(budget_total), 0),
			       COALESCE(SUM(material_budget), 0),
			       COALESCE(SUM(transport_budget), 0)
			FROM construction_estimate
			WHERE project_id = $1
			  AND tenant_id = $2
			  AND LOWER(COALESCE(source_type, '')) = 'resurs'
		`
		budgetArgs := []interface{}{projectID, tenantID}
		if buildingFilter != "" {
			bid, _ := strconv.ParseInt(buildingFilter, 10, 64)
			budgetQuery += ` AND building_id = $3`
			budgetArgs = append(budgetArgs, bid)
		}
		_ = h.db.QueryRow(budgetQuery, budgetArgs...).Scan(
			&importedBudgetTotal,
			&importedMaterialBudget,
			&importedTransportBudget,
		)
	}

	// effectivePlan picks the imported budget when it's present (the
	// user's stated source of truth) and falls back to the per-line sum
	// when no Ресурс file has been imported yet. This keeps the
	// `difference` and `budget_used_pct` cards meaningful in both modes.
	effectivePlan := planTotal
	if importedBudgetTotal > 0 {
		effectivePlan = importedBudgetTotal
	}

	// completion_pct — block-wide "Bajarilish" card. Per stage =
	// completed_sub_stages / total_sub_stages * 100, then averaged across the
	// stages that have sub-stages (matches the web getSubStageProgress).
	// Computed over the WHOLE block (subStagesMap holds every block sub-stage),
	// so it stays correct regardless of which page is returned.
	var completionPctSum float64
	var completionStageCount int
	for _, sid := range stagesOrder {
		subs := subStagesMap[sid]
		if len(subs) == 0 {
			continue
		}
		completed := 0
		for _, ss := range subs {
			if ss.Status == "completed" {
				completed++
			}
		}
		completionPctSum += float64(completed) / float64(len(subs)) * 100
		completionStageCount++
	}
	completionPct := 0.0
	if completionStageCount > 0 {
		completionPct = completionPctSum / float64(completionStageCount)
	}

	// Pagination: total = block-wide top-level stage count. Slice the assembled
	// tree to the requested page; the summary above is unaffected.
	total := len(stages)
	var meta map[string]interface{}
	if paginate {
		start := offset
		if start > len(stages) {
			start = len(stages)
		}
		end := start + limit
		if end > len(stages) {
			end = len(stages)
		}
		stages = stages[start:end]

		totalPages := 0
		if limit > 0 {
			totalPages = (total + limit - 1) / limit
		}
		meta = map[string]interface{}{
			"page": page, "limit": limit, "total": total,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
			"has_prev":    page > 1,
		}
	}

	payload := map[string]interface{}{
		"stages": stages,
		"summary": map[string]interface{}{
			"material_plan_total":  totalMaterialPlan,
			"material_fact_total":  totalMaterialFact,
			"equipment_plan_total": totalEquipPlan,
			"equipment_fact_total": totalEquipFact,
			// plan_total now prefers the imported budget. Frontend can
			// still distinguish derived vs imported via plan_total_derived
			// and imported_budget below.
			"plan_total":         effectivePlan,
			"plan_total_derived": planTotal,
			"imported_budget":    importedBudgetTotal,
			"imported_material":  importedMaterialBudget,
			"imported_transport": importedTransportBudget,
			"fact_total":         factTotal,
			"difference":         effectivePlan - factTotal,
			"budget_used_pct":    budgetPct(factTotal, effectivePlan),
			"completion_pct":     completionPct,
		},
	}
	if meta != nil {
		payload["meta"] = meta
	}
	cacheRejaFaktResponse(cacheKey, payload)
	response.Success(c, payload)
}

// cacheRejaFaktResponse stores the response payload as JSON bytes so
// subsequent reads can short-circuit straight back to the network
// without re-marshalling. The shape mirrors what response.Success
// would have written, so frontend code sees identical results
// whether the handler computed fresh or pulled from cache.
func cacheRejaFaktResponse(key string, payload map[string]interface{}) {
	body, err := json.Marshal(map[string]interface{}{
		"data":    payload,
		"success": true,
	})
	if err != nil {
		return // best-effort — skip cache on marshal failure
	}
	rejaFaktCache.Store(key, &rejaFaktCacheEntry{
		body:      body,
		expiresAt: time.Now().Add(rejaFaktCacheTTL),
	})
}

func budgetPct(fact, plan float64) float64 {
	if plan <= 0 {
		return 0
	}
	return fact / plan * 100
}

// =====================================================
// EQUIPMENT CRUD HANDLERS
// =====================================================

// ListSubStageEquipment returns all equipment for a sub-stage
func (h *Handler) ListSubStageEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	subStageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid sub-stage ID")
		return
	}

	type Equipment struct {
		ID           int64     `json:"id"`
		SubStageID   int64     `json:"sub_stage_id"`
		Name         string    `json:"name"`
		Type         string    `json:"type"`
		WorkUnit     string    `json:"work_unit"`
		PlanQuantity float64   `json:"plan_quantity"`
		FactQuantity float64   `json:"fact_quantity"`
		Quantity     float64   `json:"quantity"`
		UnitPrice    float64   `json:"unit_price"`
		TotalCost    float64   `json:"total_cost"`
		Note         *string   `json:"note"`
		CreatedAt    time.Time `json:"created_at"`
	}

	rows, err := h.db.Query(`
		SELECT id, sub_stage_id, name, COALESCE(resource_type,'equipment'), work_unit, plan_quantity, fact_quantity, unit_price, note, created_at
		FROM construction_sub_stage_equipment
		WHERE sub_stage_id = $1 AND tenant_id = $2
		ORDER BY id ASC
	`, subStageID, tenantID)
	if err != nil {
		h.log.Error("Failed to list sub-stage equipment", "error", err)
		response.InternalError(c, "Failed to list equipment")
		return
	}
	defer rows.Close()

	items := []Equipment{}
	for rows.Next() {
		var e Equipment
		if err := rows.Scan(&e.ID, &e.SubStageID, &e.Name, &e.Type, &e.WorkUnit, &e.PlanQuantity, &e.FactQuantity, &e.UnitPrice, &e.Note, &e.CreatedAt); err != nil {
			continue
		}
		// Convenience fields: quantity = plan_quantity, total_cost = plan * price
		e.Quantity = e.PlanQuantity
		e.TotalCost = e.PlanQuantity * e.UnitPrice
		items = append(items, e)
	}

	response.Success(c, items)
}

// CreateSubStageEquipment adds equipment to a sub-stage
func (h *Handler) CreateSubStageEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	subStageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid sub-stage ID")
		return
	}

	var req struct {
		Name         string  `json:"name" binding:"required"`
		Type         string  `json:"type"`
		WorkUnit     string  `json:"work_unit"`
		Quantity     float64 `json:"quantity"`
		PlanQuantity float64 `json:"plan_quantity"`
		FactQuantity float64 `json:"fact_quantity"`
		UnitPrice    float64 `json:"unit_price"`
		Note         string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	if req.WorkUnit == "" {
		req.WorkUnit = "soat"
	}
	// Accept `quantity` as alias for plan_quantity (from frontend)
	if req.PlanQuantity == 0 && req.Quantity != 0 {
		req.PlanQuantity = req.Quantity
	}
	// Normalize resource type
	if req.Type != "employee" {
		req.Type = "equipment"
	}

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_sub_stage_equipment (
			tenant_id, sub_stage_id, name, resource_type, work_unit, plan_quantity, fact_quantity, unit_price, note
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, tenantID, subStageID, req.Name, req.Type, req.WorkUnit, req.PlanQuantity, req.FactQuantity, req.UnitPrice, nullStringFromVal(req.Note)).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create sub-stage equipment", "error", err)
		response.InternalError(c, "Failed to create equipment")
		return
	}

	projectID := h.getProjectIDForSubStage(subStageID)
	h.logRejaFaktAudit(tenantID, projectID, "equipment", id, req.Name, subStageID, "create", map[string]interface{}{
		"plan_quantity": req.PlanQuantity, "fact_quantity": req.FactQuantity, "unit_price": req.UnitPrice,
	}, c)

	// Activity log
	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "material", "Texnika qo'shildi: "+req.Name, "SubStageEquipment", id)

	// Check budget and notify if exceeded
	h.checkBudgetAndNotify(tenantID, projectID, subStageID, c)

	response.Success(c, map[string]interface{}{"id": id})
}

// UpdateSubStageEquipment updates an equipment row
func (h *Handler) UpdateSubStageEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		Name         *string  `json:"name"`
		WorkUnit     *string  `json:"work_unit"`
		PlanQuantity *float64 `json:"plan_quantity"`
		FactQuantity *float64 `json:"fact_quantity"`
		UnitPrice    *float64 `json:"unit_price"`
		Note         *string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic update
	sets := []string{}
	args := []interface{}{}
	argIdx := 1

	addArg := func(col string, val interface{}) {
		sets = append(sets, col+" = $"+strconv.Itoa(argIdx))
		args = append(args, val)
		argIdx++
	}

	if req.Name != nil {
		addArg("name", *req.Name)
	}
	if req.WorkUnit != nil {
		addArg("work_unit", *req.WorkUnit)
	}
	if req.PlanQuantity != nil {
		addArg("plan_quantity", *req.PlanQuantity)
	}
	if req.FactQuantity != nil {
		addArg("fact_quantity", *req.FactQuantity)
	}
	if req.UnitPrice != nil {
		addArg("unit_price", *req.UnitPrice)
	}
	if req.Note != nil {
		addArg("note", *req.Note)
	}

	if len(sets) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addArg("updated_at", time.Now())

	query := "UPDATE construction_sub_stage_equipment SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += " WHERE id = $" + strconv.Itoa(argIdx) + " AND tenant_id = $" + strconv.Itoa(argIdx+1)
	args = append(args, id, tenantID)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update equipment", "error", err)
		response.InternalError(c, "Failed to update equipment")
		return
	}

	if ra, _ := result.RowsAffected(); ra == 0 {
		response.NotFound(c, "Equipment not found")
		return
	}

	// Audit log
	var eqName string
	var eqSubStageID int64
	h.db.QueryRow(`SELECT name, sub_stage_id FROM construction_sub_stage_equipment WHERE id = $1`, id).Scan(&eqName, &eqSubStageID)
	eqProjectID := h.getProjectIDForSubStage(eqSubStageID)
	h.logRejaFaktAudit(tenantID, eqProjectID, "equipment", id, eqName, eqSubStageID, "update", map[string]interface{}{}, c)

	// Activity log
	eqUserID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, eqProjectID, eqUserID, "material", "Texnika yangilandi: "+eqName, "SubStageEquipment", id)

	// Check budget and notify
	h.checkBudgetAndNotify(tenantID, eqProjectID, eqSubStageID, c)

	response.Success(c, map[string]interface{}{"id": id})
}

// DeleteSubStageEquipment removes equipment from a sub-stage
func (h *Handler) DeleteSubStageEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	// Capture info before delete for audit
	var delEqName string
	var delEqSubStageID int64
	h.db.QueryRow(`SELECT name, sub_stage_id FROM construction_sub_stage_equipment WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&delEqName, &delEqSubStageID)

	result, err := h.db.Exec(`DELETE FROM construction_sub_stage_equipment WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete equipment", "error", err)
		response.InternalError(c, "Failed to delete equipment")
		return
	}

	if ra, _ := result.RowsAffected(); ra == 0 {
		response.NotFound(c, "Equipment not found")
		return
	}

	if delEqSubStageID > 0 {
		delProjectID := h.getProjectIDForSubStage(delEqSubStageID)
		h.logRejaFaktAudit(tenantID, delProjectID, "equipment", id, delEqName, delEqSubStageID, "delete", nil, c)

		// Activity log
		delUserID, _ := middleware.GetUserID(c)
		h.logConstructionActivity(tenantID, delProjectID, delUserID, "material", "Texnika o'chirildi: "+delEqName, "SubStageEquipment", id)
	}

	response.Success(c, map[string]interface{}{"message": "Deleted"})
}

// UpdateSubStageMaterialPlanFact updates plan/fact quantities for a material
func (h *Handler) UpdateSubStageMaterialPlanFact(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		PlanQuantity *float64 `json:"plan_quantity"`
		FactQuantity *float64 `json:"fact_quantity"`
		UnitCost     *float64 `json:"unit_cost"`
		Note         *string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get current
	var planQty, factQty, unitCost float64
	err = h.db.QueryRow(`
		SELECT COALESCE(plan_quantity, quantity), fact_quantity, unit_cost
		FROM construction_sub_stage_materials WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(&planQty, &factQty, &unitCost)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Material not found")
		} else {
			response.InternalError(c, "Failed to get material")
		}
		return
	}

	if req.PlanQuantity != nil {
		planQty = *req.PlanQuantity
	}
	if req.FactQuantity != nil {
		factQty = *req.FactQuantity
	}
	if req.UnitCost != nil {
		unitCost = *req.UnitCost
	}

	totalCost := planQty * unitCost

	if req.Note != nil {
		_, err = h.db.Exec(`
			UPDATE construction_sub_stage_materials
			SET plan_quantity = $1, fact_quantity = $2, unit_cost = $3, quantity = $1, total_cost = $4, note = $5, updated_date = NOW()
			WHERE id = $6 AND tenant_id = $7
		`, planQty, factQty, unitCost, totalCost, *req.Note, id, tenantID)
	} else {
		_, err = h.db.Exec(`
			UPDATE construction_sub_stage_materials
			SET plan_quantity = $1, fact_quantity = $2, unit_cost = $3, quantity = $1, total_cost = $4, updated_date = NOW()
			WHERE id = $5 AND tenant_id = $6
		`, planQty, factQty, unitCost, totalCost, id, tenantID)
	}
	if err != nil {
		h.log.Error("Failed to update material plan/fact", "error", err)
		response.InternalError(c, "Failed to update material")
		return
	}

	// Audit log
	var matName string
	var subStageID int64
	h.db.QueryRow(`SELECT product_name, sub_stage_id FROM construction_sub_stage_materials WHERE id = $1`, id).Scan(&matName, &subStageID)
	projectID := h.getProjectIDForSubStage(subStageID)
	h.logRejaFaktAudit(tenantID, projectID, "material", id, matName, subStageID, "update", map[string]interface{}{
		"plan_quantity": planQty, "fact_quantity": factQty, "unit_cost": unitCost,
	}, c)

	// Activity log
	matUserID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, matUserID, "material", "Material yangilandi: "+matName, "SubStageMaterial", id)

	// Check budget and notify
	h.checkBudgetAndNotify(tenantID, projectID, subStageID, c)

	response.Success(c, map[string]interface{}{"id": id})
}

// =====================================================
// AUDIT LOG HELPERS & ENDPOINT
// =====================================================

func (h *Handler) logRejaFaktAudit(tenantID uuid.UUID, projectID int64, itemType string, itemID int64, itemName string, subStageID int64, action string, changes map[string]interface{}, c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	var userName string
	if userID != uuid.Nil {
		h.db.QueryRow(`SELECT COALESCE(name, email, '') FROM users WHERE id = $1`, userID).Scan(&userName)
	}
	changesJSON, _ := json.Marshal(changes)
	h.db.Exec(`
		INSERT INTO construction_reja_fakt_audit (tenant_id, project_id, item_type, item_id, item_name, sub_stage_id, action, changes, user_id, user_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, tenantID, projectID, itemType, itemID, itemName, subStageID, action, changesJSON, userID, userName)
}

func (h *Handler) getProjectIDForSubStage(subStageID int64) int64 {
	var projectID int64
	h.db.QueryRow(`
		SELECT s.project_id FROM construction_sub_stages ss
		JOIN construction_stages s ON s.id = ss.stage_id
		WHERE ss.id = $1
	`, subStageID).Scan(&projectID)
	return projectID
}

// checkBudgetAndNotify checks if a stage's budget is exceeded after a change,
// and sends notification to the project manager + logs activity
func (h *Handler) checkBudgetAndNotify(tenantID uuid.UUID, projectID int64, subStageID int64, c *gin.Context) {
	// Find the stage for this sub-stage
	var stageID int64
	var stageName string
	err := h.db.QueryRow(`
		SELECT s.id, s.name FROM construction_sub_stages ss
		JOIN construction_stages s ON s.id = ss.stage_id
		WHERE ss.id = $1
	`, subStageID).Scan(&stageID, &stageName)
	if err != nil {
		return
	}

	// Calculate plan and fact totals for the stage (materials + equipment)
	var planTotal, factTotal float64

	// Materials
	h.db.QueryRow(`
		SELECT COALESCE(SUM(COALESCE(plan_quantity, quantity) * unit_cost), 0),
		       COALESCE(SUM(fact_quantity * unit_cost), 0)
		FROM construction_sub_stage_materials m
		JOIN construction_sub_stages ss ON ss.id = m.sub_stage_id
		WHERE ss.stage_id = $1 AND m.tenant_id = $2
	`, stageID, tenantID).Scan(&planTotal, &factTotal)

	// Equipment
	var eqPlan, eqFact float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(plan_quantity * unit_price), 0),
		       COALESCE(SUM(fact_quantity * unit_price), 0)
		FROM construction_sub_stage_equipment e
		JOIN construction_sub_stages ss ON ss.id = e.sub_stage_id
		WHERE ss.stage_id = $1 AND e.tenant_id = $2
	`, stageID, tenantID).Scan(&eqPlan, &eqFact)

	planTotal += eqPlan
	factTotal += eqFact

	if planTotal <= 0 {
		return
	}

	pct := factTotal / planTotal * 100

	// Only notify at critical thresholds
	var notifType, notifTitle, notifMsg string
	if pct > 100 {
		notifType = "budget_exceeded"
		notifTitle = "Byudjet oshdi: " + stageName
		notifMsg = stageName + " bosqichi byudjeti oshdi (" + strconv.FormatFloat(pct, 'f', 1, 64) + "%)"
	} else if pct > 90 {
		notifType = "budget_warning"
		notifTitle = "Byudjet ogohlantirishI: " + stageName
		notifMsg = stageName + " bosqichi byudjeti 90% dan oshdi (" + strconv.FormatFloat(pct, 'f', 1, 64) + "%)"
	} else {
		return
	}

	// Log activity on the project record
	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "status_change", notifMsg, "Stage", stageID)

	// Send notification to project manager
	var managerEmployeeID *int64
	h.db.QueryRow(`SELECT project_manager_id FROM construction_projects WHERE id = $1 AND tenant_id = $2`, projectID, tenantID).Scan(&managerEmployeeID)
	if managerEmployeeID != nil {
		var managerUserID uuid.UUID
		err := h.db.QueryRow(`SELECT user_id FROM employees WHERE id = $1 AND tenant_id = $2 AND user_id IS NOT NULL`, *managerEmployeeID, tenantID).Scan(&managerUserID)
		if err == nil && managerUserID != uuid.Nil {
			h.createNotification(tenantID, managerUserID, notifType, notifTitle, notifMsg, map[string]interface{}{
				"project_id": projectID,
				"stage_id":   stageID,
				"stage_name": stageName,
				"budget_pct": pct,
			})
		}
	}

	// Also notify chief engineer if different
	var chiefEmployeeID *int64
	h.db.QueryRow(`SELECT chief_engineer_id FROM construction_projects WHERE id = $1 AND tenant_id = $2`, projectID, tenantID).Scan(&chiefEmployeeID)
	if chiefEmployeeID != nil && (managerEmployeeID == nil || *chiefEmployeeID != *managerEmployeeID) {
		var chiefUserID uuid.UUID
		err := h.db.QueryRow(`SELECT user_id FROM employees WHERE id = $1 AND tenant_id = $2 AND user_id IS NOT NULL`, *chiefEmployeeID, tenantID).Scan(&chiefUserID)
		if err == nil && chiefUserID != uuid.Nil {
			h.createNotification(tenantID, chiefUserID, notifType, notifTitle, notifMsg, map[string]interface{}{
				"project_id": projectID,
				"stage_id":   stageID,
				"stage_name": stageName,
				"budget_pct": pct,
			})
		}
	}
}

// ListRejaFaktAudit returns audit history for a specific item or sub-stage
func (h *Handler) ListRejaFaktAudit(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	itemType := c.Query("item_type")
	itemIDStr := c.Query("item_id")
	subStageIDStr := c.Query("sub_stage_id")

	type AuditEntry struct {
		ID        int64           `json:"id"`
		ItemType  string          `json:"item_type"`
		ItemID    int64           `json:"item_id"`
		ItemName  string          `json:"item_name"`
		Action    string          `json:"action"`
		Changes   json.RawMessage `json:"changes"`
		UserName  *string         `json:"user_name"`
		CreatedAt time.Time       `json:"created_at"`
	}

	// The client sends `limit` (and now `page`); both were previously ignored
	// and each branch truncated at a hardcoded 50/100 with no offset, so older
	// rows were unreachable. `page_size` is accepted as an alias.
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limitRaw := c.Query("page_size")
	if limitRaw == "" {
		limitRaw = c.DefaultQuery("limit", "20")
	}
	limit, _ := strconv.Atoi(limitRaw)
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	offset := (page - 1) * limit

	// Each branch keeps its own distinct WHERE; only the tail changes.
	var where string
	var args []interface{}
	if itemType != "" && itemIDStr != "" {
		itemID, _ := strconv.ParseInt(itemIDStr, 10, 64)
		where = " WHERE tenant_id = $1 AND item_type = $2 AND item_id = $3"
		args = []interface{}{tenantID, itemType, itemID}
	} else if subStageIDStr != "" {
		subStageID, _ := strconv.ParseInt(subStageIDStr, 10, 64)
		where = " WHERE tenant_id = $1 AND sub_stage_id = $2"
		args = []interface{}{tenantID, subStageID}
	} else {
		response.BadRequest(c, "Provide item_type+item_id or sub_stage_id")
		return
	}

	var total int
	if err := h.db.QueryRow("SELECT COUNT(*) FROM construction_reja_fakt_audit"+where, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count audit log", "error", err)
		response.InternalError(c, "Failed to list audit log")
		return
	}

	rows, qErr := h.db.Query(fmt.Sprintf(`
		SELECT id, item_type, item_id, item_name, action, COALESCE(changes, '{}'), user_name, created_at
		FROM construction_reja_fakt_audit`+where+`
		ORDER BY created_at DESC, id DESC LIMIT %d OFFSET %d`, limit, offset), args...)

	if qErr != nil {
		h.log.Error("Failed to list audit log", "error", qErr)
		response.InternalError(c, "Failed to list audit log")
		return
	}
	defer rows.Close()

	entries := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ItemType, &e.ItemID, &e.ItemName, &e.Action, &e.Changes, &e.UserName, &e.CreatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	response.Paginated(c, entries, page, limit, total)
}
