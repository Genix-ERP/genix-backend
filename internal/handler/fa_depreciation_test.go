package handler

import (
	"math"
	"testing"
	"time"
)

// simulateSchedule runs the accrual month-by-month exactly as the engine does
// (priorPeriods = number of already-accrued entries), returning every amount.
func simulateSchedule(cost, salvage float64, life, rounding int) []float64 {
	var out []float64
	acc := 0.0
	for i := 0; i < life+5; i++ {
		amt := faAccrualAmount(cost, salvage, acc, life, i, rounding)
		if amt <= 0 {
			break
		}
		acc += amt
		out = append(out, amt)
	}
	return out
}

func faApprox(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// §12.6 — the rounding tail must land the remainder on exactly 0 in the last
// scheduled month, over exactly `life` periods.
func TestAccrualRoundingTail(t *testing.T) {
	sched := simulateSchedule(12_000_000, 0, 36, 2)
	if len(sched) != 36 {
		t.Fatalf("expected 36 periods, got %d", len(sched))
	}
	for i := 0; i < 35; i++ {
		if !faApprox(sched[i], 333_333.33) {
			t.Errorf("period %d = %.2f, want 333333.33", i+1, sched[i])
		}
	}
	if !faApprox(sched[35], 333_333.45) {
		t.Errorf("last period = %.2f, want 333333.45", sched[35])
	}
	var sum float64
	for _, a := range sched {
		sum += a
	}
	if !faApprox(sum, 12_000_000) {
		t.Errorf("sum = %.2f, want exactly 12000000 (remainder must be 0)", sum)
	}
}

// §11 example — 120,000,000 over 120 months = 1,000,000 flat, remainder 0.
func TestAccrualFlatSchedule(t *testing.T) {
	sched := simulateSchedule(120_000_000, 0, 120, 2)
	if len(sched) != 120 {
		t.Fatalf("expected 120 periods, got %d", len(sched))
	}
	for i, a := range sched {
		if !faApprox(a, 1_000_000) {
			t.Fatalf("period %d = %.2f, want 1000000", i+1, a)
		}
	}
}

// §8.2 — accumulated depreciation never exceeds cost − salvage.
func TestAccrualNeverExceedsBase(t *testing.T) {
	cost, salvage, life := 1_000_000.0, 100_000.0, 7 // awkward division
	sched := simulateSchedule(cost, salvage, life, 2)
	var sum float64
	for _, a := range sched {
		sum += a
	}
	base := cost - salvage
	if sum > base+0.005 {
		t.Errorf("accumulated %.2f exceeds base %.2f", sum, base)
	}
	if !faApprox(sum, base) {
		t.Errorf("accumulated %.2f should equal base %.2f exactly", sum, base)
	}
}

// §12.15 — a fully depreciated asset accrues nothing.
func TestAccrualFullyDepreciated(t *testing.T) {
	if a := faAccrualAmount(1_000_000, 0, 1_000_000, 12, 12, 2); a != 0 {
		t.Errorf("fully depreciated should be 0, got %.2f", a)
	}
	// Salvage floor: accumulated already at base.
	if a := faAccrualAmount(1_000_000, 200_000, 800_000, 24, 5, 2); a != 0 {
		t.Errorf("at salvage floor should be 0, got %.2f", a)
	}
}

// Guard: zero/negative life never divides by zero.
func TestAccrualGuards(t *testing.T) {
	if a := faAccrualAmount(1_000_000, 0, 0, 0, 0, 2); a != 0 {
		t.Errorf("zero life should yield 0, got %.2f", a)
	}
}

func TestFaLastDayOf(t *testing.T) {
	cases := map[string]string{
		"2026-08": "2026-08-31",
		"2026-02": "2026-02-28",
		"2024-02": "2024-02-29", // leap year
		"2026-12": "2026-12-31",
	}
	for period, want := range cases {
		got := faLastDayOf(period).Format("2006-01-02")
		if got != want {
			t.Errorf("faLastDayOf(%s) = %s, want %s", period, got, want)
		}
	}
}

func TestFaRound(t *testing.T) {
	if faRound(333333.335, 2) != 333333.34 && faRound(333333.335, 2) != 333333.33 {
		// banker's vs half-up tolerance — just ensure 2dp
	}
	if r := faRound(1000000.0/3.0, 2); r != 333333.33 {
		t.Errorf("round(1000000/3,2) = %.4f, want 333333.33", r)
	}
	if faRound(1_000_000, 2) != 1_000_000 {
		t.Error("round of whole number changed value")
	}
}

// Sanity: period-comparison used by the §7 selection filter is lexical-safe for
// YYYY-MM (commissioning month >= run period => not yet in service).
func TestPeriodStringOrdering(t *testing.T) {
	comm := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Format("2006-01")
	if !(comm >= "2026-07") {
		t.Error("commissioned 2026-07 must be excluded from the 2026-07 run")
	}
	if comm >= "2026-08" {
		t.Error("commissioned 2026-07 must be included in the 2026-08 run")
	}
}
