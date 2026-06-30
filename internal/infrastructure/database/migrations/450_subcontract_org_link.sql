-- 449_subcontract_org_link.sql
--
-- Cross-company subcontracting: a subcontract can name another COMPANY of the
-- same tenant as the subcontractor (Company B does work on Company A's project).
-- When set, that company's users see the project in their normal Loyihalar list
-- (badged "Subpudratchi") and control their works as usual; final YAKUNIY
-- acceptance stays with the project-owner company.
--
-- NULL = an external subcontractor identified only by partner_name (unchanged).

ALTER TABLE construction_subcontract
    ADD COLUMN IF NOT EXISTS subcontractor_organization_id UUID REFERENCES organizations(id);

CREATE INDEX IF NOT EXISTS idx_subcontract_sub_org
    ON construction_subcontract (tenant_id, subcontractor_organization_id)
    WHERE subcontractor_organization_id IS NOT NULL;
