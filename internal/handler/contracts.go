package handler

import (
	"database/sql"
	"fmt"
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
// PROCUREMENT CONTRACT HANDLERS
// =====================================================

// ListContracts returns a paginated list of contracts
func (h *Handler) ListContracts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	search := c.Query("search")
	status := c.Query("status")
	vendorID := c.Query("vendor_id")
	expiringSoon := c.Query("expiring_soon") == "true"

	baseQuery := `
		SELECT c.id, c.contract_number, c.title, c.vendor_id, v.name as vendor_name,
			   c.contract_type, c.status, c.start_date, c.end_date, c.value,
			   c.terms, c.description, c.auto_renewal, c.renewal_term_days, c.notes,
			   CASE WHEN c.end_date IS NOT NULL THEN (c.end_date - CURRENT_DATE) ELSE 0 END as days_to_expiry,
			   c.created_at, c.updated_at
		FROM procurement_contracts c
		LEFT JOIN contacts v ON c.vendor_id = v.id
		WHERE c.tenant_id = $1 AND c.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM procurement_contracts c WHERE c.tenant_id = $1 AND c.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND c.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND c.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND c.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND c.status = $%d", argCount)
		args = append(args, status)
	}

	if vendorID != "" {
		if vid, err := uuid.Parse(vendorID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND c.vendor_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND c.vendor_id = $%d", argCount)
			args = append(args, vid)
		}
	}

	if expiringSoon {
		baseQuery += " AND c.end_date IS NOT NULL AND c.end_date <= CURRENT_DATE + INTERVAL '30 days'"
		countQuery += " AND c.end_date IS NOT NULL AND c.end_date <= CURRENT_DATE + INTERVAL '30 days'"
	}

	if search != "" {
		argCount++
		searchFilter := fmt.Sprintf(" AND (c.contract_number ILIKE $%d OR c.title ILIKE $%d OR v.name ILIKE $%d)", argCount, argCount, argCount)
		baseQuery += searchFilter
		countQuery += searchFilter
		args = append(args, "%"+search+"%")
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count contracts", "error", err)
		response.InternalError(c, "Failed to list contracts")
		return
	}

	baseQuery += " ORDER BY c.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list contracts", "error", err)
		response.InternalError(c, "Failed to list contracts")
		return
	}
	defer rows.Close()

	contracts := make([]*entity.ContractResponse, 0)
	for rows.Next() {
		var contract entity.ContractResponse
		var endDate sql.NullTime
		var terms, description, notes sql.NullString

		err := rows.Scan(
			&contract.ID, &contract.ContractNumber, &contract.Title, &contract.VendorID, &contract.VendorName,
			&contract.ContractType, &contract.Status, &contract.StartDate, &endDate, &contract.Value,
			&terms, &description, &contract.AutoRenewal, &contract.RenewalTermDays, &notes,
			&contract.DaysToExpiry, &contract.CreatedAt, &contract.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan contract", "error", err)
			continue
		}

		if endDate.Valid {
			contract.EndDate = &endDate.Time
		}
		if terms.Valid {
			contract.Terms = &terms.String
		}
		if description.Valid {
			contract.Description = &description.String
		}
		if notes.Valid {
			contract.Notes = &notes.String
		}

		contracts = append(contracts, &contract)
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, contracts, pagination)
}

// CreateContract creates a new contract
func (h *Handler) CreateContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateContractInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid vendor ID")
		return
	}

	// Verify vendor exists
	var vendorName string
	err = h.db.QueryRow("SELECT name FROM contacts WHERE id = $1 AND tenant_id = $2 AND type IN ('vendor', 'both') AND deleted_at IS NULL", vendorID, tenantID).Scan(&vendorName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Vendor")
		return
	}
	if err != nil {
		h.log.Error("Failed to verify vendor", "error", err)
		response.InternalError(c, "Failed to create contract")
		return
	}

	id := uuid.New()
	now := time.Now()

	// Generate contract number
	var count int
	h.db.QueryRow("SELECT COUNT(*) FROM procurement_contracts WHERE tenant_id = $1", tenantID).Scan(&count)
	contractNumber := fmt.Sprintf("CNT-%s-%04d", now.Format("2006"), count+1)

	// Parse dates
	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start date")
		return
	}

	var endDate *time.Time
	if input.EndDate != "" {
		if ed, err := time.Parse("2006-01-02", input.EndDate); err == nil {
			endDate = &ed
		}
	}

	// Parse optional UUID
	var currencyID *uuid.UUID
	if input.CurrencyID != "" {
		if cid, err := uuid.Parse(input.CurrencyID); err == nil {
			currencyID = &cid
		}
	}

	// Prepare optional strings
	var terms, description, notes *string
	if input.Terms != "" {
		terms = &input.Terms
	}
	if input.Description != "" {
		description = &input.Description
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO procurement_contracts (
			id, tenant_id, organization_id, contract_number, title, vendor_id, vendor_name, contract_type, status,
			start_date, end_date, value, currency_id, terms, description,
			auto_renewal, renewal_term_days, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	`

	_, err = h.db.Exec(query,
		id, tenantID, orgIDPtr, contractNumber, input.Title, vendorID, vendorName, input.ContractType, entity.ContractStatusDraft,
		startDate, endDate, input.Value, currencyID, terms, description,
		input.AutoRenewal, input.RenewalTermDays, notes, userID, now, now,
	)
	if err != nil {
		h.log.Error("Failed to insert contract", "error", err)
		response.InternalError(c, "Failed to create contract")
		return
	}

	// Calculate days to expiry
	daysToExpiry := 0
	if endDate != nil {
		daysToExpiry = int(endDate.Sub(now).Hours() / 24)
	}

	resp := &entity.ContractResponse{
		ID:              id,
		ContractNumber:  contractNumber,
		Title:           input.Title,
		VendorID:        vendorID,
		VendorName:      vendorName,
		ContractType:    entity.ContractType(input.ContractType),
		Status:          entity.ContractStatusDraft,
		StartDate:       startDate,
		EndDate:         endDate,
		Value:           input.Value,
		Terms:           terms,
		Description:     description,
		AutoRenewal:     input.AutoRenewal,
		RenewalTermDays: input.RenewalTermDays,
		Notes:           notes,
		DaysToExpiry:    daysToExpiry,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	response.Created(c, resp)
}

// GetContract returns a single contract by ID
func (h *Handler) GetContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	query := `
		SELECT c.id, c.contract_number, c.title, c.vendor_id, v.name as vendor_name,
			   c.contract_type, c.status, c.start_date, c.end_date, c.value,
			   c.terms, c.description, c.auto_renewal, c.renewal_term_days, c.notes,
			   CASE WHEN c.end_date IS NOT NULL THEN (c.end_date - CURRENT_DATE) ELSE 0 END as days_to_expiry,
			   c.created_at, c.updated_at
		FROM procurement_contracts c
		LEFT JOIN contacts v ON c.vendor_id = v.id
		WHERE c.id = $1 AND c.tenant_id = $2 AND c.deleted_at IS NULL
	`

	var contract entity.ContractResponse
	var endDate sql.NullTime
	var terms, description, notes sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&contract.ID, &contract.ContractNumber, &contract.Title, &contract.VendorID, &contract.VendorName,
		&contract.ContractType, &contract.Status, &contract.StartDate, &endDate, &contract.Value,
		&terms, &description, &contract.AutoRenewal, &contract.RenewalTermDays, &notes,
		&contract.DaysToExpiry, &contract.CreatedAt, &contract.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	}
	if err != nil {
		h.log.Error("Failed to get contract", "error", err)
		response.InternalError(c, "Failed to get contract")
		return
	}

	if endDate.Valid {
		contract.EndDate = &endDate.Time
	}
	if terms.Valid {
		contract.Terms = &terms.String
	}
	if description.Valid {
		contract.Description = &description.String
	}
	if notes.Valid {
		contract.Notes = &notes.String
	}

	response.Success(c, contract)
}

// UpdateContract updates an existing contract
func (h *Handler) UpdateContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	// Check if contract exists
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM procurement_contracts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)", id, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Contract")
		return
	}

	var input entity.UpdateContractInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Title != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("title = $%d", argCount))
		args = append(args, *input.Title)
	}
	if input.ContractType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("contract_type = $%d", argCount))
		args = append(args, *input.ContractType)
	}
	if input.EndDate != nil {
		if ed, err := time.Parse("2006-01-02", *input.EndDate); err == nil {
			argCount++
			updates = append(updates, fmt.Sprintf("end_date = $%d", argCount))
			args = append(args, ed)
		}
	}
	if input.Value != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("value = $%d", argCount))
		args = append(args, *input.Value)
	}
	if input.Terms != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("terms = $%d", argCount))
		args = append(args, *input.Terms)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.AutoRenewal != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("auto_renewal = $%d", argCount))
		args = append(args, *input.AutoRenewal)
	}
	if input.RenewalTermDays != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("renewal_term_days = $%d", argCount))
		args = append(args, *input.RenewalTermDays)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
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
		"UPDATE procurement_contracts SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update contract", "error", err)
		response.InternalError(c, "Failed to update contract")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Contract")
		return
	}

	response.Success(c, gin.H{"message": "Contract updated successfully"})
}

// DeleteContract soft deletes a contract
func (h *Handler) DeleteContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	// Check status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM procurement_contracts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	}

	if currentStatus != string(entity.ContractStatusDraft) && currentStatus != string(entity.ContractStatusTerminated) {
		response.BadRequest(c, "Only draft or terminated contracts can be deleted")
		return
	}

	_, err = h.db.Exec("UPDATE procurement_contracts SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND tenant_id = $3", time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete contract", "error", err)
		response.InternalError(c, "Failed to delete contract")
		return
	}

	response.NoContent(c)
}

// ActivateContract activates a draft contract
func (h *Handler) ActivateContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM procurement_contracts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	}

	if currentStatus != string(entity.ContractStatusDraft) {
		response.BadRequest(c, "Only draft contracts can be activated")
		return
	}

	_, err = h.db.Exec("UPDATE procurement_contracts SET status = $1, updated_at = $2 WHERE id = $3", entity.ContractStatusActive, time.Now(), id)
	if err != nil {
		h.log.Error("Failed to activate contract", "error", err)
		response.InternalError(c, "Failed to activate contract")
		return
	}

	response.Success(c, gin.H{"message": "Contract activated successfully", "status": entity.ContractStatusActive})
}

// TerminateContract terminates an active contract
func (h *Handler) TerminateContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM procurement_contracts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	}

	if currentStatus != string(entity.ContractStatusActive) {
		response.BadRequest(c, "Only active contracts can be terminated")
		return
	}

	_, err = h.db.Exec("UPDATE procurement_contracts SET status = $1, updated_at = $2 WHERE id = $3", entity.ContractStatusTerminated, time.Now(), id)
	if err != nil {
		h.log.Error("Failed to terminate contract", "error", err)
		response.InternalError(c, "Failed to terminate contract")
		return
	}

	response.Success(c, gin.H{"message": "Contract terminated successfully", "status": entity.ContractStatusTerminated})
}
