package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONTACT HANDLERS
// =====================================================

// ListContacts returns a paginated list of contacts
func (h *Handler) ListContacts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Parse filters
	search := c.Query("search")
	contactType := c.Query("type")
	industry := c.Query("industry")
	isActiveStr := c.Query("is_active")

	// Build query - LEFT JOIN supplier_performance to get average rating for vendors
	baseQuery := `
		SELECT c.id, c.tenant_id, c.type, c.code, c.name, c.legal_name, c.tax_id,
			   c.registration_number, c.industry, c.website, c.email, c.phone, c.fax,
			   c.billing_address, c.shipping_address, c.payment_terms, c.credit_limit,
			   c.current_balance, c.currency_id, c.tax_exempt, c.tags, c.notes, c.expected_revenue,
			   c.custom_fields, c.is_active, c.created_by, c.created_at, c.updated_at,
			   COALESCE(sp.avg_rating, 0) AS avg_rating,
			   COALESCE(sp.rating_count, 0) AS rating_count,
			   c.source_organization_id,
			   c.default_receivable_account_id, c.default_payable_account_id
		FROM contacts c
		LEFT JOIN (
			SELECT vendor_id, AVG(overall_rating) AS avg_rating, COUNT(*) AS rating_count
			FROM supplier_performance
			WHERE overall_rating > 0
			GROUP BY vendor_id
		) sp ON sp.vendor_id = c.id
		WHERE c.tenant_id = $1 AND c.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM contacts c WHERE c.tenant_id = $1 AND c.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND c.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if contactType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND c.type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND type = $%d", argCount)
		args = append(args, contactType)
	}

	if industry != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND c.industry = $%d", argCount)
		countQuery += fmt.Sprintf(" AND industry = $%d", argCount)
		args = append(args, industry)
	}

	if isActiveStr != "" {
		isActive := isActiveStr == "true"
		argCount++
		baseQuery += fmt.Sprintf(" AND c.is_active = $%d", argCount)
		countQuery += fmt.Sprintf(" AND is_active = $%d", argCount)
		args = append(args, isActive)
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (c.name ILIKE $%d OR c.code ILIKE $%d OR c.email ILIKE $%d OR c.phone ILIKE $%d)", argCount, argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	// Get count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count contacts", "error", err)
		response.InternalError(c, "Failed to list contacts")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY c.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list contacts", "error", err)
		response.InternalError(c, "Failed to list contacts")
		return
	}
	defer rows.Close()

	contacts := make([]*entity.ContactResponse, 0)
	for rows.Next() {
		var ct entity.Contact
		var legalName, taxID, regNum, industry, website, email, phone, fax, notes sql.NullString
		var currencyID, createdBy sql.NullString
		var billingAddr, shippingAddr, tags, customFields []byte
		var avgRating float64
		var ratingCount int
		var expectedRevenue sql.NullFloat64
		var sourceOrgID sql.NullString
		var defaultReceivableAccountID, defaultPayableAccountID sql.NullString

		err := rows.Scan(
			&ct.ID, &ct.TenantID, &ct.Type, &ct.Code, &ct.Name, &legalName, &taxID,
			&regNum, &industry, &website, &email, &phone, &fax,
			&billingAddr, &shippingAddr, &ct.PaymentTerms, &ct.CreditLimit,
			&ct.CurrentBalance, &currencyID, &ct.TaxExempt, &tags, &notes, &expectedRevenue,
			&customFields, &ct.IsActive, &createdBy, &ct.CreatedAt, &ct.UpdatedAt,
			&avgRating, &ratingCount, &sourceOrgID,
			&defaultReceivableAccountID, &defaultPayableAccountID,
		)
		if err != nil {
			h.log.Error("Failed to scan contact", "error", err)
			continue
		}

		resp := &entity.ContactResponse{
			ID:             ct.ID,
			Type:           ct.Type,
			Code:           ct.Code,
			Name:           ct.Name,
			PaymentTerms:   ct.PaymentTerms,
			CreditLimit:    ct.CreditLimit,
			CurrentBalance: ct.CurrentBalance,
			TaxExempt:      ct.TaxExempt,
			IsActive:       ct.IsActive,
			CreatedAt:      ct.CreatedAt,
			UpdatedAt:      ct.UpdatedAt,
		}

		// Set rating if available
		if avgRating > 0 {
			r := math.Round(avgRating*10) / 10
			resp.Rating = &r
			resp.RatingCount = &ratingCount
		}

		if legalName.Valid {
			resp.LegalName = &legalName.String
		}
		if taxID.Valid {
			resp.TaxID = &taxID.String
		}
		if email.Valid {
			resp.Email = &email.String
		}
		if phone.Valid {
			resp.Phone = &phone.String
		}

		// Parse addresses
		if len(billingAddr) > 0 {
			var addr entity.Address
			if json.Unmarshal(billingAddr, &addr) == nil {
				resp.BillingAddress = &addr
			}
		}
		if len(shippingAddr) > 0 {
			var addr entity.Address
			if json.Unmarshal(shippingAddr, &addr) == nil {
				resp.ShippingAddress = &addr
			}
		}

		// Parse tags
		if len(tags) > 0 {
			json.Unmarshal(tags, &resp.Tags)
		}

		// Parse custom_fields
		if len(customFields) > 0 {
			var cf map[string]interface{}
			if json.Unmarshal(customFields, &cf) == nil {
				resp.CustomFields = cf
			}
		}

		// Set industry
		if industry.Valid {
			resp.Industry = &industry.String
		}
		if notes.Valid {
			resp.Notes = &notes.String
		}
		if expectedRevenue.Valid {
			resp.ExpectedRevenue = &expectedRevenue.Float64
		}
		if sourceOrgID.Valid {
			resp.SourceOrganizationID = &sourceOrgID.String
		}
		if defaultReceivableAccountID.Valid {
			resp.DefaultReceivableAccountID = &defaultReceivableAccountID.String
		}
		if defaultPayableAccountID.Valid {
			resp.DefaultPayableAccountID = &defaultPayableAccountID.String
		}

		contacts = append(contacts, resp)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, contacts, pagination)
}

// CreateContact creates a new contact
func (h *Handler) CreateContact(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateContactInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Set default contact type if not provided
	if input.Type == "" {
		input.Type = entity.ContactTypeCustomer
	}

	// Generate code if not provided
	if input.Code == "" {
		input.Code = fmt.Sprintf("%s-%d", strings.ToUpper(string(input.Type)[:3]), now.Unix()%100000)
	}

	// Check for duplicates before creating
	if enabled, checkFields := h.getDuplicateDetectionSettings(tenantID); enabled {
		duplicates := h.checkContactDuplicates(tenantID, input.Email, input.Phone, input.Name, checkFields)
		if len(duplicates) > 0 {
			response.ConflictWithData(c, "DUPLICATE_DETECTED", "Potential duplicate contact(s) found", map[string]interface{}{
				"duplicates": duplicates,
			})
			return
		}
	}

	// Prepare optional strings
	var legalName, taxID, regNum, industry, website, email, phone, fax, notes *string
	if input.LegalName != "" {
		legalName = &input.LegalName
	}
	if input.TaxID != "" {
		taxID = &input.TaxID
	}
	if input.RegistrationNumber != "" {
		regNum = &input.RegistrationNumber
	}
	if input.Industry != "" {
		industry = &input.Industry
	}
	if input.Website != "" {
		website = &input.Website
	}
	if input.Email != "" {
		email = &input.Email
	}
	if input.Phone != "" {
		phone = &input.Phone
	}
	if input.Fax != "" {
		fax = &input.Fax
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	// Marshal addresses
	var billingAddr, shippingAddr []byte
	if input.BillingAddress != nil {
		billingAddr, _ = json.Marshal(input.BillingAddress)
	} else {
		billingAddr = []byte("{}")
	}
	if input.ShippingAddress != nil {
		shippingAddr, _ = json.Marshal(input.ShippingAddress)
	} else {
		shippingAddr = []byte("{}")
	}

	// Marshal tags
	var tags []byte
	if len(input.Tags) > 0 {
		tags, _ = json.Marshal(input.Tags)
	} else {
		tags = []byte("[]")
	}

	// Marshal custom_fields
	var customFields []byte
	if input.CustomFields != nil && len(input.CustomFields) > 0 {
		customFields, _ = json.Marshal(input.CustomFields)
	} else {
		customFields = []byte("{}")
	}

	// Get organization ID from context and validate it exists
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		var exists bool
		_ = h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1 AND tenant_id = $2)`, orgID, tenantID).Scan(&exists)
		if exists {
			orgIDPtr = &orgID
		}
	}

	// Parse optional account IDs
	var receivableAccountID, payableAccountID *uuid.UUID
	if input.DefaultReceivableAccountID != "" {
		if uid, err := uuid.Parse(input.DefaultReceivableAccountID); err == nil {
			receivableAccountID = &uid
		}
	}
	if input.DefaultPayableAccountID != "" {
		if uid, err := uuid.Parse(input.DefaultPayableAccountID); err == nil {
			payableAccountID = &uid
		}
	}

	query := `
		INSERT INTO contacts (
			id, tenant_id, organization_id, type, code, name, legal_name, tax_id,
			registration_number, industry, website, email, phone, fax,
			billing_address, shipping_address, payment_terms, credit_limit,
			current_balance, tax_exempt, tags, notes, expected_revenue, custom_fields,
			is_active, created_by, created_at, updated_at,
			default_receivable_account_id, default_payable_account_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, input.Type, input.Code, input.Name, legalName, taxID,
		regNum, industry, website, email, phone, fax,
		billingAddr, shippingAddr, input.PaymentTerms, input.CreditLimit,
		0, input.TaxExempt, tags, notes, input.ExpectedRevenue, customFields,
		true, userID, now, now,
		receivableAccountID, payableAccountID,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create contact", "error", err)
		response.InternalError(c, "Failed to create contact")
		return
	}

	resp := &entity.ContactResponse{
		ID:           id,
		Type:         input.Type,
		Code:         input.Code,
		Name:         input.Name,
		LegalName:    legalName,
		TaxID:        taxID,
		Email:        email,
		Phone:        phone,
		PaymentTerms: input.PaymentTerms,
		CreditLimit:  input.CreditLimit,
		TaxExempt:    input.TaxExempt,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if input.BillingAddress != nil {
		resp.BillingAddress = input.BillingAddress
	}
	if input.ShippingAddress != nil {
		resp.ShippingAddress = input.ShippingAddress
	}
	if len(input.Tags) > 0 {
		resp.Tags = input.Tags
	}
	resp.Notes = notes
	if input.ExpectedRevenue != nil {
		resp.ExpectedRevenue = input.ExpectedRevenue
	}
	if receivableAccountID != nil {
		s := receivableAccountID.String()
		resp.DefaultReceivableAccountID = &s
	}
	if payableAccountID != nil {
		s := payableAccountID.String()
		resp.DefaultPayableAccountID = &s
	}

	response.Created(c, resp)
}

// GetContact returns a single contact by ID
func (h *Handler) GetContact(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	query := `
		SELECT id, tenant_id, type, code, name, legal_name, tax_id,
			   registration_number, industry, website, email, phone, fax,
			   billing_address, shipping_address, payment_terms, credit_limit,
			   current_balance, currency_id, tax_exempt, tags, notes, expected_revenue,
			   custom_fields, is_active, created_by, created_at, updated_at,
			   default_receivable_account_id, default_payable_account_id
		FROM contacts
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var ct entity.Contact
	var legalName, taxID, regNum, industry, website, email, phone, fax, notes sql.NullString
	var currencyID, createdBy sql.NullString
	var billingAddr, shippingAddr, tags, customFields []byte
	var expectedRevenue sql.NullFloat64
	var defaultReceivableAccountID, defaultPayableAccountID sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&ct.ID, &ct.TenantID, &ct.Type, &ct.Code, &ct.Name, &legalName, &taxID,
		&regNum, &industry, &website, &email, &phone, &fax,
		&billingAddr, &shippingAddr, &ct.PaymentTerms, &ct.CreditLimit,
		&ct.CurrentBalance, &currencyID, &ct.TaxExempt, &tags, &notes, &expectedRevenue,
		&customFields, &ct.IsActive, &createdBy, &ct.CreatedAt, &ct.UpdatedAt,
		&defaultReceivableAccountID, &defaultPayableAccountID,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Contact")
		return
	}
	if err != nil {
		h.log.Error("Failed to get contact", "error", err)
		response.InternalError(c, "Failed to get contact")
		return
	}

	resp := &entity.ContactResponse{
		ID:             ct.ID,
		Type:           ct.Type,
		Code:           ct.Code,
		Name:           ct.Name,
		PaymentTerms:   ct.PaymentTerms,
		CreditLimit:    ct.CreditLimit,
		CurrentBalance: ct.CurrentBalance,
		TaxExempt:      ct.TaxExempt,
		IsActive:       ct.IsActive,
		CreatedAt:      ct.CreatedAt,
		UpdatedAt:      ct.UpdatedAt,
	}

	if legalName.Valid {
		resp.LegalName = &legalName.String
	}
	if taxID.Valid {
		resp.TaxID = &taxID.String
	}
	if email.Valid {
		resp.Email = &email.String
	}
	if phone.Valid {
		resp.Phone = &phone.String
	}

	if len(billingAddr) > 0 {
		var addr entity.Address
		if json.Unmarshal(billingAddr, &addr) == nil {
			resp.BillingAddress = &addr
		}
	}
	if len(shippingAddr) > 0 {
		var addr entity.Address
		if json.Unmarshal(shippingAddr, &addr) == nil {
			resp.ShippingAddress = &addr
		}
	}
	if len(tags) > 0 {
		json.Unmarshal(tags, &resp.Tags)
	}

	// Parse custom_fields
	if len(customFields) > 0 {
		var cf map[string]interface{}
		if json.Unmarshal(customFields, &cf) == nil {
			resp.CustomFields = cf
		}
	}

	// Set industry
	if industry.Valid {
		resp.Industry = &industry.String
	}
	if notes.Valid {
		resp.Notes = &notes.String
	}
	if expectedRevenue.Valid {
		resp.ExpectedRevenue = &expectedRevenue.Float64
	}
	if defaultReceivableAccountID.Valid {
		resp.DefaultReceivableAccountID = &defaultReceivableAccountID.String
	}
	if defaultPayableAccountID.Valid {
		resp.DefaultPayableAccountID = &defaultPayableAccountID.String
	}

	response.Success(c, resp)
}

// UpdateContact updates an existing contact
func (h *Handler) UpdateContact(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	var input entity.UpdateContactInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.LegalName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("legal_name = $%d", argCount))
		args = append(args, *input.LegalName)
	}
	if input.TaxID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("tax_id = $%d", argCount))
		args = append(args, *input.TaxID)
	}
	if input.RegistrationNumber != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("registration_number = $%d", argCount))
		args = append(args, *input.RegistrationNumber)
	}
	if input.Industry != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("industry = $%d", argCount))
		args = append(args, *input.Industry)
	}
	if input.Website != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("website = $%d", argCount))
		args = append(args, *input.Website)
	}
	if input.Email != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("email = $%d", argCount))
		args = append(args, *input.Email)
	}
	if input.Phone != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("phone = $%d", argCount))
		args = append(args, *input.Phone)
	}
	if input.Fax != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("fax = $%d", argCount))
		args = append(args, *input.Fax)
	}
	if input.BillingAddress != nil {
		argCount++
		billingAddr, _ := json.Marshal(input.BillingAddress)
		updates = append(updates, fmt.Sprintf("billing_address = $%d", argCount))
		args = append(args, billingAddr)
	}
	if input.ShippingAddress != nil {
		argCount++
		shippingAddr, _ := json.Marshal(input.ShippingAddress)
		updates = append(updates, fmt.Sprintf("shipping_address = $%d", argCount))
		args = append(args, shippingAddr)
	}
	if input.PaymentTerms != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("payment_terms = $%d", argCount))
		args = append(args, *input.PaymentTerms)
	}
	if input.CreditLimit != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("credit_limit = $%d", argCount))
		args = append(args, *input.CreditLimit)
	}
	if input.TaxExempt != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("tax_exempt = $%d", argCount))
		args = append(args, *input.TaxExempt)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.ExpectedRevenue != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("expected_revenue = $%d", argCount))
		args = append(args, *input.ExpectedRevenue)
	}
	if input.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
	}
	if len(input.Tags) > 0 {
		argCount++
		tags, _ := json.Marshal(input.Tags)
		updates = append(updates, fmt.Sprintf("tags = $%d", argCount))
		args = append(args, tags)
	}
	if input.CustomFields != nil && len(input.CustomFields) > 0 {
		argCount++
		customFields, _ := json.Marshal(input.CustomFields)
		updates = append(updates, fmt.Sprintf("custom_fields = $%d", argCount))
		args = append(args, customFields)
	}
	if input.DefaultReceivableAccountID != nil {
		argCount++
		if *input.DefaultReceivableAccountID == "" {
			updates = append(updates, fmt.Sprintf("default_receivable_account_id = $%d", argCount))
			args = append(args, nil)
		} else if uid, err := uuid.Parse(*input.DefaultReceivableAccountID); err == nil {
			updates = append(updates, fmt.Sprintf("default_receivable_account_id = $%d", argCount))
			args = append(args, uid)
		}
	}
	if input.DefaultPayableAccountID != nil {
		argCount++
		if *input.DefaultPayableAccountID == "" {
			updates = append(updates, fmt.Sprintf("default_payable_account_id = $%d", argCount))
			args = append(args, nil)
		} else if uid, err := uuid.Parse(*input.DefaultPayableAccountID); err == nil {
			updates = append(updates, fmt.Sprintf("default_payable_account_id = $%d", argCount))
			args = append(args, uid)
		}
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE conditions
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE contacts SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update contact", "error", err)
		response.InternalError(c, "Failed to update contact")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Contact")
		return
	}

	response.Success(c, gin.H{"message": "Contact updated successfully"})
}

// DeleteContact soft deletes a contact
func (h *Handler) DeleteContact(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	query := `
		UPDATE contacts
		SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete contact", "error", err)
		response.InternalError(c, "Failed to delete contact")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Contact")
		return
	}

	response.NoContent(c)
}

// ListContactPersons returns contact persons for a contact
func (h *Handler) ListContactPersons(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	contactIDStr := c.Param("id")
	contactID, err := uuid.Parse(contactIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	// Verify contact belongs to tenant
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)", contactID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Contact")
		return
	}

	query := `
		SELECT id, contact_id, first_name, last_name, title, email, phone, mobile, is_primary, department, notes, created_at, updated_at
		FROM contact_persons
		WHERE contact_id = $1
		ORDER BY is_primary DESC, created_at ASC
	`

	rows, err := h.db.Query(query, contactID)
	if err != nil {
		h.log.Error("Failed to list contact persons", "error", err)
		response.InternalError(c, "Failed to list contact persons")
		return
	}
	defer rows.Close()

	persons := make([]entity.ContactPerson, 0)
	for rows.Next() {
		var cp entity.ContactPerson
		var title, email, phone, mobile, dept, notes sql.NullString

		err := rows.Scan(
			&cp.ID, &cp.ContactID, &cp.FirstName, &cp.LastName, &title, &email, &phone, &mobile, &cp.IsPrimary, &dept, &notes, &cp.CreatedAt, &cp.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if title.Valid {
			cp.Title = &title.String
		}
		if email.Valid {
			cp.Email = &email.String
		}
		if phone.Valid {
			cp.Phone = &phone.String
		}
		if mobile.Valid {
			cp.Mobile = &mobile.String
		}
		if dept.Valid {
			cp.Department = &dept.String
		}
		if notes.Valid {
			cp.Notes = &notes.String
		}

		persons = append(persons, cp)
	}

	response.Success(c, persons)
}

// CreateContactPerson creates a new contact person
func (h *Handler) CreateContactPerson(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	contactIDStr := c.Param("id")
	contactID, err := uuid.Parse(contactIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	// Verify contact belongs to tenant
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)", contactID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Contact")
		return
	}

	var input entity.CreateContactPersonInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()

	var title, email, phone, mobile, dept, notes *string
	if input.Title != "" {
		title = &input.Title
	}
	if input.Email != "" {
		email = &input.Email
	}
	if input.Phone != "" {
		phone = &input.Phone
	}
	if input.Mobile != "" {
		mobile = &input.Mobile
	}
	if input.Department != "" {
		dept = &input.Department
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	query := `
		INSERT INTO contact_persons (id, contact_id, first_name, last_name, title, email, phone, mobile, is_primary, department, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id
	`

	err = h.db.QueryRow(query,
		id, contactID, input.FirstName, input.LastName, title, email, phone, mobile, input.IsPrimary, dept, notes, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create contact person", "error", err)
		response.InternalError(c, "Failed to create contact person")
		return
	}

	resp := entity.ContactPerson{
		ID:         id,
		ContactID:  contactID,
		FirstName:  input.FirstName,
		LastName:   input.LastName,
		Title:      title,
		Email:      email,
		Phone:      phone,
		Mobile:     mobile,
		IsPrimary:  input.IsPrimary,
		Department: dept,
		Notes:      notes,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	response.Created(c, resp)
}

// RateSupplier adds a performance rating for a supplier/vendor
func (h *Handler) RateSupplier(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	contactIDStr := c.Param("id")
	contactID, err := uuid.Parse(contactIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	// Verify contact is a vendor and belongs to tenant
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND tenant_id = $2 AND type IN ('vendor', 'both') AND deleted_at IS NULL)", contactID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Vendor")
		return
	}

	var input struct {
		Rating  float64 `json:"rating" binding:"required,min=0,max=5"`
		Comment string  `json:"comment"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	id := uuid.New()

	query := `
		INSERT INTO supplier_performance (id, tenant_id, vendor_id, period_start, period_end, overall_rating, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`

	err = h.db.QueryRow(query,
		id, tenantID, contactID, now, now, input.Rating, input.Comment, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to rate supplier", "error", err)
		response.InternalError(c, "Failed to rate supplier")
		return
	}

	// Fetch updated average rating
	var avgRating float64
	var ratingCount int
	err = h.db.QueryRow(`
		SELECT COALESCE(AVG(overall_rating), 0), COUNT(*)
		FROM supplier_performance
		WHERE vendor_id = $1 AND tenant_id = $2 AND overall_rating > 0
	`, contactID, tenantID).Scan(&avgRating, &ratingCount)
	if err != nil {
		avgRating = input.Rating
		ratingCount = 1
	}

	roundedRating := math.Round(avgRating*10) / 10

	response.Created(c, gin.H{
		"id":           id,
		"rating":       roundedRating,
		"rating_count": ratingCount,
	})
}
