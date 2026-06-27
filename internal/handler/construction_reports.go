package handler

import (
	"strconv"
	"strings"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// =====================================================
// CONSTRUCTION REPORTS
// =====================================================

// GetProjectSummaryReport returns summary report for a project
// GET /construction/projects/:id/reports/summary
func (h *Handler) GetProjectSummaryReport(c *gin.Context) {
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

	// Total costs
	var totalActual float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(amount), 0)
		FROM construction_expense_lines
		WHERE project_id = $1 AND tenant_id = $2 AND status = 'approved' AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&totalActual)

	// By category
	byCatRows, _ := h.db.Query(`
		SELECT COALESCE(cat.name, 'Uncategorized'), COALESCE(cat.code, ''), COALESCE(SUM(el.amount), 0)
		FROM construction_expense_lines el
		LEFT JOIN construction_cost_categories cat ON cat.id = el.cost_category_id
		WHERE el.project_id = $1 AND el.tenant_id = $2 AND el.status = 'approved' AND el.deleted_at IS NULL
		GROUP BY cat.name, cat.code
		ORDER BY SUM(el.amount) DESC
	`, projectID, tenantID)
	type CategoryTotal struct {
		Name  string  `json:"name"`
		Code  string  `json:"code"`
		Total float64 `json:"total"`
	}
	byCategory := []CategoryTotal{}
	if byCatRows != nil {
		defer byCatRows.Close()
		for byCatRows.Next() {
			var ct CategoryTotal
			if err := byCatRows.Scan(&ct.Name, &ct.Code, &ct.Total); err == nil {
				byCategory = append(byCategory, ct)
			}
		}
	}

	// By stage
	byStageRows, _ := h.db.Query(`
		SELECT COALESCE(s.name, 'No Stage'), COALESCE(SUM(el.amount), 0)
		FROM construction_expense_lines el
		LEFT JOIN construction_stages s ON s.id = el.stage_id
		WHERE el.project_id = $1 AND el.tenant_id = $2 AND el.status = 'approved' AND el.deleted_at IS NULL
		GROUP BY s.name
		ORDER BY SUM(el.amount) DESC
	`, projectID, tenantID)
	type StageTotal struct {
		Stage string  `json:"stage"`
		Total float64 `json:"total"`
	}
	byStage := []StageTotal{}
	if byStageRows != nil {
		defer byStageRows.Close()
		for byStageRows.Next() {
			var st StageTotal
			if err := byStageRows.Scan(&st.Stage, &st.Total); err == nil {
				byStage = append(byStage, st)
			}
		}
	}

	// Monthly dynamics (last 24 months)
	monthlyRows, _ := h.db.Query(`
		SELECT TO_CHAR(DATE_TRUNC('month', expense_date), 'YYYY-MM'),
		       COALESCE(SUM(amount), 0)
		FROM construction_expense_lines
		WHERE project_id = $1 AND tenant_id = $2 AND status = 'approved' AND deleted_at IS NULL
		GROUP BY DATE_TRUNC('month', expense_date)
		ORDER BY DATE_TRUNC('month', expense_date) ASC
	`, projectID, tenantID)
	type MonthlyTotal struct {
		Month string  `json:"month"`
		Total float64 `json:"total"`
	}
	monthly := []MonthlyTotal{}
	if monthlyRows != nil {
		defer monthlyRows.Close()
		for monthlyRows.Next() {
			var mt MonthlyTotal
			if err := monthlyRows.Scan(&mt.Month, &mt.Total); err == nil {
				monthly = append(monthly, mt)
			}
		}
	}

	// Top-10 materials
	top10MatRows, _ := h.db.Query(`
		SELECT COALESCE(p.name, el.description), SUM(el.quantity), AVG(el.unit_price), SUM(el.amount)
		FROM construction_expense_lines el
		LEFT JOIN products p ON p.id = el.product_id
		WHERE el.project_id = $1 AND el.tenant_id = $2 AND el.status = 'approved'
		  AND el.deleted_at IS NULL AND el.product_id IS NOT NULL
		GROUP BY p.name, el.description
		ORDER BY SUM(el.amount) DESC
		LIMIT 10
	`, projectID, tenantID)
	type MaterialLine struct {
		Name      string   `json:"name"`
		Quantity  *float64 `json:"quantity"`
		AvgPrice  *float64 `json:"avg_price"`
		Total     float64  `json:"total"`
	}
	top10Materials := []MaterialLine{}
	if top10MatRows != nil {
		defer top10MatRows.Close()
		for top10MatRows.Next() {
			var ml MaterialLine
			if err := top10MatRows.Scan(&ml.Name, &ml.Quantity, &ml.AvgPrice, &ml.Total); err == nil {
				top10Materials = append(top10Materials, ml)
			}
		}
	}

	// Top-10 vendors
	top10VendorRows, _ := h.db.Query(`
		SELECT COALESCE(v.name, 'Unknown'), SUM(el.amount)
		FROM construction_expense_lines el
		LEFT JOIN contacts v ON v.id = el.vendor_id
		WHERE el.project_id = $1 AND el.tenant_id = $2 AND el.status = 'approved'
		  AND el.deleted_at IS NULL AND el.vendor_id IS NOT NULL
		GROUP BY v.name
		ORDER BY SUM(el.amount) DESC
		LIMIT 10
	`, projectID, tenantID)
	type VendorTotal struct {
		Vendor string  `json:"vendor"`
		Total  float64 `json:"total"`
	}
	top10Vendors := []VendorTotal{}
	if top10VendorRows != nil {
		defer top10VendorRows.Close()
		for top10VendorRows.Next() {
			var vt VendorTotal
			if err := top10VendorRows.Scan(&vt.Vendor, &vt.Total); err == nil {
				top10Vendors = append(top10Vendors, vt)
			}
		}
	}

	response.Success(c, map[string]interface{}{
		"total_actual":    totalActual,
		"by_category":     byCategory,
		"by_stage":        byStage,
		"monthly":         monthly,
		"top10_materials": top10Materials,
		"top10_vendors":   top10Vendors,
	})
}

// GetStageBudgetReport returns plan vs actual by stage × category
// GET /construction/projects/:id/reports/budget
// Optional query param: ?building_id=<id> — scope the stages (and the
// total_planned / total_actual KPIs) to a single building/block so the
// Byudjet tab can mirror the per-block tab row used on the Bosqichlar tab
// (migration 333 added construction_stages.building_id). Omitting the param
// keeps the original project-wide behaviour.
func (h *Handler) GetStageBudgetReport(c *gin.Context) {
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

	// Optional building filter. `0` / missing / unparseable = project-wide.
	var buildingID int64
	if raw := c.Query("building_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			buildingID = v
		}
	}

	// When a building filter is supplied we add `AND e.building_id = $3`
	// to the estimate_line CTE so we only sum the lines of that block's
	// edinich estimate. Stage filtering would be wrong here because the
	// row set is now driven by parent_item_number from estimate lines,
	// not from construction_stages.
	stageFilterSQL := ""
	stageArgs := []interface{}{projectID, tenantID}
	if buildingID > 0 {
		stageFilterSQL = " AND e.building_id = $3"
		stageArgs = append(stageArgs, buildingID)
	}

	// Stages with planned budgets and actual totals.
	//
	// `planned_budget` on the construction_stages row is hardcoded to 0
	// at auto-create time (we don't yet know the price when the stage is
	// being seeded from a section header during import). So we COMPUTE
	// planned per-stage from the matching Единич estimate lines:
	//
	//   For each parent work whose parent_item_number = stage.name in
	//   ANY Единич estimate of this project,
	//     • if it has children → SUM(children.total_amount)
	//     • else                → parent.total_amount
	//   Sum across all such parents.
	//
	// We deliberately DON'T filter by building_id when matching lines to
	// stages. In practice, users re-import their estimates many times and
	// stages from older imports may carry a different building_id than
	// the current priced lines. Filtering by building would leave most
	// stages at 0 even though the prices exist in the project. Matching
	// by section path (parent_item_number = stage.name) is unique enough
	// in real estimates that cross-block leakage isn't a concern; the
	// total_planned at the bottom uses the same broad scope, so the per-
	// row sums add up to the total.
	// Source the row set DIRECTLY from estimate-line parent_item_numbers
	// (in the project's Единич estimates) instead of construction_stages.
	// This guarantees that every section path with priced works gets a
	// row, even when construction_stages is out of sync (re-imports, old
	// stale stages from VOR-era code, etc.). For each section path we
	// LEFT JOIN to construction_stages by name to grab the optional
	// stage_id + building_id, so per-building filtering still works when
	// a stage row exists; sections without a matching stage default to
	// stage_id=0 and pass through the 'all' filter.
	//
	// `actual` is 0 unless a matching стажа exists (because expenses are
	// keyed by stage_id). That's fine for the smeta-driven Byudjet view
	// — once the user starts recording expenses we can revisit.
	rows, err := h.db.Query(`
		WITH parent_costs AS (
		    SELECT
		        e.project_id,
		        e.tenant_id,
		        e.building_id,
		        p.parent_item_number AS section_path,
		        SUM(
		            CASE WHEN EXISTS (
		                SELECT 1 FROM construction_estimate_line ch
		                WHERE ch.parent_line_id = p.id AND ch.tenant_id = p.tenant_id
		            ) THEN COALESCE((
		                SELECT SUM(ch.total_amount)
		                FROM construction_estimate_line ch
		                WHERE ch.parent_line_id = p.id AND ch.tenant_id = p.tenant_id
		            ), 0)
		            ELSE COALESCE(p.total_amount, 0)
		            END
		        ) AS planned
		    FROM construction_estimate_line p
		    JOIN construction_estimate e ON e.id = p.estimate_id
		    WHERE e.project_id = $1
		      AND e.tenant_id  = $2
		      AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
		      AND COALESCE(p.resource_type, '') = ''
		      AND p.parent_line_id IS NULL
		      AND COALESCE(p.parent_item_number, '') <> ''
		      `+stageFilterSQL+`
		    GROUP BY e.project_id, e.tenant_id, e.building_id, p.parent_item_number
		)
		SELECT
		    COALESCE(s.id, 0)                                   AS stage_id,
		    pc.section_path                                     AS stage_name,
		    pc.planned                                          AS planned_budget,
		    COALESCE(cat.id, 0)                                 AS category_id,
		    COALESCE(cat.name, 'Uncategorized')                 AS category_name,
		    COALESCE(cat.code, '')                              AS category_code,
		    COALESCE(SUM(CASE WHEN el.status='approved' AND el.deleted_at IS NULL THEN el.amount ELSE 0 END), 0) AS actual
		FROM parent_costs pc
		LEFT JOIN LATERAL (
		    -- Match a stage to this section bucket. Two name shapes
		    -- coexist in the wild:
		    --   • Full path: "СЕКЦИЯ №5 › ЗЕМЛЯННЫЕ РАБОТЫ" (newer
		    --     auto-creates write the full parent_item_number).
		    --   • Leaf only: "ЗЕМЛЯННЫЕ РАБОТЫ" (legacy single-block
		    --     projects whose stages were created by hand).
		    -- We accept either form, BUT we have to be careful in
		    -- multi-block projects: a leaf-named stage from Block 1
		    -- ("ЗЕМЛЯННЫЕ РАБОТЫ", building_id=14) must NOT match a
		    -- Block 2 section bucket. Without that guard the LATERAL
		    -- happily picked the Block 1 stage (lower id ⇒ earlier
		    -- in ORDER BY id ASC), then the LEFT JOIN below looked
		    -- for expenses on the wrong stage_id and reported Fakt=0
		    -- in the per-section row even though the top card was
		    -- correct — that's the bug the user reported as
		    -- "Byudjet shows Fakt=0 in section but top is 97,200".
		    --
		    -- Scoping rules:
		    --   1. Building-aware: the stage's building_id must match
		    --      the line's building_id, OR the stage has none
		    --      (project-wide stage). Stages in a different
		    --      building are excluded entirely.
		    --   2. Order: prefer same-building match, then full-path
		    --      name match, then by id. So "СЕКЦИЯ №5 › ЗЕМЛЯННЫЕ
		    --      РАБОТЫ" picks the building-15 full-path stage 1277
		    --      over the building-14 leaf stage 1197.
		    SELECT cs.id, cs.stage_order
		    FROM construction_stages cs
		    WHERE cs.tenant_id  = pc.tenant_id
		      AND cs.project_id = pc.project_id
		      AND (
		          cs.name = pc.section_path
		          OR cs.name = regexp_replace(COALESCE(pc.section_path, ''), '^.*›\s*', '')
		      )
		      AND (
		          pc.building_id IS NULL
		          OR cs.building_id IS NULL
		          OR cs.building_id = pc.building_id
		      )
		    ORDER BY
		      CASE WHEN cs.building_id = pc.building_id THEN 0 ELSE 1 END ASC,
		      CASE WHEN cs.name = pc.section_path THEN 0 ELSE 1 END ASC,
		      cs.id ASC
		    LIMIT 1
		) s ON TRUE
		LEFT JOIN construction_expense_lines el
		    ON el.stage_id = s.id AND el.project_id = pc.project_id
		LEFT JOIN construction_cost_categories cat
		    ON cat.id = el.cost_category_id
		GROUP BY s.id, pc.section_path, pc.planned, s.stage_order, cat.id, cat.name, cat.code
		ORDER BY pc.section_path ASC, cat.name ASC
	`, stageArgs...)
	if err != nil {
		h.log.Error("Failed to query stage budget", "error", err)
		response.InternalError(c, "Failed to get budget report")
		return
	}
	defer rows.Close()

	type BudgetRow struct {
		StageID       int64   `json:"stage_id"`
		StageName     string  `json:"stage_name"`
		PlannedBudget float64 `json:"planned_budget"`
		CategoryID    int64   `json:"category_id"`
		CategoryName  string  `json:"category_name"`
		CategoryCode  string  `json:"category_code"`
		Actual        float64 `json:"actual"`
		Variance      float64 `json:"variance"`
		VariancePct   float64 `json:"variance_pct"`
	}

	budgetRows := []BudgetRow{}
	for rows.Next() {
		var br BudgetRow
		if err := rows.Scan(
			&br.StageID, &br.StageName, &br.PlannedBudget,
			&br.CategoryID, &br.CategoryName, &br.CategoryCode,
			&br.Actual,
		); err != nil {
			continue
		}
		br.Variance = br.PlannedBudget - br.Actual
		if br.PlannedBudget > 0 {
			br.VariancePct = (br.Actual / br.PlannedBudget) * 100
		}
		budgetRows = append(budgetRows, br)
	}

	// Also compute total actual for project (including non-stage expenses).
	// When a building filter is active we scope actuals to that building too.
	//
	// Two attribution paths are used so that legacy data (where
	// construction_stages.building_id is NULL because the stage was created
	// before migration 333 backfilled buildings) still shows up:
	//   (a) direct match — stage.building_id = $3, OR
	//   (b) name-via-estimate fallback — the stage's name equals a
	//       parent_item_number on an estimate line whose estimate has
	//       building_id = $3. This mirrors the per-section breakdown
	//       above, which sources rows from estimate-line section paths
	//       rather than from construction_stages directly.
	// Project-wide expenses (stage_id NULL) are still excluded from per-
	// building totals because they can't be attributed to a specific block.
	var totalActual float64
	if buildingID > 0 {
		_ = h.db.QueryRow(`
			SELECT COALESCE(SUM(el.amount), 0)
			FROM construction_expense_lines el
			JOIN construction_stages s
			  ON s.id = el.stage_id AND s.tenant_id = el.tenant_id
			WHERE el.project_id = $1 AND el.tenant_id = $2
			  AND el.status = 'approved' AND el.deleted_at IS NULL
			  AND (
			    s.building_id = $3
			    OR EXISTS (
			      SELECT 1
			      FROM construction_estimate_line ll
			      JOIN construction_estimate ee ON ee.id = ll.estimate_id
			      WHERE ll.tenant_id = el.tenant_id
			        AND ee.project_id = el.project_id
			        AND ee.building_id = $3
			        AND ll.parent_item_number = s.name
			      LIMIT 1
			    )
			  )
		`, projectID, tenantID, buildingID).Scan(&totalActual)
	} else {
		_ = h.db.QueryRow(`
			SELECT COALESCE(SUM(amount), 0)
			FROM construction_expense_lines
			WHERE project_id = $1 AND tenant_id = $2 AND status = 'approved' AND deleted_at IS NULL
		`, projectID, tenantID).Scan(&totalActual)
	}

	// Total planned — sum the per-parent-work computed cost across every
	// parent work in the project's Единич estimates (scoped to building
	// when filtered). Mirrors the per-stage formula above so that
	// `total_planned == SUM(rows.planned_budget)` modulo rounding.
	var totalPlanned float64
	if buildingID > 0 {
		_ = h.db.QueryRow(`
			SELECT COALESCE(SUM(
				CASE WHEN EXISTS (
					SELECT 1 FROM construction_estimate_line ch
					WHERE ch.parent_line_id = p.id AND ch.tenant_id = p.tenant_id
				) THEN COALESCE((
					SELECT SUM(ch.total_amount)
					FROM construction_estimate_line ch
					WHERE ch.parent_line_id = p.id AND ch.tenant_id = p.tenant_id
				), 0)
				ELSE COALESCE(p.total_amount, 0)
				END
			), 0)
			FROM construction_estimate_line p
			JOIN construction_estimate e ON e.id = p.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
			  AND e.building_id = $3
			  AND COALESCE(p.resource_type, '') = ''
			  AND p.parent_line_id IS NULL
		`, projectID, tenantID, buildingID).Scan(&totalPlanned)
	} else {
		_ = h.db.QueryRow(`
			SELECT COALESCE(SUM(
				CASE WHEN EXISTS (
					SELECT 1 FROM construction_estimate_line ch
					WHERE ch.parent_line_id = p.id AND ch.tenant_id = p.tenant_id
				) THEN COALESCE((
					SELECT SUM(ch.total_amount)
					FROM construction_estimate_line ch
					WHERE ch.parent_line_id = p.id AND ch.tenant_id = p.tenant_id
				), 0)
				ELSE COALESCE(p.total_amount, 0)
				END
			), 0)
			FROM construction_estimate_line p
			JOIN construction_estimate e ON e.id = p.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
			  AND COALESCE(p.resource_type, '') = ''
			  AND p.parent_line_id IS NULL
		`, projectID, tenantID).Scan(&totalPlanned)
	}

	// Imported-budget override (migration 369). The Ресурс Excel sheet
	// carries the canonical project totals at the bottom (ИТОГО ПРЯМЫЕ
	// ЗАТРАТЫ) — captured at import time and stored on construction_estimate.
	// The Единич-derived sum above always under-counts the real budget
	// because it ignores transport overhead and indirect costs that live
	// only on the Ресурс sheet, so when an imported budget exists we
	// prefer it as the authoritative figure for the Byudjet KPI cards.
	// Scoped to the same building filter as the rest of the handler so a
	// per-block view shows that block's imported total.
	{
		var importedBudget float64
		budgetQuery := `
			SELECT COALESCE(SUM(budget_total), 0)
			FROM construction_estimate
			WHERE project_id = $1
			  AND tenant_id  = $2
			  AND LOWER(COALESCE(source_type, '')) = 'resurs'
		`
		budgetArgs := []interface{}{projectID, tenantID}
		if buildingID > 0 {
			budgetQuery += ` AND building_id = $3`
			budgetArgs = append(budgetArgs, buildingID)
		}
		_ = h.db.QueryRow(budgetQuery, budgetArgs...).Scan(&importedBudget)
		if importedBudget > 0 {
			totalPlanned = importedBudget
		}
	}

	// Reconciliation row. The per-section breakdown query above only
	// counts expenses whose stage_id matches a stage with the same
	// `name` as a parent_item_number — manual expense entries created
	// without a stage_id (or with a stage_id that doesn't map to any
	// section in the smeta) drop out. Those still flow into
	// `total_actual` because the top-card query just sums every
	// approved expense for the project, so the sum of breakdown rows
	// would be lower than the headline. Adding a synthetic
	// "Boshqalar / Project-wide" row equal to the difference keeps the
	// two sides reconciled — bug "Fakt 4 612 000 in headline but 0
	// across all sections".
	//
	// Skipped when a building filter is active because the per-building
	// totalActual query already restricts to expenses tied to a stage
	// with that building_id, so there's no untagged residue.
	if buildingID == 0 {
		var mapped float64
		for _, br := range budgetRows {
			mapped += br.Actual
		}
		residue := totalActual - mapped
		// Allow a small floating-point cushion so a rounding tail
		// doesn't show as a 0,01 sum row.
		if residue > 0.5 {
			budgetRows = append(budgetRows, BudgetRow{
				StageID:       0,
				StageName:     "(Boshqalar / Project-wide)",
				PlannedBudget: 0,
				CategoryID:    0,
				CategoryName:  "Uncategorized",
				CategoryCode:  "",
				Actual:        residue,
				Variance:      -residue,
				VariancePct:   0,
			})
		}
	}

	response.Success(c, map[string]interface{}{
		"rows":          budgetRows,
		"total_planned": totalPlanned,
		"total_actual":  totalActual,
		"total_variance": totalPlanned - totalActual,
	})
}

// GetMaterialsReport returns materials summary for a project
// GET /construction/projects/:id/reports/materials
func (h *Handler) GetMaterialsReport(c *gin.Context) {
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

	query := `
		SELECT COALESCE(p.name, el.description),
		       COALESCE(el.uom, ''),
		       COALESCE(SUM(el.quantity), 0),
		       CASE WHEN SUM(el.quantity) > 0 THEN SUM(el.amount) / SUM(el.quantity) ELSE 0 END,
		       COALESCE(SUM(el.amount), 0)
		FROM construction_expense_lines el
		LEFT JOIN products p ON p.id = el.product_id
		WHERE el.project_id = $1 AND el.tenant_id = $2
		  AND el.status = 'approved' AND el.deleted_at IS NULL
		  AND el.product_id IS NOT NULL
	`
	args := []interface{}{projectID, tenantID}
	argCount := 2

	if stageIDStr := c.Query("stage_id"); stageIDStr != "" {
		argCount++
		stageID, _ := strconv.ParseInt(stageIDStr, 10, 64)
		query += " AND el.stage_id = $" + strconv.Itoa(argCount)
		args = append(args, stageID)
	}
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		query += " AND el.expense_date >= $" + strconv.Itoa(argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		query += " AND el.expense_date <= $" + strconv.Itoa(argCount)
		args = append(args, dateTo)
	}
	if vendorIDStr := c.Query("vendor_id"); vendorIDStr != "" {
		argCount++
		vendorID, _ := uuid.Parse(vendorIDStr)
		query += " AND el.vendor_id = $" + strconv.Itoa(argCount)
		args = append(args, vendorID)
	}

	query += " GROUP BY p.name, el.description, el.uom ORDER BY SUM(el.amount) DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query materials report", "error", err)
		response.InternalError(c, "Failed to get materials report")
		return
	}
	defer rows.Close()

	type MaterialRow struct {
		Name     string  `json:"name"`
		Uom      string  `json:"uom"`
		Quantity float64 `json:"quantity"`
		AvgPrice float64 `json:"avg_price"`
		Total    float64 `json:"total"`
	}

	materials := []MaterialRow{}
	var grandTotal float64
	for rows.Next() {
		var mr MaterialRow
		if err := rows.Scan(&mr.Name, &mr.Uom, &mr.Quantity, &mr.AvgPrice, &mr.Total); err == nil {
			materials = append(materials, mr)
			grandTotal += mr.Total
		}
	}

	response.Success(c, map[string]interface{}{
		"items":       materials,
		"grand_total": grandTotal,
	})
}

// GetJournalEntriesReport returns all construction journal entries for a project
// GET /construction/projects/:id/reports/journal-entries
func (h *Handler) GetJournalEntriesReport(c *gin.Context) {
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

	// Collect journal entry IDs linked to expense lines for this project
	query := `
		SELECT je.id, je.entry_number, je.entry_date, je.description,
		       je.source_type, je.total_debit, je.total_credit, je.status,
		       je.created_at
		FROM journal_entries je
		WHERE je.tenant_id = $1
		  AND je.source_type IN ('construction_expense','construction_expense_reversal','project_commission','material_request')
		  AND je.id IN (
		      SELECT journal_entry_id FROM construction_expense_lines
		      WHERE project_id = $2 AND tenant_id = $1 AND deleted_at IS NULL AND journal_entry_id IS NOT NULL
		      UNION ALL
		      SELECT commission_journal_entry_id FROM construction_projects
		      WHERE id = $2 AND tenant_id = $1 AND commission_journal_entry_id IS NOT NULL
		  )
	`
	args := []interface{}{tenantID, projectID}
	argCount := 2

	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		query += " AND je.entry_date >= $" + strconv.Itoa(argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		query += " AND je.entry_date <= $" + strconv.Itoa(argCount)
		args = append(args, dateTo)
	}

	query += " ORDER BY je.entry_date DESC, je.created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query journal entries report", "error", err)
		response.InternalError(c, "Failed to get journal entries")
		return
	}
	defer rows.Close()

	type JERow struct {
		ID          string  `json:"id"`
		EntryNumber string  `json:"entry_number"`
		EntryDate   string  `json:"entry_date"`
		Description string  `json:"description"`
		SourceType  string  `json:"source_type"`
		TotalDebit  float64 `json:"total_debit"`
		TotalCredit float64 `json:"total_credit"`
		Status      string  `json:"status"`
		CreatedAt   string  `json:"created_at"`
	}

	entries := []JERow{}
	for rows.Next() {
		var jr JERow
		if err := rows.Scan(
			&jr.ID, &jr.EntryNumber, &jr.EntryDate, &jr.Description,
			&jr.SourceType, &jr.TotalDebit, &jr.TotalCredit, &jr.Status, &jr.CreatedAt,
		); err == nil {
			entries = append(entries, jr)
		}
	}

	response.Success(c, map[string]interface{}{
		"items": entries,
		"count": len(entries),
	})
}

// =====================================================================
// СВОД (consolidated estimate) report
// =====================================================================
//
// GET /construction/projects/:id/reports/svod
//
// Returns the per-building breakdown for the Uzbek "Сводная сметная"
// 12-line layout. Each building gets four cost rows split between PLAN
// and FAKT so the frontend can toggle between modes:
//
//   • R4 — installed equipment / furniture / inventory ("оборудование")
//   • R5 — labor wages ("з/плата рабочих")
//   • R6 — machine ops ("эксплуатация машин и механизмов")
//   • R7 — building materials ("строительные материалы")
//
// Rows 8 (direct subtotal), 9 (overhead %), 10 (insurance %), 11
// (subtotal), 12 (VAT), 13 (current price), 14 (PQ-161), 15 (grand
// total) are derived on the FRONTEND from the user-editable percentage
// inputs.
//
// =================
// Source of values
// =================
//
// In GenixERP each top-level work (parent_line_id IS NULL,
// resource_type = '') in a Единич estimate decomposes into
// sub-resources (parent_line_id > 0) tagged with resource_type
// ∈ {labor, equipment, material, ...}. Sub.total_amount is the
// per-unit-of-parent cost contribution × parent.quantity, i.e. the
// sub already encodes the parent's planned scale.
//
// PLAN per (building, resource_class):
//   sum(sub.total_amount) grouped by classify(sub.resource_type)
//
// FAKT per (building, resource_class):
//   sub.total_amount × (parent.done_quantity / parent.quantity)
//   When parent.quantity is 0 (Единич template-mode imports), we
//   fall back to original_quantity (the import-time anchor).
//   When parent.done_quantity is 0 (work not started), FAKT is 0.
//
// Resource type classification (case-insensitive, tolerant of
// Russian / Uzbek-Latin variants):
//   labor    = labor, mehnat, ish, ishchi, worker, трудовой
//   machine  = equipment, mashina, mexanizm, machinery
//   material = material, materialy, mat
//   else     = "equipment" bucket (R4 — installed equipment /
//              furniture / inventory). This rescues lines in
//              engineering-system estimates whose authors typed
//              non-standard resource_types like "оборудование".
//
// Reference: matches the structure of "Жилдом Саттепо Авеню Блок 1.xlsx"
// resource sheet's three subtotal rows (G14 labor, G69 machines, G296
// materials) — the same totals every Block file in that project ships.
func (h *Handler) GetSvodReport(c *gin.Context) {
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

	// Project header (name + address) for the modal title block.
	var projectName, projectAddress string
	if err := h.db.QueryRow(`
		SELECT COALESCE(name, ''), COALESCE(address, '')
		  FROM construction_projects
		 WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&projectName, &projectAddress); err != nil {
		h.log.Error("Failed to load project for svod report", "error", err)
		response.NotFound(c, "Project not found")
		return
	}

	// Walk every sub-resource (parent_line_id IS NOT NULL) of the
	// project's estimates, classify each line, and emit per-building
	// totals split between PLAN and FAKT.
	//
	// CRITICAL: classification uses BOTH resource_type AND uom.
	// Real Единич imports (like the reference "Жилдом Саттепо Авеню
	// Блок 1.xlsx") tag sub-resources only by UOM —
	//   ЧЕЛ.-Ч (man-hour)  → labor
	//   МАШ.-Ч (machine-hour) → machine ops
	//   anything else      → material
	// — and SmetaImportModal sometimes leaves resource_type blank.
	// Filtering by resource_type alone would drop most of the data
	// and skew the labor/material ratio (the bug the user observed
	// where Block 2 showed labor 735M vs material only 105M, when
	// the real Block 1 ratio is labor 2.7B vs material 7.8B).
	//
	// Source-type filtering removed too — `parent_line_id IS NOT NULL`
	// already restricts us to estimates with a parent/child hierarchy,
	// which is the Единич shape. Ресурс estimates are flat (no
	// children) so they're naturally excluded.
	//
	// LEFT JOIN on construction_buildings starts FROM all the project's
	// buildings so empty blocks still appear as 0-columns ready to be
	// filled in manually if the user wants — this matches the reference
	// xlsx where every block has a column whether or not data exists.
	rows, err := h.db.Query(`
		WITH classified AS (
		    SELECT
		        e.building_id AS bid,
		        CASE
		            -- Explicit resource_type wins.
		            WHEN LOWER(COALESCE(s.resource_type, '')) IN
		                 ('labor', 'mehnat', 'ish', 'ishchi', 'worker', 'трудовой', 'трудовые')
		                 THEN 'labor'
		            WHEN LOWER(COALESCE(s.resource_type, '')) IN
		                 ('equipment', 'mashina', 'masina', 'mexanizm', 'mexanizmlar', 'machinery', 'машина')
		                 THEN 'machine'
		            WHEN LOWER(COALESCE(s.resource_type, '')) IN
		                 ('material', 'materialy', 'mat', 'materiallar', 'материал', 'материалы')
		                 THEN 'material'
		            -- Fallback: classify by UOM. ЧЕЛ.-Ч / ЧЕЛ-Ч / ЧЕЛ Ч all
		            -- mean man-hours → labor. Anything with МАШ → machine.
		            -- Everything else (m3, шт, kg, t, ...) → material.
		            WHEN UPPER(COALESCE(s.uom, '')) LIKE '%ЧЕЛ%' THEN 'labor'
		            WHEN UPPER(COALESCE(s.uom, '')) LIKE '%МАШ%' THEN 'machine'
		            ELSE 'material'
		        END AS rc,
		        COALESCE(s.total_amount, 0) AS plan_amt,
		        CASE
		            WHEN COALESCE(p.done_quantity, 0) <= 0 THEN 0
		            WHEN COALESCE(p.quantity, 0) > 0 THEN
		                COALESCE(s.total_amount, 0) * (p.done_quantity::numeric / p.quantity)
		            WHEN COALESCE(p.original_quantity, 0) > 0 THEN
		                COALESCE(s.total_amount, 0) * (p.done_quantity::numeric / p.original_quantity)
		            ELSE COALESCE(s.total_amount, 0)
		        END AS fakt_amt
		    FROM construction_estimate_line s
		    JOIN construction_estimate_line p
		      ON p.id = s.parent_line_id
		     AND p.tenant_id = s.tenant_id
		    JOIN construction_estimate e
		      ON e.id = s.estimate_id
		     AND e.tenant_id = s.tenant_id
		    WHERE e.project_id  = $1
		      AND e.tenant_id   = $2
		      AND s.tenant_id   = $2
		      AND s.parent_line_id IS NOT NULL
		)
		SELECT
		    b.id                                                                AS building_id,
		    COALESCE(NULLIF(b.name, ''), b.code, 'Block #' || b.id::text)       AS building_name,
		    COALESCE(SUM(CASE WHEN c.rc = 'labor'    THEN c.plan_amt ELSE 0 END), 0) AS labor_plan,
		    COALESCE(SUM(CASE WHEN c.rc = 'machine'  THEN c.plan_amt ELSE 0 END), 0) AS machine_plan,
		    COALESCE(SUM(CASE WHEN c.rc = 'material' THEN c.plan_amt ELSE 0 END), 0) AS material_plan,
		    0                                                                   AS equipment_plan,
		    COALESCE(SUM(CASE WHEN c.rc = 'labor'    THEN c.fakt_amt ELSE 0 END), 0) AS labor_fakt,
		    COALESCE(SUM(CASE WHEN c.rc = 'machine'  THEN c.fakt_amt ELSE 0 END), 0) AS machine_fakt,
		    COALESCE(SUM(CASE WHEN c.rc = 'material' THEN c.fakt_amt ELSE 0 END), 0) AS material_fakt,
		    0                                                                   AS equipment_fakt
		FROM construction_buildings b
		LEFT JOIN classified c ON c.bid = b.id
		WHERE b.tenant_id = $2
		  AND b.project_id = $1
		GROUP BY b.id, b.name, b.code, b.sort_order
		ORDER BY b.sort_order ASC NULLS LAST, b.code ASC, b.id ASC
	`, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query svod report", "error", err)
		response.InternalError(c, "Failed to compute svod report")
		return
	}
	defer rows.Close()

	type buildingRow struct {
		ID             int64   `json:"id"`
		Name           string  `json:"name"`
		LaborPlan      float64 `json:"labor_plan"`
		MachinePlan    float64 `json:"machine_plan"`
		MaterialPlan   float64 `json:"material_plan"`
		EquipmentPlan  float64 `json:"equipment_plan"`
		LaborFakt      float64 `json:"labor_fakt"`
		MachineFakt    float64 `json:"machine_fakt"`
		MaterialFakt   float64 `json:"material_fakt"`
		EquipmentFakt  float64 `json:"equipment_fakt"`
	}
	var buildings []buildingRow
	for rows.Next() {
		var br buildingRow
		if err := rows.Scan(
			&br.ID, &br.Name,
			&br.LaborPlan, &br.MachinePlan, &br.MaterialPlan, &br.EquipmentPlan,
			&br.LaborFakt, &br.MachineFakt, &br.MaterialFakt, &br.EquipmentFakt,
		); err != nil {
			h.log.Error("Failed to scan svod row", "error", err)
			continue
		}
		if br.ID == 0 && br.Name == "" {
			br.Name = "Umumiy"
		}
		buildings = append(buildings, br)
	}

	response.Success(c, gin.H{
		"project": gin.H{
			"id":      projectID,
			"name":    projectName,
			"address": projectAddress,
		},
		"buildings": buildings,
	})
}

// =====================================================================
// Material consolidation report
// =====================================================================
//
// GET /construction/projects/:id/reports/material-consolidation
//
// Aggregates all MATERIAL sub-resources (resource_type = 'material', or
// any non-labor / non-machine resource by UOM) of the project's
// estimates, in **Fakt mode** — i.e. each line's contribution is scaled
// by its parent work's done_quantity / quantity ratio.
//
// Aggregation key
// ───────────────
//   (building_id, name (case-insensitive), uom, unit_rate)
//
// So two lines with the same name + uom but DIFFERENT unit_rate
// produce two separate output rows, each with its own consumed
// quantity. Same name + same uom + same unit_rate → one combined row
// with the summed quantity. This matches the user's requirement:
// "if a material's price is different, show separately based on volume;
// if the same, sum them up".
//
// Resource topups (migration 358 — purchases that came in at a new
// price after the line was already in the smeta) are returned as a
// nested list under each parent group. They're emitted under the group
// of the line they were attached to so the UI can render them as
// indented sub-rows.
//
// Output shape
// ────────────
//
//   {
//     project: {id, name, address},
//     blocks: [
//       {
//         id, name,
//         groups: [{
//             name, uom, unit_rate,
//             fakt_quantity, fakt_amount,
//             topups: [{extra_quantity, new_price, amount, ordered_at, note}]
//         }],
//         total_amount
//       }
//     ],
//     total: {
//       groups: [...same shape, aggregated across blocks],
//       total_amount
//     }
//   }
//
// "Block #0" is reserved for lines whose estimate has no building_id —
// the modal renders these under "Umumiy" so the user can see them.
func (h *Handler) GetMaterialConsolidationReport(c *gin.Context) {
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

	// Project header
	var projectName, projectAddress string
	if err := h.db.QueryRow(`
		SELECT COALESCE(name, ''), COALESCE(address, '')
		  FROM construction_projects
		 WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&projectName, &projectAddress); err != nil {
		h.log.Error("Failed to load project for material report", "error", err)
		response.NotFound(c, "Project not found")
		return
	}

	// Per-building × (name, uom, unit_rate) Fakt aggregation.
	//
	// `is_material` selector matches the same fallback logic Свод uses:
	//   resource_type explicitly tagged 'material' wins; otherwise the
	//   UOM excludes labor (ЧЕЛ) and machine (МАШ) and everything else
	//   counts as material. This keeps legacy imports (where
	//   resource_type was left blank) from being mis-classified.
	//
	// `parent_ratio` is parent.done_quantity / parent.quantity (or
	// /original_quantity as fallback) — same shape as the Свод query.
	rows, err := h.db.Query(`
		WITH material_lines AS (
		    SELECT
		        e.building_id                     AS bid,
		        s.name                            AS name,
		        COALESCE(s.uom, '')               AS uom,
		        COALESCE(s.unit_rate, 0)          AS unit_rate,
		        s.id                              AS line_id,
		        CASE
		            WHEN COALESCE(p.done_quantity, 0) <= 0 THEN 0
		            WHEN COALESCE(p.quantity, 0) > 0 THEN
		                COALESCE(s.quantity, 0) * (p.done_quantity::numeric / p.quantity)
		            WHEN COALESCE(p.original_quantity, 0) > 0 THEN
		                COALESCE(s.quantity, 0) * (p.done_quantity::numeric / p.original_quantity)
		            ELSE COALESCE(s.quantity, 0)
		        END                               AS fakt_quantity,
		        -- Name of the subcontractor assigned to this material's parent
		        -- work (matched by section+name+uom), NULL when in-house only.
		        (SELECT COALESCE(NULLIF(sc.partner_name, ''), sc.name)
		           FROM construction_estimate_line sl
		           JOIN construction_estimate se ON se.id = sl.estimate_id AND se.tenant_id = s.tenant_id
		           JOIN construction_subcontract sc ON sc.id = se.subcontract_id AND sc.tenant_id = s.tenant_id
		          WHERE se.project_id = e.project_id
		            AND se.subcontract_id IS NOT NULL
		            AND LOWER(COALESCE(se.source_type, '')) = 'edinich'
		            AND se.building_id IS NOT DISTINCT FROM e.building_id
		            AND sl.parent_line_id IS NULL AND COALESCE(sl.resource_type, '') = ''
		            AND TRIM(COALESCE(sl.parent_item_number, '')) = TRIM(COALESCE(p.parent_item_number, ''))
		            AND LOWER(TRIM(COALESCE(sl.name, ''))) = LOWER(TRIM(COALESCE(p.name, '')))
		            AND TRIM(COALESCE(sl.uom, '')) = TRIM(COALESCE(p.uom, ''))
		          LIMIT 1)                        AS subcontractor
		    FROM construction_estimate_line s
		    JOIN construction_estimate_line p
		      ON p.id = s.parent_line_id
		     AND p.tenant_id = s.tenant_id
		    JOIN construction_estimate e
		      ON e.id = s.estimate_id
		     AND e.tenant_id = s.tenant_id
		    WHERE e.project_id  = $1
		      AND e.tenant_id   = $2
		      AND s.tenant_id   = $2
		      -- In-house master estimate only; subcontractor copies are mirrored
		      -- onto it via the FAKT sync, so including them would double-count.
		      AND e.subcontract_id IS NULL
		      AND s.parent_line_id IS NOT NULL
		      AND (
		          LOWER(COALESCE(s.resource_type, '')) IN ('material', 'materialy', 'mat', 'materiallar', 'материал', 'материалы')
		          OR (
		              UPPER(COALESCE(s.uom, '')) NOT LIKE '%ЧЕЛ%'
		              AND UPPER(COALESCE(s.uom, '')) NOT LIKE '%МАШ%'
		              AND LOWER(COALESCE(s.resource_type, '')) NOT IN
		                  ('labor', 'mehnat', 'ish', 'ishchi', 'worker',
		                   'equipment', 'mashina', 'masina', 'mexanizm', 'mexanizmlar', 'machinery',
		                   'трудовой', 'трудовые', 'машина')
		          )
		      )
		)
		SELECT
		    COALESCE(ml.bid, 0)                 AS building_id,
		    COALESCE(b.name, b.code, 'Umumiy')  AS building_name,
		    ml.name                             AS name,
		    ml.uom                              AS uom,
		    ml.unit_rate                        AS unit_rate,
		    SUM(ml.fakt_quantity)               AS fakt_quantity,
		    ARRAY_AGG(ml.line_id)               AS line_ids,
		    COALESCE(STRING_AGG(DISTINCT ml.subcontractor, ', ')
		             FILTER (WHERE ml.subcontractor IS NOT NULL AND ml.subcontractor <> ''), '') AS subcontractors
		FROM material_lines ml
		LEFT JOIN construction_buildings b ON b.id = ml.bid
		GROUP BY ml.bid, b.id, b.name, b.code, b.sort_order, ml.name, ml.uom, ml.unit_rate
		-- Drop materials that haven't actually been consumed. The
		-- catalog often contains many resources at planned prices
		-- that the foreman never used (zero done_quantity → zero
		-- fakt_quantity), so they were padding the report with empty
		-- rows of БЕНЗИН РАСТВОРИТЕЛЬ / БЛОКИ ДВЕРНЫЕ etc. at qty 0
		-- that distract from the actual consumption picture.
		-- Topups attached to those lines are still loaded below, but
		-- only matter when the parent group has a non-zero base spend
		-- — a pure topup-only material is rare and can be added back
		-- here later if the user wants it.
		HAVING SUM(ml.fakt_quantity) > 0
		ORDER BY b.sort_order ASC NULLS LAST, b.id ASC NULLS LAST,
		         UPPER(ml.name) ASC, ml.uom ASC, ml.unit_rate ASC
	`, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query material consolidation", "error", err)
		response.InternalError(c, "Failed to compute material report")
		return
	}
	defer rows.Close()

	type topup struct {
		ExtraQuantity float64 `json:"extra_quantity"`
		NewPrice      float64 `json:"new_price"`
		Amount        float64 `json:"amount"`
		OrderedAt     string  `json:"ordered_at"`
		Note          string  `json:"note"`
	}
	type group struct {
		Name          string  `json:"name"`
		UOM           string  `json:"uom"`
		UnitRate      float64 `json:"unit_rate"`
		FaktQuantity  float64 `json:"fakt_quantity"`
		FaktAmount    float64 `json:"fakt_amount"`
		Subcontractor string  `json:"subcontractor"`
		Topups        []topup `json:"topups"`
		LineIDs       []int64 `json:"-"`
	}
	type block struct {
		ID          int64   `json:"id"`
		Name        string  `json:"name"`
		Groups      []group `json:"groups"`
		TotalAmount float64 `json:"total_amount"`
	}

	// Bucket groups by building_id in iteration order (preserved by
	// ORDER BY in the SQL above).
	blockOrder := []int64{}
	blockMap := map[int64]*block{}
	allLineIDs := []int64{}

	for rows.Next() {
		var bid int64
		var bname, name, uom, subcontractor string
		var rate, qty float64
		var lineIDs pq.Int64Array
		if err := rows.Scan(&bid, &bname, &name, &uom, &rate, &qty, &lineIDs, &subcontractor); err != nil {
			h.log.Error("Failed to scan material row", "error", err)
			continue
		}
		blk, ok := blockMap[bid]
		if !ok {
			b := &block{ID: bid, Name: bname}
			blockMap[bid] = b
			blockOrder = append(blockOrder, bid)
			blk = b
		}
		g := group{
			Name:          name,
			UOM:           uom,
			UnitRate:      rate,
			FaktQuantity:  qty,
			FaktAmount:    qty * rate,
			Subcontractor: subcontractor,
			Topups:        []topup{},
			LineIDs:       []int64(lineIDs),
		}
		blk.Groups = append(blk.Groups, g)
		blk.TotalAmount += g.FaktAmount
		allLineIDs = append(allLineIDs, []int64(lineIDs)...)
	}

	// Bulk-load topups attached to any line we just emitted, then
	// distribute them into their parent groups. Each topup's amount
	// is extra_quantity × new_price (independent of the line's base
	// unit_rate — that's the whole point of a top-up).
	topupsByLine := map[int64][]topup{}
	if len(allLineIDs) > 0 {
		topupRows, terr := h.db.Query(`
			SELECT estimate_line_id, COALESCE(extra_quantity, 0), COALESCE(new_price, 0),
			       COALESCE(ordered_at::text, ''), COALESCE(note, '')
			FROM construction_resource_topup
			WHERE tenant_id = $1
			  AND estimate_line_id = ANY($2::bigint[])
			ORDER BY ordered_at ASC, id ASC
		`, tenantID, pq.Array(allLineIDs))
		if terr == nil {
			defer topupRows.Close()
			for topupRows.Next() {
				var lineID int64
				var t topup
				if scanErr := topupRows.Scan(
					&lineID, &t.ExtraQuantity, &t.NewPrice, &t.OrderedAt, &t.Note,
				); scanErr != nil {
					continue
				}
				t.Amount = t.ExtraQuantity * t.NewPrice
				topupsByLine[lineID] = append(topupsByLine[lineID], t)
			}
		} else {
			h.log.Error("Failed to load resource topups for material report", "error", terr)
		}
	}

	// Attach topups to their groups (one topup may belong to one of
	// several lines that fold into the same group when the lines share
	// name/uom/unit_rate). Sum into block totals.
	for _, blk := range blockMap {
		for i := range blk.Groups {
			for _, lid := range blk.Groups[i].LineIDs {
				if ts, ok := topupsByLine[lid]; ok {
					blk.Groups[i].Topups = append(blk.Groups[i].Topups, ts...)
					for _, tp := range ts {
						blk.TotalAmount += tp.Amount
					}
				}
			}
		}
	}

	// Build the project-wide "Total" pseudo-block by re-aggregating
	// across blocks on the same (name, uom, unit_rate) key.
	type aggKey struct {
		name string
		uom  string
		rate float64
	}
	totalAgg := map[aggKey]*group{}
	totalOrder := []aggKey{}
	var grandTotal float64
	for _, blk := range blockMap {
		for _, g := range blk.Groups {
			k := aggKey{name: strings.ToUpper(g.Name), uom: g.UOM, rate: g.UnitRate}
			tg, ok := totalAgg[k]
			if !ok {
				ng := group{
					Name: g.Name, UOM: g.UOM, UnitRate: g.UnitRate,
					Subcontractor: g.Subcontractor,
					Topups:        []topup{},
				}
				totalAgg[k] = &ng
				tg = &ng
				totalOrder = append(totalOrder, k)
			}
			if tg.Subcontractor == "" {
				tg.Subcontractor = g.Subcontractor
			}
			tg.FaktQuantity += g.FaktQuantity
			tg.FaktAmount += g.FaktAmount
			tg.Topups = append(tg.Topups, g.Topups...)
			grandTotal += g.FaktAmount
			for _, tp := range g.Topups {
				grandTotal += tp.Amount
			}
		}
	}
	totalGroups := make([]group, 0, len(totalOrder))
	for _, k := range totalOrder {
		totalGroups = append(totalGroups, *totalAgg[k])
	}

	// Materialise blocks in deterministic order.
	out := make([]*block, 0, len(blockOrder))
	for _, bid := range blockOrder {
		out = append(out, blockMap[bid])
	}

	response.Success(c, gin.H{
		"project": gin.H{
			"id":      projectID,
			"name":    projectName,
			"address": projectAddress,
		},
		"blocks": out,
		"total": gin.H{
			"groups":       totalGroups,
			"total_amount": grandTotal,
		},
	})
}

// GetResourceConsolidationReport aggregates the NORMA (planned normative)
// quantities of resources — materials, equipment(machinery) and labor — across
// every estimate section, per block, for a project. Unlike the material
// consolidation (which is Fakt / done-scaled and materials-only), this report
// uses each resource sub-line's PLANNED quantity (parent_qty × norm) and tags
// every group with its resource `type` so the frontend can let the user filter
// by material / equipment / labor.
//
// GET /construction/projects/:id/reports/resource-consolidation
func (h *Handler) GetResourceConsolidationReport(c *gin.Context) {
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

	var projectName, projectAddress string
	if err := h.db.QueryRow(`
		SELECT COALESCE(name, ''), COALESCE(address, '')
		  FROM construction_projects
		 WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&projectName, &projectAddress); err != nil {
		h.log.Error("Failed to load project for resource report", "error", err)
		response.NotFound(c, "Project not found")
		return
	}

	// Classify each resource sub-line into material / equipment / labor with
	// the same precedence Свод uses (explicit resource_type wins; otherwise
	// fall back to the UOM: ЧЕЛ → labor, МАШ → equipment, else material).
	// NORMA quantity is the line's own planned quantity (parent_qty × norm).
	rows, err := h.db.Query(`
		WITH resource_lines AS (
		    -- (1) Ресурс (resource) estimate — the authoritative resource norms,
		    --     stored as flat resource lines with the file's Количество in
		    --     imported_quantity. This is what the user sees on the Smetalar
		    --     Ресурс tab (e.g. ЗАТРАТЫ ТРУДА = 83 411 ЧЕЛ-Ч).
		    SELECT
		        e.building_id                     AS bid,
		        CASE
		            WHEN LOWER(COALESCE(l.resource_type, '')) IN
		                 ('labor','mehnat','ish','ishchi','worker','трудовой','трудовые') THEN 'labor'
		            WHEN UPPER(COALESCE(l.uom, '')) LIKE '%ЧЕЛ%' THEN 'labor'
		            WHEN LOWER(COALESCE(l.resource_type, '')) IN
		                 ('equipment','mashina','masina','mexanizm','mexanizmlar','machinery','машина') THEN 'equipment'
		            WHEN UPPER(COALESCE(l.uom, '')) LIKE '%МАШ%' THEN 'equipment'
		            -- Material family — split by material_type so Кабель / Оборудование
		            -- match the Excel's separate subtotals.
		            WHEN LOWER(COALESCE(l.material_type, '')) = 'cable' THEN 'cable'
		            WHEN LOWER(COALESCE(l.material_type, '')) = 'equipment' THEN 'installed'
		            ELSE 'material'
		        END                               AS rtype,
		        l.name                            AS name,
		        COALESCE(l.uom, '')               AS uom,
		        COALESCE(NULLIF(l.unit_rate, 0),
		                 COALESCE(l.material_rate,0) + COALESCE(l.labor_rate,0) + COALESCE(l.equipment_rate,0)) AS unit_rate,
		        COALESCE(NULLIF(l.imported_quantity, 0), NULLIF(l.original_quantity, 0), l.quantity, 0) AS norma_quantity,
		        -- Amount = the file's own Сумма (с транспортом) when present, so
		        -- the report matches the Excel exactly instead of re-deriving
		        -- qty × price (which omits transport/coefficients).
		        COALESCE(NULLIF(l.imported_total, 0),
		                 COALESCE(NULLIF(l.imported_quantity, 0), NULLIF(l.original_quantity, 0), l.quantity, 0)
		                 * COALESCE(NULLIF(l.unit_rate, 0),
		                            COALESCE(l.material_rate,0) + COALESCE(l.labor_rate,0) + COALESCE(l.equipment_rate,0))) AS norma_amount,
		        NULL::text                        AS subcontractor
		    FROM construction_estimate_line l
		    JOIN construction_estimate e ON e.id = l.estimate_id AND e.tenant_id = l.tenant_id
		    WHERE e.project_id = $1 AND e.tenant_id = $2 AND l.tenant_id = $2
		      AND e.subcontract_id IS NULL
		      AND LOWER(COALESCE(e.source_type, '')) = 'resurs'
		      AND COALESCE(l.resource_type, '') <> ''
		      -- Type/unit consistency: a machine MUST be МАШ-Ч and labor ЧЕЛ-Ч.
		      -- Lines tagged machine/labor with a work-volume unit (e.g. 1000М2)
		      -- are the "parent unit leaked onto the resource" anomaly — drop them.
		      AND NOT (LOWER(COALESCE(l.resource_type, '')) IN
		               ('equipment','mashina','masina','mexanizm','mexanizmlar','machinery','машина')
		               AND UPPER(COALESCE(l.uom, '')) NOT LIKE '%МАШ%')
		      AND NOT (LOWER(COALESCE(l.resource_type, '')) IN
		               ('labor','mehnat','ish','ishchi','worker','трудовой','трудовые')
		               AND UPPER(COALESCE(l.uom, '')) NOT LIKE '%ЧЕЛ%')

		    UNION ALL

		    -- (2) Едиinич sub-line resources (norm_rate × parent reja qty) — used
		    --     ONLY for buildings that have NO Ресурс estimate, so the two
		    --     representations never double-count.
		    SELECT
		        e.building_id                     AS bid,
		        CASE
		            WHEN LOWER(COALESCE(s.resource_type, '')) IN
		                 ('labor','mehnat','ish','ishchi','worker','трудовой','трудовые') THEN 'labor'
		            WHEN UPPER(COALESCE(s.uom, '')) LIKE '%ЧЕЛ%' THEN 'labor'
		            WHEN LOWER(COALESCE(s.resource_type, '')) IN
		                 ('equipment','mashina','masina','mexanizm','mexanizmlar','machinery','машина') THEN 'equipment'
		            WHEN UPPER(COALESCE(s.uom, '')) LIKE '%МАШ%' THEN 'equipment'
		            WHEN LOWER(COALESCE(s.material_type, '')) = 'cable' THEN 'cable'
		            WHEN LOWER(COALESCE(s.material_type, '')) = 'equipment' THEN 'installed'
		            ELSE 'material'
		        END                               AS rtype,
		        s.name                            AS name,
		        COALESCE(s.uom, '')               AS uom,
		        COALESCE(s.unit_rate, 0)          AS unit_rate,
		        CASE
		            WHEN COALESCE(s.quantity_override, false)
		                THEN COALESCE(NULLIF(s.original_quantity, 0), s.quantity, 0)
		            ELSE COALESCE(s.norm_rate, 0)
		                 * COALESCE(NULLIF(p.original_quantity, 0), p.quantity, 0)
		        END                               AS norma_quantity,
		        (CASE
		            WHEN COALESCE(s.quantity_override, false)
		                THEN COALESCE(NULLIF(s.original_quantity, 0), s.quantity, 0)
		            ELSE COALESCE(s.norm_rate, 0)
		                 * COALESCE(NULLIF(p.original_quantity, 0), p.quantity, 0)
		        END) * COALESCE(s.unit_rate, 0)   AS norma_amount,
		        (SELECT COALESCE(NULLIF(sc.partner_name, ''), sc.name)
		           FROM construction_estimate_line sl
		           JOIN construction_estimate se ON se.id = sl.estimate_id AND se.tenant_id = s.tenant_id
		           JOIN construction_subcontract sc ON sc.id = se.subcontract_id AND sc.tenant_id = s.tenant_id
		          WHERE se.project_id = e.project_id
		            AND se.subcontract_id IS NOT NULL
		            AND LOWER(COALESCE(se.source_type, '')) = 'edinich'
		            AND se.building_id IS NOT DISTINCT FROM e.building_id
		            AND sl.parent_line_id IS NULL AND COALESCE(sl.resource_type, '') = ''
		            AND TRIM(COALESCE(sl.parent_item_number, '')) = TRIM(COALESCE(p.parent_item_number, ''))
		            AND LOWER(TRIM(COALESCE(sl.name, ''))) = LOWER(TRIM(COALESCE(p.name, '')))
		            AND TRIM(COALESCE(sl.uom, '')) = TRIM(COALESCE(p.uom, ''))
		          LIMIT 1)                        AS subcontractor
		    FROM construction_estimate_line s
		    JOIN construction_estimate_line p
		      ON p.id = s.parent_line_id
		     AND p.tenant_id = s.tenant_id
		    JOIN construction_estimate e
		      ON e.id = s.estimate_id
		     AND e.tenant_id = s.tenant_id
		    WHERE e.project_id  = $1
		      AND e.tenant_id   = $2
		      AND s.tenant_id   = $2
		      AND e.subcontract_id IS NULL
		      AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
		      AND s.parent_line_id IS NOT NULL
		      -- Same type/unit consistency guard as the Ресурс branch: drop
		      -- machine/labor resources whose unit isn't МАШ-Ч / ЧЕЛ-Ч (parent
		      -- work unit leaked onto the resource, e.g. 1000М2 graders).
		      AND NOT (LOWER(COALESCE(s.resource_type, '')) IN
		               ('equipment','mashina','masina','mexanizm','mexanizmlar','machinery','машина')
		               AND UPPER(COALESCE(s.uom, '')) NOT LIKE '%МАШ%')
		      AND NOT (LOWER(COALESCE(s.resource_type, '')) IN
		               ('labor','mehnat','ish','ishchi','worker','трудовой','трудовые')
		               AND UPPER(COALESCE(s.uom, '')) NOT LIKE '%ЧЕЛ%')
		      AND NOT EXISTS (
		          SELECT 1 FROM construction_estimate re
		          WHERE re.project_id = e.project_id AND re.tenant_id = e.tenant_id
		            AND re.subcontract_id IS NULL
		            AND LOWER(COALESCE(re.source_type, '')) = 'resurs'
		            AND re.building_id IS NOT DISTINCT FROM e.building_id
		      )
		)
		SELECT
		    COALESCE(rl.bid, 0)                 AS building_id,
		    COALESCE(b.name, b.code, 'Umumiy')  AS building_name,
		    rl.rtype                            AS rtype,
		    rl.name                             AS name,
		    rl.uom                              AS uom,
		    rl.unit_rate                        AS unit_rate,
		    SUM(rl.norma_quantity)              AS norma_quantity,
		    SUM(rl.norma_amount)                AS norma_amount,
		    COALESCE(STRING_AGG(DISTINCT rl.subcontractor, ', ')
		             FILTER (WHERE rl.subcontractor IS NOT NULL AND rl.subcontractor <> ''), '') AS subcontractors
		FROM resource_lines rl
		LEFT JOIN construction_buildings b ON b.id = rl.bid
		GROUP BY rl.bid, b.id, b.name, b.code, b.sort_order, rl.rtype, rl.name, rl.uom, rl.unit_rate
		HAVING SUM(rl.norma_quantity) > 0
		ORDER BY b.sort_order ASC NULLS LAST, b.id ASC NULLS LAST,
		         rl.rtype ASC, UPPER(rl.name) ASC, rl.uom ASC, rl.unit_rate ASC
	`, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query resource consolidation", "error", err)
		response.InternalError(c, "Failed to compute resource report")
		return
	}
	defer rows.Close()

	type group struct {
		Type          string  `json:"type"`
		Name          string  `json:"name"`
		UOM           string  `json:"uom"`
		UnitRate      float64 `json:"unit_rate"`
		NormaQuantity float64 `json:"norma_quantity"`
		NormaAmount   float64 `json:"norma_amount"`
		Subcontractor string  `json:"subcontractor"`
	}
	type block struct {
		ID          int64   `json:"id"`
		Name        string  `json:"name"`
		Groups      []group `json:"groups"`
		TotalAmount float64 `json:"total_amount"`
	}

	blockOrder := []int64{}
	blockMap := map[int64]*block{}

	for rows.Next() {
		var bid int64
		var bname, rtype, name, uom, subcontractor string
		var rate, qty, amount float64
		if err := rows.Scan(&bid, &bname, &rtype, &name, &uom, &rate, &qty, &amount, &subcontractor); err != nil {
			h.log.Error("Failed to scan resource row", "error", err)
			continue
		}
		blk, ok := blockMap[bid]
		if !ok {
			b := &block{ID: bid, Name: bname}
			blockMap[bid] = b
			blockOrder = append(blockOrder, bid)
			blk = b
		}
		g := group{
			Type:          rtype,
			Name:          name,
			UOM:           uom,
			UnitRate:      rate,
			NormaQuantity: qty,
			NormaAmount:   amount, // file's Сумма (с транспортом) — matches Excel
			Subcontractor: subcontractor,
		}
		blk.Groups = append(blk.Groups, g)
		blk.TotalAmount += g.NormaAmount
	}

	// Project-wide total: re-aggregate across blocks on (type, name, uom, rate).
	type aggKey struct {
		rtype string
		name  string
		uom   string
		rate  float64
	}
	totalAgg := map[aggKey]*group{}
	totalOrder := []aggKey{}
	var grandTotal float64
	for _, blk := range blockMap {
		for _, g := range blk.Groups {
			k := aggKey{rtype: g.Type, name: strings.ToUpper(g.Name), uom: g.UOM, rate: g.UnitRate}
			tg, ok := totalAgg[k]
			if !ok {
				ng := group{Type: g.Type, Name: g.Name, UOM: g.UOM, UnitRate: g.UnitRate, Subcontractor: g.Subcontractor}
				totalAgg[k] = &ng
				tg = &ng
				totalOrder = append(totalOrder, k)
			}
			if tg.Subcontractor == "" {
				tg.Subcontractor = g.Subcontractor
			}
			tg.NormaQuantity += g.NormaQuantity
			tg.NormaAmount += g.NormaAmount
			grandTotal += g.NormaAmount
		}
	}
	totalGroups := make([]group, 0, len(totalOrder))
	for _, k := range totalOrder {
		totalGroups = append(totalGroups, *totalAgg[k])
	}

	out := make([]*block, 0, len(blockOrder))
	for _, bid := range blockOrder {
		out = append(out, blockMap[bid])
	}

	response.Success(c, gin.H{
		"project": gin.H{
			"id":      projectID,
			"name":    projectName,
			"address": projectAddress,
		},
		"blocks": out,
		"total": gin.H{
			"groups":       totalGroups,
			"total_amount": grandTotal,
		},
	})
}
