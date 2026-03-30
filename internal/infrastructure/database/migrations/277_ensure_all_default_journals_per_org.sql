-- Fix journals that were created at tenant level (no organization_id) by migration 276
-- Assign them to the first active organization for that tenant

UPDATE journals
SET organization_id = (
    SELECT o.id FROM organizations o
    WHERE o.tenant_id = journals.tenant_id
      AND o.deleted_at IS NULL
    ORDER BY o.created_at ASC
    LIMIT 1
),
updated_at = NOW()
WHERE journals.organization_id IS NULL
  AND journals.deleted_at IS NULL
  AND EXISTS (
    SELECT 1 FROM organizations o
    WHERE o.tenant_id = journals.tenant_id
      AND o.deleted_at IS NULL
  );
