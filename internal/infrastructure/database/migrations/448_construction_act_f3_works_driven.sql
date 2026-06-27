-- 444_construction_act_f3_works_driven.sql
--
-- The works-driven Forma 3 (КС-3) generator builds the certificate directly
-- from engineer-confirmed (YAKUNIY) works rather than from signed КС-2 act
-- lines. When such a Forma 3 is generated we persist a lightweight act record
-- so it shows up in the Hujjatlar → Formalar list, but the record carries NO
-- act lines — its content is re-derived from the live works on every export.
--
-- This flag marks those records so ExportActDocument knows to regenerate via
-- loadForma3FromWorks(subcontract_id, period) instead of rendering stored
-- act lines.

ALTER TABLE construction_act
    ADD COLUMN IF NOT EXISTS f3_works_driven BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN construction_act.f3_works_driven IS
    'TRUE for КС-3 records produced by the works-driven Forma 3 generator. '
    'Such records have no act lines; their xlsx is re-derived from the current '
    'engineer-confirmed works for the stored subcontract + reporting period.';
