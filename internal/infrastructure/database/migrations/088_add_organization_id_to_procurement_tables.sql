-- Migration 086: Add organization_id to procurement tables
-- Tables: rfqs, procurement_contracts, contracts, supplier_price_history,
--         purchase_requisitions, goods_receipts, vendor_prices

-- =====================================================
-- RFQs (backfill from tenant default org)
-- =====================================================
ALTER TABLE rfqs ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE rfqs r SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = r.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE r.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_rfqs_organization ON rfqs(organization_id);

-- =====================================================
-- PROCUREMENT CONTRACTS (backfill from vendor/contact org)
-- =====================================================
ALTER TABLE procurement_contracts ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE procurement_contracts pc SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = pc.vendor_id
) WHERE pc.organization_id IS NULL AND pc.vendor_id IS NOT NULL;
UPDATE procurement_contracts pc SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = pc.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE pc.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_procurement_contracts_organization ON procurement_contracts(organization_id);

-- =====================================================
-- CONTRACTS (backfill from supplier/contact org)
-- =====================================================
ALTER TABLE contracts ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE contracts ct SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = ct.supplier_id
) WHERE ct.organization_id IS NULL AND ct.supplier_id IS NOT NULL;
UPDATE contracts ct SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = ct.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE ct.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_contracts_organization ON contracts(organization_id);

-- =====================================================
-- SUPPLIER PRICE HISTORY (backfill from vendor/contact org)
-- =====================================================
ALTER TABLE supplier_price_history ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE supplier_price_history sph SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = sph.vendor_id
) WHERE sph.organization_id IS NULL AND sph.vendor_id IS NOT NULL;
UPDATE supplier_price_history sph SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = sph.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE sph.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_supplier_price_history_organization ON supplier_price_history(organization_id);

-- =====================================================
-- PURCHASE REQUISITIONS (backfill from tenant default org)
-- =====================================================
ALTER TABLE purchase_requisitions ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE purchase_requisitions pr SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = pr.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE pr.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_purchase_requisitions_organization ON purchase_requisitions(organization_id);

-- =====================================================
-- GOODS RECEIPTS (backfill from purchase order or warehouse org)
-- =====================================================
ALTER TABLE goods_receipts ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE goods_receipts gr SET organization_id = (
    SELECT po.organization_id FROM purchase_orders po WHERE po.id = gr.purchase_order_id
) WHERE gr.organization_id IS NULL AND gr.purchase_order_id IS NOT NULL;
UPDATE goods_receipts gr SET organization_id = (
    SELECT w.organization_id FROM warehouses w WHERE w.id = gr.warehouse_id
) WHERE gr.organization_id IS NULL AND gr.warehouse_id IS NOT NULL;
UPDATE goods_receipts gr SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = gr.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE gr.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_goods_receipts_organization ON goods_receipts(organization_id);

-- =====================================================
-- VENDOR PRICES (backfill from vendor/contact org)
-- =====================================================
ALTER TABLE vendor_prices ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE vendor_prices vp SET organization_id = (
    SELECT c.organization_id FROM contacts c WHERE c.id = vp.vendor_id
) WHERE vp.organization_id IS NULL AND vp.vendor_id IS NOT NULL;
UPDATE vendor_prices vp SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = vp.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE vp.organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_vendor_prices_organization ON vendor_prices(organization_id);
