-- diagnose_inventory.sql
-- Verifies whether YAKUNIY confirmations have actually decremented inventory.
-- Run with:
--   psql "postgres://genix:genix_secret@localhost:5432/genixerp" -f diagnose_inventory.sql

\echo
\echo '======================================================================'
\echo 'I1 - Material reservations created for project 1 YAKUNIY works.'
\echo '     Check status (should be approved), warehouse_id (must be NOT NULL'
\echo '     for inventory to be touched), and quantity.'
\echo '======================================================================'
SELECT
    mr.id,
    mr.estimate_line_id AS work_id,
    mr.status,
    mr.warehouse_id IS NULL AS no_warehouse,
    mr.quantity,
    mr.unit_cost,
    mr.total_cost,
    LEFT(p.name, 40) AS product
FROM material_reservations mr
JOIN construction_estimate_line l ON l.id = mr.estimate_line_id
JOIN construction_estimate e ON e.id = l.estimate_id
LEFT JOIN products p ON p.id = mr.product_id
WHERE e.project_id = 1
  AND COALESCE(mr.deleted_at, '1970-01-01'::timestamptz) = '1970-01-01'::timestamptz
ORDER BY mr.estimate_line_id, mr.created_at DESC
LIMIT 50;

\echo
\echo '======================================================================'
\echo 'I2 - Per-reservation status breakdown for project 1.'
\echo '     `pending` = reservation made but work not YAKUNIY-confirmed yet.'
\echo '     `approved` = work was YAKUNIY-confirmed, inventory should be decremented.'
\echo '======================================================================'
SELECT
    mr.status,
    COUNT(*) AS count,
    SUM(CASE WHEN mr.warehouse_id IS NULL THEN 1 ELSE 0 END) AS no_warehouse_count
FROM material_reservations mr
JOIN construction_estimate_line l ON l.id = mr.estimate_line_id
JOIN construction_estimate e ON e.id = l.estimate_id
WHERE e.project_id = 1
GROUP BY mr.status
ORDER BY mr.status;

\echo
\echo '======================================================================'
\echo 'I3 - Inventory rows for products tied to project 1 reservations.'
\echo '     Check whether quantity_on_hand has gone negative (proves decrement ran)'
\echo '     or whether rows simply do not exist (decrement was skipped because'
\echo '     warehouse_id was NULL on the reservation).'
\echo '======================================================================'
SELECT
    LEFT(p.name, 40) AS product,
    inv.quantity_on_hand AS on_hand,
    inv.quantity_reserved AS reserved,
    inv.warehouse_id
FROM products p
JOIN material_reservations mr ON mr.product_id = p.id
JOIN construction_estimate_line l ON l.id = mr.estimate_line_id
JOIN construction_estimate e ON e.id = l.estimate_id
LEFT JOIN inventory inv ON inv.product_id = p.id AND inv.tenant_id = p.tenant_id
WHERE e.project_id = 1
  AND mr.status = 'approved'
GROUP BY p.id, p.name, inv.quantity_on_hand, inv.quantity_reserved, inv.warehouse_id
ORDER BY inv.quantity_on_hand NULLS LAST
LIMIT 30;
