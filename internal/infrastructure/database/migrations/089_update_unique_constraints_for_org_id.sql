-- Migration 087: Update unique constraints to include organization_id
-- Allows the same codes to exist in different organizations within the same tenant

-- product_categories: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE product_categories DROP CONSTRAINT IF EXISTS product_categories_tenant_id_code_key;
ALTER TABLE product_categories ADD CONSTRAINT product_categories_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- products: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_tenant_id_code_key;
ALTER TABLE products ADD CONSTRAINT products_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- product_boms: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE product_boms DROP CONSTRAINT IF EXISTS product_boms_tenant_id_code_key;
ALTER TABLE product_boms ADD CONSTRAINT product_boms_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- production_orders: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE production_orders DROP CONSTRAINT IF EXISTS production_orders_tenant_id_code_key;
ALTER TABLE production_orders ADD CONSTRAINT production_orders_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- work_centers: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE work_centers DROP CONSTRAINT IF EXISTS work_centers_tenant_id_code_key;
ALTER TABLE work_centers ADD CONSTRAINT work_centers_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- work_orders: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE work_orders DROP CONSTRAINT IF EXISTS work_orders_tenant_id_code_key;
ALTER TABLE work_orders ADD CONSTRAINT work_orders_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- quality_checks: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE quality_checks DROP CONSTRAINT IF EXISTS quality_checks_tenant_id_code_key;
ALTER TABLE quality_checks ADD CONSTRAINT quality_checks_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- opportunities: (tenant_id, code) -> (tenant_id, organization_id, code)
ALTER TABLE opportunities DROP CONSTRAINT IF EXISTS opportunities_tenant_id_code_key;
ALTER TABLE opportunities ADD CONSTRAINT opportunities_tenant_org_code_key UNIQUE (tenant_id, organization_id, code);

-- rfqs: (tenant_id, rfq_number) -> (tenant_id, organization_id, rfq_number)
ALTER TABLE rfqs DROP CONSTRAINT IF EXISTS rfqs_tenant_id_rfq_number_key;
ALTER TABLE rfqs ADD CONSTRAINT rfqs_tenant_org_rfq_number_key UNIQUE (tenant_id, organization_id, rfq_number);

-- procurement_contracts: (tenant_id, contract_number) -> (tenant_id, organization_id, contract_number)
ALTER TABLE procurement_contracts DROP CONSTRAINT IF EXISTS procurement_contracts_tenant_id_contract_number_key;
ALTER TABLE procurement_contracts ADD CONSTRAINT procurement_contracts_tenant_org_contract_number_key UNIQUE (tenant_id, organization_id, contract_number);

-- contracts: (tenant_id, contract_number) -> (tenant_id, organization_id, contract_number)
ALTER TABLE contracts DROP CONSTRAINT IF EXISTS contracts_tenant_id_contract_number_key;
ALTER TABLE contracts ADD CONSTRAINT contracts_tenant_org_contract_number_key UNIQUE (tenant_id, organization_id, contract_number);

-- scrap_orders: (tenant_id, scrap_number) -> (tenant_id, organization_id, scrap_number)
ALTER TABLE scrap_orders DROP CONSTRAINT IF EXISTS scrap_orders_tenant_id_scrap_number_key;
ALTER TABLE scrap_orders ADD CONSTRAINT scrap_orders_tenant_org_scrap_number_key UNIQUE (tenant_id, organization_id, scrap_number);

-- reorder_rules: (tenant_id, product_id, warehouse_id) -> keep as-is (warehouse is already org-scoped)
-- warehouse_operation_types: (warehouse_id, code) -> keep as-is (warehouse is already org-scoped)
-- inventory: (tenant_id, product_id, warehouse_id, location_id, lot_number, serial_number) -> keep as-is (warehouse is already org-scoped)
