package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// Two-factor enrolment for platform staff.
//
// PlatformLogin has always ENFORCED TOTP when totp_enabled is set, but nothing
// could turn it on — the column had to be edited by hand, which in practice
// meant nobody used it. These three endpoints close that gap.
//
// Enrolment is deliberately two-step: begin returns a secret and stores it
// WITHOUT setting totp_enabled, and only a correct code from the authenticator
// flips the flag. Enabling on the first call would lock a user out of their own
// account the moment they mistyped the secret into their phone or closed the
// dialog early — and the account that administers the platform is the worst one
// to be locked out of.
//
// Every route here acts on the CALLER's own account. Enrolling someone else's
// second factor is not an administrative action, it is a way to take their
// account, so there is no user id parameter anywhere in this file.

func platformUserIDFrom(c *gin.Context) (string, bool) {
	claims, _ := middleware.GetClaims(c)
	if claims == nil || claims.PlatformUserID == nil {
		return "", false
	}
	return claims.PlatformUserID.String(), true
}

// BeginPlatformTOTPEnrollment godoc
// @Summary Start 2FA enrolment
// @Tags Platform - Auth
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /admin/platform/totp/begin [post]
func (h *Handler) BeginPlatformTOTPEnrollment(c *gin.Context) {
	userID, ok := platformUserIDFrom(c)
	if !ok {
		// A tenant-admin token has no platform_users row to attach a secret to.
		// Refusing here rather than inventing one keeps the legacy path from
		// quietly creating half-configured platform accounts.
		response.Error(c, http.StatusForbidden, "PLATFORM_ACCOUNT_REQUIRED",
			"2FA faqat platforma hisobi uchun sozlanadi — ERP tokeni bilan emas")
		return
	}

	var email string
	var enabled bool
	if err := h.db.QueryRow(
		`SELECT email, totp_enabled FROM platform_users WHERE id = $1 AND deleted_at IS NULL`,
		userID).Scan(&email, &enabled); err != nil {
		response.NotFound(c, "Platform user")
		return
	}
	if enabled {
		// Re-enrolling would silently invalidate the authenticator they are
		// already using. Disabling first is an explicit, audited act.
		response.BadRequest(c, "2FA allaqachon yoqilgan — avval o'chiring")
		return
	}

	secret, err := crypto.GenerateTOTPSecret()
	if err != nil {
		h.log.Error("totp secret generation failed", "error", err)
		response.InternalError(c, "2FA kalitini yaratib bo'lmadi")
		return
	}

	// Stored but NOT enabled. An abandoned enrolment leaves a dormant secret
	// that the next begin overwrites, and login is unaffected because it only
	// consults the secret when totp_enabled is true.
	if _, err := h.db.Exec(
		`UPDATE platform_users SET totp_secret = $1, updated_at = NOW() WHERE id = $2`,
		secret, userID); err != nil {
		h.log.Error("totp secret store failed", "error", err)
		response.InternalError(c, "2FA kalitini saqlab bo'lmadi")
		return
	}

	issuer := "Genix"
	otpauth := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		url.PathEscape(issuer), url.PathEscape(email), secret, url.QueryEscape(issuer))

	response.Success(c, gin.H{
		"secret":  secret,
		"otpauth": otpauth,
		// The client renders the QR itself. Generating the image server-side
		// would put the secret through another encoder and another response
		// body for no benefit.
		"note": "Kodni tasdiqlaguningizcha 2FA yoqilmaydi",
	})
}

// ConfirmPlatformTOTPEnrollment godoc
// @Summary Confirm 2FA with a code from the authenticator
// @Tags Platform - Auth
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /admin/platform/totp/confirm [post]
func (h *Handler) ConfirmPlatformTOTPEnrollment(c *gin.Context) {
	userID, ok := platformUserIDFrom(c)
	if !ok {
		response.Error(c, http.StatusForbidden, "PLATFORM_ACCOUNT_REQUIRED",
			"2FA faqat platforma hisobi uchun sozlanadi")
		return
	}

	var input struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Kod kiritilmadi")
		return
	}

	var secret sql.NullString
	if err := h.db.QueryRow(
		`SELECT totp_secret FROM platform_users WHERE id = $1 AND deleted_at IS NULL`,
		userID).Scan(&secret); err != nil {
		response.NotFound(c, "Platform user")
		return
	}
	if !secret.Valid || secret.String == "" {
		response.BadRequest(c, "Avval 2FA sozlashni boshlang")
		return
	}
	if !crypto.VerifyTOTP(secret.String, input.Code) {
		response.Error(c, http.StatusBadRequest, "TOTP_INVALID", "Kod noto'g'ri yoki muddati o'tgan")
		return
	}

	if _, err := h.db.Exec(
		`UPDATE platform_users SET totp_enabled = true, updated_at = NOW() WHERE id = $1`,
		userID); err != nil {
		h.log.Error("totp enable failed", "error", err)
		response.InternalError(c, "2FA ni yoqib bo'lmadi")
		return
	}
	h.writePlatformAudit(c, "platform.totp.enabled", "platform_user", userID, nil, nil, nil)

	response.Success(c, gin.H{"totp_enabled": true})
}

// DisablePlatformTOTP godoc
// @Summary Turn 2FA off
// @Tags Platform - Auth
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /admin/platform/totp [delete]
//
// Requires a CURRENT code, not just a session. A stolen session should not be
// able to strip the second factor off the account it stole — that is precisely
// the attack 2FA exists to survive.
func (h *Handler) DisablePlatformTOTP(c *gin.Context) {
	userID, ok := platformUserIDFrom(c)
	if !ok {
		response.Error(c, http.StatusForbidden, "PLATFORM_ACCOUNT_REQUIRED",
			"2FA faqat platforma hisobi uchun sozlanadi")
		return
	}

	var input struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Kod kiritilmadi")
		return
	}

	var secret sql.NullString
	var enabled bool
	if err := h.db.QueryRow(
		`SELECT totp_secret, totp_enabled FROM platform_users WHERE id = $1 AND deleted_at IS NULL`,
		userID).Scan(&secret, &enabled); err != nil {
		response.NotFound(c, "Platform user")
		return
	}
	if !enabled {
		response.BadRequest(c, "2FA yoqilmagan")
		return
	}
	if !secret.Valid || !crypto.VerifyTOTP(secret.String, input.Code) {
		response.Error(c, http.StatusBadRequest, "TOTP_INVALID", "Kod noto'g'ri yoki muddati o'tgan")
		return
	}

	// The secret is cleared as well as the flag. Leaving it behind means a
	// later re-enable silently resurrects an authenticator entry the user may
	// have deleted months ago.
	if _, err := h.db.Exec(
		`UPDATE platform_users SET totp_enabled = false, totp_secret = NULL, updated_at = NOW() WHERE id = $1`,
		userID); err != nil {
		h.log.Error("totp disable failed", "error", err)
		response.InternalError(c, "2FA ni o'chirib bo'lmadi")
		return
	}
	h.writePlatformAudit(c, "platform.totp.disabled", "platform_user", userID, nil, nil, nil)

	response.Success(c, gin.H{"totp_enabled": false})
}
