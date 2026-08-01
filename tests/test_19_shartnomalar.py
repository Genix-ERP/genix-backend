"""
Genix ERP - Shartnomalar (Contracts v2) moduli testlari

Qamrov (docs/shartnomalar-audit.md §9 sifat talablari):
  1. Tenant izolyatsiyasi — begona tenant shartnomani ko'ra/o'zgartira olmaydi
  2. Status o'tish qoidalari server tomonda (draft→completed taqiqlangan,
     PUT orqali status o'zgartirib bo'lmaydi, DB CHECK backstop)
  3. effective_amount = value + Σ ilova (amendment) deltalari
  4. To'langan/qoldiq rollup — biriktirilgan hisob-fakturalardan
  5. Muddati tugash bildirishnomasi threshold dedupe (unique constraint)
  6. Permission — autentifikatsiyasiz so'rov rad etiladi
  7. AI ekstraksiya endpointi ma'lumot o'zgartirmasdan taklif qaytaradi
"""
import io
import uuid
from datetime import date, timedelta

import pytest
import requests

from conftest import BASE_URL, APIClient


# ============================================
# HELPERS / FIXTURES
# ============================================

def _iso(d):
    return d.strftime("%Y-%m-%d")


@pytest.fixture(scope="module")
def counterparty(db_read, auth_token):
    """Any live contact of the test tenant."""
    db_read.execute(
        "SELECT id, name FROM contacts WHERE tenant_id = %s AND deleted_at IS NULL LIMIT 1",
        (auth_token["tenant_id"],),
    )
    row = db_read.fetchone()
    if not row:
        pytest.skip("No contacts in test tenant")
    return dict(row)


@pytest.fixture()
def contract(api_client, counterparty):
    """Fresh draft contract; force-cleaned via DB-independent delete path."""
    resp = api_client.post("/contracts", json={
        "title": f"Test shartnoma {uuid.uuid4().hex[:6]}",
        "vendor_id": str(counterparty["id"]),
        "direction": "income",
        "start_date": _iso(date.today()),
        "end_date": _iso(date.today() + timedelta(days=90)),
        "value": 1_000_000,
    })
    assert resp.status_code in (200, 201), resp.text
    data = resp.json()["data"]
    yield data
    # draft/cancelled delete is allowed; ignore failures for non-deletable states
    api_client.delete(f"/contracts/{data['id']}")


@pytest.fixture(scope="module")
def foreign_client(auth_token):
    """Same token, spoofed X-Tenant-ID — handlers must scope by tenant."""
    return APIClient(
        base_url=BASE_URL,
        token=auth_token["token"],
        tenant_id=str(uuid.uuid4()),
    )


def _multipart_headers(api_client):
    """Auth headers without the JSON content type (requests sets multipart)."""
    h = {}
    if api_client.token:
        h["Authorization"] = f"Bearer {api_client.token}"
    if api_client.tenant_id:
        h["X-Tenant-ID"] = api_client.tenant_id
    if api_client.org_id:
        h["X-Organization-ID"] = api_client.org_id
    return h


# ============================================
# 1. CRUD asoslari
# ============================================

class TestContractBasics:
    def test_create_returns_autonumber_and_draft(self, contract):
        assert contract["contract_number"].startswith("CNT-")
        assert contract["status"] == "draft"
        assert contract["effective_amount"] == contract["value"]
        assert "allowed_transitions" in contract

    def test_next_number_endpoint(self, api_client):
        resp = api_client.get("/contracts/next-number")
        assert resp.status_code == 200, resp.text
        num = resp.json()["data"]["contract_number"]
        assert num.startswith(f"CNT-{date.today().year}-")

    def test_stats_endpoint(self, api_client, contract):
        resp = api_client.get("/contracts/stats")
        assert resp.status_code == 200, resp.text
        stats = resp.json()["data"]
        for key in ("total", "active", "expiring_soon", "active_total_value", "outstanding"):
            assert key in stats

    def test_update_edits_counterparty_and_dates(self, api_client, contract, counterparty):
        """The pre-rebuild UpdateContractInput silently dropped vendor_id and
        start_date — this pins the fix."""
        new_start = _iso(date.today() - timedelta(days=5))
        resp = api_client.put(f"/contracts/{contract['id']}", json={
            "start_date": new_start,
            "vendor_id": str(counterparty["id"]),
            "value": 2_000_000,
        })
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert data["value"] == 2_000_000
        assert data["start_date"][:10] == new_start

    def test_archive_hides_from_default_list(self, api_client, contract):
        resp = api_client.post(f"/contracts/{contract['id']}/archive")
        assert resp.status_code == 200, resp.text
        assert resp.json()["data"]["archived_at"]

        listed = api_client.get("/contracts", params={"search": contract["contract_number"]})
        ids = [c["id"] for c in listed.json()["data"]]
        assert contract["id"] not in ids

        archived = api_client.get("/contracts", params={"archived": "true", "search": contract["contract_number"]})
        ids = [c["id"] for c in archived.json()["data"]]
        assert contract["id"] in ids

        resp = api_client.post(f"/contracts/{contract['id']}/unarchive")
        assert resp.status_code == 200, resp.text


# ============================================
# 2. Tenant izolyatsiyasi
# ============================================

class TestTenantIsolation:
    def test_foreign_tenant_cannot_read(self, contract, foreign_client):
        resp = foreign_client.get(f"/contracts/{contract['id']}")
        assert resp.status_code in (403, 404), resp.text

    def test_foreign_tenant_cannot_update(self, contract, foreign_client):
        resp = foreign_client.put(f"/contracts/{contract['id']}", json={"title": "hacked"})
        assert resp.status_code in (403, 404), resp.text

    def test_foreign_tenant_cannot_transition(self, contract, foreign_client):
        resp = foreign_client.post(f"/contracts/{contract['id']}/status", json={"status": "active"})
        assert resp.status_code in (403, 404), resp.text

    def test_foreign_tenant_list_excludes(self, contract, foreign_client):
        resp = foreign_client.get("/contracts", params={"search": contract["contract_number"]})
        if resp.status_code == 200:
            ids = [c["id"] for c in resp.json().get("data") or []]
            assert contract["id"] not in ids

    def test_unauthenticated_request_rejected(self):
        resp = requests.get(f"{BASE_URL}/contracts")
        assert resp.status_code in (401, 403)


# ============================================
# 3. Status o'tish qoidalari
# ============================================

class TestStatusTransitions:
    def test_draft_cannot_jump_to_completed(self, api_client, contract):
        resp = api_client.post(f"/contracts/{contract['id']}/status", json={"status": "completed"})
        assert resp.status_code == 400, resp.text

    def test_unknown_status_rejected(self, api_client, contract):
        resp = api_client.post(f"/contracts/{contract['id']}/status", json={"status": "renewed"})
        assert resp.status_code == 400, resp.text

    def test_lifecycle_walk(self, api_client, contract):
        # draft → negotiation → signing → active → completed
        for target in ("negotiation", "signing", "active"):
            resp = api_client.post(f"/contracts/{contract['id']}/status", json={"status": target})
            assert resp.status_code == 200, f"{target}: {resp.text}"
            assert resp.json()["data"]["status"] == target

        # entering active stamps signed_date
        assert resp.json()["data"]["signed_date"]

        # active → draft is not allowed
        resp = api_client.post(f"/contracts/{contract['id']}/status", json={"status": "draft"})
        assert resp.status_code == 400, resp.text

        resp = api_client.post(f"/contracts/{contract['id']}/status", json={"status": "completed"})
        assert resp.status_code == 200, resp.text

        # completed is terminal
        resp = api_client.post(f"/contracts/{contract['id']}/status", json={"status": "active"})
        assert resp.status_code == 400, resp.text

    def test_put_cannot_change_status(self, api_client, contract):
        """PUT used to accept any raw status string — the transition endpoint
        is now the only path."""
        resp = api_client.put(f"/contracts/{contract['id']}", json={"status": "completed", "title": "still draft"})
        assert resp.status_code == 200, resp.text
        assert resp.json()["data"]["status"] == "draft"

    def test_db_check_constraint_backstop(self, db_session, tenant_id):
        """Migration 443 adds a CHECK on procurement_contracts.status."""
        db_session.execute(
            "SELECT id FROM procurement_contracts WHERE tenant_id = %s LIMIT 1",
            (tenant_id,),
        )
        row = db_session.fetchone()
        if not row:
            pytest.skip("No contracts to test the constraint against")
        with pytest.raises(Exception) as exc:
            db_session.execute(
                "UPDATE procurement_contracts SET status = 'bogus' WHERE id = %s",
                (row["id"],),
            )
        assert "chk_procurement_contracts_status" in str(exc.value)


# ============================================
# 4. Ilovalar (amendments) va effective_amount
# ============================================

class TestAmendments:
    def test_amendment_changes_effective_amount(self, api_client, contract):
        resp = requests.post(
            f"{BASE_URL}/contracts/{contract['id']}/amendments",
            data={
                "number": "Ilova 1",
                "date": _iso(date.today()),
                "amount_delta": "250000",
                "description": "Qo'shimcha ishlar",
            },
            headers=_multipart_headers(api_client),
        )
        assert resp.status_code in (200, 201), resp.text
        amendment_id = resp.json()["data"]["id"]

        detail = api_client.get(f"/contracts/{contract['id']}").json()["data"]
        assert detail["effective_amount"] == detail["value"] + 250000
        assert detail["amendment_count"] == 1

        # duplicate number rejected
        resp = requests.post(
            f"{BASE_URL}/contracts/{contract['id']}/amendments",
            data={"number": "Ilova 1", "date": _iso(date.today())},
            headers=_multipart_headers(api_client),
        )
        assert resp.status_code == 409, resp.text

        # delete restores the base amount
        resp = api_client.delete(f"/contracts/{contract['id']}/amendments/{amendment_id}")
        assert resp.status_code == 200, resp.text
        detail = api_client.get(f"/contracts/{contract['id']}").json()["data"]
        assert detail["effective_amount"] == detail["value"]


# ============================================
# 5. To'lovlar rollup
# ============================================

class TestPaymentsRollup:
    def test_attach_invoice_feeds_rollup(self, api_client, db_read, tenant_id, contract):
        db_read.execute(
            """
            SELECT id, COALESCE(amount_paid, 0) AS paid, COALESCE(total_amount, 0) AS total
            FROM sales_invoices
            WHERE tenant_id = %s AND deleted_at IS NULL AND contract_id IS NULL
              AND status <> 'cancelled' AND COALESCE(amount_paid, 0) > 0
            LIMIT 1
            """,
            (tenant_id,),
        )
        inv = db_read.fetchone()
        if not inv:
            pytest.skip("No paid, unlinked sales invoice available for rollup test")

        resp = api_client.post(f"/contracts/{contract['id']}/invoices", json={
            "invoice_id": str(inv["id"]), "kind": "sales",
        })
        assert resp.status_code == 200, resp.text

        try:
            rollup = api_client.get(f"/contracts/{contract['id']}/invoices").json()["data"]
            assert any(str(r["id"]) == str(inv["id"]) for r in rollup["invoices"])
            assert rollup["paid_total"] == pytest.approx(float(inv["paid"]))
            detail = api_client.get(f"/contracts/{contract['id']}").json()["data"]
            assert detail["paid_total"] == pytest.approx(float(inv["paid"]))
            assert detail["outstanding"] == pytest.approx(detail["effective_amount"] - float(inv["paid"]))
        finally:
            api_client.post(f"/contracts/{contract['id']}/invoices", json={
                "invoice_id": str(inv["id"]), "kind": "sales", "detach": True,
            })


# ============================================
# 6. Muddati tugash bildirishnomasi dedupe
# ============================================

class TestExpiryNotificationDedupe:
    def test_threshold_unique_constraint(self, db_session, tenant_id):
        db_session.execute(
            "SELECT id FROM procurement_contracts WHERE tenant_id = %s AND deleted_at IS NULL LIMIT 1",
            (tenant_id,),
        )
        row = db_session.fetchone()
        if not row:
            pytest.skip("No contracts available")
        ins = """
            INSERT INTO contract_expiry_notifications (id, tenant_id, contract_id, threshold_days)
            VALUES (%s, %s, %s, 30)
            ON CONFLICT (contract_id, threshold_days) DO NOTHING
        """
        db_session.execute(ins, (str(uuid.uuid4()), tenant_id, row["id"]))
        first = db_session.rowcount
        db_session.execute(ins, (str(uuid.uuid4()), tenant_id, row["id"]))
        second = db_session.rowcount
        assert first == 1
        assert second == 0  # one notification per contract per threshold


# ============================================
# 7. AI ekstraksiya — mutatsiyasiz takliflar
# ============================================

class TestAIExtraction:
    def test_extract_returns_suggestions_without_mutation(self, api_client, db_read, tenant_id):
        db_read.execute(
            "SELECT COUNT(*) AS n FROM procurement_contracts WHERE tenant_id = %s", (tenant_id,)
        )
        before = db_read.fetchone()["n"]

        body = (
            "SHARTNOMA N 77-2026\n"
            "Toshkent sh. 2026-05-01\n"
            "Buyurtmachi: Test Invest MChJ\n"
            "Shartnoma summasi: 150 000 000 so'm\n"
            "Amal qilish muddati: 2026-05-01 dan 2027-05-01 gacha\n"
        ).encode("utf-8")
        resp = requests.post(
            f"{BASE_URL}/contracts/ai/extract",
            files={"file": ("shartnoma.txt", io.BytesIO(body), "text/plain")},
            headers=_multipart_headers(api_client),
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert data["file_id"]
        assert "suggestions" in data  # null when AI is not configured — still no error

        db_read.execute(
            "SELECT COUNT(*) AS n FROM procurement_contracts WHERE tenant_id = %s", (tenant_id,)
        )
        after = db_read.fetchone()["n"]
        assert after == before  # suggestions never auto-create a contract
