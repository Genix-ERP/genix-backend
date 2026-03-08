-- Migration 196: Fix empty payment references
-- Populate reference field for payments that have allocations but empty/null reference
-- Uses Uzbek reference format: "{invoice_number} uchun to'lov"

-- Fix customer payments (receipts) linked to sales invoices
UPDATE payments p
SET reference = si.invoice_number || ' uchun to''lov',
    updated_at = NOW()
FROM payment_allocations pa
JOIN sales_invoices si ON si.id = pa.document_id
WHERE pa.payment_id = p.id
  AND pa.document_type = 'sales_invoice'
  AND p.deleted_at IS NULL
  AND (p.reference IS NULL OR p.reference = '' OR p.reference = '-');

-- Fix vendor payments linked to purchase invoices
UPDATE payments p
SET reference = pi.invoice_number || ' uchun to''lov',
    updated_at = NOW()
FROM payment_allocations pa
JOIN purchase_invoices pi ON pi.id = pa.document_id
WHERE pa.payment_id = p.id
  AND pa.document_type = 'purchase_invoice'
  AND p.deleted_at IS NULL
  AND (p.reference IS NULL OR p.reference = '' OR p.reference = '-');

-- Fix payments without allocations — use contact name as reference
UPDATE payments p
SET reference = c.name || ' — to''lov',
    updated_at = NOW()
FROM contacts c
WHERE c.id = p.contact_id
  AND p.deleted_at IS NULL
  AND (p.reference IS NULL OR p.reference = '' OR p.reference = '-')
  AND NOT EXISTS (
    SELECT 1 FROM payment_allocations pa WHERE pa.payment_id = p.id
  );
