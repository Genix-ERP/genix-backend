-- Backfill finished-goods inventory for production orders that completed but
-- never created an inventory receipt. Pre-fix code inserted into
-- inventory_lots.purchase_order_id with the production_order id, which failed
-- an FK check against purchase_orders and aborted the whole transaction —
-- so the inventory row and the receipt transaction were rolled back too,
-- and the produced goods never made it into the warehouse.
--
-- For each completed production order with no receipt transaction yet,
-- this inserts (or updates) an inventory row and a receipt transaction
-- using quantity_produced (or quantity_planned as a fallback) and the
-- product's current cost_price. If there is no warehouse on the PO, it
-- picks the tenant's oldest warehouse.

DO $$
DECLARE
    rec RECORD;
    inv_id UUID;
    qty NUMERIC;
    unit_cost NUMERIC;
    wh_id UUID;
BEGIN
    FOR rec IN
        SELECT po.id, po.tenant_id, po.organization_id, po.product_id,
               po.warehouse_id, po.quantity_produced, po.quantity_planned
        FROM production_orders po
        WHERE po.status = 'completed'
          AND po.deleted_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM inventory_transactions it
              WHERE it.tenant_id = po.tenant_id
                AND it.reference_type = 'production_order'
                AND it.reference_id = po.id
                AND it.transaction_type = 'receipt'
          )
    LOOP
        qty := COALESCE(NULLIF(rec.quantity_produced, 0), rec.quantity_planned);
        IF qty IS NULL OR qty <= 0 THEN
            CONTINUE;
        END IF;

        SELECT COALESCE(cost_price, list_price, 0) INTO unit_cost
        FROM products WHERE id = rec.product_id;
        unit_cost := COALESCE(unit_cost, 0);

        wh_id := rec.warehouse_id;
        IF wh_id IS NULL THEN
            SELECT id INTO wh_id
            FROM warehouses
            WHERE tenant_id = rec.tenant_id AND deleted_at IS NULL
            ORDER BY created_at ASC LIMIT 1;
        END IF;

        IF wh_id IS NULL THEN
            CONTINUE; -- tenant has no warehouses; skip
        END IF;

        SELECT id INTO inv_id
        FROM inventory
        WHERE tenant_id = rec.tenant_id
          AND product_id = rec.product_id
          AND warehouse_id = wh_id
          AND lot_number IS NULL
          AND serial_number IS NULL
        LIMIT 1;

        IF inv_id IS NULL THEN
            inv_id := gen_random_uuid();
            INSERT INTO inventory (
                id, tenant_id, organization_id, product_id, warehouse_id,
                quantity_on_hand, quantity_reserved, unit_cost,
                last_movement_date, created_at, updated_at
            ) VALUES (
                inv_id, rec.tenant_id, rec.organization_id, rec.product_id, wh_id,
                qty, 0, unit_cost, NOW(), NOW(), NOW()
            );
        ELSE
            UPDATE inventory
            SET quantity_on_hand = quantity_on_hand + qty,
                last_movement_date = NOW(),
                updated_at = NOW()
            WHERE id = inv_id;
        END IF;

        INSERT INTO inventory_transactions (
            id, tenant_id, organization_id, inventory_id, transaction_type,
            reference_type, reference_id, quantity, unit_cost, total_cost,
            reason, notes, transaction_date, created_at
        ) VALUES (
            gen_random_uuid(), rec.tenant_id, rec.organization_id, inv_id, 'receipt',
            'production_order', rec.id, qty, unit_cost, qty * unit_cost,
            'production_complete',
            'Backfilled by migration 347 (original receipt lost to FK violation on inventory_lots.purchase_order_id)',
            NOW(), NOW()
        );
    END LOOP;
END $$;
