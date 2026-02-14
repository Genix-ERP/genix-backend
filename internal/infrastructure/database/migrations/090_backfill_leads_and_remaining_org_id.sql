-- Migration 090: Backfill organization_id for leads and other tables that had the column
-- from original creation but were never backfilled

-- leads
UPDATE leads l SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = l.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE l.organization_id IS NULL;

-- bank_transactions (backfill from bank_accounts)
UPDATE bank_transactions bt SET organization_id = (
    SELECT ba.organization_id FROM bank_accounts ba WHERE ba.id = bt.bank_account_id
) WHERE bt.organization_id IS NULL AND bt.bank_account_id IS NOT NULL;
UPDATE bank_transactions bt SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = bt.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE bt.organization_id IS NULL;

-- budgets
UPDATE budgets b SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = b.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE b.organization_id IS NULL;

-- fiscal_years: deduplicate (keep earliest created row per tenant+start_date)
-- First reassign budgets from duplicate fiscal_years to the surviving one
UPDATE budgets b SET fiscal_year_id = (
    SELECT DISTINCT ON (fy2.tenant_id, fy2.start_date) fy2.id
    FROM fiscal_years fy2
    WHERE fy2.tenant_id = (SELECT fy3.tenant_id FROM fiscal_years fy3 WHERE fy3.id = b.fiscal_year_id)
      AND fy2.start_date = (SELECT fy3.start_date FROM fiscal_years fy3 WHERE fy3.id = b.fiscal_year_id)
      AND fy2.organization_id IS NULL
    ORDER BY fy2.tenant_id, fy2.start_date, fy2.created_at ASC
)
WHERE b.fiscal_year_id IN (
    SELECT id FROM fiscal_years WHERE organization_id IS NULL
);

-- Now delete duplicate fiscal_years
DELETE FROM fiscal_years
WHERE organization_id IS NULL
  AND id NOT IN (
    SELECT DISTINCT ON (tenant_id, start_date) id
    FROM fiscal_years
    WHERE organization_id IS NULL
    ORDER BY tenant_id, start_date, created_at ASC
  );

-- Backfill surviving fiscal_years
UPDATE fiscal_years fy SET organization_id = (
    SELECT o.id FROM organizations o WHERE o.tenant_id = fy.tenant_id ORDER BY o.created_at ASC LIMIT 1
) WHERE fy.organization_id IS NULL;
