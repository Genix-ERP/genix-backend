"""
BOSQICH 7: SOLIQ (Tax - QQS 12%)
O'zbekiston QQS stavkasi: 12%
"""
import uuid
import pytest
from conftest import today, find_leaf_account, QQS_RATE


class TestVATOnSale:
    """7.1 - Sotishda QQS 12% qo'shiladi."""

    def test_vat_on_sale_calculation(self, api_client, accounts, journals, test_customer):
        base_amount = 10000000  # 10M UZS
        vat_amount = base_amount * QQS_RATE / 100  # 1.2M

        ar_acc = find_leaf_account(accounts, "1200", "4010")
        rev_acc = find_leaf_account(accounts, "4000", "9010", "4100")
        tax_acc = find_leaf_account(accounts, "2200", "2210", "6420", "4410")
        journal = journals.get("sales") or journals.get("general") or list(journals.values())[0]

        if not all([ar_acc, rev_acc, tax_acc]):
            pytest.skip("Kerakli schyotlar topilmadi")

        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "reference": f"VAT-SALE-{uuid.uuid4().hex[:6]}",
            "description": "Sotish + QQS 12%",
            "lines": [
                {
                    "account_id": str(ar_acc["id"]),
                    "contact_id": str(test_customer["id"]),
                    "description": "Debitorlik (summa + QQS)",
                    "debit_amount": base_amount + vat_amount,
                    "credit_amount": 0,
                },
                {
                    "account_id": str(rev_acc["id"]),
                    "description": "Sotish daromadi (QQSsiz)",
                    "debit_amount": 0,
                    "credit_amount": base_amount,
                },
                {
                    "account_id": str(tax_acc["id"]),
                    "description": "QQS hisoblangan 12%",
                    "debit_amount": 0,
                    "credit_amount": vat_amount,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"QQSli sotish xato: {resp.text}"
        data = resp.json().get("data", resp.json())
        total_dt = float(data.get("total_debit", 0))
        total_ct = float(data.get("total_credit", 0))
        assert abs(total_dt - total_ct) < 0.01, f"QQS provodka balanssiz: Dt={total_dt}, Ct={total_ct}"
        assert abs(vat_amount - base_amount * 0.12) < 0.01, "QQS summasi noto'g'ri"


class TestVATOnPurchase:
    """7.2 - Xaridda QQS ajratiladi."""

    def test_vat_on_purchase(self, api_client, accounts, journals, test_supplier):
        total_with_vat = 11200000  # 11.2M (10M + 1.2M QQS)
        base_amount = total_with_vat / (1 + QQS_RATE / 100)
        vat_amount = total_with_vat - base_amount

        ap_acc = find_leaf_account(accounts, "2000", "6010")
        inv_acc = find_leaf_account(accounts, "1300", "1010", "5000")
        tax_acc = find_leaf_account(accounts, "2200", "2210", "4420", "4410")
        journal = journals.get("purchase") or journals.get("general") or list(journals.values())[0]

        if not all([ap_acc, inv_acc, tax_acc]):
            pytest.skip("Kerakli schyotlar topilmadi")

        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "reference": f"VAT-PUR-{uuid.uuid4().hex[:6]}",
            "description": "Xarid + QQS 12%",
            "lines": [
                {
                    "account_id": str(inv_acc["id"]),
                    "description": "Material (QQSsiz)",
                    "debit_amount": round(base_amount, 2),
                    "credit_amount": 0,
                },
                {
                    "account_id": str(tax_acc["id"]),
                    "description": "QQS oldindan to'langan",
                    "debit_amount": round(vat_amount, 2),
                    "credit_amount": 0,
                },
                {
                    "account_id": str(ap_acc["id"]),
                    "contact_id": str(test_supplier["id"]),
                    "description": "Kreditorlik (summa + QQS)",
                    "debit_amount": 0,
                    "credit_amount": total_with_vat,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"QQSli xarid xato: {resp.text}"


class TestVATReport:
    """7.4 - QQS hisoboti."""

    def test_vat_report_endpoint(self, api_client):
        resp = api_client.get("/tax-reports/summary", params={
            "period_from": "2026-01-01",
            "period_to": today(),
        })
        assert resp.status_code == 200

    def test_tax_transactions(self, api_client):
        resp = api_client.get("/tax-reports/transactions", params={
            "start_date": "2026-01-01",
            "end_date": today(),
        })
        assert resp.status_code == 200


class TestZeroVAT:
    """7.5 - QQS siz operatsiya."""

    def test_zero_vat_entry(self, api_client, accounts, journals, test_customer):
        ar_acc = find_leaf_account(accounts, "1200", "4010")
        rev_acc = find_leaf_account(accounts, "4000", "4100", "9010")
        journal = journals.get("sales") or journals.get("general") or list(journals.values())[0]

        if not ar_acc or not rev_acc:
            pytest.skip("Schyotlar topilmadi")

        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "description": "QQS siz sotish",
            "lines": [
                {
                    "account_id": str(ar_acc["id"]),
                    "debit_amount": 5000000,
                    "credit_amount": 0,
                },
                {
                    "account_id": str(rev_acc["id"]),
                    "debit_amount": 0,
                    "credit_amount": 5000000,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"QQSsiz yozuv xato: {resp.text}"


class TestTaxRates:
    """Soliq stavkalari CRUD."""

    def test_list_tax_rates(self, api_client):
        resp = api_client.get("/tax-rates")
        assert resp.status_code == 200

    def test_create_tax_rate(self, api_client, accounts):
        tax_acc = accounts.get("2200") or accounts.get("2210")
        tax_account_id = str(tax_acc["id"]) if tax_acc else None

        resp = api_client.post("/tax-rates", json={
            "code": f"VAT-TEST-{uuid.uuid4().hex[:4]}",
            "name": "QQS 12% (test)",
            "rate": 12.0,
            "type": "percentage",
            "tax_type": "sales",
            "tax_account_id": tax_account_id,
        })
        assert resp.status_code in (200, 201), f"Tax rate yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        if data.get("id"):
            api_client.delete(f"/tax-rates/{data['id']}")
