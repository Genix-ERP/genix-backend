"""
RFQ (Request for Quotation) lifecycle tests.

Scope — the full RFQ flow that was rebuilt on 2026-08-13 after CreateRFQ
turned out to be broken against the live schema (INSERT referenced a
nonexistent rfqs.response_deadline column, so the rfqs table had 0 rows):
  1. Create: 201, generated RFQ number (RFQxxxxx, MAX+1 per tenant/org),
     status 'draft', items + invitations persisted, unit string preserved.
  2. Validation: empty items → 400.
  3. Open → respond: response total = qty * unit_price; re-submitting the
     same vendor's response must UPDATE the existing row (regression for the
     ON CONFLICT id mismatch that aborted the tx), not duplicate or 500.
  4. Select winner → RFQ closed; convert-to-PO creates a draft PO with
     correct lines (regression for the stale-schema INSERTs: vendor_name /
     product_name / total_price / tenant_id columns don't exist, join used
     ri.item_id instead of ri.rfq_item_id). Duplicate convert → 400.
  5. Convert with a product-less (free-text) item → 400, no orphan PO
     (purchase_order_lines.product_id is NOT NULL).
  6. Delete: closed RFQ → 400; draft RFQ → 204 (soft delete).

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
    payload = {
        "name": f"RFQTEST {code_suffix}",
        "code": f"RFQT-{code_suffix}",
        "sku": f"RFQT-{code_suffix}",
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
def product_id(api_client):
    return _make_product(api_client, uuid.uuid4().hex[:8].upper())


def _create_rfq(api_client, product_id, supplier_id, **overrides):
    payload = {
        "title": f"RFQ test {uuid.uuid4().hex[:6]}",
        "description": "integration test RFQ",
        "deadline": "2026-12-31",
        "items": [
            {
                "product_id": product_id,
                "description": "RFQ test item",
                "quantity": 10,
                "unit_id": "dona",
            }
        ],
        "vendor_ids": [supplier_id],
    }
    payload.update(overrides)
    return api_client.post("/rfqs", json=payload)


def _full_rfq_to_winner(api_client, product_id, supplier_id, unit_price=25000, **create_overrides):
    """Create → open → respond → select winner. Returns (rfq_id, response_id)."""
    resp = _create_rfq(api_client, product_id, supplier_id, **create_overrides)
    assert resp.status_code == 201, resp.text
    rfq_id = resp.json()["data"]["id"]

    resp = api_client.post(f"/rfqs/{rfq_id}/open")
    assert resp.status_code == 200, resp.text

    detail = api_client.get(f"/rfqs/{rfq_id}").json()["data"]
    items = [{"item_id": it["id"], "unit_price": unit_price} for it in detail["items"]]
    resp = api_client.post(
        f"/rfqs/{rfq_id}/responses",
        json={"vendor_id": supplier_id, "lead_time_days": 5, "items": items},
    )
    assert resp.status_code == 200, resp.text

    detail = api_client.get(f"/rfqs/{rfq_id}").json()["data"]
    response_id = detail["responses"][0]["id"]
    resp = api_client.post(f"/rfqs/{rfq_id}/select-winner", json={"response_id": response_id})
    assert resp.status_code == 200, resp.text
    return rfq_id, response_id


# ============================================
# 1-2. Create + validation
# ============================================

class TestCreateRFQ:
    def test_create_minimal(self, api_client, db_read, test_supplier, product_id):
        resp = _create_rfq(api_client, product_id, test_supplier["id"])
        assert resp.status_code == 201, resp.text
        data = resp.json()["data"]

        assert re.fullmatch(r"RFQ\d{5,}", data["rfq_number"]), data["rfq_number"]
        assert data["status"] == "draft"
        assert len(data["items"]) == 1

        # Read-back: item with unit string and one invitation
        detail = api_client.get(f"/rfqs/{data['id']}").json()["data"]
        assert len(detail["items"]) == 1
        assert detail["items"][0]["unit_name"] == "dona"
        assert abs(detail["items"][0]["quantity"] - 10) < EPS

        db_read.execute(
            "SELECT COUNT(*) AS n FROM rfq_invitations WHERE rfq_id = %s",
            (data["id"],),
        )
        assert db_read.fetchone()["n"] == 1

    def test_numbers_increment(self, api_client, test_supplier, product_id):
        n1 = _create_rfq(api_client, product_id, test_supplier["id"]).json()["data"]["rfq_number"]
        n2 = _create_rfq(api_client, product_id, test_supplier["id"]).json()["data"]["rfq_number"]
        assert n1 != n2
        assert int(n2[3:]) == int(n1[3:]) + 1

    def test_empty_items_is_400(self, api_client, test_supplier, product_id):
        resp = _create_rfq(api_client, product_id, test_supplier["id"], items=[])
        assert resp.status_code == 400, resp.text

    def test_appears_in_list(self, api_client, test_supplier, product_id):
        rfq = _create_rfq(api_client, product_id, test_supplier["id"]).json()["data"]
        listing = api_client.get("/rfqs", params={"search": rfq["rfq_number"]})
        assert listing.status_code == 200, listing.text
        numbers = [r["rfq_number"] for r in listing.json()["data"]]
        assert rfq["rfq_number"] in numbers


# ============================================
# 3. Open + responses
# ============================================

class TestRFQResponses:
    def test_respond_and_resubmit_updates_in_place(self, api_client, db_read, test_supplier, product_id):
        resp = _create_rfq(api_client, product_id, test_supplier["id"])
        rfq_id = resp.json()["data"]["id"]

        # Responses only allowed on open RFQs
        detail = api_client.get(f"/rfqs/{rfq_id}").json()["data"]
        item_id = detail["items"][0]["id"]
        early = api_client.post(
            f"/rfqs/{rfq_id}/responses",
            json={"vendor_id": test_supplier["id"], "items": [{"item_id": item_id, "unit_price": 100}]},
        )
        assert early.status_code == 400, early.text

        assert api_client.post(f"/rfqs/{rfq_id}/open").status_code == 200

        resp = api_client.post(
            f"/rfqs/{rfq_id}/responses",
            json={"vendor_id": test_supplier["id"], "lead_time_days": 7,
                  "items": [{"item_id": item_id, "unit_price": 30000}]},
        )
        assert resp.status_code == 200, resp.text
        assert abs(resp.json()["data"]["total_amount"] - 10 * 30000) < EPS

        # Re-submission must update the same row, not 500 or duplicate
        resp = api_client.post(
            f"/rfqs/{rfq_id}/responses",
            json={"vendor_id": test_supplier["id"], "lead_time_days": 4,
                  "items": [{"item_id": item_id, "unit_price": 28000}]},
        )
        assert resp.status_code == 200, resp.text

        db_read.execute(
            "SELECT COUNT(*) AS n, MAX(total_amount) AS amt, BOOL_AND(lead_time_days = 4) AS lead_ok "
            "FROM rfq_responses WHERE rfq_id = %s AND vendor_id = %s",
            (rfq_id, test_supplier["id"]),
        )
        row = db_read.fetchone()
        assert row["n"] == 1
        assert abs(float(row["amt"]) - 10 * 28000) < EPS
        assert row["lead_ok"] is True

        db_read.execute(
            "SELECT COUNT(*) AS n FROM rfq_response_items ri "
            "JOIN rfq_responses r ON r.id = ri.response_id WHERE r.rfq_id = %s",
            (rfq_id,),
        )
        assert db_read.fetchone()["n"] == 1


# ============================================
# 4-5. Winner + convert to PO
# ============================================

class TestConvertToPO:
    def test_winner_and_convert(self, api_client, db_read, test_supplier, product_id):
        rfq_id, _ = _full_rfq_to_winner(api_client, product_id, test_supplier["id"], unit_price=25000)

        detail = api_client.get(f"/rfqs/{rfq_id}").json()["data"]
        assert detail["status"] == "closed"
        assert detail["winner_id"] == test_supplier["id"]

        resp = api_client.post(f"/rfqs/{rfq_id}/convert-to-po")
        assert resp.status_code == 201, resp.text
        data = resp.json()["data"]
        po_id = data["purchase_order_id"]
        assert re.fullmatch(r"PO-\d{5,}", data["order_number"]), data["order_number"]
        assert abs(data["total_amount"] - 10 * 25000) < EPS

        db_read.execute(
            "SELECT po.status, po.total_amount, COUNT(l.id) AS lines, "
            "       MIN(l.line_total) AS line_total, BOOL_AND(l.product_id IS NOT NULL) AS products_ok "
            "FROM purchase_orders po JOIN purchase_order_lines l ON l.purchase_order_id = po.id "
            "WHERE po.id = %s GROUP BY po.id, po.status, po.total_amount",
            (po_id,),
        )
        row = db_read.fetchone()
        assert row is not None, "PO has no lines"
        assert row["status"] == "draft"
        assert row["lines"] == 1
        assert abs(float(row["line_total"]) - 10 * 25000) < EPS
        assert row["products_ok"] is True

        # Duplicate convert must be rejected
        dup = api_client.post(f"/rfqs/{rfq_id}/convert-to-po")
        assert dup.status_code == 400, dup.text

    def test_convert_without_winner_is_400(self, api_client, test_supplier, product_id):
        resp = _create_rfq(api_client, product_id, test_supplier["id"])
        rfq_id = resp.json()["data"]["id"]
        conv = api_client.post(f"/rfqs/{rfq_id}/convert-to-po")
        assert conv.status_code == 400, conv.text

    def test_convert_freetext_item_is_400_no_orphan(self, api_client, db_read, test_supplier):
        # Item without product_id: convert must fail cleanly (PO lines require
        # a product) and must not leave an orphaned PO header behind.
        rfq_id, _ = _full_rfq_to_winner(
            api_client, None, test_supplier["id"],
            items=[{"description": "erkin matnli pozitsiya", "quantity": 3, "unit_id": "kg"}],
        )
        conv = api_client.post(f"/rfqs/{rfq_id}/convert-to-po")
        assert conv.status_code == 400, conv.text

        db_read.execute("SELECT COUNT(*) AS n FROM purchase_orders WHERE rfq_id = %s", (rfq_id,))
        assert db_read.fetchone()["n"] == 0


# ============================================
# 6. Delete rules
# ============================================

class TestDeleteRFQ:
    def test_closed_rfq_not_deletable(self, api_client, test_supplier, product_id):
        rfq_id, _ = _full_rfq_to_winner(api_client, product_id, test_supplier["id"])
        resp = api_client.delete(f"/rfqs/{rfq_id}")
        assert resp.status_code == 400, resp.text

    def test_draft_rfq_soft_deleted(self, api_client, db_read, test_supplier, product_id):
        rfq = _create_rfq(api_client, product_id, test_supplier["id"]).json()["data"]
        resp = api_client.delete(f"/rfqs/{rfq['id']}")
        assert resp.status_code in (200, 204), resp.text

        db_read.execute("SELECT deleted_at FROM rfqs WHERE id = %s", (rfq["id"],))
        assert db_read.fetchone()["deleted_at"] is not None

        assert api_client.get(f"/rfqs/{rfq['id']}").status_code == 404
