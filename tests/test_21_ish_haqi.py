"""
Ish haqi (Payroll) → Moliyaviy ma'lumotlar (GL) integration tests.

Proves the accounting connection end-to-end (audit: docs/ish-haqi-audit.md):
  - ProcessPayroll posts a balanced accrual JE (Dt 9420 / Kt 6710), once.
  - Period → paid posts a balanced payment JE (Dt 6710 / Kt cash/bank), once,
    with the CORRECT debit sign on the 6710 balance (the pre-fix code
    subtracted both legs).
  - TT advance/remainder mark-paid posts a real JE; un-marking reverses it.
  - Loan repayment posts Dt cash / Kt 4720 when the disbursement JE exists.
  - Accounts 4720/4730 exist (migration 445).
  - /employee-loans and /employee-taxes mutations are permission-gated.
"""
import uuid
import pytest

from conftest import BASE_URL, find_leaf_account


def _mk_employee(api_client, salary=2_000_000):
    """Create a throwaway employee with a salary; returns dict or skip."""
    suffix = uuid.uuid4().hex[:8]
    resp = api_client.post("/employees", json={
        "employee_number": f"EMP-T21-{suffix}",
        "first_name": "Test",
        "last_name": f"Payroll{suffix[:4]}",
        "email": f"t21-{suffix}@test.uz",
        "hire_date": "2026-01-01",
        "salary": salary,
        "job_title": "Test lavozim",
    })
    if resp.status_code not in (200, 201):
        pytest.skip(f"Cannot create employee: {resp.status_code} {resp.text[:200]}")
    return resp.json().get("data", resp.json())


def _mk_period(api_client, name_prefix="T21"):
    suffix = uuid.uuid4().hex[:8]
    resp = api_client.post("/payroll-periods", json={
        "period_code": f"{name_prefix}-{suffix}",
        "period_name": f"{name_prefix} davri {suffix}",
        "start_date": "2026-07-01",
        "end_date": "2026-07-31",
        "pay_date": "2026-08-05",
    })
    assert resp.status_code in (200, 201), resp.text
    return resp.json().get("data", resp.json())


def _je_rows(db_read, tenant_id, source_type, source_id):
    db_read.execute(
        """SELECT id, entry_number, total_debit, total_credit, status, reversed_entry_id
           FROM journal_entries
           WHERE tenant_id = %s AND source_type = %s AND source_id::text = %s
             AND deleted_at IS NULL ORDER BY created_at""",
        (tenant_id, source_type, str(source_id)),
    )
    return db_read.fetchall()


def _je_lines(db_read, je_id):
    db_read.execute(
        """SELECT l.debit_amount, l.credit_amount, a.code
           FROM journal_entry_lines l JOIN accounts a ON a.id = l.account_id
           WHERE l.journal_entry_id = %s ORDER BY l.line_number""",
        (je_id,),
    )
    return db_read.fetchall()


def _fund_account(db_read, tenant_id, account_id, amount):
    """Fund an account with a REAL balanced JE (Dt account / Cr equity).

    Cache-only bumps satisfied the balance guard but drifted the cache away
    from the ledger, and repeated suite runs drove the ledger cash negative
    (test_14's balance sheet reads the ledger). A real posted JE moves both.
    """
    db_read.execute("SELECT organization_id FROM accounts WHERE id = %s", (account_id,))
    row = db_read.fetchone()
    org = row["organization_id"] if row else None
    db_read.execute(
        """SELECT id FROM accounts WHERE tenant_id = %s AND organization_id IS NOT DISTINCT FROM %s
           AND code LIKE '8%%' AND COALESCE(is_leaf, true) AND deleted_at IS NULL ORDER BY code LIMIT 1""",
        (tenant_id, org))
    eq = db_read.fetchone()
    db_read.execute(
        "SELECT id FROM journals WHERE tenant_id = %s AND code = 'MISC' AND deleted_at IS NULL LIMIT 1",
        (tenant_id,))
    j = db_read.fetchone()
    if not eq or not j:
        db_read.execute(
            "UPDATE accounts SET current_balance = current_balance + %s WHERE id = %s",
            (amount, account_id))
        return
    je = str(uuid.uuid4())
    num = f"TESTFUND{uuid.uuid4().hex[:8].upper()}"
    # db_read is autocommit; explicit BEGIN/COMMIT so the deferred JE-balance
    # trigger (migration 416) sees header+lines together.
    db_read.execute("BEGIN")
    db_read.execute(
        """INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date,
             description, source_type, status, total_debit, total_credit, created_at, updated_at)
           VALUES (%s, %s, %s, %s, %s, NOW(), 'Test funding', 'manual', 'posted', %s, %s, NOW(), NOW())""",
        (je, tenant_id, org, j["id"], num, amount, amount))
    db_read.execute(
        """INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, line_number, description, debit_amount, credit_amount, created_at)
           VALUES (%s, %s, %s, 1, 'Test funding', %s, 0, NOW())""",
        (str(uuid.uuid4()), je, account_id, amount))
    db_read.execute(
        """INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, line_number, description, debit_amount, credit_amount, created_at)
           VALUES (%s, %s, %s, 2, 'Test funding', 0, %s, NOW())""",
        (str(uuid.uuid4()), je, eq["id"], amount))
    db_read.execute(
        "UPDATE accounts SET current_balance = current_balance + %s WHERE id = %s", (amount, account_id))
    db_read.execute(
        "UPDATE accounts SET current_balance = current_balance - %s WHERE id = %s", (amount, eq["id"]))
    db_read.execute("COMMIT")


def _fund_cash_accounts(db_read, tenant_id, amount):
    """Fund every kassa/bank row so whichever one findAccount resolves has cover."""
    db_read.execute(
        "SELECT id FROM accounts WHERE tenant_id = %s AND code IN ('5010','5110') AND deleted_at IS NULL",
        (tenant_id,))
    for row in db_read.fetchall():
        _fund_account(db_read, tenant_id, row["id"], amount)


def _balance(db_read, account_id):
    db_read.execute("SELECT current_balance FROM accounts WHERE id = %s", (account_id,))
    row = db_read.fetchone()
    return float(row["current_balance"]) if row else 0.0


# ─────────────────────────────────────────────────────────────────────────────
# Chart prerequisites (migration 445)
# ─────────────────────────────────────────────────────────────────────────────

def test_445_accounts_seeded(accounts):
    """4720 (loans to employees) and 4730 (material damage) must exist."""
    assert accounts.get("4720"), "4720 missing — migration 445 not applied"
    assert accounts.get("4730"), "4730 missing — migration 445 not applied"
    assert accounts["4720"].get("is_leaf", True)
    assert accounts["4730"].get("is_leaf", True)


# ─────────────────────────────────────────────────────────────────────────────
# Accrual: ProcessPayroll → Dt 9420 / Kt 6710, idempotent
# ─────────────────────────────────────────────────────────────────────────────

class TestAccrual:
    @pytest.fixture(scope="class")
    def period_with_entry(self, api_client, db_read, tenant_id):
        emp = _mk_employee(api_client, salary=3_000_000)
        period = _mk_period(api_client, "T21ACC")
        resp = api_client.post(f"/payroll-periods/{period['id']}/entries", json={
            "employee_id": emp["id"],
            "base_salary": 3_000_000,
        })
        assert resp.status_code in (200, 201), resp.text
        entry = resp.json().get("data", resp.json())
        return {"period": period, "entry": entry, "employee": emp}

    def test_process_posts_balanced_accrual_je(self, api_client, db_read, tenant_id, period_with_entry):
        pid = period_with_entry["period"]["id"]
        resp = api_client.post(f"/payroll-periods/{pid}/process", json={})
        assert resp.status_code == 200, resp.text

        jes = _je_rows(db_read, tenant_id, "payroll", pid)
        assert len(jes) == 1, f"expected exactly one accrual JE, got {len(jes)}"
        je = jes[0]
        assert je["status"] == "posted"
        assert abs(float(je["total_debit"]) - float(je["total_credit"])) < 0.01

        lines = _je_lines(db_read, je["id"])
        codes = {l["code"] for l in lines}
        assert "9420" in codes, f"salary expense line missing: {codes}"
        assert "6710" in codes or "6010" in codes, f"payable line missing: {codes}"
        total_debit = sum(float(l["debit_amount"]) for l in lines)
        total_credit = sum(float(l["credit_amount"]) for l in lines)
        assert abs(total_debit - total_credit) < 0.01

    def test_process_is_idempotent(self, api_client, db_read, tenant_id, period_with_entry):
        pid = period_with_entry["period"]["id"]
        resp = api_client.post(f"/payroll-periods/{pid}/process", json={})
        assert resp.status_code == 200
        jes = _je_rows(db_read, tenant_id, "payroll", pid)
        assert len(jes) == 1, "re-processing must not post a second accrual JE"


# ─────────────────────────────────────────────────────────────────────────────
# Payment: period → paid → Dt 6710 / Kt cash, correct signs, idempotent
# ─────────────────────────────────────────────────────────────────────────────

class TestPayment:
    @pytest.fixture(scope="class")
    def paid_setup(self, api_client, db_read, tenant_id, accounts):
        emp = _mk_employee(api_client, salary=1_500_000)
        period = _mk_period(api_client, "T21PAY")
        resp = api_client.post(f"/payroll-periods/{period['id']}/entries", json={
            "employee_id": emp["id"],
            "base_salary": 1_500_000,
        })
        assert resp.status_code in (200, 201), resp.text
        # Accrue first so Dt 6710 clears a real liability
        resp = api_client.post(f"/payroll-periods/{period['id']}/process", json={})
        assert resp.status_code == 200
        return {"period": period}

    def test_paid_posts_payment_je_with_correct_signs(
        self, api_client, db_read, tenant_id, accounts, paid_setup
    ):
        pid = paid_setup["period"]["id"]
        cash = find_leaf_account(accounts, "5010", "5110")
        assert cash, "no cash account on chart"
        payable = accounts.get("6710")
        assert payable, "no 6710 on chart"

        db_read.execute(
            "SELECT COALESCE(total_net,0) AS net FROM payroll_periods WHERE id = %s", (pid,))
        net = float(db_read.fetchone()["net"])
        assert net > 0

        # The pay path now refuses on insufficient balance (expenses
        # standard) — fund every kassa/bank row so whichever one findAccount
        # resolves has cover.
        db_read.execute(
            "UPDATE accounts SET current_balance = current_balance + %s WHERE tenant_id = %s AND code IN ('5010','5110') AND deleted_at IS NULL",
            (net + 1_000_000, tenant_id))

        payable_before = _balance(db_read, payable["id"])
        cash_before = _balance(db_read, str(cash["id"]))

        resp = api_client.put(f"/payroll-periods/{pid}", json={
            "status": "paid", "payment_method": "cash",
        })
        assert resp.status_code == 200, resp.text

        jes = _je_rows(db_read, tenant_id, "payroll_payment", pid)
        assert len(jes) == 1, f"expected one payment JE, got {len(jes)}"
        lines = _je_lines(db_read, jes[0]["id"])
        total_debit = sum(float(l["debit_amount"]) for l in lines)
        total_credit = sum(float(l["credit_amount"]) for l in lines)
        assert abs(total_debit - total_credit) < 0.01
        assert abs(total_debit - net) < 0.01

        # Balance convention: += debit − credit (migration 407).
        # Debit leg on 6710 must ADD (pre-fix bug subtracted both legs).
        payable_after = _balance(db_read, payable["id"])
        cash_after = _balance(db_read, str(cash["id"]))
        assert abs((payable_after - payable_before) - net) < 0.01, \
            f"6710 must move +{net}, moved {payable_after - payable_before}"
        assert abs((cash_after - cash_before) + net) < 0.01, \
            f"cash must move -{net}, moved {cash_after - cash_before}"

    def test_paid_is_idempotent(self, api_client, db_read, tenant_id, paid_setup):
        pid = paid_setup["period"]["id"]
        resp = api_client.put(f"/payroll-periods/{pid}", json={
            "status": "paid", "payment_method": "cash",
        })
        assert resp.status_code == 200, resp.text
        jes = _je_rows(db_read, tenant_id, "payroll_payment", pid)
        assert len(jes) == 1, "re-paying must not post a second payment JE"


# ─────────────────────────────────────────────────────────────────────────────
# TT flow: advance-paid / remainder-paid post + reverse real JEs
# ─────────────────────────────────────────────────────────────────────────────

class TestTTMarkPaid:
    @pytest.fixture(scope="class")
    def tt_entry(self, api_client, db_read, tenant_id):
        resp = api_client.post("/payroll/periods/current-or-create", json={})
        if resp.status_code not in (200, 201):
            pytest.skip(f"TT current-or-create failed: {resp.status_code} {resp.text[:200]}")
        period = resp.json().get("data", resp.json()).get("period")
        assert period and period.get("id")
        resp = api_client.get(f"/payroll-periods/{period['id']}/entries")
        assert resp.status_code == 200
        entries = resp.json().get("data", resp.json())
        if isinstance(entries, dict):
            entries = entries.get("items", entries.get("entries", []))
        candidates = [e for e in entries if float(e.get("advance_amount") or 0) > 0]
        if not candidates:
            pytest.skip("no TT entry with a positive advance_amount")
        return {"period": period, "entry": candidates[0]}

    def test_advance_paid_posts_je_and_unmark_reverses(
        self, api_client, db_read, tenant_id, accounts, tt_entry
    ):
        entry = tt_entry["entry"]
        eid = entry["id"]
        advance = float(entry.get("advance_amount") or 0)

        # Fund the credit accounts (real JEs) so the balance guard passes.
        _fund_cash_accounts(db_read, tenant_id, advance + 1_000_000)

        # Ensure a clean start: if a previous run left it paid, unmark first.
        if entry.get("advance_paid"):
            r = api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": False})
            assert r.status_code == 200, r.text

        active_before = [j for j in _je_rows(db_read, tenant_id, "payroll_payment", eid)
                         if j["reversed_entry_id"] is None]

        r = api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": True, "day": 15})
        assert r.status_code == 200, r.text

        active_after = [j for j in _je_rows(db_read, tenant_id, "payroll_payment", eid)
                        if j["reversed_entry_id"] is None]
        assert len(active_after) == len(active_before) + 1, \
            "marking advance paid must post exactly one new active JE"
        je = active_after[-1]
        lines = _je_lines(db_read, je["id"])
        total_debit = sum(float(l["debit_amount"]) for l in lines)
        total_credit = sum(float(l["credit_amount"]) for l in lines)
        assert abs(total_debit - total_credit) < 0.01
        assert abs(total_debit - advance) < 0.01
        codes = {l["code"] for l in lines}
        assert codes & {"6710", "9420"}, f"debit side must be 6710 or 9420: {codes}"
        assert codes & {"5010", "5110"}, f"credit side must be cash/bank: {codes}"

        # Un-mark → the JE gets a posted reversal, no active JE remains extra.
        r = api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": False})
        assert r.status_code == 200, r.text
        rows = _je_rows(db_read, tenant_id, "payroll_payment", eid)
        target = [j for j in rows if j["id"] == je["id"]][0]
        assert target["reversed_entry_id"] is not None, "un-marking must reverse the JE"
        active_final = [j for j in rows if j["reversed_entry_id"] is None]
        assert len(active_final) == len(active_before)

    def test_double_mark_does_not_double_post(
        self, api_client, db_read, tenant_id, accounts, tt_entry
    ):
        entry = tt_entry["entry"]
        eid = entry["id"]
        advance = float(entry.get("advance_amount") or 0)
        _fund_cash_accounts(db_read, tenant_id, advance + 1_000_000)

        r = api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": True})
        assert r.status_code == 200, r.text
        r = api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": True})
        assert r.status_code == 200, r.text
        active = [j for j in _je_rows(db_read, tenant_id, "payroll_payment", eid)
                  if j["reversed_entry_id"] is None]
        assert len(active) == 1, f"double-marking left {len(active)} active JEs"
        # cleanup
        api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": False})


# ─────────────────────────────────────────────────────────────────────────────
# Loans: disbursement + repayment both hit the GL
# ─────────────────────────────────────────────────────────────────────────────

class TestLoanGL:
    def test_loan_disbursement_and_repayment_jes(self, api_client, db_read, tenant_id, accounts):
        emp = _mk_employee(api_client, salary=2_500_000)
        cash = find_leaf_account(accounts, "5010", "5110")
        assert cash
        _fund_account(db_read, tenant_id, str(cash["id"]), 5_000_000)

        resp = api_client.post("/employee-loans", json={
            "employee_id": emp["id"],
            "amount": 1_200_000,
            "duration_months": 4,
            "start_date": "2026-08-01",
            "reason": "T21 test qarz",
            "cash_account_id": str(cash["id"]),
        })
        if resp.status_code not in (200, 201):
            pytest.skip(f"loan create failed: {resp.status_code} {resp.text[:200]}")
        loan = resp.json().get("data", resp.json())

        # Disbursement JE: Dt 4720 / Kt cash (entry number JE-LOAN-<number>)
        db_read.execute(
            """SELECT id, total_debit, total_credit FROM journal_entries
               WHERE tenant_id = %s AND source_type = 'employee_loan'
                 AND entry_number = %s AND deleted_at IS NULL""",
            (tenant_id, f"JE-LOAN-{loan.get('loan_number', '')}"),
        )
        disb = db_read.fetchall()
        assert len(disb) == 1, "loan disbursement JE missing (4720 seeded by migration 445)"
        lines = _je_lines(db_read, disb[0]["id"])
        assert {"4720"} & {l["code"] for l in lines}

        # Mark first scheduled payment paid → repayment JE Dt cash / Kt 4720
        resp = api_client.get(f"/employee-loans/{loan['id']}")
        assert resp.status_code == 200
        detail = resp.json().get("data", resp.json())
        payments = detail.get("payments") or []
        assert payments, "loan has no schedule"
        first = payments[0]

        resp = api_client.post(
            f"/employee-loans/{loan['id']}/payments/{first['id']}/mark-paid", json={})
        assert resp.status_code == 200, resp.text

        rep = _je_rows(db_read, tenant_id, "employee_loan_repayment", first["id"])
        assert len(rep) == 1, "repayment JE missing"
        lines = _je_lines(db_read, rep[0]["id"])
        codes = {l["code"] for l in lines}
        assert "4720" in codes
        total_debit = sum(float(l["debit_amount"]) for l in lines)
        total_credit = sum(float(l["credit_amount"]) for l in lines)
        assert abs(total_debit - total_credit) < 0.01

        # Loan totals moved together with the flag (same tx)
        db_read.execute(
            "SELECT paid_amount, remaining_amount FROM employee_loans WHERE id = %s",
            (loan["id"],))
        row = db_read.fetchone()
        assert float(row["paid_amount"]) >= float(first["amount"]) - 0.01


# ─────────────────────────────────────────────────────────────────────────────
# Permissions
# ─────────────────────────────────────────────────────────────────────────────

class TestPermissions:
    def test_employee_loans_requires_auth(self):
        import requests
        r = requests.get(f"{BASE_URL}/employee-loans")
        assert r.status_code == 401

    def test_employee_taxes_mutation_requires_auth(self):
        import requests
        r = requests.post(f"{BASE_URL}/employee-taxes", json={
            "code": "HACK", "name": "x", "rate": 99, "base_type": "gross", "payer": "employee",
        })
        assert r.status_code == 401

    def test_employee_taxes_payment_requires_auth(self):
        import requests
        r = requests.post(f"{BASE_URL}/employee-taxes/payments", json={"amount": 1})
        assert r.status_code == 401

    def test_admin_can_list_loans(self, api_client):
        r = api_client.get("/employee-loans")
        assert r.status_code == 200


# ─────────────────────────────────────────────────────────────────────────────
# Avans reclass (moliya audit §2.3): an advance paid BEFORE the accrual
# debits 9420 (cash-basis); ProcessPayroll must post a reclass
# Dt 6710 / Kt 9420 for that amount so labor expense lands exactly once.
# ─────────────────────────────────────────────────────────────────────────────

class TestAvansReclass:
    def test_process_reclasses_preaccrual_advance(self, api_client, db_read, tenant_id, accounts):
        emp = _mk_employee(api_client, salary=2_000_000)
        period = _mk_period(api_client, "T21RCL")
        pid = period["id"]
        r = api_client.post(f"/payroll-periods/{pid}/entries", json={
            "employee_id": emp["id"],
            "base_salary": 2_000_000,
        })
        assert r.status_code in (200, 201), r.text
        entry = r.json().get("data", r.json())
        eid = entry["id"]
        advance = float(entry.get("advance_amount") or 0)
        if advance <= 0:
            # Classic create doesn't compute the advance split; a no-op update
            # triggers the server-side recompute (base × advance_percent).
            r = api_client.put(f"/payroll-periods/{pid}/entries/{eid}", json={})
            assert r.status_code == 200, r.text
            entry = r.json().get("data", r.json())
            advance = float(entry.get("advance_amount") or 0)
        if advance <= 0:
            pytest.skip("no advance computed for entry (TT settings)")

        _fund_cash_accounts(db_read, tenant_id, advance + 1_000_000)

        # 1. Advance BEFORE accrual → payment JE debits 9420 (cash-basis mode).
        r = api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": True, "day": 15})
        assert r.status_code == 200, r.text
        pay = [j for j in _je_rows(db_read, tenant_id, "payroll_payment", eid)
               if j["reversed_entry_id"] is None]
        assert pay, "advance payment JE missing"
        pay_lines = _je_lines(db_read, pay[-1]["id"])
        assert any(l["code"] == "9420" and float(l["debit_amount"]) > 0 for l in pay_lines), \
            f"pre-accrual advance must debit 9420, got {[(l['code'], float(l['debit_amount'])) for l in pay_lines]}"

        # 2. Accrue the period → exactly one reclass JE must fire.
        r = api_client.post(f"/payroll-periods/{pid}/process", json={})
        assert r.status_code == 200, r.text
        reclass = [j for j in _je_rows(db_read, tenant_id, "payroll_avans_reclass", pid)
                   if j["reversed_entry_id"] is None]
        assert len(reclass) == 1, "processing must post exactly one avans reclass JE"
        by_code = {l["code"]: l for l in _je_lines(db_read, reclass[0]["id"])}
        assert float(by_code["6710"]["debit_amount"]) == pytest.approx(advance, abs=0.01), \
            "reclass must debit 6710 for the advance amount"
        assert float(by_code["9420"]["credit_amount"]) == pytest.approx(advance, abs=0.01), \
            "reclass must credit 9420 for the advance amount"

        # 3. Net 9420 across accrual + payment + reclass == gross: the labor
        #    cost is expensed exactly once (the double-count guard).
        gross = float(entry.get("gross_salary") or 2_000_000)
        db_read.execute(
            """SELECT COALESCE(SUM(l.debit_amount - l.credit_amount), 0) AS net
               FROM journal_entry_lines l
               JOIN journal_entries je ON je.id = l.journal_entry_id
               JOIN accounts a ON a.id = l.account_id
               WHERE je.tenant_id = %s AND a.code = '9420'
                 AND je.status = 'posted' AND je.deleted_at IS NULL
                 AND ((je.source_type IN ('payroll', 'payroll_avans_reclass') AND je.source_id::text = %s)
                   OR (je.source_type = 'payroll_payment' AND je.source_id::text = %s))""",
            (tenant_id, str(pid), str(eid)),
        )
        net_9420 = float(db_read.fetchone()["net"])
        assert net_9420 == pytest.approx(gross, abs=0.01), \
            f"labor expense must equal gross exactly once: 9420 net {net_9420} vs gross {gross}"

    def test_unmark_after_accrual_posts_counter_reclass(self, api_client, db_read, tenant_id, accounts):
        emp = _mk_employee(api_client, salary=1_800_000)
        period = _mk_period(api_client, "T21RCC")
        pid = period["id"]
        r = api_client.post(f"/payroll-periods/{pid}/entries", json={
            "employee_id": emp["id"],
            "base_salary": 1_800_000,
        })
        assert r.status_code in (200, 201), r.text
        entry = r.json().get("data", r.json())
        eid = entry["id"]
        advance = float(entry.get("advance_amount") or 0)
        if advance <= 0:
            r = api_client.put(f"/payroll-periods/{pid}/entries/{eid}", json={})
            assert r.status_code == 200, r.text
            entry = r.json().get("data", r.json())
            advance = float(entry.get("advance_amount") or 0)
        if advance <= 0:
            pytest.skip("no advance computed for entry (TT settings)")
        _fund_cash_accounts(db_read, tenant_id, advance + 1_000_000)

        r = api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": True, "day": 10})
        assert r.status_code == 200, r.text
        r = api_client.post(f"/payroll-periods/{pid}/process", json={})
        assert r.status_code == 200, r.text

        # Un-mark the advance AFTER accrual: the payment JE gets reversed and
        # a counter-reclass must restore 9420/6710 so the reclass doesn't dangle.
        r = api_client.post(f"/payroll/entries/{eid}/advance-paid", json={"paid": False})
        assert r.status_code == 200, r.text

        rows = _je_rows(db_read, tenant_id, "payroll_avans_reclass", pid)
        assert len(rows) == 2, f"expected reclass + counter-reclass, got {len(rows)}"
        net = sum(
            (1 if l["code"] == "6710" else 0) * (float(l["debit_amount"]) - float(l["credit_amount"]))
            for j in rows for l in _je_lines(db_read, j["id"])
        )
        assert net == pytest.approx(0, abs=0.01), \
            "counter-reclass must cancel the reclass on 6710 when the advance is unmarked"
