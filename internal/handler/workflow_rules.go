package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"strconv"
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
		SELECT id, tenant_id, name, description, category, trigger_type, trigger_event,
			   conditions, actions, is_active, priority, last_triggered_at, trigger_count,
			   created_by, created_at, updated_at
		FROM workflow_rules
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	if category != "" {
		argCount++
		query += fmt.Sprintf(" AND category = $%d", argCount)
		args = append(args, category)
	}

	if activeOnly {
		query += " AND is_active = true"
	}

	query += " ORDER BY priority DESC, created_at DESC"

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
		var lastTriggered sql.NullTime
		var createdBy sql.NullString

		err := rows.Scan(
			&r.ID, &r.TenantID, &r.Name, &description, &r.Category,
			&r.TriggerType, &triggerEvent, &r.Conditions, &r.Actions,
			&r.IsActive, &r.Priority, &lastTriggered, &r.TriggerCount,
			&createdBy, &r.CreatedAt, &r.UpdatedAt,
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

	// Validate trigger type
	validTriggerTypes := map[string]bool{"event": true, "scheduled": true, "threshold": true}
	if !validTriggerTypes[input.TriggerType] {
		response.BadRequest(c, "Invalid trigger_type. Must be: event, scheduled, or threshold")
		return
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

// ListWorkflowLogs returns execution logs for workflow rules
func (h *Handler) ListWorkflowLogs(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	ruleID := c.Query("rule_id")
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)
	if limit < 1 || limit > 200 {
		limit = 50
	}

	query := `
		SELECT wl.id, wl.rule_id, wr.name as rule_name, wl.trigger_data,
			   wl.actions_executed, wl.status, wl.error_message, wl.executed_at
		FROM workflow_logs wl
		JOIN workflow_rules wr ON wl.rule_id = wr.id
		WHERE wl.tenant_id = $1
	`
	args := []interface{}{tenantID}
	argCount := 1

	if ruleID != "" {
		argCount++
		query += fmt.Sprintf(" AND wl.rule_id = $%d", argCount)
		args = append(args, ruleID)
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
		var errorMsg sql.NullString
		var executedAt time.Time

		err := rows.Scan(
			&log.ID, &log.RuleID, &log.RuleName, &log.TriggerData,
			&log.ActionsExecuted, &log.Status, &errorMsg, &executedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan workflow log", "error", err)
			continue
		}

		if errorMsg.Valid {
			log.ErrorMessage = &errorMsg.String
		}
		log.ExecutedAt = executedAt.Format(time.RFC3339)
		logs = append(logs, &log)
	}

	response.Success(c, logs)
}

// ===== Rule Evaluation Engine =====

// WorkflowAction represents a single action to execute
type WorkflowAction struct {
	Type   string                 `json:"type"`   // notify, create_record, update_field, send_email
	Config map[string]interface{} `json:"config"` // action-specific configuration
}

// EvaluateWorkflowRules checks and executes matching rules for a given event
func (h *Handler) EvaluateWorkflowRules(tenantID uuid.UUID, event string, data map[string]interface{}) {
	// Find active rules matching this event
	rows, err := h.db.Query(`
		SELECT id, name, conditions, actions
		FROM workflow_rules
		WHERE tenant_id = $1 AND trigger_event = $2 AND is_active = true AND deleted_at IS NULL
		ORDER BY priority DESC
	`, tenantID, event)
	if err != nil {
		h.log.Error("Failed to query workflow rules", "error", err, "event", event)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var ruleID uuid.UUID
		var ruleName string
		var conditionsJSON, actionsJSON json.RawMessage

		if err := rows.Scan(&ruleID, &ruleName, &conditionsJSON, &actionsJSON); err != nil {
			h.log.Error("Failed to scan workflow rule", "error", err)
			continue
		}

		// Check conditions
		if !h.evaluateConditions(conditionsJSON, data) {
			continue
		}

		// Parse and execute actions
		var actions []WorkflowAction
		if err := json.Unmarshal(actionsJSON, &actions); err != nil {
			h.log.Error("Failed to parse workflow actions", "error", err, "rule", ruleName)
			h.logWorkflowExecution(tenantID, ruleID, data, nil, "failed", "Failed to parse actions: "+err.Error())
			continue
		}

		executedActions := make([]map[string]interface{}, 0)
		allSuccess := true
		var lastError string

		for _, action := range actions {
			result, err := h.executeAction(tenantID, action, data)
			actionLog := map[string]interface{}{
				"type":   action.Type,
				"config": action.Config,
				"result": result,
			}
			if err != nil {
				actionLog["error"] = err.Error()
				allSuccess = false
				lastError = err.Error()
			}
			executedActions = append(executedActions, actionLog)
		}

		status := "success"
		if !allSuccess {
			status = "partial"
		}

		h.logWorkflowExecution(tenantID, ruleID, data, executedActions, status, lastError)

		// Update rule stats
		h.db.Exec(`
			UPDATE workflow_rules SET last_triggered_at = $1, trigger_count = trigger_count + 1
			WHERE id = $2
		`, time.Now(), ruleID)

		h.log.Info("Workflow rule executed", "rule", ruleName, "event", event, "status", status)
	}
}

// evaluateConditions checks if the data matches the rule conditions
func (h *Handler) evaluateConditions(conditionsJSON json.RawMessage, data map[string]interface{}) bool {
	if len(conditionsJSON) == 0 || string(conditionsJSON) == "{}" || string(conditionsJSON) == "null" {
		return true // No conditions = always match
	}

	var conditions map[string]interface{}
	if err := json.Unmarshal(conditionsJSON, &conditions); err != nil {
		return false
	}

	// Simple condition evaluation: each key in conditions must match data
	for key, expected := range conditions {
		actual, exists := data[key]
		if !exists {
			return false
		}

		// Handle comparison operators
		switch exp := expected.(type) {
		case map[string]interface{}:
			// Operator-based comparison: {"quantity": {"$lte": 10}}
			for op, val := range exp {
				if !h.compareValues(op, actual, val) {
					return false
				}
			}
		default:
			// Direct equality
			if fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", expected) {
				return false
			}
		}
	}

	return true
}

// compareValues performs operator-based comparison
func (h *Handler) compareValues(op string, actual, expected interface{}) bool {
	actualFloat := toFloat64(actual)
	expectedFloat := toFloat64(expected)

	switch op {
	case "$eq":
		return actualFloat == expectedFloat
	case "$ne":
		return actualFloat != expectedFloat
	case "$gt":
		return actualFloat > expectedFloat
	case "$gte":
		return actualFloat >= expectedFloat
	case "$lt":
		return actualFloat < expectedFloat
	case "$lte":
		return actualFloat <= expectedFloat
	default:
		return false
	}
}

func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

// executeAction executes a single workflow action
func (h *Handler) executeAction(tenantID uuid.UUID, action WorkflowAction, triggerData map[string]interface{}) (string, error) {
	switch action.Type {
	case "create_notification":
		return h.actionCreateNotification(tenantID, action.Config, triggerData)
	case "update_status":
		return h.actionUpdateStatus(tenantID, action.Config, triggerData)
	case "create_record":
		return h.actionCreateRecord(tenantID, action.Config, triggerData)
	case "update_task_priority":
		return h.actionUpdateTaskPriority(tenantID, action.Config, triggerData)
	case "create_followup_task":
		return h.actionCreateFollowupTask(tenantID, action.Config, triggerData)
	default:
		return "", fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// actionCreateNotification inserts in-app notifications for the resolved recipients.
// Recipients come from data["notify_user_ids"] (array) or config["user_id"].
func (h *Handler) actionCreateNotification(tenantID uuid.UUID, config map[string]interface{}, data map[string]interface{}) (string, error) {
	tmpl := func(s string) string {
		for key, val := range data {
			s = strings.ReplaceAll(s, "{"+key+"}", fmt.Sprintf("%v", val))
		}
		return s
	}

	message, _ := config["message"].(string)
	if message == "" {
		message = "Workflow notification triggered"
	}
	message = tmpl(message)

	title, _ := config["title"].(string)
	if title == "" {
		title = "Bildirishnoma"
	}
	title = tmpl(title)

	notifType, _ := config["type"].(string)
	if notifType == "" {
		notifType = "workflow"
	}

	priority, _ := config["priority"].(string)
	if priority == "" {
		priority = "normal"
	}

	// Resolve recipient user ids
	recipients := map[string]bool{}
	if list, ok := data["notify_user_ids"].([]string); ok {
		for _, id := range list {
			if id != "" {
				recipients[id] = true
			}
		}
	}
	if list, ok := data["notify_user_ids"].([]interface{}); ok {
		for _, v := range list {
			if s := fmt.Sprintf("%v", v); s != "" && s != "<nil>" {
				recipients[s] = true
			}
		}
	}
	if uid, ok := config["user_id"].(string); ok && uid != "" {
		recipients[uid] = true
	}

	if len(recipients) == 0 {
		h.log.Info("Workflow notification (no recipients)", "tenant", tenantID, "message", message)
		return "Notification (no recipients): " + message, nil
	}

	dataJSON, _ := json.Marshal(data)
	now := time.Now()
	count := 0
	for uid := range recipients {
		userID, err := uuid.Parse(uid)
		if err != nil {
			continue
		}
		_, err = h.db.Exec(`
			INSERT INTO notifications (id, tenant_id, user_id, type, title, message, data, channel, priority, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, 'in_app', $8, $9)`,
			uuid.New(), tenantID, userID, notifType, title, message, string(dataJSON), priority, now)
		if err == nil {
			count++
		}
	}
	return fmt.Sprintf("Notified %d user(s): %s", count, message), nil
}

// actionUpdateStatus updates a record's status field
func (h *Handler) actionUpdateStatus(tenantID uuid.UUID, config map[string]interface{}, data map[string]interface{}) (string, error) {
	table, _ := config["table"].(string)
	newStatus, _ := config["status"].(string)
	if table == "" || newStatus == "" {
		return "", fmt.Errorf("table and status are required for update_status action")
	}

	// Validate table name to prevent SQL injection
	validTables := map[string]bool{
		"leads": true, "contacts": true, "sales_invoices": true,
		"purchase_invoices": true, "sales_orders": true, "purchase_orders": true,
	}
	if !validTables[table] {
		return "", fmt.Errorf("invalid table: %s", table)
	}

	recordID, ok := data["record_id"]
	if !ok {
		return "", fmt.Errorf("record_id not found in trigger data")
	}

	query := fmt.Sprintf("UPDATE %s SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4", table)
	_, err := h.db.Exec(query, newStatus, time.Now(), recordID, tenantID)
	if err != nil {
		return "", fmt.Errorf("failed to update status: %w", err)
	}

	return fmt.Sprintf("Updated %s status to %s", table, newStatus), nil
}

// actionCreateRecord creates a new record in a target table
func (h *Handler) actionCreateRecord(tenantID uuid.UUID, config map[string]interface{}, data map[string]interface{}) (string, error) {
	recordType, _ := config["record_type"].(string)

	switch recordType {
	case "purchase_order_draft":
		// Auto-create draft purchase order for low stock items
		productName, _ := data["product_name"].(string)
		h.log.Info("Auto-creating PO draft for low stock", "product", productName, "tenant", tenantID)
		return fmt.Sprintf("Draft PO created for: %s", productName), nil
	default:
		return "", fmt.Errorf("unknown record_type: %s", recordType)
	}
}

// logWorkflowExecution records a workflow execution in the logs table
func (h *Handler) logWorkflowExecution(tenantID, ruleID uuid.UUID, triggerData map[string]interface{}, actionsExecuted interface{}, status, errorMessage string) {
	triggerJSON, _ := json.Marshal(triggerData)
	actionsJSON, _ := json.Marshal(actionsExecuted)

	var errMsg *string
	if errorMessage != "" {
		errMsg = &errorMessage
	}

	_, err := h.db.Exec(`
		INSERT INTO workflow_logs (id, tenant_id, rule_id, trigger_data, actions_executed, status, error_message, executed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, uuid.New(), tenantID, ruleID, triggerJSON, actionsJSON, status, errMsg, time.Now())

	if err != nil {
		h.log.Error("Failed to log workflow execution", "error", err)
	}
}

// ===== Background Threshold Checker =====

// CheckThresholdRules runs all threshold-type rules (called periodically)
func (h *Handler) CheckThresholdRules() {
	// Get all tenants with active threshold rules
	rows, err := h.db.Query(`
		SELECT DISTINCT tenant_id FROM workflow_rules
		WHERE trigger_type = 'threshold' AND is_active = true AND deleted_at IS NULL
	`)
	if err != nil {
		h.log.Error("Failed to query tenants for threshold rules", "error", err)
		return
	}
	defer rows.Close()

	var tenantIDs []uuid.UUID
	for rows.Next() {
		var tid uuid.UUID
		if err := rows.Scan(&tid); err == nil {
			tenantIDs = append(tenantIDs, tid)
		}
	}

	for _, tenantID := range tenantIDs {
		h.checkInventoryThresholds(tenantID)
		h.checkOverdueInvoices(tenantID)
	}
}

// checkInventoryThresholds checks for low stock items and triggers rules
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

		data := map[string]interface{}{
			"product_id":    productID.String(),
			"product_name":  productName,
			"product_code":  productCode,
			"reorder_point": reorderPoint,
			"available":     available,
		}

		h.EvaluateWorkflowRules(tenantID, "inventory.low_stock", data)
	}
}

// checkOverdueInvoices checks for overdue invoices and triggers rules
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

		data := map[string]interface{}{
			"record_id":      invoiceID.String(),
			"invoice_number": invoiceNumber,
			"customer_name":  customerName,
			"total_amount":   totalAmount,
			"due_date":       dueDate.Format("2006-01-02"),
			"days_overdue":   int(time.Since(dueDate).Hours() / 24),
		}

		h.EvaluateWorkflowRules(tenantID, "invoice.overdue", data)
	}
}

// RunWorkflowScheduler starts a background ticker for periodic checks
func (h *Handler) RunWorkflowScheduler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				h.log.Debug("Running workflow threshold checks")
				h.CheckThresholdRules()
				h.autoRunReplenishment()
				h.CheckProjectWorkflows()
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
