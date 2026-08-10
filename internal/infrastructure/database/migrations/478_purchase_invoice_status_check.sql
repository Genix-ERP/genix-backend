-- D1: pin the purchase-invoice status vocabulary in the schema.
--
-- THE DECISION
-- 'pending_approval' and 'rejected' were in the client contract but nowhere in
-- the backend: nothing writes them and nothing reads them. So there was no
-- approval workflow to repair — only one to invent. Building it would mean
-- deciding who approves, what a rejected invoice may become, and whether
-- posting still requires approval; those are business rules, not a gap to
-- backfill. The two statuses come out of the contract instead, and this records
-- what the code actually does so the contract cannot quietly drift back.
--
-- The complete writable set, verified against every writer in the repo:
--   draft      — INSERT default (purchase_invoices.go, ai_agent_tools.go)
--   confirmed  — ConfirmPurchaseInvoice
--   posted     — PostPurchaseInvoice
--   partial    — payment CASE, amount_paid > 0
--   paid       — payment CASE, amount_paid >= total_amount
--   cancelled  — cancel path
-- The payment CASE's ELSE preserves the current value, so it cannot introduce a
-- seventh.
--
-- NOT VALID on purpose. It makes Postgres skip the initial full-table scan, so
-- the migration cannot fail on a legacy row carrying some other value — and it
-- still ENFORCES the constraint on every INSERT and UPDATE from here on, which
-- is the whole point. Any pre-existing oddity stays readable and surfaces as a
-- failure only if something tries to rewrite it.
--
-- To adopt it retroactively once the data is known clean:
--   ALTER TABLE purchase_invoices VALIDATE CONSTRAINT purchase_invoices_status_check;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'purchase_invoices_status_check'
          AND conrelid = 'purchase_invoices'::regclass
    ) THEN
        ALTER TABLE purchase_invoices
            ADD CONSTRAINT purchase_invoices_status_check
            CHECK (status IN ('draft', 'confirmed', 'posted', 'partial', 'paid', 'cancelled'))
            NOT VALID;
    END IF;
END $$;
