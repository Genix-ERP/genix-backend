"""
Genix ERP - Vazifalar (task management) moduli testlari

Qamrov (spec 5-bosqich talablari):
  1. Tenant izolyatsiyasi - boshqa tenant doska/vazifalarni ko'ra olmaydi va o'zgartira olmaydi
  2. Ustun CRUD uchun server tomonda permission tekshiruvi
  3. Drag & drop pozitsiya butunligi (har ustunda 0..n-1, takrorsiz)
  4. Bajarildi-ustun semantikasi: kirganda completed_at qo'yiladi, chiqqanda tozalanadi
"""
import uuid

import pytest
import requests

from conftest import BASE_URL, APIClient


# ============================================
# FIXTURES
# ============================================

@pytest.fixture(scope="module")
def board(api_client):
    """Fresh board with the 4 default columns; deleted afterwards."""
    resp = api_client.post("/task-boards", json={"name": f"Test doska {uuid.uuid4().hex[:6]}"})
    assert resp.status_code in (200, 201), resp.text
    data = resp.json()["data"]
    assert data["board"]["id"]
    yield data
    api_client.delete(f"/task-boards/{data['board']['id']}")


def _columns(api_client, board_id):
    resp = api_client.get(f"/task-boards/{board_id}")
    assert resp.status_code == 200, resp.text
    return resp.json()["data"]["columns"]


def _tasks(api_client, board_id):
    resp = api_client.get(f"/task-boards/{board_id}")
    assert resp.status_code == 200, resp.text
    return resp.json()["data"]["tasks"]


def _create_task(api_client, board_id, title, column_id=None):
    payload = {"title": title}
    if column_id:
        payload["column_id"] = column_id
    resp = api_client.post(f"/task-boards/{board_id}/tasks", json=payload)
    assert resp.status_code in (200, 201), resp.text
    return resp.json()["data"]["task"]


# ============================================
# 0. Doska va standart ustunlar
# ============================================

class TestBoardBasics:
    def test_board_has_four_default_columns(self, board):
        cols = board["columns"]
        assert [c["name"] for c in cols] == ["Yangi", "Jarayonda", "Tekshiruvda", "Bajarildi"]
        assert [c["position"] for c in cols] == [0, 1, 2, 3]
        assert [c["is_done_column"] for c in cols] == [False, False, False, True]

    def test_stats_endpoint(self, api_client):
        resp = api_client.get("/task-boards/stats")
        assert resp.status_code == 200, resp.text
        stats = resp.json()["data"]
        for key in ("total_tasks", "active_tasks", "overdue_tasks", "completed_pct_month"):
            assert key in stats

    def test_invalid_priority_rejected(self, api_client, board):
        board_id = board["board"]["id"]
        resp = api_client.post(f"/task-boards/{board_id}/tasks",
                               json={"title": "X", "priority": "banana"})
        assert resp.status_code == 400

    def test_my_tasks_endpoint(self, api_client):
        resp = api_client.get("/my-tasks")
        assert resp.status_code == 200, resp.text
        assert isinstance(resp.json()["data"], list)


# ============================================
# 1. TENANT IZOLYATSIYASI
# ============================================

class TestTenantIsolation:
    """A spoofed X-Tenant-ID must never see or mutate another tenant's data."""

    @pytest.fixture(scope="class")
    def foreign_client(self, auth_token):
        # Random (non-member) tenant id in the header — the resolver accepts any
        # UUID, so every handler must scope by tenant itself.
        return APIClient(
            base_url=BASE_URL,
            token=auth_token["token"],
            tenant_id=str(uuid.uuid4()),
        )

    def test_foreign_tenant_cannot_read_board(self, board, foreign_client):
        board_id = board["board"]["id"]
        resp = foreign_client.get(f"/task-boards/{board_id}")
        assert resp.status_code in (403, 404), resp.text

    def test_foreign_tenant_cannot_update_board(self, board, foreign_client):
        board_id = board["board"]["id"]
        resp = foreign_client.put(f"/task-boards/{board_id}", json={"name": "hacked"})
        assert resp.status_code in (403, 404), resp.text

    def test_foreign_tenant_cannot_create_task_under_board(self, board, foreign_client):
        board_id = board["board"]["id"]
        resp = foreign_client.post(f"/task-boards/{board_id}/tasks", json={"title": "sneak"})
        assert resp.status_code in (403, 404), resp.text

    def test_foreign_tenant_cannot_create_column(self, board, foreign_client):
        board_id = board["board"]["id"]
        resp = foreign_client.post(f"/task-boards/{board_id}/columns", json={"name": "sneak"})
        assert resp.status_code in (403, 404), resp.text

    def test_foreign_tenant_cannot_move_task(self, api_client, board, foreign_client):
        board_id = board["board"]["id"]
        cols = board["columns"]
        task = _create_task(api_client, board_id, "Izolyatsiya taski", cols[0]["id"])
        resp = foreign_client.post(
            f"/task-boards/{board_id}/tasks/{task['id']}/move",
            json={"column_id": cols[1]["id"], "position": 0},
        )
        assert resp.status_code in (403, 404), resp.text

    def test_board_list_does_not_leak(self, board, foreign_client):
        resp = foreign_client.get("/task-boards")
        if resp.status_code == 200:
            ids = [b["id"] for b in resp.json()["data"]]
            assert board["board"]["id"] not in ids
        else:
            assert resp.status_code in (401, 403)


# ============================================
# 2. USTUN CRUD PERMISSION TEKSHIRUVI
# ============================================

class TestColumnPermissions:
    def test_unauthenticated_column_create_rejected(self, board):
        board_id = board["board"]["id"]
        resp = requests.post(
            f"{BASE_URL}/task-boards/{board_id}/columns",
            json={"name": "anon"},
            timeout=10,
        )
        assert resp.status_code == 401

    def test_unauthenticated_column_delete_rejected(self, board):
        board_id = board["board"]["id"]
        col_id = board["columns"][0]["id"]
        resp = requests.delete(
            f"{BASE_URL}/task-boards/{board_id}/columns/{col_id}", timeout=10
        )
        assert resp.status_code == 401

    def test_limited_user_cannot_manage_columns(self, api_client, board, db_read, auth_token):
        """A non-privileged tenant user without tasks:column:* gets 403.

        Uses the seeded non-admin credential if present; skipped otherwise.
        """
        resp = requests.post(f"{BASE_URL}/auth/login",
                             json={"email": "user@genixerp.com", "password": "user123"},
                             timeout=10)
        if resp.status_code != 200:
            pytest.skip("No seeded non-admin user available")
        data = resp.json().get("data", resp.json())
        user = data.get("user", {})
        if user.get("role") in ("owner", "site_admin"):
            pytest.skip("Seeded second user is privileged — cannot test 403 path")
        limited = APIClient(
            base_url=BASE_URL,
            token=data.get("access_token") or data.get("token"),
            tenant_id=auth_token["tenant_id"],
        )
        board_id = board["board"]["id"]
        resp = limited.post(f"/task-boards/{board_id}/columns", json={"name": "no-perm"})
        assert resp.status_code == 403, resp.text

    def test_column_lifecycle_with_manage_permission(self, api_client, board):
        board_id = board["board"]["id"]
        # create
        resp = api_client.post(f"/task-boards/{board_id}/columns",
                               json={"name": "Qo'shimcha", "color": "purple", "wip_limit": 3})
        assert resp.status_code in (200, 201), resp.text
        cols = resp.json()["data"]["columns"]
        new_col = next(c for c in cols if c["name"] == "Qo'shimcha")
        assert new_col["position"] == len(cols) - 1
        assert new_col["wip_limit"] == 3
        # rename + remove wip
        resp = api_client.put(
            f"/task-boards/{board_id}/columns/{new_col['id']}",
            json={"name": "Qayta nomlangan", "remove_wip_limit": True},
        )
        assert resp.status_code == 200, resp.text
        renamed = next(c for c in resp.json()["data"]["columns"] if c["id"] == new_col["id"])
        assert renamed["name"] == "Qayta nomlangan"
        assert renamed.get("wip_limit") is None
        # reorder: move it to the front
        ids = [c["id"] for c in resp.json()["data"]["columns"]]
        ids.remove(new_col["id"])
        ids.insert(0, new_col["id"])
        resp = api_client.put(f"/task-boards/{board_id}/columns/reorder", json={"column_ids": ids})
        assert resp.status_code == 200, resp.text
        assert [c["id"] for c in resp.json()["data"]["columns"]] == ids
        # empty column deletes without move_to
        resp = api_client.delete(f"/task-boards/{board_id}/columns/{new_col['id']}")
        assert resp.status_code == 200, resp.text

    def test_delete_column_with_tasks_requires_destination(self, api_client, board):
        board_id = board["board"]["id"]
        cols = _columns(api_client, board_id)
        source = next(c for c in cols if c["name"] == "Tekshiruvda")
        target = next(c for c in cols if c["name"] == "Yangi")
        _create_task(api_client, board_id, "Ko'chiriladigan task", source["id"])
        # no destination → 409
        resp = api_client.delete(f"/task-boards/{board_id}/columns/{source['id']}")
        assert resp.status_code == 409, resp.text
        # with destination → moved and deleted
        resp = api_client.delete(
            f"/task-boards/{board_id}/columns/{source['id']}?move_to={target['id']}"
        )
        assert resp.status_code == 200, resp.text
        remaining = resp.json()["data"]["columns"]
        assert source["id"] not in [c["id"] for c in remaining]
        tasks = _tasks(api_client, board_id)
        assert any(t["title"] == "Ko'chiriladigan task" and t["column_id"] == target["id"]
                   for t in tasks)


# ============================================
# 3. DRAG & DROP POZITSIYA BUTUNLIGI
# ============================================

class TestPositionIntegrity:
    def _assert_dense_positions(self, tasks, column_id):
        positions = sorted(t["position"] for t in tasks if t["column_id"] == column_id)
        assert positions == list(range(len(positions))), positions

    def test_move_between_columns_keeps_positions_dense(self, api_client, board):
        board_id = board["board"]["id"]
        cols = _columns(api_client, board_id)
        col_a = next(c for c in cols if c["name"] == "Yangi")
        col_b = next(c for c in cols if c["name"] == "Jarayonda")

        t1 = _create_task(api_client, board_id, "P1", col_a["id"])
        t2 = _create_task(api_client, board_id, "P2", col_a["id"])
        t3 = _create_task(api_client, board_id, "P3", col_a["id"])

        # move the middle task to the top of column B
        resp = api_client.post(f"/task-boards/{board_id}/tasks/{t2['id']}/move",
                               json={"column_id": col_b["id"], "position": 0})
        assert resp.status_code == 200, resp.text

        tasks = _tasks(api_client, board_id)
        self._assert_dense_positions(tasks, col_a["id"])
        self._assert_dense_positions(tasks, col_b["id"])
        moved = next(t for t in tasks if t["id"] == t2["id"])
        assert moved["column_id"] == col_b["id"]
        assert moved["position"] == 0

        # reorder within column A: t3 above t1
        resp = api_client.post(f"/task-boards/{board_id}/tasks/{t3['id']}/move",
                               json={"column_id": col_a["id"], "position": 0})
        assert resp.status_code == 200, resp.text
        tasks = _tasks(api_client, board_id)
        self._assert_dense_positions(tasks, col_a["id"])
        order = [t["id"] for t in sorted(
            (t for t in tasks if t["column_id"] == col_a["id"]),
            key=lambda t: t["position"])]
        assert order.index(t3["id"]) < order.index(t1["id"])

    def test_out_of_range_position_is_clamped(self, api_client, board):
        board_id = board["board"]["id"]
        cols = _columns(api_client, board_id)
        col_b = next(c for c in cols if c["name"] == "Jarayonda")
        t = _create_task(api_client, board_id, "Clamp", col_b["id"])
        resp = api_client.post(f"/task-boards/{board_id}/tasks/{t['id']}/move",
                               json={"column_id": col_b["id"], "position": 999})
        assert resp.status_code == 200, resp.text
        tasks = _tasks(api_client, board_id)
        self._assert_dense_positions(tasks, col_b["id"])

    def test_move_to_foreign_column_rejected(self, api_client, board):
        board_id = board["board"]["id"]
        cols = _columns(api_client, board_id)
        t = _create_task(api_client, board_id, "Foreign col", cols[0]["id"])
        resp = api_client.post(f"/task-boards/{board_id}/tasks/{t['id']}/move",
                               json={"column_id": str(uuid.uuid4()), "position": 0})
        assert resp.status_code == 400, resp.text


# ============================================
# 4. BAJARILDI-USTUN SEMANTIKASI
# ============================================

class TestDoneColumnSemantics:
    def test_completed_at_set_and_cleared_by_moves(self, api_client, board):
        board_id = board["board"]["id"]
        cols = _columns(api_client, board_id)
        yangi = next(c for c in cols if c["name"] == "Yangi")
        done = next(c for c in cols if c["is_done_column"])

        t = _create_task(api_client, board_id, "Done semantikasi", yangi["id"])
        assert t.get("completed_at") is None

        # into the done column → stamped
        resp = api_client.post(f"/task-boards/{board_id}/tasks/{t['id']}/move",
                               json={"column_id": done["id"], "position": 0})
        assert resp.status_code == 200, resp.text
        moved = resp.json()["data"]["task"]
        assert moved["completed_at"] is not None

        # back out → cleared
        resp = api_client.post(f"/task-boards/{board_id}/tasks/{t['id']}/move",
                               json={"column_id": yangi["id"], "position": 0})
        assert resp.status_code == 200, resp.text
        reopened = resp.json()["data"]["task"]
        assert reopened.get("completed_at") is None

    def test_task_created_directly_in_done_column_is_completed(self, api_client, board):
        board_id = board["board"]["id"]
        cols = _columns(api_client, board_id)
        done = next(c for c in cols if c["is_done_column"])
        t = _create_task(api_client, board_id, "To'g'ridan done", done["id"])
        assert t["completed_at"] is not None
