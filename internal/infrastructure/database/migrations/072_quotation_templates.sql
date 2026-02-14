-- Quotation Templates Module Migration
-- Pre-configured quote blueprints for faster quotation creation

-- Main quotation templates table
CREATE TABLE IF NOT EXISTS quotation_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Basic info
    name VARCHAR(255) NOT NULL,
    code VARCHAR(50),
    description TEXT,

    -- Default settings
    validity_days INTEGER DEFAULT 30,
    payment_term_id UUID REFERENCES payment_terms(id),
    pricelist_id UUID REFERENCES pricelists(id),

    -- Terms and conditions
    terms_and_conditions TEXT,
    header_note TEXT,
    footer_note TEXT,

    -- Confirmation settings
    require_signature BOOLEAN DEFAULT false,
    require_payment BOOLEAN DEFAULT false,
    payment_percentage DECIMAL(5,2) DEFAULT 0,

    -- Display settings
    show_line_subtotals BOOLEAN DEFAULT true,
    show_line_discounts BOOLEAN DEFAULT true,
    group_by_section BOOLEAN DEFAULT true,

    -- Status
    is_active BOOLEAN DEFAULT true,

    -- Metadata
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(tenant_id, code)
);

-- Template sections (for grouping products)
CREATE TABLE IF NOT EXISTS quotation_template_sections (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    template_id UUID NOT NULL REFERENCES quotation_templates(id) ON DELETE CASCADE,

    name VARCHAR(255) NOT NULL,
    description TEXT,
    sequence INTEGER DEFAULT 10,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Template line items (required products)
CREATE TABLE IF NOT EXISTS quotation_template_lines (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    template_id UUID NOT NULL REFERENCES quotation_templates(id) ON DELETE CASCADE,
    section_id UUID REFERENCES quotation_template_sections(id) ON DELETE SET NULL,

    -- Product info
    product_id UUID REFERENCES products(id),
    product_name VARCHAR(255),
    description TEXT,

    -- Quantity and pricing
    quantity DECIMAL(15,4) DEFAULT 1,
    unit_id UUID REFERENCES units_of_measure(id),
    unit_price DECIMAL(15,2),
    discount_type VARCHAR(20), -- percentage, fixed
    discount_value DECIMAL(15,2) DEFAULT 0,

    -- Display
    sequence INTEGER DEFAULT 10,
    display_type VARCHAR(20) DEFAULT 'product', -- product, section_header, note

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Optional products (upsells)
CREATE TABLE IF NOT EXISTS quotation_template_optionals (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    template_id UUID NOT NULL REFERENCES quotation_templates(id) ON DELETE CASCADE,

    -- Product info
    product_id UUID REFERENCES products(id),
    product_name VARCHAR(255),
    description TEXT,

    -- Quantity and pricing
    quantity DECIMAL(15,4) DEFAULT 1,
    unit_id UUID REFERENCES units_of_measure(id),
    unit_price DECIMAL(15,2),
    discount_type VARCHAR(20),
    discount_value DECIMAL(15,2) DEFAULT 0,

    -- Display
    sequence INTEGER DEFAULT 10,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Add template reference to quotations
ALTER TABLE quotations ADD COLUMN IF NOT EXISTS template_id UUID REFERENCES quotation_templates(id);

-- Add optional products tracking to quotation items
ALTER TABLE quotation_lines ADD COLUMN IF NOT EXISTS is_optional BOOLEAN DEFAULT false;
ALTER TABLE quotation_lines ADD COLUMN IF NOT EXISTS is_selected BOOLEAN DEFAULT true;
ALTER TABLE quotation_lines ADD COLUMN IF NOT EXISTS section_name VARCHAR(255);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_quotation_templates_tenant ON quotation_templates(tenant_id);
CREATE INDEX IF NOT EXISTS idx_quotation_templates_active ON quotation_templates(is_active);
CREATE INDEX IF NOT EXISTS idx_quotation_templates_code ON quotation_templates(code);

CREATE INDEX IF NOT EXISTS idx_template_sections_template ON quotation_template_sections(template_id);
CREATE INDEX IF NOT EXISTS idx_template_sections_sequence ON quotation_template_sections(sequence);

CREATE INDEX IF NOT EXISTS idx_template_lines_template ON quotation_template_lines(template_id);
CREATE INDEX IF NOT EXISTS idx_template_lines_section ON quotation_template_lines(section_id);
CREATE INDEX IF NOT EXISTS idx_template_lines_product ON quotation_template_lines(product_id);
CREATE INDEX IF NOT EXISTS idx_template_lines_sequence ON quotation_template_lines(sequence);

CREATE INDEX IF NOT EXISTS idx_template_optionals_template ON quotation_template_optionals(template_id);
CREATE INDEX IF NOT EXISTS idx_template_optionals_product ON quotation_template_optionals(product_id);

CREATE INDEX IF NOT EXISTS idx_quotations_template ON quotations(template_id) WHERE template_id IS NOT NULL;

-- Add quotation template permissions
INSERT INTO permissions (id, module, resource, action, description)
VALUES
    (uuid_generate_v4(), 'sales', 'quotation_template', 'read', 'View quotation templates'),
    (uuid_generate_v4(), 'sales', 'quotation_template', 'create', 'Create quotation templates'),
    (uuid_generate_v4(), 'sales', 'quotation_template', 'update', 'Update quotation templates'),
    (uuid_generate_v4(), 'sales', 'quotation_template', 'delete', 'Delete quotation templates')
ON CONFLICT (module, resource, action) DO NOTHING;
