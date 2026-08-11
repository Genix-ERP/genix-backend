package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// =====================================================
// FOLLOW-UP LEVEL HANDLERS
// =====================================================

type FollowupLevel struct {
	ID           uuid.UUID `json:"id"`
	TenantID     uuid.UUID `json:"tenant_id"`
	Name         string    `json:"name"`
	Sequence     int       `json:"sequence"`
	DelayDays    int       `json:"delay_days"`
	SendEmail    bool      `json:"send_email"`
	SendSMS      bool      `json:"send_sms"`
	SendLetter   bool      `json:"send_letter"`
	EmailSubject *string   `json:"email_subject,omitempty"`
	EmailBody    *string   `json:"email_body,omitempty"`
	SMSBody      *string   `json:"sms_body,omitempty"`
	LetterBody   *string   `json:"letter_body,omitempty"`
	BlockSales   bool      `json:"block_sales"`
	AddLateFee   bool      `json:"add_late_fee"`
	LateFeeType  *string   `json:"late_fee_type,omitempty"`
	LateFeeValue float64   `json:"late_fee_value"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type FollowupLevelRequest struct {
	Name         string  `json:"name" binding:"required"`
	Sequence     int     `json:"sequence"`
	DelayDays    int     `json:"delay_days"`
	SendEmail    bool    `json:"send_email"`
	SendSMS      bool    `json:"send_sms"`
	SendLetter   bool    `json:"send_letter"`
	EmailSubject *string `json:"email_subject,omitempty"`
	EmailBody    *string `json:"email_body,omitempty"`
	SMSBody      *string `json:"sms_body,omitempty"`
	LetterBody   *string `json:"letter_body,omitempty"`
	BlockSales   bool    `json:"block_sales"`
	AddLateFee   bool    `json:"add_late_fee"`
	LateFeeType  *string `json:"late_fee_type,omitempty"`
	LateFeeValue float64 `json:"late_fee_value"`
	IsActive     *bool   `json:"is_active,omitempty"`
}

// ListFollowupLevels returns all follow-up levels for the tenant
func (h *Handler) ListFollowupLevels(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, name, sequence, delay_days,
		       send_email, send_sms, send_letter,
		       email_subject, email_body, sms_body, letter_body,
		       block_sales, add_late_fee, late_fee_type, late_fee_value,
		       is_active, created_at, updated_at
		FROM followup_levels
		WHERE tenant_id = $1
		ORDER BY sequence ASC
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to query followup levels", "error", err)
		response.InternalServerError(c, "Failed to query followup levels")
		return
	}
	defer rows.Close()

	var levels []FollowupLevel
	for rows.Next() {
		var l FollowupLevel
		var emailSubject, emailBody, smsBody, letterBody, lateFeeType sql.NullString

		if err := rows.Scan(
			&l.ID, &l.TenantID, &l.Name, &l.Sequence, &l.DelayDays,
			&l.SendEmail, &l.SendSMS, &l.SendLetter,
			&emailSubject, &emailBody, &smsBody, &letterBody,
			&l.BlockSales, &l.AddLateFee, &lateFeeType, &l.LateFeeValue,
			&l.IsActive, &l.CreatedAt, &l.UpdatedAt,
		); err != nil {
			h.log.Error("Failed to scan followup level", "error", err)
			continue
		}

		if emailSubject.Valid {
			l.EmailSubject = &emailSubject.String
		}
		if emailBody.Valid {
			l.EmailBody = &emailBody.String
		}
		if smsBody.Valid {
			l.SMSBody = &smsBody.String
		}
		if letterBody.Valid {
			l.LetterBody = &letterBody.String
		}
		if lateFeeType.Valid {
			l.LateFeeType = &lateFeeType.String
		}

		levels = append(levels, l)
	}

	response.Success(c, levels)
}

// CreateFollowupLevel creates a new follow-up level
func (h *Handler) CreateFollowupLevel(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var req FollowupLevelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	// Auto-assign sequence if not provided
	if req.Sequence == 0 {
		var maxSeq sql.NullInt64
		h.db.QueryRow("SELECT MAX(sequence) FROM followup_levels WHERE tenant_id = $1", tenantID).Scan(&maxSeq)
		if maxSeq.Valid {
			req.Sequence = int(maxSeq.Int64) + 1
		} else {
			req.Sequence = 1
		}
	}

	var id uuid.UUID
	var createdAt, updatedAt time.Time

	err := h.db.QueryRow(`
		INSERT INTO followup_levels (
			tenant_id, name, sequence, delay_days,
			send_email, send_sms, send_letter,
			email_subject, email_body, sms_body, letter_body,
			block_sales, add_late_fee, late_fee_type, late_fee_value,
			is_active
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, created_at, updated_at
	`,
		tenantID, req.Name, req.Sequence, req.DelayDays,
		req.SendEmail, req.SendSMS, req.SendLetter,
		req.EmailSubject, req.EmailBody, req.SMSBody, req.LetterBody,
		req.BlockSales, req.AddLateFee, req.LateFeeType, req.LateFeeValue,
		isActive,
	).Scan(&id, &createdAt, &updatedAt)

	if err != nil {
		h.log.Error("Failed to create followup level", "error", err)
		response.InternalServerError(c, "Failed to create followup level")
		return
	}

	level := FollowupLevel{
		ID:           id,
		TenantID:     tenantID,
		Name:         req.Name,
		Sequence:     req.Sequence,
		DelayDays:    req.DelayDays,
		SendEmail:    req.SendEmail,
		SendSMS:      req.SendSMS,
		SendLetter:   req.SendLetter,
		EmailSubject: req.EmailSubject,
		EmailBody:    req.EmailBody,
		SMSBody:      req.SMSBody,
		LetterBody:   req.LetterBody,
		BlockSales:   req.BlockSales,
		AddLateFee:   req.AddLateFee,
		LateFeeType:  req.LateFeeType,
		LateFeeValue: req.LateFeeValue,
		IsActive:     isActive,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}

	response.Created(c, level)
}

// UpdateFollowupLevel updates an existing follow-up level
func (h *Handler) UpdateFollowupLevel(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req FollowupLevelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	var updatedAt time.Time
	err = h.db.QueryRow(`
		UPDATE followup_levels SET
			name = $3, sequence = $4, delay_days = $5,
			send_email = $6, send_sms = $7, send_letter = $8,
			email_subject = $9, email_body = $10, sms_body = $11, letter_body = $12,
			block_sales = $13, add_late_fee = $14, late_fee_type = $15, late_fee_value = $16,
			is_active = COALESCE($17, is_active),
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND tenant_id = $2
		RETURNING updated_at
	`,
		id, tenantID,
		req.Name, req.Sequence, req.DelayDays,
		req.SendEmail, req.SendSMS, req.SendLetter,
		req.EmailSubject, req.EmailBody, req.SMSBody, req.LetterBody,
		req.BlockSales, req.AddLateFee, req.LateFeeType, req.LateFeeValue,
		req.IsActive,
	).Scan(&updatedAt)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Followup level not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to update followup level", "error", err)
		response.InternalServerError(c, "Failed to update followup level")
		return
	}

	response.Success(c, gin.H{"message": "Followup level updated", "updated_at": updatedAt})
}

// DeleteFollowupLevel deletes a follow-up level
func (h *Handler) DeleteFollowupLevel(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	result, err := h.db.Exec("DELETE FROM followup_levels WHERE id = $1 AND tenant_id = $2", id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete followup level", "error", err)
		response.InternalServerError(c, "Failed to delete followup level")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Followup level not found")
		return
	}

	response.Success(c, gin.H{"message": "Followup level deleted"})
}

// =====================================================
// CUSTOMER FOLLOW-UP STATUS HANDLERS
// =====================================================

type CustomerFollowupStatus struct {
	ID                   uuid.UUID  `json:"id"`
	TenantID             uuid.UUID  `json:"tenant_id"`
	CustomerID           uuid.UUID  `json:"customer_id"`
	CustomerName         string     `json:"customer_name"`
	CustomerEmail        *string    `json:"customer_email,omitempty"`
	CustomerPhone        *string    `json:"customer_phone,omitempty"`
	CurrentLevelID       *uuid.UUID `json:"current_level_id,omitempty"`
	CurrentLevelName     *string    `json:"current_level_name,omitempty"`
	CurrentLevelSequence int        `json:"current_level_sequence"`
	TotalOverdue         float64    `json:"total_overdue"`
	OldestDueDate        *time.Time `json:"oldest_due_date,omitempty"`
	DaysOverdue          int        `json:"days_overdue"`
	Status               string     `json:"status"`
	LastFollowupDate     *time.Time `json:"last_followup_date,omitempty"`
	NextFollowupDate     *time.Time `json:"next_followup_date,omitempty"`
	InternalNotes        *string    `json:"internal_notes,omitempty"`
	OverdueInvoiceCount  int        `json:"overdue_invoice_count"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type CustomerFollowupFilter struct {
	Status     string   `form:"status"`
	Search     string   `form:"search"`
	MinOverdue *float64 `form:"min_overdue"`
	LevelID    string   `form:"level_id"`
}

// ListCustomerFollowups returns all customers with overdue amounts
func (h *Handler) ListCustomerFollowups(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var filter CustomerFollowupFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid filter parameters")
		return
	}

	// First, update/calculate customer followup status from invoices
	_, err := h.db.Exec(`
		INSERT INTO customer_followup_status (tenant_id, customer_id, total_overdue, oldest_due_date, days_overdue, status)
		SELECT
			i.tenant_id,
			i.customer_id,
			SUM(i.total_amount - COALESCE(i.amount_paid, 0)) as total_overdue,
			MIN(i.due_date) as oldest_due_date,
			-- date - date is already an INTEGER number of days in Postgres, so
			-- EXTRACT(DAY FROM ...) over it fails outright:
			--   pq: function pg_catalog.extract(unknown, integer) does not exist
			-- This statement therefore never ran, and customer_followup_status
			-- was never refreshed — the dunning screen showed whatever was last
			-- written by hand, or nothing.
			(CURRENT_DATE - MIN(i.due_date))::INTEGER as days_overdue,
			'in_followup' as status
		FROM sales_invoices i
		WHERE i.tenant_id = $1
		  AND i.deleted_at IS NULL
		  AND i.status NOT IN ('draft', 'cancelled', 'void')
		  AND i.due_date < CURRENT_DATE
		  AND i.total_amount > COALESCE(i.amount_paid, 0)
		GROUP BY i.tenant_id, i.customer_id
		ON CONFLICT (tenant_id, customer_id)
		DO UPDATE SET
			total_overdue = EXCLUDED.total_overdue,
			oldest_due_date = EXCLUDED.oldest_due_date,
			days_overdue = EXCLUDED.days_overdue,
			status = CASE WHEN EXCLUDED.total_overdue > 0 THEN 'in_followup' ELSE 'ok' END,
			updated_at = CURRENT_TIMESTAMP
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to update customer followup status", "error", err)
	}

	paginate, page, pageSize, offset := optPagination(c)

	// Shared FROM + WHERE so the COUNT mirrors the list filters exactly.
	fromWhere := `
		FROM customer_followup_status cfs
		JOIN contacts c ON c.id = cfs.customer_id
		LEFT JOIN followup_levels fl ON fl.id = cfs.current_level_id
		WHERE cfs.tenant_id = $1 AND cfs.total_overdue > 0`
	whereExtra := ""
	args := []interface{}{tenantID}
	argCount := 1

	if filter.Status != "" && filter.Status != "all" {
		argCount++
		whereExtra += fmt.Sprintf(" AND cfs.status = $%d", argCount)
		args = append(args, filter.Status)
	}

	if filter.Search != "" {
		argCount++
		whereExtra += fmt.Sprintf(" AND (c.name ILIKE $%d OR c.email ILIKE $%d)", argCount, argCount)
		args = append(args, "%"+filter.Search+"%")
	}

	if filter.MinOverdue != nil {
		argCount++
		whereExtra += fmt.Sprintf(" AND cfs.total_overdue >= $%d", argCount)
		args = append(args, *filter.MinOverdue)
	}

	if filter.LevelID != "" {
		argCount++
		whereExtra += fmt.Sprintf(" AND cfs.current_level_id = $%d", argCount)
		args = append(args, filter.LevelID)
	}

	query := `
		SELECT
			cfs.id, cfs.tenant_id, cfs.customer_id,
			c.name as customer_name, c.email as customer_email, c.phone as customer_phone,
			cfs.current_level_id, fl.name as current_level_name, cfs.current_level_sequence,
			cfs.total_overdue, cfs.oldest_due_date, cfs.days_overdue, cfs.status,
			cfs.last_followup_date, cfs.next_followup_date, cfs.internal_notes,
			(SELECT COUNT(*) FROM sales_invoices WHERE customer_id = cfs.customer_id AND deleted_at IS NULL AND status NOT IN ('draft', 'cancelled', 'void') AND due_date < CURRENT_DATE AND total_amount > COALESCE(amount_paid, 0)) as overdue_invoice_count,
			cfs.created_at, cfs.updated_at` + fromWhere + whereExtra +
		// cfs.id is the tiebreaker. days_overdue and total_overdue are both
		// non-unique, so without it the order is not total and Postgres may
		// resolve equal rows differently for page 1's LIMIT/OFFSET than for
		// page 2's — one customer appears on both pages, another on neither,
		// with no error and nothing in the log.
		" ORDER BY cfs.days_overdue DESC, cfs.total_overdue DESC, cfs.id ASC"

	if paginate {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query customer followups", "error", err)
		response.InternalServerError(c, "Failed to query customer followups")
		return
	}
	defer rows.Close()

	var followups []CustomerFollowupStatus
	for rows.Next() {
		var f CustomerFollowupStatus
		var customerEmail, customerPhone, currentLevelID, currentLevelName, internalNotes sql.NullString
		var oldestDueDate, lastFollowupDate, nextFollowupDate pq.NullTime

		if err := rows.Scan(
			&f.ID, &f.TenantID, &f.CustomerID,
			&f.CustomerName, &customerEmail, &customerPhone,
			&currentLevelID, &currentLevelName, &f.CurrentLevelSequence,
			&f.TotalOverdue, &oldestDueDate, &f.DaysOverdue, &f.Status,
			&lastFollowupDate, &nextFollowupDate, &internalNotes,
			&f.OverdueInvoiceCount, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			h.log.Error("Failed to scan customer followup", "error", err)
			continue
		}

		if customerEmail.Valid {
			f.CustomerEmail = &customerEmail.String
		}
		if customerPhone.Valid {
			f.CustomerPhone = &customerPhone.String
		}
		if currentLevelID.Valid {
			uid, _ := uuid.Parse(currentLevelID.String)
			f.CurrentLevelID = &uid
		}
		if currentLevelName.Valid {
			f.CurrentLevelName = &currentLevelName.String
		}
		if oldestDueDate.Valid {
			f.OldestDueDate = &oldestDueDate.Time
		}
		if lastFollowupDate.Valid {
			f.LastFollowupDate = &lastFollowupDate.Time
		}
		if nextFollowupDate.Valid {
			f.NextFollowupDate = &nextFollowupDate.Time
		}
		if internalNotes.Valid {
			f.InternalNotes = &internalNotes.String
		}

		followups = append(followups, f)
	}

	if !paginate {
		response.Success(c, followups)
		return
	}

	var total int
	_ = h.db.QueryRow("SELECT COUNT(*)"+fromWhere+whereExtra, args[:argCount]...).Scan(&total)
	response.Paginated(c, followups, page, pageSize, total)
}

// GetCustomerFollowupDetails returns detailed followup info for a customer
func (h *Handler) GetCustomerFollowupDetails(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	customerIDStr := c.Param("customer_id")
	customerID, err := uuid.Parse(customerIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid customer ID")
		return
	}

	// Get customer followup status
	var status CustomerFollowupStatus
	var customerEmail, customerPhone, currentLevelID, currentLevelName, internalNotes sql.NullString
	var oldestDueDate, lastFollowupDate, nextFollowupDate pq.NullTime

	err = h.db.QueryRow(`
		SELECT
			cfs.id, cfs.tenant_id, cfs.customer_id,
			c.name, c.email, c.phone,
			cfs.current_level_id, fl.name, cfs.current_level_sequence,
			cfs.total_overdue, cfs.oldest_due_date, cfs.days_overdue, cfs.status,
			cfs.last_followup_date, cfs.next_followup_date, cfs.internal_notes,
			cfs.created_at, cfs.updated_at
		FROM customer_followup_status cfs
		JOIN contacts c ON c.id = cfs.customer_id
		LEFT JOIN followup_levels fl ON fl.id = cfs.current_level_id
		WHERE cfs.tenant_id = $1 AND cfs.customer_id = $2
	`, tenantID, customerID).Scan(
		&status.ID, &status.TenantID, &status.CustomerID,
		&status.CustomerName, &customerEmail, &customerPhone,
		&currentLevelID, &currentLevelName, &status.CurrentLevelSequence,
		&status.TotalOverdue, &oldestDueDate, &status.DaysOverdue, &status.Status,
		&lastFollowupDate, &nextFollowupDate, &internalNotes,
		&status.CreatedAt, &status.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Customer followup status not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get customer followup details", "error", err)
		response.InternalServerError(c, "Failed to get customer followup details")
		return
	}

	if customerEmail.Valid {
		status.CustomerEmail = &customerEmail.String
	}
	if customerPhone.Valid {
		status.CustomerPhone = &customerPhone.String
	}
	if currentLevelID.Valid {
		uid, _ := uuid.Parse(currentLevelID.String)
		status.CurrentLevelID = &uid
	}
	if currentLevelName.Valid {
		status.CurrentLevelName = &currentLevelName.String
	}
	if oldestDueDate.Valid {
		status.OldestDueDate = &oldestDueDate.Time
	}
	if lastFollowupDate.Valid {
		status.LastFollowupDate = &lastFollowupDate.Time
	}
	if nextFollowupDate.Valid {
		status.NextFollowupDate = &nextFollowupDate.Time
	}
	if internalNotes.Valid {
		status.InternalNotes = &internalNotes.String
	}

	// Get overdue invoices
	invoiceRows, err := h.db.Query(`
		SELECT id, invoice_number, invoice_date, due_date, total_amount, COALESCE(amount_paid, 0), total_amount - COALESCE(amount_paid, 0) as balance
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL AND customer_id = $2 AND status NOT IN ('draft', 'cancelled', 'void') AND due_date < CURRENT_DATE AND total_amount > COALESCE(amount_paid, 0)
		ORDER BY due_date ASC
	`, tenantID, customerID)
	if err != nil {
		h.log.Error("Failed to get overdue invoices", "error", err)
	}
	defer invoiceRows.Close()

	type OverdueInvoice struct {
		ID            uuid.UUID `json:"id"`
		InvoiceNumber string    `json:"invoice_number"`
		IssueDate     time.Time `json:"issue_date"`
		DueDate       time.Time `json:"due_date"`
		Total         float64   `json:"total"`
		PaidAmount    float64   `json:"paid_amount"`
		Balance       float64   `json:"balance"`
	}

	var overdueInvoices []OverdueInvoice
	for invoiceRows.Next() {
		var inv OverdueInvoice
		invoiceRows.Scan(&inv.ID, &inv.InvoiceNumber, &inv.IssueDate, &inv.DueDate, &inv.Total, &inv.PaidAmount, &inv.Balance)
		overdueInvoices = append(overdueInvoices, inv)
	}

	// Get action history
	actionRows, err := h.db.Query(`
		SELECT id, level_name, action_type, total_amount, description, recipient, subject, status, performed_at
		FROM followup_actions
		WHERE tenant_id = $1 AND customer_id = $2
		ORDER BY performed_at DESC
		LIMIT 20
	`, tenantID, customerID)
	if err != nil {
		h.log.Error("Failed to get followup actions", "error", err)
	}
	defer actionRows.Close()

	type FollowupActionSummary struct {
		ID          uuid.UUID `json:"id"`
		LevelName   *string   `json:"level_name,omitempty"`
		ActionType  string    `json:"action_type"`
		TotalAmount *float64  `json:"total_amount,omitempty"`
		Description *string   `json:"description,omitempty"`
		Recipient   *string   `json:"recipient,omitempty"`
		Subject     *string   `json:"subject,omitempty"`
		Status      string    `json:"status"`
		PerformedAt time.Time `json:"performed_at"`
	}

	var actions []FollowupActionSummary
	for actionRows.Next() {
		var a FollowupActionSummary
		var levelName, description, recipient, subject sql.NullString
		var totalAmount sql.NullFloat64
		actionRows.Scan(&a.ID, &levelName, &a.ActionType, &totalAmount, &description, &recipient, &subject, &a.Status, &a.PerformedAt)
		if levelName.Valid {
			a.LevelName = &levelName.String
		}
		if totalAmount.Valid {
			a.TotalAmount = &totalAmount.Float64
		}
		if description.Valid {
			a.Description = &description.String
		}
		if recipient.Valid {
			a.Recipient = &recipient.String
		}
		if subject.Valid {
			a.Subject = &subject.String
		}
		actions = append(actions, a)
	}

	// Get payment promises
	promiseRows, err := h.db.Query(`
		SELECT id, promised_date, promised_amount, status, notes, created_at
		FROM payment_promises
		WHERE tenant_id = $1 AND customer_id = $2
		ORDER BY created_at DESC
		LIMIT 10
	`, tenantID, customerID)
	if err != nil {
		h.log.Error("Failed to get payment promises", "error", err)
	}
	defer promiseRows.Close()

	type PaymentPromiseSummary struct {
		ID             uuid.UUID `json:"id"`
		PromisedDate   time.Time `json:"promised_date"`
		PromisedAmount float64   `json:"promised_amount"`
		Status         string    `json:"status"`
		Notes          *string   `json:"notes,omitempty"`
		CreatedAt      time.Time `json:"created_at"`
	}

	var promises []PaymentPromiseSummary
	for promiseRows.Next() {
		var p PaymentPromiseSummary
		var notes sql.NullString
		promiseRows.Scan(&p.ID, &p.PromisedDate, &p.PromisedAmount, &p.Status, &notes, &p.CreatedAt)
		if notes.Valid {
			p.Notes = &notes.String
		}
		promises = append(promises, p)
	}

	response.Success(c, gin.H{
		"status":           status,
		"overdue_invoices": overdueInvoices,
		"actions":          actions,
		"promises":         promises,
	})
}

// =====================================================
// FOLLOW-UP ACTION HANDLERS
// =====================================================

type FollowupActionRequest struct {
	CustomerID  uuid.UUID   `json:"customer_id" binding:"required"`
	ActionType  string      `json:"action_type" binding:"required"` // email_sent, sms_sent, manual_call, note, payment_promise
	LevelID     *uuid.UUID  `json:"level_id,omitempty"`
	InvoiceIDs  []uuid.UUID `json:"invoice_ids,omitempty"`
	TotalAmount *float64    `json:"total_amount,omitempty"`
	Description *string     `json:"description,omitempty"`
	Recipient   *string     `json:"recipient,omitempty"`
	Subject     *string     `json:"subject,omitempty"`
	Body        *string     `json:"body,omitempty"`
}

// CreateFollowupAction records a follow-up action
func (h *Handler) CreateFollowupAction(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req FollowupActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Get level name if level_id provided
	var levelName sql.NullString
	if req.LevelID != nil {
		h.db.QueryRow("SELECT name FROM followup_levels WHERE id = $1", req.LevelID).Scan(&levelName)
	}

	var id uuid.UUID
	var performedAt time.Time

	err := h.db.QueryRow(`
		INSERT INTO followup_actions (
			tenant_id, customer_id, level_id, level_name, action_type,
			invoice_ids, total_amount, description, recipient, subject, body,
			status, performed_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'sent', $12)
		RETURNING id, performed_at
	`,
		tenantID, req.CustomerID, req.LevelID, levelName, req.ActionType,
		pq.Array(req.InvoiceIDs), req.TotalAmount, req.Description, req.Recipient, req.Subject, req.Body,
		userID,
	).Scan(&id, &performedAt)

	if err != nil {
		h.log.Error("Failed to create followup action", "error", err)
		response.InternalServerError(c, "Failed to create followup action")
		return
	}

	// Update customer followup status
	h.db.Exec(`
		UPDATE customer_followup_status SET
			last_followup_date = CURRENT_TIMESTAMP,
			current_level_id = COALESCE($3, current_level_id),
			updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $1 AND customer_id = $2
	`, tenantID, req.CustomerID, req.LevelID)

	response.Created(c, gin.H{
		"id":           id,
		"performed_at": performedAt,
		"message":      "Follow-up action recorded",
	})
}

// =====================================================
// PAYMENT PROMISE HANDLERS
// =====================================================

type PaymentPromiseRequest struct {
	CustomerID     uuid.UUID   `json:"customer_id" binding:"required"`
	PromisedDate   string      `json:"promised_date" binding:"required"`
	PromisedAmount float64     `json:"promised_amount" binding:"required"`
	InvoiceIDs     []uuid.UUID `json:"invoice_ids,omitempty"`
	Notes          *string     `json:"notes,omitempty"`
}

// CreatePaymentPromise records a payment promise
func (h *Handler) CreatePaymentPromise(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req PaymentPromiseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	promisedDate, err := time.Parse("2006-01-02", req.PromisedDate)
	if err != nil {
		response.BadRequest(c, "Invalid promised_date format, use YYYY-MM-DD")
		return
	}

	var id uuid.UUID
	var createdAt time.Time

	err = h.db.QueryRow(`
		INSERT INTO payment_promises (tenant_id, customer_id, promised_date, promised_amount, invoice_ids, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`,
		tenantID, req.CustomerID, promisedDate, req.PromisedAmount, pq.Array(req.InvoiceIDs), req.Notes, userID,
	).Scan(&id, &createdAt)

	if err != nil {
		h.log.Error("Failed to create payment promise", "error", err)
		response.InternalServerError(c, "Failed to create payment promise")
		return
	}

	// Also record as followup action
	h.db.Exec(`
		INSERT INTO followup_actions (tenant_id, customer_id, action_type, total_amount, description, performed_by)
		VALUES ($1, $2, 'payment_promise', $3, $4, $5)
	`, tenantID, req.CustomerID, req.PromisedAmount, fmt.Sprintf("Payment promise for %s", req.PromisedDate), userID)

	response.Created(c, gin.H{
		"id":         id,
		"created_at": createdAt,
		"message":    "Payment promise recorded",
	})
}

// UpdatePaymentPromiseStatus updates the status of a payment promise
func (h *Handler) UpdatePaymentPromiseStatus(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"` // kept, broken, cancelled
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	_, err = h.db.Exec(`
		UPDATE payment_promises SET
			status = $3,
			resolved_at = CURRENT_TIMESTAMP,
			resolved_by = $4,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID, req.Status, userID)

	if err != nil {
		h.log.Error("Failed to update payment promise status", "error", err)
		response.InternalServerError(c, "Failed to update payment promise status")
		return
	}

	response.Success(c, gin.H{"message": "Payment promise status updated"})
}

// =====================================================
// FOLLOW-UP SUMMARY/STATS
// =====================================================

// GetFollowupSummary returns summary statistics for follow-ups
func (h *Handler) GetFollowupSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var summary struct {
		TotalCustomersOverdue int     `json:"total_customers_overdue"`
		TotalOverdueAmount    float64 `json:"total_overdue_amount"`
		OverdueInvoiceCount   int     `json:"overdue_invoice_count"`
		CustomersByLevel      []struct {
			LevelName string  `json:"level_name"`
			Count     int     `json:"count"`
			Amount    float64 `json:"amount"`
		} `json:"customers_by_level"`
		RecentActions   int `json:"recent_actions"`
		PendingPromises int `json:"pending_promises"`
	}

	// Total overdue
	h.db.QueryRow(`
		SELECT COUNT(DISTINCT customer_id), COALESCE(SUM(total_amount - COALESCE(amount_paid, 0)), 0), COUNT(*)
		FROM sales_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL AND status NOT IN ('draft', 'cancelled', 'void') AND due_date < CURRENT_DATE AND total_amount > COALESCE(amount_paid, 0)
	`, tenantID).Scan(&summary.TotalCustomersOverdue, &summary.TotalOverdueAmount, &summary.OverdueInvoiceCount)

	// Recent actions (last 7 days)
	h.db.QueryRow(`
		SELECT COUNT(*) FROM followup_actions WHERE tenant_id = $1 AND performed_at > CURRENT_DATE - INTERVAL '7 days'
	`, tenantID).Scan(&summary.RecentActions)

	// Pending promises
	h.db.QueryRow(`
		SELECT COUNT(*) FROM payment_promises WHERE tenant_id = $1 AND status = 'pending' AND promised_date >= CURRENT_DATE
	`, tenantID).Scan(&summary.PendingPromises)

	response.Success(c, summary)
}

// SendFollowupReminder sends a follow-up reminder to a customer
func (h *Handler) SendFollowupReminder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req struct {
		CustomerID uuid.UUID `json:"customer_id" binding:"required"`
		LevelID    uuid.UUID `json:"level_id" binding:"required"`
		SendEmail  bool      `json:"send_email"`
		SendSMS    bool      `json:"send_sms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Get level details
	var level FollowupLevel
	var emailSubject, emailBody sql.NullString
	err := h.db.QueryRow(`
		SELECT id, name, email_subject, email_body FROM followup_levels WHERE id = $1 AND tenant_id = $2
	`, req.LevelID, tenantID).Scan(&level.ID, &level.Name, &emailSubject, &emailBody)
	if err != nil {
		response.NotFound(c, "Follow-up level not found")
		return
	}

	// Get customer details
	var customerName, customerEmail string
	var totalOverdue float64
	var daysOverdue int
	err = h.db.QueryRow(`
		SELECT c.name, COALESCE(c.email, ''), cfs.total_overdue, cfs.days_overdue
		FROM contacts c
		JOIN customer_followup_status cfs ON cfs.customer_id = c.id
		WHERE c.id = $1 AND c.tenant_id = $2
	`, req.CustomerID, tenantID).Scan(&customerName, &customerEmail, &totalOverdue, &daysOverdue)
	if err != nil {
		response.NotFound(c, "Customer not found")
		return
	}

	actionsRecorded := 0

	if req.SendEmail && emailSubject.Valid && customerEmail != "" {
		// Record email action (actual sending would be handled by email service)
		h.db.Exec(`
			INSERT INTO followup_actions (tenant_id, customer_id, level_id, level_name, action_type, total_amount, recipient, subject, body, status, performed_by)
			VALUES ($1, $2, $3, $4, 'email_sent', $5, $6, $7, $8, 'sent', $9)
		`, tenantID, req.CustomerID, req.LevelID, level.Name, totalOverdue, customerEmail, emailSubject.String, emailBody.String, userID)
		actionsRecorded++
	}

	if req.SendSMS {
		// Record SMS action
		h.db.Exec(`
			INSERT INTO followup_actions (tenant_id, customer_id, level_id, level_name, action_type, total_amount, status, performed_by)
			VALUES ($1, $2, $3, $4, 'sms_sent', $5, 'sent', $6)
		`, tenantID, req.CustomerID, req.LevelID, level.Name, totalOverdue, userID)
		actionsRecorded++
	}

	// Update customer followup status
	h.db.Exec(`
		UPDATE customer_followup_status SET
			current_level_id = $3,
			current_level_sequence = (SELECT sequence FROM followup_levels WHERE id = $3),
			last_followup_date = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $1 AND customer_id = $2
	`, tenantID, req.CustomerID, req.LevelID)

	response.Success(c, gin.H{
		"message":          "Follow-up reminder sent",
		"actions_recorded": actionsRecorded,
	})
}
