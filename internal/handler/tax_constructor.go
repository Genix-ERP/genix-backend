package handler

// Tax constructor + catalog (genix_soliq_spec §2–3).
//
// - tax_types: the national catalog (vat, profit, turnover, pit, inps, social,
//   property, land, water, dividend, union) with category mandatory|regime|optional.
// - tax_type_rates: date-versioned national rates. Rates are NEVER hardcoded in
//   Go — taxRateOnDate() reads the rate in force on the document's date.
// - tenant_taxes: per-tenant on/off toggles with an effective date.
// - tenant_tax_regime: turnover | vat_profit, with history + a 1-billion monitor.
//
// Toggle rules (§2): mandatory taxes can't be turned off while the tenant has
// active employees; regime taxes (vat/profit/turnover) are driven by the regime
// document, not the manual toggle; optional taxes toggle freely with a date.

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

const turnoverThreshold = 1_000_000_000.0 // 1 mlrd so'm — mandatory VAT switch (§2)

// ---- shared helpers (used by calculation code so no rate is hardcoded) ------

// taxRateOnDate returns the rate (%) in force for a tax on a date, honoring the
// tenant's chosen rate_variant. Returns TAX_RATE_NOT_FOUND when none applies.
func (h *Handler) taxRateOnDate(tenantID uuid.UUID, code string, on time.Time) (float64, string, error) {
	variant := h.tenantRateVariant(tenantID, code, on)
	var rate float64
	err := h.db.QueryRow(`
		SELECT rate FROM tax_type_rates
		WHERE tax_code=$1 AND rate_variant=$2 AND valid_from <= $3 AND (valid_to IS NULL OR valid_to >= $3)
		ORDER BY valid_from DESC LIMIT 1`, code, variant, on).Scan(&rate)
	if err == sql.ErrNoRows {
		return 0, variant, fmt.Errorf("TAX_RATE_NOT_FOUND: no %s rate (%s) in force on %s", code, variant, on.Format("2006-01-02"))
	}
	if err != nil {
		return 0, variant, err
	}
	return rate, variant, nil
}

// tenantRateVariant returns the tenant's chosen rate_variant for a tax (default standard).
func (h *Handler) tenantRateVariant(tenantID uuid.UUID, code string, on time.Time) string {
	var v string
	err := h.db.QueryRow(`
		SELECT rate_variant FROM tenant_taxes
		WHERE tenant_id=$1 AND tax_code=$2 AND valid_from <= $3 AND (valid_to IS NULL OR valid_to >= $3)
		ORDER BY valid_from DESC LIMIT 1`, tenantID, code, on).Scan(&v)
	if err != nil || v == "" {
		return "standard"
	}
	return v
}

// tenantRegime returns the tenant's tax regime in force on a date (default vat_profit).
func (h *Handler) tenantRegime(tenantID uuid.UUID, on time.Time) string {
	var r string
	err := h.db.QueryRow(`
		SELECT regime FROM tenant_tax_regime
		WHERE tenant_id=$1 AND valid_from <= $2 ORDER BY valid_from DESC LIMIT 1`, tenantID, on).Scan(&r)
	if err != nil || r == "" {
		return "vat_profit"
	}
	return r
}

// taxEnabled reports whether a tax is active for the tenant on a date, applying
// the explicit toggle if any, otherwise the category default.
func (h *Handler) taxEnabled(tenantID uuid.UUID, code string, on time.Time) bool {
	var enabled bool
	err := h.db.QueryRow(`
		SELECT enabled FROM tenant_taxes
		WHERE tenant_id=$1 AND tax_code=$2 AND valid_from <= $3 AND (valid_to IS NULL OR valid_to >= $3)
		ORDER BY valid_from DESC LIMIT 1`, tenantID, code, on).Scan(&enabled)
	if err == nil {
		return enabled
	}
	// No explicit toggle → category default.
	var category string
	var mandatory bool
	if h.db.QueryRow(`SELECT category, mandatory FROM tax_types WHERE code=$1`, code).Scan(&category, &mandatory) != nil {
		return false
	}
	switch category {
	case "mandatory":
		return true
	case "regime":
		regime := h.tenantRegime(tenantID, on)
		if regime == "vat_profit" {
			return code == "vat" || code == "profit"
		}
		return code == "turnover"
	default:
		return false
	}
}

func (h *Handler) tenantHasActiveEmployees(tenantID uuid.UUID) bool {
	var exists bool
	_ = h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM employees WHERE tenant_id=$1 AND COALESCE(status,'active')='active')`, tenantID).Scan(&exists)
	return exists
}

// ---- HTTP handlers ---------------------------------------------------------

// GetTaxTypes returns the national catalog. GET /taxes/types
func (h *Handler) GetTaxTypes(c *gin.Context) {
	rows, err := h.db.Query(`SELECT code, name_uz, name_ru, kind, category, mandatory,
		COALESCE(liability_account,''), COALESCE(analytic_code,''), sort_order
		FROM tax_types ORDER BY sort_order`)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var code, nu, nr, kind, cat, la, ac string
		var mand bool
		var ord int
		if rows.Scan(&code, &nu, &nr, &kind, &cat, &mand, &la, &ac, &ord) == nil {
			out = append(out, gin.H{"code": code, "name_uz": nu, "name_ru": nr, "kind": kind,
				"category": cat, "mandatory": mand, "liability_account": la, "analytic_code": ac})
		}
	}
	response.Success(c, out)
}

// GetTaxRates returns the versioned rates in force on a date. GET /taxes/rates?on_date=
func (h *Handler) GetTaxRates(c *gin.Context) {
	on := parseDateOr(c.Query("on_date"), time.Now())
	rows, err := h.db.Query(`
		SELECT DISTINCT ON (tax_code, rate_variant) tax_code, rate_variant, rate, valid_from, valid_to
		FROM tax_type_rates
		WHERE valid_from <= $1 AND (valid_to IS NULL OR valid_to >= $1)
		ORDER BY tax_code, rate_variant, valid_from DESC`, on)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var code, variant string
		var rate float64
		var vf time.Time
		var vt sql.NullTime
		if rows.Scan(&code, &variant, &rate, &vf, &vt) == nil {
			row := gin.H{"tax_code": code, "rate_variant": variant, "rate": rate, "valid_from": vf.Format("2006-01-02")}
			if vt.Valid {
				row["valid_to"] = vt.Time.Format("2006-01-02")
			}
			out = append(out, row)
		}
	}
	response.Success(c, gin.H{"on_date": on.Format("2006-01-02"), "rates": out})
}

// GetTaxSettings returns every tax with its effective on/off state, current rate
// and whether it can be toggled. GET /taxes/settings
func (h *Handler) GetTaxSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	on := time.Now()
	regime := h.tenantRegime(tenantID, on)
	hasEmp := h.tenantHasActiveEmployees(tenantID)

	rows, err := h.db.Query(`SELECT code, name_uz, name_ru, kind, category, mandatory FROM tax_types ORDER BY sort_order`)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var code, nu, nr, kind, cat string
		var mand bool
		if rows.Scan(&code, &nu, &nr, &kind, &cat, &mand) != nil {
			continue
		}
		variant := h.tenantRateVariant(tenantID, code, on)
		rate, _, _ := h.taxRateOnDate(tenantID, code, on)
		canToggle, lock := true, ""
		switch cat {
		case "regime":
			canToggle, lock = false, "regime" // managed by the regime document
		case "mandatory":
			if mand && hasEmp {
				canToggle, lock = false, "mandatory" // can't disable with active employees
			}
		}
		out = append(out, gin.H{
			"code": code, "name_uz": nu, "name_ru": nr, "kind": kind, "category": cat,
			"mandatory": mand, "enabled": h.taxEnabled(tenantID, code, on),
			"rate": rate, "rate_variant": variant, "can_toggle": canToggle, "lock_reason": lock,
		})
	}
	response.Success(c, gin.H{"regime": regime, "taxes": out})
}

// UpdateTaxSetting enables/disables an optional tax from a date. PUT /taxes/settings
// Body: { tax_code, enabled, valid_from?, rate_variant? }
func (h *Handler) UpdateTaxSetting(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var in struct {
		TaxCode     string `json:"tax_code"`
		Enabled     bool   `json:"enabled"`
		ValidFrom   string `json:"valid_from"`
		RateVariant string `json:"rate_variant"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.TaxCode == "" {
		response.BadRequest(c, "tax_code and enabled are required")
		return
	}

	var category string
	var mandatory bool
	if err := h.db.QueryRow(`SELECT category, mandatory FROM tax_types WHERE code=$1`, in.TaxCode).Scan(&category, &mandatory); err != nil {
		response.BadRequest(c, "unknown tax_code")
		return
	}

	// §2 rules.
	if category == "regime" {
		c.JSON(409, gin.H{"code": "REGIME_TAX_LOCKED",
			"message_ru": "Режимные налоги (НДС/оборот/прибыль) переключаются только документом смены режима",
			"message_uz": "Rejim soliqlari (QQS/aylanma/foyda) faqat rejim o'zgartirish hujjati orqali boshqariladi"})
		return
	}
	if mandatory && !in.Enabled && h.tenantHasActiveEmployees(tenantID) {
		c.JSON(409, gin.H{"code": "MANDATORY_TAX",
			"message_ru": "Обязательный налог нельзя выключить при активных сотрудниках",
			"message_uz": "Majburiy soliqni faol xodimlar mavjud bo'lganda o'chirib bo'lmaydi"})
		return
	}

	validFrom := parseDateOr(in.ValidFrom, time.Now())
	variant := in.RateVariant
	if variant == "" {
		variant = h.tenantRateVariant(tenantID, in.TaxCode, validFrom)
	}

	if _, err := h.db.Exec(`
		INSERT INTO tenant_taxes (tenant_id, tax_code, enabled, valid_from, rate_variant, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (tenant_id, tax_code, valid_from) DO UPDATE SET
			enabled=EXCLUDED.enabled, rate_variant=EXCLUDED.rate_variant, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		tenantID, in.TaxCode, in.Enabled, validFrom, variant, userID); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	// Best-effort audit trail (§2: each toggle → audit log).
	if _, execErr := h.db.Exec(`INSERT INTO audit_logs (id, tenant_id, user_id, action, entity_type, new_values, created_at)
		VALUES ($1,$2,$3,$4,'tax_setting',$5::jsonb,now())`,
		uuid.New(), tenantID, userID,
		map[bool]string{true: "tax_enabled", false: "tax_disabled"}[in.Enabled],
		fmt.Sprintf(`{"tax_code":%q,"enabled":%v,"valid_from":%q,"rate_variant":%q}`, in.TaxCode, in.Enabled, validFrom.Format("2006-01-02"), variant)); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "INSERT audit_logs", "error", execErr)
	}

	response.Success(c, gin.H{"tax_code": in.TaxCode, "enabled": in.Enabled, "valid_from": validFrom.Format("2006-01-02"), "rate_variant": variant})
}

// GetTaxRegime returns the tenant regime plus the 1-billion threshold monitor. GET /taxes/regime
func (h *Handler) GetTaxRegime(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	now := time.Now()
	regime := h.tenantRegime(tenantID, now)

	// Year-to-date income (proxy: posted/any sales invoices this calendar year).
	var incomeYTD float64
	_ = h.db.QueryRow(`SELECT COALESCE(SUM(total_amount),0) FROM sales_invoices
		WHERE tenant_id=$1 AND deleted_at IS NULL AND invoice_date >= date_trunc('year', CURRENT_DATE)`, tenantID).Scan(&incomeYTD)

	percent := 0.0
	if turnoverThreshold > 0 {
		percent = incomeYTD / turnoverThreshold * 100
	}
	alert := "none"
	switch {
	case percent >= 100:
		alert = "exceeded"
	case percent >= 95:
		alert = "red"
	case percent >= 80:
		alert = "yellow"
	}
	response.Success(c, gin.H{
		"regime": regime, "income_ytd": incomeYTD, "threshold": turnoverThreshold,
		"percent": percent, "alert_level": alert,
	})
}

func parseDateOr(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return def
}
