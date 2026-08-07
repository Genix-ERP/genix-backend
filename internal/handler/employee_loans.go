package handler

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Loan Data Types ---

type LoanResponse struct {
	ID              uuid.UUID         `json:"id"`
	TenantID        uuid.UUID         `json:"tenant_id"`
	OrganizationID  *uuid.UUID        `json:"organization_id"`
	EmployeeID      uuid.UUID         `json:"employee_id"`
	EmployeeName    string            `json:"employee_name"`
	LoanNumber      string            `json:"loan_number"`
	Amount          float64           `json:"amount"`
	MonthlyPayment  float64           `json:"monthly_payment"`
	DurationMonths  int               `json:"duration_months"`
	PaidAmount      float64           `json:"paid_amount"`
	RemainingAmount float64           `json:"remaining_amount"`
	StartDate       string            `json:"start_date"`
	EndDate         string            `json:"end_date"`
	Reason          string            `json:"reason"`
	Status          string            `json:"status"`
	CreatedAt       time.Time         `json:"created_at"`
	Payments        []PaymentResponse `json:"payments,omitempty"`
}

type PaymentResponse struct {
	ID             uuid.UUID  `json:"id"`
	MonthLabel     string     `json:"month_label"`
	DueDate        string     `json:"due_date"`
	Amount         float64    `json:"amount"`
	RemainingAfter float64    `json:"remaining_after"`
	Status         string     `json:"status"`
	PaidAt         *time.Time `json:"paid_at"`
}

type CreateLoanInput struct {
	EmployeeID     string  `json:"employee_id" binding:"required"`
	Amount         float64 `json:"amount" binding:"required"`
	DurationMonths int     `json:"duration_months" binding:"required"`
	StartDate      string  `json:"start_date" binding:"required"`
	Reason         string  `json:"reason"`
	CashAccountID  string  `json:"cash_account_id"`
}

// --- List Loans ---

func (h *Handler) ListEmployeeLoans(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	employeeFilter := c.Query("employee_id")
	statusFilter := c.Query("status")

	baseQuery := `
		SELECT l.id, l.tenant_id, l.organization_id, l.employee_id,
			COALESCE(e.first_name || ' ' || e.last_name, '') as employee_name,
			COALESCE(l.loan_number, ''), l.amount, l.monthly_payment, l.duration_months,
			l.paid_amount, l.remaining_amount, l.start_date, l.end_date,
			COALESCE(l.reason, ''), l.status, l.created_at
		FROM employee_loans l
		LEFT JOIN employees e ON l.employee_id = e.id
		WHERE l.tenant_id = $1 AND l.deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM employee_loans l WHERE l.tenant_id = $1 AND l.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if employeeFilter != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.employee_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.employee_id = $%d", argCount)
		args = append(args, employeeFilter)
	}

	if statusFilter != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND l.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND l.status = $%d", argCount)
		args = append(args, statusFilter)
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)

	baseQuery += " ORDER BY l.created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list loans", "error", err)
		response.InternalError(c, "Failed to list loans")
		return
	}
	defer rows.Close()

	loans := []LoanResponse{}
	for rows.Next() {
		var l LoanResponse
		var orgID sql.NullString
		var startDate, endDate time.Time
		err := rows.Scan(&l.ID, &l.TenantID, &orgID, &l.EmployeeID, &l.EmployeeName,
			&l.LoanNumber, &l.Amount, &l.MonthlyPayment, &l.DurationMonths,
			&l.PaidAmount, &l.RemainingAmount, &startDate, &endDate,
			&l.Reason, &l.Status, &l.CreatedAt)
		if err != nil {
			h.log.Error("Failed to scan loan", "error", err)
			continue
		}
		if orgID.Valid {
			oid, _ := uuid.Parse(orgID.String)
			l.OrganizationID = &oid
		}
		l.StartDate = startDate.Format("2006-01-02")
		l.EndDate = endDate.Format("2006-01-02")
		loans = append(loans, l)
	}

	response.Paginated(c, loans, page, limit, total)
}

// --- Get Single Loan with Payments ---

func (h *Handler) GetEmployeeLoan(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	loanID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid loan ID")
		return
	}

	var l LoanResponse
	var orgID sql.NullString
	var startDate, endDate time.Time
	err = h.db.QueryRow(`
		SELECT l.id, l.tenant_id, l.organization_id, l.employee_id,
			COALESCE(e.first_name || ' ' || e.last_name, '') as employee_name,
			COALESCE(l.loan_number, ''), l.amount, l.monthly_payment, l.duration_months,
			l.paid_amount, l.remaining_amount, l.start_date, l.end_date,
			COALESCE(l.reason, ''), l.status, l.created_at
		FROM employee_loans l
		LEFT JOIN employees e ON l.employee_id = e.id
		WHERE l.id = $1 AND l.tenant_id = $2 AND l.deleted_at IS NULL
	`, loanID, tenantID).Scan(&l.ID, &l.TenantID, &orgID, &l.EmployeeID, &l.EmployeeName,
		&l.LoanNumber, &l.Amount, &l.MonthlyPayment, &l.DurationMonths,
		&l.PaidAmount, &l.RemainingAmount, &startDate, &endDate,
		&l.Reason, &l.Status, &l.CreatedAt)
	if err != nil {
		response.NotFound(c, "Loan not found")
		return
	}
	if orgID.Valid {
		oid, _ := uuid.Parse(orgID.String)
		l.OrganizationID = &oid
	}
	l.StartDate = startDate.Format("2006-01-02")
	l.EndDate = endDate.Format("2006-01-02")

	// Load payments
	payRows, err := h.db.Query(`
		SELECT id, month_label, due_date, amount, remaining_after, status, paid_at
		FROM employee_loan_payments
		WHERE loan_id = $1 AND tenant_id = $2
		ORDER BY due_date ASC
	`, loanID, tenantID)
	if err == nil {
		defer payRows.Close()
		for payRows.Next() {
			var p PaymentResponse
			var dueDate time.Time
			var paidAt sql.NullTime
			payRows.Scan(&p.ID, &p.MonthLabel, &dueDate, &p.Amount, &p.RemainingAfter, &p.Status, &paidAt)
			p.DueDate = dueDate.Format("2006-01-02")
			if paidAt.Valid {
				p.PaidAt = &paidAt.Time
			}
			l.Payments = append(l.Payments, p)
		}
	}

	response.Success(c, l)
}

// --- Create Loan ---

func (h *Handler) CreateEmployeeLoan(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	var input CreateLoanInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Check employee exists and get their name + salary
	var empName string
	var baseSalary float64
	err = h.db.QueryRow(`
		SELECT COALESCE(first_name || ' ' || last_name, ''), COALESCE(base_salary, 0)
		FROM employees WHERE id = $1 AND tenant_id = $2
	`, employeeID, tenantID).Scan(&empName, &baseSalary)
	if err != nil {
		response.NotFound(c, "Employee not found")
		return
	}

	// Check no active loan
	var activeLoanCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM employee_loans WHERE employee_id = $1 AND tenant_id = $2 AND status = 'active' AND deleted_at IS NULL`,
		employeeID, tenantID).Scan(&activeLoanCount)
	if activeLoanCount > 0 {
		response.BadRequest(c, "Employee already has an active loan")
		return
	}

	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start date format (use YYYY-MM-DD)")
		return
	}

	monthlyPayment := math.Ceil(input.Amount / float64(input.DurationMonths))
	endDate := startDate.AddDate(0, input.DurationMonths, 0)

	// Generate loan number
	var loanCount int
	h.db.QueryRow("SELECT COUNT(*) FROM employee_loans WHERE tenant_id = $1", tenantID).Scan(&loanCount)
	loanNumber := fmt.Sprintf("LOAN-%05d", loanCount+1)

	var cashAccountID *uuid.UUID
	if input.CashAccountID != "" {
		cid, err := uuid.Parse(input.CashAccountID)
		if err == nil {
			cashAccountID = &cid
		}
	}

	loanID := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO employee_loans (id, tenant_id, organization_id, employee_id, loan_number,
			amount, monthly_payment, duration_months, paid_amount, remaining_amount,
			start_date, end_date, reason, cash_account_id, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $6, $9, $10, $11, $12, 'active', $13)
	`, loanID, tenantID, orgID, employeeID, loanNumber,
		input.Amount, monthlyPayment, input.DurationMonths,
		startDate, endDate, input.Reason, cashAccountID, userID)
	if err != nil {
		h.log.Error("Failed to create loan", "error", err)
		response.InternalError(c, "Failed to create loan")
		return
	}

	// Generate payment schedule
	remaining := input.Amount
	for i := 0; i < input.DurationMonths; i++ {
		payDate := startDate.AddDate(0, i, 0)
		payment := monthlyPayment
		if remaining < payment {
			payment = remaining
		}
		remaining -= payment

		// Uzbek month names
		monthNames := []string{"", "Yanvar", "Fevral", "Mart", "Aprel", "May", "Iyun", "Iyul", "Avgust", "Sentabr", "Oktabr", "Noyabr", "Dekabr"}
		monthLabel := fmt.Sprintf("%s %d", monthNames[payDate.Month()], payDate.Year())

		_, err = h.db.Exec(`
			INSERT INTO employee_loan_payments (id, tenant_id, loan_id, employee_id,
				month_label, due_date, amount, remaining_after, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending')
		`, uuid.New(), tenantID, loanID, employeeID,
			monthLabel, payDate, payment, remaining)
		if err != nil {
			h.log.Error("Failed to create loan payment", "error", err)
		}
	}

	// Create journal entry for loan disbursement (Dt 4720 Employee Loans / Kt Cash Account)
	if cashAccountID != nil {
		h.createLoanJournalEntry(tenantID, orgID, *cashAccountID, input.Amount, employeeID, empName, loanNumber, userID)
	}

	// SMS: Qarz berilganda
	go func() {
		var phone string
		h.db.QueryRow(`SELECT COALESCE(phone, '') FROM employees WHERE id = $1 AND tenant_id = $2`, employeeID, tenantID).Scan(&phone)
		if phone != "" {
			msg := fmt.Sprintf("Sizga %s so'm qarz berildi. Oylik to'lov: %s so'm. Muddat: %d oy.",
				formatAmount(input.Amount), formatAmount(monthlyPayment), input.DurationMonths)
			if err := h.smsService.Send(phone, msg); err != nil {
				h.log.Error("Failed to send loan SMS", "error", err, "phone", phone)
			}
		}
	}()

	response.Created(c, map[string]interface{}{
		"id":          loanID,
		"loan_number": loanNumber,
		"message":     "Loan created successfully",
	})
}

// --- Employee Self-Service: My Payroll History ---

func (h *Handler) GetMyPayrollHistory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Get employee_id from the authenticated user
	employeeID := h.getEmployeeIDFromUser(c, tenantID)
	if employeeID == uuid.Nil {
		response.Unauthorized(c, "No employee linked to this user")
		return
	}

	// Was a hard LIMIT 24 with no offset: an employee with more than two years
	// of service could not reach their 25th payslip through any client.
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

	var total int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM payroll_entries pe
		WHERE pe.employee_id = $1 AND pe.tenant_id = $2`, employeeID, tenantID).Scan(&total); err != nil {
		h.log.Error("Failed to count payroll history", "error", err)
		response.InternalError(c, "Failed to get payroll history")
		return
	}

	// loan_deduction is a scalar subquery, not a LEFT JOIN: the join fanned out
	// one row per paid loan repayment, so an entry with two repayments appeared
	// twice AND reported only one repayment's amount.
	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT pe.id, pp.period_name, pe.base_salary, pe.overtime_amount, pe.bonus, pe.allowances,
			pe.gross_salary, pe.income_tax, pe.social_security, pe.pension, pe.other_deductions,
			pe.total_deductions, pe.net_salary, pe.status, pe.payment_method,
			pp.pay_date, pe.created_at,
			COALESCE((SELECT SUM(elp.amount) FROM employee_loan_payments elp
			          WHERE elp.payroll_entry_id = pe.id AND elp.status = 'paid'), 0) as loan_deduction
		FROM payroll_entries pe
		JOIN payroll_periods pp ON pe.payroll_period_id = pp.id
		WHERE pe.employee_id = $1 AND pe.tenant_id = $2
		ORDER BY pp.pay_date DESC, pe.id DESC
		LIMIT %d OFFSET %d
	`, pageSize, (page-1)*pageSize), employeeID, tenantID)
	if err != nil {
		h.log.Error("Failed to get payroll history", "error", err)
		response.InternalError(c, "Failed to get payroll history")
		return
	}
	defer rows.Close()

	type PayrollHistoryItem struct {
		ID              uuid.UUID `json:"id"`
		PeriodName      string    `json:"period_name"`
		BaseSalary      float64   `json:"base_salary"`
		OvertimeAmount  float64   `json:"overtime_amount"`
		Bonus           float64   `json:"bonus"`
		Allowances      float64   `json:"allowances"`
		GrossSalary     float64   `json:"gross_salary"`
		IncomeTax       float64   `json:"income_tax"`
		SocialSecurity  float64   `json:"social_security"`
		Pension         float64   `json:"pension"`
		OtherDeductions float64   `json:"other_deductions"`
		LoanDeduction   float64   `json:"loan_deduction"`
		TotalDeductions float64   `json:"total_deductions"`
		NetSalary       float64   `json:"net_salary"`
		Status          string    `json:"status"`
		PaymentMethod   string    `json:"payment_method"`
		PayDate         string    `json:"pay_date"`
		CreatedAt       time.Time `json:"created_at"`
	}

	items := []PayrollHistoryItem{}
	for rows.Next() {
		var item PayrollHistoryItem
		var payDate sql.NullTime
		err := rows.Scan(&item.ID, &item.PeriodName, &item.BaseSalary, &item.OvertimeAmount,
			&item.Bonus, &item.Allowances, &item.GrossSalary, &item.IncomeTax,
			&item.SocialSecurity, &item.Pension, &item.OtherDeductions,
			&item.TotalDeductions, &item.NetSalary, &item.Status, &item.PaymentMethod,
			&payDate, &item.CreatedAt, &item.LoanDeduction)
		if err != nil {
			continue
		}
		if payDate.Valid {
			item.PayDate = payDate.Time.Format("02.01.2006")
		}
		items = append(items, item)
	}

	response.Paginated(c, items, page, pageSize, total)
}

// --- Employee Self-Service: My Loan ---

func (h *Handler) GetMyLoan(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeID := h.getEmployeeIDFromUser(c, tenantID)
	if employeeID == uuid.Nil {
		response.Unauthorized(c, "No employee linked to this user")
		return
	}

	// Get active loan
	var l LoanResponse
	var orgID sql.NullString
	var startDate, endDate time.Time
	err := h.db.QueryRow(`
		SELECT l.id, l.tenant_id, l.organization_id, l.employee_id,
			COALESCE(e.first_name || ' ' || e.last_name, '') as employee_name,
			COALESCE(l.loan_number, ''), l.amount, l.monthly_payment, l.duration_months,
			l.paid_amount, l.remaining_amount, l.start_date, l.end_date,
			COALESCE(l.reason, ''), l.status, l.created_at
		FROM employee_loans l
		LEFT JOIN employees e ON l.employee_id = e.id
		WHERE l.employee_id = $1 AND l.tenant_id = $2 AND l.status = 'active' AND l.deleted_at IS NULL
		ORDER BY l.created_at DESC LIMIT 1
	`, employeeID, tenantID).Scan(&l.ID, &l.TenantID, &orgID, &l.EmployeeID, &l.EmployeeName,
		&l.LoanNumber, &l.Amount, &l.MonthlyPayment, &l.DurationMonths,
		&l.PaidAmount, &l.RemainingAmount, &startDate, &endDate,
		&l.Reason, &l.Status, &l.CreatedAt)
	if err != nil {
		// No active loan — return empty
		response.Success(c, nil)
		return
	}
	if orgID.Valid {
		oid, _ := uuid.Parse(orgID.String)
		l.OrganizationID = &oid
	}
	l.StartDate = startDate.Format("2006-01-02")
	l.EndDate = endDate.Format("2006-01-02")

	// Load payments
	payRows, _ := h.db.Query(`
		SELECT id, month_label, due_date, amount, remaining_after, status, paid_at
		FROM employee_loan_payments
		WHERE loan_id = $1 AND tenant_id = $2
		ORDER BY due_date ASC
	`, l.ID, tenantID)
	if payRows != nil {
		defer payRows.Close()
		for payRows.Next() {
			var p PaymentResponse
			var dueDate time.Time
			var paidAt sql.NullTime
			payRows.Scan(&p.ID, &p.MonthLabel, &dueDate, &p.Amount, &p.RemainingAfter, &p.Status, &paidAt)
			p.DueDate = dueDate.Format("2006-01-02")
			if paidAt.Valid {
				p.PaidAt = &paidAt.Time
			}
			l.Payments = append(l.Payments, p)
		}
	}

	response.Success(c, l)
}

// --- Employee Self-Service: My Profile ---

func (h *Handler) GetMyEmployeeProfile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	employeeID := h.getEmployeeIDFromUser(c, tenantID)
	if employeeID == uuid.Nil {
		response.Unauthorized(c, "No employee linked to this user")
		return
	}

	var profile struct {
		ID           uuid.UUID `json:"id"`
		EmployeeCode string    `json:"employee_code"`
		FirstName    string    `json:"first_name"`
		LastName     string    `json:"last_name"`
		Position     string    `json:"position"`
		Department   string    `json:"department"`
		BaseSalary   float64   `json:"base_salary"`
		Email        string    `json:"email"`
		Phone        string    `json:"phone"`
		HireDate     string    `json:"hire_date"`
	}

	// employees has employee_number/job_title — the old employee_code/position
	// columns never existed, so this query always errored and the cabinet
	// profile 404'd for every user.
	var hireDate sql.NullTime
	err := h.db.QueryRow(`
		SELECT e.id, COALESCE(e.employee_number, ''), COALESCE(e.first_name, ''), COALESCE(e.last_name, ''),
			COALESCE(e.job_title, ''), COALESCE(d.name, ''), COALESCE(e.base_salary, 0),
			COALESCE(e.email, ''), COALESCE(e.phone, ''), e.hire_date
		FROM employees e
		LEFT JOIN departments d ON e.department_id = d.id
		WHERE e.id = $1 AND e.tenant_id = $2
	`, employeeID, tenantID).Scan(&profile.ID, &profile.EmployeeCode, &profile.FirstName,
		&profile.LastName, &profile.Position, &profile.Department,
		&profile.BaseSalary, &profile.Email, &profile.Phone, &hireDate)
	if err != nil {
		response.NotFound(c, "Employee profile not found")
		return
	}
	if hireDate.Valid {
		profile.HireDate = hireDate.Time.Format("2006-01-02")
	}

	response.Success(c, profile)
}

// --- Mark Loan Payment as Paid ---

func (h *Handler) MarkLoanPaymentPaid(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	loanID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid loan ID")
		return
	}
	paymentID, err := uuid.Parse(c.Param("paymentId"))
	if err != nil {
		response.BadRequest(c, "Invalid payment ID")
		return
	}

	// Optional link to the payroll entry the deduction was withheld from
	// (281's payroll_entry_id column; feeds /my/payroll-history's
	// loan_deduction join and the confirm-SMS text).
	var body struct {
		PayrollEntryID *string `json:"payroll_entry_id"`
	}
	_ = c.ShouldBindJSON(&body)
	var payrollEntryID *uuid.UUID
	if body.PayrollEntryID != nil {
		if parsed, perr := uuid.Parse(*body.PayrollEntryID); perr == nil {
			var cnt int
			_ = h.db.QueryRow(`SELECT COUNT(*) FROM payroll_entries WHERE id = $1 AND tenant_id = $2`, parsed, tenantID).Scan(&cnt)
			if cnt > 0 {
				payrollEntryID = &parsed
			}
		}
	}

	// Get payment amount
	var payAmount float64
	err = h.db.QueryRow(`SELECT amount FROM employee_loan_payments WHERE id = $1 AND loan_id = $2 AND tenant_id = $3 AND status = 'pending'`,
		paymentID, loanID, tenantID).Scan(&payAmount)
	if err != nil {
		response.NotFound(c, "Payment not found or already paid")
		return
	}

	// Payment flag + loan totals must move together (a crash between the two
	// autocommit statements used to desync paid_amount from the schedule).
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("MarkLoanPaymentPaid: begin tx", "error", txErr)
		response.InternalError(c, "Failed to mark payment paid")
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE employee_loan_payments SET status = 'paid', paid_at = NOW(), payroll_entry_id = COALESCE($3, payroll_entry_id), updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, paymentID, tenantID, payrollEntryID); err != nil {
		h.log.Error("MarkLoanPaymentPaid: payment update", "error", err)
		response.InternalError(c, "Failed to mark payment paid")
		return
	}

	if _, err := tx.Exec(`UPDATE employee_loans SET paid_amount = paid_amount + $1, remaining_amount = remaining_amount - $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3`, payAmount, loanID, tenantID); err != nil {
		h.log.Error("MarkLoanPaymentPaid: loan totals update", "error", err)
		response.InternalError(c, "Failed to mark payment paid")
		return
	}

	// Repayment relieves the loans-receivable account posted at disbursement
	// (Dt cash / Kt 4720). Only posts if the disbursement JE exists — loans
	// granted without a cash account never hit the GL, so their repayments
	// must not either.
	if err := h.postLoanRepaymentJE(tx, tenantID, loanID, paymentID, payAmount); err != nil {
		h.log.Error("MarkLoanPaymentPaid: repayment JE", "error", err)
		response.InternalError(c, "Failed to post repayment journal entry")
		return
	}

	// Check if loan is fully paid
	var remaining float64
	if err := tx.QueryRow(`SELECT remaining_amount FROM employee_loans WHERE id = $1 AND tenant_id = $2`, loanID, tenantID).Scan(&remaining); err != nil {
		h.log.Error("MarkLoanPaymentPaid: remaining lookup", "error", err)
		response.InternalError(c, "Failed to mark payment paid")
		return
	}
	if remaining <= 0 {
		if _, err := tx.Exec(`UPDATE employee_loans SET status = 'completed', remaining_amount = 0, updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, loanID, tenantID); err != nil {
			h.log.Error("MarkLoanPaymentPaid: completion update", "error", err)
			response.InternalError(c, "Failed to mark payment paid")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("MarkLoanPaymentPaid: commit", "error", err)
		response.InternalError(c, "Failed to mark payment paid")
		return
	}

	if remaining <= 0 {

		// SMS: Qarz yopilganda
		go func() {
			var phone string
			h.db.QueryRow(`SELECT COALESCE(e.phone, '')
				FROM employee_loans l JOIN employees e ON l.employee_id = e.id
				WHERE l.id = $1`, loanID).Scan(&phone)
			if phone != "" {
				msg := "Qarzingiz to'liq yopildi! Tabriklaymiz."
				if err := h.smsService.Send(phone, msg); err != nil {
					h.log.Error("Failed to send loan completion SMS", "error", err)
				}
			}
		}()
	}

	response.Success(c, map[string]string{"message": "Payment marked as paid"})
}

// --- Helper: get employee ID from authenticated user ---

func (h *Handler) getEmployeeIDFromUser(c *gin.Context, tenantID uuid.UUID) uuid.UUID {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		return uuid.Nil
	}

	// Explicit link first (users.employee_id, migration 031) — the email
	// fallback below is non-unique across employees, so it must come last
	// and be deterministic.
	var employeeID uuid.UUID
	err := h.db.QueryRow(`
		SELECT e.id FROM employees e
		JOIN users u ON u.employee_id = e.id
		WHERE u.id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL
		LIMIT 1
	`, userID, tenantID).Scan(&employeeID)
	if err != nil {
		h.db.QueryRow(`SELECT id FROM employees WHERE user_id = $1 AND tenant_id = $2 AND deleted_at IS NULL ORDER BY created_at LIMIT 1`,
			userID, tenantID).Scan(&employeeID)
	}
	if employeeID == uuid.Nil {
		h.db.QueryRow(`
			SELECT e.id FROM employees e
			JOIN users u ON u.email = e.email
			WHERE u.id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL
			ORDER BY e.created_at LIMIT 1
		`, userID, tenantID).Scan(&employeeID)
	}
	return employeeID
}

// postLoanRepaymentJE posts Dt cash / Kt 4720 for a loan repayment inside the
// caller's transaction, mirroring the disbursement entry. Skips (nil) when the
// loan never hit the GL — no disbursement JE, no cash account, or no 4720.
func (h *Handler) postLoanRepaymentJE(tx *sql.Tx, tenantID, loanID, paymentID uuid.UUID, amount float64) error {
	var loanNumber, empName string
	var orgIDStr, cashAcctStr sql.NullString
	err := tx.QueryRow(`
		SELECT COALESCE(l.loan_number, ''), COALESCE(e.first_name,'') || ' ' || COALESCE(e.last_name,''),
		       l.organization_id, l.cash_account_id
		FROM employee_loans l JOIN employees e ON e.id = l.employee_id
		WHERE l.id = $1 AND l.tenant_id = $2
	`, loanID, tenantID).Scan(&loanNumber, &empName, &orgIDStr, &cashAcctStr)
	if err != nil || !cashAcctStr.Valid {
		return nil
	}
	cashAccountID, err := uuid.Parse(cashAcctStr.String)
	if err != nil {
		return nil
	}

	// Only relieve 4720 if the disbursement actually posted.
	var disbursed int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM journal_entries WHERE tenant_id = $1 AND entry_number = $2 AND status = 'posted' AND deleted_at IS NULL`,
		tenantID, "JE-LOAN-"+loanNumber).Scan(&disbursed)
	if disbursed == 0 {
		return nil
	}

	var loanAccountID uuid.UUID
	_ = tx.QueryRow(`SELECT id FROM accounts WHERE tenant_id = $1 AND code = '4720' AND deleted_at IS NULL ORDER BY organization_id NULLS LAST LIMIT 1`, tenantID).Scan(&loanAccountID)
	if loanAccountID == uuid.Nil {
		return nil
	}

	var journalID uuid.UUID
	_ = tx.QueryRow(`SELECT id FROM journals WHERE tenant_id = $1 AND code = 'MISC' AND deleted_at IS NULL LIMIT 1`, tenantID).Scan(&journalID)
	if journalID == uuid.Nil {
		return nil
	}

	var orgVal interface{}
	var orgIDPtr *uuid.UUID
	if orgIDStr.Valid {
		if parsed, perr := uuid.Parse(orgIDStr.String); perr == nil {
			orgVal = parsed
			orgIDPtr = &parsed
		}
	}

	now := time.Now()
	jeID := uuid.New()
	entryNumber := fmt.Sprintf("LOANPMT%05d", nextEntryNumberSeq(tx, tenantID, orgVal, "LOANPMT", 1))

	if _, err := tx.Exec(`
		INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number,
			entry_date, description, source_type, source_id, status, total_debit, total_credit, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'employee_loan_repayment', $8, 'posted', $9, $9, $10, $10)
	`, jeID, tenantID, orgIDPtr, journalID, entryNumber, now,
		fmt.Sprintf("Qarz qaytarildi: %s - %s", empName, loanNumber), paymentID.String(), amount, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, line_number, description, debit_amount, credit_amount, created_at)
		VALUES ($1, $2, $3, 1, $4, $5, 0, $6)
	`, uuid.New(), jeID, cashAccountID, fmt.Sprintf("Qarz qaytarildi: %s", empName), amount, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, line_number, description, debit_amount, credit_amount, created_at)
		VALUES ($1, $2, $3, 2, $4, 0, $5, $6)
	`, uuid.New(), jeID, loanAccountID, fmt.Sprintf("Qarz qoldig'i kamaydi: %s", empName), amount, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, amount, now, cashAccountID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, amount, now, loanAccountID); err != nil {
		return err
	}
	return nil
}

// --- Helper: create journal entry for loan ---

func (h *Handler) createLoanJournalEntry(tenantID uuid.UUID, orgID uuid.UUID, cashAccountID uuid.UUID, amount float64, employeeID uuid.UUID, empName string, loanNumber string, createdBy uuid.UUID) {
	// Find loan receivable account (4720) or any receivable
	var loanAccountID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM accounts WHERE tenant_id = $1 AND organization_id = $2 AND code = '4720' AND deleted_at IS NULL
	`, tenantID, orgID).Scan(&loanAccountID)
	if err != nil {
		// Try without org
		h.db.QueryRow(`SELECT id FROM accounts WHERE tenant_id = $1 AND code = '4720' AND deleted_at IS NULL`, tenantID).Scan(&loanAccountID)
	}
	if loanAccountID == uuid.Nil {
		return // No account found, skip journal entry
	}

	// Find a journal for misc entries
	var journalID uuid.UUID
	h.db.QueryRow(`SELECT id FROM journals WHERE tenant_id = $1 AND organization_id = $2 AND code = 'MISC' AND deleted_at IS NULL`, tenantID, orgID).Scan(&journalID)
	if journalID == uuid.Nil {
		h.db.QueryRow(`SELECT id FROM journals WHERE tenant_id = $1 AND code = 'MISC' AND deleted_at IS NULL`, tenantID).Scan(&journalID)
	}
	if journalID == uuid.Nil {
		return
	}

	now := time.Now()
	entryNumber := fmt.Sprintf("JE-LOAN-%s", loanNumber)
	jeID := uuid.New()

	// Header + both lines + balance updates must be in ONE transaction:
	// migration 416's deferred balance trigger rejects any single-line
	// autocommit insert as imbalanced (leaving 0-line "posted" headers).
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin loan journal tx", "error", err)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number,
			entry_date, description, source_type, status, total_debit, total_credit, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'employee_loan', 'posted', $8, $8, $9, $10, $10)
	`, jeID, tenantID, orgID, journalID, entryNumber, now, fmt.Sprintf("Xodimga qarz: %s - %s", empName, loanNumber), amount, createdBy, now); err != nil {
		h.log.Error("Failed to insert loan journal entry", "error", err)
		return
	}

	// Debit: 4720 (Employee Loans Receivable)
	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, line_number, description, debit_amount, credit_amount, created_at)
		VALUES ($1, $2, $3, 1, $4, $5, 0, $6)
	`, uuid.New(), jeID, loanAccountID, fmt.Sprintf("Qarz: %s", empName), amount, now); err != nil {
		h.log.Error("Failed to insert loan debit line", "error", err)
		return
	}

	// Credit: Cash/Bank account
	if _, err := tx.Exec(`
		INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, line_number, description, debit_amount, credit_amount, created_at)
		VALUES ($1, $2, $3, 2, $4, 0, $5, $6)
	`, uuid.New(), jeID, cashAccountID, fmt.Sprintf("Qarz to'lovi: %s", empName), amount, now); err != nil {
		h.log.Error("Failed to insert loan credit line", "error", err)
		return
	}

	// Update account balances (inside the same tx so they roll back on failure)
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, amount, now, loanAccountID); err != nil {
		h.log.Error("Failed to update loan receivable balance", "error", err)
		return
	}
	if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, amount, now, cashAccountID); err != nil {
		h.log.Error("Failed to update cash balance for loan", "error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit loan journal entry", "error", err)
	}
}
