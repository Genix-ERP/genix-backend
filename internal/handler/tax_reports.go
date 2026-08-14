package handler

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TaxReportPeriod represents a tax reporting period
type TaxReportPeriod struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	Name             string     `json:"name"`
	PeriodType       string     `json:"period_type"`
	StartDate        string     `json:"start_date"`
	EndDate          string     `json:"end_date"`
	Deadline         *string    `json:"deadline,omitempty"`
	Status           string     `json:"status"`
	TotalSales       float64    `json:"total_sales"`
	TotalSalesTax    float64    `json:"total_sales_tax"`
	TotalPurchases   float64    `json:"total_purchases"`
	TotalPurchaseTax float64    `json:"total_purchase_tax"`
	NetTaxLiability  float64    `json:"net_tax_liability"`
	FilingReference  *string    `json:"filing_reference,omitempty"`
	FiledDate        *string    `json:"filed_date,omitempty"`
	FiledByID        *uuid.UUID `json:"filed_by,omitempty"`
	FiledByName      *string    `json:"filed_by_name,omitempty"`
	Notes            *string    `json:"notes,omitempty"`
	PaidAt           *time.Time `json:"paid_at,omitempty"`
	CreatedByID      *uuid.UUID `json:"created_by,omitempty"`
	CreatedByName    *string    `json:"created_by_name,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// TaxReportLine represents a line item in a tax report
type TaxReportLine struct {
	ID               uuid.UUID  `json:"id"`
	ReportID         uuid.UUID  `json:"report_id"`
	TaxRateID        *uuid.UUID `json:"tax_rate_id,omitempty"`
	TaxName          string     `json:"tax_name"`
	TaxRate          float64    `json:"tax_rate"`
	TransactionType  string     `json:"transaction_type"`
	TaxableAmount    float64    `json:"taxable_amount"`
	TaxAmount        float64    `json:"tax_amount"`
	TotalAmount      float64    `json:"total_amount"`
	TransactionCount int        `json:"transaction_count"`
}

// TaxTransaction represents an individual tax transaction
type TaxTransaction struct {
	ID                    uuid.UUID  `json:"id"`
	TenantID              uuid.UUID  `json:"tenant_id"`
	ReportID              *uuid.UUID `json:"report_id,omitempty"`
	TransactionType       string     `json:"transaction_type"`
	TransactionID         uuid.UUID  `json:"transaction_id"`
	TransactionNumber     string     `json:"transaction_number"`
	TransactionDate       string     `json:"transaction_date"`
	PartyType             *string    `json:"party_type,omitempty"`
	PartyID               *uuid.UUID `json:"party_id,omitempty"`
	PartyName             *string    `json:"party_name,omitempty"`
	PartyTaxID            *string    `json:"party_tax_id,omitempty"`
	TaxRateID             *uuid.UUID `json:"tax_rate_id,omitempty"`
	TaxName               string     `json:"tax_name"`
	TaxRate               float64    `json:"tax_rate"`
	TaxableAmount         float64    `json:"taxable_amount"`
	TaxAmount             float64    `json:"tax_amount"`
	TotalAmount           float64    `json:"total_amount"`
	IsCorrection          bool       `json:"is_correction"`
	OriginalTransactionID *uuid.UUID `json:"original_transaction_id,omitempty"`
	CorrectionReason      *string    `json:"correction_reason,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

// autoCreateTaxPeriods creates quarterly + yearly tax periods for the given year if they don't exist
func (h *Handler) autoCreateTaxPeriods(tenantID uuid.UUID, year int) {
	type periodDef struct {
		name      string
		pType     string
		startDate string
		endDate   string
		deadline  string
	}

	periods := []periodDef{
		{fmt.Sprintf("I Chorak %d", year), "quarterly", fmt.Sprintf("%d-01-01", year), fmt.Sprintf("%d-03-31", year), fmt.Sprintf("%d-04-25", year)},
		{fmt.Sprintf("II Chorak %d", year), "quarterly", fmt.Sprintf("%d-04-01", year), fmt.Sprintf("%d-06-30", year), fmt.Sprintf("%d-07-25", year)},
		{fmt.Sprintf("III Chorak %d", year), "quarterly", fmt.Sprintf("%d-07-01", year), fmt.Sprintf("%d-09-30", year), fmt.Sprintf("%d-10-25", year)},
		{fmt.Sprintf("IV Chorak %d", year), "quarterly", fmt.Sprintf("%d-10-01", year), fmt.Sprintf("%d-12-31", year), fmt.Sprintf("%d-01-25", year+1)},
		{fmt.Sprintf("Yillik %d", year), "yearly", fmt.Sprintf("%d-01-01", year), fmt.Sprintf("%d-12-31", year), fmt.Sprintf("%d-04-01", year+1)},
	}

	for _, p := range periods {
		// Only insert if not already exists (UNIQUE constraint on tenant_id, period_type, start_date)
		_, err := h.db.Exec(`
			INSERT INTO tax_report_periods (id, tenant_id, name, period_type, start_date, end_date, deadline, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'draft', NOW(), NOW())
			ON CONFLICT (tenant_id, period_type, start_date) DO NOTHING
		`, uuid.New(), tenantID, p.name, p.pType, p.startDate, p.endDate, p.deadline)
		if err != nil {
			h.log.Error("Failed to auto-create tax period", "error", err, "name", p.name)
		}
	}
}

// ListTaxReportPeriods returns all tax report periods for the tenant
func (h *Handler) ListTaxReportPeriods(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Auto-create quarterly + yearly periods for current year
	currentYear := time.Now().Year()
	h.autoCreateTaxPeriods(tenantID, currentYear)

	periodType := c.Query("period_type")
	status := c.Query("status")
	year := c.Query("year")

	query := `
		SELECT
			trp.id, trp.tenant_id, trp.name, trp.period_type, trp.start_date, trp.end_date,
			trp.deadline, trp.status, trp.total_sales, trp.total_sales_tax, trp.total_purchases, trp.total_purchase_tax,
			trp.net_tax_liability, trp.filing_reference, trp.filed_date, trp.filed_by,
			uf.first_name || ' ' || uf.last_name as filed_by_name,
			trp.notes, trp.paid_at, trp.created_by,
			uc.first_name || ' ' || uc.last_name as created_by_name,
			trp.created_at, trp.updated_at
		FROM tax_report_periods trp
		LEFT JOIN users uf ON uf.id = trp.filed_by
		LEFT JOIN users uc ON uc.id = trp.created_by
		WHERE trp.tenant_id = $1`

	args := []interface{}{tenantID}
	argCount := 1

	if periodType != "" {
		argCount++
		query += fmt.Sprintf(" AND trp.period_type = $%d", argCount)
		args = append(args, periodType)
	}

	if status != "" {
		argCount++
		query += fmt.Sprintf(" AND trp.status = $%d", argCount)
		args = append(args, status)
	}

	if year != "" {
		argCount++
		query += fmt.Sprintf(" AND EXTRACT(YEAR FROM trp.start_date) = $%d", argCount)
		args = append(args, year)
	}

	// Opt-in paging: this list grows with business activity, not with
	// configuration, so it has no natural ceiling. Callers that send no page
	// params still get the full list.
	countQuery := "SELECT COUNT(*)" + query[strings.Index(query, "FROM tax_report_periods"):]
	query += " ORDER BY trp.start_date DESC, trp.id ASC"

	paginate, page, pageSize, offset := optPagination(c)
	filterArgs := append([]interface{}{}, args...)
	if paginate {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list tax report periods", "error", err)
		response.InternalError(c, "Failed to list tax report periods")
		return
	}
	defer rows.Close()

	periods := make([]TaxReportPeriod, 0)
	for rows.Next() {
		var p TaxReportPeriod
		var startDate, endDate time.Time
		var deadline sql.NullTime
		var filingRef, notes sql.NullString
		var filedDate, paidAt sql.NullTime
		var filedByID, createdByID sql.NullString
		var filedByName, createdByName sql.NullString

		err := rows.Scan(
			&p.ID, &p.TenantID, &p.Name, &p.PeriodType, &startDate, &endDate,
			&deadline, &p.Status, &p.TotalSales, &p.TotalSalesTax, &p.TotalPurchases, &p.TotalPurchaseTax,
			&p.NetTaxLiability, &filingRef, &filedDate, &filedByID,
			&filedByName, &notes, &paidAt, &createdByID, &createdByName,
			&p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan tax report period", "error", err)
			continue
		}
		if paidAt.Valid {
			p.PaidAt = &paidAt.Time
		}

		p.StartDate = startDate.Format("2006-01-02")
		p.EndDate = endDate.Format("2006-01-02")
		if deadline.Valid {
			dl := deadline.Time.Format("2006-01-02")
			p.Deadline = &dl
		}

		if filingRef.Valid {
			p.FilingReference = &filingRef.String
		}
		if filedDate.Valid {
			fd := filedDate.Time.Format("2006-01-02")
			p.FiledDate = &fd
		}
		if filedByID.Valid {
			id, _ := uuid.Parse(filedByID.String)
			p.FiledByID = &id
		}
		if filedByName.Valid {
			p.FiledByName = &filedByName.String
		}
		if notes.Valid {
			p.Notes = &notes.String
		}
		if createdByID.Valid {
			id, _ := uuid.Parse(createdByID.String)
			p.CreatedByID = &id
		}
		if createdByName.Valid {
			p.CreatedByName = &createdByName.String
		}

		periods = append(periods, p)
	}

	if !paginate {
		response.Success(c, periods)
		return
	}

	total := 0
	if err := h.db.QueryRow(countQuery, filterArgs...).Scan(&total); err != nil {
		h.log.Error("Failed to count tax report periods", "error", err)
		total = len(periods)
	}
	response.Paginated(c, periods, page, pageSize, total)
}

// GetTaxReportPeriod returns a single tax report period with details
func (h *Handler) GetTaxReportPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid period ID")
		return
	}

	// Get period
	var p TaxReportPeriod
	var startDate, endDate time.Time
	var deadline sql.NullTime
	var filingRef, notes sql.NullString
	var filedDate, paidAt sql.NullTime
	var filedByID, createdByID sql.NullString
	var filedByName, createdByName sql.NullString

	err = h.db.QueryRow(`
		SELECT
			trp.id, trp.tenant_id, trp.name, trp.period_type, trp.start_date, trp.end_date,
			trp.deadline, trp.status, trp.total_sales, trp.total_sales_tax, trp.total_purchases, trp.total_purchase_tax,
			trp.net_tax_liability, trp.filing_reference, trp.filed_date, trp.filed_by,
			uf.first_name || ' ' || uf.last_name,
			trp.notes, trp.paid_at, trp.created_by,
			uc.first_name || ' ' || uc.last_name,
			trp.created_at, trp.updated_at
		FROM tax_report_periods trp
		LEFT JOIN users uf ON uf.id = trp.filed_by
		LEFT JOIN users uc ON uc.id = trp.created_by
		WHERE trp.id = $1 AND trp.tenant_id = $2
	`, periodID, tenantID).Scan(
		&p.ID, &p.TenantID, &p.Name, &p.PeriodType, &startDate, &endDate,
		&deadline, &p.Status, &p.TotalSales, &p.TotalSalesTax, &p.TotalPurchases, &p.TotalPurchaseTax,
		&p.NetTaxLiability, &filingRef, &filedDate, &filedByID,
		&filedByName, &notes, &paidAt, &createdByID, &createdByName,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tax report period not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get tax report period", "error", err)
		response.InternalError(c, "Failed to get tax report period")
		return
	}

	p.StartDate = startDate.Format("2006-01-02")
	p.EndDate = endDate.Format("2006-01-02")
	if deadline.Valid {
		dl := deadline.Time.Format("2006-01-02")
		p.Deadline = &dl
	}

	if filingRef.Valid {
		p.FilingReference = &filingRef.String
	}
	if filedDate.Valid {
		fd := filedDate.Time.Format("2006-01-02")
		p.FiledDate = &fd
	}
	if filedByID.Valid {
		id, _ := uuid.Parse(filedByID.String)
		p.FiledByID = &id
	}
	if filedByName.Valid {
		p.FiledByName = &filedByName.String
	}
	if notes.Valid {
		p.Notes = &notes.String
	}
	if paidAt.Valid {
		p.PaidAt = &paidAt.Time
	}
	if createdByID.Valid {
		id, _ := uuid.Parse(createdByID.String)
		p.CreatedByID = &id
	}
	if createdByName.Valid {
		p.CreatedByName = &createdByName.String
	}

	// Get report lines
	lineRows, err := h.db.Query(`
		SELECT id, report_id, tax_rate_id, tax_name, tax_rate, transaction_type,
			   taxable_amount, tax_amount, total_amount, transaction_count
		FROM tax_report_lines
		WHERE report_id = $1
		ORDER BY transaction_type, tax_name
	`, periodID)
	if err != nil {
		h.log.Error("Failed to get tax report lines", "error", err)
	}

	lines := make([]TaxReportLine, 0)
	if lineRows != nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var l TaxReportLine
			var taxRateID sql.NullString

			err := lineRows.Scan(
				&l.ID, &l.ReportID, &taxRateID, &l.TaxName, &l.TaxRate, &l.TransactionType,
				&l.TaxableAmount, &l.TaxAmount, &l.TotalAmount, &l.TransactionCount,
			)
			if err != nil {
				continue
			}
			if taxRateID.Valid {
				id, _ := uuid.Parse(taxRateID.String)
				l.TaxRateID = &id
			}
			lines = append(lines, l)
		}
	}

	response.Success(c, gin.H{
		"period": p,
		"lines":  lines,
	})
}

// CreateTaxReportPeriod creates a new tax report period
func (h *Handler) CreateTaxReportPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input struct {
		Name       string  `json:"name" binding:"required"`
		PeriodType string  `json:"period_type" binding:"required"`
		StartDate  string  `json:"start_date" binding:"required"`
		EndDate    string  `json:"end_date" binding:"required"`
		Notes      *string `json:"notes"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse dates
	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start date format")
		return
	}
	endDate, err := time.Parse("2006-01-02", input.EndDate)
	if err != nil {
		response.BadRequest(c, "Invalid end date format")
		return
	}

	if endDate.Before(startDate) {
		response.BadRequest(c, "End date must be after start date")
		return
	}

	periodID := uuid.New()
	now := time.Now()

	_, err = h.db.Exec(`
		INSERT INTO tax_report_periods (id, tenant_id, name, period_type, start_date, end_date, status, notes, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $9, $9)
	`, periodID, tenantID, input.Name, input.PeriodType, startDate, endDate, input.Notes, userID, now)

	if err != nil {
		h.log.Error("Failed to create tax report period", "error", err)
		response.InternalError(c, "Failed to create tax report period")
		return
	}

	response.Created(c, gin.H{"id": periodID, "message": "Tax report period created"})
}

// CalculateTaxReport calculates/recalculates a tax report
func (h *Handler) CalculateTaxReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid period ID")
		return
	}

	// Get period
	var startDate, endDate time.Time
	var status string
	err = h.db.QueryRow(`
		SELECT start_date, end_date, status FROM tax_report_periods
		WHERE id = $1 AND tenant_id = $2
	`, periodID, tenantID).Scan(&startDate, &endDate, &status)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Tax report period not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to get tax report period")
		return
	}

	if status == "filed" || status == "paid" {
		response.BadRequest(c, "Cannot recalculate a filed or paid report")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Clear existing lines and transactions
	_, _ = tx.Exec("DELETE FROM tax_report_lines WHERE report_id = $1", periodID)
	_, _ = tx.Exec("DELETE FROM tax_transactions WHERE report_id = $1", periodID)

	// Calculate sales tax from sales invoices.
	//
	// sales_invoices has NO header tax_rate_id column (only purchases got one,
	// migration 202) — the old query referenced the phantom si.tax_rate_id and
	// si.total, always errored, and the error was swallowed, so Calculate
	// 500'd on the period UPDATE forever and periods never left 'draft'
	// (soliq audit 2026-08-13). Sales taxes live on the LINES (tax_id), so
	// the representative rate is derived per invoice from its lines. Credit
	// notes REDUCE output tax — they are the same table with
	// invoice_type='credit_note' and positive amounts.
	var totalSales, totalSalesTax float64
	salesRows, err := tx.Query(`
		SELECT
			COALESCE(lt.tax_id::text, '00000000-0000-0000-0000-000000000000') as tax_rate_id,
			COALESCE(tr.name, 'No Tax') as tax_name,
			COALESCE(tr.rate, 0) as tax_rate,
			COUNT(*) as tx_count,
			SUM(si.subtotal * CASE WHEN COALESCE(si.invoice_type, 'invoice') = 'credit_note' THEN -1 ELSE 1 END) as taxable_amount,
			SUM(si.tax_amount * CASE WHEN COALESCE(si.invoice_type, 'invoice') = 'credit_note' THEN -1 ELSE 1 END) as tax_amount,
			SUM(si.total_amount * CASE WHEN COALESCE(si.invoice_type, 'invoice') = 'credit_note' THEN -1 ELSE 1 END) as total_amount
		FROM sales_invoices si
		LEFT JOIN LATERAL (
			SELECT MIN(l.tax_id::text)::uuid AS tax_id FROM sales_invoice_lines l
			WHERE l.sales_invoice_id = si.id AND l.tax_id IS NOT NULL
		) lt ON true
		LEFT JOIN tax_rates tr ON tr.id = lt.tax_id
		WHERE si.tenant_id = $1
			AND si.invoice_date >= $2
			AND si.invoice_date <= $3
			AND si.status NOT IN ('draft', 'cancelled')
			AND si.deleted_at IS NULL
		GROUP BY lt.tax_id, tr.name, tr.rate
	`, tenantID, startDate, endDate)

	if err != nil {
		h.log.Error("CalculateTaxReport: sales query failed", "error", err)
		response.InternalError(c, "Failed to calculate sales tax")
		return
	}
	// Drain the rows FIRST, insert after Close: issuing tx.Exec while rows
	// from the same tx connection are open is not supported by lib/pq — the
	// old in-loop INSERTs failed silently behind the discarded error.
	type taxBucket struct {
		taxRateID                    string
		taxName                      string
		taxRate                      float64
		txCount                      int
		taxableAmt, taxAmt, totalAmt float64
	}
	var salesBuckets []taxBucket
	for salesRows.Next() {
		var b taxBucket
		if err := salesRows.Scan(&b.taxRateID, &b.taxName, &b.taxRate, &b.txCount, &b.taxableAmt, &b.taxAmt, &b.totalAmt); err != nil {
			salesRows.Close()
			h.log.Error("CalculateTaxReport: sales scan failed", "error", err)
			response.InternalError(c, "Failed to calculate sales tax")
			return
		}
		salesBuckets = append(salesBuckets, b)
	}
	salesRows.Close()

	for _, b := range salesBuckets {
		totalSales += b.taxableAmt
		totalSalesTax += b.taxAmt

		var taxRateIDPtr *uuid.UUID
		if b.taxRateID != "00000000-0000-0000-0000-000000000000" {
			id, _ := uuid.Parse(b.taxRateID)
			taxRateIDPtr = &id
		}
		if _, err := tx.Exec(`
			INSERT INTO tax_report_lines (id, report_id, tax_rate_id, tax_name, tax_rate, transaction_type, taxable_amount, tax_amount, total_amount, transaction_count)
			VALUES ($1, $2, $3, $4, $5, 'sales', $6, $7, $8, $9)
		`, uuid.New(), periodID, taxRateIDPtr, b.taxName, b.taxRate, b.taxableAmt, b.taxAmt, b.totalAmt, b.txCount); err != nil {
			h.log.Error("CalculateTaxReport: sales line insert failed", "error", err)
			response.InternalError(c, "Failed to calculate sales tax")
			return
		}
	}

	// Calculate purchase tax from purchase invoices. Debit notes REDUCE the
	// input-tax offset (same table, invoice_type='debit_note', positive
	// amounts).
	var totalPurchases, totalPurchaseTax float64
	purchaseRows, err := tx.Query(`
		SELECT
			COALESCE(pi.tax_rate_id::text, '00000000-0000-0000-0000-000000000000') as tax_rate_id,
			COALESCE(tr.name, 'No Tax') as tax_name,
			COALESCE(tr.rate, 0) as tax_rate,
			COUNT(*) as tx_count,
			SUM(pi.subtotal * CASE WHEN COALESCE(pi.invoice_type, 'invoice') = 'debit_note' THEN -1 ELSE 1 END) as taxable_amount,
			SUM(pi.tax_amount * CASE WHEN COALESCE(pi.invoice_type, 'invoice') = 'debit_note' THEN -1 ELSE 1 END) as tax_amount,
			SUM(pi.total_amount * CASE WHEN COALESCE(pi.invoice_type, 'invoice') = 'debit_note' THEN -1 ELSE 1 END) as total_amount
		FROM purchase_invoices pi
		LEFT JOIN tax_rates tr ON tr.id = pi.tax_rate_id
		WHERE pi.tenant_id = $1
			AND pi.invoice_date >= $2
			AND pi.invoice_date <= $3
			AND pi.status NOT IN ('draft', 'cancelled')
			AND pi.deleted_at IS NULL
		GROUP BY pi.tax_rate_id, tr.name, tr.rate
	`, tenantID, startDate, endDate)

	if err != nil {
		h.log.Error("CalculateTaxReport: purchase query failed", "error", err)
		response.InternalError(c, "Failed to calculate purchase tax")
		return
	}
	var purchaseBuckets []taxBucket
	for purchaseRows.Next() {
		var b taxBucket
		if err := purchaseRows.Scan(&b.taxRateID, &b.taxName, &b.taxRate, &b.txCount, &b.taxableAmt, &b.taxAmt, &b.totalAmt); err != nil {
			purchaseRows.Close()
			h.log.Error("CalculateTaxReport: purchase scan failed", "error", err)
			response.InternalError(c, "Failed to calculate purchase tax")
			return
		}
		purchaseBuckets = append(purchaseBuckets, b)
	}
	purchaseRows.Close()

	for _, b := range purchaseBuckets {
		totalPurchases += b.taxableAmt
		totalPurchaseTax += b.taxAmt

		var taxRateIDPtr *uuid.UUID
		if b.taxRateID != "00000000-0000-0000-0000-000000000000" {
			id, _ := uuid.Parse(b.taxRateID)
			taxRateIDPtr = &id
		}
		if _, err := tx.Exec(`
			INSERT INTO tax_report_lines (id, report_id, tax_rate_id, tax_name, tax_rate, transaction_type, taxable_amount, tax_amount, total_amount, transaction_count)
			VALUES ($1, $2, $3, $4, $5, 'purchases', $6, $7, $8, $9)
		`, uuid.New(), periodID, taxRateIDPtr, b.taxName, b.taxRate, b.taxableAmt, b.taxAmt, b.totalAmt, b.txCount); err != nil {
			h.log.Error("CalculateTaxReport: purchase line insert failed", "error", err)
			response.InternalError(c, "Failed to calculate purchase tax")
			return
		}
	}

	// Calculate net tax liability
	netTaxLiability := totalSalesTax - totalPurchaseTax

	// Update period totals
	_, err = tx.Exec(`
		UPDATE tax_report_periods SET
			total_sales = $1,
			total_sales_tax = $2,
			total_purchases = $3,
			total_purchase_tax = $4,
			net_tax_liability = $5,
			status = 'calculated',
			updated_at = $6
		WHERE id = $7
	`, totalSales, totalSalesTax, totalPurchases, totalPurchaseTax, netTaxLiability, time.Now(), periodID)

	if err != nil {
		h.log.Error("Failed to update tax report period", "error", err)
		response.InternalError(c, "Failed to update tax report period")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	response.Success(c, gin.H{
		"message":            "Tax report calculated",
		"total_sales":        totalSales,
		"total_sales_tax":    totalSalesTax,
		"total_purchases":    totalPurchases,
		"total_purchase_tax": totalPurchaseTax,
		"net_tax_liability":  netTaxLiability,
	})
}

// FileTaxReport marks a tax report as filed
func (h *Handler) FileTaxReport(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid period ID")
		return
	}

	var input struct {
		FilingReference string `json:"filing_reference"`
		Notes           string `json:"notes"`
	}
	c.ShouldBindJSON(&input)

	// Check status
	var status string
	err = h.db.QueryRow(`
		SELECT status FROM tax_report_periods WHERE id = $1 AND tenant_id = $2
	`, periodID, tenantID).Scan(&status)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Tax report period not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to load tax report period status", "error", err)
		response.InternalError(c, "Failed to file tax report")
		return
	}
	if status == "draft" {
		response.BadRequest(c, "Report must be calculated before filing")
		return
	}
	if status == "filed" || status == "paid" {
		response.BadRequest(c, "Report is already filed")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE tax_report_periods SET
			status = 'filed',
			filing_reference = $1,
			filed_date = $2,
			filed_by = $3,
			notes = COALESCE(NULLIF($4, ''), notes),
			updated_at = $2
		WHERE id = $5 AND tenant_id = $6
	`, input.FilingReference, now, userID, input.Notes, periodID, tenantID)

	if err != nil {
		h.log.Error("Failed to file tax report", "error", err)
		response.InternalError(c, "Failed to file tax report")
		return
	}

	response.Success(c, gin.H{"message": "Tax report filed"})
}

// DeleteTaxReportPeriod deletes a tax report period
func (h *Handler) DeleteTaxReportPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid period ID")
		return
	}

	// Check if filed
	var status string
	err = h.db.QueryRow(`
		SELECT status FROM tax_report_periods WHERE id = $1 AND tenant_id = $2
	`, periodID, tenantID).Scan(&status)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Tax report period not found")
		return
	}
	if status == "filed" || status == "paid" {
		// A paid period has a posted tax-payment JE hanging off it — deleting
		// the period would orphan that entry (soliq audit 2026-08-13).
		response.BadRequest(c, "Cannot delete a filed or paid report")
		return
	}

	_, err = h.db.Exec(`DELETE FROM tax_report_periods WHERE id = $1 AND tenant_id = $2`, periodID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete tax report period", "error", err)
		response.InternalError(c, "Failed to delete tax report period")
		return
	}

	response.Success(c, gin.H{"message": "Tax report period deleted"})
}

// GetTaxReportSummary returns a summary of tax for a date range
func (h *Handler) GetTaxReportSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, _ := middleware.GetOrganizationID(c)

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	hasDates := startDate != "" && endDate != ""

	// Build org filter
	orgFilter := ""
	args := []interface{}{tenantID}
	argIdx := 2
	if organizationID != uuid.Nil {
		orgFilter = fmt.Sprintf(" AND organization_id = $%d", argIdx)
		args = append(args, organizationID)
		argIdx++
	}

	// Get sales tax summary
	var totalSales, totalSalesTax float64
	var salesCount int
	if hasDates {
		dateArgs := append(args, startDate, endDate)
		h.db.QueryRow(fmt.Sprintf(`
			SELECT COALESCE(SUM(subtotal * CASE WHEN COALESCE(invoice_type, 'invoice') IN ('credit_note', 'debit_note') THEN -1 ELSE 1 END), 0),
			       COALESCE(SUM(tax_amount * CASE WHEN COALESCE(invoice_type, 'invoice') IN ('credit_note', 'debit_note') THEN -1 ELSE 1 END), 0),
			       COUNT(*)
			FROM sales_invoices
			WHERE tenant_id = $1%s AND invoice_date >= $%d AND invoice_date <= $%d
				AND status NOT IN ('draft', 'cancelled') AND deleted_at IS NULL
		`, orgFilter, argIdx, argIdx+1), dateArgs...).Scan(&totalSales, &totalSalesTax, &salesCount)
	} else {
		h.db.QueryRow(fmt.Sprintf(`
			SELECT COALESCE(SUM(subtotal * CASE WHEN COALESCE(invoice_type, 'invoice') IN ('credit_note', 'debit_note') THEN -1 ELSE 1 END), 0),
			       COALESCE(SUM(tax_amount * CASE WHEN COALESCE(invoice_type, 'invoice') IN ('credit_note', 'debit_note') THEN -1 ELSE 1 END), 0),
			       COUNT(*)
			FROM sales_invoices
			WHERE tenant_id = $1%s
				AND status NOT IN ('draft', 'cancelled') AND deleted_at IS NULL
		`, orgFilter), args...).Scan(&totalSales, &totalSalesTax, &salesCount)
	}

	// Get purchase tax summary
	var totalPurchases, totalPurchaseTax float64
	var purchaseCount int
	if hasDates {
		dateArgs := append(args, startDate, endDate)
		h.db.QueryRow(fmt.Sprintf(`
			SELECT COALESCE(SUM(subtotal * CASE WHEN COALESCE(invoice_type, 'invoice') IN ('credit_note', 'debit_note') THEN -1 ELSE 1 END), 0),
			       COALESCE(SUM(tax_amount * CASE WHEN COALESCE(invoice_type, 'invoice') IN ('credit_note', 'debit_note') THEN -1 ELSE 1 END), 0),
			       COUNT(*)
			FROM purchase_invoices
			WHERE tenant_id = $1%s AND invoice_date >= $%d AND invoice_date <= $%d
				AND status NOT IN ('draft', 'cancelled') AND deleted_at IS NULL
		`, orgFilter, argIdx, argIdx+1), dateArgs...).Scan(&totalPurchases, &totalPurchaseTax, &purchaseCount)
	} else {
		h.db.QueryRow(fmt.Sprintf(`
			SELECT COALESCE(SUM(subtotal * CASE WHEN COALESCE(invoice_type, 'invoice') IN ('credit_note', 'debit_note') THEN -1 ELSE 1 END), 0),
			       COALESCE(SUM(tax_amount * CASE WHEN COALESCE(invoice_type, 'invoice') IN ('credit_note', 'debit_note') THEN -1 ELSE 1 END), 0),
			       COUNT(*)
			FROM purchase_invoices
			WHERE tenant_id = $1%s
				AND status NOT IN ('draft', 'cancelled') AND deleted_at IS NULL
		`, orgFilter), args...).Scan(&totalPurchases, &totalPurchaseTax, &purchaseCount)
	}

	// Get filed periods count
	var filedCount int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM tax_report_periods
		WHERE tenant_id = $1 AND status = 'filed'
	`, tenantID).Scan(&filedCount)

	// Get pending periods count
	var pendingCount int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM tax_report_periods
		WHERE tenant_id = $1 AND status IN ('draft', 'calculated')
	`, tenantID).Scan(&pendingCount)

	response.Success(c, gin.H{
		"period": gin.H{
			"start_date": startDate,
			"end_date":   endDate,
		},
		"sales": gin.H{
			"total_amount":      totalSales,
			"total_tax":         totalSalesTax,
			"transaction_count": salesCount,
		},
		"purchases": gin.H{
			"total_amount":      totalPurchases,
			"total_tax":         totalPurchaseTax,
			"transaction_count": purchaseCount,
		},
		"net_tax_liability": totalSalesTax - totalPurchaseTax,
		"reports": gin.H{
			"filed_count":   filedCount,
			"pending_count": pendingCount,
		},
	})
}

// GetTaxTransactions returns tax transactions for a period
func (h *Handler) GetTaxTransactions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	organizationID, _ := middleware.GetOrganizationID(c)

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	// Accept both vocabularies: the client's chips send 'sale'/'purchase',
	// this handler historically wanted 'sales'/'purchases'. Mismatched values
	// used to silently select BOTH arms, so the chips looked broken.
	txType := strings.ToLower(strings.TrimSpace(c.Query("type")))
	switch txType {
	case "sale":
		txType = "sales"
	case "purchase":
		txType = "purchases"
	}

	// Build date filter clause and args dynamically
	hasDates := startDate != "" && endDate != ""

	// Build org filter
	txOrgFilter := ""
	baseArgs := []interface{}{tenantID}
	nextArg := 2
	if organizationID != uuid.Nil {
		txOrgFilter = fmt.Sprintf(" AND si.organization_id = $%d", nextArg)
		baseArgs = append(baseArgs, organizationID)
		nextArg++
	}

	transactions := make([]map[string]interface{}, 0)

	if txType == "" || txType == "sales" {
		// Get sales invoices. The header has NO tax_rate_id column (that's a
		// purchases-only column, migration 202) — the old join on the phantom
		// si.tax_rate_id (and phantom si.total) made this whole arm error out,
		// and the swallowed error meant the tab silently showed purchases
		// only (soliq audit 2026-08-13). Sales taxes live on the lines, so
		// the representative rate is derived per invoice from them.
		salesQuery := fmt.Sprintf(`
			SELECT
				si.id, 'sales_invoice' as type, si.invoice_number, si.invoice_date,
				'customer' as party_type, si.customer_id, si.customer_name,
				COALESCE(c.tax_id, '') as party_tax_id,
				lt.tax_id as tax_rate_id, COALESCE(tr.name, 'No Tax') as tax_name, COALESCE(tr.rate, 0) as tax_rate,
				si.subtotal, si.tax_amount, si.total_amount
			FROM sales_invoices si
			LEFT JOIN contacts c ON c.id = si.customer_id
			LEFT JOIN LATERAL (
				SELECT MIN(l.tax_id::text)::uuid AS tax_id FROM sales_invoice_lines l
				WHERE l.sales_invoice_id = si.id AND l.tax_id IS NOT NULL
			) lt ON true
			LEFT JOIN tax_rates tr ON tr.id = lt.tax_id
			WHERE si.tenant_id = $1%s
				AND si.status NOT IN ('draft', 'cancelled') AND si.deleted_at IS NULL`, txOrgFilter)
		salesArgs := make([]interface{}, len(baseArgs))
		copy(salesArgs, baseArgs)
		if hasDates {
			salesQuery += fmt.Sprintf(` AND si.invoice_date >= $%d AND si.invoice_date <= $%d`, nextArg, nextArg+1)
			salesArgs = append(salesArgs, startDate, endDate)
		}
		salesQuery += ` ORDER BY si.invoice_date DESC`
		salesRows, err := h.db.Query(salesQuery, salesArgs...)

		if err != nil {
			h.log.Error("GetTaxTransactions: sales query failed", "error", err)
		}
		if err == nil {
			defer salesRows.Close()
			for salesRows.Next() {
				var id uuid.UUID
				var txTypeStr, txNumber, partyType, partyName, partyTaxID, taxName string
				var txDate time.Time
				var partyID, taxRateID sql.NullString
				var taxRate, taxableAmt, taxAmt, totalAmt float64

				if err := salesRows.Scan(
					&id, &txTypeStr, &txNumber, &txDate,
					&partyType, &partyID, &partyName, &partyTaxID,
					&taxRateID, &taxName, &taxRate,
					&taxableAmt, &taxAmt, &totalAmt,
				); err != nil {
					continue
				}

				tx := map[string]interface{}{
					"id":                 id,
					"transaction_type":   taxTxKind(txTypeStr),
					"document_type":      txTypeStr,
					"transaction_number": txNumber,
					"transaction_date":   txDate.Format("2006-01-02"),
					"party_type":         partyType,
					"party_name":         partyName,
					"party_tax_id":       partyTaxID,
					"tax_name":           taxName,
					"tax_rate":           taxRate,
					"taxable_amount":     taxableAmt,
					"tax_amount":         taxAmt,
					"total_amount":       totalAmt,
				}
				if partyID.Valid {
					tx["party_id"] = partyID.String
				}
				if taxRateID.Valid {
					tx["tax_rate_id"] = taxRateID.String
				}
				transactions = append(transactions, tx)
			}
		}
	}

	if txType == "" || txType == "purchases" {
		// Get purchase invoices - build org filter for pi alias
		piOrgFilter := ""
		if organizationID != uuid.Nil {
			piOrgFilter = fmt.Sprintf(" AND pi.organization_id = $%d", 2)
		}
		purchaseQuery := fmt.Sprintf(`
			SELECT
				pi.id, 'purchase_invoice' as type, pi.invoice_number, pi.invoice_date,
				'vendor' as party_type, pi.vendor_id, COALESCE(pi.supplier_name, c.name, '') as vendor_name,
				COALESCE(c.tax_id, '') as party_tax_id,
				tr.id as tax_rate_id, COALESCE(tr.name, 'No Tax') as tax_name, COALESCE(tr.rate, 0) as tax_rate,
				pi.subtotal, pi.tax_amount, pi.total_amount
			FROM purchase_invoices pi
			LEFT JOIN contacts c ON c.id = pi.vendor_id
			LEFT JOIN tax_rates tr ON tr.id = pi.tax_rate_id
			WHERE pi.tenant_id = $1%s
				AND pi.status NOT IN ('draft', 'cancelled') AND pi.deleted_at IS NULL`, piOrgFilter)
		purchaseArgs := make([]interface{}, len(baseArgs))
		copy(purchaseArgs, baseArgs)
		if hasDates {
			purchaseQuery += fmt.Sprintf(` AND pi.invoice_date >= $%d AND pi.invoice_date <= $%d`, nextArg, nextArg+1)
			purchaseArgs = append(purchaseArgs, startDate, endDate)
		}
		purchaseQuery += ` ORDER BY pi.invoice_date DESC`
		purchaseRows, err := h.db.Query(purchaseQuery, purchaseArgs...)

		if err != nil {
			h.log.Error("GetTaxTransactions: purchase query failed", "error", err)
		}
		if err == nil {
			defer purchaseRows.Close()
			for purchaseRows.Next() {
				var id uuid.UUID
				var txTypeStr, txNumber, partyType, partyName, partyTaxID, taxName string
				var txDate time.Time
				var partyID, taxRateID sql.NullString
				var taxRate, taxableAmt, taxAmt, totalAmt float64

				if err := purchaseRows.Scan(
					&id, &txTypeStr, &txNumber, &txDate,
					&partyType, &partyID, &partyName, &partyTaxID,
					&taxRateID, &taxName, &taxRate,
					&taxableAmt, &taxAmt, &totalAmt,
				); err != nil {
					continue
				}

				tx := map[string]interface{}{
					"id":                 id,
					"transaction_type":   taxTxKind(txTypeStr),
					"document_type":      txTypeStr,
					"transaction_number": txNumber,
					"transaction_date":   txDate.Format("2006-01-02"),
					"party_type":         partyType,
					"party_name":         partyName,
					"party_tax_id":       partyTaxID,
					"tax_name":           taxName,
					"tax_rate":           taxRate,
					"taxable_amount":     taxableAmt,
					"tax_amount":         taxAmt,
					"total_amount":       totalAmt,
				}
				if partyID.Valid {
					tx["party_id"] = partyID.String
				}
				if taxRateID.Valid {
					tx["tax_rate_id"] = taxRateID.String
				}
				transactions = append(transactions, tx)
			}
		}
	}

	// Both arms are ordered by date individually; sort the union so paging is
	// meaningful, then slice. (Row counts here are bounded by the period filter.)
	sort.SliceStable(transactions, func(i, j int) bool {
		return fmt.Sprint(transactions[i]["transaction_date"]) > fmt.Sprint(transactions[j]["transaction_date"])
	})

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	total := len(transactions)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	response.Paginated(c, transactions[start:end], page, pageSize, total)
}

// PayTaxPeriod records a tax payment for a period, creates journal entry, and marks period as paid
func (h *Handler) PayTaxPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	organizationID, _ := middleware.GetOrganizationID(c)

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid period ID")
		return
	}

	var input struct {
		PaymentMethod string `json:"payment_method"` // "bank" or "cash"
	}
	c.ShouldBindJSON(&input)
	if input.PaymentMethod == "" {
		input.PaymentMethod = "bank"
	}

	// Load period
	var period TaxReportPeriod
	var paidAt sql.NullTime
	err = h.db.QueryRow(`
		SELECT id, tenant_id, name, status, total_sales_tax, total_purchase_tax, net_tax_liability, paid_at
		FROM tax_report_periods WHERE id = $1 AND tenant_id = $2
	`, periodID, tenantID).Scan(
		&period.ID, &period.TenantID, &period.Name, &period.Status,
		&period.TotalSalesTax, &period.TotalPurchaseTax, &period.NetTaxLiability,
		&paidAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Tax report period not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to load tax period", "error", err)
		response.InternalError(c, "Failed to load tax period")
		return
	}

	// Validate
	if period.Status == "draft" {
		response.BadRequest(c, "Avval hisobotni hisoblang (Calculate)")
		return
	}
	if paidAt.Valid {
		response.BadRequest(c, "Bu davr allaqachon to'langan")
		return
	}
	if period.NetTaxLiability <= 0 {
		response.BadRequest(c, "To'lanadigan soliq majburiyati yo'q")
		return
	}

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Transaction failed")
		return
	}
	defer tx.Rollback()

	now := time.Now()

	// Find accounts
	var orgIDPtr *uuid.UUID
	if organizationID != uuid.Nil {
		orgIDPtr = &organizationID
	}

	outputVATAccountID := findAccount(tx, tenantID, orgIDPtr, "vat payable", "6420")
	if outputVATAccountID == uuid.Nil {
		outputVATAccountID = findAccount(tx, tenantID, orgIDPtr, "tax payable", "6410")
	}
	inputVATAccountID := findAccount(tx, tenantID, orgIDPtr, "input vat", "4410")
	if inputVATAccountID == uuid.Nil {
		inputVATAccountID = findAccount(tx, tenantID, orgIDPtr, "vat", "4410")
	}

	var bankAccountID uuid.UUID
	if input.PaymentMethod == "cash" {
		bankAccountID = findAccount(tx, tenantID, orgIDPtr, "cash", "5010")
	} else {
		bankAccountID = findAccount(tx, tenantID, orgIDPtr, "bank account", "5110")
	}

	if outputVATAccountID == uuid.Nil || bankAccountID == uuid.Nil {
		response.BadRequest(c, "Soliq yoki bank hisobi topilmadi")
		return
	}

	// Find MISC journal
	var journalID uuid.UUID
	var nextNumber int
	tx.QueryRow(`
		SELECT id, next_number FROM journals WHERE tenant_id = $1 AND code IN ('MISC','GENERAL') AND deleted_at IS NULL
		ORDER BY CASE WHEN code='MISC' THEN 0 ELSE 1 END LIMIT 1`,
		tenantID).Scan(&journalID, &nextNumber)

	if journalID == uuid.Nil {
		response.BadRequest(c, "MISC jurnali topilmadi")
		return
	}

	entryNumber := fmt.Sprintf("MISC-%d-%04d", now.Year(), nextNumber)

	// Calculate total debit/credit for the journal entry
	// Dt: OutputVAT, Kt: InputVAT (agar 4410 topilsa) + Bank (NetTaxLiability)
	// Debet summasi amalda yoziladigan kredit qatorlari yig'indisidan olinadi,
	// shunda Dt = Kt har doim ta'minlanadi (4410 topilmasa faqat NetTaxLiability)
	writeInputVAT := period.TotalPurchaseTax > 0 && inputVATAccountID != uuid.Nil
	entryTotal := period.NetTaxLiability
	if writeInputVAT {
		entryTotal += period.TotalPurchaseTax
	}

	// Create journal entry
	journalEntryID := uuid.New()
	description := fmt.Sprintf("QQS to'lovi: %s", period.Name)

	_, err = tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
			source_type, source_id, total_debit, total_credit, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'posted', $13, $14, $15)`,
		journalEntryID, tenantID, organizationID, journalID, entryNumber, now, period.Name, description,
		"tax_payment", periodID, entryTotal, entryTotal, userID, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create tax payment journal entry", "error", err)
		response.InternalError(c, "Jurnal yozuvi yaratishda xato")
		return
	}

	lineNumber := 1

	// Dt Output VAT (clear the liability) — summa amaldagi kredit qatorlari yig'indisiga teng
	_, err = tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		uuid.New(), journalEntryID, lineNumber, outputVATAccountID, "Chiquvchi QQS yopish",
		entryTotal, 0.0, now,
	)
	if err != nil {
		h.log.Error("Failed to create tax payment journal entry line", "error", err)
		response.InternalError(c, "Jurnal yozuvi qatorini yaratishda xato")
		return
	}
	if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3",
		entryTotal, now, outputVATAccountID); err != nil {
		h.log.Error("Failed to update output VAT account balance", "error", err)
		response.InternalError(c, "Hisob balansini yangilashda xato")
		return
	}
	lineNumber++

	// Kt Input VAT (clear the receivable)
	if writeInputVAT {
		_, err = tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			uuid.New(), journalEntryID, lineNumber, inputVATAccountID, "Kiruvchi QQS yopish",
			0.0, period.TotalPurchaseTax, now,
		)
		if err != nil {
			h.log.Error("Failed to create tax payment journal entry line", "error", err)
			response.InternalError(c, "Jurnal yozuvi qatorini yaratishda xato")
			return
		}
		if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3",
			period.TotalPurchaseTax, now, inputVATAccountID); err != nil {
			h.log.Error("Failed to update input VAT account balance", "error", err)
			response.InternalError(c, "Hisob balansini yangilashda xato")
			return
		}
		lineNumber++
	}

	// Kt Bank/Cash (net payment)
	_, err = tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		uuid.New(), journalEntryID, lineNumber, bankAccountID, "Soliq to'lovi",
		0.0, period.NetTaxLiability, now,
	)
	if err != nil {
		h.log.Error("Failed to create tax payment journal entry line", "error", err)
		response.InternalError(c, "Jurnal yozuvi qatorini yaratishda xato")
		return
	}
	if _, err = tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3",
		period.NetTaxLiability, now, bankAccountID); err != nil {
		h.log.Error("Failed to update bank account balance", "error", err)
		response.InternalError(c, "Hisob balansini yangilashda xato")
		return
	}

	// Update journal next_number
	if _, err = tx.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID); err != nil {
		h.log.Error("Failed to update journal next_number", "error", err)
		response.InternalError(c, "Jurnal raqamini yangilashda xato")
		return
	}

	// Mark period as paid
	_, err = tx.Exec(`
		UPDATE tax_report_periods SET
			status = 'paid',
			paid_at = $1,
			paid_by = $2,
			payment_journal_entry_id = $3,
			updated_at = $1
		WHERE id = $4 AND tenant_id = $5
	`, now, userID, journalEntryID, periodID, tenantID)
	if err != nil {
		h.log.Error("Failed to update tax period status", "error", err)
		response.InternalError(c, "Davr holatini yangilashda xato")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit tax payment", "error", err)
		response.InternalError(c, "Tranzaksiya yakunlanmadi")
		return
	}

	response.Success(c, gin.H{
		"message":          "Soliq to'lovi muvaffaqiyatli qayd etildi",
		"journal_entry_id": journalEntryID,
		"entry_number":     entryNumber,
		"amount":           period.NetTaxLiability,
		"paid_at":          now,
	})
}

// taxTxKind maps the internal document type to the transaction_type vocabulary
// the clients filter on ('sale' / 'purchase'). The raw value stays available as
// document_type so existing consumers of 'sales_invoice'/'purchase_invoice'
// keep working.
func taxTxKind(documentType string) string {
	switch documentType {
	case "sales_invoice":
		return "sale"
	case "purchase_invoice":
		return "purchase"
	}
	return documentType
}
