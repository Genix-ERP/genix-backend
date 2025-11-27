package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AIConversation represents an AI conversation session
type AIConversation struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	TenantID    uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	Title       *string         `json:"title,omitempty" db:"title"`
	Context     json.RawMessage `json:"context" db:"context"`
	Model       *string         `json:"model,omitempty" db:"model"`
	TotalTokens int             `json:"total_tokens" db:"total_tokens"`
	IsArchived  bool            `json:"is_archived" db:"is_archived"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`

	// Relationships
	Messages []AIMessage `json:"messages,omitempty"`
}

// AIMessageRole represents the role of a message in a conversation
type AIMessageRole string

const (
	AIMessageRoleSystem    AIMessageRole = "system"
	AIMessageRoleUser      AIMessageRole = "user"
	AIMessageRoleAssistant AIMessageRole = "assistant"
	AIMessageRoleFunction  AIMessageRole = "function"
)

// AIMessage represents a message in an AI conversation
type AIMessage struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	ConversationID uuid.UUID       `json:"conversation_id" db:"conversation_id"`
	Role           AIMessageRole   `json:"role" db:"role"`
	Content        string          `json:"content" db:"content"`
	Tokens         int             `json:"tokens" db:"tokens"`
	Metadata       json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
}

// AIPrompt represents a reusable AI prompt template
type AIPrompt struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	TenantID       *uuid.UUID      `json:"tenant_id,omitempty" db:"tenant_id"`
	Name           string          `json:"name" db:"name"`
	Description    *string         `json:"description,omitempty" db:"description"`
	Category       string          `json:"category" db:"category"`
	PromptTemplate string          `json:"prompt_template" db:"prompt_template"`
	Variables      json.RawMessage `json:"variables" db:"variables"`
	IsSystem       bool            `json:"is_system" db:"is_system"`
	IsActive       bool            `json:"is_active" db:"is_active"`
	UsageCount     int             `json:"usage_count" db:"usage_count"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

// AIPromptVariable represents a variable in a prompt template
type AIPromptVariable struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // string, number, boolean, array, object
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
}

// AIPromptCategory represents categories of AI prompts
type AIPromptCategory string

const (
	AIPromptCategoryGeneral     AIPromptCategory = "general"
	AIPromptCategoryAnalysis    AIPromptCategory = "analysis"
	AIPromptCategoryReporting   AIPromptCategory = "reporting"
	AIPromptCategoryForecasting AIPromptCategory = "forecasting"
	AIPromptCategoryAutomation  AIPromptCategory = "automation"
	AIPromptCategoryContent     AIPromptCategory = "content"
	AIPromptCategoryCode        AIPromptCategory = "code"
	AIPromptCategorySupport     AIPromptCategory = "support"
)

// AIRequest represents a request to the AI service
type AIRequest struct {
	ConversationID *uuid.UUID               `json:"conversation_id,omitempty"`
	Messages       []AIMessageInput         `json:"messages" binding:"required"`
	Model          string                   `json:"model,omitempty"`
	MaxTokens      int                      `json:"max_tokens,omitempty"`
	Temperature    float64                  `json:"temperature,omitempty"`
	Stream         bool                     `json:"stream,omitempty"`
	Functions      []AIFunction             `json:"functions,omitempty"`
	Context        map[string]interface{}   `json:"context,omitempty"`
}

// AIMessageInput represents input for an AI message
type AIMessageInput struct {
	Role    AIMessageRole `json:"role" binding:"required,oneof=system user assistant function"`
	Content string        `json:"content" binding:"required"`
	Name    string        `json:"name,omitempty"` // For function messages
}

// AIFunction represents a function that can be called by the AI
type AIFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// AIResponse represents a response from the AI service
type AIResponse struct {
	ID             string            `json:"id"`
	ConversationID uuid.UUID         `json:"conversation_id"`
	Message        AIMessageOutput   `json:"message"`
	Usage          AIUsage           `json:"usage"`
	FinishReason   string            `json:"finish_reason"`
	FunctionCall   *AIFunctionCall   `json:"function_call,omitempty"`
}

// AIMessageOutput represents an output message from the AI
type AIMessageOutput struct {
	Role    AIMessageRole `json:"role"`
	Content string        `json:"content"`
}

// AIUsage represents token usage information
type AIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// AIFunctionCall represents a function call made by the AI
type AIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// CreateConversationInput represents input for creating a conversation
type CreateConversationInput struct {
	Title   string                 `json:"title,omitempty"`
	Context map[string]interface{} `json:"context,omitempty"`
}

// CreatePromptInput represents input for creating a prompt
type CreatePromptInput struct {
	Name           string             `json:"name" binding:"required,min=2,max=100"`
	Description    string             `json:"description,omitempty"`
	Category       AIPromptCategory   `json:"category" binding:"required"`
	PromptTemplate string             `json:"prompt_template" binding:"required"`
	Variables      []AIPromptVariable `json:"variables,omitempty"`
}

// UpdatePromptInput represents input for updating a prompt
type UpdatePromptInput struct {
	Name           *string            `json:"name,omitempty"`
	Description    *string            `json:"description,omitempty"`
	Category       *AIPromptCategory  `json:"category,omitempty"`
	PromptTemplate *string            `json:"prompt_template,omitempty"`
	Variables      []AIPromptVariable `json:"variables,omitempty"`
	IsActive       *bool              `json:"is_active,omitempty"`
}

// AIConversationListFilter represents filters for listing conversations
type AIConversationListFilter struct {
	Search     string `form:"search"`
	IsArchived *bool  `form:"is_archived"`
	DateFrom   string `form:"date_from"`
	DateTo     string `form:"date_to"`
}

// AIPromptListFilter represents filters for listing prompts
type AIPromptListFilter struct {
	Search   string           `form:"search"`
	Category AIPromptCategory `form:"category"`
	IsActive *bool            `form:"is_active"`
}

// AIAssistantCapability represents an AI assistant capability
type AIAssistantCapability struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Examples    []string `json:"examples"`
}

// GetAICapabilities returns available AI capabilities
func GetAICapabilities() []AIAssistantCapability {
	return []AIAssistantCapability{
		{
			Name:        "Sales Analysis",
			Description: "Analyze sales data, trends, and provide insights",
			Category:    "analysis",
			Examples: []string{
				"Analyze last quarter's sales performance",
				"What are the top selling products?",
				"Identify sales trends by region",
			},
		},
		{
			Name:        "Inventory Optimization",
			Description: "Optimize inventory levels and predict stock needs",
			Category:    "forecasting",
			Examples: []string{
				"Which products need reordering?",
				"Forecast inventory needs for next month",
				"Identify slow-moving inventory",
			},
		},
		{
			Name:        "Financial Insights",
			Description: "Analyze financial data and provide recommendations",
			Category:    "analysis",
			Examples: []string{
				"What's our current cash flow status?",
				"Analyze accounts receivable aging",
				"Compare revenue vs expenses",
			},
		},
		{
			Name:        "Report Generation",
			Description: "Generate various business reports",
			Category:    "reporting",
			Examples: []string{
				"Generate a monthly sales report",
				"Create an inventory valuation report",
				"Prepare a customer aging report",
			},
		},
		{
			Name:        "Process Automation",
			Description: "Suggest and help automate business processes",
			Category:    "automation",
			Examples: []string{
				"How can we automate invoice processing?",
				"Suggest workflows for order fulfillment",
				"Automate low stock alerts",
			},
		},
		{
			Name:        "Document Generation",
			Description: "Generate business documents and content",
			Category:    "content",
			Examples: []string{
				"Draft a follow-up email to customers",
				"Generate a proposal template",
				"Create product descriptions",
			},
		},
		{
			Name:        "Data Query",
			Description: "Query business data using natural language",
			Category:    "general",
			Examples: []string{
				"Show me all orders from last week",
				"List customers with overdue invoices",
				"Find products below reorder point",
			},
		},
		{
			Name:        "Forecasting",
			Description: "Predict future business metrics",
			Category:    "forecasting",
			Examples: []string{
				"Forecast next quarter's revenue",
				"Predict customer demand patterns",
				"Estimate upcoming expenses",
			},
		},
	}
}
