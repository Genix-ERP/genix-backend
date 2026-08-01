package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ===== CRUD Endpoints =====

// ListWorkflowRules returns all workflow rules for a tenant
func (h *Handler) ListWorkflowRules(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	category := c.Query("category")
	activeOnly := c.Query("active") == "true"

	query := `
		SELECT r.id, r.tenant_id, r.name, r.description, r.category, r.trigger_type, r.trigger_event,
			   r.conditions, r.actions, r.is_active, r.priority, r.last_triggered_at, r.trigger_count,
			   r.created_by, r.created_at, r.updated_at, r.auto_paused_at, r.paused_reason, ll.status
		FROM workflow_rules r
		LEFT JOIN LATERAL (
			SELECT status FROM workflow_logs wl
			WHERE wl.rule_id = r.id ORDER BY wl.executed_at DESC LIMIT 1
		) ll ON true
		WHERE r.tenant_id = $1 AND r.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	if category != "" {
		argCount++
		query += fmt.Sprintf(" AND r.category = $%d", argCount)
		args = append(args, category)
	}

	if activeOnly {
		query += " AND r.is_active = true"
	}

	query += " ORDER BY r.created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list workflow rules", "error", err)
		response.InternalError(c, "Failed to list workflow rules")
		return
	}
	defer rows.Close()

	rules := make([]*entity.WorkflowRuleResponse, 0)
	for rows.Next() {
		var r entity.WorkflowRule
		var description, triggerEvent sql.NullString
		var lastTriggered, autoPaused sql.NullTime
		var createdBy, pausedReason, lastStatus sql.NullString

		err := rows.Scan(
			&r.ID, &r.TenantID, &r.Name, &description, &r.Category,
			&r.TriggerType, &triggerEvent, &r.Conditions, &r.Actions,
			&r.IsActive, &r.Priority, &lastTriggered, &r.TriggerCount,
			&createdBy, &r.CreatedAt, &r.UpdatedAt, &autoPaused, &pausedReason, &lastStatus,
		)
		if err != nil {
			h.log.Error("Failed to scan workflow rule", "error", err)
			continue
		}

		if description.Valid {
			r.Description = &description.String
		}
		if triggerEvent.Valid {
			r.TriggerEvent = &triggerEvent.String
		}
		if lastTriggered.Valid {
			r.LastTriggeredAt = &lastTriggered.Time
		}
		if autoPaused.Valid {
			r.AutoPausedAt = &autoPaused.Time
		}
		if pausedReason.Valid {
			r.PausedReason = &pausedReason.String
		}
		if lastStatus.Valid {
			r.LastStatus = &lastStatus.String
		}

		rules = append(rules, r.ToResponse())
	}

	response.Success(c, rules)
}

// CreateWorkflowRule creates a new workflow rule
func (h *Handler) CreateWorkflowRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	claims, _ := middleware.GetClaims(c)

	var input entity.CreateWorkflowRuleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Validate against the event catalog; category and trigger_type are
	// derived server-side so a client can't create a dead rule (the old UI's
	// event/threshold/scheduled picker foot-gun).
	category, scheduled, vErr := validateWorkflowRuleConfig(input.TriggerEvent, input.Conditions, input.Actions)
	if vErr != nil {
		response.BadRequest(c, vErr.Error())
		return
	}
	input.Category = category
	input.TriggerType = "event"
	if scheduled {
		input.TriggerType = "scheduled"
	}

	id := uuid.New()
	now := time.Now()

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	conditions := input.Conditions
	if conditions == nil {
		conditions = json.RawMessage("{}")
	}

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	var triggerEvent *string
	if input.TriggerEvent != "" {
		triggerEvent = &input.TriggerEvent
	}

	query := `
		INSERT INTO workflow_rules (
			id, tenant_id, name, description, category, trigger_type, trigger_event,
			conditions, actions, is_active, priority, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id
	`

	err := h.db.QueryRow(query,
		id, tenantID, input.Name, description, input.Category,
		input.TriggerType, triggerEvent, conditions, input.Actions,
		isActive, input.Priority, claims.UserID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create workflow rule", "error", err)
		response.InternalError(c, "Failed to create workflow rule")
		return
	}

	rule := &entity.WorkflowRule{
		ID:           id,
		TenantID:     tenantID,
		Name:         input.Name,
		Description:  description,
		Category:     input.Category,
		TriggerType:  input.TriggerType,
		TriggerEvent: triggerEvent,
		Conditions:   conditions,
		Actions:      input.Actions,
		IsActive:     isActive,
		Priority:     input.Priority,
		TriggerCount: 0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	response.Created(c, rule.ToResponse())
}

// GetWorkflowRule returns a single workflow rule
func (h *Handler) GetWorkflowRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid rule ID")
		return
	}

	query := `
		SELECT id, tenant_id, name, description, category, trigger_type, trigger_event,
			   conditions, actions, is_active, priority, last_triggered_at, trigger_count,
			   created_by, created_at, updated_at
		FROM workflow_rules
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`

	var r entity.WorkflowRule
	var description, triggerEvent sql.NullString
	var lastTriggered sql.NullTime
	var createdBy sql.NullString

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&r.ID, &r.TenantID, &r.Name, &description, &r.Category,
		&r.TriggerType, &triggerEvent, &r.Conditions, &r.Actions,
		&r.IsActive, &r.Priority, &lastTriggered, &r.TriggerCount,
		&createdBy, &r.CreatedAt, &r.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Workflow rule")
		return
	}
	if err != nil {
		h.log.Error("Failed to get workflow rule", "error", err)
		response.InternalError(c, "Failed to get workflow rule")
		return
	}

	if description.Valid {
		r.Description = &description.String
	}
	if triggerEvent.Valid {
		r.TriggerEvent = &triggerEvent.String
	}
	if lastTriggered.Valid {
		r.LastTriggeredAt = &lastTriggered.Time
	}

	response.Success(c, r.ToResponse())
}

// UpdateWorkflowRule updates an existing workflow rule
func (h *Handler) UpdateWorkflowRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid rule ID")
		return
	}

	var input entity.UpdateWorkflowRuleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Validate the effective (merged) config when anything engine-relevant changes
	if input.TriggerEvent != nil || input.Conditions != nil || input.Actions != nil {
		var curEvent sql.NullString
		var curConditions, curActions json.RawMessage
		err := h.db.QueryRow(`
			SELECT trigger_event, conditions, actions FROM workflow_rules
			WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		`, id, tenantID).Scan(&curEvent, &curConditions, &curActions)
		if err == sql.ErrNoRows {
			response.NotFound(c, "Workflow rule")
			return
		}
		if err != nil {
			h.log.Error("Failed to load workflow rule for validation", "error", err)
			response.InternalError(c, "Failed to update workflow rule")
			return
		}

		effEvent := curEvent.String
		if input.TriggerEvent != nil {
			effEvent = *input.TriggerEvent
		}
		effConditions := curConditions
		if input.Conditions != nil {
			effConditions = *input.Conditions
		}
		effActions := curActions
		if input.Actions != nil {
			effActions = *input.Actions
		}

		category, scheduled, vErr := validateWorkflowRuleConfig(effEvent, effConditions, effActions)
		if vErr != nil {
			response.BadRequest(c, vErr.Error())
			return
		}
		// keep derived columns in sync with the (possibly new) event
		cat := category
		input.Category = &cat
		tt := "event"
		if scheduled {
			tt = "scheduled"
		}
		input.TriggerType = &tt
	}

	updates := make([]string, 0)
	args := make([]interface{}, 0)
	argCount := 0

	addUpdate := func(field string, value interface{}) {
		argCount++
		updates = append(updates, fmt.Sprintf("%s = $%d", field, argCount))
		args = append(args, value)
	}

	if input.Name != nil {
		addUpdate("name", *input.Name)
	}
	if input.Description != nil {
		addUpdate("description", *input.Description)
	}
	if input.Category != nil {
		addUpdate("category", *input.Category)
	}
	if input.TriggerType != nil {
		addUpdate("trigger_type", *input.TriggerType)
	}
	if input.TriggerEvent != nil {
		addUpdate("trigger_event", *input.TriggerEvent)
	}
	if input.Conditions != nil {
		addUpdate("conditions", *input.Conditions)
	}
	if input.Actions != nil {
		addUpdate("actions", *input.Actions)
	}
	if input.IsActive != nil {
		addUpdate("is_active", *input.IsActive)
		if *input.IsActive {
			// manual re-activation clears the auto-pause flag
			updates = append(updates, "auto_paused_at = NULL", "paused_reason = NULL")
		}
	}
	if input.Priority != nil {
		addUpdate("priority", *input.Priority)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	addUpdate("updated_at", time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(`
		UPDATE workflow_rules SET %s
		WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL
		RETURNING id
	`, strings.Join(updates, ", "), argCount-1, argCount)

	var returnedID uuid.UUID
	err = h.db.QueryRow(query, args...).Scan(&returnedID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Workflow rule")
		return
	}
	if err != nil {
		h.log.Error("Failed to update workflow rule", "error", err)
		response.InternalError(c, "Failed to update workflow rule")
		return
	}

	h.GetWorkflowRule(c)
}

// DeleteWorkflowRule soft-deletes a workflow rule
func (h *Handler) DeleteWorkflowRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid rule ID")
		return
	}

	result, err := h.db.Exec(`
		UPDATE workflow_rules SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, time.Now(), id, tenantID)

	if err != nil {
		h.log.Error("Failed to delete workflow rule", "error", err)
		response.InternalError(c, "Failed to delete workflow rule")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Workflow rule")
		return
	}

	response.NoContent(c)
}

// ListWorkflowLogs returns execution logs for workflow rules.
// Filters: rule_id, status, event, date_from, date_to (YYYY-MM-DD), limit.
func (h *Handler) ListWorkflowLogs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 200 {
		limit = 50
	}

	query := `
		SELECT wl.id, wl.rule_id, wr.name as rule_name, wl.trigger_data,
			   wl.actions_executed, wl.condition_results, wl.status, wl.error_message,
			   wl.trigger_event, wl.related_type, wl.related_id, wl.duration_ms, wl.executed_at
		FROM workflow_logs wl
		JOIN workflow_rules wr ON wl.rule_id = wr.id
		WHERE wl.tenant_id = $1
	`
	args := []interface{}{tenantID}
	argCount := 1
	addFilter := func(clause string, value interface{}) {
		argCount++
		query += fmt.Sprintf(clause, argCount)
		args = append(args, value)
	}

	if v := c.Query("rule_id"); v != "" {
		addFilter(" AND wl.rule_id = $%d", v)
	}
	if v := c.Query("status"); v != "" {
		addFilter(" AND wl.status = $%d", v)
	}
	if v := c.Query("event"); v != "" {
		addFilter(" AND wl.trigger_event = $%d", v)
	}
	if v := c.Query("date_from"); v != "" {
		addFilter(" AND wl.executed_at >= $%d::date", v)
	}
	if v := c.Query("date_to"); v != "" {
		addFilter(" AND wl.executed_at < ($%d::date + INTERVAL '1 day')", v)
	}

	query += fmt.Sprintf(" ORDER BY wl.executed_at DESC LIMIT %d", limit)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list workflow logs", "error", err)
		response.InternalError(c, "Failed to list workflow logs")
		return
	}
	defer rows.Close()

	logs := make([]*entity.WorkflowLogResponse, 0)
	for rows.Next() {
		var log entity.WorkflowLogResponse
		var errorMsg, triggerEvent, relatedType, relatedID sql.NullString
		var conditionResults []byte
		var durationMs sql.NullInt64
		var executedAt time.Time

		err := rows.Scan(
			&log.ID, &log.RuleID, &log.RuleName, &log.TriggerData,
			&log.ActionsExecuted, &conditionResults, &log.Status, &errorMsg,
			&triggerEvent, &relatedType, &relatedID, &durationMs, &executedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan workflow log", "error", err)
			continue
		}

		if errorMsg.Valid {
			log.ErrorMessage = &errorMsg.String
		}
		if triggerEvent.Valid {
			log.TriggerEvent = &triggerEvent.String
		}
		if relatedType.Valid {
			log.RelatedType = &relatedType.String
		}
		if relatedID.Valid {
			log.RelatedID = &relatedID.String
		}
		if durationMs.Valid {
			d := int(durationMs.Int64)
			log.DurationMs = &d
		}
		if len(conditionResults) > 0 {
			log.ConditionResults = conditionResults
		}
		log.ExecutedAt = executedAt.Format(time.RFC3339)
		logs = append(logs, &log)
	}

	response.Success(c, logs)
}

// ListWorkflowEvents returns the server-side trigger-event catalog: event id,
// category, whether it is scheduler-emitted, and the payload variables
// available for conditions and {{templates}}.
func (h *Handler) ListWorkflowEvents(c *gin.Context) {
	type eventInfo struct {
		Event     string   `json:"event"`
		Category  string   `json:"category"`
		Scheduled bool     `json:"scheduled"`
		Variables []string `json:"variables"`
	}
	events := make([]eventInfo, 0, len(workflowEventCatalog))
	for name, def := range workflowEventCatalog {
		vars := make([]string, 0, len(def.SampleData))
		for k := range def.SampleData {
			if k != "record_id" {
				vars = append(vars, k)
			}
		}
		sort.Strings(vars)
		events = append(events, eventInfo{Event: name, Category: def.Category, Scheduled: def.Scheduled, Variables: vars})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Category != events[j].Category {
			return events[i].Category < events[j].Category
		}
		return events[i].Event < events[j].Event
	})
	response.Success(c, events)
}

// TestWorkflowRule dry-runs a rule: evaluates its conditions against sample
// (or provided) data and reports what each action WOULD do — no side effects.
func (h *Handler) TestWorkflowRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid rule ID")
		return
	}

	var triggerEvent sql.NullString
	var conditionsJSON, actionsJSON json.RawMessage
	err = h.db.QueryRow(`
		SELECT trigger_event, conditions, actions FROM workflow_rules
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&triggerEvent, &conditionsJSON, &actionsJSON)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Workflow rule")
		return
	}
	if err != nil {
		h.log.Error("Failed to load workflow rule for test", "error", err)
		response.InternalError(c, "Failed to test workflow rule")
		return
	}

	// Sample payload from the catalog, overridable from the request body
	data := map[string]interface{}{}
	if def, ok := workflowEventCatalog[triggerEvent.String]; ok {
		for k, v := range def.SampleData {
			data[k] = v
		}
	}
	var body struct {
		Data map[string]interface{} `json:"data"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&body); err == nil {
			for k, v := range body.Data {
				data[k] = v
			}
		}
	}

	matched, condResults := evaluateWorkflowConditions(conditionsJSON, data)

	type actionPreview struct {
		Type    string `json:"type"`
		Preview string `json:"preview"`
		Error   string `json:"error,omitempty"`
	}
	previews := []actionPreview{}
	var actions []WorkflowAction
	if err := json.Unmarshal(actionsJSON, &actions); err == nil {
		for _, a := range actions {
			p := actionPreview{Type: a.Type}
			switch a.Type {
			case "create_notification":
				msg, _ := a.Config["message"].(string)
				title, _ := a.Config["title"].(string)
				recipients, rErr := h.resolveWorkflowRecipients(tenantID, a.Config)
				if rErr != nil {
					p.Error = rErr.Error()
				}
				p.Preview = fmt.Sprintf("%s — %s (→ %d recipient(s))",
					renderWorkflowTemplate(title, data), renderWorkflowTemplate(msg, data), len(recipients))
			case "create_task", "create_followup_task":
				title, _ := a.Config["title"].(string)
				boardID, _ := a.Config["board_id"].(string)
				var boardName string
				if boardID != "" {
					h.db.QueryRow(`SELECT name FROM task_boards WHERE id = $1 AND tenant_id = $2`, boardID, tenantID).Scan(&boardName)
				}
				if boardName == "" {
					p.Error = "board not found"
				}
				p.Preview = fmt.Sprintf("Task: %s → %s", renderWorkflowTemplate(title, data), boardName)
			case "update_field":
				target, _ := a.Config["target"].(string)
				field, _ := a.Config["field"].(string)
				p.Preview = fmt.Sprintf("%s.%s = %v", target, field, renderWorkflowTemplate(fmt.Sprintf("%v", a.Config["value"]), data))
			case "update_status":
				p.Preview = fmt.Sprintf("%v.status = %v", a.Config["table"], a.Config["status"])
			case "send_telegram":
				p.Error = "telegram_not_configured"
			default:
				p.Preview = a.Type
			}
			previews = append(previews, p)
		}
	}

	response.Success(c, gin.H{
		"matched":           matched,
		"sample_data":       data,
		"condition_results": condResults,
		"actions":           previews,
	})
}

// DuplicateWorkflowRule copies a rule (inactive) so it can be tweaked safely.
func (h *Handler) DuplicateWorkflowRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	claims, _ := middleware.GetClaims(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid rule ID")
		return
	}

	newID := uuid.New()
	err = h.db.QueryRow(`
		INSERT INTO workflow_rules (id, tenant_id, name, description, category, trigger_type,
			trigger_event, conditions, actions, is_active, priority, created_by, created_at, updated_at)
		SELECT $1, tenant_id, name || ' (nusxa)', description, category, trigger_type,
			trigger_event, conditions, actions, false, priority, $2, NOW(), NOW()
		FROM workflow_rules WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
		RETURNING id
	`, newID, claims.UserID, id, tenantID).Scan(&newID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Workflow rule")
		return
	}
	if err != nil {
		h.log.Error("Failed to duplicate workflow rule", "error", err)
		response.InternalError(c, "Failed to duplicate workflow rule")
		return
	}

	response.Created(c, gin.H{"id": newID.String()})
}

// RetryWorkflowLog re-executes a failed/partial run's actions with the stored
// trigger payload and writes a fresh log entry.
func (h *Handler) RetryWorkflowLog(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid log ID")
		return
	}

	var ruleID uuid.UUID
	var ruleName string
	var triggerEvent sql.NullString
	var triggerData, conditionsJSON, actionsJSON json.RawMessage
	var createdBy sql.NullString
	err = h.db.QueryRow(`
		SELECT wl.rule_id, wr.name, wl.trigger_event, wl.trigger_data, wr.conditions, wr.actions, wr.created_by
		FROM workflow_logs wl
		JOIN workflow_rules wr ON wr.id = wl.rule_id
		WHERE wl.id = $1 AND wl.tenant_id = $2 AND wr.deleted_at IS NULL
	`, id, tenantID).Scan(&ruleID, &ruleName, &triggerEvent, &triggerData, &conditionsJSON, &actionsJSON, &createdBy)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Workflow log")
		return
	}
	if err != nil {
		h.log.Error("Failed to load workflow log for retry", "error", err)
		response.InternalError(c, "Failed to retry workflow run")
		return
	}

	var data map[string]interface{}
	if err := json.Unmarshal(triggerData, &data); err != nil {
		response.BadRequest(c, "Stored trigger data is not replayable")
		return
	}
	data["retried_from"] = id.String()

	var creator *uuid.UUID
	if createdBy.Valid {
		if uid, err := uuid.Parse(createdBy.String); err == nil {
			creator = &uid
		}
	}

	ctx := workflowEventCtx{TenantID: tenantID, Event: triggerEvent.String, Data: data}
	h.executeWorkflowRule(ctx, ruleID, ruleName, conditionsJSON, actionsJSON, creator)

	// Return the fresh log entry for immediate display
	var newLog entity.WorkflowLogResponse
	var errorMsg sql.NullString
	var durationMs sql.NullInt64
	var executedAt time.Time
	err = h.db.QueryRow(`
		SELECT id, status, error_message, duration_ms, executed_at
		FROM workflow_logs
		WHERE tenant_id = $1 AND rule_id = $2
		ORDER BY executed_at DESC LIMIT 1
	`, tenantID, ruleID).Scan(&newLog.ID, &newLog.Status, &errorMsg, &durationMs, &executedAt)
	if err != nil {
		response.Success(c, gin.H{"status": "executed"})
		return
	}
	if errorMsg.Valid {
		newLog.ErrorMessage = &errorMsg.String
	}
	if durationMs.Valid {
		d := int(durationMs.Int64)
		newLog.DurationMs = &d
	}
	newLog.ExecutedAt = executedAt.Format(time.RFC3339)
	response.Success(c, newLog)
}

// ===== Engine types =====
// The evaluation engine itself lives in workflow_engine.go.

// WorkflowAction represents a single action to execute
type WorkflowAction struct {
	Type   string                 `json:"type"`   // create_notification, create_task, update_field, send_telegram
	Config map[string]interface{} `json:"config"` // action-specific configuration
}

// ===== Scheduled trigger scans =====
//
// Time-based triggers (overdue invoice/task, expiring contract, low stock)
// are not events — nothing "happens" at the overdue moment, so a periodic
// scan emits them. Every emission goes through the fired-marker dedupe in
// workflow_engine.go so a rule fires once per record, not once per scan.

// CheckThresholdRules scans all tenants that have active rules on scheduled
// events and emits the matching events with per-record dedupe.
func (h *Handler) CheckThresholdRules() {
	rows, err := h.db.Query(`
		SELECT DISTINCT tenant_id, trigger_event FROM workflow_rules
		WHERE trigger_event IN ('inventory.low_stock', 'inventory.transfer_stuck', 'invoice.overdue', 'task.overdue', 'contracts.expiring_soon', 'lead.stale')
		  AND is_active = true AND deleted_at IS NULL
	`)
	if err != nil {
		h.log.Error("Failed to query tenants for scheduled workflow rules", "error", err)
		return
	}
	defer rows.Close()

	type scan struct {
		tenantID uuid.UUID
		event    string
	}
	var scans []scan
	for rows.Next() {
		var s scan
		if err := rows.Scan(&s.tenantID, &s.event); err == nil {
			scans = append(scans, s)
		}
	}
	rows.Close()

	for _, s := range scans {
		switch s.event {
		case "inventory.low_stock":
			h.checkInventoryThresholds(s.tenantID)
		case "inventory.transfer_stuck":
			h.checkStuckTransfers(s.tenantID)
		case "invoice.overdue":
			h.checkOverdueInvoices(s.tenantID)
		case "task.overdue":
			h.checkOverdueTaskEvents(s.tenantID)
		case "contracts.expiring_soon":
			h.checkExpiringContracts(s.tenantID)
		case "lead.stale":
			h.checkStaleLeads(s.tenantID)
		}
	}
}

// checkStuckTransfers emits inventory.transfer_stuck for intercompany
// transfers sitting in_transit for more than 3 days — that stock is out of
// the source warehouse and not yet in the destination, so nobody's on-hand
// includes it. Re-fires per transfer at most once per 24h.
func (h *Handler) checkStuckTransfers(tenantID uuid.UUID) {
	rows, err := h.db.Query(`
		SELECT t.id, COALESCE(t.transfer_number, ''),
		       COALESCE(wf.name, ''), COALESCE(wt.name, ''),
		       EXTRACT(DAY FROM NOW() - t.shipped_at)::int AS days_in_transit
		FROM intercompany_transfers t
		LEFT JOIN warehouses wf ON wf.id = t.from_warehouse_id
		LEFT JOIN warehouses wt ON wt.id = t.to_warehouse_id
		WHERE t.tenant_id = $1 AND t.deleted_at IS NULL
		  AND t.status = 'in_transit'
		  AND t.shipped_at < NOW() - INTERVAL '3 days'
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to check stuck transfers", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var transferID uuid.UUID
		var transferNumber, fromWH, toWH string
		var days int
		if err := rows.Scan(&transferID, &transferNumber, &fromWH, &toWH, &days); err != nil {
			continue
		}
		h.runWorkflowEvent(workflowEventCtx{
			TenantID: tenantID,
			Event:    "inventory.transfer_stuck",
			Data: map[string]interface{}{
				"record_id":       transferID.String(),
				"transfer_number": transferNumber,
				"from_warehouse":  fromWH,
				"to_warehouse":    toWH,
				"days_in_transit": days,
			},
			DedupeKey: transferID.String(),
			Cooldown:  24 * time.Hour,
		})
	}
}

// checkInventoryThresholds emits inventory.low_stock for products at/below
// their reorder point. Re-fires per product at most once per 24h.
func (h *Handler) checkInventoryThresholds(tenantID uuid.UUID) {
	rows, err := h.db.Query(`
		SELECT p.id, p.name, p.code, p.reorder_point,
			   COALESCE(SUM(i.quantity_available), 0) as available
		FROM products p
		LEFT JOIN inventory i ON p.id = i.product_id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL AND p.is_active = true
			  AND p.track_inventory = true AND p.reorder_point > 0
		GROUP BY p.id, p.name, p.code, p.reorder_point
		HAVING COALESCE(SUM(i.quantity_available), 0) <= p.reorder_point
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to check inventory thresholds", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var productID uuid.UUID
		var productName, productCode string
		var reorderPoint, available float64
		if err := rows.Scan(&productID, &productName, &productCode, &reorderPoint, &available); err != nil {
			continue
		}
		h.runWorkflowEvent(workflowEventCtx{
			TenantID: tenantID,
			Event:    "inventory.low_stock",
			Data: map[string]interface{}{
				"record_id":     productID.String(),
				"product_id":    productID.String(),
				"product_name":  productName,
				"product_code":  productCode,
				"reorder_point": reorderPoint,
				"available":     available,
			},
			DedupeKey: productID.String(),
			Cooldown:  24 * time.Hour,
		})
	}
}

// checkOverdueInvoices emits invoice.overdue once per invoice+rule (30-day
// re-fire window covers the partial-payment → overdue-again edge case).
func (h *Handler) checkOverdueInvoices(tenantID uuid.UUID) {
	rows, err := h.db.Query(`
		SELECT id, invoice_number, customer_name, total_amount, due_date
		FROM sales_invoices
		WHERE tenant_id = $1 AND status = 'sent' AND due_date < $2 AND deleted_at IS NULL
	`, tenantID, time.Now())
	if err != nil {
		h.log.Error("Failed to check overdue invoices", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var invoiceID uuid.UUID
		var invoiceNumber, customerName string
		var totalAmount float64
		var dueDate time.Time
		if err := rows.Scan(&invoiceID, &invoiceNumber, &customerName, &totalAmount, &dueDate); err != nil {
			continue
		}
		h.runWorkflowEvent(workflowEventCtx{
			TenantID: tenantID,
			Event:    "invoice.overdue",
			Data: map[string]interface{}{
				"record_id":      invoiceID.String(),
				"invoice_number": invoiceNumber,
				"customer_name":  customerName,
				"total_amount":   totalAmount,
				"due_date":       dueDate.Format("2006-01-02"),
				"days_overdue":   int(time.Since(dueDate).Hours() / 24),
			},
			DedupeKey: invoiceID.String(),
			Cooldown:  30 * 24 * time.Hour,
		})
	}
}

// checkOverdueTaskEvents emits task.overdue for open Vazifalar tasks past
// their due date. The marker key includes the due date, so a rescheduled task
// fires again when it becomes newly overdue.
func (h *Handler) checkOverdueTaskEvents(tenantID uuid.UUID) {
	rows, err := h.db.Query(`
		SELECT t.id, t.title, t.due_date, b.id, b.name
		FROM tasks t
		JOIN task_boards b ON b.id = t.board_id
		WHERE t.tenant_id = $1 AND t.due_date < CURRENT_DATE
		  AND t.completed_at IS NULL AND t.archived_at IS NULL AND b.archived_at IS NULL
		LIMIT 500
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to check overdue tasks for workflows", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var taskID, boardID uuid.UUID
		var title, boardName string
		var dueDate time.Time
		if err := rows.Scan(&taskID, &title, &dueDate, &boardID, &boardName); err != nil {
			continue
		}
		dueStr := dueDate.Format("2006-01-02")
		h.runWorkflowEvent(workflowEventCtx{
			TenantID: tenantID,
			Event:    "task.overdue",
			Data: map[string]interface{}{
				"record_id":    taskID.String(),
				"task_title":   title,
				"board_id":     boardID.String(),
				"board_name":   boardName,
				"due_date":     dueStr,
				"days_overdue": int(time.Since(dueDate).Hours() / 24),
			},
			DedupeKey: taskID.String() + ":" + dueStr,
			Cooldown:  0, // one-shot per task+due_date
		})
	}
}

// contractExpiryThresholds are the "N days before end_date" marks at which
// contracts.expiring_soon fires — once per contract per threshold.
var contractExpiryThresholds = []int{30, 14, 3}

// checkExpiringContracts emits contracts.expiring_soon for active contracts
// approaching end_date. Reads procurement_contracts (the Shartnomalar module
// table — the pre-443 version scanned the orphaned legacy `contracts` table,
// so UI-created contracts were never seen). One-shot per
// contract+end_date+threshold via the fired-marker dedupe.
func (h *Handler) checkExpiringContracts(tenantID uuid.UUID) {
	rows, err := h.db.Query(`
		SELECT c.id, c.contract_number, COALESCE(v.name, c.vendor_name, ''), c.end_date,
		       (c.end_date - CURRENT_DATE) as days_to_expiry
		FROM procurement_contracts c
		LEFT JOIN contacts v ON v.id = c.vendor_id AND v.tenant_id = c.tenant_id
		WHERE c.tenant_id = $1 AND c.deleted_at IS NULL AND c.archived_at IS NULL
		  AND c.end_date IS NOT NULL
		  AND c.end_date >= CURRENT_DATE
		  AND c.end_date <= CURRENT_DATE + INTERVAL '30 days'
		  AND c.status = 'active'
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to check expiring contracts for workflows", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var contractID uuid.UUID
		var contractNumber, contactName string
		var endDate time.Time
		var daysToExpiry int
		if err := rows.Scan(&contractID, &contractNumber, &contactName, &endDate, &daysToExpiry); err != nil {
			continue
		}
		endStr := endDate.Format("2006-01-02")
		for _, threshold := range contractExpiryThresholds {
			if daysToExpiry > threshold {
				continue
			}
			h.runWorkflowEvent(workflowEventCtx{
				TenantID: tenantID,
				Event:    "contracts.expiring_soon",
				Data: map[string]interface{}{
					"record_id":       contractID.String(),
					"contract_number": contractNumber,
					"contact_name":    contactName,
					"end_date":        endStr,
					"days_to_expiry":  daysToExpiry,
					"threshold_days":  threshold,
				},
				DedupeKey: contractID.String() + ":" + endStr + ":" + strconv.Itoa(threshold),
				Cooldown:  0, // one-shot per contract+end_date+threshold
			})
		}
	}
}

// markExpiredContracts flips active contracts past their end_date to
// 'expired' (all tenants) and emits contracts.expired for each. Runs from
// the workflow scheduler tick; the status change happens regardless of
// whether any automation rule listens.
func (h *Handler) markExpiredContracts() {
	rows, err := h.db.Query(`
		UPDATE procurement_contracts c
		SET status = 'expired', updated_at = NOW()
		WHERE c.status = 'active' AND c.deleted_at IS NULL
		  AND c.end_date IS NOT NULL AND c.end_date < CURRENT_DATE
		RETURNING c.id, c.tenant_id, c.contract_number, COALESCE(c.vendor_name, ''), c.end_date
	`)
	if err != nil {
		h.log.Error("Failed to mark expired contracts", "error", err)
		return
	}
	defer rows.Close()

	type expired struct {
		id             uuid.UUID
		tenantID       uuid.UUID
		contractNumber string
		contactName    string
		endDate        time.Time
	}
	var items []expired
	for rows.Next() {
		var e expired
		if err := rows.Scan(&e.id, &e.tenantID, &e.contractNumber, &e.contactName, &e.endDate); err == nil {
			items = append(items, e)
		}
	}
	rows.Close()

	for _, e := range items {
		endStr := e.endDate.Format("2006-01-02")
		h.contractAudit(e.tenantID, uuid.Nil, e.id, "status_change",
			map[string]interface{}{"status": "active"},
			map[string]interface{}{"status": "expired", "by": "scheduler"})
		h.runWorkflowEvent(workflowEventCtx{
			TenantID: e.tenantID,
			Event:    "contracts.expired",
			Data: map[string]interface{}{
				"record_id":       e.id.String(),
				"contract_number": e.contractNumber,
				"contact_name":    e.contactName,
				"end_date":        endStr,
			},
			DedupeKey: e.id.String() + ":" + endStr,
			Cooldown:  0, // one-shot per contract+end_date
		})
	}
	if len(items) > 0 {
		h.log.Info("Marked expired contracts", "count", len(items))
	}
}

// cleanupWorkflowLogs enforces the retention policy (logs 90 days, markers
// 400 days). Called once per scheduler day.
func (h *Handler) cleanupWorkflowLogs() {
	if _, err := h.db.Exec(`DELETE FROM workflow_logs WHERE executed_at < NOW() - make_interval(secs => $1)`,
		workflowLogRetention.Seconds()); err != nil {
		h.log.Error("Workflow log retention cleanup failed", "error", err)
	}
	if _, err := h.db.Exec(`DELETE FROM workflow_fired_markers WHERE fired_at < NOW() - make_interval(secs => $1)`,
		markerRetention.Seconds()); err != nil {
		h.log.Error("Workflow marker cleanup failed", "error", err)
	}
}

// RunWorkflowScheduler starts the background ticker for scheduled-event scans,
// auto-replenishment and log retention. Runs an initial pass shortly after
// boot so a restart doesn't delay overdue checks by a full interval.
func (h *Handler) RunWorkflowScheduler(ctx context.Context, interval time.Duration) {
	go func() {
		runAll := func() {
			defer func() {
				if r := recover(); r != nil {
					h.log.Error("Workflow scheduler panic recovered", "panic", fmt.Sprintf("%v", r))
				}
			}()
			h.CheckThresholdRules()
			h.markExpiredContracts()
			h.autoRunReplenishment()
		}

		select {
		case <-time.After(30 * time.Second):
			runAll()
			h.cleanupWorkflowLogs()
		case <-ctx.Done():
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastCleanup := time.Now()
		for {
			select {
			case <-ticker.C:
				runAll()
				if time.Since(lastCleanup) > 24*time.Hour {
					h.cleanupWorkflowLogs()
					lastCleanup = time.Now()
				}
			case <-ctx.Done():
				h.log.Info("Workflow scheduler stopped")
				return
			}
		}
	}()
	h.log.Info("Workflow scheduler started", "interval", interval)
}

// autoRunReplenishment checks all tenants for active reorder rules with auto_create_po=true
// and creates draft purchase orders when stock falls below min_qty.
func (h *Handler) autoRunReplenishment() {
	// Find all tenants with active auto-PO reorder rules
	tenantRows, err := h.db.Query(`
		SELECT DISTINCT tenant_id FROM reorder_rules
		WHERE is_active = true AND auto_create_po = true
	`)
	if err != nil {
		h.log.Error("Auto replenishment: failed to query tenants", "error", err)
		return
	}
	defer tenantRows.Close()

	var tenantIDs []uuid.UUID
	for tenantRows.Next() {
		var tid uuid.UUID
		if err := tenantRows.Scan(&tid); err == nil {
			tenantIDs = append(tenantIDs, tid)
		}
	}

	for _, tenantID := range tenantIDs {
		h.autoReplenishForTenant(tenantID)
	}
}

// autoReplenishForTenant creates purchase orders for a single tenant's auto reorder rules.
func (h *Handler) autoReplenishForTenant(tenantID uuid.UUID) {
	now := time.Now()

	// Query rules where stock <= min_qty, auto_create_po=true,
	// and not triggered within the last hour (dedup)
	rows, err := h.db.Query(`
		SELECT r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty,
			   r.preferred_vendor_id, r.lead_time_days,
			   p.name as product_name,
			   COALESCE(SUM(i.quantity_available), 0) as current_stock
		FROM reorder_rules r
		JOIN products p ON r.product_id = p.id
		LEFT JOIN inventory i ON r.product_id = i.product_id
			AND (r.warehouse_id IS NULL OR r.warehouse_id = i.warehouse_id)
		WHERE r.tenant_id = $1
			AND r.is_active = true
			AND r.auto_create_po = true
			AND (r.last_triggered_at IS NULL OR r.last_triggered_at < NOW() - INTERVAL '1 hour')
		GROUP BY r.id, r.product_id, r.warehouse_id, r.min_qty, r.max_qty, r.reorder_qty,
				 r.preferred_vendor_id, r.lead_time_days, p.name
		HAVING COALESCE(SUM(i.quantity_available), 0) <= r.min_qty
		ORDER BY r.preferred_vendor_id NULLS LAST, p.name
	`, tenantID)
	if err != nil {
		h.log.Error("Auto replenishment: failed to query rules", "error", err, "tenant_id", tenantID)
		return
	}
	defer rows.Close()

	type reorderItem struct {
		RuleID      uuid.UUID
		ProductID   uuid.UUID
		ProductName string
		WarehouseID *uuid.UUID
		MinQty      float64
		MaxQty      float64
		ReorderQty  float64
		VendorID    *uuid.UUID
		LeadDays    int
		Stock       float64
		OrderQty    float64
	}

	var items []reorderItem
	for rows.Next() {
		var item reorderItem
		var warehouseID, vendorID sql.NullString
		var maxQty sql.NullFloat64

		if err := rows.Scan(&item.RuleID, &item.ProductID, &warehouseID, &item.MinQty, &maxQty,
			&item.ReorderQty, &vendorID, &item.LeadDays, &item.ProductName, &item.Stock); err != nil {
			h.log.Error("Auto replenishment: scan error", "error", err)
			continue
		}

		if warehouseID.Valid {
			wid, _ := uuid.Parse(warehouseID.String)
			item.WarehouseID = &wid
		}
		if vendorID.Valid {
			vid, _ := uuid.Parse(vendorID.String)
			item.VendorID = &vid
		}
		if maxQty.Valid {
			item.MaxQty = maxQty.Float64
		}

		// Calculate order quantity
		if item.MaxQty > 0 {
			item.OrderQty = item.MaxQty - item.Stock
		} else {
			item.OrderQty = item.ReorderQty
		}
		if item.OrderQty < item.ReorderQty {
			item.OrderQty = item.ReorderQty
		}

		items = append(items, item)
	}

	if len(items) == 0 {
		return
	}

	// Get organization_id for this tenant
	var orgID *uuid.UUID
	var oid uuid.UUID
	if err := h.db.QueryRow(`SELECT id FROM organizations WHERE tenant_id = $1 LIMIT 1`, tenantID).Scan(&oid); err == nil {
		orgID = &oid
	}

	// Get a system user (first admin) for requested_by
	var systemUserID uuid.UUID
	h.db.QueryRow(`SELECT id FROM users WHERE tenant_id = $1 AND is_active = true ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&systemUserID)

	// Group items by vendor
	vendorGroups := make(map[string][]reorderItem)
	var noVendorItems []reorderItem
	for _, item := range items {
		if item.VendorID != nil {
			vendorGroups[item.VendorID.String()] = append(vendorGroups[item.VendorID.String()], item)
		} else {
			noVendorItems = append(noVendorItems, item)
		}
	}

	ordersCreated := 0

	// Create PO per vendor
	for vidStr, group := range vendorGroups {
		vid, _ := uuid.Parse(vidStr)
		poID := uuid.New()
		orderNumber := fmt.Sprintf("PO-%s-%04d", now.Format("20060102"), ordersCreated+1)

		// Try to get a proper sequential number
		h.db.QueryRow(`
			SELECT 'PO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-' || LPAD(
				(COALESCE(MAX(CAST(SUBSTRING(order_number FROM 'PO-[0-9]+-([0-9]+)') AS INTEGER)), 0) + 1)::TEXT, 4, '0')
			FROM purchase_orders WHERE tenant_id = $1 AND order_number LIKE 'PO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-%'
		`, tenantID).Scan(&orderNumber)

		_, err := h.db.Exec(`
			INSERT INTO purchase_orders (
				id, tenant_id, organization_id, order_number, vendor_id, order_date, status, payment_status,
				subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
				exchange_rate, requested_by, notes, is_auto_replenishment, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,'draft','unpaid',0,0,0,0,0,1.0,$7,'Avtomatik qayta buyurtma',true,$8,$8)
		`, poID, tenantID, orgID, orderNumber, vid, now, systemUserID, now)
		if err != nil {
			h.log.Error("Auto replenishment: failed to create PO", "error", err, "vendor_id", vidStr)
			continue
		}

		// Batch: collect all product IDs for this vendor group
		productIDs := make([]uuid.UUID, len(group))
		for i, item := range group {
			productIDs[i] = item.ProductID
		}

		// ONE query: get all vendor prices for these products
		priceMap := make(map[uuid.UUID]float64)
		vpRows, vpErr := h.db.Query(
			`SELECT product_id, price FROM vendor_prices WHERE vendor_id = $1 AND product_id = ANY($2) AND tenant_id = $3
			 ORDER BY product_id, created_at DESC`,
			vid, pq.Array(productIDs), tenantID)
		if vpErr == nil {
			for vpRows.Next() {
				var pid uuid.UUID
				var price float64
				if err := vpRows.Scan(&pid, &price); err == nil {
					if _, exists := priceMap[pid]; !exists {
						priceMap[pid] = price
					}
				}
			}
			vpRows.Close()
		}

		// ONE query: get fallback purchase prices for products missing from vendor_prices
		var missingPIDs []uuid.UUID
		for _, pid := range productIDs {
			if _, ok := priceMap[pid]; !ok {
				missingPIDs = append(missingPIDs, pid)
			}
		}
		if len(missingPIDs) > 0 {
			ppRows, ppErr := h.db.Query(`SELECT id, COALESCE(purchase_price, 0) FROM products WHERE id = ANY($1)`, pq.Array(missingPIDs))
			if ppErr == nil {
				for ppRows.Next() {
					var pid uuid.UUID
					var price float64
					if err := ppRows.Scan(&pid, &price); err == nil {
						priceMap[pid] = price
					}
				}
				ppRows.Close()
			}
		}

		// Build bulk INSERT for purchase_order_lines and collect rule IDs
		var subtotal float64
		var lineValues []string
		var lineArgs []interface{}
		var ruleIDs []uuid.UUID
		argIdx := 0
		for lineNum, item := range group {
			unitPrice := priceMap[item.ProductID]
			lineTotal := item.OrderQty * unitPrice
			subtotal += lineTotal
			ruleIDs = append(ruleIDs, item.RuleID)

			lineID := uuid.New()
			lineValues = append(lineValues, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,0,0,$%d,$%d,0,0,$%d,$%d,$%d)",
				argIdx+1, argIdx+2, argIdx+3, argIdx+4, argIdx+5, argIdx+6, argIdx+7, argIdx+8, argIdx+9, argIdx+10, argIdx+11, argIdx+12))
			lineArgs = append(lineArgs, lineID, poID, lineNum+1, item.ProductID, item.ProductName, item.OrderQty,
				unitPrice, lineTotal, item.WarehouseID, item.RuleID, now, now)
			argIdx += 12
		}

		// ONE INSERT for all purchase_order_lines
		if len(lineValues) > 0 {
			h.db.Exec(`
				INSERT INTO purchase_order_lines (
					id, purchase_order_id, line_number, product_id, description, quantity,
					unit_price, discount_amount, tax_amount, line_total, warehouse_id,
					quantity_received, quantity_invoiced, reorder_rule_id, created_at, updated_at
				) VALUES `+strings.Join(lineValues, ","), lineArgs...)
		}

		// ONE UPDATE for all reorder_rules
		if len(ruleIDs) > 0 {
			h.db.Exec(`UPDATE reorder_rules SET last_triggered_at=$1, updated_at=$1 WHERE id = ANY($2)`, now, pq.Array(ruleIDs))
		}

		h.db.Exec(`UPDATE purchase_orders SET subtotal=$1, total_amount=$1, updated_at=$2 WHERE id=$3`, subtotal, now, poID)
		ordersCreated++
		h.log.Info("Auto replenishment: created PO", "order_number", orderNumber, "vendor_id", vidStr, "lines", len(group), "tenant_id", tenantID)
	}

	// Items without vendor — create a single PO
	if len(noVendorItems) > 0 {
		poID := uuid.New()
		orderNumber := fmt.Sprintf("PO-%s-%04d", now.Format("20060102"), ordersCreated+1)
		h.db.QueryRow(`
			SELECT 'PO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-' || LPAD(
				(COALESCE(MAX(CAST(SUBSTRING(order_number FROM 'PO-[0-9]+-([0-9]+)') AS INTEGER)), 0) + 1)::TEXT, 4, '0')
			FROM purchase_orders WHERE tenant_id = $1 AND order_number LIKE 'PO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-%'
		`, tenantID).Scan(&orderNumber)

		_, err := h.db.Exec(`
			INSERT INTO purchase_orders (
				id, tenant_id, organization_id, order_number, vendor_id, order_date, status, payment_status,
				subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
				exchange_rate, requested_by, notes, is_auto_replenishment, created_at, updated_at
			) VALUES ($1,$2,$3,$4,NULL,$5,'draft','unpaid',0,0,0,0,0,1.0,$6,'Avtomatik qayta buyurtma (yetkazuvchisiz)',true,$7,$7)
		`, poID, tenantID, orgID, orderNumber, now, systemUserID, now)
		if err != nil {
			h.log.Error("Auto replenishment: failed to create no-vendor PO", "error", err)
		} else {
			// ONE query: get all purchase prices for no-vendor products
			nvProductIDs := make([]uuid.UUID, len(noVendorItems))
			for i, item := range noVendorItems {
				nvProductIDs[i] = item.ProductID
			}
			nvPriceMap := make(map[uuid.UUID]float64)
			nvPPRows, nvPPErr := h.db.Query(`SELECT id, COALESCE(purchase_price, 0) FROM products WHERE id = ANY($1)`, pq.Array(nvProductIDs))
			if nvPPErr == nil {
				for nvPPRows.Next() {
					var pid uuid.UUID
					var price float64
					if err := nvPPRows.Scan(&pid, &price); err == nil {
						nvPriceMap[pid] = price
					}
				}
				nvPPRows.Close()
			}

			// Build bulk INSERT for purchase_order_lines
			var subtotal float64
			var nvLineValues []string
			var nvLineArgs []interface{}
			var nvRuleIDs []uuid.UUID
			nvArgIdx := 0
			for lineNum, item := range noVendorItems {
				unitPrice := nvPriceMap[item.ProductID]
				lineTotal := item.OrderQty * unitPrice
				subtotal += lineTotal
				nvRuleIDs = append(nvRuleIDs, item.RuleID)

				lineID := uuid.New()
				nvLineValues = append(nvLineValues, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,0,0,$%d,$%d,0,0,$%d,$%d,$%d)",
					nvArgIdx+1, nvArgIdx+2, nvArgIdx+3, nvArgIdx+4, nvArgIdx+5, nvArgIdx+6, nvArgIdx+7, nvArgIdx+8, nvArgIdx+9, nvArgIdx+10, nvArgIdx+11, nvArgIdx+12))
				nvLineArgs = append(nvLineArgs, lineID, poID, lineNum+1, item.ProductID, item.ProductName, item.OrderQty,
					unitPrice, lineTotal, item.WarehouseID, item.RuleID, now, now)
				nvArgIdx += 12
			}

			// ONE INSERT for all purchase_order_lines
			if len(nvLineValues) > 0 {
				h.db.Exec(`
					INSERT INTO purchase_order_lines (
						id, purchase_order_id, line_number, product_id, description, quantity,
						unit_price, discount_amount, tax_amount, line_total, warehouse_id,
						quantity_received, quantity_invoiced, reorder_rule_id, created_at, updated_at
					) VALUES `+strings.Join(nvLineValues, ","), nvLineArgs...)
			}

			// ONE UPDATE for all reorder_rules
			if len(nvRuleIDs) > 0 {
				h.db.Exec(`UPDATE reorder_rules SET last_triggered_at=$1, updated_at=$1 WHERE id = ANY($2)`, now, pq.Array(nvRuleIDs))
			}

			h.db.Exec(`UPDATE purchase_orders SET subtotal=$1, total_amount=$1, updated_at=$2 WHERE id=$3`, subtotal, now, poID)
			ordersCreated++
			h.log.Info("Auto replenishment: created no-vendor PO", "order_number", orderNumber, "lines", len(noVendorItems), "tenant_id", tenantID)
		}
	}

	if ordersCreated > 0 {
		h.log.Info("Auto replenishment: completed", "tenant_id", tenantID, "orders_created", ordersCreated, "total_items", len(items))
	}
}
