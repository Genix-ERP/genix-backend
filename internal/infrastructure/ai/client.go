package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
)

// RequireSecureEndpoint refuses to send data to a non-HTTPS AI endpoint so the
// tenant's business data is always encrypted in transit (TLS). Plain http is
// allowed ONLY for localhost (local dev). An empty endpoint means the provider's
// default, which is always https.
func RequireSecureEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid AI endpoint: %w", err)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil // local development only
		}
	}
	return fmt.Errorf("refusing to send data to a non-HTTPS AI endpoint (%q): configure an https:// URL so data is encrypted in transit", endpoint)
}

// Provider represents an AI provider
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderLocal     Provider = "local"
)

// Client represents an AI client interface
type Client interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
	CountTokens(text string) int
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Model       string     `json:"model"`
	Messages    []Message  `json:"messages"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
	Temperature float64    `json:"temperature,omitempty"`
	Stream      bool       `json:"stream,omitempty"`
	Functions   []Function `json:"functions,omitempty"` // legacy
	Tools       []Tool     `json:"tools,omitempty"`     // modern function/tool calling
}

// Message represents a chat message. For the agent loop it also carries an
// assistant's tool_calls (when the model wants to call tools) and, on a tool
// result message, the tool_call_id it answers.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Function represents a callable function's schema.
type Function struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Tool wraps a Function for the modern "tools" API.
type Tool struct {
	Type     string   `json:"type"` // always "function"
	Function Function `json:"function"`
}

// ToolCall is one tool invocation the model asked for.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// ChatResponse represents a chat completion response
type ChatResponse struct {
	ID           string        `json:"id"`
	Model        string        `json:"model"`
	Message      Message       `json:"message"`
	Usage        Usage         `json:"usage"`
	FinishReason string        `json:"finish_reason"`
	FunctionCall *FunctionCall `json:"function_call,omitempty"` // legacy
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`    // modern
}

// Usage represents token usage
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// FunctionCall represents a function call made by the AI
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamChunk represents a streaming response chunk
type StreamChunk struct {
	Content      string `json:"content"`
	Done         bool   `json:"done"`
	Error        error  `json:"error,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
}

// OpenAIClient implements the Client interface for OpenAI
type OpenAIClient struct {
	config     config.AIConfig
	httpClient *http.Client
}

// NewOpenAIClient creates a new OpenAI client
func NewOpenAIClient(cfg config.AIConfig) *OpenAIClient {
	return &OpenAIClient{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}
}

// Chat sends a chat completion request to OpenAI
func (c *OpenAIClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.config.Model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = c.config.MaxTokens
	}
	if req.Temperature == 0 {
		req.Temperature = c.config.Temperature
	}

	// Build OpenAI request
	openaiReq := map[string]interface{}{
		"model":       req.Model,
		"messages":    req.Messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	}

	if len(req.Functions) > 0 {
		openaiReq["functions"] = req.Functions
	}
	if len(req.Tools) > 0 {
		openaiReq["tools"] = req.Tools
	}

	body, err := json.Marshal(openaiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := c.config.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}
	if err := RequireSecureEndpoint(endpoint); err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %s - %s", resp.Status, string(bodyBytes))
	}

	var openaiResp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(openaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := openaiResp.Choices[0]
	return &ChatResponse{
		ID:           openaiResp.ID,
		Model:        openaiResp.Model,
		Message:      choice.Message,
		Usage:        openaiResp.Usage,
		FinishReason: choice.FinishReason,
		ToolCalls:    choice.Message.ToolCalls, // tool_calls live inside message
	}, nil
}

// ChatStream sends a streaming chat request
func (c *OpenAIClient) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	req.Stream = true
	ch := make(chan StreamChunk)

	// Implementation would use SSE for streaming
	// For now, return a simple implementation
	go func() {
		defer close(ch)
		resp, err := c.Chat(ctx, req)
		if err != nil {
			ch <- StreamChunk{Error: err}
			return
		}
		ch <- StreamChunk{
			Content:      resp.Message.Content,
			Done:         true,
			FinishReason: resp.FinishReason,
		}
	}()

	return ch, nil
}

// CountTokens estimates token count (simplified)
func (c *OpenAIClient) CountTokens(text string) int {
	// Rough estimation: ~4 characters per token
	return len(text) / 4
}

// AnthropicClient implements the Client interface for Anthropic
type AnthropicClient struct {
	config     config.AIConfig
	httpClient *http.Client
}

// NewAnthropicClient creates a new Anthropic client
func NewAnthropicClient(cfg config.AIConfig) *AnthropicClient {
	return &AnthropicClient{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}
}

// Chat sends a chat completion request to Anthropic
func (c *AnthropicClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = "claude-3-opus-20240229"
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = c.config.MaxTokens
	}

	// Convert messages to Anthropic format
	var systemPrompt string
	var messages []map[string]string

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			systemPrompt = msg.Content
			continue
		}
		messages = append(messages, map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}

	anthropicReq := map[string]interface{}{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": req.MaxTokens,
	}

	if systemPrompt != "" {
		anthropicReq["system"] = systemPrompt
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := c.config.Endpoint
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	if err := RequireSecureEndpoint(endpoint); err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.config.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %s - %s", resp.Status, string(bodyBytes))
	}

	var anthropicResp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	content := ""
	if len(anthropicResp.Content) > 0 {
		content = anthropicResp.Content[0].Text
	}

	return &ChatResponse{
		ID:    anthropicResp.ID,
		Model: anthropicResp.Model,
		Message: Message{
			Role:    "assistant",
			Content: content,
		},
		Usage: Usage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
		},
		FinishReason: anthropicResp.StopReason,
	}, nil
}

// ChatStream sends a streaming chat request
func (c *AnthropicClient) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk)

	go func() {
		defer close(ch)
		resp, err := c.Chat(ctx, req)
		if err != nil {
			ch <- StreamChunk{Error: err}
			return
		}
		ch <- StreamChunk{
			Content:      resp.Message.Content,
			Done:         true,
			FinishReason: resp.FinishReason,
		}
	}()

	return ch, nil
}

// CountTokens estimates token count
func (c *AnthropicClient) CountTokens(text string) int {
	return len(text) / 4
}

// NewClient creates an AI client based on the provider
func NewClient(cfg config.AIConfig) Client {
	switch Provider(cfg.Provider) {
	case ProviderAnthropic:
		return NewAnthropicClient(cfg)
	case ProviderOpenAI:
		fallthrough
	default:
		return NewOpenAIClient(cfg)
	}
}

// RateLimiter implements rate limiting for AI requests
type RateLimiter struct {
	requests  int
	window    time.Duration
	lastReset time.Time
	count     int
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(requestsPerMinute int) *RateLimiter {
	return &RateLimiter{
		requests:  requestsPerMinute,
		window:    time.Minute,
		lastReset: time.Now(),
	}
}

// Allow checks if a request is allowed
func (r *RateLimiter) Allow() bool {
	now := time.Now()
	if now.Sub(r.lastReset) >= r.window {
		r.count = 0
		r.lastReset = now
	}

	if r.count >= r.requests {
		return false
	}

	r.count++
	return true
}

// Wait waits until a request is allowed
func (r *RateLimiter) Wait(ctx context.Context) error {
	for !r.Allow() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil
}
