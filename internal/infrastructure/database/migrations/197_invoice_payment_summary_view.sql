-- Migration 197: Create invoice_payment_summary views
-- These views calculate paid/pending/residual amounts from confirmed/draft payment allocations

-- Sales invoice payment summary
CREATE OR REPLACE VIEW sales_invoice_payment_summary AS
SELECT
    si.id AS invoice_id,
    si.tenant_id,
    si.total_amount,
    COALESCE(SUM(CASE WHEN p.status = 'confirmed'
                      THEN pa.amount ELSE 0 END), 0) AS amount_paid,
    COALESCE(SUM(CASE WHEN p.status = 'draft'
                      THEN pa.amount ELSE 0 END), 0) AS amount_pending,
    si.total_amount - COALESCE(SUM(CASE WHEN p.status = 'confirmed'
                                        THEN pa.amount ELSE 0 END), 0) AS amount_residual
FROM sales_invoices si
LEFT JOIN payment_allocations pa ON pa.document_id = si.id AND pa.document_type = 'sales_invoice'
LEFT JOIN payments p ON p.id = pa.payment_id AND p.deleted_at IS NULL
WHERE si.deleted_at IS NULL
GROUP BY si.id, si.tenant_id, si.total_amount;

-- Purchase invoice payment summary
CREATE OR REPLACE VIEW purchase_invoice_payment_summary AS
SELECT
    pi.id AS invoice_id,
    pi.tenant_id,
    pi.total_amount,
    COALESCE(SUM(CASE WHEN p.status = 'confirmed'
                      THEN pa.amount ELSE 0 END), 0) AS amount_paid,
    COALESCE(SUM(CASE WHEN p.status = 'draft'
                      THEN pa.amount ELSE 0 END), 0) AS amount_pending,
    pi.total_amount - COALESCE(SUM(CASE WHEN p.status = 'confirmed'
                                        THEN pa.amount ELSE 0 END), 0) AS amount_residual
FROM purchase_invoices pi
LEFT JOIN payment_allocations pa ON pa.document_id = pi.id AND pa.document_type = 'purchase_invoice'
LEFT JOIN payments p ON p.id = pa.payment_id AND p.deleted_at IS NULL
WHERE pi.deleted_at IS NULL
GROUP BY pi.id, pi.tenant_id, pi.total_amount;

COMMENT ON VIEW sales_invoice_payment_summary IS 'Calculates paid (confirmed), pending (draft), and residual amounts per sales invoice';
COMMENT ON VIEW purchase_invoice_payment_summary IS 'Calculates paid (confirmed), pending (draft), and residual amounts per purchase invoice';
