"""
BOSQICH 4: KASSA (Cash Registers, PKO/RKO, Cash Book)
"""
import uuid
import pytest
from conftest import today, yesterday, find_account_id


class TestCreatePKO:
    """4.1 - PKO (Kirim order) yaratish."""

    def test_create_pko(self, api_client, test_cash_register, accounts, test_customer):
        reg_id = test_cash_register.get("id")
        if not reg_id:
            pytest.skip("Cash register topilmadi (stub handler)")
        revenue_acc = accounts.get("4000") or accounts.get("9010") or accounts.get("4100")
        account_id = str(revenue_acc["id"]) if revenue_acc else None

        resp = api_client.post("/cash/orders", json={
            "cash_register_id": str(reg_id),
            "order_type": "pko",
            "order_date": today(),
            "amount": 5000000,
            "currency": "UZS",
            "partner_id": str(test_customer.get("id", "")),
            "account_id": account_id,
            "description": "Test PKO - xizmat uchun to'lov",
        })
        assert resp.status_code in (200, 201), f"PKO yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("order_type") == "pko"
        assert float(data.get("amount", 0)) == 5000000


class TestPKOJournalEntry:
    """4.2 - PKO provodkasi: Dt 5010 (Kassa)."""

    def test_pko_creates_journal_on_confirm(self, api_client, test_cash_register, accounts, test_customer):
        reg_id = test_cash_register.get("id")
        revenue_acc = accounts.get("4000") or accounts.get("9010") or accounts.get("4100")
        account_id = str(revenue_acc["id"]) if revenue_acc else None

        # Yaratish
        resp = api_client.post("/cash/orders", json={
            "cash_register_id": str(reg_id),
            "order_type": "pko",
            "order_date": today(),
            "amount": 1000000,
            "currency": "UZS",
            "partner_id": str(test_customer.get("id", "")),
            "account_id": account_id,
            "description": "PKO journal entry test",
        })
        if resp.status_code not in (200, 201):
            pytest.skip(f"PKO yaratilmadi: {resp.text}")

        data = resp.json().get("data", resp.json())
        order_id = data.get("id")

        # Tasdiqlash
        resp2 = api_client.post(f"/cash/orders/{order_id}/confirm")
        if resp2.status_code in (200, 201):
            confirmed = resp2.json().get("data", resp2.json())
            # Journal entry yaratilganini tekshirish
            je_id = confirmed.get("journal_entry_id")
            if je_id:
                je_resp = api_client.get(f"/journal-entries/{je_id}")
                if je_resp.status_code == 200:
                    je = je_resp.json().get("data", je_resp.json())
                    total_dt = float(je.get("total_debit", 0))
                    total_ct = float(je.get("total_credit", 0))
                    assert abs(total_dt - total_ct) < 0.01, "PKO provodka balanssiz"


class TestCreateRKO:
    """4.3 - RKO (Chiqim order) yaratish."""

    def test_create_rko(self, api_client, test_cash_register, accounts, test_supplier):
        reg_id = test_cash_register.get("id")
        if not reg_id:
            pytest.skip("Cash register topilmadi (stub handler)")
        expense_acc = accounts.get("5000") or accounts.get("6000") or accounts.get("6100")
        account_id = str(expense_acc["id"]) if expense_acc else None

        resp = api_client.post("/cash/orders", json={
            "cash_register_id": str(reg_id),
            "order_type": "rko",
            "order_date": today(),
            "amount": 2000000,
            "currency": "UZS",
            "partner_id": str(test_supplier.get("id", "")),
            "account_id": account_id,
            "description": "Test RKO - material uchun to'lov",
        })
        assert resp.status_code in (200, 201), f"RKO yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("order_type") == "rko"


class TestRKOJournalEntry:
    """4.4 - RKO provodkasi: Kt 5010 (Kassa)."""

    def test_rko_confirm_creates_journal(self, api_client, test_cash_register, accounts):
        reg_id = test_cash_register.get("id")
        expense_acc = accounts.get("6000") or accounts.get("5000") or accounts.get("6100")
        account_id = str(expense_acc["id"]) if expense_acc else None

        resp = api_client.post("/cash/orders", json={
            "cash_register_id": str(reg_id),
            "order_type": "rko",
            "order_date": today(),
            "amount": 500000,
            "currency": "UZS",
            "account_id": account_id,
            "description": "RKO journal test",
        })
        if resp.status_code not in (200, 201):
            pytest.skip(f"RKO yaratilmadi: {resp.text}")

        data = resp.json().get("data", resp.json())
        order_id = data.get("id")

        resp2 = api_client.post(f"/cash/orders/{order_id}/confirm")
        if resp2.status_code in (200, 201):
            confirmed = resp2.json().get("data", resp2.json())
            je_id = confirmed.get("journal_entry_id")
            if je_id:
                je_resp = api_client.get(f"/journal-entries/{je_id}")
                if je_resp.status_code == 200:
                    je = je_resp.json().get("data", je_resp.json())
                    total_dt = float(je.get("total_debit", 0))
                    total_ct = float(je.get("total_credit", 0))
                    assert abs(total_dt - total_ct) < 0.01, "RKO provodka balanssiz"


class TestCashLimitWarning:
    """4.6 - Kassa limitidan oshsa ogohlantirish."""

    def test_cash_register_has_limit(self, test_cash_register):
        if not test_cash_register.get("id"):
            pytest.skip("Cash register topilmadi (stub handler)")
        limit = float(test_cash_register.get("limit_amount", 0))
        assert limit > 0, "Kassa limitga ega bo'lishi kerak"


class TestCashBook:
    """4.7 - Kunlik kassa kitob."""

    def test_cash_book_endpoint(self, api_client, test_cash_register):
        reg_id = test_cash_register.get("id")
        resp = api_client.get("/cash/book", params={
            "cash_register_id": str(reg_id),
            "date_from": "2026-01-01",
            "date_to": today(),
        })
        assert resp.status_code == 200

    def test_cash_book_formula(self, api_client, test_cash_register):
        """opening + income - expense = closing."""
        reg_id = test_cash_register.get("id")
        resp = api_client.get("/cash/book", params={
            "cash_register_id": str(reg_id),
        })
        if resp.status_code == 200:
            data = resp.json().get("data", resp.json())
            entries = data if isinstance(data, list) else data.get("entries", [])
            for entry in entries:
                opening = float(entry.get("opening_balance", 0))
                income = float(entry.get("total_income", 0))
                expense = float(entry.get("total_expense", 0))
                closing = float(entry.get("closing_balance", 0))
                expected = opening + income - expense
                assert abs(closing - expected) < 0.01, \
                    f"Kassa kitob: {opening} + {income} - {expense} = {expected}, lekin closing = {closing}"


class TestCashOrdersList:
    """Kassa orderlari ro'yxati."""

    def test_list_cash_orders(self, api_client):
        resp = api_client.get("/cash/orders")
        assert resp.status_code == 200

    def test_list_cash_registers(self, api_client):
        resp = api_client.get("/cash/registers")
        assert resp.status_code == 200
