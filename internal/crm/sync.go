// Package crm — sync.go orchestrates pushing a Genix photo report to
// the matching CRM block_progress row. The handler's job is to call
// Syncer.Enqueue right after CreatePhotoReport's DB insert; the rest
// (download, upload, retry) happens in goroutines / the worker.

package crm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Logger is just the subset of slog we use; lets us avoid a hard dep here.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Syncer pushes Genix photo reports into the CRM. One instance is shared
// across handlers.
type Syncer struct {
	db     *sql.DB
	client *Client
	log    Logger
	// selfBase is the absolute base URL used to resolve RELATIVE photo
	// URLs stored in construction_photo_reports (e.g. "/api/v1/files/abc").
	// We deliberately use loopback (http://localhost:<APP_PORT>) rather
	// than APP_BASE_URL: the syncer runs in the same container/process as
	// the file server, and the public domain would NAT-hairpin (the exact
	// problem that broke the CRM call leg). Loopback always works.
	selfBase string
}

func NewSyncer(db *sql.DB, log Logger) *Syncer {
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}
	return &Syncer{
		db:       db,
		client:   NewClient(),
		log:      log,
		selfBase: "http://localhost:" + port,
	}
}

// Configured reports whether the underlying client has env config.
func (s *Syncer) Configured() bool { return s.client.Configured() }

// Client returns the underlying HTTP client (used by proxy endpoints).
func (s *Syncer) Client() *Client { return s.client }

// Enqueue queues a sync attempt for a freshly-created photo report. Safe
// to call inline from the handler; it spawns a goroutine for the actual
// network work. Errors are logged + persisted to crm_sync_queue; they
// never propagate to the caller (because the user's report already saved
// successfully — we shouldn't fail their UI on CRM hiccups).
func (s *Syncer) Enqueue(ctx context.Context, tenantID uuid.UUID, reportID int64) {
	if !s.client.Configured() {
		// Silent skip — Genix runs fine without CRM wiring.
		return
	}
	go func() {
		if err := s.syncOnce(context.Background(), tenantID, reportID); err != nil {
			s.recordFailure(tenantID, reportID, err)
		}
	}()
}

// syncOnce does the full upload for one report. Idempotent on retries —
// the GetOrInitBlockProgress + already-synced check make replaying safe.
func (s *Syncer) syncOnce(ctx context.Context, tenantID uuid.UUID, reportID int64) error {
	// 1) Pull report + linked building + project metadata in one round trip.
	var (
		projectID    int64
		buildingID   sql.NullInt64
		photosJSON   sql.NullString
		alreadySync  sql.NullTime
		crmBlockID   sql.NullInt64
		currentStage sql.NullString
		autoSync     sql.NullBool
	)
	// JOIN on building_id (not section_id — those are completely different
	// FKs; section_id points to smeta_sections, building_id points to
	// construction_buildings). Earlier code used section_id here, which
	// silently broke the link lookup whenever the report did have a
	// section but not the matching building. Building is what carries
	// the crm_block_id, so we use that.
	err := s.db.QueryRowContext(ctx, `
		SELECT pr.project_id, pr.building_id, pr.photos, pr.crm_synced_at,
		       b.crm_block_id, b.current_crm_stage, b.crm_auto_sync
		FROM construction_photo_reports pr
		LEFT JOIN construction_buildings b ON b.id = pr.building_id AND b.tenant_id = pr.tenant_id
		WHERE pr.id = $1 AND pr.tenant_id = $2
	`, reportID, tenantID).Scan(&projectID, &buildingID, &photosJSON, &alreadySync,
		&crmBlockID, &currentStage, &autoSync)
	if err != nil {
		return fmt.Errorf("load report: %w", err)
	}

	// Already synced? No-op (this is what makes retries safe).
	if alreadySync.Valid {
		s.log.Info("crm sync: report already synced, skipping", "report_id", reportID)
		return nil
	}

	// No CRM link on this building? Skip silently — operator can link later
	// and click Re-send manually.
	if !buildingID.Valid || !crmBlockID.Valid {
		s.log.Info("crm sync: building not linked, skipping", "report_id", reportID, "building_id", buildingID.Int64)
		return nil
	}
	if autoSync.Valid && !autoSync.Bool {
		s.log.Info("crm sync: auto_sync disabled for building, skipping", "report_id", reportID, "building_id", buildingID.Int64)
		return nil
	}
	stage := "foundation"
	if currentStage.Valid && currentStage.String != "" {
		stage = currentStage.String
	}

	// 2) Resolve target progress id in CRM (creates the 8-stage set if missing).
	progressID, err := s.client.GetOrInitBlockProgress(int(crmBlockID.Int64), stage)
	if err != nil {
		return fmt.Errorf("resolve block_progress: %w", err)
	}

	// 3) Parse the photos JSON. Each entry is expected to be a URL
	//    (Genix stores uploaded images in /storage/uploads/... and we
	//    record the public URL here). We fetch each URL and stream the
	//    bytes into a multipart upload to CRM.
	var photos []photoEntry
	if photosJSON.Valid && photosJSON.String != "" && photosJSON.String != "[]" {
		if err := json.Unmarshal([]byte(photosJSON.String), &photos); err != nil {
			// Fall back to a simple string-array decode for older rows.
			var bareURLs []string
			if err2 := json.Unmarshal([]byte(photosJSON.String), &bareURLs); err2 != nil {
				return fmt.Errorf("parse photos json: %w", err)
			}
			photos = make([]photoEntry, len(bareURLs))
			for i, u := range bareURLs {
				photos[i] = photoEntry{URL: u}
			}
		}
	}

	if len(photos) == 0 {
		// Nothing to upload but we still want to mark the report as synced
		// so the UI shows "✓ synced (0 photos)" instead of "pending".
		return s.markSynced(tenantID, reportID, progressID)
	}

	files, cleanup, err := s.fetchPhotoFiles(photos)
	defer cleanup()
	if err != nil {
		return err
	}

	if err := s.client.UploadImages(progressID, files); err != nil {
		return fmt.Errorf("upload images: %w", err)
	}

	return s.markSynced(tenantID, reportID, progressID)
}

type photoEntry struct {
	URL string `json:"url"`
	// Some Genix reports store {url, caption, taken_at, ...} — we only
	// care about the URL here. Extra fields decode harmlessly.
}

// fetchPhotoFiles downloads each photo URL via HTTP and returns a slice
// of (filename, reader) ready for multipart upload. Each reader must be
// drained / closed via the returned cleanup func.
func (s *Syncer) fetchPhotoFiles(photos []photoEntry) ([]File, func(), error) {
	files := make([]File, 0, len(photos))
	closers := make([]io.Closer, 0, len(photos))
	cleanup := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	for _, p := range photos {
		u := strings.TrimSpace(p.URL)
		if u == "" {
			continue
		}
		// Genix stores photo URLs as relative paths ("/api/v1/files/<id>").
		// Go's http.Get rejects those with "unsupported protocol scheme".
		// Resolve against our own loopback base so the fetch hits the
		// in-process file server. Absolute URLs (legacy rows) pass through.
		if strings.HasPrefix(u, "/") {
			u = s.selfBase + u
		}
		// Best effort. If a single photo fails to fetch we log + skip it
		// so the report can still partially sync.
		resp, err := httpClient.Get(u)
		if err != nil {
			s.log.Warn("crm sync: photo fetch failed", "url", u, "error", err)
			continue
		}
		if resp.StatusCode >= 400 {
			s.log.Warn("crm sync: photo fetch non-2xx", "url", u, "status", resp.StatusCode)
			resp.Body.Close()
			continue
		}
		closers = append(closers, resp.Body)
		name := path.Base(u)
		if name == "" || name == "/" || name == "." {
			name = fmt.Sprintf("photo-%d.jpg", len(files)+1)
		}
		files = append(files, File{Filename: name, Reader: resp.Body})
	}
	return files, cleanup, nil
}

// markSynced updates the report and clears any queue entry. Called only on
// success.
func (s *Syncer) markSynced(tenantID uuid.UUID, reportID int64, progressID int) error {
	if _, err := s.db.Exec(`
		UPDATE construction_photo_reports
		SET crm_block_progress_id = $1,
		    crm_synced_at = NOW(),
		    crm_sync_error = NULL,
		    updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, progressID, reportID, tenantID); err != nil {
		return err
	}
	// Drop any pending queue entries for this report — they're done.
	_, _ = s.db.Exec(`UPDATE crm_sync_queue SET status = 'done', updated_at = NOW() WHERE report_id = $1 AND tenant_id = $2 AND status <> 'done'`, reportID, tenantID)

	// Update the building's last-synced marker so the UI can show "Last
	// activity 2 min ago" without scanning every photo report.
	_, _ = s.db.Exec(`
		UPDATE construction_buildings
		SET crm_last_synced_at = NOW(), crm_last_sync_error = NULL
		WHERE id = (SELECT section_id FROM construction_photo_reports WHERE id = $1)
		  AND tenant_id = $2
	`, reportID, tenantID)
	return nil
}

// recordFailure persists the error to the queue (insert OR bump retry_count)
// and to the report row so the UI can show why.
func (s *Syncer) recordFailure(tenantID uuid.UUID, reportID int64, syncErr error) {
	s.log.Error("crm sync failed", "report_id", reportID, "error", syncErr)
	msg := syncErr.Error()

	_, _ = s.db.Exec(`
		UPDATE construction_photo_reports
		SET crm_sync_error = $1, updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, msg, reportID, tenantID)

	// Lookup the building id so the queue row knows where to dispatch
	// retries (also lets the worker batch by building).
	var buildingID sql.NullInt64
	_ = s.db.QueryRow(`SELECT section_id FROM construction_photo_reports WHERE id = $1`, reportID).Scan(&buildingID)
	if !buildingID.Valid {
		// No building — nothing to retry against. Skip the queue entry.
		return
	}

	// Upsert pattern via DELETE-then-INSERT to keep retry_count fresh.
	var existingID sql.NullString
	var existingRetries sql.NullInt64
	_ = s.db.QueryRow(`SELECT id::text, retry_count FROM crm_sync_queue WHERE report_id = $1 AND tenant_id = $2`, reportID, tenantID).Scan(&existingID, &existingRetries)

	nextAttempt := time.Now().Add(backoffFor(int(existingRetries.Int64) + 1))

	if existingID.Valid {
		_, _ = s.db.Exec(`
			UPDATE crm_sync_queue
			SET status = 'pending', retry_count = retry_count + 1,
			    next_attempt_at = $1, last_attempted_at = NOW(),
			    last_error = $2, updated_at = NOW()
			WHERE id = $3::uuid
		`, nextAttempt, msg, existingID.String)
	} else {
		_, _ = s.db.Exec(`
			INSERT INTO crm_sync_queue (
				tenant_id, report_id, building_id, status, retry_count,
				next_attempt_at, last_attempted_at, last_error
			) VALUES ($1, $2, $3, 'pending', 1, $4, NOW(), $5)
		`, tenantID, reportID, buildingID.Int64, nextAttempt, msg)
	}

	_, _ = s.db.Exec(`
		UPDATE construction_buildings
		SET crm_last_sync_error = $1
		WHERE id = $2 AND tenant_id = $3
	`, msg, buildingID.Int64, tenantID)
}

// backoffFor returns the wait until the next retry attempt. Exponential
// with cap, so a flapping CRM doesn't lose data but also doesn't hammer
// us at peak.
func backoffFor(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 1 * time.Minute
	case attempt == 2:
		return 5 * time.Minute
	case attempt == 3:
		return 15 * time.Minute
	case attempt == 4:
		return 1 * time.Hour
	case attempt == 5:
		return 4 * time.Hour
	default:
		return 12 * time.Hour
	}
}

// RunWorker processes the queue forever, polling every 5 minutes. Call
// once from main() as `go syncer.RunWorker(ctx)`.
func (s *Syncer) RunWorker(ctx context.Context) {
	if !s.Configured() {
		s.log.Info("crm sync worker: not configured, skipping")
		return
	}
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	s.log.Info("crm sync worker started")
	for {
		select {
		case <-ctx.Done():
			s.log.Info("crm sync worker stopping")
			return
		case <-ticker.C:
			s.drainQueue(ctx)
		}
	}
}

// drainQueue picks up due rows (status='pending' AND next_attempt_at<=NOW)
// and replays the sync. Each replay is the same syncOnce path — idempotent.
func (s *Syncer) drainQueue(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, report_id, retry_count
		FROM crm_sync_queue
		WHERE status = 'pending' AND next_attempt_at <= NOW()
		ORDER BY next_attempt_at
		LIMIT 50
	`)
	if err != nil {
		s.log.Error("crm sync worker: pick failed", "error", err)
		return
	}
	type job struct {
		id         uuid.UUID
		tenantID   uuid.UUID
		reportID   int64
		retryCount int
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.tenantID, &j.reportID, &j.retryCount); err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	for _, j := range jobs {
		// Give up after too many retries — saves the queue from filling
		// with permanently-broken rows. User can clear & re-trigger manually.
		if j.retryCount >= 8 {
			_, _ = s.db.Exec(`UPDATE crm_sync_queue SET status = 'failed', updated_at = NOW() WHERE id = $1`, j.id)
			continue
		}
		if err := s.syncOnce(ctx, j.tenantID, j.reportID); err != nil {
			s.recordFailure(j.tenantID, j.reportID, err)
			continue
		}
		// success path is handled by markSynced (sets status='done')
	}
}
