package handler

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Notification translation templates ────────────────────────────────────────
// Each key maps to a map[lang]template with %s placeholders.

var notificationTemplates = map[string]map[string]struct{ Title, Message string }{
	// ── Sales Invoices ──
	"invoice_sent": {
		"en": {Title: "Invoice Sent", Message: "Invoice %s has been sent to %s for %s"},
		"uz": {Title: "Faktura yuborildi", Message: "%s faktura %s ga %s uchun yuborildi"},
		"ru": {Title: "Счёт отправлен", Message: "Счёт %s отправлен клиенту %s на сумму %s"},
	},
	"invoice_overdue": {
		"en": {Title: "Invoice Overdue", Message: "Invoice %s for %s is overdue"},
		"uz": {Title: "Faktura muddati o'tgan", Message: "%s faktura %s uchun muddati o'tgan"},
		"ru": {Title: "Счёт просрочен", Message: "Счёт %s для %s просрочен"},
	},
	"payment_recorded": {
		"en": {Title: "Payment Recorded", Message: "Payment of %s recorded on invoice %s from %s"},
		"uz": {Title: "To'lov qayd etildi", Message: "%s miqdorida to'lov %s fakturaga %s dan qayd etildi"},
		"ru": {Title: "Платёж записан", Message: "Платёж на сумму %s записан по счёту %s от %s"},
	},
	"credit_note_created": {
		"en": {Title: "Credit Note Created", Message: "Credit note %s created for invoice %s, amount %s"},
		"uz": {Title: "Kredit nota yaratildi", Message: "%s kredit nota %s faktura uchun yaratildi, %s miqdorida"},
		"ru": {Title: "Кредит-нота создана", Message: "Кредит-нота %s создана по счёту %s на сумму %s"},
	},
	// ── Payments ──
	"payment_confirmed": {
		"en": {Title: "Payment Confirmed", Message: "Payment %s of %s from %s has been confirmed"},
		"uz": {Title: "To'lov tasdiqlandi", Message: "%s to'lov %s miqdorida %s dan tasdiqlandi"},
		"ru": {Title: "Платёж подтверждён", Message: "Платёж %s на сумму %s от %s подтверждён"},
	},
	"payment_received": {
		"en": {Title: "Payment Received", Message: "Payment of %s received from %s"},
		"uz": {Title: "To'lov qabul qilindi", Message: "%s miqdorida to'lov %s dan qabul qilindi"},
		"ru": {Title: "Платёж получен", Message: "Платёж на сумму %s получен от %s"},
	},
	// ── Purchase Invoices ──
	"purchase_invoice_created": {
		"en": {Title: "Purchase Invoice Created", Message: "Purchase invoice %s created for vendor %s, amount %s"},
		"uz": {Title: "Xarid fakturasi yaratildi", Message: "%s xarid fakturasi %s yetkazuvchi uchun yaratildi, %s miqdorida"},
		"ru": {Title: "Счёт на закупку создан", Message: "Счёт на закупку %s создан для поставщика %s на сумму %s"},
	},
	"purchase_invoice_confirmed": {
		"en": {Title: "Purchase Invoice Confirmed", Message: "Purchase invoice %s for %s has been confirmed"},
		"uz": {Title: "Xarid fakturasi tasdiqlandi", Message: "%s xarid fakturasi %s uchun tasdiqlandi"},
		"ru": {Title: "Счёт на закупку подтверждён", Message: "Счёт на закупку %s для %s подтверждён"},
	},
	// ── Inventory ──
	"low_stock": {
		"en": {Title: "Low Stock Warning", Message: "Product %s is running low (%s remaining)"},
		"uz": {Title: "Kam qoldiq ogohlantirishi", Message: "%s mahsulot kam qoldi (%s qoldi)"},
		"ru": {Title: "Мало на складе", Message: "Товар %s заканчивается (осталось %s)"},
	},
	// ── Expenses ──
	"expense_approved": {
		"en": {Title: "Expense Approved", Message: "Expense %s for %s has been approved"},
		"uz": {Title: "Xarajat tasdiqlandi", Message: "%s xarajat %s miqdorida tasdiqlandi"},
		"ru": {Title: "Расход одобрен", Message: "Расход %s на сумму %s одобрен"},
	},
	// ── Payroll ──
	"salary_confirmed": {
		"en": {Title: "Salary Payment Confirmed", Message: "Salary payment of %s has been confirmed for %s"},
		"uz": {Title: "Ish haqi tasdiqlandi", Message: "%s miqdoridagi ish haqi %s uchun tasdiqlandi"},
		"ru": {Title: "Зарплата подтверждена", Message: "Выплата зарплаты %s подтверждена для %s"},
	},
	// ── Sales Orders ──
	"sales_order_confirmed": {
		"en": {Title: "Sales Order Confirmed", Message: "Sales order %s for %s confirmed, amount %s"},
		"uz": {Title: "Sotuv buyurtmasi tasdiqlandi", Message: "%s sotuv buyurtmasi %s uchun tasdiqlandi, %s miqdorida"},
		"ru": {Title: "Заказ на продажу подтверждён", Message: "Заказ на продажу %s для %s подтверждён на сумму %s"},
	},
	// ── Purchase Orders ──
	"purchase_order_approved": {
		"en": {Title: "Purchase Order Approved", Message: "Purchase order %s for vendor %s approved, amount %s"},
		"uz": {Title: "Xarid buyurtmasi tasdiqlandi", Message: "%s xarid buyurtmasi %s yetkazuvchi uchun tasdiqlandi, %s miqdorida"},
		"ru": {Title: "Заказ на закупку одобрён", Message: "Заказ на закупку %s для поставщика %s одобрён на сумму %s"},
	},
}

// getUserLanguage retrieves the language preference from the user profile
func (h *Handler) getUserLanguage(userID uuid.UUID) string {
	var lang string
	err := h.db.QueryRow(`SELECT COALESCE(language, 'en') FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&lang)
	if err != nil || lang == "" {
		return "en"
	}
	return lang
}

// createNotification inserts a notification for a specific user (legacy signature, no translation)
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

// createTranslatedNotification inserts a notification with auto-translated content based on user's language
func (h *Handler) createTranslatedNotification(tenantID, userID uuid.UUID, notifType string, data map[string]interface{}, args ...string) {
	lang := h.getUserLanguage(userID)

	title := notifType
	message := ""

	if templates, ok := notificationTemplates[notifType]; ok {
		tmpl, exists := templates[lang]
		if !exists {
			tmpl = templates["en"] // fallback to English
		}
		title = tmpl.Title

		// Format message with provided args
		iArgs := make([]interface{}, len(args))
		for i, a := range args {
			iArgs[i] = a
		}
		message = fmt.Sprintf(tmpl.Message, iArgs...)
	}

	h.createNotification(tenantID, userID, notifType, title, message, data)
}

// createNotificationForAllTenantUsers creates a notification for all active users in a tenant
func (h *Handler) createNotificationForAllTenantUsers(tenantID uuid.UUID, notifType string, data map[string]interface{}, args ...string) {
	rows, err := h.db.Query(`SELECT id FROM users WHERE tenant_id = $1 AND is_active = true AND deleted_at IS NULL`, tenantID)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var uid uuid.UUID
		if err := rows.Scan(&uid); err == nil {
			h.createTranslatedNotification(tenantID, uid, notifType, data, args...)
		}
	}
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

// UnreadCount returns the count of unread notifications for the current user
func (h *Handler) UnreadCount(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var count int
	err := h.db.QueryRow(`
		SELECT COUNT(*) FROM notifications
		WHERE tenant_id = $1 AND user_id = $2 AND is_read = false
	`, tenantID, userID).Scan(&count)
	if err != nil {
		count = 0
	}

	response.Success(c, gin.H{"count": count})
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
