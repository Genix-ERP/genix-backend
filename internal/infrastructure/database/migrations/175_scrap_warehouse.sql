-- Add warehouse_type to warehouses for scrap warehouse identification
ALTER TABLE warehouses ADD COLUMN IF NOT EXISTS warehouse_type VARCHAR(50) DEFAULT 'regular';
