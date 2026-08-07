package handler

import (
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListAccountingPeriods returns all accounting periods for the tenant
func (h *Handler) ListAccountingPeriods(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Opt-in paging: one row per month per tenant, so a company five years in
	// has 60 and it only ever grows. The web period-lock screen sends no page
	// params and still gets the full history.
	query := `
		SELECT id, tenant_id, name, start_date, end_date, is_locked,
		       locked_by, locked_at, created_at, updated_at
		FROM accounting_periods
		WHERE tenant_id = $1
		ORDER BY start_date ASC
	`
	args := []interface{}{tenantID}
	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		query += " LIMIT $2 OFFSET $3"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list accounting periods", "error", err)
		response.InternalError(c, "Failed to list accounting periods")
		return
	}
	defer rows.Close()

	type AccountingPeriodResponse struct {
		ID        uuid.UUID  `json:"id"`
		TenantID  uuid.UUID  `json:"tenant_id"`
		Name      string     `json:"name"`
		StartDate string     `json:"start_date"`
		EndDate   string     `json:"end_date"`
		IsLocked  bool       `json:"is_locked"`
		LockedBy  *uuid.UUID `json:"locked_by"`
		LockedAt  *time.Time `json:"locked_at"`
		CreatedAt time.Time  `json:"created_at"`
		UpdatedAt time.Time  `json:"updated_at"`
	}

	periods := make([]AccountingPeriodResponse, 0)
	for rows.Next() {
		var p AccountingPeriodResponse
		var startDate, endDate time.Time
		err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &startDate, &endDate,
			&p.IsLocked, &p.LockedBy, &p.LockedAt, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			h.log.Error("Failed to scan accounting period", "error", err)
			continue
		}
		p.StartDate = startDate.Format("2006-01-02")
		p.EndDate = endDate.Format("2006-01-02")
		periods = append(periods, p)
	}

	if !paginate {
		response.Success(c, periods)
		return
	}

	total := 0
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM accounting_periods WHERE tenant_id = $1`, tenantID).Scan(&total); err != nil {
		h.log.Error("Failed to count accounting periods", "error", err)
		total = len(periods)
	}
	response.Paginated(c, periods, page, pageSize, total)
}

// CreateAccountingPeriod creates a new accounting period
func (h *Handler) CreateAccountingPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input struct {
		Name      string `json:"name" binding:"required"`
		StartDate string `json:"start_date" binding:"required"`
		EndDate   string `json:"end_date" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
		return
	}
	endDate, err := time.Parse("2006-01-02", input.EndDate)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
		return
	}
	if !endDate.After(startDate) {
		response.BadRequest(c, "end_date must be after start_date")
		return
	}

	id := uuid.New()
	now := time.Now()
	_, err = h.db.Exec(`
		INSERT INTO accounting_periods (id, tenant_id, name, start_date, end_date, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, tenantID, input.Name, startDate, endDate, now, now)
	if err != nil {
		h.log.Error("Failed to create accounting period", "error", err)
		response.InternalError(c, "Failed to create accounting period")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":         id,
		"tenant_id":  tenantID,
		"name":       input.Name,
		"start_date": input.StartDate,
		"end_date":   input.EndDate,
		"is_locked":  false,
		"created_at": now,
		"updated_at": now,
	})
}

// LockAccountingPeriod locks an accounting period
func (h *Handler) LockAccountingPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid period ID")
		return
	}

	userID, _ := middleware.GetUserID(c)
	now := time.Now()

	result, err := h.db.Exec(`
		UPDATE accounting_periods
		SET is_locked = true, locked_by = $1, locked_at = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5
	`, userID, now, now, periodID, tenantID)
	if err != nil {
		h.log.Error("Failed to lock accounting period", "error", err)
		response.InternalError(c, "Failed to lock accounting period")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Accounting period")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":        periodID,
		"is_locked": true,
		"locked_by": userID,
		"locked_at": now,
	})
}

// UnlockAccountingPeriod unlocks an accounting period
func (h *Handler) UnlockAccountingPeriod(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	periodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid period ID")
		return
	}

	now := time.Now()
	result, err := h.db.Exec(`
		UPDATE accounting_periods
		SET is_locked = false, locked_by = NULL, locked_at = NULL, updated_at = $1
		WHERE id = $2 AND tenant_id = $3
	`, now, periodID, tenantID)
	if err != nil {
		h.log.Error("Failed to unlock accounting period", "error", err)
		response.InternalError(c, "Failed to unlock accounting period")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Accounting period")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":        periodID,
		"is_locked": false,
	})
}

// AutoCreatePeriods creates 12 monthly accounting periods for a given year
func (h *Handler) AutoCreatePeriods(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input struct {
		Year int `json:"year" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	if input.Year < 2000 || input.Year > 2100 {
		response.BadRequest(c, "Year must be between 2000 and 2100")
		return
	}

	monthNames := []string{
		"Yanvar", "Fevral", "Mart", "Aprel", "May", "Iyun",
		"Iyul", "Avgust", "Sentabr", "Oktabr", "Noyabr", "Dekabr",
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create periods")
		return
	}
	defer tx.Rollback()

	type PeriodResult struct {
		ID        uuid.UUID `json:"id"`
		Name      string    `json:"name"`
		StartDate string    `json:"start_date"`
		EndDate   string    `json:"end_date"`
		IsLocked  bool      `json:"is_locked"`
	}

	results := make([]PeriodResult, 0, 12)
	now := time.Now()

	for month := 1; month <= 12; month++ {
		startDate := time.Date(input.Year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		endDate := startDate.AddDate(0, 1, -1) // last day of month
		name := fmt.Sprintf("%s %d", monthNames[month-1], input.Year)
		id := uuid.New()

		_, err := tx.Exec(`
			INSERT INTO accounting_periods (id, tenant_id, name, start_date, end_date, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, start_date, end_date) DO NOTHING
		`, id, tenantID, name, startDate, endDate, now, now)
		if err != nil {
			h.log.Error("Failed to create accounting period", "error", err, "month", month)
			response.InternalError(c, "Failed to create periods")
			return
		}

		results = append(results, PeriodResult{
			ID:        id,
			Name:      name,
			StartDate: startDate.Format("2006-01-02"),
			EndDate:   endDate.Format("2006-01-02"),
			IsLocked:  false,
		})
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create periods")
		return
	}

	response.Success(c, results)
}
