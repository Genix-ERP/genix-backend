package handler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/listparams"
	"github.com/genixerp/genix-backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ListDiscounts returns all discounts for a tenant
func (h *Handler) ListDiscounts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	status := c.Query("status")

	lp := listparams.Parse(c,
		[]string{"created_at", "code", "name", "discount_value", "valid_from", "valid_until", "status"},
		"created_at")

	query := `
		SELECT id, tenant_id, code, name, description, discount_type, discount_value,
			   min_order_amount, max_discount_amount, usage_limit, usage_per_customer, used_count,
			   applies_to, applicable_products, applicable_categories, applicable_customers,
			   new_customers_only, valid_from, valid_until, status, created_at, updated_at,
			   ` + dateExpiredSQL("valid_until") + ` AS is_expired,
			   ` + dateDaysRemainingSQL("valid_until") + ` AS days_remaining
		FROM discounts
		WHERE tenant_id = $1 AND deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM discounts WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" {
		argCount++
		query += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	// Search
	if lp.Search != "" {
		argCount++
		pattern := "%" + strings.ToLower(lp.Search) + "%"
		query += fmt.Sprintf(" AND (LOWER(code) LIKE $%d OR LOWER(name) LIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(code) LIKE $%d OR LOWER(name) LIKE $%d)", argCount, argCount)
		args = append(args, pattern)
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)

	query += lp.OrderClause("") + fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, lp.PageSize, lp.Offset())

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list discounts", "error", err)
		response.InternalError(c, "Failed to list discounts")
		return
	}
	defer rows.Close()

	var discounts []map[string]interface{}
	now := time.Now()

	for rows.Next() {
		var isExpired bool
		var daysRemaining sql.NullInt64
		var id, tenantIDScan uuid.UUID
		var code, name, discountType, appliesTo, status string
		var description sql.NullString
		var discountValue, minOrderAmount float64
		var maxDiscountAmount sql.NullFloat64
		var usageLimit, usagePerCustomer sql.NullInt32
		var usedCount int
		var applicableProducts, applicableCategories, applicableCustomers pq.StringArray
		var newCustomersOnly bool
		var validFrom, validUntil time.Time
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &tenantIDScan, &code, &name, &description, &discountType, &discountValue,
			&minOrderAmount, &maxDiscountAmount, &usageLimit, &usagePerCustomer, &usedCount,
			&appliesTo, &applicableProducts, &applicableCategories, &applicableCustomers,
			&newCustomersOnly, &validFrom, &validUntil, &status, &createdAt, &updatedAt,
			&isExpired, &daysRemaining,
		)
		if err != nil {
			h.log.Error("Failed to scan discount", "error", err)
			continue
		}

		// Auto-update status if expired
		actualStatus := status
		// isExpired comes from SQL now (dateExpiredSQL), the same CURRENT_DATE
		// the emitted is_expired field uses — so the status shown and the flag
		// beside it can never disagree.
		if status == "active" && isExpired {
			actualStatus = "expired"
			// Update in database
			if _, execErr := h.db.Exec("UPDATE discounts SET status = 'expired', updated_at = $1 WHERE id = $2", now, id); execErr != nil {
				h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE discounts", "error", execErr)
			}
		}

		discount := map[string]interface{}{
			"id":                 id.String(),
			"code":               code,
			"name":               name,
			"discount_type":      discountType,
			"discount_value":     discountValue,
			"min_order_amount":   minOrderAmount,
			"used_count":         usedCount,
			"applies_to":         appliesTo,
			"new_customers_only": newCustomersOnly,
			"valid_from":         validFrom.Format("2006-01-02"),
			"valid_until":        validUntil.Format("2006-01-02"),
			"is_expired":         isExpired,
			"status":             actualStatus,
			"created_at":         createdAt,
			"updated_at":         updatedAt,
		}

		// NULL valid_until means "no expiry" — emitted as null rather than 0,
		// which would read as "expires today".
		if daysRemaining.Valid {
			discount["days_remaining"] = daysRemaining.Int64
		} else {
			discount["days_remaining"] = nil
		}

		if description.Valid {
			discount["description"] = description.String
		}
		if maxDiscountAmount.Valid {
			discount["max_discount_amount"] = maxDiscountAmount.Float64
		}
		if usageLimit.Valid {
			discount["usage_limit"] = usageLimit.Int32
		}
		if usagePerCustomer.Valid {
			discount["usage_per_customer"] = usagePerCustomer.Int32
		}
		if len(applicableProducts) > 0 {
			discount["applicable_products"] = []string(applicableProducts)
		}
		if len(applicableCategories) > 0 {
			discount["applicable_categories"] = []string(applicableCategories)
		}
		if len(applicableCustomers) > 0 {
			discount["applicable_customers"] = []string(applicableCustomers)
		}

		discounts = append(discounts, discount)
	}

	if discounts == nil {
		discounts = []map[string]interface{}{}
	}

	response.Paginated(c, discounts, lp.Page, lp.PageSize, total)
}

// GetDiscount returns a single discount by ID
func (h *Handler) GetDiscount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	discountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid discount ID")
		return
	}

	var id uuid.UUID
	var code, name, discountType, appliesTo, status string
	var description sql.NullString
	var discountValue, minOrderAmount float64
	var maxDiscountAmount sql.NullFloat64
	var usageLimit, usagePerCustomer sql.NullInt32
	var usedCount int
	var applicableProducts, applicableCategories, applicableCustomers pq.StringArray
	var newCustomersOnly bool
	var validFrom, validUntil time.Time
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(`
		SELECT id, code, name, description, discount_type, discount_value,
			   min_order_amount, max_discount_amount, usage_limit, usage_per_customer, used_count,
			   applies_to, applicable_products, applicable_categories, applicable_customers,
			   new_customers_only, valid_from, valid_until, status, created_at, updated_at
		FROM discounts
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		discountID, tenantID,
	).Scan(
		&id, &code, &name, &description, &discountType, &discountValue,
		&minOrderAmount, &maxDiscountAmount, &usageLimit, &usagePerCustomer, &usedCount,
		&appliesTo, &applicableProducts, &applicableCategories, &applicableCustomers,
		&newCustomersOnly, &validFrom, &validUntil, &status, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Discount")
		return
	}
	if err != nil {
		h.log.Error("Failed to get discount", "error", err)
		response.InternalError(c, "Failed to get discount")
		return
	}

	discount := map[string]interface{}{
		"id":                 id.String(),
		"code":               code,
		"name":               name,
		"discount_type":      discountType,
		"discount_value":     discountValue,
		"min_order_amount":   minOrderAmount,
		"used_count":         usedCount,
		"applies_to":         appliesTo,
		"new_customers_only": newCustomersOnly,
		"valid_from":         validFrom.Format("2006-01-02"),
		"valid_until":        validUntil.Format("2006-01-02"),
		"status":             status,
		"created_at":         createdAt,
		"updated_at":         updatedAt,
	}

	if description.Valid {
		discount["description"] = description.String
	}
	if maxDiscountAmount.Valid {
		discount["max_discount_amount"] = maxDiscountAmount.Float64
	}
	if usageLimit.Valid {
		discount["usage_limit"] = usageLimit.Int32
	}
	if usagePerCustomer.Valid {
		discount["usage_per_customer"] = usagePerCustomer.Int32
	}

	response.Success(c, discount)
}

// CreateDiscount creates a new discount
func (h *Handler) CreateDiscount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Get organization ID from middleware header
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var input struct {
		Code                 string   `json:"code" binding:"required"`
		Name                 string   `json:"name" binding:"required"`
		Description          string   `json:"description"`
		DiscountType         string   `json:"discount_type" binding:"required"` // percentage, fixed_amount
		DiscountValue        float64  `json:"discount_value"`
		MinOrderAmount       float64  `json:"min_order_amount"`
		MaxDiscountAmount    *float64 `json:"max_discount_amount"`
		UsageLimit           *int     `json:"usage_limit"`
		UsagePerCustomer     *int     `json:"usage_per_customer"`
		AppliesTo            string   `json:"applies_to"` // all, specific_products, specific_categories, specific_customers
		ApplicableProducts   []string `json:"applicable_products"`
		ApplicableCategories []string `json:"applicable_categories"`
		ApplicableCustomers  []string `json:"applicable_customers"`
		NewCustomersOnly     bool     `json:"new_customers_only"`
		ValidFrom            string   `json:"valid_from" binding:"required"`
		ValidUntil           string   `json:"valid_until" binding:"required"`
		Status               string   `json:"status"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse dates
	validFrom, err := time.Parse("2006-01-02", input.ValidFrom)
	if err != nil {
		response.BadRequest(c, "Invalid valid_from date format")
		return
	}

	validUntil, err := time.Parse("2006-01-02", input.ValidUntil)
	if err != nil {
		response.BadRequest(c, "Invalid valid_until date format")
		return
	}

	if validUntil.Before(validFrom) {
		response.BadRequest(c, "valid_until must be after valid_from")
		return
	}

	// Set defaults
	if input.AppliesTo == "" {
		input.AppliesTo = "all"
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.UsagePerCustomer == nil {
		defaultUsage := 1
		input.UsagePerCustomer = &defaultUsage
	}

	discountID := uuid.New()
	now := time.Now()

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	_, err = h.db.Exec(`
		INSERT INTO discounts (
			id, tenant_id, organization_id, code, name, description, discount_type, discount_value,
			min_order_amount, max_discount_amount, usage_limit, usage_per_customer,
			applies_to, applicable_products, applicable_categories, applicable_customers,
			new_customers_only, valid_from, valid_until, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)`,
		discountID, tenantID, orgIDPtr, input.Code, input.Name, input.Description, input.DiscountType, input.DiscountValue,
		input.MinOrderAmount, input.MaxDiscountAmount, input.UsageLimit, input.UsagePerCustomer,
		input.AppliesTo, pq.Array(input.ApplicableProducts), pq.Array(input.ApplicableCategories), pq.Array(input.ApplicableCustomers),
		input.NewCustomersOnly, validFrom, validUntil, input.Status, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create discount", "error", err)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			response.BadRequest(c, "A discount with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create discount")
		return
	}

	response.Created(c, map[string]interface{}{
		"id":                  discountID.String(),
		"code":                input.Code,
		"name":                input.Name,
		"description":         input.Description,
		"discount_type":       input.DiscountType,
		"discount_value":      input.DiscountValue,
		"min_order_amount":    input.MinOrderAmount,
		"max_discount_amount": input.MaxDiscountAmount,
		"usage_limit":         input.UsageLimit,
		"usage_per_customer":  input.UsagePerCustomer,
		"used_count":          0,
		"applies_to":          input.AppliesTo,
		"new_customers_only":  input.NewCustomersOnly,
		"valid_from":          input.ValidFrom,
		"valid_until":         input.ValidUntil,
		"status":              input.Status,
		"created_at":          now,
	})
}

// UpdateDiscount updates an existing discount
func (h *Handler) UpdateDiscount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	discountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid discount ID")
		return
	}

	var input struct {
		Code              *string  `json:"code"`
		Name              *string  `json:"name"`
		Description       *string  `json:"description"`
		DiscountType      *string  `json:"discount_type"`
		DiscountValue     *float64 `json:"discount_value"`
		MinOrderAmount    *float64 `json:"min_order_amount"`
		MaxDiscountAmount *float64 `json:"max_discount_amount"`
		UsageLimit        *int     `json:"usage_limit"`
		UsagePerCustomer  *int     `json:"usage_per_customer"`
		AppliesTo         *string  `json:"applies_to"`
		NewCustomersOnly  *bool    `json:"new_customers_only"`
		ValidFrom         *string  `json:"valid_from"`
		ValidUntil        *string  `json:"valid_until"`
		Status            *string  `json:"status"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Code != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("code = $%d", argCount))
		args = append(args, *input.Code)
	}
	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.DiscountType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("discount_type = $%d", argCount))
		args = append(args, *input.DiscountType)
	}
	if input.DiscountValue != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("discount_value = $%d", argCount))
		args = append(args, *input.DiscountValue)
	}
	if input.MinOrderAmount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("min_order_amount = $%d", argCount))
		args = append(args, *input.MinOrderAmount)
	}
	if input.MaxDiscountAmount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("max_discount_amount = $%d", argCount))
		args = append(args, *input.MaxDiscountAmount)
	}
	if input.UsageLimit != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("usage_limit = $%d", argCount))
		args = append(args, *input.UsageLimit)
	}
	if input.UsagePerCustomer != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("usage_per_customer = $%d", argCount))
		args = append(args, *input.UsagePerCustomer)
	}
	if input.AppliesTo != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("applies_to = $%d", argCount))
		args = append(args, *input.AppliesTo)
	}
	if input.NewCustomersOnly != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("new_customers_only = $%d", argCount))
		args = append(args, *input.NewCustomersOnly)
	}
	if input.ValidFrom != nil {
		validFrom, err := time.Parse("2006-01-02", *input.ValidFrom)
		if err != nil {
			response.BadRequest(c, "Invalid valid_from date format")
			return
		}
		argCount++
		updates = append(updates, fmt.Sprintf("valid_from = $%d", argCount))
		args = append(args, validFrom)
	}
	if input.ValidUntil != nil {
		validUntil, err := time.Parse("2006-01-02", *input.ValidUntil)
		if err != nil {
			response.BadRequest(c, "Invalid valid_until date format")
			return
		}
		argCount++
		updates = append(updates, fmt.Sprintf("valid_until = $%d", argCount))
		args = append(args, validUntil)
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

	// Add WHERE clause parameters
	argCount++
	args = append(args, discountID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE discounts SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		joinStrings(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update discount", "error", err)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			response.BadRequest(c, "A discount with this code already exists")
			return
		}
		response.InternalError(c, "Failed to update discount")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Discount")
		return
	}

	// Fetch and return updated discount
	h.GetDiscount(c)
}

// DeleteDiscount soft deletes a discount
func (h *Handler) DeleteDiscount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	discountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid discount ID")
		return
	}

	result, err := h.db.Exec(
		"UPDATE discounts SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), discountID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete discount", "error", err)
		response.InternalError(c, "Failed to delete discount")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Discount")
		return
	}

	response.Success(c, map[string]string{"message": "Discount deleted successfully"})
}

// ValidateDiscountCode validates a discount code and returns discount details
func (h *Handler) ValidateDiscountCode(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	var input struct {
		Code          string  `json:"code" binding:"required"`
		OrderAmount   float64 `json:"order_amount"`
		CustomerID    string  `json:"customer_id"`
		IsNewCustomer bool    `json:"is_new_customer"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	var id uuid.UUID
	var name, discountType, appliesTo string
	var discountValue, minOrderAmount float64
	var maxDiscountAmount sql.NullFloat64
	var usageLimit, usagePerCustomer sql.NullInt32
	var usedCount int
	var newCustomersOnly bool
	var validFrom, validUntil time.Time

	err := h.db.QueryRow(`
		SELECT id, name, discount_type, discount_value, min_order_amount, max_discount_amount,
			   usage_limit, usage_per_customer, used_count, applies_to, new_customers_only, valid_from, valid_until
		FROM discounts
		WHERE code = $1 AND tenant_id = $2 AND status = 'active' AND deleted_at IS NULL`,
		input.Code, tenantID,
	).Scan(
		&id, &name, &discountType, &discountValue, &minOrderAmount, &maxDiscountAmount,
		&usageLimit, &usagePerCustomer, &usedCount, &appliesTo, &newCustomersOnly, &validFrom, &validUntil,
	)
	if err == sql.ErrNoRows {
		response.BadRequest(c, "Invalid or inactive discount code")
		return
	}
	if err != nil {
		h.log.Error("Failed to validate discount", "error", err)
		response.InternalError(c, "Failed to validate discount")
		return
	}

	now := time.Now()

	// Check validity period
	if now.Before(validFrom) {
		response.BadRequest(c, "Discount code is not yet active")
		return
	}
	if now.After(validUntil) {
		response.BadRequest(c, "Discount code has expired")
		return
	}

	// Check usage limit
	if usageLimit.Valid && usedCount >= int(usageLimit.Int32) {
		response.BadRequest(c, "Discount code usage limit reached")
		return
	}

	// Check new customers only
	if newCustomersOnly && !input.IsNewCustomer {
		response.BadRequest(c, "This discount is only for new customers")
		return
	}

	// Check minimum order amount
	if input.OrderAmount < minOrderAmount {
		response.BadRequest(c, fmt.Sprintf("Minimum order amount of %.2f required for this discount", minOrderAmount))
		return
	}

	// Check customer usage limit
	if input.CustomerID != "" && usagePerCustomer.Valid {
		var customerUsageCount int
		h.db.QueryRow(`
			SELECT COUNT(*) FROM discount_usage
			WHERE discount_id = $1 AND customer_id = $2`,
			id, input.CustomerID,
		).Scan(&customerUsageCount)

		if customerUsageCount >= int(usagePerCustomer.Int32) {
			response.BadRequest(c, "You have already used this discount the maximum number of times")
			return
		}
	}

	// Calculate discount amount
	var discountAmount float64
	if discountType == "percentage" {
		discountAmount = input.OrderAmount * (discountValue / 100)
		if maxDiscountAmount.Valid && discountAmount > maxDiscountAmount.Float64 {
			discountAmount = maxDiscountAmount.Float64
		}
	} else {
		discountAmount = discountValue
		if discountAmount > input.OrderAmount {
			discountAmount = input.OrderAmount
		}
	}

	response.Success(c, map[string]interface{}{
		"valid":           true,
		"discount_id":     id.String(),
		"name":            name,
		"discount_type":   discountType,
		"discount_value":  discountValue,
		"discount_amount": discountAmount,
		"message":         fmt.Sprintf("%s: -%.2f", name, discountAmount),
	})
}

// UseDiscountCode records the usage of a discount code
func (h *Handler) UseDiscountCode(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	discountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid discount ID")
		return
	}

	var input struct {
		CustomerID       string  `json:"customer_id"`
		SalesOrderID     string  `json:"sales_order_id"`
		AmountDiscounted float64 `json:"amount_discounted" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()

	// Create usage record
	usageID := uuid.New()
	var customerID, salesOrderID *uuid.UUID

	if input.CustomerID != "" {
		if cid, err := uuid.Parse(input.CustomerID); err == nil {
			customerID = &cid
		}
	}
	if input.SalesOrderID != "" {
		if soid, err := uuid.Parse(input.SalesOrderID); err == nil {
			salesOrderID = &soid
		}
	}

	_, err = h.db.Exec(`
		INSERT INTO discount_usage (id, discount_id, tenant_id, customer_id, sales_order_id, amount_discounted, used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		usageID, discountID, tenantID, customerID, salesOrderID, input.AmountDiscounted, now,
	)
	if err != nil {
		h.log.Error("Failed to record discount usage", "error", err)
		response.InternalError(c, "Failed to record discount usage")
		return
	}

	// Increment used_count
	if _, execErr := h.db.Exec("UPDATE discounts SET used_count = used_count + 1, updated_at = $1 WHERE id = $2", now, discountID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE discounts", "error", execErr)
	}

	response.Success(c, map[string]string{"message": "Discount usage recorded"})
}
