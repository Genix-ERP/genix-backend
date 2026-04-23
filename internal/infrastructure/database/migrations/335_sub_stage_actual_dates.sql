-- 335_sub_stage_actual_dates.sql
-- Give sub-stages their own actual_start / actual_end dates.
--
-- Until now sub-stages inherited the parent stage's actual dates at query
-- time (see GetProjectInProgressItems). That made the "Haqiqiy boshlanish"
-- column on the Jarayon tab show the stage's date for every sub-stage under
-- it, and there was no way for the UI to record when an individual
-- sub-stage actually started. The stage already has these columns (see
-- migration 156). Mirror them on construction_sub_stages so the status
-- update handler can stamp CURRENT_DATE when a sub-stage transitions into
-- in_progress / completed.

ALTER TABLE construction_sub_stages
    ADD COLUMN IF NOT EXISTS actual_start DATE,
    ADD COLUMN IF NOT EXISTS actual_end   DATE;
