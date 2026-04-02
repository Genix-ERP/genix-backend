-- Pipeline stages should be tenant-wide, not per-organization.
-- Clear organization_id so all employees in the same tenant see the same stages.
UPDATE pipeline_stages SET organization_id = NULL WHERE organization_id IS NOT NULL;
