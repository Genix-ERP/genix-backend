package handler

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

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

	// Optional filters
	stageFilter := c.Query("stage_id")
	statusFilter := c.Query("status")

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
	stageQuery += ` ORDER BY stage_order ASC, id ASC`

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
		response.Success(c, map[string]interface{}{
			"stages": []interface{}{},
			"summary": map[string]interface{}{
				"material_plan_total": 0, "material_fact_total": 0,
				"equipment_plan_total": 0, "equipment_fact_total": 0,
				"plan_total": 0, "fact_total": 0, "difference": 0,
			},
		})
		return
	}

	// 2. Load sub-stages for these stages
	type SubStage struct {
		ID       int64  `json:"id"`
		StageID  int64  `json:"stage_id"`
		Name     string `json:"name"`
		SubOrder int    `json:"sub_order"`
		Status   string `json:"status"`
	}

	subRows, err := h.db.Query(`
		SELECT id, stage_id, name, sub_order, status
		FROM construction_sub_stages
		WHERE tenant_id = $1 AND stage_id = ANY($2)
		ORDER BY sub_order ASC, id ASC
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
		if err := subRows.Scan(&ss.ID, &ss.StageID, &ss.Name, &ss.SubOrder, &ss.Status); err != nil {
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
		for _, sub := range subs {
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
			ssr.Difference = ssr.PlanTotal - ssr.FactTotal

			sr.MaterialPlanTotal += ssr.MaterialPlanTotal
			sr.MaterialFactTotal += ssr.MaterialFactTotal
			sr.EquipPlanTotal += ssr.EquipPlanTotal
			sr.EquipFactTotal += ssr.EquipFactTotal

			sr.SubStages = append(sr.SubStages, ssr)
		}

		sr.PlanTotal = sr.MaterialPlanTotal + sr.EquipPlanTotal
		sr.FactTotal = sr.MaterialFactTotal + sr.EquipFactTotal
		sr.Difference = sr.PlanTotal - sr.FactTotal

		totalMaterialPlan += sr.MaterialPlanTotal
		totalMaterialFact += sr.MaterialFactTotal
		totalEquipPlan += sr.EquipPlanTotal
		totalEquipFact += sr.EquipFactTotal

		stages = append(stages, sr)
	}

	planTotal := totalMaterialPlan + totalEquipPlan
	factTotal := totalMaterialFact + totalEquipFact

	response.Success(c, map[string]interface{}{
		"stages": stages,
		"summary": map[string]interface{}{
			"material_plan_total":  totalMaterialPlan,
			"material_fact_total":  totalMaterialFact,
			"equipment_plan_total": totalEquipPlan,
			"equipment_fact_total": totalEquipFact,
			"plan_total":           planTotal,
			"fact_total":           factTotal,
			"difference":           planTotal - factTotal,
			"budget_used_pct":      budgetPct(factTotal, planTotal),
		},
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
		WorkUnit     string    `json:"work_unit"`
		PlanQuantity float64   `json:"plan_quantity"`
		FactQuantity float64   `json:"fact_quantity"`
		UnitPrice    float64   `json:"unit_price"`
		Note         *string   `json:"note"`
		CreatedAt    time.Time `json:"created_at"`
	}

	rows, err := h.db.Query(`
		SELECT id, sub_stage_id, name, work_unit, plan_quantity, fact_quantity, unit_price, note, created_at
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
		if err := rows.Scan(&e.ID, &e.SubStageID, &e.Name, &e.WorkUnit, &e.PlanQuantity, &e.FactQuantity, &e.UnitPrice, &e.Note, &e.CreatedAt); err != nil {
			continue
		}
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
		WorkUnit     string  `json:"work_unit"`
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

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_sub_stage_equipment (
			tenant_id, sub_stage_id, name, work_unit, plan_quantity, fact_quantity, unit_price, note
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, tenantID, subStageID, req.Name, req.WorkUnit, req.PlanQuantity, req.FactQuantity, req.UnitPrice, nullStringFromVal(req.Note)).Scan(&id)
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

	var rows *sql.Rows
	var qErr error

	if itemType != "" && itemIDStr != "" {
		itemID, _ := strconv.ParseInt(itemIDStr, 10, 64)
		rows, qErr = h.db.Query(`
			SELECT id, item_type, item_id, item_name, action, COALESCE(changes, '{}'), user_name, created_at
			FROM construction_reja_fakt_audit
			WHERE tenant_id = $1 AND item_type = $2 AND item_id = $3
			ORDER BY created_at DESC LIMIT 50
		`, tenantID, itemType, itemID)
	} else if subStageIDStr != "" {
		subStageID, _ := strconv.ParseInt(subStageIDStr, 10, 64)
		rows, qErr = h.db.Query(`
			SELECT id, item_type, item_id, item_name, action, COALESCE(changes, '{}'), user_name, created_at
			FROM construction_reja_fakt_audit
			WHERE tenant_id = $1 AND sub_stage_id = $2
			ORDER BY created_at DESC LIMIT 100
		`, tenantID, subStageID)
	} else {
		response.BadRequest(c, "Provide item_type+item_id or sub_stage_id")
		return
	}

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

	response.Success(c, entries)
}
