package handler

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// Fixed Assets v1 — account mapping settings (§2.5) + dropdown feed (§8.11-12).
// ============================================================================

type faCategoryDTO struct {
	ID                  string `json:"id,omitempty"`
	Code                string `json:"code"`
	NameUz              string `json:"name_uz"`
	NameRu              string `json:"name_ru"`
	AssetAccount        string `json:"asset_account"`
	DepreciationAccount string `json:"depreciation_account"`
	Depreciable         bool   `json:"depreciable"`
	IsActive            bool   `json:"is_active"`
}

type faDepartmentDTO struct {
	ID             string `json:"id,omitempty"`
	Code           string `json:"code"`
	NameUz         string `json:"name_uz"`
	ExpenseAccount string `json:"expense_account"`
	IsActive       bool   `json:"is_active"`
}

type faSettingsDTO struct {
	AutoPost    bool   `json:"auto_post"`
	CronEnabled bool   `json:"cron_enabled"`
	StartRule   string `json:"start_rule"`
	Rounding    int    `json:"rounding"`
	// Lifecycle accounts (migration 453) — NSBU defaults, tenant-editable.
	AcquisitionAccount        string `json:"acquisition_account"`
	APAccount                 string `json:"ap_account"`
	CashAccount               string `json:"cash_account"`
	BankAccount               string `json:"bank_account"`
	VATInputAccount           string `json:"vat_input_account"`
	DisposalAccount           string `json:"disposal_account"`
	DisposalGainAccount       string `json:"disposal_gain_account"`
	DisposalLossAccount       string `json:"disposal_loss_account"`
	DisposalReceivableAccount string `json:"disposal_receivable_account"`
}

// GetAssetMapping returns the tenant's categories, departments and settings.
// GET /api/v1/settings/asset-mapping
func (h *Handler) GetAssetMapping(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		faErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Tenant topilmadi", "Тенант не найден")
		return
	}

	// Deliberately NOT paginated. A single `meta` cannot describe two
	// independent collections, so paging this would mean splitting it into
	// /settings/asset-mapping/categories and /…/departments and leaving this
	// route as a settings-only read — a breaking change to the web mapping
	// screen and to PUT /settings/asset-mapping, which round-trips the same
	// object. Both collections are dropdown sources, not feeds, and are
	// bounded by the tenant's own hand-written chart-of-accounts mapping.
	//
	// is_active=true is the cheap mitigation: the client already filters on
	// is_active locally, so this just moves that filter to where the rows are.
	activeOnly := ""
	if c.Query("is_active") == "true" {
		activeOnly = " AND is_active = true"
	}

	cats := []faCategoryDTO{}
	rows, err := h.db.Query(`
		SELECT id, code, name_uz, COALESCE(name_ru,''), asset_account, COALESCE(depreciation_account,''), depreciable, is_active
		FROM fa_categories WHERE tenant_id = $1`+activeOnly+` ORDER BY code`, tenantID)
	if err == nil {
		for rows.Next() {
			var d faCategoryDTO
			if rows.Scan(&d.ID, &d.Code, &d.NameUz, &d.NameRu, &d.AssetAccount, &d.DepreciationAccount, &d.Depreciable, &d.IsActive) == nil {
				cats = append(cats, d)
			}
		}
		rows.Close()
	}

	depts := []faDepartmentDTO{}
	drows, err := h.db.Query(`
		SELECT id, code, name_uz, expense_account, is_active
		FROM fa_departments WHERE tenant_id = $1`+activeOnly+` ORDER BY code`, tenantID)
	if err == nil {
		for drows.Next() {
			var d faDepartmentDTO
			if drows.Scan(&d.ID, &d.Code, &d.NameUz, &d.ExpenseAccount, &d.IsActive) == nil {
				depts = append(depts, d)
			}
		}
		drows.Close()
	}

	settings := faSettingsDTO{
		CronEnabled: true, StartRule: "next_month", Rounding: 2,
		AcquisitionAccount: "0810", APAccount: "6010", CashAccount: "5010", BankAccount: "5110",
		VATInputAccount: "4410", DisposalAccount: "9210", DisposalGainAccount: "9310",
		DisposalLossAccount: "9490", DisposalReceivableAccount: "4790",
	}
	h.db.QueryRow(`SELECT auto_post, cron_enabled, start_rule, rounding,
			acquisition_account, ap_account, cash_account, bank_account, vat_input_account,
			disposal_account, disposal_gain_account, disposal_loss_account, disposal_receivable_account
		FROM fa_settings WHERE tenant_id = $1`, tenantID).
		Scan(&settings.AutoPost, &settings.CronEnabled, &settings.StartRule, &settings.Rounding,
			&settings.AcquisitionAccount, &settings.APAccount, &settings.CashAccount, &settings.BankAccount,
			&settings.VATInputAccount, &settings.DisposalAccount, &settings.DisposalGainAccount,
			&settings.DisposalLossAccount, &settings.DisposalReceivableAccount)

	faOK(c, gin.H{"categories": cats, "departments": depts, "settings": settings})
}

type updateAssetMappingInput struct {
	Categories  []faCategoryDTO   `json:"categories"`
	Departments []faDepartmentDTO `json:"departments"`
	Settings    *faSettingsDTO    `json:"settings"`
}

// UpdateAssetMapping upserts categories/departments/settings. Every account code
// is validated by group + leaf (§8.11-12); a bad one aborts the whole save.
// PUT /api/v1/settings/asset-mapping   (perm accounting.edit_mapping)
func (h *Handler) UpdateAssetMapping(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		faErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Tenant topilmadi", "Тенант не найден")
		return
	}

	var in updateAssetMappingInput
	if err := c.ShouldBindJSON(&in); err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Ma'lumot noto'g'ri", "Некорректные данные")
		return
	}

	// Validate all account codes BEFORE writing anything.
	for _, cat := range in.Categories {
		if code, uz, ru := h.validateAccountCode(tenantID, cat.AssetAccount, faKindAsset); code != "" {
			faErr(c, http.StatusBadRequest, code, cat.Code+": "+uz, cat.Code+": "+ru)
			return
		}
		if cat.Depreciable {
			if code, uz, ru := h.validateAccountCode(tenantID, cat.DepreciationAccount, faKindDepreciation); code != "" {
				faErr(c, http.StatusBadRequest, code, cat.Code+": "+uz, cat.Code+": "+ru)
				return
			}
		}
	}
	for _, dept := range in.Departments {
		if code, uz, ru := h.validateAccountCode(tenantID, dept.ExpenseAccount, faKindExpense); code != "" {
			faErr(c, http.StatusBadRequest, code, dept.Code+": "+uz, dept.Code+": "+ru)
			return
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer tx.Rollback()

	for _, cat := range in.Categories {
		var deprAcct interface{}
		if cat.Depreciable && strings.TrimSpace(cat.DepreciationAccount) != "" {
			deprAcct = strings.TrimSpace(cat.DepreciationAccount)
		}
		_, err := tx.Exec(`
			INSERT INTO fa_categories (tenant_id, code, name_uz, name_ru, asset_account, depreciation_account, depreciable, is_active, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,COALESCE($8,true),NOW())
			ON CONFLICT (tenant_id, code) DO UPDATE SET
				name_uz = EXCLUDED.name_uz, name_ru = EXCLUDED.name_ru,
				asset_account = EXCLUDED.asset_account, depreciation_account = EXCLUDED.depreciation_account,
				depreciable = EXCLUDED.depreciable, is_active = EXCLUDED.is_active, updated_at = NOW()
		`, tenantID, strings.TrimSpace(cat.Code), cat.NameUz, nullIfEmpty(cat.NameRu),
			strings.TrimSpace(cat.AssetAccount), deprAcct, cat.Depreciable, cat.IsActive)
		if err != nil {
			faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Kategoriyani saqlab bo'lmadi", "Не удалось сохранить категорию")
			return
		}
	}

	for _, dept := range in.Departments {
		_, err := tx.Exec(`
			INSERT INTO fa_departments (tenant_id, code, name_uz, expense_account, is_active, updated_at)
			VALUES ($1,$2,$3,$4,$5,NOW())
			ON CONFLICT (tenant_id, code) DO UPDATE SET
				name_uz = EXCLUDED.name_uz, expense_account = EXCLUDED.expense_account,
				is_active = EXCLUDED.is_active, updated_at = NOW()
		`, tenantID, strings.TrimSpace(dept.Code), dept.NameUz, strings.TrimSpace(dept.ExpenseAccount), dept.IsActive)
		if err != nil {
			faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Bo'limni saqlab bo'lmadi", "Не удалось сохранить подразделение")
			return
		}
	}

	if in.Settings != nil {
		s := in.Settings
		if s.Rounding <= 0 {
			s.Rounding = 2
		}
		if s.StartRule == "" {
			s.StartRule = "next_month"
		}
		// Lifecycle accounts: every non-empty code must be an active leaf in the
		// tenant's chart (group whitelists don't apply — these span 08/60/50/51/
		// 44/92/93/94/48). Empty values fall back to the NSBU defaults.
		lc := map[string]*string{
			"0810": &s.AcquisitionAccount, "6010": &s.APAccount, "5010": &s.CashAccount,
			"5110": &s.BankAccount, "4410": &s.VATInputAccount, "9210": &s.DisposalAccount,
			"9310": &s.DisposalGainAccount, "9490": &s.DisposalLossAccount, "4790": &s.DisposalReceivableAccount,
		}
		for def, field := range lc {
			*field = strings.TrimSpace(*field)
			if *field == "" {
				*field = def
				continue
			}
			var isLeaf sql.NullBool
			err := h.db.QueryRow(`SELECT COALESCE(is_leaf, true) FROM accounts
				WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL AND COALESCE(is_active, true) = true`,
				tenantID, *field).Scan(&isLeaf)
			if err != nil || !isLeaf.Bool {
				faErr(c, http.StatusBadRequest, "ACCOUNT_NOT_FOUND",
					"Hisob ("+*field+") ish rejasida topilmadi yoki guruh hisobi",
					"Счёт ("+*field+") не найден или групповой")
				return
			}
		}
		_, err := tx.Exec(`
			INSERT INTO fa_settings (tenant_id, auto_post, cron_enabled, start_rule, rounding,
				acquisition_account, ap_account, cash_account, bank_account, vat_input_account,
				disposal_account, disposal_gain_account, disposal_loss_account, disposal_receivable_account, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NOW())
			ON CONFLICT (tenant_id) DO UPDATE SET
				auto_post = EXCLUDED.auto_post, cron_enabled = EXCLUDED.cron_enabled,
				start_rule = EXCLUDED.start_rule, rounding = EXCLUDED.rounding,
				acquisition_account = EXCLUDED.acquisition_account, ap_account = EXCLUDED.ap_account,
				cash_account = EXCLUDED.cash_account, bank_account = EXCLUDED.bank_account,
				vat_input_account = EXCLUDED.vat_input_account, disposal_account = EXCLUDED.disposal_account,
				disposal_gain_account = EXCLUDED.disposal_gain_account, disposal_loss_account = EXCLUDED.disposal_loss_account,
				disposal_receivable_account = EXCLUDED.disposal_receivable_account, updated_at = NOW()
		`, tenantID, s.AutoPost, s.CronEnabled, s.StartRule, s.Rounding,
			s.AcquisitionAccount, s.APAccount, s.CashAccount, s.BankAccount, s.VATInputAccount,
			s.DisposalAccount, s.DisposalGainAccount, s.DisposalLossAccount, s.DisposalReceivableAccount)
		if err != nil {
			faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Sozlamalarni saqlab bo'lmadi", "Не удалось сохранить настройки")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	faOK(c, gin.H{"message": "Mapping saqlandi"})
}

// ListChartAccountsFiltered feeds the mapping dropdowns: only active LEAF
// accounts, optionally filtered by a 2-digit group prefix (§2.5, §8.12).
// GET /api/v1/asset-mapping/accounts?group=01&leaf=true
func (h *Handler) ListChartAccountsFiltered(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		faErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Tenant topilmadi", "Тенант не найден")
		return
	}

	q := `SELECT id, code, name FROM accounts
	      WHERE tenant_id = $1 AND deleted_at IS NULL AND COALESCE(is_active, true) = true`
	args := []interface{}{tenantID}

	// leaf defaults to true — never offer a group node for posting.
	if strings.ToLower(c.DefaultQuery("leaf", "true")) != "false" {
		q += ` AND COALESCE(is_leaf, true) = true`
	}
	if group := strings.TrimSpace(c.Query("group")); group != "" {
		args = append(args, group+"%")
		q += ` AND code LIKE $2`
	}
	q += ` ORDER BY code`

	type acct struct {
		ID   string `json:"id"`
		Code string `json:"code"`
		Name string `json:"name"`
	}
	out := []acct{}
	rows, err := h.db.Query(q, args...)
	if err == nil {
		for rows.Next() {
			var a acct
			if rows.Scan(&a.ID, &a.Code, &a.Name) == nil {
				out = append(out, a)
			}
		}
		rows.Close()
	}
	faOK(c, out)
}
