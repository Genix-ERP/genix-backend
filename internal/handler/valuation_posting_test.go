package handler

import (
	"errors"
	"math/big"
	"testing"
)

func fullAccounts() ValuationAccounts {
	return ValuationAccounts{
		Stock: "2910", COGS: "9120", Payables: "6010", Expense: "9430",
		Surplus: "9390", Shortage: "5910", Variance: "CHET",
	}
}

func sides(t *testing.T, lines []JournalLine) (dr, cr string) {
	t.Helper()
	for _, l := range lines {
		if l.Debit > 0 {
			dr = l.AccountID
		}
		if l.Credit > 0 {
			cr = l.AccountID
		}
	}
	return dr, cr
}

// Every row of the plan's §4 table, checked for the accounts it hits and — the
// point of the whole exercise — for balancing.
func TestEveryStockOperationBalances(t *testing.T) {
	cases := []struct {
		op     StockOperation
		wantDr string
		wantCr string
	}{
		{OpSupplierReceipt, "2910", "6010"},
		{OpSaleIssue, "9120", "2910"},
		{OpCustomerReturn, "2910", "9120"},
		{OpSupplierReturn, "6010", "2910"},
		{OpScrap, "9430", "2910"},
		{OpCountSurplus, "2910", "9390"},
		{OpCountShortage, "5910", "2910"},
		{OpStandardRevaluation, "2910", "CHET"},
	}
	for _, tc := range cases {
		lines, err := BuildStockLines(tc.op, fullAccounts(), 123_456, "test")
		if err != nil {
			t.Fatalf("%s: %v", tc.op, err)
		}
		if err := assertBalanced(lines); err != nil {
			t.Fatalf("%s: %v", tc.op, err)
		}
		dr, cr := sides(t, lines)
		if dr != tc.wantDr || cr != tc.wantCr {
			t.Fatalf("%s: want Dt %s / Kt %s, got Dt %s / Kt %s", tc.op, tc.wantDr, tc.wantCr, dr, cr)
		}
	}
}

// A negative movement is the same operation reversed, not a different one.
// Storno is how this system cancels anything (plan §2.6), so it has to come out
// of the same mapping rather than a parallel set of "reversal" branches that
// could disagree with it.
func TestNegativeAmountReversesTheSides(t *testing.T) {
	fwd, err := BuildStockLines(OpSaleIssue, fullAccounts(), 5_000, "sale")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := BuildStockLines(OpSaleIssue, fullAccounts(), -5_000, "storno")
	if err != nil {
		t.Fatal(err)
	}
	fdr, fcr := sides(t, fwd)
	rdr, rcr := sides(t, rev)
	if fdr != rcr || fcr != rdr {
		t.Fatalf("storno must mirror the original: %s/%s vs %s/%s", fdr, fcr, rdr, rcr)
	}
	if err := assertBalanced(rev); err != nil {
		t.Fatal(err)
	}
}

// A missing account is an error, never a guess. Posting a sale with no COGS
// account configured has to fail loudly at the point of posting rather than
// produce a one-sided entry — which is exactly what happened before migration
// 416 installed a database trigger to catch it.
func TestMissingAccountIsRefused(t *testing.T) {
	acc := fullAccounts()
	acc.COGS = ""
	if _, err := BuildStockLines(OpSaleIssue, acc, 1_000, "sale"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("want ErrNoAccount, got %v", err)
	}
}

func TestZeroAmountIsRefused(t *testing.T) {
	if _, err := BuildStockLines(OpSaleIssue, fullAccounts(), 0, ""); !errors.Is(err, ErrZeroAmount) {
		t.Fatalf("want ErrZeroAmount, got %v", err)
	}
}

func TestUnknownOperationIsRefused(t *testing.T) {
	if _, err := BuildStockLines("teleport", fullAccounts(), 100, ""); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("want ErrUnknownOp, got %v", err)
	}
}

// ------------------------------------------------- standard-cost receipt ---

// The plan's §3.5 worked example: standard 1 000, receive 10 at an actual
// 1 100. Stock enters at 10 000, variance takes 1 000, payables owe 11 000.
func TestStandardReceiptThreeSidedEntry(t *testing.T) {
	qty := big.NewRat(10, 1)
	layerValue, variance, err := StandardReceiptVariance(qty, som(1_100), som(1_000))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := BuildStandardReceiptLines(fullAccounts(), layerValue, variance, "receipt")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("a receipt above standard has three sides, got %d", len(lines))
	}
	if err := assertBalanced(lines); err != nil {
		t.Fatal(err)
	}
	var payable int64
	for _, l := range lines {
		if l.AccountID == "6010" {
			payable = l.Credit
		}
	}
	if payable != som(11_000) {
		t.Fatalf("payables must be what is actually owed: want 11 000 so'm, got %d tiyin", payable)
	}
}

// Buying BELOW standard puts the variance on the credit side. The sign has to
// follow the direction rather than be assumed, or a favourable purchase would
// be booked as an unfavourable one.
func TestStandardReceiptBelowStandardCreditsVariance(t *testing.T) {
	layerValue, variance, _ := StandardReceiptVariance(big.NewRat(10, 1), som(900), som(1_000))
	lines, err := BuildStandardReceiptLines(fullAccounts(), layerValue, variance, "receipt")
	if err != nil {
		t.Fatal(err)
	}
	if err := assertBalanced(lines); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.AccountID == "CHET" && l.Credit != som(1_000) {
			t.Fatalf("variance must be a 1 000 so'm credit, got Dt %d / Kt %d", l.Debit, l.Credit)
		}
	}
}

// At exactly standard there is no variance and the entry is a plain pair.
func TestStandardReceiptAtStandardHasNoVarianceLine(t *testing.T) {
	layerValue, variance, _ := StandardReceiptVariance(big.NewRat(10, 1), som(1_000), som(1_000))
	if variance != 0 {
		t.Fatalf("want no variance, got %d", variance)
	}
	lines, err := BuildStandardReceiptLines(fullAccounts(), layerValue, variance, "receipt")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("want a two-sided entry, got %d lines", len(lines))
	}
}

// An unconfigured variance account must fail rather than be folded into stock.
// Absorbing it would value the stock at actual cost while the method claims
// standard, and the difference would vanish from every report that exists to
// show it.
func TestStandardReceiptRefusesToHideTheVariance(t *testing.T) {
	acc := fullAccounts()
	acc.Variance = ""
	layerValue, variance, _ := StandardReceiptVariance(big.NewRat(10, 1), som(1_100), som(1_000))
	if _, err := BuildStandardReceiptLines(acc, layerValue, variance, "receipt"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("want ErrNoAccount, got %v", err)
	}
	// ...but at exactly standard it is not needed, so that must still post.
	lv, v, _ := StandardReceiptVariance(big.NewRat(10, 1), som(1_000), som(1_000))
	if _, err := BuildStandardReceiptLines(acc, lv, v, "receipt"); err != nil {
		t.Fatalf("no variance means no variance account is required: %v", err)
	}
}

// The awkward-rounding case, end to end: the posting must close against the
// payable to the tiyin, which is only true because StandardReceiptVariance
// takes the difference of the two ROUNDED sums rather than rounding the
// difference.
func TestStandardReceiptClosesOnAwkwardRounding(t *testing.T) {
	qty := big.NewRat(3, 1)
	layerValue, variance, _ := StandardReceiptVariance(qty, 333_333, 300_000)
	lines, err := BuildStandardReceiptLines(fullAccounts(), layerValue, variance, "receipt")
	if err != nil {
		t.Fatal(err)
	}
	if err := assertBalanced(lines); err != nil {
		t.Fatalf("awkward rounding broke the entry: %v", err)
	}
	want, _ := StandardIssue(qty, 333_333)
	for _, l := range lines {
		if l.AccountID == "6010" && l.Credit != want {
			t.Fatalf("payable must equal the actual cost: want %d, got %d", want, l.Credit)
		}
	}
}

// --------------------------------------------------------- cost -> lines ---

// The number the engine computed is the number that gets posted. If the
// posting re-derived it from quantity × a unit price, the ledger and the layers
// would drift apart — which is the one thing the whole design exists to
// prevent.
func TestPostedAmountIsTheEngineAmount(t *testing.T) {
	layers := []Layer{layer("A", 1, 1, 3, som(10_000))}
	res, err := FIFOIssue(layers, big.NewRat(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := BuildStockLines(OpSaleIssue, fullAccounts(), res.Cost, "sale")
	if err != nil {
		t.Fatal(err)
	}
	var posted int64
	for _, l := range lines {
		posted += l.Debit
	}
	if posted != res.Cost {
		t.Fatalf("posted %d but the engine costed %d", posted, res.Cost)
	}
	// 10 000,00 over three units is 3 333,33 — not a round number, which is
	// the case where a re-derivation would differ.
	if res.Cost != 333333 {
		t.Fatalf("expected the awkward third, got %d", res.Cost)
	}
}

func TestAccountsNeededIsReportedForEveryOperation(t *testing.T) {
	for _, op := range []StockOperation{
		OpSupplierReceipt, OpSaleIssue, OpCustomerReturn, OpSupplierReturn,
		OpScrap, OpCountSurplus, OpCountShortage, OpStandardRevaluation,
	} {
		if got := StockOperationsNeedingAccounts(op); len(got) != 2 {
			t.Fatalf("%s: want two required accounts, got %v", op, got)
		}
	}
}
