package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// construction_resource_prices.go — implements the Resurslar tab from the
// Form2_Works_v2 mockup against real estimate lines.
//
// The mockup keeps a global `currentPrices[rid]` map and lets the user edit
// it. Our schema doesn't have a separate resource catalog: every estimate
// line carries its own unit_rate. So the "edit a resource price" action
// here is actually a BULK UPDATE that propagates the new price to every
// matching estimate_line.unit_rate inside the project.
//
// A "resource" key is (project_id, lower(name), uom, resource_type). The
// list endpoint groups by that key and returns the current effective
// price (avg of rates across matching lines, in practice all the same
// number because the bulk-update keeps them aligned).
//
// Routes registered in handler.go:
//   GET  /construction/projects/:id/resource-prices
//   POST /construction/projects/:id/resource-prices/bulk-update
//   POST /construction/projects/:id/resource-prices/material-type
//   GET  /construction/projects/:id/resource-prices/history?name=...&uom=...

// ListResourcePrices returns one row per (name, uom, resource_type) with
// the current price + history-row count, so the Resurslar UI can render
// a "Tarix" badge next to each resource showing how many times its price
// has been changed.
func (h *Handler) ListResourcePrices(c *gin.Context) {
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
	resType := strings.ToLower(strings.TrimSpace(c.Query("type")))
	// Optional `?estimate_id=…` scopes the catalog to one estimate so the
	// Resurslar tab matches the estimate selected at the top of the page.
	// When absent the response stays project-wide (every estimate's lines).
	estimateIDStr := strings.TrimSpace(c.Query("estimate_id"))

	// Build the resource catalog from estimate_lines. Tenant scoping comes
	// through `construction_estimate.tenant_id`. We pull the average
	// unit_rate per group as a defensive measure; in practice the
	// bulk-update endpoint keeps every line at the same value.
	q := `
		SELECT
		  el.name,
		  COALESCE(el.uom, ''),
		  COALESCE(NULLIF(el.resource_type, ''), 'material') AS rtype,
		  COALESCE(MIN(el.material_type), 'standard') AS mat_type,
		  COUNT(*) AS line_count,
		  AVG(el.unit_rate) AS avg_price,
		  MIN(el.unit_rate) AS min_price,
		  MAX(el.unit_rate) AS max_price,
		  AVG(COALESCE(el.original_unit_rate, el.unit_rate)) AS orig_price
		FROM construction_estimate_line el
		JOIN construction_estimate e ON e.id = el.estimate_id
		WHERE e.project_id = $1 AND e.tenant_id = $2
		  AND COALESCE(el.name, '') <> ''
		  -- A row counts as a "resource" if either:
		  --   (a) it's a sub-line of a BOP/Единич work (parent_line_id > 0), OR
		  --   (b) it's a top-level row in a Ресурс-type estimate where the
		  --       lines themselves ARE the resources (no parent nesting).
		  -- Both shapes carry a real resource_type (labor/equipment/material)
		  -- so we use that as the unifying filter — sub-stages we created
		  -- with empty resource_type get correctly excluded.
		  AND COALESCE(el.resource_type, '') <> ''
	`
	args := []interface{}{projectID, tenantID}
	argIdx := 3
	if resType != "" {
		q += fmt.Sprintf(" AND LOWER(el.resource_type) = $%d", argIdx)
		args = append(args, resType)
		argIdx++
	}
	if estimateIDStr != "" {
		if estID, err := strconv.ParseInt(estimateIDStr, 10, 64); err == nil && estID > 0 {
			q += fmt.Sprintf(" AND el.estimate_id = $%d", argIdx)
			args = append(args, estID)
			argIdx++
		}
	}
	q += `
		GROUP BY el.name, el.uom, rtype
		ORDER BY rtype ASC, el.name ASC
	`

	rows, err := h.db.Query(q, args...)
	if err != nil {
		h.log.Error("Failed to list resource prices", "error", err)
		response.InternalError(c, "Failed to list resource prices")
		return
	}
	defer rows.Close()

	type resourceRow struct {
		Name          string  `json:"name"`
		UOM           string  `json:"uom"`
		ResourceType  string  `json:"resource_type"`
		MaterialType  string  `json:"material_type"`
		LineCount     int64   `json:"line_count"`
		Price         float64 `json:"price"`
		MinPrice      float64 `json:"min_price"`
		MaxPrice      float64 `json:"max_price"`
		OriginalPrice float64 `json:"original_price"`
		HistoryCount  int64   `json:"history_count"`
	}

	out := []resourceRow{}
	for rows.Next() {
		var r resourceRow
		var avg, minP, maxP, origP sql.NullFloat64
		if err := rows.Scan(&r.Name, &r.UOM, &r.ResourceType, &r.MaterialType, &r.LineCount, &avg, &minP, &maxP, &origP); err != nil {
			continue
		}
		if avg.Valid {
			r.Price = avg.Float64
		}
		if minP.Valid {
			r.MinPrice = minP.Float64
		}
		if maxP.Valid {
			r.MaxPrice = maxP.Float64
		}
		if origP.Valid {
			r.OriginalPrice = origP.Float64
		}
		out = append(out, r)
	}

	// Annotate with history counts in one extra round-trip — keeps the
	// per-resource lookup at O(1) and avoids N+1 queries from the UI.
	if len(out) > 0 {
		histCounts := map[string]int64{}
		histRows, err := h.db.Query(`
			SELECT LOWER(resource_name) || '|' || COALESCE(uom, ''), COUNT(*)
			FROM construction_resource_price_history
			WHERE tenant_id = $1 AND project_id = $2
			GROUP BY LOWER(resource_name) || '|' || COALESCE(uom, '')
		`, tenantID, projectID)
		if err == nil {
			defer histRows.Close()
			for histRows.Next() {
				var k string
				var n int64
				if err := histRows.Scan(&k, &n); err == nil {
					histCounts[k] = n
				}
			}
		}
		for i, r := range out {
			k := strings.ToLower(r.Name) + "|" + r.UOM
			out[i].HistoryCount = histCounts[k]
		}
	}

	response.Success(c, out)
}

// BulkUpdateResourcePrice — body: { resource_name, uom, resource_type,
// new_price, note? }. Updates unit_rate on every matching
// construction_estimate_line and writes one history row.
func (h *Handler) BulkUpdateResourcePrice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var in struct {
		ResourceName string  `json:"resource_name" binding:"required"`
		UOM          string  `json:"uom"`
		ResourceType string  `json:"resource_type"`
		NewPrice     float64 `json:"new_price" binding:"required"`
		Note         string  `json:"note"`
		// EstimateID — when > 0, scope the price update to lines of THIS
		// one estimate (block) only. Without it the change is project-wide
		// (legacy behaviour). The Smeta boshqaruvi → Resurslar tab now
		// always sets it so editing Block 1's cement price doesn't bleed
		// into Block 2's lines.
		EstimateID int64 `json:"estimate_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if in.NewPrice < 0 {
		response.BadRequest(c, "new_price must be >= 0")
		return
	}
	in.ResourceName = strings.TrimSpace(in.ResourceName)
	if in.ResourceName == "" {
		response.BadRequest(c, "resource_name is required")
		return
	}

	// Read current avg price for the history row's `old_price` field.
	// Mirrors the same estimate_id scoping as the UPDATE so the recorded
	// "old_price" matches what the user actually saw before editing.
	var oldPrice sql.NullFloat64
	if in.EstimateID > 0 {
		_ = h.db.QueryRow(`
			SELECT AVG(el.unit_rate)
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND el.estimate_id = $3
			  AND LOWER(el.name) = LOWER($4)
			  AND COALESCE(el.uom, '') = COALESCE($5, '')
		`, projectID, tenantID, in.EstimateID, in.ResourceName, in.UOM).Scan(&oldPrice)
	} else {
		_ = h.db.QueryRow(`
			SELECT AVG(el.unit_rate)
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND LOWER(el.name) = LOWER($3)
			  AND COALESCE(el.uom, '') = COALESCE($4, '')
		`, projectID, tenantID, in.ResourceName, in.UOM).Scan(&oldPrice)
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Bulk update unit_rate + recompute total_amount = unit_rate × quantity
	// across every estimate-line in this project that matches the resource.
	// When estimate_id is set we add `el.estimate_id = $N` so the UPDATE
	// touches one block only.
	var (
		res sql.Result
	)
	if in.EstimateID > 0 {
		res, err = tx.Exec(`
			UPDATE construction_estimate_line
			SET unit_rate    = $1,
			    total_amount = $1 * COALESCE(quantity, 0),
			    updated_date = NOW()
			WHERE id IN (
			    SELECT el.id
			    FROM construction_estimate_line el
			    JOIN construction_estimate e ON e.id = el.estimate_id
			    WHERE e.project_id = $2 AND e.tenant_id = $3
			      AND el.estimate_id = $4
			      AND LOWER(el.name) = LOWER($5)
			      AND COALESCE(el.uom, '') = COALESCE($6, '')
			)
		`, in.NewPrice, projectID, tenantID, in.EstimateID, in.ResourceName, in.UOM)
	} else {
		res, err = tx.Exec(`
			UPDATE construction_estimate_line
			SET unit_rate    = $1,
			    total_amount = $1 * COALESCE(quantity, 0),
			    updated_date = NOW()
			WHERE id IN (
			    SELECT el.id
			    FROM construction_estimate_line el
			    JOIN construction_estimate e ON e.id = el.estimate_id
			    WHERE e.project_id = $2 AND e.tenant_id = $3
			      AND LOWER(el.name) = LOWER($4)
			      AND COALESCE(el.uom, '') = COALESCE($5, '')
			)
		`, in.NewPrice, projectID, tenantID, in.ResourceName, in.UOM)
	}
	if err != nil {
		h.log.Error("Failed to bulk-update resource price", "error", err)
		response.InternalError(c, "Failed to update price")
		return
	}
	updated, _ := res.RowsAffected()

	// Write history row (one per bulk-update event, not one per line).
	var changedByVal interface{}
	if userID != uuid.Nil {
		changedByVal = userID
	}
	_, _ = tx.Exec(`
		INSERT INTO construction_resource_price_history
		    (tenant_id, project_id, resource_name, uom, resource_type, old_price, new_price, changed_by, changed_at, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NULLIF($9, ''))
	`, tenantID, projectID, in.ResourceName, in.UOM, strings.ToLower(in.ResourceType),
		oldPrice.Float64, in.NewPrice, changedByVal, in.Note)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit price update", "error", err)
		response.InternalError(c, "Failed to update price")
		return
	}

	// Recalc the parent estimates' totals. When the price update was
	// scoped to one estimate, only that estimate needs its headline
	// numbers refreshed; otherwise sweep every estimate that has a line
	// matching this resource.
	if in.EstimateID > 0 {
		h.recalculateEstimateTotals(in.EstimateID)
	} else {
		estRows, _ := h.db.Query(`
			SELECT DISTINCT e.id
			FROM construction_estimate e
			JOIN construction_estimate_line el ON el.estimate_id = e.id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND LOWER(el.name) = LOWER($3)
			  AND COALESCE(el.uom, '') = COALESCE($4, '')
		`, projectID, tenantID, in.ResourceName, in.UOM)
		if estRows != nil {
			for estRows.Next() {
				var eid int64
				if err := estRows.Scan(&eid); err == nil {
					h.recalculateEstimateTotals(eid)
				}
			}
			estRows.Close()
		}
	}

	userNameLog := c.GetString("user_name")
	h.logSmetaAudit(tenantID, projectID, nil, "price_change", in.ResourceName, nil,
		strconv.FormatFloat(oldPrice.Float64, 'f', -1, 64),
		strconv.FormatFloat(in.NewPrice, 'f', -1, 64),
		"Resurs narxi yangilandi", userID, userNameLog)

	response.Success(c, gin.H{
		"resource_name":   in.ResourceName,
		"uom":             in.UOM,
		"old_price":       oldPrice.Float64,
		"new_price":       in.NewPrice,
		"lines_updated":   updated,
	})
}

// BulkUpdateResourceMaterialType — body: { resource_name, uom, material_type }.
// Sets material_type on every matching line so the Form 2 overhead engine
// applies the correct transport+storage rates.
func (h *Handler) BulkUpdateResourceMaterialType(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var in struct {
		ResourceName string `json:"resource_name" binding:"required"`
		UOM          string `json:"uom"`
		MaterialType string `json:"material_type" binding:"required"`
		// EstimateID — when > 0, scope the material_type change to one
		// estimate (block) only. Same per-block scoping rule as
		// BulkUpdateResourcePrice — see that handler for context.
		EstimateID int64 `json:"estimate_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	mt := strings.ToLower(strings.TrimSpace(in.MaterialType))
	switch mt {
	case "standard", "equipment", "cable", "metal", "import":
	default:
		response.BadRequest(c, "material_type must be one of: standard, equipment, cable, metal, import")
		return
	}

	// Capture an old material_type for the audit row before we mutate.
	var oldMt string
	if in.EstimateID > 0 {
		_ = h.db.QueryRow(`
			SELECT COALESCE(MIN(material_type), 'standard')
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND el.estimate_id = $3
			  AND LOWER(el.name) = LOWER($4)
			  AND COALESCE(el.uom, '') = COALESCE($5, '')
		`, projectID, tenantID, in.EstimateID, in.ResourceName, in.UOM).Scan(&oldMt)
	} else {
		_ = h.db.QueryRow(`
			SELECT COALESCE(MIN(material_type), 'standard')
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND LOWER(el.name) = LOWER($3)
			  AND COALESCE(el.uom, '') = COALESCE($4, '')
		`, projectID, tenantID, in.ResourceName, in.UOM).Scan(&oldMt)
	}

	var res sql.Result
	if in.EstimateID > 0 {
		res, err = h.db.Exec(`
			UPDATE construction_estimate_line
			SET material_type = $1, updated_date = NOW()
			WHERE id IN (
			    SELECT el.id
			    FROM construction_estimate_line el
			    JOIN construction_estimate e ON e.id = el.estimate_id
			    WHERE e.project_id = $2 AND e.tenant_id = $3
			      AND el.estimate_id = $4
			      AND LOWER(el.name) = LOWER($5)
			      AND COALESCE(el.uom, '') = COALESCE($6, '')
			)
		`, mt, projectID, tenantID, in.EstimateID, in.ResourceName, in.UOM)
	} else {
		res, err = h.db.Exec(`
			UPDATE construction_estimate_line
			SET material_type = $1, updated_date = NOW()
			WHERE id IN (
			    SELECT el.id
			    FROM construction_estimate_line el
			    JOIN construction_estimate e ON e.id = el.estimate_id
			    WHERE e.project_id = $2 AND e.tenant_id = $3
			      AND LOWER(el.name) = LOWER($4)
			      AND COALESCE(el.uom, '') = COALESCE($5, '')
			)
		`, mt, projectID, tenantID, in.ResourceName, in.UOM)
	}
	if err != nil {
		h.log.Error("Failed to bulk-update material_type", "error", err)
		response.InternalError(c, "Failed to update material type")
		return
	}
	n, _ := res.RowsAffected()

	userNameLog := c.GetString("user_name")
	h.logSmetaAudit(tenantID, projectID, nil, "mat_type", in.ResourceName, nil,
		oldMt, mt, "Material turi o'zgartirildi", userID, userNameLog)

	response.Success(c, gin.H{"lines_updated": n, "material_type": mt})
}

// ResetResourcePrice — body: { resource_name, uom }.
// Reverts unit_rate back to the per-line original_unit_rate anchor (set at
// migration 349) for every matching line in the project. One history row is
// written using the avg current price as old_price and the avg
// original_unit_rate as new_price, so the Tarix UI can show the rollback.
func (h *Handler) ResetResourcePrice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var in struct {
		ResourceName string `json:"resource_name" binding:"required"`
		UOM          string `json:"uom"`
		// EstimateID — when > 0, scope the rollback to one estimate only.
		EstimateID int64 `json:"estimate_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	in.ResourceName = strings.TrimSpace(in.ResourceName)
	if in.ResourceName == "" {
		response.BadRequest(c, "resource_name is required")
		return
	}

	// Capture old + new (original) avg before the bulk-update for the history row.
	var oldPrice, newPrice sql.NullFloat64
	if in.EstimateID > 0 {
		_ = h.db.QueryRow(`
			SELECT AVG(el.unit_rate), AVG(COALESCE(el.original_unit_rate, el.unit_rate))
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND el.estimate_id = $3
			  AND LOWER(el.name) = LOWER($4)
			  AND COALESCE(el.uom, '') = COALESCE($5, '')
		`, projectID, tenantID, in.EstimateID, in.ResourceName, in.UOM).Scan(&oldPrice, &newPrice)
	} else {
		_ = h.db.QueryRow(`
			SELECT AVG(el.unit_rate), AVG(COALESCE(el.original_unit_rate, el.unit_rate))
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND LOWER(el.name) = LOWER($3)
			  AND COALESCE(el.uom, '') = COALESCE($4, '')
		`, projectID, tenantID, in.ResourceName, in.UOM).Scan(&oldPrice, &newPrice)
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	var res sql.Result
	if in.EstimateID > 0 {
		res, err = tx.Exec(`
			UPDATE construction_estimate_line
			SET unit_rate    = COALESCE(original_unit_rate, unit_rate),
			    total_amount = COALESCE(original_unit_rate, unit_rate) * COALESCE(quantity, 0),
			    updated_date = NOW()
			WHERE id IN (
			    SELECT el.id
			    FROM construction_estimate_line el
			    JOIN construction_estimate e ON e.id = el.estimate_id
			    WHERE e.project_id = $1 AND e.tenant_id = $2
			      AND el.estimate_id = $3
			      AND LOWER(el.name) = LOWER($4)
			      AND COALESCE(el.uom, '') = COALESCE($5, '')
			)
		`, projectID, tenantID, in.EstimateID, in.ResourceName, in.UOM)
	} else {
		res, err = tx.Exec(`
			UPDATE construction_estimate_line
			SET unit_rate    = COALESCE(original_unit_rate, unit_rate),
			    total_amount = COALESCE(original_unit_rate, unit_rate) * COALESCE(quantity, 0),
			    updated_date = NOW()
			WHERE id IN (
			    SELECT el.id
			    FROM construction_estimate_line el
			    JOIN construction_estimate e ON e.id = el.estimate_id
			    WHERE e.project_id = $1 AND e.tenant_id = $2
			      AND LOWER(el.name) = LOWER($3)
			      AND COALESCE(el.uom, '') = COALESCE($4, '')
			)
		`, projectID, tenantID, in.ResourceName, in.UOM)
	}
	if err != nil {
		h.log.Error("Failed to reset resource price", "error", err)
		response.InternalError(c, "Failed to reset price")
		return
	}
	updated, _ := res.RowsAffected()

	var changedByVal interface{}
	if userID != uuid.Nil {
		changedByVal = userID
	}
	_, _ = tx.Exec(`
		INSERT INTO construction_resource_price_history
		    (tenant_id, project_id, resource_name, uom, resource_type, old_price, new_price, changed_by, changed_at, note)
		VALUES ($1, $2, $3, $4, '', $5, $6, $7, NOW(), 'Reset to original')
	`, tenantID, projectID, in.ResourceName, in.UOM, oldPrice.Float64, newPrice.Float64, changedByVal)

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit reset", "error", err)
		response.InternalError(c, "Failed to reset price")
		return
	}

	// Recalc estimate totals — scope to the touched estimate when the
	// rollback was per-block, else sweep every estimate that has a line
	// matching this resource (mirrors BulkUpdateResourcePrice).
	if in.EstimateID > 0 {
		h.recalculateEstimateTotals(in.EstimateID)
	} else {
		estRows, _ := h.db.Query(`
			SELECT DISTINCT e.id
			FROM construction_estimate e
			JOIN construction_estimate_line el ON el.estimate_id = e.id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND LOWER(el.name) = LOWER($3)
			  AND COALESCE(el.uom, '') = COALESCE($4, '')
		`, projectID, tenantID, in.ResourceName, in.UOM)
		if estRows != nil {
			for estRows.Next() {
				var eid int64
				if err := estRows.Scan(&eid); err == nil {
					h.recalculateEstimateTotals(eid)
				}
			}
			estRows.Close()
		}
	}

	userNameLog := c.GetString("user_name")
	h.logSmetaAudit(tenantID, projectID, nil, "reset_price", in.ResourceName, nil,
		strconv.FormatFloat(oldPrice.Float64, 'f', -1, 64),
		strconv.FormatFloat(newPrice.Float64, 'f', -1, 64),
		"Narx asliga qaytarildi", userID, userNameLog)

	response.Success(c, gin.H{
		"resource_name": in.ResourceName,
		"uom":           in.UOM,
		"old_price":     oldPrice.Float64,
		"new_price":     newPrice.Float64,
		"lines_updated": updated,
	})
}

// ResetAllResourcePrices — project-wide rollback. Restores unit_rate to
// original_unit_rate for every estimate line in the project. Useful when the
// user has been editing prices and wants to wipe the slate. Writes ONE
// history row per (resource_name, uom) bucket so the Tarix log stays
// human-readable; we don't spam one row per line.
func (h *Handler) ResetAllResourcePrices(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	projectID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	// Optional `estimate_id` body field — when set, scope the rollback to
	// one estimate (block) only. The Resurslar tab passes it so "Reset
	// all" inside Block 1 doesn't wipe out modified prices in Block 2.
	var body struct {
		EstimateID int64 `json:"estimate_id"`
	}
	_ = c.ShouldBindJSON(&body)

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Snapshot per-resource averages BEFORE the reset for the history rows.
	type bucketSnap struct {
		Name   string
		UOM    string
		OldAvg float64
		NewAvg float64
	}
	var snaps []bucketSnap
	var snapRows *sql.Rows
	if body.EstimateID > 0 {
		snapRows, err = tx.Query(`
			SELECT el.name, COALESCE(el.uom, ''),
			       AVG(el.unit_rate), AVG(COALESCE(el.original_unit_rate, el.unit_rate))
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND el.estimate_id = $3
			  AND COALESCE(el.name, '') <> ''
			  AND COALESCE(el.resource_type, '') <> ''
			  AND ABS(COALESCE(el.unit_rate, 0) - COALESCE(el.original_unit_rate, el.unit_rate)) > 0.0001
			GROUP BY el.name, el.uom
		`, projectID, tenantID, body.EstimateID)
	} else {
		snapRows, err = tx.Query(`
			SELECT el.name, COALESCE(el.uom, ''),
			       AVG(el.unit_rate), AVG(COALESCE(el.original_unit_rate, el.unit_rate))
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id
			WHERE e.project_id = $1 AND e.tenant_id = $2
			  AND COALESCE(el.name, '') <> ''
			  -- Same resource-detection criterion as ListResourcePrices: a row
			  -- counts as a resource if it has a real resource_type, regardless
			  -- of whether it's a sub-line (BOP) or a flat top-level row (Ресурс).
			  AND COALESCE(el.resource_type, '') <> ''
			  AND ABS(COALESCE(el.unit_rate, 0) - COALESCE(el.original_unit_rate, el.unit_rate)) > 0.0001
			GROUP BY el.name, el.uom
		`, projectID, tenantID)
	}
	if err == nil && snapRows != nil {
		for snapRows.Next() {
			var s bucketSnap
			var oldAvg, newAvg sql.NullFloat64
			if err := snapRows.Scan(&s.Name, &s.UOM, &oldAvg, &newAvg); err == nil {
				if oldAvg.Valid {
					s.OldAvg = oldAvg.Float64
				}
				if newAvg.Valid {
					s.NewAvg = newAvg.Float64
				}
				snaps = append(snaps, s)
			}
		}
		snapRows.Close()
	}

	// Bulk reset — every line in the project, OR only the selected estimate.
	var res sql.Result
	if body.EstimateID > 0 {
		res, err = tx.Exec(`
			UPDATE construction_estimate_line
			SET unit_rate    = COALESCE(original_unit_rate, unit_rate),
			    total_amount = COALESCE(original_unit_rate, unit_rate) * COALESCE(quantity, 0),
			    updated_date = NOW()
			WHERE id IN (
			    SELECT el.id
			    FROM construction_estimate_line el
			    JOIN construction_estimate e ON e.id = el.estimate_id
			    WHERE e.project_id = $1 AND e.tenant_id = $2
			      AND el.estimate_id = $3
			      AND ABS(COALESCE(el.unit_rate, 0) - COALESCE(el.original_unit_rate, el.unit_rate)) > 0.0001
			)
		`, projectID, tenantID, body.EstimateID)
	} else {
		res, err = tx.Exec(`
			UPDATE construction_estimate_line
			SET unit_rate    = COALESCE(original_unit_rate, unit_rate),
			    total_amount = COALESCE(original_unit_rate, unit_rate) * COALESCE(quantity, 0),
			    updated_date = NOW()
			WHERE id IN (
			    SELECT el.id
			    FROM construction_estimate_line el
			    JOIN construction_estimate e ON e.id = el.estimate_id
			    WHERE e.project_id = $1 AND e.tenant_id = $2
			      AND ABS(COALESCE(el.unit_rate, 0) - COALESCE(el.original_unit_rate, el.unit_rate)) > 0.0001
			)
		`, projectID, tenantID)
	}
	if err != nil {
		h.log.Error("Failed to reset all resource prices", "error", err)
		response.InternalError(c, "Failed to reset prices")
		return
	}
	updated, _ := res.RowsAffected()

	var changedByVal interface{}
	if userID != uuid.Nil {
		changedByVal = userID
	}
	for _, s := range snaps {
		_, _ = tx.Exec(`
			INSERT INTO construction_resource_price_history
			    (tenant_id, project_id, resource_name, uom, resource_type, old_price, new_price, changed_by, changed_at, note)
			VALUES ($1, $2, $3, $4, '', $5, $6, $7, NOW(), 'Reset all (project-wide)')
		`, tenantID, projectID, s.Name, s.UOM, s.OldAvg, s.NewAvg, changedByVal)
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit reset all", "error", err)
		response.InternalError(c, "Failed to reset prices")
		return
	}

	// Recalc the affected estimates' totals — single estimate when scoped,
	// every estimate in the project otherwise.
	if body.EstimateID > 0 {
		h.recalculateEstimateTotals(body.EstimateID)
	} else {
		estRows, _ := h.db.Query(`
			SELECT id FROM construction_estimate WHERE project_id = $1 AND tenant_id = $2
		`, projectID, tenantID)
		if estRows != nil {
			for estRows.Next() {
				var eid int64
				if err := estRows.Scan(&eid); err == nil {
					h.recalculateEstimateTotals(eid)
				}
			}
			estRows.Close()
		}
	}

	userNameLog := c.GetString("user_name")
	h.logSmetaAudit(tenantID, projectID, nil, "reset_price_all", "", nil,
		"", strconv.FormatInt(updated, 10),
		"Barcha narxlar asliga qaytarildi", userID, userNameLog)

	response.Success(c, gin.H{
		"lines_updated":      updated,
		"resources_affected": len(snaps),
	})
}

// GetResourcePriceHistory returns the chronological log for one resource.
func (h *Handler) GetResourcePriceHistory(c *gin.Context) {
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
	name := strings.TrimSpace(c.Query("name"))
	uom := c.Query("uom")
	if name == "" {
		response.BadRequest(c, "name query param is required")
		return
	}

	rows, err := h.db.Query(`
		SELECT h.id, h.old_price, h.new_price, h.changed_at,
		       COALESCE(u.first_name || ' ' || u.last_name, ''), COALESCE(h.note, '')
		FROM construction_resource_price_history h
		LEFT JOIN users u ON u.id = h.changed_by
		WHERE h.tenant_id = $1
		  AND h.project_id = $2
		  AND LOWER(h.resource_name) = LOWER($3)
		  AND COALESCE(h.uom, '') = COALESCE($4, '')
		ORDER BY h.changed_at DESC
		LIMIT 200
	`, tenantID, projectID, name, uom)
	if err != nil {
		h.log.Error("Failed to load price history", "error", err)
		response.InternalError(c, "Failed to load history")
		return
	}
	defer rows.Close()

	type histRow struct {
		ID         int64     `json:"id"`
		OldPrice   float64   `json:"old_price"`
		NewPrice   float64   `json:"new_price"`
		ChangedAt  time.Time `json:"changed_at"`
		ChangedBy  string    `json:"changed_by_name"`
		Note       string    `json:"note,omitempty"`
	}
	out := []histRow{}
	for rows.Next() {
		var h histRow
		if err := rows.Scan(&h.ID, &h.OldPrice, &h.NewPrice, &h.ChangedAt, &h.ChangedBy, &h.Note); err != nil {
			continue
		}
		out = append(out, h)
	}
	response.Success(c, out)
}
