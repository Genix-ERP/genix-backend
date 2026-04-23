-- 342_estimate_line_quantity_override.sql
-- Adds a `quantity_override` flag to construction_estimate_line so a sub-line
-- (подкатор) can carry a USER-ENTERED quantity that is decoupled from the
-- parent smeta line's volume × norm_rate auto-derivation.
--
-- Background / Why:
--   Migration 332 introduced sub-lines where quantity is denormalized as
--   `parent.quantity × norm_rate`. This works for pure ШРНК-style normative
--   estimates but breaks down in practice: rentals book whole shifts, pumps
--   sit idle, foremen know a machine needs exactly 10 hours independent of
--   what the parent volume × norm would produce.
--
--   The user's teammate raised this: "If ten hours of work are needed, we
--   need to take the time of the machine that needs to work ten hours,
--   calculate it separately without adding it onto the other volume."
--
-- Behaviour:
--   - quantity_override = FALSE (default, existing behaviour): backend keeps
--     computing quantity = parent.quantity × norm_rate on create/update.
--   - quantity_override = TRUE: backend stores the user-supplied Quantity
--     verbatim. norm_rate stays persisted for reference but is no longer
--     used to derive the sub-line quantity.

ALTER TABLE construction_estimate_line
    ADD COLUMN IF NOT EXISTS quantity_override BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN construction_estimate_line.quantity_override IS
    'When TRUE on a sub-line, quantity is user-entered and must not be re-derived from parent.quantity × norm_rate.';
