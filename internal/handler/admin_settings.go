package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AdminSettingsResponse represents the admin settings structure
type AdminSettingsResponse struct {
	General       map[string]interface{} `json:"general"`
	CRM           map[string]interface{} `json:"crm"`
	Sales         map[string]interface{} `json:"sales"`
	Inventory     map[string]interface{} `json:"inventory"`
	Purchase      map[string]interface{} `json:"purchase"`
	Manufacturing map[string]interface{} `json:"manufacturing"`
	HR            map[string]interface{} `json:"hr"`
	Finance       map[string]interface{} `json:"finance"`
	Projects      map[string]interface{} `json:"projects"`
	UpdatedAt     *time.Time             `json:"updated_at,omitempty"`
	UpdatedBy     *uuid.UUID             `json:"updated_by,omitempty"`
}

// Default admin settings
func getDefaultAdminSettings() AdminSettingsResponse {
	return AdminSettingsResponse{
		General: map[string]interface{}{
			"company": map[string]interface{}{
				"name":       "",
				"legal_name": "",
				"tax_id":     "",
				"address":    "",
				"phone":      "",
				"email":      "",
				"website":    "",
			},
			"localization": map[string]interface{}{
				"language":    "en",
				"timezone":    "Asia/Tashkent",
				"currency":    "UZS",
				"date_format": "DD/MM/YYYY",
				"time_format": "24h",
			},
			"fiscal": map[string]interface{}{
				"fiscal_year_start": "01-01",
			},
		},
		CRM: map[string]interface{}{
			"lead_scoring": map[string]interface{}{
				"enabled": true,
			},
			"pipeline": map[string]interface{}{
				"default_stages": []string{"New", "Qualified", "Proposal", "Negotiation", "Won", "Lost"},
			},
			"auto_assign": map[string]interface{}{
				"enabled": false,
				"method":  "round_robin",
			},
		},
		Sales: map[string]interface{}{
			"quotation": map[string]interface{}{
				"validity_days": 30,
				"auto_confirm":  false,
			},
			"pricing": map[string]interface{}{
				"strategy":     "standard",
				"allow_discount": true,
				"max_discount": 20,
			},
			"payment": map[string]interface{}{
				"default_terms": "net_30",
			},
		},
		Inventory: map[string]interface{}{
			"traceability": map[string]interface{}{
				"lot_tracking":    false,
				"serial_tracking": false,
				"expiry_tracking": false,
			},
			"costing": map[string]interface{}{
				"method": "average",
			},
			"warehouse": map[string]interface{}{
				"allow_negative_stock": false,
				"require_location":     false,
			},
		},
		Purchase: map[string]interface{}{
			"approval": map[string]interface{}{
				"enabled":              true,
				"threshold":            1000000,
				"require_multi_quotes": false,
			},
			"vendor": map[string]interface{}{
				"rating_enabled":      true,
				"default_payment_terms": "net_30",
			},
		},
		Manufacturing: map[string]interface{}{
			"planning": map[string]interface{}{
				"method":   "mrp",
				"horizon":  30,
			},
			"work_center": map[string]interface{}{
				"default_efficiency": 100,
			},
			"quality": map[string]interface{}{
				"enabled":   true,
				"mandatory": false,
			},
		},
		HR: map[string]interface{}{
			"leave": map[string]interface{}{
				"types": []map[string]interface{}{
					{"name": "Annual Leave", "days_per_year": 24},
					{"name": "Sick Leave", "days_per_year": 10},
					{"name": "Personal Leave", "days_per_year": 5},
				},
			},
			"work_hours": map[string]interface{}{
				"hours_per_day": 8,
				"days_per_week": 5,
			},
		},
		Finance: map[string]interface{}{
			"fiscal_year": map[string]interface{}{
				"start_month": 1,
				"start_day":   1,
			},
			"tax": map[string]interface{}{
				"default_rate": 12,
			},
			"accounts": map[string]interface{}{
				"receivable_account": "",
				"payable_account":    "",
				"revenue_account":    "",
				"expense_account":    "",
			},
			"lock_date": map[string]interface{}{
				"date":    nil,
				"enabled": false,
			},
		},
		Projects: map[string]interface{}{
			"billing": map[string]interface{}{
				"default_type": "fixed",
			},
			"timesheet": map[string]interface{}{
				"approval_required": true,
			},
		},
	}
}

// GetAdminSettings returns admin settings for the tenant
func (h *Handler) GetAdminSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Try to get settings from database
	var settingsJSON []byte
	var updatedAt sql.NullTime
	var updatedBy sql.NullString

	err := h.db.QueryRow(`
		SELECT settings, updated_at, updated_by
		FROM tenant_settings
		WHERE tenant_id = $1
	`, tenantID).Scan(&settingsJSON, &updatedAt, &updatedBy)

	if err == sql.ErrNoRows {
		// Return default settings
		response.Success(c, getDefaultAdminSettings())
		return
	}
	if err != nil {
		h.log.Error("Failed to get admin settings", "error", err)
		// Return default settings on error
		response.Success(c, getDefaultAdminSettings())
		return
	}

	// Parse stored settings
	var settings AdminSettingsResponse
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		h.log.Error("Failed to parse admin settings", "error", err)
		response.Success(c, getDefaultAdminSettings())
		return
	}

	if updatedAt.Valid {
		settings.UpdatedAt = &updatedAt.Time
	}
	if updatedBy.Valid {
		if uid, err := uuid.Parse(updatedBy.String); err == nil {
			settings.UpdatedBy = &uid
		}
	}

	response.Success(c, settings)
}

// UpdateAdminSettings updates all admin settings
func (h *Handler) UpdateAdminSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input map[string]interface{}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get settings from input, if wrapped in "settings" key
	settings, ok := input["settings"]
	if !ok {
		settings = input
	}

	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		response.BadRequest(c, "Invalid settings format")
		return
	}

	now := time.Now()

	// Upsert settings
	_, err = h.db.Exec(`
		INSERT INTO tenant_settings (tenant_id, settings, updated_at, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id) DO UPDATE SET
			settings = EXCLUDED.settings,
			updated_at = EXCLUDED.updated_at,
			updated_by = EXCLUDED.updated_by
	`, tenantID, settingsJSON, now, userID)

	if err != nil {
		h.log.Error("Failed to update admin settings", "error", err)
		response.InternalError(c, "Failed to update settings")
		return
	}

	response.Success(c, gin.H{
		"message":    "Settings updated successfully",
		"updated_at": now,
	})
}

// UpdateAdminSettingsSection updates a specific section of admin settings
func (h *Handler) UpdateAdminSettingsSection(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	section := c.Param("section")
	if section == "" {
		response.BadRequest(c, "Section parameter is required")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get current settings
	var settingsJSON []byte
	err := h.db.QueryRow("SELECT settings FROM tenant_settings WHERE tenant_id = $1", tenantID).Scan(&settingsJSON)

	var currentSettings map[string]interface{}
	if err == sql.ErrNoRows {
		// Start with defaults
		defaults := getDefaultAdminSettings()
		settingsBytes, _ := json.Marshal(defaults)
		json.Unmarshal(settingsBytes, &currentSettings)
	} else if err != nil {
		h.log.Error("Failed to get current settings", "error", err)
		response.InternalError(c, "Failed to update settings")
		return
	} else {
		json.Unmarshal(settingsJSON, &currentSettings)
	}

	// Update the specific section
	currentSettings[section] = input.Data

	// Save back
	newSettingsJSON, _ := json.Marshal(currentSettings)
	now := time.Now()

	_, err = h.db.Exec(`
		INSERT INTO tenant_settings (tenant_id, settings, updated_at, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id) DO UPDATE SET
			settings = EXCLUDED.settings,
			updated_at = EXCLUDED.updated_at,
			updated_by = EXCLUDED.updated_by
	`, tenantID, newSettingsJSON, now, userID)

	if err != nil {
		h.log.Error("Failed to update admin settings section", "error", err)
		response.InternalError(c, "Failed to update settings")
		return
	}

	response.Success(c, gin.H{
		"message":    "Section updated successfully",
		"section":    section,
		"updated_at": now,
	})
}

// ResetAdminSettingsSection resets a specific section to defaults
func (h *Handler) ResetAdminSettingsSection(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	section := c.Param("section")
	if section == "" {
		response.BadRequest(c, "Section parameter is required")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Get current settings
	var settingsJSON []byte
	err := h.db.QueryRow("SELECT settings FROM tenant_settings WHERE tenant_id = $1", tenantID).Scan(&settingsJSON)

	var currentSettings map[string]interface{}
	if err == sql.ErrNoRows {
		response.Success(c, gin.H{"message": "Section reset to defaults", "section": section})
		return
	} else if err != nil {
		h.log.Error("Failed to get current settings", "error", err)
		response.InternalError(c, "Failed to reset settings")
		return
	}
	json.Unmarshal(settingsJSON, &currentSettings)

	// Get defaults
	defaults := getDefaultAdminSettings()
	defaultsBytes, _ := json.Marshal(defaults)
	var defaultSettings map[string]interface{}
	json.Unmarshal(defaultsBytes, &defaultSettings)

	// Reset the specific section
	if defaultValue, ok := defaultSettings[section]; ok {
		currentSettings[section] = defaultValue
	} else {
		response.BadRequest(c, "Unknown section: "+section)
		return
	}

	// Save back
	newSettingsJSON, _ := json.Marshal(currentSettings)
	now := time.Now()

	_, err = h.db.Exec(`
		INSERT INTO tenant_settings (tenant_id, settings, updated_at, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id) DO UPDATE SET
			settings = EXCLUDED.settings,
			updated_at = EXCLUDED.updated_at,
			updated_by = EXCLUDED.updated_by
	`, tenantID, newSettingsJSON, now, userID)

	if err != nil {
		h.log.Error("Failed to reset admin settings section", "error", err)
		response.InternalError(c, "Failed to reset settings")
		return
	}

	response.Success(c, gin.H{
		"message":    "Section reset to defaults",
		"section":    section,
		"updated_at": now,
	})
}

// ResetAllAdminSettings resets all settings to defaults
func (h *Handler) ResetAllAdminSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	defaults := getDefaultAdminSettings()
	settingsJSON, _ := json.Marshal(defaults)
	now := time.Now()

	_, err := h.db.Exec(`
		INSERT INTO tenant_settings (tenant_id, settings, updated_at, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id) DO UPDATE SET
			settings = EXCLUDED.settings,
			updated_at = EXCLUDED.updated_at,
			updated_by = EXCLUDED.updated_by
	`, tenantID, settingsJSON, now, userID)

	if err != nil {
		h.log.Error("Failed to reset all admin settings", "error", err)
		response.InternalError(c, "Failed to reset settings")
		return
	}

	response.Success(c, gin.H{
		"message":    "All settings reset to defaults",
		"updated_at": now,
	})
}

// checkLockDate reads the lock date from tenant_settings and returns an error message
// if the given entryDate is on or before the lock date. Returns "" if allowed.
func (h *Handler) checkLockDate(tenantID uuid.UUID, entryDate time.Time) string {
	var settingsJSON []byte
	err := h.db.QueryRow(
		"SELECT settings FROM tenant_settings WHERE tenant_id = $1",
		tenantID,
	).Scan(&settingsJSON)
	if err != nil {
		return ""
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		return ""
	}

	finance, ok := settings["finance"].(map[string]interface{})
	if !ok {
		return ""
	}

	lockDateSection, ok := finance["lock_date"].(map[string]interface{})
	if !ok {
		return ""
	}

	enabled, _ := lockDateSection["enabled"].(bool)
	if !enabled {
		return ""
	}

	dateStr, ok := lockDateSection["date"].(string)
	if !ok || dateStr == "" {
		return ""
	}

	lockDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return ""
	}

	entryDateOnly := time.Date(entryDate.Year(), entryDate.Month(), entryDate.Day(), 0, 0, 0, 0, time.UTC)
	lockDateOnly := time.Date(lockDate.Year(), lockDate.Month(), lockDate.Day(), 0, 0, 0, 0, time.UTC)

	if !entryDateOnly.After(lockDateOnly) {
		return fmt.Sprintf("Cannot create or modify accounting entries on or before the lock date (%s)", lockDate.Format("2006-01-02"))
	}

	return ""
}

// checkPeriodLock checks if the entry date falls in a locked or closed fiscal period
// or in a locked accounting period
func (h *Handler) checkPeriodLock(tenantID uuid.UUID, entryDate time.Time) string {
	// Check fiscal periods
	var periodStatus sql.NullString
	err := h.db.QueryRow(`
		SELECT fp.status FROM fiscal_periods fp
		JOIN fiscal_years fy ON fp.fiscal_year_id = fy.id
		WHERE fy.tenant_id = $1 AND $2 BETWEEN fp.start_date AND fp.end_date
		LIMIT 1
	`, tenantID, entryDate).Scan(&periodStatus)
	if err == nil && periodStatus.Valid {
		if periodStatus.String == "locked" || periodStatus.String == "closed" {
			return fmt.Sprintf("This period is %s. Contact admin to unlock.", periodStatus.String)
		}
	}

	// Check accounting periods
	var isLocked bool
	err = h.db.QueryRow(`
		SELECT is_locked FROM accounting_periods
		WHERE tenant_id = $1 AND start_date <= $2 AND end_date >= $2 AND is_locked = true
		LIMIT 1
	`, tenantID, entryDate).Scan(&isLocked)
	if err == nil && isLocked {
		return "Bu davr yopilgan"
	}

	return ""
}

// dbQuerier is an interface satisfied by both *sql.DB and *sql.Tx
type dbQuerier interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

// findAccount looks up an account by name pattern within an organization, falling back to
// tenant-wide name match, then org-specific code match. This handles organizations where
// account codes may be reassigned to different account types.
// nameLike should be specific (e.g. "accounts receivable", "sales revenue") to avoid ambiguity.
func findAccount(q dbQuerier, tenantID uuid.UUID, orgID *uuid.UUID, nameLike string, code string) uuid.UUID {
	var id uuid.UUID

	// 1. Try org + exact name start match (most precise)
	if orgID != nil {
		_ = q.QueryRow(
			`SELECT id FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND LOWER(name) LIKE $3 AND deleted_at IS NULL LIMIT 1`,
			tenantID, *orgID, nameLike+"%",
		).Scan(&id)
		if id != uuid.Nil {
			return id
		}
	}

	// 2. Try org + contains name match (broader)
	if orgID != nil {
		_ = q.QueryRow(
			`SELECT id FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND LOWER(name) LIKE $3 AND deleted_at IS NULL LIMIT 1`,
			tenantID, *orgID, "%"+nameLike+"%",
		).Scan(&id)
		if id != uuid.Nil {
			return id
		}
	}

	// 3. Try org + code match
	if orgID != nil {
		_ = q.QueryRow(
			`SELECT id FROM accounts WHERE tenant_id = $1 AND (organization_id = $2 OR organization_id IS NULL) AND code = $3 AND deleted_at IS NULL LIMIT 1`,
			tenantID, *orgID, code,
		).Scan(&id)
		if id != uuid.Nil {
			return id
		}
	}

	// 4. Fallback: tenant-wide name match (no org filter)
	_ = q.QueryRow(
		`SELECT id FROM accounts WHERE tenant_id = $1 AND LOWER(name) LIKE $2 AND deleted_at IS NULL LIMIT 1`,
		tenantID, nameLike+"%",
	).Scan(&id)
	if id != uuid.Nil {
		return id
	}

	// 5. Last resort: tenant-wide code match
	_ = q.QueryRow(
		`SELECT id FROM accounts WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL LIMIT 1`,
		tenantID, code,
	).Scan(&id)
	if id != uuid.Nil {
		return id
	}

	// 6. Code prefix match (org-scoped, then tenant-wide)
	if orgID != nil {
		_ = q.QueryRow(
			`SELECT id FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND code LIKE $3 AND deleted_at IS NULL ORDER BY code ASC LIMIT 1`,
			tenantID, *orgID, code+"%",
		).Scan(&id)
		if id != uuid.Nil {
			return id
		}
	}
	_ = q.QueryRow(
		`SELECT id FROM accounts WHERE tenant_id = $1 AND code LIKE $2 AND deleted_at IS NULL ORDER BY code ASC LIMIT 1`,
		tenantID, code+"%",
	).Scan(&id)
	return id
}

// getContactDefaultAccount returns the contact's default receivable or payable account.
// accountType should be "receivable" or "payable".
func getContactDefaultAccount(q dbQuerier, contactID uuid.UUID, accountType string) uuid.UUID {
	var id uuid.UUID
	col := "default_receivable_account_id"
	if accountType == "payable" {
		col = "default_payable_account_id"
	}
	_ = q.QueryRow(
		fmt.Sprintf(`SELECT %s FROM contacts WHERE id = $1 AND %s IS NOT NULL AND deleted_at IS NULL`, col, col),
		contactID,
	).Scan(&id)
	return id
}

// CategoryAccounts holds the GL accounts configured on a product category (Odoo-style).
type CategoryAccounts struct {
	IncomeAccountID         uuid.UUID
	ExpenseAccountID        uuid.UUID
	StockValuationAccountID uuid.UUID
	StockInputAccountID     uuid.UUID
	StockOutputAccountID    uuid.UUID
}

// getCategoryAccounts returns GL accounts for a product's category.
// Falls back to findAccount() defaults if category accounts are not configured.
func getCategoryAccounts(q dbQuerier, tenantID uuid.UUID, orgID *uuid.UUID, productID uuid.UUID) CategoryAccounts {
	var ca CategoryAccounts
	// Query: product → category → category's account fields
	_ = q.QueryRow(`
		SELECT COALESCE(pc.income_account_id, '00000000-0000-0000-0000-000000000000'),
		       COALESCE(pc.expense_account_id, '00000000-0000-0000-0000-000000000000'),
		       COALESCE(pc.stock_valuation_account_id, '00000000-0000-0000-0000-000000000000'),
		       COALESCE(pc.stock_input_account_id, '00000000-0000-0000-0000-000000000000'),
		       COALESCE(pc.stock_output_account_id, '00000000-0000-0000-0000-000000000000')
		FROM products p
		JOIN product_categories pc ON p.category_id = pc.id
		WHERE p.id = $1
	`, productID).Scan(&ca.IncomeAccountID, &ca.ExpenseAccountID,
		&ca.StockValuationAccountID, &ca.StockInputAccountID, &ca.StockOutputAccountID)

	// Fallbacks if category accounts not set
	if ca.IncomeAccountID == uuid.Nil {
		ca.IncomeAccountID = findAccount(q, tenantID, orgID, "sales revenue", "4000")
	}
	if ca.ExpenseAccountID == uuid.Nil {
		ca.ExpenseAccountID = findAccount(q, tenantID, orgID, "cost of goods", "5000")
		if ca.ExpenseAccountID == uuid.Nil {
			ca.ExpenseAccountID = findAccount(q, tenantID, orgID, "cogs", "5000")
		}
	}
	if ca.StockValuationAccountID == uuid.Nil {
		ca.StockValuationAccountID = findAccount(q, tenantID, orgID, "inventory", "1300")
	}
	if ca.StockInputAccountID == uuid.Nil {
		ca.StockInputAccountID = findAccount(q, tenantID, orgID, "stock interim receipt", "2230")
		if ca.StockInputAccountID == uuid.Nil {
			ca.StockInputAccountID = findAccount(q, tenantID, orgID, "stock interim receipt", "2200")
		}
	}
	if ca.StockOutputAccountID == uuid.Nil {
		ca.StockOutputAccountID = findAccount(q, tenantID, orgID, "stock interim delivery", "2231")
		if ca.StockOutputAccountID == uuid.Nil {
			ca.StockOutputAccountID = findAccount(q, tenantID, orgID, "stock interim delivery", "2201")
		}
	}
	return ca
}

// getInventoryAccountByType returns the GL account based on a product's inventory_type.
// raw → 1310, trade → 1340, finished → 1330, service → uuid.Nil
func getInventoryAccountByType(q dbQuerier, tenantID uuid.UUID, orgID *uuid.UUID, productID uuid.UUID) uuid.UUID {
	var inventoryType string
	_ = q.QueryRow(
		`SELECT COALESCE(inventory_type, 'trade') FROM products WHERE id = $1`,
		productID,
	).Scan(&inventoryType)

	switch inventoryType {
	case "raw":
		return findAccount(q, tenantID, orgID, "raw materials", "1310")
	case "finished":
		return findAccount(q, tenantID, orgID, "finished goods", "1330")
	case "trade":
		return findAccount(q, tenantID, orgID, "goods for resale", "1340")
	case "service":
		return uuid.Nil
	default:
		return findAccount(q, tenantID, orgID, "goods for resale", "1340")
	}
}
