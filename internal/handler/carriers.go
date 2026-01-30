package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Carrier represents a shipping carrier
type Carrier struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	TenantID    uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	Code        string     `json:"code" db:"code"`
	Name        string     `json:"name" db:"name"`
	TrackingURL *string    `json:"tracking_url,omitempty" db:"tracking_url"`
	Phone       *string    `json:"phone,omitempty" db:"phone"`
	Email       *string    `json:"email,omitempty" db:"email"`
	Website     *string    `json:"website,omitempty" db:"website"`
	Notes       *string    `json:"notes,omitempty" db:"notes"`
	IsActive    bool       `json:"is_active" db:"is_active"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
}

// CreateCarrierInput represents input for creating a carrier
type CreateCarrierInput struct {
	Code        string  `json:"code" binding:"required"`
	Name        string  `json:"name" binding:"required"`
	TrackingURL *string `json:"tracking_url,omitempty"`
	Phone       *string `json:"phone,omitempty"`
	Email       *string `json:"email,omitempty"`
	Website     *string `json:"website,omitempty"`
	Notes       *string `json:"notes,omitempty"`
	IsActive    *bool   `json:"is_active,omitempty"`
}

// UpdateCarrierInput represents input for updating a carrier
type UpdateCarrierInput struct {
	Code        *string `json:"code,omitempty"`
	Name        *string `json:"name,omitempty"`
	TrackingURL *string `json:"tracking_url,omitempty"`
	Phone       *string `json:"phone,omitempty"`
	Email       *string `json:"email,omitempty"`
	Website     *string `json:"website,omitempty"`
	Notes       *string `json:"notes,omitempty"`
	IsActive    *bool   `json:"is_active,omitempty"`
}

// ListCarriers returns all carriers for the tenant
func (h *Handler) ListCarriers(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	// Build query
	query := `
		SELECT id, tenant_id, code, name, tracking_url, phone, email, website, notes, is_active, created_by, created_at, updated_at
		FROM carriers
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by active status
	if activeOnly := c.Query("active"); activeOnly == "true" {
		argCount++
		query += fmt.Sprintf(" AND is_active = $%d", argCount)
		args = append(args, true)
	}

	query += " ORDER BY name ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list carriers", "error", err)
		response.InternalError(c, "Failed to list carriers")
		return
	}
	defer rows.Close()

	carriers := []Carrier{}
	for rows.Next() {
		var carrier Carrier
		err := rows.Scan(
			&carrier.ID, &carrier.TenantID, &carrier.Code, &carrier.Name,
			&carrier.TrackingURL, &carrier.Phone, &carrier.Email, &carrier.Website,
			&carrier.Notes, &carrier.IsActive, &carrier.CreatedBy, &carrier.CreatedAt, &carrier.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan carrier", "error", err)
			continue
		}
		carriers = append(carriers, carrier)
	}

	response.Success(c, carriers)
}

// GetCarrier returns a single carrier by ID
func (h *Handler) GetCarrier(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	carrierID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid carrier ID")
		return
	}

	var carrier Carrier
	err = h.db.QueryRow(`
		SELECT id, tenant_id, code, name, tracking_url, phone, email, website, notes, is_active, created_by, created_at, updated_at
		FROM carriers
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, carrierID, tenantID).Scan(
		&carrier.ID, &carrier.TenantID, &carrier.Code, &carrier.Name,
		&carrier.TrackingURL, &carrier.Phone, &carrier.Email, &carrier.Website,
		&carrier.Notes, &carrier.IsActive, &carrier.CreatedBy, &carrier.CreatedAt, &carrier.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Carrier")
		return
	}
	if err != nil {
		h.log.Error("Failed to get carrier", "error", err)
		response.InternalError(c, "Failed to get carrier")
		return
	}

	response.Success(c, carrier)
}

// CreateCarrier creates a new carrier
func (h *Handler) CreateCarrier(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input CreateCarrierInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check for duplicate code
	var exists bool
	err := h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM carriers WHERE tenant_id = $1 AND code = $2 AND deleted_at IS NULL)",
		tenantID, input.Code,
	).Scan(&exists)
	if err != nil {
		response.InternalError(c, "Failed to check carrier code")
		return
	}
	if exists {
		response.Conflict(c, "Carrier with this code already exists")
		return
	}

	carrierID := uuid.New()
	now := time.Now()
	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	_, err = h.db.Exec(`
		INSERT INTO carriers (id, tenant_id, code, name, tracking_url, phone, email, website, notes, is_active, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, carrierID, tenantID, input.Code, input.Name, input.TrackingURL, input.Phone, input.Email, input.Website, input.Notes, isActive, createdBy, now, now)

	if err != nil {
		h.log.Error("Failed to create carrier", "error", err)
		response.InternalError(c, "Failed to create carrier")
		return
	}

	carrier := Carrier{
		ID:          carrierID,
		TenantID:    tenantID,
		Code:        input.Code,
		Name:        input.Name,
		TrackingURL: input.TrackingURL,
		Phone:       input.Phone,
		Email:       input.Email,
		Website:     input.Website,
		Notes:       input.Notes,
		IsActive:    isActive,
		CreatedBy:   createdBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	response.Created(c, carrier)
}

// UpdateCarrier updates an existing carrier
func (h *Handler) UpdateCarrier(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	carrierID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid carrier ID")
		return
	}

	var input UpdateCarrierInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Check if carrier exists
	var exists bool
	err = h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM carriers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)",
		carrierID, tenantID,
	).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Carrier")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Code != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("code = $%d", argCount))
		args = append(args, *input.Code)
	}
	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.TrackingURL != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("tracking_url = $%d", argCount))
		args = append(args, *input.TrackingURL)
	}
	if input.Phone != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("phone = $%d", argCount))
		args = append(args, *input.Phone)
	}
	if input.Email != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("email = $%d", argCount))
		args = append(args, *input.Email)
	}
	if input.Website != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("website = $%d", argCount))
		args = append(args, *input.Website)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause
	argCount++
	args = append(args, carrierID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE carriers SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update carrier", "error", err)
		response.InternalError(c, "Failed to update carrier")
		return
	}

	// Fetch and return updated carrier
	h.GetCarrier(c)
}

// DeleteCarrier soft deletes a carrier
func (h *Handler) DeleteCarrier(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	carrierID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid carrier ID")
		return
	}

	result, err := h.db.Exec(
		"UPDATE carriers SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), carrierID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete carrier", "error", err)
		response.InternalError(c, "Failed to delete carrier")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Carrier")
		return
	}

	response.NoContent(c)
}

