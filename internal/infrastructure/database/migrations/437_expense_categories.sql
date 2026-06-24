-- No-op: the expense_categories table already exists (created by an earlier
-- migration) with a richer schema. Custom expense categories reuse that table
-- and the existing /expense-categories endpoints.
SELECT 1;
