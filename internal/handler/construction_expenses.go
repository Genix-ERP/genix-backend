package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION EXPENSE LINES HANDLERS (Xarajat operatsiyasi)
// =====================================================

type expenseLine struct {
	ID                  string   `json:"id"`
	TenantID            string   `json:"tenant_id"`
	ProjectID           int64    `json:"project_id"`
	StageID             *int64   `json:"stage_id"`
	StageName           *string  `json:"stage_name"`
	WbsID               *int64   `json:"wbs_id"`
	CostCategoryID      *int64   `json:"cost_category_id"`
	CostCategoryName    *string  `json:"cost_category_name"`
	ExpenseDate         string   `json:"expense_date"`
	Description         string   `json:"description"`
	ProductID           *string  `json:"product_id"`
	ProductName         *string  `json:"product_name"`
	Quantity            *float64 `json:"quantity"`
	Uom                 *string  `json:"uom"`
	UnitPrice           *float64 `json:"unit_price"`
	Amount              float64  `json:"amount"`
	CurrencyCode        string   `json:"currency_code"`
	VendorID            *string  `json:"vendor_id"`
	VendorName          *string  `json:"vendor_name"`
	DebitAccountID      *string  `json:"debit_account_id"`
	DebitAccountCode    *string  `json:"debit_account_code"`
	CreditAccountID     *string  `json:"credit_account_id"`
	CreditAccountCode   *string  `json:"credit_account_code"`
	AnalyticAccountID   *string  `json:"analytic_account_id"`
	JournalEntryID      *string  `json:"journal_entry_id"`
	DocumentURL         *string  `json:"document_url"`
	Status              string   `json:"status"`
	ApprovedBy          *string  `json:"approved_by"`
	ApprovedAt          *string  `json:"approved_at"`
	CancelledReason     *string  `json:"cancelled_reason"`
	CreatedBy           *string  `json:"created_by"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ListExpenseLines returns expense lines for a project
func (h *Handler) ListExpenseLines(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	query := `
		SELECT el.id, el.tenant_id, el.project_id,
		       el.stage_id, s.name,
		       el.wbs_id, el.cost_category_id, cat.name,
		       el.expense_date, el.description,
		       el.product_id, p.name,
		       el.quantity, el.uom, el.unit_price,
		       el.amount, el.currency_code,
		       el.vendor_id, v.name,
		       el.debit_account_id,  da.code,
		       el.credit_account_id, ca.code,
		       el.analytic_account_id,
		       el.journal_entry_id,
		       el.document_url, el.status,
		       el.approved_by, el.approved_at,
		       el.cancelled_reason, el.created_by,
		       el.created_at, el.updated_at
		FROM construction_expense_lines el
		LEFT JOIN construction_stages s ON s.id = el.stage_id
		LEFT JOIN construction_cost_categories cat ON cat.id = el.cost_category_id
		LEFT JOIN products p ON p.id = el.product_id
		LEFT JOIN contacts v ON v.id = el.vendor_id
		LEFT JOIN accounts da ON da.id = el.debit_account_id
		LEFT JOIN accounts ca ON ca.id = el.credit_account_id
		WHERE el.project_id = $1 AND el.tenant_id = $2 AND el.deleted_at IS NULL
	`

	args := []interface{}{projectID, tenantID}
	argCount := 2

	if stageIDStr := c.Query("stage_id"); stageIDStr != "" {
		argCount++
		stageID, _ := strconv.ParseInt(stageIDStr, 10, 64)
		query += fmt.Sprintf(" AND el.stage_id = $%d", argCount)
		args = append(args, stageID)
	}
	if catIDStr := c.Query("category_id"); catIDStr != "" {
		argCount++
		catID, _ := strconv.ParseInt(catIDStr, 10, 64)
		query += fmt.Sprintf(" AND el.cost_category_id = $%d", argCount)
		args = append(args, catID)
	}
	if status := c.Query("status"); status != "" {
		argCount++
		query += fmt.Sprintf(" AND el.status = $%d", argCount)
		args = append(args, status)
	}
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		query += fmt.Sprintf(" AND el.expense_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		query += fmt.Sprintf(" AND el.expense_date <= $%d", argCount)
		args = append(args, dateTo)
	}
	if vendorIDStr := c.Query("vendor_id"); vendorIDStr != "" {
		argCount++
		vendorID, _ := uuid.Parse(vendorIDStr)
		query += fmt.Sprintf(" AND el.vendor_id = $%d", argCount)
		args = append(args, vendorID)
	}

	query += " ORDER BY el.expense_date DESC, el.created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list expense lines", "error", err)
		response.InternalError(c, "Failed to list expense lines")
		return
	}
	defer rows.Close()

	lines := []expenseLine{}
	for rows.Next() {
		var el expenseLine
		if err := rows.Scan(
			&el.ID, &el.TenantID, &el.ProjectID,
			&el.StageID, &el.StageName,
			&el.WbsID, &el.CostCategoryID, &el.CostCategoryName,
			&el.ExpenseDate, &el.Description,
			&el.ProductID, &el.ProductName,
			&el.Quantity, &el.Uom, &el.UnitPrice,
			&el.Amount, &el.CurrencyCode,
			&el.VendorID, &el.VendorName,
			&el.DebitAccountID, &el.DebitAccountCode,
			&el.CreditAccountID, &el.CreditAccountCode,
			&el.AnalyticAccountID,
			&el.JournalEntryID,
			&el.DocumentURL, &el.Status,
			&el.ApprovedBy, &el.ApprovedAt,
			&el.CancelledReason, &el.CreatedBy,
			&el.CreatedAt, &el.UpdatedAt,
		); err != nil {
			h.log.Error("Failed to scan expense line", "error", err)
			continue
		}
		lines = append(lines, el)
	}

	// Compute totals
	var totalApproved, totalDraft float64
	for _, l := range lines {
		switch l.Status {
		case "approved":
			totalApproved += l.Amount
		case "draft":
			totalDraft += l.Amount
		}
	}

	response.Success(c, map[string]interface{}{
		"items":          lines,
		"total_approved": totalApproved,
		"total_draft":    totalDraft,
		"total":          totalApproved + totalDraft,
	})
}

// CreateExpenseLine creates a new draft expense line
func (h *Handler) CreateExpenseLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var req struct {
		StageID        int64   `json:"stage_id"`
		WbsID          int64   `json:"wbs_id"`
		CostCategoryID int64   `json:"cost_category_id"`
		ExpenseDate    string  `json:"expense_date" binding:"required"`
		Description    string  `json:"description" binding:"required"`
		ProductID      string  `json:"product_id"`
		Quantity       float64 `json:"quantity"`
		Uom            string  `json:"uom"`
		UnitPrice      float64 `json:"unit_price"`
		Amount         float64 `json:"amount" binding:"required"`
		CurrencyCode   string  `json:"currency_code"`
		VendorID       string  `json:"vendor_id"`
		DebitAccountID  string `json:"debit_account_id"`
		CreditAccountID string `json:"credit_account_id"`
		DocumentURL    string  `json:"document_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Amount <= 0 {
		response.BadRequest(c, "Amount must be greater than 0")
		return
	}
	if req.CurrencyCode == "" {
		req.CurrencyCode = "UZS"
	}

	// Validate project exists
	var projStatus string
	var analyticAccID *uuid.UUID
	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}
	err = h.db.QueryRow(`
		SELECT COALESCE(status,'draft'), analytic_account_id
		FROM construction_projects WHERE id = $1 AND tenant_id = $2
	`, projectID, tenantID).Scan(&projStatus, &analyticAccID)
	if err != nil {
		response.NotFound(c, "Project not found")
		return
	}
	if projStatus == "completed" {
		response.BadRequest(c, "Cannot add expenses to a completed project")
		return
	}

	// Resolve debit account: use provided or fall back to WIP mapping
	debitAccID := nullUUIDFromVal(req.DebitAccountID)
	if debitAccID == nil {
		wipAcct := h.getConstructionMappedAccount(tenantID, orgIDPtr, "wip_0810", "tugallanmagan qurilish", "0810")
		if wipAcct != uuid.Nil {
			debitAccID = wipAcct
		}
	}

	// Resolve analytic account from project
	var analyticVal interface{}
	if analyticAccID != nil && *analyticAccID != uuid.Nil {
		analyticVal = *analyticAccID
	}

	id := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO construction_expense_lines (
			id, tenant_id, organization_id, project_id, stage_id, wbs_id, cost_category_id,
			expense_date, description, product_id, quantity, uom, unit_price,
			amount, currency_code, vendor_id,
			debit_account_id, credit_account_id, analytic_account_id,
			document_url, status, created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13,
			$14, $15, $16,
			$17, $18, $19,
			$20, 'draft', $21, NOW(), NOW()
		)
	`,
		id, tenantID, orgIDPtr, projectID,
		nullInt64FromVal(req.StageID), nullInt64FromVal(req.WbsID), nullInt64FromVal(req.CostCategoryID),
		req.ExpenseDate, req.Description,
		nullUUIDFromVal(req.ProductID), nullFloat64FromVal(req.Quantity), nullStringFromVal(req.Uom), nullFloat64FromVal(req.UnitPrice),
		req.Amount, req.CurrencyCode, nullUUIDFromVal(req.VendorID),
		debitAccID, nullUUIDFromVal(req.CreditAccountID), analyticVal,
		nullStringFromVal(req.DocumentURL), userID,
	)
	if err != nil {
		h.log.Error("Failed to create expense line", "error", err)
		response.InternalError(c, "Failed to create expense line")
		return
	}

	h.logConstructionActivity(tenantID, projectID, userID, "expense", fmt.Sprintf("Xarajat yaratildi: %s (%.2f %s)", req.Description, req.Amount, req.CurrencyCode), "ExpenseLine", 0)

	response.Success(c, map[string]interface{}{"id": id, "message": "Expense line created"})
}

// UpdateExpenseLine updates a draft expense line
func (h *Handler) UpdateExpenseLine(c *gin.Context) {
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

	// Only draft lines can be updated
	var currentStatus string
	err = h.db.QueryRow(
		`SELECT status FROM construction_expense_lines WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		id, tenantID,
	).Scan(&currentStatus)
	if err != nil {
		response.NotFound(c, "Expense line not found")
		return
	}
	if currentStatus != "draft" {
		response.BadRequest(c, "Only draft expense lines can be edited")
		return
	}

	var req struct {
		StageID         *int64   `json:"stage_id"`
		WbsID           *int64   `json:"wbs_id"`
		CostCategoryID  *int64   `json:"cost_category_id"`
		ExpenseDate     *string  `json:"expense_date"`
		Description     *string  `json:"description"`
		ProductID       *string  `json:"product_id"`
		Quantity        *float64 `json:"quantity"`
		Uom             *string  `json:"uom"`
		UnitPrice       *float64 `json:"unit_price"`
		Amount          *float64 `json:"amount"`
		CurrencyCode    *string  `json:"currency_code"`
		VendorID        *string  `json:"vendor_id"`
		DebitAccountID  *string  `json:"debit_account_id"`
		CreditAccountID *string  `json:"credit_account_id"`
		DocumentURL     *string  `json:"document_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	addArg := func(col string, val interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", col, argCount))
		args = append(args, val)
	}

	if req.StageID != nil {
		addArg("stage_id", nullInt64FromVal(*req.StageID))
	}
	if req.WbsID != nil {
		addArg("wbs_id", nullInt64FromVal(*req.WbsID))
	}
	if req.CostCategoryID != nil {
		addArg("cost_category_id", nullInt64FromVal(*req.CostCategoryID))
	}
	if req.ExpenseDate != nil {
		addArg("expense_date", *req.ExpenseDate)
	}
	if req.Description != nil {
		addArg("description", *req.Description)
	}
	if req.ProductID != nil {
		addArg("product_id", nullUUIDFromVal(*req.ProductID))
	}
	if req.Quantity != nil {
		addArg("quantity", nullFloat64FromVal(*req.Quantity))
	}
	if req.Uom != nil {
		addArg("uom", nullStringFromVal(*req.Uom))
	}
	if req.UnitPrice != nil {
		addArg("unit_price", nullFloat64FromVal(*req.UnitPrice))
	}
	if req.Amount != nil {
		if *req.Amount <= 0 {
			response.BadRequest(c, "Amount must be greater than 0")
			return
		}
		addArg("amount", *req.Amount)
	}
	if req.CurrencyCode != nil {
		addArg("currency_code", *req.CurrencyCode)
	}
	if req.VendorID != nil {
		addArg("vendor_id", nullUUIDFromVal(*req.VendorID))
	}
	if req.DebitAccountID != nil {
		addArg("debit_account_id", nullUUIDFromVal(*req.DebitAccountID))
	}
	if req.CreditAccountID != nil {
		addArg("credit_account_id", nullUUIDFromVal(*req.CreditAccountID))
	}
	if req.DocumentURL != nil {
		addArg("document_url", nullStringFromVal(*req.DocumentURL))
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE construction_expense_lines SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update expense line", "error", err)
		response.InternalError(c, "Failed to update expense line")
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		response.NotFound(c, "Expense line not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Expense line updated"})
}

// DeleteExpenseLine soft-deletes a draft expense line
func (h *Handler) DeleteExpenseLine(c *gin.Context) {
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

	var currentStatus string
	err = h.db.QueryRow(
		`SELECT status FROM construction_expense_lines WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		id, tenantID,
	).Scan(&currentStatus)
	if err != nil {
		response.NotFound(c, "Expense line not found")
		return
	}
	if currentStatus != "draft" {
		response.BadRequest(c, "Only draft expense lines can be deleted")
		return
	}

	_, err = h.db.Exec(
		`UPDATE construction_expense_lines SET deleted_at = NOW() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete expense line", "error", err)
		response.InternalError(c, "Failed to delete expense line")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Expense line deleted"})
}

// ApproveExpenseLine approves an expense line and creates a journal entry
func (h *Handler) ApproveExpenseLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	lineID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	// Load expense line
	type lineData struct {
		ProjectID       int64
		StageID         *int64
		CostCategoryID  *int64
		Amount          float64
		Description     string
		ExpenseDate     string
		CurrencyCode    string
		DebitAccountID  *uuid.UUID
		CreditAccountID *uuid.UUID
		AnalyticAccID   *uuid.UUID
		Status          string
	}
	var ld lineData
	err = h.db.QueryRow(`
		SELECT project_id, stage_id, cost_category_id, amount, description, expense_date::text, currency_code,
		       debit_account_id, credit_account_id, analytic_account_id, status
		FROM construction_expense_lines
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, lineID, tenantID).Scan(
		&ld.ProjectID, &ld.StageID, &ld.CostCategoryID, &ld.Amount, &ld.Description, &ld.ExpenseDate, &ld.CurrencyCode,
		&ld.DebitAccountID, &ld.CreditAccountID, &ld.AnalyticAccID, &ld.Status,
	)
	if err != nil {
		response.NotFound(c, "Expense line not found")
		return
	}
	if ld.Status != "draft" {
		response.BadRequest(c, "Only draft expense lines can be approved")
		return
	}

	// Resolve approver employee
	var approverEmpID *uuid.UUID
	if userID != uuid.Nil {
		var empID uuid.UUID
		if err := h.db.QueryRow(`SELECT id FROM employees WHERE user_id = $1 AND tenant_id = $2 LIMIT 1`, userID, tenantID).Scan(&empID); err == nil {
			approverEmpID = &empID
		}
	}

	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}

	// Resolve debit account: expense line → category's default account → WIP mapping
	debitAccID := ld.DebitAccountID
	if (debitAccID == nil || *debitAccID == uuid.Nil) && ld.CostCategoryID != nil {
		var catAccID uuid.UUID
		h.db.QueryRow(`SELECT default_debit_account_id FROM construction_cost_categories WHERE id = $1 AND tenant_id = $2 AND default_debit_account_id IS NOT NULL`, *ld.CostCategoryID, tenantID).Scan(&catAccID)
		if catAccID != uuid.Nil {
			debitAccID = &catAccID
		}
	}
	if debitAccID == nil || *debitAccID == uuid.Nil {
		wipAcct := h.getConstructionMappedAccount(tenantID, orgIDPtr, "wip_0810", "tugallanmagan qurilish", "0810")
		if wipAcct != uuid.Nil {
			debitAccID = &wipAcct
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalError(c, "Failed to approve expense")
		return
	}
	defer tx.Rollback()

	now := time.Now()

	// Resolve credit account: explicit → cash/bank fallback
	creditAccID := ld.CreditAccountID
	if creditAccID == nil || *creditAccID == uuid.Nil {
		cashAcct := h.getConstructionMappedAccount(tenantID, orgIDPtr, "cash_5010", "kassa", "5010")
		if cashAcct == uuid.Nil {
			cashAcct = findAccount(h.db, tenantID, orgIDPtr, "cash", "1000")
		}
		if cashAcct != uuid.Nil {
			creditAccID = &cashAcct
		}
	}

	// Create journal entry (best-effort with savepoint)
	var journalEntryID *uuid.UUID
	if debitAccID != nil && creditAccID != nil && *creditAccID != uuid.Nil {
		journalID := h.ensureConstructionJournal(tenantID, orgIDPtr)
		if journalID != uuid.Nil {
			tx.Exec(`SAVEPOINT sp_je_expense`)

			nextNum := h.getNextJournalNumber(tx, journalID)
			entryID := uuid.New()
			entryNumber := fmt.Sprintf("CE%06d", nextNum)

			_, jeErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number,
					entry_date, description, source_type, source_id,
					status, total_debit, total_credit, created_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,'construction_expense',NULL,'posted',$8,$8,$9,$9)
			`, entryID, tenantID, organizationID, journalID, entryNumber,
				now, fmt.Sprintf("Construction Expense: %s", ld.Description),
				ld.Amount, now)

			if jeErr != nil {
				h.log.Error("Journal entry insert failed", "error", jeErr)
				tx.Exec(`ROLLBACK TO SAVEPOINT sp_je_expense`)
			} else {
				// Debit line
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,$5,0,1,$6)`,
					uuid.New(), entryID, *debitAccID, ld.Description, ld.Amount, now)
				// Credit line
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,0,$5,2,$6)`,
					uuid.New(), entryID, *creditAccID, ld.Description, ld.Amount, now)
				// Update account balances
				tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, ld.Amount, now, *debitAccID)
				tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, ld.Amount, now, *creditAccID)
				tx.Exec(`RELEASE SAVEPOINT sp_je_expense`)
				journalEntryID = &entryID
			}
		}
	}

	// Mark expense line as approved
	_, err = tx.Exec(`
		UPDATE construction_expense_lines
		SET status = 'approved',
		    approved_by = $1,
		    approved_at = $2,
		    journal_entry_id = $3,
		    updated_at = $2
		WHERE id = $4 AND tenant_id = $5
	`, approverEmpID, now, journalEntryID, lineID, tenantID)
	if err != nil {
		h.log.Error("Failed to approve expense line", "error", err)
		response.InternalError(c, "Failed to approve expense line")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit approval", "error", err)
		response.InternalError(c, "Failed to approve expense line")
		return
	}

	h.logConstructionActivity(tenantID, ld.ProjectID, userID, "expense", fmt.Sprintf("Xarajat tasdiqlandi: %s (%.2f)", ld.Description, ld.Amount), "ExpenseLine", 0)

	result := map[string]interface{}{"message": "Expense line approved"}
	if journalEntryID != nil {
		result["journal_entry_id"] = journalEntryID.String()
	}
	response.Success(c, result)
}

// CancelExpenseLine cancels an expense line (creates reversal if already approved)
func (h *Handler) CancelExpenseLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	lineID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)

	type lineData struct {
		ProjectID       int64
		Amount          float64
		Description     string
		Status          string
		DebitAccountID  *uuid.UUID
		CreditAccountID *uuid.UUID
		JournalEntryID  *uuid.UUID
	}
	var ld lineData
	err = h.db.QueryRow(`
		SELECT project_id, amount, description, status,
		       debit_account_id, credit_account_id, journal_entry_id
		FROM construction_expense_lines
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, lineID, tenantID).Scan(
		&ld.ProjectID, &ld.Amount, &ld.Description, &ld.Status,
		&ld.DebitAccountID, &ld.CreditAccountID, &ld.JournalEntryID,
	)
	if err != nil {
		response.NotFound(c, "Expense line not found")
		return
	}
	if ld.Status == "cancelled" {
		response.BadRequest(c, "Expense line is already cancelled")
		return
	}

	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to cancel expense")
		return
	}
	defer tx.Rollback()

	now := time.Now()

	// If approved, create reversal journal entry
	if ld.Status == "approved" && ld.DebitAccountID != nil && ld.CreditAccountID != nil && *ld.CreditAccountID != uuid.Nil {
		journalID := h.ensureConstructionJournal(tenantID, orgIDPtr)
		if journalID != uuid.Nil {
			tx.Exec(`SAVEPOINT sp_je_reversal`)
			nextNum := h.getNextJournalNumber(tx, journalID)
			reversalID := uuid.New()
			reversalNumber := fmt.Sprintf("CE-REV%06d", nextNum)

			_, revErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number,
					entry_date, description, source_type, source_id,
					status, total_debit, total_credit, created_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,'construction_expense_reversal',NULL,'posted',$8,$8,$9,$9)
			`, reversalID, tenantID, organizationID, journalID, reversalNumber,
				now, fmt.Sprintf("REVERSAL: %s", ld.Description), ld.Amount, now)

			if revErr != nil {
				tx.Exec(`ROLLBACK TO SAVEPOINT sp_je_reversal`)
			} else {
				// Reverse: Debit credit account, Credit debit account
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,$5,0,1,$6)`,
					uuid.New(), reversalID, *ld.CreditAccountID, "Reversal", ld.Amount, now)
				tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1,$2,$3,$4,0,$5,2,$6)`,
					uuid.New(), reversalID, *ld.DebitAccountID, "Reversal", ld.Amount, now)
				tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, ld.Amount, now, *ld.DebitAccountID)
				tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, ld.Amount, now, *ld.CreditAccountID)
				tx.Exec(`RELEASE SAVEPOINT sp_je_reversal`)
			}
		}
	}

	cancelReason := req.Reason
	if cancelReason == "" {
		cancelReason = "Cancelled"
	}

	_, err = tx.Exec(`
		UPDATE construction_expense_lines
		SET status = 'cancelled', cancelled_reason = $1, cancelled_at = $2, updated_at = $2
		WHERE id = $3 AND tenant_id = $4
	`, cancelReason, now, lineID, tenantID)
	if err != nil {
		h.log.Error("Failed to cancel expense line", "error", err)
		response.InternalError(c, "Failed to cancel expense line")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to cancel expense line")
		return
	}

	h.logConstructionActivity(tenantID, ld.ProjectID, userID, "expense", fmt.Sprintf("Xarajat bekor qilindi: %s", ld.Description), "ExpenseLine", 0)

	response.Success(c, map[string]interface{}{"message": "Expense line cancelled"})
}

// =====================================================
// HELPER
// =====================================================

func nullFloat64FromVal(val float64) interface{} {
	if val == 0 {
		return nil
	}
	return val
}
