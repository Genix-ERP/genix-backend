package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// PROCUREMENT RULES HANDLERS
// =====================================================

// ListProcurementRules returns a list of procurement rules
func (h *Handler) ListProcurementRules(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Parse filters
	ruleType := c.Query("rule_type")
	documentType := c.Query("document_type")
	isActive := c.Query("is_active")

	// Build query
	baseQuery := `
		SELECT id, tenant_id, name, description, rule_type, document_type,
			   priority, conditions, actions, is_active, created_by, created_at, updated_at
		FROM procurement_rules
		WHERE tenant_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM procurement_rules WHERE tenant_id = $1`

	args := []interface{}{tenantID}
	argCount := 1

	if ruleType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND rule_type = $%d", argCount)
		countQuery += fmt.Sprintf(" AND rule_type = $%d", argCount)
		args = append(args, ruleType)
	}

	if documentType != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND (document_type = $%d OR document_type = 'both')", argCount)
		countQuery += fmt.Sprintf(" AND (document_type = $%d OR document_type = 'both')", argCount)
		args = append(args, documentType)
	}

	if isActive != "" {
		argCount++
		active := isActive == "true"
		baseQuery += fmt.Sprintf(" AND is_active = $%d", argCount)
		countQuery += fmt.Sprintf(" AND is_active = $%d", argCount)
		args = append(args, active)
	}

	// Get total count
	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count procurement rules", "error", err)
		response.InternalError(c, "Failed to list procurement rules")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY priority ASC, created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, limit, offset)

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list procurement rules", "error", err)
		response.InternalError(c, "Failed to list procurement rules")
		return
	}
	defer rows.Close()

	var rules []entity.ProcurementRule
	for rows.Next() {
		var rule entity.ProcurementRule
		err := rows.Scan(
			&rule.ID, &rule.TenantID, &rule.Name, &rule.Description, &rule.RuleType,
			&rule.DocumentType, &rule.Priority, &rule.Conditions, &rule.Actions,
			&rule.IsActive, &rule.CreatedBy, &rule.CreatedAt, &rule.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan procurement rule", "error", err)
			continue
		}
		rules = append(rules, rule)
	}

	response.Paginated(c, rules, page, limit, total)
}

// CreateProcurementRule creates a new procurement rule
func (h *Handler) CreateProcurementRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateProcurementRuleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Validate rule type
	validRuleTypes := map[string]bool{
		entity.RuleTypeAutoApprove:     true,
		entity.RuleTypeRequireApproval: true,
		entity.RuleTypeRouteToApprover: true,
		entity.RuleTypeBudgetCheck:     true,
		entity.RuleTypeVendorCheck:     true,
	}
	if !validRuleTypes[input.RuleType] {
		response.BadRequest(c, "Invalid rule type")
		return
	}

	// Set defaults
	documentType := input.DocumentType
	if documentType == "" {
		documentType = entity.DocumentTypeBoth
	}

	priority := input.Priority
	if priority == 0 {
		priority = 100
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	// Create rule
	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO procurement_rules (
			id, tenant_id, name, description, rule_type, document_type,
			priority, conditions, actions, is_active, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
		RETURNING id
	`

	var description *string
	if input.Description != "" {
		description = &input.Description
	}

	err := h.db.QueryRow(
		query, id, tenantID, input.Name, description, input.RuleType, documentType,
		priority, input.Conditions, input.Actions, isActive, userID, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create procurement rule", "error", err)
		response.InternalError(c, "Failed to create procurement rule")
		return
	}

	rule := entity.ProcurementRule{
		ID:           id,
		TenantID:     tenantID,
		Name:         input.Name,
		Description:  description,
		RuleType:     input.RuleType,
		DocumentType: documentType,
		Priority:     priority,
		Conditions:   input.Conditions,
		Actions:      input.Actions,
		IsActive:     isActive,
		CreatedBy:    &userID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	response.Created(c, rule)
}

// GetProcurementRule returns a single procurement rule
func (h *Handler) GetProcurementRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid rule ID")
		return
	}

	query := `
		SELECT id, tenant_id, name, description, rule_type, document_type,
			   priority, conditions, actions, is_active, created_by, created_at, updated_at
		FROM procurement_rules
		WHERE id = $1 AND tenant_id = $2
	`

	var rule entity.ProcurementRule
	err = h.db.QueryRow(query, id, tenantID).Scan(
		&rule.ID, &rule.TenantID, &rule.Name, &rule.Description, &rule.RuleType,
		&rule.DocumentType, &rule.Priority, &rule.Conditions, &rule.Actions,
		&rule.IsActive, &rule.CreatedBy, &rule.CreatedAt, &rule.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Procurement rule")
		return
	}
	if err != nil {
		h.log.Error("Failed to get procurement rule", "error", err)
		response.InternalError(c, "Failed to get procurement rule")
		return
	}

	response.Success(c, rule)
}

// UpdateProcurementRule updates a procurement rule
func (h *Handler) UpdateProcurementRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid rule ID")
		return
	}

	var input entity.UpdateProcurementRuleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check if rule exists
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM procurement_rules WHERE id = $1 AND tenant_id = $2)", id, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Procurement rule")
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.RuleType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("rule_type = $%d", argCount))
		args = append(args, *input.RuleType)
	}
	if input.DocumentType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("document_type = $%d", argCount))
		args = append(args, *input.DocumentType)
	}
	if input.Priority != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("priority = $%d", argCount))
		args = append(args, *input.Priority)
	}
	if input.Conditions != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("conditions = $%d", argCount))
		args = append(args, *input.Conditions)
	}
	if input.Actions != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("actions = $%d", argCount))
		args = append(args, *input.Actions)
	}
	if input.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE procurement_rules SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(updates, ", "), argCount-1, argCount,
	)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update procurement rule", "error", err)
		response.InternalError(c, "Failed to update procurement rule")
		return
	}

	// Return updated rule
	h.GetProcurementRule(c)
}

// DeleteProcurementRule deletes a procurement rule
func (h *Handler) DeleteProcurementRule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid rule ID")
		return
	}

	result, err := h.db.Exec("DELETE FROM procurement_rules WHERE id = $1 AND tenant_id = $2", id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete procurement rule", "error", err)
		response.InternalError(c, "Failed to delete procurement rule")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Procurement rule")
		return
	}

	response.NoContent(c)
}

// =====================================================
// RULES EVALUATION ENGINE
// =====================================================

// EvaluateRules evaluates procurement rules for a document
func (h *Handler) EvaluateRules(tenantID uuid.UUID, documentType string, documentID uuid.UUID, amount float64, vendorID *uuid.UUID, userID uuid.UUID) (*entity.RuleEvaluationResult, error) {
	// Fetch all active rules for this tenant and document type, ordered by priority
	query := `
		SELECT id, tenant_id, name, description, rule_type, document_type,
			   priority, conditions, actions, is_active, created_by, created_at, updated_at
		FROM procurement_rules
		WHERE tenant_id = $1 AND is_active = true
		  AND (document_type = $2 OR document_type = 'both')
		ORDER BY priority ASC
	`

	rows, err := h.db.Query(query, tenantID, documentType)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch rules: %w", err)
	}
	defer rows.Close()

	var rules []entity.ProcurementRule
	for rows.Next() {
		var rule entity.ProcurementRule
		err := rows.Scan(
			&rule.ID, &rule.TenantID, &rule.Name, &rule.Description, &rule.RuleType,
			&rule.DocumentType, &rule.Priority, &rule.Conditions, &rule.Actions,
			&rule.IsActive, &rule.CreatedBy, &rule.CreatedAt, &rule.UpdatedAt,
		)
		if err != nil {
			continue
		}
		rules = append(rules, rule)
	}

	result := &entity.RuleEvaluationResult{
		Action:         entity.ActionAutoApprove, // Default: auto-approve if no rules match
		RulesEvaluated: []entity.RuleEvaluationEntry{},
	}

	// Evaluate each rule
	for _, rule := range rules {
		entry := entity.RuleEvaluationEntry{
			RuleID:   rule.ID,
			RuleName: rule.Name,
			Matched:  false,
		}

		// Parse conditions
		cond, err := rule.GetConditions()
		if err != nil {
			entry.Reason = "Failed to parse conditions"
			result.RulesEvaluated = append(result.RulesEvaluated, entry)
			continue
		}

		// Check amount conditions
		if cond.AmountMin != nil && amount < *cond.AmountMin {
			entry.Reason = fmt.Sprintf("Amount %.2f < min %.2f", amount, *cond.AmountMin)
			result.RulesEvaluated = append(result.RulesEvaluated, entry)
			continue
		}
		if cond.AmountMax != nil && amount > *cond.AmountMax {
			entry.Reason = fmt.Sprintf("Amount %.2f > max %.2f", amount, *cond.AmountMax)
			result.RulesEvaluated = append(result.RulesEvaluated, entry)
			continue
		}

		// Check vendor conditions
		if len(cond.VendorIDs) > 0 && vendorID != nil {
			vendorMatch := false
			for _, vid := range cond.VendorIDs {
				if vid == vendorID.String() {
					vendorMatch = true
					break
				}
			}
			if !vendorMatch {
				entry.Reason = "Vendor not in allowed list"
				result.RulesEvaluated = append(result.RulesEvaluated, entry)
				continue
			}
		}

		// Rule matched!
		entry.Matched = true
		entry.Reason = "All conditions matched"
		result.RulesEvaluated = append(result.RulesEvaluated, entry)

		// Parse actions
		actions, err := rule.GetActions()
		if err != nil {
			continue
		}

		result.Action = actions.Action
		result.Message = actions.Message
		result.MatchedRule = &rule

		// Handle routing action
		if actions.Action == entity.ActionRoute && len(actions.ApproverIDs) > 0 {
			result.ApprovalType = actions.ApprovalType
			if result.ApprovalType == "" {
				result.ApprovalType = entity.ApprovalTypeSequential
			}
			for _, aidStr := range actions.ApproverIDs {
				if aid, err := uuid.Parse(aidStr); err == nil {
					result.ApproverIDs = append(result.ApproverIDs, aid)
				}
			}
		}

		// First matching rule wins
		break
	}

	// Log the evaluation
	h.logRuleEvaluation(tenantID, documentType, documentID, "", result, userID)

	return result, nil
}

// logRuleEvaluation logs the rule evaluation
func (h *Handler) logRuleEvaluation(tenantID uuid.UUID, documentType string, documentID uuid.UUID, documentNumber string, result *entity.RuleEvaluationResult, userID uuid.UUID) {
	rulesJSON, _ := json.Marshal(result.RulesEvaluated)

	detailsJSON, _ := json.Marshal(map[string]interface{}{
		"message":       result.Message,
		"approver_ids":  result.ApproverIDs,
		"approval_type": result.ApprovalType,
	})

	query := `
		INSERT INTO rule_evaluation_logs (
			id, tenant_id, document_type, document_id, document_number,
			rules_evaluated, final_action, final_action_details, evaluated_by, evaluated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	var docNum *string
	if documentNumber != "" {
		docNum = &documentNumber
	}

	_, err := h.db.Exec(query,
		uuid.New(), tenantID, documentType, documentID, docNum,
		rulesJSON, result.Action, detailsJSON, userID, time.Now(),
	)
	if err != nil {
		h.log.Error("Failed to log rule evaluation", "error", err)
	}
}

// CreateApprovalWorkflow creates a new approval workflow instance
func (h *Handler) CreateApprovalWorkflow(tenantID uuid.UUID, documentType string, documentID uuid.UUID, documentNumber string, result *entity.RuleEvaluationResult) (*entity.ApprovalWorkflowInstance, error) {
	if len(result.ApproverIDs) == 0 {
		return nil, fmt.Errorf("no approvers specified")
	}

	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	workflowID := uuid.New()
	now := time.Now()

	var ruleName *string
	var ruleID *uuid.UUID
	if result.MatchedRule != nil {
		ruleID = &result.MatchedRule.ID
		ruleName = &result.MatchedRule.Name
	}

	// Create workflow instance
	query := `
		INSERT INTO approval_workflow_instances (
			id, tenant_id, document_type, document_id, document_number,
			rule_id, rule_name, status, approval_type, current_step, total_steps,
			started_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12, $12)
	`

	_, err = tx.Exec(query,
		workflowID, tenantID, documentType, documentID, documentNumber,
		ruleID, ruleName, entity.WorkflowStatusPending, result.ApprovalType,
		1, len(result.ApproverIDs), now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}

	// Create workflow steps
	for i, approverID := range result.ApproverIDs {
		// Get approver name
		var approverName string
		h.db.QueryRow("SELECT COALESCE(full_name, email) FROM users WHERE id = $1", approverID).Scan(&approverName)

		stepQuery := `
			INSERT INTO approval_workflow_steps (
				id, workflow_id, step_number, approver_id, approver_name, status, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`

		_, err = tx.Exec(stepQuery,
			uuid.New(), workflowID, i+1, approverID, approverName, entity.StepStatusPending, now,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create workflow step: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	workflow := &entity.ApprovalWorkflowInstance{
		ID:             workflowID,
		TenantID:       tenantID,
		DocumentType:   documentType,
		DocumentID:     documentID,
		DocumentNumber: documentNumber,
		RuleID:         ruleID,
		RuleName:       ruleName,
		Status:         entity.WorkflowStatusPending,
		ApprovalType:   result.ApprovalType,
		CurrentStep:    1,
		TotalSteps:     len(result.ApproverIDs),
		StartedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	return workflow, nil
}

// =====================================================
// APPROVAL WORKFLOW HANDLERS
// =====================================================

// GetApprovalWorkflow returns an approval workflow with its steps
func (h *Handler) GetApprovalWorkflow(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid workflow ID")
		return
	}

	query := `
		SELECT id, tenant_id, document_type, document_id, document_number,
			   rule_id, rule_name, status, approval_type, current_step, total_steps,
			   started_at, completed_at, created_at, updated_at
		FROM approval_workflow_instances
		WHERE id = $1 AND tenant_id = $2
	`

	var wf entity.ApprovalWorkflowInstance
	err = h.db.QueryRow(query, id, tenantID).Scan(
		&wf.ID, &wf.TenantID, &wf.DocumentType, &wf.DocumentID, &wf.DocumentNumber,
		&wf.RuleID, &wf.RuleName, &wf.Status, &wf.ApprovalType, &wf.CurrentStep, &wf.TotalSteps,
		&wf.StartedAt, &wf.CompletedAt, &wf.CreatedAt, &wf.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Approval workflow")
		return
	}
	if err != nil {
		h.log.Error("Failed to get approval workflow", "error", err)
		response.InternalError(c, "Failed to get approval workflow")
		return
	}

	// Get steps
	stepsQuery := `
		SELECT id, workflow_id, step_number, approver_id, approver_name,
			   status, action_date, comments, created_at
		FROM approval_workflow_steps
		WHERE workflow_id = $1
		ORDER BY step_number ASC
	`

	rows, err := h.db.Query(stepsQuery, id)
	if err != nil {
		h.log.Error("Failed to get workflow steps", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var step entity.ApprovalWorkflowStep
			rows.Scan(
				&step.ID, &step.WorkflowID, &step.StepNumber, &step.ApproverID, &step.ApproverName,
				&step.Status, &step.ActionDate, &step.Comments, &step.CreatedAt,
			)
			wf.Steps = append(wf.Steps, step)
		}
	}

	response.Success(c, wf)
}

// ApproveWorkflowStep approves the current step in a workflow
func (h *Handler) ApproveWorkflowStep(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	workflowID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid workflow ID")
		return
	}

	var input entity.ApproveWorkflowStepInput
	if err := c.ShouldBindJSON(&input); err != nil {
		// Comments are optional for approval
		input.Comments = ""
	}

	// Get workflow
	var wf entity.ApprovalWorkflowInstance
	query := `
		SELECT id, tenant_id, document_type, document_id, document_number,
			   status, approval_type, current_step, total_steps
		FROM approval_workflow_instances
		WHERE id = $1 AND tenant_id = $2
	`
	err = h.db.QueryRow(query, workflowID, tenantID).Scan(
		&wf.ID, &wf.TenantID, &wf.DocumentType, &wf.DocumentID, &wf.DocumentNumber,
		&wf.Status, &wf.ApprovalType, &wf.CurrentStep, &wf.TotalSteps,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Approval workflow")
		return
	}
	if err != nil {
		h.log.Error("Failed to get workflow", "error", err)
		response.InternalError(c, "Failed to approve workflow")
		return
	}

	if wf.Status != entity.WorkflowStatusPending {
		response.BadRequest(c, "Workflow is not pending")
		return
	}

	// Check if user is the approver for current step
	var stepID uuid.UUID
	var stepApproverID uuid.UUID
	stepQuery := `
		SELECT id, approver_id FROM approval_workflow_steps
		WHERE workflow_id = $1 AND step_number = $2 AND status = 'pending'
	`
	err = h.db.QueryRow(stepQuery, workflowID, wf.CurrentStep).Scan(&stepID, &stepApproverID)
	if err == sql.ErrNoRows {
		response.BadRequest(c, "No pending step found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get step", "error", err)
		response.InternalError(c, "Failed to approve workflow")
		return
	}

	if stepApproverID != userID {
		response.Forbidden(c, "You are not the approver for this step")
		return
	}

	now := time.Now()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Update step
	_, err = tx.Exec(`
		UPDATE approval_workflow_steps
		SET status = 'approved', action_date = $1, comments = $2
		WHERE id = $3
	`, now, input.Comments, stepID)
	if err != nil {
		response.InternalError(c, "Failed to update step")
		return
	}

	// Check if workflow is complete
	isComplete := wf.CurrentStep >= wf.TotalSteps

	if isComplete {
		// Complete workflow
		_, err = tx.Exec(`
			UPDATE approval_workflow_instances
			SET status = 'approved', completed_at = $1, updated_at = $1
			WHERE id = $2
		`, now, workflowID)
		if err != nil {
			response.InternalError(c, "Failed to complete workflow")
			return
		}

		// Approve the document
		err = h.approveDocument(tx, wf.DocumentType, wf.DocumentID, userID, now)
		if err != nil {
			h.log.Error("Failed to approve document", "error", err)
			response.InternalError(c, "Failed to approve document")
			return
		}
	} else {
		// Move to next step
		_, err = tx.Exec(`
			UPDATE approval_workflow_instances
			SET current_step = current_step + 1, updated_at = $1
			WHERE id = $2
		`, now, workflowID)
		if err != nil {
			response.InternalError(c, "Failed to advance workflow")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	response.Success(c, gin.H{
		"message":    "Step approved successfully",
		"completed":  isComplete,
		"next_step":  wf.CurrentStep + 1,
		"total_steps": wf.TotalSteps,
	})
}

// RejectWorkflowStep rejects the current step in a workflow
func (h *Handler) RejectWorkflowStep(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	workflowID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid workflow ID")
		return
	}

	var input entity.RejectWorkflowStepInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Comments are required for rejection")
		return
	}

	// Get workflow
	var wf entity.ApprovalWorkflowInstance
	query := `
		SELECT id, tenant_id, document_type, document_id, document_number,
			   status, approval_type, current_step, total_steps
		FROM approval_workflow_instances
		WHERE id = $1 AND tenant_id = $2
	`
	err = h.db.QueryRow(query, workflowID, tenantID).Scan(
		&wf.ID, &wf.TenantID, &wf.DocumentType, &wf.DocumentID, &wf.DocumentNumber,
		&wf.Status, &wf.ApprovalType, &wf.CurrentStep, &wf.TotalSteps,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Approval workflow")
		return
	}
	if err != nil {
		h.log.Error("Failed to get workflow", "error", err)
		response.InternalError(c, "Failed to reject workflow")
		return
	}

	if wf.Status != entity.WorkflowStatusPending {
		response.BadRequest(c, "Workflow is not pending")
		return
	}

	// Check if user is the approver for current step
	var stepID uuid.UUID
	var stepApproverID uuid.UUID
	stepQuery := `
		SELECT id, approver_id FROM approval_workflow_steps
		WHERE workflow_id = $1 AND step_number = $2 AND status = 'pending'
	`
	err = h.db.QueryRow(stepQuery, workflowID, wf.CurrentStep).Scan(&stepID, &stepApproverID)
	if err == sql.ErrNoRows {
		response.BadRequest(c, "No pending step found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get step", "error", err)
		response.InternalError(c, "Failed to reject workflow")
		return
	}

	if stepApproverID != userID {
		response.Forbidden(c, "You are not the approver for this step")
		return
	}

	now := time.Now()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Update step
	_, err = tx.Exec(`
		UPDATE approval_workflow_steps
		SET status = 'rejected', action_date = $1, comments = $2
		WHERE id = $3
	`, now, input.Comments, stepID)
	if err != nil {
		response.InternalError(c, "Failed to update step")
		return
	}

	// Reject workflow
	_, err = tx.Exec(`
		UPDATE approval_workflow_instances
		SET status = 'rejected', completed_at = $1, updated_at = $1
		WHERE id = $2
	`, now, workflowID)
	if err != nil {
		response.InternalError(c, "Failed to reject workflow")
		return
	}

	// Reject the document (set back to draft or rejected status)
	err = h.rejectDocument(tx, wf.DocumentType, wf.DocumentID, input.Comments, now)
	if err != nil {
		h.log.Error("Failed to reject document", "error", err)
		response.InternalError(c, "Failed to reject document")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	response.Success(c, gin.H{"message": "Workflow rejected"})
}

// GetMyPendingApprovals returns workflows pending approval by the current user
func (h *Handler) GetMyPendingApprovals(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Query for pending approvals where user is the approver for current step
	query := `
		SELECT w.id, w.tenant_id, w.document_type, w.document_id, w.document_number,
			   w.rule_id, w.rule_name, w.status, w.approval_type, w.current_step, w.total_steps,
			   w.started_at, w.created_at, w.updated_at,
			   s.id as step_id, s.step_number, s.comments
		FROM approval_workflow_instances w
		JOIN approval_workflow_steps s ON s.workflow_id = w.id AND s.step_number = w.current_step
		WHERE w.tenant_id = $1 AND w.status = 'pending' AND s.approver_id = $2 AND s.status = 'pending'
		ORDER BY w.started_at DESC
		LIMIT $3 OFFSET $4
	`

	countQuery := `
		SELECT COUNT(*)
		FROM approval_workflow_instances w
		JOIN approval_workflow_steps s ON s.workflow_id = w.id AND s.step_number = w.current_step
		WHERE w.tenant_id = $1 AND w.status = 'pending' AND s.approver_id = $2 AND s.status = 'pending'
	`

	var total int
	if err := h.db.QueryRow(countQuery, tenantID, userID).Scan(&total); err != nil {
		h.log.Error("Failed to count pending approvals", "error", err)
		response.InternalError(c, "Failed to get pending approvals")
		return
	}

	rows, err := h.db.Query(query, tenantID, userID, limit, offset)
	if err != nil {
		h.log.Error("Failed to get pending approvals", "error", err)
		response.InternalError(c, "Failed to get pending approvals")
		return
	}
	defer rows.Close()

	type PendingApproval struct {
		entity.ApprovalWorkflowInstance
		StepID     uuid.UUID `json:"step_id"`
		StepNumber int       `json:"step_number"`
		Comments   *string   `json:"comments,omitempty"`
	}

	var approvals []PendingApproval
	for rows.Next() {
		var a PendingApproval
		err := rows.Scan(
			&a.ID, &a.TenantID, &a.DocumentType, &a.DocumentID, &a.DocumentNumber,
			&a.RuleID, &a.RuleName, &a.Status, &a.ApprovalType, &a.CurrentStep, &a.TotalSteps,
			&a.StartedAt, &a.CreatedAt, &a.UpdatedAt,
			&a.StepID, &a.StepNumber, &a.Comments,
		)
		if err != nil {
			h.log.Error("Failed to scan pending approval", "error", err)
			continue
		}
		approvals = append(approvals, a)
	}

	response.Paginated(c, approvals, page, limit, total)
}

// GetWorkflowByDocument returns the active workflow for a document
func (h *Handler) GetWorkflowByDocument(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	documentType := c.Query("document_type")
	documentIDStr := c.Query("document_id")

	if documentType == "" || documentIDStr == "" {
		response.BadRequest(c, "document_type and document_id are required")
		return
	}

	documentID, err := uuid.Parse(documentIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid document_id")
		return
	}

	query := `
		SELECT id, tenant_id, document_type, document_id, document_number,
			   rule_id, rule_name, status, approval_type, current_step, total_steps,
			   started_at, completed_at, created_at, updated_at
		FROM approval_workflow_instances
		WHERE tenant_id = $1 AND document_type = $2 AND document_id = $3
		ORDER BY created_at DESC
		LIMIT 1
	`

	var wf entity.ApprovalWorkflowInstance
	err = h.db.QueryRow(query, tenantID, documentType, documentID).Scan(
		&wf.ID, &wf.TenantID, &wf.DocumentType, &wf.DocumentID, &wf.DocumentNumber,
		&wf.RuleID, &wf.RuleName, &wf.Status, &wf.ApprovalType, &wf.CurrentStep, &wf.TotalSteps,
		&wf.StartedAt, &wf.CompletedAt, &wf.CreatedAt, &wf.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.Success(c, nil)
		return
	}
	if err != nil {
		h.log.Error("Failed to get workflow by document", "error", err)
		response.InternalError(c, "Failed to get workflow")
		return
	}

	// Get steps
	stepsQuery := `
		SELECT id, workflow_id, step_number, approver_id, approver_name,
			   status, action_date, comments, created_at
		FROM approval_workflow_steps
		WHERE workflow_id = $1
		ORDER BY step_number ASC
	`

	rows, err := h.db.Query(stepsQuery, wf.ID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var step entity.ApprovalWorkflowStep
			rows.Scan(
				&step.ID, &step.WorkflowID, &step.StepNumber, &step.ApproverID, &step.ApproverName,
				&step.Status, &step.ActionDate, &step.Comments, &step.CreatedAt,
			)
			wf.Steps = append(wf.Steps, step)
		}
	}

	response.Success(c, wf)
}

// =====================================================
// HELPER FUNCTIONS
// =====================================================

// approveDocument approves the underlying document
func (h *Handler) approveDocument(tx *sql.Tx, documentType string, documentID uuid.UUID, userID uuid.UUID, now time.Time) error {
	var query string
	switch documentType {
	case entity.DocumentTypePurchaseOrder:
		query = `UPDATE purchase_orders SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $2 WHERE id = $3`
	case entity.DocumentTypePurchaseRequisition:
		query = `UPDATE purchase_requisitions SET status = 'approved', approved_by = $1, approved_at = $2, updated_at = $2 WHERE id = $3`
	default:
		return fmt.Errorf("unknown document type: %s", documentType)
	}

	_, err := tx.Exec(query, userID, now, documentID)
	return err
}

// rejectDocument rejects the underlying document
func (h *Handler) rejectDocument(tx *sql.Tx, documentType string, documentID uuid.UUID, reason string, now time.Time) error {
	var query string
	switch documentType {
	case entity.DocumentTypePurchaseOrder:
		query = `UPDATE purchase_orders SET status = 'draft', notes = COALESCE(notes, '') || E'\nRejected: ' || $1, updated_at = $2 WHERE id = $3`
	case entity.DocumentTypePurchaseRequisition:
		query = `UPDATE purchase_requisitions SET status = 'rejected', rejection_reason = $1, updated_at = $2 WHERE id = $3`
	default:
		return fmt.Errorf("unknown document type: %s", documentType)
	}

	_, err := tx.Exec(query, reason, now, documentID)
	return err
}
