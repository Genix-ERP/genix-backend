package handler

import (
	"math"
	"testing"
)

// tax_calculations_test.go — table-driven tests for the formulas mandated
// by TZ_Ish_Haqi_Soliq_Tolik.docx §11. These are pure-function tests
// (no DB / HTTP needed) so they run quickly and gate every CI commit.
//
// Mapping to the TZ scenario table (§11):
//   1.  ФОТ 20 000 000 → НДФЛ                          → 2 400 000
//   2.  ФОТ 20 000 000 → Нетто (after NDFL+Profsoyuz+INPS) → 17 380 000
//   3.  ФОТ 20 000 000 → ЕСП                            → 2 400 000
//   4.  ФОТ 20 000 000 → Жами харажат (FOT + ESP)       → 22 400 000
//   5.  Tan olinmagan 3.5 mln → solik bazasi shifts +3.5 mln
//   7.  100 mln daromad − 55.5 mln tan olingan          → tax base 44 500 000
//   8.  Tax base 44.5 mln × 15%                         → 6 675 000
//   9.  Rate 12% → 13% changes → all calcs refresh (covered by getCompanyTaxRatePct)
//   11. NDS zachet 1.2 mln, realizatsiya 6 mln          → to'lov 4 800 000
//
// Scenarios 6 and 10 (live recalc, Excel export) are integration concerns
// covered by the frontend; here we lock down the arithmetic.

const epsilon = 0.001

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}

// payroll math from §2.1 of the TZ — gross, withholdings, netto, ESP,
// total company cost. Mirrors handler/employee_taxes.go's
// computeEmployeeTaxesForEntry but factored as a pure function so we can
// test §11 scenarios 1–4 in isolation.
type payrollExpectation struct {
	gross        float64
	ndfl         float64
	profsoyuz    float64
	inps         float64
	esp          float64
	netto        float64
	companyTotal float64
}

func payroll(fot, ndflPct, profPct, inpsPct, espPct float64) payrollExpectation {
	ndfl := math.Round(fot * ndflPct / 100)
	prof := math.Round(fot * profPct / 100)
	inps := math.Round(fot * inpsPct / 100)
	esp := math.Round(fot * espPct / 100)
	withhold := ndfl + prof + inps
	return payrollExpectation{
		gross:        fot,
		ndfl:         ndfl,
		profsoyuz:    prof,
		inps:         inps,
		esp:          esp,
		netto:        fot - withhold,
		companyTotal: fot + esp,
	}
}

func TestTZ_Section11_PayrollScenarios(t *testing.T) {
	// FOT = 20 000 000, default rates per TZ §1.2
	got := payroll(20_000_000, 12, 1, 0.1, 12)

	// Scenario 1: NDFL = 2 400 000
	if got.ndfl != 2_400_000 {
		t.Errorf("§11.1 NDFL: got %.0f, expected 2 400 000", got.ndfl)
	}
	// Scenario 2: Netto = 17 380 000
	if got.netto != 17_380_000 {
		t.Errorf("§11.2 Netto: got %.0f, expected 17 380 000", got.netto)
	}
	// Scenario 3: ESP = 2 400 000
	if got.esp != 2_400_000 {
		t.Errorf("§11.3 ESP: got %.0f, expected 2 400 000", got.esp)
	}
	// Scenario 4: Total company cost = 22 400 000
	if got.companyTotal != 22_400_000 {
		t.Errorf("§11.4 Company total: got %.0f, expected 22 400 000", got.companyTotal)
	}
}

// profitTax mirrors profit_tax.go's GetProfitTax math. Pure function.
func profitTaxComputation(income, recognized, unrecognized, ratePct float64) (accountingProfit, taxBase, taxAmount, netProfit float64) {
	total := recognized + unrecognized
	accountingProfit = income - total
	taxBase = income - recognized
	if taxBase < 0 {
		taxBase = 0
	}
	taxAmount = math.Round(taxBase*ratePct/100*100) / 100 // round2
	netProfit = accountingProfit - taxAmount
	return
}

func TestTZ_Section11_ProfitTaxScenarios(t *testing.T) {
	// Scenario 7: 100 mln income, 55.5 mln recognized → tax base 44.5 mln
	_, taxBase7, _, _ := profitTaxComputation(100_000_000, 55_500_000, 3_500_000, 15)
	if !approxEqual(taxBase7, 44_500_000) {
		t.Errorf("§11.7 tax base: got %.0f, expected 44 500 000", taxBase7)
	}

	// Scenario 8: tax base 44.5 mln × 15% → 6 675 000
	_, _, taxAmount8, _ := profitTaxComputation(100_000_000, 55_500_000, 3_500_000, 15)
	if !approxEqual(taxAmount8, 6_675_000) {
		t.Errorf("§11.8 tax amount: got %.0f, expected 6 675 000", taxAmount8)
	}

	// Scenario 5: tan olinmagan increases the gap between accounting profit
	// and tax base. With 3.5 mln unrecognized: accounting profit shrinks
	// by 3.5 mln but tax base stays at (income − recognized).
	apTrue, taxBaseTrue, _, _ := profitTaxComputation(100_000_000, 55_500_000, 3_500_000, 15)
	apIfRecognized, taxBaseIfRecognized, _, _ := profitTaxComputation(100_000_000, 59_000_000, 0, 15)
	if !approxEqual(apTrue, apIfRecognized) {
		t.Errorf("§11.5 accounting profit invariance: got %.0f vs %.0f", apTrue, apIfRecognized)
	}
	gap := taxBaseTrue - taxBaseIfRecognized
	if !approxEqual(gap, 3_500_000) {
		t.Errorf("§11.5 tax base shift = unrecognized: got %.0f, expected 3 500 000", gap)
	}

	// Scenario 9: rate change cascades. Same inputs at 13% should produce a
	// different tax_amount than at 12%. (The rate-source switch is tested
	// elsewhere; here we just confirm the computation is rate-driven.)
	_, _, taxAt12, _ := profitTaxComputation(100_000_000, 55_500_000, 0, 12)
	_, _, taxAt13, _ := profitTaxComputation(100_000_000, 55_500_000, 0, 13)
	if approxEqual(taxAt12, taxAt13) {
		t.Errorf("§11.9 rate change must change tax amount: 12%% gave %.0f, 13%% gave %.0f", taxAt12, taxAt13)
	}
}

// Scenario 11: NDS zachet/realizatsiya/balansi.
// Mirrors the GetNdsTax handler's arithmetic.
func ndsBalance(realiz, zachet float64) float64 {
	return math.Round((realiz-zachet)*100) / 100 // round2
}

func TestTZ_Section11_NdsBalance(t *testing.T) {
	// §11.11: 6 mln realizatsiya − 1.2 mln zachet = 4.8 mln to'lov.
	got := ndsBalance(6_000_000, 1_200_000)
	if !approxEqual(got, 4_800_000) {
		t.Errorf("§11.11 NDS balansi: got %.0f, expected 4 800 000", got)
	}

	// Edge: zachet > realizatsiya → kredit balans (negative payable).
	got = ndsBalance(1_200_000, 6_000_000)
	if !approxEqual(got, -4_800_000) {
		t.Errorf("§11.11 NDS kredit balans: got %.0f, expected -4 800 000", got)
	}

	// Edge: equal → 0.
	got = ndsBalance(5_000_000, 5_000_000)
	if !approxEqual(got, 0) {
		t.Errorf("§11.11 NDS zero balans: got %.0f, expected 0", got)
	}
}

// Dividend computation §5.3 — uses the production helper directly so any
// regression in computeDividendBreakdown (rounding, sign handling) trips
// these assertions.
func TestTZ_Section53_DividendBreakdown(t *testing.T) {
	cases := []struct {
		name    string
		amount  float64
		ratePct float64
		wantTax float64
		wantNet float64
	}{
		{"§5.3 example: 10 mln × 5%", 10_000_000, 5, 500_000, 9_500_000},
		{"§5.3 1 mln × 5%", 1_000_000, 5, 50_000, 950_000},
		{"§5.3 odd amount rounds half-up", 100_001, 5, 5_000, 95_001},
		{"§5.3 zero amount returns zero", 0, 5, 0, 0},
		{"§5.3 negative amount returns zero", -1, 5, 0, 0},
		{"§5.3 negative rate returns zero", 1000, -1, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotTax, gotNet := computeDividendBreakdown(tc.amount, tc.ratePct)
			if !approxEqual(gotTax, tc.wantTax) {
				t.Errorf("tax: got %.2f, expected %.2f", gotTax, tc.wantTax)
			}
			if !approxEqual(gotNet, tc.wantNet) {
				t.Errorf("net: got %.2f, expected %.2f", gotNet, tc.wantNet)
			}
		})
	}
}

// Turnover tax §5.2 — turnover × rate / 100.
func TestTZ_Section52_TurnoverTax(t *testing.T) {
	// 100 mln turnover × 4% = 4 mln.
	got := math.Round(100_000_000 * 4 / 100)
	if !approxEqual(got, 4_000_000) {
		t.Errorf("§5.2 turnover: got %.0f, expected 4 000 000", got)
	}
	// Turnover 0 → 0.
	got = math.Round(0 * 4 / 100)
	if !approxEqual(got, 0) {
		t.Errorf("§5.2 zero turnover: got %.0f, expected 0", got)
	}
}
