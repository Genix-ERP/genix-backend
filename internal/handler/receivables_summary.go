package handler

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// GetSalesInvoicesSummary godoc
// @Summary Accounts-receivable totals
// @Description The AR tab's metric cards and aging buckets over the whole filtered set
// @Tags Sales - Invoices
// @Produce json
// @Param status query string false "Comma-separated document statuses"
// @Param payment_status query string false "Comma-separated payment statuses"
// @Param customer_id query string false "Filter by customer"
// @Param date_from query string false "Invoice date from (YYYY-MM-DD)"
// @Param date_to query string false "Invoice date to (YYYY-MM-DD)"
// @Param overdue query bool false "Only overdue invoices"
// @Param search query string false "Invoice number or customer name"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /sales-invoices/summary [get]
//
// Takes exactly the filters ListSalesInvoices takes, through the same
// salesInvoiceWhere, so the cards always describe the rows underneath them.
// page/page_size are ignored: a summary that paginated would answer a different
// question than it appears to.
//
// The web AR tab folded these in the browser over a page_size=1000 fetch, which
// is silently wrong past the thousandth invoice, and mobile — paginating at 10 —
// could not copy that and so showed no cards at all.
func (h *Handler) GetSalesInvoicesSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	args := []interface{}{tenantID}
	where := `WHERE si.tenant_id = $1 AND si.deleted_at IS NULL` + salesInvoiceWhere(c, &args)

	// Receivable = what is still owed, so cancelled invoices are excluded and
	// fully-paid ones contribute nothing. Residual is derived rather than read
	// from amount_due, which is not maintained on every write path.
	const residual = "GREATEST(si.total_amount - COALESCE(si.amount_paid, 0), 0)"
	overdue := invoiceOverdueSQL("si")

	var out struct {
		TotalReceivable float64  `json:"total_receivable"`
		OverdueAmount   float64  `json:"overdue_amount"`
		OverdueInvoices int      `json:"overdue_invoices"`
		OpenInvoices    int      `json:"open_invoices"`
		AvgDaysToPay    *float64 `json:"avg_days_to_pay"`
		Aging           struct {
			Current    float64 `json:"current"`
			Days1To30  float64 `json:"days_1_30"`
			Days31To60 float64 `json:"days_31_60"`
			Days61To90 float64 `json:"days_61_90"`
			Days90Plus float64 `json:"days_90_plus"`
		} `json:"aging"`
	}

	// Aging buckets are 1-30 / 31-60 / 61-90 / 90+ with no gap. The web version
	// jumped from `<= 60` straight to '90+', so every 61-90 day debt was
	// reported as 90+ — the bucket simply did not exist.
	var avgDays sql.NullFloat64
	err := h.db.QueryRow(`
		SELECT
			COALESCE(SUM(`+residual+`) FILTER (WHERE si.status <> 'cancelled'), 0),
			COALESCE(SUM(`+residual+`) FILTER (WHERE `+overdue+`), 0),
			COUNT(*) FILTER (WHERE `+overdue+`),
			COUNT(*) FILTER (WHERE si.status <> 'cancelled' AND `+residual+` > 0),
			COALESCE(SUM(`+residual+`) FILTER (WHERE si.status <> 'cancelled' AND NOT (`+overdue+`)), 0),
			COALESCE(SUM(`+residual+`) FILTER (WHERE `+overdue+` AND (CURRENT_DATE - si.due_date) BETWEEN 1 AND 30), 0),
			COALESCE(SUM(`+residual+`) FILTER (WHERE `+overdue+` AND (CURRENT_DATE - si.due_date) BETWEEN 31 AND 60), 0),
			COALESCE(SUM(`+residual+`) FILTER (WHERE `+overdue+` AND (CURRENT_DATE - si.due_date) BETWEEN 61 AND 90), 0),
			COALESCE(SUM(`+residual+`) FILTER (WHERE `+overdue+` AND (CURRENT_DATE - si.due_date) > 90), 0),
			-- Average days from invoice_date to the LAST confirmed payment that
			-- settled it, over invoices that are actually paid. The web derived
			-- this from updated_at, which any edit moves — so an invoice touched
			-- months later reported months to pay.
			--
			-- B1: computed over the SAME rows as the five figures above rather
			-- than a second, tenant-wide scan. Its own subquery only filtered
			-- tenant + deleted_at, so applying a date range or a search left five
			-- cards describing the filtered set and this one describing the whole
			-- tenant — the a60cb5b §2 defect surviving inside a single column —
			-- and it mixed organizations, having no organization_id predicate at
			-- all. Correlating on si.id ties it to the outer WHERE, so every
			-- filter reaches it for free and can never be forgotten again.
			AVG((
				SELECT MAX(p.payment_date)::date - si.invoice_date
				FROM payment_allocations pa
				JOIN payments p ON p.id = pa.payment_id
				                AND p.status = 'confirmed' AND p.deleted_at IS NULL
				WHERE pa.document_id = si.id AND pa.document_type = 'sales_invoice'
			)) FILTER (
				WHERE COALESCE(si.amount_paid, 0) >= si.total_amount AND si.total_amount > 0
			)
		FROM sales_invoices si
		LEFT JOIN contacts c ON si.customer_id = c.id
		`+where, args...).Scan(
		&out.TotalReceivable, &out.OverdueAmount, &out.OverdueInvoices, &out.OpenInvoices,
		&out.Aging.Current, &out.Aging.Days1To30, &out.Aging.Days31To60,
		&out.Aging.Days61To90, &out.Aging.Days90Plus, &avgDays)
	if err != nil {
		h.log.Error("Failed to build receivables summary", "error", err)
		response.InternalError(c, "Failed to build receivables summary")
		return
	}
	// B2: NULL stays null in the payload. AVG over an empty set is NULL, and
	// collapsing that to 0 made "no fully-paid invoice has a confirmed payment
	// allocation" indistinguishable from "invoices are paid the same day" — the
	// card read 0 when the honest answer was "unknown". The client renders the
	// same placeholder it uses for any absent field.
	if avgDays.Valid {
		v := avgDays.Float64
		out.AvgDaysToPay = &v
	}

	response.Success(c, out)
}

// GetSupplierStats godoc
// @Summary Supplier totals
// @Description Counts and purchase totals across every supplier, for the stat cards
// @Tags Purchase - Suppliers
// @Produce json
// @Param search query string false "Name, phone or email"
// @Param is_active query bool false "Filter by active flag"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /suppliers/stats [get]
//
// SupplierStatCards folded these over the loaded page, so the figures described
// page 1 and grew as the user scrolled — the same defect the invoice cards had.
func (h *Handler) GetSupplierStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	args := []interface{}{tenantID}
	// type IN ('vendor','both') — the vocabulary the rest of the purchase module
	// uses (GetSupplierKPIs, and the vendor guard in contacts.go). There is no
	// dedicated /suppliers list endpoint; the clients read /contacts?type=vendor,
	// so these are the same rows.
	where := " WHERE ct.tenant_id = $1 AND ct.deleted_at IS NULL AND ct.type IN ('vendor', 'both')"

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		args = append(args, orgID)
		where += " AND (ct.organization_id = $" + strconv.Itoa(len(args)) + " OR ct.organization_id IS NULL)"
	}
	if v := c.Query("is_active"); v == "true" || v == "false" {
		where += " AND COALESCE(ct.is_active, true) = " + v
	}
	if v := strings.TrimSpace(c.Query("search")); v != "" {
		args = append(args, "%"+v+"%")
		n := strconv.Itoa(len(args))
		where += " AND (ct.name ILIKE $" + n + " OR COALESCE(ct.phone,'') ILIKE $" + n + " OR COALESCE(ct.email,'') ILIKE $" + n + ")"
	}

	var out struct {
		TotalCount          int     `json:"total_count"`
		ActiveCount         int     `json:"active_count"`
		AvgRating           float64 `json:"avg_rating"`
		TotalPurchaseAmount float64 `json:"total_purchase_amount"`
		TotalOrderCount     int     `json:"total_order_count"`
	}

	// Purchase totals come from a grouped pass over purchase_orders, not a
	// correlated subquery per supplier: this is a summary, so the per-contact
	// form would scan the order table once per row.
	//
	// The rating is supplier_performance.overall_rating, the same source
	// GetSupplierKPIs averages — contacts has no rating column at all, so
	// reading ct.rating would have been a guaranteed runtime error.
	var avgRating sql.NullFloat64
	err := h.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE COALESCE(ct.is_active, true)),
		       AVG(perf.rating),
		       COALESCE(SUM(COALESCE(po.total_amount, 0)), 0),
		       COALESCE(SUM(COALESCE(po.order_count, 0)), 0)
		FROM contacts ct
		LEFT JOIN (
		    SELECT vendor_id,
		           SUM(total_amount) AS total_amount,
		           COUNT(*)          AS order_count
		    FROM purchase_orders
		    WHERE tenant_id = $1 AND deleted_at IS NULL AND status <> 'cancelled'
		    GROUP BY vendor_id
		) po ON po.vendor_id = ct.id
		LEFT JOIN (
		    SELECT vendor_id, AVG(overall_rating)::float AS rating
		    FROM supplier_performance
		    WHERE tenant_id = $1
		    GROUP BY vendor_id
		) perf ON perf.vendor_id = ct.id`+where, args...).Scan(
		&out.TotalCount, &out.ActiveCount, &avgRating,
		&out.TotalPurchaseAmount, &out.TotalOrderCount)
	if err != nil {
		h.log.Error("Failed to build supplier stats", "error", err)
		response.InternalError(c, "Failed to build supplier stats")
		return
	}
	// NULL when no supplier has a performance record — reported as 0 rather
	// than failing the scan.
	out.AvgRating = avgRating.Float64

	response.Success(c, out)
}
