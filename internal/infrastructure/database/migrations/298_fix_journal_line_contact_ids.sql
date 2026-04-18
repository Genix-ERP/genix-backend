-- Migration: 298_fix_journal_line_contact_ids.sql
-- Description: Fix journal entry lines that incorrectly have contact_id on non-AP/AR lines.
-- The reconciliation act query uses contact_id to find partner transactions,
-- so only the AP (payable) or AR (receivable) lines should have contact_id set.
-- Expense, cash, write-off, and discount lines should NOT have contact_id.
-- Date: 2026-04-04

-- Fix purchase invoice journal entries:
-- Remove contact_id from DEBIT lines (expense/stock interim), keep on CREDIT lines (AP)
UPDATE journal_entry_lines jel
SET contact_id = NULL
FROM journal_entries je
WHERE jel.journal_entry_id = je.id
  AND je.source_type = 'purchase_invoice'
  AND jel.debit_amount > 0
  AND jel.credit_amount = 0
  AND jel.contact_id IS NOT NULL;

-- Fix sales invoice payment journal entries:
-- Remove contact_id from non-AR lines (cash debit, write-off, discount lines)
-- Keep contact_id only on the AR credit line
UPDATE journal_entry_lines jel
SET contact_id = NULL
FROM journal_entries je
WHERE jel.journal_entry_id = je.id
  AND je.source_type = 'payment_receipt'
  AND jel.debit_amount > 0
  AND jel.credit_amount = 0
  AND jel.contact_id IS NOT NULL
  AND jel.description != 'Accounts Receivable';
