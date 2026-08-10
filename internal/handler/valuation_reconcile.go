package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// GetStockValuationReconciliation godoc
// @Summary Ombor ↔ buxgalteriya solishtiruvi
// @Description Per-account comparison of the valuation layers against the general ledger
// @Tags Inventory - Valuation
// @Produce json
// @Param as_of_date query string false "Compare as of this date (YYYY-MM-DD); default today"
// @Param only_differences query bool false "Return only the accounts that disagree"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /inventory/valuation/reconciliation [get]
//
// The §1.3 invariant, made checkable:
//
//	Σ layer remaining_value  =  stock value  =  the valuation account's balance
//
// The plan asks for a button that shows the difference as 0. This is what it
// calls. A report that can only ever say "fine" would be worthless, so the
// difference is computed per account and the endpoint says plainly which side
// each figure came from — the layers or the ledger — rather than reporting one
// number and leaving the reader to guess which is authoritative.
//
// Note this compares against the GL, NOT against inventory.unit_cost. The
// existing average in the inventory table is a third figure maintained by a
// different path; reconciling to it would prove the two halves of the warehouse
// agree with each other while both drifted from the books, which is precisely
// the failure the invariant exists to catch.
func (h *Handler) GetStockValuationReconciliation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	args := []interface{}{tenantID}
	orgFilter := ""
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		args = append(args, orgID)
		orgFilter = " AND svl.organization_id = $2"
	}

	asOf := c.Query("as_of_date")
	dateFilter := ""
	if asOf != "" {
		args = append(args, asOf)
		dateFilter = " AND svl.layer_date <= $" + strconv.Itoa(len(args))
	}

	// Layer side, grouped by the account the POSTING used — svl.stock_account_id,
	// resolved once when the layer was created.
	//
	// This deliberately does not re-derive the routing (category override, then
	// the product's account, then inventory_type). That chain lives in Go, in
	// getCategoryAccounts and getInventoryAccountByType, and a SQL copy of it
	// here would be a second definition that eventually disagrees — at which
	// point this report announces discrepancies that are only its own
	// disagreement with the postings. A reconciliation that can cry wolf is
	// worse than none, because the next real alarm gets ignored.
	rows, err := h.db.Query(`
		WITH layer_side AS (
			SELECT svl.stock_account_id AS account_id,
			       SUM(svl.remaining_value) AS layer_value,
			       SUM(svl.remaining_qty)   AS layer_qty,
			       COUNT(*)                 AS layer_count
			FROM stock_valuation_layers svl
			WHERE svl.tenant_id = $1
			  AND svl.is_reversed = false
			  AND svl.remaining_qty > 0
			  AND svl.stock_account_id IS NOT NULL`+orgFilter+dateFilter+`
			GROUP BY 1
		),
		gl_side AS (
			SELECT jel.account_id,
			       SUM(jel.debit_amount - jel.credit_amount) AS gl_balance
			FROM journal_entry_lines jel
			JOIN journal_entries je ON je.id = jel.journal_entry_id
			WHERE je.tenant_id = $1
			  AND je.status = 'posted' AND je.deleted_at IS NULL
			GROUP BY 1
		)
		SELECT a.id, a.code, a.name,
		       COALESCE(l.layer_value, 0),
		       COALESCE(l.layer_qty, 0),
		       COALESCE(l.layer_count, 0),
		       COALESCE(g.gl_balance, 0)
		FROM layer_side l
		FULL OUTER JOIN gl_side g ON g.account_id = l.account_id
		JOIN accounts a ON a.id = COALESCE(l.account_id, g.account_id)
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
		  -- Only the accounts stock actually posts to. Without this the report
		  -- would list every account in the chart with a layer value of zero
		  -- and call each one a discrepancy.
		  AND (l.account_id IS NOT NULL OR a.code IN ('1010', '2810', '2910'))
		ORDER BY a.code
	`, args...)
	if err != nil {
		h.log.Error("Failed to reconcile stock valuation", "error", err)
		response.InternalError(c, "Failed to reconcile stock valuation")
		return
	}
	defer rows.Close()

	type line struct {
		AccountID   string  `json:"account_id"`
		AccountCode string  `json:"account_code"`
		AccountName string  `json:"account_name"`
		LayerValue  float64 `json:"layer_value"`
		LayerQty    float64 `json:"layer_qty"`
		LayerCount  int     `json:"layer_count"`
		GLBalance   float64 `json:"gl_balance"`
		Difference  float64 `json:"difference"`
		Balanced    bool    `json:"balanced"`
	}

	onlyDiff := c.Query("only_differences") == "true"
	// Non-nil so an all-clear serialises as [] rather than null, which a client
	// cannot tell from a missing field.
	out := make([]line, 0)
	var totalLayers, totalGL float64
	var worst float64

	for rows.Next() {
		var l line
		if err := rows.Scan(&l.AccountID, &l.AccountCode, &l.AccountName,
			&l.LayerValue, &l.LayerQty, &l.LayerCount, &l.GLBalance); err != nil {
			h.log.Error("Failed to scan reconciliation row", "error", err)
			continue
		}
		l.Difference = l.LayerValue - l.GLBalance
		// Compared at currency precision, not exactly. Layer values are
		// NUMERIC(20,2) and the GL is NUMERIC(20,4), so an exact test would
		// report a rounding artefact as a discrepancy on every line.
		l.Balanced = l.Difference < 0.005 && l.Difference > -0.005
		totalLayers += l.LayerValue
		totalGL += l.GLBalance
		if d := l.Difference; d > worst || -d > worst {
			if d < 0 {
				worst = -d
			} else {
				worst = d
			}
		}
		if onlyDiff && l.Balanced {
			continue
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("Reconciliation iteration failed", "error", err)
		response.InternalError(c, "Failed to reconcile stock valuation")
		return
	}

	response.Success(c, gin.H{
		"as_of_date":         asOf,
		"accounts":           out,
		"total_layer_value":  totalLayers,
		"total_gl_balance":   totalGL,
		"total_difference":   totalLayers - totalGL,
		"largest_difference": worst,
		"balanced":           worst < 0.005,
	})
}
