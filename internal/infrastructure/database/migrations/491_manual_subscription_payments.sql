-- Subscription payments that did not come through Multicard.
--
-- The table was built for one payment route: invoice_id is the id we send to
-- Multicard, card_pan and ps describe the card, checkout_url is theirs. A
-- customer who pays in cash — which is how a good share of them pay — leaves
-- no trace at all. The subscription gets extended by hand through
-- ActivateTenantSubscription (whose comment already says "cash payment"), and
-- the money that bought it is recorded nowhere.
--
-- So there is no answer to "when did this company last pay, and how much",
-- except for the ones who happened to use a card.
--
-- Three columns fix that without disturbing the Multicard path:
--
--   method       how the money arrived. Existing rows are all Multicard, so
--                that is the backfill and the default.
--   note         free text — a receipt number, who handed it over, which bank
--                transfer it matches.
--   recorded_by  which platform user entered it. A cash payment is an
--                assertion by a person rather than a callback from a payment
--                provider, so the person belongs in the record.
--
-- invoice_id stays NOT NULL UNIQUE. Manual rows synthesise one (CASH-<uuid>)
-- rather than relaxing the constraint, because that column is what makes a
-- Multicard callback idempotent and it should keep doing that job.

ALTER TABLE subscription_payments
    ADD COLUMN IF NOT EXISTS method      VARCHAR(20) NOT NULL DEFAULT 'multicard',
    ADD COLUMN IF NOT EXISTS note        TEXT,
    ADD COLUMN IF NOT EXISTS recorded_by UUID;

-- Only the values the application writes. Anything else is a bug rather than
-- a new payment route, and should fail loudly at the point of insert.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'subscription_payments_method_check'
    ) THEN
        ALTER TABLE subscription_payments
            ADD CONSTRAINT subscription_payments_method_check
            CHECK (method IN ('multicard', 'cash', 'bank_transfer', 'other'));
    END IF;
END $$;

-- The history panel reads per tenant, newest first.
CREATE INDEX IF NOT EXISTS idx_sub_payments_tenant_created
    ON subscription_payments (tenant_id, created_at DESC);
