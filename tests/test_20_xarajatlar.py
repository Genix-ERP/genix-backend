"""
Genix ERP - Xarajatlar (Expenses v2) moduli testlari

Qamrov (docs/xarajatlar-audit.md sifat talablari):
  1. Tenant izolyatsiyasi — begona tenant xarajatni ko'ra/o'zgartira olmaydi
  2. Status o'tish qoidalari server tomonda:
     draft → submitted → approved → paid (+ rejected, sabab majburiy);
     PUT orqali status o'zgartirib bo'lmaydi
  3. Kategoriya majburiy — kategoriya yo'q xarajat yaratil olmaydi
     (donutning "bitta ko'k doira" bo'lishining ildizi shu edi)
  4. To'lov haqiqiy provodka yozadi: POST /pay → posted, balanslangan,
     2 qatorli JE (Dt kategoriya schyoti/9410, Kt kassa/bank)
  5. /expenses/stats — butun tenant bo'yicha to'g'ri agregatlar
  6. Permission — autentifikatsiyasiz so'rov rad etiladi
  7. Default kategoriyalar seed qilingan (migration 444 / lazy seed)
  8. Workflow katalogida expenses.* eventlari bor
"""
import uuid
from datetime import date, timedelta
import random

import pytest
import requests

from conftest import BASE_URL, APIClient


def _iso(d):
    return d.strftime("%Y-%m-%d")


# ============================================
# FIXTURES
# ============================================

@pytest.fixture(scope="module")
def categories(api_client):
    resp = api_client.get("/expense-categories")
    assert resp.status_code == 200, resp.text
    cats = resp.json()["data"]
    assert isinstance(cats, list) and len(cats) > 0, "expense categories must be seeded"
    return cats


@pytest.fixture(scope="module")
def category(categories):
    by_code = {c.get("code"): c for c in categories}
    return by_code.get("TRANSPORT") or categories[0]


@pytest.fixture(scope="module")
def employee(db_read, auth_token):
    db_read.execute(
        "SELECT id, first_name, last_name FROM employees WHERE tenant_id = %s LIMIT 1",
        (auth_token["tenant_id"],),
    )
    row = db_read.fetchone()
    if not row:
        pytest.skip("No employees in test tenant")
    return dict(row)


def _mk_expense(api_client, category, employee, **overrides):
    body = {
        "date": _iso(date.today()),
        "description": f"Test xarajat {uuid.uuid4().hex[:6]}",
        "amount": 1000,
        "category_id": str(category["id"]),
        "employee_id": str(employee["id"]),
    }
    body.update(overrides)
    return api_client.post("/expenses", json=body)


@pytest.fixture()
def expense(api_client, category, employee):
    """Fresh submitted expense (create default)."""
    resp = _mk_expense(api_client, category, employee)
    assert resp.status_code in (200, 201), resp.text
    data = resp.json()["data"]
    yield data
    # Best-effort cleanup: reject (if still submitted) then delete.
    api_client.post(f"/expenses/{data['id']}/reject", json={"reason": "test cleanup"})
    api_client.delete(f"/expenses/{data['id']}")


@pytest.fixture(scope="module")
def foreign_client(auth_token):
    """Same token, spoofed X-Tenant-ID — handlers must scope by tenant."""
    return APIClient(
        base_url=BASE_URL,
        token=auth_token["token"],
        tenant_id=str(uuid.uuid4()),
    )


# ============================================
# 1. Asosiy CRUD va validatsiya
# ============================================

class TestExpenseBasics:
    def test_create_defaults_to_submitted_with_number(self, expense):
        assert expense["expense_number"].startswith(f"EXP-{date.today().year}-")
        assert expense["status"] == "submitted"
        assert expense["submitted_at"] is not None

    def test_create_populates_category_and_employee(self, expense, category, employee):
        # "category" must ALWAYS be serialized (the old omitempty made the
        # key vanish → frontend faked 'Boshqa' → one-slice donut)
        assert expense["category"] == category["name"]
        full_name = f"{employee['first_name']} {employee['last_name']}".strip()
        assert expense["employee_name"] == full_name
        assert expense["employee_id"] == str(employee["id"])

    def test_category_is_required(self, api_client, category, employee):
        resp = _mk_expense(api_client, category, employee, category_id="")
        assert resp.status_code == 400
        assert "category" in resp.text.lower()

    def test_foreign_category_rejected(self, api_client, category, employee):
        resp = _mk_expense(api_client, category, employee, category_id=str(uuid.uuid4()))
        assert resp.status_code == 400

    def test_create_draft(self, api_client, category, employee):
        resp = _mk_expense(api_client, category, employee, status="draft")
        assert resp.status_code in (200, 201), resp.text
        data = resp.json()["data"]
        assert data["status"] == "draft"
        assert data.get("submitted_at") is None
        api_client.delete(f"/expenses/{data['id']}")

    def test_put_cannot_flip_status(self, api_client, expense):
        resp = api_client.put(f"/expenses/{expense['id']}", json={
            "status": "paid",
            "description": "status flip attempt",
        })
        assert resp.status_code == 200, resp.text
        assert resp.json()["data"]["status"] == "submitted"

    def test_list_always_serializes_category_key(self, api_client, expense):
        resp = api_client.get("/expenses", params={"limit": 5})
        assert resp.status_code == 200
        items = resp.json()["data"]
        assert len(items) > 0
        for item in items:
            assert "category" in item
            assert "employee_name" in item


# ============================================
# 2. Lifecycle o'tish qoidalari
# ============================================

class TestLifecycle:
    def test_cannot_pay_submitted(self, api_client, expense):
        resp = api_client.post(f"/expenses/{expense['id']}/pay", json={})
        assert resp.status_code == 400
        assert "approved" in resp.text

    def test_approve_then_double_approve_fails(self, api_client, expense):
        resp = api_client.post(f"/expenses/{expense['id']}/approve")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert data["status"] == "approved"
        assert data["approved_at"] is not None

        again = api_client.post(f"/expenses/{expense['id']}/approve")
        assert again.status_code == 400

    def test_reject_requires_reason(self, api_client, expense):
        no_reason = api_client.post(f"/expenses/{expense['id']}/reject", json={})
        assert no_reason.status_code == 400

        resp = api_client.post(f"/expenses/{expense['id']}/reject", json={"reason": "Chek yo'q"})
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert data["status"] == "rejected"
        assert data["rejection_reason"] == "Chek yo'q"

        # rejected → resubmit is allowed and clears the rejection fields
        resubmit = api_client.post(f"/expenses/{expense['id']}/submit")
        assert resubmit.status_code == 200, resubmit.text
        assert resubmit.json()["data"]["status"] == "submitted"

    def test_draft_submit_flow(self, api_client, category, employee):
        resp = _mk_expense(api_client, category, employee, status="draft")
        data = resp.json()["data"]
        try:
            submit = api_client.post(f"/expenses/{data['id']}/submit")
            assert submit.status_code == 200, submit.text
            assert submit.json()["data"]["status"] == "submitted"
            # submitted expenses are not deletable
            delete = api_client.delete(f"/expenses/{data['id']}")
            assert delete.status_code == 400
        finally:
            api_client.post(f"/expenses/{data['id']}/reject", json={"reason": "cleanup"})
            api_client.delete(f"/expenses/{data['id']}")

    def test_approved_not_deletable_or_editable(self, api_client, expense):
        api_client.post(f"/expenses/{expense['id']}/approve")
        delete = api_client.delete(f"/expenses/{expense['id']}")
        assert delete.status_code == 400
        edit = api_client.put(f"/expenses/{expense['id']}", json={"description": "nope"})
        assert edit.status_code == 400


# ============================================
# 3. To'lov → haqiqiy provodka (GL)
# ============================================

class TestPayPostsJournalEntry:
    @pytest.fixture(scope="class")
    def kassa(self, db_read, auth_token):
        # Any money account (kassa 5010 / bank 51xx) with enough balance —
        # PayExpense's outflow guard rejects when current_balance < amount.
        # (The dev DB's 5010 sits at a NEGATIVE balance: the legacy
        # approve-time posting credited cash 18 times with no real inflow.)
        db_read.execute(
            """SELECT id, code, current_balance FROM accounts
               WHERE tenant_id = %s AND (code LIKE '50%%' OR code LIKE '51%%')
                 AND COALESCE(is_leaf, true) = true AND deleted_at IS NULL
                 AND COALESCE(current_balance, 0) >= 1000
               ORDER BY code LIMIT 1""",
            (auth_token["tenant_id"],),
        )
        row = db_read.fetchone()
        if not row:
            pytest.skip("No money account with sufficient balance in test tenant")
        return dict(row)

    def test_pay_flow_posts_balanced_je(self, api_client, db_read, category, employee, kassa):
        created = _mk_expense(api_client, category, employee, amount=1000).json()["data"]
        approve = api_client.post(f"/expenses/{created['id']}/approve")
        assert approve.status_code == 200, approve.text

        pay = api_client.post(f"/expenses/{created['id']}/pay", json={
            "payment_account_id": str(kassa["id"]),
            "payment_method": "cash",
        })
        assert pay.status_code == 200, pay.text
        data = pay.json()["data"]
        assert data["status"] == "paid"
        assert data["paid_at"] is not None
        assert data["journal_entry_id"], "pay must link the posted JE"
        assert data["payment_account_id"] == str(kassa["id"])
        # double pay is rejected
        again = api_client.post(f"/expenses/{created['id']}/pay", json={})
        assert again.status_code == 400

        # DB: posted, balanced, 2-line JE with the expense as source
        db_read.execute(
            """SELECT je.id, je.status, je.source_type, je.total_debit, je.total_credit
               FROM journal_entries je WHERE je.id = %s""",
            (data["journal_entry_id"],),
        )
        je = db_read.fetchone()
        assert je is not None
        assert je["status"] == "posted"
        assert je["source_type"] == "expense"

        db_read.execute(
            """SELECT COUNT(*) AS n,
                      COALESCE(SUM(debit_amount), 0) AS dt,
                      COALESCE(SUM(credit_amount), 0) AS kt
               FROM journal_entry_lines WHERE journal_entry_id = %s""",
            (data["journal_entry_id"],),
        )
        lines = db_read.fetchone()
        assert lines["n"] == 2, "expense JE must have exactly 2 lines"
        assert float(lines["dt"]) == pytest.approx(1000)
        assert float(lines["kt"]) == pytest.approx(1000)

        # credit leg must hit the chosen kassa account
        db_read.execute(
            """SELECT account_id FROM journal_entry_lines
               WHERE journal_entry_id = %s AND credit_amount > 0""",
            (data["journal_entry_id"],),
        )
        credit_line = db_read.fetchone()
        assert str(credit_line["account_id"]) == str(kassa["id"])

    def test_approve_alone_posts_nothing(self, api_client, db_read, expense):
        """v2 invariant: the JE moves to pay time — approval must NOT touch the GL."""
        api_client.post(f"/expenses/{expense['id']}/approve")
        db_read.execute(
            """SELECT COUNT(*) AS n FROM journal_entries
               WHERE source_type = 'expense' AND source_id = %s AND deleted_at IS NULL""",
            (expense["id"],),
        )
        assert db_read.fetchone()["n"] == 0


# ============================================
# 4. Stats endpoint
# ============================================

class TestStats:
    def test_stats_shape(self, api_client):
        resp = api_client.get("/expenses/stats")
        assert resp.status_code == 200, resp.text
        stats = resp.json()["data"]
        for key in ("total", "draft", "pending_approval", "pending_payment",
                    "paid", "rejected", "by_category", "by_month"):
            assert key in stats, f"missing {key}"
        assert "count" in stats["total"] and "amount" in stats["total"]

    def test_stats_respect_date_window(self, api_client, category, employee):
        # Unique far-past date so parallel/dev data can't collide
        past = date(1991, 1, 1) + timedelta(days=random.randint(0, 3000))
        amount = random.randint(10_000, 99_000)
        created = _mk_expense(
            api_client, category, employee,
            amount=amount, date=_iso(past),
        ).json()["data"]
        try:
            resp = api_client.get("/expenses/stats", params={
                "date_from": _iso(past), "date_to": _iso(past),
            })
            stats = resp.json()["data"]
            assert stats["total"]["count"] == 1
            assert stats["total"]["amount"] == pytest.approx(amount)
            assert stats["pending_approval"]["count"] == 1
            cats = {c["name"]: c for c in stats["by_category"]}
            assert category["name"] in cats
            assert cats[category["name"]]["amount"] == pytest.approx(amount)
        finally:
            api_client.post(f"/expenses/{created['id']}/reject", json={"reason": "cleanup"})
            api_client.delete(f"/expenses/{created['id']}")


# ============================================
# 5. Tenant izolyatsiyasi
# ============================================

class TestTenantIsolation:
    def test_foreign_tenant_cannot_read(self, foreign_client, expense):
        resp = foreign_client.get(f"/expenses/{expense['id']}")
        assert resp.status_code in (403, 404)

    def test_foreign_tenant_cannot_approve_or_pay(self, foreign_client, expense):
        approve = foreign_client.post(f"/expenses/{expense['id']}/approve")
        assert approve.status_code in (400, 403, 404)
        pay = foreign_client.post(f"/expenses/{expense['id']}/pay", json={})
        assert pay.status_code in (400, 403, 404)

    def test_foreign_tenant_list_is_isolated(self, foreign_client, expense):
        resp = foreign_client.get("/expenses", params={"limit": 500})
        if resp.status_code == 200:
            ids = {e["id"] for e in resp.json()["data"]}
            assert expense["id"] not in ids


# ============================================
# 6. Permission
# ============================================

class TestPermissions:
    def test_unauthenticated_rejected(self):
        resp = requests.get(f"{BASE_URL}/expenses")
        assert resp.status_code == 401

    def test_unauthenticated_create_rejected(self):
        resp = requests.post(f"{BASE_URL}/expenses", json={"amount": 1})
        assert resp.status_code == 401


# ============================================
# 7. Kategoriyalar seed (migration 444 + lazy seed)
# ============================================

class TestCategoriesSeeded:
    DEFAULT_CODES = {"TRANSPORT", "IJARA", "KOMMUNAL", "OFIS",
                     "SAFAR", "REKLAMA", "MATERIAL", "BOSHQA"}

    def test_default_set_present_with_colors(self, categories):
        codes = {c.get("code") for c in categories}
        missing = self.DEFAULT_CODES - codes
        assert not missing, f"default categories missing: {missing}"
        for c in categories:
            if c.get("code") in self.DEFAULT_CODES:
                assert c.get("color"), f"{c['code']} must have a color"
                assert "usage_count" in c


# ============================================
# 8. Workflow event katalogi
# ============================================

class TestWorkflowCatalog:
    def test_expense_events_registered(self, api_client):
        resp = api_client.get("/workflow-events")
        assert resp.status_code == 200, resp.text
        events = {e.get("event") or e.get("value") for e in resp.json()["data"]}
        for evt in ("expenses.submitted", "expenses.approved",
                    "expenses.rejected", "expenses.paid"):
            assert evt in events, f"{evt} missing from workflow catalog"
