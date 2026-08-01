"""
Genix ERP - Ish jarayonlari (workflow automation) moduli testlari

Qamrov:
  1. Qoida CRUD + server-side validatsiya (noto'g'ri event/action rad etiladi,
     category/trigger_type katalogdan hisoblanadi)
  2. Tenant izolyatsiyasi - qoidalar va jurnal boshqa tenantga ko'rinmaydi
  3. Shartlarni baholash (AND/OR guruhlari, operatorlar) - dry-run test endpoint
  4. End-to-end: lead.created -> bildirishnoma + vazifa yaratish harakatlari
  5. Loop guard: qoida o'zini qayta ishga tushira olmaydi (zanjir 1 qadamda to'xtaydi)
  6. Nusxalash, jurnal filtrlari, retry
"""
import time
import uuid

import pytest

from conftest import BASE_URL, APIClient


# ============================================
# HELPERS / FIXTURES
# ============================================

def _mk_rule(api_client, **overrides):
    payload = {
        "name": f"Test qoida {uuid.uuid4().hex[:6]}",
        "trigger_event": "lead.created",
        "conditions": {},
        "actions": [{"type": "create_notification",
                     "config": {"message": "Yangi lid: {{contact_name}}", "recipient_type": "all"}}],
        "is_active": True,
    }
    payload.update(overrides)
    resp = api_client.post("/workflow-rules", json=payload)
    return resp


@pytest.fixture()
def rule(api_client):
    resp = _mk_rule(api_client)
    assert resp.status_code in (200, 201), resp.text
    data = resp.json()["data"]
    yield data
    api_client.delete(f"/workflow-rules/{data['id']}")


def _wait_for(fn, timeout=6.0, interval=0.5):
    """Poll fn() until it returns a truthy value or timeout expires."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = fn()
        if result:
            return result
        time.sleep(interval)
    return None


def _q(cur, sql, params=None):
    """Run a query on the conftest db_read cursor and return all rows."""
    cur.execute(sql, params)
    if cur.description is None:
        return []
    return cur.fetchall()


# ============================================
# 1. CRUD + VALIDATSIYA
# ============================================

class TestRuleValidation:
    def test_create_and_derive_category(self, api_client, rule):
        # category/trigger_type are derived server-side from the event catalog
        assert rule["category"] == "crm"
        assert rule["trigger_type"] == "event"
        assert rule["is_active"] is True

    def test_scheduled_event_derives_trigger_type(self, api_client):
        resp = _mk_rule(api_client, trigger_event="invoice.overdue",
                        trigger_type="event")  # client value must be ignored
        assert resp.status_code in (200, 201), resp.text
        data = resp.json()["data"]
        assert data["trigger_type"] == "scheduled"
        assert data["category"] == "sales"
        api_client.delete(f"/workflow-rules/{data['id']}")

    def test_unknown_event_rejected(self, api_client):
        resp = _mk_rule(api_client, trigger_event="nonexistent.event")
        assert resp.status_code == 400, resp.text

    def test_empty_actions_rejected(self, api_client):
        resp = _mk_rule(api_client, actions=[])
        assert resp.status_code == 400, resp.text

    def test_unknown_action_type_rejected(self, api_client):
        resp = _mk_rule(api_client, actions=[{"type": "launch_rocket", "config": {}}])
        assert resp.status_code == 400, resp.text

    def test_notification_without_message_rejected(self, api_client):
        resp = _mk_rule(api_client, actions=[{"type": "create_notification", "config": {}}])
        assert resp.status_code == 400, resp.text

    def test_update_field_outside_whitelist_rejected(self, api_client):
        resp = _mk_rule(api_client, actions=[{
            "type": "update_field",
            "config": {"target": "users", "field": "password_hash", "value": "x"},
        }])
        assert resp.status_code == 400, resp.text

    def test_events_catalog_endpoint(self, api_client):
        resp = api_client.get("/workflow-events")
        assert resp.status_code == 200, resp.text
        events = resp.json()["data"]
        ids = {e["event"] for e in events}
        # contract.expiring was renamed to contracts.expiring_soon in the
        # Shartnomalar rebuild (migration 443 renames stored rules too).
        for expected in ("lead.created", "invoice.overdue", "task.assigned",
                        "sales_order.created", "contracts.expiring_soon"):
            assert expected in ids
        overdue = next(e for e in events if e["event"] == "invoice.overdue")
        assert overdue["scheduled"] is True

    def test_reactivation_clears_auto_pause(self, api_client, rule, db_read):
        rid = rule["id"]
        _q(db_read,  # simulate the guard pausing it
           "UPDATE workflow_rules SET is_active=false, auto_paused_at=NOW(), paused_reason='rate_limit' WHERE id=%s RETURNING id",
           (rid,))
        resp = api_client.put(f"/workflow-rules/{rid}", json={"is_active": True})
        assert resp.status_code == 200, resp.text
        row = _q(db_read, "SELECT is_active, auto_paused_at, paused_reason FROM workflow_rules WHERE id=%s", (rid,))
        assert row[0]["is_active"] is True
        assert row[0]["auto_paused_at"] is None
        assert row[0]["paused_reason"] is None


# ============================================
# 2. TENANT IZOLYATSIYASI
# ============================================

class TestTenantIsolation:
    @pytest.fixture(scope="class")
    def foreign_client(self, auth_token):
        return APIClient(base_url=BASE_URL, token=auth_token["token"], tenant_id=str(uuid.uuid4()))

    def test_foreign_tenant_cannot_read_rule(self, rule, foreign_client):
        resp = foreign_client.get(f"/workflow-rules/{rule['id']}")
        assert resp.status_code in (401, 403, 404), resp.text

    def test_foreign_tenant_cannot_update_rule(self, rule, foreign_client):
        resp = foreign_client.put(f"/workflow-rules/{rule['id']}", json={"name": "hacked"})
        assert resp.status_code in (401, 403, 404), resp.text

    def test_foreign_tenant_cannot_delete_rule(self, rule, foreign_client):
        resp = foreign_client.delete(f"/workflow-rules/{rule['id']}")
        assert resp.status_code in (401, 403, 404), resp.text

    def test_rule_list_does_not_leak(self, rule, foreign_client):
        resp = foreign_client.get("/workflow-rules")
        if resp.status_code == 200:
            assert rule["id"] not in [r["id"] for r in resp.json()["data"]]
        else:
            assert resp.status_code in (401, 403)

    def test_logs_do_not_leak(self, foreign_client, api_client):
        own = api_client.get("/workflow-logs")
        assert own.status_code == 200
        resp = foreign_client.get("/workflow-logs")
        if resp.status_code == 200:
            own_ids = {l["id"] for l in own.json()["data"]}
            foreign_ids = {l["id"] for l in resp.json()["data"]}
            assert not (own_ids & foreign_ids) or not own_ids
        else:
            assert resp.status_code in (401, 403)


# ============================================
# 3. SHARTLAR (dry-run test endpoint)
# ============================================

class TestConditions:
    def _test(self, api_client, rule_id, data=None):
        resp = api_client.post(f"/workflow-rules/{rule_id}/test", json={"data": data} if data else {})
        assert resp.status_code == 200, resp.text
        return resp.json()["data"]

    def test_no_conditions_always_match(self, api_client, rule):
        result = self._test(api_client, rule["id"])
        assert result["matched"] is True

    def test_and_group(self, api_client):
        resp = _mk_rule(api_client, conditions={
            "logic": "and",
            "conditions": [
                {"field": "expected_value", "operator": "gte", "value": 1000},
                {"field": "source", "operator": "eq", "value": "website"},
            ],
        })
        assert resp.status_code in (200, 201), resp.text
        rid = resp.json()["data"]["id"]
        try:
            ok = self._test(api_client, rid, {"expected_value": 5000, "source": "website"})
            assert ok["matched"] is True
            assert all(c["passed"] for c in ok["condition_results"])

            fail = self._test(api_client, rid, {"expected_value": 500, "source": "website"})
            assert fail["matched"] is False
            assert any(not c["passed"] for c in fail["condition_results"])
        finally:
            api_client.delete(f"/workflow-rules/{rid}")

    def test_or_group(self, api_client):
        resp = _mk_rule(api_client, conditions={
            "logic": "or",
            "conditions": [
                {"field": "source", "operator": "eq", "value": "telegram"},
                {"field": "expected_value", "operator": "gt", "value": 9000},
            ],
        })
        assert resp.status_code in (200, 201), resp.text
        rid = resp.json()["data"]["id"]
        try:
            ok = self._test(api_client, rid, {"source": "telegram", "expected_value": 1})
            assert ok["matched"] is True
            fail = self._test(api_client, rid, {"source": "website", "expected_value": 1})
            assert fail["matched"] is False
        finally:
            api_client.delete(f"/workflow-rules/{rid}")

    def test_contains_operator(self, api_client):
        resp = _mk_rule(api_client, conditions={
            "logic": "and",
            "conditions": [{"field": "company_name", "operator": "contains", "value": "stroy"}],
        })
        assert resp.status_code in (200, 201), resp.text
        rid = resp.json()["data"]["id"]
        try:
            ok = self._test(api_client, rid, {"company_name": "Karimov STROY invest"})
            assert ok["matched"] is True  # case-insensitive
            fail = self._test(api_client, rid, {"company_name": "Metall Servis"})
            assert fail["matched"] is False
        finally:
            api_client.delete(f"/workflow-rules/{rid}")

    def test_legacy_condition_format_still_works(self, api_client):
        resp = _mk_rule(api_client, conditions={"expected_value": {"$gte": 100}})
        assert resp.status_code in (200, 201), resp.text
        rid = resp.json()["data"]["id"]
        try:
            ok = self._test(api_client, rid, {"expected_value": 200})
            assert ok["matched"] is True
            fail = self._test(api_client, rid, {"expected_value": 50})
            assert fail["matched"] is False
        finally:
            api_client.delete(f"/workflow-rules/{rid}")


# ============================================
# 4. END-TO-END: lead.created -> harakatlar
# ============================================

class TestEndToEnd:
    def test_lead_created_fires_notification_rule(self, api_client, db_read, tenant_id):
        marker = uuid.uuid4().hex[:8]
        resp = _mk_rule(
            api_client,
            name=f"E2E notif {marker}",
            actions=[{"type": "create_notification",
                      "config": {"message": f"E2E {marker}: {{{{contact_name}}}}", "recipient_type": "all"}}],
        )
        assert resp.status_code in (200, 201), resp.text
        rid = resp.json()["data"]["id"]
        try:
            lead = api_client.post("/leads", json={
                "contact_name": f"Lid {marker}", "company_name": "Test MChJ", "source": "website",
            })
            assert lead.status_code in (200, 201), lead.text

            # engine goroutine yozishini kutamiz
            log_row = _wait_for(lambda: _q(db_read,
                "SELECT status, related_type, related_id, duration_ms, trigger_event "
                "FROM workflow_logs WHERE rule_id=%s ORDER BY executed_at DESC LIMIT 1", (rid,)))
            assert log_row, "workflow_logs yozuvi paydo bo'lmadi"
            assert log_row[0]["status"] == "success", log_row
            assert log_row[0]["trigger_event"] == "lead.created"
            assert log_row[0]["related_type"] == "lead"
            assert log_row[0]["related_id"]
            assert log_row[0]["duration_ms"] is not None

            notif = _q(db_read,
                "SELECT COUNT(*) AS n FROM notifications WHERE tenant_id=%s AND type='workflow' AND message LIKE %s",
                (tenant_id, f"E2E {marker}%"))
            assert notif[0]["n"] >= 1, "haqiqiy in-app bildirishnoma yaratilmadi"
        finally:
            api_client.delete(f"/workflow-rules/{rid}")

    def test_skipped_conditions_logged(self, api_client, db_read):
        resp = _mk_rule(api_client, conditions={
            "logic": "and",
            "conditions": [{"field": "expected_value", "operator": "gte", "value": 10**12}],
        })
        assert resp.status_code in (200, 201), resp.text
        rid = resp.json()["data"]["id"]
        try:
            lead = api_client.post("/leads", json={
                "contact_name": "Skip test", "company_name": "X", "expected_value": 5,
            })
            assert lead.status_code in (200, 201), lead.text
            log_row = _wait_for(lambda: _q(db_read,
                "SELECT status, condition_results FROM workflow_logs WHERE rule_id=%s LIMIT 1", (rid,)))
            assert log_row, "skipped_conditions log yozuvi yo'q"
            assert log_row[0]["status"] == "skipped_conditions"
            assert log_row[0]["condition_results"]
        finally:
            api_client.delete(f"/workflow-rules/{rid}")

    def test_create_task_action_and_loop_guard(self, api_client, db_read, tenant_id):
        """task.assigned qoidasi vazifa yaratadi (assignee bilan) -> yana
        task.assigned chiqadi, lekin zanjir guard tufayli qoida o'zini qayta
        ishga tushirmaydi: aynan 1 ta avtomatik vazifa."""
        emp_rows = _q(db_read,
            "SELECT id FROM employees WHERE tenant_id=%s AND deleted_at IS NULL LIMIT 1", (tenant_id,))
        if emp_rows:
            emp_id = str(emp_rows[0]["id"])
        else:
            emp_resp = api_client.post("/employees", json={
                "first_name": "WF", "last_name": "Test",
                "email": f"wf-test-{uuid.uuid4().hex[:6]}@example.com",
                "job_title": "Tester", "hire_date": "2026-01-01",
            })
            if emp_resp.status_code not in (200, 201):
                pytest.skip(f"Xodim yaratib bo'lmadi: {emp_resp.status_code}")
            emp_id = emp_resp.json()["data"]["id"]

        board_resp = api_client.post("/task-boards", json={"name": f"WF test {uuid.uuid4().hex[:6]}"})
        assert board_resp.status_code in (200, 201), board_resp.text
        board = board_resp.json()["data"]
        board_id = board["board"]["id"]

        marker = uuid.uuid4().hex[:8]
        rule_resp = _mk_rule(
            api_client,
            name=f"Loop guard {marker}",
            trigger_event="task.assigned",
            conditions={"logic": "and",
                        "conditions": [{"field": "board_id", "operator": "eq", "value": board_id}]},
            actions=[{"type": "create_task", "config": {
                "board_id": board_id,
                "title": f"AUTO {marker} <- {{{{task_title}}}}",
                "assignee_employee_ids": [emp_id],
                "priority": "high",
            }}],
        )
        assert rule_resp.status_code in (200, 201), rule_resp.text
        rid = rule_resp.json()["data"]["id"]

        try:
            task_resp = api_client.post(f"/task-boards/{board_id}/tasks", json={
                "title": f"Trigger {marker}", "assignee_ids": [emp_id],
            })
            assert task_resp.status_code in (200, 201), task_resp.text

            auto_tasks = _wait_for(lambda: _q(db_read,
                "SELECT id, title, priority FROM tasks WHERE board_id=%s AND title LIKE %s",
                (board_id, f"AUTO {marker}%")))
            assert auto_tasks, "create_task harakati vazifa yaratmadi"
            assert auto_tasks[0]["priority"] == "high"
            assert f"Trigger {marker}" in auto_tasks[0]["title"]  # {{task_title}} renderlandi

            # loop guard: yana kutamiz va faqat 1 ta AUTO vazifa borligini tasdiqlaymiz
            time.sleep(3)
            auto_tasks = _q(db_read,
                "SELECT id FROM tasks WHERE board_id=%s AND title LIKE %s",
                (board_id, f"AUTO {marker}%"))
            assert len(auto_tasks) == 1, f"loop guard ishlamadi: {len(auto_tasks)} ta avtomatik vazifa"
        finally:
            api_client.delete(f"/workflow-rules/{rid}")
            api_client.delete(f"/task-boards/{board_id}")


# ============================================
# 5. NUSXALASH, JURNAL, RETRY
# ============================================

class TestLogAndUtilities:
    def test_duplicate_rule(self, api_client, rule):
        resp = api_client.post(f"/workflow-rules/{rule['id']}/duplicate")
        assert resp.status_code in (200, 201), resp.text
        new_id = resp.json()["data"]["id"]
        try:
            dup = api_client.get(f"/workflow-rules/{new_id}").json()["data"]
            assert dup["is_active"] is False  # nusxa doim o'chirilgan holda
            assert "(nusxa)" in dup["name"]
            assert dup["trigger_event"] == rule["trigger_event"]
        finally:
            api_client.delete(f"/workflow-rules/{new_id}")

    def test_log_filters(self, api_client):
        resp = api_client.get("/workflow-logs", params={"status": "success", "limit": 10})
        assert resp.status_code == 200, resp.text
        for log in resp.json()["data"]:
            assert log["status"] == "success"

    def test_retry_failed_run(self, api_client, db_read):
        """update_field harakati yo'q record bilan fail bo'ladi -> retry endpoint
        yangi log yozuvi qaytaradi."""
        resp = _mk_rule(api_client, actions=[{
            "type": "update_field",
            "config": {"target": "leads", "field": "status", "value": "contacted"},
        }])
        assert resp.status_code in (200, 201), resp.text
        rid = resp.json()["data"]["id"]
        try:
            # record_id yo'q payload bilan sinovdan tashqari real ishga tushirish:
            # test endpointi emas, engine orqali - lead yaratmasdan iloji yo'q,
            # shuning uchun sun'iy failed log yozamiz va retry qilamiz
            log_id = str(uuid.uuid4())
            _q(db_read,
                "INSERT INTO workflow_logs (id, tenant_id, rule_id, trigger_data, actions_executed, status, error_message, executed_at, trigger_event) "
                "SELECT %s, tenant_id, id, '{\"record_id\": \"00000000-0000-0000-0000-000000000001\"}'::jsonb, '[]'::jsonb, 'failed', 'test', NOW(), 'lead.created' "
                "FROM workflow_rules WHERE id=%s RETURNING id",
                (log_id, rid))
            resp = api_client.post(f"/workflow-logs/{log_id}/retry")
            assert resp.status_code == 200, resp.text
            new_log = resp.json()["data"]
            assert new_log["status"] in ("failed", "partial")  # record hali ham yo'q
        finally:
            api_client.delete(f"/workflow-rules/{rid}")
