package handler

import (
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

	log.Info("Background jobs started (step timeout checker every 15 min, reconciliation reminders every 1 hour, auto depreciation hourly)")
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
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'reconciliation_reminder', $4, $5, $6::jsonb, 'in_app', 'normal', $7)
				`, uuid.New(), tenantID, *createdBy,
					"Akt sverka javobsiz",
					partnerName+" hali akt sverkaga javob bermadi (3 kun)",
					`{"reconciliation_id":"`+actID.String()+`"}`, now)
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

			// Notify creator
			if createdBy != nil {
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, 'reconciliation_no_response', $4, $5, $6::jsonb, 'in_app', 'high', $7)
				`, uuid.New(), tenantID, *createdBy,
					"Akt sverka javobsiz — eslatma yuboring",
					partnerName+" 7 kun davomida akt sverkaga javob bermadi",
					`{"reconciliation_id":"`+actID.String()+`"}`, now)
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
							`{"reconciliation_id":"`+actID.String()+`"}`, now)
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

			if createdBy != nil {
				db.Exec(`
					INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, 'in_app', $8, $9)
				`, uuid.New(), tenantID, *createdBy,
					"reconciliation_response", title, message,
					`{"reconciliation_id":"`+actID.String()+`","response":"`+respStatus+`"}`, priority, now)
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
								`{"reconciliation_id":"`+actID.String()+`","response":"`+respStatus+`"}`, now)
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
		deprExpenseAcct := findAccountBg(db, tenantID, "depreciation expense", "6500")
		accumDeprAcct := findAccountBg(db, tenantID, "accumulated depreciation", "1510")
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

			var depAmount float64
			if monthlyDeprAmt > 0 {
				depAmount = monthlyDeprAmt
			} else {
				switch deprMethod {
				case "straight_line":
					depAmount = depreciableAmount / float64(usefulLifeMonths)
				case "declining_balance":
					depAmount = curValue * (1.0 / float64(usefulLifeMonths))
				case "double_declining":
					depAmount = curValue * (2.0 / float64(usefulLifeMonths))
				default:
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

			// Create journal entry
			if deprExpenseAcct != uuid.Nil && accumDeprAcct != uuid.Nil && journalID != uuid.Nil {
				jeID := uuid.New()
				entryNumber := fmt.Sprintf("DEP%06d", nextNumber)
				nextNumber++

				db.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
						source_type, source_id, exchange_rate, total_debit, total_credit, status, created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'depreciation', $9, 1.0, $10, $10, 'posted', $11, $11)`,
					jeID, tenantID, orgID, journalID, entryNumber, deprDate,
					period, fmt.Sprintf("Auto amortizatsiya %s", period),
					assetID.String(), depAmount, now)
				db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
					VALUES ($1, $2, 1, $3, 'Amortizatsiya xarajati', $4, 0, 1.0, $5)`, uuid.New(), jeID, deprExpenseAcct, depAmount, now)
				db.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, description, debit_amount, credit_amount, exchange_rate, created_at)
					VALUES ($1, $2, 2, $3, 'Yig''ilgan amortizatsiya', 0, $4, 1.0, $5)`, uuid.New(), jeID, accumDeprAcct, depAmount, now)
				db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", depAmount, now, deprExpenseAcct)
				db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", depAmount, now, accumDeprAcct)
				db.Exec("UPDATE depreciation_entries SET journal_entry_id = $1 WHERE id = $2", jeID, entryID)
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
