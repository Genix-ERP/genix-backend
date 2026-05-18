-- Add cost_method to work_centers: 'capacity' (default) or 'time'
-- capacity: unit cost = hourly_cost / capacity_per_hour (fixed, high-volume)
-- time: unit cost = hourly_cost * actual_hours / qty_produced (variable, custom work)
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS cost_method VARCHAR(10) NOT NULL DEFAULT 'capacity';
