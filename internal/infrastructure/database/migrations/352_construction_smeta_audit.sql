-- 352_construction_smeta_audit.sql
--
-- Audit log for the Smeta boshqaruvi → Jurnal tab.
--
-- Every meaningful mutation on an estimate (qty change, price change,
-- material-type change, sub-stage add/delete, prochie %, VAT toggle,
-- form save) writes one row here so foremen can answer "who changed
-- what, when?" without trawling through git/db logs.
--
-- The schema mirrors the v23 mockup's `auditLog` array but persists it
-- per-tenant per-estimate.
--
-- Action enum values:
--   qty_change     — work or sub-stage quantity edited
--   price_change   — resource unit_rate edited (per resource bucket)
--   mat_type       — material type bucket changed (standard/cable/etc)
--   subwork_add    — Yangi etap created
--   subwork_del    — Sub-stage deleted
--   res_add        — Resurs qo'shish (new resource sub-line)
--   res_del        — Resource sub-line deleted
--   reset_qty      — per-line qty reset to original
--   reset_qty_all  — bulk-zero estimate qtys
--   reset_price    — per-resource price reset to original
--   reset_price_all — project-wide price reset
--   form_save      — Forma 2 snapshot saved
--   form_delete    — Forma 2 snapshot deleted
--   other_pct      — прочие % changed (tracked at snapshot save)
--   use_vat        — VAT toggle changed (tracked at snapshot save)

CREATE TABLE IF NOT EXISTS construction_smeta_audit (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    UUID NOT NULL,
    project_id   BIGINT NOT NULL REFERENCES construction_projects(id) ON DELETE CASCADE,
    estimate_id  BIGINT REFERENCES construction_estimate(id) ON DELETE CASCADE,

    action       VARCHAR(40) NOT NULL,
    target       TEXT,            -- human-readable target name (resource name, work code, etc.)
    line_id      BIGINT,          -- optional reference to construction_estimate_line.id

    from_value   TEXT,            -- old value as text (numeric, string, JSON — caller's choice)
    to_value     TEXT,            -- new value as text
    description  TEXT,            -- free-form note

    user_id      UUID,
    user_name    TEXT,            -- denormalised so deleted users still show
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_construction_smeta_audit_estimate
    ON construction_smeta_audit (estimate_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_construction_smeta_audit_project
    ON construction_smeta_audit (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_construction_smeta_audit_action
    ON construction_smeta_audit (action);
CREATE INDEX IF NOT EXISTS idx_construction_smeta_audit_tenant
    ON construction_smeta_audit (tenant_id);

COMMENT ON TABLE construction_smeta_audit IS
    'Per-estimate audit log of every mutation done from the Smeta boshqaruvi tab.';
