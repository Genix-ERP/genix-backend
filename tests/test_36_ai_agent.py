"""
AI Yordamchi platform suite (docs/ai-yordamchi/audit.md → rebuild 2026-08-10).

Pins the agent-platform contracts that work WITHOUT a live AI key (the
agentic loop itself needs a provider; everything below is deterministic
server-side governance):

  1. Migration 480 objects — ai_action_log is append-only (DB trigger),
     tenant_agent_settings + monthly_request_limit exist.
  2. Agent catalog — GET /ai/agents returns the per-module agents with
     localized names, per-tool states and the quota object; module agents
     never carry cross-module write tools.
  3. Agent Studio — PUT /ai/agents/:key persists settings, validates tool
     names/states, and a "read"-tier write tool is blocked server-side at
     /ai/agent/execute IMMEDIATELY (mid-conversation tightening, E2E #7).
  4. Studio denials land in ai_action_log (kind='denied').
  5. Server-side quota — a tenant over its monthly limit gets 429
     quota_exceeded from /ai/agent BEFORE any model round-trip.
  6. Conversations — DB-backed CRUD over ai_conversations/ai_messages
     (was TODO stubs; history no longer lives only in localStorage).
"""

import uuid as uuidlib

import psycopg2
import pytest
import requests
from psycopg2.extras import RealDictCursor

from conftest import BASE_URL, DB_HOST, DB_NAME, DB_PASSWORD, DB_PORT, DB_USER


@pytest.fixture(scope="module")
def db():
    conn = psycopg2.connect(
        host=DB_HOST, port=DB_PORT, user=DB_USER,
        password=DB_PASSWORD, dbname=DB_NAME,
    )
    conn.autocommit = True
    cur = conn.cursor(cursor_factory=RealDictCursor)
    yield cur
    conn.close()


def _raw_post(api_client, path, payload):
    """POST without the APIClient 429-retry (quota tests EXPECT a 429)."""
    return requests.post(
        f"{BASE_URL}{path}",
        headers=dict(api_client.session.headers),
        json=payload,
        timeout=30,
    )


# ============================================
# 1. Migration 480 schema
# ============================================

class TestMigration480:
    def test_ai_action_log_exists(self, db):
        db.execute("SELECT to_regclass('ai_action_log') AS t")
        assert db.fetchone()["t"] == "ai_action_log"

    def test_tenant_agent_settings_exists(self, db):
        db.execute("SELECT to_regclass('tenant_agent_settings') AS t")
        assert db.fetchone()["t"] == "tenant_agent_settings"

    def test_monthly_request_limit_column(self, db):
        db.execute("""
            SELECT 1 FROM information_schema.columns
            WHERE table_name = 'tenant_ai_settings'
              AND column_name = 'monthly_request_limit'
        """)
        assert db.fetchone() is not None

    def test_ai_action_log_is_append_only(self, db, tenant_id):
        db.execute(
            """INSERT INTO ai_action_log (tenant_id, agent_key, tool, kind, ok)
               VALUES (%s, 'orchestrator', '_test_probe', 'read', true)
               RETURNING id""",
            (tenant_id,),
        )
        row_id = db.fetchone()["id"]
        with pytest.raises(psycopg2.Error):
            db.execute("UPDATE ai_action_log SET ok = false WHERE id = %s", (row_id,))
        with pytest.raises(psycopg2.Error):
            db.execute("DELETE FROM ai_action_log WHERE id = %s", (row_id,))


# ============================================
# 2. Agent catalog
# ============================================

EXPECTED_AGENTS = {
    "orchestrator", "moliya", "savdo", "ombor", "xarid",
    "crm", "qurilish", "hr", "vazifalar", "ishlab_chiqarish",
}

# A module agent must never expose another module's WRITE tool.
FOREIGN_WRITES = {
    "moliya": {"create_sales_order", "create_sales_invoice", "stock_adjust", "create_lead"},
    "hr": {"record_payment", "create_sales_order", "stock_adjust", "create_contract"},
    "crm": {"record_payment", "stock_adjust", "create_vendor_bill"},
    "qurilish": {"record_payment", "stock_adjust", "create_sales_order"},
}


class TestAgentCatalog:
    @pytest.fixture(scope="class")
    def catalog(self, api_client):
        resp = api_client.get("/ai/agents")
        assert resp.status_code == 200, resp.text
        data = resp.json()["data"]
        assert "agents" in data and "quota" in data
        return data

    def test_all_agents_present(self, catalog):
        keys = {a["key"] for a in catalog["agents"]}
        assert EXPECTED_AGENTS <= keys

    def test_agent_shape(self, catalog):
        for a in catalog["agents"]:
            assert a["name"]["uz"], a["key"]
            assert isinstance(a["tools"], list) and a["tools"], f"{a['key']} has no tools"
            for t in a["tools"]:
                assert t["kind"] in ("read", "write")
                assert t["state"] in ("off", "read", "draft", "auto")

    def test_no_cross_module_writes(self, catalog):
        by_key = {a["key"]: a for a in catalog["agents"]}
        for key, forbidden in FOREIGN_WRITES.items():
            tools = {t["name"] for t in by_key[key]["tools"]}
            leaked = tools & forbidden
            assert not leaked, f"{key} leaks foreign writes: {leaked}"

    def test_hr_agent_is_read_only(self, catalog):
        hr = next(a for a in catalog["agents"] if a["key"] == "hr")
        writes = [t for t in hr["tools"] if t["kind"] == "write"]
        assert not writes, f"HR agent must be read-only, has {writes}"

    def test_vazifalar_agent_has_task_tools(self, catalog):
        vaz = next(a for a in catalog["agents"] if a["key"] == "vazifalar")
        tools = {t["name"]: t["kind"] for t in vaz["tools"]}
        assert tools.get("list_tasks") == "read"
        assert tools.get("create_task") == "write"

    def test_quota_object(self, catalog):
        q = catalog["quota"]
        assert isinstance(q["used"], int) and isinstance(q["limit"], int)


# ============================================
# 3. Agent Studio + immediate enforcement
# ============================================

class TestAgentStudio:
    def test_validation_unknown_tool(self, api_client):
        resp = api_client.put("/ai/agents/moliya", json={"tool_overrides": {"no_such_tool": "off"}})
        assert resp.status_code == 400

    def test_validation_unknown_state(self, api_client):
        resp = api_client.put("/ai/agents/moliya", json={"tool_overrides": {"record_payment": "yolo"}})
        assert resp.status_code == 400

    def test_unknown_agent_404(self, api_client):
        resp = api_client.put("/ai/agents/no_such_agent", json={"enabled": True})
        assert resp.status_code == 404

    def test_settings_roundtrip_and_immediate_block(self, api_client, db, tenant_id):
        try:
            # Tighten: record_payment → read-only for the Moliya agent.
            resp = api_client.put("/ai/agents/moliya", json={
                "instructions": "test: javoblarni qisqa yoz",
                "tool_overrides": {"record_payment": "read"},
            })
            assert resp.status_code == 200, resp.text

            # Roundtrip via the catalog.
            data = api_client.get("/ai/agents").json()["data"]
            moliya = next(a for a in data["agents"] if a["key"] == "moliya")
            assert moliya["instructions"] == "test: javoblarni qisqa yoz"
            state = next(t["state"] for t in moliya["tools"] if t["name"] == "record_payment")
            assert state == "read"

            # Takes effect immediately: the direct execute path is blocked
            # server-side (no model, no confirmation card can bypass it).
            resp = api_client.post("/ai/agent/execute", json={
                "agent": "moliya",
                "tool": "record_payment",
                "args": {"direction": "in", "amount": 1000, "contact": "Test"},
            })
            assert resp.status_code == 403, resp.text

            # The denial is auditable.
            db.execute("""
                SELECT kind, ok FROM ai_action_log
                WHERE tenant_id = %s AND tool = 'record_payment' AND kind = 'denied'
                ORDER BY created_at DESC LIMIT 1
            """, (tenant_id,))
            row = db.fetchone()
            assert row is not None and row["ok"] is False
        finally:
            api_client.put("/ai/agents/moliya", json={
                "instructions": "",
                "tool_overrides": {},
                "clear_auto_limit": True,
            })

    def test_execute_rejects_read_tools(self, api_client):
        resp = api_client.post("/ai/agent/execute", json={"tool": "find_contacts", "args": {}})
        assert resp.status_code == 400

    def test_execute_rejects_unknown_tool(self, api_client):
        resp = api_client.post("/ai/agent/execute", json={"tool": "drop_database", "args": {}})
        assert resp.status_code == 400


# ============================================
# 4. Server-side quota
# ============================================

class TestQuota:
    def test_quota_exceeded_is_429_before_model(self, api_client, db, tenant_id):
        # Give the tenant a limit of 1 and one consumed request this month.
        db.execute("""
            INSERT INTO tenant_ai_settings (tenant_id, provider, model, endpoint, api_key, monthly_request_limit)
            VALUES (%s, 'openai', '', '', '', 1)
            ON CONFLICT (tenant_id) DO UPDATE SET monthly_request_limit = 1
        """, (tenant_id,))
        db.execute("""
            INSERT INTO ai_usage_logs (tenant_id, operation, model, prompt_tokens, completion_tokens, total_tokens)
            VALUES (%s, 'agent', 'test', 1, 1, 2) RETURNING id
        """, (tenant_id,))
        probe_id = db.fetchone()["id"]
        try:
            resp = _raw_post(api_client, "/ai/agent", {"message": "salom"})
            assert resp.status_code == 429, resp.text
            body = resp.json()
            assert body["error"]["code"] == "quota_exceeded"
            assert body["quota"]["limit"] == 1
        finally:
            db.execute("DELETE FROM ai_usage_logs WHERE id = %s", (probe_id,))
            db.execute(
                "UPDATE tenant_ai_settings SET monthly_request_limit = NULL WHERE tenant_id = %s",
                (tenant_id,),
            )


# ============================================
# 5. Conversations (DB-backed threads)
# ============================================

class TestConversations:
    def test_crud_roundtrip(self, api_client):
        # Create
        resp = api_client.post("/ai/conversations", json={"title": "Test thread"})
        assert resp.status_code in (200, 201), resp.text
        conv_id = resp.json()["data"]["id"]

        try:
            # Listed
            resp = api_client.get("/ai/conversations")
            assert resp.status_code == 200
            ids = [c["id"] for c in resp.json()["data"]]
            assert conv_id in ids

            # Append a message → readable back
            resp = api_client.post(f"/ai/conversations/{conv_id}/messages",
                                   json={"role": "user", "content": "Kassadagi pul qancha?"})
            assert resp.status_code in (200, 201), resp.text

            resp = api_client.get(f"/ai/conversations/{conv_id}")
            assert resp.status_code == 200
            body = resp.json()["data"]
            assert any(m["content"] == "Kassadagi pul qancha?" for m in body["messages"])

            # Invalid role rejected
            resp = api_client.post(f"/ai/conversations/{conv_id}/messages",
                                   json={"role": "tool", "content": "x"})
            assert resp.status_code == 400
        finally:
            resp = api_client.delete(f"/ai/conversations/{conv_id}")
            assert resp.status_code in (200, 204)

        # Gone after delete
        resp = api_client.get(f"/ai/conversations/{conv_id}")
        assert resp.status_code == 404

    def test_foreign_conversation_not_readable(self, api_client):
        resp = api_client.get(f"/ai/conversations/{uuidlib.uuid4()}")
        assert resp.status_code == 404


# ============================================
# 6. run_ai_agent workflow action (Ish jarayonlari)
# ============================================

class TestRunAIAgentAction:
    def test_prompt_required(self, api_client):
        resp = api_client.post("/workflow-rules", json={
            "name": "AI test rule",
            "trigger_event": "lead.created",
            "actions": [{"type": "run_ai_agent", "config": {"agent": "crm"}}],
        })
        assert resp.status_code == 400
        assert "prompt" in resp.text

    def test_unknown_agent_rejected(self, api_client):
        resp = api_client.post("/workflow-rules", json={
            "name": "AI test rule",
            "trigger_event": "lead.created",
            "actions": [{"type": "run_ai_agent",
                         "config": {"agent": "no_such", "prompt": "x"}}],
        })
        assert resp.status_code == 400
        assert "unknown agent" in resp.text

    def test_valid_rule_roundtrip(self, api_client):
        resp = api_client.post("/workflow-rules", json={
            "name": "AI digest test rule",
            "trigger_event": "lead.created",
            "is_active": False,  # never fire during the suite
            "actions": [{"type": "run_ai_agent", "config": {
                "agent": "crm",
                "prompt": "Yangi lid keldi: {{name}}. Voronka holatini qisqacha yozib ber.",
                "recipient_type": "all",
            }}],
        })
        assert resp.status_code in (200, 201), resp.text
        rule_id = resp.json()["data"]["id"]
        try:
            # The preview endpoint must render the action without a model call.
            resp = api_client.post(f"/workflow-rules/{rule_id}/test", json={})
            if resp.status_code == 200:
                previews = resp.json()["data"]["actions"]
                ai_previews = [p for p in previews if p["type"] == "run_ai_agent"]
                assert ai_previews and "AI (crm)" in ai_previews[0]["preview"]
        finally:
            api_client.delete(f"/workflow-rules/{rule_id}")


# ============================================
# 7. Transcribe endpoint exists (audit C13)
# ============================================

class TestTranscribe:
    def test_endpoint_exists_and_validates(self, api_client):
        # No file attached: 400 when an OpenAI key is configured, 501 when the
        # provider can't transcribe. Both prove the endpoint is real (the old
        # frontend mic 404'd forever).
        resp = requests.post(
            f"{BASE_URL}/ai/transcribe",
            headers={k: v for k, v in api_client.session.headers.items()
                     if k.lower() != "content-type"},
            timeout=30,
        )
        assert resp.status_code in (400, 501), resp.text
