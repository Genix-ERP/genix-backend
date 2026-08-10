package handler

import (
	"strings"
	"testing"
	"time"
)

func day(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

// §2.1: the trigger is a MOVEMENT, not the presence of products. A category
// with goods attached but nothing yet received must stay editable — that is the
// normal state during setup, and locking it would force people to delete and
// recreate categories to fix a first-day mistake.
func TestMethodIsEditableUntilSomethingMoves(t *testing.T) {
	if got := evaluateMethodLock(0, nil); got.Locked {
		t.Fatal("no movements must leave the method editable")
	}
	if got := evaluateMethodLock(0, day("2026-03-01")); got.Locked {
		t.Fatal("a date without movements is not a lock")
	}
}

// A lock has to say what to do about it. "Locked" alone gives the user nothing.
func TestLockReasonNamesTheCountAndTheDate(t *testing.T) {
	got := evaluateMethodLock(143, day("2026-03-12"))
	if !got.Locked {
		t.Fatal("143 movements must lock the method")
	}
	if got.MovementCount != 143 {
		t.Fatalf("want 143, got %d", got.MovementCount)
	}
	if got.Since != "2026-03-12" {
		t.Fatalf("want 2026-03-12, got %q", got.Since)
	}
	// The plan writes the message as "N harakat, KK.OO.YYYY dan".
	if !strings.Contains(got.Reason, "143") || !strings.Contains(got.Reason, "12.03.2026") {
		t.Fatalf("the reason must name the count and the date, got %q", got.Reason)
	}
}

// A lock with no date still has to be a lock. Reaching for earliest.Format on a
// nil would panic on the request that discovers it.
func TestLockWithoutADateStillLocks(t *testing.T) {
	got := evaluateMethodLock(5, nil)
	if !got.Locked || got.Reason == "" {
		t.Fatalf("want a locked verdict with a reason, got %+v", got)
	}
	if got.Since != "" {
		t.Fatalf("no date means no since, got %q", got.Since)
	}
}

// "aveco" and "avco" are the same choice. Without normalising, a client that
// PUTs the category object back unchanged would be told it is trying to change
// a locked method — and would be unable to edit the name.
func TestNormaliseMethodTreatsSpellingsAsOneChoice(t *testing.T) {
	if normaliseMethod("aveco") != normaliseMethod("avco") {
		t.Fatal("aveco and avco must normalise to the same value")
	}
	if normaliseMethod("") != "" {
		t.Fatal("empty means inherit and must stay empty")
	}
	// An unparseable value is returned as-is rather than collapsing to a valid
	// one, so a no-op comparison never accidentally succeeds.
	if normaliseMethod("nonsense") != "nonsense" {
		t.Fatal("an unknown method must not be normalised into a real one")
	}
}

// ---------------------------------------------------------- backdating ---

// §2.4: under AVCO the running average is rebuilt movement by movement, so a
// document inserted before the last movement would have had to change every
// average computed after it — and those costs are already posted.
func TestAVCORefusesADocumentBeforeTheLastMovement(t *testing.T) {
	last := day("2026-06-01")
	msg := guardAVCOChronology(CostMethodAVCO, *day("2026-05-31"), last)
	if msg == "" {
		t.Fatal("a backdated AVCO document must be refused")
	}
	if !strings.Contains(msg, "01.06.2026") {
		t.Fatalf("the message must name the blocking date, got %q", msg)
	}
}

func TestAVCOAllowsSameDayAndLater(t *testing.T) {
	last := day("2026-06-01")
	if msg := guardAVCOChronology(CostMethodAVCO, *day("2026-06-01"), last); msg != "" {
		t.Fatalf("the same day must be allowed, got %q", msg)
	}
	if msg := guardAVCOChronology(CostMethodAVCO, *day("2026-06-02"), last); msg != "" {
		t.Fatalf("a later day must be allowed, got %q", msg)
	}
}

// FIFO consumes layers by date, so a late insert simply takes its place in the
// order; standard does not depend on chronology at all. Applying the AVCO rule
// to them would block corrections that are perfectly safe.
func TestChronologyRuleIsAVCOOnly(t *testing.T) {
	last := day("2026-06-01")
	early := *day("2026-01-01")
	if msg := guardAVCOChronology(CostMethodFIFO, early, last); msg != "" {
		t.Fatalf("FIFO must not be blocked, got %q", msg)
	}
	if msg := guardAVCOChronology(CostMethodStandard, early, last); msg != "" {
		t.Fatalf("standard must not be blocked, got %q", msg)
	}
}

// A product with no history yet has no chronology to violate.
func TestChronologyAllowsTheFirstMovement(t *testing.T) {
	if msg := guardAVCOChronology(CostMethodAVCO, *day("2020-01-01"), nil); msg != "" {
		t.Fatalf("the first movement must be allowed, got %q", msg)
	}
}

// -------------------------------------------------------------- storno ---

// §2.6: reversing a receipt whose goods have since been issued would leave
// those issues costed against a layer that no longer exists. Refusing is the
// whole point — silently allowing it is how a warehouse ends up with a negative
// layer and a cost of sale nobody can trace.
func TestStornoRefusesAConsumedReceipt(t *testing.T) {
	plan := StornoPlan{Blocked: "already issued"}
	if err := applyStorno(nil, plan); err == nil {
		t.Fatal("a blocked plan must return an error rather than write anything")
	}
}

// An unblocked plan with nothing to do must not error — cancelling a document
// that never moved stock is legitimate.
func TestStornoOfANonStockDocumentIsANoop(t *testing.T) {
	if err := applyStorno(nil, StornoPlan{}); err != nil {
		t.Fatalf("an empty plan must succeed, got %v", err)
	}
}

// A storno gives back exactly what the consumption took — the value from the
// consumption record, not a recomputation at today's cost. That is the same
// reason returns are priced at the original issue cost.
func TestStornoReturnsTheRecordedValue(t *testing.T) {
	layers := []Layer{layer("A", 1, 1, 10, som(10_000))}
	res, err := FIFOIssue(layers, Rat(3, 1))
	if err != nil {
		t.Fatal(err)
	}
	afterIssue := StockValue(layers)

	// Undo it the way applyStorno does: add the consumption back.
	for _, c := range res.Consumptions {
		for i := range layers {
			if layers[i].ID == c.LayerID {
				layers[i].RemainingQty.Add(layers[i].RemainingQty, c.Qty)
				layers[i].RemainingValue += c.Value
			}
		}
	}

	if got := StockValue(layers); got != som(10_000) {
		t.Fatalf("storno must restore the layer exactly: want %d, got %d", som(10_000), got)
	}
	if afterIssue+res.Cost != som(10_000) {
		t.Fatalf("the issue and the restore must be the same number: %d + %d", afterIssue, res.Cost)
	}
}
