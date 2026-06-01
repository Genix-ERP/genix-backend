-- 343_fix_low_stock_reservation_rows.sql
--
-- Repairs historical `notifications` rows that were wrongly emitted with
-- type='low_stock' from the material-reservation request flow
-- (handler/construction_material_reservations.go, pre-fix).
--
-- Those rows had data = {reservation_id, product_id} and the backend
-- `low_stock` template, which expects (product_name, remaining_qty), produced
-- garbled text like:
--   "Product Reservation request for product (qty: 3.00) is running low ( remaining)"
-- with a misleading title "Low Stock Warning".
--
-- The emitter is now fixed to use a dedicated `material_reservation_request`
-- type (see notifications.go + construction_material_reservations.go). This
-- migration retrofits the historical rows so:
--   * Mobile, which reads frozen `title`/`message`, sees a clean Uzbek title
--     and body instead of the broken English sentence.
--   * Web, which re-renders via the frontend notificationCatalog.js, picks
--     the new type's template and can interpolate `product_name` + `quantity`
--     from the `data` JSONB — yielding a correctly translated body on every
--     UI language switch.
--
-- Selection criterion: type='low_stock' AND data has a `reservation_id` key.
-- This is exactly the footprint of the misused emitter and leaves genuine
-- inventory low_stock rows untouched (those carry product_name/available
-- in data and have no reservation_id).
--
-- Idempotent: rerunning it is a no-op because pass A flips the type away
-- from 'low_stock'; subsequent passes only match rows that already match
-- their own conditions.

-- ─── Pass A — reclassify the type on every reservation-shaped row ────────────
UPDATE notifications
SET type = 'material_reservation_request'
WHERE type = 'low_stock'
  AND data ? 'reservation_id';

-- ─── Pass B — backfill data + refresh title/message where the reservation
--               still exists (so web can retranslate and mobile shows clean
--               Uzbek defaults). ─────────────────────────────────────────────
--
-- Wrapped in a table-existence check because some legacy production DBs have
-- schema_migrations marked through 310 but never actually created the
-- material_reservations table (DB restore from a logical dump that copied
-- the migrations table without copying all the data tables). Without this
-- guard the migration runner crash-loops on every container start.
-- Pass A is independent of material_reservations and always runs.
DO $mr$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_tables
        WHERE schemaname = 'public' AND tablename = 'material_reservations'
    ) THEN
        UPDATE notifications n
        SET title   = 'Material bron qilish so''rovi',
            message = COALESCE(p.name, 'Mahsulot')
                      || ' uchun bron qilish so''rovi (miqdori: '
                      || TRIM(TO_CHAR(COALESCE(mr.quantity, 0), 'FM9999999990.00'))
                      || ')',
            data    = COALESCE(n.data, '{}'::jsonb)
                      || jsonb_build_object(
                           'product_name', COALESCE(p.name, ''),
                           'quantity',     COALESCE(mr.quantity, 0)
                         )
        FROM material_reservations mr
        LEFT JOIN products p ON p.id = mr.product_id
        WHERE n.type = 'material_reservation_request'
          AND n.data ? 'reservation_id'
          AND NOT (n.data ? 'product_name')
          AND mr.id::text = n.data->>'reservation_id';
    ELSE
        RAISE NOTICE 'Skipping Pass B: material_reservations table does not exist (legacy DB).';
    END IF;
END
$mr$;

-- ─── Pass C — orphan rows (reservation deleted). At minimum replace the
--               broken English title/message with a generic Uzbek default so
--               mobile and web fallback both show something sensible. Web
--               will still fall back to the stored strings because product_name
--               is absent, but at least the strings are Uzbek now. ──────────
UPDATE notifications
SET title   = 'Material bron qilish so''rovi',
    message = 'Material bron qilish so''rovi yuborildi'
WHERE type = 'material_reservation_request'
  AND NOT (data ? 'product_name')
  AND (title = 'Low Stock Warning' OR title LIKE 'Product %' OR message LIKE 'Product %');
