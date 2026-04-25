-- 347_estimate_line_material_type.sql
--
-- Adds `material_type` to construction_estimate_line so the Form 2
-- (KS-2 / ВОР) preview can apply the correct transport + storage
-- overhead percentages per Госкомархитектстрой Письмо № 352/11-05
-- (31.01.2011) and ШНК 4.01.16-09 §4.6 / §5.6.
--
-- Five buckets — only relevant when resource_type='material':
--   standard   — обычные стройматериалы (transport 5%, storage 2%)
--   equipment  — оборудование              (transport 2%, storage 1.2%)
--   cable      — кабельно-проводниковая    (transport 1.5%, storage 2%)
--   metal      — металлоконструкции        (no auto overhead — by agreement)
--   import     — импортные материалы       (no auto overhead — by agreement)
--
-- Default 'standard' so existing rows keep producing the same overhead
-- numbers they did before the column existed (i.e., today's behaviour
-- if someone ran the calculator manually).

ALTER TABLE construction_estimate_line
    ADD COLUMN IF NOT EXISTS material_type VARCHAR(16) NOT NULL DEFAULT 'standard'
        CHECK (material_type IN ('standard', 'equipment', 'cable', 'metal', 'import'));

COMMENT ON COLUMN construction_estimate_line.material_type IS
    'Material classification for Form 2 overhead calc per Госкомархитектстрой 352/11-05. Only meaningful when resource_type = ''material''.';
