package handler

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// Aggregate endpoints for the paginated list screens that had no equivalent.
//
// Purchase orders, supplier performance and the sales dashboard are NOT here:
// /purchase-orders/stats, /purchase-orders/supplier-kpis and
// /reports/sales-summary already answer those questions, and a second, weaker
// endpoint for the same figure is how two screens start disagreeing.
//
// Paging a list silently breaks any client that was computing its header cards
// from the rows it received: with 20 of 4000 orders in hand, "Total value" is
// off by two orders of magnitude and looks like a data-loss bug rather than a
// paging change. Each handler below answers the same question over the WHOLE
// filtered set in one query, and accepts exactly the filters its list endpoint
// accepts so the cards and the visible rows always describe the same selection.
//
// The per-status breakdown is produced by GROUP BY status and returned as a
// map, NOT by a fixed set of COUNT(*) FILTER (WHERE status = '...') columns.
// The status vocabularies here are only documented in column comments and they
// disagree with each other across modules — purchase_orders uses
// 'pending_approval' where sales uses 'confirmed', payroll_periods' comment
// lists 'processing' while the code writes 'approved'. Hardcoding names would
// mean a card silently reading zero the day a status is renamed or added.
// Callers read by_status["received"] and get 0 for a status that has no rows,
// which is the same answer without the fragility.

// statusBucket is one status and its rollup within a summary.
type statusBucket struct {
	Count int     `json:"count"`
	Value float64 `json:"value"`
}

// summaryScope builds the tenant/organization predicate shared by the summary
// queries. alias is the table alias used in the caller's FROM clause.
func summaryScope(c *gin.Context, alias string, args *[]interface{}) (string, bool) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		return "", false
	}
	*args = append(*args, tenantID)
	where := fmt.Sprintf(" WHERE %s.tenant_id = $%d AND %s.deleted_at IS NULL", alias, len(*args), alias)

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		*args = append(*args, orgID)
		where += fmt.Sprintf(" AND %s.organization_id = $%d", alias, len(*args))
	}
	return where, true
}

// dateRange appends the caller's date_from/date_to filters against column.
// Empty values are skipped, so an unfiltered summary covers all time exactly
// like an unfiltered list.
func dateRange(c *gin.Context, column string, args *[]interface{}) string {
	clause := ""
	if v := strings.TrimSpace(c.Query("date_from")); v != "" {
		*args = append(*args, v)
		clause += fmt.Sprintf(" AND %s >= $%d", column, len(*args))
	}
	if v := strings.TrimSpace(c.Query("date_to")); v != "" {
		*args = append(*args, v)
		clause += fmt.Sprintf(" AND %s <= $%d", column, len(*args))
	}
	return clause
}

// statusRollup runs one grouped query and returns the per-status buckets plus
// the totals across every status. The query must select exactly three columns:
// the status, a COUNT, and a SUM of whatever money column the table carries.
func (h *Handler) statusRollup(query string, args []interface{}) (map[string]statusBucket, int, float64, error) {
	rows, err := h.db.Query(query, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	buckets := map[string]statusBucket{}
	totalCount := 0
	totalValue := 0.0
	for rows.Next() {
		var status sql.NullString
		var b statusBucket
		if err := rows.Scan(&status, &b.Count, &b.Value); err != nil {
			return nil, 0, 0, err
		}
		key := status.String
		if !status.Valid || key == "" {
			// A NULL status is a real row and must not vanish from the totals;
			// it is bucketed under "unknown" rather than dropped.
			key = "unknown"
		}
		// Statuses differing only by case are the same status to every caller.
		key = strings.ToLower(key)
		prev := buckets[key]
		prev.Count += b.Count
		prev.Value += b.Value
		buckets[key] = prev

		totalCount += b.Count
		totalValue += b.Value
	}
	return buckets, totalCount, totalValue, rows.Err()
}

// GetPaymentsSummary godoc
// @Summary Payment totals
// @Description Counts and amounts of payments over the whole filtered set
// @Tags Finance - Payments
// @Produce json
// @Param type query string false "Filter by payment type (receipt|payment)"
// @Param status query string false "Filter by status"
// @Param contact_id query string false "Filter by contact"
// @Param date_from query string false "Payment date from (YYYY-MM-DD)"
// @Param date_to query string false "Payment date to (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /payments/summary [get]
func (h *Handler) GetPaymentsSummary(c *gin.Context) {
	args := []interface{}{}
	where, ok := summaryScope(c, "p", &args)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	if v := strings.TrimSpace(c.Query("type")); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND p.type = $%d", len(args))
	}
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND p.status = $%d", len(args))
	}
	if v := strings.TrimSpace(c.Query("contact_id")); v != "" {
		if contactID, err := uuid.Parse(v); err == nil {
			args = append(args, contactID)
			where += fmt.Sprintf(" AND p.contact_id = $%d", len(args))
		}
	}
	where += dateRange(c, "p.payment_date", &args)

	byStatus, totalPayments, totalAmount, err := h.statusRollup(`
		SELECT p.status, COUNT(*), COALESCE(SUM(p.amount), 0)
		FROM payments p`+where+`
		GROUP BY p.status`, args)
	if err != nil {
		h.log.Error("Failed to build payments summary", "error", err)
		response.InternalError(c, "Failed to build payments summary")
		return
	}

	// Money in vs money out is split on p.type, not on the sign of the amount:
	// amounts are stored unsigned and direction lives entirely in the type,
	// which is 'receipt' (money in) or 'payment' (money out).
	var receiptAmount, paymentAmount float64
	var receiptCount, paymentCount int
	if err := h.db.QueryRow(`
		SELECT COALESCE(SUM(p.amount) FILTER (WHERE p.type = 'receipt'), 0),
		       COALESCE(SUM(p.amount) FILTER (WHERE p.type = 'payment'), 0),
		       COUNT(*) FILTER (WHERE p.type = 'receipt'),
		       COUNT(*) FILTER (WHERE p.type = 'payment')
		FROM payments p`+where, args...).Scan(
		&receiptAmount, &paymentAmount, &receiptCount, &paymentCount); err != nil {
		h.log.Error("Failed to build payments summary totals", "error", err)
		response.InternalError(c, "Failed to build payments summary")
		return
	}

	response.Success(c, gin.H{
		"total_payments": totalPayments,
		"total_amount":   totalAmount,
		"receipt_amount": receiptAmount,
		"payment_amount": paymentAmount,
		"receipt_count":  receiptCount,
		"payment_count":  paymentCount,
		"net_amount":     receiptAmount - paymentAmount,
		"by_status":      byStatus,
	})
}

// GetWorkflowsSummary godoc
// @Summary Workflow totals
// @Description Counts and automation metrics across all workflows
// @Tags Workflow
// @Produce json
// @Param category query string false "Filter by category"
// @Param status query string false "Filter by status"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /workflows/summary [get]
func (h *Handler) GetWorkflowsSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// workflows is tenant-scoped only — the table has no organization_id, so
	// summaryScope's org predicate cannot be applied here.
	args := []interface{}{tenantID}
	where := " WHERE tenant_id = $1 AND deleted_at IS NULL"

	if v := strings.TrimSpace(c.Query("category")); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND category = $%d", len(args))
	}
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}

	// cost_savings is the money column here, so it rolls up as the bucket value.
	byStatus, totalWorkflows, totalSavings, err := h.statusRollup(`
		SELECT status, COUNT(*), COALESCE(SUM(cost_savings), 0)
		FROM workflows`+where+`
		GROUP BY status`, args)
	if err != nil {
		h.log.Error("Failed to build workflows summary", "error", err)
		response.InternalError(c, "Failed to build workflows summary")
		return
	}

	// AVG skips NULL success_rate on its own; COALESCE only covers the all-NULL
	// case, i.e. a tenant whose workflows have never run.
	var avgSuccess float64
	if err := h.db.QueryRow(`
		SELECT COALESCE(AVG(success_rate), 0) FROM workflows`+where, args...).Scan(&avgSuccess); err != nil {
		h.log.Error("Failed to build workflows summary totals", "error", err)
		response.InternalError(c, "Failed to build workflows summary")
		return
	}

	response.Success(c, gin.H{
		"total_workflows":    totalWorkflows,
		"total_cost_savings": totalSavings,
		"avg_success_rate":   avgSuccess,
		"by_status":          byStatus,
	})
}

// GetPayrollPeriodsSummary godoc
// @Summary Payroll period totals
// @Description Period counts and payroll totals across the whole filtered set
// @Tags HR - Payroll
// @Produce json
// @Param status query string false "Filter by status"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /payroll-periods/summary [get]
func (h *Handler) GetPayrollPeriodsSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	args := []interface{}{tenantID}
	where := " WHERE pp.tenant_id = $1 AND pp.deleted_at IS NULL"
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND pp.status = $%d", len(args))
	}

	// Totals come from payroll_entries, matching what the list itself shows:
	// the denormalised pp.total_* columns are only its fallback and go stale as
	// soon as an entry is edited after the period was first totalled.
	entriesJoin := `
		LEFT JOIN (
		    SELECT payroll_period_id,
		           SUM(gross_salary)     AS gross,
		           SUM(total_deductions) AS deductions,
		           SUM(net_salary)       AS net,
		           COUNT(*)              AS headcount
		    FROM payroll_entries
		    WHERE deleted_at IS NULL
		    GROUP BY payroll_period_id
		) e ON e.payroll_period_id = pp.id`

	byStatus, totalPeriods, totalNet, err := h.statusRollup(`
		SELECT pp.status, COUNT(*), COALESCE(SUM(COALESCE(e.net, pp.total_net)), 0)
		FROM payroll_periods pp`+entriesJoin+where+`
		GROUP BY pp.status`, args)
	if err != nil {
		h.log.Error("Failed to build payroll periods summary", "error", err)
		response.InternalError(c, "Failed to build payroll periods summary")
		return
	}

	var totalGross, totalDeduct float64
	var headcount sql.NullInt64
	if err := h.db.QueryRow(`
		SELECT COALESCE(SUM(COALESCE(e.gross, pp.total_gross)), 0),
		       COALESCE(SUM(COALESCE(e.deductions, pp.total_deductions)), 0),
		       COALESCE(SUM(COALESCE(e.headcount, pp.employee_count)), 0)
		FROM payroll_periods pp`+entriesJoin+where, args...).Scan(
		&totalGross, &totalDeduct, &headcount); err != nil {
		h.log.Error("Failed to build payroll periods summary totals", "error", err)
		response.InternalError(c, "Failed to build payroll periods summary")
		return
	}

	response.Success(c, gin.H{
		"total_periods":    totalPeriods,
		"total_gross":      totalGross,
		"total_deductions": totalDeduct,
		"total_net":        totalNet,
		"employee_count":   headcount.Int64,
		"by_status":        byStatus,
	})
}
