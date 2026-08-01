package handler

// Tenant-level valuation (tannarx usuli) setting — docs/ombor-changelog.md
// follow-up #3. Stored under the `inventory_valuation` key of the
// tenant_settings JSONB blob (same storage as admin settings, written with
// jsonb_set so other keys are never clobbered).
//
// Semantics:
//   - "aveco" (default): applyStockDelta maintains inventory.unit_cost as a
//     quantity-weighted average on receipts; issues consume at that average.
//   - "fifo": outbound sales deliveries drain inventory_lots oldest-first
//     and value COGS at the weighted cost of the consumed lots
//     (sales_delivery.go); other issue paths fall back to the stored
//     average.
//
// Changing the method NEVER rewrites history: the change is recorded with
// effective_from = today and appended to a history array; only postings
// made after the change follow the new method.

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type inventoryValuationSetting struct {
	Method        string                   `json:"method"` // aveco | fifo
	EffectiveFrom string                   `json:"effective_from,omitempty"`
	ChangedBy     string                   `json:"changed_by,omitempty"`
	ChangedAt     string                   `json:"changed_at,omitempty"`
	History       []map[string]interface{} `json:"history,omitempty"`
}

func (h *Handler) readValuationSetting(tenantID uuid.UUID) inventoryValuationSetting {
	setting := inventoryValuationSetting{Method: "aveco"}
	var raw []byte
	err := h.db.QueryRow(`
		SELECT settings->'inventory_valuation' FROM tenant_settings WHERE tenant_id = $1
	`, tenantID).Scan(&raw)
	if err == nil && len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &setting)
		if setting.Method != "fifo" {
			setting.Method = "aveco"
		}
	}
	return setting
}

// GetInventoryValuationSettings — GET /inventory/valuation-settings
func (h *Handler) GetInventoryValuationSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	response.Success(c, h.readValuationSetting(tenantID))
}

// UpdateInventoryValuationSettings — PUT /inventory/valuation-settings
func (h *Handler) UpdateInventoryValuationSettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input struct {
		Method string `json:"method" binding:"required,oneof=aveco fifo"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "method must be 'aveco' or 'fifo'")
		return
	}

	current := h.readValuationSetting(tenantID)
	if current.Method == input.Method {
		response.Success(c, current)
		return
	}

	now := time.Now()
	next := inventoryValuationSetting{
		Method:        input.Method,
		EffectiveFrom: now.Format("2006-01-02"),
		ChangedBy:     userID.String(),
		ChangedAt:     now.Format(time.RFC3339),
		History: append(current.History, map[string]interface{}{
			"method":     current.Method,
			"applied_to": now.Format("2006-01-02"),
		}),
	}
	nextJSON, err := json.Marshal(next)
	if err != nil {
		response.InternalError(c, "Failed to save setting")
		return
	}

	_, err = h.db.Exec(`
		INSERT INTO tenant_settings (tenant_id, settings, updated_at, updated_by)
		VALUES ($1, jsonb_build_object('inventory_valuation', $2::jsonb), $3, $4)
		ON CONFLICT (tenant_id) DO UPDATE SET
			settings   = jsonb_set(COALESCE(tenant_settings.settings, '{}'::jsonb), '{inventory_valuation}', $2::jsonb),
			updated_at = EXCLUDED.updated_at,
			updated_by = EXCLUDED.updated_by
	`, tenantID, nextJSON, now, nilIfZeroUUID(userID))
	if err != nil {
		h.log.Error("Failed to save inventory valuation setting", "error", err)
		response.InternalError(c, "Failed to save setting")
		return
	}
	response.Success(c, next)
}

// tenantUsesFIFO reports whether the tenant has switched valuation to FIFO.
// Callers on hot paths get one cheap indexed lookup; sql.ErrNoRows = default
// AVECO.
func (h *Handler) tenantUsesFIFO(q dbExecQuerier, tenantID uuid.UUID) bool {
	var method sql.NullString
	_ = q.QueryRow(`
		SELECT settings->'inventory_valuation'->>'method' FROM tenant_settings WHERE tenant_id = $1
	`, tenantID).Scan(&method)
	return method.Valid && method.String == "fifo"
}
