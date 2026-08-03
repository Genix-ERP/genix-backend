package handler

// GET /production-orders/stats — the single call behind the rebuilt Ishlab
// chiqarish "Asosiy panel" (docs/production-audit.md §1, integration map §7).
// Same contract shape as GET /purchase-orders/stats (Xarid): stat tiles, a
// gap-filled daily series, status distribution, period work-center load,
// late orders and an MRP-lite shortage list — one request instead of the old
// 6-way client-side hydration.
//
// Conventions fixed in one place:
//   - Period is [from, to] (2006-01-02), defaulting to the current month;
//     `to` is clamped to end of day.
//   - "Yakunlangan" (completed_period) counts by actual_end within the
//     period — not lifetime.
//   - otd_rate = share of period completions with actual_end <= scheduled_end.
//   - wip_value comes from posted GL lines on the 2010 WIP account
//     (debit − credit), NOT from the denormalized accounts.current_balance.
//   - avg_load is period-based: work_order_time_logs hours vs
//     working_hours_per_day × period days — not the lifetime
//     current_utilization figure the old endpoint averaged.
//   - Legacy top-level keys (average_utilization, completion_rate, …) are
//     kept alongside the new `totals` object so the current frontend context
//     keeps working during the rollout.
//   - Sub-query failures are log-only; slices are always non-nil.

import (
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// openMOStatuses is THE "not finished yet" rule for production orders. Keep
// in sync with the lifecycle handlers in manufacturing.go.
const openMOStatuses = `('draft', 'confirmed', 'ready', 'in_progress', 'paused', 'packaging')`

func (h *Handler) GetManufacturingStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

	// Period: [from, to]; defaults to the current month; `to` inclusive EOD.
	nowT := time.Now()
	from := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location())
	to := nowT
	customPeriod := false
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
			customPeriod = true
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			to = t.Add(24*time.Hour - time.Nanosecond)
			customPeriod = true
		}
	}
	if to.Before(from) {
		to = from.Add(24*time.Hour - time.Nanosecond)
	}

	// ── KPI totals: one COUNT(*) FILTER query ───────────────────────────
	var (
		totalOrders, draftOrders, confirmedOrders, inProgressOrders int
		activeOrders, completedLifetime, completedPeriod            int
		overdueOrders, otdNumerator                                 int
		qtyProduced, qtyScrapped                                    float64
	)
	if err := h.db.QueryRow(`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'draft'),
			COUNT(*) FILTER (WHERE status = 'confirmed'),
			COUNT(*) FILTER (WHERE status = 'in_progress'),
			COUNT(*) FILTER (WHERE status IN ('in_progress', 'paused', 'packaging')),
			COUNT(*) FILTER (WHERE status = 'completed'),
			COUNT(*) FILTER (WHERE status = 'completed' AND actual_end >= $2 AND actual_end <= $3),
			COUNT(*) FILTER (WHERE status IN `+openMOStatuses+`
			                 AND scheduled_end IS NOT NULL AND scheduled_end < NOW()),
			COUNT(*) FILTER (WHERE status = 'completed' AND actual_end >= $2 AND actual_end <= $3
			                 AND scheduled_end IS NOT NULL AND actual_end::date <= scheduled_end::date),
			COALESCE(SUM(quantity_produced) FILTER (WHERE status = 'completed' AND actual_end >= $2 AND actual_end <= $3), 0),
			COALESCE(SUM(quantity_scrapped) FILTER (WHERE status = 'completed' AND actual_end >= $2 AND actual_end <= $3), 0)
		FROM production_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND ($4::uuid IS NULL OR organization_id = $4)
	`, tenantID, from, to, orgArg).Scan(
		&totalOrders, &draftOrders, &confirmedOrders, &inProgressOrders,
		&activeOrders, &completedLifetime, &completedPeriod,
		&overdueOrders, &otdNumerator, &qtyProduced, &qtyScrapped,
	); err != nil {
		h.log.Error("Failed to load production KPI totals", "error", err)
	}

	scrapRate := 0.0
	if qtyProduced+qtyScrapped > 0 {
		scrapRate = qtyScrapped / (qtyProduced + qtyScrapped) * 100
	}
	otdRate := 0.0
	if completedPeriod > 0 {
		otdRate = float64(otdNumerator) / float64(completedPeriod) * 100
	}
	completionRate := 0.0
	if totalOrders > 0 {
		completionRate = float64(completedLifetime) / float64(totalOrders) * 100
	}

	// WIP value from posted GL lines on 2010 (asset: debit − credit) — the
	// akt-sverka source of truth, not accounts.current_balance.
	var wipValue float64
	if err := h.db.QueryRow(`
		SELECT COALESCE(SUM(jel.debit_amount - jel.credit_amount), 0)
		FROM journal_entry_lines jel
		JOIN journal_entries je ON je.id = jel.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
		JOIN accounts a ON a.id = jel.account_id
		WHERE je.tenant_id = $1 AND a.code = '2010'
		  AND ($2::uuid IS NULL OR je.organization_id = $2)
	`, tenantID, orgArg).Scan(&wipValue); err != nil {
		h.log.Error("Failed to load WIP value", "error", err)
	}

	// Quality (B5): real aggregates from quality_checks in the period —
	// replaces the always-zero placeholders of the old endpoint. Note:
	// quality_checks carries no organization_id (011 schema), so this is
	// tenant + period scoped.
	var qualityChecksCount int
	var qtyInspected, qtyPassed float64
	if err := h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(quantity_inspected), 0), COALESCE(SUM(quantity_passed), 0)
		FROM quality_checks
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND inspection_date >= $2 AND inspection_date <= $3
	`, tenantID, from, to).Scan(&qualityChecksCount, &qtyInspected, &qtyPassed); err != nil {
		h.log.Error("Failed to load quality aggregates", "error", err)
	}
	qualityPassRate := 0.0
	if qtyInspected > 0 {
		qualityPassRate = qtyPassed / qtyInspected * 100
	}

	// ── Daily series, gap-filled in Go ──────────────────────────────────
	// Default window: last 30 days; a custom ?from/?to uses the period
	// (capped at 92 days so the payload stays sane).
	seriesTo := time.Date(nowT.Year(), nowT.Month(), nowT.Day(), 0, 0, 0, 0, nowT.Location())
	seriesFrom := seriesTo.AddDate(0, 0, -29)
	if customPeriod {
		seriesFrom = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
		seriesTo = time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
		if seriesTo.Sub(seriesFrom) > 92*24*time.Hour {
			seriesFrom = seriesTo.AddDate(0, 0, -91)
		}
	}
	seriesEnd := seriesTo.Add(24*time.Hour - time.Nanosecond)

	plannedBy := map[string]float64{}
	if pRows, pErr := h.db.Query(`
		SELECT scheduled_end::date::text, COALESCE(SUM(quantity_planned), 0)
		FROM production_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status != 'cancelled'
		  AND scheduled_end >= $2 AND scheduled_end <= $3
		  AND ($4::uuid IS NULL OR organization_id = $4)
		GROUP BY 1
	`, tenantID, seriesFrom, seriesEnd, orgArg); pErr == nil {
		for pRows.Next() {
			var d string
			var v float64
			if pRows.Scan(&d, &v) == nil {
				plannedBy[d] = v
			}
		}
		pRows.Close()
	} else {
		h.log.Error("Failed to load planned daily series", "error", pErr)
	}

	producedBy := map[string]float64{}
	if pRows, pErr := h.db.Query(`
		SELECT actual_end::date::text, COALESCE(SUM(quantity_produced), 0)
		FROM production_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status = 'completed'
		  AND actual_end >= $2 AND actual_end <= $3
		  AND ($4::uuid IS NULL OR organization_id = $4)
		GROUP BY 1
	`, tenantID, seriesFrom, seriesEnd, orgArg); pErr == nil {
		for pRows.Next() {
			var d string
			var v float64
			if pRows.Scan(&d, &v) == nil {
				producedBy[d] = v
			}
		}
		pRows.Close()
	} else {
		h.log.Error("Failed to load produced daily series", "error", pErr)
	}

	type dailyPoint struct {
		Date     string  `json:"date"`
		Planned  float64 `json:"planned"`
		Produced float64 `json:"produced"`
	}
	dailySeries := []dailyPoint{}
	for d := seriesFrom; !d.After(seriesTo); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		dailySeries = append(dailySeries, dailyPoint{
			Date:     key,
			Planned:  plannedBy[key],
			Produced: producedBy[key],
		})
	}

	// ── Status distribution: open orders + last 90 days of closed ones ──
	type statusCount struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	statusCounts := []statusCount{}
	if sRows, sErr := h.db.Query(`
		SELECT status, COUNT(*)
		FROM production_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND (status IN `+openMOStatuses+` OR created_at >= NOW() - INTERVAL '90 days')
		  AND ($2::uuid IS NULL OR organization_id = $2)
		GROUP BY status
		ORDER BY COUNT(*) DESC
	`, tenantID, orgArg); sErr == nil {
		for sRows.Next() {
			var sc statusCount
			if sRows.Scan(&sc.Status, &sc.Count) == nil {
				statusCounts = append(statusCounts, sc)
			}
		}
		sRows.Close()
	} else {
		h.log.Error("Failed to load status counts", "error", sErr)
	}

	// ── Work-center load over the period ────────────────────────────────
	// booked = time-log hours in the period; capacity = working_hours_per_day
	// × period days (calendar-day approximation).
	periodDays := int(to.Sub(from).Hours()/24) + 1
	if periodDays < 1 {
		periodDays = 1
	}
	type workCenterLoad struct {
		WorkCenterID  uuid.UUID `json:"work_center_id"`
		Name          string    `json:"name"`
		BookedHours   float64   `json:"booked_hours"`
		CapacityHours float64   `json:"capacity_hours"`
		LoadPercent   float64   `json:"load_percent"`
	}
	wcLoad := []workCenterLoad{}
	avgLoad := 0.0
	// No LIMIT in SQL: avg_load must cover ALL active work centers, the
	// response list is truncated to the busiest 12 afterwards.
	if wRows, wErr := h.db.Query(`
		SELECT wc.id, wc.name, COALESCE(wc.working_hours_per_day, 8),
		       COALESCE(SUM(tl.duration_hours), 0) AS booked
		FROM work_centers wc
		LEFT JOIN work_orders wo ON wo.work_center_id = wc.id
			AND wo.tenant_id = $1 AND wo.deleted_at IS NULL
		LEFT JOIN work_order_time_logs tl ON tl.work_order_id = wo.id
			AND tl.start_time >= $2 AND tl.start_time <= $3
		WHERE wc.tenant_id = $1 AND wc.deleted_at IS NULL AND wc.status = 'active'
		  AND ($4::uuid IS NULL OR wc.organization_id = $4)
		GROUP BY wc.id, wc.name, wc.working_hours_per_day
		ORDER BY booked DESC, wc.name ASC
	`, tenantID, from, to, orgArg); wErr == nil {
		var loadSum float64
		for wRows.Next() {
			var wl workCenterLoad
			var hoursPerDay float64
			if wRows.Scan(&wl.WorkCenterID, &wl.Name, &hoursPerDay, &wl.BookedHours) == nil {
				wl.CapacityHours = hoursPerDay * float64(periodDays)
				if wl.CapacityHours > 0 {
					wl.LoadPercent = wl.BookedHours / wl.CapacityHours * 100
					if wl.LoadPercent > 999 {
						wl.LoadPercent = 999
					}
				}
				loadSum += wl.LoadPercent
				wcLoad = append(wcLoad, wl)
			}
		}
		wRows.Close()
		if len(wcLoad) > 0 {
			avgLoad = loadSum / float64(len(wcLoad))
		}
		if len(wcLoad) > 12 {
			wcLoad = wcLoad[:12]
		}
	} else {
		h.log.Error("Failed to load work center load", "error", wErr)
	}

	var totalWorkCenters, activeWorkCenters int
	if err := h.db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'active')
		FROM work_centers
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND ($2::uuid IS NULL OR organization_id = $2)
	`, tenantID, orgArg).Scan(&totalWorkCenters, &activeWorkCenters); err != nil {
		h.log.Error("Failed to load work center counts", "error", err)
	}

	// ── Late orders: top 10 open orders past their scheduled_end ────────
	type lateOrder struct {
		ID           uuid.UUID `json:"id"`
		Code         string    `json:"code"`
		ProductName  string    `json:"product_name"`
		Status       string    `json:"status"`
		ScheduledEnd string    `json:"scheduled_end"`
		DaysLate     int       `json:"days_late"`
	}
	lateOrders := []lateOrder{}
	if lRows, lErr := h.db.Query(`
		SELECT po.id, po.code, COALESCE(p.name, ''), po.status,
		       po.scheduled_end::date::text,
		       GREATEST(CURRENT_DATE - po.scheduled_end::date, 0)
		FROM production_orders po
		LEFT JOIN products p ON p.id = po.product_id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		  AND po.status IN `+openMOStatuses+`
		  AND po.scheduled_end IS NOT NULL AND po.scheduled_end < NOW()
		  AND ($2::uuid IS NULL OR po.organization_id = $2)
		ORDER BY po.scheduled_end ASC
		LIMIT 10
	`, tenantID, orgArg); lErr == nil {
		for lRows.Next() {
			var lo lateOrder
			if lRows.Scan(&lo.ID, &lo.Code, &lo.ProductName, &lo.Status, &lo.ScheduledEnd, &lo.DaysLate) == nil {
				lateOrders = append(lateOrders, lo)
			}
		}
		lRows.Close()
	} else {
		h.log.Error("Failed to load late orders", "error", lErr)
	}

	// ── Shortages (MRP-lite, integration map §7) ────────────────────────
	// required = BOM demand of confirmed/in_progress orders for the still-
	// unproduced remainder; on_hand = org-scoped inventory; on_order = open
	// PO line quantity not yet received. missing = required − on_hand −
	// on_order, positive only.
	type shortage struct {
		ProductID   uuid.UUID `json:"product_id"`
		ProductName string    `json:"product_name"`
		ProductCode string    `json:"product_code"`
		Required    float64   `json:"required"`
		OnHand      float64   `json:"on_hand"`
		OnOrder     float64   `json:"on_order"`
		Missing     float64   `json:"missing"`
	}
	shortages := []shortage{}
	if shRows, shErr := h.db.Query(`
		WITH need AS (
			SELECT bl.component_id AS product_id,
			       SUM(bl.quantity
			           * GREATEST(po.quantity_planned - COALESCE(po.quantity_produced, 0), 0)
			           / GREATEST(COALESCE(pb.quantity, 1), 0.0001)
			           * (1 + COALESCE(bl.scrap_percent, 0) / 100.0)) AS required
			FROM production_orders po
			JOIN product_boms pb ON pb.id = po.bom_id
			JOIN bom_lines bl ON bl.bom_id = pb.id
			JOIN products cp ON cp.id = bl.component_id AND COALESCE(cp.track_inventory, true)
			WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
			  AND po.status IN ('confirmed', 'in_progress')
			  AND ($2::uuid IS NULL OR po.organization_id = $2)
			GROUP BY bl.component_id
		)
		SELECT n.product_id, COALESCE(p.name, ''), COALESCE(p.code, ''), n.required,
		       COALESCE(oh.qty, 0) AS on_hand, COALESCE(oo.qty, 0) AS on_order,
		       n.required - COALESCE(oh.qty, 0) - COALESCE(oo.qty, 0) AS missing
		FROM need n
		JOIN products p ON p.id = n.product_id
		LEFT JOIN LATERAL (
			SELECT SUM(i.quantity_on_hand) AS qty
			FROM inventory i
			JOIN warehouses w ON w.id = i.warehouse_id
			WHERE i.tenant_id = $1 AND i.product_id = n.product_id
			  AND w.deleted_at IS NULL
			  AND ($2::uuid IS NULL OR w.organization_id = $2)
		) oh ON true
		LEFT JOIN LATERAL (
			SELECT SUM(pol.quantity - COALESCE(pol.quantity_received, 0)) AS qty
			FROM purchase_order_lines pol
			JOIN purchase_orders p2 ON p2.id = pol.purchase_order_id
			WHERE p2.tenant_id = $1 AND p2.deleted_at IS NULL
			  AND p2.status IN ('approved', 'ordered', 'partial')
			  AND pol.product_id = n.product_id
			  AND ($2::uuid IS NULL OR p2.organization_id = $2)
		) oo ON true
		WHERE n.required - COALESCE(oh.qty, 0) - COALESCE(oo.qty, 0) > 0.0001
		ORDER BY missing DESC
		LIMIT 15
	`, tenantID, orgArg); shErr == nil {
		for shRows.Next() {
			var sh shortage
			if shRows.Scan(&sh.ProductID, &sh.ProductName, &sh.ProductCode,
				&sh.Required, &sh.OnHand, &sh.OnOrder, &sh.Missing) == nil {
				shortages = append(shortages, sh)
			}
		}
		shRows.Close()
	} else {
		h.log.Error("Failed to load shortages", "error", shErr)
	}

	response.Success(c, gin.H{
		"totals": gin.H{
			"active_orders":        activeOrders,
			"draft_orders":         draftOrders,
			"confirmed_orders":     confirmedOrders,
			"in_progress_orders":   inProgressOrders,
			"completed_period":     completedPeriod,
			"overdue_orders":       overdueOrders,
			"quantity_produced":    qtyProduced,
			"quantity_scrapped":    qtyScrapped,
			"scrap_rate":           scrapRate,
			"otd_rate":             otdRate,
			"wip_value":            wipValue,
			"work_centers_active":  activeWorkCenters,
			"avg_load":             avgLoad,
			"quality_checks_count": qualityChecksCount,
			"quality_pass_rate":    qualityPassRate,
		},
		"daily_series":     dailySeries,
		"status_counts":    statusCounts,
		"work_center_load": wcLoad,
		"late_orders":      lateOrders,
		"shortages":        shortages,
		"as_of":            nowT.Format("2006-01-02"),

		// Legacy top-level keys the current frontend context reads — kept
		// during the rollout so nothing breaks; new consumers use `totals`.
		"average_utilization":     avgLoad,
		"confirmed_orders":        confirmedOrders,
		"in_progress_orders":      inProgressOrders,
		"completed_orders":        completedLifetime,
		"draft_orders":            draftOrders,
		"total_orders":            totalOrders,
		"total_production_orders": totalOrders,
		"overdue_orders":          overdueOrders,
		"scrap_rate":              scrapRate,
		"completion_rate":         completionRate,
		"total_work_centers":      totalWorkCenters,
		"active_work_centers":     activeWorkCenters,
	})
}

// GetManufacturingReport — GET /production-orders/report, the backend for the
// rebuilt Hisobot tab (audit §1: it was 100% client-side over a 1000-row
// slice). One period-aware call: totals, per-product and per-category
// aggregates and a scrap Pareto.
//
// PERIOD DEFINITION (pinned): an order belongs to [from, to] when
//   - status IN ('completed','cancelled') and COALESCE(actual_end,
//     updated_at) falls in the period (actual_end is when the outcome
//     happened; updated_at is the fallback for legacy rows that never got
//     actual_end stamped), OR
//   - any other (open) status and created_at falls in the period.
//
// total_cost = COALESCE(actual_cost, planned_cost, 0) per order.
// Defaults: current month; `to` clamped to end of day. Org via the resolver
// header as a nullable SQL param. Slices non-nil; sub-query failures
// log-only.
func (h *Handler) GetManufacturingReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var orgArg interface{}
	if orgID, okOrg := middleware.GetOrganizationID(c); okOrg && orgID != uuid.Nil {
		orgArg = orgID
	}

	nowT := time.Now()
	from := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location())
	to := nowT
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			to = t.Add(24*time.Hour - time.Nanosecond)
		}
	}
	if to.Before(from) {
		to = from.Add(24*time.Hour - time.Nanosecond)
	}

	// Shared period-set predicate — params: $1 tenant, $2 from, $3 to, $4 org.
	const reportSetWhere = `
		po.tenant_id = $1 AND po.deleted_at IS NULL
		AND ($4::uuid IS NULL OR po.organization_id = $4)
		AND (
			(po.status IN ('completed', 'cancelled')
			 AND COALESCE(po.actual_end, po.updated_at) >= $2 AND COALESCE(po.actual_end, po.updated_at) <= $3)
			OR (po.status NOT IN ('completed', 'cancelled')
			 AND po.created_at >= $2 AND po.created_at <= $3)
		)`

	// ── Totals ──────────────────────────────────────────────────────────
	var totOrders int
	var totPlanned, totProduced, totScrapped, totCost float64
	if err := h.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(po.quantity_planned), 0),
		       COALESCE(SUM(COALESCE(po.quantity_produced, 0)), 0),
		       COALESCE(SUM(COALESCE(po.quantity_scrapped, 0)), 0),
		       COALESCE(SUM(COALESCE(NULLIF(po.actual_cost, 0), po.planned_cost, 0)), 0)
		FROM production_orders po
		WHERE `+reportSetWhere+`
	`, tenantID, from, to, orgArg).Scan(&totOrders, &totPlanned, &totProduced, &totScrapped, &totCost); err != nil {
		h.log.Error("Failed to load production report totals", "error", err)
	}
	totScrapRate := 0.0
	if totProduced+totScrapped > 0 {
		totScrapRate = totScrapped / (totProduced + totScrapped) * 100
	}

	// ── Per-product breakdown (top 50 by produced) ──────────────────────
	type productRow struct {
		ProductName string  `json:"product_name"`
		Orders      int     `json:"orders"`
		Planned     float64 `json:"planned"`
		Produced    float64 `json:"produced"`
		Scrapped    float64 `json:"scrapped"`
		ScrapRate   float64 `json:"scrap_rate"`
		TotalCost   float64 `json:"total_cost"`
	}
	byProduct := []productRow{}
	if rows, err := h.db.Query(`
		SELECT COALESCE(p.name, '—'), COUNT(*),
		       COALESCE(SUM(po.quantity_planned), 0),
		       COALESCE(SUM(COALESCE(po.quantity_produced, 0)), 0),
		       COALESCE(SUM(COALESCE(po.quantity_scrapped, 0)), 0),
		       COALESCE(SUM(COALESCE(NULLIF(po.actual_cost, 0), po.planned_cost, 0)), 0)
		FROM production_orders po
		LEFT JOIN products p ON p.id = po.product_id
		WHERE `+reportSetWhere+`
		GROUP BY COALESCE(p.name, '—')
		ORDER BY COALESCE(SUM(COALESCE(po.quantity_produced, 0)), 0) DESC
		LIMIT 50
	`, tenantID, from, to, orgArg); err == nil {
		for rows.Next() {
			var r productRow
			if rows.Scan(&r.ProductName, &r.Orders, &r.Planned, &r.Produced, &r.Scrapped, &r.TotalCost) == nil {
				if r.Produced+r.Scrapped > 0 {
					r.ScrapRate = r.Scrapped / (r.Produced + r.Scrapped) * 100
				}
				byProduct = append(byProduct, r)
			}
		}
		rows.Close()
	} else {
		h.log.Error("Failed to load production report by_product", "error", err)
	}

	// ── Per-category breakdown (uncategorized → 'Boshqa') ───────────────
	type categoryRow struct {
		CategoryName string  `json:"category_name"`
		Orders       int     `json:"orders"`
		Produced     float64 `json:"produced"`
		Scrapped     float64 `json:"scrapped"`
		TotalCost    float64 `json:"total_cost"`
	}
	byCategory := []categoryRow{}
	if rows, err := h.db.Query(`
		SELECT COALESCE(mc.name, 'Boshqa'), COUNT(*),
		       COALESCE(SUM(COALESCE(po.quantity_produced, 0)), 0),
		       COALESCE(SUM(COALESCE(po.quantity_scrapped, 0)), 0),
		       COALESCE(SUM(COALESCE(NULLIF(po.actual_cost, 0), po.planned_cost, 0)), 0)
		FROM production_orders po
		LEFT JOIN manufacturing_categories mc ON mc.id = po.manufacturing_category_id
		WHERE `+reportSetWhere+`
		GROUP BY COALESCE(mc.name, 'Boshqa')
		ORDER BY COALESCE(SUM(COALESCE(NULLIF(po.actual_cost, 0), po.planned_cost, 0)), 0) DESC
	`, tenantID, from, to, orgArg); err == nil {
		for rows.Next() {
			var r categoryRow
			if rows.Scan(&r.CategoryName, &r.Orders, &r.Produced, &r.Scrapped, &r.TotalCost) == nil {
				byCategory = append(byCategory, r)
			}
		}
		rows.Close()
	} else {
		h.log.Error("Failed to load production report by_category", "error", err)
	}

	// ── Scrap Pareto (top 10; shares vs the period total, cumulative over
	// the ranked list). B5: when quality_checks rows with a defect reason
	// exist in the period, the pareto ranks DEFECT REASONS (quantity_failed
	// per defect_type); otherwise it falls back to per-product scrap.
	// `pareto_by` ('reason'|'product') tells the frontend how to label it.
	type paretoRow struct {
		Name            string  `json:"name"`
		Scrapped        float64 `json:"scrapped"`
		Share           float64 `json:"share"`
		CumulativeShare float64 `json:"cumulative_share"`
	}
	scrapPareto := []paretoRow{}
	paretoBy := "product"

	var defectChecks int
	var defectFailedTotal float64
	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(quantity_failed), 0)
		FROM quality_checks
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND inspection_date >= $2 AND inspection_date <= $3
		  AND COALESCE(defect_type, '') != '' AND COALESCE(quantity_failed, 0) > 0
	`, tenantID, from, to).Scan(&defectChecks, &defectFailedTotal)

	if defectChecks > 0 && defectFailedTotal > 0 {
		paretoBy = "reason"
		if rows, err := h.db.Query(`
			SELECT defect_type, COALESCE(SUM(quantity_failed), 0) AS sc
			FROM quality_checks
			WHERE tenant_id = $1 AND deleted_at IS NULL
			  AND inspection_date >= $2 AND inspection_date <= $3
			  AND COALESCE(defect_type, '') != '' AND COALESCE(quantity_failed, 0) > 0
			GROUP BY defect_type
			ORDER BY sc DESC
			LIMIT 10
		`, tenantID, from, to); err == nil {
			cumulative := 0.0
			for rows.Next() {
				var r paretoRow
				if rows.Scan(&r.Name, &r.Scrapped) == nil {
					r.Share = r.Scrapped / defectFailedTotal * 100
					cumulative += r.Share
					r.CumulativeShare = cumulative
					scrapPareto = append(scrapPareto, r)
				}
			}
			rows.Close()
		} else {
			h.log.Error("Failed to load production report defect pareto", "error", err)
		}
	} else if rows, err := h.db.Query(`
		SELECT COALESCE(p.name, '—'), COALESCE(SUM(COALESCE(po.quantity_scrapped, 0)), 0) AS sc
		FROM production_orders po
		LEFT JOIN products p ON p.id = po.product_id
		WHERE `+reportSetWhere+`
		GROUP BY COALESCE(p.name, '—')
		HAVING COALESCE(SUM(COALESCE(po.quantity_scrapped, 0)), 0) > 0
		ORDER BY sc DESC
		LIMIT 10
	`, tenantID, from, to, orgArg); err == nil {
		cumulative := 0.0
		for rows.Next() {
			var r paretoRow
			if rows.Scan(&r.Name, &r.Scrapped) == nil {
				if totScrapped > 0 {
					r.Share = r.Scrapped / totScrapped * 100
				}
				cumulative += r.Share
				r.CumulativeShare = cumulative
				scrapPareto = append(scrapPareto, r)
			}
		}
		rows.Close()
	} else {
		h.log.Error("Failed to load production report scrap_pareto", "error", err)
	}

	// ── Plan-vs-fact variance (B4): top 15 completed-in-period orders by
	// |actual − planned|. actual_cost is only reliably stamped since the
	// v2 completion rebuild, so NULLIF(…,0) guards keep half-costed legacy
	// rows out instead of reporting a fake 100% variance. NO variance JE is
	// posted — reporting only (finance sign-off pending).
	type varianceRow struct {
		Code            string  `json:"code"`
		ProductName     string  `json:"product_name"`
		PlannedCost     float64 `json:"planned_cost"`
		ActualCost      float64 `json:"actual_cost"`
		Variance        float64 `json:"variance"`
		VariancePercent float64 `json:"variance_percent"`
	}
	variance := []varianceRow{}
	varianceSum := 0.0
	if vRows, vErr := h.db.Query(`
		SELECT po.code, COALESCE(p.name, ''),
		       po.planned_cost, po.actual_cost,
		       po.actual_cost - po.planned_cost AS variance
		FROM production_orders po
		LEFT JOIN products p ON p.id = po.product_id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		  AND po.status = 'completed'
		  AND COALESCE(po.actual_end, po.updated_at) >= $2 AND COALESCE(po.actual_end, po.updated_at) <= $3
		  AND NULLIF(po.actual_cost, 0) IS NOT NULL
		  AND NULLIF(po.planned_cost, 0) IS NOT NULL
		  AND ($4::uuid IS NULL OR po.organization_id = $4)
		ORDER BY ABS(po.actual_cost - po.planned_cost) DESC
		LIMIT 15
	`, tenantID, from, to, orgArg); vErr == nil {
		for vRows.Next() {
			var v varianceRow
			if vRows.Scan(&v.Code, &v.ProductName, &v.PlannedCost, &v.ActualCost, &v.Variance) == nil {
				if v.PlannedCost != 0 {
					v.VariancePercent = v.Variance / v.PlannedCost * 100
				}
				variance = append(variance, v)
			}
		}
		vRows.Close()
	} else {
		h.log.Error("Failed to load production report variance", "error", vErr)
	}
	if err := h.db.QueryRow(`
		SELECT COALESCE(SUM(po.actual_cost - po.planned_cost), 0)
		FROM production_orders po
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		  AND po.status = 'completed'
		  AND COALESCE(po.actual_end, po.updated_at) >= $2 AND COALESCE(po.actual_end, po.updated_at) <= $3
		  AND NULLIF(po.actual_cost, 0) IS NOT NULL
		  AND NULLIF(po.planned_cost, 0) IS NOT NULL
		  AND ($4::uuid IS NULL OR po.organization_id = $4)
	`, tenantID, from, to, orgArg).Scan(&varianceSum); err != nil {
		h.log.Error("Failed to load production report variance sum", "error", err)
	}

	response.Success(c, gin.H{
		"totals": gin.H{
			"orders":       totOrders,
			"planned":      totPlanned,
			"produced":     totProduced,
			"scrapped":     totScrapped,
			"scrap_rate":   totScrapRate,
			"total_cost":   totCost,
			"variance_sum": varianceSum,
		},
		"by_product":   byProduct,
		"by_category":  byCategory,
		"scrap_pareto": scrapPareto,
		"pareto_by":    paretoBy,
		"variance":     variance,
		"as_of":        nowT.Format("2006-01-02"),
	})
}
