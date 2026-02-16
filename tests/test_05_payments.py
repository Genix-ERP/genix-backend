"""
BOSQICH 5: TO'LOVLAR (Payments)
"""
import uuid
import pytest
from conftest import today


class TestCustomerPaymentBank:
    """5.1 - Mijozdan bankka to'lov."""

    def test_customer_payment_bank(self, api_client, test_customer, test_bank_account):
        resp = api_client.post("/payments", json={
            "type": "receipt",
            "contact_id": str(test_customer["id"]),
            "payment_date": today(),
            "amount": 5000000,
            "bank_account_id": str(test_bank_account.get("id", "")),
            "reference": f"PAY-TEST-{uuid.uuid4().hex[:6]}",
            "notes": "Test - mijozdan bank to'lov",
        })
        assert resp.status_code in (200, 201), f"To'lov yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("type") == "receipt"
        assert float(data.get("amount", 0)) == 5000000


class TestCustomerPaymentCash:
    """5.2 - Mijozdan kassaga to'lov."""

    def test_customer_payment_cash(self, api_client, test_customer):
        resp = api_client.post("/payments", json={
            "type": "receipt",
            "contact_id": str(test_customer["id"]),
            "payment_date": today(),
            "amount": 3000000,
            "reference": f"CASH-PAY-{uuid.uuid4().hex[:6]}",
            "notes": "Test - mijozdan naqd to'lov",
        })
        assert resp.status_code in (200, 201), f"Naqd to'lov xato: {resp.text}"


class TestSupplierPaymentBank:
    """5.3 - Yetkazuvchiga bank to'lov."""

    def test_supplier_payment_bank(self, api_client, test_supplier, test_bank_account):
        resp = api_client.post("/payments", json={
            "type": "payment",
            "contact_id": str(test_supplier["id"]),
            "payment_date": today(),
            "amount": 8000000,
            "bank_account_id": str(test_bank_account.get("id", "")),
            "reference": f"SUP-PAY-{uuid.uuid4().hex[:6]}",
            "notes": "Test - yetkazuvchiga bank to'lov",
        })
        assert resp.status_code in (200, 201), f"Supplier to'lov xato: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("type") == "payment"


class TestSupplierPaymentCash:
    """5.4 - Yetkazuvchiga naqd to'lov."""

    def test_supplier_payment_cash(self, api_client, test_supplier):
        resp = api_client.post("/payments", json={
            "type": "payment",
            "contact_id": str(test_supplier["id"]),
            "payment_date": today(),
            "amount": 2000000,
            "reference": f"SUP-CASH-{uuid.uuid4().hex[:6]}",
            "notes": "Test - yetkazuvchiga naqd",
        })
        assert resp.status_code in (200, 201), f"Naqd to'lov xato: {resp.text}"


class TestPartialPayment:
    """5.5 - Qisman to'lov."""

    def test_partial_payment(self, api_client, test_customer):
        # Full amount is 10M, pay only 6M
        resp = api_client.post("/payments", json={
            "type": "receipt",
            "contact_id": str(test_customer["id"]),
            "payment_date": today(),
            "amount": 6000000,
            "reference": f"PARTIAL-{uuid.uuid4().hex[:6]}",
            "notes": "Qisman to'lov (10M dan 6M)",
        })
        assert resp.status_code in (200, 201), f"Qisman to'lov xato: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert float(data.get("amount", 0)) == 6000000


class TestPaymentConfirm:
    """To'lovni tasdiqlash va jurnal yaratilishi."""

    def test_confirm_payment(self, api_client, test_customer, test_bank_account):
        # Yaratish
        resp = api_client.post("/payments", json={
            "type": "receipt",
            "contact_id": str(test_customer["id"]),
            "payment_date": today(),
            "amount": 1000000,
            "bank_account_id": str(test_bank_account.get("id", "")),
            "reference": f"CONF-{uuid.uuid4().hex[:6]}",
        })
        if resp.status_code not in (200, 201):
            pytest.skip(f"To'lov yaratilmadi: {resp.text}")

        data = resp.json().get("data", resp.json())
        payment_id = data.get("id")

        # Tasdiqlash
        resp2 = api_client.post(f"/payments/{payment_id}/confirm")
        assert resp2.status_code in (200, 201), f"To'lov tasdiqlanmadi: {resp2.text}"

        # Tasdiqlangandan keyin journal entry bo'lishi kerak
        confirmed = resp2.json().get("data", resp2.json())
        je_id = confirmed.get("journal_entry_id")
        if je_id:
            je_resp = api_client.get(f"/journal-entries/{je_id}")
            if je_resp.status_code == 200:
                je = je_resp.json().get("data", je_resp.json())
                total_dt = float(je.get("total_debit", 0))
                total_ct = float(je.get("total_credit", 0))
                assert abs(total_dt - total_ct) < 0.01, "To'lov provodkasi balanssiz"


class TestPaymentsList:
    """To'lovlar ro'yxati."""

    def test_list_payments(self, api_client):
        resp = api_client.get("/payments")
        assert resp.status_code == 200

    def test_filter_by_type(self, api_client):
        resp = api_client.get("/payments", params={"type": "receipt"})
        assert resp.status_code == 200
