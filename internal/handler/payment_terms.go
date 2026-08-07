package handler

import (
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ==================== PAYMENT TERMS ====================

// ListPaymentTerms returns all payment terms for a tenant
func (h *Handler) ListPaymentTerms(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Filters
	activeOnly := c.Query("active_only") == "true"
	termType := c.Query("term_type")

	query := `
		SELECT id, tenant_id, code, name, description, term_type, due_days,
		       has_early_discount, discount_percentage, discount_days,
		       end_of_month_day, display_order, is_default, is_active,
		       created_by, created_at, updated_at
		FROM payment_terms
		WHERE tenant_id = $1
	`
	args := []interface{}{tenantID}
	argIndex := 2

	if activeOnly {
		query += " AND is_active = true"
	}

	if termType != "" {
		query += " AND term_type = $" + strconv.Itoa(argIndex)
		args = append(args, termType)
		argIndex++
	}

	// The count must reproduce BOTH filters, and active_only is inlined as
	// literal SQL while term_type is a bound param — mirroring only the bound
	// one would report a total the page can never reach.
	countQuery := "SELECT COUNT(*)" + query[strings.Index(query, "FROM payment_terms"):]

	query += " ORDER BY display_order ASC, name ASC"

	// Opt-in paging: this feeds the payment-terms dropdown in the quotation
	// template form, which needs the whole set when it sends no params.
	paginate, page, pageSize, offset := optPagination(c)
	filterArgs := append([]interface{}{}, args...)
	if paginate {
		query += " LIMIT $" + strconv.Itoa(argIndex) + " OFFSET $" + strconv.Itoa(argIndex+1)
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to fetch payment terms", "error", err)
		response.InternalServerError(c, "Failed to fetch payment terms")
		return
	}
	defer rows.Close()

	var terms []entity.PaymentTerm
	for rows.Next() {
		var term entity.PaymentTerm
		err := rows.Scan(
			&term.ID, &term.TenantID, &term.Code, &term.Name, &term.Description,
			&term.TermType, &term.DueDays, &term.HasEarlyDiscount, &term.DiscountPercentage,
			&term.DiscountDays, &term.EndOfMonthDay, &term.DisplayOrder, &term.IsDefault,
			&term.IsActive, &term.CreatedBy, &term.CreatedAt, &term.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan payment term", "error", err)
			response.InternalServerError(c, "Failed to scan payment term")
			return
		}
		terms = append(terms, term)
	}

	if terms == nil {
		terms = []entity.PaymentTerm{}
	}

	if !paginate {
		response.Success(c, terms)
		return
	}

	total := 0
	if err := h.db.QueryRow(countQuery, filterArgs...).Scan(&total); err != nil {
		h.log.Error("Failed to count payment terms", "error", err)
		total = len(terms)
	}
	response.Paginated(c, terms, page, pageSize, total)
}

// GetPaymentTerm returns a single payment term
func (h *Handler) GetPaymentTerm(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payment term ID")
		return
	}

	var term entity.PaymentTerm
	err = h.db.QueryRow(`
		SELECT id, tenant_id, code, name, description, term_type, due_days,
		       has_early_discount, discount_percentage, discount_days,
		       end_of_month_day, display_order, is_default, is_active,
		       created_by, created_at, updated_at
		FROM payment_terms
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&term.ID, &term.TenantID, &term.Code, &term.Name, &term.Description,
		&term.TermType, &term.DueDays, &term.HasEarlyDiscount, &term.DiscountPercentage,
		&term.DiscountDays, &term.EndOfMonthDay, &term.DisplayOrder, &term.IsDefault,
		&term.IsActive, &term.CreatedBy, &term.CreatedAt, &term.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payment term not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch payment term", "error", err)
		response.InternalServerError(c, "Failed to fetch payment term")
		return
	}

	response.Success(c, term)
}

// CreatePaymentTerm creates a new payment term
func (h *Handler) CreatePaymentTerm(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.PaymentTermInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// If this term is set as default, unset other defaults
	if input.IsDefault {
		_, err := h.db.Exec(`
			UPDATE payment_terms SET is_default = false
			WHERE tenant_id = $1 AND is_default = true
		`, tenantID)
		if err != nil {
			h.log.Error("Failed to update default terms", "error", err)
			response.InternalServerError(c, "Failed to update default terms")
			return
		}
	}

	var term entity.PaymentTerm
	err := h.db.QueryRow(`
		INSERT INTO payment_terms (
			tenant_id, code, name, description, term_type, due_days,
			has_early_discount, discount_percentage, discount_days,
			end_of_month_day, display_order, is_default, is_active, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, tenant_id, code, name, description, term_type, due_days,
		          has_early_discount, discount_percentage, discount_days,
		          end_of_month_day, display_order, is_default, is_active,
		          created_by, created_at, updated_at
	`,
		tenantID, input.Code, input.Name, input.Description, input.TermType, input.DueDays,
		input.HasEarlyDiscount, input.DiscountPercentage, input.DiscountDays,
		input.EndOfMonthDay, input.DisplayOrder, input.IsDefault, input.IsActive, userID,
	).Scan(
		&term.ID, &term.TenantID, &term.Code, &term.Name, &term.Description,
		&term.TermType, &term.DueDays, &term.HasEarlyDiscount, &term.DiscountPercentage,
		&term.DiscountDays, &term.EndOfMonthDay, &term.DisplayOrder, &term.IsDefault,
		&term.IsActive, &term.CreatedBy, &term.CreatedAt, &term.UpdatedAt,
	)
	if err != nil {
		h.log.Error("Failed to create payment term", "error", err)
		response.InternalServerError(c, "Failed to create payment term")
		return
	}

	response.Created(c, term)
}

// UpdatePaymentTerm updates an existing payment term
func (h *Handler) UpdatePaymentTerm(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payment term ID")
		return
	}

	var input entity.PaymentTermInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// If this term is set as default, unset other defaults
	if input.IsDefault {
		_, err := h.db.Exec(`
			UPDATE payment_terms SET is_default = false
			WHERE tenant_id = $1 AND is_default = true AND id != $2
		`, tenantID, id)
		if err != nil {
			h.log.Error("Failed to update default terms", "error", err)
			response.InternalServerError(c, "Failed to update default terms")
			return
		}
	}

	var term entity.PaymentTerm
	err = h.db.QueryRow(`
		UPDATE payment_terms SET
			code = $3, name = $4, description = $5, term_type = $6, due_days = $7,
			has_early_discount = $8, discount_percentage = $9, discount_days = $10,
			end_of_month_day = $11, display_order = $12, is_default = $13, is_active = $14,
			updated_at = $15
		WHERE id = $1 AND tenant_id = $2
		RETURNING id, tenant_id, code, name, description, term_type, due_days,
		          has_early_discount, discount_percentage, discount_days,
		          end_of_month_day, display_order, is_default, is_active,
		          created_by, created_at, updated_at
	`,
		id, tenantID, input.Code, input.Name, input.Description, input.TermType, input.DueDays,
		input.HasEarlyDiscount, input.DiscountPercentage, input.DiscountDays,
		input.EndOfMonthDay, input.DisplayOrder, input.IsDefault, input.IsActive, time.Now(),
	).Scan(
		&term.ID, &term.TenantID, &term.Code, &term.Name, &term.Description,
		&term.TermType, &term.DueDays, &term.HasEarlyDiscount, &term.DiscountPercentage,
		&term.DiscountDays, &term.EndOfMonthDay, &term.DisplayOrder, &term.IsDefault,
		&term.IsActive, &term.CreatedBy, &term.CreatedAt, &term.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payment term not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to update payment term", "error", err)
		response.InternalServerError(c, "Failed to update payment term")
		return
	}

	response.Success(c, term)
}

// DeletePaymentTerm deletes a payment term
func (h *Handler) DeletePaymentTerm(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payment term ID")
		return
	}

	// Check if term is in use
	var usageCount int
	err = h.db.QueryRow(`
		SELECT COUNT(*)
		FROM (
			SELECT id FROM sales_orders WHERE payment_term_id = $1
			UNION ALL
			SELECT id FROM sales_invoices WHERE payment_term_id = $1
			UNION ALL
			SELECT id FROM contacts WHERE payment_term_id = $1
			UNION ALL
			SELECT id FROM purchase_orders WHERE payment_term_id = $1
		) AS usage
	`, id).Scan(&usageCount)
	if err != nil {
		h.log.Error("Failed to check term usage", "error", err)
		response.InternalServerError(c, "Failed to check term usage")
		return
	}

	if usageCount > 0 {
		response.BadRequest(c, "Cannot delete payment term that is in use. Deactivate it instead.")
		return
	}

	result, err := h.db.Exec(`
		DELETE FROM payment_terms WHERE id = $1 AND tenant_id = $2
	`, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete payment term", "error", err)
		response.InternalServerError(c, "Failed to delete payment term")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Payment term not found")
		return
	}

	response.Success(c, gin.H{"message": "Payment term deleted successfully"})
}

// GetDefaultPaymentTerm returns the default payment term
func (h *Handler) GetDefaultPaymentTerm(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var term entity.PaymentTerm
	err := h.db.QueryRow(`
		SELECT id, tenant_id, code, name, description, term_type, due_days,
		       has_early_discount, discount_percentage, discount_days,
		       end_of_month_day, display_order, is_default, is_active,
		       created_by, created_at, updated_at
		FROM payment_terms
		WHERE tenant_id = $1 AND is_default = true AND is_active = true
		LIMIT 1
	`, tenantID).Scan(
		&term.ID, &term.TenantID, &term.Code, &term.Name, &term.Description,
		&term.TermType, &term.DueDays, &term.HasEarlyDiscount, &term.DiscountPercentage,
		&term.DiscountDays, &term.EndOfMonthDay, &term.DisplayOrder, &term.IsDefault,
		&term.IsActive, &term.CreatedBy, &term.CreatedAt, &term.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		// Return first active term if no default
		err = h.db.QueryRow(`
			SELECT id, tenant_id, code, name, description, term_type, due_days,
			       has_early_discount, discount_percentage, discount_days,
			       end_of_month_day, display_order, is_default, is_active,
			       created_by, created_at, updated_at
			FROM payment_terms
			WHERE tenant_id = $1 AND is_active = true
			ORDER BY display_order ASC
			LIMIT 1
		`, tenantID).Scan(
			&term.ID, &term.TenantID, &term.Code, &term.Name, &term.Description,
			&term.TermType, &term.DueDays, &term.HasEarlyDiscount, &term.DiscountPercentage,
			&term.DiscountDays, &term.EndOfMonthDay, &term.DisplayOrder, &term.IsDefault,
			&term.IsActive, &term.CreatedBy, &term.CreatedAt, &term.UpdatedAt,
		)
		if err == sql.ErrNoRows {
			response.NotFound(c, "No payment terms found")
			return
		}
	}
	if err != nil {
		h.log.Error("Failed to fetch default payment term", "error", err)
		response.InternalServerError(c, "Failed to fetch default payment term")
		return
	}

	response.Success(c, term)
}

// CalculateDueDate calculates the due date for a given payment term and invoice date
func (h *Handler) CalculateDueDate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input struct {
		PaymentTermID uuid.UUID `json:"payment_term_id" binding:"required"`
		InvoiceDate   string    `json:"invoice_date" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	invoiceDate, err := time.Parse("2006-01-02", input.InvoiceDate)
	if err != nil {
		response.BadRequest(c, "Invalid date format. Use YYYY-MM-DD")
		return
	}

	var term entity.PaymentTerm
	err = h.db.QueryRow(`
		SELECT id, tenant_id, code, name, description, term_type, due_days,
		       has_early_discount, discount_percentage, discount_days,
		       end_of_month_day, display_order, is_default, is_active,
		       created_by, created_at, updated_at
		FROM payment_terms
		WHERE id = $1 AND tenant_id = $2
	`, input.PaymentTermID, tenantID).Scan(
		&term.ID, &term.TenantID, &term.Code, &term.Name, &term.Description,
		&term.TermType, &term.DueDays, &term.HasEarlyDiscount, &term.DiscountPercentage,
		&term.DiscountDays, &term.EndOfMonthDay, &term.DisplayOrder, &term.IsDefault,
		&term.IsActive, &term.CreatedBy, &term.CreatedAt, &term.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Payment term not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch payment term", "error", err)
		response.InternalServerError(c, "Failed to fetch payment term")
		return
	}

	dueDate := term.CalculateDueDate(invoiceDate)
	earlyDiscountDate := term.CalculateEarlyDiscountDate(invoiceDate)

	result := gin.H{
		"invoice_date":        input.InvoiceDate,
		"due_date":            dueDate.Format("2006-01-02"),
		"payment_term":        term.Name,
		"has_early_discount":  term.HasEarlyDiscount,
		"discount_percentage": term.DiscountPercentage,
	}

	if earlyDiscountDate != nil {
		result["early_discount_date"] = earlyDiscountDate.Format("2006-01-02")
	}

	response.Success(c, result)
}

// GetPaymentTermStats returns statistics about payment terms
func (h *Handler) GetPaymentTermStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var stats entity.PaymentTermStats
	err := h.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM payment_terms WHERE tenant_id = $1) as total_terms,
			(SELECT COUNT(*) FROM payment_terms WHERE tenant_id = $1 AND is_active = true) as active_terms,
			(SELECT COUNT(DISTINCT pt.id) FROM payment_terms pt
			 LEFT JOIN sales_orders so ON so.payment_term_id = pt.id
			 LEFT JOIN sales_invoices i ON i.payment_term_id = pt.id
			 WHERE pt.tenant_id = $1 AND (so.id IS NOT NULL OR i.id IS NOT NULL)) as terms_with_orders
	`, tenantID).Scan(&stats.TotalTerms, &stats.ActiveTerms, &stats.TermsWithOrders)
	if err != nil {
		h.log.Error("Failed to fetch payment term stats", "error", err)
		response.InternalServerError(c, "Failed to fetch payment term stats")
		return
	}

	response.Success(c, stats)
}

// SetDefaultPaymentTerm sets a payment term as the default
func (h *Handler) SetDefaultPaymentTerm(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid payment term ID")
		return
	}

	// Unset all other defaults
	_, err = h.db.Exec(`
		UPDATE payment_terms SET is_default = false
		WHERE tenant_id = $1 AND is_default = true
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to unset default terms", "error", err)
		response.InternalServerError(c, "Failed to unset default terms")
		return
	}

	// Set the new default
	result, err := h.db.Exec(`
		UPDATE payment_terms SET is_default = true, updated_at = $3
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID, time.Now())
	if err != nil {
		h.log.Error("Failed to set default payment term", "error", err)
		response.InternalServerError(c, "Failed to set default payment term")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Payment term not found")
		return
	}

	response.Success(c, gin.H{"message": "Default payment term updated"})
}
