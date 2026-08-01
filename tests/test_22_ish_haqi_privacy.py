"""
Ish haqi — privacy + unified-calculation tests (Phase 5 quality bar).

Privacy is the top bar (prompt §5): an ordinary employee must not read other
employees' payroll via API — not just hidden UI. We mint a real limited user
(argon2 password set directly in the DB), log in, and probe every payroll
surface.

Calculation: the three creation paths were unified (audit §2.2); these tests
pin the advance-split and tax-engine behaviour so they can't silently diverge
again.
"""
import uuid
import pytest
import requests

from conftest import BASE_URL, APIClient

TEST_PASSWORD = "Xodim#2026test"


def _mk_employee(api_client, salary=2_000_000, first="Oddiy", last=None):
    suffix = uuid.uuid4().hex[:8]
    resp = api_client.post("/employees", json={
        "employee_number": f"EMP-T22-{suffix}",
        "first_name": first,
        "last_name": last or f"Xodim{suffix[:4]}",
        "email": f"t22-{suffix}@test.uz",
        "hire_date": "2026-01-01",
        "salary": salary,
        "job_title": "Ishchi",
    })
    if resp.status_code not in (200, 201):
        pytest.skip(f"Cannot create employee: {resp.status_code} {resp.text[:200]}")
    return resp.json().get("data", resp.json())


def _mk_period_with_entry(api_client, employee, salary=2_000_000, prefix="T22"):
    suffix = uuid.uuid4().hex[:8]
    resp = api_client.post("/payroll-periods", json={
        "period_code": f"{prefix}-{suffix}",
        "period_name": f"{prefix} davri {suffix}",
        "start_date": "2026-07-01",
        "end_date": "2026-07-31",
        "pay_date": "2026-08-05",
    })
    assert resp.status_code in (200, 201), resp.text
    period = resp.json().get("data", resp.json())
    resp = api_client.post(f"/payroll-periods/{period['id']}/entries", json={
        "employee_id": employee["id"],
        "base_salary": salary,
    })
    assert resp.status_code in (200, 201), resp.text
    return period, resp.json().get("data", resp.json())


# ─────────────────────────────────────────────────────────────────────────────
# Privacy: a real limited user probes every payroll surface
# ─────────────────────────────────────────────────────────────────────────────

class TestEmployeePrivacy:
    @pytest.fixture(scope="class")
    def limited(self, api_client, db_read, tenant_id):
        """A plain employee user with a real login and zero payroll grants."""
        from argon2 import PasswordHasher

        me = _mk_employee(api_client, salary=1_800_000)
        other = _mk_employee(api_client, salary=9_900_000, first="Boshqa")
        _mk_period_with_entry(api_client, me, 1_800_000, "T22ME")
        other_period, _ = _mk_period_with_entry(api_client, other, 9_900_000, "T22OT")

        # CreateEmployee auto-creates a linked users row; give it a known
        # password so we can actually log in as this employee.
        db_read.execute(
            "SELECT id, email FROM users WHERE employee_id = %s AND tenant_id = %s LIMIT 1",
            (me["id"], tenant_id))
        row = db_read.fetchone()
        if not row:
            pytest.skip("employee user auto-creation did not run")
        pw_hash = PasswordHasher().hash(TEST_PASSWORD)
        db_read.execute(
            "UPDATE users SET password_hash = %s, is_active = true, is_verified = true WHERE id = %s",
            (pw_hash, row["id"]))

        resp = requests.post(f"{BASE_URL}/auth/login", json={
            "email": row["email"], "password": TEST_PASSWORD})
        assert resp.status_code == 200, f"limited login failed: {resp.text[:300]}"
        data = resp.json().get("data", resp.json())
        token = data.get("access_token") or data.get("token")
        client = APIClient(base_url=BASE_URL, token=token, tenant_id=tenant_id)
        return {"client": client, "me": me, "other": other,
                "other_period": other_period, "user_email": row["email"]}

    def test_my_profile_is_own(self, limited):
        r = limited["client"].get("/my/profile")
        assert r.status_code == 200, r.text
        data = r.json().get("data", r.json())
        # JSON field is employee_code; the value comes from employees.employee_number
        assert data.get("employee_code") == limited["me"]["employee_number"]

    def test_my_history_returns_own_only(self, limited):
        r = limited["client"].get("/my/payroll-history")
        assert r.status_code == 200, r.text
        rows = r.json().get("data", r.json()) or []
        if isinstance(rows, dict):
            rows = rows.get("items", rows.get("history", []))
        # The other employee's 9.9M entry must never appear here.
        for row in rows:
            assert abs(float(row.get("gross_salary") or row.get("base_salary") or 0) - 9_900_000) > 0.01

    def test_cannot_list_payroll_periods(self, limited):
        r = limited["client"].get("/payroll-periods")
        assert r.status_code in (401, 403), \
            f"plain employee can read the whole tenant's payroll: {r.status_code}"

    def test_cannot_read_other_entries(self, limited):
        r = limited["client"].get(f"/payroll-periods/{limited['other_period']['id']}/entries")
        assert r.status_code in (401, 403), \
            f"plain employee can read another employee's payslip lines: {r.status_code}"

    def test_cannot_list_loans(self, limited):
        r = limited["client"].get("/employee-loans")
        assert r.status_code in (401, 403)

    def test_cannot_export_payroll(self, limited):
        r = limited["client"].get("/payroll/export")
        assert r.status_code in (401, 403)

    def test_cannot_mutate_tax_catalog(self, limited):
        r = limited["client"].post("/employee-taxes", json={
            "code": f"HAK{uuid.uuid4().hex[:4]}", "name": "hack", "rate": 50,
            "base_type": "gross", "payer": "employee"})
        assert r.status_code in (401, 403)

    def test_cannot_mark_tt_paid(self, limited):
        r = limited["client"].post(
            f"/payroll/entries/{uuid.uuid4()}/advance-paid", json={"paid": True})
        assert r.status_code in (401, 403)


# ─────────────────────────────────────────────────────────────────────────────
# Unified calculation behaviour
# ─────────────────────────────────────────────────────────────────────────────

class TestUnifiedCalc:
    def test_bulk_uses_settings_advance_percent(self, api_client, db_read, tenant_id):
        """calculate-all used to force advance 0/100%; must follow settings."""
        db_read.execute(
            "SELECT COALESCE(advance_percent, 40) AS pct FROM payroll_settings WHERE tenant_id = %s",
            (tenant_id,))
        row = db_read.fetchone()
        pct = float(row["pct"]) if row else 40.0

        emp = _mk_employee(api_client, salary=4_000_000)
        suffix = uuid.uuid4().hex[:8]
        resp = api_client.post("/payroll-periods", json={
            "period_code": f"T22BULK-{suffix}",
            "period_name": f"T22 bulk {suffix}",
            "start_date": "2026-07-01", "end_date": "2026-07-31", "pay_date": "2026-08-05",
        })
        assert resp.status_code in (200, 201)
        period = resp.json().get("data", resp.json())
        resp = api_client.post(f"/payroll-periods/{period['id']}/calculate-all", json={})
        assert resp.status_code == 200, resp.text

        db_read.execute(
            """SELECT base_salary, advance_amount, remainder_amount, advance_percent_used
               FROM payroll_entries WHERE payroll_period_id = %s AND employee_id = %s""",
            (period["id"], emp["id"]))
        e = db_read.fetchone()
        assert e, "bulk did not create an entry for the new employee"
        base = float(e["base_salary"])
        advance = float(e["advance_amount"])
        remainder = float(e["remainder_amount"])
        assert abs(float(e["advance_percent_used"]) - pct) < 0.01
        assert abs(advance - round(base * pct / 100)) < 1.0
        assert abs((advance + remainder) - base) < 0.01

    def test_active_tax_applies_to_created_entry(self, api_client, db_read, tenant_id):
        """An active 12% tax -> entry net must be gross minus tax; then restore."""
        db_read.execute(
            """SELECT id, rate FROM employee_taxes
               WHERE tenant_id = %s AND UPPER(code) = 'NDFL' AND deleted_at IS NULL LIMIT 1""",
            (tenant_id,))
        tax = db_read.fetchone()
        created_here = False
        if not tax:
            # Dev DB has an empty catalog — create a temporary tenant-scoped row.
            tax_id = str(uuid.uuid4())
            db_read.execute(
                """INSERT INTO employee_taxes
                   (id, tenant_id, code, name, rate, base_type, payer, is_active, sort_order, created_at, updated_at)
                   VALUES (%s, %s, 'NDFL', 'T22 NDFL', 12, 'gross', 'employee', true, 1, NOW(), NOW())""",
                (tax_id, tenant_id))
            tax = {"id": tax_id, "rate": 12}
            created_here = True
        db_read.execute("UPDATE employee_taxes SET is_active = true WHERE id = %s", (tax["id"],))
        try:
            emp = _mk_employee(api_client, salary=3_000_000)
            period, entry = _mk_period_with_entry(api_client, emp, 3_000_000, "T22TAX")
            expected_tax = round(3_000_000 * float(tax["rate"]) / 100)
            assert abs(float(entry.get("gross_salary", 0)) - 3_000_000) < 0.01
            assert abs(float(entry.get("total_deductions", 0)) - expected_tax) < 1.0, \
                f"active tax not applied: {entry.get('total_deductions')}"
            assert abs(float(entry.get("net_salary", 0)) - (3_000_000 - expected_tax)) < 1.0
            db_read.execute(
                "SELECT COUNT(*) AS n FROM payroll_entry_taxes WHERE payroll_entry_id = %s",
                (entry["id"],))
            assert db_read.fetchone()["n"] >= 1, "tax snapshot rows missing"
        finally:
            if created_here:
                db_read.execute("DELETE FROM payroll_entry_taxes WHERE tax_id = %s", (tax["id"],))
                db_read.execute("DELETE FROM employee_taxes WHERE id = %s", (tax["id"],))
            else:
                db_read.execute("UPDATE employee_taxes SET is_active = false WHERE id = %s", (tax["id"],))

    def test_confirm_applies_deduction_to_net(self, api_client, db_read, tenant_id, auth_token):
        """Confirm books the JE AND reduces net_salary (audit §2.4 fix)."""
        emp = _mk_employee(api_client, salary=2_500_000)
        period, entry = _mk_period_with_entry(api_client, emp, 2_500_000, "T22DED")
        net_before = float(entry["net_salary"])

        ded_id = str(uuid.uuid4())
        db_read.execute(
            """INSERT INTO employee_deductions
               (id, tenant_id, employee_id, amount, reason, source_type, status, created_by, created_at, updated_at)
               VALUES (%s, %s, %s, 150000, 'T22 test kamomad', 'inventory_shortage', 'pending', %s, NOW(), NOW())""",
            (ded_id, tenant_id, emp["id"], auth_token["user_id"]))

        r = api_client.post(
            f"/payroll-periods/{period['id']}/entries/{entry['id']}/confirm",
            json={"deduction_percent": 100})
        assert r.status_code == 200, r.text

        db_read.execute(
            "SELECT net_salary, other_deductions, status FROM payroll_entries WHERE id = %s",
            (entry["id"],))
        e = db_read.fetchone()
        assert e["status"] == "paid"
        assert abs(float(e["net_salary"]) - (net_before - 150_000)) < 0.01, \
            f"net not reduced: {e['net_salary']} (was {net_before})"
        assert float(e["other_deductions"]) >= 150_000 - 0.01

        db_read.execute(
            "SELECT status FROM employee_deductions WHERE id = %s", (ded_id,))
        assert db_read.fetchone()["status"] == "deducted"

        # 4730 exists since migration 445 -> the salary_deduction JE must post.
        db_read.execute(
            """SELECT total_debit, total_credit, status FROM journal_entries
               WHERE tenant_id = %s AND source_type = 'salary_deduction' AND source_id::text = %s""",
            (tenant_id, str(entry["id"])))
        jes = db_read.fetchall()
        assert len(jes) == 1, "salary_deduction JE missing (4730 seeded by 445)"
        assert jes[0]["status"] == "posted"
        assert abs(float(jes[0]["total_debit"]) - 150_000) < 0.01
        assert abs(float(jes[0]["total_debit"]) - float(jes[0]["total_credit"])) < 0.01
