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
			   pi.tax_amount, pi.total_amount, pi.amount_paid, pi.amount_due, pi.status,
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
		response.InternalError(c, "Failed to fetch purchase invoices: "+err.Error())
		return
	}
	defer rows.Close()

	var invoices []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan, vendorID uuid.UUID
		var invoiceNumber, status, threeWayMatchStatus string
		var vendorInvoiceNumber, notes, vendorName sql.NullString
		var invoiceDate, dueDate time.Time
		var subtotal, discountAmount, taxAmount, totalAmount, amountPaid, amountDue float64
		var createdAt, updatedAt time.Time
		var purchaseOrderID, goodsReceiptID sql.NullString
		var poNumber, grNumber sql.NullString
		var invoiceType string
		var originalInvoiceID, reason sql.NullString

		err := rows.Scan(
			&id, &tenantIDScan, &invoiceNumber, &vendorID, &vendorInvoiceNumber,
			&invoiceDate, &dueDate, &subtotal, &discountAmount,
			&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
			&threeWayMatchStatus, &notes, &createdAt, &updatedAt,
			&vendorName,
			&purchaseOrderID, &poNumber,
			&goodsReceiptID, &grNumber,
			&invoiceType, &originalInvoiceID, &reason,
		)
		if err != nil {
			continue
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
			"status":                status,
			"three_way_match_status": threeWayMatchStatus,
			"invoice_type":          invoiceType,
			"created_at":            createdAt,
			"updated_at":            updatedAt,
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
		VendorInvoiceNumber string  `json:"vendor_invoice_number"`
		InvoiceDate         string  `json:"invoice_date" binding:"required"`
		DueDate             string  `json:"due_date" binding:"required"`
		Subtotal            float64 `json:"subtotal"`
		TaxAmount           float64 `json:"tax_amount"`
		TotalAmount         float64 `json:"total_amount"`
		Notes               string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
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

	// Insert purchase invoice
	query := `
		INSERT INTO purchase_invoices (
			id, tenant_id, invoice_number, vendor_id, vendor_invoice_number,
			invoice_date, due_date, subtotal, discount_amount,
			tax_amount, total_amount, amount_paid, status,
			three_way_match_status, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`

	_, err = h.db.Exec(query,
		invoiceID, tenantID, invoiceNumber, vendorID, input.VendorInvoiceNumber,
		invoiceDate, dueDate, subtotal, 0,
		taxAmount, totalAmount, 0, "draft",
		"pending", input.Notes, createdBy, now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create purchase invoice: "+err.Error())
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
			   COALESCE(pi.invoice_type, 'invoice') as invoice_type, pi.original_invoice_id, pi.reason
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.id = $1 AND pi.tenant_id = $2 AND pi.deleted_at IS NULL`

	var id, tenantIDScan, vendorID uuid.UUID
	var invoiceNumber, status, threeWayMatchStatus string
	var vendorInvoiceNumber, notes, vendorName sql.NullString
	var invoiceDate, dueDate time.Time
	var subtotal, discountAmount, taxAmount, totalAmount, amountPaid, amountDue float64
	var createdAt, updatedAt time.Time
	var invoiceType string
	var originalInvoiceID, reason sql.NullString

	err = h.db.QueryRow(query, invoiceID, tenantID).Scan(
		&id, &tenantIDScan, &invoiceNumber, &vendorID, &vendorInvoiceNumber,
		&invoiceDate, &dueDate, &subtotal, &discountAmount,
		&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
		&threeWayMatchStatus, &notes, &createdAt, &updatedAt,
		&vendorName,
		&invoiceType, &originalInvoiceID, &reason,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase invoice")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch purchase invoice: "+err.Error())
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
		Status  string `json:"status"`
		Notes   string `json:"notes"`
		DueDate string `json:"due_date"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Status != "" {
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
		response.InternalError(c, "Failed to update purchase invoice: "+err.Error())
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

	h.GetPurchaseInvoice(c)
}

// PostPurchaseInvoice posts a purchase invoice and creates journal entry
func (h *Handler) PostPurchaseInvoice(c *gin.Context) {
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

	// Get current invoice status and details
	var currentStatus string
	var totalAmount float64
	var vendorName sql.NullString
	err = h.db.QueryRow(`
		SELECT pi.status, pi.total_amount, c.name
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.id = $1 AND pi.tenant_id = $2 AND pi.deleted_at IS NULL`,
		invoiceID, tenantID,
	).Scan(&currentStatus, &totalAmount, &vendorName)
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

	// Get account IDs for journal entry
	// Try to find Inventory or Stock account (asset)
	var inventoryAccountID sql.NullString
	h.db.QueryRow(`
		SELECT id FROM chart_of_accounts
		WHERE tenant_id = $1 AND account_type = 'asset'
		AND (LOWER(name) LIKE '%inventory%' OR LOWER(name) LIKE '%stock%')
		AND deleted_at IS NULL
		LIMIT 1`, tenantID).Scan(&inventoryAccountID)

	// Try to find Accounts Payable account (liability)
	var apAccountID sql.NullString
	h.db.QueryRow(`
		SELECT id FROM chart_of_accounts
		WHERE tenant_id = $1 AND account_type = 'liability'
		AND LOWER(name) LIKE '%payable%'
		AND deleted_at IS NULL
		LIMIT 1`, tenantID).Scan(&apAccountID)

	// Create journal entry if we have both accounts
	if inventoryAccountID.Valid && apAccountID.Valid {
		entryID := uuid.New()
		entryNumber := fmt.Sprintf("JE-%s", now.Format("20060102150405"))
		description := "Bill Posted"
		if vendorName.Valid {
			description = fmt.Sprintf("Bill: %s", vendorName.String)
		}

		invAcctID, _ := uuid.Parse(inventoryAccountID.String)
		apAcctID, _ := uuid.Parse(apAccountID.String)

		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, entry_number, entry_date, entry_type,
				debit_account_id, credit_account_id, amount,
				description, reference_type, reference_id, status,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'purchase', $5, $6, $7, $8, 'purchase_invoice', $9, 'posted', $10, $10)
		`, entryID, tenantID, entryNumber, now, invAcctID, apAcctID,
			totalAmount, description, invoiceID, now)

		if err != nil {
			h.log.Error("Failed to create journal entry for posted invoice", "error", err)
		} else {
			h.log.Info("Journal entry created for posted invoice", "entry_id", entryID, "invoice_id", invoiceID)
		}
	} else {
		h.log.Warn("Could not create journal entry: missing Inventory or Accounts Payable account",
			"has_inventory", inventoryAccountID.Valid, "has_ap", apAccountID.Valid)
	}

	// Update invoice status to posted
	_, err = h.db.Exec(
		"UPDATE purchase_invoices SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		"posted", now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to post invoice")
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

	invoiceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	var input struct {
		Amount float64 `json:"amount"`
	}
	c.ShouldBindJSON(&input)

	// Get current invoice status and amounts
	var currentStatus string
	var amountPaid, totalAmount float64
	err = h.db.QueryRow(
		"SELECT status, amount_paid, total_amount FROM purchase_invoices WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		invoiceID, tenantID,
	).Scan(&currentStatus, &amountPaid, &totalAmount)
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
		paymentAmount = totalAmount - amountPaid
	}

	newAmountPaid := amountPaid + paymentAmount
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

	// If invoice is fully paid, update linked project expense status to 'paid'
	if newStatus == "paid" {
		_, err = h.db.Exec(
			"UPDATE project_expenses SET status = 'paid', updated_at = $1 WHERE purchase_invoice_id = $2 AND tenant_id = $3",
			now, invoiceID, tenantID,
		)
		if err != nil {
			h.log.Error("Failed to update project expense status", "error", err, "invoice_id", invoiceID)
		} else {
			h.log.Info("Project expense status updated to paid", "invoice_id", invoiceID)
		}
	}

	// Create Payment record so it appears in Payments section
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

	// Only create payment if we have a valid contact_id (required by schema)
	if vendorID.Valid {
		contactUUID, parseErr := uuid.Parse(vendorID.String)
		if parseErr == nil {
			_, err = h.db.Exec(`
				INSERT INTO payments (
					id, tenant_id, payment_number, type, contact_id, payment_date, amount,
					exchange_rate, reference, notes, status, created_at, updated_at
				) VALUES ($1, $2, $3, 'payment', $4, $5, $6, 1, $7, $8, 'confirmed', $9, $9)
			`, paymentID, tenantID, paymentNumber, contactUUID, now, paymentAmount, reference, fmt.Sprintf("Payment for invoice %s", invoiceID.String()[:8]), now)

			if err != nil {
				h.log.Error("Failed to create payment record", "error", err)
			} else {
				h.log.Info("Payment record created", "payment_id", paymentID, "invoice_id", invoiceID)
			}
		}
	} else {
		h.log.Warn("Cannot create payment record: no vendor_id on invoice", "invoice_id", invoiceID)
	}

	// Create journal entry for payment: Debit AP, Credit Cash/Bank
	// Get account IDs
	var apAccountID sql.NullString
	h.db.QueryRow(`
		SELECT id FROM chart_of_accounts
		WHERE tenant_id = $1 AND account_type = 'liability'
		AND LOWER(name) LIKE '%payable%'
		AND deleted_at IS NULL
		LIMIT 1`, tenantID).Scan(&apAccountID)

	var cashAccountID sql.NullString
	h.db.QueryRow(`
		SELECT id FROM chart_of_accounts
		WHERE tenant_id = $1 AND account_type = 'asset'
		AND (LOWER(name) LIKE '%cash%' OR LOWER(name) LIKE '%bank%')
		AND deleted_at IS NULL
		LIMIT 1`, tenantID).Scan(&cashAccountID)

	// Create journal entry if we have both accounts
	if apAccountID.Valid && cashAccountID.Valid {
		entryID := uuid.New()
		entryNumber := fmt.Sprintf("PAY-%s", now.Format("20060102150405"))
		description := fmt.Sprintf("Payment for invoice %s", invoiceID.String()[:8])

		apAcctID, _ := uuid.Parse(apAccountID.String)
		cashAcctID, _ := uuid.Parse(cashAccountID.String)

		// For payment: Debit AP (reduce liability), Credit Cash (reduce asset)
		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, entry_number, entry_date, entry_type,
				debit_account_id, credit_account_id, amount,
				description, reference_type, reference_id, status,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'payment', $5, $6, $7, $8, 'purchase_invoice_payment', $9, 'posted', $10, $10)
		`, entryID, tenantID, entryNumber, now, apAcctID, cashAcctID,
			paymentAmount, description, invoiceID, now)

		if err != nil {
			h.log.Error("Failed to create journal entry for payment", "error", err)
		} else {
			h.log.Info("Journal entry created for payment", "entry_id", entryID, "invoice_id", invoiceID, "amount", paymentAmount)
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
		response.BadRequest(c, "Invalid input: "+err.Error())
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
		response.InternalError(c, "Failed to create debit note: "+err.Error())
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
	var totalAmount, taxAmount, dnSubtotal float64
	var dnDate time.Time

	err = h.db.QueryRow(`
		SELECT status, invoice_number, vendor_id, original_invoice_id, total_amount, tax_amount, subtotal, invoice_date
		FROM purchase_invoices
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND invoice_type = 'debit_note'`,
		debitNoteID, tenantID,
	).Scan(&currentStatus, &dnNumber, &vendorID, &originalInvoiceID, &totalAmount, &taxAmount, &dnSubtotal, &dnDate)
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
		FROM journals WHERE tenant_id = $1 AND code = 'PURCHASE' AND deleted_at IS NULL`,
		tenantID,
	).Scan(&purchaseJournalID, &nextNumber, &numberPrefix)

	if err == nil {
		var apAccountID, expenseAccountID, taxAccountID uuid.UUID
		tx.QueryRow(`SELECT id FROM accounts WHERE tenant_id = $1 AND code = '2000' AND deleted_at IS NULL`, tenantID).Scan(&apAccountID)
		tx.QueryRow(`SELECT id FROM accounts WHERE tenant_id = $1 AND code = '5000' AND deleted_at IS NULL`, tenantID).Scan(&expenseAccountID)
		tx.QueryRow(`SELECT id FROM accounts WHERE tenant_id = $1 AND code = '2100' AND deleted_at IS NULL`, tenantID).Scan(&taxAccountID)

		if apAccountID != uuid.Nil {
			prefix := ""
			if numberPrefix.Valid {
				prefix = numberPrefix.String
			}
			entryNumber := fmt.Sprintf("%s%06d", prefix, nextNumber)

			journalEntryID := uuid.New()
			description := fmt.Sprintf("Debit Note %s", dnNumber)

			_, err = tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'posted', $13, $14, $15)`,
				journalEntryID, tenantID, purchaseJournalID, entryNumber, dnDate, dnNumber, description,
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

			// Credit Expense/COGS (reversal)
			if expenseAccountID != uuid.Nil && dnSubtotal > 0 {
				lineID := uuid.New()
				tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					lineID, journalEntryID, lineNumber, expenseAccountID, "Expense Reversal (Debit Note)",
					0.0, dnSubtotal, 1.0, now,
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
			if expenseAccountID != uuid.Nil {
				tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", dnSubtotal, now, expenseAccountID)
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
