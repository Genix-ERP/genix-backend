"""
Ishlab chiqarish (Manufacturing) v2 integrity tests — migration 459,
docs/production-integration-map.md kontrakti.

Invariants under test:
  1. MO lifecycle happy path: draft -> confirm -> start -> complete; each
     transition is reflected in production_orders.status and a draft MO has
     no journal entry.
  2. START issues BOM components ONLY via applyStockDelta: component on-hand
     drops by BOM demand, inventory_transactions gets self-contained
     'production_out' rows (product_id + warehouse_id, signed negative,
     reference_type='production_order'), and a balanced leaf-only JE with
     source_type='production_start' (Dt 2010 WIP / Kt 1010 Xom ashyo) whose
     header totals equal the line sums, all in one transaction.
  3. COMPLETE receives finished goods ('production_in', reason
     'production_complete'), posts source_type='production_complete'
     (Dt 2810 / Kt 2010; material leg skipped because start already posted
     it) and the JE total equals the FG receipt value.
  4. A second COMPLETE is a hard 400 and creates NO extra JE and NO extra
     inventory_transactions rows (no double production_return /
     material_return receipts — the cumulative-drift bug of v1).
  5. CANCEL after start returns the issued materials ('return', reason
     'production_cancel'), restores component on-hand exactly, and posts a
     balanced source_type='production_cancel' reversal (Dt 1010 / Kt 2010).
     A second cancel is refused (400/404) with no second JE.
  6. GET /production-orders/stats honours the §7 contract: documented
     `totals` keys, non-null lists (daily_series, status_counts,
     work_center_load, late_orders, shortages), gap-filled daily series for
     a custom ?from&to period, and the legacy top-level keys
     (average_utilization, in_progress_orders, ...) kept for the rollout.
  7. After all of the above the global ledger invariants of test_16 still
     hold: all posted JEs balanced, no postings to group accounts, no
     posted JEs with <2 lines, header totals match line sums.
  8. MRP cycle (migrations 463-466): POST /mrp/run nets demand vs supply and
     writes pending recommendations with correct shortage quantities;
     'purchase' for a BOM-less component, 'manufacture' for a product with
     an active BOM. Execute claims pending->executed atomically (second
     execute is 400) and creates the target document as DRAFT
     (purchase_requisition / production_order with source_type='mrp').
  9. POST /work-orders/:id/quality-check writes a quality_checks row with
     derived result/pass_rate; stats totals expose quality_checks_count and
     quality_pass_rate; a defect-reason check flips the report Pareto to
     pareto_by='reason'.
 10. CompleteProductionOrder persists actual_cost/material_cost/labor_cost/
     overhead_cost on the order row; the report's totals.total_cost uses
     COALESCE(NULLIF(actual_cost,0), planned_cost) and variance[]/
     variance_sum reflect plan-vs-fact for fully costed completions.
 11. GET /payroll/piecework returns the ishbay summary contract
     (employees[], total_amount, from, to).

All records created here use the T27 prefix; products/BOMs are soft-deleted
at module teardown. Requires the API server and the seeded dev DB
(see conftest.py).
"""

import uuid
from datetime import date

import psycopg2
import pytest

from conftest import (
    DB_HOST, DB_NAME, DB_PASSWORD, DB_PORT, DB_USER,
    first_of_month, today,
)
from test_16_cross_module_gl import api_data, assert_je_balanced_and_leaf


# ============================================
# CONSTANTS
# ============================================

COMPONENT_COST = 5_000.0     # so'm per component unit
BOM_QTY_PER_UNIT = 2.0       # components per 1 finished good (BOM output=1)


# ============================================
# MODULE FIXTURES & HELPERS
# ============================================

@pytest.fixture(scope="module", autouse=True)
def _require_auth(api_client):
    if not api_client.token:
        pytest.skip("No auth token — cannot run the production suite")


@pytest.fixture(scope="module", autouse=True)
def _cleanup_t27(api_client):
    """Soft-delete every T27-prefixed product/BOM after the module so the
    demo tenant's product and BOM lists don't accumulate test rows
    (conftest employee-cleanup convention). JEs and inventory ledger rows
    are intentionally left in place — the suite accumulates those by
    design, like test_16/test_23."""
    yield
    conn = psycopg2.connect(
        host=DB_HOST, port=DB_PORT, user=DB_USER,
        password=DB_PASSWORD, dbname=DB_NAME,
    )
    conn.autocommit = True
    tid = api_client.tenant_id
    with conn.cursor() as cur:
        for stmt, params in (
            # MRP artifacts for T27 products (recs + the draft requisition)
            ("DELETE FROM mrp_recommendations WHERE tenant_id = %s AND product_id IN "
             "(SELECT id FROM products WHERE tenant_id = %s AND code LIKE 'T27-%%')",
             (tid, tid)),
            ("UPDATE purchase_requisitions SET deleted_at = NOW(), updated_at = NOW() "
             "WHERE tenant_id = %s AND deleted_at IS NULL AND id IN ("
             "  SELECT l.requisition_id FROM purchase_requisition_lines l"
             "  JOIN products p ON p.id = l.product_id"
             "  WHERE p.tenant_id = %s AND p.code LIKE 'T27-%%')",
             (tid, tid)),
            ("UPDATE quality_checks SET deleted_at = NOW(), updated_at = NOW() "
             "WHERE tenant_id = %s AND deleted_at IS NULL AND product_id IN "
             "(SELECT id FROM products WHERE tenant_id = %s AND code LIKE 'T27-%%')",
             (tid, tid)),
            ("UPDATE work_centers SET deleted_at = NOW(), updated_at = NOW() "
             "WHERE tenant_id = %s AND code LIKE 'T27-%%' AND deleted_at IS NULL",
             (tid,)),
            ("UPDATE product_boms SET deleted_at = NOW(), is_active = false, updated_at = NOW() "
             "WHERE tenant_id = %s AND code LIKE 'T27-%%' AND deleted_at IS NULL",
             (tid,)),
            ("UPDATE products SET deleted_at = NOW(), updated_at = NOW() "
             "WHERE tenant_id = %s AND (code LIKE 'T27-%%' OR sku LIKE 'T27-%%') AND deleted_at IS NULL",
             (tid,)),
        ):
            try:
                cur.execute(stmt, params)
            except Exception:
                pass  # cleanup is best-effort, never fails the suite
    conn.close()


@pytest.fixture(scope="module")
def warehouse(api_client, db_read):
    """An active warehouse owned by the client's ORGANIZATION (not just the
    tenant) — CreateProductionOrder silently drops cross-org warehouses and
    Start's greedy sourcing only pulls from org warehouses."""
    db_read.execute(
        """SELECT id FROM warehouses
           WHERE tenant_id = %s AND organization_id = %s
             AND deleted_at IS NULL AND is_active = true
           ORDER BY created_at ASC LIMIT 1""",
        (api_client.tenant_id, api_client.org_id),
    )
    row = db_read.fetchone()
    if row:
        return str(row["id"])
    resp = api_client.post("/warehouses", json={
        "name": f"T27 Ishlab chiqarish ombori {uuid.uuid4().hex[:4]}",
        "code": f"T27W{uuid.uuid4().hex[:5].upper()}",
        "address": {"city": "Toshkent"},
    })
    assert resp.status_code in (200, 201), f"warehouse create failed: {resp.status_code} {resp.text[:300]}"
    return api_data(resp)["id"]


def _make_product(api_client, tag, **overrides):
    payload = {
        "name": f"T27 {tag}",
        "code": f"T27-{tag}",
        "sku": f"T27-{tag}",
        "type": "product",
        "is_stockable": True,
        "track_inventory": True,
        "cost_price": COMPONENT_COST,
        "list_price": COMPONENT_COST * 2,
        "is_active": True,
    }
    payload.update(overrides)
    resp = api_client.post("/products", json=payload)
    assert resp.status_code in (200, 201), (
        f"product create failed ({payload['code']}): {resp.status_code} {resp.text[:400]}"
    )
    return api_data(resp)["id"]


def _stock(api_client, product_id, warehouse_id, qty, unit_cost):
    resp = api_client.post("/inventory/adjust", json={
        "product_id": product_id, "warehouse_id": warehouse_id,
        "quantity": qty, "unit_cost": unit_cost, "reason": "T27 kirim",
    })
    assert resp.status_code == 200, (
        f"inventory adjust failed: {resp.status_code} {resp.text[:400]}"
    )


def _make_work_center(api_client, tag, **overrides):
    payload = {
        "code": f"T27-WC-{tag}",
        "name": f"T27 ish markazi {tag}",
        "capacity_per_hour": 10.0,
        "hourly_cost": 60_000.0,
        "working_hours_per_day": 8.0,
        "currency": "UZS",
        "status": "active",
    }
    payload.update(overrides)
    resp = api_client.post("/work-centers", json=payload)
    assert resp.status_code in (200, 201), (
        f"work center create failed: {resp.status_code} {resp.text[:400]}"
    )
    return api_data(resp)["id"]


def _make_bom(api_client, fg_id, component_id, warehouse_id, tag):
    resp = api_client.post("/boms", json={
        "code": f"T27-{tag}",
        "name": f"T27 BOM {tag}",
        "product_id": fg_id,
        "quantity": 1,
        "warehouse_id": warehouse_id,
        "lines": [
            {"component_id": component_id, "quantity": BOM_QTY_PER_UNIT, "unit_of_measure": "pcs"},
        ],
    })
    assert resp.status_code in (200, 201), (
        f"BOM create failed: {resp.status_code} {resp.text[:400]}"
    )
    return api_data(resp)["id"]


def _make_mo(api_client, fg_id, bom_id, warehouse_id, qty, tag):
    resp = api_client.post("/production-orders", json={
        "name": f"T27 MO {tag}",
        "product_id": fg_id,
        "bom_id": bom_id,
        "quantity_planned": qty,
        "uom": "pcs",
        "warehouse_id": warehouse_id,
        "scheduled_start": today(),
        "scheduled_end": today(),
        "notes": "T27 integration test",
    })
    assert resp.status_code in (200, 201), (
        f"MO create failed: {resp.status_code} {resp.text[:400]}"
    )
    return api_data(resp)


def _on_hand(db_read, tenant_id, product_id, warehouse_id):
    db_read.execute(
        """SELECT COALESCE(SUM(quantity_on_hand), 0) AS q FROM inventory
           WHERE tenant_id = %s AND product_id = %s AND warehouse_id = %s""",
        (tenant_id, product_id, warehouse_id),
    )
    return float(db_read.fetchone()["q"])


def _find_je(db_read, tenant_id, source_type, source_id):
    db_read.execute(
        """SELECT id, entry_number, status FROM journal_entries
           WHERE tenant_id = %s AND source_type = %s AND source_id = %s
             AND deleted_at IS NULL
           ORDER BY created_at DESC""",
        (tenant_id, source_type, source_id),
    )
    return db_read.fetchall()


def _mo_txn_rows(db_read, tenant_id, mo_id, *types):
    # inventory_transactions.deleted_at exists since migration 460 (added
    # after this suite exposed the missing-column cancel bug).
    q = """SELECT * FROM inventory_transactions
           WHERE tenant_id = %s AND reference_type = 'production_order'
             AND reference_id = %s AND deleted_at IS NULL"""
    params = [tenant_id, mo_id]
    if types:
        q += " AND transaction_type = ANY(%s)"
        params.append(list(types))
    db_read.execute(q, params)
    return db_read.fetchall()


def _mo_footprint(db_read, tenant_id, mo_id):
    """(JE count, inventory txn count) for double-post comparisons."""
    db_read.execute(
        """SELECT COUNT(*) AS n FROM journal_entries
           WHERE tenant_id = %s AND source_id = %s AND deleted_at IS NULL""",
        (tenant_id, mo_id),
    )
    jes = int(db_read.fetchone()["n"])
    db_read.execute(
        """SELECT COUNT(*) AS n FROM inventory_transactions
           WHERE tenant_id = %s AND reference_type = 'production_order'
             AND reference_id = %s AND deleted_at IS NULL""",
        (tenant_id, mo_id),
    )
    txns = int(db_read.fetchone()["n"])
    return jes, txns


# ============================================
# FULL MO HAPPY PATH (create -> confirm -> start -> complete)
# ============================================

class TestMOFullLifecycle:
    MO_QTY = 10.0
    STOCK = 100.0
    CONSUMED = MO_QTY * BOM_QTY_PER_UNIT           # 20
    START_JE_TOTAL = CONSUMED * COMPONENT_COST     # 100 000

    @pytest.fixture(scope="class")
    def flow(self, api_client, db_read, warehouse):
        tag = uuid.uuid4().hex[:8].upper()
        component_id = _make_product(api_client, f"KOMP-{tag}", cost_price=COMPONENT_COST)
        fg_id = _make_product(api_client, f"TM-{tag}", cost_price=0)
        _stock(api_client, component_id, warehouse, self.STOCK, COMPONENT_COST)
        bom_id = _make_bom(api_client, fg_id, component_id, warehouse, f"BOM-{tag}")
        mo = _make_mo(api_client, fg_id, bom_id, warehouse, self.MO_QTY, tag)
        return {
            "component_id": component_id,
            "fg_id": fg_id,
            "bom_id": bom_id,
            "mo_id": str(mo["id"]),
            "mo": mo,
            "warehouse": warehouse,
        }

    def test_draft_status_and_no_je(self, api_client, db_read, flow):
        assert flow["mo"]["status"] == "draft", f"new MO status: {flow['mo']}"
        db_read.execute(
            """SELECT COUNT(*) AS n FROM journal_entries
               WHERE tenant_id = %s AND source_id = %s AND deleted_at IS NULL""",
            (api_client.tenant_id, flow["mo_id"]),
        )
        assert int(db_read.fetchone()["n"]) == 0, "Draft MO already has a journal entry"

    def test_confirm(self, api_client, db_read, flow):
        resp = api_client.post(f"/production-orders/{flow['mo_id']}/confirm")
        assert resp.status_code in (200, 201), (
            f"confirm failed: {resp.status_code} {resp.text[:400]}"
        )
        assert api_data(resp)["status"] == "confirmed"
        # Confirm generates at least one work order
        db_read.execute(
            """SELECT COUNT(*) AS n FROM work_orders
               WHERE production_order_id = %s AND tenant_id = %s AND deleted_at IS NULL""",
            (flow["mo_id"], api_client.tenant_id),
        )
        assert int(db_read.fetchone()["n"]) >= 1, "confirm did not create a work order"
        # v2 guard: NO planned-cost JE at confirm (double-count bug fix)
        db_read.execute(
            """SELECT COUNT(*) AS n FROM journal_entries
               WHERE tenant_id = %s AND source_id = %s AND deleted_at IS NULL""",
            (api_client.tenant_id, flow["mo_id"]),
        )
        assert int(db_read.fetchone()["n"]) == 0, "confirm must not post a JE (v2 contract)"

    def test_start_deducts_components(self, api_client, db_read, flow):
        before = _on_hand(db_read, api_client.tenant_id, flow["component_id"], flow["warehouse"])
        assert abs(before - self.STOCK) < 0.001, (
            f"precondition: component on-hand {before}, expected {self.STOCK}"
        )
        resp = api_client.post(f"/production-orders/{flow['mo_id']}/start")
        assert resp.status_code in (200, 201), (
            f"start failed: {resp.status_code} {resp.text[:400]}"
        )
        assert api_data(resp)["status"] == "in_progress"

        after = _on_hand(db_read, api_client.tenant_id, flow["component_id"], flow["warehouse"])
        assert abs(after - (self.STOCK - self.CONSUMED)) < 0.001, (
            f"component on-hand after start: {after}, expected {self.STOCK - self.CONSUMED}"
        )

    def test_start_ledger_rows_selfcontained(self, api_client, db_read, flow):
        rows = _mo_txn_rows(db_read, api_client.tenant_id, flow["mo_id"], "production_out")
        assert rows, "no production_out inventory_transactions for the started MO"
        total_out = 0.0
        for r in rows:
            assert r["product_id"] is not None, f"production_out row without product_id: {r['id']}"
            assert r["warehouse_id"] is not None, f"production_out row without warehouse_id: {r['id']}"
            assert str(r["product_id"]) == flow["component_id"]
            assert float(r["quantity"]) < 0, (
                f"production_out must be a signed NEGATIVE delta, got {r['quantity']}"
            )
            total_out += -float(r["quantity"])
        assert abs(total_out - self.CONSUMED) < 0.001, (
            f"issued {total_out}, expected {self.CONSUMED}"
        )

    def test_start_je_balanced_leaf_totals(self, api_client, db_read, flow):
        jes = _find_je(db_read, api_client.tenant_id, "production_start", flow["mo_id"])
        assert len(jes) == 1, (
            f"expected exactly 1 production_start JE, found {len(jes)}: "
            f"{[j['entry_number'] for j in jes]}"
        )
        je, lines = assert_je_balanced_and_leaf(
            db_read, jes[0]["id"], expect_total=self.START_JE_TOTAL
        )
        assert je["status"] == "posted"
        debit_codes = {l["account_code"] for l in lines if float(l["debit_amount"] or 0) > 0}
        credit_codes = {l["account_code"] for l in lines if float(l["credit_amount"] or 0) > 0}
        assert "2010" in debit_codes, f"Dt 2010 (WIP) yo'q, debit: {debit_codes}"
        assert "1010" in credit_codes, f"Kt 1010 (Xom ashyo) yo'q, kredit: {credit_codes}"

    def test_complete_receives_fg(self, api_client, db_read, flow):
        fg_before = _on_hand(db_read, api_client.tenant_id, flow["fg_id"], flow["warehouse"])
        resp = api_client.post(
            f"/production-orders/{flow['mo_id']}/complete",
            json={"quantity_produced": self.MO_QTY},
        )
        assert resp.status_code in (200, 201), (
            f"complete failed: {resp.status_code} {resp.text[:400]}"
        )
        assert api_data(resp)["status"] == "completed"

        fg_after = _on_hand(db_read, api_client.tenant_id, flow["fg_id"], flow["warehouse"])
        assert abs((fg_after - fg_before) - self.MO_QTY) < 0.001, (
            f"FG on-hand grew by {fg_after - fg_before}, expected {self.MO_QTY}"
        )
        receipts = _mo_txn_rows(db_read, api_client.tenant_id, flow["mo_id"], "production_in")
        assert receipts, "no production_in inventory_transactions after complete"
        assert any(r["reason"] == "production_complete" for r in receipts), (
            f"FG receipt reasons: {[r['reason'] for r in receipts]}"
        )
        for r in receipts:
            assert r["product_id"] is not None and r["warehouse_id"] is not None
            assert str(r["product_id"]) == flow["fg_id"]

    def test_complete_je_balanced_leaf_totals(self, api_client, db_read, flow):
        jes = _find_je(db_read, api_client.tenant_id, "production_complete", flow["mo_id"])
        assert len(jes) == 1, (
            f"expected exactly 1 production_complete JE, found {len(jes)}"
        )
        # JE total must equal the FG receipt value written in the same tx
        receipts = _mo_txn_rows(db_read, api_client.tenant_id, flow["mo_id"], "production_in")
        fg_value = sum(float(r["total_cost"] or 0) for r in receipts)
        assert fg_value > 0, f"FG receipt has no value: {receipts}"
        je, lines = assert_je_balanced_and_leaf(db_read, jes[0]["id"], expect_total=fg_value)
        assert je["status"] == "posted"
        debit_codes = {l["account_code"] for l in lines if float(l["debit_amount"] or 0) > 0}
        credit_codes = {l["account_code"] for l in lines if float(l["credit_amount"] or 0) > 0}
        assert "2810" in debit_codes, f"Dt 2810 (Tayyor mahsulot) yo'q, debit: {debit_codes}"
        assert "2010" in credit_codes, f"Kt 2010 (WIP) yo'q, kredit: {credit_codes}"

    def test_second_complete_is_400_and_no_side_effects(self, api_client, db_read, flow):
        jes_before, txns_before = _mo_footprint(db_read, api_client.tenant_id, flow["mo_id"])
        fg_before = _on_hand(db_read, api_client.tenant_id, flow["fg_id"], flow["warehouse"])

        resp = api_client.post(
            f"/production-orders/{flow['mo_id']}/complete",
            json={"quantity_produced": self.MO_QTY},
        )
        assert resp.status_code == 400, (
            f"second complete must be a hard 400, got {resp.status_code}: {resp.text[:300]}"
        )

        jes_after, txns_after = _mo_footprint(db_read, api_client.tenant_id, flow["mo_id"])
        assert jes_after == jes_before, (
            f"second complete created JEs: {jes_before} -> {jes_after}"
        )
        assert txns_after == txns_before, (
            f"second complete created inventory transactions: {txns_before} -> {txns_after} "
            "(double production_return / material_return receipt)"
        )
        # Explicitly: no material_return / production_return artifacts appeared
        returns = _mo_txn_rows(db_read, api_client.tenant_id, flow["mo_id"], "return")
        assert not returns, (
            f"full-quantity completion must not write return rows, got {len(returns)}"
        )
        db_read.execute(
            """SELECT COUNT(*) AS n FROM journal_entries
               WHERE tenant_id = %s AND source_type = 'production_return'
                 AND source_id = %s AND deleted_at IS NULL""",
            (api_client.tenant_id, flow["mo_id"]),
        )
        assert int(db_read.fetchone()["n"]) == 0, "unexpected production_return JE"

        fg_after = _on_hand(db_read, api_client.tenant_id, flow["fg_id"], flow["warehouse"])
        assert abs(fg_after - fg_before) < 0.001, (
            f"second complete changed FG stock: {fg_before} -> {fg_after}"
        )


# ============================================
# CANCEL PATH (separate MO)
# ============================================

class TestCancelFlow:
    MO_QTY = 5.0
    STOCK = 50.0
    CONSUMED = MO_QTY * BOM_QTY_PER_UNIT          # 10
    REVERSAL_TOTAL = CONSUMED * COMPONENT_COST    # 50 000

    @pytest.fixture(scope="class")
    def flow(self, api_client, db_read, warehouse):
        tag = uuid.uuid4().hex[:8].upper()
        component_id = _make_product(api_client, f"KOMP-{tag}", cost_price=COMPONENT_COST)
        fg_id = _make_product(api_client, f"TM-{tag}", cost_price=0)
        _stock(api_client, component_id, warehouse, self.STOCK, COMPONENT_COST)
        bom_id = _make_bom(api_client, fg_id, component_id, warehouse, f"BOM-{tag}")
        mo = _make_mo(api_client, fg_id, bom_id, warehouse, self.MO_QTY, tag)
        mo_id = str(mo["id"])

        resp = api_client.post(f"/production-orders/{mo_id}/confirm")
        assert resp.status_code in (200, 201), f"confirm failed: {resp.status_code} {resp.text[:400]}"
        resp = api_client.post(f"/production-orders/{mo_id}/start")
        assert resp.status_code in (200, 201), f"start failed: {resp.status_code} {resp.text[:400]}"

        return {
            "component_id": component_id,
            "fg_id": fg_id,
            "mo_id": mo_id,
            "warehouse": warehouse,
        }

    def test_cancel_restores_stock(self, api_client, db_read, flow):
        issued = _on_hand(db_read, api_client.tenant_id, flow["component_id"], flow["warehouse"])
        assert abs(issued - (self.STOCK - self.CONSUMED)) < 0.001, (
            f"precondition: on-hand after start {issued}, expected {self.STOCK - self.CONSUMED}"
        )

        resp = api_client.post(
            f"/production-orders/{flow['mo_id']}/cancel",
            json={"reason": "T27 test bekor qilish"},
        )
        assert resp.status_code in (200, 201), (
            f"cancel failed: {resp.status_code} {resp.text[:400]}"
        )
        assert api_data(resp)["status"] == "cancelled"

        restored = _on_hand(db_read, api_client.tenant_id, flow["component_id"], flow["warehouse"])
        assert abs(restored - self.STOCK) < 0.001, (
            f"component stock not restored on cancel: {restored}, expected {self.STOCK}"
        )
        returns = _mo_txn_rows(db_read, api_client.tenant_id, flow["mo_id"], "return")
        assert returns, "cancel wrote no 'return' inventory_transactions"
        assert all(r["reason"] == "production_cancel" for r in returns), (
            f"return reasons: {[r['reason'] for r in returns]}"
        )
        for r in returns:
            assert r["product_id"] is not None and r["warehouse_id"] is not None
            assert float(r["quantity"]) > 0, "cancel return must be a positive delta"

    def test_cancel_je_balanced_leaf_totals(self, api_client, db_read, flow):
        jes = _find_je(db_read, api_client.tenant_id, "production_cancel", flow["mo_id"])
        assert len(jes) == 1, (
            f"expected exactly 1 production_cancel JE, found {len(jes)}"
        )
        je, lines = assert_je_balanced_and_leaf(
            db_read, jes[0]["id"], expect_total=self.REVERSAL_TOTAL
        )
        assert je["status"] == "posted"
        debit_codes = {l["account_code"] for l in lines if float(l["debit_amount"] or 0) > 0}
        credit_codes = {l["account_code"] for l in lines if float(l["credit_amount"] or 0) > 0}
        assert "1010" in debit_codes, f"Dt 1010 (Xom ashyo qaytishi) yo'q, debit: {debit_codes}"
        assert "2010" in credit_codes, f"Kt 2010 (WIP reversal) yo'q, kredit: {credit_codes}"

    def test_second_cancel_idempotent(self, api_client, db_read, flow):
        jes_before, txns_before = _mo_footprint(db_read, api_client.tenant_id, flow["mo_id"])
        stock_before = _on_hand(db_read, api_client.tenant_id, flow["component_id"], flow["warehouse"])

        resp = api_client.post(
            f"/production-orders/{flow['mo_id']}/cancel",
            json={"reason": "T27 ikkinchi bekor qilish"},
        )
        assert resp.status_code in (400, 404), (
            f"second cancel must be refused (400/404), got {resp.status_code}: {resp.text[:300]}"
        )

        jes_after, txns_after = _mo_footprint(db_read, api_client.tenant_id, flow["mo_id"])
        assert jes_after == jes_before, (
            f"second cancel created JEs: {jes_before} -> {jes_after}"
        )
        assert txns_after == txns_before, (
            f"second cancel moved stock again: {txns_before} -> {txns_after}"
        )
        # Idempotency check proper: never MORE than one cancel JE. (Whether
        # exactly one exists at all is test_cancel_je_balanced_leaf_totals.)
        cancel_jes = _find_je(db_read, api_client.tenant_id, "production_cancel", flow["mo_id"])
        assert len(cancel_jes) <= 1, f"duplicate production_cancel JEs: {len(cancel_jes)}"

        stock_after = _on_hand(db_read, api_client.tenant_id, flow["component_id"], flow["warehouse"])
        assert abs(stock_after - stock_before) < 0.001, (
            f"second cancel changed stock: {stock_before} -> {stock_after}"
        )


# ============================================
# MRP CYCLE — /mrp/run, /mrp/recommendations, execute handoff
# ============================================

class TestMRPCycle:
    PURCHASE_STOCK = 10.0
    MO1_QTY = 20.0   # component demand 40 vs 10 on hand -> purchase rec 30
    MO2_QTY = 5.0    # semi-finished demand 10 vs 0     -> manufacture rec 10
    EXPECT_PURCHASE_QTY = MO1_QTY * BOM_QTY_PER_UNIT - PURCHASE_STOCK
    EXPECT_MANUF_QTY = MO2_QTY * BOM_QTY_PER_UNIT

    @pytest.fixture(scope="class")
    def env(self, api_client, db_read, warehouse):
        tag = uuid.uuid4().hex[:8].upper()

        # Scenario A: BOM-less component, stocked BELOW a confirmed MO's need
        comp = _make_product(api_client, f"KOMP-{tag}")
        _stock(api_client, comp, warehouse, self.PURCHASE_STOCK, COMPONENT_COST)
        fg1 = _make_product(api_client, f"TM-{tag}", cost_price=0)
        bom1 = _make_bom(api_client, fg1, comp, warehouse, f"BOM-{tag}")
        mo1 = _make_mo(api_client, fg1, bom1, warehouse, self.MO1_QTY, tag)
        resp = api_client.post(f"/production-orders/{mo1['id']}/confirm")
        assert resp.status_code in (200, 201), f"confirm mo1: {resp.status_code} {resp.text[:300]}"

        # Scenario B: shortage product WITH an active default BOM ->
        # 'manufacture'. The semi-finished product is a component of fg2's
        # BOM and has its own BOM (comp_b), zero stock.
        comp_b = _make_product(api_client, f"KOMPB-{tag}")
        semi = _make_product(api_client, f"YIG-{tag}")
        semi_bom = _make_bom(api_client, semi, comp_b, warehouse, f"BOMY-{tag}")
        fg2 = _make_product(api_client, f"TM2-{tag}", cost_price=0)
        bom2 = _make_bom(api_client, fg2, semi, warehouse, f"BOM2-{tag}")
        mo2 = _make_mo(api_client, fg2, bom2, warehouse, self.MO2_QTY, tag)
        resp = api_client.post(f"/production-orders/{mo2['id']}/confirm")
        assert resp.status_code in (200, 201), f"confirm mo2: {resp.status_code} {resp.text[:300]}"

        run_resp = api_client.post("/mrp/run")
        assert run_resp.status_code == 200, (
            f"mrp/run failed: {run_resp.status_code} {run_resp.text[:400]}"
        )
        recs_resp = api_client.get("/mrp/recommendations")
        assert recs_resp.status_code == 200, (
            f"mrp/recommendations failed: {recs_resp.status_code} {recs_resp.text[:400]}"
        )
        return {
            "comp": comp, "semi": semi, "semi_bom": semi_bom,
            "mo1_id": str(mo1["id"]), "mo2_id": str(mo2["id"]),
            "run": api_data(run_resp), "recs": api_data(recs_resp),
        }

    @staticmethod
    def _rec_for(env, product_id):
        return next((r for r in env["recs"] if r["product_id"] == product_id), None)

    def test_run_summary_counts(self, env):
        run = env["run"]
        for key in ("demand_rows", "supply_rows", "recommendations", "run_at"):
            assert key in run, f"mrp/run missing {key}: {run}"
        assert run["demand_rows"] > 0, f"no demand snapshotted: {run}"
        assert run["supply_rows"] > 0, f"no supply snapshotted: {run}"
        assert run["recommendations"] >= 2, (
            f"expected >=2 recommendations (purchase + manufacture), got {run}"
        )

    def test_purchase_recommendation_shape(self, env):
        rec = self._rec_for(env, env["comp"])
        assert rec is not None, (
            f"no pending recommendation for the short component; got products: "
            f"{[r['product_code'] for r in env['recs']]}"
        )
        assert rec["recommendation_type"] == "purchase", rec
        assert abs(float(rec["quantity"]) - self.EXPECT_PURCHASE_QTY) < 0.01, (
            f"quantity {rec['quantity']}, expected {self.EXPECT_PURCHASE_QTY} "
            f"(demand {self.MO1_QTY * BOM_QTY_PER_UNIT} - on hand {self.PURCHASE_STOCK})"
        )
        assert rec["status"] == "pending", rec
        assert rec["urgency"] in ("low", "normal", "high", "critical"), rec
        assert rec["reason"], "reason bo'sh"

    def test_manufacture_recommendation_for_bom_product(self, env):
        rec = self._rec_for(env, env["semi"])
        assert rec is not None, (
            f"no pending recommendation for the semi-finished product; got: "
            f"{[r['product_code'] for r in env['recs']]}"
        )
        assert rec["recommendation_type"] == "manufacture", (
            f"product with an active default BOM must net to 'manufacture', got {rec}"
        )
        assert abs(float(rec["quantity"]) - self.EXPECT_MANUF_QTY) < 0.01, rec

    def test_execute_purchase_creates_draft_requisition(self, api_client, db_read, env):
        rec = self._rec_for(env, env["comp"])
        assert rec is not None, "purchase recommendation missing (see previous test)"
        resp = api_client.post(f"/mrp/recommendations/{rec['id']}/execute")
        assert resp.status_code == 200, (
            f"execute failed: {resp.status_code} {resp.text[:400]}"
        )
        data = api_data(resp)
        assert data["executed_type"] == "purchase_requisition", data
        env["purchase_rec_id"] = rec["id"]
        env["requisition_id"] = str(data["executed_id"])

        db_read.execute(
            "SELECT status, deleted_at FROM purchase_requisitions WHERE id = %s AND tenant_id = %s",
            (env["requisition_id"], api_client.tenant_id),
        )
        pr = db_read.fetchone()
        assert pr is not None, "requisition row not found in DB"
        assert pr["status"] == "draft", f"requisition status {pr['status']}, expected draft"
        db_read.execute(
            "SELECT product_id, quantity FROM purchase_requisition_lines WHERE requisition_id = %s",
            (env["requisition_id"],),
        )
        lines = db_read.fetchall()
        assert lines, "requisition has no lines"
        assert str(lines[0]["product_id"]) == env["comp"]
        assert abs(float(lines[0]["quantity"]) - self.EXPECT_PURCHASE_QTY) < 0.01, lines[0]

    def test_second_execute_is_400(self, api_client, env):
        rec_id = env.get("purchase_rec_id")
        assert rec_id, "purchase recommendation was not executed (see previous test)"
        resp = api_client.post(f"/mrp/recommendations/{rec_id}/execute")
        assert resp.status_code == 400, (
            f"second execute must be 400, got {resp.status_code}: {resp.text[:300]}"
        )

    def test_execute_manufacture_creates_draft_mo(self, api_client, db_read, env):
        rec = self._rec_for(env, env["semi"])
        assert rec is not None, "manufacture recommendation missing (see previous test)"
        resp = api_client.post(f"/mrp/recommendations/{rec['id']}/execute")
        assert resp.status_code == 200, (
            f"execute failed: {resp.status_code} {resp.text[:400]}"
        )
        data = api_data(resp)
        assert data["executed_type"] == "production_order", data
        env["mrp_mo_id"] = str(data["executed_id"])

        db_read.execute(
            """SELECT status, source_type, product_id, bom_id, quantity_planned
               FROM production_orders WHERE id = %s AND tenant_id = %s AND deleted_at IS NULL""",
            (env["mrp_mo_id"], api_client.tenant_id),
        )
        mo = db_read.fetchone()
        assert mo is not None, "MRP production order not found in DB"
        assert mo["status"] == "draft", f"MO status {mo['status']}, expected draft"
        assert mo["source_type"] == "mrp", f"source_type {mo['source_type']}, expected 'mrp'"
        assert str(mo["product_id"]) == env["semi"]
        assert str(mo["bom_id"]) == env["semi_bom"], "MO not linked to the default BOM"
        assert abs(float(mo["quantity_planned"]) - self.EXPECT_MANUF_QTY) < 0.01, mo

    def test_status_all_lists_executed(self, api_client, env):
        resp = api_client.get("/mrp/recommendations", params={"status": "all"})
        assert resp.status_code == 200, resp.text[:300]
        recs = api_data(resp)
        by_product = {r["product_id"]: r for r in recs
                      if r["product_id"] in (env["comp"], env["semi"])}
        assert len(by_product) == 2, (
            f"status=all misses executed recs: {sorted(by_product)}"
        )
        for r in by_product.values():
            assert r["status"] == "executed", r
            assert r.get("executed_id"), f"executed rec without executed_id: {r}"

    def test_zz_cleanup(self, api_client, env):
        """Cancel the demand MOs and delete the MRP draft so they stop
        feeding shortages/MRP demand on future runs."""
        for mo_id in (env["mo1_id"], env["mo2_id"]):
            resp = api_client.post(f"/production-orders/{mo_id}/cancel",
                                   json={"reason": "T27 MRP test yakuni"})
            assert resp.status_code in (200, 201), (
                f"cleanup cancel failed: {resp.status_code} {resp.text[:300]}"
            )
        if env.get("mrp_mo_id"):
            resp = api_client.delete(f"/production-orders/{env['mrp_mo_id']}")
            assert resp.status_code == 200, (
                f"cleanup delete of MRP draft MO failed: {resp.status_code} {resp.text[:300]}"
            )


# ============================================
# QUALITY CAPTURE — POST /work-orders/:id/quality-check
# ============================================

class TestQualityCapture:
    MO_QTY = 10.0
    STOCK = 100.0

    @pytest.fixture(scope="class")
    def env(self, api_client, db_read, warehouse):
        tag = uuid.uuid4().hex[:8].upper()
        comp = _make_product(api_client, f"KOMP-{tag}")
        _stock(api_client, comp, warehouse, self.STOCK, COMPONENT_COST)
        fg = _make_product(api_client, f"TM-{tag}", cost_price=0)
        bom = _make_bom(api_client, fg, comp, warehouse, f"BOM-{tag}")
        mo = _make_mo(api_client, fg, bom, warehouse, self.MO_QTY, tag)
        mo_id = str(mo["id"])
        resp = api_client.post(f"/production-orders/{mo_id}/confirm")
        assert resp.status_code in (200, 201), f"confirm: {resp.status_code} {resp.text[:300]}"
        resp = api_client.post(f"/production-orders/{mo_id}/start")
        assert resp.status_code in (200, 201), f"start: {resp.status_code} {resp.text[:300]}"

        db_read.execute(
            """SELECT id FROM work_orders
               WHERE production_order_id = %s AND tenant_id = %s AND deleted_at IS NULL
               ORDER BY sequence ASC LIMIT 1""",
            (mo_id, api_client.tenant_id),
        )
        wo = db_read.fetchone()
        assert wo is not None, "in_progress MO has no work order"
        return {"mo_id": mo_id, "wo_id": str(wo["id"])}

    def test_quality_check_creates_row(self, api_client, db_read, env):
        resp = api_client.post(f"/work-orders/{env['wo_id']}/quality-check", json={
            "quantity_inspected": 10,
            "quantity_passed": 8,
            "quantity_failed": 2,
            "defect_reason": "sirt qirilishi",
            "notes": "T27 sifat nazorati",
        })
        assert resp.status_code in (200, 201), (
            f"quality-check failed: {resp.status_code} {resp.text[:400]}"
        )
        data = api_data(resp)
        assert data["result"] == "partial", data
        assert abs(float(data["pass_rate"]) - 80.0) < 0.01, data
        env["qc_id"] = str(data["id"])

        db_read.execute("SELECT * FROM quality_checks WHERE id = %s", (env["qc_id"],))
        row = db_read.fetchone()
        assert row is not None, "quality_checks row not written"
        assert str(row["production_order_id"]) == env["mo_id"]
        assert str(row["work_order_id"]) == env["wo_id"]
        assert row["defect_type"] == "sirt qirilishi", row["defect_type"]
        assert float(row["quantity_failed"]) == 2
        assert abs(float(row["pass_rate"]) - 80.0) < 0.01

    def test_stats_quality_totals(self, api_client, env):
        assert env.get("qc_id"), "quality check was not created (see previous test)"
        resp = api_client.get("/production-orders/stats")
        assert resp.status_code == 200, resp.text[:300]
        totals = api_data(resp)["totals"]
        assert totals.get("quality_checks_count", 0) >= 1, (
            f"quality_checks_count={totals.get('quality_checks_count')}, expected >=1"
        )
        assert 0 <= float(totals.get("quality_pass_rate", -1)) <= 100, totals

    def test_report_pareto_switches_to_reason(self, api_client, env):
        assert env.get("qc_id"), "quality check was not created (see previous test)"
        resp = api_client.get("/production-orders/report")
        assert resp.status_code == 200, resp.text[:300]
        rep = api_data(resp)
        assert rep.get("pareto_by") == "reason", (
            f"pareto_by={rep.get('pareto_by')} — a defect-reason QC exists in the "
            "period, the Pareto must rank reasons"
        )
        names = [p["name"] for p in rep["scrap_pareto"]]
        assert "sirt qirilishi" in names, f"pareto misses our defect reason: {names}"

    def test_zz_cleanup(self, api_client, env):
        resp = api_client.post(f"/production-orders/{env['mo_id']}/cancel",
                               json={"reason": "T27 QC test yakuni"})
        assert resp.status_code in (200, 201), (
            f"cleanup cancel failed: {resp.status_code} {resp.text[:300]}"
        )


# ============================================
# COMPLETE PERSISTS COSTS + REPORT VARIANCE
# ============================================

class TestCompletePersistsCosts:
    MO_QTY = 8.0
    STOCK = 100.0

    @pytest.fixture(scope="class")
    def env(self, api_client, db_read, warehouse):
        tag = uuid.uuid4().hex[:8].upper()
        wc_id = _make_work_center(api_client, tag)
        comp = _make_product(api_client, f"KOMP-{tag}")
        _stock(api_client, comp, warehouse, self.STOCK, COMPONENT_COST)
        fg = _make_product(api_client, f"TM-{tag}", cost_price=0)
        bom = _make_bom(api_client, fg, comp, warehouse, f"BOM-{tag}")
        # An operation on the work center gives the MO a non-zero planned_cost
        # at confirm — required for the variance row to qualify.
        resp = api_client.post(f"/boms/{bom}/operations", json={
            "sequence": 10, "operation_name": "Yig'ish T27",
            "work_center_id": wc_id,
            "setup_time_minutes": 10, "run_time_minutes": 6,
        })
        assert resp.status_code in (200, 201), (
            f"BOM operation create failed: {resp.status_code} {resp.text[:400]}"
        )

        mo = _make_mo(api_client, fg, bom, warehouse, self.MO_QTY, tag)
        mo_id = str(mo["id"])
        resp = api_client.post(f"/production-orders/{mo_id}/confirm")
        assert resp.status_code in (200, 201), f"confirm: {resp.status_code} {resp.text[:300]}"
        resp = api_client.post(f"/production-orders/{mo_id}/start")
        assert resp.status_code in (200, 201), f"start: {resp.status_code} {resp.text[:300]}"
        resp = api_client.post(f"/production-orders/{mo_id}/complete",
                               json={"quantity_produced": self.MO_QTY})
        assert resp.status_code in (200, 201), f"complete: {resp.status_code} {resp.text[:300]}"
        return {"mo_id": mo_id, "mo_code": mo["code"]}

    def test_complete_persists_costs(self, db_read, env):
        db_read.execute(
            """SELECT planned_cost, actual_cost, material_cost, labor_cost, overhead_cost
               FROM production_orders WHERE id = %s""",
            (env["mo_id"],),
        )
        row = db_read.fetchone()
        assert row is not None
        assert float(row["actual_cost"] or 0) > 0, (
            f"actual_cost not persisted at complete: {dict(row)}"
        )
        assert float(row["material_cost"] or 0) > 0, (
            f"material_cost not persisted: {dict(row)}"
        )
        assert float(row["planned_cost"] or 0) > 0, (
            f"planned_cost is 0 despite a BOM operation: {dict(row)}"
        )
        env["planned_cost"] = float(row["planned_cost"])
        env["actual_cost"] = float(row["actual_cost"])

    def test_report_variance_and_total_cost(self, api_client, env):
        assert env.get("actual_cost"), "costs were not persisted (see previous test)"
        resp = api_client.get("/production-orders/report")
        assert resp.status_code == 200, resp.text[:300]
        rep = api_data(resp)
        totals = rep["totals"]
        assert "variance_sum" in totals, f"totals keys: {sorted(totals.keys())}"
        assert "variance" in rep and isinstance(rep["variance"], list)
        # total_cost now includes the persisted actual cost
        assert float(totals["total_cost"]) >= env["actual_cost"] - 0.01, (
            f"totals.total_cost={totals['total_cost']} does not include "
            f"actual_cost={env['actual_cost']}"
        )

        expected_var = env["actual_cost"] - env["planned_cost"]
        row = next((v for v in rep["variance"] if v["code"] == env["mo_code"]), None)
        if abs(expected_var) > 0.01:
            assert row is not None, (
                f"variance list misses {env['mo_code']} "
                f"(planned={env['planned_cost']}, actual={env['actual_cost']}): "
                f"{[v['code'] for v in rep['variance']]}"
            )
            assert abs(float(row["variance"]) - expected_var) < 1, (
                f"variance {row['variance']}, expected {expected_var}"
            )
        # variance_sum is consistent with the plan-vs-fact direction when
        # our MO is the only fully costed completion; at minimum it is a number
        assert isinstance(totals["variance_sum"], (int, float))


# ============================================
# ISHBAY (PIECEWORK) SUMMARY — GET /payroll/piecework
# ============================================

class TestPieceworkSummary:
    def test_contract_shape(self, api_client):
        resp = api_client.get("/payroll/piecework")
        assert resp.status_code == 200, (
            f"piecework failed: {resp.status_code} {resp.text[:400]}"
        )
        data = api_data(resp)
        for key in ("employees", "total_amount", "from", "to"):
            assert key in data, f"missing {key}: {sorted(data.keys())}"
        assert isinstance(data["employees"], list), "employees must be a non-null list"
        for row in data["employees"]:
            for key in ("employee_id", "employee_name", "entries", "good_quantity", "total_amount"):
                assert key in row, f"employee row missing {key}: {row}"


# ============================================
# STATS CONTRACT — GET /production-orders/stats
# ============================================

TOTALS_KEYS = (
    "active_orders", "draft_orders", "in_progress_orders", "completed_period",
    "overdue_orders", "quantity_produced", "quantity_scrapped", "scrap_rate",
    "otd_rate", "wip_value", "work_centers_active", "avg_load",
    # iteration-2 additions (quality capture, migrations 463-466)
    "quality_checks_count", "quality_pass_rate",
)
LIST_KEYS = ("daily_series", "status_counts", "work_center_load", "late_orders", "shortages")
LEGACY_KEYS = (
    "average_utilization", "in_progress_orders", "completed_orders",
    "draft_orders", "total_orders", "overdue_orders", "scrap_rate",
    "completion_rate", "total_work_centers", "active_work_centers",
)


class TestStatsContract:
    def test_stats_default_contract(self, api_client):
        resp = api_client.get("/production-orders/stats")
        assert resp.status_code == 200, f"stats failed: {resp.status_code} {resp.text[:400]}"
        data = api_data(resp)

        assert "totals" in data, f"missing 'totals', keys: {sorted(data.keys())}"
        for key in TOTALS_KEYS:
            assert key in data["totals"], f"missing totals.{key}"
        for key in LIST_KEYS:
            assert key in data, f"missing {key}"
            assert isinstance(data[key], list), (
                f"{key} must be a non-null list, got {type(data[key]).__name__}: {data[key]!r}"
            )
        assert data.get("as_of"), "missing as_of"
        for key in LEGACY_KEYS:
            assert key in data, f"legacy key {key} dropped (frontend rollout contract)"

        # This run definitely completed and cancelled MOs — the status
        # distribution (open + last 90 days) must reflect at least one.
        statuses = {s["status"] for s in data["status_counts"]}
        assert "completed" in statuses or "cancelled" in statuses, (
            f"status_counts misses this run's terminal MOs: {sorted(statuses)}"
        )

    def test_stats_period_daily_series_gap_filled(self, api_client):
        frm, to = first_of_month(), today()
        resp = api_client.get("/production-orders/stats", params={"from": frm, "to": to})
        assert resp.status_code == 200, f"stats?from&to failed: {resp.status_code} {resp.text[:400]}"
        data = api_data(resp)
        series = data["daily_series"]
        expected_days = date.today().day  # 1st .. today inclusive
        assert len(series) == expected_days, (
            f"daily_series not gap-filled for [{frm}..{to}]: {len(series)} points, "
            f"expected {expected_days}"
        )
        for pt in series:
            assert set(("date", "planned", "produced")) <= set(pt.keys()), f"bad point: {pt}"
        assert series[0]["date"] == frm and series[-1]["date"] == to, (
            f"series range {series[0]['date']}..{series[-1]['date']}, expected {frm}..{to}"
        )
        # totals must survive a custom period too
        for key in TOTALS_KEYS:
            assert key in data["totals"], f"missing totals.{key} with custom period"


# ============================================
# GLOBAL LEDGER INVARIANTS (test_16 re-run after production flows)
# ============================================

class TestLedgerInvariantsAfterProduction:
    """The four key test_16 TestLedgerInvariants checks, re-asserted after
    the production start/complete/cancel JEs above landed."""

    def test_all_posted_entries_balanced(self, db_read, tenant_id):
        db_read.execute(
            """SELECT je.id, je.entry_number,
                      SUM(jel.debit_amount) AS dt, SUM(jel.credit_amount) AS kt
               FROM journal_entries je
               JOIN journal_entry_lines jel ON jel.journal_entry_id = je.id
               WHERE je.tenant_id = %s AND je.status = 'posted' AND je.deleted_at IS NULL
               GROUP BY je.id, je.entry_number
               HAVING ABS(SUM(jel.debit_amount) - SUM(jel.credit_amount)) > 0.01
               LIMIT 20""",
            (tenant_id,),
        )
        bad = db_read.fetchall()
        assert not bad, (
            f"Balanslanmagan posted JE lar: "
            f"{[(b['entry_number'], float(b['dt']), float(b['kt'])) for b in bad]}"
        )

    def test_no_postings_to_group_accounts(self, db_read, tenant_id):
        db_read.execute(
            """SELECT DISTINCT a.code, a.name
               FROM journal_entry_lines jel
               JOIN accounts a ON a.id = jel.account_id
               JOIN journal_entries je ON je.id = jel.journal_entry_id
               WHERE je.tenant_id = %s AND je.deleted_at IS NULL AND a.is_leaf = false
               LIMIT 20""",
            (tenant_id,),
        )
        bad = db_read.fetchall()
        assert not bad, f"Guruh schyotlariga provodkalar: {[b['code'] for b in bad]}"

    def test_no_posted_entries_without_lines(self, db_read, tenant_id):
        db_read.execute(
            """SELECT je.id, je.entry_number
               FROM journal_entries je
               LEFT JOIN journal_entry_lines jel ON jel.journal_entry_id = je.id
               WHERE je.tenant_id = %s AND je.status = 'posted' AND je.deleted_at IS NULL
               GROUP BY je.id, je.entry_number
               HAVING COUNT(jel.id) < 2
               LIMIT 20""",
            (tenant_id,),
        )
        bad = db_read.fetchall()
        assert not bad, f"Qatorlari 2 tadan kam posted JE lar: {[b['entry_number'] for b in bad]}"

    def test_header_totals_match_lines(self, db_read, tenant_id):
        db_read.execute(
            """SELECT je.entry_number, je.total_debit, SUM(jel.debit_amount) AS line_dt
               FROM journal_entries je
               JOIN journal_entry_lines jel ON jel.journal_entry_id = je.id
               WHERE je.tenant_id = %s AND je.deleted_at IS NULL
               GROUP BY je.id, je.entry_number, je.total_debit
               HAVING ABS(COALESCE(je.total_debit, 0) - SUM(jel.debit_amount)) > 0.01
               LIMIT 20""",
            (tenant_id,),
        )
        bad = db_read.fetchall()
        assert not bad, (
            f"Header/qator summalari mos emas: "
            f"{[(b['entry_number'], float(b['total_debit']), float(b['line_dt'])) for b in bad]}"
        )
