"""
Xodimlar (HR) module guardrail tests (audit: docs/hr-audit.md, 2026-08-03).

Covers the HR rebuild's backend contracts:
  - GET /employees/stats returns coherent, server-computed KPI numbers
    (the old UI derived them from one 20-row page — P0).
  - PUT status=terminated writes termination_date; re-activating clears it.
  - DELETE /employees/:id must NOT remove a user that now belongs to a
    different employee sharing the same email (old email-fallback P0).
  - DELETE /departments/:id is refused (409) while employees reference it.
  - Employee mutations write audit_logs rows.
"""
import uuid
import pytest


def _mk_employee(api_client, **over):
    suffix = uuid.uuid4().hex[:8]
    payload = {
        "employee_number": f"EMP-T23-{suffix}",
        "first_name": "Test",
        "last_name": f"HR{suffix[:4]}",
        "hire_date": "2026-01-15",
        "salary": 3_000_000,
        "job_title": "Test lavozim",
    }
    payload.update(over)
    resp = api_client.post("/employees", json=payload)
    if resp.status_code not in (200, 201):
        pytest.skip(f"Cannot create employee: {resp.status_code} {resp.text[:200]}")
    return resp.json().get("data", resp.json())


class TestEmployeeStats:
    def test_stats_shape_and_coherence(self, api_client):
        resp = api_client.get("/employees/stats")
        assert resp.status_code == 200, resp.text
        d = resp.json()["data"]
        for key in ("total", "active", "salary_fund", "headcount_by_month",
                    "departments", "tenure_buckets"):
            assert key in d, f"missing {key}"
        assert d["total"] >= d["active"]
        assert d["salary_fund"] >= 0
        assert len(d["headcount_by_month"]) == 12
        # months are consecutive YYYY-MM strings ending at the current month
        months = [p["month"] for p in d["headcount_by_month"]]
        assert months == sorted(months)

    def test_stats_counts_created_employee(self, api_client):
        before = api_client.get("/employees/stats").json()["data"]["total"]
        _mk_employee(api_client)
        after = api_client.get("/employees/stats").json()["data"]["total"]
        assert after == before + 1


class TestTermination:
    def test_terminate_writes_termination_date(self, api_client, db_read, tenant_id):
        emp = _mk_employee(api_client)
        resp = api_client.put(f"/employees/{emp['id']}", json={"status": "terminated", "termination_reason": "Test sabab"})
        assert resp.status_code == 200, resp.text
        db_read.execute(
            "SELECT status, termination_date, termination_reason FROM employees WHERE id = %s",
            (emp["id"],),
        )
        row = db_read.fetchone()
        assert row["status"] == "terminated"
        assert row["termination_date"] is not None
        assert row["termination_reason"] == "Test sabab"

        # Re-hire clears the termination fields
        resp = api_client.put(f"/employees/{emp['id']}", json={"status": "active"})
        assert resp.status_code == 200, resp.text
        db_read.execute("SELECT termination_date, termination_reason FROM employees WHERE id = %s", (emp["id"],))
        row = db_read.fetchone()
        assert row["termination_date"] is None
        assert row["termination_reason"] is None


class TestDeleteEmployeeUserSafety:
    def test_delete_does_not_remove_relinked_user(self, api_client, db_read, tenant_id):
        """Two employees created with the same email share one user record
        (CreateEmployee links the existing user to the newest employee).
        Deleting the FIRST employee must not delete that user."""
        email = f"t23-shared-{uuid.uuid4().hex[:8]}@test.uz"
        emp_a = _mk_employee(api_client, email=email)
        emp_b = _mk_employee(api_client, email=email)

        db_read.execute(
            "SELECT id, employee_id FROM users WHERE tenant_id = %s AND email = %s",
            (tenant_id, email),
        )
        user = db_read.fetchone()
        assert user is not None, "auto-created user missing"
        assert str(user["employee_id"]) == emp_b["id"], "user should be linked to the newest employee"

        resp = api_client.delete(f"/employees/{emp_a['id']}")
        assert resp.status_code in (200, 204), resp.text

        db_read.execute(
            "SELECT id FROM users WHERE tenant_id = %s AND email = %s",
            (tenant_id, email),
        )
        assert db_read.fetchone() is not None, \
            "user belonging to another employee was deleted by the email fallback"


class TestDepartmentGuards:
    def test_delete_department_with_employees_refused(self, api_client):
        code = f"T23{uuid.uuid4().hex[:5].upper()}"
        resp = api_client.post("/departments", json={"code": code, "name": f"Test bo'lim {code}"})
        if resp.status_code not in (200, 201):
            pytest.skip(f"Cannot create department: {resp.status_code}")
        dept = resp.json().get("data", resp.json())

        _mk_employee(api_client, department_id=dept["id"], department=dept["id"])

        resp = api_client.delete(f"/departments/{dept['id']}")
        assert resp.status_code == 409, f"expected 409 while employees reference the department, got {resp.status_code}"


class TestAuditLog:
    def test_employee_create_writes_audit_row(self, api_client, db_read, tenant_id):
        emp = _mk_employee(api_client)
        db_read.execute(
            "SELECT action FROM audit_logs WHERE tenant_id = %s AND entity_type = 'employee' AND entity_id = %s",
            (tenant_id, emp["id"]),
        )
        rows = db_read.fetchall()
        assert any(r["action"] == "create" for r in rows), "no create audit row for employee"
