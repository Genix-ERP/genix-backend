"""
Genix ERP — Boshqaruv paneli (platform super-admin control plane) SECURITY suite.

This is the permanent attack-simulation regression suite from the admin-panel
audit (docs/admin-panel/audit.md, Phase 1). It proves the boundary between an
ordinary tenant user and the platform control plane, and it is designed to run
in CI forever.

Two markers are in play:
  * plain tests  -> assert the SECURE state; they PASS today (backend already
    gates /admin/* with RequireSystemAdmin).
  * xfail(strict=True) tests -> assert the SECURE state for a hole that is still
    OPEN (SEC-01 tender-admin, SEC-03 audience separation). They "xfail" today.
    The instant Phase 2 closes the hole they XPASS, and strict=True turns an
    unexpected pass into a FAILURE — forcing whoever fixes it to delete the
    marker. That is the intended self-cleaning behaviour: a green security suite
    while a documented hole is open would be worthless.

Run:
    cd genix-backend
    GENIX_API_URL=http://localhost:8080/api/v1 \
        python -m pytest tests/test_35_admin_panel_security.py -v

Needs the API + DB up, exactly like the rest of tests/.
"""
import os
import uuid
import json

import pytest
import requests

BASE_URL = os.getenv("GENIX_API_URL", "http://localhost:8080/api/v1")

# Platform-admin seed login (same account the rest of the suite uses).
PLATFORM_ADMIN_CREDS = [
    {"email": "admin@genixerp.com", "password": "admin123"},
    {"email": "user@genixerp.com", "password": "user123"},
]

TIMEOUT = 15


# ---------------------------------------------------------------------------
# Endpoint maps (kept in sync with docs/admin-panel/audit.md §2)
# ---------------------------------------------------------------------------
# ERP platform-admin endpoints — MUST reject a non-platform token (401/403).
# For write verbs we use a random UUID; the auth middleware runs before the
# handler, so the id never needs to be real to observe the 401/403.
_RID = str(uuid.uuid4())
ADMIN_ENDPOINTS = [
    ("GET", "/admin/users", None),
    ("GET", "/admin/tenants", None),
    ("GET", f"/admin/tenants/{_RID}", None),
    ("PUT", f"/admin/tenants/{_RID}/activate", {"paid_users": 1, "months": 1}),
    ("DELETE", f"/admin/users/{_RID}", None),
    ("POST", "/admin/clean-expired-tenants", {}),
    ("GET", "/admin/mobile-versions", None),
    ("PUT", "/admin/mobile-versions/android", {"min_version": "1.0.0"}),
    ("GET", "/admin/migrations", None),
    ("GET", "/admin/inventory-reconcile", None),
]

# Tender platform-admin endpoints — SEC-01: today gated by Auth only, so a plain
# tenant token reaches them. Target state: rejected (401/403). xfail until fixed.
TENDER_ADMIN_ENDPOINTS = [
    ("GET", "/tender/admin/stats", None),
    ("GET", "/tender/admin/users", None),
    ("PUT", f"/tender/admin/users/{_RID}", {"role": "buyer", "is_verified": True}),
    ("POST", f"/tender/admin/companies/{_RID}/verify", {"verified": True}),
    ("GET", "/tender/admin/tenders", None),
    ("GET", "/tender/admin/reports", None),
    ("POST", "/tender/admin/categories", {"name_uz": "hack", "name_ru": "hack", "name_en": "hack"}),
    ("DELETE", f"/tender/admin/categories/{_RID}", None),
]

FORBIDDEN_SECRET_KEYS = {
    "password_hash", "password", "secret", "api_key", "apikey",
    "access_token", "refresh_token", "jwt_secret", "otp_secret",
    "private_key", "client_secret",
}


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
def _http(method, path, token=None, tenant_id=None, body=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if tenant_id:
        headers["X-Tenant-ID"] = tenant_id
    try:
        return requests.request(
            method, f"{BASE_URL}{path}", headers=headers, json=body, timeout=TIMEOUT
        )
    except requests.exceptions.RequestException as exc:  # server down / network
        pytest.skip(f"API not reachable at {BASE_URL}: {exc}")


def _collect_keys(obj, out):
    """Recursively collect every JSON object key (lower-cased)."""
    if isinstance(obj, dict):
        for k, v in obj.items():
            out.add(str(k).lower())
            _collect_keys(v, out)
    elif isinstance(obj, list):
        for v in obj:
            _collect_keys(v, out)


def _register_throwaway():
    """Register a fresh tenant + owner; returns dict with token/refresh/tenant_id
    or None if registration is unavailable (caller should skip)."""
    suffix = uuid.uuid4().hex[:10]
    resp = _http("POST", "/auth/register", body={
        "tenant_code": f"SEC35{suffix}"[:50],
        "tenant_name": f"SecTest35 {suffix}",
        "email": f"sec35-{suffix}@example.com",
        "password": "SecTest35!pass",
        "first_name": "Sec",
        "last_name": "Test",
    })
    if resp.status_code not in (200, 201):
        return None
    d = resp.json().get("data", resp.json())
    return {
        "token": d.get("access_token") or d.get("token"),
        "refresh": d.get("refresh_token"),
        "tenant_id": (d.get("tenant") or {}).get("id") or d.get("tenant_id"),
    }


def _cleanup_tenant(tenant_id):
    """Best-effort soft-delete of a throwaway tenant + its users."""
    if not tenant_id:
        return
    try:
        import psycopg2
        conn = psycopg2.connect(
            host=os.getenv("DB_HOST", "localhost"), port=os.getenv("DB_PORT", "5432"),
            user=os.getenv("DB_USER", "genix"), password=os.getenv("DB_PASSWORD", "genix_secret"),
            dbname=os.getenv("DB_NAME", "genixerp"))
        conn.autocommit = True
        with conn.cursor() as cur:
            cur.execute("UPDATE users SET deleted_at = NOW() WHERE tenant_id = %s", (tenant_id,))
            cur.execute("UPDATE tenants SET deleted_at = NOW() WHERE id = %s", (tenant_id,))
        conn.close()
    except Exception:
        pass


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------
@pytest.fixture(scope="module")
def tenant_user():
    """A freshly registered tenant + owner (is_system_admin = FALSE).

    Registration is the honest way to obtain a lowest-trust, non-platform token:
    every register path hardcodes is_system_admin=false (SEC-I2). The throwaway
    tenant is soft-deleted in teardown so the suite leaves no residue.
    """
    suffix = uuid.uuid4().hex[:10]
    payload = {
        "tenant_code": f"SEC35{suffix}"[:50],
        "tenant_name": f"SecTest35 {suffix}",
        "email": f"sec35-{suffix}@example.com",
        "password": "SecTest35!pass",
        "first_name": "Sec",
        "last_name": "Test",
    }
    resp = _http("POST", "/auth/register", body=payload)
    if resp.status_code not in (200, 201):
        pytest.skip(f"cannot register throwaway tenant (status {resp.status_code}): {resp.text[:200]}")

    data = resp.json().get("data", resp.json())
    token = data.get("access_token") or data.get("token")
    tenant = data.get("tenant") or {}
    user = data.get("user") or {}
    info = {
        "token": token,
        "tenant_id": tenant.get("id") or data.get("tenant_id"),
        "user_id": user.get("id") or data.get("user_id"),
        "email": payload["email"],
        "is_system_admin": bool(user.get("is_system_admin", False)),
    }
    if not token:
        pytest.skip("registration returned no access token")

    yield info

    # Best-effort cleanup: soft-delete the throwaway tenant + its users.
    try:
        import psycopg2
        conn = psycopg2.connect(
            host=os.getenv("DB_HOST", "localhost"),
            port=os.getenv("DB_PORT", "5432"),
            user=os.getenv("DB_USER", "genix"),
            password=os.getenv("DB_PASSWORD", "genix_secret"),
            dbname=os.getenv("DB_NAME", "genixerp"),
        )
        conn.autocommit = True
        with conn.cursor() as cur:
            if info["tenant_id"]:
                cur.execute(
                    "UPDATE users SET deleted_at = NOW() WHERE tenant_id = %s",
                    (info["tenant_id"],),
                )
                cur.execute(
                    "UPDATE tenants SET deleted_at = NOW() WHERE id = %s",
                    (info["tenant_id"],),
                )
        conn.close()
    except Exception:
        pass  # cleanup is best-effort; never fail the suite on it


@pytest.fixture(scope="module")
def platform_admin_token():
    """Log in the platform super-admin seed account (for read-side assertions)."""
    for cred in PLATFORM_ADMIN_CREDS:
        resp = _http("POST", "/auth/login", body=cred)
        if resp.status_code == 200:
            data = resp.json().get("data", resp.json())
            tok = data.get("access_token") or data.get("token")
            if tok:
                return tok
    pytest.skip("no platform-admin seed login available")


# ---------------------------------------------------------------------------
# 1. ERP admin endpoints reject a non-platform tenant token  (SEC-03/SEC-04)
# ---------------------------------------------------------------------------
class TestAdminEndpointsRejectTenantToken:
    @pytest.mark.parametrize("method,path,body", ADMIN_ENDPOINTS,
                             ids=[f"{m}:{p}" for m, p, _ in ADMIN_ENDPOINTS])
    def test_tenant_token_gets_403(self, tenant_user, method, path, body):
        resp = _http(method, path, token=tenant_user["token"],
                     tenant_id=tenant_user["tenant_id"], body=body)
        assert resp.status_code in (401, 403), (
            f"{method} {path} returned {resp.status_code} to a NON-admin tenant "
            f"token — platform endpoint must be 401/403. Body: {resp.text[:300]}"
        )


# ---------------------------------------------------------------------------
# 2. ERP admin endpoints reject an unauthenticated caller
# ---------------------------------------------------------------------------
class TestAdminEndpointsRejectNoToken:
    @pytest.mark.parametrize("method,path,body", ADMIN_ENDPOINTS,
                             ids=[f"{m}:{p}" for m, p, _ in ADMIN_ENDPOINTS])
    def test_no_token_gets_401(self, method, path, body):
        resp = _http(method, path, token=None, body=body)
        assert resp.status_code in (401, 403), (
            f"{method} {path} returned {resp.status_code} with NO token — must be 401/403."
        )


# ---------------------------------------------------------------------------
# 3. SEC-01 — tender admin plane MUST require authorization (FIXED Phase 2)
# ---------------------------------------------------------------------------
# Gated by middleware.RequireSystemAdmin (tender_routes.go). A plain tenant
# token must now be rejected. Kept as a permanent regression guard so the
# authorization is never commented out again.
class TestTenderAdminRequiresAuthorization:
    @pytest.mark.parametrize("method,path,body", TENDER_ADMIN_ENDPOINTS,
                             ids=[f"{m}:{p}" for m, p, _ in TENDER_ADMIN_ENDPOINTS])
    def test_tenant_token_rejected(self, tenant_user, method, path, body):
        resp = _http(method, path, token=tenant_user["token"],
                     tenant_id=tenant_user["tenant_id"], body=body)
        assert resp.status_code in (401, 403), (
            f"{method} {path} returned {resp.status_code} to an ordinary tenant "
            f"token — the tender admin plane must reject non-admins (SEC-01)."
        )


# ---------------------------------------------------------------------------
# 4. SEC-I2 — a tenant user cannot become is_system_admin via any API path
# ---------------------------------------------------------------------------
class TestPrivilegeEscalationSystemAdminFlag:
    def test_register_does_not_grant_system_admin(self, tenant_user):
        assert tenant_user["is_system_admin"] is False, (
            "A freshly registered tenant owner came back with is_system_admin=true "
            "— registration must never mint a platform admin."
        )

    def test_self_service_profile_update_cannot_set_flag(self, tenant_user):
        # Attempt to smuggle the flag through the self-service profile update.
        for path in ("/users/me", "/auth/me", "/profile"):
            resp = _http("PUT", path, token=tenant_user["token"],
                         tenant_id=tenant_user["tenant_id"],
                         body={"is_system_admin": True, "first_name": "Sec"})
            if resp.status_code == 404:
                continue  # try the next candidate route
            # Whatever the route, it must not have flipped the flag.
            me = _http("GET", "/auth/me", token=tenant_user["token"],
                       tenant_id=tenant_user["tenant_id"])
            if me.status_code == 200:
                data = me.json().get("data", me.json())
                user = data.get("user", data)
                assert user.get("is_system_admin", False) is False, (
                    f"PUT {path} with is_system_admin=true escalated the user to "
                    f"platform admin — mass-assignment hole."
                )
            break


# ---------------------------------------------------------------------------
# 5. IDOR — cross-tenant enumeration is blocked before the id is even resolved
# ---------------------------------------------------------------------------
class TestIDOR:
    def test_cannot_read_another_tenant_by_id(self, tenant_user):
        # Enumerate a foreign tenant id with a tenant token. The platform gate
        # must 401/403 before any object-level check — so IDOR is moot.
        victim = str(uuid.uuid4())
        resp = _http("GET", f"/admin/tenants/{victim}", token=tenant_user["token"],
                     tenant_id=tenant_user["tenant_id"])
        assert resp.status_code in (401, 403), (
            f"Tenant token reached /admin/tenants/:id (status {resp.status_code}) — "
            f"cross-tenant IDOR surface."
        )

    def test_cannot_delete_arbitrary_user(self, tenant_user):
        victim = str(uuid.uuid4())
        resp = _http("DELETE", f"/admin/users/{victim}", token=tenant_user["token"],
                     tenant_id=tenant_user["tenant_id"])
        assert resp.status_code in (401, 403), (
            f"Tenant token reached DELETE /admin/users/:id (status {resp.status_code})."
        )


# ---------------------------------------------------------------------------
# 6. SEC-I3 — admin responses expose no secrets
# ---------------------------------------------------------------------------
class TestSecretsExposure:
    def test_admin_users_no_secrets(self, platform_admin_token):
        resp = _http("GET", "/admin/users", token=platform_admin_token)
        if resp.status_code != 200:
            pytest.skip(f"/admin/users not available to seed admin: {resp.status_code}")
        keys = set()
        _collect_keys(resp.json(), keys)
        leaked = keys & FORBIDDEN_SECRET_KEYS
        assert not leaked, f"/admin/users leaked secret-bearing keys: {leaked}"

    def test_admin_tenants_no_secrets(self, platform_admin_token):
        resp = _http("GET", "/admin/tenants", token=platform_admin_token)
        if resp.status_code != 200:
            pytest.skip(f"/admin/tenants not available to seed admin: {resp.status_code}")
        keys = set()
        _collect_keys(resp.json(), keys)
        leaked = keys & FORBIDDEN_SECRET_KEYS
        assert not leaked, f"/admin/tenants leaked secret-bearing keys: {leaked}"

    def test_tenant_details_no_password_hash(self, platform_admin_token):
        listing = _http("GET", "/admin/tenants", token=platform_admin_token)
        if listing.status_code != 200:
            pytest.skip("cannot list tenants")
        data = listing.json().get("data", [])
        if not data:
            pytest.skip("no tenants to inspect")
        tid = data[0].get("id")
        if not tid:
            pytest.skip("tenant row has no id")
        detail = _http("GET", f"/admin/tenants/{tid}", token=platform_admin_token)
        if detail.status_code != 200:
            pytest.skip(f"tenant details unavailable: {detail.status_code}")
        keys = set()
        _collect_keys(detail.json(), keys)
        assert "password_hash" not in keys, "GetTenantDetails leaked password_hash"


# ---------------------------------------------------------------------------
# 7. SEC-02 — refresh rotation + revocation
# ---------------------------------------------------------------------------
class TestRefreshTokenHardening:
    def test_rotation_revokes_old_refresh_token(self):
        u = _register_throwaway()
        if not u or not u.get("refresh"):
            pytest.skip("registration/refresh unavailable")
        try:
            r1 = _http("POST", "/auth/refresh", body={"refresh_token": u["refresh"]})
            assert r1.status_code == 200, f"first refresh failed: {r1.status_code}"
            # Reusing the now-rotated token must be rejected.
            r2 = _http("POST", "/auth/refresh", body={"refresh_token": u["refresh"]})
            assert r2.status_code in (401, 403), (
                f"a rotated refresh token was accepted again ({r2.status_code}) — "
                f"rotation must revoke the prior token (SEC-02)."
            )
        finally:
            _cleanup_tenant(u["tenant_id"])

    def test_logout_revokes_refresh_token(self):
        u = _register_throwaway()
        if not u or not u.get("refresh") or not u.get("token"):
            pytest.skip("registration unavailable")
        try:
            logout = _http("POST", "/auth/logout", token=u["token"])
            if logout.status_code not in (200, 204):
                pytest.skip(f"logout unavailable: {logout.status_code}")
            r = _http("POST", "/auth/refresh", body={"refresh_token": u["refresh"]})
            assert r.status_code in (401, 403), (
                f"refresh succeeded after logout ({r.status_code}) — logout must "
                f"revoke the refresh token (SEC-02)."
            )
        finally:
            _cleanup_tenant(u["tenant_id"])


# ---------------------------------------------------------------------------
# 8. SEC-05 — platform audit log
# ---------------------------------------------------------------------------
class TestPlatformAuditLog:
    def test_audit_log_requires_platform_admin(self, tenant_user):
        resp = _http("GET", "/admin/audit-log", token=tenant_user["token"],
                     tenant_id=tenant_user["tenant_id"])
        assert resp.status_code in (401, 403), (
            f"/admin/audit-log reachable by a tenant token ({resp.status_code})."
        )

    def test_admin_mutation_writes_audit_row(self, platform_admin_token):
        u = _register_throwaway()
        if not u:
            pytest.skip("registration unavailable")
        tid = u["tenant_id"]
        try:
            act = _http("PUT", f"/admin/tenants/{tid}/activate",
                        token=platform_admin_token, body={"paid_users": 1, "months": 1})
            if act.status_code != 200:
                pytest.skip(f"activate unavailable: {act.status_code}")
            logs = _http("GET", "/admin/audit-log?action=tenant.activate",
                         token=platform_admin_token)
            assert logs.status_code == 200, f"audit-log read failed: {logs.status_code}"
            rows = logs.json().get("data") or []
            assert any(r.get("target_id") == tid for r in rows), (
                "the tenant.activate mutation did not write a platform_audit_log row (SEC-05)."
            )
        finally:
            _cleanup_tenant(tid)
