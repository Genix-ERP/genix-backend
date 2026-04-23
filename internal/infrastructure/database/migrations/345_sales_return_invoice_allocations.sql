-- Persist which customer invoices a sales return's amount should be applied to.
-- Previously, ApproveSalesReturn did a FIFO pass over the customer's unpaid
-- invoices (oldest due first). Some users want explicit control — e.g. credit
-- the return against a specific invoice, not an arbitrary earlier one.
--
-- When a row exists for a return, ApproveSalesReturn uses these allocations
-- verbatim and skips the FIFO fallback. When no rows exist, behavior is
-- unchanged (FIFO over unpaid invoices).

CREATE TABLE IF NOT EXISTS sales_return_invoice_allocations (
    id UUID PRIMARY KEY,
    sales_return_id UUID NOT NULL REFERENCES sales_returns(id) ON DELETE CASCADE,
    sales_invoice_id UUID NOT NULL REFERENCES sales_invoices(id) ON DELETE RESTRICT,
    amount NUMERIC(18, 2) NOT NULL CHECK (amount > 0),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sria_return ON sales_return_invoice_allocations(sales_return_id);
CREATE INDEX IF NOT EXISTS idx_sria_invoice ON sales_return_invoice_allocations(sales_invoice_id);
