package handler

// Accounting diagnostics (genix_diagnostika_spec §2.4) — anomaly detection over
// the trial balance so a wrong проводка is spotted at a glance and drilled into
// via the account card. The scan mirrors GetTrialBalanceWithTurnover's posted-
// only, tenant/org-scoped computation but keeps NON-leaf accounts too (to catch
// postings made on a group node instead of a leaf subaccount).
//
// Besides the per-account balance rules it runs entry-level integrity scans
// (orphan/duplicate source refs, zero-line and unbalanced posted entries,
// months without postings). Entry-level findings reuse the same anomaly shape
// with an empty account_id — there is no account card to drill into.

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

const anomalyEps = 0.005

// anomalyScanLimit caps each entry-level scan so a systemic breakage (e.g.
// thousands of orphans after a bad purge) doesn't flood the panel.
const anomalyScanLimit = 50

// GetTrialBalanceAnomalies scans a period for the §2.4 anomaly rules.
// GET /reports/trial-balance/anomalies?period_from=&period_to=
func (h *Handler) GetTrialBalanceAnomalies(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	periodFrom := c.Query("period_from")
	periodTo := c.Query("period_to")
	if periodFrom == "" || periodTo == "" {
		response.BadRequest(c, "period_from and period_to are required (YYYY-MM-DD)")
		return
	}
	orgID, orgOK := middleware.GetOrganizationID(c)

	q := `
		SELECT a.id, a.code,
		       COALESCE(NULLIF(a.name_uz,''), a.name) AS name_uz,
		       COALESCE(NULLIF(a.name_ru,''), a.name) AS name_ru,
		       at.category, at.normal_balance, COALESCE(a.is_leaf, true) AS is_leaf,
		       COALESCE(SUM(CASE WHEN je.entry_date <  $2 THEN jel.debit_amount  ELSE 0 END), 0) AS prior_dt,
		       COALESCE(SUM(CASE WHEN je.entry_date <  $2 THEN jel.credit_amount ELSE 0 END), 0) AS prior_kt,
		       COALESCE(SUM(CASE WHEN je.entry_date >= $2 AND je.entry_date <= $3 THEN jel.debit_amount  ELSE 0 END), 0) AS period_dt,
		       COALESCE(SUM(CASE WHEN je.entry_date >= $2 AND je.entry_date <= $3 THEN jel.credit_amount ELSE 0 END), 0) AS period_kt
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
		LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
			AND je.status = 'posted' AND je.deleted_at IS NULL
		WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true`
	args := []interface{}{tenantID, periodFrom, periodTo}
	if orgOK && orgID != uuid.Nil {
		q += " AND a.organization_id = $4"
		args = append(args, orgID)
	}
	q += " GROUP BY a.id, a.code, a.name, a.name_uz, a.name_ru, at.category, at.normal_balance, a.is_leaf ORDER BY a.code"

	rows, err := h.db.Query(q, args...)
	if err != nil {
		h.log.Error("GetTrialBalanceAnomalies: query failed", "error", err)
		response.InternalError(c, "Failed to scan trial balance")
		return
	}
	defer rows.Close()

	anomalies := []gin.H{}
	var totDt, totKt float64
	crit, warn, info := 0, 0, 0
	addMsg := func(id, code, nu, nr, rule, sev string, cd, ck float64, mu, mr string) {
		anomalies = append(anomalies, gin.H{
			"account_id": id, "code": code, "name_uz": nu, "name_ru": nr,
			"rule": rule, "severity": sev, "closing_debit": cd, "closing_credit": ck,
			"message_uz": mu, "message_ru": mr,
		})
		switch sev {
		case "critical":
			crit++
		case "warning":
			warn++
		default:
			info++
		}
	}
	add := func(id, code, nu, nr, rule, sev string, cd, ck float64) {
		mu, mr := anomalyMessage(rule)
		addMsg(id, code, nu, nr, rule, sev, cd, ck, mu, mr)
	}

	// TRANSIT_NOT_CLOSED context: transit (9xxx / revenue / expense) balances are
	// only an error once the year is closed — mid-year they are the normal state.
	// closedEnd = latest closed period end for the tenant ('' when nothing was
	// ever closed); transitOpenAtClose = transit accounts still non-zero AS OF
	// that date (i.e. the close ran but didn't zero them — a real violation).
	closedEndQ := `SELECT COALESCE(MAX(period_end)::text, '') FROM period_closings
		WHERE tenant_id = $1 AND status = 'closed'`
	closedArgs := []interface{}{tenantID}
	if orgOK && orgID != uuid.Nil {
		// tenant-level closings (organization_id IS NULL) close the org's books too
		closedEndQ += " AND (organization_id IS NULL OR organization_id = $2)"
		closedArgs = append(closedArgs, orgID)
	}
	var closedEnd string
	if err := h.db.QueryRow(closedEndQ, closedArgs...).Scan(&closedEnd); err != nil {
		h.log.Error("GetTrialBalanceAnomalies: closed-period lookup failed", "error", err)
	}

	transitOpenAtClose := map[string]bool{}
	if closedEnd != "" {
		tq := `
			SELECT a.id
			FROM accounts a
			JOIN account_types at ON a.account_type_id = at.id
			LEFT JOIN journal_entry_lines jel ON jel.account_id = a.id
			LEFT JOIN journal_entries je ON jel.journal_entry_id = je.id
				AND je.status = 'posted' AND je.deleted_at IS NULL AND je.entry_date <= $2
			WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.is_active = true
			  AND (at.category IN ('revenue', 'expense') OR a.code LIKE '9%')`
		tArgs := []interface{}{tenantID, closedEnd}
		if orgOK && orgID != uuid.Nil {
			tq += " AND a.organization_id = $3"
			tArgs = append(tArgs, orgID)
		}
		tq += ` GROUP BY a.id
			HAVING ABS(COALESCE(SUM(jel.debit_amount), 0) - COALESCE(SUM(jel.credit_amount), 0)) > 0.005`
		tRows, tErr := h.db.Query(tq, tArgs...)
		if tErr != nil {
			h.log.Error("GetTrialBalanceAnomalies: transit-at-close query failed", "error", tErr)
		} else {
			for tRows.Next() {
				var id string
				if tRows.Scan(&id) == nil {
					transitOpenAtClose[id] = true
				}
			}
			tRows.Close()
		}
	}

	for rows.Next() {
		var id, code, nu, nr, category, normal string
		var isLeaf bool
		var priorDt, priorKt, periodDt, periodKt float64
		if rows.Scan(&id, &code, &nu, &nr, &category, &normal, &isLeaf, &priorDt, &priorKt, &periodDt, &periodKt) != nil {
			continue
		}
		totDt += periodDt
		totKt += periodKt

		closing := (priorDt - priorKt) + periodDt - periodKt
		cd, ck := 0.0, 0.0
		if closing >= 0 {
			cd = closing
		} else {
			ck = -closing
		}
		turnover := priorDt + priorKt + periodDt + periodKt
		isTransit := category == "revenue" || category == "expense" || strings.HasPrefix(code, "9")

		// GROUP_NODE_POSTING — direct postings on a non-leaf (group) account. This
		// is the headline problem for that account; skip the balance-nature rules.
		if !isLeaf {
			if turnover > anomalyEps {
				add(id, code, nu, nr, "GROUP_NODE_POSTING", "critical", cd, ck)
			}
			continue
		}

		switch {
		case isTransit && (closing > anomalyEps || closing < -anomalyEps):
			// Critical only when a closed period exists AND the balance was still
			// open at that closing date; otherwise it's the normal mid-year state
			// (every P&L account carries a balance until the year-end close).
			if closedEnd != "" && transitOpenAtClose[id] {
				add(id, code, nu, nr, "TRANSIT_NOT_CLOSED", "critical", cd, ck)
			} else {
				addMsg(id, code, nu, nr, "TRANSIT_NOT_CLOSED", "info", cd, ck,
					"9xxx (tranzit) hisobda qoldiq bor — yil yopilganda tekshiriladi",
					"Остаток на транзитном счёте (9xxx) — будет проверено при закрытии года")
			}
		case normal == "debit" && closing < -anomalyEps:
			add(id, code, nu, nr, "NEG_ACTIVE_BALANCE", "critical", cd, ck)
		case normal == "credit" && closing > anomalyEps:
			add(id, code, nu, nr, "DEBIT_PASSIVE_BALANCE", "warning", cd, ck)
		}
		if strings.HasPrefix(code, "0820") && (closing > anomalyEps || closing < -anomalyEps) {
			add(id, code, nu, nr, "CAPEX_STUCK", "warning", cd, ck)
		}
	}

	// jeOrg appends the journal-entry org filter at the given placeholder index
	// (the entry-level scans below take different parameter lists).
	jeOrg := func(idx int) string {
		if orgOK && orgID != uuid.Nil {
			return fmt.Sprintf(" AND je.organization_id = $%d", idx)
		}
		return ""
	}
	jeOrgArgs := func(base ...interface{}) []interface{} {
		if orgOK && orgID != uuid.Nil {
			return append(base, orgID)
		}
		return base
	}

	// ORPHAN_SOURCE — posted entries whose source document is gone. The map of
	// source_type → table follows the writers' conventions: payroll JEs store the
	// payroll_periods id, per-asset depreciation JEs the fa_assets id, run-level
	// depreciation JEs the fa_depreciation_runs id.
	orphanQ := `
		SELECT je.entry_number, je.source_type, je.source_id::text, je.total_debit
		FROM journal_entries je
		WHERE je.tenant_id = $1 AND je.status = 'posted' AND je.deleted_at IS NULL
		  AND je.source_id IS NOT NULL
		  AND ((je.source_type = 'expense'          AND NOT EXISTS (SELECT 1 FROM expenses t WHERE t.id = je.source_id))
		    OR (je.source_type = 'sales_invoice'    AND NOT EXISTS (SELECT 1 FROM sales_invoices t WHERE t.id = je.source_id))
		    OR (je.source_type = 'purchase_invoice' AND NOT EXISTS (SELECT 1 FROM purchase_invoices t WHERE t.id = je.source_id))
		    OR (je.source_type = 'payroll'          AND NOT EXISTS (SELECT 1 FROM payroll_periods t WHERE t.id = je.source_id))
		    OR (je.source_type = 'cash_order'       AND NOT EXISTS (SELECT 1 FROM cash_orders t WHERE t.id = je.source_id))
		    OR (je.source_type = 'depreciation'     AND NOT EXISTS (SELECT 1 FROM fa_assets t WHERE t.id = je.source_id))
		    OR (je.source_type = 'depreciation_run' AND NOT EXISTS (SELECT 1 FROM fa_depreciation_runs t WHERE t.id = je.source_id)))` +
		jeOrg(2) + `
		ORDER BY je.entry_date, je.entry_number
		LIMIT ` + fmt.Sprint(anomalyScanLimit)
	if oRows, oErr := h.db.Query(orphanQ, jeOrgArgs(tenantID)...); oErr != nil {
		h.log.Error("GetTrialBalanceAnomalies: orphan-source scan failed", "error", oErr)
	} else {
		for oRows.Next() {
			var entryNum, srcType, srcID string
			var amount float64
			if oRows.Scan(&entryNum, &srcType, &srcID, &amount) != nil {
				continue
			}
			label := fmt.Sprintf("manba: %s %.8s", srcType, srcID)
			add("", entryNum, label, label, "ORPHAN_SOURCE", "critical", amount, 0)
		}
		oRows.Close()
	}

	// DUPLICATE_SOURCE — more than one live posted JE per (source_type,
	// source_id). By-design pairs documented in the 2026-08-10 ledger audit are
	// excluded: storno chains (is_reversal / reversal_of_id / reversed_entry_id
	// on either side), payroll_avans_reclass (reclass + counter-reclass share the
	// source_id on purpose), payment_receipt (mixed-source convention — the JE
	// may carry the payment id or the settled document id), and 'manual'/
	// 'reversal' which have no one-JE-per-document contract at all.
	dupQ := `
		SELECT je.source_type, je.source_id::text, COUNT(*),
		       MIN(je.entry_number), MAX(je.entry_number), COALESCE(SUM(je.total_debit), 0)
		FROM journal_entries je
		WHERE je.tenant_id = $1 AND je.status = 'posted' AND je.deleted_at IS NULL
		  AND je.source_id IS NOT NULL
		  AND COALESCE(je.is_reversal, false) = false
		  AND je.reversal_of_id IS NULL
		  AND je.reversed_entry_id IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM journal_entries r
		      WHERE (r.reversal_of_id = je.id OR r.reversed_entry_id = je.id)
		        AND r.status = 'posted' AND r.deleted_at IS NULL)
		  AND je.source_type NOT IN ('manual', 'reversal', 'payroll_avans_reclass', 'payment_receipt')` +
		jeOrg(2) + `
		GROUP BY je.source_type, je.source_id
		HAVING COUNT(*) > 1
		ORDER BY COUNT(*) DESC, je.source_type
		LIMIT ` + fmt.Sprint(anomalyScanLimit)
	if dRows, dErr := h.db.Query(dupQ, jeOrgArgs(tenantID)...); dErr != nil {
		h.log.Error("GetTrialBalanceAnomalies: duplicate-source scan failed", "error", dErr)
	} else {
		for dRows.Next() {
			var srcType, srcID, minNum, maxNum string
			var cnt int
			var amount float64
			if dRows.Scan(&srcType, &srcID, &cnt, &minNum, &maxNum, &amount) != nil {
				continue
			}
			nu := fmt.Sprintf("%.8s → %d ta provodka (%s … %s)", srcID, cnt, minNum, maxNum)
			nr := fmt.Sprintf("%.8s → %d проводок (%s … %s)", srcID, cnt, minNum, maxNum)
			add("", srcType, nu, nr, "DUPLICATE_SOURCE", "warning", amount, 0)
		}
		dRows.Close()
	}

	// ZERO_LINE_ENTRY — posted entries with no lines at all (historic bug class:
	// migration 416 deferred trigger + non-tx JE paths).
	zeroQ := `
		SELECT je.entry_number, je.entry_date::text
		FROM journal_entries je
		WHERE je.tenant_id = $1 AND je.status = 'posted' AND je.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM journal_entry_lines l WHERE l.journal_entry_id = je.id)` +
		jeOrg(2) + `
		ORDER BY je.entry_date
		LIMIT ` + fmt.Sprint(anomalyScanLimit)
	if zRows, zErr := h.db.Query(zeroQ, jeOrgArgs(tenantID)...); zErr != nil {
		h.log.Error("GetTrialBalanceAnomalies: zero-line scan failed", "error", zErr)
	} else {
		for zRows.Next() {
			var entryNum, entryDate string
			if zRows.Scan(&entryNum, &entryDate) != nil {
				continue
			}
			add("", entryNum, entryDate, entryDate, "ZERO_LINE_ENTRY", "critical", 0, 0)
		}
		zRows.Close()
	}

	// UNBALANCED_ENTRY — per-entry Dt ≠ Kt. The deferred DB trigger should make
	// this impossible; scanning anyway localizes WHICH entry broke if the
	// aggregate balance_broken banner ever fires.
	unbalQ := `
		SELECT je.entry_number, je.entry_date::text, SUM(l.debit_amount), SUM(l.credit_amount)
		FROM journal_entries je
		JOIN journal_entry_lines l ON l.journal_entry_id = je.id
		WHERE je.tenant_id = $1 AND je.status = 'posted' AND je.deleted_at IS NULL` +
		jeOrg(2) + `
		GROUP BY je.id, je.entry_number, je.entry_date
		HAVING ABS(SUM(l.debit_amount) - SUM(l.credit_amount)) > 0.005
		ORDER BY je.entry_date
		LIMIT ` + fmt.Sprint(anomalyScanLimit)
	if uRows, uErr := h.db.Query(unbalQ, jeOrgArgs(tenantID)...); uErr != nil {
		h.log.Error("GetTrialBalanceAnomalies: unbalanced scan failed", "error", uErr)
	} else {
		for uRows.Next() {
			var entryNum, entryDate string
			var sd, sc float64
			if uRows.Scan(&entryNum, &entryDate, &sd, &sc) != nil {
				continue
			}
			add("", entryNum, entryDate, entryDate, "UNBALANCED_ENTRY", "critical", sd, sc)
		}
		uRows.Close()
	}

	// PERIOD_GAP — months of period_to's year with zero postings BETWEEN months
	// that have postings (leading/trailing empty months are not gaps).
	gapQ := `
		WITH months AS (
			SELECT date_trunc('month', je.entry_date)::date AS m
			FROM journal_entries je
			WHERE je.tenant_id = $1 AND je.status = 'posted' AND je.deleted_at IS NULL
			  AND je.entry_date >= date_trunc('year', $2::date)
			  AND je.entry_date <  date_trunc('year', $2::date) + interval '1 year'` +
		jeOrg(3) + `
			GROUP BY 1
		)
		SELECT to_char(gs.m, 'YYYY-MM')
		FROM generate_series((SELECT MIN(m) FROM months), (SELECT MAX(m) FROM months), interval '1 month') AS gs(m)
		WHERE NOT EXISTS (SELECT 1 FROM months WHERE months.m = gs.m::date)
		ORDER BY 1`
	if gRows, gErr := h.db.Query(gapQ, jeOrgArgs(tenantID, periodTo)...); gErr != nil {
		h.log.Error("GetTrialBalanceAnomalies: period-gap scan failed", "error", gErr)
	} else {
		for gRows.Next() {
			var month string
			if gRows.Scan(&month) != nil {
				continue
			}
			add("", month, "Provodkasiz oy", "Месяц без проводок", "PERIOD_GAP", "warning", 0, 0)
		}
		gRows.Close()
	}

	diff := totDt - totKt
	response.Success(c, gin.H{
		"period_from":    periodFrom,
		"period_to":      periodTo,
		"balance_broken": diff > anomalyEps || diff < -anomalyEps,
		"totals":         gin.H{"turnover_debit": totDt, "turnover_credit": totKt, "diff": diff},
		"counts":         gin.H{"critical": crit, "warning": warn, "info": info, "total": crit + warn + info},
		"anomalies":      anomalies,
	})
}

// anomalyMessage returns the uz/ru explanation for an anomaly rule (§2.4).
func anomalyMessage(rule string) (string, string) {
	switch rule {
	case "NEG_ACTIVE_BALANCE":
		return "Aktiv hisobda kredit (manfiy) qoldiq — noto'g'ri provodka bo'lishi mumkin",
			"Кредитовое сальдо на активном счёте — вероятна ошибочная проводка"
	case "DEBIT_PASSIVE_BALANCE":
		return "Passiv hisobda debet qoldiq — tekshiring",
			"Дебетовое сальдо на пассивном счёте — проверьте"
	case "TRANSIT_NOT_CLOSED":
		return "9xxx (tranzit) hisob yopilmagan — davr oxirida qoldiq qolgan",
			"Транзитный счёт (9xxx) не закрыт — остался остаток на конец периода"
	case "CAPEX_STUCK":
		return "0820 da qoldiq — kapital qo'yilma asosiy vositaga o'tkazilmagan",
			"Остаток на 0820 — капвложение не переведено в ОС"
	case "GROUP_NODE_POSTING":
		return "Provodka guruh hisobiga qilingan, subhisobga emas",
			"Проводка сделана на групповой счёт, а не на субсчёт"
	case "ORPHAN_SOURCE":
		return "Provodkaning manba hujjati topilmadi — hujjat o'chirilgan, provodka 'yetim' qolgan",
			"Документ-источник проводки не найден — проводка осталась «сиротой»"
	case "DUPLICATE_SOURCE":
		return "Bitta hujjatga bir nechta provodka o'tkazilgan — ikki marta hisob bo'lishi mumkin",
			"На один документ проведено несколько проводок — возможен двойной учёт"
	case "ZERO_LINE_ENTRY":
		return "O'tkazilgan provodkada birorta ham qator yo'q",
			"Проведённая проводка не содержит строк"
	case "UNBALANCED_ENTRY":
		return "Provodkada debet va kredit teng emas",
			"В проводке дебет не равен кредиту"
	case "PERIOD_GAP":
		return "Bu oyda birorta ham provodka yo'q (oldingi va keyingi oylarda bor) — davr tushib qolgan bo'lishi mumkin",
			"В этом месяце нет ни одной проводки (в соседних месяцах есть) — возможно, пропущен период"
	}
	return rule, rule
}
