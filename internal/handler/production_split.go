package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// CompleteSplitOutput finalizes a production order that has has_split_output=true.
// The worker supplies the list of packaged output products with quantities.
// For each product the handler:
//  1. Looks up product.weight (kg per unit)
//  2. Calculates cost_per_kg from BOM + machine costs
//  3. Adds inventory for each packaged product
//  4. Inserts rows into production_split_outputs
//  5. Creates a single journal entry (WIP → Finished Goods)
//  6. Marks the production order as "completed"
func (h *Handler) CompleteSplitOutput(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	poID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	var input entity.CompleteSplitOutputInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Load production order
	var productID uuid.UUID
	var organizationID *uuid.UUID
	var defaultWarehouseID *uuid.UUID
	var bomID *uuid.UUID
	var quantityProduced float64
	var status string
	var hasSplitOutput bool

	err = h.db.QueryRow(`
		SELECT product_id, organization_id, warehouse_id, bom_id,
		       COALESCE(quantity_produced, quantity_planned), status, has_split_output
		FROM production_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, poID, tenantID).Scan(
		&productID, &organizationID, &defaultWarehouseID, &bomID,
		&quantityProduced, &status, &hasSplitOutput,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found")
		return
	}
	if err != nil {
		h.log.Error("CompleteSplitOutput: failed to load PO", "error", err)
		response.InternalError(c, "Failed to load production order")
		return
	}
	if !hasSplitOutput {
		response.BadRequest(c, "This production order does not use split output")
		return
	}
	if status != "packaging" {
		response.BadRequest(c, fmt.Sprintf("Production order must be in 'packaging' status (current: %s)", status))
		return
	}

	// Calculate cost_per_kg = (BOM material cost + machine cost) / quantity_produced_kg
	costPerKg := h.calculateCostPerKg(tenantID, bomID, quantityProduced)

	now := time.Now()
	var outputs []entity.SplitOutputResponse
	var totalFinishedCost float64

	for _, item := range input.Items {
		// Per-piece bulk consumption factor. The bulk is measured in its own unit
		// (m, m³, kg…) and each packaged piece consumes `factor` units of it.
		// Priority: explicit weight (legacy size-factor) > full volume L×W×H >
		// length alone > 1. So a 5.9 m piece with no weight consumes 5.9 per unit;
		// once width & height are filled it switches to true volume L×W×H.
		// (Variable kept as unitWeightKg for diff minimality; it is now a size factor.)
		var productName string
		var unitWeightKg float64
		if scanErr := h.db.QueryRow(`
			SELECT name,
			       CASE
			         WHEN COALESCE(weight, 0) > 0 THEN weight
			         WHEN COALESCE(length, 0) > 0 AND COALESCE(width, 0) > 0 AND COALESCE(height, 0) > 0
			              THEN length * width * height
			         WHEN COALESCE(length, 0) > 0 THEN length
			         ELSE 1
			       END
			FROM products WHERE id = $1 AND tenant_id = $2
		`, item.ProductID, tenantID).Scan(&productName, &unitWeightKg); scanErr != nil {
			response.BadRequest(c, fmt.Sprintf("Product %s not found", item.ProductID))
			return
		}

		totalWeightKg := item.Quantity * unitWeightKg
		unitCost := unitWeightKg * costPerKg

		// Determine warehouse for this item
		warehouseID := defaultWarehouseID
		if item.WarehouseID != nil {
			warehouseID = item.WarehouseID
		}

		// Process additional materials per piece
		var materialCostPerPiece float64
		type materialRecord struct {
			ProductID        uuid.UUID
			QuantityPerPiece float64
			TotalQty         float64
			UnitCost         float64
			TotalCost        float64
		}
		var matRecords []materialRecord

		for _, mat := range item.Materials {
			totalMatQty := mat.QuantityPerPiece * item.Quantity

			// Look up material cost_price
			var matCostPrice float64
			if scanErr := h.db.QueryRow(`
				SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1 AND tenant_id = $2
			`, mat.ProductID, tenantID).Scan(&matCostPrice); scanErr != nil {
				response.BadRequest(c, fmt.Sprintf("Material product %s not found", mat.ProductID))
				return
			}

			matTotalCost := totalMatQty * matCostPrice
			matCostPerUnit := mat.QuantityPerPiece * matCostPrice
			materialCostPerPiece += matCostPerUnit

			// Deduct material from inventory (use production order's warehouse)
			h.consumeSplitMaterial(tenantID, organizationID, poID, mat.ProductID, defaultWarehouseID, userID, totalMatQty, matCostPrice, now)

			matRecords = append(matRecords, materialRecord{
				ProductID:        mat.ProductID,
				QuantityPerPiece: mat.QuantityPerPiece,
				TotalQty:         totalMatQty,
				UnitCost:         matCostPrice,
				TotalCost:        matTotalCost,
			})
		}

		// Add material cost to the unit cost
		unitCost += materialCostPerPiece
		totalCost := item.Quantity * unitCost

		// Add to inventory
		h.receiveSplitProduct(tenantID, organizationID, poID, item.ProductID, warehouseID, userID, item.Quantity, unitCost, now)

		// Insert into production_split_outputs
		var outID uuid.UUID
		if insertErr := h.db.QueryRow(`
			INSERT INTO production_split_outputs (
				id, tenant_id, production_order_id, product_id, product_name,
				quantity, unit_weight_kg, total_weight_kg,
				cost_per_kg, unit_cost, total_cost,
				warehouse_id, created_by, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id
		`, uuid.New(), tenantID, poID, item.ProductID, productName,
			item.Quantity, unitWeightKg, totalWeightKg,
			costPerKg, unitCost, totalCost,
			warehouseID, userID, now,
		).Scan(&outID); insertErr != nil {
			h.log.Error("CompleteSplitOutput: failed to insert split output row", "error", insertErr)
			response.InternalError(c, "Failed to save split output")
			return
		}

		// Insert material records into production_split_output_materials
		for _, mr := range matRecords {
			if _, matInsertErr := h.db.Exec(`
				INSERT INTO production_split_output_materials (
					id, tenant_id, split_output_id, product_id,
					quantity_per_piece, total_quantity, unit_cost, total_cost, created_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			`, uuid.New(), tenantID, outID, mr.ProductID,
				mr.QuantityPerPiece, mr.TotalQty, mr.UnitCost, mr.TotalCost, now,
			); matInsertErr != nil {
				h.log.Error("CompleteSplitOutput: failed to insert split output material", "error", matInsertErr)
			}
		}

		outputs = append(outputs, entity.SplitOutputResponse{
			ID:                outID,
			ProductionOrderID: poID,
			ProductID:         item.ProductID,
			ProductName:       productName,
			Quantity:          item.Quantity,
			UnitWeightKg:      unitWeightKg,
			TotalWeightKg:     totalWeightKg,
			CostPerKg:         costPerKg,
			UnitCost:          unitCost,
			TotalCost:         totalCost,
			WarehouseID:       warehouseID,
			CreatedAt:         now,
		})
		totalFinishedCost += totalCost
	}

	// Calculate leftover: total produced - total split weight
	var totalSplitWeight float64
	for _, out := range outputs {
		totalSplitWeight += out.TotalWeightKg
	}
	leftover := quantityProduced - totalSplitWeight
	if leftover > 0.001 { // small threshold for float precision
		// Add leftover back to inventory as the original bulk product
		leftoverCost := leftover * costPerKg
		if defaultWarehouseID != nil {
			h.receiveSplitProduct(tenantID, organizationID, poID, productID, defaultWarehouseID, userID, leftover, costPerKg, now)
			h.log.Info("CompleteSplitOutput: leftover added to inventory", "product_id", productID, "leftover_qty", leftover, "warehouse_id", defaultWarehouseID)
		}
		totalFinishedCost += leftoverCost
	}

	// Create journal entry (WIP → Finished Goods) for total split output cost
	if totalFinishedCost > 0 {
		h.createSplitOutputJournalEntry(poID, tenantID, organizationID, productID, userID, totalFinishedCost, now)
	}

	// Mark production order as completed
	h.db.Exec(`
		UPDATE production_orders
		SET status = 'completed', current_stage = 'done', progress_percent = 100,
		    actual_cost = $1, material_cost = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4
	`, totalFinishedCost, now, poID, tenantID)

	response.Success(c, gin.H{
		"message":      "Split output completed",
		"outputs":      outputs,
		"total_cost":   totalFinishedCost,
		"cost_per_kg":  costPerKg,
	})
}

// GetSplitOutputs returns the split output rows for a production order.
func (h *Handler) GetSplitOutputs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	poID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, production_order_id, product_id, product_name,
		       quantity, unit_weight_kg, total_weight_kg,
		       cost_per_kg, unit_cost, total_cost,
		       warehouse_id, created_at
		FROM production_split_outputs
		WHERE production_order_id = $1 AND tenant_id = $2
		ORDER BY created_at ASC
	`, poID, tenantID)
	if err != nil {
		h.log.Error("GetSplitOutputs: query failed", "error", err)
		response.InternalError(c, "Failed to get split outputs")
		return
	}
	defer rows.Close()

	var result []entity.SplitOutputResponse
	for rows.Next() {
		var o entity.SplitOutputResponse
		var warehouseID sql.NullString
		if scanErr := rows.Scan(
			&o.ID, &o.ProductionOrderID, &o.ProductID, &o.ProductName,
			&o.Quantity, &o.UnitWeightKg, &o.TotalWeightKg,
			&o.CostPerKg, &o.UnitCost, &o.TotalCost,
			&warehouseID, &o.CreatedAt,
		); scanErr != nil {
			continue
		}
		if warehouseID.Valid {
			wid, _ := uuid.Parse(warehouseID.String)
			o.WarehouseID = &wid
		}
		result = append(result, o)
	}
	if result == nil {
		result = []entity.SplitOutputResponse{}
	}

	response.Success(c, result)
}

// calculateCostPerKg computes (BOM material cost + machine cost) / bulk_qty_kg.
// bulk_qty_kg is the quantity_produced from the production order (in kg).
func (h *Handler) calculateCostPerKg(tenantID uuid.UUID, bomID *uuid.UUID, bulkQtyKg float64) float64 {
	if bulkQtyKg <= 0 || bomID == nil {
		return 0
	}

	var materialCost float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(bl.quantity * COALESCE(p.cost_price, 0) * (1 + COALESCE(bl.scrap_percent, 0) / 100.0)), 0)
		FROM bom_lines bl
		JOIN products p ON p.id = bl.component_id
		WHERE bl.bom_id = $1
	`, bomID).Scan(&materialCost)

	var machineCost float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(
			COALESCE(wc.hourly_cost, 0) / GREATEST(COALESCE(wc.capacity_per_hour, 1), 1)
		), 0)
		FROM bom_operations bo
		LEFT JOIN work_centers wc ON bo.work_center_id = wc.id
		WHERE bo.bom_id = $1
	`, bomID).Scan(&machineCost)

	return (materialCost + machineCost) / bulkQtyKg
}

// receiveSplitProduct adds inventory for one packaged output product.
func (h *Handler) receiveSplitProduct(
	tenantID uuid.UUID, organizationID *uuid.UUID,
	poID, productID uuid.UUID, warehouseID *uuid.UUID,
	userID uuid.UUID, qty, unitCost float64, now time.Time,
) {
	if warehouseID == nil {
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("receiveSplitProduct: begin tx failed", "error", err)
		return
	}
	defer tx.Rollback()

	var invID uuid.UUID
	scanErr := tx.QueryRow(`
		SELECT id FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND lot_number IS NULL AND serial_number IS NULL
	`, tenantID, productID, warehouseID).Scan(&invID)

	if scanErr == sql.ErrNoRows {
		invID = uuid.New()
		if _, insertErr := tx.Exec(`
			INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id,
				quantity_on_hand, quantity_reserved, unit_cost, last_movement_date, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$8,$8)
		`, invID, tenantID, organizationID, productID, warehouseID, qty, unitCost, now); insertErr != nil {
			h.log.Error("receiveSplitProduct: insert inventory failed", "error", insertErr)
			return
		}
	} else if scanErr == nil {
		if _, updateErr := tx.Exec(`
			UPDATE inventory SET quantity_on_hand = quantity_on_hand + $1, unit_cost = $2,
			last_movement_date = $3, updated_at = $3 WHERE id = $4
		`, qty, unitCost, now, invID); updateErr != nil {
			h.log.Error("receiveSplitProduct: update inventory failed", "error", updateErr)
			return
		}
	} else {
		h.log.Error("receiveSplitProduct: query inventory failed", "error", scanErr)
		return
	}

	if _, txErr := tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, organization_id, inventory_id, transaction_type,
			reference_type, reference_id, quantity, unit_cost, total_cost,
			reason, notes, transaction_date, created_by, created_at
		) VALUES ($1,$2,$3,$4,'receipt','production_order',$5,$6,$7,$8,'split_output','Packaged output from split production',$9,$10,$9)
	`, uuid.New(), tenantID, organizationID, invID, poID, qty, unitCost, qty*unitCost, now, userID); txErr != nil {
		h.log.Error("receiveSplitProduct: insert transaction failed", "error", txErr)
		return
	}

	// Update product cost_price
	tx.Exec(`UPDATE products SET cost_price = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4`,
		unitCost, now, productID, tenantID)

	if err := tx.Commit(); err != nil {
		h.log.Error("receiveSplitProduct: commit failed", "error", err)
	}
}

// consumeSplitMaterial deducts a material from inventory for split output material consumption.
func (h *Handler) consumeSplitMaterial(
	tenantID uuid.UUID, organizationID *uuid.UUID,
	poID, productID uuid.UUID, warehouseID *uuid.UUID,
	userID uuid.UUID, qty, unitCost float64, now time.Time,
) {
	if warehouseID == nil || qty <= 0 {
		return
	}

	// Check if the product tracks inventory. Products with track_inventory=false
	// (e.g. water, gas) are infinite-supply — skip the deduction but still record
	// the transaction for cost tracking.
	var trackInventory bool
	if err := h.db.QueryRow(`SELECT COALESCE(track_inventory, true) FROM products WHERE id = $1`, productID).Scan(&trackInventory); err != nil {
		h.log.Error("consumeSplitMaterial: failed to check track_inventory", "product_id", productID, "error", err)
		// Default to tracking if lookup fails
		trackInventory = true
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("consumeSplitMaterial: begin tx failed", "error", err)
		return
	}
	defer tx.Rollback()

	var invID uuid.UUID
	var currentQty float64
	scanErr := tx.QueryRow(`
		SELECT id, quantity_on_hand FROM inventory
		WHERE tenant_id = $1 AND product_id = $2 AND warehouse_id = $3
		AND lot_number IS NULL AND serial_number IS NULL
	`, tenantID, productID, warehouseID).Scan(&invID, &currentQty)

	if scanErr != nil {
		h.log.Error("consumeSplitMaterial: material not found in inventory", "product_id", productID, "error", scanErr)
		return
	}

	// Only deduct quantity if the product tracks inventory
	if trackInventory {
		if _, updateErr := tx.Exec(`
			UPDATE inventory SET quantity_on_hand = quantity_on_hand - $1,
			last_movement_date = $2, updated_at = $2 WHERE id = $3
		`, qty, now, invID); updateErr != nil {
			h.log.Error("consumeSplitMaterial: update inventory failed", "error", updateErr)
			return
		}
	} else {
		h.log.Info("consumeSplitMaterial: skipping inventory deduction for non-tracked product",
			"product_id", productID, "qty", qty)
	}

	// Create inventory transaction (issue) — always recorded for cost tracking
	if _, txErr := tx.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, organization_id, inventory_id, transaction_type,
			reference_type, reference_id, quantity, unit_cost, total_cost,
			reason, notes, transaction_date, created_by, created_at
		) VALUES ($1,$2,$3,$4,'issue','production_order',$5,$6,$7,$8,'split_material_consumption','Additional material consumed during split packaging',$9,$10,$9)
	`, uuid.New(), tenantID, organizationID, invID, poID, qty, unitCost, qty*unitCost, now, userID); txErr != nil {
		h.log.Error("consumeSplitMaterial: insert transaction failed", "error", txErr)
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("consumeSplitMaterial: commit failed", "error", err)
	}
}

// createSplitOutputJournalEntry creates one WIP → Finished Goods JE for the entire split.
func (h *Handler) createSplitOutputJournalEntry(
	poID, tenantID uuid.UUID, organizationID *uuid.UUID,
	primaryProductID, userID uuid.UUID,
	totalCost float64, now time.Time,
) {
	// Prevent duplicates
	var existing int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE tenant_id = $1 AND source_type = 'split_output_complete' AND source_id = $2
	`, tenantID, poID.String()).Scan(&existing)
	if existing > 0 {
		return
	}

	wipAcct := findAccount(h.db, tenantID, organizationID, "work in progress", "2010")
	finishedAcct := findAccount(h.db, tenantID, organizationID, "finished goods", "2810")
	if wipAcct == uuid.Nil || finishedAcct == uuid.Nil {
		return
	}

	var journalID uuid.UUID
	var nextNumber int
	if err := h.db.QueryRow(`
		SELECT id, next_number FROM journals
		WHERE tenant_id = $1 AND type = 'general' AND is_active = true
		ORDER BY created_at ASC LIMIT 1
	`, tenantID).Scan(&journalID, &nextNumber); err != nil || journalID == uuid.Nil {
		return
	}

	var poCode, productName string
	h.db.QueryRow(`SELECT code FROM production_orders WHERE id = $1`, poID).Scan(&poCode)
	h.db.QueryRow(`SELECT name FROM products WHERE id = $1`, primaryProductID).Scan(&productName)

	entryID := uuid.New()
	entryNumber := fmt.Sprintf("MFG%06d", nextEntryNumberSeq(h.db, tenantID, organizationID, "MFG", nextNumber))
	description := fmt.Sprintf("Split output: %s — %s", poCode, productName)

	tx, err := h.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number,
			entry_date, description, source_type, source_id, status, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'split_output_complete',$8,'posted',$9,$10,$10)
	`, entryID, tenantID, organizationID, journalID, entryNumber,
		now, description, poID.String(), userID, now); err != nil {
		return
	}

	// Debit Finished Goods
	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, line_number, debit_amount, credit_amount, description, created_at)
		VALUES ($1,$2,$3,1,$4,0,$5,$6)
	`, uuid.New(), entryID, finishedAcct, totalCost, description, now); err != nil {
		return
	}

	// Credit WIP
	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, line_number, debit_amount, credit_amount, description, created_at)
		VALUES ($1,$2,$3,2,0,$4,$5,$6)
	`, uuid.New(), entryID, wipAcct, totalCost, description, now); err != nil {
		return
	}

	tx.Exec(`UPDATE journals SET next_number = next_number + 1 WHERE id = $1`, journalID)
	tx.Commit()
}
