package handler

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// =====================================================
// Ish grafigini avtomatik rejalashtirish (TZ v1) — hisob yadrosi.
//
// Uch ingredient (TZ §0.2): DAVOMIYLIK (hajm × norma / brigada), KETMA-KETLIK
// (smeta tartibi + bog'liqliklar) va KALENDAR (ish kunlari), so'ng CPM to'g'ri
// o'tish (forward pass) sanalarni beradi, teskari o'tish kritik yo'lni.
//
// Daxlsizlik qoidalari (TZ §0.3, §0.4): qo'lda qo'yilgan (schedule_source =
// 'manual'), qotirilgan (is_fixed) va BOSHLANGAN ishlar hech qachon
// siljitilmaydi — ular faqat o'z davomchilariga tayanch bo'ladi. "Boshlangan"
// 353-migratsiyaning approval_status/done_quantity'sidan aniqlanadi, yangi
// status tushunchasi yaratilmaydi.
//
// Butun hisob BACKENDDA (TZ §4): front tayyor sanalarni oladi.
// =====================================================

// ---------------------------------------------------------------------------
// Kalendar
// ---------------------------------------------------------------------------

// schedCalendar — hafta ish kunlari bitmask'i (bit0=Dushanba … bit6=Yakshanba)
// + bayramlar. Barcha sana arifmetikasi shu yerdan o'tadi.
type schedCalendar struct {
	mask     int
	holidays map[string]bool
}

func (c schedCalendar) isWorkday(d time.Time) bool {
	// time.Weekday: Sunday=0 … Saturday=6 → bit indeksga (Dushanba=0) o'tkazamiz.
	bit := (int(d.Weekday()) + 6) % 7
	if c.mask&(1<<bit) == 0 {
		return false
	}
	return !c.holidays[d.Format(schedDateLayout)]
}

// nextWorkday — d ish kuni bo'lsa o'zini, aks holda keyingi ish kunini qaytaradi.
func (c schedCalendar) nextWorkday(d time.Time) time.Time {
	for i := 0; i < 400; i++ { // bayramlar bilan ham 400 kun yetarlicha zaxira
		if c.isWorkday(d) {
			return d
		}
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// addWorkdays — start'dan (o'zi ish kuni deb hisoblanadi) n ta ish kuni oldinga.
// n = 0 → start. Ish tugashi = addWorkdays(start, duration-1).
func (c schedCalendar) addWorkdays(start time.Time, n int) time.Time {
	d := c.nextWorkday(start)
	for i := 0; i < n; i++ {
		d = c.nextWorkday(d.AddDate(0, 0, 1))
	}
	return d
}

// subWorkdays — teskari o'tish uchun: d'dan n ta ish kuni orqaga.
func (c schedCalendar) subWorkdays(d time.Time, n int) time.Time {
	for i := 0; i < n; i++ {
		d = d.AddDate(0, 0, -1)
		for j := 0; j < 400 && !c.isWorkday(d); j++ {
			d = d.AddDate(0, 0, -1)
		}
	}
	return d
}

// ---------------------------------------------------------------------------
// Parametrlar va ma'lumot yuklash
// ---------------------------------------------------------------------------

type schedParams struct {
	StartDate     *time.Time `json:"start_date"`
	ParallelLimit int        `json:"parallel_limit"`
	CrewSize      int        `json:"crew_size"`
	HoursPerShift float64    `json:"hours_per_shift"`
	Shifts        int        `json:"shifts"`
	WorkdaysMask  int        `json:"workdays_mask"`
}

func defaultSchedParams() schedParams {
	return schedParams{ParallelLimit: 2, CrewSize: 4, HoursPerShift: 8, Shifts: 1, WorkdaysMask: 63}
}

// loadSchedParams — loyiha parametrlari (bo'lmasa — standartlar).
func (h *Handler) loadSchedParams(tenantID uuid.UUID, projectID int64) schedParams {
	p := defaultSchedParams()
	var start nullableTime
	err := h.db.QueryRow(`
		SELECT start_date, parallel_limit, crew_size, hours_per_shift, shifts, workdays_mask
		FROM construction_schedule_params WHERE project_id = $1 AND tenant_id = $2`,
		projectID, tenantID).Scan(&start, &p.ParallelLimit, &p.CrewSize, &p.HoursPerShift, &p.Shifts, &p.WorkdaysMask)
	if err == nil && start.valid {
		t := start.time
		p.StartDate = &t
	}
	if p.ParallelLimit < 1 {
		p.ParallelLimit = 1
	}
	if p.CrewSize < 1 {
		p.CrewSize = 1
	}
	if p.HoursPerShift <= 0 {
		p.HoursPerShift = 8
	}
	if p.Shifts < 1 {
		p.Shifts = 1
	}
	if p.WorkdaysMask < 1 || p.WorkdaysMask > 127 {
		p.WorkdaysMask = 63
	}
	return p
}

func (h *Handler) loadCalendar(tenantID uuid.UUID, mask int) schedCalendar {
	cal := schedCalendar{mask: mask, holidays: map[string]bool{}}
	rows, err := h.db.Query(`SELECT holiday_date FROM construction_calendar_holidays WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return cal
	}
	defer rows.Close()
	for rows.Next() {
		var d time.Time
		if rows.Scan(&d) == nil {
			cal.holidays[d.Format(schedDateLayout)] = true
		}
	}
	return cal
}

// autoWork — rejalashtirish uchun ish qatorining ko'rinishi.
type autoWork struct {
	ID         int64
	ItemNumber string
	Name       string
	Section    string
	Uom        string
	// Quantity — workQtyCase: ВОР importi → NORMA langari → jonli hisob.
	// Bu REJA (smetadagi) hajm.
	Quantity float64
	// QuantityFakt — el.quantity, ya'ni UI'dagi FAKT maydoni: obyektda
	// haqiqatda bajariladigan hajm. Resurs sublinelarining JAMI ustuni ham
	// shundan chiqadi (JAMI = ota-ish FAKT × NORMA), shuning uchun davomiylik
	// ham shu bazaga tayanishi kerak — aks holda mehnat sarfi bor ish FAKT
	// bo'yicha, normadan hisoblangani esa REJA bo'yicha o'lchanardi.
	QuantityFakt float64
	DoneQty      float64
	Approval     string
	Start, End   *time.Time
	Source       string // none | auto | manual
	IsFixed      bool
	Duration     int
	DurSource    string // norm | productivity | default | manual
	NormSnap     []byte
	SortOrder    int
	ManHours     float64 // mehnat sublinelaridan (0 → norma topilmadi)
	// Yuklashdagi asl qiymatlar — delta/undo uchun (hisob paytida Source va
	// Duration qayta yoziladi, shuning uchun "oldin" ni alohida saqlaymiz).
	OrigSource   string
	OrigDuration int
	// outOfScope — scope filtri (unplanned/overdue/section) chetlab o'tgan ish.
	// Sanasi o'zgarmaydi, ammo davomchilariga tayanch bo'lib qoladi. Ilgari
	// buni Source = "manual" deb belgilash orqali qilingan edi; endi alohida
	// bayroq, chunki "qo'lda qo'yilgan" va "qamrovdan tashqarida" — ikki xil
	// narsa va release_manual faqat birinchisini bo'shatadi.
	outOfScope bool
	// hisob natijalari
	newStart, newEnd *time.Time
	critical         bool
}

// started — boshlangan ish avtomat tomonidan siljitilmaydi (TZ §4, §5).
func (w *autoWork) started() bool {
	return w.DoneQty > 0 || (w.Approval != "" && w.Approval != "pending")
}

// finished — tekshiruvga yuborilgan/qabul qilingan ish daxlsiz (TZ §5.1).
func (w *autoWork) finished() bool {
	switch w.Approval {
	case "submitted", "confirmed_supervisor", "confirmed_engineer":
		return true
	}
	return false
}

// immutable — rejalashtiruvchi tegmaydigan ish.
//
// releaseManual = true bo'lsa, faqat "qo'lda qo'yilgan sana" himoyasi olib
// tashlanadi. Muzlatilgan (is_fixed), boshlangan va qamrovdan tashqaridagi
// ishlar baribir joyida qoladi — ular boshqa sabablar bilan daxlsiz.
//
// Bu kerak, chunki 495-migratsiya sanasi bor HAR BIR ishni 'manual' deb
// belgilagan (avtoreja paydo bo'lishidan oldingi barcha sanalar). Shu sababli
// eski loyihalarda "Barcha ishlar" qamrovi ham hech narsani siljita olmasdi.
func (w *autoWork) immutable(releaseManual bool) bool {
	if w.outOfScope || w.IsFixed || w.started() {
		return true
	}
	return !releaseManual && w.Source == "manual"
}

// labourResourceFilter — resurs sublinesi mehnat sarfimi?
//
// Lug'at construction_reports.go:855 dagi klassifikator bilan bir xil bo'lishi
// SHART. Avval bu yerda faqat ('employee','labor','labour') turardi, holbuki
// smetalarda resource_type = 'mehnat' (UI'dagi MEHNAT beyji). Natijada
// ЗАТРАТЫ ТРУДА qatorlari topilmay, man_hours = 0 bo'lib chiqardi va deyarli
// har bir ish "normasiz → default 1 kun" yo'liga tushardi. Ya'ni smetada
// normalar to'liq bo'lsa ham, avtoreja ularni ko'rmasdi.
//
// UOM bo'yicha zaxira ham reports bilan bir xil tartibda: resource_type
// tanish qiymatlardan biri bo'lmasa (masalan bo'sh), o'lchov birligida ЧЕЛ
// bo'lsa — bu kishi-soat.
const labourResourceFilter = `
	LOWER(COALESCE(sub.resource_type, '')) IN
	    ('labor', 'labour', 'employee', 'mehnat', 'ish', 'ishchi', 'worker', 'трудовой', 'трудовые')
	OR (
	    LOWER(COALESCE(sub.resource_type, '')) NOT IN
	        ('equipment', 'mashina', 'masina', 'mexanizm', 'mexanizmlar', 'machinery', 'машина',
	         'material', 'materialy', 'mat', 'materiallar', 'материал', 'материалы')
	    AND UPPER(COALESCE(sub.uom, '')) LIKE '%ЧЕЛ%'
	)`

// loadSchedWorks — loyihaning barcha ish qatorlari + mehnat sarfi (kishi-soat).
func (h *Handler) loadSchedWorks(tenantID uuid.UUID, projectID int64) ([]*autoWork, error) {
	rows, err := h.db.Query(`
		SELECT el.id, COALESCE(el.item_number,''), COALESCE(el.name,''),
		       COALESCE(el.parent_item_number,''), COALESCE(el.uom,''),
		       `+workQtyCase+` AS quantity,
		       COALESCE(el.quantity,0) AS quantity_fakt,
		       COALESCE(el.done_quantity,0), COALESCE(el.approval_status,'pending'),
		       el.sched_start, el.sched_end,
		       COALESCE(el.schedule_source,'none'), COALESCE(el.is_fixed,false),
		       COALESCE(el.duration_days,0), COALESCE(el.duration_source,''),
		       COALESCE(el.sort_order,0),
		       COALESCE((
		           SELECT SUM(
		               CASE WHEN LOWER(COALESCE(sub.uom,'')) ~ '(kun|дн|day)'
		                    THEN COALESCE(sub.quantity,0) * 8
		                    ELSE COALESCE(sub.quantity,0) END)
		           FROM construction_estimate_line sub
		           WHERE sub.parent_line_id = el.id
		             AND sub.tenant_id = el.tenant_id
		             AND (`+labourResourceFilter+`)
		       ), 0) AS man_hours
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
		WHERE el.tenant_id = $1 AND e.project_id = $2 AND `+workRowFilter+`
		ORDER BY el.sort_order ASC, el.id ASC`, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	works := []*autoWork{}
	for rows.Next() {
		w := &autoWork{}
		var st, en nullableTime
		if err := rows.Scan(&w.ID, &w.ItemNumber, &w.Name, &w.Section, &w.Uom, &w.Quantity,
			&w.QuantityFakt,
			&w.DoneQty, &w.Approval, &st, &en, &w.Source, &w.IsFixed,
			&w.Duration, &w.DurSource, &w.SortOrder, &w.ManHours); err != nil {
			continue
		}
		if st.valid {
			t := st.time
			w.Start = &t
		}
		if en.valid {
			t := en.time
			w.End = &t
		}
		w.OrigSource, w.OrigDuration = w.Source, w.Duration
		works = append(works, w)
	}
	return works, rows.Err()
}

// ---------------------------------------------------------------------------
// Davomiylik (TZ §2)
// ---------------------------------------------------------------------------

type productivityNorm struct {
	code, name, uom string
	manHours        float64
}

func (h *Handler) loadProductivityNorms(tenantID uuid.UUID) []productivityNorm {
	out := []productivityNorm{}
	rows, err := h.db.Query(`
		SELECT COALESCE(match_code,''), COALESCE(match_name,''), COALESCE(uom,''), man_hours_per_unit
		FROM construction_productivity_norms WHERE tenant_id = $1 AND is_active = true`, tenantID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n productivityNorm
		if rows.Scan(&n.code, &n.name, &n.uom, &n.manHours) == nil {
			out = append(out, n)
		}
	}
	return out
}

// computeDuration — normalar kaskadi (TZ §2). Birinchi topilgani g'olib.
// Qaytadi: kunlar, manba, snapshot.
func computeDuration(w *autoWork, p schedParams, norms []productivityNorm) (int, string, []byte) {
	capacity := float64(p.CrewSize) * p.HoursPerShift * float64(p.Shifts) // kishi-soat/kun
	if capacity <= 0 {
		capacity = 8
	}

	// 1. Smeta rastsenkasining mehnat sarfi (asosiy yo'l).
	if w.ManHours > 0 {
		days := int(math.Ceil(w.ManHours / capacity))
		if days < 1 {
			days = 1
		}
		snap, _ := json.Marshal(map[string]interface{}{
			"source": "norm", "man_hours": w.ManHours, "capacity_per_day": capacity,
			"crew_size": p.CrewSize, "hours_per_shift": p.HoursPerShift, "shifts": p.Shifts,
			"formula": fmt.Sprintf("%.2f kishi-soat / (%d × %.0f × %d) = %d kun",
				w.ManHours, p.CrewSize, p.HoursPerShift, p.Shifts, days),
		})
		return days, "norm", snap
	}

	// 2. Unumdorlik spravochnigi (rastsenkasiz pozitsiyalar).
	//
	// Hajm FAKT dan olinadi — 1-yo'l bilan bir xil baza. JAMI ustuni
	// (ota-ish FAKT × NORMA) shundan chiqadi, shuning uchun normadan
	// hisoblangan ish ham REJA emas, FAKT hajmi bo'yicha o'lchanishi kerak.
	// FAKT nol bo'lsa (hali kiritilmagan) — reja hajmiga qaytamiz, aks holda
	// ish umuman davomiylik olmay qolardi.
	qty := w.QuantityFakt
	if qty <= 0 {
		qty = w.Quantity
	}
	if qty > 0 {
		lowName, lowUom := strings.ToLower(w.Name), strings.ToLower(w.Uom)
		for _, n := range norms {
			match := false
			if n.code != "" && strings.Contains(strings.ToLower(w.ItemNumber), strings.ToLower(n.code)) {
				match = true
			}
			if !match && n.name != "" && strings.Contains(lowName, strings.ToLower(n.name)) {
				match = true
			}
			if match && n.uom != "" && !strings.Contains(lowUom, strings.ToLower(n.uom)) {
				match = false // birlik mos kelmasa — bu norma emas
			}
			if match {
				mh := qty * n.manHours
				days := int(math.Ceil(mh / capacity))
				if days < 1 {
					days = 1
				}
				snap, _ := json.Marshal(map[string]interface{}{
					"source": "productivity", "man_hours_per_unit": n.manHours,
					"quantity": qty, "man_hours": mh, "capacity_per_day": capacity,
					"formula": fmt.Sprintf("%.3f × %.2f kishi-soat / (%d × %.0f × %d) = %d kun",
						qty, n.manHours, p.CrewSize, p.HoursPerShift, p.Shifts, days),
				})
				return days, "productivity", snap
			}
		}
	}

	// 3. Default 1 kun + norm_missing (UI'da beyj va "Normasiz" filtri).
	snap, _ := json.Marshal(map[string]interface{}{
		"source": "default", "norm_missing": true,
		"formula": "norma topilmadi → 1 kun",
	})
	return 1, "default", snap
}

// ---------------------------------------------------------------------------
// Bog'liqliklar (TZ §3)
// ---------------------------------------------------------------------------

type autoDep struct {
	Pred, Succ int64
	Lag        int
	Source     string // auto | manual
}

func (h *Handler) loadManualDeps(tenantID uuid.UUID, projectID int64) ([]autoDep, error) {
	rows, err := h.db.Query(`
		SELECT predecessor_line_id, successor_line_id, COALESCE(lag_days,0), COALESCE(dep_source,'manual')
		FROM construction_work_dependencies
		WHERE tenant_id = $1 AND project_id = $2 AND COALESCE(dep_source,'manual') = 'manual'`,
		tenantID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deps := []autoDep{}
	for rows.Next() {
		var d autoDep
		if rows.Scan(&d.Pred, &d.Succ, &d.Lag, &d.Source) == nil {
			deps = append(deps, d)
		}
	}
	return deps, rows.Err()
}

// buildAutoDeps — smeta tartibidan FS zanjirini quradi (TZ §3):
//  1. bo'lim ichida i-pozitsiya (i−N)-pozitsiyaga bog'lanadi → N ta parallel
//     "yo'lakcha" (brigada); N=1 → qat'iy zanjir.
//  2. k-bo'limning birinchi N pozitsiyasi (k−1)-bo'limning oxirgilariga.
//
// Bo'limlar tartibi = smeta tartibi (u texnologik).
func buildAutoDeps(works []*autoWork, parallel int) []autoDep {
	if parallel < 1 {
		parallel = 1
	}
	// Bo'limlarni ko'rinish tartibida yig'amiz.
	sectionOrder := []string{}
	bySection := map[string][]*autoWork{}
	for _, w := range works {
		if _, ok := bySection[w.Section]; !ok {
			sectionOrder = append(sectionOrder, w.Section)
		}
		bySection[w.Section] = append(bySection[w.Section], w)
	}

	deps := []autoDep{}
	var prevTail []*autoWork
	for _, sec := range sectionOrder {
		list := bySection[sec]
		// 1) bo'lim ichidagi yo'lakchalar
		for i := parallel; i < len(list); i++ {
			deps = append(deps, autoDep{Pred: list[i-parallel].ID, Succ: list[i].ID, Source: "auto"})
		}
		// 2) bo'limlar orasidagi ulanish
		if len(prevTail) > 0 {
			n := parallel
			if n > len(list) {
				n = len(list)
			}
			for i := 0; i < n; i++ {
				// oldingi bo'limning mos yo'lakchasining oxirgi ishi
				p := prevTail[i%len(prevTail)]
				deps = append(deps, autoDep{Pred: p.ID, Succ: list[i].ID, Source: "auto"})
			}
		}
		// bo'limning "dumi" — oxirgi N ta ish
		tail := list
		if len(list) > parallel {
			tail = list[len(list)-parallel:]
		}
		prevTail = tail
	}
	return deps
}

// topoSort — Kahn algoritmi; sikl topilsa zanjir bilan xato (TZ §1.2).
func topoSort(works []*autoWork, deps []autoDep) ([]*autoWork, error) {
	index := map[int64]*autoWork{}
	for _, w := range works {
		index[w.ID] = w
	}
	indeg := map[int64]int{}
	succ := map[int64][]int64{}
	for _, w := range works {
		indeg[w.ID] = 0
	}
	for _, d := range deps {
		if index[d.Pred] == nil || index[d.Succ] == nil {
			continue // boshqa loyihaga/o'chirilgan qatorga havola
		}
		succ[d.Pred] = append(succ[d.Pred], d.Succ)
		indeg[d.Succ]++
	}

	queue := []int64{}
	for _, w := range works { // barqaror tartib uchun works bo'yicha yuramiz
		if indeg[w.ID] == 0 {
			queue = append(queue, w.ID)
		}
	}
	out := make([]*autoWork, 0, len(works))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, index[id])
		for _, s := range succ[id] {
			indeg[s]--
			if indeg[s] == 0 {
				queue = append(queue, s)
			}
		}
	}
	if len(out) != len(works) {
		// Siklga tushib qolganlarni ko'rsatamiz.
		stuck := []string{}
		for _, w := range works {
			if indeg[w.ID] > 0 && len(stuck) < 8 {
				label := w.ItemNumber
				if label == "" {
					label = w.Name
				}
				stuck = append(stuck, label)
			}
		}
		return nil, fmt.Errorf("bog'liqliklarda sikl aniqlandi: %s", strings.Join(stuck, " → "))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Forward / backward pass (TZ §4)
// ---------------------------------------------------------------------------

type schedConflict struct {
	LineID  int64  `json:"line_id"`
	Label   string `json:"label"`
	Reason  string `json:"reason"`
	Wanted  string `json:"wanted_start"`
	Current string `json:"current_start"`
}

// forwardPass — sanalarni generatsiya qiladi. Daxlsiz ishlar o'z sanasida
// qoladi va davomchilariga tayanch bo'ladi; ular bilan konflikt ro'yxatga tushadi.
func forwardPass(order []*autoWork, deps []autoDep, cal schedCalendar, anchor time.Time, releaseManual bool) []schedConflict {
	predsOf := map[int64][]autoDep{}
	for _, d := range deps {
		predsOf[d.Succ] = append(predsOf[d.Succ], d)
	}
	byID := map[int64]*autoWork{}
	for _, w := range order {
		byID[w.ID] = w
	}
	conflicts := []schedConflict{}

	for _, w := range order {
		// Eng erta boshlanish: oldingilarning tugashi + 1 kun + lag.
		es := anchor
		for _, d := range predsOf[w.ID] {
			p := byID[d.Pred]
			if p == nil {
				continue
			}
			pe := p.newEnd
			if pe == nil {
				pe = p.End // daxlsiz ish o'z sanasi bilan tayanch bo'ladi
			}
			if pe == nil {
				continue
			}
			cand := cal.nextWorkday(pe.AddDate(0, 0, 1+d.Lag))
			if cand.After(es) {
				es = cand
			}
		}
		es = cal.nextWorkday(es)

		if w.immutable(releaseManual) {
			// Sanasi bor daxlsiz ish o'z joyida qoladi; talab qilingan boshlanish
			// undan keyin bo'lsa — bu konflikt (fixed siljimaydi, TZ §4).
			if w.Start != nil {
				if es.After(*w.Start) {
					label := w.ItemNumber
					if label == "" {
						label = w.Name
					}
					// Sabab aniqligi muhim: foydalanuvchi "manual" ni ko'rib
					// kimdir sanani qo'lda qo'ygan deb o'ylaydi, aslida ish
					// shunchaki qamrovdan tashqarida bo'lishi mumkin.
					reason := "manual"
					switch {
					case w.IsFixed:
						reason = "fixed"
					case w.started():
						reason = "started"
					case w.outOfScope:
						reason = "scope"
					}
					conflicts = append(conflicts, schedConflict{
						LineID: w.ID, Label: label, Reason: reason,
						Wanted: es.Format(schedDateLayout), Current: w.Start.Format(schedDateLayout),
					})
				}
				continue
			}
			// Daxlsiz, lekin sanasi yo'q (masalan boshlangan, ammo rejasiz) —
			// unga ham sana beramiz, aks holda davomchilari tayanchsiz qoladi.
		}

		dur := w.Duration
		if dur < 1 {
			dur = 1
		}
		end := cal.addWorkdays(es, dur-1)
		s, e := es, end
		w.newStart, w.newEnd = &s, &e
	}
	return conflicts
}

// backwardPass — rezervi 0 bo'lgan ishlarni kritik deb belgilaydi (TZ §4).
func backwardPass(order []*autoWork, deps []autoDep, cal schedCalendar) {
	succOf := map[int64][]autoDep{}
	for _, d := range deps {
		succOf[d.Pred] = append(succOf[d.Pred], d)
	}
	byID := map[int64]*autoWork{}
	var projectEnd time.Time
	for _, w := range order {
		byID[w.ID] = w
		if e := effEnd(w); e != nil && e.After(projectEnd) {
			projectEnd = *e
		}
	}
	if projectEnd.IsZero() {
		return
	}
	lateFinish := map[int64]time.Time{}
	// Teskari topologik tartibda yuramiz.
	for i := len(order) - 1; i >= 0; i-- {
		w := order[i]
		lf := projectEnd
		for _, d := range succOf[w.ID] {
			s := byID[d.Succ]
			if s == nil {
				continue
			}
			ls, ok := lateFinish[s.ID]
			if !ok {
				continue
			}
			dur := s.Duration
			if dur < 1 {
				dur = 1
			}
			// successor'ning kech boshlanishi → uning oldidagi ish kuni
			cand := cal.subWorkdays(cal.subWorkdays(ls, dur-1), 1+d.Lag)
			if cand.Before(lf) {
				lf = cand
			}
		}
		lateFinish[w.ID] = lf
		if e := effEnd(w); e != nil && !lf.After(*e) {
			w.critical = true // rezerv ≤ 0
		}
	}
}

func effEnd(w *autoWork) *time.Time {
	if w.newEnd != nil {
		return w.newEnd
	}
	return w.End
}
func effStart(w *autoWork) *time.Time {
	if w.newStart != nil {
		return w.newStart
	}
	return w.Start
}

// serverToday — "bugun" har doim serverdan (TZ §0.5).
func (h *Handler) serverToday() time.Time {
	var d time.Time
	if err := h.db.QueryRow(`SELECT CURRENT_DATE`).Scan(&d); err != nil {
		return time.Now().Truncate(24 * time.Hour)
	}
	return d
}

// ---------------------------------------------------------------------------
// Yurgizish: hisob + delta
// ---------------------------------------------------------------------------

type schedDelta struct {
	LineID       int64  `json:"line_id"`
	ItemNumber   string `json:"item_number"`
	Name         string `json:"name"`
	Section      string `json:"section"`
	StartBefore  string `json:"start_before"`
	EndBefore    string `json:"end_before"`
	StartAfter   string `json:"start_after"`
	EndAfter     string `json:"end_after"`
	SourceBefore string `json:"source_before"`
	DurBefore    int    `json:"duration_before"`
	DurAfter     int    `json:"duration_after"`
	DurSource    string `json:"duration_source"`
	Critical     bool   `json:"critical"`
	NormMissing  bool   `json:"norm_missing"`
}

type schedResult struct {
	Works       []*autoWork
	Deltas      []schedDelta
	Conflicts   []schedConflict
	AutoDeps    []autoDep
	ProjectEnd  *time.Time
	ServerToday time.Time
	Params      schedParams
}

func fmtDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(schedDateLayout)
}

// runAutoSchedule — to'liq hisob, HECH NARSA YOZMAYDI (TZ §4).
// scope: "unplanned" (faqat sanasizlar) | "all" | "overdue" (qolgan qismni qayta hisob)
func (h *Handler) runAutoSchedule(tenantID uuid.UUID, projectID int64, p schedParams, scope string, sectionFilter string, releaseManual bool) (*schedResult, error) {
	works, err := h.loadSchedWorks(tenantID, projectID)
	if err != nil {
		return nil, err
	}
	if len(works) == 0 {
		return nil, fmt.Errorf("loyihada ish qatorlari topilmadi")
	}
	cal := h.loadCalendar(tenantID, p.WorkdaysMask)
	norms := h.loadProductivityNorms(tenantID)
	today := h.serverToday()

	// Davomiylik: qo'lda kiritilgani saqlanadi, qolgani kaskad bo'yicha.
	for _, w := range works {
		if w.DurSource == "manual" && w.Duration > 0 {
			continue
		}
		d, src, snap := computeDuration(w, p, norms)
		w.Duration, w.DurSource, w.NormSnap = d, src, snap
	}

	// Bog'liqliklar: auto qayta quriladi, manual tegilmaydi.
	autoDeps := buildAutoDeps(works, p.ParallelLimit)
	manualDeps, err := h.loadManualDeps(tenantID, projectID)
	if err != nil {
		return nil, err
	}
	deps := append(append([]autoDep{}, autoDeps...), manualDeps...)

	order, err := topoSort(works, deps)
	if err != nil {
		return nil, err
	}

	// Tayanch sana: loyiha starti va bugundan kechrog'i (o'tmishga rejalashtirmaymiz).
	anchor := today
	if p.StartDate != nil && p.StartDate.After(anchor) {
		anchor = *p.StartDate
	}
	anchor = cal.nextWorkday(anchor)

	// Qamrov: hisobdan tashqaridagi ishlar o'z sanasida qoladi (tayanch sifatida).
	for _, w := range works {
		out := false
		switch scope {
		case "unplanned":
			out = w.Start != nil // sanasi borlar tegilmaydi
		case "overdue":
			overdue := w.End != nil && w.End.Before(today) &&
				(w.Approval == "pending" || w.Approval == "in_progress")
			out = !overdue
		case "section":
			out = sectionFilter != "" && !strings.Contains(w.Section, sectionFilter)
		}
		if out {
			// Daxlsiz sifatida ko'rsatamiz — forwardPass uni siljitmaydi.
			// Source ga tegmaymiz: u "kim sanani qo'ygan" degan haqiqiy holat,
			// qamrov esa shu yugurishning filtri.
			w.outOfScope = true
		}
	}

	conflicts := forwardPass(order, deps, cal, anchor, releaseManual)
	backwardPass(order, deps, cal)

	// Delta va loyiha tugashi.
	deltas := []schedDelta{}
	var projectEnd *time.Time
	for _, w := range order {
		if e := effEnd(w); e != nil && (projectEnd == nil || e.After(*projectEnd)) {
			t := *e
			projectEnd = &t
		}
		if w.newStart == nil {
			continue
		}
		// O'zgarish yo'q bo'lsa deltaga qo'shmaymiz.
		if w.Start != nil && w.End != nil &&
			w.Start.Format(schedDateLayout) == w.newStart.Format(schedDateLayout) &&
			w.End.Format(schedDateLayout) == w.newEnd.Format(schedDateLayout) {
			continue
		}
		deltas = append(deltas, schedDelta{
			LineID: w.ID, ItemNumber: w.ItemNumber, Name: w.Name, Section: w.Section,
			StartBefore: fmtDate(w.Start), EndBefore: fmtDate(w.End),
			StartAfter: fmtDate(w.newStart), EndAfter: fmtDate(w.newEnd),
			SourceBefore: w.OrigSource, DurBefore: w.OrigDuration, DurAfter: w.Duration,
			DurSource: w.DurSource, Critical: w.critical,
			NormMissing: w.DurSource == "default",
		})
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i].LineID < deltas[j].LineID })

	return &schedResult{
		Works: order, Deltas: deltas, Conflicts: conflicts, AutoDeps: autoDeps,
		ProjectEnd: projectEnd, ServerToday: today, Params: p,
	}, nil
}
