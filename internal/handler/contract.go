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

// ListContracts returns a paginated list of contracts
func (h *Handler) ListContracts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	status := c.Query("status")
	supplierID := c.Query("supplier_id")

	baseQuery := `
		SELECT id, tenant_id, contract_number, supplier_id, supplier_name, title, description,
			   contract_type, start_date, end_date, value, currency, payment_terms, terms,
			   auto_renew, renewal_notice_days, status, created_at, updated_at
		FROM contracts
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM contracts WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if status != "" && status != "all" {
		argCount++
		baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	if supplierID != "" {
		if id, err := uuid.Parse(supplierID); err == nil {
			argCount++
			baseQuery += fmt.Sprintf(" AND supplier_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND supplier_id = $%d", argCount)
			args = append(args, id)
		}
	}

	baseQuery += " ORDER BY created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count contracts", "error", err)
		response.InternalError(c, "Failed to count contracts")
		return
	}

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list contracts", "error", err)
		response.InternalError(c, "Failed to list contracts")
		return
	}
	defer rows.Close()

	contracts := make([]*entity.ContractResponse, 0)
	for rows.Next() {
		var contract entity.Contract
		var supplierID sql.NullString
		var description, paymentTerms, terms sql.NullString

		if err := rows.Scan(
			&contract.ID, &contract.TenantID, &contract.ContractNumber, &supplierID,
			&contract.SupplierName, &contract.Title, &description, &contract.ContractType,
			&contract.StartDate, &contract.EndDate, &contract.Value, &contract.Currency,
			&paymentTerms, &terms, &contract.AutoRenew, &contract.RenewalNoticeDays,
			&contract.Status, &contract.CreatedAt, &contract.UpdatedAt,
		); err != nil {
			h.log.Error("Failed to scan contract", "error", err)
			continue
		}

		if supplierID.Valid {
			if id, err := uuid.Parse(supplierID.String); err == nil {
				contract.SupplierID = &id
			}
		}
		if description.Valid {
			contract.Description = &description.String
		}
		if paymentTerms.Valid {
			contract.PaymentTerms = &paymentTerms.String
		}
		if terms.Valid {
			contract.Terms = &terms.String
		}

		contracts = append(contracts, contract.ToResponse())
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

	var input entity.CreateContractInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Generate contract number if not provided
	contractNumber := input.ContractNumber
	if contractNumber == "" {
		contractNumber = fmt.Sprintf("CNT-%d-%d", time.Now().Year(), time.Now().UnixNano()%10000)
	}

	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format")
		return
	}

	endDate, err := time.Parse("2006-01-02", input.EndDate)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format")
		return
	}

	id := uuid.New()
	now := time.Now()

	var supplierID *uuid.UUID
	if input.SupplierID != "" {
		if parsedID, err := uuid.Parse(input.SupplierID); err == nil {
			supplierID = &parsedID
		}
	}

	currency := input.Currency
	if currency == "" {
		currency = "UZS"
	}

	query := `
		INSERT INTO contracts (
			id, tenant_id, contract_number, supplier_id, supplier_name, title, description,
			contract_type, start_date, end_date, value, currency, payment_terms, terms,
			auto_renew, renewal_notice_days, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING id
	`

	var description, paymentTerms, terms *string
	if input.Description != "" {
		description = &input.Description
	}
	if input.PaymentTerms != "" {
		paymentTerms = &input.PaymentTerms
	}
	if input.Terms != "" {
		terms = &input.Terms
	}

	renewalNoticeDays := input.RenewalNoticeDays
	if renewalNoticeDays == 0 {
		renewalNoticeDays = 30
	}

	if err := h.db.QueryRow(query,
		id, tenantID, contractNumber, supplierID, input.SupplierName, input.Title, description,
		input.ContractType, startDate, endDate, input.Value, currency, paymentTerms, terms,
		input.AutoRenew, renewalNoticeDays, "draft", now, now,
	).Scan(&id); err != nil {
		h.log.Error("Failed to create contract", "error", err)
		if strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Contract with this number already exists")
			return
		}
		response.InternalError(c, "Failed to create contract")
		return
	}

	contract := &entity.Contract{
		ID:                id,
		TenantID:          tenantID,
		ContractNumber:    contractNumber,
		SupplierID:        supplierID,
		SupplierName:      input.SupplierName,
		Title:             input.Title,
		Description:       description,
		ContractType:      input.ContractType,
		StartDate:         startDate,
		EndDate:           endDate,
		Value:             input.Value,
		Currency:          currency,
		PaymentTerms:      paymentTerms,
		Terms:             terms,
		AutoRenew:         input.AutoRenew,
		RenewalNoticeDays: renewalNoticeDays,
		Status:            "draft",
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	response.Created(c, contract.ToResponse())
}

// GetContract returns a single contract
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
		SELECT id, tenant_id, contract_number, supplier_id, supplier_name, title, description,
			   contract_type, start_date, end_date, value, currency, payment_terms, terms,
			   auto_renew, renewal_notice_days, status, created_at, updated_at
		FROM contracts
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var contract entity.Contract
	var supplierID sql.NullString
	var description, paymentTerms, terms sql.NullString

	if err := h.db.QueryRow(query, id, tenantID).Scan(
		&contract.ID, &contract.TenantID, &contract.ContractNumber, &supplierID,
		&contract.SupplierName, &contract.Title, &description, &contract.ContractType,
		&contract.StartDate, &contract.EndDate, &contract.Value, &contract.Currency,
		&paymentTerms, &terms, &contract.AutoRenew, &contract.RenewalNoticeDays,
		&contract.Status, &contract.CreatedAt, &contract.UpdatedAt,
	); err == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	} else if err != nil {
		h.log.Error("Failed to get contract", "error", err)
		response.InternalError(c, "Failed to get contract")
		return
	}

	if supplierID.Valid {
		if id, err := uuid.Parse(supplierID.String); err == nil {
			contract.SupplierID = &id
		}
	}
	if description.Valid {
		contract.Description = &description.String
	}
	if paymentTerms.Valid {
		contract.PaymentTerms = &paymentTerms.String
	}
	if terms.Valid {
		contract.Terms = &terms.String
	}

	response.Success(c, contract.ToResponse())
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

	var input entity.UpdateContractInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.SupplierID != nil {
		if *input.SupplierID != "" {
			if parsedID, err := uuid.Parse(*input.SupplierID); err == nil {
				addUpdate("supplier_id", parsedID)
			}
		}
	}
	if input.SupplierName != nil {
		addUpdate("supplier_name", *input.SupplierName)
	}
	if input.Title != nil {
		addUpdate("title", *input.Title)
	}
	if input.Description != nil {
		addUpdate("description", *input.Description)
	}
	if input.ContractType != nil {
		addUpdate("contract_type", *input.ContractType)
	}
	if input.StartDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.StartDate); err == nil {
			addUpdate("start_date", parsed)
		}
	}
	if input.EndDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.EndDate); err == nil {
			addUpdate("end_date", parsed)
		}
	}
	if input.Value != nil {
		addUpdate("value", *input.Value)
	}
	if input.Currency != nil {
		addUpdate("currency", *input.Currency)
	}
	if input.PaymentTerms != nil {
		addUpdate("payment_terms", *input.PaymentTerms)
	}
	if input.Terms != nil {
		addUpdate("terms", *input.Terms)
	}
	if input.AutoRenew != nil {
		addUpdate("auto_renew", *input.AutoRenew)
	}
	if input.RenewalNoticeDays != nil {
		addUpdate("renewal_notice_days", *input.RenewalNoticeDays)
	}
	if input.Status != nil {
		addUpdate("status", *input.Status)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addUpdate("updated_at", time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE contracts SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	if err := h.db.QueryRow(query, args...).Scan(&returnedID); err == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	} else if err != nil {
		h.log.Error("Failed to update contract", "error", err)
		response.InternalError(c, "Failed to update contract")
		return
	}

	h.GetContract(c)
}

// DeleteContract soft-deletes a contract
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

	query := `
		UPDATE contracts SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete contract", "error", err)
		response.InternalError(c, "Failed to delete contract")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Contract")
		return
	}

	response.NoContent(c)
}
