-- Fix module IDs in roles permissions JSON to match backend Require() names
-- The roles table stores permissions as JSONB with module IDs as keys
-- Old: customers, financials, purchases, procurement, ai_assistant, sales_orders
-- New: crm, finance, purchase, purchase, ai, sales

UPDATE roles SET permissions = (
  SELECT jsonb_object_agg(
    CASE key
      WHEN 'customers' THEN 'crm'
      WHEN 'financials' THEN 'finance'
      WHEN 'purchases' THEN 'purchase'
      WHEN 'procurement' THEN 'purchase'
      WHEN 'ai_assistant' THEN 'ai'
      WHEN 'sales_orders' THEN 'sales'
      ELSE key
    END,
    value
  )
  FROM jsonb_each(permissions::jsonb)
)
WHERE permissions IS NOT NULL
  AND permissions::text != 'null'
  AND (
    permissions::text LIKE '%customers%'
    OR permissions::text LIKE '%financials%'
    OR permissions::text LIKE '%purchases%'
    OR permissions::text LIKE '%procurement%'
    OR permissions::text LIKE '%ai_assistant%'
    OR permissions::text LIKE '%sales_orders%'
  );
