package handler

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// migrationRecord is the JSON shape returned to clients for one row of
// schema_migrations. Lower-case JSON keys match the rest of the API.
type migrationRecord struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	AppliedAt time.Time `json:"applied_at"`
}

// GetMigrationStatus returns the list of database migrations that have
// been applied to the running backend's database. Intended for production
// deploy verification when direct psql access isn't available — the system
// admin can hit GET /api/v1/admin/migrations from a browser/curl and
// confirm whether a given migration (e.g. 404, 405, ..., 409) actually
// ran on the live DB.
//
// Route is mounted under the existing `/admin` group in handler.go, which
// is already gated by middleware.RequireSystemAdmin(). No additional
// permission check is needed here.
//
// Response shape (Success envelope):
//
//	{
//	  "data": {
//	    "count":       412,
//	    "min_version": 1,
//	    "max_version": 412,
//	    "migrations":  [
//	      {"version": 1,   "name": "core_schema",     "applied_at": "2025-..."},
//	      ...
//	      {"version": 409, "name": "fix_recompute...","applied_at": "2026-..."}
//	    ]
//	  }
//	}
//
// The list is ordered by version ascending. We pull the whole table on
// purpose (it's small — one row per migration file, typically a few hundred
// at most) so the caller can spot gaps without having to paginate.
func (h *Handler) GetMigrationStatus(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT version, COALESCE(name, '') AS name, applied_at
		FROM schema_migrations
		ORDER BY version ASC
	`)
	if err != nil {
		h.log.Error("Failed to query schema_migrations", "error", err)
		response.InternalError(c, "Failed to read migration status")
		return
	}
	defer rows.Close()

	records := make([]migrationRecord, 0)
	for rows.Next() {
		var r migrationRecord
		if err := rows.Scan(&r.Version, &r.Name, &r.AppliedAt); err != nil {
			h.log.Error("Failed to scan migration row", "error", err)
			continue
		}
		records = append(records, r)
	}

	// Summary stats so the caller can answer "is 409 applied?" without
	// scanning the whole array. min_version is typically 1; max_version is
	// the latest migration this binary's DB knows about.
	minVersion, maxVersion := 0, 0
	if len(records) > 0 {
		minVersion = records[0].Version
		maxVersion = records[len(records)-1].Version
	}

	response.Success(c, gin.H{
		"count":       len(records),
		"min_version": minVersion,
		"max_version": maxVersion,
		"migrations":  records,
	})
}
