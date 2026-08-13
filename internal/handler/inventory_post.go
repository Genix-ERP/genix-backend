package handler

// Central stock posting helper — docs/ombor-audit.md §9 item 2.
//
// Every stock movement is two writes that must land together: the balance
// row (inventory.quantity_on_hand) and the ledger row
// (inventory_transactions). Historically ~25 call sites each hand-rolled
// both writes, many outside a transaction and with per-writer sign
// conventions — which is how the module accumulated the drift the audit
// documents. New code and repaired paths route through applyStockDelta
// inside the caller's transaction.
//
// Conventions enforced here:
//   - quantity is a SIGNED delta: positive = stock in, negative = stock out.
//     The ledger row stores the same signed quantity, so the stock_ledger
//     view (migration 447) can rebuild balances by plain SUM.
//   - the ledger row is self-contained: product_id + warehouse_id are
//     denormalized alongside inventory_id (migration 447 columns).
//   - AVECO (o'rtacha tannarx): positive deltas fold their unit cost into
//     the balance row's unit_cost as a quantity-weighted average; negative
//     deltas consume at the current average and never change it.
//   - negative stock is refused with errInsufficientStock unless the caller
//     explicitly allows it (production shortfall booking does).

import (
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
)

// errInsufficientStock is returned when a negative delta would push the
// balance below zero and the caller did not opt into negative stock.
var errInsufficientStock = errors.New("insufficient stock")

type stockDeltaArgs struct {
	TenantID    uuid.UUID
	OrgID       *uuid.UUID
	ProductID   uuid.UUID
	WarehouseID uuid.UUID
	Qty         float64 // signed delta: + in, − out
	UnitCost    float64 // cost per unit for positive deltas; 0 = use stored avg
	TxType      string  // must satisfy chk_invtxn_type (migration 447)
	RefType     string
	RefID       string
	Reason      string
	Notes       string
	FromWH      *uuid.UUID
	ToWH        *uuid.UUID
	CreatedBy   uuid.UUID
	When        time.Time
	AllowNeg    bool

	// --- Zaxiralarni baholash (valuation_hook.go) ---
	// Op overrides the transaction_type → operation mapping when the ledger
	// type is ambiguous. Leave empty to let valuationOpFor decide.
	Op StockOperation
	// Returns are priced from the original issue, not at today's cost (§3.1).
	OriginalDocType string
	OriginalDocID   uuid.UUID
	// SkipValuation opts a movement out of layer tracking entirely. Use only
	// for movements that are not a change in owned stock.
	SkipValuation bool
}

// applyStockDelta upserts the balance row and appends the matching ledger
// row using the SQL context q (pass the caller's *sql.Tx — both writes
// must share one transaction). Returns the post-delta balance and the
// unit cost the movement was valued at.
func (h *Handler) applyStockDelta(q dbExecQuerier, a stockDeltaArgs) (newBalance, valuedCost float64, err error) {
	if a.When.IsZero() {
		a.When = time.Now()
	}

	var invID uuid.UUID
	var avgCost float64
	err = q.QueryRow(`
		INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id,
			quantity_on_hand, quantity_reserved, unit_cost, last_movement_date, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 0, $7, $8, $8, $8)
		ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE SET
			unit_cost = CASE
				WHEN EXCLUDED.quantity_on_hand > 0
				     AND (GREATEST(inventory.quantity_on_hand, 0) + EXCLUDED.quantity_on_hand) > 0
				     AND EXCLUDED.unit_cost > 0
				THEN ((GREATEST(inventory.quantity_on_hand, 0) * inventory.unit_cost)
				      + (EXCLUDED.quantity_on_hand * EXCLUDED.unit_cost))
				     / (GREATEST(inventory.quantity_on_hand, 0) + EXCLUDED.quantity_on_hand)
				ELSE inventory.unit_cost
			END,
			quantity_on_hand   = inventory.quantity_on_hand + EXCLUDED.quantity_on_hand,
			organization_id    = COALESCE(inventory.organization_id, EXCLUDED.organization_id),
			last_movement_date = EXCLUDED.last_movement_date,
			updated_at         = EXCLUDED.updated_at
		RETURNING id, quantity_on_hand, unit_cost
	`, uuid.New(), a.TenantID, a.OrgID, a.ProductID, a.WarehouseID,
		a.Qty, a.UnitCost, a.When).Scan(&invID, &newBalance, &avgCost)
	if err != nil {
		return 0, 0, err
	}

	if !a.AllowNeg && a.Qty < 0 && newBalance < -0.0001 {
		return newBalance, avgCost, errInsufficientStock
	}

	valuedCost = a.UnitCost
	if valuedCost == 0 {
		valuedCost = avgCost
	}

	_, err = q.Exec(`
		INSERT INTO inventory_transactions (
			id, tenant_id, organization_id, inventory_id, product_id, warehouse_id,
			transaction_type, quantity, unit_cost, total_cost,
			from_warehouse_id, to_warehouse_id,
			reference_type, reference_id, reason, notes,
			transaction_date, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
		          NULLIF($13, ''), NULLIF($14, '')::uuid, NULLIF($15, ''), NULLIF($16, ''), $17, $18, $17)
	`, uuid.New(), a.TenantID, a.OrgID, invID, a.ProductID, a.WarehouseID,
		a.TxType, a.Qty, valuedCost, math.Abs(a.Qty)*valuedCost,
		a.FromWH, a.ToWH, a.RefType, a.RefID, a.Reason, a.Notes, a.When, nilIfZeroUUID(a.CreatedBy))
	if err != nil {
		return newBalance, valuedCost, err
	}

	// Baholash qatlamlari. Ombor qoldig'i va daftar qatori yozilgandan keyin,
	// o'sha tranzaksiya ichida — ya'ni hujjat orqaga qaytsa, qatlam ham
	// qaytadi. Bu bosqichda provodka yozilmaydi (valuation_hook.go izohi).
	h.recordValuationForDelta(q, a, valuedCost)

	return newBalance, valuedCost, nil
}

func nilIfZeroUUID(id uuid.UUID) interface{} {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// poStockReceivedVia reports whether 'receipt' ledger rows for this PO were
// already written by one of the OTHER receipt paths. Three independent paths
// can put purchase stock in (PO approve auto-receive, POST /receive, goods
// receipt complete — audit finding #4); each caller passes the types the
// other paths use, so cross-path double-counting is blocked while legitimate
// within-path partial receipts keep working.
func (h *Handler) poStockReceivedVia(tenantID, poID uuid.UUID, via ...string) bool {
	for _, v := range via {
		var exists bool
		switch v {
		case "purchase_order":
			h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_transactions
				WHERE tenant_id=$1 AND transaction_type='receipt'
				  AND reference_type='purchase_order' AND reference_id=$2)`,
				tenantID, poID).Scan(&exists)
		case "goods_receipt":
			h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_transactions t
				WHERE t.tenant_id=$1 AND t.transaction_type='receipt'
				  AND t.reference_type='goods_receipt'
				  AND t.reference_id IN (SELECT id FROM goods_receipts WHERE purchase_order_id=$2 AND tenant_id=$1))`,
				tenantID, poID).Scan(&exists)
		case "stock_operation":
			h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_transactions t
				WHERE t.tenant_id=$1 AND t.transaction_type='receipt'
				  AND t.reference_type='stock_operation'
				  AND t.reference_id IN (SELECT id FROM stock_operations WHERE source_type='purchase_order' AND source_id=$2 AND tenant_id=$1))`,
				tenantID, poID).Scan(&exists)
		}
		if exists {
			return true
		}
	}
	return false
}

// emitInventoryAdjusted fires the inventory.adjusted workflow event for a
// posted movement (fire-and-forget; call after the transaction commits).
func (h *Handler) emitInventoryAdjusted(tenantID, productID uuid.UUID, qty, newBalance float64) {
	var name, code string
	_ = h.db.QueryRow(`SELECT name, COALESCE(code, '') FROM products WHERE id = $1`, productID).Scan(&name, &code)
	h.EmitWorkflowEvent(tenantID, "inventory.adjusted", map[string]interface{}{
		"record_id":    productID.String(),
		"product_name": name,
		"product_code": code,
		"quantity":     qty,
		"new_balance":  newBalance,
	})
}
