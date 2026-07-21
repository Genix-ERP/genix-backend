package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RunPushDispatcher periodically delivers a mobile push for notifications that
// were inserted by paths NOT going through createNotification (background jobs
// in background_jobs.go, and scheduler_reminders.go). Rows created via
// createNotification stamp push_sent_at themselves and push inline, so they're
// skipped here. Best-effort; the loop stops when ctx is cancelled.
func (h *Handler) RunPushDispatcher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.dispatchPendingPush()
			}
		}
	}()
}

func (h *Handler) dispatchPendingPush() {
	// When push isn't configured, just mark the pending rows processed so they
	// don't accumulate — enabling FCM later then won't backfill a backlog.
	if !h.fcm.Enabled() {
		h.db.Exec(`UPDATE notifications SET push_sent_at = NOW() WHERE push_sent_at IS NULL`)
		return
	}

	rows, err := h.db.Query(`
		SELECT id, tenant_id, user_id, type, title, COALESCE(message, ''), COALESCE(data::text, '{}')
		FROM notifications
		WHERE push_sent_at IS NULL
		ORDER BY created_at
		LIMIT 500
	`)
	if err != nil {
		h.log.Error("push dispatcher: query failed", "error", err)
		return
	}

	type pending struct {
		id       uuid.UUID
		tenantID uuid.UUID
		userID   uuid.UUID
		ntype    string
		title    string
		message  string
		data     string
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.tenantID, &p.userID, &p.ntype, &p.title, &p.message, &p.data); err != nil {
			continue
		}
		batch = append(batch, p)
	}
	rows.Close()

	for _, p := range batch {
		data := map[string]string{"type": p.ntype}
		var raw map[string]interface{}
		if json.Unmarshal([]byte(p.data), &raw) == nil {
			for k, v := range raw {
				data[k] = fmt.Sprintf("%v", v)
			}
		}
		h.pushToUser(p.tenantID, p.userID, p.title, p.message, data)
		// Mark processed regardless of per-token delivery outcome: pushToUser is
		// best-effort and prunes dead tokens itself, so we never want to retry
		// this row and risk duplicate deliveries.
		h.db.Exec(`UPDATE notifications SET push_sent_at = NOW() WHERE id = $1`, p.id)
	}
}
