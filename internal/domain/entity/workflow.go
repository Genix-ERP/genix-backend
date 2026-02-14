package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Workflow represents an automated workflow
type Workflow struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	TenantID          uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Name              string          `json:"name" db:"name"`
	Description       *string         `json:"description,omitempty" db:"description"`
	Category          string          `json:"category" db:"category"` // hr, procurement, customer_support, sales, inventory, finance, marketing
	Trigger           string          `json:"trigger" db:"trigger"` // manual, scheduled, event_based, ai_triggered
	Status            string          `json:"status" db:"status"` // active, paused, draft
	AutomationLevel   string          `json:"automation_level" db:"automation_level"` // manual, semi_automated, fully_automated
	Steps             json.RawMessage `json:"steps" db:"steps"` // JSON array of workflow steps
	SuccessRate       float64         `json:"success_rate" db:"success_rate"` // Percentage
	AvgCompletionTime float64         `json:"avg_completion_time" db:"avg_completion_time"` // Hours
	CostSavings       float64         `json:"cost_savings" db:"cost_savings"` // Monthly savings in currency
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt         sql.NullTime    `json:"-" db:"deleted_at"`
}

// WorkflowStep represents a single step in a workflow
type WorkflowStep struct {
	StepName          string `json:"step_name"`
	Action            string `json:"action"`
	Assignee          string `json:"assignee"`
	EstimatedDuration int    `json:"estimated_duration"` // in hours
}

// CreateWorkflowInput represents input for creating a workflow
type CreateWorkflowInput struct {
	Name              string         `json:"name" binding:"required"`
	Description       string         `json:"description,omitempty"`
	Category          string         `json:"category" binding:"required"`
	Trigger           string         `json:"trigger"`
	Status            string         `json:"status"`
	AutomationLevel   string         `json:"automation_level"`
	Steps             []WorkflowStep `json:"steps"`
	SuccessRate       float64        `json:"success_rate"`
	AvgCompletionTime float64        `json:"avg_completion_time"`
	CostSavings       float64        `json:"cost_savings"`
}

// UpdateWorkflowInput represents input for updating a workflow
type UpdateWorkflowInput struct {
	Name              *string         `json:"name,omitempty"`
	Description       *string         `json:"description,omitempty"`
	Category          *string         `json:"category,omitempty"`
	Trigger           *string         `json:"trigger,omitempty"`
	Status            *string         `json:"status,omitempty"`
	AutomationLevel   *string         `json:"automation_level,omitempty"`
	Steps             *[]WorkflowStep `json:"steps,omitempty"`
	SuccessRate       *float64        `json:"success_rate,omitempty"`
	AvgCompletionTime *float64        `json:"avg_completion_time,omitempty"`
	CostSavings       *float64        `json:"cost_savings,omitempty"`
}

// WorkflowResponse represents the API response for a workflow
type WorkflowResponse struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       *string         `json:"description,omitempty"`
	Category          string          `json:"category"`
	Trigger           string          `json:"trigger"`
	Status            string          `json:"status"`
	AutomationLevel   string          `json:"automation_level"`
	Steps             json.RawMessage `json:"steps"`
	SuccessRate       float64         `json:"success_rate"`
	AvgCompletionTime float64         `json:"avg_completion_time"`
	CostSavings       float64         `json:"cost_savings"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

// ToResponse converts a Workflow to WorkflowResponse
func (w *Workflow) ToResponse() *WorkflowResponse {
	return &WorkflowResponse{
		ID:                w.ID.String(),
		Name:              w.Name,
		Description:       w.Description,
		Category:          w.Category,
		Trigger:           w.Trigger,
		Status:            w.Status,
		AutomationLevel:   w.AutomationLevel,
		Steps:             w.Steps,
		SuccessRate:       w.SuccessRate,
		AvgCompletionTime: w.AvgCompletionTime,
		CostSavings:       w.CostSavings,
		CreatedAt:         w.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         w.UpdatedAt.Format(time.RFC3339),
	}
}
