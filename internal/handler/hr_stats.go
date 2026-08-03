package handler

import (
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetEmployeeStats returns all HR dashboard numbers in one call
// (finance-dashboard pattern): KPI totals, monthly headcount dynamics,
// department distribution, tenure buckets and upcoming-date widgets.
// Everything is computed in SQL — the frontend must never derive KPIs
// from a paginated page again.
func (h *Handler) GetEmployeeStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Same optional org scoping as ListEmployees
	orgFilter := ""
	args := []interface{}{tenantID}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgFilter = " AND (e.organization_id = $2 OR e.id IN (SELECT employee_id FROM employee_organizations WHERE organization_id = $2))"
		args = append(args, orgID)
	}

	base := "FROM employees e WHERE e.tenant_id = $1 AND e.deleted_at IS NULL" + orgFilter

	// ---- KPI totals -------------------------------------------------------
	var total, active, onLeave, terminated, hiredThisMonth, hiredPrevMonth, exitsThisMonth int
	var salaryFund, avgSalary float64
	err := h.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE e.status = 'active'),
		       COUNT(*) FILTER (WHERE e.status = 'on_leave'),
		       COUNT(*) FILTER (WHERE e.status = 'terminated'),
		       COUNT(*) FILTER (WHERE date_trunc('month', e.hire_date) = date_trunc('month', CURRENT_DATE)),
		       COUNT(*) FILTER (WHERE date_trunc('month', e.hire_date) = date_trunc('month', CURRENT_DATE - INTERVAL '1 month')),
		       COUNT(*) FILTER (WHERE e.termination_date IS NOT NULL AND date_trunc('month', e.termination_date) = date_trunc('month', CURRENT_DATE)),
		       COALESCE(SUM(e.base_salary) FILTER (WHERE e.status = 'active'), 0),
		       COALESCE(AVG(e.base_salary) FILTER (WHERE e.status = 'active' AND e.base_salary > 0), 0)
		`+base, args...).Scan(&total, &active, &onLeave, &terminated, &hiredThisMonth, &hiredPrevMonth, &exitsThisMonth, &salaryFund, &avgSalary)
	if err != nil {
		h.log.Error("Failed to compute employee stats", "error", err)
		response.InternalError(c, "Failed to compute employee stats")
		return
	}

	// ---- 12-month headcount dynamics (hires / exits / month-end headcount) -
	type monthPoint struct {
		Month     string `json:"month"`
		Hires     int    `json:"hires"`
		Exits     int    `json:"exits"`
		Headcount int    `json:"headcount"`
	}
	months := make([]monthPoint, 0, 12)
	mrows, err := h.db.Query(`
		WITH m AS (
			SELECT date_trunc('month', CURRENT_DATE) - (INTERVAL '1 month' * gs) AS mstart
			FROM generate_series(11, 0, -1) AS gs
		)
		SELECT to_char(m.mstart, 'YYYY-MM'),
		       (SELECT COUNT(*) `+base+` AND date_trunc('month', e.hire_date) = m.mstart),
		       (SELECT COUNT(*) `+base+` AND e.termination_date IS NOT NULL AND date_trunc('month', e.termination_date) = m.mstart),
		       (SELECT COUNT(*) `+base+` AND e.hire_date < m.mstart + INTERVAL '1 month'
		            AND (e.termination_date IS NULL OR e.termination_date >= m.mstart + INTERVAL '1 month'))
		FROM m ORDER BY m.mstart`, args...)
	if err == nil {
		defer mrows.Close()
		for mrows.Next() {
			var p monthPoint
			if mrows.Scan(&p.Month, &p.Hires, &p.Exits, &p.Headcount) == nil {
				months = append(months, p)
			}
		}
	} else {
		h.log.Error("Failed to compute headcount dynamics", "error", err)
	}

	// ---- Department distribution -----------------------------------------
	type deptCount struct {
		ID    *string `json:"id"`
		Name  string  `json:"name"`
		Count int     `json:"count"`
	}
	departments := make([]deptCount, 0)
	drows, err := h.db.Query(fmt.Sprintf(`
		SELECT d.id::text, COALESCE(d.name, ''), COUNT(*)
		FROM employees e
		LEFT JOIN departments d ON d.id = e.department_id AND d.deleted_at IS NULL
		WHERE e.tenant_id = $1 AND e.deleted_at IS NULL%s
		GROUP BY d.id, d.name ORDER BY COUNT(*) DESC`, orgFilter), args...)
	if err == nil {
		defer drows.Close()
		for drows.Next() {
			var dc deptCount
			var idStr *string
			if drows.Scan(&idStr, &dc.Name, &dc.Count) == nil {
				dc.ID = idStr
				departments = append(departments, dc)
			}
		}
	} else {
		h.log.Error("Failed to compute department distribution", "error", err)
	}

	// ---- Tenure buckets ---------------------------------------------------
	type bucket struct {
		Bucket string `json:"bucket"`
		Count  int    `json:"count"`
	}
	tenure := make([]bucket, 0, 4)
	trows, err := h.db.Query(`
		SELECT b, COUNT(*) FROM (
			SELECT CASE
				WHEN e.hire_date > CURRENT_DATE - INTERVAL '1 year' THEN '0-1'
				WHEN e.hire_date > CURRENT_DATE - INTERVAL '3 year' THEN '1-3'
				WHEN e.hire_date > CURRENT_DATE - INTERVAL '5 year' THEN '3-5'
				ELSE '5+' END AS b
			`+base+` AND e.status = 'active'
		) t GROUP BY b
		ORDER BY CASE b WHEN '0-1' THEN 1 WHEN '1-3' THEN 2 WHEN '3-5' THEN 3 ELSE 4 END`, args...)
	if err == nil {
		defer trows.Close()
		for trows.Next() {
			var bk bucket
			if trows.Scan(&bk.Bucket, &bk.Count) == nil {
				tenure = append(tenure, bk)
			}
		}
	}

	// ---- Widgets: probation ending / upcoming birthdays (30 days) ---------
	type personDate struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Date string `json:"date"`
	}
	probation := make([]personDate, 0)
	prows, err := h.db.Query(`
		SELECT e.id::text, TRIM(COALESCE(e.first_name,'')||' '||COALESCE(e.last_name,'')), to_char(e.probation_end_date, 'YYYY-MM-DD')
		`+base+` AND e.status = 'active' AND e.probation_end_date IS NOT NULL
		AND e.probation_end_date BETWEEN CURRENT_DATE AND CURRENT_DATE + INTERVAL '30 days'
		ORDER BY e.probation_end_date ASC LIMIT 10`, args...)
	if err == nil {
		defer prows.Close()
		for prows.Next() {
			var pd personDate
			if prows.Scan(&pd.ID, &pd.Name, &pd.Date) == nil {
				probation = append(probation, pd)
			}
		}
	}

	birthdays := make([]personDate, 0)
	brows, err := h.db.Query(`
		SELECT e.id::text, TRIM(COALESCE(e.first_name,'')||' '||COALESCE(e.last_name,'')), to_char(e.date_of_birth, 'MM-DD')
		`+base+` AND e.status = 'active' AND e.date_of_birth IS NOT NULL
		AND (
			(to_char(e.date_of_birth,'MM-DD') >= to_char(CURRENT_DATE,'MM-DD') AND to_char(e.date_of_birth,'MM-DD') <= to_char(CURRENT_DATE + INTERVAL '30 days','MM-DD'))
			OR (to_char(CURRENT_DATE + INTERVAL '30 days','MM-DD') < to_char(CURRENT_DATE,'MM-DD') -- year wrap
				AND (to_char(e.date_of_birth,'MM-DD') >= to_char(CURRENT_DATE,'MM-DD') OR to_char(e.date_of_birth,'MM-DD') <= to_char(CURRENT_DATE + INTERVAL '30 days','MM-DD')))
		)
		ORDER BY to_char(e.date_of_birth,'MM-DD') ASC LIMIT 10`, args...)
	if err == nil {
		defer brows.Close()
		for brows.Next() {
			var pd personDate
			if brows.Scan(&pd.ID, &pd.Name, &pd.Date) == nil {
				birthdays = append(birthdays, pd)
			}
		}
	}

	response.Success(c, gin.H{
		"total":              total,
		"active":             active,
		"on_leave":           onLeave,
		"terminated":         terminated,
		"hired_this_month":   hiredThisMonth,
		"hired_prev_month":   hiredPrevMonth,
		"exits_this_month":   exitsThisMonth,
		"salary_fund":        salaryFund,
		"avg_salary":         avgSalary,
		"headcount_by_month": months,
		"departments":        departments,
		"tenure_buckets":     tenure,
		"probation_ending":   probation,
		"upcoming_birthdays": birthdays,
		"as_of":              time.Now().Format("2006-01-02"),
	})
}
