package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// Fixed Assets v1 — shared helpers (structured errors, account-group
// validation §8.11-12, effective-account resolution §2.6/§5).
// ============================================================================

// faErr emits the structured bilingual error the TZ mandates (§9): every
// failure carries a stable code + uz/ru messages — never a bare 404/empty body.
func faErr(c *gin.Context, status int, code, uz, ru string) {
	c.JSON(status, gin.H{
		"success": false,
		"error": gin.H{
			"code":       code,
			"message_uz": uz,
			"message_ru": ru,
			"message":    uz, // back-compat for existing FE error readers
		},
	})
}

// Account-group white-lists (§8.11). Prefix = first two digits of the code.
//
//	asset        ∈ 01xx (tangible) or 04xx (intangible)
//	depreciation ∈ 02xx or 05xx
//	expense      ∈ 20,23,25,27,94,08
var (
	faAssetGroups   = map[string]bool{"01": true, "04": true}
	faDeprGroups    = map[string]bool{"02": true, "05": true}
	faExpenseGroups = map[string]bool{"20": true, "23": true, "25": true, "27": true, "94": true, "08": true}
)

func faGroupOf(code string) string {
	code = strings.TrimSpace(code)
	if len(code) < 2 {
		return ""
	}
	return code[:2]
}

// faKind identifies which white-list an account code must satisfy.
type faKind int

const (
	faKindAsset faKind = iota
	faKindDepreciation
	faKindExpense
)

func faAllowedFor(kind faKind) map[string]bool {
	switch kind {
	case faKindAsset:
		return faAssetGroups
	case faKindDepreciation:
		return faDeprGroups
	default:
		return faExpenseGroups
	}
}

// validateAccountCode enforces both group membership (§8.11) and that the code
// resolves to an ACTIVE LEAF account in the tenant's chart (§8.12). Returns a
// structured error code ("" when valid) plus bilingual messages.
func (h *Handler) validateAccountCode(tenantID uuid.UUID, code string, kind faKind) (errCode, uz, ru string) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "ACCOUNT_REQUIRED", "Hisob tanlanmagan", "Счёт не выбран"
	}
	if !faAllowedFor(kind)[faGroupOf(code)] {
		return "ACCOUNT_GROUP_NOT_ALLOWED",
			"Bu hisob (" + code + ") ushbu maqsad uchun ruxsat etilmagan guruhda",
			"Счёт (" + code + ") относится к недопустимой для этой цели группе"
	}
	var isLeaf sql.NullBool
	err := h.db.QueryRow(`
		SELECT COALESCE(is_leaf, true) FROM accounts
		WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL AND COALESCE(is_active, true) = true
	`, tenantID, code).Scan(&isLeaf)
	if err == sql.ErrNoRows {
		return "ACCOUNT_NOT_FOUND",
			"Hisob (" + code + ") ish rejasida topilmadi", "Счёт (" + code + ") не найден в рабочем плане"
	}
	if err != nil {
		return "ACCOUNT_LOOKUP_FAILED", "Hisobni tekshirib bo'lmadi", "Не удалось проверить счёт"
	}
	if !isLeaf.Bool {
		return "ACCOUNT_NOT_LEAF",
			"Guruh hisobiga (" + code + ") provodka yozib bo'lmaydi", "Проводка на групповой счёт (" + code + ") запрещена"
	}
	return "", "", ""
}

// faEffectiveAccounts holds the resolved (post-override) account CODES for an
// asset: expense = COALESCE(override, department.expense_account),
// credit(depreciation) = COALESCE(override, category.depreciation_account),
// asset = COALESCE(override, category.asset_account).
type faEffectiveAccounts struct {
	AssetCode        string
	DepreciationCode string // "" when the category isn't depreciable
	ExpenseCode      string
	Depreciable      bool
}

// effectiveAccountsForAsset joins an asset to its category + department and
// applies per-asset overrides (§2.6). Missing pieces come back as empty strings
// so callers can report precise "MAPPING_MISSING" reasons.
func (h *Handler) effectiveAccountsForAsset(tenantID, assetID uuid.UUID) (faEffectiveAccounts, error) {
	var e faEffectiveAccounts
	var catAsset, catDepr sql.NullString
	var deptExpense sql.NullString
	var ovAsset, ovDepr, ovExpense sql.NullString
	var depreciable sql.NullBool
	err := h.db.QueryRow(`
		SELECT c.asset_account, c.depreciation_account, c.depreciable,
		       d.expense_account,
		       a.asset_account_override, a.depreciation_account_override, a.expense_account_override
		FROM fa_assets a
		JOIN fa_categories  c ON c.id = a.category_id
		JOIN fa_departments d ON d.id = a.department_id
		WHERE a.id = $1 AND a.tenant_id = $2
	`, assetID, tenantID).Scan(&catAsset, &catDepr, &depreciable, &deptExpense, &ovAsset, &ovDepr, &ovExpense)
	if err != nil {
		return e, err
	}
	e.Depreciable = depreciable.Bool
	e.AssetCode = coalesceStr(ovAsset, catAsset)
	e.DepreciationCode = coalesceStr(ovDepr, catDepr)
	e.ExpenseCode = coalesceStr(ovExpense, deptExpense)
	return e, nil
}

func coalesceStr(override, base sql.NullString) string {
	if override.Valid && strings.TrimSpace(override.String) != "" {
		return strings.TrimSpace(override.String)
	}
	if base.Valid {
		return strings.TrimSpace(base.String)
	}
	return ""
}

// resolveAccountID maps a text account code to its UUID in the tenant's chart,
// preferring a leaf account. Used when writing journal_entry_lines.
func (h *Handler) resolveAccountID(tenantID uuid.UUID, code string) (uuid.UUID, bool) {
	var id uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM accounts
		WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL AND COALESCE(is_active, true) = true
		ORDER BY COALESCE(is_leaf, true) DESC
		LIMIT 1
	`, tenantID, code).Scan(&id)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// nextInventoryNumber allocates the next per-tenant inventory number atomically
// via the fa_number_counters upsert (§2.3). Format: FA-000001.
func (h *Handler) nextInventoryNumber(tx *sql.Tx, tenantID uuid.UUID) (string, error) {
	var n int64
	err := tx.QueryRow(`
		INSERT INTO fa_number_counters (tenant_id, next_inventory) VALUES ($1, 2)
		ON CONFLICT (tenant_id) DO UPDATE SET next_inventory = fa_number_counters.next_inventory + 1
		RETURNING next_inventory - 1
	`, tenantID).Scan(&n)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("FA-%06d", n), nil
}

// faOK is a tiny success helper so we always emit a body (TZ §9 incident ref).
func faOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// faLifecycleAccounts are the tenant-editable accounts used by lifecycle
// postings (purchase, VAT, payment, commission clearing, disposal). Formerly
// hardcoded constants in fa_assets.go; since migration 453 they live in
// fa_settings with NSBU defaults.
type faLifecycleAccounts struct {
	Acquisition        string // 0810 kapital qo'yilma
	AP                 string // 6010 ta'minotchilar
	Cash               string // 5010
	Bank               string // 5110
	VATInput           string // 4410
	Disposal           string // 9210 chiqib ketish (tranzit)
	DisposalGain       string // 9310
	DisposalLoss       string // 9490
	DisposalReceivable string // 4790 (sotish tushumi)
}

// lifecycleAccounts loads the tenant's lifecycle accounts, falling back to the
// NSBU defaults when the settings row is missing (pre-453 tenants get the row
// via the migration; this is belt-and-braces).
func (h *Handler) lifecycleAccounts(tenantID uuid.UUID) faLifecycleAccounts {
	a := faLifecycleAccounts{
		Acquisition: "0810", AP: "6010", Cash: "5010", Bank: "5110", VATInput: "4410",
		Disposal: "9210", DisposalGain: "9310", DisposalLoss: "9490", DisposalReceivable: "4790",
	}
	h.db.QueryRow(`
		SELECT acquisition_account, ap_account, cash_account, bank_account, vat_input_account,
		       disposal_account, disposal_gain_account, disposal_loss_account, disposal_receivable_account
		FROM fa_settings WHERE tenant_id = $1`, tenantID).
		Scan(&a.Acquisition, &a.AP, &a.Cash, &a.Bank, &a.VATInput,
			&a.Disposal, &a.DisposalGain, &a.DisposalLoss, &a.DisposalReceivable)
	return a
}
