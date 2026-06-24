package handler

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// defaultProjectStages are seeded the first time a project's stages are listed,
// keeping backwards compatibility with tasks whose status is one of these keys.
var defaultProjectStages = []entity.ProjectTaskStage{
	{StageKey: "todo", Name: "To Do", Color: "bg-gray-100 text-gray-700", Position: 0},
	{StageKey: "in_progress", Name: "In Progress", Color: "bg-blue-100 text-blue-700", Position: 1},
	{StageKey: "review", Name: "Review", Color: "bg-yellow-100 text-yellow-700", Position: 2},
	{StageKey: "completed", Name: "Completed", Color: "bg-green-100 text-green-700", Position: 3},
}

var stageSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugifyStage(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = stageSlugRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "stage"
	}
	return s
}

// ListProjectStages returns the kanban stages for a project, seeding defaults
// the first time it is called for a project that has none.
func (h *Handler) ListProjectStages(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	stages, err := h.fetchProjectStages(tenantID, projectID)
	if err != nil {
		h.log.Error("Failed to fetch stages", "error", err)
		response.InternalError(c, "Failed to fetch stages")
		return
	}

	// Lazily seed defaults for projects that don't have stages yet.
	if len(stages) == 0 {
		now := time.Now()
		for _, s := range defaultProjectStages {
			h.db.Exec(`
				INSERT INTO project_task_stages (id, tenant_id, project_id, stage_key, name, color, position, is_default, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, true, $8, $8)
				ON CONFLICT (project_id, stage_key) DO NOTHING`,
				uuid.New(), tenantID, projectID, s.StageKey, s.Name, s.Color, s.Position, now)
		}
		stages, err = h.fetchProjectStages(tenantID, projectID)
		if err != nil {
			response.InternalError(c, "Failed to fetch stages")
			return
		}
	}

	response.Success(c, stages)
}

func (h *Handler) fetchProjectStages(tenantID, projectID uuid.UUID) ([]entity.ProjectTaskStage, error) {
	rows, err := h.db.Query(`
		SELECT id, tenant_id, project_id, stage_key, name, COALESCE(color, ''), position, is_default, created_at, updated_at
		FROM project_task_stages
		WHERE project_id = $1 AND tenant_id = $2
		ORDER BY position ASC, created_at ASC`, projectID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stages := []entity.ProjectTaskStage{}
	for rows.Next() {
		var s entity.ProjectTaskStage
		if err := rows.Scan(&s.ID, &s.TenantID, &s.ProjectID, &s.StageKey, &s.Name, &s.Color, &s.Position, &s.IsDefault, &s.CreatedAt, &s.UpdatedAt); err != nil {
			continue
		}
		stages = append(stages, s)
	}
	return stages, nil
}

// CreateProjectStage adds a new kanban stage to a project.
func (h *Handler) CreateProjectStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}

	var input entity.CreateProjectStageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build a unique stage key within the project.
	base := slugifyStage(input.Name)
	stageKey := base
	for i := 1; ; i++ {
		var exists bool
		h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_task_stages WHERE project_id = $1 AND stage_key = $2)`, projectID, stageKey).Scan(&exists)
		if !exists {
			break
		}
		stageKey = fmt.Sprintf("%s_%d", base, i)
	}

	// Next position = max + 1
	var nextPos int
	h.db.QueryRow(`SELECT COALESCE(MAX(position), -1) + 1 FROM project_task_stages WHERE project_id = $1 AND tenant_id = $2`, projectID, tenantID).Scan(&nextPos)

	color := input.Color
	if color == "" {
		color = "bg-slate-100 text-slate-700"
	}

	id := uuid.New()
	now := time.Now()
	_, err = h.db.Exec(`
		INSERT INTO project_task_stages (id, tenant_id, project_id, stage_key, name, color, position, is_default, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, false, $8, $8)`,
		id, tenantID, projectID, stageKey, input.Name, color, nextPos, now)
	if err != nil {
		h.log.Error("Failed to create stage", "error", err)
		response.InternalError(c, "Failed to create stage")
		return
	}

	response.Created(c, entity.ProjectTaskStage{
		ID: id, TenantID: tenantID, ProjectID: projectID, StageKey: stageKey,
		Name: input.Name, Color: color, Position: nextPos, IsDefault: false,
		CreatedAt: now, UpdatedAt: now,
	})
}

// UpdateProjectStage updates a stage's name, color, or position.
func (h *Handler) UpdateProjectStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	stageID, err := uuid.Parse(c.Param("stageId"))
	if err != nil {
		response.BadRequest(c, "Invalid stage ID")
		return
	}

	var input entity.UpdateProjectStageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0
	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.Color != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("color = $%d", argCount))
		args = append(args, *input.Color)
	}
	if input.Position != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("position = $%d", argCount))
		args = append(args, *input.Position)
	}
	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	stagePlaceholder := argCount + 1
	tenantPlaceholder := argCount + 2
	args = append(args, stageID, tenantID)

	query := fmt.Sprintf("UPDATE project_task_stages SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), stagePlaceholder, tenantPlaceholder)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update stage", "error", err)
		response.InternalError(c, "Failed to update stage")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		response.NotFound(c, "Stage")
		return
	}

	response.Success(c, gin.H{"message": "Stage updated"})
}

// DeleteProjectStage removes a stage. Blocked if any task still uses it.
func (h *Handler) DeleteProjectStage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}
	stageID, err := uuid.Parse(c.Param("stageId"))
	if err != nil {
		response.BadRequest(c, "Invalid stage ID")
		return
	}

	// Look up the stage key so we can check for tasks using it.
	var stageKey string
	if err := h.db.QueryRow(`SELECT stage_key FROM project_task_stages WHERE id = $1 AND tenant_id = $2`, stageID, tenantID).Scan(&stageKey); err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Stage")
			return
		}
		response.InternalError(c, "Failed to delete stage")
		return
	}

	var taskCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM project_tasks WHERE project_id = $1 AND status = $2 AND deleted_at IS NULL`, projectID, stageKey).Scan(&taskCount)
	if taskCount > 0 {
		response.BadRequest(c, "Cannot delete a stage that still has tasks. Move the tasks to another stage first.")
		return
	}

	result, err := h.db.Exec(`DELETE FROM project_task_stages WHERE id = $1 AND tenant_id = $2`, stageID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete stage", "error", err)
		response.InternalError(c, "Failed to delete stage")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		response.NotFound(c, "Stage")
		return
	}

	response.Success(c, gin.H{"message": "Stage deleted"})
}
