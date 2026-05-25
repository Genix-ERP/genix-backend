-- Backfill work_centers.hourly_cost from the breakdown components whenever
-- the stored value is 0 but the breakdown is populated. This repairs records
-- that were edited to add breakdown inputs before the update handler was
-- fixed to always recompute hourly_cost from the components.
--
-- Formula (matches backend calculateWorkCenterCosts / frontend calculatedCosts):
--   monthlyHours   = working_hours_per_day * 27   (Uzbek 6-day week)
--   annualHours    = working_hours_per_day * 365
--   depreciation   = asset_value / useful_life_years / annualHours
--   electricity    = power_kw * electricity_rate
--   maintenance    = annual_maintenance / annualHours
--   labor          = operator_monthly_salary / monthlyHours
--   hourly_cost    = depreciation + electricity + maintenance + labor + overhead_cost

UPDATE work_centers wc
SET
    hourly_cost = (
        CASE
            WHEN COALESCE(wc.asset_value, 0) > 0
                 AND COALESCE(wc.useful_life_years, 0) > 0
                 AND COALESCE(wc.working_hours_per_day, 0) > 0
            THEN wc.asset_value / wc.useful_life_years / (wc.working_hours_per_day * 365)
            ELSE 0
        END
      + COALESCE(wc.power_kw, 0) * COALESCE(wc.electricity_rate, 0)
      + CASE
            WHEN COALESCE(wc.annual_maintenance, 0) > 0
                 AND COALESCE(wc.working_hours_per_day, 0) > 0
            THEN wc.annual_maintenance / (wc.working_hours_per_day * 365)
            ELSE 0
        END
      + CASE
            WHEN COALESCE(wc.operator_monthly_salary, 0) > 0
                 AND COALESCE(wc.working_hours_per_day, 0) > 0
            THEN wc.operator_monthly_salary / (wc.working_hours_per_day * 27)
            ELSE 0
        END
      + COALESCE(wc.overhead_cost, 0)
    ),
    depreciation_per_hour = CASE
        WHEN COALESCE(wc.asset_value, 0) > 0
             AND COALESCE(wc.useful_life_years, 0) > 0
             AND COALESCE(wc.working_hours_per_day, 0) > 0
        THEN wc.asset_value / wc.useful_life_years / (wc.working_hours_per_day * 365)
        ELSE 0
    END,
    electricity_per_hour = COALESCE(wc.power_kw, 0) * COALESCE(wc.electricity_rate, 0),
    maintenance_per_hour = CASE
        WHEN COALESCE(wc.annual_maintenance, 0) > 0
             AND COALESCE(wc.working_hours_per_day, 0) > 0
        THEN wc.annual_maintenance / (wc.working_hours_per_day * 365)
        ELSE 0
    END,
    labor_per_hour = CASE
        WHEN COALESCE(wc.operator_monthly_salary, 0) > 0
             AND COALESCE(wc.working_hours_per_day, 0) > 0
        THEN wc.operator_monthly_salary / (wc.working_hours_per_day * 27)
        ELSE 0
    END
WHERE
    -- Only touch records where stored hourly_cost is 0 (or NULL)...
    COALESCE(wc.hourly_cost, 0) <= 0
    -- ...but at least one breakdown component is populated.
    AND (
        COALESCE(wc.asset_value, 0) > 0
        OR COALESCE(wc.power_kw, 0) > 0
        OR COALESCE(wc.annual_maintenance, 0) > 0
        OR COALESCE(wc.operator_monthly_salary, 0) > 0
        OR COALESCE(wc.overhead_cost, 0) > 0
    );
