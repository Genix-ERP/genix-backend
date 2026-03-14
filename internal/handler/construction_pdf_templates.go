package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION ACT PDF TEMPLATES (Forma 2, 3, 19)
// =====================================================

const pdfBaseCSS = `
	body { font-family: 'Times New Roman', serif; font-size: 11pt; margin: 0; padding: 20px; }
	h1 { font-size: 14pt; text-align: center; margin-bottom: 5px; }
	h2 { font-size: 12pt; text-align: center; margin-top: 0; }
	.header-info { margin-bottom: 15px; }
	.header-info td { padding: 2px 8px; vertical-align: top; }
	table.data { width: 100%%; border-collapse: collapse; margin: 10px 0; }
	table.data th, table.data td { border: 1px solid #333; padding: 4px 6px; font-size: 10pt; }
	table.data th { background: #f0f0f0; text-align: center; font-weight: bold; }
	table.data td.num { text-align: right; }
	table.data td.center { text-align: center; }
	.totals { margin-top: 10px; }
	.totals td { padding: 3px 8px; font-weight: bold; }
	.signatures { margin-top: 40px; }
	.signatures td { padding: 10px 20px; vertical-align: top; width: 50%%; }
	.sig-line { border-bottom: 1px solid #333; min-width: 200px; display: inline-block; margin-top: 30px; }
	.sig-date { font-size: 9pt; color: #666; }
	@media print { body { margin: 0; } }
`

// renderForma2HTML generates HTML for Forma 2 (KS-2) act
func (h *Handler) renderForma2HTML(actID int64, tenantID uuid.UUID, projectName, projectAddress, clientName string) string {
	var b strings.Builder

	// Get act header
	var name, state, currency string
	var subcontractName sql.NullString
	var periodFrom, periodTo sql.NullTime
	var amountTotal, vatPct, vatAmount, totalWithVat float64
	var actNumber sql.NullInt64
	var signedContractorAt, signedClientAt sql.NullTime

	h.db.QueryRow(`
		SELECT a.name, a.state, a.currency, COALESCE(s.name, ''),
		       a.period_from, a.period_to, a.amount_total,
		       COALESCE(a.vat_pct, 12), COALESCE(a.vat_amount, 0), COALESCE(a.amount_total_with_vat, 0),
		       a.act_number, a.signed_contractor_at, a.signed_client_at
		FROM construction_act a
		LEFT JOIN construction_subcontract s ON s.id = a.subcontract_id
		WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(
		&name, &state, &currency, &subcontractName,
		&periodFrom, &periodTo, &amountTotal,
		&vatPct, &vatAmount, &totalWithVat,
		&actNumber, &signedContractorAt, &signedClientAt,
	)

	b.WriteString(fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>%s</style></head><body>`, pdfBaseCSS))
	b.WriteString(`<h1>АКТ</h1>`)
	b.WriteString(`<h2>приёмки выполненных работ (Форма 2)</h2>`)

	// Header info
	b.WriteString(`<table class="header-info">`)
	b.WriteString(fmt.Sprintf(`<tr><td><b>Объект:</b></td><td>%s</td></tr>`, projectName))
	if projectAddress != "" {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Адрес:</b></td><td>%s</td></tr>`, projectAddress))
	}
	b.WriteString(fmt.Sprintf(`<tr><td><b>Заказчик:</b></td><td>%s</td></tr>`, clientName))
	if subcontractName.Valid && subcontractName.String != "" {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Подрядчик:</b></td><td>%s</td></tr>`, subcontractName.String))
	}
	if actNumber.Valid {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Акт №:</b></td><td>%d</td></tr>`, actNumber.Int64))
	}
	if periodFrom.Valid && periodTo.Valid {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Период:</b></td><td>%s — %s</td></tr>`,
			periodFrom.Time.Format("02.01.2006"), periodTo.Time.Format("02.01.2006")))
	}
	b.WriteString(`</table>`)

	// Lines table
	b.WriteString(`<table class="data">`)
	b.WriteString(`<tr><th>№</th><th>Наименование работ</th><th>Ед.изм.</th><th>Кол-во по смете</th><th>Кол-во за период</th><th>Цена ед.</th><th>Стоимость</th><th>Примечание</th></tr>`)

	rows, err := h.db.Query(`
		SELECT sort_order, name, uom, COALESCE(qty_smeta, 0), quantity, unit_rate, total_amount, COALESCE(note, '')
		FROM construction_act_line WHERE act_id = $1 ORDER BY sort_order
	`, actID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var sortOrder int
			var lName, lUOM, lNote string
			var qtySmeta, qty, unitRate, lineTotal float64
			if rows.Scan(&sortOrder, &lName, &lUOM, &qtySmeta, &qty, &unitRate, &lineTotal, &lNote) == nil {
				b.WriteString(fmt.Sprintf(`<tr><td class="center">%d</td><td>%s</td><td class="center">%s</td><td class="num">%.2f</td><td class="num">%.2f</td><td class="num">%.2f</td><td class="num">%.2f</td><td>%s</td></tr>`,
					sortOrder, lName, lUOM, qtySmeta, qty, unitRate, lineTotal, lNote))
			}
		}
	}
	b.WriteString(`</table>`)

	// Totals
	b.WriteString(`<table class="totals">`)
	b.WriteString(fmt.Sprintf(`<tr><td>Итого:</td><td style="text-align:right">%.2f %s</td></tr>`, amountTotal, currency))
	b.WriteString(fmt.Sprintf(`<tr><td>НДС (%.0f%%):</td><td style="text-align:right">%.2f %s</td></tr>`, vatPct, vatAmount, currency))
	b.WriteString(fmt.Sprintf(`<tr><td><b>Всего с НДС:</b></td><td style="text-align:right"><b>%.2f %s</b></td></tr>`, totalWithVat, currency))
	b.WriteString(`</table>`)

	// Signatures
	b.WriteString(`<table class="signatures"><tr>`)
	b.WriteString(`<td><b>Подрядчик:</b><br><span class="sig-line">&nbsp;</span>`)
	if signedContractorAt.Valid {
		b.WriteString(fmt.Sprintf(`<br><span class="sig-date">Подписано: %s</span>`, signedContractorAt.Time.Format("02.01.2006")))
	}
	b.WriteString(`</td>`)
	b.WriteString(`<td><b>Заказчик:</b><br><span class="sig-line">&nbsp;</span>`)
	if signedClientAt.Valid {
		b.WriteString(fmt.Sprintf(`<br><span class="sig-date">Подписано: %s</span>`, signedClientAt.Time.Format("02.01.2006")))
	}
	b.WriteString(`</td>`)
	b.WriteString(`</tr></table>`)

	b.WriteString(`</body></html>`)
	return b.String()
}

// renderForma3HTML generates HTML for Forma 3 (KS-3) certificate
func (h *Handler) renderForma3HTML(actID int64, tenantID uuid.UUID, projectName, projectAddress, clientName string) string {
	var b strings.Builder

	var name string
	var subcontractName sql.NullString
	var periodFrom, periodTo sql.NullTime
	var amountTotal, vatPct, vatAmount, totalWithVat float64
	var cumulFromStart, cumulFromYearStart, cumulPrevPeriod float64
	var smrAmount, equipAmount, otherAmount float64

	h.db.QueryRow(`
		SELECT a.name, COALESCE(s.name, ''),
		       a.period_from, a.period_to, a.amount_total,
		       COALESCE(a.vat_pct, 12), COALESCE(a.vat_amount, 0), COALESCE(a.amount_total_with_vat, 0),
		       COALESCE(a.cumul_from_start, 0), COALESCE(a.cumul_from_year_start, 0), COALESCE(a.cumul_previous_period, 0),
		       COALESCE(a.smr_amount, 0), COALESCE(a.equipment_amount, 0), COALESCE(a.other_amount, 0)
		FROM construction_act a
		LEFT JOIN construction_subcontract s ON s.id = a.subcontract_id
		WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(
		&name, &subcontractName,
		&periodFrom, &periodTo, &amountTotal,
		&vatPct, &vatAmount, &totalWithVat,
		&cumulFromStart, &cumulFromYearStart, &cumulPrevPeriod,
		&smrAmount, &equipAmount, &otherAmount,
	)

	_ = name

	b.WriteString(fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>%s</style></head><body>`, pdfBaseCSS))
	b.WriteString(`<h1>СПРАВКА</h1>`)
	b.WriteString(`<h2>о стоимости выполненных работ и затрат (Форма 3)</h2>`)

	// Header
	b.WriteString(`<table class="header-info">`)
	b.WriteString(fmt.Sprintf(`<tr><td><b>Объект:</b></td><td>%s</td></tr>`, projectName))
	if projectAddress != "" {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Адрес:</b></td><td>%s</td></tr>`, projectAddress))
	}
	b.WriteString(fmt.Sprintf(`<tr><td><b>Заказчик:</b></td><td>%s</td></tr>`, clientName))
	if subcontractName.Valid && subcontractName.String != "" {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Подрядчик:</b></td><td>%s</td></tr>`, subcontractName.String))
	}
	if periodFrom.Valid && periodTo.Valid {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Период:</b></td><td>%s — %s</td></tr>`,
			periodFrom.Time.Format("02.01.2006"), periodTo.Time.Format("02.01.2006")))
	}
	b.WriteString(`</table>`)

	// Cumulative table
	b.WriteString(`<table class="data">`)
	b.WriteString(`<tr><th>Наименование</th><th>С начала строительства</th><th>С начала года</th><th>За отчётный период</th><th>За предыдущий период</th></tr>`)

	writeRow := func(label string, fromStart, fromYear, period, prev float64) {
		b.WriteString(fmt.Sprintf(`<tr><td>%s</td><td class="num">%.2f</td><td class="num">%.2f</td><td class="num">%.2f</td><td class="num">%.2f</td></tr>`,
			label, fromStart, fromYear, period, prev))
	}

	writeRow("Стоимость работ — всего", cumulFromStart, cumulFromYearStart, amountTotal, cumulPrevPeriod)
	writeRow("в т.ч. СМР", cumulFromStart, cumulFromYearStart, smrAmount, cumulPrevPeriod)
	writeRow("Оборудование", 0, 0, equipAmount, 0)
	writeRow("Прочие затраты", 0, 0, otherAmount, 0)

	// VAT row
	vatFromStart := cumulFromStart * vatPct / 100
	vatFromYear := cumulFromYearStart * vatPct / 100
	writeRow(fmt.Sprintf("НДС (%.0f%%)", vatPct), vatFromStart, vatFromYear, vatAmount, cumulPrevPeriod*vatPct/100)

	totalFromStart := cumulFromStart + vatFromStart
	totalFromYear := cumulFromYearStart + vatFromYear
	b.WriteString(fmt.Sprintf(`<tr><td><b>Итого с НДС</b></td><td class="num"><b>%.2f</b></td><td class="num"><b>%.2f</b></td><td class="num"><b>%.2f</b></td><td class="num"><b>%.2f</b></td></tr>`,
		totalFromStart, totalFromYear, totalWithVat, cumulPrevPeriod+cumulPrevPeriod*vatPct/100))

	b.WriteString(`</table>`)

	// Signatures
	b.WriteString(`<table class="signatures"><tr>`)
	b.WriteString(`<td><b>Подрядчик:</b><br><span class="sig-line">&nbsp;</span></td>`)
	b.WriteString(`<td><b>Заказчик:</b><br><span class="sig-line">&nbsp;</span></td>`)
	b.WriteString(`</tr></table>`)

	b.WriteString(`</body></html>`)
	return b.String()
}

// renderForma19HTML generates HTML for Forma 19 (Hidden Works Act)
func (h *Handler) renderForma19HTML(actID int64, tenantID uuid.UUID, projectName, projectAddress, clientName string) string {
	var b strings.Builder

	var name, notes string
	var locationAxes, drawingReference sql.NullString
	var worksStartDate, worksEndDate sql.NullTime
	var signedContractorAt, signedClientAt, signedDesignerAt, signedGasnAt sql.NullTime
	var stageName sql.NullString

	h.db.QueryRow(`
		SELECT a.name, COALESCE(a.notes, ''),
		       a.location_axes, a.drawing_reference,
		       a.works_start_date, a.works_end_date,
		       a.signed_contractor_at, a.signed_client_at, a.signed_designer_at, a.signed_gasn_at,
		       COALESCE(st.name, '')
		FROM construction_act a
		LEFT JOIN construction_stages st ON st.id = a.stage_id
		WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(
		&name, &notes,
		&locationAxes, &drawingReference,
		&worksStartDate, &worksEndDate,
		&signedContractorAt, &signedClientAt, &signedDesignerAt, &signedGasnAt,
		&stageName,
	)

	_ = name

	b.WriteString(fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>%s</style></head><body>`, pdfBaseCSS))
	b.WriteString(`<h1>АКТ</h1>`)
	b.WriteString(`<h2>освидетельствования скрытых работ (Форма 19)</h2>`)

	// Header
	b.WriteString(`<table class="header-info">`)
	b.WriteString(fmt.Sprintf(`<tr><td><b>Объект:</b></td><td>%s</td></tr>`, projectName))
	if projectAddress != "" {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Адрес:</b></td><td>%s</td></tr>`, projectAddress))
	}
	if stageName.Valid && stageName.String != "" {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Этап:</b></td><td>%s</td></tr>`, stageName.String))
	}
	if locationAxes.Valid && locationAxes.String != "" {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Оси, отметки:</b></td><td>%s</td></tr>`, locationAxes.String))
	}
	if drawingReference.Valid && drawingReference.String != "" {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Ссылка на чертёж:</b></td><td>%s</td></tr>`, drawingReference.String))
	}
	if worksStartDate.Valid && worksEndDate.Valid {
		b.WriteString(fmt.Sprintf(`<tr><td><b>Период работ:</b></td><td>%s — %s</td></tr>`,
			worksStartDate.Time.Format("02.01.2006"), worksEndDate.Time.Format("02.01.2006")))
	}
	b.WriteString(`</table>`)

	// Work description
	b.WriteString(`<h3 style="margin-top:15px">Описание скрытых работ</h3>`)
	b.WriteString(fmt.Sprintf(`<p>%s</p>`, notes))

	// Materials
	var materialsRaw []byte
	h.db.QueryRow(`SELECT COALESCE(materials_json::text, '[]')::bytea FROM construction_act WHERE id = $1`, actID).Scan(&materialsRaw)
	type matItem struct {
		Name           string `json:"name"`
		CertificateURL string `json:"certificate_url"`
	}
	var materials []matItem
	if err := json.Unmarshal(materialsRaw, &materials); err == nil && len(materials) > 0 {
		b.WriteString(`<h3>Применённые материалы</h3>`)
		b.WriteString(`<table class="data"><tr><th>№</th><th>Наименование</th><th>Сертификат</th></tr>`)
		for i, m := range materials {
			cert := "—"
			if m.CertificateURL != "" {
				cert = "Прилагается"
			}
			b.WriteString(fmt.Sprintf(`<tr><td class="center">%d</td><td>%s</td><td class="center">%s</td></tr>`, i+1, m.Name, cert))
		}
		b.WriteString(`</table>`)
	}

	// 4 Signatures
	b.WriteString(`<table class="signatures">`)
	writeSig := func(title string, signedAt sql.NullTime) {
		b.WriteString(fmt.Sprintf(`<td><b>%s:</b><br><span class="sig-line">&nbsp;</span>`, title))
		if signedAt.Valid {
			b.WriteString(fmt.Sprintf(`<br><span class="sig-date">Подписано: %s</span>`, signedAt.Time.Format("02.01.2006")))
		}
		b.WriteString(`</td>`)
	}
	b.WriteString(`<tr>`)
	writeSig("Подрядчик", signedContractorAt)
	writeSig("Заказчик / Технадзор", signedClientAt)
	b.WriteString(`</tr><tr>`)
	writeSig("Проектировщик", signedDesignerAt)
	writeSig("Инспектор ГАСН", signedGasnAt)
	b.WriteString(`</tr></table>`)

	b.WriteString(fmt.Sprintf(`<p style="font-size:9pt;color:#666;margin-top:30px">Документ сформирован: %s</p>`, time.Now().Format("02.01.2006 15:04")))
	b.WriteString(`</body></html>`)
	return b.String()
}
