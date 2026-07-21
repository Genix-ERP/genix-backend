// Package push sends mobile push notifications via Firebase Cloud Messaging
// (FCM HTTP v1). It authenticates with a Google service-account JSON using
// golang.org/x/oauth2/google and POSTs one message per device token.
//
// FCM relays to APNs for iOS, so a single sender covers both Android and iOS.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// Sender sends FCM HTTP v1 push messages. Build with NewSender; a disabled
// sender (no credentials) is safe to hold and no-ops on Send.
type Sender struct {
	projectID string
	ts        oauth2.TokenSource // cached, auto-refreshing
	http      *http.Client
	enabled   bool
}

// Result reports the outcome for one token.
type Result struct {
	Token      string
	OK         bool
	Unregister bool // token is dead (UNREGISTERED / invalid) -> caller should delete it
	Err        error
}

// NewSender builds a sender from a service-account JSON. Empty credentialsJSON
// yields a disabled sender (Send is a no-op) so callers never need a nil check.
// projectID may be empty -> taken from the JSON's "project_id".
func NewSender(ctx context.Context, credentialsJSON, projectID string) (*Sender, error) {
	if credentialsJSON == "" {
		return &Sender{enabled: false}, nil
	}
	creds, err := google.CredentialsFromJSON(ctx, []byte(credentialsJSON), fcmScope)
	if err != nil {
		return nil, fmt.Errorf("fcm: parse credentials: %w", err)
	}
	pid := projectID
	if pid == "" {
		pid = creds.ProjectID
	}
	if pid == "" {
		return nil, fmt.Errorf("fcm: project id not set and not present in credentials")
	}
	return &Sender{
		projectID: pid,
		ts:        creds.TokenSource,
		http:      &http.Client{Timeout: 10 * time.Second},
		enabled:   true,
	}, nil
}

// Enabled reports whether push is configured.
func (s *Sender) Enabled() bool { return s != nil && s.enabled }

// Send delivers the same notification to each token, returning one Result per
// token (same order). Individual token failures are reported per-Result; a
// non-nil error means the whole batch couldn't be attempted (e.g. auth failure).
func (s *Sender) Send(ctx context.Context, tokens []string, title, body string, data map[string]string) ([]Result, error) {
	results := make([]Result, 0, len(tokens))
	if !s.Enabled() || len(tokens) == 0 {
		return results, nil
	}
	tok, err := s.ts.Token()
	if err != nil {
		return results, fmt.Errorf("fcm: get access token: %w", err)
	}
	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", s.projectID)

	for _, t := range tokens {
		r := Result{Token: t}

		var msg fcmMessage
		msg.Message.Token = t
		msg.Message.Notification.Title = title
		msg.Message.Notification.Body = body
		msg.Message.Data = data // FCM requires all data values to be strings
		payload, _ := json.Marshal(msg)

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if reqErr != nil {
			r.Err = reqErr
			results = append(results, r)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		req.Header.Set("Content-Type", "application/json")

		resp, doErr := s.http.Do(req)
		if doErr != nil {
			r.Err = doErr
			results = append(results, r)
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			r.OK = true
			results = append(results, r)
			continue
		}
		r.Err = fmt.Errorf("fcm: status %d: %s", resp.StatusCode, string(respBody))
		// Dead token -> tell the caller to prune it. FCM v1 returns 404
		// UNREGISTERED for stale tokens and 400 INVALID_ARGUMENT for malformed
		// ones; both mean "never deliver here again".
		if resp.StatusCode == http.StatusNotFound {
			r.Unregister = true
		} else if code := parseFCMErrorCode(respBody); code == "UNREGISTERED" || code == "INVALID_ARGUMENT" {
			r.Unregister = true
		}
		results = append(results, r)
	}
	return results, nil
}

type fcmMessage struct {
	Message struct {
		Token        string `json:"token"`
		Notification struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"notification"`
		Data map[string]string `json:"data,omitempty"`
	} `json:"message"`
}

// parseFCMErrorCode extracts the errorCode from an FCM v1 error body, e.g.
// {"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}
func parseFCMErrorCode(b []byte) string {
	var e struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) != nil {
		return ""
	}
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			return d.ErrorCode
		}
	}
	return e.Error.Status
}
