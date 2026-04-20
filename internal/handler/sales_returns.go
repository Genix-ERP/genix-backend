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
)

// ListSalesReturns returns all sales returns for the tenant
func (h *Handler) ListSalesReturns(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	status := c.Query("status")
	customerID := c.Query("customer_id")

	lp := listparams.Parse(c,
		[]string{"created_at", "return_number", "customer_name", "total_amount", "return_date", "status"},
		"created_at")

	query := `
		SELECT sr.id, sr.tenant_id, sr.return_number, sr.sales_invoice_id, sr.sales_order_id, sr.customer_id, sr.customer_name,
			   sr.return_date, sr.reason, sr.subtotal, sr.tax_amount, sr.total_amount, sr.status,
			   sr.refund_status, sr.refund_method, sr.refund_date, sr.refund_reference, sr.notes, sr.created_at, sr.updated_at,
			   so.order_number
		FROM sales_returns sr
		LEFT JOIN sales_orders so ON so.id = sr.sales_order_id
		WHERE sr.tenant_id = $1 AND sr.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM sales_returns sr WHERE sr.tenant_id = $1 AND sr.deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND sr.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND sr.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status != "" {
		argCount++
		query += fmt.Sprintf(" AND sr.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND sr.status = $%d", argCount)
		args = append(args, status)
	}

	if customerID != "" {
		argCount++
		query += fmt.Sprintf(" AND sr.customer_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND sr.customer_id = $%d", argCount)
		args = append(args, customerID)
	}

	// Search
	if lp.Search != "" {
		argCount++
		pattern := "%" + strings.ToLower(lp.Search) + "%"
		query += fmt.Sprintf(" AND (LOWER(sr.return_number) LIKE $%d OR LOWER(sr.customer_name) LIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(sr.return_number) LIKE $%d OR LOWER(sr.customer_name) LIKE $%d)", argCount, argCount)
		args = append(args, pattern)
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)

	query += lp.OrderClause("sr.") + fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, lp.PageSize, lp.Offset())

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list sales returns", "error", err)
		response.InternalError(c, "Failed to list sales returns")
		return
	}
	defer rows.Close()

	var returns []map[string]interface{}
	for rows.Next() {
		var id, tenantID uuid.UUID
		var returnNumber, customerName, reason, status, refundStatus string
		var subtotal, taxAmount, totalAmount float64
		var returnDate time.Time
		var salesInvoiceID, salesOrderID, customerID sql.NullString
		var refundMethod, refundReference, notes sql.NullString
		var soNumber sql.NullString
		var refundDate sql.NullTime
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &tenantID, &returnNumber, &salesInvoiceID, &salesOrderID, &customerID, &customerName,
			&returnDate, &reason, &subtotal, &taxAmount, &totalAmount, &status,
			&refundStatus, &refundMethod, &refundDate, &refundReference, &notes, &createdAt, &updatedAt,
			&soNumber,
		)
		if err != nil {
			h.log.Error("Failed to scan sales return", "error", err)
			continue
		}

		ret := map[string]interface{}{
			"id":            id.String(),
			"return_number": returnNumber,
			"customer_name": customerName,
			"return_date":   returnDate.Format("2006-01-02"),
			"reason":        reason,
			"subtotal":      subtotal,
			"tax_amount":    taxAmount,
			"total_amount":  totalAmount,
			"status":        status,
			"refund_status": refundStatus,
			"created_at":    createdAt,
			"updated_at":    updatedAt,
		}

		if salesInvoiceID.Valid {
			ret["sales_invoice_id"] = salesInvoiceID.String
		}
		if salesOrderID.Valid {
			ret["sales_order_id"] = salesOrderID.String
		}
		if soNumber.Valid {
			ret["so_number"] = soNumber.String
		}
		if customerID.Valid {
			ret["customer_id"] = customerID.String
		}
		if refundMethod.Valid {
			ret["refund_method"] = refundMethod.String
		}
		if refundDate.Valid {
			ret["refund_date"] = refundDate.Time.Format("2006-01-02")
		}
		if refundReference.Valid {
			ret["refund_reference"] = refundReference.String
		}
		if notes.Valid {
			ret["notes"] = notes.String
		}

		// Load items
		ret["items"] = h.loadSalesReturnItems(id)

		returns = append(returns, ret)
	}

	if returns == nil {
		returns = []map[string]interface{}{}
	}

	response.Paginated(c, returns, lp.Page, lp.PageSize, total)
}

// GetSalesReturn returns a single sales return by ID
func (h *Handler) GetSalesReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid return ID")
		return
	}

	ret, err := h.getSalesReturnByID(tenantID, returnID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Sales Return")
			return
		}
		response.InternalError(c, "Failed to get sales return")
		return
	}

	response.Success(c, ret)
}

// CreateSalesReturn creates a new sales return
func (h *Handler) CreateSalesReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
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
		SalesInvoiceID string `json:"invoice_id"`
		SalesOrderID   string `json:"sales_order_id"`
		CustomerID     string `json:"customer_id"`
		CustomerName   string `json:"customer_name"`
		ReturnDate     string `json:"return_date"`
		Reason         string `json:"reason"`
		Notes          string `json:"notes"`
		Items          []struct {
			ProductID   string  `json:"product_id"`
			ProductName string  `json:"product_name"`
			Quantity    float64 `json:"quantity"`
			UnitPrice   float64 `json:"unit_price"`
			Condition   string  `json:"condition"`
		} `json:"items"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()
	returnID := uuid.New()
	var srCount int
	h.db.QueryRow("SELECT COUNT(*) FROM sales_returns WHERE tenant_id = $1", tenantID).Scan(&srCount)
	returnNumber := fmt.Sprintf("SR%05d", srCount+1)

	// Calculate totals
	subtotal := 0.0
	for _, item := range input.Items {
		subtotal += item.Quantity * item.UnitPrice
	}
	// For returns, typically no additional tax calculation (use original amounts)
	totalAmount := subtotal

	// Parse return date
	returnDate := now
	if input.ReturnDate != "" {
		if t, err := time.Parse("2006-01-02", input.ReturnDate); err == nil {
			returnDate = t
		}
	}

	// Parse IDs
	var salesInvoiceID, salesOrderID, customerID *uuid.UUID
	if input.SalesInvoiceID != "" {
		if id, err := uuid.Parse(input.SalesInvoiceID); err == nil {
			salesInvoiceID = &id
		}
	}
	if input.SalesOrderID != "" {
		if id, err := uuid.Parse(input.SalesOrderID); err == nil {
			salesOrderID = &id
		}
	}
	if input.CustomerID != "" {
		if id, err := uuid.Parse(input.CustomerID); err == nil {
			customerID = &id
		}
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Insert return
	_, err := h.db.Exec(`
		INSERT INTO sales_returns (
			id, tenant_id, organization_id, return_number, sales_invoice_id, sales_order_id, customer_id, customer_name,
			return_date, reason, subtotal, tax_amount, total_amount, status, refund_status,
			notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		returnID, tenantID, orgIDPtr, returnNumber, salesInvoiceID, salesOrderID, customerID, input.CustomerName,
		returnDate, input.Reason, subtotal, 0, totalAmount, "pending", "pending",
		input.Notes, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create sales return", "error", err)
		response.InternalError(c, "Failed to create sales return")
		return
	}

	// Insert items
	for i, item := range input.Items {
		itemID := uuid.New()
		var productID *uuid.UUID
		if item.ProductID != "" {
			if pid, err := uuid.Parse(item.ProductID); err == nil {
				productID = &pid
			}
		}
		itemTotal := item.Quantity * item.UnitPrice

		_, err := h.db.Exec(`
			INSERT INTO sales_return_items (
				id, sales_return_id, product_id, product_name, quantity, unit_price, total, condition, sort_order, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			itemID, returnID, productID, item.ProductName, item.Quantity, item.UnitPrice, itemTotal, item.Condition, i, now,
		)
		if err != nil {
			h.log.Error("Failed to create sales return item", "error", err, "item_index", i)
		}
	}

	// Fetch and return the created return
	ret, err := h.getSalesReturnByID(tenantID, returnID)
	if err != nil {
		response.InternalError(c, "Sales return created but failed to retrieve")
		return
	}

	response.Created(c, ret)
}

// UpdateSalesReturn updates an existing sales return
func (h *Handler) UpdateSalesReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid return ID")
		return
	}

	var input struct {
		Status       *string `json:"status"`
		RefundStatus *string `json:"refund_status"`
		RefundMethod *string `json:"refund_method"`
		RefundDate   *string `json:"refund_date"`
		Notes        *string `json:"notes"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()

	// Build update query dynamically
	updates := []string{"updated_at = $1"}
	args := []interface{}{now}
	argCount := 1

	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.RefundStatus != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("refund_status = $%d", argCount))
		args = append(args, *input.RefundStatus)
	}
	if input.RefundMethod != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("refund_method = $%d", argCount))
		args = append(args, *input.RefundMethod)
	}
	if input.RefundDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("refund_date = $%d", argCount))
		if *input.RefundDate == "" {
			args = append(args, nil)
		} else {
			t, _ := time.Parse("2006-01-02", *input.RefundDate)
			args = append(args, t)
		}
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	argCount++
	args = append(args, returnID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE sales_returns SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update sales return", "error", err)
		response.InternalError(c, "Failed to update sales return")
		return
	}

	// Fetch and return updated return
	ret, err := h.getSalesReturnByID(tenantID, returnID)
	if err != nil {
		response.InternalError(c, "Sales return updated but failed to retrieve")
		return
	}

	response.Success(c, ret)
}

// DeleteSalesReturn soft deletes a sales return
func (h *Handler) DeleteSalesReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid return ID")
		return
	}

	now := time.Now()
	result, err := h.db.Exec(
		"UPDATE sales_returns SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		now, returnID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete sales return")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Sales Return")
		return
	}

	response.NoContent(c)
}

// ApproveSalesReturn approves a pending return
// This creates a Credit Note (journal entry) that reduces Accounts Receivable
func (h *Handler) ApproveSalesReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid return ID")
		return
	}

	now := time.Now()

	var approvedBy *uuid.UUID
	if userID != uuid.Nil {
		approvedBy = &userID
	}

	// Get return details for journal entry
	var returnNumber, customerName string
	var customerID sql.NullString
	var totalAmount float64
	err = h.db.QueryRow(`
		SELECT return_number, customer_id, customer_name, total_amount
		FROM sales_returns WHERE id = $1 AND tenant_id = $2 AND status = 'pending' AND deleted_at IS NULL`,
		returnID, tenantID,
	).Scan(&returnNumber, &customerID, &customerName, &totalAmount)
	if err == sql.ErrNoRows {
		response.BadRequest(c, "Return not found or not in pending status")
		return
	}
	if err != nil {
		h.log.Error("Failed to get sales return", "error", err)
		response.InternalError(c, "Failed to get sales return")
		return
	}

	// Update return status
	result, err := h.db.Exec(`
		UPDATE sales_returns SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5 AND status = 'pending' AND deleted_at IS NULL`,
		approvedBy, now, now, returnID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to approve sales return", "error", err)
		response.InternalError(c, "Failed to approve sales return")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.BadRequest(c, "Return not found or not in pending status")
		return
	}

	// =====================================================
	// CREATE CREDIT NOTE (Journal Entry) - Reduces AR
	// Debit: Sales Revenue (reduce revenue)
	// Credit: Accounts Receivable (reduce what customer owes)
	// =====================================================

	// Get organization_id from linked invoice for correct account lookup
	var returnOrgID *uuid.UUID
	h.db.QueryRow(`SELECT si.organization_id FROM sales_invoices si
		JOIN sales_returns sr ON sr.sales_invoice_id = si.id
		WHERE sr.id = $1`, returnID).Scan(&returnOrgID)

	// Get accounts — lookup by name first, then code fallback
	arAccountID := findAccount(h.db, tenantID, returnOrgID, "accounts receivable", "4010")
	revenueAccountID := findAccount(h.db, tenantID, returnOrgID, "sales revenue", "9010")

	// Get journal
	var journalID uuid.UUID
	h.db.QueryRow("SELECT id FROM journals WHERE tenant_id = $1 AND code IN ('SALES', 'SAL') AND deleted_at IS NULL", tenantID).Scan(&journalID)

	if arAccountID != uuid.Nil && revenueAccountID != uuid.Nil && journalID != uuid.Nil {
		// Generate entry number
		entryNumber := fmt.Sprintf("CN-%s-%s", now.Format("20060102"), uuid.New().String()[:6])

		// Create journal entry (Credit Note)
		entryID := uuid.New()
		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'posted', $13, $14, $15)`,
			entryID, tenantID, returnOrgID, journalID, entryNumber, now, returnNumber,
			fmt.Sprintf("Credit Note for Sales Return %s - %s", returnNumber, customerName),
			"sales_return", returnID, totalAmount, totalAmount, approvedBy, now, now,
		)
		if err != nil {
			h.log.Error("Failed to create credit note journal entry", "error", err)
		} else {
			// Create journal entry lines
			// Line 1: Debit Sales Revenue (reduce revenue)
			line1ID := uuid.New()
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				line1ID, entryID, revenueAccountID, "Sales Return - Revenue Reversal", totalAmount, 0, 1, now,
			)

			// Line 2: Credit Accounts Receivable (reduce what customer owes)
			line2ID := uuid.New()
			var contactID *uuid.UUID
			if customerID.Valid {
				cid, _ := uuid.Parse(customerID.String)
				contactID = &cid
			}
			h.db.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, account_id, contact_id, description, debit_amount, credit_amount, line_number, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				line2ID, entryID, arAccountID, contactID, "Sales Return - AR Reduction", 0, totalAmount, 2, now,
			)

			// Update account balances
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalAmount, now, revenueAccountID)
			h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalAmount, now, arAccountID)

			h.log.Info("Credit Note created for sales return", "return_id", returnID, "entry_number", entryNumber, "amount", totalAmount)
		}
	} else {
		h.log.Warn("Could not create credit note - missing accounts or journal", "ar_account", arAccountID, "revenue_account", revenueAccountID, "journal", journalID)
	}

	// Deduct return total from customer balance
	if customerID.Valid {
		_, err := h.db.Exec(
			"UPDATE contacts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3",
			totalAmount, now, customerID.String,
		)
		if err != nil {
			h.log.Error("Failed to update customer balance for return", "error", err, "customer_id", customerID.String)
		} else {
			h.log.Info("Customer balance deducted for sales return", "customer_id", customerID.String, "amount", totalAmount)
		}
	}

	ret, _ := h.getSalesReturnByID(tenantID, returnID)
	response.Success(c, ret)
}

// RejectSalesReturn rejects a pending return
func (h *Handler) RejectSalesReturn(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid return ID")
		return
	}

	now := time.Now()

	result, err := h.db.Exec(`
		UPDATE sales_returns SET status = 'rejected', updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND status = 'pending' AND deleted_at IS NULL`,
		now, returnID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to reject sales return", "error", err)
		response.InternalError(c, "Failed to reject sales return")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.BadRequest(c, "Return not found or not in pending status")
		return
	}

	ret, _ := h.getSalesReturnByID(tenantID, returnID)
	response.Success(c, ret)
}

// ProcessRefund processes the refund for an approved return
// This also INCREASES inventory (products come back from customer)
func (h *Handler) ProcessRefund(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	returnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid return ID")
		return
	}

	var input struct {
		RefundMethod string `json:"refund_method"`
		WarehouseID  string `json:"warehouse_id"` // Optional: which warehouse to return items to
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()

	// First check if return exists and is approved
	var currentStatus string
	err = h.db.QueryRow(
		"SELECT status FROM sales_returns WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		returnID, tenantID,
	).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales Return")
		return
	}
	if currentStatus != "approved" {
		response.BadRequest(c, "Return must be approved before processing refund")
		return
	}

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, now); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	// Get return items for inventory update
	items := h.loadSalesReturnItems(returnID)
	h.log.Info("Processing refund inventory update", "return_id", returnID, "items_count", len(items))

	// Update inventory for each item (increase stock - products returned)
	for _, item := range items {
		productIDStr, ok := item["product_id"].(string)
		if !ok || productIDStr == "" {
			h.log.Warn("Return item missing product_id", "item", item)
			continue // Skip items without product_id
		}

		productID, err := uuid.Parse(productIDStr)
		if err != nil {
			continue
		}

		quantity, _ := item["quantity"].(float64)
		unitPrice, _ := item["unit_price"].(float64)
		if quantity <= 0 {
			continue
		}

		// Find or create inventory record
		// First try to find existing inventory for this product
		var inventoryID uuid.UUID
		var currentQty float64
		var warehouseID uuid.UUID

		// If warehouse specified, use it; otherwise find default
		if input.WarehouseID != "" {
			warehouseID, _ = uuid.Parse(input.WarehouseID)
		}

		err = h.db.QueryRow(`
			SELECT id, quantity_on_hand, warehouse_id FROM inventory
			WHERE tenant_id = $1 AND product_id = $2
			LIMIT 1`,
			tenantID, productID,
		).Scan(&inventoryID, &currentQty, &warehouseID)

		if err == sql.ErrNoRows {
			// No inventory record exists, create one
			inventoryID = uuid.New()
			if warehouseID == uuid.Nil {
				// Get default warehouse
				h.db.QueryRow("SELECT id FROM warehouses WHERE tenant_id = $1 LIMIT 1", tenantID).Scan(&warehouseID)
			}

			_, err = h.db.Exec(`
				INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, quantity_on_hand, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $6)`,
				inventoryID, tenantID, productID, warehouseID, quantity, now,
			)
			if err != nil {
				h.log.Error("Failed to create inventory record for return", "error", err, "product_id", productID)
				continue
			}
		} else if err == nil {
			// Update existing inventory - INCREASE quantity (return from customer)
			// Note: quantity_available is a generated column, only update quantity_on_hand
			_, err = h.db.Exec(`
				UPDATE inventory SET quantity_on_hand = quantity_on_hand + $1, updated_at = $2
				WHERE id = $3`,
				quantity, now, inventoryID,
			)
			if err != nil {
				h.log.Error("Failed to update inventory for return", "error", err, "inventory_id", inventoryID)
				continue
			}
		}

		// Create inventory transaction for audit trail
		txID := uuid.New()
		h.db.Exec(`
			INSERT INTO inventory_transactions (
				id, tenant_id, inventory_id, transaction_type, quantity,
				unit_cost, total_cost, reference_type, reference_id,
				reason, transaction_date, created_at
			) VALUES ($1, $2, $3, 'return', $4, $5, $6, 'sales_return', $7, 'Sales Return - Customer Return', $8, $8)
		`, txID, tenantID, inventoryID, quantity, unitPrice, quantity*unitPrice, returnID, now)
	}

	// Get return details for payment entry
	var returnNumber, customerName string
	var customerID sql.NullString
	var totalAmount, returnTaxAmount float64
	h.db.QueryRow(`
		SELECT return_number, customer_id, customer_name, total_amount, COALESCE(tax_amount, 0)
		FROM sales_returns WHERE id = $1 AND tenant_id = $2`,
		returnID, tenantID,
	).Scan(&returnNumber, &customerID, &customerName, &totalAmount, &returnTaxAmount)

	// Update return status
	result, err := h.db.Exec(`
		UPDATE sales_returns SET refund_status = 'processed', refund_method = $1, refund_date = $2, status = 'completed', updated_at = $3
		WHERE id = $4 AND tenant_id = $5 AND status = 'approved' AND deleted_at IS NULL`,
		input.RefundMethod, now, now, returnID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to process refund", "error", err)
		response.InternalError(c, "Failed to process refund")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.BadRequest(c, "Return not found or not in approved status")
		return
	}

	// =====================================================
	// CREATE PAYMENT ENTRY (if cash refund)
	// When customer gets cash back:
	// Debit: Accounts Receivable (we now owe customer)
	// Credit: Cash (money goes out)
	// =====================================================

	if input.RefundMethod == "cash_refund" || input.RefundMethod == "cash" {
		// Get organization_id from linked invoice
		var refundOrgID *uuid.UUID
		h.db.QueryRow(`SELECT si.organization_id FROM sales_invoices si
			JOIN sales_returns sr ON sr.sales_invoice_id = si.id
			WHERE sr.id = $1`, returnID).Scan(&refundOrgID)

		// Get accounts — lookup by name first, then code fallback
		arAccountID := findAccount(h.db, tenantID, refundOrgID, "accounts receivable", "4010")
		cashAccountID := findAccount(h.db, tenantID, refundOrgID, "cash", "5010")

		// Get journal
		var journalID uuid.UUID
		h.db.QueryRow("SELECT id FROM journals WHERE tenant_id = $1 AND code IN ('CASH_RECEIPTS', 'CASH') AND deleted_at IS NULL", tenantID).Scan(&journalID)

		if arAccountID != uuid.Nil && cashAccountID != uuid.Nil && journalID != uuid.Nil {
			// Generate entry number
			entryNumber := fmt.Sprintf("REF-%s-%s", now.Format("20060102"), uuid.New().String()[:6])

			// Create journal entry (Cash Refund Payment)
			entryID := uuid.New()
			_, err = h.db.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, total_debit, total_credit, status, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'posted', $13, $14)`,
				entryID, tenantID, refundOrgID, journalID, entryNumber, now, returnNumber,
				fmt.Sprintf("Cash Refund for Sales Return %s - %s", returnNumber, customerName),
				"sales_return_refund", returnID, totalAmount, totalAmount, now, now,
			)
			if err != nil {
				h.log.Error("Failed to create refund journal entry", "error", err)
			} else {
				// Line 1: Debit Accounts Receivable (clear the credit note)
				line1ID := uuid.New()
				var contactID *uuid.UUID
				if customerID.Valid {
					cid, _ := uuid.Parse(customerID.String)
					contactID = &cid
				}
				h.db.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, account_id, contact_id, description, debit_amount, credit_amount, line_number, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					line1ID, entryID, arAccountID, contactID, "Cash Refund - Clear AR Credit", totalAmount, 0, 1, now,
				)

				// Line 2: Credit Cash (money goes out)
				line2ID := uuid.New()
				h.db.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
					line2ID, entryID, cashAccountID, "Cash Refund Payment", 0, totalAmount, 2, now,
				)

				// Update account balances
				h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalAmount, now, arAccountID)
				h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalAmount, now, cashAccountID)

				h.log.Info("Cash refund payment recorded", "return_id", returnID, "entry_number", entryNumber, "amount", totalAmount)
			}
		}
	}
	// For "credit_note" refund method, create an actual credit note document
	if input.RefundMethod == "credit_note" {
		// Find the original sales invoice linked to this return
		var salesInvID sql.NullString
		h.db.QueryRow("SELECT sales_invoice_id FROM sales_returns WHERE id = $1 AND tenant_id = $2", returnID, tenantID).Scan(&salesInvID)

		if salesInvID.Valid {
			// Create a credit note for the linked invoice
			cnDate := now
			cnNumber := "CN-" + cnDate.Format("20060102") + "-" + uuid.New().String()[:6]
			creditNoteID := uuid.New()
			reason := fmt.Sprintf("Sales Return %s - %s", returnNumber, customerName)

			// Get original invoice details
			var orgCustomerID uuid.UUID
			var orgID *uuid.UUID
			var currencyID *uuid.UUID
			h.db.QueryRow(`
				SELECT customer_id, organization_id, currency_id
				FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
				salesInvID.String, tenantID,
			).Scan(&orgCustomerID, &orgID, &currencyID)

			_, err = h.db.Exec(`
				INSERT INTO sales_invoices (
					id, tenant_id, organization_id, invoice_number, customer_id,
					invoice_date, due_date, invoice_type, original_invoice_id, reason,
					currency_id, exchange_rate, subtotal, discount_amount,
					tax_amount, total_amount, amount_paid, status,
					reference, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 'credit_note', $8, $9, $10, 1.0, $11, 0, $12, $13, 0, 'draft', $14, $15, $16)`,
				creditNoteID, tenantID, orgID, cnNumber, orgCustomerID,
				cnDate, cnDate, salesInvID.String, reason,
				currencyID, totalAmount-returnTaxAmount, returnTaxAmount, totalAmount,
				returnNumber, now, now,
			)
			if err != nil {
				h.log.Error("Failed to create credit note for sales return", "error", err, "return_id", returnID)
			} else {
				h.log.Info("Credit note created for sales return", "credit_note_id", creditNoteID, "return_id", returnID)
			}
		}
	}

	h.log.Info("Sales return processed with inventory update", "return_id", returnID, "items_count", len(items))

	ret, _ := h.getSalesReturnByID(tenantID, returnID)
	response.Success(c, ret)
}

// Helper functions

func (h *Handler) getSalesReturnByID(tenantID, returnID uuid.UUID) (map[string]interface{}, error) {
	var id uuid.UUID
	var returnNumber, customerName, reason, status, refundStatus string
	var subtotal, taxAmount, totalAmount float64
	var returnDate time.Time
	var salesInvoiceID, salesOrderID, customerID sql.NullString
	var refundMethod, refundReference, notes sql.NullString
	var soNumber sql.NullString
	var refundDate sql.NullTime
	var createdAt, updatedAt time.Time

	err := h.db.QueryRow(`
		SELECT sr.id, sr.return_number, sr.sales_invoice_id, sr.sales_order_id, sr.customer_id, sr.customer_name,
			   sr.return_date, sr.reason, sr.subtotal, sr.tax_amount, sr.total_amount, sr.status,
			   sr.refund_status, sr.refund_method, sr.refund_date, sr.refund_reference, sr.notes, sr.created_at, sr.updated_at,
			   so.order_number
		FROM sales_returns sr
		LEFT JOIN sales_orders so ON so.id = sr.sales_order_id
		WHERE sr.id = $1 AND sr.tenant_id = $2 AND sr.deleted_at IS NULL`,
		returnID, tenantID,
	).Scan(
		&id, &returnNumber, &salesInvoiceID, &salesOrderID, &customerID, &customerName,
		&returnDate, &reason, &subtotal, &taxAmount, &totalAmount, &status,
		&refundStatus, &refundMethod, &refundDate, &refundReference, &notes, &createdAt, &updatedAt,
		&soNumber,
	)
	if err != nil {
		return nil, err
	}

	ret := map[string]interface{}{
		"id":            id.String(),
		"return_number": returnNumber,
		"customer_name": customerName,
		"return_date":   returnDate.Format("2006-01-02"),
		"reason":        reason,
		"subtotal":      subtotal,
		"tax_amount":    taxAmount,
		"total_amount":  totalAmount,
		"status":        status,
		"refund_status": refundStatus,
		"created_at":    createdAt,
		"updated_at":    updatedAt,
	}

	if salesInvoiceID.Valid {
		ret["sales_invoice_id"] = salesInvoiceID.String
	}
	if salesOrderID.Valid {
		ret["sales_order_id"] = salesOrderID.String
	}
	if soNumber.Valid {
		ret["so_number"] = soNumber.String
	}
	if customerID.Valid {
		ret["customer_id"] = customerID.String
	}
	if refundMethod.Valid {
		ret["refund_method"] = refundMethod.String
	}
	if refundDate.Valid {
		ret["refund_date"] = refundDate.Time.Format("2006-01-02")
	}
	if refundReference.Valid {
		ret["refund_reference"] = refundReference.String
	}
	if notes.Valid {
		ret["notes"] = notes.String
	}

	ret["items"] = h.loadSalesReturnItems(id)

	return ret, nil
}

func (h *Handler) loadSalesReturnItems(returnID uuid.UUID) []map[string]interface{} {
	rows, err := h.db.Query(`
		SELECT id, product_id, product_name, quantity, unit_price, total, condition, sort_order
		FROM sales_return_items
		WHERE sales_return_id = $1
		ORDER BY sort_order`,
		returnID,
	)
	if err != nil {
		return []map[string]interface{}{}
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var id uuid.UUID
		var productName, condition string
		var quantity, unitPrice, total float64
		var sortOrder int
		var productID sql.NullString

		err := rows.Scan(&id, &productID, &productName, &quantity, &unitPrice, &total, &condition, &sortOrder)
		if err != nil {
			continue
		}

		item := map[string]interface{}{
			"id":           id.String(),
			"product_name": productName,
			"quantity":     quantity,
			"unit_price":   unitPrice,
			"total":        total,
			"condition":    condition,
		}

		if productID.Valid {
			item["product_id"] = productID.String
		}

		items = append(items, item)
	}

	if items == nil {
		return []map[string]interface{}{}
	}

	return items
}
