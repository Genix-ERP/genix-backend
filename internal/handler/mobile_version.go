package handler

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// mobileAppVersion mirrors a row of mobile_app_versions.
type mobileAppVersion struct {
	Platform      string    `json:"platform"`
	LatestVersion string    `json:"latest_version"`
	MinVersion    string    `json:"min_version"`
	UpdateURL     string    `json:"update_url"`
	ReleaseNotes  string    `json:"release_notes"`
	ForceUpdate   bool      `json:"force_update"`
	IsActive      bool      `json:"is_active"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// parseSemver turns "v1.4.2-beta" / "1.4" / "12" into a comparable [major,
// minor, patch] triple. Missing segments are 0; a pre-release/build suffix
// (after '-' or '+') is ignored; non-numeric junk parses as 0. Never errors —
// unparseable input sorts as 0.0.0, which just means "no update required".
func parseSemver(v string) [3]int {
	var out [3]int
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	for i, part := range strings.Split(v, ".") {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(strings.TrimSpace(part))
		if n < 0 {
			n = 0
		}
		out[i] = n
	}
	return out
}

// compareSemver returns -1 if a<b, 0 if equal, 1 if a>b (patch-level precision).
func compareSemver(a, b string) int {
	pa, pb := parseSemver(a), parseSemver(b)
	for i := 0; i < 3; i++ {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

// CheckMobileVersion is the PUBLIC endpoint the mobile app calls on launch.
//
//	GET /api/v1/mobile/version?platform=android&version=1.2.0
//
// It reports whether a newer version exists (update_available) and whether the
// client is too old to keep running (update_required = force_update OR the
// client version is below min_version).
// @Summary  Mobile app version check
// @Tags     Mobile
// @Router   /mobile/version [get]
func (h *Handler) CheckMobileVersion(c *gin.Context) {
	platform := strings.ToLower(strings.TrimSpace(c.Query("platform")))
	if platform != "android" && platform != "ios" {
		response.BadRequest(c, "platform must be 'android' or 'ios'")
		return
	}
	current := strings.TrimSpace(c.Query("version"))
	if current == "" {
		current = strings.TrimSpace(c.Query("current_version"))
	}
	if current == "" {
		response.BadRequest(c, "version is required")
		return
	}

	var v mobileAppVersion
	err := h.db.QueryRow(`
		SELECT platform, latest_version, min_version, update_url, release_notes, force_update, is_active, updated_at
		FROM mobile_app_versions WHERE platform = $1
	`, platform).Scan(&v.Platform, &v.LatestVersion, &v.MinVersion, &v.UpdateURL,
		&v.ReleaseNotes, &v.ForceUpdate, &v.IsActive, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		// No config for this platform yet — nothing to enforce.
		response.Success(c, gin.H{
			"platform":         platform,
			"current_version":  current,
			"update_available": false,
			"update_required":  false,
		})
		return
	}
	if err != nil {
		h.log.Error("mobile version check failed", "error", err, "platform", platform)
		response.InternalError(c, "Failed to check version")
		return
	}

	// A disabled row, or one with no download link, advertises no update — the
	// app keeps working rather than being pushed to a dead link.
	gateOn := v.IsActive && strings.TrimSpace(v.UpdateURL) != ""
	updateAvailable := gateOn && compareSemver(current, v.LatestVersion) < 0
	updateRequired := gateOn && (v.ForceUpdate || compareSemver(current, v.MinVersion) < 0)

	response.Success(c, gin.H{
		"platform":         platform,
		"current_version":  current,
		"latest_version":   v.LatestVersion,
		"min_version":      v.MinVersion,
		"update_available": updateAvailable,
		"update_required":  updateRequired,
		"update_url":       v.UpdateURL,
		"release_notes":    v.ReleaseNotes,
	})
}

// ListMobileVersions returns every platform config for the Settings screen.
// System-admin only (route-gated).
// @Summary  List mobile app version configs
// @Tags     Admin - Mobile
// @Router   /admin/mobile-versions [get]
func (h *Handler) ListMobileVersions(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT platform, latest_version, min_version, update_url, release_notes, force_update, is_active, updated_at
		FROM mobile_app_versions ORDER BY platform
	`)
	if err != nil {
		h.log.Error("failed to list mobile versions", "error", err)
		response.InternalError(c, "Failed to load mobile versions")
		return
	}
	defer rows.Close()

	out := make([]mobileAppVersion, 0, 2)
	for rows.Next() {
		var v mobileAppVersion
		if err := rows.Scan(&v.Platform, &v.LatestVersion, &v.MinVersion, &v.UpdateURL,
			&v.ReleaseNotes, &v.ForceUpdate, &v.IsActive, &v.UpdatedAt); err != nil {
			continue
		}
		out = append(out, v)
	}
	response.Success(c, out)
}

// updateMobileVersionInput is the admin edit payload.
type updateMobileVersionInput struct {
	LatestVersion string `json:"latest_version"`
	MinVersion    string `json:"min_version"`
	UpdateURL     string `json:"update_url"`
	ReleaseNotes  string `json:"release_notes"`
	ForceUpdate   bool   `json:"force_update"`
	IsActive      *bool  `json:"is_active"`
}

// UpsertMobileVersion sets the config for one platform. System-admin only.
// @Summary  Update mobile app version config
// @Tags     Admin - Mobile
// @Router   /admin/mobile-versions/{platform} [put]
func (h *Handler) UpsertMobileVersion(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	platform := strings.ToLower(strings.TrimSpace(c.Param("platform")))
	if platform != "android" && platform != "ios" {
		response.BadRequest(c, "platform must be 'android' or 'ios'")
		return
	}

	var in updateMobileVersionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	in.LatestVersion = strings.TrimSpace(in.LatestVersion)
	in.MinVersion = strings.TrimSpace(in.MinVersion)
	in.UpdateURL = strings.TrimSpace(in.UpdateURL)
	if in.LatestVersion == "" || in.MinVersion == "" {
		response.BadRequest(c, "latest_version and min_version are required")
		return
	}
	// min_version can't be newer than latest_version — that would force an
	// update to a version that doesn't exist yet and hard-lock every client.
	if compareSemver(in.MinVersion, in.LatestVersion) > 0 {
		response.BadRequest(c, "min_version cannot be greater than latest_version")
		return
	}

	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}

	var updatedBy interface{}
	if userID != uuid.Nil {
		updatedBy = userID
	}

	_, err := h.db.Exec(`
		INSERT INTO mobile_app_versions
			(platform, latest_version, min_version, update_url, release_notes, force_update, is_active, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), $8)
		ON CONFLICT (platform) DO UPDATE SET
			latest_version = EXCLUDED.latest_version,
			min_version    = EXCLUDED.min_version,
			update_url     = EXCLUDED.update_url,
			release_notes  = EXCLUDED.release_notes,
			force_update   = EXCLUDED.force_update,
			is_active      = EXCLUDED.is_active,
			updated_at     = NOW(),
			updated_by     = EXCLUDED.updated_by
	`, platform, in.LatestVersion, in.MinVersion, in.UpdateURL, in.ReleaseNotes, in.ForceUpdate, isActive, updatedBy)
	if err != nil {
		h.log.Error("failed to upsert mobile version", "error", err, "platform", platform)
		response.InternalError(c, "Failed to save mobile version")
		return
	}

	h.writePlatformAudit(c, "mobile_version.upsert", "mobile_version", platform, nil, nil,
		map[string]interface{}{
			"latest_version": in.LatestVersion, "min_version": in.MinVersion,
			"force_update": in.ForceUpdate, "is_active": isActive,
		})

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Mobile version updated", "platform": platform})
}
