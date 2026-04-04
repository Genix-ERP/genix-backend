package handler

import (
	"fmt"
	"math"
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
		SELECT a.id, a.code, a.name, COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''),
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
		GROUP BY a.id, a.code, a.name, a.name_uz, a.name_en, at.category, at.normal_balance, a.parent_id
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

		err := rows.Scan(&acc.AccountID, &acc.AccountCode, &acc.AccountName, &acc.AccountNameUz, &acc.AccountNameEn, &acc.Category, &normalBalance, &parentID, &debitSum, &creditSum)
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

	query := `
		SELECT a.id, a.code, a.name, COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''),
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
			AND at.category IN ('asset', 'liability', 'equity')
	`
	args := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND a.organization_id = $3"
		args = append(args, orgID)
	}
	query += `
		GROUP BY a.id, a.code, a.name, a.name_uz, a.name_en, at.category, at.normal_balance, a.opening_balance
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

	for rows.Next() {
		var accountID uuid.UUID
		var code, name, nameUz, nameEn, category, normalBalance string
		var openingBalance, debitSum, creditSum float64

		err := rows.Scan(&accountID, &code, &name, &nameUz, &nameEn, &category, &normalBalance, &openingBalance, &debitSum, &creditSum)
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
		SELECT a.id, a.code, a.name, COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''),
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
		GROUP BY a.id, a.code, a.name, a.name_uz, a.name_en, at.category, at.normal_balance, at.code
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
		var code, name, nameUz, nameEn, category, normalBalance, typeCode string
		var debitSum, creditSum float64

		err := rows.Scan(&accountID, &code, &name, &nameUz, &nameEn, &category, &normalBalance, &typeCode, &debitSum, &creditSum)
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
			// Investing: 0100-0899 (fixed/intangible assets, investments, capital expenditures)
			// Financing: 6000-6999 (long-term liabilities), 3000-3999 (equity), 7000 (interest)
			// Operating: everything else (revenue, COGS, OpEx, AR, AP, etc.)
			codePrefix := ""
			if len(code) >= 2 {
				codePrefix = code[:2]
			}
			switch {
			case code >= "0100" && code <= "0899":
				// Fixed assets (01xx-04xx), intangible assets (04xx),
				// long-term investments (06xx), equipment (08xx) → investing
				investingItems = append(investingItems, item)
				investingTotal += amount
			case code >= "1500" && code <= "1699":
				// Capital equipment, depreciation, intangible assets → investing
				investingItems = append(investingItems, item)
				investingTotal += amount
			case code >= "2100" && code <= "2599":
				// Short/long-term loans → financing
				financingItems = append(financingItems, item)
				financingTotal += amount
			case code >= "6000" && code < "7000":
				// Long-term liabilities (6000-6999) → financing
				financingItems = append(financingItems, item)
				financingTotal += amount
			case codePrefix >= "30" && codePrefix <= "39":
				// Equity accounts (3000-3999) → financing
				financingItems = append(financingItems, item)
				financingTotal += amount
			case code == "7000":
				// Interest expense → financing
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

	// Remove contacts with zero or negative net balance (fully paid)
	for id, contact := range contactMap {
		if contact.TotalAmount <= 0 {
			// Subtract their bucket amounts from totals
			currentTotal -= contact.Current
			days1To30 -= contact.Days1To30
			days31To60 -= contact.Days31To60
			days61To90 -= contact.Days61To90
			over90Days -= contact.Over90Days
			totalAmount -= contact.TotalAmount
			delete(contactMap, id)
		}
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

	// Remove contacts with zero or negative net balance (fully paid)
	for id, contact := range contactMap {
		if contact.TotalAmount <= 0 {
			currentTotal -= contact.Current
			days1To30 -= contact.Days1To30
			days31To60 -= contact.Days31To60
			days61To90 -= contact.Days61To90
			over90Days -= contact.Over90Days
			totalAmount -= contact.TotalAmount
			delete(contactMap, id)
		}
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

// GetSalesSummary returns sales summary report
func (h *Handler) GetSalesSummary(c *gin.Context) {
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

	// Get sales totals
	var totalOrders, totalInvoiced int
	var orderAmount, invoicedAmount, paidAmount float64

	soQuery := `
		SELECT COUNT(*), COALESCE(SUM(total_amount), 0)
		FROM sales_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL
			AND order_date >= $2 AND order_date <= $3
	`
	siQuery := `
		SELECT COUNT(*), COALESCE(SUM(total_amount), 0), COALESCE(SUM(amount_paid), 0)
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL
			AND invoice_date >= $2 AND invoice_date <= $3
	`
	tcQuery := `
		SELECT c.id, c.name, COUNT(si.id), COALESCE(SUM(si.total_amount), 0)
		FROM sales_invoices si
		JOIN contacts c ON si.customer_id = c.id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL
			AND si.invoice_date >= $2 AND si.invoice_date <= $3
	`
	salesArgs := []interface{}{tenantID, periodFrom, periodTo}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		soQuery += " AND organization_id = $4"
		siQuery += " AND organization_id = $4"
		tcQuery += " AND si.organization_id = $4"
		salesArgs = append(salesArgs, orgID)
	}

	h.db.QueryRow(soQuery, salesArgs...).Scan(&totalOrders, &orderAmount)
	h.db.QueryRow(siQuery, salesArgs...).Scan(&totalInvoiced, &invoicedAmount, &paidAmount)

	// Get top customers
	type TopCustomer struct {
		CustomerID   uuid.UUID `json:"customer_id"`
		CustomerName string    `json:"customer_name"`
		OrderCount   int       `json:"order_count"`
		TotalAmount  float64   `json:"total_amount"`
	}

	tcQuery += `
		GROUP BY c.id, c.name
		ORDER BY SUM(si.total_amount) DESC
		LIMIT 10
	`
	topCustomers := make([]TopCustomer, 0)
	rows, err := h.db.Query(tcQuery, salesArgs...)

	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tc TopCustomer
			rows.Scan(&tc.CustomerID, &tc.CustomerName, &tc.OrderCount, &tc.TotalAmount)
			topCustomers = append(topCustomers, tc)
		}
	}

	response.Success(c, gin.H{
		"period_from":      periodFrom,
		"period_to":        periodTo,
		"total_orders":     totalOrders,
		"order_amount":     orderAmount,
		"total_invoiced":   totalInvoiced,
		"invoiced_amount":  invoicedAmount,
		"paid_amount":      paidAmount,
		"outstanding":      invoicedAmount - paidAmount,
		"top_customers":    topCustomers,
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
