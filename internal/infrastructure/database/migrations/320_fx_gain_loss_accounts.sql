-- Migration 320: FX gain/loss account mapping for multi-currency journal entries
-- Reference: TT Buxgalteriya ERP §4.6 — "Kurs farqlari alohida provodka bilan aks ettiriladi"
--
-- The spec text names "9540 / 9510" but the real BHMS №21 chart (migration 315) uses:
--   9540 = Valyuta kursi farqlaridan daromadlar (FX gain, passive/credit)
--   9630 = Valyuta kursi farqlaridan zararlar (FX loss,  active/debit)
-- We follow the chart-of-accounts reality; the generic CreateJournalEntry handler
-- looks up these accounts by code and falls back to 9540/9630 when present.
--
-- This migration simply asserts the accounts exist and adds an index to speed up
-- their lookup per-organization.

-- Marker indexes — safe no-op if the columns are already indexed
CREATE INDEX IF NOT EXISTS idx_accounts_code_org_active
    ON accounts (organization_id, code)
    WHERE deleted_at IS NULL AND is_active = true;

-- Soft-assert the accounts exist. We only log a warning; we do not try to
-- create them here because the organization-level seeding is done in earlier
-- migrations (315/317) and should already have run.
DO $$
DECLARE
    missing_9540 INT;
    missing_9630 INT;
BEGIN
    SELECT COUNT(*) INTO missing_9540
    FROM organizations o
    WHERE o.deleted_at IS NULL
      AND NOT EXISTS (
          SELECT 1 FROM accounts a
          WHERE a.organization_id = o.id
            AND a.code = '9540'
            AND a.deleted_at IS NULL
      );

    SELECT COUNT(*) INTO missing_9630
    FROM organizations o
    WHERE o.deleted_at IS NULL
      AND NOT EXISTS (
          SELECT 1 FROM accounts a
          WHERE a.organization_id = o.id
            AND a.code = '9630'
            AND a.deleted_at IS NULL
      );

    IF missing_9540 > 0 THEN
        RAISE WARNING 'Migration 320: % organizations missing account 9540 (FX gain). Re-run migration 315 or seed these organizations.', missing_9540;
    END IF;
    IF missing_9630 > 0 THEN
        RAISE WARNING 'Migration 320: % organizations missing account 9630 (FX loss). Re-run migration 315 or seed these organizations.', missing_9630;
    END IF;
END $$;
