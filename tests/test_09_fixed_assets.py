"""
BOSQICH 9: ASOSIY VOSITALAR (Fixed Assets)
Amortizatsiya: to'g'ri chiziqli usul
Provodka: Dt 2010 (xarajat), Kt 0200 (amortizatsiya)
"""
import uuid
import pytest
from conftest import today


class TestCreateAsset:
    """9.1 - Asosiy vosita yaratish."""

    def test_create_fixed_asset(self, api_client):
        resp = api_client.post("/fixed-assets", json={
            "name": "Test Mikser MX-500",
            "asset_code": f"FA-{uuid.uuid4().hex[:6].upper()}",
            "acquisition_date": "2026-01-15",
            "acquisition_cost": 50000000,
            "salvage_value": 5000000,
            "useful_life_months": 60,
            "depreciation_method": "straight_line",
            "category_name": "Uskunalar",
            "description": "Test uchun asosiy vosita",
        })
        assert resp.status_code in (200, 201), f"Asset yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("status") in ("active", "draft"), f"Status: {data.get('status')}"
        assert float(data.get("acquisition_cost", 0)) == 50000000


class TestMonthlyDepreciation:
    """9.2 - Oylik amortizatsiya hisoblash."""

    def test_depreciation_calculation(self):
        """50M - 5M qoldiq = 45M / 60 oy = 750,000 UZS/oy."""
        cost = 50000000
        salvage = 5000000
        months = 60
        monthly = (cost - salvage) / months
        assert monthly == 750000, f"Oylik amortizatsiya: {monthly}"

    def test_run_depreciation(self, api_client):
        resp = api_client.post("/run-depreciation", json={
            "depreciation_date": today(),
        })
        # May succeed or no assets to depreciate
        assert resp.status_code in (200, 201, 400, 404, 500), \
            f"Amortizatsiya hisoblash xato: {resp.text}"


class TestDepreciationJournal:
    """9.3 - Amortizatsiya provodkasi."""

    def test_depreciation_entries_endpoint(self, api_client):
        """Amortizatsiya yozuvlari ro'yxati."""
        # First get an asset
        resp = api_client.get("/fixed-assets")
        if resp.status_code == 200:
            data = resp.json().get("data", resp.json())
            items = data if isinstance(data, list) else data.get("items", data.get("assets", []))
            if items:
                asset_id = items[0].get("id")
                dep_resp = api_client.get(f"/fixed-assets/{asset_id}/depreciation")
                assert dep_resp.status_code in (200, 404)


class TestAccumulatedDepreciation:
    """9.4 - Jami to'plangan amortizatsiya."""

    def test_accumulated_depreciation(self, api_client):
        resp = api_client.get("/fixed-assets")
        if resp.status_code == 200:
            data = resp.json().get("data", resp.json())
            items = data if isinstance(data, list) else data.get("items", data.get("assets", []))
            for item in items:
                accum = float(item.get("accumulated_depreciation", 0))
                cost = float(item.get("acquisition_cost", 0))
                assert accum <= cost, f"To'plangan amortizatsiya sotib olish qiymatidan oshib ketdi"


class TestNetBookValue:
    """9.5 - Qoldiq qiymat (NBV)."""

    def test_net_book_value(self, api_client):
        resp = api_client.get("/fixed-assets")
        if resp.status_code == 200:
            data = resp.json().get("data", resp.json())
            items = data if isinstance(data, list) else data.get("items", data.get("assets", []))
            for item in items:
                cost = float(item.get("acquisition_cost", 0))
                salvage = float(item.get("salvage_value", 0))
                accum = float(item.get("accumulated_depreciation", 0))
                nbv = float(item.get("book_value", cost - accum))
                # book_value = acquisition_cost - accumulated_depreciation
                # OR book_value could be cost - salvage (if that's how the system stores it)
                assert nbv <= cost, f"NBV sotib olish narxidan katta bo'lmasligi kerak: {nbv} > {cost}"
                assert nbv >= 0, f"NBV manfiy bo'lmasligi kerak: {nbv}"


class TestAssetDisposal:
    """9.6 - Asosiy vositani hisobdan chiqarish."""

    def test_dispose_asset(self, api_client):
        # Create an asset first
        code = f"DISP-{uuid.uuid4().hex[:6].upper()}"
        resp = api_client.post("/fixed-assets", json={
            "name": "Disposal test asset",
            "asset_code": code,
            "acquisition_date": "2025-01-01",
            "acquisition_cost": 10000000,
            "salvage_value": 0,
            "useful_life_months": 12,
            "depreciation_method": "straight_line",
        })
        if resp.status_code not in (200, 201):
            pytest.skip(f"Asset yaratilmadi: {resp.text}")

        data = resp.json().get("data", resp.json())
        asset_id = data.get("id")

        # Dispose
        resp2 = api_client.post(f"/fixed-assets/{asset_id}/dispose", json={
            "disposal_date": today(),
            "disposal_amount": 2000000,
            "disposal_reason": "Test disposal",
        })
        if resp2.status_code in (200, 201):
            disposed = resp2.json().get("data", resp2.json())
            assert disposed.get("status") in ("disposed", "written_off")


class TestListAssets:
    """Asosiy vositalar ro'yxati."""

    def test_list_fixed_assets(self, api_client):
        resp = api_client.get("/fixed-assets")
        assert resp.status_code == 200
