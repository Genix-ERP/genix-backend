package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Organization represents a company/organization within a tenant
type Organization struct {
	ID                 uuid.UUID              `json:"id"`
	TenantID           uuid.UUID              `json:"tenant_id"`
	ParentID           *uuid.UUID             `json:"parent_id,omitempty"`
	Code               string                 `json:"code"`
	Name               string                 `json:"name"`
	Type               string                 `json:"type"`
	TaxID              *string                `json:"tax_id,omitempty"`
	RegistrationNumber *string                `json:"registration_number,omitempty"`
	Address            map[string]interface{} `json:"address,omitempty"`
	ContactInfo        map[string]interface{} `json:"contact_info,omitempty"`
	Country            *string                `json:"country,omitempty"`
	Currency           *string                `json:"currency,omitempty"`
	AccountingStandard *string                `json:"accounting_standard,omitempty"`
	LogoURL            *string                `json:"logo_url,omitempty"`
	Settings           map[string]interface{} `json:"settings,omitempty"`
	IsActive           bool                   `json:"is_active"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

// CreateOrganizationInput represents the input for creating an organization
type CreateOrganizationInput struct {
	Code               string                 `json:"code" binding:"required,min=2,max=50"`
	Name               string                 `json:"name" binding:"required,min=2,max=255"`
	Type               string                 `json:"type"`
	ParentID           *string                `json:"parent_id,omitempty"`
	TaxID              *string                `json:"tax_id,omitempty"`
	RegistrationNumber *string                `json:"registration_number,omitempty"`
	Address            map[string]interface{} `json:"address,omitempty"`
	ContactInfo        map[string]interface{} `json:"contact_info,omitempty"`
	Country            *string                `json:"country,omitempty"`
	Currency           *string                `json:"currency,omitempty"`
	AccountingStandard *string                `json:"accounting_standard,omitempty"`
	LogoURL            *string                `json:"logo_url,omitempty"`
}

// UpdateOrganizationInput represents the input for updating an organization
type UpdateOrganizationInput struct {
	Code               *string                `json:"code,omitempty"`
	Name               *string                `json:"name,omitempty"`
	Type               *string                `json:"type,omitempty"`
	ParentID           *string                `json:"parent_id,omitempty"`
	TaxID              *string                `json:"tax_id,omitempty"`
	RegistrationNumber *string                `json:"registration_number,omitempty"`
	Address            map[string]interface{} `json:"address,omitempty"`
	ContactInfo        map[string]interface{} `json:"contact_info,omitempty"`
	Country            *string                `json:"country,omitempty"`
	Currency           *string                `json:"currency,omitempty"`
	AccountingStandard *string                `json:"accounting_standard,omitempty"`
	LogoURL            *string                `json:"logo_url,omitempty"`
	IsActive           *bool                  `json:"is_active,omitempty"`
}

// ListOrganizations returns all organizations for the current tenant
func (h *Handler) ListOrganizations(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
		       address, contact_info, country, currency, accounting_standard, logo_url,
		       settings, is_active, created_at, updated_at
		FROM organizations
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY name ASC
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to list organizations", "error", err)
		response.InternalServerError(c, "Failed to list organizations")
		return
	}
	defer rows.Close()

	var organizations []Organization
	for rows.Next() {
		var org Organization
		var addressJSON, contactInfoJSON, settingsJSON []byte

		err := rows.Scan(
			&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
			&org.TaxID, &org.RegistrationNumber, &addressJSON, &contactInfoJSON,
			&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
			&settingsJSON, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan organization", "error", err)
			continue
		}

		// Parse JSONB fields
		if len(addressJSON) > 0 {
			json.Unmarshal(addressJSON, &org.Address)
		}
		if len(contactInfoJSON) > 0 {
			json.Unmarshal(contactInfoJSON, &org.ContactInfo)
		}
		if len(settingsJSON) > 0 {
			json.Unmarshal(settingsJSON, &org.Settings)
		}

		organizations = append(organizations, org)
	}

	if organizations == nil {
		organizations = []Organization{}
	}

	response.Success(c, organizations)
}

// CreateOrganization creates a new organization
func (h *Handler) CreateOrganization(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input CreateOrganizationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check for duplicate code within tenant
	var existingCount int
	err := h.db.QueryRow(
		"SELECT COUNT(*) FROM organizations WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL",
		tenantID, input.Code,
	).Scan(&existingCount)
	if err != nil {
		h.log.Error("Failed to check duplicate code", "error", err)
		response.InternalServerError(c, "Failed to create organization")
		return
	}
	if existingCount > 0 {
		response.Conflict(c, "Organization with this code already exists")
		return
	}

	// Set default type if not provided
	orgType := input.Type
	if orgType == "" {
		orgType = "company"
	}

	// Parse parent ID if provided
	var parentID *uuid.UUID
	if input.ParentID != nil && *input.ParentID != "" {
		parsed, err := uuid.Parse(*input.ParentID)
		if err == nil {
			parentID = &parsed
		}
	}

	// Convert maps to JSON for JSONB columns (default to empty object)
	addressJSON := []byte("{}")
	contactInfoJSON := []byte("{}")
	if input.Address != nil {
		addressJSON, _ = json.Marshal(input.Address)
	}
	if input.ContactInfo != nil {
		contactInfoJSON, _ = json.Marshal(input.ContactInfo)
	}

	orgID := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO organizations (
			id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
			address, contact_info, country, currency, accounting_standard, logo_url,
			settings, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
		          address, contact_info, country, currency, accounting_standard, logo_url,
		          settings, is_active, created_at, updated_at
	`

	var org Organization
	var addressJSONOut, contactInfoJSONOut, settingsJSONOut []byte

	err = h.db.QueryRow(
		query,
		orgID, tenantID, parentID, input.Code, input.Name, orgType,
		input.TaxID, input.RegistrationNumber, addressJSON, contactInfoJSON,
		input.Country, input.Currency, input.AccountingStandard, input.LogoURL,
		[]byte("{}"), true, now, now,
	).Scan(
		&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
		&org.TaxID, &org.RegistrationNumber, &addressJSONOut, &contactInfoJSONOut,
		&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
		&settingsJSONOut, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
	)
	if err != nil {
		h.log.Error("Failed to create organization", "error", err)
		response.InternalServerError(c, "Failed to create organization")
		return
	}

	// Parse JSONB fields
	if len(addressJSONOut) > 0 {
		json.Unmarshal(addressJSONOut, &org.Address)
	}
	if len(contactInfoJSONOut) > 0 {
		json.Unmarshal(contactInfoJSONOut, &org.ContactInfo)
	}
	if len(settingsJSONOut) > 0 {
		json.Unmarshal(settingsJSONOut, &org.Settings)
	}

	response.Created(c, org)
}

// GetOrganization returns a single organization by ID
func (h *Handler) GetOrganization(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid organization ID")
		return
	}

	query := `
		SELECT id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
		       address, contact_info, country, currency, accounting_standard, logo_url,
		       settings, is_active, created_at, updated_at
		FROM organizations
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var org Organization
	var addressJSON, contactInfoJSON, settingsJSON []byte

	err = h.db.QueryRow(query, orgID, tenantID).Scan(
		&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
		&org.TaxID, &org.RegistrationNumber, &addressJSON, &contactInfoJSON,
		&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
		&settingsJSON, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Organization")
		return
	}
	if err != nil {
		h.log.Error("Failed to get organization", "error", err)
		response.InternalServerError(c, "Failed to get organization")
		return
	}

	// Parse JSONB fields
	if len(addressJSON) > 0 {
		json.Unmarshal(addressJSON, &org.Address)
	}
	if len(contactInfoJSON) > 0 {
		json.Unmarshal(contactInfoJSON, &org.ContactInfo)
	}
	if len(settingsJSON) > 0 {
		json.Unmarshal(settingsJSON, &org.Settings)
	}

	response.Success(c, org)
}

// UpdateOrganization updates an existing organization
func (h *Handler) UpdateOrganization(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid organization ID")
		return
	}

	var input UpdateOrganizationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if organization exists
	var existingCode string
	err = h.db.QueryRow(
		"SELECT code FROM organizations WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		orgID, tenantID,
	).Scan(&existingCode)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Organization")
		return
	}
	if err != nil {
		h.log.Error("Failed to find organization", "error", err)
		response.InternalServerError(c, "Failed to update organization")
		return
	}

	// Check for duplicate code if code is being updated
	if input.Code != nil && *input.Code != existingCode {
		var existingCount int
		err := h.db.QueryRow(
			"SELECT COUNT(*) FROM organizations WHERE tenant_id = $1 AND code = $2 AND id != $3 AND deleted_at IS NULL",
			tenantID, *input.Code, orgID,
		).Scan(&existingCount)
		if err != nil {
			h.log.Error("Failed to check duplicate code", "error", err)
			response.InternalServerError(c, "Failed to update organization")
			return
		}
		if existingCount > 0 {
			response.Conflict(c, "Organization with this code already exists")
			return
		}
	}

	// Build update query dynamically
	query := `UPDATE organizations SET updated_at = $1`
	args := []interface{}{time.Now()}
	argIndex := 2

	if input.Code != nil {
		query += fmt.Sprintf(", code = $%d", argIndex)
		args = append(args, *input.Code)
		argIndex++
	}
	if input.Name != nil {
		query += fmt.Sprintf(", name = $%d", argIndex)
		args = append(args, *input.Name)
		argIndex++
	}
	if input.Type != nil {
		query += fmt.Sprintf(", type = $%d", argIndex)
		args = append(args, *input.Type)
		argIndex++
	}
	if input.TaxID != nil {
		query += fmt.Sprintf(", tax_id = $%d", argIndex)
		args = append(args, *input.TaxID)
		argIndex++
	}
	if input.RegistrationNumber != nil {
		query += fmt.Sprintf(", registration_number = $%d", argIndex)
		args = append(args, *input.RegistrationNumber)
		argIndex++
	}
	if input.Address != nil {
		addressJSON, _ := json.Marshal(input.Address)
		query += fmt.Sprintf(", address = $%d", argIndex)
		args = append(args, addressJSON)
		argIndex++
	}
	if input.ContactInfo != nil {
		contactInfoJSON, _ := json.Marshal(input.ContactInfo)
		query += fmt.Sprintf(", contact_info = $%d", argIndex)
		args = append(args, contactInfoJSON)
		argIndex++
	}
	if input.Country != nil {
		query += fmt.Sprintf(", country = $%d", argIndex)
		args = append(args, *input.Country)
		argIndex++
	}
	if input.Currency != nil {
		query += fmt.Sprintf(", currency = $%d", argIndex)
		args = append(args, *input.Currency)
		argIndex++
	}
	if input.AccountingStandard != nil {
		query += fmt.Sprintf(", accounting_standard = $%d", argIndex)
		args = append(args, *input.AccountingStandard)
		argIndex++
	}
	if input.LogoURL != nil {
		query += fmt.Sprintf(", logo_url = $%d", argIndex)
		args = append(args, *input.LogoURL)
		argIndex++
	}
	if input.IsActive != nil {
		query += fmt.Sprintf(", is_active = $%d", argIndex)
		args = append(args, *input.IsActive)
		argIndex++
	}

	query += fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL", argIndex, argIndex+1)
	args = append(args, orgID, tenantID)

	query += ` RETURNING id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
	           address, contact_info, country, currency, accounting_standard, logo_url,
	           settings, is_active, created_at, updated_at`

	var org Organization
	var addressJSON, contactInfoJSON, settingsJSON []byte

	err = h.db.QueryRow(query, args...).Scan(
		&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
		&org.TaxID, &org.RegistrationNumber, &addressJSON, &contactInfoJSON,
		&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
		&settingsJSON, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
	)
	if err != nil {
		h.log.Error("Failed to update organization", "error", err)
		response.InternalServerError(c, "Failed to update organization")
		return
	}

	// Parse JSONB fields
	if len(addressJSON) > 0 {
		json.Unmarshal(addressJSON, &org.Address)
	}
	if len(contactInfoJSON) > 0 {
		json.Unmarshal(contactInfoJSON, &org.ContactInfo)
	}
	if len(settingsJSON) > 0 {
		json.Unmarshal(settingsJSON, &org.Settings)
	}

	response.Success(c, org)
}

// DeleteOrganization soft-deletes an organization
func (h *Handler) DeleteOrganization(c *gin.Context) {
	tenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid organization ID")
		return
	}

	// Check if this is the last organization - don't allow deleting the last one
	var orgCount int
	err = h.db.QueryRow(
		"SELECT COUNT(*) FROM organizations WHERE tenant_id = $1 AND deleted_at IS NULL",
		tenantID,
	).Scan(&orgCount)
	if err != nil {
		h.log.Error("Failed to count organizations", "error", err)
		response.InternalServerError(c, "Failed to delete organization")
		return
	}
	if orgCount <= 1 {
		response.BadRequest(c, "Cannot delete the last organization")
		return
	}

	// Soft delete the organization
	result, err := h.db.Exec(
		"UPDATE organizations SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), orgID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete organization", "error", err)
		response.InternalServerError(c, "Failed to delete organization")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Organization")
		return
	}

	c.JSON(http.StatusNoContent, nil)
}
