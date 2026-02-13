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

	// Build query with filters - join with contacts for customer_name
	baseQuery := `
		SELECT si.id, si.tenant_id, si.organization_id, si.invoice_number, si.customer_id, si.sales_order_id,
			   si.invoice_date, si.due_date, si.billing_address, si.shipping_address,
			   si.currency_id, si.exchange_rate, si.subtotal, si.discount_amount,
			   si.tax_amount, si.total_amount, si.amount_paid, si.amount_due, si.status,
			   si.reference, si.po_number, si.notes, si.terms_conditions,
			   si.journal_entry_id, si.sent_at, si.viewed_at, si.created_by, si.created_at, si.updated_at,
			   COALESCE(c.name, si.customer_name, '') as customer_name,
			   COALESCE(si.invoice_type, 'invoice') as invoice_type, si.original_invoice_id, si.reason
		FROM sales_invoices si
		LEFT JOIN contacts c ON si.customer_id = c.id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM sales_invoices si WHERE si.tenant_id = $1 AND si.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND si.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND si.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND si.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND si.status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by customer_id
	if customerID := c.Query("customer_id"); customerID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND si.customer_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND si.customer_id = $%d", argCount)
		args = append(args, customerID)
	}

	// Filter by date range
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND si.invoice_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND si.invoice_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND si.invoice_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND si.invoice_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Filter by due date range
	if dueFrom := c.Query("due_from"); dueFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND si.due_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND si.due_date >= $%d", argCount)
		args = append(args, dueFrom)
	}
	if dueTo := c.Query("due_to"); dueTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND si.due_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND si.due_date <= $%d", argCount)
		args = append(args, dueTo)
	}

	// Filter overdue invoices
	if overdue := c.Query("overdue"); overdue == "true" {
		baseQuery += " AND si.due_date < CURRENT_DATE AND si.status NOT IN ('paid', 'cancelled')"
		countQuery += " AND si.due_date < CURRENT_DATE AND si.status NOT IN ('paid', 'cancelled')"
	}

	// Filter by invoice_type (invoice or credit_note)
	if invoiceType := c.Query("invoice_type"); invoiceType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND COALESCE(si.invoice_type, 'invoice') = $%d", argCount)
		countQuery += fmt.Sprintf(" AND COALESCE(si.invoice_type, 'invoice') = $%d", argCount)
		args = append(args, invoiceType)
	}

	// Search - also search by customer name
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(si.invoice_number) LIKE $%d OR LOWER(si.reference) LIKE $%d OR LOWER(si.po_number) LIKE $%d OR LOWER(c.name) LIKE $%d)", argCount, argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(si.invoice_number) LIKE $%d OR LOWER(si.reference) LIKE $%d OR LOWER(si.po_number) LIKE $%d)", argCount, argCount, argCount)
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
	baseQuery += fmt.Sprintf(" ORDER BY si.created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
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
		var customerName string
		var invoiceType string
		var originalInvoiceID sql.NullString
		var reason sql.NullString

		err := rows.Scan(
			&id, &tenantIDScan, &organizationID, &invoiceNumber, &customerID, &salesOrderID,
			&invoiceDate, &dueDate, &billingAddress, &shippingAddress,
			&currencyID, &exchangeRate, &subtotal, &discountAmount,
			&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
			&reference, &poNumber, &notes, &termsConditions,
			&journalEntryID, &sentAt, &viewedAt, &createdBy, &createdAt, &updatedAt,
			&customerName,
			&invoiceType, &originalInvoiceID, &reason,
		)
		if err != nil {
			continue
		}

		// Determine payment status based on amounts
		paymentStatus := "unpaid"
		if amountPaid >= totalAmount && totalAmount > 0 {
			paymentStatus = "paid"
		} else if amountPaid > 0 {
			paymentStatus = "partial"
		}

		invoice := map[string]interface{}{
			"id":              id.String(),
			"tenant_id":       tenantIDScan.String(),
			"invoice_number":  invoiceNumber,
			"customer_id":     customerID.String(),
			"customer_name":   customerName,
			"invoice_date":    invoiceDate.Format("2006-01-02"),
			"due_date":        dueDate.Format("2006-01-02"),
			"exchange_rate":   exchangeRate,
			"subtotal":        subtotal,
			"discount_amount": discountAmount,
			"tax_amount":      taxAmount,
			"total_amount":    totalAmount,
			"amount_paid":     amountPaid,
			"amount_due":      amountDue,
			"balance":         amountDue, // Add balance as alias for amount_due for frontend compatibility
			"status":          status,
			"payment_status":  paymentStatus,
			"invoice_type":    invoiceType,
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
		if originalInvoiceID.Valid {
			invoice["original_invoice_id"] = originalInvoiceID.String
		}
		if reason.Valid {
			invoice["reason"] = reason.String
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
	var salesOrderID, currencyID, orgID *uuid.UUID
	if input.OrganizationID != "" {
		id, _ := uuid.Parse(input.OrganizationID)
		orgID = &id
	}
	// Fallback to middleware header if not provided in body
	if orgID == nil {
		if headerOrgID, orgOk := middleware.GetOrganizationID(c); orgOk && headerOrgID != uuid.Nil {
			orgID = &headerOrgID
		}
	}
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
			id, tenant_id, organization_id, invoice_number, customer_id, sales_order_id,
			invoice_date, due_date, billing_address, shipping_address,
			currency_id, exchange_rate, subtotal, discount_amount,
			tax_amount, total_amount, amount_paid, status,
			reference, po_number, notes, terms_conditions,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)`

	_, err = h.db.Exec(query,
		invoiceID, tenantID, orgID, invoiceNumber, customerID, salesOrderID,
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

	// Get invoice with customer name
	query := `
		SELECT si.id, si.tenant_id, si.organization_id, si.invoice_number, si.customer_id, si.sales_order_id,
			   si.invoice_date, si.due_date, si.billing_address, si.shipping_address,
			   si.currency_id, si.exchange_rate, si.subtotal, si.discount_amount,
			   si.tax_amount, si.total_amount, si.amount_paid, si.amount_due, si.status,
			   si.reference, si.po_number, si.notes, si.terms_conditions,
			   si.journal_entry_id, si.sent_at, si.viewed_at, si.created_by, si.created_at, si.updated_at,
			   COALESCE(c.name, si.customer_name, '') as customer_name,
			   COALESCE(si.invoice_type, 'invoice') as invoice_type, si.original_invoice_id, si.reason
		FROM sales_invoices si
		LEFT JOIN contacts c ON si.customer_id = c.id
		WHERE si.id = $1 AND si.tenant_id = $2 AND si.deleted_at IS NULL`

	var id, tenantIDScan, customerID uuid.UUID
	var organizationID, salesOrderID, currencyID, journalEntryID, createdBy sql.NullString
	var invoiceNumber, status string
	var invoiceDate, dueDate time.Time
	var sentAt, viewedAt sql.NullTime
	var billingAddress, shippingAddress []byte
	var exchangeRate, subtotal, discountAmount, taxAmount, totalAmount, amountPaid, amountDue float64
	var reference, poNumber, notes, termsConditions sql.NullString
	var createdAt, updatedAt time.Time
	var customerName string
	var invoiceType string
	var originalInvoiceID sql.NullString
	var reason sql.NullString

	err = h.db.QueryRow(query, invoiceID, tenantID).Scan(
		&id, &tenantIDScan, &organizationID, &invoiceNumber, &customerID, &salesOrderID,
		&invoiceDate, &dueDate, &billingAddress, &shippingAddress,
		&currencyID, &exchangeRate, &subtotal, &discountAmount,
		&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
		&reference, &poNumber, &notes, &termsConditions,
		&journalEntryID, &sentAt, &viewedAt, &createdBy, &createdAt, &updatedAt,
		&customerName,
		&invoiceType, &originalInvoiceID, &reason,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch sales invoice: "+err.Error())
		return
	}

	// Determine payment status based on amounts
	paymentStatus := "unpaid"
	if amountPaid >= totalAmount && totalAmount > 0 {
		paymentStatus = "paid"
	} else if amountPaid > 0 {
		paymentStatus = "partial"
	}

	invoice := map[string]interface{}{
		"id":              id.String(),
		"tenant_id":       tenantIDScan.String(),
		"invoice_number":  invoiceNumber,
		"customer_id":     customerID.String(),
		"customer_name":   customerName,
		"invoice_date":    invoiceDate.Format("2006-01-02"),
		"due_date":        dueDate.Format("2006-01-02"),
		"exchange_rate":   exchangeRate,
		"subtotal":        subtotal,
		"discount_amount": discountAmount,
		"tax_amount":      taxAmount,
		"total_amount":    totalAmount,
		"amount_paid":     amountPaid,
		"amount_due":      amountDue,
		"balance":         amountDue, // Alias for frontend compatibility
		"status":          status,
		"payment_status":  paymentStatus,
		"invoice_type":    invoiceType,
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
	if originalInvoiceID.Valid {
		invoice["original_invoice_id"] = originalInvoiceID.String
	}
	if reason.Valid {
		invoice["reason"] = reason.String
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

	// Get invoice lines (join with products to get name if description is null)
	linesQuery := `
		SELECT sil.id, sil.line_number, sil.product_id, COALESCE(NULLIF(sil.description, ''), p.name) as description,
			   sil.quantity, sil.unit_id, sil.unit_price, sil.discount_amount, sil.tax_id, sil.tax_amount,
			   sil.line_total, sil.account_id
		FROM sales_invoice_lines sil
		LEFT JOIN products p ON p.id = sil.product_id
		WHERE sil.sales_invoice_id = $1
		ORDER BY sil.line_number`

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
		DueDate         *string `json:"due_date,omitempty"`
		Reference       *string `json:"reference,omitempty"`
		PONumber        *string `json:"po_number,omitempty"`
		Notes           *string `json:"notes,omitempty"`
		TermsConditions *string `json:"terms_conditions,omitempty"`
		Status          *string `json:"status,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if invoice exists
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", invoiceID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}

	// Only allow updating non-status fields if draft, but allow status changes from draft to sent
	isStatusOnlyUpdate := input.Status != nil && input.DueDate == nil && input.Reference == nil && input.PONumber == nil && input.Notes == nil && input.TermsConditions == nil
	if !isStatusOnlyUpdate && currentStatus != string(entity.InvoiceStatusDraft) {
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
	if input.Status != nil {
		// Validate status transition
		validStatuses := map[string]bool{"draft": true, "sent": true, "paid": true, "cancelled": true}
		if !validStatuses[*input.Status] {
			response.BadRequest(c, "Invalid status")
			return
		}
		// Only allow draft -> sent, or sent -> paid transitions
		if currentStatus == "draft" && (*input.Status != "sent" && *input.Status != "cancelled") {
			response.BadRequest(c, "Draft invoices can only be sent or cancelled")
			return
		}
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
		// If sending, record sent_at
		if *input.Status == "sent" {
			argCount++
			updates = append(updates, fmt.Sprintf("sent_at = $%d", argCount))
			args = append(args, time.Now())
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

// SendInvoice marks an invoice as sent and creates GL journal entry
func (h *Handler) SendInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	// Get invoice details
	var currentStatus, invoiceNumber string
	var customerID uuid.UUID
	var organizationID *uuid.UUID
	var totalAmount, taxAmount, subtotal float64
	var invoiceDate time.Time
	err = h.db.QueryRow(`
		SELECT status, invoice_number, customer_id, organization_id, total_amount, tax_amount, subtotal, invoice_date
		FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		invoiceID, tenantID,
	).Scan(&currentStatus, &invoiceNumber, &customerID, &organizationID, &totalAmount, &taxAmount, &subtotal, &invoiceDate)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch invoice")
		return
	}
	if currentStatus != string(entity.InvoiceStatusDraft) {
		response.BadRequest(c, "Can only send invoices in draft status")
		return
	}

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, invoiceDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	now := time.Now()

	// Start transaction for GL posting
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Get Sales Journal ID
	var salesJournalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString
	err = tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1), number_prefix
		FROM journals WHERE tenant_id = $1 AND code = 'SALES' AND deleted_at IS NULL`,
		tenantID,
	).Scan(&salesJournalID, &nextNumber, &numberPrefix)
	if err != nil {
		// Journal doesn't exist yet, skip GL posting but still send invoice
		h.log.Warn("Sales journal not found, skipping GL posting", "tenant_id", tenantID)
		_, err = tx.Exec(
			"UPDATE sales_invoices SET status = $1, sent_at = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
			entity.InvoiceStatusSent, now, now, invoiceID, tenantID,
		)
		if err != nil {
			response.InternalError(c, "Failed to send invoice")
			return
		}
		if err := tx.Commit(); err != nil {
			response.InternalError(c, "Failed to commit transaction")
			return
		}
		h.GetSalesInvoice(c)
		return
	}

	// Odoo-style: AR + per-category Income + COGS/Interim clearing
	arAccountID := findAccount(tx, tenantID, organizationID, "accounts receivable", "1100")
	if arAccountID == uuid.Nil {
		arAccountID = findAccount(tx, tenantID, organizationID, "accounts receivable", "1200")
	}
	if arAccountID == uuid.Nil {
		h.log.Warn("AR account not found, skipping GL posting", "tenant_id", tenantID)
		_, err = tx.Exec(
			"UPDATE sales_invoices SET status = $1, sent_at = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
			entity.InvoiceStatusSent, now, now, invoiceID, tenantID,
		)
		if err != nil {
			response.InternalError(c, "Failed to send invoice")
			return
		}
		if err := tx.Commit(); err != nil {
			response.InternalError(c, "Failed to commit transaction")
			return
		}
		h.GetSalesInvoice(c)
		return
	}

	taxAccountID := findAccount(tx, tenantID, organizationID, "tax", "2100")

	// Get invoice lines for per-category accounting
	type invoiceLineAcct struct {
		ProductID    uuid.UUID
		LineTotal    float64
		Quantity     float64
		CostPrice    float64
		IncomeAcct   uuid.UUID
		ExpenseAcct  uuid.UUID
		OutputAcct   uuid.UUID
	}
	var invoiceLines []invoiceLineAcct
	lineRows, lineErr := tx.Query(`
		SELECT sil.product_id, COALESCE(sil.line_total, 0), COALESCE(sil.quantity, 0), COALESCE(p.cost_price, 0)
		FROM sales_invoice_lines sil
		JOIN products p ON sil.product_id = p.id
		WHERE sil.sales_invoice_id = $1
	`, invoiceID)
	if lineErr == nil {
		for lineRows.Next() {
			var il invoiceLineAcct
			if err := lineRows.Scan(&il.ProductID, &il.LineTotal, &il.Quantity, &il.CostPrice); err == nil {
				invoiceLines = append(invoiceLines, il)
			}
		}
		lineRows.Close()
		// Resolve category accounts after closing rows
		for i := range invoiceLines {
			ca := getCategoryAccounts(tx, tenantID, organizationID, invoiceLines[i].ProductID)
			invoiceLines[i].IncomeAcct = ca.IncomeAccountID
			invoiceLines[i].ExpenseAcct = ca.ExpenseAccountID
			invoiceLines[i].OutputAcct = ca.StockOutputAccountID
		}
	}

	// Group revenue by income account
	revenueGrouped := make(map[uuid.UUID]float64)
	// Group COGS by expense/output account pair
	type cogsPair struct {
		Expense uuid.UUID
		Output  uuid.UUID
	}
	cogsGrouped := make(map[cogsPair]float64)

	for _, il := range invoiceLines {
		if il.LineTotal > 0 {
			revenueGrouped[il.IncomeAcct] += il.LineTotal
		}
		costAmount := il.Quantity * il.CostPrice
		if costAmount > 0 {
			cogsGrouped[cogsPair{Expense: il.ExpenseAcct, Output: il.OutputAcct}] += costAmount
		}
	}

	// Fallback if no lines found
	if len(revenueGrouped) == 0 && subtotal > 0 {
		fallbackRevenue := findAccount(tx, tenantID, organizationID, "sales revenue", "4000")
		if fallbackRevenue != uuid.Nil {
			revenueGrouped[fallbackRevenue] = subtotal
		}
	}

	// Generate entry number
	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}
	entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

	// Calculate total debit = AR total + COGS total
	var totalCogs float64
	for _, amt := range cogsGrouped {
		totalCogs += amt
	}
	totalDebit := totalAmount + totalCogs
	totalCredit := totalDebit // Must balance

	// Create journal entry
	journalEntryID := uuid.New()
	description := fmt.Sprintf("Sales Invoice %s", invoiceNumber)

	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
		journalEntryID, tenantID, organizationID, salesJournalID, entryNumber, invoiceDate, invoiceNumber, description,
		"sales_invoice", invoiceID.String(), 1.0, totalDebit, totalCredit, userID, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create journal entry", "error", err)
		response.InternalError(c, "Failed to create journal entry")
		return
	}

	lineNumber := 1

	// Debit: Accounts Receivable (invoice total)
	arLineID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO journal_entry_lines (
			id, journal_entry_id, line_number, account_id, contact_id, description,
			debit_amount, credit_amount, exchange_rate, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		arLineID, journalEntryID, lineNumber, arAccountID, customerID, "Accounts Receivable",
		totalAmount, 0.0, 1.0, now,
	)
	if err != nil {
		h.log.Error("Failed to create AR line", "error", err)
		response.InternalError(c, "Failed to create journal entry")
		return
	}
	tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalAmount, now, arAccountID)
	lineNumber++

	// Credit: Income/Revenue (per category)
	for incomeAcct, amount := range revenueGrouped {
		revenueLineID := uuid.New()
		tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			revenueLineID, journalEntryID, lineNumber, incomeAcct, "Sales Revenue",
			0.0, amount, 1.0, now,
		)
		tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", amount, now, incomeAcct)
		lineNumber++
	}

	// Credit: Tax Payable (if tax > 0)
	if taxAccountID != uuid.Nil && taxAmount > 0 {
		taxLineID := uuid.New()
		tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			taxLineID, journalEntryID, lineNumber, taxAccountID, "Sales Tax Payable",
			0.0, taxAmount, 1.0, now,
		)
		tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", taxAmount, now, taxAccountID)
		lineNumber++
	}

	// COGS entries: Debit Expense, Credit Stock Interim Delivery (clears interim from shipping)
	for pair, costAmount := range cogsGrouped {
		// Debit: COGS/Expense
		cogsLineID := uuid.New()
		tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			cogsLineID, journalEntryID, lineNumber, pair.Expense, "Cost of Goods Sold",
			costAmount, 0.0, 1.0, now,
		)
		tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", costAmount, now, pair.Expense)
		lineNumber++

		// Credit: Stock Interim Delivery (clears interim)
		outputLineID := uuid.New()
		tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			outputLineID, journalEntryID, lineNumber, pair.Output, "Stock Interim Delivery",
			0.0, costAmount, 1.0, now,
		)
		tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", costAmount, now, pair.Output)
		lineNumber++
	}

	// Update journal next number
	tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", salesJournalID)

	// Update invoice with journal entry ID and status
	_, err = tx.Exec(
		"UPDATE sales_invoices SET status = $1, sent_at = $2, journal_entry_id = $3, updated_at = $4 WHERE id = $5 AND tenant_id = $6",
		entity.InvoiceStatusSent, now, journalEntryID, now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to send invoice")
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	h.GetSalesInvoice(c)
}

// RecordPayment records a payment against an invoice and creates GL journal entry
func (h *Handler) RecordPayment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	var input struct {
		Amount        float64 `json:"amount" binding:"required,gt=0"`
		PaymentDate   string  `json:"payment_date" binding:"required"`
		PaymentMethod string  `json:"payment_method,omitempty"`
		Reference     string  `json:"reference,omitempty"`
		Notes         string  `json:"notes,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Parse payment date
	paymentDate, err := time.Parse("2006-01-02", input.PaymentDate)
	if err != nil {
		response.BadRequest(c, "Invalid payment_date format, expected YYYY-MM-DD")
		return
	}

	// Get current invoice status and amounts
	var currentStatus, invoiceNumber string
	var customerID uuid.UUID
	var organizationID *uuid.UUID
	var amountPaid, totalAmount float64
	err = h.db.QueryRow(`
		SELECT status, invoice_number, customer_id, organization_id, amount_paid, total_amount
		FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		invoiceID, tenantID,
	).Scan(&currentStatus, &invoiceNumber, &customerID, &organizationID, &amountPaid, &totalAmount)
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

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, paymentDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	now := time.Now()

	// Start transaction for GL posting
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Get Cash Receipts Journal ID
	var cashJournalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString
	err = tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1), number_prefix
		FROM journals WHERE tenant_id = $1 AND code = 'CASH_RECEIPTS' AND deleted_at IS NULL`,
		tenantID,
	).Scan(&cashJournalID, &nextNumber, &numberPrefix)

	// Get default account IDs — lookup by name first, then code fallback
	arAccountID := findAccount(tx, tenantID, organizationID, "accounts receivable", "1200")
	// Choose cash/bank account based on payment method
	var cashAccountID uuid.UUID
	if input.PaymentMethod == "cash" {
		cashAccountID = findAccount(tx, tenantID, organizationID, "cash", "1000")
		if cashAccountID == uuid.Nil {
			cashAccountID = findAccount(tx, tenantID, organizationID, "petty cash", "1010")
		}
	} else {
		// Default to bank account for bank_transfer, check, etc.
		cashAccountID = findAccount(tx, tenantID, organizationID, "bank account", "1100")
		if cashAccountID == uuid.Nil {
			cashAccountID = findAccount(tx, tenantID, organizationID, "petty cash", "1010")
		}
	}

	// Create GL entry if accounts exist
	if cashJournalID != uuid.Nil && arAccountID != uuid.Nil && cashAccountID != uuid.Nil {
		// Generate entry number
		prefix := ""
		if numberPrefix.Valid {
			prefix = numberPrefix.String
		}
		entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

		// Create journal entry
		journalEntryID := uuid.New()
		description := fmt.Sprintf("Payment received for Invoice %s", invoiceNumber)
		reference := input.Reference
		if reference == "" {
			reference = invoiceNumber
		}

		_, err = tx.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
			journalEntryID, tenantID, organizationID, cashJournalID, entryNumber, paymentDate, reference, description,
			"payment_receipt", invoiceID.String(), 1.0, input.Amount, input.Amount, "posted", userID, now, now,
		)
		if err != nil {
			h.log.Error("Failed to create payment journal entry", "error", err)
			// Continue without GL posting
		} else {
			// Line 1: Debit Cash/Bank
			cashLineID := uuid.New()
			_, err = tx.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, contact_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				cashLineID, journalEntryID, 1, cashAccountID, customerID, "Cash Receipt",
				input.Amount, 0.0, 1.0, now,
			)

			// Line 2: Credit AR
			arLineID := uuid.New()
			_, err = tx.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, contact_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				arLineID, journalEntryID, 2, arAccountID, customerID, "Accounts Receivable",
				0.0, input.Amount, 1.0, now,
			)

			// Update journal next number
			tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", cashJournalID)

			// Update account balances
			// Debit Cash (debit-normal: increases)
			tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", input.Amount, now, cashAccountID)
			// Credit AR (debit-normal: credit decreases)
			tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", input.Amount, now, arAccountID)

			// Create payment record
			paymentID := uuid.New()
			_, err = tx.Exec(`
				INSERT INTO payments (
					id, tenant_id, organization_id, type, payment_number, contact_id, payment_date, amount,
					currency_id, exchange_rate, reference, notes, status, journal_entry_id,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
				paymentID, tenantID, organizationID, "receipt", fmt.Sprintf("REC-%s", entryNumber), customerID, paymentDate, input.Amount,
				nil, 1.0, input.Reference, input.Notes, "confirmed", journalEntryID,
				userID, now, now,
			)

			// Create payment allocation
			if err == nil {
				allocationID := uuid.New()
				tx.Exec(`
					INSERT INTO payment_allocations (
						id, payment_id, document_type, document_id, amount, created_at
					) VALUES ($1, $2, $3, $4, $5, $6)`,
					allocationID, paymentID, "sales_invoice", invoiceID, input.Amount, now,
				)
			}
		}
	}

	// Update invoice
	_, err = tx.Exec(
		"UPDATE sales_invoices SET amount_paid = $1, status = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		newAmountPaid, newStatus, now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to record payment")
		return
	}

	// Update related sales order's payment_status if invoice is linked to an order
	var salesOrderID sql.NullString
	tx.QueryRow(`SELECT sales_order_id FROM sales_invoices WHERE id = $1`, invoiceID).Scan(&salesOrderID)
	if salesOrderID.Valid && salesOrderID.String != "" {
		orderPaymentStatus := "partial"
		if newStatus == entity.InvoiceStatusPaid {
			orderPaymentStatus = "paid"
		}
		tx.Exec(
			"UPDATE sales_orders SET payment_status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
			orderPaymentStatus, now, salesOrderID.String, tenantID,
		)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	h.GetSalesInvoice(c)
}

// CreateCreditNote creates a credit note from an existing sales invoice
func (h *Handler) CreateCreditNote(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	var input entity.CreateCreditNoteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Load original invoice
	var orgInvoiceNumber, currentStatus string
	var customerID uuid.UUID
	var orgID *uuid.UUID
	var subtotal, taxAmount, totalAmount float64
	var currencyID *uuid.UUID

	err = h.db.QueryRow(`
		SELECT invoice_number, status, customer_id, organization_id, subtotal, tax_amount,
			total_amount, currency_id
		FROM sales_invoices
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND invoice_type = 'invoice'`,
		invoiceID, tenantID,
	).Scan(&orgInvoiceNumber, &currentStatus, &customerID, &orgID, &subtotal, &taxAmount,
		&totalAmount, &currencyID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch invoice")
		return
	}

	if currentStatus != string(entity.InvoiceStatusSent) && currentStatus != string(entity.InvoiceStatusPartial) && currentStatus != string(entity.InvoiceStatusPaid) {
		response.BadRequest(c, "Can only create credit notes for sent, partial, or paid invoices")
		return
	}

	cnDate := time.Now()
	if input.CreditNoteDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.CreditNoteDate); err == nil {
			cnDate = parsed
		}
	}

	cnNumber := "CN-" + cnDate.Format("20060102") + "-" + uuid.New().String()[:6]
	creditNoteID := uuid.New()
	now := time.Now()

	var cnSubtotal, cnTaxAmount, cnTotalAmount float64

	if len(input.Lines) > 0 {
		for _, line := range input.Lines {
			cnSubtotal += line.Quantity * line.UnitPrice
		}
		if subtotal > 0 {
			cnTaxAmount = cnSubtotal * (taxAmount / subtotal)
		}
		cnTotalAmount = cnSubtotal + cnTaxAmount
	} else {
		cnSubtotal = subtotal
		cnTaxAmount = taxAmount
		cnTotalAmount = totalAmount
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	_, err = h.db.Exec(`
		INSERT INTO sales_invoices (
			id, tenant_id, organization_id, invoice_number, customer_id,
			invoice_date, due_date, invoice_type, original_invoice_id, reason,
			currency_id, exchange_rate, subtotal, discount_amount,
			tax_amount, total_amount, amount_paid, status,
			reference, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'credit_note', $8, $9, $10, 1.0, $11, 0, $12, $13, 0, 'draft', $14, $15, $16, $17)`,
		creditNoteID, tenantID, orgID, cnNumber, customerID,
		cnDate, cnDate, invoiceID, input.Reason,
		currencyID, cnSubtotal, cnTaxAmount, cnTotalAmount,
		orgInvoiceNumber, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create credit note", "error", err)
		response.InternalError(c, "Failed to create credit note: "+err.Error())
		return
	}

	// Insert credit note lines
	if len(input.Lines) > 0 {
		for i, line := range input.Lines {
			lineID := uuid.New()
			lineTotal := line.Quantity * line.UnitPrice
			var productID, taxID *uuid.UUID
			if line.ProductID != nil && *line.ProductID != "" {
				id, _ := uuid.Parse(*line.ProductID)
				productID = &id
			}
			if line.TaxID != nil && *line.TaxID != "" {
				id, _ := uuid.Parse(*line.TaxID)
				taxID = &id
			}
			h.db.Exec(`
				INSERT INTO sales_invoice_lines (
					id, sales_invoice_id, line_number, product_id, description,
					quantity, unit_price, discount_amount, tax_id, tax_amount, line_total, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, 0, $9, $10)`,
				lineID, creditNoteID, i+1, productID, line.Description,
				line.Quantity, line.UnitPrice, taxID, lineTotal, now,
			)
		}
	} else {
		// Copy lines from original invoice
		rows, err := h.db.Query(`
			SELECT product_id, description, quantity, unit_id, unit_price, discount_amount,
				tax_id, tax_amount, line_total, account_id
			FROM sales_invoice_lines WHERE sales_invoice_id = $1 ORDER BY line_number`, invoiceID)
		if err == nil {
			defer rows.Close()
			lineNum := 1
			for rows.Next() {
				var productID, unitID, taxID, accountID *uuid.UUID
				var desc string
				var qty, unitPrice, discAmt, taxAmt, lineTotal float64
				rows.Scan(&productID, &desc, &qty, &unitID, &unitPrice, &discAmt, &taxID, &taxAmt, &lineTotal, &accountID)
				lineID := uuid.New()
				h.db.Exec(`
					INSERT INTO sales_invoice_lines (
						id, sales_invoice_id, line_number, product_id, description,
						quantity, unit_id, unit_price, discount_amount, tax_id, tax_amount, line_total, account_id, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
					lineID, creditNoteID, lineNum, productID, desc,
					qty, unitID, unitPrice, discAmt, taxID, taxAmt, lineTotal, accountID, now,
				)
				lineNum++
			}
		}
	}

	// Replace the id param so GetSalesInvoice returns the credit note
	for i, p := range c.Params {
		if p.Key == "id" {
			c.Params[i].Value = creditNoteID.String()
			break
		}
	}
	h.GetSalesInvoice(c)
}

// ConfirmCreditNote confirms a credit note and creates reversed GL entries
func (h *Handler) ConfirmCreditNote(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	creditNoteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid credit note ID")
		return
	}

	var currentStatus, cnNumber string
	var customerID uuid.UUID
	var originalInvoiceID *uuid.UUID
	var organizationID *uuid.UUID
	var totalAmount, taxAmount, cnSubtotal float64
	var cnDate time.Time

	err = h.db.QueryRow(`
		SELECT status, invoice_number, customer_id, original_invoice_id, organization_id, total_amount, tax_amount, subtotal, invoice_date
		FROM sales_invoices
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND invoice_type = 'credit_note'`,
		creditNoteID, tenantID,
	).Scan(&currentStatus, &cnNumber, &customerID, &originalInvoiceID, &organizationID, &totalAmount, &taxAmount, &cnSubtotal, &cnDate)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Credit note")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch credit note")
		return
	}
	if currentStatus != string(entity.InvoiceStatusDraft) {
		response.BadRequest(c, "Credit note is not in draft status")
		return
	}

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, cnDate); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	now := time.Now()

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Get Sales Journal
	var salesJournalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString
	err = tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1), number_prefix
		FROM journals WHERE tenant_id = $1 AND code = 'SALES' AND deleted_at IS NULL`,
		tenantID,
	).Scan(&salesJournalID, &nextNumber, &numberPrefix)

	if err == nil {
		arAccountID := findAccount(tx, tenantID, organizationID, "accounts receivable", "1100")
		revenueAccountID := findAccount(tx, tenantID, organizationID, "sales revenue", "4000")
		taxAccountID := findAccount(tx, tenantID, organizationID, "tax", "2100")

		if arAccountID != uuid.Nil {
			prefix := ""
			if numberPrefix.Valid {
				prefix = numberPrefix.String
			}
			entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

			journalEntryID := uuid.New()
			description := fmt.Sprintf("Credit Note %s", cnNumber)

			_, err = tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
				journalEntryID, tenantID, organizationID, salesJournalID, entryNumber, cnDate, cnNumber, description,
				"credit_note", creditNoteID.String(), 1.0, totalAmount, totalAmount, userID, now, now,
			)
			if err != nil {
				h.log.Error("Failed to create credit note journal entry", "error", err)
				response.InternalError(c, "Failed to create journal entry")
				return
			}

			lineNumber := 1

			// Debit Revenue (reversal)
			if revenueAccountID != uuid.Nil && cnSubtotal > 0 {
				lineID := uuid.New()
				tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					lineID, journalEntryID, lineNumber, revenueAccountID, "Revenue Reversal (Credit Note)",
					cnSubtotal, 0.0, 1.0, now,
				)
				lineNumber++
			}

			// Debit Tax Payable (reversal)
			if taxAccountID != uuid.Nil && taxAmount > 0 {
				lineID := uuid.New()
				tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					lineID, journalEntryID, lineNumber, taxAccountID, "Tax Reversal (Credit Note)",
					taxAmount, 0.0, 1.0, now,
				)
				lineNumber++
			}

			// Credit AR (reduce receivable)
			arLineID := uuid.New()
			tx.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, contact_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				arLineID, journalEntryID, lineNumber, arAccountID, customerID, "Accounts Receivable (Credit Note)",
				0.0, totalAmount, 1.0, now,
			)

			// Update account balances
			tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalAmount, now, arAccountID)
			if revenueAccountID != uuid.Nil {
				tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", cnSubtotal, now, revenueAccountID)
			}
			if taxAccountID != uuid.Nil && taxAmount > 0 {
				tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", taxAmount, now, taxAccountID)
			}

			tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", salesJournalID)
			tx.Exec("UPDATE sales_invoices SET journal_entry_id = $1 WHERE id = $2", journalEntryID, creditNoteID)
		}
	}

	// Update credit note status
	_, err = tx.Exec(
		"UPDATE sales_invoices SET status = 'sent', sent_at = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		now, now, creditNoteID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to confirm credit note")
		return
	}

	// Reduce the original invoice balance
	if originalInvoiceID != nil {
		tx.Exec(`
			UPDATE sales_invoices SET amount_paid = amount_paid + $1, updated_at = $2
			WHERE id = $3 AND tenant_id = $4`,
			totalAmount, now, *originalInvoiceID, tenantID,
		)
		tx.Exec(`
			UPDATE sales_invoices SET status = CASE
				WHEN amount_paid >= total_amount THEN 'paid'
				WHEN amount_paid > 0 THEN 'partial'
				ELSE status
			END, updated_at = $1
			WHERE id = $2 AND tenant_id = $3`,
			now, *originalInvoiceID, tenantID,
		)
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	h.GetSalesInvoice(c)
}
