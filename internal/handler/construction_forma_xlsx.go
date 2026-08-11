package handler

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

// =====================================================
// CONSTRUCTION ACT XLSX GENERATORS (Forma 2 / Forma 3)
// Match the industry reference layout (сиеб булдингс / форма 3).
// All generation is server-side via excelize.
// =====================================================

// --- shared helpers --------------------------------------------------------

const xlsxFont = "Times New Roman"

// numberToWordsRU converts an integer (sum in Uzbek som) to its Russian
// textual representation, matching the Uzbekistan construction industry
// convention used in Forma 3 ("Итого к оплате: 173 666 813 (Сто семьдесят …) сум").
// Supports values up to 999 999 999 999.
func numberToWordsRU(n int64) string {
	if n == 0 {
		return "ноль"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}

	units1 := []string{"", "один", "два", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"}
	units1f := []string{"", "одна", "две", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"}
	teens := []string{"десять", "одиннадцать", "двенадцать", "тринадцать", "четырнадцать", "пятнадцать", "шестнадцать", "семнадцать", "восемнадцать", "девятнадцать"}
	tens := []string{"", "", "двадцать", "тридцать", "сорок", "пятьдесят", "шестьдесят", "семьдесят", "восемьдесят", "девяносто"}
	hundreds := []string{"", "сто", "двести", "триста", "четыреста", "пятьсот", "шестьсот", "семьсот", "восемьсот", "девятьсот"}

	triple := func(num int, feminine bool) string {
		var parts []string
		h := num / 100
		rem := num % 100
		t := rem / 10
		u := rem % 10
		if h > 0 {
			parts = append(parts, hundreds[h])
		}
		if t == 1 {
			parts = append(parts, teens[u])
		} else {
			if t > 0 {
				parts = append(parts, tens[t])
			}
			if u > 0 {
				if feminine {
					parts = append(parts, units1f[u])
				} else {
					parts = append(parts, units1[u])
				}
			}
		}
		return strings.Join(parts, " ")
	}

	plural := func(num int, forms [3]string) string {
		mod100 := num % 100
		mod10 := num % 10
		if mod100 >= 11 && mod100 <= 14 {
			return forms[2]
		}
		switch mod10 {
		case 1:
			return forms[0]
		case 2, 3, 4:
			return forms[1]
		default:
			return forms[2]
		}
	}

	billions := int(n / 1_000_000_000)
	millions := int((n / 1_000_000) % 1000)
	thousands := int((n / 1000) % 1000)
	ones := int(n % 1000)

	var out []string
	if billions > 0 {
		out = append(out, triple(billions, false), plural(billions, [3]string{"миллиард", "миллиарда", "миллиардов"}))
	}
	if millions > 0 {
		out = append(out, triple(millions, false), plural(millions, [3]string{"миллион", "миллиона", "миллионов"}))
	}
	if thousands > 0 {
		out = append(out, triple(thousands, true), plural(thousands, [3]string{"тысяча", "тысячи", "тысяч"}))
	}
	if ones > 0 {
		out = append(out, triple(ones, false))
	}

	result := strings.Join(out, " ")
	result = strings.TrimSpace(strings.Join(strings.Fields(result), " "))
	// Capitalize first letter
	if len(result) > 0 {
		runes := []rune(result)
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		result = string(runes)
	}
	if negative {
		return "минус " + result
	}
	return result
}

// formatWithSpaces formats an integer with Russian-style thousand separators
// (non-breaking spaces avoided in favour of regular spaces for xlsx compatibility).
func formatWithSpaces(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, " ")
	if neg {
		out = "-" + out
	}
	return out
}

// monthNameRU returns Russian month name (lowercase) for months 1..12.
func monthNameRU(m int) string {
	names := []string{"", "январь", "февраль", "март", "апрель", "май", "июнь",
		"июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"}
	if m < 1 || m > 12 {
		return ""
	}
	return names[m]
}

// --- Data struct types ------------------------------------------------------

type forma2Party struct {
	Name       string
	Address    string
	Phone      string
	Bank       string
	Account    string
	MFO        string
	STIR       string
	OKONH      string
	Director   string
	Accountant string
}

type forma2Line struct {
	Display      string // "1", "1.1"
	NormCode     string
	Name         string
	UOM          string
	QtyPlan      float64
	QtyPeriod    float64
	UnitPrice    float64
	Total        float64
	Labor        float64
	Equipment    float64
	Materials    float64
	Cables       float64
	IsSection    bool // РАЗДЕЛ divider row
	SectionTitle string
}

type forma2Data struct {
	Client         forma2Party
	Contractor     forma2Party
	ProjectName    string
	ActNumber      string
	PeriodLabel    string // "за январь 2026года"
	Lines          []forma2Line
	LaborTotal     float64
	EquipTotal     float64
	MaterialsTotal float64
	TransportPct   float64
	OtherPct       float64
	CablesTotal    float64
	ReturnAmount   float64
}

type forma3Data struct {
	Client                     forma2Party
	Contractor                 forma2Party
	ProjectName                string
	CertNumber                 string
	CertDate                   time.Time
	PeriodMonthFromName        string
	PeriodMonthToName          string
	ContractNumber             string
	ContractDate               time.Time
	TotalContractValueThousand float64 // in тыс. сум
	Rows                       []forma3Row
	TotalWithoutVAT            float64 // тыс. сум
}

type forma3Row struct {
	No                int
	Name              string
	UOM               string
	VolumeTotal       float64
	VolumeCurrentYear float64
	ValueTotal        float64
	ValueCurrentYear  float64
	DoneFromStartQty  float64
	DoneFromStartPct  float64
	DoneFromStartVal  float64
	DoneFromYearQty   float64
	DoneFromYearPct   float64
	DoneFromYearVal   float64
	DoneThisPeriodQty float64
	DoneThisPeriodPct float64
	DoneThisPeriodVal float64
}

// --- Data loading -----------------------------------------------------------

func (h *Handler) loadForma2Data(actID int64, tenantID uuid.UUID) (*forma2Data, error) {
	d := &forma2Data{}

	var projectID int64
	var periodFrom, periodTo sql.NullTime
	var actNumber sql.NullInt64
	var subcontractID sql.NullInt64
	var laborTotal, equipTotal, materialsTotal, cablesTotal sql.NullFloat64
	var transportPct, otherPct sql.NullFloat64
	var returnAmount sql.NullFloat64

	err := h.db.QueryRow(`
		SELECT a.project_id, a.subcontract_id, a.act_number, a.period_from, a.period_to,
		       COALESCE(a.f2_labor_total, 0),
		       COALESCE(a.f2_equipment_total, 0),
		       COALESCE(a.f2_materials_total, 0),
		       COALESCE(a.f2_cables_total, 0),
		       COALESCE(a.f2_transport_pct, 5),
		       COALESCE(a.f2_other_pct, 17),
		       COALESCE(a.f2_materials_returned, 0)
		FROM construction_act a
		WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(
		&projectID, &subcontractID, &actNumber, &periodFrom, &periodTo,
		&laborTotal, &equipTotal, &materialsTotal, &cablesTotal,
		&transportPct, &otherPct, &returnAmount,
	)
	if err != nil {
		return nil, err
	}

	// Project / client
	_ = h.db.QueryRow(`
		SELECT COALESCE(name, ''), COALESCE(object_full_name, name),
		       COALESCE(client_name, ''), COALESCE(client_address, address),
		       COALESCE(client_phone, ''), COALESCE(client_bank_name, ''),
		       COALESCE(client_bank_account, ''), COALESCE(client_mfo, ''),
		       COALESCE(client_stir, ''), COALESCE(client_okonh, ''),
		       COALESCE(client_director_name, ''), COALESCE(client_chief_accountant_name, '')
		FROM construction_projects WHERE id = $1
	`, projectID).Scan(
		&d.ProjectName, &d.ProjectName,
		&d.Client.Name, &d.Client.Address, &d.Client.Phone,
		&d.Client.Bank, &d.Client.Account, &d.Client.MFO,
		&d.Client.STIR, &d.Client.OKONH,
		&d.Client.Director, &d.Client.Accountant,
	)

	// Contractor
	if subcontractID.Valid {
		_ = h.db.QueryRow(`
			SELECT COALESCE(name, ''), COALESCE(address, ''), COALESCE(phone, ''),
			       COALESCE(bank_name, ''), COALESCE(bank_account, ''),
			       COALESCE(mfo, ''), COALESCE(stir, ''), COALESCE(okonh, ''),
			       COALESCE(director_name, ''), COALESCE(chief_accountant_name, '')
			FROM construction_subcontract WHERE id = $1
		`, subcontractID.Int64).Scan(
			&d.Contractor.Name, &d.Contractor.Address, &d.Contractor.Phone,
			&d.Contractor.Bank, &d.Contractor.Account,
			&d.Contractor.MFO, &d.Contractor.STIR, &d.Contractor.OKONH,
			&d.Contractor.Director, &d.Contractor.Accountant,
		)
	}

	// Act number + period label
	if actNumber.Valid {
		d.ActNumber = fmt.Sprintf("%d", actNumber.Int64)
	}
	if periodFrom.Valid && periodTo.Valid {
		from := periodFrom.Time
		d.PeriodLabel = fmt.Sprintf("за %s %d года",
			monthNameRU(int(from.Month())), from.Year())
	}

	// Lines
	rows, err := h.db.Query(`
		SELECT COALESCE(line_number_display, ''), COALESCE(norm_code, ''),
		       COALESCE(is_section_header, FALSE), COALESCE(section_name, ''),
		       COALESCE(name, ''), COALESCE(uom, ''),
		       COALESCE(qty_smeta, 0), COALESCE(quantity, 0),
		       COALESCE(unit_rate, 0), COALESCE(total_amount, 0),
		       COALESCE(labor_amount, 0), COALESCE(equipment_amount, 0),
		       COALESCE(materials_amount, 0), COALESCE(cables_amount, 0)
		FROM construction_act_line
		WHERE act_id = $1
		ORDER BY sort_order, id
	`, actID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var l forma2Line
			_ = rows.Scan(&l.Display, &l.NormCode, &l.IsSection, &l.SectionTitle,
				&l.Name, &l.UOM, &l.QtyPlan, &l.QtyPeriod,
				&l.UnitPrice, &l.Total,
				&l.Labor, &l.Equipment, &l.Materials, &l.Cables)
			d.Lines = append(d.Lines, l)
		}
	}

	d.LaborTotal = laborTotal.Float64
	d.EquipTotal = equipTotal.Float64
	d.MaterialsTotal = materialsTotal.Float64
	d.CablesTotal = cablesTotal.Float64
	d.TransportPct = transportPct.Float64
	d.OtherPct = otherPct.Float64
	d.ReturnAmount = returnAmount.Float64

	// Fallback: compute totals from lines if act snapshot is empty.
	if d.LaborTotal == 0 && d.EquipTotal == 0 && d.MaterialsTotal == 0 {
		for _, l := range d.Lines {
			d.LaborTotal += l.Labor
			d.EquipTotal += l.Equipment
			d.MaterialsTotal += l.Materials
			d.CablesTotal += l.Cables
		}
	}

	return d, nil
}

func (h *Handler) loadForma3Data(actID int64, tenantID uuid.UUID) (*forma3Data, error) {
	d := &forma3Data{}

	var projectID int64
	var subcontractID sql.NullInt64
	var periodFrom, periodTo sql.NullTime
	var periodMonthFrom, periodMonthTo sql.NullInt16
	var actNumber sql.NullInt64
	var amountTotal sql.NullFloat64
	var createdDate sql.NullTime

	err := h.db.QueryRow(`
		SELECT a.project_id, a.subcontract_id, a.act_number,
		       a.period_from, a.period_to,
		       a.period_month_from, a.period_month_to,
		       COALESCE(a.amount_total, 0),
		       a.created_date
		FROM construction_act a
		WHERE a.id = $1 AND a.tenant_id = $2
	`, actID, tenantID).Scan(
		&projectID, &subcontractID, &actNumber,
		&periodFrom, &periodTo,
		&periodMonthFrom, &periodMonthTo,
		&amountTotal, &createdDate,
	)
	if err != nil {
		return nil, err
	}

	// Client / project
	var objectName string
	var contractNumber sql.NullString
	var contractDate sql.NullTime
	var contractAmount sql.NullFloat64
	_ = h.db.QueryRow(`
		SELECT COALESCE(object_full_name, name, ''),
		       COALESCE(client_name, ''), COALESCE(client_address, address),
		       COALESCE(client_phone, ''), COALESCE(client_bank_name, ''),
		       COALESCE(client_bank_account, ''), COALESCE(client_mfo, ''),
		       COALESCE(client_stir, ''), COALESCE(client_okonh, ''),
		       COALESCE(client_director_name, ''), COALESCE(client_chief_accountant_name, ''),
		       contract_number, contract_date, contract_amount
		FROM construction_projects WHERE id = $1
	`, projectID).Scan(
		&objectName,
		&d.Client.Name, &d.Client.Address, &d.Client.Phone,
		&d.Client.Bank, &d.Client.Account, &d.Client.MFO,
		&d.Client.STIR, &d.Client.OKONH,
		&d.Client.Director, &d.Client.Accountant,
		&contractNumber, &contractDate, &contractAmount,
	)
	d.ProjectName = objectName
	if contractNumber.Valid {
		d.ContractNumber = contractNumber.String
	}
	if contractDate.Valid {
		d.ContractDate = contractDate.Time
	}
	if contractAmount.Valid {
		d.TotalContractValueThousand = contractAmount.Float64 / 1000.0
	}

	// Contractor
	if subcontractID.Valid {
		_ = h.db.QueryRow(`
			SELECT COALESCE(name, ''), COALESCE(address, ''), COALESCE(phone, ''),
			       COALESCE(bank_name, ''), COALESCE(bank_account, ''),
			       COALESCE(mfo, ''), COALESCE(stir, ''), COALESCE(okonh, ''),
			       COALESCE(director_name, ''), COALESCE(chief_accountant_name, '')
			FROM construction_subcontract WHERE id = $1
		`, subcontractID.Int64).Scan(
			&d.Contractor.Name, &d.Contractor.Address, &d.Contractor.Phone,
			&d.Contractor.Bank, &d.Contractor.Account,
			&d.Contractor.MFO, &d.Contractor.STIR, &d.Contractor.OKONH,
			&d.Contractor.Director, &d.Contractor.Accountant,
		)
	}

	// Metadata
	if actNumber.Valid {
		d.CertNumber = fmt.Sprintf("%d", actNumber.Int64)
	}
	if createdDate.Valid {
		d.CertDate = createdDate.Time
	} else {
		d.CertDate = time.Now()
	}
	if periodMonthFrom.Valid {
		d.PeriodMonthFromName = monthNameRU(int(periodMonthFrom.Int16))
	} else if periodFrom.Valid {
		d.PeriodMonthFromName = monthNameRU(int(periodFrom.Time.Month()))
	}
	if periodMonthTo.Valid {
		d.PeriodMonthToName = monthNameRU(int(periodMonthTo.Int16))
	} else if periodTo.Valid {
		d.PeriodMonthToName = monthNameRU(int(periodTo.Time.Month()))
	}

	// Single aggregate row (one Forma 3 is typically a single work-package line).
	// Convert to тыс. сум (thousands).
	valThousand := amountTotal.Float64 / 1000.0
	d.Rows = append(d.Rows, forma3Row{
		No:                1,
		Name:              d.ProjectName,
		UOM:               "",
		VolumeTotal:       valThousand,
		VolumeCurrentYear: valThousand,
		ValueTotal:        valThousand,
		ValueCurrentYear:  valThousand,
		DoneFromStartVal:  valThousand,
		DoneFromYearVal:   valThousand,
		DoneThisPeriodVal: valThousand,
	})
	d.TotalWithoutVAT = valThousand

	return d, nil
}

// --- Styling helpers --------------------------------------------------------

func addBorderedStyle(f *excelize.File, bold bool, hAlign, vAlign string, wrap bool, bgColor string, numFmt string) (int, error) {
	style := &excelize.Style{
		Font: &excelize.Font{Family: xlsxFont, Size: 10, Bold: bold},
		Alignment: &excelize.Alignment{
			Horizontal: hAlign, Vertical: vAlign, WrapText: wrap,
		},
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
	}
	if bgColor != "" {
		style.Fill = excelize.Fill{Type: "pattern", Color: []string{bgColor}, Pattern: 1}
	}
	if numFmt != "" {
		// Custom number format
		cfmt := numFmt
		style.CustomNumFmt = &cfmt
	}
	return f.NewStyle(style)
}

// --- Forma 2 XLSX generator -------------------------------------------------

// GenerateForma2XLSXBytes renders Forma 2 (KS-2) act as .xlsx bytes.
func (h *Handler) GenerateForma2XLSXBytes(actID int64, tenantID uuid.UUID) ([]byte, error) {
	d, err := h.loadForma2Data(actID, tenantID)
	if err != nil {
		return nil, err
	}

	f := excelize.NewFile()
	defer f.Close()
	sheet := "Акт"
	idx, _ := f.NewSheet(sheet)
	f.SetActiveSheet(idx)
	_ = f.DeleteSheet("Sheet1")

	// Column widths (12 columns, A..L)
	widths := map[string]float64{
		"A": 5, "B": 18, "C": 45, "D": 9, "E": 10, "F": 10,
		"G": 13, "H": 13, "I": 13, "J": 13, "K": 13, "L": 13,
	}
	for col, w := range widths {
		_ = f.SetColWidth(sheet, col, col, w)
	}

	hdr, _ := addBorderedStyle(f, true, "center", "center", true, "", "")
	plain, _ := addBorderedStyle(f, false, "left", "top", true, "", "")
	num, _ := addBorderedStyle(f, false, "right", "center", false, "", "#,##0.00")
	numBold, _ := addBorderedStyle(f, true, "right", "center", false, "", "#,##0.00")
	centerCell, _ := addBorderedStyle(f, false, "center", "center", true, "", "")
	totalRow, _ := addBorderedStyle(f, true, "left", "center", true, "FDE9D9", "")
	totalNum, _ := addBorderedStyle(f, true, "right", "center", false, "FDE9D9", "#,##0.00")
	sectionStyle, _ := addBorderedStyle(f, true, "center", "center", true, "D9E1F2", "")
	title, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 12, Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	plainBare, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"},
	})
	plainBoldBare, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 10, Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"},
	})

	// Header block: parties
	_ = f.SetCellValue(sheet, "A1", "ЗАКАЗЧИК:")
	_ = f.SetCellStyle(sheet, "A1", "A1", plainBoldBare)
	_ = f.MergeCell(sheet, "C1", "L1")
	_ = f.SetCellValue(sheet, "C1", d.Client.Name)
	_ = f.SetCellStyle(sheet, "C1", "L1", plainBoldBare)

	_ = f.SetCellValue(sheet, "A2", "ПОДРЯДЧИК:")
	_ = f.SetCellStyle(sheet, "A2", "A2", plainBoldBare)
	_ = f.MergeCell(sheet, "C2", "L2")
	_ = f.SetCellValue(sheet, "C2", d.Contractor.Name)
	_ = f.SetCellStyle(sheet, "C2", "L2", plainBoldBare)

	// Title
	_ = f.MergeCell(sheet, "A3", "L3")
	_ = f.SetCellValue(sheet, "A3", fmt.Sprintf("АКТ №%s", d.ActNumber))
	_ = f.SetCellStyle(sheet, "A3", "A3", title)
	_ = f.MergeCell(sheet, "A4", "L4")
	_ = f.SetCellValue(sheet, "A4", "выполненных работ")
	_ = f.SetCellStyle(sheet, "A4", "A4", title)
	_ = f.MergeCell(sheet, "A5", "L5")
	_ = f.SetCellValue(sheet, "A5", fmt.Sprintf("ПО ОБЪЕКТУ: %s", d.ProjectName))
	_ = f.SetCellStyle(sheet, "A5", "A5", title)

	if d.PeriodLabel != "" {
		_ = f.MergeCell(sheet, "A6", "L6")
		_ = f.SetCellValue(sheet, "A6", d.PeriodLabel)
		_ = f.SetCellStyle(sheet, "A6", "A6", plainBare)
	}

	// Table headers (rows 9-10 double-level header)
	headerRow := 9
	// Row 9: top-level
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", headerRow), "Т/р")
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", headerRow), "Меъёрлар рақами (шифри)")
	_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", headerRow), "Иш турлари")
	_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", headerRow), "Ўлчов бирлиги")
	_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", headerRow), "Лойиха кўрсаткичлари бўйича ишлари миқдори")
	_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", headerRow), "Давр бўйича миқдор")
	_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", headerRow), "Қиймати")
	// Merge top-level header for "Қиймати" G..L
	_ = f.MergeCell(sheet, fmt.Sprintf("G%d", headerRow), fmt.Sprintf("L%d", headerRow))
	// For single-row top headers merge vertically with row 10
	for _, c := range []string{"A", "B", "C", "D", "E", "F"} {
		_ = f.MergeCell(sheet, fmt.Sprintf("%s%d", c, headerRow), fmt.Sprintf("%s%d", c, headerRow+1))
	}
	// Row 10: second-level under Қиймати
	_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", headerRow+1), "БИРЛИККА")
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", headerRow+1), "ЖАМИ")
	_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", headerRow+1), "ЗАРПЛАТА")
	_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", headerRow+1), "ЭММ")
	_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", headerRow+1), "МАТЕРИАЛЫ")
	_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", headerRow+1), "КАБЕЛИ")
	for _, c := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L"} {
		_ = f.SetCellStyle(sheet, fmt.Sprintf("%s%d", c, headerRow), fmt.Sprintf("%s%d", c, headerRow+1), hdr)
	}
	_ = f.SetRowHeight(sheet, headerRow, 30)
	_ = f.SetRowHeight(sheet, headerRow+1, 22)

	// Column-number row (12 = columns 1..12)
	numRow := headerRow + 2
	for i, label := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"} {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, fmt.Sprintf("%s%d", col, numRow), label)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("%s%d", col, numRow), fmt.Sprintf("%s%d", col, numRow), centerCell)
	}

	// Data rows
	row := numRow + 1
	for _, l := range d.Lines {
		if l.IsSection {
			_ = f.MergeCell(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("L%d", row))
			_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), l.SectionTitle)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("L%d", row), sectionStyle)
			row++
			continue
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), l.Display)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), l.NormCode)
		_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), l.Name)
		_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", row), l.UOM)
		_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", row), l.QtyPlan)
		_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", row), l.QtyPeriod)
		_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", row), l.UnitPrice)
		_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), l.Total)
		if l.Labor > 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", row), l.Labor)
		}
		if l.Equipment > 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", row), l.Equipment)
		}
		if l.Materials > 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", row), l.Materials)
		}
		if l.Cables > 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", row), l.Cables)
		}
		_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), centerCell)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), centerCell)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), plain)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("D%d", row), centerCell)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("E%d", row), fmt.Sprintf("L%d", row), num)
		row++
	}

	// Totals block (match reference structure)
	writeTotalRow := func(label string, amount float64, bold bool) {
		_ = f.MergeCell(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("G%d", row))
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), label)
		_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), amount)
		if bold {
			_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("G%d", row), totalRow)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("L%d", row), totalNum)
		} else {
			_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("G%d", row), plain)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("H%d", row), numBold)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("I%d", row), fmt.Sprintf("L%d", row), num)
		}
		row++
	}

	baseTotal := d.LaborTotal + d.EquipTotal + d.MaterialsTotal
	transport := d.MaterialsTotal * d.TransportPct / 100.0
	subtotal1 := baseTotal + transport
	subtotal2 := subtotal1 + d.CablesTotal
	other := subtotal2 * d.OtherPct / 100.0
	grand := subtotal2 + other
	contractor := grand - d.ReturnAmount

	writeTotalRow("ИТОГО", baseTotal, true)
	writeTotalRow("В ТОМ ЧИСЛЕ: ЗАРПЛАТА", d.LaborTotal, false)
	writeTotalRow("ЭММ", d.EquipTotal, false)
	writeTotalRow("МАТЕРИАЛЫ", d.MaterialsTotal, false)
	writeTotalRow(fmt.Sprintf("ТРАНСПОРТ МАТЕРИАЛОВ %.0f%%", d.TransportPct), transport, false)
	writeTotalRow("ИТОГО", subtotal1, true)
	writeTotalRow("КАБЕЛЬНО-ПРОВОДНИКОВЫЕ МАТЕРИАЛЫ", d.CablesTotal, false)
	writeTotalRow("ИТОГО", subtotal2, true)
	writeTotalRow(fmt.Sprintf("ПРОЧИЕ ЗАТРАТЫ %.0f%%", d.OtherPct), other, false)
	writeTotalRow("ИТОГО", grand, true)
	if d.ReturnAmount != 0 {
		writeTotalRow("ВОЗВРАТ МАТЕРИАЛОВ ЗАКАЗЧИКА", d.ReturnAmount, false)
	}
	writeTotalRow("ИТОГО ВЫПОЛНЕНИЕ ПОДРЯДЧИКА", contractor, true)

	// Signatures
	row += 2
	_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), "ЗАКАЗЧИК:")
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), "ПОДРЯДЧИК:")
	_ = f.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), plainBoldBare)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("H%d", row), plainBoldBare)
	row++
	_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("Директор %s", d.Client.Name))
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("Директор %s", d.Contractor.Name))
	_ = f.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), plainBare)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("H%d", row), plainBare)
	row += 2
	if d.Client.Director != "" {
		_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("__________ %s", d.Client.Director))
	} else {
		_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), "__________________________")
	}
	if d.Contractor.Director != "" {
		_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("__________ %s", d.Contractor.Director))
	} else {
		_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), "__________________________")
	}
	_ = f.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), plainBare)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("H%d", row), plainBare)

	// Page setup
	_ = f.SetPageLayout(sheet, &excelize.PageLayoutOptions{
		Orientation: func() *string { s := "landscape"; return &s }(),
	})

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- Forma 3 XLSX generator -------------------------------------------------

// GenerateForma3XLSXBytes renders Forma 3 (KS-3) certificate as .xlsx bytes.
func (h *Handler) GenerateForma3XLSXBytes(actID int64, tenantID uuid.UUID) ([]byte, error) {
	d, err := h.loadForma3Data(actID, tenantID)
	if err != nil {
		return nil, err
	}

	f := excelize.NewFile()
	defer f.Close()
	sheet := "Форма 3"
	idx, _ := f.NewSheet(sheet)
	f.SetActiveSheet(idx)
	_ = f.DeleteSheet("Sheet1")

	// 16 columns A..P
	for i := 1; i <= 16; i++ {
		col, _ := excelize.ColumnNumberToName(i)
		w := 10.0
		switch col {
		case "B":
			w = 22
		case "C":
			w = 8
		}
		_ = f.SetColWidth(sheet, col, col, w)
	}

	centered := &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true}
	leftWrap := &excelize.Alignment{Horizontal: "left", Vertical: "center", WrapText: true}

	boldCenter, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 12, Bold: true},
		Alignment: centered,
	})
	plainCenter, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 10},
		Alignment: centered,
	})
	plainLeft, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 10},
		Alignment: leftWrap,
	})
	boldLeft, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 10, Bold: true},
		Alignment: leftWrap,
	})
	borderHdr, _ := addBorderedStyle(f, true, "center", "center", true, "", "")
	borderNum, _ := addBorderedStyle(f, false, "right", "center", false, "", "#,##0.00")
	borderPlain, _ := addBorderedStyle(f, false, "left", "center", true, "", "")
	borderNumBold, _ := addBorderedStyle(f, true, "right", "center", false, "FDE9D9", "#,##0.00")
	borderTotalLabel, _ := addBorderedStyle(f, true, "left", "center", true, "FDE9D9", "")

	// Title
	_ = f.MergeCell(sheet, "B2", "P2")
	_ = f.SetCellValue(sheet, "B2", "СПРАВКА-СЧЕТ-ФАКТУРА")
	_ = f.SetCellStyle(sheet, "B2", "B2", boldCenter)
	_ = f.MergeCell(sheet, "B3", "P3")
	_ = f.SetCellValue(sheet, "B3", "О СТОИМОСТИ ВЫПОЛНЕННЫХ РАБОТ (ПОНЕСЕННЫХ ЗАТРАТ)")
	_ = f.SetCellStyle(sheet, "B3", "B3", boldCenter)

	// № / дата / период row
	_ = f.SetCellValue(sheet, "D4", "№")
	_ = f.SetCellValue(sheet, "F4", "Дата составления")
	_ = f.MergeCell(sheet, "H4", "L4")
	_ = f.SetCellValue(sheet, "H4", "Отчетный период")
	_ = f.SetCellStyle(sheet, "D4", "L4", borderHdr)
	_ = f.SetCellValue(sheet, "H5", "с")
	_ = f.SetCellValue(sheet, "K5", "по")
	_ = f.SetCellStyle(sheet, "H5", "L5", borderHdr)
	_ = f.SetCellValue(sheet, "D6", d.CertNumber)
	_ = f.SetCellValue(sheet, "F6", d.CertDate.Format("02.01.2006")+"г.")
	_ = f.SetCellValue(sheet, "H6", d.PeriodMonthFromName)
	_ = f.SetCellValue(sheet, "K6", d.PeriodMonthToName)
	_ = f.SetCellStyle(sheet, "D6", "L6", borderPlain)

	// Parties block
	party := func(row int, leftLabel, leftVal, rightLabel, rightVal string) {
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), leftLabel)
		_ = f.MergeCell(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("J%d", row))
		_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", row), leftVal)
		_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", row), rightLabel)
		_ = f.MergeCell(sheet, fmt.Sprintf("M%d", row), fmt.Sprintf("P%d", row))
		_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", row), rightVal)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), boldLeft)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("J%d", row), plainLeft)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("K%d", row), fmt.Sprintf("K%d", row), boldLeft)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("M%d", row), fmt.Sprintf("P%d", row), plainLeft)
	}
	party(7, "Заказчик", d.Client.Name, "Подрядчик:", d.Contractor.Name)
	party(8, "Адрес:", d.Client.Address, "Адрес:", d.Contractor.Address)
	party(9, "Телефон:", d.Client.Phone, "Телефон:", d.Contractor.Phone)
	party(10, "Банк:", d.Client.Bank, "Банк:", d.Contractor.Bank)
	party(11, "Расчетный счет:", d.Client.Account, "Расчетный счет:", d.Contractor.Account)
	party(12, "МФО:", d.Client.MFO, "СТИР:", d.Contractor.STIR)
	party(13, "СТИР:", d.Client.STIR, "КОД ПО ОКОНХ", d.Contractor.OKONH)
	party(14, "КОД по ОКОНХ", d.Client.OKONH, "МФО", d.Contractor.MFO)

	// Object + contract
	_ = f.SetCellValue(sheet, "B15", "Наименование объекта и его адрес:")
	_ = f.MergeCell(sheet, "D15", "P15")
	_ = f.SetCellValue(sheet, "D15", d.ProjectName)
	_ = f.SetCellStyle(sheet, "B15", "B15", boldLeft)
	_ = f.SetCellStyle(sheet, "D15", "P15", plainLeft)

	contractLine := "Договор: "
	if d.ContractNumber != "" {
		contractLine += "№ " + d.ContractNumber
	}
	if !d.ContractDate.IsZero() {
		contractLine += " от " + d.ContractDate.Format("02.01.2006") + " г."
	}
	_ = f.MergeCell(sheet, "B16", "G16")
	_ = f.SetCellValue(sheet, "B16", contractLine)
	_ = f.SetCellStyle(sheet, "B16", "G16", boldLeft)
	_ = f.MergeCell(sheet, "H16", "L17")
	_ = f.SetCellValue(sheet, "H16", "Общая стоимость в договорных\nтекущих ценах ( тыс. сум)")
	_ = f.SetCellStyle(sheet, "H16", "L17", borderHdr)
	_ = f.MergeCell(sheet, "M16", "P17")
	_ = f.SetCellValue(sheet, "M16", d.TotalContractValueThousand)
	_ = f.SetCellStyle(sheet, "M16", "P17", borderNumBold)

	// Main table — multi-row header
	// Row 18 (top), row 19 (2nd), row 20 (3rd)
	// A=№№ п/п, B=Наименование, C=Ед. изм, D-E=Объем работ (Всего / в т.ч. на текущий год)
	// F-G=Стоим. дог. тек. ценах (Всего / в т.ч. на текущий год)
	// H-P=Выполненные работы: с начала строит (H,I,J) | с начала года (K,L,M) | за отчётный месяц (N,O,P)
	h18 := 18
	_ = f.MergeCell(sheet, fmt.Sprintf("A%d", h18), fmt.Sprintf("A%d", h18+2))
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", h18), "№№ п/п")
	_ = f.MergeCell(sheet, fmt.Sprintf("B%d", h18), fmt.Sprintf("B%d", h18+2))
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", h18), "Наименование объектов, этапов, видов работ")
	_ = f.MergeCell(sheet, fmt.Sprintf("C%d", h18), fmt.Sprintf("C%d", h18+2))
	_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", h18), "Ед. изм")

	_ = f.MergeCell(sheet, fmt.Sprintf("D%d", h18), fmt.Sprintf("E%d", h18))
	_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", h18), "Объем работ в физич. показ.")
	_ = f.MergeCell(sheet, fmt.Sprintf("D%d", h18+1), fmt.Sprintf("D%d", h18+2))
	_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", h18+1), "Всего")
	_ = f.MergeCell(sheet, fmt.Sprintf("E%d", h18+1), fmt.Sprintf("E%d", h18+2))
	_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", h18+1), "в т.ч. на текущий год")

	_ = f.MergeCell(sheet, fmt.Sprintf("F%d", h18), fmt.Sprintf("G%d", h18))
	_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", h18), "Стоим. в догов. тек. ценах (тыс.сум)")
	_ = f.MergeCell(sheet, fmt.Sprintf("F%d", h18+1), fmt.Sprintf("F%d", h18+2))
	_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", h18+1), "Всего")
	_ = f.MergeCell(sheet, fmt.Sprintf("G%d", h18+1), fmt.Sprintf("G%d", h18+2))
	_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", h18+1), "в т.ч. на текущий год")

	_ = f.MergeCell(sheet, fmt.Sprintf("H%d", h18), fmt.Sprintf("P%d", h18))
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", h18), "Выполненные работы (понесенные затраты)")
	_ = f.MergeCell(sheet, fmt.Sprintf("H%d", h18+1), fmt.Sprintf("J%d", h18+1))
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", h18+1), "с начала строительства")
	_ = f.MergeCell(sheet, fmt.Sprintf("K%d", h18+1), fmt.Sprintf("M%d", h18+1))
	_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", h18+1), "с начала года")
	_ = f.MergeCell(sheet, fmt.Sprintf("N%d", h18+1), fmt.Sprintf("P%d", h18+1))
	_ = f.SetCellValue(sheet, fmt.Sprintf("N%d", h18+1), "в т.ч. за отчетный месяц")

	triples := map[string][3]string{
		"H": {"в физич. показ.", "в % к объему всего работ", "в дог. тек. ценах (тыс.сум)"},
		"K": {"в физич. показ.", "в % к объему всего работ", "в дог. тек. ценах (тыс.сум)"},
		"N": {"в физич. показ.", "в % к объему всего работ", "в дог. тек. ценах (тыс.сум)"},
	}
	triplesOrder := []string{"H", "K", "N"}
	for _, startCol := range triplesOrder {
		labels := triples[startCol]
		startIdx, _ := excelize.ColumnNameToNumber(startCol)
		for i, lbl := range labels {
			col, _ := excelize.ColumnNumberToName(startIdx + i)
			_ = f.SetCellValue(sheet, fmt.Sprintf("%s%d", col, h18+2), lbl)
		}
	}
	_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", h18), fmt.Sprintf("P%d", h18+2), borderHdr)
	_ = f.SetRowHeight(sheet, h18, 18)
	_ = f.SetRowHeight(sheet, h18+1, 22)
	_ = f.SetRowHeight(sheet, h18+2, 38)

	// Column-number row (1..16)
	numRow := h18 + 3
	for i := 1; i <= 16; i++ {
		col, _ := excelize.ColumnNumberToName(i)
		_ = f.SetCellValue(sheet, fmt.Sprintf("%s%d", col, numRow), fmt.Sprintf("%d", i))
		_ = f.SetCellStyle(sheet, fmt.Sprintf("%s%d", col, numRow), fmt.Sprintf("%s%d", col, numRow), borderHdr)
	}

	// Data rows
	dataRow := numRow + 1
	for _, r := range d.Rows {
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", dataRow), r.No)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", dataRow), r.Name)
		_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", dataRow), r.UOM)
		if r.VolumeTotal != 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", dataRow), r.VolumeTotal)
		}
		if r.VolumeCurrentYear != 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", dataRow), r.VolumeCurrentYear)
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", dataRow), r.ValueTotal)
		_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", dataRow), r.ValueCurrentYear)
		if r.DoneFromStartQty != 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", dataRow), r.DoneFromStartQty)
		}
		if r.DoneFromStartPct != 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", dataRow), r.DoneFromStartPct)
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", dataRow), r.DoneFromStartVal)
		if r.DoneFromYearQty != 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", dataRow), r.DoneFromYearQty)
		}
		if r.DoneFromYearPct != 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", dataRow), r.DoneFromYearPct)
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", dataRow), r.DoneFromYearVal)
		if r.DoneThisPeriodQty != 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("N%d", dataRow), r.DoneThisPeriodQty)
		}
		if r.DoneThisPeriodPct != 0 {
			_ = f.SetCellValue(sheet, fmt.Sprintf("O%d", dataRow), r.DoneThisPeriodPct)
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("P%d", dataRow), r.DoneThisPeriodVal)

		_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", dataRow), fmt.Sprintf("A%d", dataRow), borderHdr)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", dataRow), fmt.Sprintf("B%d", dataRow), borderPlain)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("C%d", dataRow), fmt.Sprintf("C%d", dataRow), plainCenter)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("D%d", dataRow), fmt.Sprintf("P%d", dataRow), borderNum)
		dataRow++
	}

	// Total row
	_ = f.MergeCell(sheet, fmt.Sprintf("B%d", dataRow), fmt.Sprintf("B%d", dataRow))
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", dataRow), "ВСЕГО БЕЗ НДС")
	_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", dataRow), "т.сум")
	_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", dataRow), d.TotalWithoutVAT)
	_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", dataRow), d.TotalWithoutVAT)
	_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", dataRow), d.TotalWithoutVAT)
	_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", dataRow), d.TotalWithoutVAT)
	_ = f.SetCellValue(sheet, fmt.Sprintf("P%d", dataRow), d.TotalWithoutVAT)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", dataRow), fmt.Sprintf("B%d", dataRow), borderTotalLabel)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("C%d", dataRow), fmt.Sprintf("C%d", dataRow), borderHdr)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("D%d", dataRow), fmt.Sprintf("P%d", dataRow), borderNumBold)
	dataRow += 2

	// Amount in words
	sumInSum := int64(d.TotalWithoutVAT * 1000.0) // totals are in тыс. сум
	words := numberToWordsRU(sumInSum)
	_ = f.MergeCell(sheet, fmt.Sprintf("B%d", dataRow), fmt.Sprintf("P%d", dataRow))
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", dataRow), fmt.Sprintf(
		"Итого к оплате: %s (%s) сум", formatWithSpaces(sumInSum), words))
	_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", dataRow), fmt.Sprintf("B%d", dataRow), boldLeft)
	dataRow += 3

	// Signatures
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", dataRow), "Руководитель Заказчика:")
	_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", dataRow), "Руководитель Подрядчика:")
	_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", dataRow), fmt.Sprintf("B%d", dataRow), boldLeft)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("I%d", dataRow), fmt.Sprintf("I%d", dataRow), boldLeft)
	dataRow += 2
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", dataRow), "_______________ "+d.Client.Director)
	_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", dataRow), "_______________ "+d.Contractor.Director)
	dataRow += 2
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", dataRow), "Главный бухгалтер:")
	_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", dataRow), "Главный бухгалтер:")
	_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", dataRow), fmt.Sprintf("B%d", dataRow), boldLeft)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("I%d", dataRow), fmt.Sprintf("I%d", dataRow), boldLeft)
	dataRow += 2
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", dataRow), "_______________ "+d.Client.Accountant)
	_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", dataRow), "_______________ "+d.Contractor.Accountant)

	_ = f.SetPageLayout(sheet, &excelize.PageLayoutOptions{
		Orientation: func() *string { s := "landscape"; return &s }(),
	})

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
