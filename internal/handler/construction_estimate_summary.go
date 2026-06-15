package handler

import (
	"strconv"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// ESTIMATE SUMMARY — server-side stat-card aggregates
// =====================================================
//
// Mirrors the `kpis` / `resourceCount` calculations that SmetaManagementTab
// (and the Bosqichlar / Smetalar stat cards) currently do in the browser by
// looping the FULL estimate-line array. Moving them server-side lets the
// mobile client (which paginates lines 20-at-a-time and never holds the whole
// estimate in memory) show the same headline numbers without downloading
// every row.
//
// The math is a faithful port of the front logic so the numbers match:
//
//   resource line   = a child line that is NOT a sub-stage marker
//                     (sub-stage marker = resource_type '' AND norm_rate 0).
//   resource cost c = unit_rate * quantity, counted only when > 0.
//   classify(rt)    = labor    → ish / ishchi / worker / labor
//                     machines → equipment / machine / mashina / masina
//                     materials→ everything else (incl. 'material', unknown).
//   labor/machines/materials = Σ c per bucket over every resource line.
//   grand           = Σ over every TOP-LEVEL line of:
//                       (has direct resources ? Σ their c : total_amount)
//                       + Σ over its sub-stage children of
//                           (sub-stage has resources ? Σ their c : its total_amount)
//   work_count      = number of top-level lines.
//   filled_count    = top-level lines whose quantity > 0.
//   resource_count  = distinct (lower(name), lower(uom)) over rows whose
//                     resource_type is non-empty (the "Resurslar N" badge).
//
// Per-estimate. A block view that spans several единич estimates sums these
// across the block's estimate ids on the caller side (same as the front
// concatenates lines across activeEstimateIds today).

// EstimateSummary is the response shape for GetEstimateSummary.
type EstimateSummary struct {
	EstimateID    int64   `json:"estimate_id"`
	Labor         float64 `json:"labor"`
	Machines      float64 `json:"machines"`
	Materials     float64 `json:"materials"`
	Grand         float64 `json:"grand"`
	WorkCount     int     `json:"work_count"`
	FilledCount   int     `json:"filled_count"`
	ResourceCount int     `json:"resource_count"`
}

// GetEstimateSummary returns the headline aggregates for one estimate.
//
// Route: GET /construction/estimates/:id/summary
func (h *Handler) GetEstimateSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	estimateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid estimate ID")
		return
	}

	out := EstimateSummary{EstimateID: estimateID}
	err = h.db.QueryRow(`
		WITH l AS (
			SELECT
				id,
				COALESCE(parent_line_id, 0)            AS parent_line_id,
				LOWER(TRIM(COALESCE(resource_type, ''))) AS rt,
				COALESCE(norm_rate, 0)                 AS norm_rate,
				COALESCE(unit_rate, 0)                 AS unit_rate,
				COALESCE(quantity, 0)                  AS quantity,
				COALESCE(total_amount, 0)              AS total_amount,
				LOWER(COALESCE(name, ''))              AS lname,
				LOWER(COALESCE(uom, ''))               AS luom
			FROM construction_estimate_line
			WHERE estimate_id = $1 AND tenant_id = $2
		),
		f AS (
			SELECT *,
				(rt = '' AND norm_rate = 0) AS is_substage,
				(parent_line_id = 0)        AS is_top
			FROM l
		),
		-- resource rows = non-sub-stage children, with positive cost folded in
		res AS (
			SELECT parent_line_id, rt,
			       CASE WHEN unit_rate * quantity > 0 THEN unit_rate * quantity ELSE 0 END AS c
			FROM f
			WHERE NOT is_substage AND parent_line_id <> 0
		),
		buckets AS (
			SELECT
				COALESCE(SUM(CASE WHEN rt IN ('labor','ish','ishchi','worker') THEN c ELSE 0 END), 0) AS labor,
				COALESCE(SUM(CASE WHEN rt IN ('equipment','machine','mashina','masina') THEN c ELSE 0 END), 0) AS machines,
				COALESCE(SUM(CASE WHEN rt NOT IN ('labor','ish','ishchi','worker','equipment','machine','mashina','masina') THEN c ELSE 0 END), 0) AS materials
			FROM res
		),
		-- direct resource sum + count per container (any parent of a resource)
		child_res AS (
			SELECT parent_line_id AS container_id, SUM(c) AS res_sum, COUNT(*) AS res_count
			FROM res
			GROUP BY parent_line_id
		),
		-- effective amount of each sub-stage child
		substage_eff AS (
			SELECT f.parent_line_id AS parent_id,
			       CASE WHEN COALESCE(cr.res_count, 0) > 0 THEN COALESCE(cr.res_sum, 0) ELSE f.total_amount END AS eff
			FROM f
			LEFT JOIN child_res cr ON cr.container_id = f.id
			WHERE f.is_substage AND f.parent_line_id <> 0
		),
		substage_per_parent AS (
			SELECT parent_id, SUM(eff) AS ss_sum FROM substage_eff GROUP BY parent_id
		),
		toplevel AS (
			SELECT f.id, f.total_amount, f.quantity,
			       COALESCE(cr.res_sum, 0)   AS direct_res_sum,
			       COALESCE(cr.res_count, 0) AS direct_res_count,
			       COALESCE(sp.ss_sum, 0)    AS ss_sum
			FROM f
			LEFT JOIN child_res cr ON cr.container_id = f.id
			LEFT JOIN substage_per_parent sp ON sp.parent_id = f.id
			WHERE f.is_top
		)
		SELECT
			(SELECT labor FROM buckets),
			(SELECT machines FROM buckets),
			(SELECT materials FROM buckets),
			COALESCE((SELECT SUM((CASE WHEN direct_res_count > 0 THEN direct_res_sum ELSE total_amount END) + ss_sum) FROM toplevel), 0) AS grand,
			(SELECT COUNT(*) FROM f WHERE is_top) AS work_count,
			(SELECT COUNT(*) FROM f WHERE is_top AND quantity > 0) AS filled_count,
			(SELECT COUNT(DISTINCT (lname || '::' || luom)) FROM f WHERE rt <> '') AS resource_count
	`, estimateID, tenantID).Scan(
		&out.Labor, &out.Machines, &out.Materials, &out.Grand,
		&out.WorkCount, &out.FilledCount, &out.ResourceCount,
	)
	if err != nil {
		h.log.Error("Failed to compute estimate summary", "error", err, "estimate_id", estimateID)
		response.InternalError(c, "Failed to compute estimate summary")
		return
	}

	response.Success(c, out)
}
