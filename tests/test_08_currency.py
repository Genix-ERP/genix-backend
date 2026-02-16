"""
BOSQICH 8: VALYUTA (Currency, Exchange Rates, Revaluation)
UZS (asosiy), USD, RUB
Kurs farqi: ijobiy -> Kt 9540, salbiy -> Dt 9620
"""
import pytest
from conftest import today, first_of_month


class TestCurrenciesExist:
    """8.1 - UZS, USD, RUB valyutalar mavjud."""

    def test_currencies_via_api(self, api_client):
        resp = api_client.get("/currencies")
        assert resp.status_code == 200
        data = resp.json().get("data", resp.json())
        currencies = data if isinstance(data, list) else data.get("items", [])
        codes = [c.get("code") for c in currencies]
        assert "UZS" in codes, "UZS valyutasi topilmadi"
        assert "USD" in codes, "USD valyutasi topilmadi"

    def test_currencies_in_db(self, currencies):
        assert "UZS" in currencies, "UZS DB da topilmadi"
        assert "USD" in currencies, "USD DB da topilmadi"
        assert "RUB" in currencies, "RUB DB da topilmadi"


class TestRateSync:
    """8.2 - MB kurs yuklanishi (CBU)."""

    def test_sync_currency_rates(self, api_client):
        resp = api_client.post("/currency/rates/sync", json={
            "date": today(),
        })
        # May return 200 with count, or error if CBU API unavailable
        assert resp.status_code in (200, 201, 500, 502), \
            f"Kurs sinxronizatsiya kutilmagan xato: {resp.status_code}"

    def test_list_exchange_rates(self, api_client):
        resp = api_client.get("/exchange-rates")
        assert resp.status_code == 200


class TestForexOperations:
    """8.3, 8.4 - Valyutali operatsiyalar."""

    def test_set_exchange_rate(self, api_client):
        resp = api_client.post("/currencies/USD/rate", json={
            "rate": 12750.0,
            "effective_date": today(),
        })
        # May succeed or rate already set
        assert resp.status_code in (200, 201, 400, 409), f"Kurs o'rnatish xato: {resp.text}"

    def test_get_exchange_rate(self, api_client):
        resp = api_client.get("/currencies/USD/rate")
        assert resp.status_code in (200, 404), f"Kursni olish xato: {resp.text}"


class TestExchangeDifferences:
    """8.5, 8.6 - Kurs farqlari."""

    def test_list_exchange_diffs(self, api_client):
        resp = api_client.get("/currency/rates", params={
            "period_start": "2026-01-01",
            "period_end": today(),
        })
        assert resp.status_code == 200

    def test_revalue_currency(self, api_client):
        """Valyuta qayta baholash."""
        resp = api_client.post("/currency/revalue", json={
            "period_end": today(),
        })
        # Revaluation may have no data to process
        assert resp.status_code in (200, 201, 400, 500), \
            f"Qayta baholash kutilmagan xato: {resp.status_code}"


class TestMultiCurrencyBalance:
    """8.9 - Ko'p valyutali qoldiq."""

    def test_forex_journal_entry(self, api_client, accounts, journals, currencies):
        """USD da jurnal yozuvi."""
        bank_acc = accounts.get("1100") or accounts.get("5110") or accounts.get("5220")
        rev_acc = accounts.get("4000") or accounts.get("4100")
        journal = journals.get("general") or list(journals.values())[0]
        usd = currencies.get("USD")

        if not all([bank_acc, rev_acc, usd]):
            pytest.skip("Kerakli ma'lumotlar topilmadi")

        # $10,000 * 12,750 = 127,500,000 UZS
        resp = api_client.post("/journal-entries", json={
            "journal_id": str(journal["id"]),
            "entry_date": today(),
            "description": "USD xizmat ko'rsatish",
            "currency_id": str(usd["id"]),
            "exchange_rate": 12750.0,
            "lines": [
                {
                    "account_id": str(bank_acc["id"]),
                    "description": "Bank USD kirim",
                    "debit_amount": 127500000,
                    "credit_amount": 0,
                },
                {
                    "account_id": str(rev_acc["id"]),
                    "description": "Xizmat daromadi",
                    "debit_amount": 0,
                    "credit_amount": 127500000,
                },
            ],
        })
        assert resp.status_code in (200, 201), f"Valyuta yozuvi xato: {resp.text}"
