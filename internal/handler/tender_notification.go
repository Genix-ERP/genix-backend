package handler

import (
	"database/sql"
	"fmt"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListTenderNotifications lists notifications for the current user
func (h *Handler) ListTenderNotifications(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	isRead := c.Query("is_read")

	pagination := entity.NewPagination(page, limit)

	countQuery := `SELECT COUNT(*) FROM tender_notifications WHERE user_id = $1`
	countArgs := []interface{}{userID}
	argIdx := 2

	if isRead == "true" {
		countQuery += fmt.Sprintf(" AND is_read = $%d", argIdx)
		countArgs = append(countArgs, true)
		argIdx++
	} else if isRead == "false" {
		countQuery += fmt.Sprintf(" AND is_read = $%d", argIdx)
		countArgs = append(countArgs, false)
		argIdx++
	}

	var total int
	h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	pagination.Calculate(total)

	query := `
		SELECT id, type, title, message, data, is_read, created_at
		FROM tender_notifications
		WHERE user_id = $1
	`
	qArgs := []interface{}{userID}
	qIdx := 2

	if isRead == "true" {
		query += fmt.Sprintf(" AND is_read = $%d", qIdx)
		qArgs = append(qArgs, true)
		qIdx++
	} else if isRead == "false" {
		query += fmt.Sprintf(" AND is_read = $%d", qIdx)
		qArgs = append(qArgs, false)
		qIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", qIdx, qIdx+1)
	qArgs = append(qArgs, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, qArgs...)
	if err != nil {
		h.log.Error("Failed to list notifications", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var notifications []entity.NotificationResponse
	for rows.Next() {
		var n entity.NotificationResponse
		var data sql.NullString

		err := rows.Scan(&n.ID, &n.Type, &n.Title, &n.Message, &data, &n.IsRead, &n.CreatedAt)
		if err != nil {
			continue
		}
		// Data is stored as JSONB but we return empty map if null
		if n.Data == nil {
			n.Data = map[string]interface{}{}
		}

		notifications = append(notifications, n)
	}

	// Also return unread count
	var unreadCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_notifications WHERE user_id = $1 AND is_read = false`, userID).Scan(&unreadCount)

	response.Success(c, map[string]interface{}{
		"notifications": notifications,
		"unread_count":  unreadCount,
		"pagination":    pagination,
	})
}

// MarkTenderNotificationRead marks a single notification as read
func (h *Handler) MarkTenderNotificationRead(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	notifID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid notification ID")
		return
	}

	result, err := h.db.Exec(`
		UPDATE tender_notifications SET is_read = true, read_at = NOW()
		WHERE id = $1 AND user_id = $2
	`, notifID, userID)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Notification not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Notification marked as read"})
}

// MarkAllTenderNotificationsRead marks all notifications as read for the user
func (h *Handler) MarkAllTenderNotificationsRead(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	_, err := h.db.Exec(`
		UPDATE tender_notifications SET is_read = true, read_at = NOW()
		WHERE user_id = $1 AND is_read = false
	`, userID)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}

	response.Success(c, map[string]interface{}{"message": "All notifications marked as read"})
}
