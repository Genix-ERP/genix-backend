-- 344_backfill_notification_data_fields.sql
--
-- Historical `notifications` rows were written with sparse `data` payloads
-- (e.g., {reconciliation_id} only). Their frozen `title`/`message` captured
-- the user's language at creation time, so when the UI language changes the
-- frontend catalog (src/utils/notificationCatalog.js) cannot re-render them
-- and falls back to the stored strings — producing the "Akt sverka javobsiz"
-- Uzbek title under an English UI that prompted this migration.
--
-- The emitters were already updated (additively, mobile-safe) to pack the
-- missing interpolation fields into `data`. This migration backfills those
-- fields into all pre-existing rows by joining each notification to its
-- source record, so the catalog can retranslate them on language switch.
--
-- Types handled:
--   reconciliation_reminder      → partner_name, days=3
--   reconciliation_no_response   → partner_name, days=7
--   reconciliation_response      → partner_name (response already present)
--   salary_confirmed             → employee_name
--   payment_recorded             → customer_name
--   credit_note_created          → customer_name
--   act_cancelled                → reason  (from construction_act.rejection_reason)
--   forma19_created              → act_name (from construction_act.name)
--
-- Safety:
--   - WHERE NOT (data ? '<field>') guards keep the migration idempotent:
--     rerunning never double-writes a key.
--   - The updates only touch `data`; `title`/`message` stay unchanged so
--     mobile behaviour is unchanged. Web retranslates via the catalog.
--   - Rows whose source record was deleted (orphans) aren't matched by the
--     joins and remain with sparse `data`; the web keeps falling back to
--     stored strings, which is the intended graceful-degradation behaviour.

-- ─── Reconciliation: reminder (3-day) and no-response (7-day) ────────────────
UPDATE notifications n
SET data = COALESCE(n.data, '{}'::jsonb)
           || jsonb_build_object(
                'partner_name', COALESCE(ct.name, 'Kontragent'),
                'days', CASE n.type
                          WHEN 'reconciliation_reminder'     THEN 3
                          WHEN 'reconciliation_no_response'  THEN 7
                        END
              )
FROM reconciliation_acts ra
LEFT JOIN contacts ct ON ct.id = ra.partner_id
WHERE n.type IN ('reconciliation_reminder', 'reconciliation_no_response')
  AND n.data ? 'reconciliation_id'
  AND NOT (n.data ? 'partner_name')
  AND ra.id::text = n.data->>'reconciliation_id';

-- ─── Reconciliation: response (partner_name only; `response` already present) ─
UPDATE notifications n
SET data = COALESCE(n.data, '{}'::jsonb)
           || jsonb_build_object('partner_name', COALESCE(ct.name, 'Kontragent'))
FROM reconciliation_acts ra
LEFT JOIN contacts ct ON ct.id = ra.partner_id
WHERE n.type = 'reconciliation_response'
  AND n.data ? 'reconciliation_id'
  AND NOT (n.data ? 'partner_name')
  AND ra.id::text = n.data->>'reconciliation_id';

-- ─── Payroll: salary_confirmed → employee_name ──────────────────────────────
UPDATE notifications n
SET data = COALESCE(n.data, '{}'::jsonb)
           || jsonb_build_object('employee_name', TRIM(COALESCE(e.first_name, '') || ' ' || COALESCE(e.last_name, '')))
FROM payroll_entries pe
JOIN employees e ON e.id = pe.employee_id
WHERE n.type = 'salary_confirmed'
  AND n.data ? 'entry_id'
  AND NOT (n.data ? 'employee_name')
  AND pe.id::text = n.data->>'entry_id';

-- ─── Sales: payment_recorded → customer_name ────────────────────────────────
UPDATE notifications n
SET data = COALESCE(n.data, '{}'::jsonb)
           || jsonb_build_object('customer_name', COALESCE(ct.name, ''))
FROM contacts ct
WHERE n.type = 'payment_recorded'
  AND n.data ? 'customer_id'
  AND NOT (n.data ? 'customer_name')
  AND ct.id::text = n.data->>'customer_id';

-- ─── Sales: credit_note_created → customer_name ─────────────────────────────
UPDATE notifications n
SET data = COALESCE(n.data, '{}'::jsonb)
           || jsonb_build_object('customer_name', COALESCE(ct.name, ''))
FROM contacts ct
WHERE n.type = 'credit_note_created'
  AND n.data ? 'customer_id'
  AND NOT (n.data ? 'customer_name')
  AND ct.id::text = n.data->>'customer_id';

-- ─── Construction acts: act_cancelled → reason ──────────────────────────────
UPDATE notifications n
SET data = COALESCE(n.data, '{}'::jsonb)
           || jsonb_build_object('reason', COALESCE(ca.rejection_reason, ''))
FROM construction_act ca
WHERE n.type = 'act_cancelled'
  AND n.data ? 'act_id'
  AND NOT (n.data ? 'reason')
  AND ca.id = (n.data->>'act_id')::bigint;

-- ─── Construction acts: forma19_created → act_name ──────────────────────────
UPDATE notifications n
SET data = COALESCE(n.data, '{}'::jsonb)
           || jsonb_build_object('act_name', COALESCE(ca.name, ''))
FROM construction_act ca
WHERE n.type = 'forma19_created'
  AND n.data ? 'act_id'
  AND NOT (n.data ? 'act_name')
  AND ca.id = (n.data->>'act_id')::bigint;
