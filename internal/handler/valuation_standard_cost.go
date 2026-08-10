package handler

import (
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// Zaxiralarni baholash, bosqich 3: standart narx.
//
// Changing a standard cost while stock is held is not an edit — it is a
// document (§3.3). It revalues everything on hand, posts the difference to the
// variance account, rewrites the open layers so Σ remaining_value still equals
// Q × standard, and records what it did. All of that or none of it: the whole
// thing runs in one transaction and any failure rolls back.
//
// That last point is the reason this file does not follow the shape of
// postInventoryConsumptionJE, which logs an insert failure and returns — the
// pattern that produced one-sided entries until migration 416 installed a
// database trigger to catch them. Here every error aborts.

// resolveVarianceAccount finds the Chetlanishlar account for a product:
// category override first, then a tenant-level account whose name looks like a
// variance account.
//
// It returns uuid.Nil rather than guessing a code. The chart of accounts has no
// conventional number for this under BHMS №21 — the plan itself says the final
// mapping has to be agreed with the client's accountant — so inventing one
// would post real money to whatever account happened to sit there.
func (h *Handler) resolveVarianceAccount(q dbQuerier, tenantID uuid.UUID, orgID *uuid.UUID, productID uuid.UUID) uuid.UUID {
	var id uuid.UUID
	_ = q.QueryRow(`
		SELECT COALESCE(pc.cost_variance_account_id, '00000000-0000-0000-0000-000000000000')
		FROM products p
		LEFT JOIN product_categories pc ON pc.id = p.category_id
		WHERE p.id = $1`, productID).Scan(&id)

	if id = resolveLeafAccount(q, id); id != uuid.Nil {
		return id
	}
	// No code fallback, deliberately — name only.
	for _, name := range []string{"chetlanish", "variance", "otklonenie"} {
		if found := findAccount(q, tenantID, orgID, name, ""); found != uuid.Nil {
			return found
		}
	}
	return uuid.Nil
}

// productStandardCostState is everything the revaluation needs to decide.
type productStandardCostState struct {
	StandardCost   int64 // tiyin
	CategoryMethod string
	QtyOnHand      *big.Rat
	StockAccountID uuid.UUID
}

func (h *Handler) loadStandardCostState(q dbQuerier, tenantID, productID uuid.UUID, orgID *uuid.UUID) (productStandardCostState, error) {
	var st productStandardCostState
	var stdCost float64
	var categoryMethod sql.NullString
	var qtyOnHand float64

	err := q.QueryRow(`
		SELECT COALESCE(p.standard_cost, 0),
		       pc.cost_method,
		       -- On-hand is the PHYSICAL quantity from the inventory register,
		       -- not a sum over valuation layers. Layers are the valuation; a
		       -- product can hold stock before this system has ever written a
		       -- layer for it, and a revaluation of that stock is still a real
		       -- revaluation.
		       COALESCE((SELECT SUM(i.quantity_on_hand) FROM inventory i
		                 WHERE i.product_id = p.id AND i.tenant_id = p.tenant_id), 0)
		FROM products p
		LEFT JOIN product_categories pc ON pc.id = p.category_id
		WHERE p.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL`,
		productID, tenantID).Scan(&stdCost, &categoryMethod, &qtyOnHand)
	if err != nil {
		return st, err
	}

	st.StandardCost = toTiyin(stdCost)
	st.CategoryMethod = categoryMethod.String
	st.QtyOnHand = floatToRat(qtyOnHand)

	ca := getCategoryAccounts(q, tenantID, orgID, productID)
	st.StockAccountID = ca.StockValuationAccountID
	if st.StockAccountID == uuid.Nil {
		st.StockAccountID = getInventoryAccountByType(q, tenantID, orgID, productID)
	}
	return st, nil
}

// GetProductStandardCost godoc
// @Summary Standart narx va uning tarixi
// @Description Current standard cost, the effective method, stock on hand and every past change
// @Tags Inventory - Valuation
// @Produce json
// @Param id path string true "Product ID"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /products/{id}/standard-cost [get]
func (h *Handler) GetProductStandardCost(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	productID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}
	orgID := orgPointer(c)

	st, err := h.loadStandardCostState(h.db, tenantID, productID, orgID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Product")
		return
	}
	if err != nil {
		h.log.Error("Failed to load standard cost", "error", err)
		response.InternalError(c, "Failed to load standard cost")
		return
	}

	method := EffectiveCostMethod(st.CategoryMethod, h.readValuationSetting(tenantID).Method)

	rows, err := h.db.Query(`
		SELECT effective_date, old_cost, new_cost, quantity_on_hand,
		       revaluation_delta, journal_entry_id, created_at
		FROM product_standard_cost_history
		WHERE tenant_id = $1 AND product_id = $2
		ORDER BY effective_date DESC, created_at DESC`, tenantID, productID)
	if err != nil {
		h.log.Error("Failed to load standard cost history", "error", err)
		response.InternalError(c, "Failed to load standard cost history")
		return
	}
	defer rows.Close()

	type change struct {
		EffectiveDate    string  `json:"effective_date"`
		OldCost          float64 `json:"old_cost"`
		NewCost          float64 `json:"new_cost"`
		QuantityOnHand   float64 `json:"quantity_on_hand"`
		RevaluationDelta float64 `json:"revaluation_delta"`
		JournalEntryID   *string `json:"journal_entry_id"`
		CreatedAt        string  `json:"created_at"`
	}
	history := make([]change, 0)
	for rows.Next() {
		var ch change
		var d time.Time
		var jeID sql.NullString
		var created time.Time
		if err := rows.Scan(&d, &ch.OldCost, &ch.NewCost, &ch.QuantityOnHand,
			&ch.RevaluationDelta, &jeID, &created); err != nil {
			h.log.Error("Failed to scan standard cost history row", "error", err)
			continue
		}
		ch.EffectiveDate = d.Format("2006-01-02")
		ch.CreatedAt = created.Format(time.RFC3339)
		if jeID.Valid {
			ch.JournalEntryID = &jeID.String
		}
		history = append(history, ch)
	}

	qty, _ := st.QtyOnHand.Float64()
	response.Success(c, gin.H{
		"product_id":       productID,
		"standard_cost":    fromTiyin(st.StandardCost),
		"effective_method": string(method),
		// standard_cost only means anything under the standard method. Saying
		// so lets the product card grey the field out instead of showing a
		// number that nothing reads.
		"is_standard_method": method == CostMethodStandard,
		"quantity_on_hand":   qty,
		"stock_value":        fromTiyin(mulTiyin(st.StandardCost, st.QtyOnHand)),
		"history":            history,
	})
}

// UpdateProductStandardCost godoc
// @Summary Qiymatni qayta baholash — change the standard cost
// @Description Revalues stock on hand, posts the difference to variances, rescales the layers and records the change
// @Tags Inventory - Valuation
// @Accept json
// @Produce json
// @Param id path string true "Product ID"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /products/{id}/standard-cost [put]
//
// The §3.3 procedure, as one transaction:
//
//	delta = (new − old) × on_hand
//	if delta != 0:  post Dt/Kt stock ↔ variance, and rescale the open layers
//	record the change, then set the new cost
//
// With no stock on hand there is no delta, so there is no posting and no
// document — just a price change and a history row. The plan is explicit about
// that case and it matters: a business setting up its standards before its
// first receipt should not be made to think it is moving money.
func (h *Handler) UpdateProductStandardCost(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	productID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid product ID")
		return
	}
	orgID := orgPointer(c)

	var input struct {
		NewCost       float64 `json:"new_cost" binding:"required,gte=0"`
		EffectiveDate string  `json:"effective_date"`
		Notes         string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	effectiveDate := time.Now().Format("2006-01-02")
	if input.EffectiveDate != "" {
		if _, perr := time.Parse("2006-01-02", input.EffectiveDate); perr != nil {
			response.BadRequest(c, "Invalid effective_date, expected YYYY-MM-DD")
			return
		}
		effectiveDate = input.EffectiveDate
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer func() { _ = tx.Rollback() }()

	st, err := h.loadStandardCostState(tx, tenantID, productID, orgID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Product")
		return
	}
	if err != nil {
		h.log.Error("Failed to load standard cost", "error", err)
		response.InternalError(c, "Failed to load standard cost")
		return
	}

	method := EffectiveCostMethod(st.CategoryMethod, h.readValuationSetting(tenantID).Method)
	if method != CostMethodStandard {
		// Refused rather than stored-for-later. Writing a standard cost under
		// FIFO or AVCO would leave a number on the product that no valuation
		// reads, and the next person to switch the category to standard would
		// silently inherit a price nobody reviewed.
		response.BadRequest(c, fmt.Sprintf(
			"Standart narxni faqat 'standard' usulida o'zgartirish mumkin (joriy usul: %s)", method))
		return
	}

	newCost := toTiyin(input.NewCost)
	oldCost := st.StandardCost
	if newCost == oldCost {
		response.BadRequest(c, "Yangi narx joriy narx bilan bir xil")
		return
	}

	delta := StandardRevaluation(st.QtyOnHand, oldCost, newCost)

	var jeID *uuid.UUID
	if delta != 0 {
		varianceID := h.resolveVarianceAccount(tx, tenantID, orgID, productID)
		if varianceID == uuid.Nil || st.StockAccountID == uuid.Nil {
			response.BadRequest(c,
				"Qayta baholash uchun zaxiralar va chetlanishlar schyotlari sozlanmagan")
			return
		}

		lines, lerr := BuildStockLines(OpStandardRevaluation, ValuationAccounts{
			Stock:    st.StockAccountID.String(),
			Variance: varianceID.String(),
		}, delta, "Standart narxni qayta baholash")
		if lerr != nil {
			h.log.Error("Failed to build revaluation lines", "error", lerr)
			response.InternalError(c, "Failed to build revaluation entry")
			return
		}

		id, perr := h.postValuationEntry(tx, valuationEntryArgs{
			TenantID:       tenantID,
			OrganizationID: orgID,
			EntryDate:      effectiveDate,
			Description:    "Standart narxni qayta baholash",
			SourceType:     "stock_revaluation",
			SourceID:       productID,
			Lines:          lines,
			CreatedBy:      userID,
		})
		if perr != nil {
			// Aborts. The period guard installed by migration 483 surfaces here
			// as an error on a closed period, which is exactly what should stop
			// a backdated revaluation (§2.4).
			h.log.Error("Failed to post revaluation entry", "error", perr)
			response.BadRequest(c, "Provodka yaratib bo'lmadi: "+perr.Error())
			return
		}
		jeID = &id

		if rerr := h.rescaleLayersToStandard(tx, tenantID, orgID, productID, newCost); rerr != nil {
			h.log.Error("Failed to rescale valuation layers", "error", rerr)
			response.InternalError(c, "Failed to rescale valuation layers")
			return
		}
	}

	qtyFloat, _ := st.QtyOnHand.Float64()
	if _, err := tx.Exec(`
		INSERT INTO product_standard_cost_history (
			tenant_id, organization_id, product_id, effective_date,
			old_cost, new_cost, quantity_on_hand, revaluation_delta,
			journal_entry_id, created_by
		) VALUES ($1, $2, $3, $4::date, $5, $6, $7, $8, $9, $10)`,
		tenantID, orgID, productID, effectiveDate,
		fromTiyin(oldCost), fromTiyin(newCost), qtyFloat, fromTiyin(delta),
		jeID, nilIfZeroUUID(userID),
	); err != nil {
		h.log.Error("Failed to record standard cost change", "error", err)
		response.InternalError(c, "Failed to record standard cost change")
		return
	}

	if _, err := tx.Exec(`
		UPDATE products SET standard_cost = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3`,
		fromTiyin(newCost), productID, tenantID,
	); err != nil {
		h.log.Error("Failed to update standard cost", "error", err)
		response.InternalError(c, "Failed to update standard cost")
		return
	}

	if err := tx.Commit(); err != nil {
		// The deferred balance trigger from migration 416 fires here, so an
		// imbalanced entry surfaces as a commit failure rather than as data.
		h.log.Error("Failed to commit standard cost change", "error", err)
		response.InternalError(c, "Failed to commit standard cost change: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"product_id":        productID,
		"old_cost":          fromTiyin(oldCost),
		"new_cost":          fromTiyin(newCost),
		"quantity_on_hand":  qtyFloat,
		"revaluation_delta": fromTiyin(delta),
		"journal_entry_id":  jeID,
		"posted":            jeID != nil,
	})
}

// rescaleLayersToStandard rewrites the open layers so Σ remaining_value equals
// Q × the new standard (§1.3 must hold immediately after a revaluation, not
// merely on average).
func (h *Handler) rescaleLayersToStandard(tx *sql.Tx, tenantID uuid.UUID, orgID *uuid.UUID, productID uuid.UUID, newCost int64) error {
	rows, err := tx.Query(`
		SELECT id, layer_date, remaining_qty, remaining_value
		FROM stock_valuation_layers
		WHERE tenant_id = $1 AND product_id = $2
		  AND is_reversed = false AND remaining_qty > 0
		ORDER BY layer_date, created_at, id`, tenantID, productID)
	if err != nil {
		return err
	}

	var layers []Layer
	var seq int64
	for rows.Next() {
		var id string
		var d time.Time
		var qty, val float64
		if err := rows.Scan(&id, &d, &qty, &val); err != nil {
			// Closed explicitly: a `defer` inside this loop would hold the
			// cursor until the function returns, and the UPDATEs below run on
			// the same transaction.
			rows.Close()
			return err
		}
		seq++
		layers = append(layers, Layer{
			ID: id, SeqNo: seq, DateOrdinal: d.Unix() / 86400,
			RemainingQty: floatToRat(qty), RemainingValue: toTiyin(val),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(layers) == 0 {
		return nil
	}

	RescaleLayersToStandard(layers, newCost)
	for _, l := range layers {
		if _, err := tx.Exec(`
			UPDATE stock_valuation_layers SET remaining_value = $1 WHERE id = $2`,
			fromTiyin(l.RemainingValue), l.ID); err != nil {
			return err
		}
	}
	return nil
}

// valuationEntryArgs describes one balanced posting.
type valuationEntryArgs struct {
	TenantID       uuid.UUID
	OrganizationID *uuid.UUID
	EntryDate      string
	Description    string
	SourceType     string
	SourceID       uuid.UUID
	Lines          []JournalLine
	CreatedBy      uuid.UUID
}

// postValuationEntry writes a balanced journal entry and its lines.
//
// Every failure is RETURNED, never logged-and-ignored. postInventoryConsumptionJE
// logs an insert failure and carries on, which is how a credit could land
// without its debit — migration 416's header records 50+ entries that reached
// production that way before a database trigger was installed to stop them.
func (h *Handler) postValuationEntry(tx *sql.Tx, args valuationEntryArgs) (uuid.UUID, error) {
	if err := assertBalanced(args.Lines); err != nil {
		return uuid.Nil, err
	}

	var journalID uuid.UUID
	var nextNumber int
	err := tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1) FROM journals
		WHERE tenant_id = $1 AND type = 'general' AND deleted_at IS NULL
		ORDER BY created_at LIMIT 1`, args.TenantID).Scan(&journalID, &nextNumber)
	if err != nil {
		return uuid.Nil, fmt.Errorf("no general journal configured: %w", err)
	}

	var total int64
	for _, l := range args.Lines {
		total += l.Debit
	}

	var orgArg interface{}
	if args.OrganizationID != nil && *args.OrganizationID != uuid.Nil {
		orgArg = *args.OrganizationID
	}

	entryID := uuid.New()
	now := time.Now()
	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number,
			entry_date, description, source_type, source_id, exchange_rate,
			total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8, $9, 1.0, $10, $10, 'posted', $11, $12, $12)`,
		entryID, args.TenantID, orgArg, journalID, fmt.Sprintf("REVAL%06d", nextNumber),
		args.EntryDate, args.Description, args.SourceType, args.SourceID,
		fromTiyin(total), nilIfZeroUUID(args.CreatedBy), now,
	); err != nil {
		return uuid.Nil, err
	}

	if _, err := tx.Exec(`
		UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`,
		now, journalID); err != nil {
		return uuid.Nil, err
	}

	for i, l := range args.Lines {
		accountID, perr := uuid.Parse(l.AccountID)
		if perr != nil {
			return uuid.Nil, fmt.Errorf("line %d: invalid account: %w", i+1, perr)
		}
		amount := l.Debit
		if amount == 0 {
			amount = l.Credit
		}
		if _, err := tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, amount_base,
				analytics_json, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, 1.0, $8, '{}'::jsonb, $9)`,
			uuid.New(), entryID, i+1, accountID, l.Memo,
			fromTiyin(l.Debit), fromTiyin(l.Credit), fromTiyin(amount), now,
		); err != nil {
			return uuid.Nil, err
		}
		if _, err := tx.Exec(`
			UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2
			WHERE id = $3`, fromTiyin(l.Debit-l.Credit), now, accountID); err != nil {
			return uuid.Nil, err
		}
	}

	return entryID, nil
}

// ------------------------------------------------------------- helpers ---

// toTiyin converts a NUMERIC read as float64 into whole minor units.
//
// The float is the database driver's representation, not a value the engine
// computes with: everything downstream is int64 tiyin. Rounding here rather
// than truncating means a stored 1234.5649999 reads back as 123456 tiyin
// instead of 123456 or 123457 depending on the wind.
func toTiyin(v float64) int64 {
	if v < 0 {
		return -int64(-v*100 + 0.5)
	}
	return int64(v*100 + 0.5)
}

func fromTiyin(v int64) float64 { return float64(v) / 100 }

func mulTiyin(unit int64, qty *big.Rat) int64 {
	if qty == nil {
		return 0
	}
	r := new(big.Rat).SetInt64(unit)
	r.Mul(r, qty)
	return roundHalfUp(r)
}

// floatToRat converts a NUMERIC(20,4) quantity into an exact rational at the
// column's own precision, so 0.1 becomes 1/10 rather than the nearest float.
func floatToRat(v float64) *big.Rat {
	return big.NewRat(int64(v*10000+copySign(0.5, v)), 10000)
}

func copySign(mag, sign float64) float64 {
	if sign < 0 {
		return -mag
	}
	return mag
}

func orgPointer(c *gin.Context) *uuid.UUID {
	if orgID, ok := middleware.GetOrganizationID(c); ok && orgID != uuid.Nil {
		return &orgID
	}
	return nil
}
