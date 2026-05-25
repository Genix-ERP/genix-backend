package handler

// construction_work_material_engine.go
//
// Glue between the v2 Bosqichlar (StagesTabV2) approval workflow and the
// existing material_reservations + inventory pipeline. The flow:
//
//   Foreman types BAJARILDI on a work
//     → presses "Tekshiruvga yuborish"
//     → SubmitWork transitions approval_status: in_progress → submitted
//     → reserveMaterialsForWork: one pending material_reservations row per
//       material sub-line of the work, sized as
//         done_quantity × sub.norm_rate
//       against any tenant warehouse that has stock for the resolved product.
//
//   Supervisor confirms (no inventory effect) → ConfirmWorkSupervisor.
//   Chief engineer confirms → ConfirmWorkEngineer.
//     → finaliseMaterialsForWork: every pending reservation for the work
//       is approved, quantity_on_hand is decremented (allowed to go
//       NEGATIVE per product feedback — procurement refills later).
//
//   Either reviewer rejects → RejectWorkSupervisor / RejectWorkEngineer.
//     → cancelMaterialsForWork: every pending reservation for the work
//       is set to 'cancelled' and quantity_reserved is released so the
//       warehouse returns to its pre-submit state.
//
// All three helpers are best-effort. If the lookup for a particular
// resource fails (no matching product, no warehouse with stock, etc.),
// that line is skipped with a warning log; the workflow status update
// itself is never rolled back. The user's primary use-case is "the
// foreman declared X done; record what's been used" — partial coverage
// is much better than a hard failure that prevents work being marked
// done.

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// reserveMaterialsForWork creates one pending reservation per material
// sub-line of `workID`, sized as work.done_quantity × subline.norm_rate.
//
// Idempotency: existing pending reservations for this work are cancelled
// first (releasing reserved qty), then a fresh batch is created based on
// the current done_quantity. This keeps re-submits sane — the foreman can
// rev a number, push, see the consumption, and repeat without leaking
// stale reservation rows.
func (h *Handler) reserveMaterialsForWork(tenantID, orgID, userID uuid.UUID, projectID, workID int64) {
	// 1. Cancel any pending reservations from a previous submit so we
	//    don't double-count after a rev-and-resubmit.
	h.cancelMaterialsForWork(tenantID, workID)

	// 2. Pull the work's done_quantity and every material sub-line.
	//    Labour (labor) and machine (equipment) sub-lines are NOT
	//    reserved — only physical materials get warehouse rows.
	rows, err := h.db.Query(`
		SELECT
		    sub.id,
		    sub.name,
		    COALESCE(sub.uom, ''),
		    COALESCE(sub.norm_rate, 0),
		    COALESCE(sub.unit_rate, 0),
		    COALESCE(parent.done_quantity, 0)
		FROM construction_estimate_line sub
		JOIN construction_estimate_line parent ON parent.id = sub.parent_line_id
		WHERE sub.tenant_id = $1
		  AND sub.parent_line_id = $2
		  AND LOWER(COALESCE(sub.resource_type, '')) = 'material'
	`, tenantID, workID)
	if err != nil {
		h.log.Error("reserveMaterialsForWork: failed to load sub-lines",
			"error", err, "work_id", workID)
		return
	}
	defer rows.Close()

	type subRow struct {
		ID       int64
		Name     string
		UOM      string
		NormRate float64
		UnitRate float64
		DoneQty  float64
	}
	var subs []subRow
	for rows.Next() {
		var s subRow
		if scanErr := rows.Scan(&s.ID, &s.Name, &s.UOM, &s.NormRate, &s.UnitRate, &s.DoneQty); scanErr == nil {
			subs = append(subs, s)
		}
	}

	if len(subs) == 0 {
		// No material sub-lines OR done_quantity not set yet — nothing to do.
		return
	}

	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Project's chosen default warehouse (migration 365). Resolved once
	// before the loop and reused for every sub-line so a single project
	// always charges the same warehouse — no more "random tiebreaker"
	// behaviour where each sub-line could land on a different warehouse
	// just because of inventory row order. Stays nil when the project
	// hasn't picked one, in which case the existing auto-pick chain
	// (highest-stock → oldest-active) takes over downstream.
	var projectWarehouseID uuid.NullUUID
	var projectOrgID uuid.NullUUID
	_ = h.db.QueryRow(`
		SELECT warehouse_id, organization_id
		FROM construction_projects
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&projectWarehouseID, &projectOrgID)

	now := time.Now()
	created := 0
	skipped := 0

	for _, s := range subs {
		// Consumed quantity = parent.done_quantity × subline.norm_rate.
		// (sub.quantity is the planned consumption based on parent.quantity;
		// for the actual/fact, we re-scale by done_quantity.)
		consumed := s.DoneQty * s.NormRate
		if consumed <= 0 {
			continue
		}

		// Resolve product by case-insensitive name match. The auto-create
		// pipeline stores resurs items with the exact same name string,
		// so this catches everything that has been ingested at least once.
		var productID uuid.UUID
		err := h.db.QueryRow(`
			SELECT id FROM products
			WHERE tenant_id = $1
			  AND deleted_at IS NULL
			  AND UPPER(name) = UPPER($2)
			ORDER BY created_at DESC
			LIMIT 1
		`, tenantID, s.Name).Scan(&productID)
		if err != nil || productID == uuid.Nil {
			h.log.Warn("reserveMaterialsForWork: no matching product for sub-line",
				"work_id", workID, "sub_line_id", s.ID, "name", s.Name)
			skipped++
			continue
		}

		// Pick a warehouse. Multi-step chain, biased toward the org the
		// CONFIRMING USER is currently looking at, because the products
		// tab in Inventory.jsx filters out inventory rows whose
		// warehouse.organization_id != activeCompany.id. If the engine
		// picks an org-mismatched warehouse, the deduction ends up
		// "invisible" to the very user who just confirmed the work
		// ("Qoldiq yo'q" even though the DB has -N) — that's the bug
		// reported as "i created new product and confirmed stage
		// completion but inventory not changed".
		//
		//   0. Project's chosen default (construction_projects.warehouse_id,
		//      migration 365) — explicit user pick, always wins.
		//   1. CONFIRMING USER's active org — highest-stock for this product.
		//      Lets the user-visible inventory show the decrement.
		//   2. CONFIRMING USER's active org — oldest active warehouse.
		//   3. Project's org — highest-stock for this product. Falls back
		//      when the confirming user's org doesn't have a warehouse
		//      to lay the row into.
		//   4. Project's org — oldest active warehouse.
		//   5. Tenant-wide oldest active warehouse — only when neither
		//      org has a usable warehouse. Stock may end up invisible
		//      to the current user, but writing a row is better than
		//      dropping silently.
		var warehouseID uuid.UUID
		if projectWarehouseID.Valid {
			warehouseID = projectWarehouseID.UUID
		}
		if warehouseID == uuid.Nil && orgID != uuid.Nil {
			_ = h.db.QueryRow(`
				SELECT inv.warehouse_id
				FROM inventory inv
				JOIN warehouses w ON w.id = inv.warehouse_id
				WHERE inv.product_id = $1 AND inv.tenant_id = $2
				  AND w.organization_id = $3
				ORDER BY inv.quantity_on_hand DESC NULLS LAST
				LIMIT 1
			`, productID, tenantID, orgID).Scan(&warehouseID)
		}
		if warehouseID == uuid.Nil && orgID != uuid.Nil {
			_ = h.db.QueryRow(`
				SELECT id FROM warehouses
				WHERE tenant_id = $1
				  AND organization_id = $2
				  AND COALESCE(is_active, true) = true
				ORDER BY created_at LIMIT 1
			`, tenantID, orgID).Scan(&warehouseID)
		}
		if warehouseID == uuid.Nil && projectOrgID.Valid {
			_ = h.db.QueryRow(`
				SELECT inv.warehouse_id
				FROM inventory inv
				JOIN warehouses w ON w.id = inv.warehouse_id
				WHERE inv.product_id = $1 AND inv.tenant_id = $2
				  AND w.organization_id = $3
				ORDER BY inv.quantity_on_hand DESC NULLS LAST
				LIMIT 1
			`, productID, tenantID, projectOrgID.UUID).Scan(&warehouseID)
		}
		if warehouseID == uuid.Nil && projectOrgID.Valid {
			_ = h.db.QueryRow(`
				SELECT id FROM warehouses
				WHERE tenant_id = $1
				  AND organization_id = $2
				  AND COALESCE(is_active, true) = true
				ORDER BY created_at LIMIT 1
			`, tenantID, projectOrgID.UUID).Scan(&warehouseID)
		}
		if warehouseID == uuid.Nil {
			_ = h.db.QueryRow(`
				SELECT warehouse_id FROM inventory
				WHERE product_id = $1 AND tenant_id = $2
				ORDER BY quantity_on_hand DESC NULLS LAST
				LIMIT 1
			`, productID, tenantID).Scan(&warehouseID)
		}
		if warehouseID == uuid.Nil {
			_ = h.db.QueryRow(`
				SELECT id FROM warehouses
				WHERE tenant_id = $1 AND COALESCE(is_active, true) = true
				ORDER BY created_at LIMIT 1
			`, tenantID).Scan(&warehouseID)
		}

		// Make the product visible in the picked warehouse's org so the
		// inventory list shows the deduction. Without this, products
		// created via Yangi Mahsulot are linked only to the user's
		// creating org; if the picker chose a different-org warehouse
		// (project default, or a fallback), the deduction lands in a
		// row the user can't see. Idempotent via ON CONFLICT.
		if warehouseID != uuid.Nil {
			_, _ = h.db.Exec(`
				INSERT INTO product_organization_settings (
					tenant_id, product_id, organization_id,
					cost_price, list_price, min_price,
					min_stock_level, reorder_point, reorder_quantity
				)
				SELECT
					$1, $2, w.organization_id,
					COALESCE((SELECT cost_price FROM products WHERE id = $2), 0),
					COALESCE((SELECT list_price FROM products WHERE id = $2), 0),
					0, 0, 0, 0
				FROM warehouses w
				WHERE w.id = $3 AND w.organization_id IS NOT NULL
				ON CONFLICT (product_id, organization_id) DO NOTHING
			`, tenantID, productID, warehouseID)
		}

		var warehouseIDPtr *uuid.UUID
		if warehouseID != uuid.Nil {
			warehouseIDPtr = &warehouseID
		}

		totalCost := consumed * s.UnitRate
		notes := fmt.Sprintf("Auto-reserved on work submit (work #%d)", workID)

		resID := uuid.New()
		// stage_id / substage_id stay NULL — migration 354 relaxed those
		// columns specifically for this estimate-line based flow.
		_, insErr := h.db.Exec(`
			INSERT INTO material_reservations (
				id, tenant_id, organization_id,
				project_id, stage_id, substage_id, estimate_line_id,
				product_id, warehouse_id, quantity, unit, unit_cost, total_cost,
				status, requested_by, notes, created_at, updated_at
			) VALUES (
				$1, $2, $3,
				$4, NULL, NULL, $5,
				$6, $7, $8, $9, $10, $11,
				'pending', $12, $13, $14, $14
			)
		`, resID, tenantID, orgIDPtr,
			projectID, workID,
			productID, warehouseIDPtr, consumed, s.UOM, s.UnitRate, totalCost,
			uuidArg(userID), notes, now)
		if insErr != nil {
			h.log.Error("reserveMaterialsForWork: failed to insert reservation",
				"error", insErr, "work_id", workID, "product_id", productID, "qty", consumed)
			skipped++
			continue
		}

		// Increment quantity_reserved (does NOT touch quantity_on_hand
		// yet — the on-hand decrement happens at engineer confirm).
		// This is allowed to make quantity_reserved exceed quantity_on_hand
		// since stock can run negative.
		if warehouseIDPtr != nil {
			_, _ = h.db.Exec(`
				UPDATE inventory
				SET quantity_reserved = COALESCE(quantity_reserved, 0) + $1, updated_at = $2
				WHERE product_id = $3 AND warehouse_id = $4 AND tenant_id = $5
			`, consumed, now, productID, warehouseIDPtr, tenantID)
		}

		created++
	}

	h.log.Info("reserveMaterialsForWork: complete",
		"work_id", workID, "created", created, "skipped", skipped)
}

// finaliseMaterialsForWork approves every pending reservation tied to the
// work, decrementing quantity_on_hand AND quantity_reserved by the
// reservation qty AND recording a draft construction_expense_lines row so
// the project's Xarajatlar tab reflects what was consumed.
//
// Allows quantity_on_hand to go negative — per product feedback the user
// wants the system to record reality even if procurement hasn't refilled
// the warehouse yet ("ombordan ostatka ayrilmadi minusga bolsa ham" bug).
//
// If the inventory row for (product, warehouse) doesn't exist yet we
// INSERT it at the negative balance instead of silently no-op'ing the
// UPDATE — the page would otherwise keep showing Qoldiq=0 forever.
//
// Each approved reservation also produces one construction_expense_lines
// row tagged with material_request_id=NULL but enough metadata
// (product_id, qty, uom, unit_price, amount, supplier_name) for the
// Xarajatlar tab to surface it. Without this the section's Tasdiqlangan
// xarajatlar / Jami totals stayed at 0 even after a YAKUNIY work
// ("bu joylardi xarajatlar hisoblanmadi" bug).
func (h *Handler) finaliseMaterialsForWork(tenantID, userID uuid.UUID, projectID, workID int64) {
	// We need extra metadata per reservation (cost, uom, organization,
	// product name) to build the expense line — pull it inline.
	rows, err := h.db.Query(`
		SELECT id, organization_id, product_id, warehouse_id, quantity,
		       COALESCE(unit, ''), COALESCE(unit_cost, 0), COALESCE(total_cost, 0)
		FROM material_reservations
		WHERE tenant_id = $1
		  AND estimate_line_id = $2
		  AND status = 'pending'
		  AND deleted_at IS NULL
	`, tenantID, workID)
	if err != nil {
		h.log.Error("finaliseMaterialsForWork: failed to load reservations",
			"error", err, "work_id", workID)
		return
	}
	defer rows.Close()

	type resRow struct {
		ID          uuid.UUID
		OrgID       *uuid.UUID
		ProductID   uuid.UUID
		WarehouseID *uuid.UUID
		Quantity    float64
		Unit        string
		UnitCost    float64
		TotalCost   float64
	}
	var ress []resRow
	for rows.Next() {
		var r resRow
		var org, wh uuid.NullUUID
		if scanErr := rows.Scan(&r.ID, &org, &r.ProductID, &wh, &r.Quantity,
			&r.Unit, &r.UnitCost, &r.TotalCost); scanErr == nil {
			if org.Valid {
				v := org.UUID
				r.OrgID = &v
			}
			if wh.Valid {
				w := wh.UUID
				r.WarehouseID = &w
			}
			ress = append(ress, r)
		}
	}

	// Resolve the work line metadata up front. We need it for both the
	// section→stage lookup AND the work-level expense line at the end.
	// Pulling once here keeps the inner loop compact.
	var (
		workName       string
		workUOM        string
		sectionPath    sql.NullString
		workQty        float64
		workDone       float64
		workUnitRate   float64
		workTotalAmt   float64
		workOrgID      sql.NullString
	)
	// `organization_id` lives on construction_projects, not on
	// construction_estimate. Joining through the project keeps the
	// query honest (the previous SELECT against e.organization_id
	// silently returned NULL because the column doesn't exist on the
	// estimate header — that left expense rows untagged with org).
	_ = h.db.QueryRow(`
		SELECT COALESCE(l.name, ''),
		       COALESCE(l.uom, ''),
		       l.parent_item_number,
		       COALESCE(l.quantity, 0),
		       COALESCE(l.done_quantity, 0),
		       COALESCE(l.unit_rate, 0),
		       COALESCE(l.total_amount, 0),
		       (SELECT cp.organization_id::text
		        FROM construction_estimate e
		        JOIN construction_projects cp ON cp.id = e.project_id
		        WHERE e.id = l.estimate_id AND e.tenant_id = l.tenant_id)
		FROM construction_estimate_line l
		WHERE l.id = $1 AND l.tenant_id = $2
	`, workID, tenantID).Scan(
		&workName, &workUOM, &sectionPath,
		&workQty, &workDone, &workUnitRate, &workTotalAmt, &workOrgID,
	)

	// Sub-resource derived rate — Σ(sub.unit_rate × sub.norm_rate) — so
	// works with zero stored unit_rate (common for ВОР-imported parents)
	// still produce a non-zero expense line. Mirrors the same fallback
	// used by the Bosqichlar table and the Reja vs Fakt aggregation.
	var subDerivedRate float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(COALESCE(s.unit_rate, 0) * COALESCE(s.norm_rate, 0)), 0)
		FROM construction_estimate_line s
		WHERE s.parent_line_id = $1
		  AND s.tenant_id = $2
		  AND COALESCE(s.resource_type, '') <> ''
	`, workID, tenantID).Scan(&subDerivedRate)

	// Effective per-unit cost. Prefer stored unit_rate; back-compute from
	// total_amount / quantity if it's zero; finally fall back to the
	// sub-resource derived rate.
	effectiveRate := workUnitRate
	if effectiveRate <= 0 && workQty > 0 && workTotalAmt > 0 {
		effectiveRate = workTotalAmt / workQty
	}
	if effectiveRate <= 0 {
		effectiveRate = subDerivedRate
	}

	// Resolve the work's section → stage_id once. The Byudjet tab joins
	// expenses to sections via stage_id; without it the auto-generated
	// row falls into the synthetic "(Boshqalar)" bucket instead of
	// attributing to the right section.
	//
	// We have to be building-aware: when the user filters Byudjet to a
	// specific Block (Bosqichlar tab building filter), the per-building
	// totalActual query inner-joins construction_stages with
	// building_id = <selected>. So a stage match for the wrong block
	// would correctly attribute to a section but would still miss the
	// per-block filter — this is what the user reported as "Fakt still
	// 0 under Block 2 even after YAKUNIY". We pull the work's
	// building_id from its estimate and prefer a stage in the same
	// building. Falling back to a name-only match keeps backwards
	// compat for projects whose stages were created before migration
	// 333 added building_id.
	var workBuildingID sql.NullInt64
	_ = h.db.QueryRow(`
		SELECT e.building_id
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE l.id = $1 AND l.tenant_id = $2
	`, workID, tenantID).Scan(&workBuildingID)

	var stageIDForExpense sql.NullInt64
	if sectionPath.Valid && sectionPath.String != "" {
		var stageID int64
		// Prefer a stage in the same building.
		if workBuildingID.Valid {
			if err := h.db.QueryRow(`
				SELECT id
				FROM construction_stages
				WHERE tenant_id = $1
				  AND project_id = $2
				  AND name = $3
				  AND building_id = $4
				ORDER BY id ASC
				LIMIT 1
			`, tenantID, projectID, sectionPath.String, workBuildingID.Int64).Scan(&stageID); err == nil {
				stageIDForExpense = sql.NullInt64{Int64: stageID, Valid: true}
			}
		}
		// Fallback: any stage with the matching name.
		if !stageIDForExpense.Valid {
			if err := h.db.QueryRow(`
				SELECT id
				FROM construction_stages
				WHERE tenant_id = $1
				  AND project_id = $2
				  AND name = $3
				ORDER BY id ASC
				LIMIT 1
			`, tenantID, projectID, sectionPath.String).Scan(&stageID); err == nil {
				stageIDForExpense = sql.NullInt64{Int64: stageID, Valid: true}
			}
		}
		// Auto-create fallback. StagesTabV2 derives sections directly from
		// estimate-line parent_item_numbers — there is no UI step that
		// creates rows in construction_stages for those sections. So a
		// project can have YAKUNIY works in a section that has no
		// matching stage row, which would force stage_id=NULL on the
		// expense line and hide it from the per-Block Fakt total (the
		// report INNER-JOINs construction_stages and filters by
		// building_id). To avoid that, we create the stage on demand,
		// tagged with the work's building_id when known. The stage_order
		// is set to MAX+1 within the project so it sorts after existing
		// rows and doesn't collide.
		if !stageIDForExpense.Valid {
			var bidArg interface{}
			if workBuildingID.Valid {
				bidArg = workBuildingID.Int64
			} else {
				bidArg = nil
			}
			var newStageID int64
			if err := h.db.QueryRow(`
				INSERT INTO construction_stages (
					tenant_id, project_id, building_id, name,
					stage_order, status, planned_budget,
					created_at, updated_at
				)
				VALUES (
					$1, $2, $3, $4,
					COALESCE((
						SELECT MAX(stage_order) + 1
						FROM construction_stages
						WHERE tenant_id = $1 AND project_id = $2
					), 1),
					'pending', 0,
					NOW(), NOW()
				)
				RETURNING id
			`, tenantID, projectID, bidArg, sectionPath.String).Scan(&newStageID); err == nil {
				stageIDForExpense = sql.NullInt64{Int64: newStageID, Valid: true}
			} else {
				h.log.Error("finaliseMaterialsForWork: stage auto-create failed",
					"error", err, "section", sectionPath.String, "project_id", projectID)
			}
		}
	}

	// Resolve the company name once — used as supplier_name for the
	// expense line so the row reads as an internal-stock issue rather
	// than a blank vendor.
	var companyName string
	{
		var orgIDForName *uuid.UUID
		if len(ress) > 0 && ress[0].OrgID != nil {
			orgIDForName = ress[0].OrgID
		} else if workOrgID.Valid {
			if u, err := uuid.Parse(workOrgID.String); err == nil {
				orgIDForName = &u
			}
		}
		if orgIDForName != nil {
			_ = h.db.QueryRow(`SELECT COALESCE(name, '') FROM organizations WHERE id = $1`, *orgIDForName).Scan(&companyName)
		}
	}

	now := time.Now()
	approved := 0
	for _, r := range ress {
		// Atomic reservation-approve + inventory-decrement.
		//
		// Previously the status UPDATE and the inventory UPDATE/INSERT
		// were separate non-transactional Exec calls. When the
		// inventory step silently no-op'd (UPDATE matched 0 rows AND
		// INSERT failed for any reason — e.g. a constraint hiccup, a
		// dropped connection, or even just the unique-index race when
		// two reservations for the same product land at once), the
		// reservation stayed status='approved' without the
		// corresponding deduction. Result: reservation table says the
		// material was consumed, inventory table doesn't agree, and
		// finaliseMaterialsForWork's WHERE status='pending' filter
		// means a re-run can't find the row to retry. That's exactly
		// the "Test inventory 123 has approved reservations but no
		// inventory row" state the user reported.
		//
		// Wrapping both writes in a single tx + ON CONFLICT keeps
		// them atomic: either both succeed, or neither does and the
		// reservation stays pending so the next confirm attempt can
		// retry.
		tx, txErr := h.db.Begin()
		if txErr != nil {
			h.log.Error("finaliseMaterialsForWork: failed to start tx",
				"error", txErr, "reservation_id", r.ID)
			continue
		}

		if _, upErr := tx.Exec(`
			UPDATE material_reservations
			SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $2
			WHERE id = $3
		`, uuidArg(userID), now, r.ID); upErr != nil {
			h.log.Error("finaliseMaterialsForWork: failed to mark approved",
				"error", upErr, "reservation_id", r.ID)
			tx.Rollback()
			continue
		}

		// Single-statement UPSERT against the unique index
		// (tenant_id, product_id, warehouse_id) added in migration
		// 282. Decrement existing rows in place; insert a -consumed
		// row when none exists. Allowed to go negative — the runtime
		// has always permitted that for YAKUNIY consumption when the
		// procurement side hasn't refilled yet.
		if r.WarehouseID != nil {
			// CAST $4 to numeric — without it Postgres can't resolve the
			// unary `-` (and the inline `-$4` in VALUES) and errors with
			// "operator is not unique: - unknown". The driver sends the
			// float64 parameter as type 'unknown' and Postgres has
			// multiple negation operators to choose from.
			if _, exErr := tx.Exec(`
				INSERT INTO inventory (
					id, tenant_id, product_id, warehouse_id,
					quantity_on_hand, quantity_reserved,
					created_at, updated_at
				) VALUES (gen_random_uuid(), $1, $2, $3, (0 - $4::numeric), 0, $5, $5)
				ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE
				SET quantity_on_hand  = inventory.quantity_on_hand - $4::numeric,
				    quantity_reserved = GREATEST(COALESCE(inventory.quantity_reserved, 0) - $4::numeric, 0::numeric),
				    updated_at        = $5
			`, tenantID, r.ProductID, r.WarehouseID, r.Quantity, now); exErr != nil {
				h.log.Error("finaliseMaterialsForWork: failed to upsert inventory",
					"error", exErr, "reservation_id", r.ID,
					"product_id", r.ProductID, "warehouse_id", r.WarehouseID)
				tx.Rollback()
				continue
			}
		}

		if cErr := tx.Commit(); cErr != nil {
			h.log.Error("finaliseMaterialsForWork: tx commit failed",
				"error", cErr, "reservation_id", r.ID)
			continue
		}

		// GL posting — DR cost-of-materials / CR inventory, posted
		// AFTER the inventory-decrement tx commits so a transient JE
		// failure (missing journal, missing chart accounts, trigger
		// hiccup) never rolls back the user's reservation approval.
		// Before this call, this loop wrote only to
		// construction_expense_lines (a module-local cost row), never
		// to journal_entries. That left the inventory GL account
		// untouched when goods left stock, which is what produced
		// the +334M Buxgalteriya-vs-Ombor drift across EVROPLIT and
		// LUXURYMEBEL by mid-2026.
		//
		// Trade-off: if the server crashes between commit and JE
		// post, we have stock decremented but no JE. The reconcile
		// admin endpoint (/admin/inventory-reconcile) will surface
		// that gap, and the idempotency key here means it's safe to
		// re-run a backfill that posts missing JEs by reservation
		// UUID without double-posting for events that did succeed.
		//
		// Idempotency: the reservation UUID is unique per row.
		// Reconfirms-after-reject create new reservations (the
		// previous batch is set to 'cancelled' by
		// cancelMaterialsForWork), so a reconfirm posts a fresh JE
		// rather than being silently deduped.
		h.postInventoryConsumptionJE(h.db, postInventoryConsumptionArgs{
			TenantID:       tenantID,
			OrganizationID: r.OrgID,
			ProductID:      r.ProductID,
			Quantity:       r.Quantity,
			UnitCost:       r.UnitCost,
			SourceType:     "construction_yakuniy_reservation",
			SourceID:       r.ID,
			IdempotencyKey: fmt.Sprintf("CONS-YAK-RES-%s", r.ID.String()),
			Description: fmt.Sprintf("Yakunlangan ish #%d — reservation %s",
				workID, r.ID.String()[:8]),
		})

		approved++
	}

	// ── Finalise-from-estimate sweep ───────────────────────────────
	// Catches material sub-lines that were grafted onto the work
	// AFTER it transitioned to 'submitted' (so reserveMaterialsForWork
	// didn't see them) but BEFORE engineer-confirm — the natural mid-
	// flow add. Without this sweep those resources land in the
	// estimate but never touch inventory or expenses, exactly the
	// "biton" case the user reported.
	//
	// We iterate every material sub-line of this work, compute the
	// expected consumption (override qty if pinned, else parent.done ×
	// norm_rate), and process anything that doesn't already have a
	// matching `Yakunlangan ish #<work_id> — <resource_name>` expense.
	// The expense's existence is the idempotency marker — both the
	// reservation flow above and processYakuniyAdHocResource use the
	// same description shape, so we won't double-process.
	estRows, estErr := h.db.Query(`
		SELECT
		    sub.id,
		    sub.name,
		    COALESCE(sub.uom, ''),
		    COALESCE(sub.norm_rate, 0),
		    COALESCE(sub.unit_rate, 0),
		    COALESCE(sub.quantity, 0),
		    COALESCE(sub.quantity_override, FALSE)
		FROM construction_estimate_line sub
		WHERE sub.tenant_id = $1
		  AND sub.parent_line_id = $2
		  AND LOWER(COALESCE(sub.resource_type, '')) = 'material'
	`, tenantID, workID)
	if estErr == nil {
		defer estRows.Close()
		for estRows.Next() {
			var subID int64
			var subName, subUOM string
			var subNormRate, subUnitRate, subOwnQty float64
			var subOverride bool
			if scanErr := estRows.Scan(&subID, &subName, &subUOM,
				&subNormRate, &subUnitRate, &subOwnQty, &subOverride); scanErr != nil {
				continue
			}
			consumed := subNormRate * workDone
			if subOverride && subOwnQty > 0 {
				consumed = subOwnQty
			}
			if consumed <= 0 {
				continue
			}
			// Already processed? Two markers, either one is sufficient:
			//
			// 1. A per-resource expense with the canonical
			//    `Yakunlangan ish #<workID> — <name>` description.
			//    Written by processYakuniyAdHocResource and by
			//    backfill migration 370.
			//
			// 2. An APPROVED material_reservation for this work whose
			//    product_id matches a product with this resource's
			//    name. The reservation loop above just decremented
			//    inventory for it, but DOESN'T write a per-resource
			//    expense — only a per-WORK summary expense at the
			//    bottom of this function. Without this second check
			//    the sweep below treated a freshly-decremented
			//    reservation row as "unprocessed" and decremented a
			//    second time. User reported as "test 123 confirmed
			//    with 30, inventory -60" (the double-debit).
			expenseDesc := fmt.Sprintf("Yakunlangan ish #%d — %s", workID, subName)
			var alreadyHave int64
			_ = h.db.QueryRow(`
				SELECT COUNT(*) FROM construction_expense_lines
				WHERE tenant_id = $1 AND project_id = $2
				  AND deleted_at IS NULL
				  AND description = $3
			`, tenantID, projectID, expenseDesc).Scan(&alreadyHave)
			if alreadyHave > 0 {
				continue
			}
			// Reservation-side dedup. Match by name → product → any
			// approved reservation for THIS work. Case-insensitive
			// name match mirrors the rest of the engine.
			var reservedHits int64
			_ = h.db.QueryRow(`
				SELECT COUNT(*)
				FROM material_reservations mr
				JOIN products pr ON pr.id = mr.product_id
				WHERE mr.tenant_id = $1
				  AND mr.estimate_line_id = $2
				  AND mr.status = 'approved'
				  AND mr.deleted_at IS NULL
				  AND UPPER(pr.name) = UPPER($3)
			`, tenantID, workID, subName).Scan(&reservedHits)
			if reservedHits > 0 {
				continue
			}

			// Look up the matching product. Skip silently if the user
			// typed a resource name that doesn't have a SKU yet — the
			// estimate row stays, but inventory has nothing to update.
			var subProductID uuid.UUID
			_ = h.db.QueryRow(`
				SELECT id FROM products
				WHERE tenant_id = $1 AND deleted_at IS NULL
				  AND UPPER(name) = UPPER($2)
				ORDER BY created_at DESC LIMIT 1
			`, tenantID, subName).Scan(&subProductID)

			// Pick a warehouse using the same chain reserveMaterialsForWork
			// uses. orgID isn't available here (this function doesn't
			// receive it) — fall back to the project's org for the bias.
			var subProjectWh, subProjectOrg uuid.NullUUID
			_ = h.db.QueryRow(`
				SELECT warehouse_id, organization_id
				FROM construction_projects
				WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
			`, projectID, tenantID).Scan(&subProjectWh, &subProjectOrg)
			var subWarehouse uuid.UUID
			if subProjectWh.Valid {
				subWarehouse = subProjectWh.UUID
			}
			if subWarehouse == uuid.Nil && subProductID != uuid.Nil && subProjectOrg.Valid {
				_ = h.db.QueryRow(`
					SELECT inv.warehouse_id FROM inventory inv
					JOIN warehouses w ON w.id = inv.warehouse_id
					WHERE inv.product_id = $1 AND inv.tenant_id = $2
					  AND w.organization_id = $3
					ORDER BY inv.quantity_on_hand DESC NULLS LAST LIMIT 1
				`, subProductID, tenantID, subProjectOrg.UUID).Scan(&subWarehouse)
			}
			if subWarehouse == uuid.Nil && subProjectOrg.Valid {
				_ = h.db.QueryRow(`
					SELECT id FROM warehouses
					WHERE tenant_id = $1 AND organization_id = $2
					  AND COALESCE(is_active, true) = true
					ORDER BY created_at LIMIT 1
				`, tenantID, subProjectOrg.UUID).Scan(&subWarehouse)
			}
			if subWarehouse == uuid.Nil {
				_ = h.db.QueryRow(`
					SELECT id FROM warehouses
					WHERE tenant_id = $1 AND COALESCE(is_active, true) = true
					ORDER BY created_at LIMIT 1
				`, tenantID).Scan(&subWarehouse)
			}

			// Decrement inventory atomically (UPSERT). Same shape as the
			// reservation loop above so a re-run is safe — the expense
			// existence check at the top of this iteration prevents a
			// second pass from double-decrementing. $4 is cast to
			// numeric so Postgres can resolve the `-` operator.
			if subProductID != uuid.Nil && subWarehouse != uuid.Nil {
				if _, exErr := h.db.Exec(`
					INSERT INTO inventory (
						id, tenant_id, product_id, warehouse_id,
						quantity_on_hand, quantity_reserved,
						created_at, updated_at
					) VALUES (gen_random_uuid(), $1, $2, $3, (0 - $4::numeric), 0, $5, $5)
					ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE
					SET quantity_on_hand = inventory.quantity_on_hand - $4::numeric,
					    updated_at       = $5
				`, tenantID, subProductID, subWarehouse, consumed, now); exErr != nil {
					h.log.Error("finaliseMaterialsForWork: estimate-sweep inventory upsert failed",
						"error", exErr, "sub_id", subID,
						"product_id", subProductID, "warehouse_id", subWarehouse)
					continue
				}
				// Link product to the warehouse's org so the inventory
				// list can render the new row.
				_, _ = h.db.Exec(`
					INSERT INTO product_organization_settings (
						tenant_id, product_id, organization_id,
						cost_price, list_price, min_price,
						min_stock_level, reorder_point, reorder_quantity
					)
					SELECT
						$1, $2, w.organization_id,
						COALESCE((SELECT cost_price FROM products WHERE id = $2), 0),
						COALESCE((SELECT list_price FROM products WHERE id = $2), 0),
						0, 0, 0, 0
					FROM warehouses w
					WHERE w.id = $3 AND w.organization_id IS NOT NULL
					ON CONFLICT (product_id, organization_id) DO NOTHING
				`, tenantID, subProductID, subWarehouse)
			}

			// GL posting for the estimate-sweep path — DR cost-of-
			// materials / CR inventory. Same reason as the reservation
			// loop above: the existing code wrote only to
			// construction_expense_lines, never to journal_entries,
			// leaving the GL drifted from physical. The expense-line
			// existence check at the top of this iteration is the
			// outer idempotency guard, and the helper's own JE-
			// reference check is the inner guard, so re-runs are safe
			// twice over.
			//
			// Idempotency key uses (work, sub) IDs so each estimate-
			// line resource gets a unique JE per work confirmation.
			var subOrgPtr *uuid.UUID
			if workOrgID.Valid {
				if u, err := uuid.Parse(workOrgID.String); err == nil {
					subOrgPtr = &u
				}
			}
			if subProductID != uuid.Nil {
				h.postInventoryConsumptionJE(h.db, postInventoryConsumptionArgs{
					TenantID:       tenantID,
					OrganizationID: subOrgPtr,
					ProductID:      subProductID,
					Quantity:       consumed,
					UnitCost:       subUnitRate,
					SourceType:     "construction_yakuniy_sweep",
					// journal_entries.source_id is UUID; subID is int64
					// (construction_estimate_line.id). The idempotency
					// key already encodes (sub, work) for traceability,
					// so leaving source_id NULL here is the safe choice.
					SourceID:       nil,
					IdempotencyKey: fmt.Sprintf("CONS-YAK-SUB-%d-W%d", subID, workID),
					Description:    expenseDesc,
				})
			}

			// Write the per-resource expense (the idempotency marker).
			subOrgIDArg := interface{}(nil)
			if subOrgPtr != nil {
				subOrgIDArg = *subOrgPtr
			}
			// approved_by → NULL: the column FKs to employees(id), and
			// the user confirming YAKUNIY may not have a matching
			// employee row. Same fix as the work-summary insert below.
			if _, exErr := h.db.Exec(`
				INSERT INTO construction_expense_lines (
					tenant_id, organization_id, project_id, stage_id,
					expense_date, description,
					quantity, uom, unit_price,
					amount, currency_code,
					supplier_name, status, approved_by, approved_at,
					created_by, created_at, updated_at
				) VALUES (
					$1, $2, $3, $11,
					$4::date, $5,
					$6, $7, $8,
					$9, 'UZS',
					$10, 'approved', NULL, $4,
					$12, $4, $4
				)
			`, tenantID, subOrgIDArg, projectID, now, expenseDesc,
				consumed, subUOM, subUnitRate,
				consumed*subUnitRate, companyName, stageIDForExpense, uuidArg(userID)); exErr != nil {
				h.log.Error("finaliseMaterialsForWork: estimate-sweep expense insert failed",
					"error", exErr, "sub_id", subID, "resource", subName)
			}
		}
	}

	// One summary expense line per finalised work, regardless of how
	// many (or how few) reservations existed. Computed straight from
	// the work line's done × effective rate so it always agrees with
	// what the user sees as FAKT JAMI on the Bosqichlar table.
	//
	// We deliberately don't break this into per-reservation rows any
	// more. Doing it per-reservation meant works with no material
	// sub-lines (labour-only / machine-only) wrote nothing, even when
	// they had real cost — that's why the user reported "FAKT still 0
	// after YAKUNIY". The reservations themselves are still updated
	// above for warehouse tracking; this row is purely the cost-side
	// projection.
	//
	// Idempotency: if the work was already finalised once and the
	// engineer re-runs the transition (e.g. after a reject + reconfirm
	// round-trip), we'd risk double-writing. We use estimate_line.id
	// as part of a description-based dedupe key and check before
	// inserting.
	workCost := workDone * effectiveRate
	if workCost > 0 {
		// Check for an existing approved expense line for this work to
		// prevent double-write on re-confirmation. We tag the row's
		// description with the work id ("Yakunlangan ish #N — …") so
		// LIKE-matching on that prefix is enough to detect it.
		var existing int64
		_ = h.db.QueryRow(`
			SELECT COUNT(*)
			FROM construction_expense_lines
			WHERE tenant_id = $1
			  AND project_id = $2
			  AND status = 'approved'
			  AND deleted_at IS NULL
			  AND description LIKE $3
		`, tenantID, projectID, fmt.Sprintf("Yakunlangan ish #%d —%%", workID)).Scan(&existing)
		if existing == 0 {
			desc := fmt.Sprintf("Yakunlangan ish #%d — %s", workID, workName)
			var orgIDArg interface{}
			if len(ress) > 0 && ress[0].OrgID != nil {
				orgIDArg = ress[0].OrgID
			} else if workOrgID.Valid {
				if u, err := uuid.Parse(workOrgID.String); err == nil {
					orgIDArg = u
				}
			}
			// approved_by must reference employees(id), not users(id) — the
			// user confirming YAKUNIY may not have a matching employee
			// row, which is what produced the
			//   "violates foreign key constraint
			//    construction_expense_lines_approved_by_fkey"
			// error in the logs. Send NULL; the description and
			// approved_at timestamp are sufficient audit trail.
			if _, exErr := h.db.Exec(`
				INSERT INTO construction_expense_lines (
					tenant_id, organization_id, project_id, stage_id,
					expense_date, description,
					quantity, uom, unit_price,
					amount, currency_code,
					supplier_name, status, approved_by, approved_at,
					created_by, created_at, updated_at
				) VALUES (
					$1, $2, $3, $11,
					$4::date, $5,
					$6, $7, $8,
					$9, 'UZS',
					$10, 'approved', NULL, $4,
					$12, $4, $4
				)
			`, tenantID, orgIDArg, projectID, now, desc,
				workDone, workUOM, effectiveRate,
				workCost, companyName, stageIDForExpense, uuidArg(userID)); exErr != nil {
				h.log.Error("finaliseMaterialsForWork: failed to insert work expense line",
					"error", exErr, "work_id", workID)
			}
		}
	}

	h.log.Info("finaliseMaterialsForWork: complete",
		"work_id", workID, "reservations_approved", approved, "work_cost", workCost)
}

// cancelMaterialsForWork cancels every pending reservation tied to the
// work and releases the reserved qty so the warehouse balance returns to
// its pre-submit value. Used when a reviewer rejects the work back to
// in_progress, OR as the first step of a re-submit (so we don't
// double-reserve when the foreman edits done_quantity and re-presses
// "Tekshiruvga yuborish").
func (h *Handler) cancelMaterialsForWork(tenantID uuid.UUID, workID int64) {
	rows, err := h.db.Query(`
		SELECT id, product_id, warehouse_id, quantity
		FROM material_reservations
		WHERE tenant_id = $1
		  AND estimate_line_id = $2
		  AND status = 'pending'
		  AND deleted_at IS NULL
	`, tenantID, workID)
	if err != nil {
		h.log.Error("cancelMaterialsForWork: failed to load reservations",
			"error", err, "work_id", workID)
		return
	}
	defer rows.Close()

	type resRow struct {
		ID          uuid.UUID
		ProductID   uuid.UUID
		WarehouseID *uuid.UUID
		Quantity    float64
	}
	var ress []resRow
	for rows.Next() {
		var r resRow
		var wh uuid.NullUUID
		if scanErr := rows.Scan(&r.ID, &r.ProductID, &wh, &r.Quantity); scanErr == nil {
			if wh.Valid {
				w := wh.UUID
				r.WarehouseID = &w
			}
			ress = append(ress, r)
		}
	}
	if len(ress) == 0 {
		return
	}

	now := time.Now()
	cancelled := 0
	for _, r := range ress {
		if _, upErr := h.db.Exec(`
			UPDATE material_reservations
			SET status = 'cancelled', updated_at = $1
			WHERE id = $2
		`, now, r.ID); upErr != nil {
			h.log.Error("cancelMaterialsForWork: failed to mark cancelled",
				"error", upErr, "reservation_id", r.ID)
			continue
		}

		if r.WarehouseID != nil {
			_, _ = h.db.Exec(`
				UPDATE inventory
				SET quantity_reserved = GREATEST(COALESCE(quantity_reserved, 0) - $1, 0),
				    updated_at = $2
				WHERE product_id = $3 AND warehouse_id = $4 AND tenant_id = $5
			`, r.Quantity, now, r.ProductID, r.WarehouseID, tenantID)
		}
		cancelled++
	}

	h.log.Info("cancelMaterialsForWork: complete",
		"work_id", workID, "cancelled", cancelled)
}

// processYakuniyAdHocResource handles a single resource line that gets
// grafted onto a work that's already been engineer-confirmed. The normal
// reserveMaterialsForWork → finaliseMaterialsForWork pipeline can't help
// here because both events have already passed for the parent work.
//
// We do the minimum needed for the page to be self-consistent:
//   • inventory of the matching product is decremented by `consumed`
//     (allowed to go negative — same policy as finaliseMaterialsForWork)
//   • a single approved expense_line is written for `consumed × unit_rate`
//     so the Moliya → Xarajatlar feed and Byudjet Fakt totals reflect
//     the new resource's cost
//
// We do NOT create a `pending` reservation row — there's nothing to
// "reserve" anymore once the work is locked, and skipping reservations
// keeps the post-confirmation audit trail tied to the resource line
// directly via the expense's description.
//
// All steps are best-effort: errors are logged but the parent CreateLine
// HTTP response still succeeds. Worst case the user sees the line but
// has to re-trigger inventory manually — preferable to bouncing the
// whole add-resource UI just because one warehouse op failed.
func (h *Handler) processYakuniyAdHocResource(
	c *gin.Context,
	tenantID uuid.UUID,
	estimateID, parentLineID, lineID int64,
	resourceName, uom string,
	unitPrice, consumed float64,
	parentSectionPath string,
) {
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	// Resolve the project + building of the parent work via its estimate.
	var projectID int64
	var buildingID sql.NullInt64
	if err := h.db.QueryRow(`
		SELECT project_id, building_id
		FROM construction_estimate
		WHERE id = $1 AND tenant_id = $2
	`, estimateID, tenantID).Scan(&projectID, &buildingID); err != nil {
		h.log.Error("processYakuniyAdHocResource: failed to load estimate", "error", err, "estimate_id", estimateID)
		return
	}

	// Pull the project's default warehouse + owning organization. Both
	// drive the warehouse picker below so the inventory row lands
	// somewhere the project's company can actually see (the products
	// tab filters out inventory in warehouses owned by a DIFFERENT org
	// than the active company).
	var projectWarehouseID uuid.NullUUID
	var projectOrgID uuid.NullUUID
	_ = h.db.QueryRow(`
		SELECT warehouse_id, organization_id
		FROM construction_projects
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&projectWarehouseID, &projectOrgID)

	// Resolve the matching product by case-insensitive name match. If not
	// found we skip inventory decrement but still write the expense — the
	// resource may be a service item (no SKU) and the user expects the
	// cost to land on the project regardless.
	var productID uuid.UUID
	_ = h.db.QueryRow(`
		SELECT id FROM products
		WHERE tenant_id = $1
		  AND deleted_at IS NULL
		  AND UPPER(name) = UPPER($2)
		ORDER BY created_at DESC
		LIMIT 1
	`, tenantID, resourceName).Scan(&productID)

	// Pick a warehouse — same chain as reserveMaterialsForWork, biased
	// toward the CALLING USER's active org so the deduction is visible
	// in their products tab. Inventory.jsx filters out rows in
	// warehouses owned by a different org than the active company; an
	// org-mismatched pick here is exactly what makes "Yangi mahsulot"
	// products show 0 even after a YAKUNIY-confirmed consumption.
	//   0. Project's chosen default
	//   1. Calling user's active org — highest-stock for this product
	//   2. Calling user's active org — oldest active warehouse
	//   3. Project's org — highest-stock for this product
	//   4. Project's org — oldest active warehouse
	//   5. Tenant-wide oldest active (only when neither org has one)
	var warehouseID uuid.UUID
	if projectWarehouseID.Valid {
		warehouseID = projectWarehouseID.UUID
	}
	if warehouseID == uuid.Nil && productID != uuid.Nil && orgID != uuid.Nil {
		_ = h.db.QueryRow(`
			SELECT inv.warehouse_id
			FROM inventory inv
			JOIN warehouses w ON w.id = inv.warehouse_id
			WHERE inv.product_id = $1 AND inv.tenant_id = $2
			  AND w.organization_id = $3
			ORDER BY inv.quantity_on_hand DESC NULLS LAST
			LIMIT 1
		`, productID, tenantID, orgID).Scan(&warehouseID)
	}
	if warehouseID == uuid.Nil && orgID != uuid.Nil {
		_ = h.db.QueryRow(`
			SELECT id FROM warehouses
			WHERE tenant_id = $1
			  AND organization_id = $2
			  AND COALESCE(is_active, true) = true
			ORDER BY created_at LIMIT 1
		`, tenantID, orgID).Scan(&warehouseID)
	}
	if warehouseID == uuid.Nil && productID != uuid.Nil && projectOrgID.Valid {
		_ = h.db.QueryRow(`
			SELECT inv.warehouse_id
			FROM inventory inv
			JOIN warehouses w ON w.id = inv.warehouse_id
			WHERE inv.product_id = $1 AND inv.tenant_id = $2
			  AND w.organization_id = $3
			ORDER BY inv.quantity_on_hand DESC NULLS LAST
			LIMIT 1
		`, productID, tenantID, projectOrgID.UUID).Scan(&warehouseID)
	}
	if warehouseID == uuid.Nil && projectOrgID.Valid {
		_ = h.db.QueryRow(`
			SELECT id FROM warehouses
			WHERE tenant_id = $1
			  AND organization_id = $2
			  AND COALESCE(is_active, true) = true
			ORDER BY created_at LIMIT 1
		`, tenantID, projectOrgID.UUID).Scan(&warehouseID)
	}
	if warehouseID == uuid.Nil && productID != uuid.Nil {
		_ = h.db.QueryRow(`
			SELECT warehouse_id FROM inventory
			WHERE product_id = $1 AND tenant_id = $2
			ORDER BY quantity_on_hand DESC NULLS LAST
			LIMIT 1
		`, productID, tenantID).Scan(&warehouseID)
	}
	if warehouseID == uuid.Nil {
		_ = h.db.QueryRow(`
			SELECT id FROM warehouses
			WHERE tenant_id = $1 AND COALESCE(is_active, true) = true
			ORDER BY created_at LIMIT 1
		`, tenantID).Scan(&warehouseID)
	}

	// Make the product visible in the picked warehouse's org. The
	// products tab INNER JOINs product_organization_settings, so a
	// row created via "Yangi mahsulot" stays invisible to the org
	// owning the picked warehouse unless we add the link. Idempotent
	// via ON CONFLICT (product_id, organization_id).
	if productID != uuid.Nil && warehouseID != uuid.Nil {
		_, _ = h.db.Exec(`
			INSERT INTO product_organization_settings (
				tenant_id, product_id, organization_id,
				cost_price, list_price, min_price,
				min_stock_level, reorder_point, reorder_quantity
			)
			SELECT
				$1, $2, w.organization_id,
				COALESCE((SELECT cost_price FROM products WHERE id = $2), 0),
				COALESCE((SELECT list_price FROM products WHERE id = $2), 0),
				0, 0, 0, 0
			FROM warehouses w
			WHERE w.id = $3 AND w.organization_id IS NOT NULL
			ON CONFLICT (product_id, organization_id) DO NOTHING
		`, tenantID, productID, warehouseID)
	}

	now := time.Now()

	// Inventory decrement, atomic via UPSERT against the unique index
	// (tenant_id, product_id, warehouse_id) from migration 282.
	//
	// Previously this was a two-step UPDATE-then-INSERT-on-zero-rows. The
	// gap between the two statements (each was its own auto-committed
	// Exec) meant a silent INSERT failure left no row at all — exactly
	// the "biton" case the user reported: product created, resource
	// added to YAKUNIY work, no inventory row ever materialised. Replacing
	// it with a single ON CONFLICT statement removes the gap; either
	// the row gets inserted at -consumed, or the existing row gets its
	// balance decremented, with no in-between failure mode.
	if productID != uuid.Nil && warehouseID != uuid.Nil {
		// $4 cast to numeric so Postgres can resolve the unary `-` and
		// the subtract; without the cast the parameter type is 'unknown'
		// and Postgres errors with "operator is not unique: - unknown".
		if _, exErr := h.db.Exec(`
			INSERT INTO inventory (
				id, tenant_id, product_id, warehouse_id,
				quantity_on_hand, quantity_reserved,
				created_at, updated_at
			) VALUES (gen_random_uuid(), $1, $2, $3, (0 - $4::numeric), 0, $5, $5)
			ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE
			SET quantity_on_hand = inventory.quantity_on_hand - $4::numeric,
			    updated_at       = $5
		`, tenantID, productID, warehouseID, consumed, now); exErr != nil {
			h.log.Error("processYakuniyAdHocResource: inventory upsert failed",
				"error", exErr, "product_id", productID,
				"warehouse_id", warehouseID, "consumed", consumed)
		}
	} else {
		h.log.Warn("processYakuniyAdHocResource: skipping inventory step — missing id",
			"product_id", productID, "warehouse_id", warehouseID,
			"resource_name", resourceName)
	}

	// Resolve the section's stage_id (same lookup the work-level handler
	// uses) so the expense lands in the right Byudjet bucket.
	var stageIDForExpense sql.NullInt64
	if parentSectionPath != "" {
		var stageID int64
		if buildingID.Valid {
			if err := h.db.QueryRow(`
				SELECT id FROM construction_stages
				WHERE tenant_id = $1 AND project_id = $2 AND name = $3 AND building_id = $4
				ORDER BY id ASC LIMIT 1
			`, tenantID, projectID, parentSectionPath, buildingID.Int64).Scan(&stageID); err == nil {
				stageIDForExpense = sql.NullInt64{Int64: stageID, Valid: true}
			}
		}
		if !stageIDForExpense.Valid {
			if err := h.db.QueryRow(`
				SELECT id FROM construction_stages
				WHERE tenant_id = $1 AND project_id = $2 AND name = $3
				ORDER BY id ASC LIMIT 1
			`, tenantID, projectID, parentSectionPath).Scan(&stageID); err == nil {
				stageIDForExpense = sql.NullInt64{Int64: stageID, Valid: true}
			}
		}
	}

	// Resolve the project's organization for the expense row.
	var orgIDArg interface{}
	{
		var orgIDFromProject sql.NullString
		_ = h.db.QueryRow(`
			SELECT organization_id::text FROM construction_projects
			WHERE id = $1 AND tenant_id = $2
		`, projectID, tenantID).Scan(&orgIDFromProject)
		if orgIDFromProject.Valid {
			if u, err := uuid.Parse(orgIDFromProject.String); err == nil {
				orgIDArg = u
			}
		}
		if orgIDArg == nil && orgID != uuid.Nil {
			orgIDArg = orgID
		}
	}

	// Resolve a supplier display name (project's company).
	var companyName string
	if u, ok := orgIDArg.(uuid.UUID); ok && u != uuid.Nil {
		_ = h.db.QueryRow(`SELECT COALESCE(name, '') FROM organizations WHERE id = $1`, u).Scan(&companyName)
	}

	// Approved expense line. Description ties it to the parent work id
	// + the resource name so the Moliya → Xarajatlar list reads
	// naturally and `Yakunlangan ish #N — <resource>` keeps the same
	// shape as legacy per-reservation rows.
	//
	// We write the expense unconditionally — even at cost=0 — for two
	// reasons:
	//   1. Idempotency. Backfill migrations (366 / 370) detect "already
	//      processed" by looking for a matching description. Skipping
	//      the write at cost=0 leaves no marker, so a backfill re-run
	//      decrements inventory a second time. A 0-сум expense is the
	//      cheap way to keep the bookkeeping consistent.
	//   2. Audit trail. The user added the resource on purpose; even at
	//      0 СУМ it's visible in the Xarajatlar feed as confirmation
	//      that the consumption was recorded.
	// The user can filter out 0-сум rows in the Xarajatlar UI if they
	// find them noisy.
	cost := consumed * unitPrice
	desc := fmt.Sprintf("Yakunlangan ish #%d — %s", parentLineID, resourceName)
	if _, exErr := h.db.Exec(`
		INSERT INTO construction_expense_lines (
			tenant_id, organization_id, project_id, stage_id,
			expense_date, description,
			quantity, uom, unit_price,
			amount, currency_code,
			supplier_name, status, approved_by, approved_at,
			created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $11,
			$4::date, $5,
			$6, $7, $8,
			$9, 'UZS',
			$10, 'approved', NULL, $4,
			$12, $4, $4
		)
	`, tenantID, orgIDArg, projectID, now, desc,
		consumed, uom, unitPrice,
		cost, companyName, stageIDForExpense, uuidArg(userID)); exErr != nil {
		h.log.Error("processYakuniyAdHocResource: expense insert failed",
			"error", exErr, "parent_line_id", parentLineID, "resource", resourceName)
	}

	h.log.Info("processYakuniyAdHocResource: complete",
		"parent_line_id", parentLineID, "resource_line_id", lineID,
		"resource", resourceName, "consumed", consumed, "cost", cost)
}
