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

// ─── Material Reservation DTOs ───────────────────────────────────────────────

type CreateMaterialReservationRequest struct {
	ProjectID   int64   `json:"project_id" binding:"required"`
	StageID     int64   `json:"stage_id" binding:"required"`
	SubstageID  int64   `json:"substage_id" binding:"required"`
	ProductID   string  `json:"product_id" binding:"required"`
	WarehouseID string  `json:"warehouse_id"`
	Quantity    float64 `json:"quantity" binding:"required"`
	Unit        string  `json:"unit"`
	UnitCost    float64 `json:"unit_cost"`
	Notes       string  `json:"notes"`
}

// ─── List Reservations (for a project or globally for inventory tab) ─────────

func (h *Handler) ListMaterialReservations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectIDStr := c.Query("project_id")
	substageIDStr := c.Query("substage_id")
	status := c.Query("status")

	query := `
		SELECT mr.id, mr.project_id, mr.stage_id, mr.substage_id,
			   mr.product_id, COALESCE(p.name,''), mr.warehouse_id,
			   mr.quantity, COALESCE(mr.unit,''), mr.unit_cost, mr.total_cost,
			   mr.status, mr.requested_by, COALESCE(TRIM(CONCAT(req_u.first_name,' ',req_u.last_name)),''),
			   mr.approved_by, COALESCE(TRIM(CONCAT(app_u.first_name,' ',app_u.last_name)),''),
			   mr.notes, mr.created_at, mr.updated_at, mr.approved_at,
			   COALESCE(cp.name,''), COALESCE(cs.name,''), COALESCE(css.name,'')
		FROM material_reservations mr
		LEFT JOIN products p ON p.id = mr.product_id
		LEFT JOIN users req_u ON req_u.id = mr.requested_by
		LEFT JOIN users app_u ON app_u.id = mr.approved_by
		LEFT JOIN construction_projects cp ON cp.id = mr.project_id
		LEFT JOIN construction_stages cs ON cs.id = mr.stage_id
		LEFT JOIN construction_sub_stages css ON css.id = mr.substage_id
		WHERE mr.tenant_id = $1 AND mr.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argIdx := 2

	if projectIDStr != "" {
		query += fmt.Sprintf(" AND mr.project_id = $%d", argIdx)
		pid, _ := strconv.ParseInt(projectIDStr, 10, 64)
		args = append(args, pid)
		argIdx++
	}
	if substageIDStr != "" {
		query += fmt.Sprintf(" AND mr.substage_id = $%d", argIdx)
		sid, _ := strconv.ParseInt(substageIDStr, 10, 64)
		args = append(args, sid)
		argIdx++
	}
	if status != "" {
		query += fmt.Sprintf(" AND mr.status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}

	query += " ORDER BY mr.created_at DESC LIMIT 200"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list material reservations", "error", err)
		response.InternalError(c, "Failed to list reservations")
		return
	}
	defer rows.Close()

	var reservations []map[string]interface{}
	for rows.Next() {
		var (
			id, productID                            string
			projectID, stageID, substageID           int64
			warehouseID                              *string
			quantity, unitCost, totalCost             float64
			unit, status, notes                      string
			requestedBy, approvedBy                  *string
			requestedByName, approvedByName          string
			productName, projectName, stageName, subName string
			createdAt, updatedAt                     time.Time
			approvedAt                               *time.Time
		)
		if err := rows.Scan(
			&id, &projectID, &stageID, &substageID,
			&productID, &productName, &warehouseID,
			&quantity, &unit, &unitCost, &totalCost,
			&status, &requestedBy, &requestedByName,
			&approvedBy, &approvedByName,
			&notes, &createdAt, &updatedAt, &approvedAt,
			&projectName, &stageName, &subName,
		); err != nil {
			h.log.Error("Scan reservation row", "error", err)
			continue
		}

		r := map[string]interface{}{
			"id":                id,
			"project_id":        projectID,
			"stage_id":          stageID,
			"substage_id":       substageID,
			"product_id":        productID,
			"product_name":      productName,
			"warehouse_id":      warehouseID,
			"quantity":           quantity,
			"unit":               unit,
			"unit_cost":          unitCost,
			"total_cost":         totalCost,
			"status":             status,
			"requested_by":       requestedBy,
			"requested_by_name":  requestedByName,
			"approved_by":        approvedBy,
			"approved_by_name":   approvedByName,
			"notes":              notes,
			"created_at":         createdAt,
			"updated_at":         updatedAt,
			"project_name":       projectName,
			"stage_name":         stageName,
			"substage_name":      subName,
		}
		if approvedAt != nil {
			r["approved_at"] = approvedAt
		}
		reservations = append(reservations, r)
	}
	if reservations == nil {
		reservations = []map[string]interface{}{}
	}
	response.Success(c, reservations)
}

// ─── Create Reservation ──────────────────────────────────────────────────────

func (h *Handler) CreateMaterialReservation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	var req CreateMaterialReservationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	productID, err := uuid.Parse(req.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}

	// Look up product details if unit/cost not provided
	unit := req.Unit
	unitCost := req.UnitCost
	if unit == "" || unitCost == 0 {
		var pUnit sql.NullString
		var pCost float64
		err := h.db.QueryRow(`
			SELECT COALESCE(u.name,''), COALESCE(p.cost_price, 0)
			FROM products p
			LEFT JOIN units u ON u.id = p.unit_id
			WHERE p.id = $1 AND p.tenant_id = $2
		`, productID, tenantID).Scan(&pUnit, &pCost)
		if err == nil {
			if unit == "" && pUnit.Valid {
				unit = pUnit.String
			}
			if unitCost == 0 {
				unitCost = pCost
			}
		}
	}

	// Try to get unit cost from inventory if still 0
	if unitCost == 0 {
		h.db.QueryRow(`
			SELECT COALESCE(unit_cost, 0) FROM inventory
			WHERE product_id = $1 AND tenant_id = $2 AND quantity_on_hand > 0
			ORDER BY updated_at DESC LIMIT 1
		`, productID, tenantID).Scan(&unitCost)
	}

	totalCost := req.Quantity * unitCost

	var warehouseID *uuid.UUID
	if req.WarehouseID != "" {
		wid, err := uuid.Parse(req.WarehouseID)
		if err == nil {
			warehouseID = &wid
		}
	}

	// If no warehouse specified, pick the default one
	if warehouseID == nil {
		var wid uuid.UUID
		h.db.QueryRow(`
			SELECT warehouse_id FROM inventory
			WHERE product_id = $1 AND tenant_id = $2 AND quantity_on_hand > 0
			ORDER BY quantity_on_hand DESC LIMIT 1
		`, productID, tenantID).Scan(&wid)
		if wid != uuid.Nil {
			warehouseID = &wid
		}
	}

	id := uuid.New()
	now := time.Now()

	_, err = h.db.Exec(`
		INSERT INTO material_reservations (
			id, tenant_id, organization_id, project_id, stage_id, substage_id,
			product_id, warehouse_id, quantity, unit, unit_cost, total_cost,
			status, requested_by, notes, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending',$13,$14,$15,$15)
	`, id, tenantID, organizationID, req.ProjectID, req.StageID, req.SubstageID,
		productID, warehouseID, req.Quantity, unit, unitCost, totalCost,
		userID, req.Notes, now)

	if err != nil {
		h.log.Error("Failed to create material reservation", "error", err)
		response.InternalError(c, "Failed to create reservation")
		return
	}

	// Update inventory quantity_reserved
	if warehouseID != nil {
		h.db.Exec(`
			UPDATE inventory SET quantity_reserved = quantity_reserved + $1, updated_at = $2
			WHERE product_id = $3 AND warehouse_id = $4 AND tenant_id = $5
		`, req.Quantity, now, productID, warehouseID, tenantID)
	}

	// Notify inventory users
	h.createNotificationForAllTenantUsers(tenantID, "low_stock", map[string]interface{}{
		"reservation_id": id.String(),
		"product_id":     productID.String(),
	}, fmt.Sprintf("Reservation request for product (qty: %.2f)", req.Quantity), "")

	response.Created(c, map[string]interface{}{
		"id":         id,
		"status":     "pending",
		"quantity":   req.Quantity,
		"unit":       unit,
		"unit_cost":  unitCost,
		"total_cost": totalCost,
	})
}

// ─── Approve Reservation ─────────────────────────────────────────────────────

func (h *Handler) ApproveMaterialReservation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	resID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid reservation ID")
		return
	}

	// Fetch reservation details
	var (
		productID, rTenantID uuid.UUID
		warehouseID          *uuid.UUID
		projectID, stageID   int64
		quantity, unitCost, totalCost float64
		unit, status         string
		requestedBy          *uuid.UUID
	)
	err = h.db.QueryRow(`
		SELECT product_id, tenant_id, warehouse_id, project_id, stage_id,
			   quantity, unit_cost, total_cost, COALESCE(unit,''), status, requested_by
		FROM material_reservations
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, resID, tenantID).Scan(
		&productID, &rTenantID, &warehouseID, &projectID, &stageID,
		&quantity, &unitCost, &totalCost, &unit, &status, &requestedBy,
	)
	if err != nil {
		response.NotFound(c, "Reservation not found")
		return
	}
	if status != "pending" {
		response.BadRequest(c, "Only pending reservations can be approved")
		return
	}

	// Check available inventory
	if warehouseID != nil {
		var available float64
		h.db.QueryRow(`
			SELECT COALESCE(quantity_on_hand - quantity_reserved, 0)
			FROM inventory WHERE product_id = $1 AND warehouse_id = $2 AND tenant_id = $3
		`, productID, warehouseID, tenantID).Scan(&available)
		// We already reserved, so available might be negative — we check on_hand instead
		var onHand float64
		h.db.QueryRow(`
			SELECT COALESCE(quantity_on_hand, 0)
			FROM inventory WHERE product_id = $1 AND warehouse_id = $2 AND tenant_id = $3
		`, productID, warehouseID, tenantID).Scan(&onHand)
		if onHand < quantity {
			response.BadRequest(c, fmt.Sprintf("Insufficient stock. Available: %.2f, Requested: %.2f", onHand, quantity))
			return
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	now := time.Now()

	// 1. Update reservation status
	tx.Exec(`
		UPDATE material_reservations
		SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $2
		WHERE id = $3
	`, userID, now, resID)

	// 2. Deduct from inventory (quantity_on_hand) and release reserved
	if warehouseID != nil {
		tx.Exec(`
			UPDATE inventory
			SET quantity_on_hand = quantity_on_hand - $1,
				quantity_reserved = quantity_reserved - $1,
				updated_at = $2
			WHERE product_id = $3 AND warehouse_id = $4 AND tenant_id = $5
		`, quantity, now, productID, warehouseID, tenantID)

		// Record inventory movement
		tx.Exec(`
			INSERT INTO inventory_movements (
				id, tenant_id, product_id, warehouse_id, movement_type,
				quantity, unit_cost, reference_type, reference_id,
				notes, created_at
			) VALUES ($1,$2,$3,$4,'out',$5,$6,'material_reservation',$7,'Material reservation approved',$8)
		`, uuid.New(), tenantID, productID, warehouseID, quantity, unitCost, resID.String(), now)
	}

	// 3. Create construction expense line
	var expenseLineID int64
	err = tx.QueryRow(`
		INSERT INTO construction_expense_lines (
			tenant_id, project_id, stage_id, product_id,
			description, quantity, unit_price, amount,
			status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'approved',$9,$9)
		RETURNING id
	`, tenantID, projectID, stageID, productID,
		fmt.Sprintf("Material reservation #%s", resID.String()[:8]),
		quantity, unitCost, totalCost, now).Scan(&expenseLineID)

	if err != nil {
		h.log.Error("Failed to create expense line", "error", err)
		// Continue without expense line — not fatal
	}

	// 4. Create journal entry (Debit: WIP 0810, Credit: Inventory 1000/product inventory account)
	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}

	wipAcct := h.getConstructionMappedAccount(tenantID, orgIDPtr, "wip_0810", "tugallanmagan qurilish", "0810")
	// Get inventory/credit account from product or fallback
	var creditAcct uuid.UUID
	h.db.QueryRow(`SELECT COALESCE(inventory_account_id, '00000000-0000-0000-0000-000000000000') FROM products WHERE id = $1`, productID).Scan(&creditAcct)
	if creditAcct == uuid.Nil {
		creditAcct = findAccount(h.db, tenantID, orgIDPtr, "material", "1010")
	}

	var journalEntryID uuid.UUID
	if wipAcct != uuid.Nil && creditAcct != uuid.Nil {
		journalID := h.ensureConstructionJournal(tenantID, orgIDPtr)
		if journalID != uuid.Nil {
			tx.Exec(`SAVEPOINT sp_je_reservation`)

			nextNum := h.getNextJournalNumber(tx, journalID)
			journalEntryID = uuid.New()
			entryNumber := fmt.Sprintf("MR%06d", nextNum)

			_, jeErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number,
					entry_date, description, source_type, source_id,
					status, total_debit, total_credit, created_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,'material_reservation',NULL,'posted',$8,$8,$9,$9)
			`, journalEntryID, tenantID, organizationID, journalID, entryNumber,
				now, fmt.Sprintf("Material Reservation: %s", resID.String()[:8]),
				totalCost, now)

			if jeErr != nil {
				h.log.Error("JE creation failed", "error", jeErr)
				tx.Exec(`ROLLBACK TO sp_je_reservation`)
			} else {
				// Debit line — WIP
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,$5,0,1,$6)`,
					uuid.New(), journalEntryID, wipAcct,
					fmt.Sprintf("Material reservation %s", resID.String()[:8]), totalCost, now)
				// Credit line — Inventory
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,0,$5,2,$6)`,
					uuid.New(), journalEntryID, creditAcct,
					fmt.Sprintf("Material reservation %s", resID.String()[:8]), totalCost, now)

				// Update account balances
				tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalCost, now, wipAcct)
				tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalCost, now, creditAcct)

				tx.Exec(`RELEASE sp_je_reservation`)
			}
		}
	}

	// 5. Update reservation with expense & journal references
	if expenseLineID > 0 || journalEntryID != uuid.Nil {
		tx.Exec(`
			UPDATE material_reservations
			SET expense_line_id = $1, journal_entry_id = $2
			WHERE id = $3
		`, expenseLineID, journalEntryID, resID)
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit reservation approval", "error", err)
		response.InternalError(c, "Failed to approve reservation")
		return
	}

	// Notify requester
	if requestedBy != nil {
		h.createTranslatedNotification(tenantID, *requestedBy, "expense_approved",
			map[string]interface{}{"reservation_id": resID.String()},
			resID.String()[:8], fmt.Sprintf("%.2f", totalCost))
	}

	response.Success(c, gin.H{"message": "Reservation approved", "id": resID})
}

// ─── Reject Reservation ──────────────────────────────────────────────────────

func (h *Handler) RejectMaterialReservation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	resID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid reservation ID")
		return
	}

	var status string
	var quantity float64
	var productID uuid.UUID
	var warehouseID *uuid.UUID
	var requestedBy *uuid.UUID
	err = h.db.QueryRow(`
		SELECT status, quantity, product_id, warehouse_id, requested_by
		FROM material_reservations
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, resID, tenantID).Scan(&status, &quantity, &productID, &warehouseID, &requestedBy)
	if err != nil {
		response.NotFound(c, "Reservation not found")
		return
	}
	if status != "pending" {
		response.BadRequest(c, "Only pending reservations can be rejected")
		return
	}

	now := time.Now()

	// Update status
	h.db.Exec(`
		UPDATE material_reservations
		SET status = 'rejected', approved_by = $1, updated_at = $2
		WHERE id = $3
	`, userID, now, resID)

	// Release reserved quantity
	if warehouseID != nil {
		h.db.Exec(`
			UPDATE inventory
			SET quantity_reserved = GREATEST(quantity_reserved - $1, 0), updated_at = $2
			WHERE product_id = $3 AND warehouse_id = $4 AND tenant_id = $5
		`, quantity, now, productID, warehouseID, tenantID)
	}

	// Notify requester
	if requestedBy != nil {
		h.createNotification(tenantID, *requestedBy, "info",
			"Reservation Rejected",
			fmt.Sprintf("Your material reservation %s has been rejected", resID.String()[:8]),
			map[string]interface{}{"reservation_id": resID.String()})
	}

	response.Success(c, gin.H{"message": "Reservation rejected"})
}

// ─── Delete Reservation ──────────────────────────────────────────────────────

func (h *Handler) DeleteMaterialReservation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	resID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid reservation ID")
		return
	}

	var status string
	var quantity float64
	var productID uuid.UUID
	var warehouseID *uuid.UUID
	err = h.db.QueryRow(`
		SELECT status, quantity, product_id, warehouse_id
		FROM material_reservations
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, resID, tenantID).Scan(&status, &quantity, &productID, &warehouseID)
	if err != nil {
		response.NotFound(c, "Reservation not found")
		return
	}
	if status == "approved" {
		response.BadRequest(c, "Cannot delete approved reservations")
		return
	}

	now := time.Now()
	h.db.Exec(`UPDATE material_reservations SET deleted_at = $1 WHERE id = $2`, now, resID)

	// Release reserved quantity if was pending
	if status == "pending" && warehouseID != nil {
		h.db.Exec(`
			UPDATE inventory
			SET quantity_reserved = GREATEST(quantity_reserved - $1, 0), updated_at = $2
			WHERE product_id = $3 AND warehouse_id = $4 AND tenant_id = $5
		`, quantity, now, productID, warehouseID, tenantID)
	}

	response.Success(c, gin.H{"message": "Reservation deleted"})
}
