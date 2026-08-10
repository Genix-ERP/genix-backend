package middleware

import (
	"net/http"

	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Platform (control-plane) role codes — fixed, code-enforced (Phase 3).
const (
	PlatformRoleSuperAdmin = "super_admin"
	PlatformRoleAdmin      = "admin"
	PlatformRoleManejer    = "manejer"
	PlatformRoleTexSupport = "tex_podderjka"
)

// Capabilities. These are the ONLY authorization units the admin plane checks;
// they are assigned to roles below and never editable at runtime.
const (
	CapCompanyView       = "company.view"
	CapCompanyCreate     = "company.create"
	CapCompanyBlock      = "company.block"
	CapSubscription      = "subscription.manage"
	CapImpersonate       = "impersonate"          // full (read/write)
	CapImpersonateRead   = "impersonate.readonly" // read-only only
	CapPlatformUserMgmt  = "platform_user.manage"
	CapPlansEdit         = "plans.edit"
	CapAuditViewAll      = "audit.view.all"
	CapAuditViewOwn      = "audit.view.own"
	CapPlatformSettings  = "platform.settings"
)

// platformRoleCapabilities is the capability matrix from the Phase 3 spec.
var platformRoleCapabilities = map[string]map[string]bool{
	PlatformRoleSuperAdmin: {
		CapCompanyView: true, CapCompanyCreate: true, CapCompanyBlock: true,
		CapSubscription: true, CapImpersonate: true, CapPlatformUserMgmt: true,
		CapPlansEdit: true, CapAuditViewAll: true, CapPlatformSettings: true,
	},
	PlatformRoleAdmin: {
		CapCompanyView: true, CapCompanyCreate: true, CapCompanyBlock: true,
		CapSubscription: true, CapImpersonate: true, CapPlansEdit: true,
		CapAuditViewAll: true,
	},
	PlatformRoleManejer: {
		CapCompanyView: true, CapSubscription: true, CapAuditViewOwn: true,
	},
	PlatformRoleTexSupport: {
		CapCompanyView: true, CapImpersonateRead: true, CapAuditViewOwn: true,
	},
}

// EffectivePlatformRole returns the platform role for the request's claims.
// A token minted by /platform/auth/login carries PlatformRole. A legacy
// tenant-admin token (is_system_admin, no PlatformRole) is treated as
// super_admin for backward compatibility — it already had full access.
func EffectivePlatformRole(claims *crypto.Claims) string {
	if claims == nil {
		return ""
	}
	if claims.PlatformRole != "" {
		return claims.PlatformRole
	}
	if claims.IsSystemAdmin {
		return PlatformRoleSuperAdmin
	}
	return ""
}

// HasCapability reports whether a platform role holds a capability.
func HasCapability(role, capability string) bool {
	caps, ok := platformRoleCapabilities[role]
	if !ok {
		return false
	}
	return caps[capability]
}

// CapabilitiesForRole returns the capability set of a role (for the read-only
// Rollar/Ruxsatlar matrix screen).
func CapabilitiesForRole(role string) map[string]bool {
	out := map[string]bool{}
	for k, v := range platformRoleCapabilities[role] {
		out[k] = v
	}
	return out
}

// RequireCapability gates an endpoint on a platform capability. It runs AFTER
// RequireSystemAdmin (which already confirmed a platform-plane token), so it can
// assume claims exist; it maps the token to its effective role and checks the
// matrix.
func RequireCapability(capability string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := GetClaims(c)
		if !ok || claims == nil {
			response.Unauthorized(c, "Authentication required")
			c.Abort()
			return
		}
		role := EffectivePlatformRole(claims)
		if !HasCapability(role, capability) {
			response.Forbidden(c, "Missing required platform capability: "+capability)
			c.Abort()
			return
		}
		c.Next()
	}
}

// BlockReadOnlyImpersonationMutations rejects any non-GET/HEAD request made with
// a read-only impersonation token (tex_podderjka). Mounted globally on the tenant
// API so read-only support sessions can never mutate tenant data.
func BlockReadOnlyImpersonationMutations() gin.HandlerFunc {
	return func(c *gin.Context) {
		if claims, ok := GetClaims(c); ok && claims != nil && claims.ReadOnly && claims.ImpersonatedBy != nil {
			switch c.Request.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				// read allowed
			default:
				response.Forbidden(c, "Read-only support session: mutations are not allowed")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
