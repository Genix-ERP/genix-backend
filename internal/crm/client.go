// Package crm is a small HTTP client for talking to the Yuksalish CRM
// (Django REST + SimpleJWT). It only exposes the bits Genix needs for
// construction progress sync: listing projects and blocks (so the
// linking UI can show dropdowns), and pushing photo-report images into
// a block_progress row.
//
// Auth — JWT with login + refresh, NOT a static token:
//
//	CRM_API_BASE   e.g. "https://crm-api.yuksalish.group/api"  or  "http://yuksalish-backend:8000/api"
//	CRM_USERNAME   phone number of the service user (CRM uses phone as the login field)
//	CRM_PASSWORD   that user's password
//
// On first request the client POSTs to /mobile/token/ to get access +
// refresh tokens. The access token lasts a short while (typically 5–15
// min); when a request returns 401, the client transparently POSTs to
// /mobile/token/refresh/ to get a fresh access token. If the refresh
// token has also expired (or been blacklisted), the client re-logs in
// with username+password. Tokens are cached in memory only — never on
// disk — so they vanish on restart and a fresh login happens.
//
// If any of the three env vars are unset, every method returns
// ErrNotConfigured so callers can short-circuit gracefully (no sync,
// no crash).
package crm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ErrNotConfigured is returned when CRM_API_BASE / CRM_USERNAME / CRM_PASSWORD
// are not all set. Treat it as a soft skip: log and move on.
var ErrNotConfigured = errors.New("crm: CRM_API_BASE / CRM_USERNAME / CRM_PASSWORD not configured")

// Client talks to the CRM. Zero value is unconfigured; use NewClient.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client

	// Cached JWT pair. Protected by mu because the client is shared
	// across goroutines (auto-sync from many photo-report handlers + the
	// background retry worker).
	mu      sync.Mutex
	access  string
	refresh string
}

// Project / Block / Progress are the minimal shapes we need.
type Project struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Block struct {
	ID      int    `json:"id"`
	Project int    `json:"project"`
	Name    string `json:"name"`
}

type BlockProgress struct {
	ID         int    `json:"id"`
	Block      int    `json:"block"`
	Stage      string `json:"stage"`
	Status     string `json:"status"`
	Percentage int    `json:"percentage"`
}

// NewClient reads env vars and returns a configured client (or one that
// reports ErrNotConfigured from every call).
func NewClient() *Client {
	return &Client{
		baseURL:  strings.TrimRight(os.Getenv("CRM_API_BASE"), "/"),
		username: os.Getenv("CRM_USERNAME"),
		password: os.Getenv("CRM_PASSWORD"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Configured reports whether the client has URL + credentials. Callers
// should use this to skip cleanly when the deployment hasn't wired CRM up.
func (c *Client) Configured() bool {
	return c.baseURL != "" && c.username != "" && c.password != ""
}

// login POSTs username + password to /mobile/token/ and caches both
// tokens. Holds c.mu while writing the tokens.
func (c *Client) login() error {
	body, _ := json.Marshal(map[string]string{
		"phone":    c.username,
		"password": c.password,
	})
	req, err := http.NewRequest("POST", c.baseURL+"/mobile/token/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("crm login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("crm login: %d %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("crm login decode: %w", err)
	}
	if out.Access == "" {
		return errors.New("crm login: empty access token in response")
	}
	c.mu.Lock()
	c.access = out.Access
	c.refresh = out.Refresh
	c.mu.Unlock()
	return nil
}

// tryRefresh swaps the cached access token for a new one using the
// refresh token. Returns an error if refresh is empty / rejected — the
// caller should fall back to a full login() in that case.
func (c *Client) tryRefresh() error {
	c.mu.Lock()
	r := c.refresh
	c.mu.Unlock()
	if r == "" {
		return errors.New("crm: no refresh token cached")
	}
	body, _ := json.Marshal(map[string]string{"refresh": r})
	req, err := http.NewRequest("POST", c.baseURL+"/mobile/token/refresh/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		// 401 = refresh expired or blacklisted. Wipe the cache so the
		// next request triggers a full re-login.
		c.mu.Lock()
		c.access = ""
		c.refresh = ""
		c.mu.Unlock()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("crm refresh: %d %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Access  string `json:"access"`
		Refresh string `json:"refresh"` // SimpleJWT may rotate refresh too
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.Access == "" {
		return errors.New("crm refresh: empty access token in response")
	}
	c.mu.Lock()
	c.access = out.Access
	if out.Refresh != "" {
		c.refresh = out.Refresh
	}
	c.mu.Unlock()
	return nil
}

// ensureAuth makes sure we have at least an access token; logs in if not.
func (c *Client) ensureAuth() error {
	c.mu.Lock()
	have := c.access != ""
	c.mu.Unlock()
	if have {
		return nil
	}
	return c.login()
}

// do is the single HTTP entry point. Auto-retries once on 401: first
// via refresh, then via full login. After two consecutive 401s the
// error is returned so the caller can record a failure / requeue.
//
// reqBuilder is a closure that builds a fresh *http.Request each call
// (because *bytes.Reader bodies get drained and can't be replayed).
func (c *Client) do(reqBuilder func() (*http.Request, error)) (*http.Response, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if err := c.ensureAuth(); err != nil {
		return nil, err
	}

	attempt := func() (*http.Response, error) {
		req, err := reqBuilder()
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		token := c.access
		c.mu.Unlock()
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		return c.httpClient.Do(req)
	}

	resp, err := attempt()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	// Access expired. Drain body, try refresh.
	resp.Body.Close()
	if err := c.tryRefresh(); err != nil {
		// Refresh failed → fall through to full login below.
		if loginErr := c.login(); loginErr != nil {
			return nil, fmt.Errorf("crm auth: refresh + relogin failed: %v / %v", err, loginErr)
		}
	}

	resp2, err := attempt()
	if err != nil {
		return nil, err
	}
	if resp2.StatusCode == http.StatusUnauthorized {
		// Still 401 even with a fresh login — the service user has been
		// disabled or the password is wrong.
		raw, _ := io.ReadAll(io.LimitReader(resp2.Body, 1024))
		resp2.Body.Close()
		return nil, fmt.Errorf("crm: 401 after relogin, body=%s", string(raw))
	}
	return resp2, nil
}

// jsonReqBuilder returns a reqBuilder that re-encodes the body each call
// so a 401 retry can replay the request safely.
func jsonReqBuilder(method, url string, body interface{}) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		var rdr io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			rdr = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, url, rdr)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, nil
	}
}

func (c *Client) decodeOrError(resp *http.Response, out interface{}) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("crm: %s %s -> %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, string(body))
	}
	if out == nil {
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var paginated struct {
			Results json.RawMessage `json:"results"`
		}
		if json.Unmarshal(data, &paginated) == nil && len(paginated.Results) > 0 {
			return json.Unmarshal(paginated.Results, out)
		}
	}
	return json.Unmarshal(data, out)
}

// ListProjects fetches every CRM project the service user can see.
func (c *Client) ListProjects() ([]Project, error) {
	resp, err := c.do(jsonReqBuilder("GET", c.baseURL+"/projects/projects/", nil))
	if err != nil {
		return nil, err
	}
	var out []Project
	if err := c.decodeOrError(resp, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListBlocks fetches blocks for a CRM project (filter ?project=N).
func (c *Client) ListBlocks(crmProjectID int) ([]Block, error) {
	q := url.Values{}
	q.Set("project", fmt.Sprintf("%d", crmProjectID))
	resp, err := c.do(jsonReqBuilder("GET", c.baseURL+"/projects/blocks/?"+q.Encode(), nil))
	if err != nil {
		return nil, err
	}
	var out []Block
	if err := c.decodeOrError(resp, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetOrInitBlockProgress finds the block_progress row for (block, stage),
// initialising the block's full 8-stage set first if none exist.
func (c *Client) GetOrInitBlockProgress(blockID int, stage string) (int, error) {
	q := url.Values{}
	q.Set("block", fmt.Sprintf("%d", blockID))
	resp, err := c.do(jsonReqBuilder("GET", c.baseURL+"/projects/block-progress/?"+q.Encode(), nil))
	if err != nil {
		return 0, err
	}
	var existing []BlockProgress
	if err := c.decodeOrError(resp, &existing); err != nil {
		return 0, err
	}

	if len(existing) == 0 {
		initResp, err := c.do(jsonReqBuilder("POST", c.baseURL+"/projects/block-progress/initialize/", map[string]int{"block_id": blockID}))
		if err != nil {
			return 0, err
		}
		if err := c.decodeOrError(initResp, &existing); err != nil {
			return 0, err
		}
	}

	for _, bp := range existing {
		if strings.EqualFold(bp.Stage, stage) {
			return bp.ID, nil
		}
	}
	return 0, fmt.Errorf("crm: block %d has no stage %q in block_progress", blockID, stage)
}

// File is a (filename, body) pair for multipart upload.
type File struct {
	Filename string
	Reader   io.Reader
}

// UploadImages POSTs the given files to /projects/block-progress/{id}/upload-image/.
// Multipart bodies can't be safely replayed across a 401 retry (the
// readers have already streamed), so we buffer everything into memory
// first. Photo reports usually carry a handful of small images, so this
// is fine; if we ever ship large videos through here we'd switch to a
// disk-buffered approach.
func (c *Client) UploadImages(progressID int, files []File) error {
	if len(files) == 0 {
		return nil
	}

	// Pre-read all bytes so we can rebuild the multipart body if the
	// first attempt hits a 401.
	type stash struct {
		name string
		data []byte
	}
	stashed := make([]stash, 0, len(files))
	for _, f := range files {
		data, err := io.ReadAll(f.Reader)
		if err != nil {
			return fmt.Errorf("read photo %s: %w", f.Filename, err)
		}
		stashed = append(stashed, stash{name: f.Filename, data: data})
	}

	path := fmt.Sprintf("%s/projects/block-progress/%d/upload-image/", c.baseURL, progressID)

	build := func() (*http.Request, error) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		for _, s := range stashed {
			part, err := w.CreateFormFile("images", s.name)
			if err != nil {
				return nil, err
			}
			if _, err := part.Write(s.data); err != nil {
				return nil, err
			}
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		req, err := http.NewRequest("POST", path, &body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
		return req, nil
	}

	resp, err := c.do(build)
	if err != nil {
		return err
	}
	return c.decodeOrError(resp, nil)
}
