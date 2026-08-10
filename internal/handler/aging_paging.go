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

// agingFilterSortPage narrows a finished contact list by `search`, orders it,
// and returns the requested page.
//
// Paging is OPT-IN: with no `page` and no `page_size` the full list comes back,
// exactly as before. That matters because both shipped clients render whatever
// the response contains — defaulting to 20 would have made them show the first
// twenty partners under totals describing all of them, wrong and silent, on
// three separately-deployed backends.
//
// The returned meta is nil when the caller did not opt in.
func agingFilterSortPage(c *gin.Context, contacts []entity.AgingContact) ([]entity.AgingContact, *entity.AgingMeta) {
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
		return contacts, nil
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

	return contacts[start:end], &entity.AgingMeta{
		Page: page, PageSize: size, Total: total, TotalPages: totalPages,
	}
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
	pct := func(v float64) float64 { return math.Round((v/denom)*1000) / 10 }
	return &entity.AgingPercentages{
		Current:    pct(current),
		Days1To30:  pct(d1),
		Days31To60: pct(d2),
		Days61To90: pct(d3),
		Over90Days: pct(over90),
	}
}
