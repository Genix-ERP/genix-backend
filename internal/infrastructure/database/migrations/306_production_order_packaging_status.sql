-- Add 'packaging' to production_orders status CHECK constraint
-- Required for split output flow: bulk production -> packaged products

ALTER TABLE production_orders DROP CONSTRAINT IF EXISTS production_orders_status_check;

ALTER TABLE production_orders ADD CONSTRAINT production_orders_status_check
    CHECK (status IN (
        'draft', 'confirmed', 'ready', 'in_progress',
        'paused', 'packaging', 'completed', 'cancelled', 'closed'
    ));
