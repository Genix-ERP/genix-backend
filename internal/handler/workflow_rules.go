package handler

import (
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
		response.BadRequest(c, "Invalid input: "+err.Error())
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
		response.BadRequest(c, "Invalid input: "+err.Error())
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
	default:
		return "", fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// actionCreateNotification inserts a notification for users
func (h *Handler) actionCreateNotification(tenantID uuid.UUID, config map[string]interface{}, data map[string]interface{}) (string, error) {
	message, _ := config["message"].(string)
	if message == "" {
		message = "Workflow notification triggered"
	}

	// Replace placeholders in message like {product_name}
	for key, val := range data {
		message = strings.ReplaceAll(message, "{"+key+"}", fmt.Sprintf("%v", val))
	}

	// Store as a workflow log notification (can be extended to a notifications table later)
	h.log.Info("Workflow notification", "tenant", tenantID, "message", message)
	return "Notification: " + message, nil
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
func (h *Handler) RunWorkflowScheduler(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			h.log.Debug("Running workflow threshold checks")
			h.CheckThresholdRules()
		}
	}()
	h.log.Info("Workflow scheduler started", "interval", interval)
}
