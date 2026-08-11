package handler

import (
	"math"
	"strconv"
	"strings"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION — server-side calculations for mobile
// =====================================================
//
// The web client computes two things in the browser that the mobile app
// cannot replicate (it paginates lines and never holds the whole estimate):
//
//   1. Bosqichlar (Stages) progress roll-ups — cost-weighted % per
//      section / per work / overall. Mirrors StagesTabV2.jsx
//      progressFromWorks()/stageProgress()/blockProgress().
//   2. Forma 2 summary — transport+storage overhead, "прочие", VAT and the
//      grand total. Mirrors Form2Preview.jsx buildSummary().
//
// Both are faithful ports of the front-end math so the numbers match.

// roundMoney rounds to 2 decimals (kopeks).
func roundMoney(v float64) float64 { return math.Round(v*100) / 100 }

// ---------------------------------------------------------------------------
// 1) STAGES PROGRESS
// ---------------------------------------------------------------------------

type stageProgressWork struct {
	ID           int64   `json:"id"`
	ItemNumber   string  `json:"item_number"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	UOM          string  `json:"uom"`
	Quantity     float64 `json:"quantity"` // resolved plan qty (imported/original/quantity)
	DoneQuantity float64 `json:"done_quantity"`
	TotalAmount  float64 `json:"total_amount"`
	ProgressPct  float64 `json:"progress_pct"`
}

type stageProgressSection struct {
	Section     string              `json:"section"`
	WorksCount  int                 `json:"works_count"`
	PlanCost    float64             `json:"plan_cost"`
	DoneCost    float64             `json:"done_cost"`
	ProgressPct float64             `json:"progress_pct"`
	Works       []stageProgressWork `json:"works"`
}

type stagesProgressResponse struct {
	EstimateID  int64                  `json:"estimate_id"`
	ProgressPct float64                `json:"progress_pct"`
	PlanCost    float64                `json:"plan_cost"`
	DoneCost    float64                `json:"done_cost"`
	WorksCount  int                    `json:"works_count"`
	Sections    []stageProgressSection `json:"sections"`
}

// GetEstimateStagesProgress returns the Bosqichlar progress tree for one
// estimate: per-section and overall cost-weighted progress, plus each work's
// own ratio. Mirrors StagesTabV2 progressFromWorks (cost-weighted when costs
// exist, else the average of per-work ratios).
//
// Route: GET /construction/estimates/:id/stages-progress
func (h *Handler) GetEstimateStagesProgress(c *gin.Context) {
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

	// Top-level WORK rows only (resource_type empty AND no parent). The plan
	// quantity resolves the same way as the project overview: imported →
	// original → quantity, which covers ВОР template mode where `quantity`
	// is 0 but the real plan lives in imported/original_quantity.
	rows, err := h.db.Query(`
		SELECT
			id,
			COALESCE(item_number, ''),
			COALESCE(code, ''),
			COALESCE(name, ''),
			COALESCE(uom, ''),
			CASE
				WHEN COALESCE(imported_quantity, 0) > 0 THEN imported_quantity
				WHEN COALESCE(original_quantity, 0) > 0 THEN original_quantity
				ELSE COALESCE(quantity, 0)
			END AS q,
			COALESCE(done_quantity, 0),
			COALESCE(total_amount, 0),
			COALESCE(NULLIF(TRIM(COALESCE(parent_item_number, '')), ''), '—') AS section
		FROM construction_estimate_line
		WHERE estimate_id = $1 AND tenant_id = $2
		  AND COALESCE(resource_type, '') = ''
		  AND COALESCE(parent_line_id, 0) = 0
		ORDER BY section, item_number, id
	`, estimateID, tenantID)
	if err != nil {
		h.log.Error("stages-progress: query failed", "error", err, "estimate_id", estimateID)
		response.InternalError(c, "Failed to compute stages progress")
		return
	}
	defer rows.Close()

	// Group by section, preserving first-seen order.
	order := []string{}
	bySection := map[string]*stageProgressSection{}
	// running accumulators for cost-weighted + avg-ratio fallback, per section
	type acc struct{ ratioSum float64 }
	secAcc := map[string]*acc{}

	for rows.Next() {
		var w stageProgressWork
		var section string
		if err := rows.Scan(&w.ID, &w.ItemNumber, &w.Code, &w.Name, &w.UOM,
			&w.Quantity, &w.DoneQuantity, &w.TotalAmount, &section); err != nil {
			continue
		}
		ratio := 0.0
		if w.Quantity > 0 {
			ratio = w.DoneQuantity / w.Quantity
			if ratio > 1 {
				ratio = 1
			}
		}
		w.ProgressPct = roundMoney(ratio * 100)

		sec := bySection[section]
		if sec == nil {
			sec = &stageProgressSection{Section: section, Works: []stageProgressWork{}}
			bySection[section] = sec
			secAcc[section] = &acc{}
			order = append(order, section)
		}
		sec.Works = append(sec.Works, w)
		sec.WorksCount++
		sec.PlanCost += w.TotalAmount
		sec.DoneCost += w.TotalAmount * ratio
		secAcc[section].ratioSum += ratio
	}

	out := stagesProgressResponse{EstimateID: estimateID, Sections: []stageProgressSection{}}
	var totalRatioSum float64
	for _, section := range order {
		sec := bySection[section]
		// Cost-weighted when costs exist; otherwise the average of per-work
		// ratios (same fallback as the front-end progressFromWorks).
		if sec.PlanCost > 0 {
			sec.ProgressPct = roundMoney(sec.DoneCost / sec.PlanCost * 100)
		} else if sec.WorksCount > 0 {
			sec.ProgressPct = roundMoney(secAcc[section].ratioSum / float64(sec.WorksCount) * 100)
		}
		sec.PlanCost = roundMoney(sec.PlanCost)
		sec.DoneCost = roundMoney(sec.DoneCost)

		out.Sections = append(out.Sections, *sec)
		out.PlanCost += bySection[section].PlanCost // already rounded; fine for total
		out.DoneCost += bySection[section].DoneCost
		out.WorksCount += sec.WorksCount
		totalRatioSum += secAcc[section].ratioSum
	}
	if out.PlanCost > 0 {
		out.ProgressPct = roundMoney(out.DoneCost / out.PlanCost * 100)
	} else if out.WorksCount > 0 {
		out.ProgressPct = roundMoney(totalRatioSum / float64(out.WorksCount) * 100)
	}
	out.PlanCost = roundMoney(out.PlanCost)
	out.DoneCost = roundMoney(out.DoneCost)

	response.Success(c, out)
}

// ---------------------------------------------------------------------------
// 2) FORMA 2 SUMMARY
// ---------------------------------------------------------------------------

// Combined transport+storage overhead rates (Госкомархитектстрой letter
// №352/12-05, 2011) — the same constants Form2Preview uses for the summary
// roll-up. Only the three regulated material buckets the project uses.
const (
	f2OverheadStandard  = 7.0
	f2OverheadEquipment = 3.2
	f2OverheadCable     = 3.5
	f2VatPct            = 12.0
)

type forma2Summary struct {
	EstimateID int64 `json:"estimate_id"`
	// direct costs
	Labor        float64 `json:"labor"`
	Machines     float64 `json:"machines"`
	MatStandard  float64 `json:"mat_standard"`
	MatEquipment float64 `json:"mat_equipment"`
	MatCable     float64 `json:"mat_cable"`
	MatTotal     float64 `json:"mat_total"`
	Direct       float64 `json:"direct"`
	// transport+storage overhead per bucket
	OverheadStandard  float64 `json:"overhead_standard"`
	OverheadEquipment float64 `json:"overhead_equipment"`
	OverheadCable     float64 `json:"overhead_cable"`
	OverheadTotal     float64 `json:"overhead_total"`
	// прочие / construction / equipment / totals
	OtherPct          float64 `json:"other_pct"`
	OtherBase         float64 `json:"other_base"`
	Other             float64 `json:"other"`
	ConstructionTotal float64 `json:"construction_total"`
	EquipmentTotal    float64 `json:"equipment_total"`
	Subtotal          float64 `json:"subtotal"`
	UseVat            bool    `json:"use_vat"`
	VatPct            float64 `json:"vat_pct"`
	Vat               float64 `json:"vat"`
	Grand             float64 `json:"grand"`
}

// GetEstimateForma2Summary returns the Forma 2 (КС-2/ВОР) money roll-up for
// one estimate — direct costs, transport+storage overhead, прочие, VAT and the
// grand total. Mirrors Form2Preview.jsx buildSummary().
//
// Query params:
//
//	other_pct — "прочие" percentage (default 0)
//	vat       — "true" to apply 12% VAT (default false)
//
// Route: GET /construction/estimates/:id/forma2-summary
func (h *Handler) GetEstimateForma2Summary(c *gin.Context) {
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

	otherPct, _ := strconv.ParseFloat(strings.TrimSpace(c.Query("other_pct")), 64)
	if otherPct < 0 {
		otherPct = 0
	}
	useVat := strings.EqualFold(strings.TrimSpace(c.Query("vat")), "true")

	// Direct-cost buckets over resource sub-lines (skip sub-stage markers).
	// labor/machines classify by resource_type; material lines bucket by
	// material_type (equipment/cable/standard). Cost = unit_rate × quantity,
	// counted only when positive — identical to buildSummary().
	out := forma2Summary{
		EstimateID: estimateID,
		OtherPct:   otherPct,
		UseVat:     useVat,
		VatPct:     f2VatPct,
	}
	err = h.db.QueryRow(`
		WITH res AS (
			SELECT
				LOWER(TRIM(COALESCE(resource_type, ''))) AS rt,
				LOWER(COALESCE(NULLIF(TRIM(COALESCE(material_type, '')), ''), 'standard')) AS mt,
				CASE WHEN COALESCE(unit_rate,0) * COALESCE(quantity,0) > 0
				     THEN COALESCE(unit_rate,0) * COALESCE(quantity,0) ELSE 0 END AS c,
				COALESCE(norm_rate, 0) AS norm_rate
			FROM construction_estimate_line
			WHERE estimate_id = $1 AND tenant_id = $2
			  AND COALESCE(parent_line_id, 0) <> 0
			  AND NOT (COALESCE(resource_type,'') = '' AND COALESCE(norm_rate,0) = 0)
		)
		SELECT
			COALESCE(SUM(c) FILTER (WHERE rt IN ('labor','ish','ishchi','worker')), 0),
			COALESCE(SUM(c) FILTER (WHERE rt IN ('equipment','machine','mashina','masina')), 0),
			COALESCE(SUM(c) FILTER (WHERE rt NOT IN ('labor','ish','ishchi','worker','equipment','machine','mashina','masina') AND mt NOT IN ('equipment','cable')), 0),
			COALESCE(SUM(c) FILTER (WHERE rt NOT IN ('labor','ish','ishchi','worker','equipment','machine','mashina','masina') AND mt = 'equipment'), 0),
			COALESCE(SUM(c) FILTER (WHERE rt NOT IN ('labor','ish','ishchi','worker','equipment','machine','mashina','masina') AND mt = 'cable'), 0)
		FROM res
	`, estimateID, tenantID).Scan(&out.Labor, &out.Machines, &out.MatStandard, &out.MatEquipment, &out.MatCable)
	if err != nil {
		h.log.Error("forma2-summary: query failed", "error", err, "estimate_id", estimateID)
		response.InternalError(c, "Failed to compute Forma 2 summary")
		return
	}

	out.MatTotal = out.MatStandard + out.MatEquipment + out.MatCable
	out.Direct = out.Labor + out.Machines + out.MatTotal

	out.OverheadStandard = out.MatStandard * f2OverheadStandard / 100
	out.OverheadEquipment = out.MatEquipment * f2OverheadEquipment / 100
	out.OverheadCable = out.MatCable * f2OverheadCable / 100
	out.OverheadTotal = out.OverheadStandard + out.OverheadEquipment + out.OverheadCable

	// "прочие" base = everything that belongs to construction (excludes the
	// equipment slice — material equipment + its overhead — which is a
	// separate line and never gets the прочие multiplier).
	constructionDirect := out.Direct - out.MatEquipment
	constructionCombined := out.OverheadTotal - out.OverheadEquipment
	out.OtherBase = constructionDirect + constructionCombined
	out.Other = out.OtherBase * otherPct / 100
	out.ConstructionTotal = out.OtherBase + out.Other

	out.EquipmentTotal = out.MatEquipment + out.OverheadEquipment

	out.Subtotal = out.ConstructionTotal + out.EquipmentTotal
	if useVat {
		out.Vat = out.Subtotal * f2VatPct / 100
	}
	out.Grand = out.Subtotal + out.Vat

	// round money fields for stable display
	out.Labor = roundMoney(out.Labor)
	out.Machines = roundMoney(out.Machines)
	out.MatStandard = roundMoney(out.MatStandard)
	out.MatEquipment = roundMoney(out.MatEquipment)
	out.MatCable = roundMoney(out.MatCable)
	out.MatTotal = roundMoney(out.MatTotal)
	out.Direct = roundMoney(out.Direct)
	out.OverheadStandard = roundMoney(out.OverheadStandard)
	out.OverheadEquipment = roundMoney(out.OverheadEquipment)
	out.OverheadCable = roundMoney(out.OverheadCable)
	out.OverheadTotal = roundMoney(out.OverheadTotal)
	out.OtherBase = roundMoney(out.OtherBase)
	out.Other = roundMoney(out.Other)
	out.ConstructionTotal = roundMoney(out.ConstructionTotal)
	out.EquipmentTotal = roundMoney(out.EquipmentTotal)
	out.Subtotal = roundMoney(out.Subtotal)
	out.Vat = roundMoney(out.Vat)
	out.Grand = roundMoney(out.Grand)

	response.Success(c, out)
}
