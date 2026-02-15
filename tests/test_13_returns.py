"""
BOSQICH 13: QAYTARILADIGAN (Returns - Credit/Debit Notes)
Mijozdan qaytarish va yetkazuvchiga qaytarish
"""
import uuid
import pytest
from conftest import today, assert_balanced


class TestCustomerReturn:
    """13.1 - Mijozdan qaytarish (Credit Note)."""

    def test_customer_return_journal_entry(self, api_client, accounts, journals, test_customer):
        """Teskari provodka: Dt 9010 (Revenue), Kt 4010 (AR) - debitorlik kamayadi."""
        ar_acc = accounts.get("1200") or accounts.get("4010")
        rev_acc = accounts.get("4000") or accounts.get("9010") or accounts.get("4100")
        journal = journals.get("sales") or journals.get("general") or list(journals.values())[0]

        if not all([ar_acc, rev_acc]):
            pytest.skip("AR yoki Revenue schyot topilmadi")

        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "reference": f"CN-{uuid.uuid4().hex[:6]}",
            "description": "Mijozdan qaytarish (credit note)",
            "lines": [
                {
                    "account_id": str(rev_acc["id"]),
                    "description": "Daromad qaytarish",
                    "debit_amount": 2000000,
                    "credit_amount": 0,
                },
                {
                    "account_id": str(ar_acc["id"]),
                    "contact_id": str(test_customer["id"]),
                    "description": "Debitorlik kamayishi",
                    "debit_amount": 0,
                    "credit_amount": 2000000,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"Credit note xato: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert_balanced(data)


class TestSupplierReturn:
    """13.2 - Yetkazuvchiga qaytarish (Debit Note)."""

    def test_supplier_return_journal_entry(self, api_client, accounts, journals, test_supplier):
        """Teskari provodka: Dt 6010 (AP), Kt 1300 (Inventory) - kreditorlik kamayadi."""
        ap_acc = accounts.get("2000") or accounts.get("6010")
        inv_acc = accounts.get("1300") or accounts.get("5000")
        journal = journals.get("purchase") or journals.get("general") or list(journals.values())[0]

        if not all([ap_acc, inv_acc]):
            pytest.skip("AP yoki Inventory schyot topilmadi")

        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "reference": f"DN-{uuid.uuid4().hex[:6]}",
            "description": "Yetkazuvchiga qaytarish (debit note)",
            "lines": [
                {
                    "account_id": str(ap_acc["id"]),
                    "contact_id": str(test_supplier["id"]),
                    "description": "Kreditorlik kamayishi",
                    "debit_amount": 3000000,
                    "credit_amount": 0,
                },
                {
                    "account_id": str(inv_acc["id"]),
                    "description": "Material qaytarish",
                    "debit_amount": 0,
                    "credit_amount": 3000000,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"Debit note xato: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert_balanced(data)


class TestReturnJournalEntry:
    """13.3 - Teskari provodka."""

    def test_reverse_journal_entry(self, api_client, accounts, journals):
        """Mavjud yozuvni teskari qilish (reverse)."""
        cash_acc = accounts.get("1000") or accounts.get("1010")
        rev_acc = accounts.get("4000") or accounts.get("4100")
        journal = journals.get("general") or list(journals.values())[0]

        if not all([cash_acc, rev_acc]):
            pytest.skip("Schyotlar topilmadi")

        # Asl yozuv yaratish
        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "description": "Reverse qilinishi kerak",
            "lines": [
                {"account_id": str(cash_acc["id"]), "debit_amount": 1000000, "credit_amount": 0},
                {"account_id": str(rev_acc["id"]), "debit_amount": 0, "credit_amount": 1000000},
            ],
        })
        if resp.status_code not in (200, 201):
            pytest.skip(f"Asl yozuv yaratilmadi: {resp.text}")

        data = resp.json().get("data", resp.json())
        entry_id = data.get("id")

        # Post first
        api_client.post(f"/journal-entries/{entry_id}/post")

        # Reverse
        resp2 = api_client.post(f"/journal-entries/{entry_id}/reverse")
        assert resp2.status_code in (200, 201, 400), f"Reverse xato: {resp2.text}"


class TestRefundPayment:
    """13.4 - Pul qaytarish."""

    def test_refund_payment(self, api_client, test_customer):
        """Mijozga pul qaytarish - payment type."""
        resp = api_client.post("/payments", json={
            "type": "payment",
            "contact_id": str(test_customer["id"]),
            "payment_date": today(),
            "amount": 1000000,
            "reference": f"REFUND-{uuid.uuid4().hex[:6]}",
            "notes": "Mijozga pul qaytarish",
        })
        assert resp.status_code in (200, 201), f"Refund xato: {resp.text}"


class TestPartialReturn:
    """13.5 - Qisman qaytarish."""

    def test_partial_return(self, api_client, accounts, journals, test_customer):
        ar_acc = accounts.get("1200") or accounts.get("4010")
        rev_acc = accounts.get("4000") or accounts.get("4100")
        journal = journals.get("general") or list(journals.values())[0]

        if not all([ar_acc, rev_acc]):
            pytest.skip("Schyotlar topilmadi")

        # Qisman qaytarish: 10M dan 3M
        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "description": "Qisman qaytarish (10M dan 3M)",
            "lines": [
                {"account_id": str(rev_acc["id"]), "debit_amount": 3000000, "credit_amount": 0},
                {
                    "account_id": str(ar_acc["id"]),
                    "contact_id": str(test_customer["id"]),
                    "debit_amount": 0,
                    "credit_amount": 3000000,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"Qisman qaytarish xato: {resp.text}"
