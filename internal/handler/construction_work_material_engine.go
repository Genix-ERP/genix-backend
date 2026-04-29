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
	_ = h.db.QueryRow(`
		SELECT warehouse_id
		FROM construction_projects
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, projectID, tenantID).Scan(&projectWarehouseID)

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

		// Pick a warehouse. Three-step chain:
		//   0. Project's chosen default (construction_projects.warehouse_id,
		//      migration 365). When set, every sub-line of every work in
		//      this project lands on the same warehouse — predictable for
		//      the project manager, no surprise debits to other sites.
		//   1. Whichever warehouse already has the most stock for this
		//      product (legacy behaviour, kept as fallback for projects
		//      that haven't chosen a default).
		//   2. Oldest active tenant warehouse — last-ditch so we always
		//      have a row to write to (stock is allowed to go negative
		//      per product feedback).
		var warehouseID uuid.UUID
		if projectWarehouseID.Valid {
			warehouseID = projectWarehouseID.UUID
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
		// Mark reservation approved.
		if _, upErr := h.db.Exec(`
			UPDATE material_reservations
			SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $2
			WHERE id = $3
		`, uuidArg(userID), now, r.ID); upErr != nil {
			h.log.Error("finaliseMaterialsForWork: failed to mark approved",
				"error", upErr, "reservation_id", r.ID)
			continue
		}

		// Decrement on_hand AND release reserved. UPSERT path: try
		// UPDATE first, and if no row exists (RowsAffected = 0) INSERT
		// a new inventory row at the negative balance so the deduction
		// is visible. Without this fallback, products that never had
		// stock would keep showing Qoldiq=0 even after work was finalised.
		if r.WarehouseID != nil {
			result, exErr := h.db.Exec(`
				UPDATE inventory
				SET quantity_on_hand  = COALESCE(quantity_on_hand, 0)  - $1,
				    quantity_reserved = GREATEST(COALESCE(quantity_reserved, 0) - $1, 0),
				    updated_at = $2
				WHERE product_id = $3 AND warehouse_id = $4 AND tenant_id = $5
			`, r.Quantity, now, r.ProductID, r.WarehouseID, tenantID)
			if exErr != nil {
				h.log.Error("finaliseMaterialsForWork: failed to update inventory",
					"error", exErr, "reservation_id", r.ID)
			} else if affected, _ := result.RowsAffected(); affected == 0 {
				// No matching inventory row — create one at the negative
				// balance. Allowed per product policy.
				if _, insErr := h.db.Exec(`
					INSERT INTO inventory (
						id, tenant_id, product_id, warehouse_id,
						quantity_on_hand, quantity_reserved,
						created_at, updated_at
					) VALUES (gen_random_uuid(), $1, $2, $3, -$4, 0, $5, $5)
				`, tenantID, r.ProductID, r.WarehouseID, r.Quantity, now); insErr != nil {
					h.log.Error("finaliseMaterialsForWork: failed to insert inventory row",
						"error", insErr, "reservation_id", r.ID)
				}
			}
		}

		approved++
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
					$10, 'approved', $12, $4,
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
