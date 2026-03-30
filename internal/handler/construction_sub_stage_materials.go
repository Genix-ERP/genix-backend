package handler

import (
	"fmt"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListSubStageMaterials returns all materials for a sub-stage
func (h *Handler) ListSubStageMaterials(c *gin.Context) {
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

	type Material struct {
		ID          int64     `json:"id"`
		SubStageID  int64     `json:"sub_stage_id"`
		ProductID   *string   `json:"product_id"`
		ProductName string    `json:"product_name"`
		UOM         string    `json:"uom"`
		Quantity    float64   `json:"quantity"`
		UnitCost    float64   `json:"unit_cost"`
		TotalCost   float64   `json:"total_cost"`
		CreatedDate time.Time `json:"created_date"`
	}

	rows, err := h.db.Query(`
		SELECT id, sub_stage_id, product_id, product_name, uom, quantity, unit_cost, total_cost, created_date
		FROM construction_sub_stage_materials
		WHERE sub_stage_id = $1 AND tenant_id = $2
		ORDER BY id ASC
	`, subStageID, tenantID)
	if err != nil {
		h.log.Error("Failed to list sub-stage materials", "error", err)
		response.InternalError(c, "Failed to list sub-stage materials")
		return
	}
	defer rows.Close()

	items := []Material{}
	for rows.Next() {
		var m Material
		if err := rows.Scan(&m.ID, &m.SubStageID, &m.ProductID, &m.ProductName, &m.UOM, &m.Quantity, &m.UnitCost, &m.TotalCost, &m.CreatedDate); err != nil {
			continue
		}
		items = append(items, m)
	}

	response.Success(c, items)
}

// ListStageMaterials returns all materials for all sub-stages of a stage (aggregated)
func (h *Handler) ListStageMaterials(c *gin.Context) {
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

	type Material struct {
		ID           int64   `json:"id"`
		SubStageID   int64   `json:"sub_stage_id"`
		SubStageName string  `json:"sub_stage_name"`
		ProductID    *string `json:"product_id"`
		ProductName  string  `json:"product_name"`
		UOM          string  `json:"uom"`
		Quantity     float64 `json:"quantity"`
		UnitCost     float64 `json:"unit_cost"`
		TotalCost    float64 `json:"total_cost"`
	}

	rows, err := h.db.Query(`
		SELECT m.id, m.sub_stage_id, ss.name as sub_stage_name,
		       m.product_id, m.product_name, m.uom, m.quantity, m.unit_cost, m.total_cost
		FROM construction_sub_stage_materials m
		JOIN construction_sub_stages ss ON ss.id = m.sub_stage_id
		WHERE ss.stage_id = $1 AND m.tenant_id = $2
		ORDER BY ss.sub_order ASC, m.id ASC
	`, stageID, tenantID)
	if err != nil {
		h.log.Error("Failed to list stage materials", "error", err)
		response.InternalError(c, "Failed to list stage materials")
		return
	}
	defer rows.Close()

	items := []Material{}
	for rows.Next() {
		var m Material
		if err := rows.Scan(&m.ID, &m.SubStageID, &m.SubStageName, &m.ProductID, &m.ProductName, &m.UOM, &m.Quantity, &m.UnitCost, &m.TotalCost); err != nil {
			continue
		}
		items = append(items, m)
	}

	response.Success(c, items)
}

// CreateSubStageMaterial adds a material to a sub-stage
func (h *Handler) CreateSubStageMaterial(c *gin.Context) {
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
		ProductID   string  `json:"product_id"`
		ProductName string  `json:"product_name" binding:"required"`
		UOM         string  `json:"uom"`
		Quantity    float64 `json:"quantity"`
		UnitCost    float64 `json:"unit_cost"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if req.UOM == "" {
		req.UOM = "шт"
	}

	totalCost := req.Quantity * req.UnitCost

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_sub_stage_materials (
			tenant_id, sub_stage_id, product_id, product_name, uom, quantity, unit_cost, total_cost, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING id
	`, tenantID, subStageID, nullStringFromVal(req.ProductID), req.ProductName, req.UOM, req.Quantity, req.UnitCost, totalCost).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create sub-stage material", "error", err)
		response.InternalError(c, "Failed to create sub-stage material")
		return
	}

	// Also record in material usage (sub_stage → stage → construction process → wbs/building)
	var projectID int64
	var buildingID, wbsID *int64
	var orgID *uuid.UUID
	err = h.db.QueryRow(`
		SELECT s.project_id,
		       (SELECT d.building_id FROM construction_daily_log d WHERE d.stage_id = s.id AND d.tenant_id = s.tenant_id ORDER BY d.date DESC LIMIT 1),
		       (SELECT d.wbs_id FROM construction_daily_log d WHERE d.stage_id = s.id AND d.tenant_id = s.tenant_id ORDER BY d.date DESC LIMIT 1),
		       cp.organization_id
		FROM construction_sub_stages ss
		JOIN construction_stages s ON s.id = ss.stage_id
		JOIN construction_projects cp ON cp.id = s.project_id
		WHERE ss.id = $1
	`, subStageID).Scan(&projectID, &buildingID, &wbsID, &orgID)
	if err == nil && projectID > 0 {
		userID, _ := middleware.GetUserID(c)
		h.db.Exec(`
			INSERT INTO construction_material_usage (
				tenant_id, project_id, building_id, wbs_id, product_id, product_name, uom, quantity_used,
				usage_date, recorded_by, notes, sub_stage_material_id, created_date, updated_date
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CURRENT_DATE, $9, $10, $11, NOW(), NOW())
		`, tenantID, projectID, buildingID, wbsID, nullStringFromVal(req.ProductID), req.ProductName, req.UOM, req.Quantity,
			userID, "Material assigned to stage", id)

		// Create accounting entry: Debit 5100 (Direct Materials) / Credit 1300 (Inventory)
		// This records the expense when materials are actually used in a construction stage
		if totalCost > 0 {
			now := time.Now()

			// Find journal
			var journalID uuid.UUID
			var nextNumber int
			h.db.QueryRow(`SELECT id, COALESCE(next_number,1) FROM journals WHERE tenant_id=$1 AND code IN ('STOCK','INVENTORY','MISC','GENERAL') AND deleted_at IS NULL ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'INVENTORY' THEN 1 WHEN 'MISC' THEN 2 ELSE 3 END LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)

			if journalID != uuid.Nil {
				debitAcct := findAccount(h.db, tenantID, orgID, "direct material", "5100")
				if debitAcct == uuid.Nil {
					debitAcct = findAccount(h.db, tenantID, orgID, "cost of goods", "5100")
				}
				if debitAcct == uuid.Nil {
					debitAcct = findAccount(h.db, tenantID, orgID, "cogs", "5000")
				}
				creditAcct := findAccount(h.db, tenantID, orgID, "inventory", "1300")

				if debitAcct != uuid.Nil && creditAcct != uuid.Nil {
					entryID := uuid.New()
					entryNumber := fmt.Sprintf("STG%06d", nextNumber)
					description := fmt.Sprintf("Stage material usage: %s x%.0f", req.ProductName, req.Quantity)

					h.db.Exec(`
						INSERT INTO journal_entries (
							id, tenant_id, organization_id, journal_id, entry_number, entry_date,
							description, source_type, source_id, status, total_debit, total_credit,
							created_by, created_at, updated_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, 'stage_material', $8, 'posted', $9, $9, $10, $11, $11)
					`, entryID, tenantID, orgID, journalID, entryNumber, now,
						description, fmt.Sprintf("%d", id), totalCost, userID, now)

					h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, $5, 0, 1, $6)`,
						uuid.New(), entryID, debitAcct, description, totalCost, now)
					h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, 0, $5, 2, $6)`,
						uuid.New(), entryID, creditAcct, description, totalCost, now)

					h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalCost, now, debitAcct)
					h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalCost, now, creditAcct)
					h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)

					h.log.Info("Stage material accounting entry created", "entry_id", entryID, "product", req.ProductName, "amount", totalCost)
				}
			}
		}
	}

	response.Success(c, map[string]interface{}{"id": id})
}

// UpdateSubStageMaterial updates quantity/cost of a sub-stage material
func (h *Handler) UpdateSubStageMaterial(c *gin.Context) {
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
		Quantity *float64 `json:"quantity"`
		UnitCost *float64 `json:"unit_cost"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get current values
	var qty, uc float64
	err = h.db.QueryRow(`SELECT quantity, unit_cost FROM construction_sub_stage_materials WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&qty, &uc)
	if err != nil {
		response.NotFound(c, "Material not found")
		return
	}

	if req.Quantity != nil {
		qty = *req.Quantity
	}
	if req.UnitCost != nil {
		uc = *req.UnitCost
	}
	totalCost := qty * uc

	_, err = h.db.Exec(`
		UPDATE construction_sub_stage_materials SET quantity = $1, unit_cost = $2, total_cost = $3, updated_date = NOW()
		WHERE id = $4 AND tenant_id = $5
	`, qty, uc, totalCost, id, tenantID)
	if err != nil {
		h.log.Error("Failed to update sub-stage material", "error", err)
		response.InternalError(c, "Failed to update sub-stage material")
		return
	}

	// Sync linked material usage record
	h.db.Exec(`
		UPDATE construction_material_usage SET quantity_used = $1, updated_date = NOW()
		WHERE sub_stage_material_id = $2 AND tenant_id = $3
	`, qty, id, tenantID)

	response.Success(c, map[string]interface{}{"id": id})
}

// DeleteSubStageMaterial removes a material from a sub-stage
func (h *Handler) DeleteSubStageMaterial(c *gin.Context) {
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

	result, err := h.db.Exec(`DELETE FROM construction_sub_stage_materials WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete sub-stage material", "error", err)
		response.InternalError(c, "Failed to delete sub-stage material")
		return
	}

	if ra, _ := result.RowsAffected(); ra == 0 {
		response.NotFound(c, "Material not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Deleted"})
}
