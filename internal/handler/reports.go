package handler

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
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

	// Aggregate lines in a subquery joined to their entries so the
	// posted/date/deleted filters actually exclude the lines: filtering in a
	// second LEFT JOIN condition only nulls the entry columns while the line
	// amounts still aggregate, leaking draft and deleted entries into the report.
	query := `
		SELECT a.id, a.code, a.name, COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''), COALESCE(a.name_ru, ''),
			   at.category, at.normal_balance, a.parent_id,
			   COALESCE(jel.total_debit, 0) as total_debit,
			   COALESCE(jel.total_credit, 0) as total_credit
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN (
			SELECT l.account_id,
				   SUM(l.debit_amount) AS total_debit,
				   SUM(l.credit_amount) AS total_credit
			FROM journal_entry_lines l
			JOIN journal_entries je ON l.journal_entry_id = je.id
			WHERE je.tenant_id = $1 AND je.status = 'posted' AND je.entry_date <= $2 AND je.deleted_at IS NULL
			GROUP BY l.account_id
		) jel ON a.id = jel.account_id
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
	`
	args := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND a.organization_id = $3"
		args = append(args, orgID)
	}
	query += `
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

	// Build parent-child hierarchy (e.g., 1300 = sum of 1310+1320+1330+1340).
	// Rollups must run bottom-up: with multi-level charts (leaf 6420 -> group
	// 6400 -> section 6000) a parent has to see its child groups' rolled-up
	// balances, not their raw posted ones, or nested balances drop out of the
	// report. Each subtree is computed once, children before their parent.
	accountByID := make(map[uuid.UUID]*entity.TrialBalanceAccount)
	for i := range allAccounts {
		accountByID[allAccounts[i].AccountID] = &allAccounts[i]
	}
	// allAccounts is ordered by code, so children keep code order; an account
	// whose parent is missing from the result set stays at top level.
	childIDsByParent := make(map[uuid.UUID][]uuid.UUID)
	hasParent := make(map[uuid.UUID]bool)
	for i := range allAccounts {
		if pid := allAccounts[i].ParentID; pid != nil {
			if _, ok := accountByID[*pid]; ok {
				childIDsByParent[*pid] = append(childIDsByParent[*pid], allAccounts[i].AccountID)
				hasParent[allAccounts[i].AccountID] = true
			}
		}
	}

	memo := make(map[uuid.UUID]*entity.TrialBalanceAccount)
	visiting := make(map[uuid.UUID]bool)
	var rollup func(id uuid.UUID) *entity.TrialBalanceAccount
	rollup = func(id uuid.UUID) *entity.TrialBalanceAccount {
		if node, ok := memo[id]; ok {
			return node
		}
		node := *accountByID[id]
		// A parent_id cycle (malformed data) would recurse forever; treat the
		// repeated account as a leaf instead
		if !visiting[id] && len(childIDsByParent[id]) > 0 {
			visiting[id] = true
			childIDs := childIDsByParent[id]
			node.IsParent = true
			node.Children = make([]entity.TrialBalanceAccount, 0, len(childIDs))
			var childDebit, childCredit float64
			for _, cid := range childIDs {
				child := rollup(cid)
				node.Children = append(node.Children, *child)
				childDebit += child.DebitBalance
				childCredit += child.CreditBalance
			}
			// A parent's own posted balance is only overridden when its
			// children carry a balance
			if childDebit > 0 || childCredit > 0 {
				node.DebitBalance = childDebit
				node.CreditBalance = childCredit
			}
			delete(visiting, id)
		}
		memo[id] = &node
		return &node
	}

	// Final list: top-level accounts with children nested, totals from top level only
	accounts := make([]entity.TrialBalanceAccount, 0)
	for i := range allAccounts {
		if hasParent[allAccounts[i].AccountID] {
			continue // Skip children at top level (they're nested)
		}
		acc := *rollup(allAccounts[i].AccountID)
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
			   -- Only sum lines whose journal entry actually qualifies (posted, not
			   -- deleted, on/before as-of date). The filter lives on the je join, so
			   -- an unconditional SUM(jel.*) would also count draft/deleted/future
			   -- lines because jel is joined straight to the account.
			   COALESCE(SUM(CASE WHEN je.id IS NOT NULL THEN jel.debit_amount ELSE 0 END), 0) as total_debit,
			   COALESCE(SUM(CASE WHEN je.id IS NOT NULL THEN jel.credit_amount ELSE 0 END), 0) as total_credit
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON a.id = jel.account_id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.entry_date <= $2 AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
			AND at.category IN ('asset', 'contra_asset', 'liability', 'equity', 'revenue', 'expense')
	`
	args := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND a.organization_id = $3"
		args = append(args, orgID)
	}
	query += `
		GROUP BY a.id, a.code, a.name, a.name_uz, a.name_en, a.name_ru, at.category, at.normal_balance, a.opening_balance
		ORDER BY a.code
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

		// Each section is totalled on its natural side so contra accounts net
		// correctly: assets are debit-positive (a credit-normal contra-asset like
		// accumulated depreciation reduces total assets instead of inflating it);
		// liabilities and equity are credit-positive (a debit-normal contra-equity
		// reduces equity). contra_asset (eskirish 0220/0230/0250/0260) belongs
		// INSIDE the assets section as a negative line (BHMS presentation) —
		// dropping the category from the query broke A = L + E by exactly the
		// accumulated-depreciation balance.
		switch category {
		case "asset", "contra_asset":
			sectionVal := openingBalance + debitSum - creditSum
			acc.Balance = math.Round(sectionVal*100) / 100
			assetAccounts = append(assetAccounts, acc)
			totalAssets += sectionVal
		case "liability":
			sectionVal := openingBalance + creditSum - debitSum
			acc.Balance = math.Round(sectionVal*100) / 100
			liabilityAccounts = append(liabilityAccounts, acc)
			totalLiabilities += sectionVal
		case "equity":
			sectionVal := openingBalance + creditSum - debitSum
			acc.Balance = math.Round(sectionVal*100) / 100
			equityAccounts = append(equityAccounts, acc)
			totalEquity += sectionVal
		}
	}

	// Net income = Revenue - Expenses (this is the current year's undistributed profit)
	netIncome := math.Round((totalRevenue-totalExpenses)*100) / 100

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

	args := []interface{}{tenantID, periodFrom, periodTo}
	jeOrgFilter := ""
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		jeOrgFilter = " AND je.organization_id = $4"
		args = append(args, orgID)
	}

	query := `
		SELECT a.id, a.code, a.name, COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''), COALESCE(a.name_ru, ''),
			   at.category, at.normal_balance, at.code as type_code,
			   COALESCE(SUM(jel.debit_amount), 0) as total_debit,
			   COALESCE(SUM(jel.credit_amount), 0) as total_credit
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN (
			journal_entry_lines jel
			INNER JOIN journal_entries je ON jel.journal_entry_id = je.id
				AND je.status = 'posted'
				AND je.entry_date >= $2 AND je.entry_date <= $3
				AND je.deleted_at IS NULL` + jeOrgFilter + `
		) ON a.id = jel.account_id
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
			AND at.category IN ('revenue', 'expense')
	`
	if jeOrgFilter != "" {
		query += " AND a.organization_id = $4"
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

	// Income tax at the tenant's configured profit-tax rate (falls back to the
	// 15% Uzbekistan standard) — only if profit is positive. Using the configured
	// rate keeps the income statement consistent with the Taxes module.
	var incomeTax float64
	if preTaxProfit > 0 {
		profitTaxPct := h.getCompanyTaxRatePct(tenantID, "profit", 15.0)
		incomeTax = preTaxProfit * profitTaxPct / 100.0
	}
	netIncome := preTaxProfit - incomeTax
	totalExpenses := totalCOGS + totalOpex + totalOtherExpenses
	// total_revenue includes other income so the published fields stay an
	// identity: net = total_revenue − total_expenses − income_tax. Before, a
	// disposal gain (9310) broke that contract — it was in net but not in
	// total_revenue (surfaced by the Aktivlar disposal tests, 2026-08-03).
	totalIncome := totalRevenue + totalOtherIncome

	report := entity.IncomeStatementReport{
		PeriodFrom:        periodFrom,
		PeriodTo:          periodTo,
		TotalRevenue:      math.Round(totalIncome*100) / 100,
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
	accountType := strings.TrimSpace(c.Query("account_type"))
	search := strings.TrimSpace(c.Query("search"))

	now := time.Now()
	if periodFrom == "" {
		periodFrom = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	}
	if periodTo == "" {
		periodTo = now.Format("2006-01-02")
	}

	// Pagination is OPT-IN: it applies only when the caller actually asks for a
	// page. Defaulting to a page size would silently truncate the existing web
	// Bosh daftar screen, which fetches the whole ledger and has no paging UI.
	// Mobile sends page + page_size=10 and therefore gets the paged shape with a
	// `meta` envelope; everyone else keeps the full list and no `meta` (exactly
	// the "meta absent → server sent everything" contract the client expects).
	pageSizeRaw := c.Query("page_size")
	if pageSizeRaw == "" {
		pageSizeRaw = c.Query("limit")
	}
	paginate := c.Query("page") != "" || pageSizeRaw != ""
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(pageSizeRaw)
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// One WHERE clause shared by the count, the page query and the period-wide
	// totals, so all three always agree on "which accounts match".
	filter := " WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true"
	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		filter += fmt.Sprintf(" AND a.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if accountID != "" {
		argCount++
		filter += fmt.Sprintf(" AND a.id = $%d", argCount)
		args = append(args, accountID)
	}

	// account_type filters on the account_types category ("asset", "liability",
	// …). It used to be accepted and silently dropped, so the UI's type dropdown
	// changed nothing.
	if accountType != "" && strings.ToLower(accountType) != "all" {
		argCount++
		filter += fmt.Sprintf(" AND at.category = $%d", argCount)
		args = append(args, accountType)
	}

	// search matches code or any of the trilingual names. Server-side so it
	// searches the whole ledger, not just the page the client happens to hold.
	if search != "" {
		argCount++
		filter += fmt.Sprintf(` AND (a.code ILIKE '%%' || $%d || '%%'
			OR a.name ILIKE '%%' || $%d || '%%'
			OR COALESCE(a.name_uz,'') ILIKE '%%' || $%d || '%%'
			OR COALESCE(a.name_en,'') ILIKE '%%' || $%d || '%%'
			OR COALESCE(a.name_ru,'') ILIKE '%%' || $%d || '%%')`,
			argCount, argCount, argCount, argCount, argCount)
		args = append(args, search)
	}

	const fromClause = ` FROM accounts a JOIN account_types at ON a.account_type_id = at.id`

	// Total matching accounts — drives meta.total / total_pages.
	total := 0
	if paginate {
		if err := h.db.QueryRow(`SELECT COUNT(*)`+fromClause+filter, args...).Scan(&total); err != nil {
			h.log.Error("Failed to count general ledger accounts", "error", err)
			response.InternalError(c, "Failed to generate general ledger")
			return
		}
	}

	// Get accounts to include. We pull `category` and `normal_balance` from
	// account_types so the UI can render the debit/credit closing columns
	// without a second round-trip, and so we can split ClosingBalance by side.
	// Trilingual name columns (migration 316) are returned too so the frontend
	// can pick the right label per-language — same pattern as ListAccounts.
	accountQuery := `
		SELECT a.id, a.code, a.name,
		       COALESCE(a.name_uz, ''), COALESCE(a.name_en, ''), COALESCE(a.name_ru, ''),
		       a.opening_balance,
		       COALESCE(at.category, ''), COALESCE(at.normal_balance, 'debit')` +
		fromClause + filter + " ORDER BY a.code"

	pageArgs := args
	if paginate {
		accountQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
		pageArgs = append(append([]interface{}{}, args...), pageSize, (page-1)*pageSize)
	}

	accountRows, err := h.db.Query(accountQuery, pageArgs...)
	if err != nil {
		h.log.Error("Failed to get accounts", "error", err)
		response.InternalError(c, "Failed to generate general ledger")
		return
	}
	defer accountRows.Close()

	accounts := make([]entity.GeneralLedgerAccount, 0)
	accountIDs := make([]string, 0)

	for accountRows.Next() {
		var acc entity.GeneralLedgerAccount
		var category, normalBalance string

		err := accountRows.Scan(
			&acc.AccountID, &acc.AccountCode, &acc.AccountName,
			&acc.AccountNameUz, &acc.AccountNameEn, &acc.AccountNameRu,
			&acc.OpeningBalance, &category, &normalBalance,
		)
		if err != nil {
			continue
		}
		acc.AccountType = category
		acc.NormalBalance = normalBalance
		acc.Transactions = make([]entity.GeneralLedgerTransaction, 0)

		accounts = append(accounts, acc)
		accountIDs = append(accountIDs, acc.AccountID.String())
	}

	// Fetch every transaction for THIS PAGE of accounts in a single query and
	// group in Go. Previously this ran one query per account — 163 round-trips
	// for a 162-account tenant.
	txByAccount := make(map[string][]entity.GeneralLedgerTransaction, len(accountIDs))
	if len(accountIDs) > 0 {
		txRows, txErr := h.db.Query(`
			SELECT jel.account_id, je.entry_date, je.entry_number, je.description, je.reference,
			       jel.debit_amount, jel.credit_amount
			FROM journal_entry_lines jel
			JOIN journal_entries je ON jel.journal_entry_id = je.id
			WHERE jel.account_id = ANY($1::uuid[]) AND je.tenant_id = $2
			  AND je.status = 'posted' AND je.deleted_at IS NULL
			  AND je.entry_date >= $3 AND je.entry_date <= $4
			ORDER BY jel.account_id, je.entry_date, je.entry_number
		`, pq.Array(accountIDs), tenantID, periodFrom, periodTo)
		if txErr != nil {
			h.log.Error("Failed to get general ledger transactions", "error", txErr)
			response.InternalError(c, "Failed to generate general ledger")
			return
		}
		for txRows.Next() {
			var accID uuid.UUID
			var tx entity.GeneralLedgerTransaction
			var entryDate time.Time
			var desc, ref *string

			if err := txRows.Scan(&accID, &entryDate, &tx.EntryNumber, &desc, &ref,
				&tx.DebitAmount, &tx.CreditAmount); err != nil {
				continue
			}
			tx.Date = entryDate.Format("2006-01-02")
			if desc != nil {
				tx.Description = *desc
			}
			if ref != nil {
				tx.Reference = *ref
			}
			key := accID.String()
			txByAccount[key] = append(txByAccount[key], tx)
		}
		txRows.Close()
	}

	// Walk each account's transactions in the query's per-account order and
	// build the running balance exactly as the per-account loop used to.
	for i := range accounts {
		acc := &accounts[i]
		normalBalance := acc.NormalBalance
		runningBalance := acc.OpeningBalance

		for _, tx := range txByAccount[acc.AccountID.String()] {
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

		acc.ClosingBalance = runningBalance
		acc.TotalDebit = math.Round(acc.TotalDebit*100) / 100
		acc.TotalCredit = math.Round(acc.TotalCredit*100) / 100
		acc.ClosingBalance = math.Round(acc.ClosingBalance*100) / 100

		// Split the closing balance by side. `runningBalance` is the signed
		// balance in the account's "natural" direction (positive = sits on the
		// normal_balance side). Negative balances flip to the opposite column.
		switch normalBalance {
		case "credit":
			if acc.ClosingBalance >= 0 {
				acc.ClosingCredit = acc.ClosingBalance
				acc.ClosingDebit = 0
			} else {
				acc.ClosingCredit = 0
				acc.ClosingDebit = math.Round(-acc.ClosingBalance*100) / 100
			}
		default: // "debit"
			if acc.ClosingBalance >= 0 {
				acc.ClosingDebit = acc.ClosingBalance
				acc.ClosingCredit = 0
			} else {
				acc.ClosingDebit = 0
				acc.ClosingCredit = math.Round(-acc.ClosingBalance*100) / 100
			}
		}

	}

	// Report-level totals: frontend uses the debit/credit-closing difference as
	// a balance-integrity check (shows red ≠ indicator when mismatched). These
	// are summed over the accounts actually returned, since they're derived from
	// each account's running balance.
	var totalOpening, closingDebit, closingCredit float64
	for _, a := range accounts {
		totalOpening += a.OpeningBalance
		closingDebit += a.ClosingDebit
		closingCredit += a.ClosingCredit
	}

	// Period-wide debit/credit totals over EVERY account matching the filters,
	// ignoring LIMIT/OFFSET. The summary strip must not shrink to the loaded
	// page — so this is one aggregate over the same WHERE clause rather than a
	// sum of what we happened to return.
	var periodDebit, periodCredit float64
	totalsArgs := append(append([]interface{}{}, args...), tenantID, periodFrom, periodTo)
	totalsQuery := `
		SELECT COALESCE(SUM(jel.debit_amount), 0), COALESCE(SUM(jel.credit_amount), 0)
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		JOIN accounts a ON a.id = jel.account_id
		JOIN account_types at ON a.account_type_id = at.id` + filter +
		fmt.Sprintf(` AND je.tenant_id = $%d AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.entry_date >= $%d AND je.entry_date <= $%d`, argCount+1, argCount+2, argCount+3)
	if err := h.db.QueryRow(totalsQuery, totalsArgs...).Scan(&periodDebit, &periodCredit); err != nil {
		h.log.Error("Failed to compute general ledger period totals", "error", err)
		// Non-fatal: fall back to the page's own sums rather than failing the report.
		periodDebit, periodCredit = 0, 0
		for _, a := range accounts {
			periodDebit += a.TotalDebit
			periodCredit += a.TotalCredit
		}
	}
	periodDebit = math.Round(periodDebit*100) / 100
	periodCredit = math.Round(periodCredit*100) / 100

	report := entity.GeneralLedgerReport{
		PeriodFrom:         periodFrom,
		PeriodTo:           periodTo,
		Accounts:           accounts,
		TotalOpening:       math.Round(totalOpening*100) / 100,
		TotalDebit:         periodDebit,
		TotalCredit:        periodCredit,
		ClosingDebitTotal:  math.Round(closingDebit*100) / 100,
		ClosingCreditTotal: math.Round(closingCredit*100) / 100,
		Totals: entity.GeneralLedgerTotals{
			TotalDebit:  periodDebit,
			TotalCredit: periodCredit,
		},
	}

	if paginate {
		response.Paginated(c, report, page, pageSize, total)
		return
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

	// Cash position via the shared definition in cash_balance.go — the same
	// helper /reports/finance-dashboard calls, so the two endpoints can no
	// longer disagree. Replaces an inline query that omitted is_active and
	// is_leaf (so deactivated accounts counted and every parent rollup
	// double-counted its children) and signed its pre-period movement by
	// at.normal_balance while its in-period movement was debit-positive, giving
	// the two halves of one balance opposite signs on a credit-normal bank
	// account.
	cashOrg := uuid.Nil
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		cashOrg = orgID
	}

	// Opening = the position at the last instant before the period starts.
	// Derived by asking for the balance as of the day before period_from rather
	// than by subtracting in-period movement from the closing figure, so the
	// two ends are computed the same way.
	openingAsOf := periodFrom
	if d, perr := time.Parse("2006-01-02", periodFrom); perr == nil {
		openingAsOf = d.AddDate(0, 0, -1).Format("2006-01-02")
	}

	_, openingCash, err := h.cashBalancesAsOf(tenantID, cashOrg, openingAsOf)
	if err != nil {
		h.log.Error("Failed to get opening cash balance", "error", err)
	}

	periodDebits, periodCredits, err := h.cashMovementBetween(tenantID, cashOrg, periodFrom, periodTo)
	if err != nil {
		h.log.Error("Failed to get cash movement", "error", err)
	}

	// Flows are derived ONLY from entries that touch a cash account (EXISTS on a
	// canonical cash line), taking the NON-cash counter-lines of those entries.
	// Sweeping every posted entry instead reported pure accruals — payroll
	// accrual, depreciation Dr 9420 / Cr 0220 — as cash flows; the sections only
	// summed to the net change via the double-entry identity. Restricted to
	// cash-touching entries, sum(credit-debit) over the counter-lines equals the
	// entries' cash delta exactly, so the sections now reconcile line by line.
	cfQuery := `
		SELECT
			a.code,
			COALESCE(NULLIF(a.name_uz, ''), a.name) as display_name,
			COALESCE(SUM(jel.debit_amount), 0) as total_debit,
			COALESCE(SUM(jel.credit_amount), 0) as total_credit
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		JOIN journal_entry_lines jel ON a.id = jel.account_id
		JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.entry_date BETWEEN $2 AND $3 AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
			AND NOT ` + cashAccountPredicate("a", "at") + `
			AND EXISTS (
				SELECT 1 FROM journal_entry_lines cl
				JOIN accounts ca ON ca.id = cl.account_id
				JOIN account_types cat ON cat.id = ca.account_type_id
				WHERE cl.journal_entry_id = je.id
					AND ca.deleted_at IS NULL
					AND ` + cashAccountPredicate("ca", "cat") + `
			)
	`
	cfArgs := []interface{}{tenantID, periodFrom, periodTo}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		cfQuery += fmt.Sprintf(" AND a.organization_id = $%d", len(cfArgs)+1)
		cfArgs = append(cfArgs, orgID)
	}
	cfQuery += " GROUP BY a.code, a.name, a.name_uz ORDER BY a.code"

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
			var code, name string
			var debit, credit float64
			if err := rows.Scan(&code, &name, &debit, &credit); err != nil {
				continue
			}

			// Cash impact of a counter-account is always (credit - debit),
			// regardless of the account's own normal balance: a credit on the
			// other side means cash came in (Dr cash / Cr revenue), a debit
			// means cash went out (Dr expense / Cr cash).
			amount := credit - debit

			if math.Abs(amount) < 0.01 {
				continue
			}

			item := entity.CashFlowItem{
				Description: name,
				Amount:      math.Round(amount*100) / 100,
			}

			// Categorize by BHMS account code:
			// Investing: 0xxx (fixed/intangible assets, capital investments 0810,
			//   long-term investments 06xx) and 55xx (short-term fin. investments).
			// Financing: real debt and equity only — 62xx/68xx short-term loans,
			//   78xx/79xx long-term loans, 8xxx equity. The rest of 6xxx (AP 6010/
			//   6015, taxes 64xx, wages 6710, other 69xx) is working capital →
			//   operating, NOT financing.
			// Operating: everything else (AR/AP, inventory, wages, taxes, 9xxx).
			switch {
			case strings.HasPrefix(code, "0"), strings.HasPrefix(code, "55"):
				investingItems = append(investingItems, item)
				investingTotal += amount
			case strings.HasPrefix(code, "62"), strings.HasPrefix(code, "68"),
				strings.HasPrefix(code, "78"), strings.HasPrefix(code, "79"),
				strings.HasPrefix(code, "8"):
				financingItems = append(financingItems, item)
				financingTotal += amount
			default:
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
			   COALESCE(c.id, si.customer_id) as contact_id,
			   COALESCE(c.name, si.customer_name, '—') as contact_name,
			   ($2::date - si.due_date)::int as days_overdue
		FROM sales_invoices si
		LEFT JOIN contacts c ON si.customer_id = c.id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL
			AND si.customer_id IS NOT NULL
			-- 'draft' excluded, matching the rest of the finance layer
			-- (finance_dashboard.go, finance_kpis.go, payments_reconcile.go,
			-- tax_reports.go, nds_tax.go). Aging was the only query that
			-- counted an unconfirmed draft as a receivable, so this screen
			-- and the dashboard card reported different debt for one tenant.
			-- Both status columns DEFAULT to 'draft' (migrations 002, 004),
			-- so the exposure is every invoice nobody has advanced yet.
			AND si.status NOT IN ('draft', 'cancelled', 'void')
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
		       COALESCE(c.id, p.contact_id) as contact_id,
		       COALESCE(c.name, '—') as contact_name,
		       ($2::date - p.payment_date)::int as days_since
		FROM payments p
		LEFT JOIN contacts c ON p.contact_id = c.id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND p.contact_id IS NOT NULL
		  -- 'posted' included: payments_reconcile.go treats both as real
		  -- money, so a posted receipt showed as credit on Solishtirish and
		  -- was invisible here — the same payment, two answers.
		  AND p.type = 'receipt' AND p.status IN ('confirmed', 'posted')
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
		  AND COALESCE(sr.approved_at::date, sr.return_date::date) <= $2::date
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

	// Search/sort/page the finished list. The totals above are computed over the
	// whole ledger and are NOT recomputed from the page — that is the whole
	// point of returning them.
	// Search narrows the totals with the rows; paging does not. `agingTot` is
	// the filtered set's figures — identical to the accumulators above when no
	// search is given.
	pagedContacts, agingTot, agingMeta := agingFilterSortPage(c, contacts)

	report := entity.AgingReport{
		AsOfDate:     asOfDate,
		ReportType:   "receivables",
		TotalAmount:  math.Round(agingTot.TotalAmount*100) / 100,
		CurrentTotal: math.Round(agingTot.Current*100) / 100,
		Days1To30:    math.Round(agingTot.Days1To30*100) / 100,
		Days31To60:   math.Round(agingTot.Days31To60*100) / 100,
		Days61To90:   math.Round(agingTot.Days61To90*100) / 100,
		Over90Days:   math.Round(agingTot.Over90Days*100) / 100,
		Percentages: agingPercentages(agingTot.Current, agingTot.Days1To30,
			agingTot.Days31To60, agingTot.Days61To90, agingTot.Over90Days),
		Contacts: pagedContacts,
		Meta:     agingMeta,
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
			   COALESCE(c.id, pi.vendor_id) as contact_id,
			   COALESCE(c.name, '—') as contact_name,
			   ($2::date - pi.due_date)::int as days_overdue
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.tenant_id = $1 AND pi.deleted_at IS NULL
			AND pi.vendor_id IS NOT NULL
			-- Same as the AR side above: a draft bill is not yet a payable.
			AND pi.status NOT IN ('draft', 'cancelled', 'void')
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
		       COALESCE(c.id, p.contact_id) as contact_id,
		       COALESCE(c.name, '—') as contact_name,
		       ($2::date - p.payment_date)::int as days_since
		FROM payments p
		LEFT JOIN contacts c ON p.contact_id = c.id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND p.contact_id IS NOT NULL
		  -- Same vocabulary as the AR side and payments_reconcile.go.
		  AND p.type = 'payment' AND p.status IN ('confirmed', 'posted')
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

	// ── Step 3: Subtract CREDITED purchase returns ──
	//
	// The AR twin exists; the AP side never had one, so a vendor invoice that
	// was returned and credited stayed fully payable in aging on all three apps.
	//
	// This is deliberately NOT a copy of the AR step — purchase_returns has a
	// different shape and a different vocabulary, and reusing AR's would have
	// been a 500 on a missing column plus a silently wrong status filter:
	//   * amount is total_value, not total_amount (which does not exist here),
	//     preferring credit_amount when the supplier's credit note names a
	//     different figure than the goods were valued at;
	//   * the party is supplier_id, not customer_id;
	//   * status 'completed' does not exist in this vocabulary at all.
	//
	// Recognised at 'credited' only. Until the supplier issues the credit note
	// the goods may be back but the liability is not reduced, and 'credited' is
	// exactly the state that says it was — recognising at 'approved' would
	// understate payables for every return the supplier later rejects.
	prQuery := `
		SELECT pr.id, pr.return_number,
		       COALESCE(pr.credit_note_date, pr.approved_at::date, pr.return_date),
		       COALESCE(NULLIF(pr.credit_amount, 0), pr.total_value),
		       pr.supplier_id, COALESCE(c.name, '') as contact_name
		FROM purchase_returns pr
		LEFT JOIN contacts c ON pr.supplier_id = c.id
		WHERE pr.tenant_id = $1 AND pr.deleted_at IS NULL
		  AND pr.status = 'credited'
		  AND pr.supplier_id IS NOT NULL
		  AND COALESCE(pr.credit_note_date, pr.approved_at::date, pr.return_date) <= $2::date
	`
	prArgs := []interface{}{tenantID, asOfDate}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		prQuery += " AND pr.organization_id = $3"
		prArgs = append(prArgs, orgID)
	}
	prRows, prErr := h.db.Query(prQuery, prArgs...)
	if prErr != nil {
		h.log.Error("aging payables: purchase returns step failed", "error", prErr)
	} else {
		defer prRows.Close()
		for prRows.Next() {
			var retID, contactID uuid.UUID
			var retNumber, contactName string
			var retDate time.Time
			var amount float64
			if err := prRows.Scan(&retID, &retNumber, &retDate, &amount, &contactID, &contactName); err != nil {
				h.log.Error("aging payables: scan purchase return", "error", err)
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
			contact.Invoices = append(contact.Invoices, entity.AgingInvoice{
				InvoiceID:     retID,
				InvoiceNumber: "CN-" + retNumber,
				InvoiceDate:   retDate.Format("2006-01-02"),
				DueDate:       retDate.Format("2006-01-02"),
				TotalAmount:   negativeAmount,
				AmountDue:     negativeAmount,
				DaysOverdue:   0,
				AgingBucket:   "current",
			})
			contact.TotalAmount += negativeAmount
			totalAmount += negativeAmount
		}
	}

	// ── Step 4: FIFO-apply vendor credits against oldest vendor invoices ──
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

	// Search/sort/page the finished list. The totals above are computed over the
	// whole ledger and are NOT recomputed from the page — that is the whole
	// point of returning them.
	// Search narrows the totals with the rows; paging does not. `agingTot` is
	// the filtered set's figures — identical to the accumulators above when no
	// search is given.
	pagedContacts, agingTot, agingMeta := agingFilterSortPage(c, contacts)

	report := entity.AgingReport{
		AsOfDate:     asOfDate,
		ReportType:   "payables",
		TotalAmount:  math.Round(agingTot.TotalAmount*100) / 100,
		CurrentTotal: math.Round(agingTot.Current*100) / 100,
		Days1To30:    math.Round(agingTot.Days1To30*100) / 100,
		Days31To60:   math.Round(agingTot.Days31To60*100) / 100,
		Days61To90:   math.Round(agingTot.Days61To90*100) / 100,
		Over90Days:   math.Round(agingTot.Over90Days*100) / 100,
		Percentages: agingPercentages(agingTot.Current, agingTot.Days1To30,
			agingTot.Days31To60, agingTot.Days61To90, agingTot.Over90Days),
		Contacts: pagedContacts,
		Meta:     agingMeta,
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

// =====================================================
// DIRECTOR DASHBOARD — single-shot cross-company summary
// Returns aggregated metrics for every organization in the tenant in ONE call,
// so the frontend doesn't have to fan out N×M requests.
// =====================================================

type directorCompanySummary struct {
	ID                   string                       `json:"id"`
	Name                 string                       `json:"name"`
	Revenue              float64                      `json:"revenue"`
	Expenses             float64                      `json:"expenses"`
	Profit               float64                      `json:"profit"`
	Debtors              float64                      `json:"debtors"`
	Creditors            float64                      `json:"creditors"`
	MonthlyRevenue       []float64                    `json:"monthly_revenue"`
	ExpenseByCategory    map[string]float64           `json:"expense_by_category"`
	StockUnits           float64                      `json:"stock_units"`
	StockValue           float64                      `json:"stock_value"`
	LowStockCount        int                          `json:"low_stock_count"`
	TotalEmployees       int                          `json:"total_employees"`
	ActiveEmployees      int                          `json:"active_employees"`
	SalaryFund           float64                      `json:"salary_fund"`
	PayrollFund          float64                      `json:"payroll_fund"`
	PayrollUnpaid        float64                      `json:"payroll_unpaid"`
	TopStockProducts     []directorTopStockProduct    `json:"top_stock_products"`
	ConstructionProjects []directorConstructionProjct `json:"construction_projects"`
	// Shartnomalar aggregates (active = amaldagi, expiring = ≤30 days)
	ActiveContracts      int     `json:"active_contracts"`
	ActiveContractsValue float64 `json:"active_contracts_value"`
	ExpiringContracts    int     `json:"expiring_contracts"`
	ContractsOutstanding float64 `json:"contracts_outstanding"`
	// Xarid aggregates (davr xaridi, ta'minotchi AP, kechikkan yetkazmalar)
	PurchasesPeriodTotal      float64 `json:"purchases_period_total"`
	PurchasesPeriodCount      int     `json:"purchases_period_count"`
	PurchaseAPTotal           float64 `json:"purchase_ap_total"`
	PurchaseOverdueDeliveries int     `json:"purchase_overdue_deliveries"`
	// CRM aggregates (open pipeline, bu oy yutilgan, konversiya %)
	CRMOpenLeads     int     `json:"crm_open_leads"`
	CRMPipelineValue float64 `json:"crm_pipeline_value"`
	CRMWonMonth      int     `json:"crm_won_month"`
	CRMWonValueMonth float64 `json:"crm_won_value_month"`
	CRMConversion    float64 `json:"crm_conversion"`
	CRMTopLossReason string  `json:"crm_top_loss_reason"`
}

type directorTopStockProduct struct {
	Name  string  `json:"name"`
	Qty   float64 `json:"qty"`
	Value float64 `json:"value"`
}

type directorConstructionProjct struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Code           string  `json:"code"`
	Status         string  `json:"status"`
	Progress       float64 `json:"progress"`
	ContractAmount float64 `json:"contract_amount"`
}

// GetDirectorSummary returns per-organization financial/stock/HR metrics in a single response.
func (h *Handler) GetDirectorSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Period-aware time buckets. The dashboard's Day/Month/Quarter/Year
	// pill filters the `period` query param; each value picks a number
	// of buckets and the unit spanned by each bucket. Oldest bucket is
	// index 0, newest (= current) is the last index.
	now := time.Now()
	period := strings.ToLower(strings.TrimSpace(c.Query("period")))
	if period == "" {
		period = "month"
	}
	var bucketCount int
	switch period {
	case "day":
		bucketCount = 7 // last 7 days
	case "quarter":
		bucketCount = 4 // last 4 quarters
	case "year":
		bucketCount = 5 // last 5 years
	default: // "month"
		period = "month"
		bucketCount = 6
	}

	type timeBucket struct {
		start time.Time
		end   time.Time
		label string
	}
	buckets := make([]timeBucket, bucketCount)
	labels := make([]string, bucketCount)
	for i := 0; i < bucketCount; i++ {
		offsetFromNow := bucketCount - 1 - i
		var start, end time.Time
		var label string
		switch period {
		case "day":
			d := time.Date(now.Year(), now.Month(), now.Day()-offsetFromNow, 0, 0, 0, 0, now.Location())
			start = d
			end = d.AddDate(0, 0, 1)
			label = d.Format("Jan 2")
		case "quarter":
			currentQ := (int(now.Month()) - 1) / 3
			currentQStart := time.Date(now.Year(), time.Month(currentQ*3+1), 1, 0, 0, 0, 0, now.Location())
			start = currentQStart.AddDate(0, -3*offsetFromNow, 0)
			end = start.AddDate(0, 3, 0)
			q := (int(start.Month())-1)/3 + 1
			label = fmt.Sprintf("Q%d %d", q, start.Year())
		case "year":
			start = time.Date(now.Year()-offsetFromNow, 1, 1, 0, 0, 0, 0, now.Location())
			end = start.AddDate(1, 0, 0)
			label = fmt.Sprintf("%d", start.Year())
		default: // "month"
			d := time.Date(now.Year(), now.Month()-time.Month(offsetFromNow), 1, 0, 0, 0, 0, now.Location())
			start = d
			end = d.AddDate(0, 1, 0)
			label = d.Format("Jan")
		}
		buckets[i] = timeBucket{start: start, end: end, label: label}
		labels[i] = label
	}

	// Earliest/latest — used to scope the revenue/expense SQL queries
	// so totals also respect the selected period.
	rangeStart := buckets[0].start
	rangeEnd := buckets[len(buckets)-1].end

	bucketIdx := func(t time.Time) int {
		for i, b := range buckets {
			if !t.Before(b.start) && t.Before(b.end) {
				return i
			}
		}
		return -1
	}

	// 1. All organizations for this tenant (base skeleton)
	orgRows, err := h.db.Query(
		`SELECT id, COALESCE(name, '') FROM organizations WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY name`,
		tenantID,
	)
	if err != nil {
		h.log.Error("director-summary: org query failed", "error", err)
		response.InternalError(c, "Failed to load organizations")
		return
	}
	defer orgRows.Close()
	byOrg := make(map[string]*directorCompanySummary)
	order := make([]string, 0, 32)
	for orgRows.Next() {
		var id uuid.UUID
		var name string
		if err := orgRows.Scan(&id, &name); err != nil {
			continue
		}
		idStr := id.String()
		byOrg[idStr] = &directorCompanySummary{
			ID:                idStr,
			Name:              name,
			MonthlyRevenue:    make([]float64, bucketCount),
			ExpenseByCategory: map[string]float64{},
		}
		order = append(order, idStr)
	}

	// Ensure a row exists before indexing
	touch := func(orgID string) *directorCompanySummary {
		s, ok := byOrg[orgID]
		if !ok {
			return nil
		}
		return s
	}

	// 2. Revenue — from the posted ledger (revenue-category lines), the same
	//    source Moliya's dashboard reads. The previous sales_invoices sum
	//    disagreed with Moliya's numbers and counted unposted documents
	//    (docs/moliya-audit.md §2.7 — one source of truth).
	revRows, err := h.db.Query(`
		SELECT je.organization_id, je.entry_date,
		       SUM(l.credit_amount - l.debit_amount) AS amt
		FROM journal_entry_lines l
		JOIN journal_entries je ON je.id = l.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.organization_id IS NOT NULL
			AND je.entry_date >= $2 AND je.entry_date < $3
		JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
		JOIN account_types at ON at.id = a.account_type_id AND at.category = 'revenue'
		WHERE je.tenant_id = $1
		GROUP BY je.organization_id, je.entry_date`,
		tenantID, rangeStart, rangeEnd,
	)
	if err == nil {
		for revRows.Next() {
			var orgID uuid.UUID
			var entryDate time.Time
			var amt float64
			if err := revRows.Scan(&orgID, &entryDate, &amt); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.Revenue += amt
			if idx := bucketIdx(entryDate); idx >= 0 {
				s.MonthlyRevenue[idx] += amt
			}
		}
		revRows.Close()
	}

	// 3. Expenses — from the posted ledger (expense-category lines), so
	//    payroll and COGS are included (the old expenses-table sum missed
	//    both). The donut breakdown is "current bucket" and is
	//    category-enriched: expense-sourced lines take their Xarajatlar
	//    category name, everything else the GL account name.
	expRows, err := h.db.Query(`
		SELECT je.organization_id, je.entry_date,
		       SUM(l.debit_amount - l.credit_amount) AS amt,
		       COALESCE(ec.name, COALESCE(a.name_uz, a.name)) AS category
		FROM journal_entry_lines l
		JOIN journal_entries je ON je.id = l.journal_entry_id
			AND je.status = 'posted' AND je.deleted_at IS NULL
			AND je.organization_id IS NOT NULL
			AND je.entry_date >= $2 AND je.entry_date < $3
		JOIN accounts a ON a.id = l.account_id AND a.tenant_id = $1 AND a.deleted_at IS NULL
		JOIN account_types at ON at.id = a.account_type_id AND at.category = 'expense'
		LEFT JOIN expenses e ON je.source_type = 'expense' AND e.id = je.source_id
		LEFT JOIN expense_categories ec ON ec.id = e.category_id
		WHERE je.tenant_id = $1
		GROUP BY je.organization_id, je.entry_date, COALESCE(ec.name, COALESCE(a.name_uz, a.name))`,
		tenantID, rangeStart, rangeEnd,
	)
	if err == nil {
		latestStart := buckets[len(buckets)-1].start
		latestEnd := buckets[len(buckets)-1].end
		for expRows.Next() {
			var orgID uuid.UUID
			var entryDate time.Time
			var amt float64
			var cat string
			if err := expRows.Scan(&orgID, &entryDate, &amt, &cat); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.Expenses += amt
			if !entryDate.Before(latestStart) && entryDate.Before(latestEnd) && amt > 0 {
				s.ExpenseByCategory[cat] += amt
			}
		}
		expRows.Close()
	}

	// 4. Debtors / Creditors — open invoices minus payments (amount_due),
	//    the same computation as AR/AP aging. contacts.current_balance was
	//    a third balance store that could drift (docs/moliya-audit.md §2.7).
	// Netted per partner within each organization, then summed — the same
	// definition the finance dashboard card uses (debt_definition.go). The
	// previous `amount_due > 0` filtered invoice by invoice, so an overpaid
	// invoice was dropped instead of offsetting what the same customer still
	// owed, and the director's figure exceeded the finance panel's for the
	// same tenant.
	cRows, err := h.db.Query(`
		SELECT organization_id, COALESCE(SUM(due) FILTER (WHERE due > 0.005), 0)
		FROM (`+partnerNetDue("sales_invoices", "customer_id", "organization_id", " AND organization_id IS NOT NULL")+`) p
		GROUP BY organization_id`,
		tenantID,
	)
	if err == nil {
		for cRows.Next() {
			var orgID uuid.UUID
			var deb float64
			if err := cRows.Scan(&orgID, &deb); err != nil {
				continue
			}
			if s := touch(orgID.String()); s != nil {
				s.Debtors = deb
			}
		}
		cRows.Close()
	}
	crRows, err := h.db.Query(`
		SELECT organization_id, COALESCE(SUM(due) FILTER (WHERE due > 0.005), 0)
		FROM (`+partnerNetDue("purchase_invoices", "vendor_id", "organization_id", " AND organization_id IS NOT NULL")+`) p
		GROUP BY organization_id`,
		tenantID,
	)
	if err == nil {
		for crRows.Next() {
			var orgID uuid.UUID
			var cred float64
			if err := crRows.Scan(&orgID, &cred); err != nil {
				continue
			}
			if s := touch(orgID.String()); s != nil {
				s.Creditors = cred
			}
		}
		crRows.Close()
	}

	// 5. Inventory — stock units + stock value + low-stock count via warehouse.organization_id
	invRows, err := h.db.Query(`
		SELECT w.organization_id,
		       COALESCE(SUM(i.quantity_on_hand), 0) AS qty,
		       COALESCE(SUM(i.quantity_on_hand * COALESCE(p.cost_price, i.unit_cost, 0)), 0) AS value,
		       COUNT(DISTINCT p.id) FILTER (
		         WHERE COALESCE(p.min_stock_level, 0) > 0
		           AND i.quantity_on_hand <= COALESCE(p.min_stock_level, 0)
		       ) AS low_stock
		FROM inventory i
		JOIN warehouses w ON w.id = i.warehouse_id
		LEFT JOIN products p ON p.id = i.product_id
		WHERE i.tenant_id = $1 AND w.organization_id IS NOT NULL
		GROUP BY w.organization_id`,
		tenantID,
	)
	if err == nil {
		for invRows.Next() {
			var orgID uuid.UUID
			var qty, val float64
			var low int
			if err := invRows.Scan(&orgID, &qty, &val, &low); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.StockUnits = qty
			s.StockValue = val
			s.LowStockCount = low
		}
		invRows.Close()
	}

	// 6. Employees — total + active + salary fund per org (direct org + many-to-many)
	//    Union the two sources, then aggregate in Go to avoid double-counting.
	empRows, err := h.db.Query(`
		SELECT DISTINCT e.id, COALESCE(org_id, e.organization_id) AS org_id,
		       COALESCE(e.base_salary, 0),
		       COALESCE(e.termination_date IS NULL, true) AS is_active
		FROM employees e
		LEFT JOIN LATERAL (
		  SELECT eo.organization_id AS org_id
		  FROM employee_organizations eo
		  WHERE eo.employee_id = e.id
		) eo_link ON true
		WHERE e.tenant_id = $1 AND e.deleted_at IS NULL
		  AND COALESCE(eo_link.org_id, e.organization_id) IS NOT NULL`,
		tenantID,
	)
	if err == nil {
		// employee rows may repeat per-org via the lateral join; that's intentional —
		// each (emp, org) pair contributes once to that org's counters.
		type empSeen struct {
			emp string
			org string
		}
		seen := map[empSeen]bool{}
		for empRows.Next() {
			var empID, orgID uuid.UUID
			var salary float64
			var active bool
			if err := empRows.Scan(&empID, &orgID, &salary, &active); err != nil {
				continue
			}
			key := empSeen{emp: empID.String(), org: orgID.String()}
			if seen[key] {
				continue
			}
			seen[key] = true
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.TotalEmployees++
			if active {
				s.ActiveEmployees++
			}
			s.SalaryFund += salary
		}
		empRows.Close()
	}

	// 6b. Payroll — actual calculated wage fund per org from payroll_periods
	//     (periods whose end_date falls inside the selected window; cancelled
	//     periods excluded), unlike SalaryFund which is a static headcount
	//     snapshot from employees.base_salary.
	pfRows, err := h.db.Query(`
		SELECT pp.organization_id, COALESCE(SUM(pp.total_net), 0)
		FROM payroll_periods pp
		WHERE pp.tenant_id = $1 AND pp.deleted_at IS NULL AND pp.organization_id IS NOT NULL
		  AND pp.status <> 'cancelled'
		  AND pp.end_date >= $2 AND pp.end_date <= $3
		GROUP BY pp.organization_id`,
		tenantID, rangeStart, rangeEnd,
	)
	if err == nil {
		for pfRows.Next() {
			var orgID uuid.UUID
			var total float64
			if err := pfRows.Scan(&orgID, &total); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.PayrollFund = total
		}
		pfRows.Close()
	}

	// 6c. Payroll unpaid — confirmed (approved) but not yet paid periods,
	//     all time: the remainder the director still owes as wages.
	puRows, err := h.db.Query(`
		SELECT pp.organization_id, COALESCE(SUM(pp.total_net), 0)
		FROM payroll_periods pp
		WHERE pp.tenant_id = $1 AND pp.deleted_at IS NULL AND pp.organization_id IS NOT NULL
		  AND pp.status = 'approved'
		GROUP BY pp.organization_id`,
		tenantID,
	)
	if err == nil {
		for puRows.Next() {
			var orgID uuid.UUID
			var total float64
			if err := puRows.Scan(&orgID, &total); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.PayrollUnpaid = total
		}
		puRows.Close()
	}

	// 7. Top stock products per org (top 8 by value) — for warehouse diagram
	topRows, err := h.db.Query(`
		SELECT org_id, name, qty, value FROM (
			SELECT w.organization_id AS org_id,
			       COALESCE(p.name, 'N/A') AS name,
			       COALESCE(SUM(i.quantity_on_hand), 0) AS qty,
			       COALESCE(SUM(i.quantity_on_hand * COALESCE(p.cost_price, i.unit_cost, 0)), 0) AS value,
			       ROW_NUMBER() OVER (PARTITION BY w.organization_id ORDER BY COALESCE(SUM(i.quantity_on_hand * COALESCE(p.cost_price, i.unit_cost, 0)), 0) DESC) AS rn
			FROM inventory i
			JOIN warehouses w ON w.id = i.warehouse_id
			LEFT JOIN products p ON p.id = i.product_id
			WHERE i.tenant_id = $1 AND w.organization_id IS NOT NULL
			GROUP BY w.organization_id, p.id, p.name
		) t WHERE rn <= 8`,
		tenantID,
	)
	if err == nil {
		for topRows.Next() {
			var orgID uuid.UUID
			var name string
			var qty, val float64
			if err := topRows.Scan(&orgID, &name, &qty, &val); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.TopStockProducts = append(s.TopStockProducts, directorTopStockProduct{Name: name, Qty: qty, Value: val})
		}
		topRows.Close()
	}

	// 8. Construction projects per org (active/in-progress) — for Активные объекты.
	// Progress — smeta ishlaridan hisoblangan cost-weighted tayyorlik (stats
	// bilan bir xil formula); ishlar bo'lmasa manual progress_percent fallback.
	// Manual ustunning yozuv yo'li yopilgan (6-to'plam) — undan to'g'ridan-
	// to'g'ri o'qish doim 0 ko'rsatardi.
	projRows, err := h.db.Query(`
		SELECT p.id, p.organization_id, COALESCE(p.code, ''), COALESCE(p.name, ''),
		       COALESCE(p.status, ''),
		       COALESCE(r.readiness_pct, COALESCE(p.progress_percent, 0)) AS progress,
		       COALESCE(p.contract_amount, 0)
		FROM construction_projects p
		LEFT JOIN LATERAL (
			SELECT CASE WHEN SUM(COALESCE(el.total_amount, 0)) > 0
			            THEN SUM(COALESCE(el.total_amount, 0) * LEAST(COALESCE(el.done_quantity, 0) / NULLIF(CASE
			                     WHEN COALESCE(el.imported_quantity, 0) > 0 THEN el.imported_quantity
			                     WHEN COALESCE(el.original_quantity, 0) > 0 THEN el.original_quantity
			                     ELSE COALESCE(el.quantity, 0) END, 0), 1))
			                 / SUM(COALESCE(el.total_amount, 0)) * 100
			       END AS readiness_pct
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
			WHERE e.project_id = p.id AND el.tenant_id = p.tenant_id
			  AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
			  AND COALESCE(el.resource_type, '') = ''
			  AND COALESCE(el.parent_line_id, 0) = 0
		) r ON true
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.organization_id IS NOT NULL
		  AND COALESCE(p.status, '') NOT IN ('cancelled', 'archived')
		ORDER BY p.organization_id, progress DESC, p.id`,
		tenantID,
	)
	if err == nil {
		for projRows.Next() {
			var id int64
			var orgID uuid.UUID
			var code, name, status string
			var progress, amt float64
			if err := projRows.Scan(&id, &orgID, &code, &name, &status, &progress, &amt); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.ConstructionProjects = append(s.ConstructionProjects, directorConstructionProjct{
				ID: id, Name: name, Code: code, Status: status,
				Progress: progress, ContractAmount: amt,
			})
		}
		projRows.Close()
	}

	// 9. Shartnomalar aggregates per org — amaldagi shartnomalar summasi,
	// muddati tugayotganlar soni, to'lanmagan qoldiq.
	contractRows, err := h.db.Query(`
		SELECT c.organization_id,
		       COUNT(*) FILTER (WHERE c.status = 'active'),
		       COALESCE(SUM(COALESCE(c.value, 0) + am.delta_sum) FILTER (WHERE c.status = 'active'), 0),
		       COUNT(*) FILTER (WHERE c.status = 'active' AND c.end_date IS NOT NULL
		           AND c.end_date >= CURRENT_DATE AND c.end_date <= CURRENT_DATE + INTERVAL '30 days'),
		       COALESCE(SUM(COALESCE(c.value, 0) + am.delta_sum - inv.paid_total) FILTER (WHERE c.status = 'active'), 0)
		FROM procurement_contracts c
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(a.amount_delta), 0) AS delta_sum
			FROM contract_amendments a WHERE a.contract_id = c.id AND a.deleted_at IS NULL
		) am ON true
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(x.amount_paid), 0) AS paid_total FROM (
				SELECT si.amount_paid FROM sales_invoices si
				WHERE si.contract_id = c.id AND si.deleted_at IS NULL AND si.status <> 'cancelled'
				UNION ALL
				SELECT pi.amount_paid FROM purchase_invoices pi
				WHERE pi.contract_id = c.id AND pi.deleted_at IS NULL AND pi.status <> 'cancelled'
			) x
		) inv ON true
		WHERE c.tenant_id = $1 AND c.deleted_at IS NULL AND c.archived_at IS NULL
		  AND c.organization_id IS NOT NULL
		GROUP BY c.organization_id`,
		tenantID,
	)
	if err == nil {
		for contractRows.Next() {
			var orgID uuid.UUID
			var active, expiring int
			var activeValue, outstanding float64
			if err := contractRows.Scan(&orgID, &active, &activeValue, &expiring, &outstanding); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.ActiveContracts = active
			s.ActiveContractsValue = activeValue
			s.ExpiringContracts = expiring
			s.ContractsOutstanding = outstanding
		}
		contractRows.Close()
	}

	// 9b. Xarid aggregates per org — davr xaridi (tanlangan period oynasida),
	// kechikkan yetkazmalar; AP alohida so'rovda GL 6010 dan.
	poAggRows, err := h.db.Query(`
		SELECT po.organization_id,
		       COALESCE(SUM(po.total_amount) FILTER (WHERE po.order_date >= $2 AND po.order_date < $3), 0),
		       COUNT(*) FILTER (WHERE po.order_date >= $2 AND po.order_date < $3),
		       COUNT(*) FILTER (WHERE po.status IN ('approved', 'ordered', 'partial')
		           AND po.expected_date IS NOT NULL AND po.expected_date < CURRENT_DATE)
		FROM purchase_orders po
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL AND po.status != 'cancelled'
		  AND po.organization_id IS NOT NULL
		GROUP BY po.organization_id`,
		tenantID, rangeStart, rangeEnd,
	)
	if err == nil {
		for poAggRows.Next() {
			var orgID uuid.UUID
			var total float64
			var count, overdue int
			if err := poAggRows.Scan(&orgID, &total, &count, &overdue); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.PurchasesPeriodTotal = total
			s.PurchasesPeriodCount = count
			s.PurchaseOverdueDeliveries = overdue
		}
		poAggRows.Close()
	}

	// Supplier AP from posted GL lines on 6010 (same source as
	// /purchase-orders/stats — NOT contacts.current_balance).
	apRows, err := h.db.Query(`
		SELECT je.organization_id, COALESCE(SUM(jel.credit_amount - jel.debit_amount), 0)
		FROM journal_entry_lines jel
		JOIN journal_entries je ON je.id = jel.journal_entry_id AND je.status = 'posted'
		JOIN accounts a ON a.id = jel.account_id
		WHERE je.tenant_id = $1 AND a.code = '6010' AND je.organization_id IS NOT NULL
		GROUP BY je.organization_id`,
		tenantID,
	)
	if err == nil {
		for apRows.Next() {
			var orgID uuid.UUID
			var ap float64
			if err := apRows.Scan(&orgID, &ap); err != nil {
				continue
			}
			if s := touch(orgID.String()); s != nil {
				s.PurchaseAPTotal = ap
			}
		}
		apRows.Close()
	}

	// 10. CRM aggregates per org — ochiq voronka (soni + summasi), bu oy
	// yutilganlar, umumiy konversiya %, eng ko'p yo'qotish sababi.
	crmRows, err := h.db.Query(`
		SELECT l.organization_id,
		       COUNT(*) FILTER (WHERE l.won_at IS NULL AND l.lost_at IS NULL),
		       COALESCE(SUM(l.expected_value) FILTER (WHERE l.won_at IS NULL AND l.lost_at IS NULL), 0),
		       COUNT(*) FILTER (WHERE l.won_at >= date_trunc('month', CURRENT_DATE)),
		       COALESCE(SUM(l.expected_value) FILTER (WHERE l.won_at >= date_trunc('month', CURRENT_DATE)), 0),
		       COALESCE(COUNT(*) FILTER (WHERE l.won_at IS NOT NULL)::float /
		                NULLIF(COUNT(*) FILTER (WHERE l.won_at IS NOT NULL OR l.lost_at IS NOT NULL), 0) * 100, 0)
		FROM leads l
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL AND l.organization_id IS NOT NULL
		GROUP BY l.organization_id`,
		tenantID,
	)
	if err == nil {
		for crmRows.Next() {
			var orgID uuid.UUID
			var open, wonMonth int
			var pipelineValue, wonValueMonth, conversion float64
			if err := crmRows.Scan(&orgID, &open, &pipelineValue, &wonMonth, &wonValueMonth, &conversion); err != nil {
				continue
			}
			s := touch(orgID.String())
			if s == nil {
				continue
			}
			s.CRMOpenLeads = open
			s.CRMPipelineValue = pipelineValue
			s.CRMWonMonth = wonMonth
			s.CRMWonValueMonth = wonValueMonth
			s.CRMConversion = math.Round(conversion*10) / 10
		}
		crmRows.Close()
	}
	lossRows, err := h.db.Query(`
		SELECT DISTINCT ON (l.organization_id) l.organization_id, r.name
		FROM leads l
		JOIN lost_reasons r ON r.id = l.lost_reason_id
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL AND l.lost_at IS NOT NULL
		  AND l.organization_id IS NOT NULL
		GROUP BY l.organization_id, r.name
		ORDER BY l.organization_id, COUNT(*) DESC`,
		tenantID,
	)
	if err == nil {
		for lossRows.Next() {
			var orgID uuid.UUID
			var reason string
			if err := lossRows.Scan(&orgID, &reason); err != nil {
				continue
			}
			if s := touch(orgID.String()); s != nil {
				s.CRMTopLossReason = reason
			}
		}
		lossRows.Close()
	}

	// Compute profit and round output
	companies := make([]directorCompanySummary, 0, len(order))
	round2 := func(v float64) float64 { return math.Round(v*100) / 100 }
	for _, id := range order {
		s := byOrg[id]
		s.Profit = s.Revenue - s.Expenses
		s.Revenue = round2(s.Revenue)
		s.Expenses = round2(s.Expenses)
		s.Profit = round2(s.Profit)
		s.Debtors = round2(s.Debtors)
		s.Creditors = round2(s.Creditors)
		s.StockValue = round2(s.StockValue)
		s.SalaryFund = round2(s.SalaryFund)
		s.PayrollFund = round2(s.PayrollFund)
		s.PayrollUnpaid = round2(s.PayrollUnpaid)
		s.CRMPipelineValue = round2(s.CRMPipelineValue)
		s.CRMWonValueMonth = round2(s.CRMWonValueMonth)
		for i, v := range s.MonthlyRevenue {
			s.MonthlyRevenue[i] = round2(v)
		}
		for k, v := range s.ExpenseByCategory {
			s.ExpenseByCategory[k] = round2(v)
		}
		companies = append(companies, *s)
	}

	// Grand totals
	var totRev, totExp, totDeb, totCred, totStockVal, totSalary float64
	var totPayrollFund, totPayrollUnpaid float64
	var totStockUnits float64
	var totLow, totEmps, totActive int
	for _, s := range companies {
		totRev += s.Revenue
		totExp += s.Expenses
		totDeb += s.Debtors
		totCred += s.Creditors
		totStockVal += s.StockValue
		totStockUnits += s.StockUnits
		totLow += s.LowStockCount
		totEmps += s.TotalEmployees
		totActive += s.ActiveEmployees
		totSalary += s.SalaryFund
		totPayrollFund += s.PayrollFund
		totPayrollUnpaid += s.PayrollUnpaid
	}

	// Savdo bloki (savdo-audit §6.7): the director previously saw revenue (GL) and
	// debtors, but no order flow at all — a confirmed-but-uninvoiced order was
	// invisible. Period scope matches the report's selected range.
	var salesOrdersCount, salesOverdueCount int
	var salesOrdersSum, salesOverdueSum float64
	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_amount), 0)
		FROM sales_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND status NOT IN ('cancelled', 'quotation')
		  AND order_date >= $2 AND order_date < $3`,
		tenantID, rangeStart, rangeEnd).Scan(&salesOrdersCount, &salesOrdersSum)
	// Two different questions, so two queries rather than one pair of numbers
	// that half-answers both.
	//
	// overdue_invoices counts INVOICES — that is what the field is called and
	// what the screen shows — so it stays at invoice level.
	//
	// overdue_receivables is money, and money nets per partner, matching the
	// dashboard's `overdue`. A customer sitting in credit overall contributes
	// nothing to the amount even while their overdue invoices are still
	// counted, which is both true statements at once.
	//
	// The status filter was IN ('sent','partial') — a fourth vocabulary, and
	// the only one that dropped invoices whose status is literally 'overdue'
	// out of the overdue figure.
	h.db.QueryRow(`
		SELECT COUNT(*)
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND `+debtStatusFilter+` AND amount_due > 0
		  AND due_date < CURRENT_DATE`,
		tenantID).Scan(&salesOverdueCount)
	h.db.QueryRow(`
		SELECT COALESCE(SUM(overdue) FILTER (WHERE overdue > 0.005), 0)
		FROM (`+partnerNetDue("sales_invoices", "customer_id", "", "")+`) p`,
		tenantID).Scan(&salesOverdueSum)

	// Aktivlar bloki (aktivlar-audit §10): the director saw no fixed-asset
	// numbers at all — NBV, this-month depreciation and the fleet status were
	// invisible outside the GL expense total.
	var assetsCount, assetsInService, assetsConserved int
	var assetsCost, assetsBook float64
	h.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'in_service'),
		       COUNT(*) FILTER (WHERE status = 'conserved'),
		       COALESCE(SUM(cost) FILTER (WHERE status <> 'disposed'), 0),
		       COALESCE(SUM(cost - accumulated_depreciation) FILTER (WHERE status <> 'disposed'), 0)
		FROM fa_assets WHERE tenant_id = $1 AND deleted_at IS NULL`,
		tenantID).Scan(&assetsCount, &assetsInService, &assetsConserved, &assetsCost, &assetsBook)
	var assetsMonthDepr float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(amount), 0) FROM fa_depreciation_entries
		WHERE tenant_id = $1 AND status = 'active' AND journal_entry_id IS NOT NULL
		  AND period = to_char(NOW(), 'YYYY-MM')`, tenantID).Scan(&assetsMonthDepr)

	// Latest USD→UZS rate, so the panel's Som/USD toggle has something to
	// convert with. The toggle shipped as pure decoration — state was set and
	// never read (manual test report BUG-03). 0 = no rate configured; the
	// client keeps the toggle disabled in that case rather than dividing by
	// a guess.
	var usdRate float64
	h.db.QueryRow(`
		SELECT er.rate
		FROM exchange_rates er
		JOIN currencies cf ON cf.id = er.from_currency_id
		JOIN currencies ct ON ct.id = er.to_currency_id
		WHERE er.tenant_id = $1 AND cf.code = 'USD' AND ct.code = 'UZS'
		ORDER BY er.effective_date DESC, er.created_at DESC LIMIT 1`,
		tenantID).Scan(&usdRate)

	response.Success(c, gin.H{
		"companies":    companies,
		"month_labels": labels,
		"usd_rate":     usdRate,
		"sales": gin.H{
			"orders_count":        salesOrdersCount,
			"orders_sum":          round2(salesOrdersSum),
			"overdue_invoices":    salesOverdueCount,
			"overdue_receivables": round2(salesOverdueSum),
		},
		"assets": gin.H{
			"total_count":        assetsCount,
			"in_service":         assetsInService,
			"conserved":          assetsConserved,
			"total_cost":         round2(assetsCost),
			"total_book_value":   round2(assetsBook),
			"month_depreciation": round2(assetsMonthDepr),
		},
		"totals": gin.H{
			"revenue":          round2(totRev),
			"expenses":         round2(totExp),
			"profit":           round2(totRev - totExp),
			"debtors":          round2(totDeb),
			"creditors":        round2(totCred),
			"stock_units":      round2(totStockUnits),
			"stock_value":      round2(totStockVal),
			"low_stock_count":  totLow,
			"total_employees":  totEmps,
			"active_employees": totActive,
			"salary_fund":      round2(totSalary),
			"payroll_fund":     round2(totPayrollFund),
			"payroll_unpaid":   round2(totPayrollUnpaid),
		},
	})
}
