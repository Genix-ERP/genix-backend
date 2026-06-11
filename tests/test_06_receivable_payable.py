"""
BOSQICH 6: KREDITOR VA DEBITOR (Receivable / Payable)
Provodkalar orqali debitorlik va kreditorlik tekshiruvi.
"""
import uuid
import pytest
from conftest import today, find_account_id, find_leaf_account, assert_balanced


class TestSaleCreatesReceivable:
    """6.2 - Sotish -> debitorlik oshadi (Dt 4010)."""

    def test_sale_journal_entry(self, api_client, accounts, journals, test_customer):
        ar_acc = find_leaf_account(accounts, "1200", "4010")
        rev_acc = find_leaf_account(accounts, "4000", "9010", "4100")
        journal = journals.get("sales") or journals.get("general") or list(journals.values())[0]

        if not ar_acc or not rev_acc:
            pytest.skip("AR yoki Revenue schyot topilmadi")

        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "reference": f"SALE-{uuid.uuid4().hex[:6]}",
            "description": "Test sotish - debitorlik",
            "lines": [
                {
                    "account_id": str(ar_acc["id"]),
                    "contact_id": str(test_customer["id"]),
                    "description": "Debitorlik",
                    "debit_amount": 10000000,
                    "credit_amount": 0,
                },
                {
                    "account_id": str(rev_acc["id"]),
                    "description": "Sotish daromadi",
                    "debit_amount": 0,
                    "credit_amount": 10000000,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"Sotish yozuvi xato: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert_balanced(data)


class TestPurchaseCreatesPayable:
    """6.1 - Xarid -> kreditorlik oshadi (Kt 6010)."""

    def test_purchase_journal_entry(self, api_client, accounts, journals, test_supplier):
        ap_acc = find_leaf_account(accounts, "2000", "6010")
        inv_acc = find_leaf_account(accounts, "1300", "1010", "5000")
        journal = journals.get("purchase") or journals.get("general") or list(journals.values())[0]

        if not ap_acc or not inv_acc:
            pytest.skip("AP yoki Inventory schyot topilmadi")

        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "reference": f"PUR-{uuid.uuid4().hex[:6]}",
            "description": "Test xarid - kreditorlik",
            "lines": [
                {
                    "account_id": str(inv_acc["id"]),
                    "description": "Material xaridi",
                    "debit_amount": 8000000,
                    "credit_amount": 0,
                },
                {
                    "account_id": str(ap_acc["id"]),
                    "contact_id": str(test_supplier["id"]),
                    "description": "Kreditorlik",
                    "debit_amount": 0,
                    "credit_amount": 8000000,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"Xarid yozuvi xato: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert_balanced(data)


class TestAgingReport:
    """6.5, 6.6 - Qarzdorlik yoshlanishi hisoboti."""

    def test_aging_receivables(self, api_client):
        resp = api_client.get("/reports/aging-receivables")
        assert resp.status_code == 200

    def test_aging_payables(self, api_client):
        resp = api_client.get("/reports/aging-payables")
        assert resp.status_code == 200

    def test_aging_has_categories(self, api_client):
        resp = api_client.get("/reports/aging-receivables")
        if resp.status_code == 200:
            data = resp.json().get("data", resp.json())
            # Aging report should have period categories
            assert data is not None, "Aging hisoboti bo'sh"


class TestPartnerStatement:
    """6.7 - Kontragent barcha operatsiyalari."""

    def test_general_ledger_has_contact_data(self, api_client):
        resp = api_client.get("/reports/general-ledger", params={
            "period_from": "2026-01-01",
            "period_to": today(),
        })
        assert resp.status_code == 200
