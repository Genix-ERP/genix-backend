-- Trial system: add trial_ends_at and account_clear_at to tenants

ALTER TABLE tenants
  ADD COLUMN IF NOT EXISTS trial_ends_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS account_clear_at TIMESTAMPTZ;

-- Back-fill existing tenants: treat them as already in trial from their creation date
UPDATE tenants
SET
  trial_ends_at    = created_at + INTERVAL '7 days',
  account_clear_at = created_at + INTERVAL '30 days',
  subscription_status = CASE
    WHEN subscription_status = 'active' AND subscription_plan = 'free' THEN 'trialing'
    ELSE subscription_status
  END
WHERE deleted_at IS NULL;
