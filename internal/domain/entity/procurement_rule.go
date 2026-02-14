package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Rule Types
const (
	RuleTypeAutoApprove     = "auto_approve"
	RuleTypeRequireApproval = "require_approval"
	RuleTypeRouteToApprover = "route_to_approver"
	RuleTypeBudgetCheck     = "budget_check"
	RuleTypeVendorCheck     = "vendor_check"
)

// Document Types
const (
	DocumentTypePurchaseOrder       = "purchase_order"
	DocumentTypePurchaseRequisition = "purchase_requisition"
	DocumentTypeBoth                = "both"
)

// Action Types
const (
	ActionAutoApprove = "auto_approve"
	ActionRoute       = "route"
	ActionBlock       = "block"
	ActionWarn        = "warn"
)

// Approval Types
const (
	ApprovalTypeSequential = "sequential"
	ApprovalTypeParallel   = "parallel"
	ApprovalTypeAnyOne     = "any_one"
)

// Workflow Statuses
const (
	WorkflowStatusPending   = "pending"
	WorkflowStatusApproved  = "approved"
	WorkflowStatusRejected  = "rejected"
	WorkflowStatusCancelled = "cancelled"
)

// Step Statuses
const (
	StepStatusPending  = "pending"
	StepStatusApproved = "approved"
	StepStatusRejected = "rejected"
	StepStatusSkipped  = "skipped"
)

// RuleConditions represents the conditions for a procurement rule
type RuleConditions struct {
	AmountMin    *float64  `json:"amount_min,omitempty"`
	AmountMax    *float64  `json:"amount_max,omitempty"`
	VendorIDs    []string  `json:"vendor_ids,omitempty"`
	CategoryIDs  []string  `json:"category_ids,omitempty"`
	WarehouseIDs []string  `json:"warehouse_ids,omitempty"`
}

// RuleActions represents the actions for a procurement rule
type RuleActions struct {
	Action       string   `json:"action"` // auto_approve, route, block, warn
	ApproverIDs  []string `json:"approver_ids,omitempty"`
	ApprovalType string   `json:"approval_type,omitempty"` // sequential, parallel, any_one
	Message      string   `json:"message,omitempty"`
}

// ProcurementRule represents a procurement rule
type ProcurementRule struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	TenantID     uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Name         string          `json:"name" db:"name"`
	Description  *string         `json:"description,omitempty" db:"description"`
	RuleType     string          `json:"rule_type" db:"rule_type"`
	DocumentType string          `json:"document_type" db:"document_type"`
	Priority     int             `json:"priority" db:"priority"`
	Conditions   json.RawMessage `json:"conditions" db:"conditions" swaggertype:"object"`
	Actions      json.RawMessage `json:"actions" db:"actions" swaggertype:"object"`
	IsActive     bool            `json:"is_active" db:"is_active"`
	CreatedBy    *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

// GetConditions parses and returns the conditions
func (r *ProcurementRule) GetConditions() (*RuleConditions, error) {
	var cond RuleConditions
	if err := json.Unmarshal(r.Conditions, &cond); err != nil {
		return nil, err
	}
	return &cond, nil
}

// GetActions parses and returns the actions
func (r *ProcurementRule) GetActions() (*RuleActions, error) {
	var act RuleActions
	if err := json.Unmarshal(r.Actions, &act); err != nil {
		return nil, err
	}
	return &act, nil
}

// ApprovalWorkflowInstance represents an active approval workflow
type ApprovalWorkflowInstance struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TenantID       uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	DocumentType   string     `json:"document_type" db:"document_type"`
	DocumentID     uuid.UUID  `json:"document_id" db:"document_id"`
	DocumentNumber string     `json:"document_number" db:"document_number"`
	RuleID         *uuid.UUID `json:"rule_id,omitempty" db:"rule_id"`
	RuleName       *string    `json:"rule_name,omitempty" db:"rule_name"`
	Status         string     `json:"status" db:"status"`
	ApprovalType   string     `json:"approval_type" db:"approval_type"`
	CurrentStep    int        `json:"current_step" db:"current_step"`
	TotalSteps     int        `json:"total_steps" db:"total_steps"`
	StartedAt      time.Time  `json:"started_at" db:"started_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`

	// Relationships
	Steps []ApprovalWorkflowStep `json:"steps,omitempty"`
}

// ApprovalWorkflowStep represents a step in an approval workflow
type ApprovalWorkflowStep struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	WorkflowID   uuid.UUID  `json:"workflow_id" db:"workflow_id"`
	StepNumber   int        `json:"step_number" db:"step_number"`
	ApproverID   uuid.UUID  `json:"approver_id" db:"approver_id"`
	ApproverName *string    `json:"approver_name,omitempty" db:"approver_name"`
	Status       string     `json:"status" db:"status"`
	ActionDate   *time.Time `json:"action_date,omitempty" db:"action_date"`
	Comments     *string    `json:"comments,omitempty" db:"comments"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}

// RuleEvaluationLog represents a log entry for rule evaluation
type RuleEvaluationLog struct {
	ID                 uuid.UUID       `json:"id" db:"id"`
	TenantID           uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	DocumentType       string          `json:"document_type" db:"document_type"`
	DocumentID         uuid.UUID       `json:"document_id" db:"document_id"`
	DocumentNumber     *string         `json:"document_number,omitempty" db:"document_number"`
	RulesEvaluated     json.RawMessage `json:"rules_evaluated" db:"rules_evaluated" swaggertype:"object"`
	FinalAction        string          `json:"final_action" db:"final_action"`
	FinalActionDetails json.RawMessage `json:"final_action_details" db:"final_action_details" swaggertype:"object"`
	EvaluatedBy        *uuid.UUID      `json:"evaluated_by,omitempty" db:"evaluated_by"`
	EvaluatedAt        time.Time       `json:"evaluated_at" db:"evaluated_at"`
}

// RuleEvaluationResult represents the result of rule evaluation
type RuleEvaluationResult struct {
	Action            string                    `json:"action"`
	Message           string                    `json:"message,omitempty"`
	MatchedRule       *ProcurementRule          `json:"matched_rule,omitempty"`
	WorkflowInstance  *ApprovalWorkflowInstance `json:"workflow_instance,omitempty"`
	ApproverIDs       []uuid.UUID               `json:"approver_ids,omitempty"`
	ApprovalType      string                    `json:"approval_type,omitempty"`
	RulesEvaluated    []RuleEvaluationEntry     `json:"rules_evaluated"`
}

// RuleEvaluationEntry represents a single rule evaluation
type RuleEvaluationEntry struct {
	RuleID   uuid.UUID `json:"rule_id"`
	RuleName string    `json:"rule_name"`
	Matched  bool      `json:"matched"`
	Reason   string    `json:"reason,omitempty"`
}

// CreateProcurementRuleInput represents input for creating a rule
type CreateProcurementRuleInput struct {
	Name         string          `json:"name" binding:"required"`
	Description  string          `json:"description,omitempty"`
	RuleType     string          `json:"rule_type" binding:"required"`
	DocumentType string          `json:"document_type,omitempty"`
	Priority     int             `json:"priority,omitempty"`
	Conditions   json.RawMessage `json:"conditions" binding:"required"`
	Actions      json.RawMessage `json:"actions" binding:"required"`
	IsActive     *bool           `json:"is_active,omitempty"`
}

// UpdateProcurementRuleInput represents input for updating a rule
type UpdateProcurementRuleInput struct {
	Name         *string          `json:"name,omitempty"`
	Description  *string          `json:"description,omitempty"`
	RuleType     *string          `json:"rule_type,omitempty"`
	DocumentType *string          `json:"document_type,omitempty"`
	Priority     *int             `json:"priority,omitempty"`
	Conditions   *json.RawMessage `json:"conditions,omitempty"`
	Actions      *json.RawMessage `json:"actions,omitempty"`
	IsActive     *bool            `json:"is_active,omitempty"`
}

// ApproveWorkflowStepInput represents input for approving a workflow step
type ApproveWorkflowStepInput struct {
	Comments string `json:"comments,omitempty"`
}

// RejectWorkflowStepInput represents input for rejecting a workflow step
type RejectWorkflowStepInput struct {
	Comments string `json:"comments" binding:"required"`
}
