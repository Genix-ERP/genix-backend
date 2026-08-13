-- 501 (asli 499): Seed the legacy tax_rates catalog with the standard Uzbek QQS rates.
--
-- Soliq audit 2026-08-13: the only tax_rates rows most tenants ever had were
-- test artifacts (soft-deleted), so the sales UI's default-tax fallback found
-- nothing, every sales order/invoice line went out with tax_id = NULL, and
-- output VAT was permanently 0 while input VAT (typed manually on vendor
-- bills) accumulated.
--
-- NDS12 is activated ONLY for tenants that show VAT evidence (input VAT on
-- purchase invoices, or an active 'sales' company_tax_rates row) — activating
-- it blindly would start applying 12% VAT to sales of simplified-regime
-- tenants. Everyone else gets the row inactive: visible in pickers and one
-- toggle away in settings.

INSERT INTO tax_rates (id, tenant_id, code, name, rate, type, tax_type, is_active, created_at, updated_at)
SELECT uuid_generate_v4(), t.id, 'NDS12', 'QQS 12%', 12, 'percentage', 'sales',
       (EXISTS (SELECT 1 FROM purchase_invoices pi
                WHERE pi.tenant_id = t.id AND COALESCE(pi.tax_amount, 0) > 0 AND pi.deleted_at IS NULL)
        OR EXISTS (SELECT 1 FROM company_tax_rates ctr
                   WHERE ctr.tenant_id = t.id AND ctr.applies_to = 'sales'
                     AND ctr.is_active = TRUE AND ctr.deleted_at IS NULL)),
       NOW(), NOW()
FROM tenants t
WHERE t.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tax_rates tr
                  WHERE tr.tenant_id = t.id AND tr.code = 'NDS12')
ON CONFLICT (tenant_id, code) DO NOTHING;

INSERT INTO tax_rates (id, tenant_id, code, name, rate, type, tax_type, is_active, created_at, updated_at)
SELECT uuid_generate_v4(), t.id, 'NDS0', 'QQS 0% (imtiyozli)', 0, 'percentage', 'sales', FALSE, NOW(), NOW()
FROM tenants t
WHERE t.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tax_rates tr
                  WHERE tr.tenant_id = t.id AND tr.code = 'NDS0')
ON CONFLICT (tenant_id, code) DO NOTHING;
