package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION SUBCONTRACT HANDLERS
// =====================================================

// ListSubcontracts returns subcontracts for a project
func (h *Handler) ListSubcontracts(c *gin.Context) {
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

	stateFilter := c.Query("state")

	query := `
		SELECT s.id, s.name, s.project_id, s.partner_name, s.work_description,
		       COALESCE(s.contract_number, ''),
		       s.amount, s.currency, s.start_date, s.end_date,
		       s.retention_pct, s.state, s.rating,
		       s.contact_person, s.contact_phone, s.notes,
		       COALESCE(s.address, ''), COALESCE(s.phone, ''),
		       COALESCE(s.bank_name, ''), COALESCE(s.bank_account, ''),
		       COALESCE(s.mfo, ''), COALESCE(s.stir, ''), COALESCE(s.okonh, ''),
		       COALESCE(s.director_name, ''), COALESCE(s.chief_accountant_name, ''),
		       s.created_by, s.created_date, s.updated_date,
		       COALESCE(acts.completed_amount, 0) as completed_amount,
		       COALESCE(acts.paid_amount, 0) as paid_amount
		FROM construction_subcontract s
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(CASE WHEN a.state = 'approved' AND a.act_type = 'ks2' THEN a.amount_total ELSE 0 END), 0) as completed_amount,
			       COALESCE(SUM(CASE WHEN a.state = 'approved' AND a.act_type = 'ks3' THEN a.amount_total ELSE 0 END), 0) as paid_amount
			FROM construction_act a WHERE a.subcontract_id = s.id
		) acts ON true
		WHERE s.project_id = $1 AND s.tenant_id = $2
	`
	args := []interface{}{projectID, tenantID}
	argCount := 2

	if stateFilter != "" {
		argCount++
		query += fmt.Sprintf(" AND s.state = $%d", argCount)
		args = append(args, stateFilter)
	}

	query += " ORDER BY s.created_date DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list subcontracts", "error", err)
		response.InternalError(c, "Failed to list subcontracts")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id, projectIDVal int64
		var name string
		var partnerName sql.NullString
		var contractNumber string
		var workDescription, notes sql.NullString
		var amount float64
		var currency string
		var startDate, endDate sql.NullTime
		var retentionPct, rating float64
		var contactPerson, contactPhone sql.NullString
		var address, phone, bankName, bankAccount, mfo, stir, okonh, directorName, chiefAccName string
		var createdBy uuid.NullUUID
		var createdDate, updatedDate time.Time
		var state string
		var completedAmount, paidAmount float64

		if err := rows.Scan(
			&id, &name, &projectIDVal, &partnerName, &workDescription,
			&contractNumber,
			&amount, &currency, &startDate, &endDate,
			&retentionPct, &state, &rating,
			&contactPerson, &contactPhone, &notes,
			&address, &phone, &bankName, &bankAccount,
			&mfo, &stir, &okonh, &directorName, &chiefAccName,
			&createdBy, &createdDate, &updatedDate,
			&completedAmount, &paidAmount,
		); err != nil {
			h.log.Error("Failed to scan subcontract", "error", err)
			continue
		}

		// Get linked WBS IDs
		wbsIDs := []int64{}
		wbsRows, err := h.db.Query(
			`SELECT wbs_id FROM construction_subcontract_wbs WHERE subcontract_id = $1`, id)
		if err == nil {
			defer wbsRows.Close()
			for wbsRows.Next() {
				var wbsID int64
				if wbsRows.Scan(&wbsID) == nil {
					wbsIDs = append(wbsIDs, wbsID)
				}
			}
		}

		// Get linked building IDs
		buildingIDs := []int64{}
		bldRows, err := h.db.Query(
			`SELECT building_id FROM construction_subcontract_buildings WHERE subcontract_id = $1`, id)
		if err == nil {
			defer bldRows.Close()
			for bldRows.Next() {
				var bldID int64
				if bldRows.Scan(&bldID) == nil {
					buildingIDs = append(buildingIDs, bldID)
				}
			}
		}

		var progressPct float64
		if amount > 0 {
			progressPct = (completedAmount / amount) * 100
			if progressPct > 100 {
				progressPct = 100
			}
		}

		outstandingAmount := completedAmount - paidAmount
		if outstandingAmount < 0 {
			outstandingAmount = 0
		}

		items = append(items, map[string]interface{}{
			"id":                    id,
			"name":                  name,
			"project_id":            projectIDVal,
			"partner_name":          nullStringVal(partnerName),
			"contract_number":       contractNumber,
			"work_description":      nullStringVal(workDescription),
			"amount":                amount,
			"currency":              currency,
			"start_date":            nullTimeVal(startDate),
			"end_date":              nullTimeVal(endDate),
			"retention_pct":         retentionPct,
			"state":                 state,
			"rating":                rating,
			"contact_person":        nullStringVal(contactPerson),
			"contact_phone":         nullStringVal(contactPhone),
			"notes":                 nullStringVal(notes),
			"address":               address,
			"phone":                 phone,
			"bank_name":             bankName,
			"bank_account":          bankAccount,
			"mfo":                   mfo,
			"stir":                  stir,
			"okonh":                 okonh,
			"director_name":         directorName,
			"chief_accountant_name": chiefAccName,
			"created_date":          createdDate,
			"updated_date":          updatedDate,
			"wbs_ids":               wbsIDs,
			"building_ids":          buildingIDs,
			"completed_amount":      completedAmount,
			"paid_amount":           paidAmount,
			"outstanding_amount":    outstandingAmount,
			"progress_pct":          round2(progressPct),
		})
	}

	response.Success(c, items)
}

// CreateSubcontract creates a new subcontract
func (h *Handler) CreateSubcontract(c *gin.Context) {
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
		ContractNumber  string  `json:"contract_number"`
		PartnerName     string  `json:"partner_name"`
		WorkDescription string  `json:"work_description"`
		Amount          float64 `json:"amount"`
		Currency        string  `json:"currency"`
		StartDate       string  `json:"start_date"`
		EndDate         string  `json:"end_date"`
		RetentionPct    float64 `json:"retention_pct"`
		ContactPerson   string  `json:"contact_person"`
		ContactPhone    string  `json:"contact_phone"`
		Notes           string  `json:"notes"`
		WBSIDs          []int64 `json:"wbs_ids"`
		BuildingIDs     []int64 `json:"building_ids"`
		// Forma 2/3 identity block
		Address             string `json:"address"`
		Phone               string `json:"phone"`
		BankName            string `json:"bank_name"`
		BankAccount         string `json:"bank_account"`
		MFO                 string `json:"mfo"`
		STIR                string `json:"stir"`
		OKONH               string `json:"okonh"`
		DirectorName        string `json:"director_name"`
		ChiefAccountantName string `json:"chief_accountant_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Auto-generate name
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_subcontract WHERE project_id = $1 AND tenant_id = $2`,
		projectID, tenantID).Scan(&count)
	name := fmt.Sprintf("SUB-%03d", count+1)

	currency := req.Currency
	if currency == "" {
		currency = "UZS"
	}

	var startDate, endDate interface{}
	if req.StartDate != "" {
		startDate = req.StartDate
	}
	if req.EndDate != "" {
		endDate = req.EndDate
	}

	var id int64
	err = h.db.QueryRow(`
		INSERT INTO construction_subcontract (
			tenant_id, project_id, partner_name, name, work_description,
			amount, currency, start_date, end_date, retention_pct,
			state, rating, contact_person, contact_phone, notes,
			address, phone, bank_name, bank_account, mfo, stir, okonh,
			director_name, chief_accountant_name,
			created_by, created_date, updated_date, contract_number
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'draft', 0, $11, $12, $13,
			$14, $15, $16, $17, $18, $19, $20, $21, $22,
			$23, NOW(), NOW(), $24)
		RETURNING id
	`, tenantID, projectID, nullStringFromVal(req.PartnerName), name, nullStringFromVal(req.WorkDescription),
		req.Amount, currency, startDate, endDate, req.RetentionPct,
		nullStringFromVal(req.ContactPerson), nullStringFromVal(req.ContactPhone),
		nullStringFromVal(req.Notes),
		nullStringFromVal(req.Address), nullStringFromVal(req.Phone),
		nullStringFromVal(req.BankName), nullStringFromVal(req.BankAccount),
		nullStringFromVal(req.MFO), nullStringFromVal(req.STIR), nullStringFromVal(req.OKONH),
		nullStringFromVal(req.DirectorName), nullStringFromVal(req.ChiefAccountantName),
		userID, nullStringFromVal(req.ContractNumber),
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create subcontract", "error", err)
		response.InternalError(c, "Failed to create subcontract")
		return
	}

	// Link WBS items
	for _, wbsID := range req.WBSIDs {
		h.db.Exec(`INSERT INTO construction_subcontract_wbs (subcontract_id, wbs_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			id, wbsID)
	}

	// Link buildings
	for _, bldID := range req.BuildingIDs {
		h.db.Exec(`INSERT INTO construction_subcontract_buildings (subcontract_id, building_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			id, bldID)
	}

	h.logConstructionActivity(tenantID, projectID, userID, "team",
		fmt.Sprintf("Pudratchi shartnomasi yaratildi: %s", name), "Subcontract", id)

	response.Created(c, map[string]interface{}{
		"id":      id,
		"name":    name,
		"message": "Subcontract created successfully",
	})
}

// GetSubcontract returns a single subcontract with details
func (h *Handler) GetSubcontract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var id, projectIDVal int64
	var name, state, currency string
	var partnerNameVal sql.NullString
	var contractNumberVal string
	var workDescription, notes, contactPerson, contactPhone sql.NullString
	var address, phone, bankName, bankAccount, mfo, stir, okonh, directorName, chiefAccName sql.NullString
	var amount, retentionPct, rating float64
	var startDate, endDate sql.NullTime
	var createdBy uuid.NullUUID
	var createdDate, updatedDate time.Time

	err = h.db.QueryRow(`
		SELECT s.id, s.name, s.project_id, s.partner_name, s.work_description,
		       COALESCE(s.contract_number, ''),
		       s.amount, s.currency, s.start_date, s.end_date,
		       s.retention_pct, s.state, s.rating,
		       s.contact_person, s.contact_phone, s.notes,
		       s.address, s.phone, s.bank_name, s.bank_account,
		       s.mfo, s.stir, s.okonh, s.director_name, s.chief_accountant_name,
		       s.created_by, s.created_date, s.updated_date
		FROM construction_subcontract s
		WHERE s.id = $1 AND s.tenant_id = $2
	`, subID, tenantID).Scan(
		&id, &name, &projectIDVal, &partnerNameVal, &workDescription,
		&contractNumberVal,
		&amount, &currency, &startDate, &endDate,
		&retentionPct, &state, &rating,
		&contactPerson, &contactPhone, &notes,
		&address, &phone, &bankName, &bankAccount,
		&mfo, &stir, &okonh, &directorName, &chiefAccName,
		&createdBy, &createdDate, &updatedDate,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Subcontract not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get subcontract", "error", err)
		response.InternalError(c, "Failed to get subcontract")
		return
	}

	// Get WBS IDs
	wbsIDs := []int64{}
	wbsRows, _ := h.db.Query(`SELECT wbs_id FROM construction_subcontract_wbs WHERE subcontract_id = $1`, id)
	if wbsRows != nil {
		defer wbsRows.Close()
		for wbsRows.Next() {
			var wbsID int64
			if wbsRows.Scan(&wbsID) == nil {
				wbsIDs = append(wbsIDs, wbsID)
			}
		}
	}

	// Get building IDs
	buildingIDs := []int64{}
	bldRows, _ := h.db.Query(`SELECT building_id FROM construction_subcontract_buildings WHERE subcontract_id = $1`, id)
	if bldRows != nil {
		defer bldRows.Close()
		for bldRows.Next() {
			var bldID int64
			if bldRows.Scan(&bldID) == nil {
				buildingIDs = append(buildingIDs, bldID)
			}
		}
	}

	response.Success(c, map[string]interface{}{
		"id":                    id,
		"name":                  name,
		"project_id":            projectIDVal,
		"partner_name":          nullStringVal(partnerNameVal),
		"contract_number":       contractNumberVal,
		"work_description":      nullStringVal(workDescription),
		"amount":                amount,
		"currency":              currency,
		"start_date":            nullTimeVal(startDate),
		"end_date":              nullTimeVal(endDate),
		"retention_pct":         retentionPct,
		"state":                 state,
		"rating":                rating,
		"contact_person":        nullStringVal(contactPerson),
		"contact_phone":         nullStringVal(contactPhone),
		"notes":                 nullStringVal(notes),
		"address":               nullStringVal(address),
		"phone":                 nullStringVal(phone),
		"bank_name":             nullStringVal(bankName),
		"bank_account":          nullStringVal(bankAccount),
		"mfo":                   nullStringVal(mfo),
		"stir":                  nullStringVal(stir),
		"okonh":                 nullStringVal(okonh),
		"director_name":         nullStringVal(directorName),
		"chief_accountant_name": nullStringVal(chiefAccName),
		"created_date":          createdDate,
		"updated_date":          updatedDate,
		"wbs_ids":               wbsIDs,
		"building_ids":          buildingIDs,
	})
}

// UpdateSubcontract updates a subcontract
func (h *Handler) UpdateSubcontract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var projectID int64
	err = h.db.QueryRow(`SELECT project_id FROM construction_subcontract WHERE id = $1 AND tenant_id = $2`,
		subID, tenantID).Scan(&projectID)
	if err != nil {
		response.NotFound(c, "Subcontract not found")
		return
	}

	var req struct {
		ContractNumber  *string  `json:"contract_number"`
		PartnerName     *string  `json:"partner_name"`
		WorkDescription *string  `json:"work_description"`
		Amount          *float64 `json:"amount"`
		Currency        *string  `json:"currency"`
		StartDate       *string  `json:"start_date"`
		EndDate         *string  `json:"end_date"`
		RetentionPct    *float64 `json:"retention_pct"`
		Rating          *float64 `json:"rating"`
		ContactPerson   *string  `json:"contact_person"`
		ContactPhone    *string  `json:"contact_phone"`
		Notes           *string  `json:"notes"`
		WBSIDs          []int64  `json:"wbs_ids"`
		BuildingIDs     []int64  `json:"building_ids"`
		// Forma 2/3 identity block
		Address             *string `json:"address"`
		Phone               *string `json:"phone"`
		BankName            *string `json:"bank_name"`
		BankAccount         *string `json:"bank_account"`
		MFO                 *string `json:"mfo"`
		STIR                *string `json:"stir"`
		OKONH               *string `json:"okonh"`
		DirectorName        *string `json:"director_name"`
		ChiefAccountantName *string `json:"chief_accountant_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if req.ContractNumber != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("contract_number = $%d", argCount))
		args = append(args, nullStringFromVal(*req.ContractNumber))
	}
	if req.PartnerName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("partner_name = $%d", argCount))
		args = append(args, nullStringFromVal(*req.PartnerName))
	}
	if req.WorkDescription != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("work_description = $%d", argCount))
		args = append(args, nullStringFromVal(*req.WorkDescription))
	}
	if req.Amount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("amount = $%d", argCount))
		args = append(args, *req.Amount)
	}
	if req.Currency != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("currency = $%d", argCount))
		args = append(args, *req.Currency)
	}
	if req.StartDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("start_date = $%d", argCount))
		if *req.StartDate == "" {
			args = append(args, nil)
		} else {
			args = append(args, *req.StartDate)
		}
	}
	if req.EndDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("end_date = $%d", argCount))
		if *req.EndDate == "" {
			args = append(args, nil)
		} else {
			args = append(args, *req.EndDate)
		}
	}
	if req.RetentionPct != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("retention_pct = $%d", argCount))
		args = append(args, *req.RetentionPct)
	}
	if req.Rating != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("rating = $%d", argCount))
		args = append(args, *req.Rating)
	}
	if req.ContactPerson != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("contact_person = $%d", argCount))
		args = append(args, nullStringFromVal(*req.ContactPerson))
	}
	if req.ContactPhone != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("contact_phone = $%d", argCount))
		args = append(args, nullStringFromVal(*req.ContactPhone))
	}
	if req.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, nullStringFromVal(*req.Notes))
	}
	if req.Address != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("address = $%d", argCount))
		args = append(args, nullStringFromVal(*req.Address))
	}
	if req.Phone != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("phone = $%d", argCount))
		args = append(args, nullStringFromVal(*req.Phone))
	}
	if req.BankName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("bank_name = $%d", argCount))
		args = append(args, nullStringFromVal(*req.BankName))
	}
	if req.BankAccount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("bank_account = $%d", argCount))
		args = append(args, nullStringFromVal(*req.BankAccount))
	}
	if req.MFO != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("mfo = $%d", argCount))
		args = append(args, nullStringFromVal(*req.MFO))
	}
	if req.STIR != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("stir = $%d", argCount))
		args = append(args, nullStringFromVal(*req.STIR))
	}
	if req.OKONH != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("okonh = $%d", argCount))
		args = append(args, nullStringFromVal(*req.OKONH))
	}
	if req.DirectorName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("director_name = $%d", argCount))
		args = append(args, nullStringFromVal(*req.DirectorName))
	}
	if req.ChiefAccountantName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("chief_accountant_name = $%d", argCount))
		args = append(args, nullStringFromVal(*req.ChiefAccountantName))
	}

	if len(updates) == 0 && req.WBSIDs == nil && req.BuildingIDs == nil {
		response.BadRequest(c, "No fields to update")
		return
	}

	if len(updates) > 0 {
		argCount++
		updates = append(updates, fmt.Sprintf("updated_date = $%d", argCount))
		args = append(args, time.Now())

		argCount++
		args = append(args, subID)
		argCount++
		args = append(args, tenantID)

		query := fmt.Sprintf(
			"UPDATE construction_subcontract SET %s WHERE id = $%d AND tenant_id = $%d",
			strings.Join(updates, ", "), argCount-1, argCount,
		)

		_, err := h.db.Exec(query, args...)
		if err != nil {
			h.log.Error("Failed to update subcontract", "error", err)
			response.InternalError(c, "Failed to update subcontract")
			return
		}
	}

	// Update WBS links if provided
	if req.WBSIDs != nil {
		h.db.Exec(`DELETE FROM construction_subcontract_wbs WHERE subcontract_id = $1`, subID)
		for _, wbsID := range req.WBSIDs {
			h.db.Exec(`INSERT INTO construction_subcontract_wbs (subcontract_id, wbs_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				subID, wbsID)
		}
	}

	// Update building links if provided
	if req.BuildingIDs != nil {
		h.db.Exec(`DELETE FROM construction_subcontract_buildings WHERE subcontract_id = $1`, subID)
		for _, bldID := range req.BuildingIDs {
			h.db.Exec(`INSERT INTO construction_subcontract_buildings (subcontract_id, building_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				subID, bldID)
		}
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "team",
		"Pudratchi shartnomasi yangilandi", "Subcontract", subID)

	response.Success(c, map[string]interface{}{
		"id":      subID,
		"message": "Subcontract updated successfully",
	})
}

// DeleteSubcontract deletes a subcontract
func (h *Handler) DeleteSubcontract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var projectID int64
	err = h.db.QueryRow(`SELECT project_id FROM construction_subcontract WHERE id = $1 AND tenant_id = $2`,
		subID, tenantID).Scan(&projectID)
	if err != nil {
		response.NotFound(c, "Subcontract not found")
		return
	}

	// Check for linked acts
	var actCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM construction_act WHERE subcontract_id = $1`, subID).Scan(&actCount)
	if actCount > 0 {
		response.BadRequest(c, "Cannot delete subcontract with linked acts")
		return
	}

	h.db.Exec(`DELETE FROM construction_subcontract_wbs WHERE subcontract_id = $1`, subID)
	h.db.Exec(`DELETE FROM construction_subcontract_buildings WHERE subcontract_id = $1`, subID)
	_, err = h.db.Exec(`DELETE FROM construction_subcontract WHERE id = $1 AND tenant_id = $2`, subID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete subcontract", "error", err)
		response.InternalError(c, "Failed to delete subcontract")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "team",
		"Pudratchi shartnomasi o'chirildi", "Subcontract", subID)

	response.Success(c, map[string]interface{}{
		"message": "Subcontract deleted successfully",
	})
}

// UpdateSubcontractState changes a subcontract's state
func (h *Handler) UpdateSubcontractState(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		State string `json:"state" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "State is required")
		return
	}

	validStates := map[string]bool{
		"draft": true, "active": true, "completed": true, "terminated": true,
	}
	if !validStates[req.State] {
		response.BadRequest(c, "Invalid state. Must be: draft, active, completed, or terminated")
		return
	}

	var projectID int64
	var currentState string
	err = h.db.QueryRow(`SELECT project_id, state FROM construction_subcontract WHERE id = $1 AND tenant_id = $2`,
		subID, tenantID).Scan(&projectID, &currentState)
	if err != nil {
		response.NotFound(c, "Subcontract not found")
		return
	}

	// Validate state transitions
	validTransitions := map[string][]string{
		"draft":      {"active", "terminated"},
		"active":     {"completed", "terminated"},
		"completed":  {},
		"terminated": {"draft"},
	}
	allowed := false
	for _, s := range validTransitions[currentState] {
		if s == req.State {
			allowed = true
			break
		}
	}
	if !allowed {
		response.BadRequest(c, fmt.Sprintf("Cannot transition from '%s' to '%s'", currentState, req.State))
		return
	}

	_, err = h.db.Exec(`UPDATE construction_subcontract SET state = $1, updated_date = NOW() WHERE id = $2 AND tenant_id = $3`,
		req.State, subID, tenantID)
	if err != nil {
		h.log.Error("Failed to update subcontract state", "error", err)
		response.InternalError(c, "Failed to update state")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logConstructionActivity(tenantID, projectID, userID, "team",
		fmt.Sprintf("Pudratchi holati o'zgartirildi: %s → %s", currentState, req.State), "Subcontract", subID)

	response.Success(c, map[string]interface{}{
		"id":      subID,
		"state":   req.State,
		"message": "State updated successfully",
	})
}

// =====================================================
// SUBCONTRACT FILE ATTACHMENTS
// =====================================================

type subcontractFile struct {
	ID          int    `json:"id"`
	FileID      string `json:"file_id"`
	FileURL     string `json:"file_url"`
	Filename    string `json:"filename"`
	FileSize    int64  `json:"file_size"`
	MimeType    string `json:"mime_type"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	CreatedBy   string `json:"created_by"`
}

// ListSubcontractFiles returns the documents attached to a subcontractor.
func (h *Handler) ListSubcontractFiles(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid subcontractor ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, file_id, file_url, filename, file_size, mime_type, description, created_at, created_by
		FROM subcontract_files
		WHERE tenant_id = $1 AND subcontract_id = $2
		ORDER BY created_at DESC`, tenantID, subID)
	if err != nil {
		h.log.Error("Failed to list subcontract files", "error", err)
		response.InternalError(c, "Failed to list files")
		return
	}
	defer rows.Close()

	files := []subcontractFile{}
	for rows.Next() {
		var f subcontractFile
		var createdAt time.Time
		if err := rows.Scan(&f.ID, &f.FileID, &f.FileURL, &f.Filename, &f.FileSize, &f.MimeType, &f.Description, &createdAt, &f.CreatedBy); err != nil {
			continue
		}
		f.CreatedAt = createdAt.Format(time.RFC3339)
		files = append(files, f)
	}
	response.Success(c, files)
}

// CreateSubcontractFile stores a file reference (after the raw file is uploaded
// via POST /files/upload) against a subcontractor.
func (h *Handler) CreateSubcontractFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid subcontractor ID")
		return
	}

	var input struct {
		FileID      string `json:"file_id" binding:"required"`
		FileURL     string `json:"file_url" binding:"required"`
		Filename    string `json:"filename" binding:"required"`
		FileSize    int64  `json:"file_size"`
		MimeType    string `json:"mime_type"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	createdBy := ""
	if email, exists := c.Get("user_email"); exists {
		if s, ok := email.(string); ok {
			createdBy = s
		}
	}

	var id int
	err = h.db.QueryRow(`
		INSERT INTO subcontract_files (tenant_id, subcontract_id, file_id, file_url, filename, file_size, mime_type, description, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, tenantID, subID, input.FileID, input.FileURL, input.Filename, input.FileSize, input.MimeType, input.Description, createdBy).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create subcontract file", "error", err)
		response.InternalError(c, "Failed to save file")
		return
	}

	response.Created(c, gin.H{
		"id":          id,
		"file_id":     input.FileID,
		"file_url":    input.FileURL,
		"filename":    input.Filename,
		"file_size":   input.FileSize,
		"mime_type":   input.MimeType,
		"description": input.Description,
		"created_by":  createdBy,
	})
}

// DeleteSubcontractFile removes a subcontractor file reference.
func (h *Handler) DeleteSubcontractFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	fileID, err := strconv.ParseInt(c.Param("fileId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid file ID")
		return
	}

	result, err := h.db.Exec(`DELETE FROM subcontract_files WHERE id = $1 AND tenant_id = $2`, fileID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete subcontract file", "error", err)
		response.InternalError(c, "Failed to delete file")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		response.NotFound(c, "File not found")
		return
	}
	response.Success(c, gin.H{"message": "File deleted"})
}

// Helper for nullable time in maps
func nullTimeVal(nt sql.NullTime) interface{} {
	if nt.Valid {
		return nt.Time.Format("2006-01-02")
	}
	return nil
}
