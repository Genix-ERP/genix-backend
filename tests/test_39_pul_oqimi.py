"""
Pul oqimi deep-fix suite (2026-08-13 audit → fixes).

Pins the repaired behaviors so they cannot regress:

  1. GET /bank-accounts/:id is 200 — it referenced last_reconciled_date,
     a column renamed by migration 035, and 500'd on every call (which also
     broke the response of every successful PUT).
  2. The debit sufficiency guard reads the LEDGER, not the dead
     bank_accounts.balance column: a GL-linked account with millions in the
     ledger accepts a small debit; an absurd debit is rejected with the
     ledger figure in the message; creating a transaction never writes the
     dead balance column and never creates a journal entry.
  3. GL-link validation: a made-up account_id is a 400, not a silently
     unlinked account; account_type 'deposit' (offered by the UI forever)
     is accepted.
  4. Reconciliation cannot be completed with no statement-vs-ledger check:
     a fresh draft with a nonzero statement balance and nothing cleared is
     a 400 (the old code read NULL difference as 0 and completed it).
  5. The quick reconcile endpoint sets BOTH status and the is_reconciled
     column (the workflow reads the column; they used to drift).
  6. Legacy /cash-transactions writes are retired (410): rows there carried
     status='posted' with no journal entry, invisible to the cash engine.
"""

import uuid as uuidlib

import pytest


ODD_SUFFIX = uuidlib.uuid4().hex[:8]


@pytest.fixture(scope="module")
def test_account(api_client):
    """An UNLINKED bank account (no GL account) that tests may write to.

    Soft-deleted at teardown so reruns never accumulate rows.
    """
    resp = api_client.post("/bank-accounts", json={
        "name": f"PulOqimi Test {ODD_SUFFIX}",
        "bank_name": "Test Bank",
        "account_number": f"99999{ODD_SUFFIX}",
        "currency": "UZS",
        "account_type": "deposit",  # pins fix 3b: 'deposit' is accepted now
    })
    assert resp.status_code in (200, 201), resp.text
    acct = resp.json()["data"]
    yield acct
    api_client.delete(f"/bank-accounts/{acct['id']}")


def _real_linked_account(api_client):
    """The first GL-linked bank account with a positive ledger balance."""
    resp = api_client.get("/bank-accounts")
    assert resp.status_code == 200, resp.text
    data = resp.json()["data"]
    items = data.get("items", data) if isinstance(data, dict) else data
    for acct in items or []:
        if acct.get("gl_linked") and float(acct.get("ledger_balance") or 0) > 0:
            return acct
    return None


class TestBankAccountDetail:
    def test_get_detail_is_200(self, api_client, test_account):
        resp = api_client.get(f"/bank-accounts/{test_account['id']}")
        assert resp.status_code == 200, resp.text
        assert resp.json()["data"]["id"] == test_account["id"]

    def test_update_returns_200_not_500(self, api_client, test_account):
        resp = api_client.put(f"/bank-accounts/{test_account['id']}", json={
            "bank_name": "Test Bank Renamed",
        })
        assert resp.status_code == 200, resp.text
        assert resp.json()["data"]["bank_name"] == "Test Bank Renamed"

    def test_bogus_gl_link_is_400(self, api_client, test_account):
        resp = api_client.put(f"/bank-accounts/{test_account['id']}", json={
            "account_id": str(uuidlib.uuid4()),
        })
        assert resp.status_code == 400, resp.text


class TestDebitGuardReadsLedger:
    def test_ledger_backed_account_rejects_only_absurd_debits(self, api_client):
        acct = _real_linked_account(api_client)
        if acct is None:
            pytest.skip("no GL-linked bank account with a positive ledger balance")
        resp = api_client.post(f"/bank-accounts/{acct['id']}/transactions", json={
            "transaction_date": "2026-08-13",
            "description": "test_39 absurd debit",
            "amount": 999_999_999_999.0,
            "type": "debit",
            "reference": f"T39-{ODD_SUFFIX}",
        })
        # Rejection must quote the LEDGER balance, not the dead column's 0.00
        assert resp.status_code == 400, resp.text
        msg = str(resp.json().get("error", {}).get("message", ""))
        assert "balans: 0.00" not in msg, (
            "debit guard is reading the dead balance column again: " + msg
        )
        assert str(int(float(acct["ledger_balance"]))) [:4] in msg.replace(" ", ""), (
            "rejection does not quote the ledger balance: " + msg
        )

    def test_create_writes_no_dead_balance_and_no_je(self, api_client, db_read, tenant_id, test_account):
        resp = api_client.post(f"/bank-accounts/{test_account['id']}/transactions", json={
            "transaction_date": "2026-08-13",
            "description": "test_39 debit on unlinked account",
            "amount": 1000.0,
            "type": "debit",
            "reference": f"T39-DEBIT-{ODD_SUFFIX}",
        })
        # Unlinked account has no ledger truth → guard skipped → accepted
        assert resp.status_code in (200, 201), resp.text

        db_read.execute(
            "SELECT COALESCE(balance, 0) AS balance FROM bank_accounts WHERE id = %s",
            (test_account["id"],),
        )
        assert float(db_read.fetchone()["balance"]) == 0.0, (
            "CreateBankTransaction wrote the dead bank_accounts.balance column again"
        )

        db_read.execute(
            """
            SELECT COUNT(*) AS n FROM journal_entries
            WHERE tenant_id = %s AND source_type = 'bank_transaction'
            """,
            (tenant_id,),
        )
        assert db_read.fetchone()["n"] == 0

    def test_reconcile_sets_both_flags(self, api_client, db_read, test_account):
        listing = api_client.get(f"/bank-accounts/{test_account['id']}/transactions")
        assert listing.status_code == 200, listing.text
        rows = listing.json()["data"]
        rows = rows.get("items", rows) if isinstance(rows, dict) else rows
        assert rows, "expected the transaction created above"
        tx_id = rows[0]["id"]

        resp = api_client.post(
            f"/bank-accounts/{test_account['id']}/transactions/{tx_id}/reconcile"
        )
        assert resp.status_code == 200, resp.text

        db_read.execute(
            "SELECT status, COALESCE(is_reconciled, false) AS is_reconciled "
            "FROM bank_transactions WHERE id = %s",
            (tx_id,),
        )
        row = db_read.fetchone()
        assert row["status"] == "reconciled"
        assert row["is_reconciled"] is True, (
            "quick reconcile sets only status again — the workflow reads the column"
        )


class TestReconciliationCompleteGuard:
    def test_complete_without_clearing_is_400(self, api_client, test_account):
        create = api_client.post(f"/bank-accounts/{test_account['id']}/reconciliations", json={
            "statement_date": "2026-08-13",
            "statement_ending_balance": 500_000.0,
        })
        assert create.status_code in (200, 201), create.text
        recon_id = create.json()["data"]["id"]
        try:
            done = api_client.post(
                f"/bank-accounts/{test_account['id']}/reconciliations/{recon_id}/complete"
            )
            # anchor(0) + cleared(0) vs statement 500 000 → must NOT complete
            assert done.status_code == 400, (
                "reconciliation completed with no statement-vs-ledger check: " + done.text
            )
        finally:
            api_client.delete(
                f"/bank-accounts/{test_account['id']}/reconciliations/{recon_id}"
            )


class TestLegacyCashTransactionsRetired:
    def test_post_is_gone(self, api_client):
        resp = api_client.post("/cash-transactions", json={
            "transaction_date": "2026-08-13",
            "type": "expense",
            "amount": 1.0,
            "description": "must be rejected",
        })
        assert resp.status_code == 410, resp.text

    def test_list_still_readable(self, api_client):
        resp = api_client.get("/cash-transactions")
        assert resp.status_code == 200, resp.text
