package handler

// File: buxgalteriya_forma.go
//
// TT Buxgalteriya ERP §6.4 — regulated BHMS №21 reports:
//   * Forma 1 — Buxgalteriya balansi  (Balance Sheet)
//   * Forma 2 — Moliyaviy natijalar   (Income Statement)
//   * Forma 3 — Pul mablag'lari harakati (Cash Flow, direct method)
//
// The data-building logic lives in load* helpers so both the JSON handlers and
// the combined Excel exporter can reuse it.

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// ---------------------------------------------------------------------------
// shared types
// ---------------------------------------------------------------------------

type formaLine struct {
	LineCode     string  `json:"line_code"`
	NameUz       string  `json:"name_uz"`
	NameRu       string  `json:"name_ru"`
	AccountMatch string  `json:"account_match"`
	Current      float64 `json:"current"`
	Prior        float64 `json:"prior,omitempty"`
}

type formaResponse struct {
	FormaCode    string         `json:"forma_code"`
	Title        string         `json:"title"`
	AsOfDate     string         `json:"as_of_date,omitempty"`
	PeriodFrom   string         `json:"period_from,omitempty"`
	PeriodTo     string         `json:"period_to,omitempty"`
	Sections     []formaSection `json:"sections"`
	TotalCurrent float64        `json:"total_current"`
	TotalPrior   float64        `json:"total_prior,omitempty"`
	IsBalanced   *bool          `json:"is_balanced,omitempty"`
}

type formaSection struct {
	SectionCode string      `json:"section_code"`
	NameUz      string      `json:"name_uz"`
	Lines       []formaLine `json:"lines"`
	Subtotal    float64     `json:"subtotal"`
}

// ---------------------------------------------------------------------------
// low-level balance / turnover queries (shared by all formas and ASQ)
// ---------------------------------------------------------------------------

func (h *Handler) accountBalanceByPattern(
	tenantID uuid.UUID, orgID *uuid.UUID, pattern string, endDate time.Time,
) (debit float64, credit float64, err error) {

	q := `
		SELECT COALESCE(SUM(jel.debit_amount), 0),
		       COALESCE(SUM(jel.credit_amount), 0)
		FROM accounts a
		LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date <= $3
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
		  AND a.code LIKE $2`
	args := []interface{}{tenantID, pattern, endDate.Format("2006-01-02")}
	if orgID != nil && *orgID != uuid.Nil {
		q += " AND a.organization_id = $4"
		args = append(args, *orgID)
	}

	if err = h.db.QueryRow(q, args...).Scan(&debit, &credit); err != nil {
		return 0, 0, err
	}
	return debit, credit, nil
}

func (h *Handler) balanceNet(tenantID uuid.UUID, orgID *uuid.UUID, pattern string, endDate time.Time, asDebit bool) float64 {
	dt, kt, err := h.accountBalanceByPattern(tenantID, orgID, pattern, endDate)
	if err != nil {
		return 0
	}
	if asDebit {
		return bxgRound2(dt - kt)
	}
	return bxgRound2(kt - dt)
}

func (h *Handler) turnover(tenantID uuid.UUID, orgID *uuid.UUID, pattern string,
	startDate, endDate time.Time, which string) float64 {
	q := `
		SELECT COALESCE(SUM(` + which + `), 0)
		FROM accounts a
		JOIN journal_entry_lines jel ON jel.account_id = a.id
		JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date BETWEEN $3 AND $4
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
		  AND a.code LIKE $2`
	args := []interface{}{tenantID, pattern,
		startDate.Format("2006-01-02"), endDate.Format("2006-01-02")}
	if orgID != nil && *orgID != uuid.Nil {
		q += " AND a.organization_id = $5"
		args = append(args, *orgID)
	}
	var v float64
	_ = h.db.QueryRow(q, args...).Scan(&v)
	return bxgRound2(v)
}

// pairedMovement returns the sum of line amounts (of `side`) on accounts
// matching `debitLike` when the same journal entry contains an offsetting
// line on `creditLike`. Used for cash-flow classification.
func (h *Handler) pairedMovement(tenantID uuid.UUID, orgID *uuid.UUID,
	debitLike, creditLike string, start, end time.Time, side string) float64 {

	sumCol := "jel.debit_amount"
	if side == "credit" {
		sumCol = "jel.credit_amount"
	}

	q := `
		SELECT COALESCE(SUM(` + sumCol + `), 0)
		FROM journal_entry_lines jel
		JOIN accounts a ON jel.account_id = a.id
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE je.tenant_id = $1 AND je.status = 'posted' AND je.deleted_at IS NULL
		  AND je.entry_date BETWEEN $2 AND $3
		  AND a.code LIKE $4
		  AND EXISTS (
			  SELECT 1 FROM journal_entry_lines jel2
			  JOIN accounts a2 ON jel2.account_id = a2.id
			  WHERE jel2.journal_entry_id = jel.journal_entry_id
			    AND jel2.id <> jel.id
			    AND a2.code LIKE $5
		  )`
	args := []interface{}{tenantID, start.Format("2006-01-02"), end.Format("2006-01-02"),
		debitLike, creditLike}
	if orgID != nil && *orgID != uuid.Nil {
		q += " AND je.organization_id = $6"
		args = append(args, *orgID)
	}
	var v float64
	_ = h.db.QueryRow(q, args...).Scan(&v)
	return v
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// ---------------------------------------------------------------------------
// Forma 1 — Buxgalteriya balansi
// ---------------------------------------------------------------------------

type formaLineDef struct {
	code, match, nameUz, nameRu string
	asDebit                     bool
}

var forma1Assets = []formaLineDef{
	{"010", "01%", "Asosiy vositalar (dastlabki qiymati)", "Основные средства (первоначальная стоимость)", true},
	// Accumulated depreciation is a contra-asset: read as debit-normal (dt-kt)
	// so its credit balance comes through NEGATIVE and reduces total assets
	// (the line is labelled "ayriladi" / to be deducted).
	{"020", "02%", "Asosiy vositalar eskirishi (ayriladi)", "Амортизация основных средств", true},
	{"040", "04%", "Nomoddiy aktivlar", "Нематериальные активы", true},
	{"080", "08%", "Kapital qo'yilmalar (tugallanmagan qurilish)", "Капитальные вложения", true},
	{"100", "1%", "Tovar-moddiy zaxiralar", "Товарно-материальные запасы", true},
	{"200", "2%", "Ishlab chiqarish xarajatlari va tayyor mahsulot", "Затраты на производство и готовая продукция", true},
	{"300", "3%", "Kelgusi davr xarajatlari", "Расходы будущих периодов", true},
	{"400", "4%", "Debitorlik qarzdorligi", "Дебиторская задолженность", true},
	{"500", "5%", "Pul mablag'lari", "Денежные средства", true},
}

var forma1Liabs = []formaLineDef{
	{"600", "6%", "Qisqa muddatli majburiyatlar", "Краткосрочные обязательства", false},
	{"700", "7%", "Uzoq muddatli majburiyatlar", "Долгосрочные обязательства", false},
}

var forma1Equity = []formaLineDef{
	{"800", "8%", "Kapital (ustav, zaxira, taqsimlanmagan foyda)", "Собственный капитал", false},
	// Current financial result = net of ALL 9xxx accounts (kt-dt): revenue less
	// expenses. Matching only "9900%"/9910 missed the current-year profit that
	// is still open in 9000-9899 (not yet closed to 9910), so the balance never
	// tied. Netting all 9xxx captures both the closed (9910) and open portions
	// without double counting, since closing just moves balances within 9xxx.
	{"990", "9%", "Yakuniy moliyaviy natija (foyda/zarar)", "Финансовый результат", false},
}

// loadForma1 computes the balance sheet as of `asOf` for the given tenant/org.
func (h *Handler) loadForma1(tenantID uuid.UUID, orgPtr *uuid.UUID, asOfStr string) (formaResponse, error) {
	asOf, err := time.Parse("2006-01-02", asOfStr)
	if err != nil {
		return formaResponse{}, fmt.Errorf("invalid as_of_date: %w", err)
	}

	build := func(ds []formaLineDef) (formaSection, float64) {
		sec := formaSection{}
		var subtotal float64
		for _, d := range ds {
			v := h.balanceNet(tenantID, orgPtr, d.match, asOf, d.asDebit)
			sec.Lines = append(sec.Lines, formaLine{
				LineCode: d.code, NameUz: d.nameUz, NameRu: d.nameRu,
				AccountMatch: d.match, Current: v,
			})
			subtotal += v
		}
		sec.Subtotal = bxgRound2(subtotal)
		return sec, sec.Subtotal
	}

	assetSec, assetTotal := build(forma1Assets)
	assetSec.SectionCode = "I"
	assetSec.NameUz = "I. AKTIVLAR"

	liabSec, liabTotal := build(forma1Liabs)
	liabSec.SectionCode = "II"
	liabSec.NameUz = "II. MAJBURIYATLAR"

	equitySec, equityTotal := build(forma1Equity)
	equitySec.SectionCode = "III"
	equitySec.NameUz = "III. KAPITAL"

	passiveTotal := liabTotal + equityTotal
	balanced := absFloat(assetTotal-passiveTotal) < 0.01

	return formaResponse{
		FormaCode:    "1",
		Title:        "Forma 1 — Buxgalteriya balansi (BHMS №21)",
		AsOfDate:     asOfStr,
		Sections:     []formaSection{assetSec, liabSec, equitySec},
		TotalCurrent: bxgRound2(assetTotal),
		IsBalanced:   &balanced,
	}, nil
}

// GetForma1 — HTTP JSON endpoint.
func (h *Handler) GetForma1(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	asOfStr := c.Query("as_of_date")
	if asOfStr == "" {
		asOfStr = time.Now().Format("2006-01-02")
	}
	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}
	resp, err := h.loadForma1(tenantID, orgPtr, asOfStr)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, resp)
}

// ---------------------------------------------------------------------------
// Forma 2 — Moliyaviy natijalar (Income Statement)
// ---------------------------------------------------------------------------

func (h *Handler) loadForma2(tenantID uuid.UUID, orgPtr *uuid.UUID, periodFromStr, periodToStr string) (formaResponse, error) {
	start, err := time.Parse("2006-01-02", periodFromStr)
	if err != nil {
		return formaResponse{}, fmt.Errorf("invalid period_from: %w", err)
	}
	end, err := time.Parse("2006-01-02", periodToStr)
	if err != nil {
		return formaResponse{}, fmt.Errorf("invalid period_to: %w", err)
	}

	revenueSales := h.turnover(tenantID, orgPtr, "9010", start, end, "credit_amount")
	revenueOther := h.turnover(tenantID, orgPtr, "92%", start, end, "credit_amount") +
		h.turnover(tenantID, orgPtr, "93%", start, end, "credit_amount")

	cogs := h.turnover(tenantID, orgPtr, "91%", start, end, "debit_amount")
	opex := h.turnover(tenantID, orgPtr, "94%", start, end, "debit_amount")
	finIncome := h.turnover(tenantID, orgPtr, "95%", start, end, "credit_amount")
	finExpense := h.turnover(tenantID, orgPtr, "96%", start, end, "debit_amount")
	extrGain := h.turnover(tenantID, orgPtr, "97%", start, end, "credit_amount")
	extrLoss := h.turnover(tenantID, orgPtr, "98%", start, end, "debit_amount")

	grossProfit := revenueSales - cogs
	operatingProfit := grossProfit + revenueOther - opex
	profitBeforeTax := operatingProfit + finIncome - finExpense + extrGain - extrLoss
	netProfit := profitBeforeTax

	lines := []formaLine{
		{LineCode: "010", NameUz: "Sotishdan tushum (QQS siz)", NameRu: "Выручка от реализации", AccountMatch: "9010", Current: revenueSales},
		{LineCode: "020", NameUz: "Sotilgan tovarlar tannarxi", NameRu: "Себестоимость", AccountMatch: "91%", Current: -cogs},
		{LineCode: "030", NameUz: "Yalpi foyda (zarar)", NameRu: "Валовая прибыль (убыток)", AccountMatch: "", Current: grossProfit},
		{LineCode: "040", NameUz: "Boshqa operatsion daromadlar", NameRu: "Прочие опер. доходы", AccountMatch: "92-93%", Current: revenueOther},
		{LineCode: "050", NameUz: "Davr xarajatlari", NameRu: "Расходы периода", AccountMatch: "94%", Current: -opex},
		{LineCode: "060", NameUz: "Operatsion faoliyat foydasi (zarari)", NameRu: "Операционная прибыль", AccountMatch: "", Current: operatingProfit},
		{LineCode: "070", NameUz: "Moliyaviy faoliyat daromadlari", NameRu: "Финансовые доходы", AccountMatch: "95%", Current: finIncome},
		{LineCode: "080", NameUz: "Moliyaviy faoliyat xarajatlari", NameRu: "Финансовые расходы", AccountMatch: "96%", Current: -finExpense},
		{LineCode: "090", NameUz: "Favqulodda foyda", NameRu: "Чрезвычайные прибыли", AccountMatch: "97%", Current: extrGain},
		{LineCode: "100", NameUz: "Favqulodda zarar / soliqlar", NameRu: "Чрезвычайные убытки", AccountMatch: "98%", Current: -extrLoss},
		{LineCode: "110", NameUz: "Hisobot davri sof foydasi (zarari)", NameRu: "Чистая прибыль (убыток)", AccountMatch: "", Current: netProfit},
	}
	for i := range lines {
		lines[i].Current = bxgRound2(lines[i].Current)
	}

	return formaResponse{
		FormaCode:  "2",
		Title:      "Forma 2 — Moliyaviy natijalar to'g'risida hisobot (BHMS №21)",
		PeriodFrom: periodFromStr,
		PeriodTo:   periodToStr,
		Sections: []formaSection{{
			SectionCode: "I",
			NameUz:      "Moliyaviy natijalar",
			Lines:       lines,
			Subtotal:    bxgRound2(netProfit),
		}},
		TotalCurrent: bxgRound2(netProfit),
	}, nil
}

func (h *Handler) GetForma2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	periodFromStr := c.Query("period_from")
	periodToStr := c.Query("period_to")
	if periodFromStr == "" || periodToStr == "" {
		response.BadRequest(c, "period_from and period_to are required")
		return
	}
	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}
	resp, err := h.loadForma2(tenantID, orgPtr, periodFromStr, periodToStr)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, resp)
}

// ---------------------------------------------------------------------------
// Forma 3 — Pul mablag'lari harakati (Cash Flow, direct method)
// ---------------------------------------------------------------------------

func (h *Handler) loadForma3(tenantID uuid.UUID, orgPtr *uuid.UUID, periodFromStr, periodToStr string) (formaResponse, error) {
	start, err := time.Parse("2006-01-02", periodFromStr)
	if err != nil {
		return formaResponse{}, fmt.Errorf("invalid period_from: %w", err)
	}
	end, err := time.Parse("2006-01-02", periodToStr)
	if err != nil {
		return formaResponse{}, fmt.Errorf("invalid period_to: %w", err)
	}

	openingCash := h.balanceNet(tenantID, orgPtr, "5%", start.Add(-time.Hour*24), true)
	closingCash := h.balanceNet(tenantID, orgPtr, "5%", end, true)

	opInflow := h.pairedMovement(tenantID, orgPtr, "5%", "4010", start, end, "debit") +
		h.pairedMovement(tenantID, orgPtr, "5%", "9010", start, end, "debit")
	opOutSupp := h.pairedMovement(tenantID, orgPtr, "6010", "5%", start, end, "debit")
	opOutEmp := h.pairedMovement(tenantID, orgPtr, "6710", "5%", start, end, "debit")
	opOutTax := h.pairedMovement(tenantID, orgPtr, "64%", "5%", start, end, "debit")
	opNet := opInflow - opOutSupp - opOutEmp - opOutTax

	invOut := h.pairedMovement(tenantID, orgPtr, "01%", "5%", start, end, "debit") +
		h.pairedMovement(tenantID, orgPtr, "04%", "5%", start, end, "debit")

	finIn := h.pairedMovement(tenantID, orgPtr, "5%", "62%", start, end, "debit") +
		h.pairedMovement(tenantID, orgPtr, "5%", "78%", start, end, "debit")
	finOut := h.pairedMovement(tenantID, orgPtr, "62%", "5%", start, end, "debit") +
		h.pairedMovement(tenantID, orgPtr, "78%", "5%", start, end, "debit")
	finNet := finIn - finOut

	lines := []formaLine{
		{LineCode: "001", NameUz: "Davr boshidagi pul qoldig'i", NameRu: "Остаток ден. средств на начало", Current: openingCash},
		{LineCode: "010", NameUz: "Operatsion: xaridorlardan tushum", NameRu: "Поступления от покупателей", Current: opInflow},
		{LineCode: "020", NameUz: "Operatsion: ta'minotchilarga to'lov", NameRu: "Выплаты поставщикам", Current: -opOutSupp},
		{LineCode: "030", NameUz: "Operatsion: xodimlarga to'lov", NameRu: "Выплаты работникам", Current: -opOutEmp},
		{LineCode: "040", NameUz: "Operatsion: soliqlar", NameRu: "Налоги", Current: -opOutTax},
		{LineCode: "050", NameUz: "Operatsion faoliyat sof oqimi", NameRu: "Чистый опер. поток", Current: opNet},
		{LineCode: "110", NameUz: "Investitsion: asosiy vositalarga", NameRu: "Инвест: ОС/НМА", Current: -invOut},
		{LineCode: "120", NameUz: "Investitsion sof oqimi", NameRu: "Чистый инвест. поток", Current: -invOut},
		{LineCode: "210", NameUz: "Moliyaviy: kreditlar olindi", NameRu: "Поступления от займов", Current: finIn},
		{LineCode: "220", NameUz: "Moliyaviy: kreditlar qaytarildi", NameRu: "Погашение займов", Current: -finOut},
		{LineCode: "230", NameUz: "Moliyaviy faoliyat sof oqimi", NameRu: "Чистый фин. поток", Current: finNet},
		{LineCode: "900", NameUz: "Davr oxiridagi pul qoldig'i", NameRu: "Остаток ден. средств на конец", Current: closingCash},
	}
	for i := range lines {
		lines[i].Current = bxgRound2(lines[i].Current)
	}

	netDelta := opNet - invOut + finNet
	computedClosing := openingCash + netDelta
	matches := absFloat(computedClosing-closingCash) < 0.01

	return formaResponse{
		FormaCode:  "3",
		Title:      "Forma 3 — Pul mablag'lari harakati to'g'risida hisobot (BHMS №21)",
		PeriodFrom: periodFromStr,
		PeriodTo:   periodToStr,
		Sections: []formaSection{{
			SectionCode: "I",
			NameUz:      fmt.Sprintf("Pul oqimlari (computed=%.2f, actual=%.2f, match=%v)", computedClosing, closingCash, matches),
			Lines:       lines,
			Subtotal:    bxgRound2(closingCash),
		}},
		TotalCurrent: bxgRound2(closingCash),
		IsBalanced:   &matches,
	}, nil
}

func (h *Handler) GetForma3(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	periodFromStr := c.Query("period_from")
	periodToStr := c.Query("period_to")
	if periodFromStr == "" || periodToStr == "" {
		response.BadRequest(c, "period_from and period_to are required")
		return
	}
	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}
	resp, err := h.loadForma3(tenantID, orgPtr, periodFromStr, periodToStr)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, resp)
}

// ---------------------------------------------------------------------------
// Combined Excel export — all three formas
// ---------------------------------------------------------------------------

// ExportFormasExcel emits an .xlsx with real data from loadForma1/2/3.
// Query params: as_of_date (for F1), period_from / period_to (for F2, F3).
func (h *Handler) ExportFormasExcel(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	asOfDate := c.Query("as_of_date")
	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")
	if asOfDate == "" {
		asOfDate = time.Now().Format("2006-01-02")
	}
	if periodFrom == "" || periodTo == "" {
		response.BadRequest(c, "period_from and period_to are required for Forma 2/3")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}

	f1, err := h.loadForma1(tenantID, orgPtr, asOfDate)
	if err != nil {
		response.BadRequest(c, "Forma 1: "+err.Error())
		return
	}
	f2, err := h.loadForma2(tenantID, orgPtr, periodFrom, periodTo)
	if err != nil {
		response.BadRequest(c, "Forma 2: "+err.Error())
		return
	}
	f3, err := h.loadForma3(tenantID, orgPtr, periodFrom, periodTo)
	if err != nil {
		response.BadRequest(c, "Forma 3: "+err.Error())
		return
	}

	f := excelize.NewFile()
	defer f.Close()
	f.DeleteSheet("Sheet1")

	writeFormaSheet(f, "Forma1", f1)
	writeFormaSheet(f, "Forma2", f2)
	writeFormaSheet(f, "Forma3", f3)

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition",
		fmt.Sprintf("attachment; filename=BHMS_Forma_1_2_3_%s.xlsx", time.Now().Format("20060102")))
	c.Status(http.StatusOK)
	if err := f.Write(c.Writer); err != nil {
		h.log.Error("ExportFormasExcel: write failed", "error", err)
	}
}

// writeFormaSheet renders a single formaResponse onto an excelize worksheet.
// Used for all three formas so their layout is identical.
func writeFormaSheet(f *excelize.File, sheetName string, r formaResponse) {
	_, _ = f.NewSheet(sheetName)
	_ = f.SetCellValue(sheetName, "A1", r.Title)
	if r.AsOfDate != "" {
		_ = f.SetCellValue(sheetName, "A2", "Sana: "+r.AsOfDate)
	} else {
		_ = f.SetCellValue(sheetName, "A2", "Davr: "+r.PeriodFrom+" — "+r.PeriodTo)
	}

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#E8F1FF"}, Pattern: 1},
	})
	sectionStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#F4F4F4"}, Pattern: 1},
	})
	totalStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#FFF2CC"}, Pattern: 1},
	})

	row := 4
	_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), "Kod")
	_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), "Nomi")
	_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), "Hisob kodi")
	_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), "Summa")
	_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", row), fmt.Sprintf("D%d", row), headerStyle)
	row++

	for _, sec := range r.Sections {
		_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), sec.SectionCode)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), sec.NameUz)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", row), fmt.Sprintf("D%d", row), sectionStyle)
		row++

		for _, l := range sec.Lines {
			_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), l.LineCode)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), l.NameUz)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), l.AccountMatch)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), l.Current)
			row++
		}

		_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), fmt.Sprintf("Jami %s", sec.SectionCode))
		_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), sec.Subtotal)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", row), fmt.Sprintf("D%d", row), totalStyle)
		row++
	}

	_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), "UMUMIY JAMI")
	_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), r.TotalCurrent)
	_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", row), fmt.Sprintf("D%d", row), totalStyle)
	row++

	if r.IsBalanced != nil {
		label := "Balans tekshiruvi"
		result := "HA"
		if !*r.IsBalanced {
			result = "YO'Q (xato!)"
		}
		_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), label)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), result)
	}

	_ = f.SetColWidth(sheetName, "A", "A", 8)
	_ = f.SetColWidth(sheetName, "B", "B", 55)
	_ = f.SetColWidth(sheetName, "C", "C", 14)
	_ = f.SetColWidth(sheetName, "D", "D", 20)
}
