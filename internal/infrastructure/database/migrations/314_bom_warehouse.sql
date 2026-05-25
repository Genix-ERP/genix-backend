-- Add warehouse_id to product_boms for finished goods destination
ALTER TABLE product_boms ADD COLUMN IF NOT EXISTS warehouse_id UUID REFERENCES warehouses(id);
