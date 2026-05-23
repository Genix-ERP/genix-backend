// construction_crm_link.go — handlers for the "connect Genix construction
// to Yuksalish CRM" feature.
//
// This file owns three concerns:
//
//   1. Proxy endpoints that fetch CRM projects/blocks so the link UI in
//      Genix can render dropdowns without the browser needing CRM creds.
//   2. Link CRUD: setting/clearing crm_project_id on a project and
//      crm_block_id + current_crm_stage on a building.
//   3. Manual re-sync trigger for a photo report (used by the "Re-send"
//      button when auto-sync failed and the user wants to retry now).

package handler

import (
	"net/http"
	"strconv"

	"github.com/genixerp/genix-backend/internal/crm"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListCRMProjects proxies CRM's project list. Returns an empty array if
// the CRM client isn't configured so the frontend can render a friendly
// "CRM not configured" message instead of erroring.
//
// @Router /construction/crm/projects [get]
func (h *Handler) ListCRMProjects(c *gin.Context) {
	if h.crmSync == nil || !h.crmSync.Configured() {
		response.Success(c, map[string]interface{}{
			"configured": false,
			"projects":   []crm.Project{},
		})
		return
	}
	projects, err := h.crmSync.Client().ListProjects()
	if err != nil {
		h.log.Error("ListCRMProjects: client error", "error", err)
		response.InternalError(c, "Failed to list CRM projects: "+err.Error())
		return
	}
	response.Success(c, map[string]interface{}{
		"configured": true,
		"projects":   projects,
	})
}

// ListCRMBlocks proxies CRM's blocks list for a given CRM project.
// Frontend uses this to populate the "Link to CRM block" dropdown in
// the building create/edit form.
//
// @Router /construction/crm/blocks [get]
func (h *Handler) ListCRMBlocks(c *gin.Context) {
	if h.crmSync == nil || !h.crmSync.Configured() {
		response.Success(c, map[string]interface{}{
			"configured": false,
			"blocks":     []crm.Block{},
		})
		return
	}
	crmProjectID, err := strconv.Atoi(c.Query("crm_project_id"))
	if err != nil || crmProjectID <= 0 {
		response.BadRequest(c, "crm_project_id query param is required")
		return
	}
	blocks, err := h.crmSync.Client().ListBlocks(crmProjectID)
	if err != nil {
		h.log.Error("ListCRMBlocks: client error", "error", err)
		response.InternalError(c, "Failed to list CRM blocks: "+err.Error())
		return
	}
	response.Success(c, map[string]interface{}{
		"configured": true,
		"blocks":     blocks,
	})
}

// UpdateProjectCRMLink sets (or clears) crm_project_id on a Genix
// construction project. Pass crm_project_id=0 or null to unlink.
//
// @Router /construction/projects/{id}/crm-link [put]
func (h *Handler) UpdateProjectCRMLink(c *gin.Context) {
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
	var input struct {
		CRMProjectID *int `json:"crm_project_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	var crmID interface{}
	if input.CRMProjectID != nil && *input.CRMProjectID > 0 {
		crmID = *input.CRMProjectID
	} else {
		crmID = nil
	}
	_, err = h.db.Exec(`
		UPDATE construction_projects
		SET crm_project_id = $1, updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, crmID, projectID, tenantID)
	if err != nil {
		h.log.Error("UpdateProjectCRMLink failed", "error", err)
		response.InternalError(c, "Failed to update CRM link")
		return
	}
	response.Success(c, map[string]interface{}{"crm_project_id": crmID})
}

// UpdateBuildingCRMLink sets crm_block_id, current_crm_stage and the
// crm_auto_sync toggle on a Genix construction_buildings row.
//
// @Router /construction/projects/{id}/buildings/{building_id}/crm-link [put]
func (h *Handler) UpdateBuildingCRMLink(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	buildingID, err := strconv.ParseInt(c.Param("building_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid building ID")
		return
	}
	var input struct {
		CRMBlockID      *int    `json:"crm_block_id"`
		CurrentCRMStage *string `json:"current_crm_stage"`
		AutoSync        *bool   `json:"crm_auto_sync"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic UPDATE so callers can patch any subset of the three
	// fields without clobbering the others.
	updates := []string{}
	args := []interface{}{}
	argCount := 0
	if input.CRMBlockID != nil {
		argCount++
		if *input.CRMBlockID > 0 {
			updates = append(updates, "crm_block_id = $"+strconv.Itoa(argCount))
			args = append(args, *input.CRMBlockID)
		} else {
			updates = append(updates, "crm_block_id = NULL")
			argCount-- // didn't actually consume a placeholder
		}
	}
	if input.CurrentCRMStage != nil && *input.CurrentCRMStage != "" {
		argCount++
		updates = append(updates, "current_crm_stage = $"+strconv.Itoa(argCount))
		args = append(args, *input.CurrentCRMStage)
	}
	if input.AutoSync != nil {
		argCount++
		updates = append(updates, "crm_auto_sync = $"+strconv.Itoa(argCount))
		args = append(args, *input.AutoSync)
	}
	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}
	argCount++
	args = append(args, buildingID)
	argCount++
	args = append(args, tenantID)

	q := "UPDATE construction_buildings SET " + joinStrings(updates, ", ") +
		", updated_date = NOW() WHERE id = $" + strconv.Itoa(argCount-1) +
		" AND tenant_id = $" + strconv.Itoa(argCount)
	if _, err := h.db.Exec(q, args...); err != nil {
		h.log.Error("UpdateBuildingCRMLink failed", "error", err)
		response.InternalError(c, "Failed to update CRM link")
		return
	}
	response.Success(c, map[string]string{"message": "Link updated"})
}

// ResyncPhotoReportToCRM lets the operator manually re-trigger a sync.
// Used by the "Re-send" button after a failure (or after the building
// was linked to a different CRM block after the report was created).
//
// @Router /construction/photo-reports/{id}/resync-crm [post]
func (h *Handler) ResyncPhotoReportToCRM(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid report ID")
		return
	}

	// Clear the synced-at marker so the syncer doesn't short-circuit on
	// the "already synced" check.
	_, _ = h.db.Exec(`
		UPDATE construction_photo_reports
		SET crm_synced_at = NULL, crm_sync_error = NULL, updated_date = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, reportID, tenantID)

	if h.crmSync != nil {
		h.crmSync.Enqueue(c.Request.Context(), tenantID, reportID)
	}
	response.Success(c, map[string]string{"message": "Re-sync queued"})
}

// joinStrings is a tiny strings.Join shim so we don't import strings just
// for one line.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}

// suppress unused import in case http isn't referenced after future refactors
var _ = http.StatusOK
