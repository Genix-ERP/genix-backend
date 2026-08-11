package handler

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListUnitsOfMeasure returns all UOM entries for the tenant
func (h *Handler) ListUnitsOfMeasure(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	offset := (page - 1) * limit

	search := c.Query("search")
	category := c.Query("category")

	baseQuery := `
		SELECT id, tenant_id, code, name, category, base_unit_id, conversion_factor, is_active, created_at
		FROM units_of_measure
		WHERE tenant_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM units_of_measure WHERE tenant_id = $1`

	args := []interface{}{tenantID}
	argCount := 1

	if search != "" {
		argCount++
		filter := fmt.Sprintf(" AND (code ILIKE $%d OR name ILIKE $%d)", argCount, argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, "%"+search+"%")
	}

	if category != "" && category != "all" {
		argCount++
		filter := fmt.Sprintf(" AND category = $%d", argCount)
		baseQuery += filter
		countQuery += filter
		args = append(args, category)
	}

	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count units of measure", "error", err)
		response.InternalError(c, "Failed to list units of measure")
		return
	}

	baseQuery += " ORDER BY category ASC, conversion_factor ASC, name ASC, id ASC"
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list units of measure", "error", err)
		response.InternalError(c, "Failed to list units of measure")
		return
	}
	defer rows.Close()

	units := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, tenantID uuid.UUID
		var code, name string
		var cat sql.NullString
		var baseUnitID *uuid.UUID
		var convFactor float64
		var isActive bool
		var createdAt sql.NullTime

		err := rows.Scan(&id, &tenantID, &code, &name, &cat, &baseUnitID, &convFactor, &isActive, &createdAt)
		if err != nil {
			h.log.Error("Failed to scan unit of measure", "error", err)
			continue
		}

		unit := map[string]interface{}{
			"id":                id,
			"tenant_id":         tenantID,
			"code":              code,
			"name":              name,
			"conversion_factor": convFactor,
			"is_active":         isActive,
		}
		if cat.Valid {
			unit["category"] = cat.String
		}
		if baseUnitID != nil {
			unit["base_unit_id"] = *baseUnitID
		}
		if createdAt.Valid {
			unit["created_at"] = createdAt.Time
		}

		units = append(units, unit)
	}

	response.Paginated(c, units, page, limit, total)
}

// CreateUnitOfMeasure creates a new unit of measure
func (h *Handler) CreateUnitOfMeasure(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input struct {
		Code             string  `json:"code" binding:"required"`
		Name             string  `json:"name" binding:"required"`
		Category         string  `json:"category"`
		BaseUnitID       *string `json:"base_unit_id"`
		ConversionFactor float64 `json:"conversion_factor"`
		IsActive         *bool   `json:"is_active"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.ConversionFactor <= 0 {
		input.ConversionFactor = 1
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	// Check duplicate code
	var exists bool
	err := h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM units_of_measure WHERE tenant_id = $1 AND code ILIKE $2)",
		tenantID, input.Code,
	).Scan(&exists)
	if err == nil && exists {
		response.BadRequest(c, "A unit with this code already exists")
		return
	}

	id := uuid.New()

	var baseUnitID *uuid.UUID
	if input.BaseUnitID != nil && *input.BaseUnitID != "" {
		parsed, err := uuid.Parse(*input.BaseUnitID)
		if err == nil {
			baseUnitID = &parsed
		}
	}

	query := `
		INSERT INTO units_of_measure (id, tenant_id, code, name, category, base_unit_id, conversion_factor, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, tenant_id, code, name, category, base_unit_id, conversion_factor, is_active, created_at
	`

	var resID, resTenantID uuid.UUID
	var resCode, resName string
	var resCat sql.NullString
	var resBaseUnitID *uuid.UUID
	var resConvFactor float64
	var resIsActive bool
	var resCreatedAt sql.NullTime

	err = h.db.QueryRow(query,
		id, tenantID, input.Code, input.Name, nullString(input.Category), baseUnitID, input.ConversionFactor, isActive,
	).Scan(&resID, &resTenantID, &resCode, &resName, &resCat, &resBaseUnitID, &resConvFactor, &resIsActive, &resCreatedAt)

	if err != nil {
		h.log.Error("Failed to create unit of measure", "error", err)
		response.InternalError(c, "Failed to create unit of measure")
		return
	}

	result := map[string]interface{}{
		"id":                resID,
		"tenant_id":         resTenantID,
		"code":              resCode,
		"name":              resName,
		"conversion_factor": resConvFactor,
		"is_active":         resIsActive,
	}
	if resCat.Valid {
		result["category"] = resCat.String
	}
	if resBaseUnitID != nil {
		result["base_unit_id"] = *resBaseUnitID
	}
	if resCreatedAt.Valid {
		result["created_at"] = resCreatedAt.Time
	}

	response.Created(c, result)
}

// UpdateUnitOfMeasure updates a unit of measure
func (h *Handler) UpdateUnitOfMeasure(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var input struct {
		Code             *string  `json:"code"`
		Name             *string  `json:"name"`
		Category         *string  `json:"category"`
		BaseUnitID       *string  `json:"base_unit_id"`
		ConversionFactor *float64 `json:"conversion_factor"`
		IsActive         *bool    `json:"is_active"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check exists
	var exists bool
	err = h.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM units_of_measure WHERE id = $1 AND tenant_id = $2)",
		id, tenantID,
	).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Unit of measure not found")
		return
	}

	// Check duplicate code if changing
	if input.Code != nil {
		var dup bool
		h.db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM units_of_measure WHERE tenant_id = $1 AND code ILIKE $2 AND id != $3)",
			tenantID, *input.Code, id,
		).Scan(&dup)
		if dup {
			response.BadRequest(c, "A unit with this code already exists")
			return
		}
	}

	// Build dynamic update
	sets := []string{}
	args := []interface{}{}
	argN := 0

	addSet := func(col string, val interface{}) {
		argN++
		sets = append(sets, fmt.Sprintf("%s = $%d", col, argN))
		args = append(args, val)
	}

	if input.Code != nil {
		addSet("code", *input.Code)
	}
	if input.Name != nil {
		addSet("name", *input.Name)
	}
	if input.Category != nil {
		addSet("category", *input.Category)
	}
	if input.ConversionFactor != nil {
		addSet("conversion_factor", *input.ConversionFactor)
	}
	if input.IsActive != nil {
		addSet("is_active", *input.IsActive)
	}
	if input.BaseUnitID != nil {
		if *input.BaseUnitID == "" {
			addSet("base_unit_id", nil)
		} else {
			parsed, err := uuid.Parse(*input.BaseUnitID)
			if err == nil {
				addSet("base_unit_id", parsed)
			}
		}
	}

	if len(sets) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addSet("updated_at", "NOW()")

	query := "UPDATE units_of_measure SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	// Fix: updated_at should use NOW() directly, not as a string param
	// Remove the last set and handle updated_at separately
	query = "UPDATE units_of_measure SET "
	// Rebuild without the updated_at param
	args = args[:len(args)-1]
	argN--
	sets = sets[:len(sets)-1]
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	if len(sets) > 0 {
		query += ", "
	}
	query += "updated_at = CURRENT_TIMESTAMP"

	argN++
	query += fmt.Sprintf(" WHERE id = $%d", argN)
	args = append(args, id)
	argN++
	query += fmt.Sprintf(" AND tenant_id = $%d", argN)
	args = append(args, tenantID)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update unit of measure", "error", err)
		response.InternalError(c, "Failed to update unit of measure")
		return
	}

	// Fetch updated record
	var resID, resTenantID uuid.UUID
	var resCode, resName string
	var resCat sql.NullString
	var resBaseUnitID *uuid.UUID
	var resConvFactor float64
	var resIsActive bool
	var resCreatedAt sql.NullTime

	err = h.db.QueryRow(
		`SELECT id, tenant_id, code, name, category, base_unit_id, conversion_factor, is_active, created_at
		 FROM units_of_measure WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(&resID, &resTenantID, &resCode, &resName, &resCat, &resBaseUnitID, &resConvFactor, &resIsActive, &resCreatedAt)

	if err != nil {
		response.Success(c, map[string]interface{}{"id": id, "message": "Updated successfully"})
		return
	}

	result := map[string]interface{}{
		"id":                resID,
		"tenant_id":         resTenantID,
		"code":              resCode,
		"name":              resName,
		"conversion_factor": resConvFactor,
		"is_active":         resIsActive,
	}
	if resCat.Valid {
		result["category"] = resCat.String
	}
	if resBaseUnitID != nil {
		result["base_unit_id"] = *resBaseUnitID
	}
	if resCreatedAt.Valid {
		result["created_at"] = resCreatedAt.Time
	}

	response.Success(c, result)
}

// DeleteUnitOfMeasure deletes a unit of measure
func (h *Handler) DeleteUnitOfMeasure(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	// Check if UOM is in use by products
	var inUse bool
	err = h.db.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM products
			WHERE tenant_id = $1 AND (unit_id = $2 OR purchase_unit_id = $2 OR sales_unit_id = $2)
		)`,
		tenantID, id,
	).Scan(&inUse)
	if err == nil && inUse {
		response.BadRequest(c, "Cannot delete: this unit is assigned to one or more products")
		return
	}

	result, err := h.db.Exec(
		"DELETE FROM units_of_measure WHERE id = $1 AND tenant_id = $2",
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete unit of measure", "error", err)
		response.InternalError(c, "Failed to delete unit of measure")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Unit of measure not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Unit of measure deleted"})
}
