-- diagnose_inventory_backfill.sql
-- Pinpoints why migration 367 didn't decrement inventory for "test 123123"
-- (and any other YAKUNIY-resource products still showing 0).
--
-- Run with:
--   psql "postgres://genix:genix_secret@localhost:5432/genixerp" \
--        -f diagnose_inventory_backfill.sql

\echo
\echo '──────────────────────────────────────────────────────────────────'
\echo 'D1 — schema_migrations: is 366 / 367 actually recorded?'
\echo '──────────────────────────────────────────────────────────────────'
SELECT version, name, applied_at
FROM schema_migrations
WHERE version IN (363, 364, 365, 366, 367)
ORDER BY version;

\echo
\echo '──────────────────────────────────────────────────────────────────'
\echo 'D2 — Does the test 123123 product exist? Multiple rows?'
\echo '──────────────────────────────────────────────────────────────────'
SELECT id, tenant_id, name, deleted_at IS NOT NULL AS deleted, created_at
FROM products
WHERE UPPER(name) LIKE UPPER('%test%123%')
ORDER BY created_at DESC;

\echo
\echo '──────────────────────────────────────────────────────────────────'
\echo 'D3 — All inventory rows for test 123123 across all warehouses.'
\echo '──────────────────────────────────────────────────────────────────'
SELECT inv.id, inv.warehouse_id, w.name AS warehouse_name,
       inv.quantity_on_hand, inv.updated_at
FROM inventory inv
LEFT JOIN warehouses w ON w.id = inv.warehouse_id
WHERE inv.product_id IN (
  SELECT id FROM products WHERE UPPER(name) LIKE UPPER('%test%123%')
);

\echo
\echo '──────────────────────────────────────────────────────────────────'
\echo 'D4 — How many YAKUNIY estimate_lines are pulling the test resource'
\echo '     and what consumed totals do they imply?'
\echo '──────────────────────────────────────────────────────────────────'
SELECT
    c.id AS resource_line_id,
    c.tenant_id,
    c.name,
    p.id AS parent_id,
    p.name AS parent_name,
    p.approval_status AS parent_status,
    c.norm_rate,
    c.quantity AS own_qty,
    c.quantity_override,
    p.done_quantity,
    CASE
        WHEN COALESCE(c.quantity_override, FALSE) AND COALESCE(c.quantity, 0) > 0
            THEN c.quantity
        WHEN COALESCE(c.norm_rate, 0) > 0
            THEN COALESCE(p.done_quantity, 0) * c.norm_rate
        ELSE 0
    END AS consumed_for_this_row
FROM construction_estimate_line c
JOIN construction_estimate_line p
  ON p.id = c.parent_line_id AND p.tenant_id = c.tenant_id
WHERE UPPER(c.name) LIKE UPPER('%test%123%')
  AND COALESCE(c.resource_type, '') <> ''
ORDER BY c.id;

\echo
\echo '──────────────────────────────────────────────────────────────────'
\echo 'D5 — Are there any active warehouses in this tenant at all?'
\echo '──────────────────────────────────────────────────────────────────'
SELECT id, tenant_id, name, COALESCE(is_active, true) AS active, created_at
FROM warehouses
ORDER BY created_at;

\echo
\echo '──────────────────────────────────────────────────────────────────'
\echo 'D6 — Final read: what would the migration WANT to deduct?'
\echo '     (Replicates the consumed_total CTE so we can see it directly.)'
\echo '──────────────────────────────────────────────────────────────────'
WITH yc AS (
    SELECT
        c.tenant_id,
        c.name AS resource_name,
        SUM(
            CASE
                WHEN COALESCE(c.quantity_override, FALSE) AND COALESCE(c.quantity, 0) > 0
                    THEN c.quantity
                WHEN COALESCE(c.norm_rate, 0) > 0
                    THEN COALESCE(p.done_quantity, 0) * c.norm_rate
                ELSE 0
            END
        ) AS consumed_total
    FROM construction_estimate_line c
    JOIN construction_estimate_line p
      ON p.id = c.parent_line_id AND p.tenant_id = c.tenant_id
    WHERE COALESCE(p.approval_status, '') = 'confirmed_engineer'
      AND COALESCE(p.done_quantity, 0) > 0
      AND COALESCE(c.resource_type, '') <> ''
    GROUP BY c.tenant_id, c.name
)
SELECT yc.resource_name, yc.consumed_total,
       (SELECT COUNT(*) FROM products pr
         WHERE UPPER(pr.name) = UPPER(yc.resource_name)
           AND pr.tenant_id = yc.tenant_id
           AND pr.deleted_at IS NULL) AS matching_products,
       (SELECT pr.id FROM products pr
         WHERE UPPER(pr.name) = UPPER(yc.resource_name)
           AND pr.tenant_id = yc.tenant_id
           AND pr.deleted_at IS NULL
         LIMIT 1) AS first_product_id
FROM yc
WHERE UPPER(yc.resource_name) LIKE UPPER('%test%123%');
