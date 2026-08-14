-- 507: repair the auto-created opening layers and surface the AVCO average.
--
-- ensureOpeningLayer (valuation_hook.go) documented that it valued the balance
-- BEFORE the movement being recorded — "the caller has already applied its
-- delta, so subtract it back out" — but the subtraction was never written.
-- Consequences, present in every database that ran the hook:
--
--   * A brand-new product's FIRST receipt was double-counted: stock was
--     already 10 when the hook looked, so it wrote a 10-unit "opening" layer
--     AND the 10-unit receipt layer. Layers said 20, the shelf held 10.
--   * A legacy product whose first post-deploy movement was a SALE got the
--     opposite: the opening layer was created at the post-sale balance and
--     then drained by that same sale, leaving Σ remaining one sale short of
--     the shelf.
--
-- Either way the §1.3 invariant (Σ remaining = physical stock value) was
-- broken from the product's first movement, and the phantom layer sat oldest
-- in every FIFO queue. The hook is fixed in the same release; this migration
-- repairs the rows it already wrote.
--
-- The repair adjusts ONLY the auto opening layer ('opening_balance'/'AUTO' —
-- at most one per product by construction) by exactly the drift between
-- physical stock and Σ open layers, at the layer's own unit cost. Only the
-- remaining_* pair moves: quantity/value keep the originally-recorded figures
-- (they are the audit trail of what the buggy hook wrote, and the table's
-- checks require quantity > 0 and remaining_qty <= quantity — a fully-phantom
-- layer simply ends at remaining 0, which every consumer already skips).
-- Products with no AUTO layer are untouched: their layers predate the hook
-- (migration 500 seeded them) or never existed.
--
-- Note: products whose stock also moves through paths that deliberately skip
-- valuation (manufactured goods — production_in creates no layer yet) get
-- their drift folded into the opening layer at its unit cost. That keeps the
-- invariant honest at an approximate value until production costing lands
-- (plan §8, v2).

WITH phys AS (
    SELECT tenant_id, product_id, SUM(quantity_on_hand) AS qty
    FROM inventory
    GROUP BY tenant_id, product_id
),
open_sum AS (
    SELECT tenant_id, product_id, SUM(remaining_qty) AS rq
    FROM stock_valuation_layers
    WHERE NOT is_reversed
    GROUP BY tenant_id, product_id
),
drift AS (
    SELECT o.tenant_id, o.product_id, COALESCE(p.qty, 0) - o.rq AS delta
    FROM open_sum o
    LEFT JOIN phys p USING (tenant_id, product_id)
    WHERE ABS(COALESCE(p.qty, 0) - o.rq) > 0.0001
)
UPDATE stock_valuation_layers l
SET remaining_qty   = LEAST(GREATEST(l.remaining_qty + d.delta, 0), l.quantity),
    remaining_value = ROUND((LEAST(GREATEST(l.remaining_qty + d.delta, 0), l.quantity) * l.unit_cost)::numeric, 2)
FROM drift d
WHERE l.tenant_id = d.tenant_id
  AND l.product_id = d.product_id
  AND l.source_type = 'opening_balance'
  AND l.source_doc_number = 'AUTO'
  AND NOT l.is_reversed;

-- Rebuild the AVCO running state from the repaired layers. By design the two
-- agree (recordIssue rescales layers back to avco_value; the service test
-- asserts it), so for healthy products this is a no-op; for products whose
-- opening stock never reached the state — ensureOpeningLayer did not seed it
-- until this release — it finally folds the old stock into the average.
-- State rows for FIFO/standard products are written too and are harmless:
-- the state is only ever read when the effective method is AVCO.
INSERT INTO product_avco_state (tenant_id, organization_id, product_id, quantity, value, last_unit_cost, updated_at)
SELECT l.tenant_id,
       COALESCE(l.organization_id, '00000000-0000-0000-0000-000000000000'::uuid),
       l.product_id,
       SUM(l.remaining_qty),
       SUM(l.remaining_value),
       CASE WHEN SUM(l.remaining_qty) > 0 THEN SUM(l.remaining_value) / SUM(l.remaining_qty) ELSE 0 END,
       NOW()
FROM stock_valuation_layers l
WHERE NOT l.is_reversed
GROUP BY l.tenant_id, COALESCE(l.organization_id, '00000000-0000-0000-0000-000000000000'::uuid), l.product_id
ON CONFLICT (tenant_id, organization_id, product_id) DO UPDATE
SET quantity = EXCLUDED.quantity,
    value    = EXCLUDED.value,
    last_unit_cost = CASE WHEN EXCLUDED.quantity > 0 THEN EXCLUDED.last_unit_cost
                          ELSE product_avco_state.last_unit_cost END,
    updated_at = NOW();

-- Finally: put the average where the user actually looks. Every receipt path
-- used to overwrite products.cost_price with the OLDEST open lot (hard-coded
-- FIFO display), so an AVCO product's card froze on its first purchase price —
-- "tan narxni o'rtachasini hisoblamayapti". The handlers now write the
-- method-correct figure on every movement; this backfills products that
-- already had their receipts. Effective method mirrors EffectiveCostMethod:
-- category override first, then the tenant policy (default 'aveco' = AVCO).
WITH avg_state AS (
    SELECT tenant_id, product_id, SUM(quantity) AS qty, SUM(value) AS value
    FROM product_avco_state
    GROUP BY tenant_id, product_id
    HAVING SUM(quantity) > 0 AND SUM(value) > 0
),
avco_products AS (
    SELECT p.id, p.tenant_id, s.value / s.qty AS avg_cost
    FROM products p
    JOIN avg_state s ON s.tenant_id = p.tenant_id AND s.product_id = p.id
    LEFT JOIN product_categories pc ON pc.id = p.category_id
    LEFT JOIN tenant_settings ts ON ts.tenant_id = p.tenant_id
    WHERE CASE
        WHEN LOWER(COALESCE(pc.cost_method, '')) IN ('avco','aveco','average','weighted_average') THEN 'avco'
        WHEN LOWER(COALESCE(pc.cost_method, '')) = 'fifo' THEN 'fifo'
        WHEN LOWER(COALESCE(pc.cost_method, '')) IN ('standard','standard_price') THEN 'standard'
        WHEN LOWER(COALESCE(ts.settings->'inventory_valuation'->>'method', 'aveco')) = 'fifo' THEN 'fifo'
        ELSE 'avco'
    END = 'avco'
)
UPDATE products p
SET cost_price = ap.avg_cost, updated_at = NOW()
FROM avco_products ap
WHERE p.id = ap.id AND p.tenant_id = ap.tenant_id
  AND ABS(COALESCE(p.cost_price, 0) - ap.avg_cost) > 0.0001;

-- The product list prefers product_organization_settings.cost_price when it is
-- non-zero, so a stale per-org figure would keep hiding the repaired average.
WITH org_state AS (
    SELECT s.tenant_id, s.organization_id, s.product_id, s.value / s.quantity AS avg_cost
    FROM product_avco_state s
    WHERE s.quantity > 0 AND s.value > 0
      AND s.organization_id <> '00000000-0000-0000-0000-000000000000'::uuid
),
avco_org AS (
    SELECT o.*
    FROM org_state o
    JOIN products p ON p.id = o.product_id AND p.tenant_id = o.tenant_id
    LEFT JOIN product_categories pc ON pc.id = p.category_id
    LEFT JOIN tenant_settings ts ON ts.tenant_id = p.tenant_id
    WHERE CASE
        WHEN LOWER(COALESCE(pc.cost_method, '')) IN ('avco','aveco','average','weighted_average') THEN 'avco'
        WHEN LOWER(COALESCE(pc.cost_method, '')) = 'fifo' THEN 'fifo'
        WHEN LOWER(COALESCE(pc.cost_method, '')) IN ('standard','standard_price') THEN 'standard'
        WHEN LOWER(COALESCE(ts.settings->'inventory_valuation'->>'method', 'aveco')) = 'fifo' THEN 'fifo'
        ELSE 'avco'
    END = 'avco'
)
UPDATE product_organization_settings pos
SET cost_price = ao.avg_cost, updated_at = NOW()
FROM avco_org ao
WHERE pos.product_id = ao.product_id
  AND pos.organization_id = ao.organization_id
  AND COALESCE(pos.cost_price, 0) > 0
  AND ABS(pos.cost_price - ao.avg_cost) > 0.0001;
