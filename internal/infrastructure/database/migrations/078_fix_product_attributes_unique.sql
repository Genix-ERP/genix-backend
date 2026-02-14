-- Fix unique constraint on product_attributes to exclude soft-deleted rows
-- The old constraint prevents re-creating attributes with the same name after deletion

-- Drop the old unique constraint
ALTER TABLE product_attributes DROP CONSTRAINT IF EXISTS product_attributes_tenant_id_name_key;

-- Create a partial unique index that only applies to non-deleted rows
CREATE UNIQUE INDEX IF NOT EXISTS product_attributes_tenant_id_name_active_key
    ON product_attributes (tenant_id, name)
    WHERE deleted_at IS NULL;

-- Same fix for product_attribute_values
ALTER TABLE product_attribute_values DROP CONSTRAINT IF EXISTS product_attribute_values_attribute_id_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS product_attribute_values_attribute_id_name_active_key
    ON product_attribute_values (attribute_id, name)
    WHERE deleted_at IS NULL;
