package handler

// PO → Qurilish obyekt tannarxi (migration 450).
//
// A purchase order linked to a construction project (purchase_orders.
// construction_project_id) contributes ACTUAL cost to the object when goods
// arrive: each received line becomes a construction_expense_lines row
// (the central per-object cost table, DR 0810 construction WIP by default,
// status 'approved' — receipt of ordered goods needs no second approval).
//
// Called from BOTH receipt paths — POST /purchase-orders/:id/receive and
// goods-receipt complete. The paths are mutually exclusive per PO thanks to
// poStockReceivedVia, so cost rows never double.

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type poCostLine struct {
	ProductID uuid.UUID
	Qty       float64
	UnitPrice float64
}

func (h *Handler) addPOReceiptToObjectCost(tenantID, poID uuid.UUID, lines []poCostLine, createdBy uuid.UUID) {
	if len(lines) == 0 {
		return
	}

	var projectID sql.NullInt64
	var orgID *uuid.UUID
	var vendorID *uuid.UUID
	var orderNumber string
	err := h.db.QueryRow(`
		SELECT construction_project_id, organization_id, vendor_id, order_number
		FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, poID, tenantID).Scan(&projectID, &orgID, &vendorID, &orderNumber)
	if err != nil || !projectID.Valid {
		return // not linked to an object — nothing to do
	}

	debitAcct := findAccount(h.db, tenantID, orgID, "tugallanmagan kapital", "0810")
	now := time.Now()

	for _, l := range lines {
		if l.Qty <= 0 {
			continue
		}
		var pname, uom string
		h.db.QueryRow(`
			SELECT p.name, COALESCE(u.name, '')
			FROM products p LEFT JOIN units_of_measure u ON u.id = p.unit_id
			WHERE p.id = $1
		`, l.ProductID).Scan(&pname, &uom)

		if _, insErr := h.db.Exec(`
			INSERT INTO construction_expense_lines (
				id, tenant_id, organization_id, project_id, expense_date, description,
				product_id, quantity, uom, unit_price, amount, vendor_id,
				debit_account_id, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12,
				$13, 'approved', $14, $15, $15)
		`, uuid.New(), tenantID, orgID, projectID.Int64, now,
			fmt.Sprintf("Xarid qabul %s — %s", orderNumber, pname),
			l.ProductID, l.Qty, uom, l.UnitPrice, l.Qty*l.UnitPrice, vendorID,
			nilIfZeroUUID(debitAcct), nilIfZeroUUID(createdBy), now,
		); insErr != nil {
			h.log.Error("Failed to add PO receipt to object cost", "error", insErr,
				"po_id", poID, "product_id", l.ProductID)
		}
	}
}
