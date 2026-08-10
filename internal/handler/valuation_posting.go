package handler

import (
	"errors"
	"fmt"
)

// Zaxiralarni baholash, bosqich 2: the journal lines a stock movement produces.
//
// §4 of the plan is a table of operations against Dt/Kt pairs. This turns that
// table into one function, so the mapping lives in a single place instead of
// being re-derived at each of the dozen call sites that post stock movements —
// which is how goods-delivery ended up crediting inventory without debiting
// COGS (migration 416's header records that one; it produced 50+ one-sided
// entries before a database trigger stopped it).
//
// Like valuation_engine.go this is pure: it takes resolved account IDs and an
// amount in tiyin and returns lines. Resolving the accounts is the caller's job
// (getCategoryAccounts already does it, including the category override and the
// leaf fallback), and so is writing them.

// StockOperation is a row of the plan's §4 table.
type StockOperation string

const (
	// Kirim — goods arriving from a supplier. Dt stock, Kt payables.
	OpSupplierReceipt StockOperation = "supplier_receipt"
	// Sotuv — writing the cost off on sale. Dt COGS, Kt stock.
	OpSaleIssue StockOperation = "sale_issue"
	// Xaridordan qaytarish — a customer return. Dt stock, Kt COGS (a storno
	// of the original cost, which is why it is priced at the ORIGINAL issue
	// cost and not at today's).
	OpCustomerReturn StockOperation = "customer_return"
	// Yetkazib beruvchiga qaytarish. Dt payables, Kt stock.
	OpSupplierReturn StockOperation = "supplier_return"
	// Spisaniye / brak — a write-off. Dt expense, Kt stock.
	OpScrap StockOperation = "scrap"
	// Inventarizatsiya ortiqcha — a surplus found on a stock count.
	OpCountSurplus StockOperation = "count_surplus"
	// Inventarizatsiya kamomad — a shortage found on a stock count.
	OpCountShortage StockOperation = "count_shortage"
	// Standard narxni qayta baholash — revaluing held stock to a new standard.
	OpStandardRevaluation StockOperation = "standard_revaluation"
)

// ValuationAccounts is the resolved mapping for one product.
//
// Stock is whichever account the product's type and category route to — 1010
// for raw materials, 2810 for finished goods, 2910 for goods for resale. The
// plan writes 2910 throughout its §4 table and then says the final mapping has
// to be agreed with the client's accountant because raw materials and finished
// goods post elsewhere; getInventoryAccountByType already makes that
// distinction, so nothing here hardcodes a code.
type ValuationAccounts struct {
	Stock    string // 2910 / 2810 / 1010 — the valuation account
	COGS     string // 9120 Sotilgan tovarlar tannarxi
	Payables string // 6010 Yetkazib beruvchilar
	Expense  string // 9430 or a write-off sub-account
	Surplus  string // 9390 Boshqa operatsion daromadlar
	Shortage string // 5910 Kamomad va yo'qotishlar
	Variance string // Chetlanishlar — standard costing only
}

// JournalLine is one side of a posting, in tiyin.
type JournalLine struct {
	AccountID string
	Debit     int64
	Credit    int64
	Memo      string
}

var (
	ErrNoAccount     = errors.New("valuation posting: required account is not configured")
	ErrZeroAmount    = errors.New("valuation posting: amount must be non-zero")
	ErrUnknownOp     = errors.New("valuation posting: unknown stock operation")
	ErrUnbalancedGen = errors.New("valuation posting: generated lines do not balance")
)

// BuildStockLines returns the balanced journal lines for a stock movement.
//
// `amount` is the movement's VALUE in tiyin as the cost engine computed it —
// never quantity × a unit price re-derived here. The two must be the same
// number or the ledger and the layers drift apart, which is the §1.3 invariant
// this whole design exists to keep.
//
// The result is checked for balance before returning. A caller that ignores an
// error and posts anyway is the exact failure migration 416 had to install a
// database trigger to catch; returning an unbalanced set from here would put
// that back.
func BuildStockLines(op StockOperation, acc ValuationAccounts, amount int64, memo string) ([]JournalLine, error) {
	if amount == 0 {
		return nil, ErrZeroAmount
	}

	// A negative movement is the same operation in the other direction — a
	// credit note that reduces a return, a reversal. Swapping the sides and
	// working with the magnitude keeps every branch below single-signed, which
	// is what makes them readable.
	if amount < 0 {
		lines, err := BuildStockLines(op, acc, -amount, memo)
		if err != nil {
			return nil, err
		}
		for i := range lines {
			lines[i].Debit, lines[i].Credit = lines[i].Credit, lines[i].Debit
		}
		return lines, nil
	}

	var debit, credit string
	switch op {
	case OpSupplierReceipt:
		debit, credit = acc.Stock, acc.Payables
	case OpSaleIssue:
		debit, credit = acc.COGS, acc.Stock
	case OpCustomerReturn:
		// Stock comes back and the cost of sale is reversed.
		debit, credit = acc.Stock, acc.COGS
	case OpSupplierReturn:
		debit, credit = acc.Payables, acc.Stock
	case OpScrap:
		debit, credit = acc.Expense, acc.Stock
	case OpCountSurplus:
		debit, credit = acc.Stock, acc.Surplus
	case OpCountShortage:
		debit, credit = acc.Shortage, acc.Stock
	case OpStandardRevaluation:
		// A positive delta means stock is now worth more.
		debit, credit = acc.Stock, acc.Variance
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownOp, op)
	}

	if debit == "" || credit == "" {
		return nil, fmt.Errorf("%w: %s needs both sides configured", ErrNoAccount, op)
	}

	lines := []JournalLine{
		{AccountID: debit, Debit: amount, Memo: memo},
		{AccountID: credit, Credit: amount, Memo: memo},
	}
	return lines, assertBalanced(lines)
}

// BuildStandardReceiptLines is the one operation that is not a simple pair.
//
// Stock enters at the standard price and the difference against what is
// actually owed goes to the variance account, so the entry has three sides
// (§3.3):
//
//	Dt Stock       layerValue   (qty × standard)
//	Dt Variance    variance     when the actual price was higher
//	Kt Payables    layerValue + variance   (what is actually owed)
//
// A negative variance — bought below standard — moves that line to the credit
// side instead. Both cases must close against the payable to the tiyin, which
// is why StandardReceiptVariance computes the variance as the difference of the
// two ROUNDED sums rather than the rounding of the difference.
func BuildStandardReceiptLines(acc ValuationAccounts, layerValue, variance int64, memo string) ([]JournalLine, error) {
	if layerValue == 0 && variance == 0 {
		return nil, ErrZeroAmount
	}
	if acc.Stock == "" || acc.Payables == "" {
		return nil, fmt.Errorf("%w: standard receipt needs stock and payables", ErrNoAccount)
	}
	if variance != 0 && acc.Variance == "" {
		// Failing here rather than folding the variance into stock is
		// deliberate: silently absorbing it would value the stock at actual
		// cost while the method claims standard, and the difference would then
		// be invisible in every report that exists to show it.
		return nil, fmt.Errorf("%w: a standard-cost receipt at a price other than standard needs a variance account", ErrNoAccount)
	}

	payable := layerValue + variance
	lines := []JournalLine{{AccountID: acc.Stock, Debit: layerValue, Memo: memo}}
	if variance > 0 {
		lines = append(lines, JournalLine{AccountID: acc.Variance, Debit: variance, Memo: memo})
	} else if variance < 0 {
		lines = append(lines, JournalLine{AccountID: acc.Variance, Credit: -variance, Memo: memo})
	}
	lines = append(lines, JournalLine{AccountID: acc.Payables, Credit: payable, Memo: memo})

	return lines, assertBalanced(lines)
}

// assertBalanced is the §1.3 invariant at the posting level. Nothing leaves
// this file unbalanced.
func assertBalanced(lines []JournalLine) error {
	var dr, cr int64
	for _, l := range lines {
		dr += l.Debit
		cr += l.Credit
	}
	if dr != cr {
		return fmt.Errorf("%w: Dt %d <> Kt %d", ErrUnbalancedGen, dr, cr)
	}
	return nil
}

// StockOperationsNeedingAccounts reports which account fields an operation
// consults, so a settings screen can tell the user what is still unconfigured
// BEFORE they post a document and get a failure they cannot act on.
func StockOperationsNeedingAccounts(op StockOperation) []string {
	switch op {
	case OpSupplierReceipt:
		return []string{"Stock", "Payables"}
	case OpSaleIssue, OpCustomerReturn:
		return []string{"Stock", "COGS"}
	case OpSupplierReturn:
		return []string{"Stock", "Payables"}
	case OpScrap:
		return []string{"Stock", "Expense"}
	case OpCountSurplus:
		return []string{"Stock", "Surplus"}
	case OpCountShortage:
		return []string{"Stock", "Shortage"}
	case OpStandardRevaluation:
		return []string{"Stock", "Variance"}
	}
	return nil
}
