package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/infrastructure/cache"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/logger"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Context keys
const (
	ContextKeyRequestID      = "request_id"
	ContextKeyTenantID       = "tenant_id"
	ContextKeyOrganizationID = "organization_id"
	ContextKeyUserID         = "user_id"
	ContextKeyUser           = "user"
	ContextKeyClaims         = "claims"
)

// RequestID middleware adds a unique request ID to each request
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set(ContextKeyRequestID, requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// Logger middleware logs HTTP requests
func Logger(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Log after request
		latency := time.Since(start)
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()
		bodySize := c.Writer.Size()

		if raw != "" {
			path = path + "?" + raw
		}

		// Get request ID from context
		requestID, _ := c.Get(ContextKeyRequestID)

		log.Info("http_request",
			"request_id", requestID,
			"method", method,
			"path", path,
			"status", statusCode,
			"latency_ms", latency.Milliseconds(),
			"client_ip", clientIP,
			"body_size", bodySize,
		)
	}
}

// CORS middleware handles Cross-Origin Resource Sharing
func CORS(cfg config.CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// Check if origin is allowed
		allowed := false
		for _, allowedOrigin := range cfg.AllowedOrigins {
			if allowedOrigin == "*" || allowedOrigin == origin {
				allowed = true
				break
			}
		}

		if allowed {
			c.Header("Access-Control-Allow-Origin", origin)
		}

		c.Header("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ", "))
		c.Header("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
		c.Header("Access-Control-Expose-Headers", strings.Join(cfg.ExposedHeaders, ", "))

		if cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		c.Header("Access-Control-Max-Age", string(rune(cfg.MaxAge)))

		// Handle preflight requests
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// SecurityHeaders middleware adds security headers
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Next()
	}
}

// Timeout middleware adds request timeout
func Timeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)

		finished := make(chan struct{})

		go func() {
			c.Next()
			close(finished)
		}()

		select {
		case <-finished:
			// Request completed normally
		case <-ctx.Done():
			c.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "TIMEOUT",
					"message": "Request timeout",
				},
			})
		}
	}
}

// RateLimiter middleware implements rate limiting
func RateLimiter(cfg config.RateLimitConfig, redis *cache.RedisClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Enabled || redis == nil {
			c.Next()
			return
		}

		// Get identifier (IP or user ID)
		identifier := c.ClientIP()
		if userID, exists := c.Get(ContextKeyUserID); exists {
			identifier = userID.(string)
		}

		key := "ratelimit:" + identifier
		ctx := c.Request.Context()

		// Increment counter
		count, err := redis.Incr(ctx, key)
		if err != nil {
			// If Redis fails, allow the request
			c.Next()
			return
		}

		// Set expiration on first request
		if count == 1 {
			redis.Expire(ctx, key, cfg.WindowSize)
		}

		// Check if over limit
		if int(count) > cfg.RequestsLimit {
			response.TooManyRequests(c, "Rate limit exceeded. Please try again later.")
			c.Abort()
			return
		}

		// Add rate limit headers
		c.Header("X-RateLimit-Limit", string(rune(cfg.RequestsLimit)))
		c.Header("X-RateLimit-Remaining", string(rune(cfg.RequestsLimit-int(count))))

		c.Next()
	}
}

// Auth middleware validates JWT tokens
func Auth(jwtManager *crypto.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Unauthorized(c, "Authorization header required")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.Unauthorized(c, "Invalid authorization header format")
			c.Abort()
			return
		}

		claims, err := jwtManager.ValidateAccessToken(parts[1])
		if err != nil {
			if err == crypto.ErrTokenExpired {
				response.Error(c, http.StatusUnauthorized, response.ErrCodeTokenExpired, "Token has expired")
			} else {
				response.Error(c, http.StatusUnauthorized, response.ErrCodeTokenInvalid, "Invalid token")
			}
			c.Abort()
			return
		}

		// Set user information in context
		c.Set(ContextKeyClaims, claims)
		c.Set(ContextKeyUserID, claims.UserID.String())
		c.Set(ContextKeyTenantID, claims.TenantID.String())

		c.Next()
	}
}

// OptionalAuth middleware validates JWT tokens if present, but doesn't require them
func OptionalAuth(jwtManager *crypto.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.Next()
			return
		}

		claims, err := jwtManager.ValidateAccessToken(parts[1])
		if err == nil {
			c.Set(ContextKeyClaims, claims)
			c.Set(ContextKeyUserID, claims.UserID.String())
			c.Set(ContextKeyTenantID, claims.TenantID.String())
		}

		c.Next()
	}
}

// TenantResolver middleware resolves tenant from header or subdomain
func TenantResolver() gin.HandlerFunc {
	return func(c *gin.Context) {
		// First try X-Tenant-ID header
		tenantID := c.GetHeader("X-Tenant-ID")

		// If not in header, try to get from JWT claims
		if tenantID == "" {
			if claims, exists := c.Get(ContextKeyClaims); exists {
				if jwtClaims, ok := claims.(*crypto.Claims); ok {
					tenantID = jwtClaims.TenantID.String()
				}
			}
		}

		if tenantID != "" {
			// Validate UUID format
			if _, err := uuid.Parse(tenantID); err == nil {
				c.Set(ContextKeyTenantID, tenantID)
			}
		}

		c.Next()
	}
}

// OrganizationResolver middleware resolves organization from header
func OrganizationResolver() gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID := c.GetHeader("X-Organization-ID")
		if orgID != "" {
			if _, err := uuid.Parse(orgID); err == nil {
				c.Set(ContextKeyOrganizationID, orgID)
			}
		}
		c.Next()
	}
}

// RequireTenant middleware ensures a tenant is resolved
func RequireTenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, exists := c.Get(ContextKeyTenantID); !exists {
			response.BadRequest(c, "Tenant ID is required")
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequirePermission middleware checks if user has required permission
func RequirePermission(module, resource, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get(ContextKeyClaims)
		if !exists {
			response.Unauthorized(c, "Authentication required")
			c.Abort()
			return
		}

		jwtClaims, ok := claims.(*crypto.Claims)
		if !ok {
			response.Unauthorized(c, "Invalid authentication")
			c.Abort()
			return
		}

		// System admins have all permissions
		if jwtClaims.IsSystemAdmin {
			c.Next()
			return
		}

		// TODO: Check user permissions from database/cache
		// For now, allow all authenticated users
		c.Next()
	}
}

// RequireSystemAdmin middleware ensures user is a system admin
func RequireSystemAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get(ContextKeyClaims)
		if !exists {
			response.Unauthorized(c, "Authentication required")
			c.Abort()
			return
		}

		jwtClaims, ok := claims.(*crypto.Claims)
		if !ok || !jwtClaims.IsSystemAdmin {
			response.Forbidden(c, "System administrator access required")
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetUserID retrieves user ID from context
func GetUserID(c *gin.Context) (uuid.UUID, bool) {
	if id, exists := c.Get(ContextKeyUserID); exists {
		if strID, ok := id.(string); ok {
			if parsed, err := uuid.Parse(strID); err == nil {
				return parsed, true
			}
		}
	}
	return uuid.UUID{}, false
}

// GetTenantID retrieves tenant ID from context
func GetTenantID(c *gin.Context) (uuid.UUID, bool) {
	if id, exists := c.Get(ContextKeyTenantID); exists {
		if strID, ok := id.(string); ok {
			if parsed, err := uuid.Parse(strID); err == nil {
				return parsed, true
			}
		}
	}
	return uuid.UUID{}, false
}

// GetOrganizationID retrieves organization ID from context
func GetOrganizationID(c *gin.Context) (uuid.UUID, bool) {
	if id, exists := c.Get(ContextKeyOrganizationID); exists {
		if strID, ok := id.(string); ok {
			if parsed, err := uuid.Parse(strID); err == nil {
				return parsed, true
			}
		}
	}
	return uuid.UUID{}, false
}

// GetClaims retrieves JWT claims from context
func GetClaims(c *gin.Context) (*crypto.Claims, bool) {
	if claims, exists := c.Get(ContextKeyClaims); exists {
		if jwtClaims, ok := claims.(*crypto.Claims); ok {
			return jwtClaims, true
		}
	}
	return nil, false
}
