package handler

import (
	"database/sql"
	"encoding/json"
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
		response.BadRequest(c, "Invalid input: "+err.Error())
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
		response.BadRequest(c, "Invalid input: "+err.Error())
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
