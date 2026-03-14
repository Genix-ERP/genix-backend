package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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

		// Get previous rate before updating
		var previousRate sql.NullFloat64
		h.db.QueryRow(`
			SELECT rate FROM exchange_rates
			WHERE tenant_id = $1 AND from_currency_id = $2 AND to_currency_id = $3
			ORDER BY effective_date DESC LIMIT 1
		`, tenantID, currencyID, baseCurrencyID).Scan(&previousRate)

		var prevRate, rateChange, rateChangePct float64
		if previousRate.Valid && previousRate.Float64 > 0 {
			prevRate = previousRate.Float64
			rateChange = rate - prevRate
			rateChangePct = (rateChange / prevRate) * 100
		}

		// Upsert exchange rate for today with previous rate tracking
		_, err = h.db.Exec(`
			INSERT INTO exchange_rates (id, tenant_id, from_currency_id, to_currency_id, rate, effective_date, source, previous_rate, rate_change, rate_change_percent)
			VALUES ($1, $2, $3, $4, $5, $6, 'CBU', $7, $8, $9)
			ON CONFLICT (tenant_id, from_currency_id, to_currency_id, effective_date)
			DO UPDATE SET rate = $5, source = 'CBU', previous_rate = $7, rate_change = $8, rate_change_percent = $9
		`, uuid.New(), tenantID, currencyID, baseCurrencyID, rate, today, prevRate, rateChange, rateChangePct)
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
// RunCurrencySyncScheduler starts a background goroutine that syncs CBU rates daily at 09:00 Tashkent time
func (h *Handler) RunCurrencySyncScheduler(ctx context.Context) {
	go func() {
		loc, err := time.LoadLocation("Asia/Tashkent")
		if err != nil {
			h.log.Error("Failed to load Asia/Tashkent timezone, using UTC+5 offset", "error", err)
			loc = time.FixedZone("UZT", 5*60*60)
		}

		for {
			now := time.Now().In(loc)
			// Next 09:00 Tashkent time
			next := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, loc)
			if now.After(next) {
				next = next.Add(24 * time.Hour)
			}
			sleepDuration := next.Sub(now)
			h.log.Info("Currency sync scheduled", "next_run", next.Format("2006-01-02 15:04"), "sleep", sleepDuration.Round(time.Minute))

			select {
			case <-time.After(sleepDuration):
				h.syncCBURatesForAllTenants()
			case <-ctx.Done():
				h.log.Info("Currency sync scheduler stopped")
				return
			}
		}
	}()
	h.log.Info("Currency sync scheduler started (daily at 09:00 Tashkent time)")
}

// syncCBURatesForAllTenants fetches CBU rates and applies them to all tenants
func (h *Handler) syncCBURatesForAllTenants() {
	h.log.Info("Starting daily CBU currency sync for all tenants")

	// Fetch rates from CBU
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://cbu.uz/uz/arkhiv-kursov-valyut/json/")
	if err != nil {
		h.log.Error("Daily sync: failed to fetch CBU rates", "error", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.log.Error("Daily sync: failed to read CBU response", "error", err)
		return
	}

	var cbuRates []struct {
		Code  string `json:"Ccy"`
		Rate  string `json:"Rate"`
		Date  string `json:"Date"`
		Title string `json:"CcyNm_UZ"`
	}
	if err := json.Unmarshal(body, &cbuRates); err != nil {
		h.log.Error("Daily sync: failed to parse CBU rates", "error", err)
		return
	}

	// Get all tenant IDs
	rows, err := h.db.Query("SELECT id FROM tenants WHERE deleted_at IS NULL")
	if err != nil {
		h.log.Error("Daily sync: failed to query tenants", "error", err)
		return
	}
	defer rows.Close()

	var tenantIDs []uuid.UUID
	for rows.Next() {
		var tid uuid.UUID
		if err := rows.Scan(&tid); err == nil {
			tenantIDs = append(tenantIDs, tid)
		}
	}

	today := time.Now().Format("2006-01-02")
	totalSynced := 0

	// Pre-parse all CBU rates into a usable slice
	type parsedRate struct {
		Code string
		Rate float64
	}
	var validRates []parsedRate
	for _, cbuRate := range cbuRates {
		var rate float64
		if _, err := fmt.Sscanf(cbuRate.Rate, "%f", &rate); err != nil || rate <= 0 {
			continue
		}
		validRates = append(validRates, parsedRate{Code: cbuRate.Code, Rate: rate})
	}

	for _, tenantID := range tenantIDs {
		// Get base currency for this tenant
		var baseCurrencyID uuid.UUID
		err := h.db.QueryRow("SELECT id FROM currencies WHERE tenant_id = $1 AND is_base_currency = true LIMIT 1", tenantID).Scan(&baseCurrencyID)
		if err != nil {
			h.db.QueryRow("SELECT id FROM currencies WHERE tenant_id = $1 AND code = 'UZS' LIMIT 1", tenantID).Scan(&baseCurrencyID)
		}
		if baseCurrencyID == uuid.Nil {
			continue
		}

		// ONE query: get all currencies for this tenant
		currencyMap := make(map[string]uuid.UUID)
		curRows, curErr := h.db.Query("SELECT id, code FROM currencies WHERE tenant_id = $1", tenantID)
		if curErr != nil {
			continue
		}
		for curRows.Next() {
			var cid uuid.UUID
			var code string
			if err := curRows.Scan(&cid, &code); err == nil {
				currencyMap[code] = cid
			}
		}
		curRows.Close()

		// Build batch INSERT with ON CONFLICT for exchange rates
		var erValues []string
		var erArgs []interface{}
		argIdx := 0
		synced := 0
		for _, vr := range validRates {
			currencyID, ok := currencyMap[vr.Code]
			if !ok {
				continue
			}
			erValues = append(erValues, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,'CBU')",
				argIdx+1, argIdx+2, argIdx+3, argIdx+4, argIdx+5, argIdx+6))
			erArgs = append(erArgs, uuid.New(), tenantID, currencyID, baseCurrencyID, vr.Rate, today)
			argIdx += 6
			synced++
		}

		// ONE INSERT for all exchange rates with previous_rate tracking
		if len(erValues) > 0 {
			h.db.Exec(`
				INSERT INTO exchange_rates (id, tenant_id, from_currency_id, to_currency_id, rate, effective_date, source)
				VALUES `+strings.Join(erValues, ",")+`
				ON CONFLICT (tenant_id, from_currency_id, to_currency_id, effective_date)
				DO UPDATE SET
					previous_rate = exchange_rates.rate,
					rate_change = EXCLUDED.rate - exchange_rates.rate,
					rate_change_percent = CASE WHEN exchange_rates.rate > 0 THEN ((EXCLUDED.rate - exchange_rates.rate) / exchange_rates.rate) * 100 ELSE 0 END,
					rate = EXCLUDED.rate,
					source = 'CBU'
			`, erArgs...)
		}
		totalSynced += synced
	}

	h.log.Info("Daily CBU sync completed", "tenants", len(tenantIDs), "total_rates_synced", totalSynced)
}

func (h *Handler) RevalueCurrency(c *gin.Context)   { response.Success(c, gin.H{"message": "Currency revaluation completed"}) }
func (h *Handler) ListExchangeDiffs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	dateFrom := c.DefaultQuery("date_from", "2020-01-01")
	dateTo := c.DefaultQuery("date_to", time.Now().Format("2006-01-02"))

	query := `
		SELECT ed.id, ed.currency_id, COALESCE(cur.code, '') as currency_code,
			   ed.amount_uzs, ed.diff_type, ed.period_start, ed.description,
			   ed.journal_entry_id, ed.created_at,
			   COALESCE(ed.document_number, '') as document_number,
			   COALESCE(ed.counterparty_name, '') as counterparty_name,
			   COALESCE(ed.foreign_amount, 0) as foreign_amount,
			   COALESCE(ed.initial_rate, 0) as initial_rate,
			   COALESCE(ed.final_rate, 0) as final_rate
		FROM exchange_diffs ed
		LEFT JOIN currencies cur ON ed.currency_id = cur.id
		WHERE ed.tenant_id = $1 AND ed.deleted_at IS NULL
		  AND ed.period_start >= $2 AND ed.period_start <= $3
		ORDER BY ed.period_start DESC`

	rows, err := h.db.Query(query, tenantID, dateFrom, dateTo)
	if err != nil {
		h.log.Error("Failed to list exchange diffs", "error", err)
		response.InternalError(c, "Failed to list exchange diffs")
		return
	}
	defer rows.Close()

	var results []map[string]interface{}
	var totalGain, totalLoss float64

	for rows.Next() {
		var edID, currencyID uuid.UUID
		var currencyCode, diffType, description string
		var documentNumber, counterpartyName string
		var amount, foreignAmount, initialRate, finalRate float64
		var periodStart, createdAt time.Time
		var journalEntryID sql.NullString

		if err := rows.Scan(&edID, &currencyID, &currencyCode, &amount, &diffType, &periodStart, &description, &journalEntryID, &createdAt,
			&documentNumber, &counterpartyName, &foreignAmount, &initialRate, &finalRate); err != nil {
			continue
		}

		item := map[string]interface{}{
			"id":                edID.String(),
			"currency_code":    currencyCode,
			"amount":           amount,
			"type":             diffType,
			"date":             periodStart.Format("2006-01-02"),
			"description":      description,
			"created_at":       createdAt,
			"document_number":  documentNumber,
			"counterparty":     counterpartyName,
			"foreign_amount":   foreignAmount,
			"initial_rate":     initialRate,
			"final_rate":       finalRate,
		}
		if journalEntryID.Valid {
			item["journal_entry_id"] = journalEntryID.String
		}
		results = append(results, item)

		if diffType == "positive" {
			totalGain += amount
		} else {
			totalLoss += amount
		}
	}

	response.Success(c, gin.H{
		"items":      results,
		"total_gain": totalGain,
		"total_loss": totalLoss,
		"net":        totalGain - totalLoss,
	})
}

// ========== CURRENCY DEBT REPORT (Valyutadagi qarzdorlik) ==========

func (h *Handler) CurrencyDebtReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgFilter string
	var args []interface{}
	args = append(args, tenantID) // $1

	if orgID != uuid.Nil {
		orgFilter = " AND organization_id = $2"
		args = append(args, orgID)
	}

	nextParam := len(args) + 1

	// Get current exchange rates for each foreign currency
	type currentRate struct {
		CurrencyID uuid.UUID
		Code       string
		Rate       float64
	}
	rateRows, err := h.db.Query(`
		SELECT DISTINCT ON (c.id) c.id, c.code, er.rate
		FROM currencies c
		JOIN exchange_rates er ON er.from_currency_id = c.id AND er.tenant_id = $1
		WHERE c.tenant_id = $1 AND c.is_base_currency = false AND c.deleted_at IS NULL
		ORDER BY c.id, er.effective_date DESC
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to get current rates", "error", err)
		response.InternalError(c, "Failed to get current rates")
		return
	}
	defer rateRows.Close()

	currentRates := map[uuid.UUID]currentRate{}
	for rateRows.Next() {
		var cr currentRate
		if err := rateRows.Scan(&cr.CurrencyID, &cr.Code, &cr.Rate); err == nil {
			currentRates[cr.CurrencyID] = cr
		}
	}

	var items []map[string]interface{}
	var totalInvoiceUZS, totalCurrentUZS, totalDiff float64

	// Sales invoices with foreign currency and amount_due > 0
	salesQuery := fmt.Sprintf(`
		SELECT si.id, si.invoice_number, 'sales' as type,
			COALESCE(c.code, '') as currency_code, si.currency_id,
			si.exchange_rate, si.total_amount, (si.total_amount - si.amount_paid) as amount_due,
			COALESCE(cu.name, '') as partner_name, si.invoice_date
		FROM sales_invoices si
		LEFT JOIN currencies c ON si.currency_id = c.id
		LEFT JOIN customers cu ON si.customer_id = cu.id
		WHERE si.tenant_id = $1 %s
			AND si.currency_id IS NOT NULL
			AND si.exchange_rate > 1
			AND (si.total_amount - si.amount_paid) > 0.01
			AND si.status NOT IN ('cancelled', 'draft')
			AND si.deleted_at IS NULL
	`, orgFilter)

	salesRows, err := h.db.Query(salesQuery, args...)
	if err != nil {
		h.log.Error("Failed to query sales invoices for currency debt", "error", err)
	} else {
		defer salesRows.Close()
		for salesRows.Next() {
			var id uuid.UUID
			var invoiceNumber, invType, currencyCode, partnerName string
			var currencyID uuid.UUID
			var exchangeRate, totalAmount, amountDue float64
			var invoiceDate time.Time

			if err := salesRows.Scan(&id, &invoiceNumber, &invType, &currencyCode, &currencyID, &exchangeRate, &totalAmount, &amountDue, &partnerName, &invoiceDate); err != nil {
				continue
			}

			invoiceUZS := amountDue * exchangeRate
			cr := currentRates[currencyID]
			currentUZS := amountDue * cr.Rate
			if cr.Rate == 0 {
				currentUZS = invoiceUZS
			}
			diff := currentUZS - invoiceUZS

			items = append(items, map[string]interface{}{
				"id":             id.String(),
				"invoice_number": invoiceNumber,
				"type":           "sales",
				"currency_code":  currencyCode,
				"partner_name":   partnerName,
				"invoice_date":   invoiceDate.Format("2006-01-02"),
				"amount_due":     amountDue,
				"invoice_rate":   exchangeRate,
				"current_rate":   cr.Rate,
				"invoice_uzs":    invoiceUZS,
				"current_uzs":    currentUZS,
				"diff":           diff,
			})
			totalInvoiceUZS += invoiceUZS
			totalCurrentUZS += currentUZS
			totalDiff += diff
		}
	}

	// Purchase invoices with foreign currency and amount_due > 0
	purchaseQuery := fmt.Sprintf(`
		SELECT pi.id, pi.bill_number, 'purchase' as type,
			COALESCE(c.code, '') as currency_code, pi.currency_id,
			COALESCE(pi.exchange_rate, 1) as exchange_rate, pi.total_amount, (pi.total_amount - pi.amount_paid) as amount_due,
			COALESCE(s.name, '') as partner_name, pi.bill_date
		FROM purchase_invoices pi
		LEFT JOIN currencies c ON pi.currency_id = c.id
		LEFT JOIN suppliers s ON pi.supplier_id = s.id
		WHERE pi.tenant_id = $1 %s
			AND pi.currency_id IS NOT NULL
			AND COALESCE(pi.exchange_rate, 1) > 1
			AND (pi.total_amount - pi.amount_paid) > 0.01
			AND pi.status NOT IN ('cancelled', 'draft')
			AND pi.deleted_at IS NULL
	`, orgFilter)

	_ = nextParam // args already set

	purchaseRows, err := h.db.Query(purchaseQuery, args...)
	if err != nil {
		h.log.Error("Failed to query purchase invoices for currency debt", "error", err)
	} else {
		defer purchaseRows.Close()
		for purchaseRows.Next() {
			var id uuid.UUID
			var invoiceNumber, invType, currencyCode, partnerName string
			var currencyID uuid.UUID
			var exchangeRate, totalAmount, amountDue float64
			var invoiceDate time.Time

			if err := purchaseRows.Scan(&id, &invoiceNumber, &invType, &currencyCode, &currencyID, &exchangeRate, &totalAmount, &amountDue, &partnerName, &invoiceDate); err != nil {
				continue
			}

			invoiceUZS := amountDue * exchangeRate
			cr := currentRates[currencyID]
			currentUZS := amountDue * cr.Rate
			if cr.Rate == 0 {
				currentUZS = invoiceUZS
			}
			diff := currentUZS - invoiceUZS

			items = append(items, map[string]interface{}{
				"id":             id.String(),
				"invoice_number": invoiceNumber,
				"type":           "purchase",
				"currency_code":  currencyCode,
				"partner_name":   partnerName,
				"invoice_date":   invoiceDate.Format("2006-01-02"),
				"amount_due":     amountDue,
				"invoice_rate":   exchangeRate,
				"current_rate":   cr.Rate,
				"invoice_uzs":    invoiceUZS,
				"current_uzs":    currentUZS,
				"diff":           diff,
			})
			totalInvoiceUZS += invoiceUZS
			totalCurrentUZS += currentUZS
			totalDiff += diff
		}
	}

	if items == nil {
		items = []map[string]interface{}{}
	}

	response.Success(c, gin.H{
		"items":           items,
		"total_invoice_uzs": totalInvoiceUZS,
		"total_current_uzs": totalCurrentUZS,
		"total_diff":        totalDiff,
	})
}

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
	SentAt          *time.Time `json:"sent_at,omitempty"`
	SentVia         *string    `json:"sent_via,omitempty"`
	SentTo          *string    `json:"sent_to,omitempty"`
	DisputeAmount   *float64   `json:"dispute_amount,omitempty"`
	ShareExpiresAt  *time.Time `json:"share_expires_at,omitempty"`
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
// htmlToPDF converts HTML content to PDF using wkhtmltopdf
func htmlToPDF(htmlContent string) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "reconciliation-*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(htmlContent); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write HTML: %w", err)
	}
	tmpFile.Close()

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("wkhtmltopdf", "--quiet", "--encoding", "UTF-8", "--page-size", "A4", "--margin-top", "10mm", "--margin-bottom", "10mm", tmpFile.Name(), "-")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("wkhtmltopdf failed: %w, stderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

func getUzDescription(sourceType, originalDescription string) string {
	uzDescriptions := map[string]string{
		// Xarid (Purchases)
		"purchase_invoice":         "Xarid fakturasi",
		"purchase_invoice_payment": "Xarid fakturasi to'lovi",
		"purchase_return":          "Xariddan qaytarish",
		"debit_note":               "Debet nota",
		// Sotuv (Sales)
		"sales_invoice":      "Sotuv fakturasi",
		"sales_order":        "Sotuv buyurtmasi",
		"sales_return":       "Sotuvdan qaytarish",
		"sales_return_refund": "Sotuvdan qaytarish to'lovi",
		"credit_note":        "Kredit nota",
		"payment_receipt":    "To'lov qabul qilish",
		// To'lovlar (Payments)
		"payment": "To'lov",
		// Ombor (Inventory)
		"goods_receipt":        "Tovar qabul qilish",
		"inventory_adjustment": "Inventarizatsiya tuzatish",
		"inventory_shortage":   "Inventarizatsiya kamomadi",
		"scrap":                "Yaroqsizga chiqarish",
		"stock_count":          "Ombor sanab chiqish",
		"stock_operation":      "Ombor operatsiyasi",
		// Asosiy vositalar (Fixed Assets)
		"fixed_asset":   "Asosiy vosita kirim",
		"depreciation":  "Amortizatsiya",
		"disposal":      "Asosiy vosita chiqarish",
		"maintenance":   "Texnik xizmat",
		"asset_payment": "Asosiy vosita to'lovi",
		// Qurilish (Construction)
		"construction_expense":          "Qurilish xarajati",
		"construction_expense_reversal": "Qurilish xarajati bekor qilish",
		"material_request":              "Material so'rovi",
		"project_commission":            "Loyiha komissiyasi",
		// Ishlab chiqarish (Manufacturing)
		"production_complete": "Ishlab chiqarish yakunlandi",
		// Ish haqi (Payroll)
		"payroll":          "Ish haqi hisoblash",
		"salary_deduction": "Ish haqidan ushlab qolish",
		// Buyurtmalar (Orders)
		"purchase_order": "Xarid buyurtmasi",
		// Tizim (System)
		"opening_balance": "Boshlang'ich qoldiq",
		// Boshqa (Other)
		"expense":                      "Xarajat",
		"landed_cost":                  "Qo'shimcha xarajat",
		"bank_reconciliation":          "Bank solishtirma",
		"bank_reconciliation_writeoff": "Bank farqi hisobdan chiqarish",
		"manual":                       "Qo'lda kiritilgan yozuv",
	}

	if uz, ok := uzDescriptions[sourceType]; ok {
		return uz
	}

	// Fallback: translate common English patterns in the description text
	descLower := strings.ToLower(originalDescription)
	descPatterns := []struct {
		pattern     string
		replacement string
	}{
		{"purchase order", "Xarid buyurtmasi"},
		{"goods delivery", "Tovar yetkazish"},
		{"sales invoice", "Sotuv fakturasi"},
		{"payment received", "To'lov qabul qilindi"},
		{"vendor bill", "Yetkazuvchi fakturasi"},
		{"credit note", "Kredit nota"},
		{"stock adjustment", "Inventarizatsiya tuzatish"},
		{"opening balance", "Boshlang'ich qoldiq"},
		{"goods receipt", "Tovar qabul qilish"},
		{"purchase invoice", "Xarid fakturasi"},
	}
	for _, p := range descPatterns {
		if strings.Contains(descLower, p.pattern) {
			return p.replacement
		}
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
	var respondedAt, sentAt, actShareExpiresAt sql.NullTime
	var actDisputeAmount sql.NullFloat64

	err = h.db.QueryRow(`
		SELECT ra.id, ra.partner_id, COALESCE(ct.name, '') as partner_name,
			   ra.period_start, ra.period_end,
			   ra.opening_balance, ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.status, ra.notes, ra.created_at,
			   ra.response_status, ra.responded_at, ra.dispute_note, ra.respondent_name,
			   ra.sent_at, ra.sent_via, ra.sent_to,
			   ra.dispute_amount, ra.share_expires_at
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
		&actDisputeAmount, &actShareExpiresAt,
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
	if actDisputeAmount.Valid {
		act.DisputeAmount = &actDisputeAmount.Float64
	}
	if actShareExpiresAt.Valid {
		act.ShareExpiresAt = &actShareExpiresAt.Time
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
			"draft":       {"sent", "confirmed", "discrepancy"},
			"sent":        {"confirmed", "disputed", "discrepancy", "draft"},
			"confirmed":   {"draft"},
			"disputed":    {"draft", "confirmed", "sent"},
			"discrepancy": {"draft", "confirmed"},
			"no_response": {"draft", "sent", "confirmed"},
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
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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

	htmlContent := h.renderReconciliationHTML(act, lines)

	format := c.DefaultQuery("format", "html")
	if format == "pdf" {
		pdfBytes, pdfErr := htmlToPDF(htmlContent)
		if pdfErr != nil {
			h.log.Error("Failed to generate PDF", "error", pdfErr)
			response.InternalError(c, "PDF yaratishda xatolik")
			return
		}
		filename := fmt.Sprintf("akt_sverka_%s_%s_%s.pdf", act.PartnerName, act.PeriodStart, act.PeriodEnd)
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		c.Data(200, "application/pdf", pdfBytes)
		return
	}

	c.Data(200, "text/html; charset=utf-8", []byte(htmlContent))
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
		Via     string `json:"via" binding:"required"` // "email", "whatsapp", or "link"
		Email   string `json:"email"`
		Phone   string `json:"phone"`
		Subject string `json:"subject"`
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
		_, err = h.db.Exec(`UPDATE reconciliation_acts SET share_token = $1, share_expires_at = NOW() + INTERVAL '24 hours' WHERE id = $2 AND tenant_id = $3`, shareToken, actID, tenantID)
		if err != nil {
			h.log.Error("Failed to set share token", "error", err)
			response.InternalError(c, "Failed to generate share link")
			return
		}
	} else {
		// Refresh expiry on re-send
		_, _ = h.db.Exec(`UPDATE reconciliation_acts SET share_expires_at = NOW() + INTERVAL '24 hours' WHERE id = $1 AND tenant_id = $2`, actID, tenantID)
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
		subject := input.Subject
		if subject == "" {
			subject = fmt.Sprintf("Akt sverka — %s (%s – %s)", act.PartnerName, act.PeriodStart, act.PeriodEnd)
		}

		emailBody := h.renderReconciliationEmailHTML(act, lines, closingBalance, shareURL)
		if input.Message != "" {
			emailBody = fmt.Sprintf(`<div style="margin-bottom:20px;padding:12px 16px;background:#f8fafc;border-left:3px solid #3b82f6;border-radius:4px;font-size:14px;color:#334155;">%s</div>`, strings.ReplaceAll(input.Message, "\n", "<br>")) + emailBody
		}

		// Generate PDF attachment
		var attachments []email.Attachment
		pdfHTML := h.renderReconciliationHTML(act, lines)
		if pdfBytes, pdfErr := htmlToPDF(pdfHTML); pdfErr == nil {
			pdfFilename := fmt.Sprintf("akt_sverka_%s_%s_%s.pdf", act.PartnerName, act.PeriodStart, act.PeriodEnd)
			attachments = append(attachments, email.Attachment{
				Filename:    pdfFilename,
				ContentType: "application/pdf",
				Data:        pdfBytes,
			})
		} else {
			h.log.Error("Failed to generate PDF for email attachment", "error", pdfErr)
		}

		sendErr := h.emailService.Send(&email.Email{
			To:          []string{input.Email},
			Subject:     subject,
			Body:        emailBody,
			IsHTML:      true,
			Attachments: attachments,
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

	case "link":
		// Just generate/refresh the share link, no sending
		response.Success(c, gin.H{
			"message":   "Havola tayyor",
			"share_url": shareURL,
		})

	default:
		response.BadRequest(c, "Invalid send method. Use 'email', 'whatsapp', or 'link'")
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
	var shareExpiresAt sql.NullTime
	var disputeAmount sql.NullFloat64

	err := h.db.QueryRow(`
		SELECT ra.id, ra.tenant_id, COALESCE(ct.name, ''), ra.period_start, ra.period_end,
			   ra.opening_balance, ra.our_debit_total, ra.our_credit_total, ra.status, ra.notes,
			   ra.response_status, ra.share_expires_at, ra.dispute_amount
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.share_token = $1 AND ra.deleted_at IS NULL
	`, token).Scan(&actID, &tenantID, &partnerName, &periodStart, &periodEnd,
		&openingBalance, &ourDebitTotal, &ourCreditTotal, &status, &notes, &responseStatus,
		&shareExpiresAt, &disputeAmount)
	if err != nil {
		response.NotFound(c, "Reconciliation act")
		return
	}

	// Check if share link has expired
	if shareExpiresAt.Valid && time.Now().After(shareExpiresAt.Time) {
		c.JSON(410, gin.H{"error": "Bu havola muddati tugagan. Iltimos, yangi havola so'rang."})
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
	if shareExpiresAt.Valid {
		result["share_expires_at"] = shareExpiresAt.Time
	}
	if disputeAmount.Valid {
		result["dispute_amount"] = disputeAmount.Float64
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
		Action string   `json:"action" binding:"required"` // "confirm" or "dispute"
		Name   string   `json:"name"`
		Note   string   `json:"note"`
		Amount *float64 `json:"amount"` // counterparty's stated balance (for disputes)
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
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
	var respondShareExpiresAt sql.NullTime
	err := h.db.QueryRow(`
		SELECT id, status, response_status, share_expires_at FROM reconciliation_acts
		WHERE share_token = $1 AND deleted_at IS NULL
	`, token).Scan(&actID, &currentStatus, &currentResponse, &respondShareExpiresAt)
	if err != nil {
		response.NotFound(c, "Reconciliation act")
		return
	}

	// Check if share link has expired
	if respondShareExpiresAt.Valid && time.Now().After(respondShareExpiresAt.Time) {
		c.JSON(410, gin.H{"error": "Bu havola muddati tugagan. Iltimos, yangi havola so'rang."})
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

	var disputeAmountVal interface{}
	if input.Amount != nil {
		disputeAmountVal = *input.Amount
	}

	_, err = h.db.Exec(`
		UPDATE reconciliation_acts
		SET response_status = $1, responded_at = NOW(), respondent_name = $2, dispute_note = $3,
			status = $4, dispute_amount = $5, response_notified = FALSE, updated_at = NOW()
		WHERE id = $6
	`, responseStatus, nullStr(input.Name), nullStr(input.Note), newActStatus, disputeAmountVal, actID)
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

