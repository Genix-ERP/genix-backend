-- 349_estimate_line_original_anchors.sql
--
-- Adds original_quantity + original_unit_rate to construction_estimate_line
-- so the Smeta boshqaruvi UI can render "Reset to original" affordances
-- (per-work qty reset, per-resource price reset, project-wide reset-all).
--
-- The anchor values are set ONCE on INSERT — they capture whatever the row
-- carried at creation time. They're not user-editable after that, so the
-- reset action means "put quantity (or unit_rate) back to what it was
-- when the line was first written".
--
-- Existing rows are backfilled from their current quantity / unit_rate
-- since we don't have a real anchor for them — the migration moment is
-- treated as their "original".

ALTER TABLE construction_estimate_line
    ADD COLUMN IF NOT EXISTS original_quantity  NUMERIC(18, 6),
    ADD COLUMN IF NOT EXISTS original_unit_rate NUMERIC(20, 4);

-- Backfill: every existing row's current values become its anchor.
UPDATE construction_estimate_line
   SET original_quantity = COALESCE(original_quantity, quantity),
       original_unit_rate = COALESCE(original_unit_rate, unit_rate)
 WHERE original_quantity IS NULL OR original_unit_rate IS NULL;

-- Trigger keeps the anchor in sync for future INSERTs without forcing
-- every CreateEstimateLine code path to remember to set them. Anchors
-- are intentionally NEVER updated post-insert — that's what makes them
-- anchors. The trigger only fires on INSERT.
CREATE OR REPLACE FUNCTION construction_estimate_line_set_original_anchors()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.original_quantity IS NULL THEN
        NEW.original_quantity := NEW.quantity;
    END IF;
    IF NEW.original_unit_rate IS NULL THEN
        NEW.original_unit_rate := NEW.unit_rate;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_construction_estimate_line_original
    ON construction_estimate_line;

CREATE TRIGGER trg_construction_estimate_line_original
    BEFORE INSERT ON construction_estimate_line
    FOR EACH ROW
    EXECUTE FUNCTION construction_estimate_line_set_original_anchors();

COMMENT ON COLUMN construction_estimate_line.original_quantity IS
    'Quantity at first INSERT — used as the anchor for the "Reset to original" qty button.';
COMMENT ON COLUMN construction_estimate_line.original_unit_rate IS
    'Unit rate at first INSERT — used as the anchor for the "Reset to original" price button.';
