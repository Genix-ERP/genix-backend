package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

	query := fmt.Sprintf(`
		SELECT e.id, e.tenant_id, e.project_id, e.building_id, e.version, e.name, e.state, e.is_current,
		       e.overhead_pct, e.profit_pct, e.vat_pct,
		       e.amount_direct, e.amount_total,
		       COALESCE(e.source_type, '') as source_type,
		       e.subcontract_id,
		       e.approved_by, e.approved_date, e.created_by,
		       e.created_date, e.updated_date,
		       COALESCE((SELECT COUNT(*) FROM construction_estimate_line l WHERE l.estimate_id = e.id), 0) as lines_count,
		       COALESCE(ua.first_name || ' ' || ua.last_name, '') as approved_name,
		       COALESCE(uc.first_name || ' ' || uc.last_name, '') as created_name,
		       COALESCE(b.name, '') as building_name,
		       COALESCE(sc.name, '') as subcontract_name
		FROM construction_estimate e
		LEFT JOIN users ua ON ua.id = e.approved_by
		LEFT JOIN users uc ON uc.id = e.created_by
		LEFT JOIN construction_buildings b ON b.id = e.building_id
		LEFT JOIN construction_subcontract sc ON sc.id = e.subcontract_id
		WHERE e.project_id = $1 AND e.tenant_id = $2 %s
		ORDER BY e.version DESC
	`, scopeFilter)

	rows, err := h.db.Query(query, projectID, tenantID)
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

	response.Success(c, items)
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

	// Get lines
	lines := h.getEstimateLines(id, tenantID)

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

	// Auto-create a construction stage when VOR estimate is created
	if req.SourceType == "vor" && req.Name != "" {
		var existingStageID int64
		err := h.db.QueryRow(
			`SELECT id FROM construction_stages WHERE project_id = $1 AND tenant_id = $2 AND name = $3 LIMIT 1`,
			projectID, tenantID, req.Name,
		).Scan(&existingStageID)
		if err != nil {
			// Stage doesn't exist, create it
			h.db.Exec(`
				INSERT INTO construction_stages (tenant_id, project_id, name, status, planned_budget, stage_order, created_at, updated_at)
				VALUES ($1, $2, $3, 'not_started', 0, (SELECT COALESCE(MAX(stage_order), 0) + 1 FROM construction_stages WHERE project_id = $2 AND tenant_id = $1), NOW(), NOW())
			`, tenantID, projectID, req.Name)
		}
	}

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

	// Copy lines
	_, err = tx.Exec(`
		INSERT INTO construction_estimate_line (
			tenant_id, estimate_id, wbs_id, name, uom, quantity,
			material_rate, labor_rate, equipment_rate,
			unit_rate, total_amount, sort_order,
			created_date, updated_date
		)
		SELECT $1, $2, wbs_id, name, uom, quantity,
		       material_rate, labor_rate, equipment_rate,
		       unit_rate, total_amount, sort_order,
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

// ListEstimateLines returns lines for an estimate
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

	lines := h.getEstimateLines(estimateID, tenantID)
	response.Success(c, lines)
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

	// Aggregate across duplicate names by taking the MAX rate — so a resource
	// that has a non-zero rate in at least one estimate line is returned
	// with that rate (avoids picking a 0-rate skeleton row).
	query := `
		SELECT
			MIN(el.id) AS id,
			el.name,
			MAX(el.uom) AS uom,
			MAX(el.quantity) AS quantity,
			MAX(el.material_rate) AS material_rate,
			MAX(el.labor_rate) AS labor_rate,
			MAX(el.equipment_rate) AS equipment_rate,
			MAX(el.unit_rate) AS unit_rate,
			MAX(COALESCE(el.code, '')) AS code,
			MAX(COALESCE(el.resource_type, '')) AS resource_type
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		WHERE e.tenant_id = $1
		  AND e.project_id = $2
		  AND el.name != ''
	`
	args := []interface{}{tenantID, projectID}
	argIdx := 3

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

	query += ` GROUP BY el.name ORDER BY UPPER(el.name) LIMIT 500`

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

	// Check state
	var state string
	err = h.db.QueryRow(`SELECT state FROM construction_estimate WHERE id = $1 AND tenant_id = $2`, estimateID, tenantID).Scan(&state)
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

	// Calculate rates
	unitRate := req.MaterialRate + req.LaborRate + req.EquipmentRate
	totalAmount := unitRate * req.Quantity

	uom := req.UOM
	if uom == "" {
		uom = "шт"
	}

	var lineID int64
	err = h.db.QueryRow(`
		INSERT INTO construction_estimate_line (
			tenant_id, estimate_id, wbs_id, name, uom, quantity,
			material_rate, labor_rate, equipment_rate,
			unit_rate, total_amount, code, item_number,
			resource_type, parent_item_number, sort_order,
			created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, NOW(), NOW())
		RETURNING id
	`, tenantID, estimateID, nullInt64FromVal(req.WBSID),
		req.Name, uom, req.Quantity,
		req.MaterialRate, req.LaborRate, req.EquipmentRate,
		unitRate, totalAmount, nullStringFromVal(req.Code), nullStringFromVal(req.ItemNumber),
		nullStringFromVal(req.ResourceType), nullStringFromVal(req.ParentItemNumber), req.SortOrder,
	).Scan(&lineID)

	if err != nil {
		h.log.Error("Failed to create estimate line", "error", err)
		response.InternalError(c, "Failed to create estimate line")
		return
	}

	// Recalculate estimate totals
	h.recalculateEstimateTotals(estimateID)

	response.Success(c, map[string]interface{}{
		"id": lineID,
	})
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

	// Check state
	var state string
	err = h.db.QueryRow(`SELECT state FROM construction_estimate WHERE id = $1 AND tenant_id = $2`, estimateID, tenantID).Scan(&state)
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

	count := 0
	for i, line := range req.Lines {
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

		_, err := tx.Exec(`
			INSERT INTO construction_estimate_line (
				tenant_id, estimate_id, wbs_id, name, uom, quantity,
				material_rate, labor_rate, equipment_rate,
				unit_rate, total_amount, code, item_number,
				resource_type, parent_item_number, sort_order,
				created_date, updated_date
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, NOW(), NOW())
		`, tenantID, estimateID, nullInt64FromVal(line.WBSID),
			line.Name, uom, line.Quantity,
			line.MaterialRate, line.LaborRate, line.EquipmentRate,
			unitRate, totalAmount, nullStringFromVal(line.Code), nullStringFromVal(line.ItemNumber),
			nullStringFromVal(line.ResourceType), nullStringFromVal(line.ParentItemNumber), sortOrder,
		)
		if err != nil {
			h.log.Error("Failed to insert estimate line", "error", err, "index", i)
			response.InternalError(c, fmt.Sprintf("Failed to insert line %d: %s", i+1, line.Name))
			return
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to complete import")
		return
	}

	// Recalculate estimate totals
	h.recalculateEstimateTotals(estimateID)

	// Auto-create products from imported resource lines
	// Exclude labor (ЧЕЛ.-Ч) and equipment (МАШ.-Ч) resources
	orgID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)
	productsCreated := h.autoCreateProductsFromEstimateLines(tenantID, orgID, userID, req.Lines)

	response.Success(c, map[string]interface{}{
		"count":            count,
		"products_created": productsCreated,
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

	// Check estimate state
	var state string
	var estimateID int64
	err = h.db.QueryRow(`
		SELECT e.state, l.estimate_id
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE l.id = $1 AND l.tenant_id = $2
	`, lineID, tenantID).Scan(&state, &estimateID)
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
		req.Quantity == nil && req.MaterialRate == nil && req.LaborRate == nil && req.EquipmentRate == nil && req.SortOrder == nil

	if state != "draft" && !isActualAmountOnly {
		response.BadRequest(c, "Only draft estimates can be modified")
		return
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

	// Recalculate estimate totals
	h.recalculateEstimateTotals(estimateID)

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

	// Check estimate state and get estimate_id
	var state string
	var estimateID int64
	err = h.db.QueryRow(`
		SELECT e.state, l.estimate_id
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE l.id = $1 AND l.tenant_id = $2
	`, lineID, tenantID).Scan(&state, &estimateID)
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

	response.Success(c, map[string]interface{}{
		"message": "Estimate line deleted successfully",
	})
}

// =====================================================
// HELPER FUNCTIONS
// =====================================================

// getEstimateLines retrieves all lines for an estimate
func (h *Handler) getEstimateLines(estimateID int64, tenantID uuid.UUID) []entity.ConstructionEstimateLine {
	query := `
		SELECT l.id, l.tenant_id, l.estimate_id, l.wbs_id,
		       l.name, l.uom, l.quantity,
		       l.material_rate, l.labor_rate, l.equipment_rate,
		       l.unit_rate, l.total_amount, l.actual_amount,
		       COALESCE(l.code, ''), COALESCE(l.item_number, ''),
		       COALESCE(l.resource_type, ''), COALESCE(l.parent_item_number, ''),
		       l.sort_order, l.created_date, l.updated_date,
		       COALESCE(w.code, '') as wbs_code,
		       COALESCE(w.name, '') as wbs_name
		FROM construction_estimate_line l
		LEFT JOIN construction_wbs w ON w.id = l.wbs_id
		WHERE l.estimate_id = $1 AND l.tenant_id = $2
		ORDER BY l.sort_order ASC, l.id ASC
	`

	rows, err := h.db.Query(query, estimateID, tenantID)
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
			&line.SortOrder, &line.CreatedDate, &line.UpdatedDate,
			&line.WBSCode, &line.WBSName,
		); err != nil {
			h.log.Error("Failed to scan estimate line", "error", err)
			continue
		}
		lines = append(lines, line)
	}

	return lines
}

// recalculateEstimateTotals updates the estimate header with recalculated totals
func (h *Handler) recalculateEstimateTotals(estimateID int64) {
	// Sum direct costs from lines
	var amountDirect float64
	err := h.db.QueryRow(
		`SELECT COALESCE(SUM(total_amount), 0) FROM construction_estimate_line WHERE estimate_id = $1`,
		estimateID,
	).Scan(&amountDirect)
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

// ─── Auto-create products from estimate resource lines ───────────────────────

// autoCreateProductsFromEstimateLines creates products for resource lines
// that are material type (excludes ЧЕЛ.-Ч labor and МАШ.-Ч equipment).
// Returns number of products created.
func (h *Handler) autoCreateProductsFromEstimateLines(tenantID, orgID, userID uuid.UUID, lines []entity.CreateEstimateLineInput) int {
	created := 0

	// Deduplicate by name (case-insensitive)
	seen := make(map[string]bool)

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
			continue
		}
		if exists {
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
			// Generate code from first letters + index
			code = fmt.Sprintf("EST-%s", strings.ReplaceAll(strings.ToUpper(name[:min(len(name), 10)]), " ", ""))
		}

		// Ensure code uniqueness
		var codeExists bool
		h.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM products WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)`,
			tenantID, code,
		).Scan(&codeExists)
		if codeExists {
			code = fmt.Sprintf("%s-%s", code, uuid.New().String()[:4])
		}

		id := uuid.New()
		now := time.Now()

		var orgIDPtr *uuid.UUID
		if orgID != uuid.Nil {
			orgIDPtr = &orgID
		}

		_, err = h.db.Exec(`
			INSERT INTO products (
				id, tenant_id, origin_organization_id, type, code, name,
				unit_id, cost_price, list_price,
				is_stockable, track_inventory,
				is_purchasable, is_sellable, can_be_sold, can_be_purchased,
				can_be_expensed, inventory_type,
				is_active, tags, created_by, created_at, updated_at
			) VALUES (
				$1, $2, $3, 'product', $4, $5,
				$6, $7, $7,
				true, true,
				true, false, false, true,
				true, 'trade',
				true, '["estimate-import"]'::jsonb, $8, $9, $9
			)
		`, id, tenantID, orgIDPtr, code, name,
			unitID, costPrice,
			userID, now,
		)
		if err != nil {
			h.log.Error("Failed to auto-create product from estimate", "error", err, "name", name)
			continue
		}

		// Create org settings if org is set
		if orgID != uuid.Nil {
			h.db.Exec(`
				INSERT INTO product_organization_settings (
					tenant_id, product_id, organization_id,
					cost_price, list_price, min_price,
					min_stock_level, reorder_point, reorder_quantity
				) VALUES ($1, $2, $3, $4, $4, 0, 0, 0, 0)
				ON CONFLICT (product_id, organization_id) DO NOTHING
			`, tenantID, id, orgID, costPrice)
		}

		created++
		h.log.Info("Auto-created product from estimate", "name", name, "code", code, "cost", costPrice)
	}

	return created
}

