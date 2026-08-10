package handler

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// Zaxiralarni baholash, bosqich 6: mavjud qoldiqlar migratsiyasi.
//
// "Qoldiqlarni kiritish": one starting layer per product at its current book
// value, so the layer system begins life already agreeing with the warehouse
// and the ledger.
//
// This is also the CHANGEOVER GATE. Before a tenant runs it they have no
// layers, so RecordStockMovement's FIFO path has nothing to drain and the
// reconciliation has nothing to compare — the whole system is inert. After it,
// the layers are authoritative. The plan sequences it last for exactly this
// reason, and it means the switchover is a deliberate act per tenant rather
// than a deploy that changes how everyone's stock is valued at once.
//
// The plan also notes: from this moment the category's method is locked, which
// falls out of §2.1 without any extra code — the opening layers ARE movements.

// GetStockOpeningPreview godoc
// @Summary Qoldiqlarni kiritish — oldindan ko'rish
// @Description What the opening-balance document would create, without creating it
// @Tags Inventory - Valuation
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /inventory/valuation/opening-balance [get]
//
// A preview exists because this document locks every affected category's
// valuation method. Asking someone to trigger that blind, on their whole
// catalogue, would be the kind of irreversible one-click action that deserves
// to be seen first.
func (h *Handler) GetStockOpeningPreview(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID := orgPointer(c)

	rows, err := h.openingCandidates(h.db, tenantID, orgID)
	if err != nil {
		h.log.Error("Failed to preview opening balances", "error", err)
		response.InternalError(c, "Failed to preview opening balances")
		return
	}

	var totalValue float64
	var already int
	for _, r := range rows {
		totalValue += r.Value
		if r.HasLayers {
			already++
		}
	}

	response.Success(c, gin.H{
		"products":       rows,
		"product_count":  len(rows),
		"total_value":    totalValue,
		"already_seeded": already,
		"note":           "Qoldiqlar kiritilgandan so'ng kategoriyalarning baholash usuli qulflanadi",
	})
}

type openingCandidate struct {
	ProductID   uuid.UUID `json:"product_id"`
	ProductName string    `json:"product_name"`
	CategoryID  *string   `json:"category_id"`
	Quantity    float64   `json:"quantity"`
	UnitCost    float64   `json:"unit_cost"`
	Value       float64   `json:"value"`
	HasLayers   bool      `json:"has_layers"`
}

// openingCandidates lists every product holding stock, valued at the average
// the inventory register already carries.
//
// unit_cost from `inventory` is deliberately the source: it is what the books
// have been using, so seeding from it means the opening layers agree with the
// existing ledger balance on day one. Seeding from a price list or the last
// purchase price would have created a difference on the very first
// reconciliation, and someone would have had to explain it.
func (h *Handler) openingCandidates(q dbRowsQuerier, tenantID uuid.UUID, orgID *uuid.UUID) ([]openingCandidate, error) {
	args := []interface{}{tenantID}
	orgFilter := ""
	if orgID != nil {
		args = append(args, *orgID)
		orgFilter = " AND (i.organization_id = $2 OR i.organization_id IS NULL)"
	}

	rows, err := q.Query(`
		SELECT p.id, p.name, p.category_id,
		       SUM(i.quantity_on_hand) AS qty,
		       COALESCE(MAX(i.unit_cost), 0) AS unit_cost,
		       EXISTS (SELECT 1 FROM stock_valuation_layers svl
		               WHERE svl.tenant_id = p.tenant_id AND svl.product_id = p.id
		                 AND svl.is_reversed = false) AS has_layers
		FROM products p
		JOIN inventory i ON i.product_id = p.id AND i.tenant_id = p.tenant_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`+orgFilter+`
		GROUP BY p.id, p.name, p.category_id
		HAVING SUM(i.quantity_on_hand) > 0
		ORDER BY p.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]openingCandidate, 0)
	for rows.Next() {
		var r openingCandidate
		var cat uuid.NullUUID
		if err := rows.Scan(&r.ProductID, &r.ProductName, &cat, &r.Quantity, &r.UnitCost, &r.HasLayers); err != nil {
			return nil, err
		}
		if cat.Valid {
			s := cat.UUID.String()
			r.CategoryID = &s
		}
		r.Value = fromTiyin(mulTiyin(toTiyin(r.UnitCost), floatToRat(r.Quantity)))
		out = append(out, r)
	}
	return out, rows.Err()
}

// PostStockOpeningBalance godoc
// @Summary Qoldiqlarni kiritish
// @Description Seeds one opening valuation layer per product holding stock
// @Tags Inventory - Valuation
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /inventory/valuation/opening-balance [post]
//
// One transaction for the whole catalogue. A half-migrated tenant — some
// products with layers and some without — would value one part of the warehouse
// by layers and the other by the old average, and no report could be trusted
// while it lasted.
//
// Products that already have layers are SKIPPED rather than treated as an
// error, so a run interrupted by a timeout can simply be repeated. That makes
// the document idempotent in the way that matters: running it twice does not
// double the stock.
func (h *Handler) PostStockOpeningBalance(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	orgID := orgPointer(c)

	var input struct {
		AsOfDate string `json:"as_of_date"`
		Notes    string `json:"notes"`
	}
	_ = c.ShouldBindJSON(&input)

	asOf := time.Now()
	if input.AsOfDate != "" {
		parsed, perr := time.Parse("2006-01-02", input.AsOfDate)
		if perr != nil {
			response.BadRequest(c, "Invalid as_of_date, expected YYYY-MM-DD")
			return
		}
		asOf = parsed
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer func() { _ = tx.Rollback() }()

	candidates, err := h.openingCandidates(tx, tenantID, orgID)
	if err != nil {
		h.log.Error("Failed to load opening candidates", "error", err)
		response.InternalError(c, "Failed to load opening balances")
		return
	}

	docID := uuid.New()
	var created, skipped int
	var totalValue int64

	for _, cand := range candidates {
		if cand.HasLayers {
			skipped++
			continue
		}
		qty := floatToRat(cand.Quantity)
		if qty.Sign() <= 0 {
			continue
		}
		value := mulTiyin(toTiyin(cand.UnitCost), qty)

		_, acc, aerr := h.resolveValuationContext(tx, StockMovement{
			TenantID: tenantID, OrganizationID: orgID, ProductID: cand.ProductID,
		})
		if aerr != nil {
			h.log.Error("Failed to resolve accounts for opening balance", "error", aerr, "product", cand.ProductID)
			response.InternalError(c, "Failed to resolve accounts for opening balance")
			return
		}

		layerID := uuid.New()
		if _, err := tx.Exec(`
			INSERT INTO stock_valuation_layers (
				id, tenant_id, organization_id, product_id, stock_account_id,
				layer_date, source_type, source_doc_id, source_doc_number,
				quantity, unit_cost, value, remaining_qty, remaining_value, created_by
			) VALUES ($1,$2,$3,$4,$5,$6::date,'opening_balance',$7,$8,$9,$10,$11,$9,$11,$12)`,
			layerID, tenantID, orgID, cand.ProductID, nilIfEmptyUUID(acc.Stock),
			asOf.Format("2006-01-02"), docID, "OPEN-"+asOf.Format("20060102"),
			cand.Quantity, cand.UnitCost, fromTiyin(value), nilIfZeroUUID(userID),
		); err != nil {
			h.log.Error("Failed to insert opening layer", "error", err, "product", cand.ProductID)
			response.InternalError(c, "Failed to seed opening balances")
			return
		}

		// AVCO needs its running state seeded too, or the first issue after the
		// migration would find an empty average and refuse for lack of stock.
		if _, err := tx.Exec(`
			INSERT INTO product_avco_state (tenant_id, organization_id, product_id, quantity, value, last_unit_cost, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,NOW())
			ON CONFLICT (tenant_id, organization_id, product_id) DO UPDATE
			SET quantity = EXCLUDED.quantity, value = EXCLUDED.value,
			    last_unit_cost = EXCLUDED.last_unit_cost, updated_at = NOW()`,
			tenantID, orgKey(orgID), cand.ProductID,
			cand.Quantity, fromTiyin(value), cand.UnitCost,
		); err != nil {
			h.log.Error("Failed to seed AVCO state", "error", err, "product", cand.ProductID)
			response.InternalError(c, "Failed to seed opening balances")
			return
		}

		created++
		totalValue += value
	}

	// No journal entry.
	//
	// The plan's §6 describes one against a suspense account, and that is right
	// for a system whose ledger does not yet know about this stock. Here it
	// does: inventory.unit_cost has been posting to the same valuation accounts
	// all along, so the balance is already on 2910/2810/1010. Posting again
	// would DOUBLE it. What this document does is give the existing balance a
	// layer structure, not introduce it.
	//
	// The reconciliation report is the check: run it straight after and the
	// difference should be zero, because both sides were derived from the same
	// unit_cost.

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit opening balances", "error", err)
		response.InternalError(c, "Failed to commit opening balances: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"document_id":    docID,
		"as_of_date":     asOf.Format("2006-01-02"),
		"layers_created": created,
		"skipped":        skipped,
		"total_value":    fromTiyin(totalValue),
		"note":           "Kategoriyalarning baholash usuli endi qulflangan. Solishtiruvni ishga tushiring.",
	})
}

// GetStockValuationReport godoc
// @Summary Zaxiralar bahosi
// @Description Stock value by product with a drill-down to the layers behind it
// @Tags Inventory - Valuation
// @Produce json
// @Param as_of_date query string false "Value as of this date (YYYY-MM-DD)"
// @Param product_id query string false "Drill down to one product's layers"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /inventory/valuation/report [get]
//
// §3.4: under FIFO the remaining units do NOT share one cost — each layer keeps
// its own — so the report gives the total value and a unit price DERIVED as
// value ÷ quantity, clearly a reference figure rather than a price anything is
// held at.
func (h *Handler) GetStockValuationReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	args := []interface{}{tenantID}
	where := ""
	if v := c.Query("as_of_date"); v != "" {
		args = append(args, v)
		where += " AND svl.layer_date <= $2"
	}
	if v := c.Query("product_id"); v != "" {
		args = append(args, v)
		where += " AND svl.product_id = $" + strconv.Itoa(len(args))
	}

	rows, err := h.db.Query(`
		SELECT svl.product_id, p.name,
		       COALESCE(pc.name, '—') AS category_name,
		       SUM(svl.remaining_qty)   AS qty,
		       SUM(svl.remaining_value) AS value,
		       COUNT(*)                 AS layer_count,
		       MIN(svl.layer_date)      AS oldest
		FROM stock_valuation_layers svl
		JOIN products p ON p.id = svl.product_id
		LEFT JOIN product_categories pc ON pc.id = p.category_id
		WHERE svl.tenant_id = $1 AND svl.is_reversed = false AND svl.remaining_qty > 0`+where+`
		GROUP BY svl.product_id, p.name, pc.name
		ORDER BY SUM(svl.remaining_value) DESC`, args...)
	if err != nil {
		h.log.Error("Failed to build valuation report", "error", err)
		response.InternalError(c, "Failed to build valuation report")
		return
	}
	defer rows.Close()

	type line struct {
		ProductID    uuid.UUID `json:"product_id"`
		ProductName  string    `json:"product_name"`
		CategoryName string    `json:"category_name"`
		Quantity     float64   `json:"quantity"`
		Value        float64   `json:"value"`
		UnitValue    float64   `json:"unit_value"`
		LayerCount   int       `json:"layer_count"`
		OldestLayer  string    `json:"oldest_layer"`
	}
	out := make([]line, 0)
	var totalValue float64
	for rows.Next() {
		var l line
		var oldest sql.NullTime
		if err := rows.Scan(&l.ProductID, &l.ProductName, &l.CategoryName,
			&l.Quantity, &l.Value, &l.LayerCount, &oldest); err != nil {
			h.log.Error("Failed to scan valuation row", "error", err)
			continue
		}
		if l.Quantity != 0 {
			// Derived for reference only. Under FIFO the remaining units really
			// do have different costs, so this is an average and is named as
			// one rather than being called a unit price.
			l.UnitValue = l.Value / l.Quantity
		}
		if oldest.Valid {
			l.OldestLayer = oldest.Time.Format("2006-01-02")
		}
		totalValue += l.Value
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("Valuation report iteration failed", "error", err)
		response.InternalError(c, "Failed to build valuation report")
		return
	}

	response.Success(c, gin.H{
		"as_of_date":  c.Query("as_of_date"),
		"products":    out,
		"total_value": totalValue,
	})
}

// GetStockMarginReport godoc
// @Summary Sotuv tannarxi / marja
// @Description Revenue less cost of sales, by product, from the consumption records
// @Tags Inventory - Valuation
// @Produce json
// @Param date_from query string false "From date (YYYY-MM-DD)"
// @Param date_to query string false "To date (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /inventory/valuation/margin [get]
//
// Cost comes from the consumption records — what the layers actually gave up —
// not from a product's current cost price. Those are different numbers whenever
// prices have moved, and only the first one is what the sale really cost.
func (h *Handler) GetStockMarginReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	args := []interface{}{tenantID}
	where := ""
	if v := c.Query("date_from"); v != "" {
		args = append(args, v)
		where += " AND svc.issue_date >= $" + strconv.Itoa(len(args))
	}
	if v := c.Query("date_to"); v != "" {
		args = append(args, v)
		where += " AND svc.issue_date <= $" + strconv.Itoa(len(args))
	}

	rows, err := h.db.Query(`
		SELECT svc.product_id, p.name,
		       SUM(svc.quantity) AS qty,
		       SUM(svc.value)    AS cost
		FROM stock_valuation_consumptions svc
		JOIN products p ON p.id = svc.product_id
		WHERE svc.tenant_id = $1 AND svc.is_reversed = false
		  AND svc.source_type IN ('sale', 'pos_sale', 'sales_delivery')`+where+`
		GROUP BY svc.product_id, p.name
		ORDER BY SUM(svc.value) DESC`, args...)
	if err != nil {
		h.log.Error("Failed to build margin report", "error", err)
		response.InternalError(c, "Failed to build margin report")
		return
	}
	defer rows.Close()

	type line struct {
		ProductID   uuid.UUID `json:"product_id"`
		ProductName string    `json:"product_name"`
		Quantity    float64   `json:"quantity"`
		Cost        float64   `json:"cost"`
	}
	out := make([]line, 0)
	var totalCost float64
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.ProductID, &l.ProductName, &l.Quantity, &l.Cost); err != nil {
			h.log.Error("Failed to scan margin row", "error", err)
			continue
		}
		totalCost += l.Cost
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("Margin report iteration failed", "error", err)
		response.InternalError(c, "Failed to build margin report")
		return
	}

	response.Success(c, gin.H{
		"date_from":  c.Query("date_from"),
		"date_to":    c.Query("date_to"),
		"products":   out,
		"total_cost": totalCost,
		// Revenue is deliberately absent rather than guessed. Joining sales
		// lines back to a consumption is a document-level mapping that differs
		// per source (invoice, POS receipt, delivery note), and reporting a
		// margin computed from the wrong half is worse than reporting cost
		// alone and letting the sales report supply the other side.
		"note": "Tushum savdo hisobotidan olinadi; bu yerda faqat tannarx",
	})
}
