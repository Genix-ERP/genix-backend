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


# ============================================================================
# Filtering, stable paging and the summary card totals (B1–B4).
#
# The screen these back paginates on mobile and does not on web, which is the
# whole reason these have to be server-side: a filter or a total computed over
# "the pages loaded so far" is wrong on one client and right on the other.
# ============================================================================


@pytest.fixture()
def seeded_partner_set(tenant_id, test_company):
    """N contacts sharing one `due`, each with invoice + confirmed payment.

    Same due on every row on purpose: it is the case where ORDER BY
    (due, name) stops being a total order, which is what test_pagination_is_
    stable_across_pages exercises.
    """
    conn = psycopg2.connect(
        host=DB_HOST, port=DB_PORT, user=DB_USER,
        password=DB_PASSWORD, dbname=DB_NAME,
    )
    conn.autocommit = True
    cur = conn.cursor(cursor_factory=RealDictCursor)

    marker = uuid.uuid4().hex[:8].upper()
    org_id = str(test_company["id"])
    created = []

    def make(label, invoice_total, payment_amount):
        cid = str(uuid.uuid4())
        inv_id = str(uuid.uuid4())
        pay_id = str(uuid.uuid4())
        suffix = uuid.uuid4().hex[:6].upper()
        cur.execute(
            """
            INSERT INTO contacts (id, tenant_id, organization_id, name, code, type)
            VALUES (%s, %s, %s, %s, %s, 'customer')
            """,
            (cid, tenant_id, org_id, f"{marker} {label}", f"{marker}-{suffix}"),
        )
        if invoice_total:
            cur.execute(
                """
                INSERT INTO sales_invoices (id, tenant_id, organization_id, invoice_number,
                    customer_id, invoice_date, due_date, subtotal, total_amount, amount_paid, status)
                VALUES (%s, %s, %s, %s, %s, CURRENT_DATE, CURRENT_DATE + 14, %s, %s, 0, 'sent')
                """,
                (inv_id, tenant_id, org_id, f"INV-{marker}-{suffix}", cid, invoice_total, invoice_total),
            )
        if payment_amount:
            cur.execute(
                """
                INSERT INTO payments (id, tenant_id, organization_id, payment_number, type,
                    contact_id, payment_date, amount, status)
                VALUES (%s, %s, %s, %s, 'receipt', %s, CURRENT_DATE, %s, 'confirmed')
                """,
                (pay_id, tenant_id, org_id, f"REC-{marker}-{suffix}", cid, payment_amount),
            )
        created.append((cid, inv_id if invoice_total else None, pay_id if payment_amount else None))
        return cid

    yield {"marker": marker, "make": make, "created": created}

    for cid, inv_id, pay_id in created:
        if pay_id:
            cur.execute("DELETE FROM payment_allocations WHERE payment_id = %s", (pay_id,))
            cur.execute("DELETE FROM payments WHERE id = %s", (pay_id,))
        if inv_id:
            cur.execute("DELETE FROM sales_invoices WHERE id = %s", (inv_id,))
        cur.execute("DELETE FROM contacts WHERE id = %s", (cid,))
    conn.close()


class TestPartnerBalancesFiltering:
    def test_search_filters_rows_and_total(self, api_client, seeded_partner_set):
        marker = seeded_partner_set["marker"]
        seeded_partner_set["make"]("ALPHA", 100000, 50000)
        seeded_partner_set["make"]("BRAVO", 100000, 50000)
        seeded_partner_set["make"]("CHARLIE", 100000, 50000)

        resp = api_client.get(
            "/payments/partner-balances",
            params={"direction": "customer", "search": f"{marker} BRAVO",
                    "page": 1, "page_size": 20},
        )
        assert resp.status_code == 200, resp.text
        body = resp.json()
        rows = body["data"]
        assert len(rows) == 1, rows
        assert rows[0]["name"] == f"{marker} BRAVO"

        # The count must be filtered too. If it is not, has_next stays true and
        # a paginating client scrolls through empty pages forever.
        meta = body["meta"]
        assert meta["total"] == 1, meta
        assert meta["has_next"] is False, meta

    def test_search_is_case_insensitive_and_partial(self, api_client, seeded_partner_set):
        marker = seeded_partner_set["marker"]
        seeded_partner_set["make"]("DELTA", 100000, 50000)

        resp = api_client.get(
            "/payments/partner-balances",
            params={"direction": "customer", "search": marker.lower(),
                    "page": 1, "page_size": 20},
        )
        assert resp.status_code == 200, resp.text
        assert len(resp.json()["data"]) == 1

    def test_only_reconcilable_matches_badge_rule(self, api_client, seeded_partner_set):
        marker = seeded_partner_set["marker"]
        both = seeded_partner_set["make"]("BOTH", 100000, 50000)      # due + credit
        seeded_partner_set["make"]("DUEONLY", 100000, 0)              # due, no credit
        seeded_partner_set["make"]("CREDITONLY", 0, 50000)            # credit, no due

        resp = api_client.get(
            "/payments/partner-balances",
            params={"direction": "customer", "search": marker,
                    "only_reconcilable": "true", "page": 1, "page_size": 20},
        )
        assert resp.status_code == 200, resp.text
        rows = resp.json()["data"]
        assert [r["contact_id"] for r in rows] == [both], rows

        # Exactly the rule the mobile card draws its badge from: BOTH sides
        # above 0.001. A row the filter keeps must be a row the badge marks.
        assert rows[0]["due"] > 0.001 and rows[0]["credit"] > 0.001

    def test_pagination_is_stable_across_pages(self, api_client, seeded_partner_set):
        """Pages must cover distinct contacts — 25 rows sharing due AND name.

        HONEST LIMIT: this does not prove the ORDER BY is total. Verified by
        mutation — removing the `c.id ASC` tiebreaker leaves this test GREEN,
        because Postgres is free to return a stable order for a table this
        small and generally does. A non-total ORDER BY is a planner-dependent
        bug; reproducing it on demand would mean pinning a query plan, which
        would make the test about the planner instead of the endpoint.

        What it does catch is the class of paging bug that IS deterministic:
        off-by-one offsets, a page-size mismatch between rows and LIMIT, and
        rows duplicated across pages by a bad join. The tiebreaker itself is
        argued for in the handler, not proven here.
        """
        marker = seeded_partner_set["marker"]
        for _ in range(25):
            # Same label for every one, so `name` cannot break the tie either.
            seeded_partner_set["make"]("SAME", 100000, 50000)

        seen = []
        for page in (1, 2):
            resp = api_client.get(
                "/payments/partner-balances",
                params={"direction": "customer", "search": marker,
                        "page": page, "page_size": 10},
            )
            assert resp.status_code == 200, resp.text
            seen.extend(r["contact_id"] for r in resp.json()["data"])

        assert len(seen) == 20, seen
        assert len(set(seen)) == 20, "a contact appeared on two pages; the order is not total"


class TestPartnerBalancesSummary:
    def test_summary_matches_list_totals(self, api_client, seeded_partner_set):
        marker = seeded_partner_set["marker"]
        seeded_partner_set["make"]("SUMA", 100000, 50000)     # reconcilable
        seeded_partner_set["make"]("SUMB", 200000, 0)         # due only
        seeded_partner_set["make"]("SUMC", 0, 70000)          # credit only

        listed = api_client.get(
            "/payments/partner-balances",
            params={"direction": "customer", "search": marker},
        )
        assert listed.status_code == 200, listed.text
        rows = listed.json()["data"]
        assert len(rows) == 3

        summary = api_client.get(
            "/payments/partner-balances/summary",
            params={"direction": "customer", "search": marker},
        )
        assert summary.status_code == 200, summary.text
        s = summary.json()["data"]

        assert s["partner_count"] == len(rows)
        assert s["total_due"] == pytest.approx(sum(r["due"] for r in rows))
        assert s["total_credit"] == pytest.approx(sum(r["credit"] for r in rows))
        assert s["reconcilable_count"] == sum(
            1 for r in rows if r["due"] > 0.001 and r["credit"] > 0.001
        )
        assert s["reconcilable_count"] == 1

    def test_summary_narrows_with_search(self, api_client, seeded_partner_set):
        """The cards sit above the rows; both must describe the same set."""
        marker = seeded_partner_set["marker"]
        seeded_partner_set["make"]("NARROWA", 100000, 50000)
        seeded_partner_set["make"]("NARROWB", 400000, 0)

        wide = api_client.get(
            "/payments/partner-balances/summary",
            params={"direction": "customer", "search": marker},
        ).json()["data"]
        narrow = api_client.get(
            "/payments/partner-balances/summary",
            params={"direction": "customer", "search": f"{marker} NARROWB"},
        ).json()["data"]

        assert wide["partner_count"] == 2
        assert narrow["partner_count"] == 1
        assert narrow["total_due"] == pytest.approx(400000)
        assert narrow["total_credit"] == pytest.approx(0)
        assert narrow["reconcilable_count"] == 0

    def test_summary_honours_only_reconcilable(self, api_client, seeded_partner_set):
        marker = seeded_partner_set["marker"]
        seeded_partner_set["make"]("ONLYA", 100000, 50000)
        seeded_partner_set["make"]("ONLYB", 300000, 0)

        s = api_client.get(
            "/payments/partner-balances/summary",
            params={"direction": "customer", "search": marker,
                    "only_reconcilable": "true"},
        ).json()["data"]

        assert s["partner_count"] == 1
        assert s["total_due"] == pytest.approx(100000)

    def test_summary_rejects_bad_direction(self, api_client):
        resp = api_client.get(
            "/payments/partner-balances/summary", params={"direction": "sideways"}
        )
        assert resp.status_code == 400


class TestPartnerBalancesPagingContract:
    def test_unpaginated_call_still_returns_bare_array(self, api_client, seeded_partner):
        """Web calls this with no page params and expects a bare array.

        Making paging mandatory would silently truncate that screen to the
        first page — no error, just missing partners.
        """
        resp = api_client.get("/payments/partner-balances", params={"direction": "customer"})
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert isinstance(body["data"], list)
        assert "meta" not in body or body.get("meta") is None
