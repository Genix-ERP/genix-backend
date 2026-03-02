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
		       director_name, director_phone, legal_address, notes
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
			&org.OKED, &org.BankAccount, &org.BankMFO, &org.BankName, &org.IsVATPayer,
			&org.TaxRegime, &org.ActivityStatus, &org.BusinessGroup, &org.IntercompanyRelations,
			&org.DirectorName, &org.DirectorPhone, &org.LegalAddress, &org.Notes,
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
		response.BadRequest(c, err.Error())
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
		       director_name, director_phone, legal_address, notes
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
		&org.OKED, &org.BankAccount, &org.BankMFO, &org.BankName, &org.IsVATPayer,
		&org.TaxRegime, &org.ActivityStatus, &org.BusinessGroup, &org.IntercompanyRelations,
		&org.DirectorName, &org.DirectorPhone, &org.LegalAddress, &org.Notes,
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

	query += fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL", argIndex, argIndex+1)
	args = append(args, orgID, tenantID)

	query += ` RETURNING id, tenant_id, parent_id, code, name, type, tax_id, registration_number,
	           address, contact_info, country, currency, accounting_standard, logo_url,
	           settings, is_active, created_at, updated_at,
	           oked, bank_account, bank_mfo, bank_name, COALESCE(is_vat_payer, false),
	           tax_regime, activity_status, business_group, intercompany_relations,
	           director_name, director_phone, legal_address, notes`

	var org Organization
	var addressJSON, contactInfoJSON, settingsJSON []byte

	err = h.db.QueryRow(query, args...).Scan(
		&org.ID, &org.TenantID, &org.ParentID, &org.Code, &org.Name, &org.Type,
		&org.TaxID, &org.RegistrationNumber, &addressJSON, &contactInfoJSON,
		&org.Country, &org.Currency, &org.AccountingStandard, &org.LogoURL,
		&settingsJSON, &org.IsActive, &org.CreatedAt, &org.UpdatedAt,
		&org.OKED, &org.BankAccount, &org.BankMFO, &org.BankName, &org.IsVATPayer,
		&org.TaxRegime, &org.ActivityStatus, &org.BusinessGroup, &org.IntercompanyRelations,
		&org.DirectorName, &org.DirectorPhone, &org.LegalAddress, &org.Notes,
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

	// Define default accounts - following standard Chart of Accounts
	defaultAccounts := []struct {
		code        string
		name        string
		typeCode    string
		isBankAcc   bool
		isControl   bool
		isRecon     bool
		description string
	}{
		// Assets (1xxx)
		{"1000", "Cash", "CASH", false, false, true, "Cash on hand"},
		{"1010", "Petty Cash", "CASH", false, false, true, "Petty cash fund"},
		{"1100", "Bank Account", "CASH", true, false, true, "Main bank account"},
		{"1200", "Accounts Receivable", "AR", false, true, true, "Trade receivables from customers"},
		{"1210", "Allowance for Doubtful Accounts", "AR", false, false, false, "Reserve for bad debts"},
		{"1300", "Inventory", "INV", false, true, false, "Goods held for sale"},
		{"1310", "Raw Materials", "INV", false, false, false, "Raw materials inventory"},
		{"1320", "Work in Progress", "INV", false, false, false, "Work in progress inventory"},
		{"1330", "Finished Goods", "INV", false, false, false, "Finished goods inventory"},
		{"1400", "Prepaid Expenses", "OA", false, false, false, "Prepaid expenses"},
		{"1500", "Fixed Assets", "FA", false, false, false, "Property, plant and equipment"},
		{"1510", "Accumulated Depreciation", "FA", false, false, false, "Accumulated depreciation"},
		{"1600", "Intangible Assets", "OA", false, false, false, "Intangible assets"},

		// Liabilities (2xxx)
		{"2000", "Accounts Payable", "AP", false, true, true, "Trade payables to suppliers"},
		{"2100", "Accrued Expenses", "ST_LIAB", false, false, false, "Accrued liabilities"},
		{"2110", "Wages Payable", "ST_LIAB", false, false, false, "Wages and salaries payable"},
		{"2120", "Interest Payable", "ST_LIAB", false, false, false, "Interest payable"},
		{"2200", "Tax Payable", "ST_LIAB", false, false, false, "Tax liabilities"},
		{"2210", "VAT Payable", "ST_LIAB", false, false, false, "VAT/Sales tax payable"},
		{"2220", "Income Tax Payable", "ST_LIAB", false, false, false, "Income tax payable"},
		{"2230", "Stock Interim Receipt", "ST_LIAB", false, false, false, "Interim account for goods received not yet invoiced"},
		{"2231", "Stock Interim Delivery", "ST_LIAB", false, false, false, "Interim account for goods delivered not yet invoiced"},
		{"2300", "Unearned Revenue", "ST_LIAB", false, false, false, "Deferred revenue"},
		{"2400", "Short-term Loans", "ST_LIAB", false, false, true, "Short-term borrowings"},
		{"2500", "Long-term Loans", "LT_LIAB", false, false, true, "Long-term borrowings"},

		// Equity (3xxx)
		{"3000", "Owner's Equity", "EQUITY", false, false, false, "Owner's capital"},
		{"3100", "Share Capital", "EQUITY", false, false, false, "Issued share capital"},
		{"3200", "Retained Earnings", "RETAIN", false, false, false, "Accumulated profits"},
		{"3300", "Current Year Earnings", "RETAIN", false, false, false, "Current period profit/loss"},
		{"3400", "Dividends", "EQUITY", false, false, false, "Dividends declared"},

		// Revenue (4xxx)
		{"4000", "Sales Revenue", "REVENUE", false, false, false, "Revenue from sales"},
		{"4100", "Service Revenue", "REVENUE", false, false, false, "Revenue from services"},
		{"4200", "Product Sales", "REVENUE", false, false, false, "Revenue from product sales"},
		{"4900", "Other Income", "OTHER_INC", false, false, false, "Miscellaneous income"},
		{"4910", "Interest Income", "OTHER_INC", false, false, false, "Interest earned"},
		{"4920", "Foreign Exchange Gain", "OTHER_INC", false, false, false, "Gain on foreign exchange"},

		// Cost of Goods Sold (5xxx)
		{"5000", "Cost of Goods Sold", "COGS", false, false, false, "Direct cost of goods sold"},
		{"5100", "Direct Materials", "COGS", false, false, false, "Cost of raw materials used"},
		{"5200", "Direct Labor", "COGS", false, false, false, "Direct labor costs"},
		{"5300", "Manufacturing Overhead", "COGS", false, false, false, "Manufacturing overhead"},

		// Operating Expenses (6xxx)
		{"6000", "Salaries & Wages", "OPEX", false, false, false, "Employee salaries and wages"},
		{"6100", "Rent Expense", "OPEX", false, false, false, "Rent and lease payments"},
		{"6200", "Utilities", "OPEX", false, false, false, "Electricity, water, gas"},
		{"6300", "Office Supplies", "OPEX", false, false, false, "Office supplies expense"},
		{"6400", "Insurance Expense", "OPEX", false, false, false, "Insurance premiums"},
		{"6500", "Depreciation Expense", "OPEX", false, false, false, "Depreciation of assets"},
		{"6600", "Advertising & Marketing", "OPEX", false, false, false, "Marketing expenses"},
		{"6700", "Professional Fees", "OPEX", false, false, false, "Legal, accounting fees"},
		{"6800", "Travel & Entertainment", "OPEX", false, false, false, "Business travel expenses"},
		{"6900", "Miscellaneous Expense", "OPEX", false, false, false, "Other operating expenses"},

		// Other Expenses (7xxx)
		{"7000", "Interest Expense", "OTHER_EXP", false, false, false, "Interest on borrowings"},
		{"7100", "Bank Charges", "OTHER_EXP", false, false, false, "Bank fees and charges"},
		{"7200", "Foreign Exchange Loss", "OTHER_EXP", false, false, false, "Loss on foreign exchange"},
		{"7900", "Other Expenses", "OTHER_EXP", false, false, false, "Miscellaneous expenses"},
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

	h.log.Info("Created default chart of accounts", "tenant_id", tenantID, "org_id", orgID, "account_count", len(defaultAccounts))
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

	// Define default journals
	defaultJournals := []struct {
		code              string
		name              string
		journalType       string
		defaultDebitCode  string
		defaultCreditCode string
	}{
		{"GEN", "General Journal", "general", "", ""},
		{"SAL", "Sales Journal", "sales", "1200", "4000"},         // AR debit, Sales Revenue credit
		{"PUR", "Purchase Journal", "purchase", "5000", "2000"},   // COGS debit, AP credit
		{"CASH", "Cash Journal", "cash", "1000", "1000"},          // Cash
		{"BANK", "Bank Journal", "bank", "1100", "1100"},          // Bank
		{"MISC", "Miscellaneous Journal", "miscellaneous", "", ""},
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

		_, err := h.db.Exec(`
			INSERT INTO journals (
				id, tenant_id, organization_id, code, name, type,
				default_debit_account_id, default_credit_account_id,
				is_active, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tenant_id, code) DO UPDATE
				SET organization_id = EXCLUDED.organization_id,
				    updated_at = NOW()
			WHERE journals.organization_id IS NULL
		`,
			id, tenantID, orgID, j.code, j.name, j.journalType,
			defaultDebitID, defaultCreditID,
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

	// Find a default account for payment methods (cash account 1000)
	var cashAccountID *uuid.UUID
	var acctID uuid.UUID
	if err := h.db.QueryRow(`
		SELECT id FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND code = '1000' AND deleted_at IS NULL
	`, tenantID, orgID).Scan(&acctID); err == nil {
		cashAccountID = &acctID
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

		h.db.Exec(`
			INSERT INTO journal_payment_methods (id, tenant_id, journal_id, payment_method_id, direction, name, is_active, created_at)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, '', true, $5)
			ON CONFLICT (journal_id, payment_method_id, direction) DO NOTHING
		`, tenantID, journalID, pmID, link.direction, now)
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
		response.BadRequest(c, err.Error())
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
