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
		SELECT a.id, a.code, a.name, at.category, at.normal_balance,
			   COALESCE(SUM(jel.debit_amount), 0) as total_debit,
			   COALESCE(SUM(jel.credit_amount), 0) as total_credit
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON a.id = jel.account_id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.entry_date <= $2 AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
		GROUP BY a.id, a.code, a.name, at.category, at.normal_balance
		HAVING COALESCE(SUM(jel.debit_amount), 0) > 0 OR COALESCE(SUM(jel.credit_amount), 0) > 0
		ORDER BY a.code
	`

	rows, err := h.db.Query(query, tenantID, asOfDate)
	if err != nil {
		h.log.Error("Failed to get trial balance", "error", err)
		response.InternalError(c, "Failed to generate trial balance")
		return
	}
	defer rows.Close()

	accounts := make([]entity.TrialBalanceAccount, 0)
	var totalDebit, totalCredit float64

	for rows.Next() {
		var acc entity.TrialBalanceAccount
		var normalBalance string
		var debitSum, creditSum float64

		err := rows.Scan(&acc.AccountID, &acc.AccountCode, &acc.AccountName, &acc.Category, &normalBalance, &debitSum, &creditSum)
		if err != nil {
			continue
		}

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
		SELECT a.id, a.code, a.name, at.category, at.normal_balance,
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
		GROUP BY a.id, a.code, a.name, at.category, at.normal_balance, a.opening_balance
		ORDER BY at.category, a.code
	`

	rows, err := h.db.Query(query, tenantID, asOfDate)
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
		var code, name, category, normalBalance string
		var openingBalance, debitSum, creditSum float64

		err := rows.Scan(&accountID, &code, &name, &category, &normalBalance, &openingBalance, &debitSum, &creditSum)
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

		if math.Abs(balance) < 0.01 {
			continue // Skip zero balances
		}

		acc := entity.BalanceSheetAccount{
			AccountID:   accountID,
			AccountCode: code,
			AccountName: name,
			Balance:     math.Round(balance*100) / 100,
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
		SELECT a.id, a.code, a.name, at.category, at.normal_balance,
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
		GROUP BY a.id, a.code, a.name, at.category, at.normal_balance
		HAVING COALESCE(SUM(jel.debit_amount), 0) > 0 OR COALESCE(SUM(jel.credit_amount), 0) > 0
		ORDER BY at.category DESC, a.code
	`

	rows, err := h.db.Query(query, tenantID, periodFrom, periodTo)
	if err != nil {
		h.log.Error("Failed to get income statement", "error", err)
		response.InternalError(c, "Failed to generate income statement")
		return
	}
	defer rows.Close()

	revenue := make([]entity.IncomeStatementSection, 0)
	expenses := make([]entity.IncomeStatementSection, 0)
	var totalRevenue, totalExpenses float64

	for rows.Next() {
		var accountID uuid.UUID
		var code, name, category, normalBalance string
		var debitSum, creditSum float64

		err := rows.Scan(&accountID, &code, &name, &category, &normalBalance, &debitSum, &creditSum)
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
			AccountID:   accountID,
			AccountCode: code,
			AccountName: name,
			Amount:      math.Round(amount*100) / 100,
		}

		if category == "revenue" {
			revenue = append(revenue, section)
			totalRevenue += amount
		} else {
			expenses = append(expenses, section)
			totalExpenses += amount
		}
	}

	netIncome := totalRevenue - totalExpenses

	report := entity.IncomeStatementReport{
		PeriodFrom:        periodFrom,
		PeriodTo:          periodTo,
		TotalRevenue:      math.Round(totalRevenue*100) / 100,
		TotalExpenses:     math.Round(totalExpenses*100) / 100,
		GrossProfit:       math.Round(totalRevenue*100) / 100, // Simplified - no COGS separation
		OperatingProfit:   math.Round(netIncome*100) / 100,
		NetIncome:         math.Round(netIncome*100) / 100,
		Revenue:           revenue,
		OperatingExpenses: expenses,
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

	if accountID != "" {
		accountQuery += " AND a.id = $2"
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

	// Get cash/bank account balances
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
			AND (a.is_bank_account = true OR at.code IN ('1010', '1020'))
	`

	var openingCash, periodDebits, periodCredits float64
	err := h.db.QueryRow(cashQuery, tenantID, periodFrom, periodTo).Scan(&openingCash, &periodDebits, &periodCredits)
	if err != nil {
		h.log.Error("Failed to get cash balances", "error", err)
		// Continue with zeros
	}

	netCashChange := periodDebits - periodCredits
	closingCash := openingCash + netCashChange

	// Simplified cash flow - in real implementation, categorize by account types
	report := entity.CashFlowReport{
		PeriodFrom:         periodFrom,
		PeriodTo:           periodTo,
		OpeningCashBalance: math.Round(openingCash*100) / 100,
		ClosingCashBalance: math.Round(closingCash*100) / 100,
		NetCashChange:      math.Round(netCashChange*100) / 100,
		OperatingActivities: entity.CashFlowSection{
			Total: math.Round(netCashChange*100) / 100,
			Items: []entity.CashFlowItem{
				{Description: "Net cash from operations", Amount: math.Round(netCashChange*100) / 100},
			},
		},
		InvestingActivities: entity.CashFlowSection{
			Total: 0,
			Items: []entity.CashFlowItem{},
		},
		FinancingActivities: entity.CashFlowSection{
			Total: 0,
			Items: []entity.CashFlowItem{},
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

	query := `
		SELECT si.id, si.invoice_number, si.invoice_date, si.due_date, si.total_amount,
			   si.total_amount - si.amount_paid as amount_due,
			   c.id as contact_id, c.name as contact_name,
			   ($2::date - si.due_date)::int as days_overdue
		FROM sales_invoices si
		JOIN contacts c ON si.customer_id = c.id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL
			AND si.status NOT IN ('cancelled', 'paid')
			AND si.total_amount > si.amount_paid
		ORDER BY c.name, si.due_date
	`

	rows, err := h.db.Query(query, tenantID, asOfDate)
	if err != nil {
		h.log.Error("Failed to get aging receivables", "error", err)
		response.InternalError(c, "Failed to generate aging report")
		return
	}
	defer rows.Close()

	contactMap := make(map[uuid.UUID]*entity.AgingContact)
	var totalAmount, currentTotal, days1To30, days31To60, days61To90, over90Days float64

	for rows.Next() {
		var invoiceID, contactID uuid.UUID
		var invoiceNumber, contactName string
		var invoiceDate, dueDate time.Time
		var total, amountDue float64
		var daysOverdue int

		err := rows.Scan(&invoiceID, &invoiceNumber, &invoiceDate, &dueDate, &total, &amountDue,
			&contactID, &contactName, &daysOverdue)
		if err != nil {
			continue
		}

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

		// Determine aging bucket
		var bucket string
		if daysOverdue <= 0 {
			bucket = "current"
			contact.Current += amountDue
			currentTotal += amountDue
		} else if daysOverdue <= 30 {
			bucket = "1-30"
			contact.Days1To30 += amountDue
			days1To30 += amountDue
		} else if daysOverdue <= 60 {
			bucket = "31-60"
			contact.Days31To60 += amountDue
			days31To60 += amountDue
		} else if daysOverdue <= 90 {
			bucket = "61-90"
			contact.Days61To90 += amountDue
			days61To90 += amountDue
		} else {
			bucket = "90+"
			contact.Over90Days += amountDue
			over90Days += amountDue
		}

		invoice := entity.AgingInvoice{
			InvoiceID:     invoiceID,
			InvoiceNumber: invoiceNumber,
			InvoiceDate:   invoiceDate.Format("2006-01-02"),
			DueDate:       dueDate.Format("2006-01-02"),
			TotalAmount:   total,
			AmountDue:     amountDue,
			DaysOverdue:   daysOverdue,
			AgingBucket:   bucket,
		}

		contact.Invoices = append(contact.Invoices, invoice)
		contact.TotalAmount += amountDue
		totalAmount += amountDue
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

	query := `
		SELECT pi.id, pi.invoice_number, pi.invoice_date, pi.due_date, pi.total_amount,
			   pi.total_amount - pi.amount_paid as amount_due,
			   c.id as contact_id, c.name as contact_name,
			   ($2::date - pi.due_date)::int as days_overdue
		FROM purchase_invoices pi
		JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.tenant_id = $1 AND pi.deleted_at IS NULL
			AND pi.status NOT IN ('cancelled', 'paid')
			AND pi.total_amount > pi.amount_paid
		ORDER BY c.name, pi.due_date
	`

	rows, err := h.db.Query(query, tenantID, asOfDate)
	if err != nil {
		h.log.Error("Failed to get aging payables", "error", err)
		response.InternalError(c, "Failed to generate aging report")
		return
	}
	defer rows.Close()

	contactMap := make(map[uuid.UUID]*entity.AgingContact)
	var totalAmount, currentTotal, days1To30, days31To60, days61To90, over90Days float64

	for rows.Next() {
		var invoiceID, contactID uuid.UUID
		var invoiceNumber, contactName string
		var invoiceDate, dueDate time.Time
		var total, amountDue float64
		var daysOverdue int

		err := rows.Scan(&invoiceID, &invoiceNumber, &invoiceDate, &dueDate, &total, &amountDue,
			&contactID, &contactName, &daysOverdue)
		if err != nil {
			continue
		}

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
			contact.Current += amountDue
			currentTotal += amountDue
		} else if daysOverdue <= 30 {
			bucket = "1-30"
			contact.Days1To30 += amountDue
			days1To30 += amountDue
		} else if daysOverdue <= 60 {
			bucket = "31-60"
			contact.Days31To60 += amountDue
			days31To60 += amountDue
		} else if daysOverdue <= 90 {
			bucket = "61-90"
			contact.Days61To90 += amountDue
			days61To90 += amountDue
		} else {
			bucket = "90+"
			contact.Over90Days += amountDue
			over90Days += amountDue
		}

		invoice := entity.AgingInvoice{
			InvoiceID:     invoiceID,
			InvoiceNumber: invoiceNumber,
			InvoiceDate:   invoiceDate.Format("2006-01-02"),
			DueDate:       dueDate.Format("2006-01-02"),
			TotalAmount:   total,
			AmountDue:     amountDue,
			DaysOverdue:   daysOverdue,
			AgingBucket:   bucket,
		}

		contact.Invoices = append(contact.Invoices, invoice)
		contact.TotalAmount += amountDue
		totalAmount += amountDue
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

	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_amount), 0)
		FROM sales_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL
			AND order_date >= $2 AND order_date <= $3
	`, tenantID, periodFrom, periodTo).Scan(&totalOrders, &orderAmount)

	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_amount), 0), COALESCE(SUM(amount_paid), 0)
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL
			AND invoice_date >= $2 AND invoice_date <= $3
	`, tenantID, periodFrom, periodTo).Scan(&totalInvoiced, &invoicedAmount, &paidAmount)

	// Get top customers
	type TopCustomer struct {
		CustomerID   uuid.UUID `json:"customer_id"`
		CustomerName string    `json:"customer_name"`
		OrderCount   int       `json:"order_count"`
		TotalAmount  float64   `json:"total_amount"`
	}

	topCustomers := make([]TopCustomer, 0)
	rows, err := h.db.Query(`
		SELECT c.id, c.name, COUNT(si.id), COALESCE(SUM(si.total_amount), 0)
		FROM sales_invoices si
		JOIN contacts c ON si.customer_id = c.id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL
			AND si.invoice_date >= $2 AND si.invoice_date <= $3
		GROUP BY c.id, c.name
		ORDER BY SUM(si.total_amount) DESC
		LIMIT 10
	`, tenantID, periodFrom, periodTo)

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
