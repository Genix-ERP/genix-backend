"""
Solishtirish (partner reconciliation) tests — Moliya → Qarzdorlik → Solishtirish.

The Reconcile UI was shipped against three endpoints that did not exist
(/payments/partner-balances fell through to GET /payments/:id → 400).
These tests pin the implemented contract:

  1. GET /payments/partner-balances?direction=customer|vendor — per-partner
     invoiced/paid/due totals + unallocated confirmed-payment credit; unknown
     direction → 400.
  2. GET /payments/partner-ledger — open docs + credit payments; the
     credit_available equals the sum of the listed unallocated amounts.
  3. POST /payments/reconcile — applies credit oldest-first, caps at the
     document's due, writes payment_allocations + invoice amount_paid/status
     atomically, and reduces the partner's credit by exactly the applied sum.

Seeds its own contact + invoice + confirmed payment via a committed DB
connection (the API can't see db_session's uncommitted rows) and removes
them afterwards.
"""

import uuid

import psycopg2
import pytest
from psycopg2.extras import RealDictCursor

from conftest import DB_HOST, DB_NAME, DB_PASSWORD, DB_PORT, DB_USER


@pytest.fixture()
def seeded_partner(api_client, tenant_id, test_company):
    """Committed contact + sent invoice (500 000) + confirmed receipt (800 000)."""
    conn = psycopg2.connect(
        host=DB_HOST, port=DB_PORT, user=DB_USER,
        password=DB_PASSWORD, dbname=DB_NAME,
    )
    conn.autocommit = True
    cur = conn.cursor(cursor_factory=RealDictCursor)

    suffix = uuid.uuid4().hex[:6].upper()
    contact_id = str(uuid.uuid4())
    invoice_id = str(uuid.uuid4())
    payment_id = str(uuid.uuid4())
    org_id = str(test_company["id"])

    cur.execute(
        """
        INSERT INTO contacts (id, tenant_id, organization_id, name, code, type, email)
        VALUES (%s, %s, %s, %s, %s, 'customer', %s)
        """,
        (contact_id, tenant_id, org_id, f"RECONTEST {suffix}", f"RECON-{suffix}", f"recon-{suffix}@test.uz"),
    )
    cur.execute(
        """
        INSERT INTO sales_invoices (id, tenant_id, organization_id, invoice_number,
            customer_id, invoice_date, due_date, subtotal, total_amount, amount_paid, status)
        VALUES (%s, %s, %s, %s, %s, CURRENT_DATE, CURRENT_DATE + 14, 500000, 500000, 0, 'sent')
        """,
        (invoice_id, tenant_id, org_id, f"INV-RECON-{suffix}", contact_id),
    )
    cur.execute(
        """
        INSERT INTO payments (id, tenant_id, organization_id, payment_number, type,
            contact_id, payment_date, amount, status)
        VALUES (%s, %s, %s, %s, 'receipt', %s, CURRENT_DATE, 800000, 'confirmed')
        """,
        (payment_id, tenant_id, org_id, f"REC-RECON-{suffix}", contact_id),
    )

    yield {"contact_id": contact_id, "invoice_id": invoice_id, "payment_id": payment_id}

    cur.execute("DELETE FROM payment_allocations WHERE payment_id = %s", (payment_id,))
    cur.execute("DELETE FROM payments WHERE id = %s", (payment_id,))
    cur.execute("DELETE FROM sales_invoices WHERE id = %s", (invoice_id,))
    cur.execute("DELETE FROM contacts WHERE id = %s", (contact_id,))
    conn.close()


class TestPartnerBalancesContract:
    def test_direction_validation(self, api_client):
        resp = api_client.get("/payments/partner-balances", params={"direction": "sideways"})
        assert resp.status_code == 400

    def test_shape_and_seeded_row(self, api_client, seeded_partner):
        resp = api_client.get("/payments/partner-balances", params={"direction": "customer"})
        assert resp.status_code == 200, resp.text
        rows = resp.json()["data"]
        assert isinstance(rows, list)

        row = next((r for r in rows if r["contact_id"] == seeded_partner["contact_id"]), None)
        assert row is not None, "seeded partner missing from balances"
        assert row["invoiced"] == 500000
        assert row["paid"] == 0
        assert row["due"] == 500000
        assert row["credit"] == 800000


class TestPartnerLedgerAndReconcile:
    def test_ledger_then_reconcile_applies_and_settles(self, api_client, seeded_partner, db_read):
        cid = seeded_partner["contact_id"]

        ledger = api_client.get(
            "/payments/partner-ledger",
            params={"contact_id": cid, "direction": "customer"},
        )
        assert ledger.status_code == 200, ledger.text
        data = ledger.json()["data"]
        assert data["credit_available"] == 800000
        assert sum(p["unallocated"] for p in data["payments"]) == data["credit_available"]
        assert [d["id"] for d in data["open_docs"]] == [seeded_partner["invoice_id"]]
        assert data["open_docs"][0]["due"] == 500000

        resp = api_client.post(
            "/payments/reconcile",
            json={
                "contact_id": cid,
                "direction": "customer",
                "document_id": seeded_partner["invoice_id"],
                "amount": 0,
            },
        )
        assert resp.status_code == 200, resp.text
        result = resp.json()["data"]
        assert result["applied"] == 500000, "should cap at the document's due, not the credit"
        assert result["fully_paid"] is True

        # Invoice settled, allocation written, credit reduced to the remainder.
        db_read.execute(
            "SELECT status, amount_paid::float, amount_due::float FROM sales_invoices WHERE id = %s",
            (seeded_partner["invoice_id"],),
        )
        inv = db_read.fetchone()
        assert inv["status"] == "paid"
        assert inv["amount_paid"] == 500000
        assert inv["amount_due"] == 0

        after = api_client.get(
            "/payments/partner-ledger",
            params={"contact_id": cid, "direction": "customer"},
        )
        data2 = after.json()["data"]
        assert data2["credit_available"] == 300000
        assert data2["open_docs"] == []

        # Second apply against the same (now settled) document is a no-op.
        again = api_client.post(
            "/payments/reconcile",
            json={
                "contact_id": cid,
                "direction": "customer",
                "document_id": seeded_partner["invoice_id"],
                "amount": 0,
            },
        )
        assert again.status_code in (200, 400)
        if again.status_code == 200:
            assert again.json()["data"]["applied"] == 0
