"""
CRM v2 (migration 446) integration tests.

Covers the rebuilt single-entity pipeline: default pipeline + terminal
stages, lead lifecycle (create → move → won/lost), server-enforced win/loss
flows (loss reason required, win creates-or-links the unified partner with
normalized-phone dedupe), stage history, stats, the four Hisobotlar
endpoints, tenant isolation and auth gating.
"""
import uuid
import pytest
import requests

from conftest import BASE_URL


def _mkphone():
    # unique 9-digit Uzbek-style number
    return "9" + uuid.uuid4().hex[:8].replace("a", "1").replace("b", "2").replace(
        "c", "3").replace("d", "4").replace("e", "5").replace("f", "6")


@pytest.fixture(scope="module")
def pipeline(api_client):
    resp = api_client.get("/pipelines")
    assert resp.status_code == 200, resp.text
    pipelines = resp.json()["data"]
    assert len(pipelines) >= 1, "default pipeline must be auto-seeded"
    default = next((p for p in pipelines if p.get("is_default")), pipelines[0])
    stages = default.get("stages") or []
    assert len(stages) >= 3
    return default


def _stage(pipeline, **kw):
    for s in pipeline["stages"]:
        if all(s.get(k) == v for k, v in kw.items()):
            return s
    return None


def _create_lead(api_client, **overrides):
    payload = {
        "contact_name": f"Test Lead {uuid.uuid4().hex[:6]}",
        "company_name": "CRM Test MChJ",
        "phone": "+998" + _mkphone(),
        "email": f"crm-{uuid.uuid4().hex[:8]}@test.uz",
        "source": "telegram",
        "expected_value": 5_000_000,
        "currency": "UZS",
    }
    payload.update(overrides)
    resp = api_client.post("/leads", json=payload)
    assert resp.status_code in (200, 201), resp.text
    return resp.json()["data"]


class TestPipelineSetup:
    def test_default_pipeline_has_terminal_stages(self, pipeline):
        assert _stage(pipeline, is_won=True) is not None, "won stage missing"
        assert _stage(pipeline, is_lost=True) is not None, "lost stage missing"
        open_stages = [s for s in pipeline["stages"] if not s["is_won"] and not s["is_lost"]]
        assert len(open_stages) >= 3

    def test_terminal_stages_ordered_last(self, pipeline):
        open_max = max(s["sequence"] for s in pipeline["stages"]
                       if not s["is_won"] and not s["is_lost"])
        won = _stage(pipeline, is_won=True)
        lost = _stage(pipeline, is_lost=True)
        assert won["sequence"] > open_max
        assert lost["sequence"] > won["sequence"]

    def test_qualified_stage_is_open_negotiation(self, pipeline):
        q = next((s for s in pipeline["stages"] if s["code"] == "qualified"), None)
        if q is None:
            pytest.skip("no qualified stage in this pipeline")
        assert q["is_won"] is False, "seed bug: qualified must not be the hidden win"

    def test_lost_reasons_seeded(self, api_client):
        resp = api_client.get("/lost-reasons")
        assert resp.status_code == 200, resp.text
        reasons = resp.json()["data"]
        assert len(reasons) >= 4


class TestLeadLifecycle:
    def test_create_lands_in_first_open_stage_with_amount(self, api_client, pipeline):
        lead = _create_lead(api_client)
        assert lead["stage_id"] is not None
        assert lead["currency"] == "UZS"
        assert float(lead["expected_value"]) == 5_000_000
        first_open = min((s for s in pipeline["stages"] if not s["is_won"] and not s["is_lost"]),
                         key=lambda s: s["sequence"])
        assert lead["stage_id"] == first_open["id"]

    def test_move_writes_stage_history_and_status(self, api_client, pipeline, db_read):
        lead = _create_lead(api_client)
        target = _stage(pipeline, code="in_progress") or _stage(pipeline, code="contacted")
        resp = api_client.post(f"/leads/{lead['id']}/move", json={"stage_id": target["id"]})
        assert resp.status_code == 200, resp.text

        got = api_client.get(f"/leads/{lead['id']}").json()["data"]
        assert got["stage_id"] == target["id"]
        assert got["status"] == target["code"]
        assert got.get("last_activity_at")

        db_read.execute(
            "SELECT COUNT(*) AS n FROM lead_stage_history WHERE lead_id = %s", (lead["id"],))
        assert db_read.fetchone()["n"] >= 2  # initial + move

    def test_move_to_won_stage_requires_win_flow(self, api_client, pipeline):
        lead = _create_lead(api_client)
        won = _stage(pipeline, is_won=True)
        resp = api_client.post(f"/leads/{lead['id']}/move", json={"stage_id": won["id"]})
        assert resp.status_code == 409, resp.text
        assert resp.json()["error"]["code"] == "WON_FLOW_REQUIRED"

    def test_move_to_lost_stage_requires_reason(self, api_client, pipeline):
        lead = _create_lead(api_client)
        lost = _stage(pipeline, is_lost=True)
        resp = api_client.post(f"/leads/{lead['id']}/move", json={"stage_id": lost["id"]})
        assert resp.status_code == 409, resp.text
        assert resp.json()["error"]["code"] == "LOST_REASON_REQUIRED"


class TestWinFlow:
    def test_win_creates_partner_and_stamps_converted_to(self, api_client, pipeline, db_read):
        lead = _create_lead(api_client)
        resp = api_client.post(f"/leads/{lead['id']}/won", json={})
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert data["partner_id"]
        assert data["partner_created"] is True

        got = api_client.get(f"/leads/{lead['id']}").json()["data"]
        assert got["won_at"] is not None
        assert got["partner_id"] == data["partner_id"]
        assert got["status"] == "won"

        # converted_to must reference a real contact (the old ConvertLead
        # wrote opportunity UUIDs into this contacts FK)
        db_read.execute(
            "SELECT c.id FROM leads l JOIN contacts c ON c.id = l.converted_to WHERE l.id = %s",
            (lead["id"],))
        assert db_read.fetchone() is not None

    def test_win_dedupes_partner_by_normalized_phone(self, api_client):
        phone_digits = _mkphone()
        # first win creates the partner with a formatted number
        lead1 = _create_lead(api_client, phone=f"+998 {phone_digits[:2]} {phone_digits[2:5]} {phone_digits[5:7]} {phone_digits[7:]}")
        r1 = api_client.post(f"/leads/{lead1['id']}/won", json={})
        assert r1.status_code == 200, r1.text
        partner1 = r1.json()["data"]["partner_id"]

        # second lead, same digits without formatting → must LINK, not duplicate
        lead2 = _create_lead(api_client, phone=phone_digits)
        r2 = api_client.post(f"/leads/{lead2['id']}/won", json={})
        assert r2.status_code == 200, r2.text
        assert r2.json()["data"]["partner_id"] == partner1
        assert r2.json()["data"]["partner_created"] is False

    def test_double_win_conflicts(self, api_client):
        lead = _create_lead(api_client)
        assert api_client.post(f"/leads/{lead['id']}/won", json={}).status_code == 200
        assert api_client.post(f"/leads/{lead['id']}/won", json={}).status_code == 409


class TestLossFlow:
    def test_lost_requires_reason(self, api_client):
        lead = _create_lead(api_client)
        resp = api_client.post(f"/leads/{lead['id']}/lost", json={})
        assert resp.status_code == 400

    def test_lost_with_reason(self, api_client):
        lead = _create_lead(api_client)
        reasons = api_client.get("/lost-reasons").json()["data"]
        resp = api_client.post(
            f"/leads/{lead['id']}/lost",
            json={"lost_reason_id": reasons[0]["id"], "note": "test loss"})
        assert resp.status_code == 200, resp.text

        got = api_client.get(f"/leads/{lead['id']}").json()["data"]
        assert got["lost_at"] is not None
        assert got["lost_reason_id"] == reasons[0]["id"]
        assert got["status"] == "lost"

    def test_invalid_reason_rejected(self, api_client):
        lead = _create_lead(api_client)
        resp = api_client.post(
            f"/leads/{lead['id']}/lost",
            json={"lost_reason_id": str(uuid.uuid4())})
        assert resp.status_code == 404


class TestStatsAndReports:
    def test_stats_shape(self, api_client):
        stats = api_client.get("/leads/stats").json()["data"]
        for key in ("total_leads", "open_leads", "open_value", "won_leads",
                    "won_this_month", "won_value_month", "conversion_rate", "avg_deal_size"):
            assert key in stats, f"missing {key}"
        assert stats["total_leads"] >= stats["open_leads"]

    def test_funnel_report(self, api_client, pipeline):
        resp = api_client.get("/crm/reports/funnel", params={"pipeline_id": pipeline["id"]})
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert "stages" in data and "totals" in data
        # open stages ordered by sequence, lost excluded
        seqs = [s["sequence"] for s in data["stages"]]
        assert seqs == sorted(seqs)

    def test_sources_report_counts_wins(self, api_client):
        # dedicated source so the fixture rows are identifiable
        src = f"src_{uuid.uuid4().hex[:6]}"
        l1 = _create_lead(api_client, source=src, expected_value=1_000_000)
        l2 = _create_lead(api_client, source=src, expected_value=2_000_000)
        assert api_client.post(f"/leads/{l1['id']}/won", json={}).status_code == 200
        reasons = api_client.get("/lost-reasons").json()["data"]
        assert api_client.post(
            f"/leads/{l2['id']}/lost", json={"lost_reason_id": reasons[0]["id"]}).status_code == 200

        rows = api_client.get("/crm/reports/sources").json()["data"]
        row = next((r for r in rows if r["source"] == src), None)
        assert row is not None
        assert row["total"] == 2 and row["won"] == 1 and row["lost"] == 1
        assert row["win_rate"] == 50.0
        assert float(row["won_value"]) == 1_000_000

    def test_loss_reasons_report(self, api_client):
        data = api_client.get("/crm/reports/loss-reasons").json()["data"]
        assert "reasons" in data and "total_lost" in data
        assert data["total_lost"] >= 1  # earlier tests lost leads
        named = [r for r in data["reasons"] if r["reason"]]
        assert len(named) >= 1

    def test_managers_report(self, api_client):
        resp = api_client.get("/crm/reports/managers")
        assert resp.status_code == 200
        assert isinstance(resp.json()["data"], list)


class TestSecurity:
    def test_unauthenticated_401(self):
        for path in ("/leads", "/pipelines", "/lost-reasons", "/crm/reports/funnel"):
            resp = requests.get(f"{BASE_URL}{path}")
            assert resp.status_code == 401, f"{path} → {resp.status_code}"

    def test_tenant_isolation_on_lead_read(self, api_client):
        lead = _create_lead(api_client)
        spoofed = requests.get(
            f"{BASE_URL}/leads/{lead['id']}",
            headers={
                "Authorization": f"Bearer {api_client.token}",
                "X-Tenant-ID": str(uuid.uuid4()),
            })
        # a foreign tenant must never see the lead
        assert spoofed.status_code in (401, 403, 404), spoofed.status_code

    def test_timeline_and_tasks_endpoints(self, api_client):
        lead = _create_lead(api_client)
        tl = api_client.get(f"/leads/{lead['id']}/timeline")
        assert tl.status_code == 200
        items = tl.json()["data"]
        assert any(i["type"] == "stage_change" for i in items), "initial stage row missing"
        tk = api_client.get(f"/leads/{lead['id']}/tasks")
        assert tk.status_code == 200
        assert tk.json()["data"] == []
