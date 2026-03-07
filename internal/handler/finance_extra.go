package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/infrastructure/email"
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

func (h *Handler) SyncCurrencyRates(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Fetch rates from CBU (Central Bank of Uzbekistan)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://cbu.uz/uz/arkhiv-kursov-valyut/json/")
	if err != nil {
		h.log.Error("Failed to fetch CBU rates", "error", err)
		response.InternalError(c, "Failed to connect to Central Bank API")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.log.Error("Failed to read CBU response", "error", err)
		response.InternalError(c, "Failed to read Central Bank response")
		return
	}

	var cbuRates []struct {
		Code  string `json:"Ccy"`
		Rate  string `json:"Rate"`
		Date  string `json:"Date"`
		Title string `json:"CcyNm_UZ"`
	}
	if err := json.Unmarshal(body, &cbuRates); err != nil {
		h.log.Error("Failed to parse CBU rates", "error", err)
		response.InternalError(c, "Failed to parse Central Bank data")
		return
	}

	// Get base currency (UZS)
	var baseCurrencyID uuid.UUID
	err = h.db.QueryRow("SELECT id FROM currencies WHERE is_base_currency = true LIMIT 1").Scan(&baseCurrencyID)
	if err != nil {
		err = h.db.QueryRow("SELECT id FROM currencies WHERE code = 'UZS' LIMIT 1").Scan(&baseCurrencyID)
	}
	if err != nil {
		h.log.Error("No base currency found", "error", err)
		response.InternalError(c, "No base currency (UZS) found. Please create UZS currency first.")
		return
	}

	today := time.Now().Format("2006-01-02")
	synced := 0

	for _, cbuRate := range cbuRates {
		// Check if we have this currency
		var currencyID uuid.UUID
		err := h.db.QueryRow("SELECT id FROM currencies WHERE code = $1", cbuRate.Code).Scan(&currencyID)
		if err != nil {
			continue // Skip currencies we don't track
		}

		// Parse rate
		var rate float64
		if _, err := fmt.Sscanf(cbuRate.Rate, "%f", &rate); err != nil || rate <= 0 {
			continue
		}

		// Upsert exchange rate for today
		_, err = h.db.Exec(`
			INSERT INTO exchange_rates (id, tenant_id, from_currency_id, to_currency_id, rate, effective_date, source)
			VALUES ($1, $2, $3, $4, $5, $6, 'CBU')
			ON CONFLICT (tenant_id, from_currency_id, to_currency_id, effective_date)
			DO UPDATE SET rate = $5, source = 'CBU'
		`, uuid.New(), tenantID, currencyID, baseCurrencyID, rate, today)
		if err != nil {
			h.log.Error("Failed to upsert rate", "currency", cbuRate.Code, "error", err)
			continue
		}
		synced++
	}

	response.Success(c, gin.H{
		"message":      fmt.Sprintf("Synced %d exchange rates from CBU", synced),
		"synced_count": synced,
		"date":         today,
		"source":       "CBU",
	})
}
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
	// Response tracking
	ResponseStatus *string    `json:"response_status,omitempty"`
	RespondedAt    *time.Time `json:"responded_at,omitempty"`
	DisputeNote    *string    `json:"dispute_note,omitempty"`
	RespondentName *string    `json:"respondent_name,omitempty"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	SentVia        *string    `json:"sent_via,omitempty"`
	SentTo         *string    `json:"sent_to,omitempty"`
}

type reconciliationLine struct {
	Date           string  `json:"date"`
	Document       string  `json:"document"`
	Description    string  `json:"description"`
	Debit          float64 `json:"debit"`
	Credit         float64 `json:"credit"`
	RunningBalance float64 `json:"running_balance"`
}

// getUzDescription translates a journal entry source_type into an Uzbek description.
// If the source_type is not recognized, the original English description is returned.
func getUzDescription(sourceType, originalDescription string) string {
	uzDescriptions := map[string]string{
		// Xarid (Purchases)
		"purchase_invoice":         "Xarid fakturasi",
		"purchase_invoice_payment": "Xarid fakturasi to'lovi",
		"purchase_return":          "Xariddan qaytarish",
		"debit_note":               "Debet nota",
		// Sotuv (Sales)
		"sales_invoice":   "Sotuv fakturasi",
		"sales_order":     "Sotuv buyurtmasi",
		"sales_return":    "Sotuvdan qaytarish",
		"credit_note":     "Kredit nota",
		"payment_receipt": "To'lov qabul qilish",
		// To'lovlar (Payments)
		"payment": "To'lov",
		// Ombor (Inventory)
		"goods_receipt":        "Tovar qabul qilish",
		"inventory_adjustment": "Inventarizatsiya tuzatish",
		"scrap":                "Yaroqsizga chiqarish",
		"stock_count":          "Ombor sanab chiqish",
		"stock_operation":      "Ombor operatsiyasi",
		// Asosiy vositalar (Fixed Assets)
		"fixed_asset":    "Asosiy vosita kirim",
		"depreciation":   "Amortizatsiya",
		"disposal":       "Asosiy vosita chiqarish",
		"maintenance":    "Texnik xizmat",
		"asset_payment":  "Asosiy vosita to'lovi",
		// Qurilish (Construction)
		"construction_expense":          "Qurilish xarajati",
		"construction_expense_reversal": "Qurilish xarajati bekor qilish",
		"material_request":              "Material so'rovi",
		"project_commission":            "Loyiha komissiyasi",
		// Ish haqi (Payroll)
		"payroll":          "Ish haqi hisoblash",
		"salary_deduction": "Ish haqidan ushlab qolish",
		// Boshqa (Other)
		"expense":                       "Xarajat",
		"landed_cost":                   "Qo'shimcha xarajat",
		"bank_reconciliation":           "Bank solishtirma",
		"bank_reconciliation_writeoff":  "Bank farqi hisobdan chiqarish",
		"manual":                        "Qo'lda kiritilgan yozuv",
	}

	if uz, ok := uzDescriptions[sourceType]; ok {
		return uz
	}
	return originalDescription
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
			   COALESCE(je.source_type, ''), jel.debit_amount, jel.credit_amount
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
		var sourceType string
		err = rows.Scan(&entryDate, &l.Document, &l.Description, &sourceType, &l.Debit, &l.Credit)
		if err != nil {
			return
		}
		l.Date = entryDate.Format("2006-01-02")
		l.Description = getUzDescription(sourceType, l.Description)
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
			   ra.status, ra.notes, ra.created_at,
			   ra.response_status, ra.responded_at, ra.dispute_note, ra.respondent_name,
			   ra.sent_at, ra.sent_via, ra.sent_to
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
		var responseStatus, disputeNote, respondentName, sentVia, sentTo sql.NullString
		var respondedAt, sentAt sql.NullTime

		err := rows.Scan(
			&a.ID, &a.PartnerID, &a.PartnerName,
			&periodStart, &periodEnd,
			&a.OpeningBalance, &a.OurDebitTotal, &a.OurCreditTotal, &a.OurBalance,
			&a.Status, &notes, &a.CreatedAt,
			&responseStatus, &respondedAt, &disputeNote, &respondentName,
			&sentAt, &sentVia, &sentTo,
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
		if responseStatus.Valid {
			a.ResponseStatus = &responseStatus.String
		}
		if respondedAt.Valid {
			a.RespondedAt = &respondedAt.Time
		}
		if disputeNote.Valid {
			a.DisputeNote = &disputeNote.String
		}
		if respondentName.Valid {
			a.RespondentName = &respondentName.String
		}
		if sentAt.Valid {
			a.SentAt = &sentAt.Time
		}
		if sentVia.Valid {
			a.SentVia = &sentVia.String
		}
		if sentTo.Valid {
			a.SentTo = &sentTo.String
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
	var responseStatus, disputeNote, respondentName, sentVia, sentTo sql.NullString
	var respondedAt, sentAt sql.NullTime

	err = h.db.QueryRow(`
		SELECT ra.id, ra.partner_id, COALESCE(ct.name, '') as partner_name,
			   ra.period_start, ra.period_end,
			   ra.opening_balance, ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.status, ra.notes, ra.created_at,
			   ra.response_status, ra.responded_at, ra.dispute_note, ra.respondent_name,
			   ra.sent_at, ra.sent_via, ra.sent_to
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.id = $1 AND ra.tenant_id = $2 AND ra.deleted_at IS NULL
	`, id, tenantID).Scan(
		&act.ID, &act.PartnerID, &act.PartnerName,
		&periodStart, &periodEnd,
		&act.OpeningBalance, &act.OurDebitTotal, &act.OurCreditTotal, &act.OurBalance,
		&act.Status, &notes, &act.CreatedAt,
		&responseStatus, &respondedAt, &disputeNote, &respondentName,
		&sentAt, &sentVia, &sentTo,
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
	if responseStatus.Valid {
		act.ResponseStatus = &responseStatus.String
	}
	if respondedAt.Valid {
		act.RespondedAt = &respondedAt.Time
	}
	if disputeNote.Valid {
		act.DisputeNote = &disputeNote.String
	}
	if respondentName.Valid {
		act.RespondentName = &respondentName.String
	}
	if sentAt.Valid {
		act.SentAt = &sentAt.Time
	}
	if sentVia.Valid {
		act.SentVia = &sentVia.String
	}
	if sentTo.Valid {
		act.SentTo = &sentTo.String
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

	// Enforce status state machine
	if input.Status != nil {
		var currentStatus string
		err = h.db.QueryRow(`SELECT status FROM reconciliation_acts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&currentStatus)
		if err != nil {
			response.NotFound(c, "Reconciliation act")
			return
		}

		allowed := map[string][]string{
			"draft":     {"sent", "confirmed"},
			"sent":      {"confirmed", "disputed", "draft"},
			"confirmed": {"draft"},
			"disputed":  {"draft", "confirmed"},
		}

		valid := false
		for _, s := range allowed[currentStatus] {
			if s == *input.Status {
				valid = true
				break
			}
		}
		if !valid {
			response.BadRequest(c, fmt.Sprintf("'%s' holatidan '%s' holatiga o'tish mumkin emas", currentStatus, *input.Status))
			return
		}
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argCount := 0

	if input.Status != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)

		if *input.Status == "confirmed" {
			setClauses = append(setClauses, "confirmed_at = NOW()")
		}
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
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid act ID")
		return
	}

	act, lines, err := h.loadReconciliationActFull(tenantID, actID)
	if err != nil {
		response.NotFound(c, "Reconciliation act")
		return
	}

	html := h.renderReconciliationHTML(act, lines)
	c.Data(200, "text/html; charset=utf-8", []byte(html))
}

// SendReconciliationAct sends the act via email or generates a WhatsApp share link
func (h *Handler) SendReconciliationAct(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	actID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid act ID")
		return
	}

	var input struct {
		Via   string `json:"via" binding:"required"` // "email" or "whatsapp"
		Email string `json:"email"`
		Phone string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Generate share token if not already set
	var shareToken string
	err = h.db.QueryRow(`SELECT COALESCE(share_token, '') FROM reconciliation_acts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, actID, tenantID).Scan(&shareToken)
	if err != nil {
		response.NotFound(c, "Reconciliation act")
		return
	}

	if shareToken == "" {
		shareToken = uuid.New().String()[:8] + uuid.New().String()[:8]
		_, err = h.db.Exec(`UPDATE reconciliation_acts SET share_token = $1 WHERE id = $2 AND tenant_id = $3`, shareToken, actID, tenantID)
		if err != nil {
			h.log.Error("Failed to set share token", "error", err)
			response.InternalError(c, "Failed to generate share link")
			return
		}
	}

	shareURL := fmt.Sprintf("%s/shared/reconciliation/%s", h.config.App.FrontendURL, shareToken)
	now := time.Now()

	switch input.Via {
	case "email":
		if input.Email == "" {
			response.BadRequest(c, "Email is required")
			return
		}

		// Load act data for email
		act, lines, loadErr := h.loadReconciliationActFull(tenantID, actID)
		if loadErr != nil {
			response.InternalError(c, "Failed to load act data")
			return
		}

		closingBalance := act.OpeningBalance + act.OurDebitTotal - act.OurCreditTotal
		subject := fmt.Sprintf("Akt sverka — %s (%s – %s)", act.PartnerName, act.PeriodStart, act.PeriodEnd)

		emailBody := h.renderReconciliationEmailHTML(act, lines, closingBalance, shareURL)

		sendErr := h.emailService.Send(&email.Email{
			To:      []string{input.Email},
			Subject: subject,
			Body:    emailBody,
			IsHTML:  true,
		})
		if sendErr != nil {
			h.log.Error("Failed to send reconciliation email", "error", sendErr)
			response.InternalError(c, "Email yuborishda xatolik: "+sendErr.Error())
			return
		}

		_, _ = h.db.Exec(`UPDATE reconciliation_acts SET status = 'sent', sent_at = $1, sent_via = 'email', sent_to = $2 WHERE id = $3 AND tenant_id = $4`,
			now, input.Email, actID, tenantID)

		response.Success(c, gin.H{
			"message":   "Email muvaffaqiyatli yuborildi",
			"share_url": shareURL,
			"sent_to":   input.Email,
		})

	case "whatsapp":
		// For WhatsApp, we generate a share URL — the frontend opens WhatsApp with a pre-filled message
		act, _, loadErr := h.loadReconciliationActFull(tenantID, actID)
		if loadErr != nil {
			response.InternalError(c, "Failed to load act data")
			return
		}

		closingBalance := act.OpeningBalance + act.OurDebitTotal - act.OurCreditTotal
		message := fmt.Sprintf("Akt sverka: %s\nDavr: %s — %s\nDavr boshidagi qoldiq: %.2f\nJami debet: %.2f\nJami kredit: %.2f\nDavr oxiridagi qoldiq: %.2f\n\nBatafsil ko'rish: %s",
			act.PartnerName, act.PeriodStart, act.PeriodEnd,
			act.OpeningBalance, act.OurDebitTotal, act.OurCreditTotal, closingBalance, shareURL)

		_, _ = h.db.Exec(`UPDATE reconciliation_acts SET status = 'sent', sent_at = $1, sent_via = 'whatsapp', sent_to = $2 WHERE id = $3 AND tenant_id = $4`,
			now, input.Phone, actID, tenantID)

		response.Success(c, gin.H{
			"message":          "WhatsApp havolasi tayyor",
			"share_url":        shareURL,
			"whatsapp_message": message,
		})

	default:
		response.BadRequest(c, "Invalid send method. Use 'email' or 'whatsapp'")
	}
}

// GetPublicReconciliationAct returns a reconciliation act by share token (no auth required)
func (h *Handler) GetPublicReconciliationAct(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		response.BadRequest(c, "Token is required")
		return
	}

	var actID uuid.UUID
	var tenantID uuid.UUID
	var partnerName, periodStart, periodEnd, status string
	var openingBalance, ourDebitTotal, ourCreditTotal float64
	var notes, responseStatus sql.NullString

	err := h.db.QueryRow(`
		SELECT ra.id, ra.tenant_id, COALESCE(ct.name, ''), ra.period_start, ra.period_end,
			   ra.opening_balance, ra.our_debit_total, ra.our_credit_total, ra.status, ra.notes,
			   ra.response_status
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.share_token = $1 AND ra.deleted_at IS NULL
	`, token).Scan(&actID, &tenantID, &partnerName, &periodStart, &periodEnd,
		&openingBalance, &ourDebitTotal, &ourCreditTotal, &status, &notes, &responseStatus)
	if err != nil {
		response.NotFound(c, "Reconciliation act")
		return
	}

	// Load transaction lines
	var partnerID uuid.UUID
	_ = h.db.QueryRow(`SELECT partner_id FROM reconciliation_acts WHERE id = $1`, actID).Scan(&partnerID)

	var lines []reconciliationLine
	if partnerID != uuid.Nil {
		_, lines, _, _, _ = h.computeReconciliationData(tenantID, partnerID, nil, periodStart, periodEnd)
	}

	closingBalance := openingBalance + ourDebitTotal - ourCreditTotal

	canRespond := (status == "sent" || status == "confirmed") &&
		(!responseStatus.Valid || responseStatus.String == "" || responseStatus.String == "no_response")

	result := gin.H{
		"partner_name":     partnerName,
		"period_start":     periodStart,
		"period_end":       periodEnd,
		"opening_balance":  openingBalance,
		"our_debit_total":  ourDebitTotal,
		"our_credit_total": ourCreditTotal,
		"closing_balance":  closingBalance,
		"status":           status,
		"lines":            lines,
		"can_respond":      canRespond,
	}
	if notes.Valid {
		result["notes"] = notes.String
	}
	if responseStatus.Valid {
		result["response_status"] = responseStatus.String
	}

	response.Success(c, result)
}

// RespondReconciliationAct allows a counterparty to confirm or dispute an act via share token (public, no auth)
func (h *Handler) RespondReconciliationAct(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		response.BadRequest(c, "Token is required")
		return
	}

	var input struct {
		Action  string `json:"action" binding:"required"` // "confirm" or "dispute"
		Name    string `json:"name"`
		Note    string `json:"note"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	if input.Action != "confirm" && input.Action != "dispute" {
		response.BadRequest(c, "Action must be 'confirm' or 'dispute'")
		return
	}

	// Check act exists and is in sent status
	var actID uuid.UUID
	var currentStatus string
	var currentResponse sql.NullString
	err := h.db.QueryRow(`
		SELECT id, status, response_status FROM reconciliation_acts
		WHERE share_token = $1 AND deleted_at IS NULL
	`, token).Scan(&actID, &currentStatus, &currentResponse)
	if err != nil {
		response.NotFound(c, "Reconciliation act")
		return
	}

	// Only allow response if act was sent
	if currentStatus != "sent" && currentStatus != "confirmed" {
		response.BadRequest(c, "Bu aktga javob berish mumkin emas. Akt hali yuborilmagan.")
		return
	}

	// Don't allow re-responding if already responded
	if currentResponse.Valid && currentResponse.String != "" && currentResponse.String != "no_response" {
		response.BadRequest(c, "Bu aktga allaqachon javob berilgan.")
		return
	}

	responseStatus := "confirmed"
	newActStatus := "confirmed"
	if input.Action == "dispute" {
		responseStatus = "disputed"
		newActStatus = "disputed"
	}

	_, err = h.db.Exec(`
		UPDATE reconciliation_acts
		SET response_status = $1, responded_at = NOW(), respondent_name = $2, dispute_note = $3,
			status = $4, updated_at = NOW()
		WHERE id = $5
	`, responseStatus, nullStr(input.Name), nullStr(input.Note), newActStatus, actID)
	if err != nil {
		h.log.Error("Failed to record response", "error", err)
		response.InternalError(c, "Failed to record response")
		return
	}

	msg := "Akt muvaffaqiyatli tasdiqlandi"
	if input.Action == "dispute" {
		msg = "Norozilik muvaffaqiyatli yuborildi"
	}

	response.Success(c, gin.H{"message": msg, "response_status": responseStatus})
}

// loadReconciliationActFull loads act metadata + computed lines
func (h *Handler) loadReconciliationActFull(tenantID, actID uuid.UUID) (reconciliationActResponse, []reconciliationLine, error) {
	var act reconciliationActResponse
	var notes sql.NullString
	var periodStart, periodEnd time.Time
	var partnerID uuid.UUID

	err := h.db.QueryRow(`
		SELECT ra.id, ra.partner_id, COALESCE(ct.name, ''), ra.period_start, ra.period_end,
			   ra.opening_balance, ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.status, ra.notes, ra.created_at
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.id = $1 AND ra.tenant_id = $2 AND ra.deleted_at IS NULL
	`, actID, tenantID).Scan(
		&act.ID, &partnerID, &act.PartnerName,
		&periodStart, &periodEnd,
		&act.OpeningBalance, &act.OurDebitTotal, &act.OurCreditTotal, &act.OurBalance,
		&act.Status, &notes, &act.CreatedAt,
	)
	if err != nil {
		return act, nil, err
	}

	act.PartnerID = partnerID
	act.PeriodStart = periodStart.Format("2006-01-02")
	act.PeriodEnd = periodEnd.Format("2006-01-02")
	act.ClosingBalance = act.OpeningBalance + act.OurDebitTotal - act.OurCreditTotal
	if notes.Valid {
		act.Notes = &notes.String
	}

	_, lines, _, _, _ := h.computeReconciliationData(tenantID, partnerID, nil, act.PeriodStart, act.PeriodEnd)

	return act, lines, nil
}

// renderReconciliationHTML generates a printable HTML document for the act
func (h *Handler) renderReconciliationHTML(act reconciliationActResponse, lines []reconciliationLine) string {
	closingBalance := act.OpeningBalance + act.OurDebitTotal - act.OurCreditTotal

	var linesHTML strings.Builder
	for i, l := range lines {
		debitStr := ""
		creditStr := ""
		if l.Debit > 0 {
			debitStr = fmt.Sprintf("%.2f", l.Debit)
		}
		if l.Credit > 0 {
			creditStr = fmt.Sprintf("%.2f", l.Credit)
		}
		linesHTML.WriteString(fmt.Sprintf(`<tr><td style="text-align:center">%d</td><td>%s</td><td>%s</td><td>%s</td><td style="text-align:right">%s</td><td style="text-align:right">%s</td></tr>`,
			i+1, l.Date, l.Document, l.Description, debitStr, creditStr))
	}

	obDebit, obCredit := "", ""
	if act.OpeningBalance >= 0 {
		obDebit = fmt.Sprintf("%.2f", act.OpeningBalance)
	} else {
		obCredit = fmt.Sprintf("%.2f", -act.OpeningBalance)
	}

	cbDebit, cbCredit := "", ""
	if closingBalance >= 0 {
		cbDebit = fmt.Sprintf("%.2f", closingBalance)
	} else {
		cbCredit = fmt.Sprintf("%.2f", -closingBalance)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<title>Akt sverka - %s</title>
<style>
body{font-family:'Times New Roman',serif;padding:40px;color:#000;font-size:13px}
h1{text-align:center;font-size:16px;margin-bottom:2px;text-transform:uppercase}
h2{text-align:center;font-size:13px;font-weight:normal;color:#333;margin-bottom:20px}
table{width:100%%;border-collapse:collapse;margin-top:12px}
th,td{border:1px solid #000;padding:4px 8px;font-size:12px}
th{background:#f5f5f5;text-align:center;font-weight:bold}
.info-row{display:flex;justify-content:space-between;margin-bottom:4px;font-size:13px}
.signatures{display:flex;justify-content:space-between;margin-top:60px}
.signatures div{width:45%%}
.sig-line{border-bottom:1px solid #000;margin-top:30px;margin-bottom:4px}
.totals td{font-weight:bold}
@media print{body{padding:20px}}
</style>
</head>
<body>
<h1>SOLISHTIRMA DALOLATNOMA (AKT SVERKA)</h1>
<h2>O'zaro hisob-kitoblarni solishtirib tekshirish dalolatnomasi</h2>
<div class="info-row"><span><strong>Kontragent:</strong> %s</span><span><strong>Davr:</strong> %s — %s</span></div>
<table>
<thead><tr><th style="width:30px">No</th><th style="width:90px">Sana</th><th style="width:120px">Hujjat</th><th>Tavsif</th><th style="width:120px">Debet</th><th style="width:120px">Kredit</th></tr></thead>
<tbody>
<tr class="totals" style="background:#f9f9f9"><td colspan="4">Davr boshidagi qoldiq</td><td style="text-align:right">%s</td><td style="text-align:right">%s</td></tr>
%s
<tr class="totals" style="background:#f0f0f0"><td colspan="4">Davr bo'yicha aylanma</td><td style="text-align:right">%.2f</td><td style="text-align:right">%.2f</td></tr>
<tr class="totals" style="background:#e8e8e8"><td colspan="4">Davr oxiridagi qoldiq</td><td style="text-align:right">%s</td><td style="text-align:right">%s</td></tr>
</tbody>
</table>
<div class="signatures">
<div><p><strong>Tashkilot nomidan:</strong></p><div class="sig-line"></div><p style="font-size:11px;color:#666">F.I.O., imzo, muhr</p></div>
<div><p><strong>Kontragent nomidan:</strong></p><div class="sig-line"></div><p style="font-size:11px;color:#666">F.I.O., imzo, muhr</p></div>
</div>
</body>
</html>`,
		act.PartnerName,
		act.PartnerName, act.PeriodStart, act.PeriodEnd,
		obDebit, obCredit,
		linesHTML.String(),
		act.OurDebitTotal, act.OurCreditTotal,
		cbDebit, cbCredit,
	)
}

// renderReconciliationEmailHTML generates an email-friendly HTML for the act
func (h *Handler) renderReconciliationEmailHTML(act reconciliationActResponse, lines []reconciliationLine, closingBalance float64, shareURL string) string {
	var linesHTML strings.Builder
	for i, l := range lines {
		debitStr, creditStr := "-", "-"
		if l.Debit > 0 {
			debitStr = fmt.Sprintf("%.2f", l.Debit)
		}
		if l.Credit > 0 {
			creditStr = fmt.Sprintf("%.2f", l.Credit)
		}
		bg := "#fff"
		if i%2 == 0 {
			bg = "#f9f9f9"
		}
		linesHTML.WriteString(fmt.Sprintf(`<tr style="background:%s"><td style="padding:6px 8px;border-bottom:1px solid #eee;text-align:center;font-size:12px">%d</td><td style="padding:6px 8px;border-bottom:1px solid #eee;font-size:12px">%s</td><td style="padding:6px 8px;border-bottom:1px solid #eee;font-size:12px">%s</td><td style="padding:6px 8px;border-bottom:1px solid #eee;font-size:12px">%s</td><td style="padding:6px 8px;border-bottom:1px solid #eee;text-align:right;font-size:12px">%s</td><td style="padding:6px 8px;border-bottom:1px solid #eee;text-align:right;font-size:12px">%s</td></tr>`,
			bg, i+1, l.Date, l.Document, l.Description, debitStr, creditStr))
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;line-height:1.6;color:#333;max-width:700px;margin:0 auto;padding:20px">
<div style="background:linear-gradient(135deg,#667eea 0%%,#764ba2 100%%);padding:24px;border-radius:10px 10px 0 0;text-align:center">
<h1 style="color:white;margin:0;font-size:20px">Solishtirma dalolatnoma (Akt sverka)</h1>
</div>
<div style="background:#fff;padding:24px;border:1px solid #e0e0e0;border-top:none;border-radius:0 0 10px 10px">
<p style="margin-top:0"><strong>Kontragent:</strong> %s</p>
<p><strong>Davr:</strong> %s — %s</p>

<div style="display:flex;gap:12px;margin:16px 0">
<div style="flex:1;background:#f0f7ff;padding:12px;border-radius:8px;text-align:center">
<div style="font-size:11px;color:#666">Davr boshidagi qoldiq</div>
<div style="font-size:18px;font-weight:bold;color:#333">%.2f</div>
</div>
<div style="flex:1;background:#f0f7ff;padding:12px;border-radius:8px;text-align:center">
<div style="font-size:11px;color:#666">Jami debet</div>
<div style="font-size:18px;font-weight:bold;color:#2563eb">%.2f</div>
</div>
<div style="flex:1;background:#fff0f0;padding:12px;border-radius:8px;text-align:center">
<div style="font-size:11px;color:#666">Jami kredit</div>
<div style="font-size:18px;font-weight:bold;color:#dc2626">%.2f</div>
</div>
<div style="flex:1;background:#f0fff0;padding:12px;border-radius:8px;text-align:center">
<div style="font-size:11px;color:#666">Davr oxiridagi qoldiq</div>
<div style="font-size:18px;font-weight:bold;color:#333">%.2f</div>
</div>
</div>

<table style="width:100%%;border-collapse:collapse;margin-top:16px">
<thead><tr style="background:#f5f5f5">
<th style="padding:8px;border-bottom:2px solid #ddd;text-align:center;font-size:12px">No</th>
<th style="padding:8px;border-bottom:2px solid #ddd;text-align:left;font-size:12px">Sana</th>
<th style="padding:8px;border-bottom:2px solid #ddd;text-align:left;font-size:12px">Hujjat</th>
<th style="padding:8px;border-bottom:2px solid #ddd;text-align:left;font-size:12px">Tavsif</th>
<th style="padding:8px;border-bottom:2px solid #ddd;text-align:right;font-size:12px">Debet</th>
<th style="padding:8px;border-bottom:2px solid #ddd;text-align:right;font-size:12px">Kredit</th>
</tr></thead>
<tbody>%s</tbody>
</table>

<div style="text-align:center;margin-top:24px">
<a href="%s" style="background:linear-gradient(135deg,#667eea 0%%,#764ba2 100%%);color:white;padding:12px 28px;text-decoration:none;border-radius:8px;font-weight:600;display:inline-block">Batafsil ko'rish</a>
</div>

<hr style="border:none;border-top:1px solid #e0e0e0;margin:20px 0">
<p style="color:#999;font-size:12px;margin-bottom:0">GenixERP - Zamonaviy ERP tizimi</p>
</div>
</body>
</html>`,
		act.PartnerName, act.PeriodStart, act.PeriodEnd,
		act.OpeningBalance, act.OurDebitTotal, act.OurCreditTotal, closingBalance,
		linesHTML.String(), shareURL,
	)
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

