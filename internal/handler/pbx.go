package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// OnlinePBX INTEGRATION
// =====================================================

// PBXConfig represents the PBX configuration for a tenant
type PBXConfig struct {
	Enabled      bool   `json:"enabled"`
	Provider     string `json:"provider"`      // "onlinepbx"
	Domain       string `json:"domain"`        // e.g., "pbx36019.onpbx.ru"
	APIKey       string `json:"api_key"`
	Extension    string `json:"extension"`     // Default extension
	CallerID     string `json:"caller_id"`
	WebhookToken string `json:"webhook_token"` // Token for verifying webhook calls (set in OnlinePBX panel)
}

// onlinePBXAuth handles authentication with OnlinePBX API
type onlinePBXAuth struct {
	KeyID string
	Key   string
}

func (h *Handler) authenticateOnlinePBX(domain, apiKey string) (*onlinePBXAuth, error) {
	authURL := fmt.Sprintf("https://api2.onlinepbx.ru/%s/auth.json", domain)

	formData := fmt.Sprintf("auth_key=%s", apiKey)
	req, err := http.NewRequest("POST", authURL, strings.NewReader(formData))
	if err != nil {
		return nil, fmt.Errorf("failed to create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth request failed: %w", err)
	}
	defer resp.Body.Close()

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode auth response: %w", err)
	}

	// Status can be bool (true) or string ("1"/"0")
	statusOK := false
	if s, ok := raw["status"]; ok {
		str := strings.Trim(string(s), "\"")
		statusOK = str == "true" || str == "1"
	}

	if !statusOK {
		comment := ""
		if c, ok := raw["comment"]; ok {
			comment = strings.Trim(string(c), "\"")
		}
		if comment != "" {
			return nil, fmt.Errorf("authentication failed: %s", comment)
		}
		return nil, fmt.Errorf("authentication failed")
	}

	var data struct {
		KeyID string `json:"key_id"`
		Key   string `json:"key"`
	}
	if d, ok := raw["data"]; ok {
		json.Unmarshal(d, &data)
	}

	return &onlinePBXAuth{
		KeyID: data.KeyID,
		Key:   data.Key,
	}, nil
}

// GetPBXConfig returns the PBX configuration for the current tenant
func (h *Handler) GetPBXConfig(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	config, err := h.getPBXConfigFromDB(tenantID)
	if err != nil {
		// Return empty config if not found
		response.Success(c, PBXConfig{})
		return
	}

	// Mask secrets for security
	if config.APIKey != "" {
		config.APIKey = "***" + config.APIKey[max(0, len(config.APIKey)-4):]
	}
	if config.WebhookToken != "" {
		config.WebhookToken = "***" + config.WebhookToken[max(0, len(config.WebhookToken)-4):]
	}

	response.Success(c, config)
}

// SavePBXConfig saves the PBX configuration for the current tenant
func (h *Handler) SavePBXConfig(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var config PBXConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// If secrets are masked, keep the old values
	if strings.HasPrefix(config.APIKey, "***") || strings.HasPrefix(config.WebhookToken, "***") {
		oldConfig, err := h.getPBXConfigFromDB(tenantID)
		if err == nil {
			if strings.HasPrefix(config.APIKey, "***") {
				config.APIKey = oldConfig.APIKey
			}
			if strings.HasPrefix(config.WebhookToken, "***") {
				config.WebhookToken = oldConfig.WebhookToken
			}
		}
	}

	configJSON, _ := json.Marshal(config)

	query := `
		INSERT INTO tenant_settings (tenant_id, settings, created_at, updated_at)
		VALUES ($1, jsonb_set(COALESCE('{}', '{}')::jsonb, '{pbx}', $2::jsonb), NOW(), NOW())
		ON CONFLICT (tenant_id) DO UPDATE
		SET settings = jsonb_set(COALESCE(tenant_settings.settings, '{}')::jsonb, '{pbx}', $2::jsonb),
		    updated_at = NOW()
	`

	_, err := h.db.Exec(query, tenantID, string(configJSON))
	if err != nil {
		h.log.Error("Failed to save PBX config", "error", err)
		response.InternalError(c, "Failed to save PBX configuration")
		return
	}

	response.Success(c, gin.H{"message": "PBX configuration saved"})
}

// TestPBXConnection tests the connection to the PBX provider
func (h *Handler) TestPBXConnection(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input struct {
		Domain string `json:"domain"`
		APIKey string `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// If API key is masked, use saved one
	if strings.HasPrefix(input.APIKey, "***") {
		config, err := h.getPBXConfigFromDB(tenantID)
		if err == nil {
			input.APIKey = config.APIKey
		}
	}

	auth, err := h.authenticateOnlinePBX(input.Domain, input.APIKey)
	if err != nil {
		h.log.Error("PBX authentication failed", "error", err)
		response.Success(c, gin.H{"connected": false, "error": "Authentication failed"})
		return
	}

	response.Success(c, gin.H{"connected": true, "key_id": auth.KeyID})
}

// InitiateCall initiates an outbound call via OnlinePBX
func (h *Handler) InitiateCall(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)

	var input struct {
		Phone      string `json:"phone" binding:"required"`
		Extension  string `json:"extension"`
		ContactID  string `json:"contact_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	config, err := h.getPBXConfigFromDB(tenantID)
	if err != nil || !config.Enabled {
		response.BadRequest(c, "PBX is not configured")
		return
	}

	// Authenticate with OnlinePBX
	auth, err := h.authenticateOnlinePBX(config.Domain, config.APIKey)
	if err != nil {
		h.log.Error("OnlinePBX auth failed", "error", err)
		response.InternalError(c, "Failed to authenticate with PBX")
		return
	}

	// Use provided extension or default
	fromExt := input.Extension
	if fromExt == "" {
		fromExt = config.Extension
	}

	// Clean phone number — OnlinePBX only accepts digits (no +, spaces, dashes, parens)
	cleanPhone := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, input.Phone)

	// Initiate call via OnlinePBX (form-encoded, not JSON)
	h.log.Info("OnlinePBX call initiation", "from_ext", fromExt, "to_phone", input.Phone, "clean_phone", cleanPhone, "input_ext", input.Extension, "config_ext", config.Extension, "domain", config.Domain)
	callURL := fmt.Sprintf("https://api2.onlinepbx.ru/%s/call/now.json", config.Domain)
	callFormData := fmt.Sprintf("from=%s&to=%s", fromExt, cleanPhone)

	req, _ := http.NewRequest("POST", callURL, strings.NewReader(callFormData))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("x-pbx-authentication", fmt.Sprintf("%s:%s", auth.KeyID, auth.Key))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		h.log.Error("OnlinePBX call initiation failed", "error", err)
		response.InternalError(c, "Failed to initiate call")
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	h.log.Info("OnlinePBX call response", "status_code", resp.StatusCode, "body", string(respBody))

	var callRaw map[string]json.RawMessage
	json.Unmarshal(respBody, &callRaw)

	callStatusOK := false
	if s, ok := callRaw["status"]; ok {
		str := strings.Trim(string(s), "\"")
		callStatusOK = str == "true" || str == "1"
	}

	// Extract error comment if call failed
	callComment := ""
	if c2, ok := callRaw["comment"]; ok {
		callComment = strings.Trim(string(c2), "\"")
	}

	// If OnlinePBX rejected the call, return error to frontend
	if !callStatusOK {
		errorCode := ""
		if ec, ok := callRaw["errorCode"]; ok {
			errorCode = strings.Trim(string(ec), "\"")
		}
		h.log.Error("OnlinePBX call rejected", "status", "0", "comment", callComment, "errorCode", errorCode)
		response.BadRequest(c, fmt.Sprintf("PBX call failed: %s", callComment))
		return
	}

	var callData map[string]interface{}
	if d, ok := callRaw["data"]; ok {
		json.Unmarshal(d, &callData)
	}

	// Extract call UUID from response
	pbxCallID := ""
	if callData != nil {
		if uid, ok := callData["uuid"].(string); ok {
			pbxCallID = uid
		}
	}

	// Create call log only on successful initiation
	callLogID := uuid.New()
	now := time.Now()

	var contactID *uuid.UUID
	if input.ContactID != "" {
		cid, err := uuid.Parse(input.ContactID)
		if err == nil {
			contactID = &cid
		}
	}

	logQuery := `
		INSERT INTO call_logs (
			id, tenant_id, organization_id, contact_id, caller_number, receiver_number,
			call_type, call_start_time, call_duration, call_outcome,
			pbx_call_id, agent_id, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'outbound', $7, 0, 'initiated', $8, $9, $9, $10, $10)
	`

	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	_, err = h.db.Exec(logQuery,
		callLogID, tenantID, orgIDPtr, contactID, fromExt, cleanPhone,
		now, pbxNullString(pbxCallID), userID, now,
	)
	if err != nil {
		h.log.Error("Failed to create call log", "error", err)
	}

	response.Success(c, gin.H{
		"success":     callStatusOK,
		"call_id":     callLogID,
		"pbx_call_id": pbxCallID,
		"comment":     callComment,
	})
}

// PBXWebhook handles incoming webhook events from OnlinePBX
func (h *Handler) PBXWebhook(c *gin.Context) {
	// GET requests are for webhook verification
	if c.Request.Method == "GET" {
		token := c.Query("token")
		challenge := c.Query("challenge")

		if token == "" {
			c.String(http.StatusBadRequest, "missing token")
			return
		}

		// Verify token matches a tenant's PBX webhook token
		var count int
		err := h.db.QueryRow(`
			SELECT COUNT(*) FROM tenant_settings
			WHERE settings->'pbx'->>'webhook_token' = $1
		`, token).Scan(&count)

		if err != nil || count == 0 {
			c.String(http.StatusForbidden, "invalid token")
			return
		}

		if challenge != "" {
			c.String(http.StatusOK, challenge)
		} else {
			c.String(http.StatusOK, "OK")
		}
		return
	}

	// POST requests are actual webhook events
	bodyBytes, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20)) // 1MB limit
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var data map[string]interface{}
	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := json.Unmarshal(bodyBytes, &data); err != nil {
			c.String(http.StatusBadRequest, "invalid JSON")
			return
		}
	} else {
		// Handle form-encoded data (OnlinePBX sends this format)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		_ = c.Request.ParseForm()
		data = make(map[string]interface{})
		for k, v := range c.Request.PostForm {
			if len(v) == 1 {
				data[k] = v[0]
			} else {
				data[k] = v
			}
		}
		for k, v := range c.Request.URL.Query() {
			if _, exists := data[k]; !exists {
				if len(v) == 1 {
					data[k] = v[0]
				}
			}
		}
	}

	h.log.Info("PBX webhook received", "data", data)

	// Extract call info — OnlinePBX field names:
	// event, uuid, caller, callee, direction, dialog_duration, call_duration,
	// hangup_cause, download_url
	event := getStr(data, "event")
	callSID := getStr(data, "uuid")
	if callSID == "" {
		callSID = getStr(data, "call_id")
	}
	callerNumber := getStr(data, "caller")
	if callerNumber == "" {
		callerNumber = getStr(data, "caller_id_number")
	}
	recipientNumber := getStr(data, "callee")
	if recipientNumber == "" {
		recipientNumber = getStr(data, "destination_number")
	}
	direction := getStr(data, "direction")

	// Identify tenant by X-Pbx-Token header (primary, how OnlinePBX sends it)
	// or by tenant_id query param (fallback)
	pbxToken := c.GetHeader("X-Pbx-Token")
	if pbxToken == "" {
		pbxToken = c.GetHeader("x-pbx-token")
	}

	var tenantID uuid.UUID
	if pbxToken != "" {
		err := h.db.QueryRow(`
			SELECT tenant_id FROM tenant_settings
			WHERE settings->'pbx'->>'webhook_token' = $1
		`, pbxToken).Scan(&tenantID)
		if err != nil {
			h.log.Warn("PBX webhook: invalid X-Pbx-Token", "token", pbxToken)
			c.String(http.StatusForbidden, "invalid webhook token")
			return
		}
	} else {
		// Fallback: tenant_id in query param
		tenantIDStr := c.Query("tenant_id")
		if tenantIDStr == "" {
			tenantIDStr = getStr(data, "tenant_id")
		}
		var err error
		tenantID, err = uuid.Parse(tenantIDStr)
		if err != nil {
			h.log.Warn("PBX webhook: no token and no valid tenant_id", "tenant_id", tenantIDStr)
			c.String(http.StatusOK, "ok")
			return
		}
	}

	// Skip intermediate ring events (call_user_start, call_answered)
	// Only process call_start (to create) and call_end (to update with final data)
	if event == "call_user_start" || event == "call_answered" {
		h.log.Debug("PBX webhook skipping intermediate event", "event", event, "uuid", callSID)
		c.JSON(http.StatusOK, gin.H{"status": "ok", "skipped": event})
		return
	}

	// Duration: dialog_duration = actual talk time, call_duration = total including ringing
	dialogDuration := getInt(data, "dialog_duration")
	callDuration := getInt(data, "call_duration")
	duration := dialogDuration
	if duration == 0 {
		duration = callDuration
	}

	// Status based on event type, talk time, and hangup cause
	callStatus := "initiated"
	if event == "call_end" {
		hangup := getStr(data, "hangup_cause")
		if dialogDuration > 0 {
			callStatus = "answered"
		} else if hangup == "USER_BUSY" || hangup == "CALL_REJECTED" {
			callStatus = "busy"
		} else if hangup == "NO_ANSWER" || hangup == "NO_USER_RESPONSE" || hangup == "ORIGINATOR_CANCEL" || hangup == "RECOVERY_ON_TIMER_EXPIRE" {
			callStatus = "no_answer"
		} else {
			callStatus = "no_answer"
		}
	} else if event == "call_start" {
		callStatus = "initiated"
	}

	// Recording URL (OnlinePBX uses download_url in call_end event)
	recordingURL := getStr(data, "download_url")
	if recordingURL == "" {
		recordingURL = getStr(data, "download")
	}
	if recordingURL == "" {
		recordingURL = getStr(data, "recording_url")
	}

	// Map direction
	callType := "inbound"
	if direction == "outbound" || direction == "out" {
		callType = "outbound"
	}
	if callStatus == "no_answer" && callType == "inbound" {
		callType = "missed"
	}

	// Try to find existing call log by pbx_call_id
	var existingID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM call_logs
		WHERE tenant_id = $1 AND pbx_call_id = $2 AND deleted_at IS NULL
		LIMIT 1
	`, tenantID, callSID).Scan(&existingID)

	// Fallback: if UUID doesn't match, find most recent call log for this number
	// (initiate_call may create with a different UUID than what webhooks report)
	if err != nil && recipientNumber != "" {
		err = h.db.QueryRow(`
			SELECT id FROM call_logs
			WHERE tenant_id = $1
				AND receiver_number = $2
				AND call_type = $3
				AND created_at >= NOW() - INTERVAL '2 minutes'
				AND deleted_at IS NULL
			ORDER BY created_at DESC
			LIMIT 1
		`, tenantID, recipientNumber, callType).Scan(&existingID)
	}

	if err == nil {
		// Update existing call log — only update with real data, never overwrite with empty/zero
		updates := []string{"updated_at = NOW()"}
		args := []interface{}{}
		argN := 0

		if duration > 0 {
			argN++
			updates = append(updates, fmt.Sprintf("call_duration = $%d", argN))
			args = append(args, duration)
		}
		if recordingURL != "" {
			argN++
			updates = append(updates, fmt.Sprintf("recording_url = $%d", argN))
			args = append(args, recordingURL)
		}
		if callStatus != "" {
			argN++
			updates = append(updates, fmt.Sprintf("call_outcome = $%d", argN))
			args = append(args, callStatus)
		}
		if callSID != "" {
			argN++
			updates = append(updates, fmt.Sprintf("pbx_call_id = COALESCE(NULLIF(pbx_call_id, ''), $%d)", argN))
			args = append(args, callSID)
		}
		if event == "call_end" {
			argN++
			updates = append(updates, fmt.Sprintf("call_end_time = $%d", argN))
			args = append(args, time.Now())
		}

		argN++
		args = append(args, existingID)
		argN++
		args = append(args, tenantID)

		query := fmt.Sprintf(
			"UPDATE call_logs SET %s WHERE id = $%d AND tenant_id = $%d",
			strings.Join(updates, ", "), argN-1, argN,
		)
		_, execErr := h.db.Exec(query, args...)
		if execErr != nil {
			h.log.Error("PBX webhook: failed to update call log", "error", execErr)
		} else {
			h.log.Info("PBX webhook updated call log", "id", existingID, "event", event, "duration", duration, "status", callStatus, "recording", recordingURL)
		}
	} else {
		// Create new call log (inbound calls or calls not initiated from CRM)
		newID := uuid.New()
		now := time.Now()

		// Try to match caller to a contact (last 10 digits)
		var contactID *uuid.UUID
		cleanNum := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, callerNumber)

		if len(cleanNum) >= 10 {
			last10 := cleanNum[len(cleanNum)-10:]
			var cid uuid.UUID
			qErr := h.db.QueryRow(`
				SELECT id FROM contacts
				WHERE tenant_id = $1 AND (phone LIKE '%' || $2 OR phone LIKE '%' || $2 || '%')
				AND deleted_at IS NULL
				LIMIT 1
			`, tenantID, last10).Scan(&cid)
			if qErr == nil {
				contactID = &cid
			}
		}

		query := `
			INSERT INTO call_logs (
				id, tenant_id, contact_id, caller_number, receiver_number,
				call_type, call_start_time, call_end_time, call_duration,
				call_outcome, recording_url, pbx_call_id,
				created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
		`

		var endTime *time.Time
		if event == "call_end" {
			t := now
			endTime = &t
		}

		_, execErr := h.db.Exec(query,
			newID, tenantID, contactID, callerNumber, recipientNumber,
			callType, now, endTime, duration,
			callStatus, pbxNullString(recordingURL), pbxNullString(callSID),
			uuid.Nil, now,
		)
		if execErr != nil {
			h.log.Error("PBX webhook: failed to create call log", "error", execErr)
		} else {
			h.log.Info("PBX webhook created call log", "id", newID, "event", event, "call_sid", callSID, "direction", direction)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Helper: get PBX config from database
func (h *Handler) getPBXConfigFromDB(tenantID uuid.UUID) (*PBXConfig, error) {
	var configJSON []byte
	err := h.db.QueryRow(`
		SELECT settings->'pbx' FROM tenant_settings
		WHERE tenant_id = $1
	`, tenantID).Scan(&configJSON)

	if err != nil {
		return nil, err
	}

	if configJSON == nil || string(configJSON) == "null" {
		return nil, sql.ErrNoRows
	}

	var config PBXConfig
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// Helper functions
func pbxNullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}
