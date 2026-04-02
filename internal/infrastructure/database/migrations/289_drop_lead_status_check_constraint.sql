-- Drop the hardcoded CHECK constraint on leads.status
-- Now that lead stages are configurable via pipeline_stages table,
-- the status field must accept any stage code, not just the original 5
ALTER TABLE leads DROP CONSTRAINT IF EXISTS chk_lead_status;
