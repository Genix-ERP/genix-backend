-- 444_expenses_v2.sql
--
-- Xarajatlar (Expenses) module rebuild — data model phase.
-- Full background: docs/xarajatlar-audit.md (repo root /docs).
--
-- What this migration does:
--   1. Lifecycle status model with a CHECK backstop:
--      draft → submitted → approved → paid, + rejected (reason required
--      server-side) and cancelled (legacy soft-cancel kept valid).
--      Existing rows remapped: pending→submitted, reimbursed→paid,
--      anything unrecognized→submitted.
--   2. Rows that already carry a posted journal entry from the OLD
--      approve-time posting (source_type='expense') are marked 'paid':
--      the GL already saw the cash leave at approval, so treating them
--      as awaiting-payment would double-post when the new /pay endpoint
--      fires. paid_at is backfilled from approved_at.
--   3. New lifecycle columns: submitted_at, rejected_by/at + reason,
--      paid_at/by, payment_account_id (which kassa/bank paid),
--      journal_entry_id (the posted JE created at pay time).
--   4. expense_categories gains color/icon/position for the UI and is
--      seeded with 8 defaults for EVERY existing tenant (new tenants
--      are seeded lazily by ListExpenseCategories in Go — same pattern
--      as construction_settings.seedDefaultCostCategories).
--   5. Backfill expenses.category_id from the legacy free-text
--      category_name column (exact tenant-scoped name match first,
--      then the old i18n keys travel/meals/transportation/…, else
--      "Boshqa") — this is what un-flattens the one-slice donut.
--   6. Backfill expenses.employee_id from employee_name when the name
--      matches exactly one employee in the tenant.
--   7. expense_links — polymorphic links to construction objects,
--      contracts and CRM deals (same convention as contract_links,
--      migration 443: linked_id is VARCHAR to span UUID and BIGSERIAL
--      keyed modules).
--
-- Companion Go changes: internal/handler/expense.go (transitions,
-- /submit /approve /reject /pay, stats), handler.go routes,
-- workflow_engine.go catalog (expenses.* events).

-- ---------------------------------------------------------------
-- 1. Lifecycle columns
-- ---------------------------------------------------------------
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS submitted_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS rejected_by UUID REFERENCES users(id);
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS rejected_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS rejection_reason TEXT;
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS paid_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS paid_by UUID REFERENCES users(id);
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS payment_account_id UUID REFERENCES accounts(id);
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS journal_entry_id UUID REFERENCES journal_entries(id);

-- ---------------------------------------------------------------
-- 2. Status remap (before the CHECK goes on)
-- ---------------------------------------------------------------
UPDATE expenses SET status = 'submitted' WHERE status = 'pending';
UPDATE expenses SET status = 'paid'      WHERE status = 'reimbursed';

-- Old approve-time posting: expense already has a posted JE → the cash
-- (or AP) leg is already in the GL. Mark paid and link the entry.
UPDATE expenses e
SET status           = 'paid',
    paid_at          = COALESCE(e.approved_at, e.updated_at),
    journal_entry_id = je.id
FROM journal_entries je
WHERE je.source_type = 'expense'
  AND je.source_id   = e.id
  AND je.status      = 'posted'
  AND je.deleted_at IS NULL
  AND e.status IN ('approved', 'paid')
  AND e.journal_entry_id IS NULL;

-- submitted_at for anything that has clearly been past draft
UPDATE expenses SET submitted_at = created_at
WHERE submitted_at IS NULL AND status IN ('submitted', 'approved', 'paid', 'rejected');

-- Anything left outside the new vocabulary (free-typed statuses were
-- possible: PUT accepted arbitrary strings) → submitted.
UPDATE expenses SET status = 'submitted'
WHERE status NOT IN ('draft', 'submitted', 'approved', 'paid', 'rejected', 'cancelled');

ALTER TABLE expenses DROP CONSTRAINT IF EXISTS chk_expenses_status;
ALTER TABLE expenses ADD CONSTRAINT chk_expenses_status
    CHECK (status IN ('draft', 'submitted', 'approved', 'paid', 'rejected', 'cancelled'));

-- ---------------------------------------------------------------
-- 3. Category model: UI columns + per-tenant default set
-- ---------------------------------------------------------------
ALTER TABLE expense_categories ADD COLUMN IF NOT EXISTS color VARCHAR(20);
ALTER TABLE expense_categories ADD COLUMN IF NOT EXISTS icon VARCHAR(50);
ALTER TABLE expense_categories ADD COLUMN IF NOT EXISTS position INTEGER NOT NULL DEFAULT 0;

INSERT INTO expense_categories (tenant_id, code, name, description, is_active, color, icon, position)
SELECT t.id, v.code, v.name, v.description, true, v.color, v.icon, v.position
FROM tenants t
CROSS JOIN (VALUES
    ('TRANSPORT', 'Transport',        'Yoqilg''i, taksi, yo''l xarajatlari',        '#185FA5', 'Car',           1),
    ('IJARA',     'Ijara',            'Ofis va ombor ijarasi',                      '#534AB7', 'Building2',     2),
    ('KOMMUNAL',  'Kommunal',         'Elektr, suv, gaz, internet, aloqa',          '#1D9E75', 'Zap',           3),
    ('OFIS',      'Ofis xarajatlari', 'Kanselyariya, jihozlar, xo''jalik buyumlari','#EF9F27', 'Briefcase',     4),
    ('SAFAR',     'Safar',            'Xizmat safari, mehmonxona, chipta',          '#0E9AA7', 'Plane',         5),
    ('REKLAMA',   'Reklama',          'Marketing va reklama xarajatlari',           '#D9534F', 'Megaphone',     6),
    ('MATERIAL',  'Materiallar',      'Xom ashyo va materiallar',                   '#8A6D3B', 'Package',       7),
    ('BOSHQA',    'Boshqa',           'Boshqa turdagi xarajatlar',                  '#888780', 'MoreHorizontal',8)
) AS v(code, name, description, color, icon, position)
ON CONFLICT (tenant_id, code) DO NOTHING;

-- Give user-created categories (pre-444) stable ordering after the seeds
UPDATE expense_categories SET position = 100 WHERE position = 0 AND code NOT IN
    ('TRANSPORT','IJARA','KOMMUNAL','OFIS','SAFAR','REKLAMA','MATERIAL','BOSHQA');

-- ---------------------------------------------------------------
-- 4. Backfill category_id from legacy free-text category_name
-- ---------------------------------------------------------------
-- 4a. Exact (case-insensitive) name match against the tenant's categories
UPDATE expenses e
SET category_id = c.id
FROM expense_categories c
WHERE e.category_id IS NULL
  AND e.category_name IS NOT NULL AND btrim(e.category_name) <> ''
  AND c.tenant_id = e.tenant_id
  AND lower(btrim(c.name)) = lower(btrim(e.category_name));

-- 4b. Legacy i18n keys the old UI stored as category_name
UPDATE expenses e
SET category_id = c.id
FROM expense_categories c
WHERE e.category_id IS NULL
  AND c.tenant_id = e.tenant_id
  AND c.code = CASE lower(btrim(COALESCE(e.category_name, '')))
        WHEN 'transportation'  THEN 'TRANSPORT'
        WHEN 'transport'       THEN 'TRANSPORT'
        WHEN 'travel'          THEN 'SAFAR'
        WHEN 'accommodation'   THEN 'SAFAR'
        WHEN 'rent'            THEN 'IJARA'
        WHEN 'ijara'           THEN 'IJARA'
        WHEN 'utilities'       THEN 'KOMMUNAL'
        WHEN 'kommunal'        THEN 'KOMMUNAL'
        WHEN 'office_supplies' THEN 'OFIS'
        WHEN 'office'          THEN 'OFIS'
        WHEN 'marketing'       THEN 'REKLAMA'
        WHEN 'advertising'     THEN 'REKLAMA'
        WHEN 'reklama'         THEN 'REKLAMA'
        WHEN 'equipment'       THEN 'MATERIAL'
        WHEN 'materials'       THEN 'MATERIAL'
        ELSE '' END;

-- 4c. Everything still uncategorized → Boshqa (kept editable; the new
-- create form REQUIRES a category so the pool stops growing)
UPDATE expenses e
SET category_id = c.id
FROM expense_categories c
WHERE e.category_id IS NULL
  AND c.tenant_id = e.tenant_id
  AND c.code = 'BOSHQA';

-- ---------------------------------------------------------------
-- 5. Backfill employee_id from employee_name (unique matches only)
-- ---------------------------------------------------------------
WITH name_matches AS (
    SELECT e.id AS expense_id,
           MIN(emp.id::text)::uuid AS emp_id,
           COUNT(DISTINCT emp.id)  AS n
    FROM expenses e
    JOIN employees emp
      ON emp.tenant_id = e.tenant_id
     AND lower(btrim(e.employee_name)) IN (
           lower(btrim(emp.first_name || ' ' || emp.last_name)),
           lower(btrim(emp.last_name  || ' ' || emp.first_name))
         )
    WHERE e.employee_id IS NULL
      AND e.employee_name IS NOT NULL AND btrim(e.employee_name) <> ''
    GROUP BY e.id
)
UPDATE expenses e
SET employee_id = m.emp_id
FROM name_matches m
WHERE e.id = m.expense_id AND m.n = 1;

-- ---------------------------------------------------------------
-- 6. Polymorphic links (contract_links convention, migration 443)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS expense_links (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    expense_id UUID NOT NULL REFERENCES expenses(id) ON DELETE CASCADE,
    -- linked_id is VARCHAR to span UUID-keyed modules and the
    -- BIGSERIAL-keyed construction_projects (same as task_links/439
    -- and contract_links/443)
    linked_module VARCHAR(50) NOT NULL CHECK (linked_module IN ('construction_object', 'contract', 'crm_deal')),
    linked_id VARCHAR(100) NOT NULL,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(expense_id, linked_module, linked_id)
);
CREATE INDEX IF NOT EXISTS idx_expense_links_target ON expense_links(tenant_id, linked_module, linked_id);
CREATE INDEX IF NOT EXISTS idx_expense_links_expense ON expense_links(expense_id);

-- ---------------------------------------------------------------
-- 7. Query-path indexes for the new list/stats endpoints
-- ---------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_expenses_tenant_status
    ON expenses (tenant_id, status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_expenses_tenant_date
    ON expenses (tenant_id, expense_date) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_expenses_category
    ON expenses (category_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_expenses_employee
    ON expenses (employee_id) WHERE deleted_at IS NULL;
