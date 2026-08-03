"""
Savdo (Sales) v2 integrity tests — docs/savdo-audit.md §8.

Invariants under test:
  1. Free-form order status writes are refused: PUT may only toggle
     draft ↔ quotation; payment_status is derived and read-only.
  2. Confirm creates ONE shipment document (sales_delivery_orders) and no
     parallel stock_operations chain; confirm moves no stock.
  3. Delivery validation ships exactly once: stock/ledger drop by the shipped
     quantity, re-validate refuses (atomic claim), and a second pre-created DO
     cannot over-deliver the same order line (clamp inside the stock tx).
  4. Invoice-from-order posts ONE balanced journal entry (Σdebit == Σcredit
     even with an order-level discount + shipping) and maintains
     quantity_invoiced; a posted invoice cannot be reset or re-sent
     (double-AR guard) and cannot be faked to 'paid' via PUT.
  5. RecordPayment writes the full artifact set atomically: journal entry +
     payments row + allocation + invoice amount_paid + order paid_amount
     (aggregate); overpayment refused.
  6. GET /sales-orders/stats returns the dashboard contract and sees the data.
  7. Quotation numbering comes from sales_quotations (not the abandoned
     `quotations` table): two quotes get distinct numbers; conversion produces
     an org-scoped S-series order exactly once.
  8. Migration 451 status CHECK constraints refuse unknown statuses.

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import uuid

import pytest


def _make_product(api_client, code_suffix, **overrides):
    payload = {
        "name": f"SAVDOTEST {code_suffix}",
        "code": f"SVD-{code_suffix}",
        "sku": f"SVD-{code_suffix}",
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


def _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=20):
    """Stock via the Xarid receive path (the applyStockDelta-covered route)."""
    resp = api_client.post("/purchase-orders", json={
        "vendor_id": test_supplier["id"],
        "warehouse_id": warehouse_id,
        "payment_terms": "net_30",
        "lines": [{"product_id": product_id, "description": "stock-in for savdo test",
                   "quantity": qty, "unit_price": 20000}],
    })
    assert resp.status_code in (200, 201), resp.text
    po = resp.json()["data"]
    resp = api_client.post(f"/purchase-orders/{po['id']}/approve")
    assert resp.status_code == 200, resp.text
    db_read.execute(
        "SELECT id FROM purchase_order_lines WHERE purchase_order_id = %s", (po["id"],))
    line_id = str(db_read.fetchone()["id"])
    resp = api_client.post(f"/purchase-orders/{po['id']}/receive", json={
        "lines": [{"line_id": line_id, "quantity_received": qty}],
    })
    assert resp.status_code == 200, f"receive failed: {resp.text}"


def _create_order(api_client, customer_id, warehouse_id, product_id, qty=10,
                  price=30000, **extra):
    payload = {
        "customer_id": customer_id,
        "order_date": "2026-08-03",
        "warehouse_id": warehouse_id,
        "lines": [{"product_id": product_id, "description": "Savdo test line",
                   "quantity": qty, "unit_price": price}],
    }
    payload.update(extra)
    resp = api_client.post("/sales-orders", json=payload)
    assert resp.status_code in (200, 201), f"SO create failed: {resp.text}"
    return resp.json()["data"]


def _order_row(db_read, order_id):
    db_read.execute(
        "SELECT status, payment_status, paid_amount, organization_id, order_number, total_amount "
        "FROM sales_orders WHERE id = %s", (order_id,))
    return db_read.fetchone()


def _order_line(db_read, order_id):
    db_read.execute(
        """SELECT id, quantity, COALESCE(quantity_delivered, 0) AS qd,
                  COALESCE(quantity_invoiced, 0) AS qi
           FROM sales_order_lines WHERE sales_order_id = %s""", (order_id,))
    return db_read.fetchone()


def _do_rows(db_read, order_id):
    db_read.execute(
        "SELECT id, status, delivery_number FROM sales_delivery_orders "
        "WHERE sales_order_id = %s ORDER BY created_at", (order_id,))
    return db_read.fetchall()


def _on_hand(db_read, tenant_id, product_id):
    db_read.execute(
        """SELECT COALESCE(SUM(quantity_on_hand), 0) AS q
           FROM inventory WHERE tenant_id = %s AND product_id = %s""",
        (tenant_id, product_id))
    return float(db_read.fetchone()["q"])


class TestOrderStatusGuard:
    def test_free_form_status_and_payment_status_refused(
            self, api_client, db_read, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=3)

        # draft -> quotation and back is the only legal manual toggle
        resp = api_client.put(f"/sales-orders/{so['id']}", json={"status": "quotation"})
        assert resp.status_code == 200, resp.text
        assert _order_row(db_read, so["id"])["status"] == "quotation"
        resp = api_client.put(f"/sales-orders/{so['id']}", json={"status": "draft"})
        assert resp.status_code == 200, resp.text

        for bogus in ("shipped", "delivered", "confirmed", "cancelled"):
            resp = api_client.put(f"/sales-orders/{so['id']}", json={"status": bogus})
            assert resp.status_code == 400, f"PUT status={bogus} must be refused: {resp.text}"

        resp = api_client.put(f"/sales-orders/{so['id']}", json={"payment_status": "paid"})
        assert resp.status_code == 400, "manual payment_status must be refused"
        assert _order_row(db_read, so["id"])["payment_status"] == "unpaid"


class TestConfirmSingleShipmentDoc:
    def test_confirm_creates_do_but_no_stock_op_chain_and_moves_nothing(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=20)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=10)

        before = _on_hand(db_read, api_client.tenant_id, product_id)
        resp = api_client.post(f"/sales-orders/{so['id']}/confirm")
        assert resp.status_code == 200, resp.text
        assert _order_row(db_read, so["id"])["status"] == "confirmed"
        assert _on_hand(db_read, api_client.tenant_id, product_id) == before, \
            "confirm must not move stock"

        dos = _do_rows(db_read, so["id"])
        assert len(dos) == 1 and dos[0]["status"] == "draft"

        db_read.execute(
            """SELECT COUNT(*) AS n FROM stock_operations
               WHERE source_type = 'sales_order' AND source_id = %s AND deleted_at IS NULL""",
            (so["id"],))
        assert db_read.fetchone()["n"] == 0, \
            "confirm must not create the parallel stock_operations chain"


class TestDeliveryShipsExactlyOnce:
    def test_validate_moves_stock_once_and_revalidate_refused(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=20)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=10)
        api_client.post(f"/sales-orders/{so['id']}/confirm")
        do_id = str(_do_rows(db_read, so["id"])[0]["id"])

        before = _on_hand(db_read, api_client.tenant_id, product_id)
        resp = api_client.post(f"/sales/delivery-orders/{do_id}/validate")
        assert resp.status_code == 200, f"validate failed: {resp.text}"

        after = _on_hand(db_read, api_client.tenant_id, product_id)
        assert after == before - 10, f"expected -10, got {after - before}"

        line = _order_line(db_read, so["id"])
        assert float(line["qd"]) == 10, "quantity_delivered must equal shipped qty"

        db_read.execute(
            """SELECT COUNT(*) AS n FROM inventory_transactions
               WHERE tenant_id = %s AND reference_type = 'sales_delivery' AND reference_id = %s""",
            (api_client.tenant_id, do_id))
        assert db_read.fetchone()["n"] >= 1, "ledger row with reference_type=sales_delivery required"

        # Second validate: atomic claim must refuse, stock must not move again
        resp = api_client.post(f"/sales/delivery-orders/{do_id}/validate")
        assert resp.status_code == 400, f"re-validate must refuse: {resp.text}"
        assert _on_hand(db_read, api_client.tenant_id, product_id) == after

    def test_second_do_cannot_over_deliver(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=20)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=5)
        api_client.post(f"/sales-orders/{so['id']}/confirm")

        # Second DO created BEFORE anything ships → it also carries the full 5
        resp = api_client.post("/sales/delivery-orders", json={"sales_order_id": so["id"]})
        assert resp.status_code in (200, 201), resp.text

        dos = _do_rows(db_read, so["id"])
        assert len(dos) == 2

        before = _on_hand(db_read, api_client.tenant_id, product_id)
        resp = api_client.post(f"/sales/delivery-orders/{dos[0]['id']}/validate")
        assert resp.status_code == 200, resp.text

        # Stock is still sufficient (20-5=15), so the ONLY thing stopping DO2 is the clamp
        resp = api_client.post(f"/sales/delivery-orders/{dos[1]['id']}/validate")
        assert resp.status_code == 400, f"over-delivery must be refused: {resp.text}"
        assert _on_hand(db_read, api_client.tenant_id, product_id) == before - 5, \
            "refused DO must move nothing (tx rollback)"
        assert float(_order_line(db_read, so["id"])["qd"]) == 5


class TestInvoiceJournalIntegrity:
    def _je_sums(self, db_read, je_id):
        db_read.execute(
            """SELECT COALESCE(SUM(debit_amount), 0) AS d, COALESCE(SUM(credit_amount), 0) AS c
               FROM journal_entry_lines WHERE journal_entry_id = %s""", (je_id,))
        row = db_read.fetchone()
        return float(row["d"]), float(row["c"])

    def test_invoice_from_order_balanced_with_discount_and_shipping(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=10)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id,
                           qty=10, price=30000,
                           discount_type="fixed", discount_value=30000,
                           shipping_amount=20000)
        api_client.post(f"/sales-orders/{so['id']}/confirm")

        resp = api_client.post(f"/sales-orders/{so['id']}/invoice")
        assert resp.status_code in (200, 201), f"invoice-from-order failed: {resp.text}"

        db_read.execute(
            "SELECT id, journal_entry_id, status, total_amount FROM sales_invoices "
            "WHERE sales_order_id = %s AND deleted_at IS NULL", (so["id"],))
        inv = db_read.fetchone()
        assert inv is not None and inv["journal_entry_id"] is not None, \
            "invoice must carry its journal entry"
        assert inv["status"] == "sent"

        d, c = self._je_sums(db_read, inv["journal_entry_id"])
        assert abs(d - c) < 0.01, f"journal entry unbalanced: debit={d} credit={c}"
        assert d > 0

        assert float(_order_line(db_read, so["id"])["qi"]) == 10, \
            "quantity_invoiced must be maintained"

    def test_posted_invoice_cannot_be_reset_resent_or_faked_paid(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=10)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=4)
        api_client.post(f"/sales-orders/{so['id']}/confirm")
        api_client.post(f"/sales-orders/{so['id']}/invoice")

        db_read.execute(
            "SELECT id, journal_entry_id FROM sales_invoices WHERE sales_order_id = %s",
            (so["id"],))
        inv = db_read.fetchone()
        inv_id = str(inv["id"])
        first_je = inv["journal_entry_id"]

        resp = api_client.put(f"/sales-invoices/{inv_id}", json={"status": "draft"})
        assert resp.status_code == 400, "sent -> draft must be refused"
        resp = api_client.put(f"/sales-invoices/{inv_id}", json={"status": "paid"})
        assert resp.status_code == 400, "PUT status=paid must be refused"
        resp = api_client.post(f"/sales-invoices/{inv_id}/send")
        assert resp.status_code == 400, "re-send of a posted invoice must be refused"

        db_read.execute(
            "SELECT journal_entry_id FROM sales_invoices WHERE id = %s", (inv_id,))
        assert db_read.fetchone()["journal_entry_id"] == first_je, "JE must be unchanged"
        db_read.execute(
            """SELECT COUNT(*) AS n FROM journal_entries
               WHERE source_type = 'sales_invoice' AND source_id = %s AND deleted_at IS NULL""",
            (inv_id,))
        assert db_read.fetchone()["n"] == 1, "exactly one AR journal entry per invoice"


class TestRecordPaymentAtomicity:
    def test_payment_writes_all_artifacts_and_order_paid_amount(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=10)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id,
                           qty=10, price=30000)
        api_client.post(f"/sales-orders/{so['id']}/confirm")
        api_client.post(f"/sales-orders/{so['id']}/invoice")

        db_read.execute(
            "SELECT id, total_amount FROM sales_invoices WHERE sales_order_id = %s", (so["id"],))
        inv = db_read.fetchone()
        inv_id, total = str(inv["id"]), float(inv["total_amount"])

        resp = api_client.post(f"/sales-invoices/{inv_id}/record-payment", json={
            "amount": 100000, "payment_method": "cash", "payment_date": "2026-08-03"})
        assert resp.status_code == 200, f"record-payment failed: {resp.text}"

        db_read.execute(
            "SELECT amount_paid, status FROM sales_invoices WHERE id = %s", (inv_id,))
        row = db_read.fetchone()
        assert float(row["amount_paid"]) == 100000 and row["status"] == "partial"

        db_read.execute(
            """SELECT COUNT(*) AS n FROM payment_allocations pa
               JOIN payments p ON p.id = pa.payment_id
               WHERE pa.document_type = 'sales_invoice' AND pa.document_id = %s
                 AND p.status = 'confirmed'""", (inv_id,))
        assert db_read.fetchone()["n"] == 1, "payment + allocation row required"

        db_read.execute(
            """SELECT COUNT(*) AS n FROM journal_entries
               WHERE source_type = 'payment_receipt' AND source_id = %s AND deleted_at IS NULL""",
            (inv_id,))
        assert db_read.fetchone()["n"] == 1, "payment journal entry required"

        order = _order_row(db_read, so["id"])
        assert float(order["paid_amount"]) == 100000, \
            "order paid_amount must follow invoice payments"
        assert order["payment_status"] == "partial"

        # Overpayment refused
        resp = api_client.post(f"/sales-invoices/{inv_id}/record-payment", json={
            "amount": total, "payment_method": "cash", "payment_date": "2026-08-03"})
        assert resp.status_code == 400, "overpayment must be refused"


class TestStatsContract:
    def test_stats_shape_and_visibility(self, api_client, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=2)

        resp = api_client.get("/sales-orders/stats")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]

        for key in ("period", "totals", "monthly_series", "top_customers",
                    "recent_orders", "overdue_invoices"):
            assert key in data, f"missing key: {key}"
        totals = data["totals"]
        for key in ("orders_count", "orders_sum", "revenue_paid", "unpaid_total",
                    "unpaid_over_30d", "overdue_invoices", "undelivered_orders"):
            assert key in totals, f"missing totals key: {key}"
        assert totals["orders_count"] >= 1
        assert len(data["monthly_series"]) == 6
        assert isinstance(data["recent_orders"], list) and len(data["recent_orders"]) >= 1


class TestQuotationFlow:
    def test_numbering_and_single_conversion(self, api_client, db_read, test_customer):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())

        def make_quote():
            resp = api_client.post("/quotations", json={
                "customer_id": test_customer["id"],
                "customer_name": test_customer.get("name", "Test Xaridor MChJ"),
                "tax_percent": 0,
                "items": [{"product_id": product_id, "product_name": "SavdoTest",
                           "quantity": 2, "unit_price": 30000}],
            })
            assert resp.status_code in (200, 201), f"quotation create failed: {resp.text}"
            return resp.json()["data"]

        q1, q2 = make_quote(), make_quote()
        assert q1["quotation_number"] != q2["quotation_number"], \
            "quotation numbers must not collide (old generator counted the wrong table)"

        resp = api_client.post(f"/quotations/{q1['id']}/convert")
        assert resp.status_code == 200, f"convert failed: {resp.text}"

        db_read.execute(
            "SELECT sales_order_id FROM sales_quotations WHERE id = %s", (q1["id"],))
        so_id = db_read.fetchone()["sales_order_id"]
        assert so_id is not None
        order = _order_row(db_read, so_id)
        assert order["order_number"].startswith("S") and "-" not in order["order_number"], \
            f"converted order must use the S-series: {order['order_number']}"
        assert order["organization_id"] is not None, \
            "converted order must be org-scoped (was dropped before)"

        db_read.execute(
            "SELECT COUNT(*) AS n FROM sales_order_lines WHERE sales_order_id = %s", (so_id,))
        assert db_read.fetchone()["n"] == 1

        resp = api_client.post(f"/quotations/{q1['id']}/convert")
        assert resp.status_code == 400, "second conversion must be refused"


class TestStatusConstraints:
    def test_unknown_statuses_refused_by_db(self, db_read, api_client, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=1)

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
                        "UPDATE sales_orders SET status = 'bogus' WHERE id = %s", (so["id"],))
            conn.rollback()
            with conn.cursor() as cur:
                with pytest.raises(psycopg2.errors.CheckViolation):
                    cur.execute(
                        "UPDATE sales_orders SET payment_status = 'bogus' WHERE id = %s",
                        (so["id"],))
        finally:
            conn.rollback()
            conn.close()


# ============================================
# Phase 2 (savdo-changelog "Qolgan ishlar" 1-3-bandlari)
# ============================================

import json as _json


def _own_write_conn(db_read):
    import psycopg2
    conn = psycopg2.connect(
        host=db_read.connection.info.host,
        port=db_read.connection.info.port,
        user=db_read.connection.info.user,
        password=db_read.connection.info.password,
        dbname=db_read.connection.info.dbname,
    )
    conn.autocommit = True
    return conn


class TestLeadOrderHandoff:
    def test_won_lead_creates_order_and_lists(self, api_client, db_read):
        phone = "+99890" + uuid.uuid4().hex[:7].translate(str.maketrans("abcdef", "123456"))
        resp = api_client.post("/leads", json={
            "contact_name": f"Savdo Lead {uuid.uuid4().hex[:6]}",
            "company_name": "Savdo Handoff MChJ",
            "phone": phone,
            "email": f"savdo-{uuid.uuid4().hex[:8]}@test.uz",
            "expected_value": 5_000_000,
            "currency": "UZS",
        })
        assert resp.status_code in (200, 201), resp.text
        lead = resp.json()["data"]

        # Not won yet -> refused
        resp = api_client.post(f"/leads/{lead['id']}/sales-order")
        assert resp.status_code == 400, "lead without partner must be refused"

        resp = api_client.post(f"/leads/{lead['id']}/won", json={})
        assert resp.status_code == 200, resp.text

        resp = api_client.post(f"/leads/{lead['id']}/sales-order")
        assert resp.status_code in (200, 201), resp.text
        order = resp.json()["data"]
        assert order["order_number"].startswith("S")
        assert order["status"] == "draft"

        db_read.execute(
            "SELECT lead_id, customer_id, status FROM sales_orders WHERE id = %s",
            (order["id"],))
        row = db_read.fetchone()
        assert str(row["lead_id"]) == str(lead["id"])
        assert row["status"] == "draft"

        db_read.execute("SELECT partner_id FROM leads WHERE id = %s", (lead["id"],))
        assert str(db_read.fetchone()["partner_id"]) == str(row["customer_id"]), \
            "order customer must be the lead's won partner"

        resp = api_client.get(f"/leads/{lead['id']}/sales-orders")
        assert resp.status_code == 200
        orders = resp.json()["data"]
        assert len(orders) == 1 and orders[0]["id"] == order["id"]


class TestCreditLimit:
    def test_confirm_blocked_when_over_limit(self, api_client, db_read, warehouse_id):
        conn = _own_write_conn(db_read)
        old_settings = None
        try:
            with conn.cursor() as cur:
                cur.execute("SELECT settings FROM tenant_settings WHERE tenant_id = %s",
                            (api_client.tenant_id,))
                row = cur.fetchone()
                old_settings = row[0] if row else None

            settings = dict(old_settings or {})
            sales = dict(settings.get("sales") or {})
            sales["credit"] = {"enable_credit_limit": True, "policy": "block"}
            settings["sales"] = sales
            with conn.cursor() as cur:
                if old_settings is None and row is None:
                    cur.execute(
                        "INSERT INTO tenant_settings (tenant_id, settings, updated_at) VALUES (%s, %s::jsonb, NOW())",
                        (api_client.tenant_id, _json.dumps(settings)))
                else:
                    cur.execute(
                        "UPDATE tenant_settings SET settings = %s::jsonb WHERE tenant_id = %s",
                        (_json.dumps(settings), api_client.tenant_id))

            # Customer with a 100k limit; order for 300k must be blocked at confirm
            code = f"CRD-{uuid.uuid4().hex[:6].upper()}"
            resp = api_client.post("/contacts", json={
                "type": "customer", "code": code, "name": f"Kredit Test {code}",
                "credit_limit": 100000,
            })
            assert resp.status_code in (200, 201), resp.text
            customer = resp.json()["data"]

            product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
            so = _create_order(api_client, customer["id"], warehouse_id, product_id,
                               qty=10, price=30000)
            resp = api_client.post(f"/sales-orders/{so['id']}/confirm")
            assert resp.status_code == 422, f"expected credit-limit block: {resp.text}"
            assert "CREDIT_LIMIT_EXCEEDED" in resp.text
            assert _order_row(db_read, so["id"])["status"] == "draft", \
                "blocked confirm must leave the order in draft"
        finally:
            with conn.cursor() as cur:
                if old_settings is not None:
                    cur.execute("UPDATE tenant_settings SET settings = %s::jsonb WHERE tenant_id = %s",
                                (_json.dumps(old_settings), api_client.tenant_id))
                else:
                    cur.execute("UPDATE tenant_settings SET settings = settings - 'sales' WHERE tenant_id = %s",
                                (api_client.tenant_id,))
            conn.close()


class TestContractInheritance:
    def test_invoice_inherits_order_contract(self, api_client, db_read, test_customer, warehouse_id):
        from datetime import date, timedelta
        resp = api_client.post("/contracts", json={
            "title": f"Savdo shartnoma {uuid.uuid4().hex[:6]}",
            "vendor_id": str(test_customer["id"]),
            "direction": "income",
            "start_date": date.today().isoformat(),
            "end_date": (date.today() + timedelta(days=90)).isoformat(),
            "value": 2_000_000,
        })
        assert resp.status_code in (200, 201), resp.text
        contract = resp.json()["data"]

        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id,
                           qty=2, price=30000, contract_id=str(contract["id"]))
        db_read.execute("SELECT contract_id FROM sales_orders WHERE id = %s", (so["id"],))
        assert str(db_read.fetchone()["contract_id"]) == str(contract["id"]), \
            "order must persist contract_id"

        api_client.post(f"/sales-orders/{so['id']}/confirm")
        resp = api_client.post(f"/sales-orders/{so['id']}/invoice")
        assert resp.status_code in (200, 201), resp.text

        db_read.execute(
            "SELECT contract_id FROM sales_invoices WHERE sales_order_id = %s", (so["id"],))
        assert str(db_read.fetchone()["contract_id"]) == str(contract["id"]), \
            "invoice must inherit the order's contract link"


class TestAutoDueDate:
    def test_due_date_derived_when_omitted(self, api_client, db_read, test_customer):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        resp = api_client.post("/sales-invoices", json={
            "customer_id": test_customer["id"],
            "invoice_date": "2026-08-03",
            "lines": [{"product_id": product_id, "description": "auto due date",
                       "quantity": 1, "unit_price": 50000}],
        })
        assert resp.status_code in (200, 201), f"invoice without due_date failed: {resp.text}"
        inv_id = resp.json()["data"]["id"]
        db_read.execute("SELECT due_date FROM sales_invoices WHERE id = %s", (inv_id,))
        assert str(db_read.fetchone()["due_date"]) == "2026-09-02", \
            "NET30 fallback: due = invoice_date + 30 days"


# ============================================
# Phase 3 (rezervatsiya, delivered-basis fakturalash, org-scope, cancel guard)
# ============================================


def _reserved(db_read, tenant_id, product_id):
    db_read.execute(
        """SELECT COALESCE(SUM(quantity_reserved), 0) AS q
           FROM inventory WHERE tenant_id = %s AND product_id = %s""",
        (tenant_id, product_id))
    return float(db_read.fetchone()["q"])


class TestReservationLifecycle:
    def test_confirm_reserves_validate_releases(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=20)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=10)

        base = _reserved(db_read, api_client.tenant_id, product_id)
        api_client.post(f"/sales-orders/{so['id']}/confirm")
        assert _reserved(db_read, api_client.tenant_id, product_id) == base + 10, \
            "confirm must reserve the ordered quantity"

        do_id = str(_do_rows(db_read, so["id"])[0]["id"])
        resp = api_client.post(f"/sales/delivery-orders/{do_id}/validate")
        assert resp.status_code == 200, resp.text
        assert _reserved(db_read, api_client.tenant_id, product_id) == base, \
            "shipping must release the reservation"

    def test_cancel_releases_reservation_and_open_dos(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=20)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=5)

        base = _reserved(db_read, api_client.tenant_id, product_id)
        api_client.post(f"/sales-orders/{so['id']}/confirm")
        assert _reserved(db_read, api_client.tenant_id, product_id) == base + 5

        resp = api_client.post(f"/sales-orders/{so['id']}/cancel")
        assert resp.status_code == 200, resp.text
        assert _reserved(db_read, api_client.tenant_id, product_id) == base, \
            "cancel must release the reservation"
        dos = _do_rows(db_read, so["id"])
        assert dos and all(d["status"] == "cancelled" for d in dos), \
            "cancel must cancel the order's open delivery documents"

    def test_cancel_refused_after_shipping(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=20)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=4)
        api_client.post(f"/sales-orders/{so['id']}/confirm")
        do_id = str(_do_rows(db_read, so["id"])[0]["id"])
        api_client.post(f"/sales/delivery-orders/{do_id}/validate")

        resp = api_client.post(f"/sales-orders/{so['id']}/cancel")
        assert resp.status_code == 400, "cancel after shipping must point to the return flow"


class TestDeliveredBasisInvoice:
    def test_partial_delivery_partial_invoice(
            self, api_client, db_read, test_supplier, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        _stock_product(api_client, db_read, test_supplier, warehouse_id, product_id, qty=6)
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id,
                           qty=10, price=30000)
        api_client.post(f"/sales-orders/{so['id']}/confirm")
        do_id = str(_do_rows(db_read, so["id"])[0]["id"])

        # Only 6 of 10 in stock → partial ship + backorder
        resp = api_client.post(f"/sales/delivery-orders/{do_id}/validate?partial=true")
        assert resp.status_code == 200, resp.text
        assert float(_order_line(db_read, so["id"])["qd"]) == 6

        resp = api_client.post(f"/sales-orders/{so['id']}/invoice?basis=delivered")
        assert resp.status_code in (200, 201), f"delivered-basis invoice failed: {resp.text}"

        db_read.execute(
            """SELECT si.total_amount, sil.quantity
               FROM sales_invoices si JOIN sales_invoice_lines sil ON sil.sales_invoice_id = si.id
               WHERE si.sales_order_id = %s AND si.deleted_at IS NULL""", (so["id"],))
        row = db_read.fetchone()
        assert float(row["quantity"]) == 6, "invoice qty must equal delivered qty"
        assert float(row["total_amount"]) == 6 * 30000, "totals must be line-derived"
        assert float(_order_line(db_read, so["id"])["qi"]) == 6

        # Nothing new delivered → second delivered-basis invoice refused
        resp = api_client.post(f"/sales-orders/{so['id']}/invoice?basis=delivered")
        assert resp.status_code == 400, "no new delivered qty → refuse"


class TestOrgScopeOnDetail:
    def test_foreign_org_header_gets_404(self, api_client, test_customer, warehouse_id):
        product_id = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        so = _create_order(api_client, test_customer["id"], warehouse_id, product_id, qty=1)

        foreign = {"X-Organization-ID": str(uuid.uuid4())}
        url = f"{api_client.base_url}/sales-orders/{so['id']}"
        resp = api_client.session.get(url, headers=foreign)
        assert resp.status_code == 404, f"foreign org must not read the order: {resp.status_code}"
        resp = api_client.session.post(f"{url}/confirm", headers=foreign)
        assert resp.status_code == 404, "foreign org must not confirm the order"
