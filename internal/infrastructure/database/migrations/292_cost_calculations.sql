-- Cost Calculations: pre-order price estimation with profit margin
CREATE TABLE IF NOT EXISTS cost_calculations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    product_name VARCHAR(255) NOT NULL,
    quantity DECIMAL(15,4) NOT NULL DEFAULT 1,
    profit_percent DECIMAL(5,2) NOT NULL DEFAULT 45,
    material_cost DECIMAL(15,2) NOT NULL DEFAULT 0,
    total_cost DECIMAL(15,2) NOT NULL DEFAULT 0,
    total_with_profit DECIMAL(15,2) NOT NULL DEFAULT 0,
    notes TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS cost_calculation_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    calculation_id UUID NOT NULL REFERENCES cost_calculations(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID REFERENCES products(id) ON DELETE SET NULL,
    product_name VARCHAR(255) NOT NULL,
    quantity DECIMAL(15,4) NOT NULL DEFAULT 1,
    unit VARCHAR(50) DEFAULT 'pcs',
    unit_cost DECIMAL(15,4) NOT NULL DEFAULT 0,
    total_cost DECIMAL(15,4) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cost_calc_tenant ON cost_calculations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_cost_calc_org ON cost_calculations(organization_id);
CREATE INDEX IF NOT EXISTS idx_cost_calc_deleted ON cost_calculations(deleted_at);
CREATE INDEX IF NOT EXISTS idx_cost_calc_lines_calc ON cost_calculation_lines(calculation_id);
