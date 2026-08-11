package handler

import (
	"fmt"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// What a bank account's balance is, and which accounts a screen is looking at,
// in one place.
//
// bank_accounts carries two numbers that both look like a balance. `balance` is
// a mutable column the account-creation form writes once as an opening figure
// and nothing keeps current; `ledger_balance` is SUM(debit - credit) over
// posted journal entry lines on the linked GL account — the Moliya v2 truth.
// The web read the second, the mobile app read the first, and the same tenant
// saw 153 344 032 so'm on one screen and 0 so'm on the other.
//
// Both the account list and the summary cards above it need this definition. It
// lived inline inside ListBankAccounts, so a summary that re-typed it would be
// the debt-card story again: one definition in two places, drifting apart.

// bankLedgerBalanceJoin exposes `lb.bal` — the posted-ledger balance of the GL
// account a bank account is linked to. Requires the bank_accounts row to be
// aliased `ba`. Accounts with no GL link (account_id IS NULL) match no lines
// and come back NULL, so every caller must COALESCE.
//
// je.tenant_id is compared to ba.tenant_id rather than to the query's tenant
// parameter: journal_entry_lines has no tenant column of its own, so without it
// a GL account id colliding across tenants would pull in another tenant's lines.
const bankLedgerBalanceJoin = `
		LEFT JOIN LATERAL (
			SELECT SUM(l.debit_amount - l.credit_amount) AS bal
			FROM journal_entry_lines l
			JOIN journal_entries je ON je.id = l.journal_entry_id
				AND je.status = 'posted' AND je.deleted_at IS NULL AND je.tenant_id = ba.tenant_id
			WHERE l.account_id = ba.account_id
		) lb ON true`

// bankAccountScope builds the WHERE predicates that decide which accounts a
// request is looking at: tenant, the caller's organization if the request
// carries one, and the list filters.
//
// Every predicate is qualified `ba.`, so the fragment drops into a query that
// joins bank_accounts to something else — which the unreconciled count does,
// because bank_transactions.organization_id is nullable and the statement
// importer (finance.go, ImportBankStatement) never fills it, so scoping
// transactions by their own column would hide every imported row the moment an
// organization is selected. A transaction belongs to its account's
// organization; that is the only definition the data supports.
//
// $1 is the tenant. argIndex comes back pointing at the next free placeholder.
func bankAccountScope(c *gin.Context, tenantID string, filter entity.BankAccountListFilter) (where string, args []interface{}, argIndex int) {
	where = "ba.tenant_id = $1 AND ba.deleted_at IS NULL"
	args = []interface{}{tenantID}
	argIndex = 2

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		where += fmt.Sprintf(" AND ba.organization_id = $%d", argIndex)
		args = append(args, orgID)
		argIndex++
	}

	if filter.Search != "" {
		where += fmt.Sprintf(" AND (COALESCE(ba.name, ba.bank_name) ILIKE $%d OR ba.bank_name ILIKE $%d OR ba.account_number ILIKE $%d)", argIndex, argIndex, argIndex)
		args = append(args, "%"+filter.Search+"%")
		argIndex++
	}

	if filter.Currency != "" {
		where += fmt.Sprintf(" AND COALESCE(ba.currency, 'UZS') = $%d", argIndex)
		args = append(args, filter.Currency)
		argIndex++
	}

	if filter.AccountType != "" {
		where += fmt.Sprintf(" AND COALESCE(ba.account_type, 'checking') = $%d", argIndex)
		args = append(args, filter.AccountType)
		argIndex++
	}

	if filter.IsActive != nil {
		where += fmt.Sprintf(" AND COALESCE(ba.is_active, true) = $%d", argIndex)
		args = append(args, *filter.IsActive)
		argIndex++
	}

	return where, args, argIndex
}

// bankTxUnreconciled is what "tasdiqlanmagan" means for a bank transaction.
//
// The status vocabulary is (unmatched, matched, reconciled) and the list
// endpoint hands the client is_reconciled as `status == "reconciled"`
// (finance.go, ListBankTransactions). Counting only 'unmatched' would therefore
// disagree with the rows the user is looking at: every transaction sitting in
// 'matched' — proposed against a journal entry but not yet confirmed — would
// vanish from the card while still showing as unreconciled in the table.
//
// bank_transactions has no deleted_at column (migration 004, and nothing since
// adds one); writing `t.deleted_at IS NULL` here fails the query outright.
const bankTxUnreconciled = `COALESCE(t.status, 'unmatched') <> 'reconciled'`

// GetBankAccountsSummary godoc
// @Summary Totals for the Pul oqimi — Bank hisobvaraqlari cards
// @Tags Finance - Bank Accounts
// @Produce json
// @Param search query string false "Filter by name, bank name or account number"
// @Param currency query string false "Filter by currency"
// @Param account_type query string false "Filter by account type"
// @Param is_active query bool false "Filter by active status"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /finance/bank-accounts/summary [get]
//
// Five finished numbers, so neither client adds anything up itself. Both were:
// the web reduced over the loaded array and the mobile app ran a for loop —
// and the moment the list starts paginating (ListBankAccounts already supports
// opt-in paging) a client-side reduce quietly becomes the first page's total.
// The mobile app did not even try for unreconciled; it emitted a literal 0.
//
// Takes the same filters as the list and must: the cards sit above the rows, so
// a summary that ignored `search` would report totals over a different set of
// accounts than the table underneath it shows.
func (h *Handler) GetBankAccountsSummary(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		response.BadRequest(c, "tenant_id is required")
		return
	}

	var filter entity.BankAccountListFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	where, args, _ := bankAccountScope(c, tenantID, filter)

	var accountCount, activeAccounts int
	var uzsBalance, usdBalance float64
	err := h.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE COALESCE(ba.is_active, true)),
		       COALESCE(SUM(COALESCE(lb.bal, 0)) FILTER (WHERE COALESCE(ba.currency, 'UZS') = 'UZS'), 0),
		       COALESCE(SUM(COALESCE(lb.bal, 0)) FILTER (WHERE COALESCE(ba.currency, 'UZS') = 'USD'), 0)
		FROM bank_accounts ba`+bankLedgerBalanceJoin+`
		WHERE `+where, args...).
		Scan(&accountCount, &activeAccounts, &uzsBalance, &usdBalance)
	if err != nil {
		h.log.Error("Failed to summarise bank accounts", "error", err)
		response.InternalError(c, "Failed to summarise bank accounts")
		return
	}

	// Scoped through the account, not through the transaction's own tenant
	// column alone — same set of accounts the balances above were summed over.
	var unreconciledCount int
	err = h.db.QueryRow(`
		SELECT COUNT(*)
		FROM bank_transactions t
		JOIN bank_accounts ba ON ba.id = t.bank_account_id
		WHERE t.tenant_id = $1 AND `+bankTxUnreconciled+`
		  AND `+where, args...).Scan(&unreconciledCount)
	if err != nil {
		h.log.Error("Failed to count unreconciled bank transactions", "error", err)
		response.InternalError(c, "Failed to summarise bank accounts")
		return
	}

	response.Success(c, gin.H{
		"account_count":      accountCount,
		"active_accounts":    activeAccounts,
		"uzs_balance":        uzsBalance,
		"usd_balance":        usdBalance,
		"unreconciled_count": unreconciledCount,
	})
}
