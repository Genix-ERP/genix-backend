package handler

import (
	"encoding/json"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// createNotification inserts a notification for a specific user
func (h *Handler) createNotification(tenantID, userID uuid.UUID, notifType, title, message string, data map[string]interface{}) {
	dataJSON := "{}"
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			dataJSON = string(b)
		}
	}
	h.db.Exec(`
		INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, 'in_app', 'normal', $8)
	`, uuid.New(), tenantID, userID, notifType, title, message, dataJSON, time.Now())
}

// ListNotifications returns notifications for the current user
func (h *Handler) ListNotifications(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	isReadFilter := c.Query("is_read")

	query := `
		SELECT id, type, title, COALESCE(message, ''), COALESCE(data::text, '{}'),
		       channel, priority, is_read, created_at, read_at
		FROM notifications
		WHERE tenant_id = $1 AND user_id = $2
	`
	args := []interface{}{tenantID, userID}

	if isReadFilter == "false" {
		query += " AND is_read = false"
	} else if isReadFilter == "true" {
		query += " AND is_read = true"
	}

	query += " ORDER BY created_at DESC LIMIT 50"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to query notifications")
		return
	}
	defer rows.Close()

	var notifications []map[string]interface{}
	for rows.Next() {
		var (
			id, notifType, title, message, data, channel, priority string
			isRead                                                  bool
			createdAt                                               time.Time
			readAt                                                  *time.Time
		)
		if err := rows.Scan(&id, &notifType, &title, &message, &data, &channel, &priority, &isRead, &createdAt, &readAt); err != nil {
			continue
		}
		n := map[string]interface{}{
			"id":         id,
			"type":       notifType,
			"title":      title,
			"message":    message,
			"data":       data,
			"channel":    channel,
			"priority":   priority,
			"is_read":    isRead,
			"created_at": createdAt,
		}
		if readAt != nil {
			n["read_at"] = readAt
		}
		notifications = append(notifications, n)
	}
	if notifications == nil {
		notifications = []map[string]interface{}{}
	}
	response.Success(c, notifications)
}

// MarkNotificationRead marks a single notification as read
func (h *Handler) MarkNotificationRead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid notification ID")
		return
	}

	h.db.Exec(`
		UPDATE notifications SET is_read = true, read_at = $1
		WHERE id = $2 AND tenant_id = $3 AND user_id = $4
	`, time.Now(), id, tenantID, userID)

	response.Success(c, gin.H{"message": "Marked as read"})
}

// MarkAllNotificationsRead marks all unread notifications as read for the user
func (h *Handler) MarkAllNotificationsRead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	h.db.Exec(`
		UPDATE notifications SET is_read = true, read_at = $1
		WHERE tenant_id = $2 AND user_id = $3 AND is_read = false
	`, time.Now(), tenantID, userID)

	response.Success(c, gin.H{"message": "All marked as read"})
}
