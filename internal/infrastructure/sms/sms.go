package sms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
)

// Service handles SMS sending via Eskiz.uz
type Service struct {
	config *config.SMSConfig
	token  string
	expiry time.Time
	mu     sync.Mutex
}

// NewService creates a new SMS service
func NewService(cfg *config.SMSConfig) *Service {
	return &Service{config: cfg}
}

// Send sends an SMS message
func (s *Service) Send(phone, message string) error {
	fmt.Printf("[SMS] Attempting to send SMS to: %s\n", phone)

	if s.config.Provider == "console" || s.config.APIEmail == "" || s.config.APIKey == "" {
		// Dev mode — just log
		fmt.Printf("[SMS] Development mode - logging only\n")
		fmt.Printf("[SMS] To: %s, Message: %s\n", phone, message)
		return nil
	}

	// Normalize phone: remove +, spaces, dashes
	phone = normalizePhone(phone)

	// Get auth token (cached)
	token, err := s.getToken()
	if err != nil {
		return fmt.Errorf("failed to get SMS auth token: %w", err)
	}

	// Send SMS via Eskiz API
	data := url.Values{}
	data.Set("mobile_phone", phone)
	data.Set("message", message)
	data.Set("from", s.config.From)

	req, err := http.NewRequest("POST", s.config.BaseURL+"/message/sms/send", strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create SMS request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send SMS: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[SMS] Failed with status %d: %s\n", resp.StatusCode, string(body))
		return fmt.Errorf("SMS API returned status %d: %s", resp.StatusCode, string(body))
	}

	fmt.Printf("[SMS] Successfully sent to %s\n", phone)
	return nil
}

// getToken returns a cached auth token or fetches a new one
func (s *Service) getToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && time.Now().Before(s.expiry) {
		return s.token, nil
	}

	data := url.Values{}
	data.Set("email", s.config.APIEmail)
	data.Set("password", s.config.APIKey)

	resp, err := http.PostForm(s.config.BaseURL+"/auth/login", data)
	if err != nil {
		return "", fmt.Errorf("failed to authenticate with SMS provider: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse SMS auth response: %w", err)
	}

	if result.Data.Token == "" {
		return "", fmt.Errorf("empty token from SMS provider: %s", string(body))
	}

	s.token = result.Data.Token
	s.expiry = time.Now().Add(28 * 24 * time.Hour) // Eskiz tokens valid for 30 days
	return s.token, nil
}

func normalizePhone(phone string) string {
	var buf bytes.Buffer
	for _, c := range phone {
		if c >= '0' && c <= '9' {
			buf.WriteRune(c)
		}
	}
	result := buf.String()
	// Ensure starts with 998 for Uzbekistan
	if !strings.HasPrefix(result, "998") && len(result) == 9 {
		result = "998" + result
	}
	return result
}
