package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// defaultAnthropicModel is used when the tenant/env config leaves the model
// blank on the anthropic provider. claude-opus-5 is the current Opus-tier
// model; older dated claude-3-* ids are retired and 404.
const defaultAnthropicModel = "claude-opus-5"

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

// ImageAttachment is a base64 image attached to a user message (vision).
// Rendered provider-specifically: Anthropic image blocks / OpenAI image_url
// data URIs. Never serialized directly.
type ImageAttachment struct {
	MediaType  string `json:"-"` // e.g. image/jpeg, image/png, application/pdf
	DataBase64 string `json:"-"`
}

// Message represents a chat message. For the agent loop it also carries an
// assistant's tool_calls (when the model wants to call tools) and, on a tool
// result message, the tool_call_id it answers. Images may be attached to user
// messages for vision requests.
type Message struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	Name       string            `json:"name,omitempty"`
	ToolCalls  []ToolCall        `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Images     []ImageAttachment `json:"-"`
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
	PromptTokens             int `json:"prompt_tokens"`
	CompletionTokens         int `json:"completion_tokens"`
	TotalTokens              int `json:"total_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
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

// ==========================================================================
// OpenAI client
// ==========================================================================

// OpenAIClient implements the Client interface for OpenAI-compatible endpoints.
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

// openaiRenderMessage renders one Message. Plain messages keep the historical
// {role, content} string shape; user messages with images become the vision
// content-array shape.
func openaiRenderMessage(msg Message) map[string]interface{} {
	m := map[string]interface{}{"role": msg.Role}
	if len(msg.Images) > 0 && msg.Role == "user" {
		parts := make([]map[string]interface{}, 0, len(msg.Images)+1)
		for _, img := range msg.Images {
			parts = append(parts, map[string]interface{}{
				"type": "image_url",
				"image_url": map[string]interface{}{
					"url": "data:" + img.MediaType + ";base64," + img.DataBase64,
				},
			})
		}
		if msg.Content != "" {
			parts = append(parts, map[string]interface{}{"type": "text", "text": msg.Content})
		}
		m["content"] = parts
	} else {
		m["content"] = msg.Content
	}
	if msg.Name != "" {
		m["name"] = msg.Name
	}
	if len(msg.ToolCalls) > 0 {
		m["tool_calls"] = msg.ToolCalls
	}
	if msg.ToolCallID != "" {
		m["tool_call_id"] = msg.ToolCallID
	}
	return m
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

	msgs := make([]map[string]interface{}, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, openaiRenderMessage(m))
	}

	openaiReq := map[string]interface{}{
		"model":       req.Model,
		"messages":    msgs,
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

// ChatStream is a non-streaming fallback: it performs a blocking Chat and
// emits the whole answer as one chunk. Real SSE streaming is not implemented.
func (c *OpenAIClient) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	req.Stream = true
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

// CountTokens estimates token count (simplified)
func (c *OpenAIClient) CountTokens(text string) int {
	// Rough estimation: ~4 characters per token
	return len(text) / 4
}

// ==========================================================================
// Anthropic client
// ==========================================================================

// AnthropicClient implements the Client interface for Anthropic's Messages
// API, including tool use (the agent loop), vision and prompt caching.
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

// anthropic wire types -----------------------------------------------------

type anthropicCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type anthropicTextBlock struct {
	Type         string                 `json:"type"` // "text"
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicContentBlock is a request-side content block (only the fields we
// emit; the union is discriminated by Type).
type anthropicContentBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// image
	Source *anthropicImageSource `json:"source,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"` // "user" | "assistant"
	Content []anthropicContentBlock `json:"content"`
}

type anthropicRequest struct {
	Model     string               `json:"model"`
	MaxTokens int                  `json:"max_tokens"`
	System    []anthropicTextBlock `json:"system,omitempty"`
	Messages  []anthropicMessage   `json:"messages"`
	Tools     []anthropicTool      `json:"tools,omitempty"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// buildAnthropicMessages translates the provider-neutral message list into
// Anthropic's format:
//   - "system" messages are collected into the top-level system param
//   - assistant tool_calls become tool_use content blocks
//   - "tool" result messages become tool_result blocks; consecutive tool
//     results are merged into ONE user message (the API requires all results
//     for a turn in the single following user message)
//   - user images become image blocks before the text
func buildAnthropicMessages(in []Message) (system []anthropicTextBlock, out []anthropicMessage) {
	var pendingToolResults []anthropicContentBlock
	flushToolResults := func() {
		if len(pendingToolResults) > 0 {
			out = append(out, anthropicMessage{Role: "user", Content: pendingToolResults})
			pendingToolResults = nil
		}
	}

	for _, msg := range in {
		switch msg.Role {
		case "system":
			flushToolResults()
			if strings.TrimSpace(msg.Content) != "" {
				system = append(system, anthropicTextBlock{Type: "text", Text: msg.Content})
			}
		case "tool":
			pendingToolResults = append(pendingToolResults, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			})
		case "assistant":
			flushToolResults()
			blocks := make([]anthropicContentBlock, 0, len(msg.ToolCalls)+1)
			if strings.TrimSpace(msg.Content) != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				input := json.RawMessage(tc.Function.Arguments)
				if !json.Valid(input) || len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: ""})
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: blocks})
		default: // "user"
			flushToolResults()
			blocks := make([]anthropicContentBlock, 0, len(msg.Images)+1)
			for _, img := range msg.Images {
				blocks = append(blocks, anthropicContentBlock{
					Type: "image",
					Source: &anthropicImageSource{
						Type:      "base64",
						MediaType: img.MediaType,
						Data:      img.DataBase64,
					},
				})
			}
			if msg.Content != "" || len(blocks) == 0 {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: msg.Content})
			}
			out = append(out, anthropicMessage{Role: "user", Content: blocks})
		}
	}
	flushToolResults()
	return system, out
}

// Chat sends a chat completion request to Anthropic's Messages API with full
// tool-use support and prompt caching (breakpoints on the last tool and the
// system prompt, so the stable prefix is cached across agent-loop iterations).
func (c *AnthropicClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.config.Model
	}
	if model == "" {
		model = defaultAnthropicModel
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = c.config.MaxTokens
	}
	if maxTokens == 0 {
		maxTokens = 4096
	}

	system, messages := buildAnthropicMessages(req.Messages)

	// Cache breakpoint 1: the last tool definition (tools render before
	// system, so this caches the whole tool list).
	tools := make([]anthropicTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		params := t.Function.Parameters
		if params == nil {
			params = map[string]interface{}{"type": "object"}
		}
		tools = append(tools, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: params,
		})
	}
	if len(tools) > 0 {
		tools[len(tools)-1].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}
	// Cache breakpoint 2: the system prompt (caches tools + system together).
	if len(system) > 0 {
		system[len(system)-1].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}

	anthReq := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  messages,
		Tools:     tools,
	}

	body, err := json.Marshal(anthReq)
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

	var anthResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var textParts []string
	var toolCalls []ToolCall
	for _, block := range anthResp.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			args := "{}"
			if len(block.Input) > 0 {
				args = string(block.Input)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}

	msg := Message{
		Role:      "assistant",
		Content:   strings.Join(textParts, "\n"),
		ToolCalls: toolCalls,
	}

	return &ChatResponse{
		ID:      anthResp.ID,
		Model:   anthResp.Model,
		Message: msg,
		Usage: Usage{
			PromptTokens:             anthResp.Usage.InputTokens,
			CompletionTokens:         anthResp.Usage.OutputTokens,
			TotalTokens:              anthResp.Usage.InputTokens + anthResp.Usage.OutputTokens,
			CacheCreationInputTokens: anthResp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     anthResp.Usage.CacheReadInputTokens,
		},
		FinishReason: anthResp.StopReason,
		ToolCalls:    toolCalls,
	}, nil
}

// ChatStream is a non-streaming fallback: it performs a blocking Chat and
// emits the whole answer as one chunk. Real SSE streaming is not implemented.
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
