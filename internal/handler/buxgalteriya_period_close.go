package handler

// File: buxgalteriya_period_close.go
//
// TT Buxgalteriya ERP §4.3 + §4.4 period-close workflow.
// Wraps the fn_close_accounting_period / fn_reopen_accounting_period procedures
// created in migration 321.
//
// Endpoints:
//   POST /finance/periods/close    — run close procedure for a date range
//   POST /finance/periods/reopen   — reverse a close (admin only at route layer)
//   GET  /finance/periods/closings — list past closings
//   GET  /finance/periods/closings/:id — closing with its invariant checks

import (
	"database/sql"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

type closePeriodInput struct {
	PeriodStart string `json:"period_start" binding:"required"` // YYYY-MM-DD
	PeriodEnd   string `json:"period_end"   binding:"required"` // YYYY-MM-DD
	Notes       string `json:"notes"`
}

type periodClosingSummary struct {
	ID                    uuid.UUID            `json:"id"`
	PeriodStart           string               `json:"period_start"`
	PeriodEnd             string               `json:"period_end"`
	Status                string               `json:"status"`
	TotalDebit            float64              `json:"total_debit"`
	TotalCredit           float64              `json:"total_credit"`
	IsBalanced            bool                 `json:"is_balanced"`
	DraftEntryCount       int                  `json:"draft_entry_count"`
	PnlDebit              float64              `json:"pnl_debit"`
	PnlCredit             float64              `json:"pnl_credit"`
	NetProfit             float64              `json:"net_profit"`
	ClosingJournalEntryID *uuid.UUID           `json:"closing_journal_entry_id,omitempty"`
	StartedAt             time.Time            `json:"started_at"`
	CompletedAt           *time.Time           `json:"completed_at,omitempty"`
	StartedBy             *uuid.UUID           `json:"started_by,omitempty"`
	Checks                []periodClosingCheck `json:"checks,omitempty"`
}

type periodClosingCheck struct {
	CheckName     string  `json:"check_name"`
	Passed        bool    `json:"passed"`
	ExpectedValue *string `json:"expected_value,omitempty"`
	ActualValue   *string `json:"actual_value,omitempty"`
	Message       *string `json:"message,omitempty"`
}

// ClosePeriod runs fn_close_accounting_period and returns the summary with checks.
func (h *Handler) ClosePeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input closePeriodInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDArg interface{}
	if orgID != uuid.Nil {
		orgIDArg = orgID
	}

	var closingID uuid.UUID
	err := h.db.QueryRow(
		`SELECT fn_close_accounting_period($1, $2, $3::date, $4::date, $5)`,
		tenantID, orgIDArg, input.PeriodStart, input.PeriodEnd, userID,
	).Scan(&closingID)
	if err != nil {
		h.log.Error("ClosePeriod: fn_close_accounting_period failed", "error", err)
		response.InternalError(c, "Failed to close period: "+err.Error())
		return
	}

	summary, err := h.loadClosingSummary(closingID)
	if err != nil {
		h.log.Error("ClosePeriod: loadClosingSummary failed", "error", err)
		response.InternalError(c, "Period closed but summary load failed")
		return
	}

	// TT §7.4 — dispatch webhook event
	if summary.Status == "closed" {
		go h.DispatchWebhookEvent(tenantID, "period.closed", summary)
	}

	response.Success(c, summary)
}

// ReopenPeriod calls fn_reopen_accounting_period.
func (h *Handler) ReopenPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	closingIDStr := c.Param("id")
	closingID, err := uuid.Parse(closingIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid closing ID")
		return
	}

	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Reason is required")
		return
	}

	// Confirm the closing belongs to this tenant before reopening.
	var exists bool
	if err := h.db.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM period_closings
		 WHERE id = $1 AND tenant_id = $2 AND status = 'closed')
	`, closingID, tenantID).Scan(&exists); err != nil || !exists {
		response.NotFound(c, "Closed period")
		return
	}

	var ok2 bool
	if err := h.db.QueryRow(
		`SELECT fn_reopen_accounting_period($1, $2, $3)`,
		closingID, userID, body.Reason,
	).Scan(&ok2); err != nil {
		h.log.Error("ReopenPeriod: fn_reopen failed", "error", err)
		response.InternalError(c, "Failed to reopen period")
		return
	}
	if !ok2 {
		response.BadRequest(c, "Closing is not in state 'closed'")
		return
	}

	summary, err := h.loadClosingSummary(closingID)
	if err != nil {
		response.Success(c, gin.H{"id": closingID, "status": "reopened"})
		return
	}
	response.Success(c, summary)
}

// ListClosings returns past period closings for the tenant, newest first.
func (h *Handler) ListClosings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, period_start, period_end, status,
		       total_debit, total_credit, is_balanced, draft_entry_count,
		       pnl_debit, pnl_credit, net_profit, closing_journal_entry_id,
		       started_at, completed_at, started_by
		FROM period_closings
		WHERE tenant_id = $1
		ORDER BY started_at DESC
		LIMIT 100
	`, tenantID)
	if err != nil {
		h.log.Error("ListClosings: query failed", "error", err)
		response.InternalError(c, "Failed to list closings")
		return
	}
	defer rows.Close()

	out := make([]periodClosingSummary, 0)
	for rows.Next() {
		s, err := scanClosing(rows)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	response.Success(c, out)
}

// GetClosing returns a single closing with its checks.
func (h *Handler) GetClosing(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid closing ID")
		return
	}

	var tID uuid.UUID
	if err := h.db.QueryRow(`SELECT tenant_id FROM period_closings WHERE id = $1`, id).
		Scan(&tID); err != nil || tID != tenantID {
		response.NotFound(c, "Closing")
		return
	}

	summary, err := h.loadClosingSummary(id)
	if err != nil {
		response.InternalError(c, "Failed to load closing")
		return
	}
	response.Success(c, summary)
}

// GetCloseChecklist previews what would block or should precede a period close
// for the given range: draft entries (fn_close_accounting_period hard-fails on
// them), months with no posted depreciation run, and vipiska imports with
// unmatched lines. GET /finance/periods/close-checklist?period_start&period_end
func (h *Handler) GetCloseChecklist(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	periodStart := c.Query("period_start")
	periodEnd := c.Query("period_end")
	if _, err := time.Parse("2006-01-02", periodStart); err != nil {
		response.BadRequest(c, "period_start (YYYY-MM-DD) is required")
		return
	}
	if _, err := time.Parse("2006-01-02", periodEnd); err != nil {
		response.BadRequest(c, "period_end (YYYY-MM-DD) is required")
		return
	}

	var draftCount int
	var draftSum float64
	_ = h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_debit), 0)
		FROM journal_entries
		WHERE tenant_id = $1 AND status = 'draft' AND deleted_at IS NULL
		  AND entry_date >= $2 AND entry_date <= $3
	`, tenantID, periodStart, periodEnd).Scan(&draftCount, &draftSum)

	// Months in the range with no posted depreciation run (only meaningful for
	// tenants that own depreciable assets).
	missingDepMonths := make([]string, 0, 4)
	var hasAssets bool
	_ = h.db.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM fa_assets
			WHERE tenant_id = $1 AND deleted_at IS NULL AND status = 'in_service')
	`, tenantID).Scan(&hasAssets)
	if hasAssets {
		rows, err := h.db.Query(`
			SELECT to_char(m, 'YYYY-MM')
			FROM generate_series(date_trunc('month', $2::date),
			                     date_trunc('month', $3::date), interval '1 month') AS m
			WHERE NOT EXISTS (
				SELECT 1 FROM fa_depreciation_runs r
				WHERE r.tenant_id = $1 AND r.period = to_char(m, 'YYYY-MM')
				  AND r.status = 'posted'
			)
			ORDER BY 1
		`, tenantID, periodStart, periodEnd)
		if err == nil {
			for rows.Next() {
				var ym string
				if rows.Scan(&ym) == nil {
					missingDepMonths = append(missingDepMonths, ym)
				}
			}
			rows.Close()
		}
	}

	var unmatchedImports, unmatchedLines int
	_ = h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(unmatched_count), 0)
		FROM bank_statement_imports
		WHERE tenant_id = $1 AND statement_date >= $2 AND statement_date <= $3
		  AND COALESCE(unmatched_count, 0) > 0
	`, tenantID, periodStart, periodEnd).Scan(&unmatchedImports, &unmatchedLines)

	var periodStatus sql.NullString
	_ = h.db.QueryRow(`
		SELECT fp.status
		FROM fiscal_periods fp
		JOIN fiscal_years fy ON fy.id = fp.fiscal_year_id
		WHERE fy.tenant_id = $1 AND fp.start_date <= $2::date AND fp.end_date >= $2::date
		ORDER BY fp.start_date DESC LIMIT 1
	`, tenantID, periodStart).Scan(&periodStatus)

	ready := draftCount == 0 && len(missingDepMonths) == 0
	response.Success(c, gin.H{
		"period_start":                periodStart,
		"period_end":                  periodEnd,
		"period_status":               periodStatus.String,
		"draft_entries":               draftCount,
		"draft_entries_total":         draftSum,
		"missing_depreciation_months": missingDepMonths,
		"unmatched_vipiska_imports":   unmatchedImports,
		"unmatched_vipiska_lines":     unmatchedLines,
		"ready":                       ready,
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (h *Handler) loadClosingSummary(id uuid.UUID) (periodClosingSummary, error) {
	var s periodClosingSummary

	row := h.db.QueryRow(`
		SELECT id, period_start, period_end, status,
		       total_debit, total_credit, is_balanced, draft_entry_count,
		       pnl_debit, pnl_credit, net_profit, closing_journal_entry_id,
		       started_at, completed_at, started_by
		FROM period_closings WHERE id = $1
	`, id)
	s, err := scanClosing(row)
	if err != nil {
		return s, err
	}

	// Fetch checks
	rows, err := h.db.Query(`
		SELECT check_name, passed, expected_value, actual_value, message
		FROM period_closing_checks
		WHERE period_closing_id = $1
		ORDER BY created_at ASC
	`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var cc periodClosingCheck
			var exp, act, msg sql.NullString
			if err := rows.Scan(&cc.CheckName, &cc.Passed, &exp, &act, &msg); err != nil {
				continue
			}
			if exp.Valid {
				v := exp.String
				cc.ExpectedValue = &v
			}
			if act.Valid {
				v := act.String
				cc.ActualValue = &v
			}
			if msg.Valid {
				v := msg.String
				cc.Message = &v
			}
			s.Checks = append(s.Checks, cc)
		}
	}

	return s, nil
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanClosing(r rowScanner) (periodClosingSummary, error) {
	var s periodClosingSummary
	var periodStart, periodEnd time.Time
	var completedAt sql.NullTime
	var closingJEID, startedBy sql.NullString

	err := r.Scan(
		&s.ID, &periodStart, &periodEnd, &s.Status,
		&s.TotalDebit, &s.TotalCredit, &s.IsBalanced, &s.DraftEntryCount,
		&s.PnlDebit, &s.PnlCredit, &s.NetProfit, &closingJEID,
		&s.StartedAt, &completedAt, &startedBy,
	)
	if err != nil {
		return s, err
	}
	s.PeriodStart = periodStart.Format("2006-01-02")
	s.PeriodEnd = periodEnd.Format("2006-01-02")
	if completedAt.Valid {
		t := completedAt.Time
		s.CompletedAt = &t
	}
	if closingJEID.Valid {
		if u, err := uuid.Parse(closingJEID.String); err == nil {
			s.ClosingJournalEntryID = &u
		}
	}
	if startedBy.Valid {
		if u, err := uuid.Parse(startedBy.String); err == nil {
			s.StartedBy = &u
		}
	}
	return s, nil
}
