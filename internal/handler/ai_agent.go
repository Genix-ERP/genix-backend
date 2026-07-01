package handler

// AI Agent — an agentic layer over Genix ERP.
//
// The model is given a set of TOOLS, each mapped to a real ERP operation that
// is always scoped to the caller's tenant + active organization. READ tools run
// automatically inside the reasoning loop (the model can chain them:
// find_contacts -> list_sales_orders -> financial_summary). WRITE tools are
// never executed inside the loop: when the model asks for one, the loop stops
// and returns a `confirmation_required` payload; the client shows it and, only
// on explicit user approval, calls POST /ai/agent/execute to run that single
// action. This keeps a misunderstanding from writing straight to the DB.
//
// Phase 1 ships the framework + read tools + create_contact (a safe write to
// prove the confirm flow). Adding more write tools = one entry in agentTools().

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	aipkg "github.com/genixerp/genix-backend/internal/infrastructure/ai"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// agentTool is one capability exposed to the model.
type agentTool struct {
	name        string
	description string
	parameters  map[string]interface{} // JSON schema for the arguments
	mutating    bool                   // write tools require confirmation
	exec        func(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error)
}

const agentMaxIterations = 8

// obj/str/arr are tiny helpers to keep the JSON-schema literals readable.
func obj(props map[string]interface{}, required ...string) map[string]interface{} {
	m := map[string]interface{}{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func str(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}
func intp(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "integer", "description": desc}
}

// AIAgentChat runs the agentic reasoning loop.
// Body: { message: string, history?: [{role, content, ...}] }
func (h *Handler) AIAgentChat(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if orgID != uuid.Nil {
		orgArg = orgID
	}

	var req struct {
		Message string          `json:"message"`
		History []aipkg.Message `json:"history"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		response.BadRequest(c, "message is required")
		return
	}

	svc := h.getAIService()
	if svc == nil {
		response.Success(c, gin.H{"type": "message", "message": "AI is not configured. Set the tenant's AI provider/key in Admin → AI settings.", "model": "demo"})
		return
	}

	tools := agentTools()
	aiTools := make([]aipkg.Tool, 0, len(tools))
	for _, t := range tools {
		aiTools = append(aiTools, aipkg.Tool{Type: "function", Function: aipkg.Function{
			Name: t.name, Description: t.description, Parameters: t.parameters,
		}})
	}

	// Build the running message list: system + prior history + new user turn.
	msgs := []aipkg.Message{{Role: "system", Content: h.agentSystemPrompt(c)}}
	msgs = append(msgs, req.History...)
	msgs = append(msgs, aipkg.Message{Role: "user", Content: req.Message})

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.config.AI.RequestTimeout)
	defer cancel()

	steps := make([]gin.H, 0) // trace of tool calls, surfaced to the UI
	for i := 0; i < agentMaxIterations; i++ {
		if err := svc.rateLimiter.Wait(ctx); err != nil {
			response.TooManyRequests(c, "Rate limit exceeded")
			return
		}
		resp, err := svc.client.Chat(ctx, &aipkg.ChatRequest{Messages: msgs, Tools: aiTools})
		if err != nil {
			h.log.Error("agent: chat error", "error", err)
			response.InternalError(c, "AI request failed: "+err.Error())
			return
		}

		// No tool calls → the model produced its final answer.
		if len(resp.ToolCalls) == 0 {
			response.Success(c, gin.H{
				"type": "message", "message": resp.Message.Content,
				"model": resp.Model, "steps": steps,
				"history": append(msgs[1:], aipkg.Message{Role: "assistant", Content: resp.Message.Content}),
			})
			return
		}

		// Record the assistant's tool-call turn in the history.
		msgs = append(msgs, aipkg.Message{Role: "assistant", Content: resp.Message.Content, ToolCalls: resp.ToolCalls})

		for _, tc := range resp.ToolCalls {
			tool := findTool(tools, tc.Function.Name)
			var args map[string]interface{}
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

			if tool == nil {
				msgs = append(msgs, aipkg.Message{Role: "tool", ToolCallID: tc.ID, Content: `{"error":"unknown tool"}`})
				continue
			}

			// WRITE tool → stop and ask the user to confirm.
			if tool.mutating {
				response.Success(c, gin.H{
					"type": "confirmation_required",
					"pending_action": gin.H{
						"tool":    tool.name,
						"args":    args,
						"summary": summariseAction(tool.name, args),
					},
					"assistant_note": resp.Message.Content,
					"steps":          steps,
				})
				return
			}

			// READ tool → run it and feed the result back.
			result, execErr := tool.exec(h, c, tenantID, orgArg, userID, args)
			payload := gin.H{"ok": execErr == nil}
			if execErr != nil {
				payload["error"] = execErr.Error()
			} else {
				payload["data"] = result
			}
			b, _ := json.Marshal(payload)
			msgs = append(msgs, aipkg.Message{Role: "tool", ToolCallID: tc.ID, Content: string(b)})
			steps = append(steps, gin.H{"tool": tool.name, "args": args, "ok": execErr == nil})
		}
	}

	response.Success(c, gin.H{"type": "message", "message": "I couldn't finish that in the allowed number of steps — please narrow the request.", "steps": steps})
}

// AIAgentExecute runs ONE write tool after the user approved it in the UI.
// Body: { tool: string, args: object }
func (h *Handler) AIAgentExecute(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if orgID != uuid.Nil {
		orgArg = orgID
	}

	var req struct {
		Tool string                 `json:"tool"`
		Args map[string]interface{} `json:"args"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Tool == "" {
		response.BadRequest(c, "tool is required")
		return
	}
	tool := findTool(agentTools(), req.Tool)
	if tool == nil || !tool.mutating {
		response.BadRequest(c, "Unknown or non-executable action")
		return
	}
	result, err := tool.exec(h, c, tenantID, orgArg, userID, req.Args)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"ok": true, "data": result, "summary": summariseAction(tool.name, req.Args)})
}

func findTool(tools []agentTool, name string) *agentTool {
	for i := range tools {
		if tools[i].name == name {
			return &tools[i]
		}
	}
	return nil
}

func (h *Handler) agentSystemPrompt(c *gin.Context) string {
	return `You are the Genix ERP Agent — an assistant embedded INSIDE the Genix ERP for this specific company. You can call TOOLS to read the company's real data and to propose changes.

Rules:
- ALWAYS use tools to get real data; never invent numbers, names, ids, or stock levels. If a tool returns nothing, say so.
- Chain read tools as needed (e.g. find the customer, then list their orders).
- For any action that CHANGES data, call the matching write tool with precise arguments; the system will pause and ask the user to confirm before it runs — so state clearly what you're about to do.
- Only answer questions about THIS company's ERP (sales, purchases, inventory, finance, manufacturing, customers/vendors). Politely decline unrelated requests.
- Reply in the SAME language as the user (Uzbek, Russian, or English). Be concise; use short markdown.
- Money is in so'm unless stated otherwise. Today's data is whatever the tools return.`
}

// summariseAction produces a short human confirmation line for a write tool.
func summariseAction(tool string, args map[string]interface{}) string {
	switch tool {
	case "create_contact":
		return fmt.Sprintf("Create a new %v named %q", args["type"], args["name"])
	}
	return "Run " + tool
}
