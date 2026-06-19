-- Migration 423: track upfront/partial payment on a sales order directly.
--
-- The CRM "create customer + order" flow lets a customer pay part of the total
-- upfront and the rest after the product is finished. sales_orders previously
-- tracked only payment_status (unpaid/partial/paid) with no amount, so we add an
-- explicit paid_amount; remaining = total_amount - paid_amount.
ALTER TABLE sales_orders
    ADD COLUMN IF NOT EXISTS paid_amount DECIMAL(20, 4) NOT NULL DEFAULT 0;
