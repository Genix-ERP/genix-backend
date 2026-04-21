package handler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// JobPosition represents a job position
type JobPosition struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	Code           string     `json:"code"`
	Name           string     `json:"name"`
	Description    *string    `json:"description,omitempty"`
	IsActive       bool       `json:"is_active"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CreateJobPositionInput represents input for creating a job position
type CreateJobPositionInput struct {
	Code        string  `json:"code"`
	Name        string  `json:"name" binding:"required"`
	Description *string `json:"description,omitempty"`
}

// UpdateJobPositionInput represents input for updating a job position
type UpdateJobPositionInput struct {
	Code        *string `json:"code,omitempty"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	IsActive    *bool   `json:"is_active,omitempty"`
}

// ListJobPositions returns all job positions for the tenant
func (h *Handler) ListJobPositions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	query := `
		SELECT id, tenant_id, organization_id, code, name, description, is_active, created_at, updated_at
		FROM job_positions
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY name ASC
	`
	args := []interface{}{tenantID}

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query = `
			SELECT id, tenant_id, organization_id, code, name, description, is_active, created_at, updated_at
			FROM job_positions
			WHERE tenant_id = $1 AND (organization_id = $2 OR organization_id IS NULL) AND deleted_at IS NULL
			ORDER BY name ASC
		`
		args = append(args, orgID)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list job positions", "error", err)
		response.InternalError(c, "Failed to list job positions")
		return
	}
	defer rows.Close()

	positions := make([]JobPosition, 0)
	for rows.Next() {
		var jp JobPosition
		var orgID sql.NullString
		var description sql.NullString

		err := rows.Scan(
			&jp.ID, &jp.TenantID, &orgID, &jp.Code, &jp.Name,
			&description, &jp.IsActive, &jp.CreatedAt, &jp.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan job position", "error", err)
			continue
		}
		if orgID.Valid {
			id, _ := uuid.Parse(orgID.String)
			jp.OrganizationID = &id
		}
		if description.Valid {
			jp.Description = &description.String
		}
		positions = append(positions, jp)
	}

	response.Success(c, positions)
}

// CreateJobPosition creates a new job position
func (h *Handler) CreateJobPosition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input CreateJobPositionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	var orgID *uuid.UUID
	if id, orgOk := middleware.GetOrganizationID(c); orgOk && id != uuid.Nil {
		orgID = &id
	}

	// Auto-generate code from name if not provided (supports Cyrillic/non-Latin names)
	if strings.TrimSpace(input.Code) == "" {
		input.Code = generateCodeFromName(input.Name, 20, "POS")
	}

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO job_positions (id, tenant_id, organization_id, code, name, description, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, true, $7, $7)
		RETURNING id, created_at
	`

	err := h.db.QueryRow(query, id, tenantID, orgID, input.Code, input.Name, input.Description, now).Scan(&id, &now)
	if err != nil {
		h.log.Error("Failed to create job position", "error", err)
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			response.Conflict(c, "Job position with this code already exists")
			return
		}
		response.InternalError(c, "Failed to create job position")
		return
	}

	jp := JobPosition{
		ID:             id,
		TenantID:       tenantID,
		OrganizationID: orgID,
		Code:           input.Code,
		Name:           input.Name,
		Description:    input.Description,
		IsActive:       true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	response.Created(c, jp)
}

// GetJobPosition returns a single job position by ID
func (h *Handler) GetJobPosition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid job position ID")
		return
	}

	query := `
		SELECT id, tenant_id, organization_id, code, name, description, is_active, created_at, updated_at
		FROM job_positions
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var jp JobPosition
	var orgID, description sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&jp.ID, &jp.TenantID, &orgID, &jp.Code, &jp.Name,
		&description, &jp.IsActive, &jp.CreatedAt, &jp.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Job position")
		return
	}
	if err != nil {
		h.log.Error("Failed to get job position", "error", err)
		response.InternalError(c, "Failed to get job position")
		return
	}

	if orgID.Valid {
		oid, _ := uuid.Parse(orgID.String)
		jp.OrganizationID = &oid
	}
	if description.Valid {
		jp.Description = &description.String
	}

	response.Success(c, jp)
}

// UpdateJobPosition updates an existing job position
func (h *Handler) UpdateJobPosition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid job position ID")
		return
	}

	var input UpdateJobPositionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.Code != nil {
		addUpdate("code", *input.Code)
	}
	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.Description != nil {
		addUpdate("description", *input.Description)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
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
		UPDATE job_positions SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Job position")
		return
	}
	if err != nil {
		h.log.Error("Failed to update job position", "error", err)
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			response.Conflict(c, "Job position with this code already exists")
			return
		}
		response.InternalError(c, "Failed to update job position")
		return
	}

	h.GetJobPosition(c)
}

// DeleteJobPosition soft-deletes a job position
func (h *Handler) DeleteJobPosition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid job position ID")
		return
	}

	query := `
		UPDATE job_positions SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`

	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete job position", "error", err)
		response.InternalError(c, "Failed to delete job position")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Job position")
		return
	}

	response.NoContent(c)
}
