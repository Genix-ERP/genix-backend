package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION ACTS (KS-2 / KS-3) HANDLERS
// =====================================================

// ListActs returns acts for a project with optional filters
func (h *Handler) ListConstructionActs(c *gin.Context) {
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

	actType := c.Query("type")
	state := c.Query("state")
	subcontractIDStr := c.Query("subcontract_id")

	query := `
		SELECT a.id, a.name, a.act_type, a.project_id, a.subcontract_id,
		       a.period_from, a.period_to, a.amount_total, a.currency,
		       a.state, a.approved_by, a.approved_date, a.rejection_reason,
		       a.ks2_source_id, a.notes, a.created_by, a.created_date, a.updated_date,
		       COALESCE(s.name, '') as subcontract_name,
		       COALESCE(u.first_name || ' ' || u.last_name, '') as approved_name,
		       COALESCE(cu.first_name || ' ' || cu.last_name, '') as created_name
		FROM construction_act a
		LEFT JOIN construction_subcontract s ON s.id = a.subcontract_id
		LEFT JOIN users u ON u.id = a.approved_by
		LEFT JOIN users cu ON cu.id = a.created_by
		WHERE a.project_id = $1 AND a.tenant_id = $2
	`
	args := []interface{}{projectID, tenantID}
	argCount := 2

	if actType != "" {
		argCount++
		query += fmt.Sprintf(" AND a.act_type = $%d", argCount)
		args = append(args, actType)
	}
	if state != "" {
		argCount++
		query += fmt.Sprintf(" AND a.state = $%d", argCount)
		args = append(args, state)
	}
	if subcontractIDStr != "" {
		argCount++
		query += fmt.Sprintf(" AND a.subcontract_id = $%d", argCount)
		subID, _ := strconv.ParseInt(subcontractIDStr, 10, 64)
		args = append(args, subID)
	}

	query += " ORDER BY a.created_date DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list acts", "error", err)
		response.InternalError(c, "Failed to list acts")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var name, actTypeVal, stateVal, currency string
		var subcontractID sql.NullInt64
		var periodFrom, periodTo sql.NullTime
		var amountTotal float64
		var approvedBy uuid.NullUUID
		var approvedDate sql.NullTime
		var rejectionReason, notes sql.NullString
		var ks2SourceID sql.NullInt64
		var createdBy uuid.NullUUID
		var createdDate, updatedDate time.Time
		var subcontractName, approvedName, createdName string

		if err := rows.Scan(
			&id, &name, &actTypeVal, &projectIDVal, &subcontractID,
			&periodFrom, &periodTo, &amountTotal, &currency,
			&stateVal, &approvedBy, &approvedDate, &rejectionReason,
			&ks2SourceID, &notes, &createdBy, &createdDate, &updatedDate,
			&subcontractName, &approvedName, &createdName,
		); err != nil {
			h.log.Error("Failed to scan act", "error", err)
			continue
		}

		items = append(items, map[string]interface{}{
			"id":                id,
			"name":              name,
			"act_type":          actTypeVal,
			"project_id":        projectIDVal,
			"subcontract_id":    nullInt64Val(subcontractID),
			"subcontract_name":  subcontractName,
			"period_from":       nullTimeVal(periodFrom),
			"period_to":         nullTimeVal(periodTo),
			"amount_total":      amountTotal,
			"currency":          currency,
			"state":             stateVal,
			"approved_by":       nullUUIDVal(approvedBy),
			"approved_name":     approvedName,
			"approved_date":     nullTimeVal(approvedDate),
			"rejection_reason":  nullStringVal(rejectionReason),
			"ks2_source_id":     nullInt64Val(ks2SourceID),
			"notes":             nullStringVal(notes),
			"created_by":        nullUUIDVal(createdBy),
			"created_name":      createdName,
			"created_date":      createdDate,
			"updated_date":      updatedDate,
		})
	}

	response.Success(c, items)
}

// CreateConstructionAct creates a new act manually
func (h *Handler) CreateConstructionAct(c *gin.Context) {
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
		ActType       string `json:"act_type" binding:"required"`
		SubcontractID int64  `json:"subcontract_id"`
		PeriodFrom    string `json:"period_from"`
		PeriodTo      string `json:"period_to"`
		Notes         string `json:"notes"`
		Lines         []struct {
			WBSID          int64   `json:"wbs_id"`
			EstimateLineID int64   `json:"estimate_line_id"`
			Name           string  `json:"name"`
			UOM            string  `json:"uom"`
			Quantity       float64 `json:"quantity"`
			UnitRate       float64 `json:"unit_rate"`
		} `json:"lines"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input: act_type is required")
		return
	}

	validTypes := map[string]bool{
		"ks2": true, "ks3": true, "hidden_work": true, "acceptance": true, "defect": true,
	}
	if !validTypes[req.ActType] {
		response.BadRequest(c, "Invalid act_type")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Auto-generate name
	prefix := "ACT"
	switch req.ActType {
	case "ks2":
		prefix = "KS2"
	case "ks3":
		prefix = "KS3"
	}
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE project_id = $1 AND tenant_id = $2 AND act_type = $3`,
		projectID, tenantID, req.ActType).Scan(&count)
	name := fmt.Sprintf("%s-%03d", prefix, count+1)

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create act")
		return
	}
	defer tx.Rollback()

	var periodFrom, periodTo interface{}
	if req.PeriodFrom != "" {
		periodFrom = req.PeriodFrom
	}
	if req.PeriodTo != "" {
		periodTo = req.PeriodTo
	}

	var actID int64
	err = tx.QueryRow(`
		INSERT INTO construction_act (
			tenant_id, project_id, subcontract_id, name, act_type,
			period_from, period_to, amount_total, currency,
			state, notes, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 'UZS', 'draft', $8, $9, NOW(), NOW())
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(req.SubcontractID), name, req.ActType,
		periodFrom, periodTo, nullStringFromVal(req.Notes), userID,
	).Scan(&actID)
	if err != nil {
		h.log.Error("Failed to create act", "error", err)
		response.InternalError(c, "Failed to create act")
		return
	}

	// Insert lines and calculate total
	var totalAmount float64
	for i, line := range req.Lines {
		lineTotal := line.Quantity * line.UnitRate
		totalAmount += lineTotal
		_, err := tx.Exec(`
			INSERT INTO construction_act_line (
				act_id, wbs_id, estimate_line_id, name, uom,
				quantity, unit_rate, total_amount, sort_order
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, actID, nullInt64FromVal(line.WBSID), nullInt64FromVal(line.EstimateLineID),
			line.Name, line.UOM, line.Quantity, line.UnitRate, lineTotal, i+1,
		)
		if err != nil {
			h.log.Error("Failed to create act line", "error", err)
			response.InternalError(c, "Failed to create act line")
			return
		}
	}

	// Update total
	tx.Exec(`UPDATE construction_act SET amount_total = $1 WHERE id = $2`, totalAmount, actID)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit act", "error", err)
		response.InternalError(c, "Failed to create act")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "act",
		fmt.Sprintf("Akt yaratildi: %s", name), "Act", actID)

	response.Created(c, map[string]interface{}{
		"id":           actID,
		"name":         name,
		"amount_total": totalAmount,
		"message":      "Act created successfully",
	})
}

// GetConstructionAct returns a single act with its lines
func (h *Handler) GetConstructionAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	// Get act header
	var id, projectIDVal int64
	var name, actType, state, currency string
	var subcontractID sql.NullInt64
	var periodFrom, periodTo sql.NullTime
	var amountTotal float64
	var approvedBy uuid.NullUUID
	var approvedDate sql.NullTime
	var rejectionReason, notes sql.NullString
	var ks2SourceID sql.NullInt64
	var createdBy uuid.NullUUID
	var createdDate, updatedDate time.Time
	var subcontractName string

	err = h.db.QueryRow(`
		SELECT a.id, a.name, a.act_type, a.project_id, a.subcontract_id,
		       a.period_from, a.period_to, a.amount_total, a.currency,
		       a.state, a.approved_by, a.approved_date, a.rejection_reason,
		       a.ks2_source_id, a.notes, a.created_by, a.created_date, a.updated_date,
		       COALESCE(s.name, '') as subcontract_name
		FROM construction_act a
		LEFT JOIN construction_subcontract s ON s.id = a.subcontract_id
		WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(
		&id, &name, &actType, &projectIDVal, &subcontractID,
		&periodFrom, &periodTo, &amountTotal, &currency,
		&state, &approvedBy, &approvedDate, &rejectionReason,
		&ks2SourceID, &notes, &createdBy, &createdDate, &updatedDate,
		&subcontractName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Act not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get act", "error", err)
		response.InternalError(c, "Failed to get act")
		return
	}

	// Get lines
	lineRows, err := h.db.Query(`
		SELECT al.id, al.wbs_id, al.estimate_line_id, al.name, al.uom,
		       al.quantity, al.unit_rate, al.total_amount, al.sort_order,
		       COALESCE(w.code, '') as wbs_code,
		       COALESCE(w.name, '') as wbs_name
		FROM construction_act_line al
		LEFT JOIN construction_wbs w ON w.id = al.wbs_id
		WHERE al.act_id = $1
		ORDER BY al.sort_order, al.id
	`, actID)
	lines := []map[string]interface{}{}
	if err == nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var lineID int64
			var wbsID, estimateLineID sql.NullInt64
			var lineName, lineUOM, wbsCode, wbsName string
			var qty, unitRate, lineTotal float64
			var sortOrder int

			if lineRows.Scan(
				&lineID, &wbsID, &estimateLineID, &lineName, &lineUOM,
				&qty, &unitRate, &lineTotal, &sortOrder,
				&wbsCode, &wbsName,
			) == nil {
				lines = append(lines, map[string]interface{}{
					"id":               lineID,
					"wbs_id":           nullInt64Val(wbsID),
					"estimate_line_id": nullInt64Val(estimateLineID),
					"name":             lineName,
					"uom":              lineUOM,
					"quantity":         qty,
					"unit_rate":        unitRate,
					"total_amount":     lineTotal,
					"sort_order":       sortOrder,
					"wbs_code":         wbsCode,
					"wbs_name":         wbsName,
				})
			}
		}
	}

	response.Success(c, map[string]interface{}{
		"id":               id,
		"name":             name,
		"act_type":         actType,
		"project_id":       projectIDVal,
		"subcontract_id":   nullInt64Val(subcontractID),
		"subcontract_name": subcontractName,
		"period_from":      nullTimeVal(periodFrom),
		"period_to":        nullTimeVal(periodTo),
		"amount_total":     amountTotal,
		"currency":         currency,
		"state":            state,
		"approved_by":      nullUUIDVal(approvedBy),
		"approved_date":    nullTimeVal(approvedDate),
		"rejection_reason": nullStringVal(rejectionReason),
		"ks2_source_id":    nullInt64Val(ks2SourceID),
		"notes":            nullStringVal(notes),
		"created_date":     createdDate,
		"updated_date":     updatedDate,
		"lines":            lines,
	})
}

// AutoGenerateKS2 automatically creates a KS-2 act from daily logs
func (h *Handler) AutoGenerateKS2(c *gin.Context) {
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
		SubcontractID int64  `json:"subcontract_id" binding:"required"`
		PeriodFrom    string `json:"period_from" binding:"required"`
		PeriodTo      string `json:"period_to" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "subcontract_id, period_from, and period_to are required")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Get WBS IDs linked to this subcontract
	wbsRows, err := h.db.Query(
		`SELECT wbs_id FROM construction_subcontract_wbs WHERE subcontract_id = $1`, req.SubcontractID)
	if err != nil {
		h.log.Error("Failed to get subcontract WBS", "error", err)
		response.InternalError(c, "Failed to get subcontract WBS items")
		return
	}
	defer wbsRows.Close()

	wbsIDs := []int64{}
	for wbsRows.Next() {
		var wbsID int64
		if wbsRows.Scan(&wbsID) == nil {
			wbsIDs = append(wbsIDs, wbsID)
		}
	}

	if len(wbsIDs) == 0 {
		response.BadRequest(c, "Subcontract has no linked WBS items")
		return
	}

	// For each WBS, get daily log quantities in period and estimate line info
	type actLineData struct {
		WBSID          int64
		EstimateLineID int64
		Name           string
		UOM            string
		Quantity       float64
		UnitRate       float64
	}

	var lineDataList []actLineData

	for _, wbsID := range wbsIDs {
		// Get total done in period from daily logs
		var totalDone float64
		h.db.QueryRow(`
			SELECT COALESCE(SUM(quantity_done), 0)
			FROM construction_daily_log
			WHERE wbs_id = $1 AND tenant_id = $2 AND date >= $3 AND date <= $4
		`, wbsID, tenantID, req.PeriodFrom, req.PeriodTo).Scan(&totalDone)

		if totalDone <= 0 {
			continue
		}

		// Get estimate line for rate
		rows, err := h.db.Query(`
			SELECT el.id, el.name, el.uom, el.unit_rate
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE el.wbs_id = $1 AND e.is_current = true AND e.tenant_id = $2
			LIMIT 1
		`, wbsID, tenantID)
		if err != nil {
			continue
		}

		for rows.Next() {
			var elID int64
			var elName, elUOM string
			var unitRate float64
			if rows.Scan(&elID, &elName, &elUOM, &unitRate) == nil {
				lineDataList = append(lineDataList, actLineData{
					WBSID:          wbsID,
					EstimateLineID: elID,
					Name:           elName,
					UOM:            elUOM,
					Quantity:       totalDone,
					UnitRate:       unitRate,
				})
			}
		}
		rows.Close()
	}

	if len(lineDataList) == 0 {
		response.BadRequest(c, "No work found for this subcontract in the specified period")
		return
	}

	// Auto-generate name
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE project_id = $1 AND tenant_id = $2 AND act_type = 'ks2'`,
		projectID, tenantID).Scan(&count)
	name := fmt.Sprintf("KS2-%03d", count+1)

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create KS-2")
		return
	}
	defer tx.Rollback()

	var actID int64
	err = tx.QueryRow(`
		INSERT INTO construction_act (
			tenant_id, project_id, subcontract_id, name, act_type,
			period_from, period_to, amount_total, currency,
			state, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, 'ks2', $5, $6, 0, 'UZS', 'draft', $7, NOW(), NOW())
		RETURNING id
	`, tenantID, projectID, req.SubcontractID, name, req.PeriodFrom, req.PeriodTo, userID,
	).Scan(&actID)
	if err != nil {
		h.log.Error("Failed to create KS-2 act", "error", err)
		response.InternalError(c, "Failed to create KS-2")
		return
	}

	var totalAmount float64
	for i, ld := range lineDataList {
		lineTotal := ld.Quantity * ld.UnitRate
		totalAmount += lineTotal
		tx.Exec(`
			INSERT INTO construction_act_line (
				act_id, wbs_id, estimate_line_id, name, uom,
				quantity, unit_rate, total_amount, sort_order
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, actID, ld.WBSID, ld.EstimateLineID, ld.Name, ld.UOM,
			ld.Quantity, ld.UnitRate, lineTotal, i+1,
		)
	}

	tx.Exec(`UPDATE construction_act SET amount_total = $1 WHERE id = $2`, totalAmount, actID)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit KS-2", "error", err)
		response.InternalError(c, "Failed to create KS-2")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "act",
		fmt.Sprintf("KS-2 avtomatik yaratildi: %s (%.0f so'm)", name, totalAmount), "Act", actID)

	response.Created(c, map[string]interface{}{
		"id":           actID,
		"name":         name,
		"amount_total": totalAmount,
		"lines_count":  len(lineDataList),
		"message":      "KS-2 act auto-generated successfully",
	})
}

// ApproveConstructionAct approves an act
func (h *Handler) ApproveConstructionAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var projectID int64
	var currentState string
	err = h.db.QueryRow(`SELECT project_id, state FROM construction_act WHERE id = $1 AND tenant_id = $2`,
		actID, tenantID).Scan(&projectID, &currentState)
	if err != nil {
		response.NotFound(c, "Act not found")
		return
	}

	if currentState != "draft" && currentState != "submitted" {
		response.BadRequest(c, "Only draft or submitted acts can be approved")
		return
	}

	userID, _ := middleware.GetUserID(c)

	_, err = h.db.Exec(`
		UPDATE construction_act SET state = 'approved', approved_by = $1, approved_date = NOW(), updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, userID, actID, tenantID)
	if err != nil {
		h.log.Error("Failed to approve act", "error", err)
		response.InternalError(c, "Failed to approve act")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "act",
		"Akt tasdiqlandi", "Act", actID)

	response.Success(c, map[string]interface{}{
		"id":      actID,
		"state":   "approved",
		"message": "Act approved successfully",
	})
}

// RejectConstructionAct rejects an act with a reason
func (h *Handler) RejectConstructionAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		RejectionReason string `json:"rejection_reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Rejection reason is required")
		return
	}

	var projectID int64
	var currentState string
	err = h.db.QueryRow(`SELECT project_id, state FROM construction_act WHERE id = $1 AND tenant_id = $2`,
		actID, tenantID).Scan(&projectID, &currentState)
	if err != nil {
		response.NotFound(c, "Act not found")
		return
	}

	if currentState != "draft" && currentState != "submitted" {
		response.BadRequest(c, "Only draft or submitted acts can be rejected")
		return
	}

	userID, _ := middleware.GetUserID(c)

	_, err = h.db.Exec(`
		UPDATE construction_act SET state = 'rejected', rejection_reason = $1, updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, req.RejectionReason, actID, tenantID)
	if err != nil {
		h.log.Error("Failed to reject act", "error", err)
		response.InternalError(c, "Failed to reject act")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "act",
		"Akt rad etildi", "Act", actID)

	response.Success(c, map[string]interface{}{
		"id":      actID,
		"state":   "rejected",
		"message": "Act rejected",
	})
}

// GenerateKS3FromKS2 creates a KS-3 act from an approved KS-2
func (h *Handler) GenerateKS3FromKS2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	ks2ID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var projectID, subcontractID int64
	var ks2State, ks2Type string
	var periodFrom, periodTo sql.NullTime
	var ks2Amount float64

	err = h.db.QueryRow(`
		SELECT project_id, COALESCE(subcontract_id, 0), state, act_type, period_from, period_to, amount_total
		FROM construction_act WHERE id = $1 AND tenant_id = $2
	`, ks2ID, tenantID).Scan(&projectID, &subcontractID, &ks2State, &ks2Type, &periodFrom, &periodTo, &ks2Amount)
	if err != nil {
		response.NotFound(c, "KS-2 act not found")
		return
	}

	if ks2Type != "ks2" {
		response.BadRequest(c, "Source act must be KS-2 type")
		return
	}
	if ks2State != "approved" {
		response.BadRequest(c, "KS-2 must be approved before generating KS-3")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE project_id = $1 AND tenant_id = $2 AND act_type = 'ks3'`,
		projectID, tenantID).Scan(&count)
	name := fmt.Sprintf("KS3-%03d", count+1)

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create KS-3")
		return
	}
	defer tx.Rollback()

	var ks3ID int64
	err = tx.QueryRow(`
		INSERT INTO construction_act (
			tenant_id, project_id, subcontract_id, name, act_type,
			period_from, period_to, amount_total, currency,
			state, ks2_source_id, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, 'ks3', $5, $6, $7, 'UZS', 'draft', $8, $9, NOW(), NOW())
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(subcontractID), name,
		periodFrom, periodTo, ks2Amount, ks2ID, userID,
	).Scan(&ks3ID)
	if err != nil {
		h.log.Error("Failed to create KS-3", "error", err)
		response.InternalError(c, "Failed to create KS-3")
		return
	}

	// Copy lines from KS-2
	_, err = tx.Exec(`
		INSERT INTO construction_act_line (act_id, wbs_id, estimate_line_id, name, uom, quantity, unit_rate, total_amount, sort_order)
		SELECT $1, wbs_id, estimate_line_id, name, uom, quantity, unit_rate, total_amount, sort_order
		FROM construction_act_line WHERE act_id = $2
	`, ks3ID, ks2ID)
	if err != nil {
		h.log.Error("Failed to copy KS-2 lines to KS-3", "error", err)
		response.InternalError(c, "Failed to create KS-3 lines")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit KS-3", "error", err)
		response.InternalError(c, "Failed to create KS-3")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "act",
		fmt.Sprintf("KS-3 yaratildi: %s (KS-2 dan)", name), "Act", ks3ID)

	response.Created(c, map[string]interface{}{
		"id":           ks3ID,
		"name":         name,
		"amount_total": ks2Amount,
		"message":      "KS-3 generated from KS-2 successfully",
	})
}

// DeleteConstructionAct deletes a draft act
func (h *Handler) DeleteConstructionAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var projectID int64
	var state string
	err = h.db.QueryRow(`SELECT project_id, state FROM construction_act WHERE id = $1 AND tenant_id = $2`,
		actID, tenantID).Scan(&projectID, &state)
	if err != nil {
		response.NotFound(c, "Act not found")
		return
	}

	if state == "approved" {
		response.BadRequest(c, "Cannot delete approved acts")
		return
	}

	h.db.Exec(`DELETE FROM construction_act_line WHERE act_id = $1`, actID)
	_, err = h.db.Exec(`DELETE FROM construction_act WHERE id = $1 AND tenant_id = $2`, actID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete act", "error", err)
		response.InternalError(c, "Failed to delete act")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "act",
		"Akt o'chirildi", "Act", actID)

	response.Success(c, map[string]interface{}{
		"message": "Act deleted successfully",
	})
}
