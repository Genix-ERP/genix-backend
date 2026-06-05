-- Migration 304: Fix contact_id on payment/receipt journal entry lines
--
-- Problem: When payments (vendor) and receipts (customer) are confirmed,
-- BOTH journal entry lines (AP/AR + Cash) got the contact_id set.
-- This caused reconciliation (akt sverka) to count both sides, making
-- every vendor/customer appear as "paid" (net zero) since debit = credit.
--
-- Fix: Remove contact_id from the cash/bank side of payment journal entries,
-- keeping it only on the AP debit line (vendor payments) and AR credit line (receipts).

-- For vendor payments (source_type = 'payment'):
-- The AP debit line is on an AP-type account (keep contact_id).
-- Remove contact_id from the credit (cash) line — it's NOT on AP.
UPDATE journal_entry_lines jel
SET contact_id = NULL
FROM journal_entries je
WHERE jel.journal_entry_id = je.id
  AND je.source_type = 'payment'
  AND jel.contact_id IS NOT NULL
  AND jel.credit_amount > 0
  AND jel.debit_amount = 0;

-- For customer receipts (source_type = 'payment_receipt'):
-- The AR credit line is on an AR-type account (keep contact_id).
-- Remove contact_id from the debit (cash) line — it's NOT on AR.
UPDATE journal_entry_lines jel
SET contact_id = NULL
FROM journal_entries je
WHERE jel.journal_entry_id = je.id
  AND je.source_type = 'payment_receipt'
  AND jel.contact_id IS NOT NULL
  AND jel.debit_amount > 0
  AND jel.credit_amount = 0;
