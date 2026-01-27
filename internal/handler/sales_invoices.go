package handler

import (
	"database/sql"
	"encoding/json"
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

// ListSalesInvoices returns paginated list of sales invoices
func (h *Handler) ListSalesInvoices(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	// Pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Build query with filters
	baseQuery := `
		SELECT id, tenant_id, organization_id, invoice_number, customer_id, sales_order_id,
			   invoice_date, due_date, billing_address, shipping_address,
			   currency_id, exchange_rate, subtotal, discount_amount,
			   tax_amount, total_amount, amount_paid, amount_due, status,
			   reference, po_number, notes, terms_conditions,
			   journal_entry_id, sent_at, viewed_at, created_by, created_at, updated_at
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM sales_invoices WHERE tenant_id = $1 AND deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by customer_id
	if customerID := c.Query("customer_id"); customerID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND customer_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND customer_id = $%d", argCount)
		args = append(args, customerID)
	}

	// Filter by date range
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND invoice_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND invoice_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND invoice_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND invoice_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Filter by due date range
	if dueFrom := c.Query("due_from"); dueFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND due_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND due_date >= $%d", argCount)
		args = append(args, dueFrom)
	}
	if dueTo := c.Query("due_to"); dueTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND due_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND due_date <= $%d", argCount)
		args = append(args, dueTo)
	}

	// Filter overdue invoices
	if overdue := c.Query("overdue"); overdue == "true" {
		baseQuery += " AND due_date < CURRENT_DATE AND status NOT IN ('paid', 'cancelled')"
		countQuery += " AND due_date < CURRENT_DATE AND status NOT IN ('paid', 'cancelled')"
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(invoice_number) LIKE $%d OR LOWER(reference) LIKE $%d OR LOWER(po_number) LIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(invoice_number) LIKE $%d OR LOWER(reference) LIKE $%d OR LOWER(po_number) LIKE $%d)", argCount, argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count sales invoices")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		response.InternalError(c, "Failed to fetch sales invoices: "+err.Error())
		return
	}
	defer rows.Close()

	var invoices []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan, customerID uuid.UUID
		var organizationID, salesOrderID, currencyID, journalEntryID, createdBy sql.NullString
		var invoiceNumber, status string
		var invoiceDate, dueDate time.Time
		var sentAt, viewedAt sql.NullTime
		var billingAddress, shippingAddress []byte
		var exchangeRate, subtotal, discountAmount, taxAmount, totalAmount, amountPaid, amountDue float64
		var reference, poNumber, notes, termsConditions sql.NullString
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &tenantIDScan, &organizationID, &invoiceNumber, &customerID, &salesOrderID,
			&invoiceDate, &dueDate, &billingAddress, &shippingAddress,
			&currencyID, &exchangeRate, &subtotal, &discountAmount,
			&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
			&reference, &poNumber, &notes, &termsConditions,
			&journalEntryID, &sentAt, &viewedAt, &createdBy, &createdAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		invoice := map[string]interface{}{
			"id":              id.String(),
			"tenant_id":       tenantIDScan.String(),
			"invoice_number":  invoiceNumber,
			"customer_id":     customerID.String(),
			"invoice_date":    invoiceDate.Format("2006-01-02"),
			"due_date":        dueDate.Format("2006-01-02"),
			"exchange_rate":   exchangeRate,
			"subtotal":        subtotal,
			"discount_amount": discountAmount,
			"tax_amount":      taxAmount,
			"total_amount":    totalAmount,
			"amount_paid":     amountPaid,
			"amount_due":      amountDue,
			"status":          status,
			"created_at":      createdAt,
			"updated_at":      updatedAt,
		}

		if organizationID.Valid {
			invoice["organization_id"] = organizationID.String
		}
		if salesOrderID.Valid {
			invoice["sales_order_id"] = salesOrderID.String
		}
		if currencyID.Valid {
			invoice["currency_id"] = currencyID.String
		}
		if reference.Valid {
			invoice["reference"] = reference.String
		}
		if poNumber.Valid {
			invoice["po_number"] = poNumber.String
		}
		if notes.Valid {
			invoice["notes"] = notes.String
		}
		if sentAt.Valid {
			invoice["sent_at"] = sentAt.Time
		}
		if viewedAt.Valid {
			invoice["viewed_at"] = viewedAt.Time
		}

		// Parse addresses
		if len(billingAddress) > 0 {
			var addr map[string]interface{}
			if json.Unmarshal(billingAddress, &addr) == nil {
				invoice["billing_address"] = addr
			}
		}
		if len(shippingAddress) > 0 {
			var addr map[string]interface{}
			if json.Unmarshal(shippingAddress, &addr) == nil {
				invoice["shipping_address"] = addr
			}
		}

		invoices = append(invoices, invoice)
	}

	response.Paginated(c, invoices, page, pageSize, total)
}

// CreateSalesInvoice creates a new sales invoice
func (h *Handler) CreateSalesInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateSalesInvoiceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Parse customer ID
	customerID, err := uuid.Parse(input.CustomerID)
	if err != nil {
		response.BadRequest(c, "Invalid customer_id")
		return
	}

	// Parse dates
	invoiceDate, err := time.Parse("2006-01-02", input.InvoiceDate)
	if err != nil {
		response.BadRequest(c, "Invalid invoice_date format, expected YYYY-MM-DD")
		return
	}
	dueDate, err := time.Parse("2006-01-02", input.DueDate)
	if err != nil {
		response.BadRequest(c, "Invalid due_date format, expected YYYY-MM-DD")
		return
	}

	// Generate invoice number
	invoiceNumber := "INV-" + time.Now().Format("20060102") + "-" + uuid.New().String()[:6]

	invoiceID := uuid.New()
	now := time.Now()

	// Calculate totals from lines
	var subtotal, taxAmount, discountAmount float64
	for _, line := range input.Lines {
		lineTotal := line.Quantity * line.UnitPrice
		subtotal += lineTotal - line.DiscountAmount
		// Tax calculation would need tax rate lookup
	}

	totalAmount := subtotal - discountAmount + taxAmount

	// Marshal addresses to JSON - use null if not provided
	var billingAddressJSON, shippingAddressJSON []byte = []byte("null"), []byte("null")
	if input.BillingAddress != nil {
		billingAddressJSON, _ = json.Marshal(input.BillingAddress)
	}
	if input.ShippingAddress != nil {
		shippingAddressJSON, _ = json.Marshal(input.ShippingAddress)
	}

	// Parse optional UUIDs
	var salesOrderID, currencyID *uuid.UUID
	if input.SalesOrderID != "" {
		id, _ := uuid.Parse(input.SalesOrderID)
		salesOrderID = &id
	}
	if input.CurrencyID != "" {
		id, _ := uuid.Parse(input.CurrencyID)
		currencyID = &id
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Insert sales invoice
	query := `
		INSERT INTO sales_invoices (
			id, tenant_id, invoice_number, customer_id, sales_order_id,
			invoice_date, due_date, billing_address, shipping_address,
			currency_id, exchange_rate, subtotal, discount_amount,
			tax_amount, total_amount, amount_paid, status,
			reference, po_number, notes, terms_conditions,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`

	_, err = h.db.Exec(query,
		invoiceID, tenantID, invoiceNumber, customerID, salesOrderID,
		invoiceDate, dueDate, billingAddressJSON, shippingAddressJSON,
		currencyID, 1.0, subtotal, discountAmount,
		taxAmount, totalAmount, 0, entity.InvoiceStatusDraft,
		input.Reference, input.PONumber, input.Notes, input.TermsConditions,
		createdBy, now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create sales invoice: "+err.Error())
		return
	}

	// Insert invoice lines
	for i, line := range input.Lines {
		lineID := uuid.New()

		lineTotal := line.Quantity * line.UnitPrice - line.DiscountAmount

		var productID, unitID, taxID, salesOrderLineID, accountID *uuid.UUID
		if line.ProductID != "" {
			id, _ := uuid.Parse(line.ProductID)
			productID = &id
		}
		if line.UnitID != "" {
			id, _ := uuid.Parse(line.UnitID)
			unitID = &id
		}
		if line.TaxID != "" {
			id, _ := uuid.Parse(line.TaxID)
			taxID = &id
		}
		if line.SalesOrderLineID != "" {
			id, _ := uuid.Parse(line.SalesOrderLineID)
			salesOrderLineID = &id
		}
		if line.AccountID != "" {
			id, _ := uuid.Parse(line.AccountID)
			accountID = &id
		}

		lineQuery := `
			INSERT INTO sales_invoice_lines (
				id, sales_invoice_id, sales_order_line_id, line_number, product_id, description,
				quantity, unit_id, unit_price, discount_amount,
				tax_id, tax_amount, line_total, account_id, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

		h.db.Exec(lineQuery,
			lineID, invoiceID, salesOrderLineID, i+1, productID, line.Description,
			line.Quantity, unitID, line.UnitPrice, line.DiscountAmount,
			taxID, 0.0, lineTotal, accountID, now,
		)
	}

	// Return created invoice
	invoiceResponse := map[string]interface{}{
		"id":              invoiceID.String(),
		"tenant_id":       tenantID.String(),
		"invoice_number":  invoiceNumber,
		"customer_id":     customerID.String(),
		"invoice_date":    invoiceDate.Format("2006-01-02"),
		"due_date":        dueDate.Format("2006-01-02"),
		"subtotal":        subtotal,
		"discount_amount": discountAmount,
		"tax_amount":      taxAmount,
		"total_amount":    totalAmount,
		"amount_paid":     0.0,
		"amount_due":      totalAmount,
		"status":          string(entity.InvoiceStatusDraft),
		"created_at":      now,
	}

	if input.Reference != "" {
		invoiceResponse["reference"] = input.Reference
	}
	if input.PONumber != "" {
		invoiceResponse["po_number"] = input.PONumber
	}
	if input.Notes != "" {
		invoiceResponse["notes"] = input.Notes
	}

	response.Created(c, invoiceResponse)
}

// GetSalesInvoice returns a single sales invoice by ID
func (h *Handler) GetSalesInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	// Get invoice
	query := `
		SELECT id, tenant_id, organization_id, invoice_number, customer_id, sales_order_id,
			   invoice_date, due_date, billing_address, shipping_address,
			   currency_id, exchange_rate, subtotal, discount_amount,
			   tax_amount, total_amount, amount_paid, amount_due, status,
			   reference, po_number, notes, terms_conditions,
			   journal_entry_id, sent_at, viewed_at, created_by, created_at, updated_at
		FROM sales_invoices
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`

	var id, tenantIDScan, customerID uuid.UUID
	var organizationID, salesOrderID, currencyID, journalEntryID, createdBy sql.NullString
	var invoiceNumber, status string
	var invoiceDate, dueDate time.Time
	var sentAt, viewedAt sql.NullTime
	var billingAddress, shippingAddress []byte
	var exchangeRate, subtotal, discountAmount, taxAmount, totalAmount, amountPaid, amountDue float64
	var reference, poNumber, notes, termsConditions sql.NullString
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(query, invoiceID, tenantID).Scan(
		&id, &tenantIDScan, &organizationID, &invoiceNumber, &customerID, &salesOrderID,
		&invoiceDate, &dueDate, &billingAddress, &shippingAddress,
		&currencyID, &exchangeRate, &subtotal, &discountAmount,
		&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
		&reference, &poNumber, &notes, &termsConditions,
		&journalEntryID, &sentAt, &viewedAt, &createdBy, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch sales invoice: "+err.Error())
		return
	}

	invoice := map[string]interface{}{
		"id":              id.String(),
		"tenant_id":       tenantIDScan.String(),
		"invoice_number":  invoiceNumber,
		"customer_id":     customerID.String(),
		"invoice_date":    invoiceDate.Format("2006-01-02"),
		"due_date":        dueDate.Format("2006-01-02"),
		"exchange_rate":   exchangeRate,
		"subtotal":        subtotal,
		"discount_amount": discountAmount,
		"tax_amount":      taxAmount,
		"total_amount":    totalAmount,
		"amount_paid":     amountPaid,
		"amount_due":      amountDue,
		"status":          status,
		"created_at":      createdAt,
		"updated_at":      updatedAt,
	}

	if organizationID.Valid {
		invoice["organization_id"] = organizationID.String
	}
	if salesOrderID.Valid {
		invoice["sales_order_id"] = salesOrderID.String
	}
	if currencyID.Valid {
		invoice["currency_id"] = currencyID.String
	}
	if reference.Valid {
		invoice["reference"] = reference.String
	}
	if poNumber.Valid {
		invoice["po_number"] = poNumber.String
	}
	if notes.Valid {
		invoice["notes"] = notes.String
	}
	if termsConditions.Valid {
		invoice["terms_conditions"] = termsConditions.String
	}
	if journalEntryID.Valid {
		invoice["journal_entry_id"] = journalEntryID.String
	}
	if sentAt.Valid {
		invoice["sent_at"] = sentAt.Time
	}
	if viewedAt.Valid {
		invoice["viewed_at"] = viewedAt.Time
	}

	// Parse addresses
	if len(billingAddress) > 0 {
		var addr map[string]interface{}
		if json.Unmarshal(billingAddress, &addr) == nil {
			invoice["billing_address"] = addr
		}
	}
	if len(shippingAddress) > 0 {
		var addr map[string]interface{}
		if json.Unmarshal(shippingAddress, &addr) == nil {
			invoice["shipping_address"] = addr
		}
	}

	// Get invoice lines
	linesQuery := `
		SELECT id, line_number, product_id, description, quantity, unit_id, unit_price,
			   discount_amount, tax_id, tax_amount, line_total, account_id
		FROM sales_invoice_lines
		WHERE sales_invoice_id = $1
		ORDER BY line_number`

	linesRows, err := h.db.Query(linesQuery, invoiceID)
	if err == nil {
		defer linesRows.Close()
		var lines []map[string]interface{}
		for linesRows.Next() {
			var lineID uuid.UUID
			var lineNumber int
			var description string
			var quantity, unitPrice, lineDiscountAmount, lineTaxAmount, lineTotal float64
			var productID, unitID, taxID, accountID sql.NullString

			err := linesRows.Scan(
				&lineID, &lineNumber, &productID, &description, &quantity, &unitID, &unitPrice,
				&lineDiscountAmount, &taxID, &lineTaxAmount, &lineTotal, &accountID,
			)
			if err != nil {
				continue
			}

			line := map[string]interface{}{
				"id":              lineID.String(),
				"line_number":     lineNumber,
				"description":     description,
				"quantity":        quantity,
				"unit_price":      unitPrice,
				"discount_amount": lineDiscountAmount,
				"tax_amount":      lineTaxAmount,
				"line_total":      lineTotal,
			}

			if productID.Valid {
				line["product_id"] = productID.String
			}
			if unitID.Valid {
				line["unit_id"] = unitID.String
			}
			if taxID.Valid {
				line["tax_id"] = taxID.String
			}
			if accountID.Valid {
				line["account_id"] = accountID.String
			}

			lines = append(lines, line)
		}
		invoice["lines"] = lines
	}

	response.Success(c, invoice)
}

// UpdateSalesInvoice updates an existing sales invoice
func (h *Handler) UpdateSalesInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	var input struct {
		DueDate         *string  `json:"due_date,omitempty"`
		Reference       *string  `json:"reference,omitempty"`
		PONumber        *string  `json:"po_number,omitempty"`
		Notes           *string  `json:"notes,omitempty"`
		TermsConditions *string  `json:"terms_conditions,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if invoice exists and is in draft status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", invoiceID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if currentStatus != string(entity.InvoiceStatusDraft) {
		response.BadRequest(c, "Can only update invoices in draft status")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.DueDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("due_date = $%d", argCount))
		dd, _ := time.Parse("2006-01-02", *input.DueDate)
		args = append(args, dd)
	}
	if input.Reference != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("reference = $%d", argCount))
		args = append(args, *input.Reference)
	}
	if input.PONumber != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("po_number = $%d", argCount))
		args = append(args, *input.PONumber)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.TermsConditions != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("terms_conditions = $%d", argCount))
		args = append(args, *input.TermsConditions)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, invoiceID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE sales_invoices SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to update sales invoice: "+err.Error())
		return
	}

	// Fetch and return updated invoice
	h.GetSalesInvoice(c)
}

// DeleteSalesInvoice soft deletes a sales invoice
func (h *Handler) DeleteSalesInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	// Check if invoice is in draft status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", invoiceID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if currentStatus != string(entity.InvoiceStatusDraft) {
		response.BadRequest(c, "Can only delete invoices in draft status")
		return
	}

	result, err := h.db.Exec(
		"UPDATE sales_invoices SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete sales invoice")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Sales invoice")
		return
	}

	response.NoContent(c)
}

// SendInvoice marks an invoice as sent
func (h *Handler) SendInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", invoiceID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if currentStatus != string(entity.InvoiceStatusDraft) {
		response.BadRequest(c, "Can only send invoices in draft status")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE sales_invoices SET status = $1, sent_at = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		entity.InvoiceStatusSent, now, now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to send invoice")
		return
	}

	h.GetSalesInvoice(c)
}

// RecordPayment records a payment against an invoice
func (h *Handler) RecordPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	var input struct {
		Amount      float64 `json:"amount" binding:"required,gt=0"`
		PaymentDate string  `json:"payment_date" binding:"required"`
		Reference   string  `json:"reference,omitempty"`
		Notes       string  `json:"notes,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Get current invoice status and amounts
	var currentStatus string
	var amountPaid, totalAmount float64
	err = h.db.QueryRow(
		"SELECT status, amount_paid, total_amount FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		invoiceID, tenantID,
	).Scan(&currentStatus, &amountPaid, &totalAmount)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if currentStatus == string(entity.InvoiceStatusCancelled) {
		response.BadRequest(c, "Cannot record payment for cancelled invoice")
		return
	}
	if currentStatus == string(entity.InvoiceStatusPaid) {
		response.BadRequest(c, "Invoice is already fully paid")
		return
	}

	amountDue := totalAmount - amountPaid
	if input.Amount > amountDue {
		response.BadRequest(c, fmt.Sprintf("Payment amount exceeds amount due (%.2f)", amountDue))
		return
	}

	newAmountPaid := amountPaid + input.Amount
	newStatus := entity.InvoiceStatusPartial
	if newAmountPaid >= totalAmount {
		newStatus = entity.InvoiceStatusPaid
	}

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE sales_invoices SET amount_paid = $1, status = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		newAmountPaid, newStatus, now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to record payment")
		return
	}

	h.GetSalesInvoice(c)
}
