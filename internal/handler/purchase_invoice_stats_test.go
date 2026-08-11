package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// The vendor-bill cards and the list beneath them must answer with the same
// vocabulary. They did not: /purchase-invoices/stats wrote its own status list
// and its own payment-status expression, so a draft bill counted as debt on one
// screen and not on another, and "To'lanmagan: 4" opened a list of 5.
//
// These are string assertions on a pure builder rather than row assertions,
// which is the point: the defect was never a wrong number, it was a SECOND
// definition. A behavioural test would pass again the moment someone re-spelled
// the rule in a way that happens to agree today.

func statsSQL() string {
	return purchaseInvoiceStatsQuery("WHERE pi.tenant_id = $1 AND pi.deleted_at IS NULL")
}

// B1. 'draft' must not be reachable as debt through any hand-written list.
func TestStatsUsesSharedDebtVocabulary(t *testing.T) {
	sql := statsSQL()
	if strings.Contains(sql, "NOT IN ('paid', 'cancelled')") {
		t.Error("the local status list is back; debt vocabulary belongs to debtStatusFilter")
	}
	if !strings.Contains(sql, debtStatusFilterFor("pi")) {
		t.Fatalf("want %q in the query", debtStatusFilterFor("pi"))
	}
	// One draft bill used to raise unpaid/outstanding/overdue AND draft_count,
	// so the same document appeared twice in one row of cards.
	for _, card := range []string{"unpaid_count", "unpaid_amount", "partial_count",
		"partial_amount", "outstanding_amount"} {
		if !debtFiltered(sql, card) {
			t.Errorf("%s is a debt figure and must carry the debt status filter", card)
		}
	}
}

// B3. The cards classify payment state with the same expression the
// payment_status= filter uses, so clicking a card cannot contradict it.
func TestStatsUsesSharedPaymentStatus(t *testing.T) {
	sql := statsSQL()
	if !strings.Contains(sql, invoicePaymentStatusSQL("pi")) {
		t.Fatal("stats must classify payment state with invoicePaymentStatusSQL")
	}
	// The old hand-written forms, both of which disagreed with it.
	for _, gone := range []string{
		"pi.amount_paid IS NULL OR pi.amount_paid = 0",
		"pi.amount_paid > 0 AND pi.amount_paid < pi.total_amount",
	} {
		if strings.Contains(sql, gone) {
			t.Errorf("hand-written payment test is back: %s", gone)
		}
	}
}

// B2. The Xaridlar module's "To'langan" card, which the browser used to compute
// from a key the API never sent (paid_amount vs the amount_paid column) and so
// rendered NaN.
func TestStatsExposesPaidFigures(t *testing.T) {
	sql := statsSQL()
	for _, col := range []string{"paid_count", "paid_amount"} {
		if !strings.Contains(sql, "AS "+col) {
			t.Errorf("missing %s", col)
		}
	}
	// paid_amount is money paid across every document in scope — it pairs with
	// total_amount, which is also unfiltered. Giving it a debt filter would make
	// total_amount - paid_amount stop meaning anything.
	if debtFiltered(sql, "paid_amount") {
		t.Error("paid_amount must not be debt-filtered; it is the money side of total_amount")
	}
}

// total_count / total_amount count documents, not debt. The spec is explicit
// that these two must not move: they are what "Jami hisob-fakturalar" and
// "Jami summa" mean on the card.
func TestStatsDocumentTotalsAreUnfiltered(t *testing.T) {
	sql := statsSQL()
	if !strings.Contains(sql, "COUNT(*) AS total_count") {
		t.Error("total_count must stay a bare COUNT(*) over the filtered rows")
	}
	if !strings.Contains(sql, "COALESCE(SUM(pi.total_amount), 0) AS total_amount") {
		t.Error("total_amount must stay a bare SUM over the filtered rows")
	}
	if debtFiltered(sql, "total_amount") {
		t.Error("total_amount counts documents, so it must carry no debt filter")
	}
}

// The handler Scans positionally, so column order is part of the contract —
// adding paid_count/paid_amount in the wrong place would silently swap two
// numbers on the cards rather than fail.
func TestStatsColumnOrderMatchesScan(t *testing.T) {
	want := []string{
		"total_count", "total_amount",
		"unpaid_count", "unpaid_amount",
		"partial_count", "partial_amount",
		"paid_count", "paid_amount",
		"overdue_count", "overdue_amount",
		"outstanding_amount", "draft_count",
	}
	sql := statsSQL()
	at := 0
	for _, col := range want {
		i := strings.Index(sql[at:], "AS "+col)
		if i < 0 {
			t.Fatalf("column %s missing or out of order (expected after %d)", col, at)
		}
		at += i
	}
}

// The card and the list it links to must be able to describe the same set.
// Sharing invoicePaymentStatusSQL (B3) does not achieve that by itself, because
// the card also applies debtStatusFilter — with the shared payment expression
// alone, "To'lanmagan: 1" opens a list of 4 (a draft, a cancellation and a void
// ride along). debt=true is how the card says the rest of what it means.
func TestDebtFilterIsOptInAndMatchesTheCards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := func(query string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/purchase-invoices?"+query, nil)
		return c
	}

	var args []interface{}
	if w := purchaseInvoiceWhere(ctx(""), &args); strings.Contains(w, debtStatusFilterFor("pi")) {
		t.Error("no debt= parameter must leave every existing caller's result set alone")
	}
	args = nil
	if w := purchaseInvoiceWhere(ctx("debt=false"), &args); strings.Contains(w, debtStatusFilterFor("pi")) {
		t.Error("only debt=true narrows")
	}
	args = nil
	if w := purchaseInvoiceWhere(ctx("payment_status=unpaid&debt=true"), &args); !strings.Contains(w, debtStatusFilterFor("pi")) {
		t.Error("debt=true must apply the same status vocabulary the cards use")
	}
	// The sales twin, so the AR cards can close the same gap.
	args = nil
	if w := salesInvoiceWhere(ctx("debt=true"), &args); !strings.Contains(w, debtStatusFilterFor("si")) {
		t.Error("salesInvoiceWhere must honor debt=true with its own alias")
	}
}

// debtFiltered reports whether the column named by alias is aggregated under a
// FILTER carrying the debt status predicate. It reads the fragment between the
// previous column boundary and "AS <alias>".
func debtFiltered(sql, alias string) bool {
	end := strings.Index(sql, "AS "+alias)
	if end < 0 {
		return false
	}
	frag := sql[:end]
	if i := strings.LastIndex(frag, "COUNT(*)"); i >= 0 {
		if j := strings.LastIndex(frag, "COALESCE(SUM("); j > i {
			frag = frag[j:]
		} else {
			frag = frag[i:]
		}
	} else if j := strings.LastIndex(frag, "COALESCE(SUM("); j >= 0 {
		frag = frag[j:]
	}
	return strings.Contains(frag, debtStatusFilterFor("pi"))
}
