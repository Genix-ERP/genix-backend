package handler

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/genixerp/genix-backend/internal/infrastructure/email"
	"github.com/google/uuid"
)

// RunReconciliationReminderScheduler starts a daily background job at 09:00 Tashkent time
// that sends email reminders for unanswered reconciliation acts (3-day and 7-day).
func (h *Handler) RunReconciliationReminderScheduler(ctx context.Context) {
	go func() {
		loc, err := time.LoadLocation("Asia/Tashkent")
		if err != nil {
			h.log.Error("Failed to load Asia/Tashkent timezone, using UTC+5 offset", "error", err)
			loc = time.FixedZone("UZT", 5*60*60)
		}

		for {
			now := time.Now().In(loc)
			// Next 09:00 Tashkent time
			next := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, loc)
			if now.After(next) {
				next = next.Add(24 * time.Hour)
			}
			sleepDuration := next.Sub(now)
			h.log.Info("Reconciliation reminder scheduler: next run", "next_run", next.Format("2006-01-02 15:04"), "sleep", sleepDuration.Round(time.Minute))

			select {
			case <-time.After(sleepDuration):
				h.sendReconciliationAutoReminders()
			case <-ctx.Done():
				h.log.Info("Reconciliation reminder scheduler stopped")
				return
			}
		}
	}()
	h.log.Info("Reconciliation reminder scheduler started (daily at 09:00 Tashkent time)")
}

// sendReconciliationAutoReminders finds reconciliation acts with pending responses
// and sends email reminders at 3-day and 7-day marks.
func (h *Handler) sendReconciliationAutoReminders() {
	h.log.Info("Running reconciliation auto-reminder check")
	now := time.Now()
	count := 0

	// 3-day reminders: acts sent 3+ days ago, no response, email reminder not yet sent
	rows3d, err := h.db.Query(`
		SELECT ra.id, ra.tenant_id, ra.created_by,
		       COALESCE(ct.name, 'Kontragent'),
		       COALESCE(ra.sent_via, ''), COALESCE(ra.sent_to, ''), ra.share_token
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.status = 'sent'
		  AND (ra.response_status IS NULL OR ra.response_status = '' OR ra.response_status = 'pending' OR ra.response_status = 'no_response')
		  AND COALESCE(ra.reminder_3d_sent, false) = false
		  AND ra.sent_at IS NOT NULL
		  AND ra.sent_at + INTERVAL '3 days' < NOW()
		  AND ra.deleted_at IS NULL
	`)
	if err != nil {
		h.log.Error("Failed to query 3-day reconciliation reminders", "error", err)
	} else {
		defer rows3d.Close()
		for rows3d.Next() {
			var actID, tenantID uuid.UUID
			var createdBy *uuid.UUID
			var partnerName, sentVia, sentTo string
			var shareToken *string
			if err := rows3d.Scan(&actID, &tenantID, &createdBy, &partnerName, &sentVia, &sentTo, &shareToken); err != nil {
				continue
			}

			// Send email reminder if originally sent via email
			if sentVia == "email" && sentTo != "" {
				h.sendReconciliationReminderEmail(tenantID, actID, sentTo, partnerName, shareToken, "3 kun")
			}

			// Also create in-app notification for the creator
			if createdBy != nil {
				h.db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'reconciliation_reminder', $4, $5, $6::jsonb, 'in_app', 'normal', $7)
				`, uuid.New(), tenantID, *createdBy,
					"Akt sverka javobsiz",
					partnerName+" hali akt sverkaga javob bermadi (3 kun)",
					`{"reconciliation_id":"`+actID.String()+`"}`, now)
			}

			h.db.Exec("UPDATE reconciliation_acts SET reminder_3d_sent = true WHERE id = $1", actID)
			count++
		}
	}

	// 7-day reminders: acts sent 7+ days ago, no response, 7-day email reminder not yet sent
	rows7d, err := h.db.Query(`
		SELECT ra.id, ra.tenant_id, ra.created_by,
		       COALESCE(ct.name, 'Kontragent'),
		       COALESCE(ra.sent_via, ''), COALESCE(ra.sent_to, ''), ra.share_token
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.status = 'sent'
		  AND (ra.response_status IS NULL OR ra.response_status = '' OR ra.response_status = 'pending' OR ra.response_status = 'no_response')
		  AND COALESCE(ra.reminder_7d_sent, false) = false
		  AND ra.sent_at IS NOT NULL
		  AND ra.sent_at + INTERVAL '7 days' < NOW()
		  AND ra.deleted_at IS NULL
	`)
	if err != nil {
		h.log.Error("Failed to query 7-day reconciliation reminders", "error", err)
	} else {
		defer rows7d.Close()
		for rows7d.Next() {
			var actID, tenantID uuid.UUID
			var createdBy *uuid.UUID
			var partnerName, sentVia, sentTo string
			var shareToken *string
			if err := rows7d.Scan(&actID, &tenantID, &createdBy, &partnerName, &sentVia, &sentTo, &shareToken); err != nil {
				continue
			}

			// Send email reminder if originally sent via email
			if sentVia == "email" && sentTo != "" {
				h.sendReconciliationReminderEmail(tenantID, actID, sentTo, partnerName, shareToken, "7 kun")
			}

			// Notify creator
			if createdBy != nil {
				h.db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'reconciliation_no_response', $4, $5, $6::jsonb, 'in_app', 'high', $7)
				`, uuid.New(), tenantID, *createdBy,
					"Akt sverka javobsiz — eslatma yuboring",
					partnerName+" 7 kun davomida akt sverkaga javob bermadi",
					`{"reconciliation_id":"`+actID.String()+`"}`, now)
			}

			// Notify admins
			adminRows, _ := h.db.Query(`
				SELECT tu.user_id FROM tenant_users tu
				WHERE tu.tenant_id = $1 AND tu.role IN ('admin', 'owner') AND tu.deleted_at IS NULL
			`, tenantID)
			if adminRows != nil {
				for adminRows.Next() {
					var adminID uuid.UUID
					if adminRows.Scan(&adminID) == nil && (createdBy == nil || adminID != *createdBy) {
						h.db.Exec(`
							INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
							VALUES ($1, $2, $3, 'reconciliation_no_response', $4, $5, $6::jsonb, 'in_app', 'high', $7)
						`, uuid.New(), tenantID, adminID,
							"Akt sverka javobsiz — eslatma yuboring",
							partnerName+" 7 kun davomida akt sverkaga javob bermadi",
							`{"reconciliation_id":"`+actID.String()+`"}`, now)
					}
				}
				adminRows.Close()
			}

			// Auto-set status to no_response
			h.db.Exec("UPDATE reconciliation_acts SET response_status = 'no_response', reminder_7d_sent = true WHERE id = $1", actID)
			count++
		}
	}

	if count > 0 {
		h.log.Info("Reconciliation auto-reminders processed", "count", count)
	}
}

// sendReconciliationReminderEmail sends an email reminder for a specific reconciliation act.
func (h *Handler) sendReconciliationReminderEmail(tenantID, actID uuid.UUID, sentTo, partnerName string, shareToken *string, dayLabel string) {
	// Ensure share token exists and refresh expiry
	token := ""
	if shareToken != nil && *shareToken != "" {
		token = *shareToken
	} else {
		token = uuid.New().String()[:8] + uuid.New().String()[:8]
		h.db.Exec(`UPDATE reconciliation_acts SET share_token = $1, share_expires_at = NOW() + INTERVAL '24 hours' WHERE id = $2 AND tenant_id = $3`, token, actID, tenantID)
	}
	// Always refresh expiry
	h.db.Exec(`UPDATE reconciliation_acts SET share_expires_at = NOW() + INTERVAL '24 hours' WHERE id = $1 AND tenant_id = $2`, actID, tenantID)

	shareURL := fmt.Sprintf("%s/shared/reconciliation/%s", h.config.App.FrontendURL, token)

	act, lines, loadErr := h.loadReconciliationActFull(tenantID, actID)
	if loadErr != nil {
		h.log.Error("Failed to load reconciliation act for auto-reminder email", "act_id", actID, "error", loadErr)
		return
	}

	closingBalance := act.OpeningBalance + act.OurDebitTotal - act.OurCreditTotal
	subject := fmt.Sprintf("Eslatma (%s): Akt sverka — %s (%s – %s)", dayLabel, partnerName, act.PeriodStart, act.PeriodEnd)
	emailBody := fmt.Sprintf(`<div style="margin-bottom:20px;padding:12px 16px;background:#fef3c7;border-left:3px solid #f59e0b;border-radius:4px;font-size:14px;color:#92400e;">Eslatma (%s): Iltimos, akt sverkaga javob bering.</div>`, dayLabel) +
		h.renderReconciliationEmailHTML(act, lines, closingBalance, shareURL)

	sendErr := h.emailService.Send(&email.Email{
		To:      []string{sentTo},
		Subject: subject,
		Body:    emailBody,
		IsHTML:  true,
	})
	if sendErr != nil {
		h.log.Error("Failed to send reconciliation auto-reminder email", "act_id", actID, "to", sentTo, "error", sendErr)
	} else {
		h.log.Info("Sent reconciliation auto-reminder email", "act_id", actID, "to", sentTo, "day_label", dayLabel)
	}
}

// RunVendorBillOverdueScheduler starts a daily background job at 09:00 Tashkent time
// that creates notifications for overdue vendor bills (purchase invoices).
func (h *Handler) RunVendorBillOverdueScheduler(ctx context.Context) {
	go func() {
		loc, err := time.LoadLocation("Asia/Tashkent")
		if err != nil {
			h.log.Error("Failed to load Asia/Tashkent timezone, using UTC+5 offset", "error", err)
			loc = time.FixedZone("UZT", 5*60*60)
		}

		for {
			now := time.Now().In(loc)
			// Next 09:00 Tashkent time
			next := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, loc)
			if now.After(next) {
				next = next.Add(24 * time.Hour)
			}
			sleepDuration := next.Sub(now)
			h.log.Info("Vendor bill overdue scheduler: next run", "next_run", next.Format("2006-01-02 15:04"), "sleep", sleepDuration.Round(time.Minute))

			select {
			case <-time.After(sleepDuration):
				h.checkVendorBillsOverdue()
			case <-ctx.Done():
				h.log.Info("Vendor bill overdue scheduler stopped")
				return
			}
		}
	}()
	h.log.Info("Vendor bill overdue scheduler started (daily at 09:00 Tashkent time)")
}

// checkVendorBillsOverdue finds overdue purchase invoices and creates notifications.
// Only notifies once per bill using the overdue_notified flag.
func (h *Handler) checkVendorBillsOverdue() {
	h.log.Info("Running vendor bill overdue check")
	now := time.Now()
	count := 0

	rows, err := h.db.Query(`
		SELECT pi.id, pi.tenant_id, pi.invoice_number, pi.due_date,
		       COALESCE(c.name, 'Noma''lum yetkazuvchi'), pi.total_amount, pi.amount_paid,
		       pi.created_by
		FROM purchase_invoices pi
		LEFT JOIN contacts c ON pi.vendor_id = c.id
		WHERE pi.due_date < CURRENT_DATE
		  AND pi.status NOT IN ('paid', 'cancelled')
		  AND COALESCE(pi.overdue_notified, false) = false
		  AND pi.deleted_at IS NULL
	`)
	if err != nil {
		h.log.Error("Failed to query overdue vendor bills", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			invoiceID  uuid.UUID
			tenantID   uuid.UUID
			number     string
			dueDate    time.Time
			vendorName string
			totalAmt   float64
			amountPaid float64
			createdBy  *uuid.UUID
		)
		if err := rows.Scan(&invoiceID, &tenantID, &number, &dueDate, &vendorName, &totalAmt, &amountPaid, &createdBy); err != nil {
			h.log.Error("Failed to scan overdue vendor bill row", "error", err)
			continue
		}

		overdueDays := int(math.Floor(now.Sub(dueDate).Hours() / 24))
		if overdueDays < 1 {
			overdueDays = 1
		}

		title := "Yetkazuvchi hisob-fakturasi muddati o'tdi"
		message := fmt.Sprintf("Vendor bill %s is overdue by %d days (vendor: %s, amount: %.2f)",
			number, overdueDays, vendorName, totalAmt-amountPaid)

		notifData := fmt.Sprintf(`{"purchase_invoice_id":"%s","invoice_number":"%s","overdue_days":%d,"vendor":"%s"}`,
			invoiceID.String(), number, overdueDays, vendorName)

		// Notify the bill creator
		if createdBy != nil {
			h.db.Exec(`
				INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
				VALUES ($1, $2, $3, 'vendor_bill_overdue', $4, $5, $6::jsonb, 'in_app', 'high', $7)
			`, uuid.New(), tenantID, *createdBy, title, message, notifData, now)
		}

		// Also notify admins
		adminRows, _ := h.db.Query(`
			SELECT tu.user_id FROM tenant_users tu
			WHERE tu.tenant_id = $1 AND tu.role IN ('admin', 'owner') AND tu.deleted_at IS NULL
		`, tenantID)
		if adminRows != nil {
			for adminRows.Next() {
				var adminID uuid.UUID
				if adminRows.Scan(&adminID) == nil && (createdBy == nil || adminID != *createdBy) {
					h.db.Exec(`
						INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
						VALUES ($1, $2, $3, 'vendor_bill_overdue', $4, $5, $6::jsonb, 'in_app', 'high', $7)
					`, uuid.New(), tenantID, adminID, title, message, notifData, now)
				}
			}
			adminRows.Close()
		}

		// Mark as notified so we don't re-notify
		h.db.Exec("UPDATE purchase_invoices SET overdue_notified = true WHERE id = $1", invoiceID)
		count++
	}

	if count > 0 {
		h.log.Info("Vendor bill overdue notifications sent", "count", count)
	}
}
