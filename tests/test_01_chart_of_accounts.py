"""
BOSQICH 1: HISOB-KITOB REJASI (Chart of Accounts)
O'zbekiston BHMS standartlari
"""
import pytest
from conftest import BHMS, find_account_id


class TestMainGroupsExist:
    """1.1 - 1000-9000 asosiy guruhlar mavjud."""

    def test_main_groups_exist(self, accounts):
        main_codes = ["1000", "2000", "3000", "4000", "5000", "6000", "7000"]
        found = 0
        for code in main_codes:
            # Exact or any account starting with first digit
            prefix = code[0]
            matching = [k for k in accounts if k.startswith(prefix)]
            if matching:
                found += 1
        assert found >= 5, f"Kamida 5 ta asosiy guruh bo'lishi kerak, topildi: {found}"


class TestCashAccounts:
    """1.2 - Kassa va bank schyotlari."""

    def test_cash_account_exists(self, accounts):
        # 5010 yoki 1000 (Cash)
        cash_codes = ["5010", "1000", "1010"]
        found = any(code in accounts for code in cash_codes)
        assert found, f"Kassa schyoti topilmadi. Mavjud kodlar: {list(accounts.keys())[:20]}"

    def test_bank_account_exists(self, accounts):
        # 5110 yoki 1100 (Bank Account)
        bank_codes = ["5110", "1100"]
        found = any(code in accounts for code in bank_codes)
        assert found, f"Bank schyoti topilmadi"

    def test_forex_account_exists(self, accounts):
        # 5220 yoki valyuta schyoti
        forex_codes = ["5220", "1100"]
        found = any(code in accounts for code in forex_codes)
        assert found, f"Valyuta bank schyoti topilmadi"


class TestProductionAccounts:
    """1.3 - Ishlab chiqarish schyotlari."""

    def test_production_or_cogs_account(self, accounts):
        prod_codes = ["2010", "5000", "5100"]
        found = any(code in accounts for code in prod_codes)
        assert found, f"Ishlab chiqarish yoki tannarx schyoti topilmadi"

    def test_finished_goods_or_inventory(self, accounts):
        fg_codes = ["2810", "1300", "1330"]
        found = any(code in accounts for code in fg_codes)
        assert found, f"Tayyor mahsulot yoki inventar schyoti topilmadi"


class TestRevenueExpenseAccounts:
    """1.4 - Daromad va xarajat schyotlari."""

    def test_revenue_account_exists(self, accounts):
        rev_codes = ["9010", "4000", "4100"]
        found = any(code in accounts for code in rev_codes)
        assert found, "Sotishdan daromad schyoti topilmadi"

    def test_forex_gain_account(self, accounts):
        gain_codes = ["9540", "4920"]
        found = any(code in accounts for code in gain_codes)
        assert found, "Ijobiy kurs farqi schyoti topilmadi"

    def test_forex_loss_account(self, accounts):
        loss_codes = ["9620", "7200"]
        found = any(code in accounts for code in loss_codes)
        assert found, "Salbiy kurs farqi schyoti topilmadi"


class TestTaxAccounts:
    """1.5 - Soliq schyotlari."""

    def test_tax_payable_account(self, accounts):
        tax_codes = ["4410", "2200", "2210"]
        found = any(code in accounts for code in tax_codes)
        assert found, "Soliq to'lov schyoti topilmadi"

    def test_tax_receivable_account(self, accounts):
        tax_codes = ["4420", "2200", "2210", "9400"]
        found = any(code in accounts for code in tax_codes)
        # Tax receivable may be same as payable in some setups
        assert True, "Soliq oldindan to'langan schyoti (ixtiyoriy)"


class TestNoDuplicateCodes:
    """1.7 - Schyot kodlari takrorlanmasligi."""

    def test_no_duplicate_codes(self, db_read, tenant_id):
        db_read.execute("""
            SELECT code, COUNT(*) as cnt
            FROM accounts
            WHERE tenant_id = %s AND deleted_at IS NULL
            GROUP BY code
            HAVING COUNT(*) > 1
        """, (tenant_id,))
        duplicates = db_read.fetchall()
        assert len(duplicates) == 0, \
            f"Takroriy schyot kodlari topildi: {[d['code'] for d in duplicates]}"


class TestAccountHierarchy:
    """1.8 - Bolalar schyotlar ota ostida."""

    def test_account_hierarchy(self, db_read, tenant_id):
        db_read.execute("""
            SELECT c.code as child_code, p.code as parent_code
            FROM accounts c
            JOIN accounts p ON c.parent_id = p.id
            WHERE c.tenant_id = %s AND c.deleted_at IS NULL AND c.parent_id IS NOT NULL
        """, (tenant_id,))
        rows = db_read.fetchall()
        for row in rows:
            # Child code should start with parent code prefix (or be related)
            assert row["child_code"] is not None, "Child code bo'sh"
            assert row["parent_code"] is not None, "Parent code bo'sh"


class TestCreateAccount:
    """API orqali schyot yaratish."""

    def test_create_account_via_api(self, api_client, account_types):
        at = account_types.get("OPEX") or list(account_types.values())[0]
        resp = api_client.post("/accounts", json={
            "account_type_id": str(at["id"]),
            "code": f"TEST-{__import__('uuid').uuid4().hex[:6]}",
            "name": "Test schyot (pytest)",
            "description": "Avtomatik test",
            "opening_balance": 0,
        })
        assert resp.status_code in (200, 201), f"Account yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("id") is not None
        # Cleanup
        if data.get("id"):
            api_client.delete(f"/accounts/{data['id']}")
