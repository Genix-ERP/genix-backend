package handler

import "testing"

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.2.0", "1.4.2", -1},
		{"1.4.2", "1.2.0", 1},
		{"1.10.0", "1.9.0", 1},     // numeric, not lexical
		{"2.0.0", "1.99.99", 1},    // major dominates
		{"1.4", "1.4.0", 0},        // missing patch == 0
		{"v1.4.2", "1.4.2", 0},     // leading v ignored
		{"1.4.2-beta", "1.4.2", 0}, // pre-release suffix ignored
		{"1.4.2+build9", "1.4.2", 0},
		{"", "1.0.0", -1}, // empty == 0.0.0
		{"1.0.1", "1.0.0", 1},
	}
	for _, tc := range cases {
		if got := compareSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("compareSemver(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// Gate semantics: below min => required; below latest but >= min => optional.
func TestVersionGateSemantics(t *testing.T) {
	const latest, min = "1.5.0", "1.3.0"
	type r struct{ available, required bool }
	cases := map[string]r{
		"1.2.0": {true, true},   // below min -> required
		"1.3.0": {true, false},  // == min -> optional only
		"1.4.0": {true, false},  // between -> optional
		"1.5.0": {false, false}, // current -> nothing
		"1.6.0": {false, false}, // ahead (beta) -> nothing
	}
	for cur, want := range cases {
		available := compareSemver(cur, latest) < 0
		required := compareSemver(cur, min) < 0
		if available != want.available || required != want.required {
			t.Errorf("cur=%s got(avail=%v,req=%v) want(avail=%v,req=%v)",
				cur, available, required, want.available, want.required)
		}
	}
}
