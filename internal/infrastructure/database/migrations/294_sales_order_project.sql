-- Add project_id and project_name to sales_orders for intercompany project tracking
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL;
ALTER TABLE sales_orders ADD COLUMN IF NOT EXISTS project_name VARCHAR(255);
CREATE INDEX IF NOT EXISTS idx_sales_orders_project ON sales_orders(project_id) WHERE project_id IS NOT NULL;
