-- 361_backfill_topup_audit_target.sql
--
-- Legacy fix: rewrites the literal "topup" target written by older
-- versions of the resource top-up endpoints (POST /resource-topup,
-- DELETE /resource-topup/:id) to the actual resource line name. The
-- endpoints now log the line name directly (see commit that touched
-- construction_resource_topup.go), but rows already in the audit
-- table still carry the placeholder. Without this backfill the
-- O'zgarishlar jurnali keeps showing a bare "topup" headline above
-- the description for every old top-up event.
--
-- Strategy: pull the resource name from the row's `description` —
-- both topup_add and topup_del descriptions follow the pattern
--   "<resource name> uchun qo'shimcha buyurtma…"
-- so SPLIT_PART on " uchun qo'shimcha buyurtma" gives us the name
-- in one shot. We TRIM defensively in case the importer ever
-- prefixed/suffixed a space.
--
-- Idempotent: only rows where target = 'topup' are touched, so
-- re-running the migration is a no-op.

UPDATE construction_smeta_audit
SET target = TRIM(SPLIT_PART(description, ' uchun qo''shimcha buyurtma', 1))
WHERE action IN ('topup_add', 'topup_del')
  AND target = 'topup'
  AND description ILIKE '% uchun qo''shimcha buyurtma%'
  AND TRIM(SPLIT_PART(description, ' uchun qo''shimcha buyurtma', 1)) <> '';
