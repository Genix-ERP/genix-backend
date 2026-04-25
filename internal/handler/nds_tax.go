package handler

import (
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// nds_tax.go — period calculator for НДС (QQS) per TZ §5.1 of
// ТЗ_Ish_Haqi_Soliq_Tolik.docx.
//
// Formula (§5.1):
//     НДС реализация (чиқим) = Σ sales_invoices.tax_amount     (output VAT)
//     НДС зачет      (кирим) = Σ purchase_invoices.tax_amount  (input VAT)
//     НДС балансы    (тўлов) = реализация − зачет              (net payable)
//
// TZ test §11.11 (50 млн савдо → 6 млн realiz; 10 млн харид → 1.2 млн
// zachet; to'lov = 4.8 млн) — this calculator reproduces that result
// verbatim because it sums the `tax_amount` the invoice line calculators
// already stamped onto each invoice at creation time.
//
// Rate: read from company_tax_rates applies_to='sales'; default 12%. The
// rate is NOT used to recompute amounts (historical invoices carry their
// own stamped tax), but it's returned in the response so the UI can show
// "at 12%" next to the figures.
//
// Route: GET /nds-tax?period_type=month|quarter|year&period_key=...

const defaultNdsRatePct = 12.0 // §5.1 of the TZ

// GetNdsTax returns the VAT realisation, input-VAT offset and net payable
// for a period.
func (h *Handler) GetNdsTax(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodType := c.DefaultQuery("period_type", "month")
	periodKey := c.Query("period_key")
	if periodKey == "" {
		periodKey = time.Now().UTC().Format("2006-01")
	}

	start, end, err := parseProfitTaxPeriod(periodType, periodKey)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Rate resolution: ?rate= → company_tax_rates(sales) → 12%.
	rate := h.getCompanyTaxRatePct(tenantID, "sales", defaultNdsRatePct)
	if r, err := strconv.ParseFloat(c.Query("rate"), 64); err == nil && r > 0 {
		rate = r
	}

	// Pre-flight regime check: if the tenant has Aylanma (turnover) tax
	// active alongside this NDS row, TZ §5.2 is violated. Save-time
	// enforcement already blocks future configs; legacy rows get flagged
	// so the UI can warn even for pre-existing state.
	regimeConflict := h.hasActiveCompanyTaxRate(tenantID, "turnover", nil)

	// Output VAT — sum of tax stamped onto non-draft, non-cancelled sales
	// invoices in the window. Mirrors the filter used by tax_reports.go so
	// the two reports never disagree for the same period.
	var salesSubtotal, salesTax float64
	var salesCount int64
	if err := h.db.QueryRow(`
		SELECT
		  COALESCE(SUM(si.subtotal),   0),
		  COALESCE(SUM(si.tax_amount), 0),
		  COUNT(*)
		FROM sales_invoices si
		WHERE si.tenant_id     = $1
		  AND si.invoice_date >= $2
		  AND si.invoice_date <= $3
		  AND si.status NOT IN ('draft', 'cancelled')
		  AND si.deleted_at IS NULL
	`, tenantID, start, end).Scan(&salesSubtotal, &salesTax, &salesCount); err != nil {
		h.log.Error("Failed to sum sales invoices for NDS", "error", err)
		response.InternalError(c, "Failed to compute NDS realisation")
		return
	}

	// Input VAT — same shape, from purchase invoices.
	var purchasesSubtotal, purchaseTax float64
	var purchasesCount int64
	if err := h.db.QueryRow(`
		SELECT
		  COALESCE(SUM(pi.subtotal),   0),
		  COALESCE(SUM(pi.tax_amount), 0),
		  COUNT(*)
		FROM purchase_invoices pi
		WHERE pi.tenant_id     = $1
		  AND pi.invoice_date >= $2
		  AND pi.invoice_date <= $3
		  AND pi.status NOT IN ('draft', 'cancelled')
		  AND pi.deleted_at IS NULL
	`, tenantID, start, end).Scan(&purchasesSubtotal, &purchaseTax, &purchasesCount); err != nil {
		h.log.Error("Failed to sum purchase invoices for NDS", "error", err)
		response.InternalError(c, "Failed to compute NDS zachet")
		return
	}

	balance := round2(salesTax - purchaseTax) // >0 = payable, <0 = kredit balans

	response.Success(c, gin.H{
		"period_type":  periodType,
		"period_key":   periodKey,
		"period_start": start.Format("2006-01-02"),
		"period_end":   end.Format("2006-01-02"),
		"rate":         rate,

		// TZ §5.1 field names, both in machine and TZ form so the UI can
		// label them however it wants.
		"realizatsiya": round2(salesTax),     // output VAT
		"zachet":       round2(purchaseTax),  // input VAT
		"balansi":      balance,              // net payable
		"payable":      balance > 0,

		// Context for the UI.
		"sales_subtotal":    round2(salesSubtotal),
		"purchase_subtotal": round2(purchasesSubtotal),
		"sales_count":       salesCount,
		"purchases_count":   purchasesCount,

		// True = tenant has BOTH NDS and Turnover active → §5.2 violation.
		"regime_conflict": regimeConflict,
	})
}
