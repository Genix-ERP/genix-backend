package handler

import (
	"context"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/infrastructure/ai"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AIService handles AI-related operations
type AIService struct {
	client      ai.Client
	rateLimiter *ai.RateLimiter
}

// NewAIService creates a new AI service
func (h *Handler) getAIService() *AIService {
	if h.config.AI.APIKey == "" {
		return nil
	}
	return &AIService{
		client:      ai.NewClient(h.config.AI),
		rateLimiter: ai.NewRateLimiter(h.config.AI.RateLimitPerMin),
	}
}

// AIChatRequest represents a chat request
type AIChatRequest struct {
	Message       string                 `json:"message" binding:"required"`
	Context       map[string]interface{} `json:"context,omitempty"`
	SystemPrompt  string                 `json:"system_prompt,omitempty"`
	Model         string                 `json:"model,omitempty"`
	MaxTokens     int                    `json:"max_tokens,omitempty"`
	Temperature   float64                `json:"temperature,omitempty"`
}

// AIChat handles AI chat requests
func (h *Handler) AIChat(c *gin.Context) {
	var req AIChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	aiService := h.getAIService()

	// If no AI API key configured, return demo response
	if aiService == nil {
		demoResponse := h.generateDemoResponse(req.Message, req.Context)
		response.Success(c, gin.H{
			"message": gin.H{
				"role":    "assistant",
				"content": demoResponse,
			},
			"model":  "demo",
			"usage": gin.H{
				"prompt_tokens":     0,
				"completion_tokens": 0,
				"total_tokens":      0,
			},
		})
		return
	}

	// Check rate limit
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.config.AI.RequestTimeout)
	defer cancel()

	if err := aiService.rateLimiter.Wait(ctx); err != nil {
		response.TooManyRequests(c, "Rate limit exceeded, please try again later")
		return
	}

	// Build messages
	messages := []ai.Message{}

	// Add system prompt
	systemPrompt := req.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = h.getDefaultSystemPrompt(req.Context)
	}
	messages = append(messages, ai.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	// Add user message
	messages = append(messages, ai.Message{
		Role:    "user",
		Content: req.Message,
	})

	// Create chat request
	chatReq := &ai.ChatRequest{
		Model:       req.Model,
		Messages:    messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	// Call AI
	chatResp, err := aiService.client.Chat(ctx, chatReq)
	if err != nil {
		h.log.Error("AI chat error", "error", err)
		// Fallback to demo response on error
		demoResponse := h.generateDemoResponse(req.Message, req.Context)
		response.Success(c, gin.H{
			"message": gin.H{
				"role":    "assistant",
				"content": demoResponse,
			},
			"model":  "demo-fallback",
			"error":  "AI service temporarily unavailable, using fallback",
			"usage": gin.H{
				"prompt_tokens":     0,
				"completion_tokens": 0,
				"total_tokens":      0,
			},
		})
		return
	}

	response.Success(c, gin.H{
		"message": gin.H{
			"role":    chatResp.Message.Role,
			"content": chatResp.Message.Content,
		},
		"model": chatResp.Model,
		"usage": gin.H{
			"prompt_tokens":     chatResp.Usage.PromptTokens,
			"completion_tokens": chatResp.Usage.CompletionTokens,
			"total_tokens":      chatResp.Usage.TotalTokens,
		},
		"finish_reason": chatResp.FinishReason,
	})
}

// getDefaultSystemPrompt returns the default system prompt for GenixERP
func (h *Handler) getDefaultSystemPrompt(context map[string]interface{}) string {
	prompt := `You are GenixERP AI Assistant, an intelligent business advisor integrated into an Enterprise Resource Planning system.

Your capabilities include:
- Analyzing sales data, trends, and customer behavior
- Optimizing inventory levels and predicting stock needs
- Providing financial insights and recommendations
- Generating reports and business intelligence
- Helping with process automation suggestions
- Answering questions about ERP data and operations

Guidelines:
- Be concise but thorough in your responses
- Use data and metrics when available
- Provide actionable recommendations
- Format responses with markdown for readability
- When analyzing data, highlight key insights first
- Always consider business impact in your suggestions

Current context:
- Date: ` + time.Now().Format("2006-01-02")

	if context != nil {
		if module, ok := context["module"].(string); ok {
			prompt += "\n- Active module: " + module
		}
		if page, ok := context["page"].(string); ok {
			prompt += "\n- Current page: " + page
		}
	}

	return prompt
}

// generateDemoResponse generates a demo response when AI is not configured
func (h *Handler) generateDemoResponse(message string, context map[string]interface{}) string {
	msg := strings.ToLower(message)

	// Check for common intents
	if strings.Contains(msg, "inventory") || strings.Contains(msg, "stock") {
		return `## Inventory Analysis

Based on your inventory data:

**Key Metrics:**
- Monitor items with stock below reorder levels
- Review slow-moving inventory for potential clearance
- Optimize reorder quantities based on demand patterns

**Recommendations:**
1. Set up automated reorder alerts for critical items
2. Review ABC classification to prioritize high-value items
3. Consider just-in-time ordering for fast-moving products

*Note: Connect an AI provider (OpenAI/Anthropic) for detailed analysis of your actual inventory data.*`
	}

	if strings.Contains(msg, "sales") || strings.Contains(msg, "revenue") {
		return `## Sales Insights

**Overview:**
Your sales performance can be optimized by:

1. **Customer Segmentation** - Identify high-value customers for targeted campaigns
2. **Product Analysis** - Focus on top-performing products
3. **Trend Monitoring** - Track seasonal patterns and adjust strategy

**Quick Actions:**
- Review customers with declining orders
- Analyze product margins for optimization opportunities
- Set up alerts for large order opportunities

*Note: Connect an AI provider (OpenAI/Anthropic) for real-time analysis of your sales data.*`
	}

	if strings.Contains(msg, "customer") || strings.Contains(msg, "crm") {
		return `## CRM Insights

**Customer Health:**
- Track engagement levels and order frequency
- Monitor customer lifetime value trends
- Identify at-risk customers for retention efforts

**Pipeline Management:**
- Prioritize high-probability opportunities
- Follow up on stale leads
- Nurture relationships with key accounts

*Note: Connect an AI provider (OpenAI/Anthropic) for personalized customer insights.*`
	}

	if strings.Contains(msg, "financial") || strings.Contains(msg, "finance") || strings.Contains(msg, "profit") {
		return `## Financial Overview

**Key Areas to Monitor:**
1. **Cash Flow** - Track receivables and payables aging
2. **Profitability** - Analyze margins by product/customer
3. **Expenses** - Identify cost optimization opportunities

**Recommendations:**
- Review overdue invoices regularly
- Set up early payment incentives
- Monitor expense ratios

*Note: Connect an AI provider (OpenAI/Anthropic) for detailed financial analysis.*`
	}

	// Default response
	return `## GenixERP AI Assistant

I can help you with:

- **📊 Sales Analysis** - Trends, forecasts, customer insights
- **📦 Inventory Management** - Stock levels, reorder optimization
- **💰 Financial Insights** - Cash flow, profitability, expenses
- **👥 CRM Intelligence** - Customer health, pipeline management
- **📈 Business Reports** - Custom analytics and KPIs

**Try asking:**
- "Analyze my sales performance"
- "Which inventory items need attention?"
- "Show me customer insights"
- "What are my financial recommendations?"

*Note: For full AI capabilities, configure your OpenAI or Anthropic API key in the backend settings.*`
}

// GetAICapabilities returns available AI capabilities
func (h *Handler) GetAICapabilities(c *gin.Context) {
	capabilities := entity.GetAICapabilities()

	// Add status info
	aiStatus := "demo"
	if h.config.AI.APIKey != "" {
		aiStatus = "active"
	}

	response.Success(c, gin.H{
		"status":       aiStatus,
		"provider":     h.config.AI.Provider,
		"model":        h.config.AI.Model,
		"capabilities": capabilities,
	})
}

// ========== Conversations ==========

// ListAIConversations lists AI conversations (stub - would need database implementation)
func (h *Handler) ListAIConversations(c *gin.Context) {
	// TODO: Implement with database
	response.Success(c, []interface{}{})
}

// CreateAIConversation creates a new AI conversation
func (h *Handler) CreateAIConversation(c *gin.Context) {
	var input entity.CreateConversationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}

	// TODO: Save to database
	conversation := gin.H{
		"id":         uuid.New().String(),
		"title":      input.Title,
		"context":    input.Context,
		"created_at": time.Now(),
	}

	response.Created(c, conversation)
}

// GetAIConversation gets an AI conversation by ID
func (h *Handler) GetAIConversation(c *gin.Context) {
	// TODO: Implement with database
	response.NotFound(c, "Conversation")
}

// DeleteAIConversation deletes an AI conversation
func (h *Handler) DeleteAIConversation(c *gin.Context) {
	// TODO: Implement with database
	response.NoContent(c)
}

// AddAIMessage adds a message to a conversation
func (h *Handler) AddAIMessage(c *gin.Context) {
	// TODO: Implement with database
	response.Created(c, gin.H{"message": "Message added"})
}

// ========== Prompts ==========

// ListAIPrompts lists AI prompts
func (h *Handler) ListAIPrompts(c *gin.Context) {
	// Return some default prompts
	prompts := []gin.H{
		{
			"id":          uuid.New().String(),
			"name":        "Sales Analysis",
			"description": "Analyze sales performance and trends",
			"category":    "analysis",
			"template":    "Analyze the sales data and provide insights on performance, trends, and recommendations.",
			"is_system":   true,
		},
		{
			"id":          uuid.New().String(),
			"name":        "Inventory Optimization",
			"description": "Optimize inventory levels",
			"category":    "forecasting",
			"template":    "Review inventory levels and suggest optimizations for stock management.",
			"is_system":   true,
		},
		{
			"id":          uuid.New().String(),
			"name":        "Financial Report",
			"description": "Generate financial insights",
			"category":    "reporting",
			"template":    "Analyze financial data and generate a comprehensive report with key metrics.",
			"is_system":   true,
		},
	}

	response.Success(c, prompts)
}

// CreateAIPrompt creates a new AI prompt
func (h *Handler) CreateAIPrompt(c *gin.Context) {
	var input entity.CreatePromptInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}

	prompt := gin.H{
		"id":              uuid.New().String(),
		"name":            input.Name,
		"description":     input.Description,
		"category":        input.Category,
		"prompt_template": input.PromptTemplate,
		"variables":       input.Variables,
		"is_system":       false,
		"created_at":      time.Now(),
	}

	response.Created(c, prompt)
}

// GetAIPrompt gets an AI prompt by ID
func (h *Handler) GetAIPrompt(c *gin.Context) {
	response.NotFound(c, "Prompt")
}

// UpdateAIPrompt updates an AI prompt
func (h *Handler) UpdateAIPrompt(c *gin.Context) {
	response.Success(c, gin.H{"message": "Prompt updated"})
}

// DeleteAIPrompt deletes an AI prompt
func (h *Handler) DeleteAIPrompt(c *gin.Context) {
	response.NoContent(c)
}
