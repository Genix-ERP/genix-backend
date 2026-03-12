package handler

import (
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
	log.Info("Background jobs started (step timeout checker every 15 min)")
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
