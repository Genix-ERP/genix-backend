package handler

// sales_guards.go — Savdo v2 phase 2 (savdo-audit.md §8 D):
//   * tenant-setting helpers for the nested tenant_settings JSON,
//   * credit-limit check ("this one feature prevents more losses than any
//     report" — enforced at order confirm),
//   * server-side order-discount cap (the client-trusted discount_amount was
//     the module's sharpest input-trust hole).

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// tenantSettingsMap loads the tenant's nested settings JSON ({} on any error).
func (h *Handler) tenantSettingsMap(tenantID uuid.UUID) map[string]interface{} {
	var raw []byte
	if err := h.db.QueryRow(
		"SELECT settings FROM tenant_settings WHERE tenant_id = $1", tenantID,
	).Scan(&raw); err != nil {
		return map[string]interface{}{}
	}
	var settings map[string]interface{}
	if json.Unmarshal(raw, &settings) != nil {
		return map[string]interface{}{}
	}
	return settings
}

// settingAt walks a nested settings map ("sales" → "credit" → "policy").
// Also tolerates a flat dotted key at any level ("sales.credit.policy"),
// since the frontend historically saved both shapes.
func settingAt(m map[string]interface{}, path ...string) interface{} {
	// Flat dotted key first
	flat := ""
	for i, p := range path {
		if i > 0 {
			flat += "."
		}
		flat += p
	}
	if v, ok := m[flat]; ok {
		return v
	}
	cur := m
	for i, p := range path {
		v, ok := cur[p]
		if !ok {
			return nil
		}
		if i == len(path)-1 {
			return v
		}
		next, ok := v.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = next
	}
	return nil
}

func settingBool(m map[string]interface{}, def bool, path ...string) bool {
	if v := settingAt(m, path...); v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func settingFloat(m map[string]interface{}, def float64, path ...string) float64 {
	if v := settingAt(m, path...); v != nil {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case string:
			var f float64
			if _, err := jsonNumberScan(n, &f); err == nil {
				return f
			}
		}
	}
	return def
}

func jsonNumberScan(s string, out *float64) (int, error) {
	var f float64
	err := json.Unmarshal([]byte(s), &f)
	if err == nil {
		*out = f
		return 1, nil
	}
	return 0, err
}

func settingString(m map[string]interface{}, def string, path ...string) string {
	if v := settingAt(m, path...); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}

// creditCheckResult is what salesCreditCheck reports back to the caller.
type creditCheckResult struct {
	Enabled     bool
	Exceeded    bool
	Policy      string // "block" | "warn"
	Limit       float64
	Outstanding float64 // open AR (sent/partial/overdue amount_due)
}

// salesCreditCheck answers: with this additional order amount, does the
// customer's unpaid balance exceed their credit limit? Limit resolution:
// contacts.credit_limit, else the tenant default; 0/absent limit = unlimited.
func (h *Handler) salesCreditCheck(tenantID uuid.UUID, orgID *uuid.UUID, customerID uuid.UUID, addAmount float64) creditCheckResult {
	res := creditCheckResult{Policy: "block"}
	settings := h.tenantSettingsMap(tenantID)
	if !settingBool(settings, false, "sales", "credit", "enable_credit_limit") {
		return res
	}
	res.Enabled = true
	res.Policy = settingString(settings, "block", "sales", "credit", "policy")

	var limit float64
	_ = h.db.QueryRow(
		"SELECT COALESCE(credit_limit, 0) FROM contacts WHERE id = $1 AND tenant_id = $2",
		customerID, tenantID,
	).Scan(&limit)
	if limit <= 0 {
		limit = settingFloat(settings, 0, "sales", "credit", "default_credit_limit")
	}
	if limit <= 0 {
		return res // no limit configured for this customer
	}
	res.Limit = limit

	var orgArg interface{}
	if orgID != nil {
		orgArg = *orgID
	}
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(amount_due), 0)
		FROM sales_invoices
		WHERE tenant_id = $1 AND customer_id = $2 AND deleted_at IS NULL
		  AND status NOT IN ('draft', 'cancelled', 'void') AND amount_due > 0
		  AND ($3::uuid IS NULL OR organization_id = $3)`,
		tenantID, customerID, orgArg,
	).Scan(&res.Outstanding)

	res.Exceeded = res.Outstanding+addAmount > limit+0.01
	return res
}

// salesMaxDiscountPct returns the tenant's order-discount cap in percent, or a
// negative number when no cap is configured. Reads the frontend's
// max_discount_percent key first, then the backend default map's max_discount
// (the two drifted historically — savdo-audit §5).
func (h *Handler) salesMaxDiscountPct(tenantID uuid.UUID) float64 {
	settings := h.tenantSettingsMap(tenantID)
	if v := settingAt(settings, "sales", "pricing", "max_discount_percent"); v != nil {
		return settingFloat(settings, -1, "sales", "pricing", "max_discount_percent")
	}
	if v := settingAt(settings, "sales", "pricing", "max_discount"); v != nil {
		return settingFloat(settings, -1, "sales", "pricing", "max_discount")
	}
	return -1
}

// reserveSalesOrderStock books each open line's remaining quantity into
// inventory.quantity_reserved (line warehouse, else the order's). Reservation is
// advisory: it feeds quantity_available for ordering/UI; the hard guard on
// shipping stays applyStockDelta's on-hand >= 0. Called on order confirm.
func (h *Handler) reserveSalesOrderStock(tenantID uuid.UUID, orgID *uuid.UUID, orderID uuid.UUID, orderWarehouse *uuid.UUID) {
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("reserveSalesOrderStock: begin failed", "error", err)
		return
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT sol.product_id, sol.quantity - COALESCE(sol.quantity_delivered, 0), sol.warehouse_id
		FROM sales_order_lines sol
		JOIN products p ON p.id = sol.product_id
		WHERE sol.sales_order_id = $1
		  AND sol.quantity > COALESCE(sol.quantity_delivered, 0)
		  AND COALESCE(p.track_inventory, true)`,
		orderID)
	if err != nil {
		h.log.Error("reserveSalesOrderStock: line query failed", "error", err)
		return
	}
	type resLine struct {
		ProductID uuid.UUID
		Qty       float64
		LineWH    *uuid.UUID
	}
	var lines []resLine
	for rows.Next() {
		var l resLine
		if rows.Scan(&l.ProductID, &l.Qty, &l.LineWH) == nil {
			lines = append(lines, l)
		}
	}
	rows.Close()

	now := time.Now()
	for _, l := range lines {
		wh := l.LineWH
		if wh == nil {
			wh = orderWarehouse
		}
		if wh == nil || l.Qty <= 0 {
			continue
		}
		if _, uErr := tx.Exec(`
			INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id,
				quantity_on_hand, quantity_reserved, last_movement_date, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $7, $7)
			ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE SET
				quantity_reserved = inventory.quantity_reserved + EXCLUDED.quantity_reserved,
				updated_at = EXCLUDED.updated_at`,
			uuid.New(), tenantID, orgID, l.ProductID, *wh, l.Qty, now,
		); uErr != nil {
			h.log.Error("reserveSalesOrderStock: reserve failed", "error", uErr, "product_id", l.ProductID)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		h.log.Error("reserveSalesOrderStock: commit failed", "error", err)
	}
}

// releaseSalesOrderReservation frees the still-reserved remainder of an order's
// lines (quantity - delivered), used on cancel. Floors at zero — reservations
// from before this feature (or drift) can never push reserved negative.
func (h *Handler) releaseSalesOrderReservation(tenantID uuid.UUID, orderID uuid.UUID, orderWarehouse *uuid.UUID) {
	rows, err := h.db.Query(`
		SELECT sol.product_id, sol.quantity - COALESCE(sol.quantity_delivered, 0), sol.warehouse_id
		FROM sales_order_lines sol
		WHERE sol.sales_order_id = $1 AND sol.quantity > COALESCE(sol.quantity_delivered, 0)`,
		orderID)
	if err != nil {
		return
	}
	type resLine struct {
		ProductID uuid.UUID
		Qty       float64
		LineWH    *uuid.UUID
	}
	var lines []resLine
	for rows.Next() {
		var l resLine
		if rows.Scan(&l.ProductID, &l.Qty, &l.LineWH) == nil {
			lines = append(lines, l)
		}
	}
	rows.Close()

	now := time.Now()
	for _, l := range lines {
		wh := l.LineWH
		if wh == nil {
			wh = orderWarehouse
		}
		if wh == nil || l.Qty <= 0 {
			continue
		}
		if _, execErr := h.db.Exec(`
			UPDATE inventory SET quantity_reserved = GREATEST(0, quantity_reserved - $1), updated_at = $2
			WHERE tenant_id = $3 AND product_id = $4 AND warehouse_id = $5`,
			l.Qty, now, tenantID, l.ProductID, *wh); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE inventory", "error", execErr)
		}
	}
}

// salesOrgScopeOK verifies a record's organization against the request's org
// header. List endpoints were always org-scoped, but detail/mutate endpoints
// checked tenant only — any UUID from another company in the same tenant could
// be read, confirmed, shipped or paid (audit §7). Rows without an org (legacy)
// and requests without an org header pass.
func (h *Handler) salesOrgScopeOK(c *gin.Context, table string, id, tenantID uuid.UUID) bool {
	orgID, ok := middleware.GetOrganizationID(c)
	if !ok || orgID == uuid.Nil {
		return true
	}
	var recOrg sql.NullString
	if err := h.db.QueryRow(
		"SELECT organization_id FROM "+table+" WHERE id = $1 AND tenant_id = $2", id, tenantID,
	).Scan(&recOrg); err != nil {
		return true // not found — the handler's own lookup will 404
	}
	if !recOrg.Valid || recOrg.String == "" {
		return true
	}
	return recOrg.String == orgID.String()
}
