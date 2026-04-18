package handler

import (
	"bytes"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

// =====================================================
// FORMA 19 (Material Report) — XLSX generator
// Layout matches the Uzbekistan-standard "Материальный отчет"
// reference (example: MCHJ GOLDEN AVENUE / Голден материал.xls):
//   • company + period + account header block
//   • two-level column header: N, Name, Code, Unit, Price,
//     Opening (qty + sum), Incoming (qty + sum),
//     Outgoing (qty + sum), Closing (qty + sum)
//   • account / warehouse group rows
//   • material detail rows
//   • Итого summary row
// When F19 rows contain change-type lines (row_type='change'),
// they are rendered in a separate section below the base rows
// with an orange fill matching the UI.
// =====================================================

type f19Row struct {
	ID           int64
	Name         string
	UOM          string
	RowType      string // base | change
	Boshi        float64
	Keldi        float64
	Sarf         float64
	Qoldi        float64
	CostPrice    float64
	ChangeReason string
	ChangeNote   string
	SortOrder    int
}

type f19Data struct {
	ProjectName    string
	ProjectAddress string
	ClientName     string
	ActName        string
	ActNumber      string
	PeriodFrom     *time.Time
	PeriodTo       *time.Time
	Rows           []f19Row
}

func (h *Handler) loadForma19Data(actID int64, tenantID uuid.UUID) (*f19Data, error) {
	d := &f19Data{}

	var projectID int64
	var periodFrom, periodTo sql.NullTime
	var actNumber sql.NullInt64

	err := h.db.QueryRow(`
		SELECT a.project_id, a.name, a.period_from, a.period_to, a.act_number
		FROM construction_act a
		WHERE a.id = $1 AND a.tenant_id = $2 AND a.act_type = 'hidden_work'
	`, actID, tenantID).Scan(&projectID, &d.ActName, &periodFrom, &periodTo, &actNumber)
	if err != nil {
		return nil, fmt.Errorf("load forma19 act: %w", err)
	}
	if periodFrom.Valid {
		v := periodFrom.Time
		d.PeriodFrom = &v
	}
	if periodTo.Valid {
		v := periodTo.Time
		d.PeriodTo = &v
	}
	if actNumber.Valid {
		d.ActNumber = fmt.Sprintf("%d", actNumber.Int64)
	} else {
		d.ActNumber = d.ActName
	}

	// Project + client
	var pname, paddr sql.NullString
	var cname sql.NullString
	_ = h.db.QueryRow(`
		SELECT COALESCE(p.name, ''), COALESCE(p.address, ''), COALESCE(p.client_name, '')
		FROM construction_projects p WHERE p.id = $1 AND p.tenant_id = $2
	`, projectID, tenantID).Scan(&pname, &paddr, &cname)
	if pname.Valid {
		d.ProjectName = pname.String
	}
	if paddr.Valid {
		d.ProjectAddress = paddr.String
	}
	if cname.Valid {
		d.ClientName = cname.String
	}

	// Rows
	rows, err := h.db.Query(`
		SELECT id, name, COALESCE(uom, ''), COALESCE(row_type, 'base'),
		       COALESCE(boshi, 0), COALESCE(keldi, 0), COALESCE(sarf, 0),
		       COALESCE(qoldi, 0), COALESCE(cost_price, 0),
		       COALESCE(change_reason, ''), COALESCE(change_note, ''),
		       COALESCE(sort_order, 0)
		FROM construction_act_line
		WHERE act_id = $1
		ORDER BY row_type ASC, sort_order ASC, id ASC
	`, actID)
	if err != nil {
		return nil, fmt.Errorf("load forma19 rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r f19Row
		if err := rows.Scan(&r.ID, &r.Name, &r.UOM, &r.RowType,
			&r.Boshi, &r.Keldi, &r.Sarf, &r.Qoldi, &r.CostPrice,
			&r.ChangeReason, &r.ChangeNote, &r.SortOrder); err != nil {
			continue
		}
		d.Rows = append(d.Rows, r)
	}
	return d, nil
}

// GenerateForma19XLSXBytes renders Forma 19 material report as .xlsx bytes
// using a layout matching Uzbekistan industry standard (e.g. Голден материал).
func (h *Handler) GenerateForma19XLSXBytes(actID int64, tenantID uuid.UUID) ([]byte, error) {
	d, err := h.loadForma19Data(actID, tenantID)
	if err != nil {
		return nil, err
	}

	f := excelize.NewFile()
	defer f.Close()
	sheet := "Материальный отчет"
	idx, _ := f.NewSheet(sheet)
	f.SetActiveSheet(idx)
	_ = f.DeleteSheet("Sheet1")

	// 14 columns used: A header gutter, B..N data
	widths := map[string]float64{
		"A": 3, "B": 5, "C": 40, "D": 10, "E": 8, "F": 12,
		"G": 11, "H": 14, "I": 11, "J": 14, "K": 11, "L": 14,
		"M": 11, "N": 14,
	}
	for col, w := range widths {
		_ = f.SetColWidth(sheet, col, col, w)
	}

	// Styles ----------------------------------------------------------------
	titleStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 13, Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	subTitleStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 10, Italic: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	boldLeft, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: xlsxFont, Size: 10, Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"},
	})

	hdr, _ := addBorderedStyle(f, true, "center", "center", true, "E7E6E6", "")
	num, _ := addBorderedStyle(f, false, "right", "center", false, "", "#,##0.000")
	numMoney, _ := addBorderedStyle(f, false, "right", "center", false, "", "#,##0.00")
	numInt, _ := addBorderedStyle(f, false, "right", "center", false, "", "#,##0")
	centerCell, _ := addBorderedStyle(f, false, "center", "center", true, "", "")
	plain, _ := addBorderedStyle(f, false, "left", "top", true, "", "")
	accountRow, _ := addBorderedStyle(f, true, "left", "center", true, "D9E1F2", "")
	accountNumMoney, _ := addBorderedStyle(f, true, "right", "center", false, "D9E1F2", "#,##0.00")
	accountNum, _ := addBorderedStyle(f, true, "right", "center", false, "D9E1F2", "#,##0.000")
	warehouseRow, _ := addBorderedStyle(f, false, "left", "center", true, "F2F2F2", "")
	warehouseNumMoney, _ := addBorderedStyle(f, false, "right", "center", false, "F2F2F2", "#,##0.00")
	warehouseNum, _ := addBorderedStyle(f, false, "right", "center", false, "F2F2F2", "#,##0.000")
	changeRow, _ := addBorderedStyle(f, false, "left", "top", true, "FDE9D9", "")
	changeRowCenter, _ := addBorderedStyle(f, false, "center", "center", true, "FDE9D9", "")
	changeRowNum, _ := addBorderedStyle(f, false, "right", "center", false, "FDE9D9", "#,##0.000")
	changeRowMoney, _ := addBorderedStyle(f, false, "right", "center", false, "FDE9D9", "#,##0.00")
	totalRow, _ := addBorderedStyle(f, true, "left", "center", true, "FDE9D9", "")
	totalMoney, _ := addBorderedStyle(f, true, "right", "center", false, "FDE9D9", "#,##0.00")
	totalQty, _ := addBorderedStyle(f, true, "right", "center", false, "FDE9D9", "#,##0.000")

	// Header block ---------------------------------------------------------
	_ = f.MergeCell(sheet, "B1", "N1")
	_ = f.SetCellValue(sheet, "B1", d.ClientName)
	_ = f.SetCellStyle(sheet, "B1", "B1", titleStyle)

	periodLabel := "Материальный отчет"
	if d.PeriodFrom != nil && d.PeriodTo != nil {
		periodLabel = fmt.Sprintf("Материальный отчет за %s %d г. - %s %d г.",
			capitalizeRU(monthNameRU(int(d.PeriodFrom.Month()))), d.PeriodFrom.Year(),
			capitalizeRU(monthNameRU(int(d.PeriodTo.Month()))), d.PeriodTo.Year())
	}
	_ = f.MergeCell(sheet, "B2", "N2")
	_ = f.SetCellValue(sheet, "B2", periodLabel)
	_ = f.SetCellStyle(sheet, "B2", "B2", titleStyle)

	_ = f.MergeCell(sheet, "B3", "N3")
	actRef := d.ActNumber
	if actRef != "" && !startsWithF19(actRef) {
		actRef = "Ф19 №" + actRef
	}
	headerLine3 := "Счет: 1010 СЧЕТА УЧЕТА МАТЕРИАЛОВ"
	if actRef != "" {
		headerLine3 = headerLine3 + ";   " + actRef
	}
	_ = f.SetCellValue(sheet, "B3", headerLine3)
	_ = f.SetCellStyle(sheet, "B3", "B3", subTitleStyle)

	if d.ProjectName != "" {
		_ = f.MergeCell(sheet, "B4", "N4")
		_ = f.SetCellValue(sheet, "B4", "Объект: "+d.ProjectName)
		_ = f.SetCellStyle(sheet, "B4", "B4", boldLeft)
	}

	// Column headers (rows 6-7) --------------------------------------------
	hdrRow := 6
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", hdrRow), "№№")
	_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", hdrRow), "Наименование")
	_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", hdrRow), "Код в справочнике")
	_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", hdrRow), "Ед.\nизм.")
	_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", hdrRow), "Цена учетная")
	_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", hdrRow), "Сальдо на начало периода")
	_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", hdrRow), "Приход")
	_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", hdrRow), "Расход")
	_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", hdrRow), "Сальдо на конец периода")
	// Merge top-level pairs
	_ = f.MergeCell(sheet, fmt.Sprintf("G%d", hdrRow), fmt.Sprintf("H%d", hdrRow))
	_ = f.MergeCell(sheet, fmt.Sprintf("I%d", hdrRow), fmt.Sprintf("J%d", hdrRow))
	_ = f.MergeCell(sheet, fmt.Sprintf("K%d", hdrRow), fmt.Sprintf("L%d", hdrRow))
	_ = f.MergeCell(sheet, fmt.Sprintf("M%d", hdrRow), fmt.Sprintf("N%d", hdrRow))
	// Vertical merge for single-level columns
	for _, c := range []string{"B", "C", "D", "E", "F"} {
		_ = f.MergeCell(sheet, fmt.Sprintf("%s%d", c, hdrRow), fmt.Sprintf("%s%d", c, hdrRow+1))
	}
	// Row hdr+1 sub-headers
	_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", hdrRow+1), "Кол-во")
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", hdrRow+1), "Сумма")
	_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", hdrRow+1), "Кол-во")
	_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", hdrRow+1), "Сумма")
	_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", hdrRow+1), "Кол-во")
	_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", hdrRow+1), "Сумма")
	_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", hdrRow+1), "Кол-во")
	_ = f.SetCellValue(sheet, fmt.Sprintf("N%d", hdrRow+1), "Сумма")
	_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", hdrRow), fmt.Sprintf("N%d", hdrRow+1), hdr)
	_ = f.SetRowHeight(sheet, hdrRow, 28)
	_ = f.SetRowHeight(sheet, hdrRow+1, 18)

	// Column number row (1..12 like reference)
	numRow := hdrRow + 2
	colNumLabels := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13"}
	for i, label := range colNumLabels {
		col, _ := excelize.ColumnNumberToName(i + 2) // skip column A
		_ = f.SetCellValue(sheet, fmt.Sprintf("%s%d", col, numRow), label)
	}
	_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", numRow), fmt.Sprintf("N%d", numRow), centerCell)

	// Data -----------------------------------------------------------------
	baseRows := []f19Row{}
	changeRows := []f19Row{}
	for _, r := range d.Rows {
		if r.RowType == "change" {
			changeRows = append(changeRows, r)
		} else {
			baseRows = append(baseRows, r)
		}
	}

	// Collect totals
	var openQty, openSum, inQty, inSum, outQty, outSum, closeQty, closeSum float64
	row := numRow + 1

	// Account heading — single group for now (reference uses 1010/1050 split)
	if len(baseRows) > 0 {
		_ = f.MergeCell(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("F%d", row))
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), "1010 : Сырье и материалы")
		_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("F%d", row), accountRow)
		// prepare placeholder for totals in this account
		accountHeaderRow := row
		var accOpenQ, accOpenS, accInQ, accInS, accOutQ, accOutS, accCloseQ, accCloseS float64
		row++

		// Warehouse group
		_ = f.MergeCell(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("F%d", row))
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), "   Склад материалов")
		_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("F%d", row), warehouseRow)
		warehouseHeaderRow := row
		row++

		for i, r := range baseRows {
			// ensure closing = boshi + keldi - sarf (defensive even if DB-stored)
			closingQty := r.Qoldi
			if closingQty == 0 {
				closingQty = r.Boshi + r.Keldi - r.Sarf
			}
			sumOpen := r.Boshi * r.CostPrice
			sumIn := r.Keldi * r.CostPrice
			sumOut := r.Sarf * r.CostPrice
			sumClose := closingQty * r.CostPrice

			_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), i+1)
			_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), r.Name)
			_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", row), r.ID) // surrogate code
			_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", row), r.UOM)
			_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", row), r.CostPrice)

			if r.Boshi > 0 {
				_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", row), r.Boshi)
				_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), sumOpen)
			}
			if r.Keldi > 0 {
				_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", row), r.Keldi)
				_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", row), sumIn)
			}
			if r.Sarf > 0 {
				_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", row), r.Sarf)
				_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", row), sumOut)
			}
			if closingQty > 0 {
				_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", row), closingQty)
				_ = f.SetCellValue(sheet, fmt.Sprintf("N%d", row), sumClose)
			}
			_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), centerCell)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), plain)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("D%d", row), numInt)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("E%d", row), fmt.Sprintf("E%d", row), centerCell)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("F%d", row), fmt.Sprintf("F%d", row), numMoney)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("G%d", row), fmt.Sprintf("G%d", row), num)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("H%d", row), numMoney)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("I%d", row), fmt.Sprintf("I%d", row), num)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("J%d", row), fmt.Sprintf("J%d", row), numMoney)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("K%d", row), fmt.Sprintf("K%d", row), num)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("L%d", row), fmt.Sprintf("L%d", row), numMoney)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("M%d", row), fmt.Sprintf("M%d", row), num)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("N%d", row), fmt.Sprintf("N%d", row), numMoney)

			accOpenQ += r.Boshi
			accOpenS += sumOpen
			accInQ += r.Keldi
			accInS += sumIn
			accOutQ += r.Sarf
			accOutS += sumOut
			accCloseQ += closingQty
			accCloseS += sumClose
			row++
		}

		// Back-fill warehouse header totals
		_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", warehouseHeaderRow), accOpenQ)
		_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", warehouseHeaderRow), accOpenS)
		_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", warehouseHeaderRow), accInQ)
		_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", warehouseHeaderRow), accInS)
		_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", warehouseHeaderRow), accOutQ)
		_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", warehouseHeaderRow), accOutS)
		_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", warehouseHeaderRow), accCloseQ)
		_ = f.SetCellValue(sheet, fmt.Sprintf("N%d", warehouseHeaderRow), accCloseS)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("G%d", warehouseHeaderRow), fmt.Sprintf("G%d", warehouseHeaderRow), warehouseNum)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", warehouseHeaderRow), fmt.Sprintf("H%d", warehouseHeaderRow), warehouseNumMoney)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("I%d", warehouseHeaderRow), fmt.Sprintf("I%d", warehouseHeaderRow), warehouseNum)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("J%d", warehouseHeaderRow), fmt.Sprintf("J%d", warehouseHeaderRow), warehouseNumMoney)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("K%d", warehouseHeaderRow), fmt.Sprintf("K%d", warehouseHeaderRow), warehouseNum)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("L%d", warehouseHeaderRow), fmt.Sprintf("L%d", warehouseHeaderRow), warehouseNumMoney)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("M%d", warehouseHeaderRow), fmt.Sprintf("M%d", warehouseHeaderRow), warehouseNum)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("N%d", warehouseHeaderRow), fmt.Sprintf("N%d", warehouseHeaderRow), warehouseNumMoney)
		// Back-fill account header totals with same sums
		_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", accountHeaderRow), accOpenQ)
		_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", accountHeaderRow), accOpenS)
		_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", accountHeaderRow), accInQ)
		_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", accountHeaderRow), accInS)
		_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", accountHeaderRow), accOutQ)
		_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", accountHeaderRow), accOutS)
		_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", accountHeaderRow), accCloseQ)
		_ = f.SetCellValue(sheet, fmt.Sprintf("N%d", accountHeaderRow), accCloseS)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("G%d", accountHeaderRow), fmt.Sprintf("G%d", accountHeaderRow), accountNum)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", accountHeaderRow), fmt.Sprintf("H%d", accountHeaderRow), accountNumMoney)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("I%d", accountHeaderRow), fmt.Sprintf("I%d", accountHeaderRow), accountNum)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("J%d", accountHeaderRow), fmt.Sprintf("J%d", accountHeaderRow), accountNumMoney)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("K%d", accountHeaderRow), fmt.Sprintf("K%d", accountHeaderRow), accountNum)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("L%d", accountHeaderRow), fmt.Sprintf("L%d", accountHeaderRow), accountNumMoney)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("M%d", accountHeaderRow), fmt.Sprintf("M%d", accountHeaderRow), accountNum)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("N%d", accountHeaderRow), fmt.Sprintf("N%d", accountHeaderRow), accountNumMoney)

		openQty += accOpenQ
		openSum += accOpenS
		inQty += accInQ
		inSum += accInS
		outQty += accOutQ
		outSum += accOutS
		closeQty += accCloseQ
		closeSum += accCloseS
	}

	// Changes section ------------------------------------------------------
	if len(changeRows) > 0 {
		_ = f.MergeCell(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("N%d", row))
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), "O'ZGARISHLAR (Изменения сметы)")
		_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("N%d", row), accountRow)
		row++

		for i, r := range changeRows {
			closingQty := r.Qoldi
			if closingQty == 0 {
				closingQty = r.Boshi + r.Keldi - r.Sarf
			}
			sumOpen := r.Boshi * r.CostPrice
			sumIn := r.Keldi * r.CostPrice
			sumOut := r.Sarf * r.CostPrice
			sumClose := closingQty * r.CostPrice

			_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), i+1)
			reasonSuffix := ""
			if r.ChangeReason != "" {
				reasonSuffix = " (" + r.ChangeReason + ")"
			}
			_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), r.Name+reasonSuffix)
			_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", row), r.ID)
			_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", row), r.UOM)
			_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", row), r.CostPrice)
			if r.Boshi > 0 {
				_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", row), r.Boshi)
				_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), sumOpen)
			}
			if r.Keldi > 0 {
				_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", row), r.Keldi)
				_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", row), sumIn)
			}
			if r.Sarf > 0 {
				_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", row), r.Sarf)
				_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", row), sumOut)
			}
			if closingQty > 0 {
				_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", row), closingQty)
				_ = f.SetCellValue(sheet, fmt.Sprintf("N%d", row), sumClose)
			}
			_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), changeRowCenter)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), changeRow)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("E%d", row), changeRowCenter)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("F%d", row), fmt.Sprintf("F%d", row), changeRowMoney)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("G%d", row), fmt.Sprintf("G%d", row), changeRowNum)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("H%d", row), changeRowMoney)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("I%d", row), fmt.Sprintf("I%d", row), changeRowNum)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("J%d", row), fmt.Sprintf("J%d", row), changeRowMoney)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("K%d", row), fmt.Sprintf("K%d", row), changeRowNum)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("L%d", row), fmt.Sprintf("L%d", row), changeRowMoney)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("M%d", row), fmt.Sprintf("M%d", row), changeRowNum)
			_ = f.SetCellStyle(sheet, fmt.Sprintf("N%d", row), fmt.Sprintf("N%d", row), changeRowMoney)

			openQty += r.Boshi
			openSum += sumOpen
			inQty += r.Keldi
			inSum += sumIn
			outQty += r.Sarf
			outSum += sumOut
			closeQty += closingQty
			closeSum += sumClose
			row++
		}
	}

	// Total row ------------------------------------------------------------
	_ = f.MergeCell(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("F%d", row))
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), "Итого")
	_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", row), openQty)
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), openSum)
	_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", row), inQty)
	_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", row), inSum)
	_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", row), outQty)
	_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", row), outSum)
	_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", row), closeQty)
	_ = f.SetCellValue(sheet, fmt.Sprintf("N%d", row), closeSum)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("F%d", row), totalRow)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("G%d", row), fmt.Sprintf("G%d", row), totalQty)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("H%d", row), totalMoney)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("I%d", row), fmt.Sprintf("I%d", row), totalQty)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("J%d", row), fmt.Sprintf("J%d", row), totalMoney)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("K%d", row), fmt.Sprintf("K%d", row), totalQty)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("L%d", row), fmt.Sprintf("L%d", row), totalMoney)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("M%d", row), fmt.Sprintf("M%d", row), totalQty)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("N%d", row), fmt.Sprintf("N%d", row), totalMoney)

	// Landscape page layout
	_ = f.SetPageLayout(sheet, &excelize.PageLayoutOptions{
		Orientation: func() *string { s := "landscape"; return &s }(),
	})

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// capitalizeRU uppercases the first rune of a Russian lowercase string.
func capitalizeRU(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	// Convert common lowercase Russian letters to uppercase
	c := runes[0]
	// Basic Latin uppercase
	if c >= 'a' && c <= 'z' {
		c = c - 32
	}
	// Cyrillic lowercase а-я (U+0430..U+044F) → А-Я (U+0410..U+042F)
	if c >= 0x0430 && c <= 0x044F {
		c = c - 0x20
	}
	if c == 0x0451 { // ё → Ё
		c = 0x0401
	}
	runes[0] = c
	return string(runes)
}

// startsWithF19 returns true if the act name already begins with "Ф19" or "F19".
func startsWithF19(s string) bool {
	if len(s) < 3 {
		return false
	}
	prefix3 := s[:3]
	return prefix3 == "F19" || prefix3 == "f19" || prefix3 == "Ф19" || prefix3 == "ф19"
}
