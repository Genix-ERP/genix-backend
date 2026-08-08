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
func invoiceOverdueSQL(alias string) string {
	return fmt.Sprintf(
		"(%s.due_date IS NOT NULL AND %s.due_date < CURRENT_DATE AND %s.status NOT IN ('paid', 'cancelled'))",
		alias, alias, alias)
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
