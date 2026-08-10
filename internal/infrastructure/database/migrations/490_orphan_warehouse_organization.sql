-- Warehouses with no organization hold stock nobody can see.
--
-- Every read path filters on the WAREHOUSE's organization, not on
-- inventory.organization_id:
--
--   ListInventory    AND w.organization_id = $orgID
--   ListWarehouses   AND w.organization_id = $orgID
--   Products.jsx     accessibleWarehouseIds (the same rule again, client-side)
--
-- `w.organization_id = $orgID` is never true when the column is NULL, so a
-- warehouse without an organization is invisible to EVERY organization. Goods
-- received into it are real, posted to the ledger, and unreachable: the
-- operation reads "Bajarildi 200 Dona" and the product reads "Qoldiq 0".
--
-- NUMBERED 490, NOT 489, DELIBERATELY.
-- Version 489 is recorded in schema_migrations on the production database — it
-- was applied before that migration was withdrawn from the repository. Because
-- the runner keys on the integer version and skips versions already recorded, a
-- file numbered 489 would never execute there. It would not fail; it would be
-- silently ignored, which is worse.
--
-- WHAT THIS DOES AND DOES NOT REPAIR
-- Only tenants with exactly ONE organization are backfilled, because only there
-- is the answer unambiguous. A tenant running several companies could have
-- meant any of them, and quietly moving a warehouse — and with it all of its
-- stock and history — into the wrong company is a far more expensive mistake
-- than leaving it visibly broken. Those are reported at the end instead.
--
-- No NOT NULL constraint is added for the same reason: it would fail for
-- exactly the multi-organization tenants this cannot repair.

-- WHY THIS UPDATES ONE ROW AT A TIME INSIDE AN EXCEPTION BLOCK
--
-- The first version of this migration ran a single set-based UPDATE. It hit
-- `warehouses_tenant_org_code_key` on production — a warehouse with no
-- organization can freely share a `code` with one that already sits in the
-- target organization, and the moment it is moved in, that pair collides.
--
-- The failure was not the collision. The failure was that a migration whose
-- only job is to REPAIR data was able to abort, and an aborted migration is
-- never recorded, so the API restarted into the same error forever. A repair
-- must never be able to take the system down: the worst it may do is repair
-- less than it hoped and say so.
--
-- Each row is therefore attempted on its own, and a row that cannot be moved
-- is reported by name instead of killing the transaction. Rows are also
-- pre-filtered against codes already taken in the target organization, so the
-- exception handler is a backstop for constraints this file does not know
-- about, not the primary mechanism.

DO $$
DECLARE
    fixed_count     INTEGER := 0;
    conflict_count  INTEGER := 0;
    orphan_count    INTEGER := 0;
    r               RECORD;
BEGIN
    -- Backfill from the tenant's sole organization.
    -- (array_agg(id))[1] rather than MIN(id): Postgres has no MIN() for uuid.
    -- HAVING COUNT(*) = 1 means the array holds exactly one element anyway.
    FOR r IN
        WITH single_org AS (
            SELECT tenant_id, (array_agg(id))[1] AS org_id
            FROM organizations
            GROUP BY tenant_id
            HAVING COUNT(*) = 1
        )
        SELECT w.id, w.name, w.code, w.tenant_id, s.org_id,
               -- Is this code already in use inside the target organization?
               -- deleted_at is deliberately NOT considered: the unique
               -- constraint is on (tenant_id, organization_id, code) and takes
               -- no notice of soft deletion, so a soft-deleted row still
               -- occupies the code.
               EXISTS (
                   SELECT 1 FROM warehouses taken
                   WHERE taken.tenant_id = w.tenant_id
                     AND taken.organization_id = s.org_id
                     AND taken.code = w.code
               ) AS code_taken,
               -- Two organization-less warehouses in the same tenant can share
               -- a code with EACH OTHER. Neither collides with anything yet, so
               -- both pass the check above and then collide on the way in.
               -- Only the first of each code is moved.
               ROW_NUMBER() OVER (PARTITION BY w.tenant_id, s.org_id, w.code ORDER BY w.created_at, w.id) AS code_rank
        FROM warehouses w
        JOIN single_org s ON s.tenant_id = w.tenant_id
        WHERE w.organization_id IS NULL
          AND w.deleted_at IS NULL
    LOOP
        IF r.code_taken OR r.code_rank > 1 THEN
            conflict_count := conflict_count + 1;
            RAISE WARNING 'Warehouse "%" (code %, id %) not moved: code already used in the target company. Give it a different code, then open and save it to attach it.',
                r.name, r.code, r.id;
            CONTINUE;
        END IF;

        BEGIN
            UPDATE warehouses
            SET organization_id = r.org_id,
                updated_at      = CURRENT_TIMESTAMP
            WHERE id = r.id;
            fixed_count := fixed_count + 1;
        EXCEPTION WHEN unique_violation OR check_violation OR foreign_key_violation THEN
            -- Backstop. Reaching here means a constraint this file does not
            -- know about rejected the row; skipping it costs one unrepaired
            -- warehouse, whereas propagating would cost the whole API.
            conflict_count := conflict_count + 1;
            RAISE WARNING 'Warehouse "%" (id %) not moved: %', r.name, r.id, SQLERRM;
        END;
    END LOOP;

    IF fixed_count > 0 THEN
        RAISE NOTICE 'Backfilled organization_id on % warehouse(s); their stock is now visible.', fixed_count;
    END IF;
    IF conflict_count > 0 THEN
        RAISE WARNING '% warehouse(s) could not be attached automatically — see the warnings above.', conflict_count;
    END IF;

    -- Report, do not touch, the ones that need a human decision.
    SELECT COUNT(*) INTO orphan_count
    FROM warehouses w
    WHERE w.organization_id IS NULL AND w.deleted_at IS NULL;

    IF orphan_count > 0 THEN
        RAISE WARNING 'Still % warehouse(s) with no organization (multi-organization tenants). Stock in them stays invisible until an organization is assigned:', orphan_count;
        FOR r IN
            SELECT w.id, w.name, w.tenant_id,
                   COALESCE((SELECT SUM(i.quantity_on_hand)
                             FROM inventory i
                             WHERE i.warehouse_id = w.id), 0) AS qty
            FROM warehouses w
            WHERE w.organization_id IS NULL AND w.deleted_at IS NULL
            ORDER BY qty DESC
        LOOP
            RAISE WARNING '  warehouse % (%) tenant % — % unit(s) of stock unreachable', r.name, r.id, r.tenant_id, r.qty;
        END LOOP;
    END IF;
END $$;

-- Finding these again without reading this file.
CREATE INDEX IF NOT EXISTS idx_warehouses_missing_organization
    ON warehouses (tenant_id)
    WHERE organization_id IS NULL AND deleted_at IS NULL;
