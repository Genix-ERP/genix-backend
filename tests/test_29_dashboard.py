"""
Asosiy panel (main dashboard) backend tests — docs/dashboard-audit.md §7.

Invariants under test:
  1. GET /dashboard/top-products returns the hardened contract:
     {period_from, period_to, products[≤5]} sorted by revenue desc, each row
     carrying product_id/name/quantity/revenue — and requires auth (the route
     used to be un-gated and unscoped).
  2. Period params are honoured: a far-past window returns no products.
  3. The KPI source /reports/finance-dashboard reconciles with the posted
     ledger: net_result == total_income - total_expense, and the sign is
     preserved (the old UI ran Math.abs over a loss).

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import requests

from conftest import BASE_URL


class TestTopProductsContract:
    def test_shape_and_ordering(self, api_client):
        resp = api_client.get("/dashboard/top-products")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]

        for key in ("period_from", "period_to", "products"):
            assert key in data, f"missing key: {key}"

        products = data["products"]
        assert isinstance(products, list)
        assert len(products) <= 5

        revenues = []
        for p in products:
            for key in ("product_id", "name", "quantity", "revenue"):
                assert key in p, f"missing product key: {key}"
            assert p["revenue"] > 0
            revenues.append(p["revenue"])
        assert revenues == sorted(revenues, reverse=True), "not sorted by revenue desc"

    def test_period_scoping(self, api_client):
        resp = api_client.get(
            "/dashboard/top-products",
            params={"from": "2001-01-01", "to": "2001-01-31"},
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert data["period_from"] == "2001-01-01"
        assert data["products"] == []

    def test_requires_auth(self):
        resp = requests.get(f"{BASE_URL}/dashboard/top-products", timeout=10)
        assert resp.status_code == 401


class TestFinanceDashboardReconciliation:
    def test_net_result_is_signed_income_minus_expense(self, api_client, db_read, tenant_id):
        resp = api_client.get(
            "/reports/finance-dashboard",
            params={"period_from": "2000-01-01", "period_to": "2099-12-31"},
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]

        assert abs(data["net_result"] - (data["total_income"] - data["total_expense"])) < 0.01

        # Reconcile against the posted ledger directly (org of the api_client).
        db_read.execute(
            """
            SELECT
              COALESCE(SUM(CASE WHEN at.category = 'revenue' THEN l.credit_amount - l.debit_amount ELSE 0 END), 0) AS income,
              COALESCE(SUM(CASE WHEN at.category = 'expense' THEN l.debit_amount - l.credit_amount ELSE 0 END), 0) AS expense
            FROM journal_entry_lines l
            JOIN journal_entries je ON je.id = l.journal_entry_id
                AND je.status = 'posted' AND je.deleted_at IS NULL
                AND je.organization_id = %s
            JOIN accounts a ON a.id = l.account_id AND a.tenant_id = %s AND a.deleted_at IS NULL
            JOIN account_types at ON at.id = a.account_type_id
            WHERE at.category IN ('revenue', 'expense')
            """,
            (api_client.org_id, tenant_id),
        )
        row = db_read.fetchone()
        assert abs(float(row["income"]) - data["total_income"]) < 0.01, "income does not reconcile with ledger"
        assert abs(float(row["expense"]) - data["total_expense"]) < 0.01, "expense does not reconcile with ledger"
