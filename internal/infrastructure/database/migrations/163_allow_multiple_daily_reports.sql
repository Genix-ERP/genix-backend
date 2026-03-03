-- Allow multiple daily reports per day per project
ALTER TABLE construction_daily_reports DROP CONSTRAINT IF EXISTS construction_daily_reports_project_id_report_date_key;
