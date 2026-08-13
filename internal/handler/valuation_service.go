package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// Zaxiralarni baholash, bosqich 5: the one place a stock movement is recorded.
//
// §3 opens with a conveyor that every method shares:
//
//	document → post → create layers (receipt) / consume layers (issue)
//	         → cost the movement by the method
//	         → post the journal entry
//
// This is that conveyor, as a single function. Every document path calls
// RecordStockMovement and nothing else re-implements any part of it. That is
// the whole design goal: the reason goods-delivery once credited inventory
// without debiting COGS, and production orders posted a debit with no matching
// credit, is that each path built its own posting.
//
// Everything runs on the caller's transaction. A movement that cannot be costed
// or cannot be posted must take its document down with it — a document that
// exists without its valuation is the defect this file is here to prevent.

// StockMovement is one product moving, in one document.
type StockMovement struct {
	TenantID       uuid.UUID
	OrganizationID *uuid.UUID
	ProductID      uuid.UUID
	Operation      StockOperation
	Date           time.Time
	Quantity       *big.Rat // always positive; Operation carries the direction

	// Receipts only: what was actually paid per unit, in tiyin. Ignored for
	// issues, which are costed by the method.
	UnitCost int64

	SourceType   string
	SourceDocID  uuid.UUID
	SourceDocNum string
	Description  string
	CreatedBy    uuid.UUID

	// Returns only: the document whose issue is being reversed. The return is
	// priced from that issue's consumption records, not at today's cost.
	OriginalDocType string
	OriginalDocID   uuid.UUID

	// POS and other high-volume paths: write the layers and consumptions but
	// leave the journal entry to a later aggregated posting (§4). Detail is
	// preserved at the layer level; only the GL is summarised.
	DeferPosting bool
}

// StockMovementResult is what the movement cost and what it wrote.
type StockMovementResult struct {
	Cost           int64
	Method         CostMethod
	LayerID        *uuid.UUID
	JournalEntryID *uuid.UUID
	Deferred       bool
}

var ErrMovementNotCosted = errors.New("stock movement could not be costed")

// RecordStockMovement is the conveyor. It resolves the effective method,
// writes or drains layers, costs the movement, and posts the entry.
func (h *Handler) RecordStockMovement(tx *sql.Tx, m StockMovement) (StockMovementResult, error) {
	var res StockMovementResult
	if m.Quantity == nil || m.Quantity.Sign() <= 0 {
		return res, ErrNonPositiveQty
	}

	method, accounts, err := h.resolveValuationContext(tx, m)
	if err != nil {
		return res, err
	}
	res.Method = method

	// §2.4 backdating: "Har qanday ombor hujjatining sanasi ≥ period_lock_date".
	// Reuses the ledger's own period lock (fiscal periods / fiscal year /
	// accounting periods) rather than a second, stock-only rule — a stock
	// movement whose date sits in a closed period would post a cost into books
	// that are already signed off, which is exactly what closing a period is
	// for. Same helper the fixed-assets module calls, so one lock governs
	// every module.
	if msg := h.checkPeriodLock(m.TenantID, m.Date); msg != "" {
		return res, errors.New(msg)
	}

	// §2.4: under AVCO a document dated before this product's last movement
	// would have had to change every average computed after it, and those costs
	// are already posted. Checked here rather than in each document handler,
	// which is the only way it actually holds for all of them.
	if last, lerr := h.lastMovementDate(tx, m.TenantID, m.ProductID); lerr != nil {
		return res, lerr
	} else if msg := guardAVCOChronology(method, m.Date, last); msg != "" {
		return res, errors.New(msg)
	}

	var alreadyPosted bool
	if isReceiptOperation(m.Operation) {
		res, alreadyPosted, err = h.recordReceipt(tx, m, method, accounts, res)
	} else {
		res, err = h.recordIssue(tx, m, method, accounts, res)
	}
	if err != nil {
		return res, err
	}

	// A standard-cost receipt posts its own three-sided entry (stock at
	// standard, the variance, the payable). The generic pair below must not
	// post it again.
	if alreadyPosted {
		return res, nil
	}
	if m.DeferPosting {
		res.Deferred = true
		return res, nil
	}

	lines, err := h.buildMovementLines(m, accounts, res.Cost)
	if err != nil {
		return res, err
	}
	if len(lines) == 0 {
		// A zero-value movement posts nothing. It is not an error: a receipt of
		// goods that genuinely cost nothing (a sample, a promotional unit) is
		// real, and its quantity is still recorded on the layer.
		return res, nil
	}

	jeID, err := h.postValuationEntry(tx, valuationEntryArgs{
		TenantID:       m.TenantID,
		OrganizationID: m.OrganizationID,
		EntryDate:      m.Date.Format("2006-01-02"),
		Description:    m.Description,
		SourceType:     m.SourceType,
		SourceID:       m.SourceDocID,
		Lines:          lines,
		CreatedBy:      m.CreatedBy,
	})
	if err != nil {
		return res, err
	}
	res.JournalEntryID = &jeID

	if res.LayerID != nil {
		if _, err := tx.Exec(`UPDATE stock_valuation_layers SET journal_entry_id = $1 WHERE id = $2`,
			jeID, *res.LayerID); err != nil {
			return res, err
		}
	}
	if _, err := tx.Exec(`
		UPDATE stock_valuation_consumptions SET journal_entry_id = $1
		WHERE tenant_id = $2 AND source_type = $3 AND source_doc_id = $4 AND journal_entry_id IS NULL`,
		jeID, m.TenantID, m.SourceType, m.SourceDocID); err != nil {
		return res, err
	}

	return res, nil
}

func isReceiptOperation(op StockOperation) bool {
	switch op {
	case OpSupplierReceipt, OpCustomerReturn, OpCountSurplus:
		return true
	}
	return false
}

// resolveValuationContext works out the method and the accounts once, so the
// receipt and issue paths cannot reach different answers.
func (h *Handler) resolveValuationContext(tx *sql.Tx, m StockMovement) (CostMethod, ValuationAccounts, error) {
	var categoryMethod sql.NullString
	if err := tx.QueryRow(`
		SELECT pc.cost_method FROM products p
		LEFT JOIN product_categories pc ON pc.id = p.category_id
		WHERE p.id = $1 AND p.tenant_id = $2`, m.ProductID, m.TenantID).Scan(&categoryMethod); err != nil {
		return "", ValuationAccounts{}, err
	}
	method := EffectiveCostMethod(categoryMethod.String, h.readValuationSetting(m.TenantID).Method)

	ca := getCategoryAccounts(tx, m.TenantID, m.OrganizationID, m.ProductID)
	stock := ca.StockValuationAccountID
	if stock == uuid.Nil {
		stock = getInventoryAccountByType(tx, m.TenantID, m.OrganizationID, m.ProductID)
	}

	acc := ValuationAccounts{
		Stock:    uuidOrEmpty(stock),
		COGS:     uuidOrEmpty(ca.ExpenseAccountID),
		Expense:  uuidOrEmpty(ca.ExpenseAccountID),
		Variance: uuidOrEmpty(h.resolveVarianceAccount(tx, m.TenantID, m.OrganizationID, m.ProductID)),
		Payables: uuidOrEmpty(findAccount(tx, m.TenantID, m.OrganizationID, "yetkazib beruvchi", "6010")),
		Surplus:  uuidOrEmpty(findAccount(tx, m.TenantID, m.OrganizationID, "boshqa operatsion daromad", "9390")),
		Shortage: uuidOrEmpty(findAccount(tx, m.TenantID, m.OrganizationID, "kamomad", "5910")),
	}
	return method, acc, nil
}

func uuidOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func (h *Handler) lastMovementDate(tx *sql.Tx, tenantID, productID uuid.UUID) (*time.Time, error) {
	var d sql.NullTime
	if err := tx.QueryRow(`
		SELECT MAX(d) FROM (
			SELECT MAX(layer_date) AS d FROM stock_valuation_layers
			WHERE tenant_id = $1 AND product_id = $2 AND is_reversed = false
			UNION ALL
			SELECT MAX(issue_date) FROM stock_valuation_consumptions
			WHERE tenant_id = $1 AND product_id = $2 AND is_reversed = false
		) x`, tenantID, productID).Scan(&d); err != nil {
		return nil, err
	}
	if !d.Valid {
		return nil, nil
	}
	return &d.Time, nil
}

// recordReceipt creates the layer and, for AVCO, moves the running average.
func (h *Handler) recordReceipt(tx *sql.Tx, m StockMovement, method CostMethod, acc ValuationAccounts, res StockMovementResult) (StockMovementResult, bool, error) {
	unitCost := m.UnitCost
	value := mulTiyin(unitCost, m.Quantity)
	postedHere := false

	switch {
	case m.Operation == OpCustomerReturn && m.OriginalDocID != uuid.Nil:
		// §3.1/§3.2: a return re-enters at the ORIGINAL issue cost, in
		// proportion to what is coming back. Valuing it at today's price is how
		// a business books a profit purely by selling and un-selling one item
		// across a price change.
		origQty, origCost, err := h.originalIssue(tx, m.TenantID, m.ProductID, m.OriginalDocType, m.OriginalDocID)
		if err != nil {
			return res, false, err
		}
		if origQty != nil && origQty.Sign() > 0 {
			v, rerr := ReturnValue(m.Quantity, origQty, origCost)
			if rerr != nil {
				return res, false, rerr
			}
			value = v
		}
	case method == CostMethodStandard:
		std, err := h.standardCostOf(tx, m.TenantID, m.ProductID)
		if err != nil {
			return res, false, err
		}
		layerValue, variance, verr := StandardReceiptVariance(m.Quantity, unitCost, std)
		if verr != nil {
			return res, false, verr
		}
		value = layerValue
		if variance != 0 && m.Operation == OpSupplierReceipt && !m.DeferPosting {
			lines, lerr := BuildStandardReceiptLines(acc, layerValue, variance, m.Description)
			if lerr != nil {
				return res, false, lerr
			}
			id, perr := h.postValuationEntry(tx, valuationEntryArgs{
				TenantID: m.TenantID, OrganizationID: m.OrganizationID,
				EntryDate: m.Date.Format("2006-01-02"), Description: m.Description,
				SourceType: m.SourceType, SourceID: m.SourceDocID,
				Lines: lines, CreatedBy: m.CreatedBy,
			})
			if perr != nil {
				return res, false, perr
			}
			res.JournalEntryID = &id
			postedHere = true
		}
	}

	layerID := uuid.New()
	qty, _ := m.Quantity.Float64()
	if _, err := tx.Exec(`
		INSERT INTO stock_valuation_layers (
			id, tenant_id, organization_id, product_id, stock_account_id,
			layer_date, source_type, source_doc_id, source_doc_number,
			quantity, unit_cost, value, remaining_qty, remaining_value, created_by
		) VALUES ($1,$2,$3,$4,$5,$6::date,$7,$8,$9,$10,$11,$12,$10,$12,$13)`,
		layerID, m.TenantID, m.OrganizationID, m.ProductID, nilIfEmptyUUID(acc.Stock),
		m.Date.Format("2006-01-02"), m.SourceType, nilIfZeroUUID(m.SourceDocID), m.SourceDocNum,
		qty, fromTiyin(divTiyin(value, m.Quantity)), fromTiyin(value), nilIfZeroUUID(m.CreatedBy),
	); err != nil {
		return res, false, err
	}
	res.LayerID = &layerID
	res.Cost = value

	if method == CostMethodAVCO {
		if err := h.bumpAVCO(tx, m, m.Quantity, value); err != nil {
			return res, false, err
		}
	}
	if postedHere && res.LayerID != nil && res.JournalEntryID != nil {
		if _, err := tx.Exec(`UPDATE stock_valuation_layers SET journal_entry_id = $1 WHERE id = $2`,
			*res.JournalEntryID, layerID); err != nil {
			return res, false, err
		}
	}
	return res, postedHere, nil
}

// recordIssue drains layers and costs the movement by the method.
func (h *Handler) recordIssue(tx *sql.Tx, m StockMovement, method CostMethod, acc ValuationAccounts, res StockMovementResult) (StockMovementResult, error) {
	layers, err := h.openLayers(tx, m.TenantID, m.ProductID)
	if err != nil {
		return res, err
	}

	switch method {
	case CostMethodStandard:
		std, serr := h.standardCostOf(tx, m.TenantID, m.ProductID)
		if serr != nil {
			return res, serr
		}
		cost, cerr := StandardIssue(m.Quantity, std)
		if cerr != nil {
			return res, cerr
		}
		res.Cost = cost
		// The layers are still drained, for the audit trail and so the quantity
		// on them matches the quantity in the warehouse. But the VALUE they are
		// left holding must not be the FIFO value: under standard costing stock
		// is worth Q × standard by definition (§3.4), so without the rescale the
		// layers would disagree with both the ledger and the method itself, and
		// the §1.3 reconciliation would report a difference on every product.
		if _, derr := FIFOIssue(layers, m.Quantity); derr != nil {
			return res, derr
		}
		RescaleLayersToStandard(layers, std)
	case CostMethodAVCO:
		state, serr := h.avcoState(tx, m)
		if serr != nil {
			return res, serr
		}
		cost, next, ierr := AVCOIssue(state, m.Quantity)
		if ierr != nil {
			return res, ierr
		}
		res.Cost = cost
		if err := h.writeAVCO(tx, m, next); err != nil {
			return res, err
		}
		// The plan calls this "texnik iste'mol" — the layers are drained FIFO
		// purely for audit, and their total is then forced back to avco_value,
		// which IS the stock value under this method (§3.4).
		if _, derr := FIFOIssue(layers, m.Quantity); derr != nil {
			return res, derr
		}
		RescaleLayersToTotal(layers, next.Value)
	default: // FIFO
		out, ierr := FIFOIssue(layers, m.Quantity)
		if ierr != nil {
			return res, ierr
		}
		res.Cost = out.Cost
	}

	if err := h.persistLayerDrain(tx, m, layers); err != nil {
		return res, err
	}
	return res, nil
}

// buildMovementLines turns the costed movement into balanced lines.
func (h *Handler) buildMovementLines(m StockMovement, acc ValuationAccounts, cost int64) ([]JournalLine, error) {
	if cost == 0 {
		return nil, nil
	}
	memo := m.Description
	if memo == "" {
		memo = string(m.Operation)
	}
	return BuildStockLines(m.Operation, acc, cost, memo)
}

// openLayers loads a product's undrained layers in FIFO order.
func (h *Handler) openLayers(tx *sql.Tx, tenantID, productID uuid.UUID) ([]Layer, error) {
	rows, err := tx.Query(`
		SELECT id, layer_date, remaining_qty, remaining_value
		FROM stock_valuation_layers
		WHERE tenant_id = $1 AND product_id = $2
		  AND is_reversed = false AND remaining_qty > 0
		ORDER BY layer_date, created_at, id`, tenantID, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var layers []Layer
	var seq int64
	for rows.Next() {
		var id string
		var d time.Time
		var qty, val float64
		if err := rows.Scan(&id, &d, &qty, &val); err != nil {
			return nil, err
		}
		seq++
		layers = append(layers, Layer{
			ID: id, SeqNo: seq, DateOrdinal: d.Unix() / 86400,
			RemainingQty: floatToRat(qty), RemainingValue: toTiyin(val),
		})
	}
	return layers, rows.Err()
}

// persistLayerDrain writes back what the engine decided and records what each
// layer gave up.
func (h *Handler) persistLayerDrain(tx *sql.Tx, m StockMovement, layers []Layer) error {
	before, err := h.openLayers(tx, m.TenantID, m.ProductID)
	if err != nil {
		return err
	}
	prior := make(map[string]Layer, len(before))
	for _, l := range before {
		prior[l.ID] = l
	}

	for _, l := range layers {
		p, ok := prior[l.ID]
		if !ok {
			continue
		}
		takenQty := new(big.Rat).Sub(p.RemainingQty, l.RemainingQty)
		takenValue := p.RemainingValue - l.RemainingValue
		if takenQty.Sign() == 0 && takenValue == 0 {
			continue
		}

		qty, _ := l.RemainingQty.Float64()
		if _, err := tx.Exec(`
			UPDATE stock_valuation_layers
			SET remaining_qty = $1, remaining_value = $2 WHERE id = $3`,
			qty, fromTiyin(l.RemainingValue), l.ID); err != nil {
			return err
		}

		tq, _ := takenQty.Float64()
		if _, err := tx.Exec(`
			INSERT INTO stock_valuation_consumptions (
				tenant_id, organization_id, product_id, layer_id, issue_date,
				source_type, source_doc_id, source_doc_number, quantity, value, created_by
			) VALUES ($1,$2,$3,$4,$5::date,$6,$7,$8,$9,$10,$11)`,
			m.TenantID, m.OrganizationID, m.ProductID, l.ID, m.Date.Format("2006-01-02"),
			m.SourceType, nilIfZeroUUID(m.SourceDocID), m.SourceDocNum,
			tq, fromTiyin(takenValue), nilIfZeroUUID(m.CreatedBy),
		); err != nil {
			return err
		}
	}
	return nil
}

// originalIssue totals what a document's issue actually cost, so a return can
// be priced from it.
func (h *Handler) originalIssue(tx *sql.Tx, tenantID, productID uuid.UUID, docType string, docID uuid.UUID) (*big.Rat, int64, error) {
	var qty sql.NullFloat64
	var value sql.NullFloat64
	if err := tx.QueryRow(`
		SELECT SUM(quantity), SUM(value) FROM stock_valuation_consumptions
		WHERE tenant_id = $1 AND product_id = $2 AND source_doc_id = $3
		  AND ($4 = '' OR source_type = $4) AND is_reversed = false`,
		tenantID, productID, docID, docType).Scan(&qty, &value); err != nil {
		return nil, 0, err
	}
	if !qty.Valid || qty.Float64 <= 0 {
		return nil, 0, nil
	}
	return floatToRat(qty.Float64), toTiyin(value.Float64), nil
}

func (h *Handler) standardCostOf(tx *sql.Tx, tenantID, productID uuid.UUID) (int64, error) {
	var v float64
	if err := tx.QueryRow(`SELECT COALESCE(standard_cost, 0) FROM products WHERE id = $1 AND tenant_id = $2`,
		productID, tenantID).Scan(&v); err != nil {
		return 0, err
	}
	return toTiyin(v), nil
}

func (h *Handler) avcoState(tx *sql.Tx, m StockMovement) (AVCOState, error) {
	var qty, value float64
	err := tx.QueryRow(`
		SELECT quantity, value FROM product_avco_state
		WHERE tenant_id = $1 AND organization_id = $2 AND product_id = $3`,
		m.TenantID, orgKey(m.OrganizationID), m.ProductID).Scan(&qty, &value)
	if err == sql.ErrNoRows {
		return AVCOState{Qty: new(big.Rat)}, nil
	}
	if err != nil {
		return AVCOState{}, err
	}
	return AVCOState{Qty: floatToRat(qty), Value: toTiyin(value)}, nil
}

func (h *Handler) bumpAVCO(tx *sql.Tx, m StockMovement, qty *big.Rat, value int64) error {
	state, err := h.avcoState(tx, m)
	if err != nil {
		return err
	}
	next, err := AVCOReceipt(state, qty, value)
	if err != nil {
		return err
	}
	return h.writeAVCO(tx, m, next)
}

func (h *Handler) writeAVCO(tx *sql.Tx, m StockMovement, s AVCOState) error {
	qty, _ := s.Qty.Float64()
	unit := 0.0
	if s.Qty.Sign() > 0 {
		unit = fromTiyin(s.Value) / qty
	}
	_, err := tx.Exec(`
		INSERT INTO product_avco_state (tenant_id, organization_id, product_id, quantity, value, last_unit_cost, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,NOW())
		ON CONFLICT (tenant_id, organization_id, product_id) DO UPDATE
		SET quantity = EXCLUDED.quantity, value = EXCLUDED.value,
		    last_unit_cost = CASE WHEN EXCLUDED.quantity > 0 THEN EXCLUDED.last_unit_cost
		                          ELSE product_avco_state.last_unit_cost END,
		    updated_at = NOW()`,
		m.TenantID, orgKey(m.OrganizationID), m.ProductID, qty, fromTiyin(s.Value), unit)
	return err
}

// orgKey maps a missing organization to the nil UUID, matching the NOT NULL
// default on product_avco_state. Two NULLs do not conflict in a unique index,
// so a nullable key would have accumulated one state row per receipt.
func orgKey(orgID *uuid.UUID) uuid.UUID {
	if orgID == nil {
		return uuid.Nil
	}
	return *orgID
}

func nilIfEmptyUUID(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// divTiyin derives a per-unit cost for display. It is stored on the layer for
// reference only — every calculation reads `value` (§3.5, first golden rule).
func divTiyin(value int64, qty *big.Rat) int64 {
	if qty == nil || qty.Sign() == 0 {
		return 0
	}
	r := new(big.Rat).SetInt64(value)
	r.Quo(r, qty)
	return roundHalfUp(r)
}

// PostAggregatedMovements collapses a batch of deferred movements into ONE
// journal entry (§4).
//
// POS writes a receipt per sale; posting a journal entry for each would sink
// both the ledger and its own throughput. Detail is not lost — the layers and
// consumptions are written per movement as usual, and only the GL is
// summarised, which is exactly what the plan asks for.
func (h *Handler) PostAggregatedMovements(tx *sql.Tx, tenantID uuid.UUID, orgID *uuid.UUID, sourceType string, sessionID uuid.UUID, entryDate time.Time, description string, createdBy uuid.UUID) (*uuid.UUID, error) {
	rows, err := tx.Query(`
		SELECT svc.product_id, SUM(svc.value)
		FROM stock_valuation_consumptions svc
		WHERE svc.tenant_id = $1 AND svc.source_type = $2 AND svc.source_doc_id = $3
		  AND svc.journal_entry_id IS NULL AND svc.is_reversed = false
		GROUP BY svc.product_id`, tenantID, sourceType, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type bucket struct {
		productID uuid.UUID
		value     int64
	}
	var buckets []bucket
	for rows.Next() {
		var b bucket
		var v float64
		if err := rows.Scan(&b.productID, &v); err != nil {
			return nil, err
		}
		b.value = toTiyin(v)
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(buckets) == 0 {
		return nil, nil
	}

	// Grouped by product because the stock account is a product-level routing:
	// raw materials, finished goods and goods for resale post to different
	// accounts, so one lump sum would put them all in whichever account the
	// first product happened to use.
	var lines []JournalLine
	for _, b := range buckets {
		if b.value == 0 {
			continue
		}
		_, acc, aerr := h.resolveValuationContext(tx, StockMovement{
			TenantID: tenantID, OrganizationID: orgID, ProductID: b.productID,
		})
		if aerr != nil {
			return nil, aerr
		}
		part, berr := BuildStockLines(OpSaleIssue, acc, b.value, description)
		if berr != nil {
			return nil, berr
		}
		lines = append(lines, part...)
	}
	if len(lines) == 0 {
		return nil, nil
	}
	if err := assertBalanced(lines); err != nil {
		return nil, err
	}

	jeID, err := h.postValuationEntry(tx, valuationEntryArgs{
		TenantID: tenantID, OrganizationID: orgID,
		EntryDate: entryDate.Format("2006-01-02"), Description: description,
		SourceType: sourceType, SourceID: sessionID,
		Lines: lines, CreatedBy: createdBy,
	})
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`
		UPDATE stock_valuation_consumptions SET journal_entry_id = $1
		WHERE tenant_id = $2 AND source_type = $3 AND source_doc_id = $4 AND journal_entry_id IS NULL`,
		jeID, tenantID, sourceType, sessionID); err != nil {
		return nil, err
	}
	return &jeID, nil
}

// StornoDocument reverses a document's stock effect (§2.6). Refuses rather than
// corrupts when the goods have already moved on.
func (h *Handler) StornoDocument(tx *sql.Tx, tenantID uuid.UUID, sourceType string, sourceID uuid.UUID) error {
	plan, err := h.planStorno(tx, tenantID, sourceType, sourceID)
	if err != nil {
		return err
	}
	if plan.Blocked != "" {
		return fmt.Errorf("%s", plan.Blocked)
	}
	return applyStorno(tx, plan)
}
