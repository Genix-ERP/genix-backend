package handler

// MRP netting engine — Ishlab chiqarish v2 strategic phase B1/B8
// (docs/production-audit.md §1 "MRP: backend umuman yo'q",
// docs/production-integration-map.md §6). The 011_manufacturing.sql tables
// mrp_demand / mrp_supply / mrp_recommendations were dead schema until now;
// this file gives them their engine.
//
// Model:
//   POST /mrp/run   — recompute: clears the previous open state, snapshots
//                     demand (open SO lines, open-MO component needs,
//                     reorder-rule safety stock) and supply (on-hand, open
//                     PO lines, open-MO FG output), then nets per product
//                     and writes one 'pending' recommendation per shortage.
//   GET  /mrp/recommendations           — list (default: pending).
//   POST /mrp/recommendations/:id/execute — hand off: 'purchase' creates a
//                     DRAFT purchase requisition (existing requisition→PO
//                     flow takes it from there — no new purchasing path);
//                     'manufacture' creates a DRAFT production order with
//                     source_type='mrp'; when the driving demand is a sales
//                     order the MO gets sales_order_id + customer_id — the
//                     B8 make-to-order handoff.
//
// Conventions/caveats pinned here:
//   - "open" recommendation state is the schema's 'pending' (the 011 CHECK
//     allows pending/accepted/rejected/executed — there is no 'open').
//   - The mrp_* tables carry NO organization_id column (011 schema);
//     gathering is org-scoped via the resolver header, but the persisted
//     run state is tenant-level — two orgs re-running MRP supersede each
//     other's open rows. Acceptable for the netting snapshot semantics
//     (each run replaces the previous one).
//   - Lead times: products carry no lead_time_days column, so purchase lead
//     comes from the product's reorder rule (fallback 7 days) and
//     manufacture lead from the default BOM's operation minutes at an
//     8-hour day (min 1 day).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// openMOStatusesMRP mirrors openMOStatuses minus draft/ready: only orders
// that WILL consume components and WILL produce output feed the netting.
const openMOStatusesMRP = `('confirmed', 'in_progress', 'paused', 'packaging')`

// mrpAudit writes one audit_logs row for an MRP action (contractAudit shape).
func (h *Handler) mrpAudit(tenantID, userID, recID uuid.UUID, action string, oldValues, newValues map[string]interface{}) {
	oldJSON, _ := json.Marshal(oldValues)
	newJSON, _ := json.Marshal(newValues)
	if _, err := h.db.Exec(`
		INSERT INTO audit_logs (id, tenant_id, user_id, action, entity_type, entity_id, old_values, new_values, created_at)
		VALUES ($1, $2, $3, $4, 'mrp_recommendation', $5, $6, $7, $8)
	`, uuid.New(), tenantID, nullableUUID(userID), action, recID, oldJSON, newJSON, time.Now()); err != nil {
		h.log.Error("Failed to write MRP audit log", "error", err, "rec_id", recID)
	}
}

// RunMRP — POST /mrp/run. One transaction: clear → demand → supply → net →
// recommendations.
func (h *Handler) RunMRP(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}
	now := time.Now()

	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("RunMRP: failed to begin transaction", "error", txErr)
		response.InternalError(c, "Failed to run MRP")
		return
	}
	defer tx.Rollback()

	runFail := func(step string, err error) {
		h.log.Error("RunMRP: "+step, "error", err)
		response.InternalError(c, "Failed to run MRP")
	}

	// ── 1. Clear the previous open state ────────────────────────────────
	// Demand/supply flip to 'cancelled' (executed recommendations keep
	// their demand_id FK working); superseded pending recommendations are
	// deleted outright — nothing references them.
	if _, err := tx.Exec(`DELETE FROM mrp_recommendations WHERE tenant_id = $1 AND status = 'pending'`, tenantID); err != nil {
		runFail("failed to clear pending recommendations", err)
		return
	}
	if _, err := tx.Exec(`UPDATE mrp_demand SET status = 'cancelled', updated_at = $2 WHERE tenant_id = $1 AND status = 'open'`, tenantID, now); err != nil {
		runFail("failed to clear open demand", err)
		return
	}
	if _, err := tx.Exec(`UPDATE mrp_supply SET status = 'cancelled', updated_at = $2 WHERE tenant_id = $1 AND status IN ('on_hand', 'expected')`, tenantID, now); err != nil {
		runFail("failed to clear open supply", err)
		return
	}

	var demandRows, supplyRows int64

	// ── 2a. Demand: open sales-order lines (FG demand) ──────────────────
	// source_id = the sales ORDER id (not the line) — the execute handoff
	// resolves sales_order_id/customer_id straight from it (B8).
	if res, err := tx.Exec(`
		INSERT INTO mrp_demand (id, tenant_id, product_id, source_type, source_id, source_reference,
			quantity_required, uom, required_date, priority, warehouse_id, status, created_at, updated_at)
		SELECT gen_random_uuid(), $1, sol.product_id, 'sales_order', so.id, so.order_number,
		       sol.quantity - COALESCE(sol.quantity_delivered, 0),
		       COALESCE(u.code, 'dona'),
		       COALESCE(so.expected_date::date, (NOW() + INTERVAL '7 days')::date),
		       5, sol.warehouse_id, 'open', $3, $3
		FROM sales_order_lines sol
		JOIN sales_orders so ON so.id = sol.sales_order_id
		JOIN products p ON p.id = sol.product_id
		LEFT JOIN units_of_measure u ON u.id = p.unit_id
		WHERE so.tenant_id = $1 AND so.deleted_at IS NULL
		  AND so.status IN ('confirmed', 'processing')
		  AND sol.quantity - COALESCE(sol.quantity_delivered, 0) > 0.0001
		  AND COALESCE(p.track_inventory, true)
		  AND ($2::uuid IS NULL OR so.organization_id = $2)
	`, tenantID, orgArg, now); err != nil {
		runFail("failed to snapshot sales-order demand", err)
		return
	} else {
		n, _ := res.RowsAffected()
		demandRows += n
	}

	// ── 2b. Demand: component needs of open MOs ─────────────────────────
	// Same BOM-demand math as the stats shortages query: remaining planned
	// output × line qty ÷ BOM output qty × (1 + scrap%).
	if res, err := tx.Exec(`
		INSERT INTO mrp_demand (id, tenant_id, product_id, source_type, source_id, source_reference,
			quantity_required, uom, required_date, priority, warehouse_id, status, created_at, updated_at)
		SELECT gen_random_uuid(), $1, bl.component_id, 'production_order', po.id, po.code,
		       bl.quantity
		         * GREATEST(po.quantity_planned - COALESCE(po.quantity_produced, 0), 0)
		         / GREATEST(COALESCE(pb.quantity, 1), 0.0001)
		         * (1 + COALESCE(bl.scrap_percent, 0) / 100.0),
		       COALESCE(u.code, 'dona'),
		       COALESCE(po.scheduled_start::date, CURRENT_DATE),
		       COALESCE(po.priority, 5), po.warehouse_id, 'open', $3, $3
		FROM production_orders po
		JOIN product_boms pb ON pb.id = po.bom_id
		JOIN bom_lines bl ON bl.bom_id = pb.id
		JOIN products cp ON cp.id = bl.component_id AND COALESCE(cp.track_inventory, true)
		LEFT JOIN units_of_measure u ON u.id = cp.unit_id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		  AND po.status IN `+openMOStatusesMRP+`
		  AND ($2::uuid IS NULL OR po.organization_id = $2)
		  AND bl.quantity
		         * GREATEST(po.quantity_planned - COALESCE(po.quantity_produced, 0), 0)
		         / GREATEST(COALESCE(pb.quantity, 1), 0.0001) > 0.0001
	`, tenantID, orgArg, now); err != nil {
		runFail("failed to snapshot production-order component demand", err)
		return
	} else {
		n, _ := res.RowsAffected()
		demandRows += n
	}

	// ── 2c. Demand: reorder rules (safety stock) ────────────────────────
	// Rule fires when on-hand (rule warehouse, or all org warehouses when
	// the rule has none) is below min_qty; demand tops back up to max_qty
	// (min_qty when no max is set).
	if res, err := tx.Exec(`
		INSERT INTO mrp_demand (id, tenant_id, product_id, source_type, source_id, source_reference,
			quantity_required, uom, required_date, priority, warehouse_id, status, created_at, updated_at)
		SELECT gen_random_uuid(), $1, rr.product_id, 'safety_stock', rr.id, 'REORDER',
		       GREATEST(COALESCE(rr.max_qty, rr.min_qty), rr.min_qty) - oh.qty,
		       COALESCE(u.code, 'dona'), CURRENT_DATE, 3, rr.warehouse_id, 'open', $3, $3
		FROM reorder_rules rr
		JOIN products p ON p.id = rr.product_id AND p.deleted_at IS NULL AND COALESCE(p.track_inventory, true)
		LEFT JOIN units_of_measure u ON u.id = p.unit_id
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(i.quantity_on_hand), 0) AS qty
			FROM inventory i
			JOIN warehouses w ON w.id = i.warehouse_id AND w.deleted_at IS NULL
			WHERE i.tenant_id = $1 AND i.product_id = rr.product_id
			  AND (rr.warehouse_id IS NULL OR i.warehouse_id = rr.warehouse_id)
			  AND ($2::uuid IS NULL OR w.organization_id = $2)
		) oh ON true
		WHERE rr.tenant_id = $1 AND COALESCE(rr.is_active, true)
		  AND oh.qty < rr.min_qty
		  AND GREATEST(COALESCE(rr.max_qty, rr.min_qty), rr.min_qty) - oh.qty > 0.0001
	`, tenantID, orgArg, now); err != nil {
		runFail("failed to snapshot reorder-rule demand", err)
		return
	} else {
		n, _ := res.RowsAffected()
		demandRows += n
	}

	// ── 3. Supply: on-hand ──────────────────────────────────────────────
	if res, err := tx.Exec(`
		INSERT INTO mrp_supply (id, tenant_id, product_id, source_type, source_id, source_reference,
			quantity_available, uom, available_date, warehouse_id, status, created_at, updated_at)
		SELECT gen_random_uuid(), $1, i.product_id, 'on_hand', NULL, 'INVENTORY',
		       SUM(i.quantity_on_hand), COALESCE(MIN(u.code), 'dona'), CURRENT_DATE, NULL, 'on_hand', $3, $3
		FROM inventory i
		JOIN warehouses w ON w.id = i.warehouse_id AND w.deleted_at IS NULL
		JOIN products p ON p.id = i.product_id
		LEFT JOIN units_of_measure u ON u.id = p.unit_id
		WHERE i.tenant_id = $1
		  AND ($2::uuid IS NULL OR w.organization_id = $2)
		GROUP BY i.product_id
		HAVING SUM(i.quantity_on_hand) > 0.0001
	`, tenantID, orgArg, now); err != nil {
		runFail("failed to snapshot on-hand supply", err)
		return
	} else {
		n, _ := res.RowsAffected()
		supplyRows += n
	}

	// ── 3b. Supply: open purchase-order lines ───────────────────────────
	if res, err := tx.Exec(`
		INSERT INTO mrp_supply (id, tenant_id, product_id, source_type, source_id, source_reference,
			quantity_available, uom, available_date, warehouse_id, status, created_at, updated_at)
		SELECT gen_random_uuid(), $1, pol.product_id, 'purchase_order', po2.id, po2.order_number,
		       pol.quantity - COALESCE(pol.quantity_received, 0),
		       COALESCE(u.code, 'dona'),
		       COALESCE(po2.expected_date::date, (NOW() + INTERVAL '7 days')::date),
		       pol.warehouse_id, 'expected', $3, $3
		FROM purchase_order_lines pol
		JOIN purchase_orders po2 ON po2.id = pol.purchase_order_id
		JOIN products p ON p.id = pol.product_id
		LEFT JOIN units_of_measure u ON u.id = p.unit_id
		WHERE po2.tenant_id = $1 AND po2.deleted_at IS NULL
		  AND po2.status IN ('approved', 'ordered', 'partial')
		  AND pol.quantity - COALESCE(pol.quantity_received, 0) > 0.0001
		  AND ($2::uuid IS NULL OR po2.organization_id = $2)
	`, tenantID, orgArg, now); err != nil {
		runFail("failed to snapshot purchase-order supply", err)
		return
	} else {
		n, _ := res.RowsAffected()
		supplyRows += n
	}

	// ── 3c. Supply: open-MO finished-goods output ───────────────────────
	if res, err := tx.Exec(`
		INSERT INTO mrp_supply (id, tenant_id, product_id, source_type, source_id, source_reference,
			quantity_available, uom, available_date, warehouse_id, status, created_at, updated_at)
		SELECT gen_random_uuid(), $1, po.product_id, 'production_order', po.id, po.code,
		       GREATEST(po.quantity_planned - COALESCE(po.quantity_produced, 0), 0),
		       COALESCE(po.uom, 'dona'),
		       COALESCE(po.scheduled_end::date, (NOW() + INTERVAL '7 days')::date),
		       po.warehouse_id, 'expected', $3, $3
		FROM production_orders po
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		  AND po.status IN `+openMOStatusesMRP+`
		  AND GREATEST(po.quantity_planned - COALESCE(po.quantity_produced, 0), 0) > 0.0001
		  AND ($2::uuid IS NULL OR po.organization_id = $2)
	`, tenantID, orgArg, now); err != nil {
		runFail("failed to snapshot production-order supply", err)
		return
	} else {
		n, _ := res.RowsAffected()
		supplyRows += n
	}

	// ── 4. Net per product ──────────────────────────────────────────────
	type demandRow struct {
		ID        uuid.UUID
		Ref       string
		Qty       float64
		Required  time.Time
		SourceTyp string
	}
	demandByProduct := map[uuid.UUID][]demandRow{}
	dRows, err := tx.Query(`
		SELECT product_id, id, COALESCE(source_reference, source_type), quantity_required, required_date, source_type
		FROM mrp_demand
		WHERE tenant_id = $1 AND status = 'open'
		ORDER BY product_id, required_date ASC, created_at ASC
	`, tenantID)
	if err != nil {
		runFail("failed to read demand for netting", err)
		return
	}
	for dRows.Next() {
		var pid uuid.UUID
		var d demandRow
		if dRows.Scan(&pid, &d.ID, &d.Ref, &d.Qty, &d.Required, &d.SourceTyp) == nil {
			demandByProduct[pid] = append(demandByProduct[pid], d)
		}
	}
	dRows.Close()

	supplyByProduct := map[uuid.UUID]float64{}
	sRows, err := tx.Query(`
		SELECT product_id, COALESCE(SUM(quantity_available), 0)
		FROM mrp_supply
		WHERE tenant_id = $1 AND status IN ('on_hand', 'expected')
		GROUP BY product_id
	`, tenantID)
	if err != nil {
		runFail("failed to read supply for netting", err)
		return
	}
	for sRows.Next() {
		var pid uuid.UUID
		var qty float64
		if sRows.Scan(&pid, &qty) == nil {
			supplyByProduct[pid] = qty
		}
	}
	sRows.Close()

	recommendations := 0
	for productID, demands := range demandByProduct {
		var totalDemand float64
		for _, d := range demands {
			totalDemand += d.Qty
		}
		net := totalDemand - supplyByProduct[productID]
		if net <= 0.0001 {
			continue
		}

		// Product meta: uom, default BOM, reorder rule (lot size + lead).
		var uom, productName string
		var defaultBOMID uuid.UUID
		var hasBOM bool
		tx.QueryRow(`
			SELECT COALESCE(p.name, ''), COALESCE(u.code, 'dona'),
			       COALESCE((SELECT b.id FROM product_boms b
			                 WHERE b.product_id = p.id AND b.tenant_id = $1
			                   AND COALESCE(b.is_active, true) AND b.deleted_at IS NULL
			                 ORDER BY b.is_default DESC, b.created_at ASC LIMIT 1), '00000000-0000-0000-0000-000000000000'::uuid)
			FROM products p
			LEFT JOIN units_of_measure u ON u.id = p.unit_id
			WHERE p.id = $2
		`, tenantID, productID).Scan(&productName, &uom, &defaultBOMID)
		hasBOM = defaultBOMID != uuid.Nil

		var ruleLotSize float64
		var ruleLeadDays int
		tx.QueryRow(`
			SELECT COALESCE(reorder_qty, 0), COALESCE(lead_time_days, 0)
			FROM reorder_rules
			WHERE tenant_id = $1 AND product_id = $2 AND COALESCE(is_active, true)
			ORDER BY warehouse_id NULLS FIRST LIMIT 1
		`, tenantID, productID).Scan(&ruleLotSize, &ruleLeadDays)

		qty := net
		if ruleLotSize > 0 {
			qty = math.Ceil(net/ruleLotSize) * ruleLotSize
		}

		recType := "purchase"
		leadDays := ruleLeadDays
		if leadDays <= 0 {
			leadDays = 7
		}
		if hasBOM {
			recType = "manufacture"
			// Manufacture lead: default BOM operation minutes for the net
			// quantity at an 8-hour day, min 1 day.
			var totalMinutes float64
			tx.QueryRow(`
				SELECT COALESCE(SUM(COALESCE(bo.setup_time_minutes, 0) + COALESCE(bo.run_time_minutes, 0) * $2), 0)
				FROM bom_operations bo WHERE bo.bom_id = $1
			`, defaultBOMID, qty).Scan(&totalMinutes)
			leadDays = int(math.Ceil(totalMinutes / (60 * 8)))
			if leadDays < 1 {
				leadDays = 1
			}
		}

		earliest := demands[0] // sorted by required_date ASC
		recommendedDate := earliest.Required.AddDate(0, 0, -leadDays)
		urgency := "normal"
		if recommendedDate.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())) {
			urgency = "high"
		}

		// Short human reasoning: top demand sources + supply + shortage.
		parts := []string{}
		for i, d := range demands {
			if i >= 4 {
				parts = append(parts, "…")
				break
			}
			parts = append(parts, fmt.Sprintf("%s: %.0f %s", d.Ref, d.Qty, uom))
		}
		reason := fmt.Sprintf("%s; ta'minot %.0f; yetishmovchilik %.0f",
			strings.Join(parts, ", "), supplyByProduct[productID], net)

		if _, err := tx.Exec(`
			INSERT INTO mrp_recommendations (id, tenant_id, product_id, recommendation_type,
				quantity, uom, recommended_date, due_date, demand_id, priority, urgency,
				status, reason, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8::date, $9, 5, $10, 'pending', $11, $12, $12)
		`, uuid.New(), tenantID, productID, recType, qty, uom,
			recommendedDate, earliest.Required, earliest.ID, urgency, reason, now); err != nil {
			runFail("failed to insert recommendation", err)
			return
		}
		recommendations++
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("RunMRP: commit failed", "error", commitErr)
		response.InternalError(c, "Failed to run MRP")
		return
	}

	h.EmitWorkflowEvent(tenantID, "mrp.run_completed", map[string]interface{}{
		"record_id":       tenantID.String(),
		"demand_rows":     demandRows,
		"supply_rows":     supplyRows,
		"recommendations": recommendations,
	})
	h.mrpAudit(tenantID, userID, uuid.Nil, "mrp.run", nil, map[string]interface{}{
		"demand_rows": demandRows, "supply_rows": supplyRows, "recommendations": recommendations,
	})

	response.Success(c, gin.H{
		"demand_rows":     demandRows,
		"supply_rows":     supplyRows,
		"recommendations": recommendations,
		"run_at":          now.Format(time.RFC3339),
	})
}

// ListMRPRecommendations — GET /mrp/recommendations?status=&type=.
// Default status: 'pending' (the schema's open state); status=all disables
// the filter.
func (h *Handler) ListMRPRecommendations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	status := c.DefaultQuery("status", "pending")
	recType := c.Query("type")

	query := `
		SELECT r.id, r.product_id, COALESCE(p.name, ''), COALESCE(p.code, ''),
		       r.recommendation_type, r.quantity, r.uom,
		       r.recommended_date::text, COALESCE(r.due_date::text, ''),
		       r.priority, r.urgency, r.status, COALESCE(r.reason, ''),
		       COALESCE(r.executed_type, ''), r.executed_id, r.created_at
		FROM mrp_recommendations r
		LEFT JOIN products p ON p.id = r.product_id
		WHERE r.tenant_id = $1`
	args := []interface{}{tenantID}
	argN := 1
	if status != "" && status != "all" {
		argN++
		query += fmt.Sprintf(" AND r.status = $%d", argN)
		args = append(args, status)
	}
	if recType != "" {
		argN++
		query += fmt.Sprintf(" AND r.recommendation_type = $%d", argN)
		args = append(args, recType)
	}
	query += ` ORDER BY CASE r.urgency WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END,
	           r.recommended_date ASC, r.created_at ASC LIMIT 200`

	type recRow struct {
		ID              uuid.UUID  `json:"id"`
		ProductID       uuid.UUID  `json:"product_id"`
		ProductName     string     `json:"product_name"`
		ProductCode     string     `json:"product_code"`
		Type            string     `json:"recommendation_type"`
		Quantity        float64    `json:"quantity"`
		UOM             string     `json:"uom"`
		RecommendedDate string     `json:"recommended_date"`
		DueDate         string     `json:"due_date"`
		Priority        int        `json:"priority"`
		Urgency         string     `json:"urgency"`
		Status          string     `json:"status"`
		Reason          string     `json:"reason"`
		ExecutedType    string     `json:"executed_type,omitempty"`
		ExecutedID      *uuid.UUID `json:"executed_id,omitempty"`
		CreatedAt       time.Time  `json:"created_at"`
	}
	recs := []recRow{}
	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list MRP recommendations", "error", err)
		response.InternalError(c, "Failed to list MRP recommendations")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var r recRow
		if rows.Scan(&r.ID, &r.ProductID, &r.ProductName, &r.ProductCode,
			&r.Type, &r.Quantity, &r.UOM, &r.RecommendedDate, &r.DueDate,
			&r.Priority, &r.Urgency, &r.Status, &r.Reason,
			&r.ExecutedType, &r.ExecutedID, &r.CreatedAt) == nil {
			recs = append(recs, r)
		}
	}

	response.Success(c, recs)
}

// ExecuteMRPRecommendation — POST /mrp/recommendations/:id/execute.
// One transaction: claim 'pending'→'executed', create the target document
// (purchase requisition draft / production order draft), link it back via
// executed_type/executed_id. Events + audit after commit.
func (h *Handler) ExecuteMRPRecommendation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	recID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid recommendation ID")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}
	now := time.Now()

	// Pre-read the recommendation (existence + payload; the in-tx claim is
	// the concurrency guard).
	var productID uuid.UUID
	var recType, uom, reason string
	var qty float64
	var recommendedDate time.Time
	var dueDate sql.NullTime
	var demandID *uuid.UUID
	err = h.db.QueryRow(`
		SELECT product_id, recommendation_type, quantity, uom, recommended_date, due_date, demand_id, COALESCE(reason, '')
		FROM mrp_recommendations
		WHERE id = $1 AND tenant_id = $2
	`, recID, tenantID).Scan(&productID, &recType, &qty, &uom, &recommendedDate, &dueDate, &demandID, &reason)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Recommendation not found")
		return
	}
	if err != nil {
		h.log.Error("ExecuteMRPRecommendation: failed to load recommendation", "error", err)
		response.InternalError(c, "Failed to execute recommendation")
		return
	}
	if recType != "purchase" && recType != "manufacture" {
		response.BadRequest(c, fmt.Sprintf("Bu tavsiya turini avtomatik bajarish qo'llab-quvvatlanmaydi: %s", recType))
		return
	}

	var productName, productCode string
	var productCost float64
	h.db.QueryRow(`SELECT COALESCE(name, ''), COALESCE(code, ''), COALESCE(cost_price, 0) FROM products WHERE id = $1 AND tenant_id = $2`,
		productID, tenantID).Scan(&productName, &productCode, &productCost)

	var userName string
	h.db.QueryRow(`SELECT COALESCE(first_name || ' ' || last_name, email) FROM users WHERE id = $1`, userID).Scan(&userName)
	if userName == "" {
		userName = "MRP"
	}

	requiredDate := recommendedDate
	if dueDate.Valid {
		requiredDate = dueDate.Time
	}

	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("ExecuteMRPRecommendation: failed to begin tx", "error", txErr)
		response.InternalError(c, "Failed to execute recommendation")
		return
	}
	defer tx.Rollback()

	// Atomic claim: pending → executed. Two concurrent executes → one wins.
	claimRes, claimErr := tx.Exec(`
		UPDATE mrp_recommendations
		SET status = 'executed', executed_at = $1, executed_by = $2, updated_at = $1
		WHERE id = $3 AND tenant_id = $4 AND status = 'pending'
	`, now, nullableUUID(userID), recID, tenantID)
	if claimErr != nil {
		h.log.Error("ExecuteMRPRecommendation: claim failed", "error", claimErr)
		response.InternalError(c, "Failed to execute recommendation")
		return
	}
	if n, _ := claimRes.RowsAffected(); n == 0 {
		response.BadRequest(c, "Tavsiya allaqachon bajarilgan yoki bekor qilingan")
		return
	}

	executedType := ""
	executedID := uuid.New()

	if recType == "purchase" {
		// DRAFT purchase requisition (mirrors CreatePurchaseRequisition's
		// INSERT; the existing requisition→PO flow takes it from there).
		executedType = "purchase_requisition"
		prNumber := "PR-" + now.Format("200601") + "-" + uuid.New().String()[:6]
		totalAmount := qty * productCost

		var preferredVendor *uuid.UUID
		var vendorID uuid.UUID
		if tx.QueryRow(`
			SELECT preferred_vendor_id FROM reorder_rules
			WHERE tenant_id = $1 AND product_id = $2 AND preferred_vendor_id IS NOT NULL
			ORDER BY warehouse_id NULLS FIRST LIMIT 1
		`, tenantID, productID).Scan(&vendorID) == nil && vendorID != uuid.Nil {
			preferredVendor = &vendorID
		}

		if _, err := tx.Exec(`
			INSERT INTO purchase_requisitions (
				id, tenant_id, organization_id, pr_number, requested_by, department, request_date,
				required_date, status, priority, total_amount, purpose, notes,
				requested_by_id, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, 'MRP', $6, $7, 'draft', 'medium', $8, $9, $10, $11, $12, $12)
		`, executedID, tenantID, orgIDPtr, prNumber, userName, now,
			requiredDate, totalAmount, "MRP tavsiyasi", reason,
			nullableUUID(userID), now); err != nil {
			h.log.Error("ExecuteMRPRecommendation: requisition insert failed", "error", err)
			response.InternalError(c, "Failed to create purchase requisition")
			return
		}
		if _, err := tx.Exec(`
			INSERT INTO purchase_requisition_lines (
				id, requisition_id, product_id, product_name, product_code, description,
				quantity, unit, estimated_price, total_price, preferred_vendor,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
		`, uuid.New(), executedID, productID, productName, productCode, reason,
			qty, uom, productCost, totalAmount, preferredVendor, now); err != nil {
			h.log.Error("ExecuteMRPRecommendation: requisition line insert failed", "error", err)
			response.InternalError(c, "Failed to create purchase requisition")
			return
		}
	} else {
		// DRAFT production order (CreateProductionOrder's INSERT shape).
		executedType = "production_order"

		var bomID *uuid.UUID
		var bomWarehouseID *uuid.UUID
		var hasSplitOutput bool
		{
			var bID, bWh uuid.UUID
			var bSplit sql.NullBool
			var bWhNull sql.NullString
			if tx.QueryRow(`
				SELECT b.id, COALESCE(b.warehouse_id::text, ''), b.has_split_output
				FROM product_boms b
				WHERE b.product_id = $1 AND b.tenant_id = $2
				  AND COALESCE(b.is_active, true) AND b.deleted_at IS NULL
				ORDER BY b.is_default DESC, b.created_at ASC LIMIT 1
			`, productID, tenantID).Scan(&bID, &bWhNull, &bSplit) == nil && bID != uuid.Nil {
				bomID = &bID
				if bWhNull.Valid && bWhNull.String != "" {
					if parsed, pErr := uuid.Parse(bWhNull.String); pErr == nil {
						bWh = parsed
						bomWarehouseID = &bWh
					}
				}
				hasSplitOutput = bSplit.Valid && bSplit.Bool
			}
		}

		// B8 make-to-order handoff: when the driving demand is a sales
		// order, carry sales_order_id + customer_id onto the MO.
		var salesOrderID, customerID *uuid.UUID
		if demandID != nil {
			var srcType string
			var srcID *uuid.UUID
			if tx.QueryRow(`SELECT source_type, source_id FROM mrp_demand WHERE id = $1 AND tenant_id = $2`,
				demandID, tenantID).Scan(&srcType, &srcID) == nil && srcType == "sales_order" && srcID != nil {
				var custID uuid.UUID
				if tx.QueryRow(`SELECT customer_id FROM sales_orders WHERE id = $1 AND tenant_id = $2`,
					srcID, tenantID).Scan(&custID) == nil {
					salesOrderID = srcID
					if custID != uuid.Nil {
						customerID = &custID
					}
				}
			}
		}

		// MO code — same MAX-based numbering as CreateProductionOrder.
		var maxCode sql.NullString
		tx.QueryRow(`SELECT MAX(code) FROM production_orders WHERE tenant_id = $1 AND code ~ '^MO[0-9]+$'`, tenantID).Scan(&maxCode)
		nextNum := 1
		if maxCode.Valid && len(maxCode.String) > 2 {
			if n, convErr := strconv.Atoi(maxCode.String[2:]); convErr == nil {
				nextNum = n + 1
			}
		}
		moCode := fmt.Sprintf("MO%05d", nextNum)

		moName := "MRP: " + productName
		if _, err := tx.Exec(`
			INSERT INTO production_orders (
				id, tenant_id, organization_id, code, name, product_id, bom_id, quantity_planned, uom,
				mold_count, shift, current_stage,
				scheduled_start, scheduled_end, priority, status, source_type, source_id,
				sales_order_id, customer_id, warehouse_id, location_id, assigned_to,
				work_center_id, requires_quality_check, notes, tags, created_by, created_at, updated_at,
				manufacturing_category_id, has_split_output
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 0, NULL, 'draft',
			          $10, $11, 5, 'draft', 'mrp', $12,
			          $13, $14, $15, NULL, NULL, NULL, false, $16, '[]', $17, $18, $18, NULL, $19)
		`, executedID, tenantID, orgIDPtr, moCode, moName, productID, bomID, qty, uom,
			recommendedDate, requiredDate, recID,
			salesOrderID, customerID, bomWarehouseID, reason,
			nullableUUID(userID), now, hasSplitOutput); err != nil {
			h.log.Error("ExecuteMRPRecommendation: production order insert failed", "error", err)
			response.InternalError(c, "Failed to create production order")
			return
		}
	}

	// Link the created document back onto the recommendation.
	if _, err := tx.Exec(`
		UPDATE mrp_recommendations SET executed_type = $1, executed_id = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5
	`, executedType, executedID, now, recID, tenantID); err != nil {
		h.log.Error("ExecuteMRPRecommendation: failed to link executed document", "error", err)
		response.InternalError(c, "Failed to execute recommendation")
		return
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("ExecuteMRPRecommendation: commit failed", "error", commitErr)
		response.InternalError(c, "Failed to execute recommendation")
		return
	}

	h.EmitWorkflowEvent(tenantID, "mrp.recommendation_executed", map[string]interface{}{
		"record_id":     recID.String(),
		"product_name":  productName,
		"type":          recType,
		"quantity":      qty,
		"executed_type": executedType,
		"executed_id":   executedID.String(),
	})
	h.mrpAudit(tenantID, userID, recID, "mrp_recommendation.executed",
		map[string]interface{}{"status": "pending"},
		map[string]interface{}{"status": "executed", "executed_type": executedType, "executed_id": executedID.String()})

	response.Success(c, gin.H{
		"message":       "Tavsiya bajarildi",
		"executed_type": executedType,
		"executed_id":   executedID,
	})
}
