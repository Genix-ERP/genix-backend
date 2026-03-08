-- Migration 195: Fix payment-invoice balances
-- Recalculate amount_paid on sales_invoices based ONLY on confirmed payment allocations
-- This fixes any invoices where draft payment amounts were incorrectly counted

-- Step 1: Recalculate sales_invoices.amount_paid from confirmed payments only
UPDATE sales_invoices si
SET amount_paid = sub.confirmed_paid,
    status = CASE
        WHEN sub.confirmed_paid >= si.total_amount THEN 'paid'
        WHEN sub.confirmed_paid > 0 THEN 'partial'
        ELSE CASE WHEN si.status IN ('paid', 'partial') THEN 'sent' ELSE si.status END
    END,
    updated_at = NOW()
FROM (
    SELECT pa.document_id AS invoice_id,
           COALESCE(SUM(pa.amount), 0) AS confirmed_paid
    FROM payment_allocations pa
    JOIN payments p ON p.id = pa.payment_id
    WHERE pa.document_type = 'sales_invoice'
      AND p.status = 'confirmed'
      AND p.deleted_at IS NULL
    GROUP BY pa.document_id
) sub
WHERE si.id = sub.invoice_id
  AND si.deleted_at IS NULL
  AND si.amount_paid != sub.confirmed_paid;

-- Step 2: Also fix invoices that have allocations but ALL payments are draft
-- (amount_paid should be 0 for these)
UPDATE sales_invoices si
SET amount_paid = 0,
    status = CASE WHEN si.status IN ('paid', 'partial') THEN 'sent' ELSE si.status END,
    updated_at = NOW()
WHERE si.id IN (
    SELECT DISTINCT pa.document_id
    FROM payment_allocations pa
    JOIN payments p ON p.id = pa.payment_id
    WHERE pa.document_type = 'sales_invoice'
      AND p.deleted_at IS NULL
    GROUP BY pa.document_id
    HAVING SUM(CASE WHEN p.status = 'confirmed' THEN pa.amount ELSE 0 END) = 0
)
AND si.amount_paid > 0
AND si.deleted_at IS NULL;

-- Step 3: Same fix for purchase_invoices
UPDATE purchase_invoices pi
SET amount_paid = sub.confirmed_paid,
    status = CASE
        WHEN sub.confirmed_paid >= pi.total_amount THEN 'paid'
        WHEN sub.confirmed_paid > 0 THEN 'partial'
        ELSE CASE WHEN pi.status IN ('paid', 'partial') THEN 'received' ELSE pi.status END
    END,
    payment_status = CASE
        WHEN sub.confirmed_paid >= pi.total_amount THEN 'paid'
        WHEN sub.confirmed_paid > 0 THEN 'partial'
        ELSE 'unpaid'
    END,
    updated_at = NOW()
FROM (
    SELECT pa.document_id AS invoice_id,
           COALESCE(SUM(pa.amount), 0) AS confirmed_paid
    FROM payment_allocations pa
    JOIN payments p ON p.id = pa.payment_id
    WHERE pa.document_type = 'purchase_invoice'
      AND p.status = 'confirmed'
      AND p.deleted_at IS NULL
    GROUP BY pa.document_id
) sub
WHERE pi.id = sub.invoice_id
  AND pi.deleted_at IS NULL
  AND pi.amount_paid != sub.confirmed_paid;
