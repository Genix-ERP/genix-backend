package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// company_tax_rates.go — CRUD for the activity-level tax catalog
// (NDS / Profit / Turnover / Dividend / …). See migration 340 for schema.
// Mirrors the shape of employee_taxes.go but without payer/base_type
// semantics — activity taxes are always company-paid and scoped by
// `applies_to` (sales | profit | turnover | dividend | other).

var validCompanyTaxAppliesTo = map[string]bool{
	"sales":    true,
	"profit":   true,
	"turnover": true,
	"dividend": true,
	"other":    true,
}

func normalizeCompanyTaxAppliesTo(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "other"
	}
	if !validCompanyTaxAppliesTo[v] {
		return "other"
	}
	return v
}

const selectCompanyTaxCols = `
	ctr.id, ctr.tenant_id, ctr.organization_id,
	ctr.code, ctr.name, COALESCE(ctr.description, ''),
	ctr.rate, ctr.applies_to,
	ctr.account_id,
	ctr.is_active, ctr.sort_order,
	ctr.created_by, ctr.created_at, ctr.updated_at,
	a.code AS account_code_en, a.name AS account_name_en
`

const fromCompanyTaxJoins = `
	FROM company_tax_rates ctr
	LEFT JOIN accounts a ON a.id = ctr.account_id
`

func scanCompanyTaxRate(row interface {
	Scan(dest ...interface{}) error
}) (*entity.CompanyTaxRate, error) {
	var ct entity.CompanyTaxRate
	var orgID, accountID, createdBy sql.NullString
	var accountCode, accountName sql.NullString

	if err := row.Scan(
		&ct.ID, &ct.TenantID, &orgID,
		&ct.Code, &ct.Name, &ct.Description,
		&ct.Rate, &ct.AppliesTo,
		&accountID,
		&ct.IsActive, &ct.SortOrder,
		&createdBy, &ct.CreatedAt, &ct.UpdatedAt,
		&accountCode, &accountName,
	); err != nil {
		return nil, err
	}

	if orgID.Valid {
		if u, err := uuid.Parse(orgID.String); err == nil {
			ct.OrganizationID = &u
		}
	}
	if accountID.Valid {
		if u, err := uuid.Parse(accountID.String); err == nil {
			ct.AccountID = &u
		}
	}
	if createdBy.Valid {
		if u, err := uuid.Parse(createdBy.String); err == nil {
			ct.CreatedBy = &u
		}
	}
	if accountCode.Valid {
		s := accountCode.String
		ct.AccountCode = &s
	}
	if accountName.Valid {
		s := accountName.String
		ct.AccountName = &s
	}
	return &ct, nil
}

// ListCompanyTaxRates GET /company-tax-rates?only_active=true
func (h *Handler) ListCompanyTaxRates(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	onlyActive := strings.ToLower(c.Query("only_active")) == "true"

	where := "ctr.tenant_id = $1 AND ctr.deleted_at IS NULL"
	args := []interface{}{tenantID}
	if onlyActive {
		where += " AND ctr.is_active = TRUE"
	}

	query := fmt.Sprintf(`
		SELECT %s %s
		WHERE %s
		ORDER BY ctr.sort_order ASC, ctr.code ASC
	`, selectCompanyTaxCols, fromCompanyTaxJoins, where)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list company tax rates", "error", err)
		response.InternalError(c, "Failed to list company tax rates")
		return
	}
	defer rows.Close()

	out := make([]*entity.CompanyTaxRate, 0)
	for rows.Next() {
		ct, err := scanCompanyTaxRate(rows)
		if err != nil {
			h.log.Error("Failed to scan company tax rate", "error", err)
			continue
		}
		out = append(out, ct)
	}
	response.Success(c, out)
}

// CreateCompanyTaxRate POST /company-tax-rates
func (h *Handler) CreateCompanyTaxRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input entity.CreateCompanyTaxRateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	if input.Code == "" || input.Name == "" {
		response.BadRequest(c, "code and name are required")
		return
	}
	if input.Rate < 0 || input.Rate > 100 {
		response.BadRequest(c, "rate must be between 0 and 100")
		return
	}
	appliesTo := normalizeCompanyTaxAppliesTo(input.AppliesTo)

	// Duplicate check
	var existingCount int
	if err := h.db.QueryRow(`
		SELECT COUNT(*) FROM company_tax_rates
		WHERE tenant_id = $1 AND LOWER(code) = LOWER($2) AND deleted_at IS NULL
	`, tenantID, input.Code).Scan(&existingCount); err == nil && existingCount > 0 {
		response.BadRequest(c, fmt.Sprintf("company tax with code %q already exists", input.Code))
		return
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}
	sortOrder := 0
	if input.SortOrder != nil {
		sortOrder = *input.SortOrder
	}
	description := strings.TrimSpace(input.Description)

	// TZ §5.2: NDS and Turnover tax are mutually exclusive (general vs
	// simplified regime). Block activating one while the other is active.
	if conflict, ok := h.ensureVatTurnoverExclusive(tenantID, appliesTo, isActive, nil); !ok {
		response.ErrorWithDetails(c, http.StatusBadRequest, "TAX_REGIME_CONFLICT",
			"NDS (sales) and Turnover tax cannot both be active at the same time",
			map[string]string{
				"applies_to": appliesTo,
				"conflict":   conflict,
			})
		return
	}

	id := uuid.New()
	now := time.Now()
	_, err := h.db.Exec(`
		INSERT INTO company_tax_rates (
			id, tenant_id, organization_id,
			code, name, description,
			rate, applies_to,
			account_id, is_active, sort_order,
			created_by, created_at, updated_at
		)
		VALUES ($1, $2, NULL,
			$3, $4, NULLIF($5, ''),
			$6, $7,
			$8, $9, $10,
			$11, $12, $12)
	`, id, tenantID,
		input.Code, input.Name, description,
		input.Rate, appliesTo,
		input.AccountID, isActive, sortOrder,
		userID, now)
	if err != nil {
		h.log.Error("Failed to insert company tax rate", "error", err)
		response.InternalError(c, "Failed to create company tax rate")
		return
	}

	// Project the sales-NDS rate into the legacy tax_rates table so per-line
	// invoice VAT picks it up (TZ §11.9). No-op for non-sales rows.
	if appliesTo == "sales" {
		h.syncSalesNdsToLegacyTaxRates(tenantID, input.Code, input.Name, input.Rate, isActive)
	}

	response.Success(c, gin.H{"id": id})
}

// UpdateCompanyTaxRate PUT /company-tax-rates/:id
func (h *Handler) UpdateCompanyTaxRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}

	var input entity.UpdateCompanyTaxRateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// TZ §5.2: NDS / Turnover exclusivity. Fetch the current row so we know
	// the EFFECTIVE applies_to + is_active after the caller's patch is
	// applied, then block the save if it would produce "both VAT and
	// turnover active" in the tenant.
	var currentApplies string
	var currentActive bool
	if err := h.db.QueryRow(`
		SELECT applies_to, is_active FROM company_tax_rates
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&currentApplies, &currentActive); err != nil {
		response.NotFound(c, "Company tax rate")
		return
	}
	effectiveApplies := currentApplies
	if input.AppliesTo != nil {
		effectiveApplies = normalizeCompanyTaxAppliesTo(*input.AppliesTo)
	}
	effectiveActive := currentActive
	if input.IsActive != nil {
		effectiveActive = *input.IsActive
	}
	if conflict, ok := h.ensureVatTurnoverExclusive(tenantID, effectiveApplies, effectiveActive, &id); !ok {
		response.ErrorWithDetails(c, http.StatusBadRequest, "TAX_REGIME_CONFLICT",
			"NDS (sales) and Turnover tax cannot both be active at the same time",
			map[string]string{
				"applies_to": effectiveApplies,
				"conflict":   conflict,
			})
		return
	}

	sets := []string{}
	args := []interface{}{}
	idx := 0
	addSet := func(col string, v interface{}) {
		idx++
		sets = append(sets, fmt.Sprintf("%s = $%d", col, idx))
		args = append(args, v)
	}

	if input.Code != nil {
		code := strings.TrimSpace(*input.Code)
		if code == "" {
			response.BadRequest(c, "code cannot be empty")
			return
		}
		addSet("code", code)
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			response.BadRequest(c, "name cannot be empty")
			return
		}
		addSet("name", name)
	}
	if input.Description != nil {
		desc := strings.TrimSpace(*input.Description)
		if desc == "" {
			addSet("description", nil)
		} else {
			addSet("description", desc)
		}
	}
	if input.Rate != nil {
		if *input.Rate < 0 || *input.Rate > 100 {
			response.BadRequest(c, "rate must be between 0 and 100")
			return
		}
		addSet("rate", *input.Rate)
	}
	if input.AppliesTo != nil {
		addSet("applies_to", normalizeCompanyTaxAppliesTo(*input.AppliesTo))
	}
	if input.ClearAccount {
		addSet("account_id", nil)
	} else if input.AccountID != nil {
		addSet("account_id", *input.AccountID)
	}
	if input.IsActive != nil {
		addSet("is_active", *input.IsActive)
	}
	if input.SortOrder != nil {
		addSet("sort_order", *input.SortOrder)
	}

	if len(sets) == 0 {
		response.BadRequest(c, "no fields to update")
		return
	}

	// updated_at always bumped
	idx++
	sets = append(sets, fmt.Sprintf("updated_at = $%d", idx))
	args = append(args, time.Now())

	// WHERE
	idx++
	args = append(args, id)
	idWhereIdx := idx
	idx++
	args = append(args, tenantID)
	tenantWhereIdx := idx

	q := fmt.Sprintf(`
		UPDATE company_tax_rates
		SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
	`, strings.Join(sets, ", "), idWhereIdx, tenantWhereIdx)

	res, err := h.db.Exec(q, args...)
	if err != nil {
		h.log.Error("Failed to update company tax rate", "error", err)
		response.InternalError(c, "Failed to update company tax rate")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Company tax rate")
		return
	}

	// If the row is (or became) sales-NDS, re-project its current state
	// into the legacy tax_rates table so invoice VAT is in sync (TZ §11.9).
	// We refetch instead of inferring so partial updates that didn't touch
	// rate/name still produce the correct projection.
	if effectiveApplies == "sales" {
		var (
			refCode   string
			refName   string
			refRate   float64
			refActive bool
		)
		if err := h.db.QueryRow(`
			SELECT code, name, rate, COALESCE(is_active, TRUE)
			FROM company_tax_rates
			WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		`, id, tenantID).Scan(&refCode, &refName, &refRate, &refActive); err == nil {
			h.syncSalesNdsToLegacyTaxRates(tenantID, refCode, refName, refRate, refActive)
		}
	}

	response.Success(c, gin.H{"id": id})
}

// DeleteCompanyTaxRate DELETE /company-tax-rates/:id (soft delete)
func (h *Handler) DeleteCompanyTaxRate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}
	res, err := h.db.Exec(`
		UPDATE company_tax_rates SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete company tax rate", "error", err)
		response.InternalError(c, "Failed to delete company tax rate")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Company tax rate")
		return
	}
	response.Success(c, gin.H{"id": id, "deleted": true})
}

// ─── Shared helpers for the consumer side ───────────────────────────────────
// These are what makes the Kompaniya soliqlari catalog actually drive
// calculations elsewhere (profit tax, turnover tax, etc. — see TZ §5, §6,
// §11.9). Nothing outside this file wrote to company_tax_rates before; now
// profit_tax.go and others call `getCompanyTaxRatePct` to pick up the
// tenant's configured rate instead of hardcoded constants.

// getCompanyTaxRatePct returns the active rate (percent, e.g. 15.0) for the
// given `applies_to` bucket. Falls back to the supplied default when no
// active rate is configured or when the lookup errors. Always returns a
// sensible value so callers can inline it without extra nil-handling.
func (h *Handler) getCompanyTaxRatePct(tenantID uuid.UUID, appliesTo string, fallback float64) float64 {
	var rate float64
	err := h.db.QueryRow(`
		SELECT rate FROM company_tax_rates
		WHERE tenant_id = $1
		  AND applies_to = $2
		  AND is_active = TRUE
		  AND deleted_at IS NULL
		ORDER BY sort_order ASC, created_at ASC
		LIMIT 1
	`, tenantID, appliesTo).Scan(&rate)
	if err != nil || rate <= 0 {
		return fallback
	}
	return rate
}

// hasActiveCompanyTaxRate reports whether the tenant has ANY active rate
// in the given `applies_to` bucket (excluding the optional row id so the
// caller can ignore the row currently being updated).
func (h *Handler) hasActiveCompanyTaxRate(tenantID uuid.UUID, appliesTo string, excludeID *uuid.UUID) bool {
	q := `
		SELECT COUNT(*) FROM company_tax_rates
		WHERE tenant_id = $1
		  AND applies_to = $2
		  AND is_active = TRUE
		  AND deleted_at IS NULL`
	args := []interface{}{tenantID, appliesTo}
	if excludeID != nil {
		q += " AND id <> $3"
		args = append(args, *excludeID)
	}
	var n int
	if err := h.db.QueryRow(q, args...).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// syncSalesNdsToLegacyTaxRates propagates the sales-NDS rate from
// `company_tax_rates` (the activity-level catalog the admin edits) into
// the legacy `tax_rates` table that the per-line invoice calculator
// already consumes (sales_order_lines.tax_id, sales_invoice_lines.tax_id,
// purchase_invoices.tax_rate_id, etc.). This is the lightweight bridge
// that closes TZ §11.9 "rate change → all calculations update" for
// invoice VAT without rewiring 10 FK columns.
//
// Direction: company_tax_rates → tax_rates only. The legacy table is
// FK'd from a dozen other places, so we treat it as the downstream
// projection of the catalog. We touch only the fields that map cleanly
// (rate, name, is_active); user-set behavioural flags on the legacy row
// (price_include, is_compound, is_recoverable, tax_account_id) are
// preserved because they don't have a counterpart in `company_tax_rates`.
//
// Match key: (tenant_id, LOWER(code)). On miss we insert a new
// `tax_rates` row with `tax_type='sales'` so it shows up in sales-line
// tax pickers immediately.
//
// The sync is best-effort: failures are logged but don't fail the
// caller's CRUD operation. The catalog row is the source of truth and
// stays consistent on its own; the legacy projection is convenience.
func (h *Handler) syncSalesNdsToLegacyTaxRates(tenantID uuid.UUID, code, name string, rate float64, isActive bool) {
	if code == "" {
		return
	}
	// Try update first.
	res, err := h.db.Exec(`
		UPDATE tax_rates
		SET rate = $1, name = $2, is_active = $3, updated_at = NOW()
		WHERE tenant_id = $4 AND LOWER(code) = LOWER($5) AND COALESCE(deleted_at IS NULL, TRUE)
	`, rate, name, isActive, tenantID, code)
	if err != nil {
		h.log.Warn("company_tax_rates → tax_rates sync UPDATE failed", "code", code, "error", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return
	}
	// No matching row — insert a fresh sales-tax entry.
	_, err = h.db.Exec(`
		INSERT INTO tax_rates (id, tenant_id, code, name, rate, type, tax_type, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'percentage', 'sales', $6, NOW(), NOW())
		ON CONFLICT (tenant_id, code) DO NOTHING
	`, uuid.New(), tenantID, code, name, rate, isActive)
	if err != nil {
		h.log.Warn("company_tax_rates → tax_rates sync INSERT failed", "code", code, "error", err)
	}
}

// ensureVatTurnoverExclusive enforces TZ §5.2: "НДС ёки Айланма --- бир
// вақтда иккаласи тўланмайди". A tenant is either on the general regime
// (NDS/VAT + profit tax) or on the simplified regime (turnover tax) — never
// both. Called from Create/Update when the caller is trying to make a
// `sales` or `turnover` row active.
//
// Returns (conflictAppliesTo, ok). `ok == false` means the save must be
// rejected; `conflictAppliesTo` is the opposing bucket so the UI can tell
// the user which rate to deactivate first.
func (h *Handler) ensureVatTurnoverExclusive(tenantID uuid.UUID, appliesTo string, willBeActive bool, excludeID *uuid.UUID) (string, bool) {
	if !willBeActive {
		return "", true
	}
	var opposing string
	switch appliesTo {
	case "sales":
		opposing = "turnover"
	case "turnover":
		opposing = "sales"
	default:
		return "", true
	}
	if h.hasActiveCompanyTaxRate(tenantID, opposing, excludeID) {
		return opposing, false
	}
	return "", true
}
