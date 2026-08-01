package handler

import (
	"fmt"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListConstructionProjectsByOrganization lists open construction projects for
// an organization (intercompany sales-order flow). Moved verbatim from the
// retired Loyihalar module's /projects/by-organization — it always queried
// construction_projects, so it now lives under /construction/projects.
func (h *Handler) ListConstructionProjectsByOrganization(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orgID := c.Query("organization_id")
	if orgID == "" {
		response.BadRequest(c, "organization_id is required")
		return
	}

	parsedOrgID, err := uuid.Parse(orgID)
	if err != nil {
		response.BadRequest(c, "Invalid organization_id")
		return
	}

	// Verify that this organization belongs to the same tenant
	var exists bool
	err = h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1 AND tenant_id = $2)`, parsedOrgID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Organization")
		return
	}

	// Fetch construction projects: first try matching org, then fall back to all tenant projects
	query := `
		SELECT cp.id, cp.code, cp.name, cp.status
		FROM construction_projects cp
		WHERE cp.tenant_id = $1 AND cp.deleted_at IS NULL
		  AND cp.status IN ('draft', 'planning', 'active', 'in_progress')
		  AND (cp.organization_id = $2 OR cp.organization_id IS NULL OR cp.organization_id = '00000000-0000-0000-0000-000000000000')
		ORDER BY cp.name ASC`

	rows, err := h.db.Query(query, tenantID, parsedOrgID)
	if err != nil {
		response.InternalError(c, "Failed to fetch projects")
		return
	}
	defer rows.Close()

	var projects []map[string]interface{}
	for rows.Next() {
		var id int64
		var projectCode, projectName, status string
		if err := rows.Scan(&id, &projectCode, &projectName, &status); err != nil {
			continue
		}
		projects = append(projects, map[string]interface{}{
			"id":           fmt.Sprintf("%d", id),
			"project_code": projectCode,
			"project_name": projectName,
			"status":       status,
		})
	}

	// If no projects found with org filter, fetch all construction projects in the tenant
	if len(projects) == 0 {
		fallbackQuery := `
			SELECT cp.id, cp.code, cp.name, cp.status
			FROM construction_projects cp
			WHERE cp.tenant_id = $1 AND cp.deleted_at IS NULL
			  AND cp.status IN ('draft', 'planning', 'active', 'in_progress')
			ORDER BY cp.name ASC`

		fbRows, fbErr := h.db.Query(fallbackQuery, tenantID)
		if fbErr == nil {
			defer fbRows.Close()
			for fbRows.Next() {
				var id int64
				var projectCode, projectName, status string
				if fbErr := fbRows.Scan(&id, &projectCode, &projectName, &status); fbErr != nil {
					continue
				}
				projects = append(projects, map[string]interface{}{
					"id":           fmt.Sprintf("%d", id),
					"project_code": projectCode,
					"project_name": projectName,
					"status":       status,
				})
			}
		}
	}

	if projects == nil {
		projects = []map[string]interface{}{}
	}

	response.Success(c, projects)
}
