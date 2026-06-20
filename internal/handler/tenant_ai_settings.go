package handler

import (
	"database/sql"
	"strings"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Per-tenant AI provider settings (user-supplied API key + model). Lets a tenant
// plug in their own OpenAI / Anthropic key for AI features (e.g. the
// purchase-receipt scanner) instead of relying on the server-wide env config.
//
// Security: the raw api_key is NEVER returned to the client. GET only exposes a
// masked preview and a has_key flag.

// maskAPIKey returns a safe preview of a secret (e.g. "sk-…AB12"), never the key.
func maskAPIKey(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return strings.Repeat("•", len(k))
	}
	return k[:3] + "…" + k[len(k)-4:]
}

type tenantAISettingsResp struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	HasKey     bool   `json:"has_key"`
	KeyPreview string `json:"key_preview"`
}

// GetTenantAISettings returns the tenant's AI settings without the raw key.
// GET /admin/ai-settings
func (h *Handler) GetTenantAISettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var provider, model, apiKey string
	err := h.db.QueryRow(
		`SELECT provider, model, api_key FROM tenant_ai_settings WHERE tenant_id = $1`,
		tenantID,
	).Scan(&provider, &model, &apiKey)
	if err == sql.ErrNoRows {
		// No tenant override yet — surface the env defaults so the UI can prefill
		// the provider/model dropdowns (no key is exposed).
		response.Success(c, tenantAISettingsResp{
			Provider: h.config.AI.Provider,
			Model:    h.config.AI.Model,
			HasKey:   false,
			KeyPreview: "",
		})
		return
	}
	if err != nil {
		h.log.Error("Failed to load tenant AI settings", "error", err)
		response.InternalError(c, "Failed to load AI settings")
		return
	}

	response.Success(c, tenantAISettingsResp{
		Provider:   provider,
		Model:      model,
		HasKey:     strings.TrimSpace(apiKey) != "",
		KeyPreview: maskAPIKey(apiKey),
	})
}

// UpdateTenantAISettings upserts provider/model and (optionally) the API key.
//   - api_key empty           → keep the existing stored key (lets the user edit
//     the model without re-entering the secret)
//   - api_key non-empty       → replace the stored key
//   - clear_key = true        → wipe the stored key
//
// PUT /admin/ai-settings
func (h *Handler) UpdateTenantAISettings(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var in struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		APIKey   string `json:"api_key"`
		ClearKey bool   `json:"clear_key"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider != "anthropic" {
		provider = "openai"
	}
	model := strings.TrimSpace(in.Model)
	newKey := strings.TrimSpace(in.APIKey)
	if in.ClearKey {
		newKey = ""
	}

	// Upsert. The api_key CASE keeps the existing value when no new key is given
	// and clear_key is false, so editing the model never wipes the secret.
	_, err := h.db.Exec(`
		INSERT INTO tenant_ai_settings (tenant_id, provider, model, api_key, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (tenant_id) DO UPDATE SET
			provider = EXCLUDED.provider,
			model    = EXCLUDED.model,
			api_key  = CASE
			             WHEN $5 THEN ''
			             WHEN EXCLUDED.api_key <> '' THEN EXCLUDED.api_key
			             ELSE tenant_ai_settings.api_key
			           END,
			updated_at = NOW()
	`, tenantID, provider, model, newKey, in.ClearKey)
	if err != nil {
		h.log.Error("Failed to save tenant AI settings", "error", err)
		response.InternalError(c, "Failed to save AI settings")
		return
	}

	response.Success(c, gin.H{"message": "saved"})
}

// resolveTenantAIConfig returns the AI provider/key/model to use for a tenant.
// It uses the tenant's saved settings when a key is present, otherwise falls
// back to the server-wide env config (h.config.AI). The model falls back to the
// env model only when the tenant left it blank.
func (h *Handler) resolveTenantAIConfig(tenantID uuid.UUID) (provider, apiKey, model string) {
	provider = h.config.AI.Provider
	apiKey = h.config.AI.APIKey
	model = h.config.AI.Model

	if tenantID == uuid.Nil {
		return provider, apiKey, model
	}

	var p, m, k string
	err := h.db.QueryRow(
		`SELECT provider, model, api_key FROM tenant_ai_settings WHERE tenant_id = $1`,
		tenantID,
	).Scan(&p, &m, &k)
	if err == nil && strings.TrimSpace(k) != "" {
		provider = p
		apiKey = k
		if strings.TrimSpace(m) != "" {
			model = m
		}
	}
	return provider, apiKey, model
}
