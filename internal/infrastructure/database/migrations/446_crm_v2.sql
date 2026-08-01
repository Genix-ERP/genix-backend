-- 446_crm_v2.sql — CRM rebuild (see docs/crm-audit.md)
--
-- Model decision: single-entity pipeline. `leads` IS the deal (bitim): it
-- carries amount+currency, a configurable stage, a responsible employee and,
-- once won, a link to the unified partner table (`contacts`). The
-- `opportunities` table is left frozen for `contract_links.crm_deal`
-- back-compat; nothing is dropped.
--
--   1. pipelines (several funnels per org; existing lead stages → default one)
--   2. pipeline_stages.pipeline_id + semantic fix (qualified was seeded
--      is_won=TRUE; it becomes an open "Negotiation" stage and every lead
--      pipeline gets a real terminal Won stage)
--   3. lost_reasons (tenant-configurable, required on loss)
--   4. leads: currency, stage/pipeline FKs, responsible employee, partner,
--      lost reason, won_at/lost_at, last_activity_at + carry-over
--   5. lead_stage_history (funnel conversion / cycle time source of truth)
--   6. call_logs.lead_id (calls attach to unconverted leads)
--   7. contract_links CHECK gains 'crm_lead'
--   8. crm permissions seeded (routes are gated from this release)
--   9. indexes incl. normalized-phone dedupe lookups

-- ── 1. pipelines ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS pipelines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT false,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- one default pipeline per (tenant, org)
CREATE UNIQUE INDEX IF NOT EXISTS idx_pipelines_one_default
    ON pipelines (tenant_id, COALESCE(organization_id, '00000000-0000-0000-0000-000000000000'))
    WHERE is_default;
CREATE INDEX IF NOT EXISTS idx_pipelines_tenant ON pipelines(tenant_id, organization_id);

-- Seed a default pipeline for every org that has lead stages (288 seeded all
-- orgs) plus every active org, deduped by UNION.
INSERT INTO pipelines (tenant_id, organization_id, name, is_default)
SELECT DISTINCT ps.tenant_id, ps.organization_id, 'Savdo voronkasi', true
FROM pipeline_stages ps WHERE ps.pipeline_type = 'lead'
UNION
SELECT o.tenant_id, o.id, 'Savdo voronkasi', true
FROM organizations o WHERE o.is_active = true;

-- ── 2. Attach lead stages + fix terminal semantics ───────────────────────
ALTER TABLE pipeline_stages ADD COLUMN IF NOT EXISTS pipeline_id UUID REFERENCES pipelines(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_pipeline_stages_pipeline ON pipeline_stages(pipeline_id, sequence);

UPDATE pipeline_stages ps SET pipeline_id = p.id
FROM pipelines p
WHERE ps.pipeline_type = 'lead' AND ps.pipeline_id IS NULL
  AND p.tenant_id = ps.tenant_id AND p.is_default
  AND p.organization_id IS NOT DISTINCT FROM ps.organization_id;

-- 287/288 seeded "Qualified" with is_won=TRUE — a hidden pseudo-win that made
-- conversion uncomputable. It becomes an open negotiation stage; the default
-- rename only applies where the name is still the seeded literal.
UPDATE pipeline_stages SET name = 'Negotiation', probability = 60
WHERE pipeline_type = 'lead' AND code = 'qualified' AND name = 'Qualified';
UPDATE pipeline_stages SET is_won = false
WHERE pipeline_type = 'lead' AND code = 'qualified' AND is_won = true;

-- Real Won stage per lead pipeline that lacks one
INSERT INTO pipeline_stages (id, tenant_id, name, code, sequence, probability, is_won, is_lost, color, is_active, pipeline_type, organization_id, pipeline_id, created_at, updated_at)
SELECT uuid_generate_v4(), p.tenant_id, 'Won', 'won',
       COALESCE((SELECT MAX(s.sequence) FROM pipeline_stages s WHERE s.pipeline_id = p.id), 0) + 1,
       100, true, false, 'emerald', true, 'lead', p.organization_id, p.id, NOW(), NOW()
FROM pipelines p
WHERE NOT EXISTS (SELECT 1 FROM pipeline_stages s WHERE s.pipeline_id = p.id AND s.is_won)
ON CONFLICT DO NOTHING;

-- Orgs that never had lead stages (seeded after 288) now have a pipeline with
-- only the Won stage — give every pipeline the full open-stage set and a Lost
-- stage so the board is usable.
INSERT INTO pipeline_stages (id, tenant_id, name, code, sequence, probability, is_won, is_lost, color, is_active, pipeline_type, organization_id, pipeline_id, created_at, updated_at)
SELECT uuid_generate_v4(), p.tenant_id, s.name, s.code, s.seq, s.prob, false, false, s.color, true, 'lead', p.organization_id, p.id, NOW(), NOW()
FROM pipelines p
CROSS JOIN (VALUES
    ('New',         'new',         0, 10.0, 'blue'),
    ('Contacted',   'contacted',   1, 30.0, 'amber'),
    ('In Progress', 'in_progress', 2, 50.0, 'purple'),
    ('Negotiation', 'qualified',   3, 60.0, 'green')
) AS s(name, code, seq, prob, color)
WHERE NOT EXISTS (SELECT 1 FROM pipeline_stages ps WHERE ps.pipeline_id = p.id AND NOT ps.is_won AND NOT ps.is_lost)
ON CONFLICT DO NOTHING;

INSERT INTO pipeline_stages (id, tenant_id, name, code, sequence, probability, is_won, is_lost, color, is_active, pipeline_type, organization_id, pipeline_id, created_at, updated_at)
SELECT uuid_generate_v4(), p.tenant_id, 'Lost', 'lost',
       COALESCE((SELECT MAX(s.sequence) FROM pipeline_stages s WHERE s.pipeline_id = p.id), 0) + 2,
       0, false, true, 'red', true, 'lead', p.organization_id, p.id, NOW(), NOW()
FROM pipelines p
WHERE NOT EXISTS (SELECT 1 FROM pipeline_stages s WHERE s.pipeline_id = p.id AND s.is_lost)
ON CONFLICT DO NOTHING;

-- keep terminal stages ordered after the open ones: Won, then Lost
UPDATE pipeline_stages won SET sequence = mx.max_open + 1
FROM (
    SELECT pipeline_id, MAX(sequence) AS max_open FROM pipeline_stages
    WHERE pipeline_type = 'lead' AND NOT is_won AND NOT is_lost GROUP BY pipeline_id
) mx
WHERE won.pipeline_id = mx.pipeline_id AND won.is_won AND won.sequence <= mx.max_open;

UPDATE pipeline_stages lost SET sequence = won.sequence + 1
FROM pipeline_stages won
WHERE lost.pipeline_id = won.pipeline_id
  AND won.pipeline_type = 'lead' AND won.is_won
  AND lost.is_lost AND lost.sequence <= won.sequence;

-- ── 3. lost_reasons ──────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS lost_reasons (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, name)
);

INSERT INTO lost_reasons (tenant_id, name, position)
SELECT t.id, r.name, r.pos
FROM tenants t
CROSS JOIN (VALUES
    ('Narx qimmat', 0),
    ('Raqobatchini tanladi', 1),
    ('Javob bermadi', 2),
    ('Ehtiyoj yo''q', 3),
    ('Boshqa', 4)
) AS r(name, pos)
ON CONFLICT DO NOTHING;

-- ── 4. leads — the bitim columns ─────────────────────────────────────────
ALTER TABLE leads
    ADD COLUMN IF NOT EXISTS currency VARCHAR(3) NOT NULL DEFAULT 'UZS',
    ADD COLUMN IF NOT EXISTS pipeline_id UUID REFERENCES pipelines(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS stage_id UUID REFERENCES pipeline_stages(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS responsible_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS partner_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS lost_reason_id UUID REFERENCES lost_reasons(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS lost_note TEXT,
    ADD COLUMN IF NOT EXISTS won_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS lost_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS last_activity_at TIMESTAMP;

-- status CHECK was dropped in 289 but the source CHECK survived — leads must
-- accept tenant-defined sources (Telegram, Tavsiya, …) the same way.
ALTER TABLE leads DROP CONSTRAINT IF EXISTS chk_lead_source;

-- Carry-over: stage from the status code, per-org first…
UPDATE leads l SET stage_id = ps.id, pipeline_id = ps.pipeline_id
FROM pipeline_stages ps
WHERE l.deleted_at IS NULL AND l.stage_id IS NULL
  AND ps.pipeline_type = 'lead' AND ps.tenant_id = l.tenant_id
  AND ps.organization_id IS NOT DISTINCT FROM l.organization_id
  AND ps.code = l.status;

-- …then unmatched (free-text status / org mismatch) land in the same-org 'new'…
UPDATE leads l SET stage_id = s.sid, pipeline_id = s.pid
FROM (
    SELECT DISTINCT ON (tenant_id, organization_id) tenant_id, organization_id, id AS sid, pipeline_id AS pid
    FROM pipeline_stages WHERE pipeline_type = 'lead' AND code = 'new'
    ORDER BY tenant_id, organization_id, created_at
) s
WHERE l.deleted_at IS NULL AND l.stage_id IS NULL
  AND s.tenant_id = l.tenant_id
  AND s.organization_id IS NOT DISTINCT FROM l.organization_id;

-- …and as a last resort any 'new' stage of the tenant.
UPDATE leads l SET stage_id = s.sid, pipeline_id = s.pid
FROM (
    SELECT DISTINCT ON (tenant_id) tenant_id, id AS sid, pipeline_id AS pid
    FROM pipeline_stages WHERE pipeline_type = 'lead' AND code = 'new'
    ORDER BY tenant_id, created_at
) s
WHERE l.deleted_at IS NULL AND l.stage_id IS NULL AND s.tenant_id = l.tenant_id;

-- Partner link from past conversions. Guarded by EXISTS because ConvertLead
-- historically wrote the *opportunity* id into converted_to when no contact
-- was created (see audit §1) — those rows stay NULL here.
UPDATE leads l SET partner_id = l.converted_to
WHERE l.converted_to IS NOT NULL AND l.partner_id IS NULL
  AND EXISTS (SELECT 1 FROM contacts c WHERE c.id = l.converted_to);

-- Converted leads are historical wins: move to the Won stage of their pipeline.
UPDATE leads l SET won_at = COALESCE(l.converted_at, l.updated_at), stage_id = w.id, status = 'won'
FROM pipeline_stages w
WHERE l.deleted_at IS NULL AND l.converted_at IS NOT NULL AND l.won_at IS NULL
  AND w.pipeline_id = l.pipeline_id AND w.is_won;

-- Lost leads get a timestamp (reason stays NULL — it was never captured).
UPDATE leads SET lost_at = updated_at
WHERE deleted_at IS NULL AND status = 'lost' AND lost_at IS NULL;

UPDATE leads SET last_activity_at = COALESCE(updated_at, created_at)
WHERE last_activity_at IS NULL;

-- Responsible: map the legacy assigned_to (users) onto employees where linked.
UPDATE leads l SET responsible_employee_id = u.employee_id
FROM users u
WHERE l.assigned_to = u.id AND u.employee_id IS NOT NULL
  AND l.responsible_employee_id IS NULL;

-- ── 5. lead_stage_history ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS lead_stage_history (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    lead_id UUID NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    from_stage_id UUID REFERENCES pipeline_stages(id) ON DELETE SET NULL,
    to_stage_id UUID REFERENCES pipeline_stages(id) ON DELETE SET NULL,
    changed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    changed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_lead_stage_history_lead ON lead_stage_history(lead_id, changed_at);
CREATE INDEX IF NOT EXISTS idx_lead_stage_history_tenant ON lead_stage_history(tenant_id, to_stage_id, changed_at);

-- Every live lead gets an initial history row so funnel/cycle reports work
-- from day one (entered-current-stage at created_at).
INSERT INTO lead_stage_history (tenant_id, lead_id, from_stage_id, to_stage_id, changed_at)
SELECT l.tenant_id, l.id, NULL, l.stage_id, l.created_at
FROM leads l
WHERE l.deleted_at IS NULL AND l.stage_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM lead_stage_history h WHERE h.lead_id = l.id);

-- ── 6. Calls attach to leads ─────────────────────────────────────────────
ALTER TABLE call_logs ADD COLUMN IF NOT EXISTS lead_id UUID REFERENCES leads(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_call_logs_lead ON call_logs(lead_id);

-- ── 7. contract_links accepts direct lead links ──────────────────────────
-- 'crm_deal' (→ opportunities) stays valid for existing rows; new CRM links
-- write 'crm_lead' (→ leads).
ALTER TABLE contract_links DROP CONSTRAINT IF EXISTS contract_links_linked_module_check;
ALTER TABLE contract_links ADD CONSTRAINT contract_links_linked_module_check
    CHECK (linked_module IN ('crm_deal', 'crm_lead', 'construction_object', 'purchase_order', 'sale_order'));

-- ── 8. Permissions (CRM routes are gated from this release) ──────────────
INSERT INTO permissions (id, module, resource, action, description)
SELECT gen_random_uuid(), 'crm', r.resource, a.action,
       'Permission to ' || a.action || ' crm ' || r.resource || 's'
FROM (VALUES ('lead'), ('pipeline'), ('contact'), ('call'), ('activity'), ('report')) AS r(resource)
CROSS JOIN (VALUES ('read'), ('create'), ('update'), ('delete')) AS a(action)
WHERE NOT EXISTS (
    SELECT 1 FROM permissions WHERE module = 'crm' AND resource = r.resource AND action = a.action
);

-- ── 9. Indexes ───────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_leads_stage ON leads(tenant_id, stage_id);
CREATE INDEX IF NOT EXISTS idx_leads_responsible ON leads(tenant_id, responsible_employee_id);
CREATE INDEX IF NOT EXISTS idx_leads_last_activity ON leads(tenant_id, last_activity_at);
-- normalized-phone dedupe lookups (last 9 digits — Uzbek numbers without country code)
CREATE INDEX IF NOT EXISTS idx_leads_phone_digits
    ON leads (tenant_id, RIGHT(REGEXP_REPLACE(COALESCE(phone, ''), '[^0-9]', '', 'g'), 9));
CREATE INDEX IF NOT EXISTS idx_contacts_phone_digits
    ON contacts (tenant_id, RIGHT(REGEXP_REPLACE(COALESCE(phone, ''), '[^0-9]', '', 'g'), 9));
