package handler

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// dividend_tax.go — withholding helper for Dividend solig'i per TZ §5.3 of
// ТЗ_Ish_Haqi_Soliq_Tolik.docx.
//
// Formula (§5.3):
//     Dividend solig'i = Taqsimlangan foyda × rate / 100
//     Net to shareholder = Distribution − Dividend solig'i
//
// Base: "Ta'sischilarga foyda tarqatilganda" — called whenever an admin is
// about to distribute profit to shareholders. Today this is exposed as a
// pure compute helper (no dividend-distribution posting flow exists yet).
// When the dividend-payout UI/flow is built later, it can call this
// endpoint to get the authoritative numbers instead of hardcoding 5%.
//
// Rate: read from company_tax_rates applies_to='dividend'; default 5%.
//
// Routes:
//   GET /dividend-tax?amount=12345                — preview (no side effects)
//   POST /dividend-tax/compute  { amount }        — same, body-based
//
// The response deliberately includes both the gross distribution and the
// shareholder net so the UI can show the full picture without arithmetic.

const defaultDividendRatePct = 5.0 // §5.3 of the TZ

type dividendComputeInput struct {
	// Amount gross-of-tax that the company intends to distribute.
	Amount float64 `json:"amount" form:"amount"`
	// Optional explicit rate override (admin preview of "what if" scenarios).
	Rate *float64 `json:"rate,omitempty" form:"rate,omitempty"`
}

// computeDividendBreakdown is the pure-function core; trivially unit-testable.
// Amounts are rounded to the nearest so'm per TT §4 (same convention as
// payroll and profit tax).
func computeDividendBreakdown(amount, rate float64) (taxAmount, netToShareholder float64) {
	if amount <= 0 || rate < 0 {
		return 0, 0
	}
	taxAmount = math.Round(amount * rate / 100)
	netToShareholder = amount - taxAmount
	return
}

// ComputeDividendTax preview endpoint. Works for both GET (query string)
// and POST (JSON body).
func (h *Handler) ComputeDividendTax(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var in dividendComputeInput
	// Accept either a JSON body (POST) or query params (GET). Gin's
	// ShouldBind will pick the right binding based on Content-Type; for
	// GET requests we fall through to query parsing.
	if c.Request.Method == "POST" {
		if err := c.ShouldBindJSON(&in); err != nil {
			response.BadRequest(c, "Invalid input")
			return
		}
	} else {
		if err := c.ShouldBindQuery(&in); err != nil {
			response.BadRequest(c, "Invalid input")
			return
		}
	}

	if in.Amount <= 0 {
		response.BadRequest(c, "amount must be greater than 0")
		return
	}

	// Rate resolution: explicit override → company_tax_rates(dividend) → 5%.
	rate := h.getCompanyTaxRatePct(tenantID, "dividend", defaultDividendRatePct)
	if in.Rate != nil && *in.Rate > 0 {
		rate = *in.Rate
	}

	taxAmount, net := computeDividendBreakdown(in.Amount, rate)

	response.Success(c, gin.H{
		"gross_distribution": in.Amount,
		"rate":               rate,
		"tax_amount":         taxAmount,
		"net_to_shareholder": net,
	})
}

// ─── Dividend distribution posting (TZ §5.3 consumer flow) ────────────────

// DividendDistributionInput — payload for POST /dividend-distributions.
// Caller provides the three GL accounts involved in the posting. This
// keeps the handler simple and explicit: no magic chart-of-accounts
// lookups, no silent "use whatever's configured" fallbacks that could
// post to the wrong account without anyone noticing.
type DividendDistributionInput struct {
	Amount                    float64 `json:"amount" binding:"required"`
	ShareholderName           string  `json:"shareholder_name" binding:"required"`
	DistributionDate          string  `json:"distribution_date"` // YYYY-MM-DD, defaults to today
	RetainedEarningsAccountID string  `json:"retained_earnings_account_id" binding:"required"`
	CashAccountID             string  `json:"cash_account_id" binding:"required"`
	TaxLiabilityAccountID     string  `json:"tax_liability_account_id"` // optional: defaults to DIVIDEND rate's account_id
	Rate                      *float64 `json:"rate,omitempty"`           // optional override
	Notes                     string  `json:"notes,omitempty"`
}

// CreateDividendDistribution posts the dividend journal entry per TZ §5.3.
//
// Journal entry shape:
//   Dr Retained Earnings        = gross_amount
//   Cr Cash / Bank              = net_to_shareholder
//   Cr Tax Liability            = tax_amount           (5% withheld)
//
// Source fields on the journal:
//   source_type = 'dividend_distribution'
//   reference   = shareholder name (searchable in the GL)
//
// Rate resolution: explicit override → company_tax_rates(dividend) → 5%.
// Tax liability account: explicit override → company_tax_rates(dividend).account_id.
// If neither is provided we reject with a structured error the UI can
// surface ("configure the Dividend row's Kreditorlik hisobi first").
//
// Route: POST /dividend-distributions
func (h *Handler) CreateDividendDistribution(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	orgID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	var in DividendDistributionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if in.Amount <= 0 {
		response.BadRequest(c, "amount must be greater than 0")
		return
	}

	retainedID, err := uuid.Parse(in.RetainedEarningsAccountID)
	if err != nil {
		response.BadRequest(c, "retained_earnings_account_id must be a UUID")
		return
	}
	cashID, err := uuid.Parse(in.CashAccountID)
	if err != nil {
		response.BadRequest(c, "cash_account_id must be a UUID")
		return
	}

	// Tax liability account: prefer explicit param, else look up the
	// company_tax_rates DIVIDEND row's configured account_id.
	var taxLiabID uuid.UUID
	if in.TaxLiabilityAccountID != "" {
		if id, err := uuid.Parse(in.TaxLiabilityAccountID); err == nil {
			taxLiabID = id
		}
	}
	if taxLiabID == uuid.Nil {
		var accountIDStr sql.NullString
		_ = h.db.QueryRow(`
			SELECT account_id::text FROM company_tax_rates
			WHERE tenant_id = $1 AND applies_to = 'dividend'
			  AND is_active = TRUE AND deleted_at IS NULL
			ORDER BY sort_order ASC, created_at ASC
			LIMIT 1
		`, tenantID).Scan(&accountIDStr)
		if accountIDStr.Valid && accountIDStr.String != "" {
			if id, err := uuid.Parse(accountIDStr.String); err == nil {
				taxLiabID = id
			}
		}
	}
	if taxLiabID == uuid.Nil {
		response.BadRequest(c, "tax_liability_account_id is required (or configure the Dividend row's account_id in company_tax_rates)")
		return
	}

	// Rate resolution.
	rate := h.getCompanyTaxRatePct(tenantID, "dividend", defaultDividendRatePct)
	if in.Rate != nil && *in.Rate > 0 {
		rate = *in.Rate
	}

	taxAmount, net := computeDividendBreakdown(in.Amount, rate)

	// Distribution date.
	distDate := time.Now().UTC()
	if in.DistributionDate != "" {
		if t, err := time.Parse("2006-01-02", in.DistributionDate); err == nil {
			distDate = t
		}
	}

	// Find / ensure the construction journal — dividends aren't construction
	// but the generic "main" journal helper in this codebase is the same one
	// payroll + material-reservation use; we reuse it so every tenant ends
	// up with one canonical posting journal rather than a spray.
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}
	journalID := h.ensureConstructionJournal(tenantID, orgIDPtr)
	if journalID == uuid.Nil {
		response.InternalError(c, "No posting journal available — check finance settings")
		return
	}

	// Transactional posting: either the full JE commits or nothing does.
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	nextNum := h.getNextJournalNumber(tx, journalID)
	entryNumber := fmt.Sprintf("DIV%06d", nextNum)
	journalEntryID := uuid.New()
	now := time.Now()

	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number,
			entry_date, reference, description,
			source_type, source_id, exchange_rate,
			total_debit, total_credit, status,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5,
			$6, $7, $8,
			'dividend_distribution', NULL, 1.0,
			$9, $9, 'posted',
			$10, $11, $11)
	`, journalEntryID, tenantID, orgIDPtr, journalID, entryNumber,
		distDate, in.ShareholderName,
		fmt.Sprintf("Dividend distribution to %s", in.ShareholderName),
		in.Amount, userID, now,
	); err != nil {
		h.log.Error("Failed to create dividend JE", "error", err)
		response.InternalError(c, "Failed to post dividend")
		return
	}

	// Dr Retained Earnings = gross
	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (
			id, journal_entry_id, line_number, account_id, description,
			debit_amount, credit_amount, exchange_rate, created_at
		) VALUES ($1, $2, 1, $3, $4, $5, 0, 1.0, $6)
	`, uuid.New(), journalEntryID, retainedID,
		"Dividend distribution — Retained Earnings", in.Amount, now,
	); err != nil {
		h.log.Error("Failed to post Dr retained earnings", "error", err)
		response.InternalError(c, "Failed to post dividend")
		return
	}
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, in.Amount, now, retainedID); err != nil {
		h.log.Error("Failed to update retained earnings balance", "error", err)
		response.InternalError(c, "Failed to post dividend")
		return
	}

	// Cr Cash / Bank = net_to_shareholder
	if net > 0 {
		if _, err := tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 2, $3, $4, 0, $5, 1.0, $6)
		`, uuid.New(), journalEntryID, cashID,
			fmt.Sprintf("Cash paid to %s", in.ShareholderName), net, now,
		); err != nil {
			h.log.Error("Failed to post Cr cash", "error", err)
			response.InternalError(c, "Failed to post dividend")
			return
		}
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, net, now, cashID); err != nil {
			h.log.Error("Failed to update cash balance for dividend", "error", err)
			response.InternalError(c, "Failed to post dividend")
			return
		}
	}

	// Cr Tax Liability = tax_amount (5% withheld)
	if taxAmount > 0 {
		if _, err := tx.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 3, $3, $4, 0, $5, 1.0, $6)
		`, uuid.New(), journalEntryID, taxLiabID,
			fmt.Sprintf("Dividend tax withheld (%.2f%%)", rate), taxAmount, now,
		); err != nil {
			h.log.Error("Failed to post Cr tax liability", "error", err)
			response.InternalError(c, "Failed to post dividend")
			return
		}
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, taxAmount, now, taxLiabID); err != nil {
			h.log.Error("Failed to update tax liability balance for dividend", "error", err)
			response.InternalError(c, "Failed to post dividend")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit dividend JE", "error", err)
		response.InternalError(c, "Failed to post dividend")
		return
	}

	response.Success(c, gin.H{
		"journal_entry_id":   journalEntryID,
		"entry_number":       entryNumber,
		"shareholder_name":   in.ShareholderName,
		"distribution_date":  distDate.Format("2006-01-02"),
		"gross_distribution": in.Amount,
		"rate":               rate,
		"tax_amount":         taxAmount,
		"net_to_shareholder": net,
	})
}
