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
	"github.com/lib/pq"
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
	// Extended fields for Uzbekistan business requirements
	OKED                  *string `json:"oked,omitempty"`
	BankAccount           *string `json:"bank_account,omitempty"`
	BankMFO               *string `json:"bank_mfo,omitempty"`
	BankName              *string `json:"bank_name,omitempty"`
	IsVATPayer            bool    `json:"is_vat_payer"`
	TaxRegime             *string `json:"tax_regime,omitempty"`
	ActivityStatus        *string `json:"activity_status,omitempty"`
	BusinessGroup         *string `json:"business_group,omitempty"`
	IntercompanyRelations *string `json:"intercompany_relations,omitempty"`
	DirectorName          *string  `json:"director_name,omitempty"`
	DirectorPhone         *string  `json:"director_phone,omitempty"`
	LegalAddress          *string  `json:"legal_address,omitempty"`
	Notes                 *string  `json:"notes,omitempty"`
	IntercompanyVendorIDs []string `json:"intercompany_vendor_ids,omitempty"`
	// Per-org sidebar visibility override (migration 386).
	// If an app_id is in this list, the app is hidden from the sidebar
	// when this organization is the active company. Empty by default.
	HiddenApps []string `json:"hidden_apps"`
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
	// Extended fields
	OKED                  *string `json:"oked,omitempty"`
	BankAccount           *string `json:"bank_account,omitempty"`
	BankMFO               *string `json:"bank_mfo,omitempty"`
	BankName              *string `json:"bank_name,omitempty"`
	IsVATPayer            *bool   `json:"is_vat_payer,omitempty"`
	TaxRegime             *string `json:"tax_regime,omitempty"`
	ActivityStatus        *string `json:"activity_status,omitempty"`
	BusinessGroup         *string `json:"business_group,omitempty"`
	IntercompanyRelations *string `json:"intercompany_relations,omitempty"`
	DirectorName          *string `json:"director_name,omitempty"`
	DirectorPhone         *string `json:"director_phone,omitempty"`
	LegalAddress          *string `json:"legal_address,omitempty"`
	Notes                 *string `json:"notes,omitempty"`
	// Intercompany vendoring: create vendor+customer contacts in these org IDs
	IntercompanyVendorIDs []string `json:"intercompany_vendor_ids,omitempty"`
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
	// Extended fields
	OKED                  *string `json:"oked,omitempty"`
	BankAccount           *string `json:"bank_account,omitempty"`
	BankMFO               *string `json:"bank_mfo,omitempty"`
	BankName              *string `json:"bank_name,omitempty"`
	IsVATPayer            *bool   `json:"is_vat_payer,omitempty"`
	TaxRegime             *string `json:"tax_regime,omitempty"`
	ActivityStatus        *string `json:"activity_status,omitempty"`
	BusinessGroup         *string `json:"business_group,omitempty"`
	IntercompanyRelations *string `json:"intercompany_relations,omitempty"`
	DirectorName          *string `json:"director_name,omitempty"`
	DirectorPhone         *string `json:"director_phone,omitempty"`
	LegalAddress          *string `json:"legal_address,omitempty"`
	Notes                 *string `json:"notes,omitempty"`
	// Intercompany vendoring: create vendor+customer contacts in these org IDs
	IntercompanyVendorIDs []string `json:"intercompany_vendor_ids,omitempty"`
	// Per-org sidebar visibility override (migration 386). Pointer so
	// "absent in payload" is distinguishable from "explicitly empty".
	HiddenApps *[]string `json:"hidden_apps,omitempty"`
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
		       settings, is_active, created_at, updated_at,
		       oked, bank_account, bank_mfo, bank_name, COALESCE(is_vat_payer, false),
		       tax_regime, activity_status, business_group, intercompany_relations,
		       director_name, director_phone, legal_address, notes,
		       COALESCE(hidden_apps, '{}'::text[])
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
		var hiddenApps pq.StringArray

		err := rows.Scan(
			&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
			&org.TaxID, &org.RegistrationNumber, &addressJSON, &contactInfoJSON,
			&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
			&settingsJSON, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
			&org.OKED, &org.BankAccount, &org.BankMFO, &org.BankName, &org.IsVATPayer,
			&org.TaxRegime, &org.ActivityStatus, &org.BusinessGroup, &org.IntercompanyRelations,
			&org.DirectorName, &org.DirectorPhone, &org.LegalAddress, &org.Notes,
			&hiddenApps,
		)
		org.HiddenApps = []string(hiddenApps)
		if org.HiddenApps == nil {
			org.HiddenApps = []string{}
		}
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

	// Build intercompany vendor lookup using source_organization_id (with name fallback for old data)
	if len(organizations) > 1 {
		orgNameToID := make(map[string]string)
		for _, org := range organizations {
			orgNameToID[org.Name] = org.ID.String()
		}

		for i, org := range organizations {
			vendorRows, err := h.db.Query(
				`SELECT source_organization_id, name FROM contacts WHERE tenant_id = $1 AND organization_id = $2 AND type = 'vendor' AND deleted_at IS NULL`,
				tenantID, org.ID,
			)
			if err != nil {
				continue
			}
			seen := make(map[string]bool)
			var vendorIDs []string
			for vendorRows.Next() {
				var sourceOrgID *uuid.UUID
				var vendorName string
				if err := vendorRows.Scan(&sourceOrgID, &vendorName); err != nil {
					continue
				}
				var matchedID string
				if sourceOrgID != nil {
					matchedID = sourceOrgID.String()
				} else if id, ok := orgNameToID[vendorName]; ok && id != org.ID.String() {
					matchedID = id
				}
				if matchedID != "" && !seen[matchedID] {
					seen[matchedID] = true
					vendorIDs = append(vendorIDs, matchedID)
				}
			}
			vendorRows.Close()
			if len(vendorIDs) > 0 {
				organizations[i].IntercompanyVendorIDs = vendorIDs
			}
		}
	}

	response.Success(c, organizations)
}

// CreateOrganization creates a new organization
func (h *Handler) CreateOrganization(c *gin.Context) {
	tenantIDStr, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse tenant ID - it comes as string from middleware
	tenantID, err := uuid.Parse(tenantIDStr.(string))
	if err != nil {
		response.BadRequest(c, "Invalid tenant ID")
		return
	}

	var input CreateOrganizationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check for duplicate code within tenant
	var existingCount int
	err = h.db.QueryRow(
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

	// Set default VAT payer status
	isVATPayer := false
	if input.IsVATPayer != nil {
		isVATPayer = *input.IsVATPayer
	}

	query := `
		INSERT INTO organizations (
			id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
			address, contact_info, country, currency, accounting_standard, logo_url,
			settings, is_active, created_at, updated_at,
			oked, bank_account, bank_mfo, bank_name, is_vat_payer,
			tax_regime, activity_status, business_group, intercompany_relations,
			director_name, director_phone, legal_address, notes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
		          $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31)
		RETURNING id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
		          address, contact_info, country, currency, accounting_standard, logo_url,
		          settings, is_active, created_at, updated_at,
		          oked, bank_account, bank_mfo, bank_name, COALESCE(is_vat_payer, false),
		          tax_regime, activity_status, business_group, intercompany_relations,
		          director_name, director_phone, legal_address, notes
	`

	var org Organization
	var addressJSONOut, contactInfoJSONOut, settingsJSONOut []byte

	err = h.db.QueryRow(
		query,
		orgID, tenantID, parentID, input.Code, input.Name, orgType,
		input.TaxID, input.RegistrationNumber, addressJSON, contactInfoJSON,
		input.Country, input.Currency, input.AccountingStandard, input.LogoURL,
		[]byte("{}"), true, now, now,
		input.OKED, input.BankAccount, input.BankMFO, input.BankName, isVATPayer,
		input.TaxRegime, input.ActivityStatus, input.BusinessGroup, input.IntercompanyRelations,
		input.DirectorName, input.DirectorPhone, input.LegalAddress, input.Notes,
	).Scan(
		&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
		&org.TaxID, &org.RegistrationNumber, &addressJSONOut, &contactInfoJSONOut,
		&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
		&settingsJSONOut, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
		&org.OKED, &org.BankAccount, &org.BankMFO, &org.BankName, &org.IsVATPayer,
		&org.TaxRegime, &org.ActivityStatus, &org.BusinessGroup, &org.IntercompanyRelations,
		&org.DirectorName, &org.DirectorPhone, &org.LegalAddress, &org.Notes,
	)
	if err != nil {
		h.log.Error("Failed to create organization", "error", err)
		response.InternalServerError(c, "Failed to create organization")
		return
	}
	// New orgs always start with no hidden apps. Setting explicitly so the
	// JSON response includes [] instead of null.
	org.HiddenApps = []string{}

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

	// Create default Chart of Accounts for the new organization
	if err := h.createDefaultChartOfAccounts(tenantID, orgID); err != nil {
		h.log.Error("Failed to create default chart of accounts", "error", err, "org_id", orgID)
		// Don't fail the organization creation, just log the error
	}

	// Create default Journals for the new organization
	if err := h.createDefaultJournals(tenantID, orgID); err != nil {
		h.log.Error("Failed to create default journals", "error", err, "org_id", orgID)
		// Don't fail the organization creation, just log the error
	}

	// Create default Warehouse for the new organization
	if err := h.createDefaultWarehouse(tenantID, orgID, input.Code); err != nil {
		h.log.Error("Failed to create default warehouse", "error", err, "org_id", orgID)
		// Don't fail the organization creation, just log the error
	}

	// Backfill product_organization_settings so existing products are visible in the new org
	if err := h.backfillProductOrgSettings(tenantID, orgID); err != nil {
		h.log.Error("Failed to backfill product org settings", "error", err, "org_id", orgID)
		// Don't fail the organization creation, just log the error
	}

	// Create intercompany vendor/customer contacts in selected organizations
	if len(input.IntercompanyVendorIDs) > 0 {
		h.createIntercompanyContacts(tenantID, orgID, input.Name, input.TaxID, input.IntercompanyVendorIDs)
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
		       settings, is_active, created_at, updated_at,
		       oked, bank_account, bank_mfo, bank_name, COALESCE(is_vat_payer, false),
		       tax_regime, activity_status, business_group, intercompany_relations,
		       director_name, director_phone, legal_address, notes,
		       COALESCE(hidden_apps, '{}'::text[])
		FROM organizations
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var org Organization
	var addressJSON, contactInfoJSON, settingsJSON []byte
	var hiddenApps pq.StringArray

	err = h.db.QueryRow(query, orgID, tenantID).Scan(
		&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
		&org.TaxID, &org.RegistrationNumber, &addressJSON, &contactInfoJSON,
		&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
		&settingsJSON, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
		&org.OKED, &org.BankAccount, &org.BankMFO, &org.BankName, &org.IsVATPayer,
		&org.TaxRegime, &org.ActivityStatus, &org.BusinessGroup, &org.IntercompanyRelations,
		&org.DirectorName, &org.DirectorPhone, &org.LegalAddress, &org.Notes,
		&hiddenApps,
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
	org.HiddenApps = []string(hiddenApps)
	if org.HiddenApps == nil {
		org.HiddenApps = []string{}
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
	// Extended fields
	if input.OKED != nil {
		query += fmt.Sprintf(", oked = $%d", argIndex)
		args = append(args, *input.OKED)
		argIndex++
	}
	if input.BankAccount != nil {
		query += fmt.Sprintf(", bank_account = $%d", argIndex)
		args = append(args, *input.BankAccount)
		argIndex++
	}
	if input.BankMFO != nil {
		query += fmt.Sprintf(", bank_mfo = $%d", argIndex)
		args = append(args, *input.BankMFO)
		argIndex++
	}
	if input.BankName != nil {
		query += fmt.Sprintf(", bank_name = $%d", argIndex)
		args = append(args, *input.BankName)
		argIndex++
	}
	if input.IsVATPayer != nil {
		query += fmt.Sprintf(", is_vat_payer = $%d", argIndex)
		args = append(args, *input.IsVATPayer)
		argIndex++
	}
	if input.TaxRegime != nil {
		query += fmt.Sprintf(", tax_regime = $%d", argIndex)
		args = append(args, *input.TaxRegime)
		argIndex++
	}
	if input.ActivityStatus != nil {
		query += fmt.Sprintf(", activity_status = $%d", argIndex)
		args = append(args, *input.ActivityStatus)
		argIndex++
	}
	if input.BusinessGroup != nil {
		query += fmt.Sprintf(", business_group = $%d", argIndex)
		args = append(args, *input.BusinessGroup)
		argIndex++
	}
	if input.IntercompanyRelations != nil {
		query += fmt.Sprintf(", intercompany_relations = $%d", argIndex)
		args = append(args, *input.IntercompanyRelations)
		argIndex++
	}
	if input.DirectorName != nil {
		query += fmt.Sprintf(", director_name = $%d", argIndex)
		args = append(args, *input.DirectorName)
		argIndex++
	}
	if input.DirectorPhone != nil {
		query += fmt.Sprintf(", director_phone = $%d", argIndex)
		args = append(args, *input.DirectorPhone)
		argIndex++
	}
	if input.LegalAddress != nil {
		query += fmt.Sprintf(", legal_address = $%d", argIndex)
		args = append(args, *input.LegalAddress)
		argIndex++
	}
	if input.Notes != nil {
		query += fmt.Sprintf(", notes = $%d", argIndex)
		args = append(args, *input.Notes)
		argIndex++
	}
	if input.HiddenApps != nil {
		// Per-org sidebar visibility list (migration 386). pq.StringArray
		// marshals []string to PostgreSQL text[] correctly; passing the
		// raw slice would land as a single quoted string.
		query += fmt.Sprintf(", hidden_apps = $%d", argIndex)
		args = append(args, pq.StringArray(*input.HiddenApps))
		argIndex++
	}

	query += fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL", argIndex, argIndex+1)
	args = append(args, orgID, tenantID)

	query += ` RETURNING id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
	           address, contact_info, country, currency, accounting_standard, logo_url,
	           settings, is_active, created_at, updated_at,
	           oked, bank_account, bank_mfo, bank_name, COALESCE(is_vat_payer, false),
	           tax_regime, activity_status, business_group, intercompany_relations,
	           director_name, director_phone, legal_address, notes,
	           COALESCE(hidden_apps, '{}'::text[])`

	var org Organization
	var addressJSON, contactInfoJSON, settingsJSON []byte
	var hiddenApps pq.StringArray

	err = h.db.QueryRow(query, args...).Scan(
		&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
		&org.TaxID, &org.RegistrationNumber, &addressJSON, &contactInfoJSON,
		&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
		&settingsJSON, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
		&org.OKED, &org.BankAccount, &org.BankMFO, &org.BankName, &org.IsVATPayer,
		&org.TaxRegime, &org.ActivityStatus, &org.BusinessGroup, &org.IntercompanyRelations,
		&org.DirectorName, &org.DirectorPhone, &org.LegalAddress, &org.Notes,
		&hiddenApps,
	)
	if err != nil {
		h.log.Error("Failed to update organization", "error", err)
		response.InternalServerError(c, "Failed to update organization")
		return
	}
	org.HiddenApps = []string(hiddenApps)
	if org.HiddenApps == nil {
		org.HiddenApps = []string{}
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

	// Sync intercompany vendor/customer contacts
	if input.IntercompanyVendorIDs != nil {
		if parsedTenantID, err := uuid.Parse(tenantID.(string)); err == nil {
			// Create or update contacts for selected companies
			if len(input.IntercompanyVendorIDs) > 0 {
				h.createIntercompanyContacts(parsedTenantID, orgID, org.Name, org.TaxID, input.IntercompanyVendorIDs)
			}

			// Remove contacts for unselected companies
			// Build set of selected org IDs and their names for matching
			selectedSet := make(map[string]bool)
			selectedNames := make(map[string]bool)
			for _, id := range input.IntercompanyVendorIDs {
				selectedSet[id] = true
				// Get org name for this ID
				var name string
				if err := h.db.QueryRow(`SELECT name FROM organizations WHERE id = $1 AND tenant_id = $2`, id, parsedTenantID).Scan(&name); err == nil {
					selectedNames[name] = true
				}
			}

			// Get all vendor contacts for this org
			rows, err := h.db.Query(`SELECT id, source_organization_id, name FROM contacts WHERE tenant_id = $1 AND organization_id = $2 AND type = 'vendor' AND deleted_at IS NULL`,
				parsedTenantID, orgID)
			if err == nil {
				for rows.Next() {
					var contactID uuid.UUID
					var sourceOrgID *uuid.UUID
					var contactName string
					if err := rows.Scan(&contactID, &sourceOrgID, &contactName); err != nil {
						continue
					}

					shouldKeep := false
					var matchedSourceOrgID *uuid.UUID

					if sourceOrgID != nil {
						// New-style: check by source_organization_id
						if selectedSet[sourceOrgID.String()] {
							shouldKeep = true
						}
						matchedSourceOrgID = sourceOrgID
					} else if selectedNames[contactName] {
						// Old-style (no source_organization_id): check by name match
						shouldKeep = true
					}

					if !shouldKeep {
						// Soft-delete this vendor contact
						h.db.Exec(`UPDATE contacts SET deleted_at = NOW() WHERE id = $1`, contactID)
						// Also soft-delete the corresponding customer contact
						if matchedSourceOrgID != nil {
							h.db.Exec(`UPDATE contacts SET deleted_at = NOW() WHERE tenant_id = $1 AND organization_id = $2 AND type = 'customer' AND source_organization_id = $3 AND deleted_at IS NULL`,
								parsedTenantID, *matchedSourceOrgID, orgID)
						} else {
							// Old-style: delete customer by name match
							h.db.Exec(`UPDATE contacts SET deleted_at = NOW() WHERE tenant_id = $1 AND type = 'customer' AND name = $2 AND source_organization_id IS NULL AND deleted_at IS NULL`,
								parsedTenantID, org.Name)
						}
					}
				}
				rows.Close()
			}
		}
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

// InitializeOrganizationAccounts creates default chart of accounts and journals for an existing organization
// POST /api/v1/organizations/:id/initialize-accounts
func (h *Handler) InitializeOrganizationAccounts(c *gin.Context) {
	tenantIDStr, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse tenant ID - it comes as string from middleware
	tenantID, err := uuid.Parse(tenantIDStr.(string))
	if err != nil {
		response.BadRequest(c, "Invalid tenant ID")
		return
	}

	orgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid organization ID")
		return
	}

	// Check if organization exists
	var orgExists bool
	err = h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		orgID, tenantID,
	).Scan(&orgExists)
	if err != nil || !orgExists {
		response.NotFound(c, "Organization")
		return
	}

	// Check if accounts already exist
	var accountCount int
	err = h.db.QueryRow(
		"SELECT COUNT(*) FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL",
		tenantID, orgID,
	).Scan(&accountCount)
	if err != nil {
		h.log.Error("Failed to count accounts", "error", err)
		response.InternalServerError(c, "Failed to initialize accounts")
		return
	}

	if accountCount > 0 {
		response.BadRequest(c, fmt.Sprintf("Organization already has %d accounts. Delete existing accounts first or create accounts manually.", accountCount))
		return
	}

	// Create default Chart of Accounts
	if err := h.createDefaultChartOfAccounts(tenantID, orgID); err != nil {
		h.log.Error("Failed to create default chart of accounts", "error", err, "org_id", orgID)
		response.InternalServerError(c, "Failed to create chart of accounts")
		return
	}

	// Create default Journals
	if err := h.createDefaultJournals(tenantID, orgID); err != nil {
		h.log.Error("Failed to create default journals", "error", err, "org_id", orgID)
		response.InternalServerError(c, "Failed to create journals")
		return
	}

	response.Success(c, gin.H{
		"message": "Default chart of accounts and journals created successfully",
		"organization_id": orgID,
	})
}

// createDefaultChartOfAccounts creates a standard chart of accounts for a new organization
// This follows standard accounting practices similar to Odoo
func (h *Handler) createDefaultChartOfAccounts(tenantID, orgID uuid.UUID) error {
	now := time.Now()

	// Get account type IDs
	accountTypeIDs := make(map[string]uuid.UUID)
	rows, err := h.db.Query("SELECT id, code FROM account_types")
	if err != nil {
		return fmt.Errorf("failed to get account types: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			continue
		}
		accountTypeIDs[code] = id
	}

	// Define default accounts - following Uzbekistan NAS (lex.uz/acts/1357627)
	defaultAccounts := []struct {
		code        string
		name        string
		typeCode    string
		isBankAcc   bool
		isControl   bool
		isRecon     bool
		description string
	}{
		// Asosiy vositalar (01xx)
		{"0100", "Asosiy vositalar", "FA", false, false, false, "Asosiy vositalar (mol-mulk, zavod va jihozlar)"},
		{"0110", "Yer", "FA", false, false, false, "Yer uchastkasi"},
		{"0120", "Binolar, inshootlar va uzatuvchi moslamalar", "FA", false, false, false, "Binolar va inshootlar"},
		{"0130", "Mashina va asbob-uskunalar", "FA", false, false, false, "Mashina va uskunalar"},
		{"0140", "Mebel va ofis jihozlari", "FA", false, false, false, "Mebel va ofis jihozlari"},
		{"0150", "Kompyuter jihozlari va hisoblash texnikasi", "FA", false, false, false, "Kompyuter jihozlari"},
		{"0160", "Transport vositalari", "FA", false, false, false, "Transport vositalari"},

		// Eskirish (02xx)
		{"0200", "Asosiy vositalar eskirishi", "CONTRA_ASSET", false, false, false, "Yig'ilgan eskirish"},
		{"0220", "Bino va inshootlarning eskirishi", "CONTRA_ASSET", false, false, false, "Bino va inshootlar eskirishi"},
		{"0230", "Mashina va asbob-uskunalarning eskirishi", "CONTRA_ASSET", false, false, false, "Mashina uskunalar eskirishi"},
		{"0260", "Transport vositalarining eskirishi", "CONTRA_ASSET", false, false, false, "Transport eskirishi"},

		// Nomoddiy aktivlar (04xx)
		{"0400", "Nomoddiy aktivlar", "OA", false, false, false, "Nomoddiy aktivlar"},
		{"0490", "Nomoddiy aktivlar eskirishi", "CONTRA_ASSET", false, false, false, "Nomoddiy aktivlar amortizatsiyasi"},

		// Kapital qo'yilmalar (08xx)
		{"0810", "Tugallanmagan kapital qo'yilmalar", "OA", false, false, false, "Qurilish ishlari xarajatlari"},

		// Materiallar (10xx)
		{"1010", "Xom ashyo va materiallar", "INV", false, false, false, "Tovar-moddiy zaxiralar"},
		{"1030", "Yoqilg'i", "INV", false, false, false, "Yoqilg'i materiallari"},
		{"1050", "Ehtiyot qismlar", "INV", false, false, false, "Ehtiyot qismlar"},
		{"1060", "Qurilish materiallari", "INV", false, false, false, "Qurilish materiallari"},
		{"1090", "Boshqa materiallar", "INV", false, false, false, "Boshqa materiallar"},

		// Ishlab chiqarish (20xx-29xx)
		{"2010", "Asosiy ishlab chiqarish", "INV", false, false, false, "Tugallanmagan ishlab chiqarish"},
		{"2310", "Yordamchi ishlab chiqarish", "INV", false, false, false, "Yordamchi ishlab chiqarish xarajatlari"},
		{"2510", "Umumishlab chiqarish xarajatlari", "OPEX", false, false, false, "Ishlab chiqarish ustama xarajatlari"},
		{"2810", "Tayyor mahsulot", "INV", false, false, false, "Tayyor mahsulot omborda"},
		{"2910", "Sotib olingan tovarlar", "INV", false, false, false, "Qayta sotish uchun tovarlar"},

		// Kelgusi davr xarajatlari (31xx)
		{"3100", "Kelgusi davr xarajatlari", "OA", false, false, false, "Oldindan to'langan xarajatlar"},

		// Debitorlik qarzlari (40xx-49xx)
		{"4010", "Xaridor va buyurtmachilardan olinadigan schyotlar", "AR", false, true, true, "Savdo debitorlik qarzlari"},
		{"4210", "Mehnat haqi bo'yicha berilgan bo'naklar", "OA", false, false, false, "Xodimlarga berilgan bo'naklar"},
		{"4310", "TMQ uchun berilgan bo'naklar", "OA", false, false, false, "Mol yetkazib beruvchilarga bo'naklar"},
		{"4410", "Byudjetga soliqlar bo'yicha bo'nak to'lovlari", "OA", false, false, false, "QQS kirim (olinadigan)"},
		{"4710", "Hisobdor shaxslar", "OA", false, false, false, "Hisobdor shaxslarga berilgan summa"},
		{"4790", "Boshqa debitorlik qarzlari", "AR", false, false, false, "Boshqa debitorlik qarzlari"},
		{"4910", "Shubhali qarzlar bo'yicha zaxira", "CONTRA_ASSET", false, false, false, "Shubhali qarzlar uchun zaxira"},

		// Pul mablag'lari (50xx-55xx)
		{"5010", "Kassa", "CASH", false, false, true, "Naqd pul kassada"},
		{"5020", "Valyuta kassasi", "CASH", false, false, true, "Chet el valyutasidagi naqd pullar"},
		{"5110", "Hisob-kitob schyoti", "CASH", true, false, true, "Asosiy bank hisob raqami"},
		{"5210", "Mamlakat ichidagi valyuta schyotlari", "CASH", true, false, true, "Mamlakat ichidagi banklardagi chet el valyutasi schyotlari"},
		{"5220", "Chet eldagi valyuta schyotlari", "CASH", true, false, true, "Chet eldagi banklardagi chet el valyutasi schyotlari"},

		// Kreditorlik qarzlari (60xx-69xx)
		{"6010", "Mol yetkazib beruvchilar va pudratchilar", "AP", false, true, true, "Mol yetkazib beruvchilarga savdo kreditorlik qarzlari"},
		{"6015", "Olingan, lekin hisob-faktura qilinmagan tovarlar", "ST_LIAB", false, false, false, "Olingan, lekin hali hisob-faktura ko'rsatilmagan tovarlar uchun oraliq hisob"},
		{"6016", "Yetkazilgan, lekin hisob-faktura qilinmagan tovarlar", "ST_LIAB", false, false, false, "Yetkazilgan, lekin hali hisob-faktura ko'rsatilmagan tovarlar uchun oraliq hisob"},
		{"6310", "Kelgusi davr daromadlari", "ST_LIAB", false, false, false, "Kechiktirilgan daromad"},
		{"6410", "Byudjetga to'lovlar bo'yicha qarz (turlar bo'yicha)", "ST_LIAB", false, false, false, "Soliq majburiyatlari"},
		{"6420", "QQS bo'yicha qarz", "ST_LIAB", false, false, false, "QQS/Savdo solig'i to'lanishi kerak"},
		{"6430", "Foyda solig'i bo'yicha qarz", "ST_LIAB", false, false, false, "Daromad solig'i to'lanishi kerak"},
		{"6510", "Maqsadli davlat jamg'armalariga to'lovlar", "ST_LIAB", false, false, false, "Ijtimoiy sug'urta to'lovlari"},
		{"6710", "Mehnat haqi bo'yicha xodimlarga bo'lgan qarz", "ST_LIAB", false, false, false, "Ish haqi va maoshlar to'lanishi kerak"},
		{"6810", "Qisqa muddatli bank kreditlari", "ST_LIAB", false, false, true, "Qisqa muddatli qarzlar"},
		{"6920", "Foizlar bo'yicha hisoblashlar", "ST_LIAB", false, false, false, "To'lanishi kerak bo'lgan foizlar"},
		{"6990", "Boshqa kreditorlik qarzlari", "ST_LIAB", false, false, false, "Hisoblangan majburiyatlar"},

		// Uzoq muddatli majburiyatlar (78xx)
		{"7810", "Uzoq muddatli bank kreditlari", "LT_LIAB", false, false, true, "Uzoq muddatli qarzlar"},
		{"7820", "Uzoq muddatli qarzlar", "LT_LIAB", false, false, true, "Uzoq muddatli qarzlar"},

		// Kapital (83xx-87xx)
		{"8300", "Ustav kapitali", "EQUITY", false, false, false, "Egasining kapitali"},
		{"8310", "Oddiy aksiyalar", "EQUITY", false, false, false, "Chiqarilgan aksiyadorlik kapitali"},
		{"8400", "Zaxira kapitali", "EQUITY", false, false, false, "Zaxira kapitali"},
		{"8500", "Qo'shimcha kapital", "EQUITY", false, false, false, "Qo'shimcha kapital"},
		{"8700", "Taqsimlanmagan foyda (qoplanmagan zarar)", "RETAIN", false, false, false, "Yig'ilgan foyda"},
		{"8720", "E'lon qilingan dividendlar", "EQUITY", false, false, false, "E'lon qilingan dividendlar"},

		// Daromadlar (90xx-95xx)
		{"9010", "Tayyor mahsulot sotishdan daromadlar", "REVENUE", false, false, false, "Sotishdan tushum"},
		{"9020", "Tovarlar sotishdan daromadlar", "REVENUE", false, false, false, "Tovarlar sotishdan tushum"},
		{"9030", "Xizmatlar ko'rsatishdan daromadlar", "REVENUE", false, false, false, "Xizmatlardan tushum"},
		{"9040", "Qurilish shartnomasi bo'yicha daromadlar", "REVENUE", false, false, false, "Qurilish loyihalaridan tushum"},
		{"9310", "Boshqa operatsion daromadlar", "OTHER_INC", false, false, false, "Turli xil daromadlar"},
		{"9510", "Foiz shaklida daromadlar", "OTHER_INC", false, false, false, "Olingan foizlar"},
		{"9540", "Valyuta kursi farqlaridan daromadlar", "OTHER_INC", false, false, false, "Valyuta ayirboshlash bo'yicha foyda"},

		// Xarajatlar (91xx-96xx)
		{"9110", "Sotilgan tayyor mahsulot tannarxi", "COGS", false, false, false, "Sotilgan tovarlarning to'g'ridan-to'g'ri tannarxi"},
		{"9120", "Sotilgan tovarlar tannarxi", "COGS", false, false, false, "Ishlatilgan xom ashyo tannarxi"},
		{"9130", "Ishlab chiqarish xarajatlari", "COGS", false, false, false, "Bevosita mehnat xarajatlari"},
		{"9140", "Umumishlab chiqarish xarajatlari", "COGS", false, false, false, "Ishlab chiqarish ustama xarajatlari"},
		{"9150", "Xizmatlar tannarxi", "COGS", false, false, false, "Ko'rsatilgan xizmatlar tannarxi"},
		{"9160", "Qurilish ishlari tannarxi", "COGS", false, false, false, "Qurilish ishlari tannarxi"},
		{"9410", "Davr xarajatlari", "OPEX", false, false, false, "Boshqa operatsion xarajatlar"},
		{"9420", "Mehnat haqi xarajatlari", "OPEX", false, false, false, "Xodimlar ish haqi va maoshlari"},
		{"9430", "Ijara xarajatlari", "OPEX", false, false, false, "Ijara va lizing to'lovlari"},
		{"9440", "Kommunal xarajatlar", "OPEX", false, false, false, "Elektr, suv, gaz"},
		{"9450", "Ofis xarajatlari", "OPEX", false, false, false, "Ofis jihozlari xarajati"},
		{"9460", "Sug'urta xarajatlari", "OPEX", false, false, false, "Sug'urta mukofotlari"},
		{"9470", "Eskirish xarajatlari", "OPEX", false, false, false, "Aktivlar eskirishi"},
		{"9480", "Reklama va marketing xarajatlari", "OPEX", false, false, false, "Marketing xarajatlari"},
		{"9490", "Boshqa xizmatlar uchun xarajatlar", "OPEX", false, false, false, "Yuridik, buxgalteriya to'lovlari"},
		{"9610", "Foiz shaklida xarajatlar", "OTHER_EXP", false, false, false, "Qarzlar bo'yicha foizlar"},
		{"9620", "Bank xizmatlari uchun xarajatlar", "OTHER_EXP", false, false, false, "Bank to'lovlari va yig'imlar"},
		{"9630", "Valyuta kursi farqlaridan zararlar", "OTHER_EXP", false, false, false, "Valyuta ayirboshlashda zarar"},
		{"9690", "Boshqa moliyaviy xarajatlar", "OTHER_EXP", false, false, false, "Turli xil xarajatlar"},

		// Yakuniy natija (99xx)
		{"9910", "Yakuniy moliyaviy natija", "RETAIN", false, false, false, "Joriy davr foydasi/zarari"},
	}

	for _, acc := range defaultAccounts {
		typeID, ok := accountTypeIDs[acc.typeCode]
		if !ok {
			h.log.Warn("Account type not found", "type_code", acc.typeCode)
			continue
		}

		id := uuid.New()
		_, err := h.db.Exec(`
			INSERT INTO accounts (
				id, tenant_id, organization_id, account_type_id, code, name, description,
				is_bank_account, is_control_account, is_reconcilable,
				current_balance, opening_balance, is_active, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (tenant_id, organization_id, code) DO NOTHING
		`,
			id, tenantID, orgID, typeID, acc.code, acc.name, acc.description,
			acc.isBankAcc, acc.isControl, acc.isRecon,
			0, 0, true, now, now,
		)
		if err != nil {
			h.log.Error("Failed to create default account", "error", err, "code", acc.code)
		}
	}

	// ── GROUP accounts (mirror migration 317) ─────────────────────────
	// New orgs created AFTER migration 317 don't get the group
	// hierarchy because migration 317 only ran once for orgs that
	// existed at that time. Without these, the chart-of-accounts view
	// only shows leaves and the hierarchy looks "flat". Insert the
	// same 40 group accounts here so every new org has the standard
	// UzNAS hierarchy from day one.
	groupAccounts := []struct {
		code     string
		nameUz   string
		nameEn   string
		nameRu   string
		typeCode string
		nature   string
	}{
		// Section 0
		{"0000", "Uzoq muddatli aktivlar", "Long-term Assets", "Долгосрочные активы", "FA", "ACTIVE"},
		{"0100", "Asosiy vositalar", "Fixed Assets", "Основные средства", "FA", "ACTIVE"},
		{"0200", "Asosiy vositalar eskirishi", "Depreciation of Fixed Assets", "Износ основных средств", "CONTRA_ASSET", "PASSIVE"},
		{"0400", "Nomoddiy aktivlar", "Intangible Assets", "Нематериальные активы", "OA", "ACTIVE"},
		{"0800", "Kapital qo'yilmalar", "Capital Investments", "Капитальные вложения", "FA", "ACTIVE"},
		// Section 1
		{"1000", "Tovar-moddiy zaxiralar", "Inventories", "Товарно-материальные запасы", "INV", "ACTIVE"},
		// Section 2
		{"2000", "Ishlab chiqarish xarajatlari", "Production Costs", "Затраты на производство", "INV", "ACTIVE"},
		{"2800", "Tayyor mahsulot va tovarlar", "Finished Goods and Merchandise", "Готовая продукция и товары", "INV", "ACTIVE"},
		{"2900", "Sotib olingan tovarlar", "Purchased Goods", "Приобретённые товары", "INV", "ACTIVE"},
		// Section 3
		{"3000", "Kelgusi davr xarajatlari", "Deferred Expenses", "Расходы будущих периодов", "OA", "ACTIVE"},
		// Section 4
		{"4000", "Debitorlik qarzdorligi", "Receivables", "Дебиторская задолженность", "AR", "ACTIVE"},
		{"4200", "Hisobdor shaxslar bilan hisob-kitob", "Settlements with Accountable Persons", "Расчёты с подотчётными лицами", "OA", "ACTIVE"},
		{"4400", "Byudjet bilan hisob-kitob", "Budget Settlements", "Расчёты с бюджетом", "OA", "ACTIVE_PASSIVE"},
		{"4700", "Turli debitorlar", "Various Debtors", "Разные дебиторы", "OA", "ACTIVE"},
		// Section 5
		{"5000", "Pul mablag'lari", "Cash and Cash Equivalents", "Денежные средства", "CASH", "ACTIVE"},
		{"5100", "Bank hisob raqamlari", "Bank Accounts", "Банковские счета", "CASH", "ACTIVE"},
		{"5200", "Maxsus bank hisobvaraqlari", "Special Bank Accounts", "Специальные банковские счета", "CASH", "ACTIVE"},
		{"5500", "Qisqa muddatli moliyaviy qo'yilmalar", "Short-term Financial Investments", "Краткосрочные финансовые вложения", "OA", "ACTIVE"},
		// Section 6
		{"6000", "Qisqa muddatli majburiyatlar", "Short-term Liabilities", "Краткосрочные обязательства", "AP", "PASSIVE"},
		{"6100", "Bo'naklar", "Advances", "Авансы полученные", "ST_LIAB", "PASSIVE"},
		{"6200", "Qisqa muddatli kreditlar", "Short-term Loans", "Краткосрочные кредиты", "ST_LIAB", "PASSIVE"},
		{"6400", "Byudjetga to'lovlar", "Tax Payments", "Расчёты с бюджетом", "ST_LIAB", "PASSIVE"},
		{"6500", "Ijtimoiy sug'urta", "Social Insurance", "Социальное страхование", "ST_LIAB", "PASSIVE"},
		{"6700", "Xodimlar bilan hisob-kitob", "Employee Settlements", "Расчёты с персоналом", "ST_LIAB", "PASSIVE"},
		{"6800", "Qisqa muddatli kreditlar va qarzlar", "Short-term Credits", "Краткосрочные кредиты и займы", "ST_LIAB", "PASSIVE"},
		{"6900", "Boshqa majburiyatlar", "Other Liabilities", "Прочие обязательства", "ST_LIAB", "PASSIVE"},
		// Section 7
		{"7000", "Uzoq muddatli majburiyatlar", "Long-term Liabilities", "Долгосрочные обязательства", "LT_LIAB", "PASSIVE"},
		{"7300", "Uzoq muddatli bo'naklar", "Long-term Advances", "Долгосрочные авансы", "LT_LIAB", "PASSIVE"},
		{"7800", "Uzoq muddatli kreditlar", "Long-term Credits", "Долгосрочные кредиты", "LT_LIAB", "PASSIVE"},
		// Section 8
		{"8000", "Kapital", "Equity", "Собственный капитал", "EQUITY", "PASSIVE"},
		// Section 9
		{"9000", "Daromadlar va xarajatlar", "Revenue and Expenses", "Доходы и расходы", "REVENUE", "ACTIVE_PASSIVE"},
		{"9100", "Tannarx", "Cost of Goods Sold", "Себестоимость", "COGS", "ACTIVE"},
		{"9200", "Boshqa daromadlar", "Other Income", "Прочие доходы", "OTHER_INC", "PASSIVE"},
		{"9300", "Boshqa operatsion daromadlar", "Other Operating Income", "Прочие операционные доходы", "OTHER_INC", "PASSIVE"},
		{"9400", "Davr xarajatlari", "Period Expenses", "Расходы периода", "OPEX", "ACTIVE"},
		{"9500", "Moliyaviy daromadlar", "Financial Income", "Финансовые доходы", "OTHER_INC", "PASSIVE"},
		{"9600", "Moliyaviy xarajatlar", "Financial Expenses", "Финансовые расходы", "OTHER_EXP", "ACTIVE"},
		{"9700", "Favqulodda foyda va zarar", "Extraordinary Gains and Losses", "Чрезвычайные прибыли и убытки", "OTHER_EXP", "ACTIVE_PASSIVE"},
		{"9800", "Favqulodda xarajatlar", "Extraordinary Expenses", "Чрезвычайные расходы", "OTHER_EXP", "ACTIVE"},
		{"9900", "Yakuniy moliyaviy natija", "Final Financial Result", "Итоговый финансовый результат", "EQUITY", "ACTIVE_PASSIVE"},
	}
	for _, grp := range groupAccounts {
		typeID, ok := accountTypeIDs[grp.typeCode]
		if !ok {
			continue
		}
		_, _ = h.db.Exec(`
			INSERT INTO accounts (
				id, tenant_id, organization_id, account_type_id,
				code, name, name_en, name_ru,
				is_bank_account, is_control_account, is_reconcilable,
				current_balance, opening_balance, is_active, is_leaf, account_nature,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false, false, false, 0, 0, true, false, $9, $10, $10)
			ON CONFLICT (tenant_id, organization_id, code) DO UPDATE
				SET is_leaf = false,
				    account_nature = EXCLUDED.account_nature,
				    name_en = COALESCE(NULLIF(accounts.name_en, ''), EXCLUDED.name_en),
				    name_ru = COALESCE(NULLIF(accounts.name_ru, ''), EXCLUDED.name_ru),
				    updated_at = EXCLUDED.updated_at
				WHERE accounts.deleted_at IS NULL
		`, uuid.New(), tenantID, orgID, typeID,
			grp.code, grp.nameUz, grp.nameEn, grp.nameRu,
			grp.nature, now)
	}

	// ── parent_id linkage ─────────────────────────────────────────────
	// Wire leaf codes (1010, 4010, etc.) to their group parents (1000,
	// 4000, etc.) using the SAME logic migration 317 applied to legacy
	// orgs. Idempotent — only touches rows where parent_id IS NULL.
	// Link 4-char leaves to 4-char "X000" group: e.g. 1010 → 1000.
	h.db.Exec(`
		UPDATE accounts a SET parent_id = g.id
		FROM accounts g
		WHERE a.organization_id = $2 AND g.organization_id = $2
		  AND a.tenant_id = $1 AND g.tenant_id = $1
		  AND a.deleted_at IS NULL AND g.deleted_at IS NULL
		  AND a.parent_id IS NULL
		  AND a.code ~ '^[1-9][0-9]{2,3}$'
		  AND LENGTH(a.code) = 4
		  AND g.code = LEFT(a.code, 2) || '00'
		  AND a.id != g.id
	`, tenantID, orgID)
	// Link section groups (0100, 0200, ...) to section header (0000).
	h.db.Exec(`
		UPDATE accounts a SET parent_id = g.id
		FROM accounts g
		WHERE a.organization_id = $2 AND g.organization_id = $2
		  AND a.tenant_id = $1 AND g.tenant_id = $1
		  AND a.deleted_at IS NULL AND g.deleted_at IS NULL
		  AND a.parent_id IS NULL
		  AND a.code ~ '^0[0-9]00$'
		  AND g.code = '0000'
		  AND a.id != g.id
		  AND a.code != '0000'
	`, tenantID, orgID)
	// Link sub-section groups (e.g. 6100, 6200) to section header (6000).
	h.db.Exec(`
		UPDATE accounts a SET parent_id = g.id
		FROM accounts g
		WHERE a.organization_id = $2 AND g.organization_id = $2
		  AND a.tenant_id = $1 AND g.tenant_id = $1
		  AND a.deleted_at IS NULL AND g.deleted_at IS NULL
		  AND a.parent_id IS NULL
		  AND a.code ~ '^[1-9][0-9]00$'
		  AND g.code = LEFT(a.code, 1) || '000'
		  AND a.id != g.id
		  AND a.code != LEFT(a.code, 1) || '000'
		  AND EXISTS (SELECT 1 FROM accounts WHERE code = LEFT(a.code, 1) || '000' AND organization_id = $2 AND tenant_id = $1 AND deleted_at IS NULL)
	`, tenantID, orgID)
	// Mark any account that now has children as non-leaf.
	h.db.Exec(`
		UPDATE accounts a SET is_leaf = false
		WHERE a.tenant_id = $1 AND a.organization_id = $2 AND a.deleted_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM accounts c
		    WHERE c.parent_id = a.id AND c.deleted_at IS NULL
		  )
	`, tenantID, orgID)

	// Set parent_id for inventory sub-accounts (1310/1320/1330/1340 → parent 1300)
	h.db.Exec(`
		UPDATE accounts SET parent_id = (
			SELECT id FROM accounts a2
			WHERE a2.tenant_id = accounts.tenant_id AND a2.organization_id = accounts.organization_id
			AND a2.code = '1300' AND a2.deleted_at IS NULL LIMIT 1
		)
		WHERE tenant_id = $1 AND organization_id = $2
		AND code IN ('1310', '1320', '1330', '1340') AND parent_id IS NULL
	`, tenantID, orgID)

	h.log.Info("Created default chart of accounts", "tenant_id", tenantID, "org_id", orgID,
		"leaf_count", len(defaultAccounts), "group_count", len(groupAccounts))
	return nil
}

// createDefaultJournals creates default accounting journals for a new organization
func (h *Handler) createDefaultJournals(tenantID, orgID uuid.UUID) error {
	now := time.Now()

	// Get some key account IDs for default debit/credit accounts
	accountIDs := make(map[string]uuid.UUID)
	rows, err := h.db.Query(`
		SELECT id, code FROM accounts
		WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL
	`, tenantID, orgID)
	if err != nil {
		return fmt.Errorf("failed to get accounts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			continue
		}
		accountIDs[code] = id
	}

	// Define default journals — must match migration 276/278 list.
	// Each journal carries name in three languages so the UI can
	// localize. The `name` field (Russian, the system's lingua franca)
	// is what migration 278's backfill stored; keeping the same value
	// here so re-running this seeder for an org that already has
	// migration-seeded journals is a no-op.
	defaultJournals := []struct {
		code              string
		nameRu            string
		nameUz            string
		nameEn            string
		journalType       string
		defaultDebitCode  string
		defaultCreditCode string
	}{
		{"GEN", "Главный журнал", "Bosh jurnal", "General Journal", "general", "", ""},
		{"SAL", "Журнал продаж", "Sotish jurnali", "Sales Journal", "sales", "4010", "9010"},
		{"PUR", "Журнал закупок", "Xarid jurnali", "Purchase Journal", "purchase", "9110", "6010"},
		{"CASH", "Кассовый журнал", "Kassa jurnali", "Cash Journal", "cash", "5010", "5010"},
		{"BANK", "Банковский журнал", "Bank jurnali", "Bank Journal", "bank", "5110", "5110"},
		{"MISC", "Прочие операции", "Boshqa operatsiyalar jurnali", "Miscellaneous Journal", "miscellaneous", "", ""},
		// CASH_RECEIPTS removed — was redundant with CASH. Callers in
		// sales_returns.go already fall back to 'CASH' when the
		// CASH_RECEIPTS code isn't found, so no other code paths
		// break. Migration 395 retires the existing rows.
		{"STOCK", "Складской журнал", "Ombor jurnali", "Stock Journal", "general", "", ""},
		{"ASSET", "Журнал основных средств", "Asosiy vositalar jurnali", "Fixed Assets Journal", "general", "", ""},
		{"PAYROLL", "Журнал зарплаты", "Ish haqi jurnali", "Payroll Journal", "general", "", ""},
		{"CONST", "Строительный журнал", "Qurilish jurnali", "Construction Journal", "general", "", ""},
	}

	// Find profit/loss accounts for cash/bank journals
	var profitAccountID, lossAccountID *uuid.UUID
	// Profit: 9540 (Valyuta kurs farqi daromadi) or 9310 (Boshqa daromadlar) or 6900
	for _, code := range []string{"9540", "9310", "6900"} {
		if accID, ok := accountIDs[code]; ok {
			profitAccountID = &accID
			break
		}
	}
	// Loss: 9630 (Valyuta kurs farqi zararlari) or 9410 (Boshqa xarajatlar) or 9400
	for _, code := range []string{"9630", "9410", "9400"} {
		if accID, ok := accountIDs[code]; ok {
			lossAccountID = &accID
			break
		}
	}

	for _, j := range defaultJournals {
		id := uuid.New()

		var defaultDebitID, defaultCreditID *uuid.UUID
		if j.defaultDebitCode != "" {
			if accID, ok := accountIDs[j.defaultDebitCode]; ok {
				defaultDebitID = &accID
			}
		}
		if j.defaultCreditCode != "" {
			if accID, ok := accountIDs[j.defaultCreditCode]; ok {
				defaultCreditID = &accID
			}
		}

		// Set profit/loss accounts for cash and bank journals
		var profitAcct, lossAcct *uuid.UUID
		if j.journalType == "cash" || j.journalType == "bank" {
			profitAcct = profitAccountID
			lossAcct = lossAccountID
		}

		// Migration 278 dropped the old UNIQUE(tenant_id, code) and
		// added UNIQUE(tenant_id, organization_id, code) so each org
		// can have its own journals. The ON CONFLICT here MUST match
		// the live constraint or the insert will throw "no unique or
		// exclusion constraint matching the ON CONFLICT specification"
		// and the org will end up with zero journals (the symptom: an
		// empty Journals page on the second+ org under any tenant).
		_, err := h.db.Exec(`
			INSERT INTO journals (
				id, tenant_id, organization_id, code, name, name_uz, name_en, type,
				default_debit_account_id, default_credit_account_id,
				profit_account_id, loss_account_id,
				is_active, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (tenant_id, organization_id, code) DO UPDATE
				SET default_debit_account_id  = COALESCE(EXCLUDED.default_debit_account_id,  journals.default_debit_account_id),
				    default_credit_account_id = COALESCE(EXCLUDED.default_credit_account_id, journals.default_credit_account_id),
				    profit_account_id         = COALESCE(EXCLUDED.profit_account_id,         journals.profit_account_id),
				    loss_account_id           = COALESCE(EXCLUDED.loss_account_id,           journals.loss_account_id),
				    -- Backfill localized name columns if they were empty
				    -- (legacy rows created before the seeder wrote them).
				    name_uz = COALESCE(NULLIF(journals.name_uz, ''), EXCLUDED.name_uz),
				    name_en = COALESCE(NULLIF(journals.name_en, ''), EXCLUDED.name_en),
				    is_active = true,
				    updated_at = NOW()
				WHERE journals.deleted_at IS NULL
		`,
			id, tenantID, orgID, j.code, j.nameRu, j.nameUz, j.nameEn, j.journalType,
			defaultDebitID, defaultCreditID,
			profitAcct, lossAcct,
			true, now,
		)
		if err != nil {
			h.log.Error("Failed to create default journal", "error", err, "code", j.code)
		}
	}

	h.log.Info("Created default journals", "tenant_id", tenantID, "org_id", orgID, "journal_count", len(defaultJournals))

	// Seed default payment methods and link to cash/bank journals
	h.createDefaultPaymentMethods(tenantID, orgID)

	return nil
}

// createDefaultPaymentMethods seeds payment methods and links them to cash/bank journals
func (h *Handler) createDefaultPaymentMethods(tenantID, orgID uuid.UUID) {
	now := time.Now()

	defaultPMs := []struct {
		code    string
		name    string
		pmType  string
	}{
		{"CASH", "Cash", "cash"},
		{"BANK", "Bank Transfer", "bank_transfer"},
		{"CARD", "Credit Card", "credit_card"},
		{"CHECK", "Check", "check"},
	}

	// Find default accounts for payment methods
	var cashAccountID, bankAccountID *uuid.UUID
	var acctID uuid.UUID
	if err := h.db.QueryRow(`
		SELECT id FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND code = '1000' AND deleted_at IS NULL
	`, tenantID, orgID).Scan(&acctID); err == nil {
		cashAccountID = &acctID
	}
	var bankAcctID uuid.UUID
	if err := h.db.QueryRow(`
		SELECT id FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND code = '1010' AND deleted_at IS NULL
	`, tenantID, orgID).Scan(&bankAcctID); err == nil {
		bankAccountID = &bankAcctID
	}

	pmIDs := make(map[string]uuid.UUID)
	for _, pm := range defaultPMs {
		id := uuid.New()
		_, err := h.db.Exec(`
			INSERT INTO payment_methods (id, tenant_id, code, name, type, account_id, is_active, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7)
			ON CONFLICT (tenant_id, code) DO UPDATE SET name = EXCLUDED.name
			RETURNING id
		`, id, tenantID, pm.code, pm.name, pm.pmType, cashAccountID, now)
		if err != nil {
			h.log.Error("Failed to create payment method", "error", err, "code", pm.code)
			continue
		}
		// Get the actual ID (in case of conflict/update)
		h.db.QueryRow("SELECT id FROM payment_methods WHERE tenant_id = $1 AND code = $2", tenantID, pm.code).Scan(&id)
		pmIDs[pm.code] = id
	}

	// Link payment methods to cash and bank journals
	type journalPMLink struct {
		journalCode string
		pmCode      string
		direction   string
	}
	links := []journalPMLink{
		{"CASH", "CASH", "inbound"},
		{"CASH", "CASH", "outbound"},
		{"BANK", "BANK", "inbound"},
		{"BANK", "BANK", "outbound"},
		{"BANK", "CARD", "inbound"},
		{"BANK", "CHECK", "inbound"},
		{"BANK", "CHECK", "outbound"},
	}

	for _, link := range links {
		pmID, ok := pmIDs[link.pmCode]
		if !ok {
			continue
		}
		var journalID uuid.UUID
		if err := h.db.QueryRow(`
			SELECT id FROM journals WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL
		`, tenantID, link.journalCode).Scan(&journalID); err != nil {
			continue
		}

		// Set outstanding_account_id: bank account for BANK journal, cash account for CASH journal
		var outstandingAcct *uuid.UUID
		if link.journalCode == "BANK" {
			outstandingAcct = bankAccountID
		} else if link.journalCode == "CASH" {
			outstandingAcct = cashAccountID
		}

		h.db.Exec(`
			INSERT INTO journal_payment_methods (id, tenant_id, journal_id, payment_method_id, direction, name, outstanding_account_id, is_active, created_at)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, '', $5, true, $6)
			ON CONFLICT (journal_id, payment_method_id, direction) DO NOTHING
		`, tenantID, journalID, pmID, link.direction, outstandingAcct, now)
	}

	h.log.Info("Created default payment methods", "tenant_id", tenantID, "count", len(defaultPMs))
}

// createIntercompanyContacts sets up intercompany vendor/client relationships.
// When creating Company B and selecting Company A:
//   - A becomes a vendor (supplier) in B
//   - B becomes a customer (client) in A
// intercompanyOrgInfo holds organization fields needed for creating intercompany contacts
type intercompanyOrgInfo struct {
	Name         string
	TaxID        *string
	Email        *string
	Phone        *string
	LegalAddress *string
	BankAccount  *string
	BankMFO      *string
	BankName     *string
}

func (h *Handler) getOrgInfoForIntercompany(tenantID, orgID uuid.UUID) (*intercompanyOrgInfo, error) {
	var info intercompanyOrgInfo
	var contactInfoJSON []byte
	err := h.db.QueryRow(
		`SELECT name, tax_id, contact_info, legal_address, bank_account, bank_mfo, bank_name
		 FROM organizations WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		orgID, tenantID,
	).Scan(&info.Name, &info.TaxID, &contactInfoJSON, &info.LegalAddress, &info.BankAccount, &info.BankMFO, &info.BankName)
	if err != nil {
		return nil, err
	}
	if len(contactInfoJSON) > 0 {
		var ci map[string]interface{}
		if json.Unmarshal(contactInfoJSON, &ci) == nil {
			if e, ok := ci["email"].(string); ok && e != "" {
				info.Email = &e
			}
			if p, ok := ci["phone"].(string); ok && p != "" {
				info.Phone = &p
			}
		}
	}
	return &info, nil
}

func (h *Handler) createIntercompanyContacts(tenantID, newOrgID uuid.UUID, newOrgName string, newOrgTaxID *string, targetOrgIDs []string) {
	now := time.Now()

	// Get new org's full info for customer contacts
	newOrgInfo, _ := h.getOrgInfoForIntercompany(tenantID, newOrgID)

	for _, targetIDStr := range targetOrgIDs {
		targetOrgID, err := uuid.Parse(targetIDStr)
		if err != nil {
			h.log.Error("Invalid intercompany target org ID", "error", err, "target_id", targetIDStr)
			continue
		}

		// Get target org full info for vendor contacts
		targetInfo, err := h.getOrgInfoForIntercompany(tenantID, targetOrgID)
		if err != nil {
			h.log.Error("Intercompany target org not found", "target_id", targetOrgID, "tenant_id", tenantID)
			continue
		}

		// Build billing address JSON from legal_address
		targetBillingAddr := "{}"
		if targetInfo.LegalAddress != nil && *targetInfo.LegalAddress != "" {
			if addrJSON, err := json.Marshal(map[string]string{"street": *targetInfo.LegalAddress}); err == nil {
				targetBillingAddr = string(addrJSON)
			}
		}

		// 1) Create or update target org (A) as vendor in the new org (B)
		var existingVendorID uuid.UUID
		err = h.db.QueryRow(`SELECT id FROM contacts WHERE tenant_id = $1 AND organization_id = $2 AND type = 'vendor' AND source_organization_id = $3 AND deleted_at IS NULL`,
			tenantID, newOrgID, targetOrgID).Scan(&existingVendorID)

		if err == nil {
			// Update existing vendor contact
			_, err = h.db.Exec(`UPDATE contacts SET name = $1, tax_id = $2, email = $3, phone = $4, billing_address = $5, updated_at = $6 WHERE id = $7`,
				targetInfo.Name, targetInfo.TaxID, targetInfo.Email, targetInfo.Phone, targetBillingAddr, now, existingVendorID)
			if err != nil {
				h.log.Error("Failed to update vendor contact", "error", err)
			}
		} else {
			// Create new vendor contact
			vendorID := uuid.New()
			vendorCode := fmt.Sprintf("VEN-%s", vendorID.String()[:8])
			_, err = h.db.Exec(`
				INSERT INTO contacts (id, tenant_id, organization_id, type, code, name, tax_id,
					email, phone, billing_address, shipping_address, tags, custom_fields,
					source_organization_id, is_active, created_at, updated_at)
				VALUES ($1, $2, $3, 'vendor', $4, $5, $6, $7, $8, $9, '{}', '[]', '{}', $10, true, $11, $11)
			`, vendorID, tenantID, newOrgID, vendorCode, targetInfo.Name, targetInfo.TaxID,
				targetInfo.Email, targetInfo.Phone, targetBillingAddr, targetOrgID, now)
			if err != nil {
				h.log.Error("Failed to create vendor contact", "error", err, "org", newOrgID, "vendor", targetInfo.Name)
			}
		}

		// Build new org billing address
		newOrgBillingAddr := "{}"
		if newOrgInfo != nil && newOrgInfo.LegalAddress != nil && *newOrgInfo.LegalAddress != "" {
			if addrJSON, err := json.Marshal(map[string]string{"street": *newOrgInfo.LegalAddress}); err == nil {
				newOrgBillingAddr = string(addrJSON)
			}
		}

		// 2) Create or update the new org (B) as customer in target org (A)
		var existingCustomerID uuid.UUID
		err = h.db.QueryRow(`SELECT id FROM contacts WHERE tenant_id = $1 AND organization_id = $2 AND type = 'customer' AND source_organization_id = $3 AND deleted_at IS NULL`,
			tenantID, targetOrgID, newOrgID).Scan(&existingCustomerID)

		var newEmail, newPhone *string
		if newOrgInfo != nil {
			newEmail = newOrgInfo.Email
			newPhone = newOrgInfo.Phone
		}

		if err == nil {
			// Update existing customer contact
			_, err = h.db.Exec(`UPDATE contacts SET name = $1, tax_id = $2, email = $3, phone = $4, billing_address = $5, updated_at = $6 WHERE id = $7`,
				newOrgName, newOrgTaxID, newEmail, newPhone, newOrgBillingAddr, now, existingCustomerID)
			if err != nil {
				h.log.Error("Failed to update customer contact", "error", err)
			}
		} else {
			// Create new customer contact
			customerID := uuid.New()
			customerCode := fmt.Sprintf("CUS-%s", customerID.String()[:8])
			_, err = h.db.Exec(`
				INSERT INTO contacts (id, tenant_id, organization_id, type, code, name, tax_id,
					email, phone, billing_address, shipping_address, tags, custom_fields,
					source_organization_id, is_active, created_at, updated_at)
				VALUES ($1, $2, $3, 'customer', $4, $5, $6, $7, $8, $9, '{}', '[]', '{}', $10, true, $11, $11)
			`, customerID, tenantID, targetOrgID, customerCode, newOrgName, newOrgTaxID,
				newEmail, newPhone, newOrgBillingAddr, newOrgID, now)
			if err != nil {
				h.log.Error("Failed to create customer contact", "error", err, "org", targetOrgID, "customer", newOrgName)
			}
		}
	}
}

// ImportOrganizationsInput represents the input for bulk importing organizations
type ImportOrganizationsInput struct {
	Organizations []CreateOrganizationInput `json:"organizations" binding:"required"`
}

// ImportOrganizations bulk imports organizations from JSON
func (h *Handler) ImportOrganizations(c *gin.Context) {
	tenantIDStr, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr.(string))
	if err != nil {
		response.BadRequest(c, "Invalid tenant ID")
		return
	}

	var input ImportOrganizationsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if len(input.Organizations) == 0 {
		response.BadRequest(c, "No organizations to import")
		return
	}

	imported := 0
	skipped := 0
	errors := []string{}

	for i, org := range input.Organizations {
		// Check for duplicate code
		var existingCount int
		err = h.db.QueryRow(
			"SELECT COUNT(*) FROM organizations WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL",
			tenantID, org.Code,
		).Scan(&existingCount)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Row %d: failed to check duplicate", i+1))
			continue
		}
		if existingCount > 0 {
			skipped++
			continue
		}

		// Set defaults
		orgType := org.Type
		if orgType == "" {
			orgType = "company"
		}

		isVATPayer := false
		if org.IsVATPayer != nil {
			isVATPayer = *org.IsVATPayer
		}

		// Parse parent ID if provided
		var parentID *uuid.UUID
		if org.ParentID != nil && *org.ParentID != "" {
			parsed, err := uuid.Parse(*org.ParentID)
			if err == nil {
				parentID = &parsed
			}
		}

		// Convert maps to JSON
		addressJSON := []byte("{}")
		contactInfoJSON := []byte("{}")
		if org.Address != nil {
			addressJSON, _ = json.Marshal(org.Address)
		}
		if org.ContactInfo != nil {
			contactInfoJSON, _ = json.Marshal(org.ContactInfo)
		}

		orgID := uuid.New()
		now := time.Now()

		_, err = h.db.Exec(`
			INSERT INTO organizations (
				id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
				address, contact_info, country, currency, accounting_standard, logo_url,
				settings, is_active, created_at, updated_at,
				oked, bank_account, bank_mfo, bank_name, is_vat_payer,
				tax_regime, activity_status, business_group, intercompany_relations,
				director_name, director_phone, legal_address, notes
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
			          $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31)
		`,
			orgID, tenantID, parentID, org.Code, org.Name, orgType,
			org.TaxID, org.RegistrationNumber, addressJSON, contactInfoJSON,
			org.Country, org.Currency, org.AccountingStandard, org.LogoURL,
			[]byte("{}"), true, now, now,
			org.OKED, org.BankAccount, org.BankMFO, org.BankName, isVATPayer,
			org.TaxRegime, org.ActivityStatus, org.BusinessGroup, org.IntercompanyRelations,
			org.DirectorName, org.DirectorPhone, org.LegalAddress, org.Notes,
		)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Row %d (%s): %v", i+1, org.Code, err))
			continue
		}

		// Create default accounts and journals for new organization
		if err := h.createDefaultChartOfAccounts(tenantID, orgID); err != nil {
			h.log.Error("Failed to create default chart of accounts for imported org", "error", err, "org_id", orgID)
		}
		if err := h.createDefaultJournals(tenantID, orgID); err != nil {
			h.log.Error("Failed to create default journals for imported org", "error", err, "org_id", orgID)
		}

		imported++
	}

	response.Success(c, map[string]interface{}{
		"imported": imported,
		"skipped":  skipped,
		"errors":   errors,
		"total":    len(input.Organizations),
	})
}

// createDefaultWarehouse creates a default warehouse for a new organization
func (h *Handler) createDefaultWarehouse(tenantID, orgID uuid.UUID, orgCode string) error {
	warehouseID := uuid.New()
	_, err := h.db.Exec(`
		INSERT INTO warehouses (id, tenant_id, organization_id, code, name, is_default, is_active, reception_steps, delivery_steps, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'Main Warehouse', true, true, 1, 1, NOW(), NOW())
	`, warehouseID, tenantID, orgID, "WH-"+orgCode)
	if err != nil {
		return err
	}

	// Create default warehouse locations
	_, err = h.db.Exec(`
		INSERT INTO warehouse_locations (id, warehouse_id, code, name, type, is_active)
		VALUES ($1, $2, 'STOCK', 'Stock', 'storage', true)
	`, uuid.New(), warehouseID)
	return err
}

// backfillProductOrgSettings creates product_organization_settings entries for all existing
// products in the tenant, so they are visible in the newly created organization
func (h *Handler) backfillProductOrgSettings(tenantID, orgID uuid.UUID) error {
	_, err := h.db.Exec(`
		INSERT INTO product_organization_settings (tenant_id, product_id, organization_id, cost_price, list_price, min_price, min_stock_level, reorder_point, reorder_quantity)
		SELECT p.tenant_id, p.id, $1, p.cost_price, p.list_price, p.min_price, p.min_stock_level, p.reorder_point, p.reorder_quantity
		FROM products p
		WHERE p.tenant_id = $2 AND p.deleted_at IS NULL
		ON CONFLICT (product_id, organization_id) DO NOTHING
	`, orgID, tenantID)
	return err
}
