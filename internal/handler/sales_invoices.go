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
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 10000 {
		pageSize = 10000
	}
	offset := (page - 1) * pageSize

	// Build query with filters - join with contacts for customer_name and
	// sales_orders so we can surface the originating order number.
	baseQuery := `
		SELECT si.id, si.tenant_id, si.organization_id, si.invoice_number, si.customer_id, si.sales_order_id,
			   si.invoice_date, si.due_date, si.billing_address, si.shipping_address,
			   si.currency_id, si.exchange_rate, si.subtotal, si.discount_amount,
			   si.tax_amount, si.total_amount, si.amount_paid, si.amount_due, si.status,
			   si.reference, si.po_number, si.notes, si.terms_conditions,
			   si.journal_entry_id, si.sent_at, si.viewed_at, si.created_by, si.created_at, si.updated_at,
			   COALESCE(c.name, si.customer_name, '') as customer_name,
			   COALESCE(si.invoice_type, 'invoice') as invoice_type, si.original_invoice_id, si.reason,
			   si.payment_term_id, COALESCE(si.early_discount_amount, 0), si.early_discount_date,
			   COALESCE(so.order_number, '') as order_number,
			   COALESCE((
				   SELECT string_agg(DISTINCT j.name, ', ')
				   FROM payment_allocations pa
				   JOIN payments p ON pa.payment_id = p.id
				   JOIN journals j ON p.journal_id = j.id
				   WHERE pa.document_id = si.id AND pa.document_type = 'sales_invoice' AND p.status = 'confirmed'
			   ), '') as payment_journals,
			   COALESCE((
				   SELECT string_agg(DISTINCT COALESCE(j.name_uz, j.name), ', ')
				   FROM payment_allocations pa
				   JOIN payments p ON pa.payment_id = p.id
				   JOIN journals j ON p.journal_id = j.id
				   WHERE pa.document_id = si.id AND pa.document_type = 'sales_invoice' AND p.status = 'confirmed'
			   ), '') as payment_journals_uz,
			   COALESCE((
				   SELECT string_agg(DISTINCT COALESCE(j.name_en, j.name), ', ')
				   FROM payment_allocations pa
				   JOIN payments p ON pa.payment_id = p.id
				   JOIN journals j ON p.journal_id = j.id
				   WHERE pa.document_id = si.id AND pa.document_type = 'sales_invoice' AND p.status = 'confirmed'
			   ), '') as payment_journals_en,
			   ` + invoiceOverdueSQL("si") + ` AS is_overdue,
			   ` + invoiceDaysOverdueSQL("si") + ` AS days_overdue,
			   (` + invoicePaymentStatusSQL("si") + `) AS payment_status
		FROM sales_invoices si
		LEFT JOIN contacts c ON si.customer_id = c.id
		LEFT JOIN sales_orders so ON si.sales_order_id = so.id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*)
		FROM sales_invoices si
		LEFT JOIN contacts c ON si.customer_id = c.id
		WHERE si.tenant_id = $1 AND si.deleted_at IS NULL`
	args := []interface{}{tenantID}

	// One predicate for the list, its COUNT and the AR summary — same reason
	// purchaseInvoiceWhere exists. This also brings payment_status filtering,
	// which the row field has always exposed but no query parameter could reach,
	// so both clients were filtering a single loaded page instead.
	where := salesInvoiceWhere(c, &args)
	baseQuery += where
	countQuery += where
	argCount := len(args)

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count sales invoices")
		return
	}

	// Add sorting and pagination
	// si.id DESC tiebreaker — created_at is not unique (seed, import, order->
	// invoice conversion), and without a unique second key LIMIT/OFFSET repeats
	// rows on one page and skips them on another.
	baseQuery += fmt.Sprintf(" ORDER BY si.created_at DESC, si.id DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to fetch sales invoices", "error", err)
		response.InternalError(c, "Failed to fetch sales invoices")
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
		var paymentTermID sql.NullString
		var earlyDiscountAmount float64
		var earlyDiscountDate sql.NullTime
		var paymentJournals, paymentJournalsUz, paymentJournalsEn string
		var orderNumber string
		var isOverdue bool
		var daysOverdue int
		var paymentStatus string

		err := rows.Scan(
			&id, &tenantIDScan, &organizationID, &invoiceNumber, &customerID, &salesOrderID,
			&invoiceDate, &dueDate, &billingAddress, &shippingAddress,
			&currencyID, &exchangeRate, &subtotal, &discountAmount,
			&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
			&reference, &poNumber, &notes, &termsConditions,
			&journalEntryID, &sentAt, &viewedAt, &createdBy, &createdAt, &updatedAt,
			&customerName,
			&invoiceType, &originalInvoiceID, &reason,
			&paymentTermID, &earlyDiscountAmount, &earlyDiscountDate,
			&orderNumber,
			&paymentJournals, &paymentJournalsUz, &paymentJournalsEn,
			&isOverdue, &daysOverdue, &paymentStatus,
		)
		if err != nil {
			continue
		}

		// is_overdue, days_overdue and payment_status now arrive from SQL — the
		// same expressions their filters use, so a row can never come back
		// contradicting the filter that selected it.

		invoice := map[string]interface{}{
			"id":                  id.String(),
			"tenant_id":           tenantIDScan.String(),
			"invoice_number":      invoiceNumber,
			"customer_id":         customerID.String(),
			"customer_name":       customerName,
			"invoice_date":        invoiceDate.Format("2006-01-02"),
			"due_date":            dueDate.Format("2006-01-02"),
			"exchange_rate":       exchangeRate,
			"subtotal":            subtotal,
			"discount_amount":     discountAmount,
			"tax_amount":          taxAmount,
			"total_amount":        totalAmount,
			"amount_paid":         amountPaid,
			"amount_due":          amountDue,
			"balance":             amountDue, // Add balance as alias for amount_due for frontend compatibility
			"status":              status,
			"payment_status":      paymentStatus,
			"is_overdue":          isOverdue,
			"days_overdue":        daysOverdue,
			"invoice_type":        invoiceType,
			"order_number":        orderNumber,
			"created_at":          createdAt,
			"updated_at":          updatedAt,
			"payment_journals":    paymentJournals,
			"payment_journals_uz": paymentJournalsUz,
			"payment_journals_en": paymentJournalsEn,
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
		if paymentTermID.Valid {
			invoice["payment_term_id"] = paymentTermID.String
		}
		if earlyDiscountAmount > 0 {
			invoice["early_discount_amount"] = earlyDiscountAmount
		}
		if earlyDiscountDate.Valid {
			invoice["early_discount_date"] = earlyDiscountDate.Time.Format("2006-01-02")
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
	// due_date: explicit value wins; otherwise derive it from the customer's payment
	// term (payment_terms was a fully-seeded reference table nothing consumed —
	// savdo-audit §5). Fallback: NET30.
	var dueDate time.Time
	if input.DueDate != "" {
		dueDate, err = time.Parse("2006-01-02", input.DueDate)
		if err != nil {
			response.BadRequest(c, "Invalid due_date format, expected YYYY-MM-DD")
			return
		}
	} else {
		dueDate = invoiceDate.AddDate(0, 0, 30)
		var termType string
		var dueDays int
		termErr := h.db.QueryRow(`
			SELECT pt.term_type, COALESCE(pt.due_days, 0)
			FROM contacts c
			JOIN payment_terms pt ON pt.id = c.payment_term_id
			WHERE c.id = $1 AND c.tenant_id = $2 AND pt.is_active = true`,
			input.CustomerID, tenantID).Scan(&termType, &dueDays)
		if termErr == nil {
			switch termType {
			case "immediate":
				dueDate = invoiceDate
			case "end_of_month":
				firstOfNext := time.Date(invoiceDate.Year(), invoiceDate.Month(), 1, 0, 0, 0, 0, invoiceDate.Location()).AddDate(0, 1, 0)
				dueDate = firstOfNext.AddDate(0, 0, -1+dueDays)
			case "end_of_next_month":
				firstOfNext2 := time.Date(invoiceDate.Year(), invoiceDate.Month(), 1, 0, 0, 0, 0, invoiceDate.Location()).AddDate(0, 2, 0)
				dueDate = firstOfNext2.AddDate(0, 0, -1+dueDays)
			default:
				dueDate = invoiceDate.AddDate(0, 0, dueDays)
			}
		}
	}

	// Generate invoice number
	invoiceNumber := "INV-" + time.Now().Format("20060102") + "-" + uuid.New().String()[:6]

	invoiceID := uuid.New()
	now := time.Now()

	// Calculate totals from lines, resolving VAT per line from its tax_id.
	// Rates are cached per tax_id so a multi-line invoice makes at most one
	// lookup per distinct tax. lineTaxAmounts is reused when inserting lines
	// so the stored per-line tax matches the header total.
	var subtotal, taxAmount, discountAmount float64
	taxRateCache := make(map[string]float64)
	lineTaxAmounts := make([]float64, len(input.Lines))
	for i, line := range input.Lines {
		lineNet := line.Quantity*line.UnitPrice - line.DiscountAmount
		subtotal += lineNet

		if line.TaxID != "" {
			rate, cached := taxRateCache[line.TaxID]
			if !cached {
				if tid, perr := uuid.Parse(line.TaxID); perr == nil {
					_ = h.db.QueryRow(
						`SELECT rate FROM tax_rates WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
						tid, tenantID,
					).Scan(&rate)
				}
				taxRateCache[line.TaxID] = rate
			}
			lineTax := lineNet * rate / 100.0
			lineTaxAmounts[i] = lineTax
			taxAmount += lineTax
		}
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

	// Lock the exchange rate at invoice creation time
	exchangeRate := 1.0
	if currencyID != nil {
		// This tenant's base currency (currency_scope.go). Previously this read
		// a global flag, so an invoice could be converted against another
		// tenant's chosen base.
		baseCurrencyID, _ := h.baseCurrencyID(tenantID)
		if baseCurrencyID != uuid.Nil && *currencyID != baseCurrencyID {
			var lockedRate float64
			errRate := h.db.QueryRow(`
				SELECT rate FROM exchange_rates
				WHERE from_currency_id = $1 AND to_currency_id = $2
				ORDER BY effective_date DESC LIMIT 1
			`, *currencyID, baseCurrencyID).Scan(&lockedRate)
			if errRate == nil && lockedRate > 0 {
				exchangeRate = lockedRate
			}
		}
	}

	// Header + lines in one transaction: previously bare h.db.Exec calls could leave a
	// header with no lines, and customer_name was never written (the overdue scanner
	// skipped such invoices).
	var customerName string
	_ = h.db.QueryRow("SELECT COALESCE(name, '') FROM contacts WHERE id = $1 AND tenant_id = $2", customerID, tenantID).Scan(&customerName)

	tx, txErr := h.db.Begin()
	if txErr != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	query := `
		INSERT INTO sales_invoices (
			id, tenant_id, organization_id, invoice_number, customer_id, customer_name, sales_order_id,
			invoice_date, due_date, billing_address, shipping_address,
			currency_id, exchange_rate, subtotal, discount_amount,
			tax_amount, total_amount, amount_paid, status,
			reference, po_number, notes, terms_conditions,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)`

	_, err = tx.Exec(query,
		invoiceID, tenantID, orgID, invoiceNumber, customerID, customerName, salesOrderID,
		invoiceDate, dueDate, billingAddressJSON, shippingAddressJSON,
		currencyID, exchangeRate, subtotal, discountAmount,
		taxAmount, totalAmount, 0, entity.InvoiceStatusDraft,
		input.Reference, input.PONumber, input.Notes, input.TermsConditions,
		createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create sales invoice", "error", err)
		response.InternalError(c, "Failed to create sales invoice")
		return
	}

	// Insert invoice lines
	for i, line := range input.Lines {
		lineID := uuid.New()

		lineTotal := line.Quantity*line.UnitPrice - line.DiscountAmount

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

		if _, lineErr := tx.Exec(lineQuery,
			lineID, invoiceID, salesOrderLineID, i+1, productID, line.Description,
			line.Quantity, unitID, line.UnitPrice, line.DiscountAmount,
			taxID, lineTaxAmounts[i], lineTotal, accountID, now,
		); lineErr != nil {
			h.log.Error("Failed to create sales invoice line", "error", lineErr, "line", i+1)
			response.InternalError(c, "Failed to create sales invoice lines")
			return
		}
		if salesOrderLineID != nil {
			if _, qiErr := tx.Exec(
				"UPDATE sales_order_lines SET quantity_invoiced = quantity_invoiced + $1, updated_at = $2 WHERE id = $3",
				line.Quantity, now, *salesOrderLineID,
			); qiErr != nil {
				h.log.Error("CreateSalesInvoice: quantity_invoiced update failed", "error", qiErr)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit invoice")
		return
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
	if !h.salesOrgScopeOK(c, "sales_invoices", invoiceID, tenantID) {
		response.NotFound(c, "Sales invoice")
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
			   COALESCE(si.invoice_type, 'invoice') as invoice_type, si.original_invoice_id, si.reason,
			   si.payment_term_id, COALESCE(si.early_discount_amount, 0), si.early_discount_date,
			   ` + invoiceOverdueSQL("si") + ` AS is_overdue,
			   ` + invoiceDaysOverdueSQL("si") + ` AS days_overdue
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
	// A2: the detail carried neither field, so the sheet could not show the
	// overdue badge that the list row already drives.
	var isOverdue bool
	var daysOverdue int
	var getPaymentTermID sql.NullString
	var getEarlyDiscountAmount float64
	var getEarlyDiscountDate sql.NullTime

	err = h.db.QueryRow(query, invoiceID, tenantID).Scan(
		&id, &tenantIDScan, &organizationID, &invoiceNumber, &customerID, &salesOrderID,
		&invoiceDate, &dueDate, &billingAddress, &shippingAddress,
		&currencyID, &exchangeRate, &subtotal, &discountAmount,
		&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
		&reference, &poNumber, &notes, &termsConditions,
		&journalEntryID, &sentAt, &viewedAt, &createdBy, &createdAt, &updatedAt,
		&customerName,
		&invoiceType, &originalInvoiceID, &reason,
		&getPaymentTermID, &getEarlyDiscountAmount, &getEarlyDiscountDate,
		&isOverdue, &daysOverdue,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales invoice")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch sales invoice", "error", err)
		response.InternalError(c, "Failed to fetch sales invoice")
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
		"is_overdue":      isOverdue,
		"days_overdue":    daysOverdue,
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
	if getPaymentTermID.Valid {
		invoice["payment_term_id"] = getPaymentTermID.String
	}
	if getEarlyDiscountAmount > 0 {
		invoice["early_discount_amount"] = getEarlyDiscountAmount
	}
	if getEarlyDiscountDate.Valid {
		invoice["early_discount_date"] = getEarlyDiscountDate.Time.Format("2006-01-02")
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

	// Get payment allocations with payment details
	paQuery := `
		SELECT pa.id, pa.payment_id, pa.amount, p.payment_number, p.status, p.payment_date,
			   COALESCE(p.reference, '') as reference, COALESCE(j.name, '') as journal_name,
			   COALESCE(j.name_uz, '') as journal_name_uz, COALESCE(j.name_en, '') as journal_name_en
		FROM payment_allocations pa
		JOIN payments p ON p.id = pa.payment_id
		LEFT JOIN journals j ON p.journal_id = j.id
		WHERE pa.document_type = 'sales_invoice'
		  AND pa.document_id = $1
		  AND p.deleted_at IS NULL
		ORDER BY p.payment_date DESC`

	paRows, paErr := h.db.Query(paQuery, invoiceID)
	if paErr == nil {
		defer paRows.Close()
		var paymentAllocations []map[string]interface{}
		for paRows.Next() {
			var paID, paymentID uuid.UUID
			var paAmount float64
			var paymentNumber, pStatus, pReference, journalName, journalNameUz, journalNameEn string
			var paymentDate time.Time

			if err := paRows.Scan(&paID, &paymentID, &paAmount, &paymentNumber, &pStatus, &paymentDate, &pReference, &journalName, &journalNameUz, &journalNameEn); err != nil {
				continue
			}
			paymentAllocations = append(paymentAllocations, map[string]interface{}{
				"id":              paID.String(),
				"payment_id":      paymentID.String(),
				"amount":          paAmount,
				"payment_number":  paymentNumber,
				"status":          pStatus,
				"payment_date":    paymentDate.Format("2006-01-02"),
				"reference":       pReference,
				"journal_name":    journalName,
				"journal_name_uz": journalNameUz,
				"journal_name_en": journalNameEn,
			})
		}
		invoice["payment_allocations"] = paymentAllocations

		// Query exchange diffs linked to this invoice's payments
		if exchangeRate != 1 {
			var exchangeDiffs []map[string]interface{}
			edQuery := `
				SELECT ed.id, ed.amount_uzs, ed.diff_type, ed.period_start, ed.description,
				       COALESCE(ed.document_number, ''), COALESCE(ed.initial_rate, 0), COALESCE(ed.final_rate, 0), COALESCE(ed.foreign_amount, 0)
				FROM exchange_diffs ed
				WHERE ed.tenant_id = $1 AND ed.deleted_at IS NULL
				  AND ed.journal_entry_id IN (
				    SELECT p.journal_entry_id FROM payments p
				    JOIN payment_allocations pa ON pa.payment_id = p.id
				    WHERE pa.document_type = 'sales_invoice' AND pa.document_id = $2 AND p.deleted_at IS NULL
				  )
				ORDER BY ed.period_start DESC`
			edRows, edErr := h.db.Query(edQuery, tenantID, invoiceID)
			if edErr == nil {
				defer edRows.Close()
				for edRows.Next() {
					var edID uuid.UUID
					var edAmount, edInitialRate, edFinalRate, edForeignAmount float64
					var edType, edDesc, edDocNumber string
					var edDate time.Time
					if err := edRows.Scan(&edID, &edAmount, &edType, &edDate, &edDesc, &edDocNumber, &edInitialRate, &edFinalRate, &edForeignAmount); err != nil {
						continue
					}
					exchangeDiffs = append(exchangeDiffs, map[string]interface{}{
						"id":              edID.String(),
						"amount":          edAmount,
						"type":            edType,
						"date":            edDate.Format("2006-01-02"),
						"description":     edDesc,
						"document_number": edDocNumber,
						"initial_rate":    edInitialRate,
						"final_rate":      edFinalRate,
						"foreign_amount":  edForeignAmount,
					})
				}
			}
			if len(exchangeDiffs) > 0 {
				invoice["exchange_diffs"] = exchangeDiffs
			}
		}
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
	if !h.salesOrgScopeOK(c, "sales_invoices", invoiceID, tenantID) {
		response.NotFound(c, "Sales invoice")
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
	if input.Status != nil && *input.Status != currentStatus {
		// Manual status writes are restricted to cancellation. 'sent' must go through
		// POST /:id/send (which posts the AR journal entry), and 'partial'/'paid' are
		// derived from payments — a bare PUT must never fake either.
		if *input.Status != "cancelled" {
			response.BadRequest(c, "Only cancellation is allowed here; use /send to post the invoice, payments set partial/paid")
			return
		}
		if currentStatus != "draft" && currentStatus != "sent" {
			response.BadRequest(c, fmt.Sprintf("Cannot cancel an invoice in status '%s'", currentStatus))
			return
		}
		// A posted invoice keeps a live AR entry — it must be reversed with a credit
		// note, not silently cancelled.
		var jeID sql.NullString
		_ = h.db.QueryRow("SELECT journal_entry_id FROM sales_invoices WHERE id = $1 AND tenant_id = $2", invoiceID, tenantID).Scan(&jeID)
		if jeID.Valid && jeID.String != "" {
			response.BadRequest(c, "Invoice is posted to the ledger; create a credit note instead of cancelling")
			return
		}
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

	// Add WHERE clause params
	argCount++
	args = append(args, invoiceID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE sales_invoices SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update sales invoice", "error", err)
		response.InternalError(c, "Failed to update sales invoice")
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
	if !h.salesOrgScopeOK(c, "sales_invoices", invoiceID, tenantID) {
		response.NotFound(c, "Sales invoice")
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
	if !h.salesOrgScopeOK(c, "sales_invoices", invoiceID, tenantID) {
		response.NotFound(c, "Sales invoice")
		return
	}

	// Get invoice details
	var currentStatus, invoiceNumber string
	var customerID uuid.UUID
	var organizationID *uuid.UUID
	var salesOrderID, existingJEID sql.NullString
	var totalAmount, taxAmount, subtotal float64
	var invoiceDate time.Time
	err = h.db.QueryRow(`
		SELECT status, invoice_number, customer_id, organization_id, total_amount, tax_amount, subtotal, invoice_date, sales_order_id, journal_entry_id
		FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		invoiceID, tenantID,
	).Scan(&currentStatus, &invoiceNumber, &customerID, &organizationID, &totalAmount, &taxAmount, &subtotal, &invoiceDate, &salesOrderID, &existingJEID)
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
	// Double-post guard: an invoice that already carries a journal entry must never
	// post AR a second time, whatever its status claims.
	if existingJEID.Valid && existingJEID.String != "" {
		response.BadRequest(c, "ALREADY_POSTED: invoice already has a journal entry")
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
	var numberPrefix sql.NullString
	err = tx.QueryRow(`
		SELECT id, number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('SALES', 'SAL') AND deleted_at IS NULL`,
		tenantID,
	).Scan(&salesJournalID, &numberPrefix)
	if err != nil {
		h.log.Error("Sales journal not found", "tenant_id", tenantID)
		response.InternalError(c, "Sales journal not configured. Please create a journal with code SALES or SAL.")
		return
	}

	// Odoo-style: AR + per-category Income + COGS/Interim clearing
	// 1. Try contact's default receivable account
	arAccountID := getContactDefaultAccount(tx, customerID, "receivable", organizationID)
	// 2. Fallback to standard findAccount
	if arAccountID == uuid.Nil {
		arAccountID = findAccount(tx, tenantID, organizationID, "accounts receivable", "4010")
	}
	if arAccountID == uuid.Nil {
		h.log.Error("AR account not found", "tenant_id", tenantID)
		response.InternalError(c, "Accounts Receivable account (4010) not found. Please configure chart of accounts.")
		return
	}

	taxAccountID := findAccount(tx, tenantID, organizationID, "QQS bo'yicha qarz", "6420")

	// Get invoice lines for per-category accounting
	type invoiceLineAcct struct {
		ProductID   uuid.UUID
		LineTotal   float64
		Quantity    float64
		CostPrice   float64
		IncomeAcct  uuid.UUID
		ExpenseAcct uuid.UUID
		OutputAcct  uuid.UUID
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
		// Resolve accounts after closing rows: prefer inventory-type account for COGS credit, fallback to category
		for i := range invoiceLines {
			ca := getCategoryAccounts(tx, tenantID, organizationID, invoiceLines[i].ProductID)
			invoiceLines[i].IncomeAcct = ca.IncomeAccountID
			invoiceLines[i].ExpenseAcct = ca.ExpenseAccountID
			invAcct := getInventoryAccountByType(tx, tenantID, organizationID, invoiceLines[i].ProductID)
			if invAcct != uuid.Nil {
				invoiceLines[i].OutputAcct = invAcct
			} else {
				invoiceLines[i].OutputAcct = ca.StockOutputAccountID
			}
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

	// Resolve fallback revenue account for products without category income account
	fallbackRevenue := findAccount(tx, tenantID, organizationID, "sales revenue", "9010")

	// Check if a delivery stock operation already posted COGS for this sales order
	// to avoid double-counting COGS (once from stock operation, once from invoice)
	deliveryAlreadyPostedCOGS := false
	if salesOrderID.Valid {
		var cogsPosted int
		tx.QueryRow(`
			SELECT COUNT(*) FROM journal_entries je
			JOIN stock_operations so ON so.id = je.source_id
			WHERE je.source_type = 'stock_operation' AND je.status = 'posted' AND je.deleted_at IS NULL
			  AND so.source_type = 'sales_order' AND so.source_id = $1
			  AND so.direction = 'delivery' AND so.state = 'done'
		`, salesOrderID.String).Scan(&cogsPosted)
		deliveryAlreadyPostedCOGS = cogsPosted > 0
		if !deliveryAlreadyPostedCOGS {
			// Ombor v2: ValidateDeliveryOrder posts per-line COGS at shipment
			// (source_type='sales_delivery', DO-COGS-* keys) — skip too.
			var doCogs int
			tx.QueryRow(`
				SELECT COUNT(*) FROM journal_entries je
				WHERE je.source_type = 'sales_delivery' AND je.status = 'posted' AND je.deleted_at IS NULL
				  AND je.source_id IN (SELECT id FROM sales_delivery_orders WHERE sales_order_id = $1)
			`, salesOrderID.String).Scan(&doCogs)
			deliveryAlreadyPostedCOGS = doCogs > 0
		}
	}

	for _, il := range invoiceLines {
		if il.LineTotal > 0 {
			if il.IncomeAcct != uuid.Nil {
				revenueGrouped[il.IncomeAcct] += il.LineTotal
			} else if fallbackRevenue != uuid.Nil {
				revenueGrouped[fallbackRevenue] += il.LineTotal
			}
		}
		// Only post COGS if no delivery stock operation already did
		if !deliveryAlreadyPostedCOGS {
			costAmount := il.Quantity * il.CostPrice
			if costAmount > 0 && il.ExpenseAcct != uuid.Nil && il.OutputAcct != uuid.Nil {
				cogsGrouped[cogsPair{Expense: il.ExpenseAcct, Output: il.OutputAcct}] += costAmount
			}
		}
	}

	// Fallback if no lines at all but subtotal exists
	if len(revenueGrouped) == 0 && subtotal > 0 && fallbackRevenue != uuid.Nil {
		revenueGrouped[fallbackRevenue] = subtotal
	}

	// Generate entry number from actual max to avoid duplicate key conflicts.
	// Scoped to (tenant, org) to match journal_entries_tenant_org_entry_number_key.
	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}
	nextNumber := nextEntryNumberSeq(tx, tenantID, organizationID, prefix, 1)
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
	// Fetch customer name for richer description
	var custName string
	_ = tx.QueryRow(`SELECT COALESCE(name, '') FROM contacts WHERE id = $1`, customerID).Scan(&custName)
	description := fmt.Sprintf("Sales Invoice %s", invoiceNumber)
	if custName != "" {
		description = fmt.Sprintf("Sales Invoice %s — %s", invoiceNumber, custName)
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}
	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
		journalEntryID, tenantID, organizationID, salesJournalID, entryNumber, invoiceDate, invoiceNumber, description,
		"sales_invoice", invoiceID, 1.0, totalDebit, totalCredit, createdBy, now, now,
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

	// Notify: invoice sent
	go func() {
		var customerName string
		_ = h.db.QueryRow(`SELECT COALESCE(name, '') FROM contacts WHERE id = $1`, customerID).Scan(&customerName)
		amountStr := fmt.Sprintf("%.0f", totalAmount)
		h.createTranslatedNotification(tenantID, userID, "invoice_sent",
			map[string]interface{}{
				"invoice_id":     invoiceID.String(),
				"invoice_number": invoiceNumber,
				"customer_id":    customerID.String(),
				"customer_name":  customerName,
				"amount":         totalAmount,
			},
			invoiceNumber, customerName, amountStr,
		)
	}()

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
	if !h.salesOrgScopeOK(c, "sales_invoices", invoiceID, tenantID) {
		response.NotFound(c, "Sales invoice")
		return
	}

	var input struct {
		Amount         float64 `json:"amount" binding:"required,gt=0"`
		PaymentDate    string  `json:"payment_date" binding:"required"`
		PaymentMethod  string  `json:"payment_method,omitempty"`
		Reference      string  `json:"reference,omitempty"`
		Notes          string  `json:"notes,omitempty"`
		WriteOffAmount float64 `json:"write_off_amount,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse payment date
	paymentDate, err := time.Parse("2006-01-02", input.PaymentDate)
	if err != nil {
		response.BadRequest(c, "Invalid payment_date format, expected YYYY-MM-DD")
		return
	}

	// Get current invoice status and amounts (including early discount info)
	var currentStatus, invoiceNumber string
	var customerID uuid.UUID
	var organizationID *uuid.UUID
	var amountPaid, totalAmount float64
	var invEarlyDiscountAmount float64
	var invEarlyDiscountDate sql.NullTime
	err = h.db.QueryRow(`
		SELECT status, invoice_number, customer_id, organization_id, amount_paid, total_amount,
		       COALESCE(early_discount_amount, 0), early_discount_date
		FROM sales_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		invoiceID, tenantID,
	).Scan(&currentStatus, &invoiceNumber, &customerID, &organizationID, &amountPaid, &totalAmount,
		&invEarlyDiscountAmount, &invEarlyDiscountDate)
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

	// Apply early payment discount if payment is before the discount deadline
	earlyDiscountApplied := 0.0
	if invEarlyDiscountAmount > 0 && invEarlyDiscountDate.Valid && amountPaid == 0 {
		// Discount applies only if paying before or on the discount date and no prior payments
		if !paymentDate.After(invEarlyDiscountDate.Time) {
			earlyDiscountApplied = invEarlyDiscountAmount
			h.log.Info("RecordPayment: applying early payment discount",
				"invoice_id", invoiceID, "discount", earlyDiscountApplied,
				"discount_deadline", invEarlyDiscountDate.Time.Format("2006-01-02"),
				"payment_date", input.PaymentDate)
		}
	}

	amountDue := totalAmount - amountPaid
	// With early discount, the effective amount needed to fully pay is reduced
	effectiveAmountDue := amountDue - earlyDiscountApplied
	if effectiveAmountDue < 0 {
		effectiveAmountDue = 0
	}

	if input.Amount+input.WriteOffAmount > amountDue+0.01 {
		response.BadRequest(c, fmt.Sprintf("Payment + write-off exceeds amount due (%.2f)", amountDue))
		return
	}

	// Total credited: payment + write-off + early discount
	totalCredited := input.Amount + input.WriteOffAmount + earlyDiscountApplied
	newAmountPaid := amountPaid + totalCredited
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

	// Get Cash Receipts Journal ID — prefer org-specific, then tenant-wide
	var cashJournalID uuid.UUID
	var numberPrefix sql.NullString
	if organizationID != nil {
		_ = tx.QueryRow(`
			SELECT id, number_prefix
			FROM journals WHERE tenant_id = $1 AND organization_id = $2 AND code IN ('CASH_RECEIPTS', 'CASH') AND deleted_at IS NULL
			ORDER BY CASE WHEN code = 'CASH_RECEIPTS' THEN 0 ELSE 1 END LIMIT 1`,
			tenantID, *organizationID,
		).Scan(&cashJournalID, &numberPrefix)
	}
	if cashJournalID == uuid.Nil {
		_ = tx.QueryRow(`
			SELECT id, number_prefix
			FROM journals WHERE tenant_id = $1 AND code IN ('CASH_RECEIPTS', 'CASH') AND deleted_at IS NULL
			ORDER BY CASE WHEN code = 'CASH_RECEIPTS' THEN 0 ELSE 1 END LIMIT 1`,
			tenantID,
		).Scan(&cashJournalID, &numberPrefix)
	}

	// Derive next number from actual max across ALL journals for this tenant+org
	// to avoid duplicate key on unique constraint (tenant_id, organization_id, entry_number)
	var nextNumber int
	if cashJournalID != uuid.Nil {
		prefix := ""
		if numberPrefix.Valid {
			prefix = numberPrefix.String
		}
		if prefix != "" {
			if organizationID != nil {
				_ = tx.QueryRow(
					"SELECT COALESCE(MAX(CAST(NULLIF(REGEXP_REPLACE(entry_number, '[^0-9]', '', 'g'), '') AS BIGINT)), 0) + 1 FROM journal_entries WHERE tenant_id = $1 AND organization_id = $2 AND entry_number LIKE $3 AND deleted_at IS NULL",
					tenantID, *organizationID, prefix+"%",
				).Scan(&nextNumber)
			} else {
				_ = tx.QueryRow(
					"SELECT COALESCE(MAX(CAST(NULLIF(REGEXP_REPLACE(entry_number, '[^0-9]', '', 'g'), '') AS BIGINT)), 0) + 1 FROM journal_entries WHERE tenant_id = $1 AND organization_id IS NULL AND entry_number LIKE $2 AND deleted_at IS NULL",
					tenantID, prefix+"%",
				).Scan(&nextNumber)
			}
		} else {
			if organizationID != nil {
				_ = tx.QueryRow(
					"SELECT COALESCE(MAX(CAST(NULLIF(REGEXP_REPLACE(entry_number, '[^0-9]', '', 'g'), '') AS BIGINT)), 0) + 1 FROM journal_entries WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL",
					tenantID, *organizationID,
				).Scan(&nextNumber)
			} else {
				_ = tx.QueryRow(
					"SELECT COALESCE(MAX(CAST(NULLIF(REGEXP_REPLACE(entry_number, '[^0-9]', '', 'g'), '') AS BIGINT)), 0) + 1 FROM journal_entries WHERE tenant_id = $1 AND organization_id IS NULL AND deleted_at IS NULL",
					tenantID,
				).Scan(&nextNumber)
			}
		}
		if nextNumber < 1 {
			nextNumber = 1
		}
	}

	// Get default account IDs — lookup by name first, then code fallback
	arAccountID := findAccount(tx, tenantID, organizationID, "accounts receivable", "4010")
	// Post directly to Cash/Bank based on payment method
	var cashAccountID uuid.UUID
	if input.PaymentMethod == "cash" {
		cashAccountID = findAccount(tx, tenantID, organizationID, "cash", "5010")
		if cashAccountID == uuid.Nil {
			cashAccountID = findAccount(tx, tenantID, organizationID, "bank account", "5110")
		}
	} else {
		cashAccountID = findAccount(tx, tenantID, organizationID, "bank account", "5110")
		if cashAccountID == uuid.Nil {
			cashAccountID = findAccount(tx, tenantID, organizationID, "cash", "5010")
		}
	}

	// A payment with no ledger entry is money that exists in Savdo but not in Moliya —
	// refuse instead of silently skipping the GL block (audit §3a).
	if cashJournalID == uuid.Nil || arAccountID == uuid.Nil || cashAccountID == uuid.Nil {
		response.BadRequest(c, "Payment accounts not configured (cash/bank journal, AR 4010, cash 5010 / bank 5110) — payment not recorded")
		return
	}
	{
		// Generate entry number
		prefix := ""
		if numberPrefix.Valid {
			prefix = numberPrefix.String
		}
		entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

		// Create journal entry
		journalEntryID := uuid.New()
		description := fmt.Sprintf("%s uchun to'lov qabul qilindi", invoiceNumber)
		reference := input.Reference
		if reference == "" {
			reference = GeneratePaymentReference("sales_invoice", invoiceNumber, "")
		}

		// Use savepoint so a GL failure doesn't abort the whole transaction
		tx.Exec("SAVEPOINT gl_posting")

		var glErr error
		var paymentCreatedBy *uuid.UUID
		if userID != uuid.Nil {
			paymentCreatedBy = &userID
		}
		jeTotal := input.Amount + input.WriteOffAmount + earlyDiscountApplied
		_, glErr = tx.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
			journalEntryID, tenantID, organizationID, cashJournalID, entryNumber, paymentDate, reference, description,
			"payment_receipt", invoiceID, 1.0, jeTotal, jeTotal, "posted", paymentCreatedBy, now, now,
		)

		if glErr == nil {
			// Line 1: Debit Cash/Bank (no contact_id — cash line is not partner-specific)
			cashLineID := uuid.New()
			_, glErr = tx.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				cashLineID, journalEntryID, 1, cashAccountID, "Outstanding Receipt",
				input.Amount, 0.0, 1.0, now,
			)
		}

		if glErr == nil {
			// Line 2: Credit AR (payment + write-off + early discount reduces AR)
			arCreditAmount := input.Amount + input.WriteOffAmount + earlyDiscountApplied
			arLineID := uuid.New()
			_, glErr = tx.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, contact_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				arLineID, journalEntryID, 2, arAccountID, customerID, "Accounts Receivable",
				0.0, arCreditAmount, 1.0, now,
			)
		}

		lineNumber := 3
		// Line 3: Write-off (DR Write-off Expense, already credited AR above)
		if glErr == nil && input.WriteOffAmount > 0 {
			writeOffAccountID := findAccount(tx, tenantID, organizationID, "payment difference write-off", "9690")
			if writeOffAccountID == uuid.Nil {
				writeOffAccountID = findAccount(tx, tenantID, organizationID, "miscellaneous expense", "9410")
			}
			// AR is credited the write-off above — a missing debit leg would leave the
			// entry unbalanced, so the account is mandatory (audit §3c).
			if writeOffAccountID == uuid.Nil {
				glErr = fmt.Errorf("write-off account not found (9690/9410)")
			}
			if writeOffAccountID != uuid.Nil {
				writeOffLineID := uuid.New()
				_, glErr = tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					writeOffLineID, journalEntryID, lineNumber, writeOffAccountID, "Payment Difference Write-off",
					input.WriteOffAmount, 0.0, 1.0, now,
				)
				lineNumber++
				if glErr == nil {
					_, glErr = tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", input.WriteOffAmount, now, writeOffAccountID)
				}
			}
		}

		// Line for early payment discount (DR Sales Discount)
		if glErr == nil && earlyDiscountApplied > 0 {
			discountAccountID := findAccount(tx, tenantID, organizationID, "sales discount", "9310")
			if discountAccountID == uuid.Nil {
				discountAccountID = findAccount(tx, tenantID, organizationID, "cash discount", "9310")
			}
			if discountAccountID == uuid.Nil {
				discountAccountID = findAccount(tx, tenantID, organizationID, "discount", "9310")
			}
			if discountAccountID == uuid.Nil {
				glErr = fmt.Errorf("early-payment discount account not found (9310)")
			}
			if discountAccountID != uuid.Nil {
				discountLineID := uuid.New()
				_, glErr = tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					discountLineID, journalEntryID, lineNumber, discountAccountID, "Early Payment Discount",
					earlyDiscountApplied, 0.0, 1.0, now,
				)
				lineNumber++
				if glErr == nil {
					_, glErr = tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", earlyDiscountApplied, now, discountAccountID)
				}
			}
		}

		if glErr == nil {
			// Update account balances
			arCreditAmount := input.Amount + input.WriteOffAmount + earlyDiscountApplied
			_, glErr = tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", input.Amount, now, cashAccountID)
			if glErr == nil {
				_, glErr = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", arCreditAmount, now, arAccountID)
			}
		}

		if glErr == nil {
			// Create payment record
			paymentID := uuid.New()
			paymentRef := input.Reference
			if paymentRef == "" {
				paymentRef = GeneratePaymentReference("sales_invoice", invoiceNumber, "")
			}
			_, glErr = tx.Exec(`
				INSERT INTO payments (
					id, tenant_id, organization_id, type, payment_number, contact_id, payment_date, amount,
					currency_id, exchange_rate, reference, notes, status, journal_entry_id,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
				paymentID, tenantID, organizationID, "receipt", fmt.Sprintf("REC-%s", entryNumber), customerID, paymentDate, input.Amount,
				nil, 1.0, paymentRef, input.Notes, "confirmed", journalEntryID,
				userID, now, now,
			)

			if glErr == nil {
				// Create payment allocation
				allocationID := uuid.New()
				_, glErr = tx.Exec(`
					INSERT INTO payment_allocations (
						id, payment_id, document_type, document_id, amount, created_at
					) VALUES ($1, $2, $3, $4, $5, $6)`,
					allocationID, paymentID, "sales_invoice", invoiceID, input.Amount, now,
				)
			}
		}

		// A GL failure fails the whole payment: previously this rolled back to the
		// savepoint and still marked the invoice paid (audit §3b).
		if glErr != nil {
			h.log.Error("GL posting failed in RecordPayment — payment rejected", "error", glErr, "invoice_id", invoiceID)
			response.InternalError(c, "Payment ledger posting failed — payment not recorded")
			return
		}
		tx.Exec("RELEASE SAVEPOINT gl_posting")
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

	// Update the linked order's payment_status and paid_amount from the aggregate of
	// ALL its invoices (previously: status from this one invoice, paid_amount never).
	var salesOrderID sql.NullString
	tx.QueryRow(`SELECT sales_order_id FROM sales_invoices WHERE id = $1`, invoiceID).Scan(&salesOrderID)
	if salesOrderID.Valid && salesOrderID.String != "" {
		var orderTotal, orderPaid float64
		aggErr := tx.QueryRow(`
			SELECT so.total_amount,
			       COALESCE((SELECT SUM(si.amount_paid) FROM sales_invoices si
			                 WHERE si.sales_order_id = so.id AND si.deleted_at IS NULL
			                   AND si.status NOT IN ('cancelled','void')), 0)
			FROM sales_orders so WHERE so.id = $1 AND so.tenant_id = $2`,
			salesOrderID.String, tenantID,
		).Scan(&orderTotal, &orderPaid)
		if aggErr == nil {
			orderPaymentStatus := "unpaid"
			if orderPaid >= orderTotal-0.01 && orderTotal > 0 {
				orderPaymentStatus = "paid"
			} else if orderPaid > 0 {
				orderPaymentStatus = "partial"
			}
			tx.Exec(
				"UPDATE sales_orders SET payment_status = $1, paid_amount = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
				orderPaymentStatus, orderPaid, now, salesOrderID.String, tenantID,
			)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	// Notify: payment recorded on invoice
	go func() {
		var customerName string
		_ = h.db.QueryRow(`SELECT COALESCE(name, '') FROM contacts WHERE id = $1`, customerID).Scan(&customerName)
		amountStr := fmt.Sprintf("%.0f", input.Amount)

		h.EmitWorkflowEvent(tenantID, "payment.received", map[string]interface{}{
			"record_id":      invoiceID.String(),
			"invoice_number": invoiceNumber,
			"customer_name":  customerName,
			"amount":         input.Amount,
		})
		// `customer_name` added to data so the web renderer (notificationCatalog.js)
		// can rebuild the body in the current UI language. Additive — mobile-safe.
		h.createTranslatedNotification(tenantID, userID, "payment_recorded",
			map[string]interface{}{
				"invoice_id":     invoiceID.String(),
				"invoice_number": invoiceNumber,
				"customer_id":    customerID.String(),
				"customer_name":  customerName,
				"amount":         input.Amount,
			},
			amountStr, invoiceNumber, customerName,
		)
	}()

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
	if !h.salesOrgScopeOK(c, "sales_invoices", invoiceID, tenantID) {
		response.NotFound(c, "Sales invoice")
		return
	}

	var input entity.CreateCreditNoteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		h.log.Error("Failed to create credit note", "error", err)
		response.InternalError(c, "Failed to create credit note")
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

	// Notify: credit note created
	go func() {
		var customerName string
		_ = h.db.QueryRow(`SELECT COALESCE(name, '') FROM contacts WHERE id = $1`, customerID).Scan(&customerName)
		amountStr := fmt.Sprintf("%.0f", cnTotalAmount)
		// customer_name added for web re-render; additive and mobile-safe.
		h.createTranslatedNotification(tenantID, userID, "credit_note_created",
			map[string]interface{}{
				"credit_note_id":     creditNoteID.String(),
				"credit_note_number": cnNumber,
				"invoice_number":     orgInvoiceNumber,
				"customer_id":        customerID.String(),
				"customer_name":      customerName,
				"amount":             cnTotalAmount,
			},
			cnNumber, orgInvoiceNumber, amountStr,
		)
	}()

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
	if !h.salesOrgScopeOK(c, "sales_invoices", creditNoteID, tenantID) {
		response.NotFound(c, "Sales invoice")
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
	var numberPrefix sql.NullString
	err = tx.QueryRow(`
		SELECT id, number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('SALES', 'SAL') AND deleted_at IS NULL`,
		tenantID,
	).Scan(&salesJournalID, &numberPrefix)

	if err == nil {
		arAccountID := findAccount(tx, tenantID, organizationID, "accounts receivable", "4010")
		revenueAccountID := findAccount(tx, tenantID, organizationID, "sales revenue", "9010")
		taxAccountID := findAccount(tx, tenantID, organizationID, "QQS bo'yicha qarz", "6420")

		if arAccountID != uuid.Nil {
			prefix := ""
			if numberPrefix.Valid {
				prefix = numberPrefix.String
			}
			// Scoped to (tenant, org) to match journal_entries_tenant_org_entry_number_key.
			nextNumber := nextEntryNumberSeq(tx, tenantID, organizationID, prefix, 1)
			entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

			journalEntryID := uuid.New()
			description := fmt.Sprintf("Credit Note %s", cnNumber)

			var cnCreatedBy *uuid.UUID
			if userID != uuid.Nil {
				cnCreatedBy = &userID
			}
			_, err = tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
				journalEntryID, tenantID, organizationID, salesJournalID, entryNumber, cnDate, cnNumber, description,
				"credit_note", creditNoteID.String(), 1.0, totalAmount, totalAmount, cnCreatedBy, now, now,
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

// RepairRevenueJournalEntries creates missing journal entries for invoices that
// don't have them, and fixes existing entries that are missing revenue lines.
func (h *Handler) RepairRevenueJournalEntries(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var organizationID *uuid.UUID
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		organizationID = &orgID
	}

	now := time.Now()
	var created, repaired int
	var details []map[string]interface{}

	// ===== PART 1: Create journal entries for invoices that have none =====
	missingRows, err := h.db.Query(`
		SELECT si.id, si.invoice_number, si.total_amount, si.subtotal, si.tax_amount,
			   si.customer_id, si.organization_id, si.invoice_date, si.sales_order_id
		FROM sales_invoices si
		WHERE si.tenant_id = $1
			AND si.journal_entry_id IS NULL
			AND si.invoice_number NOT LIKE 'CN-%'
			AND si.status IN ('sent', 'paid', 'partial', 'partially_paid')
			AND si.deleted_at IS NULL
		ORDER BY si.created_at
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to query invoices", "error", err)
		response.InternalError(c, "Failed to query invoices")
		return
	}
	defer missingRows.Close()

	// Get Sales Journal
	var salesJournalID uuid.UUID
	var numberPrefix sql.NullString
	_ = h.db.QueryRow(`
		SELECT id, number_prefix FROM journals
		WHERE tenant_id = $1 AND code IN ('SALES', 'SAL') AND deleted_at IS NULL`,
		tenantID,
	).Scan(&salesJournalID, &numberPrefix)

	if salesJournalID == uuid.Nil {
		response.InternalError(c, "Sales journal not found")
		return
	}

	type missingInvoice struct {
		ID             uuid.UUID
		InvoiceNumber  string
		TotalAmount    float64
		Subtotal       float64
		TaxAmount      float64
		CustomerID     uuid.UUID
		OrganizationID uuid.UUID
		InvoiceDate    time.Time
		SalesOrderID   sql.NullString
	}

	var missing []missingInvoice
	for missingRows.Next() {
		var mi missingInvoice
		var invoiceDate sql.NullTime
		if err := missingRows.Scan(&mi.ID, &mi.InvoiceNumber, &mi.TotalAmount, &mi.Subtotal,
			&mi.TaxAmount, &mi.CustomerID, &mi.OrganizationID, &invoiceDate, &mi.SalesOrderID); err != nil {
			continue
		}
		if invoiceDate.Valid {
			mi.InvoiceDate = invoiceDate.Time
		} else {
			mi.InvoiceDate = now
		}
		missing = append(missing, mi)
	}

	for _, mi := range missing {
		orgID := mi.OrganizationID
		var orgPtr *uuid.UUID
		if orgID != uuid.Nil {
			orgPtr = &orgID
		}

		arAccountID := findAccount(h.db, tenantID, orgPtr, "accounts receivable", "4010")
		taxAccountID := findAccount(h.db, tenantID, orgPtr, "QQS bo'yicha qarz", "6420")
		revenueAccountID := findAccount(h.db, tenantID, orgPtr, "sales revenue", "9010")

		if arAccountID == uuid.Nil || revenueAccountID == uuid.Nil {
			continue
		}

		// Get invoice lines for COGS
		type lineAcct struct {
			ProductID   uuid.UUID
			LineTotal   float64
			Quantity    float64
			CostPrice   float64
			IncomeAcct  uuid.UUID
			ExpenseAcct uuid.UUID
			OutputAcct  uuid.UUID
		}
		var acctLines []lineAcct
		lineRows, err := h.db.Query(`
			SELECT sil.product_id, COALESCE(sil.line_total, 0), COALESCE(sil.quantity, 0), COALESCE(p.cost_price, 0)
			FROM sales_invoice_lines sil
			JOIN products p ON sil.product_id = p.id
			WHERE sil.sales_invoice_id = $1
		`, mi.ID)
		if err == nil {
			for lineRows.Next() {
				var al lineAcct
				if err := lineRows.Scan(&al.ProductID, &al.LineTotal, &al.Quantity, &al.CostPrice); err == nil {
					acctLines = append(acctLines, al)
				}
			}
			lineRows.Close()
			for i := range acctLines {
				ca := getCategoryAccounts(h.db, tenantID, orgPtr, acctLines[i].ProductID)
				acctLines[i].IncomeAcct = ca.IncomeAccountID
				acctLines[i].ExpenseAcct = ca.ExpenseAccountID
				invAcct := getInventoryAccountByType(h.db, tenantID, orgPtr, acctLines[i].ProductID)
				if invAcct != uuid.Nil {
					acctLines[i].OutputAcct = invAcct
				} else {
					acctLines[i].OutputAcct = ca.StockOutputAccountID
				}
			}
		}

		// Group revenue by income account
		revenueGrouped := make(map[uuid.UUID]float64)
		type cogsPair struct {
			Expense uuid.UUID
			Output  uuid.UUID
		}
		cogsGrouped := make(map[cogsPair]float64)

		// Check if a delivery stock operation already posted COGS for this sales order
		// to avoid double-counting COGS (once from stock operation, once from invoice) — same as SendInvoice
		deliveryAlreadyPostedCOGS := false
		if mi.SalesOrderID.Valid {
			var cogsPosted int
			h.db.QueryRow(`
				SELECT COUNT(*) FROM journal_entries je
				JOIN stock_operations so ON so.id = je.source_id
				WHERE je.source_type = 'stock_operation' AND je.status = 'posted' AND je.deleted_at IS NULL
				  AND so.source_type = 'sales_order' AND so.source_id = $1
				  AND so.direction = 'delivery' AND so.state = 'done'
			`, mi.SalesOrderID.String).Scan(&cogsPosted)
			deliveryAlreadyPostedCOGS = cogsPosted > 0
			if !deliveryAlreadyPostedCOGS {
				// Ombor v2: shipment-time COGS from ValidateDeliveryOrder
				var doCogs int
				h.db.QueryRow(`
					SELECT COUNT(*) FROM journal_entries je
					WHERE je.source_type = 'sales_delivery' AND je.status = 'posted' AND je.deleted_at IS NULL
					  AND je.source_id IN (SELECT id FROM sales_delivery_orders WHERE sales_order_id = $1)
				`, mi.SalesOrderID.String).Scan(&doCogs)
				deliveryAlreadyPostedCOGS = doCogs > 0
			}
		}

		for _, al := range acctLines {
			if al.LineTotal > 0 {
				if al.IncomeAcct != uuid.Nil {
					revenueGrouped[al.IncomeAcct] += al.LineTotal
				} else {
					revenueGrouped[revenueAccountID] += al.LineTotal
				}
			}
			// Only post COGS if no delivery stock operation already did
			if !deliveryAlreadyPostedCOGS {
				costAmount := al.Quantity * al.CostPrice
				if costAmount > 0 && al.ExpenseAcct != uuid.Nil && al.OutputAcct != uuid.Nil {
					cogsGrouped[cogsPair{Expense: al.ExpenseAcct, Output: al.OutputAcct}] += costAmount
				}
			}
		}

		subtotal := mi.Subtotal
		if len(revenueGrouped) == 0 && subtotal > 0 {
			revenueGrouped[revenueAccountID] = subtotal
		}

		taxAmount := mi.TaxAmount
		totalAmount := mi.TotalAmount

		var totalCogs float64
		for _, amt := range cogsGrouped {
			totalCogs += amt
		}
		totalDebit := totalAmount + totalCogs
		totalCredit := totalDebit

		tx, err := h.db.Begin()
		if err != nil {
			continue
		}

		// Generate entry number — scoped to (tenant, org) to match
		// journal_entries_tenant_org_entry_number_key.
		prefix := ""
		if numberPrefix.Valid {
			prefix = numberPrefix.String
		}
		nextNumber := nextEntryNumberSeq(tx, tenantID, mi.OrganizationID, prefix, 1)
		entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

		jeID := uuid.New()
		description := fmt.Sprintf("Sales Invoice %s (repair)", mi.InvoiceNumber)

		_, err = tx.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15)`,
			jeID, tenantID, mi.OrganizationID, salesJournalID, entryNumber, mi.InvoiceDate, mi.InvoiceNumber, description,
			"sales_invoice", mi.ID, 1.0, totalDebit, totalCredit, now, now,
		)
		if err != nil {
			tx.Rollback()
			h.log.Error("RepairRevenue: failed to create JE", "error", err, "invoice", mi.InvoiceNumber)
			continue
		}

		lineNumber := 1

		// Debit: AR
		tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, contact_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			uuid.New(), jeID, lineNumber, arAccountID, mi.CustomerID, "Accounts Receivable",
			totalAmount, 0.0, 1.0, now,
		)
		tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalAmount, now, arAccountID)
		lineNumber++

		// Credit: Revenue
		for incomeAcct, amount := range revenueGrouped {
			tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), jeID, lineNumber, incomeAcct, "Sales Revenue",
				0.0, amount, 1.0, now,
			)
			tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", amount, now, incomeAcct)
			lineNumber++
		}

		// Credit: Tax
		if taxAccountID != uuid.Nil && taxAmount > 0 {
			tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), jeID, lineNumber, taxAccountID, "Sales Tax Payable",
				0.0, taxAmount, 1.0, now,
			)
			tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", taxAmount, now, taxAccountID)
			lineNumber++
		}

		// COGS entries
		for pair, costAmount := range cogsGrouped {
			tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), jeID, lineNumber, pair.Expense, "Cost of Goods Sold",
				costAmount, 0.0, 1.0, now,
			)
			tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", costAmount, now, pair.Expense)
			lineNumber++

			tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), jeID, lineNumber, pair.Output, "Stock Interim Delivery",
				0.0, costAmount, 1.0, now,
			)
			tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", costAmount, now, pair.Output)
			lineNumber++
		}

		// Update invoice with journal entry ID
		tx.Exec("UPDATE sales_invoices SET journal_entry_id = $1, updated_at = $2 WHERE id = $3", jeID, now, mi.ID)

		if err := tx.Commit(); err != nil {
			continue
		}

		created++
		details = append(details, map[string]interface{}{
			"type":           "created",
			"invoice_number": mi.InvoiceNumber,
			"entry_number":   entryNumber,
			"total_amount":   totalAmount,
			"revenue":        subtotal,
		})
	}

	// ===== PART 2: Fix existing entries with missing revenue lines =====
	brokenRows, err := h.db.Query(`
		SELECT je.id, je.source_id, je.entry_number,
			COALESCE(SUM(CASE WHEN jel.description = 'Accounts Receivable' THEN jel.debit_amount ELSE 0 END), 0) as ar_debit,
			COALESCE(SUM(CASE WHEN jel.description = 'Sales Revenue' THEN jel.credit_amount ELSE 0 END), 0) as revenue_credit,
			COALESCE(SUM(CASE WHEN jel.description = 'Sales Tax Payable' THEN jel.credit_amount ELSE 0 END), 0) as tax_credit
		FROM journal_entries je
		JOIN journal_entry_lines jel ON jel.journal_entry_id = je.id
		WHERE je.tenant_id = $1
			AND je.source_type = 'sales_invoice'
			AND je.status = 'posted'
			AND je.deleted_at IS NULL
		GROUP BY je.id, je.source_id, je.entry_number
		HAVING COALESCE(SUM(CASE WHEN jel.description = 'Sales Revenue' THEN jel.credit_amount ELSE 0 END), 0) <
			   COALESCE(SUM(CASE WHEN jel.description = 'Accounts Receivable' THEN jel.debit_amount ELSE 0 END), 0) -
			   COALESCE(SUM(CASE WHEN jel.description = 'Sales Tax Payable' THEN jel.credit_amount ELSE 0 END), 0) - 1
		ORDER BY je.entry_number
	`, tenantID)
	if err == nil {
		defer brokenRows.Close()
		for brokenRows.Next() {
			var jeID, invoiceID uuid.UUID
			var entryNumber string
			var arDebit, revCredit, taxCredit float64
			if err := brokenRows.Scan(&jeID, &invoiceID, &entryNumber, &arDebit, &revCredit, &taxCredit); err != nil {
				continue
			}
			missingRevenue := arDebit - taxCredit - revCredit
			if missingRevenue <= 0 {
				continue
			}

			revenueAccountID := findAccount(h.db, tenantID, organizationID, "sales revenue", "9010")
			if revenueAccountID == uuid.Nil {
				continue
			}

			tx, err := h.db.Begin()
			if err != nil {
				continue
			}
			var maxLine int
			_ = tx.QueryRow("SELECT COALESCE(MAX(line_number), 0) FROM journal_entry_lines WHERE journal_entry_id = $1", jeID).Scan(&maxLine)
			_, err = tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				uuid.New(), jeID, maxLine+1, revenueAccountID, "Sales Revenue",
				0.0, missingRevenue, 1.0, now,
			)
			if err != nil {
				tx.Rollback()
				continue
			}
			tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", missingRevenue, now, revenueAccountID)
			if err := tx.Commit(); err != nil {
				continue
			}
			repaired++
			details = append(details, map[string]interface{}{
				"type":            "repaired",
				"entry_number":    entryNumber,
				"missing_revenue": missingRevenue,
			})
		}
	}

	response.Success(c, gin.H{
		"message":  fmt.Sprintf("Created %d new journal entries, repaired %d existing entries", created, repaired),
		"created":  created,
		"repaired": repaired,
		"details":  details,
	})
}
