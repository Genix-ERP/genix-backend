"""
BOSQICH 3: BANK (Bank Accounts & Transactions)
"""
import uuid
import pytest
from conftest import find_account_id, today


class TestCreateBankAccount:
    """3.1 - Bank schyot yaratish (UZS)."""

    def test_create_bank_account_uzs(self, api_client, accounts):
        resp = api_client.post("/bank-accounts", json={
            "name": f"Test Bank {uuid.uuid4().hex[:4]}",
            "bank_name": "Test Bank",
            "account_number": f"20208000{uuid.uuid4().hex[:10]}",
            "currency": "UZS",
            "account_type": "checking",
            "balance": 0,
        })
        assert resp.status_code in (200, 201), f"Bank account yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        balance = float(data.get("balance", data.get("current_balance", 0)))
        assert balance == 0.0, f"Yangi bank qoldig'i 0 bo'lishi kerak: {balance}"
        if data.get("id"):
            api_client.delete(f"/bank-accounts/{data['id']}")


class TestCreateForexAccount:
    """3.2 - Valyuta bank schyot (USD)."""

    def test_create_forex_account(self, api_client, accounts):
        resp = api_client.post("/bank-accounts", json={
            "name": f"Test USD Bank {uuid.uuid4().hex[:4]}",
            "bank_name": "USD Test Bank",
            "account_number": f"20208840{uuid.uuid4().hex[:10]}",
            "currency": "USD",
            "account_type": "checking",
            "balance": 0,
        })
        assert resp.status_code in (200, 201), f"USD bank yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("id") is not None
        if data.get("id"):
            api_client.delete(f"/bank-accounts/{data['id']}")


class TestBankIncome:
    """3.3 - Bankka kirim."""

    def test_bank_income_transaction(self, api_client, test_bank_account):
        ba_id = test_bank_account.get("id")
        if not ba_id:
            pytest.skip("Bank account topilmadi")

        resp = api_client.post(f"/bank-accounts/{ba_id}/transactions", json={
            "transaction_date": today(),
            "reference": "TEST-INC-001",
            "description": "Test kirim operatsiyasi",
            "amount": 10000000,
            "type": "credit",
        })
        assert resp.status_code in (200, 201), f"Bank kirim xato: {resp.text}"


class TestBankExpense:
    """3.4 - Bankdan chiqim."""

    def test_bank_expense_transaction(self, api_client, test_bank_account):
        ba_id = test_bank_account.get("id")
        if not ba_id:
            pytest.skip("Bank account topilmadi")

        resp = api_client.post(f"/bank-accounts/{ba_id}/transactions", json={
            "transaction_date": today(),
            "reference": "TEST-EXP-001",
            "description": "Test chiqim operatsiyasi",
            "amount": 3000000,
            "type": "debit",
        })
        assert resp.status_code in (200, 201), f"Bank chiqim xato: {resp.text}"


class TestBankList:
    """Bank schyotlar ro'yxati."""

    def test_list_bank_accounts(self, api_client):
        resp = api_client.get("/bank-accounts")
        assert resp.status_code == 200

    def test_bank_transactions_list(self, api_client, test_bank_account):
        ba_id = test_bank_account.get("id")
        if not ba_id:
            pytest.skip("Bank account topilmadi")
        resp = api_client.get(f"/bank-accounts/{ba_id}/transactions")
        assert resp.status_code == 200
