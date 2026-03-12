-- Add 'awaiting_approval' state to stock_operations state constraint
-- This allows stock operations to pause when a step requires approval
ALTER TABLE stock_operations DROP CONSTRAINT IF EXISTS stock_operations_state_check;
ALTER TABLE stock_operations ADD CONSTRAINT stock_operations_state_check
    CHECK (state IN ('draft','in_progress','waiting','awaiting_approval','done','cancelled'));
