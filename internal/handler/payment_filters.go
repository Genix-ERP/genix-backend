package handler

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
)

// Filters shared by the payment list, its COUNT and /payments/summary.
//
// ListPayments built the same predicate twice — once appended to the SELECT and
// once to the COUNT — with the argument list threaded through both by hand.
// That is the shape that lets a filter reach the rows and not the count. Worse,
// /payments/summary had a THIRD copy that understood only type, status,
// contact_id and the date range: filter the list by payment method or search
// it by vendor name and the cards above went on describing a different set.
// One function now, the same reason invoice_filters.go exists.
//
// Callers bind tenantID as $1 and must use paymentsFromSQL, so the
// contact-name search and the method filter have their joins.

// paymentsFromSQL is the FROM the list, the COUNT and the summary all use, so
// the three stay textually parallel.
//
// contacts is a LEFT JOIN. It was an INNER JOIN, so a payment whose contact row
// had gone missing vanished from the list, from the count and from every total
// — silently reducing the money the screen reported. Same defect the aging
// reports carried (audit B4) and the same fix; contact_name is COALESCEd at the
// projection so the scan still gets a string.
const paymentsFromSQL = `
	FROM payments p
	LEFT JOIN contacts c ON p.contact_id = c.id
	LEFT JOIN journals j ON p.journal_id = j.id
	WHERE p.tenant_id = $1 AND p.deleted_at IS NULL`

func paymentsWhere(c *gin.Context, args *[]interface{}) string {
	where := ""

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		*args = append(*args, orgID)
		where += fmt.Sprintf(" AND p.organization_id = $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("type")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND p.type = $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND p.status = $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("contact_id")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND p.contact_id = $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("date_from")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND p.payment_date >= $%d", len(*args))
	}
	if v := strings.TrimSpace(c.Query("date_to")); v != "" {
		*args = append(*args, v)
		where += fmt.Sprintf(" AND p.payment_date <= $%d", len(*args))
	}
	// The method is the journal's type; there is no method column.
	switch strings.TrimSpace(c.Query("method")) {
	case "cash":
		where += " AND j.type = 'cash'"
	case "bank_transfer":
		where += " AND j.type = 'bank'"
	}
	if v := strings.TrimSpace(c.Query("search")); v != "" {
		*args = append(*args, "%"+v+"%")
		n := len(*args)
		where += fmt.Sprintf(
			" AND (p.reference ILIKE $%d OR p.payment_number ILIKE $%d OR COALESCE(c.name,'') ILIKE $%d)",
			n, n, n)
	}
	return where
}
