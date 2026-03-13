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

	query := `
		SELECT e.id, e.tenant_id, e.project_id, e.building_id, e.version, e.name, e.state, e.is_current,
		       e.overhead_pct, e.profit_pct, e.vat_pct,
		       e.amount_direct, e.amount_total,
		       COALESCE(e.source_type, '') as source_type,
		       e.approved_by, e.approved_date, e.created_by,
		       e.created_date, e.updated_date,
		       COALESCE((SELECT COUNT(*) FROM construction_estimate_line l WHERE l.estimate_id = e.id), 0) as lines_count,
		       COALESCE(ua.first_name || ' ' || ua.last_name, '') as approved_name,
		       COALESCE(uc.first_name || ' ' || uc.last_name, '') as created_name,
		       COALESCE(b.name, '') as building_name
		FROM construction_estimate e
		LEFT JOIN users ua ON ua.id = e.approved_by
		LEFT JOIN users uc ON uc.id = e.created_by
		LEFT JOIN construction_buildings b ON b.id = e.building_id
		WHERE e.project_id = $1 AND e.tenant_id = $2
		ORDER BY e.version DESC
	`

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
			&item.ApprovedBy, &item.ApprovedDate, &item.CreatedBy,
			&item.CreatedDate, &item.UpdatedDate,
			&item.LinesCount, &item.ApprovedName, &item.CreatedName,
			&item.BuildingName,
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
		       e.approved_by, e.approved_date, e.created_by,
		       e.created_date, e.updated_date,
		       0 as lines_count,
		       COALESCE(ua.first_name || ' ' || ua.last_name, '') as approved_name,
		       COALESCE(uc.first_name || ' ' || uc.last_name, '') as created_name,
		       COALESCE(b.name, '') as building_name
		FROM construction_estimate e
		LEFT JOIN users ua ON ua.id = e.approved_by
		LEFT JOIN users uc ON uc.id = e.created_by
		LEFT JOIN construction_buildings b ON b.id = e.building_id
		WHERE e.id = $1 AND e.tenant_id = $2
	`, id, tenantID).Scan(
		&est.ID, &est.TenantID, &est.ProjectID, &est.BuildingID, &est.Version, &est.Name, &est.State, &est.IsCurrent,
		&est.OverheadPct, &est.ProfitPct, &est.VatPct,
		&est.AmountDirect, &est.AmountTotal,
		&est.SourceType,
		&est.ApprovedBy, &est.ApprovedDate, &est.CreatedBy,
		&est.CreatedDate, &est.UpdatedDate,
		&est.LinesCount, &est.ApprovedName, &est.CreatedName,
		&est.BuildingName,
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
			source_type, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, 'draft', false, $6, $7, $8, $9, $10, NOW(), NOW())
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(req.BuildingID), nextVersion, req.Name,
		req.OverheadPct, req.ProfitPct, req.VatPct,
		nullStringFromVal(req.SourceType), userID,
	).Scan(&itemID)

	if err != nil {
		h.log.Error("Failed to create estimate", "error", err)
		response.InternalError(c, "Failed to create estimate")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "estimate", fmt.Sprintf("Smeta yaratildi: v%d - %s", nextVersion, req.Name), "Estimate", itemID)

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
		SELECT id, tenant_id, project_id, building_id, version, name, overhead_pct, profit_pct, vat_pct
		FROM construction_estimate WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&src.ID, &src.TenantID, &src.ProjectID, &src.BuildingID, &src.Version, &src.Name,
		&src.OverheadPct, &src.ProfitPct, &src.VatPct,
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
			created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, 'draft', false, $6, $7, $8, $9, NOW(), NOW())
		RETURNING id
	`, tenantID, src.ProjectID, src.BuildingID, nextVersion,
		fmt.Sprintf("%s (v%d)", src.Name, nextVersion),
		src.OverheadPct, src.ProfitPct, src.VatPct, userID,
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

	response.Success(c, map[string]interface{}{
		"count": count,
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
