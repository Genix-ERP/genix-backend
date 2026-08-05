-- ============================================================================
-- 470: Material zayavkalari v2 (Qurilish → Ombor → Xarid closed loop)
--
-- Prorab zayavka tashlaydi → omborchi tekshiradi: bor bo'lsa chiqaradi, yo'q
-- bo'lsa xarid so'roviga yuboradi → kirim kelgach chiqaradi → prorab qabul
-- qiladi. Legacy JSONB-items oqimi (mobil ilova ishlatadi) o'zgarmaydi —
-- yangi oqim flow='v2' bilan ajratiladi, qatorlar alohida jadvalda.
-- ============================================================================

-- ── 1. Request header: v2 columns ───────────────────────────────────────────
ALTER TABLE construction_material_requests
    ADD COLUMN IF NOT EXISTS flow VARCHAR(10) NOT NULL DEFAULT 'legacy',
    ADD COLUMN IF NOT EXISTS priority VARCHAR(10) NOT NULL DEFAULT 'normal',
    ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS warehouse_id UUID REFERENCES warehouses(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS requested_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS rejected_reason TEXT,
    ADD COLUMN IF NOT EXISTS closed_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS attachments JSONB NOT NULL DEFAULT '[]';

-- Priority vocabulary (new columns start valid for every existing row).
ALTER TABLE construction_material_requests
    DROP CONSTRAINT IF EXISTS chk_cmr_priority;
ALTER TABLE construction_material_requests
    ADD CONSTRAINT chk_cmr_priority CHECK (priority IN ('normal', 'urgent'));

-- Status vocabulary. Legacy rows may carry old dialects (draft/pending/
-- approved/fulfilled/…) on production tenants, so the constraint is NOT VALID:
-- it only guards new writes; the v2 state machine in Go is the real gate.
ALTER TABLE construction_material_requests
    DROP CONSTRAINT IF EXISTS chk_cmr_status;
ALTER TABLE construction_material_requests
    ADD CONSTRAINT chk_cmr_status CHECK (status IN (
        -- legacy dialect
        'draft', 'pending', 'submitted', 'approved', 'rejected', 'fulfilled', 'cancelled',
        -- v2 vocabulary
        'new', 'in_review', 'in_purchase', 'partially_fulfilled', 'issued', 'closed'
    )) NOT VALID;

-- ── 2. Normalized request lines ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS construction_material_request_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    request_id BIGINT NOT NULL REFERENCES construction_material_requests(id) ON DELETE CASCADE,

    product_id UUID NOT NULL REFERENCES products(id),
    product_name VARCHAR(500) NOT NULL DEFAULT '',
    unit VARCHAR(50) NOT NULL DEFAULT '',

    qty_requested NUMERIC(18,3) NOT NULL CHECK (qty_requested > 0),
    qty_issued NUMERIC(18,3) NOT NULL DEFAULT 0 CHECK (qty_issued >= 0),
    qty_in_purchase NUMERIC(18,3) NOT NULL DEFAULT 0 CHECK (qty_in_purchase >= 0),

    -- pending → in_purchase / partial / issued; rejected qatorlar
    -- agregatsiyada hisobga olinmaydi.
    line_status VARCHAR(30) NOT NULL DEFAULT 'pending'
        CHECK (line_status IN ('pending', 'in_purchase', 'partial', 'issued', 'rejected')),

    -- Omborchi ko'rib chiqqan paytdagi qoldiq — tarix/audit uchun.
    stock_snapshot NUMERIC(18,3),

    -- 2-bosqich (smeta-limit nazorati) uchun tayyor turadi; hozircha
    -- yozilmaydi. construction_estimate_lines.id (BIGINT) ga ishora, FK yo'q —
    -- smeta qayta import qilinganda qatorlar o'chib qayta yaratiladi.
    smeta_item_id BIGINT,

    note TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cmri_request ON construction_material_request_items(tenant_id, request_id);
CREATE INDEX IF NOT EXISTS idx_cmri_product ON construction_material_request_items(tenant_id, product_id);

-- ── 3. Backlinks: zayavka ↔ xarid so'rovi ↔ chiqim hujjati ─────────────────
ALTER TABLE purchase_requisitions
    ADD COLUMN IF NOT EXISTS material_request_id BIGINT REFERENCES construction_material_requests(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_pr_material_request
    ON purchase_requisitions(material_request_id)
    WHERE material_request_id IS NOT NULL;

ALTER TABLE stock_operations
    ADD COLUMN IF NOT EXISTS material_request_id BIGINT REFERENCES construction_material_requests(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_stock_ops_material_request
    ON stock_operations(material_request_id)
    WHERE material_request_id IS NOT NULL;

-- ── 4. Inbox/list yo'llari uchun indekslar ──────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_cmr_v2_tenant_status
    ON construction_material_requests(tenant_id, status)
    WHERE flow = 'v2';
CREATE INDEX IF NOT EXISTS idx_cmr_v2_required_date
    ON construction_material_requests(tenant_id, required_date)
    WHERE flow = 'v2';

-- ── 5. Permission seed (461-uslub, singular keys) ───────────────────────────
INSERT INTO permissions (module, resource, action, description) VALUES
('construction', 'material_request', 'read',   'View material requests (zayavkalar)'),
('construction', 'material_request', 'create', 'Create material requests'),
('construction', 'material_request', 'update', 'Edit/cancel/accept own material requests'),
('construction', 'material_request', 'delete', 'Delete material requests')
ON CONFLICT (module, resource, action) DO NOTHING;
