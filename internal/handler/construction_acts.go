package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION ACTS (Forma 2 / Forma 3 / Forma 19) HANDLERS
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
	buildingIDStr := c.Query("building_id") // new: per-building filter

	query := `
		SELECT a.id, a.name, a.act_type, a.project_id, a.subcontract_id,
		       a.period_from, a.period_to, a.amount_total, a.currency,
		       a.state, a.approved_by, a.approved_date, a.rejection_reason,
		       a.ks2_source_id, a.notes, a.created_by, a.created_date, a.updated_date,
		       COALESCE(s.name, '') as subcontract_name,
		       COALESCE(u.first_name || ' ' || u.last_name, '') as approved_name,
		       COALESCE(cu.first_name || ' ' || cu.last_name, '') as created_name,
		       a.act_number, a.vat_pct, a.vat_amount, a.amount_total_with_vat,
		       a.signed_contractor_at, a.signed_client_at,
		       a.stage_id, a.location_axes, a.works_start_date, a.works_end_date,
		       a.signed_designer_at, a.signed_gasn_at,
		       a.cumul_from_start, a.cumul_from_year_start,
		       a.building_id,
		       COALESCE(b.name, '') as building_name,
		       COALESCE(b.code, '') as building_code
		FROM construction_act a
		LEFT JOIN construction_subcontract s ON s.id = a.subcontract_id
		LEFT JOIN construction_buildings b ON b.id = a.building_id
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
	if buildingIDStr != "" {
		if bID, parseErr := strconv.ParseInt(buildingIDStr, 10, 64); parseErr == nil && bID > 0 {
			argCount++
			query += fmt.Sprintf(" AND a.building_id = $%d", argCount)
			args = append(args, bID)
		}
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
		// New fields
		var actNumber sql.NullInt64
		var vatPct, vatAmount, amountTotalWithVat sql.NullFloat64
		var signedContractorAt, signedClientAt sql.NullTime
		var stageID sql.NullInt64
		var locationAxes sql.NullString
		var worksStartDate, worksEndDate sql.NullTime
		var signedDesignerAt, signedGasnAt sql.NullTime
		var cumulFromStart, cumulFromYearStart sql.NullFloat64
		var buildingID sql.NullInt64
		var buildingName, buildingCode string

		if err := rows.Scan(
			&id, &name, &actTypeVal, &projectIDVal, &subcontractID,
			&periodFrom, &periodTo, &amountTotal, &currency,
			&stateVal, &approvedBy, &approvedDate, &rejectionReason,
			&ks2SourceID, &notes, &createdBy, &createdDate, &updatedDate,
			&subcontractName, &approvedName, &createdName,
			&actNumber, &vatPct, &vatAmount, &amountTotalWithVat,
			&signedContractorAt, &signedClientAt,
			&stageID, &locationAxes, &worksStartDate, &worksEndDate,
			&signedDesignerAt, &signedGasnAt,
			&cumulFromStart, &cumulFromYearStart,
			&buildingID, &buildingName, &buildingCode,
		); err != nil {
			h.log.Error("Failed to scan act", "error", err)
			continue
		}

		item := map[string]interface{}{
			"id":                     id,
			"name":                   name,
			"act_type":               actTypeVal,
			"project_id":             projectIDVal,
			"subcontract_id":         nullInt64Val(subcontractID),
			"subcontract_name":       subcontractName,
			"building_id":            nullInt64Val(buildingID),
			"building_name":          buildingName,
			"building_code":          buildingCode,
			"period_from":            nullTimeVal(periodFrom),
			"period_to":              nullTimeVal(periodTo),
			"amount_total":           amountTotal,
			"currency":               currency,
			"state":                  stateVal,
			"approved_by":            nullUUIDVal(approvedBy),
			"approved_name":          approvedName,
			"approved_date":          nullTimeVal(approvedDate),
			"rejection_reason":       nullStringVal(rejectionReason),
			"ks2_source_id":          nullInt64Val(ks2SourceID),
			"notes":                  nullStringVal(notes),
			"created_by":             nullUUIDVal(createdBy),
			"created_name":           createdName,
			"created_date":           createdDate,
			"updated_date":           updatedDate,
			"act_number":             nullInt64Val(actNumber),
			"vat_pct":                nullFloat64Val(vatPct),
			"vat_amount":             nullFloat64Val(vatAmount),
			"amount_total_with_vat":  nullFloat64Val(amountTotalWithVat),
			"signed_contractor_at":   nullTimeVal(signedContractorAt),
			"signed_client_at":       nullTimeVal(signedClientAt),
			"stage_id":               nullInt64Val(stageID),
			"location_axes":          nullStringVal(locationAxes),
			"works_start_date":       nullTimeVal(worksStartDate),
			"works_end_date":         nullTimeVal(worksEndDate),
			"signed_designer_at":     nullTimeVal(signedDesignerAt),
			"signed_gasn_at":         nullTimeVal(signedGasnAt),
			"cumul_from_start":       nullFloat64Val(cumulFromStart),
			"cumul_from_year_start":  nullFloat64Val(cumulFromYearStart),
		}

		items = append(items, item)
	}

	response.Success(c, items)
}

// CreateConstructionAct creates a new act (Forma 2 / Forma 19 / other)
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
		ActType          string  `json:"act_type" binding:"required"`
		SubcontractID    int64   `json:"subcontract_id"`
		PeriodFrom       string  `json:"period_from"`
		PeriodTo         string  `json:"period_to"`
		Notes            string  `json:"notes"`
		VatPct           float64 `json:"vat_pct"`
		// Forma 2 cost-split aggregates (optional — if omitted we derive from lines)
		F2LaborTotal       float64 `json:"f2_labor_total"`
		F2EquipmentTotal   float64 `json:"f2_equipment_total"`
		F2MaterialsTotal   float64 `json:"f2_materials_total"`
		F2CablesTotal      float64 `json:"f2_cables_total"`
		F2TransportPct     float64 `json:"f2_transport_pct"`
		F2OtherPct         float64 `json:"f2_other_pct"`
		F2MaterialsReturn  float64 `json:"f2_materials_returned"`
		PeriodMonthFrom    int     `json:"period_month_from"`
		PeriodMonthTo      int     `json:"period_month_to"`
		PeriodYear         int     `json:"period_year"`
		// Forma 19 fields
		StageID          int64   `json:"stage_id"`
		LocationAxes     string  `json:"location_axes"`
		DrawingReference string  `json:"drawing_reference"`
		WorksStartDate   string  `json:"works_start_date"`
		WorksEndDate     string  `json:"works_end_date"`
		Photos           []struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
		} `json:"photos"`
		MaterialsJSON []struct {
			Name           string `json:"name"`
			CertificateURL string `json:"certificate_url"`
		} `json:"materials_json"`
		Lines []struct {
			WBSID             int64   `json:"wbs_id"`
			EstimateLineID    int64   `json:"estimate_line_id"`
			Name              string  `json:"name"`
			UOM               string  `json:"uom"`
			Quantity          float64 `json:"quantity"`
			QtySmeta          float64 `json:"qty_smeta"`
			UnitRate          float64 `json:"unit_rate"`
			Note              string  `json:"note"`
			// Forma 2 per-line hierarchy + cost split (all optional)
			LineNumberDisplay string  `json:"line_number_display"`
			IsSectionHeader   bool    `json:"is_section_header"`
			SectionName       string  `json:"section_name"`
			NormCode          string  `json:"norm_code"`
			LaborAmount       float64 `json:"labor_amount"`
			EquipmentAmount   float64 `json:"equipment_amount"`
			MaterialsAmount   float64 `json:"materials_amount"`
			CablesAmount      float64 `json:"cables_amount"`
		} `json:"lines"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Akt turi tanlanishi shart")
		return
	}

	if req.ActType == "" {
		response.BadRequest(c, "Akt turi tanlanishi shart")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Forma 19 validations
	if req.ActType == "hidden_work" {
		if req.StageID == 0 {
			response.BadRequest(c, "Yashirin ishlar akti uchun bosqich (stage_id) tanlang")
			return
		}
		if req.LocationAxes == "" {
			response.BadRequest(c, "O'qlar va belgilarni kiriting")
			return
		}
		if req.WorksStartDate == "" || req.WorksEndDate == "" {
			response.BadRequest(c, "Ish boshlangan va tugagan sanalarni kiriting")
			return
		}
		if req.WorksEndDate < req.WorksStartDate {
			response.BadRequest(c, "Tugash sanasi boshlanish sanasidan oldin bo'lishi mumkin emas")
			return
		}
		if len(req.Photos) < 2 {
			response.BadRequest(c, "Kamida 2 ta rasm yuklanishi shart")
			return
		}
	}

	// Forma 2 validations
	if req.ActType == "ks2" {
		// Validate qty_period <= remaining by smeta for each line
		for _, line := range req.Lines {
			if line.EstimateLineID > 0 && line.Quantity > 0 {
				var smetaQty float64
				h.db.QueryRow(`SELECT COALESCE(quantity, 0) FROM construction_estimate_line WHERE id = $1`,
					line.EstimateLineID).Scan(&smetaQty)
				var usedQty float64
				h.db.QueryRow(`
					SELECT COALESCE(SUM(al.quantity), 0)
					FROM construction_act_line al
					JOIN construction_act a ON a.id = al.act_id
					WHERE al.estimate_line_id = $1 AND a.act_type = 'ks2'
					  AND a.state IN ('draft', 'submitted', 'approved', 'signed')
					  AND a.tenant_id = $2
				`, line.EstimateLineID, tenantID).Scan(&usedQty)
				if usedQty+line.Quantity > smetaQty && smetaQty > 0 {
					response.BadRequest(c, fmt.Sprintf("Quantity for '%s' exceeds remaining estimate (%.2f used of %.2f)", line.Name, usedQty, smetaQty))
					return
				}
			}
		}
	}

	// Auto-generate name. Migration 327 renamed the user-facing labels from
	// "KS-2 / KS-3" to "Forma 2 / Forma 3"; mirror that here so new acts are
	// named "Forma 2-001" / "Forma 3-001" instead of "KS2-001" / "KS3-001".
	// The internal act_type values (ks2 / ks3) stay the same.
	prefix := "ACT"
	switch req.ActType {
	case "ks2":
		prefix = "Forma 2"
	case "ks3":
		prefix = "Forma 3"
	case "hidden_work":
		prefix = "F19"
	}
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE project_id = $1 AND tenant_id = $2 AND act_type = $3`,
		projectID, tenantID, req.ActType).Scan(&count)
	name := fmt.Sprintf("%s-%03d", prefix, count+1)

	// Auto-assign act_number for ks2
	var actNumber int
	if req.ActType == "ks2" {
		h.db.QueryRow(`SELECT COALESCE(MAX(act_number), 0) + 1 FROM construction_act WHERE subcontract_id = $1 AND act_type = 'ks2' AND tenant_id = $2`,
			req.SubcontractID, tenantID).Scan(&actNumber)
	}

	// Default VAT to 12%
	if req.VatPct == 0 && req.ActType == "ks2" {
		req.VatPct = 12
	}

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

	// Serialize photos and materials for Forma 19
	photosJSON := "[]"
	materialsJSON := "[]"
	if req.ActType == "hidden_work" {
		if len(req.Photos) > 0 {
			if b, err := json.Marshal(req.Photos); err == nil {
				photosJSON = string(b)
			}
		}
		if len(req.MaterialsJSON) > 0 {
			if b, err := json.Marshal(req.MaterialsJSON); err == nil {
				materialsJSON = string(b)
			}
		}
	}

	var actID int64
	err = tx.QueryRow(`
		INSERT INTO construction_act (
			tenant_id, project_id, subcontract_id, name, act_type,
			period_from, period_to, amount_total, currency,
			state, notes, created_by, created_date, updated_date,
			act_number, vat_pct,
			stage_id, location_axes, drawing_reference,
			works_start_date, works_end_date,
			photos, materials_json
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 'UZS', 'draft', $8, $9, NOW(), NOW(),
			$10, $11,
			$12, $13, $14,
			$15, $16,
			$17::jsonb, $18::jsonb
		)
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(req.SubcontractID), name, req.ActType,
		periodFrom, periodTo, nullStringFromVal(req.Notes), userID,
		nullInt64FromVal(int64(actNumber)), req.VatPct,
		nullInt64FromVal(req.StageID), nullStringFromVal(req.LocationAxes), nullStringFromVal(req.DrawingReference),
		nullStringFromVal(req.WorksStartDate), nullStringFromVal(req.WorksEndDate),
		photosJSON, materialsJSON,
	).Scan(&actID)
	if err != nil {
		h.log.Error("Failed to create act", "error", err)
		response.InternalError(c, "Failed to create act")
		return
	}

	// Insert lines and calculate total
	var totalAmount float64
	var sumLabor, sumEquip, sumMaterials, sumCables float64
	for i, line := range req.Lines {
		lineTotal := line.Quantity * line.UnitRate
		// Section-header rows don't contribute to totals.
		if !line.IsSectionHeader {
			totalAmount += lineTotal
		}
		sumLabor += line.LaborAmount
		sumEquip += line.EquipmentAmount
		sumMaterials += line.MaterialsAmount
		sumCables += line.CablesAmount
		_, err := tx.Exec(`
			INSERT INTO construction_act_line (
				act_id, wbs_id, estimate_line_id, name, uom,
				quantity, unit_rate, total_amount, sort_order,
				qty_smeta, note,
				line_number_display, is_section_header, section_name, norm_code,
				labor_amount, equipment_amount, materials_amount, cables_amount
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
				$12, $13, $14, $15,
				$16, $17, $18, $19)
		`, actID, nullInt64FromVal(line.WBSID), nullInt64FromVal(line.EstimateLineID),
			line.Name, line.UOM, line.Quantity, line.UnitRate, lineTotal, i+1,
			line.QtySmeta, nullStringFromVal(line.Note),
			nullStringFromVal(line.LineNumberDisplay), line.IsSectionHeader, nullStringFromVal(line.SectionName), nullStringFromVal(line.NormCode),
			line.LaborAmount, line.EquipmentAmount, line.MaterialsAmount, line.CablesAmount,
		)
		if err != nil {
			h.log.Error("Failed to create act line", "error", err)
			response.InternalError(c, "Failed to create act line")
			return
		}
	}

	// Update total with VAT and Forma-2 cost-split snapshot.
	// If caller didn't provide explicit totals, derive them from per-line values.
	if req.F2LaborTotal == 0 {
		req.F2LaborTotal = sumLabor
	}
	if req.F2EquipmentTotal == 0 {
		req.F2EquipmentTotal = sumEquip
	}
	if req.F2MaterialsTotal == 0 {
		req.F2MaterialsTotal = sumMaterials
	}
	if req.F2CablesTotal == 0 {
		req.F2CablesTotal = sumCables
	}
	if req.F2TransportPct == 0 {
		req.F2TransportPct = 5
	}
	if req.F2OtherPct == 0 {
		req.F2OtherPct = 17
	}
	vatAmount := totalAmount * req.VatPct / 100
	totalWithVat := totalAmount + vatAmount
	tx.Exec(`UPDATE construction_act SET
			amount_total = $1, vat_amount = $2, amount_total_with_vat = $3,
			f2_labor_total = $4, f2_equipment_total = $5, f2_materials_total = $6, f2_cables_total = $7,
			f2_transport_pct = $8, f2_other_pct = $9, f2_materials_returned = $10,
			period_month_from = NULLIF($11, 0), period_month_to = NULLIF($12, 0), period_year = NULLIF($13, 0)
		WHERE id = $14`,
		totalAmount, vatAmount, totalWithVat,
		req.F2LaborTotal, req.F2EquipmentTotal, req.F2MaterialsTotal, req.F2CablesTotal,
		req.F2TransportPct, req.F2OtherPct, req.F2MaterialsReturn,
		req.PeriodMonthFrom, req.PeriodMonthTo, req.PeriodYear,
		actID)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit act", "error", err)
		response.InternalError(c, "Failed to create act")
		return
	}

	// Activity logging
	actDesc := fmt.Sprintf("Akt yaratildi: %s", name)
	if req.ActType == "hidden_work" {
		actDesc = fmt.Sprintf("Forma 19 yaratildi: %s", name)
	}
	h.logConstructionActivity(tenantID, projectID, userID, "act", actDesc, "Act", actID)

	// Forma 19: send notification to project manager
	if req.ActType == "hidden_work" {
		var pmUserID uuid.UUID
		err := h.db.QueryRow(`
			SELECT COALESCE(e.user_id, '00000000-0000-0000-0000-000000000000')
			FROM construction_projects p
			JOIN employees e ON e.id = p.project_manager_id
			WHERE p.id = $1
		`, projectID).Scan(&pmUserID)
		if err == nil && pmUserID != uuid.Nil {
			// `act_name` is stored in `data` so the web can re-render the
			// notification body in the user's current UI language via
			// notificationCatalog.js. Additive — mobile still reads title/message.
			h.createNotification(tenantID, pmUserID, "forma19_created",
				"Yashirin ishlar akti yaratildi",
				fmt.Sprintf("Yangi Forma 19 yaratildi: %s. Tekshirish va imzolash talab etiladi.", name),
				map[string]interface{}{"project_id": projectID, "act_id": actID, "act_name": name})
		}
	}

	// Check period overlap warning for ks2
	var warning string
	if req.ActType == "ks2" && req.PeriodFrom != "" && req.PeriodTo != "" {
		var overlapCount int
		h.db.QueryRow(`
			SELECT COUNT(*) FROM construction_act
			WHERE subcontract_id = $1 AND act_type = 'ks2' AND id != $2
			  AND period_from <= $3::date AND period_to >= $4::date
			  AND state NOT IN ('cancelled', 'rejected')
			  AND tenant_id = $5
		`, req.SubcontractID, actID, req.PeriodTo, req.PeriodFrom, tenantID).Scan(&overlapCount)
		if overlapCount > 0 {
			warning = fmt.Sprintf("Warning: period overlaps with %d existing act(s)", overlapCount)
		}
	}

	resp := map[string]interface{}{
		"id":                    actID,
		"name":                  name,
		"act_number":            actNumber,
		"amount_total":          totalAmount,
		"vat_amount":            vatAmount,
		"amount_total_with_vat": totalWithVat,
		"message":               "Act created successfully",
	}
	if warning != "" {
		resp["warning"] = warning
	}
	response.Created(c, resp)
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

	// Get act header with all new columns
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
	// New fields
	var actNumber sql.NullInt64
	var vatPct, vatAmount, amountTotalWithVat sql.NullFloat64
	var signedContractorAt, signedClientAt sql.NullTime
	var signedContractorBy, signedClientBy uuid.NullUUID
	var stageID sql.NullInt64
	var locationAxes, drawingReference sql.NullString
	var worksStartDate, worksEndDate sql.NullTime
	var photosRaw, materialsRaw []byte
	var signedDesignerAt, signedGasnAt sql.NullTime
	var signedDesignerBy, signedGasnBy uuid.NullUUID
	var cumulFromStart, cumulFromYearStart, cumulPrevPeriod sql.NullFloat64
	var smrAmount, equipAmount, otherAmount sql.NullFloat64
	var stageName sql.NullString

	err = h.db.QueryRow(`
		SELECT a.id, a.name, a.act_type, a.project_id, a.subcontract_id,
		       a.period_from, a.period_to, a.amount_total, a.currency,
		       a.state, a.approved_by, a.approved_date, a.rejection_reason,
		       a.ks2_source_id, a.notes, a.created_by, a.created_date, a.updated_date,
		       COALESCE(s.name, '') as subcontract_name,
		       a.act_number, a.vat_pct, a.vat_amount, a.amount_total_with_vat,
		       a.signed_contractor_at, a.signed_contractor_by,
		       a.signed_client_at, a.signed_client_by,
		       a.stage_id, a.location_axes, a.drawing_reference,
		       a.works_start_date, a.works_end_date,
		       COALESCE(a.photos::text, '[]')::bytea, COALESCE(a.materials_json::text, '[]')::bytea,
		       a.signed_designer_at, a.signed_designer_by,
		       a.signed_gasn_at, a.signed_gasn_by,
		       a.cumul_from_start, a.cumul_from_year_start, a.cumul_previous_period,
		       a.smr_amount, a.equipment_amount, a.other_amount,
		       COALESCE(st.name, '') as stage_name
		FROM construction_act a
		LEFT JOIN construction_subcontract s ON s.id = a.subcontract_id
		LEFT JOIN construction_stages st ON st.id = a.stage_id
		WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(
		&id, &name, &actType, &projectIDVal, &subcontractID,
		&periodFrom, &periodTo, &amountTotal, &currency,
		&state, &approvedBy, &approvedDate, &rejectionReason,
		&ks2SourceID, &notes, &createdBy, &createdDate, &updatedDate,
		&subcontractName,
		&actNumber, &vatPct, &vatAmount, &amountTotalWithVat,
		&signedContractorAt, &signedContractorBy,
		&signedClientAt, &signedClientBy,
		&stageID, &locationAxes, &drawingReference,
		&worksStartDate, &worksEndDate,
		&photosRaw, &materialsRaw,
		&signedDesignerAt, &signedDesignerBy,
		&signedGasnAt, &signedGasnBy,
		&cumulFromStart, &cumulFromYearStart, &cumulPrevPeriod,
		&smrAmount, &equipAmount, &otherAmount,
		&stageName,
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

	// Parse JSON fields
	var photos, materials interface{}
	json.Unmarshal(photosRaw, &photos)
	json.Unmarshal(materialsRaw, &materials)

	// Get lines with new fields (Forma 2 hierarchy + cost split)
	lineRows, err := h.db.Query(`
		SELECT al.id, al.wbs_id, al.estimate_line_id, al.name, al.uom,
		       al.quantity, al.unit_rate, al.total_amount, al.sort_order,
		       COALESCE(w.code, '') as wbs_code,
		       COALESCE(w.name, '') as wbs_name,
		       al.qty_smeta, al.note,
		       COALESCE(al.line_number_display, ''), COALESCE(al.is_section_header, FALSE),
		       COALESCE(al.section_name, ''), COALESCE(al.norm_code, ''),
		       COALESCE(al.labor_amount, 0), COALESCE(al.equipment_amount, 0),
		       COALESCE(al.materials_amount, 0), COALESCE(al.cables_amount, 0)
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
			var qtySmeta sql.NullFloat64
			var lineNote sql.NullString
			var lineNumberDisplay, sectionName, normCode string
			var isSectionHeader bool
			var laborAmt, equipAmt, materialsAmt, cablesAmt float64

			if lineRows.Scan(
				&lineID, &wbsID, &estimateLineID, &lineName, &lineUOM,
				&qty, &unitRate, &lineTotal, &sortOrder,
				&wbsCode, &wbsName,
				&qtySmeta, &lineNote,
				&lineNumberDisplay, &isSectionHeader,
				&sectionName, &normCode,
				&laborAmt, &equipAmt, &materialsAmt, &cablesAmt,
			) == nil {
				lines = append(lines, map[string]interface{}{
					"id":                  lineID,
					"wbs_id":              nullInt64Val(wbsID),
					"estimate_line_id":    nullInt64Val(estimateLineID),
					"name":                lineName,
					"uom":                 lineUOM,
					"quantity":            qty,
					"unit_rate":           unitRate,
					"total_amount":        lineTotal,
					"sort_order":          sortOrder,
					"wbs_code":            wbsCode,
					"wbs_name":            wbsName,
					"qty_smeta":           nullFloat64Val(qtySmeta),
					"note":                nullStringVal(lineNote),
					"line_number_display": lineNumberDisplay,
					"is_section_header":   isSectionHeader,
					"section_name":        sectionName,
					"norm_code":           normCode,
					"labor_amount":        laborAmt,
					"equipment_amount":    equipAmt,
					"materials_amount":    materialsAmt,
					"cables_amount":       cablesAmt,
				})
			}
		}
	}

	// Get signer names
	getSignerName := func(signerID uuid.NullUUID) interface{} {
		if !signerID.Valid || signerID.UUID == uuid.Nil {
			return nil
		}
		var n string
		h.db.QueryRow(`SELECT COALESCE(first_name || ' ' || last_name, '') FROM users WHERE id = $1`, signerID.UUID).Scan(&n)
		return n
	}

	// For KS-2 acts, also fetch material usage during the act period
	var materialUsage []map[string]interface{}
	if actType == "ks2" && periodFrom.Valid && periodTo.Valid {
		muRows, muErr := h.db.Query(`
			SELECT mu.product_name, mu.uom, SUM(mu.quantity_used) as total_qty,
			       mu.product_id, mu.estimate_line_id,
			       STRING_AGG(DISTINCT mu.notes, '; ') FILTER (WHERE mu.notes IS NOT NULL AND mu.notes != '') as notes
			FROM construction_material_usage mu
			WHERE mu.tenant_id = $1 AND mu.project_id = $2
			  AND mu.usage_date >= $3 AND mu.usage_date <= $4
			GROUP BY mu.product_name, mu.uom, mu.product_id, mu.estimate_line_id
			ORDER BY mu.product_name
		`, tenantID, projectIDVal, periodFrom.Time, periodTo.Time)
		if muErr == nil {
			defer muRows.Close()
			idx := 0
			for muRows.Next() {
				var muName, muUOM string
				var muQty float64
				var muProductID uuid.NullUUID
				var muEstLineID sql.NullInt64
				var muNotes sql.NullString
				idx++
				if muRows.Scan(&muName, &muUOM, &muQty, &muProductID, &muEstLineID, &muNotes) == nil {
					materialUsage = append(materialUsage, map[string]interface{}{
						"id":               idx,
						"product_name":     muName,
						"uom":              muUOM,
						"quantity_used":    muQty,
						"product_id":       nullUUIDVal(muProductID),
						"notes":            nullStringVal(muNotes),
						"estimate_line_id": nullInt64Val(muEstLineID),
					})
				}
			}
		}
	}
	if materialUsage == nil {
		materialUsage = []map[string]interface{}{}
	}

	result := map[string]interface{}{
		"id":                     id,
		"name":                   name,
		"act_type":               actType,
		"project_id":             projectIDVal,
		"subcontract_id":         nullInt64Val(subcontractID),
		"subcontract_name":       subcontractName,
		"period_from":            nullTimeVal(periodFrom),
		"period_to":              nullTimeVal(periodTo),
		"amount_total":           amountTotal,
		"currency":               currency,
		"state":                  state,
		"approved_by":            nullUUIDVal(approvedBy),
		"approved_date":          nullTimeVal(approvedDate),
		"rejection_reason":       nullStringVal(rejectionReason),
		"ks2_source_id":          nullInt64Val(ks2SourceID),
		"notes":                  nullStringVal(notes),
		"created_date":           createdDate,
		"updated_date":           updatedDate,
		"lines":                  lines,
		"act_number":             nullInt64Val(actNumber),
		"vat_pct":                nullFloat64Val(vatPct),
		"vat_amount":             nullFloat64Val(vatAmount),
		"amount_total_with_vat":  nullFloat64Val(amountTotalWithVat),
		"signed_contractor_at":   nullTimeVal(signedContractorAt),
		"signed_contractor_by":   nullUUIDVal(signedContractorBy),
		"signed_contractor_name": getSignerName(signedContractorBy),
		"signed_client_at":       nullTimeVal(signedClientAt),
		"signed_client_by":       nullUUIDVal(signedClientBy),
		"signed_client_name":     getSignerName(signedClientBy),
		"stage_id":               nullInt64Val(stageID),
		"stage_name":             nullStringVal(stageName),
		"location_axes":          nullStringVal(locationAxes),
		"drawing_reference":      nullStringVal(drawingReference),
		"works_start_date":       nullTimeVal(worksStartDate),
		"works_end_date":         nullTimeVal(worksEndDate),
		"photos":                 photos,
		"materials_json":         materials,
		"signed_designer_at":     nullTimeVal(signedDesignerAt),
		"signed_designer_by":     nullUUIDVal(signedDesignerBy),
		"signed_designer_name":   getSignerName(signedDesignerBy),
		"signed_gasn_at":         nullTimeVal(signedGasnAt),
		"signed_gasn_by":         nullUUIDVal(signedGasnBy),
		"signed_gasn_name":       getSignerName(signedGasnBy),
		"cumul_from_start":       nullFloat64Val(cumulFromStart),
		"cumul_from_year_start":  nullFloat64Val(cumulFromYearStart),
		"cumul_previous_period":  nullFloat64Val(cumulPrevPeriod),
		"smr_amount":             nullFloat64Val(smrAmount),
		"equipment_amount":       nullFloat64Val(equipAmount),
		"other_amount":           nullFloat64Val(otherAmount),
		"material_usage":         materialUsage,
	}

	response.Success(c, result)
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
		SubcontractID             int64  `json:"subcontract_id"`
		BuildingID                int64  `json:"building_id"`            // 0 = all buildings (project-wide)
		PeriodFrom                string `json:"period_from" binding:"required"`
		PeriodTo                  string `json:"period_to" binding:"required"`
		ClientName                string `json:"client_name"`
		ClientPhone               string `json:"client_phone"`
		ClientAddress             string `json:"client_address"`
		ClientBankName            string `json:"client_bank_name"`
		ClientBankAccount         string `json:"client_bank_account"`
		ClientMFO                 string `json:"client_mfo"`
		ClientSTIR                string `json:"client_stir"`
		ClientOKONH               string `json:"client_okonh"`
		ContractNumber            string `json:"contract_number"`
		ObjectFullName            string `json:"object_full_name"`
		ClientDirectorName        string `json:"client_director_name"`
		ClientChiefAccountantName string `json:"client_chief_accountant_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Boshlanish va tugash sanalarini kiriting")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Save client requisites to project if provided
	if req.ClientName != "" {
		h.saveProjectClientDetails(tenantID, projectID, req.ClientName, req.ClientPhone, req.ClientAddress,
			req.ClientBankName, req.ClientBankAccount, req.ClientMFO, req.ClientSTIR, req.ClientOKONH,
			req.ContractNumber, req.ObjectFullName, req.ClientDirectorName, req.ClientChiefAccountantName)
	}

	// Validate project has required client details for KS-2
	if !h.projectHasRequiredClientDetails(tenantID, projectID) {
		response.Error(c, http.StatusBadRequest, "MISSING_CLIENT_DETAILS",
			"Loyiha buyurtmachi ma'lumotlari to'ldirilmagan (nomi, STIR, bank, MFO, manzil)")
		return
	}

	// If a specific building is requested, verify it belongs to this project
	if req.BuildingID > 0 {
		var ok bool
		h.db.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM construction_buildings
			WHERE id = $1 AND project_id = $2 AND tenant_id = $3
		)`, req.BuildingID, projectID, tenantID).Scan(&ok)
		if !ok {
			response.BadRequest(c, "Tanlangan bino ushbu loyihaga tegishli emas")
			return
		}
	}

	wbsIDs := []int64{}

	if req.SubcontractID > 0 {
		// Get WBS IDs linked to this subcontract
		wbsRows, err := h.db.Query(
			`SELECT wbs_id FROM construction_subcontract_wbs WHERE subcontract_id = $1`, req.SubcontractID)
		if err != nil {
			h.log.Error("Failed to get subcontract WBS", "error", err)
			response.InternalError(c, "Failed to get subcontract WBS items")
			return
		}
		defer wbsRows.Close()

		for wbsRows.Next() {
			var wbsID int64
			if wbsRows.Scan(&wbsID) == nil {
				wbsIDs = append(wbsIDs, wbsID)
			}
		}

		if len(wbsIDs) == 0 {
			response.BadRequest(c, "Subpudratchi WBS elementlariga bog'lanmagan")
			return
		}
	} else {
		// Own forces: get ALL WBS IDs for this project
		wbsRows, err := h.db.Query(
			`SELECT id FROM construction_wbs WHERE project_id = $1 AND tenant_id = $2 AND is_active = true`,
			projectID, tenantID)
		if err != nil {
			h.log.Error("Failed to get project WBS", "error", err)
			response.InternalError(c, "Failed to get project WBS items")
			return
		}
		defer wbsRows.Close()

		for wbsRows.Next() {
			var wbsID int64
			if wbsRows.Scan(&wbsID) == nil {
				wbsIDs = append(wbsIDs, wbsID)
			}
		}

		if len(wbsIDs) == 0 {
			response.BadRequest(c, "Loyihada WBS elementlari topilmadi")
			return
		}
	}

	type actLineData struct {
		WBSID          int64
		EstimateLineID int64
		Name           string
		UOM            string
		Quantity       float64
		QtySmeta       float64
		UnitRate       float64
	}

	var lineDataList []actLineData

	for _, wbsID := range wbsIDs {
		var totalDone float64
		h.db.QueryRow(`
			SELECT COALESCE(SUM(quantity_done), 0)
			FROM construction_daily_log
			WHERE wbs_id = $1 AND tenant_id = $2 AND date >= $3 AND date <= $4
		`, wbsID, tenantID, req.PeriodFrom, req.PeriodTo).Scan(&totalDone)

		if totalDone <= 0 {
			continue
		}

		// When a specific building is requested, restrict the estimate-line
		// pick to that building (via construction_estimate.building_id). Else
		// stay project-wide (legacy behaviour).
		var rows *sql.Rows
		var queryErr error
		if req.BuildingID > 0 {
			rows, queryErr = h.db.Query(`
				SELECT el.id, el.name, el.uom, el.unit_rate, COALESCE(el.quantity, 0)
				FROM construction_estimate_line el
				JOIN construction_estimate e ON e.id = el.estimate_id
				WHERE el.wbs_id = $1 AND e.is_current = true AND e.tenant_id = $2
				  AND e.building_id = $3
				LIMIT 1
			`, wbsID, tenantID, req.BuildingID)
		} else {
			rows, queryErr = h.db.Query(`
				SELECT el.id, el.name, el.uom, el.unit_rate, COALESCE(el.quantity, 0)
				FROM construction_estimate_line el
				JOIN construction_estimate e ON e.id = el.estimate_id
				WHERE el.wbs_id = $1 AND e.is_current = true AND e.tenant_id = $2
				LIMIT 1
			`, wbsID, tenantID)
		}
		if queryErr != nil {
			continue
		}

		for rows.Next() {
			var elID int64
			var elName, elUOM string
			var unitRate, smetaQty float64
			if rows.Scan(&elID, &elName, &elUOM, &unitRate, &smetaQty) == nil {
				lineDataList = append(lineDataList, actLineData{
					WBSID:          wbsID,
					EstimateLineID: elID,
					Name:           elName,
					UOM:            elUOM,
					Quantity:       totalDone,
					QtySmeta:       smetaQty,
					UnitRate:       unitRate,
				})
			}
		}
		rows.Close()
	}

	if len(lineDataList) == 0 {
		if req.BuildingID > 0 {
			response.BadRequest(c, "Tanlangan bino va davr uchun ishlar topilmadi")
		} else if req.SubcontractID > 0 {
			response.BadRequest(c, "Tanlangan davr uchun bu subpudratchi bo'yicha ishlar topilmadi")
		} else {
			response.BadRequest(c, "Tanlangan davr uchun ishlar topilmadi")
		}
		return
	}

	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE project_id = $1 AND tenant_id = $2 AND act_type = 'ks2'`,
		projectID, tenantID).Scan(&count)
	name := fmt.Sprintf("KS2-%03d", count+1)

	var actNumber int
	if req.SubcontractID > 0 {
		h.db.QueryRow(`SELECT COALESCE(MAX(act_number), 0) + 1 FROM construction_act WHERE subcontract_id = $1 AND act_type = 'ks2' AND tenant_id = $2`,
			req.SubcontractID, tenantID).Scan(&actNumber)
	} else {
		h.db.QueryRow(`SELECT COALESCE(MAX(act_number), 0) + 1 FROM construction_act WHERE subcontract_id IS NULL AND act_type = 'ks2' AND project_id = $1 AND tenant_id = $2`,
			projectID, tenantID).Scan(&actNumber)
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create KS-2")
		return
	}
	defer tx.Rollback()

	var actID int64
	err = tx.QueryRow(`
		INSERT INTO construction_act (
			tenant_id, project_id, subcontract_id, building_id, name, act_type,
			period_from, period_to, amount_total, currency,
			state, created_by, created_date, updated_date,
			act_number, vat_pct
		) VALUES ($1, $2, $3, $4, $5, 'ks2', $6, $7, 0, 'UZS', 'draft', $8, NOW(), NOW(), $9, 12)
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(req.SubcontractID), nullInt64FromVal(req.BuildingID),
		name, req.PeriodFrom, req.PeriodTo, userID, actNumber,
	).Scan(&actID)
	if err != nil {
		h.log.Error("Failed to create Forma 2 act", "error", err)
		response.InternalError(c, "Failed to create Forma 2")
		return
	}

	var totalAmount float64
	for i, ld := range lineDataList {
		lineTotal := ld.Quantity * ld.UnitRate
		totalAmount += lineTotal
		tx.Exec(`
			INSERT INTO construction_act_line (
				act_id, wbs_id, estimate_line_id, name, uom,
				quantity, unit_rate, total_amount, sort_order, qty_smeta
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, actID, ld.WBSID, ld.EstimateLineID, ld.Name, ld.UOM,
			ld.Quantity, ld.UnitRate, lineTotal, i+1, ld.QtySmeta,
		)
	}

	vatAmount := totalAmount * 0.12
	totalWithVat := totalAmount + vatAmount
	tx.Exec(`UPDATE construction_act SET amount_total = $1, vat_amount = $2, amount_total_with_vat = $3 WHERE id = $4`,
		totalAmount, vatAmount, totalWithVat, actID)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit KS-2", "error", err)
		response.InternalError(c, "Failed to create KS-2")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "act",
		fmt.Sprintf("Forma 2 avtomatik yaratildi: %s (%.0f so'm)", name, totalAmount), "Act", actID)

	response.Created(c, map[string]interface{}{
		"id":                    actID,
		"name":                  name,
		"act_number":            actNumber,
		"amount_total":          totalAmount,
		"vat_amount":            vatAmount,
		"amount_total_with_vat": totalWithVat,
		"lines_count":           len(lineDataList),
		"message":               "Forma 2 act auto-generated successfully",
	})
}

// PreviewAutoGenerateKS2 computes what an auto-generated KS-2 would contain
// without creating any database records. Used to show a confirmation dialog
// before the user commits to creating the act.
func (h *Handler) PreviewAutoGenerateKS2(c *gin.Context) {
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
		SubcontractID int64  `json:"subcontract_id"`
		BuildingID    int64  `json:"building_id"`
		PeriodFrom    string `json:"period_from" binding:"required"`
		PeriodTo      string `json:"period_to" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Boshlanish va tugash sanalarini kiriting")
		return
	}

	wbsIDs := []int64{}
	if req.SubcontractID > 0 {
		wbsRows, err := h.db.Query(
			`SELECT wbs_id FROM construction_subcontract_wbs WHERE subcontract_id = $1`, req.SubcontractID)
		if err != nil {
			response.InternalError(c, "Failed to get subcontract WBS items")
			return
		}
		defer wbsRows.Close()
		for wbsRows.Next() {
			var wbsID int64
			if wbsRows.Scan(&wbsID) == nil {
				wbsIDs = append(wbsIDs, wbsID)
			}
		}
		if len(wbsIDs) == 0 {
			response.BadRequest(c, "Subpudratchi WBS elementlariga bog'lanmagan")
			return
		}
	} else {
		wbsRows, err := h.db.Query(
			`SELECT id FROM construction_wbs WHERE project_id = $1 AND tenant_id = $2 AND is_active = true`,
			projectID, tenantID)
		if err != nil {
			response.InternalError(c, "Failed to get project WBS items")
			return
		}
		defer wbsRows.Close()
		for wbsRows.Next() {
			var wbsID int64
			if wbsRows.Scan(&wbsID) == nil {
				wbsIDs = append(wbsIDs, wbsID)
			}
		}
		if len(wbsIDs) == 0 {
			response.BadRequest(c, "Loyihada WBS elementlari topilmadi")
			return
		}
	}

	type previewLine struct {
		WBSID          int64   `json:"wbs_id"`
		EstimateLineID int64   `json:"estimate_line_id"`
		Name           string  `json:"name"`
		UOM            string  `json:"uom"`
		Quantity       float64 `json:"quantity"`
		QtySmeta       float64 `json:"qty_smeta"`
		UnitRate       float64 `json:"unit_rate"`
		LineTotal      float64 `json:"line_total"`
	}

	lines := []previewLine{}
	var totalAmount float64

	for _, wbsID := range wbsIDs {
		var totalDone float64
		h.db.QueryRow(`
			SELECT COALESCE(SUM(quantity_done), 0)
			FROM construction_daily_log
			WHERE wbs_id = $1 AND tenant_id = $2 AND date >= $3 AND date <= $4
		`, wbsID, tenantID, req.PeriodFrom, req.PeriodTo).Scan(&totalDone)

		if totalDone <= 0 {
			continue
		}

		var rows *sql.Rows
		var queryErr error
		if req.BuildingID > 0 {
			rows, queryErr = h.db.Query(`
				SELECT el.id, el.name, el.uom, el.unit_rate, COALESCE(el.quantity, 0)
				FROM construction_estimate_line el
				JOIN construction_estimate e ON e.id = el.estimate_id
				WHERE el.wbs_id = $1 AND e.is_current = true AND e.tenant_id = $2
				  AND e.building_id = $3
				LIMIT 1
			`, wbsID, tenantID, req.BuildingID)
		} else {
			rows, queryErr = h.db.Query(`
				SELECT el.id, el.name, el.uom, el.unit_rate, COALESCE(el.quantity, 0)
				FROM construction_estimate_line el
				JOIN construction_estimate e ON e.id = el.estimate_id
				WHERE el.wbs_id = $1 AND e.is_current = true AND e.tenant_id = $2
				LIMIT 1
			`, wbsID, tenantID)
		}
		err := queryErr
		if err != nil {
			continue
		}
		for rows.Next() {
			var elID int64
			var elName, elUOM string
			var unitRate, smetaQty float64
			if rows.Scan(&elID, &elName, &elUOM, &unitRate, &smetaQty) == nil {
				lineTotal := totalDone * unitRate
				totalAmount += lineTotal
				lines = append(lines, previewLine{
					WBSID:          wbsID,
					EstimateLineID: elID,
					Name:           elName,
					UOM:            elUOM,
					Quantity:       totalDone,
					QtySmeta:       smetaQty,
					UnitRate:       unitRate,
					LineTotal:      lineTotal,
				})
			}
		}
		rows.Close()
	}

	if len(lines) == 0 {
		if req.SubcontractID > 0 {
			response.BadRequest(c, "Tanlangan davr uchun bu subpudratchi bo'yicha ishlar topilmadi")
		} else {
			response.BadRequest(c, "Tanlangan davr uchun ishlar topilmadi")
		}
		return
	}

	var existingCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE project_id = $1 AND tenant_id = $2 AND act_type = 'ks2'`,
		projectID, tenantID).Scan(&existingCount)
	proposedName := fmt.Sprintf("KS2-%03d", existingCount+1)

	var actNumber int
	if req.SubcontractID > 0 {
		h.db.QueryRow(`SELECT COALESCE(MAX(act_number), 0) + 1 FROM construction_act WHERE subcontract_id = $1 AND act_type = 'ks2' AND tenant_id = $2`,
			req.SubcontractID, tenantID).Scan(&actNumber)
	} else {
		h.db.QueryRow(`SELECT COALESCE(MAX(act_number), 0) + 1 FROM construction_act WHERE subcontract_id IS NULL AND act_type = 'ks2' AND project_id = $1 AND tenant_id = $2`,
			projectID, tenantID).Scan(&actNumber)
	}

	vatPct := 12.0
	vatAmount := totalAmount * vatPct / 100
	totalWithVat := totalAmount + vatAmount

	response.Success(c, map[string]interface{}{
		"proposed_name":         proposedName,
		"proposed_act_number":   actNumber,
		"period_from":           req.PeriodFrom,
		"period_to":             req.PeriodTo,
		"subcontract_id":        req.SubcontractID,
		"lines":                 lines,
		"lines_count":           len(lines),
		"amount_total":          totalAmount,
		"vat_pct":               vatPct,
		"vat_amount":            vatAmount,
		"amount_total_with_vat": totalWithVat,
	})
}

// SignAct handles electronic signing of acts (Forma 2 and Forma 19)
func (h *Handler) SubmitForSigning(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Noto'g'ri ID")
		return
	}

	var state, actType string
	var projectID int64
	var lineCount int
	err = h.db.QueryRow(`SELECT state, act_type, project_id FROM construction_act WHERE id = $1 AND tenant_id = $2`,
		actID, tenantID).Scan(&state, &actType, &projectID)
	if err != nil {
		response.NotFound(c, "Akt topilmadi")
		return
	}

	if state != "draft" {
		response.BadRequest(c, "Faqat qoralama aktlarni imzolashga yuborish mumkin")
		return
	}

	// For F2, require at least 1 line
	if actType == "ks2" {
		h.db.QueryRow(`SELECT COUNT(*) FROM construction_act_line WHERE act_id = $1`, actID).Scan(&lineCount)
		if lineCount == 0 {
			response.BadRequest(c, "Kamida bitta ish qatorini qo'shing")
			return
		}
	}

	_, err = h.db.Exec(`UPDATE construction_act SET state = 'pending' WHERE id = $1 AND tenant_id = $2`, actID, tenantID)
	if err != nil {
		h.log.Error("Failed to submit act for signing", "error", err)
		response.InternalError(c, "Xatolik yuz berdi")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "act",
		"Akt imzolashga yuborildi", "Act", actID)

	// Notify signers
	h.createNotification(tenantID, userID, "info",
		"Akt imzolashga yuborildi",
		fmt.Sprintf("Akt #%d imzolashni kutmoqda", actID),
		nil)

	response.Success(c, map[string]interface{}{"message": "Akt imzolashga yuborildi"})
}

func (h *Handler) SignAct(c *gin.Context) {
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
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Rolni tanlang (contractor, client, designer, gasn)")
		return
	}

	validRoles := map[string]bool{
		"contractor": true, "client": true, "designer": true, "gasn": true,
	}
	if !validRoles[req.Role] {
		response.BadRequest(c, "Noto'g'ri rol. contractor, client, designer yoki gasn bo'lishi kerak")
		return
	}

	var projectID int64
	var currentState, actType string
	var signedContractorAt, signedClientAt, signedDesignerAt, signedGasnAt sql.NullTime
	err = h.db.QueryRow(`
		SELECT project_id, state, act_type, signed_contractor_at, signed_client_at, signed_designer_at, signed_gasn_at
		FROM construction_act WHERE id = $1 AND tenant_id = $2
	`, actID, tenantID).Scan(&projectID, &currentState, &actType, &signedContractorAt, &signedClientAt, &signedDesignerAt, &signedGasnAt)
	if err != nil {
		response.NotFound(c, "Act not found")
		return
	}

	if currentState == "signed" || currentState == "cancelled" {
		response.BadRequest(c, "Akt allaqachon imzolangan yoki bekor qilingan")
		return
	}

	// Validate role is valid for act type
	if actType == "ks2" && (req.Role == "designer" || req.Role == "gasn") {
		response.BadRequest(c, "Forma 2 aktlari faqat pudratchi va buyurtmachi imzolarini qo'llab-quvvatlaydi")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Set the corresponding signature
	var col string
	switch req.Role {
	case "contractor":
		col = "signed_contractor"
	case "client":
		col = "signed_client"
	case "designer":
		col = "signed_designer"
	case "gasn":
		col = "signed_gasn"
	}

	_, err = h.db.Exec(fmt.Sprintf(`
		UPDATE construction_act SET %s_at = NOW(), %s_by = $1, updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, col, col), userID, actID, tenantID)
	if err != nil {
		h.log.Error("Failed to sign act", "error", err)
		response.InternalError(c, "Failed to sign act")
		return
	}

	// Check if all required signatures are present → transition to 'signed'
	newState := currentState
	if actType == "ks2" || actType == "ks3" {
		// Need both contractor and client
		contractorSigned := signedContractorAt.Valid || req.Role == "contractor"
		clientSigned := signedClientAt.Valid || req.Role == "client"
		if contractorSigned && clientSigned {
			newState = "signed"
		}
	} else if actType == "hidden_work" {
		// Need all 4 signatures
		contractorSigned := signedContractorAt.Valid || req.Role == "contractor"
		clientSigned := signedClientAt.Valid || req.Role == "client"
		designerSigned := signedDesignerAt.Valid || req.Role == "designer"
		gasnSigned := signedGasnAt.Valid || req.Role == "gasn"
		if contractorSigned && clientSigned && designerSigned && gasnSigned {
			newState = "signed"
		}
	}

	if newState == "signed" {
		h.db.Exec(`UPDATE construction_act SET state = 'signed', updated_date = NOW() WHERE id = $1`, actID)
	}

	// Get act name for logging
	var actName string
	h.db.QueryRow(`SELECT name FROM construction_act WHERE id = $1`, actID).Scan(&actName)

	// Activity log
	h.logConstructionActivity(tenantID, projectID, userID, "act",
		fmt.Sprintf("Akt imzolandi (%s): %s", req.Role, actName), "Act", actID)

	// Notification: if ks2 and now fully signed, notify project manager
	if newState == "signed" && (actType == "ks2" || actType == "ks3") {
		var pmUserID uuid.UUID
		h.db.QueryRow(`
			SELECT COALESCE(e.user_id, '00000000-0000-0000-0000-000000000000')
			FROM construction_projects p JOIN employees e ON e.id = p.project_manager_id
			WHERE p.id = $1
		`, projectID).Scan(&pmUserID)
		if pmUserID != uuid.Nil {
			// `act_type` carried in `data` for the web renderer; additive.
			h.createNotification(tenantID, pmUserID, "act_signed",
				"Akt imzolandi",
				fmt.Sprintf("Akt barcha tomonlar tomonidan imzolandi (loyiha: %d)", projectID),
				map[string]interface{}{"project_id": projectID, "act_id": actID, "act_type": actType})
		}

		// Recalculate Forma 3 if a KS-2 was signed
		if actType == "ks2" {
			var subID int64
			h.db.QueryRow(`SELECT COALESCE(subcontract_id, 0) FROM construction_act WHERE id = $1`, actID).Scan(&subID)
			if subID > 0 {
				h.recalculateForma3(tenantID, projectID, subID)
			}
		}
	}

	response.Success(c, map[string]interface{}{
		"id":      actID,
		"state":   newState,
		"signed":  req.Role,
		"message": fmt.Sprintf("Act signed by %s", req.Role),
	})
}

// CancelAct cancels a signed act (with reason)
func (h *Handler) CancelAct(c *gin.Context) {
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
		response.BadRequest(c, "Bekor qilish sababini kiriting")
		return
	}

	var projectID int64
	var currentState, actType string
	var subcontractID sql.NullInt64
	err = h.db.QueryRow(`SELECT project_id, state, act_type, subcontract_id FROM construction_act WHERE id = $1 AND tenant_id = $2`,
		actID, tenantID).Scan(&projectID, &currentState, &actType, &subcontractID)
	if err != nil {
		response.NotFound(c, "Act not found")
		return
	}

	if currentState != "signed" && currentState != "approved" {
		response.BadRequest(c, "Faqat imzolangan yoki tasdiqlangan aktlarni bekor qilish mumkin")
		return
	}

	userID, _ := middleware.GetUserID(c)

	_, err = h.db.Exec(`
		UPDATE construction_act SET state = 'cancelled', rejection_reason = $1, updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, req.RejectionReason, actID, tenantID)
	if err != nil {
		h.log.Error("Failed to cancel act", "error", err)
		response.InternalError(c, "Failed to cancel act")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "act",
		"Akt bekor qilindi", "Act", actID)

	// Recalculate Forma 3 if ks2 was cancelled
	if actType == "ks2" && subcontractID.Valid && subcontractID.Int64 > 0 {
		h.recalculateForma3(tenantID, projectID, subcontractID.Int64)
	}

	// Notify all parties
	var pmUserID, ceUserID uuid.UUID
	h.db.QueryRow(`
		SELECT COALESCE(e1.user_id, '00000000-0000-0000-0000-000000000000'),
		       COALESCE(e2.user_id, '00000000-0000-0000-0000-000000000000')
		FROM construction_projects p
		LEFT JOIN employees e1 ON e1.id = p.project_manager_id
		LEFT JOIN employees e2 ON e2.id = p.chief_engineer_id
		WHERE p.id = $1
	`, projectID).Scan(&pmUserID, &ceUserID)

	notifMsg := fmt.Sprintf("Akt bekor qilindi. Sabab: %s", req.RejectionReason)
	// `reason` carried in `data` so the web renderer can rebuild the body in
	// the current UI language. Mobile continues to use the frozen notifMsg.
	notifData := map[string]interface{}{"project_id": projectID, "act_id": actID, "reason": req.RejectionReason}
	if pmUserID != uuid.Nil {
		h.createNotification(tenantID, pmUserID, "act_cancelled", "Akt bekor qilindi", notifMsg, notifData)
	}
	if ceUserID != uuid.Nil && ceUserID != pmUserID {
		h.createNotification(tenantID, ceUserID, "act_cancelled", "Akt bekor qilindi", notifMsg, notifData)
	}

	response.Success(c, map[string]interface{}{
		"id":      actID,
		"state":   "cancelled",
		"message": "Act cancelled",
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

// GenerateForma3 creates a KS-3 (Forma 3) from signed KS-2 acts with cumulative totals
func (h *Handler) GenerateForma3(c *gin.Context) {
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
		SubcontractID             int64  `json:"subcontract_id"`
		BuildingID                int64  `json:"building_id"` // 0 = project-wide
		PeriodFrom                string `json:"period_from" binding:"required"`
		PeriodTo                  string `json:"period_to" binding:"required"`
		ClientName                string `json:"client_name"`
		ClientPhone               string `json:"client_phone"`
		ClientAddress             string `json:"client_address"`
		ClientBankName            string `json:"client_bank_name"`
		ClientBankAccount         string `json:"client_bank_account"`
		ClientMFO                 string `json:"client_mfo"`
		ClientSTIR                string `json:"client_stir"`
		ClientOKONH               string `json:"client_okonh"`
		ContractNumber            string `json:"contract_number"`
		ObjectFullName            string `json:"object_full_name"`
		ClientDirectorName        string `json:"client_director_name"`
		ClientChiefAccountantName string `json:"client_chief_accountant_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Boshlanish va tugash sanalarini kiriting")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Save client requisites to project if provided
	if req.ClientName != "" {
		h.saveProjectClientDetails(tenantID, projectID, req.ClientName, req.ClientPhone, req.ClientAddress,
			req.ClientBankName, req.ClientBankAccount, req.ClientMFO, req.ClientSTIR, req.ClientOKONH,
			req.ContractNumber, req.ObjectFullName, req.ClientDirectorName, req.ClientChiefAccountantName)
	}

	// Validate project has required client details for KS-3
	if !h.projectHasRequiredClientDetails(tenantID, projectID) {
		response.Error(c, http.StatusBadRequest, "MISSING_CLIENT_DETAILS",
			"Loyiha buyurtmachi ma'lumotlari to'ldirilmagan (nomi, STIR, bank, MFO, manzil)")
		return
	}

	// Verify the building belongs to this project
	if req.BuildingID > 0 {
		var ok bool
		h.db.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM construction_buildings
			WHERE id = $1 AND project_id = $2 AND tenant_id = $3
		)`, req.BuildingID, projectID, tenantID).Scan(&ok)
		if !ok {
			response.BadRequest(c, "Tanlangan bino ushbu loyihaga tegishli emas")
			return
		}
	}

	// Build subcontract + building filter for SQL queries. The $N numbers
	// are preserved from the original code; we append the building as an
	// extra AND-clause when needed (no positional conflict).
	var subFilter string
	var subFilterArgs []interface{}
	if req.SubcontractID > 0 {
		subFilter = "subcontract_id = $1"
		subFilterArgs = []interface{}{req.SubcontractID}
	} else {
		subFilter = "subcontract_id IS NULL AND project_id = $1"
		subFilterArgs = []interface{}{projectID}
	}
	if req.BuildingID > 0 {
		// Append as last positional arg — callers concatenate $2,$3,$4
		// (period_from, period_to, tenant_id) after this filter, so we must
		// stay at $1. Instead, inline the building_id as a raw int64 (safe —
		// validated above).
		subFilter = fmt.Sprintf("%s AND building_id = %d", subFilter, req.BuildingID)
	}

	// Check there are signed KS-2 acts for this period
	var periodAmount float64
	err = h.db.QueryRow(fmt.Sprintf(`
		SELECT COALESCE(SUM(amount_total), 0)
		FROM construction_act
		WHERE %s AND act_type = 'ks2' AND state = 'signed'
		  AND period_from >= $2::date AND period_to <= $3::date
		  AND tenant_id = $4
	`, subFilter), append(subFilterArgs, req.PeriodFrom, req.PeriodTo, tenantID)...).Scan(&periodAmount)
	if err != nil || periodAmount == 0 {
		response.BadRequest(c, "Tanlangan davr uchun imzolangan KS-2 aktlar topilmadi")
		return
	}

	// Cumulative from start of construction
	var cumulFromStart float64
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COALESCE(SUM(amount_total), 0)
		FROM construction_act
		WHERE %s AND act_type = 'ks2' AND state = 'signed' AND tenant_id = $2
	`, subFilter), append(subFilterArgs, tenantID)...).Scan(&cumulFromStart)

	// Cumulative from start of year
	periodYear := req.PeriodFrom[:4] // Extract year
	yearStart := periodYear + "-01-01"
	var cumulFromYearStart float64
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COALESCE(SUM(amount_total), 0)
		FROM construction_act
		WHERE %s AND act_type = 'ks2' AND state = 'signed'
		  AND period_from >= $2::date AND tenant_id = $3
	`, subFilter), append(subFilterArgs, yearStart, tenantID)...).Scan(&cumulFromYearStart)

	cumulPrevPeriod := cumulFromStart - periodAmount

	// VAT
	var vatPct float64 = 12
	vatAmount := periodAmount * vatPct / 100
	totalWithVat := periodAmount + vatAmount

	// Auto-generate name
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE project_id = $1 AND tenant_id = $2 AND act_type = 'ks3'`,
		projectID, tenantID).Scan(&count)
	name := fmt.Sprintf("KS3-%03d", count+1)

	var actNumber int
	if req.SubcontractID > 0 {
		h.db.QueryRow(`SELECT COALESCE(MAX(act_number), 0) + 1 FROM construction_act WHERE subcontract_id = $1 AND act_type = 'ks3' AND tenant_id = $2`,
			req.SubcontractID, tenantID).Scan(&actNumber)
	} else {
		h.db.QueryRow(`SELECT COALESCE(MAX(act_number), 0) + 1 FROM construction_act WHERE subcontract_id IS NULL AND act_type = 'ks3' AND project_id = $1 AND tenant_id = $2`,
			projectID, tenantID).Scan(&actNumber)
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create KS-3")
		return
	}
	defer tx.Rollback()

	var ks3ID int64
	err = tx.QueryRow(`
		INSERT INTO construction_act (
			tenant_id, project_id, subcontract_id, building_id, name, act_type,
			period_from, period_to, amount_total, vat_pct, vat_amount, amount_total_with_vat,
			currency, state, created_by, created_date, updated_date,
			act_number, cumul_from_start, cumul_from_year_start, cumul_previous_period,
			smr_amount, equipment_amount, other_amount
		) VALUES ($1, $2, $3, $4, $5, 'ks3', $6, $7, $8, $9, $10, $11,
			'UZS', 'draft', $12, NOW(), NOW(),
			$13, $14, $15, $16,
			$8, 0, 0
		)
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(req.SubcontractID), nullInt64FromVal(req.BuildingID), name,
		req.PeriodFrom, req.PeriodTo, periodAmount, vatPct, vatAmount, totalWithVat,
		userID,
		actNumber, cumulFromStart, cumulFromYearStart, cumulPrevPeriod,
	).Scan(&ks3ID)
	if err != nil {
		h.log.Error("Failed to create Forma 3", "error", err)
		response.InternalError(c, "Failed to create Forma 3")
		return
	}

	// Copy lines from all signed KS-2 in the period. When a specific
	// building is requested, only copy lines from KS-2 acts that belong to
	// that building (using the building_id column added in migration 328).
	var copyQuery string
	var copyArgs []interface{}
	buildingClause := ""
	if req.BuildingID > 0 {
		// Safe: BuildingID is int64 already validated above.
		buildingClause = fmt.Sprintf(" AND a.building_id = %d", req.BuildingID)
	}
	if req.SubcontractID > 0 {
		copyQuery = `
			INSERT INTO construction_act_line (act_id, wbs_id, estimate_line_id, name, uom, quantity, unit_rate, total_amount, sort_order, qty_smeta, note)
			SELECT $1, al.wbs_id, al.estimate_line_id, al.name, al.uom, al.quantity, al.unit_rate, al.total_amount, al.sort_order, al.qty_smeta, al.note
			FROM construction_act_line al
			JOIN construction_act a ON a.id = al.act_id
			WHERE a.subcontract_id = $2 AND a.act_type = 'ks2' AND a.state = 'signed'
			  AND a.period_from >= $3::date AND a.period_to <= $4::date
			  AND a.tenant_id = $5` + buildingClause + `
			ORDER BY al.sort_order`
		copyArgs = []interface{}{ks3ID, req.SubcontractID, req.PeriodFrom, req.PeriodTo, tenantID}
	} else {
		copyQuery = `
			INSERT INTO construction_act_line (act_id, wbs_id, estimate_line_id, name, uom, quantity, unit_rate, total_amount, sort_order, qty_smeta, note)
			SELECT $1, al.wbs_id, al.estimate_line_id, al.name, al.uom, al.quantity, al.unit_rate, al.total_amount, al.sort_order, al.qty_smeta, al.note
			FROM construction_act_line al
			JOIN construction_act a ON a.id = al.act_id
			WHERE a.subcontract_id IS NULL AND a.project_id = $2 AND a.act_type = 'ks2' AND a.state = 'signed'
			  AND a.period_from >= $3::date AND a.period_to <= $4::date
			  AND a.tenant_id = $5` + buildingClause + `
			ORDER BY al.sort_order`
		copyArgs = []interface{}{ks3ID, projectID, req.PeriodFrom, req.PeriodTo, tenantID}
	}
	_, err = tx.Exec(copyQuery, copyArgs...)
	if err != nil {
		h.log.Error("Failed to copy lines to KS-3", "error", err)
		response.InternalError(c, "Failed to create KS-3 lines")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit KS-3", "error", err)
		response.InternalError(c, "Failed to create KS-3")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "act",
		fmt.Sprintf("Forma 3 yaratildi: %s (%.0f so'm)", name, periodAmount), "Act", ks3ID)

	response.Created(c, map[string]interface{}{
		"id":                    ks3ID,
		"name":                  name,
		"amount_total":          periodAmount,
		"vat_amount":            vatAmount,
		"amount_total_with_vat": totalWithVat,
		"cumul_from_start":      cumulFromStart,
		"cumul_from_year_start": cumulFromYearStart,
		"cumul_previous_period": cumulPrevPeriod,
		"message":               "Forma 3 generated successfully",
	})
}

// GenerateKS3FromKS2 creates a KS-3 act from an approved KS-2 (legacy, kept for backward compatibility)
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
	var ks2BuildingID sql.NullInt64

	err = h.db.QueryRow(`
		SELECT project_id, COALESCE(subcontract_id, 0), state, act_type, period_from, period_to, amount_total, building_id
		FROM construction_act WHERE id = $1 AND tenant_id = $2
	`, ks2ID, tenantID).Scan(&projectID, &subcontractID, &ks2State, &ks2Type, &periodFrom, &periodTo, &ks2Amount, &ks2BuildingID)
	if err != nil {
		response.NotFound(c, "Forma 2 act not found")
		return
	}

	if ks2Type != "ks2" {
		response.BadRequest(c, "Source act must be KS-2 type")
		return
	}
	if ks2State != "approved" && ks2State != "signed" {
		response.BadRequest(c, "Forma 2 must be approved or signed before generating Forma 3")
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

	// Calculate cumulative totals
	var cumulFromStart float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(amount_total), 0) FROM construction_act
		WHERE subcontract_id = $1 AND act_type = 'ks2' AND state IN ('approved', 'signed') AND tenant_id = $2
	`, subcontractID, tenantID).Scan(&cumulFromStart)

	vatAmount := ks2Amount * 0.12
	totalWithVat := ks2Amount + vatAmount

	var ks3ID int64
	err = tx.QueryRow(`
		INSERT INTO construction_act (
			tenant_id, project_id, subcontract_id, building_id, name, act_type,
			period_from, period_to, amount_total, vat_pct, vat_amount, amount_total_with_vat,
			currency, state, ks2_source_id, created_by, created_date, updated_date,
			cumul_from_start, cumul_previous_period, smr_amount
		) VALUES ($1, $2, $3, $4, $5, 'ks3', $6, $7, $8, 12, $9, $10,
			'UZS', 'draft', $11, $12, NOW(), NOW(),
			$13, $14, $8
		)
		RETURNING id
	`, tenantID, projectID, nullInt64FromVal(subcontractID), ks2BuildingID, name,
		periodFrom, periodTo, ks2Amount, vatAmount, totalWithVat, ks2ID, userID,
		cumulFromStart, cumulFromStart-ks2Amount,
	).Scan(&ks3ID)
	if err != nil {
		h.log.Error("Failed to create Forma 3", "error", err)
		response.InternalError(c, "Failed to create Forma 3")
		return
	}

	// Copy lines from KS-2
	_, err = tx.Exec(`
		INSERT INTO construction_act_line (act_id, wbs_id, estimate_line_id, name, uom, quantity, unit_rate, total_amount, sort_order, qty_smeta, note)
		SELECT $1, wbs_id, estimate_line_id, name, uom, quantity, unit_rate, total_amount, sort_order, qty_smeta, note
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
		fmt.Sprintf("Forma 3 yaratildi: %s (Forma 2 dan)", name), "Act", ks3ID)

	response.Created(c, map[string]interface{}{
		"id":                    ks3ID,
		"name":                  name,
		"amount_total":          ks2Amount,
		"vat_amount":            vatAmount,
		"amount_total_with_vat": totalWithVat,
		"cumul_from_start":      cumulFromStart,
		"message":               "Forma 3 generated from Forma 2 successfully",
	})
}

// recalculateForma3 recalculates cumulative fields on existing Forma 3 for a subcontract
func (h *Handler) recalculateForma3(tenantID uuid.UUID, projectID int64, subcontractID int64) {
	// Get all KS-3 acts for this subcontract
	rows, err := h.db.Query(`
		SELECT id, period_from, period_to FROM construction_act
		WHERE subcontract_id = $1 AND act_type = 'ks3' AND state != 'cancelled' AND tenant_id = $2
		ORDER BY created_date ASC
	`, subcontractID, tenantID)
	if err != nil {
		return
	}
	defer rows.Close()

	var cumulFromStart float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(amount_total), 0) FROM construction_act
		WHERE subcontract_id = $1 AND act_type = 'ks2' AND state = 'signed' AND tenant_id = $2
	`, subcontractID, tenantID).Scan(&cumulFromStart)

	for rows.Next() {
		var ks3ID int64
		var pfrom, pto sql.NullTime
		if rows.Scan(&ks3ID, &pfrom, &pto) != nil {
			continue
		}

		// Period amount from signed KS-2
		var periodAmt float64
		if pfrom.Valid && pto.Valid {
			h.db.QueryRow(`
				SELECT COALESCE(SUM(amount_total), 0) FROM construction_act
				WHERE subcontract_id = $1 AND act_type = 'ks2' AND state = 'signed'
				  AND period_from >= $2 AND period_to <= $3 AND tenant_id = $4
			`, subcontractID, pfrom.Time, pto.Time, tenantID).Scan(&periodAmt)
		}

		// Year start cumulative
		var cumulYear float64
		if pfrom.Valid {
			yearStart := fmt.Sprintf("%d-01-01", pfrom.Time.Year())
			h.db.QueryRow(`
				SELECT COALESCE(SUM(amount_total), 0) FROM construction_act
				WHERE subcontract_id = $1 AND act_type = 'ks2' AND state = 'signed'
				  AND period_from >= $2::date AND tenant_id = $3
			`, subcontractID, yearStart, tenantID).Scan(&cumulYear)
		}

		vatAmt := periodAmt * 0.12
		h.db.Exec(`
			UPDATE construction_act SET
				amount_total = $1, vat_amount = $2, amount_total_with_vat = $3,
				cumul_from_start = $4, cumul_from_year_start = $5,
				cumul_previous_period = $6, smr_amount = $1,
				updated_date = NOW()
			WHERE id = $7
		`, periodAmt, vatAmt, periodAmt+vatAmt,
			cumulFromStart, cumulYear, cumulFromStart-periodAmt,
			ks3ID)
	}
}

// ExportActDocument exports an act as PDF or XLSX
func (h *Handler) ExportActDocument(c *gin.Context) {
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

	format := c.DefaultQuery("format", "pdf")

	// Get act details
	var actType string
	var projectID int64
	err = h.db.QueryRow(`SELECT act_type, project_id FROM construction_act WHERE id = $1 AND tenant_id = $2`,
		actID, tenantID).Scan(&actType, &projectID)
	if err != nil {
		response.NotFound(c, "Act not found")
		return
	}

	// Get project info
	var projectName, projectAddress, clientName string
	h.db.QueryRow(`SELECT COALESCE(name, ''), COALESCE(address, ''), COALESCE(client_name, '') FROM construction_projects WHERE id = $1`,
		projectID).Scan(&projectName, &projectAddress, &clientName)

	if format == "xlsx" {
		// Server-side XLSX generation matching reference industry format.
		var (
			xlsxBytes []byte
			xlsxErr   error
		)
		switch actType {
		case "ks2":
			xlsxBytes, xlsxErr = h.GenerateForma2XLSXBytes(actID, tenantID)
		case "ks3":
			xlsxBytes, xlsxErr = h.GenerateForma3XLSXBytes(actID, tenantID)
		case "hidden_work":
			xlsxBytes, xlsxErr = h.GenerateForma19XLSXBytes(actID, tenantID)
		default:
			// Fallback: surface JSON metadata so the frontend can still render
			// unsupported act types locally.
			response.Success(c, map[string]interface{}{
				"format":       "xlsx",
				"project_name": projectName,
				"address":      projectAddress,
				"client_name":  clientName,
				"act_type":     actType,
				"act_id":       actID,
			})
			return
		}
		if xlsxErr != nil {
			h.log.Error("Failed to generate XLSX", "error", xlsxErr, "actType", actType, "actID", actID)
			response.InternalError(c, "Failed to generate XLSX")
			return
		}
		filename := fmt.Sprintf("act_%s_%d.xlsx", actType, actID)
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
		c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", xlsxBytes)
		return
	}

	// Generate PDF. Forma 2 / 3 / 19 have regulated layouts with their
	// own renderers. Everything else (acceptance, defect, plus any
	// future ad-hoc types) falls through to the generic renderer so the
	// PDF button works for them too instead of returning a 400.
	var htmlContent string
	switch actType {
	case "ks2":
		htmlContent = h.renderForma2HTML(actID, tenantID, projectName, projectAddress, clientName)
	case "ks3":
		htmlContent = h.renderForma3HTML(actID, tenantID, projectName, projectAddress, clientName)
	case "hidden_work":
		htmlContent = h.renderForma19HTML(actID, tenantID, projectName, projectAddress, clientName)
	default:
		htmlContent = h.renderGenericActHTML(actID, tenantID, actType, projectName, projectAddress, clientName)
	}

	pdfBytes, pdfErr := htmlToPDF(htmlContent)
	if pdfErr != nil {
		h.log.Error("Failed to generate PDF", "error", pdfErr)
		response.InternalError(c, "Failed to generate PDF")
		return
	}

	filename := fmt.Sprintf("act_%s_%d.pdf", actType, actID)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Data(http.StatusOK, "application/pdf", pdfBytes)
}

// DeleteConstructionAct deletes a draft act
func (h *Handler) UpdateActLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid act ID")
		return
	}
	lineID, err := strconv.ParseInt(c.Param("lineId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid line ID")
		return
	}

	// Check act is draft
	var state, actType string
	var projectID int64
	err = h.db.QueryRow(`SELECT state, act_type, project_id FROM construction_act WHERE id = $1 AND tenant_id = $2`,
		actID, tenantID).Scan(&state, &actType, &projectID)
	if err != nil {
		response.NotFound(c, "Act not found")
		return
	}
	if state != "draft" {
		response.BadRequest(c, "Can only edit lines in draft acts")
		return
	}

	var req struct {
		QtyPeriod *float64 `json:"qty_period"`
		Note      *string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}

	// Get current line
	var qtySmeta, unitRate float64
	err = h.db.QueryRow(`SELECT COALESCE(qty_smeta, 0), COALESCE(unit_rate, 0) FROM construction_act_line WHERE id = $1 AND act_id = $2`,
		lineID, actID).Scan(&qtySmeta, &unitRate)
	if err != nil {
		response.NotFound(c, "Line not found")
		return
	}

	// Validate qty_period <= qty_smeta
	if req.QtyPeriod != nil && qtySmeta > 0 && *req.QtyPeriod > qtySmeta {
		response.BadRequest(c, "Miqdor smeta miqdoridan oshmasligi kerak")
		return
	}

	// Update line
	if req.QtyPeriod != nil {
		lineTotal := *req.QtyPeriod * unitRate
		_, err = h.db.Exec(`UPDATE construction_act_line SET quantity = $1, total_amount = $2 WHERE id = $3 AND act_id = $4`,
			*req.QtyPeriod, lineTotal, lineID, actID)
		if err != nil {
			h.log.Error("Failed to update act line quantity", "error", err)
			response.InternalError(c, "Failed to update line")
			return
		}
	}
	if req.Note != nil {
		_, err = h.db.Exec(`UPDATE construction_act_line SET note = $1 WHERE id = $2 AND act_id = $3`,
			*req.Note, lineID, actID)
		if err != nil {
			h.log.Error("Failed to update act line note", "error", err)
			response.InternalError(c, "Failed to update line")
			return
		}
	}

	// Recalculate act totals
	var totalAmt float64
	h.db.QueryRow(`SELECT COALESCE(SUM(total_amount), 0) FROM construction_act_line WHERE act_id = $1`, actID).Scan(&totalAmt)
	var vatPct float64
	h.db.QueryRow(`SELECT COALESCE(vat_pct, 12) FROM construction_act WHERE id = $1`, actID).Scan(&vatPct)
	vatAmt := totalAmt * vatPct / 100
	totalWithVat := totalAmt + vatAmt
	h.db.Exec(`UPDATE construction_act SET amount_total = $1, vat_amount = $2, amount_total_with_vat = $3 WHERE id = $4`,
		totalAmt, vatAmt, totalWithVat, actID)

	response.Success(c, map[string]interface{}{"message": "Line updated"})
}

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

	if state == "approved" || state == "signed" || state == "cancelled" {
		response.BadRequest(c, "Tasdiqlangan, imzolangan yoki bekor qilingan aktlarni o'chirib bo'lmaydi")
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

// nullFloat64Val converts sql.NullFloat64 to interface{} (nil if not valid)
func nullFloat64Val(n sql.NullFloat64) interface{} {
	if n.Valid {
		return math.Round(n.Float64*100) / 100
	}
	return nil
}

// projectHasRequiredClientDetails checks if a construction project has all required
// client details filled in for generating KS-2 and KS-3 documents.
func (h *Handler) projectHasRequiredClientDetails(tenantID uuid.UUID, projectID int64) bool {
	var clientName, clientStir, clientBankName, clientBankAccount, clientMfo, clientAddress sql.NullString
	err := h.db.QueryRow(`
		SELECT client_name, client_stir, client_bank_name, client_bank_account, client_mfo, client_address
		FROM construction_projects WHERE id = $1 AND tenant_id = $2
	`, projectID, tenantID).Scan(&clientName, &clientStir, &clientBankName, &clientBankAccount, &clientMfo, &clientAddress)
	if err != nil {
		return false
	}
	return clientName.Valid && strings.TrimSpace(clientName.String) != "" &&
		clientStir.Valid && strings.TrimSpace(clientStir.String) != "" &&
		clientBankName.Valid && strings.TrimSpace(clientBankName.String) != "" &&
		clientBankAccount.Valid && strings.TrimSpace(clientBankAccount.String) != "" &&
		clientMfo.Valid && strings.TrimSpace(clientMfo.String) != "" &&
		clientAddress.Valid && strings.TrimSpace(clientAddress.String) != ""
}

// saveProjectClientDetails persists client requisites to the construction project.
// Called when KS-2/KS-3 forms are created with inline client details.
func (h *Handler) saveProjectClientDetails(tenantID uuid.UUID, projectID int64,
	clientName, clientPhone, clientAddress, clientBankName, clientBankAccount,
	clientMFO, clientSTIR, clientOKONH, contractNumber, objectFullName,
	clientDirectorName, clientChiefAccountantName string) {

	_, err := h.db.Exec(`
		UPDATE construction_projects SET
			client_name = COALESCE(NULLIF($1, ''), client_name),
			client_phone = COALESCE(NULLIF($2, ''), client_phone),
			client_address = COALESCE(NULLIF($3, ''), client_address),
			client_bank_name = COALESCE(NULLIF($4, ''), client_bank_name),
			client_bank_account = COALESCE(NULLIF($5, ''), client_bank_account),
			client_mfo = COALESCE(NULLIF($6, ''), client_mfo),
			client_stir = COALESCE(NULLIF($7, ''), client_stir),
			client_okonh = COALESCE(NULLIF($8, ''), client_okonh),
			contract_number = COALESCE(NULLIF($9, ''), contract_number),
			object_full_name = COALESCE(NULLIF($10, ''), object_full_name),
			client_director_name = COALESCE(NULLIF($11, ''), client_director_name),
			client_chief_accountant_name = COALESCE(NULLIF($12, ''), client_chief_accountant_name)
		WHERE id = $13 AND tenant_id = $14
	`, clientName, clientPhone, clientAddress, clientBankName, clientBankAccount,
		clientMFO, clientSTIR, clientOKONH, contractNumber, objectFullName,
		clientDirectorName, clientChiefAccountantName, projectID, tenantID)
	if err != nil {
		h.log.Error("Failed to save project client details", "error", err, "projectID", projectID)
	}
}
