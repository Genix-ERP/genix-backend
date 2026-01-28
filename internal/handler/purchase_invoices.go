package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

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
			   c.name as vendor_name
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
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

		err := rows.Scan(
			&id, &tenantIDScan, &invoiceNumber, &vendorID, &vendorInvoiceNumber,
			&invoiceDate, &dueDate, &subtotal, &discountAmount,
			&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
			&threeWayMatchStatus, &notes, &createdAt, &updatedAt,
			&vendorName,
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
			   c.name as vendor_name
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.id = $1 AND pi.tenant_id = $2 AND pi.deleted_at IS NULL`

	var id, tenantIDScan, vendorID uuid.UUID
	var invoiceNumber, status, threeWayMatchStatus string
	var vendorInvoiceNumber, notes, vendorName sql.NullString
	var invoiceDate, dueDate time.Time
	var subtotal, discountAmount, taxAmount, totalAmount, amountPaid, amountDue float64
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(query, invoiceID, tenantID).Scan(
		&id, &tenantIDScan, &invoiceNumber, &vendorID, &vendorInvoiceNumber,
		&invoiceDate, &dueDate, &subtotal, &discountAmount,
		&taxAmount, &totalAmount, &amountPaid, &amountDue, &status,
		&threeWayMatchStatus, &notes, &createdAt, &updatedAt,
		&vendorName,
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

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE purchase_invoices SET amount_paid = $1, status = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		newAmountPaid, newStatus, now, invoiceID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to record payment")
		return
	}

	h.GetPurchaseInvoice(c)
}
