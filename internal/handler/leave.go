package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ======================
// LEAVE REQUEST HANDLERS
// ======================

// ListLeaveRequests returns a paginated list of leave requests
func (h *Handler) ListLeaveRequests(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := (page - 1) * limit

	// Parse filters
	employeeID := c.Query("employee_id")
	status := c.Query("status")
	leaveType := c.Query("leave_type")
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "DESC")

	// Build query
	baseQuery := `
		SELECT id, tenant_id, employee_id, employee_name, leave_type,
			   start_date, end_date, days, reason, status, half_day,
			   half_day_period, approved_by, approved_at, rejection_note,
			   created_at, updated_at
		FROM leave_requests
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM leave_requests WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Apply filters
	if employeeID != "" {
		if empUUID, err := uuid.Parse(employeeID); err == nil {
			argCount++
			filter := fmt.Sprintf(" AND employee_id = $%d", argCount)
			baseQuery += filter
			countQuery += filter
			args = append(args, empUUID)
		}
	}

	if status != "" && status != "all" {
		argCount++
		filter := fmt.Sprintf(" AND status = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, status)
	}

	if leaveType != "" && leaveType != "all" {
		argCount++
		filter := fmt.Sprintf(" AND leave_type = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, leaveType)
	}

	// Add sorting
	validSortFields := map[string]bool{
		"start_date": true, "created_at": true, "status": true,
	}
	if !validSortFields[sortBy] {
		sortBy = "created_at"
	}
	if sortOrder != "ASC" && sortOrder != "DESC" {
		sortOrder = "DESC"
	}
	baseQuery += fmt.Sprintf(" ORDER BY %s %s", sortBy, sortOrder)

	// Add pagination
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count leave requests", "error", err)
		response.InternalError(c, "Failed to count leave requests")
		return
	}

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list leave requests", "error", err)
		response.InternalError(c, "Failed to list leave requests")
		return
	}
	defer rows.Close()

	requests := make([]*entity.LeaveRequestResponse, 0)
	for rows.Next() {
		var req entity.LeaveRequest
		var employeeName, halfDayPeriod, rejectionNote sql.NullString
		var approvedBy sql.NullString
		var approvedAt sql.NullTime

		err := rows.Scan(
			&req.ID, &req.TenantID, &req.EmployeeID, &employeeName, &req.LeaveType,
			&req.StartDate, &req.EndDate, &req.Days, &req.Reason, &req.Status, &req.HalfDay,
			&halfDayPeriod, &approvedBy, &approvedAt, &rejectionNote,
			&req.CreatedAt, &req.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan leave request", "error", err)
			continue
		}

		if employeeName.Valid {
			req.EmployeeName = &employeeName.String
		}
		if halfDayPeriod.Valid {
			req.HalfDayPeriod = &halfDayPeriod.String
		}
		if rejectionNote.Valid {
			req.RejectionNote = &rejectionNote.String
		}
		if approvedBy.Valid {
			if approvedByUUID, err := uuid.Parse(approvedBy.String); err == nil {
				req.ApprovedBy = &approvedByUUID
			}
		}
		if approvedAt.Valid {
			req.ApprovedAt = &approvedAt.Time
		}

		requests = append(requests, req.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, requests, pagination)
}

// CreateLeaveRequest creates a new leave request
func (h *Handler) CreateLeaveRequest(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateLeaveRequestInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse employee ID
	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	// Parse dates
	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format. Use YYYY-MM-DD")
		return
	}

	endDate, err := time.Parse("2006-01-02", input.EndDate)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format. Use YYYY-MM-DD")
		return
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO leave_requests (
			id, tenant_id, employee_id, employee_name, leave_type,
			start_date, end_date, days, reason, status, half_day,
			half_day_period, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, created_at
	`

	var employeeName *string
	if input.EmployeeName != "" {
		employeeName = &input.EmployeeName
	}

	var halfDayPeriod *string
	if input.HalfDayPeriod != "" {
		halfDayPeriod = &input.HalfDayPeriod
	}

	err = h.db.QueryRow(query,
		id, tenantID, employeeID, employeeName, input.LeaveType,
		startDate, endDate, input.Days, input.Reason, "pending", input.HalfDay,
		halfDayPeriod, now, now,
	).Scan(&id, &now)

	if err != nil {
		h.log.Error("Failed to create leave request", "error", err)
		response.InternalError(c, "Failed to create leave request")
		return
	}

	req := &entity.LeaveRequest{
		ID:            id,
		TenantID:      tenantID,
		EmployeeID:    employeeID,
		EmployeeName:  employeeName,
		LeaveType:     input.LeaveType,
		StartDate:     startDate,
		EndDate:       endDate,
		Days:          input.Days,
		Reason:        input.Reason,
		Status:        "pending",
		HalfDay:       input.HalfDay,
		HalfDayPeriod: halfDayPeriod,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	response.Created(c, req.ToResponse())
}

// GetLeaveRequest returns a single leave request by ID
func (h *Handler) GetLeaveRequest(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid leave request ID")
		return
	}

	query := `
		SELECT id, tenant_id, employee_id, employee_name, leave_type,
			   start_date, end_date, days, reason, status, half_day,
			   half_day_period, approved_by, approved_at, rejection_note,
			   created_at, updated_at
		FROM leave_requests
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var req entity.LeaveRequest
	var employeeName, halfDayPeriod, rejectionNote sql.NullString
	var approvedBy sql.NullString
	var approvedAt sql.NullTime

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&req.ID, &req.TenantID, &req.EmployeeID, &employeeName, &req.LeaveType,
		&req.StartDate, &req.EndDate, &req.Days, &req.Reason, &req.Status, &req.HalfDay,
		&halfDayPeriod, &approvedBy, &approvedAt, &rejectionNote,
		&req.CreatedAt, &req.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Leave request")
		return
	}
	if err != nil {
		h.log.Error("Failed to get leave request", "error", err)
		response.InternalError(c, "Failed to get leave request")
		return
	}

	if employeeName.Valid {
		req.EmployeeName = &employeeName.String
	}
	if halfDayPeriod.Valid {
		req.HalfDayPeriod = &halfDayPeriod.String
	}
	if rejectionNote.Valid {
		req.RejectionNote = &rejectionNote.String
	}
	if approvedBy.Valid {
		if approvedByUUID, err := uuid.Parse(approvedBy.String); err == nil {
			req.ApprovedBy = &approvedByUUID
		}
	}
	if approvedAt.Valid {
		req.ApprovedAt = &approvedAt.Time
	}

	response.Success(c, req.ToResponse())
}

// UpdateLeaveRequest updates an existing leave request
func (h *Handler) UpdateLeaveRequest(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid leave request ID")
		return
	}

	var input entity.UpdateLeaveRequestInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic update query
	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.LeaveType != nil {
		addUpdate("leave_type", *input.LeaveType)
	}
	if input.StartDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.StartDate); err == nil {
			addUpdate("start_date", parsed)
		}
	}
	if input.EndDate != nil {
		if parsed, err := time.Parse("2006-01-02", *input.EndDate); err == nil {
			addUpdate("end_date", parsed)
		}
	}
	if input.Days != nil {
		addUpdate("days", *input.Days)
	}
	if input.Reason != nil {
		addUpdate("reason", *input.Reason)
	}
	if input.Status != nil {
		addUpdate("status", *input.Status)
		// If approving, set approved_at
		if *input.Status == "approved" {
			addUpdate("approved_at", time.Now())
		}
	}
	if input.HalfDay != nil {
		addUpdate("half_day", *input.HalfDay)
	}
	if input.HalfDayPeriod != nil {
		addUpdate("half_day_period", *input.HalfDayPeriod)
	}
	if input.RejectionNote != nil {
		addUpdate("rejection_note", *input.RejectionNote)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addUpdate("updated_at", time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE leave_requests SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Leave request")
		return
	}
	if err != nil {
		h.log.Error("Failed to update leave request", "error", err)
		response.InternalError(c, "Failed to update leave request")
		return
	}

	// Fetch updated record
	h.GetLeaveRequest(c)
}

// DeleteLeaveRequest soft-deletes a leave request
func (h *Handler) DeleteLeaveRequest(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid leave request ID")
		return
	}

	query := `
		UPDATE leave_requests SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete leave request", "error", err)
		response.InternalError(c, "Failed to delete leave request")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Leave request")
		return
	}

	response.NoContent(c)
}

// =======================
// LEAVE BALANCE HANDLERS
// =======================

// ListLeaveBalances returns a paginated list of leave balances
func (h *Handler) ListLeaveBalances(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := (page - 1) * limit

	employeeID := c.Query("employee_id")
	year, _ := strconv.Atoi(c.DefaultQuery("year", fmt.Sprintf("%d", time.Now().Year())))

	// Build query
	baseQuery := `
		SELECT id, tenant_id, employee_id,
			   annual_total, annual_used, annual_pending, annual_remaining,
			   sick_total, sick_used, sick_pending, sick_remaining,
			   personal_total, personal_used, personal_pending, personal_remaining,
			   unpaid_used, year, created_at, updated_at
		FROM leave_balances
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM leave_balances WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	if employeeID != "" {
		if empUUID, err := uuid.Parse(employeeID); err == nil {
			argCount++
			filter := fmt.Sprintf(" AND employee_id = $%d", argCount)
			baseQuery += filter
			countQuery += filter
			args = append(args, empUUID)
		}
	}

	if year > 0 {
		argCount++
		filter := fmt.Sprintf(" AND year = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, year)
	}

	baseQuery += " ORDER BY created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count leave balances", "error", err)
		response.InternalError(c, "Failed to count leave balances")
		return
	}

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list leave balances", "error", err)
		response.InternalError(c, "Failed to list leave balances")
		return
	}
	defer rows.Close()

	balances := make([]*entity.LeaveBalanceResponse, 0)
	for rows.Next() {
		var bal entity.LeaveBalance

		err := rows.Scan(
			&bal.ID, &bal.TenantID, &bal.EmployeeID,
			&bal.AnnualTotal, &bal.AnnualUsed, &bal.AnnualPending, &bal.AnnualRemaining,
			&bal.SickTotal, &bal.SickUsed, &bal.SickPending, &bal.SickRemaining,
			&bal.PersonalTotal, &bal.PersonalUsed, &bal.PersonalPending, &bal.PersonalRemaining,
			&bal.UnpaidUsed, &bal.Year, &bal.CreatedAt, &bal.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan leave balance", "error", err)
			continue
		}

		balances = append(balances, bal.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, balances, pagination)
}

// CreateLeaveBalance creates a new leave balance
func (h *Handler) CreateLeaveBalance(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateLeaveBalanceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Parse employee ID
	employeeID, err := uuid.Parse(input.EmployeeID)
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	year := input.Year
	if year == 0 {
		year = time.Now().Year()
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO leave_balances (
			id, tenant_id, employee_id,
			annual_total, annual_used, annual_pending, annual_remaining,
			sick_total, sick_used, sick_pending, sick_remaining,
			personal_total, personal_used, personal_pending, personal_remaining,
			unpaid_used, year, created_at, updated_at
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, $7,
			$8, $9, $10, $11,
			$12, $13, $14, $15,
			$16, $17, $18, $19
		)
		RETURNING id, created_at
	`

	err = h.db.QueryRow(query,
		id, tenantID, employeeID,
		input.AnnualTotal, 0, 0, input.AnnualTotal,
		input.SickTotal, 0, 0, input.SickTotal,
		input.PersonalTotal, 0, 0, input.PersonalTotal,
		0, year, now, now,
	).Scan(&id, &now)

	if err != nil {
		h.log.Error("Failed to create leave balance", "error", err)
		response.InternalError(c, "Failed to create leave balance")
		return
	}

	bal := &entity.LeaveBalance{
		ID:                id,
		TenantID:          tenantID,
		EmployeeID:        employeeID,
		AnnualTotal:       input.AnnualTotal,
		AnnualRemaining:   input.AnnualTotal,
		SickTotal:         input.SickTotal,
		SickRemaining:     input.SickTotal,
		PersonalTotal:     input.PersonalTotal,
		PersonalRemaining: input.PersonalTotal,
		Year:              year,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	response.Created(c, bal.ToResponse())
}

// GetLeaveBalance returns a single leave balance by ID
func (h *Handler) GetLeaveBalance(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid leave balance ID")
		return
	}

	query := `
		SELECT id, tenant_id, employee_id,
			   annual_total, annual_used, annual_pending, annual_remaining,
			   sick_total, sick_used, sick_pending, sick_remaining,
			   personal_total, personal_used, personal_pending, personal_remaining,
			   unpaid_used, year, created_at, updated_at
		FROM leave_balances
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var bal entity.LeaveBalance

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&bal.ID, &bal.TenantID, &bal.EmployeeID,
		&bal.AnnualTotal, &bal.AnnualUsed, &bal.AnnualPending, &bal.AnnualRemaining,
		&bal.SickTotal, &bal.SickUsed, &bal.SickPending, &bal.SickRemaining,
		&bal.PersonalTotal, &bal.PersonalUsed, &bal.PersonalPending, &bal.PersonalRemaining,
		&bal.UnpaidUsed, &bal.Year, &bal.CreatedAt, &bal.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Leave balance")
		return
	}
	if err != nil {
		h.log.Error("Failed to get leave balance", "error", err)
		response.InternalError(c, "Failed to get leave balance")
		return
	}

	response.Success(c, bal.ToResponse())
}

// UpdateLeaveBalance updates an existing leave balance
func (h *Handler) UpdateLeaveBalance(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid leave balance ID")
		return
	}

	var input entity.UpdateLeaveBalanceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic update query
	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.AnnualTotal != nil {
		addUpdate("annual_total", *input.AnnualTotal)
	}
	if input.AnnualUsed != nil {
		addUpdate("annual_used", *input.AnnualUsed)
	}
	if input.AnnualPending != nil {
		addUpdate("annual_pending", *input.AnnualPending)
	}
	if input.AnnualRemaining != nil {
		addUpdate("annual_remaining", *input.AnnualRemaining)
	}
	if input.SickTotal != nil {
		addUpdate("sick_total", *input.SickTotal)
	}
	if input.SickUsed != nil {
		addUpdate("sick_used", *input.SickUsed)
	}
	if input.SickPending != nil {
		addUpdate("sick_pending", *input.SickPending)
	}
	if input.SickRemaining != nil {
		addUpdate("sick_remaining", *input.SickRemaining)
	}
	if input.PersonalTotal != nil {
		addUpdate("personal_total", *input.PersonalTotal)
	}
	if input.PersonalUsed != nil {
		addUpdate("personal_used", *input.PersonalUsed)
	}
	if input.PersonalPending != nil {
		addUpdate("personal_pending", *input.PersonalPending)
	}
	if input.PersonalRemaining != nil {
		addUpdate("personal_remaining", *input.PersonalRemaining)
	}
	if input.UnpaidUsed != nil {
		addUpdate("unpaid_used", *input.UnpaidUsed)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addUpdate("updated_at", time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE leave_balances SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Leave balance")
		return
	}
	if err != nil {
		h.log.Error("Failed to update leave balance", "error", err)
		response.InternalError(c, "Failed to update leave balance")
		return
	}

	// Fetch updated record
	h.GetLeaveBalance(c)
}

// DeleteLeaveBalance soft-deletes a leave balance
func (h *Handler) DeleteLeaveBalance(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid leave balance ID")
		return
	}

	query := `
		UPDATE leave_balances SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete leave balance", "error", err)
		response.InternalError(c, "Failed to delete leave balance")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Leave balance")
		return
	}

	response.NoContent(c)
}
