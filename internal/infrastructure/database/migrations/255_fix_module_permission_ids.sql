-- Fix module IDs in employee_module_permissions AND roles table to match backend Require() names

-- 1. Fix employee_module_permissions
ALTER TABLE employee_module_permissions DROP CONSTRAINT IF EXISTS employee_module_permissions_tenant_id_employee_id_module_id_key;

UPDATE employee_module_permissions SET module_id = 'crm' WHERE module_id = 'customers';
UPDATE employee_module_permissions SET module_id = 'finance' WHERE module_id = 'financials';
UPDATE employee_module_permissions SET module_id = 'purchase' WHERE module_id IN ('purchases', 'procurement');
UPDATE employee_module_permissions SET module_id = 'ai' WHERE module_id = 'ai_assistant';
UPDATE employee_module_permissions SET module_id = 'sales' WHERE module_id = 'sales_orders';

DELETE FROM employee_module_permissions a
USING employee_module_permissions b
WHERE a.tenant_id = b.tenant_id
  AND a.employee_id = b.employee_id
  AND a.module_id = b.module_id
  AND a.id < b.id;

ALTER TABLE employee_module_permissions ADD CONSTRAINT employee_module_permissions_tenant_id_employee_id_module_id_key UNIQUE (tenant_id, employee_id, module_id);

-- 2. Fix roles permissions JSON - rename old module keys to new ones
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
