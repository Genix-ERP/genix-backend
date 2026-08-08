package handler

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The general ledger is kept in the tenant's base currency. Every amount that
// reaches journal_entry_lines or accounts.current_balance must therefore be the
// CONVERTED figure, never the raw transaction-currency amount.
//
// Both payment write paths violated this: they posted `amount` verbatim and
// stored exchange_rate as a decorative column, so a 1,000 USD payment at 12,115
// debited the cash account 1,000. It went unnoticed because every
// same-currency payment has rate 1, which makes the bug invisible in exactly
// the case everyone tests.
//
// This is a source-level check rather than a behavioural one because the defect
// is a missing multiplication, not a wrong result — with rate 1 the correct and
// incorrect code produce identical output, so no same-currency fixture can tell
// them apart. Asserting on the source is what makes the regression detectable.
func TestPaymentGLPostingsUseConvertedAmount(t *testing.T) {
	for _, tc := range []struct {
		file     string
		fn       string
		baseVar  string
		rawVars  []string
		endMarks []string
	}{
		{
			file:    "finance.go",
			fn:      "func (h *Handler) ConfirmPayment(",
			baseVar: "baseAmount",
			rawVars: []string{"amount"},
		},
		{
			file:    "payments_reconcile.go",
			fn:      "func (h *Handler) RegisterPartnerPayment(",
			baseVar: "baseAmount",
			rawVars: []string{"input.Amount"},
		},
	} {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		body := functionBody(string(src), tc.fn)
		if body == "" {
			t.Fatalf("%s: could not locate %s", tc.file, tc.fn)
		}

		if !strings.Contains(body, tc.baseVar+" :=") {
			t.Errorf("%s/%s: no %s — the GL conversion has been removed", tc.file, tc.fn, tc.baseVar)
			continue
		}

		// Every current_balance arithmetic update in a payment path moves the
		// ledger, so it must carry the converted figure.
		for _, m := range regexp.MustCompile(`current_balance = current_balance [+-] \$1[^)]*\)`).FindAllString(body, -1) {
			for _, raw := range tc.rawVars {
				if regexp.MustCompile(`,\s*` + regexp.QuoteMeta(raw) + `\s*,`).MatchString(m) {
					t.Errorf("%s/%s: account balance updated with raw %s, not %s:\n  %s",
						tc.file, tc.fn, raw, tc.baseVar, strings.TrimSpace(m))
				}
			}
		}
	}
}

// functionBody returns the text of the function beginning with sig, up to the
// next top-level func declaration.
func functionBody(src, sig string) string {
	i := strings.Index(src, sig)
	if i < 0 {
		return ""
	}
	rest := src[i+len(sig):]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		return rest[:j]
	}
	return rest
}
