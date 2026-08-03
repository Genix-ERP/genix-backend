"""
BOSQICH 9: ASOSIY VOSITALAR (Aktivlar v2 — unified register, 2026-08-03 rebuild)

Exercises the fa_* engine end-to-end:
  - unified register (/assets) with mapping-driven accounts
  - depreciation starts the month AFTER commissioning
  - straight-line schedule sums exactly to cost - salvage (rounding tail)
  - run journal: draft -> post -> 409 on re-run -> reverse frees the period
  - disposal with sale price -> gain/loss in the response
  - stats, run list + gap detection, register<->GL reconciliation
The legacy /fixed-assets endpoints were removed by migration 453.
"""
import uuid
from datetime import date

import pytest


def months_ago_first(n):
    """First day of the month n months before today (YYYY-MM-DD)."""
    y, m = date.today().year, date.today().month - n
    while m <= 0:
        y, m = y - 1, m + 12
    return f"{y:04d}-{m:02d}-01"


def period_of(datestr):
    return datestr[:7]


def add_month(period):
    y, m = int(period[:4]), int(period[5:7])
    m += 1
    if m > 12:
        y, m = y + 1, 1
    return f"{y:04d}-{m:02d}"


@pytest.fixture(scope="module")
def mapping(api_client):
    resp = api_client.get("/settings/asset-mapping")
    assert resp.status_code == 200, f"Mapping olinmadi: {resp.text}"
    data = resp.json()["data"]
    cats = [c for c in data["categories"] if c.get("depreciable") and c.get("is_active", True)]
    depts = [d for d in data["departments"] if d.get("is_active", True)]
    assert cats and depts, "Mapping bo'sh — 437/453 seedlari ishlamagan"
    return {"category": cats[0], "department": depts[0], "settings": data["settings"]}


def create_asset(api_client, mapping, *, cost, salvage=0, life=12, commission_now=False,
                 commissioning_date="", name_prefix="Test"):
    resp = api_client.post("/assets", json={
        "name": f"{name_prefix} {uuid.uuid4().hex[:6].upper()}",
        "category_id": mapping["category"]["id"],
        "department_id": mapping["department"]["id"],
        "cost": cost,
        "salvage_value": salvage,
        "useful_life_months": life,
        "purchase_date": commissioning_date or months_ago_first(3),
        "payment_method": "credit",
        "commission_now": commission_now,
        "commissioning_date": commissioning_date,
    })
    assert resp.status_code == 200, f"Aktiv yaratilmadi: {resp.text}"
    return resp.json()["data"]


class TestCreateAsset:
    """9.1 — yaratish, avto inventar raqam, draft -> commission."""

    def test_create_draft_then_commission(self, api_client, mapping):
        a = create_asset(api_client, mapping, cost=50_000_000, salvage=5_000_000, life=60)
        assert a["status"] == "draft"
        assert a["inventory_number"], "Inventar raqami avtomatik berilishi kerak"

        comm = months_ago_first(3)
        resp = api_client.post(f"/assets/{a['id']}/commission",
                               json={"commissioning_date": comm})
        assert resp.status_code == 200, f"Commission xato: {resp.text}"
        assert resp.json()["data"]["status"] == "in_service"

    def test_mapping_is_required(self, api_client, mapping):
        resp = api_client.post("/assets", json={
            "name": "No category",
            "category_id": str(uuid.uuid4()),
            "department_id": mapping["department"]["id"],
            "cost": 1_000_000, "useful_life_months": 12,
            "purchase_date": months_ago_first(1),
        })
        assert resp.status_code == 400, "Mavjud bo'lmagan kategoriya rad etilishi kerak"


class TestScheduleMath:
    """9.2 — jadval: keyingi oydan boshlanadi, qoldiq oxirgi oyda yutiladi."""

    def test_schedule_starts_next_month_and_sums_exactly(self, api_client, mapping):
        comm = months_ago_first(1)
        a = create_asset(api_client, mapping, cost=10_000_000, salvage=0, life=36,
                         commission_now=True, commissioning_date=comm)
        resp = api_client.get(f"/assets/{a['id']}/schedule")
        assert resp.status_code == 200, resp.text
        sched = resp.json()["data"]
        assert len(sched) == 36, f"36 davr kutilgan edi, {len(sched)} keldi"
        assert sched[0]["period"] == add_month(period_of(comm)), \
            "Amortizatsiya foydalanishga topshirilgan oydan KEYINGI oydan boshlanadi"
        total = round(sum(r["amount"] for r in sched), 2)
        assert total == 10_000_000.00, f"Jadval jami {total}, 10 000 000.00 bo'lishi shart (drift yo'q)"
        assert sched[-1]["book_value"] == 0, "Oxirgi oyda qoldiq 0 bo'lishi kerak"


class TestDepreciationRunGuardrails:
    """9.3 — reglament: bitta davr faqat bir marta; revers davri bo'shatadi."""

    def test_run_lifecycle_and_double_run_guard(self, api_client, mapping):
        comm = months_ago_first(2)
        a = create_asset(api_client, mapping, cost=12_000_000, salvage=0, life=60,
                         commission_now=True, commissioning_date=comm,
                         name_prefix="RunGuard")
        period = add_month(period_of(comm))  # fully in the past

        # Self-heal: the dev DB accumulates state — a previous (possibly
        # interrupted) suite run may have left this period POSTED. Reverse it
        # so the lifecycle under test starts from a free period.
        journal = api_client.get("/depreciation/runs").json()["data"]
        for r in journal["runs"]:
            if r["period"] == period and r["status"] == "posted":
                api_client.post(f"/depreciation/runs/{r['id']}/reverse")

        resp = api_client.post("/depreciation/runs", json={"period": period})
        assert resp.status_code == 200, f"Qoralama yaratilmadi: {resp.text}"
        run = resp.json()["data"]
        assert run["status"] == "draft"
        line = next((l for l in run["lines"] if l["asset_id"] == a["id"]), None)
        assert line is not None, "Yangi aktiv run qatorlarida bo'lishi kerak"
        assert line["amount"] == 200_000.00, f"12M/60 oy = 200 000, keldi: {line['amount']}"

        post = api_client.post(f"/depreciation/runs/{run['id']}/post")
        assert post.status_code == 200, f"Post xato: {post.text}"
        assert post.json()["data"]["status"] == "posted"

        again = api_client.post("/depreciation/runs", json={"period": period})
        assert again.status_code == 409, \
            f"Post qilingan davr uchun qayta run 409 bo'lishi shart, keldi {again.status_code}"

        rev = api_client.post(f"/depreciation/runs/{run['id']}/reverse")
        assert rev.status_code == 200, f"Revers xato: {rev.text}"

        redo = api_client.post("/depreciation/runs", json={"period": period})
        assert redo.status_code == 200, "Reversdan keyin davr qayta ochilishi kerak"

    def test_invalid_period_rejected(self, api_client):
        resp = api_client.post("/depreciation/runs", json={"period": "not-a-period"})
        assert resp.status_code == 400

    def test_asset_not_depreciated_before_commissioning(self, api_client, mapping):
        """Shu oy topshirilgan aktiv o'tgan oy runiga kirmasligi kerak."""
        comm = months_ago_first(0)  # this month
        a = create_asset(api_client, mapping, cost=6_000_000, life=12,
                         commission_now=True, commissioning_date=comm,
                         name_prefix="TooNew")
        period = period_of(months_ago_first(1))
        resp = api_client.post("/depreciation/runs", json={"period": period})
        if resp.status_code == 409:
            pytest.skip("O'tgan oy allaqachon post qilingan — filtr testi run summary'siz o'tkazildi")
        assert resp.status_code == 200, resp.text
        run = resp.json()["data"]
        assert all(l["asset_id"] != a["id"] for l in run["lines"]), \
            "Joriy oyda topshirilgan aktiv o'tgan oy reglamentiga kirmasligi kerak"


class TestDisposal:
    """9.4 — chiqarish: sotish narxi -> foyda/zarar, holat terminal."""

    def test_sale_returns_gain(self, api_client, mapping):
        comm = months_ago_first(2)
        a = create_asset(api_client, mapping, cost=10_000_000, life=12,
                         commission_now=True, commissioning_date=comm,
                         name_prefix="SaleGain")
        resp = api_client.post(f"/assets/{a['id']}/dispose", json={
            "disposal_date": date.today().isoformat(),
            "disposal_type": "sale",
            "sale_price": 12_000_000,
            "reason": "Test sale",
        })
        assert resp.status_code == 200, f"Sotish xato: {resp.text}"
        data = resp.json()["data"]
        assert data["status"] == "disposed"
        assert data["gain_loss"] == round(12_000_000 - data["book_value"], 2)
        assert data["gain_loss"] > 0, "Qoldiqdan qimmat sotish foyda berishi kerak"

    def test_sale_requires_price(self, api_client, mapping):
        a = create_asset(api_client, mapping, cost=5_000_000, life=12,
                         commission_now=True,
                         commissioning_date=months_ago_first(2),
                         name_prefix="NoPrice")
        resp = api_client.post(f"/assets/{a['id']}/dispose", json={
            "disposal_date": date.today().isoformat(),
            "disposal_type": "sale",
        })
        assert resp.status_code == 400, "Narxsiz sotish rad etilishi kerak"

    def test_draft_cannot_be_disposed(self, api_client, mapping):
        a = create_asset(api_client, mapping, cost=5_000_000, life=12,
                         name_prefix="DraftDisp")
        resp = api_client.post(f"/assets/{a['id']}/dispose", json={
            "disposal_date": date.today().isoformat(),
            "disposal_type": "writeoff",
        })
        assert resp.status_code == 400, "Draft aktivni chiqarib bo'lmaydi"


class TestReadModels:
    """9.5 — stats, run jurnali, entries, reconciliation."""

    def test_list_and_invariants(self, api_client):
        resp = api_client.get("/assets")
        assert resp.status_code == 200
        for a in resp.json()["data"]:
            assert float(a["accumulated_depreciation"]) <= float(a["cost"]) + 0.01, \
                "Yig'ilgan iznos tannarxdan oshmasligi kerak"
            assert abs(float(a["book_value"]) - (float(a["cost"]) - float(a["accumulated_depreciation"]))) < 0.01

    def test_stats_shape(self, api_client):
        resp = api_client.get("/assets/stats")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert data["total_count"] >= 1
        assert len(data["nbv_trend"]) == 12, "NBV grafigi 12 oylik bo'lishi kerak"
        assert isinstance(data["by_status"], list)

    def test_run_journal_list(self, api_client):
        resp = api_client.get("/depreciation/runs")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert "runs" in data and "unposted_gaps" in data and "suggested_period" in data
        assert len(data["suggested_period"]) == 7  # YYYY-MM

    def test_asset_entries_history(self, api_client):
        resp = api_client.get("/assets")
        assets = resp.json()["data"]
        withdep = next((a for a in assets if float(a["accumulated_depreciation"]) > 0), None)
        if not withdep:
            pytest.skip("Iznosli aktiv topilmadi")
        er = api_client.get(f"/assets/{withdep['id']}/entries")
        assert er.status_code == 200
        entries = er.json()["data"]
        # accumulated reflects APPLIED accruals: opening rows (run_id NULL),
        # disposal top-ups and entries of posted runs. Entries still sitting in
        # a DRAFT run are 'active' but not yet in accumulated — exclude them.
        journal = api_client.get("/depreciation/runs").json()["data"]
        draft_runs = {r["id"] for r in journal["runs"] if r["status"] == "draft"}
        applied = round(sum(
            e["amount"] for e in entries
            if e["status"] == "active" and e["run_id"] not in draft_runs
        ), 2)
        assert abs(applied - float(withdep["accumulated_depreciation"])) < 0.01, \
            "Qo'llangan entries yig'indisi accumulated bilan teng bo'lishi kerak (denormalizatsiya invarianti)"

    def test_reconciliation_endpoint(self, api_client):
        resp = api_client.get("/assets/reconcile")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert "rows" in data and "mismatch_count" in data


class TestMaintenance:
    """9.6 — texnik xizmat (457): expense vs kapitalizatsiya."""

    def test_capital_repair_increases_cost_and_life(self, api_client, mapping):
        a = create_asset(api_client, mapping, cost=10_000_000, life=24,
                         commission_now=True, commissioning_date=months_ago_first(1),
                         name_prefix="MaintCap")
        resp = api_client.post(f"/assets/{a['id']}/maintenance", json={
            "maintenance_type": "capital_repair",
            "service_date": date.today().isoformat(),
            "cost": 2_000_000,
            "payment_method": "credit",
            "life_extension_months": 6,
            "description": "Dvigatel kapital ta'miri",
        })
        assert resp.status_code == 200, f"Kapital ta'mir xato: {resp.text}"
        data = resp.json()["data"]
        assert data["cost_after"] == 12_000_000, "Kapital ta'mir qiymatga qo'shilishi kerak"
        assert data["life_after"] == 30, "Muddat uzayishi qo'llanishi kerak"

        hist = api_client.get(f"/assets/{a['id']}/maintenance")
        assert hist.status_code == 200
        rows = hist.json()["data"]
        assert len(rows) == 1 and rows[0]["maintenance_type"] == "capital_repair"
        assert rows[0]["journal_entry_id"], "Kapital ta'mir JE yaratishi kerak"

    def test_regular_to_does_not_touch_cost(self, api_client, mapping):
        a = create_asset(api_client, mapping, cost=5_000_000, life=12,
                         commission_now=True, commissioning_date=months_ago_first(1),
                         name_prefix="MaintTO")
        resp = api_client.post(f"/assets/{a['id']}/maintenance", json={
            "maintenance_type": "regular_to",
            "service_date": date.today().isoformat(),
            "cost": 300_000,
            "payment_method": "cash",
        })
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert data["cost_after"] == 5_000_000, "Oddiy TO qiymatni o'zgartirmasligi kerak"

    def test_life_extension_requires_capitalizing_type(self, api_client, mapping):
        a = create_asset(api_client, mapping, cost=5_000_000, life=12,
                         commission_now=True, commissioning_date=months_ago_first(1),
                         name_prefix="MaintBad")
        resp = api_client.post(f"/assets/{a['id']}/maintenance", json={
            "maintenance_type": "regular_to",
            "service_date": date.today().isoformat(),
            "cost": 100_000,
            "life_extension_months": 6,
        })
        assert resp.status_code == 400, "Oddiy TO'da muddat uzayishi rad etilishi kerak"


class TestLegacyRoutesRemoved:
    """9.7 — legacy marshrutlar o'chirilganini qotirib qo'yamiz."""

    def test_legacy_endpoints_gone(self, api_client):
        assert api_client.get("/fixed-assets").status_code in (404, 405)
        assert api_client.post("/run-depreciation", json={"period": "2026-01"}).status_code in (404, 405)
