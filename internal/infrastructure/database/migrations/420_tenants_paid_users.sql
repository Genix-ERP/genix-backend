-- Per-user pricing: track how many users the tenant has paid for
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS paid_users INT NOT NULL DEFAULT 1;
