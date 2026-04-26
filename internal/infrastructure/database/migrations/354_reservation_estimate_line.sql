-- 354_reservation_estimate_line.sql
--
-- Lets a material_reservation attach directly to a `construction_estimate_line`
-- (one work in the v2 Bosqichlar tab) instead of always going through the
-- legacy stage_id / substage_id route.
--
-- The v2 Bosqichlar / StagesTabV2 flow is:
--   • Foreman types BAJARILDI on a work line.
--   • Foreman presses "Tekshiruvga yuborish" → SubmitWork.
--     Backend now also creates one pending material_reservation per material
--     sub-line of that work, sized as `done_quantity × subline.norm_rate`,
--     against any tenant warehouse that has stock for the resolved product.
--   • Chief engineer confirms → ConfirmWorkEngineer.
--     Backend approves every reservation tied to that estimate_line_id,
--     decrementing quantity_on_hand. Stock IS allowed to go negative —
--     procurement refills it later (per product feedback: "minus bo'lsa,
--     keyin to'ldirib qo'yishadi").
--   • Supervisor rejects → reservations are cancelled and quantity_reserved
--     is released so the warehouse balance returns to its pre-submit value.
--
-- The original `material_reservations` schema (migration 310) made stage_id
-- and substage_id NOT NULL. The estimate-line flow has neither, so we relax
-- both and add a new estimate_line_id pointer.

ALTER TABLE material_reservations
    ADD COLUMN IF NOT EXISTS estimate_line_id BIGINT;

ALTER TABLE material_reservations
    ALTER COLUMN stage_id DROP NOT NULL;

ALTER TABLE material_reservations
    ALTER COLUMN substage_id DROP NOT NULL;

CREATE INDEX IF NOT EXISTS idx_material_reservations_estimate_line
    ON material_reservations(estimate_line_id);

COMMENT ON COLUMN material_reservations.estimate_line_id IS
    'When set, this reservation belongs to one work line in the Bosqichlar (StagesTabV2) flow. Engineer confirmation of that work approves the reservation.';
