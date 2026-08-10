package handler

import (
	"math/big"
	"testing"
)

// The invariant that phases 1-4 could only assert in the abstract: after an
// issue, the layers must hold what the METHOD says the stock is worth — not
// what FIFO happened to leave behind.
//
// This is the bug the service surfaced. Standard and AVCO drain layers FIFO for
// the audit trail, but their stock value is defined elsewhere (Q × standard,
// and avco_value). Without a rescale the layers keep the FIFO figure and the
// §1.3 reconciliation reports a difference on every product using either
// method — a report crying wolf on its own arithmetic.

func TestStandardIssueLeavesLayersAtQtyTimesStandard(t *testing.T) {
	std := som(1_200)
	// Two layers bought at DIFFERENT prices, which is the case that exposes it:
	// FIFO would leave 1 200 behind, standard says 2 units × 1 200 = 2 400.
	layers := []Layer{
		layer("A", 1, 1, 2, som(2_000)), // 1 000 each
		layer("B", 2, 2, 2, som(2_800)), // 1 400 each
	}
	if _, err := FIFOIssue(layers, big.NewRat(2, 1)); err != nil {
		t.Fatal(err)
	}
	RescaleLayersToStandard(layers, std)

	qty := StockQty(layers)
	if qty.Cmp(big.NewRat(2, 1)) != 0 {
		t.Fatalf("2 units must remain, got %s", qty.RatString())
	}
	want := mulTiyin(std, qty)
	if got := StockValue(layers); got != want {
		t.Fatalf("standard stock value must be Q × standard: want %d, got %d", want, got)
	}
}

func TestAVCOIssueLeavesLayersAtAvcoValue(t *testing.T) {
	state := AVCOState{Qty: new(big.Rat)}
	state, _ = AVCOReceipt(state, big.NewRat(2, 1), som(2_000))
	state, _ = AVCOReceipt(state, big.NewRat(2, 1), som(2_800))

	layers := []Layer{
		layer("A", 1, 1, 2, som(2_000)),
		layer("B", 2, 2, 2, som(2_800)),
	}

	_, next, err := AVCOIssue(state, big.NewRat(3, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FIFOIssue(layers, big.NewRat(3, 1)); err != nil {
		t.Fatal(err)
	}
	RescaleLayersToTotal(layers, next.Value)

	if got := StockValue(layers); got != next.Value {
		t.Fatalf("Σ layer value must equal avco_value: want %d, got %d", next.Value, got)
	}
}

// The distribution must land on the target exactly, including when the split
// does not divide evenly. The last open layer absorbs the remainder.
func TestRescaleToTotalHitsTheTargetExactly(t *testing.T) {
	for _, total := range []int64{1, 7, 100_001, 333_333, 999_999_999} {
		layers := []Layer{
			{ID: "A", SeqNo: 1, DateOrdinal: 1, RemainingQty: big.NewRat(1, 3)},
			{ID: "B", SeqNo: 2, DateOrdinal: 2, RemainingQty: big.NewRat(1, 3)},
			{ID: "C", SeqNo: 3, DateOrdinal: 3, RemainingQty: big.NewRat(1, 3)},
		}
		RescaleLayersToTotal(layers, total)
		if got := StockValue(layers); got != total {
			t.Fatalf("total %d: Σ came out %d", total, got)
		}
	}
}

// A depleted layer must be left holding nothing, and a set with no open layers
// must not panic reaching for a last index that is not there.
func TestRescaleToTotalIgnoresDepletedLayers(t *testing.T) {
	layers := []Layer{
		{ID: "spent", SeqNo: 1, DateOrdinal: 1, RemainingQty: new(big.Rat), RemainingValue: 42},
		layer("open", 2, 2, 4, som(400)),
	}
	RescaleLayersToTotal(layers, som(1_000))
	for _, l := range layers {
		if l.ID == "spent" && l.RemainingValue != 0 {
			t.Fatalf("a depleted layer must hold nothing, got %d", l.RemainingValue)
		}
	}
	if got := StockValue(layers); got != som(1_000) {
		t.Fatalf("want 1 000 so'm on the open layer, got %d", got)
	}

	empty := []Layer{{ID: "x", RemainingQty: new(big.Rat), RemainingValue: 5}}
	RescaleLayersToTotal(empty, 100) // must not panic
}

// Standard costing with everything gone must leave nothing behind, or the
// product carries value against zero quantity — the state
// product_avco_state's CHECK exists to make impossible.
func TestRescaleToStandardOnEmptyStockClearsEverything(t *testing.T) {
	layers := []Layer{{ID: "A", SeqNo: 1, DateOrdinal: 1, RemainingQty: new(big.Rat), RemainingValue: 777}}
	RescaleLayersToStandard(layers, som(1_000))
	if got := StockValue(layers); got != 0 {
		t.Fatalf("nothing on hand must be worth nothing, got %d", got)
	}
}

// A receipt is a receipt; an issue is an issue. Getting this backwards would
// drain layers on a purchase.
func TestReceiptOperationsAreClassifiedCorrectly(t *testing.T) {
	receipts := []StockOperation{OpSupplierReceipt, OpCustomerReturn, OpCountSurplus}
	issues := []StockOperation{OpSaleIssue, OpSupplierReturn, OpScrap, OpCountShortage}
	for _, op := range receipts {
		if !isReceiptOperation(op) {
			t.Fatalf("%s brings stock in", op)
		}
	}
	for _, op := range issues {
		if isReceiptOperation(op) {
			t.Fatalf("%s takes stock out", op)
		}
	}
}

// The per-unit cost stored on a layer is for display only. It must never be
// multiplied back to recover the value — 10 000,00 over 3 units is 3 333,33
// each, and 3 × 3 333,33 is 9 999,99.
func TestLayerUnitCostIsDerivedNotAuthoritative(t *testing.T) {
	value := som(10_000)
	qty := big.NewRat(3, 1)
	unit := divTiyin(value, qty)
	if unit != 333333 {
		t.Fatalf("want 333333 tiyin per unit, got %d", unit)
	}
	if back := mulTiyin(unit, qty); back == value {
		t.Fatal("this test exists because multiplying the unit cost back does NOT recover the value")
	}
}
