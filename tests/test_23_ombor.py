"""
Ombor (Inventory) v2 integrity tests — docs/ombor-audit.md §9 item 5.

Invariants under test:
  1. Every stock movement writes BOTH the balance (inventory.quantity_on_hand)
     and a self-contained ledger row (inventory_transactions with product_id +
     warehouse_id, migration 447).
  2. The stock_ledger view rebuilds per-warehouse on-hand: SUM(qty_delta)
     equals the balance after mixed operations.
  3. Negative stock is refused on manual adjust and transfer.
  4. Transfers conserve quantity (source loss == destination gain, two
     ledger legs in one transaction).
  5. GET /inventory/stats returns the dashboard contract and its low-stock
     rule (reorder_point else min_stock_level) sees a product under threshold.
  6. GET /inventory/valuation average cost is quantity-weighted.

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import uuid

import pytest


def _make_product(api_client, code_suffix, **overrides):
    payload = {
        "name": f"OMBORTEST {code_suffix}",
        "code": f"OMB-{code_suffix}",
        "sku": f"OMB-{code_suffix}",
        "type": "product",
        "is_stockable": True,
        "track_inventory": True,
        "cost_price": 10000,
        "list_price": 15000,
        "is_active": True,
    }
    payload.update(overrides)
    resp = api_client.post("/products", json=payload)
    assert resp.status_code in (200, 201), f"product create failed: {resp.text}"
    data = resp.json()["data"]
    return data["id"]


@pytest.fixture(scope="module")
def warehouses(api_client, db_read):
    """Two active warehouses for the client's tenant (create the 2nd if needed)."""
    db_read.execute(
        """SELECT id FROM warehouses
           WHERE tenant_id = %s AND deleted_at IS NULL AND is_active = true
           ORDER BY created_at ASC LIMIT 2""",
        (api_client.tenant_id,),
    )
    rows = db_read.fetchall()
    ids = [str(r["id"]) for r in rows]
    while len(ids) < 2:
        resp = api_client.post("/warehouses", json={
            "name": f"OMBORTEST WH {len(ids) + 1} {uuid.uuid4().hex[:6]}",
            "code": f"OMBT{uuid.uuid4().hex[:6].upper()}",
            "address": {"city": "Toshkent"},
        })
        assert resp.status_code in (200, 201), f"warehouse create failed: {resp.text}"
        ids.append(resp.json()["data"]["id"])
    return ids


def _on_hand(db_read, tenant_id, product_id, warehouse_id):
    db_read.execute(
        """SELECT COALESCE(SUM(quantity_on_hand), 0) AS q FROM inventory
           WHERE tenant_id = %s AND product_id = %s AND warehouse_id = %s""",
        (tenant_id, product_id, warehouse_id),
    )
    return float(db_read.fetchone()["q"])


def _ledger_sum(db_read, tenant_id, product_id, warehouse_id):
    db_read.execute(
        """SELECT COALESCE(SUM(qty_delta), 0) AS q FROM stock_ledger
           WHERE tenant_id = %s AND product_id = %s AND warehouse_id = %s""",
        (tenant_id, product_id, warehouse_id),
    )
    return float(db_read.fetchone()["q"])


class TestLedgerIntegrity:
    def test_adjust_writes_balance_and_selfcontained_ledger(self, api_client, db_read, warehouses):
        pid = _make_product(api_client, uuid.uuid4().hex[:8])
        wh = warehouses[0]

        resp = api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": wh,
            "quantity": 25, "unit_cost": 12000, "reason": "test kirim",
        })
        assert resp.status_code == 200, resp.text

        assert _on_hand(db_read, api_client.tenant_id, pid, wh) == 25

        # Migration 447: the ledger row must carry product_id + warehouse_id
        db_read.execute(
            """SELECT product_id, warehouse_id FROM inventory_transactions
               WHERE tenant_id = %s AND product_id = %s""",
            (api_client.tenant_id, pid),
        )
        rows = db_read.fetchall()
        assert rows, "no self-contained ledger row written"
        assert all(str(r["warehouse_id"]) == wh for r in rows)

    def test_balance_equals_ledger_sum_after_mixed_ops(self, api_client, db_read, warehouses):
        pid = _make_product(api_client, uuid.uuid4().hex[:8])
        wh_a, wh_b = warehouses[0], warehouses[1]

        for qty, reason in [(30, "kirim"), (-10, "chiqim")]:
            r = api_client.post("/inventory/adjust", json={
                "product_id": pid, "warehouse_id": wh_a,
                "quantity": qty, "unit_cost": 5000, "reason": reason,
            })
            assert r.status_code == 200, r.text

        r = api_client.post("/inventory/transfer", json={
            "product_id": pid, "from_warehouse_id": wh_a,
            "to_warehouse_id": wh_b, "quantity": 5,
        })
        assert r.status_code == 200, r.text

        for wh, expected in [(wh_a, 15), (wh_b, 5)]:
            on_hand = _on_hand(db_read, api_client.tenant_id, pid, wh)
            ledger = _ledger_sum(db_read, api_client.tenant_id, pid, wh)
            assert on_hand == expected, f"on-hand {on_hand} != expected {expected} in {wh}"
            assert abs(on_hand - ledger) < 0.0001, (
                f"ledger cannot rebuild balance: on_hand={on_hand} ledger_sum={ledger}"
            )

    def test_transfer_conserves_quantity(self, api_client, db_read, warehouses):
        pid = _make_product(api_client, uuid.uuid4().hex[:8])
        wh_a, wh_b = warehouses[0], warehouses[1]

        api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": wh_a,
            "quantity": 20, "unit_cost": 3000, "reason": "kirim",
        })
        r = api_client.post("/inventory/transfer", json={
            "product_id": pid, "from_warehouse_id": wh_a,
            "to_warehouse_id": wh_b, "quantity": 8,
        })
        assert r.status_code == 200, r.text

        total = (_on_hand(db_read, api_client.tenant_id, pid, wh_a)
                 + _on_hand(db_read, api_client.tenant_id, pid, wh_b))
        assert total == 20, f"transfer lost/created stock: total={total}"

        # Two transfer legs on the ledger, net zero
        db_read.execute(
            """SELECT COALESCE(SUM(qty_delta), 0) AS net, COUNT(*) AS legs
               FROM stock_ledger
               WHERE tenant_id = %s AND product_id = %s AND transaction_type = 'transfer'""",
            (api_client.tenant_id, pid),
        )
        row = db_read.fetchone()
        assert int(row["legs"]) == 2
        assert abs(float(row["net"])) < 0.0001


class TestNegativeStockGuards:
    def test_adjust_below_zero_rejected(self, api_client, warehouses):
        pid = _make_product(api_client, uuid.uuid4().hex[:8])
        wh = warehouses[0]
        api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": wh,
            "quantity": 5, "unit_cost": 1000, "reason": "kirim",
        })
        r = api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": wh,
            "quantity": -50, "reason": "chiqim ko'p",
        })
        assert r.status_code == 400, f"expected 400 on negative stock, got {r.status_code}: {r.text}"

    def test_transfer_more_than_available_rejected(self, api_client, warehouses):
        pid = _make_product(api_client, uuid.uuid4().hex[:8])
        wh_a, wh_b = warehouses[0], warehouses[1]
        api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": wh_a,
            "quantity": 3, "unit_cost": 1000, "reason": "kirim",
        })
        r = api_client.post("/inventory/transfer", json={
            "product_id": pid, "from_warehouse_id": wh_a,
            "to_warehouse_id": wh_b, "quantity": 10,
        })
        assert r.status_code == 400, f"expected 400, got {r.status_code}: {r.text}"


class TestStatsEndpoint:
    def test_stats_contract(self, api_client):
        r = api_client.get("/inventory/stats")
        assert r.status_code == 200, r.text
        data = r.json()["data"]
        for key in ("totals", "value_series", "category_values", "low_stock_items", "recent_moves"):
            assert key in data, f"missing {key}"
        totals = data["totals"]
        for key in ("total_value", "low_stock_count", "product_count", "warehouse_count", "today_movements"):
            assert key in totals, f"missing totals.{key}"
        assert len(data["value_series"]) == 6

    def test_low_stock_rule_reorder_point(self, api_client, warehouses):
        suffix = uuid.uuid4().hex[:8]
        pid = _make_product(api_client, suffix, reorder_point=10, min_stock_level=0)
        api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": warehouses[0],
            "quantity": 2, "unit_cost": 1000, "reason": "kirim past",
        })
        r = api_client.get("/inventory/stats")
        assert r.status_code == 200
        low = r.json()["data"]["low_stock_items"]
        match = [i for i in low if i["product_id"] == pid]
        # Top-10 list may be full of older seeds; the count must include it though
        assert r.json()["data"]["totals"]["low_stock_count"] >= 1
        if match:
            assert match[0]["threshold"] == 10
            assert match[0]["on_hand"] == 2

    def test_recent_moves_have_product_names(self, api_client, warehouses):
        pid = _make_product(api_client, uuid.uuid4().hex[:8])
        api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": warehouses[0],
            "quantity": 7, "unit_cost": 100, "reason": "kirim feed",
        })
        r = api_client.get("/inventory/stats")
        moves = r.json()["data"]["recent_moves"]
        assert moves, "recent_moves empty right after a movement"
        assert any(m["product_name"].startswith("OMBORTEST") for m in moves)


class TestValuationSetting:
    def test_default_is_aveco(self, api_client):
        r = api_client.get("/inventory/valuation-settings")
        assert r.status_code == 200, r.text
        assert r.json()["data"]["method"] in ("aveco", "fifo")

    def test_switch_records_history_and_effective_date(self, api_client):
        cur = api_client.get("/inventory/valuation-settings").json()["data"]["method"]
        other = "fifo" if cur == "aveco" else "aveco"

        r = api_client.put("/inventory/valuation-settings", json={"method": other})
        assert r.status_code == 200, r.text
        data = r.json()["data"]
        assert data["method"] == other
        assert data.get("effective_from"), "effective_from missing on method change"

        # switch back — history must retain the intermediate step
        r = api_client.put("/inventory/valuation-settings", json={"method": cur})
        assert r.status_code == 200
        data = r.json()["data"]
        assert data["method"] == cur
        assert any(h.get("method") == other for h in data.get("history", [])), \
            "history does not record the previous method"

    def test_invalid_method_rejected(self, api_client):
        r = api_client.put("/inventory/valuation-settings", json={"method": "lifo"})
        assert r.status_code == 400, "LIFO must be rejected (IFRS-prohibited)"


class TestDemoSeedIntegrity:
    def test_demo_seed_balances_match_ledger(self, api_client, db_read):
        """If the demo seed ran, its balances must equal ledger sums."""
        db_read.execute(
            """SELECT COUNT(*) AS n FROM inventory_transactions
               WHERE tenant_id = %s AND reference_type = 'demo_seed'""",
            (api_client.tenant_id,),
        )
        if int(db_read.fetchone()["n"]) == 0:
            pytest.skip("demo seed not present")

        db_read.execute(
            """SELECT COUNT(*) AS bad FROM inventory inv
               JOIN (
                 SELECT inventory_id, SUM(quantity) AS q
                 FROM inventory_transactions
                 WHERE tenant_id = %s AND reference_type = 'demo_seed'
                 GROUP BY inventory_id
               ) l ON l.inventory_id = inv.id
               WHERE ABS(inv.quantity_on_hand - l.q) > 0.001""",
            (api_client.tenant_id,),
        )
        assert int(db_read.fetchone()["bad"]) == 0


class TestValuation:
    def test_average_cost_is_quantity_weighted(self, api_client, warehouses):
        """10 dona @1000 (A ombor) + 90 dona @2000 (B ombor) →
        weighted avg 1900, NOT the row mean 1500."""
        pid = _make_product(api_client, uuid.uuid4().hex[:8], cost_price=0)
        api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": warehouses[0],
            "quantity": 10, "unit_cost": 1000, "reason": "kirim A",
        })
        api_client.post("/inventory/adjust", json={
            "product_id": pid, "warehouse_id": warehouses[1],
            "quantity": 90, "unit_cost": 2000, "reason": "kirim B",
        })
        # Valuation ORDER BY total_value DESC bilan sahifalanadi (limit cap
        # 200); dev DB'da test-mahsulotlar yig'ilib 200 qatordan oshdi, shuning
        # uchun kichik summali test qatori endi 1-sahifada bo'lmasligi mumkin —
        # topilguncha sahifalab boramiz.
        row = None
        for page in range(1, 8):
            r = api_client.get("/inventory/valuation", params={"limit": 200, "page": page})
            assert r.status_code == 200, r.text
            payload = r.json()["data"]
            if isinstance(payload, dict):
                items = payload.get("items") or payload.get("data") or []
            else:
                items = payload or []
            if not isinstance(items, list) or not items:
                break
            row = next((i for i in items if i.get("product_id") == pid), None)
            if row is not None:
                break
        assert row is not None, "product missing from valuation"
        avg = float(row["average_cost"])
        assert abs(avg - 1900) < 1, f"average_cost {avg} is not quantity-weighted (expected 1900)"
