package handler

// POST /ai/transcribe — server-side speech-to-text for the assistant's voice
// input. The frontend has shipped a mic that POSTs here since the first AI
// page, but the endpoint never existed (audit C13) — every recording 404'd.
//
// Transcription is an OpenAI (Whisper) capability; Anthropic has no
// transcription API. Key resolution: the tenant's own key when their provider
// is openai, else the server-wide env key when THAT is openai, else 501 with a
// clear message the UI can show.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

const (
	transcribeMaxBytes = 15 << 20 // 15 MB (Whisper hard limit is 25 MB)
	whisperEndpoint    = "https://api.openai.com/v1/audio/transcriptions"
)

// TranscribeAudio accepts multipart {file, language?} and returns {text}.
func (h *Handler) TranscribeAudio(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	if h.aiQuotaExceeded(c, tenantID) {
		return
	}

	// Resolve an OpenAI key: tenant's own if their provider is openai,
	// otherwise the server env config if it is openai.
	cfg := h.tenantAIConfig(tenantID)
	if cfg.Provider != "openai" || cfg.APIKey == "" {
		cfg = h.config.AI
	}
	if cfg.Provider != "openai" || cfg.APIKey == "" {
		c.JSON(http.StatusNotImplemented, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "transcription_unavailable",
				"message": "Ovozli kiritish uchun OpenAI (Whisper) kaliti kerak — Admin → AI sozlamalarida openai provayderini ulang.",
			},
		})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file is required (multipart)")
		return
	}
	if fileHeader.Size > transcribeMaxBytes {
		response.BadRequest(c, "Audio 15 MB dan katta bo'lmasligi kerak")
		return
	}
	src, err := fileHeader.Open()
	if err != nil {
		response.InternalError(c, "Failed to read audio")
		return
	}
	defer src.Close()

	// Re-encode as multipart for Whisper.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", fileHeader.Filename)
	if err == nil {
		_, err = io.Copy(part, io.LimitReader(src, transcribeMaxBytes))
	}
	if err != nil {
		response.InternalError(c, "Failed to buffer audio")
		return
	}
	_ = mw.WriteField("model", "whisper-1")
	if lang := c.PostForm("language"); lang != "" && len(lang) <= 5 {
		_ = mw.WriteField("language", lang)
	}
	_ = mw.Close()

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", whisperEndpoint, &buf)
	if err != nil {
		response.InternalError(c, "Failed to build request")
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: h.config.AI.RequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		h.log.Error("transcribe: request failed", "error", err)
		response.InternalError(c, "Transkripsiya xizmatiga ulanib bo'lmadi")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		h.log.Warn("transcribe: provider error", "status", resp.Status, "body", string(body))
		response.BadRequest(c, fmt.Sprintf("Transkripsiya xatosi (%s)", resp.Status))
		return
	}

	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		response.InternalError(c, "Javobni o'qib bo'lmadi")
		return
	}

	userID, _ := middleware.GetUserID(c)
	h.logAIUsage(tenantID.String(), userID.String(), "transcribe", "whisper-1", 0, 0)

	response.Success(c, gin.H{"text": out.Text})
}
