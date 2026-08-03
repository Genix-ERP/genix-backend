package handler

// Workflow automation engine v2.
//
// Modules emit domain events with EmitWorkflowEvent (fire-and-forget, panic
// safe). The engine matches active rules for the tenant + event, evaluates
// AND/OR condition groups, executes the ordered actions and writes one
// workflow_logs row per rule run (success / partial / failed /
// skipped_conditions) with duration and a link to the triggering record.
//
// Safety:
//   - loop guard: an event chain carries its depth and the rule IDs already
//     fired; depth is capped and a rule can never re-trigger itself.
//   - rate guard: a rule firing more than maxRuleRunsPerMinute times/min or
//     failing repeatedly is auto-paused and its creator notified.
//   - scheduler dedupe: scanned events (overdue invoice/task, expiring
//     contract, low stock) fire once per record+rule via workflow_fired_markers.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	maxWorkflowDepth     = 2
	maxRuleRunsPerMinute = 30
	maxRuleFailsPer10Min = 10
	workflowLogRetention = 90 * 24 * time.Hour
	markerRetention      = 400 * 24 * time.Hour
)

// workflowEventDef describes one entry of the trigger-event catalog.
type workflowEventDef struct {
	Category    string                 // module the event belongs to
	RelatedType string                 // record type used for log links
	Scheduled   bool                   // emitted by the scheduler scan, not a live operation
	SampleData  map[string]interface{} // payload used by the dry-run test endpoint
}

// workflowEventCatalog is the server-side source of truth for valid trigger
// events. The frontend catalog must stay a subset of this.
var workflowEventCatalog = map[string]workflowEventDef{
	// Scheduled: the 15-minute workflow scheduler scans thresholds and emits
	// this with a 24h per-product dedupe (workflow_rules.go
	// checkInventoryThresholds); it also fires inline from AdjustInventory.
	// The flag was false, which contradicted the scheduler and confused
	// rule validation (docs/ombor-audit.md §3).
	"inventory.low_stock": {Category: "inventory", RelatedType: "product", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "", "product_name": "Sement M400", "product_code": "SEM-400", "reorder_point": 50.0, "available": 12.0}},
	"inventory.adjusted": {Category: "inventory", RelatedType: "product", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "product_name": "Sement M400", "product_code": "SEM-400", "quantity": -5.0, "new_balance": 45.0}},
	// Scheduler scan (workflow_rules.go checkStuckTransfers): intercompany
	// transfers in_transit > 3 days — stock is in neither warehouse.
	"inventory.transfer_stuck": {Category: "inventory", RelatedType: "intercompany_transfer", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "", "transfer_number": "ICT-0007", "from_warehouse": "Asosiy ombor", "to_warehouse": "Filial ombori", "days_in_transit": 5}},
	"invoice.overdue": {Category: "sales", RelatedType: "sales_invoice", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "", "invoice_number": "INV-2026-0042", "customer_name": "Qurilish Invest MChJ", "total_amount": 12500000.0, "days_overdue": 3}},
	"lead.created": {Category: "crm", RelatedType: "lead", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "contact_name": "Aziz Karimov", "company_name": "Karimov Stroy", "source": "website", "expected_value": 5000000.0}},
	"lead.status_changed": {Category: "crm", RelatedType: "lead", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "contact_name": "Aziz Karimov", "old_status": "new", "new_status": "qualified"}},
	"lead.won": {Category: "crm", RelatedType: "lead", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "contact_name": "Aziz Karimov", "company_name": "Karimov Stroy", "amount": 5000000.0, "currency": "UZS", "partner_id": ""}},
	"lead.lost": {Category: "crm", RelatedType: "lead", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "contact_name": "Aziz Karimov", "company_name": "Karimov Stroy", "amount": 5000000.0, "currency": "UZS", "lost_reason": "Narx qimmat"}},
	"lead.stale": {Category: "crm", RelatedType: "lead", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "", "contact_name": "Aziz Karimov", "company_name": "Karimov Stroy", "amount": 5000000.0, "currency": "UZS", "stage": "in_progress", "stale_days": 7}},
	"task.assigned": {Category: "tasks", RelatedType: "task", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "task_title": "Smeta tayyorlash", "board_name": "Qurilish obyekti", "priority": "high", "assignee_names": "Dilshod Rahimov"}},
	"task.status_changed": {Category: "tasks", RelatedType: "task", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "task_title": "Smeta tayyorlash", "old_column": "Jarayonda", "new_column": "Bajarildi", "is_completed": true}},
	"task.overdue": {Category: "tasks", RelatedType: "task", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "", "task_title": "Smeta tayyorlash", "board_name": "Qurilish obyekti", "due_date": "2026-07-30", "days_overdue": 2}},
	"sales_order.created": {Category: "sales", RelatedType: "sales_order", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "order_number": "SO-2026-0107", "customer_name": "Qurilish Invest MChJ", "total_amount": 8400000.0}},
	"sales_order.confirmed": {Category: "sales", RelatedType: "sales_order", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "order_number": "S00107", "customer_name": "Qurilish Invest MChJ", "amount": 8400000.0}},
	"sales_order.credit_limit_exceeded": {Category: "sales", RelatedType: "sales_order", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "order_number": "S00107", "customer_name": "Qurilish Invest MChJ", "amount": 8400000.0,
		"outstanding": 12000000.0, "credit_limit": 15000000.0, "policy": "block"}},
	"payment.received": {Category: "sales", RelatedType: "sales_invoice", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "invoice_number": "INV-2026-0042", "customer_name": "Qurilish Invest MChJ", "amount": 4000000.0}},
	"purchase_order.confirmed": {Category: "purchase", RelatedType: "purchase_order", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "order_number": "PO-20260731-0003", "vendor_name": "Metall Servis", "total_amount": 15200000.0}},
	"purchase_order.received": {Category: "purchase", RelatedType: "purchase_order", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "order_number": "PO-20260731-0003", "vendor_name": "Metall Servis"}},
	"purchase_order.approval_required": {Category: "purchase", RelatedType: "purchase_order", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "order_number": "PO-20260731-0003", "vendor_name": "Metall Servis", "total_amount": 15200000.0}},
	"purchase_order.delivery_overdue": {Category: "purchase", RelatedType: "purchase_order", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "", "order_number": "PO-20260731-0003", "vendor_name": "Metall Servis", "expected_date": "2026-07-30", "days_overdue": 3}},
	"employee.created": {Category: "hr", RelatedType: "employee", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "employee_name": "Dilshod Rahimov", "position": "Prorab"}},
	"employee.terminated": {Category: "hr", RelatedType: "employee", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "employee_name": "Dilshod Rahimov"}},
	"contracts.created": {Category: "contracts", RelatedType: "contract", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "contract_number": "CNT-2026-0014", "title": "Bosh pudrat shartnomasi", "contact_name": "Qurilish Invest MChJ", "value": 250000000.0, "direction": "income"}},
	"contracts.status_changed": {Category: "contracts", RelatedType: "contract", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "contract_number": "CNT-2026-0014", "contact_name": "Qurilish Invest MChJ", "old_status": "signing", "new_status": "active"}},
	"contracts.expiring_soon": {Category: "contracts", RelatedType: "contract", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "", "contract_number": "CNT-2026-0014", "contact_name": "Qurilish Invest MChJ", "end_date": "2026-08-20", "days_to_expiry": 19, "threshold_days": 30}},
	"contracts.expired": {Category: "contracts", RelatedType: "contract", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "", "contract_number": "CNT-2026-0014", "contact_name": "Qurilish Invest MChJ", "end_date": "2026-07-20"}},
	"expenses.submitted": {Category: "expenses", RelatedType: "expense", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "expense_number": "EXP-2026-0018", "employee_name": "Dilshod Rahimov", "category_name": "Transport", "total_amount": 250000.0}},
	"expenses.approved": {Category: "expenses", RelatedType: "expense", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "expense_number": "EXP-2026-0018", "employee_name": "Dilshod Rahimov", "category_name": "Transport", "total_amount": 250000.0}},
	"expenses.rejected": {Category: "expenses", RelatedType: "expense", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "expense_number": "EXP-2026-0018", "employee_name": "Dilshod Rahimov", "total_amount": 250000.0, "reason": "Chek ilova qilinmagan"}},
	"expenses.paid": {Category: "expenses", RelatedType: "expense", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "expense_number": "EXP-2026-0018", "employee_name": "Dilshod Rahimov", "category_name": "Transport", "total_amount": 250000.0}},
	"payroll.period_confirmed": {Category: "hr", RelatedType: "payroll_period", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "period_name": "Iyul 2026", "total_net": 45000000.0}},
	"payroll.paid": {Category: "hr", RelatedType: "payroll_period", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "period_name": "Iyul 2026", "total_net": 45000000.0}},
	// Aktivlar (fixed assets) — added by the 2026-08-03 rebuild.
	"assets.commissioned": {Category: "assets", RelatedType: "fa_asset", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "inventory_number": "FA-000012", "asset_name": "Ekskavator CAT 320", "cost": 850000000.0}},
	"assets.depreciation_posted": {Category: "assets", RelatedType: "fa_depreciation_run", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "period": "2026-07", "total_amount": 12400000.0, "asset_count": 8}},
	"assets.disposed": {Category: "assets", RelatedType: "fa_asset", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "inventory_number": "FA-000012", "asset_name": "Ekskavator CAT 320", "disposal_type": "sale",
		"book_value": 420000000.0, "sale_price": 500000000.0, "gain_loss": 80000000.0, "reason": "Yangisiga almashtirildi"}},
	"assets.fully_depreciated": {Category: "assets", RelatedType: "fa_asset", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "", "inventory_number": "FA-000005", "asset_name": "Damas yuk mashinasi", "depreciable_base": 96000000.0}},
	// Qurilish (construction) — added by the 2026-08-03 hardening
	// (docs/construction-integration-map.md §3). record_id is the BIGINT
	// project id as a string (construction PKs are not UUIDs).
	"construction.project_created": {Category: "construction", RelatedType: "construction_project", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "42", "code": "QUR-2026-001", "name": "Yunusobod turar-joy majmuasi", "status": "draft", "region": "Toshkent shahri"}},
	"construction.project_status_changed": {Category: "construction", RelatedType: "construction_project", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "42", "name": "Yunusobod turar-joy majmuasi", "old_status": "planning", "new_status": "in_progress"}},
	"construction.project_commissioned": {Category: "construction", RelatedType: "construction_project", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "42", "name": "Yunusobod turar-joy majmuasi", "capitalized_amount": 12500000000.0}},
	"construction.act_approved": {Category: "construction", RelatedType: "construction_act", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "17", "act_number": "F2-2026-07", "act_type": "forma2", "project_name": "Yunusobod turar-joy majmuasi", "total_amount": 4200000000.0}},
	"construction.act_signed": {Category: "construction", RelatedType: "construction_act", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "17", "act_number": "F2-2026-07", "act_type": "forma2", "project_name": "Yunusobod turar-joy majmuasi", "signer_role": "client"}},
	"construction.material_request_approved": {Category: "construction", RelatedType: "construction_material_request", Scheduled: false, SampleData: map[string]interface{}{
		"record_id": "31", "project_name": "Yunusobod turar-joy majmuasi", "total_expense": 185000000.0}},
	// Scheduled: the 15-minute scanner compares approved object costs
	// (construction_expense_lines) against contract_amount per live project;
	// 7-day marker cooldown per project (workflow_rules.go
	// checkConstructionBudgetOverruns).
	"construction.budget_overrun": {Category: "construction", RelatedType: "construction_project", Scheduled: true, SampleData: map[string]interface{}{
		"record_id": "42", "name": "Yunusobod turar-joy majmuasi", "budget": 48000000000.0, "actual": 51300000000.0, "overrun_pct": 6.9}},
}

// workflowActionTypes is the set of executable action types. Legacy types
// created by the old UI are kept and mapped onto the new executors.
var workflowActionTypes = map[string]bool{
	"create_notification": true,
	"create_task":         true,
	"update_field":        true,
	"send_telegram":       true,
	// legacy aliases still present in stored rules
	"update_status":        true,
	"update_task_priority": true,
	"create_followup_task": true,
	"create_record":        true,
}

// workflowUpdatableFields whitelists what update_field may touch.
var workflowUpdatableFields = map[string]map[string]bool{
	"leads":           {"status": true, "source": true},
	"tasks":           {"priority": true},
	"contacts":        {"is_active": true}, // `status` column doesn't exist on contacts
	"sales_invoices":  {"status": true},
	"sales_orders":    {"status": true},
	"purchase_orders": {"status": true},
}

// workflowEventCtx carries one event through the engine.
type workflowEventCtx struct {
	TenantID  uuid.UUID
	Event     string
	Data      map[string]interface{}
	Depth     int
	RuleChain []uuid.UUID   // rules already fired in this causation chain
	DedupeKey string        // scheduler record key; "" for live events
	Cooldown  time.Duration // marker re-fire window; 0 = one-shot forever
}

// EmitWorkflowEvent is the single entry point modules use to feed the rules
// engine. Fire-and-forget: never blocks or panics the calling operation.
func (h *Handler) EmitWorkflowEvent(tenantID uuid.UUID, event string, data map[string]interface{}) {
	h.emitWorkflowEvent(workflowEventCtx{TenantID: tenantID, Event: event, Data: data})
}

func (h *Handler) emitWorkflowEvent(ctx workflowEventCtx) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				h.log.Error("Workflow engine panic recovered", "event", ctx.Event, "panic", fmt.Sprintf("%v", r))
			}
		}()
		h.runWorkflowEvent(ctx)
	}()
}

// EvaluateWorkflowRules is the legacy synchronous entry point; kept so older
// call sites and tests keep working. Prefer EmitWorkflowEvent.
func (h *Handler) EvaluateWorkflowRules(tenantID uuid.UUID, event string, data map[string]interface{}) {
	h.runWorkflowEvent(workflowEventCtx{TenantID: tenantID, Event: event, Data: data})
}

func (h *Handler) runWorkflowEvent(ctx workflowEventCtx) {
	if ctx.Depth > maxWorkflowDepth {
		h.log.Warn("Workflow chain depth limit reached, event dropped", "event", ctx.Event, "depth", ctx.Depth)
		return
	}

	rows, err := h.db.Query(`
		SELECT id, name, conditions, actions, created_by
		FROM workflow_rules
		WHERE tenant_id = $1 AND trigger_event = $2 AND is_active = true AND deleted_at IS NULL
		ORDER BY priority DESC, created_at ASC
	`, ctx.TenantID, ctx.Event)
	if err != nil {
		h.log.Error("Failed to query workflow rules", "error", err, "event", ctx.Event)
		return
	}
	defer rows.Close()

	type matchedRule struct {
		ID         uuid.UUID
		Name       string
		Conditions json.RawMessage
		Actions    json.RawMessage
		CreatedBy  *uuid.UUID
	}
	var rules []matchedRule
	for rows.Next() {
		var r matchedRule
		var createdBy sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &r.Conditions, &r.Actions, &createdBy); err != nil {
			continue
		}
		if createdBy.Valid {
			if uid, err := uuid.Parse(createdBy.String); err == nil {
				r.CreatedBy = &uid
			}
		}
		rules = append(rules, r)
	}
	rows.Close()

	for _, rule := range rules {
		if workflowChainContains(ctx.RuleChain, rule.ID) {
			h.log.Warn("Workflow loop prevented: rule already in causation chain", "rule", rule.Name, "event", ctx.Event)
			continue
		}
		if ctx.DedupeKey != "" && !h.claimWorkflowMarker(ctx.TenantID, rule.ID, ctx.Event, ctx.DedupeKey, ctx.Cooldown) {
			continue // already fired for this record within the cooldown window
		}
		if h.workflowRuleOverRate(rule.ID) {
			h.autoPauseWorkflowRule(ctx.TenantID, rule.ID, rule.Name, rule.CreatedBy, "rate_limit")
			continue
		}
		h.executeWorkflowRule(ctx, rule.ID, rule.Name, rule.Conditions, rule.Actions, rule.CreatedBy)
	}
}

func workflowChainContains(chain []uuid.UUID, id uuid.UUID) bool {
	for _, c := range chain {
		if c == id {
			return true
		}
	}
	return false
}

// claimWorkflowMarker atomically claims the (rule, event, record) marker.
// Returns true when this run is allowed to fire.
func (h *Handler) claimWorkflowMarker(tenantID, ruleID uuid.UUID, event, recordKey string, cooldown time.Duration) bool {
	if cooldown <= 0 {
		// one-shot forever: only a fresh insert wins
		var one int
		err := h.db.QueryRow(`
			INSERT INTO workflow_fired_markers (tenant_id, rule_id, trigger_event, record_key)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (rule_id, trigger_event, record_key) DO NOTHING
			RETURNING 1
		`, tenantID, ruleID, event, recordKey).Scan(&one)
		return err == nil
	}
	var one int
	err := h.db.QueryRow(`
		INSERT INTO workflow_fired_markers (tenant_id, rule_id, trigger_event, record_key, fired_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (rule_id, trigger_event, record_key)
		DO UPDATE SET fired_at = NOW()
		WHERE workflow_fired_markers.fired_at < NOW() - make_interval(secs => $5)
		RETURNING 1
	`, tenantID, ruleID, event, recordKey, cooldown.Seconds()).Scan(&one)
	return err == nil
}

// workflowRuleOverRate reports whether the rule exceeded the runs/min or
// failure-storm limits.
func (h *Handler) workflowRuleOverRate(ruleID uuid.UUID) bool {
	var lastMinute, failed10 int
	h.db.QueryRow(`
		SELECT COUNT(*) FILTER (WHERE executed_at > NOW() - INTERVAL '1 minute'),
		       COUNT(*) FILTER (WHERE executed_at > NOW() - INTERVAL '10 minutes' AND status = 'failed')
		FROM workflow_logs WHERE rule_id = $1 AND executed_at > NOW() - INTERVAL '10 minutes'
	`, ruleID).Scan(&lastMinute, &failed10)
	return lastMinute >= maxRuleRunsPerMinute || failed10 >= maxRuleFailsPer10Min
}

func (h *Handler) autoPauseWorkflowRule(tenantID, ruleID uuid.UUID, ruleName string, createdBy *uuid.UUID, reason string) {
	res, err := h.db.Exec(`
		UPDATE workflow_rules SET is_active = false, auto_paused_at = NOW(), paused_reason = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3 AND is_active = true
	`, reason, ruleID, tenantID)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // someone else already paused it — don't re-notify
	}
	h.log.Warn("Workflow rule auto-paused", "rule", ruleName, "reason", reason)
	if createdBy != nil && *createdBy != uuid.Nil {
		h.createTranslatedNotification(tenantID, *createdBy, "workflow_rule_paused",
			map[string]interface{}{"rule_id": ruleID.String(), "reason": reason}, ruleName)
	}
}

// executeWorkflowRule evaluates conditions, runs the actions and logs the run.
func (h *Handler) executeWorkflowRule(ctx workflowEventCtx, ruleID uuid.UUID, ruleName string, conditionsJSON, actionsJSON json.RawMessage, createdBy *uuid.UUID) {
	started := time.Now()

	matched, condResults := evaluateWorkflowConditions(conditionsJSON, ctx.Data)
	if !matched {
		h.logWorkflowRun(ctx, ruleID, nil, condResults, "skipped_conditions", "", time.Since(started))
		return
	}

	var actions []WorkflowAction
	if err := json.Unmarshal(actionsJSON, &actions); err != nil {
		h.logWorkflowRun(ctx, ruleID, nil, condResults, "failed", "Failed to parse actions: "+err.Error(), time.Since(started))
		return
	}

	execCtx := ctx
	execCtx.RuleChain = append(append([]uuid.UUID{}, ctx.RuleChain...), ruleID)

	executedActions := make([]map[string]interface{}, 0, len(actions))
	allSuccess := true
	var lastError string
	for _, action := range actions {
		aStart := time.Now()
		result, err := h.executeWorkflowAction(execCtx, ruleID, ruleName, action)
		actionLog := map[string]interface{}{
			"type":        action.Type,
			"config":      action.Config,
			"result":      result,
			"duration_ms": time.Since(aStart).Milliseconds(),
		}
		if err != nil {
			actionLog["error"] = err.Error()
			allSuccess = false
			lastError = err.Error()
		}
		executedActions = append(executedActions, actionLog)
	}

	status := "success"
	if !allSuccess {
		status = "partial"
		if len(actions) > 0 {
			failedAll := true
			for _, a := range executedActions {
				if _, hasErr := a["error"]; !hasErr {
					failedAll = false
					break
				}
			}
			if failedAll {
				status = "failed"
			}
		}
	}

	h.logWorkflowRun(ctx, ruleID, executedActions, condResults, status, lastError, time.Since(started))

	h.db.Exec(`
		UPDATE workflow_rules SET last_triggered_at = NOW(), trigger_count = trigger_count + 1
		WHERE id = $1
	`, ruleID)

	if status == "failed" && h.workflowRuleOverRate(ruleID) {
		h.autoPauseWorkflowRule(ctx.TenantID, ruleID, ruleName, createdBy, "failure_storm")
	}

	h.log.Info("Workflow rule executed", "rule", ruleName, "event", ctx.Event, "status", status)
}

// logWorkflowRun writes one workflow_logs row with v2 detail columns.
func (h *Handler) logWorkflowRun(ctx workflowEventCtx, ruleID uuid.UUID, actionsExecuted interface{}, condResults interface{}, status, errorMessage string, duration time.Duration) {
	cleanData := make(map[string]interface{}, len(ctx.Data))
	for k, v := range ctx.Data {
		if !strings.HasPrefix(k, "_") {
			cleanData[k] = v
		}
	}
	triggerJSON, _ := json.Marshal(cleanData)
	actionsJSON, _ := json.Marshal(actionsExecuted)
	condJSON, _ := json.Marshal(condResults)

	var errMsg *string
	if errorMessage != "" {
		errMsg = &errorMessage
	}

	relatedType := ""
	if def, ok := workflowEventCatalog[ctx.Event]; ok {
		relatedType = def.RelatedType
	}
	relatedID := ""
	if v, ok := ctx.Data["record_id"]; ok {
		relatedID = fmt.Sprintf("%v", v)
	}

	_, err := h.db.Exec(`
		INSERT INTO workflow_logs (id, tenant_id, rule_id, trigger_data, actions_executed, status,
		                           error_message, executed_at, trigger_event, related_type, related_id,
		                           duration_ms, condition_results)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), $8, NULLIF($9, ''), NULLIF($10, ''), $11, $12)
	`, uuid.New(), ctx.TenantID, ruleID, triggerJSON, actionsJSON, status, errMsg,
		ctx.Event, relatedType, relatedID, duration.Milliseconds(), condJSON)
	if err != nil {
		h.log.Error("Failed to log workflow execution", "error", err)
	}
}

// ── Condition evaluation ────────────────────────────────────────────────────

type workflowConditionResult struct {
	Field    string      `json:"field"`
	Operator string      `json:"operator"`
	Expected interface{} `json:"expected"`
	Actual   interface{} `json:"actual"`
	Passed   bool        `json:"passed"`
}

// evaluateWorkflowConditions supports two formats:
//
//	v2: {"logic":"and","conditions":[{"field":"available","operator":"lte","value":10}, {...nested group...}]}
//	legacy: {"available":{"$lte":10},"warehouse":"main"}
//
// Empty/null conditions always match.
func evaluateWorkflowConditions(conditionsJSON json.RawMessage, data map[string]interface{}) (bool, []workflowConditionResult) {
	trimmed := strings.TrimSpace(string(conditionsJSON))
	if trimmed == "" || trimmed == "{}" || trimmed == "null" || trimmed == "[]" {
		return true, nil
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(conditionsJSON, &raw); err != nil {
		return false, []workflowConditionResult{{Field: "_parse", Operator: "valid_json", Passed: false}}
	}

	if _, isGroup := raw["conditions"]; isGroup {
		return evaluateConditionGroup(raw, data)
	}
	return evaluateLegacyConditions(raw, data)
}

func evaluateConditionGroup(group map[string]interface{}, data map[string]interface{}) (bool, []workflowConditionResult) {
	logic := "and"
	if l, ok := group["logic"].(string); ok && strings.EqualFold(l, "or") {
		logic = "or"
	}
	items, _ := group["conditions"].([]interface{})
	if len(items) == 0 {
		return true, nil
	}

	var results []workflowConditionResult
	groupPassed := logic == "and"
	for _, item := range items {
		cond, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var passed bool
		if _, nested := cond["conditions"]; nested {
			nestedPassed, nestedResults := evaluateConditionGroup(cond, data)
			passed = nestedPassed
			results = append(results, nestedResults...)
		} else {
			field, _ := cond["field"].(string)
			operator, _ := cond["operator"].(string)
			expected := cond["value"]
			actual := data[field]
			passed = compareWorkflowValues(operator, actual, expected)
			results = append(results, workflowConditionResult{
				Field: field, Operator: operator, Expected: expected, Actual: actual, Passed: passed,
			})
		}
		if logic == "and" && !passed {
			groupPassed = false
		}
		if logic == "or" && passed {
			groupPassed = true
		}
	}
	if logic == "or" {
		anyPassed := false
		for _, r := range results {
			if r.Passed {
				anyPassed = true
				break
			}
		}
		groupPassed = anyPassed
	}
	return groupPassed, results
}

func evaluateLegacyConditions(conditions map[string]interface{}, data map[string]interface{}) (bool, []workflowConditionResult) {
	var results []workflowConditionResult
	allPassed := true
	legacyOps := map[string]string{"$eq": "eq", "$ne": "neq", "$gt": "gt", "$gte": "gte", "$lt": "lt", "$lte": "lte"}
	for key, expected := range conditions {
		actual, exists := data[key]
		switch exp := expected.(type) {
		case map[string]interface{}:
			for op, val := range exp {
				mapped, known := legacyOps[op]
				passed := exists && known && compareWorkflowValues(mapped, actual, val)
				results = append(results, workflowConditionResult{Field: key, Operator: mapped, Expected: val, Actual: actual, Passed: passed})
				if !passed {
					allPassed = false
				}
			}
		default:
			passed := exists && fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
			results = append(results, workflowConditionResult{Field: key, Operator: "eq", Expected: expected, Actual: actual, Passed: passed})
			if !passed {
				allPassed = false
			}
		}
	}
	return allPassed, results
}

func compareWorkflowValues(op string, actual, expected interface{}) bool {
	switch op {
	case "is_empty":
		return actual == nil || fmt.Sprintf("%v", actual) == ""
	case "is_not_empty":
		return actual != nil && fmt.Sprintf("%v", actual) != ""
	}
	if actual == nil {
		return false
	}
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	switch op {
	case "contains":
		return strings.Contains(strings.ToLower(actualStr), strings.ToLower(expectedStr))
	case "not_contains":
		return !strings.Contains(strings.ToLower(actualStr), strings.ToLower(expectedStr))
	}

	aFloat, aErr := strconv.ParseFloat(actualStr, 64)
	eFloat, eErr := strconv.ParseFloat(expectedStr, 64)
	numeric := aErr == nil && eErr == nil

	switch op {
	case "eq":
		if numeric {
			return aFloat == eFloat
		}
		return strings.EqualFold(actualStr, expectedStr)
	case "neq", "ne":
		if numeric {
			return aFloat != eFloat
		}
		return !strings.EqualFold(actualStr, expectedStr)
	case "gt":
		return numeric && aFloat > eFloat
	case "gte":
		return numeric && aFloat >= eFloat
	case "lt":
		return numeric && aFloat < eFloat
	case "lte":
		return numeric && aFloat <= eFloat
	default:
		return false
	}
}

// ── Template rendering ──────────────────────────────────────────────────────

var workflowTemplateVar = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}|\{([a-zA-Z0-9_.]+)\}`)

// renderWorkflowTemplate substitutes {{var}} (and legacy {var}) placeholders
// from the trigger payload. Unknown placeholders are left as-is.
func renderWorkflowTemplate(tmpl string, data map[string]interface{}) string {
	return workflowTemplateVar.ReplaceAllStringFunc(tmpl, func(m string) string {
		key := strings.Trim(m, "{} ")
		if v, ok := data[key]; ok {
			switch n := v.(type) {
			case float64:
				if n == float64(int64(n)) {
					return strconv.FormatInt(int64(n), 10)
				}
				return strconv.FormatFloat(n, 'f', 2, 64)
			default:
				return fmt.Sprintf("%v", v)
			}
		}
		return m
	})
}

// ── Actions ─────────────────────────────────────────────────────────────────

func (h *Handler) executeWorkflowAction(ctx workflowEventCtx, ruleID uuid.UUID, ruleName string, action WorkflowAction) (string, error) {
	switch action.Type {
	case "create_notification":
		return h.wfActionCreateNotification(ctx, ruleName, action.Config)
	case "create_task", "create_followup_task":
		return h.wfActionCreateTask(ctx, ruleID, ruleName, action.Config)
	case "update_field":
		return h.wfActionUpdateField(ctx, action.Config)
	case "update_status":
		cfg := map[string]interface{}{"target": action.Config["table"], "field": "status", "value": action.Config["status"]}
		return h.wfActionUpdateField(ctx, cfg)
	case "update_task_priority":
		cfg := map[string]interface{}{"target": "tasks", "field": "priority", "value": action.Config["priority"]}
		return h.wfActionUpdateField(ctx, cfg)
	case "send_telegram":
		return "", fmt.Errorf("telegram_not_configured")
	case "create_record":
		return "", fmt.Errorf("deprecated action 'create_record': use Ombor > Qayta buyurtma qoidalari (reorder rules) for auto purchase orders")
	default:
		return "", fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// wfActionCreateNotification sends a real in-app (+push) notification to the
// resolved recipients.
// Config: {title?, message, recipient_type: users|employees|roles|all,
//
//	user_ids?: [], employee_ids?: [], roles?: []}
func (h *Handler) wfActionCreateNotification(ctx workflowEventCtx, ruleName string, config map[string]interface{}) (string, error) {
	message, _ := config["message"].(string)
	if message == "" {
		return "", fmt.Errorf("notification message is required")
	}
	title, _ := config["title"].(string)
	if title == "" {
		title = ruleName
	}
	message = renderWorkflowTemplate(message, ctx.Data)
	title = renderWorkflowTemplate(title, ctx.Data)

	recipients, err := h.resolveWorkflowRecipients(ctx.TenantID, config)
	if err != nil {
		return "", err
	}
	if len(recipients) == 0 {
		return "", fmt.Errorf("no recipients resolved")
	}

	notifData := map[string]interface{}{"trigger_event": ctx.Event, "source": "workflow"}
	if v, ok := ctx.Data["record_id"]; ok {
		notifData["record_id"] = v
	}
	if def, ok := workflowEventCatalog[ctx.Event]; ok {
		notifData["record_type"] = def.RelatedType
	}
	for _, uid := range recipients {
		h.createNotification(ctx.TenantID, uid, "workflow", title, message, notifData)
	}
	return fmt.Sprintf("Notification sent to %d user(s)", len(recipients)), nil
}

// resolveWorkflowRecipients maps a recipient config to user IDs.
func (h *Handler) resolveWorkflowRecipients(tenantID uuid.UUID, config map[string]interface{}) ([]uuid.UUID, error) {
	recipientType, _ := config["recipient_type"].(string)
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(id uuid.UUID) {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	collectIDs := func(key string) []string {
		var ids []string
		if arr, ok := config[key].([]interface{}); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok && s != "" {
					ids = append(ids, s)
				}
			}
		}
		return ids
	}

	switch recipientType {
	case "all":
		rows, err := h.db.Query(`SELECT id FROM users WHERE tenant_id = $1 AND is_active = true AND deleted_at IS NULL`, tenantID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var uid uuid.UUID
			if rows.Scan(&uid) == nil {
				add(uid)
			}
		}
	case "roles":
		roles := collectIDs("roles")
		if len(roles) == 0 {
			return nil, fmt.Errorf("roles list is empty")
		}
		rows, err := h.db.Query(`
			SELECT id FROM users
			WHERE tenant_id = $1 AND is_active = true AND deleted_at IS NULL AND role = ANY($2)
		`, tenantID, pq.Array(roles))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var uid uuid.UUID
			if rows.Scan(&uid) == nil {
				add(uid)
			}
		}
	case "employees":
		for _, idStr := range collectIDs("employee_ids") {
			empID, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			var userID sql.NullString
			if err := h.db.QueryRow(`
				SELECT user_id FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
			`, empID, tenantID).Scan(&userID); err == nil && userID.Valid {
				if uid, err := uuid.Parse(userID.String); err == nil {
					add(uid)
				}
			}
		}
	default: // "users" or unspecified
		for _, idStr := range collectIDs("user_ids") {
			if uid, err := uuid.Parse(idStr); err == nil {
				var one int
				if h.db.QueryRow(`SELECT 1 FROM users WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, uid, tenantID).Scan(&one) == nil {
					add(uid)
				}
			}
		}
	}
	return out, nil
}

// wfActionCreateTask creates a real Vazifalar task.
// Config: {board_id, column_id?, title, description?, priority?,
//
//	assignee_employee_ids?: [], due_in_days?: number}
func (h *Handler) wfActionCreateTask(ctx workflowEventCtx, ruleID uuid.UUID, ruleName string, config map[string]interface{}) (string, error) {
	boardIDStr, _ := config["board_id"].(string)
	titleTmpl, _ := config["title"].(string)
	if boardIDStr == "" || titleTmpl == "" {
		return "", fmt.Errorf("board_id and title are required for create_task")
	}
	boardID, err := uuid.Parse(boardIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid board_id")
	}

	var boardName string
	if err := h.db.QueryRow(`
		SELECT name FROM task_boards WHERE id = $1 AND tenant_id = $2 AND archived_at IS NULL
	`, boardID, ctx.TenantID).Scan(&boardName); err != nil {
		return "", fmt.Errorf("board not found for this company")
	}

	var columnID uuid.UUID
	if colStr, _ := config["column_id"].(string); colStr != "" {
		colID, err := uuid.Parse(colStr)
		if err != nil {
			return "", fmt.Errorf("invalid column_id")
		}
		if err := h.db.QueryRow(`
			SELECT id FROM task_columns WHERE id = $1 AND board_id = $2 AND tenant_id = $3
		`, colID, boardID, ctx.TenantID).Scan(&columnID); err != nil {
			return "", fmt.Errorf("column does not belong to the board")
		}
	} else {
		if err := h.db.QueryRow(`
			SELECT id FROM task_columns WHERE board_id = $1 ORDER BY position LIMIT 1
		`, boardID).Scan(&columnID); err != nil {
			return "", fmt.Errorf("board has no columns")
		}
	}

	priority, _ := config["priority"].(string)
	if priority == "" {
		priority = "normal"
	}
	if !boardTaskPriorities[priority] {
		return "", fmt.Errorf("invalid priority: %s", priority)
	}

	title := renderWorkflowTemplate(titleTmpl, ctx.Data)
	description := ""
	if d, _ := config["description"].(string); d != "" {
		description = renderWorkflowTemplate(d, ctx.Data)
	}

	var dueDate interface{}
	if offset, ok := toFloat64Ok(config["due_in_days"]); ok && offset > 0 {
		dueDate = time.Now().AddDate(0, 0, int(offset)).Format("2006-01-02")
	}

	var assignees []uuid.UUID
	if arr, ok := config["assignee_employee_ids"].([]interface{}); ok {
		for _, v := range arr {
			idStr, _ := v.(string)
			empID, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			var one int
			if h.db.QueryRow(`
				SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
			`, empID, ctx.TenantID).Scan(&one) == nil {
				assignees = append(assignees, empID)
			}
		}
	}

	taskID := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO tasks (id, tenant_id, board_id, column_id, title, description, priority, due_date, position)
		SELECT $1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8::date, COALESCE(MAX(position) + 1, 0)
		FROM tasks WHERE column_id = $4
	`, taskID, ctx.TenantID, boardID, columnID, title, description, priority, dueDate); err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	for _, empID := range assignees {
		h.db.Exec(`
			INSERT INTO task_assignees (task_id, employee_id, tenant_id)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING
		`, taskID, empID, ctx.TenantID)
	}

	h.logTaskActivity(ctx.TenantID, taskID, uuid.Nil, "Avtomatlashtirish · "+ruleName, "created",
		nil, map[string]interface{}{"title": title, "workflow_rule_id": ruleID.String()})

	if len(assignees) > 0 {
		go h.notifyTaskAssigned(ctx.TenantID, assignees, uuid.Nil, taskID, boardID, title, boardName)

		// Feed the chain (loop-guarded): a rule on task.assigned may react to
		// tasks created by another rule, but never beyond maxWorkflowDepth.
		h.emitWorkflowEvent(workflowEventCtx{
			TenantID: ctx.TenantID,
			Event:    "task.assigned",
			Data: map[string]interface{}{
				"record_id":  taskID.String(),
				"task_title": title,
				"board_id":   boardID.String(),
				"board_name": boardName,
				"priority":   priority,
			},
			Depth:     ctx.Depth + 1,
			RuleChain: ctx.RuleChain,
		})
	}

	return fmt.Sprintf("Task created: %s (board: %s)", title, boardName), nil
}

// wfActionUpdateField updates one whitelisted field on the triggering record.
// Config: {target, field, value}
func (h *Handler) wfActionUpdateField(ctx workflowEventCtx, config map[string]interface{}) (string, error) {
	target, _ := config["target"].(string)
	field, _ := config["field"].(string)
	valueRaw := config["value"]
	if target == "" || field == "" || valueRaw == nil {
		return "", fmt.Errorf("target, field and value are required for update_field")
	}
	allowedFields, ok := workflowUpdatableFields[target]
	if !ok || !allowedFields[field] {
		return "", fmt.Errorf("field %s.%s is not allowed for automation updates", target, field)
	}

	value := renderWorkflowTemplate(fmt.Sprintf("%v", valueRaw), ctx.Data)
	if target == "tasks" && field == "priority" && !boardTaskPriorities[value] {
		return "", fmt.Errorf("invalid task priority: %s", value)
	}

	recordIDRaw, ok := ctx.Data["record_id"]
	if !ok {
		return "", fmt.Errorf("record_id not found in trigger data")
	}
	recordID, err := uuid.Parse(fmt.Sprintf("%v", recordIDRaw))
	if err != nil {
		return "", fmt.Errorf("invalid record_id in trigger data")
	}

	// target+field validated against the whitelist above, so this Sprintf is safe
	query := fmt.Sprintf("UPDATE %s SET %s = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3", target, field)
	res, err := h.db.Exec(query, value, recordID, ctx.TenantID)
	if err != nil {
		return "", fmt.Errorf("failed to update %s.%s: %w", target, field, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", fmt.Errorf("record not found: %s %s", target, recordID)
	}
	return fmt.Sprintf("Updated %s.%s = %s", target, field, value), nil
}

func toFloat64Ok(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// ── Rule config validation (used by Create/Update handlers) ─────────────────

func validateWorkflowRuleConfig(triggerEvent string, conditions, actions json.RawMessage) (category string, scheduled bool, err error) {
	def, ok := workflowEventCatalog[triggerEvent]
	if !ok {
		return "", false, fmt.Errorf("unknown trigger_event: %s", triggerEvent)
	}

	if len(actions) > 0 {
		var parsed []WorkflowAction
		if uErr := json.Unmarshal(actions, &parsed); uErr != nil {
			return "", false, fmt.Errorf("actions must be a JSON array of {type, config}")
		}
		if len(parsed) == 0 {
			return "", false, fmt.Errorf("at least one action is required")
		}
		for i, a := range parsed {
			if !workflowActionTypes[a.Type] {
				return "", false, fmt.Errorf("action %d: unknown type %q", i+1, a.Type)
			}
			switch a.Type {
			case "create_notification":
				if msg, _ := a.Config["message"].(string); msg == "" {
					return "", false, fmt.Errorf("action %d (create_notification): message is required", i+1)
				}
			case "create_task":
				boardID, _ := a.Config["board_id"].(string)
				title, _ := a.Config["title"].(string)
				if boardID == "" || title == "" {
					return "", false, fmt.Errorf("action %d (create_task): board_id and title are required", i+1)
				}
			case "update_field":
				target, _ := a.Config["target"].(string)
				field, _ := a.Config["field"].(string)
				if allowed, ok := workflowUpdatableFields[target]; !ok || !allowed[field] {
					return "", false, fmt.Errorf("action %d (update_field): %s.%s is not an allowed target", i+1, target, field)
				}
			}
		}
	} else {
		return "", false, fmt.Errorf("at least one action is required")
	}

	if len(conditions) > 0 {
		trimmed := strings.TrimSpace(string(conditions))
		if trimmed != "" && trimmed != "{}" && trimmed != "null" {
			var raw map[string]interface{}
			if uErr := json.Unmarshal(conditions, &raw); uErr != nil {
				return "", false, fmt.Errorf("conditions must be a JSON object")
			}
		}
	}

	return def.Category, def.Scheduled, nil
}
