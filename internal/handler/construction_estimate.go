package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// =====================================================
// CONSTRUCTION ESTIMATE HANDLERS
// =====================================================

// ListEstimates returns estimates for a project
func (h *Handler) ListEstimates(c *gin.Context) {
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

	scope := c.Query("scope") // "subcontract" to get subcontract-linked estimates only

	scopeFilter := "AND e.subcontract_id IS NULL"
	if scope == "subcontract" {
		scopeFilter = "AND e.subcontract_id IS NOT NULL"
	}

	// Opt-in pagination — only when the client explicitly asks. Absent →
	// full list (today's behaviour), which the web Smetalar tab and Smeta
	// boshqaruvi block selector rely on to build block options / totals.
	// Mobile sends page+page_size to page the version cards. Mirrors
	// ListEstimateLines (same response.Paginated envelope).
	pageStr := c.Query("page")
	sizeStr := c.Query("page_size")
	paginate := pageStr != "" || sizeStr != ""
	page, _ := strconv.Atoi(pageStr)
	pageSize, _ := strconv.Atoi(sizeStr)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Pre-aggregate line counts in a CTE rather than running a
	// correlated `(SELECT COUNT(*) ...)` subquery for every estimate
	// row. On tenants with hundreds of estimates the old per-row form
	// was an O(N×M) sequential scan over construction_estimate_line
	// (count(estimates) × count(lines per estimate)) and was the
	// observed cause of Smeta boshqaruvi page timeouts.
	//
	// The CTE is scoped to THIS project (via the JOIN on e.project_id)
	// so it only counts the slice we actually render. With migration
	// 378's covering index (tenant_id, estimate_id) this becomes a
	// single index-only scan + GROUP BY hash aggregate.
	//
	// Optional building/block filter (mobile Smetalar block selector). When a
	// numeric building_id is present, scope estimates to that block; absent or
	// the "Hammasi" chip (no param) → all estimates, as today. Placeholder
	// indexes are computed dynamically so building_id ($3) and the LIMIT/OFFSET
	// pagination args compose correctly.
	var buildingID int
	hasBuilding := false
	if bid := strings.TrimSpace(c.Query("building_id")); bid != "" {
		if id, e := strconv.Atoi(bid); e == nil && id > 0 {
			buildingID = id
			hasBuilding = true
		}
	}

	args := []interface{}{projectID, tenantID}
	buildingFilter := ""
	if hasBuilding {
		args = append(args, buildingID)
		buildingFilter = fmt.Sprintf("AND e.building_id = $%d", len(args))
	}
	limitClause := ""
	if paginate {
		limitClause = fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		args = append(args, pageSize, offset)
	}
	query := fmt.Sprintf(`
		WITH estimate_line_counts AS (
		    SELECT l.estimate_id, COUNT(*)::int AS lines_count
		    FROM construction_estimate_line l
		    JOIN construction_estimate e ON e.id = l.estimate_id
		    WHERE e.project_id = $1 AND l.tenant_id = $2
		    GROUP BY l.estimate_id
		)
		SELECT e.id, e.tenant_id, e.project_id, e.building_id, e.version, e.name, e.state, e.is_current,
		       e.overhead_pct, e.profit_pct, e.vat_pct,
		       e.amount_direct, e.amount_total,
		       COALESCE(e.source_type, '') as source_type,
		       e.subcontract_id,
		       e.approved_by, e.approved_date, e.created_by,
		       e.created_date, e.updated_date,
		       COALESCE(elc.lines_count, 0) as lines_count,
		       COALESCE(ua.first_name || ' ' || ua.last_name, '') as approved_name,
		       COALESCE(uc.first_name || ' ' || uc.last_name, '') as created_name,
		       COALESCE(b.name, '') as building_name,
		       COALESCE(sc.name, '') as subcontract_name
		FROM construction_estimate e
		LEFT JOIN estimate_line_counts elc ON elc.estimate_id = e.id
		LEFT JOIN users ua ON ua.id = e.approved_by
		LEFT JOIN users uc ON uc.id = e.created_by
		LEFT JOIN construction_buildings b ON b.id = e.building_id
		LEFT JOIN construction_subcontract sc ON sc.id = e.subcontract_id
		WHERE e.project_id = $1 AND e.tenant_id = $2 %s %s
		ORDER BY e.version DESC, e.id DESC%s
	`, scopeFilter, buildingFilter, limitClause)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list estimates", "error", err)
		response.InternalError(c, "Failed to list estimates")
		return
	}
	defer rows.Close()

	items := []entity.ConstructionEstimate{}
	for rows.Next() {
		var item entity.ConstructionEstimate
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.ProjectID, &item.BuildingID, &item.Version, &item.Name, &item.State, &item.IsCurrent,
			&item.OverheadPct, &item.ProfitPct, &item.VatPct,
			&item.AmountDirect, &item.AmountTotal,
			&item.SourceType,
			&item.SubcontractID,
			&item.ApprovedBy, &item.ApprovedDate, &item.CreatedBy,
			&item.CreatedDate, &item.UpdatedDate,
			&item.LinesCount, &item.ApprovedName, &item.CreatedName,
			&item.BuildingName,
			&item.SubcontractName,
		); err != nil {
			h.log.Error("Failed to scan estimate", "error", err)
			continue
		}
		items = append(items, item)
	}

	// Backward compatible: no page params → full list, bare array (web).
	if !paginate {
		response.Success(c, items)
		return
	}

	// total count for meta — same project + scope + building predicate so
	// has_next reflects the filtered block, not the whole project.
	countArgs := []interface{}{projectID, tenantID}
	countBuildingFilter := ""
	if hasBuilding {
		countArgs = append(countArgs, buildingID)
		countBuildingFilter = fmt.Sprintf("AND e.building_id = $%d", len(countArgs))
	}
	var total int
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*) FROM construction_estimate e
		WHERE e.project_id = $1 AND e.tenant_id = $2 %s %s
	`, scopeFilter, countBuildingFilter)
	if err := h.db.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		h.log.Error("Failed to count estimates", "error", err)
	}

	response.Paginated(c, items, page, pageSize, total)
}

// GetEstimate returns a single estimate with its lines
func (h *Handler) GetEstimate(c *gin.Context) {
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

	// Get estimate header
	var est entity.ConstructionEstimate
	err = h.db.QueryRow(`
		SELECT e.id, e.tenant_id, e.project_id, e.building_id, e.version, e.name, e.state, e.is_current,
		       e.overhead_pct, e.profit_pct, e.vat_pct,
		       e.amount_direct, e.amount_total,
		       COALESCE(e.source_type, '') as source_type,
		       e.subcontract_id,
		       e.approved_by, e.approved_date, e.created_by,
		       e.created_date, e.updated_date,
		       0 as lines_count,
		       COALESCE(ua.first_name || ' ' || ua.last_name, '') as approved_name,
		       COALESCE(uc.first_name || ' ' || uc.last_name, '') as created_name,
		       COALESCE(b.name, '') as building_name,
		       COALESCE(sc.name, '') as subcontract_name
		FROM construction_estimate e
		LEFT JOIN users ua ON ua.id = e.approved_by
		LEFT JOIN users uc ON uc.id = e.created_by
		LEFT JOIN construction_buildings b ON b.id = e.building_id
		LEFT JOIN construction_subcontract sc ON sc.id = e.subcontract_id
		WHERE e.id = $1 AND e.tenant_id = $2
	`, id, tenantID).Scan(
		&est.ID, &est.TenantID, &est.ProjectID, &est.BuildingID, &est.Version, &est.Name, &est.State, &est.IsCurrent,
		&est.OverheadPct, &est.ProfitPct, &est.VatPct,
		&est.AmountDirect, &est.AmountTotal,
		&est.SourceType,
		&est.SubcontractID,
		&est.ApprovedBy, &est.ApprovedDate, &est.CreatedBy,
		&est.CreatedDate, &est.UpdatedDate,
		&est.LinesCount, &est.ApprovedName, &est.CreatedName,
		&est.BuildingName,
		&est.SubcontractName,
	)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}

	// Get lines — honour `?include_manual=false` so the Smetalar tab can
	// hide rows added via the Smeta boshqaruvi / Bosqichlar UI (migration
	// 417). Defaults to TRUE for every other caller so behaviour is
	// unchanged unless the param is explicitly passed.
	includeManual := true
	if raw := strings.TrimSpace(c.Query("include_manual")); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			includeManual = v
		}
	}
	lines := h.getEstimateLines(id, tenantID, includeManual)

	response.Success(c, map[string]interface{}{
		"estimate": est,
		"lines":    lines,
	})
}

// CreateEstimate creates a new estimate version for a project
func (h *Handler) CreateEstimate(c *gin.Context) {
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

	var req entity.CreateEstimateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Validate percentage ranges
	if req.OverheadPct < 0 || req.OverheadPct > 99999 {
		response.BadRequest(c, "Overhead percentage must be between 0 and 99999")
		return
	}
	if req.ProfitPct < 0 || req.ProfitPct > 99999 {
		response.BadRequest(c, "Profit percentage must be between 0 and 99999")
		return
	}
	if req.VatPct < 0 || req.VatPct > 99999 {
		response.BadRequest(c, "VAT percentage must be between 0 and 99999")
		return
	}

	// Get next version number
	var maxVersion int
	_ = h.db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM construction_estimate WHERE project_id = $1 AND tenant_id = $2`,
		projectID, tenantID,
	).Scan(&maxVersion)
	nextVersion := maxVersion + 1

	userID, _ := middleware.GetUserID(c)

	var itemID int64
	err = h.db.QueryRow(`
		INSERT INTO construction_estimate (
			tenant_id, project_id, building_id, version, name, state, is_current,
			overhead_pct, profit_pct, vat_pct,
			source_type, subcontract_id, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, 'draft', false, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(req.BuildingID), nextVersion, req.Name,
		req.OverheadPct, req.ProfitPct, req.VatPct,
		nullStringFromVal(req.SourceType), nullInt64FromVal(req.SubcontractID), userID,
	).Scan(&itemID)

	if err != nil {
		h.log.Error("Failed to create estimate", "error", err)
		response.InternalError(c, "Failed to create estimate")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "estimate", fmt.Sprintf("Smeta yaratildi: v%d - %s", nextVersion, req.Name), "Estimate", itemID)

	// NOTE: Auto-stage creation used to happen here, creating a single
	// stage named after the estimate (`req.Name`). That was wrong for
	// real-world imports — the uploaded file typically has multiple bold
	// section headers (e.g. "ТРУДОВЫЕ РЕСУРСЫ", "СТРОИТЕЛЬНЫЕ МАШИНЫ И
	// МЕХАНИЗМЫ", "МАТЕРИАЛЬНЫЕ РЕСУРСЫ" in a Ресурс sheet, or "Блок №1",
	// "Блок №2" in a ВОР sheet), each of which should be its own stage.
	// The SmetaImportModal parser already splits the file into sections
	// and the frontend handleImport flow now creates one stage per
	// section, scoped to the estimate's building, for every source type.
	// Removing the single-stage auto-create here prevents duplicate
	// "catch-all" stages named after the estimate.

	response.Success(c, map[string]interface{}{
		"id":      itemID,
		"version": nextVersion,
	})
}

// UpdateEstimate updates estimate header fields
func (h *Handler) UpdateEstimate(c *gin.Context) {
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

	// Check state - only draft estimates can be updated
	var state string
	err = h.db.QueryRow(`SELECT state FROM construction_estimate WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&state)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be updated")
		return
	}

	var req entity.UpdateEstimateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *req.Name)
	}
	if req.BuildingID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("building_id = $%d", argCount))
		if *req.BuildingID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.BuildingID)
		}
	}
	if req.OverheadPct != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("overhead_pct = $%d", argCount))
		args = append(args, *req.OverheadPct)
	}
	if req.ProfitPct != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("profit_pct = $%d", argCount))
		args = append(args, *req.ProfitPct)
	}
	if req.VatPct != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("vat_pct = $%d", argCount))
		args = append(args, *req.VatPct)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_estimate SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update estimate", "error", err)
		response.InternalError(c, "Failed to update estimate")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Estimate not found")
		return
	}

	// Recalculate totals
	h.recalculateEstimateTotals(id)

	response.Success(c, map[string]interface{}{
		"id":      id,
		"message": "Estimate updated successfully",
	})
}

// ApproveEstimate marks an estimate as approved and sets it as current
func (h *Handler) ApproveEstimate(c *gin.Context) {
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

	var state string
	var projectID int64
	err = h.db.QueryRow(`SELECT state, project_id FROM construction_estimate WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&state, &projectID)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be approved")
		return
	}

	userID, _ := middleware.GetUserID(c)

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Un-mark all other estimates for this project as not current
	_, err = tx.Exec(
		`UPDATE construction_estimate SET is_current = false WHERE project_id = $1 AND tenant_id = $2`,
		projectID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to un-mark estimates", "error", err)
		response.InternalError(c, "Failed to approve estimate")
		return
	}

	// Archive previously approved estimates
	_, err = tx.Exec(
		`UPDATE construction_estimate SET state = 'archived' WHERE project_id = $1 AND tenant_id = $2 AND state = 'approved' AND id != $3`,
		projectID, tenantID, id,
	)
	if err != nil {
		h.log.Error("Failed to archive estimates", "error", err)
		response.InternalError(c, "Failed to approve estimate")
		return
	}

	// Approve this estimate
	_, err = tx.Exec(
		`UPDATE construction_estimate SET state = 'approved', is_current = true, approved_by = $1, approved_date = NOW(), updated_date = NOW() WHERE id = $2`,
		userID, id,
	)
	if err != nil {
		h.log.Error("Failed to approve estimate", "error", err)
		response.InternalError(c, "Failed to approve estimate")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit approve", "error", err)
		response.InternalError(c, "Failed to approve estimate")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "estimate", "Smeta tasdiqlandi", "Estimate", id)

	response.Success(c, map[string]interface{}{
		"id":      id,
		"message": "Estimate approved successfully",
	})
}

// DeleteEstimate deletes a draft estimate
func (h *Handler) DeleteEstimate(c *gin.Context) {
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

	var state string
	var projectID int64
	err = h.db.QueryRow(`SELECT state, project_id FROM construction_estimate WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&state, &projectID)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be deleted")
		return
	}

	_, err = h.db.Exec(`DELETE FROM construction_estimate WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete estimate", "error", err)
		response.InternalError(c, "Failed to delete estimate")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "estimate", "Smeta o'chirildi", "Estimate", id)

	response.Success(c, map[string]interface{}{
		"message": "Estimate deleted successfully",
	})
}

// DuplicateEstimate creates a copy of an existing estimate as a new draft version
func (h *Handler) DuplicateEstimate(c *gin.Context) {
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

	// Get source estimate
	var src entity.ConstructionEstimate
	err = h.db.QueryRow(`
		SELECT id, tenant_id, project_id, building_id, version, name, overhead_pct, profit_pct, vat_pct, subcontract_id
		FROM construction_estimate WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&src.ID, &src.TenantID, &src.ProjectID, &src.BuildingID, &src.Version, &src.Name,
		&src.OverheadPct, &src.ProfitPct, &src.VatPct, &src.SubcontractID,
	)
	if err != nil {
		response.NotFound(c, "Source estimate not found")
		return
	}

	// Get next version
	var maxVersion int
	_ = h.db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM construction_estimate WHERE project_id = $1 AND tenant_id = $2`,
		src.ProjectID, tenantID,
	).Scan(&maxVersion)
	nextVersion := maxVersion + 1

	userID, _ := middleware.GetUserID(c)

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Create new estimate
	var newID int64
	err = tx.QueryRow(`
		INSERT INTO construction_estimate (
			tenant_id, project_id, building_id, version, name, state, is_current,
			overhead_pct, profit_pct, vat_pct,
			subcontract_id, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, 'draft', false, $6, $7, $8, $9, $10, NOW(), NOW())
		RETURNING id
	`, tenantID, src.ProjectID, src.BuildingID, nextVersion,
		fmt.Sprintf("%s (v%d)", src.Name, nextVersion),
		src.OverheadPct, src.ProfitPct, src.VatPct, src.SubcontractID, userID,
	).Scan(&newID)
	if err != nil {
		h.log.Error("Failed to create duplicate estimate", "error", err)
		response.InternalError(c, "Failed to duplicate estimate")
		return
	}

	// Copy lines. imported_quantity / imported_total (migration 400) are
	// carried over so the duplicated estimate still shows the file's
	// "Количество" and "Сметная стоимость" figures — those columns are
	// per-line metadata about the source XLSX, not derived state, so a
	// copy should preserve them verbatim.
	_, err = tx.Exec(`
		INSERT INTO construction_estimate_line (
			tenant_id, estimate_id, wbs_id, name, uom, quantity,
			material_rate, labor_rate, equipment_rate,
			unit_rate, total_amount, sort_order,
			imported_quantity, imported_total,
			created_date, updated_date
		)
		SELECT $1, $2, wbs_id, name, uom, quantity,
		       material_rate, labor_rate, equipment_rate,
		       unit_rate, total_amount, sort_order,
		       imported_quantity, imported_total,
		       NOW(), NOW()
		FROM construction_estimate_line
		WHERE estimate_id = $3
		ORDER BY sort_order
	`, tenantID, newID, id)
	if err != nil {
		h.log.Error("Failed to copy estimate lines", "error", err)
		response.InternalError(c, "Failed to duplicate estimate")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit duplicate", "error", err)
		response.InternalError(c, "Failed to duplicate estimate")
		return
	}

	// Recalculate totals for the new estimate
	h.recalculateEstimateTotals(newID)

	h.logConstructionActivity(tenantID, src.ProjectID, userID, "estimate",
		fmt.Sprintf("Smeta nusxalandi: v%d -> v%d", src.Version, nextVersion), "Estimate", newID)

	response.Success(c, map[string]interface{}{
		"id":      newID,
		"version": nextVersion,
	})
}

// =====================================================
// ESTIMATE LINE HANDLERS
// =====================================================

// ListEstimateLines returns lines for an estimate with pagination
func (h *Handler) ListEstimateLines(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	// Pagination — default 20 (mobile/on-demand clients page through in
	// 20-item chunks), max 5000 (the web Smeta boshqaruvi / Bosqichlar
	// loaders still request the full set explicitly so their totals,
	// search and Forma 2 stay correct).
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 5000 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// include_manual — when "false", hide rows created via the
	// individual line endpoints (CreateEstimateLine,
	// CloneEstimateLineByCode, CreateProjectResource — all flagged
	// is_manual = TRUE by migration 417). Defaults to true so the
	// Smeta boshqaruvi / Bosqichlar tabs keep seeing everything; the
	// Smetalar tab passes ?include_manual=false to limit its view to
	// the file's original imported content.
	includeManual := true
	if raw := strings.TrimSpace(c.Query("include_manual")); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			includeManual = v
		}
	}
	manualFilter := ""
	if !includeManual {
		manualFilter = " AND COALESCE(l.is_manual, FALSE) = FALSE"
	}
	// Count total — respect the same filter so pagination is consistent.
	countFilter := ""
	if !includeManual {
		countFilter = " AND COALESCE(is_manual, FALSE) = FALSE"
	}

	// Optional section filter (mobile Stage Works screen). When present, the
	// endpoint returns only the works of ONE Bosqichlar section (= the stage's
	// parent_item_number / section_key) PLUS all their descendant resource
	// lines, and paginates over the WORKS rather than the flat line list. This
	// lets mobile load one stage's ~200 works instead of looping the whole
	// block's ~16k lines. A work matches when its parent_item_number equals the
	// section exactly OR is a nested sub-section ("<section> › …"); the › is
	// U+203A, the app's path delimiter.
	section := strings.TrimSpace(c.Query("section"))

	var total int
	var sectionIDs []int64 // this page's works + ALL their descendants
	if section == "" {
		h.db.QueryRow(
			"SELECT COUNT(*) FROM construction_estimate_line WHERE estimate_id = $1 AND tenant_id = $2"+countFilter,
			estimateID, tenantID,
		).Scan(&total)
	} else {
		// Predicate that identifies a section's top-level works. countFilter
		// uses the un-aliased is_manual column, matching these queries.
		const workPredicate = `
			AND COALESCE(resource_type, '') = ''
			AND COALESCE(parent_line_id, 0) = 0
			AND (parent_item_number = $3
			     OR parent_item_number LIKE $3 || ' › %'
			     OR parent_item_number LIKE '% › ' || $3
			     OR parent_item_number LIKE '% › ' || $3 || ' › %')`

		// total = number of matched works (paging is over works).
		h.db.QueryRow(
			`SELECT COUNT(*) FROM construction_estimate_line
			 WHERE estimate_id = $1 AND tenant_id = $2`+countFilter+workPredicate,
			estimateID, tenantID, section,
		).Scan(&total)

		// This page's work IDs (ordered like the main list).
		var workIDs []int64
		wrows, werr := h.db.Query(
			`SELECT id FROM construction_estimate_line
			 WHERE estimate_id = $1 AND tenant_id = $2`+countFilter+workPredicate+`
			 ORDER BY sort_order ASC, id ASC
			 LIMIT $4 OFFSET $5`,
			estimateID, tenantID, section, pageSize, offset,
		)
		if werr != nil {
			h.log.Error("Failed to select section works", "error", werr)
			response.InternalError(c, "Failed to list estimate lines")
			return
		}
		for wrows.Next() {
			var id int64
			if wrows.Scan(&id) == nil {
				workIDs = append(workIDs, id)
			}
		}
		wrows.Close()

		// Expand to works + all descendants (resources / nested sub-stages) via
		// the parent_line_id chain, so each work card has its resource breakdown.
		if len(workIDs) > 0 {
			trows, terr := h.db.Query(`
				WITH RECURSIVE tree AS (
					SELECT id FROM construction_estimate_line
					WHERE id = ANY($1) AND tenant_id = $2
					UNION ALL
					SELECT c.id FROM construction_estimate_line c
					JOIN tree t ON c.parent_line_id = t.id
					WHERE c.tenant_id = $2
				)
				SELECT id FROM tree`,
				pq.Array(workIDs), tenantID,
			)
			if terr != nil {
				h.log.Error("Failed to expand section descendants", "error", terr)
				response.InternalError(c, "Failed to list estimate lines")
				return
			}
			for trows.Next() {
				var id int64
				if trows.Scan(&id) == nil {
					sectionIDs = append(sectionIDs, id)
				}
			}
			trows.Close()
		}
	}

	// Hoist the parent estimate's project_id and building_id out to
	// Go-side parameters. The ВОР fallback subquery below previously
	// re-derived them via three nested scalar subqueries per ROW; on
	// big tenants that ballooned ListEstimateLines latency to multiple
	// seconds and was the observed cause of Smeta boshqaruvi + Stages
	// page timeouts. One extra round-trip here saves N×3 wasted scalar
	// lookups in the main query (N = page size, often 5000).
	var estProjectID int64
	var estBuildingID sql.NullInt64
	if err := h.db.QueryRow(
		`SELECT project_id, building_id
		   FROM construction_estimate
		  WHERE id = $1 AND tenant_id = $2`,
		estimateID, tenantID,
	).Scan(&estProjectID, &estBuildingID); err != nil {
		h.log.Error("Failed to load estimate header for ListEstimateLines",
			"error", err, "estimate_id", estimateID)
		response.NotFound(c, "Estimate not found")
		return
	}
	var estBuildingArg interface{}
	if estBuildingID.Valid {
		estBuildingArg = estBuildingID.Int64
	} else {
		estBuildingArg = nil
	}

	// Default (no section): page the flat line list with LIMIT $3 OFFSET $4 and
	// the ВОР fallback's project/building at $5/$6 — unchanged from before.
	// Section mode: the $3 id-array carries this page's works + descendants, so
	// LIMIT/OFFSET drop out and $5/$6 shift down to $4/$5 (renumbered below).
	whereExtra := ""
	limitClause := "\n\t\tLIMIT $3 OFFSET $4"
	queryArgs := []interface{}{estimateID, tenantID, pageSize, offset, estProjectID, estBuildingArg}
	if section != "" {
		if len(sectionIDs) == 0 {
			// No works on this page (or empty section) — return empty, keep meta.
			response.Paginated(c, []entity.ConstructionEstimateLine{}, page, pageSize, total)
			return
		}
		whereExtra = " AND l.id = ANY($3)"
		limitClause = ""
		queryArgs = []interface{}{estimateID, tenantID, pq.Array(sectionIDs), estProjectID, estBuildingArg}
	}

	// Query paginated rows. We group each sub-line immediately after its
	// parent by sorting on the *parent's* sort_order (via self-join) plus the
	// parent's id as a stable group key. Within a group, the parent comes
	// first, then children by subline_seq.
	query := `
		SELECT l.id, l.tenant_id, l.estimate_id, l.wbs_id,
		       l.name, l.uom, l.quantity,
		       l.material_rate, l.labor_rate, l.equipment_rate,
		       l.unit_rate, l.total_amount, l.actual_amount,
		       COALESCE(l.code, ''), COALESCE(l.item_number, ''),
		       COALESCE(l.resource_type, ''), COALESCE(l.parent_item_number, ''),
		       l.parent_line_id, COALESCE(l.norm_rate, 0), COALESCE(l.subline_seq, 0),
		       COALESCE(l.quantity_override, FALSE),
		       COALESCE(l.material_type, 'standard'),
		       -- Norma anchor for the Smeta boshqaruvi NORMA pill.
		       -- Falls back from explicit anchor → matching ВОР work's
		       -- quantity. The ВОР fallback only fires for parent rows
		       -- (resource_type = '') and rescues Единич template-mode
		       -- imports whose own anchor is 0.
		       --
		       -- IMPORTANT: we deliberately do NOT fall back to
		       -- l.quantity here. l.quantity is the live FAKT ledger
		       -- (mirrored from done_quantity by UpdateEstimateLine), so
		       -- coupling NORMA to it makes NORMA visibly follow FAKT
		       -- as the user types — exactly the bug the user reported
		       -- ("when i change Fakt, Norma is also changing"). Skipping
		       -- the fallback means rows with no anchor and no ВОР match
		       -- render NORMA as "—", which is correct (we genuinely
		       -- don't know the planned amount) and harmless to the
		       -- numeric calculations that read original_quantity directly.
		       COALESCE(
		         NULLIF(l.original_quantity, 0),
		         CASE WHEN COALESCE(l.resource_type, '') = '' THEN (
		             -- One ВОР row per work — pick the first matching one
		             -- (lowest id ⇒ earliest import). Earlier this query
		             -- summed across all matches, which double-counted
		             -- when the user re-imported the same ВОР file (the
		             -- old rows aren't deleted because each import gets
		             -- a fresh estimate_id). Picking a single row keeps
		             -- NORMA aligned with the file's stated quantity for
		             -- this work, even if the user has re-imported.
		             -- Restricted to the SAME building when the Единич
		             -- estimate carries a building_id, so multi-block
		             -- projects don't cross-pollinate quantities.
		             --
		             -- $5 is the parent estimate's project_id and
		             -- $6 is its building_id (nullable) — both fetched
		             -- once in Go above. Migration 378 added a partial
		             -- index on construction_estimate(project_id,
		             -- tenant_id) WHERE source_type='vor' that this
		             -- clause uses.
		             SELECT vl.quantity
		             FROM construction_estimate_line vl
		             JOIN construction_estimate ve ON ve.id = vl.estimate_id
		             WHERE ve.tenant_id = l.tenant_id
		               AND ve.project_id = $5
		               AND LOWER(COALESCE(ve.source_type, '')) = 'vor'
		               AND (
		                 ve.building_id IS NULL
		                 OR ve.building_id = $6::bigint
		                 OR $6::bigint IS NULL
		               )
		               AND vl.name = l.name
		               AND COALESCE(vl.parent_item_number, '') = COALESCE(l.parent_item_number, '')
		               AND COALESCE(vl.resource_type, '') = ''
		               AND COALESCE(vl.parent_line_id, 0) = 0
		               AND vl.quantity > 0
		             ORDER BY vl.id ASC
		             LIMIT 1
		         ) ELSE NULL END,
		         0
		       ),
		       COALESCE(l.original_unit_rate, l.unit_rate),
		       -- Display-only fields from migration 400. Returned as raw
		       -- nullable numerics so the frontend can distinguish "no
		       -- imported value" from "imported zero" and render an
		       -- em-dash for the former.
		       l.imported_quantity,
		       l.imported_total,
		       COALESCE(l.approval_status, 'pending'),
		       COALESCE(l.done_quantity, 0),
		       l.sort_order, COALESCE(l.is_manual, FALSE),
		       l.created_date, l.updated_date,
		       COALESCE(w.code, '') as wbs_code,
		       COALESCE(w.name, '') as wbs_name
		FROM construction_estimate_line l
		LEFT JOIN construction_wbs w ON w.id = l.wbs_id
		LEFT JOIN construction_estimate_line p ON p.id = l.parent_line_id
		WHERE l.estimate_id = $1 AND l.tenant_id = $2` + manualFilter + whereExtra + `
		ORDER BY COALESCE(p.sort_order, l.sort_order) ASC,
		         COALESCE(l.parent_line_id, l.id) ASC,
		         (CASE WHEN l.parent_line_id IS NULL THEN 0 ELSE 1 END) ASC,
		         COALESCE(l.subline_seq, 0) ASC,
		         l.id ASC` + limitClause + `
	`
	if section != "" {
		// $3/$4 (page LIMIT/OFFSET) collapsed into the single $3 id-array, so
		// shift the ВОР fallback's project ($5→$4) and building ($6→$5)
		// placeholders down by one. NewReplacer does a single non-overlapping
		// pass, so $6→$5 is not re-rewritten. Only the ВОР subquery (and its
		// comment) reference $5/$6, so nothing else is affected.
		query = strings.NewReplacer("$5", "$4", "$6", "$5").Replace(query)
	}
	rows, err := h.db.Query(query, queryArgs...)
	if err != nil {
		h.log.Error("Failed to list estimate lines", "error", err)
		response.InternalError(c, "Failed to list estimate lines")
		return
	}
	defer rows.Close()

	lines := []entity.ConstructionEstimateLine{}
	for rows.Next() {
		var line entity.ConstructionEstimateLine
		if err := rows.Scan(
			&line.ID, &line.TenantID, &line.EstimateID, &line.WBSID,
			&line.Name, &line.UOM, &line.Quantity,
			&line.MaterialRate, &line.LaborRate, &line.EquipmentRate,
			&line.UnitRate, &line.TotalAmount, &line.ActualAmount,
			&line.Code, &line.ItemNumber,
			&line.ResourceType, &line.ParentItemNumber,
			&line.ParentLineID, &line.NormRate, &line.SublineSeq,
			&line.QuantityOverride,
			&line.MaterialType,
			&line.OriginalQuantity, &line.OriginalUnitRate,
			// Migration 400 — display-only nullable numerics. Scanning
			// into sql.NullFloat64 preserves the NULL distinction so
			// MarshalJSON emits `null` (not 0) when no file value exists.
			&line.ImportedQuantity, &line.ImportedTotal,
			&line.ApprovalStatus, &line.DoneQuantity,
			&line.SortOrder, &line.IsManual,
			&line.CreatedDate, &line.UpdatedDate,
			&line.WBSCode, &line.WBSName,
		); err != nil {
			continue
		}
		lines = append(lines, line)
	}

	// Load top-ups (migration 358) for every line in this estimate in a
	// single query and attach them to their parent line. We do this on
	// the HTTP path (not just the internal helper) because the
	// SmetaManagementTab consumes this endpoint directly and renders
	// indented top-up rows under each resource — without this, a newly
	// created top-up wouldn't show until a hard reload.
	topupRows, terr := h.db.Query(`
		SELECT t.id, t.tenant_id, t.estimate_line_id,
		       t.extra_quantity, t.new_price, t.ordered_at,
		       COALESCE(t.note, ''), t.created_by, t.created_date
		FROM construction_resource_topup t
		JOIN construction_estimate_line l ON l.id = t.estimate_line_id
		WHERE l.estimate_id = $1 AND t.tenant_id = $2
		ORDER BY t.ordered_at ASC, t.id ASC
	`, estimateID, tenantID)
	if terr == nil {
		defer topupRows.Close()
		bucket := make(map[int64][]entity.ResourceTopup)
		for topupRows.Next() {
			var t entity.ResourceTopup
			if scanErr := topupRows.Scan(
				&t.ID, &t.TenantID, &t.EstimateLineID,
				&t.ExtraQuantity, &t.NewPrice, &t.OrderedAt,
				&t.Note, &t.CreatedBy, &t.CreatedDate,
			); scanErr != nil {
				h.log.Error("Failed to scan resource topup", "error", scanErr)
				continue
			}
			bucket[t.EstimateLineID] = append(bucket[t.EstimateLineID], t)
		}
		for i := range lines {
			if list, ok := bucket[lines[i].ID]; ok {
				lines[i].Topups = list
			}
		}
	} else {
		// Don't fail the listing if the topup table doesn't exist yet
		// (e.g. migration 358 not yet applied) — just log and proceed.
		h.log.Error("Failed to load resource topups", "error", terr)
	}

	response.Paginated(c, lines, page, pageSize, total)
}

// ListProjectEstimateResources returns unique resource lines across all estimates for a project
// Filtered by resource_type (labor, equipment, material) via ?type= query param
func (h *Handler) ListProjectEstimateResources(c *gin.Context) {
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

	resourceType := c.Query("type") // "labor", "equipment", "material", or empty for all
	// Optional name/code substring search — sent by the picker so resources
	// past the alphabetical LIMIT cap still show up when the user types
	// their name. Without this, names starting with letters deep in the
	// Cyrillic alphabet (e.g. С — position 19) get cut off in projects with
	// hundreds of resources.
	searchQ := strings.TrimSpace(c.Query("q"))

	// Aggregate across duplicate rows by taking the MAX rate — so a resource
	// that has a non-zero rate in at least one estimate line is returned
	// with that rate (avoids picking a 0-rate skeleton row).
	//
	// Grouping key is (name, uom, resource_type) to match what the Resurslar
	// tab does, so each variant of the same name surfaces as its own picker
	// row. Earlier this grouped by name only, which collapsed manually-added
	// resources sharing a name (different uoms / different types) into one
	// row — they then "disappeared" from the picker even though the Resurslar
	// tab still showed them. User report:
	//     "some still not seeing manually added resources in add resource
	//      to work modal, but seeing in resources tab"
	query := `
		SELECT
			MIN(el.id) AS id,
			el.name,
			COALESCE(el.uom, '') AS uom,
			MAX(el.quantity) AS quantity,
			MAX(el.material_rate) AS material_rate,
			MAX(el.labor_rate) AS labor_rate,
			MAX(el.equipment_rate) AS equipment_rate,
			MAX(el.unit_rate) AS unit_rate,
			MAX(COALESCE(el.code, '')) AS code,
			COALESCE(el.resource_type, '') AS resource_type
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		WHERE e.tenant_id = $1
		  AND e.project_id = $2
		  AND el.name != ''
	`
	args := []interface{}{tenantID, projectID}
	argIdx := 3

	if searchQ != "" {
		// Case-insensitive substring match on name / uom / code. We filter
		// BEFORE the GROUP BY so groups whose name doesn't match are
		// excluded — that's what makes the LIMIT useful for narrow
		// searches and bypasses the alphabetical cap entirely when the
		// user types a query.
		query += fmt.Sprintf(`
			AND (
			  UPPER(COALESCE(el.name, '')) LIKE UPPER('%%' || $%d || '%%')
			  OR UPPER(COALESCE(el.uom, '')) LIKE UPPER('%%' || $%d || '%%')
			  OR UPPER(COALESCE(el.code, '')) LIKE UPPER('%%' || $%d || '%%')
			)
		`, argIdx, argIdx, argIdx)
		args = append(args, searchQ)
		argIdx++
	}

	if resourceType != "" {
		// Filter by resource type or UOM pattern
		switch strings.ToLower(resourceType) {
		case "labor":
			query += ` AND (LOWER(el.resource_type) = 'labor' OR (UPPER(el.uom) LIKE '%ЧЕЛ%' AND UPPER(el.uom) LIKE '%Ч%') OR (el.labor_rate > 0 AND el.material_rate = 0 AND el.equipment_rate = 0))`
		case "equipment":
			query += ` AND (LOWER(el.resource_type) = 'equipment' OR (UPPER(el.uom) LIKE '%МАШ%' AND UPPER(el.uom) LIKE '%Ч%') OR (el.equipment_rate > 0 AND el.material_rate = 0 AND el.labor_rate = 0))`
		case "material":
			query += ` AND LOWER(COALESCE(el.resource_type,'')) != 'labor' AND LOWER(COALESCE(el.resource_type,'')) != 'equipment'`
			query += ` AND NOT (UPPER(el.uom) LIKE '%ЧЕЛ%' AND UPPER(el.uom) LIKE '%Ч%')`
			query += ` AND NOT (UPPER(el.uom) LIKE '%МАШ%' AND UPPER(el.uom) LIKE '%Ч%')`
			query += ` AND NOT (el.labor_rate > 0 AND el.material_rate = 0 AND el.equipment_rate = 0)`
			query += ` AND NOT (el.equipment_rate > 0 AND el.material_rate = 0 AND el.labor_rate = 0)`
		default:
			query += fmt.Sprintf(` AND LOWER(el.resource_type) = $%d`, argIdx)
			args = append(args, strings.ToLower(resourceType))
			argIdx++
		}
	}

	// Higher cap than before (was 500) — projects with multiple єдинич +
	// ВОР + Ресурс imports can easily exceed 500 distinct
	// (name, uom, resource_type) tuples, and the alphabetical sort meant
	// rows past position 500 (often Cyrillic С/Т/У letters) silently
	// disappeared from the picker. With server-side `q=` search this cap
	// is rarely hit anyway.
	query += ` GROUP BY el.name, COALESCE(el.uom, ''), COALESCE(el.resource_type, '') ORDER BY UPPER(el.name), COALESCE(el.uom, '') LIMIT 5000`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list project estimate resources", "error", err, "query", query, "args", args)
		response.InternalError(c, "Failed to list resources")
		return
	}
	defer rows.Close()

	type ResourceLine struct {
		ID            int64   `json:"id"`
		Name          string  `json:"name"`
		UOM           string  `json:"uom"`
		Quantity      float64 `json:"quantity"`
		MaterialRate  float64 `json:"material_rate"`
		LaborRate     float64 `json:"labor_rate"`
		EquipmentRate float64 `json:"equipment_rate"`
		UnitRate      float64 `json:"unit_rate"`
		Code          string  `json:"code"`
		ResourceType  string  `json:"resource_type"`
	}

	resources := make([]ResourceLine, 0)
	for rows.Next() {
		var r ResourceLine
		if err := rows.Scan(&r.ID, &r.Name, &r.UOM, &r.Quantity,
			&r.MaterialRate, &r.LaborRate, &r.EquipmentRate,
			&r.UnitRate, &r.Code, &r.ResourceType); err != nil {
			h.log.Error("Scan resource row", "error", err)
			continue
		}
		resources = append(resources, r)
	}

	response.Success(c, resources)
}

// CreateProjectResource — POST /construction/projects/:id/resources
//
// Lets the user define a brand-new resource (labour entry, machine,
// or material) from inside the AddResourcePickerModal when the smeta
// import didn't include it. The new resource lands in a sentinel
// "catalog" estimate per project (created lazily) so the existing
// ListProjectEstimateResources aggregation picks it up everywhere —
// the modal's resource list refreshes and the resource becomes pickable
// for any work / sub-stage afterwards.
//
// For resource_type='material' we also auto-create an inventory product
// (mirrors the autoCreateProductsFromEstimateLines path used at import)
// so the warehouse reservation flow has a real product to bind to.
//
// Body: { name, uom, resource_type ('labor' | 'equipment' | 'material'),
//         unit_price?, material_type? }
func (h *Handler) CreateProjectResource(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req struct {
		Name         string  `json:"name" binding:"required"`
		UOM          string  `json:"uom"`
		ResourceType string  `json:"resource_type" binding:"required"`
		UnitPrice    float64 `json:"unit_price"`
		MaterialType string  `json:"material_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.UOM = strings.TrimSpace(req.UOM)
	rType := strings.ToLower(strings.TrimSpace(req.ResourceType))
	switch rType {
	case "labor", "equipment", "material", "machinery":
		// 'machinery' = строительные машины и механизмы (construction
		// machinery used to BUILD, MAШ.-Ч hours) — added as a separate
		// bucket alongside 'equipment' (стационарное оборудование). Both
		// classify into the MASHINA section on the frontend.
		// ok
	default:
		response.BadRequest(c, "resource_type must be one of: labor, equipment, material, machinery")
		return
	}
	if req.Name == "" {
		response.BadRequest(c, "name is required")
		return
	}
	if req.UnitPrice < 0 {
		response.BadRequest(c, "unit_price must be >= 0")
		return
	}

	// Material sub-bucket — only meaningful for material rows. Default to
	// 'standard'; the modal can pass 'cable' / 'equipment' explicitly later.
	matType := strings.ToLower(strings.TrimSpace(req.MaterialType))
	switch matType {
	case "standard", "equipment", "cable", "metal", "import":
		// ok
	default:
		matType = "standard"
	}

	// Resolve / lazily create the project's catalog estimate (source_type
	// 'catalog'). Only one per (tenant, project). Block-selector and
	// estimates list filter this out — see SmetaManagementTab.
	var catalogID int64
	if err := h.db.QueryRow(`
		SELECT id FROM construction_estimate
		WHERE tenant_id = $1 AND project_id = $2 AND LOWER(COALESCE(source_type, '')) = 'catalog'
		ORDER BY id ASC LIMIT 1
	`, tenantID, projectID).Scan(&catalogID); err != nil {
		// Not found — create it. The unique index
		// idx_construction_estimate_version covers (project_id, version),
		// and the column defaults to 1 — which collides with the project's
		// existing ВОР/Единич at version 1. We pick MAX(version)+1 so the
		// catalog row slots in after whatever's already there. The
		// version itself is meaningless for catalog estimates (they're a
		// hidden bucket for project-level resources) but Postgres still
		// needs it to be unique per project.
		err = h.db.QueryRow(`
			INSERT INTO construction_estimate (
				tenant_id, project_id, version, name, state, source_type,
				overhead_pct, profit_pct, vat_pct,
				created_by, created_date, updated_date
			) VALUES (
				$1, $2,
				COALESCE((SELECT MAX(version) FROM construction_estimate WHERE project_id = $2), 0) + 1,
				'__catalog__', 'draft', 'catalog',
				0, 0, 0,
				$3, NOW(), NOW()
			)
			RETURNING id
		`, tenantID, projectID, uuidArg(userID)).Scan(&catalogID)
		if err != nil {
			h.log.Error("Failed to create catalog estimate", "error", err)
			response.InternalError(c, "Failed to create resource")
			return
		}
	}

	// Insert a standalone line into the catalog estimate. quantity = 0 so
	// it doesn't pollute Form 2 totals. parent_line_id stays NULL.
	matRate, labRate, eqRate := 0.0, 0.0, 0.0
	switch rType {
	case "labor":
		labRate = req.UnitPrice
	case "equipment", "machinery":
		// Both stationary equipment and construction machinery park
		// their unit price into equipment_rate — the frontend Form 2
		// rollups already sum equipment_rate × quantity for both.
		eqRate = req.UnitPrice
	case "material":
		matRate = req.UnitPrice
	}

	// is_manual = TRUE (migration 417) — CreateProjectResource is only
	// reachable from the AddResourcePickerModal "+" affordance, so every
	// row is by definition user-added. The Smetalar tab filters these out.
	var lineID int64
	err = h.db.QueryRow(`
		INSERT INTO construction_estimate_line (
			tenant_id, estimate_id, name, uom, quantity,
			material_rate, labor_rate, equipment_rate,
			unit_rate, total_amount, resource_type, material_type,
			parent_item_number, sort_order, is_manual, created_date, updated_date
		) VALUES (
			$1, $2, $3, $4, 0,
			$5, $6, $7,
			$8, 0, $9, $10,
			'__catalog__', 0, TRUE, NOW(), NOW()
		)
		RETURNING id
	`, tenantID, catalogID, req.Name, req.UOM,
		matRate, labRate, eqRate,
		req.UnitPrice, rType, matType).Scan(&lineID)
	if err != nil {
		h.log.Error("Failed to insert catalog resource", "error", err, "name", req.Name)
		response.InternalError(c, "Failed to create resource")
		return
	}

	// For material resources, also create a matching product in inventory
	// so the warehouse reservation flow has something to bind to. Reuses
	// the same helper that handles bulk imports from Ресурс estimates.
	if rType == "material" {
		h.autoCreateProductsFromEstimateLines(tenantID, orgID, userID,
			[]entity.CreateEstimateLineInput{{
				Name:         req.Name,
				UOM:          req.UOM,
				MaterialRate: matRate,
				LaborRate:    labRate,
				EquipmentRate: eqRate,
				ResourceType: rType,
			}})
	}

	response.Created(c, map[string]interface{}{
		"id":            lineID,
		"name":          req.Name,
		"uom":           req.UOM,
		"resource_type": rType,
		"material_type": matType,
		"unit_rate":     req.UnitPrice,
	})
}

// CreateEstimateLine creates a new line in an estimate
func (h *Handler) CreateEstimateLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	// Check state + capture project_id for the audit row.
	var state string
	var projectID int64
	err = h.db.QueryRow(`SELECT state, project_id FROM construction_estimate WHERE id = $1 AND tenant_id = $2`, estimateID, tenantID).Scan(&state, &projectID)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	var req entity.CreateEstimateLineInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// ──────────────── Sub-line (подкатор) resolution ────────────────
	// When the client asked to bind this new line under a parent, fetch the
	// parent, auto-assign subline_seq + item_number, and re-derive quantity
	// from parent.quantity × norm_rate. See migration 332.
	var (
		parentLineIDSQL   sql.NullInt64
		parentItemNumber  = req.ParentItemNumber
		parentQuantity    float64
		sublineSeq        int
		assignedItemNum   = req.ItemNumber
	)
	// parent metadata for the YAKUNIY-trigger path below.
	var parentApprovalStatus string
	var parentDoneQuantity float64
	var parentSectionPath string
	var parentName string
	// Parent's OWN item_number (e.g. "13"), captured here at the outer
	// scope so the later tx block can compose the "13-N" suffix once
	// subline_seq is known. Distinct from `parentItemNumber`, which is
	// the value stored on the new row's parent_item_number column — for
	// sub-stages that's the parent's parent_item_number (the SECTION path),
	// not the parent's item_number.
	var parentOwnItemNumber string
	if req.ParentLineID > 0 {
		var pItem, pUom, pParentItem sql.NullString
		var pQty float64
		var pEstimateID int64
		var pSortOrder int
		err := h.db.QueryRow(`
			SELECT estimate_id, COALESCE(item_number, ''), COALESCE(uom, ''),
			       COALESCE(quantity, 0), COALESCE(sort_order, 0),
			       COALESCE(parent_item_number, ''),
			       COALESCE(approval_status, ''), COALESCE(done_quantity, 0),
			       COALESCE(name, '')
			FROM construction_estimate_line
			WHERE id = $1 AND tenant_id = $2
		`, req.ParentLineID, tenantID).Scan(
			&pEstimateID, &pItem, &pUom, &pQty, &pSortOrder, &pParentItem,
			&parentApprovalStatus, &parentDoneQuantity, &parentName,
		)
		parentSectionPath = pParentItem.String
		if err == sql.ErrNoRows {
			response.BadRequest(c, "Parent line not found")
			return
		}
		if err != nil {
			h.log.Error("Failed to load parent estimate line", "error", err)
			response.InternalError(c, "Failed to load parent line")
			return
		}
		if pEstimateID != estimateID {
			response.BadRequest(c, "Parent line belongs to a different estimate")
			return
		}

		parentLineIDSQL = sql.NullInt64{Int64: req.ParentLineID, Valid: true}
		// parent_item_number resolution depends on what KIND of child this is:
		//   • Resource child (Mehnat / Mashina / Material — resource_type set)
		//     ─ tracks which work owns it via parent.item_number ("13"), so
		//       legacy fallbacks that bucket children by parent_item_number
		//       still work when parent_line_id is absent. Existing behavior.
		//   • Sub-stage (resource_type = '' — created via "Yangi etap" in
		//     Smeta boshqaruvi) — must inherit the parent work's SECTION
		//     path ("СЕКЦИЯ №1 › ФУНДАМЕНТЫ") so deriveStages() in the
		//     Bosqichlar tab buckets it inside the same stage as its
		//     parent. Otherwise the sub-stage ends up under a fake stage
		//     called "13" (parent.item_number), and the user reported it
		//     as missing from Bosqichlar entirely.
		isSubStageNew := strings.TrimSpace(req.ResourceType) == "" && req.NormRate == 0
		if isSubStageNew {
			parentItemNumber = pParentItem.String
		} else {
			parentItemNumber = pItem.String
		}
		parentOwnItemNumber = pItem.String
		parentQuantity = pQty

		// Inherit the parent's sort_order so the backend ORDER BY places the
		// sub-line next to its parent (without this, newly-created sub-lines
		// default to sort_order = 0 and drift to the top of the list when other
		// rows have higher sort_orders — which was the "3-1 appears before 1"
		// bug). Clients can still override by sending their own sort_order.
		if req.SortOrder == 0 {
			req.SortOrder = pSortOrder
		}

		// subline_seq is auto-assigned inside the same transaction as the
		// INSERT below (see "tx, err := h.db.Begin()" further down) so
		// concurrent "+ Qo'shimcha resurs" / "+ Yangi etap" clicks under
		// the same parent can't both read MAX = N and both insert N+1 —
		// the second one would otherwise crash on the
		// uq_estimate_line_parent_seq (parent_line_id, subline_seq) unique
		// constraint (migration 332) and surface as the intermittent
		// "adding resource sometimes fails" 500 the field team reported.
		// The actual SELECT-MAX runs after the FOR UPDATE row lock is
		// taken so it sees every concurrent sibling.
		//
		// item_number composition also moves under the lock — without the
		// new subline_seq it'd resolve to the wrong "13-N" suffix.

		// Sub-line quantity = parent.quantity × norm_rate, denormalized so
		// existing reports don't need to know about the sub-line model.
		//
		// BUT: when the client switches the sub-line into MANUAL mode
		// (QuantityOverride == true, migration 342), respect whatever
		// Quantity the user entered and do NOT re-derive from parent×norm.
		// This covers the "10 hours of pump time independent of parent
		// volume" case raised by the field team.
		//
		// Defensive carve-out: if the client SAID it's an override but
		// also sent quantity ≤ 0, that's almost always a stale bundle
		// from when the AddResourcePickerModal blindly stamped
		// quantity_override = true regardless of the empty Total Qty
		// field. Treat it as "no override" so the cascade kicks in and
		// the new row picks up parent.quantity × norm_rate. Legitimate
		// manual overrides always carry a non-zero quantity (a user
		// typing an explicit "0" intends to delete the row, not pin it).
		if req.QuantityOverride && req.Quantity <= 0 && req.NormRate > 0 {
			req.QuantityOverride = false
		}
		if req.NormRate > 0 && !req.QuantityOverride {
			req.Quantity = parentQuantity * req.NormRate
		}
		// Inherit parent UOM when the client didn't override it.
		if req.UOM == "" && pUom.String != "" {
			req.UOM = pUom.String
		}
	}

	// Top-level work without parent — append to the END of its section
	// instead of letting sort_order default to 0 (which would put the new
	// "+ Ish" line ABOVE every imported row in that section, surfaced by
	// the user as the "123" row appearing at the top of ФУНДАМЕНТЫ before
	// row #8). We take MAX(sort_order)+1 inside the same estimate AND the
	// same parent_item_number — scoped this way so:
	//   • Adding a work to ФУНДАМЕНТЫ doesn't jump it past unrelated
	//     ЗЕМЛЯННЫЕ work rows that happen to have higher sort_order.
	//   • Two sections with overlapping sort_order numbers don't fight.
	// Skip when client pinned a SortOrder explicitly (≥ 1) — that
	// overrides our default.
	if req.ParentLineID == 0 && req.SortOrder == 0 {
		var maxSort int
		_ = h.db.QueryRow(`
			SELECT COALESCE(MAX(sort_order), 0)
			FROM construction_estimate_line
			WHERE estimate_id = $1
			  AND tenant_id   = $2
			  AND parent_line_id IS NULL
			  AND COALESCE(parent_item_number, '') = $3
		`, estimateID, tenantID, req.ParentItemNumber).Scan(&maxSort)
		req.SortOrder = maxSort + 1
	}

	// UnitPrice convenience: if the caller set it, map it onto the rate column
	// that matches resource_type. This keeps the existing unit_rate =
	// material_rate + labor_rate + equipment_rate invariant intact.
	if req.UnitPrice > 0 {
		switch strings.ToLower(strings.TrimSpace(req.ResourceType)) {
		case "equipment", "machine", "mashina", "masina":
			req.EquipmentRate = req.UnitPrice
		case "labor", "ish", "ishchi", "worker":
			req.LaborRate = req.UnitPrice
		case "material", "materialy", "mat":
			req.MaterialRate = req.UnitPrice
		default:
			// Unknown resource type — default to equipment (machine), since that's
			// what BHMS breakdowns most often describe.
			req.EquipmentRate = req.UnitPrice
		}
	}

	// Calculate rates
	unitRate := req.MaterialRate + req.LaborRate + req.EquipmentRate
	totalAmount := unitRate * req.Quantity

	uom := req.UOM
	if uom == "" {
		uom = "шт"
	}

	// Insert runs in a transaction so the subline_seq MAX+1 assign + INSERT
	// happen atomically against the parent row. When req.ParentLineID > 0
	// we take a FOR UPDATE row lock on the parent first; concurrent
	// "+ Qo'shimcha resurs" / "+ Yangi etap" clicks under the same parent
	// queue on that lock instead of both reading MAX = N and both inserting
	// N+1 (the second of which would crash on the uq_estimate_line_parent_seq
	// unique constraint from migration 332). Sibling inserts under a
	// DIFFERENT parent take a different row lock and proceed in parallel,
	// so this doesn't introduce a global bottleneck.
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin estimate line insert tx", "error", err)
		response.InternalError(c, "Failed to create estimate line")
		return
	}
	txCommitted := false
	defer func() {
		if !txCommitted {
			_ = tx.Rollback()
		}
	}()

	if req.ParentLineID > 0 {
		// FOR UPDATE row lock — serializes concurrent sibling inserts.
		if _, lockErr := tx.Exec(`
			SELECT 1 FROM construction_estimate_line
			WHERE id = $1 AND tenant_id = $2
			FOR UPDATE
		`, req.ParentLineID, tenantID); lockErr != nil {
			h.log.Error("Failed to lock parent line for sub-insert", "error", lockErr, "parent_line_id", req.ParentLineID)
			response.InternalError(c, "Failed to create estimate line")
			return
		}

		// Inside the lock we can safely MAX+1 — no other writer can land a
		// sibling between this read and our INSERT.
		if seqErr := tx.QueryRow(`
			SELECT COALESCE(MAX(subline_seq), 0) + 1
			FROM construction_estimate_line
			WHERE parent_line_id = $1 AND tenant_id = $2
		`, req.ParentLineID, tenantID).Scan(&sublineSeq); seqErr != nil {
			sublineSeq = 1
		}

		// Compose item_number now that subline_seq is final. We deliberately
		// only auto-fill when the client left it blank — explicit values
		// from the modal still win.
		if assignedItemNum == "" && parentOwnItemNumber != "" {
			assignedItemNum = fmt.Sprintf("%s-%d", parentOwnItemNumber, sublineSeq)
		}
	}

	var lineID int64
	err = tx.QueryRow(`
		INSERT INTO construction_estimate_line (
			tenant_id, estimate_id, wbs_id, name, uom, quantity,
			material_rate, labor_rate, equipment_rate,
			unit_rate, total_amount, code, item_number,
			resource_type, parent_item_number, sort_order,
			parent_line_id, norm_rate, subline_seq, quantity_override,
			is_manual,
			created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		          $17, $18, $19, $20,
		          TRUE,
		          NOW(), NOW())
		RETURNING id
	`, tenantID, estimateID, nullInt64FromVal(req.WBSID),
		req.Name, uom, req.Quantity,
		req.MaterialRate, req.LaborRate, req.EquipmentRate,
		unitRate, totalAmount, nullStringFromVal(req.Code), nullStringFromVal(assignedItemNum),
		nullStringFromVal(req.ResourceType), nullStringFromVal(parentItemNumber), req.SortOrder,
		parentLineIDSQL, req.NormRate, sublineSeq, req.QuantityOverride,
	).Scan(&lineID)
	// is_manual = TRUE (migration 417) — every CreateEstimateLine call
	// comes from a UI affordance ("+ Ish", "+ Yangi qo'shimcha etap",
	// "+ Qo'shimcha resurs"), so the row is by definition user-added.
	// The Smetalar tab filters by is_manual = FALSE to hide these.

	if err != nil {
		h.log.Error("Failed to create estimate line", "error", err)
		response.InternalError(c, "Failed to create estimate line")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit estimate line insert tx", "error", err)
		response.InternalError(c, "Failed to create estimate line")
		return
	}
	txCommitted = true

	// Recalculate estimate totals
	h.recalculateEstimateTotals(estimateID)

	// YAKUNIY catch-up: when an ad-hoc resource is grafted onto a work
	// that's already in confirmed_engineer state (e.g. the foreman
	// remembered a missing material after final sign-off), the normal
	// reserve→confirm pipeline never runs for it because both events are
	// already in the past. Process the resource immediately so:
	//   • inventory of the matching product is decremented (allowing
	//     negative balance per product policy)
	//   • a single approved expense_line is written with the new
	//     resource's consumed cost
	// We only run this when:
	//   - parent is confirmed_engineer
	//   - this row is a resource (resource_type set, norm_rate or override
	//     gives a non-zero consumed quantity)
	if parentLineIDSQL.Valid &&
		parentApprovalStatus == "confirmed_engineer" &&
		strings.TrimSpace(req.ResourceType) != "" {
		// Consumed = override quantity if user pinned one, else
		// parent.done_quantity × norm_rate (cascade rule).
		consumed := req.Quantity
		if !req.QuantityOverride {
			consumed = parentDoneQuantity * req.NormRate
		}
		// We deliberately drop the legacy `req.UnitPrice > 0` guard from
		// this gate. Inventory decrement is a physical-reality
		// bookkeeping step — when the foreman says "I consumed 330 шт of
		// test 11" the warehouse balance has to follow even if the user
		// didn't bother to fill in a price for that ad-hoc resource.
		// processYakuniyAdHocResource itself still gates the EXPENSE
		// write on cost > 0 (an empty price means there's no money line
		// to record), so a price-less resource produces an inventory
		// decrement without a phantom 0-сум expense row. The user-
		// reported case "test 11 — REJA SARF 11 / FAKT SARF 330,
		// inventory still 0" is exactly this path: ad-hoc material
		// added with no unit price never triggered the engine.
		if consumed > 0 {
			h.processYakuniyAdHocResource(
				c, tenantID, estimateID, req.ParentLineID, lineID,
				req.Name, uom, req.UnitPrice, consumed, parentSectionPath,
			)
		}
		_ = parentName // reserved for future audit description shapes
	}

	// Audit: parent rows are sub-stages, child rows are resources.
	userIDLog, _ := middleware.GetUserID(c)
	userNameLog := c.GetString("user_name")
	action := "subwork_add"
	if parentLineIDSQL.Valid {
		action = "res_add"
	}
	h.logSmetaAudit(tenantID, projectID, &estimateID, action, req.Name, &lineID,
		"", strconv.FormatFloat(req.Quantity, 'f', -1, 64),
		"Yangi qator qo'shildi", userIDLog, userNameLog)

	response.Success(c, map[string]interface{}{
		"id":          lineID,
		"item_number": assignedItemNum,
		"subline_seq": sublineSeq,
	})
}

// CloneEstimateLineByCode — POST /construction/estimates/:id/lines/clone-by-code
//
// Creates a new estimate line and, if any existing parent line in the SAME
// project carries the same `source_code`, copies every one of that line's
// resource sub-lines onto the new line in one round-trip. The new line itself
// inherits the source's material/labor/equipment rates, uom and resource_type
// unless the request explicitly overrides them.
//
// When no source line is found, the endpoint just creates the new line with
// the supplied fields (same as a plain CreateEstimateLine) so the frontend
// can call this unconditionally on every "+ Ish" / "+ Yangi qo'shimcha etap"
// submission and let the backend decide whether cloning applies.
//
// Body: {
//   source_code:        string  // required — the code to match against existing lines
//   parent_line_id?:    int64   // attach the new line as a sub-stage of this work
//   parent_item_number?: string // top-level: lands the new line under this section
//   item_number?:       string  // explicit numbering (else auto-derived from parent)
//   name?:              string  // overrides source's name
//   uom?:               string  // overrides source's uom
//   code?:              string  // overrides source's code on the new row (defaults to source_code)
//   quantity?:          float64 // overrides quantity (defaults to 0)
// }
//
// Returns: { id, cloned_resources, source_id?, source_name? }
func (h *Handler) CloneEstimateLineByCode(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	// Pull the destination estimate so we can scope the code lookup to its
	// project. Clone semantics are project-wide (the user added a work in
	// section A and may have shifted to section B keeping the same SHRNK
	// code), but never cross-tenant or cross-project.
	var destState string
	var destProjectID int64
	err = h.db.QueryRow(`
		SELECT state, project_id FROM construction_estimate
		WHERE id = $1 AND tenant_id = $2
	`, estimateID, tenantID).Scan(&destState, &destProjectID)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}
	if destState != "draft" {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	var req struct {
		SourceCode       string   `json:"source_code" binding:"required"`
		ParentLineID     int64    `json:"parent_line_id"`
		ParentItemNumber string   `json:"parent_item_number"`
		ItemNumber       string   `json:"item_number"`
		Name             string   `json:"name"`
		UOM              string   `json:"uom"`
		Code             string   `json:"code"`
		Quantity         *float64 `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	srcCode := strings.TrimSpace(req.SourceCode)
	if srcCode == "" {
		response.BadRequest(c, "source_code is required")
		return
	}

	// Locate a line in this project whose code matches. We accept BOTH
	// top-level parents AND sub-stages (sub-stages are sub-lines of a
	// work but can themselves carry resources — the user's "1122" etap
	// added via "+ Yangi bosqich qo'shish" is exactly this shape). The
	// "MOST children" tie-break naturally pushes resource leaf rows
	// (zero children) to the back, so we never accidentally clone from
	// a resource row that has no payload to copy.
	//
	// Tie-break: prefer the candidate with the MOST direct children —
	// the user almost certainly wants to clone from a line that already
	// has resources, not from a blank stub they created earlier under
	// the same code. Within the same child-count tier we fall back to
	// the oldest id (earliest import).
	//
	// Case- and whitespace-insensitive comparison.
	var (
		srcID                                          int64
		srcName, srcUOM, srcResourceType, srcMaterial string
		srcMatRate, srcLabRate, srcEqRate             float64
		srcChildCount                                  int64
	)
	err = h.db.QueryRow(`
		SELECT l.id,
		       COALESCE(l.name, ''),
		       COALESCE(l.uom, ''),
		       COALESCE(l.resource_type, ''),
		       COALESCE(l.material_type, 'standard'),
		       COALESCE(l.material_rate, 0),
		       COALESCE(l.labor_rate, 0),
		       COALESCE(l.equipment_rate, 0),
		       (SELECT COUNT(*) FROM construction_estimate_line c
		         WHERE c.tenant_id = l.tenant_id
		           AND c.parent_line_id = l.id) AS child_count
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE e.tenant_id = $1
		  AND e.project_id = $2
		  AND UPPER(TRIM(COALESCE(l.code, ''))) = UPPER(TRIM($3))
		ORDER BY child_count DESC, l.id ASC
		LIMIT 1
	`, tenantID, destProjectID, srcCode).Scan(
		&srcID, &srcName, &srcUOM, &srcResourceType, &srcMaterial,
		&srcMatRate, &srcLabRate, &srcEqRate, &srcChildCount,
	)
	hasSource := err == nil

	// Fill in defaults from the source when the user left a field blank.
	name := strings.TrimSpace(req.Name)
	uom := strings.TrimSpace(req.UOM)
	newCode := strings.TrimSpace(req.Code)
	if newCode == "" {
		newCode = srcCode
	}
	if hasSource {
		if name == "" {
			name = srcName
		}
		if uom == "" {
			uom = srcUOM
		}
	}
	if name == "" {
		response.BadRequest(c, "name is required")
		return
	}

	// Run the clone inside a single transaction so a partial copy never
	// leaks (either the new work + every cloned resource lands, or none).
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to open clone transaction", "error", err)
		response.InternalError(c, "Failed to clone line")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var parentLineArg interface{}
	if req.ParentLineID > 0 {
		parentLineArg = req.ParentLineID
	}
	var parentItemArg interface{}
	if strings.TrimSpace(req.ParentItemNumber) != "" {
		parentItemArg = req.ParentItemNumber
	}
	itemNumberArg := strings.TrimSpace(req.ItemNumber)
	qty := 0.0
	if req.Quantity != nil {
		qty = *req.Quantity
	}

	matRate := srcMatRate
	labRate := srcLabRate
	eqRate := srcEqRate
	if !hasSource {
		matRate, labRate, eqRate = 0, 0, 0
	}
	unitRate := matRate + labRate + eqRate

	// When the new line is a sub-line (parent_line_id set), auto-assign
	// the next subline_seq for that parent. Without this the column
	// defaults to 0 and conflicts with the (parent_line_id, subline_seq)
	// unique index from migration 332 if another sub-line already exists
	// — surfaces as "duplicate key value violates unique constraint
	// uq_estimate_line_parent_seq" 500 on the clone endpoint.
	var newSublineSeq int
	if req.ParentLineID > 0 {
		if err := tx.QueryRow(`
			SELECT COALESCE(MAX(subline_seq), 0) + 1
			FROM construction_estimate_line
			WHERE parent_line_id = $1 AND tenant_id = $2
		`, req.ParentLineID, tenantID).Scan(&newSublineSeq); err != nil {
			newSublineSeq = 1
		}
	}

	// Insert the new line. Mirrors the column set CreateEstimateLine uses
	// so trigger-set defaults (original_quantity / original_unit_rate from
	// migration 349) and constraints stay consistent.
	// is_manual = TRUE (migration 417) — the clone-by-code endpoint is
	// only ever called from the UI, so the new row is user-added and
	// must be hidden from the Smetalar tab.
	var newID int64
	err = tx.QueryRow(`
		INSERT INTO construction_estimate_line (
			tenant_id, estimate_id, parent_line_id, parent_item_number,
			item_number, code, name, uom,
			resource_type, material_type,
			quantity, quantity_override,
			material_rate, labor_rate, equipment_rate,
			unit_rate, sort_order,
			subline_seq,
			total_amount,
			norm_rate,
			is_manual,
			created_date, updated_date
		) VALUES (
			$1, $2, $3, $4,
			NULLIF($5, ''), NULLIF($6, ''), $7, $8,
			$9, COALESCE(NULLIF($10, ''), 'standard'),
			$11, TRUE,
			$12, $13, $14,
			$15, 0,
			$17,
			$16,
			0,
			TRUE,
			NOW(), NOW()
		) RETURNING id
	`,
		tenantID, estimateID, parentLineArg, parentItemArg,
		itemNumberArg, newCode, name, uom,
		srcResourceType, srcMaterial,
		qty,
		matRate, labRate, eqRate,
		unitRate, qty*unitRate,
		newSublineSeq,
	).Scan(&newID)
	if err != nil {
		h.log.Error("Failed to insert cloned line", "error", err, "source_code", srcCode)
		response.InternalError(c, "Failed to create line")
		return
	}

	// Clone every resource sub-line of the source onto the new line. We
	// preserve norm_rate, unit_rate, material_type, resource_type, uom and
	// the per-component rates exactly. quantity is re-derived from the new
	// parent's qty × norm_rate so the JAMI column matches the new work's
	// scale (this is the same cascade rule UpdateEstimateLine uses).
	clonedCount := 0
	if hasSource {
		rows, qerr := tx.Query(`
			SELECT COALESCE(name, ''), COALESCE(uom, ''),
			       COALESCE(resource_type, ''),
			       COALESCE(material_type, 'standard'),
			       COALESCE(material_rate, 0), COALESCE(labor_rate, 0),
			       COALESCE(equipment_rate, 0), COALESCE(unit_rate, 0),
			       COALESCE(norm_rate, 0)
			FROM construction_estimate_line
			WHERE tenant_id = $1
			  AND parent_line_id = $2
			ORDER BY subline_seq ASC, id ASC
		`, tenantID, srcID)
		if qerr != nil {
			h.log.Error("Failed to read source children", "error", qerr, "source_id", srcID)
			response.InternalError(c, "Failed to clone children")
			return
		}
		type childRow struct {
			Name, UOM, ResType, MatType                       string
			MatRate, LabRate, EqRate, UnitRate, NormRate      float64
		}
		var children []childRow
		for rows.Next() {
			var ch childRow
			if scanErr := rows.Scan(
				&ch.Name, &ch.UOM, &ch.ResType, &ch.MatType,
				&ch.MatRate, &ch.LabRate, &ch.EqRate, &ch.UnitRate, &ch.NormRate,
			); scanErr != nil {
				rows.Close()
				h.log.Error("Failed to scan source child", "error", scanErr)
				response.InternalError(c, "Failed to clone children")
				return
			}
			children = append(children, ch)
		}
		rows.Close()

		for i, ch := range children {
			childQty := qty * ch.NormRate
			_, ierr := tx.Exec(`
				INSERT INTO construction_estimate_line (
					tenant_id, estimate_id, parent_line_id, parent_item_number,
					item_number, name, uom,
					resource_type, material_type,
					quantity, quantity_override,
					material_rate, labor_rate, equipment_rate,
					unit_rate, total_amount,
					norm_rate, subline_seq, sort_order,
					is_manual,
					created_date, updated_date
				) VALUES (
					$1, $2, $3, NULL,
					NULL, $4, $5,
					$6, $7,
					$8, FALSE,
					$9, $10, $11,
					$12, $13,
					$14, $15, 0,
					TRUE,
					NOW(), NOW()
				)
			`,
				tenantID, estimateID, newID,
				ch.Name, ch.UOM,
				ch.ResType, ch.MatType,
				childQty,
				ch.MatRate, ch.LabRate, ch.EqRate,
				ch.UnitRate, childQty*ch.UnitRate,
				ch.NormRate, i+1,
			)
			if ierr != nil {
				h.log.Error("Failed to insert cloned child", "error", ierr, "source_id", srcID, "i", i)
				response.InternalError(c, "Failed to clone children")
				return
			}
			clonedCount++
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit clone transaction", "error", err)
		response.InternalError(c, "Failed to clone line")
		return
	}
	committed = true

	// Recompute estimate totals so the topbar pills and Form 2 rollups
	// pick up the freshly cloned resources without a manual refresh.
	h.recalculateEstimateTotals(estimateID)

	out := map[string]interface{}{
		"id":               newID,
		"cloned_resources": clonedCount,
	}
	if hasSource {
		out["source_id"] = srcID
		out["source_name"] = srcName
	}
	response.Success(c, out)
}

// BulkCreateEstimateLines creates multiple estimate lines in a single transaction
func (h *Handler) BulkCreateEstimateLines(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	// Check state + grab project_id (needed for the post-import price
	// propagation step that copies Ресурс prices into Единич sub-lines).
	var state string
	var importProjectID int64
	err = h.db.QueryRow(`SELECT state, project_id FROM construction_estimate WHERE id = $1 AND tenant_id = $2`, estimateID, tenantID).Scan(&state, &importProjectID)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	var req entity.BulkCreateEstimateLinesInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if len(req.Lines) == 0 {
		response.BadRequest(c, "No lines provided")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalError(c, "Failed to start import")
		return
	}
	defer tx.Rollback()

	// If replace mode, delete all existing lines first
	if req.Replace {
		_, err := tx.Exec(`DELETE FROM construction_estimate_line WHERE estimate_id = $1 AND tenant_id = $2`, estimateID, tenantID)
		if err != nil {
			h.log.Error("Failed to clear existing estimate lines", "error", err)
			response.InternalError(c, "Failed to clear existing lines")
			return
		}
	}

	// Update source_type on the estimate if provided (important for re-imports into existing estimates)
	if req.SourceType != "" {
		_, stErr := tx.Exec(`UPDATE construction_estimate SET source_type = $1 WHERE id = $2 AND tenant_id = $3`,
			req.SourceType, estimateID, tenantID)
		if stErr != nil {
			h.log.Error("Failed to update source_type on estimate", "error", stErr, "source_type", req.SourceType)
		}
	}

	// Imported-budget capture (migration 369). Only updates the columns
	// that arrived non-zero so non-Ресурс re-imports don't wipe an
	// already-stored budget. The Reja vs Fakt summary handler reads
	// these to display the canonical project budget.
	//
	// The ::numeric casts on every $N parameter are mandatory: without
	// them Postgres infers the parameter type from the `> 0` comparison
	// and picks INTEGER, which overflows on real-world budgets (15 B
	// СУМ etc.) with "value 15006076328.372952 is out of range for
	// type integer". Even worse, the failed Exec aborts the parent
	// transaction, so every subsequent INSERT in this bulk-import
	// cascades with "current transaction is aborted, commands ignored".
	// We also wrap the call in a savepoint so a stray budget failure
	// (e.g. column missing on an old DB that hasn't run migration 369)
	// can't take the line inserts down with it.
	if req.BudgetTotal > 0 || req.MaterialBudget > 0 || req.TransportBudget > 0 {
		if _, spErr := tx.Exec(`SAVEPOINT budget_update`); spErr == nil {
			_, bErr := tx.Exec(`
				UPDATE construction_estimate
				SET budget_total     = CASE WHEN $1::numeric > 0 THEN $1::numeric ELSE budget_total END,
				    material_budget  = CASE WHEN $2::numeric > 0 THEN $2::numeric ELSE material_budget END,
				    transport_budget = CASE WHEN $3::numeric > 0 THEN $3::numeric ELSE transport_budget END
				WHERE id = $4 AND tenant_id = $5`,
				req.BudgetTotal, req.MaterialBudget, req.TransportBudget,
				estimateID, tenantID)
			if bErr != nil {
				h.log.Error("Failed to persist imported budget on estimate",
					"error", bErr,
					"budget_total", req.BudgetTotal,
					"material_budget", req.MaterialBudget,
					"transport_budget", req.TransportBudget,
					"estimate_id", estimateID)
				// Rewind to the savepoint so the parent tx stays alive
				// for the line INSERTs below.
				_, _ = tx.Exec(`ROLLBACK TO SAVEPOINT budget_update`)
			} else {
				_, _ = tx.Exec(`RELEASE SAVEPOINT budget_update`)
			}
		}
	}

	// TEMPLATE MODE — applies to the structural sheets (edinich /
	// resurs) where the user wants every line to land at quantity = 0
	// and fill BAJARILDI manually. ВОР is the canonical source of the
	// planned project Miqdor — stripping its quantities would make the
	// Bosqichlar tab's REJA column 0 across the board, since REJA is
	// matched by work name to the ВОР row's quantity. Per-row cascade
	//   child.quantity = parent.quantity × child.norm_rate
	// runs only on the единич side, so its quantities can safely
	// start at 0.
	importTemplateMode := false
	switch strings.ToLower(strings.TrimSpace(req.SourceType)) {
	case "edinich", "resurs":
		importTemplateMode = true
	}

	// Batched bulk INSERT — `batchSize` rows per round-trip instead of
	// one-row-per-Exec. The previous loop's 3 000+ sequential
	// tx.Exec() calls were the dominant cost on real-world imports
	// (each call = round-trip + parse + plan + write); switching to
	// multi-row VALUES collapses 3 000 round-trips down to ~6 for a
	// 3 000-line file, which is the difference between "tens of
	// seconds" and "well under one".
	//
	// 21 columns × 500 rows = 10 500 placeholders per batch — well
	// under PostgreSQL's 65 535 parameter limit.
	//
	// The last two slots (imported_quantity, imported_total) are the
	// migration-400 display-only fields. They mirror the Ресурс XLSX
	// "Количество" and "Сметная стоимость в базисном уровне" columns and
	// are deliberately NOT consumed by any cost / cascade / ledger logic.
	// Non-resurs imports leave them nil → stored as NULL.
	const fieldsPerRow = 21
	const batchSize = 500

	insertHeader := `INSERT INTO construction_estimate_line (
		tenant_id, estimate_id, wbs_id, name, uom, quantity,
		material_rate, labor_rate, equipment_rate,
		unit_rate, total_amount, code, item_number,
		resource_type, material_type, parent_item_number, norm_rate, sort_order,
		original_quantity,
		imported_quantity, imported_total,
		created_date, updated_date
	) VALUES `

	count := 0
	for batchStart := 0; batchStart < len(req.Lines); batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > len(req.Lines) {
			batchEnd = len(req.Lines)
		}

		var sb strings.Builder
		sb.Grow(len(insertHeader) + (batchEnd-batchStart)*120)
		sb.WriteString(insertHeader)

		args := make([]interface{}, 0, (batchEnd-batchStart)*fieldsPerRow)

		for i := batchStart; i < batchEnd; i++ {
			line := req.Lines[i]
			// In template mode, force the ledger column to 0 — the
			// file's pre-baked total is intentionally discarded.
			// norm_rate is left alone so the per-unit norm survives
			// the round-trip.
			if importTemplateMode {
				line.Quantity = 0
			}
			unitRate := line.MaterialRate + line.LaborRate + line.EquipmentRate
			totalAmount := unitRate * line.Quantity
			uom := line.UOM
			if uom == "" {
				uom = "шт"
			}
			sortOrder := line.SortOrder
			if sortOrder == 0 {
				sortOrder = i + 1
			}

			// material_type — sub-bucket for resource_type='material'
			// rows (migration 350). Falls back to 'standard' when
			// omitted or when the line is labor/equipment so the CHECK
			// constraint is happy.
			materialType := strings.ToLower(strings.TrimSpace(line.MaterialType))
			switch materialType {
			case "standard", "equipment", "cable", "metal", "import":
				// already valid
			default:
				materialType = "standard"
			}

			// original_quantity passthrough — see comments in the
			// pre-batched implementation; behaviour is unchanged.
			var origQtyArg interface{}
			if line.OriginalQuantity != nil {
				origQtyArg = *line.OriginalQuantity
			}

			// Migration 400: display-only "imported" fields. Pass the
			// pointer's value when present, nil otherwise — pq will write
			// NULL into the nullable NUMERIC columns. These never feed
			// any business logic; only the read path returns them so the
			// frontend can render the file figure alongside the
			// computed/live columns.
			var impQtyArg interface{}
			if line.ImportedQuantity != nil {
				impQtyArg = *line.ImportedQuantity
			}
			var impTotalArg interface{}
			if line.ImportedTotal != nil {
				impTotalArg = *line.ImportedTotal
			}

			// Build the ($N, $N+1, ..., $N+20, NOW(), NOW()) tuple.
			if i > batchStart {
				sb.WriteByte(',')
			}
			base := (i - batchStart) * fieldsPerRow
			sb.WriteByte('(')
			for j := 0; j < fieldsPerRow; j++ {
				if j > 0 {
					sb.WriteByte(',')
				}
				sb.WriteByte('$')
				sb.WriteString(strconv.Itoa(base + j + 1))
			}
			sb.WriteString(",NOW(),NOW())")

			args = append(args,
				tenantID, estimateID, nullInt64FromVal(line.WBSID),
				line.Name, uom, line.Quantity,
				line.MaterialRate, line.LaborRate, line.EquipmentRate,
				unitRate, totalAmount, nullStringFromVal(line.Code), nullStringFromVal(line.ItemNumber),
				nullStringFromVal(line.ResourceType), materialType, nullStringFromVal(line.ParentItemNumber), line.NormRate, sortOrder,
				origQtyArg,
				impQtyArg, impTotalArg,
			)
			count++
		}

		if _, err := tx.Exec(sb.String(), args...); err != nil {
			h.log.Error("Failed to bulk-insert estimate lines",
				"error", err, "batch_start", batchStart, "batch_end", batchEnd)
			response.InternalError(c, fmt.Sprintf("Failed to insert lines %d-%d", batchStart+1, batchEnd))
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to complete import")
		return
	}

	// Resolve parent_line_id (and subline_seq) for child resource rows.
	//
	// The bulk INSERT above writes parent_item_number (text — the parent
	// work's local item_number, e.g. "648" or "1.1's parent = 1") but not
	// parent_line_id (the FK). The Smeta Management tab and the v2
	// Bosqichlar tab both group sub-resources by parent_line_id, so
	// without this step every work shows "0 resurs" even though the
	// children are present in the database.
	//
	// For each child line (resource_type set, parent_item_number is a
	// numeric string like "648" or "1.1") we link it to the IMMEDIATELY
	// PRECEDING top-level work whose item_number matches — preceding by
	// sort_order, since the import always writes a parent followed by
	// its children. The numeric-string filter on parent_item_number is
	// what distinguishes children (parent_item_number = "648") from
	// top-level works (parent_item_number = "СЕКЦИЯ №3 › ЗЕМЛЯННЫЕ
	// РАБОТЫ" — a section path, has spaces / "›").
	//
	// We MUST also assign a unique subline_seq per parent because of the
	// `uq_estimate_line_parent_seq (parent_line_id, subline_seq)` index
	// from migration 332. Without it every child of a single parent
	// would default to subline_seq=0 and collide on the second row.
	// ROW_NUMBER() partitioned by the resolved parent gives 1, 2, 3, …
	// in sort_order, which keeps the visual ordering stable.
	if _, linkErr := h.db.Exec(`
		WITH resolved AS (
		    SELECT
		        child.id            AS child_id,
		        parent.parent_id    AS parent_id,
		        ROW_NUMBER() OVER (
		            PARTITION BY parent.parent_id
		            ORDER BY child.sort_order, child.id
		        ) AS new_seq
		    FROM construction_estimate_line child
		    CROSS JOIN LATERAL (
		        SELECT p.id AS parent_id
		        FROM construction_estimate_line p
		        WHERE p.estimate_id = child.estimate_id
		          AND p.tenant_id   = child.tenant_id
		          AND p.item_number = child.parent_item_number
		          AND p.sort_order  < child.sort_order
		          AND COALESCE(p.resource_type, '') = ''
		        ORDER BY p.sort_order DESC
		        LIMIT 1
		    ) parent
		    WHERE child.estimate_id = $1
		      AND child.tenant_id   = $2
		      AND child.parent_line_id IS NULL
		      AND COALESCE(child.resource_type, '') <> ''
		      AND child.parent_item_number ~ '^[0-9]+([.][0-9]+)?$'
		)
		UPDATE construction_estimate_line el
		SET parent_line_id = r.parent_id,
		    subline_seq    = r.new_seq
		FROM resolved r
		WHERE el.id = r.child_id
	`, estimateID, tenantID); linkErr != nil {
		// Non-fatal — works will still display, sub-resources will just
		// be missing until the user re-imports or links manually.
		h.log.Error("Failed to resolve parent_line_id post-import",
			"error", linkErr, "estimate_id", estimateID)
	}

	// Propagate prices from the project's Ресурс estimate(s) into any
	// 0-priced sub-lines in the project. The Единич parser doesn't
	// extract per-resource prices (the Единич sheet has only norm + qty
	// columns), so without this step every sub-resource on the Smeta
	// boshqaruvi tab shows NARX = 0. Runs project-wide so it handles
	// both directions:
	//   • non-resurs just imported → fill from existing resurs prices
	//   • resurs just imported     → push prices into already-imported
	//                                Единич / ВОР sub-lines
	if importProjectID > 0 {
		h.propagateResursPricesForProject(tenantID, importProjectID)
	}

	// Recalculate estimate totals
	h.recalculateEstimateTotals(estimateID)

	// Use source_type from request if provided, otherwise fetch from DB
	sourceType := req.SourceType
	if sourceType == "" {
		h.db.QueryRow(`SELECT COALESCE(source_type, '') FROM construction_estimate WHERE id = $1 AND tenant_id = $2`,
			estimateID, tenantID).Scan(&sourceType)
	}

	orgID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	// Auto-create products ONLY for resource-type estimates
	// (excludes labor ЧЕЛ.-Ч and equipment МАШ.-Ч resources)
	productsCreated := 0
	if sourceType == "resurs" {
		productsCreated = h.autoCreateProductsFromEstimateLines(tenantID, orgID, userID, req.Lines)
	}

	// Auto-create Forma 2 was previously triggered here on every import.
	// Disabled per product feedback — the user creates Forma 2 manually
	// from the Smeta boshqaruvi tab when they're ready, so importing an
	// estimate no longer side-effects a Forma 2 draft. The helper
	// `autoCreateForma2FromEstimate` is kept in the codebase (still
	// referenced from CreateProductsFromEstimate) in case the behaviour
	// needs to come back, just not invoked here.

	resp := map[string]interface{}{
		"count":            count,
		"products_created": productsCreated,
	}
	response.Success(c, resp)
}

// CreateProductsFromEstimate creates inventory products from an existing estimate's resource lines.
// This is useful for retroactively creating products from estimates that were imported before
// the auto-create feature was added.
func (h *Handler) CreateProductsFromEstimate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	// Verify estimate exists and belongs to this tenant
	var exists bool
	err = h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM construction_estimate WHERE id = $1 AND tenant_id = $2)`, estimateID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Estimate not found")
		return
	}

	// Fetch all lines from this estimate
	rows, err := h.db.Query(`
		SELECT name, uom, quantity, material_rate, labor_rate, equipment_rate,
		       COALESCE(code, '') as code, COALESCE(item_number, '') as item_number,
		       COALESCE(resource_type, '') as resource_type
		FROM construction_estimate_line
		WHERE estimate_id = $1 AND tenant_id = $2
		ORDER BY sort_order
	`, estimateID, tenantID)
	if err != nil {
		h.log.Error("Failed to fetch estimate lines", "error", err)
		response.InternalError(c, "Failed to fetch estimate lines")
		return
	}
	defer rows.Close()

	var lines []entity.CreateEstimateLineInput
	for rows.Next() {
		var line entity.CreateEstimateLineInput
		err := rows.Scan(&line.Name, &line.UOM, &line.Quantity,
			&line.MaterialRate, &line.LaborRate, &line.EquipmentRate,
			&line.Code, &line.ItemNumber, &line.ResourceType)
		if err != nil {
			h.log.Error("Failed to scan estimate line", "error", err)
			continue
		}
		lines = append(lines, line)
	}

	orgID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)
	productsCreated := h.autoCreateProductsFromEstimateLines(tenantID, orgID, userID, lines)

	response.Success(c, map[string]interface{}{
		"products_created": productsCreated,
		"total_lines":      len(lines),
	})
}

// UpdateEstimateLine updates an estimate line
func (h *Handler) UpdateEstimateLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	lineID, err := strconv.ParseInt(c.Param("line_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}

	// Check estimate state + capture pre-update snapshot for the audit log.
	var state string
	var estimateID, projectID int64
	var oldName, oldMaterialType string
	var oldQty, oldUnitRate float64
	err = h.db.QueryRow(`
		SELECT e.state, l.estimate_id, e.project_id,
		       COALESCE(l.name, ''), COALESCE(l.material_type, 'standard'),
		       COALESCE(l.quantity, 0), COALESCE(l.unit_rate, 0)
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE l.id = $1 AND l.tenant_id = $2
	`, lineID, tenantID).Scan(&state, &estimateID, &projectID,
		&oldName, &oldMaterialType, &oldQty, &oldUnitRate)
	if err != nil {
		response.NotFound(c, "Estimate line not found")
		return
	}
	var req entity.UpdateEstimateLineInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Only actual_amount can be updated on non-draft estimates
	isActualAmountOnly := req.ActualAmount != nil && req.WBSID == nil && req.Name == nil && req.UOM == nil &&
		req.Quantity == nil && req.MaterialRate == nil && req.LaborRate == nil && req.EquipmentRate == nil && req.SortOrder == nil &&
		req.Code == nil && req.ItemNumber == nil && req.ResourceType == nil && req.NormRate == nil && req.UnitPrice == nil &&
		req.QuantityOverride == nil && req.OriginalQuantity == nil

	if state != "draft" && !isActualAmountOnly {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	// ─── Sub-line handling (migration 332 + 342) ───
	// If this row has a parent and the user edited norm_rate, re-derive its
	// quantity from parent.quantity × norm_rate. Same logic as create.
	//
	// Skip the derivation when the sub-line is (or is becoming) in MANUAL
	// override mode — in that case the user-supplied Quantity is the source
	// of truth and must not be overwritten. Derivation is allowed again only
	// when override is explicitly being turned back off AND the user didn't
	// send a Quantity alongside.
	if req.NormRate != nil {
		var parentLineID sql.NullInt64
		var parentQty float64
		var storedOverride bool
		if err := h.db.QueryRow(`
			SELECT l.parent_line_id, COALESCE(p.quantity, 0),
			       COALESCE(l.quantity_override, FALSE)
			FROM construction_estimate_line l
			LEFT JOIN construction_estimate_line p ON p.id = l.parent_line_id
			WHERE l.id = $1 AND l.tenant_id = $2
		`, lineID, tenantID).Scan(&parentLineID, &parentQty, &storedOverride); err == nil && parentLineID.Valid {
			effectiveOverride := storedOverride
			if req.QuantityOverride != nil {
				effectiveOverride = *req.QuantityOverride
			}
			if !effectiveOverride && req.Quantity == nil {
				derivedQty := parentQty * (*req.NormRate)
				req.Quantity = &derivedQty
			}
		}
	}

	// UnitPrice convenience: route a single edited unit price into the matching
	// rate column based on resource_type. Does not override rates the user
	// edited directly in the same request.
	if req.UnitPrice != nil {
		var rType sql.NullString
		if req.ResourceType != nil {
			rType = sql.NullString{String: *req.ResourceType, Valid: true}
		} else {
			h.db.QueryRow(`SELECT COALESCE(resource_type, '') FROM construction_estimate_line WHERE id = $1 AND tenant_id = $2`,
				lineID, tenantID).Scan(&rType)
		}
		up := *req.UnitPrice
		switch strings.ToLower(strings.TrimSpace(rType.String)) {
		case "equipment", "machine", "mashina", "masina":
			if req.EquipmentRate == nil {
				req.EquipmentRate = &up
			}
		case "labor", "ish", "ishchi", "worker":
			if req.LaborRate == nil {
				req.LaborRate = &up
			}
		case "material", "materialy", "mat":
			if req.MaterialRate == nil {
				req.MaterialRate = &up
			}
		default:
			if req.EquipmentRate == nil {
				req.EquipmentRate = &up
			}
		}
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.WBSID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("wbs_id = $%d", argCount))
		if *req.WBSID == 0 {
			args = append(args, nil)
		} else {
			args = append(args, *req.WBSID)
		}
	}
	if req.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *req.Name)
	}
	if req.UOM != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("uom = $%d", argCount))
		args = append(args, *req.UOM)
	}
	if req.Quantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("quantity = $%d", argCount))
		args = append(args, *req.Quantity)
	}
	if req.MaterialRate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("material_rate = $%d", argCount))
		args = append(args, *req.MaterialRate)
	}
	if req.LaborRate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("labor_rate = $%d", argCount))
		args = append(args, *req.LaborRate)
	}
	if req.EquipmentRate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("equipment_rate = $%d", argCount))
		args = append(args, *req.EquipmentRate)
	}
	if req.SortOrder != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("sort_order = $%d", argCount))
		args = append(args, *req.SortOrder)
	}
	if req.ActualAmount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("actual_amount = $%d", argCount))
		args = append(args, *req.ActualAmount)
	}
	if req.Code != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("code = $%d", argCount))
		args = append(args, *req.Code)
	}
	if req.ItemNumber != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("item_number = $%d", argCount))
		args = append(args, *req.ItemNumber)
	}
	if req.ResourceType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("resource_type = $%d", argCount))
		args = append(args, *req.ResourceType)
	}
	if req.NormRate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("norm_rate = $%d", argCount))
		args = append(args, *req.NormRate)
	}
	if req.QuantityOverride != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("quantity_override = $%d", argCount))
		args = append(args, *req.QuantityOverride)
	}
	if req.OriginalQuantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("original_quantity = $%d", argCount))
		args = append(args, *req.OriginalQuantity)
	}
	if req.MaterialType != nil {
		// Validate against the schema CHECK constraint so we get a 400 instead
		// of an opaque pq error.
		mt := strings.ToLower(strings.TrimSpace(*req.MaterialType))
		switch mt {
		case "standard", "equipment", "cable", "metal", "import":
			argCount++
			updates = append(updates, fmt.Sprintf("material_type = $%d", argCount))
			args = append(args, mt)
		default:
			response.BadRequest(c, "material_type must be one of: standard, equipment, cable, metal, import")
			return
		}
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, lineID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_estimate_line SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update estimate line", "error", err)
		response.InternalError(c, "Failed to update estimate line")
		return
	}

	// Recalculate unit_rate and total_amount for this line
	_, err = h.db.Exec(`
		UPDATE construction_estimate_line
		SET unit_rate = material_rate + labor_rate + equipment_rate,
		    total_amount = (material_rate + labor_rate + equipment_rate) * quantity
		WHERE id = $1
	`, lineID)
	if err != nil {
		h.log.Error("Failed to recalculate line totals", "error", err)
	}

	// Cascade quantity changes to non-override children.
	//
	// When the user edits ISH HAJMI on ANY line that has children — a
	// top-level work OR a sub-stage (sub-stages have parent_line_id
	// set, but ALSO carry their own resource children) — every child
	// sub-line whose quantity_override = FALSE should recompute
	//     child.quantity     = new_parent_qty × child.norm_rate
	//     child.total_amount = child.quantity × child.unit_rate
	// so the JAMI / SUMMA columns pick up the new values.
	//
	// Earlier this fired only for top-level works (parent_line_id
	// IS NULL). User report: typing FAKT on a sub-stage left every
	// inner resource at 0. The cascade UPDATE itself is a no-op when
	// the line has no children (it just touches zero rows), so we
	// can fire it unconditionally on any quantity update.
	if req.Quantity != nil {
		if _, cascadeErr := h.db.Exec(`
			UPDATE construction_estimate_line c
			SET quantity          = $1 * COALESCE(c.norm_rate, 0),
			    total_amount      = ($1 * COALESCE(c.norm_rate, 0)) * COALESCE(c.unit_rate, 0),
			    quantity_override = FALSE,
			    updated_date      = NOW()
			WHERE c.parent_line_id = $2
			  AND (
			    COALESCE(c.quantity_override, FALSE) = FALSE
			    OR (COALESCE(c.quantity_override, FALSE) = TRUE
			        AND COALESCE(c.quantity, 0) = 0
			        AND COALESCE(c.norm_rate, 0) > 0)
			  )
		`, *req.Quantity, lineID); cascadeErr != nil {
			h.log.Error("Failed to cascade quantity to children",
				"error", cascadeErr, "parent_line_id", lineID)
		}

		// Mirror ISH HAJMI → BAJARILDI on the row itself. For
		// top-level works this drives the Bosqichlar BAJARILDI column;
		// for sub-stages it does the same against the sub-stage's own
		// done_quantity. Approval_status follows: 0 → pending,
		// >0 → in_progress.
		newStatus := "pending"
		if *req.Quantity > 0 {
			newStatus = "in_progress"
		}
		if _, mirrorErr := h.db.Exec(`
			UPDATE construction_estimate_line
			SET done_quantity   = $1,
			    approval_status = CASE
			        WHEN COALESCE(approval_status, 'pending') IN ('pending', 'in_progress')
			          THEN $2
			        ELSE approval_status
			    END,
			    updated_date = NOW()
			WHERE id = $3 AND tenant_id = $4
		`, *req.Quantity, newStatus, lineID, tenantID); mirrorErr != nil {
			h.log.Error("Failed to mirror plan quantity to done_quantity",
				"error", mirrorErr, "line_id", lineID)
		}
	}

	// Recalculate estimate totals
	h.recalculateEstimateTotals(estimateID)

	// ─── Audit: Smeta boshqaruvi → Jurnal ───
	// Read the post-update line so we can diff vs the snapshot taken above.
	userIDLog, _ := middleware.GetUserID(c)
	userNameLog := c.GetString("user_name")
	var newName, newMaterialType string
	var newQty, newUnitRate float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(name, ''), COALESCE(material_type, 'standard'),
		       COALESCE(quantity, 0), COALESCE(unit_rate, 0)
		FROM construction_estimate_line WHERE id = $1
	`, lineID).Scan(&newName, &newMaterialType, &newQty, &newUnitRate)

	target := newName
	if target == "" {
		target = oldName
	}
	if newQty != oldQty {
		h.logSmetaAudit(tenantID, projectID, &estimateID, "qty_change", target, &lineID,
			strconv.FormatFloat(oldQty, 'f', -1, 64),
			strconv.FormatFloat(newQty, 'f', -1, 64),
			"", userIDLog, userNameLog)
	}
	if newUnitRate != oldUnitRate {
		h.logSmetaAudit(tenantID, projectID, &estimateID, "price_change", target, &lineID,
			strconv.FormatFloat(oldUnitRate, 'f', -1, 64),
			strconv.FormatFloat(newUnitRate, 'f', -1, 64),
			"", userIDLog, userNameLog)
	}
	if newMaterialType != oldMaterialType {
		h.logSmetaAudit(tenantID, projectID, &estimateID, "mat_type", target, &lineID,
			oldMaterialType, newMaterialType, "", userIDLog, userNameLog)
	}

	response.Success(c, map[string]interface{}{
		"id":      lineID,
		"message": "Estimate line updated successfully",
	})
}

// DeleteEstimateLine deletes an estimate line
func (h *Handler) DeleteEstimateLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	lineID, err := strconv.ParseInt(c.Param("line_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}

	// Check estimate state and grab everything we need for the audit row.
	var state string
	var estimateID, projectID int64
	var lineName string
	var parentLineID sql.NullInt64
	err = h.db.QueryRow(`
		SELECT e.state, l.estimate_id, e.project_id,
		       COALESCE(l.name, ''), l.parent_line_id
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE l.id = $1 AND l.tenant_id = $2
	`, lineID, tenantID).Scan(&state, &estimateID, &projectID, &lineName, &parentLineID)
	if err != nil {
		response.NotFound(c, "Estimate line not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	_, err = h.db.Exec(`DELETE FROM construction_estimate_line WHERE id = $1 AND tenant_id = $2`, lineID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete estimate line", "error", err)
		response.InternalError(c, "Failed to delete estimate line")
		return
	}

	// Recalculate estimate totals
	h.recalculateEstimateTotals(estimateID)

	// Audit: parent rows are sub-stages (subwork_del), children are resources (res_del).
	userIDLog, _ := middleware.GetUserID(c)
	userNameLog := c.GetString("user_name")
	action := "subwork_del"
	if parentLineID.Valid {
		action = "res_del"
	}
	h.logSmetaAudit(tenantID, projectID, &estimateID, action, lineName, &lineID,
		"", "", "Qator o'chirildi", userIDLog, userNameLog)

	response.Success(c, map[string]interface{}{
		"message": "Estimate line deleted successfully",
	})
}

// ResetEstimateLineQuantity reverts the line's quantity back to
// original_quantity (anchor set by migration 349). For parent rows the action
// also re-derives any sub-line quantities that aren't in manual override mode
// — keeping their parent.quantity × norm_rate invariant intact.
func (h *Handler) ResetEstimateLineQuantity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	lineID, err := strconv.ParseInt(c.Param("line_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}

	// Confirm the estimate is editable + grab the estimate_id for recalc.
	var state string
	var estimateID, projectID int64
	var origQty sql.NullFloat64
	var unitRate, oldQty float64
	var lineName string
	err = h.db.QueryRow(`
		SELECT e.state, l.estimate_id, e.project_id,
		       COALESCE(l.original_quantity, l.quantity), l.unit_rate,
		       COALESCE(l.quantity, 0), COALESCE(l.name, '')
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE l.id = $1 AND l.tenant_id = $2
	`, lineID, tenantID).Scan(&state, &estimateID, &projectID, &origQty, &unitRate, &oldQty, &lineName)
	if err != nil {
		response.NotFound(c, "Estimate line not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Reset the line itself. total_amount is rebuilt from the now-restored qty.
	_, err = tx.Exec(`
		UPDATE construction_estimate_line
		SET quantity     = COALESCE(original_quantity, quantity),
		    total_amount = COALESCE(original_quantity, quantity) * COALESCE(unit_rate, 0),
		    updated_date = NOW()
		WHERE id = $1
	`, lineID)
	if err != nil {
		h.log.Error("Failed to reset line quantity", "error", err)
		response.InternalError(c, "Failed to reset quantity")
		return
	}

	// Cascade to children that aren't in manual override mode — they recompute
	// from parent.quantity × norm_rate. Override children keep their qty.
	_, _ = tx.Exec(`
		UPDATE construction_estimate_line c
		SET quantity     = $1 * COALESCE(c.norm_rate, 0),
		    total_amount = ($1 * COALESCE(c.norm_rate, 0)) * COALESCE(c.unit_rate, 0),
		    updated_date = NOW()
		WHERE c.parent_line_id = $2
		  AND COALESCE(c.quantity_override, FALSE) = FALSE
	`, origQty.Float64, lineID)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit reset qty", "error", err)
		response.InternalError(c, "Failed to reset quantity")
		return
	}

	h.recalculateEstimateTotals(estimateID)

	userIDLog, _ := middleware.GetUserID(c)
	userNameLog := c.GetString("user_name")
	h.logSmetaAudit(tenantID, projectID, &estimateID, "reset_qty", lineName, &lineID,
		strconv.FormatFloat(oldQty, 'f', -1, 64),
		strconv.FormatFloat(origQty.Float64, 'f', -1, 64),
		"Hajm asliga qaytarildi", userIDLog, userNameLog)

	response.Success(c, gin.H{
		"id":       lineID,
		"quantity": origQty.Float64,
	})
}

// ResetAllEstimateQuantities zeroes the quantity on every TOP-LEVEL work
// row in an estimate (parent_line_id IS NULL) and cascades to non-override
// sub-lines via parent.quantity × norm_rate (which collapses to 0). Used
// by the Smeta boshqaruvi "Reset all quantities" button when foremen want
// to start a Forma 2 from a clean slate.
//
// IMPORTANT: original_quantity anchors are kept untouched so the per-line
// reset-to-original button can still bring back the imported figure. We
// only mutate `quantity` and `total_amount`.
func (h *Handler) ResetAllEstimateQuantities(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	// Block on non-draft estimates — same guard as per-line edits.
	var state string
	var projectID int64
	err = h.db.QueryRow(
		`SELECT state, project_id FROM construction_estimate WHERE id = $1 AND tenant_id = $2`,
		estimateID, tenantID,
	).Scan(&state, &projectID)
	if err != nil {
		response.NotFound(c, "Estimate not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Zero every top-level row's qty + recompute total.
	res, err := tx.Exec(`
		UPDATE construction_estimate_line
		SET quantity = 0, total_amount = 0, updated_date = NOW()
		WHERE estimate_id = $1 AND tenant_id = $2
		  AND parent_line_id IS NULL
	`, estimateID, tenantID)
	if err != nil {
		h.log.Error("Failed to zero estimate qty", "error", err)
		response.InternalError(c, "Failed to reset quantities")
		return
	}
	worksZeroed, _ := res.RowsAffected()

	// Cascade to non-override sub-lines (parent.qty=0 × norm_rate = 0).
	_, _ = tx.Exec(`
		UPDATE construction_estimate_line c
		SET quantity = 0, total_amount = 0, updated_date = NOW()
		FROM construction_estimate_line p
		WHERE p.id = c.parent_line_id
		  AND c.estimate_id = $1 AND c.tenant_id = $2
		  AND COALESCE(c.quantity_override, FALSE) = FALSE
	`, estimateID, tenantID)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit reset-all-qty", "error", err)
		response.InternalError(c, "Failed to reset quantities")
		return
	}

	h.recalculateEstimateTotals(estimateID)

	userIDLog, _ := middleware.GetUserID(c)
	userNameLog := c.GetString("user_name")
	h.logSmetaAudit(tenantID, projectID, &estimateID, "reset_qty_all", "", nil,
		"", strconv.FormatInt(worksZeroed, 10),
		"Barcha hajmlar nolga tushirildi", userIDLog, userNameLog)

	response.Success(c, gin.H{"works_zeroed": worksZeroed})
}

// =====================================================
// HELPER FUNCTIONS
// =====================================================

// getEstimateLines retrieves all lines for an estimate, including the
// sub-line linkage fields (parent_line_id, norm_rate, subline_seq) added by
// migration 332. Without these the frontend falls back to matching
// item_number against /^\d+-\d+$/, which works for numeric BOP/Единич codes
// but silently fails for Ресурс estimates whose item_numbers are SNiP-style
// codes (e.g. "Э14-1-17"), so podkators created on resurs rows never render
// as nested children of their parent.
func (h *Handler) getEstimateLines(estimateID int64, tenantID uuid.UUID, includeManual ...bool) []entity.ConstructionEstimateLine {
	// Variadic for backwards compatibility — existing callers don't pass
	// includeManual and default to TRUE (show everything). The
	// EstimatesTab path passes FALSE so user-added lines are hidden
	// from the file-derived Smetalar view.
	wantManual := true
	if len(includeManual) > 0 {
		wantManual = includeManual[0]
	}
	// Hoist the parent estimate's project_id and building_id out to
	// Go-side parameters. The ВОР fallback subquery below previously
	// re-derived these via three nested scalar subqueries
	// (`SELECT project_id FROM construction_estimate WHERE id = l.estimate_id`)
	// per ROW — and on a 5K-line estimate the planner ran them N times
	// instead of once, which is the observed source of Smeta boshqaruvi
	// timeouts on big tenants. Fetching them once here is one extra
	// round-trip but eliminates ~15K wasted scalar lookups in the main
	// query.
	var estProjectID int64
	var estBuildingID sql.NullInt64
	if err := h.db.QueryRow(
		`SELECT project_id, building_id
		   FROM construction_estimate
		  WHERE id = $1 AND tenant_id = $2`,
		estimateID, tenantID,
	).Scan(&estProjectID, &estBuildingID); err != nil {
		h.log.Error("Failed to load estimate header for getEstimateLines",
			"error", err, "estimate_id", estimateID)
		return []entity.ConstructionEstimateLine{}
	}
	var estBuildingArg interface{}
	if estBuildingID.Valid {
		estBuildingArg = estBuildingID.Int64
	} else {
		// Pass NULL through pq so the SQL `$4::bigint IS NULL` branch
		// fires correctly.
		estBuildingArg = nil
	}

	query := `
		SELECT l.id, l.tenant_id, l.estimate_id, l.wbs_id,
		       l.name, l.uom, l.quantity,
		       l.material_rate, l.labor_rate, l.equipment_rate,
		       l.unit_rate, l.total_amount, l.actual_amount,
		       COALESCE(l.code, ''), COALESCE(l.item_number, ''),
		       COALESCE(l.resource_type, ''), COALESCE(l.parent_item_number, ''),
		       l.parent_line_id, COALESCE(l.norm_rate, 0), COALESCE(l.subline_seq, 0),
		       COALESCE(l.quantity_override, FALSE),
		       COALESCE(l.material_type, 'standard'),
		       -- Norma anchor for the Smeta boshqaruvi NORMA pill.
		       -- Falls back from explicit anchor → matching ВОР work's
		       -- quantity. The ВОР fallback only fires for parent rows
		       -- (resource_type = '') and rescues Единич template-mode
		       -- imports whose own anchor is 0.
		       --
		       -- IMPORTANT: we deliberately do NOT fall back to
		       -- l.quantity here. l.quantity is the live FAKT ledger
		       -- (mirrored from done_quantity by UpdateEstimateLine), so
		       -- coupling NORMA to it makes NORMA visibly follow FAKT
		       -- as the user types — exactly the bug the user reported
		       -- ("when i change Fakt, Norma is also changing"). Skipping
		       -- the fallback means rows with no anchor and no ВОР match
		       -- render NORMA as "—", which is correct (we genuinely
		       -- don't know the planned amount) and harmless to the
		       -- numeric calculations that read original_quantity directly.
		       COALESCE(
		         NULLIF(l.original_quantity, 0),
		         CASE WHEN COALESCE(l.resource_type, '') = '' THEN (
		             -- One ВОР row per work — pick the first matching one
		             -- (lowest id ⇒ earliest import). Earlier this query
		             -- summed across all matches, which double-counted
		             -- when the user re-imported the same ВОР file (the
		             -- old rows aren't deleted because each import gets
		             -- a fresh estimate_id). Picking a single row keeps
		             -- NORMA aligned with the file's stated quantity for
		             -- this work, even if the user has re-imported.
		             -- Restricted to the SAME building when the Единич
		             -- estimate carries a building_id, so multi-block
		             -- projects don't cross-pollinate quantities.
		             --
		             -- $3 is the parent estimate's project_id and
		             -- $4 is its building_id (nullable) — both fetched
		             -- once in Go. Migration 378 added a partial index
		             -- on construction_estimate(project_id, tenant_id)
		             -- WHERE source_type='vor' that this clause uses.
		             -- Same priority as the other VOR NORMA fallback
		             -- (see lines ~713): imported_quantity > original_
		             -- quantity > live quantity, so this matches what
		             -- Smetalar tab actually displays.
		             SELECT vl.quantity
		             FROM construction_estimate_line vl
		             JOIN construction_estimate ve ON ve.id = vl.estimate_id
		             WHERE ve.tenant_id = l.tenant_id
		               AND ve.project_id = $3
		               AND LOWER(COALESCE(ve.source_type, '')) = 'vor'
		               AND (
		                 ve.building_id IS NULL
		                 OR ve.building_id = $4::bigint
		                 OR $4::bigint IS NULL
		               )
		               AND vl.name = l.name
		               AND COALESCE(vl.parent_item_number, '') = COALESCE(l.parent_item_number, '')
		               AND COALESCE(vl.resource_type, '') = ''
		               AND COALESCE(vl.parent_line_id, 0) = 0
		               AND vl.quantity > 0
		             ORDER BY vl.id ASC
		             LIMIT 1
		         ) ELSE NULL END,
		         0
		       ),
		       COALESCE(l.original_unit_rate, l.unit_rate),
		       -- Display-only fields from migration 400. Returned as raw
		       -- nullable numerics so the frontend can distinguish "no
		       -- imported value" from "imported zero" and render an
		       -- em-dash for the former.
		       l.imported_quantity,
		       l.imported_total,
		       COALESCE(l.approval_status, 'pending'),
		       COALESCE(l.done_quantity, 0),
		       l.sort_order, COALESCE(l.is_manual, FALSE),
		       l.created_date, l.updated_date,
		       COALESCE(w.code, '') as wbs_code,
		       COALESCE(w.name, '') as wbs_name
		FROM construction_estimate_line l
		LEFT JOIN construction_wbs w ON w.id = l.wbs_id
		LEFT JOIN construction_estimate_line p ON p.id = l.parent_line_id
		WHERE l.estimate_id = $1 AND l.tenant_id = $2` + func() string {
		if !wantManual {
			return " AND COALESCE(l.is_manual, FALSE) = FALSE"
		}
		return ""
	}() + `
		ORDER BY COALESCE(p.sort_order, l.sort_order) ASC,
		         COALESCE(l.parent_line_id, l.id) ASC,
		         (CASE WHEN l.parent_line_id IS NULL THEN 0 ELSE 1 END) ASC,
		         COALESCE(l.subline_seq, 0) ASC,
		         l.id ASC
	`

	rows, err := h.db.Query(query, estimateID, tenantID, estProjectID, estBuildingArg)
	if err != nil {
		h.log.Error("Failed to get estimate lines", "error", err)
		return []entity.ConstructionEstimateLine{}
	}
	defer rows.Close()

	lines := []entity.ConstructionEstimateLine{}
	for rows.Next() {
		var line entity.ConstructionEstimateLine
		if err := rows.Scan(
			&line.ID, &line.TenantID, &line.EstimateID, &line.WBSID,
			&line.Name, &line.UOM, &line.Quantity,
			&line.MaterialRate, &line.LaborRate, &line.EquipmentRate,
			&line.UnitRate, &line.TotalAmount, &line.ActualAmount,
			&line.Code, &line.ItemNumber,
			&line.ResourceType, &line.ParentItemNumber,
			&line.ParentLineID, &line.NormRate, &line.SublineSeq,
			&line.QuantityOverride,
			&line.MaterialType,
			&line.OriginalQuantity, &line.OriginalUnitRate,
			// Migration 400 — display-only nullable numerics. Scanning
			// into sql.NullFloat64 preserves the NULL distinction so
			// MarshalJSON emits `null` (not 0) when no file value exists.
			&line.ImportedQuantity, &line.ImportedTotal,
			&line.ApprovalStatus, &line.DoneQuantity,
			&line.SortOrder, &line.IsManual,
			&line.CreatedDate, &line.UpdatedDate,
			&line.WBSCode, &line.WBSName,
		); err != nil {
			h.log.Error("Failed to scan estimate line", "error", err)
			continue
		}
		lines = append(lines, line)
	}

	// Load top-ups (migration 358) for every line in this estimate in a
	// single query, then bucket them by line ID so we can attach without
	// re-querying. Top-ups are rare on most lines, so the slice is nil
	// unless the line actually has any.
	topupQuery := `
		SELECT t.id, t.tenant_id, t.estimate_line_id,
		       t.extra_quantity, t.new_price, t.ordered_at,
		       COALESCE(t.note, ''), t.created_by, t.created_date
		FROM construction_resource_topup t
		JOIN construction_estimate_line l ON l.id = t.estimate_line_id
		WHERE l.estimate_id = $1 AND t.tenant_id = $2
		ORDER BY t.ordered_at ASC, t.id ASC
	`
	topupRows, terr := h.db.Query(topupQuery, estimateID, tenantID)
	if terr == nil {
		defer topupRows.Close()
		bucket := make(map[int64][]entity.ResourceTopup)
		for topupRows.Next() {
			var t entity.ResourceTopup
			if scanErr := topupRows.Scan(
				&t.ID, &t.TenantID, &t.EstimateLineID,
				&t.ExtraQuantity, &t.NewPrice, &t.OrderedAt,
				&t.Note, &t.CreatedBy, &t.CreatedDate,
			); scanErr != nil {
				h.log.Error("Failed to scan resource topup", "error", scanErr)
				continue
			}
			bucket[t.EstimateLineID] = append(bucket[t.EstimateLineID], t)
		}
		for i := range lines {
			if list, ok := bucket[lines[i].ID]; ok {
				lines[i].Topups = list
			}
		}
	} else {
		// Don't fail the whole estimate fetch if the topup table
		// doesn't exist yet — just log and proceed without topups.
		h.log.Error("Failed to load resource topups", "error", terr)
	}

	return lines
}

// recalculateEstimateTotals updates the estimate header with recalculated totals.
// Per-line effective cost rule (matches the SmetaManagementTab +
// Form2Preview frontend):
//   - If the line has top-ups (migration 358) AND their total
//     extra_quantity COVERS the line's planned quantity
//     (Σ extra_quantity ≥ l.quantity), the effective cost is
//     Σ (extra_quantity × new_price) — the top-ups represent the
//     full purchase at the prices actually paid, so the planned
//     total_amount is replaced.
//   - If the top-up qty is SMALLER than the planned qty, the
//     top-ups are just a partial side-record. The planned
//     total_amount stays in place; otherwise the resource would be
//     understated.
//   - With no top-ups at all, fall back to the stored total_amount.
//
// amount_direct = sum of effective per-line costs across the whole
// estimate; overhead / profit / VAT then layer on top.
func (h *Handler) recalculateEstimateTotals(estimateID int64) {
	var amountDirect float64
	err := h.db.QueryRow(`
		SELECT COALESCE(SUM(
		    CASE
		        WHEN COALESCE(tp.tp_qty, 0) >= COALESCE(l.quantity, 0)
		         AND COALESCE(tp.tp_sum, 0) > 0
		            THEN tp.tp_sum
		        ELSE l.total_amount
		    END
		), 0)
		FROM construction_estimate_line l
		LEFT JOIN (
		    SELECT estimate_line_id,
		           SUM(extra_quantity)             AS tp_qty,
		           SUM(extra_quantity * new_price) AS tp_sum
		    FROM construction_resource_topup
		    GROUP BY estimate_line_id
		) tp ON tp.estimate_line_id = l.id
		WHERE l.estimate_id = $1
	`, estimateID).Scan(&amountDirect)
	if err != nil {
		h.log.Error("Failed to sum estimate lines", "error", err)
		return
	}

	// Get percentages
	var overheadPct, profitPct, vatPct float64
	err = h.db.QueryRow(
		`SELECT overhead_pct, profit_pct, vat_pct FROM construction_estimate WHERE id = $1`,
		estimateID,
	).Scan(&overheadPct, &profitPct, &vatPct)
	if err != nil {
		h.log.Error("Failed to get estimate percentages", "error", err)
		return
	}

	// Calculate total with overhead, profit, and VAT
	afterOverhead := amountDirect * (1 + overheadPct/100)
	afterProfit := afterOverhead * (1 + profitPct/100)
	amountTotal := afterProfit * (1 + vatPct/100)

	_, err = h.db.Exec(
		`UPDATE construction_estimate SET amount_direct = $1, amount_total = $2, updated_date = NOW() WHERE id = $3`,
		amountDirect, amountTotal, estimateID,
	)
	if err != nil {
		h.log.Error("Failed to update estimate totals", "error", err)
	}
}

// =====================================================
// ESTIMATE SUMMARY (Свод) HANDLERS
// =====================================================

// ImportEstimateSummary bulk-imports Свод cross-tab data for a project
func (h *Handler) ImportEstimateSummary(c *gin.Context) {
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

	var req entity.BulkCreateEstimateSummaryInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if len(req.Rows) == 0 {
		response.BadRequest(c, "No rows provided")
		return
	}

	// Generate batch ID
	batchID := uuid.New().String()[:8]

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalError(c, "Failed to start import")
		return
	}
	defer tx.Rollback()

	count := 0
	for _, row := range req.Rows {
		_, err := tx.Exec(`
			INSERT INTO construction_estimate_summary (
				tenant_id, project_id, batch_id, row_number,
				category_name, building_column, amount, created_date
			) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		`, tenantID, projectID, batchID, row.RowNumber,
			row.CategoryName, row.BuildingColumn, row.Amount,
		)
		if err != nil {
			h.log.Error("Failed to insert summary row", "error", err, "category", row.CategoryName)
			response.InternalError(c, fmt.Sprintf("Failed to insert row: %s", row.CategoryName))
			return
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to complete import")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "estimate", fmt.Sprintf("Svod import qilindi: %d qator", count), "EstimateSummary", 0)

	response.Success(c, map[string]interface{}{
		"count":    count,
		"batch_id": batchID,
	})
}

// ListEstimateSummary returns all summary data for a project
func (h *Handler) ListEstimateSummary(c *gin.Context) {
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
		SELECT id, tenant_id, project_id, batch_id, row_number,
		       category_name, building_column, amount, created_date
		FROM construction_estimate_summary
		WHERE project_id = $1 AND tenant_id = $2
		ORDER BY batch_id DESC, row_number ASC, building_column ASC
	`

	rows, err := h.db.Query(query, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to list estimate summary", "error", err)
		response.InternalError(c, "Failed to list estimate summary")
		return
	}
	defer rows.Close()

	items := []entity.ConstructionEstimateSummary{}
	for rows.Next() {
		var item entity.ConstructionEstimateSummary
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.ProjectID, &item.BatchID, &item.RowNumber,
			&item.CategoryName, &item.BuildingColumn, &item.Amount, &item.CreatedDate,
		); err != nil {
			h.log.Error("Failed to scan summary row", "error", err)
			continue
		}
		items = append(items, item)
	}

	response.Success(c, items)
}

// DeleteEstimateSummaryBatch deletes all summary rows for a batch
func (h *Handler) DeleteEstimateSummaryBatch(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	batchID := c.Param("batch_id")
	if batchID == "" {
		response.BadRequest(c, "Invalid batch ID")
		return
	}

	result, err := h.db.Exec(
		`DELETE FROM construction_estimate_summary WHERE batch_id = $1 AND tenant_id = $2`,
		batchID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete summary batch", "error", err)
		response.InternalError(c, "Failed to delete summary batch")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	response.Success(c, map[string]interface{}{
		"deleted": rowsAffected,
	})
}

// ─── Auto-create Forma 2 (KS-2) from estimate ───────────────────────────────

// autoCreateForma2FromEstimate creates a draft Forma 2 act when a VOR or
// Edinich estimate is imported. Lines are pre-filled from the estimate
// with qty_period = qty_smeta. Returns the act ID (new or reused), or 0
// if not applicable.
//
// `sourceFileName` (optional) is the name of the Excel file the user
// uploaded. When non-empty it acts as the dedup key: every estimate
// imported from the same file merges into the same Forma 2 draft — so
// importing VOR + Единич from "april-estimate.xlsx" produces one Forma 2,
// while a later import of "may-estimate.xlsx" produces a separate one.
func (h *Handler) autoCreateForma2FromEstimate(tenantID, userID uuid.UUID, estimateID int64, sourceFileName string) int64 {
	// Fetch estimate metadata: source_type, project_id, subcontract_id, building_id, name
	// building_id is the per-building scope added in migration 213 — we copy
	// it to the auto-created Forma 2 so the act belongs to the same building
	// as the estimate it was derived from.
	var sourceType, estName string
	var projectID int64
	var subcontractID, buildingID sql.NullInt64
	err := h.db.QueryRow(`
		SELECT COALESCE(source_type, ''), project_id, subcontract_id, building_id, COALESCE(name, '')
		FROM construction_estimate
		WHERE id = $1 AND tenant_id = $2
	`, estimateID, tenantID).Scan(&sourceType, &projectID, &subcontractID, &buildingID, &estName)
	if err != nil {
		h.log.Error("Failed to fetch estimate for auto Forma 2", "error", err, "estimate_id", estimateID)
		return 0
	}

	h.log.Info("autoCreateForma2FromEstimate called",
		"estimate_id", estimateID, "source_type", sourceType,
		"project_id", projectID, "est_name", estName)

	// Only auto-create for VOR and Edinich types
	if sourceType != "vor" && sourceType != "edinich" {
		h.log.Info("Skipping auto Forma 2: source_type not vor/edinich", "source_type", sourceType)
		return 0
	}

	// Skip if a forma2 was already auto-created from this estimate (avoid duplicates on re-import)
	var existingActID int64
	err = h.db.QueryRow(`
		SELECT a.id FROM construction_act a
		WHERE a.project_id = $1 AND a.tenant_id = $2 AND a.act_type = 'ks2'
		  AND EXISTS (
			SELECT 1 FROM construction_act_line al
			JOIN construction_estimate_line el ON el.id = al.estimate_line_id
			WHERE al.act_id = a.id AND el.estimate_id = $3
		  )
		LIMIT 1
	`, projectID, tenantID, estimateID).Scan(&existingActID)
	if err == nil && existingActID > 0 {
		h.log.Info("Forma 2 already exists for this estimate, skipping auto-create",
			"existing_act_id", existingActID, "estimate_id", estimateID)
		return existingActID
	}

	// Reuse a DRAFT Forma 2 that was auto-created from the SAME uploaded
	// Excel file. This is the dedup key: importing multiple estimate
	// types (VOR + Единич) from one file merges into one Forma 2, while
	// imports from different files stay separate — regardless of how
	// much time passes between the uploads, which is what the user
	// wanted. (An earlier pass of this fix used a 10-minute time window
	// and incorrectly absorbed unrelated drafts that happened to land
	// within the window.)
	//
	// `IS NOT DISTINCT FROM` treats NULL == NULL so no-subcontract /
	// no-building drafts still match within a file. We only ever look up
	// by non-empty file name — a missing name falls through to the
	// create-new-act branch so auto-create never silently merges into
	// something unrelated.
	var reuseActID int64
	if sourceFileName != "" {
		var subParam, bldParam interface{}
		if subcontractID.Valid && subcontractID.Int64 > 0 {
			subParam = subcontractID.Int64
		}
		if buildingID.Valid {
			bldParam = buildingID.Int64
		}
		_ = h.db.QueryRow(`
			SELECT id FROM construction_act
			WHERE tenant_id = $1 AND project_id = $2 AND act_type = 'ks2'
			  AND state = 'draft'
			  AND (subcontract_id IS NOT DISTINCT FROM $3)
			  AND (building_id   IS NOT DISTINCT FROM $4)
			  AND source_file_name = $5
			ORDER BY id DESC
			LIMIT 1
		`, tenantID, projectID, subParam, bldParam, sourceFileName).Scan(&reuseActID)
	}

	// Fetch all estimate lines (excluding resource sub-items for edinich)
	rows, err := h.db.Query(`
		SELECT id, name, uom, quantity,
			   material_rate, labor_rate, equipment_rate,
			   COALESCE(code, '') as code,
			   COALESCE(item_number, '') as item_number,
			   COALESCE(resource_type, '') as resource_type,
			   sort_order
		FROM construction_estimate_line
		WHERE estimate_id = $1 AND tenant_id = $2
		ORDER BY sort_order
	`, estimateID, tenantID)
	if err != nil {
		h.log.Error("Failed to fetch estimate lines for Forma 2", "error", err)
		return 0
	}
	defer rows.Close()

	type estLine struct {
		ID            int64
		Name          string
		UOM           string
		Quantity      float64
		MaterialRate  float64
		LaborRate     float64
		EquipmentRate float64
		Code          string
		ItemNumber    string
		ResourceType  string
		SortOrder     int
	}

	var lines []estLine
	for rows.Next() {
		var l estLine
		err := rows.Scan(&l.ID, &l.Name, &l.UOM, &l.Quantity,
			&l.MaterialRate, &l.LaborRate, &l.EquipmentRate,
			&l.Code, &l.ItemNumber, &l.ResourceType, &l.SortOrder)
		if err != nil {
			h.log.Error("Failed to scan estimate line for Forma 2", "error", err)
			continue
		}
		lines = append(lines, l)
	}

	if len(lines) == 0 {
		h.log.Info("No estimate lines found for auto Forma 2", "estimate_id", estimateID)
		return 0
	}

	// For edinich, skip resource sub-items (labor/equipment/material children)
	// and only include parent work items
	var f2Lines []estLine
	if sourceType == "edinich" {
		for _, l := range lines {
			rt := strings.ToLower(strings.TrimSpace(l.ResourceType))
			if rt == "" || rt == "material" {
				// Parent work items have empty resource_type; include those + material items
				// Only include if they have an item_number (parent rows)
				if l.ItemNumber != "" {
					f2Lines = append(f2Lines, l)
				}
			}
		}
		// If no parent rows found (all have item numbers), include all non-resource lines
		if len(f2Lines) == 0 {
			f2Lines = lines
		}
	} else {
		// VOR: include all lines
		f2Lines = lines
	}

	h.log.Info("Auto Forma 2: line filtering complete",
		"total_lines", len(lines), "f2_lines", len(f2Lines), "source_type", sourceType)

	// Generate act name
	var actCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE project_id = $1 AND tenant_id = $2 AND act_type = 'ks2'`,
		projectID, tenantID).Scan(&actCount)
	actName := fmt.Sprintf("KS2-%03d", actCount+1)

	// Auto-assign act_number for the subcontract
	var actNumber int
	if subcontractID.Valid && subcontractID.Int64 > 0 {
		h.db.QueryRow(`SELECT COALESCE(MAX(act_number), 0) + 1 FROM construction_act WHERE subcontract_id = $1 AND act_type = 'ks2' AND tenant_id = $2`,
			subcontractID.Int64, tenantID).Scan(&actNumber)
	}

	// Create the act within a transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin tx for auto Forma 2", "error", err)
		return 0
	}
	defer tx.Rollback()

	var subcontractIDVal interface{}
	if subcontractID.Valid && subcontractID.Int64 > 0 {
		subcontractIDVal = subcontractID.Int64
	}

	// Pass through the estimate's building_id so the auto-created Forma 2
	// belongs to the same building/block as the source estimate.
	var actBuildingID interface{}
	if buildingID.Valid {
		actBuildingID = buildingID.Int64
	}

	var actID int64
	// Running base values we'll add the new lines onto. For a brand-new act
	// these start at zero. For a reused draft we seed them from the existing
	// row so the final UPDATE writes the merged totals rather than
	// overwriting what was already there.
	var sortOffset int
	var existingTotal, existingLabor, existingEquip, existingMaterials float64

	if reuseActID > 0 {
		actID = reuseActID
		// Pick up where the previous lines left off. Using MAX(sort_order)
		// keeps the merged list in the order we inserted it — VOR lines
		// first, then Единич, then Ресурс (whatever order the caller ran).
		_ = tx.QueryRow(
			`SELECT COALESCE(MAX(sort_order), 0) FROM construction_act_line WHERE act_id = $1`,
			actID,
		).Scan(&sortOffset)
		_ = tx.QueryRow(`
			SELECT COALESCE(amount_total, 0),
			       COALESCE(f2_labor_total, 0),
			       COALESCE(f2_equipment_total, 0),
			       COALESCE(f2_materials_total, 0)
			FROM construction_act WHERE id = $1
		`, actID).Scan(&existingTotal, &existingLabor, &existingEquip, &existingMaterials)
		h.log.Info("Reusing draft Forma 2 for auto-create merge",
			"existing_act_id", actID, "estimate_id", estimateID,
			"sort_offset", sortOffset, "existing_total", existingTotal)
	} else {
		// `source_file_name` is the dedup key that a subsequent estimate
		// from the same uploaded file will match on — see the reuse
		// branch above. nullStringFromVal keeps the column NULL when
		// the caller didn't supply a name.
		err = tx.QueryRow(`
			INSERT INTO construction_act (
				tenant_id, project_id, subcontract_id, building_id, name, act_type,
				amount_total, currency, state, notes,
				created_by, created_date, updated_date,
				act_number, vat_pct,
				f2_transport_pct, f2_other_pct,
				source_file_name
			) VALUES ($1, $2, $3, $4, $5, 'ks2',
				0, 'UZS', 'draft', $6,
				$7, NOW(), NOW(),
				$8, 12,
				5, 17,
				$9
			)
			RETURNING id
		`, tenantID, projectID, subcontractIDVal, actBuildingID, actName,
			fmt.Sprintf("Avtomatik yaratildi: %s smetasidan", estName),
			userID,
			nullInt64FromVal(int64(actNumber)),
			nullStringFromVal(sourceFileName),
		).Scan(&actID)
		if err != nil {
			h.log.Error("Failed to insert auto Forma 2 act", "error", err)
			return 0
		}
	}

	// Insert act lines from estimate lines
	// Use savepoints so a single failed INSERT does not abort the entire transaction
	var totalAmount float64
	var sumLabor, sumEquip, sumMaterials float64
	var insertedLines int
	for i, l := range f2Lines {
		unitRate := l.MaterialRate + l.LaborRate + l.EquipmentRate
		lineTotal := l.Quantity * unitRate

		// Create savepoint before each insert
		spName := fmt.Sprintf("sp_line_%d", i)
		if _, spErr := tx.Exec("SAVEPOINT " + spName); spErr != nil {
			h.log.Error("Failed to create savepoint for auto Forma 2 line", "error", spErr, "name", l.Name)
			continue
		}

		_, err := tx.Exec(`
			INSERT INTO construction_act_line (
				act_id, estimate_line_id, name, uom,
				quantity, unit_rate, total_amount, sort_order,
				qty_smeta, norm_code,
				line_number_display,
				labor_amount, equipment_amount, materials_amount, cables_amount
			) VALUES ($1, $2, $3, $4,
				$5, $6, $7, $8,
				$9, $10,
				$11,
				$12, $13, $14, 0)
		`, actID, l.ID, l.Name, l.UOM,
			l.Quantity, unitRate, lineTotal, sortOffset+i+1,
			l.Quantity, nullStringFromVal(l.Code),
			nullStringFromVal(l.ItemNumber),
			l.Quantity*l.LaborRate, l.Quantity*l.EquipmentRate, l.Quantity*l.MaterialRate,
		)
		if err != nil {
			h.log.Error("Failed to insert auto Forma 2 line", "error", err, "name", l.Name)
			// Rollback to savepoint so the transaction remains usable
			tx.Exec("ROLLBACK TO SAVEPOINT " + spName)
			continue
		}

		// Release savepoint on success and count totals
		tx.Exec("RELEASE SAVEPOINT " + spName)
		totalAmount += lineTotal
		sumLabor += l.Quantity * l.LaborRate
		sumEquip += l.Quantity * l.EquipmentRate
		sumMaterials += l.Quantity * l.MaterialRate
		insertedLines++
	}

	h.log.Info("Auto Forma 2 lines insert result",
		"total_lines", len(f2Lines), "inserted", insertedLines,
		"skipped", len(f2Lines)-insertedLines, "reused", reuseActID > 0)

	// Merge with the existing totals when we're appending to a reused draft.
	mergedTotal := totalAmount + existingTotal
	mergedLabor := sumLabor + existingLabor
	mergedEquip := sumEquip + existingEquip
	mergedMaterials := sumMaterials + existingMaterials

	// Update totals
	vatAmount := mergedTotal * 12 / 100
	totalWithVat := mergedTotal + vatAmount
	tx.Exec(`UPDATE construction_act SET
			amount_total = $1, vat_amount = $2, amount_total_with_vat = $3,
			f2_labor_total = $4, f2_equipment_total = $5, f2_materials_total = $6, f2_cables_total = 0,
			updated_date = NOW()
		WHERE id = $7`,
		mergedTotal, vatAmount, totalWithVat,
		mergedLabor, mergedEquip, mergedMaterials,
		actID)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit auto Forma 2", "error", err)
		return 0
	}

	h.log.Info("Auto Forma 2 merge result",
		"act_id", actID, "act_name", actName,
		"estimate_id", estimateID, "source_type", sourceType,
		"reused_draft", reuseActID > 0,
		"appended_lines", len(f2Lines), "appended_total", totalAmount,
		"merged_total", mergedTotal)

	// Activity logging — different verb depending on whether we created
	// a fresh act or merged into an existing draft.
	if reuseActID > 0 {
		h.logConstructionActivity(tenantID, projectID, userID, "act",
			fmt.Sprintf("Forma 2 ga %s smetasining qatorlari qo'shildi", estName),
			"Act", actID)
	} else {
		h.logConstructionActivity(tenantID, projectID, userID, "act",
			fmt.Sprintf("Forma 2 avtomatik yaratildi: %s (%s smetasidan)", actName, estName),
			"Act", actID)
	}

	return actID
}

// ─── Auto-create products from estimate resource lines ───────────────────────

// truncateRuneSafe truncates a string to at most maxRunes Unicode characters.
func truncateRuneSafe(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return s
}

// autoCreateProductsFromEstimateLines creates products for resource lines
// that are material type (excludes ЧЕЛ.-Ч labor and МАШ.-Ч equipment).
// Returns number of products created.
func (h *Handler) autoCreateProductsFromEstimateLines(tenantID, orgID, userID uuid.UUID, lines []entity.CreateEstimateLineInput) int {
	created := 0
	skipped := 0
	errors := 0

	// Deduplicate by name (case-insensitive)
	seen := make(map[string]bool)

	// Track codes used in this batch to avoid collisions
	usedCodes := make(map[string]bool)

	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Resolve which organizations the new product should be linked to via
	// product_organization_settings. The Inventory list page INNER JOINs
	// product_organization_settings on the active org, so a product with
	// NO org link is invisible everywhere — that's why "auto-created
	// products don't show up in inventory" was the most common report.
	//
	// Strategy:
	//   • If the request had an active org → link to just that org.
	//   • Otherwise → link to every active organization of the tenant so
	//     the product is visible regardless of which company the user
	//     later switches to.
	var targetOrgIDs []uuid.UUID
	if orgID != uuid.Nil {
		targetOrgIDs = []uuid.UUID{orgID}
	} else {
		orgRows, qErr := h.db.Query(
			`SELECT id FROM organizations WHERE tenant_id = $1 AND COALESCE(is_active, true) = true`,
			tenantID,
		)
		if qErr != nil {
			h.log.Error("Failed to load tenant organizations for auto-create",
				"error", qErr, "tenant_id", tenantID)
		} else {
			for orgRows.Next() {
				var oid uuid.UUID
				if scanErr := orgRows.Scan(&oid); scanErr == nil {
					targetOrgIDs = append(targetOrgIDs, oid)
				}
			}
			orgRows.Close()
		}
	}

	for _, line := range lines {
		name := strings.TrimSpace(line.Name)
		if name == "" {
			continue
		}

		// Skip labor and equipment resources by UOM
		uomUpper := strings.ToUpper(strings.TrimSpace(line.UOM))
		if strings.Contains(uomUpper, "ЧЕЛ") && strings.Contains(uomUpper, "Ч") {
			continue // Labor — employee hours
		}
		if strings.Contains(uomUpper, "МАШ") && strings.Contains(uomUpper, "Ч") {
			continue // Equipment — machine hours
		}

		// Also skip by resource_type if explicitly set
		rt := strings.ToLower(strings.TrimSpace(line.ResourceType))
		if rt == "labor" || rt == "equipment" {
			continue
		}

		// Skip BOP/section headers (they have no UOM or are parent items)
		if line.UOM == "" && line.MaterialRate == 0 && line.LaborRate == 0 && line.EquipmentRate == 0 {
			continue
		}

		// Deduplicate within this import batch
		nameKey := strings.ToUpper(name)
		if seen[nameKey] {
			continue
		}
		seen[nameKey] = true

		// Check if product with this name already exists for this tenant
		var exists bool
		err := h.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM products WHERE tenant_id = $1 AND UPPER(name) = UPPER($2) AND deleted_at IS NULL)`,
			tenantID, name,
		).Scan(&exists)
		if err != nil {
			h.log.Error("Failed to check product existence", "error", err, "name", name)
			errors++
			continue
		}
		if exists {
			skipped++
			continue
		}

		// Resolve UOM to unit_id
		unitID := h.resolveUOMCode(tenantID, line.UOM)

		// Determine cost price from the estimate rates
		costPrice := line.MaterialRate
		if costPrice == 0 {
			costPrice = line.MaterialRate + line.LaborRate + line.EquipmentRate
		}

		// Generate a product code from the estimate code or name
		code := strings.TrimSpace(line.Code)
		if code == "" {
			// Generate code from first runes (Unicode-safe), keep under VARCHAR(50)
			nameClean := strings.ReplaceAll(strings.ToUpper(truncateRuneSafe(name, 15)), " ", "")
			code = truncateRuneSafe(fmt.Sprintf("EST-%s", nameClean), 40)
		}

		// Ensure code uniqueness — check both DB and current batch
		for attempt := 0; attempt < 5; attempt++ {
			var codeExists bool
			h.db.QueryRow(
				`SELECT EXISTS(SELECT 1 FROM products WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)`,
				tenantID, code,
			).Scan(&codeExists)
			if !codeExists && !usedCodes[code] {
				break
			}
			code = fmt.Sprintf("%s-%s", code, uuid.New().String()[:6])
		}
		usedCodes[code] = true

		id := uuid.New()
		now := time.Now()

		// Copy search_key ONLY if an existing same-name product in the
		// tenant already has one. We do NOT derive a key from the
		// smeta name — construction names are long GOST Cyrillic
		// strings that produce meaningless keys. Keys are meant to be
		// set by hand on the manufacturing side (short technical
		// codes); the construction side just picks them up when names
		// match. If there is no match, leave the column NULL so the
		// product remains unlinked until a user sets a key manually.
		var searchKeyPtr *string
		if k := h.lookupSearchKeyForName(tenantID, name); k != "" {
			searchKeyPtr = &k
		}

		_, err = h.db.Exec(`
			INSERT INTO products (
				id, tenant_id, origin_organization_id, type, code, name, search_key,
				unit_id, cost_price, list_price,
				is_stockable, track_inventory,
				is_purchasable, is_sellable, can_be_sold, can_be_purchased,
				can_be_expensed, inventory_type,
				is_active, tags, created_by, created_at, updated_at
			) VALUES (
				$1, $2, $3, 'product', $4, $5, $6,
				$7, $8, $8,
				true, true,
				true, false, false, true,
				true, 'trade',
				true, '["estimate-import"]'::jsonb, $9, $10, $10
			)
		`, id, tenantID, orgIDPtr, code, name, searchKeyPtr,
			unitID, costPrice,
			userID, now,
		)
		if err != nil {
			h.log.Error("Failed to auto-create product from estimate", "error", err, "name", name, "code", code)
			errors++
			continue
		}

		// Link the product to every target organization so it shows up in
		// the Mahsulotlar / Inventory list under the right company. With
		// no rows here the product is created but invisible because the
		// inventory list INNER JOINs product_organization_settings.
		for _, targetOrg := range targetOrgIDs {
			if _, posErr := h.db.Exec(`
				INSERT INTO product_organization_settings (
					tenant_id, product_id, organization_id,
					cost_price, list_price, min_price,
					min_stock_level, reorder_point, reorder_quantity
				) VALUES ($1, $2, $3, $4, $4, 0, 0, 0, 0)
				ON CONFLICT (product_id, organization_id) DO NOTHING
			`, tenantID, id, targetOrg, costPrice); posErr != nil {
				h.log.Error("Failed to link auto-created product to organization",
					"error", posErr, "product_id", id, "organization_id", targetOrg)
			}
		}

		created++
	}

	h.log.Info("Auto-create products from estimate completed",
		"created", created, "skipped_existing", skipped, "errors", errors,
		"total_lines", len(lines))

	return created
}

// ─── Cross-estimate price propagation ────────────────────────────────────────
//
// propagateResursPricesForProject copies unit_rate (+ the labor / equipment /
// material rate splits and total_amount) from the project's Ресурс estimate(s)
// into any 0-priced sub-lines in any other estimate in the same project.
//
// Why this exists: the Единич sheet has only norm and quantity columns —
// no per-resource price. So when the Единич parser emits sub-line resources
// they all carry unit_rate=0. The Ресурс estimate is where prices live, and
// users typically import all three (ВОР / Единич / Ресурс) for a building.
// Without this step, the Smeta boshqaruvi tab shows NARX = 0 and
// SUMMA = 0 for every sub-resource even though the Ресурс estimate has the
// number right next door.
//
// Idempotency:
//   • only touches rows where unit_rate is currently 0, so manual edits
//     made from the Resurslar tab aren't overwritten.
//   • only touches sub-lines (parent_line_id IS NOT NULL), never the
//     top-level work rows or the Ресурс lines themselves.
//
// Match key: case-insensitive name + uom. When several Ресурс estimates
// have the same resource, the most-recently-created one wins (DISTINCT ON
// + ORDER BY created_date DESC).
func (h *Handler) propagateResursPricesForProject(tenantID uuid.UUID, projectID int64) {
	if _, err := h.db.Exec(`
		WITH price_src AS (
		    SELECT DISTINCT ON (LOWER(srcLine.name), COALESCE(srcLine.uom, ''))
		        LOWER(srcLine.name)         AS name_key,
		        COALESCE(srcLine.uom, '')   AS uom_key,
		        srcLine.unit_rate           AS unit_rate,
		        srcLine.material_rate       AS material_rate,
		        srcLine.labor_rate          AS labor_rate,
		        srcLine.equipment_rate      AS equipment_rate
		    FROM construction_estimate_line srcLine
		    JOIN construction_estimate src ON src.id = srcLine.estimate_id
		    WHERE src.tenant_id  = $1
		      AND src.project_id = $2
		      AND LOWER(COALESCE(src.source_type, '')) = 'resurs'
		      AND COALESCE(srcLine.unit_rate, 0) > 0
		    ORDER BY LOWER(srcLine.name),
		             COALESCE(srcLine.uom, ''),
		             srcLine.created_date DESC
		)
		UPDATE construction_estimate_line tgt
		SET unit_rate      = ps.unit_rate,
		    material_rate  = ps.material_rate,
		    labor_rate     = ps.labor_rate,
		    equipment_rate = ps.equipment_rate,
		    total_amount   = ps.unit_rate * COALESCE(tgt.quantity, 0),
		    updated_date   = NOW()
		FROM price_src ps, construction_estimate tgt_e
		WHERE tgt_e.id              = tgt.estimate_id
		  AND tgt.tenant_id         = $1
		  AND tgt_e.project_id      = $2
		  AND LOWER(COALESCE(tgt_e.source_type, '')) <> 'resurs'
		  AND tgt.parent_line_id IS NOT NULL
		  AND COALESCE(tgt.unit_rate, 0) = 0
		  AND LOWER(tgt.name)       = ps.name_key
		  AND COALESCE(tgt.uom, '') = ps.uom_key
	`, tenantID, projectID); err != nil {
		h.log.Error("Failed to propagate Ресурс prices",
			"error", err, "project_id", projectID)
	}
}
