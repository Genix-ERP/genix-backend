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
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"`  // "onlinepbx"
	Domain    string `json:"domain"`    // e.g., "pbx36019.onpbx.ru"
	APIKey    string `json:"api_key"`
	Extension string `json:"extension"` // Default extension
	CallerID  string `json:"caller_id"`
}

// onlinePBXAuth handles authentication with OnlinePBX API
type onlinePBXAuth struct {
	KeyID string
	Key   string
}

func (h *Handler) authenticateOnlinePBX(domain, apiKey string) (*onlinePBXAuth, error) {
	url := fmt.Sprintf("https://api2.onlinepbx.ru/%s/auth.json", domain)

	body, _ := json.Marshal(map[string]string{"auth_key": apiKey})
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Status bool `json:"status"`
		Data   struct {
			KeyID string `json:"key_id"`
			Key   string `json:"key"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode auth response: %w", err)
	}

	if !result.Status {
		return nil, fmt.Errorf("authentication failed")
	}

	return &onlinePBXAuth{
		KeyID: result.Data.KeyID,
		Key:   result.Data.Key,
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

	// Mask API key for security
	if config.APIKey != "" {
		config.APIKey = "***" + config.APIKey[max(0, len(config.APIKey)-4):]
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

	// If API key is masked, keep the old one
	if strings.HasPrefix(config.APIKey, "***") {
		oldConfig, err := h.getPBXConfigFromDB(tenantID)
		if err == nil {
			config.APIKey = oldConfig.APIKey
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

	// Initiate call via OnlinePBX
	callURL := fmt.Sprintf("https://api2.onlinepbx.ru/%s/call/now.json", config.Domain)
	callBody, _ := json.Marshal(map[string]string{
		"from": fromExt,
		"to":   input.Phone,
	})

	req, _ := http.NewRequest("POST", callURL, bytes.NewBuffer(callBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-pbx-authentication", fmt.Sprintf("%s:%s", auth.KeyID, auth.Key))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		h.log.Error("OnlinePBX call initiation failed", "error", err)
		response.InternalError(c, "Failed to initiate call")
		return
	}
	defer resp.Body.Close()

	var callResult struct {
		Status bool   `json:"status"`
		Data   map[string]interface{} `json:"data"`
		Comment string `json:"comment"`
	}
	json.NewDecoder(resp.Body).Decode(&callResult)

	// Extract call UUID from response
	pbxCallID := ""
	if callResult.Data != nil {
		if uid, ok := callResult.Data["uuid"].(string); ok {
			pbxCallID = uid
		}
	}

	// Create call log
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
			id, tenant_id, contact_id, caller_number, receiver_number,
			call_type, call_start_time, call_duration, call_outcome,
			pbx_call_id, agent_id, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'outbound', $6, 0, 'initiated', $7, $8, $8, $9, $9)
	`

	_, err = h.db.Exec(logQuery,
		callLogID, tenantID, contactID, fromExt, input.Phone,
		now, pbxNullString(pbxCallID), userID, now,
	)
	if err != nil {
		h.log.Error("Failed to create call log", "error", err)
	}

	response.Success(c, gin.H{
		"success":     callResult.Status,
		"call_id":     callLogID,
		"pbx_call_id": pbxCallID,
		"comment":     callResult.Comment,
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
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		c.String(http.StatusBadRequest, "invalid JSON")
		return
	}

	h.log.Info("PBX webhook received", "data", data)

	// Extract common fields
	event := getStr(data, "event")
	callSID := getStr(data, "uuid")
	callerNumber := getStr(data, "caller")
	recipientNumber := getStr(data, "callee")
	direction := getStr(data, "direction")

	// Get tenant from query param or data
	tenantIDStr := c.Query("tenant_id")
	if tenantIDStr == "" {
		tenantIDStr = getStr(data, "tenant_id")
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.Warn("PBX webhook: invalid tenant_id", "tenant_id", tenantIDStr)
		c.String(http.StatusOK, "ok")
		return
	}

	// Skip intermediate events
	if event == "call_user_start" || event == "call_answered" {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "skipped": event})
		return
	}

	// Parse duration
	dialogDuration := getInt(data, "dialog_duration")
	callDuration := getInt(data, "call_duration")
	duration := dialogDuration
	if duration == 0 {
		duration = callDuration
	}

	// Parse call status
	callStatus := "initiated"
	if event == "call_end" {
		hangup := getStr(data, "hangup_cause")
		if dialogDuration > 0 {
			callStatus = "answered"
		} else if hangup == "USER_BUSY" || hangup == "CALL_REJECTED" {
			callStatus = "busy"
		} else {
			callStatus = "no_answer"
		}
	} else if event == "call_start" {
		callStatus = "initiated"
	}

	// Recording URL
	recordingURL := getStr(data, "download_url")

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
	err = h.db.QueryRow(`
		SELECT id FROM call_logs
		WHERE tenant_id = $1 AND pbx_call_id = $2 AND deleted_at IS NULL
		LIMIT 1
	`, tenantID, callSID).Scan(&existingID)

	if err == nil {
		// Update existing call log
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
		h.db.Exec(query, args...)
	} else {
		// Create new call log (inbound or unknown)
		newID := uuid.New()
		now := time.Now()

		// Try to match caller to a contact
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
			err := h.db.QueryRow(`
				SELECT id FROM contacts
				WHERE tenant_id = $1 AND (phone LIKE '%' || $2 OR phone LIKE '%' || $2 || '%')
				AND deleted_at IS NULL
				LIMIT 1
			`, tenantID, last10).Scan(&cid)
			if err == nil {
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

		h.db.Exec(query,
			newID, tenantID, contactID, callerNumber, recipientNumber,
			callType, now, endTime, duration,
			callStatus, pbxNullString(recordingURL), pbxNullString(callSID),
			uuid.Nil, now,
		)
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
