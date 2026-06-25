package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TranscribeAudio accepts an uploaded audio clip and returns its transcription.
// It supports OpenAI Whisper (multipart) and Google Gemini (native
// generateContent with inline audio). The audio is streamed straight to the
// provider and is NOT stored on disk.
func (h *Handler) TranscribeAudio(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}
	if h.config.AI.APIKey == "" {
		response.BadRequest(c, "AI provider is not configured")
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "No audio file provided")
		return
	}
	defer file.Close()

	const maxAudio = 25 * 1024 * 1024
	if header.Size > maxAudio {
		response.BadRequest(c, "Audio file too large (max 25MB)")
		return
	}

	audio, err := io.ReadAll(file)
	if err != nil {
		response.InternalError(c, "Failed to read audio")
		return
	}
	language := c.PostForm("language") // optional: uz / ru / en

	mimeType := header.Header.Get("Content-Type")
	if i := strings.Index(mimeType, ";"); i >= 0 {
		mimeType = mimeType[:i]
	}
	if mimeType == "" {
		mimeType = "audio/webm"
	}

	isGemini := strings.Contains(h.config.AI.Endpoint, "generativelanguage.googleapis.com") ||
		strings.Contains(h.config.AI.TranscribeEndpoint, "generativelanguage.googleapis.com") ||
		strings.EqualFold(h.config.AI.Provider, "gemini") ||
		strings.EqualFold(h.config.AI.Provider, "google") ||
		strings.Contains(strings.ToLower(h.config.AI.Model), "gemini")

	if strings.EqualFold(h.config.AI.Provider, "anthropic") {
		response.BadRequest(c, "Audio transcription is not supported by the configured provider")
		return
	}

	if isGemini {
		h.transcribeWithGemini(c, audio, mimeType, language)
		return
	}
	h.transcribeWithOpenAI(c, audio, header.Filename, language)
}

// transcribeWithGemini uses Gemini's native generateContent with inline audio.
func (h *Handler) transcribeWithGemini(c *gin.Context, audio []byte, mimeType, language string) {
	model := h.config.AI.TranscribeModel
	if model == "" || strings.EqualFold(model, "whisper-1") {
		if strings.Contains(strings.ToLower(h.config.AI.Model), "gemini") {
			model = h.config.AI.Model
		} else {
			model = "gemini-1.5-flash"
		}
	}

	prompt := "Transcribe the following audio recording to plain text. Output ONLY the exact transcription with no extra words, labels, or commentary."
	if language != "" {
		prompt += " Spoken language hint: " + language + "."
	}

	reqBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]interface{}{
				{"text": prompt},
				{"inline_data": map[string]interface{}{
					"mime_type": mimeType,
					"data":      base64.StdEncoding.EncodeToString(audio),
				}},
			}},
		},
		"generationConfig": map[string]interface{}{"temperature": 0},
	}
	body, _ := json.Marshal(reqBody)

	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		model, url.QueryEscape(h.config.AI.APIKey))

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		response.InternalError(c, "Failed to build transcription request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: h.config.AI.RequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		h.log.Error("Gemini transcription request failed", "error", err)
		response.InternalError(c, "Transcription service unavailable")
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		h.log.Error("Gemini transcription error", "status", resp.StatusCode, "model", model, "mime", mimeType, "body", string(respBody))
		response.InternalError(c, "Transcription failed (provider returned "+http.StatusText(resp.StatusCode)+")")
		return
	}

	var gr struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &gr); err != nil {
		response.InternalError(c, "Failed to parse transcription")
		return
	}
	var text string
	if len(gr.Candidates) > 0 {
		for _, p := range gr.Candidates[0].Content.Parts {
			text += p.Text
		}
	}
	response.Success(c, gin.H{"text": strings.TrimSpace(text)})
}

// transcribeWithOpenAI uses the OpenAI Whisper multipart endpoint.
func (h *Handler) transcribeWithOpenAI(c *gin.Context, audio []byte, filename, language string) {
	model := h.config.AI.TranscribeModel
	if model == "" {
		model = "whisper-1"
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		response.InternalError(c, "Failed to prepare audio")
		return
	}
	fw.Write(audio)
	_ = w.WriteField("model", model)
	if language != "" {
		_ = w.WriteField("language", language)
	}
	w.Close()

	endpoint := h.config.AI.TranscribeEndpoint
	if endpoint == "" {
		endpoint = h.config.AI.Endpoint
		switch {
		case endpoint == "":
			endpoint = "https://api.openai.com/v1/audio/transcriptions"
		case strings.Contains(endpoint, "/chat/completions"):
			endpoint = strings.Replace(endpoint, "/chat/completions", "/audio/transcriptions", 1)
		case !strings.Contains(endpoint, "/audio/transcriptions"):
			endpoint = strings.TrimRight(endpoint, "/") + "/audio/transcriptions"
		}
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", endpoint, &buf)
	if err != nil {
		response.InternalError(c, "Failed to build transcription request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+h.config.AI.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: h.config.AI.RequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		h.log.Error("Transcription request failed", "error", err)
		response.InternalError(c, "Transcription service unavailable")
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		h.log.Error("Transcription API error", "status", resp.StatusCode, "endpoint", endpoint, "model", model, "body", string(respBody))
		response.InternalError(c, "Transcription failed (provider returned "+http.StatusText(resp.StatusCode)+")")
		return
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		response.InternalError(c, "Failed to parse transcription")
		return
	}
	response.Success(c, gin.H{"text": parsed.Text})
}
