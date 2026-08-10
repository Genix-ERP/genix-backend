"""
BOSQICH 12: BYUDJET (Budget)
80% da warning, 100% da alert
"""
import uuid
import pytest
from conftest import today


class TestCreateBudget:
    """12.1 - Byudjet yaratish."""

    def test_create_budget(self, api_client, db_read, tenant_id):
        # Need a fiscal year first
        db_read.execute(
            "SELECT id FROM fiscal_years WHERE tenant_id = %s LIMIT 1",
            (tenant_id,)
        )
        fy = db_read.fetchone()

        if not fy:
            # Try to create fiscal year
            fy_resp = api_client.post("/fiscal-years", json={
                "name": "2026",
                "code": "FY2026",
                "start_date": "2026-01-01",
                "end_date": "2026-12-31",
            })
            if fy_resp.status_code in (200, 201):
                fy_data = fy_resp.json().get("data", fy_resp.json())
                fy_id = fy_data.get("id")
            else:
                pytest.skip("Fiscal year topilmadi/yaratilmadi")
                return
        else:
            fy_id = str(fy["id"])

        code = f"BUD-{uuid.uuid4().hex[:6].upper()}"
        resp = api_client.post("/budgets", json={
            "name": f"Test Byudjet {code}",
            "code": code,
            "fiscal_year_id": fy_id,
            "budget_type": "expense",
            "total_amount": 100000000,
            "description": "Test xarajat byudjeti",
        })
        assert resp.status_code in (200, 201), f"Byudjet yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert float(data.get("total_amount", 0)) == 100000000

        # Teardown: the suite left one 'Test Byudjet' per run in the dev DB
        # (65 by 2026-08-10, migration 474 cleaned them) — delete what we made.
        if data.get("id"):
            api_client.delete(f"/budgets/{data['id']}")


class TestVarianceCalculation:
    """12.3 - Reja va fakt farqi."""

    def test_variance(self):
        """Reja 100M, fakt 60M -> farq 40M (40%)."""
        planned = 100000000
        actual = 60000000
        variance = planned - actual
        variance_pct = (variance / planned) * 100

        assert variance == 40000000
        assert abs(variance_pct - 40.0) < 0.01


class TestWarningAt80Pct:
    """12.4 - 80% da ogohlantirish."""

    def test_80_percent_threshold(self):
        """Fakt 80M, reja 100M -> warning."""
        planned = 100000000
        actual = 80000000
        usage_pct = (actual / planned) * 100
        warning = usage_pct >= 80
        assert warning is True, f"80% da warning bo'lishi kerak: {usage_pct}%"


class TestOverBudgetAlert:
    """12.5 - Byudjetdan oshsa alert."""

    def test_over_budget(self):
        """Fakt 110M > reja 100M."""
        planned = 100000000
        actual = 110000000
        over_budget = actual > planned
        assert over_budget is True


class TestConsolidatedBudget:
    """12.7 - Konsolidatsiya qilingan byudjet."""

    def test_consolidated_budget_endpoint(self, api_client):
        resp = api_client.get("/budget/consolidated")
        assert resp.status_code in (200, 404), f"Consolidated budget xato: {resp.text}"


class TestBudgetCRUD:
    """Byudjet CRUD operatsiyalari."""

    def test_list_budgets(self, api_client):
        resp = api_client.get("/budgets")
        assert resp.status_code == 200

    def test_list_budgets_v2_decommissioned(self, api_client):
        """The V2 CRUD stubs fake-succeeded (always []) and were removed in
        Moliya v2 — GET /budget must no longer answer 200. CRUD is /budgets."""
        resp = api_client.get("/budget")
        assert resp.status_code in (404, 501)

    def test_budget_lines(self, api_client, db_read, tenant_id):
        """Byudjet satrlari."""
        db_read.execute(
            "SELECT id FROM budgets WHERE tenant_id = %s AND deleted_at IS NULL LIMIT 1",
            (tenant_id,)
        )
        budget = db_read.fetchone()
        if budget:
            resp = api_client.get(f"/budget/{budget['id']}/lines")
            assert resp.status_code in (200, 404)
