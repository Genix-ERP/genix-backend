-- Per-company category account mappings
-- Categories are shared across all organizations within a tenant,
-- but each organization has its own account assignments.

CREATE TABLE IF NOT EXISTS category_organization_settings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES product_categories(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    -- Account mappings
    income_account_id UUID REFERENCES accounts(id),
    expense_account_id UUID REFERENCES accounts(id),
    stock_valuation_account_id UUID REFERENCES accounts(id),
    stock_input_account_id UUID REFERENCES accounts(id),
    stock_output_account_id UUID REFERENCES accounts(id),

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(category_id, organization_id)
);

CREATE INDEX IF NOT EXISTS idx_cos_tenant ON category_organization_settings(tenant_id);
CREATE INDEX IF NOT EXISTS idx_cos_category ON category_organization_settings(category_id);
CREATE INDEX IF NOT EXISTS idx_cos_organization ON category_organization_settings(organization_id);

-- Migrate existing data
INSERT INTO category_organization_settings (
    tenant_id, category_id, organization_id,
    income_account_id, expense_account_id,
    stock_valuation_account_id, stock_input_account_id, stock_output_account_id
)
SELECT
    pc.tenant_id, pc.id, pc.organization_id,
    pc.income_account_id, pc.expense_account_id,
    pc.stock_valuation_account_id, pc.stock_input_account_id, pc.stock_output_account_id
FROM product_categories pc
WHERE pc.organization_id IS NOT NULL AND pc.deleted_at IS NULL
ON CONFLICT (category_id, organization_id) DO NOTHING;
