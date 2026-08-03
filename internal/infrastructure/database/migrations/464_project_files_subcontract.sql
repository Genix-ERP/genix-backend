-- 464_project_files_subcontract.sql
-- Subcontract file attachments (docs/construction-roadmap.md, 4-to'plam).
--
-- The SubcontractorsTab UI has been calling GET/POST/DELETE
-- /construction/subcontracts/:id/files since the tab shipped, but the routes
-- never existed (phantom-endpoint audit finding #5). The payload the tab
-- sends is exactly the project_files shape, so instead of a new table the
-- register gains an optional subcontract_id: NULL = project-level document,
-- set = document of that subcontract. Project-level listings and the
-- files_count card badge exclude subcontract rows (handler-side filter).
ALTER TABLE project_files
    ADD COLUMN IF NOT EXISTS subcontract_id BIGINT REFERENCES construction_subcontract(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_project_files_subcontract
    ON project_files(subcontract_id)
    WHERE subcontract_id IS NOT NULL;
