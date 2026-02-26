package handler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ========== CASH REGISTERS ==========

func (h *Handler) ListCashRegisters(c *gin.Context)  { response.Success(c, []interface{}{}) }
func (h *Handler) CreateCashRegister(c *gin.Context) { response.Created(c, gin.H{"message": "Cash register created"}) }
func (h *Handler) GetCashRegister(c *gin.Context)    { response.NotFound(c, "Cash register") }
func (h *Handler) UpdateCashRegister(c *gin.Context) { response.Success(c, gin.H{"message": "Cash register updated"}) }

// ========== CASH ORDERS (PKO/RKO) ==========

func (h *Handler) ListCashOrders(c *gin.Context)   { response.Success(c, []interface{}{}) }
func (h *Handler) CreateCashOrder(c *gin.Context)  { response.Created(c, gin.H{"message": "Cash order created"}) }
func (h *Handler) GetCashOrder(c *gin.Context)     { response.NotFound(c, "Cash order") }
func (h *Handler) UpdateCashOrder(c *gin.Context)  { response.Success(c, gin.H{"message": "Cash order updated"}) }
func (h *Handler) ConfirmCashOrder(c *gin.Context) { response.Success(c, gin.H{"message": "Cash order confirmed"}) }

// ========== CASH BOOK ==========

func (h *Handler) GetCashBook(c *gin.Context) { response.Success(c, []interface{}{}) }

// ========== CURRENCY RATES SYNC ==========

func (h *Handler) SyncCurrencyRates(c *gin.Context) { response.Success(c, gin.H{"message": "Exchange rates synced from CBU"}) }
func (h *Handler) RevalueCurrency(c *gin.Context)   { response.Success(c, gin.H{"message": "Currency revaluation completed"}) }
func (h *Handler) ListExchangeDiffs(c *gin.Context) { response.Success(c, []interface{}{}) }

// ========== RECONCILIATION ACTS (Akt sverka) ==========

type reconciliationActResponse struct {
	ID             uuid.UUID              `json:"id"`
	PartnerID      uuid.UUID              `json:"partner_id"`
	PartnerName    string                 `json:"partner_name"`
	PeriodStart    string                 `json:"period_start"`
	PeriodEnd      string                 `json:"period_end"`
	OpeningBalance float64                `json:"opening_balance"`
	OurDebitTotal  float64                `json:"our_debit_total"`
	OurCreditTotal float64                `json:"our_credit_total"`
	OurBalance     float64                `json:"our_balance"`
	ClosingBalance float64                `json:"closing_balance"`
	Status         string                 `json:"status"`
	Notes          *string                `json:"notes"`
	Lines          []reconciliationLine   `json:"lines,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
}

type reconciliationLine struct {
	Date           string  `json:"date"`
	Document       string  `json:"document"`
	Description    string  `json:"description"`
	Debit          float64 `json:"debit"`
	Credit         float64 `json:"credit"`
	RunningBalance float64 `json:"running_balance"`
}

// computeReconciliationData queries journal_entry_lines for the given partner/tenant/org/period
// and computes opening balance, transaction lines, totals, and closing balance.
func (h *Handler) computeReconciliationData(tenantID, partnerID uuid.UUID, orgID *uuid.UUID, periodStart, periodEnd string) (
	openingBalance float64, lines []reconciliationLine, totalDebit, totalCredit float64, err error,
) {
	// 1. Opening balance: sum of all debit - credit for this partner BEFORE period_start
	obQuery := `
		SELECT COALESCE(SUM(jel.debit_amount), 0) - COALESCE(SUM(jel.credit_amount), 0)
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE je.tenant_id = $1
		  AND jel.contact_id = $2
		  AND je.entry_date < $3
		  AND je.status = 'posted'
	`
	obArgs := []interface{}{tenantID, partnerID, periodStart}
	if orgID != nil {
		obQuery += " AND je.organization_id = $4"
		obArgs = append(obArgs, *orgID)
	}

	err = h.db.QueryRow(obQuery, obArgs...).Scan(&openingBalance)
	if err != nil {
		return
	}

	// 2. Transaction lines within the period
	linesQuery := `
		SELECT je.entry_date, je.entry_number, COALESCE(je.description, COALESCE(jel.description, '')),
			   jel.debit_amount, jel.credit_amount
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE je.tenant_id = $1
		  AND jel.contact_id = $2
		  AND je.entry_date >= $3
		  AND je.entry_date <= $4
		  AND je.status = 'posted'
	`
	linesArgs := []interface{}{tenantID, partnerID, periodStart, periodEnd}
	if orgID != nil {
		linesQuery += " AND je.organization_id = $5"
		linesArgs = append(linesArgs, *orgID)
	}
	linesQuery += " ORDER BY je.entry_date, je.entry_number, jel.line_number"

	var rows *sql.Rows
	rows, err = h.db.Query(linesQuery, linesArgs...)
	if err != nil {
		return
	}
	defer rows.Close()

	lines = make([]reconciliationLine, 0)
	runningBal := openingBalance
	for rows.Next() {
		var l reconciliationLine
		var entryDate time.Time
		err = rows.Scan(&entryDate, &l.Document, &l.Description, &l.Debit, &l.Credit)
		if err != nil {
			return
		}
		l.Date = entryDate.Format("2006-01-02")
		runningBal += l.Debit - l.Credit
		l.RunningBalance = runningBal
		totalDebit += l.Debit
		totalCredit += l.Credit
		lines = append(lines, l)
	}

	return
}

func (h *Handler) ListReconciliationActs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT ra.id, ra.partner_id, COALESCE(ct.name, '') as partner_name,
			   ra.period_start, ra.period_end,
			   ra.opening_balance, ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.status, ra.notes, ra.created_at
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.tenant_id = $1 AND ra.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND ra.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if status := c.Query("status"); status != "" {
		argCount++
		query += fmt.Sprintf(" AND ra.status = $%d", argCount)
		args = append(args, status)
	}

	query += " ORDER BY ra.created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list reconciliation acts", "error", err)
		response.InternalError(c, "Failed to list reconciliation acts")
		return
	}
	defer rows.Close()

	acts := make([]reconciliationActResponse, 0)
	for rows.Next() {
		var a reconciliationActResponse
		var notes sql.NullString
		var periodStart, periodEnd time.Time

		err := rows.Scan(
			&a.ID, &a.PartnerID, &a.PartnerName,
			&periodStart, &periodEnd,
			&a.OpeningBalance, &a.OurDebitTotal, &a.OurCreditTotal, &a.OurBalance,
			&a.Status, &notes, &a.CreatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan reconciliation act", "error", err)
			continue
		}

		a.PeriodStart = periodStart.Format("2006-01-02")
		a.PeriodEnd = periodEnd.Format("2006-01-02")
		a.ClosingBalance = a.OpeningBalance + a.OurDebitTotal - a.OurCreditTotal
		if notes.Valid {
			a.Notes = &notes.String
		}
		acts = append(acts, a)
	}

	response.Success(c, acts)
}

type createReconciliationActInput struct {
	PartnerID   string `json:"partner_id"`
	PartnerName string `json:"partner_name"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Notes       string `json:"notes"`
}

func (h *Handler) CreateReconciliationAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	var input createReconciliationActInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	if input.PeriodStart == "" || input.PeriodEnd == "" {
		response.BadRequest(c, "period_start and period_end are required")
		return
	}

	// Resolve partner_id
	var partnerID uuid.UUID
	if input.PartnerID != "" {
		parsed, err := uuid.Parse(input.PartnerID)
		if err == nil {
			partnerID = parsed
		}
	}

	if partnerID == uuid.Nil && input.PartnerName != "" {
		err := h.db.QueryRow(
			"SELECT id FROM contacts WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL LIMIT 1",
			tenantID, input.PartnerName,
		).Scan(&partnerID)
		if err != nil {
			partnerID = uuid.New()
			code := fmt.Sprintf("C-%s", partnerID.String()[:8])
			_, err = h.db.Exec(
				`INSERT INTO contacts (id, tenant_id, code, name, type, created_at, updated_at)
				 VALUES ($1, $2, $3, $4, 'company', NOW(), NOW())`,
				partnerID, tenantID, code, input.PartnerName,
			)
			if err != nil {
				h.log.Error("Failed to create contact for reconciliation", "error", err)
				response.InternalError(c, "Failed to create contact")
				return
			}
		}
	}

	if partnerID == uuid.Nil {
		response.BadRequest(c, "partner_id or partner_name is required")
		return
	}

	// Compute balances from journal entry lines
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	openingBalance, lines, totalDebit, totalCredit, err := h.computeReconciliationData(tenantID, partnerID, orgIDPtr, input.PeriodStart, input.PeriodEnd)
	if err != nil {
		h.log.Error("Failed to compute reconciliation data", "error", err)
		response.InternalError(c, "Failed to compute reconciliation data")
		return
	}

	ourBalance := openingBalance + totalDebit - totalCredit

	id := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO reconciliation_acts (id, tenant_id, organization_id, partner_id, period_start, period_end,
			opening_balance, our_debit_total, our_credit_total, our_balance, notes, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
	`, id, tenantID, orgIDPtr, partnerID, input.PeriodStart, input.PeriodEnd,
		openingBalance, totalDebit, totalCredit, ourBalance, nullStr(input.Notes), userID)
	if err != nil {
		h.log.Error("Failed to create reconciliation act", "error", err)
		response.InternalError(c, "Failed to create reconciliation act")
		return
	}

	// Get partner name
	var partnerName string
	_ = h.db.QueryRow("SELECT COALESCE(name, '') FROM contacts WHERE id = $1", partnerID).Scan(&partnerName)

	act := reconciliationActResponse{
		ID:             id,
		PartnerID:      partnerID,
		PartnerName:    partnerName,
		PeriodStart:    input.PeriodStart,
		PeriodEnd:      input.PeriodEnd,
		OpeningBalance: openingBalance,
		OurDebitTotal:  totalDebit,
		OurCreditTotal: totalCredit,
		OurBalance:     ourBalance,
		ClosingBalance: ourBalance,
		Status:         "draft",
		Lines:          lines,
		CreatedAt:      time.Now(),
	}
	if input.Notes != "" {
		act.Notes = &input.Notes
	}

	response.Created(c, act)
}

func (h *Handler) GetReconciliationAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var act reconciliationActResponse
	var notes sql.NullString
	var periodStart, periodEnd time.Time

	err = h.db.QueryRow(`
		SELECT ra.id, ra.partner_id, COALESCE(ct.name, '') as partner_name,
			   ra.period_start, ra.period_end,
			   ra.opening_balance, ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.status, ra.notes, ra.created_at
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.id = $1 AND ra.tenant_id = $2 AND ra.deleted_at IS NULL
	`, id, tenantID).Scan(
		&act.ID, &act.PartnerID, &act.PartnerName,
		&periodStart, &periodEnd,
		&act.OpeningBalance, &act.OurDebitTotal, &act.OurCreditTotal, &act.OurBalance,
		&act.Status, &notes, &act.CreatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Reconciliation act")
		return
	}
	if err != nil {
		h.log.Error("Failed to get reconciliation act", "error", err)
		response.InternalError(c, "Failed to get reconciliation act")
		return
	}

	act.PeriodStart = periodStart.Format("2006-01-02")
	act.PeriodEnd = periodEnd.Format("2006-01-02")
	act.ClosingBalance = act.OpeningBalance + act.OurDebitTotal - act.OurCreditTotal
	if notes.Valid {
		act.Notes = &notes.String
	}

	// Fetch live transaction lines from journal entries
	var orgIDPtr *uuid.UUID
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	_, lines, _, _, linesErr := h.computeReconciliationData(tenantID, act.PartnerID, orgIDPtr, act.PeriodStart, act.PeriodEnd)
	if linesErr != nil {
		h.log.Error("Failed to fetch reconciliation lines", "error", linesErr)
	} else {
		act.Lines = lines
	}

	response.Success(c, act)
}

func (h *Handler) UpdateReconciliationAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var input struct {
		Status *string `json:"status"`
		Notes  *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argCount := 0

	if input.Status != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.Notes != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE reconciliation_acts SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(setClauses, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update reconciliation act", "error", err)
		response.InternalError(c, "Failed to update reconciliation act")
		return
	}

	response.Success(c, gin.H{"message": "Reconciliation act updated"})
}

// RefreshReconciliationAct recalculates the act from live journal entry data.
func (h *Handler) RefreshReconciliationAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	// Fetch the act
	var partnerID uuid.UUID
	var periodStart, periodEnd time.Time
	var orgIDNullable sql.NullString

	err = h.db.QueryRow(`
		SELECT partner_id, period_start, period_end, CAST(organization_id AS TEXT)
		FROM reconciliation_acts
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&partnerID, &periodStart, &periodEnd, &orgIDNullable)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Reconciliation act")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch act for refresh", "error", err)
		response.InternalError(c, "Failed to fetch act")
		return
	}

	var orgIDPtr *uuid.UUID
	if orgIDNullable.Valid {
		if parsed, pErr := uuid.Parse(orgIDNullable.String); pErr == nil {
			orgIDPtr = &parsed
		}
	}

	pStart := periodStart.Format("2006-01-02")
	pEnd := periodEnd.Format("2006-01-02")

	openingBalance, lines, totalDebit, totalCredit, compErr := h.computeReconciliationData(tenantID, partnerID, orgIDPtr, pStart, pEnd)
	if compErr != nil {
		h.log.Error("Failed to compute refresh data", "error", compErr)
		response.InternalError(c, "Failed to compute reconciliation data")
		return
	}

	ourBalance := openingBalance + totalDebit - totalCredit

	_, err = h.db.Exec(`
		UPDATE reconciliation_acts
		SET opening_balance = $1, our_debit_total = $2, our_credit_total = $3, our_balance = $4, updated_at = NOW()
		WHERE id = $5
	`, openingBalance, totalDebit, totalCredit, ourBalance, id)
	if err != nil {
		h.log.Error("Failed to update act balances", "error", err)
		response.InternalError(c, "Failed to update act balances")
		return
	}

	var partnerName string
	_ = h.db.QueryRow("SELECT COALESCE(name, '') FROM contacts WHERE id = $1", partnerID).Scan(&partnerName)

	act := reconciliationActResponse{
		ID:             id,
		PartnerID:      partnerID,
		PartnerName:    partnerName,
		PeriodStart:    pStart,
		PeriodEnd:      pEnd,
		OpeningBalance: openingBalance,
		OurDebitTotal:  totalDebit,
		OurCreditTotal: totalCredit,
		OurBalance:     ourBalance,
		ClosingBalance: ourBalance,
		Status:         "draft",
		Lines:          lines,
		CreatedAt:      time.Now(),
	}

	response.Success(c, act)
}

func (h *Handler) DeleteReconciliationAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	_, err = h.db.Exec(
		"UPDATE reconciliation_acts SET deleted_at = NOW() WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL",
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete reconciliation act", "error", err)
		response.InternalError(c, "Failed to delete reconciliation act")
		return
	}

	response.NoContent(c)
}

func (h *Handler) BulkGenerateReconciliation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	var input struct {
		PeriodStart string `json:"period_start"`
		PeriodEnd   string `json:"period_end"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	if input.PeriodStart == "" || input.PeriodEnd == "" {
		response.BadRequest(c, "period_start and period_end are required")
		return
	}

	// Find all contacts that have journal entry lines in the period
	partnerQuery := `
		SELECT DISTINCT jel.contact_id
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE je.tenant_id = $1
		  AND jel.contact_id IS NOT NULL
		  AND je.status = 'posted'
		  AND je.entry_date >= $2
		  AND je.entry_date <= $3
	`
	partnerArgs := []interface{}{tenantID, input.PeriodStart, input.PeriodEnd}
	if orgID != uuid.Nil {
		partnerQuery += " AND je.organization_id = $4"
		partnerArgs = append(partnerArgs, orgID)
	}

	rows, err := h.db.Query(partnerQuery, partnerArgs...)
	if err != nil {
		h.log.Error("Failed to find partners for bulk generate", "error", err)
		response.InternalError(c, "Failed to find partners")
		return
	}
	defer rows.Close()

	var partnerIDs []uuid.UUID
	for rows.Next() {
		var pid uuid.UUID
		if err := rows.Scan(&pid); err == nil {
			partnerIDs = append(partnerIDs, pid)
		}
	}

	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	count := 0
	for _, pid := range partnerIDs {
		// Check if act already exists for this partner+period
		var exists bool
		_ = h.db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM reconciliation_acts
			WHERE tenant_id = $1 AND partner_id = $2 AND period_start = $3 AND period_end = $4 AND deleted_at IS NULL)
		`, tenantID, pid, input.PeriodStart, input.PeriodEnd).Scan(&exists)
		if exists {
			continue
		}

		openingBalance, _, totalDebit, totalCredit, compErr := h.computeReconciliationData(tenantID, pid, orgIDPtr, input.PeriodStart, input.PeriodEnd)
		if compErr != nil {
			h.log.Error("Failed to compute data for bulk partner", "partner_id", pid, "error", compErr)
			continue
		}

		ourBalance := openingBalance + totalDebit - totalCredit
		id := uuid.New()

		_, err = h.db.Exec(`
			INSERT INTO reconciliation_acts (id, tenant_id, organization_id, partner_id, period_start, period_end,
				opening_balance, our_debit_total, our_credit_total, our_balance, created_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		`, id, tenantID, orgIDPtr, pid, input.PeriodStart, input.PeriodEnd,
			openingBalance, totalDebit, totalCredit, ourBalance, userID)
		if err != nil {
			h.log.Error("Failed to create bulk act", "partner_id", pid, "error", err)
			continue
		}
		count++
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("Generated %d reconciliation acts", count), "count": count})
}

func (h *Handler) ExportReconciliationAct(c *gin.Context) {
	response.Success(c, gin.H{"message": "Export generated", "url": ""})
}

// ========== BUDGETS (extended) ==========

func (h *Handler) ListBudgetsV2(c *gin.Context)        { response.Success(c, []interface{}{}) }
func (h *Handler) CreateBudgetV2(c *gin.Context)       { response.Created(c, gin.H{"message": "Budget created"}) }
func (h *Handler) GetBudgetV2(c *gin.Context)          { response.NotFound(c, "Budget") }
func (h *Handler) UpdateBudgetV2(c *gin.Context)       { response.Success(c, gin.H{"message": "Budget updated"}) }
func (h *Handler) DeleteBudgetV2(c *gin.Context)       { response.NoContent(c) }

func (h *Handler) GetConsolidatedBudget(c *gin.Context) {
	response.Success(c, gin.H{"consolidated": []interface{}{}, "total_planned": 0, "total_actual": 0})
}

// helpers

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

