-- Migration 452: Savdo v2 phase 2 — cross-module link columns.
-- savdo-audit.md §6: no lead link existed at all, and the Qurilish link was
-- broken by a type mismatch (sales_orders.project_id is UUID while
-- construction_projects.id is BIGSERIAL — writes were silently dropped or
-- zero-UUID). lead_id closes the CRM won-deal handoff; construction_project_id
-- is the correctly-typed object link (legacy project_id/project_name columns
-- stay for display compatibility).

ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS lead_id UUID REFERENCES leads(id);
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS construction_project_id BIGINT REFERENCES construction_projects(id);

CREATE INDEX IF NOT EXISTS idx_sales_orders_lead
    ON sales_orders(lead_id) WHERE lead_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sales_orders_construction_project
    ON sales_orders(tenant_id, construction_project_id) WHERE construction_project_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sales_orders_contract
    ON sales_orders(tenant_id, contract_id) WHERE contract_id IS NOT NULL;
