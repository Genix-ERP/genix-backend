package handler

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// FINANCIAL REPORTS
// =====================================================

// GetTrialBalance returns trial balance report
func (h *Handler) GetTrialBalance(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	asOfDate := c.Query("as_of_date")
	if asOfDate == "" {
		asOfDate = time.Now().Format("2006-01-02")
	}

	query := `
		SELECT a.id, a.code, a.name, COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''), COALESCE(a.name_ru, ''),
			   at.category, at.normal_balance, a.parent_id,
			   COALESCE(SUM(jel.debit_amount), 0) as total_debit,
			   COALESCE(SUM(jel.credit_amount), 0) as total_credit
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON a.id = jel.account_id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.entry_date <= $2 AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
	`
	args := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND a.organization_id = $3"
		args = append(args, orgID)
	}
	query += `
		GROUP BY a.id, a.code, a.name, a.name_uz, a.name_en, a.name_ru, at.category, at.normal_balance, a.parent_id
		ORDER BY a.code
	`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to get trial balance", "error", err)
		response.InternalError(c, "Failed to generate trial balance")
		return
	}
	defer rows.Close()

	allAccounts := make([]entity.TrialBalanceAccount, 0)
	var totalDebit, totalCredit float64

	for rows.Next() {
		var acc entity.TrialBalanceAccount
		var normalBalance string
		var debitSum, creditSum float64
		var parentID *uuid.UUID

		err := rows.Scan(&acc.AccountID, &acc.AccountCode, &acc.AccountName, &acc.AccountNameUz, &acc.AccountNameEn, &acc.AccountNameRu, &acc.Category, &normalBalance, &parentID, &debitSum, &creditSum)
		if err != nil {
			continue
		}

		acc.ParentID = parentID

		// Calculate balance based on normal balance
		if normalBalance == "debit" {
			acc.DebitBalance = debitSum - creditSum
			if acc.DebitBalance < 0 {
				acc.CreditBalance = -acc.DebitBalance
				acc.DebitBalance = 0
			}
		} else {
			acc.CreditBalance = creditSum - debitSum
			if acc.CreditBalance < 0 {
				acc.DebitBalance = -acc.CreditBalance
				acc.CreditBalance = 0
			}
		}

		allAccounts = append(allAccounts, acc)
	}

	// Build parent-child hierarchy (e.g., 1300 = sum of 1310+1320+1330+1340)
	parentMap := make(map[uuid.UUID]*entity.TrialBalanceAccount)
	childMap := make(map[uuid.UUID][]entity.TrialBalanceAccount)
	for i := range allAccounts {
		parentMap[allAccounts[i].AccountID] = &allAccounts[i]
		if allAccounts[i].ParentID != nil {
			childMap[*allAccounts[i].ParentID] = append(childMap[*allAccounts[i].ParentID], allAccounts[i])
		}
	}

	// Build final list: parent accounts get children nested, children are excluded from top level
	accounts := make([]entity.TrialBalanceAccount, 0)
	childIDs := make(map[uuid.UUID]bool)
	for parentID, children := range childMap {
		if parent, ok := parentMap[parentID]; ok {
			parent.IsParent = true
			parent.Children = children
			// Recalculate parent balance as sum of children
			var childDebit, childCredit float64
			for _, ch := range children {
				childDebit += ch.DebitBalance
				childCredit += ch.CreditBalance
			}
			if childDebit > 0 || childCredit > 0 {
				parent.DebitBalance = childDebit
				parent.CreditBalance = childCredit
			}
		}
		for _, ch := range children {
			childIDs[ch.AccountID] = true
		}
	}

	for _, acc := range allAccounts {
		if childIDs[acc.AccountID] {
			continue // Skip children at top level (they're nested)
		}
		if acc.DebitBalance == 0 && acc.CreditBalance == 0 && !acc.IsParent {
			continue // Skip zero-balance non-parent accounts
		}
		totalDebit += acc.DebitBalance
		totalCredit += acc.CreditBalance
		accounts = append(accounts, acc)
	}

	report := entity.TrialBalanceReport{
		AsOfDate:    asOfDate,
		TotalDebit:  math.Round(totalDebit*100) / 100,
		TotalCredit: math.Round(totalCredit*100) / 100,
		IsBalanced:  math.Abs(totalDebit-totalCredit) < 0.01,
		Accounts:    accounts,
	}

	response.Success(c, report)
}

// GetBalanceSheet returns balance sheet report
func (h *Handler) GetBalanceSheet(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	asOfDate := c.Query("as_of_date")
	if asOfDate == "" {
		asOfDate = time.Now().Format("2006-01-02")
	}

	// Query ALL account categories including revenue/expense to compute net income
	query := `
		SELECT a.id, a.code, a.name, COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''), COALESCE(a.name_ru, ''),
			   at.category, at.normal_balance,
			   a.opening_balance,
			   COALESCE(SUM(jel.debit_amount), 0) as total_debit,
			   COALESCE(SUM(jel.credit_amount), 0) as total_credit
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON a.id = jel.account_id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.entry_date <= $2 AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
			AND at.category IN ('asset', 'liability', 'equity', 'revenue', 'expense')
	`
	args := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND a.organization_id = $3"
		args = append(args, orgID)
	}
	query += `
		GROUP BY a.id, a.code, a.name, a.name_uz, a.name_en, a.name_ru, at.category, at.normal_balance, a.opening_balance
		ORDER BY at.category, a.code
	`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to get balance sheet", "error", err)
		response.InternalError(c, "Failed to generate balance sheet")
		return
	}
	defer rows.Close()

	assetAccounts := make([]entity.BalanceSheetAccount, 0)
	liabilityAccounts := make([]entity.BalanceSheetAccount, 0)
	equityAccounts := make([]entity.BalanceSheetAccount, 0)
	var totalAssets, totalLiabilities, totalEquity float64
	var totalRevenue, totalExpenses float64

	for rows.Next() {
		var accountID uuid.UUID
		var code, name, nameUz, nameEn, nameRu, category, normalBalance string
		var openingBalance, debitSum, creditSum float64

		err := rows.Scan(&accountID, &code, &name, &nameUz, &nameEn, &nameRu, &category, &normalBalance, &openingBalance, &debitSum, &creditSum)
		if err != nil {
			continue
		}

		// Calculate balance
		var balance float64
		if normalBalance == "debit" {
			balance = openingBalance + debitSum - creditSum
		} else {
			balance = openingBalance + creditSum - debitSum
		}

		// Revenue and expense accounts contribute to net income (shown in equity)
		// but are not displayed as separate balance sheet line items
		switch category {
		case "revenue":
			totalRevenue += balance
			continue
		case "expense":
			totalExpenses += balance
			continue
		}

		// Skip zero balances for assets/liabilities, but always show equity accounts
		if math.Abs(balance) < 0.01 && category != "equity" {
			continue
		}

		acc := entity.BalanceSheetAccount{
			AccountID:     accountID,
			AccountCode:   code,
			AccountName:   name,
			AccountNameUz: nameUz,
			AccountNameEn: nameEn,
			AccountNameRu: nameRu,
			Balance:       math.Round(balance*100) / 100,
		}

		switch category {
		case "asset":
			assetAccounts = append(assetAccounts, acc)
			totalAssets += balance
		case "liability":
			liabilityAccounts = append(liabilityAccounts, acc)
			totalLiabilities += balance
		case "equity":
			equityAccounts = append(equityAccounts, acc)
			totalEquity += balance
		}
	}

	// Net income = Revenue - Expenses (this is the current year's undistributed profit)
	netIncome := math.Round((totalRevenue - totalExpenses) * 100) / 100

	// Add net income as a line item in equity if it's non-zero
	// This represents "Joriy yil foydasi" (Current Year Profit/Loss)
	if math.Abs(netIncome) >= 0.01 {
		// Check if there's already a "8710" account in equityAccounts
		found := false
		for i, acc := range equityAccounts {
			if acc.AccountCode == "8710" {
				// Add net income to the existing account's balance
				equityAccounts[i].Balance += netIncome
				found = true
				break
			}
		}
		if !found {
			equityAccounts = append(equityAccounts, entity.BalanceSheetAccount{
				AccountCode:   "8710",
				AccountName:   "Joriy yil taqsimlanmagan foydasi",
				AccountNameUz: "Joriy yil taqsimlanmagan foydasi",
				AccountNameEn: "Current Year Undistributed Profit/Loss",
				AccountNameRu: "Нераспределённая прибыль текущего года",
				Balance:       netIncome,
			})
		}
		totalEquity += netIncome
	}

	report := entity.BalanceSheetReport{
		AsOfDate:         asOfDate,
		TotalAssets:      math.Round(totalAssets*100) / 100,
		TotalLiabilities: math.Round(totalLiabilities*100) / 100,
		TotalEquity:      math.Round(totalEquity*100) / 100,
		Assets: []entity.BalanceSheetSection{{
			Category: "Assets",
			Total:    math.Round(totalAssets*100) / 100,
			Accounts: assetAccounts,
		}},
		Liabilities: []entity.BalanceSheetSection{{
			Category: "Liabilities",
			Total:    math.Round(totalLiabilities*100) / 100,
			Accounts: liabilityAccounts,
		}},
		Equity: []entity.BalanceSheetSection{{
			Category: "Equity",
			Total:    math.Round(totalEquity*100) / 100,
			Accounts: equityAccounts,
		}},
	}

	response.Success(c, report)
}

// GetIncomeStatement returns income statement (P&L) report
func (h *Handler) GetIncomeStatement(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")

	// Default to current month
	now := time.Now()
	if periodFrom == "" {
		periodFrom = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	}
	if periodTo == "" {
		periodTo = now.Format("2006-01-02")
	}

	query := `
		SELECT a.id, a.code, a.name, COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''), COALESCE(a.name_ru, ''),
			   at.category, at.normal_balance, at.code as type_code,
			   COALESCE(SUM(jel.debit_amount), 0) as total_debit,
			   COALESCE(SUM(jel.credit_amount), 0) as total_credit
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON a.id = jel.account_id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted'
			AND je.entry_date >= $2 AND je.entry_date <= $3
			AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
			AND at.category IN ('revenue', 'expense')
	`
	args := []interface{}{tenantID, periodFrom, periodTo}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND a.organization_id = $4"
		args = append(args, orgID)
	}
	query += `
		GROUP BY a.id, a.code, a.name, a.name_uz, a.name_en, a.name_ru, at.category, at.normal_balance, at.code
		HAVING COALESCE(SUM(jel.debit_amount), 0) > 0 OR COALESCE(SUM(jel.credit_amount), 0) > 0
		ORDER BY at.category DESC, a.code
	`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to get income statement", "error", err)
		response.InternalError(c, "Failed to generate income statement")
		return
	}
	defer rows.Close()

	revenue := make([]entity.IncomeStatementSection, 0)
	costOfSales := make([]entity.IncomeStatementSection, 0)
	operatingExpenses := make([]entity.IncomeStatementSection, 0)
	otherIncome := make([]entity.IncomeStatementSection, 0)
	otherExpenses := make([]entity.IncomeStatementSection, 0)
	var totalRevenue, totalCOGS, totalOpex, totalOtherIncome, totalOtherExpenses float64

	for rows.Next() {
		var accountID uuid.UUID
		var code, name, nameUz, nameEn, nameRu, category, normalBalance, typeCode string
		var debitSum, creditSum float64

		err := rows.Scan(&accountID, &code, &name, &nameUz, &nameEn, &nameRu, &category, &normalBalance, &typeCode, &debitSum, &creditSum)
		if err != nil {
			continue
		}

		// Calculate amount
		var amount float64
		if category == "revenue" {
			amount = creditSum - debitSum // Revenue has credit normal balance
		} else {
			amount = debitSum - creditSum // Expense has debit normal balance
		}

		if math.Abs(amount) < 0.01 {
			continue
		}

		section := entity.IncomeStatementSection{
			AccountID:     accountID,
			AccountCode:   code,
			AccountName:   name,
			AccountNameUz: nameUz,
			AccountNameEn: nameEn,
			AccountNameRu: nameRu,
			Amount:        math.Round(amount*100) / 100,
		}

		// Categorize by account_type code, with account code range fallback
		// Account code ranges (Uzbekistan chart of accounts):
		//   4xxx = Revenue, 5xxx = COGS, 6xxx = Operating Expenses,
		//   7xxx = Other Expenses, 8xxx = Other Income, 9xxx = Other Expenses
		effectiveType := typeCode
		if category == "expense" && typeCode != "COGS" && typeCode != "OPEX" && typeCode != "OTHER_EXP" {
			// Fallback to account code range
			if len(code) > 0 {
				switch code[0] {
				case '5':
					effectiveType = "COGS"
				case '6':
					effectiveType = "OPEX"
				case '7', '9':
					effectiveType = "OTHER_EXP"
				}
			}
		}
		if category == "revenue" && typeCode != "REVENUE" && typeCode != "OTHER_INC" {
			if len(code) > 0 && (code[0] == '8') {
				effectiveType = "OTHER_INC"
			}
		}
		// For expense accounts coded 5xxx-6xxx assigned to OTHER_EXP, override to correct type
		if effectiveType == "OTHER_EXP" && len(code) > 0 {
			switch code[0] {
			case '5':
				effectiveType = "COGS"
			case '6':
				effectiveType = "OPEX"
			}
		}

		switch effectiveType {
		case "REVENUE":
			revenue = append(revenue, section)
			totalRevenue += amount
		case "OTHER_INC":
			otherIncome = append(otherIncome, section)
			totalOtherIncome += amount
		case "COGS":
			costOfSales = append(costOfSales, section)
			totalCOGS += amount
		case "OTHER_EXP":
			otherExpenses = append(otherExpenses, section)
			totalOtherExpenses += amount
		default:
			// OPEX and any other expense types
			operatingExpenses = append(operatingExpenses, section)
			totalOpex += amount
		}
	}

	grossProfit := totalRevenue - totalCOGS
	operatingProfit := grossProfit - totalOpex
	preTaxProfit := operatingProfit + totalOtherIncome - totalOtherExpenses

	// Income tax at 15% (Uzbekistan standard rate) — only if profit is positive
	var incomeTax float64
	if preTaxProfit > 0 {
		incomeTax = preTaxProfit * 0.15
	}
	netIncome := preTaxProfit - incomeTax
	totalExpenses := totalCOGS + totalOpex + totalOtherExpenses

	report := entity.IncomeStatementReport{
		PeriodFrom:        periodFrom,
		PeriodTo:          periodTo,
		TotalRevenue:      math.Round(totalRevenue*100) / 100,
		TotalExpenses:     math.Round(totalExpenses*100) / 100,
		GrossProfit:       math.Round(grossProfit*100) / 100,
		OperatingProfit:   math.Round(operatingProfit*100) / 100,
		PreTaxProfit:      math.Round(preTaxProfit*100) / 100,
		IncomeTax:         math.Round(incomeTax*100) / 100,
		NetIncome:         math.Round(netIncome*100) / 100,
		Revenue:           revenue,
		CostOfSales:       costOfSales,
		OperatingExpenses: operatingExpenses,
		OtherIncome:       otherIncome,
		OtherExpenses:     otherExpenses,
	}

	response.Success(c, report)
}

// GetGeneralLedger returns general ledger report
func (h *Handler) GetGeneralLedger(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")
	accountID := c.Query("account_id")

	now := time.Now()
	if periodFrom == "" {
		periodFrom = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	}
	if periodTo == "" {
		periodTo = now.Format("2006-01-02")
	}

	// Get accounts to include
	accountQuery := `
		SELECT a.id, a.code, a.name, a.opening_balance, at.normal_balance
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
	`
	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		accountQuery += fmt.Sprintf(" AND a.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if accountID != "" {
		argCount++
		accountQuery += fmt.Sprintf(" AND a.id = $%d", argCount)
		args = append(args, accountID)
	}

	accountQuery += " ORDER BY a.code"

	accountRows, err := h.db.Query(accountQuery, args...)
	if err != nil {
		h.log.Error("Failed to get accounts", "error", err)
		response.InternalError(c, "Failed to generate general ledger")
		return
	}
	defer accountRows.Close()

	accounts := make([]entity.GeneralLedgerAccount, 0)

	for accountRows.Next() {
		var acc entity.GeneralLedgerAccount
		var normalBalance string

		err := accountRows.Scan(&acc.AccountID, &acc.AccountCode, &acc.AccountName, &acc.OpeningBalance, &normalBalance)
		if err != nil {
			continue
		}

		// Get transactions for this account
		txQuery := `
			SELECT je.entry_date, je.entry_number, je.description, je.reference,
				   jel.debit_amount, jel.credit_amount
			FROM journal_entry_lines jel
			JOIN journal_entries je ON jel.journal_entry_id = je.id
			WHERE jel.account_id = $1 AND je.tenant_id = $2
				AND je.status = 'posted' AND je.deleted_at IS NULL
				AND je.entry_date >= $3 AND je.entry_date <= $4
			ORDER BY je.entry_date, je.entry_number
		`

		txRows, err := h.db.Query(txQuery, acc.AccountID, tenantID, periodFrom, periodTo)
		if err != nil {
			continue
		}

		acc.Transactions = make([]entity.GeneralLedgerTransaction, 0)
		runningBalance := acc.OpeningBalance

		for txRows.Next() {
			var tx entity.GeneralLedgerTransaction
			var entryDate time.Time
			var desc, ref *string

			err := txRows.Scan(&entryDate, &tx.EntryNumber, &desc, &ref, &tx.DebitAmount, &tx.CreditAmount)
			if err != nil {
				continue
			}

			tx.Date = entryDate.Format("2006-01-02")
			if desc != nil {
				tx.Description = *desc
			}
			if ref != nil {
				tx.Reference = *ref
			}

			// Calculate running balance
			if normalBalance == "debit" {
				runningBalance += tx.DebitAmount - tx.CreditAmount
			} else {
				runningBalance += tx.CreditAmount - tx.DebitAmount
			}
			tx.RunningBalance = math.Round(runningBalance*100) / 100

			acc.TotalDebit += tx.DebitAmount
			acc.TotalCredit += tx.CreditAmount
			acc.Transactions = append(acc.Transactions, tx)
		}
		txRows.Close()

		acc.ClosingBalance = runningBalance
		acc.TotalDebit = math.Round(acc.TotalDebit*100) / 100
		acc.TotalCredit = math.Round(acc.TotalCredit*100) / 100
		acc.ClosingBalance = math.Round(acc.ClosingBalance*100) / 100

		accounts = append(accounts, acc)
	}

	report := entity.GeneralLedgerReport{
		PeriodFrom: periodFrom,
		PeriodTo:   periodTo,
		Accounts:   accounts,
	}

	response.Success(c, report)
}

// GetCashFlow returns cash flow statement
func (h *Handler) GetCashFlow(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")

	now := time.Now()
	if periodFrom == "" {
		periodFrom = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	}
	if periodTo == "" {
		periodTo = now.Format("2006-01-02")
	}

	// Get cash/bank account balances (opening balance)
	cashQuery := `
		SELECT
			COALESCE(SUM(CASE WHEN je.entry_date < $2 THEN
				CASE WHEN at.normal_balance = 'debit' THEN jel.debit_amount - jel.credit_amount
				ELSE jel.credit_amount - jel.debit_amount END
			ELSE 0 END), 0) + COALESCE(SUM(a.opening_balance), 0) as opening_balance,
			COALESCE(SUM(CASE WHEN je.entry_date BETWEEN $2 AND $3 THEN jel.debit_amount ELSE 0 END), 0) as period_debits,
			COALESCE(SUM(CASE WHEN je.entry_date BETWEEN $2 AND $3 THEN jel.credit_amount ELSE 0 END), 0) as period_credits
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON a.id = jel.account_id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id AND je.status = 'posted' AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
			AND (a.is_bank_account = true OR at.code IN ('CASH'))
	`
	cashArgs := []interface{}{tenantID, periodFrom, periodTo}
	orgFilter := ""
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgFilter = fmt.Sprintf(" AND a.organization_id = $%d", len(cashArgs)+1)
		cashQuery += orgFilter
		cashArgs = append(cashArgs, orgID)
	}

	var openingCash, periodDebits, periodCredits float64
	err := h.db.QueryRow(cashQuery, cashArgs...).Scan(&openingCash, &periodDebits, &periodCredits)
	if err != nil {
		h.log.Error("Failed to get cash balances", "error", err)
	}

	// Cash flow mapping by account code prefix
	// Operating: 1100 (AR), 2000 (AP), 4xxx (Revenue), 5xxx (COGS), 6xxx (OpEx), 7900 (Other)
	// Investing: 1500 (Fixed Assets), 1510 (Depreciation), 1600 (Intangible/Investments)
	// Financing: 2100-2500 (Loans), 3xxx (Equity), 7000 (Interest)
	cfQuery := `
		SELECT
			a.code,
			COALESCE(NULLIF(a.name_uz, ''), a.name) as display_name,
			COALESCE(SUM(jel.debit_amount), 0) as total_debit,
			COALESCE(SUM(jel.credit_amount), 0) as total_credit,
			at.normal_balance
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		JOIN journal_entry_lines jel ON a.id = jel.account_id
		JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.entry_date BETWEEN $2 AND $3 AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
			AND at.code NOT IN ('CASH')
			AND a.is_bank_account = false
	`
	cfArgs := []interface{}{tenantID, periodFrom, periodTo}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		cfQuery += fmt.Sprintf(" AND a.organization_id = $%d", len(cfArgs)+1)
		cfArgs = append(cfArgs, orgID)
	}
	cfQuery += " GROUP BY a.code, a.name, a.name_uz, at.normal_balance ORDER BY a.code"

	rows, err := h.db.Query(cfQuery, cfArgs...)
	if err != nil {
		h.log.Error("Failed to get cash flow details", "error", err)
	}

	operatingItems := make([]entity.CashFlowItem, 0)
	investingItems := make([]entity.CashFlowItem, 0)
	financingItems := make([]entity.CashFlowItem, 0)
	var operatingTotal, investingTotal, financingTotal float64

	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var code, name, normalBalance string
			var debit, credit float64
			if err := rows.Scan(&code, &name, &debit, &credit, &normalBalance); err != nil {
				continue
			}

			// Calculate net cash impact
			var amount float64
			if normalBalance == "debit" {
				amount = debit - credit
			} else {
				amount = credit - debit
			}

			if math.Abs(amount) < 0.01 {
				continue
			}

			item := entity.CashFlowItem{
				Description: name,
				Amount:      math.Round(amount*100) / 100,
			}

			// Categorize by account code (Uzbekistan NAS chart of accounts)
			// NAS classification for cash flow statement:
			// Investing: 0100-0899 (fixed/intangible assets, investments, capital expenditures)
			// Financing: 6000-6999 (current liabilities), 7000-7999 (long-term liabilities), 8000-8999 (equity)
			// Operating: everything else (revenue, COGS, OpEx, AR, AP, etc.)
			switch {
			case code >= "0100" && code <= "0899":
				// Fixed assets (01xx-04xx), intangible assets (04xx),
				// long-term investments (06xx), equipment (08xx) → investing
				investingItems = append(investingItems, item)
				investingTotal += amount
			case code >= "0400" && code <= "0499":
				// Intangible assets (04xx) → investing
				investingItems = append(investingItems, item)
				investingTotal += amount
			case code >= "0600" && code <= "0699":
				// Long-term investments (06xx) → investing
				investingItems = append(investingItems, item)
				investingTotal += amount
			case code >= "6000" && code <= "6999":
				// Current liabilities / accounts payable (60xx-69xx) → financing
				financingItems = append(financingItems, item)
				financingTotal += amount
			case code >= "7000" && code <= "7999":
				// Long-term liabilities (70xx-79xx) → financing
				financingItems = append(financingItems, item)
				financingTotal += amount
			case code >= "8000" && code <= "8999":
				// Equity accounts (80xx-89xx) → financing
				financingItems = append(financingItems, item)
				financingTotal += amount
			default:
				// Everything else → operating (AR, AP, revenue, COGS, OpEx, etc.)
				operatingItems = append(operatingItems, item)
				operatingTotal += amount
			}
		}
	}

	netCashChange := periodDebits - periodCredits
	closingCash := openingCash + netCashChange

	report := entity.CashFlowReport{
		PeriodFrom:         periodFrom,
		PeriodTo:           periodTo,
		OpeningCashBalance: math.Round(openingCash*100) / 100,
		ClosingCashBalance: math.Round(closingCash*100) / 100,
		NetCashChange:      math.Round(netCashChange*100) / 100,
		OperatingActivities: entity.CashFlowSection{
			Total: math.Round(operatingTotal*100) / 100,
			Items: operatingItems,
		},
		InvestingActivities: entity.CashFlowSection{
			Total: math.Round(investingTotal*100) / 100,
			Items: investingItems,
		},
		FinancingActivities: entity.CashFlowSection{
			Total: math.Round(financingTotal*100) / 100,
			Items: financingItems,
		},
	}

	response.Success(c, report)
}

// GetAgingReceivables returns accounts receivable aging report
func (h *Handler) GetAgingReceivables(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	asOfDate := c.Query("as_of_date")
	if asOfDate == "" {
		asOfDate = time.Now().Format("2006-01-02")
	}

	// ── Step 1: Fetch ALL invoices (full total_amount, not net) ──
	query := `
		SELECT si.id, si.invoice_number, si.invoice_date, si.due_date, si.total_amount,
			   c.id as contact_id, c.name as contact_name,
			   ($2::date - si.due_date)::int as days_overdue
		FROM sales_invoices si
		JOIN contacts c ON si.customer_id = c.id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL
			AND si.status NOT IN ('cancelled')
			AND si.total_amount > 0
			AND COALESCE(si.invoice_type, 'invoice') = 'invoice'
			AND si.invoice_date <= $2::date
	`
	arArgs := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND si.organization_id = $3"
		arArgs = append(arArgs, orgID)
	}
	query += " ORDER BY c.name, si.due_date"

	rows, err := h.db.Query(query, arArgs...)
	if err != nil {
		h.log.Error("Failed to get aging receivables", "error", err)
		response.InternalError(c, "Failed to generate aging report")
		return
	}
	defer rows.Close()

	contactMap := make(map[uuid.UUID]*entity.AgingContact)
	// Track which contacts have invoices so we can include their payments
	contactHasInvoices := make(map[uuid.UUID]bool)
	var totalAmount, currentTotal, days1To30, days31To60, days61To90, over90Days float64

	for rows.Next() {
		var invoiceID, contactID uuid.UUID
		var invoiceNumber, contactName string
		var invoiceDate, dueDate time.Time
		var total float64
		var daysOverdue int

		err := rows.Scan(&invoiceID, &invoiceNumber, &invoiceDate, &dueDate, &total,
			&contactID, &contactName, &daysOverdue)
		if err != nil {
			continue
		}

		contactHasInvoices[contactID] = true

		// Get or create contact entry
		contact, exists := contactMap[contactID]
		if !exists {
			contact = &entity.AgingContact{
				ContactID:   contactID,
				ContactName: contactName,
				Invoices:    make([]entity.AgingInvoice, 0),
			}
			contactMap[contactID] = contact
		}

		// Determine aging bucket based on due date
		var bucket string
		if daysOverdue <= 0 {
			bucket = "current"
			contact.Current += total
			currentTotal += total
		} else if daysOverdue <= 30 {
			bucket = "1-30"
			contact.Days1To30 += total
			days1To30 += total
		} else if daysOverdue <= 60 {
			bucket = "31-60"
			contact.Days31To60 += total
			days31To60 += total
		} else if daysOverdue <= 90 {
			bucket = "61-90"
			contact.Days61To90 += total
			days61To90 += total
		} else {
			bucket = "90+"
			contact.Over90Days += total
			over90Days += total
		}

		invoice := entity.AgingInvoice{
			InvoiceID:     invoiceID,
			InvoiceNumber: invoiceNumber,
			InvoiceDate:   invoiceDate.Format("2006-01-02"),
			DueDate:       dueDate.Format("2006-01-02"),
			TotalAmount:   total,
			AmountDue:     total,
			DaysOverdue:   daysOverdue,
			AgingBucket:   bucket,
		}

		contact.Invoices = append(contact.Invoices, invoice)
		contact.TotalAmount += total
		totalAmount += total
	}

	// ── Step 2: Fetch ALL confirmed payments as negative entries (like Odoo) ──
	payQuery := `
		SELECT p.id, p.payment_number, p.payment_date, p.amount,
		       c.id as contact_id, c.name as contact_name,
		       ($2::date - p.payment_date)::int as days_since
		FROM payments p
		JOIN contacts c ON p.contact_id = c.id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND p.type = 'receipt' AND p.status = 'confirmed'
		  AND p.payment_date <= $2::date
	`
	payArgs := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		payQuery += " AND p.organization_id = $3"
		payArgs = append(payArgs, orgID)
	}
	payQuery += " ORDER BY c.name, p.payment_date"

	payRows, payErr := h.db.Query(payQuery, payArgs...)
	if payErr == nil {
		defer payRows.Close()
		for payRows.Next() {
			var payID, contactID uuid.UUID
			var payNumber, contactName string
			var payDate time.Time
			var amount float64
			var daysSince int

			if err := payRows.Scan(&payID, &payNumber, &payDate, &amount, &contactID, &contactName, &daysSince); err != nil {
				continue
			}

			negativeAmount := -amount

			contact, exists := contactMap[contactID]
			if !exists {
				// Only include payments for contacts that also have invoices
				if !contactHasInvoices[contactID] {
					continue
				}
				contact = &entity.AgingContact{
					ContactID:   contactID,
					ContactName: contactName,
					Invoices:    make([]entity.AgingInvoice, 0),
				}
				contactMap[contactID] = contact
			}

			// All payments go into "current" (Not Due) bucket as negative
			contact.Current += negativeAmount
			currentTotal += negativeAmount

			payEntry := entity.AgingInvoice{
				InvoiceID:     payID,
				InvoiceNumber: payNumber,
				InvoiceDate:   payDate.Format("2006-01-02"),
				DueDate:       payDate.Format("2006-01-02"),
				TotalAmount:   negativeAmount,
				AmountDue:     negativeAmount,
				DaysOverdue:   0,
				AgingBucket:   "current",
			}

			contact.Invoices = append(contact.Invoices, payEntry)
			contact.TotalAmount += negativeAmount
			totalAmount += negativeAmount
		}
	}

	// ── Step 3: Subtract approved sales returns (credit notes) ──
	retQuery := `
		SELECT sr.id, sr.return_number, COALESCE(sr.approved_at, sr.return_date), sr.total_amount,
		       sr.customer_id, COALESCE(c.name, sr.customer_name, '') as contact_name
		FROM sales_returns sr
		LEFT JOIN contacts c ON sr.customer_id = c.id
		WHERE sr.tenant_id = $1 AND sr.deleted_at IS NULL
		  AND sr.status IN ('approved', 'completed')
		  AND sr.customer_id IS NOT NULL
		  AND COALESCE(sr.approved_at, sr.return_date) <= $2::date
	`
	retArgs := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		retQuery += " AND sr.organization_id = $3"
		retArgs = append(retArgs, orgID)
	}
	retRows, retErr := h.db.Query(retQuery, retArgs...)
	if retErr == nil {
		defer retRows.Close()
		for retRows.Next() {
			var retID, contactID uuid.UUID
			var retNumber, contactName string
			var retDate time.Time
			var amount float64
			if err := retRows.Scan(&retID, &retNumber, &retDate, &amount, &contactID, &contactName); err != nil {
				continue
			}
			if amount <= 0 {
				continue
			}
			negativeAmount := -amount
			contact, exists := contactMap[contactID]
			if !exists {
				if !contactHasInvoices[contactID] {
					continue
				}
				contact = &entity.AgingContact{
					ContactID:   contactID,
					ContactName: contactName,
					Invoices:    make([]entity.AgingInvoice, 0),
				}
				contactMap[contactID] = contact
			}
			contact.Current += negativeAmount
			currentTotal += negativeAmount
			retEntry := entity.AgingInvoice{
				InvoiceID:     retID,
				InvoiceNumber: "CN-" + retNumber,
				InvoiceDate:   retDate.Format("2006-01-02"),
				DueDate:       retDate.Format("2006-01-02"),
				TotalAmount:   negativeAmount,
				AmountDue:     negativeAmount,
				DaysOverdue:   0,
				AgingBucket:   "current",
			}
			contact.Invoices = append(contact.Invoices, retEntry)
			contact.TotalAmount += negativeAmount
			totalAmount += negativeAmount
		}
	}

	// ── Step 4: FIFO-apply credits (payments + credit notes) against oldest invoices ──
	// Credits consume the oldest invoices first (FIFO). Once a credit is consumed, its
	// amount_due is zeroed so line items and bucket totals stay consistent on the UI.
	// Leftover credit (if credits exceed invoices) shows as a negative in "Not Due".
	totalAmount = 0
	currentTotal = 0
	days1To30 = 0
	days31To60 = 0
	days61To90 = 0
	over90Days = 0

	for _, contact := range contactMap {
		var creditIdx, invoiceIdx []int
		for i := range contact.Invoices {
			if contact.Invoices[i].TotalAmount < 0 {
				creditIdx = append(creditIdx, i)
			} else {
				invoiceIdx = append(invoiceIdx, i)
			}
		}

		// Sort both oldest-first by date
		sort.SliceStable(invoiceIdx, func(a, b int) bool {
			return contact.Invoices[invoiceIdx[a]].DueDate < contact.Invoices[invoiceIdx[b]].DueDate
		})
		sort.SliceStable(creditIdx, func(a, b int) bool {
			return contact.Invoices[creditIdx[a]].DueDate < contact.Invoices[creditIdx[b]].DueDate
		})

		var totalCredits float64
		for _, ci := range creditIdx {
			totalCredits += -contact.Invoices[ci].TotalAmount
		}

		// Reset per-contact bucket accumulators; rebuild from post-FIFO amount_due
		contact.Current = 0
		contact.Days1To30 = 0
		contact.Days31To60 = 0
		contact.Days61To90 = 0
		contact.Over90Days = 0
		contact.TotalAmount = 0

		// Apply credits FIFO against invoices, reducing each invoice's amount_due
		remaining := totalCredits
		for _, ii := range invoiceIdx {
			inv := &contact.Invoices[ii]
			applied := math.Min(remaining, inv.TotalAmount)
			inv.AmountDue = inv.TotalAmount - applied
			remaining -= applied

			switch inv.AgingBucket {
			case "current":
				contact.Current += inv.AmountDue
			case "1-30":
				contact.Days1To30 += inv.AmountDue
			case "31-60":
				contact.Days31To60 += inv.AmountDue
			case "61-90":
				contact.Days61To90 += inv.AmountDue
			case "90+":
				contact.Over90Days += inv.AmountDue
			}
			contact.TotalAmount += inv.AmountDue
		}

		// Mirror consumption back onto credit line items so UI stays consistent:
		// consumed credits get amount_due=0; unconsumed (overpayment) retain negative amount_due
		appliedCredit := totalCredits - remaining
		for _, ci := range creditIdx {
			credit := &contact.Invoices[ci]
			creditAmount := -credit.TotalAmount
			switch {
			case appliedCredit >= creditAmount:
				credit.AmountDue = 0
				appliedCredit -= creditAmount
			case appliedCredit > 0:
				credit.AmountDue = -(creditAmount - appliedCredit)
				appliedCredit = 0
			default:
				credit.AmountDue = credit.TotalAmount
			}
			contact.Current += credit.AmountDue
			contact.TotalAmount += credit.AmountDue
		}
	}

	// Drop fully-settled contacts, then accumulate grand totals
	for id, contact := range contactMap {
		if math.Abs(contact.TotalAmount) < 0.005 {
			delete(contactMap, id)
			continue
		}
		currentTotal += contact.Current
		days1To30 += contact.Days1To30
		days31To60 += contact.Days31To60
		days61To90 += contact.Days61To90
		over90Days += contact.Over90Days
		totalAmount += contact.TotalAmount
	}

	// Convert map to slice
	contacts := make([]entity.AgingContact, 0, len(contactMap))
	for _, c := range contactMap {
		contacts = append(contacts, *c)
	}

	report := entity.AgingReport{
		AsOfDate:     asOfDate,
		ReportType:   "receivables",
		TotalAmount:  math.Round(totalAmount*100) / 100,
		CurrentTotal: math.Round(currentTotal*100) / 100,
		Days1To30:    math.Round(days1To30*100) / 100,
		Days31To60:   math.Round(days31To60*100) / 100,
		Days61To90:   math.Round(days61To90*100) / 100,
		Over90Days:   math.Round(over90Days*100) / 100,
		Contacts:     contacts,
	}

	response.Success(c, report)
}

// GetAgingPayables returns accounts payable aging report
func (h *Handler) GetAgingPayables(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	asOfDate := c.Query("as_of_date")
	if asOfDate == "" {
		asOfDate = time.Now().Format("2006-01-02")
	}

	// ── Step 1: Fetch ALL vendor invoices (full total_amount) ──
	query := `
		SELECT pi.id, pi.invoice_number, pi.invoice_date, pi.due_date, pi.total_amount,
			   c.id as contact_id, c.name as contact_name,
			   ($2::date - pi.due_date)::int as days_overdue
		FROM purchase_invoices pi
		JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.tenant_id = $1 AND pi.deleted_at IS NULL
			AND pi.status NOT IN ('cancelled')
			AND pi.total_amount > 0
			AND pi.invoice_date <= $2::date
	`
	apArgs := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND pi.organization_id = $3"
		apArgs = append(apArgs, orgID)
	}
	query += " ORDER BY c.name, pi.due_date"

	rows, err := h.db.Query(query, apArgs...)
	if err != nil {
		h.log.Error("Failed to get aging payables", "error", err)
		response.InternalError(c, "Failed to generate aging report")
		return
	}
	defer rows.Close()

	contactMap := make(map[uuid.UUID]*entity.AgingContact)
	contactHasInvoices := make(map[uuid.UUID]bool)
	var totalAmount, currentTotal, days1To30, days31To60, days61To90, over90Days float64

	for rows.Next() {
		var invoiceID, contactID uuid.UUID
		var invoiceNumber, contactName string
		var invoiceDate, dueDate time.Time
		var total float64
		var daysOverdue int

		err := rows.Scan(&invoiceID, &invoiceNumber, &invoiceDate, &dueDate, &total,
			&contactID, &contactName, &daysOverdue)
		if err != nil {
			continue
		}

		contactHasInvoices[contactID] = true

		contact, exists := contactMap[contactID]
		if !exists {
			contact = &entity.AgingContact{
				ContactID:   contactID,
				ContactName: contactName,
				Invoices:    make([]entity.AgingInvoice, 0),
			}
			contactMap[contactID] = contact
		}

		var bucket string
		if daysOverdue <= 0 {
			bucket = "current"
			contact.Current += total
			currentTotal += total
		} else if daysOverdue <= 30 {
			bucket = "1-30"
			contact.Days1To30 += total
			days1To30 += total
		} else if daysOverdue <= 60 {
			bucket = "31-60"
			contact.Days31To60 += total
			days31To60 += total
		} else if daysOverdue <= 90 {
			bucket = "61-90"
			contact.Days61To90 += total
			days61To90 += total
		} else {
			bucket = "90+"
			contact.Over90Days += total
			over90Days += total
		}

		invoice := entity.AgingInvoice{
			InvoiceID:     invoiceID,
			InvoiceNumber: invoiceNumber,
			InvoiceDate:   invoiceDate.Format("2006-01-02"),
			DueDate:       dueDate.Format("2006-01-02"),
			TotalAmount:   total,
			AmountDue:     total,
			DaysOverdue:   daysOverdue,
			AgingBucket:   bucket,
		}

		contact.Invoices = append(contact.Invoices, invoice)
		contact.TotalAmount += total
		totalAmount += total
	}

	// ── Step 2: Fetch ALL confirmed vendor payments as negative entries ──
	payQuery := `
		SELECT p.id, p.payment_number, p.payment_date, p.amount,
		       c.id as contact_id, c.name as contact_name,
		       ($2::date - p.payment_date)::int as days_since
		FROM payments p
		JOIN contacts c ON p.contact_id = c.id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND p.type = 'payment' AND p.status = 'confirmed'
		  AND p.payment_date <= $2::date
	`
	payArgs := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		payQuery += " AND p.organization_id = $3"
		payArgs = append(payArgs, orgID)
	}
	payQuery += " ORDER BY c.name, p.payment_date"

	payRows, payErr := h.db.Query(payQuery, payArgs...)
	if payErr == nil {
		defer payRows.Close()
		for payRows.Next() {
			var payID, contactID uuid.UUID
			var payNumber, contactName string
			var payDate time.Time
			var amount float64
			var daysSince int

			if err := payRows.Scan(&payID, &payNumber, &payDate, &amount, &contactID, &contactName, &daysSince); err != nil {
				continue
			}

			negativeAmount := -amount

			contact, exists := contactMap[contactID]
			if !exists {
				if !contactHasInvoices[contactID] {
					continue
				}
				contact = &entity.AgingContact{
					ContactID:   contactID,
					ContactName: contactName,
					Invoices:    make([]entity.AgingInvoice, 0),
				}
				contactMap[contactID] = contact
			}

			// All payments go into "current" (Not Due) bucket as negative
			contact.Current += negativeAmount
			currentTotal += negativeAmount

			payEntry := entity.AgingInvoice{
				InvoiceID:     payID,
				InvoiceNumber: payNumber,
				InvoiceDate:   payDate.Format("2006-01-02"),
				DueDate:       payDate.Format("2006-01-02"),
				TotalAmount:   negativeAmount,
				AmountDue:     negativeAmount,
				DaysOverdue:   0,
				AgingBucket:   "current",
			}

			contact.Invoices = append(contact.Invoices, payEntry)
			contact.TotalAmount += negativeAmount
			totalAmount += negativeAmount
		}
	}

	// ── Step 3: FIFO-apply vendor payments against oldest vendor invoices ──
	totalAmount = 0
	currentTotal = 0
	days1To30 = 0
	days31To60 = 0
	days61To90 = 0
	over90Days = 0

	for _, contact := range contactMap {
		var creditIdx, invoiceIdx []int
		for i := range contact.Invoices {
			if contact.Invoices[i].TotalAmount < 0 {
				creditIdx = append(creditIdx, i)
			} else {
				invoiceIdx = append(invoiceIdx, i)
			}
		}

		sort.SliceStable(invoiceIdx, func(a, b int) bool {
			return contact.Invoices[invoiceIdx[a]].DueDate < contact.Invoices[invoiceIdx[b]].DueDate
		})
		sort.SliceStable(creditIdx, func(a, b int) bool {
			return contact.Invoices[creditIdx[a]].DueDate < contact.Invoices[creditIdx[b]].DueDate
		})

		var totalCredits float64
		for _, ci := range creditIdx {
			totalCredits += -contact.Invoices[ci].TotalAmount
		}

		contact.Current = 0
		contact.Days1To30 = 0
		contact.Days31To60 = 0
		contact.Days61To90 = 0
		contact.Over90Days = 0
		contact.TotalAmount = 0

		remaining := totalCredits
		for _, ii := range invoiceIdx {
			inv := &contact.Invoices[ii]
			applied := math.Min(remaining, inv.TotalAmount)
			inv.AmountDue = inv.TotalAmount - applied
			remaining -= applied

			switch inv.AgingBucket {
			case "current":
				contact.Current += inv.AmountDue
			case "1-30":
				contact.Days1To30 += inv.AmountDue
			case "31-60":
				contact.Days31To60 += inv.AmountDue
			case "61-90":
				contact.Days61To90 += inv.AmountDue
			case "90+":
				contact.Over90Days += inv.AmountDue
			}
			contact.TotalAmount += inv.AmountDue
		}

		appliedCredit := totalCredits - remaining
		for _, ci := range creditIdx {
			credit := &contact.Invoices[ci]
			creditAmount := -credit.TotalAmount
			switch {
			case appliedCredit >= creditAmount:
				credit.AmountDue = 0
				appliedCredit -= creditAmount
			case appliedCredit > 0:
				credit.AmountDue = -(creditAmount - appliedCredit)
				appliedCredit = 0
			default:
				credit.AmountDue = credit.TotalAmount
			}
			contact.Current += credit.AmountDue
			contact.TotalAmount += credit.AmountDue
		}
	}

	for id, contact := range contactMap {
		if math.Abs(contact.TotalAmount) < 0.005 {
			delete(contactMap, id)
			continue
		}
		currentTotal += contact.Current
		days1To30 += contact.Days1To30
		days31To60 += contact.Days31To60
		days61To90 += contact.Days61To90
		over90Days += contact.Over90Days
		totalAmount += contact.TotalAmount
	}

	contacts := make([]entity.AgingContact, 0, len(contactMap))
	for _, c := range contactMap {
		contacts = append(contacts, *c)
	}

	report := entity.AgingReport{
		AsOfDate:     asOfDate,
		ReportType:   "payables",
		TotalAmount:  math.Round(totalAmount*100) / 100,
		CurrentTotal: math.Round(currentTotal*100) / 100,
		Days1To30:    math.Round(days1To30*100) / 100,
		Days31To60:   math.Round(days31To60*100) / 100,
		Days61To90:   math.Round(days61To90*100) / 100,
		Over90Days:   math.Round(over90Days*100) / 100,
		Contacts:     contacts,
	}

	response.Success(c, report)
}

// GetSalesSummary returns comprehensive sales dashboard stats in a single response
func (h *Handler) GetSalesSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID, hasOrg := middleware.GetOrganizationID(c)
	orgFilter := ""
	args := []interface{}{tenantID}
	if hasOrg && orgID != uuid.Nil {
		orgFilter = " AND organization_id = $2"
		args = append(args, orgID)
	}

	// Sales orders: total, revenue, active
	var totalOrders, activeOrders int
	var totalRevenue float64
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(SUM(total_amount), 0),
		       COUNT(*) FILTER (WHERE status IN ('draft','quotation','confirmed','processing','shipped'))
		FROM sales_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL%s
	`, orgFilter), args...).Scan(&totalOrders, &totalRevenue, &activeOrders)

	// Quotations
	var totalQuotations, pendingQuotations int
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'sent')
		FROM quotations
		WHERE tenant_id = $1 AND deleted_at IS NULL%s
	`, orgFilter), args...).Scan(&totalQuotations, &pendingQuotations)

	// Invoices
	var totalInvoices, unpaidInvoices int
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE payment_status IN ('unpaid','partial'))
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL%s
	`, orgFilter), args...).Scan(&totalInvoices, &unpaidInvoices)

	// Returns
	var totalReturns, pendingReturns int
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'pending')
		FROM sales_returns
		WHERE tenant_id = $1 AND deleted_at IS NULL%s
	`, orgFilter), args...).Scan(&totalReturns, &pendingReturns)

	// Active discounts
	var activeDiscounts int
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COUNT(*) FILTER (WHERE is_active = true)
		FROM discounts
		WHERE tenant_id = $1 AND deleted_at IS NULL%s
	`, orgFilter), args...).Scan(&activeDiscounts)

	// Chart data: revenue per month for last 6 months
	type ChartPoint struct {
		Month   string  `json:"month"`
		Revenue float64 `json:"revenue"`
	}
	chartData := make([]ChartPoint, 0)
	chartRows, err := h.db.Query(fmt.Sprintf(`
		SELECT TO_CHAR(order_date, 'YYYY-MM') AS month,
		       COALESCE(SUM(total_amount), 0) AS revenue
		FROM sales_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND order_date >= NOW() - INTERVAL '6 months'%s
		GROUP BY TO_CHAR(order_date, 'YYYY-MM')
		ORDER BY month
	`, orgFilter), args...)
	if err == nil {
		defer chartRows.Close()
		for chartRows.Next() {
			var p ChartPoint
			if err := chartRows.Scan(&p.Month, &p.Revenue); err == nil {
				chartData = append(chartData, p)
			}
		}
	}

	response.Success(c, gin.H{
		"total_orders":       totalOrders,
		"total_revenue":      totalRevenue,
		"active_orders":      activeOrders,
		"total_quotations":   totalQuotations,
		"pending_quotations": pendingQuotations,
		"total_invoices":     totalInvoices,
		"unpaid_invoices":    unpaidInvoices,
		"total_returns":      totalReturns,
		"pending_returns":    pendingReturns,
		"active_discounts":   activeDiscounts,
		"chart_data":         chartData,
	})
}

// GetInventoryReport returns inventory summary report
func (h *Handler) GetInventoryReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	warehouseID := c.Query("warehouse_id")
	lowStock := c.Query("low_stock") == "true"

	baseQuery := `
		SELECT p.id, p.code, p.name, p.sku,
			   COALESCE(SUM(i.quantity_on_hand), 0) as quantity_on_hand,
			   COALESCE(SUM(i.quantity_reserved), 0) as quantity_reserved,
			   COALESCE(SUM(i.quantity_available), 0) as quantity_available,
			   COALESCE(SUM(i.total_value), 0) as total_value,
			   p.min_stock_level, p.reorder_point, p.reorder_quantity
		FROM products p
		LEFT JOIN inventory i ON p.id = i.product_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
	`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND p.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if warehouseID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND i.warehouse_id = $%d", argCount)
		args = append(args, warehouseID)
	}

	baseQuery += " GROUP BY p.id, p.code, p.name, p.sku, p.min_stock_level, p.reorder_point, p.reorder_quantity"

	if lowStock {
		baseQuery += " HAVING COALESCE(SUM(i.quantity_available), 0) <= COALESCE(p.reorder_point, 0)"
	}

	baseQuery += " ORDER BY p.code"

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to get inventory report", "error", err)
		response.InternalError(c, "Failed to generate inventory report")
		return
	}
	defer rows.Close()

	type InventoryItem struct {
		ProductID         uuid.UUID `json:"product_id"`
		ProductCode       string    `json:"product_code"`
		ProductName       string    `json:"product_name"`
		SKU               *string   `json:"sku,omitempty"`
		QuantityOnHand    float64   `json:"quantity_on_hand"`
		QuantityReserved  float64   `json:"quantity_reserved"`
		QuantityAvailable float64   `json:"quantity_available"`
		TotalValue        float64   `json:"total_value"`
		MinStockLevel     float64   `json:"min_stock_level"`
		ReorderPoint      float64   `json:"reorder_point"`
		ReorderQuantity   float64   `json:"reorder_quantity"`
		NeedsReorder      bool      `json:"needs_reorder"`
	}

	items := make([]InventoryItem, 0)
	var totalValue float64
	var lowStockCount int

	for rows.Next() {
		var item InventoryItem
		var sku *string
		var minStock, reorderPoint, reorderQty *float64

		err := rows.Scan(&item.ProductID, &item.ProductCode, &item.ProductName, &sku,
			&item.QuantityOnHand, &item.QuantityReserved, &item.QuantityAvailable, &item.TotalValue,
			&minStock, &reorderPoint, &reorderQty)
		if err != nil {
			continue
		}

		item.SKU = sku
		if minStock != nil {
			item.MinStockLevel = *minStock
		}
		if reorderPoint != nil {
			item.ReorderPoint = *reorderPoint
		}
		if reorderQty != nil {
			item.ReorderQuantity = *reorderQty
		}

		item.NeedsReorder = item.QuantityAvailable <= item.ReorderPoint
		if item.NeedsReorder {
			lowStockCount++
		}

		totalValue += item.TotalValue
		items = append(items, item)
	}

	response.Success(c, gin.H{
		"items":           items,
		"total_items":     len(items),
		"total_value":     math.Round(totalValue*100) / 100,
		"low_stock_count": lowStockCount,
	})
}

// GetAccountCard returns hisob kartochkasi (account card) - detailed transaction
// history for a single account with counterpart accounts and running balance
func (h *Handler) GetAccountCard(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	accountID := c.Query("account_id")
	if accountID == "" {
		response.BadRequest(c, "account_id is required")
		return
	}

	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")
	counterpartCode := c.Query("counterpart_code")
	contactName := c.Query("contact_name")
	// TT §6.3: filters — summa (amount range), hujjat turi (doc type)
	docType := c.Query("doc_type")
	amountMinStr := c.Query("amount_min")
	amountMaxStr := c.Query("amount_max")

	now := time.Now()
	if periodFrom == "" {
		periodFrom = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	}
	if periodTo == "" {
		periodTo = now.Format("2006-01-02")
	}

	// Get account info
	var accID uuid.UUID
	var accCode, accName, normalBalance string
	var openingBalance float64

	accQuery := `
		SELECT a.id, a.code, a.name, a.opening_balance, at.normal_balance
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		WHERE a.id = $1 AND a.tenant_id = $2 AND a.deleted_at IS NULL
	`
	err := h.db.QueryRow(accQuery, accountID, tenantID).Scan(&accID, &accCode, &accName, &openingBalance, &normalBalance)
	if err != nil {
		h.log.Error("Account not found", "error", err)
		response.NotFound(c, "Account not found")
		return
	}

	// Calculate opening balance: account's opening_balance + all posted transactions BEFORE periodFrom
	priorQuery := `
		SELECT COALESCE(SUM(jel.debit_amount), 0), COALESCE(SUM(jel.credit_amount), 0)
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE jel.account_id = $1 AND je.tenant_id = $2
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date < $3
	`
	var priorDebit, priorCredit float64
	err = h.db.QueryRow(priorQuery, accID, tenantID, periodFrom).Scan(&priorDebit, &priorCredit)
	if err != nil {
		priorDebit = 0
		priorCredit = 0
	}

	calcOpeningBalance := openingBalance
	if normalBalance == "debit" {
		calcOpeningBalance += priorDebit - priorCredit
	} else {
		calcOpeningBalance += priorCredit - priorDebit
	}
	calcOpeningBalance = math.Round(calcOpeningBalance*100) / 100

	// Get transactions with counterpart accounts
	txQuery := `
		SELECT je.id, je.entry_date, je.entry_number, COALESCE(je.doc_type, ''),
			   COALESCE(je.description, ''), COALESCE(je.reference, ''),
			   jel.debit_amount, jel.credit_amount,
			   COALESCE(cp_acc.code, '') as counterpart_code,
			   COALESCE(cp_acc.name, '') as counterpart_name,
			   COALESCE(c.company_name, c.contact_name, '') as contact_name
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		LEFT JOIN LATERAL (
			SELECT a2.code, a2.name
			FROM journal_entry_lines jel2
			JOIN accounts a2 ON jel2.account_id = a2.id
			WHERE jel2.journal_entry_id = jel.journal_entry_id
				AND jel2.account_id != jel.account_id
			LIMIT 1
		) cp_acc ON true
		LEFT JOIN contacts c ON je.contact_id = c.id
		WHERE jel.account_id = $1 AND je.tenant_id = $2
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date >= $3 AND je.entry_date <= $4
	`
	args := []interface{}{accID, tenantID, periodFrom, periodTo}
	argCount := 4

	if counterpartCode != "" {
		argCount++
		txQuery += fmt.Sprintf(" AND cp_acc.code = $%d", argCount)
		args = append(args, counterpartCode)
	}
	if contactName != "" {
		argCount++
		txQuery += fmt.Sprintf(" AND (c.company_name ILIKE $%d OR c.contact_name ILIKE $%d)", argCount, argCount)
		args = append(args, "%"+contactName+"%")
	}
	// TT §6.3: doc type and amount range filters
	if docType != "" {
		argCount++
		txQuery += fmt.Sprintf(" AND (je.doc_type = $%d OR je.source_type = $%d)", argCount, argCount)
		args = append(args, docType)
	}
	if amountMinStr != "" {
		if v, err := strconv.ParseFloat(amountMinStr, 64); err == nil {
			argCount++
			txQuery += fmt.Sprintf(" AND (jel.debit_amount + jel.credit_amount) >= $%d", argCount)
			args = append(args, v)
		}
	}
	if amountMaxStr != "" {
		if v, err := strconv.ParseFloat(amountMaxStr, 64); err == nil {
			argCount++
			txQuery += fmt.Sprintf(" AND (jel.debit_amount + jel.credit_amount) <= $%d", argCount)
			args = append(args, v)
		}
	}

	txQuery += " ORDER BY je.entry_date ASC, je.entry_number ASC"

	rows, err := h.db.Query(txQuery, args...)
	if err != nil {
		h.log.Error("Failed to get account card transactions", "error", err)
		response.InternalError(c, "Failed to generate account card")
		return
	}
	defer rows.Close()

	transactions := make([]entity.AccountCardTransaction, 0)
	runningBalance := calcOpeningBalance
	var totalDebit, totalCredit float64

	for rows.Next() {
		var tx entity.AccountCardTransaction
		var entryDate time.Time
		var entryID uuid.UUID

		err := rows.Scan(
			&entryID, &entryDate, &tx.EntryNumber, &tx.DocType,
			&tx.Description, &tx.Reference,
			&tx.DebitAmount, &tx.CreditAmount,
			&tx.CounterpartCode, &tx.CounterpartName,
			&tx.ContactName,
		)
		if err != nil {
			h.log.Error("Failed to scan account card row", "error", err)
			continue
		}

		tx.Date = entryDate.Format("2006-01-02")
		tx.EntryID = entryID.String()

		// Calculate running balance
		if normalBalance == "debit" {
			runningBalance += tx.DebitAmount - tx.CreditAmount
		} else {
			runningBalance += tx.CreditAmount - tx.DebitAmount
		}
		tx.RunningBalance = math.Round(runningBalance*100) / 100

		totalDebit += tx.DebitAmount
		totalCredit += tx.CreditAmount
		transactions = append(transactions, tx)
	}

	accountType := "active"
	if normalBalance == "credit" {
		accountType = "passive"
	}

	report := entity.AccountCardReport{
		AccountID:      accID,
		AccountCode:    accCode,
		AccountName:    accName,
		AccountType:    accountType,
		PeriodFrom:     periodFrom,
		PeriodTo:       periodTo,
		OpeningBalance: calcOpeningBalance,
		TotalDebit:     math.Round(totalDebit*100) / 100,
		TotalCredit:    math.Round(totalCredit*100) / 100,
		ClosingBalance: math.Round(runningBalance*100) / 100,
		Transactions:   transactions,
	}

	response.Success(c, report)
}
