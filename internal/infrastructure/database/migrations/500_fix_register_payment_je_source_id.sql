-- 500: RegisterPartnerPayment JEs carried the CONTACT id as source_id.
--
-- POST /payments/register wrote journal_entries.source_id = contact_id instead
-- of the payment id. That broke JE→payment tracing and made the
-- DUPLICATE_SOURCE diagnostic flag any partner with two registered payments as
-- a critical anomaly. The handler is fixed; this repairs the rows it wrote.
--
-- Predicate is the bug's exact signature — source_id equals the linked
-- payment's contact_id (linkage via payments.journal_entry_id, which the
-- handler has always set). It must NOT be widened to "source_id != payment
-- id": sales-side RecordPayment legitimately writes payment_receipt JEs with
-- source_id = the settled INVOICE id (the mixed-source convention), and the
-- Akt sverka enrichment (enrichReconciliationLines) resolves invoice lines
-- through exactly those ids — re-pointing them would strip its line items.
-- (source_id is header metadata, not a posted amount, so the TT 4.4
-- line-immutability trigger does not apply.)
UPDATE journal_entries je
SET source_id = p.id,
    updated_at = NOW()
FROM payments p
WHERE p.journal_entry_id = je.id
  AND p.tenant_id = je.tenant_id
  AND je.source_type IN ('payment', 'payment_receipt')
  AND je.source_id = p.contact_id;
