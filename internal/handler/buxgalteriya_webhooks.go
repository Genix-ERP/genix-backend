package handler

// File: buxgalteriya_webhooks.go
//
// TT Buxgalteriya ERP §7.4 — generic webhook subscriptions + dispatcher.
//
// Flow:
//   1. Tenant registers a subscription (URL, event list, secret) via POST /webhooks.
//   2. Business handlers (CreateJournalEntry, PostJournalEntry, ClosePeriod, etc.)
//      call h.DispatchWebhookEvent(...) after a successful commit.
//   3. DispatchWebhookEvent enqueues a webhook_deliveries row in 'pending' state
//      and fires it in a goroutine. Failed attempts are re-queued with exponential
//      backoff up to max_retries.
//
// HMAC-SHA256 signature over the raw JSON payload using the subscription secret
// is added as the X-Genix-Signature header.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// ---------------------------------------------------------------------------
// CRUD for subscriptions
// ---------------------------------------------------------------------------

type webhookSubscriptionInput struct {
	Name       string   `json:"name" binding:"required,min=1,max=255"`
	URL        string   `json:"url" binding:"required,url"`
	Events     []string `json:"events" binding:"required,min=1"`
	Secret     string   `json:"secret"`
	IsActive   *bool    `json:"is_active"`
	MaxRetries *int     `json:"max_retries"`
	TimeoutMS  *int     `json:"timeout_ms"`
}

// CreateWebhookSubscription registers a new subscription.
func (h *Handler) CreateWebhookSubscription(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var in webhookSubscriptionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}

	id := uuid.New()
	now := time.Now()

	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}
	maxRetries := 5
	if in.MaxRetries != nil && *in.MaxRetries > 0 {
		maxRetries = *in.MaxRetries
	}
	timeoutMS := 10000
	if in.TimeoutMS != nil && *in.TimeoutMS > 0 {
		timeoutMS = *in.TimeoutMS
	}

	_, err := h.db.Exec(`
		INSERT INTO webhook_subscriptions (
			id, tenant_id, organization_id, name, url, events, secret,
			is_active, max_retries, timeout_ms, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
	`, id, tenantID, orgPtr, in.Name, in.URL, pq.Array(in.Events), in.Secret,
		isActive, maxRetries, timeoutMS, userID, now)
	if err != nil {
		h.log.Error("CreateWebhookSubscription: insert failed", "error", err)
		response.InternalError(c, "Failed to create subscription")
		return
	}

	response.Created(c, gin.H{
		"id": id, "name": in.Name, "url": in.URL, "events": in.Events,
		"is_active": isActive,
	})
}

// ListWebhookSubscriptions returns all subscriptions for the tenant.
func (h *Handler) ListWebhookSubscriptions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, name, url, events, is_active, max_retries, timeout_ms,
		       last_triggered_at, last_status, created_at
		FROM webhook_subscriptions
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to list")
		return
	}
	defer rows.Close()

	out := make([]gin.H, 0)
	for rows.Next() {
		var (
			id                    uuid.UUID
			name, url             string
			events                []string
			isActive              bool
			maxRetries, timeoutMS int
			lastTriggeredAt       *time.Time
			lastStatus            *string
			createdAt             time.Time
		)
		if err := rows.Scan(&id, &name, &url, pq.Array(&events), &isActive,
			&maxRetries, &timeoutMS, &lastTriggeredAt, &lastStatus, &createdAt); err != nil {
			continue
		}
		row := gin.H{
			"id": id, "name": name, "url": url, "events": events,
			"is_active": isActive, "max_retries": maxRetries,
			"timeout_ms": timeoutMS, "created_at": createdAt,
		}
		if lastTriggeredAt != nil {
			row["last_triggered_at"] = lastTriggeredAt
		}
		if lastStatus != nil {
			row["last_status"] = *lastStatus
		}
		out = append(out, row)
	}
	response.Success(c, out)
}

// DeleteWebhookSubscription removes a subscription by ID.
func (h *Handler) DeleteWebhookSubscription(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}

	res, err := h.db.Exec(
		`DELETE FROM webhook_subscriptions WHERE id = $1 AND tenant_id = $2`,
		id, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Subscription")
		return
	}
	response.NoContent(c)
}

// ListWebhookDeliveries returns recent delivery attempts for a subscription.
func (h *Handler) ListWebhookDeliveries(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	subIDStr := c.Param("id")
	subID, err := uuid.Parse(subIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid id")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, event_name, attempt_number, response_status, status,
		       duration_ms, error_message, created_at, completed_at
		FROM webhook_deliveries
		WHERE subscription_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT 100
	`, subID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to list")
		return
	}
	defer rows.Close()

	out := make([]gin.H, 0)
	for rows.Next() {
		var (
			id                uuid.UUID
			eventName, status string
			attempt           int
			respStatus        *int
			durationMS        *int
			errorMessage      *string
			createdAt         time.Time
			completedAt       *time.Time
		)
		if err := rows.Scan(&id, &eventName, &attempt, &respStatus, &status,
			&durationMS, &errorMessage, &createdAt, &completedAt); err != nil {
			continue
		}
		out = append(out, gin.H{
			"id": id, "event_name": eventName, "attempt_number": attempt,
			"response_status": respStatus, "status": status, "duration_ms": durationMS,
			"error_message": errorMessage, "created_at": createdAt, "completed_at": completedAt,
		})
	}
	response.Success(c, out)
}

// ---------------------------------------------------------------------------
// Dispatcher (call from business handlers)
// ---------------------------------------------------------------------------

// DispatchWebhookEvent enqueues deliveries for all active subscriptions in
// the tenant that match `eventName`, then fires them asynchronously.
// Call this AFTER a successful commit — we do NOT guarantee the caller's
// transaction semantics across webhook delivery.
//
// Typical call sites (TT Buxgalteriya):
//   - After PostJournalEntry           → "journal_entry.posted"
//   - After ReverseJournalEntry        → "journal_entry.reversed"
//   - After ClosePeriod success        → "period.closed"
//   - After ApproveEInvoice            → "einvoice.approved"
//   - After payment confirm            → "payment.confirmed"
func (h *Handler) DispatchWebhookEvent(
	tenantID uuid.UUID, eventName string, payload interface{},
) {
	// Find subscriptions interested in this event
	rows, err := h.db.Query(`
		SELECT id, url, secret, max_retries, timeout_ms
		FROM webhook_subscriptions
		WHERE tenant_id = $1 AND is_active = true
		  AND $2 = ANY(events)
	`, tenantID, eventName)
	if err != nil {
		h.log.Error("DispatchWebhookEvent: subscription lookup failed", "error", err)
		return
	}
	defer rows.Close()

	type sub struct {
		id       uuid.UUID
		url      string
		secret   string
		maxRetry int
		timeout  int
	}
	var subs []sub
	for rows.Next() {
		var s sub
		if err := rows.Scan(&s.id, &s.url, &s.secret, &s.maxRetry, &s.timeout); err == nil {
			subs = append(subs, s)
		}
	}
	if len(subs) == 0 {
		return
	}

	eventID := uuid.New()
	body, err := json.Marshal(gin.H{
		"event":     eventName,
		"event_id":  eventID,
		"tenant_id": tenantID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data":      payload,
	})
	if err != nil {
		h.log.Error("DispatchWebhookEvent: marshal failed", "error", err)
		return
	}

	for _, s := range subs {
		sig := signWebhookBody(s.secret, body)
		deliveryID := uuid.New()
		// Record queued delivery first so we have a paper trail even if the
		// worker crashes.
		_, _ = h.db.Exec(`
			INSERT INTO webhook_deliveries (
				id, subscription_id, tenant_id, event_name, event_id, payload,
				request_signature, attempt_number, status, scheduled_at, created_at
			) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, 1, 'pending', NOW(), NOW())
		`, deliveryID, s.id, tenantID, eventName, eventID, string(body), sig)

		// Fire-and-forget
		go h.sendWebhookDelivery(deliveryID, s.id, s.url, s.secret, s.timeout, s.maxRetry, sig, body)
	}
}

func (h *Handler) sendWebhookDelivery(
	deliveryID, subID uuid.UUID, url, secret string,
	timeoutMS, maxRetries int, signature string, body []byte,
) {
	attempt := 1
	for {
		started := time.Now()
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			h.recordDeliveryFailure(deliveryID, subID, attempt, 0, err.Error(), 0)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Genix-Signature", signature)
		req.Header.Set("User-Agent", "Genix-Webhook/1.0")

		client := &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond}
		resp, err := client.Do(req)
		duration := int(time.Since(started).Milliseconds())

		if err != nil {
			h.recordDeliveryFailure(deliveryID, subID, attempt, 0, err.Error(), duration)
		} else {
			bodyBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			truncated := string(bodyBytes)
			if len(truncated) > 2000 {
				truncated = truncated[:2000] + "…"
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				h.recordDeliverySuccess(deliveryID, subID, attempt, resp.StatusCode, truncated, duration)
				return
			}
			h.recordDeliveryFailure(deliveryID, subID, attempt,
				resp.StatusCode,
				fmt.Sprintf("non-2xx response: %s", truncated), duration)
		}

		if attempt >= maxRetries {
			_, _ = h.db.Exec(
				`UPDATE webhook_deliveries SET status = 'abandoned' WHERE id = $1`,
				deliveryID)
			return
		}
		attempt++
		// Exponential backoff: 5s, 25s, 125s, ...
		sleep := time.Duration(5*int64(pow5(attempt-1))) * time.Second
		if sleep > 10*time.Minute {
			sleep = 10 * time.Minute
		}
		time.Sleep(sleep)

		// Record a fresh attempt row
		_, _ = h.db.Exec(`
			UPDATE webhook_deliveries SET
				attempt_number = $2,
				status = 'retrying',
				next_retry_at = NULL
			WHERE id = $1
		`, deliveryID, attempt)
	}
}

func (h *Handler) recordDeliverySuccess(deliveryID, subID uuid.UUID, attempt, status int, respBody string, duration int) {
	now := time.Now()
	_, _ = h.db.Exec(`
		UPDATE webhook_deliveries SET
			attempt_number = $2, response_status = $3, response_body = $4,
			duration_ms = $5, status = 'success',
			started_at = COALESCE(started_at, $6), completed_at = $6
		WHERE id = $1
	`, deliveryID, attempt, status, respBody, duration, now)
	_, _ = h.db.Exec(`
		UPDATE webhook_subscriptions SET
			last_triggered_at = $1, last_status = 'success', updated_at = $1
		WHERE id = $2
	`, now, subID)
}

func (h *Handler) recordDeliveryFailure(deliveryID, subID uuid.UUID, attempt, status int, errMsg string, duration int) {
	_, _ = h.db.Exec(`
		UPDATE webhook_deliveries SET
			attempt_number = $2, response_status = $3, error_message = $4,
			duration_ms = $5, status = 'failed',
			started_at = COALESCE(started_at, NOW())
		WHERE id = $1
	`, deliveryID, attempt, status, errMsg, duration)
	_, _ = h.db.Exec(`
		UPDATE webhook_subscriptions SET
			last_triggered_at = NOW(), last_status = 'failed', updated_at = NOW()
		WHERE id = $1
	`, subID)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func signWebhookBody(secret string, body []byte) string {
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func pow5(n int) int64 {
	if n <= 0 {
		return 1
	}
	out := int64(1)
	for i := 0; i < n; i++ {
		out *= 5
	}
	return out
}

// KnownWebhookEvents documents the event names currently emitted by the app.
// Keep this in sync with any DispatchWebhookEvent(...) call sites.
var KnownWebhookEvents = []string{
	"journal_entry.posted",
	"journal_entry.reversed",
	"journal_entry.created",
	"period.closed",
	"period.reopened",
	"einvoice.ingested",
	"einvoice.approved",
	"einvoice.rejected",
	"payment.confirmed",
	"bank_statement.imported",
}
