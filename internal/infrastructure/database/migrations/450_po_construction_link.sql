-- 450_po_construction_link.sql — PO ↔ Qurilish obyekti bog'i
-- (docs/xarid-changelog.md "Qolgan ishlar" §5).
--
-- A PO placed for a construction site links to the project; on goods
-- receipt each received line contributes an actual-cost row to
-- construction_expense_lines (the central per-object cost table, DR 0810
-- construction WIP by default). construction_projects PK is BIGSERIAL.

ALTER TABLE purchase_orders
    ADD COLUMN IF NOT EXISTS construction_project_id BIGINT REFERENCES construction_projects(id);

CREATE INDEX IF NOT EXISTS idx_purchase_orders_construction
    ON purchase_orders(construction_project_id)
    WHERE construction_project_id IS NOT NULL;
