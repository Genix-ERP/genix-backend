package handler

// AI Agent — an agentic layer over Genix ERP.
//
// The model is given a set of TOOLS, each mapped to a real ERP operation that
// is always scoped to the caller's tenant + active organization. READ tools run
// automatically inside the reasoning loop (the model can chain them:
// find_contacts -> list_sales_orders -> financial_summary). WRITE tools are
// never executed inside the loop: when the model asks for one, the loop stops
// and returns a `confirmation_required` payload together with the resumable
// conversation history. The client shows the confirmation card and, only on
// explicit user approval, re-calls POST /ai/agent with that history + an
// `approved` action. The handler executes the single write, feeds the result
// back to the model, and lets it continue reasoning (e.g. confirm + suggest a
// next step). This keeps a misunderstanding from writing straight to the DB
// while still giving one continuous agentic conversation.
//
// Adding a capability = one entry in agentTools(): read tools are pure SELECTs;
// write tools set mutating:true and go through the confirm-then-continue flow.
// (POST /ai/agent/execute remains as a stateless single-shot executor.)

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

const agentMaxIterations = 12

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
		Message  string          `json:"message"`
		History  []aipkg.Message `json:"history"`
		Approved *struct {
			Tool string                 `json:"tool"`
			Args map[string]interface{} `json:"args"`
		} `json:"approved"` // a write the user just confirmed — execute then continue
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Approved == nil && strings.TrimSpace(req.Message) == "") {
		response.BadRequest(c, "message or approved action is required")
		return
	}

	svc := h.getAIService(tenantID)
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

	// Build the running message list: system + prior history + the new turn.
	msgs := []aipkg.Message{{Role: "system", Content: h.agentSystemPrompt(c)}}
	msgs = append(msgs, req.History...)

	steps := make([]gin.H, 0) // trace of tool calls, surfaced to the UI

	if req.Approved != nil {
		// The user approved a write in the confirmation card — execute it now,
		// then let the model continue reasoning from the result.
		tool := findTool(tools, req.Approved.Tool)
		if tool == nil || !tool.mutating {
			response.BadRequest(c, "Unknown or non-executable action")
			return
		}
		if reason := h.agentToolDenied(c, tool.name); reason != "" {
			response.Forbidden(c, "You do not have permission for this action ("+reason+")")
			return
		}
		result, execErr := tool.exec(h, c, tenantID, orgArg, userID, req.Approved.Args)
		var note string
		if execErr != nil {
			note = fmt.Sprintf("[system: the approved action %q FAILED: %s. Explain this to the user.]", req.Approved.Tool, execErr.Error())
			steps = append(steps, gin.H{"tool": req.Approved.Tool, "args": req.Approved.Args, "ok": false, "executed": true})
		} else {
			b, _ := json.Marshal(result)
			note = fmt.Sprintf("[system: the user approved and you executed %q. Result: %s. Confirm it briefly and ask if anything else is needed.]", req.Approved.Tool, string(b))
			steps = append(steps, gin.H{"tool": req.Approved.Tool, "args": req.Approved.Args, "ok": true, "executed": true})
		}
		msgs = append(msgs, aipkg.Message{Role: "user", Content: note})
	} else {
		msgs = append(msgs, aipkg.Message{Role: "user", Content: req.Message})
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.config.AI.RequestTimeout)
	defer cancel()

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

		// preTurn = history up to (excluding) this assistant tool-call turn. If we
		// hit a write we return THIS as the resumable history, so it never carries
		// a dangling tool_call the API would reject on resume.
		preTurn := make([]aipkg.Message, len(msgs[1:]))
		copy(preTurn, msgs[1:])

		// Record the assistant's tool-call turn in the running list.
		msgs = append(msgs, aipkg.Message{Role: "assistant", Content: resp.Message.Content, ToolCalls: resp.ToolCalls})

		for _, tc := range resp.ToolCalls {
			tool := findTool(tools, tc.Function.Name)
			var args map[string]interface{}
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

			if tool == nil {
				msgs = append(msgs, aipkg.Message{Role: "tool", ToolCallID: tc.ID, Content: `{"error":"unknown tool"}`})
				continue
			}

			// Permission gate — the agent may only do what THIS user could do in
			// the ERP. Denied tools (read or write) never execute and a write never
			// reaches the confirmation card.
			if reason := h.agentToolDenied(c, tool.name); reason != "" {
				msgs = append(msgs, aipkg.Message{Role: "tool", ToolCallID: tc.ID,
					Content: fmt.Sprintf(`{"error":"permission denied: the user does not have the %q permission. Do not retry; tell them they don't have access to this."}`, reason)})
				steps = append(steps, gin.H{"tool": tool.name, "args": args, "ok": false, "denied": true})
				continue
			}

			// WRITE tool → stop and ask the user to confirm. On approval the client
			// resends `preTurn` as history + the approved action.
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
					"history":        preTurn,
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
	if reason := h.agentToolDenied(c, tool.name); reason != "" {
		response.Forbidden(c, "You do not have permission for this action ("+reason+")")
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

// toolPerms maps each tool to the RBAC node it requires (module, resource,
// action) — the SAME permission the corresponding ERP route enforces. A tool
// absent from this map corresponds to an ERP route that has no perm.Require
// guard (contact reads stay open — every module's partner picker needs them)
// and so is available to any tenant user. The agent mirrors the ERP's own
// gating exactly: never stricter, never looser than what the user could
// already do through the normal UI/API. CRM lead/opportunity/report routes
// are gated since migration 446.
var toolPerms = map[string][3]string{
	// ---- reads ----
	"find_products":              {"inventory", "product", "read"},
	"check_stock":                {"inventory", "stock", "read"},
	"low_stock_products":         {"inventory", "stock", "read"},
	"inventory_valuation":        {"inventory", "stock", "read"},
	"stock_movements":            {"inventory", "stock", "read"},
	"list_stock_counts":          {"inventory", "stock", "read"},
	"list_boms":                  {"inventory", "bom", "read"},
	"get_bom":                    {"inventory", "bom", "read"},
	"list_sales_orders":          {"sales", "order", "read"},
	"get_sales_order":            {"sales", "order", "read"},
	"list_sales_invoices":        {"sales", "invoice", "read"},
	"customer_statement":         {"sales", "invoice", "read"},
	"aged_receivables":           {"sales", "invoice", "read"},
	"sales_summary":              {"sales", "invoice", "read"},
	"list_quotations":            {"sales", "quotation", "read"},
	"list_sales_returns":         {"sales", "return", "read"},
	"list_purchase_orders":       {"purchase", "order", "read"},
	"get_purchase_order":         {"purchase", "order", "read"},
	"supplier_prices":            {"purchase", "price_history", "read"},
	"list_vendor_bills":          {"purchase", "invoice", "read"},
	"aged_payables":              {"purchase", "invoice", "read"},
	"list_contracts":             {"purchase", "contract", "read"},
	"get_contract":               {"purchase", "contract", "read"},
	"create_contract":            {"purchase", "contract", "create"},
	"list_goods_receipts":        {"purchase", "receipt", "read"},
	"list_purchase_requisitions": {"purchase", "requisition", "read"},
	"list_rfqs":                  {"purchase", "rfq", "read"},
	"list_purchase_returns":      {"purchase", "return", "read"},
	"financial_summary":          {"finance", "account", "read"},
	"business_overview":          {"finance", "report", "read"},
	"list_bank_accounts":         {"finance", "bank_account", "read"},
	"list_payments":              {"finance", "payment", "read"},
	"list_expenses":              {"finance", "expense", "read"},
	"list_journal_entries":       {"finance", "journal_entry", "read"},
	"list_fixed_assets":          {"finance", "asset", "read"},
	"tax_summary":                {"finance", "tax_report", "read"},
	"list_production_orders":     {"manufacturing", "production_orders", "read"},
	"list_work_orders":           {"manufacturing", "work_orders", "read"},
	"find_employees":             {"hr", "employee", "read"},
	"list_leave_requests":        {"hr", "leave", "read"},
	"list_attendance":            {"hr", "attendance", "read"},
	"list_payroll_periods":       {"hr", "payroll", "read"},
	"list_projects":              {"construction", "project", "read"},
	"list_workflows":             {"workflow", "workflow", "read"},
	// CRM routes are gated since migration 446 — the tools mirror that.
	"list_leads":         {"crm", "lead", "read"},
	"list_opportunities": {"crm", "opportunity", "read"},
	"crm_summary":        {"crm", "report", "read"},
	// ---- writes ----
	"create_contact":       {"crm", "contact", "create"},
	"create_lead":          {"crm", "lead", "create"},
	"create_sales_order":   {"sales", "order", "create"},
	"create_quotation":     {"sales", "quotation", "create"},
	"create_sales_invoice": {"sales", "invoice", "create"},
	"create_vendor_bill":   {"purchase", "invoice", "create"},
	"record_payment":       {"finance", "payment", "create"},
	"stock_adjust":         {"inventory", "stock", "adjust"},
	"stock_transfer":       {"inventory", "stock", "transfer"},
	"create_workflow":      {"workflow", "workflow", "create"},
	"set_workflow_status":  {"workflow", "workflow", "update"},
}

// agentToolDenied returns the required permission node (module:resource:action)
// if the caller may NOT run the named tool, or "" if allowed. Fails closed when
// a checker is present and denies; allows only when the tool is ungated in the
// ERP or no checker is configured (dev/test).
func (h *Handler) agentToolDenied(c *gin.Context, name string) string {
	p, ok := toolPerms[name]
	if !ok {
		return "" // ungated ERP route → available to any tenant user
	}
	if h.perm == nil || h.perm.Can(c, p[0], p[1], p[2]) {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s", p[0], p[1], p[2])
}

func (h *Handler) agentSystemPrompt(c *gin.Context) string {
	return `You are the Genix ERP Agent — an assistant embedded INSIDE the Genix ERP, working for THIS specific company and its currently active organization. You call TOOLS to read the company's real data and to carry out work.

What you can do with tools:
- Look things up: customers/vendors, products & stock (per warehouse), sales orders/invoices/quotations/returns, purchase orders/bills/requisitions/RFQs/returns, goods receipts, payments, expenses, production orders, work orders, BOMs, stock counts, employees, construction projects, fixed assets, procurement contracts, automation workflows, CRM leads & opportunities (pipeline), general-ledger journal entries, HR leave/attendance/payroll.
- Report & analyse: a whole-business overview snapshot, financial summary, cash/bank position, aged receivables & payables, sales totals for a period, VAT/NDS tax summary, a customer statement, and full drill-downs of one sales/purchase order.
- Take actions (each pauses for user confirmation): create a customer/vendor, create a DRAFT sales order or sales invoice, create a DRAFT vendor bill, record a DRAFT payment, adjust stock after a count, transfer stock between warehouses, create a DRAFT automation workflow, and activate/pause a workflow.

How to work:
- ALWAYS use tools for real data; never invent numbers, names, ids, or stock. If a tool returns nothing, say so plainly.
- Call ONE tool per step and read its result before the next.
- Before ANY write, resolve names to real records with a find_/list_ tool first (e.g. find the customer and each product), so the action targets the right ids.
- When the user asks you to DO something (create, record, adjust, transfer), actually CALL the write tool with precise arguments — do not merely describe it. The system then shows the user a confirmation card; on approval it runs and you continue. State clearly what you are about to do.
- Writes you create are DRAFTS: tell the user to review and confirm them in the app to post the accounting/inventory effects.
- You act with the SAME permissions as the user. A tool may return a "permission denied" error — if so, briefly tell the user they don't have access to that area; do NOT retry it or try another tool to get around it.
- Only handle THIS company's ERP work; politely decline unrelated requests.
- Reply in the SAME language as the user (Uzbek, Russian, or English). Be concise; use short markdown and tables for lists.
- Money is in so'm unless a currency is given. Today's data is whatever the tools return.`
}

// summariseAction produces a short human confirmation line for a write tool.
func summariseAction(tool string, args map[string]interface{}) string {
	switch tool {
	case "create_contact":
		return fmt.Sprintf("Create a new %v named %q", args["type"], args["name"])
	case "create_sales_order":
		return fmt.Sprintf("Create a DRAFT sales order for %v", args["customer"])
	case "record_payment":
		return fmt.Sprintf("Record a DRAFT %v payment of %v for %v", args["direction"], args["amount"], args["contact"])
	case "create_sales_invoice":
		return fmt.Sprintf("Create a DRAFT sales invoice for %v", args["customer"])
	case "create_vendor_bill":
		return fmt.Sprintf("Create a DRAFT vendor bill of %v for %v", args["amount"], args["vendor"])
	case "stock_adjust":
		return fmt.Sprintf("Set %v stock in %v to %v", args["product"], args["warehouse"], args["new_quantity"])
	case "stock_transfer":
		return fmt.Sprintf("Transfer %v %v from %v to %v", args["quantity"], args["product"], args["from_warehouse"], args["to_warehouse"])
	case "create_workflow":
		return fmt.Sprintf("Create a DRAFT %v workflow %q", args["category"], args["name"])
	case "set_workflow_status":
		return fmt.Sprintf("Set workflow %q to %v", args["name"], args["status"])
	}
	return "Run " + tool
}
