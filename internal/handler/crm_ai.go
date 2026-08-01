package handler

// CRM AI endpoints (CRM v2). Both degrade gracefully when the tenant has no
// AI provider configured (ai_configured=false, nothing breaks) and never
// auto-save — a human confirms every suggestion.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxLeadTextChars = 6000

// ExtractLeadFromText — POST /leads/ai/extract {"text": "..."}.
// Paste a Telegram message / free text; returns field suggestions with
// confidences for the lead form (name, phone, company, need, amount).
func (h *Handler) ExtractLeadFromText(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	var input struct {
		Text string `json:"text" binding:"required,min=3"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "text is required")
		return
	}
	text := input.Text
	if len(text) > maxLeadTextChars {
		text = text[:maxLeadTextChars]
	}

	systemPrompt := `You extract CRM lead data from free-form text (Telegram messages, call notes — usually Uzbek or Russian). ` +
		`Respond with ONLY a JSON object, no prose. Schema:
{
  "contact_name": {"value": "string|null", "confidence": 0.0},
  "company_name": {"value": "string|null", "confidence": 0.0},
  "phone": {"value": "string|null", "confidence": 0.0},
  "email": {"value": "string|null", "confidence": 0.0},
  "need": {"value": "string|null", "confidence": 0.0},
  "amount": {"value": number|null, "confidence": 0.0},
  "currency": {"value": "UZS|USD|null", "confidence": 0.0},
  "source": {"value": "telegram|website|referral|cold_call|other|null", "confidence": 0.0}
}
"need" is a one-sentence summary in Uzbek of what the client wants.
Amounts written like "50 mln" mean 50000000 UZS. Use null when absent. confidence is 0..1.`

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.config.AI.RequestTimeout)
	defer cancel()

	raw, configured, aiErr := h.contractAIChat(ctx, tenantID, systemPrompt, "Text:\n\n"+text)
	if !configured {
		response.Success(c, gin.H{"ai_configured": false, "suggestions": nil})
		return
	}
	if aiErr != nil {
		h.log.Error("Lead AI extraction failed", "error", aiErr, "tenant_id", tenantID)
		response.Success(c, gin.H{"ai_configured": true, "suggestions": nil, "reason": "ai_unavailable"})
		return
	}
	var suggestions map[string]interface{}
	if err := json.Unmarshal([]byte(stripCodeFences(raw)), &suggestions); err != nil {
		h.log.Error("Lead AI extraction returned non-JSON", "error", err, "tenant_id", tenantID)
		response.Success(c, gin.H{"ai_configured": true, "suggestions": nil, "reason": "ai_bad_response"})
		return
	}
	response.Success(c, gin.H{"ai_configured": true, "suggestions": suggestions})
}

// SuggestLeadNextStep — POST /leads/:id/ai/next-step.
// Drafts a short follow-up message in Uzbek the manager can copy ("Keyingi
// qadam" hint for stale leads).
func (h *Handler) SuggestLeadNextStep(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}

	var contactName, companyName, stageName, source, notes string
	var amount sql.NullFloat64
	var currency string
	var lastActivity sql.NullTime
	var createdAt time.Time
	err = h.db.QueryRow(`
		SELECT l.contact_name, COALESCE(l.company_name, ''),
		       COALESCE(COALESCE(ps.custom_name, ps.name), l.status::text),
		       COALESCE(l.source::text, ''), COALESCE(l.notes, ''),
		       l.expected_value, COALESCE(l.currency, 'UZS'),
		       l.last_activity_at, l.created_at
		FROM leads l
		LEFT JOIN pipeline_stages ps ON ps.id = l.stage_id
		WHERE l.id = $1 AND l.tenant_id = $2 AND l.deleted_at IS NULL
	`, id, tenantID).Scan(&contactName, &companyName, &stageName, &source, &notes, &amount, &currency, &lastActivity, &createdAt)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Lead")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load lead")
		return
	}

	daysSilent := int(time.Since(createdAt).Hours() / 24)
	if lastActivity.Valid {
		daysSilent = int(time.Since(lastActivity.Time).Hours() / 24)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Lid: %s", contactName)
	if companyName != "" {
		fmt.Fprintf(&sb, " (%s)", companyName)
	}
	fmt.Fprintf(&sb, "\nBosqich: %s\nManba: %s\nJim davr: %d kun", stageName, source, daysSilent)
	if amount.Valid && amount.Float64 > 0 {
		fmt.Fprintf(&sb, "\nSumma: %.0f %s", amount.Float64, currency)
	}
	if notes != "" {
		n := notes
		if len(n) > 1500 {
			n = n[:1500]
		}
		fmt.Fprintf(&sb, "\nIzohlar: %s", n)
	}

	systemPrompt := `Sen qurilish sohasidagi kichik biznes uchun savdo yordamchisisan. ` +
		`Berilgan lid ma'lumotidan kelib chiqib, menejer mijozga yuborishi mumkin bo'lgan QISQA follow-up xabar yoz (o'zbek tilida, samimiy, 2-4 jumla, sotuvga undovchi lekin bosim o'tkazmaydigan). ` +
		`Javobingda FAQAT JSON qaytar: {"next_step": "menejerga qisqa tavsiya (1 jumla, o'zbekcha)", "message": "mijozga yuboriladigan tayyor xabar"}`

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.config.AI.RequestTimeout)
	defer cancel()

	raw, configured, aiErr := h.contractAIChat(ctx, tenantID, systemPrompt, sb.String())
	if !configured {
		response.Success(c, gin.H{"ai_configured": false})
		return
	}
	if aiErr != nil {
		h.log.Error("Lead next-step AI failed", "error", aiErr, "tenant_id", tenantID)
		response.Success(c, gin.H{"ai_configured": true, "reason": "ai_unavailable"})
		return
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(stripCodeFences(raw)), &out); err != nil {
		// model answered in prose — still useful, return as message
		out = map[string]interface{}{"message": strings.TrimSpace(raw)}
	}
	response.Success(c, gin.H{"ai_configured": true, "suggestion": out})
}
