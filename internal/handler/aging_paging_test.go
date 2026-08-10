package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/genixerp/genix-backend/internal/domain/entity"
)

// agingFilterSortPage and agingPercentages are pure — no database, no handler —
// so there is no excuse for not pinning them. An earlier commit message claimed
// these cases were covered when the tests had been run and then deleted; this
// file is that claim made true.

func agingCtx(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/aging?"+query, nil)
	return c
}

func agingContacts(names ...string) []entity.AgingContact {
	out := make([]entity.AgingContact, len(names))
	for i, n := range names {
		out[i] = entity.AgingContact{
			ContactName: n,
			Current:     10,
			Over90Days:  5,
			TotalAmount: 15,
		}
	}
	return out
}

// A negative grand total must not blank every bucket. This is the A3 defect:
// the web guarded on `total > 0`, so when credits outweighed invoices it printed
// a confident 0.0% beside a real 32.5m sitting in 90+.
func TestAgingPercentagesIgnoreNegativeGrandTotal(t *testing.T) {
	p := agingPercentages(-79_000_000, 0, 0, 0, 32_509_032)
	if p == nil {
		t.Fatal("a positive bucket exists, so a share is computable")
	}
	if p.Over90Days != 100 {
		t.Fatalf("90+ is the only positive bucket: want 100, got %v", p.Over90Days)
	}
	if p.Current != 0 {
		t.Fatalf("a negative bucket contributes nothing: want 0, got %v", p.Current)
	}
}

// Nothing positive means the share is undefined, not zero.
func TestAgingPercentagesNilWhenNothingPositive(t *testing.T) {
	if got := agingPercentages(-5, 0, 0, -1, 0); got != nil {
		t.Fatalf("want nil so clients render an em dash, got %+v", got)
	}
}

func TestAgingPercentagesSplit(t *testing.T) {
	p := agingPercentages(25, 25, 0, 0, 50)
	if p == nil || p.Current != 25 || p.Days1To30 != 25 || p.Over90Days != 50 {
		t.Fatalf("want 25/25/50, got %+v", p)
	}
}

// Paging must stay opt-in: both shipped clients render whatever comes back, so
// a default page size would silently truncate them.
func TestAgingPagingIsOptIn(t *testing.T) {
	in := agingContacts("A", "B", "C")
	got, tot, meta := agingFilterSortPage(agingCtx(""), in)
	if len(got) != 3 || meta != nil {
		t.Fatalf("no params must return everything unpaged: %d rows, meta=%v", len(got), meta)
	}
	if tot.TotalAmount != 45 {
		t.Fatalf("totals cover all three contacts: want 45, got %v", tot.TotalAmount)
	}
}

func TestAgingPagingWindow(t *testing.T) {
	in := agingContacts("A", "B", "C", "D", "E")
	got, tot, meta := agingFilterSortPage(agingCtx("page=2&page_size=2"), in)
	if len(got) != 2 {
		t.Fatalf("page 2 of 5@2 has 2 rows, got %d", len(got))
	}
	if meta == nil || meta.Total != 5 || meta.TotalPages != 3 || meta.Page != 2 {
		t.Fatalf("meta describes the whole set: %+v", meta)
	}
	// Paging is a window, NOT a filter: the totals still cover all five.
	if tot.TotalAmount != 75 {
		t.Fatalf("paging must not narrow the totals: want 75, got %v", tot.TotalAmount)
	}
}

// Search DOES narrow the totals, so the cards and the rows describe the same
// set — otherwise searching "Tesla" leaves tenant-wide cards over Tesla's rows,
// which is the web's A6 defect moved to the server.
func TestAgingSearchNarrowsTotals(t *testing.T) {
	in := agingContacts("Tesla Motors", "Alfa", "Beta")
	got, tot, _ := agingFilterSortPage(agingCtx("search=tesla"), in)
	if len(got) != 1 || got[0].ContactName != "Tesla Motors" {
		t.Fatalf("search should keep only Tesla, got %+v", got)
	}
	if tot.TotalAmount != 15 {
		t.Fatalf("totals must follow the search: want 15, got %v", tot.TotalAmount)
	}
}

// A page past the end returns nothing rather than panicking on a bad slice.
func TestAgingPagePastEndDoesNotPanic(t *testing.T) {
	got, _, meta := agingFilterSortPage(agingCtx("page=99&page_size=10"), agingContacts("A", "B"))
	if len(got) != 0 {
		t.Fatalf("past the end is empty, got %d", len(got))
	}
	if meta == nil || meta.Total != 2 {
		t.Fatalf("meta still describes the set: %+v", meta)
	}
}

// varied builds contacts whose buckets differ, so a sort has something to do.
func varied() []entity.AgingContact {
	return []entity.AgingContact{
		{ContactName: "Beta", Current: 5, Over90Days: 300, TotalAmount: 305},
		{ContactName: "Alfa", Current: 90, Over90Days: 10, TotalAmount: 100},
		{ContactName: "Gamma", Current: 50, Over90Days: 200, TotalAmount: 250},
	}
}

func names(cs []entity.AgingContact) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ContactName
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Sorting has to happen server-side once the list is paginated: reordering the
// twenty rows of one page and labelling the result "largest debts" is worse
// than offering no control at all.
func TestAgingSortByBucket(t *testing.T) {
	got, _, _ := agingFilterSortPage(agingCtx("sort=over_90_days"), varied())
	if want := []string{"Beta", "Gamma", "Alfa"}; !eq(names(got), want) {
		t.Fatalf("90+ descending: want %v, got %v", want, names(got))
	}
	got, _, _ = agingFilterSortPage(agingCtx("sort=over_90_days&order=asc"), varied())
	if want := []string{"Alfa", "Gamma", "Beta"}; !eq(names(got), want) {
		t.Fatalf("90+ ascending: want %v, got %v", want, names(got))
	}
}

func TestAgingSortByName(t *testing.T) {
	got, _, _ := agingFilterSortPage(agingCtx("sort=contact_name&order=asc"), varied())
	if want := []string{"Alfa", "Beta", "Gamma"}; !eq(names(got), want) {
		t.Fatalf("want %v, got %v", want, names(got))
	}
}

// A typo in a presentation parameter must not take the report down.
func TestAgingSortUnknownFieldFallsBackToTotal(t *testing.T) {
	got, _, _ := agingFilterSortPage(agingCtx("sort=nonsense"), varied())
	if want := []string{"Beta", "Gamma", "Alfa"}; !eq(names(got), want) {
		t.Fatalf("unknown sort must behave like the default: want %v, got %v", want, names(got))
	}
}

// Equal balances must order deterministically, or LIMIT/OFFSET pages can repeat
// one contact and skip another.
func TestAgingSortIsStableOnTiedBalances(t *testing.T) {
	in := agingContacts("Zeta", "Alfa", "Mira")
	first, _, _ := agingFilterSortPage(agingCtx("page=1&page_size=2"), in)
	second, _, _ := agingFilterSortPage(agingCtx("page=2&page_size=2"), in)
	seen := map[string]bool{}
	for _, c := range append(append([]entity.AgingContact{}, first...), second...) {
		if seen[c.ContactName] {
			t.Fatalf("%s appeared on both pages", c.ContactName)
		}
		seen[c.ContactName] = true
	}
	if len(seen) != 3 {
		t.Fatalf("the two pages must cover all three contacts, saw %d", len(seen))
	}
}
