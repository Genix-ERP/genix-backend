-- Run this script as postgres superuser to fix table permissions
-- Usage: psql -U postgres -d YOUR_DATABASE_NAME -f fix_procurement_permissions.sql
-- Replace YOUR_APP_USER with your application's database user (e.g., genix)

-- Change ownership of goods_receipts tables
ALTER TABLE IF EXISTS goods_receipts OWNER TO :app_user;
ALTER TABLE IF EXISTS goods_receipt_lines OWNER TO :app_user;

-- Change ownership of purchase_requisitions tables
ALTER TABLE IF EXISTS purchase_requisitions OWNER TO :app_user;
ALTER TABLE IF EXISTS purchase_requisition_lines OWNER TO :app_user;

-- Change ownership of purchase_returns tables
ALTER TABLE IF EXISTS purchase_returns OWNER TO :app_user;
ALTER TABLE IF EXISTS purchase_return_lines OWNER TO :app_user;

-- Also grant all privileges
GRANT ALL PRIVILEGES ON TABLE goods_receipts TO :app_user;
GRANT ALL PRIVILEGES ON TABLE goods_receipt_lines TO :app_user;
GRANT ALL PRIVILEGES ON TABLE purchase_requisitions TO :app_user;
GRANT ALL PRIVILEGES ON TABLE purchase_requisition_lines TO :app_user;
GRANT ALL PRIVILEGES ON TABLE purchase_returns TO :app_user;
GRANT ALL PRIVILEGES ON TABLE purchase_return_lines TO :app_user;

-- Usage example:
-- psql -U postgres -d genixerp -v app_user=genix -f fix_procurement_permissions.sql
-- Or for production:
-- psql -U postgres -d production_db -v app_user=prod_user -f fix_procurement_permissions.sql
