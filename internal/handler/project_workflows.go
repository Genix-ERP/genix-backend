package handler

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// resolveUserIDsForEmployees maps a set of employee ids to their linked user ids.
func (h *Handler) resolveUserIDsForEmployees(tenantID uuid.UUID, employeeIDs []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, eid := range employeeIDs {
		if eid == "" {
			continue
		}
		ueid, err := uuid.Parse(eid)
		if err != nil {
			continue
		}
		var uid string
		err = h.db.QueryRow(`SELECT id FROM users WHERE tenant_id = $1 AND employee_id = $2 AND deleted_at IS NULL LIMIT 1`, tenantID, ueid).Scan(&uid)
		if err == nil && uid != "" && !seen[uid] {
			seen[uid] = true
			out = append(out, uid)
		}
	}
	return out
}

// projectOwnerUserIDs returns the user ids to notify for project-level events
// (the project creator and, if linked, the manager).
func (h *Handler) projectOwnerUserIDs(tenantID, projectID uuid.UUID) []string {
	out := []string{}
	seen := map[string]bool{}
	var createdBy, managerID *uuid.UUID
	h.db.QueryRow(`SELECT created_by, manager_id FROM projects WHERE id = $1 AND tenant_id = $2`, projectID, tenantID).Scan(&createdBy, &managerID)
	if createdBy != nil {
		s := createdBy.String()
		seen[s] = true
		out = append(out, s)
	}
	if managerID != nil {
		// manager_id may be an employee id; map to a user
		for _, uid := range h.resolveUserIDsForEmployees(tenantID, []string{managerID.String()}) {
			if !seen[uid] {
				seen[uid] = true
				out = append(out, uid)
			}
		}
	}
	return out
}

// fireProjectTaskAssigned notifies the assignees that a task was assigned to them.
func (h *Handler) fireProjectTaskAssigned(tenantID, projectID, taskID uuid.UUID, taskTitle string, assigneeEmployeeIDs []string) {
	users := h.resolveUserIDsForEmployees(tenantID, assigneeEmployeeIDs)
	if len(users) == 0 {
		return
	}
	h.EvaluateWorkflowRules(tenantID, "project.task.assigned", map[string]interface{}{
		"project_id":      projectID.String(),
		"task_id":         taskID.String(),
		"task_title":      taskTitle,
		"notify_user_ids": users,
	})
}

// fireProjectTaskStatusChanged notifies assignees a task's status changed.
func (h *Handler) fireProjectTaskStatusChanged(tenantID, projectID, taskID uuid.UUID, taskTitle, newStatus string, assigneeEmployeeIDs []string) {
	users := h.resolveUserIDsForEmployees(tenantID, assigneeEmployeeIDs)
	h.EvaluateWorkflowRules(tenantID, "project.task.status_changed", map[string]interface{}{
		"project_id":      projectID.String(),
		"task_id":         taskID.String(),
		"task_title":      taskTitle,
		"new_status":      newStatus,
		"notify_user_ids": users,
	})
}

// fireProjectMilestoneCompleted notifies the project owner a milestone is done.
func (h *Handler) fireProjectMilestoneCompleted(tenantID, projectID, milestoneID uuid.UUID, milestoneTitle string) {
	users := h.projectOwnerUserIDs(tenantID, projectID)
	h.EvaluateWorkflowRules(tenantID, "project.milestone.completed", map[string]interface{}{
		"project_id":      projectID.String(),
		"milestone_id":    milestoneID.String(),
		"milestone_title": milestoneTitle,
		"notify_user_ids": users,
	})
}

// actionUpdateTaskPriority raises (or sets) the priority of the task referenced
// in the trigger data. Used by the "task overdue" rule to push overdue tasks to
// high priority. Safe + idempotent: it only touches the one task, skips
// completed/cancelled tasks, and setting the same priority twice is a no-op.
func (h *Handler) actionUpdateTaskPriority(tenantID uuid.UUID, config map[string]interface{}, data map[string]interface{}) (string, error) {
	taskIDStr, _ := data["task_id"].(string)
	if taskIDStr == "" {
		return "", fmt.Errorf("task_id not found in trigger data")
	}
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid task_id: %s", taskIDStr)
	}
	priority, _ := config["priority"].(string)
	if priority == "" {
		priority = "high"
	}
	res, err := h.db.Exec(`
		UPDATE project_tasks SET priority = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
		  AND status NOT IN ('completed', 'cancelled')`,
		priority, taskID, tenantID)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	return fmt.Sprintf("Set priority=%s on %d task(s)", priority, n), nil
}

// actionCreateFollowupTask creates a follow-up task in the project referenced by
// the trigger data. Used by the "milestone completed" rule. Deduped by
// milestone: if a follow-up with the same title already exists for that
// milestone, it is not created again.
func (h *Handler) actionCreateFollowupTask(tenantID uuid.UUID, config map[string]interface{}, data map[string]interface{}) (string, error) {
	projectIDStr, _ := data["project_id"].(string)
	if projectIDStr == "" {
		return "", fmt.Errorf("project_id not found in trigger data")
	}
	projectID, err := uuid.Parse(projectIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid project_id: %s", projectIDStr)
	}

	// Template the title from config (fallback to a sensible default), filling
	// {placeholders} from the trigger data the same way notifications do.
	title, _ := config["title"].(string)
	if title == "" {
		title = "{milestone_title} — keyingi qadamlar"
	}
	for key, val := range data {
		title = strings.ReplaceAll(title, "{"+key+"}", fmt.Sprintf("%v", val))
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Keyingi qadamlar"
	}

	// Optional milestone link + dedup.
	var milestoneID *uuid.UUID
	if msStr, _ := data["milestone_id"].(string); msStr != "" {
		if mid, perr := uuid.Parse(msStr); perr == nil {
			milestoneID = &mid
			var existing uuid.UUID
			if h.db.QueryRow(`
				SELECT id FROM project_tasks
				WHERE tenant_id = $1 AND project_id = $2 AND milestone_id = $3
				  AND title = $4 AND deleted_at IS NULL LIMIT 1`,
				tenantID, projectID, mid, title).Scan(&existing) == nil {
				return "Follow-up task already exists", nil
			}
		}
	}

	taskID := uuid.New()
	taskNumber := "TASK-" + uuid.New().String()[:8]
	_, err = h.db.Exec(`
		INSERT INTO project_tasks (
			id, tenant_id, project_id, task_number, title, priority, status,
			estimated_hours, actual_hours, created_at, updated_at, milestone_id
		) VALUES ($1, $2, $3, $4, $5, 'medium', 'todo', 0, 0, NOW(), NOW(), $6)`,
		taskID, tenantID, projectID, taskNumber, title, milestoneID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Created follow-up task: %s", title), nil
}

// taskAssigneeEmployeeIDs returns the employee ids assigned to a task.
func (h *Handler) taskAssigneeEmployeeIDs(tenantID, taskID uuid.UUID) []string {
	out := []string{}
	rows, err := h.db.Query(`SELECT employee_id FROM project_task_assignees WHERE task_id = $1 AND tenant_id = $2`, taskID, tenantID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var eid uuid.UUID
		if err := rows.Scan(&eid); err == nil {
			out = append(out, eid.String())
		}
	}
	return out
}

// fireProjectTaskOverdue notifies assignees (falling back to the project
// owner when a task has no assignees) that a task is past its due date.
func (h *Handler) fireProjectTaskOverdue(tenantID, projectID, taskID uuid.UUID, taskTitle, dueDate string, assigneeEmployeeIDs []string) {
	users := h.resolveUserIDsForEmployees(tenantID, assigneeEmployeeIDs)
	if len(users) == 0 {
		users = h.projectOwnerUserIDs(tenantID, projectID)
	}
	h.EvaluateWorkflowRules(tenantID, "project.task.overdue", map[string]interface{}{
		"project_id":      projectID.String(),
		"task_id":         taskID.String(),
		"task_title":      taskTitle,
		"due_date":        dueDate,
		"notify_user_ids": users,
	})
}

// fireProjectOverBudget notifies the project owner that spend has exceeded budget.
func (h *Handler) fireProjectOverBudget(tenantID, projectID uuid.UUID, projectName string, budget, spent float64) {
	users := h.projectOwnerUserIDs(tenantID, projectID)
	h.EvaluateWorkflowRules(tenantID, "project.over_budget", map[string]interface{}{
		"project_id":      projectID.String(),
		"project_name":    projectName,
		"budget":          budget,
		"spent":           spent,
		"notify_user_ids": users,
	})
}

// CheckProjectWorkflows is run by the workflow scheduler. It fires
// notifications for tasks that have become overdue and projects that have gone
// over budget. Both use a one-shot "*_notified" flag so a single breach
// produces a single notification; the flag resets when the condition clears.
func (h *Handler) CheckProjectWorkflows() {
	now := time.Now()

	// --- Overdue tasks: due_date passed, not done/cancelled, not yet notified ---
	taskRows, err := h.db.Query(`
		SELECT t.id, t.tenant_id, t.project_id, t.title, t.due_date
		FROM project_tasks t
		WHERE t.deleted_at IS NULL
		  AND t.due_date IS NOT NULL
		  AND t.due_date < $1
		  AND t.status NOT IN ('completed', 'cancelled')
		  AND COALESCE(t.overdue_notified, false) = false
		LIMIT 500
	`, now)
	if err != nil {
		h.log.Error("Project workflow: failed to query overdue tasks", "error", err)
	} else {
		overdue := 0
		type od struct {
			taskID, tenantID, projectID uuid.UUID
			title, due                  string
		}
		var list []od
		for taskRows.Next() {
			var r od
			var due time.Time
			if err := taskRows.Scan(&r.taskID, &r.tenantID, &r.projectID, &r.title, &due); err != nil {
				continue
			}
			r.due = due.Format("2006-01-02")
			list = append(list, r)
		}
		taskRows.Close()
		for _, r := range list {
			h.fireProjectTaskOverdue(r.tenantID, r.projectID, r.taskID, r.title, r.due, h.taskAssigneeEmployeeIDs(r.tenantID, r.taskID))
			h.db.Exec(`UPDATE project_tasks SET overdue_notified = true WHERE id = $1`, r.taskID)
			overdue++
		}
		// Reset the flag for tasks that are no longer overdue so a future
		// breach (re-opened or due date pushed then passed again) re-notifies.
		h.db.Exec(`
			UPDATE project_tasks SET overdue_notified = false
			WHERE COALESCE(overdue_notified, false) = true
			  AND (due_date IS NULL OR due_date >= $1 OR status IN ('completed', 'cancelled'))
		`, now)
		if overdue > 0 {
			h.log.Info("Project workflow: overdue task notifications fired", "count", overdue)
		}
	}

	// --- Over-budget projects: spent > budget, budget set, not yet notified ---
	projRows, err := h.db.Query(`
		SELECT id, tenant_id, project_name, budget, spent
		FROM projects
		WHERE deleted_at IS NULL
		  AND budget > 0
		  AND spent > budget
		  AND status NOT IN ('completed', 'cancelled')
		  AND COALESCE(over_budget_notified, false) = false
		LIMIT 500
	`)
	if err != nil {
		h.log.Error("Project workflow: failed to query over-budget projects", "error", err)
	} else {
		type ob struct {
			projectID, tenantID uuid.UUID
			name                string
			budget, spent       float64
		}
		var list []ob
		for projRows.Next() {
			var r ob
			if err := projRows.Scan(&r.projectID, &r.tenantID, &r.name, &r.budget, &r.spent); err != nil {
				continue
			}
			list = append(list, r)
		}
		projRows.Close()
		overBudget := 0
		for _, r := range list {
			h.fireProjectOverBudget(r.tenantID, r.projectID, r.name, r.budget, r.spent)
			h.db.Exec(`UPDATE projects SET over_budget_notified = true WHERE id = $1`, r.projectID)
			overBudget++
		}
		// Reset for projects back within budget.
		h.db.Exec(`
			UPDATE projects SET over_budget_notified = false
			WHERE COALESCE(over_budget_notified, false) = true
			  AND (budget <= 0 OR spent <= budget)
		`)
		if overBudget > 0 {
			h.log.Info("Project workflow: over-budget notifications fired", "count", overBudget)
		}
	}
}
