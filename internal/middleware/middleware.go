package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/infrastructure/cache"
	"github.com/genixerp/genix-backend/internal/infrastructure/database"
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

// Permission cache TTL
const permissionCacheTTL = 5 * time.Minute

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
	// Pre-compute whether wildcard is in the allowed origins
	hasWildcard := false
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			hasWildcard = true
			break
		}
	}

	// Log configured origins on startup for debugging
	fmt.Printf("[CORS] Allowed origins: %v\n", cfg.AllowedOrigins)

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

		if !allowed && origin != "" {
			fmt.Printf("[CORS] Rejected origin: %q for %s %s\n", origin, c.Request.Method, c.Request.URL.Path)
		}

		if allowed {
			if hasWildcard {
				// When wildcard is configured, reflect the requesting origin
				// but do NOT set credentials header (credentials + wildcard is
				// insecure and browsers reject it).
				c.Header("Access-Control-Allow-Origin", origin)
			} else {
				// Specific origins configured — safe to echo origin and allow credentials.
				c.Header("Access-Control-Allow-Origin", origin)
			}
		}

		c.Header("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ", "))
		c.Header("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
		c.Header("Access-Control-Expose-Headers", strings.Join(cfg.ExposedHeaders, ", "))

		// Only allow credentials when specific origins are configured (not wildcard).
		if cfg.AllowCredentials && !hasWildcard {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		c.Header("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))

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

		// Just call c.Next() directly. The context deadline will propagate
		// to database queries and other context-aware operations, causing
		// them to return context.DeadlineExceeded naturally.
		c.Next()

		// If the context expired during processing, set a timeout response
		// (only if a response hasn't already been written).
		if ctx.Err() == context.DeadlineExceeded && !c.Writer.Written() {
			c.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{
				"success": false,
				"error": gin.H{
					"code": "TIMEOUT",
					// The one error emitted outside the response package, so
					// it carries its Uzbek itself.
					"message": "So'rov vaqti tugadi — qaytadan urinib ko'ring",
				},
			})
		}
	}
}

// rateLimitScript is a Lua script that atomically increments a counter and sets
// its TTL. It returns the current count after incrementing.
var rateLimitScript = `
local key = KEYS[1]
local window = tonumber(ARGV[1])
local count = redis.call("INCR", key)
if count == 1 then
    redis.call("EXPIRE", key, window)
end
return count
`

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

		// Atomic increment + expire using Lua script
		windowSeconds := int(cfg.WindowSize.Seconds())
		if windowSeconds < 1 {
			windowSeconds = 1
		}

		result, err := redis.Client().Eval(ctx, rateLimitScript, []string{key}, windowSeconds).Int64()
		if err != nil {
			// If Redis fails, allow the request
			c.Next()
			return
		}
		count := result

		// Check if over limit
		if int(count) > cfg.RequestsLimit {
			c.Header("Retry-After", fmt.Sprintf("%d", windowSeconds))
			response.TooManyRequests(c, "Rate limit exceeded. Please try again later.")
			c.Abort()
			return
		}

		// Add rate limit headers
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", cfg.RequestsLimit))
		remaining := cfg.RequestsLimit - int(count)
		if remaining < 0 {
			remaining = 0
		}
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

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

// orgTenantCache caches verified org→tenant memberships ("orgID|tenantID").
// Membership never changes for a live org (orgs don't move between tenants),
// so positive results are safe to cache for the process lifetime.
var orgTenantCache sync.Map

// OrganizationResolver middleware resolves the organization from the
// X-Organization-ID header. The header is client-supplied, so the org must
// belong to the caller's tenant — otherwise a forged header would stamp
// writes with a foreign organization_id (AI audit 2026-08-10). A mismatched
// header is IGNORED (treated as "no active organization") rather than
// rejected, so a stale header after a tenant switch degrades gracefully.
func OrganizationResolver(db *database.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID := c.GetHeader("X-Organization-ID")
		if orgID == "" {
			c.Next()
			return
		}
		if _, err := uuid.Parse(orgID); err != nil {
			c.Next()
			return
		}
		tenantID := c.GetString(ContextKeyTenantID)
		if tenantID == "" || db == nil {
			c.Next()
			return
		}
		cacheKey := orgID + "|" + tenantID
		if _, ok := orgTenantCache.Load(cacheKey); ok {
			c.Set(ContextKeyOrganizationID, orgID)
			c.Next()
			return
		}
		var one int
		err := db.QueryRow(
			`SELECT 1 FROM organizations WHERE id = $1 AND tenant_id = $2`,
			orgID, tenantID,
		).Scan(&one)
		if err == nil {
			orgTenantCache.Store(cacheKey, struct{}{})
			c.Set(ContextKeyOrganizationID, orgID)
		}
		c.Next()
	}
}

// TrialCheck middleware enforces the 7-day trial / 30-day account clearing policy.
// - If trial_ends_at has passed and status is 'trialing' → advance to 'past_due'.
// - If account_clear_at has passed → mark tenant expired/inactive.
// - If status is 'past_due' or 'expired' → return 402 Payment Required.
// Routes under /api/v1/auth/* and /api/v1/subscription* are always allowed through.
func TrialCheck(db *database.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Allow auth and subscription routes unconditionally
		path := c.FullPath()
		if strings.HasPrefix(path, "/api/v1/auth/") ||
			strings.HasPrefix(path, "/api/v1/subscription") ||
			strings.HasPrefix(path, "/api/v1/admin/") {
			c.Next()
			return
		}

		// System admins bypass trial checks entirely
		if claims, exists := c.Get(ContextKeyClaims); exists {
			if jwtClaims, ok := claims.(*crypto.Claims); ok && jwtClaims.IsSystemAdmin {
				c.Next()
				return
			}
		}

		tenantID := c.GetString(ContextKeyTenantID)
		if tenantID == "" {
			c.Next()
			return
		}

		var (
			status         string
			trialEndsAt    sql.NullTime
			accountClearAt sql.NullTime
			isActive       bool
		)

		err := db.QueryRow(`
			SELECT subscription_status, trial_ends_at, account_clear_at, is_active
			FROM tenants
			WHERE id = $1 AND deleted_at IS NULL
		`, tenantID).Scan(&status, &trialEndsAt, &accountClearAt, &isActive)
		if err != nil {
			// Can't verify — let the request pass (fail-open)
			c.Next()
			return
		}

		now := time.Now()

		// Advance trialing → past_due when trial period ends
		if status == "trialing" && trialEndsAt.Valid && now.After(trialEndsAt.Time) {
			db.Exec(`UPDATE tenants SET subscription_status = 'past_due' WHERE id = $1`, tenantID)
			status = "past_due"
		}

		// Advance past_due / any non-active → expired when account_clear_at passes
		if accountClearAt.Valid && now.After(accountClearAt.Time) && status != "active" {
			db.Exec(`UPDATE tenants SET subscription_status = 'expired', is_active = false WHERE id = $1`, tenantID)
			status = "expired"
			isActive = false
		}

		// Block access when not active
		if !isActive || status == "past_due" || status == "expired" || status == "cancelled" {
			response.Error(c, http.StatusPaymentRequired, "payment_required",
				"Trial period has ended. Please upgrade your plan to continue.")
			c.Abort()
			return
		}

		c.Next()
	}
}

// PermissionChecker holds the dependencies needed for permission checking.
type PermissionChecker struct {
	db    *database.DB
	redis *cache.RedisClient
	log   logger.Logger
}

// NewPermissionChecker creates a new PermissionChecker.
func NewPermissionChecker(db *database.DB, redis *cache.RedisClient, log logger.Logger) *PermissionChecker {
	return &PermissionChecker{
		db:    db,
		redis: redis,
		log:   log,
	}
}

// permissionCacheKey builds the Redis key for a user's permission set.
// The "v2:" prefix invalidates older cache entries when the permission
// map shape changes (e.g. when wildcard module:*:action keys were added).
func permissionCacheKey(tenantID, userID string) string {
	return fmt.Sprintf("permissions:v2:%s:%s", tenantID, userID)
}

// loadPermissions queries the database for all permissions granted to the user
// through their assigned roles and employee module permissions within the given tenant.
// The result is a set of permission strings in the form "module:resource:action".
func (pc *PermissionChecker) loadPermissions(ctx context.Context, tenantID, userID string) (map[string]bool, error) {
	perms := make(map[string]bool)

	// 1. Load from old role_permissions system (backward compat)
	query := `
		SELECT DISTINCT p.module, p.resource, p.action
		FROM permissions p
		INNER JOIN role_permissions rp ON rp.permission_id = p.id
		INNER JOIN user_roles ur ON ur.role_id = rp.role_id
		INNER JOIN roles r ON r.id = ur.role_id AND r.tenant_id = $1
		WHERE ur.user_id = $2
	`

	rows, err := pc.db.QueryContext(ctx, query, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query user permissions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var module, resource, action string
		if err := rows.Scan(&module, &resource, &action); err != nil {
			return nil, fmt.Errorf("failed to scan permission row: %w", err)
		}
		perms[module+":"+resource+":"+action] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating permission rows: %w", err)
	}

	// 2. Load from employee_module_permissions (via user -> employee link)
	empQuery := `
		SELECT emp.module_id, emp.can_create, emp.can_read, emp.can_update, emp.can_delete
		FROM employee_module_permissions emp
		INNER JOIN users u ON u.employee_id = emp.employee_id AND u.tenant_id = emp.tenant_id
		WHERE u.id = $1 AND u.tenant_id = $2
	`

	empRows, err := pc.db.QueryContext(ctx, empQuery, userID, tenantID)
	if err != nil {
		// Don't fail entirely, just log and continue with what we have
		return perms, nil
	}
	defer empRows.Close()

	// Collect all known resources per module so we can grant granular permissions
	resourceMap := map[string][]string{
		"inventory":     {"product", "warehouse", "stock", "category", "lot", "bom", "carrier", "product_attribute", "product_variant", "reorder", "scrap"},
		"sales":         {"order", "invoice", "customer", "quotation", "payment", "delivery", "discount", "dropship", "pricelist", "quotation_template", "return", "payment_term"},
		"purchase":      {"order", "vendor", "bill", "rfq", "contract", "invoice", "price_history", "purchase_order", "receipt", "requisition", "return", "rule"},
		"hr":            {"employee", "department", "payroll", "contract", "attendance", "leave"},
		"finance":       {"account", "journal", "journal_entry", "transaction", "report", "budget", "asset", "bank_account", "cash", "cash_transaction", "currency", "expense", "followup", "followup_level", "payment", "reconciliation", "tax_report"},
		"crm":           {"contact", "lead", "opportunity", "pipeline", "call", "activity", "report"},
		"organization":  {"organization", "department"},
		"users":         {"user", "role"},
		"tasks":         {"board", "column", "task"},
		"manufacturing": {"production_orders", "work_orders", "work_centers", "transfers", "equipment", "cost_calculations", "mrp", "quality_checks"},
		"assets":        {"asset", "category", "depreciation"},
		"expenses":      {"expense", "report", "category"},
		"ai":            {"conversation"},
		"audit":         {"log"},
		"settings":      {"organization", "tenant"},
		"workflow":      {"workflow"},
		"contracts":     {"contract"},
		"cargo":         {"shipment", "distribution", "cash"},
		"construction":  {"project", "projects", "estimate", "smeta", "wbs", "daily_log", "reports", "material_request"},
		"payroll":       {"payroll", "employee"},
		"dashboard":     {"dashboard"},
	}

	// Cross-module dependencies: when a user has access to one module,
	// they also need read access to supporting resources from other modules.
	crossModuleGrants := map[string][]string{
		"hr":            {"organization:department", "organization:organization", "users:role", "users:user"},
		// Omborchi «Kiruvchi zayavkalar» inboxini ko'rishi kerak (material
		// zayavkalari v2) — chiqarish/rad amallari baribir inventory:stock:adjust
		// bilan gate'langan.
		"inventory":     {"settings:tenant", "construction:material_request"},
		"sales":         {"settings:tenant", "inventory:product", "inventory:warehouse", "inventory:carrier", "inventory:product_variant", "inventory:product_attribute", "users:user", "crm:contact"},
		// Ta'minotchi zayavkadan kelgan xarid so'rovining manbasini ochib
		// ko'ra olishi kerak (material zayavkalari v2).
		"purchase":      {"settings:tenant", "inventory:product", "inventory:warehouse", "inventory:carrier", "inventory:product_variant", "users:user", "crm:contact", "construction:material_request"},
		"finance":       {"settings:tenant"},
		"manufacturing": {"inventory:product", "inventory:bom", "inventory:warehouse"},
		"construction":  {"organization:organization", "hr:employee", "inventory:product", "inventory:warehouse"},
		"assets":        {"finance:asset", "finance:report"},
		"expenses":      {"finance:expense", "finance:report"},
		"payroll":       {"hr:employee", "hr:payroll"},
		"contracts":     {"purchase:contract", "purchase:order"},
		// Task management needs the HR employee list for the assignee picker.
		"tasks":         {"hr:employee"},
		// CRM needs the employee list (responsible picker) and task boards
		// ("Vazifa qo'yish" quick action + the no-task marker).
		"crm":           {"hr:employee", "tasks:board", "tasks:task"},
	}

	for empRows.Next() {
		var moduleID string
		var canCreate, canRead, canUpdate, canDelete bool
		if err := empRows.Scan(&moduleID, &canCreate, &canRead, &canUpdate, &canDelete); err != nil {
			continue
		}

		// Wildcard grants: module:*:action.
		// The frontend stores permissions at the module level only (one
		// can_read/can_create/can_update/can_delete per module), but the
		// backend `Require()` middleware checks 3-level keys like
		// "inventory:product:read". The resourceMap below enumerates
		// known sub-resources per module, but it's easy to drift — any
		// route that uses a sub-resource not in the map silently 403s
		// even though the user clearly has module-level access (and the
		// sidebar shows the tab). We also publish a wildcard variant so
		// `Require()` can fall back to "module:*:action" when the
		// specific "module:resource:action" key is missing.
		if canCreate {
			perms[moduleID+":*:create"] = true
		}
		if canRead {
			perms[moduleID+":*:read"] = true
		}
		if canUpdate {
			perms[moduleID+":*:update"] = true
			perms[moduleID+":*:manage"] = true
			perms[moduleID+":*:adjust"] = true
			perms[moduleID+":*:transfer"] = true
			perms[moduleID+":*:approve"] = true
			perms[moduleID+":*:confirm"] = true
			perms[moduleID+":*:post"] = true
		}
		if canDelete {
			perms[moduleID+":*:delete"] = true
		}

		resources, ok := resourceMap[moduleID]
		if !ok {
			// Unknown module — grant wildcard-style with module as resource
			resources = []string{moduleID}
		}

		for _, res := range resources {
			if canCreate {
				perms[moduleID+":"+res+":create"] = true
			}
			if canRead {
				perms[moduleID+":"+res+":read"] = true
			}
			if canUpdate {
				perms[moduleID+":"+res+":update"] = true
				// Grant special actions that map to update permission
				perms[moduleID+":"+res+":manage"] = true
				perms[moduleID+":"+res+":adjust"] = true
				perms[moduleID+":"+res+":transfer"] = true
				perms[moduleID+":"+res+":approve"] = true
				perms[moduleID+":"+res+":confirm"] = true
				perms[moduleID+":"+res+":post"] = true
			}
			if canDelete {
				perms[moduleID+":"+res+":delete"] = true
			}
		}

		// Grant cross-module dependencies (e.g. HR needs organization:department access)
		if grants, ok := crossModuleGrants[moduleID]; ok {
			for _, grant := range grants {
				if canRead {
					perms[grant+":read"] = true
					perms[grant+":manage"] = true
				}
				if canCreate {
					perms[grant+":create"] = true
				}
				if canUpdate {
					perms[grant+":update"] = true
					perms[grant+":manage"] = true
					perms[grant+":approve"] = true
					perms[grant+":confirm"] = true
				}
				if canDelete {
					perms[grant+":delete"] = true
				}
			}
		}
	}

	return perms, nil
}

// getUserPermissions returns the set of permissions for the user, using Redis
// cache when available.
func (pc *PermissionChecker) getUserPermissions(ctx context.Context, tenantID, userID string) (map[string]bool, error) {
	cacheKey := permissionCacheKey(tenantID, userID)

	// Try cache first
	if pc.redis != nil {
		data, err := pc.redis.Get(ctx, cacheKey)
		if err == nil && data != "" {
			var perms map[string]bool
			if jsonErr := json.Unmarshal([]byte(data), &perms); jsonErr == nil {
				return perms, nil
			}
		}
	}

	// Cache miss — load from database
	perms, err := pc.loadPermissions(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}

	// Store in cache
	if pc.redis != nil {
		if encoded, err := json.Marshal(perms); err == nil {
			_ = pc.redis.Set(ctx, cacheKey, string(encoded), permissionCacheTTL)
		}
	}

	return perms, nil
}

// isUserSiteAdminOrOwner checks the users table role column for elevated roles
// that should bypass permission checks (owner, site_admin).
func (pc *PermissionChecker) isUserSiteAdminOrOwner(ctx context.Context, tenantID, userID string) (bool, error) {
	cacheKey := fmt.Sprintf("userrole:%s:%s", tenantID, userID)

	// Try cache first
	if pc.redis != nil {
		data, err := pc.redis.Get(ctx, cacheKey)
		if err == nil && data != "" {
			return data == "1", nil
		}
	}

	var role sql.NullString
	err := pc.db.QueryRowContext(ctx,
		`SELECT role FROM users WHERE id = $1 AND tenant_id = $2`,
		userID, tenantID,
	).Scan(&role)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	bypass := role.Valid && (role.String == "owner" || role.String == "site_admin")

	// Cache the result
	if pc.redis != nil {
		val := "0"
		if bypass {
			val = "1"
		}
		_ = pc.redis.Set(ctx, cacheKey, val, permissionCacheTTL)
	}

	return bypass, nil
}

// Require returns a Gin middleware that checks whether the authenticated user
// has the specified permission (module:resource:action). System admins and
// site admins/owners bypass the check.
func (pc *PermissionChecker) Require(module, resource, action string) gin.HandlerFunc {
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

		ctx := c.Request.Context()
		userID := jwtClaims.UserID.String()
		tenantID := jwtClaims.TenantID.String()

		// Site admins and owners bypass permission checks
		bypass, err := pc.isUserSiteAdminOrOwner(ctx, tenantID, userID)
		if err != nil {
			pc.log.Error("failed to check user role", "error", err, "user_id", userID)
			response.Forbidden(c, "Permission check failed")
			c.Abort()
			return
		}
		if bypass {
			c.Next()
			return
		}

		// Check user permissions
		perms, err := pc.getUserPermissions(ctx, tenantID, userID)
		if err != nil {
			pc.log.Error("failed to load user permissions", "error", err, "user_id", userID)
			response.Forbidden(c, "Permission check failed")
			c.Abort()
			return
		}

		// Check specific key first; fall back to module-level wildcard
		// (module:*:action) so users with module-level access don't 403
		// on sub-resources that the resourceMap doesn't enumerate.
		// This keeps the sidebar's module-only gating in sync with what
		// the API will actually allow.
		permKey := module + ":" + resource + ":" + action
		wildcardKey := module + ":*:" + action
		if !perms[permKey] && !perms[wildcardKey] {
			response.Forbidden(c, fmt.Sprintf("Missing required permission: %s", permKey))
			c.Abort()
			return
		}

		c.Next()
	}
}

// Can reports whether the current request's user holds module:resource:action.
// It mirrors Require's logic exactly — system admins and site admins/owners
// bypass, otherwise the permission map is checked with the module:*:action
// wildcard fallback — but returns a bool instead of aborting the request. It is
// for callers that gate individual operations (e.g. the AI agent gating each
// tool) rather than whole routes. On any error it fails closed (returns false).
func (pc *PermissionChecker) Can(c *gin.Context, module, resource, action string) bool {
	claims, exists := c.Get(ContextKeyClaims)
	if !exists {
		return false
	}
	jwtClaims, ok := claims.(*crypto.Claims)
	if !ok {
		return false
	}
	if jwtClaims.IsSystemAdmin {
		return true
	}
	ctx := c.Request.Context()
	userID := jwtClaims.UserID.String()
	tenantID := jwtClaims.TenantID.String()
	bypass, err := pc.isUserSiteAdminOrOwner(ctx, tenantID, userID)
	if err != nil {
		pc.log.Error("agent perm: role check failed", "error", err, "user_id", userID)
		return false
	}
	if bypass {
		return true
	}
	perms, err := pc.getUserPermissions(ctx, tenantID, userID)
	if err != nil {
		pc.log.Error("agent perm: load failed", "error", err, "user_id", userID)
		return false
	}
	return perms[module+":"+resource+":"+action] || perms[module+":*:"+action]
}

// InvalidatePermissionCache removes cached permissions for a user so that
// changes (e.g., role assignment) take effect immediately.
func (pc *PermissionChecker) InvalidatePermissionCache(ctx context.Context, tenantID, userID string) {
	if pc.redis == nil {
		return
	}
	_ = pc.redis.Delete(ctx, permissionCacheKey(tenantID, userID))
	_ = pc.redis.Delete(ctx, fmt.Sprintf("userrole:%s:%s", tenantID, userID))
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

		// SEC-03 (docs/admin-panel/audit.md): defence-in-depth on the single
		// admin choke point. A platform-admin token carries the platform scope;
		// a tenant token carries the tenant scope. Reject anything explicitly
		// scoped to the tenant plane even if the isa flag were somehow set.
		// Legacy tokens minted before the scope field existed carry an empty
		// scope and are still accepted (transition window).
		if jwtClaims.Scope == crypto.ScopeTenant {
			response.Forbidden(c, "System administrator access required")
			c.Abort()
			return
		}

		c.Next()
	}
}

// IsSystemAdmin reports whether the caller is a platform administrator.
// The bool twin of RequireSystemAdmin, for handlers that need to branch on it
// rather than reject outright — e.g. a route where the same verb means a
// per-tenant action for ordinary users and a shared-catalogue edit for
// platform admins. Fails closed: no claims, or claims of the wrong type,
// means not an admin.
func IsSystemAdmin(c *gin.Context) bool {
	claims, exists := c.Get(ContextKeyClaims)
	if !exists {
		return false
	}
	jwtClaims, ok := claims.(*crypto.Claims)
	return ok && jwtClaims.IsSystemAdmin
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
