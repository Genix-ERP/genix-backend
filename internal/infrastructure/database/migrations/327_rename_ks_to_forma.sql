-- Migration 327: Rename KS-2 / KS-3 construction act type labels to Forma 2 / Forma 3
-- User-facing only — the internal `value` identifiers (ks2, ks3) stay unchanged
-- so any existing acts, journal entries, or API payloads keep working.
--
-- Note: construction_act_types has only a created_at column (no updated_at),
-- so we don't touch updated_at here.

UPDATE construction_act_types
SET label = 'Forma 2'
WHERE value = 'ks2' AND label = 'KS-2';

UPDATE construction_act_types
SET label = 'Forma 3'
WHERE value = 'ks3' AND label = 'KS-3';
