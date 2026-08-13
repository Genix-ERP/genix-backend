package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"sort"
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
// CONSTRUCTION STAGES HANDLERS
// =====================================================

// ListConstructionStages returns all stages for a project
func (h *Handler) ListConstructionStages(c *gin.Context) {
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

	// Optional building filter (migration 333). Accepts:
	//   ?building_id=<id>    - stages assigned to that building
	//   ?building_id=0       - project-wide stages (no building)
	// Omit the param to get every stage (used by the "Hammasi" tab).
	var buildingFilter sql.NullInt64
	if raw := c.Query("building_id"); raw != "" {
		if v, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
			buildingFilter = sql.NullInt64{Int64: v, Valid: true}
		}
	}

	args := []interface{}{projectID, tenantID}
	buildingClause := ""
	if buildingFilter.Valid {
		if buildingFilter.Int64 == 0 {
			buildingClause = " AND s.building_id IS NULL"
		} else {
			args = append(args, buildingFilter.Int64)
			buildingClause = " AND s.building_id = $3"
		}
	}

	// Optional pagination (opt-in). Mobile always sends page+limit to avoid
	// loading thousands of stages (and the per-stage N+1) at once. If NEITHER
	// page nor limit is present we return every stage exactly like before, so
	// the web client (which sends only building_id) is unaffected.
	pageStr := c.Query("page")
	limitStr := c.Query("limit")
	paginate := pageStr != "" || limitStr != ""

	page := 1
	limit := 20
	if paginate {
		if v, e := strconv.Atoi(pageStr); e == nil && v > 0 {
			page = v
		}
		if v, e := strconv.Atoi(limitStr); e == nil {
			if v < 1 {
				v = 20
			} else if v > 100 {
				v = 100
			}
			limit = v
		}
	}
	offset := (page - 1) * limit

	// §1b — hide auto-import "ghost" stages. finaliseMaterialsForWork
	// (construction_work_material_engine.go) auto-creates a stage named after a
	// work's parent_item_number with status='pending'; when that section is
	// later renamed/re-imported the row is orphaned (no matching works) and
	// leaks into Bosqichlar as an empty card. We exclude a stage ONLY when it is
	// such an auto-created orphan: status='pending' AND no sub-stages AND no
	// estimate-line works whose section maps to it (exact name OR a "name › …"
	// nested sub-section, › = U+203A — the same join reja-fakt uses). Gating on
	// status='pending' guarantees we never hide a user-added empty stage (those
	// default to 'not_started'), which is exactly the failure mode §1b warns of.
	// A 'pending' stage that still has works/sub-stages is kept.
	ghostFilter := `
		AND NOT (
			s.status = 'pending'
			AND NOT EXISTS (
				SELECT 1 FROM construction_sub_stages gss
				WHERE gss.stage_id = s.id AND gss.tenant_id = s.tenant_id
			)
			AND NOT EXISTS (
				SELECT 1 FROM construction_estimate_line gel
				JOIN construction_estimate ge ON ge.id = gel.estimate_id AND ge.tenant_id = gel.tenant_id
				WHERE ge.project_id = s.project_id
				  AND gel.tenant_id = s.tenant_id
				  AND COALESCE(gel.resource_type, '') = ''
				  AND COALESCE(gel.parent_line_id, 0) = 0
				  AND s.name = ANY(string_to_array(gel.parent_item_number, ' › '))
			)
		)`

	query := `
		SELECT s.id, s.tenant_id, s.project_id, s.building_id, s.name, s.stage_order,
		       s.status, s.planned_budget, s.planned_start, s.planned_end,
		       s.actual_start, s.actual_end, s.notes, s.created_at, s.updated_at,
		       b.name AS building_name,
		       COALESCE(SUM(CASE WHEN el.status = 'approved' AND el.deleted_at IS NULL THEN el.amount ELSE 0 END), 0) AS actual_amount,
		       COALESCE((SELECT SUM(m.total_cost) FROM construction_sub_stage_materials m
		                 JOIN construction_sub_stages ss ON ss.id = m.sub_stage_id
		                 WHERE ss.stage_id = s.id), 0) AS material_total,
		       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq
		                 JOIN construction_sub_stages ss ON ss.id = eq.sub_stage_id
		                 WHERE ss.stage_id = s.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0) AS equipment_total,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq
		                 JOIN construction_sub_stages ss ON ss.id = eq.sub_stage_id
		                 WHERE ss.stage_id = s.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0) AS equipment_count,
		       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq
		                 JOIN construction_sub_stages ss ON ss.id = eq.sub_stage_id
		                 WHERE ss.stage_id = s.id AND eq.resource_type = 'employee'), 0) AS labor_total,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq
		                 JOIN construction_sub_stages ss ON ss.id = eq.sub_stage_id
		                 WHERE ss.stage_id = s.id AND eq.resource_type = 'employee'), 0) AS labor_count
		FROM construction_stages s
		LEFT JOIN construction_expense_lines el ON el.stage_id = s.id AND el.deleted_at IS NULL
		LEFT JOIN construction_buildings b ON b.id = s.building_id
		WHERE s.project_id = $1 AND s.tenant_id = $2` + buildingClause + ghostFilter + `
		GROUP BY s.id, b.name
		ORDER BY s.stage_order ASC, s.id ASC
	`

	// Page the list only when paginating. args already holds
	// projectID, tenantID, [buildingID], so the placeholders continue correctly.
	if paginate {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		args = append(args, limit, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list stages", "error", err)
		response.InternalError(c, "Failed to list stages")
		return
	}
	defer rows.Close()

	// SubStage mirrors the shape returned by GET /construction/stages/{id}/sub-stages
	// (same JSON field names) so mobile can read embedded sub-stages without an
	// extra request per stage.
	type SubStage struct {
		ID             int64   `json:"id"`
		StageID        int64   `json:"stage_id"`
		Name           string  `json:"name"`
		Status         string  `json:"status"`
		MaterialCount  int     `json:"material_count"`
		MaterialTotal  float64 `json:"material_total"`
		EquipmentCount int     `json:"equipment_count"`
		EquipmentTotal float64 `json:"equipment_total"`
		LaborCount     int     `json:"labor_count"`
		LaborTotal     float64 `json:"labor_total"`
	}

	type Stage struct {
		ID           int64   `json:"id"`
		TenantID     string  `json:"tenant_id"`
		ProjectID    int64   `json:"project_id"`
		BuildingID   *int64  `json:"building_id"`
		BuildingName *string `json:"building_name"`
		Name         string  `json:"name"`
		// SectionKey is the estimate-line parent_item_number this stage groups.
		// Mobile uses it to map a Bosqichlar stage to its works. stage.name is
		// set by SmetaImportModal to the section leaf the backend already matches
		// works against (see construction_reja_fakt.go), so for the common,
		// non-prefixed data it equals parent_item_number. Mobile should prefer
		// the dedicated GET /construction/stages/{id}/works endpoint, which does
		// the leaf match server-side and is robust to "СЕКЦИЯ №N ›" prefixes.
		SectionKey     string     `json:"section_key"`
		StageOrder     int        `json:"stage_order"`
		Status         string     `json:"status"`
		PlannedBudget  float64    `json:"planned_budget"`
		PlannedStart   *string    `json:"planned_start"`
		PlannedEnd     *string    `json:"planned_end"`
		ActualStart    *string    `json:"actual_start"`
		ActualEnd      *string    `json:"actual_end"`
		Notes          *string    `json:"notes"`
		CreatedAt      time.Time  `json:"created_at"`
		UpdatedAt      time.Time  `json:"updated_at"`
		ActualAmount   float64    `json:"actual_amount"`
		MaterialTotal  float64    `json:"material_total"`
		EquipmentTotal float64    `json:"equipment_total"`
		EquipmentCount int        `json:"equipment_count"`
		LaborTotal     float64    `json:"labor_total"`
		LaborCount     int        `json:"labor_count"`
		SubStages      []SubStage `json:"sub_stages"`
	}

	stages := []Stage{}
	for rows.Next() {
		var s Stage
		var bID sql.NullInt64
		var bName sql.NullString
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.ProjectID, &bID, &s.Name, &s.StageOrder,
			&s.Status, &s.PlannedBudget, &s.PlannedStart, &s.PlannedEnd,
			&s.ActualStart, &s.ActualEnd, &s.Notes, &s.CreatedAt, &s.UpdatedAt,
			&bName,
			&s.ActualAmount, &s.MaterialTotal,
			&s.EquipmentTotal, &s.EquipmentCount, &s.LaborTotal, &s.LaborCount,
		); err != nil {
			h.log.Error("Failed to scan stage", "error", err)
			continue
		}
		if bID.Valid {
			v := bID.Int64
			s.BuildingID = &v
		}
		if bName.Valid {
			v := bName.String
			s.BuildingName = &v
		}
		s.SectionKey = s.Name // section grouping key (= parent_item_number leaf)
		stages = append(stages, s)
	}

	// Embed sub-stages for the stages on this page in ONE query (no N+1).
	// The page holds at most `limit` (<=100) stages when paginating, or every
	// stage for the legacy web path; either way this is a single bounded query.
	stageIDs := make([]int64, 0, len(stages))
	for _, s := range stages {
		stageIDs = append(stageIDs, s.ID)
	}

	subsByStage := map[int64][]SubStage{}
	if len(stageIDs) > 0 {
		subRows, subErr := h.db.Query(`
			SELECT ss.id, ss.stage_id, ss.name, ss.status,
			       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_materials m WHERE m.sub_stage_id = ss.id), 0),
			       COALESCE((SELECT SUM(m.total_cost) FROM construction_sub_stage_materials m WHERE m.sub_stage_id = ss.id), 0),
			       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0),
			       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0),
			       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND eq.resource_type = 'employee'), 0),
			       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND eq.resource_type = 'employee'), 0)
			FROM construction_sub_stages ss
			WHERE ss.stage_id = ANY($1) AND ss.tenant_id = $2
			ORDER BY ss.sub_order ASC, ss.id ASC
		`, pq.Array(stageIDs), tenantID)
		if subErr != nil {
			h.log.Error("Failed to load sub-stages for stage list", "error", subErr)
		} else {
			defer subRows.Close()
			for subRows.Next() {
				var ss SubStage
				if scanErr := subRows.Scan(&ss.ID, &ss.StageID, &ss.Name, &ss.Status,
					&ss.MaterialCount, &ss.MaterialTotal,
					&ss.EquipmentCount, &ss.EquipmentTotal,
					&ss.LaborCount, &ss.LaborTotal); scanErr == nil {
					subsByStage[ss.StageID] = append(subsByStage[ss.StageID], ss)
				}
			}
		}
	}

	for i := range stages {
		if subs, ok := subsByStage[stages[i].ID]; ok {
			stages[i].SubStages = subs
		} else {
			stages[i].SubStages = []SubStage{}
		}
	}

	// Sof-prorab pul ko'rmaydi — bosqich byudjeti/fakt/resurs-summalari 0
	// bilan ketadi (conventions §4, server-side stripping).
	stagesPriceHidden := h.constructionPriceRestricted(c, tenantID, projectID)
	if stagesPriceHidden {
		for i := range stages {
			stages[i].PlannedBudget = 0
			stages[i].ActualAmount = 0
			stages[i].MaterialTotal = 0
			stages[i].EquipmentTotal = 0
			stages[i].LaborTotal = 0
			for j := range stages[i].SubStages {
				stages[i].SubStages[j].MaterialTotal = 0
				stages[i].SubStages[j].EquipmentTotal = 0
				stages[i].SubStages[j].LaborTotal = 0
			}
		}
	}

	// Legacy / web path: no pagination params → full array, exactly as before
	// (now with sub_stages embedded too).
	if !paginate {
		response.Success(c, stages)
		return
	}

	// total count over the same WHERE as the list (without limit/offset).
	countArgs := []interface{}{projectID, tenantID}
	if buildingFilter.Valid && buildingFilter.Int64 != 0 {
		countArgs = append(countArgs, buildingFilter.Int64)
	}
	total := len(stages)
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_stages s
		 WHERE s.project_id = $1 AND s.tenant_id = $2`+buildingClause+ghostFilter,
		countArgs...).Scan(&total)

	// Project-wide totals for the summary cards — computed on the first page
	// only (mobile keeps it for later pages). Each aggregate is an independent
	// scalar subquery, so joins do not multiply rows.
	var summary *gin.H
	if page == 1 {
		var planned, actual, material float64
		var stageCount int
		err := h.db.QueryRow(`
			SELECT
			  (SELECT COUNT(*) FROM construction_stages s
			     WHERE s.project_id=$1 AND s.tenant_id=$2`+buildingClause+`),
			  (SELECT COALESCE(SUM(s.planned_budget),0) FROM construction_stages s
			     WHERE s.project_id=$1 AND s.tenant_id=$2`+buildingClause+`),
			  (SELECT COALESCE(SUM(el.amount),0) FROM construction_expense_lines el
			     JOIN construction_stages s ON s.id = el.stage_id
			     WHERE el.status='approved' AND el.deleted_at IS NULL
			       AND s.project_id=$1 AND s.tenant_id=$2`+buildingClause+`),
			  (SELECT COALESCE(SUM(m.total_cost),0) FROM construction_sub_stage_materials m
			     JOIN construction_sub_stages ss ON ss.id = m.sub_stage_id
			     JOIN construction_stages s ON s.id = ss.stage_id
			     WHERE s.project_id=$1 AND s.tenant_id=$2`+buildingClause+`)
		`, countArgs...).Scan(&stageCount, &planned, &actual, &material)
		if err == nil {
			if stagesPriceHidden {
				planned, actual, material = 0, 0, 0
			}
			summary = &gin.H{
				"total_stages":          stageCount,
				"total_planned_budget":  planned,
				"total_actual_expenses": actual,
				"total_material_total":  material,
			}
		} else {
			h.log.Error("Failed to compute stage summary", "error", err)
		}
	}

	totalPages := 0
	if limit > 0 {
		totalPages = (total + limit - 1) / limit
	}
	resp := gin.H{
		"success": true,
		"data":    stages,
		"meta": gin.H{
			"page":        page,
			"limit":       limit,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
			"has_prev":    page > 1,
		},
	}
	if summary != nil {
		resp["summary"] = *summary
	}
	c.JSON(http.StatusOK, resp)
}

// GetConstructionStagesOverview returns the 4 summary cards for the mobile
// "Bosqichlar" tab (Bloklar / Block tayyorligi / Byudjet / Etap-ish), computed
// server-side and project-wide. GET /construction/projects/:id/stages/overview
//
// The figures mirror the web StagesTabV2.jsx but aggregated across the whole
// project (all blocks' Единич estimates), not the single active block:
//
//   - blocks_count            COUNT of construction_buildings for the project
//     (no soft-delete column on that table).
//   - works_count / budget /  derived from "work" estimate-lines in the project's
//     block_readiness_percent edinich estimates. A work = a top-level row with no
//     resource_type (resource_type=” AND parent_line_id=0).
//     Scoping to edinich-only avoids double-counting the
//     same work across the Единич + ВОР sheets.
//   - stages_count            distinct section path (parent_item_number) of works.
//
// Planned qty per work follows the web priority (imported → original → quantity).
// Readiness is cost-weighted: Σ(total_amount*min(done/q,1)) / Σ(total_amount).
//
// Budget is COST and must never reach a foreman (prorab): for that role we send
// budget=null and budget_hidden=true (server-side hiding, per the mobile contract).
func (h *Handler) GetConstructionStagesOverview(c *gin.Context) {
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

	// 1) blocks_count — construction_buildings has no soft-delete column.
	var blocksCount int
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_buildings WHERE project_id = $1 AND tenant_id = $2`,
		projectID, tenantID,
	).Scan(&blocksCount); err != nil {
		h.log.Error("overview: failed to count buildings", "error", err)
	}

	// 2) works_count / budget / readiness / stages_count from the project's
	//    edinich estimate "work" lines (project-wide).
	var (
		worksCount  int
		stagesCount int
		budget      float64
		readiness   float64
	)
	err = h.db.QueryRow(`
		WITH works AS (
			SELECT
				COALESCE(el.total_amount, 0)  AS total_amount,
				COALESCE(el.done_quantity, 0) AS done_quantity,
				CASE
					WHEN COALESCE(el.imported_quantity, 0) > 0 THEN el.imported_quantity
					WHEN COALESCE(el.original_quantity, 0) > 0 THEN el.original_quantity
					ELSE COALESCE(el.quantity, 0)
				END AS q,
				NULLIF(COALESCE(el.parent_item_number, ''), '') AS section
			FROM construction_estimate_line el
			JOIN construction_estimate e
			  ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
			WHERE e.project_id = $1
			  AND el.tenant_id = $2
			  AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
			  AND e.subcontract_id IS NULL
			  AND COALESCE(el.resource_type, '') = ''
			  AND COALESCE(el.parent_line_id, 0) = 0
		)
		SELECT
			COUNT(*),
			COALESCE(SUM(total_amount), 0),
			CASE WHEN SUM(total_amount) > 0
				 THEN SUM(total_amount * LEAST(done_quantity / NULLIF(q, 0), 1)) / SUM(total_amount) * 100
				 ELSE 0 END,
			COUNT(DISTINCT section)
		FROM works
	`, projectID, tenantID).Scan(&worksCount, &budget, &readiness, &stagesCount)
	if err != nil {
		h.log.Error("overview: failed to aggregate works", "error", err)
	}

	// 3) Role-based budget hiding — resolve with the same helper as /my-role.
	role := h.resolveProjectRole(tenantID, userID, projectID)
	budgetHidden := role == "foreman"

	data := gin.H{
		"blocks_count":            blocksCount,
		"block_readiness_percent": readiness,
		"budget_hidden":           budgetHidden,
		"stages_count":            stagesCount,
		"works_count":             worksCount,
	}
	if budgetHidden {
		data["budget"] = nil // never send the cost to a foreman's device
	} else {
		data["budget"] = budget
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetConstructionStageWorks returns the top-level estimate-line "works" for one
// Bosqichlar stage, paginated. GET /construction/stages/:id/works?page=&limit=
//
// A stage's works are estimate lines (construction_estimate_line) with no
// resource_type and no parent (resource_type=” AND parent_line_id=0) in the
// project's edinich estimates, whose section leaf matches the stage name. The
// leaf strips a "СЕКЦИЯ №N ›" prefix exactly like the web StagesTabV2 and the
// reja-fakt handler, so this is robust regardless of whether parent_item_number
// is stored prefixed or bare. Doing the match here lets mobile fetch one stage's
// works directly instead of loading the whole block and filtering client-side.
//
// The work IDs returned are construction_estimate_line ids, so the existing
// construction/works/* transition + bulk endpoints accept them verbatim.
func (h *Handler) GetConstructionStageWorks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	stageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid stage ID")
		return
	}

	// Resolve the stage's section name + project/building scope.
	var stageName string
	var projectID int64
	var buildingID sql.NullInt64
	err = h.db.QueryRow(`
		SELECT name, project_id, building_id
		FROM construction_stages
		WHERE id = $1 AND tenant_id = $2
	`, stageID, tenantID).Scan(&stageName, &projectID, &buildingID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Stage")
		return
	}
	if err != nil {
		h.log.Error("stage works: failed to load stage", "error", err)
		response.InternalError(c, "Failed to load stage")
		return
	}

	// Pagination — defaults page 1 / limit 20 (this endpoint always paginates).
	page, _ := strconv.Atoi(c.Query("page"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Shared WHERE: work rows in the project's edinich estimates whose section
	// leaf == this stage's name. regexp strips any "… ›" prefix (greedy to the
	// last ›), matching stageNameFromPath / the web grouping.
	where := `
		el.tenant_id = $1
		AND e.project_id = $2
		AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
		AND e.subcontract_id IS NULL
		AND COALESCE(el.resource_type, '') = ''
		AND COALESCE(el.parent_line_id, 0) = 0
		AND $3 = ANY(string_to_array(el.parent_item_number, ' › '))`
	args := []interface{}{tenantID, projectID, stageName}
	if buildingID.Valid {
		where += ` AND e.building_id = $4`
		args = append(args, buildingID.Int64)
	}

	var total int
	if err := h.db.QueryRow(`
		SELECT COUNT(*)
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE `+where, args...).Scan(&total); err != nil {
		h.log.Error("stage works: failed to count", "error", err)
	}

	listArgs := append(append([]interface{}{}, args...), limit, offset)
	listQuery := `
		SELECT el.id, el.name, COALESCE(el.uom, ''), COALESCE(el.item_number, ''),
		       CASE
		           WHEN COALESCE(el.imported_quantity, 0) > 0 THEN el.imported_quantity
		           WHEN COALESCE(el.original_quantity, 0) > 0 THEN el.original_quantity
		           ELSE COALESCE(el.quantity, 0)
		       END AS quantity,
		       COALESCE(el.done_quantity, 0),
		       COALESCE(el.unit_rate, 0),
		       COALESCE(el.total_amount, 0),
		       COALESCE(el.parent_item_number, ''),
		       COALESCE(el.approval_status, 'pending'),
		       COALESCE(el.material_rate, 0), COALESCE(el.labor_rate, 0), COALESCE(el.equipment_rate, 0),
		       COALESCE(el.rejection_note, '')
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE ` + where + `
		ORDER BY el.sort_order ASC, el.id ASC
		LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)

	rows, err := h.db.Query(listQuery, listArgs...)
	if err != nil {
		h.log.Error("stage works: failed to list", "error", err)
		response.InternalError(c, "Failed to load stage works")
		return
	}
	defer rows.Close()

	type Work struct {
		ID               int64   `json:"id"`
		Name             string  `json:"name"`
		UOM              string  `json:"uom"`
		ItemNumber       string  `json:"item_number"`
		Quantity         float64 `json:"quantity"`
		DoneQuantity     float64 `json:"done_quantity"`
		UnitRate         float64 `json:"unit_rate"`
		TotalAmount      float64 `json:"total_amount"`
		ParentItemNumber string  `json:"parent_item_number"`
		ApprovalStatus   string  `json:"approval_status"`
		MaterialRate     float64 `json:"material_rate"`
		LaborRate        float64 `json:"labor_rate"`
		EquipmentRate    float64 `json:"equipment_rate"`
		RejectionNote    string  `json:"rejection_note"`
	}

	works := []Work{}
	// Sof-prorab narx ko'rmaydi (conventions §4) — server-side stripping.
	priceHidden := h.constructionPriceRestricted(c, tenantID, projectID)
	for rows.Next() {
		var w Work
		if err := rows.Scan(&w.ID, &w.Name, &w.UOM, &w.ItemNumber,
			&w.Quantity, &w.DoneQuantity, &w.UnitRate, &w.TotalAmount,
			&w.ParentItemNumber, &w.ApprovalStatus,
			&w.MaterialRate, &w.LaborRate, &w.EquipmentRate, &w.RejectionNote); err != nil {
			h.log.Error("stage works: failed to scan", "error", err)
			continue
		}
		if priceHidden {
			w.UnitRate, w.TotalAmount = 0, 0
			w.MaterialRate, w.LaborRate, w.EquipmentRate = 0, 0, 0
		}
		works = append(works, w)
	}

	totalPages := 0
	if limit > 0 {
		totalPages = (total + limit - 1) / limit
	}
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"data":         works,
		"price_hidden": priceHidden,
		"meta": gin.H{
			"page": page, "limit": limit, "total": total,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
			"has_prev":    page > 1,
		},
	})
}

// CreateConstructionStage creates a new stage for a project
func (h *Handler) CreateConstructionStage(c *gin.Context) {
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

	var req struct {
		Name          string  `json:"name" binding:"required"`
		StageOrder    int     `json:"stage_order"`
		Status        string  `json:"status"`
		PlannedBudget float64 `json:"planned_budget"`
		PlannedStart  string  `json:"planned_start"`
		PlannedEnd    string  `json:"planned_end"`
		Notes         string  `json:"notes"`
		// Migration 333: stages can be assigned to a specific building/block.
		// 0 or absent → project-wide (unassigned), same as before.
		BuildingID int64 `json:"building_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if req.Status == "" {
		req.Status = "not_started"
	}

	var buildingIDArg interface{}
	if req.BuildingID > 0 {
		buildingIDArg = req.BuildingID
	}

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_stages (
			tenant_id, project_id, building_id, name, stage_order, status, planned_budget,
			planned_start, planned_end, notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
		RETURNING id
	`,
		tenantID, projectID, buildingIDArg, req.Name, req.StageOrder, req.Status, req.PlannedBudget,
		nullStringFromVal(req.PlannedStart), nullStringFromVal(req.PlannedEnd),
		nullStringFromVal(req.Notes),
	).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create stage", "error", err)
		response.InternalError(c, "Failed to create stage")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "stage", fmt.Sprintf("Bosqich yaratildi: %s", req.Name), "Stage", id)

	response.Success(c, map[string]interface{}{"id": id, "message": "Stage created successfully"})
}

// UpdateConstructionStage updates an existing stage
func (h *Handler) UpdateConstructionStage(c *gin.Context) {
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
		Name          *string  `json:"name"`
		StageOrder    *int     `json:"stage_order"`
		Status        *string  `json:"status"`
		PlannedBudget *float64 `json:"planned_budget"`
		PlannedStart  *string  `json:"planned_start"`
		PlannedEnd    *string  `json:"planned_end"`
		ActualStart   *string  `json:"actual_start"`
		ActualEnd     *string  `json:"actual_end"`
		Notes         *string  `json:"notes"`
		// Migration 333. Send 0 to clear the assignment (back to project-wide);
		// omit the field to leave it unchanged.
		BuildingID *int64 `json:"building_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	addField := func(col string, val interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", col, argCount))
		args = append(args, val)
	}

	if req.Name != nil {
		addField("name", *req.Name)
	}
	if req.StageOrder != nil {
		addField("stage_order", *req.StageOrder)
	}
	if req.Status != nil {
		addField("status", *req.Status)
		// Stamp actual_start the first time a stage transitions into
		// in_progress / completed, unless the request already supplies an
		// explicit actual_start. COALESCE preserves any previously-recorded
		// date, so toggling status back and forth doesn't reset it.
		if req.ActualStart == nil && (*req.Status == "in_progress" || *req.Status == "completed") {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_start = COALESCE(actual_start, $%d)", argCount))
			args = append(args, time.Now().Format("2006-01-02"))
		}
		// Stamp actual_end the first time a stage is marked completed.
		if req.ActualEnd == nil && *req.Status == "completed" {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_end = COALESCE(actual_end, $%d)", argCount))
			args = append(args, time.Now().Format("2006-01-02"))
		}
	}
	if req.PlannedBudget != nil {
		addField("planned_budget", *req.PlannedBudget)
	}
	if req.PlannedStart != nil {
		if *req.PlannedStart == "" {
			addField("planned_start", nil)
		} else {
			addField("planned_start", *req.PlannedStart)
		}
	}
	if req.PlannedEnd != nil {
		if *req.PlannedEnd == "" {
			addField("planned_end", nil)
		} else {
			addField("planned_end", *req.PlannedEnd)
		}
	}
	if req.ActualStart != nil {
		if *req.ActualStart == "" {
			addField("actual_start", nil)
		} else {
			addField("actual_start", *req.ActualStart)
		}
	}
	if req.ActualEnd != nil {
		if *req.ActualEnd == "" {
			addField("actual_end", nil)
		} else {
			addField("actual_end", *req.ActualEnd)
		}
	}
	if req.Notes != nil {
		if *req.Notes == "" {
			addField("notes", nil)
		} else {
			addField("notes", *req.Notes)
		}
	}
	if req.BuildingID != nil {
		if *req.BuildingID == 0 {
			addField("building_id", nil)
		} else {
			addField("building_id", *req.BuildingID)
		}
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_stages SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update stage", "error", err)
		response.InternalError(c, "Failed to update stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Stage not found")
		return
	}

	response.Success(c, map[string]interface{}{"id": id, "message": "Stage updated successfully"})
}

// =====================================================
// CONSTRUCTION SUB-STAGES HANDLERS
// =====================================================

// ListConstructionSubStages returns all sub-stages for a stage
func (h *Handler) ListConstructionSubStages(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	stageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid stage ID")
		return
	}

	type SubStage struct {
		ID             int64     `json:"id"`
		StageID        int64     `json:"stage_id"`
		Name           string    `json:"name"`
		SubOrder       int       `json:"sub_order"`
		Status         string    `json:"status"`
		Notes          *string   `json:"notes"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
		MaterialCount  int       `json:"material_count"`
		MaterialTotal  float64   `json:"material_total"`
		EquipmentCount int       `json:"equipment_count"`
		EquipmentTotal float64   `json:"equipment_total"`
		LaborCount     int       `json:"labor_count"`
		LaborTotal     float64   `json:"labor_total"`
	}

	rows, err := h.db.Query(`
		SELECT ss.id, ss.stage_id, ss.name, ss.sub_order, ss.status, ss.notes, ss.created_at, ss.updated_at,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_materials m WHERE m.sub_stage_id = ss.id), 0) as material_count,
		       COALESCE((SELECT SUM(m.total_cost) FROM construction_sub_stage_materials m WHERE m.sub_stage_id = ss.id), 0) as material_total,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0) as equipment_count,
		       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND COALESCE(eq.resource_type,'equipment') = 'equipment'), 0) as equipment_total,
		       COALESCE((SELECT COUNT(*) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND eq.resource_type = 'employee'), 0) as labor_count,
		       COALESCE((SELECT SUM(eq.plan_quantity * eq.unit_price) FROM construction_sub_stage_equipment eq WHERE eq.sub_stage_id = ss.id AND eq.resource_type = 'employee'), 0) as labor_total
		FROM construction_sub_stages ss
		WHERE ss.stage_id = $1 AND ss.tenant_id = $2
		ORDER BY ss.sub_order ASC, ss.id ASC
	`, stageID, tenantID)
	if err != nil {
		h.log.Error("Failed to list sub-stages", "error", err)
		response.InternalError(c, "Failed to list sub-stages")
		return
	}
	defer rows.Close()

	subStages := []SubStage{}
	for rows.Next() {
		var s SubStage
		if err := rows.Scan(
			&s.ID, &s.StageID, &s.Name, &s.SubOrder, &s.Status, &s.Notes, &s.CreatedAt, &s.UpdatedAt,
			&s.MaterialCount, &s.MaterialTotal,
			&s.EquipmentCount, &s.EquipmentTotal,
			&s.LaborCount, &s.LaborTotal,
		); err != nil {
			continue
		}
		subStages = append(subStages, s)
	}

	response.Success(c, subStages)
}

// CreateConstructionSubStage creates a new sub-stage for a stage
func (h *Handler) CreateConstructionSubStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	stageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid stage ID")
		return
	}

	var req struct {
		Name     string `json:"name" binding:"required"`
		SubOrder int    `json:"sub_order"`
		Status   string `json:"status"`
		Notes    string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if req.Status == "" {
		req.Status = "not_started"
	}

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_sub_stages (tenant_id, stage_id, name, sub_order, status, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING id
	`, tenantID, stageID, req.Name, req.SubOrder, req.Status, nullStringFromVal(req.Notes)).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create sub-stage", "error", err)
		response.InternalError(c, "Failed to create sub-stage")
		return
	}

	response.Success(c, map[string]interface{}{"id": id, "message": "Sub-stage created successfully"})
}

// UpdateConstructionSubStage updates an existing sub-stage
func (h *Handler) UpdateConstructionSubStage(c *gin.Context) {
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
		Name     *string `json:"name"`
		SubOrder *int    `json:"sub_order"`
		Status   *string `json:"status"`
		Notes    *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	addField := func(col string, val interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", col, argCount))
		args = append(args, val)
	}

	if req.Name != nil {
		addField("name", *req.Name)
	}
	if req.SubOrder != nil {
		addField("sub_order", *req.SubOrder)
	}
	if req.Status != nil {
		addField("status", *req.Status)
		// Stamp actual_start / actual_end on the matching status transitions.
		// Columns were added by migration 335; COALESCE preserves any value
		// already on record if the status is toggled multiple times.
		if *req.Status == "in_progress" || *req.Status == "completed" {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_start = COALESCE(actual_start, $%d)", argCount))
			args = append(args, time.Now().Format("2006-01-02"))
		}
		if *req.Status == "completed" {
			argCount++
			updates = append(updates, fmt.Sprintf("actual_end = COALESCE(actual_end, $%d)", argCount))
			args = append(args, time.Now().Format("2006-01-02"))
		}
	}
	if req.Notes != nil {
		if *req.Notes == "" {
			addField("notes", nil)
		} else {
			addField("notes", *req.Notes)
		}
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_sub_stages SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update sub-stage", "error", err)
		response.InternalError(c, "Failed to update sub-stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Sub-stage not found")
		return
	}

	response.Success(c, map[string]interface{}{"id": id, "message": "Sub-stage updated successfully"})
}

// DeleteConstructionSubStage deletes a sub-stage
func (h *Handler) DeleteConstructionSubStage(c *gin.Context) {
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

	result, err := h.db.Exec(
		`DELETE FROM construction_sub_stages WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete sub-stage", "error", err)
		response.InternalError(c, "Failed to delete sub-stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Sub-stage not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Sub-stage deleted successfully"})
}

// DeleteConstructionStage deletes a stage (only if no expense lines linked)
func (h *Handler) DeleteConstructionStage(c *gin.Context) {
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

	var expenseCount int
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_expense_lines WHERE stage_id = $1 AND deleted_at IS NULL`,
		id,
	).Scan(&expenseCount)
	if expenseCount > 0 {
		response.BadRequest(c, fmt.Sprintf("Cannot delete stage with %d expense line(s). Remove expenses first.", expenseCount))
		return
	}

	result, err := h.db.Exec(
		`DELETE FROM construction_stages WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete stage", "error", err)
		response.InternalError(c, "Failed to delete stage")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Stage not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Stage deleted successfully"})
}

// ─────────────────────────────────────────────────────────────────────────────
// GetProjectInProgressItems
//
// Returns a flat feed of every construction item that is currently
// `status = 'in_progress'` for a given project — stages and sub-stages today,
// pluggable for more sources later. Used by the "Jarayon" tab in the frontend.
//
// Sub-stages don't have their own planned/actual dates, so they inherit the
// parent stage's dates for reporting purposes. `progress_pct` is derived from
// the elapsed fraction of the planned window; `bucket` classifies each row
// as "on_track" (actual_start ≤ today ≤ planned_end) or "behind" (planned_end
// has passed).
// ─────────────────────────────────────────────────────────────────────────────
func (h *Handler) GetProjectInProgressItems(c *gin.Context) {
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

	// Additional columns beyond the previous version:
	//   - sub_total / sub_done: counts of sub-stages and completed sub-stages
	//     under each row. For a stage row we use its own counts; for a
	//     sub-stage row we report 1 / (1 if completed else 0) so the
	//     downstream progress logic is uniform. Drives the progress_pct
	//     that the Jarayon tab renders (matches the Bosqichlar tab's
	//     "completed/total" ratio instead of the old time-elapsed estimate).
	//   - Sub-stage actual_start / actual_end (migration 335) fall back to
	//     the parent stage's dates so legacy rows still report something.
	//
	// Union arm 3 — works (construction_estimate_line) whose v2 approval
	// workflow is mid-flight. The user wants the Jarayon tab to surface
	// everything that isn't pending (draft) or confirmed_engineer
	// (final/completed); so we list works in `in_progress`, `submitted`,
	// or `confirmed_supervisor`. These are the works currently moving
	// through the foreman → supervisor → engineer pipeline.
	const q = `
		SELECT 'stage'::text AS source,
		       s.id,
		       NULL::bigint AS parent_id,
		       NULL::text   AS parent_name,
		       s.name,
		       s.status,
		       s.planned_start, s.planned_end,
		       s.actual_start,  s.actual_end,
		       b.name AS building_name, b.id AS building_id,
		       COALESCE((SELECT COUNT(*)
		                 FROM construction_sub_stages x
		                 WHERE x.stage_id = s.id AND x.tenant_id = s.tenant_id), 0) AS sub_total,
		       COALESCE((SELECT COUNT(*)
		                 FROM construction_sub_stages x
		                 WHERE x.stage_id = s.id AND x.tenant_id = s.tenant_id
		                   AND x.status = 'completed'), 0) AS sub_done
		FROM construction_stages s
		LEFT JOIN construction_buildings b ON b.id = s.building_id
		WHERE s.project_id = $1 AND s.tenant_id = $2 AND s.status = 'in_progress'

		UNION ALL

		SELECT 'sub_stage'::text AS source,
		       ss.id,
		       s.id   AS parent_id,
		       s.name AS parent_name,
		       ss.name,
		       ss.status,
		       s.planned_start, s.planned_end,
		       COALESCE(ss.actual_start, s.actual_start) AS actual_start,
		       COALESCE(ss.actual_end,   s.actual_end)   AS actual_end,
		       b.name AS building_name, b.id AS building_id,
		       1 AS sub_total,
		       (CASE WHEN ss.status = 'completed' THEN 1 ELSE 0 END) AS sub_done
		FROM construction_sub_stages ss
		JOIN construction_stages s ON s.id = ss.stage_id
		LEFT JOIN construction_buildings b ON b.id = s.building_id
		WHERE s.project_id = $1 AND s.tenant_id = $2 AND ss.status = 'in_progress'

		UNION ALL

		-- v2 approval-workflow works that are mid-pipeline.
		--   in_progress           — foreman has entered qty but not submitted
		--   submitted             — waiting on supervisor
		--   confirmed_supervisor  — waiting on chief engineer's final OK
		-- pending and confirmed_engineer are intentionally excluded —
		-- pending == not started yet (draft), confirmed_engineer == done.
		SELECT 'work'::text AS source,
		       el.id,
		       NULL::bigint AS parent_id,
		       NULLIF(el.parent_item_number, '') AS parent_name,
		       el.name,
		       el.approval_status AS status,
		       NULL::date AS planned_start,
		       NULL::date AS planned_end,
		       NULL::date AS actual_start,
		       NULL::date AS actual_end,
		       b.name AS building_name, b.id AS building_id,
		       -- Use plan/done quantity as the progress source. Mapping
		       -- onto the existing sub_total/sub_done columns lets the
		       -- downstream code path stay uniform with stages.
		       (CASE WHEN el.quantity > 0 THEN 100 ELSE 0 END)::bigint AS sub_total,
		       (CASE
		          WHEN el.quantity > 0
		            THEN LEAST(100, ROUND(COALESCE(el.done_quantity, 0) / el.quantity * 100))
		          ELSE 0
		        END)::bigint AS sub_done
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		LEFT JOIN construction_buildings b ON b.id = e.building_id
		WHERE e.project_id = $1
		  AND el.tenant_id = $2
		  AND el.parent_line_id IS NULL                       -- top-level works only
		  AND COALESCE(el.resource_type, '') = ''             -- skip resource lines
		  AND el.approval_status IN ('in_progress', 'submitted', 'confirmed_supervisor')

		ORDER BY source, name
	`
	rows, err := h.db.Query(q, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to query in-progress items", "error", err)
		response.InternalError(c, "Failed to load in-progress items")
		return
	}
	defer rows.Close()

	type Item struct {
		Source       string  `json:"source"`
		ID           int64   `json:"id"`
		ParentID     *int64  `json:"parent_id"`
		ParentName   *string `json:"parent_name"`
		Name         string  `json:"name"`
		Status       string  `json:"status"`
		PlannedStart *string `json:"planned_start"`
		PlannedEnd   *string `json:"planned_end"`
		ActualStart  *string `json:"actual_start"`
		ActualEnd    *string `json:"actual_end"`
		BuildingName *string `json:"building_name"`
		BuildingID   *int64  `json:"building_id"`
		ProgressPct  float64 `json:"progress_pct"`
		Bucket       string  `json:"bucket"` // on_track | behind
	}

	now := time.Now()
	items := []Item{}

	for rows.Next() {
		var it Item
		var parentID sql.NullInt64
		var parentName, buildingName sql.NullString
		var buildingID sql.NullInt64
		var plannedStartT, plannedEndT, actualStartT, actualEndT sql.NullTime
		var subTotal, subDone int64
		if err := rows.Scan(
			&it.Source, &it.ID, &parentID, &parentName,
			&it.Name, &it.Status,
			&plannedStartT, &plannedEndT,
			&actualStartT, &actualEndT,
			&buildingName, &buildingID,
			&subTotal, &subDone,
		); err != nil {
			h.log.Error("Failed to scan in-progress item", "error", err)
			continue
		}
		if parentID.Valid {
			v := parentID.Int64
			it.ParentID = &v
		}
		if parentName.Valid {
			v := parentName.String
			it.ParentName = &v
		}
		if plannedStartT.Valid {
			v := plannedStartT.Time.Format("2006-01-02")
			it.PlannedStart = &v
		}
		if plannedEndT.Valid {
			v := plannedEndT.Time.Format("2006-01-02")
			it.PlannedEnd = &v
		}
		if actualStartT.Valid {
			v := actualStartT.Time.Format("2006-01-02")
			it.ActualStart = &v
		}
		if actualEndT.Valid {
			v := actualEndT.Time.Format("2006-01-02")
			it.ActualEnd = &v
		}
		if buildingName.Valid {
			v := buildingName.String
			it.BuildingName = &v
		}
		if buildingID.Valid {
			v := buildingID.Int64
			it.BuildingID = &v
		}

		// Progress policy:
		//   * stage rows → ratio of completed sub-stages (matches the
		//     Bosqichlar tab's "1/2 = 50%" display).
		//   * stage rows with no sub-stages → 0% when in progress, 100% when
		//     completed. (This feed only returns in_progress, so effectively 0.)
		//   * sub-stage rows → 0/50/100 based on their own status.
		//   * work rows (v2 approval workflow) → done_quantity / quantity *
		//     100, with a small bump for the "submitted" /
		//     "confirmed_supervisor" stages so works that already passed
		//     a review step never read as 0% even when the foreman didn't
		//     bother filling in a quantity. The SQL feeds sub_total = 100
		//     and sub_done = ratio*100 already, so this is just division.
		switch it.Source {
		case "stage":
			if subTotal > 0 {
				it.ProgressPct = float64(subDone) / float64(subTotal) * 100
			} else if it.Status == "completed" {
				it.ProgressPct = 100
			} else {
				it.ProgressPct = 0
			}
		case "sub_stage":
			switch it.Status {
			case "completed":
				it.ProgressPct = 100
			case "in_progress":
				it.ProgressPct = 50
			default:
				it.ProgressPct = 0
			}
		case "work":
			if subTotal > 0 {
				it.ProgressPct = float64(subDone) / float64(subTotal) * 100
			}
			// Floor a few representative values so submitted / supervisor-
			// confirmed works never read as 0% even when the foreman left
			// the done quantity blank.
			switch it.Status {
			case "confirmed_supervisor":
				if it.ProgressPct < 90 {
					it.ProgressPct = 90
				}
			case "submitted":
				if it.ProgressPct < 75 {
					it.ProgressPct = 75
				}
			case "in_progress":
				if it.ProgressPct < 25 {
					it.ProgressPct = 25
				}
			}
		}

		// "Behind" only makes sense once a planned_end is on record and past.
		if plannedEndT.Valid && now.After(plannedEndT.Time) {
			it.Bucket = "behind"
		} else {
			it.Bucket = "on_track"
		}
		items = append(items, it)
	}

	// KPI rollup
	total := len(items)
	onTrack := 0
	behind := 0
	sumPct := 0.0
	for _, it := range items {
		if it.Bucket == "behind" {
			behind++
		} else {
			onTrack++
		}
		sumPct += it.ProgressPct
	}
	avgPct := 0.0
	if total > 0 {
		avgPct = sumPct / float64(total)
	}

	kpis := map[string]interface{}{
		"total":     total,
		"on_track":  onTrack,
		"behind":    behind,
		"completed": 0,
		"avg_pct":   avgPct,
	}

	// Backward compatible: no page_size → today's full-array shape (web client).
	sizeStr := c.Query("page_size")
	if sizeStr == "" {
		response.Success(c, map[string]interface{}{
			"project_id": projectID,
			"items":      items,
			"kpis":       kpis,
		})
		return
	}

	// ── Paginated (mobile, §13): one building's on_track items, 20/page ──
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(sizeStr)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// summary.buildings — distinct on_track items that have a building, counted,
	// sorted by natural building-name order. Drives the block tabs.
	type bsum struct {
		name  string
		count int
	}
	bmap := map[int64]*bsum{}
	var border []int64
	for _, it := range items {
		if it.Bucket != "on_track" || it.BuildingID == nil {
			continue
		}
		bid := *it.BuildingID
		if bmap[bid] == nil {
			name := ""
			if it.BuildingName != nil {
				name = *it.BuildingName
			}
			bmap[bid] = &bsum{name: name}
			border = append(border, bid)
		}
		bmap[bid].count++
	}
	sort.Slice(border, func(i, j int) bool { return bmap[border[i]].name < bmap[border[j]].name })
	buildings := make([]map[string]interface{}, 0, len(border))
	for _, bid := range border {
		buildings = append(buildings, map[string]interface{}{
			"building_id": bid, "building_name": bmap[bid].name, "count": bmap[bid].count,
		})
	}

	// Resolve the target building: explicit ?building_id, else the first block.
	var targetBID int64
	if raw := strings.TrimSpace(c.Query("building_id")); raw != "" {
		if v, e := strconv.ParseInt(raw, 10, 64); e == nil {
			targetBID = v
		}
	} else if len(border) > 0 {
		targetBID = border[0]
	}

	// Filter to that building's on_track items, then slice the page.
	filtered := []Item{}
	for _, it := range items {
		if it.Bucket == "on_track" && it.BuildingID != nil && *it.BuildingID == targetBID {
			filtered = append(filtered, it)
		}
	}
	totalForBuilding := len(filtered)
	start := (page - 1) * pageSize
	if start > totalForBuilding {
		start = totalForBuilding
	}
	end := start + pageSize
	if end > totalForBuilding {
		end = totalForBuilding
	}
	pageItems := filtered[start:end]
	totalPages := 0
	if pageSize > 0 {
		totalPages = (totalForBuilding + pageSize - 1) / pageSize
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    pageItems,
		"meta": gin.H{
			"page": page, "page_size": pageSize, "total": totalForBuilding,
			"total_pages": totalPages, "has_next": page < totalPages, "building_id": targetBID,
		},
		"summary": gin.H{
			"buildings": buildings,
			"kpis":      kpis,
		},
	})
}
