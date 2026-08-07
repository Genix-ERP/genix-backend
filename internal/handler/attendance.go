package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListAttendanceRecords returns a paginated list of attendance records
func (h *Handler) ListAttendanceRecords(c *gin.Context) {
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
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	status := c.Query("status")
	sortBy := c.DefaultQuery("sort_by", "date")
	sortOrder := c.DefaultQuery("sort_order", "DESC")

	// Build query
	baseQuery := `
		SELECT id, tenant_id, employee_id, employee_name, department, date,
			   clock_in, clock_out, status, break_duration, notes,
			   created_at, updated_at
		FROM attendance_records
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM attendance_records WHERE tenant_id = $1 AND deleted_at IS NULL`

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

	if dateFrom != "" {
		if parsed, err := time.Parse("2006-01-02", dateFrom); err == nil {
			argCount++
			filter := fmt.Sprintf(" AND date >= $%d", argCount)
			baseQuery += filter
			countQuery += filter
			args = append(args, parsed)
		}
	}

	if dateTo != "" {
		if parsed, err := time.Parse("2006-01-02", dateTo); err == nil {
			argCount++
			filter := fmt.Sprintf(" AND date <= $%d", argCount)
			baseQuery += filter
			countQuery += filter
			args = append(args, parsed)
		}
	}

	if status != "" && status != "all" {
		argCount++
		filter := fmt.Sprintf(" AND status = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, status)
	}

	// Add sorting
	validSortFields := map[string]bool{
		"date": true, "clock_in": true, "status": true, "created_at": true,
	}
	if !validSortFields[sortBy] {
		sortBy = "date"
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
		h.log.Error("Failed to count attendance records", "error", err)
		response.InternalError(c, "Failed to count attendance records")
		return
	}

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list attendance records", "error", err)
		response.InternalError(c, "Failed to list attendance records")
		return
	}
	defer rows.Close()

	records := make([]*entity.AttendanceRecordResponse, 0)
	for rows.Next() {
		var rec entity.AttendanceRecord
		var employeeName, department, notes sql.NullString
		var clockIn, clockOut sql.NullTime

		err := rows.Scan(
			&rec.ID, &rec.TenantID, &rec.EmployeeID, &employeeName, &department,
			&rec.Date, &clockIn, &clockOut, &rec.Status, &rec.BreakDuration,
			&notes, &rec.CreatedAt, &rec.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan attendance record", "error", err)
			continue
		}

		if employeeName.Valid {
			rec.EmployeeName = &employeeName.String
		}
		if department.Valid {
			rec.Department = &department.String
		}
		if notes.Valid {
			rec.Notes = &notes.String
		}
		if clockIn.Valid {
			rec.ClockIn = &clockIn.Time
		}
		if clockOut.Valid {
			rec.ClockOut = &clockOut.Time
		}

		records = append(records, rec.ToResponse())
	}

	pagination := entity.NewPagination(page, limit)
	pagination.Calculate(total)

	response.SuccessWithPagination(c, records, pagination)
}

// CreateAttendanceRecord creates a new attendance record
func (h *Handler) CreateAttendanceRecord(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input entity.CreateAttendanceRecordInput
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

	// Parse date
	date, err := time.Parse("2006-01-02", input.Date)
	if err != nil {
		response.BadRequest(c, "Invalid date format. Use YYYY-MM-DD")
		return
	}

	// Parse clock in/out times
	var clockIn, clockOut *time.Time
	if input.ClockIn != "" {
		parsed, err := time.Parse(time.RFC3339, input.ClockIn)
		if err == nil {
			clockIn = &parsed
		}
	}
	if input.ClockOut != "" {
		parsed, err := time.Parse(time.RFC3339, input.ClockOut)
		if err == nil {
			clockOut = &parsed
		}
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO attendance_records (
			id, tenant_id, employee_id, employee_name, department, date,
			clock_in, clock_out, status, break_duration, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at
	`

	var employeeName, department, notes *string
	if input.EmployeeName != "" {
		employeeName = &input.EmployeeName
	}
	if input.Department != "" {
		department = &input.Department
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	err = h.db.QueryRow(query,
		id, tenantID, employeeID, employeeName, department, date,
		clockIn, clockOut, input.Status, input.BreakDuration, notes,
		now, now,
	).Scan(&id, &now)

	if err != nil {
		h.log.Error("Failed to create attendance record", "error", err)
		response.InternalError(c, "Failed to create attendance record")
		return
	}

	rec := &entity.AttendanceRecord{
		ID:             id,
		TenantID:       tenantID,
		EmployeeID:     employeeID,
		EmployeeName:   employeeName,
		Department:     department,
		Date:           date,
		ClockIn:        clockIn,
		ClockOut:       clockOut,
		Status:         input.Status,
		BreakDuration:  input.BreakDuration,
		Notes:          notes,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	response.Created(c, rec.ToResponse())
}

// GetAttendanceRecord returns a single attendance record by ID
func (h *Handler) GetAttendanceRecord(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid attendance record ID")
		return
	}

	query := `
		SELECT id, tenant_id, employee_id, employee_name, department, date,
			   clock_in, clock_out, status, break_duration, notes,
			   created_at, updated_at
		FROM attendance_records
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var rec entity.AttendanceRecord
	var employeeName, department, notes sql.NullString
	var clockIn, clockOut sql.NullTime

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&rec.ID, &rec.TenantID, &rec.EmployeeID, &employeeName, &department,
		&rec.Date, &clockIn, &clockOut, &rec.Status, &rec.BreakDuration,
		&notes, &rec.CreatedAt, &rec.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Attendance record")
		return
	}
	if err != nil {
		h.log.Error("Failed to get attendance record", "error", err)
		response.InternalError(c, "Failed to get attendance record")
		return
	}

	if employeeName.Valid {
		rec.EmployeeName = &employeeName.String
	}
	if department.Valid {
		rec.Department = &department.String
	}
	if notes.Valid {
		rec.Notes = &notes.String
	}
	if clockIn.Valid {
		rec.ClockIn = &clockIn.Time
	}
	if clockOut.Valid {
		rec.ClockOut = &clockOut.Time
	}

	response.Success(c, rec.ToResponse())
}

// UpdateAttendanceRecord updates an existing attendance record
func (h *Handler) UpdateAttendanceRecord(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid attendance record ID")
		return
	}

	var input entity.UpdateAttendanceRecordInput
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

	if input.EmployeeName != nil {
		addUpdate("employee_name", *input.EmployeeName)
	}
	if input.Department != nil {
		addUpdate("department", *input.Department)
	}
	if input.Date != nil {
		if parsed, err := time.Parse("2006-01-02", *input.Date); err == nil {
			addUpdate("date", parsed)
		}
	}
	if input.ClockIn != nil {
		if parsed, err := time.Parse(time.RFC3339, *input.ClockIn); err == nil {
			addUpdate("clock_in", parsed)
		}
	}
	if input.ClockOut != nil {
		if parsed, err := time.Parse(time.RFC3339, *input.ClockOut); err == nil {
			addUpdate("clock_out", parsed)
		}
	}
	if input.Status != nil {
		addUpdate("status", *input.Status)
	}
	if input.BreakDuration != nil {
		addUpdate("break_duration", *input.BreakDuration)
	}
	if input.Notes != nil {
		addUpdate("notes", *input.Notes)
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
		UPDATE attendance_records SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, fmt.Sprintf("%s", updates[0]), argCount-1, argCount)

	// Rebuild query with all updates
	query = fmt.Sprintf(`
		UPDATE attendance_records SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, fmt.Sprintf("%s", joinUpdates(updates)), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Attendance record")
		return
	}
	if err != nil {
		h.log.Error("Failed to update attendance record", "error", err)
		response.InternalError(c, "Failed to update attendance record")
		return
	}

	// Fetch updated record
	h.GetAttendanceRecord(c)
}

// DeleteAttendanceRecord soft-deletes an attendance record
func (h *Handler) DeleteAttendanceRecord(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid attendance record ID")
		return
	}

	query := `
		UPDATE attendance_records SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete attendance record", "error", err)
		response.InternalError(c, "Failed to delete attendance record")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Attendance record")
		return
	}

	response.NoContent(c)
}

// Helper function to join update strings
func joinUpdates(updates []string) string {
	if len(updates) == 0 {
		return ""
	}
	result := updates[0]
	for i := 1; i < len(updates); i++ {
		result += ", " + updates[i]
	}
	return result
}
