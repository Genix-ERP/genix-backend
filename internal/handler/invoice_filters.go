package handler

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/genixerp/genix-backend/internal/middleware"
)

// Filters shared by an invoice list, its COUNT and its /stats endpoint.
//
// /purchase-invoices/stats read only tenant_id + organization_id, so the summary
// cards at the top of the screen described the whole tenant while the rows
// underneath were filtered — the two could never agree. Building the predicate
// once means they cannot drift again, which a "remember to update both" comment
// would not have achieved.
//
// The alias is a parameter because the list SELECT is aliased (pi./si.) while
// the old COUNT and stats queries used bare column names. Both now take the
// same FROM and the same alias, so the strings stay textually parallel.

// invoiceStatusFilter renders the status predicate.
//
// status accepts a COMMA-SEPARATED list and matches with = ANY(). A single
// exact value could not express what the UI calls "Tasdiqlangan": a posted
// invoice is also confirmed, so that chip needs confirmed AND posted, and one
// `status = $n` cannot say it. Existing callers sending a single value are
// unaffected — a one-element array behaves identically.
func invoiceStatusFilter(raw, alias string, args *[]interface{}) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	*args = append(*args, pq.Array(parts))
	return fmt.Sprintf(" AND %s.status = ANY($%d)", alias, len(*args))
}

// invoiceOverdueSQL is the ONE definition of "overdue", used for both the
// `overdue=true` filter and the per-row is_overdue field.
//
// It used to be computed twice from different clocks: the filter used Postgres
// CURRENT_DATE while the row field used Go's time.Now().Truncate(24*time.Hour),
// which is midnight UTC because Truncate measures from the zero instant. Those
// agree only while the database session is also UTC. Point PGTZ at
// Asia/Tashkent and for the first five hours of every local day the filter and
// the flag disagree about the same invoice — the list would return a row
// flagged is_overdue:false. Deriving both from this expression removes the
// second clock entirely.
// D3: an invoice is overdue only when money is actually still owed. The
// predicate had no amount test, so a settled or zero-total invoice past its due
// date counted as overdue while being excluded from open_invoices (which
// requires a residual) — that is the "Muddati o'tgan 18 / Ochiq 17" mismatch on
// the AR cards, and it made overdue_invoices not a subset of open_invoices.
//
// The test is on the RESIDUAL, not on total_amount > 0. total_amount > 0 would
// still count an invoice paid in full whose status was never moved off 'sent' —
// a real data state, since status and amount_paid are written by different
// paths. Residual covers both that and the zero-total case.
//
// B1: the status list is debtStatusFilter, not a locally written NOT IN
// ('paid', 'cancelled'). That local list included 'draft' — a status both
// invoice tables DEFAULT to — so an unconfirmed bill past its due date counted
// as overdue here while /reports/finance-dashboard, which uses
// debtStatusFilter, left it out of "Siz qarzdorsiz" entirely. One invoice, two
// screens, opposite answers.
//
// Dropping 'paid' from the exclusion costs nothing: a settled row's residual is
// already <= 0 and fails the test below. What it does buy is the row whose
// amount_paid never reached total_amount while its status was moved to 'paid'
// anyway — money genuinely still owed, which the old list hid. Same reasoning
// as the residual note above: the amounts are the truth, the status column is
// the vocabulary.
func invoiceOverdueSQL(alias string) string {
	return fmt.Sprintf(
		"(%[1]s.due_date IS NOT NULL AND %[1]s.due_date < CURRENT_DATE"+
			" AND %[2]s"+
			" AND (%[1]s.total_amount - COALESCE(%[1]s.amount_paid, 0)) > 0)",
		alias, debtStatusFilterFor(alias))
}

// purchaseInvoiceWhere builds the predicate shared by ListPurchaseInvoices, its
// COUNT and GetPurchaseInvoiceStats. Callers must bind tenantID as $1 and use
// `pi` plus a `contacts c` join, so the vendor-name search resolves.
func purchaseInvoiceWhere(c *gin.Context, args *[]interface{}) string {
	where := " AND 1=1"

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		*args = append(*args, orgID)
		where += fmt.Sprintf(" AND pi.organization_id = $%d", len(*args))
	}
	where += invoiceStatusFilter(c.Query("status"), "pi", args)
	// The AP screen has a payment-status chip and, like the sales list before it,
	// had no parameter to express it — so it filtered whatever page happened to
	// be loaded. With page_size defaulting to 20, picking "To'langan" searched
	// twenty bills instead of the tenant's. Same expression as the row field.
	where += invoicePaymentStatusFilter(c.Query("payment_status"), "pi", args)

	if v := strings.TrimSpace(c.Query("vendor_id")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND pi.vendor_id = $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("date_from")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND pi.invoice_date >= $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("date_to")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND pi.invoice_date <= $%d", len(*args))
	}
	if c.Query("overdue") == "true" {
		where += " AND " + invoiceOverdueSQL("pi")
	}
	where += invoiceDebtFilter(c, "pi")
	if v := strings.TrimSpace(c.Query("invoice_type")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND COALESCE(pi.invoice_type, 'invoice') = $%d", len(*args))
	}
	// The card shows the vendor's name, so a user typing "Tesla" expects to find
	// it. Searching only the two invoice-number columns returned nothing and
	// looked like missing data.
	if v := strings.TrimSpace(c.Query("search")); v != "" {
		*args = append(*args, "%"+strings.ToLower(v)+"%")
		n := len(*args)
		where += fmt.Sprintf(
			" AND (LOWER(pi.invoice_number) LIKE $%d OR LOWER(COALESCE(pi.vendor_invoice_number,'')) LIKE $%d OR LOWER(COALESCE(c.name,'')) LIKE $%d)",
			n, n, n)
	}
	return where
}

// invoiceDebtFilter renders the optional `debt=true` scope: only the documents
// the debt cards count.
//
// It exists because sharing invoicePaymentStatusSQL between the cards and the
// payment_status= filter — B3 — does not on its own make them return the same
// rows. The cards apply a SECOND predicate, debtStatusFilter, which the filter
// had no way to express: "To'lanmagan: 1" opened a list of 4, the extra three
// being a draft, a cancellation and a void that the card is right to leave out.
// Widening the payment-status filter to match was not an option — a user asking
// for paid invoices wants the cancelled ones that were paid too, and that filter
// is also what the AR/AP screens' own chips use.
//
// So the card links with `payment_status=unpaid&debt=true` and the rule stays on
// this side. Same shape as `overdue=true` directly above, and for the same
// reason: a card-shaped predicate belongs in the query string, not re-typed as a
// status list in the browser, where it would drift the moment the vocabulary
// changes (migration 478 already retired 'pending_approval' and 'approved' out
// from under one screen that had hard-coded them).
//
// Absent or anything but "true", this adds nothing — every existing caller keeps
// its current result set.
func invoiceDebtFilter(c *gin.Context, alias string) string {
	if c.Query("debt") != "true" {
		return ""
	}
	return " AND " + debtStatusFilterFor(alias)
}

// invoicePaymentStatusSQL is the ONE definition of payment_status, used both for
// the row field and for the payment_status= filter.
//
// ListSalesInvoices computed this in Go and offered no way to filter on it, so
// both clients filtered the loaded page instead — on mobile, with page_size 10,
// "Qisman" searched ten invoices rather than the tenant's. Deriving the filter
// from a second copy of the rule would recreate exactly the two-clocks bug that
// invoiceOverdueSQL exists to prevent, so the predicate and the field are the
// same expression.
func invoicePaymentStatusSQL(alias string) string {
	return fmt.Sprintf(`CASE
		WHEN COALESCE(%s.amount_paid, 0) >= %s.total_amount AND %s.total_amount > 0 THEN 'paid'
		WHEN COALESCE(%s.amount_paid, 0) > 0 THEN 'partial'
		ELSE 'unpaid' END`, alias, alias, alias, alias)
}

// invoicePaymentStatusFilter matches a comma-separated payment_status list
// against the expression above.
func invoicePaymentStatusFilter(raw, alias string, args *[]interface{}) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, strings.ToLower(p))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	*args = append(*args, pq.Array(parts))
	return fmt.Sprintf(" AND (%s) = ANY($%d)", invoicePaymentStatusSQL(alias), len(*args))
}

// invoiceDaysOverdueSQL is positive when an invoice is past due and NEGATIVE
// when it is not yet due — the mobile card reads the sign to choose between
// "N kun oldin" and "N kun qoldi". Same expression the aging reports use
// (reports.go), against CURRENT_DATE rather than an as-of parameter.
func invoiceDaysOverdueSQL(alias string) string {
	return fmt.Sprintf("COALESCE((CURRENT_DATE - %s.due_date)::int, 0)", alias)
}

// salesInvoiceWhere is the sales twin of purchaseInvoiceWhere: one predicate for
// the list, its COUNT and the AR summary, so the cards and the rows underneath
// them always describe the same set.
// Callers bind tenantID as $1 and must have `si` plus a `contacts c` join.
func salesInvoiceWhere(c *gin.Context, args *[]interface{}) string {
	where := ""

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		*args = append(*args, orgID)
		where += fmt.Sprintf(" AND si.organization_id = $%d", len(*args))
	}
	where += invoiceStatusFilter(c.Query("status"), "si", args)
	where += invoicePaymentStatusFilter(c.Query("payment_status"), "si", args)

	if v := strings.TrimSpace(c.Query("customer_id")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND si.customer_id = $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("date_from")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND si.invoice_date >= $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("date_to")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND si.invoice_date <= $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("due_from")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND si.due_date >= $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("due_to")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND si.due_date <= $%d", len(*args))
	}
	if c.Query("overdue") == "true" {
		where += " AND " + invoiceOverdueSQL("si")
	}
	where += invoiceDebtFilter(c, "si")
	if v := strings.TrimSpace(c.Query("invoice_type")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND COALESCE(si.invoice_type, 'invoice') = $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("search")); v != "" {
		*args = append(*args, "%"+strings.ToLower(v)+"%")
		n := len(*args)
		where += fmt.Sprintf(
			" AND (LOWER(si.invoice_number) LIKE $%d OR LOWER(COALESCE(c.name,'')) LIKE $%d)", n, n)
	}
	return where
}

// Date-derived fields for the non-invoice lists.
//
// discounts, contracts and vendor_prices each had their expiry rule written a
// third time in Dart against DateTime.now(). These emit it from SQL so the
// clients render a field instead — the same reason invoiceOverdueSQL exists,
// and the same CURRENT_DATE clock, so a tenant cannot see one list agree with
// the server and another disagree.

// dateExpiredSQL is true once `column` is strictly in the past. A NULL date
// means "no expiry", which is false rather than NULL — an open-ended discount
// is not expired, and leaving it NULL would make the client handle a third
// state that has no meaning here.
func dateExpiredSQL(column string) string {
	return fmt.Sprintf("(%s IS NOT NULL AND %s < CURRENT_DATE)", column, column)
}

// dateDaysRemainingSQL counts days until `column`: positive while it is still
// in the future, 0 on the day itself, negative once past. NULL when there is no
// date at all, which the clients must render as "no expiry" rather than 0 —
// zero would read as "expires today".
func dateDaysRemainingSQL(column string) string {
	return fmt.Sprintf("(%s - CURRENT_DATE)::int", column)
}
