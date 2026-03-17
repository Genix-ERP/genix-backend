package handler

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// FORMA 19 — Material Consumption Tracking Handlers
// =====================================================

// ListForma19 returns all Forma 19 acts for a project
func (h *Handler) ListForma19(c *gin.Context) {
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
		SELECT a.id, a.name, a.period_from, a.period_to, a.state,
		       a.amount_total, a.created_date,
		       (SELECT COUNT(*) FROM construction_act_line WHERE act_id = a.id) as line_count
		FROM construction_act a
		WHERE a.act_type = 'hidden_work' AND a.project_id = $1 AND a.tenant_id = $2
	`
	args := []interface{}{projectID, tenantID}
	argCount := 2

	state := c.Query("state")
	if state != "" {
		argCount++
		query += fmt.Sprintf(" AND a.state = $%d", argCount)
		args = append(args, state)
	}

	buildingIDStr := c.Query("building_id")
	if buildingIDStr != "" {
		buildingID, parseErr := strconv.ParseInt(buildingIDStr, 10, 64)
		if parseErr == nil {
			argCount++
			query += fmt.Sprintf(` AND EXISTS (
				SELECT 1 FROM construction_estimate e
				WHERE e.project_id = a.project_id AND e.tenant_id = a.tenant_id
				  AND e.building_id = $%d AND e.source_type = 'resurs'
			)`, argCount)
			args = append(args, buildingID)
		}
	}

	query += " ORDER BY a.created_date DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list Forma 19 acts", "error", err)
		response.InternalError(c, "Failed to list Forma 19 acts")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var name string
		var periodFrom, periodTo sql.NullTime
		var state string
		var amountTotal float64
		var createdDate time.Time
		var lineCount int

		if err := rows.Scan(&id, &name, &periodFrom, &periodTo, &state,
			&amountTotal, &createdDate, &lineCount); err != nil {
			h.log.Error("Failed to scan Forma 19 act", "error", err)
			continue
		}

		item := map[string]interface{}{
			"id":           id,
			"name":         name,
			"state":        state,
			"amount_total": amountTotal,
			"created_date": createdDate,
			"line_count":   lineCount,
		}
		if periodFrom.Valid {
			item["period_from"] = periodFrom.Time.Format("2006-01-02")
		} else {
			item["period_from"] = nil
		}
		if periodTo.Valid {
			item["period_to"] = periodTo.Time.Format("2006-01-02")
		} else {
			item["period_to"] = nil
		}

		items = append(items, item)
	}

	response.Success(c, items)
}

// CreateForma19 creates a new Forma 19 act and populates base rows from estimate
func (h *Handler) CreateForma19(c *gin.Context) {
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
		Name       string `json:"name"`
		BuildingID int64  `json:"building_id" binding:"required"`
		PeriodFrom string `json:"period_from" binding:"required"`
		PeriodTo   string `json:"period_to" binding:"required"`
		Notes      string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input: building_id, period_from, and period_to are required")
		return
	}

	periodFrom, err := time.Parse("2006-01-02", req.PeriodFrom)
	if err != nil {
		response.BadRequest(c, "Invalid period_from date format, expected YYYY-MM-DD")
		return
	}

	periodTo, err := time.Parse("2006-01-02", req.PeriodTo)
	if err != nil {
		response.BadRequest(c, "Invalid period_to date format, expected YYYY-MM-DD")
		return
	}

	if periodTo.Before(periodFrom) {
		response.BadRequest(c, "period_to must be after period_from")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Auto-generate name if empty
	name := req.Name
	if name == "" {
		name = fmt.Sprintf("F19-%02d-%d", periodFrom.Month(), periodFrom.Year())
	}

	// Insert the act
	var actID int64
	err = h.db.QueryRow(`
		INSERT INTO construction_act (
			tenant_id, project_id, name, act_type, period_from, period_to,
			amount_total, currency, state, notes, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, 'hidden_work', $4, $5, 0, 'UZS', 'draft', $6, $7, NOW(), NOW())
		RETURNING id
	`, tenantID, projectID, name, periodFrom, periodTo,
		nullStringFromVal(req.Notes), userID,
	).Scan(&actID)
	if err != nil {
		h.log.Error("Failed to create Forma 19 act", "error", err)
		response.InternalError(c, "Failed to create Forma 19 act")
		return
	}

	// Auto-populate base rows from estimate lines
	estimateRows, err := h.db.Query(`
		SELECT el.id, el.name, el.uom, el.material_rate, el.quantity
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		WHERE e.project_id = $1 AND e.building_id = $2 AND e.tenant_id = $3
		  AND e.source_type = 'resurs' AND el.resource_type = 'material'
		ORDER BY el.sort_order, el.id
	`, projectID, req.BuildingID, tenantID)
	if err != nil {
		h.log.Error("Failed to query estimate lines for Forma 19", "error", err)
		// Act is created but lines failed — return act with 0 lines
		response.Created(c, map[string]interface{}{
			"id":         actID,
			"name":       name,
			"line_count": 0,
			"warning":    "Act created but failed to populate lines from estimate",
		})
		return
	}
	defer estimateRows.Close()

	lineCount := 0
	sortOrder := 0
	for estimateRows.Next() {
		var elID int64
		var elName, elUOM string
		var materialRate, quantity float64

		if err := estimateRows.Scan(&elID, &elName, &elUOM, &materialRate, &quantity); err != nil {
			h.log.Error("Failed to scan estimate line for Forma 19", "error", err)
			continue
		}

		sortOrder++
		_, insertErr := h.db.Exec(`
			INSERT INTO construction_act_line (
				act_id, name, uom, quantity, unit_rate, total_amount,
				sort_order, row_type, boshi, keldi, sarf, qoldi, cost_price,
				created_date
			) VALUES ($1, $2, $3, $4, $5, 0, $6, 'base', 0, 0, 0, 0, $7, NOW())
		`, actID, elName, elUOM, quantity, materialRate, sortOrder, materialRate)
		if insertErr != nil {
			h.log.Error("Failed to insert Forma 19 base line", "error", insertErr)
			continue
		}
		lineCount++
	}

	h.logConstructionActivity(tenantID, projectID, userID, "forma19",
		fmt.Sprintf("Forma 19 yaratildi: %s (%d qator)", name, lineCount),
		"Forma19", actID)

	response.Created(c, map[string]interface{}{
		"id":         actID,
		"name":       name,
		"line_count": lineCount,
	})
}

// GetForma19Detail returns a Forma 19 act with all its lines and summary
func (h *Handler) GetForma19Detail(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("actId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid act ID")
		return
	}

	// Fetch act
	var id, projectID int64
	var name, state, currency string
	var periodFrom, periodTo sql.NullTime
	var amountTotal float64
	var notes sql.NullString
	var approvedBy uuid.NullUUID
	var approvedDate sql.NullTime
	var createdDate, updatedDate time.Time

	err = h.db.QueryRow(`
		SELECT a.id, a.project_id, a.name, a.state, a.currency,
		       a.period_from, a.period_to, a.amount_total, a.notes,
		       a.approved_by, a.approved_date, a.created_date, a.updated_date
		FROM construction_act a
		WHERE a.id = $1 AND a.tenant_id = $2 AND a.act_type = 'hidden_work'
	`, actID, tenantID).Scan(
		&id, &projectID, &name, &state, &currency,
		&periodFrom, &periodTo, &amountTotal, &notes,
		&approvedBy, &approvedDate, &createdDate, &updatedDate,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Forma 19 act not found")
		} else {
			h.log.Error("Failed to get Forma 19 act", "error", err)
			response.InternalError(c, "Failed to get Forma 19 act")
		}
		return
	}

	act := map[string]interface{}{
		"id":           id,
		"project_id":   projectID,
		"name":         name,
		"state":        state,
		"currency":     currency,
		"amount_total": amountTotal,
		"notes":        nullStringVal(notes),
		"created_date": createdDate,
		"updated_date": updatedDate,
	}
	if periodFrom.Valid {
		act["period_from"] = periodFrom.Time.Format("2006-01-02")
	} else {
		act["period_from"] = nil
	}
	if periodTo.Valid {
		act["period_to"] = periodTo.Time.Format("2006-01-02")
	} else {
		act["period_to"] = nil
	}
	if approvedBy.Valid {
		act["approved_by"] = approvedBy.UUID
	} else {
		act["approved_by"] = nil
	}
	if approvedDate.Valid {
		act["approved_date"] = approvedDate.Time
	} else {
		act["approved_date"] = nil
	}

	// Fetch lines
	lineRows, err := h.db.Query(`
		SELECT id, name, uom, row_type, boshi, keldi, sarf, qoldi, cost_price,
		       COALESCE(change_reason, '') as change_reason,
		       COALESCE(change_note, '') as change_note,
		       sort_order
		FROM construction_act_line
		WHERE act_id = $1
		ORDER BY row_type ASC, sort_order ASC
	`, actID)
	if err != nil {
		h.log.Error("Failed to get Forma 19 lines", "error", err)
		response.InternalError(c, "Failed to get Forma 19 lines")
		return
	}
	defer lineRows.Close()

	lines := []map[string]interface{}{}
	var smetaTotal, changeTotal float64
	for lineRows.Next() {
		var lineID int64
		var lineName, lineUOM, rowType, changeReason, changeNote string
		var boshi, keldi, sarf, qoldi, costPrice float64
		var lineSortOrder int

		if err := lineRows.Scan(&lineID, &lineName, &lineUOM, &rowType,
			&boshi, &keldi, &sarf, &qoldi, &costPrice,
			&changeReason, &changeNote, &lineSortOrder); err != nil {
			h.log.Error("Failed to scan Forma 19 line", "error", err)
			continue
		}

		summa := math.Round(sarf*costPrice*100) / 100

		line := map[string]interface{}{
			"id":            lineID,
			"name":          lineName,
			"uom":           lineUOM,
			"row_type":      rowType,
			"boshi":         boshi,
			"keldi":         keldi,
			"sarf":          sarf,
			"qoldi":         qoldi,
			"cost_price":    costPrice,
			"summa":         summa,
			"change_reason": changeReason,
			"change_note":   changeNote,
			"sort_order":    lineSortOrder,
		}
		lines = append(lines, line)

		if rowType == "base" {
			smetaTotal += summa
		} else {
			changeTotal += summa
		}
	}

	smetaTotal = math.Round(smetaTotal*100) / 100
	changeTotal = math.Round(changeTotal*100) / 100
	total := math.Round((smetaTotal+changeTotal)*100) / 100
	diff := math.Round((total-smetaTotal)*100) / 100

	act["lines"] = lines
	act["summary"] = map[string]interface{}{
		"smeta_total":  smetaTotal,
		"change_total": changeTotal,
		"total":        total,
		"diff":         diff,
	}

	response.Success(c, act)
}

// AddF19ChangeRow adds a change row to a Forma 19 act
func (h *Handler) AddF19ChangeRow(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("actId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid act ID")
		return
	}

	// Verify act exists, is hidden_work, and is draft
	var projectID int64
	var actType, currentState string
	err = h.db.QueryRow(`
		SELECT project_id, act_type, state FROM construction_act WHERE id = $1 AND tenant_id = $2
	`, actID, tenantID).Scan(&projectID, &actType, &currentState)
	if err != nil {
		response.NotFound(c, "Forma 19 act not found")
		return
	}
	if actType != "hidden_work" {
		response.BadRequest(c, "Act is not a Forma 19 (hidden_work) type")
		return
	}
	if currentState != "draft" {
		response.BadRequest(c, "Only draft acts can be modified")
		return
	}

	var req struct {
		MaterialName string  `json:"material_name" binding:"required"`
		Unit         string  `json:"unit" binding:"required"`
		Keldi        float64 `json:"keldi"`
		Sarf         float64 `json:"sarf"`
		CostPrice    float64 `json:"cost_price"`
		ChangeReason string  `json:"change_reason" binding:"required"`
		ChangeNote   string  `json:"change_note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input: material_name, unit, and change_reason are required")
		return
	}

	qoldi := req.Keldi - req.Sarf
	summa := math.Round(req.Sarf*req.CostPrice*100) / 100

	// Get next sort_order
	var maxSortOrder int
	_ = h.db.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) FROM construction_act_line WHERE act_id = $1`, actID).Scan(&maxSortOrder)
	nextSortOrder := maxSortOrder + 1

	var lineID int64
	err = h.db.QueryRow(`
		INSERT INTO construction_act_line (
			act_id, name, uom, quantity, unit_rate, total_amount,
			sort_order, row_type, boshi, keldi, sarf, qoldi, cost_price,
			change_reason, change_note, created_date
		) VALUES ($1, $2, $3, 0, $4, $5, $6, 'change', 0, $7, $8, $9, $10, $11, $12, NOW())
		RETURNING id
	`, actID, req.MaterialName, req.Unit, req.CostPrice, summa,
		nextSortOrder, req.Keldi, req.Sarf, qoldi, req.CostPrice,
		req.ChangeReason, nullStringFromVal(req.ChangeNote),
	).Scan(&lineID)
	if err != nil {
		h.log.Error("Failed to add Forma 19 change row", "error", err)
		response.InternalError(c, "Failed to add change row")
		return
	}

	// Recalculate act amount_total
	var newTotal float64
	err = h.db.QueryRow(`
		SELECT COALESCE(SUM(sarf * cost_price), 0) FROM construction_act_line WHERE act_id = $1
	`, actID).Scan(&newTotal)
	if err != nil {
		h.log.Error("Failed to recalculate Forma 19 total", "error", err)
	} else {
		newTotal = math.Round(newTotal*100) / 100
		_, _ = h.db.Exec(`UPDATE construction_act SET amount_total = $1, updated_date = NOW() WHERE id = $2`, newTotal, actID)
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "forma19",
		fmt.Sprintf("Forma 19 o'zgartirish qatori qo'shildi: %s", req.MaterialName),
		"Forma19", actID)

	response.Created(c, map[string]interface{}{
		"id":            lineID,
		"name":          req.MaterialName,
		"uom":           req.Unit,
		"row_type":      "change",
		"keldi":         req.Keldi,
		"sarf":          req.Sarf,
		"qoldi":         qoldi,
		"cost_price":    req.CostPrice,
		"summa":         summa,
		"change_reason": req.ChangeReason,
		"change_note":   req.ChangeNote,
		"sort_order":    nextSortOrder,
	})
}

// UpdateF19Row updates a row in a Forma 19 act
func (h *Handler) UpdateF19Row(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("actId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid act ID")
		return
	}

	rowID, err := strconv.ParseInt(c.Param("rowId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid row ID")
		return
	}

	// Verify act is draft
	var projectID int64
	var currentState string
	err = h.db.QueryRow(`
		SELECT project_id, state FROM construction_act WHERE id = $1 AND tenant_id = $2 AND act_type = 'hidden_work'
	`, actID, tenantID).Scan(&projectID, &currentState)
	if err != nil {
		response.NotFound(c, "Forma 19 act not found")
		return
	}
	if currentState != "draft" {
		response.BadRequest(c, "Only draft acts can be modified")
		return
	}

	// Fetch current line
	var lineName, lineUOM, rowType string
	var boshi, keldi, sarf, qoldi, costPrice float64
	var changeReason, changeNote sql.NullString
	err = h.db.QueryRow(`
		SELECT name, uom, row_type, boshi, keldi, sarf, qoldi, cost_price, change_reason, change_note
		FROM construction_act_line WHERE id = $1 AND act_id = $2
	`, rowID, actID).Scan(&lineName, &lineUOM, &rowType, &boshi, &keldi, &sarf, &qoldi, &costPrice, &changeReason, &changeNote)
	if err != nil {
		response.NotFound(c, "Line not found")
		return
	}

	var req struct {
		Sarf         *float64 `json:"sarf"`
		Keldi        *float64 `json:"keldi"`
		Boshi        *float64 `json:"boshi"`
		CostPrice    *float64 `json:"cost_price"`
		MaterialName *string  `json:"material_name"`
		Unit         *string  `json:"unit"`
		ChangeReason *string  `json:"change_reason"`
		ChangeNote   *string  `json:"change_note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Apply updates based on row_type
	if rowType == "base" {
		// Base rows: allow sarf, boshi, keldi, cost_price
		if req.Sarf != nil {
			sarf = *req.Sarf
		}
		if req.Boshi != nil {
			boshi = *req.Boshi
		}
		if req.Keldi != nil {
			keldi = *req.Keldi
		}
		if req.CostPrice != nil {
			costPrice = *req.CostPrice
		}
	} else {
		// Change rows: allow all fields
		if req.Sarf != nil {
			sarf = *req.Sarf
		}
		if req.Boshi != nil {
			boshi = *req.Boshi
		}
		if req.Keldi != nil {
			keldi = *req.Keldi
		}
		if req.CostPrice != nil {
			costPrice = *req.CostPrice
		}
		if req.MaterialName != nil {
			lineName = *req.MaterialName
		}
		if req.Unit != nil {
			lineUOM = *req.Unit
		}
		if req.ChangeReason != nil {
			changeReason = sql.NullString{String: *req.ChangeReason, Valid: *req.ChangeReason != ""}
		}
		if req.ChangeNote != nil {
			changeNote = sql.NullString{String: *req.ChangeNote, Valid: *req.ChangeNote != ""}
		}
	}

	// Recalculate derived fields
	qoldi = boshi + keldi - sarf
	totalAmount := math.Round(sarf*costPrice*100) / 100

	_, err = h.db.Exec(`
		UPDATE construction_act_line
		SET name = $1, uom = $2, boshi = $3, keldi = $4, sarf = $5, qoldi = $6,
		    cost_price = $7, total_amount = $8, change_reason = $9, change_note = $10
		WHERE id = $11 AND act_id = $12
	`, lineName, lineUOM, boshi, keldi, sarf, qoldi, costPrice, totalAmount,
		changeReason, changeNote, rowID, actID)
	if err != nil {
		h.log.Error("Failed to update Forma 19 row", "error", err)
		response.InternalError(c, "Failed to update row")
		return
	}

	// Recalculate act amount_total
	var newTotal float64
	err = h.db.QueryRow(`
		SELECT COALESCE(SUM(sarf * cost_price), 0) FROM construction_act_line WHERE act_id = $1
	`, actID).Scan(&newTotal)
	if err != nil {
		h.log.Error("Failed to recalculate Forma 19 total", "error", err)
	} else {
		newTotal = math.Round(newTotal*100) / 100
		_, _ = h.db.Exec(`UPDATE construction_act SET amount_total = $1, updated_date = NOW() WHERE id = $2`, newTotal, actID)
	}

	summa := math.Round(sarf*costPrice*100) / 100

	response.Success(c, map[string]interface{}{
		"id":            rowID,
		"name":          lineName,
		"uom":           lineUOM,
		"row_type":      rowType,
		"boshi":         boshi,
		"keldi":         keldi,
		"sarf":          sarf,
		"qoldi":         qoldi,
		"cost_price":    costPrice,
		"summa":         summa,
		"change_reason": nullStringVal(changeReason),
		"change_note":   nullStringVal(changeNote),
	})
}

// ApproveForma19 approves a Forma 19 act and records material usage
func (h *Handler) ApproveForma19(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("actId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid act ID")
		return
	}

	var projectID int64
	var currentState string
	err = h.db.QueryRow(`
		SELECT project_id, state FROM construction_act WHERE id = $1 AND tenant_id = $2 AND act_type = 'hidden_work'
	`, actID, tenantID).Scan(&projectID, &currentState)
	if err != nil {
		response.NotFound(c, "Forma 19 act not found")
		return
	}
	if currentState != "draft" {
		response.BadRequest(c, "Only draft acts can be approved")
		return
	}

	// Verify at least one line has sarf > 0
	var sarfCount int
	err = h.db.QueryRow(`
		SELECT COUNT(*) FROM construction_act_line WHERE act_id = $1 AND sarf > 0
	`, actID).Scan(&sarfCount)
	if err != nil {
		h.log.Error("Failed to check Forma 19 lines", "error", err)
		response.InternalError(c, "Failed to verify act lines")
		return
	}
	if sarfCount == 0 {
		response.BadRequest(c, "At least one line must have sarf > 0 to approve")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Record material usage for lines with sarf > 0
	usageRows, err := h.db.Query(`
		SELECT id, name, uom, sarf, cost_price, row_type, COALESCE(change_reason, '')
		FROM construction_act_line WHERE act_id = $1 AND sarf > 0
	`, actID)
	if err != nil {
		h.log.Error("Failed to query Forma 19 lines for approval", "error", err)
		response.InternalError(c, "Failed to process approval")
		return
	}
	defer usageRows.Close()

	for usageRows.Next() {
		var lineID int64
		var matName, matUOM, rowType, changeReason string
		var matSarf, matCostPrice float64

		if err := usageRows.Scan(&lineID, &matName, &matUOM, &matSarf, &matCostPrice, &rowType, &changeReason); err != nil {
			h.log.Error("Failed to scan Forma 19 line for usage", "error", err)
			continue
		}

		_, insertErr := h.db.Exec(`
			INSERT INTO construction_material_usage (
				tenant_id, project_id, product_name, uom, quantity_used,
				usage_date, recorded_by, notes, created_date, updated_date
			) VALUES ($1, $2, $3, $4, $5, NOW(), $6, $7, NOW(), NOW())
		`, tenantID, projectID, matName, matUOM, matSarf, userID,
			fmt.Sprintf("Forma 19 #%d dan", actID))
		if insertErr != nil {
			h.log.Error("Failed to record material usage from Forma 19", "error", insertErr)
		}
	}

	// Create provodka (journal entries): Dt 2930 / Kt 1310
	go func() {
		now := time.Now()

		// Find MISC or GEN journal
		var journalID uuid.UUID
		var nextNumber int
		err := h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('MISC','GENERAL','GEN') AND deleted_at IS NULL
			ORDER BY CASE WHEN code='MISC' THEN 0 ELSE 1 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNumber)
		if err != nil {
			h.log.Error("Failed to find journal for Forma 19 provodka", "error", err)
			return
		}

		// Find accounts by code: 2930 (Construction expense) and 1310 (Raw materials)
		var debitAccountID, creditAccountID uuid.UUID
		err = h.db.QueryRow(`SELECT id FROM accounts WHERE tenant_id = $1 AND code = '2930' AND deleted_at IS NULL LIMIT 1`, tenantID).Scan(&debitAccountID)
		if err != nil {
			h.log.Error("Failed to find account 2930 for Forma 19 provodka", "error", err)
			return
		}
		err = h.db.QueryRow(`SELECT id FROM accounts WHERE tenant_id = $1 AND code = '1310' AND deleted_at IS NULL LIMIT 1`, tenantID).Scan(&creditAccountID)
		if err != nil {
			h.log.Error("Failed to find account 1310 for Forma 19 provodka", "error", err)
			return
		}

		// Calculate totals by row_type
		var baseTotal, changeTotal float64
		h.db.QueryRow(`SELECT COALESCE(SUM(sarf * cost_price), 0) FROM construction_act_line WHERE act_id = $1 AND row_type = 'base' AND sarf > 0`, actID).Scan(&baseTotal)
		h.db.QueryRow(`SELECT COALESCE(SUM(sarf * cost_price), 0) FROM construction_act_line WHERE act_id = $1 AND row_type = 'change' AND sarf > 0`, actID).Scan(&changeTotal)

		// Get act name
		var actName string
		h.db.QueryRow(`SELECT name FROM construction_act WHERE id = $1`, actID).Scan(&actName)

		// Create journal entries per TZ: separate for base and change rows
		createEntry := func(amount float64, description string) {
			if amount <= 0 {
				return
			}
			amount = math.Round(amount*100) / 100
			entryNumber := fmt.Sprintf("F19-%06d", nextNumber)
			journalEntryID := uuid.New()

			_, jeErr := h.db.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 'construction', $8, 1.0, $9, $9, 'posted', $10, $11, $11)`,
				journalEntryID, tenantID, journalID, entryNumber, now, actName, description,
				fmt.Sprintf("%d", actID), amount, userID, now,
			)
			if jeErr != nil {
				h.log.Error("Failed to create Forma 19 journal entry", "error", jeErr)
				return
			}

			// Dt 2930 Construction expense
			h.db.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1, $2, 1, $3, $4, $5, 0, 1.0, $6)`,
				uuid.New(), journalEntryID, debitAccountID, description, amount, now)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", amount, now, debitAccountID)

			// Kt 1310 Raw materials
			h.db.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1, $2, 2, $3, $4, 0, $5, 1.0, $6)`,
				uuid.New(), journalEntryID, creditAccountID, description, amount, now)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", amount, now, creditAccountID)

			h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
			nextNumber++
		}

		// Base rows provodka
		createEntry(baseTotal, fmt.Sprintf("Forma 19 asos: %s", actName))

		// Change rows provodka (separate entry per TZ)
		if changeTotal > 0 {
			// Get change reasons summary
			rows, _ := h.db.Query(`
				SELECT COALESCE(change_reason, ''), ROUND(SUM(sarf * cost_price)::numeric, 2)
				FROM construction_act_line WHERE act_id = $1 AND row_type = 'change' AND sarf > 0
				GROUP BY change_reason`, actID)
			if rows != nil {
				defer rows.Close()
				for rows.Next() {
					var reason string
					var reasonTotal float64
					rows.Scan(&reason, &reasonTotal)
					createEntry(reasonTotal, fmt.Sprintf("Forma 19 o'zgarish: %s (%s)", actName, reason))
				}
			}
		}
	}()

	// Recalculate final amount_total
	var finalTotal float64
	err = h.db.QueryRow(`
		SELECT COALESCE(SUM(sarf * cost_price), 0) FROM construction_act_line WHERE act_id = $1
	`, actID).Scan(&finalTotal)
	if err != nil {
		h.log.Error("Failed to recalculate final Forma 19 total", "error", err)
		finalTotal = 0
	}
	finalTotal = math.Round(finalTotal*100) / 100

	// Update act to approved
	_, err = h.db.Exec(`
		UPDATE construction_act SET state = 'approved', approved_by = $1, approved_date = NOW(),
		       amount_total = $2, updated_date = NOW()
		WHERE id = $3 AND tenant_id = $4
	`, userID, finalTotal, actID, tenantID)
	if err != nil {
		h.log.Error("Failed to approve Forma 19 act", "error", err)
		response.InternalError(c, "Failed to approve Forma 19 act")
		return
	}

	// Update all lines: set approved_by and approved_at
	_, err = h.db.Exec(`
		UPDATE construction_act_line SET approved_by = $1, approved_at = NOW() WHERE act_id = $2
	`, userID, actID)
	if err != nil {
		h.log.Error("Failed to update Forma 19 line approvals", "error", err)
	}

	h.logConstructionActivity(tenantID, projectID, userID, "forma19",
		"Forma 19 tasdiqlandi", "Forma19", actID)

	response.Success(c, map[string]interface{}{
		"id":           actID,
		"state":        "approved",
		"amount_total": finalTotal,
		"message":      "Forma 19 approved successfully",
	})
}

// DeleteForma19 deletes a draft Forma 19 act and all its lines
func (h *Handler) DeleteForma19(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("actId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid act ID")
		return
	}

	var projectID int64
	var currentState string
	err = h.db.QueryRow(`
		SELECT project_id, state FROM construction_act WHERE id = $1 AND tenant_id = $2 AND act_type = 'hidden_work'
	`, actID, tenantID).Scan(&projectID, &currentState)
	if err != nil {
		response.NotFound(c, "Forma 19 act not found")
		return
	}
	if currentState != "draft" {
		response.BadRequest(c, "Only draft acts can be deleted")
		return
	}

	// Delete all lines first (cascade would handle this, but explicit is clearer)
	_, err = h.db.Exec(`DELETE FROM construction_act_line WHERE act_id = $1`, actID)
	if err != nil {
		h.log.Error("Failed to delete Forma 19 lines", "error", err)
		response.InternalError(c, "Failed to delete Forma 19 lines")
		return
	}

	// Delete the act
	_, err = h.db.Exec(`DELETE FROM construction_act WHERE id = $1 AND tenant_id = $2`, actID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete Forma 19 act", "error", err)
		response.InternalError(c, "Failed to delete Forma 19 act")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "forma19",
		"Forma 19 o'chirildi", "Forma19", actID)

	response.Success(c, map[string]interface{}{
		"message": "Forma 19 deleted successfully",
	})
}

// Ensure unused imports are satisfied
var (
	_ = http.StatusOK
)
