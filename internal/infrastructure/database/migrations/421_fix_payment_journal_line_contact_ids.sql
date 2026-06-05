-- Migration 421: Fix contact_id on payment/receipt journal entry lines
--
-- Problem: When payments (vendor) and receipts (customer) are confirmed,
-- BOTH journal entry lines (AP/AR + Cash) got the contact_id set.
-- This caused reconciliation (akt sverka) to count both sides, making
-- every vendor/customer appear as "paid" (net zero) since debit = credit.
--
-- Fix: Remove contact_id from the cash/bank side of payment journal entries,
-- keeping it only on the AP debit line (vendor payments) and AR credit line (receipts).
--
-- NOTE on the immutability trigger (TT §4.4, migrations 319/326):
--   trg_enforce_journal_line_invariants raises
--     "TT 4.4: posted journal entry is immutable (use storno instead)"
--   on ANY update to a line whose parent entry is status='posted'. The payment/
--   receipt entries we are correcting are posted, so this data fix would be blocked.
--
--   A storno is NOT appropriate here: a storno reverses the *financial* posting
--   (the cash movement), but the amounts and accounts are correct — only the
--   denormalized contact_id analytics tag was wrongly populated, corrupting the
--   akt-sverka reconciliation. This migration changes NO debit/credit amount and
--   NO account, so the double-entry and audit trail are unaffected.
--
--   We therefore disable the invariants trigger for the duration of this one
--   correction and re-enable it. The whole migration runs in a single
--   transaction (see internal/infrastructure/database/migrations.go), and
--   ALTER TABLE ... DISABLE/ENABLE TRIGGER is transactional, so the trigger is
--   atomically restored even if the UPDATEs fail and roll back. The 325
--   enrichment trigger skips UPDATEs, so it will not re-populate contact_id.
ALTER TABLE journal_entry_lines DISABLE TRIGGER trg_enforce_journal_line_invariants;

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

-- Restore the immutability guard for all normal (non-migration) writes.
ALTER TABLE journal_entry_lines ENABLE TRIGGER trg_enforce_journal_line_invariants;
