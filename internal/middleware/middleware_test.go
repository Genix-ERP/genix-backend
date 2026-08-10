package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/infrastructure/cache"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// SecurityHeaders
// ---------------------------------------------------------------------------

func TestSecurityHeaders(t *testing.T) {
	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	expected := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"X-Xss-Protection":          "1; mode=block",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Content-Security-Policy":   "default-src 'self'",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}

	for header, want := range expected {
		got := w.Header().Get(header)
		if got != want {
			t.Errorf("header %s = %q, want %q", header, got, want)
		}
	}

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// ---------------------------------------------------------------------------
// CORS
// ---------------------------------------------------------------------------

func newCORSConfig(origins []string, credentials bool) config.CORSConfig {
	return config.CORSConfig{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: credentials,
		MaxAge:           3600,
	}
}

func TestCORS_AllowedOrigin(t *testing.T) {
	cfg := newCORSConfig([]string{"https://example.com", "https://other.com"}, true)
	router := gin.New()
	router.Use(CORS(cfg))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "https://example.com")
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want %q", got, "true")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	cfg := newCORSConfig([]string{"https://example.com"}, true)
	router := gin.New()
	router.Use(CORS(cfg))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	router.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin should be empty for disallowed origin, got %q", got)
	}
	// Methods/headers/max-age are always set regardless of origin
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Allow-Methods should still be set")
	}
}

func TestCORS_PreflightRequest(t *testing.T) {
	cfg := newCORSConfig([]string{"https://example.com"}, true)
	router := gin.New()
	router.Use(CORS(cfg))
	router.GET("/test", func(c *gin.Context) {
		t.Error("handler should not be called on preflight")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "https://example.com")
	}
}

func TestCORS_WildcardOrigin_NoCredentials(t *testing.T) {
	cfg := newCORSConfig([]string{"*"}, true) // credentials requested but wildcard
	router := gin.New()
	router.Use(CORS(cfg))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://any-origin.com")
	router.ServeHTTP(w, req)

	// With wildcard, origin is echoed back
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://any-origin.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "https://any-origin.com")
	}
	// Credentials must NOT be set when wildcard is configured
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials should be empty with wildcard, got %q", got)
	}
}

func TestCORS_MaxAge(t *testing.T) {
	cfg := newCORSConfig([]string{"*"}, false)
	cfg.MaxAge = 7200
	router := gin.New()
	router.Use(CORS(cfg))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Max-Age"); got != "7200" {
		t.Errorf("Max-Age = %q, want %q", got, "7200")
	}
}

// ---------------------------------------------------------------------------
// Timeout
// ---------------------------------------------------------------------------

func TestTimeout_SetsDeadline(t *testing.T) {
	timeout := 5 * time.Second
	router := gin.New()
	router.Use(Timeout(timeout))

	var (
		hasDeadline bool
		deadline    time.Time
	)
	router.GET("/test", func(c *gin.Context) {
		deadline, hasDeadline = c.Request.Context().Deadline()
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	if !hasDeadline {
		t.Fatal("expected context to have a deadline")
	}
	// The deadline should be roughly `timeout` from now (within a generous margin)
	remaining := time.Until(deadline)
	if remaining < 0 || remaining > timeout {
		t.Errorf("deadline remaining = %v, expected between 0 and %v", remaining, timeout)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestTimeout_DeadlineExceeded(t *testing.T) {
	timeout := 10 * time.Millisecond
	router := gin.New()
	router.Use(Timeout(timeout))
	router.GET("/test", func(c *gin.Context) {
		// Simulate work that exceeds the timeout
		select {
		case <-c.Request.Context().Done():
			// Context expired, don't write anything so the middleware can respond
		case <-time.After(200 * time.Millisecond):
			c.String(http.StatusOK, "ok")
		}
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if body["success"] != false {
		t.Errorf("expected success=false, got %v", body["success"])
	}
}

// ---------------------------------------------------------------------------
// RequestID
// ---------------------------------------------------------------------------

func TestRequestID_GeneratesNew(t *testing.T) {
	router := gin.New()
	router.Use(RequestID())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	got := w.Header().Get("X-Request-ID")
	if got == "" {
		t.Error("expected X-Request-ID header to be set")
	}
}

func TestRequestID_UsesExisting(t *testing.T) {
	router := gin.New()
	router.Use(RequestID())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "my-custom-id")
	router.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "my-custom-id" {
		t.Errorf("X-Request-ID = %q, want %q", got, "my-custom-id")
	}
}

// ---------------------------------------------------------------------------
// RateLimiter (using miniredis)
// ---------------------------------------------------------------------------

func newTestRedisClient(t *testing.T) (*cache.RedisClient, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)

	host := mr.Host()
	port, _ := strconv.Atoi(mr.Port())

	rc, err := cache.NewRedisClient(config.RedisConfig{
		Host:         host,
		Port:         port,
		PoolSize:     5,
		MinIdleConns: 1,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create redis client: %v", err)
	}
	t.Cleanup(func() { rc.Close() })
	return rc, mr
}

func TestRateLimiter_Disabled(t *testing.T) {
	cfg := config.RateLimitConfig{Enabled: false}
	router := gin.New()
	router.Use(RateLimiter(cfg, nil))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRateLimiter_NilRedis(t *testing.T) {
	cfg := config.RateLimitConfig{Enabled: true, RequestsLimit: 10, WindowSize: time.Minute}
	router := gin.New()
	router.Use(RateLimiter(cfg, nil))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rc, _ := newTestRedisClient(t)

	cfg := config.RateLimitConfig{
		Enabled:       true,
		RequestsLimit: 5,
		WindowSize:    time.Minute,
	}

	router := gin.New()
	router.Use(RateLimiter(cfg, rc))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Send 5 requests — all should succeed
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
		}

		remaining := w.Header().Get("X-RateLimit-Remaining")
		expected := strconv.Itoa(5 - (i + 1))
		if remaining != expected {
			t.Errorf("request %d: X-RateLimit-Remaining = %q, want %q", i+1, remaining, expected)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rc, _ := newTestRedisClient(t)

	cfg := config.RateLimitConfig{
		Enabled:       true,
		RequestsLimit: 3,
		WindowSize:    time.Minute,
	}

	router := gin.New()
	router.Use(RateLimiter(cfg, rc))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Send 3 requests under the limit
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
		}
	}

	// 4th request should be rate limited
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("over-limit status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Error("expected Retry-After header on rate-limited response")
	}
}

func TestRateLimiter_SetsHeaders(t *testing.T) {
	rc, _ := newTestRedisClient(t)

	cfg := config.RateLimitConfig{
		Enabled:       true,
		RequestsLimit: 10,
		WindowSize:    time.Minute,
	}

	router := gin.New()
	router.Use(RateLimiter(cfg, rc))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "172.16.0.1:12345"
	router.ServeHTTP(w, req)

	if got := w.Header().Get("X-RateLimit-Limit"); got != "10" {
		t.Errorf("X-RateLimit-Limit = %q, want %q", got, "10")
	}
	if got := w.Header().Get("X-RateLimit-Remaining"); got != "9" {
		t.Errorf("X-RateLimit-Remaining = %q, want %q", got, "9")
	}
}

func TestRateLimiter_UsesUserIDWhenPresent(t *testing.T) {
	rc, mr := newTestRedisClient(t)

	cfg := config.RateLimitConfig{
		Enabled:       true,
		RequestsLimit: 100,
		WindowSize:    time.Minute,
	}

	router := gin.New()
	// Inject a user ID before the rate limiter runs
	router.Use(func(c *gin.Context) {
		c.Set(ContextKeyUserID, "user-abc-123")
		c.Next()
	})
	router.Use(RateLimiter(cfg, rc))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify that the Redis key was created using the user ID
	keys := mr.Keys()
	found := false
	for _, k := range keys {
		if k == "ratelimit:user-abc-123" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Redis key 'ratelimit:user-abc-123', found keys: %v", keys)
	}
}

// ---------------------------------------------------------------------------
// Helper context getters
// ---------------------------------------------------------------------------

func TestGetUserID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// No user ID set
	if _, ok := GetUserID(c); ok {
		t.Error("expected ok=false when no user ID set")
	}

	c.Set(ContextKeyUserID, "550e8400-e29b-41d4-a716-446655440000")
	uid, ok := GetUserID(c)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uid.String() != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got %s, want 550e8400-e29b-41d4-a716-446655440000", uid.String())
	}
}

func TestGetTenantID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	if _, ok := GetTenantID(c); ok {
		t.Error("expected ok=false when no tenant ID set")
	}

	c.Set(ContextKeyTenantID, "660e8400-e29b-41d4-a716-446655440000")
	tid, ok := GetTenantID(c)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if tid.String() != "660e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got %s, want 660e8400-e29b-41d4-a716-446655440000", tid.String())
	}
}

func TestGetOrganizationID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	if _, ok := GetOrganizationID(c); ok {
		t.Error("expected ok=false when no org ID set")
	}

	c.Set(ContextKeyOrganizationID, "770e8400-e29b-41d4-a716-446655440000")
	oid, ok := GetOrganizationID(c)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if oid.String() != "770e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got %s, want 770e8400-e29b-41d4-a716-446655440000", oid.String())
	}
}

// ---------------------------------------------------------------------------
// TenantResolver & OrganizationResolver
// ---------------------------------------------------------------------------

func TestTenantResolver_FromHeader(t *testing.T) {
	router := gin.New()
	router.Use(TenantResolver())

	var gotTenantID string
	router.GET("/test", func(c *gin.Context) {
		if v, ok := c.Get(ContextKeyTenantID); ok {
			gotTenantID = v.(string)
		}
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Tenant-ID", "550e8400-e29b-41d4-a716-446655440000")
	router.ServeHTTP(w, req)

	if gotTenantID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("tenant ID = %q, want %q", gotTenantID, "550e8400-e29b-41d4-a716-446655440000")
	}
}

func TestTenantResolver_InvalidUUID(t *testing.T) {
	router := gin.New()
	router.Use(TenantResolver())

	var hasTenantID bool
	router.GET("/test", func(c *gin.Context) {
		_, hasTenantID = c.Get(ContextKeyTenantID)
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Tenant-ID", "not-a-uuid")
	router.ServeHTTP(w, req)

	if hasTenantID {
		t.Error("expected tenant ID not to be set for invalid UUID")
	}
	_ = w
}

// OrganizationResolver now takes a database handle and VERIFIES that the
// organization named in the header belongs to the caller's tenant before
// trusting it.
//
// This test used to assert the opposite — that the header alone set the
// organization — which is exactly the behaviour that change removed: a client
// could name any organization it liked and have every org-scoped query
// silently answer for it. The assertion is inverted to pin the new guarantee,
// because a test that still demanded the old one would have to be deleted the
// moment anyone read it.
func TestOrganizationResolver(t *testing.T) {
	run := func(t *testing.T, header string) (string, bool) {
		t.Helper()
		router := gin.New()
		// nil handle: nothing can be verified against it, so nothing may be
		// trusted. Same path a request takes before authentication has put a
		// tenant in the context.
		router.Use(OrganizationResolver(nil))

		var gotOrgID string
		var wasSet bool
		router.GET("/test", func(c *gin.Context) {
			if v, ok := c.Get(ContextKeyOrganizationID); ok {
				gotOrgID, wasSet = v.(string), true
			}
			c.String(http.StatusOK, "ok")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		if header != "" {
			req.Header.Set("X-Organization-ID", header)
		}
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request should still be served, got %d", w.Code)
		}
		return gotOrgID, wasSet
	}

	t.Run("unverifiable header is not trusted", func(t *testing.T) {
		got, wasSet := run(t, "880e8400-e29b-41d4-a716-446655440000")
		if wasSet {
			t.Errorf("organization must not be set from an unverified header, got %q", got)
		}
	})

	t.Run("malformed header is ignored", func(t *testing.T) {
		if _, wasSet := run(t, "not-a-uuid"); wasSet {
			t.Error("a malformed organization header must be ignored, not trusted")
		}
	})

	t.Run("absent header is not an error", func(t *testing.T) {
		// The middleware must let an org-less request through — most endpoints
		// are tenant-scoped only.
		if _, wasSet := run(t, ""); wasSet {
			t.Error("no header means no organization")
		}
	})
}

// ---------------------------------------------------------------------------
// RateLimiter — Lua script correctness via miniredis
// ---------------------------------------------------------------------------

func TestRateLimiter_LuaScript_Increments(t *testing.T) {
	rc, mr := newTestRedisClient(t)
	ctx := context.Background()

	// Execute the Lua script directly to verify increment behaviour
	for i := int64(1); i <= 3; i++ {
		result, err := rc.Client().Eval(ctx, rateLimitScript, []string{"testkey"}, 60).Int64()
		if err != nil {
			t.Fatalf("Eval error on iteration %d: %v", i, err)
		}
		if result != i {
			t.Errorf("iteration %d: got count %d, want %d", i, result, i)
		}
	}

	// Verify TTL was set on the key
	ttl := mr.TTL("testkey")
	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}
	if ttl > 60*time.Second {
		t.Errorf("TTL = %v, expected <= 60s", ttl)
	}
}
