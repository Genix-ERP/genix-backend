package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// WorkflowRule represents an automation rule
type WorkflowRule struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	TenantID        uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Name            string          `json:"name" db:"name"`
	Description     *string         `json:"description,omitempty" db:"description"`
	Category        string          `json:"category" db:"category"`
	TriggerType     string          `json:"trigger_type" db:"trigger_type"`
	TriggerEvent    *string         `json:"trigger_event,omitempty" db:"trigger_event"`
	Conditions      json.RawMessage `json:"conditions" db:"conditions" swaggertype:"object"`
	Actions         json.RawMessage `json:"actions" db:"actions" swaggertype:"object"`
	IsActive        bool            `json:"is_active" db:"is_active"`
	Priority        int             `json:"priority" db:"priority"`
	LastTriggeredAt *time.Time      `json:"last_triggered_at,omitempty" db:"last_triggered_at"`
	TriggerCount    int             `json:"trigger_count" db:"trigger_count"`
	CreatedBy       *uuid.UUID      `json:"created_by,omitempty" db:"created_by"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt       sql.NullTime    `json:"-" db:"deleted_at"`
}

// CreateWorkflowRuleInput represents input for creating a workflow rule
type CreateWorkflowRuleInput struct {
	Name         string          `json:"name" binding:"required"`
	Description  string          `json:"description,omitempty"`
	Category     string          `json:"category" binding:"required"`
	TriggerType  string          `json:"trigger_type" binding:"required"`
	TriggerEvent string          `json:"trigger_event,omitempty"`
	Conditions   json.RawMessage `json:"conditions"`
	Actions      json.RawMessage `json:"actions" binding:"required"`
	IsActive     *bool           `json:"is_active,omitempty"`
	Priority     int             `json:"priority,omitempty"`
}

// UpdateWorkflowRuleInput represents input for updating a workflow rule
type UpdateWorkflowRuleInput struct {
	Name         *string          `json:"name,omitempty"`
	Description  *string          `json:"description,omitempty"`
	Category     *string          `json:"category,omitempty"`
	TriggerType  *string          `json:"trigger_type,omitempty"`
	TriggerEvent *string          `json:"trigger_event,omitempty"`
	Conditions   *json.RawMessage `json:"conditions,omitempty"`
	Actions      *json.RawMessage `json:"actions,omitempty"`
	IsActive     *bool            `json:"is_active,omitempty"`
	Priority     *int             `json:"priority,omitempty"`
}

// WorkflowRuleResponse represents the API response for a workflow rule
type WorkflowRuleResponse struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     *string         `json:"description,omitempty"`
	Category        string          `json:"category"`
	TriggerType     string          `json:"trigger_type"`
	TriggerEvent    *string         `json:"trigger_event,omitempty"`
	Conditions      json.RawMessage `json:"conditions"`
	Actions         json.RawMessage `json:"actions"`
	IsActive        bool            `json:"is_active"`
	Priority        int             `json:"priority"`
	LastTriggeredAt *string         `json:"last_triggered_at,omitempty"`
	TriggerCount    int             `json:"trigger_count"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

// ToResponse converts a WorkflowRule to WorkflowRuleResponse
func (r *WorkflowRule) ToResponse() *WorkflowRuleResponse {
	resp := &WorkflowRuleResponse{
		ID:           r.ID.String(),
		Name:         r.Name,
		Description:  r.Description,
		Category:     r.Category,
		TriggerType:  r.TriggerType,
		TriggerEvent: r.TriggerEvent,
		Conditions:   r.Conditions,
		Actions:      r.Actions,
		IsActive:     r.IsActive,
		Priority:     r.Priority,
		TriggerCount: r.TriggerCount,
		CreatedAt:    r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    r.UpdatedAt.Format(time.RFC3339),
	}
	if r.LastTriggeredAt != nil {
		t := r.LastTriggeredAt.Format(time.RFC3339)
		resp.LastTriggeredAt = &t
	}
	return resp
}

// WorkflowLog represents an execution log entry
type WorkflowLog struct {
	ID              uuid.UUID       `json:"id"`
	TenantID        uuid.UUID       `json:"tenant_id"`
	RuleID          uuid.UUID       `json:"rule_id"`
	TriggerData     json.RawMessage `json:"trigger_data"`
	ActionsExecuted json.RawMessage `json:"actions_executed"`
	Status          string          `json:"status"`
	ErrorMessage    *string         `json:"error_message,omitempty"`
	ExecutedAt      time.Time       `json:"executed_at"`
}

// WorkflowLogResponse represents the API response for a workflow log
type WorkflowLogResponse struct {
	ID              string          `json:"id"`
	RuleID          string          `json:"rule_id"`
	RuleName        string          `json:"rule_name,omitempty"`
	TriggerData     json.RawMessage `json:"trigger_data"`
	ActionsExecuted json.RawMessage `json:"actions_executed"`
	Status          string          `json:"status"`
	ErrorMessage    *string         `json:"error_message,omitempty"`
	ExecutedAt      string          `json:"executed_at"`
}
