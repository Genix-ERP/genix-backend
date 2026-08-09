package handler

import (
	"database/sql"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// NARX-QO'RIQCHI (qurilish-v2/conventions.md §4).
//
// Audit 2026-08-09: narx-yashirish faqat frontendda edi (viewRole — local
// state), server 6 o'qish-endpointda pulni har qanday construction:*:read
// egasiga qaytarardi. Endi qoida SERVER tomonda:
//   - sof-PRORAB rol (foreman bor, supervisor/engineer YO'Q) uchun:
//     ish-ro'yxati endpointlari pul maydonlarini 0 + price_hidden:true bilan
//     qaytaradi; pul-hisobot endpointlari esa 403.
//   - rol biriktirilmagan (admin/demo, owner-fallback), supervisor/engineer,
//     JWT system-admin, users.role owner/site_admin → to'liq ko'rinish.
// resolveProjectRoles = 4 nuqtaviy so'rov (indekslangan) — har o'qishda
// chaqirish arzon; kesh yo'q, chunki rol o'zgarishi darhol kuchga kirsin.
// =====================================================

// constructionPriceRestricted — chaqiruvchining ushbu loyihada YAGONA roli
// prorab ekanini aytadi (narx ko'rish taqiqlanadi).
func (h *Handler) constructionPriceRestricted(c *gin.Context, tenantID uuid.UUID, projectID int64) bool {
	userID, ok := middleware.GetUserID(c)
	if !ok || userID == uuid.Nil {
		return false // ichki chaqiriq — route-gate allaqachon o'tgan
	}
	if claims, cok := middleware.GetClaims(c); cok && claims != nil && claims.IsSystemAdmin {
		return false
	}
	roles := h.resolveProjectRoles(tenantID, userID, projectID)
	if len(roles) == 0 {
		return false // rol biriktirilmagan — admin/demo rejim (work-gate bilan bir xil semantika)
	}
	if roleSetHas(roles, "supervisor") || roleSetHas(roles, "engineer") {
		return false
	}
	if !roleSetHas(roles, "foreman") {
		return false
	}
	// Sof-prorab — lekin tenant-admin bo'lsa baribir to'liq ko'radi.
	var role sql.NullString
	if err := h.db.QueryRow(
		`SELECT role FROM users WHERE id = $1 AND tenant_id = $2`, userID, tenantID,
	).Scan(&role); err == nil && role.Valid && (role.String == "owner" || role.String == "site_admin") {
		return false
	}
	return true
}

// constructionPriceRestrictedForEstimate — smeta-id orqali loyihani hal qilib
// tekshiradi (estimate-scoped endpointlar uchun).
func (h *Handler) constructionPriceRestrictedForEstimate(c *gin.Context, tenantID uuid.UUID, estimateID int64) bool {
	var projectID int64
	if err := h.db.QueryRow(
		`SELECT project_id FROM construction_estimate WHERE id = $1 AND tenant_id = $2`,
		estimateID, tenantID,
	).Scan(&projectID); err != nil {
		return false
	}
	return h.constructionPriceRestricted(c, tenantID, projectID)
}

// denyPriceRestricted — pul-hisobot endpointlari uchun 403-guard.
// true qaytarsa handler darhol return qilishi kerak.
func (h *Handler) denyPriceRestricted(c *gin.Context, tenantID uuid.UUID, projectID int64) bool {
	if h.constructionPriceRestricted(c, tenantID, projectID) {
		response.Forbidden(c, "Price data is hidden for your project role")
		return true
	}
	return false
}
