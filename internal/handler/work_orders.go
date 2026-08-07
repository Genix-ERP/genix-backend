package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
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
// WORK ORDER HANDLERS
// =====================================================

// ListWorkOrders returns work orders with filters
func (h *Handler) ListWorkOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Org scoping: the resolver header is authoritative (audit §2.10 — the
	// query param alone let a caller see the whole tenant by omitting it);
	// the query param stays as a back-compat fallback.
	organizationID := ""
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		organizationID = orgID.String()
	} else {
		organizationID = c.Query("organization_id")
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	offset := (page - 1) * limit

	// Parse filters
	productionOrderID := c.Query("production_order_id")
	workCenterID := c.Query("work_center_id")
	status := c.Query("status")

	query := `
		SELECT wo.id, wo.tenant_id, wo.organization_id, wo.production_order_id,
			   COALESCE(wo.code, '') as work_order_number,
			   COALESCE(wo.name, '') as name, wo.sequence,
			   wo.operation_id as bom_operation_id, '' as operation_name,
			   wo.work_center_id, COALESCE(wc.name, '') as work_center_name,
			   wo.quantity_to_produce, wo.quantity_produced,
			   CAST(COALESCE(wo.planned_duration_hours, 0) * 60 AS INTEGER) as expected_duration_minutes,
			   CAST(COALESCE(wo.setup_time_hours, 0) * 60 AS INTEGER) as setup_time_minutes,
			   CAST(COALESCE(wo.actual_duration_hours, 0) * 60 AS INTEGER) as actual_duration_minutes,
			   wo.scheduled_start, wo.scheduled_end, wo.actual_start, wo.actual_end,
			   wo.status, wo.assigned_to as operator_id, '' as operator_name,
			   false as quality_check_required,
			   NULL::boolean as quality_check_passed,
			   wo.instructions, wo.notes, wo.created_at,
			   COALESCE(po.code, '') as production_order_number,
			   COALESCE(p.name, '') as product_name
		FROM work_orders wo
		LEFT JOIN production_orders po ON wo.production_order_id = po.id
		LEFT JOIN products p ON po.product_id = p.id
		LEFT JOIN work_centers wc ON wo.work_center_id = wc.id
		WHERE wo.tenant_id = $1 AND wo.deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	argIdx := 2

	if organizationID != "" {
		query += fmt.Sprintf(" AND wo.organization_id = $%d", argIdx)
		args = append(args, organizationID)
		argIdx++
	}
	if productionOrderID != "" {
		query += fmt.Sprintf(" AND wo.production_order_id = $%d", argIdx)
		args = append(args, productionOrderID)
		argIdx++
	}
	if workCenterID != "" {
		query += fmt.Sprintf(" AND wo.work_center_id = $%d", argIdx)
		args = append(args, workCenterID)
		argIdx++
	}
	if status != "" {
		query += fmt.Sprintf(" AND wo.status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY wo.production_order_id, wo.sequence LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list work orders", "error", err)
		response.InternalError(c, "Failed to list work orders")
		return
	}
	defer rows.Close()

	var workOrders []entity.WorkOrderResponse
	for rows.Next() {
		var wo entity.WorkOrderResponse
		var orgID, bomOpID, wcID, operatorID sql.NullString
		var scheduledStart, scheduledEnd, actualStart, actualEnd sql.NullTime
		var qualityPassed sql.NullBool
		var instructions, notes sql.NullString

		err := rows.Scan(
			&wo.ID, &tenantID, &orgID, &wo.ProductionOrderID,
			&wo.WorkOrderNumber, &wo.Name, &wo.Sequence,
			&bomOpID, &wo.OperationName,
			&wcID, &wo.WorkCenterName,
			&wo.QuantityToProduce, &wo.QuantityProduced,
			&wo.ExpectedDurationMinutes, &wo.SetupTimeMinutes, &wo.ActualDurationMinutes,
			&scheduledStart, &scheduledEnd, &actualStart, &actualEnd,
			&wo.Status, &operatorID, &wo.OperatorName,
			&wo.QualityCheckRequired, &qualityPassed,
			&instructions, &notes, &wo.CreatedAt,
			&wo.ProductionOrderNumber, &wo.ProductName,
		)
		if err != nil {
			continue
		}

		if wcID.Valid {
			id, _ := uuid.Parse(wcID.String)
			wo.WorkCenterID = &id
		}
		if operatorID.Valid {
			id, _ := uuid.Parse(operatorID.String)
			wo.OperatorID = &id
		}
		if scheduledStart.Valid {
			wo.ScheduledStart = &scheduledStart.Time
		}
		if scheduledEnd.Valid {
			wo.ScheduledEnd = &scheduledEnd.Time
		}
		if actualStart.Valid {
			wo.ActualStart = &actualStart.Time
		}
		if actualEnd.Valid {
			wo.ActualEnd = &actualEnd.Time
		}
		if qualityPassed.Valid {
			wo.QualityCheckPassed = &qualityPassed.Bool
		}
		if instructions.Valid {
			wo.Instructions = &instructions.String
		}
		if notes.Valid {
			wo.Notes = &notes.String
		}

		// Calculate progress
		if wo.QuantityToProduce > 0 {
			wo.Progress = (wo.QuantityProduced / wo.QuantityToProduce) * 100
		}
		wo.StatusLabel = getWorkOrderStatusLabel(wo.Status)

		workOrders = append(workOrders, wo)
	}

	// Get total count
	var total int
	countQuery := `SELECT COUNT(*) FROM work_orders WHERE tenant_id = $1 AND deleted_at IS NULL`
	h.db.QueryRow(countQuery, tenantID).Scan(&total)

	pagination := entity.NewPagination(page, limit)
	pagination.Total = total
	pagination.Calculate(total)
	response.SuccessWithPagination(c, workOrders, pagination)
}

// GetWorkOrder returns a single work order
func (h *Handler) GetWorkOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	query := `
		SELECT wo.id, wo.tenant_id, wo.organization_id, wo.production_order_id,
			   COALESCE(wo.code, '') as work_order_number,
			   COALESCE(wo.name, '') as name, wo.sequence,
			   wo.operation_id as bom_operation_id, '' as operation_name,
			   wo.work_center_id, COALESCE(wc.name, '') as work_center_name,
			   wo.quantity_to_produce, wo.quantity_produced,
			   CAST(COALESCE(wo.planned_duration_hours, 0) * 60 AS INTEGER) as expected_duration_minutes,
			   CAST(COALESCE(wo.setup_time_hours, 0) * 60 AS INTEGER) as setup_time_minutes,
			   CAST(COALESCE(wo.actual_duration_hours, 0) * 60 AS INTEGER) as actual_duration_minutes,
			   wo.scheduled_start, wo.scheduled_end, wo.actual_start, wo.actual_end,
			   wo.status, wo.assigned_to as operator_id, '' as operator_name,
			   false as quality_check_required,
			   NULL::boolean as quality_check_passed,
			   wo.instructions, wo.notes, wo.created_at,
			   COALESCE(po.code, '') as production_order_number,
			   COALESCE(p.name, '') as product_name
		FROM work_orders wo
		LEFT JOIN production_orders po ON wo.production_order_id = po.id
		LEFT JOIN products p ON po.product_id = p.id
		LEFT JOIN work_centers wc ON wo.work_center_id = wc.id
		WHERE wo.id = $1 AND wo.tenant_id = $2 AND wo.deleted_at IS NULL
	`

	var wo entity.WorkOrderResponse
	var orgID, bomOpID, wcID, operatorID sql.NullString
	var scheduledStart, scheduledEnd, actualStart, actualEnd sql.NullTime
	var qualityPassed sql.NullBool
	var instructions, notes sql.NullString

	err = h.db.QueryRow(query, woID, tenantID).Scan(
		&wo.ID, &tenantID, &orgID, &wo.ProductionOrderID,
		&wo.WorkOrderNumber, &wo.Name, &wo.Sequence,
		&bomOpID, &wo.OperationName,
		&wcID, &wo.WorkCenterName,
		&wo.QuantityToProduce, &wo.QuantityProduced,
		&wo.ExpectedDurationMinutes, &wo.SetupTimeMinutes, &wo.ActualDurationMinutes,
		&scheduledStart, &scheduledEnd, &actualStart, &actualEnd,
		&wo.Status, &operatorID, &wo.OperatorName,
		&wo.QualityCheckRequired, &qualityPassed,
		&instructions, &notes, &wo.CreatedAt,
		&wo.ProductionOrderNumber, &wo.ProductName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Work order not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get work order", "error", err)
		response.InternalError(c, "Failed to get work order")
		return
	}

	if wcID.Valid {
		id, _ := uuid.Parse(wcID.String)
		wo.WorkCenterID = &id
	}
	if operatorID.Valid {
		id, _ := uuid.Parse(operatorID.String)
		wo.OperatorID = &id
	}
	if scheduledStart.Valid {
		wo.ScheduledStart = &scheduledStart.Time
	}
	if scheduledEnd.Valid {
		wo.ScheduledEnd = &scheduledEnd.Time
	}
	if actualStart.Valid {
		wo.ActualStart = &actualStart.Time
	}
	if actualEnd.Valid {
		wo.ActualEnd = &actualEnd.Time
	}
	if qualityPassed.Valid {
		wo.QualityCheckPassed = &qualityPassed.Bool
	}
	if instructions.Valid {
		wo.Instructions = &instructions.String
	}
	if notes.Valid {
		wo.Notes = &notes.String
	}

	if wo.QuantityToProduce > 0 {
		wo.Progress = (wo.QuantityProduced / wo.QuantityToProduce) * 100
	}
	wo.StatusLabel = getWorkOrderStatusLabel(wo.Status)

	response.Success(c, wo)
}

// StartWorkOrder starts a work order
func (h *Handler) StartWorkOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	var input entity.StartWorkOrderInput
	c.ShouldBindJSON(&input)

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM work_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", woID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Work order not found")
		return
	}

	if currentStatus != "draft" && currentStatus != "ready" && currentStatus != "waiting" && currentStatus != "pending" && currentStatus != "paused" {
		response.BadRequest(c, "Work order cannot be started. Current status: "+currentStatus)
		return
	}

	// Update work order
	now := time.Now()
	operatorID := userID
	if input.OperatorID != "" {
		operatorID, _ = uuid.Parse(input.OperatorID)
	}

	var operatorName string
	h.db.QueryRow("SELECT COALESCE(first_name || ' ' || last_name, email) FROM users WHERE id = $1", operatorID).Scan(&operatorName)

	// Use migration 010 columns: assigned_to, started_by
	_, err = h.db.Exec(`
		UPDATE work_orders
		SET status = 'in_progress', actual_start = $1, assigned_to = $2, started_by = $2
		WHERE id = $3 AND tenant_id = $4
	`, now, operatorID, woID, tenantID)
	if err != nil {
		h.log.Error("Failed to start work order", "error", err)
		response.InternalError(c, "Failed to start work order")
		return
	}

	// Log time start - use migration 010 columns: worker_id, worker_name
	h.db.Exec(`
		INSERT INTO work_order_time_logs (id, tenant_id, work_order_id, start_time, log_type, worker_id, worker_name, notes)
		VALUES ($1, $2, $3, $4, 'work', $5, $6, $7)
	`, uuid.New(), tenantID, woID, now, operatorID, operatorName, input.Notes)

	// Get production_order_id for this work order
	var poID uuid.UUID
	h.db.QueryRow(`SELECT production_order_id FROM work_orders WHERE id = $1 AND tenant_id = $2`, woID, tenantID).Scan(&poID)

	if poID != uuid.Nil {
		// If PO is not yet in_progress, start it (consumes materials)
		var poStatus string
		h.db.QueryRow(`SELECT status FROM production_orders WHERE id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&poStatus)
		if poStatus != "in_progress" {
			h.db.Exec(`UPDATE production_orders SET status = 'in_progress', actual_start = $1, updated_at = $1 WHERE id = $2 AND tenant_id = $3`, now, poID, tenantID)
			// Consume BOM materials if not already consumed
			h.consumeBOMComponents(poID, tenantID, operatorID, now)
		}
	}

	response.Success(c, gin.H{"message": "Work order started", "actual_start": now})
}

// CompleteWorkOrder completes a work order
func (h *Handler) CompleteWorkOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	var input entity.CompleteWorkOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check current status
	var currentStatus string
	var actualStart sql.NullTime
	err = h.db.QueryRow("SELECT status, actual_start FROM work_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", woID, tenantID).Scan(&currentStatus, &actualStart)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Work order not found")
		return
	}

	if currentStatus != "in_progress" {
		response.BadRequest(c, "Work order is not in progress. Current status: "+currentStatus)
		return
	}

	// Calculate actual duration
	now := time.Now()
	var durationMinutes int
	if actualStart.Valid {
		durationMinutes = int(now.Sub(actualStart.Time).Minutes())
	}

	// Update work order - use migration 010 columns: actual_duration_hours, completed_by
	durationHours := float64(durationMinutes) / 60.0
	_, err = h.db.Exec(`
		UPDATE work_orders
		SET status = 'completed', actual_end = $1, quantity_produced = $2,
			quantity_scrapped = $3, actual_duration_hours = $4, completed_by = $5
		WHERE id = $6 AND tenant_id = $7
	`, now, input.QuantityProduced, input.ScrapQuantity, durationHours, userID, woID, tenantID)
	if err != nil {
		h.log.Error("Failed to complete work order", "error", err)
		response.InternalError(c, "Failed to complete work order")
		return
	}

	// Ishbay (B6): operation piece_rate × good output → the
	// production_piecework HR handoff register. UNIQUE(work_order_id,
	// employee_id) makes retries safe; no payroll_entries writes here.
	h.recordPiecework(tenantID, woID, userID, input.QuantityProduced, now)

	// Log time end - use migration 010 columns: duration_hours
	h.db.Exec(`
		UPDATE work_order_time_logs
		SET end_time = $1, duration_hours = $2, notes = $3
		WHERE work_order_id = $4 AND end_time IS NULL
	`, now, durationHours, input.Notes, woID)

	// Update work center utilization
	var wcIDStr sql.NullString
	h.db.QueryRow(`SELECT work_center_id::text FROM work_orders WHERE id = $1`, woID).Scan(&wcIDStr)
	if wcIDStr.Valid && wcIDStr.String != "" {
		workCenterID, parseErr := uuid.Parse(wcIDStr.String)
		if parseErr == nil && workCenterID != uuid.Nil {
			var totalHours float64
			var operatingHours float64
			h.db.QueryRow(`
				SELECT COALESCE(SUM(wo.actual_duration_hours), 0)
				FROM work_orders wo
				WHERE wo.work_center_id = $1 AND wo.tenant_id = $2 AND wo.status IN ('completed', 'done')
					AND wo.deleted_at IS NULL
			`, workCenterID, tenantID).Scan(&totalHours)
			h.db.QueryRow(`
				SELECT COALESCE(working_hours_per_day, 8) FROM work_centers WHERE id = $1 AND tenant_id = $2
			`, workCenterID, tenantID).Scan(&operatingHours)
			if operatingHours > 0 {
				// Utilization = total hours worked / (operating hours per day × 30 days) × 100
				utilization := (totalHours / (operatingHours * 30)) * 100
				if utilization > 100 {
					utilization = 100
				}
				h.db.Exec(`UPDATE work_centers SET current_utilization = $1 WHERE id = $2 AND tenant_id = $3`,
					utilization, workCenterID, tenantID)
			}
		}
	}

	// Get the production order ID and current work order sequence
	var productionOrderID uuid.UUID
	var currentSequence int
	scanErr := h.db.QueryRow(`
		SELECT production_order_id, COALESCE(sequence, 0)
		FROM work_orders WHERE id = $1 AND tenant_id = $2
	`, woID, tenantID).Scan(&productionOrderID, &currentSequence)
	if scanErr != nil {
		h.log.Error("CompleteWorkOrder: failed to get production_order_id", "error", scanErr, "wo_id", woID)
	}
	h.log.Info("CompleteWorkOrder: context", "wo_id", woID, "production_order_id", productionOrderID, "sequence", currentSequence)

	// Find the next work order in sequence
	var nextWoID uuid.UUID
	var nextSequence int
	nextErr := h.db.QueryRow(`
		SELECT id, sequence FROM work_orders
		WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
			AND sequence > $3 AND status IN ('pending', 'ready', 'draft', 'waiting')
		ORDER BY sequence ASC LIMIT 1
	`, productionOrderID, tenantID, currentSequence).Scan(&nextWoID, &nextSequence)

	// Recalculate production order progress_percent from completed work orders
	var totalWOs, completedWOs int
	h.db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status IN ('completed', 'done'))
		FROM work_orders
		WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, productionOrderID, tenantID).Scan(&totalWOs, &completedWOs)
	progressPct := 0.0
	if totalWOs > 0 {
		progressPct = float64(completedWOs) / float64(totalWOs) * 100.0
	}
	h.log.Info("CompleteWorkOrder: progress", "totalWOs", totalWOs, "completedWOs", completedWOs, "progressPct", progressPct, "nextErr", nextErr)

	h.db.Exec(`
		UPDATE production_orders SET progress_percent = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4
	`, progressPct, now, productionOrderID, tenantID)

	if nextErr == nil {
		h.log.Info("CompleteWorkOrder: advancing to next WO", "next_wo_id", nextWoID, "next_sequence", nextSequence)
		// Mark next work order as ready — worker must press Start manually
		h.db.Exec(`
			UPDATE work_orders
			SET status = 'ready', updated_at = $1
			WHERE id = $2 AND tenant_id = $3
		`, now, nextWoID, tenantID)

		// Advance production order stage to next operation
		nextStage := fmt.Sprintf("op_%d", nextSequence)
		h.db.Exec(`
			UPDATE production_orders
			SET current_stage = $1, updated_at = $2
			WHERE id = $3 AND tenant_id = $4
		`, nextStage, now, productionOrderID, tenantID)
	} else {
		// No more work orders — check if all completed
		var incompleteCount int
		h.db.QueryRow(`
			SELECT COUNT(*) FROM work_orders
			WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
				AND status NOT IN ('completed', 'done', 'cancelled')
		`, productionOrderID, tenantID).Scan(&incompleteCount)

		h.log.Info("CompleteWorkOrder: all WOs check", "incompleteCount", incompleteCount)

		if incompleteCount == 0 {
			// All work orders done — use last step's output as final quantity
			var lastWoProduced float64
			h.db.QueryRow(`
				SELECT COALESCE(quantity_produced, 0) FROM work_orders
				WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
					AND status IN ('completed', 'done')
				ORDER BY sequence DESC LIMIT 1
			`, productionOrderID, tenantID).Scan(&lastWoProduced)

			// Total scrap across all steps
			var totalScrapped float64
			h.db.QueryRow(`
				SELECT COALESCE(SUM(quantity_scrapped), 0) FROM work_orders
				WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
			`, productionOrderID, tenantID).Scan(&totalScrapped)

			// Check if this production order uses split output (bulk → packaged products)
			var hasSplitOutput sql.NullBool
			h.db.QueryRow(`SELECT has_split_output FROM production_orders WHERE id = $1 AND tenant_id = $2`,
				productionOrderID, tenantID).Scan(&hasSplitOutput)

			splitEnabled := hasSplitOutput.Valid && hasSplitOutput.Bool
			h.log.Info("CompleteWorkOrder: split output check", "has_split_output_raw", hasSplitOutput, "splitEnabled", splitEnabled, "lastWoProduced", lastWoProduced, "totalScrapped", totalScrapped)

			if splitEnabled {
				// Move to "packaging" status — worker will use CompleteSplitOutput to finalize
				_, execErr := h.db.Exec(`
					UPDATE production_orders
					SET status = 'packaging', current_stage = 'packaging', progress_percent = 95,
					    quantity_produced = $1, good_quantity = $1, reject_quantity = $2,
					    actual_end = $3, updated_at = $3
					WHERE id = $4 AND tenant_id = $5
				`, lastWoProduced, totalScrapped, now, productionOrderID, tenantID)
				if execErr != nil {
					h.log.Error("CompleteWorkOrder: failed to set packaging status", "error", execErr)
				} else {
					h.log.Info("CompleteWorkOrder: set production order to PACKAGING", "po_id", productionOrderID)
				}
			} else {
				h.log.Info("[v2] CompleteWorkOrder: completing PO (non-split)", "po_id", productionOrderID, "lastWoProduced", lastWoProduced)
				var qtyPlannedForPO float64
				h.db.QueryRow(`SELECT quantity_planned FROM production_orders WHERE id = $1 AND tenant_id = $2`,
					productionOrderID, tenantID).Scan(&qtyPlannedForPO)
				shortfallReasonSQL := ""
				shortfallArgs := []interface{}{lastWoProduced, totalScrapped, now, productionOrderID, tenantID}
				if input.ShortfallReason != "" && (lastWoProduced+totalScrapped) < qtyPlannedForPO {
					shortfallReasonSQL = ", shortfall_reason = $6"
					shortfallArgs = append(shortfallArgs, input.ShortfallReason)
				}
				h.db.Exec(`
					UPDATE production_orders
					SET status = 'completed', current_stage = 'done', progress_percent = 100,
					    quantity_produced = $1, good_quantity = $1, reject_quantity = $2,
					    actual_end = $3, updated_at = $3`+shortfallReasonSQL+`
					WHERE id = $4 AND tenant_id = $5
				`, shortfallArgs...)

				// Add finished goods to inventory (last step's good output)
				unitCost := h.receiveFinishedGoods(productionOrderID, tenantID, userID, lastWoProduced, now)
				h.log.Info("[v2] receiveFinishedGoods returned", "unitCost", unitCost, "po_id", productionOrderID)

				// Set material_cost and actual_cost on the production order
				totalCost := unitCost * (lastWoProduced + totalScrapped)
				if totalCost > 0 {
					h.db.Exec(`
						UPDATE production_orders
						SET material_cost = $1, actual_cost = $1, updated_at = $2
						WHERE id = $3 AND tenant_id = $4
					`, totalCost, now, productionOrderID, tenantID)
				}

				// Move scrapped items to scrap warehouse
				var productID uuid.UUID
				var organizationID *uuid.UUID
				var warehouseID *uuid.UUID
				var bomID *uuid.UUID
				h.db.QueryRow(`
					SELECT product_id, organization_id, warehouse_id, bom_id FROM production_orders
					WHERE id = $1 AND tenant_id = $2
				`, productionOrderID, tenantID).Scan(&productID, &organizationID, &warehouseID, &bomID)

				if totalScrapped > 0 {
					h.receiveScrapGoods(productionOrderID, tenantID, userID, productID, organizationID, totalScrapped, unitCost, now)
				}

				// Create journal entries for finished goods (WIP → Finished Goods)
				h.createFinishedGoodsJournalEntry(productionOrderID, tenantID, organizationID, productID, userID, lastWoProduced, unitCost, now)

				// Transfer finished goods to dedicated finished goods warehouse,
				// but only if the BOM did NOT specify a warehouse — when the BOM
				// has a warehouse, the product is already in the correct location.
				if warehouseID != nil {
					var bomHasWarehouse bool
					if bomID != nil {
						var bomWhID uuid.UUID
						if h.db.QueryRow(`SELECT warehouse_id FROM product_boms WHERE id = $1 AND warehouse_id IS NOT NULL`, bomID).Scan(&bomWhID) == nil {
							bomHasWarehouse = true
						}
					}
					if !bomHasWarehouse {
						h.transferToFinishedGoodsWarehouse(productionOrderID, tenantID, organizationID, productID, *warehouseID, userID, lastWoProduced, unitCost, now)
					}
				}

				// Return unused components when produced + scrapped < planned
				var qtyPlanned float64
				h.db.QueryRow(`SELECT quantity_planned FROM production_orders WHERE id = $1 AND tenant_id = $2`,
					productionOrderID, tenantID).Scan(&qtyPlanned)
				h.returnUnusedComponents(productionOrderID, tenantID, bomID, warehouseID, organizationID, userID,
					qtyPlanned, lastWoProduced, totalScrapped, now)
			}
		}
	}

	response.Success(c, gin.H{
		"message":          "Work order completed",
		"actual_end":       now,
		"duration_minutes": durationMinutes,
	})
}

// CreateWorkOrder creates a new work order manually
func (h *Handler) CreateWorkOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateWorkOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	productionOrderID, err := uuid.Parse(input.ProductionOrderID)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	// Verify production order exists
	var poOrgID uuid.UUID
	err = h.db.QueryRow("SELECT organization_id FROM production_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", productionOrderID, tenantID).Scan(&poOrgID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found")
		return
	}

	woID := uuid.New()
	var woCount int
	h.db.QueryRow("SELECT COUNT(*) FROM work_orders WHERE tenant_id = $1", tenantID).Scan(&woCount)
	woNumber := fmt.Sprintf("WO%05d", woCount+1)

	var workCenterID interface{} = nil
	if input.WorkCenterID != "" {
		wcID, _ := uuid.Parse(input.WorkCenterID)
		workCenterID = wcID
	}

	// Use only migration 010 columns
	_, err = h.db.Exec(`
		INSERT INTO work_orders (
			id, tenant_id, production_order_id,
			code, name, sequence,
			work_center_id,
			quantity_to_produce, uom,
			planned_duration_hours, setup_time_hours,
			scheduled_start, scheduled_end,
			status, instructions, notes, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pcs', $9, $10, $11, $12, 'pending', $13, $14, $15)
	`, woID, tenantID, productionOrderID,
		woNumber, input.Name, input.Sequence,
		workCenterID,
		input.QuantityToProduce,
		float64(input.ExpectedDurationMinutes)/60, float64(input.SetupTimeMinutes)/60,
		input.ScheduledStart, input.ScheduledEnd,
		input.Instructions, input.Notes, userID)
	if err != nil {
		h.log.Error("Failed to create work order", "error", err)
		response.InternalError(c, "Failed to create work order")
		return
	}

	response.Created(c, gin.H{
		"id":                woID,
		"work_order_number": woNumber,
		"message":           "Work order created successfully",
	})
}

// RecordWorkOrderTime records time log for a work order
func (h *Handler) RecordWorkOrderTime(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	var input entity.RecordWorkOrderTimeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Verify work order exists
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM work_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", woID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Work order not found")
		return
	}

	// Get user name
	var userName string
	h.db.QueryRow("SELECT COALESCE(first_name || ' ' || last_name, email) FROM users WHERE id = $1", userID).Scan(&userName)

	logID := uuid.New()
	startTime := time.Now()
	if input.StartTime != nil {
		startTime = *input.StartTime
	}

	var endTime interface{} = nil
	var durationHours interface{} = nil
	if input.EndTime != nil {
		endTime = *input.EndTime
		durationHours = input.EndTime.Sub(startTime).Hours()
	}

	// Use migration 010 columns: worker_id, worker_name, duration_hours.
	// employee_id (migration 459) is the payroll-link groundwork: accepted
	// from the input, or resolved from the caller's user → employee link.
	var employeeID interface{}
	if input.EmployeeID != nil && *input.EmployeeID != uuid.Nil {
		employeeID = *input.EmployeeID
	} else {
		var linked uuid.UUID
		if h.db.QueryRow(`SELECT employee_id FROM users WHERE id = $1 AND employee_id IS NOT NULL`, userID).Scan(&linked) == nil && linked != uuid.Nil {
			employeeID = linked
		}
	}
	_, err = h.db.Exec(`
		INSERT INTO work_order_time_logs (
			id, tenant_id, work_order_id, start_time, end_time, duration_hours,
			log_type, worker_id, worker_name, employee_id, notes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, logID, tenantID, woID, startTime, endTime, durationHours,
		input.LogType, userID, userName, employeeID, input.Notes)
	if err != nil {
		h.log.Error("Failed to record time", "error", err)
		response.InternalError(c, "Failed to record time")
		return
	}

	// Update work order quantity produced if provided
	if input.QuantityProduced > 0 {
		h.db.Exec(`
			UPDATE work_orders
			SET quantity_produced = quantity_produced + $1
			WHERE id = $2 AND tenant_id = $3
		`, input.QuantityProduced, woID, tenantID)
	}

	response.Created(c, gin.H{
		"id":      logID,
		"message": "Time logged successfully",
	})
}

// PauseWorkOrder pauses a work order
func (h *Handler) PauseWorkOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM work_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", woID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Work order not found")
		return
	}

	if currentStatus != "in_progress" {
		response.BadRequest(c, "Work order is not in progress")
		return
	}

	now := time.Now()

	// End current time log - use migration 010 columns: duration_hours
	h.db.Exec(`
		UPDATE work_order_time_logs
		SET end_time = $1, duration_hours = EXTRACT(EPOCH FROM ($1 - start_time)) / 3600
		WHERE work_order_id = $2 AND end_time IS NULL
	`, now, woID)

	// Update status to paused (migration 010 valid status)
	_, err = h.db.Exec("UPDATE work_orders SET status = 'paused' WHERE id = $1 AND tenant_id = $2", woID, tenantID)
	if err != nil {
		h.log.Error("Failed to pause work order", "error", err)
		response.InternalError(c, "Failed to pause work order")
		return
	}

	// Log pause - use migration 010 columns: worker_id, worker_name, duration_hours
	var userName string
	h.db.QueryRow("SELECT COALESCE(first_name || ' ' || last_name, email) FROM users WHERE id = $1", userID).Scan(&userName)
	h.db.Exec(`
		INSERT INTO work_order_time_logs (id, tenant_id, work_order_id, start_time, end_time, duration_hours, log_type, worker_id, worker_name)
		VALUES ($1, $2, $3, $4, $4, 0, 'pause', $5, $6)
	`, uuid.New(), tenantID, woID, now, userID, userName)

	response.Success(c, gin.H{"message": "Work order paused"})
}

// =====================================================
// MANUFACTURING TRANSFER HANDLERS
// =====================================================

// ListManufacturingTransfers returns manufacturing transfers
func (h *Handler) ListManufacturingTransfers(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)

	productionOrderID := c.Query("production_order_id")
	transferType := c.Query("transfer_type")
	status := c.Query("status")

	query := `
		SELECT mt.id, mt.tenant_id, mt.organization_id, mt.production_order_id,
			   mt.transfer_number, mt.transfer_type,
			   mt.source_location_id, mt.destination_location_id, mt.warehouse_id,
			   mt.status, mt.scheduled_date, mt.done_date, mt.created_at,
			   COALESCE(po.code, '') as po_number,
			   COALESCE(sl.name, '') as source_location_name,
			   COALESCE(dl.name, '') as dest_location_name,
			   COALESCE(w.name, '') as warehouse_name
		FROM manufacturing_transfers mt
		LEFT JOIN production_orders po ON mt.production_order_id = po.id
		LEFT JOIN warehouse_locations sl ON mt.source_location_id = sl.id
		LEFT JOIN warehouse_locations dl ON mt.destination_location_id = dl.id
		LEFT JOIN warehouses w ON mt.warehouse_id = w.id
		WHERE mt.tenant_id = $1 AND mt.deleted_at IS NULL
	`

	args := []interface{}{tenantID}
	argIdx := 2

	if orgID != uuid.Nil {
		query += fmt.Sprintf(" AND mt.organization_id = $%d", argIdx)
		args = append(args, orgID)
		argIdx++
	}
	if productionOrderID != "" {
		query += fmt.Sprintf(" AND mt.production_order_id = $%d", argIdx)
		args = append(args, productionOrderID)
		argIdx++
	}
	if transferType != "" {
		query += fmt.Sprintf(" AND mt.transfer_type = $%d", argIdx)
		args = append(args, transferType)
		argIdx++
	}
	if status != "" {
		query += fmt.Sprintf(" AND mt.status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}

	// Opt-in paging: this list grows with business activity, not with
	// configuration, so it has no natural ceiling. Callers that send no page
	// params still get the full list.
	countQuery := "SELECT COUNT(*)" + query[strings.Index(query, "FROM manufacturing_transfers"):]
	query += " ORDER BY mt.created_at DESC"

	paginate, page, pageSize, offset := optPagination(c)
	filterArgs := append([]interface{}{}, args...)
	if paginate {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list transfers", "error", err)
		response.InternalError(c, "Failed to list transfers")
		return
	}
	defer rows.Close()

	var transfers []entity.ManufacturingTransferResponse
	for rows.Next() {
		var t entity.ManufacturingTransferResponse
		var orgID, sourceLocID, destLocID, warehouseID sql.NullString
		var scheduledDate, doneDate sql.NullTime

		err := rows.Scan(
			&t.ID, &tenantID, &orgID, &t.ProductionOrderID,
			&t.TransferNumber, &t.TransferType,
			&sourceLocID, &destLocID, &warehouseID,
			&t.Status, &scheduledDate, &doneDate, &t.CreatedAt,
			&t.ProductionOrderNumber, &t.SourceLocationName, &t.DestinationLocationName, &t.WarehouseName,
		)
		if err != nil {
			continue
		}

		if sourceLocID.Valid {
			id, _ := uuid.Parse(sourceLocID.String)
			t.SourceLocationID = &id
		}
		if destLocID.Valid {
			id, _ := uuid.Parse(destLocID.String)
			t.DestinationLocationID = &id
		}
		if warehouseID.Valid {
			id, _ := uuid.Parse(warehouseID.String)
			t.WarehouseID = &id
		}
		if scheduledDate.Valid {
			t.ScheduledDate = &scheduledDate.Time
		}
		if doneDate.Valid {
			t.DoneDate = &doneDate.Time
		}

		t.TransferTypeLabel = getTransferTypeLabel(t.TransferType)
		t.StatusLabel = getTransferStatusLabel(t.Status)

		transfers = append(transfers, t)
	}

	if !paginate {
		response.Success(c, gin.H{"data": transfers})
		return
	}

	total := 0
	if err := h.db.QueryRow(countQuery, filterArgs...).Scan(&total); err != nil {
		h.log.Error("Failed to count transfers", "error", err)
		total = len(transfers)
	}
	response.Paginated(c, gin.H{"data": transfers}, page, pageSize, total)
}

// GetManufacturingTransfer returns a single manufacturing transfer with its lines
func (h *Handler) GetManufacturingTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	transferID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid transfer ID")
		return
	}

	// Get transfer
	query := `
		SELECT mt.id, mt.tenant_id, mt.organization_id, mt.production_order_id,
			   mt.transfer_number, mt.transfer_type,
			   mt.source_location_id, mt.destination_location_id, mt.warehouse_id,
			   mt.status, mt.scheduled_date, mt.done_date, mt.notes, mt.created_at,
			   COALESCE(po.code, '') as po_number,
			   COALESCE(sl.name, '') as source_location_name,
			   COALESCE(dl.name, '') as dest_location_name,
			   COALESCE(w.name, '') as warehouse_name
		FROM manufacturing_transfers mt
		LEFT JOIN production_orders po ON mt.production_order_id = po.id
		LEFT JOIN warehouse_locations sl ON mt.source_location_id = sl.id
		LEFT JOIN warehouse_locations dl ON mt.destination_location_id = dl.id
		LEFT JOIN warehouses w ON mt.warehouse_id = w.id
		WHERE mt.id = $1 AND mt.tenant_id = $2 AND mt.deleted_at IS NULL
	`

	var t entity.ManufacturingTransferResponse
	var orgID, sourceLocID, destLocID, warehouseID sql.NullString
	var scheduledDate, doneDate sql.NullTime
	var notes sql.NullString

	err = h.db.QueryRow(query, transferID, tenantID).Scan(
		&t.ID, &tenantID, &orgID, &t.ProductionOrderID,
		&t.TransferNumber, &t.TransferType,
		&sourceLocID, &destLocID, &warehouseID,
		&t.Status, &scheduledDate, &doneDate, &notes, &t.CreatedAt,
		&t.ProductionOrderNumber, &t.SourceLocationName, &t.DestinationLocationName, &t.WarehouseName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transfer not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get transfer", "error", err)
		response.InternalError(c, "Failed to get transfer")
		return
	}

	if sourceLocID.Valid {
		id, _ := uuid.Parse(sourceLocID.String)
		t.SourceLocationID = &id
	}
	if destLocID.Valid {
		id, _ := uuid.Parse(destLocID.String)
		t.DestinationLocationID = &id
	}
	if warehouseID.Valid {
		id, _ := uuid.Parse(warehouseID.String)
		t.WarehouseID = &id
	}
	if scheduledDate.Valid {
		t.ScheduledDate = &scheduledDate.Time
	}
	if doneDate.Valid {
		t.DoneDate = &doneDate.Time
	}

	t.TransferTypeLabel = getTransferTypeLabel(t.TransferType)
	t.StatusLabel = getTransferStatusLabel(t.Status)

	// Get transfer lines
	linesQuery := `
		SELECT mtl.id, mtl.product_id, mtl.quantity_demanded, mtl.quantity_done, mtl.uom,
			   mtl.lot_number, mtl.serial_number,
			   COALESCE(p.name, '') as product_name, COALESCE(p.sku, '') as product_code,
			   COALESCE(sl.name, '') as source_loc_name, COALESCE(dl.name, '') as dest_loc_name
		FROM manufacturing_transfer_lines mtl
		LEFT JOIN products p ON mtl.product_id = p.id
		LEFT JOIN warehouse_locations sl ON mtl.source_location_id = sl.id
		LEFT JOIN warehouse_locations dl ON mtl.destination_location_id = dl.id
		WHERE mtl.manufacturing_transfer_id = $1
	`

	rows, err := h.db.Query(linesQuery, transferID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var line entity.ManufacturingTransferLineResponse
			var lotNumber, serialNumber sql.NullString

			rows.Scan(
				&line.ID, &line.ProductID, &line.QuantityDemanded, &line.QuantityDone, &line.UOM,
				&lotNumber, &serialNumber,
				&line.ProductName, &line.ProductCode,
				&line.SourceLocationName, &line.DestinationLocationName,
			)

			if lotNumber.Valid {
				line.LotNumber = &lotNumber.String
			}
			if serialNumber.Valid {
				line.SerialNumber = &serialNumber.String
			}

			t.Lines = append(t.Lines, line)
		}
	}

	response.Success(c, gin.H{"data": t})
}

// ValidateManufacturingTransfer validates/completes a transfer
func (h *Handler) ValidateManufacturingTransfer(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	transferID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid transfer ID")
		return
	}

	var input entity.ValidateTransferInput
	c.ShouldBindJSON(&input)

	// Get transfer info
	var transferType, status string
	var productionOrderID uuid.UUID
	err = h.db.QueryRow(`
		SELECT transfer_type, status, production_order_id
		FROM manufacturing_transfers
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, transferID, tenantID).Scan(&transferType, &status, &productionOrderID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transfer not found")
		return
	}

	if status == "done" {
		response.BadRequest(c, "Transfer already validated")
		return
	}

	now := time.Now()

	// Update line quantities if provided
	for _, line := range input.Lines {
		lineID, _ := uuid.Parse(line.LineID)
		h.db.Exec(`
			UPDATE manufacturing_transfer_lines
			SET quantity_done = $1, lot_number = $2, serial_number = $3
			WHERE id = $4 AND manufacturing_transfer_id = $5
		`, line.QuantityDone, line.LotNumber, line.SerialNumber, lineID, transferID)
	}

	// If no lines provided, set quantity_done = quantity_demanded for all lines
	if len(input.Lines) == 0 {
		h.db.Exec(`
			UPDATE manufacturing_transfer_lines
			SET quantity_done = quantity_demanded
			WHERE manufacturing_transfer_id = $1
		`, transferID)
	}

	// Update transfer status
	_, err = h.db.Exec(`
		UPDATE manufacturing_transfers
		SET status = 'done', done_date = $1, processed_by = $2
		WHERE id = $3 AND tenant_id = $4
	`, now, userID, transferID, tenantID)
	if err != nil {
		h.log.Error("Failed to validate transfer", "error", err)
		response.InternalError(c, "Failed to validate transfer")
		return
	}

	// TODO: Create inventory transactions for the movement
	// This would move items from source to destination location

	response.Success(c, gin.H{
		"message":   "Transfer validated successfully",
		"done_date": now,
		"next_step": getNextStepMessage(transferType),
	})
}

// CreateWorkOrdersFromBOM creates work orders from BOM operations
func (h *Handler) CreateWorkOrdersFromBOM(productionOrderID uuid.UUID, bomID uuid.UUID, tenantID uuid.UUID, orgID uuid.UUID, quantity float64, userID uuid.UUID) error {
	// Get BOM operations
	rows, err := h.db.Query(`
		SELECT bo.id, bo.operation_name, bo.sequence, bo.work_center_id,
			   bo.setup_time_minutes, bo.run_time_minutes, bo.notes,
			   COALESCE(wc.name, '') as work_center_name
		FROM bom_operations bo
		LEFT JOIN work_centers wc ON bo.work_center_id = wc.id
		WHERE bo.bom_id = $1
		ORDER BY bo.sequence
	`, bomID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var woCount int
	h.db.QueryRow("SELECT COUNT(*) FROM work_orders WHERE tenant_id = $1", tenantID).Scan(&woCount)

	seq := 1
	for rows.Next() {
		var opID uuid.UUID
		var opName string
		var sequence int
		var wcID sql.NullString
		var setupTime, runTime int
		var notes sql.NullString
		var wcName string

		rows.Scan(&opID, &opName, &sequence, &wcID, &setupTime, &runTime, &notes, &wcName)

		woID := uuid.New()
		woNumber := fmt.Sprintf("WO%05d", woCount+seq)

		var workCenterID interface{} = nil
		var capacityPerHour float64
		if wcID.Valid {
			workCenterID, _ = uuid.Parse(wcID.String)
			// Get work center capacity to calculate realistic planned duration
			h.db.QueryRow(`SELECT COALESCE(capacity_per_hour, 1) FROM work_centers WHERE id = $1`, workCenterID).Scan(&capacityPerHour)
		}
		if capacityPerHour <= 0 {
			capacityPerHour = 1
		}

		// Planned duration = quantity / capacity_per_hour (how long to actually produce this qty)
		// BOM run_time is per-unit reference; real duration depends on capacity
		plannedHours := quantity / capacityPerHour
		setupHours := float64(setupTime) / 60

		var notesVal interface{} = nil
		if notes.Valid {
			notesVal = notes.String
		}

		_, err := h.db.Exec(`
			INSERT INTO work_orders (
				id, tenant_id, organization_id, production_order_id,
				code, name, sequence,
				operation_id, work_center_id,
				quantity_to_produce, uom,
				planned_duration_hours, setup_time_hours,
				status, instructions, created_by
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pcs', $11, $12, 'pending', $13, $14)
		`, woID, tenantID, orgID, productionOrderID,
			woNumber, opName, sequence,
			opID, workCenterID,
			quantity,
			plannedHours, setupHours,
			notesVal, userID)
		if err != nil {
			return err
		}

		seq++
	}

	return nil
}

// Helper functions
func getWorkOrderStatusLabel(status string) string {
	switch status {
	case "draft":
		return "Draft"
	case "waiting":
		return "Waiting"
	case "ready":
		return "Ready"
	case "in_progress":
		return "In Progress"
	case "done":
		return "Done"
	case "cancelled":
		return "Cancelled"
	default:
		return status
	}
}

func getTransferTypeLabel(transferType string) string {
	switch transferType {
	case "pick_components":
		return "Pick Components"
	case "store_finished":
		return "Store Finished Products"
	default:
		return transferType
	}
}

func getTransferStatusLabel(status string) string {
	switch status {
	case "draft":
		return "Draft"
	case "waiting":
		return "Waiting"
	case "ready":
		return "Ready"
	case "done":
		return "Done"
	case "cancelled":
		return "Cancelled"
	default:
		return status
	}
}

func getNextStepMessage(transferType string) string {
	switch transferType {
	case "pick_components":
		return "Components picked. Ready to start production."
	case "store_finished":
		return "Finished products stored. Manufacturing complete."
	default:
		return ""
	}
}

// consumeBOMComponents deducts BOM component quantities from inventory for a production order.
// It is idempotent — if Issue transactions already exist for this PO it does nothing.
// consumeBOMComponents issues the BOM components for a production order when
// production starts via the work-order path (StartWorkOrder). v2: every
// stock leg goes through applyStockDelta (TxType production_out) and the
// per-component consumption JEs post in the SAME transaction — stock and GL
// land or fail together (docs/production-integration-map.md §3). Greedy
// multi-warehouse sourcing and the shortfall-to-PO-warehouse booking match
// StartProductionOrder.
func (h *Handler) consumeBOMComponents(poID, tenantID, userID uuid.UUID, now time.Time) {
	// Guard: skip if already consumed. Covers the legacy 'issue' rows and
	// the 'production_out' rows both this path and StartProductionOrder
	// write via applyStockDelta — otherwise starting the MO and then a work
	// order would double-issue materials.
	var existing int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2
		  AND transaction_type IN ('issue', 'production_out') AND deleted_at IS NULL
	`, tenantID, poID).Scan(&existing)
	if existing > 0 {
		return
	}

	var bomID *uuid.UUID
	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	var qtyPlanned float64
	err := h.db.QueryRow(`
		SELECT bom_id, warehouse_id, organization_id, quantity_planned
		FROM production_orders WHERE id = $1 AND tenant_id = $2
	`, poID, tenantID).Scan(&bomID, &warehouseID, &organizationID, &qtyPlanned)
	if err != nil || bomID == nil || warehouseID == nil {
		return
	}

	type bomComponent struct {
		ComponentID  uuid.UUID
		Quantity     float64
		ScrapPercent float64
		BOMOutputQty float64
	}
	rows, err := h.db.Query(`
		SELECT bl.component_id, bl.quantity, COALESCE(bl.scrap_percent, 0), pb.quantity
		FROM bom_lines bl
		JOIN product_boms pb ON pb.id = bl.bom_id
		WHERE bl.bom_id = $1
	`, bomID)
	if err != nil {
		return
	}
	var components []bomComponent
	for rows.Next() {
		var c bomComponent
		if rows.Scan(&c.ComponentID, &c.Quantity, &c.ScrapPercent, &c.BOMOutputQty) == nil {
			components = append(components, c)
		}
	}
	rows.Close()

	tx, err := h.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	// Per-component consumption accumulator (qty + cost) — BOM consumption
	// may pull from multiple warehouses for one component; aggregating to
	// one JE per component keeps GL output proportional to BOM granularity
	// rather than source granularity.
	type consAcc struct {
		componentID uuid.UUID
		qty         float64
		cost        float64
	}
	consAggMap := make(map[uuid.UUID]*consAcc, len(components))
	accumulate := func(compID uuid.UUID, qty, cost float64) {
		acc, ok := consAggMap[compID]
		if !ok {
			acc = &consAcc{componentID: compID}
			consAggMap[compID] = acc
		}
		acc.qty += qty
		acc.cost += cost
	}

	for _, comp := range components {
		bomOutputQty := comp.BOMOutputQty
		if bomOutputQty <= 0 {
			bomOutputQty = 1
		}
		totalNeeded := comp.Quantity * (1 + comp.ScrapPercent/100) * (qtyPlanned / bomOutputQty)
		if totalNeeded <= 0 {
			continue
		}

		var compCost float64
		tx.QueryRow("SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1", comp.ComponentID).Scan(&compCost)

		// Greedy multi-warehouse consumption: any warehouse in the org with
		// stock, biggest balance first.
		type src struct {
			whID     uuid.UUID
			unitCost float64
			onHand   float64
		}
		var sources []src
		srcQuery := `
			SELECT i.warehouse_id, COALESCE(i.unit_cost, 0), COALESCE(i.quantity_on_hand, 0)
			FROM inventory i
			JOIN warehouses w ON w.id = i.warehouse_id
			WHERE i.tenant_id = $1 AND i.product_id = $2
			  AND i.quantity_on_hand > 0
			  AND w.deleted_at IS NULL`
		srcArgs := []interface{}{tenantID, comp.ComponentID}
		if organizationID != nil {
			srcQuery += ` AND w.organization_id = $3`
			srcArgs = append(srcArgs, *organizationID)
		}
		srcQuery += ` ORDER BY i.quantity_on_hand DESC`
		if sRows, sErr := tx.Query(srcQuery, srcArgs...); sErr == nil {
			for sRows.Next() {
				var s src
				if sRows.Scan(&s.whID, &s.unitCost, &s.onHand) == nil {
					sources = append(sources, s)
				}
			}
			sRows.Close()
		}

		remaining := totalNeeded
		for _, s := range sources {
			if remaining <= 0 {
				break
			}
			take := s.onHand
			if take > remaining {
				take = remaining
			}
			unitCost := s.unitCost
			if unitCost == 0 {
				unitCost = compCost
			}
			_, valuedCost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
				TenantID:    tenantID,
				OrgID:       organizationID,
				ProductID:   comp.ComponentID,
				WarehouseID: s.whID,
				Qty:         -take,
				UnitCost:    unitCost,
				TxType:      "production_out",
				RefType:     "production_order",
				RefID:       poID.String(),
				Reason:      "material_consumption",
				Notes:       "Materials consumed for production",
				CreatedBy:   userID,
				When:        now,
				AllowNeg:    false,
			})
			if dErr != nil {
				h.log.Error("consumeBOMComponents: stock issue failed — rolling back",
					"error", dErr, "po_id", poID, "component_id", comp.ComponentID)
				return
			}
			accumulate(comp.ComponentID, take, take*valuedCost)
			remaining -= take
		}

		// Shortfall: book the remainder against the PO's warehouse so the
		// missing stock is visible there and the planned material cost on
		// the PO still matches the journal entries.
		if remaining > 0.0001 {
			_, valuedCost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
				TenantID:    tenantID,
				OrgID:       organizationID,
				ProductID:   comp.ComponentID,
				WarehouseID: *warehouseID,
				Qty:         -remaining,
				UnitCost:    compCost,
				TxType:      "production_out",
				RefType:     "production_order",
				RefID:       poID.String(),
				Reason:      "material_consumption",
				Notes:       "Materials shortfall — booked to PO warehouse",
				CreatedBy:   userID,
				When:        now,
				AllowNeg:    true,
			})
			if dErr != nil {
				h.log.Error("consumeBOMComponents: shortfall booking failed — rolling back",
					"error", dErr, "po_id", poID, "component_id", comp.ComponentID)
				return
			}
			accumulate(comp.ComponentID, remaining, remaining*valuedCost)
		}
	}

	// GL: one consumption JE per component, in the SAME tx as the stock
	// legs (previously post-commit — a GL failure left exactly the
	// Buxgalteriya-vs-Ombor drift the reconcile endpoint kept surfacing).
	// SourceType 'production_order_consume' + source_id=PO is what the
	// MO-complete material-JE dedupe matches; the idempotency key
	// `WO-CONS-<poID>-<componentID>` keeps re-runs safe. A JE failure
	// poisons the tx so the commit below fails and the stock legs roll
	// back with it — stock never moves without its GL.
	for _, acc := range consAggMap {
		if acc.qty <= 0 || acc.cost <= 0 {
			continue
		}
		h.postInventoryConsumptionJE(tx, postInventoryConsumptionArgs{
			TenantID:       tenantID,
			OrganizationID: organizationID,
			ProductID:      acc.componentID,
			Quantity:       acc.qty,
			UnitCost:       acc.cost / acc.qty,
			SourceType:     "production_order_consume",
			SourceID:       poID,
			IdempotencyKey: fmt.Sprintf("WO-CONS-%s-%s", poID.String(), acc.componentID.String()),
			Description: fmt.Sprintf("Production order %s — BOM component consumption",
				poID.String()[:8]),
		})
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("consumeBOMComponents: tx commit failed", "error", err, "po_id", poID)
	}
}

// receiveFinishedGoods adds the produced quantity of the finished product to
// inventory via applyStockDelta (TxType production_in) inside one
// transaction (docs/production-integration-map.md §3). It is idempotent —
// if a production_complete receipt already exists for this PO it does nothing.
func (h *Handler) receiveFinishedGoods(poID, tenantID, userID uuid.UUID, producedQty float64, now time.Time) float64 {
	// Guard: skip if the finished goods were already received. The reason
	// filter is load-bearing (audit §2.2): without it, a prior
	// material_return receipt for the same PO silently suppressed the FG
	// receipt (returned 0 → unitCost 0 → the completion JE never posted).
	// Covers the legacy 'receipt' rows and the v2 'production_in' rows;
	// mirrors the guard in CompleteProductionOrder.
	var existing int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2
		  AND transaction_type IN ('receipt', 'production_in') AND reason = 'production_complete'
		  AND deleted_at IS NULL
	`, tenantID, poID).Scan(&existing)
	if existing > 0 {
		return 0
	}

	var productID uuid.UUID
	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	var qtyPlanned float64
	err := h.db.QueryRow(`
		SELECT product_id, warehouse_id, organization_id, quantity_planned
		FROM production_orders WHERE id = $1 AND tenant_id = $2
	`, poID, tenantID).Scan(&productID, &warehouseID, &organizationID, &qtyPlanned)
	if err != nil {
		return 0
	}

	if producedQty <= 0 {
		producedQty = qtyPlanned
	}

	// Calculate unit cost from BOM components + operations (machine costs)
	var bomID *uuid.UUID
	h.db.QueryRow(`SELECT bom_id FROM production_orders WHERE id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&bomID)

	var unitCost float64
	if bomID != nil {
		var bomOutputQty float64
		if h.db.QueryRow(`SELECT COALESCE(quantity, 1) FROM product_boms WHERE id = $1`, bomID).Scan(&bomOutputQty) == nil && bomOutputQty > 0 {
			// Material cost from BOM components
			var materialCost float64
			h.db.QueryRow(`
				SELECT COALESCE(SUM(bl.quantity * COALESCE(p.cost_price, 0) * (1 + COALESCE(bl.scrap_percent, 0) / 100.0)), 0)
				FROM bom_lines bl
				JOIN products p ON p.id = bl.component_id
				WHERE bl.bom_id = $1
			`, bomID).Scan(&materialCost)

			// Machine + labor via the ONE shared costing helper (B4). This
			// path previously used a capacity-only formula with no labor at
			// all, so the FG valuation (and the downstream JE) differed from
			// the CompleteProductionOrder path.
			machineCost, laborCost := h.computeMOMachineAndLaborCost(*bomID, bomOutputQty, poID, producedQty)

			unitCost = materialCost/bomOutputQty + machineCost + laborCost
		}
	}
	// Add per-unit cost of extras added in shop floor (work_order_materials
	// are NOT in the BOM, so the BOM-derived material cost above ignores them).
	// work_order_materials.total_cost is a per-PO total; divide by producedQty
	// to convert it to a per-unit contribution.
	if producedQty > 0 {
		var extraMaterialCost float64
		h.db.QueryRow(`SELECT COALESCE(SUM(total_cost), 0) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&extraMaterialCost)
		if extraMaterialCost > 0 {
			unitCost += extraMaterialCost / producedQty
		}
	}
	if unitCost <= 0 {
		h.db.QueryRow("SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1 AND tenant_id = $2", productID, tenantID).Scan(&unitCost)
	}
	// Update product's cost_price with the calculated manufacturing cost (both tables)
	if unitCost > 0 {
		h.db.Exec(`UPDATE products SET cost_price = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4`, unitCost, now, productID, tenantID)
		if organizationID != nil {
			h.db.Exec(`UPDATE product_organization_settings SET cost_price = $1, updated_at = $2 WHERE product_id = $3 AND organization_id = $4`,
				unitCost, now, productID, *organizationID)
		}
	}

	// If no warehouse set, check BOM first, then fall back to org/tenant default
	if warehouseID == nil && bomID != nil && organizationID != nil {
		var bomWhID uuid.UUID
		if h.db.QueryRow(
			`SELECT b.warehouse_id FROM product_boms b
			 JOIN warehouses w ON w.id = b.warehouse_id
			 WHERE b.id = $1 AND w.organization_id = $2 AND w.deleted_at IS NULL`,
			bomID, *organizationID,
		).Scan(&bomWhID) == nil {
			warehouseID = &bomWhID
			h.db.Exec(`UPDATE production_orders SET warehouse_id = $1 WHERE id = $2 AND tenant_id = $3`, bomWhID, poID, tenantID)
			h.log.Info("receiveFinishedGoods: warehouse from BOM", "warehouse_id", bomWhID, "po_id", poID)
		}
	}
	if warehouseID == nil {
		var firstWH uuid.UUID
		if h.db.QueryRow(`SELECT id FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&firstWH) == nil {
			warehouseID = &firstWH
			h.db.Exec(`UPDATE production_orders SET warehouse_id = $1 WHERE id = $2 AND tenant_id = $3`, firstWH, poID, tenantID)
			h.log.Info("receiveFinishedGoods: auto-assigned warehouse", "warehouse_id", firstWH, "po_id", poID)
		} else {
			h.log.Warn("receiveFinishedGoods: no warehouse found, skipping inventory", "po_id", poID)
			return unitCost
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return 0
	}
	defer tx.Rollback()

	if _, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
		TenantID:    tenantID,
		OrgID:       organizationID,
		ProductID:   productID,
		WarehouseID: *warehouseID,
		Qty:         producedQty,
		UnitCost:    unitCost,
		TxType:      "production_in",
		RefType:     "production_order",
		RefID:       poID.String(),
		Reason:      "production_complete",
		Notes:       "Finished goods from production order",
		CreatedBy:   userID,
		When:        now,
	}); dErr != nil {
		h.log.Error("receiveFinishedGoods: stock receipt failed", "error", dErr, "po_id", poID)
		return 0
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("receiveFinishedGoods: failed to commit transaction", "error", commitErr, "po_id", poID)
		return 0
	}
	h.log.Info("receiveFinishedGoods: finished goods added to inventory", "po_id", poID, "qty", producedQty)

	// Lot insert is separate — a failure here must NOT roll back the inventory
	// update above. In PostgreSQL, any error inside a transaction poisons it
	// and makes COMMIT fail, so the lot lives outside the critical transaction.
	lotID := uuid.New()
	lotNumber := fmt.Sprintf("MFG-%s", poID.String()[:8])
	if _, lotErr := h.db.Exec(`
		INSERT INTO inventory_lots (
			id, tenant_id, product_id, warehouse_id, lot_number,
			received_date, initial_quantity, remaining_quantity,
			unit_cost, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6::date, $7, $7, $8, 'available', $9, $9)
	`, lotID, tenantID, productID, warehouseID, lotNumber,
		now, producedQty, unitCost, now); lotErr != nil {
		h.log.Error("receiveFinishedGoods: lot insert failed (non-fatal)", "error", lotErr, "po_id", poID)
	}
	return unitCost
}

// receiveScrapGoods moves scrapped quantity to a dedicated scrap warehouse.
// Auto-creates the scrap warehouse if one doesn't exist for this tenant.
func (h *Handler) receiveScrapGoods(poID, tenantID, userID uuid.UUID, productID uuid.UUID, organizationID *uuid.UUID, scrapQty float64, unitCost float64, now time.Time) {
	if scrapQty <= 0 {
		return
	}

	// Find scrap warehouse for this tenant
	var scrapWarehouseID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM warehouses
		WHERE tenant_id = $1 AND warehouse_type = 'scrap' AND is_active = true
		LIMIT 1
	`, tenantID).Scan(&scrapWarehouseID)

	if err != nil {
		// Auto-create scrap warehouse
		scrapWarehouseID = uuid.New()
		_, createErr := h.db.Exec(`
			INSERT INTO warehouses (id, tenant_id, organization_id, code, name, warehouse_type, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, 'SCRAP', 'Scrap', 'scrap', true, $4, $4)
		`, scrapWarehouseID, tenantID, organizationID, now)
		if createErr != nil {
			h.log.Error("receiveScrapGoods: failed to create scrap warehouse", "error", createErr)
			return
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	// Scrap receipt via applyStockDelta (TxType production_scrap —
	// integration map §3). The delete-cascade reversal in
	// DeleteProductionOrder includes 'production_scrap' in its receipt list.
	if _, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
		TenantID:    tenantID,
		OrgID:       organizationID,
		ProductID:   productID,
		WarehouseID: scrapWarehouseID,
		Qty:         scrapQty,
		UnitCost:    unitCost,
		TxType:      "production_scrap",
		RefType:     "production_order",
		RefID:       poID.String(),
		Reason:      "production_scrap",
		Notes:       "Scrapped items from production",
		CreatedBy:   userID,
		When:        now,
	}); dErr != nil {
		h.log.Error("receiveScrapGoods: stock receipt failed", "error", dErr, "po_id", poID)
		return
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("receiveScrapGoods: failed to commit", "error", commitErr)
	} else {
		h.log.Info("receiveScrapGoods: scrap added to scrap warehouse", "po_id", poID, "qty", scrapQty)
	}
}

// createFinishedGoodsJournalEntry creates the WIP → Finished Goods journal entry
// when production is completed via work orders.
// Flow: Dt 1320 WIP (machine+labor) / Ct 2590,6720
//
//	Dt 1330 Finished Goods    / Ct 1320 WIP = totalCost
func (h *Handler) createFinishedGoodsJournalEntry(
	poID, tenantID uuid.UUID, organizationID *uuid.UUID, productID, userID uuid.UUID,
	producedQty, unitCost float64, now time.Time,
) {
	totalCost := producedQty * unitCost
	if totalCost <= 0 {
		return
	}

	// Prevent duplicate: check if a completion JE already exists for this PO
	var existingJE int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE tenant_id = $1 AND source_type = 'production_complete' AND source_id = $2
	`, tenantID, poID.String()).Scan(&existingJE)
	if existingJE > 0 {
		return
	}

	// Calculate cost breakdown from BOM
	var bomID *uuid.UUID
	h.db.QueryRow(`SELECT bom_id FROM production_orders WHERE id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&bomID)

	var materialCost, machineCost, laborCost float64
	if bomID != nil {
		var bomOutputQty float64
		if h.db.QueryRow(`SELECT COALESCE(quantity, 1) FROM product_boms WHERE id = $1`, bomID).Scan(&bomOutputQty) == nil && bomOutputQty > 0 {
			h.db.QueryRow(`
				SELECT COALESCE(SUM(bl.quantity * COALESCE(p.cost_price, 0) * (1 + COALESCE(bl.scrap_percent, 0) / 100.0)), 0)
				FROM bom_lines bl JOIN products p ON p.id = bl.component_id WHERE bl.bom_id = $1
			`, bomID).Scan(&materialCost)
			materialCost = materialCost / bomOutputQty

			// Machine + labor via the ONE shared costing helper (B4). This
			// path previously used a capacity-only machine formula and
			// derived labor as the residual (unitCost − material − machine),
			// so the WIP legs differed from the MO-complete path's split.
			machineCost, laborCost = h.computeMOMachineAndLaborCost(*bomID, bomOutputQty, poID, producedQty)
		}
	}

	totalMaterialCost := producedQty * materialCost
	totalMachineCost := producedQty * machineCost
	totalLaborCost := producedQty * laborCost

	// Look up accounts
	wipAcct := findAccount(h.db, tenantID, organizationID, "work in progress", "2010")
	// Raw-materials account is NAS code 1010 (Xom ashyo va materiallar).
	// 1030 is Yoqilg'i (Fuel) — was the wrong primary and is what
	// migration 411 had to clean up post-hoc. Same fix applied in
	// manufacturing.go (start, complete, return-to-WIP JEs).
	rawAcct := findAccount(h.db, tenantID, organizationID, "xom ashyo", "1010")
	if rawAcct == uuid.Nil {
		rawAcct = findAccount(h.db, tenantID, organizationID, "raw materials", "1010")
	}
	finishedAcct := findAccount(h.db, tenantID, organizationID, "finished goods", "2810")
	machineAcct := findAccount(h.db, tenantID, organizationID, "accrued machine", "2590")
	salaryAcct := findAccount(h.db, tenantID, organizationID, "accrued salaries", "6710")

	useDetailedFlow := wipAcct != uuid.Nil && rawAcct != uuid.Nil && finishedAcct != uuid.Nil

	// Find journal
	var journalID uuid.UUID
	var nextNumber int
	err := h.db.QueryRow(`
		SELECT id, next_number FROM journals
		WHERE tenant_id = $1 AND type = 'general' AND is_active = true
		ORDER BY created_at ASC LIMIT 1
	`, tenantID).Scan(&journalID, &nextNumber)
	if err != nil || journalID == uuid.Nil {
		h.log.Error("createFinishedGoodsJE: no general journal found", "tenant_id", tenantID)
		return
	}

	// Get PO details for description
	var poNumber, productName string
	h.db.QueryRow(`SELECT code FROM production_orders WHERE id = $1`, poID).Scan(&poNumber)
	h.db.QueryRow(`SELECT name FROM products WHERE id = $1`, productID).Scan(&productName)

	entryID := uuid.New()
	entryNumber := fmt.Sprintf("MFG%06d", nextEntryNumberSeq(h.db, tenantID, organizationID, "MFG", nextNumber))
	description := fmt.Sprintf("Ishlab chiqarish yakunlandi: %s - %s (soni: %.0f)", poNumber, productName, producedQty)

	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("createFinishedGoodsJE: failed to begin journal tx", "error", txErr, "po_id", poID)
		return
	}
	defer tx.Rollback()

	if useDetailedFlow {
		// Check if material consumption JE was already created at production start
		var materialJEExists int
		h.db.QueryRow(`
        SELECT COUNT(*) FROM journal_entries
        WHERE tenant_id = $1 AND organization_id = $2
        AND ((source_type = 'production_start' AND source_id = $4)
             OR description LIKE '%' || $3 || '%started - materials consumed%')
        AND status = 'posted'
    `, tenantID, organizationID, poNumber, poID.String()).Scan(&materialJEExists)
		materialAlreadyJournalized := materialJEExists > 0

		// Calculate entry total
		wipInflow := float64(0)
		if totalMaterialCost > 0 && !materialAlreadyJournalized {
			wipInflow += totalMaterialCost
		}
		if totalMachineCost > 0 && machineAcct != uuid.Nil {
			wipInflow += totalMachineCost
		}
		if totalLaborCost > 0 && salaryAcct != uuid.Nil {
			wipInflow += totalLaborCost
		}
		entryTotal := wipInflow + totalCost

		if _, err := tx.Exec(`
        INSERT INTO journal_entries (
            id, tenant_id, organization_id, journal_id, entry_number,
            entry_date, description, source_type, source_id, status, total_debit, total_credit,
            created_by, created_at, updated_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, 'production_complete', $8, 'posted', $9, $9, $10, $11, $11)
    `, entryID, tenantID, organizationID, journalID, entryNumber,
			now, description, poID.String(), entryTotal, userID, now); err != nil {
			h.log.Error("createFinishedGoodsJE: failed to insert detailed journal entry", "error", err, "po_id", poID)
			return
		}

		lineNum := 1

		// Line 1: Dt WIP / Kt Raw Materials (skip if already done at start)
		if totalMaterialCost > 0 && !materialAlreadyJournalized {
			if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
            VALUES ($1, $2, $3, 'NJI: xom ashyo sarflandi', $4, 0, $5, $6)`, uuid.New(), entryID, wipAcct, totalMaterialCost, lineNum, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert WIP material debit line", "error", err, "po_id", poID)
				return
			}
			lineNum++
			if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
            VALUES ($1, $2, $3, 'NJI: xom ashyo sarflandi', 0, $4, $5, $6)`, uuid.New(), entryID, rawAcct, totalMaterialCost, lineNum, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert raw materials credit line", "error", err, "po_id", poID)
				return
			}
			lineNum++
			if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMaterialCost, now, wipAcct); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to update WIP balance (material)", "error", err, "po_id", poID)
				return
			}
			if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalMaterialCost, now, rawAcct); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to update raw materials balance", "error", err, "po_id", poID)
				return
			}
		}

		// Line 2: Dt WIP / Kt Accrued Machine
		if totalMachineCost > 0 && machineAcct != uuid.Nil {
			if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
            VALUES ($1, $2, $3, 'NJI: stanok xarajatlari', $4, 0, $5, $6)`, uuid.New(), entryID, wipAcct, totalMachineCost, lineNum, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert WIP machine debit line", "error", err, "po_id", poID)
				return
			}
			lineNum++
			if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
            VALUES ($1, $2, $3, 'NJI: stanok xarajatlari', 0, $4, $5, $6)`, uuid.New(), entryID, machineAcct, totalMachineCost, lineNum, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert machine credit line", "error", err, "po_id", poID)
				return
			}
			lineNum++
			if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMachineCost, now, wipAcct); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to update WIP balance (machine)", "error", err, "po_id", poID)
				return
			}
			if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMachineCost, now, machineAcct); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to update machine balance", "error", err, "po_id", poID)
				return
			}
		}

		// Line 3: Dt WIP / Kt Accrued Salary
		if totalLaborCost > 0 && salaryAcct != uuid.Nil {
			if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
            VALUES ($1, $2, $3, 'NJI: ish haqi xarajatlari', $4, 0, $5, $6)`, uuid.New(), entryID, wipAcct, totalLaborCost, lineNum, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert WIP labor debit line", "error", err, "po_id", poID)
				return
			}
			lineNum++
			if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
            VALUES ($1, $2, $3, 'NJI: ish haqi xarajatlari', 0, $4, $5, $6)`, uuid.New(), entryID, salaryAcct, totalLaborCost, lineNum, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert salary credit line", "error", err, "po_id", poID)
				return
			}
			lineNum++
			if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalLaborCost, now, wipAcct); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to update WIP balance (labor)", "error", err, "po_id", poID)
				return
			}
			if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalLaborCost, now, salaryAcct); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to update salary balance", "error", err, "po_id", poID)
				return
			}
		}

		// Line 4: Dt 1330 Finished Goods / Kt 1320 WIP = totalCost
		if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
        VALUES ($1, $2, $3, 'Tayyor mahsulot ishlab chiqarishdan', $4, 0, $5, $6)`, uuid.New(), entryID, finishedAcct, totalCost, lineNum, now); err != nil {
			h.log.Error("createFinishedGoodsJE: failed to insert finished goods debit line", "error", err, "po_id", poID)
			return
		}
		lineNum++
		if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
        VALUES ($1, $2, $3, 'Tayyor mahsulot ishlab chiqarishdan', 0, $4, $5, $6)`, uuid.New(), entryID, wipAcct, totalCost, lineNum, now); err != nil {
			h.log.Error("createFinishedGoodsJE: failed to insert WIP transfer credit line", "error", err, "po_id", poID)
			return
		}

		// Update balances: Finished Goods up, WIP down
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalCost, now, finishedAcct); err != nil {
			h.log.Error("createFinishedGoodsJE: failed to update finished goods balance", "error", err, "po_id", poID)
			return
		}
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalCost, now, wipAcct); err != nil {
			h.log.Error("createFinishedGoodsJE: failed to update WIP balance (transfer)", "error", err, "po_id", poID)
			return
		}

	} else {
		// Fallback: Dt Inventory / Kt COGS
		ca := getCategoryAccounts(h.db, tenantID, organizationID, productID)
		inventoryAcct := ca.StockValuationAccountID
		cogsAcct := ca.ExpenseAccountID
		if cogsAcct == uuid.Nil {
			cogsAcct = findAccount(h.db, tenantID, organizationID, "manufacturing", "9120")
		}
		if cogsAcct == uuid.Nil {
			cogsAcct = findAccount(h.db, tenantID, organizationID, "cost of production", "9110")
		}

		if inventoryAcct != uuid.Nil && cogsAcct != uuid.Nil {
			if _, err := tx.Exec(`
            INSERT INTO journal_entries (
                id, tenant_id, organization_id, journal_id, entry_number,
                entry_date, description, source_type, source_id, status, total_debit, total_credit,
                created_by, created_at, updated_at
            ) VALUES ($1, $2, $3, $4, $5, $6, $7, 'production_complete', $8, 'posted', $9, $9, $10, $11, $11)
        `, entryID, tenantID, organizationID, journalID, entryNumber,
				now, description, poID.String(), totalCost, userID, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert fallback journal entry", "error", err, "po_id", poID)
				return
			}

			if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
            VALUES ($1, $2, $3, $4, $5, 0, 1, $6)`, uuid.New(), entryID, inventoryAcct, description, totalCost, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert fallback inventory line", "error", err, "po_id", poID)
				return
			}
			if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
            VALUES ($1, $2, $3, $4, 0, $5, 2, $6)`, uuid.New(), entryID, cogsAcct, description, totalCost, now); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to insert fallback COGS line", "error", err, "po_id", poID)
				return
			}

			if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalCost, now, inventoryAcct); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to update fallback inventory balance", "error", err, "po_id", poID)
				return
			}
			if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalCost, now, cogsAcct); err != nil {
				h.log.Error("createFinishedGoodsJE: failed to update fallback COGS balance", "error", err, "po_id", poID)
				return
			}
		}
	}

	// Update journal next_number
	if _, err := tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID); err != nil {
		h.log.Error("createFinishedGoodsJE: failed to bump journal next_number", "error", err, "po_id", poID)
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("createFinishedGoodsJE: failed to commit journal entry", "error", err, "po_id", poID)
		return
	}
	h.log.Info("createFinishedGoodsJE: journal entry created", "po_id", poID, "entry_id", entryID, "total_cost", totalCost)
}

// transferToFinishedGoodsWarehouse moves finished goods from the production warehouse
// to a dedicated finished goods warehouse (warehouse_type = 'finished_goods').
func (h *Handler) transferToFinishedGoodsWarehouse(
	poID, tenantID uuid.UUID, organizationID *uuid.UUID, productID uuid.UUID,
	productionWarehouseID uuid.UUID, userID uuid.UUID,
	qty, unitCost float64, now time.Time,
) {
	// Find finished goods warehouse
	var fgWarehouseID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM warehouses
		WHERE tenant_id = $1 AND warehouse_type = 'finished_goods' AND is_active = true
		LIMIT 1
	`, tenantID).Scan(&fgWarehouseID)

	if err != nil {
		// Auto-create finished goods warehouse
		fgWarehouseID = uuid.New()
		_, createErr := h.db.Exec(`
			INSERT INTO warehouses (id, tenant_id, organization_id, code, name, warehouse_type, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, 'FG', 'Tayyor mahsulot ombori', 'finished_goods', true, $4, $4)
		`, fgWarehouseID, tenantID, organizationID, now)
		if createErr != nil {
			h.log.Error("transferToFG: failed to create FG warehouse", "error", createErr)
			return
		}
		h.log.Info("transferToFG: auto-created finished goods warehouse", "id", fgWarehouseID)
	}

	// Don't transfer if production warehouse IS the finished goods warehouse
	if fgWarehouseID == productionWarehouseID {
		return
	}

	// Both legs via applyStockDelta in ONE tx (transfer_out / transfer_in,
	// stored sign preserved by the stock_ledger view). The delete-cascade
	// reversal in DeleteProductionOrder includes both types. A failed out-leg
	// (e.g. insufficient stock) aborts the transfer — goods stay put.
	tx, txErr := h.db.Begin()
	if txErr != nil {
		return
	}
	defer tx.Rollback()

	if _, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
		TenantID:    tenantID,
		OrgID:       organizationID,
		ProductID:   productID,
		WarehouseID: productionWarehouseID,
		Qty:         -qty,
		UnitCost:    unitCost,
		TxType:      "transfer_out",
		RefType:     "production_order",
		RefID:       poID.String(),
		Reason:      "fg_transfer",
		Notes:       "Tayyor mahsulot omboriga o'tkazildi",
		FromWH:      &productionWarehouseID,
		ToWH:        &fgWarehouseID,
		CreatedBy:   userID,
		When:        now,
	}); dErr != nil {
		h.log.Error("transferToFG: out-leg failed, transfer skipped", "error", dErr, "po_id", poID)
		return
	}

	if _, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
		TenantID:    tenantID,
		OrgID:       organizationID,
		ProductID:   productID,
		WarehouseID: fgWarehouseID,
		Qty:         qty,
		UnitCost:    unitCost,
		TxType:      "transfer_in",
		RefType:     "production_order",
		RefID:       poID.String(),
		Reason:      "fg_transfer",
		Notes:       "Ishlab chiqarishdan tayyor mahsulot qabul qilindi",
		FromWH:      &productionWarehouseID,
		ToWH:        &fgWarehouseID,
		CreatedBy:   userID,
		When:        now,
	}); dErr != nil {
		h.log.Error("transferToFG: in-leg failed, transfer rolled back", "error", dErr, "po_id", poID)
		return
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("transferToFG: commit failed", "error", commitErr)
	} else {
		h.log.Info("transferToFG: finished goods transferred", "po_id", poID, "qty", qty, "from", productionWarehouseID, "to", fgWarehouseID)
	}
}

// =====================================================
// WORK ORDER MATERIALS
// =====================================================

func (h *Handler) ListWorkOrderMaterials(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	query := `
		SELECT id, product_id, product_name, quantity, uom, unit_cost, total_cost, notes, created_at
		FROM work_order_materials
		WHERE tenant_id = $1 AND work_order_id = $2
		ORDER BY created_at DESC`

	rows, err := h.db.Query(query, tenantID, woID)
	if err != nil {
		response.InternalError(c, "Failed to fetch materials")
		return
	}
	defer rows.Close()

	var materials []entity.WorkOrderMaterialResponse
	var totalCost float64
	for rows.Next() {
		var m entity.WorkOrderMaterialResponse
		if err := rows.Scan(&m.ID, &m.ProductID, &m.ProductName, &m.Quantity, &m.UOM,
			&m.UnitCost, &m.TotalCost, &m.Notes, &m.CreatedAt); err != nil {
			response.InternalError(c, "Failed to scan material")
			return
		}
		totalCost += m.TotalCost
		materials = append(materials, m)
	}

	if materials == nil {
		materials = []entity.WorkOrderMaterialResponse{}
	}

	response.Success(c, gin.H{"materials": materials, "total_cost": totalCost})
}

func (h *Handler) AddWorkOrderMaterial(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	var input entity.WorkOrderMaterialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	if input.Quantity <= 0 {
		response.BadRequest(c, "Quantity must be greater than 0")
		return
	}

	// Get work order to find production_order_id
	var poID uuid.UUID
	err = h.db.QueryRow(`SELECT production_order_id FROM work_orders WHERE id = $1 AND tenant_id = $2`, woID, tenantID).Scan(&poID)
	if err != nil {
		response.NotFound(c, "Work order not found")
		return
	}

	// Get production order warehouse and organization
	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	h.db.QueryRow(`SELECT warehouse_id, organization_id FROM production_orders WHERE id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&warehouseID, &organizationID)

	// Get product name and cost from products table
	var productName string
	var productCost float64
	err = h.db.QueryRow(`SELECT name, COALESCE(cost_price, 0) FROM products WHERE id = $1 AND tenant_id = $2`, input.ProductID, tenantID).Scan(&productName, &productCost)
	if err != nil {
		response.NotFound(c, "Product not found")
		return
	}

	unitCost := input.UnitCost
	if unitCost == 0 {
		unitCost = productCost
	}
	totalCost := unitCost * input.Quantity

	if input.UOM == "" {
		input.UOM = "pcs"
	}

	userID, _ := middleware.GetUserID(c)
	now := time.Now()
	id := uuid.New()

	// Resolve the source warehouse for the stock issue: the PO's warehouse
	// when it has an inventory row for this product, else the warehouse
	// holding the biggest balance (same preference the old lookup had).
	var srcWarehouseID *uuid.UUID
	var invUnitCost float64
	{
		var wh uuid.UUID
		lookupErr := sql.ErrNoRows
		if warehouseID != nil {
			lookupErr = h.db.QueryRow(`
				SELECT warehouse_id, COALESCE(unit_cost, 0) FROM inventory
				WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
				LIMIT 1
			`, tenantID, input.ProductID, warehouseID).Scan(&wh, &invUnitCost)
		}
		if lookupErr != nil {
			lookupErr = h.db.QueryRow(`
				SELECT warehouse_id, COALESCE(unit_cost, 0) FROM inventory
				WHERE tenant_id = $1 AND product_id = $2
				ORDER BY quantity_on_hand DESC LIMIT 1
			`, tenantID, input.ProductID).Scan(&wh, &invUnitCost)
		}
		if lookupErr == nil {
			srcWarehouseID = &wh
		}
	}

	// ── ONE tx: material row + stock issue (applyStockDelta) + consumption
	// JE + cost rollup (integration map §3). ─────────────────────────────
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("AddWorkOrderMaterial: failed to begin tx", "error", txErr)
		response.InternalError(c, "Failed to add material")
		return
	}
	defer tx.Rollback()

	if _, err = tx.Exec(`
		INSERT INTO work_order_materials (id, tenant_id, work_order_id, production_order_id, product_id, product_name, quantity, uom, unit_cost, total_cost, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, tenantID, woID, poID, input.ProductID, productName, input.Quantity, input.UOM, unitCost, totalCost, input.Notes, userID); err != nil {
		response.InternalError(c, "Failed to add material: "+err.Error())
		return
	}

	if srcWarehouseID != nil {
		effectiveUnitCost := invUnitCost
		if effectiveUnitCost == 0 {
			effectiveUnitCost = unitCost
		}
		// AllowNeg matches the old behavior: shop-floor extras were always
		// deducted even past zero (reconcilable via stock count).
		_, valuedCost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
			TenantID:    tenantID,
			OrgID:       organizationID,
			ProductID:   input.ProductID,
			WarehouseID: *srcWarehouseID,
			Qty:         -input.Quantity,
			UnitCost:    effectiveUnitCost,
			TxType:      "issue",
			RefType:     "production_order",
			RefID:       poID.String(),
			Reason:      "material_consumption",
			Notes:       "Work order material consumption",
			CreatedBy:   userID,
			When:        now,
			AllowNeg:    true,
		})
		if dErr != nil {
			h.log.Error("AddWorkOrderMaterial: stock issue failed", "error", dErr, "po_id", poID)
			response.InternalError(c, "Failed to deduct material from stock")
			return
		}

		// GL posting in the SAME tx — DR cost-of-materials / CR inventory.
		// Idempotency key uses the fresh work_order_materials row id; the
		// removal path reverses by this same key. A JE failure poisons the
		// tx so everything above rolls back together.
		h.postInventoryConsumptionJE(tx, postInventoryConsumptionArgs{
			TenantID:       tenantID,
			OrganizationID: organizationID,
			ProductID:      input.ProductID,
			Quantity:       input.Quantity,
			UnitCost:       valuedCost,
			SourceType:     "work_order_material_add",
			SourceID:       id,
			IdempotencyKey: fmt.Sprintf("WO-MAT-%s", id.String()),
			Description: fmt.Sprintf("Work order material — %s × %.2f %s",
				productName, input.Quantity, input.UOM),
		})
	} else {
		h.log.Warn("AddWorkOrderMaterial: no inventory row found — material recorded without stock deduction",
			"product_id", input.ProductID, "po_id", poID)
	}

	// Update production order material_cost and actual_cost
	if _, err = tx.Exec(`
		UPDATE production_orders SET
			material_cost = COALESCE((SELECT SUM(total_cost) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2), 0),
			actual_cost = COALESCE((SELECT SUM(total_cost) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2), 0)
		WHERE id = $1 AND tenant_id = $2`, poID, tenantID); err != nil {
		h.log.Error("AddWorkOrderMaterial: cost rollup failed", "error", err, "po_id", poID)
		response.InternalError(c, "Failed to add material")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("AddWorkOrderMaterial: commit failed", "error", err, "po_id", poID)
		response.InternalError(c, "Failed to add material")
		return
	}

	response.Success(c, gin.H{
		"id":           id,
		"product_id":   input.ProductID,
		"product_name": productName,
		"quantity":     input.Quantity,
		"uom":          input.UOM,
		"unit_cost":    unitCost,
		"total_cost":   totalCost,
		"message":      "Material added",
	})
}

func (h *Handler) RemoveWorkOrderMaterial(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	materialID, err := uuid.Parse(c.Param("material_id"))
	if err != nil {
		response.BadRequest(c, "Invalid material ID")
		return
	}

	userID, _ := middleware.GetUserID(c)
	now := time.Now()

	// Get material details before deleting (for inventory restoration)
	var poID uuid.UUID
	var productID uuid.UUID
	var quantity float64
	err = h.db.QueryRow(`SELECT production_order_id, product_id, quantity FROM work_order_materials WHERE id = $1 AND tenant_id = $2`, materialID, tenantID).Scan(&poID, &productID, &quantity)
	if err != nil {
		response.NotFound(c, "Material not found")
		return
	}

	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	h.db.QueryRow(`SELECT warehouse_id, organization_id FROM production_orders WHERE id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&warehouseID, &organizationID)

	// Resolve the warehouse the material goes back to (mirror of the add
	// path's source resolution).
	var dstWarehouseID *uuid.UUID
	{
		var wh uuid.UUID
		lookupErr := sql.ErrNoRows
		if warehouseID != nil {
			lookupErr = h.db.QueryRow(`
				SELECT warehouse_id FROM inventory
				WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
				LIMIT 1
			`, tenantID, productID, warehouseID).Scan(&wh)
		}
		if lookupErr != nil {
			lookupErr = h.db.QueryRow(`
				SELECT warehouse_id FROM inventory
				WHERE tenant_id = $1 AND product_id = $2
				ORDER BY quantity_on_hand DESC LIMIT 1
			`, tenantID, productID).Scan(&wh)
		}
		if lookupErr == nil {
			dstWarehouseID = &wh
		}
	}

	// ── ONE tx: row delete + stock return (applyStockDelta) + reversal of
	// the add-path consumption JE + cost rollup. ─────────────────────────
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("RemoveWorkOrderMaterial: failed to begin tx", "error", txErr)
		response.InternalError(c, "Failed to remove material")
		return
	}
	defer tx.Rollback()

	result, err := tx.Exec(`DELETE FROM work_order_materials WHERE id = $1 AND tenant_id = $2`, materialID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to remove material")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Material not found")
		return
	}

	if dstWarehouseID != nil {
		// UnitCost 0 → valued at the stored average; the balance row's
		// AVECO cost is untouched by design (positive deltas with zero
		// cost never fold into the average).
		if _, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
			TenantID:    tenantID,
			OrgID:       organizationID,
			ProductID:   productID,
			WarehouseID: *dstWarehouseID,
			Qty:         quantity,
			UnitCost:    0,
			TxType:      "return",
			RefType:     "production_order",
			RefID:       poID.String(),
			Reason:      "material_return",
			Notes:       "Work order material removed",
			CreatedBy:   userID,
			When:        now,
		}); dErr != nil {
			h.log.Error("RemoveWorkOrderMaterial: stock return failed", "error", dErr, "po_id", poID)
			response.InternalError(c, "Failed to restore stock")
			return
		}
	}

	// Reverse the add-path consumption JE (keyed WO-MAT-<materialID>): the
	// stock came back, so the DR-expense/CR-inventory entry must go too, or
	// the Buxgalteriya-vs-Ombor drift reappears. Soft-delete the entry and
	// recompute exactly the accounts its lines touched.
	{
		var origJE uuid.UUID
		tx.QueryRow(`
			SELECT id FROM journal_entries
			WHERE tenant_id = $1 AND reference = $2 AND deleted_at IS NULL
		`, tenantID, fmt.Sprintf("WO-MAT-%s", materialID.String())).Scan(&origJE)
		if origJE != uuid.Nil {
			touched := []string{}
			if aRows, aErr := tx.Query(`SELECT DISTINCT account_id::text FROM journal_entry_lines WHERE journal_entry_id = $1`, origJE); aErr == nil {
				for aRows.Next() {
					var acctID string
					if aRows.Scan(&acctID) == nil {
						touched = append(touched, acctID)
					}
				}
				aRows.Close()
			}
			if _, jeErr := tx.Exec(`UPDATE journal_entries SET deleted_at = $1, updated_at = $1 WHERE id = $2`, now, origJE); jeErr != nil {
				h.log.Error("RemoveWorkOrderMaterial: failed to reverse consumption JE", "error", jeErr, "material_id", materialID)
				response.InternalError(c, "Failed to reverse material journal entry")
				return
			}
			if len(touched) > 0 {
				if _, recErr := tx.Exec(`
					UPDATE accounts a
					SET current_balance = CASE
						WHEN at.normal_balance = 'debit' THEN s.dt - s.kt
						ELSE                                  s.kt - s.dt
					END,
					    updated_at = NOW()
					FROM account_types at,
					     (SELECT acc.id AS account_id,
					             COALESCE(SUM(jel.debit_amount)  FILTER (WHERE je.status = 'posted' AND je.deleted_at IS NULL), 0) AS dt,
					             COALESCE(SUM(jel.credit_amount) FILTER (WHERE je.status = 'posted' AND je.deleted_at IS NULL), 0) AS kt
					      FROM accounts acc
					      LEFT JOIN journal_entry_lines jel ON jel.account_id = acc.id
					      LEFT JOIN journal_entries je ON je.id = jel.journal_entry_id
					      WHERE acc.tenant_id = $1 AND acc.id = ANY($2::uuid[])
					      GROUP BY acc.id) s
					WHERE a.account_type_id = at.id
					  AND s.account_id = a.id
					  AND a.tenant_id = $1;
				`, tenantID, pq.Array(touched)); recErr != nil {
					h.log.Error("RemoveWorkOrderMaterial: balance recompute failed", "error", recErr, "material_id", materialID)
					response.InternalError(c, "Failed to reverse material journal entry")
					return
				}
			}
		}
	}

	// Update production order material_cost and actual_cost
	if _, err = tx.Exec(`
		UPDATE production_orders SET
			material_cost = COALESCE((SELECT SUM(total_cost) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2), 0),
			actual_cost = COALESCE((SELECT SUM(total_cost) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2), 0)
		WHERE id = $1 AND tenant_id = $2`, poID, tenantID); err != nil {
		h.log.Error("RemoveWorkOrderMaterial: cost rollup failed", "error", err, "po_id", poID)
		response.InternalError(c, "Failed to remove material")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("RemoveWorkOrderMaterial: commit failed", "error", err, "po_id", poID)
		response.InternalError(c, "Failed to remove material")
		return
	}

	response.Success(c, gin.H{"message": "Material removed"})
}

// ListWorkOrderAttachments returns all attachments for a work order
func (h *Handler) ListWorkOrderAttachments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, file_name, original_name, mime_type, file_size, storage_path,
		       COALESCE(metadata->>'description', '') as description, created_at
		FROM attachments
		WHERE tenant_id = $1 AND entity_type = 'work_order' AND entity_id = $2
		ORDER BY created_at DESC
	`, tenantID, woID)
	if err != nil {
		response.InternalError(c, "Failed to list attachments")
		return
	}
	defer rows.Close()

	type Attachment struct {
		ID           uuid.UUID `json:"id"`
		FileName     string    `json:"file_name"`
		OriginalName string    `json:"original_name"`
		MimeType     string    `json:"mime_type"`
		FileSize     int64     `json:"file_size"`
		URL          string    `json:"url"`
		Description  string    `json:"description"`
		CreatedAt    time.Time `json:"created_at"`
	}

	var attachments []Attachment
	for rows.Next() {
		var a Attachment
		var storagePath string
		if err := rows.Scan(&a.ID, &a.FileName, &a.OriginalName, &a.MimeType, &a.FileSize, &storagePath, &a.Description, &a.CreatedAt); err != nil {
			continue
		}
		a.URL = "/api/v1/files/" + a.FileName
		attachments = append(attachments, a)
	}
	if attachments == nil {
		attachments = []Attachment{}
	}

	response.Success(c, attachments)
}

// UploadWorkOrderAttachment uploads a file and attaches it to a work order
func (h *Handler) UploadWorkOrderAttachment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	// Get the file from the request
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "No file provided")
		return
	}
	defer file.Close()

	// Validate file size
	if header.Size > h.config.Storage.MaxFileSize {
		response.BadRequest(c, fmt.Sprintf("File size exceeds maximum of %d bytes", h.config.Storage.MaxFileSize))
		return
	}

	// Detect MIME type
	buffer := make([]byte, 512)
	file.Read(buffer)
	mimeType := http.DetectContentType(buffer)
	file.Seek(0, 0)

	// Generate unique file name; storedName is also the public file id served by
	// GET /files/:id, so it must match the uploaded_files row id below.
	randomBytes := make([]byte, 16)
	rand.Read(randomBytes)
	fileID := hex.EncodeToString(randomBytes)
	ext := filepath.Ext(header.Filename)
	storedName := fileID + ext

	// Read the content and store the file in the database (no filesystem).
	content, err := io.ReadAll(file)
	if err != nil {
		response.InternalError(c, "Failed to read file")
		return
	}
	if err := h.insertUploadedFile(storedName, header.Filename, mimeType, int64(len(content)), content, tenantID, userID); err != nil {
		h.log.Error("Failed to store attachment file", "error", err)
		response.InternalError(c, "Failed to save file")
		return
	}

	// Get optional description
	description := c.PostForm("description")
	metadata := fmt.Sprintf(`{"description": "%s"}`, description)

	// Insert into attachments table. storage_path holds the uploaded_files id
	// (no longer a filesystem path) so deletes can clean up the backing row.
	now := time.Now()
	attachID := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO attachments (id, tenant_id, uploaded_by, entity_type, entity_id,
			file_name, original_name, mime_type, file_size, storage_path, metadata)
		VALUES ($1, $2, $3, 'work_order', $4, $5, $6, $7, $8, $9, $10::jsonb)
	`, attachID, tenantID, userID, woID, storedName, header.Filename, mimeType, header.Size, storedName, metadata)
	if err != nil {
		h.log.Error("Failed to save attachment record", "error", err)
		response.InternalError(c, "Failed to save attachment")
		return
	}

	response.Success(c, gin.H{
		"id":            attachID,
		"file_name":     storedName,
		"original_name": header.Filename,
		"mime_type":     mimeType,
		"file_size":     header.Size,
		"url":           "/api/v1/files/" + storedName,
		"description":   description,
		"created_at":    now,
	})
}

// DeleteWorkOrderAttachment removes an attachment from a work order
func (h *Handler) DeleteWorkOrderAttachment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	attachID, err := uuid.Parse(c.Param("attachment_id"))
	if err != nil {
		response.BadRequest(c, "Invalid attachment ID")
		return
	}

	// storage_path now holds the uploaded_files id (see UploadWorkOrderAttachment)
	var storagePath string
	h.db.QueryRow(`SELECT storage_path FROM attachments WHERE id = $1 AND tenant_id = $2`, attachID, tenantID).Scan(&storagePath)

	_, err = h.db.Exec(`DELETE FROM attachments WHERE id = $1 AND tenant_id = $2`, attachID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete attachment")
		return
	}

	// Remove the backing file row from the database
	if storagePath != "" {
		h.db.Exec(`DELETE FROM uploaded_files WHERE id = $1`, storagePath)
	}

	response.Success(c, gin.H{"message": "Attachment deleted"})
}

// =====================================================
// QUALITY CAPTURE (B5) + ISHBAY / PIECE-RATE (B6)
// =====================================================

// CreateWorkOrderQualityCheck — POST /work-orders/:id/quality-check.
// Makes the 011 quality_checks table real: one inspection row per call,
// result derived from the quantities, production_order_id/product_id
// resolved from the work order. RBAC: manufacturing:quality_checks:create.
func (h *Handler) CreateWorkOrderQualityCheck(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	woID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work order ID")
		return
	}

	var input struct {
		QuantityInspected float64 `json:"quantity_inspected" binding:"required,gt=0"`
		QuantityPassed    float64 `json:"quantity_passed"`
		QuantityFailed    float64 `json:"quantity_failed"`
		DefectReason      string  `json:"defect_reason,omitempty"`
		Notes             string  `json:"notes,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	if input.QuantityPassed < 0 || input.QuantityFailed < 0 {
		response.BadRequest(c, "Miqdorlar manfiy bo'lishi mumkin emas")
		return
	}
	// Neither split given → everything not failed passed.
	if input.QuantityPassed == 0 && input.QuantityFailed >= 0 && input.QuantityFailed <= input.QuantityInspected {
		if input.QuantityPassed == 0 && input.QuantityFailed == 0 {
			input.QuantityPassed = input.QuantityInspected
		} else if input.QuantityPassed == 0 {
			input.QuantityPassed = input.QuantityInspected - input.QuantityFailed
		}
	}
	if input.QuantityPassed+input.QuantityFailed > input.QuantityInspected*1.0001 {
		response.BadRequest(c, fmt.Sprintf(
			"O'tgan + brak miqdori tekshirilganidan oshib ketadi: %.2f + %.2f > %.2f",
			input.QuantityPassed, input.QuantityFailed, input.QuantityInspected))
		return
	}

	// Work order lookup, org-scoped (audit §2.9 convention).
	var orgArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgArg = orgID
	}
	var poID *uuid.UUID
	err = h.db.QueryRow(`
		SELECT production_order_id FROM work_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		  AND (organization_id IS NULL OR $3::uuid IS NULL OR organization_id = $3)
	`, woID, tenantID, orgArg).Scan(&poID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Work order not found")
		return
	}
	if err != nil {
		h.log.Error("CreateWorkOrderQualityCheck: WO lookup failed", "error", err)
		response.InternalError(c, "Failed to record quality check")
		return
	}

	var productID *uuid.UUID
	if poID != nil {
		h.db.QueryRow(`SELECT product_id FROM production_orders WHERE id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&productID)
	}

	// Derived result + pass rate.
	result := "partial"
	if input.QuantityFailed <= 0 {
		result = "passed"
	} else if input.QuantityPassed <= 0 {
		result = "failed"
	}
	passRate := input.QuantityPassed / input.QuantityInspected * 100

	var inspectorName string
	h.db.QueryRow(`SELECT COALESCE(first_name || ' ' || last_name, email) FROM users WHERE id = $1`, userID).Scan(&inspectorName)

	// defect_reason: short form into defect_type (VARCHAR(100), the most
	// fitting column), full text into failure_reason.
	defectType := input.DefectReason
	if len(defectType) > 100 {
		defectType = defectType[:100]
	}

	now := time.Now()
	qcID := uuid.New()
	qcCode := "QC-" + now.Format("20060102") + "-" + uuid.New().String()[:6]

	if _, err := h.db.Exec(`
		INSERT INTO quality_checks (
			id, tenant_id, code, production_order_id, work_order_id, product_id,
			inspection_date, inspector_id, inspector_name,
			quantity_inspected, quantity_passed, quantity_failed,
			result, pass_rate, defect_type, failure_reason, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		          NULLIF($15, ''), NULLIF($16, ''), NULLIF($17, ''), $7, $7)
	`, qcID, tenantID, qcCode, poID, woID, productID,
		now, nullableUUID(userID), inspectorName,
		input.QuantityInspected, input.QuantityPassed, input.QuantityFailed,
		result, passRate, defectType, input.DefectReason, input.Notes); err != nil {
		h.log.Error("CreateWorkOrderQualityCheck: insert failed", "error", err, "wo_id", woID)
		response.InternalError(c, "Failed to record quality check")
		return
	}

	response.Created(c, gin.H{
		"id":        qcID,
		"code":      qcCode,
		"result":    result,
		"pass_rate": passRate,
	})
}

// recordPiecework writes the ishbay (piece-rate) HR handoff row when the
// completed operation carries a piece_rate (B6, integration map §6). NO
// payroll_entries writes — payroll consumes production_piecework in its own
// phase. The UNIQUE (work_order_id, employee_id) constraint is the
// double-post guard.
func (h *Handler) recordPiecework(tenantID, woID, userID uuid.UUID, goodQty float64, now time.Time) {
	if goodQty <= 0 {
		return
	}

	var poID *uuid.UUID
	var orgID *uuid.UUID
	var operationName string
	var pieceRate float64
	if err := h.db.QueryRow(`
		SELECT wo.production_order_id, wo.organization_id,
		       COALESCE(bo.operation_name, wo.name, ''), COALESCE(bo.piece_rate, 0)
		FROM work_orders wo
		LEFT JOIN bom_operations bo ON bo.id = wo.operation_id
		WHERE wo.id = $1 AND wo.tenant_id = $2
	`, woID, tenantID).Scan(&poID, &orgID, &operationName, &pieceRate); err != nil || pieceRate <= 0 {
		return
	}

	// Operator resolution order: latest employee-linked time log (459
	// groundwork) → the completing user's employee record → the WO
	// assignee's employee record.
	var employeeID uuid.UUID
	h.db.QueryRow(`
		SELECT employee_id FROM work_order_time_logs
		WHERE work_order_id = $1 AND tenant_id = $2 AND employee_id IS NOT NULL
		ORDER BY start_time DESC LIMIT 1
	`, woID, tenantID).Scan(&employeeID)
	if employeeID == uuid.Nil && userID != uuid.Nil {
		h.db.QueryRow(`SELECT employee_id FROM users WHERE id = $1 AND employee_id IS NOT NULL`, userID).Scan(&employeeID)
	}
	if employeeID == uuid.Nil {
		h.db.QueryRow(`
			SELECT u.employee_id FROM work_orders wo
			JOIN users u ON u.id = wo.assigned_to
			WHERE wo.id = $1 AND u.employee_id IS NOT NULL
		`, woID).Scan(&employeeID)
	}
	if employeeID == uuid.Nil {
		h.log.Info("recordPiecework: operation has piece_rate but no operator resolvable — skipping",
			"wo_id", woID, "piece_rate", pieceRate)
		return
	}

	if _, err := h.db.Exec(`
		INSERT INTO production_piecework (
			id, tenant_id, organization_id, work_order_id, production_order_id,
			employee_id, operation_name, good_quantity, piece_rate, amount, work_date, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::date, $12)
		ON CONFLICT (work_order_id, employee_id) DO NOTHING
	`, uuid.New(), tenantID, orgID, woID, poID,
		employeeID, operationName, goodQty, pieceRate, goodQty*pieceRate, now, now); err != nil {
		h.log.Error("recordPiecework: insert failed", "error", err, "wo_id", woID)
	} else {
		h.log.Info("recordPiecework: ishbay row recorded",
			"wo_id", woID, "employee_id", employeeID, "amount", goodQty*pieceRate)
	}
}

// GetPieceworkSummary — GET /payroll/piecework?from&to&employee_id.
// Per-employee ishbay summary for the HR/payroll handoff (B6): row counts,
// good quantity and total amount per employee in the period. Default period
// is the current month.
func (h *Handler) GetPieceworkSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgArg = orgID
	}

	nowT := time.Now()
	from := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location())
	to := nowT
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			to = t.Add(24*time.Hour - time.Nanosecond)
		}
	}

	query := `
		SELECT pw.employee_id,
		       COALESCE(e.first_name || ' ' || e.last_name, ''),
		       COUNT(*),
		       COALESCE(SUM(pw.good_quantity), 0),
		       COALESCE(SUM(pw.amount), 0)
		FROM production_piecework pw
		LEFT JOIN employees e ON e.id = pw.employee_id
		WHERE pw.tenant_id = $1
		  AND pw.work_date >= $2::date AND pw.work_date <= $3::date
		  AND (pw.organization_id IS NULL OR $4::uuid IS NULL OR pw.organization_id = $4)`
	args := []interface{}{tenantID, from, to, orgArg}
	if s := c.Query("employee_id"); s != "" {
		if empID, err := uuid.Parse(s); err == nil {
			query += " AND pw.employee_id = $5"
			args = append(args, empID)
		}
	}
	query += ` GROUP BY pw.employee_id, e.first_name, e.last_name
	           ORDER BY SUM(pw.amount) DESC`

	type pieceworkSummary struct {
		EmployeeID   uuid.UUID `json:"employee_id"`
		EmployeeName string    `json:"employee_name"`
		Entries      int       `json:"entries"`
		GoodQuantity float64   `json:"good_quantity"`
		TotalAmount  float64   `json:"total_amount"`
	}
	summaries := []pieceworkSummary{}
	totalAmount := 0.0
	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to load piecework summary", "error", err)
		response.InternalError(c, "Failed to load piecework summary")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var s pieceworkSummary
		if rows.Scan(&s.EmployeeID, &s.EmployeeName, &s.Entries, &s.GoodQuantity, &s.TotalAmount) == nil {
			totalAmount += s.TotalAmount
			summaries = append(summaries, s)
		}
	}

	response.Success(c, gin.H{
		"employees":    summaries,
		"total_amount": totalAmount,
		"from":         from.Format("2006-01-02"),
		"to":           to.Format("2006-01-02"),
	})
}
