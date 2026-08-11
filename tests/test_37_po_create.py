"""
Purchase-order CREATE contract tests (POST /purchase-orders).

Scope — creation only (lifecycle/receipt invariants live in test_25_xarid.py):
  1. Minimal valid PO (vendor + one line): 201, generated PO-xxxxx number,
     status 'draft', payment_status 'unpaid'.
  2. Totals math: multi-line with tax_percent/discount/shipping — subtotal,
     tax_amount, total_amount consistent with the lines, on both the create
     response and the read-back.
  3. Defaults: vehicle_number and requires_shipping OMITTED from the payload
     (the frontend no longer sends them) — create succeeds, read-back shows
     requires_shipping=true (server default) and no vehicle_number.
  4. Backward compat: payload WITH vehicle_number + requires_shipping=false
     is still accepted and both persist on read-back.
  5. Validation: missing vendor / empty lines — actual behavior is 400.
     A line without product_id currently 500s AND leaves an orphaned
     zero-line draft PO (header insert is outside the tx while
     purchase_order_lines.product_id is NOT NULL) — that test documents the
     live bug and is EXPECTED TO FAIL until the backend is fixed.
  6. List/Get: created PO appears in GET /purchase-orders (vendor_id filter)
     and GET /purchase-orders/:id with correct core fields. The ?search=
     filter currently 500s (countQuery lacks the contacts join the filter
     references) — that test also documents a live bug.
  7. Goods-receipt logistics: PO → approve (test_25 flow) → POST
     /goods-receipts with vehicle_number + driver_name → GET returns both.

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import re
import uuid

import pytest

EPS = 0.01


# ============================================
# Helpers / fixtures
# ============================================

def _make_product(api_client, code_suffix, **overrides):
    """Same shape as test_25's product factory."""
    payload = {
        "name": f"POCREATE {code_suffix}",
        "code": f"POC-{code_suffix}",
        "sku": f"POC-{code_suffix}",
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


@pytest.fixture(scope="module")
def product_id(api_client):
    """One shared product for tests that don't care about stock identity."""
    return _make_product(api_client, uuid.uuid4().hex[:6].upper())


def _create_po(api_client, payload):
    resp = api_client.post("/purchase-orders", json=payload)
    assert resp.status_code == 201, f"PO create failed ({resp.status_code}): {resp.text}"
    return resp.json()["data"]


def _get_po(api_client, po_id):
    resp = api_client.get(f"/purchase-orders/{po_id}")
    assert resp.status_code == 200, f"PO get failed ({resp.status_code}): {resp.text}"
    return resp.json()["data"]


# ============================================
# 1. Minimal valid PO
# ============================================

class TestMinimalCreate:
    def test_minimal_po(self, api_client, test_supplier, product_id):
        po = _create_po(api_client, {
            "vendor_id": test_supplier["id"],
            "lines": [{
                "product_id": product_id,
                "description": "Minimal PO line",
                "quantity": 1,
                "unit_price": 7000,
            }],
        })

        assert po.get("id"), "created PO must carry an id"
        # Generated order number (we did not supply one)
        assert re.match(r"^PO-\d+$", po.get("order_number", "")), \
            f"expected generated PO-<n> number, got {po.get('order_number')!r}"
        assert po["status"] == "draft"
        assert po["payment_status"] == "unpaid"
        assert float(po["total_amount"]) == pytest.approx(7000, abs=EPS)


# ============================================
# 2. Totals math
# ============================================

class TestTotalsMath:
    def test_multi_line_tax_discount_shipping(self, api_client, test_supplier, warehouse_id):
        p1 = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        p2 = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        p3 = _make_product(api_client, uuid.uuid4().hex[:6].upper())

        lines = [
            # (product, qty, price, discount, tax%)
            (p1, 3, 10000, 0, 12),
            (p2, 2, 5500, 1000, 12),
            (p3, 1, 8000, 0, 0),
        ]
        shipping = 15000

        exp_subtotal = sum(q * pr for _, q, pr, _, _ in lines)                     # 49000
        exp_discount = sum(d for _, _, _, d, _ in lines)                           # 1000
        exp_tax = sum((q * pr - d) * t / 100 for _, q, pr, d, t in lines)          # 4800
        exp_total = exp_subtotal - exp_discount + exp_tax + shipping               # 67800

        po = _create_po(api_client, {
            "vendor_id": test_supplier["id"],
            "warehouse_id": warehouse_id,
            "shipping_amount": shipping,
            "lines": [
                {
                    "product_id": pid,
                    "description": f"Totals line {i + 1}",
                    "quantity": q,
                    "unit_price": pr,
                    "discount_amount": d,
                    "tax_percent": t,
                }
                for i, (pid, q, pr, d, t) in enumerate(lines)
            ],
        })

        # Create response
        assert float(po["subtotal"]) == pytest.approx(exp_subtotal, abs=EPS)
        assert float(po["discount_amount"]) == pytest.approx(exp_discount, abs=EPS)
        assert float(po["tax_amount"]) == pytest.approx(exp_tax, abs=EPS)
        assert float(po["shipping_amount"]) == pytest.approx(shipping, abs=EPS)
        assert float(po["total_amount"]) == pytest.approx(exp_total, abs=EPS)

        # Read-back agrees
        got = _get_po(api_client, po["id"])
        assert float(got["subtotal"]) == pytest.approx(exp_subtotal, abs=EPS)
        assert float(got["tax_amount"]) == pytest.approx(exp_tax, abs=EPS)
        assert float(got["total_amount"]) == pytest.approx(exp_total, abs=EPS)

        # Internal consistency: total == subtotal - discount + tax + shipping
        assert float(got["total_amount"]) == pytest.approx(
            float(got["subtotal"]) - float(got["discount_amount"])
            + float(got["tax_amount"]) + float(got["shipping_amount"]),
            abs=EPS,
        )

        # Line-level math on read-back
        got_lines = got.get("lines") or []
        assert len(got_lines) == 3
        by_no = {l["line_number"]: l for l in got_lines}
        # Line 2: 2×5500 − 1000 discount + 12% tax on the discounted base
        l2 = by_no[2]
        assert float(l2["tax_amount"]) == pytest.approx(1200, abs=EPS)
        assert float(l2["line_total"]) == pytest.approx(11200, abs=EPS)
        # Sum of line subtotals matches header subtotal
        line_sub = sum(float(l["quantity"]) * float(l["unit_price"]) for l in got_lines)
        assert line_sub == pytest.approx(float(got["subtotal"]), abs=EPS)


# ============================================
# 3. Defaults (frontend no longer sends these fields)
# ============================================

class TestShippingDefaults:
    def test_omitted_fields_default_requires_shipping_true(self, api_client, test_supplier, product_id):
        payload = {
            "vendor_id": test_supplier["id"],
            "lines": [{
                "product_id": product_id,
                "description": "Defaults line",
                "quantity": 2,
                "unit_price": 3000,
            }],
        }
        assert "vehicle_number" not in payload and "requires_shipping" not in payload
        po = _create_po(api_client, payload)

        # Server default already visible on the create response
        assert po["requires_shipping"] is True

        got = _get_po(api_client, po["id"])
        assert got["requires_shipping"] is True, \
            "omitting requires_shipping must default to true on read-back"
        # vehicle_number is a NULL-able pointer with omitempty — absent or empty
        assert got.get("vehicle_number") in (None, ""), \
            f"omitted vehicle_number must not materialise, got {got.get('vehicle_number')!r}"


# ============================================
# 4. Backward compat (old payload shape still works)
# ============================================

class TestShippingBackwardCompat:
    def test_vehicle_number_and_requires_shipping_false_persist(self, api_client, test_supplier, product_id):
        vehicle = "01A123BC"
        po = _create_po(api_client, {
            "vendor_id": test_supplier["id"],
            "vehicle_number": vehicle,
            "requires_shipping": False,
            "lines": [{
                "product_id": product_id,
                "description": "Compat line",
                "quantity": 1,
                "unit_price": 4500,
            }],
        })

        assert po["requires_shipping"] is False
        assert po.get("vehicle_number") == vehicle

        got = _get_po(api_client, po["id"])
        assert got["requires_shipping"] is False, "explicit false must persist (not be defaulted)"
        assert got.get("vehicle_number") == vehicle


# ============================================
# 5. Validation
# ============================================

class TestValidation:
    def test_missing_vendor_is_400(self, api_client, product_id):
        resp = api_client.post("/purchase-orders", json={
            "lines": [{
                "product_id": product_id,
                "description": "No vendor",
                "quantity": 1,
                "unit_price": 100,
            }],
        })
        assert resp.status_code < 500, f"missing vendor must not 5xx: {resp.text}"
        assert resp.status_code == 400, \
            f"actual behavior is 400 (binding required), got {resp.status_code}: {resp.text}"

    def test_empty_lines_is_400(self, api_client, test_supplier):
        resp = api_client.post("/purchase-orders", json={
            "vendor_id": test_supplier["id"],
            "lines": [],
        })
        assert resp.status_code < 500, f"empty lines must not 5xx: {resp.text}"
        assert resp.status_code == 400, \
            f"actual behavior is 400 (binding min=1), got {resp.status_code}: {resp.text}"

    def test_nonexistent_vendor_is_404(self, api_client, product_id):
        resp = api_client.post("/purchase-orders", json={
            "vendor_id": str(uuid.uuid4()),
            "lines": [{
                "product_id": product_id,
                "description": "Ghost vendor",
                "quantity": 1,
                "unit_price": 100,
            }],
        })
        assert resp.status_code < 500, f"unknown vendor must not 5xx: {resp.text}"
        assert resp.status_code == 404, \
            f"actual behavior is 404 (vendor lookup), got {resp.status_code}: {resp.text}"

    def test_line_without_product_id_must_not_5xx(self, api_client, db_read, test_supplier):
        """KNOWN LIVE BUG — expected to FAIL until the backend is fixed.

        The input contract treats line.product_id as optional, but
        purchase_order_lines.product_id is NOT NULL in the DB. A
        description-only line therefore 500s on the line INSERT — and
        because the header INSERT runs OUTSIDE the tx (collision-retry
        design), every such request leaks an orphaned zero-line draft PO
        into the list. Correct behavior is a 400 (or acceptance) with no
        orphan; any 5xx is a real defect, so this test asserts non-5xx.
        """
        marker = f"NOPRODUCT-{uuid.uuid4().hex[:8]}"
        resp = api_client.post("/purchase-orders", json={
            "vendor_id": test_supplier["id"],
            "internal_notes": marker,
            "lines": [{
                "description": "Service line without product",
                "quantity": 1,
                "unit_price": 12345,
            }],
        })
        # Orphan check first so the diagnostic is visible even on the 5xx.
        db_read.execute(
            """SELECT COUNT(*) AS n
               FROM purchase_orders po
               WHERE po.tenant_id = %s AND po.internal_notes = %s
                 AND NOT EXISTS (SELECT 1 FROM purchase_order_lines pol
                                 WHERE pol.purchase_order_id = po.id)""",
            (api_client.tenant_id, marker),
        )
        orphans = db_read.fetchone()["n"]
        assert resp.status_code < 500, (
            f"line without product_id must not 5xx (got {resp.status_code}: {resp.text}); "
            f"orphaned zero-line PO headers leaked by this request: {orphans}"
        )


# ============================================
# 6. List / Get
# ============================================

class TestListGet:
    def test_created_po_in_list_and_get(self, api_client, test_supplier, warehouse_id):
        pid = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, {
            "vendor_id": test_supplier["id"],
            "warehouse_id": warehouse_id,
            "notes": "List/Get check",
            "lines": [{
                "product_id": pid,
                "description": "List check line",
                "quantity": 4,
                "unit_price": 2500,
            }],
        })

        # --- list (vendor_id filter; newest first) ---
        resp = api_client.get("/purchase-orders", params={
            "vendor_id": test_supplier["id"], "limit": 100,
        })
        assert resp.status_code == 200, resp.text
        rows = resp.json()["data"]
        assert isinstance(rows, list)
        mine = next((r for r in rows if r["id"] == po["id"]), None)
        assert mine is not None, "created PO missing from GET /purchase-orders"
        assert mine["order_number"] == po["order_number"]
        assert mine["status"] == "draft"
        assert mine["payment_status"] == "unpaid"
        assert float(mine["total_amount"]) == pytest.approx(10000, abs=EPS)

        # --- get by id ---
        got = _get_po(api_client, po["id"])
        assert got["id"] == po["id"]
        assert got["order_number"] == po["order_number"]
        assert got["vendor_id"] == test_supplier["id"]
        assert got["vendor_name"], "vendor_name must be joined in"
        assert got["status"] == "draft"
        assert got["payment_status"] == "unpaid"
        assert float(got["subtotal"]) == pytest.approx(10000, abs=EPS)
        lines = got.get("lines") or []
        assert len(lines) == 1
        assert lines[0]["product_id"] == pid
        assert float(lines[0]["quantity"]) == pytest.approx(4)
        assert float(lines[0]["unit_price"]) == pytest.approx(2500)
        assert float(lines[0]["quantity_received"]) == 0

    def test_get_unknown_id_is_404(self, api_client):
        resp = api_client.get(f"/purchase-orders/{uuid.uuid4()}")
        assert resp.status_code == 404, resp.text

    def test_list_search_must_not_5xx(self, api_client, test_supplier, product_id):
        """KNOWN LIVE BUG — expected to FAIL until the backend is fixed.

        GET /purchase-orders?search=... appends a filter referencing the
        contacts alias (c.name) to BOTH baseQuery and countQuery, but
        countQuery selects from purchase_orders alone — Postgres rejects it
        ('missing FROM-clause entry for table c') and every search request
        500s. Searching by order number is the primary way the frontend
        finds a PO, so this is asserted as non-5xx.
        """
        po = _create_po(api_client, {
            "vendor_id": test_supplier["id"],
            "lines": [{
                "product_id": product_id,
                "description": "Search target",
                "quantity": 1,
                "unit_price": 1100,
            }],
        })
        resp = api_client.get("/purchase-orders", params={"search": po["order_number"]})
        assert resp.status_code < 500, \
            f"?search must not 5xx (got {resp.status_code}: {resp.text})"
        rows = resp.json()["data"]
        assert any(r["id"] == po["id"] for r in rows), \
            "searched order number must find the created PO"


# ============================================
# 7. Goods-receipt logistics (vehicle/driver moved to the GR document)
# ============================================

class TestGoodsReceiptLogistics:
    def test_gr_vehicle_and_driver_persist(self, api_client, test_supplier, warehouse_id):
        pid = _make_product(api_client, uuid.uuid4().hex[:6].upper())
        po = _create_po(api_client, {
            "vendor_id": test_supplier["id"],
            "warehouse_id": warehouse_id,
            "lines": [{
                "product_id": pid,
                "description": "GR logistics line",
                "quantity": 5,
                "unit_price": 6000,
            }],
        })

        # Confirm/approve flow, the way test_25 does it
        resp = api_client.post(f"/purchase-orders/{po['id']}/approve")
        assert resp.status_code == 200, resp.text
        got = _get_po(api_client, po["id"])
        assert got["status"] == "approved"
        line = got["lines"][0]

        vehicle, driver = "01B777CD", "Test Haydovchi"
        resp = api_client.post("/goods-receipts", json={
            "purchase_order_id": po["id"],
            "received_by": "Test Qabul qiluvchi",
            "warehouse_id": warehouse_id,
            "vehicle_number": vehicle,
            "driver_name": driver,
            "lines": [{
                "po_line_id": line["id"],
                "product_id": pid,
                "product_name": line.get("product_name") or "GR logistics line",
                "ordered_quantity": 5,
                "received_quantity": 5,
                "unit_price": 6000,
            }],
        })
        assert resp.status_code in (200, 201), f"GR create failed: {resp.text}"
        gr = resp.json()["data"]
        assert gr.get("id")
        assert gr.get("gr_number"), "GR number must be generated"
        assert gr["purchase_order_id"] == po["id"]

        # Read-back: both logistics fields persisted on the GR document
        resp = api_client.get(f"/goods-receipts/{gr['id']}")
        assert resp.status_code == 200, resp.text
        got_gr = resp.json()["data"]
        assert got_gr.get("vehicle_number") == vehicle, \
            f"vehicle_number must persist on GR, got {got_gr.get('vehicle_number')!r}"
        assert got_gr.get("driver_name") == driver, \
            f"driver_name must persist on GR, got {got_gr.get('driver_name')!r}"
        assert got_gr["purchase_order_id"] == po["id"]
        assert float(got_gr["total_quantity"]) == pytest.approx(5)
        gr_lines = got_gr.get("lines") or []
        assert len(gr_lines) == 1
        assert float(gr_lines[0]["received_quantity"]) == pytest.approx(5)
