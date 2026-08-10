package handler

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/genixerp/genix-backend/internal/domain/entity"
)

// Search, paging and bucket shares for the two aging reports.
//
// Both endpoints read only as_of_date, so a tenant with many partners loaded
// every contact AND every one of its invoice lines on each open, and neither
// client could satisfy the paging rule. Search was client-side, which is why the
// web's cards silently changed meaning as soon as anyone typed.
//
// Applied AFTER the FIFO pass, deliberately. Aging is computed over the whole
// ledger — FIFO consumes a contact's oldest invoices with that contact's
// credits, so filtering rows out beforehand would change what the remaining
// numbers mean. Search and paging are presentation over a finished report.

// agingFilterSortPage narrows a finished contact list by `search`, RECOMPUTES
// the grand totals over what survived, orders the list, and returns the
// requested page.
//
// The totals describe the whole FILTERED set — not the whole ledger, and not the
// page. That distinction is the point: search "Tesla" and the table shows
// Tesla's rows, so cards still describing the entire tenant are the web's A6
// defect moved server-side. Paging, by contrast, must NOT narrow them: a page is
// a window on the same filtered set.
//
// Paging is OPT-IN: with no `page` and no `page_size` the full list comes back,
// exactly as before. That matters because both shipped clients render whatever
// the response contains — defaulting to 20 would have made them show the first
// twenty partners under totals describing all of them, wrong and silent, on
// three separately-deployed backends.
//
// The returned meta is nil when the caller did not opt in.
func agingFilterSortPage(c *gin.Context, contacts []entity.AgingContact) ([]entity.AgingContact, agingTotals, *entity.AgingMeta) {
	if q := strings.TrimSpace(c.Query("search")); q != "" {
		needle := strings.ToLower(q)
		kept := make([]entity.AgingContact, 0, len(contacts))
		for _, ct := range contacts {
			if strings.Contains(strings.ToLower(ct.ContactName), needle) {
				kept = append(kept, ct)
			}
		}
		contacts = kept
	}

	// Summed from the surviving contacts. This is the same arithmetic the
	// handler already does to build its grand totals (it accumulates from the
	// same per-contact buckets after FIFO), so an unfiltered call reproduces
	// them exactly — it is not a second, divergent definition.
	var tot agingTotals
	for _, ct := range contacts {
		tot.Current += ct.Current
		tot.Days1To30 += ct.Days1To30
		tot.Days31To60 += ct.Days31To60
		tot.Days61To90 += ct.Days61To90
		tot.Over90Days += ct.Over90Days
		tot.TotalAmount += ct.TotalAmount
	}

	// Largest balance first — the order both screens want by default — with the
	// name as a stable tiebreaker so LIMIT/OFFSET pages cannot repeat or skip a
	// contact when several share a balance.
	sort.SliceStable(contacts, func(i, j int) bool {
		if contacts[i].TotalAmount != contacts[j].TotalAmount {
			return contacts[i].TotalAmount > contacts[j].TotalAmount
		}
		return contacts[i].ContactName < contacts[j].ContactName
	})

	pageStr, sizeStr := c.Query("page"), c.Query("page_size")
	if pageStr == "" && sizeStr == "" {
		return contacts, tot, nil
	}

	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(sizeStr)
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}

	total := len(contacts)
	totalPages := (total + size - 1) / size
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}

	return contacts[start:end], tot, &entity.AgingMeta{
		Page: page, PageSize: size, Total: total, TotalPages: totalPages,
	}
}

// agingTotals carries the six grand figures over the filtered set.
type agingTotals struct {
	Current, Days1To30, Days31To60, Days61To90, Over90Days, TotalAmount float64
}

// agingPercentages returns each bucket's share of the positive total.
//
// The denominator is the sum of the POSITIVE buckets, not the grand total: when
// credits outweigh invoices the grand total goes negative, and a percentage of a
// negative base is not a share of anything. nil when nothing is positive, which
// the clients render as "—" rather than a confident 0.0%.
func agingPercentages(current, d1, d2, d3, over90 float64) *entity.AgingPercentages {
	denom := 0.0
	for _, v := range []float64{current, d1, d2, d3, over90} {
		if v > 0 {
			denom += v
		}
	}
	if denom <= 0 {
		return nil
	}
	// The denominator is the positive total, i.e. the debt outstanding, so only
	// a positive bucket has a share OF it. A negative bucket holds net credit
	// and contributes nothing to the debt — reported as 0 rather than as the
	// arithmetically-consistent but unreadable negative share (a -79m bucket
	// against a 32.5m denominator renders as "-243.0%" on a card). No
	// information is lost: the amount beside it already carries the sign.
	pct := func(v float64) float64 {
		if v <= 0 {
			return 0
		}
		return math.Round((v/denom)*1000) / 10
	}
	return &entity.AgingPercentages{
		Current:    pct(current),
		Days1To30:  pct(d1),
		Days31To60: pct(d2),
		Days61To90: pct(d3),
		Over90Days: pct(over90),
	}
}
