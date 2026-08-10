package handler

import (
	"math/big"
	"testing"
)

// Test-keyslar from §7 of the implementation plan, plus the rounding-drift
// case §3.5 calls for. The engine is pure, so all of this runs without a
// database.

func layer(id string, seq, day int64, qty int64, value int64) Layer {
	return Layer{
		ID: id, SeqNo: seq, DateOrdinal: day,
		RemainingQty: big.NewRat(qty, 1), RemainingValue: value,
	}
}

// som converts whole so'm to tiyin, so the test cases read like the plan.
func som(v int64) int64 { return v * 100 }

func mustIssue(t *testing.T, layers []Layer, qty *big.Rat) IssueResult {
	t.Helper()
	res, err := FIFOIssue(layers, qty)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

// ---------------------------------------------------------------- FIFO ---

// The plan's worked example: receipts 10 × 1 000 and 10 × 1 200, issue 15.
// COGS = 10×1 000 + 5×1 200 = 16 000; the 5 left are valued at the newer price.
func TestFIFOWorkedExample(t *testing.T) {
	layers := []Layer{
		layer("A", 1, 1, 10, som(10_000)),
		layer("B", 2, 2, 10, som(12_000)),
	}
	res := mustIssue(t, layers, big.NewRat(15, 1))
	if res.Cost != som(16_000) {
		t.Fatalf("COGS: want %d, got %d", som(16_000), res.Cost)
	}
	if got := StockValue(layers); got != som(6_000) {
		t.Fatalf("remaining value: want %d, got %d", som(6_000), got)
	}
	if got := StockQty(layers); got.Cmp(big.NewRat(5, 1)) != 0 {
		t.Fatalf("remaining qty: want 5, got %s", got.RatString())
	}
}

// Part of a single layer.
func TestFIFOPartialLayer(t *testing.T) {
	layers := []Layer{layer("A", 1, 1, 10, som(1_000))}
	res := mustIssue(t, layers, big.NewRat(3, 1))
	if res.Cost != som(300) {
		t.Fatalf("want 300 so'm, got %d tiyin", res.Cost)
	}
	if len(res.Consumptions) != 1 || res.Consumptions[0].LayerID != "A" {
		t.Fatalf("one consumption from A expected, got %+v", res.Consumptions)
	}
}

// Across three layers, with the consumption trail recorded per layer.
func TestFIFOAcrossThreeLayers(t *testing.T) {
	layers := []Layer{
		layer("A", 1, 1, 5, som(500)),
		layer("B", 2, 2, 5, som(600)),
		layer("C", 3, 3, 5, som(700)),
	}
	res := mustIssue(t, layers, big.NewRat(12, 1))
	if res.Cost != som(500)+som(600)+som(280) {
		t.Fatalf("want 1380 so'm, got %d tiyin", res.Cost)
	}
	if len(res.Consumptions) != 3 {
		t.Fatalf("expected a consumption record per layer touched, got %d", len(res.Consumptions))
	}
	if got := StockValue(layers); got != som(420) {
		t.Fatalf("remaining: want 420 so'm, got %d tiyin", got)
	}
}

// The tiyin case the plan singles out: 3 units for 10 000 divides into
// 3 333,33(3). Issuing all three must give back exactly 10 000,00 and leave
// nothing behind — that is the whole reason a full take yields the layer's
// entire remaining value instead of a recomputed proportion.
func TestFIFOFullDrainLeavesNoTiyin(t *testing.T) {
	layers := []Layer{layer("A", 1, 1, 3, som(10_000))}

	first := mustIssue(t, layers, big.NewRat(1, 1))
	second := mustIssue(t, layers, big.NewRat(1, 1))
	third := mustIssue(t, layers, big.NewRat(1, 1))

	if total := first.Cost + second.Cost + third.Cost; total != som(10_000) {
		t.Fatalf("the three issues must sum to the layer exactly: want %d, got %d", som(10_000), total)
	}
	if got := StockValue(layers); got != 0 {
		t.Fatalf("a fully drained layer must hold no value, got %d tiyin", got)
	}
	if got := StockQty(layers); got.Sign() != 0 {
		t.Fatalf("a fully drained layer must hold no quantity, got %s", got.RatString())
	}
	// And the individual figures are the plan's own: 3 333,33 + 3 333,34 +
	// 3 333,33. The middle issue is the one that rounds up — after the first
	// took 333 333 the layer holds 666 667 against 2 units, and half-up on
	// 333 333,5 goes to 333 334. The last then takes what is left. Any
	// arrangement summing to 10 000,00 with nothing stranded satisfies the
	// plan, but pinning the exact split is what would catch a change of
	// rounding mode.
	if first.Cost != 333333 || second.Cost != 333334 || third.Cost != 333333 {
		t.Fatalf("want 333333/333334/333333 tiyin, got %d/%d/%d", first.Cost, second.Cost, third.Cost)
	}
}

// An issue larger than the balance is refused (§2.5). Every method depends on
// this: without it each would have to invent a cost for goods that are absent.
func TestFIFORefusesToGoNegative(t *testing.T) {
	layers := []Layer{layer("A", 1, 1, 5, som(500))}
	if _, err := FIFOIssue(layers, big.NewRat(6, 1)); err != ErrInsufficientStock {
		t.Fatalf("want ErrInsufficientStock, got %v", err)
	}
	// And the layers must be untouched after a refusal.
	if StockValue(layers) != som(500) {
		t.Fatal("a refused issue must not consume anything")
	}
}

// Same-day receipts drain in insertion order, deterministically. Without the
// tiebreaker two runs of the same issue could pick different layers and produce
// different costs from identical inputs.
func TestFIFOSameDateIsDeterministic(t *testing.T) {
	build := func() []Layer {
		return []Layer{
			layer("second", 2, 7, 10, som(2_000)),
			layer("first", 1, 7, 10, som(1_000)),
		}
	}
	a := mustIssue(t, build(), big.NewRat(10, 1))
	b := mustIssue(t, build(), big.NewRat(10, 1))
	if a.Cost != b.Cost {
		t.Fatalf("identical inputs gave %d and %d", a.Cost, b.Cost)
	}
	if a.Cost != som(1_000) {
		t.Fatalf("the earlier-inserted layer goes first: want 1000 so'm, got %d tiyin", a.Cost)
	}
}

// ------------------------------------------------------------- returns ---

// A customer return re-enters at the ORIGINAL issue cost, not at today's price
// (§3.1). Otherwise selling and un-selling one item across a price rise books a
// profit out of nothing.
func TestReturnUsesOriginalIssueCost(t *testing.T) {
	// Sold 10 for 16 000; two come back.
	value, err := ReturnValue(big.NewRat(2, 1), big.NewRat(10, 1), som(16_000))
	if err != nil {
		t.Fatal(err)
	}
	if value != som(3_200) {
		t.Fatalf("want 3 200 so'm, got %d tiyin", value)
	}
}

func TestFullReturnGivesBackTheWholeCost(t *testing.T) {
	value, err := ReturnValue(big.NewRat(3, 1), big.NewRat(3, 1), 333333)
	if err != nil {
		t.Fatal(err)
	}
	if value != 333333 {
		t.Fatalf("a full return must return the exact original cost, got %d", value)
	}
}

func TestReturnCannotExceedWhatWasIssued(t *testing.T) {
	if _, err := ReturnValue(big.NewRat(4, 1), big.NewRat(3, 1), som(100)); err == nil {
		t.Fatal("returning more than was issued must be refused")
	}
}

// ---------------------------------------------------------------- AVCO ---

// The plan's §7 sequence: receive 10×100, receive 10×200, issue 15 at the
// average of 150.
func TestAVCOWorkedExample(t *testing.T) {
	s := AVCOState{Qty: new(big.Rat)}
	s, _ = AVCOReceipt(s, big.NewRat(10, 1), som(1_000))
	s, _ = AVCOReceipt(s, big.NewRat(10, 1), som(2_000))

	cost, s, err := AVCOIssue(s, big.NewRat(15, 1))
	if err != nil {
		t.Fatal(err)
	}
	if cost != som(2_250) {
		t.Fatalf("15 at an average of 150: want 2 250 so'm, got %d tiyin", cost)
	}
	if s.Value != som(750) {
		t.Fatalf("remaining value: want 750 so'm, got %d tiyin", s.Value)
	}
}

// Issues never move the average; only receipts do (§3.2).
func TestAVCOIssuesDoNotMoveTheAverage(t *testing.T) {
	s := AVCOState{Qty: big.NewRat(10, 1), Value: som(1_000)}
	avgBefore := new(big.Rat).SetFrac(big.NewInt(s.Value), big.NewInt(1))
	avgBefore.Quo(avgBefore, s.Qty)

	_, s, _ = AVCOIssue(s, big.NewRat(4, 1))
	avgAfter := new(big.Rat).SetFrac(big.NewInt(s.Value), big.NewInt(1))
	avgAfter.Quo(avgAfter, s.Qty)

	if avgBefore.Cmp(avgAfter) != 0 {
		t.Fatalf("average moved on an issue: %s -> %s", avgBefore.RatString(), avgAfter.RatString())
	}
}

// Taking the balance to zero must take the entire value with it. This is what
// product_avco_state's CHECK (quantity > 0 OR value = 0) enforces in the
// database; here it is enforced in the arithmetic.
func TestAVCOZeroingTakesTheWholeValue(t *testing.T) {
	s := AVCOState{Qty: big.NewRat(3, 1), Value: som(10_000)}
	first, s, _ := AVCOIssue(s, big.NewRat(1, 1))
	second, s, _ := AVCOIssue(s, big.NewRat(1, 1))
	third, s, _ := AVCOIssue(s, big.NewRat(1, 1))

	if s.Qty.Sign() != 0 {
		t.Fatalf("quantity should be zero, got %s", s.Qty.RatString())
	}
	if s.Value != 0 {
		t.Fatalf("value must be zero when quantity is: %d tiyin left behind", s.Value)
	}
	// Same split as the FIFO case, and for the same reason — the middle issue
	// rounds up, the last takes whatever remains.
	if first+second+third != som(10_000) {
		t.Fatalf("the three issues must sum to 10 000,00: got %d", first+second+third)
	}
	if third != 333333 {
		t.Fatalf("the last issue takes the remainder: want 333333, got %d", third)
	}
}

// After the balance goes to zero the next receipt starts a fresh average
// rather than inheriting anything.
func TestAVCORestartsAfterZero(t *testing.T) {
	s := AVCOState{Qty: big.NewRat(5, 1), Value: som(500)}
	_, s, _ = AVCOIssue(s, big.NewRat(5, 1))
	s, _ = AVCOReceipt(s, big.NewRat(2, 1), som(600))

	if s.Value != som(600) || s.Qty.Cmp(big.NewRat(2, 1)) != 0 {
		t.Fatalf("want 2 units at 600 so'm, got %s units / %d tiyin", s.Qty.RatString(), s.Value)
	}
}

func TestAVCORefusesToGoNegative(t *testing.T) {
	s := AVCOState{Qty: big.NewRat(2, 1), Value: som(200)}
	if _, _, err := AVCOIssue(s, big.NewRat(3, 1)); err != ErrInsufficientStock {
		t.Fatalf("want ErrInsufficientStock, got %v", err)
	}
}

// §7's drift test, at scale: ten thousand movements at prices that do not
// divide evenly. The invariant must survive all of them, and the balance must
// land exactly on zero rather than a few tiyin away.
func TestAVCONoRoundingDriftOverManyMovements(t *testing.T) {
	s := AVCOState{Qty: new(big.Rat)}
	var received, issued int64

	for i := 0; i < 10_000; i++ {
		// Awkward on purpose: 7 units for a price that is not a multiple of 7.
		s, _ = AVCOReceipt(s, big.NewRat(7, 1), 100_003+int64(i))
		received += 100_003 + int64(i)

		cost, next, err := AVCOIssue(s, big.NewRat(3, 1))
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		s = next
		issued += cost

		if s.Value < 0 {
			t.Fatalf("value went negative at movement %d", i)
		}
	}

	// Drain the rest and check the books close to the tiyin.
	rest, s, err := AVCOIssue(s, s.Qty)
	if err != nil {
		t.Fatal(err)
	}
	issued += rest
	if s.Value != 0 {
		t.Fatalf("after draining everything, %d tiyin were left against zero quantity", s.Value)
	}
	if received != issued {
		t.Fatalf("value in and value out disagree by %d tiyin over 10 000 movements", received-issued)
	}
}

// ------------------------------------------------------------ standard ---

// The plan's §3.5 example: standard 1 000, receive 10 at an actual 1 100.
// Stock enters at standard and the 1 000 difference goes to variances.
func TestStandardReceiptVariance(t *testing.T) {
	layerValue, variance, err := StandardReceiptVariance(big.NewRat(10, 1), som(1_100), som(1_000))
	if err != nil {
		t.Fatal(err)
	}
	if layerValue != som(10_000) {
		t.Fatalf("layer must enter at standard: want 10 000 so'm, got %d tiyin", layerValue)
	}
	if variance != som(1_000) {
		t.Fatalf("variance: want 1 000 so'm debit, got %d tiyin", variance)
	}
}

// Buying below standard produces a credit variance — the sign has to follow
// the direction, not be assumed.
func TestStandardReceiptVarianceCanBeNegative(t *testing.T) {
	_, variance, _ := StandardReceiptVariance(big.NewRat(10, 1), som(900), som(1_000))
	if variance != -som(1_000) {
		t.Fatalf("want a 1 000 so'm credit, got %d tiyin", variance)
	}
}

// layer value + variance must equal what is actually payable, to the tiyin, or
// the receipt's journal entry cannot balance.
func TestStandardVarianceClosesAgainstTheActualCost(t *testing.T) {
	qty := big.NewRat(3, 1)
	actual := int64(333_333) // awkward on purpose
	std := int64(300_000)

	layerValue, variance, _ := StandardReceiptVariance(qty, actual, std)
	want, _ := StandardIssue(qty, actual)
	if layerValue+variance != want {
		t.Fatalf("Dt %d + Dt %d does not close against Kt %d", layerValue, variance, want)
	}
}

func TestStandardIssueUsesTheStandardPrice(t *testing.T) {
	cost, err := StandardIssue(big.NewRat(8, 1), som(1_000))
	if err != nil {
		t.Fatal(err)
	}
	if cost != som(8_000) {
		t.Fatalf("want 8 000 so'm, got %d tiyin", cost)
	}
}

// Changing the standard while stock is held produces Δ = Q × (new − old); the
// plan's example is 2 units × 200 = 400.
func TestStandardRevaluationDelta(t *testing.T) {
	if got := StandardRevaluation(big.NewRat(2, 1), som(1_000), som(1_200)); got != som(400) {
		t.Fatalf("want 400 so'm, got %d tiyin", got)
	}
}

// With nothing on hand there is no revaluation and therefore no document and
// no posting at all.
func TestStandardRevaluationIsZeroOnEmptyStock(t *testing.T) {
	if got := StandardRevaluation(new(big.Rat), som(1_000), som(1_200)); got != 0 {
		t.Fatalf("want no revaluation, got %d tiyin", got)
	}
}

// After a standard change the layers must be rewritten so that Σ
// remaining_value equals Q × the new standard exactly — the §1.3 invariant has
// to hold immediately, not merely on average.
func TestRescaleLayersKeepsTheInvariantExact(t *testing.T) {
	layers := []Layer{
		layer("A", 1, 1, 3, som(3_000)),
		layer("B", 2, 2, 4, som(4_000)),
	}
	// A price that does not divide evenly into either layer.
	newStd := int64(33_333)
	RescaleLayersToStandard(layers, newStd)

	qty := StockQty(layers) // 7
	want, _ := StandardIssue(qty, newStd)
	if got := StockValue(layers); got != want {
		t.Fatalf("Σ remaining_value %d != Q × standard %d", got, want)
	}
}

// A depleted layer must be left holding nothing after a rescale.
func TestRescaleClearsDepletedLayers(t *testing.T) {
	layers := []Layer{
		{ID: "spent", SeqNo: 1, DateOrdinal: 1, RemainingQty: new(big.Rat), RemainingValue: 17},
		layer("open", 2, 2, 5, som(500)),
	}
	RescaleLayersToStandard(layers, som(120))
	for _, l := range layers {
		if l.ID == "spent" && l.RemainingValue != 0 {
			t.Fatalf("a depleted layer must hold no value, got %d", l.RemainingValue)
		}
	}
}

// -------------------------------------------------------------- method ---

// The category overrides the company policy; the product card never chooses
// (§0).
func TestEffectiveCostMethodHierarchy(t *testing.T) {
	if got := EffectiveCostMethod("standard", "fifo"); got != CostMethodStandard {
		t.Fatalf("category must win: got %s", got)
	}
	if got := EffectiveCostMethod("", "fifo"); got != CostMethodFIFO {
		t.Fatalf("an empty category inherits: got %s", got)
	}
	if got := EffectiveCostMethod("", ""); got != CostMethodAVCO {
		t.Fatalf("nothing configured falls back to AVCO: got %s", got)
	}
}

// The existing tenant_settings key has stored this misspelling since
// inventory_settings.go was written. Rejecting it would silently flip live
// tenants onto the fallback.
func TestParseCostMethodAcceptsTheLegacySpelling(t *testing.T) {
	got, err := ParseCostMethod("aveco")
	if err != nil || got != CostMethodAVCO {
		t.Fatalf("want avco, got %v (%v)", got, err)
	}
}

// LIFO is prohibited by BHMS № 4 and IAS 2, so it is an error rather than a
// quiet fallback — an import that tries to set it has to fail visibly.
func TestParseCostMethodRejectsLIFO(t *testing.T) {
	if _, err := ParseCostMethod("LIFO"); err == nil {
		t.Fatal("LIFO must be rejected")
	}
	if got := EffectiveCostMethod("lifo", "fifo"); got != CostMethodFIFO {
		t.Fatalf("an invalid category method must fall through to the company policy, got %s", got)
	}
}

// ----------------------------------------------------------- invariant ---

// §1.3, exercised end to end: whatever sequence of receipts and issues runs,
// Σ remaining_value over the layers equals value in minus value out.
func TestInvariantHoldsAcrossAMixedSequence(t *testing.T) {
	var layers []Layer
	var valueIn, valueOut int64

	receipts := []struct {
		qty, value int64
	}{
		{3, 10_000}, {7, 33_333}, {1, 99_991}, {12, 123_457}, {5, 1},
	}
	for i, r := range receipts {
		layers = append(layers, layer(
			string(rune('A'+i)), int64(i+1), int64(i+1), r.qty, r.value))
		valueIn += r.value
	}

	for _, q := range []int64{2, 9, 1, 6, 4} {
		res, err := FIFOIssue(layers, big.NewRat(q, 1))
		if err != nil {
			t.Fatalf("issue of %d: %v", q, err)
		}
		valueOut += res.Cost
	}

	if got := StockValue(layers); got != valueIn-valueOut {
		t.Fatalf("invariant broken: Σ remaining %d != in %d - out %d (%d)",
			got, valueIn, valueOut, valueIn-valueOut)
	}
}

// Fractional quantities are the normal case for weight and volume, and the
// invariant must survive them too.
func TestInvariantWithFractionalQuantities(t *testing.T) {
	layers := []Layer{
		{ID: "A", SeqNo: 1, DateOrdinal: 1, RemainingQty: big.NewRat(5, 2), RemainingValue: 10_000},  // 2.5
		{ID: "B", SeqNo: 2, DateOrdinal: 2, RemainingQty: big.NewRat(10, 3), RemainingValue: 20_000}, // 3.333…
	}
	res, err := FIFOIssue(layers, big.NewRat(4, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := StockValue(layers); got != 30_000-res.Cost {
		t.Fatalf("invariant broken: %d != %d", got, 30_000-res.Cost)
	}
}
