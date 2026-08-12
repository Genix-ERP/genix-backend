package handler

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// PROJECT STAGE COUNTS — per-estimate Bosqichlar badge feed
// =====================================================
//
// The Bosqichlar tab shows an "N etap" badge on every block button. The
// frontend used to compute those counts by downloading the FULL line set
// (page_size 5000) of EVERY единич estimate in the project — including
// stale re-imports and subcontractor estimates — just to run its
// deriveStages() grouping and read `.length`. On long-lived projects that
// was N heavy requests (multi-MB each) per tab visit and per work-confirm
// reload, and it was the dominant cost of the Smeta page.
//
// This endpoint returns the same numbers from ONE two-column query. The
// stage-key derivation is a faithful port of StagesTabV2's
// splitPath → dropSectionPrefix → deriveStages first-segment logic:
//
//   path       = parent_item_number of a top-level work row
//   parts      = split on " › " (U+203A, the import path delimiter)
//   parts[0] dropped when it is a "СЕКЦИЯ …" / "РАЗДЕЛ …" header
//   stage key  = parts[0] after the drop; empty path or header-only path
//                falls into one synthetic "uncategorised" bucket
//   count      = number of distinct stage keys per estimate
//
// Only top-level work rows (resource_type = '' AND parent_line_id NULL/0)
// participate — sub-stage children inherit their parent's section, so they
// can never introduce a stage key of their own.

// sectionPrefixRe mirrors StagesTabV2's SECTION_PREFIX_RE. JS \b is
// ASCII-only so the frontend matches "СЕКЦИЯ" via a followed-by check;
// RE2 handles the same alternation directly.
var sectionPrefixRe = regexp.MustCompile(`(?i)^(СЕКЦИЯ|РАЗДЕЛ)(\s|$|[№#:])`)

// stageCountRow is one estimate's badge value.
type stageCountRow struct {
	EstimateID int64 `json:"estimate_id"`
	BuildingID int64 `json:"building_id"` // 0 = no building (whole-project bucket)
	StageCount int   `json:"stage_count"`
}

// stageKeyForPath collapses a parent_item_number into the stage bucket key
// deriveStages would file the work under.
func stageKeyForPath(path string) string {
	const uncategorised = "__uncategorised__"
	parts := []string{}
	for _, p := range strings.Split(path, " › ") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 && sectionPrefixRe.MatchString(parts[0]) {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return uncategorised
	}
	return parts[0]
}

// GetProjectStageCounts returns [{estimate_id, building_id, stage_count}]
// for every единич estimate of the project.
//
// Route: GET /construction/projects/:id/stage-counts
func (h *Handler) GetProjectStageCounts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT e.id, COALESCE(e.building_id, 0), COALESCE(l.parent_item_number, '')
		FROM construction_estimate_line l
		JOIN construction_estimate e ON e.id = l.estimate_id
		WHERE e.project_id = $1
		  AND e.tenant_id = $2
		  AND l.tenant_id = $2
		  AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
		  AND COALESCE(l.resource_type, '') = ''
		  AND COALESCE(l.parent_line_id, 0) = 0`,
		projectID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to load stage-count rows", "error", err, "project_id", projectID)
		response.InternalError(c, "Failed to compute stage counts")
		return
	}
	defer rows.Close()

	type acc struct {
		buildingID int64
		keys       map[string]struct{}
	}
	perEstimate := map[int64]*acc{}
	order := []int64{}
	for rows.Next() {
		var estID, buildingID int64
		var path string
		if err := rows.Scan(&estID, &buildingID, &path); err != nil {
			continue
		}
		a := perEstimate[estID]
		if a == nil {
			a = &acc{buildingID: buildingID, keys: map[string]struct{}{}}
			perEstimate[estID] = a
			order = append(order, estID)
		}
		a.keys[stageKeyForPath(path)] = struct{}{}
	}

	out := make([]stageCountRow, 0, len(order))
	for _, estID := range order {
		a := perEstimate[estID]
		out = append(out, stageCountRow{
			EstimateID: estID,
			BuildingID: a.buildingID,
			StageCount: len(a.keys),
		})
	}
	response.Success(c, out)
}
