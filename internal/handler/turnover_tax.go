package handler

import (
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// turnover_tax.go — activity-level calculator for "Aylanma solig'i" (§5.2
// of ТЗ_Ish_Haqi_Soliq_Tolik.docx). Computes turnover tax due for a period
// using the rate configured in company_tax_rates (applies_to='turnover').
//
// TZ §5.2:
//   - Ставка: 4% default — доступно для non-VAT (simplified-regime) companies.
//   - Мутуал эксклюзив с NDS: enforced at rate-save time in
//     company_tax_rates.go via ensureVatTurnoverExclusive.
//
// Formula:
//   turnover   = sum(revenue credits − debits) over the period (same
//                source as profit-tax revenue, so the two numbers agree
//                when an accountant reconciles)
//   rate       = company_tax_rates.rate WHERE applies_to='turnover', active
//                (fallback 4% per TZ §5.2)
//   tax_amount = round2(turnover × rate / 100)
//
// Route: GET /turnover-tax?period_type=month|quarter|year&period_key=...

const defaultTurnoverTaxRatePct = 4.0 // §5.2 of the TZ

// GetTurnoverTax computes turnover tax for the requested period.
func (h *Handler) GetTurnoverTax(c *gin.Context) {
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

	// Rate resolution: explicit ?rate= override → company_tax_rates → 4%.
	rate := h.getCompanyTaxRatePct(tenantID, "turnover", defaultTurnoverTaxRatePct)
	if r, err := strconv.ParseFloat(c.Query("rate"), 64); err == nil && r > 0 {
		rate = r
	}

	// Warn-flag if the tenant also has an active NDS row — TZ §5.2 forbids
	// charging both in the same period. Save-time validation already blocks
	// this config, but a pre-existing tenant or a manual DB insert could
	// still end up here. We still compute the turnover number so the
	// accountant sees it; the UI surfaces the warning.
	hasActiveVAT := h.hasActiveCompanyTaxRate(tenantID, "sales", nil)

	// Turnover = same revenue source profit-tax uses, so two calculators
	// never disagree on "total income for Q3" when the accountant is
	// reconciling.
	var turnover float64
	var entryCount int64
	err = h.db.QueryRow(`
		SELECT
		  COALESCE(SUM(jel.credit_amount) - SUM(jel.debit_amount), 0) AS turnover,
		  COUNT(*)                                                    AS n
		FROM journal_entry_lines jel
		JOIN journal_entries je    ON je.id  = jel.journal_entry_id
		JOIN accounts a            ON a.id   = jel.account_id
		JOIN account_types at      ON at.id  = a.account_type_id
		WHERE je.tenant_id    = $1
		  AND at.category     = 'revenue'
		  AND je.status       = 'posted'
		  AND je.deleted_at   IS NULL
		  AND je.entry_date  >= $2::date
		  AND je.entry_date  <= $3::date
	`, tenantID, start.Format("2006-01-02"), end.Format("2006-01-02")).Scan(&turnover, &entryCount)
	if err != nil {
		h.log.Error("Failed to compute turnover tax revenue", "error", err)
		response.InternalError(c, "Failed to compute turnover tax")
		return
	}

	taxAmount := round2(turnover * rate / 100)

	response.Success(c, gin.H{
		"period_type":     periodType,
		"period_key":      periodKey,
		"period_start":    start.Format("2006-01-02"),
		"period_end":      end.Format("2006-01-02"),
		"turnover":        round2(turnover),
		"rate":            rate,
		"tax_amount":      taxAmount,
		"entry_count":     entryCount,
		"regime_conflict": hasActiveVAT, // true = tenant has NDS active too; §5.2 violated
	})
}
