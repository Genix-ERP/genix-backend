package handler

import (
	"math/big"
	"testing"
)

// The database boundary is where tiyin arithmetic meets float64, because that
// is what the driver hands back for NUMERIC. These conversions are the only
// place a rounding error can enter the engine, so they are worth pinning.

func TestToTiyinRoundsRatherThanTruncates(t *testing.T) {
	cases := []struct {
		in   float64
		want int64
	}{
		{0, 0},
		{1, 100},
		{1234.56, 123456},
		// A NUMERIC(20,2) of 1234.56 can come back as 1234.5599999999999 —
		// truncation would lose a tiyin on a value that is exact in the column.
		{1234.5599999999999, 123456},
		{1234.5649999, 123456},
		{0.005, 1},
		{-1234.56, -123456},
		{-0.005, -1},
	}
	for _, tc := range cases {
		if got := toTiyin(tc.in); got != tc.want {
			t.Fatalf("toTiyin(%v): want %d, got %d", tc.in, tc.want, got)
		}
	}
}

func TestTiyinRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, -1, 99, 123456, -123456, 100000000} {
		if got := toTiyin(fromTiyin(v)); got != v {
			t.Fatalf("round trip of %d gave %d", v, got)
		}
	}
}

// A NUMERIC(20,4) quantity must become an exact rational at the column's own
// precision. Going through the nearest float64 would make 0.1 something that
// is not one tenth, and the FIFO proportion take × RV / r would then round
// differently than the same arithmetic on the stored value.
func TestFloatToRatIsExactAtColumnPrecision(t *testing.T) {
	if got := floatToRat(0.1); got.Cmp(big.NewRat(1, 10)) != 0 {
		t.Fatalf("0.1 must be exactly 1/10, got %s", got.RatString())
	}
	if got := floatToRat(2.5); got.Cmp(big.NewRat(5, 2)) != 0 {
		t.Fatalf("2.5 must be exactly 5/2, got %s", got.RatString())
	}
	if got := floatToRat(0.3333); got.Cmp(big.NewRat(3333, 10000)) != 0 {
		t.Fatalf("0.3333 must be exactly 3333/10000, got %s", got.RatString())
	}
	if got := floatToRat(-1.5); got.Cmp(big.NewRat(-3, 2)) != 0 {
		t.Fatalf("-1.5 must be exactly -3/2, got %s", got.RatString())
	}
}

func TestMulTiyinRoundsOnce(t *testing.T) {
	// 3 units at 3 333,33 is 9 999,99 — not 10 000,00. The revaluation and the
	// stock-value display both go through this, so a per-unit rounding here
	// would put them a tiyin apart from the layers.
	if got := mulTiyin(333333, big.NewRat(3, 1)); got != 999999 {
		t.Fatalf("want 999999, got %d", got)
	}
	// A third of a unit at 1,00.
	if got := mulTiyin(100, big.NewRat(1, 3)); got != 33 {
		t.Fatalf("want 33, got %d", got)
	}
	if got := mulTiyin(100, nil); got != 0 {
		t.Fatalf("a nil quantity is zero, got %d", got)
	}
}

// The whole §3.3 procedure, on the plan's own example: standard 1 000 -> 1 200
// with 2 units on hand gives a 400 revaluation, and afterwards the layers must
// sum to exactly Q × the new standard.
func TestRevaluationAndRescaleAgreeWithEachOther(t *testing.T) {
	qty := big.NewRat(2, 1)
	delta := StandardRevaluation(qty, som(1_000), som(1_200))
	if delta != som(400) {
		t.Fatalf("want a 400 so'm revaluation, got %d tiyin", delta)
	}

	layers := []Layer{layer("A", 1, 1, 2, som(2_000))}
	before := StockValue(layers)
	RescaleLayersToStandard(layers, som(1_200))
	after := StockValue(layers)

	// The posting and the layers must move by the same amount, or the ledger
	// and the warehouse part company at exactly the moment the revaluation
	// claims to keep them together.
	if after-before != delta {
		t.Fatalf("layers moved by %d but the posting was %d", after-before, delta)
	}
	if want := mulTiyin(som(1_200), qty); after != want {
		t.Fatalf("after rescale Σ remaining_value must be Q × standard: want %d, got %d", want, after)
	}
}

// A price change with nothing on hand posts nothing. The plan calls this out,
// and it matters: a business setting its standards before its first receipt
// should not be told it moved money.
func TestRevaluationOnEmptyStockPostsNothing(t *testing.T) {
	if got := StandardRevaluation(new(big.Rat), som(1_000), som(5_000)); got != 0 {
		t.Fatalf("want no posting, got %d tiyin", got)
	}
}

// A price CUT is a credit to stock, not a debit. BuildStockLines handles the
// direction by reversing the sides on a negative amount, so this checks the
// two halves agree.
func TestRevaluationDownwardCreditsStock(t *testing.T) {
	delta := StandardRevaluation(big.NewRat(2, 1), som(1_200), som(1_000))
	if delta != -som(400) {
		t.Fatalf("want -400 so'm, got %d tiyin", delta)
	}
	lines, err := BuildStockLines(OpStandardRevaluation, ValuationAccounts{
		Stock: "2910", Variance: "CHET",
	}, delta, "cut")
	if err != nil {
		t.Fatal(err)
	}
	if err := assertBalanced(lines); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.AccountID == "2910" && l.Credit != som(400) {
			t.Fatalf("a price cut must CREDIT stock: got Dt %d / Kt %d", l.Debit, l.Credit)
		}
	}
}
