"""
Moliya v2 numbers-integrity suite (docs/moliya-v2/audit.md → Phase 5).

Pins the single-cash-engine contract and the v2 fixes:

  1. ONE cash engine — /cash/balance == /reports/finance-dashboard cash ==
     raw ledger SUM over CASH-type leaf accounts.
  2. Kassa PKO/RKO is real — server-side numbering, confirm posts a balanced
     JE (Dr 5010 / Cr counter) once-only, RKO over ledger balance → 400.
  3. Test-pollution cleanup held — no active 'Test Bank UZS' accounts
     (migration 474), bank accounts expose ledger_balance/gl_linked.
  4. Balance sheet balances — assets (incl. contra_asset) == liabilities +
     equity (migration-less reports.go fix).
  5. KPI endpoint — all 14 KPIs present, no NaN/Inf, null-guard honored.
  6. Merged period system — FY has monthly fiscal_periods (migration 478);
     locking a period rejects postings into it (DB trigger + checkPeriodLock).
  7. Depreciation GL — no posted run without a JE, no orphan storno
     (migration 475 + ReverseDepreciationRun guard).
"""

import math

import psycopg2
import pytest
from psycopg2.extras import RealDictCursor

from conftest import DB_HOST, DB_NAME, DB_PASSWORD, DB_PORT, DB_USER, today


@pytest.fixture(scope="module")
def db():
    conn = psycopg2.connect(
        host=DB_HOST, port=DB_PORT, user=DB_USER,
        password=DB_PASSWORD, dbname=DB_NAME,
    )
    conn.autocommit = True
    cur = conn.cursor(cursor_factory=RealDictCursor)
    yield cur
    conn.close()


def ledger_cash(db, tenant_id, as_of=None):
    db.execute(
        """
        SELECT COALESCE(SUM(l.debit_amount - l.credit_amount), 0) AS bal
        FROM journal_entry_lines l
        JOIN journal_entries je ON je.id = l.journal_entry_id
            AND je.status = 'posted' AND je.deleted_at IS NULL
            AND (%s::date IS NULL OR je.entry_date <= %s::date)
        JOIN accounts a ON a.id = l.account_id AND a.tenant_id = %s
            AND a.deleted_at IS NULL AND a.is_leaf = true AND a.is_active = true
        JOIN account_types at ON at.id = a.account_type_id AND at.code = 'CASH'
        """,
        (as_of, as_of, tenant_id),
    )
    return float(db.fetchone()["bal"])


# ---------------------------------------------------------------------------
# 1. Single cash engine
# ---------------------------------------------------------------------------

class TestSingleCashEngine:
    def test_cash_balance_equals_ledger(self, api_client, tenant_id, db):
        resp = api_client.get("/cash/balance")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert abs(float(data["total"]) - ledger_cash(db, tenant_id)) < 0.01

    def test_dashboard_agrees_with_cash_engine(self, api_client, tenant_id, db):
        resp = api_client.get("/reports/finance-dashboard")
        assert resp.status_code == 200, resp.text
        dash = resp.json()["data"]
        engine = api_client.get("/cash/balance").json()["data"]
        assert abs(float(dash["cash_balance"]) - float(engine["total"])) < 0.01

    def test_no_test_bank_pollution(self, db, tenant_id):
        db.execute(
            """
            SELECT COUNT(*) AS n FROM bank_accounts
            WHERE tenant_id = %s AND deleted_at IS NULL
              AND name = 'Test Bank UZS' AND bank_name = 'NBU Test'
            """,
            (tenant_id,),
        )
        # The session fixture may have created exactly one for THIS run;
        # the historical 67 must be gone.
        assert db.fetchone()["n"] <= 1

    def test_bank_accounts_expose_ledger_balance(self, api_client):
        resp = api_client.get("/bank-accounts")
        assert resp.status_code == 200, resp.text
        rows = resp.json().get("data") or []
        if not rows:
            pytest.skip("No bank accounts")
        for row in rows:
            assert "ledger_balance" in row
            assert "gl_linked" in row


# ---------------------------------------------------------------------------
# 2. Kassa PKO/RKO
# ---------------------------------------------------------------------------

class TestKassaPosting:
    def test_pko_confirm_posts_balanced_je(self, api_client, tenant_id, db):
        before = ledger_cash(db, tenant_id)

        resp = api_client.post("/cash/orders", json={
            "type": "pko",
            "amount": 125000,
            "description": "test_34 PKO — moliya v2 suite",
            "counterparty_name": "Test kassa mijozi",
            "account_code": "4010",
        })
        assert resp.status_code in (200, 201), resp.text
        order = resp.json()["data"]
        assert order["status"] == "draft"
        assert order["order_number"].startswith("PKO-"), "server-side numbering"

        resp = api_client.post(f"/cash/orders/{order['id']}/confirm")
        assert resp.status_code == 200, resp.text
        confirmed = resp.json()["data"]
        assert confirmed["status"] == "confirmed"
        assert confirmed.get("journal_entry_id"), "confirm must post a JE"

        db.execute(
            """
            SELECT SUM(l.debit_amount) AS dr, SUM(l.credit_amount) AS cr,
                   COUNT(*) AS lines
            FROM journal_entry_lines l WHERE l.journal_entry_id = %s
            """,
            (confirmed["journal_entry_id"],),
        )
        je = db.fetchone()
        assert je["lines"] >= 2
        assert abs(float(je["dr"]) - float(je["cr"])) < 0.01
        assert abs(float(je["dr"]) - 125000) < 0.01

        after = ledger_cash(db, tenant_id)
        assert abs((after - before) - 125000) < 0.01

        # Once-only: second confirm must not double-post
        resp = api_client.post(f"/cash/orders/{order['id']}/confirm")
        assert resp.status_code >= 400
        assert abs(ledger_cash(db, tenant_id) - after) < 0.01

    def test_rko_insufficient_funds_rejected(self, api_client, tenant_id, db):
        cash_now = ledger_cash(db, tenant_id)
        resp = api_client.post("/cash/orders", json={
            "type": "rko",
            "amount": cash_now + 10_000_000_000,
            "description": "test_34 RKO — yetarli emas",
            "counterparty_name": "Test",
            "account_code": "6010",
        })
        assert resp.status_code in (200, 201), resp.text
        order = resp.json()["data"]
        resp = api_client.post(f"/cash/orders/{order['id']}/confirm")
        assert resp.status_code == 400, "RKO over ledger balance must be rejected"
        assert "account" not in (resp.json().get("error") or {}).get("message", "").lower(), (
            "must fail on insufficiency, not on account resolution"
        )
        api_client.post(f"/cash/orders/{order['id']}/cancel")

    def test_confirmed_order_immutable(self, api_client):
        resp = api_client.get("/cash/orders", params={"status": "confirmed"})
        assert resp.status_code == 200
        items = resp.json().get("data") or []
        if isinstance(items, dict):
            items = items.get("items", [])
        if not items:
            pytest.skip("No confirmed orders")
        oid = items[0]["id"]
        resp = api_client.post(f"/cash/orders/{oid}/cancel")
        assert resp.status_code >= 400, "confirmed orders are immutable"


# ---------------------------------------------------------------------------
# 3. Reports integrity
# ---------------------------------------------------------------------------

class TestReportsIntegrity:
    def test_balance_sheet_balances(self, api_client):
        resp = api_client.get("/reports/balance-sheet")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assets = float(data.get("total_assets", 0))
        liabilities = float(data.get("total_liabilities", 0))
        equity = float(data.get("total_equity", 0))
        assert abs(assets - (liabilities + equity)) < 0.05, (
            f"A={assets} != L+E={liabilities + equity}"
        )

    def test_cash_flow_reconciles_to_ledger(self, api_client, tenant_id, db):
        resp = api_client.get("/reports/cash-flow", params={
            "period_from": "2026-01-01", "period_to": today(),
        })
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        closing = data.get("closing_cash_balance", data.get("closing_balance"))
        if closing is None:
            pytest.skip("cash-flow shape has no closing balance field")
        assert abs(float(closing) - ledger_cash(db, tenant_id, today())) < 0.05

    def test_trial_balance_balanced(self, api_client):
        resp = api_client.get("/reports/trial-balance")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        td = float(data.get("total_debits", data.get("total_debit", 0)))
        tc = float(data.get("total_credits", data.get("total_credit", 0)))
        assert abs(td - tc) < 0.05


# ---------------------------------------------------------------------------
# 4. KPI layer
# ---------------------------------------------------------------------------

KPI_KEYS = [
    "gross_margin", "operating_margin", "net_margin", "roa", "roe", "equity",
    "working_capital", "current_ratio", "cash_balance", "cash_runway_months",
    "dso", "dpo", "overdue_ar_pct", "revenue_growth",
]


class TestKPIs:
    def test_all_kpis_present_and_finite(self, api_client):
        resp = api_client.get("/reports/finance-kpis")
        assert resp.status_code == 200, resp.text
        kpis = resp.json()["data"]["kpis"]
        for key in KPI_KEYS:
            assert key in kpis, f"missing KPI {key}"
            value = kpis[key].get("value")
            if value is not None:
                assert not math.isnan(value) and not math.isinf(value), key
            assert "inputs" in kpis[key]

    def test_kpi_cash_matches_engine(self, api_client):
        kpis = api_client.get("/reports/finance-kpis").json()["data"]["kpis"]
        engine = api_client.get("/cash/balance").json()["data"]
        assert kpis["cash_balance"]["value"] is not None
        assert abs(kpis["cash_balance"]["value"] - float(engine["total"])) < 0.01


# ---------------------------------------------------------------------------
# 5. Merged period system
# ---------------------------------------------------------------------------

class TestPeriods:
    def test_fiscal_year_has_monthly_periods(self, db, tenant_id):
        db.execute(
            """
            SELECT COUNT(*) AS n FROM fiscal_periods fp
            JOIN fiscal_years fy ON fy.id = fp.fiscal_year_id
            WHERE fy.tenant_id = %s
            """,
            (tenant_id,),
        )
        assert db.fetchone()["n"] >= 12, "migration 478 must backfill months"

    def test_locked_period_rejects_posting(self, api_client, tenant_id, db, accounts):
        # Lock the earliest period of the year (no live postings expected there).
        db.execute(
            """
            SELECT fp.id, fp.start_date FROM fiscal_periods fp
            JOIN fiscal_years fy ON fy.id = fp.fiscal_year_id
            WHERE fy.tenant_id = %s AND fp.status = 'open'
            ORDER BY fp.start_date ASC LIMIT 1
            """,
            (tenant_id,),
        )
        period = db.fetchone()
        if not period:
            pytest.skip("No open fiscal period")
        pid = str(period["id"])

        resp = api_client.post(f"/fiscal-periods/{pid}/lock")
        if resp.status_code == 404:
            resp = api_client.post(f"/finance/fiscal-periods/{pid}/lock")
        assert resp.status_code in (200, 201), resp.text

        try:
            leaf = None
            for code in ("9430", "5010"):
                if accounts.get(code):
                    leaf = accounts[code]
                    break
            if not leaf:
                pytest.skip("No leaf account for the probe")
            entry_date = period["start_date"].isoformat()
            resp = api_client.post("/journal-entries", json={
                "entry_date": entry_date,
                "description": "test_34 locked-period probe",
                "lines": [
                    {"account_id": str(leaf["id"]), "debit_amount": 1000, "credit_amount": 0},
                    {"account_id": str(leaf["id"]), "debit_amount": 0, "credit_amount": 1000},
                ],
            })
            # Create may be blocked outright, or creation of a draft may pass
            # and only posting be blocked — both are acceptable lock behavior.
            if resp.status_code in (200, 201):
                je = resp.json()["data"]
                post = api_client.post(f"/journal-entries/{je['id']}/post")
                assert post.status_code >= 400, "posting into a locked period must fail"
        finally:
            unlock = api_client.post(f"/fiscal-periods/{pid}/unlock")
            if unlock.status_code == 404:
                api_client.post(f"/finance/fiscal-periods/{pid}/unlock")


# ---------------------------------------------------------------------------
# 6. Depreciation GL invariants
# ---------------------------------------------------------------------------

class TestDepreciationInvariants:
    def test_no_posted_run_without_je(self, db, tenant_id):
        db.execute(
            """
            SELECT COUNT(*) AS n FROM fa_depreciation_runs
            WHERE tenant_id = %s AND status = 'posted' AND journal_entry_id IS NULL
            """,
            (tenant_id,),
        )
        assert db.fetchone()["n"] == 0

    def test_no_orphan_depreciation_storno(self, db, tenant_id):
        db.execute(
            """
            SELECT COUNT(*) AS n
            FROM journal_entries je
            WHERE je.tenant_id = %s AND je.source_type = 'depreciation_reversal'
              AND je.status = 'posted' AND je.deleted_at IS NULL
              AND NOT EXISTS (
                SELECT 1 FROM journal_entries orig
                WHERE orig.tenant_id = je.tenant_id
                  AND orig.source_type = 'depreciation_run'
                  AND orig.source_id::text = je.source_id::text
                  AND orig.status = 'posted' AND orig.deleted_at IS NULL
              )
            """,
            (tenant_id,),
        )
        assert db.fetchone()["n"] == 0
