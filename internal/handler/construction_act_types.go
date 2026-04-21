package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION ACT TYPES — user-manageable act type list
// =====================================================

// Default system act types seeded on first access per tenant
var defaultActTypes = []struct {
	Value     string
	Label     string
	Color     string
	SortOrder int
}{
	{"acceptance", "Qabul qilish", "bg-green-100 text-green-700", 1},
	{"defect", "Nuqson", "bg-red-100 text-red-700", 2},
	{"ks2", "Forma 2", "bg-blue-100 text-blue-700", 3},
	{"ks3", "Forma 3", "bg-purple-100 text-purple-700", 4},
	{"hidden_work", "Yashirin ish", "bg-amber-100 text-amber-700", 5},
}

// ListConstructionActTypes returns all act types for the tenant, seeding defaults if empty
func (h *Handler) ListConstructionActTypes(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Check if tenant has any act types; if not, seed defaults
	var count int
	err := h.db.QueryRow(`SELECT COUNT(*) FROM construction_act_types WHERE tenant_id = $1`, tenantID).Scan(&count)
	if err != nil {
		h.log.Error("Failed to count act types", "error", err)
		response.InternalError(c, "Failed to load act types")
		return
	}

	if count == 0 {
		for _, dt := range defaultActTypes {
			_, err := h.db.Exec(
				`INSERT INTO construction_act_types (tenant_id, value, label, color, is_system, sort_order)
				 VALUES ($1, $2, $3, $4, true, $5)
				 ON CONFLICT (tenant_id, value) DO UPDATE SET is_system = true`,
				tenantID, dt.Value, dt.Label, dt.Color, dt.SortOrder,
			)
			if err != nil {
				h.log.Error("Failed to seed act type", "error", err, "value", dt.Value)
			}
		}
	}

	rows, err := h.db.Query(
		`SELECT id, value, label, color, is_system, sort_order, created_at
		 FROM construction_act_types
		 WHERE tenant_id = $1
		 ORDER BY sort_order, id`,
		tenantID,
	)
	if err != nil {
		h.log.Error("Failed to list act types", "error", err)
		response.InternalError(c, "Failed to list act types")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var value, label, color string
		var isSystem bool
		var sortOrder int
		var createdAt time.Time

		if err := rows.Scan(&id, &value, &label, &color, &isSystem, &sortOrder, &createdAt); err != nil {
			h.log.Error("Failed to scan act type", "error", err)
			continue
		}
		items = append(items, map[string]interface{}{
			"id":         id,
			"value":      value,
			"label":      label,
			"color":      color,
			"is_system":  isSystem,
			"sort_order": sortOrder,
			"created_at": createdAt,
		})
	}

	response.Success(c, items)
}

// CreateConstructionActType creates a new custom act type
func (h *Handler) CreateConstructionActType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var req struct {
		Label string `json:"label"`
		Color string `json:"color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		response.BadRequest(c, "Label is required")
		return
	}
	if len(req.Label) > 100 {
		response.BadRequest(c, "Label must be 100 characters or less")
		return
	}

	// Generate a slug-like value from the label
	value := generateActTypeValue(req.Label)

	if req.Color == "" {
		req.Color = "bg-slate-100 text-slate-700"
	}

	// Get next sort order
	var maxSort int
	_ = h.db.QueryRow(
		`SELECT COALESCE(MAX(sort_order), 0) FROM construction_act_types WHERE tenant_id = $1`,
		tenantID,
	).Scan(&maxSort)

	var id int64
	err := h.db.QueryRow(
		`INSERT INTO construction_act_types (tenant_id, value, label, color, is_system, sort_order)
		 VALUES ($1, $2, $3, $4, false, $5)
		 RETURNING id`,
		tenantID, value, req.Label, req.Color, maxSort+1,
	).Scan(&id)

	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			response.Error(c, http.StatusConflict, "CONFLICT", "Act type with this name already exists")
			return
		}
		h.log.Error("Failed to create act type", "error", err)
		response.InternalError(c, "Failed to create act type")
		return
	}

	response.Success(c, map[string]interface{}{
		"id":        id,
		"value":     value,
		"label":     req.Label,
		"color":     req.Color,
		"is_system": false,
	})
}

// DeleteConstructionActType deletes a custom (non-system) act type
func (h *Handler) DeleteConstructionActType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid act type ID")
		return
	}

	// Don't allow deleting system types
	var isSystem bool
	err = h.db.QueryRow(
		`SELECT is_system FROM construction_act_types WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(&isSystem)
	if err != nil {
		response.NotFound(c, "Act type not found")
		return
	}
	if isSystem {
		response.BadRequest(c, "Cannot delete system act types")
		return
	}

	// Check if any acts use this type
	var value string
	_ = h.db.QueryRow(`SELECT value FROM construction_act_types WHERE id = $1`, id).Scan(&value)

	var usageCount int
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM construction_act WHERE act_type = $1 AND tenant_id = $2`,
		value, tenantID,
	).Scan(&usageCount)
	if usageCount > 0 {
		response.BadRequest(c, fmt.Sprintf("Cannot delete: %d acts are using this type", usageCount))
		return
	}

	_, err = h.db.Exec(
		`DELETE FROM construction_act_types WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete act type", "error", err)
		response.InternalError(c, "Failed to delete act type")
		return
	}

	response.Success(c, map[string]interface{}{"deleted": true})
}

// generateActTypeValue creates a URL-safe slug from a label (supports Cyrillic/non-Latin)
func generateActTypeValue(label string) string {
	return generateCodeFromNameLower(label, 100, "custom")
}
