-- Zaxiralarni baholash, bosqich 2: the one account the plan's §4 table needs
-- that this schema does not already have.
--
-- WHAT WAS ALREADY THERE
-- The plan's §1.1 asks for GL accounts configured on the company and
-- overridden per product category. That exists: product_categories already
-- carries income_account_id, expense_account_id, stock_valuation_account_id,
-- stock_input_account_id and stock_output_account_id, and getCategoryAccounts
-- already falls back to the company defaults and resolves a group account down
-- to a leaf. Routing by product type (raw 1010 / finished 2810 / trade 2910)
-- is in getInventoryAccountByType. None of that needed rebuilding, and
-- building a second mapping beside it would have been the whole problem.
--
-- WHAT WAS MISSING
-- Standard costing books the difference between the actual purchase price and
-- the standard into a variance account at the moment of receipt (§3.3):
--     Dt 2910 (at standard) + Dt/Kt Chetlanishlar (difference)
--     Kt 6010 (at actual)
-- There is no such account in the mapping. Without it a standard-cost receipt
-- cannot balance, so this column is a precondition for the method, not a
-- refinement of it.
--
-- NULL means inherit from the company policy, exactly like the other five.
ALTER TABLE product_categories
    ADD COLUMN IF NOT EXISTS cost_variance_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;

COMMENT ON COLUMN product_categories.cost_variance_account_id IS
    'Standard-costing variance account (Chetlanishlar). NULL inherits the company default. Only consulted when the effective cost method is standard.';

-- The account this layer's value sits in, recorded WHEN THE LAYER IS CREATED.
--
-- The reconciliation report (§1.3) has to group layer values by GL account to
-- compare them against the ledger. The first version of it re-derived that
-- account in SQL: category override, else the product's own account, else the
-- routing by inventory_type. That is a second copy of a precedence chain that
-- already lives in Go (getCategoryAccounts and getInventoryAccountByType), and
-- the two would eventually disagree — at which point the report would announce
-- discrepancies that are nothing but its own disagreement with the postings.
-- A reconciliation that can raise a false alarm is worse than none, because the
-- next real alarm gets ignored.
--
-- Recording the resolved account on the layer removes the derivation entirely:
-- the report groups by the account the posting actually used. It also survives
-- a later re-configuration — moving a category to a different valuation account
-- must not retroactively re-file stock that was already posted somewhere else.
ALTER TABLE stock_valuation_layers
    ADD COLUMN IF NOT EXISTS stock_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_svl_stock_account
    ON stock_valuation_layers (tenant_id, stock_account_id)
    WHERE remaining_qty > 0 AND is_reversed = false;

COMMENT ON COLUMN stock_valuation_layers.stock_account_id IS
    'GL account this layer''s value was posted to, resolved once at creation. The reconciliation report groups by this rather than re-deriving the routing.';
