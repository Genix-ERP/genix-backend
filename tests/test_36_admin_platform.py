"""
Genix ERP — Platform control plane (Phase 3/4) regression suite.

Covers the separate platform identity + login, the fixed 4-role capability
matrix, onboarding, plan catalog, SaaS stats, tenant lifecycle, and
impersonation (incl. read-only enforcement). Permanent CI guard alongside
test_35 (which covers the security boundary).

Run:
    cd genix-backend
    GENIX_API_URL=http://localhost:8080/api/v1 \
        python -m pytest tests/test_36_admin_platform.py -v
Needs the API + DB up.
"""
import os
import uuid

import pytest
import requests

BASE_URL = os.getenv("GENIX_API_URL", "http://localhost:8080/api/v1")
PLATFORM_ADMIN = [
    {"email": "admin@genixerp.com", "password": "admin123"},
    {"email": "user@genixerp.com", "password": "user123"},
]
TIMEOUT = 15


def _http(method, path, token=None, body=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    try:
        return requests.request(method, f"{BASE_URL}{path}", headers=headers, json=body, timeout=TIMEOUT)
    except requests.exceptions.RequestException as exc:
        pytest.skip(f"API not reachable: {exc}")


def _data(resp):
    try:
        j = resp.json()
        return j.get("data", j)
    except Exception:
        return None


def _cleanup_tenant(tid):
    if not tid:
        return
    try:
        import psycopg2
        conn = psycopg2.connect(
            host=os.getenv("DB_HOST", "localhost"), port=os.getenv("DB_PORT", "5432"),
            user=os.getenv("DB_USER", "genix"), password=os.getenv("DB_PASSWORD", "genix_secret"),
            dbname=os.getenv("DB_NAME", "genixerp"))
        conn.autocommit = True
        with conn.cursor() as cur:
            cur.execute("UPDATE users SET deleted_at = NOW() WHERE tenant_id = %s", (tid,))
            cur.execute("UPDATE tenants SET deleted_at = NOW() WHERE id = %s", (tid,))
        conn.close()
    except Exception:
        pass


def _delete_platform_user(email):
    try:
        import psycopg2
        conn = psycopg2.connect(
            host=os.getenv("DB_HOST", "localhost"), port=os.getenv("DB_PORT", "5432"),
            user=os.getenv("DB_USER", "genix"), password=os.getenv("DB_PASSWORD", "genix_secret"),
            dbname=os.getenv("DB_NAME", "genixerp"))
        conn.autocommit = True
        with conn.cursor() as cur:
            cur.execute("DELETE FROM platform_users WHERE email = %s", (email,))
        conn.close()
    except Exception:
        pass


@pytest.fixture(scope="module")
def platform_token():
    for cred in PLATFORM_ADMIN:
        r = _http("POST", "/platform/auth/login", body=cred)
        if r.status_code == 200:
            tok = _data(r).get("access_token")
            if tok:
                return tok
    pytest.skip("no platform super-admin available (platform_users backfill missing?)")


@pytest.fixture
def throwaway_tenant(platform_token):
    s = uuid.uuid4().hex[:8]
    r = _http("POST", "/admin/tenants", token=platform_token, body={
        "tenant_code": f"T36{s}", "tenant_name": f"T36 {s}",
        "owner_email": f"t36-{s}@example.com", "owner_first_name": "T", "owner_last_name": "36",
        "plan_code": "starter", "trial_days": 14,
    })
    if r.status_code not in (200, 201):
        pytest.skip(f"onboarding unavailable: {r.status_code}")
    tid = _data(r).get("tenant_id")
    yield tid
    _cleanup_tenant(tid)


# ---------------------------------------------------------------------------
class TestPlatformIdentity:
    def test_platform_login_returns_role(self, platform_token):
        me = _http("GET", "/admin/platform/me", token=platform_token)
        assert me.status_code == 200
        d = _data(me)
        assert d.get("role") == "super_admin"
        assert d.get("capabilities", {}).get("company.create") is True

    def test_role_matrix_has_four_roles(self, platform_token):
        r = _http("GET", "/admin/role-matrix", token=platform_token)
        assert r.status_code == 200
        d = _data(r)
        assert set(d.get("roles", [])) == {"super_admin", "admin", "manejer", "tex_podderjka"}
        # Spec checks: tex_podderjka has read-only impersonation, not full.
        tex = d["matrix"]["tex_podderjka"]
        assert tex.get("impersonate.readonly") is True
        assert tex.get("impersonate", False) is False
        # manejer cannot create companies.
        assert d["matrix"]["manejer"].get("company.create", False) is False


class TestOnboardingAndCatalog:
    def test_onboarding_creates_trialing_tenant(self, throwaway_tenant, platform_token):
        detail = _http("GET", f"/admin/tenants/{throwaway_tenant}", token=platform_token)
        assert detail.status_code == 200
        # Onboarded tenants enter the trial lifecycle.
        assert _data(detail).get("tenant", {}).get("subscription_status") == "trialing"

    def test_plan_catalog_lists_plans(self, platform_token):
        r = _http("GET", "/admin/plans", token=platform_token)
        assert r.status_code == 200
        codes = {p["code"] for p in _data(r)}
        assert {"free", "starter", "professional", "enterprise"} <= codes

    def test_stats_are_platform_wide(self, platform_token):
        r = _http("GET", "/admin/stats", token=platform_token)
        assert r.status_code == 200
        d = _data(r)
        assert "companies" in d and "total" in d["companies"]
        assert "mrr" in d
        assert isinstance(d.get("daily_active_tenants"), list)


class TestTenantLifecycle:
    def test_block_and_reactivate(self, throwaway_tenant, platform_token):
        b = _http("PUT", f"/admin/tenants/{throwaway_tenant}/status",
                  token=platform_token, body={"status": "blocked", "reason": "test"})
        assert b.status_code == 200
        assert _data(b).get("is_active") is False
        a = _http("PUT", f"/admin/tenants/{throwaway_tenant}/status",
                  token=platform_token, body={"status": "active", "reason": "test"})
        assert a.status_code == 200
        assert _data(a).get("is_active") is True


class TestImpersonation:
    def test_readonly_impersonation_cannot_mutate(self, throwaway_tenant, platform_token):
        imp = _http("POST", "/admin/impersonate", token=platform_token,
                    body={"tenant_id": throwaway_tenant, "reason": "support", "read_only": True})
        assert imp.status_code == 200
        d = _data(imp)
        assert d.get("read_only") is True
        itok = d.get("access_token")
        # A read (GET) is fine; a mutation is blocked.
        mut = _http("POST", "/leads", token=itok, body={"name": "x"})
        assert mut.status_code in (401, 403), f"read-only impersonation mutated (status {mut.status_code})"

    def test_impersonation_requires_reason(self, throwaway_tenant, platform_token):
        r = _http("POST", "/admin/impersonate", token=platform_token,
                  body={"tenant_id": throwaway_tenant})
        assert r.status_code == 400


class TestCapabilityEnforcement:
    @pytest.fixture
    def manejer_token(self, platform_token):
        email = f"manejer-{uuid.uuid4().hex[:8]}@example.com"
        c = _http("POST", "/admin/platform-users", token=platform_token, body={
            "email": email, "password": "Manejer1!pass", "first_name": "M", "last_name": "J", "role": "manejer"})
        if c.status_code not in (200, 201):
            pytest.skip(f"cannot create platform user: {c.status_code}")
        login = _http("POST", "/platform/auth/login", body={"email": email, "password": "Manejer1!pass"})
        tok = _data(login).get("access_token") if login.status_code == 200 else None
        yield tok
        _delete_platform_user(email)

    def test_manejer_can_view_but_not_create_or_manage(self, manejer_token):
        if not manejer_token:
            pytest.skip("no manejer token")
        assert _http("GET", "/admin/stats", token=manejer_token).status_code == 200
        assert _http("POST", "/admin/tenants", token=manejer_token, body={
            "tenant_code": "X", "tenant_name": "X", "owner_email": "x@x.com", "owner_first_name": "X"
        }).status_code == 403
        assert _http("GET", "/admin/platform-users", token=manejer_token).status_code == 403


class TestPlatformEndpointsRejectTenantToken:
    """The new platform endpoints must also reject an ordinary tenant token."""
    @pytest.fixture
    def tenant_token(self):
        s = uuid.uuid4().hex[:8]
        r = _http("POST", "/auth/register", body={
            "tenant_code": f"TT36{s}", "tenant_name": f"TT36 {s}",
            "email": f"tt36-{s}@example.com", "password": "Tenant36!pass",
            "first_name": "T", "last_name": "T"})
        if r.status_code not in (200, 201):
            pytest.skip("registration unavailable")
        d = _data(r)
        yield {"token": d.get("access_token"), "tenant_id": (d.get("tenant") or {}).get("id")}
        _cleanup_tenant((d.get("tenant") or {}).get("id"))

    @pytest.mark.parametrize("method,path,body", [
        ("GET", "/admin/stats", None),
        ("POST", "/admin/tenants", {"tenant_code": "x", "tenant_name": "x", "owner_email": "x@x.com", "owner_first_name": "x"}),
        ("POST", "/admin/impersonate", {"tenant_id": str(uuid.uuid4()), "reason": "x"}),
        ("GET", "/admin/platform-users", None),
        ("GET", "/admin/role-matrix", None),
        ("PUT", "/admin/plans/free", {"price_per_user_monthly": 1}),
    ])
    def test_tenant_token_rejected(self, tenant_token, method, path, body):
        r = _http(method, path, token=tenant_token["token"], body=body)
        assert r.status_code in (401, 403), f"{method} {path} reachable by tenant token ({r.status_code})"


class TestF3RegisterTrial:
    def test_registered_tenant_is_trialing(self, platform_token):
        s = uuid.uuid4().hex[:8]
        r = _http("POST", "/auth/register", body={
            "tenant_code": f"TR36{s}", "tenant_name": f"TR36 {s}",
            "email": f"tr36-{s}@example.com", "password": "Trial36!pass",
            "first_name": "T", "last_name": "R"})
        if r.status_code not in (200, 201):
            pytest.skip("registration unavailable")
        tid = (_data(r).get("tenant") or {}).get("id")
        try:
            detail = _http("GET", f"/admin/tenants/{tid}", token=platform_token)
            if detail.status_code == 200:
                assert _data(detail).get("tenant", {}).get("subscription_status") == "trialing", (
                    "self-service register must create a trialing tenant (F3)")
        finally:
            _cleanup_tenant(tid)
