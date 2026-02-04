-- Migration: 057_packaging.sql
-- Description: Add packaging support (Odoo-style product packagings, package types, and packages)

-- Product Packagings (e.g., 6-pack, 12-pack, case of 24)
-- This defines how a product can be sold/purchased in bulk quantities
CREATE TABLE IF NOT EXISTS product_packagings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,           -- "6-Pack", "Case of 24", "Pallet"
    qty DECIMAL(15,3) NOT NULL,           -- Quantity of product in this packaging
    barcode VARCHAR(100),                 -- Packaging-specific barcode
    sales BOOLEAN DEFAULT true,           -- Available for sales orders
    purchase BOOLEAN DEFAULT true,        -- Available for purchase orders
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    deleted_at TIMESTAMP,
    UNIQUE(tenant_id, product_id, name)
);

-- Package Types (box sizes, pallets, containers, etc.)
-- This defines physical container specifications for shipping
CREATE TABLE IF NOT EXISTS package_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,           -- "Small Box", "Large Box", "Pallet"
    code VARCHAR(50),                     -- "SBOX", "LBOX", "PALLET"
    length_mm INT,                        -- Length in millimeters
    width_mm INT,                         -- Width in millimeters
    height_mm INT,                        -- Height in millimeters
    max_weight DECIMAL(10,2),             -- Maximum weight in kg
    barcode VARCHAR(100),
    package_use VARCHAR(20) DEFAULT 'disposable', -- 'reusable' or 'disposable'
    carrier_id UUID,                      -- Optional: preferred carrier for this package type
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    deleted_at TIMESTAMP,
    UNIQUE(tenant_id, code)
);

-- Packages (actual physical packages used in warehouse transfers)
-- This tracks real packages moving through the warehouse
CREATE TABLE IF NOT EXISTS packages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,           -- "PACK00001", auto-generated
    package_type_id UUID REFERENCES package_types(id),
    shipping_weight DECIMAL(10,2),        -- Actual weight of package
    location_id UUID,                     -- Current location in warehouse
    picking_id UUID,                      -- Link to stock picking/transfer
    pack_date TIMESTAMP DEFAULT NOW(),    -- When package was created
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

-- Package Contents (products inside a package)
CREATE TABLE IF NOT EXISTS package_contents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    package_id UUID NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id),
    quantity DECIMAL(15,3) NOT NULL,
    lot_id UUID,                          -- Optional lot tracking
    created_at TIMESTAMP DEFAULT NOW()
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_product_packagings_tenant ON product_packagings(tenant_id);
CREATE INDEX IF NOT EXISTS idx_product_packagings_product ON product_packagings(product_id);
CREATE INDEX IF NOT EXISTS idx_product_packagings_barcode ON product_packagings(barcode) WHERE barcode IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_package_types_tenant ON package_types(tenant_id);
CREATE INDEX IF NOT EXISTS idx_package_types_code ON package_types(tenant_id, code);
CREATE INDEX IF NOT EXISTS idx_packages_tenant ON packages(tenant_id);
CREATE INDEX IF NOT EXISTS idx_packages_location ON packages(location_id) WHERE location_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_packages_type ON packages(package_type_id) WHERE package_type_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_package_contents_package ON package_contents(package_id);
CREATE INDEX IF NOT EXISTS idx_package_contents_product ON package_contents(product_id);

-- Add sequence for package names
CREATE SEQUENCE IF NOT EXISTS package_seq START 1;

-- Function to generate package name
CREATE OR REPLACE FUNCTION generate_package_name(p_tenant_id UUID)
RETURNS VARCHAR(100) AS $$
DECLARE
    v_seq INT;
    v_name VARCHAR(100);
BEGIN
    SELECT COALESCE(MAX(CAST(SUBSTRING(name FROM 5) AS INT)), 0) + 1
    INTO v_seq
    FROM packages
    WHERE tenant_id = p_tenant_id AND name LIKE 'PACK%';

    v_name := 'PACK' || LPAD(v_seq::TEXT, 5, '0');
    RETURN v_name;
END;
$$ LANGUAGE plpgsql;
