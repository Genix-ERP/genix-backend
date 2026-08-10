-- SEC-05 (docs/admin-panel/audit.md): append-only audit trail for platform
-- super-admin actions and (future) impersonation. Distinct from the tenant-scoped
-- `audit_logs` table, which only records tenant business-entity changes.
--
-- Every RequireSystemAdmin-gated mutation writes one row here: who (actor),
-- what (action), which tenant/target, before/after values, IP, timestamp.
-- The table is immutable — a trigger blocks UPDATE/DELETE — so the trail is
-- tamper-evident.

CREATE TABLE IF NOT EXISTS platform_audit_log (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    actor_user_id    UUID,                        -- no FK cascade: keep the record even if the actor is later removed
    actor_email      VARCHAR(255),
    action           VARCHAR(100) NOT NULL,       -- e.g. tenant.activate, user.delete, tender.company.verify
    target_type      VARCHAR(50),                 -- tenant | user | company | mobile_version | category
    target_id        VARCHAR(100),                -- uuid or platform-scoped string (e.g. "android")
    target_tenant_id UUID,                         -- tenant the action touched, when applicable
    before_values    JSONB,
    after_values     JSONB,
    ip_address       INET,
    user_agent       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_platform_audit_actor   ON platform_audit_log(actor_user_id);
CREATE INDEX IF NOT EXISTS idx_platform_audit_action  ON platform_audit_log(action);
CREATE INDEX IF NOT EXISTS idx_platform_audit_target  ON platform_audit_log(target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_platform_audit_tenant  ON platform_audit_log(target_tenant_id);
CREATE INDEX IF NOT EXISTS idx_platform_audit_created ON platform_audit_log(created_at DESC);

-- Immutability: append-only. Block UPDATE and DELETE at the DB level.
CREATE OR REPLACE FUNCTION platform_audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'platform_audit_log is append-only (% not allowed)', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_platform_audit_no_mutate ON platform_audit_log;
CREATE TRIGGER trg_platform_audit_no_mutate
    BEFORE UPDATE OR DELETE ON platform_audit_log
    FOR EACH ROW EXECUTE FUNCTION platform_audit_log_immutable();
