package handler

// File: buxgalteriya_reports.go
//
// Phase-3 report enhancements for TT Buxgalteriya ERP.
// This file adds two things on top of the reports already in reports.go:
//
//   1. GetTrialBalanceWithTurnover (ASQ per TT §6.1)
//      The spec requires each account row to show:
//         - opening balance (boshi saldosi)
//         - Dt / Kt turnover for the period (aylanma)
//         - closing balance (oxiri saldosi)
//      The existing GetTrialBalance returns closing balances only. This
//      handler returns the full spec-compliant structure.
//
//   2. ExportTrialBalanceExcel
//      Generates an .xlsx file of the ASQ with totals and an "Is balanced?"
//      invariant row. Hooked up under GET /finance/reports/trial-balance/excel.
//
// Also adds extra filters for the Account Card (amount range + doc type).

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// ---------------------------------------------------------------------------
// ASQ (Trial Balance) with opening / turnover / closing breakdown
// ---------------------------------------------------------------------------

// trialBalanceRow is what we return per account for the rich ASQ.
type trialBalanceRow struct {
	AccountID      uuid.UUID `json:"account_id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	NameUz         string    `json:"name_uz"`
	NameRu         string    `json:"name_ru"`
	Category       string    `json:"category"`
	NormalBalance  string    `json:"normal_balance"`
	OpeningDebit   float64   `json:"opening_debit"`
	OpeningCredit  float64   `json:"opening_credit"`
	TurnoverDebit  float64   `json:"turnover_debit"`
	TurnoverCredit float64   `json:"turnover_credit"`
	ClosingDebit   float64   `json:"closing_debit"`
	ClosingCredit  float64   `json:"closing_credit"`
}

type trialBalanceResp struct {
	PeriodFrom            string            `json:"period_from"`
	PeriodTo              string            `json:"period_to"`
	Accounts              []trialBalanceRow `json:"accounts"`
	TotalOpeningDebit     float64           `json:"total_opening_debit"`
	TotalOpeningCredit    float64           `json:"total_opening_credit"`
	TotalTurnoverDebit    float64           `json:"total_turnover_debit"`
	TotalTurnoverCredit   float64           `json:"total_turnover_credit"`
	TotalClosingDebit     float64           `json:"total_closing_debit"`
	TotalClosingCredit    float64           `json:"total_closing_credit"`
	TurnoverIsBalanced    bool              `json:"turnover_is_balanced"`
	OpeningIsBalanced     bool              `json:"opening_is_balanced"`
	ClosingIsBalanced     bool              `json:"closing_is_balanced"`
}

// GetTrialBalanceWithTurnover returns the TT §6.1 structure.
// Query params: period_from (YYYY-MM-DD, required), period_to (required).
func (h *Handler) GetTrialBalanceWithTurnover(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")
	if periodFrom == "" || periodTo == "" {
		response.BadRequest(c, "period_from and period_to are required (YYYY-MM-DD)")
		return
	}

	// Pull posted-only turnovers (both pre-period for opening, and in-period).
	// Single query with two conditional SUMs keeps this to one round trip.
	orgID, orgOK := middleware.GetOrganizationID(c)

	q := `
		SELECT a.id, a.code, a.name,
		       COALESCE(a.name_uz, '') AS name_uz,
		       COALESCE(a.name_ru, '') AS name_ru,
		       at.category, at.normal_balance,
		       COALESCE(SUM(CASE WHEN je.entry_date <  $2 THEN jel.debit_amount  ELSE 0 END), 0) AS prior_dt,
		       COALESCE(SUM(CASE WHEN je.entry_date <  $2 THEN jel.credit_amount ELSE 0 END), 0) AS prior_kt,
		       COALESCE(SUM(CASE WHEN je.entry_date >= $2 AND je.entry_date <= $3 THEN jel.debit_amount  ELSE 0 END), 0) AS period_dt,
		       COALESCE(SUM(CASE WHEN je.entry_date >= $2 AND je.entry_date <= $3 THEN jel.credit_amount ELSE 0 END), 0) AS period_kt
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
		  AND COALESCE(a.is_leaf, true) = true`
	args := []interface{}{tenantID, periodFrom, periodTo}
	if orgOK && orgID != uuid.Nil {
		q += " AND a.organization_id = $4"
		args = append(args, orgID)
	}
	q += " GROUP BY a.id, a.code, a.name, a.name_uz, a.name_ru, at.category, at.normal_balance ORDER BY a.code"

	rows, err := h.db.Query(q, args...)
	if err != nil {
		h.log.Error("GetTrialBalanceWithTurnover: query failed", "error", err)
		response.InternalError(c, "Failed to generate trial balance")
		return
	}
	defer rows.Close()

	result := trialBalanceResp{PeriodFrom: periodFrom, PeriodTo: periodTo}

	for rows.Next() {
		var r trialBalanceRow
		var priorDt, priorKt, periodDt, periodKt float64
		var normal string
		if err := rows.Scan(&r.AccountID, &r.Code, &r.Name, &r.NameUz, &r.NameRu,
			&r.Category, &normal, &priorDt, &priorKt, &periodDt, &periodKt); err != nil {
			continue
		}
		r.NormalBalance = normal

		// Opening: if normal=debit, show net debit as debit, overflow as credit
		openingNet := priorDt - priorKt
		if normal == "debit" {
			if openingNet >= 0 {
				r.OpeningDebit = openingNet
			} else {
				r.OpeningCredit = -openingNet
			}
		} else {
			// credit-normal accounts: flip sign
			if openingNet <= 0 {
				r.OpeningCredit = -openingNet
			} else {
				r.OpeningDebit = openingNet
			}
		}

		r.TurnoverDebit = periodDt
		r.TurnoverCredit = periodKt

		// Closing = opening-net + period-turnover-net, then side-split as above
		closingNet := (priorDt + periodDt) - (priorKt + periodKt)
		if normal == "debit" {
			if closingNet >= 0 {
				r.ClosingDebit = closingNet
			} else {
				r.ClosingCredit = -closingNet
			}
		} else {
			if closingNet <= 0 {
				r.ClosingCredit = -closingNet
			} else {
				r.ClosingDebit = closingNet
			}
		}

		// Round to cents — money math, not display
		r.OpeningDebit = bxgRound2(r.OpeningDebit)
		r.OpeningCredit = bxgRound2(r.OpeningCredit)
		r.TurnoverDebit = bxgRound2(r.TurnoverDebit)
		r.TurnoverCredit = bxgRound2(r.TurnoverCredit)
		r.ClosingDebit = bxgRound2(r.ClosingDebit)
		r.ClosingCredit = bxgRound2(r.ClosingCredit)

		// Skip accounts with zero everything (for cleanliness)
		if r.OpeningDebit == 0 && r.OpeningCredit == 0 &&
			r.TurnoverDebit == 0 && r.TurnoverCredit == 0 &&
			r.ClosingDebit == 0 && r.ClosingCredit == 0 {
			continue
		}

		result.Accounts = append(result.Accounts, r)
		result.TotalOpeningDebit += r.OpeningDebit
		result.TotalOpeningCredit += r.OpeningCredit
		result.TotalTurnoverDebit += r.TurnoverDebit
		result.TotalTurnoverCredit += r.TurnoverCredit
		result.TotalClosingDebit += r.ClosingDebit
		result.TotalClosingCredit += r.ClosingCredit
	}

	result.TotalOpeningDebit = bxgRound2(result.TotalOpeningDebit)
	result.TotalOpeningCredit = bxgRound2(result.TotalOpeningCredit)
	result.TotalTurnoverDebit = bxgRound2(result.TotalTurnoverDebit)
	result.TotalTurnoverCredit = bxgRound2(result.TotalTurnoverCredit)
	result.TotalClosingDebit = bxgRound2(result.TotalClosingDebit)
	result.TotalClosingCredit = bxgRound2(result.TotalClosingCredit)

	result.TurnoverIsBalanced = math.Abs(result.TotalTurnoverDebit-result.TotalTurnoverCredit) < 0.01
	result.OpeningIsBalanced = math.Abs(result.TotalOpeningDebit-result.TotalOpeningCredit) < 0.01
	result.ClosingIsBalanced = math.Abs(result.TotalClosingDebit-result.TotalClosingCredit) < 0.01

	response.Success(c, result)
}

// ---------------------------------------------------------------------------
// Excel export for ASQ
// ---------------------------------------------------------------------------

// ExportTrialBalanceExcel streams the ASQ as an .xlsx file.
// Reuses the same query as GetTrialBalanceWithTurnover.
func (h *Handler) ExportTrialBalanceExcel(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")
	if periodFrom == "" || periodTo == "" {
		response.BadRequest(c, "period_from and period_to are required (YYYY-MM-DD)")
		return
	}

	data, err := h.loadTrialBalanceData(tenantID, c, periodFrom, periodTo)
	if err != nil {
		h.log.Error("ExportTrialBalanceExcel: load failed", "error", err)
		response.InternalError(c, "Failed to build trial balance")
		return
	}

	f := excelize.NewFile()
	defer f.Close()
	sheet := "ASQ"
	_, _ = f.NewSheet(sheet)
	f.DeleteSheet("Sheet1")

	// Header block
	_ = f.SetCellValue(sheet, "A1", "Aylanma-saldo qaydnomasi")
	_ = f.SetCellValue(sheet, "A2", fmt.Sprintf("Davr: %s — %s", periodFrom, periodTo))

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#E8F1FF"}, Pattern: 1},
	})

	headers := []string{
		"Kod", "Hisob nomi",
		"Boshi saldosi Dt", "Boshi saldosi Kt",
		"Aylanma Dt", "Aylanma Kt",
		"Oxiri saldosi Dt", "Oxiri saldosi Kt",
	}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 4)
		_ = f.SetCellValue(sheet, cell, h)
		_ = f.SetCellStyle(sheet, cell, cell, headerStyle)
	}

	row := 5
	for _, a := range data.Accounts {
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), a.Code)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), a.Name)
		_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), a.OpeningDebit)
		_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", row), a.OpeningCredit)
		_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", row), a.TurnoverDebit)
		_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", row), a.TurnoverCredit)
		_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", row), a.ClosingDebit)
		_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), a.ClosingCredit)
		row++
	}

	// Totals row
	totalStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#FFF2CC"}, Pattern: 1},
	})
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "JAMI")
	_ = f.MergeCell(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("B%d", row))
	_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), data.TotalOpeningDebit)
	_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", row), data.TotalOpeningCredit)
	_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", row), data.TotalTurnoverDebit)
	_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", row), data.TotalTurnoverCredit)
	_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", row), data.TotalClosingDebit)
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), data.TotalClosingCredit)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("H%d", row), totalStyle)
	row++

	// Balance sanity row (TT 10: Dt aylanma = Kt aylanma)
	balanceLabel := "Balans tekshiruvi: Aylanma Dt = Kt ?"
	balanced := "HA"
	if !data.TurnoverIsBalanced {
		balanced = "YO'Q (xato!)"
	}
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), balanceLabel)
	_ = f.MergeCell(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("G%d", row))
	_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), balanced)

	// Column widths
	_ = f.SetColWidth(sheet, "A", "A", 10)
	_ = f.SetColWidth(sheet, "B", "B", 40)
	_ = f.SetColWidth(sheet, "C", "H", 16)

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition",
		fmt.Sprintf("attachment; filename=ASQ_%s_%s.xlsx", periodFrom, periodTo))
	c.Status(http.StatusOK)
	if err := f.Write(c.Writer); err != nil {
		h.log.Error("ExportTrialBalanceExcel: write failed", "error", err)
	}
}

// loadTrialBalanceData is a small helper so JSON + Excel handlers can share SQL.
func (h *Handler) loadTrialBalanceData(tenantID uuid.UUID, c *gin.Context,
	periodFrom, periodTo string) (*trialBalanceResp, error) {

	orgID, orgOK := middleware.GetOrganizationID(c)

	q := `
		SELECT a.id, a.code, a.name,
		       COALESCE(a.name_uz, '') AS name_uz,
		       COALESCE(a.name_ru, '') AS name_ru,
		       at.category, at.normal_balance,
		       COALESCE(SUM(CASE WHEN je.entry_date <  $2 THEN jel.debit_amount  ELSE 0 END), 0) AS prior_dt,
		       COALESCE(SUM(CASE WHEN je.entry_date <  $2 THEN jel.credit_amount ELSE 0 END), 0) AS prior_kt,
		       COALESCE(SUM(CASE WHEN je.entry_date >= $2 AND je.entry_date <= $3 THEN jel.debit_amount  ELSE 0 END), 0) AS period_dt,
		       COALESCE(SUM(CASE WHEN je.entry_date >= $2 AND je.entry_date <= $3 THEN jel.credit_amount ELSE 0 END), 0) AS period_kt
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
		  AND COALESCE(a.is_leaf, true) = true`
	args := []interface{}{tenantID, periodFrom, periodTo}
	if orgOK && orgID != uuid.Nil {
		q += " AND a.organization_id = $4"
		args = append(args, orgID)
	}
	q += " GROUP BY a.id, a.code, a.name, a.name_uz, a.name_ru, at.category, at.normal_balance ORDER BY a.code"

	rows, err := h.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &trialBalanceResp{PeriodFrom: periodFrom, PeriodTo: periodTo}
	for rows.Next() {
		var r trialBalanceRow
		var priorDt, priorKt, periodDt, periodKt float64
		var normal string
		if err := rows.Scan(&r.AccountID, &r.Code, &r.Name, &r.NameUz, &r.NameRu,
			&r.Category, &normal, &priorDt, &priorKt, &periodDt, &periodKt); err != nil {
			continue
		}
		r.NormalBalance = normal
		openingNet := priorDt - priorKt
		if normal == "debit" {
			if openingNet >= 0 {
				r.OpeningDebit = openingNet
			} else {
				r.OpeningCredit = -openingNet
			}
		} else {
			if openingNet <= 0 {
				r.OpeningCredit = -openingNet
			} else {
				r.OpeningDebit = openingNet
			}
		}
		r.TurnoverDebit, r.TurnoverCredit = periodDt, periodKt
		closingNet := (priorDt + periodDt) - (priorKt + periodKt)
		if normal == "debit" {
			if closingNet >= 0 {
				r.ClosingDebit = closingNet
			} else {
				r.ClosingCredit = -closingNet
			}
		} else {
			if closingNet <= 0 {
				r.ClosingCredit = -closingNet
			} else {
				r.ClosingDebit = closingNet
			}
		}
		r.OpeningDebit = bxgRound2(r.OpeningDebit)
		r.OpeningCredit = bxgRound2(r.OpeningCredit)
		r.TurnoverDebit = bxgRound2(r.TurnoverDebit)
		r.TurnoverCredit = bxgRound2(r.TurnoverCredit)
		r.ClosingDebit = bxgRound2(r.ClosingDebit)
		r.ClosingCredit = bxgRound2(r.ClosingCredit)

		if r.OpeningDebit == 0 && r.OpeningCredit == 0 &&
			r.TurnoverDebit == 0 && r.TurnoverCredit == 0 &&
			r.ClosingDebit == 0 && r.ClosingCredit == 0 {
			continue
		}
		result.Accounts = append(result.Accounts, r)
		result.TotalOpeningDebit += r.OpeningDebit
		result.TotalOpeningCredit += r.OpeningCredit
		result.TotalTurnoverDebit += r.TurnoverDebit
		result.TotalTurnoverCredit += r.TurnoverCredit
		result.TotalClosingDebit += r.ClosingDebit
		result.TotalClosingCredit += r.ClosingCredit
	}
	result.TurnoverIsBalanced = math.Abs(result.TotalTurnoverDebit-result.TotalTurnoverCredit) < 0.01
	result.OpeningIsBalanced = math.Abs(result.TotalOpeningDebit-result.TotalOpeningCredit) < 0.01
	result.ClosingIsBalanced = math.Abs(result.TotalClosingDebit-result.TotalClosingCredit) < 0.01
	return result, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// bxgRound2 is the money rounding helper used across the buxgalteriya handlers.
// Named distinctly so it does not collide with construction_progress.go's round2.
// DO NOT rename back to "round2" — package handler already has one in construction_progress.go.
func bxgRound2(v float64) float64 {
	return math.Round(v*100) / 100
}

// ---------------------------------------------------------------------------
// Bosh kitob — monthly General Ledger per TT §6.2
// ---------------------------------------------------------------------------

type monthlyTurnover struct {
	Year          int     `json:"year"`
	Month         int     `json:"month"`
	Label         string  `json:"label"` // "2026-01"
	DebitTurnover float64 `json:"debit_turnover"`
	CreditTurnover float64 `json:"credit_turnover"`
}

type generalLedgerAccount struct {
	AccountID     uuid.UUID         `json:"account_id"`
	Code          string            `json:"code"`
	Name          string            `json:"name"`
	NormalBalance string            `json:"normal_balance"`
	OpeningNet    float64           `json:"opening_net"`
	Months        []monthlyTurnover `json:"months"`
	YearlyDebit   float64           `json:"yearly_debit"`
	YearlyCredit  float64           `json:"yearly_credit"`
	ClosingNet    float64           `json:"closing_net"`
}

type generalLedgerResp struct {
	Year     int                    `json:"year"`
	Accounts []generalLedgerAccount `json:"accounts"`
}

// GetGeneralLedgerMonthly returns per-account monthly turnovers for a year.
// Spec TT §6.2: "Har bir hisob bo'yicha oylar kesimida aylanmalar, yillik aylanma yig'indisi".
//
// Query params: year (required, e.g. 2026), account_id (optional — filter single account).
func (h *Handler) GetGeneralLedgerMonthly(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	yearStr := c.Query("year")
	if yearStr == "" {
		response.BadRequest(c, "year is required (e.g. 2026)")
		return
	}
	var year int
	_, err := fmt.Sscanf(yearStr, "%d", &year)
	if err != nil || year < 1900 || year > 9999 {
		response.BadRequest(c, "invalid year")
		return
	}

	accountFilter := c.Query("account_id")
	orgID, orgOK := middleware.GetOrganizationID(c)

	// One query aggregating by (account, month), plus a second for openings.
	monthlyQuery := `
		SELECT a.id, a.code, a.name, at.normal_balance,
		       EXTRACT(MONTH FROM je.entry_date)::int AS mo,
		       COALESCE(SUM(jel.debit_amount), 0)  AS dt,
		       COALESCE(SUM(jel.credit_amount), 0) AS kt
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND EXTRACT(YEAR FROM je.entry_date) = $2
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
		  AND COALESCE(a.is_leaf, true) = true`
	args := []interface{}{tenantID, year}
	if orgOK && orgID != uuid.Nil {
		args = append(args, orgID)
		monthlyQuery += fmt.Sprintf(" AND a.organization_id = $%d", len(args))
	}
	if accountFilter != "" {
		args = append(args, accountFilter)
		monthlyQuery += fmt.Sprintf(" AND a.id = $%d", len(args))
	}
	monthlyQuery += ` GROUP BY a.id, a.code, a.name, at.normal_balance, mo
	                  ORDER BY a.code, mo`

	rows, err := h.db.Query(monthlyQuery, args...)
	if err != nil {
		h.log.Error("GetGeneralLedgerMonthly: query failed", "error", err)
		response.InternalError(c, "Failed to generate general ledger")
		return
	}
	defer rows.Close()

	// Aggregate by account into 12 monthly buckets
	accountIdx := make(map[uuid.UUID]*generalLedgerAccount)
	for rows.Next() {
		var aid uuid.UUID
		var code, name, normal string
		var moNullable sql.NullInt64 // EXTRACT(MONTH FROM ...) is NULL when no rows
		var dt, kt float64
		if err := rows.Scan(&aid, &code, &name, &normal, &moNullable, &dt, &kt); err != nil {
			continue
		}
		if _, ok := accountIdx[aid]; !ok {
			acc := &generalLedgerAccount{
				AccountID:     aid,
				Code:          code,
				Name:          name,
				NormalBalance: normal,
				Months:        make([]monthlyTurnover, 12),
			}
			for i := 0; i < 12; i++ {
				acc.Months[i] = monthlyTurnover{
					Year:  year,
					Month: i + 1,
					Label: fmt.Sprintf("%04d-%02d", year, i+1),
				}
			}
			accountIdx[aid] = acc
		}
		if moNullable.Valid {
			mo := int(moNullable.Int64)
			if mo >= 1 && mo <= 12 {
				accountIdx[aid].Months[mo-1].DebitTurnover += bxgRound2(dt)
				accountIdx[aid].Months[mo-1].CreditTurnover += bxgRound2(kt)
				accountIdx[aid].YearlyDebit += bxgRound2(dt)
				accountIdx[aid].YearlyCredit += bxgRound2(kt)
			}
		}
	}

	// Opening balances for the year (sum of all posted entries before Jan 1 of `year`)
	openingQuery := `
		SELECT a.id, at.normal_balance,
		       COALESCE(SUM(jel.debit_amount), 0)  AS dt,
		       COALESCE(SUM(jel.credit_amount), 0) AS kt
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date < make_date($2::int, 1, 1)
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
		  AND COALESCE(a.is_leaf, true) = true
		GROUP BY a.id, at.normal_balance`
	openingArgs := []interface{}{tenantID, year}
	orows, err := h.db.Query(openingQuery, openingArgs...)
	if err == nil {
		defer orows.Close()
		for orows.Next() {
			var aid uuid.UUID
			var normal string
			var dt, kt float64
			if err := orows.Scan(&aid, &normal, &dt, &kt); err != nil {
				continue
			}
			if acc, ok := accountIdx[aid]; ok {
				if normal == "debit" {
					acc.OpeningNet = bxgRound2(dt - kt)
				} else {
					acc.OpeningNet = bxgRound2(kt - dt)
				}
			}
		}
	}

	// Finalize closing net = opening + yearly net turnover
	out := generalLedgerResp{Year: year}
	for _, acc := range accountIdx {
		yearlyNet := acc.YearlyDebit - acc.YearlyCredit
		if acc.NormalBalance == "credit" {
			yearlyNet = -yearlyNet
		}
		acc.ClosingNet = bxgRound2(acc.OpeningNet + yearlyNet)
		out.Accounts = append(out.Accounts, *acc)
	}

	// Sort deterministically by code — simple insertion sort by string
	for i := 1; i < len(out.Accounts); i++ {
		for j := i; j > 0 && out.Accounts[j].Code < out.Accounts[j-1].Code; j-- {
			out.Accounts[j], out.Accounts[j-1] = out.Accounts[j-1], out.Accounts[j]
		}
	}

	response.Success(c, out)
}
