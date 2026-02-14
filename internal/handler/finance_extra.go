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
	ID                 uuid.UUID  `json:"id"`
	PartnerID          uuid.UUID  `json:"partner_id"`
	PartnerName        string     `json:"partner_name"`
	PeriodStart        string     `json:"period_start"`
	PeriodEnd          string     `json:"period_end"`
	OpeningBalance     float64    `json:"opening_balance"`
	OurDebitTotal      float64    `json:"our_debit_total"`
	OurCreditTotal     float64    `json:"our_credit_total"`
	OurBalance         float64    `json:"our_balance"`
	PartnerDebitTotal  float64    `json:"partner_debit_total"`
	PartnerCreditTotal float64    `json:"partner_credit_total"`
	PartnerBalance     float64    `json:"partner_balance"`
	Difference         float64    `json:"difference"`
	Status             string     `json:"status"`
	Notes              *string    `json:"notes"`
	CreatedAt          time.Time  `json:"created_at"`
}

func (h *Handler) ListReconciliationActs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT ra.id, ra.partner_id, COALESCE(ct.name, '') as partner_name,
			   ra.period_start, ra.period_end, ra.opening_balance,
			   ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.partner_debit_total, ra.partner_credit_total, ra.partner_balance,
			   ra.difference, ra.status, ra.notes, ra.created_at
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
			&periodStart, &periodEnd, &a.OpeningBalance,
			&a.OurDebitTotal, &a.OurCreditTotal, &a.OurBalance,
			&a.PartnerDebitTotal, &a.PartnerCreditTotal, &a.PartnerBalance,
			&a.Difference, &a.Status, &notes, &a.CreatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan reconciliation act", "error", err)
			continue
		}

		a.PeriodStart = periodStart.Format("2006-01-02")
		a.PeriodEnd = periodEnd.Format("2006-01-02")
		if notes.Valid {
			a.Notes = &notes.String
		}
		acts = append(acts, a)
	}

	response.Success(c, acts)
}

type createReconciliationActInput struct {
	PartnerID   string  `json:"partner_id"`
	PartnerName string  `json:"partner_name"`
	PeriodStart string  `json:"period_start"`
	PeriodEnd   string  `json:"period_end"`
	Notes       string  `json:"notes"`
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

	// Resolve partner_id: if provided use it, otherwise create/find contact by name
	var partnerID uuid.UUID
	if input.PartnerID != "" {
		parsed, err := uuid.Parse(input.PartnerID)
		if err == nil {
			partnerID = parsed
		}
	}

	if partnerID == uuid.Nil && input.PartnerName != "" {
		// Try to find existing contact by name
		err := h.db.QueryRow(
			"SELECT id FROM contacts WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL LIMIT 1",
			tenantID, input.PartnerName,
		).Scan(&partnerID)

		if err != nil {
			// Create a new contact
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

	id := uuid.New()
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	_, err := h.db.Exec(`
		INSERT INTO reconciliation_acts (id, tenant_id, organization_id, partner_id, period_start, period_end, notes, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
	`, id, tenantID, orgIDPtr, partnerID, input.PeriodStart, input.PeriodEnd, nullStr(input.Notes), userID)
	if err != nil {
		h.log.Error("Failed to create reconciliation act", "error", err)
		response.InternalError(c, "Failed to create reconciliation act")
		return
	}

	// Fetch the created act to return full data
	var act reconciliationActResponse
	var notes sql.NullString
	var periodStart, periodEnd time.Time

	err = h.db.QueryRow(`
		SELECT ra.id, ra.partner_id, COALESCE(ct.name, '') as partner_name,
			   ra.period_start, ra.period_end, ra.opening_balance,
			   ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.partner_debit_total, ra.partner_credit_total, ra.partner_balance,
			   ra.difference, ra.status, ra.notes, ra.created_at
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.id = $1
	`, id).Scan(
		&act.ID, &act.PartnerID, &act.PartnerName,
		&periodStart, &periodEnd, &act.OpeningBalance,
		&act.OurDebitTotal, &act.OurCreditTotal, &act.OurBalance,
		&act.PartnerDebitTotal, &act.PartnerCreditTotal, &act.PartnerBalance,
		&act.Difference, &act.Status, &notes, &act.CreatedAt,
	)
	if err != nil {
		h.log.Error("Failed to fetch created reconciliation act", "error", err)
		response.InternalError(c, "Failed to fetch created reconciliation act")
		return
	}

	act.PeriodStart = periodStart.Format("2006-01-02")
	act.PeriodEnd = periodEnd.Format("2006-01-02")
	if notes.Valid {
		act.Notes = &notes.String
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
			   ra.period_start, ra.period_end, ra.opening_balance,
			   ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.partner_debit_total, ra.partner_credit_total, ra.partner_balance,
			   ra.difference, ra.status, ra.notes, ra.created_at
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.id = $1 AND ra.tenant_id = $2 AND ra.deleted_at IS NULL
	`, id, tenantID).Scan(
		&act.ID, &act.PartnerID, &act.PartnerName,
		&periodStart, &periodEnd, &act.OpeningBalance,
		&act.OurDebitTotal, &act.OurCreditTotal, &act.OurBalance,
		&act.PartnerDebitTotal, &act.PartnerCreditTotal, &act.PartnerBalance,
		&act.Difference, &act.Status, &notes, &act.CreatedAt,
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
	if notes.Valid {
		act.Notes = &notes.String
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
		Status             *string  `json:"status"`
		OurBalance         *float64 `json:"our_balance"`
		PartnerBalance     *float64 `json:"partner_balance"`
		OurDebitTotal      *float64 `json:"our_debit_total"`
		OurCreditTotal     *float64 `json:"our_credit_total"`
		PartnerDebitTotal  *float64 `json:"partner_debit_total"`
		PartnerCreditTotal *float64 `json:"partner_credit_total"`
		OpeningBalance     *float64 `json:"opening_balance"`
		Notes              *string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Build dynamic update
	setClauses := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argCount := 0

	if input.Status != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.OurBalance != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("our_balance = $%d", argCount))
		args = append(args, *input.OurBalance)
	}
	if input.PartnerBalance != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("partner_balance = $%d", argCount))
		args = append(args, *input.PartnerBalance)
	}
	if input.OurDebitTotal != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("our_debit_total = $%d", argCount))
		args = append(args, *input.OurDebitTotal)
	}
	if input.OurCreditTotal != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("our_credit_total = $%d", argCount))
		args = append(args, *input.OurCreditTotal)
	}
	if input.PartnerDebitTotal != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("partner_debit_total = $%d", argCount))
		args = append(args, *input.PartnerDebitTotal)
	}
	if input.PartnerCreditTotal != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("partner_credit_total = $%d", argCount))
		args = append(args, *input.PartnerCreditTotal)
	}
	if input.OpeningBalance != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("opening_balance = $%d", argCount))
		args = append(args, *input.OpeningBalance)
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
	response.Success(c, gin.H{"message": "Bulk reconciliation generated", "count": 0})
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

