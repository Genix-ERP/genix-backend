package handler

// Accounting diagnostics (genix_diagnostika_spec §2.4) — anomaly detection over
// the trial balance so a wrong проводка is spotted at a glance and drilled into
// via the account card. The scan mirrors GetTrialBalanceWithTurnover's posted-
// only, tenant/org-scoped computation but keeps NON-leaf accounts too (to catch
// postings made on a group node instead of a leaf subaccount).

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

const anomalyEps = 0.005

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
	crit, warn := 0, 0
	add := func(id, code, nu, nr, rule, sev string, cd, ck float64) {
		mu, mr := anomalyMessage(rule)
		anomalies = append(anomalies, gin.H{
			"account_id": id, "code": code, "name_uz": nu, "name_ru": nr,
			"rule": rule, "severity": sev, "closing_debit": cd, "closing_credit": ck,
			"message_uz": mu, "message_ru": mr,
		})
		if sev == "critical" {
			crit++
		} else {
			warn++
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
			add(id, code, nu, nr, "TRANSIT_NOT_CLOSED", "critical", cd, ck)
		case normal == "debit" && closing < -anomalyEps:
			add(id, code, nu, nr, "NEG_ACTIVE_BALANCE", "critical", cd, ck)
		case normal == "credit" && closing > anomalyEps:
			add(id, code, nu, nr, "DEBIT_PASSIVE_BALANCE", "warning", cd, ck)
		}
		if strings.HasPrefix(code, "0820") && (closing > anomalyEps || closing < -anomalyEps) {
			add(id, code, nu, nr, "CAPEX_STUCK", "warning", cd, ck)
		}
	}

	diff := totDt - totKt
	response.Success(c, gin.H{
		"period_from":    periodFrom,
		"period_to":      periodTo,
		"balance_broken": diff > anomalyEps || diff < -anomalyEps,
		"totals":         gin.H{"turnover_debit": totDt, "turnover_credit": totKt, "diff": diff},
		"counts":         gin.H{"critical": crit, "warning": warn, "total": crit + warn},
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
	}
	return rule, rule
}
