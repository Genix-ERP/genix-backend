package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

	organizationID := c.Query("organization_id")

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 10000 {
		limit = 100
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
				h.db.Exec(`
					UPDATE production_orders
					SET status = 'completed', current_stage = 'done', progress_percent = 100,
					    quantity_produced = $1, good_quantity = $1, reject_quantity = $2,
					    actual_end = $3, updated_at = $3
					WHERE id = $4 AND tenant_id = $5
				`, lastWoProduced, totalScrapped, now, productionOrderID, tenantID)

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

	// Use migration 010 columns: worker_id, worker_name, duration_hours
	_, err = h.db.Exec(`
		INSERT INTO work_order_time_logs (
			id, tenant_id, work_order_id, start_time, end_time, duration_hours,
			log_type, worker_id, worker_name, notes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, logID, tenantID, woID, startTime, endTime, durationHours,
		input.LogType, userID, userName, input.Notes)
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

	query += " ORDER BY mt.created_at DESC"

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

	response.Success(c, gin.H{"data": transfers})
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
		"message":    "Transfer validated successfully",
		"done_date":  now,
		"next_step":  getNextStepMessage(transferType),
	})
}

// =====================================================
// CREATE TRANSFERS FOR MANUFACTURING ORDER
// =====================================================

// CreateManufacturingTransfers creates pick/store transfers when MO is confirmed
func (h *Handler) CreateManufacturingTransfers(productionOrderID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID) error {
	// Get MO details
	var orgID uuid.UUID
	var warehouseID uuid.UUID
	var orderNumber string
	var bomID uuid.UUID

	err := h.db.QueryRow(`
		SELECT po.organization_id, po.warehouse_id, po.code, po.bom_id
		FROM production_orders po
		WHERE po.id = $1 AND po.tenant_id = $2
	`, productionOrderID, tenantID).Scan(&orgID, &warehouseID, &orderNumber, &bomID)
	if err != nil {
		return err
	}

	// Get warehouse manufacturing steps
	var manufacturingSteps int
	var productionLocationID, stockLocationID sql.NullString
	h.db.QueryRow(`
		SELECT COALESCE(manufacturing_steps, 1), production_location_id,
			   (SELECT id FROM warehouse_locations WHERE warehouse_id = $1 AND location_type = 'stock' LIMIT 1)
		FROM warehouses WHERE id = $1
	`, warehouseID).Scan(&manufacturingSteps, &productionLocationID, &stockLocationID)

	if manufacturingSteps < 2 {
		return nil // 1-step manufacturing, no transfers needed
	}

	// Create Pick Components transfer (for 2 and 3 step)
	pickTransferID := uuid.New()
	pickNumber := fmt.Sprintf("PC/%s", orderNumber)

	_, err = h.db.Exec(`
		INSERT INTO manufacturing_transfers (
			id, tenant_id, organization_id, production_order_id,
			transfer_number, transfer_type, source_location_id, destination_location_id,
			warehouse_id, status, scheduled_date, created_by
		) VALUES ($1, $2, $3, $4, $5, 'pick_components', $6, $7, $8, 'ready', NOW(), $9)
	`, pickTransferID, tenantID, orgID, productionOrderID,
		pickNumber, stockLocationID, productionLocationID, warehouseID, userID)
	if err != nil {
		return err
	}

	// Create transfer lines from BOM components
	rows, err := h.db.Query(`
		SELECT bl.component_id, bl.quantity, bl.unit_of_measure
		FROM bom_lines bl
		WHERE bl.bom_id = $1
	`, bomID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var componentID uuid.UUID
			var qty float64
			var uom string
			rows.Scan(&componentID, &qty, &uom)

			h.db.Exec(`
				INSERT INTO manufacturing_transfer_lines (
					id, manufacturing_transfer_id, product_id, quantity_demanded, uom,
					source_location_id, destination_location_id
				) VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, uuid.New(), pickTransferID, componentID, qty, uom, stockLocationID, productionLocationID)
		}
	}

	// Link pick transfer to production order
	h.db.Exec("UPDATE production_orders SET pick_transfer_id = $1 WHERE id = $2", pickTransferID, productionOrderID)

	// Create Store Finished transfer (for 3 step only)
	if manufacturingSteps >= 3 {
		storeTransferID := uuid.New()
		storeNumber := fmt.Sprintf("SF/%s", orderNumber)

		_, err = h.db.Exec(`
			INSERT INTO manufacturing_transfers (
				id, tenant_id, organization_id, production_order_id,
				transfer_number, transfer_type, source_location_id, destination_location_id,
				warehouse_id, status, created_by
			) VALUES ($1, $2, $3, $4, $5, 'store_finished', $6, $7, $8, 'waiting', $9)
		`, storeTransferID, tenantID, orgID, productionOrderID,
			storeNumber, productionLocationID, stockLocationID, warehouseID, userID)

		// Link store transfer to production order
		h.db.Exec("UPDATE production_orders SET store_transfer_id = $1 WHERE id = $2", storeTransferID, productionOrderID)
	}

	return nil
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
		if wcID.Valid {
			workCenterID, _ = uuid.Parse(wcID.String)
		}

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
			float64(runTime)/60, float64(setupTime)/60,
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
func (h *Handler) consumeBOMComponents(poID, tenantID, userID uuid.UUID, now time.Time) {
	// Guard: skip if already consumed
	var existing int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2 AND transaction_type = 'issue'
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
	defer rows.Close()

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

	for _, comp := range components {
		bomOutputQty := comp.BOMOutputQty
		if bomOutputQty <= 0 {
			bomOutputQty = 1
		}
		totalNeeded := comp.Quantity * (1 + comp.ScrapPercent/100) * (qtyPlanned / bomOutputQty)

		// Deduct from the PO's warehouse; create the inventory row if missing
		// so a zero / negative balance still records against the right
		// warehouse instead of silently skipping the consumption.
		var invID uuid.UUID
		var unitCost float64
		err = tx.QueryRow(`
			SELECT id, COALESCE(unit_cost, 0) FROM inventory
			WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
			AND lot_number IS NULL AND serial_number IS NULL
		`, tenantID, comp.ComponentID, warehouseID).Scan(&invID, &unitCost)
		if err == sql.ErrNoRows {
			invID = uuid.New()
			if _, createErr := tx.Exec(`
				INSERT INTO inventory (
					id, tenant_id, product_id, warehouse_id,
					quantity_on_hand, quantity_reserved,
					last_movement_date, created_at, updated_at
				) VALUES ($1, $2, $3, $4, 0, 0, $5, $5, $5)
			`, invID, tenantID, comp.ComponentID, warehouseID, now); createErr != nil {
				h.log.Error("Failed to create inventory row for work-order consumption",
					"error", createErr, "component_id", comp.ComponentID,
					"warehouse_id", warehouseID)
				continue
			}
			// unit_cost stays 0 for newly-created rows — pull product default.
			h.db.QueryRow("SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1", comp.ComponentID).Scan(&unitCost)
		} else if err != nil {
			continue
		}

		tx.Exec(`UPDATE inventory SET quantity_on_hand = quantity_on_hand - $1, last_movement_date = $2, updated_at = $2 WHERE id = $3`,
			totalNeeded, now, invID)

		tx.Exec(`
			INSERT INTO inventory_transactions (
				id, tenant_id, organization_id, inventory_id, transaction_type,
				reference_type, reference_id, quantity, unit_cost, total_cost,
				reason, notes, transaction_date, created_by, created_at
			) VALUES ($1,$2,$3,$4,'issue','production_order',$5,$6,$7,$8,'material_consumption','Materials consumed for production',  $9,$10,$9)
		`, uuid.New(), tenantID, organizationID, invID, poID, totalNeeded, unitCost, totalNeeded*unitCost, now, userID)
	}

	tx.Commit()
}

// receiveFinishedGoods adds the produced quantity of the finished product to inventory.
// It is idempotent — if Receipt transactions already exist for this PO it does nothing.
func (h *Handler) receiveFinishedGoods(poID, tenantID, userID uuid.UUID, producedQty float64, now time.Time) float64 {
	// Guard: skip if already received
	var existing int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2 AND transaction_type = 'receipt'
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

			// Machine cost from BOM operations (hourly_cost / capacity_per_hour per operation)
			var machineCost float64
			h.db.QueryRow(`
				SELECT COALESCE(SUM(
					COALESCE(wc.hourly_cost, 0)
					/ GREATEST(COALESCE(wc.capacity_per_hour, 1), 1)
				), 0)
				FROM bom_operations bo
				LEFT JOIN work_centers wc ON bo.work_center_id = wc.id
				WHERE bo.bom_id = $1
			`, bomID).Scan(&machineCost)

			unitCost = (materialCost + machineCost) / bomOutputQty
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

	var invID uuid.UUID
	err = tx.QueryRow(`
		SELECT id FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND lot_number IS NULL AND serial_number IS NULL
	`, tenantID, productID, warehouseID).Scan(&invID)

	if err == sql.ErrNoRows {
		invID = uuid.New()
		if _, insertErr := tx.Exec(`
			INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id,
				quantity_on_hand, quantity_reserved, unit_cost, last_movement_date, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$8,$8)
		`, invID, tenantID, organizationID, productID, warehouseID, producedQty, unitCost, now); insertErr != nil {
			h.log.Error("receiveFinishedGoods: failed to insert inventory record", "error", insertErr, "po_id", poID)
			return 0
		}
	} else if err == nil {
		if _, updateErr := tx.Exec(`UPDATE inventory SET quantity_on_hand = quantity_on_hand + $1, last_movement_date = $2, updated_at = $2 WHERE id = $3`,
			producedQty, now, invID); updateErr != nil {
			h.log.Error("receiveFinishedGoods: failed to update inventory", "error", updateErr, "inv_id", invID)
			return 0
		}
	} else {
		h.log.Error("receiveFinishedGoods: failed to query inventory", "error", err, "po_id", poID)
		return 0
	}

	if _, txErr := tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, organization_id, inventory_id, transaction_type,
			reference_type, reference_id, quantity, unit_cost, total_cost,
			reason, notes, transaction_date, created_by, created_at
		) VALUES ($1,$2,$3,$4,'receipt','production_order',$5,$6,$7,$8,'production_complete','Finished goods from production order',$9,$10,$9)
	`, uuid.New(), tenantID, organizationID, invID, poID, producedQty, unitCost, producedQty*unitCost, now, userID); txErr != nil {
		h.log.Error("receiveFinishedGoods: failed to insert inventory_transaction", "error", txErr, "po_id", poID)
		return 0
	}

	// Create inventory lot for FIFO tracking. Leave purchase_order_id NULL —
	// it FKs to purchase_orders(id), and the production_order UUID we have
	// here does not exist there. Pre-fix code stuffed the PO id into that
	// column, which triggered a silent FK violation and aborted the whole
	// transaction — so the inventory update and receipt transaction above
	// were rolled back too. That's why finished goods never showed up in
	// the warehouse. The production-order link is already captured in
	// inventory_transactions (reference_type='production_order').
	lotID := uuid.New()
	lotNumber := fmt.Sprintf("MFG-%s", poID.String()[:8])
	if _, lotErr := tx.Exec(`
		INSERT INTO inventory_lots (
			id, tenant_id, product_id, warehouse_id, lot_number,
			received_date, initial_quantity, remaining_quantity,
			unit_cost, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, 'available', $6, $6)
	`, lotID, tenantID, productID, warehouseID, lotNumber,
		now, producedQty, unitCost); lotErr != nil {
		h.log.Error("receiveFinishedGoods: lot insert failed (non-fatal, continuing)", "error", lotErr, "po_id", poID)
		// Don't return — lot is only for FIFO tracking; the inventory
		// update and receipt transaction are what actually matter.
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("receiveFinishedGoods: failed to commit transaction", "error", commitErr, "po_id", poID)
	} else {
		h.log.Info("receiveFinishedGoods: finished goods added to inventory", "po_id", poID, "qty", producedQty)
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

	// Find or create inventory record in scrap warehouse
	var invID uuid.UUID
	err = tx.QueryRow(`
		SELECT id FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND lot_number IS NULL AND serial_number IS NULL
	`, tenantID, productID, scrapWarehouseID).Scan(&invID)

	if err == sql.ErrNoRows {
		invID = uuid.New()
		if _, insertErr := tx.Exec(`
			INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id,
				quantity_on_hand, quantity_reserved, unit_cost, last_movement_date, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$8,$8)
		`, invID, tenantID, organizationID, productID, scrapWarehouseID, scrapQty, unitCost, now); insertErr != nil {
			h.log.Error("receiveScrapGoods: failed to insert inventory", "error", insertErr)
			return
		}
	} else if err == nil {
		if _, updateErr := tx.Exec(`UPDATE inventory SET quantity_on_hand = quantity_on_hand + $1, last_movement_date = $2, updated_at = $2 WHERE id = $3`,
			scrapQty, now, invID); updateErr != nil {
			h.log.Error("receiveScrapGoods: failed to update inventory", "error", updateErr)
			return
		}
	} else {
		h.log.Error("receiveScrapGoods: failed to query inventory", "error", err)
		return
	}

	// Create inventory transaction for scrap
	if _, txErr := tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, organization_id, inventory_id, transaction_type,
			reference_type, reference_id, quantity, unit_cost, total_cost,
			reason, notes, transaction_date, created_by, created_at
		) VALUES ($1,$2,$3,$4,'receipt','production_order',$5,$6,$7,$8,'production_scrap','Scrapped items from production',$9,$10,$9)
	`, uuid.New(), tenantID, organizationID, invID, poID, scrapQty, unitCost, scrapQty*unitCost, now, userID); txErr != nil {
		h.log.Error("receiveScrapGoods: failed to insert transaction", "error", txErr)
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
//       Dt 1330 Finished Goods    / Ct 1320 WIP = totalCost
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

			h.db.QueryRow(`
				SELECT COALESCE(SUM(COALESCE(wc.hourly_cost, 0) / GREATEST(COALESCE(wc.capacity_per_hour, 1), 1)), 0)
				FROM bom_operations bo LEFT JOIN work_centers wc ON bo.work_center_id = wc.id WHERE bo.bom_id = $1
			`, bomID).Scan(&machineCost)
			machineCost = machineCost / bomOutputQty

			// Labor cost = total - material - machine
			laborCost = unitCost - materialCost - machineCost
			if laborCost < 0 {
				laborCost = 0
			}
		}
	}

	totalMaterialCost := producedQty * materialCost
	totalMachineCost := producedQty * machineCost
	totalLaborCost := producedQty * laborCost

	// Look up accounts
	wipAcct := findAccount(h.db, tenantID, organizationID, "work in progress", "2010")
	rawAcct := findAccount(h.db, tenantID, organizationID, "raw materials", "1030")
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
	entryNumber := fmt.Sprintf("MFG%06d", nextNumber)
	description := fmt.Sprintf("Ishlab chiqarish yakunlandi: %s - %s (soni: %.0f)", poNumber, productName, producedQty)

	if useDetailedFlow {
		// Check if material consumption JE was already created at production start
		var materialJEExists int
		h.db.QueryRow(`
			SELECT COUNT(*) FROM journal_entries
			WHERE tenant_id = $1 AND organization_id = $2
			AND description LIKE '%' || $3 || '%started - materials consumed%'
			AND status = 'posted'
		`, tenantID, organizationID, poNumber).Scan(&materialJEExists)
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

		h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number,
				entry_date, description, source_type, source_id, status, total_debit, total_credit,
				created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, 'production_complete', $8, 'posted', $9, $9, $10, $11, $11)
		`, entryID, tenantID, organizationID, journalID, entryNumber,
			now, description, poID.String(), entryTotal, userID, now)

		lineNum := 1

		// Line 1: Dt WIP / Kt Raw Materials (skip if already done at start)
		if totalMaterialCost > 0 && !materialAlreadyJournalized {
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, 'NJI: xom ashyo sarflandi', $4, 0, $5, $6)`, uuid.New(), entryID, wipAcct, totalMaterialCost, lineNum, now)
			lineNum++
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, 'NJI: xom ashyo sarflandi', 0, $4, $5, $6)`, uuid.New(), entryID, rawAcct, totalMaterialCost, lineNum, now)
			lineNum++
			h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMaterialCost, now, wipAcct)
			h.db.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalMaterialCost, now, rawAcct)
		}

		// Line 2: Dt WIP / Kt Accrued Machine
		if totalMachineCost > 0 && machineAcct != uuid.Nil {
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, 'NJI: stanok xarajatlari', $4, 0, $5, $6)`, uuid.New(), entryID, wipAcct, totalMachineCost, lineNum, now)
			lineNum++
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, 'NJI: stanok xarajatlari', 0, $4, $5, $6)`, uuid.New(), entryID, machineAcct, totalMachineCost, lineNum, now)
			lineNum++
			h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMachineCost, now, wipAcct)
			h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMachineCost, now, machineAcct)
		}

		// Line 3: Dt WIP / Kt Accrued Salary
		if totalLaborCost > 0 && salaryAcct != uuid.Nil {
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, 'NJI: ish haqi xarajatlari', $4, 0, $5, $6)`, uuid.New(), entryID, wipAcct, totalLaborCost, lineNum, now)
			lineNum++
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, 'NJI: ish haqi xarajatlari', 0, $4, $5, $6)`, uuid.New(), entryID, salaryAcct, totalLaborCost, lineNum, now)
			lineNum++
			h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalLaborCost, now, wipAcct)
			h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalLaborCost, now, salaryAcct)
		}

		// Line 4: Dt 1330 Finished Goods / Kt 1320 WIP = totalCost
		h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
			VALUES ($1, $2, $3, 'Tayyor mahsulot ishlab chiqarishdan', $4, 0, $5, $6)`, uuid.New(), entryID, finishedAcct, totalCost, lineNum, now)
		lineNum++
		h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
			VALUES ($1, $2, $3, 'Tayyor mahsulot ishlab chiqarishdan', 0, $4, $5, $6)`, uuid.New(), entryID, wipAcct, totalCost, lineNum, now)

		// Update balances: Finished Goods up, WIP down
		h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalCost, now, finishedAcct)
		h.db.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalCost, now, wipAcct)

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
			h.db.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number,
					entry_date, description, source_type, source_id, status, total_debit, total_credit,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 'production_complete', $8, 'posted', $9, $9, $10, $11, $11)
			`, entryID, tenantID, organizationID, journalID, entryNumber,
				now, description, poID.String(), totalCost, userID, now)

			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, $5, 0, 1, $6)`, uuid.New(), entryID, inventoryAcct, description, totalCost, now)
			h.db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, 0, $5, 2, $6)`, uuid.New(), entryID, cogsAcct, description, totalCost, now)

			h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalCost, now, inventoryAcct)
			h.db.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalCost, now, cogsAcct)
		}
	}

	// Update journal next_number
	h.db.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID)
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

	tx, txErr := h.db.Begin()
	if txErr != nil {
		return
	}
	defer tx.Rollback()

	// 1. Deduct from production warehouse
	tx.Exec(`
		UPDATE inventory SET quantity_on_hand = quantity_on_hand - $1, last_movement_date = $2, updated_at = $2
		WHERE tenant_id = $3 AND product_id = $4 AND warehouse_id = $5
	`, qty, now, tenantID, productID, productionWarehouseID)

	// Create issue transaction from production warehouse
	var srcInvID uuid.UUID
	h.db.QueryRow(`SELECT id FROM inventory WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3 LIMIT 1`,
		tenantID, productID, productionWarehouseID).Scan(&srcInvID)

	tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, organization_id, inventory_id, transaction_type,
			reference_type, reference_id, quantity, unit_cost, total_cost,
			reason, notes, transaction_date, created_by, created_at
		) VALUES ($1,$2,$3,$4,'issue','production_order',$5,$6,$7,$8,'fg_transfer','Tayyor mahsulot omboriga o''tkazildi',$9,$10,$9)
	`, uuid.New(), tenantID, organizationID, srcInvID, poID, qty, unitCost, qty*unitCost, now, userID)

	// 2. Add to finished goods warehouse
	var fgInvID uuid.UUID
	err = tx.QueryRow(`
		SELECT id FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND lot_number IS NULL AND serial_number IS NULL
	`, tenantID, productID, fgWarehouseID).Scan(&fgInvID)

	if err == sql.ErrNoRows {
		fgInvID = uuid.New()
		tx.Exec(`
			INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id,
				quantity_on_hand, quantity_reserved, unit_cost, last_movement_date, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$8,$8)
		`, fgInvID, tenantID, organizationID, productID, fgWarehouseID, qty, unitCost, now)
	} else if err == nil {
		tx.Exec(`UPDATE inventory SET quantity_on_hand = quantity_on_hand + $1, last_movement_date = $2, updated_at = $2 WHERE id = $3`,
			qty, now, fgInvID)
	}

	// Create receipt transaction in FG warehouse
	tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, organization_id, inventory_id, transaction_type,
			reference_type, reference_id, quantity, unit_cost, total_cost,
			reason, notes, transaction_date, created_by, created_at
		) VALUES ($1,$2,$3,$4,'receipt','production_order',$5,$6,$7,$8,'fg_transfer','Ishlab chiqarishdan tayyor mahsulot qabul qilindi',$9,$10,$9)
	`, uuid.New(), tenantID, organizationID, fgInvID, poID, qty, unitCost, qty*unitCost, now, userID)

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
	_, err = h.db.Exec(`
		INSERT INTO work_order_materials (id, tenant_id, work_order_id, production_order_id, product_id, product_name, quantity, uom, unit_cost, total_cost, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, tenantID, woID, poID, input.ProductID, productName, input.Quantity, input.UOM, unitCost, totalCost, input.Notes, userID)
	if err != nil {
		response.InternalError(c, "Failed to add material: "+err.Error())
		return
	}

	// Deduct from inventory (same pattern as consumeBOMComponents)
	{
		var invID uuid.UUID
		var invUnitCost float64
		var invErr error
		if warehouseID != nil {
			invErr = h.db.QueryRow(`
				SELECT id, COALESCE(unit_cost, 0) FROM inventory
				WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
				AND (lot_number IS NULL OR lot_number = '') AND (serial_number IS NULL OR serial_number = '')
				LIMIT 1
			`, tenantID, input.ProductID, warehouseID).Scan(&invID, &invUnitCost)
		}
		// Fallback: find any inventory record for this product
		if invErr != nil || warehouseID == nil {
			invErr = h.db.QueryRow(`
				SELECT id, COALESCE(unit_cost, 0) FROM inventory
				WHERE tenant_id = $1 AND product_id = $2
				AND (lot_number IS NULL OR lot_number = '') AND (serial_number IS NULL OR serial_number = '')
				ORDER BY quantity_on_hand DESC LIMIT 1
			`, tenantID, input.ProductID).Scan(&invID, &invUnitCost)
		}

		if invErr == nil {
			h.db.Exec(`UPDATE inventory SET quantity_on_hand = quantity_on_hand - $1, last_movement_date = $2, updated_at = $2 WHERE id = $3`,
				input.Quantity, now, invID)

			h.db.Exec(`
				INSERT INTO inventory_transactions (
					id, tenant_id, organization_id, inventory_id, transaction_type,
					reference_type, reference_id, quantity, unit_cost, total_cost,
					reason, notes, transaction_date, created_by, created_at
				) VALUES ($1,$2,$3,$4,'issue','production_order',$5,$6,$7,$8,'material_consumption','Work order material consumption',$9,$10,$9)
			`, uuid.New(), tenantID, organizationID, invID, poID, input.Quantity, invUnitCost, input.Quantity*invUnitCost, now, userID)
		}
	}

	// Update production order material_cost and actual_cost
	h.db.Exec(`
		UPDATE production_orders SET
			material_cost = COALESCE((SELECT SUM(total_cost) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2), 0),
			actual_cost = COALESCE((SELECT SUM(total_cost) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2), 0)
		WHERE id = $1 AND tenant_id = $2`, poID, tenantID)

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

	// Get material details before deleting (for inventory restoration)
	var poID uuid.UUID
	var productID uuid.UUID
	var quantity float64
	err = h.db.QueryRow(`SELECT production_order_id, product_id, quantity FROM work_order_materials WHERE id = $1 AND tenant_id = $2`, materialID, tenantID).Scan(&poID, &productID, &quantity)
	if err != nil {
		response.NotFound(c, "Material not found")
		return
	}

	result, err := h.db.Exec(`DELETE FROM work_order_materials WHERE id = $1 AND tenant_id = $2`, materialID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to remove material")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Material not found")
		return
	}

	// Restore inventory
	{
		var warehouseID *uuid.UUID
		h.db.QueryRow(`SELECT warehouse_id FROM production_orders WHERE id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&warehouseID)

		var invID uuid.UUID
		var invErr error
		if warehouseID != nil {
			invErr = h.db.QueryRow(`
				SELECT id FROM inventory
				WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
				AND (lot_number IS NULL OR lot_number = '') AND (serial_number IS NULL OR serial_number = '')
				LIMIT 1
			`, tenantID, productID, warehouseID).Scan(&invID)
		}
		if invErr != nil || warehouseID == nil {
			invErr = h.db.QueryRow(`
				SELECT id FROM inventory
				WHERE tenant_id = $1 AND product_id = $2
				AND (lot_number IS NULL OR lot_number = '') AND (serial_number IS NULL OR serial_number = '')
				ORDER BY quantity_on_hand DESC LIMIT 1
			`, tenantID, productID).Scan(&invID)
		}
		if invErr == nil {
			now := time.Now()
			h.db.Exec(`UPDATE inventory SET quantity_on_hand = quantity_on_hand + $1, last_movement_date = $2, updated_at = $2 WHERE id = $3`,
				quantity, now, invID)

			userID, _ := middleware.GetUserID(c)
			var organizationID *uuid.UUID
			h.db.QueryRow(`SELECT organization_id FROM production_orders WHERE id = $1 AND tenant_id = $2`, poID, tenantID).Scan(&organizationID)

			h.db.Exec(`
				INSERT INTO inventory_transactions (
					id, tenant_id, organization_id, inventory_id, transaction_type,
					reference_type, reference_id, quantity, unit_cost, total_cost,
					reason, notes, transaction_date, created_by, created_at
				) VALUES ($1,$2,$3,$4,'receipt','production_order',$5,$6,0,0,'material_return','Work order material removed',$7,$8,$7)
			`, uuid.New(), tenantID, organizationID, invID, poID, quantity, now, userID)
		}
	}

	// Update production order material_cost and actual_cost
	h.db.Exec(`
		UPDATE production_orders SET
			material_cost = COALESCE((SELECT SUM(total_cost) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2), 0),
			actual_cost = COALESCE((SELECT SUM(total_cost) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2), 0)
		WHERE id = $1 AND tenant_id = $2`, poID, tenantID)

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

	// Generate unique file name
	randomBytes := make([]byte, 16)
	rand.Read(randomBytes)
	fileID := hex.EncodeToString(randomBytes)
	ext := filepath.Ext(header.Filename)
	storedName := fileID + ext

	// Create directory
	now := time.Now()
	dirPath := filepath.Join(h.config.Storage.LocalPath, "uploads", now.Format("2006"), now.Format("01"))
	os.MkdirAll(dirPath, 0755)
	filePath := filepath.Join(dirPath, storedName)

	// Save file
	dst, err := os.Create(filePath)
	if err != nil {
		response.InternalError(c, "Failed to save file")
		return
	}
	defer dst.Close()

	if _, err = io.Copy(dst, file); err != nil {
		response.InternalError(c, "Failed to write file")
		return
	}

	// Save metadata file (same pattern as files.go)
	metaPath := filePath + ".meta"
	metaContent := fmt.Sprintf("%s\n%s\n%d\n%s\n%d", header.Filename, mimeType, header.Size, filePath, now.Unix())
	os.WriteFile(metaPath, []byte(metaContent), 0644)

	// Get optional description
	description := c.PostForm("description")
	metadata := fmt.Sprintf(`{"description": "%s"}`, description)

	// Insert into attachments table
	attachID := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO attachments (id, tenant_id, uploaded_by, entity_type, entity_id,
			file_name, original_name, mime_type, file_size, storage_path, metadata)
		VALUES ($1, $2, $3, 'work_order', $4, $5, $6, $7, $8, $9, $10::jsonb)
	`, attachID, tenantID, userID, woID, storedName, header.Filename, mimeType, header.Size, filePath, metadata)
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

	// Get file path before deleting
	var storagePath string
	h.db.QueryRow(`SELECT storage_path FROM attachments WHERE id = $1 AND tenant_id = $2`, attachID, tenantID).Scan(&storagePath)

	_, err = h.db.Exec(`DELETE FROM attachments WHERE id = $1 AND tenant_id = $2`, attachID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete attachment")
		return
	}

	// Remove file from disk
	if storagePath != "" {
		os.Remove(storagePath)
		os.Remove(storagePath + ".meta")
	}

	response.Success(c, gin.H{"message": "Attachment deleted"})
}
