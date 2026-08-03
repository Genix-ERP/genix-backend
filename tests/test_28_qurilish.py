"""
Qurilish (Construction) hardening tests — docs/construction-audit.md /
docs/construction-changelog.md (2026-08-03 to'plamlar).

Invariants under test:
  1. GET /construction/projects/stats returns the v2 dashboard contract:
     totals{total_projects, by_status, contract_total, actual_total,
     actual_period, overdue_projects} + monthly_series[6] + per_project[],
     and total_projects equals the sum of by_status counts.
  2. Project status writes are vocabulary-checked: an unknown status (incl.
     legacy 'active') gets a clean 400 from PUT, and migration 461's CHECK
     constraint exists at the DB layer as the backstop.
  3. Migration 461 seeded the SINGULAR permission keys the routes gate on
     (construction:project:*, construction:smeta:*, construction:reports:read).
  4. project_files.tenant_id is UUID (was VARCHAR(100)) with the tenant FK.
  5. Subcontract file attachments (migration 463) round-trip: POST → list →
     the file does NOT leak into the project-level files list (or its
     files_count badge) → DELETE removes it.
  6. Project create/update writes global audit_logs rows
     (entity_type='construction_project', project id in new_values) and the
     construction.* workflow events exist in the backend catalog
     (GET /workflow-events).

NOT covered here (needs a second, non-admin token): the works role-gate deny
path — constructionWorkGateAllows returns false for an empty role set unless
the user is owner/site_admin. The conftest session logs in as an owner, which
legitimately bypasses the gate.

Requires the API server running and the seeded dev DB (see conftest.py).
"""

import uuid

import pytest


CODE_PREFIX = "QURT28"


def _make_project(api_client, suffix, **overrides):
    payload = {
        "code": f"{CODE_PREFIX}-{suffix}-{uuid.uuid4().hex[:6]}",
        "name": f"Qurilish test {suffix}",
        "region": "Toshkent shahri",
        "city": "Toshkent",
        "project_type": "residential",
    }
    payload.update(overrides)
    resp = api_client.post("/construction/projects", json=payload)
    assert resp.status_code in (200, 201), f"project create failed: {resp.text}"
    return resp.json()["data"]["id"]


@pytest.fixture(scope="module", autouse=True)
def _cleanup_projects(db_read):
    """Soft-delete every project this module created, after the module runs."""
    yield
    db_read.execute(
        "UPDATE construction_projects SET deleted_at = NOW() "
        "WHERE deleted_at IS NULL AND code LIKE %s",
        (f"{CODE_PREFIX}-%",),
    )


# ─── 1. Stats contract ────────────────────────────────────────────────────

def test_stats_contract_shape(api_client):
    resp = api_client.get("/construction/projects/stats")
    assert resp.status_code == 200, resp.text
    data = resp.json()["data"]

    totals = data["totals"]
    for key in ("total_projects", "by_status", "contract_total",
                "actual_total", "actual_period", "overdue_projects"):
        assert key in totals, f"totals.{key} missing"

    assert totals["total_projects"] == sum(totals["by_status"].values())

    series = data["monthly_series"]
    assert isinstance(series, list) and len(series) == 6
    for point in series:
        assert "month" in point and "value" in point

    assert isinstance(data["per_project"], list)


def test_stats_sees_new_project(api_client):
    before = api_client.get("/construction/projects/stats").json()["data"]
    _make_project(api_client, "STATS")
    after = api_client.get("/construction/projects/stats").json()["data"]
    assert after["totals"]["total_projects"] == before["totals"]["total_projects"] + 1
    assert after["totals"]["by_status"].get("draft", 0) >= 1


# ─── 2. Status vocabulary ────────────────────────────────────────────────

def test_status_put_rejects_unknown_status(api_client):
    pid = _make_project(api_client, "STATUS")
    for bad in ("active", "bogus_status"):
        resp = api_client.put(f"/construction/projects/{pid}", json={"status": bad})
        assert resp.status_code == 400, (
            f"status '{bad}' must 400, got {resp.status_code}: {resp.text}"
        )
    resp = api_client.put(f"/construction/projects/{pid}", json={"status": "planning"})
    assert resp.status_code == 200, resp.text


def test_status_check_constraint_exists(db_read):
    db_read.execute(
        "SELECT 1 FROM pg_constraint WHERE conname = 'chk_construction_project_status'"
    )
    assert db_read.fetchone() is not None, "migration 461 CHECK missing"


# ─── 3. Permission seed (migration 461) ──────────────────────────────────

def test_singular_permission_keys_seeded(db_read):
    db_read.execute(
        "SELECT resource, action FROM permissions "
        "WHERE module = 'construction' AND resource IN ('project', 'smeta', 'reports')"
    )
    got = {(r["resource"], r["action"]) for r in db_read.fetchall()}
    for expected in [("project", "read"), ("project", "create"),
                     ("project", "update"), ("project", "delete"),
                     ("smeta", "read"), ("smeta", "update"),
                     ("reports", "read")]:
        assert expected in got, f"permission construction:{expected[0]}:{expected[1]} not seeded"


# ─── 4. project_files typing (migration 461) ─────────────────────────────

def test_project_files_tenant_id_is_uuid(db_read):
    db_read.execute(
        "SELECT data_type FROM information_schema.columns "
        "WHERE table_name = 'project_files' AND column_name = 'tenant_id'"
    )
    row = db_read.fetchone()
    assert row and row["data_type"] == "uuid", f"tenant_id is {row}"

    db_read.execute(
        "SELECT 1 FROM pg_constraint WHERE conname = 'fk_project_files_tenant'"
    )
    assert db_read.fetchone() is not None, "tenant FK missing"


# ─── 5. Subcontract files (migration 463) ────────────────────────────────

def test_subcontract_files_roundtrip(api_client):
    pid = _make_project(api_client, "SUBF")

    resp = api_client.post(f"/construction/projects/{pid}/subcontracts", json={
        "name": "Test subpudrat",
        "partner_name": "Sub Test MChJ",
    })
    assert resp.status_code in (200, 201), f"subcontract create failed: {resp.text}"
    sub_id = resp.json()["data"]["id"]

    files_before = api_client.get(f"/construction/projects/{pid}/files").json()["data"] or []

    resp = api_client.post(f"/construction/subcontracts/{sub_id}/files", json={
        "file_id": "test-file-id",
        "file_url": "/api/v1/files/test-file-id",
        "filename": "shartnoma.pdf",
        "file_size": 1024,
        "mime_type": "application/pdf",
    })
    assert resp.status_code in (200, 201), f"sub file create failed: {resp.text}"
    file_id = resp.json()["data"]["id"]

    listed = api_client.get(f"/construction/subcontracts/{sub_id}/files").json()["data"]
    assert any(f["id"] == file_id for f in listed), "uploaded file not listed"

    # Must NOT leak into the project-level document list
    files_after = api_client.get(f"/construction/projects/{pid}/files").json()["data"] or []
    assert len(files_after) == len(files_before), (
        "subcontract file leaked into the project files list"
    )

    resp = api_client.delete(f"/construction/subcontracts/{sub_id}/files/{file_id}")
    assert resp.status_code == 200, resp.text
    listed = api_client.get(f"/construction/subcontracts/{sub_id}/files").json()["data"]
    assert not any(f["id"] == file_id for f in listed), "file survived delete"


# ─── 6. Audit + event catalog ────────────────────────────────────────────

def test_project_create_writes_audit_log(api_client, db_read, tenant_id):
    pid = _make_project(api_client, "AUDIT")
    db_read.execute(
        "SELECT 1 FROM audit_logs "
        "WHERE tenant_id = %s AND entity_type = 'construction_project' "
        "  AND action = 'create' AND new_values->>'project_id' = %s",
        (tenant_id, str(pid)),
    )
    assert db_read.fetchone() is not None, "create audit row missing"

    api_client.put(f"/construction/projects/{pid}", json={"status": "planning"})
    db_read.execute(
        "SELECT 1 FROM audit_logs "
        "WHERE tenant_id = %s AND entity_type = 'construction_project' "
        "  AND action = 'update' AND new_values->>'project_id' = %s",
        (tenant_id, str(pid)),
    )
    assert db_read.fetchone() is not None, "update audit row missing"


# ─── 7. S4: sub-akt → GL posting (developer scenario, migration 466) ─────

def _make_sub_act(api_client, db_read, suffix, amount):
    """Project + subcontract + forma2 act with a known amount."""
    pid = _make_project(api_client, suffix)
    resp = api_client.post(f"/construction/projects/{pid}/subcontracts", json={
        "name": f"Sub {suffix}",
        "partner_name": f"Sub {suffix} MChJ",
        "amount": amount,
    })
    assert resp.status_code in (200, 201), f"subcontract failed: {resp.text}"
    sub_id = resp.json()["data"]["id"]

    resp = api_client.post(f"/construction/projects/{pid}/acts", json={
        "act_type": "forma2",
        "subcontract_id": sub_id,
    })
    assert resp.status_code in (200, 201), f"act failed: {resp.text}"
    act_id = resp.json()["data"]["id"]

    # Acts normally roll their totals up from lines; pin the amount directly
    # so the invariant under test (posting math) is deterministic.
    db_read.execute(
        "UPDATE construction_act SET amount_total_with_vat = %s WHERE id = %s",
        (amount, act_id),
    )
    return pid, sub_id, act_id


def test_sub_act_approve_posts_balanced_je(api_client, db_read, tenant_id):
    amount = 5_000_000
    pid, sub_id, act_id = _make_sub_act(api_client, db_read, "S4POST", amount)

    resp = api_client.put(f"/construction/acts/{act_id}/approve")
    assert resp.status_code == 200, resp.text

    db_read.execute(
        "SELECT journal_entry_id, expense_line_id FROM construction_act WHERE id = %s",
        (act_id,),
    )
    act = db_read.fetchone()
    assert act["journal_entry_id"], "sub act approved without a journal entry"
    assert act["expense_line_id"], "sub act approved without an expense line"

    # Balanced, posted, Dr 0810 / Cr 6010, source = the CEL bridge row
    db_read.execute(
        "SELECT je.status, je.source_type, je.source_id, "
        "       SUM(l.debit_amount) AS d, SUM(l.credit_amount) AS c "
        "FROM journal_entries je JOIN journal_entry_lines l ON l.journal_entry_id = je.id "
        "WHERE je.id = %s GROUP BY je.id",
        (act["journal_entry_id"],),
    )
    je = db_read.fetchone()
    assert je["status"] == "posted"
    assert je["source_type"] == "construction_act"
    assert str(je["source_id"]) == str(act["expense_line_id"])
    assert float(je["d"]) == float(je["c"]) == float(amount)

    db_read.execute(
        "SELECT a.code, l.debit_amount, l.credit_amount "
        "FROM journal_entry_lines l JOIN accounts a ON a.id = l.account_id "
        "WHERE l.journal_entry_id = %s ORDER BY l.line_number",
        (act["journal_entry_id"],),
    )
    lines = db_read.fetchall()
    debit = next(l for l in lines if float(l["debit_amount"]) > 0)
    credit = next(l for l in lines if float(l["credit_amount"]) > 0)
    assert debit["code"].startswith("08"), f"debit hit {debit['code']}, expected 0810 WIP"
    assert credit["code"].startswith("60"), f"credit hit {credit['code']}, expected 6010 AP"

    # CEL row: approved, linked to the same JE, carries the subcontract
    db_read.execute(
        "SELECT status, journal_entry_id, subcontract_id, amount "
        "FROM construction_expense_lines WHERE id = %s",
        (act["expense_line_id"],),
    )
    cel = db_read.fetchone()
    assert cel["status"] == "approved"
    assert str(cel["journal_entry_id"]) == str(act["journal_entry_id"])
    assert cel["subcontract_id"] == sub_id
    assert float(cel["amount"]) == float(amount)

    # Double-approve refused (state guard = double-post guard)
    resp = api_client.put(f"/construction/acts/{act_id}/approve")
    assert resp.status_code == 400, "second approve must 400"


def test_own_forces_act_approve_posts_nothing(api_client, db_read):
    """Developer scenario: an own-forces act (no subcontract) is internal
    volume control — approval must NOT touch the GL."""
    pid = _make_project(api_client, "S4OWN")
    resp = api_client.post(f"/construction/projects/{pid}/acts", json={"act_type": "forma2"})
    assert resp.status_code in (200, 201), resp.text
    act_id = resp.json()["data"]["id"]
    db_read.execute(
        "UPDATE construction_act SET amount_total_with_vat = 9000000 WHERE id = %s", (act_id,)
    )

    resp = api_client.put(f"/construction/acts/{act_id}/approve")
    assert resp.status_code == 200, resp.text

    db_read.execute("SELECT journal_entry_id FROM construction_act WHERE id = %s", (act_id,))
    assert db_read.fetchone()["journal_entry_id"] is None


def test_cancelled_sub_act_is_reversed(api_client, db_read):
    amount = 3_000_000
    pid, sub_id, act_id = _make_sub_act(api_client, db_read, "S4CANC", amount)

    assert api_client.put(f"/construction/acts/{act_id}/approve").status_code == 200
    db_read.execute("SELECT journal_entry_id, expense_line_id FROM construction_act WHERE id = %s", (act_id,))
    act = db_read.fetchone()

    resp = api_client.put(f"/construction/acts/{act_id}/cancel",
                          json={"rejection_reason": "Test storno"})
    assert resp.status_code == 200, resp.text

    # Reversal entry exists: swapped sides, same amount, linked to original
    db_read.execute(
        "SELECT je.id, SUM(l.debit_amount) AS d, SUM(l.credit_amount) AS c "
        "FROM journal_entries je JOIN journal_entry_lines l ON l.journal_entry_id = je.id "
        "WHERE je.reversed_entry_id = %s AND je.source_type = 'construction_act_reversal' "
        "GROUP BY je.id",
        (act["journal_entry_id"],),
    )
    rev = db_read.fetchone()
    assert rev is not None, "no reversal entry after cancel"
    assert float(rev["d"]) == float(rev["c"]) == float(amount)

    # Expense line flipped to cancelled; net WIP effect = 0
    db_read.execute(
        "SELECT status FROM construction_expense_lines WHERE id = %s",
        (act["expense_line_id"],),
    )
    assert db_read.fetchone()["status"] == "cancelled"

    # Re-cancel refused (state is 'cancelled' now)
    resp = api_client.put(f"/construction/acts/{act_id}/cancel",
                          json={"rejection_reason": "again"})
    assert resp.status_code == 400


# ─── 8. S5: retention + boshqa ochiq ishlar (migration 467/468) ──────────

def test_sub_act_retention_split(api_client, db_read):
    """Subcontract with retention_pct=5: the act stores retention_amount and
    the JE stays balanced. When the tenant chart resolves a distinct 6990
    account the credit side splits (6010 net + retention line)."""
    amount = 10_000_000
    pid = _make_project(api_client, "S5RET")
    resp = api_client.post(f"/construction/projects/{pid}/subcontracts", json={
        "name": "Sub retention",
        "partner_name": "Retention Sub MChJ",
        "amount": amount,
        "retention_pct": 5,
    })
    assert resp.status_code in (200, 201), resp.text
    sub_id = resp.json()["data"]["id"]

    resp = api_client.post(f"/construction/projects/{pid}/acts", json={
        "act_type": "forma2", "subcontract_id": sub_id,
    })
    assert resp.status_code in (200, 201), resp.text
    act_id = resp.json()["data"]["id"]
    db_read.execute(
        "UPDATE construction_act SET amount_total_with_vat = %s WHERE id = %s",
        (amount, act_id),
    )

    assert api_client.put(f"/construction/acts/{act_id}/approve").status_code == 200

    db_read.execute(
        "SELECT journal_entry_id, retention_amount FROM construction_act WHERE id = %s",
        (act_id,),
    )
    act = db_read.fetchone()
    assert act["journal_entry_id"], "retention act did not post"
    assert float(act["retention_amount"]) == amount * 0.05

    db_read.execute(
        "SELECT a.code, l.debit_amount, l.credit_amount "
        "FROM journal_entry_lines l JOIN accounts a ON a.id = l.account_id "
        "WHERE l.journal_entry_id = %s ORDER BY l.line_number",
        (act["journal_entry_id"],),
    )
    lines = db_read.fetchall()
    assert sum(float(l["debit_amount"]) for l in lines) == \
        sum(float(l["credit_amount"]) for l in lines) == float(amount)
    credit_codes = {l["code"] for l in lines if float(l["credit_amount"]) > 0}
    if len(lines) == 3:
        # Chart resolved a distinct retention account → split happened
        net = next(float(l["credit_amount"]) for l in lines
                   if float(l["credit_amount"]) > 0 and l["code"].startswith("60") and not l["code"].startswith("699"))
        assert net == amount * 0.95, f"6010 must carry the net: {lines}"
        assert any(c.startswith("699") for c in credit_codes), f"no retention line: {lines}"
    else:
        # No distinct account in this chart — full amount stays on 6010
        assert len(lines) == 2, lines

    # Cancel reverses EVERY line (retention split included), balanced
    assert api_client.put(f"/construction/acts/{act_id}/cancel",
                          json={"rejection_reason": "retention storno"}).status_code == 200
    db_read.execute(
        "SELECT COUNT(*) AS n, SUM(l.debit_amount) AS d, SUM(l.credit_amount) AS c "
        "FROM journal_entries je JOIN journal_entry_lines l ON l.journal_entry_id = je.id "
        "WHERE je.reversed_entry_id = %s",
        (act["journal_entry_id"],),
    )
    rev = db_read.fetchone()
    assert rev["n"] == len(lines), "reversal must mirror every original line"
    assert float(rev["d"]) == float(rev["c"]) == float(amount)


def test_manual_progress_write_is_ignored(api_client, db_read):
    """S1: progress_percent is computed from works — a manual PUT write is
    accepted for payload-compat but silently ignored."""
    pid = _make_project(api_client, "S1PROG")
    resp = api_client.put(f"/construction/projects/{pid}", json={"progress_percent": 77})
    assert resp.status_code == 200, resp.text
    db_read.execute("SELECT COALESCE(progress_percent, 0) AS p FROM construction_projects WHERE id = %s", (pid,))
    assert float(db_read.fetchone()["p"]) == 0


def test_resource_consolidation_report_shape(api_client):
    pid = _make_project(api_client, "RESCON")
    resp = api_client.get(f"/construction/projects/{pid}/reports/resource-consolidation")
    assert resp.status_code == 200, resp.text
    data = resp.json()["data"]
    assert "blocks" in data and isinstance(data["blocks"], list)
    assert data["project"]["id"] == pid


def test_dead_tables_archived(db_read):
    """Migration 468: the three zero-referenced 111-era tables are renamed."""
    db_read.execute(
        "SELECT table_name FROM information_schema.tables WHERE table_name IN "
        "('construction_work_progress', 'construction_material_issues', 'construction_vendor_payments')"
    )
    assert db_read.fetchall() == [], "dead tables still live"
    db_read.execute(
        "SELECT COUNT(*) AS n FROM information_schema.tables WHERE table_name LIKE 'archived_construction_%'"
    )
    assert db_read.fetchone()["n"] == 3


def test_construction_events_in_catalog(api_client):
    resp = api_client.get("/workflow-events")
    assert resp.status_code == 200, resp.text
    events = resp.json()["data"]
    values = {e.get("event") or e.get("value") for e in events} if isinstance(events, list) else set(events.keys())
    for ev in ("construction.project_created",
               "construction.project_status_changed",
               "construction.project_commissioned",
               "construction.act_approved",
               "construction.act_signed",
               "construction.material_request_approved",
               "construction.budget_overrun"):
        assert ev in values, f"{ev} missing from workflow catalog"
