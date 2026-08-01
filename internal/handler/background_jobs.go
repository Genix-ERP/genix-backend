package handler

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/infrastructure/database"
	"github.com/genixerp/genix-backend/internal/pkg/logger"
	"github.com/google/uuid"
)

// StartBackgroundJobs launches periodic background tasks
func StartBackgroundJobs(db *database.DB, log logger.Logger) {
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()

		// Run once on startup after a short delay
		time.Sleep(30 * time.Second)
		checkStepTimeouts(db, log)

		for range ticker.C {
			checkStepTimeouts(db, log)
		}
	}()

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		// Run once on startup after a short delay
		time.Sleep(1 * time.Minute)
		checkReconciliationReminders(db, log)

		for range ticker.C {
			checkReconciliationReminders(db, log)
		}
	}()

	// Contract expiry notifications — responsible employee gets an in-app
	// notification at 30/14/3 days before end_date, de-duplicated per
	// threshold via contract_expiry_notifications (migration 443).
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		time.Sleep(90 * time.Second)
		checkContractExpiryNotifications(db, log)

		for range ticker.C {
			checkContractExpiryNotifications(db, log)
		}
	}()

	// Auto depreciation scheduler — runs every hour, processes on 1st of each month
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		// Run once on startup after 2 minutes
		time.Sleep(2 * time.Minute)
		runAutoDepreciation(db, log)

		for range ticker.C {
			runAutoDepreciation(db, log)
		}
	}()

	// CRM activity reminders — every minute, fires notifications when
	// an activity's reminder_datetime has passed and reminder_sent is
	// still false. 1-minute granularity is plenty for "remind me to
	// call this lead at 3pm" UX without hammering the DB.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		// First run after 20s so the server is warm.
		time.Sleep(20 * time.Second)
		checkActivityReminders(db, log)

		for range ticker.C {
			checkActivityReminders(db, log)
		}
	}()

	// Vazifalar overdue scanner — hourly. Notifies each assignee once when a
	// task's due_date passes; overdue_notified_at makes it one-shot (a changed
	// due date re-arms it, see UpdateBoardTask).
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		time.Sleep(90 * time.Second)
		checkOverdueTasks(db, log)

		for range ticker.C {
			checkOverdueTasks(db, log)
		}
	}()

	log.Info("Background jobs started (step timeout checker every 15 min, reconciliation reminders every 1 hour, auto depreciation hourly, activity reminders every 1 min, overdue tasks hourly)")
}

// checkOverdueTasks finds open tasks whose due_date has passed and that have
// not been notified yet, then notifies every assignee's linked user (falling
// back to the task creator when there are no assignees). The task is stamped
// overdue_notified_at even when nobody could be notified so it isn't
// re-scanned forever. Notifications are inserted with push_sent_at NULL so the
// background push dispatcher delivers the mobile push.
func checkOverdueTasks(db *database.DB, log logger.Logger) {
	rows, err := db.Query(`
		SELECT t.id, t.tenant_id, t.board_id, t.title, t.due_date, t.created_by
		FROM tasks t
		JOIN task_boards b ON b.id = t.board_id
		WHERE t.due_date < CURRENT_DATE
		  AND t.completed_at IS NULL
		  AND t.archived_at IS NULL
		  AND t.overdue_notified_at IS NULL
		  AND b.archived_at IS NULL
		LIMIT 200
	`)
	if err != nil {
		log.Error("Failed to query overdue tasks", "error", err)
		return
	}
	defer rows.Close()

	type overdueTask struct {
		id, tenantID, boardID uuid.UUID
		title                 string
		dueDate               time.Time
		createdBy             *uuid.UUID
	}
	tasks := []overdueTask{}
	for rows.Next() {
		var t overdueTask
		if err := rows.Scan(&t.id, &t.tenantID, &t.boardID, &t.title, &t.dueDate, &t.createdBy); err != nil {
			log.Error("Failed to scan overdue task", "error", err)
			continue
		}
		tasks = append(tasks, t)
	}
	rows.Close()

	now := time.Now()
	notified := 0
	for _, t := range tasks {
		// Recipients: linked users of all assignees; fall back to the creator.
		recipients := map[uuid.UUID]bool{}
		aRows, err := db.Query(`
			SELECT e.user_id FROM task_assignees ta
			JOIN employees e ON e.id = ta.employee_id
			WHERE ta.task_id = $1 AND e.user_id IS NOT NULL AND e.deleted_at IS NULL
		`, t.id)
		if err == nil {
			for aRows.Next() {
				var uid uuid.UUID
				if err := aRows.Scan(&uid); err == nil && uid != uuid.Nil {
					recipients[uid] = true
				}
			}
			aRows.Close()
		}
		if len(recipients) == 0 && t.createdBy != nil && *t.createdBy != uuid.Nil {
			recipients[*t.createdBy] = true
		}

		dueStr := t.dueDate.Format("2006-01-02")
		dueDisplay := t.dueDate.Format("02.01.2006")
		for uid := range recipients {
			var lang string
			if err := db.QueryRow(`SELECT COALESCE(language, 'en') FROM users WHERE id = $1`, uid).Scan(&lang); err != nil || lang == "" {
				lang = "en"
			}
			tmpl, okT := notificationTemplates["task_overdue"][lang]
			if !okT {
				tmpl = notificationTemplates["task_overdue"]["en"]
			}
			dataJSON, _ := json.Marshal(map[string]interface{}{
				"task_id":    t.id.String(),
				"board_id":   t.boardID.String(),
				"task_title": t.title,
				"due_date":   dueStr,
			})
			if _, err := db.Exec(`
				INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
				VALUES ($1, $2, $3, 'task_overdue', $4, $5, $6::jsonb, 'in_app', 'normal', $7)
			`, uuid.New(), t.tenantID, uid, tmpl.Title, fmt.Sprintf(tmpl.Message, t.title, dueDisplay), string(dataJSON), now); err != nil {
				log.Error("Failed to insert overdue notification", "error", err, "task_id", t.id.String())
				continue
			}
			notified++
		}

		// One-shot stamp — with or without recipients.
		db.Exec(`UPDATE tasks SET overdue_notified_at = $2 WHERE id = $1`, t.id, now)
	}

	if len(tasks) > 0 {
		log.Info("Overdue task scan complete", "tasks", len(tasks), "notifications", notified)
	}
}

// checkStepTimeouts finds step logs that have exceeded their max_duration_hours
// and creates notifications or blocks them based on the on_timeout configuration
func checkStepTimeouts(db *database.DB, log logger.Logger) {
	rows, err := db.Query(`
		SELECT sl.id, sl.tenant_id, sl.operation_id, sl.step_sequence, sl.step_name,
		       ots.max_duration_hours, ots.on_timeout,
		       so.name, so.responsible_id
		FROM stock_operation_step_log sl
		JOIN stock_operations so ON so.id = sl.operation_id AND so.tenant_id = sl.tenant_id
		JOIN operation_type_steps ots ON ots.id = sl.step_id AND ots.tenant_id = sl.tenant_id
		WHERE sl.state = 'in_progress'
		  AND COALESCE(sl.timeout_notified, false) = false
		  AND ots.max_duration_hours IS NOT NULL
		  AND ots.max_duration_hours > 0
		  AND sl.started_at IS NOT NULL
		  AND sl.started_at + (ots.max_duration_hours || ' hours')::interval < NOW()
		  AND so.deleted_at IS NULL
	`)
	if err != nil {
		log.Error("Failed to query step timeouts", "error", err)
		return
	}
	defer rows.Close()

	now := time.Now()
	count := 0

	for rows.Next() {
		var (
			slID, tenantID, operationID uuid.UUID
			stepSequence                int
			stepName                    string
			maxDuration                 float64
			onTimeout                   string
			opName                      string
			responsibleID               *uuid.UUID
		)
		if err := rows.Scan(&slID, &tenantID, &operationID, &stepSequence, &stepName,
			&maxDuration, &onTimeout, &opName, &responsibleID); err != nil {
			continue
		}

		title := "Step timeout: " + opName
		message := "Step '" + stepName + "' has exceeded its time limit of " +
			time.Duration(maxDuration*float64(time.Hour)).String()

		switch onTimeout {
		case "block":
			// Block the step from being advanced
			db.Exec(`
				UPDATE stock_operation_step_log
				SET timeout_blocked = true, timeout_notified = true
				WHERE id = $1
			`, slID)

			// Notify responsible user
			if responsibleID != nil {
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'step_timeout_blocked', $4, $5, $6::jsonb, 'in_app', 'high', $7)
				`, uuid.New(), tenantID, *responsibleID, title, message+" (BLOCKED)",
					`{"operation_id":"`+operationID.String()+`","step":"`+stepName+`"}`, now)
			}

		case "escalate":
			// Notify warehouse manager
			var managerID *uuid.UUID
			db.QueryRow(`
				SELECT w.manager_id FROM warehouses w
				JOIN warehouse_operation_types wot ON wot.warehouse_id = w.id
				JOIN stock_operations so ON so.operation_type_id = wot.id
				WHERE so.id = $1 AND so.tenant_id = $2 AND w.manager_id IS NOT NULL
			`, operationID, tenantID).Scan(&managerID)

			if managerID != nil {
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'step_timeout_escalated', $4, $5, $6::jsonb, 'in_app', 'high', $7)
				`, uuid.New(), tenantID, *managerID, title+" (ESCALATED)", message,
					`{"operation_id":"`+operationID.String()+`","step":"`+stepName+`"}`, now)
			}

			db.Exec("UPDATE stock_operation_step_log SET timeout_notified = true WHERE id = $1", slID)

		default: // "warn"
			// Just notify the responsible user
			if responsibleID != nil {
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'step_timeout_warning', $4, $5, $6::jsonb, 'in_app', 'normal', $7)
				`, uuid.New(), tenantID, *responsibleID, title, message,
					`{"operation_id":"`+operationID.String()+`","step":"`+stepName+`"}`, now)
			}

			db.Exec("UPDATE stock_operation_step_log SET timeout_notified = true WHERE id = $1", slID)
		}

		count++
	}

	if count > 0 {
		log.Info("Processed step timeouts", "count", count)
	}
}

// checkReconciliationReminders handles reconciliation act follow-ups:
// - 3 days no response: notify the act creator (buxgalter)
// - 7 days no response: notify creator + admins, auto-set status to 'no_response'
// - Response received: notify creator about confirmation/dispute
func checkReconciliationReminders(db *database.DB, log logger.Logger) {
	now := time.Now()
	count := 0

	// 1. Acts sent > 3 days ago, no response, 3-day reminder not yet sent
	rows3d, err := db.Query(`
		SELECT ra.id, ra.tenant_id, ra.created_by, COALESCE(ct.name, 'Kontragent')
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.status = 'sent'
		  AND (ra.response_status IS NULL OR ra.response_status = '' OR ra.response_status = 'no_response')
		  AND COALESCE(ra.reminder_3d_sent, false) = false
		  AND ra.sent_at IS NOT NULL
		  AND ra.sent_at + INTERVAL '3 days' < NOW()
		  AND ra.deleted_at IS NULL
	`)
	if err != nil {
		log.Error("Failed to query 3-day reconciliation reminders", "error", err)
	} else {
		defer rows3d.Close()
		for rows3d.Next() {
			var actID, tenantID uuid.UUID
			var createdBy *uuid.UUID
			var partnerName string
			if err := rows3d.Scan(&actID, &tenantID, &createdBy, &partnerName); err != nil {
				continue
			}
			if createdBy != nil {
				// `partner_name` + `days` packed into data so the web renderer
				// can rebuild body in the current UI language. Additive —
				// mobile reads title/message unchanged.
				notifData, _ := json.Marshal(map[string]interface{}{
					"reconciliation_id": actID.String(),
					"partner_name":      partnerName,
					"days":              3,
				})
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'reconciliation_reminder', $4, $5, $6::jsonb, 'in_app', 'normal', $7)
				`, uuid.New(), tenantID, *createdBy,
					"Akt sverka javobsiz",
					partnerName+" hali akt sverkaga javob bermadi (3 kun)",
					string(notifData), now)
			}
			db.Exec("UPDATE reconciliation_acts SET reminder_3d_sent = true WHERE id = $1", actID)
			count++
		}
	}

	// 2. Acts sent > 7 days ago, no response, 7-day reminder not yet sent
	rows7d, err := db.Query(`
		SELECT ra.id, ra.tenant_id, ra.created_by, COALESCE(ct.name, 'Kontragent')
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.status = 'sent'
		  AND (ra.response_status IS NULL OR ra.response_status = '' OR ra.response_status = 'no_response')
		  AND COALESCE(ra.reminder_7d_sent, false) = false
		  AND ra.sent_at IS NOT NULL
		  AND ra.sent_at + INTERVAL '7 days' < NOW()
		  AND ra.deleted_at IS NULL
	`)
	if err != nil {
		log.Error("Failed to query 7-day reconciliation reminders", "error", err)
	} else {
		defer rows7d.Close()
		for rows7d.Next() {
			var actID, tenantID uuid.UUID
			var createdBy *uuid.UUID
			var partnerName string
			if err := rows7d.Scan(&actID, &tenantID, &createdBy, &partnerName); err != nil {
				continue
			}

			// partner_name + days packed into data for web re-render. Additive.
			notifData7d, _ := json.Marshal(map[string]interface{}{
				"reconciliation_id": actID.String(),
				"partner_name":      partnerName,
				"days":              7,
			})

			// Notify creator
			if createdBy != nil {
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'reconciliation_no_response', $4, $5, $6::jsonb, 'in_app', 'high', $7)
				`, uuid.New(), tenantID, *createdBy,
					"Akt sverka javobsiz — eslatma yuboring",
					partnerName+" 7 kun davomida akt sverkaga javob bermadi",
					string(notifData7d), now)
			}

			// Notify admins
			adminRows, _ := db.Query(`
				SELECT tu.user_id FROM tenant_users tu
				WHERE tu.tenant_id = $1 AND tu.role IN ('admin', 'owner') AND tu.deleted_at IS NULL
			`, tenantID)
			if adminRows != nil {
				for adminRows.Next() {
					var adminID uuid.UUID
					if adminRows.Scan(&adminID) == nil && (createdBy == nil || adminID != *createdBy) {
						db.Exec(`
							INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
							VALUES ($1, $2, $3, 'reconciliation_no_response', $4, $5, $6::jsonb, 'in_app', 'high', $7)
						`, uuid.New(), tenantID, adminID,
							"Akt sverka javobsiz — eslatma yuboring",
							partnerName+" 7 kun davomida akt sverkaga javob bermadi",
							string(notifData7d), now)
					}
				}
				adminRows.Close()
			}

			// Auto-set status to no_response
			db.Exec("UPDATE reconciliation_acts SET response_status = 'no_response', reminder_7d_sent = true WHERE id = $1", actID)
			count++
		}
	}

	// 3. Acts where partner responded (confirmed/disputed) but creator not yet notified
	rowsResp, err := db.Query(`
		SELECT ra.id, ra.tenant_id, ra.created_by, COALESCE(ct.name, 'Kontragent'), ra.response_status
		FROM reconciliation_acts ra
		LEFT JOIN contacts ct ON ra.partner_id = ct.id
		WHERE ra.response_status IN ('confirmed', 'disputed')
		  AND COALESCE(ra.response_notified, false) = false
		  AND ra.deleted_at IS NULL
	`)
	if err != nil {
		log.Error("Failed to query reconciliation responses", "error", err)
	} else {
		defer rowsResp.Close()
		for rowsResp.Next() {
			var actID, tenantID uuid.UUID
			var createdBy *uuid.UUID
			var partnerName, respStatus string
			if err := rowsResp.Scan(&actID, &tenantID, &createdBy, &partnerName, &respStatus); err != nil {
				continue
			}

			var title, message, priority string
			if respStatus == "confirmed" {
				title = "Akt sverka tasdiqlandi"
				message = partnerName + " akt svkerni tasdiqladi"
				priority = "normal"
			} else {
				title = "Akt sverka bahsli"
				message = partnerName + " akt sverkaga norozilik bildirdi — tekshirish kerak"
				priority = "high"
			}

			// partner_name + response packed in data; additive.
			respData, _ := json.Marshal(map[string]interface{}{
				"reconciliation_id": actID.String(),
				"partner_name":      partnerName,
				"response":          respStatus,
			})

			if createdBy != nil {
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, 'in_app', $8, $9)
				`, uuid.New(), tenantID, *createdBy,
					"reconciliation_response", title, message,
					string(respData), priority, now)
			}

			// If disputed, also notify admins
			if respStatus == "disputed" {
				adminRows, _ := db.Query(`
					SELECT tu.user_id FROM tenant_users tu
					WHERE tu.tenant_id = $1 AND tu.role IN ('admin', 'owner') AND tu.deleted_at IS NULL
				`, tenantID)
				if adminRows != nil {
					for adminRows.Next() {
						var adminID uuid.UUID
						if adminRows.Scan(&adminID) == nil && (createdBy == nil || adminID != *createdBy) {
							db.Exec(`
								INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
								VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, 'in_app', 'high', $8)
							`, uuid.New(), tenantID, adminID,
								"reconciliation_response", title, message,
								string(respData), now)
						}
					}
					adminRows.Close()
				}
			}

			db.Exec("UPDATE reconciliation_acts SET response_notified = true WHERE id = $1", actID)
			count++
		}
	}

	if count > 0 {
		log.Info("Processed reconciliation reminders", "count", count)
	}
}

// runAutoDepreciation runs monthly depreciation on the 1st of each month.
// It checks all tenants' active assets and creates depreciation entries automatically.
func runAutoDepreciation(db *database.DB, log logger.Logger) {
	now := time.Now()

	// Only run on the 1st day of the month (between midnight and 1am to allow hourly retries)
	if now.Day() != 1 {
		return
	}

	period := now.Format("2006-01")
	deprDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	log.Info("Running auto depreciation", "period", period)

	// Get all tenants that have active assets
	tenantRows, err := db.Query(`
		SELECT DISTINCT tenant_id FROM fixed_assets
		WHERE status = 'active' AND deleted_at IS NULL AND remaining_months > 0
	`)
	if err != nil {
		log.Error("Auto depreciation: failed to query tenants", "error", err)
		return
	}
	defer tenantRows.Close()

	totalProcessed := 0

	for tenantRows.Next() {
		var tenantID uuid.UUID
		if err := tenantRows.Scan(&tenantID); err != nil {
			continue
		}

		// Look up accounts for this tenant
		deprExpenseAcct := findAccountBg(db, tenantID, "depreciation expense", "9470")
		accumDeprAcct := findAccountBg(db, tenantID, "accumulated depreciation", "0200")
		var journalID uuid.UUID
		var nextNumber int
		_ = db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('ASSET','MISC','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE code WHEN 'ASSET' THEN 0 WHEN 'MISC' THEN 1 ELSE 2 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNumber)

		// Get all active assets for this tenant
		assetRows, err := db.Query(`
			SELECT id, organization_id, acquisition_cost, salvage_value, useful_life_months, depreciation_method,
				   accumulated_depreciation, COALESCE(current_value, acquisition_cost - accumulated_depreciation),
				   COALESCE(remaining_months, useful_life_months), COALESCE(monthly_depr, 0)
			FROM fixed_assets
			WHERE tenant_id = $1 AND status = 'active' AND deleted_at IS NULL AND remaining_months > 0
		`, tenantID)
		if err != nil {
			log.Error("Auto depreciation: failed to query assets", "tenant", tenantID, "error", err)
			continue
		}

		for assetRows.Next() {
			var assetID uuid.UUID
			var orgID *uuid.UUID
			var acquisitionCost, salvageValue, accumulatedDepr, curValue, monthlyDeprAmt float64
			var usefulLifeMonths, remMonths int
			var deprMethod string

			if err := assetRows.Scan(&assetID, &orgID, &acquisitionCost, &salvageValue, &usefulLifeMonths,
				&deprMethod, &accumulatedDepr, &curValue, &remMonths, &monthlyDeprAmt); err != nil {
				continue
			}

			// Skip if already depreciated this period
			var existingID uuid.UUID
			if db.QueryRow("SELECT id FROM depreciation_entries WHERE asset_id = $1 AND period = $2", assetID, period).Scan(&existingID) == nil {
				continue
			}

			// Calculate depreciation
			depreciableAmount := acquisitionCost - salvageValue
			remainingValue := depreciableAmount - accumulatedDepr
			if remainingValue <= 0 {
				continue
			}

			// Compute by the asset's method — same logic as RunDepreciation
			// (fixed_asset.go). Declining-balance methods apply the rate to net
			// book value (cost − accumulated) and must be recomputed each
			// period, so the stored straight-line monthly_depr must not
			// override them (that made every method behave straight-line).
			var depAmount float64
			nbv := acquisitionCost - accumulatedDepr
			switch deprMethod {
			case "declining_balance":
				depAmount = nbv * (1.0 / float64(usefulLifeMonths))
			case "double_declining":
				depAmount = nbv * (2.0 / float64(usefulLifeMonths))
			case "straight_line":
				depAmount = depreciableAmount / float64(usefulLifeMonths)
			default:
				if monthlyDeprAmt > 0 {
					depAmount = monthlyDeprAmt
				} else {
					depAmount = depreciableAmount / float64(usefulLifeMonths)
				}
			}

			if depAmount > remainingValue {
				depAmount = remainingValue
			}

			newAccumulated := accumulatedDepr + depAmount
			newBookValue := acquisitionCost - newAccumulated
			newRemaining := remMonths - 1
			newCurrentValue := curValue - depAmount
			var newMonthlyDepr float64
			if newRemaining > 0 {
				newMonthlyDepr = newCurrentValue / float64(newRemaining)
			}

			// Insert depreciation entry
			entryID := uuid.New()
			if _, err := db.Exec(`
				INSERT INTO depreciation_entries (
					id, tenant_id, organization_id, asset_id, period, depreciation_date, depreciation_amount,
					accumulated_total, book_value_after, depreciation_method, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				entryID, tenantID, orgID, assetID, period, deprDate, depAmount,
				newAccumulated, newBookValue, deprMethod, now); err != nil {
				log.Error("Auto depreciation: failed to insert entry", "asset", assetID, "error", err)
				continue
			}

			// Update asset
			db.Exec(`
				UPDATE fixed_assets SET
					accumulated_depreciation = $1, book_value = $2, current_value = $3,
					remaining_months = $4, monthly_depr = $5, last_depr_date = $6, updated_at = $7
				WHERE id = $8`,
				newAccumulated, newBookValue, newCurrentValue, newRemaining, newMonthlyDepr, deprDate, now, assetID)

			// Create journal entry (header + lines + balances in one tx; migration
			// 416's deferred trigger rejects imbalanced single-line inserts).
			if deprExpenseAcct != uuid.Nil && accumDeprAcct != uuid.Nil && journalID != uuid.Nil {
				jeID := uuid.New()
				entryNumber := fmt.Sprintf("DEP%06d", nextNumber)
				nextNumber++

				if tx, txErr := db.Begin(); txErr == nil {
					committed := false
					func() {
						defer func() {
							if !committed {
								tx.Rollback()
							}
						}()
						if _, e := tx.Exec(`
							INSERT INTO journal_entries (
								id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
								source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
							) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'depreciation', $9, 1.0, $10, $10, 'posted', $11, $11)`,
							jeID, tenantID, orgID, journalID, entryNumber, deprDate,
							period, fmt.Sprintf("Auto amortizatsiya %s", period),
							assetID.String(), depAmount, now); e != nil {
							return
						}
						if _, e := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
							VALUES ($1, $2, 1, $3, 'Amortizatsiya xarajati', $4, 0, 1.0, $5)`, uuid.New(), jeID, deprExpenseAcct, depAmount, now); e != nil {
							return
						}
						if _, e := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
							VALUES ($1, $2, 2, $3, 'Yig''ilgan amortizatsiya', 0, $4, 1.0, $5)`, uuid.New(), jeID, accumDeprAcct, depAmount, now); e != nil {
							return
						}
						// Debit-positive convention (migration 407): expense +, accumulated depreciation (contra-asset) −.
						if _, e := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", depAmount, now, deprExpenseAcct); e != nil {
							return
						}
						if _, e := tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", depAmount, now, accumDeprAcct); e != nil {
							return
						}
						if _, e := tx.Exec("UPDATE depreciation_entries SET journal_entry_id = $1 WHERE id = $2", jeID, entryID); e != nil {
							return
						}
						if e := tx.Commit(); e == nil {
							committed = true
						}
					}()
				}
			}

			totalProcessed++
		}
		assetRows.Close()

		// Update journal next_number
		if journalID != uuid.Nil {
			db.Exec("UPDATE journals SET next_number = $1, updated_at = $2 WHERE id = $3", nextNumber, now, journalID)
		}
	}

	if totalProcessed > 0 {
		log.Info("Auto depreciation completed", "period", period, "assets_processed", totalProcessed)
	}
}

// findAccountBg is a background-job version of findAccount (no handler context needed)
func findAccountBg(db *database.DB, tenantID uuid.UUID, nameLike, codeFallback string) uuid.UUID {
	var id uuid.UUID
	err := db.QueryRow(`
		SELECT id FROM accounts
		WHERE tenant_id = $1 AND deleted_at IS NULL AND (LOWER(name) LIKE '%' || $2 || '%' OR code = $3)
		ORDER BY CASE WHEN code = $3 THEN 0 ELSE 1 END
		LIMIT 1`, tenantID, nameLike, codeFallback).Scan(&id)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// checkActivityReminders scans the `activities` table for any planned
// activity whose reminder_datetime has passed and whose reminder_sent
// flag is still false, then inserts an in-app notification for the
// assigned user (falling back to the creator) and flips reminder_sent
// to true so the same activity isn't notified twice.
//
// Recipient priority: assigned_to > created_by. If neither is set,
// the activity is silently skipped — there's no one to notify.
//
// Notification metadata (lead_id, activity_id, activity_type) is stored
// in the JSONB `data` column so the web/mobile clients can deep-link
// the user back to the lead detail screen on click.
func checkActivityReminders(db *database.DB, log logger.Logger) {
	// Diagnostic: count how many rows match BEFORE we process them so
	// the operator can see whether the worker is finding candidates
	// even when delivery later fails.
	var candidateCount int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM activities a
		WHERE a.reminder_datetime IS NOT NULL
		  AND a.reminder_datetime <= NOW()
		  AND COALESCE(a.reminder_sent, false) = false
		  AND a.status = 'planned'
		  AND a.deleted_at IS NULL
	`).Scan(&candidateCount)
	if candidateCount > 0 {
		log.Info("CRM activity reminders: candidates found", "count", candidateCount)
	}

	rows, err := db.Query(`
		SELECT a.id, a.tenant_id, a.activity_type, a.subject, a.description,
		       a.lead_id, a.start_datetime, a.reminder_datetime,
		       a.assigned_to, a.created_by,
		       l.contact_name, l.company_name
		FROM activities a
		LEFT JOIN leads l ON l.id = a.lead_id AND l.tenant_id = a.tenant_id
		WHERE a.reminder_datetime IS NOT NULL
		  AND a.reminder_datetime <= NOW()
		  AND COALESCE(a.reminder_sent, false) = false
		  AND a.status = 'planned'
		  AND a.deleted_at IS NULL
		LIMIT 200
	`)
	if err != nil {
		log.Error("Failed to query due activity reminders", "error", err)
		return
	}
	defer rows.Close()

	now := time.Now()
	count := 0
	skippedNoRecipient := 0

	for rows.Next() {
		var (
			activityID, tenantID            uuid.UUID
			activityType, subject           string
			description                     *string
			leadID                          *uuid.UUID
			startDatetime, reminderDatetime *time.Time
			assignedTo, createdBy           *uuid.UUID
			contactName, companyName        *string
		)
		if err := rows.Scan(&activityID, &tenantID, &activityType, &subject, &description,
			&leadID, &startDatetime, &reminderDatetime,
			&assignedTo, &createdBy, &contactName, &companyName); err != nil {
			log.Error("Failed to scan activity reminder row", "error", err)
			continue
		}

		// Pick recipient — assigned user wins; fall back to creator.
		var recipientID *uuid.UUID
		if assignedTo != nil && *assignedTo != uuid.Nil {
			recipientID = assignedTo
		} else if createdBy != nil && *createdBy != uuid.Nil {
			recipientID = createdBy
		}

		// Mark as sent regardless of whether we have a recipient, so
		// orphan activities don't get re-scanned forever.
		if recipientID == nil {
			log.Warn("CRM activity reminder skipped — no recipient", "activity_id", activityID.String())
			skippedNoRecipient++
			db.Exec(`UPDATE activities SET reminder_sent = true, updated_at = $2 WHERE id = $1`, activityID, now)
			continue
		}

		// Build a friendly title + message.
		title := subject
		if title == "" {
			switch activityType {
			case "call":
				title = "Reminder: Call"
			case "meeting":
				title = "Reminder: Meeting"
			case "email":
				title = "Reminder: Email"
			default:
				title = "Reminder: Follow-up"
			}
		}

		// Build the contact label for the message body.
		who := ""
		if contactName != nil && *contactName != "" {
			who = *contactName
		}
		if companyName != nil && *companyName != "" {
			if who != "" {
				who = who + " (" + *companyName + ")"
			} else {
				who = *companyName
			}
		}

		message := ""
		if who != "" {
			message = "Lead: " + who
		}
		if description != nil && *description != "" {
			if message != "" {
				message = message + " — " + *description
			} else {
				message = *description
			}
		}

		// Pack metadata into the JSONB data so the web client can:
		//   1. Re-render title/message in whatever language the user
		//      has selected right now (mobile uses the frozen
		//      `title`/`message` strings instead).
		//   2. Deep-link to the lead detail screen on click.
		dataMap := map[string]interface{}{
			"activity_id":   activityID.String(),
			"activity_type": activityType,
			"lead_name":     who, // already combined contact + company
		}
		if leadID != nil {
			dataMap["lead_id"] = leadID.String()
		}
		if startDatetime != nil {
			dataMap["start_datetime"] = startDatetime.Format(time.RFC3339)
		}
		if description != nil && *description != "" {
			dataMap["description"] = *description
		}
		dataJSON, _ := json.Marshal(dataMap)

		_, err := db.Exec(`
			INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
			VALUES ($1, $2, $3, 'crm_activity_reminder', $4, $5, $6::jsonb, 'in_app', 'normal', $7)
		`, uuid.New(), tenantID, *recipientID, title, message, string(dataJSON), now)
		if err != nil {
			log.Error("Failed to insert activity reminder notification", "error", err, "activity_id", activityID.String())
			continue
		}

		// Flip the flag so we don't re-notify on the next tick.
		_, err = db.Exec(`UPDATE activities SET reminder_sent = true, updated_at = $2 WHERE id = $1`, activityID, now)
		if err != nil {
			log.Error("Failed to mark activity reminder_sent", "error", err, "activity_id", activityID.String())
			continue
		}

		count++
	}

	if count > 0 || skippedNoRecipient > 0 {
		log.Info("CRM activity reminders processed",
			"sent", count,
			"skipped_no_recipient", skippedNoRecipient)
	}
}

// checkContractExpiryNotifications notifies the responsible employee (or
// the contract's creator when no responsible is set) at 30/14/3 days
// before an active contract's end_date. Each contract+threshold pair
// notifies exactly once — the contract_expiry_notifications table is the
// dedupe ledger, so a restart or a re-run never double-sends.
func checkContractExpiryNotifications(db *database.DB, log logger.Logger) {
	rows, err := db.Query(`
		SELECT c.id, c.tenant_id, c.contract_number, c.title, c.end_date,
		       (c.end_date - CURRENT_DATE) AS days_left,
		       e.user_id, c.created_by
		FROM procurement_contracts c
		LEFT JOIN employees e ON e.id = c.responsible_employee_id AND e.tenant_id = c.tenant_id
		WHERE c.status = 'active' AND c.deleted_at IS NULL AND c.archived_at IS NULL
		  AND c.end_date IS NOT NULL
		  AND c.end_date >= CURRENT_DATE
		  AND c.end_date <= CURRENT_DATE + INTERVAL '30 days'
	`)
	if err != nil {
		log.Error("Failed to scan contracts for expiry notifications", "error", err)
		return
	}
	defer rows.Close()

	type row struct {
		id             uuid.UUID
		tenantID       uuid.UUID
		contractNumber string
		title          string
		endDate        time.Time
		daysLeft       int
		recipient      uuid.UUID
	}
	var contracts []row
	for rows.Next() {
		var (
			r          row
			userID     *uuid.UUID
			createdBy  *uuid.UUID
		)
		if err := rows.Scan(&r.id, &r.tenantID, &r.contractNumber, &r.title, &r.endDate, &r.daysLeft, &userID, &createdBy); err != nil {
			continue
		}
		if userID != nil && *userID != uuid.Nil {
			r.recipient = *userID
		} else if createdBy != nil && *createdBy != uuid.Nil {
			r.recipient = *createdBy
		} else {
			continue // nobody to notify
		}
		contracts = append(contracts, r)
	}
	rows.Close()

	thresholds := []int{30, 14, 3}
	now := time.Now()
	sent := 0
	for _, ct := range contracts {
		for _, threshold := range thresholds {
			if ct.daysLeft > threshold {
				continue
			}
			// Claim the threshold first; if another instance already sent
			// it, the unique constraint makes this a no-op.
			res, err := db.Exec(`
				INSERT INTO contract_expiry_notifications (id, tenant_id, contract_id, threshold_days, sent_at)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (contract_id, threshold_days) DO NOTHING
			`, uuid.New(), ct.tenantID, ct.id, threshold, now)
			if err != nil {
				log.Error("Failed to claim contract expiry threshold", "error", err, "contract_id", ct.id.String())
				continue
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue // already notified for this threshold
			}

			var lang string
			if err := db.QueryRow(`SELECT COALESCE(language, 'en') FROM users WHERE id = $1`, ct.recipient).Scan(&lang); err != nil || lang == "" {
				lang = "en"
			}
			tmpl, okT := notificationTemplates["contract_expiring"][lang]
			if !okT {
				tmpl = notificationTemplates["contract_expiring"]["en"]
			}
			endStr := ct.endDate.Format("2006-01-02")
			endDisplay := ct.endDate.Format("02.01.2006")
			dataJSON, _ := json.Marshal(map[string]interface{}{
				"contract_id":     ct.id.String(),
				"contract_number": ct.contractNumber,
				"end_date":        endStr,
				"days_left":       ct.daysLeft,
				"threshold_days":  threshold,
			})
			if _, err := db.Exec(`
				INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
				VALUES ($1, $2, $3, 'contract_expiring', $4, $5, $6::jsonb, 'in_app', 'normal', $7)
			`, uuid.New(), ct.tenantID, ct.recipient, tmpl.Title,
				fmt.Sprintf(tmpl.Message, ct.contractNumber, ct.title, ct.daysLeft, endDisplay),
				string(dataJSON), now); err != nil {
				log.Error("Failed to insert contract expiry notification", "error", err, "contract_id", ct.id.String())
				continue
			}
			sent++
		}
	}

	if sent > 0 {
		log.Info("Contract expiry notifications sent", "count", sent)
	}
}
