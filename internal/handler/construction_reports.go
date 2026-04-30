package handler

import (
	"strconv"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
		    SELECT id, stage_order
		    FROM construction_stages
		    WHERE tenant_id  = pc.tenant_id
		      AND project_id = pc.project_id
		      AND name       = pc.section_path
		    ORDER BY id ASC
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
