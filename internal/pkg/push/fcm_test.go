package push

import (
	"context"
	"testing"
)

func TestParseFCMErrorCode(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"unregistered", `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, "UNREGISTERED"},
		{"invalid-arg", `{"error":{"status":"INVALID_ARGUMENT","details":[{"errorCode":"INVALID_ARGUMENT"}]}}`, "INVALID_ARGUMENT"},
		{"status-only", `{"error":{"status":"PERMISSION_DENIED"}}`, "PERMISSION_DENIED"},
		{"garbage", `not json`, ""},
		{"empty", ``, ""},
	}
	for _, tc := range cases {
		if got := parseFCMErrorCode([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: parseFCMErrorCode = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDisabledSenderNoOps(t *testing.T) {
	// Empty credentials -> disabled sender that never errors and sends nothing.
	s, err := NewSender(context.Background(), "", "")
	if err != nil {
		t.Fatalf("NewSender(empty) error: %v", err)
	}
	if s.Enabled() {
		t.Fatal("sender with no credentials should be disabled")
	}
	res, err := s.Send(context.Background(), []string{"tok1", "tok2"}, "t", "b", nil)
	if err != nil {
		t.Fatalf("disabled Send should not error: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("disabled Send should return no results, got %d", len(res))
	}
}

// A nil *Sender must also be safe (Enabled guards it).
func TestNilSenderEnabled(t *testing.T) {
	var s *Sender
	if s.Enabled() {
		t.Fatal("nil sender must report not-enabled")
	}
}
