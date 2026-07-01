package handler

import (
	"database/sql"
	"strings"

	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/infrastructure/ai"
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
	Endpoint   string `json:"endpoint"`
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

	var provider, model, apiKey, endpoint string
	err := h.db.QueryRow(
		`SELECT provider, model, api_key, endpoint FROM tenant_ai_settings WHERE tenant_id = $1`,
		tenantID,
	).Scan(&provider, &model, &apiKey, &endpoint)
	if err == sql.ErrNoRows {
		// No tenant override yet — surface the env defaults so the UI can prefill
		// the provider/model/endpoint fields (no key is exposed).
		response.Success(c, tenantAISettingsResp{
			Provider:   h.config.AI.Provider,
			Model:      h.config.AI.Model,
			Endpoint:   h.config.AI.Endpoint,
			HasKey:     false,
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
		Endpoint:   endpoint,
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
		Endpoint string `json:"endpoint"`
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
	endpoint := strings.TrimSpace(in.Endpoint)
	// Reject a non-HTTPS endpoint up front so tenant data is never sent in the
	// clear (same rule the AI client enforces at request time).
	if err := ai.RequireSecureEndpoint(endpoint); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	newKey := strings.TrimSpace(in.APIKey)
	if in.ClearKey {
		newKey = ""
	}

	// Upsert. The api_key CASE keeps the existing value when no new key is given
	// and clear_key is false, so editing the model never wipes the secret.
	_, err := h.db.Exec(`
		INSERT INTO tenant_ai_settings (tenant_id, provider, model, endpoint, api_key, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		ON CONFLICT (tenant_id) DO UPDATE SET
			provider = EXCLUDED.provider,
			model    = EXCLUDED.model,
			endpoint = EXCLUDED.endpoint,
			api_key  = CASE
			             WHEN $6 THEN ''
			             WHEN EXCLUDED.api_key <> '' THEN EXCLUDED.api_key
			             ELSE tenant_ai_settings.api_key
			           END,
			updated_at = NOW()
	`, tenantID, provider, model, endpoint, newKey, in.ClearKey)
	if err != nil {
		h.log.Error("Failed to save tenant AI settings", "error", err)
		response.InternalError(c, "Failed to save AI settings")
		return
	}

	response.Success(c, gin.H{"message": "saved"})
}

// resolveTenantAIConfig returns the full AI config to use for a tenant. When the
// tenant has saved their OWN key it uses the tenant's provider/key/model/endpoint
// (model/endpoint fall back to the env values only when the tenant left them
// blank). When the tenant has no key it falls back entirely to the server-wide
// env config (h.config.AI) — preserving the existing global behaviour. Non-AI
// knobs (max tokens, temperature, timeout, rate limit) always come from env.
func (h *Handler) resolveTenantAIConfig(tenantID uuid.UUID) (provider, apiKey, model, endpoint string) {
	provider = h.config.AI.Provider
	apiKey = h.config.AI.APIKey
	model = h.config.AI.Model
	endpoint = h.config.AI.Endpoint

	if tenantID == uuid.Nil {
		return provider, apiKey, model, endpoint
	}

	var p, m, k, e string
	err := h.db.QueryRow(
		`SELECT provider, model, api_key, endpoint FROM tenant_ai_settings WHERE tenant_id = $1`,
		tenantID,
	).Scan(&p, &m, &k, &e)
	if err == nil && strings.TrimSpace(k) != "" {
		provider = p
		apiKey = k
		if strings.TrimSpace(m) != "" {
			model = m
		}
		// Endpoint comes from the tenant when set; when blank we DON'T inherit the
		// env (Gemini-compat) endpoint, so their own OpenAI/Anthropic key hits that
		// provider's default URL instead of the server's compat endpoint.
		endpoint = strings.TrimSpace(e)
	}
	return provider, apiKey, model, endpoint
}

// tenantAIConfig returns the env AI config overlaid with the tenant's overrides.
func (h *Handler) tenantAIConfig(tenantID uuid.UUID) config.AIConfig {
	cfg := h.config.AI // copy — keeps max tokens, temperature, timeout, rate limit
	cfg.Provider, cfg.APIKey, cfg.Model, cfg.Endpoint = h.resolveTenantAIConfig(tenantID)
	return cfg
}
