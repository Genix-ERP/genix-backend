"""
Ish grafigi (Gantt v2 — S8 phase 1) tests: migration 471 +
internal/handler/construction_schedule.go. See docs/qurilish-audit.md.

Invariants under test:
  1. Smeta va grafik BITTA ish ro'yxati: GET /construction/projects/:id/schedule
     faqat top-level ish qatorlarini qaytaradi (resource sub-lines chiqmaydi),
     progress_pct = done_quantity / plan qty (yangi progress ustuni YO'Q).
  2. PUT /construction/works/:id/schedule validatsiyasi: end >= start, ikkala
     sana birga, faqat ishlar (resurs qatoriga 404).
  3. FS bog'liqliklar: dublikat/self/sikl 400 bilan rad etiladi (BFS).
  4. Propagatsiya (ASAP repair): predecessor oldinga surilsa successorlar
     `succ.start >= pred.end + lag + 1` shartiga keltiriladi, davomiylik
     saqlanadi, javobdagi `updated[]` prev sanalarni beradi (bitta undo qadam);
     orqaga surilsa successorlar joyida qoladi.
  5. Bulk endpoint propagatsiyasiz yozadi (undo semantikasi).
  6. Baseline freeze sched → baseline nusxalaydi; keyingi siljishlar
     baseline'ga tegmaydi.
  7. work_overdue skaner predikati muddati o'tgan+tugallanmagan ishni topadi;
     ikkala yangi event katalogda (GET /workflow-events).
  8. Capstone: obyekt → smeta → grafik → baseline → siljish → progress →
     tasdiqlash zanjiri → reja-fakt/stats jonli.

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import uuid

import pytest


CODE_PREFIX = "GRAF32"


def _make_project(api_client, suffix):
    payload = {
        "code": f"{CODE_PREFIX}-{suffix}-{uuid.uuid4().hex[:6]}",
        "name": f"Ish grafigi test {suffix}",
        "region": "Toshkent shahri",
        "city": "Toshkent",
        "project_type": "residential",
    }
    resp = api_client.post("/construction/projects", json=payload)
    assert resp.status_code in (200, 201), f"project create failed: {resp.text}"
    return resp.json()["data"]["id"]


def _make_estimate(api_client, project_id, name="Asosiy smeta"):
    resp = api_client.post(
        f"/construction/projects/{project_id}/estimates",
        json={"name": name, "source_type": "edinich"},
    )
    assert resp.status_code in (200, 201), f"estimate create failed: {resp.text}"
    return resp.json()["data"]["id"]


def _make_work(api_client, estimate_id, item_number, name, section, qty, uom="m3",
               labor_rate=0.0, sort_order=0):
    resp = api_client.post(
        f"/construction/estimates/{estimate_id}/lines",
        json={
            "item_number": item_number,
            "name": name,
            "parent_item_number": section,
            "uom": uom,
            "quantity": qty,
            "labor_rate": labor_rate,
            "sort_order": sort_order,
        },
    )
    assert resp.status_code in (200, 201), f"work create failed: {resp.text}"
    return resp.json()["data"]["id"]


def _schedule_map(api_client, project_id):
    resp = api_client.get(f"/construction/projects/{project_id}/schedule")
    assert resp.status_code == 200, resp.text
    data = resp.json()["data"]
    return data, {w["id"]: w for w in data["works"]}


@pytest.fixture(scope="module", autouse=True)
def _cleanup_projects(db_read):
    yield
    db_read.execute(
        "UPDATE construction_projects SET deleted_at = NOW() "
        "WHERE deleted_at IS NULL AND code LIKE %s",
        (f"{CODE_PREFIX}-%",),
    )


@pytest.fixture(scope="module")
def graf(api_client):
    """Project + edinich estimate + 4 works in 2 sections + 1 resource line."""
    project_id = _make_project(api_client, "MAIN")
    estimate_id = _make_estimate(api_client, project_id)
    works = {
        "A": _make_work(api_client, estimate_id, "1.1", "Maydonni tekislash",
                        "Yer ishlari", 650, uom="m2", labor_rate=10, sort_order=1),
        "B": _make_work(api_client, estimate_id, "1.2", "Grunt qazish",
                        "Yer ishlari", 713, labor_rate=20, sort_order=2),
        "C": _make_work(api_client, estimate_id, "2.1", "Poydevor osti asos",
                        "Poydevor", 158, labor_rate=30, sort_order=3),
        "D": _make_work(api_client, estimate_id, "2.2", "Lenta poydevor",
                        "Poydevor", 42, labor_rate=40, sort_order=4),
    }
    # Resource sub-line under A — grafikda ko'rinmasligi kerak.
    resp = api_client.post(
        f"/construction/estimates/{estimate_id}/lines",
        json={
            "name": "Sement M400",
            "resource_type": "material",
            "parent_line_id": works["A"],
            "norm_rate": 0.1,
            "unit_price": 1000,
            "uom": "t",
        },
    )
    assert resp.status_code in (200, 201), f"resource create failed: {resp.text}"
    resource_id = resp.json()["data"]["id"]
    return {
        "project_id": project_id,
        "estimate_id": estimate_id,
        "works": works,
        "resource_id": resource_id,
    }


# ─── 1. Bitta ish ro'yxati ────────────────────────────────────────────────

def test_schedule_lists_works_not_resources(api_client, graf):
    data, by_id = _schedule_map(api_client, graf["project_id"])
    for wid in graf["works"].values():
        assert wid in by_id, f"work {wid} missing from schedule"
    assert graf["resource_id"] not in by_id, "resource sub-line leaked into schedule"

    a = by_id[graf["works"]["A"]]
    assert a["section"] == "Yer ishlari"
    assert a["sched_start"] is None and a["sched_end"] is None
    assert a["progress_pct"] == 0
    assert data["dependencies"] == []
    assert "project" in data


# ─── 2. Sana yozish + validatsiya ────────────────────────────────────────

def test_set_dates_and_validation(api_client, graf):
    a = graf["works"]["A"]
    resp = api_client.put(f"/construction/works/{a}/schedule",
                          json={"sched_start": "2026-09-01", "sched_end": "2026-09-03"})
    assert resp.status_code == 200, resp.text
    updated = resp.json()["data"]["updated"]
    assert updated[0]["id"] == a
    assert updated[0]["prev_start"] is None

    _, by_id = _schedule_map(api_client, graf["project_id"])
    assert by_id[a]["sched_start"] == "2026-09-01"
    assert by_id[a]["sched_end"] == "2026-09-03"

    # end < start
    resp = api_client.put(f"/construction/works/{a}/schedule",
                          json={"sched_start": "2026-09-05", "sched_end": "2026-09-04"})
    assert resp.status_code == 400
    # faqat bitta sana
    resp = api_client.put(f"/construction/works/{a}/schedule",
                          json={"sched_start": "2026-09-05"})
    assert resp.status_code == 400
    # buzuq format
    resp = api_client.put(f"/construction/works/{a}/schedule",
                          json={"sched_start": "05.09.2026", "sched_end": "06.09.2026"})
    assert resp.status_code == 400
    # resurs qatori ish emas
    resp = api_client.put(f"/construction/works/{graf['resource_id']}/schedule",
                          json={"sched_start": "2026-09-01", "sched_end": "2026-09-02"})
    assert resp.status_code == 404


# ─── 3. Bog'liqliklar: dublikat / self / sikl ────────────────────────────

def test_dependencies_and_cycle_rejection(api_client, graf):
    p = graf["project_id"]
    w = graf["works"]

    def dep(pred, succ, lag=0):
        return api_client.post(f"/construction/projects/{p}/dependencies",
                               json={"predecessor_line_id": pred,
                                     "successor_line_id": succ, "lag_days": lag})

    assert dep(w["A"], w["B"]).status_code == 200
    assert dep(w["B"], w["C"]).status_code == 200

    assert dep(w["A"], w["B"]).status_code == 400, "duplicate must be rejected"
    assert dep(w["A"], w["A"]).status_code == 400, "self-link must be rejected"
    assert dep(w["C"], w["A"]).status_code == 400, "cycle A→B→C→A must be rejected"
    # 2 uzunlikdagi sikl ham
    assert dep(w["B"], w["A"]).status_code == 400

    _, by_id = _schedule_map(api_client, p)
    resp = api_client.get(f"/construction/projects/{p}/schedule")
    deps = resp.json()["data"]["dependencies"]
    assert len(deps) == 2


# ─── 4. FS propagatsiya ──────────────────────────────────────────────────

def test_fs_propagation_forward_shift(api_client, graf):
    p = graf["project_id"]
    w = graf["works"]

    # Zanjir: A(01-03) → B(04-05) → C(06-07); D mustaqil (10-11).
    resp = api_client.post(f"/construction/projects/{p}/schedule/bulk", json={"items": [
        {"line_id": w["A"], "sched_start": "2026-09-01", "sched_end": "2026-09-03"},
        {"line_id": w["B"], "sched_start": "2026-09-04", "sched_end": "2026-09-05"},
        {"line_id": w["C"], "sched_start": "2026-09-06", "sched_end": "2026-09-07"},
        {"line_id": w["D"], "sched_start": "2026-09-10", "sched_end": "2026-09-11"},
    ]})
    assert resp.status_code == 200, resp.text

    # A ni +3 kun: 04-06. B start >= 07 bo'lishi kerak → 07-08; C → 09-10.
    resp = api_client.put(f"/construction/works/{w['A']}/schedule",
                          json={"sched_start": "2026-09-04", "sched_end": "2026-09-06"})
    assert resp.status_code == 200, resp.text
    updated = {u["id"]: u for u in resp.json()["data"]["updated"]}
    assert set(updated) == {w["A"], w["B"], w["C"]}, "D must not move"
    assert updated[w["B"]]["sched_start"] == "2026-09-07"
    assert updated[w["B"]]["sched_end"] == "2026-09-08"
    assert updated[w["B"]]["prev_start"] == "2026-09-04"
    assert updated[w["C"]]["sched_start"] == "2026-09-09"
    assert updated[w["C"]]["sched_end"] == "2026-09-10"

    _, by_id = _schedule_map(api_client, p)
    assert by_id[w["B"]]["sched_start"] == "2026-09-07"
    assert by_id[w["C"]]["sched_end"] == "2026-09-10"
    assert by_id[w["D"]]["sched_start"] == "2026-09-10"


def test_fs_lag_respected(api_client, graf):
    p = graf["project_id"]
    w = graf["works"]
    # C → D lag 2: D.start >= C.end + 3 kun.
    resp = api_client.post(f"/construction/projects/{p}/dependencies",
                           json={"predecessor_line_id": w["C"],
                                 "successor_line_id": w["D"], "lag_days": 2})
    assert resp.status_code == 200, resp.text

    # C hozir 09-10 (oldingi testdan). C ni 11-12 ga suramiz:
    # D.start >= 12 + 2 + 1 = 15 → D (10-11) 15-16 ga suriladi.
    resp = api_client.put(f"/construction/works/{w['C']}/schedule",
                          json={"sched_start": "2026-09-11", "sched_end": "2026-09-12"})
    assert resp.status_code == 200, resp.text
    updated = {u["id"]: u for u in resp.json()["data"]["updated"]}
    assert w["D"] in updated
    assert updated[w["D"]]["sched_start"] == "2026-09-15"
    assert updated[w["D"]]["sched_end"] == "2026-09-16"


def test_backward_move_does_not_pull_successors(api_client, graf):
    p = graf["project_id"]
    w = graf["works"]
    # A ni orqaga: 01-03. B (07-08) joyida qolishi kerak (slack ruxsat).
    resp = api_client.put(f"/construction/works/{w['A']}/schedule",
                          json={"sched_start": "2026-09-01", "sched_end": "2026-09-03"})
    assert resp.status_code == 200, resp.text
    updated = resp.json()["data"]["updated"]
    assert [u["id"] for u in updated] == [w["A"]], "backward move must not touch successors"

    _, by_id = _schedule_map(api_client, p)
    assert by_id[w["B"]]["sched_start"] == "2026-09-07"


def test_bulk_undo_restores_dates(api_client, graf):
    p = graf["project_id"]
    w = graf["works"]
    _, before = _schedule_map(api_client, p)

    resp = api_client.put(f"/construction/works/{w['B']}/schedule",
                          json={"sched_start": "2026-09-20", "sched_end": "2026-09-21"})
    assert resp.status_code == 200
    updated = resp.json()["data"]["updated"]

    # Undo: prev qiymatlarni bulk bilan qaytarish.
    items = [{"line_id": u["id"], "sched_start": u["prev_start"],
              "sched_end": u["prev_end"]} for u in updated]
    resp = api_client.post(f"/construction/projects/{p}/schedule/bulk",
                           json={"items": items})
    assert resp.status_code == 200, resp.text

    _, after = _schedule_map(api_client, p)
    for wid in (w["B"], w["C"], w["D"]):
        assert after[wid]["sched_start"] == before[wid]["sched_start"]
        assert after[wid]["sched_end"] == before[wid]["sched_end"]


def test_bulk_rejects_foreign_lines(api_client, graf):
    other_project = _make_project(api_client, "OTHER")
    other_estimate = _make_estimate(api_client, other_project)
    foreign = _make_work(api_client, other_estimate, "1.1", "Begona ish",
                         "Bo'lim", 10)
    resp = api_client.post(
        f"/construction/projects/{graf['project_id']}/schedule/bulk",
        json={"items": [{"line_id": foreign, "sched_start": "2026-09-01",
                         "sched_end": "2026-09-02"}]})
    assert resp.status_code == 400


# ─── 5. Baseline ─────────────────────────────────────────────────────────

def test_baseline_freeze_and_immutability(api_client, graf):
    p = graf["project_id"]
    w = graf["works"]
    resp = api_client.post(f"/construction/projects/{p}/schedule/baseline")
    assert resp.status_code == 200, resp.text
    assert resp.json()["data"]["frozen"] >= 4

    _, by_id = _schedule_map(api_client, p)
    a = by_id[w["A"]]
    assert a["baseline_start"] == a["sched_start"]
    assert a["baseline_end"] == a["sched_end"]
    frozen_start = a["baseline_start"]

    # Keyingi siljish baseline'ga tegmaydi.
    resp = api_client.put(f"/construction/works/{w['A']}/schedule",
                          json={"sched_start": "2026-09-02", "sched_end": "2026-09-04"})
    assert resp.status_code == 200
    _, by_id = _schedule_map(api_client, p)
    assert by_id[w["A"]]["baseline_start"] == frozen_start
    assert by_id[w["A"]]["sched_start"] == "2026-09-02"


# ─── 6. Progress done_quantity'dan ───────────────────────────────────────

def test_progress_pct_from_done_quantity(api_client, graf):
    w = graf["works"]["D"]
    resp = api_client.post(f"/construction/works/{w}/done-quantity",
                           json={"done_quantity": 21})
    assert resp.status_code == 200, resp.text
    _, by_id = _schedule_map(api_client, graf["project_id"])
    assert by_id[w]["progress_pct"] == 50.0  # 21 / 42
    assert by_id[w]["approval_status"] == "in_progress"


# ─── 7. Overdue skaner predikati + katalog ───────────────────────────────

def test_workflow_catalog_has_schedule_events(api_client):
    resp = api_client.get("/workflow-events")
    assert resp.status_code == 200, resp.text
    payload = resp.json()["data"]
    names = set()
    stack = [payload]
    while stack:
        cur = stack.pop()
        if isinstance(cur, dict):
            if "event" in cur:
                names.add(cur["event"])
            if "name" in cur and isinstance(cur["name"], str):
                names.add(cur["name"])
            stack.extend(cur.values())
        elif isinstance(cur, list):
            stack.extend(cur)
    assert "construction.work_overdue" in names
    assert "construction.work_completed" in names


def test_overdue_scanner_predicate(api_client, db_read, graf, tenant_id):
    p = graf["project_id"]
    w = graf["works"]
    # B: muddati kecha o'tgan, progress 0 — skaner topishi kerak.
    resp = api_client.post(f"/construction/projects/{p}/schedule/bulk", json={"items": [
        {"line_id": w["B"], "sched_start": "2026-08-01", "sched_end": "2026-08-05"},
    ]})
    assert resp.status_code == 200, resp.text

    db_read.execute("""
        SELECT el.id
        FROM construction_estimate_line el
        JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
        JOIN construction_projects p ON p.id = e.project_id AND p.tenant_id = el.tenant_id
        WHERE el.tenant_id = %s
          AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
          AND COALESCE(el.resource_type, '') = ''
          AND COALESCE(el.parent_line_id, 0) = 0
          AND el.sched_end IS NOT NULL AND el.sched_end < CURRENT_DATE
          AND COALESCE(el.done_quantity, 0) < CASE
                WHEN COALESCE(el.imported_quantity, 0) > 0 THEN el.imported_quantity
                WHEN COALESCE(el.original_quantity, 0) > 0 THEN el.original_quantity
                ELSE COALESCE(el.quantity, 0)
            END
          AND p.deleted_at IS NULL
          AND COALESCE(p.status, '') NOT IN ('completed', 'cancelled')
          AND p.id = %s
    """, (tenant_id, p))
    overdue_ids = {row["id"] for row in db_read.fetchall()}
    assert w["B"] in overdue_ids


# ─── 8. Capstone E2E ─────────────────────────────────────────────────────

def test_capstone_object_to_pnl(api_client, db_read, tenant_id):
    """Obyekt → smeta → grafik + bog'liqlik → baseline → siljish →
    progress → tasdiqlash zanjiri → reja-fakt / stats jonli."""
    p = _make_project(api_client, "E2E")
    est = _make_estimate(api_client, p)
    w1 = _make_work(api_client, est, "1.1", "Qazish", "Yer ishlari", 100,
                    labor_rate=50, sort_order=1)
    w2 = _make_work(api_client, est, "2.1", "Beton quyish", "Poydevor", 40,
                    labor_rate=200, sort_order=2)
    w3 = _make_work(api_client, est, "2.2", "Gidroizolyatsiya", "Poydevor", 80,
                    labor_rate=25, sort_order=3)

    # Grafik: ketma-ket 2 kunlik ishlar + zanjir w1→w2→w3.
    resp = api_client.post(f"/construction/projects/{p}/schedule/bulk", json={"items": [
        {"line_id": w1, "sched_start": "2026-10-01", "sched_end": "2026-10-02"},
        {"line_id": w2, "sched_start": "2026-10-03", "sched_end": "2026-10-04"},
        {"line_id": w3, "sched_start": "2026-10-05", "sched_end": "2026-10-06"},
    ]})
    assert resp.status_code == 200, resp.text
    for pred, succ in ((w1, w2), (w2, w3)):
        resp = api_client.post(f"/construction/projects/{p}/dependencies",
                               json={"predecessor_line_id": pred,
                                     "successor_line_id": succ, "lag_days": 0})
        assert resp.status_code == 200, resp.text

    # Baseline muzlatiladi.
    resp = api_client.post(f"/construction/projects/{p}/schedule/baseline")
    assert resp.status_code == 200
    assert resp.json()["data"]["frozen"] == 3

    # Predecessor +5 kun → butun zanjir suriladi (qo'lda hisoblangan).
    resp = api_client.put(f"/construction/works/{w1}/schedule",
                          json={"sched_start": "2026-10-06", "sched_end": "2026-10-07"})
    assert resp.status_code == 200, resp.text
    updated = {u["id"]: u for u in resp.json()["data"]["updated"]}
    assert updated[w2]["sched_start"] == "2026-10-08"
    assert updated[w2]["sched_end"] == "2026-10-09"
    assert updated[w3]["sched_start"] == "2026-10-10"
    assert updated[w3]["sched_end"] == "2026-10-11"

    _, by_id = _schedule_map(api_client, p)
    assert by_id[w2]["baseline_start"] == "2026-10-03", "baseline ghost intact"

    # Progress + 3-rolli tasdiqlash zanjiri (owner fallback gate).
    resp = api_client.post(f"/construction/works/{w1}/done-quantity",
                           json={"done_quantity": 100})
    assert resp.status_code == 200, resp.text
    for step in ("submit", "confirm-supervisor", "confirm-engineer"):
        resp = api_client.post(f"/construction/works/{w1}/{step}", json={})
        assert resp.status_code == 200, f"{step}: {resp.text}"

    _, by_id = _schedule_map(api_client, p)
    assert by_id[w1]["progress_pct"] == 100.0
    assert by_id[w1]["approval_status"] == "confirmed_engineer"

    # Overdue predikat: w2 kechiktirilgan sanaga o'tkaziladi, w1 esa 100% —
    # faqat w2 tushishi kerak.
    resp = api_client.post(f"/construction/projects/{p}/schedule/bulk", json={"items": [
        {"line_id": w1, "sched_start": "2026-07-01", "sched_end": "2026-07-02"},
        {"line_id": w2, "sched_start": "2026-07-03", "sched_end": "2026-07-04"},
    ]})
    assert resp.status_code == 200
    db_read.execute("""
        SELECT el.id
        FROM construction_estimate_line el
        JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
        JOIN construction_projects pr ON pr.id = e.project_id AND pr.tenant_id = el.tenant_id
        WHERE el.tenant_id = %s AND pr.id = %s
          AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
          AND COALESCE(el.resource_type, '') = ''
          AND COALESCE(el.parent_line_id, 0) = 0
          AND el.sched_end IS NOT NULL AND el.sched_end < CURRENT_DATE
          AND COALESCE(el.done_quantity, 0) < CASE
                WHEN COALESCE(el.imported_quantity, 0) > 0 THEN el.imported_quantity
                WHEN COALESCE(el.original_quantity, 0) > 0 THEN el.original_quantity
                ELSE COALESCE(el.quantity, 0)
            END
          AND pr.deleted_at IS NULL
    """, (tenant_id, p))
    overdue_ids = {row["id"] for row in db_read.fetchall()}
    assert w2 in overdue_ids, "unfinished overdue work must be flagged"
    assert w1 not in overdue_ids, "completed work must not be flagged"

    # Boshqa modullar o'qiy oladigan agregatlar jonli.
    resp = api_client.get(f"/construction/projects/{p}/reja-fakt")
    assert resp.status_code == 200, resp.text
    resp = api_client.get("/construction/projects/stats")
    assert resp.status_code == 200
    resp = api_client.get(f"/construction/projects/{p}/gantt")
    assert resp.status_code == 200
