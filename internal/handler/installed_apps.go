package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// INSTALLED APPS HANDLERS
// =====================================================

// ListInstalledApps returns all installed apps for the current tenant
func (h *Handler) ListInstalledApps(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, app_id, app_name, app_description, app_version,
		       app_icon, app_color, status, installed_by, installed_date, updated_date, settings
		FROM tenant_installed_apps
		WHERE tenant_id = $1
		ORDER BY installed_date DESC
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to query installed apps", "error", err)
		response.InternalError(c, "Failed to query installed apps")
		return
	}
	defer rows.Close()

	apps := []entity.TenantInstalledApp{}
	for rows.Next() {
		var app entity.TenantInstalledApp
		if err := rows.Scan(
			&app.ID, &app.TenantID, &app.AppID, &app.AppName, &app.AppDescription,
			&app.AppVersion, &app.AppIcon, &app.AppColor, &app.Status,
			&app.InstalledBy, &app.InstalledDate, &app.UpdatedDate, &app.Settings,
		); err != nil {
			h.log.Error("Failed to scan installed app", "error", err)
			continue
		}
		apps = append(apps, app)
	}

	response.Success(c, apps)
}

// InstallApp installs a new app for the current tenant
func (h *Handler) InstallApp(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req entity.InstallAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Check if app is already installed
	var existingID int64
	checkQuery := `SELECT id FROM tenant_installed_apps WHERE tenant_id = $1 AND app_id = $2`
	err := h.db.QueryRow(checkQuery, tenantID, req.AppID).Scan(&existingID)
	if err == nil {
		// App already exists, update it to active
		updateQuery := `
			UPDATE tenant_installed_apps
			SET status = 'active', updated_date = NOW()
			WHERE id = $1
			RETURNING id
		`
		h.db.QueryRow(updateQuery, existingID).Scan(&existingID)
		response.Success(c, gin.H{"id": existingID, "message": "App reactivated"})
		return
	}

	// Insert new app
	query := `
		INSERT INTO tenant_installed_apps (
			tenant_id, app_id, app_name, app_description, app_version,
			app_icon, app_color, status, installed_by, installed_date, updated_date, settings
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, NOW(), NOW(), '{}')
		RETURNING id
	`

	var installedByUUID *uuid.UUID
	if userID != uuid.Nil {
		installedByUUID = &userID
	}

	var appID int64
	err = h.db.QueryRow(
		query, tenantID, req.AppID, req.AppName,
		sql.NullString{String: req.AppDescription, Valid: req.AppDescription != ""},
		req.AppVersion,
		sql.NullString{String: req.AppIcon, Valid: req.AppIcon != ""},
		sql.NullString{String: req.AppColor, Valid: req.AppColor != ""},
		installedByUUID,
	).Scan(&appID)

	if err != nil {
		h.log.Error("Failed to install app", "error", err)
		response.InternalError(c, "Failed to install app")
		return
	}

	response.Created(c, gin.H{"id": appID, "message": "App installed successfully"})
}

// UninstallApp removes an app for the current tenant
func (h *Handler) UninstallApp(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	appID := c.Param("app_id")
	if appID == "" {
		response.BadRequest(c, "App ID is required")
		return
	}

	// Soft delete by setting status to inactive
	query := `
		UPDATE tenant_installed_apps
		SET status = 'inactive', updated_date = NOW()
		WHERE tenant_id = $1 AND app_id = $2
	`

	result, err := h.db.Exec(query, tenantID, appID)
	if err != nil {
		h.log.Error("Failed to uninstall app", "error", err)
		response.InternalError(c, "Failed to uninstall app")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "App not found")
		return
	}

	response.Success(c, gin.H{"message": "App uninstalled successfully"})
}

// UpdateInstalledApp updates settings for an installed app
func (h *Handler) UpdateInstalledApp(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	appID := c.Param("app_id")
	if appID == "" {
		response.BadRequest(c, "App ID is required")
		return
	}

	var req entity.UpdateInstalledAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Build update query
	query := `
		UPDATE tenant_installed_apps
		SET updated_date = NOW()
	`
	args := []interface{}{}
	argCount := 0

	if req.Status != "" {
		argCount++
		query += fmt.Sprintf(", status = $%d", argCount)
		args = append(args, req.Status)
	}

	if req.Settings != nil {
		argCount++
		settingsJSON, _ := json.Marshal(req.Settings)
		query += fmt.Sprintf(", settings = $%d", argCount)
		args = append(args, settingsJSON)
	}

	argCount++
	query += fmt.Sprintf(" WHERE tenant_id = $%d", argCount)
	args = append(args, tenantID)

	argCount++
	query += fmt.Sprintf(" AND app_id = $%d", argCount)
	args = append(args, appID)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update installed app", "error", err)
		response.InternalError(c, "Failed to update installed app")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "App not found")
		return
	}

	response.Success(c, gin.H{"message": "App updated successfully"})
}

// GetInstalledApp returns a single installed app
func (h *Handler) GetInstalledApp(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	appID := c.Param("app_id")
	if appID == "" {
		response.BadRequest(c, "App ID is required")
		return
	}

	query := `
		SELECT id, tenant_id, app_id, app_name, app_description, app_version,
		       app_icon, app_color, status, installed_by, installed_date, updated_date, settings
		FROM tenant_installed_apps
		WHERE tenant_id = $1 AND app_id = $2
	`

	var app entity.TenantInstalledApp
	err := h.db.QueryRow(query, tenantID, appID).Scan(
		&app.ID, &app.TenantID, &app.AppID, &app.AppName, &app.AppDescription,
		&app.AppVersion, &app.AppIcon, &app.AppColor, &app.Status,
		&app.InstalledBy, &app.InstalledDate, &app.UpdatedDate, &app.Settings,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "App not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get installed app", "error", err)
		response.InternalError(c, "Failed to get installed app")
		return
	}

	response.Success(c, app)
}
