"""
Material zayavkalari v2 (migration 470) — Qurilish → Ombor → Xarid yopiq
halqa testlari. DoD ro'yxati (§11, genix-qurilish-material-zayavkalari
prompti) bo'yicha:

  1. To'liq oqim: prorab zayavka → omborchi review → issue → stok kamayadi,
     chiqim hujjati (done) + balansli JE (Dr 0810 / Cr zaxira) + loyiha
     xarajat qatorlari (CEL, material_request_id bilan) → prorab accept →
     closed (closed_at bilan).
  2. Xarid oqimi: stok yetmasa send-to-purchase → PR avto (status approved,
     material_request_id backlink, purpose'da MZ raqami) → convert-to-po
     (PO organization_id bilan — 470-fix) → receive → zayavka timeline'ida
     'material_arrived' (yopiq halqa) → issue → issued.
  3. Qisman: bir qator chiqarilgan + bir qator xaridda →
     partially_fulfilled; line statuslar issued/in_purchase.
  4. Rad etish sababsiz o'tmaydi (400); sabab bilan → rejected +
     rejected_reason; harakat boshlangan zayavka rad etilmaydi.
  5. State-machine: noto'g'ri o'tishlar 4xx (accept on new, cancel on
     issued, issue on cancelled).
  6. Yetarli bo'lmagan stok → 422 INSUFFICIENT_STOCK (errors[] bilan).
  7. Stats/stock-check kontraktlari.
  8. Legacy endpointlar v2 yozuvlarini boshqara olmaydi (400 guard).

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import time
import uuid

import pytest


CODE_PREFIX = "QURT31"


def _make_project(api_client, suffix):
    resp = api_client.post("/construction/projects", json={
        "code": f"{CODE_PREFIX}-{suffix}-{uuid.uuid4().hex[:6]}",
        "name": f"MZ test loyiha {suffix}",
        "region": "Toshkent shahri",
        "city": "Toshkent",
        "project_type": "residential",
    })
    assert resp.status_code in (200, 201), f"project create failed: {resp.text}"
    return resp.json()["data"]["id"]


def _make_product(api_client, suffix, cost=15000):
    resp = api_client.post("/products", json={
        "name": f"MZTEST {suffix}",
        "code": f"MZT-{suffix}",
        "sku": f"MZT-{suffix}",
        "type": "product",
        "is_stockable": True,
        "track_inventory": True,
        "cost_price": cost,
        "list_price": cost * 2,
        "is_active": True,
        "unit_name": "dona",
    })
    assert resp.status_code in (200, 201), f"product create failed: {resp.text}"
    return resp.json()["data"]["id"]


def _add_stock(api_client, product_id, warehouse_id, qty, cost=15000):
    resp = api_client.post("/inventory/adjust", json={
        "product_id": product_id, "warehouse_id": warehouse_id,
        "quantity": qty, "unit_cost": cost, "reason": "MZ test kirim",
    })
    assert resp.status_code == 200, resp.text


def _create_request(api_client, project_id, items, priority="normal", required_date=None, warehouse_id=None):
    payload = {"project_id": project_id, "priority": priority, "items": items}
    if required_date:
        payload["required_date"] = required_date
    if warehouse_id:
        payload["warehouse_id"] = warehouse_id
    resp = api_client.post("/construction/material-requests-v2", json=payload)
    assert resp.status_code in (200, 201), f"MZ create failed: {resp.text}"
    return resp.json()["data"]


def _get_request(api_client, req_id):
    resp = api_client.get(f"/construction/material-requests-v2/{req_id}")
    assert resp.status_code == 200, resp.text
    return resp.json()["data"]


def _on_hand(db_read, tenant_id, product_id, warehouse_id):
    db_read.execute(
        """SELECT COALESCE(SUM(quantity_on_hand), 0) AS q FROM inventory
           WHERE tenant_id = %s AND product_id = %s AND warehouse_id = %s""",
        (tenant_id, product_id, warehouse_id),
    )
    return float(db_read.fetchone()["q"])


@pytest.fixture(scope="module")
def warehouse_id(api_client, db_read):
    db_read.execute(
        """SELECT id FROM warehouses
           WHERE tenant_id = %s AND deleted_at IS NULL AND is_active = true
           ORDER BY is_default DESC, created_at ASC LIMIT 1""",
        (api_client.tenant_id,),
    )
    row = db_read.fetchone()
    if not row:
        pytest.skip("No warehouse in dev DB")
    return str(row["id"])


@pytest.fixture(scope="module", autouse=True)
def _cleanup(db_read):
    yield
    db_read.execute(
        "UPDATE construction_projects SET deleted_at = NOW() "
        "WHERE deleted_at IS NULL AND code LIKE %s",
        (f"{CODE_PREFIX}%",),
    )


# ═══════════════════════════════════════════════════════════════════════════
class TestFullIssueFlow:
    """3.1-stsenariy: skladda bor — zayavka → chiqim → qabul."""

    def test_full_flow(self, api_client, db_read, warehouse_id):
        project_id = _make_project(api_client, "FULL")
        p1 = _make_product(api_client, f"F1-{uuid.uuid4().hex[:5]}")
        p2 = _make_product(api_client, f"F2-{uuid.uuid4().hex[:5]}")
        _add_stock(api_client, p1, warehouse_id, 25)
        _add_stock(api_client, p2, warehouse_id, 25)

        created = _create_request(
            api_client, project_id,
            items=[{"product_id": p1, "qty": 10}, {"product_id": p2, "qty": 5}],
            priority="urgent", required_date="2026-08-20", warehouse_id=warehouse_id,
        )
        req_id = created["id"]
        number = created["request_number"]
        assert created["status"] == "new"
        assert number.startswith("MZ-2026-")

        detail = _get_request(api_client, req_id)
        assert len(detail["items"]) == 2
        assert detail["priority"] == "urgent"
        # Jonli qoldiq detalda ko'rinadi
        by_pid = {it["product_id"]: it for it in detail["items"]}
        assert by_pid[p1]["on_hand"] >= 10
        # Timeline'da 'created'
        assert any(ev["action_type"] == "created" for ev in detail["timeline"])

        # Omborchi ochdi → in_review
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/review")
        assert r.status_code == 200, r.text
        assert r.json()["data"]["status"] == "in_review"

        # To'liq chiqim (lines bo'sh = hammasi)
        before1 = _on_hand(db_read, api_client.tenant_id, p1, warehouse_id)
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/issue", json={})
        assert r.status_code == 200, r.text
        issue_data = r.json()["data"]
        assert issue_data["status"] == "issued"
        op_id = issue_data["stock_operation_id"]
        assert op_id

        # Stok kamaydi
        after1 = _on_hand(db_read, api_client.tenant_id, p1, warehouse_id)
        assert abs(before1 - after1 - 10) < 1e-6

        # Chiqim hujjati: done, zayavkaga bog'langan
        db_read.execute(
            "SELECT state, material_request_id, source_type FROM stock_operations WHERE id = %s",
            (op_id,),
        )
        op = db_read.fetchone()
        assert op["state"] == "done"
        assert op["material_request_id"] == req_id
        assert op["source_type"] == "material_request"

        # JE: balansli, Dr 0810 (dev DB BHMS chartida 0810 bor)
        je_id = issue_data.get("journal_entry_id")
        assert je_id, "issue JE expected on BHMS dev chart"
        db_read.execute(
            """SELECT je.status, je.total_debit, je.total_credit, je.entry_number,
                      (SELECT COALESCE(SUM(l.debit_amount), 0) FROM journal_entry_lines l
                        WHERE l.journal_entry_id = je.id) AS dr,
                      (SELECT COALESCE(SUM(l.credit_amount), 0) FROM journal_entry_lines l
                        WHERE l.journal_entry_id = je.id) AS cr
               FROM journal_entries je WHERE je.id = %s""",
            (je_id,),
        )
        je = db_read.fetchone()
        assert je["status"] == "posted"
        assert je["entry_number"].startswith("MZC")
        assert abs(float(je["dr"]) - float(je["cr"])) < 0.01
        db_read.execute(
            """SELECT a.code FROM journal_entry_lines l
               JOIN accounts a ON a.id = l.account_id
               WHERE l.journal_entry_id = %s AND l.debit_amount > 0""",
            (je_id,),
        )
        debit_codes = {r["code"] for r in db_read.fetchall()}
        assert any(c.startswith("08") for c in debit_codes), debit_codes

        # Loyiha xarajat qatorlari (CEL) — material_request_id bilan, approved
        db_read.execute(
            """SELECT COUNT(*) AS n FROM construction_expense_lines
               WHERE material_request_id = %s AND status = 'approved' AND deleted_at IS NULL""",
            (req_id,),
        )
        assert db_read.fetchone()["n"] == 2

        # Item agregatlari
        detail = _get_request(api_client, req_id)
        assert all(it["line_status"] == "issued" for it in detail["items"])
        assert len(detail["issues"]) == 1

        # Prorab qabul qildi → closed
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/accept")
        assert r.status_code == 200, r.text
        assert r.json()["data"]["status"] == "closed"
        db_read.execute(
            "SELECT status, closed_at FROM construction_material_requests WHERE id = %s",
            (req_id,),
        )
        row = db_read.fetchone()
        assert row["status"] == "closed" and row["closed_at"] is not None


# ═══════════════════════════════════════════════════════════════════════════
class TestPurchaseFlow:
    """3.2-stsenariy: skladda yo'q — xarid → kirim → avto-signal → chiqim."""

    def test_purchase_loop(self, api_client, db_read, warehouse_id, test_supplier):
        project_id = _make_project(api_client, "PUR")
        p = _make_product(api_client, f"P-{uuid.uuid4().hex[:5]}", cost=40000)

        created = _create_request(
            api_client, project_id,
            items=[{"product_id": p, "qty": 7}],
            warehouse_id=warehouse_id,
        )
        req_id = created["id"]
        number = created["request_number"]

        # Xaridga yuborish
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/send-to-purchase", json={})
        assert r.status_code == 200, r.text
        data = r.json()["data"]
        assert data["status"] == "in_purchase"
        pr_id = data["purchase_requisition_id"]
        pr_number = data["pr_number"]

        # PR: approved, backlink, purpose'da MZ raqami, qator bahosi cost_price'dan
        db_read.execute(
            """SELECT status, material_request_id, purpose, total_amount, organization_id
               FROM purchase_requisitions WHERE id = %s""",
            (pr_id,),
        )
        pr = db_read.fetchone()
        assert pr["status"] == "approved"
        assert pr["material_request_id"] == req_id
        assert number in (pr["purpose"] or "")
        assert float(pr["total_amount"]) == pytest.approx(7 * 40000)

        # Item: qty_in_purchase
        detail = _get_request(api_client, req_id)
        assert detail["items"][0]["line_status"] == "in_purchase"
        assert float(detail["items"][0]["qty_in_purchase"]) == pytest.approx(7)
        assert len(detail["purchases"]) == 1
        assert detail["purchases"][0]["pr_number"] == pr_number

        # Ta'minotchi: PR → PO
        r = api_client.post(f"/purchase-requisitions/{pr_id}/convert-to-po",
                            json={"supplier_id": str(test_supplier["id"])})
        assert r.status_code == 200, r.text
        po_id = r.json()["data"]["id"]

        # 470-fix: PR'dan yaratilgan PO organization_id bilan yoziladi
        db_read.execute(
            "SELECT organization_id, status FROM purchase_orders WHERE id = %s", (po_id,)
        )
        po = db_read.fetchone()
        assert po["organization_id"] is not None, "ConvertPRToPO must set organization_id"

        # PO tasdiqlab qabul qilinadi (approve → receive)
        r = api_client.post(f"/purchase-orders/{po_id}/approve")
        assert r.status_code == 200, r.text
        db_read.execute(
            "SELECT id FROM purchase_order_lines WHERE purchase_order_id = %s", (po_id,)
        )
        po_lines = db_read.fetchall()
        assert po_lines, "PO lines expected (ConvertPRToPO must copy PR lines)"
        r = api_client.post(f"/purchase-orders/{po_id}/receive", json={
            "lines": [{"line_id": str(po_lines[0]["id"]), "quantity_received": 7}],
        })
        assert r.status_code == 200, r.text

        # Yopiq halqa: 'material_arrived' timeline yozuvi (async goroutine —
        # qisqa retry bilan kutamiz)
        arrived = False
        for _ in range(10):
            detail = _get_request(api_client, req_id)
            if any(ev["action_type"] == "material_arrived" for ev in detail["timeline"]):
                arrived = True
                break
            time.sleep(0.5)
        assert arrived, "material_arrived activity expected after PO receipt"

        # Endi omborda bor — chiqim (kirim qaysi omborga tushganini aniqlaymiz)
        db_read.execute(
            """SELECT warehouse_id FROM inventory
               WHERE tenant_id = %s AND product_id = %s AND quantity_on_hand > 0
               ORDER BY quantity_on_hand DESC LIMIT 1""",
            (api_client.tenant_id, p),
        )
        stocked_wh = db_read.fetchone()
        assert stocked_wh, "PO receipt must have stocked the product"

        r = api_client.post(f"/construction/material-requests-v2/{req_id}/issue", json={})
        assert r.status_code == 200, r.text
        assert r.json()["data"]["status"] == "issued"

        # Chiqimdan keyin qty_in_purchase nolga qaytadi
        detail = _get_request(api_client, req_id)
        assert float(detail["items"][0]["qty_in_purchase"]) == pytest.approx(0)
        assert detail["items"][0]["line_status"] == "issued"


# ═══════════════════════════════════════════════════════════════════════════
class TestPartialFlow:
    """3.3-stsenariy: qisman ta'minlash — qator darajasида ajratish."""

    def test_partial(self, api_client, db_read, warehouse_id):
        project_id = _make_project(api_client, "PART")
        p_have = _make_product(api_client, f"H-{uuid.uuid4().hex[:5]}")
        p_none = _make_product(api_client, f"N-{uuid.uuid4().hex[:5]}")
        _add_stock(api_client, p_have, warehouse_id, 50)

        created = _create_request(
            api_client, project_id,
            items=[{"product_id": p_have, "qty": 20}, {"product_id": p_none, "qty": 8}],
            warehouse_id=warehouse_id,
        )
        req_id = created["id"]
        detail = _get_request(api_client, req_id)
        items = {it["product_id"]: it for it in detail["items"]}

        # Bor qatorni chiqarish
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/issue", json={
            "lines": [{"item_id": items[p_have]["id"], "qty": 20}],
        })
        assert r.status_code == 200, r.text
        assert r.json()["data"]["status"] == "partially_fulfilled"

        # Yo'q qatorni xaridga
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/send-to-purchase", json={
            "lines": [{"item_id": items[p_none]["id"], "qty": 8}],
        })
        assert r.status_code == 200, r.text
        assert r.json()["data"]["status"] == "partially_fulfilled"

        detail = _get_request(api_client, req_id)
        items = {it["product_id"]: it for it in detail["items"]}
        assert items[p_have]["line_status"] == "issued"
        assert items[p_none]["line_status"] == "in_purchase"
        # «So'ralgan / Chiqarilgan / Xaridda» miqdorlar
        assert float(items[p_have]["qty_issued"]) == pytest.approx(20)
        assert float(items[p_none]["qty_in_purchase"]) == pytest.approx(8)


# ═══════════════════════════════════════════════════════════════════════════
class TestRejectCancelStateMachine:
    """3.4/3.5 + state-machine 4xx guardlari."""

    def test_reject_requires_reason(self, api_client, warehouse_id):
        project_id = _make_project(api_client, "REJ1")
        p = _make_product(api_client, f"R1-{uuid.uuid4().hex[:5]}")
        created = _create_request(api_client, project_id,
                                  items=[{"product_id": p, "qty": 3}],
                                  warehouse_id=warehouse_id)
        r = api_client.post(f"/construction/material-requests-v2/{created['id']}/reject", json={})
        assert r.status_code == 400
        assert "MR_REASON_REQUIRED" in r.text

    def test_reject_with_reason(self, api_client, db_read, warehouse_id):
        project_id = _make_project(api_client, "REJ2")
        p = _make_product(api_client, f"R2-{uuid.uuid4().hex[:5]}")
        created = _create_request(api_client, project_id,
                                  items=[{"product_id": p, "qty": 3}],
                                  warehouse_id=warehouse_id)
        req_id = created["id"]
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/reject",
                            json={"reason": "Smeta limitidan ortiq"})
        assert r.status_code == 200, r.text
        assert r.json()["data"]["status"] == "rejected"
        detail = _get_request(api_client, req_id)
        assert detail["rejected_reason"] == "Smeta limitidan ortiq"
        assert all(it["line_status"] == "rejected" for it in detail["items"])
        # Rad etilgandan keyin chiqim yo'q
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/issue", json={})
        assert r.status_code == 400

    def test_cancel_only_from_new(self, api_client, warehouse_id):
        project_id = _make_project(api_client, "CAN")
        p = _make_product(api_client, f"C-{uuid.uuid4().hex[:5]}")
        created = _create_request(api_client, project_id,
                                  items=[{"product_id": p, "qty": 2}],
                                  warehouse_id=warehouse_id)
        req_id = created["id"]
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/cancel")
        assert r.status_code == 200, r.text
        assert r.json()["data"]["status"] == "cancelled"
        # Ikkinchi marta bekor qilish — 400
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/cancel")
        assert r.status_code == 400
        # Bekor qilinganidan chiqim — 400
        r = api_client.post(f"/construction/material-requests-v2/{req_id}/issue", json={})
        assert r.status_code == 400

    def test_accept_only_from_issued(self, api_client, warehouse_id):
        project_id = _make_project(api_client, "ACC")
        p = _make_product(api_client, f"A-{uuid.uuid4().hex[:5]}")
        created = _create_request(api_client, project_id,
                                  items=[{"product_id": p, "qty": 2}],
                                  warehouse_id=warehouse_id)
        r = api_client.post(f"/construction/material-requests-v2/{created['id']}/accept")
        assert r.status_code == 400
        assert "MR_INVALID_TRANSITION" in r.text

    def test_insufficient_stock_422(self, api_client, warehouse_id):
        project_id = _make_project(api_client, "INS")
        p = _make_product(api_client, f"I-{uuid.uuid4().hex[:5]}")
        created = _create_request(api_client, project_id,
                                  items=[{"product_id": p, "qty": 999999}],
                                  warehouse_id=warehouse_id)
        r = api_client.post(f"/construction/material-requests-v2/{created['id']}/issue", json={})
        assert r.status_code == 422, r.text
        body = r.json()
        assert body["error"]["code"] == "INSUFFICIENT_STOCK"
        assert body.get("errors"), "per-line errors expected"


# ═══════════════════════════════════════════════════════════════════════════
class TestStatsStockCheckLegacy:
    def test_stats_contract(self, api_client, warehouse_id):
        # Kamida bitta ochiq zayavka bo'lsin
        project_id = _make_project(api_client, "ST")
        p = _make_product(api_client, f"S-{uuid.uuid4().hex[:5]}")
        _create_request(api_client, project_id,
                        items=[{"product_id": p, "qty": 1}],
                        warehouse_id=warehouse_id)
        r = api_client.get("/construction/material-requests-v2/stats")
        assert r.status_code == 200, r.text
        data = r.json()["data"]
        for key in ("open_count", "in_purchase_count", "overdue_count",
                    "closed_this_month", "urgent_open", "inbox_count",
                    "avg_fulfillment_hours", "purchase_sum"):
            assert key in data, f"stats missing {key}"
        assert data["open_count"] >= 1

    def test_stock_check(self, api_client, warehouse_id):
        p = _make_product(api_client, f"SC-{uuid.uuid4().hex[:5]}")
        _add_stock(api_client, p, warehouse_id, 13)
        r = api_client.get("/construction/material-requests-v2/stock-check",
                           params={"product_ids": p, "warehouse_id": warehouse_id})
        assert r.status_code == 200, r.text
        items = r.json()["data"]["items"]
        assert len(items) == 1
        assert float(items[0]["on_hand"]) == pytest.approx(13)

    def test_list_filters(self, api_client, warehouse_id):
        r = api_client.get("/construction/material-requests-v2", params={"open": "true"})
        assert r.status_code == 200, r.text
        rows = r.json()["data"]
        assert all(row["status"] not in ("closed", "cancelled", "rejected") for row in rows)

    def test_legacy_endpoints_guard_v2(self, api_client, warehouse_id):
        project_id = _make_project(api_client, "LEG")
        p = _make_product(api_client, f"L-{uuid.uuid4().hex[:5]}")
        created = _create_request(api_client, project_id,
                                  items=[{"product_id": p, "qty": 4}],
                                  warehouse_id=warehouse_id)
        req_id = created["id"]
        # Legacy PUT — blok
        r = api_client.put(f"/construction/material-requests/{req_id}",
                           json={"notes": "legacy hack"})
        assert r.status_code == 400, r.text
        # Legacy approve — blok (qo'lda stok kamaytirib ikkilangan xarajat yozardi)
        r = api_client.put(f"/construction/material-requests/{req_id}/approve")
        assert r.status_code == 400, r.text
        # Legacy delete — blok
        r = api_client.delete(f"/construction/material-requests/{req_id}")
        assert r.status_code == 400, r.text
