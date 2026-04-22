package handler

import (
	"database/sql"
	"fmt"
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
