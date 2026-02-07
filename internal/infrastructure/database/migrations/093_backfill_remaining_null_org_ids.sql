-- Migration 093: Backfill organization_id for tables where Create handlers
-- were reading org_id from request body but frontend wasn't always sending it

-- journal_entries
UPDATE journal_entries je SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = je.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE je.organization_id IS NULL;

-- sales_invoices
UPDATE sales_invoices si SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = si.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE si.organization_id IS NULL;

-- purchase_invoices
UPDATE purchase_invoices pi SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = pi.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE pi.organization_id IS NULL;

-- contacts
UPDATE contacts c SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = c.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE c.organization_id IS NULL;

-- product_categories
UPDATE product_categories pc SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = pc.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE pc.organization_id IS NULL;
