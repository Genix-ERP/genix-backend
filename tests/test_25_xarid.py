"""
Xarid (Purchasing) v2 integrity tests — docs/xarid-audit.md §11.

Invariants under test:
  1. Approval is NOT physical arrival: POST /:id/approve leaves the PO
     'approved' with quantity_received = 0 and moves no stock (finding #2).
  2. Partial receipt math: /receive twice derives partial → received from
     line sums, and the stock balance/ledger grows by exactly the received
     quantity (applyStockDelta path).
  3. Over-receipt is refused atomically (cap inside the UPDATE's WHERE).
  4. Cancel: POST /:id/cancel is guarded — allowed from draft, refused
     after stock has been received; PUT {status: cancelled} is rejected.
  5. Double-post guard: a bill created from a PO already carries a journal
     entry; POST /purchase-invoices/:id/post must refuse (ALREADY_POSTED)
     instead of booking 6010 AP a second time (finding #1, migration 422).
  6. GET /purchase-orders/stats returns the dashboard contract and its
     figures see a created PO.
  7. Status CHECK constraint (migration 448) refuses unknown statuses.

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import uuid

import pytest


def _make_product(api_client, code_suffix, **overrides):
    payload = {
        "name": f"XARIDTEST {code_suffix}",
        "code": f"XRD-{code_suffix}",
        "sku": f"XRD-{code_suffix}",
        "type": "product",
        "is_stockable": True,
        "track_inventory": True,
        "cost_price": 20000,
        "list_price": 30000,
        "is_active": True,
    }
    payload.update(overrides)
    resp = api_client.post("/products", json=payload)
    assert resp.status_code in (200, 201), f"product create failed: {resp.text}"
    return resp.json()["data"]["id"]


@pytest.fixture(scope="module")
def warehouse_id(api_client, db_read):
    db_read.execute(
        """SELECT id FROM warehouses
           WHERE tenant_id = %s AND deleted_at IS NULL AND is_active = true
           ORDER BY created_at ASC LIMIT 1""",
        (api_client.tenant_id,),
    )
    row = db_read.fetchone()
    if not row:
        pytest.skip("No warehouse in dev DB")
    return str(row["id"])


def _create_po(api_client, vendor_id, warehouse_id, product_id, qty=10, price=25000):
    resp = api_client.post("/purchase-orders", json={
        "vendor_id": vendor_id,
        "warehouse_id": warehouse_id,
        "payment_terms": "net_30",
        "lines": [{
            "product_id": product_id,
            "description": "Xarid test line",
            "quantity": qty,
            "unit_price": price,
        }],
    })
    assert resp.status_code in (200, 201), f"PO create failed: {resp.text}"
    return resp.json()["data"]


def _po_row(db_read, po_id):
    db_read.execute(
        "SELECT status FROM purchase_orders WHERE id = %s", (po_id,)
    )
    return db_read.fetchone()


def _line_rows(db_read, po_id):
    db_read.execute(
        """SELECT id, quantity, COALESCE(quantity_received, 0) AS quantity_received
           FROM purchase_order_lines WHERE purchase_order_id = %s""",
        (po_id,),
    )
    return db_read.fetchall()


def _on_hand(db_read, tenant_id, product_id):
    db_read.execute(
        """SELECT COALESCE(SUM(quantity_on_hand), 0) AS q
           FROM inventory WHERE tenant_id = %s AND product_id = %s""",
        (tenant_id, product_id),
    )
    return float(db_read.fetchone()["q"])


class TestApproveIsNotReceipt:
    def test_approve_does_not_move_stock(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, test_supplier["id"], warehouse_id, product_id, qty=7)

        before = _on_hand(db_read, api_client.tenant_id, product_id)
        resp = api_client.post(f"/purchase-orders/{po['id']}/approve")
        assert resp.status_code == 200, resp.text

        row = _po_row(db_read, po["id"])
        assert row["status"] == "approved", "approval must not force 'received'"

        after = _on_hand(db_read, api_client.tenant_id, product_id)
        assert after == before, "approval must not move stock"

        for line in _line_rows(db_read, po["id"]):
            assert float(line["quantity_received"]) == 0


class TestPartialReceipt:
    def test_two_receipts_derive_status_and_stock(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, test_supplier["id"], warehouse_id, product_id, qty=10)
        api_client.post(f"/purchase-orders/{po['id']}/approve")

        lines = _line_rows(db_read, po["id"])
        line_id = str(lines[0]["id"])
        before = _on_hand(db_read, api_client.tenant_id, product_id)

        r1 = api_client.post(f"/purchase-orders/{po['id']}/receive", json={
            "lines": [{"line_id": line_id, "quantity_received": 4}],
        })
        assert r1.status_code == 200, r1.text
        assert _po_row(db_read, po["id"])["status"] == "partial"

        r2 = api_client.post(f"/purchase-orders/{po['id']}/receive", json={
            "lines": [{"line_id": line_id, "quantity_received": 6}],
        })
        assert r2.status_code == 200, r2.text
        assert _po_row(db_read, po["id"])["status"] == "received"

        after = _on_hand(db_read, api_client.tenant_id, product_id)
        assert after - before == 10, f"stock must grow by received qty, grew {after - before}"

        # Ledger rows exist and are self-contained (447 convention)
        db_read.execute(
            """SELECT COALESCE(SUM(quantity), 0) AS q FROM inventory_transactions
               WHERE tenant_id = %s AND product_id = %s
                 AND reference_type = 'purchase_order' AND reference_id = %s""",
            (api_client.tenant_id, product_id, str(po["id"])),
        )
        assert float(db_read.fetchone()["q"]) == 10

    def test_over_receipt_refused(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, test_supplier["id"], warehouse_id, product_id, qty=5)
        api_client.post(f"/purchase-orders/{po['id']}/approve")
        line_id = str(_line_rows(db_read, po["id"])[0]["id"])

        resp = api_client.post(f"/purchase-orders/{po['id']}/receive", json={
            "lines": [{"line_id": line_id, "quantity_received": 6}],
        })
        assert resp.status_code == 400, f"over-receipt must 400, got {resp.status_code}: {resp.text}"
        assert float(_line_rows(db_read, po["id"])[0]["quantity_received"]) == 0


class TestCancel:
    def test_cancel_draft_ok_and_put_blocked(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, test_supplier["id"], warehouse_id, product_id)

        put = api_client.put(f"/purchase-orders/{po['id']}", json={"status": "cancelled"})
        assert put.status_code == 400, "PUT status=cancelled must route to /cancel"

        resp = api_client.post(f"/purchase-orders/{po['id']}/cancel")
        assert resp.status_code == 200, resp.text
        assert _po_row(db_read, po["id"])["status"] == "cancelled"

    def test_cancel_refused_after_receipt(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, test_supplier["id"], warehouse_id, product_id, qty=3)
        api_client.post(f"/purchase-orders/{po['id']}/approve")
        line_id = str(_line_rows(db_read, po["id"])[0]["id"])
        api_client.post(f"/purchase-orders/{po['id']}/receive", json={
            "lines": [{"line_id": line_id, "quantity_received": 3}],
        })

        resp = api_client.post(f"/purchase-orders/{po['id']}/cancel")
        assert resp.status_code == 400, "cancel after receipt must be refused"


class TestBillDoublePostGuard:
    def test_post_refused_when_je_attached(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, test_supplier["id"], warehouse_id, product_id, qty=2, price=50000)
        api_client.post(f"/purchase-orders/{po['id']}/approve")

        bill_resp = api_client.post(f"/purchase-orders/{po['id']}/bill")
        assert bill_resp.status_code in (200, 201), bill_resp.text
        bill = bill_resp.json()["data"]
        bill_id = bill.get("id") or bill.get("invoice_id")
        assert bill_id, f"no bill id in response: {bill}"

        db_read.execute(
            "SELECT journal_entry_id FROM purchase_invoices WHERE id = %s", (bill_id,)
        )
        je_id = db_read.fetchone()["journal_entry_id"]
        if je_id is None:
            pytest.skip("CreateBillFromPO did not post a JE in this environment")

        resp = api_client.post(f"/purchase-invoices/{bill_id}/post")
        assert resp.status_code == 400, (
            f"posting an already-posted bill must 400 (double AP!), got {resp.status_code}: {resp.text}"
        )

        # Still exactly one JE for this bill
        db_read.execute(
            """SELECT COUNT(*) AS n FROM journal_entries
               WHERE source_type = 'purchase_invoice' AND source_id = %s
                 AND status != 'reversed'""",
            (str(bill_id),),
        )
        assert db_read.fetchone()["n"] <= 1


class TestStatsEndpoint:
    def test_contract_and_visibility(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _create_po(api_client, test_supplier["id"], warehouse_id, product_id, qty=1, price=99000)

        resp = api_client.get("/purchase-orders/stats")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]

        for key in ("totals", "monthly_series", "top_suppliers", "recent_orders", "upcoming_deliveries"):
            assert key in data, f"missing {key}"
        totals = data["totals"]
        for key in ("orders_count", "orders_sum", "pending_count", "pending_sum", "overdue_count", "payable_total"):
            assert key in totals, f"missing totals.{key}"

        assert totals["orders_count"] >= 1
        assert len(data["monthly_series"]) == 6
        assert any(r["order_number"] for r in data["recent_orders"])


class TestSupplierKPIs:
    def test_contract_and_spend_visibility(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _create_po(api_client, test_supplier["id"], warehouse_id, product_id, qty=2, price=70000)

        resp = api_client.get("/purchase-orders/supplier-kpis")
        assert resp.status_code == 200, resp.text
        rows = resp.json()["data"]
        assert isinstance(rows, list) and rows

        mine = next((r for r in rows if r["vendor_id"] == test_supplier["id"]), None)
        assert mine is not None, "test supplier missing from KPI list"
        for key in ("total_spend", "orders_count", "open_pos", "open_amount",
                    "ap_balance", "avg_delivery_days", "on_time_rate", "returns_count"):
            assert key in mine, f"missing {key}"
        assert mine["total_spend"] >= 140000


class TestDirectorSummaryPurchases:
    def test_purchase_fields_present(self, api_client):
        resp = api_client.get("/reports/director-summary")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        companies = data.get("companies") or data.get("organizations") or data
        if isinstance(companies, dict):
            companies = list(companies.values())
        assert isinstance(companies, list) and companies, f"unexpected shape: {type(companies)}"
        row = companies[0]
        for key in ("purchases_period_total", "purchases_period_count",
                    "purchase_ap_total", "purchase_overdue_deliveries"):
            assert key in row, f"missing {key} in director summary"


class TestObjectCost:
    def test_receipt_writes_construction_expense_line(self, api_client, db_read, test_supplier, warehouse_id):
        import psycopg2.extras

        # A minimal construction project via SQL (BIGSERIAL PK; the projects
        # API needs a longer setup than this invariant deserves). Status must
        # come from the 461 vocabulary — legacy 'active' now violates
        # chk_construction_project_status.
        db_read.execute(
            """INSERT INTO construction_projects (tenant_id, code, name, status)
               VALUES (%s, %s, 'XARIDTEST obyekt', 'in_progress')
               RETURNING id""",
            (api_client.tenant_id, f"XRD-OBJ-{uuid.uuid4().hex[:6].upper()}"),
        )
        project_id = db_read.fetchone()["id"]

        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        resp = api_client.post("/purchase-orders", json={
            "vendor_id": test_supplier["id"],
            "warehouse_id": warehouse_id,
            "construction_project_id": project_id,
            "lines": [{
                "product_id": product_id,
                "description": "Obyekt uchun material",
                "quantity": 4,
                "unit_price": 30000,
            }],
        })
        assert resp.status_code in (200, 201), resp.text
        po = resp.json()["data"]

        api_client.post(f"/purchase-orders/{po['id']}/approve")
        line_id = str(_line_rows(db_read, po["id"])[0]["id"])
        r = api_client.post(f"/purchase-orders/{po['id']}/receive", json={
            "lines": [{"line_id": line_id, "quantity_received": 4}],
        })
        assert r.status_code == 200, r.text

        db_read.execute(
            """SELECT COALESCE(SUM(amount), 0) AS total, COUNT(*) AS n
               FROM construction_expense_lines
               WHERE tenant_id = %s AND project_id = %s AND product_id = %s
                 AND deleted_at IS NULL""",
            (api_client.tenant_id, project_id, product_id),
        )
        row = db_read.fetchone()
        assert row["n"] == 1, "receipt must write exactly one object-cost row"
        assert float(row["total"]) == 4 * 30000

    def test_unlinked_po_writes_no_object_cost(self, api_client, db_read, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, test_supplier["id"], warehouse_id, product_id, qty=2)
        api_client.post(f"/purchase-orders/{po['id']}/approve")
        line_id = str(_line_rows(db_read, po["id"])[0]["id"])
        api_client.post(f"/purchase-orders/{po['id']}/receive", json={
            "lines": [{"line_id": line_id, "quantity_received": 2}],
        })

        db_read.execute(
            """SELECT COUNT(*) AS n FROM construction_expense_lines
               WHERE tenant_id = %s AND product_id = %s""",
            (api_client.tenant_id, product_id),
        )
        assert db_read.fetchone()["n"] == 0


class TestStatusConstraint:
    def test_unknown_status_refused_by_db(self, db_read, api_client, test_supplier, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, test_supplier["id"], warehouse_id, product_id)

        import psycopg2

        conn = psycopg2.connect(
            host=db_read.connection.info.host,
            port=db_read.connection.info.port,
            user=db_read.connection.info.user,
            password=db_read.connection.info.password,
            dbname=db_read.connection.info.dbname,
        )
        try:
            with conn.cursor() as cur:
                with pytest.raises(psycopg2.errors.CheckViolation):
                    cur.execute(
                        "UPDATE purchase_orders SET status = 'bogus' WHERE id = %s",
                        (po["id"],),
                    )
        finally:
            conn.rollback()
            conn.close()
