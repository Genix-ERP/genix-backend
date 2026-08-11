package handler

import (
	"database/sql"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION ACT PDF TEMPLATES (Forma 2, 3, 19)
// Layout mirrors the reference XLSX generators so printed
// PDF and downloaded XLSX share the same structure.
// =====================================================

const pdfBaseCSS = `
	@page { size: A4 landscape; margin: 12mm; }
	body { font-family: 'Times New Roman', serif; font-size: 10pt; margin: 0; color: #000; }
	h1.title { font-size: 14pt; text-align: center; margin: 4px 0; }
	h2.subtitle { font-size: 12pt; text-align: center; margin: 2px 0 10px 0; font-weight: normal; }
	.parties { width: 100%%; border-collapse: collapse; margin-bottom: 6px; }
	.parties td { padding: 2px 4px; vertical-align: top; font-size: 10pt; }
	.parties td.lbl { font-weight: bold; width: 16%%; }
	.parties td.val { width: 34%%; }
	table.data { width: 100%%; border-collapse: collapse; margin: 8px 0; table-layout: fixed; }
	table.data th, table.data td { border: 1px solid #000; padding: 3px 4px; font-size: 9pt; vertical-align: middle; word-wrap: break-word; }
	table.data th { background: #F2F2F2; text-align: center; font-weight: bold; }
	table.data td.num { text-align: right; font-variant-numeric: tabular-nums; }
	table.data td.center { text-align: center; }
	.section-row td { background: #D9E1F2; font-weight: bold; text-align: center; }
	.total-row td { background: #FDE9D9; font-weight: bold; }
	.total-row td.num { text-align: right; }
	.sub-total-row td.num { text-align: right; font-weight: bold; }
	.amount-in-words { font-weight: bold; margin: 10px 4px; }
	.signatures { width: 100%%; border-collapse: collapse; margin-top: 20px; }
	.signatures td { padding: 10px 8px; vertical-align: top; width: 50%%; font-size: 10pt; }
	.sig-line { display: inline-block; min-width: 220px; border-bottom: 1px solid #000; }
	@media print { body { margin: 0; } }
`

// escape html
func esc(s string) string { return html.EscapeString(s) }

// fmtMoney formats a number with space thousand separators and 2 decimals.
func fmtMoney(v float64) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	whole := int64(v)
	frac := int64((v - float64(whole)) * 100)
	if frac < 0 {
		frac = -frac
	}
	ws := fmt.Sprintf("%d", whole)
	var parts []string
	for len(ws) > 3 {
		parts = append([]string{ws[len(ws)-3:]}, parts...)
		ws = ws[:len(ws)-3]
	}
	parts = append([]string{ws}, parts...)
	return sign + strings.Join(parts, " ") + fmt.Sprintf(".%02d", frac)
}

// renderForma2HTML generates HTML for Forma 2 (KS-2) act, matching the
// reference multi-level table + totals block layout.
func (h *Handler) renderForma2HTML(actID int64, tenantID uuid.UUID, projectName, projectAddress, clientName string) string {
	_ = projectAddress
	_ = clientName

	d, err := h.loadForma2Data(actID, tenantID)
	if err != nil {
		return fmt.Sprintf("<html><body><p>Error loading Forma 2 data: %s</p></body></html>", esc(err.Error()))
	}
	if d.ProjectName == "" {
		d.ProjectName = projectName
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>%s</style></head><body>`, pdfBaseCSS))

	// Parties header strip
	b.WriteString(`<table class="parties">`)
	b.WriteString(fmt.Sprintf(`<tr><td class="lbl">ЗАКАЗЧИК:</td><td class="val" colspan="3"><b>%s</b></td></tr>`, esc(d.Client.Name)))
	b.WriteString(fmt.Sprintf(`<tr><td class="lbl">ПОДРЯДЧИК:</td><td class="val" colspan="3"><b>%s</b></td></tr>`, esc(d.Contractor.Name)))
	b.WriteString(`</table>`)

	// Title
	b.WriteString(fmt.Sprintf(`<h1 class="title">АКТ №%s</h1>`, esc(d.ActNumber)))
	b.WriteString(`<h2 class="subtitle">выполненных работ</h2>`)
	b.WriteString(fmt.Sprintf(`<div style="text-align:center;font-weight:bold;">ПО ОБЪЕКТУ: %s</div>`, esc(d.ProjectName)))
	if d.PeriodLabel != "" {
		b.WriteString(fmt.Sprintf(`<div style="text-align:center;">%s</div>`, esc(d.PeriodLabel)))
	}

	// Data table
	b.WriteString(`<table class="data">`)
	b.WriteString(`<colgroup>`)
	b.WriteString(`<col style="width:4%"><col style="width:10%"><col style="width:30%"><col style="width:6%"><col style="width:7%"><col style="width:7%">`)
	b.WriteString(`<col style="width:7%"><col style="width:7%"><col style="width:6%"><col style="width:6%"><col style="width:6%"><col style="width:4%">`)
	b.WriteString(`</colgroup>`)
	b.WriteString(`<thead>`)
	b.WriteString(`<tr>`)
	b.WriteString(`<th rowspan="2">Т/р</th>`)
	b.WriteString(`<th rowspan="2">Меъёрлар рақами (шифри)</th>`)
	b.WriteString(`<th rowspan="2">Иш турлари</th>`)
	b.WriteString(`<th rowspan="2">Ўлчов бирлиги</th>`)
	b.WriteString(`<th rowspan="2">Лойиха кўрсаткичлари бўйича ишлари миқдори</th>`)
	b.WriteString(`<th rowspan="2">Давр бўйича миқдор</th>`)
	b.WriteString(`<th colspan="6">Қиймати</th>`)
	b.WriteString(`</tr>`)
	b.WriteString(`<tr>`)
	b.WriteString(`<th>БИРЛИККА</th><th>ЖАМИ</th><th>ЗАРПЛАТА</th><th>ЭММ</th><th>МАТЕРИАЛЫ</th><th>КАБЕЛИ</th>`)
	b.WriteString(`</tr>`)
	// Column-number row
	b.WriteString(`<tr>`)
	for i := 1; i <= 12; i++ {
		b.WriteString(fmt.Sprintf(`<th>%d</th>`, i))
	}
	b.WriteString(`</tr>`)
	b.WriteString(`</thead><tbody>`)

	for _, l := range d.Lines {
		if l.IsSection {
			b.WriteString(fmt.Sprintf(`<tr class="section-row"><td colspan="12">%s</td></tr>`, esc(l.SectionTitle)))
			continue
		}
		b.WriteString(`<tr>`)
		b.WriteString(fmt.Sprintf(`<td class="center">%s</td>`, esc(l.Display)))
		b.WriteString(fmt.Sprintf(`<td class="center">%s</td>`, esc(l.NormCode)))
		b.WriteString(fmt.Sprintf(`<td>%s</td>`, esc(l.Name)))
		b.WriteString(fmt.Sprintf(`<td class="center">%s</td>`, esc(l.UOM)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(l.QtyPlan)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(l.QtyPeriod)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(l.UnitPrice)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(l.Total)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(l.Labor)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(l.Equipment)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(l.Materials)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(l.Cables)))
		b.WriteString(`</tr>`)
	}

	// Totals block
	baseTotal := d.LaborTotal + d.EquipTotal + d.MaterialsTotal
	transport := d.MaterialsTotal * d.TransportPct / 100.0
	subtotal1 := baseTotal + transport
	subtotal2 := subtotal1 + d.CablesTotal
	other := subtotal2 * d.OtherPct / 100.0
	grand := subtotal2 + other
	contractor := grand - d.ReturnAmount

	writeTotal := func(label string, amount float64, emphasise bool) {
		cls := "sub-total-row"
		if emphasise {
			cls = "total-row"
		}
		b.WriteString(fmt.Sprintf(`<tr class="%s"><td colspan="7">%s</td><td class="num">%s</td><td class="num"></td><td class="num"></td><td class="num"></td><td class="num"></td></tr>`,
			cls, esc(label), fmtMoney(amount)))
	}
	writeTotal("ИТОГО", baseTotal, true)
	writeTotal("В ТОМ ЧИСЛЕ: ЗАРПЛАТА", d.LaborTotal, false)
	writeTotal("ЭММ", d.EquipTotal, false)
	writeTotal("МАТЕРИАЛЫ", d.MaterialsTotal, false)
	writeTotal(fmt.Sprintf("ТРАНСПОРТ МАТЕРИАЛОВ %.0f%%", d.TransportPct), transport, false)
	writeTotal("ИТОГО", subtotal1, true)
	writeTotal("КАБЕЛЬНО-ПРОВОДНИКОВЫЕ МАТЕРИАЛЫ", d.CablesTotal, false)
	writeTotal("ИТОГО", subtotal2, true)
	writeTotal(fmt.Sprintf("ПРОЧИЕ ЗАТРАТЫ %.0f%%", d.OtherPct), other, false)
	writeTotal("ИТОГО", grand, true)
	if d.ReturnAmount != 0 {
		writeTotal("ВОЗВРАТ МАТЕРИАЛОВ ЗАКАЗЧИКА", d.ReturnAmount, false)
	}
	writeTotal("ИТОГО ВЫПОЛНЕНИЕ ПОДРЯДЧИКА", contractor, true)
	b.WriteString(`</tbody></table>`)

	// Signatures
	b.WriteString(`<table class="signatures"><tr>`)
	b.WriteString(`<td><b>ЗАКАЗЧИК:</b><br>`)
	if d.Client.Name != "" {
		b.WriteString(fmt.Sprintf(`Директор %s<br><br>`, esc(d.Client.Name)))
	}
	b.WriteString(`<span class="sig-line"></span>`)
	if d.Client.Director != "" {
		b.WriteString(fmt.Sprintf(` %s`, esc(d.Client.Director)))
	}
	b.WriteString(`<br><br>Главный бухгалтер:<br><br><span class="sig-line"></span>`)
	if d.Client.Accountant != "" {
		b.WriteString(fmt.Sprintf(` %s`, esc(d.Client.Accountant)))
	}
	b.WriteString(`</td>`)
	b.WriteString(`<td><b>ПОДРЯДЧИК:</b><br>`)
	if d.Contractor.Name != "" {
		b.WriteString(fmt.Sprintf(`Директор %s<br><br>`, esc(d.Contractor.Name)))
	}
	b.WriteString(`<span class="sig-line"></span>`)
	if d.Contractor.Director != "" {
		b.WriteString(fmt.Sprintf(` %s`, esc(d.Contractor.Director)))
	}
	b.WriteString(`<br><br>Главный бухгалтер:<br><br><span class="sig-line"></span>`)
	if d.Contractor.Accountant != "" {
		b.WriteString(fmt.Sprintf(` %s`, esc(d.Contractor.Accountant)))
	}
	b.WriteString(`</td></tr></table>`)

	b.WriteString(`</body></html>`)
	return b.String()
}

func emptyIfZero(v float64) string {
	if v == 0 {
		return ""
	}
	return fmtMoney(v)
}

// renderForma3HTML generates HTML for Forma 3 (KS-3) certificate matching the
// multi-level 16-column reference layout with parties block and amount-in-words.
func (h *Handler) renderForma3HTML(actID int64, tenantID uuid.UUID, projectName, projectAddress, clientName string) string {
	_ = projectAddress
	_ = clientName

	d, err := h.loadForma3Data(actID, tenantID)
	if err != nil {
		return fmt.Sprintf("<html><body><p>Error loading Forma 3 data: %s</p></body></html>", esc(err.Error()))
	}
	if d.ProjectName == "" {
		d.ProjectName = projectName
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>%s</style></head><body>`, pdfBaseCSS))

	b.WriteString(`<h1 class="title">СПРАВКА-СЧЕТ-ФАКТУРА</h1>`)
	b.WriteString(`<h2 class="subtitle">О СТОИМОСТИ ВЫПОЛНЕННЫХ РАБОТ (ПОНЕСЕННЫХ ЗАТРАТ)</h2>`)

	// Metadata strip
	b.WriteString(`<table class="parties"><tr>`)
	b.WriteString(fmt.Sprintf(`<td class="lbl">№</td><td class="val">%s</td>`, esc(d.CertNumber)))
	b.WriteString(fmt.Sprintf(`<td class="lbl">Дата составления</td><td class="val">%s г.</td>`, d.CertDate.Format("02.01.2006")))
	b.WriteString(fmt.Sprintf(`<td class="lbl">Отчетный период</td><td class="val">с %s по %s</td>`,
		esc(d.PeriodMonthFromName), esc(d.PeriodMonthToName)))
	b.WriteString(`</tr></table>`)

	// Parties block (two columns side by side)
	b.WriteString(`<table class="parties">`)
	rows := [][5]string{
		{"Заказчик", d.Client.Name, "Подрядчик:", d.Contractor.Name, ""},
		{"Адрес:", d.Client.Address, "Адрес:", d.Contractor.Address, ""},
		{"Телефон:", d.Client.Phone, "Телефон:", d.Contractor.Phone, ""},
		{"Банк:", d.Client.Bank, "Банк:", d.Contractor.Bank, ""},
		{"Расчетный счет:", d.Client.Account, "Расчетный счет:", d.Contractor.Account, ""},
		{"МФО:", d.Client.MFO, "СТИР:", d.Contractor.STIR, ""},
		{"СТИР:", d.Client.STIR, "КОД ПО ОКОНХ", d.Contractor.OKONH, ""},
		{"КОД по ОКОНХ", d.Client.OKONH, "МФО", d.Contractor.MFO, ""},
	}
	for _, r := range rows {
		b.WriteString(fmt.Sprintf(
			`<tr><td class="lbl">%s</td><td class="val">%s</td><td class="lbl">%s</td><td class="val">%s</td></tr>`,
			esc(r[0]), esc(r[1]), esc(r[2]), esc(r[3])))
	}
	b.WriteString(`</table>`)

	// Object + contract
	b.WriteString(fmt.Sprintf(`<div style="margin:6px 0;"><b>Наименование объекта и его адрес:</b> %s</div>`, esc(d.ProjectName)))
	contract := "Договор:"
	if d.ContractNumber != "" {
		contract += " № " + d.ContractNumber
	}
	if !d.ContractDate.IsZero() {
		contract += " от " + d.ContractDate.Format("02.01.2006") + " г."
	}
	b.WriteString(fmt.Sprintf(`<div style="margin:4px 0;"><b>%s</b></div>`, esc(contract)))
	b.WriteString(fmt.Sprintf(`<div style="margin:4px 0;"><b>Общая стоимость в договорных текущих ценах (тыс.сум):</b> %s</div>`,
		fmtMoney(d.TotalContractValueThousand)))

	// Main 16-column table
	b.WriteString(`<table class="data">`)
	b.WriteString(`<thead>`)
	b.WriteString(`<tr>`)
	b.WriteString(`<th rowspan="3">№№ п/п</th>`)
	b.WriteString(`<th rowspan="3">Наименование объектов, этапов, видов работ</th>`)
	b.WriteString(`<th rowspan="3">Ед. изм</th>`)
	b.WriteString(`<th colspan="2">Объем работ в физич. показ.</th>`)
	b.WriteString(`<th colspan="2">Стоим. в догов. тек. ценах (тыс.сум)</th>`)
	b.WriteString(`<th colspan="9">Выполненные работы (понесенные затраты)</th>`)
	b.WriteString(`</tr>`)
	b.WriteString(`<tr>`)
	b.WriteString(`<th rowspan="2">Всего</th><th rowspan="2">в т.ч. на текущий год</th>`)
	b.WriteString(`<th rowspan="2">Всего</th><th rowspan="2">в т.ч. на текущий год</th>`)
	b.WriteString(`<th colspan="3">с начала строительства</th>`)
	b.WriteString(`<th colspan="3">с начала года</th>`)
	b.WriteString(`<th colspan="3">в т.ч. за отчетный месяц</th>`)
	b.WriteString(`</tr>`)
	b.WriteString(`<tr>`)
	for i := 0; i < 3; i++ {
		b.WriteString(`<th>в физич. показ.</th><th>в % к объему всего работ</th><th>в дог. тек. ценах (тыс.сум)</th>`)
	}
	b.WriteString(`</tr>`)
	b.WriteString(`<tr>`)
	for i := 1; i <= 16; i++ {
		b.WriteString(fmt.Sprintf(`<th>%d</th>`, i))
	}
	b.WriteString(`</tr>`)
	b.WriteString(`</thead><tbody>`)
	for _, r := range d.Rows {
		b.WriteString(`<tr>`)
		b.WriteString(fmt.Sprintf(`<td class="center">%d</td>`, r.No))
		b.WriteString(fmt.Sprintf(`<td>%s</td>`, esc(r.Name)))
		b.WriteString(fmt.Sprintf(`<td class="center">%s</td>`, esc(r.UOM)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(r.VolumeTotal)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(r.VolumeCurrentYear)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(r.ValueTotal)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(r.ValueCurrentYear)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(r.DoneFromStartQty)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(r.DoneFromStartPct)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(r.DoneFromStartVal)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(r.DoneFromYearQty)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(r.DoneFromYearPct)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(r.DoneFromYearVal)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(r.DoneThisPeriodQty)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, emptyIfZero(r.DoneThisPeriodPct)))
		b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(r.DoneThisPeriodVal)))
		b.WriteString(`</tr>`)
	}
	// Total row (ВСЕГО БЕЗ НДС)
	b.WriteString(`<tr class="total-row">`)
	b.WriteString(`<td class="center"></td><td>ВСЕГО БЕЗ НДС</td><td class="center">т.сум</td>`)
	b.WriteString(`<td></td><td></td>`)
	b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(d.TotalWithoutVAT)))
	b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(d.TotalWithoutVAT)))
	b.WriteString(`<td></td><td></td>`)
	b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(d.TotalWithoutVAT)))
	b.WriteString(`<td></td><td></td>`)
	b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(d.TotalWithoutVAT)))
	b.WriteString(`<td></td><td></td>`)
	b.WriteString(fmt.Sprintf(`<td class="num">%s</td>`, fmtMoney(d.TotalWithoutVAT)))
	b.WriteString(`</tr>`)
	b.WriteString(`</tbody></table>`)

	// Amount in words
	sumInSum := int64(d.TotalWithoutVAT * 1000.0)
	b.WriteString(fmt.Sprintf(`<div class="amount-in-words">Итого к оплате: %s (%s) сум</div>`,
		formatWithSpaces(sumInSum), esc(numberToWordsRU(sumInSum))))

	// Signatures
	b.WriteString(`<table class="signatures"><tr>`)
	b.WriteString(`<td><b>Руководитель Заказчика:</b><br><br>`)
	b.WriteString(`<span class="sig-line"></span>`)
	if d.Client.Director != "" {
		b.WriteString(fmt.Sprintf(` %s`, esc(d.Client.Director)))
	}
	b.WriteString(`<br><br>Главный бухгалтер:<br><br><span class="sig-line"></span>`)
	if d.Client.Accountant != "" {
		b.WriteString(fmt.Sprintf(` %s`, esc(d.Client.Accountant)))
	}
	b.WriteString(`</td>`)
	b.WriteString(`<td><b>Руководитель Подрядчика:</b><br><br>`)
	b.WriteString(`<span class="sig-line"></span>`)
	if d.Contractor.Director != "" {
		b.WriteString(fmt.Sprintf(` %s`, esc(d.Contractor.Director)))
	}
	b.WriteString(`<br><br>Главный бухгалтер:<br><br><span class="sig-line"></span>`)
	if d.Contractor.Accountant != "" {
		b.WriteString(fmt.Sprintf(` %s`, esc(d.Contractor.Accountant)))
	}
	b.WriteString(`</td></tr></table>`)

	b.WriteString(`</body></html>`)
	return b.String()
}

// renderForma19HTML generates HTML for Forma 19 (Material Consumption Report)
func (h *Handler) renderForma19HTML(actID int64, tenantID uuid.UUID, projectName, projectAddress, clientName string) string {
	_ = clientName
	var b strings.Builder

	var name, notes string
	var periodFrom, periodTo sql.NullTime
	var amountTotal float64

	h.db.QueryRow(`
		SELECT a.name, COALESCE(a.notes, ''), a.period_from, a.period_to, a.amount_total
		FROM construction_act a WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(&name, &notes, &periodFrom, &periodTo, &amountTotal)

	b.WriteString(fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>%s
		.change-row { background-color: #FFF7ED; }
		.badge-base { background: #DCFCE7; color: #166534; padding: 2px 6px; border-radius: 4px; font-size: 9pt; }
		.badge-change { background: #FED7AA; color: #9A3412; padding: 2px 6px; border-radius: 4px; font-size: 9pt; }
	</style></head><body>`, pdfBaseCSS))
	b.WriteString(`<h1 class="title">ФОРМА 19</h1>`)
	b.WriteString(`<h2 class="subtitle">Отчёт о расходе материалов</h2>`)

	b.WriteString(`<table class="parties">`)
	b.WriteString(fmt.Sprintf(`<tr><td class="lbl">Объект:</td><td class="val">%s</td></tr>`, esc(projectName)))
	if projectAddress != "" {
		b.WriteString(fmt.Sprintf(`<tr><td class="lbl">Адрес:</td><td class="val">%s</td></tr>`, esc(projectAddress)))
	}
	if periodFrom.Valid && periodTo.Valid {
		b.WriteString(fmt.Sprintf(`<tr><td class="lbl">Период:</td><td class="val">%s — %s</td></tr>`,
			periodFrom.Time.Format("02.01.2006"), periodTo.Time.Format("02.01.2006")))
	}
	b.WriteString(`</table>`)

	// Lines table
	rows, err := h.db.Query(`
		SELECT name, uom, row_type, boshi, keldi, sarf, qoldi, cost_price, change_reason, COALESCE(change_note, '')
		FROM construction_act_line WHERE act_id = $1 ORDER BY row_type ASC, sort_order ASC
	`, actID)
	if err == nil {
		defer rows.Close()
		b.WriteString(`<table class="data">`)
		b.WriteString(`<tr><th>№</th><th>Материал</th><th>Ед.</th><th>Бошида</th><th>Келди</th><th>Сарф</th><th>Қолди</th><th>Нарх</th><th>Сумма</th><th>Тур</th><th>Сабаб</th></tr>`)
		i := 0
		var smetaTotal, changeTotal float64
		for rows.Next() {
			var lName, uom, rowType, changeReason, changeNote string
			var boshi, keldi, sarf, qoldi, costPrice float64
			rows.Scan(&lName, &uom, &rowType, &boshi, &keldi, &sarf, &qoldi, &costPrice, &changeReason, &changeNote)
			_ = changeNote
			i++
			summa := sarf * costPrice
			rowClass := ""
			badge := `<span class="badge-base">Asos</span>`
			if rowType == "change" {
				rowClass = ` class="change-row"`
				badge = `<span class="badge-change">O'zgarish</span>`
				changeTotal += summa
			} else {
				smetaTotal += summa
			}
			b.WriteString(fmt.Sprintf(`<tr%s><td class="center">%d</td><td>%s</td><td class="center">%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td><td class="center">%s</td><td>%s</td></tr>`,
				rowClass, i, esc(lName), esc(uom),
				fmtMoney(boshi), fmtMoney(keldi), fmtMoney(sarf), fmtMoney(qoldi),
				fmtMoney(costPrice), fmtMoney(summa), badge, esc(changeReason)))
		}
		total := smetaTotal + changeTotal
		b.WriteString(fmt.Sprintf(`<tr style="font-weight:bold"><td colspan="8" class="num">Smeta bo'yicha:</td><td class="num">%s</td><td colspan="2"></td></tr>`, fmtMoney(smetaTotal)))
		b.WriteString(fmt.Sprintf(`<tr style="font-weight:bold"><td colspan="8" class="num">O'zgarishlar:</td><td class="num">%s</td><td colspan="2"></td></tr>`, fmtMoney(changeTotal)))
		b.WriteString(fmt.Sprintf(`<tr style="font-weight:bold"><td colspan="8" class="num">JAMI:</td><td class="num">%s</td><td colspan="2"></td></tr>`, fmtMoney(total)))
		b.WriteString(`</table>`)
	}

	b.WriteString(fmt.Sprintf(`<p style="font-size:9pt;color:#666;margin-top:30px">Hujjat yaratilgan: %s</p>`, time.Now().Format("02.01.2006 15:04")))
	b.WriteString(`</body></html>`)
	return b.String()
}

// renderGenericActHTML produces a simple PDF for act types that don't
// have a regulated form layout — currently "acceptance" (Qabul qilish)
// and "defect" (Nuqson). The Russian/Uzbek construction industry only
// standardises Forma 2 / 3 / 19; acceptance and defect acts are
// project-specific, so we emit a neutral document with:
//
//   - Header titled per act type ("AKT QABUL QILISH" / "NUQSONLAR
//     AKTI"), localized in Russian below.
//   - Project info block (name, address, period).
//   - Notes section (act.notes) — typically the acceptance criteria
//     or the defect description the user entered.
//   - Line items table (if any rows are linked) — uses the same
//     construction_act_line layout the other forms use.
//   - Amount-total line.
//   - Two-column signature block for contractor + client.
//
// This keeps the user from hitting the "PDF eksport qo'llab-quvvatlanmaydi"
// 400 they used to get for these types.
func (h *Handler) renderGenericActHTML(actID int64, tenantID uuid.UUID, actType, projectName, projectAddress, clientName string) string {
	var b strings.Builder

	var name, notes string
	var periodFrom, periodTo sql.NullTime
	var amountTotal float64
	h.db.QueryRow(`
		SELECT a.name, COALESCE(a.notes, ''), a.period_from, a.period_to, a.amount_total
		FROM construction_act a WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(&name, &notes, &periodFrom, &periodTo, &amountTotal)

	// Pick the document title based on type. Both shown bilingually so
	// the printed PDF reads naturally for the Uzbek+Russian audiences
	// this app serves.
	var titleUz, titleRu string
	switch actType {
	case "acceptance":
		titleUz, titleRu = "AKT QABUL QILISH", "АКТ ПРИЁМКИ"
	case "defect":
		titleUz, titleRu = "NUQSONLAR AKTI", "АКТ ДЕФЕКТОВ"
	default:
		titleUz, titleRu = "AKT", "АКТ"
	}

	// Portrait page is fine here — no wide line tables like Forma 2.
	b.WriteString(fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>%s
		@page { size: A4; margin: 14mm; }
		.notes-block { border: 1px solid #000; padding: 8px 10px; margin: 8px 0 12px 0; min-height: 80px; font-size: 10pt; white-space: pre-wrap; }
		.notes-empty { color: #999; font-style: italic; }
	</style></head><body>`, pdfBaseCSS))

	b.WriteString(fmt.Sprintf(`<h1 class="title">%s</h1>`, esc(titleUz)))
	b.WriteString(fmt.Sprintf(`<h2 class="subtitle">%s</h2>`, esc(titleRu)))
	if name != "" {
		b.WriteString(fmt.Sprintf(`<p style="text-align:center;font-size:11pt;margin:2px 0 12px 0">№ %s</p>`, esc(name)))
	}

	// Parties / project info block.
	b.WriteString(`<table class="parties">`)
	b.WriteString(fmt.Sprintf(`<tr><td class="lbl">Объект:</td><td class="val" colspan="3">%s</td></tr>`, esc(projectName)))
	if projectAddress != "" {
		b.WriteString(fmt.Sprintf(`<tr><td class="lbl">Адрес:</td><td class="val" colspan="3">%s</td></tr>`, esc(projectAddress)))
	}
	if clientName != "" {
		b.WriteString(fmt.Sprintf(`<tr><td class="lbl">Заказчик:</td><td class="val" colspan="3">%s</td></tr>`, esc(clientName)))
	}
	if periodFrom.Valid && periodTo.Valid {
		b.WriteString(fmt.Sprintf(`<tr><td class="lbl">Период:</td><td class="val" colspan="3">%s — %s</td></tr>`,
			periodFrom.Time.Format("02.01.2006"), periodTo.Time.Format("02.01.2006")))
	}
	b.WriteString(`</table>`)

	// Notes / description block — main free-text content for
	// acceptance/defect acts.
	b.WriteString(`<p style="font-weight:bold;margin:10px 0 4px 0">Описание / Tavsif:</p>`)
	if strings.TrimSpace(notes) == "" {
		b.WriteString(`<div class="notes-block notes-empty">— описание не заполнено —</div>`)
	} else {
		b.WriteString(fmt.Sprintf(`<div class="notes-block">%s</div>`, esc(notes)))
	}

	// Lines table — same shape as the other render functions for
	// consistency, but column set trimmed since these acts don't carry
	// rates/totals the way Forma 2 does.
	rows, err := h.db.Query(`
		SELECT name, uom, quantity, unit_rate, total_amount
		FROM construction_act_line WHERE act_id = $1 ORDER BY sort_order ASC, id ASC
	`, actID)
	if err == nil {
		defer rows.Close()
		lineRows := make([]struct {
			name     string
			uom      string
			quantity float64
			unitRate float64
			totalAmt float64
		}, 0)
		for rows.Next() {
			var r struct {
				name     string
				uom      string
				quantity float64
				unitRate float64
				totalAmt float64
			}
			if scanErr := rows.Scan(&r.name, &r.uom, &r.quantity, &r.unitRate, &r.totalAmt); scanErr == nil {
				lineRows = append(lineRows, r)
			}
		}
		if len(lineRows) > 0 {
			b.WriteString(`<table class="data">`)
			b.WriteString(`<tr><th style="width:6%">№</th><th>Наименование</th><th style="width:10%">Ед.</th><th style="width:12%">Кол-во</th><th style="width:14%">Цена</th><th style="width:14%">Сумма</th></tr>`)
			var sum float64
			for i, r := range lineRows {
				b.WriteString(fmt.Sprintf(`<tr><td class="center">%d</td><td>%s</td><td class="center">%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td></tr>`,
					i+1, esc(r.name), esc(r.uom),
					fmtMoney(r.quantity), fmtMoney(r.unitRate), fmtMoney(r.totalAmt)))
				sum += r.totalAmt
			}
			b.WriteString(fmt.Sprintf(`<tr class="total-row"><td colspan="5" class="num">ИТОГО:</td><td class="num">%s</td></tr>`,
				fmtMoney(sum)))
			b.WriteString(`</table>`)
		}
	}

	// Amount summary — falls back to act.amount_total when no lines
	// exist (acceptance acts often have just a global sum).
	if amountTotal > 0 {
		b.WriteString(fmt.Sprintf(`<p class="amount-in-words">Общая сумма: %s сум</p>`, fmtMoney(amountTotal)))
	}

	// Signature block — universal contractor + client pair. Real
	// signing happens via the construction_act_signature flow; the
	// printed PDF just leaves the lines for ink signatures.
	b.WriteString(`<table class="signatures">`)
	b.WriteString(`<tr>`)
	b.WriteString(`<td><b>Подрядчик / Pudratchi:</b><br><br><span class="sig-line"></span><br><span style="font-size:9pt;color:#666">(подпись, ФИО, дата)</span></td>`)
	b.WriteString(`<td><b>Заказчик / Buyurtmachi:</b><br><br><span class="sig-line"></span><br><span style="font-size:9pt;color:#666">(подпись, ФИО, дата)</span></td>`)
	b.WriteString(`</tr></table>`)

	b.WriteString(fmt.Sprintf(`<p style="font-size:9pt;color:#666;margin-top:30px">Hujjat yaratilgan: %s</p>`, time.Now().Format("02.01.2006 15:04")))
	b.WriteString(`</body></html>`)
	return b.String()
}
