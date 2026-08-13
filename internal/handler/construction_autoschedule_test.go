package handler

import (
	"testing"
	"time"
)

// Du–Sh ish kunlari (yakshanba dam), bayramsiz.
func testCal() schedCalendar {
	return schedCalendar{mask: 63, holidays: map[string]bool{}}
}
func d(s string) time.Time {
	t, _ := time.Parse(schedDateLayout, s)
	return t
}

// TZ §8 "Davomiylik": 0,598 × «100 m³», norma 180 kishi-soat/100 m³,
// brigada 5 × 8 soat → 107,64 / 40 = 2,69 → 3 kun.
func TestDurationFromLabourNorm(t *testing.T) {
	p := schedParams{CrewSize: 5, HoursPerShift: 8, Shifts: 1}
	w := &autoWork{Quantity: 0.598, ManHours: 0.598 * 180} // smeta mehnat sublinesi
	got, src, _ := computeDuration(w, p, nil)
	if got != 3 {
		t.Errorf("duration = %d kun, want 3", got)
	}
	if src != "norm" {
		t.Errorf("source = %q, want norm", src)
	}
}

// Normasiz pozitsiya → 1 kun + norm_missing.
func TestDurationDefaultWhenNoNorm(t *testing.T) {
	p := schedParams{CrewSize: 4, HoursPerShift: 8, Shifts: 1}
	got, src, _ := computeDuration(&autoWork{Quantity: 10}, p, nil)
	if got != 1 || src != "default" {
		t.Errorf("got (%d,%q), want (1,default)", got, src)
	}
}

// Unumdorlik spravochnigi — kaskadning 2-bosqichi.
func TestDurationFromProductivityNorm(t *testing.T) {
	p := schedParams{CrewSize: 2, HoursPerShift: 8, Shifts: 1}
	norms := []productivityNorm{{name: "beton", uom: "m3", manHours: 4}}
	w := &autoWork{Name: "Ustroystvo BETON qoplamasi", Uom: "m3", Quantity: 10}
	got, src, _ := computeDuration(w, p, norms) // 40 kishi-soat / 16 = 2.5 → 3
	if got != 3 || src != "productivity" {
		t.Errorf("got (%d,%q), want (3,productivity)", got, src)
	}
}

// Yaxlitlash doim yuqoriga, minimum 1 kun.
func TestDurationRoundsUpMinimumOne(t *testing.T) {
	p := schedParams{CrewSize: 10, HoursPerShift: 8, Shifts: 1}
	if got, _, _ := computeDuration(&autoWork{ManHours: 1}, p, nil); got != 1 {
		t.Errorf("kichik hajm → %d, want 1", got)
	}
	if got, _, _ := computeDuration(&autoWork{ManHours: 81}, p, nil); got != 2 {
		t.Errorf("81/80 → %d, want 2 (yuqoriga yaxlitlash)", got)
	}
}

// Kalendar: dam olish kunlarini tashlab o'tadi.
func TestCalendarSkipsWeekend(t *testing.T) {
	cal := testCal()
	// 2026-08-15 shanba (ish kuni), 16 yakshanba (dam).
	if !cal.isWorkday(d("2026-08-15")) {
		t.Error("shanba ish kuni bo'lishi kerak (mask 63)")
	}
	if cal.isWorkday(d("2026-08-16")) {
		t.Error("yakshanba dam bo'lishi kerak")
	}
	if got := cal.nextWorkday(d("2026-08-16")).Format(schedDateLayout); got != "2026-08-17" {
		t.Errorf("nextWorkday(yakshanba) = %s, want 2026-08-17", got)
	}
	// 3 kunlik ish 15-shanbadan: 15, 17, 18 (16 tashlanadi).
	if got := cal.addWorkdays(d("2026-08-15"), 2).Format(schedDateLayout); got != "2026-08-18" {
		t.Errorf("addWorkdays = %s, want 2026-08-18", got)
	}
}

func TestCalendarHolidayAndSubWorkdays(t *testing.T) {
	cal := testCal()
	cal.holidays["2026-08-17"] = true // dushanba bayram
	if got := cal.nextWorkday(d("2026-08-16")).Format(schedDateLayout); got != "2026-08-18" {
		t.Errorf("bayram tashlanmadi: %s", got)
	}
	if got := cal.subWorkdays(d("2026-08-18"), 1).Format(schedDateLayout); got != "2026-08-15" {
		t.Errorf("subWorkdays = %s, want 2026-08-15 (16 dam, 17 bayram)", got)
	}
}

// TZ §8 "Rejalashtirish": FS bilan 3 ish → ketma-ket sanalar.
func TestForwardPassSequentialFS(t *testing.T) {
	cal := testCal()
	works := []*autoWork{
		{ID: 1, Duration: 2}, {ID: 2, Duration: 2}, {ID: 3, Duration: 1},
	}
	deps := []autoDep{{Pred: 1, Succ: 2}, {Pred: 2, Succ: 3}}
	order, err := topoSort(works, deps)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	forwardPass(order, deps, cal, d("2026-08-10")) // dushanba
	if got := works[0].newStart.Format(schedDateLayout); got != "2026-08-10" {
		t.Errorf("w1 start %s", got)
	}
	if got := works[0].newEnd.Format(schedDateLayout); got != "2026-08-11" {
		t.Errorf("w1 end %s", got)
	}
	if got := works[1].newStart.Format(schedDateLayout); got != "2026-08-12" {
		t.Errorf("w2 oldingidan keyin boshlanishi kerak, got %s", got)
	}
	if got := works[2].newStart.Format(schedDateLayout); got != "2026-08-14" {
		t.Errorf("w3 start %s", got)
	}
}

// parallel_limit = 2 → ikki yo'lakcha: 1 va 2 bir vaqtda boshlanadi.
func TestParallelLanes(t *testing.T) {
	works := []*autoWork{
		{ID: 1, Duration: 2, Section: "A"}, {ID: 2, Duration: 2, Section: "A"},
		{ID: 3, Duration: 2, Section: "A"}, {ID: 4, Duration: 2, Section: "A"},
	}
	deps := buildAutoDeps(works, 2)
	// 3 → 1 va 4 → 2 bog'lanadi (i-2), 1 va 2 bog'siz.
	if len(deps) != 2 {
		t.Fatalf("auto deps = %d, want 2", len(deps))
	}
	order, err := topoSort(works, deps)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	forwardPass(order, deps, testCal(), d("2026-08-10"))
	if works[0].newStart.Format(schedDateLayout) != works[1].newStart.Format(schedDateLayout) {
		t.Error("ikki yo'lakcha bir kunda boshlanishi kerak")
	}
	if !works[2].newStart.After(*works[0].newEnd) {
		t.Error("3-ish 1-ishdan keyin boshlanishi kerak")
	}
}

// N=1 → qat'iy zanjir.
func TestParallelLimitOneIsStrictChain(t *testing.T) {
	works := []*autoWork{{ID: 1, Section: "A"}, {ID: 2, Section: "A"}, {ID: 3, Section: "A"}}
	if got := len(buildAutoDeps(works, 1)); got != 2 {
		t.Errorf("N=1 zanjirda %d bog'liqlik, want 2", got)
	}
}

// Bo'limlar orasidagi ulanish: k-bo'lim (k−1) dan keyin.
func TestCrossSectionLink(t *testing.T) {
	works := []*autoWork{
		{ID: 1, Duration: 1, Section: "A"},
		{ID: 2, Duration: 1, Section: "B"},
	}
	deps := buildAutoDeps(works, 1)
	found := false
	for _, dep := range deps {
		if dep.Pred == 1 && dep.Succ == 2 {
			found = true
		}
	}
	if !found {
		t.Error("B bo'limi A dan keyin boshlanishi kerak")
	}
}

// Sikl → zanjiri ko'rsatilgan xato.
func TestCycleDetected(t *testing.T) {
	works := []*autoWork{{ID: 1, ItemNumber: "1.1"}, {ID: 2, ItemNumber: "1.2"}}
	deps := []autoDep{{Pred: 1, Succ: 2}, {Pred: 2, Succ: 1}}
	if _, err := topoSort(works, deps); err == nil {
		t.Fatal("sikl aniqlanmadi")
	}
}

// TZ §8 "Daxlsizlik": manual/fixed/boshlangan ish siljimaydi.
func TestImmutableWorksNotMoved(t *testing.T) {
	cal := testCal()
	fixedStart := d("2026-09-01")
	fixedEnd := d("2026-09-02")
	works := []*autoWork{
		{ID: 1, Duration: 2},
		{ID: 2, Duration: 2, Start: &fixedStart, End: &fixedEnd, IsFixed: true},
		{ID: 3, Duration: 1, Start: &fixedStart, End: &fixedEnd, Source: "manual"},
		{ID: 4, Duration: 1, Start: &fixedStart, End: &fixedEnd, Approval: "in_progress"},
	}
	deps := []autoDep{}
	order, _ := topoSort(works, deps)
	forwardPass(order, deps, cal, d("2026-08-10"))
	for _, w := range works[1:] {
		if w.newStart != nil {
			t.Errorf("daxlsiz ish %d siljitildi", w.ID)
		}
	}
	if works[0].newStart == nil {
		t.Error("oddiy ish rejalashtirilishi kerak edi")
	}
}

// Fixed bilan konflikt: fixed siljimaydi, konflikt ro'yxatga tushadi.
func TestFixedConflictReported(t *testing.T) {
	cal := testCal()
	fs := d("2026-08-10")
	fe := d("2026-08-10")
	works := []*autoWork{
		{ID: 1, Duration: 10}, // 10 kun → 2-ishning oldiga kirib boradi
		{ID: 2, Duration: 1, Start: &fs, End: &fe, IsFixed: true, ItemNumber: "2.1"},
	}
	deps := []autoDep{{Pred: 1, Succ: 2}}
	order, _ := topoSort(works, deps)
	conflicts := forwardPass(order, deps, cal, d("2026-08-10"))
	if len(conflicts) != 1 || conflicts[0].Reason != "fixed" {
		t.Fatalf("fixed konflikt kutilgandi, got %+v", conflicts)
	}
	if works[1].newStart != nil {
		t.Error("fixed ish siljitilmasligi kerak")
	}
}

// started()/finished() 353-migratsiya statuslariga mos.
func TestWorkStateHelpers(t *testing.T) {
	cases := []struct {
		approval          string
		done              float64
		started, finished bool
	}{
		{"pending", 0, false, false},
		{"pending", 5, true, false},
		{"in_progress", 0, true, false},
		{"submitted", 0, true, true},
		{"confirmed_engineer", 0, true, true},
	}
	for _, tc := range cases {
		w := &autoWork{Approval: tc.approval, DoneQty: tc.done}
		if w.started() != tc.started || w.finished() != tc.finished {
			t.Errorf("%s/%v: started=%v finished=%v", tc.approval, tc.done, w.started(), w.finished())
		}
	}
}
