package handler

import (
	"math"
	"testing"
)

func almost(a, b float64) bool { return math.Abs(a-b) < 0.0001 }

// P4: the inclusive/exclusive split must reproduce the web client's math
// (SalesOrders.jsx:481-490) — the two were disagreeing on inclusive tenants.
func TestLineTaxFor(t *testing.T) {
	cases := []struct {
		name          string
		net           float64
		info          taxRateInfo
		wantBase      float64
		wantTax       float64
	}{
		{"exclusive 12%", 100000, taxRateInfo{Rate: 12}, 100000, 12000},
		{"inclusive 12%", 112000, taxRateInfo{Rate: 12, PriceInclude: true}, 100000, 12000},
		{"inclusive 15% on 115", 115, taxRateInfo{Rate: 15, PriceInclude: true}, 100, 15},
		{"zero rate", 500, taxRateInfo{Rate: 0}, 500, 0},
		{"zero rate inclusive", 500, taxRateInfo{Rate: 0, PriceInclude: true}, 500, 0},
		{"zero net", 0, taxRateInfo{Rate: 12}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, tax := lineTaxFor(tc.net, tc.info)
			if !almost(base, tc.wantBase) || !almost(tax, tc.wantTax) {
				t.Fatalf("got base=%.4f tax=%.4f, want base=%.4f tax=%.4f", base, tax, tc.wantBase, tc.wantTax)
			}
			// Invariant: base + tax == what the customer pays for the line
			// under inclusive, and base == net under exclusive.
			if tc.info.PriceInclude {
				if !almost(base+tax, tc.net) {
					t.Fatalf("inclusive: base+tax=%.4f must equal net=%.4f", base+tax, tc.net)
				}
			} else if !almost(base, tc.net) {
				t.Fatalf("exclusive: base=%.4f must equal net=%.4f", base, tc.net)
			}
		})
	}
}

// P5: the user-facing message must name the offending id.
func TestTaxRateNotFoundMessage(t *testing.T) {
	r := &taxRateResolver{cache: map[string]taxRateInfo{}}
	_, err := r.resolve("not-a-uuid")
	if err == nil {
		t.Fatal("expected error for unparsable id")
	}
	msg := taxRateNotFoundMessage(err)
	if msg != "Soliq stavkasi topilmadi: not-a-uuid" {
		t.Fatalf("unexpected message: %q", msg)
	}
}
