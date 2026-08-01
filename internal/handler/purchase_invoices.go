package handler

import (
	"database/sql"
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

// ListPurchaseInvoices returns paginated list of purchase invoices (vendor bills)
func (h *Handler) ListPurchaseInvoices(c *gin.Context) {
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
		SELECT pi.id, pi.tenant_id, pi.invoice_number, pi.vendor_id, pi.vendor_invoice_number,
			   pi.invoice_date, pi.due_date, pi.subtotal, pi.discount_amount,
			   pi.tax_rate_id, pi.tax_amount, pi.total_amount, pi.amount_paid, pi.amount_due, pi.status,
			   pi.three_way_match_status, pi.notes, pi.created_at, pi.updated_at,
			   c.name as vendor_name,
			   pi.purchase_order_id, po.order_number as po_number,
			   pi.goods_receipt_id, gr.gr_number as gr_number,
			   COALESCE(pi.invoice_type, 'invoice') as invoice_type, pi.original_invoice_id, pi.reason
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		LEFT JOIN purchase_orders po ON pi.purchase_order_id = po.id
		LEFT JOIN goods_receipts gr ON pi.goods_receipt_id = gr.id
		WHERE pi.tenant_id = $1 AND pi.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM purchase_invoices WHERE tenant_id = $1 AND deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND pi.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pi.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by vendor_id
	if vendorID := c.Query("vendor_id"); vendorID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pi.vendor_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND vendor_id = $%d", argCount)
		args = append(args, vendorID)
	}

	// Filter by date range
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pi.invoice_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND invoice_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pi.invoice_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND invoice_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Filter overdue invoices
	if overdue := c.Query("overdue"); overdue == "true" {
		baseQuery += " AND pi.due_date < CURRENT_DATE AND pi.status NOT IN ('paid', 'cancelled')"
		countQuery += " AND due_date < CURRENT_DATE AND status NOT IN ('paid', 'cancelled')"
	}

	// Filter by invoice_type (invoice or debit_note)
	if invoiceType := c.Query("invoice_type"); invoiceType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND COALESCE(pi.invoice_type, 'invoice') = $%d", argCount)
		countQuery += fmt.Sprintf(" AND COALESCE(invoice_type, 'invoice') = $%d", argCount)
		args = append(args, invoiceType)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(pi.invoice_number) LIKE $%d OR LOWER(pi.vendor_invoice_number) LIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(invoice_number) LIKE $%d OR LOWER(vendor_invoice_number) LIKE $%d)", argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count purchase invoices")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY pi.created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to fetch purchase invoices", "error", err)
		response.InternalError(c, "Failed to fetch purchase invoices")
		return
	}
	defer rows.Close()

	var invoices []map[string]interface{}
	today := time.Now().Truncate(24 * time.Hour)
	for rows.Next() {
		var id, tenantIDScan, vendorID uuid.UUID
		var invoiceNumber, status, threeWayMatchStatus string
		var vendorInvoiceNumber, notes, vendorName sql.NullString
		var invoiceDate, dueDate time.Time
		var subtotal, discountAmount, taxAmount, totalAmount, amountPaid, amountDue float64
		var createdAt, updatedAt time.Time
		var taxRateIDStr sql.NullString
		var purchaseOrderID, goodsReceiptID sql.NullString
		var poNumber, grNumber sql.NullString
		var invoiceType string
		var originalInvoiceID, reason sql.NullString

		err := rows.Scan(
			&id, &tenantIDScan, &invoiceNumber, &vendorID, &vendorInvoiceNumber,
			&invoiceDate, &dueDate, &subtotal, &discountAmount,
			&taxRateIDStr, &taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
			&threeWayMatchStatus, &notes, &createdAt, &updatedAt,
			&vendorName,
			&purchaseOrderID, &poNumber,
			&goodsReceiptID, &grNumber,
			&invoiceType, &originalInvoiceID, &reason,
		)
		if err != nil {
			continue
		}

		// Compute is_overdue: due_date < today AND status not paid/cancelled
		isOverdue := false
		if !dueDate.IsZero() && dueDate.Before(today) && status != "paid" && status != "cancelled" {
			isOverdue = true
		}

		// amount_residual = total_amount - amount_paid (remaining unpaid)
		amountResidual := totalAmount - amountPaid
		if amountResidual < 0 {
			amountResidual = 0
		}

		invoice := map[string]interface{}{
			"id":                    id.String(),
			"tenant_id":             tenantIDScan.String(),
			"invoice_number":        invoiceNumber,
			"vendor_id":             vendorID.String(),
			"partner_id":            vendorID.String(), // Alias for frontend compatibility
			"invoice_date":          invoiceDate.Format("2006-01-02"),
			"due_date":              dueDate.Format("2006-01-02"),
			"subtotal":              subtotal,
			"discount_amount":       discountAmount,
			"tax_amount":            taxAmount,
			"total_amount":          totalAmount,
			"amount_paid":           amountPaid,
			"amount_due":            amountDue,
			"amount_residual":       amountResidual,
			"is_overdue":            isOverdue,
			"status":                status,
			"three_way_match_status": threeWayMatchStatus,
			"invoice_type":          invoiceType,
			"created_at":            createdAt,
			"updated_at":            updatedAt,
		}

		if taxRateIDStr.Valid {
			invoice["tax_rate_id"] = taxRateIDStr.String
		}
		if vendorInvoiceNumber.Valid {
			invoice["vendor_invoice_number"] = vendorInvoiceNumber.String
		}
		if notes.Valid {
			invoice["notes"] = notes.String
		}
		if vendorName.Valid {
			invoice["vendor_name"] = vendorName.String
			invoice["partner_name"] = vendorName.String // Alias for frontend
		}
		if purchaseOrderID.Valid {
			invoice["purchase_order_id"] = purchaseOrderID.String
		}
		if poNumber.Valid {
			invoice["purchase_order_number"] = poNumber.String
		}
		if goodsReceiptID.Valid {
			invoice["goods_receipt_id"] = goodsReceiptID.String
		}
		if grNumber.Valid {
			invoice["goods_receipt_number"] = grNumber.String
		}
		if originalInvoiceID.Valid {
			invoice["original_invoice_id"] = originalInvoiceID.String
		}
		if reason.Valid {
			invoice["reason"] = reason.String
		}

		invoices = append(invoices, invoice)
	}

	response.Paginated(c, invoices, page, pageSize, total)
}

// CreatePurchaseInvoice creates a new purchase invoice (vendor bill)
func (h *Handler) CreatePurchaseInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input struct {
		VendorID            string  `json:"vendor_id" binding:"required"`
		PartnerID           string  `json:"partner_id"` // Alias for vendor_id
		OrganizationID      string  `json:"organization_id"`
		VendorInvoiceNumber string  `json:"vendor_invoice_number" binding:"required"`
		InvoiceDate         string  `json:"invoice_date" binding:"required"`
		DueDate             string  `json:"due_date" binding:"required"`
		CurrencyID          string  `json:"currency_id"`
		Subtotal            float64 `json:"subtotal"`
		TaxRateID           string  `json:"tax_rate_id"`
		TaxAmount           float64 `json:"tax_amount"`
		TotalAmount         float64 `json:"total_amount"`
		Notes               string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Use partner_id if vendor_id is empty (frontend sends partner_id)
	vendorIDStr := input.VendorID
	if vendorIDStr == "" {
		vendorIDStr = input.PartnerID
	}

	// Parse vendor ID
	vendorID, err := uuid.Parse(vendorIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid vendor_id")
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
	invoiceNumber := "BILL-" + time.Now().Format("20060102") + "-" + uuid.New().String()[:6]

	invoiceID := uuid.New()
	now := time.Now()

	// Calculate total if not provided
	subtotal := input.Subtotal
	taxAmount := input.TaxAmount
	totalAmount := input.TotalAmount
	if totalAmount == 0 {
		totalAmount = subtotal + taxAmount
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	var orgID *uuid.UUID
	if input.OrganizationID != "" {
		parsed, err := uuid.Parse(input.OrganizationID)
		if err == nil {
			orgID = &parsed
		}
	}
	// Fallback to middleware header if not provided in body
	if orgID == nil {
		if headerOrgID, orgOk := middleware.GetOrganizationID(c); orgOk && headerOrgID != uuid.Nil {
			orgID = &headerOrgID
		}
	}

	// Parse tax_rate_id if provided
	var taxRateID *uuid.UUID
	if input.TaxRateID != "" {
		parsed, err := uuid.Parse(input.TaxRateID)
		if err == nil {
			taxRateID = &parsed
		}
	}

	// Parse currency_id and lock the exchange rate at invoice creation time
	var currencyID *uuid.UUID
	exchangeRate := 1.0
	if input.CurrencyID != "" {
		parsed, parseErr := uuid.Parse(input.CurrencyID)
		if parseErr == nil {
			currencyID = &parsed
			var baseCurrencyID uuid.UUID
			errBase := h.db.QueryRow("SELECT id FROM currencies WHERE is_base_currency = true LIMIT 1").Scan(&baseCurrencyID)
			if errBase != nil {
				h.db.QueryRow("SELECT id FROM currencies WHERE code = 'UZS' LIMIT 1").Scan(&baseCurrencyID)
			}
			if baseCurrencyID != uuid.Nil && parsed != baseCurrencyID {
				var lockedRate float64
				errRate := h.db.QueryRow(`
					SELECT rate FROM exchange_rates
					WHERE from_currency_id = $1 AND to_currency_id = $2
					ORDER BY effective_date DESC LIMIT 1
				`, parsed, baseCurrencyID).Scan(&lockedRate)
				if errRate == nil && lockedRate > 0 {
					exchangeRate = lockedRate
				}
			}
		}
	}

	// Insert purchase invoice
	query := `
		INSERT INTO purchase_invoices (
			id, tenant_id, organization_id, invoice_number, vendor_id, vendor_invoice_number,
			invoice_date, due_date, subtotal, discount_amount,
			tax_rate_id, tax_amount, total_amount, amount_paid, status,
			three_way_match_status, notes, currency_id, exchange_rate, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`

	_, err = h.db.Exec(query,
		invoiceID, tenantID, orgID, invoiceNumber, vendorID, input.VendorInvoiceNumber,
		invoiceDate, dueDate, subtotal, 0,
		taxRateID, taxAmount, totalAmount, 0, "draft",
		"pending", input.Notes, currencyID, exchangeRate, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create purchase invoice", "error", err)
		response.InternalError(c, "Failed to create purchase invoice")
		return
	}

	// Get vendor name for response
	var vendorName string
	h.db.QueryRow("SELECT name FROM contacts WHERE id = $1", vendorID).Scan(&vendorName)

	// Return created invoice
	invoiceResponse := map[string]interface{}{
		"id":                     invoiceID.String(),
		"tenant_id":              tenantID.String(),
		"invoice_number":         invoiceNumber,
		"vendor_id":              vendorID.String(),
		"partner_id":             vendorID.String(),
		"vendor_name":            vendorName,
		"partner_name":           vendorName,
		"invoice_date":           invoiceDate.Format("2006-01-02"),
		"due_date":               dueDate.Format("2006-01-02"),
		"subtotal":               subtotal,
		"discount_amount":        0.0,
		"tax_rate_id":            taxRateID,
		"tax_amount":             taxAmount,
		"total_amount":           totalAmount,
		"amount_paid":            0.0,
		"amount_due":             totalAmount,
		"status":                 "draft",
		"three_way_match_status": "pending",
		"created_at":             now,
	}

	if input.VendorInvoiceNumber != "" {
		invoiceResponse["vendor_invoice_number"] = input.VendorInvoiceNumber
	}
	if input.Notes != "" {
		invoiceResponse["notes"] = input.Notes
	}

	// Notify: purchase invoice created
	go func() {
		amountStr := fmt.Sprintf("%.0f", totalAmount)
		h.createTranslatedNotification(tenantID, userID, "purchase_invoice_created",
			map[string]interface{}{
				"invoice_id":     invoiceID.String(),
				"invoice_number": invoiceNumber,
				"vendor_id":      vendorID.String(),
				"vendor_name":    vendorName,
				"amount":         totalAmount,
			},
			invoiceNumber, vendorName, amountStr,
		)
	}()

	response.Created(c, invoiceResponse)
}

// GetPurchaseInvoice returns a single purchase invoice by ID
func (h *Handler) GetPurchaseInvoice(c *gin.Context) {
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

	query := `
		SELECT pi.id, pi.tenant_id, pi.invoice_number, pi.vendor_id, pi.vendor_invoice_number,
			   pi.invoice_date, pi.due_date, pi.subtotal, pi.discount_amount,
			   pi.tax_amount, pi.total_amount, pi.amount_paid, pi.amount_due, pi.status,
			   pi.three_way_match_status, pi.notes, pi.created_at, pi.updated_at,
			   c.name as vendor_name,
			   COALESCE(pi.invoice_type, 'invoice') as invoice_type, pi.original_invoice_id, pi.reason,
			   pi.currency_id, COALESCE(pi.exchange_rate, 1) as exchange_rate,
			   COALESCE(cur.code, '') as currency_code
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		LEFT JOIN currencies cur ON pi.currency_id = cur.id
		WHERE pi.id = $1 AND pi.tenant_id = $2 AND pi.deleted_at IS NULL`

	var id, tenantIDScan, vendorID uuid.UUID
	var invoiceNumber, status, threeWayMatchStatus string
	var vendorInvoiceNumber, notes, vendorName sql.NullString
	var invoiceDate, dueDate time.Time
	var subtotal, discountAmount, taxAmount, totalAmount, amountPaid, amountDue float64
	var createdAt, updatedAt time.Time
	var invoiceType string
	var originalInvoiceID, reason sql.NullString
	var piCurrencyID sql.NullString
	var piExchangeRate float64
	var piCurrencyCode string

	err = h.db.QueryRow(query, invoiceID, tenantID).Scan(
		&id, &tenantIDScan, &invoiceNumber, &vendorID, &vendorInvoiceNumber,
		&invoiceDate, &dueDate, &subtotal, &discountAmount,
		&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
		&threeWayMatchStatus, &notes, &createdAt, &updatedAt,
		&vendorName,
		&invoiceType, &originalInvoiceID, &reason,
		&piCurrencyID, &piExchangeRate, &piCurrencyCode,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase invoice")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch purchase invoice", "error", err)
		response.InternalError(c, "Failed to fetch purchase invoice")
		return
	}

	invoice := map[string]interface{}{
		"id":                     id.String(),
		"tenant_id":              tenantIDScan.String(),
		"invoice_number":         invoiceNumber,
		"vendor_id":              vendorID.String(),
		"partner_id":             vendorID.String(),
		"invoice_date":           invoiceDate.Format("2006-01-02"),
		"due_date":               dueDate.Format("2006-01-02"),
		"subtotal":               subtotal,
		"discount_amount":        discountAmount,
		"tax_amount":             taxAmount,
		"total_amount":           totalAmount,
		"amount_paid":            amountPaid,
		"amount_due":             amountDue,
		"status":                 status,
		"three_way_match_status": threeWayMatchStatus,
		"invoice_type":           invoiceType,
		"created_at":             createdAt,
		"updated_at":             updatedAt,
	}

	if piCurrencyID.Valid {
		invoice["currency_id"] = piCurrencyID.String
	}
	invoice["exchange_rate"] = piExchangeRate
	if piCurrencyCode != "" {
		invoice["currency_code"] = piCurrencyCode
	}

	if vendorInvoiceNumber.Valid {
		invoice["vendor_invoice_number"] = vendorInvoiceNumber.String
	}
	if notes.Valid {
		invoice["notes"] = notes.String
	}
	if vendorName.Valid {
		invoice["vendor_name"] = vendorName.String
		invoice["partner_name"] = vendorName.String
	}
	if originalInvoiceID.Valid {
		invoice["original_invoice_id"] = originalInvoiceID.String
	}
	if reason.Valid {
		invoice["reason"] = reason.String
	}

	// Get invoice lines
	linesQuery := `
		SELECT pil.id, pil.line_number, pil.product_id, COALESCE(NULLIF(pil.description, ''), COALESCE(p.name, '')) as description,
			   pil.quantity, pil.unit_price, COALESCE(pil.discount_amount, 0), COALESCE(pil.tax_amount, 0),
			   pil.line_total
		FROM purchase_invoice_lines pil
		LEFT JOIN products p ON p.id = pil.product_id
		WHERE pil.purchase_invoice_id = $1
		ORDER BY pil.line_number`

	linesRows, lErr := h.db.Query(linesQuery, invoiceID)
	if lErr == nil {
		defer linesRows.Close()
		var lines []map[string]interface{}
		for linesRows.Next() {
			var lineID uuid.UUID
			var lineNumber int
			var description string
			var quantity, unitPrice, lineDiscountAmount, lineTaxAmount, lineTotal float64
			var productID sql.NullString

			if err := linesRows.Scan(&lineID, &lineNumber, &productID, &description, &quantity, &unitPrice, &lineDiscountAmount, &lineTaxAmount, &lineTotal); err != nil {
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
			lines = append(lines, line)
		}
		invoice["lines"] = lines
	}

	// Get payment allocations with payment details
	paQuery := `
		SELECT pa.id, pa.payment_id, pa.amount, p.payment_number, p.status, p.payment_date,
			   COALESCE(p.reference, '') as reference, COALESCE(j.name, '') as journal_name
		FROM payment_allocations pa
		JOIN payments p ON p.id = pa.payment_id
		LEFT JOIN journals j ON p.journal_id = j.id
		WHERE pa.document_type = 'purchase_invoice'
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
			var paymentNumber, pStatus, pReference, journalName string
			var paymentDate time.Time

			if err := paRows.Scan(&paID, &paymentID, &paAmount, &paymentNumber, &pStatus, &paymentDate, &pReference, &journalName); err != nil {
				continue
			}
			paymentAllocations = append(paymentAllocations, map[string]interface{}{
				"id":             paID.String(),
				"payment_id":     paymentID.String(),
				"amount":         paAmount,
				"payment_number": paymentNumber,
				"status":         pStatus,
				"payment_date":   paymentDate.Format("2006-01-02"),
				"reference":      pReference,
				"journal_name":   journalName,
			})
		}
		invoice["payment_allocations"] = paymentAllocations

		// Query exchange diffs linked to this invoice's payments
		if piExchangeRate != 1 {
			var exchangeDiffs []map[string]interface{}
			edQuery := `
				SELECT ed.id, ed.amount_uzs, ed.diff_type, ed.period_start, ed.description,
				       COALESCE(ed.document_number, ''), COALESCE(ed.initial_rate, 0), COALESCE(ed.final_rate, 0), COALESCE(ed.foreign_amount, 0)
				FROM exchange_diffs ed
				WHERE ed.tenant_id = $1 AND ed.deleted_at IS NULL
				  AND ed.journal_entry_id IN (
				    SELECT p.journal_entry_id FROM payments p
				    JOIN payment_allocations pa ON pa.payment_id = p.id
				    WHERE pa.document_type = 'purchase_invoice' AND pa.document_id = $2 AND p.deleted_at IS NULL
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

// UpdatePurchaseInvoice updates an existing purchase invoice
func (h *Handler) UpdatePurchaseInvoice(c *gin.Context) {
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
		Status      string   `json:"status"`
		Notes       string   `json:"notes"`
		DueDate     string   `json:"due_date"`
		AmountPaid  *float64 `json:"amount_paid"`
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

	// Handle payment amount - auto-determine status based on amount
	if input.AmountPaid != nil {
		// Get total_amount to determine correct status
		var totalAmount, currentAmountPaid float64
		err = h.db.QueryRow(
			"SELECT COALESCE(total_amount, 0), COALESCE(amount_paid, 0) FROM purchase_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
			invoiceID, tenantID,
		).Scan(&totalAmount, &currentAmountPaid)
		if err != nil {
			response.InternalError(c, "Failed to fetch invoice details")
			return
		}

		newAmountPaid := currentAmountPaid + *input.AmountPaid
		argCount++
		updates = append(updates, fmt.Sprintf("amount_paid = $%d", argCount))
		args = append(args, newAmountPaid)

		// Auto-set status based on payment
		argCount++
		if newAmountPaid >= totalAmount {
			updates = append(updates, fmt.Sprintf("status = $%d", argCount))
			args = append(args, "paid")
		} else {
			updates = append(updates, fmt.Sprintf("status = $%d", argCount))
			args = append(args, "partial")
		}
	} else if input.Status != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, input.Status)
	}

	if input.Notes != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, input.Notes)
	}
	if input.DueDate != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("due_date = $%d", argCount))
		dd, _ := time.Parse("2006-01-02", input.DueDate)
		args = append(args, dd)
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

	query := fmt.Sprintf("UPDATE purchase_invoices SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update purchase invoice", "error", err)
		response.InternalError(c, "Failed to update purchase invoice")
		return
	}

	// Fetch and return updated invoice
	h.GetPurchaseInvoice(c)
}

// DeletePurchaseInvoice soft deletes a purchase invoice
func (h *Handler) DeletePurchaseInvoice(c *gin.Context) {
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
	err = h.db.QueryRow("SELECT status FROM purchase_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", invoiceID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase invoice")
		return
	}
	if currentStatus != "draft" {
		response.BadRequest(c, "Can only delete invoices in draft status")
		return
	}

	result, err := h.db.Exec(
		"UPDATE purchase_invoices SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete purchase invoice")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Purchase invoice")
		return
	}

	response.NoContent(c)
}

// ConfirmPurchaseInvoice confirms a purchase invoice (changes status from draft to confirmed)
func (h *Handler) ConfirmPurchaseInvoice(c *gin.Context) {
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
	err = h.db.QueryRow("SELECT status FROM purchase_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", invoiceID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase invoice")
		return
	}
	if currentStatus != "draft" {
		response.BadRequest(c, "Can only confirm invoices in draft status")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE purchase_invoices SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		"confirmed", now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to confirm invoice")
		return
	}

	// Notify: purchase invoice confirmed
	go func() {
		var invNumber, vendorName string
		var totalAmt float64
		h.db.QueryRow(`SELECT pi.invoice_number, COALESCE(c.name, ''), COALESCE(pi.total_amount, 0)
			FROM purchase_invoices pi LEFT JOIN contacts c ON pi.vendor_id = c.id
			WHERE pi.id = $1`, invoiceID).Scan(&invNumber, &vendorName, &totalAmt)
		userID, _ := middleware.GetUserID(c)
		h.createTranslatedNotification(tenantID, userID, "purchase_invoice_confirmed",
			map[string]interface{}{
				"invoice_id":     invoiceID.String(),
				"invoice_number": invNumber,
				"vendor_name":    vendorName,
			},
			invNumber, vendorName,
		)
	}()

	h.GetPurchaseInvoice(c)
}

// PostPurchaseInvoice posts a purchase invoice and creates journal entry
func (h *Handler) PostPurchaseInvoice(c *gin.Context) {
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

	// Get current invoice status and details
	var currentStatus string
	var totalAmount, taxAmount, subtotal float64
	var vendorID uuid.UUID
	var vendorName sql.NullString
	var organizationID *uuid.UUID
	err = h.db.QueryRow(`
		SELECT pi.status, pi.total_amount, pi.tax_amount, pi.subtotal, pi.vendor_id, c.name, pi.organization_id
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.id = $1 AND pi.tenant_id = $2 AND pi.deleted_at IS NULL`,
		invoiceID, tenantID,
	).Scan(&currentStatus, &totalAmount, &taxAmount, &subtotal, &vendorID, &vendorName, &organizationID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase invoice")
		return
	}
	if currentStatus != "draft" && currentStatus != "confirmed" {
		response.BadRequest(c, "Can only post invoices in draft or confirmed status")
		return
	}

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, time.Now()); errMsg != "" {
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

	// Get Purchase Journal
	var purchaseJournalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString
	err = tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1), number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('PURCH', 'PURCHASE', 'PUR') AND deleted_at IS NULL ORDER BY CASE code WHEN 'PURCH' THEN 0 WHEN 'PURCHASE' THEN 1 ELSE 2 END LIMIT 1`,
		tenantID,
	).Scan(&purchaseJournalID, &nextNumber, &numberPrefix)

	if err == nil {
		// Odoo-style: Debit Stock Interim Receipt (per category), Credit AP
		// 1. Try vendor's default payable account
		apAccountID := getContactDefaultAccount(tx, vendorID, "payable", organizationID)
		// 2. Fallback to standard findAccount
		if apAccountID == uuid.Nil {
			apAccountID = findAccount(tx, tenantID, organizationID, "accounts payable", "6010")
		}
		taxAccountID := findAccount(tx, tenantID, organizationID, "soliqlar bo'yicha bo'nak", "4410")

		// Get invoice lines for per-category accounting
		type billLineAcct struct {
			ProductID uuid.UUID
			LineTotal float64
			InputAcct uuid.UUID
		}
		var billLines []billLineAcct
		lineRows, lineErr := tx.Query(`
			SELECT product_id, COALESCE(line_total, 0)
			FROM purchase_invoice_lines
			WHERE purchase_invoice_id = $1 AND product_id IS NOT NULL
		`, invoiceID)
		if lineErr == nil {
			for lineRows.Next() {
				var bl billLineAcct
				if err := lineRows.Scan(&bl.ProductID, &bl.LineTotal); err == nil && bl.LineTotal > 0 {
					billLines = append(billLines, bl)
				}
			}
			lineRows.Close()
			// Resolve accounts after closing rows: prefer inventory-type account, fallback to category
			for i := range billLines {
				invAcct := getInventoryAccountByType(tx, tenantID, organizationID, billLines[i].ProductID)
				if invAcct != uuid.Nil {
					billLines[i].InputAcct = invAcct
				} else {
					ca := getCategoryAccounts(tx, tenantID, organizationID, billLines[i].ProductID)
					billLines[i].InputAcct = ca.StockInputAccountID
				}
			}
		}

		// Group by stock input account
		inputGrouped := make(map[uuid.UUID]float64)
		for _, bl := range billLines {
			inputGrouped[bl.InputAcct] += bl.LineTotal
		}

		// Fallback if no lines found
		if len(inputGrouped) == 0 && subtotal > 0 {
			fallbackInput := findAccount(tx, tenantID, organizationID, "stock interim receipt", "6015")
			if fallbackInput == uuid.Nil {
				fallbackInput = findAccount(tx, tenantID, organizationID, "cost of goods", "9110")
				if fallbackInput == uuid.Nil {
					fallbackInput = findAccount(tx, tenantID, organizationID, "inventory", "1010")
				}
			}
			if fallbackInput != uuid.Nil {
				inputGrouped[fallbackInput] = subtotal
			}
		}

		if apAccountID != uuid.Nil && len(inputGrouped) > 0 {
			prefix := ""
			if numberPrefix.Valid {
				prefix = numberPrefix.String
			}
			// Scoped to (tenant, org) to match journal_entries_tenant_org_entry_number_key;
			// the journal's next_number counter alone collides with MAX-derived numbers
			// from other journals sharing the (empty) prefix.
			entryNumber := fmt.Sprintf("%s%06d", prefix, nextEntryNumberSeq(tx, tenantID, organizationID, prefix, nextNumber))

			description := "Bill Posted"
			if vendorName.Valid {
				description = fmt.Sprintf("Bill: %s", vendorName.String)
			}

			journalEntryID := uuid.New()
			_, err = tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
				journalEntryID, tenantID, organizationID, purchaseJournalID, entryNumber, now, invoiceID.String()[:8], description,
				"purchase_invoice", invoiceID.String(), 1.0, totalAmount, totalAmount, userID, now, now,
			)
			if err != nil {
				h.log.Error("Failed to create journal entry for posted invoice", "error", err)
			} else {
				lineNumber := 1

				// Debit: Stock Interim Receipt (per category) — clears the interim from goods receipt
				for inputAcct, amount := range inputGrouped {
					lineID := uuid.New()
					tx.Exec(`
						INSERT INTO journal_entry_lines (
							id, journal_entry_id, line_number, account_id, description,
							debit_amount, credit_amount, exchange_rate, created_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
						lineID, journalEntryID, lineNumber, inputAcct, "Stock Interim Receipt",
						amount, 0.0, 1.0, now,
					)
					tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", amount, now, inputAcct)
					lineNumber++
				}

				// Debit Tax (if applicable)
				if taxAccountID != uuid.Nil && taxAmount > 0 {
					taxLineID := uuid.New()
					tx.Exec(`
						INSERT INTO journal_entry_lines (
							id, journal_entry_id, line_number, account_id, description,
							debit_amount, credit_amount, exchange_rate, created_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
						taxLineID, journalEntryID, lineNumber, taxAccountID, "Input Tax",
						taxAmount, 0.0, 1.0, now,
					)
					tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", taxAmount, now, taxAccountID)
					lineNumber++
				}

				// Credit: Accounts Payable (total amount)
				apLineID := uuid.New()
				tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, contact_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
					apLineID, journalEntryID, lineNumber, apAccountID, vendorID, "Accounts Payable",
					0.0, totalAmount, 1.0, now,
				)
				tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalAmount, now, apAccountID)

				// Update journal next number
				tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", purchaseJournalID)

				// Link journal entry to invoice
				tx.Exec("UPDATE purchase_invoices SET journal_entry_id = $1 WHERE id = $2", journalEntryID, invoiceID)
			}
		} else {
			h.log.Warn("Could not create journal entry: missing AP or input accounts",
				"has_ap", apAccountID != uuid.Nil, "input_groups", len(inputGrouped))
		}
	}

	// Update invoice status to posted
	_, err = tx.Exec(
		"UPDATE purchase_invoices SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		"posted", now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to post invoice")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	h.GetPurchaseInvoice(c)
}

// PayPurchaseInvoice records a payment against a purchase invoice
func (h *Handler) PayPurchaseInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	// The route group only enforces purchase:invoice:read; recording a payment
	// mutates the invoice, so require update here (mirrors the h.perm.Require
	// checks the other mutating purchase-invoice endpoints get at the route level).
	if h.perm != nil && !h.perm.Can(c, "purchase", "invoice", "update") {
		response.Forbidden(c, "Missing required permission: purchase:invoice:update")
		return
	}

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	var input struct {
		Amount         float64 `json:"amount"`
		WriteOffAmount float64 `json:"write_off_amount,omitempty"`
		PaymentMethod  string  `json:"payment_method"` // cash, bank, card
	}
	c.ShouldBindJSON(&input)

	// Get current invoice status and amounts
	var currentStatus string
	var amountPaid, totalAmount float64
	var organizationID *uuid.UUID
	err = h.db.QueryRow(
		"SELECT status, amount_paid, total_amount, organization_id FROM purchase_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		invoiceID, tenantID,
	).Scan(&currentStatus, &amountPaid, &totalAmount, &organizationID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase invoice")
		return
	}
	if currentStatus == "cancelled" {
		response.BadRequest(c, "Cannot record payment for cancelled invoice")
		return
	}
	if currentStatus == "paid" {
		response.BadRequest(c, "Invoice is already fully paid")
		return
	}

	// If no amount specified, pay in full
	paymentAmount := input.Amount
	if paymentAmount == 0 {
		paymentAmount = totalAmount - amountPaid - input.WriteOffAmount
	}

	newAmountPaid := amountPaid + paymentAmount + input.WriteOffAmount
	newStatus := "partial"
	if newAmountPaid >= totalAmount {
		newStatus = "paid"
		newAmountPaid = totalAmount // Don't overpay
	}

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, time.Now()); errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE purchase_invoices SET amount_paid = $1, status = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		newAmountPaid, newStatus, now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to record payment")
		return
	}

	// Payment record details. The payments/payment_allocations INSERTs happen
	// inside the journal-entry transaction below so the payment, its
	// allocation, the JE and the balance updates commit (or roll back) together.
	paymentID := uuid.New()
	paymentNumber := fmt.Sprintf("PAY-%s", now.Format("20060102150405"))

	// Get vendor info for payment reference
	var vendorID sql.NullString
	var vendorName sql.NullString
	h.db.QueryRow(`
		SELECT pi.vendor_id, c.name
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.id = $1`, invoiceID).Scan(&vendorID, &vendorName)

	reference := fmt.Sprintf("Payment for %s", invoiceID.String()[:8])
	if vendorName.Valid {
		reference = fmt.Sprintf("Payment to %s", vendorName.String)
	}

	// Create journal entry for payment: Debit AP, Credit Cash/Bank
	// Select journal based on payment method
	var payJournalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString

	var journalQuery string
	switch input.PaymentMethod {
	case "cash":
		journalQuery = `SELECT id, COALESCE(next_number, 1), number_prefix FROM journals WHERE tenant_id = $1 AND code IN ('CASH','CASH_DISBURSEMENTS') AND deleted_at IS NULL ORDER BY CASE code WHEN 'CASH' THEN 0 ELSE 1 END LIMIT 1`
	case "card":
		journalQuery = `SELECT id, COALESCE(next_number, 1), number_prefix FROM journals WHERE tenant_id = $1 AND code IN ('BANK','CASH_DISBURSEMENTS') AND deleted_at IS NULL ORDER BY CASE code WHEN 'BANK' THEN 0 ELSE 1 END LIMIT 1`
	default: // bank
		journalQuery = `SELECT id, COALESCE(next_number, 1), number_prefix FROM journals WHERE tenant_id = $1 AND code IN ('BANK','CASH_DISBURSEMENTS','PURCH','PURCHASE','PUR') AND deleted_at IS NULL ORDER BY CASE code WHEN 'BANK' THEN 0 WHEN 'CASH_DISBURSEMENTS' THEN 1 WHEN 'PURCH' THEN 2 ELSE 3 END LIMIT 1`
	}
	err = h.db.QueryRow(journalQuery, tenantID).Scan(&payJournalID, &nextNumber, &numberPrefix)

	// Fallback: try any active journal with matching type
	if err != nil {
		h.log.Warn("No journal found by code for payment, trying fallback", "payment_method", input.PaymentMethod, "error", err)
		fallbackType := "bank"
		if input.PaymentMethod == "cash" {
			fallbackType = "cash"
		}
		err = h.db.QueryRow(
			`SELECT id, COALESCE(next_number, 1), number_prefix FROM journals WHERE tenant_id = $1 AND type = $2 AND deleted_at IS NULL ORDER BY is_default DESC, created_at ASC LIMIT 1`,
			tenantID, fallbackType,
		).Scan(&payJournalID, &nextNumber, &numberPrefix)
		if err != nil {
			// Last fallback: any journal
			err = h.db.QueryRow(
				`SELECT id, COALESCE(next_number, 1), number_prefix FROM journals WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY is_default DESC LIMIT 1`,
				tenantID,
			).Scan(&payJournalID, &nextNumber, &numberPrefix)
			if err != nil {
				h.log.Error("No journal found at all for payment", "tenant_id", tenantID, "error", err)
			}
		}
	}

	if err == nil {
		apAcctID := findAccount(h.db, tenantID, organizationID, "accounts payable", "6010")
		if apAcctID == uuid.Nil {
			apAcctID = findAccount(h.db, tenantID, organizationID, "kreditorlar", "6010")
		}
		if apAcctID == uuid.Nil {
			apAcctID = findAccount(h.db, tenantID, organizationID, "payable", "6010")
		}
		if apAcctID == uuid.Nil {
			apAcctID = findAccount(h.db, tenantID, organizationID, "kreditorlik", "6010")
		}
		// Select credit account based on payment method
		var cashAcctID uuid.UUID
		switch input.PaymentMethod {
		case "cash":
			cashAcctID = findAccount(h.db, tenantID, organizationID, "cash", "5010")
			if cashAcctID == uuid.Nil {
				cashAcctID = findAccount(h.db, tenantID, organizationID, "kassa", "5010")
			}
		default: // bank, card
			cashAcctID = findAccount(h.db, tenantID, organizationID, "bank account", "5110")
			if cashAcctID == uuid.Nil {
				cashAcctID = findAccount(h.db, tenantID, organizationID, "bank account", "5110")
			}
			if cashAcctID == uuid.Nil {
				cashAcctID = findAccount(h.db, tenantID, organizationID, "bank hisobi", "5110")
			}
		}
		if cashAcctID == uuid.Nil {
			// Final fallback
			cashAcctID = findAccount(h.db, tenantID, organizationID, "outstanding payments", "5110")
		}

		if apAcctID == uuid.Nil {
			h.log.Error("AP account not found for vendor payment", "tenant_id", tenantID, "org_id", organizationID)
		}
		if cashAcctID == uuid.Nil {
			h.log.Error("Cash/Bank account not found for vendor payment", "tenant_id", tenantID, "org_id", organizationID, "payment_method", input.PaymentMethod)
		}
		if apAcctID != uuid.Nil && cashAcctID != uuid.Nil {
			prefix := ""
			if numberPrefix.Valid {
				prefix = numberPrefix.String
			}
			entryNumber := fmt.Sprintf("%s%06d", prefix, nextEntryNumberSeq(h.db, tenantID, organizationID, prefix, nextNumber))
			description := fmt.Sprintf("Payment for invoice %s", invoiceID.String()[:8])
			if vendorName.Valid && vendorName.String != "" {
				description = fmt.Sprintf("Payment for invoice %s — %s", invoiceID.String()[:8], vendorName.String)
			}

			var contactUUID uuid.UUID
			if vendorID.Valid {
				contactUUID, _ = uuid.Parse(vendorID.String)
			}

			journalEntryID := uuid.New()

			// Resolve the write-off income account up front. If a write-off is
			// requested but no income account exists, treat write-off as 0 so the
			// JE stays balanced (DR AP = CR Cash) rather than being rejected by
			// the deferred balance trigger and lost entirely.
			var otherIncomeID uuid.UUID
			effectiveWriteOff := 0.0
			if input.WriteOffAmount > 0 {
				otherIncomeID = findAccount(h.db, tenantID, organizationID, "other income", "9310")
				if otherIncomeID == uuid.Nil {
					otherIncomeID = findAccount(h.db, tenantID, organizationID, "payment difference write-off", "9690")
				}
				if otherIncomeID != uuid.Nil {
					effectiveWriteOff = input.WriteOffAmount
				} else {
					h.log.Warn("Write-off requested but no income account found; posting payment without write-off", "invoice_id", invoiceID)
				}
			}
			apDebitAmount := paymentAmount + effectiveWriteOff
			jeTotal := apDebitAmount

			// Header + all lines + balance updates in ONE transaction (migration 416).
			tx, txErr := h.db.Begin()
			if txErr != nil {
				h.log.Error("Failed to begin payment journal tx", "error", txErr)
			} else {
				committed := false
				defer func() {
					if !committed {
						tx.Rollback()
					}
				}()

				if _, err = tx.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
						source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15)`,
					journalEntryID, tenantID, organizationID, payJournalID, entryNumber, now, invoiceID.String()[:8], description,
					"purchase_invoice_payment", invoiceID.String(), 1.0, jeTotal, jeTotal, now, now,
				); err != nil {
					h.log.Error("Failed to create journal entry for payment", "error", err)
				} else {
					// Debit AP (payment + write-off reduces AP)
					apLineID := uuid.New()
					_, err = tx.Exec(`
						INSERT INTO journal_entry_lines (
							id, journal_entry_id, line_number, account_id, contact_id, description,
							debit_amount, credit_amount, exchange_rate, created_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
						apLineID, journalEntryID, 1, apAcctID, contactUUID, "Accounts Payable",
						apDebitAmount, 0.0, 1.0, now,
					)
					// Credit Cash/Outstanding Payments
					if err == nil {
						cashLineID := uuid.New()
						_, err = tx.Exec(`
							INSERT INTO journal_entry_lines (
								id, journal_entry_id, line_number, account_id, description,
								debit_amount, credit_amount, exchange_rate, created_at
							) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
							cashLineID, journalEntryID, 2, cashAcctID, "Outstanding Payment",
							0.0, paymentAmount, 1.0, now,
						)
					}

					// Write-off line: CR Other Income (vendor owes less = gain for us)
					if err == nil && effectiveWriteOff > 0 {
						writeOffLineID := uuid.New()
						if _, err = tx.Exec(`
							INSERT INTO journal_entry_lines (
								id, journal_entry_id, line_number, account_id, description,
								debit_amount, credit_amount, exchange_rate, created_at
							) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
							writeOffLineID, journalEntryID, 3, otherIncomeID, "Payment Difference Write-off",
							0.0, effectiveWriteOff, 1.0, now,
						); err == nil {
							_, err = tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", effectiveWriteOff, now, otherIncomeID)
						}
					}

					// Update journal next number + account balances
					if err == nil {
						_, err = tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", payJournalID)
					}
					if err == nil {
						// Debit AP (credit-normal: debit decreases) — includes write-off
						_, err = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", apDebitAmount, now, apAcctID)
					}
					if err == nil {
						// Credit Cash/Bank (debit-normal: credit decreases)
						_, err = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", paymentAmount, now, cashAcctID)
					}

					// Payment record + allocation in the same tx so they only
					// commit together with the journal entry.
					// Only create payment if we have a valid contact_id (required by schema)
					if err == nil {
						if contactUUID != uuid.Nil {
							if _, err = tx.Exec(`
								INSERT INTO payments (
									id, tenant_id, organization_id, payment_number, type, contact_id, payment_date, amount,
									exchange_rate, reference, notes, status, created_at, updated_at
								) VALUES ($1, $2, $3, $4, 'payment', $5, $6, $7, 1, $8, $9, 'confirmed', $10, $10)
							`, paymentID, tenantID, organizationID, paymentNumber, contactUUID, now, paymentAmount, reference, description, now); err != nil {
								h.log.Error("Failed to create payment record", "error", err)
							} else {
								// Payment allocation linking payment to this invoice
								allocID := uuid.New()
								if _, err = tx.Exec(`
									INSERT INTO payment_allocations (id, payment_id, document_type, document_id, amount, created_at)
									VALUES ($1, $2, 'purchase_invoice', $3, $4, $5)
								`, allocID, paymentID, invoiceID, paymentAmount, now); err != nil {
									h.log.Error("Failed to create payment allocation", "error", err)
								}
							}
						} else {
							h.log.Warn("Cannot create payment record: no vendor_id on invoice", "invoice_id", invoiceID)
						}
					}

					if err != nil {
						h.log.Error("Failed to post vendor payment journal entry", "error", err)
					} else if err = tx.Commit(); err != nil {
						h.log.Error("Failed to commit vendor payment journal entry", "error", err)
					} else {
						committed = true
						h.log.Info("Vendor payment posted", "payment_id", paymentID, "invoice_id", invoiceID, "ap_account", apAcctID, "cash_account", cashAcctID, "amount", paymentAmount)
					}
				}
			}
		}
	}

	h.GetPurchaseInvoice(c)
}

// CreateDebitNote creates a debit note from an existing purchase invoice (vendor bill)
func (h *Handler) CreateDebitNote(c *gin.Context) {
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

	var input entity.CreateDebitNoteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Load original purchase invoice
	var orgInvoiceNumber, currentStatus string
	var vendorID uuid.UUID
	var subtotal, taxAmount, totalAmount float64

	err = h.db.QueryRow(`
		SELECT invoice_number, status, vendor_id, subtotal, tax_amount, total_amount
		FROM purchase_invoices
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND COALESCE(invoice_type, 'invoice') = 'invoice'`,
		invoiceID, tenantID,
	).Scan(&orgInvoiceNumber, &currentStatus, &vendorID, &subtotal, &taxAmount, &totalAmount)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase invoice")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch purchase invoice")
		return
	}

	if currentStatus != "posted" && currentStatus != "partial" && currentStatus != "paid" && currentStatus != "confirmed" {
		response.BadRequest(c, "Can only create debit notes for posted, confirmed, partial, or paid bills")
		return
	}

	dnDate := time.Now()
	if input.DebitNoteDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.DebitNoteDate); err == nil {
			dnDate = parsed
		}
	}

	dnNumber := "DN-" + dnDate.Format("20060102") + "-" + uuid.New().String()[:6]
	debitNoteID := uuid.New()
	now := time.Now()

	var dnSubtotal, dnTaxAmount, dnTotalAmount float64

	if len(input.Lines) > 0 {
		for _, line := range input.Lines {
			dnSubtotal += line.Quantity * line.UnitPrice
		}
		if subtotal > 0 {
			dnTaxAmount = dnSubtotal * (taxAmount / subtotal)
		}
		dnTotalAmount = dnSubtotal + dnTaxAmount
	} else {
		dnSubtotal = subtotal
		dnTaxAmount = taxAmount
		dnTotalAmount = totalAmount
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	_, err = h.db.Exec(`
		INSERT INTO purchase_invoices (
			id, tenant_id, invoice_number, vendor_id, vendor_invoice_number,
			invoice_date, due_date, invoice_type, original_invoice_id, reason,
			subtotal, discount_amount, tax_amount, total_amount, amount_paid, status,
			three_way_match_status, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'debit_note', $8, $9, $10, 0, $11, $12, 0, 'draft', 'not_applicable', $13, $14, $15, $16)`,
		debitNoteID, tenantID, dnNumber, vendorID, "",
		dnDate, dnDate, invoiceID, input.Reason,
		dnSubtotal, dnTaxAmount, dnTotalAmount,
		orgInvoiceNumber, createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create debit note", "error", err)
		h.log.Error("Failed to create debit note", "error", err)
		response.InternalError(c, "Failed to create debit note")
		return
	}

	// Replace the id param so GetPurchaseInvoice returns the debit note
	for i, p := range c.Params {
		if p.Key == "id" {
			c.Params[i].Value = debitNoteID.String()
			break
		}
	}
	h.GetPurchaseInvoice(c)
}

// ConfirmDebitNote confirms a debit note and creates reversed GL entries
func (h *Handler) ConfirmDebitNote(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	debitNoteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid debit note ID")
		return
	}

	var currentStatus, dnNumber string
	var vendorID uuid.UUID
	var originalInvoiceID *uuid.UUID
	var organizationID *uuid.UUID
	var totalAmount, taxAmount, dnSubtotal float64
	var dnDate time.Time

	err = h.db.QueryRow(`
		SELECT status, invoice_number, vendor_id, original_invoice_id, organization_id, total_amount, tax_amount, subtotal, invoice_date
		FROM purchase_invoices
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND invoice_type = 'debit_note'`,
		debitNoteID, tenantID,
	).Scan(&currentStatus, &dnNumber, &vendorID, &originalInvoiceID, &organizationID, &totalAmount, &taxAmount, &dnSubtotal, &dnDate)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Debit note")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch debit note")
		return
	}
	if currentStatus != "draft" {
		response.BadRequest(c, "Debit note is not in draft status")
		return
	}

	// Check lock date
	if errMsg := h.checkLockDate(tenantID, dnDate); errMsg != "" {
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

	// Get Purchase Journal
	var purchaseJournalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString
	err = tx.QueryRow(`
		SELECT id, COALESCE(next_number, 1), number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('PURCH', 'PURCHASE', 'PUR') AND deleted_at IS NULL ORDER BY CASE code WHEN 'PURCH' THEN 0 WHEN 'PURCHASE' THEN 1 ELSE 2 END LIMIT 1`,
		tenantID,
	).Scan(&purchaseJournalID, &nextNumber, &numberPrefix)

	if err == nil {
		apAccountID := findAccount(tx, tenantID, organizationID, "accounts payable", "6010")
		expenseAccountID := findAccount(tx, tenantID, organizationID, "expense", "9420")
		taxAccountID := findAccount(tx, tenantID, organizationID, "soliqlar bo'yicha bo'nak", "4410")

		// Mirror PostPurchaseInvoice's debit-side account selection so the
		// reversal credits the same stock-input/interim accounts the original
		// bill was debited on (per-line inventory/category stock input, then
		// the 6015→9110→1010 fallback chain). The 9420 expense account stays
		// as the last-resort fallback for documents without product lines.
		type dnLineAcct struct {
			ProductID uuid.UUID
			LineTotal float64
			InputAcct uuid.UUID
		}
		var dnLines []dnLineAcct
		origLinesTotal := 0.0
		if originalInvoiceID != nil && dnSubtotal > 0 {
			lineRows, lineErr := tx.Query(`
				SELECT product_id, COALESCE(line_total, 0)
				FROM purchase_invoice_lines
				WHERE purchase_invoice_id = $1 AND product_id IS NOT NULL
			`, *originalInvoiceID)
			if lineErr == nil {
				for lineRows.Next() {
					var dl dnLineAcct
					if scanErr := lineRows.Scan(&dl.ProductID, &dl.LineTotal); scanErr == nil && dl.LineTotal > 0 {
						dnLines = append(dnLines, dl)
						origLinesTotal += dl.LineTotal
					}
				}
				lineRows.Close()
				// Resolve accounts after closing rows: prefer inventory-type account, fallback to category
				for i := range dnLines {
					invAcct := getInventoryAccountByType(tx, tenantID, organizationID, dnLines[i].ProductID)
					if invAcct != uuid.Nil {
						dnLines[i].InputAcct = invAcct
					} else {
						ca := getCategoryAccounts(tx, tenantID, organizationID, dnLines[i].ProductID)
						dnLines[i].InputAcct = ca.StockInputAccountID
					}
				}
			}
		}

		// Group by stock input account, scaled to the debit note subtotal
		// (partial debit notes). One group absorbs the rounding remainder so
		// the credit side sums exactly to dnSubtotal (deferred Dt=Kt trigger
		// from migration 416 rejects the commit otherwise).
		creditGrouped := make(map[uuid.UUID]float64)
		if len(dnLines) > 0 && origLinesTotal > 0 {
			ratio := dnSubtotal / origLinesTotal
			for _, dl := range dnLines {
				acct := dl.InputAcct
				if acct == uuid.Nil {
					acct = expenseAccountID
				}
				if acct == uuid.Nil {
					continue
				}
				creditGrouped[acct] += math.Round(dl.LineTotal*ratio*100) / 100
			}
			roundedSum := 0.0
			for _, amount := range creditGrouped {
				roundedSum += amount
			}
			if diff := dnSubtotal - roundedSum; diff != 0 {
				for acct := range creditGrouped {
					creditGrouped[acct] += diff
					break
				}
			}
		}

		// Fallback: no product lines on the original bill — same account chain
		// PostPurchaseInvoice falls back to, then the pre-existing 9420 expense
		if len(creditGrouped) == 0 && dnSubtotal > 0 {
			fallbackInput := findAccount(tx, tenantID, organizationID, "stock interim receipt", "6015")
			if fallbackInput == uuid.Nil {
				fallbackInput = findAccount(tx, tenantID, organizationID, "cost of goods", "9110")
				if fallbackInput == uuid.Nil {
					fallbackInput = findAccount(tx, tenantID, organizationID, "inventory", "1010")
				}
			}
			if fallbackInput == uuid.Nil {
				fallbackInput = expenseAccountID
			}
			if fallbackInput != uuid.Nil {
				creditGrouped[fallbackInput] = dnSubtotal
			}
		}

		if apAccountID != uuid.Nil {
			prefix := ""
			if numberPrefix.Valid {
				prefix = numberPrefix.String
			}
			entryNumber := fmt.Sprintf("%s%06d", prefix, nextEntryNumberSeq(tx, tenantID, organizationID, prefix, nextNumber))

			journalEntryID := uuid.New()
			description := fmt.Sprintf("Debit Note %s", dnNumber)

			_, err = tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
				journalEntryID, tenantID, organizationID, purchaseJournalID, entryNumber, dnDate, dnNumber, description,
				"debit_note", debitNoteID.String(), 1.0, totalAmount, totalAmount, userID, now, now,
			)
			if err != nil {
				h.log.Error("Failed to create debit note journal entry", "error", err)
				response.InternalError(c, "Failed to create journal entry")
				return
			}

			lineNumber := 1

			// Debit AP (reduce payable — we owe the vendor less)
			apLineID := uuid.New()
			tx.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, contact_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				apLineID, journalEntryID, lineNumber, apAccountID, vendorID, "Accounts Payable (Debit Note)",
				totalAmount, 0.0, 1.0, now,
			)
			lineNumber++

			// Credit: reverse the same stock-input accounts the original bill
			// debited (grouped per account)
			for creditAcct, amount := range creditGrouped {
				lineID := uuid.New()
				tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					lineID, journalEntryID, lineNumber, creditAcct, "Purchase Reversal (Debit Note)",
					0.0, amount, 1.0, now,
				)
				lineNumber++
			}

			// Credit Tax (reversal)
			if taxAccountID != uuid.Nil && taxAmount > 0 {
				lineID := uuid.New()
				tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					lineID, journalEntryID, lineNumber, taxAccountID, "Tax Reversal (Debit Note)",
					0.0, taxAmount, 1.0, now,
				)
			}

			// Update account balances
			tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalAmount, now, apAccountID)
			for creditAcct, amount := range creditGrouped {
				tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", amount, now, creditAcct)
			}
			if taxAccountID != uuid.Nil && taxAmount > 0 {
				tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", taxAmount, now, taxAccountID)
			}

			tx.Exec("UPDATE journals SET next_number = next_number + 1 WHERE id = $1", purchaseJournalID)
		}
	}

	// Update debit note status
	_, err = tx.Exec(
		"UPDATE purchase_invoices SET status = 'posted', updated_at = $1 WHERE id = $2 AND tenant_id = $3",
		now, debitNoteID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to confirm debit note")
		return
	}

	// Reduce the original bill balance
	if originalInvoiceID != nil {
		tx.Exec(`
			UPDATE purchase_invoices SET amount_paid = amount_paid + $1, updated_at = $2
			WHERE id = $3 AND tenant_id = $4`,
			totalAmount, now, *originalInvoiceID, tenantID,
		)
		tx.Exec(`
			UPDATE purchase_invoices SET status = CASE
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

	h.GetPurchaseInvoice(c)
}

// GetPurchaseInvoiceStats returns summary statistics for vendor bills
func (h *Handler) GetPurchaseInvoiceStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	baseWhere := "WHERE tenant_id = $1 AND deleted_at IS NULL"
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseWhere += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	query := fmt.Sprintf(`
		SELECT
			COUNT(*) AS total_count,
			COALESCE(SUM(total_amount), 0) AS total_amount,
			COUNT(*) FILTER (WHERE status NOT IN ('paid', 'cancelled') AND (amount_paid IS NULL OR amount_paid = 0)) AS unpaid_count,
			COALESCE(SUM(total_amount - COALESCE(amount_paid, 0)) FILTER (WHERE status NOT IN ('paid', 'cancelled') AND (amount_paid IS NULL OR amount_paid = 0)), 0) AS unpaid_amount,
			COUNT(*) FILTER (WHERE status NOT IN ('paid', 'cancelled') AND amount_paid > 0 AND amount_paid < total_amount) AS partial_count,
			COALESCE(SUM(total_amount - COALESCE(amount_paid, 0)) FILTER (WHERE status NOT IN ('paid', 'cancelled') AND amount_paid > 0 AND amount_paid < total_amount), 0) AS partial_amount,
			COUNT(*) FILTER (WHERE due_date < CURRENT_DATE AND status NOT IN ('paid', 'cancelled')) AS overdue_count,
			COALESCE(SUM(total_amount - COALESCE(amount_paid, 0)) FILTER (WHERE due_date < CURRENT_DATE AND status NOT IN ('paid', 'cancelled')), 0) AS overdue_amount
		FROM purchase_invoices
		%s
	`, baseWhere)

	var totalCount int
	var totalAmount, unpaidAmount, partialAmount, overdueAmount float64
	var unpaidCount, partialCount, overdueCount int

	err := h.db.QueryRow(query, args...).Scan(
		&totalCount, &totalAmount,
		&unpaidCount, &unpaidAmount,
		&partialCount, &partialAmount,
		&overdueCount, &overdueAmount,
	)
	if err != nil {
		h.log.Error("Failed to fetch purchase invoice stats", "error", err)
		response.InternalError(c, "Failed to fetch stats")
		return
	}

	response.Success(c, map[string]interface{}{
		"total_count":    totalCount,
		"total_amount":   totalAmount,
		"unpaid_count":   unpaidCount,
		"unpaid_amount":  unpaidAmount,
		"partial_count":  partialCount,
		"partial_amount": partialAmount,
		"overdue_count":  overdueCount,
		"overdue_amount": overdueAmount,
	})
}
