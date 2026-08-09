"""
Qurilish v2 tests — docs/qurilish-v2/{conventions.md, audit.md}, 2026-08-09.

Invariants under test:
  1. YAGONA progress-dvigatel: GET /construction/projects/:id/progress (F1,
     qiymat-vaznli) == /projects/stats per_project.readiness_pct == qo'lda
     hisoblangan fixture; bosqich kesimi jamlanmasi loyiha % bilan mos.
  2. Narx-qo'riqchi (P0 fix): sof-prorab rolli REAL foydalanuvchi uchun
     ish-ro'yxati endpointlari pul maydonlarini 0 + price_hidden=true bilan
     qaytaradi; pul-hisobot endpointlari 403; admin (owner) hamon to'liq
     ko'radi; prorab o'z ish-oqimini (done-quantity) bajara oladi.
  3. Subpudrat contract_number endi saqlanadi (472-gacha jimgina yo'qolardi).

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import uuid

import pytest
import requests

from conftest import BASE_URL, APIClient

CODE_PREFIX = "QV2T33"
TEST_PASSWORD = "Prorab#2026test"


def _make_project(api_client, suffix):
    resp = api_client.post("/construction/projects", json={
        "code": f"{CODE_PREFIX}-{suffix}-{uuid.uuid4().hex[:6]}",
        "name": f"Qurilish v2 test {suffix}",
        "region": "Toshkent shahri",
        "city": "Toshkent",
        "project_type": "residential",
    })
    assert resp.status_code in (200, 201), resp.text
    return resp.json()["data"]["id"]


def _make_estimate(api_client, project_id):
    resp = api_client.post(
        f"/construction/projects/{project_id}/estimates",
        json={"name": "Asosiy smeta", "source_type": "edinich"})
    assert resp.status_code in (200, 201), resp.text
    return resp.json()["data"]["id"]


def _make_work(api_client, estimate_id, item_number, name, section, qty, rate, sort_order):
    resp = api_client.post(f"/construction/estimates/{estimate_id}/lines", json={
        "item_number": item_number, "name": name, "parent_item_number": section,
        "uom": "m3", "quantity": qty, "labor_rate": rate, "sort_order": sort_order,
    })
    assert resp.status_code in (200, 201), resp.text
    return resp.json()["data"]["id"]


@pytest.fixture(scope="module", autouse=True)
def _cleanup(db_read):
    yield
    db_read.execute(
        "UPDATE construction_projects SET deleted_at = NOW() "
        "WHERE deleted_at IS NULL AND code LIKE %s", (f"{CODE_PREFIX}-%",))


@pytest.fixture(scope="module")
def proj(api_client):
    """Project with 2 sections / 3 works and known done quantities.

    Muhim semantika (mavjud F1/stats konventsiyasi): done-quantity yozuvi
    jonli `quantity` va `total_amount`ni sinxronlaydi, plan-miqdor esa
    `original_quantity` anchor'ida qoladi. Shu sabab og'irliklar:
    A: orig 100 × rate 50 → done 50  → live total 2500, ratio 0.5
    B: orig 40  × rate 200 → done 40 → live total 8000, ratio 1.0
    C: orig 80  × rate 25  → done 0  → live total 2000, ratio 0
    F1 = (2500×0.5 + 8000×1 + 2000×0) / 12500 × 100 = 74.0
    """
    p = _make_project(api_client, "MAIN")
    est = _make_estimate(api_client, p)
    a = _make_work(api_client, est, "1.1", "Qazish", "Yer ishlari", 100, 50, 1)
    b = _make_work(api_client, est, "2.1", "Beton", "Poydevor", 40, 200, 2)
    c = _make_work(api_client, est, "2.2", "Gidro", "Poydevor", 80, 25, 3)
    for wid, done in ((a, 50), (b, 40)):
        resp = api_client.post(f"/construction/works/{wid}/done-quantity",
                               json={"done_quantity": done})
        assert resp.status_code == 200, resp.text
    return {"id": p, "estimate_id": est, "works": {"A": a, "B": b, "C": c}}


@pytest.fixture(scope="module")
def foreman(api_client, db_read, tenant_id, proj):
    """Real user: construction read RBAC bor, loyihada FAQAT prorab roli."""
    from argon2 import PasswordHasher

    suffix = uuid.uuid4().hex[:8]
    resp = api_client.post("/employees", json={
        "employee_number": f"EMP-T33-{suffix}",
        "first_name": "Prorab",
        "last_name": f"Test{suffix[:4]}",
        "email": f"t33-{suffix}@test.uz",
        "hire_date": "2026-01-01",
        "salary": 3_000_000,
        "job_title": "Prorab",
    })
    if resp.status_code not in (200, 201):
        pytest.skip(f"Cannot create employee: {resp.status_code}")
    emp = resp.json().get("data", resp.json())

    db_read.execute(
        "SELECT id, email FROM users WHERE employee_id = %s AND tenant_id = %s LIMIT 1",
        (emp["id"], tenant_id))
    row = db_read.fetchone()
    if not row:
        pytest.skip("employee user auto-creation did not run")
    pw_hash = PasswordHasher().hash(TEST_PASSWORD)
    db_read.execute(
        "UPDATE users SET password_hash = %s, is_active = true, is_verified = true WHERE id = %s",
        (pw_hash, row["id"]))

    # RBAC: construction o'qish huquqlari (route-gate'lardan o'tsin).
    role_id = str(uuid.uuid4())
    db_read.execute(
        "INSERT INTO roles (id, tenant_id, name, code, description, is_system) "
        "VALUES (%s, %s, %s, %s, 'test role', false)",
        (role_id, tenant_id, f"T33 Prorab {suffix[:4]}", f"t33_prorab_{suffix[:6]}"))
    # O'qish + works-oqimi uchun estimate:update (route-gate'ning qo'pol
    # filtri; asl tekshiruv — handler ichidagi loyiha-roli).
    db_read.execute(
        "INSERT INTO role_permissions (role_id, permission_id) "
        "SELECT %s, id FROM permissions WHERE module = 'construction' "
        "AND (action = 'read' OR (resource = 'estimate' AND action = 'update')) "
        "ON CONFLICT DO NOTHING", (role_id,))
    db_read.execute(
        "INSERT INTO user_roles (user_id, role_id) VALUES (%s, %s) ON CONFLICT DO NOTHING",
        (row["id"], role_id))

    # Loyihada faqat prorab.
    db_read.execute(
        "INSERT INTO construction_project_team (project_id, employee_id, role, is_active) "
        "VALUES (%s, %s, 'foreman', true)", (proj["id"], emp["id"]))

    resp = requests.post(f"{BASE_URL}/auth/login",
                         json={"email": row["email"], "password": TEST_PASSWORD})
    assert resp.status_code == 200, f"foreman login failed: {resp.text[:300]}"
    data = resp.json().get("data", resp.json())
    token = data.get("access_token") or data.get("token")
    return APIClient(base_url=BASE_URL, token=token, tenant_id=tenant_id)


# ─── 1. Yagona progress-dvigatel ─────────────────────────────────────────

def test_progress_matches_hand_computed(api_client, proj):
    resp = api_client.get(f"/construction/projects/{proj['id']}/progress")
    assert resp.status_code == 200, resp.text
    data = resp.json()["data"]
    assert abs(data["project_pct"] - 74.0) < 0.01, data["project_pct"]
    assert data["works_total"] == 3
    assert data["smeta_total"] == 12500

    stages = {s["name"]: s for s in data["stages"]}
    assert abs(stages["Yer ishlari"]["pct"] - 50.0) < 0.01
    assert abs(stages["Poydevor"]["pct"] - 80.0) < 0.01  # (8000+0)/10000
    # Bosqich jamlanmasi == loyiha % (bir xil manba)
    total_plan = sum(s["plan_amount"] for s in data["stages"])
    weighted = sum(s["plan_amount"] * s["pct"] for s in data["stages"]) / total_plan
    assert abs(weighted - data["project_pct"]) < 0.01


def test_progress_equals_stats_readiness(api_client, proj):
    resp = api_client.get(f"/construction/projects/{proj['id']}/progress")
    assert resp.status_code == 200
    project_pct = resp.json()["data"]["project_pct"]

    resp = api_client.get("/construction/projects/stats")
    assert resp.status_code == 200, resp.text
    per_project = resp.json()["data"].get("per_project") or []
    mine = next((p for p in per_project if p.get("id") == proj["id"]), None)
    assert mine is not None, "project missing from stats per_project"
    assert abs(float(mine["readiness_pct"]) - project_pct) < 0.01, \
        f"stats {mine['readiness_pct']} != progress {project_pct}"


# ─── 2. Narx-qo'riqchi ───────────────────────────────────────────────────

def test_foreman_schedule_stripped(foreman, proj):
    resp = foreman.get(f"/construction/projects/{proj['id']}/schedule")
    assert resp.status_code == 200, resp.text
    data = resp.json()["data"]
    assert data.get("price_hidden") is True
    assert len(data["works"]) == 3
    for w in data["works"]:
        assert float(w["unit_rate"]) == 0, w
        assert float(w["total_amount"]) == 0, w


def test_foreman_estimate_lines_stripped(foreman, proj):
    resp = foreman.get(
        f"/construction/estimates/{proj['estimate_id']}/lines",
        params={"page_size": 100})
    assert resp.status_code == 200, resp.text
    body = resp.json()
    lines = body.get("data") or []
    assert len(lines) >= 3
    for l in lines:
        for f in ("unit_rate", "total_amount", "material_rate", "labor_rate",
                  "equipment_rate", "actual_amount", "original_unit_rate"):
            assert float(l.get(f) or 0) == 0, f"{f} leaked: {l.get(f)}"


def test_foreman_stages_list_stripped(api_client, foreman, proj):
    # Bosqich yaratamiz (admin) — keyin prorab ro'yxatda pul ko'rmasin.
    resp = api_client.post(f"/construction/projects/{proj['id']}/stages",
                           json={"name": "Yer ishlari", "planned_budget": 999999})
    assert resp.status_code in (200, 201), resp.text

    resp = foreman.get(f"/construction/projects/{proj['id']}/stages")
    assert resp.status_code == 200, resp.text
    stages = resp.json()["data"]
    assert len(stages) >= 1
    for s in stages:
        assert float(s.get("planned_budget") or 0) == 0
        assert float(s.get("actual_amount") or 0) == 0
        assert float(s.get("material_total") or 0) == 0


def test_foreman_money_reports_denied(foreman, proj):
    p = proj["id"]
    for path in (f"/construction/projects/{p}/reja-fakt",
                 f"/construction/projects/{p}/reports/budget",
                 f"/construction/projects/{p}/reports/summary",
                 f"/construction/projects/{p}/expenses",
                 f"/construction/projects/{p}/financial/pnl",
                 f"/construction/estimates/{proj['estimate_id']}/summary"):
        resp = foreman.get(path)
        assert resp.status_code == 403, f"{path} -> {resp.status_code} (403 kutilgan)"


def test_admin_still_sees_money(api_client, proj):
    resp = api_client.get(f"/construction/projects/{proj['id']}/schedule")
    assert resp.status_code == 200
    data = resp.json()["data"]
    assert data.get("price_hidden") is False
    rates = [float(w["unit_rate"]) for w in data["works"]]
    assert any(r > 0 for r in rates), "admin must still see rates"

    resp = api_client.get(f"/construction/projects/{proj['id']}/reja-fakt")
    assert resp.status_code == 200, "admin reja-fakt must stay open"


def test_foreman_can_still_enter_progress(foreman, proj):
    resp = foreman.post(f"/construction/works/{proj['works']['C']}/done-quantity",
                        json={"done_quantity": 8})
    assert resp.status_code == 200, resp.text


# ─── 3. Subpudrat contract_number roundtrip (472 fix) ────────────────────

def test_subcontract_contract_number_roundtrip(api_client, proj):
    resp = api_client.post(f"/construction/projects/{proj['id']}/subcontracts", json={
        "partner_name": "Test Pudratchi MChJ",
        "work_description": "Gidroizolyatsiya ishlari",
        "amount": 5_000_000,
        "contract_number": "№ 12/2026-T33",
    })
    assert resp.status_code in (200, 201), resp.text

    resp = api_client.get(f"/construction/projects/{proj['id']}/subcontracts")
    assert resp.status_code == 200, resp.text
    subs = resp.json()["data"]
    mine = next((s for s in subs if s.get("partner_name") == "Test Pudratchi MChJ"), None)
    assert mine is not None
    assert mine.get("contract_number") == "№ 12/2026-T33", \
        f"contract_number lost: {mine.get('contract_number')!r}"

    resp = api_client.put(f"/construction/subcontracts/{mine['id']}",
                          json={"contract_number": "№ 13/2026-T33"})
    assert resp.status_code == 200, resp.text
    resp = api_client.get(f"/construction/projects/{proj['id']}/subcontracts")
    updated = next(s for s in resp.json()["data"] if s["id"] == mine["id"])
    assert updated.get("contract_number") == "№ 13/2026-T33"
