package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type registerDeviceInput struct {
	Token      string          `json:"token"`
	Platform   string          `json:"platform"`
	DeviceInfo json.RawMessage `json:"device_info"`
}

// RegisterDevice stores (or refreshes) the caller's FCM device token so the
// backend can push to it. The mobile app calls this after login and on every
// FCM token refresh.
// @Summary Register a device push token
// @Tags Devices
// @Router /devices [post]
func (h *Handler) RegisterDevice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var in registerDeviceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if in.Token == "" {
		response.BadRequest(c, "token is required")
		return
	}
	platform := strings.ToLower(strings.TrimSpace(in.Platform))
	if platform != "android" && platform != "ios" {
		platform = "android"
	}
	deviceInfo := "{}"
	if len(in.DeviceInfo) > 0 {
		deviceInfo = string(in.DeviceInfo)
	}

	// Upsert on token: one FCM token belongs to one device, so if it reappears
	// (reinstall, or a different user logs in on that device) it re-homes to the
	// current (tenant, user) instead of duplicating or pushing to the old user.
	_, err := h.db.Exec(`
		INSERT INTO device_tokens (tenant_id, user_id, platform, token, device_info, last_seen_at, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, NOW(), NOW())
		ON CONFLICT (token) DO UPDATE SET
			tenant_id    = EXCLUDED.tenant_id,
			user_id      = EXCLUDED.user_id,
			platform     = EXCLUDED.platform,
			device_info  = EXCLUDED.device_info,
			last_seen_at = NOW()
	`, tenantID, userID, platform, in.Token, deviceInfo)
	if err != nil {
		h.log.Error("register device failed", "error", err, "user_id", userID)
		response.InternalError(c, "Failed to register device")
		return
	}
	response.Success(c, gin.H{"message": "Device registered"})
}

// UnregisterDevice removes a device token (e.g. on logout). Only the caller's
// own token is removed.
// @Summary Unregister a device push token
// @Tags Devices
// @Router /devices [delete]
func (h *Handler) UnregisterDevice(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var in struct {
		Token string `json:"token"`
	}
	_ = c.ShouldBindJSON(&in)
	token := strings.TrimSpace(in.Token)
	if token == "" {
		token = strings.TrimSpace(c.Query("token"))
	}
	if token == "" {
		response.BadRequest(c, "token is required")
		return
	}
	h.db.Exec(`DELETE FROM device_tokens WHERE token = $1 AND user_id = $2`, token, userID)
	response.Success(c, gin.H{"message": "Device unregistered"})
}

// TestPush sends a test notification to the caller's own devices — handy for the
// mobile team to confirm the pipeline end to end.
// @Summary Send a test push to yourself
// @Tags Devices
// @Router /devices/test [post]
func (h *Handler) TestPush(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)
	if !h.fcm.Enabled() {
		response.BadRequest(c, "Push is not configured on the server (FCM credentials missing)")
		return
	}
	h.pushToUser(tenantID, userID, "Test", "Push notifications are working ✅", map[string]string{"type": "test"})
	response.Success(c, gin.H{"message": "Test push dispatched (arrives only if you have a registered device)"})
}

// pushToUser fans a push out to all of a user's registered devices. Best-effort
// and safe to call from a goroutine: it never touches the gin.Context, swallows
// errors (logging them), respects the user's push preference, and prunes dead
// tokens. No-op when FCM isn't configured.
func (h *Handler) pushToUser(tenantID, userID uuid.UUID, title, body string, data map[string]string) {
	if !h.fcm.Enabled() {
		return
	}

	// Respect an explicit opt-out stored in users.settings; default ON.
	var pref sql.NullBool
	_ = h.db.QueryRow(
		`SELECT (settings->>'push_notifications')::bool FROM users WHERE id = $1`, userID,
	).Scan(&pref)
	if pref.Valid && !pref.Bool {
		return
	}

	rows, err := h.db.Query(`SELECT token FROM device_tokens WHERE user_id = $1`, userID)
	if err != nil {
		h.log.Error("push: load tokens failed", "error", err, "user_id", userID)
		return
	}
	var tokens []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			tokens = append(tokens, t)
		}
	}
	rows.Close()
	if len(tokens) == 0 {
		return
	}

	if data == nil {
		data = map[string]string{}
	}
	results, err := h.fcm.Send(context.Background(), tokens, title, body, data)
	if err != nil {
		h.log.Error("push: send failed", "error", err, "user_id", userID)
		return
	}
	// Prune tokens FCM told us are dead so we stop trying them.
	for _, r := range results {
		if r.Unregister {
			h.db.Exec(`DELETE FROM device_tokens WHERE token = $1`, r.Token)
		}
	}
}
