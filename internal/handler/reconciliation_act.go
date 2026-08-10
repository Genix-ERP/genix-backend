package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// One definition of "an act, as the clients see it".
//
// GetReconciliationAct built this by hand: a 23-column projection, nine
// sql.Null* unwraps, then a live recompute from journal entries. The two other
// handlers that hand back an act did not reuse any of it, and both were wrong
// in ways only a reader comparing all three would notice:
//
//   - RefreshReconciliationAct assembled a fresh struct with Status hard-coded
//     to "draft" and CreatedAt set to time.Now(). Pressing "Yangilash" on a
//     confirmed act therefore reported it as a brand-new draft, on every client,
//     and dropped notes, the sent/response tracking and both reminder flags
//     along the way.
//   - UpdateReconciliationAct returned {"message": "..."} instead of the act.
//     Mobile parsed that message object AS an act, so confirming one blanked
//     the whole record on screen.
//
// Neither is really a coding slip; they are what happens when the same response
// is assembled in three places. Assembling it in one removes the possibility
// rather than the instances.
var errReconciliationNotFound = errors.New("reconciliation act not found")

// reconciliationFromSQL is the FROM clause the list, its count and the summary
// all share.
//
// The join to contacts is load-bearing, not decoration: the search filter
// matches ct.name, so a COUNT that dropped the join would describe a different
// set than the rows it is supposed to be counting — the classic pagination lie
// where the footer says 40 and the pages hold 12.
const reconciliationFromSQL = `
	FROM reconciliation_acts ra
	LEFT JOIN contacts ct ON ra.partner_id = ct.id`

// reconciliationLiveBalanceSQL is the act's closing balance, computed the same
// way the list computes it per row — but as a single aggregate.
//
// computeReconciliationData derives our_balance as
//
//	opening(entry_date < period_start) + debit(period) - credit(period)
//	  = SUM(d-c)[< start] + SUM(d)[start..end] - SUM(c)[start..end]
//	  = SUM(d-c)[< start] + SUM(d-c)[start..end]
//	  = SUM(d-c)[<= period_end]
//
// so the three-part computation collapses to one SUM with no change of
// meaning. That matters: it is what lets "Qoldiqi bor" be counted over the
// whole filtered set in ONE query instead of one query per act, while staying
// exactly consistent with the number each row shows. A count built on the
// STORED our_balance column would be cheaper still and would silently disagree
// with the list whenever an invoice in the period had been edited since the
// last refresh — wrong in precisely the way nobody notices.
const reconciliationLiveBalanceSQL = `
	COALESCE((
		SELECT SUM(jel.debit_amount - jel.credit_amount)
		FROM journal_entry_lines jel
		JOIN journal_entries je ON jel.journal_entry_id = je.id
		WHERE je.tenant_id = ra.tenant_id
		  AND jel.contact_id = ra.partner_id
		  AND je.entry_date <= ra.period_end
		  AND je.status = 'posted'
		  AND (%s)
	), 0)`

// reconciliationWhere builds the filter shared by the list, its count and the
// summary, so that all three describe the same set of acts.
//
// `includeStatus` is false for the summary: the summary's whole job is to
// report the per-status breakdown, and applying the caller's status filter to
// it would make the selected status equal the total and every other count zero.
func reconciliationWhere(c *gin.Context, tenantID uuid.UUID, includeStatus bool) (string, []interface{}, int) {
	where := " WHERE ra.tenant_id = $1 AND ra.deleted_at IS NULL"
	args := []interface{}{tenantID}
	n := 1

	if orgID, ok := middleware.GetOrganizationID(c); ok && orgID != uuid.Nil {
		n++
		where += fmt.Sprintf(" AND ra.organization_id = $%d", n)
		args = append(args, orgID)
	}
	if status := c.Query("status"); status != "" && includeStatus {
		n++
		where += fmt.Sprintf(" AND ra.status = $%d", n)
		args = append(args, status)
	}
	// Partner search. Without it a paginated client cannot search past its
	// first page, and an unpaginated one has to fetch every act to do it in
	// the browser — which is what both clients were doing.
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		n++
		where += fmt.Sprintf(" AND ct.name ILIKE $%d", n)
		args = append(args, "%"+search+"%")
	}
	return where, args, n
}

// loadReconciliationAct returns the act exactly as GET /reconciliation/:id
// would, including the live recompute from journal entries.
//
// orgIDPtr scopes that recompute; pass nil when the request has no organization
// context. It must be the SAME scope the caller used elsewhere in the request,
// or the totals returned will not match the ones just written.
func (h *Handler) loadReconciliationAct(tenantID, id uuid.UUID, orgIDPtr *uuid.UUID) (*reconciliationActResponse, error) {
	var act reconciliationActResponse
	var notes sql.NullString
	var periodStart, periodEnd time.Time
	var responseStatus, disputeNote, respondentName, sentVia, sentTo sql.NullString
	var respondedAt, sentAt, actShareExpiresAt sql.NullTime
	var actDisputeAmount sql.NullFloat64

	err := h.db.QueryRow(`
		SELECT ra.id, ra.partner_id, COALESCE(ct.name, '') as partner_name,
			   ra.period_start, ra.period_end,
			   ra.opening_balance, ra.our_debit_total, ra.our_credit_total, ra.our_balance,
			   ra.status, ra.notes, ra.created_at,
			   ra.response_status, ra.responded_at, ra.dispute_note, ra.respondent_name,
			   ra.sent_at, ra.sent_via, ra.sent_to,
			   ra.dispute_amount, ra.share_expires_at,
			   COALESCE(ra.reminder_3d_sent, false), COALESCE(ra.reminder_7d_sent, false)
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
		&act.Reminder3dSent, &act.Reminder7dSent,
	)
	if err == sql.ErrNoRows {
		return nil, errReconciliationNotFound
	}
	if err != nil {
		return nil, err
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

	// Totals come from the journal entries as they stand now, not from the
	// stored columns, which go stale the moment an invoice or payment in the
	// period is edited. A failure here is logged and the stored values stand:
	// an act with slightly old totals is worth more to the caller than an
	// error, and the stored values are what the last refresh wrote.
	liveOpening, lines, liveDebit, liveCredit, linesErr := h.computeReconciliationData(
		tenantID, act.PartnerID, orgIDPtr, act.PeriodStart, act.PeriodEnd)
	if linesErr != nil {
		h.log.Error("Failed to fetch reconciliation lines", "error", linesErr, "act_id", id)
	} else {
		act.Lines = lines
		act.OpeningBalance = liveOpening
		act.OurDebitTotal = liveDebit
		act.OurCreditTotal = liveCredit
		act.OurBalance = liveOpening + liveDebit - liveCredit
		act.ClosingBalance = act.OurBalance
	}

	return &act, nil
}

// GetReconciliationSummary godoc
// @Summary Counts for the Akt sverka summary cards
// @Tags Finance - Reconciliation
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /reconciliation/summary [get]
//
// One request in place of the fan-out both clients had grown: mobile issued
// five page_size=1 list calls and then SUBTRACTED to get the draft count
// (total - sent - confirmed - disputed - discrepancy), which happened to be
// right only while those five were the only statuses. Web counted in the
// browser over an unpaginated fetch of every act.
//
// with_balance is the count behind "Qoldiqi bor" and is the reason this
// endpoint has to exist at all: it cannot be derived by a paginated client
// without lying about the rows it has not loaded.
func (h *Handler) GetReconciliationSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	where, args, n := reconciliationWhere(c, tenantID, false)

	// The organization filter has to be threaded into the correlated subquery
	// too. Counting balances across every organization while the rows are
	// scoped to one would make "Qoldiqi bor" exceed "Jami aktlar".
	orgPredicate := "TRUE"
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		n++
		orgPredicate = fmt.Sprintf("je.organization_id = $%d", n)
		args = append(args, orgID)
	}
	liveBalance := fmt.Sprintf(reconciliationLiveBalanceSQL, orgPredicate)

	query := `
		SELECT
			COUNT(*)                                                        AS total,
			COUNT(*) FILTER (WHERE ra.status = 'draft')                     AS draft,
			COUNT(*) FILTER (WHERE ra.status = 'sent')                      AS sent,
			COUNT(*) FILTER (WHERE ra.status = 'confirmed')                 AS confirmed,
			COUNT(*) FILTER (WHERE ra.status = 'disputed')                  AS disputed,
			COUNT(*) FILTER (WHERE ra.status = 'discrepancy')               AS discrepancy,
			COUNT(*) FILTER (WHERE ra.status = 'no_response')               AS no_response,
			COUNT(*) FILTER (WHERE ABS(` + liveBalance + `) > 0.01)         AS with_balance
		` + reconciliationFromSQL + where

	var s struct {
		Total       int `json:"total"`
		Draft       int `json:"draft"`
		Sent        int `json:"sent"`
		Confirmed   int `json:"confirmed"`
		Disputed    int `json:"disputed"`
		Discrepancy int `json:"discrepancy"`
		NoResponse  int `json:"no_response"`
		WithBalance int `json:"with_balance"`
	}
	if err := h.db.QueryRow(query, args...).Scan(
		&s.Total, &s.Draft, &s.Sent, &s.Confirmed,
		&s.Disputed, &s.Discrepancy, &s.NoResponse, &s.WithBalance,
	); err != nil {
		h.log.Error("Failed to compute reconciliation summary", "error", err)
		response.InternalError(c, "Failed to compute reconciliation summary")
		return
	}

	response.Success(c, s)
}
