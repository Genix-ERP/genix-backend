package handler

// Tool registry for the Genix ERP agent. Every exec is scoped to the caller's
// tenant + active organization. Read tools are pure SELECTs; the single write
// tool (create_contact) demonstrates the confirm-before-write flow.

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func agentTools() []agentTool {
	return []agentTool{
		{
			name:        "find_contacts",
			description: "Search the company's customers/vendors by name. Returns id, name, type, phone, INN and current balance.",
			parameters: obj(map[string]interface{}{
				"query": str("Part of the customer/vendor name to search for."),
				"type":  map[string]interface{}{"type": "string", "enum": []string{"customer", "vendor", "any"}, "description": "Filter by contact type. Default any."},
			}, "query"),
			exec: toolFindContacts,
		},
		{
			name:        "find_products",
			description: "Search products by name, SKU or barcode. Returns id, name, code, cost and total stock on hand.",
			parameters:  obj(map[string]interface{}{"query": str("Part of the product name / SKU / barcode.")}, "query"),
			exec:        toolFindProducts,
		},
		{
			name:        "check_stock",
			description: "Show on-hand stock per warehouse for products matching a name. Use to answer 'how much X do we have'.",
			parameters:  obj(map[string]interface{}{"query": str("Part of the product name.")}, "query"),
			exec:        toolCheckStock,
		},
		{
			name:        "list_sales_orders",
			description: "List recent sales orders for the active company, newest first. Optional status filter (draft, confirmed, processing, shipped, cancelled).",
			parameters: obj(map[string]interface{}{
				"status": str("Optional status filter."),
				"limit":  intp("Max rows (default 10, max 50)."),
			}),
			exec: toolListSalesOrders,
		},
		{
			name:        "list_sales_invoices",
			description: "List sales invoices with their paid/due amounts. Optional status filter (draft, confirmed, partial, paid, overdue).",
			parameters: obj(map[string]interface{}{
				"status": str("Optional status filter."),
				"limit":  intp("Max rows (default 10, max 50)."),
			}),
			exec: toolListSalesInvoices,
		},
		{
			name:        "financial_summary",
			description: "Snapshot of key account balances for the active company: cash, bank, receivables, payables, revenue.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolFinancialSummary,
		},
		{
			name:        "low_stock_products",
			description: "Products with the lowest total on-hand stock (potential shortages). Returns up to 15.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolLowStock,
		},
		{
			name:        "list_purchase_orders",
			description: "List recent purchase orders (procurement) for the active company, with vendor and total. Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListPurchaseOrders,
		},
		{
			name:        "list_vendor_bills",
			description: "List vendor bills (purchase invoices) with paid/due amounts. Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListVendorBills,
		},
		{
			name:        "list_production_orders",
			description: "List manufacturing/production orders (code, product, planned vs produced qty, status). Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListProductionOrders,
		},
		{
			name:        "find_employees",
			description: "Search employees by name. Returns name, job title, phone and status.",
			parameters:  obj(map[string]interface{}{"query": str("Part of the employee name.")}, "query"),
			exec:        toolFindEmployees,
		},
		{
			name:        "list_expenses",
			description: "List recent expenses (amount, description, vendor/employee, status). Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListExpenses,
		},
		{
			name:        "list_projects",
			description: "List construction projects for the active company (code, name, client, status, contract amount, progress %).",
			parameters:  obj(map[string]interface{}{"limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListProjects,
		},
		{
			name:        "get_sales_order",
			description: "Full detail of ONE sales order by its number, including every product line (qty, price, line total) and payment status. Use after list_sales_orders to drill in.",
			parameters:  obj(map[string]interface{}{"order_number": str("The sales order number, e.g. SO-000123.")}, "order_number"),
			exec:        toolGetSalesOrder,
		},
		{
			name:        "customer_statement",
			description: "A customer's account statement: current balance plus recent invoices and payments. Use to answer 'how much does X owe us' or 'show X's history'.",
			parameters:  obj(map[string]interface{}{"customer": str("Customer name (must exist)."), "limit": intp("Rows per section (default 10, max 30).")}, "customer"),
			exec:        toolCustomerStatement,
		},
		{
			name:        "aged_receivables",
			description: "Accounts receivable aging: unpaid sales invoices bucketed by how overdue they are (current, 1-30, 31-60, 61-90, 90+ days) with per-customer outstanding totals.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolAgedReceivables,
		},
		{
			name:        "aged_payables",
			description: "Accounts payable aging: unpaid vendor bills bucketed by how overdue they are, with per-vendor outstanding totals. Use to answer 'who do we owe'.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolAgedPayables,
		},
		{
			name:        "sales_summary",
			description: "Totals of sales invoices over a period: count, invoiced amount, collected amount and outstanding. Use for 'how much did we sell this month'.",
			parameters:  obj(map[string]interface{}{"period": map[string]interface{}{"type": "string", "enum": []string{"today", "this_week", "this_month", "this_year", "last_30_days"}, "description": "Time window. Default this_month."}}),
			exec:        toolSalesSummary,
		},
		{
			name:        "list_bank_accounts",
			description: "List the company's bank/cash accounts with their current balance and currency. Use to answer 'how much money do we have', 'cash position', 'bank balances'.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolListBankAccounts,
		},
		{
			name:        "list_fixed_assets",
			description: "The fixed-asset register: code, name, category, acquisition cost, accumulated depreciation, current book value, status and custodian. Optional status filter (active, disposed, under_maintenance, written_off).",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 15, max 50).")}),
			exec:        toolListFixedAssets,
		},
		{
			name:        "list_contracts",
			description: "Procurement contracts with vendors (number, vendor, title, type, start/end dates, value, status). Set expiring_within_days to only show active contracts ending soon (renewal watch).",
			parameters: obj(map[string]interface{}{
				"expiring_within_days": intp("Only active contracts whose end_date is within this many days from today."),
				"limit":                intp("Max rows (default 15, max 50)."),
			}),
			exec: toolListContracts,
		},
		{
			name:        "get_purchase_order",
			description: "Full detail of ONE purchase order by its number, including every product line (qty, unit price, line total, quantity received). Use after list_purchase_orders to drill in.",
			parameters:  obj(map[string]interface{}{"order_number": str("The purchase order number.")}, "order_number"),
			exec:        toolGetPurchaseOrder,
		},
		// ---- write tools (confirmation required) ----
		{
			name:        "create_contact",
			description: "Create a new customer or vendor. Requires confirmation before it runs.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"name":  str("Display name of the customer/vendor."),
				"type":  map[string]interface{}{"type": "string", "enum": []string{"customer", "vendor"}, "description": "customer or vendor."},
				"phone": str("Optional phone number."),
				"inn":   str("Optional tax id / INN."),
			}, "name", "type"),
			exec: toolCreateContact,
		},
		{
			name:        "create_sales_order",
			description: "Create a DRAFT sales order for a customer with one or more product lines. It stays draft — the user confirms it in the app to trigger delivery/accounting. Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"customer": str("Customer name (must already exist; use find_contacts first)."),
				"lines": map[string]interface{}{"type": "array", "description": "Order lines.", "items": obj(map[string]interface{}{
					"product":    str("Product name (must exist)."),
					"quantity":   map[string]interface{}{"type": "number", "description": "Quantity."},
					"unit_price": map[string]interface{}{"type": "number", "description": "Optional unit price; defaults to the product's cost."},
				}, "product", "quantity")},
			}, "customer", "lines"),
			exec: toolCreateSalesOrder,
		},
		{
			name:        "record_payment",
			description: "Record a DRAFT payment from a customer (in) or to a vendor (out). Stays draft/unposted — the user confirms it in the app to post the accounting. Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"contact":   str("Customer/vendor name (must exist)."),
				"direction": map[string]interface{}{"type": "string", "enum": []string{"in", "out"}, "description": "in = received from customer, out = paid to vendor."},
				"amount":    map[string]interface{}{"type": "number", "description": "Amount in so'm."},
				"reference": str("Optional reference/note."),
			}, "contact", "direction", "amount"),
			exec: toolRecordPayment,
		},
		{
			name:        "create_sales_invoice",
			description: "Create a DRAFT sales invoice for a customer with product lines. Stays draft — the user sends/posts it in the app. Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"customer": str("Customer name (must exist)."),
				"lines": map[string]interface{}{"type": "array", "description": "Invoice lines.", "items": obj(map[string]interface{}{
					"product":    str("Product name."),
					"quantity":   map[string]interface{}{"type": "number"},
					"unit_price": map[string]interface{}{"type": "number", "description": "Optional; defaults to product cost."},
				}, "product", "quantity")},
			}, "customer", "lines"),
			exec: toolCreateSalesInvoice,
		},
		{
			name:        "create_vendor_bill",
			description: "Create a DRAFT vendor bill (purchase invoice) for a vendor with a total amount. Stays draft — the user posts it in the app. Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"vendor":                str("Vendor name (must exist)."),
				"amount":                map[string]interface{}{"type": "number", "description": "Bill total in so'm."},
				"vendor_invoice_number": str("Optional vendor's own invoice number."),
				"description":           str("Optional notes."),
			}, "vendor", "amount"),
			exec: toolCreateVendorBill,
		},
		{
			name:        "stock_adjust",
			description: "Set or change a product's on-hand stock in a warehouse (e.g. after a stock count). Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"product":      str("Product name (must exist)."),
				"warehouse":    str("Warehouse name (must exist in the active company)."),
				"new_quantity": map[string]interface{}{"type": "number", "description": "The corrected on-hand quantity to set."},
				"reason":       str("Optional reason."),
			}, "product", "warehouse", "new_quantity"),
			exec: toolStockAdjust,
		},
		{
			name:        "stock_transfer",
			description: "Move a product's stock from one warehouse to another in the active company. Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"product":        str("Product name."),
				"from_warehouse": str("Source warehouse name."),
				"to_warehouse":   str("Destination warehouse name."),
				"quantity":       map[string]interface{}{"type": "number", "description": "Quantity to move."},
			}, "product", "from_warehouse", "to_warehouse", "quantity"),
			exec: toolStockTransfer,
		},
	}
}

// ---- resolvers -------------------------------------------------------------

func resolveContactID(h *Handler, tenantID uuid.UUID, name, typ string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var nm string
	q := `SELECT id, name FROM contacts WHERE tenant_id=$1 AND deleted_at IS NULL AND name ILIKE '%'||$2||'%'`
	a := []interface{}{tenantID, name}
	if typ == "customer" || typ == "vendor" {
		q += " AND type=$3"
		a = append(a, typ)
	}
	q += " ORDER BY name ASC LIMIT 1"
	if err := h.db.QueryRow(q, a...).Scan(&id, &nm); err != nil {
		return uuid.Nil, "", fmt.Errorf("no %s found matching %q", strFallback(typ, "contact"), name)
	}
	return id, nm, nil
}

func resolveProduct(h *Handler, tenantID uuid.UUID, name string) (uuid.UUID, string, float64, error) {
	var id uuid.UUID
	var nm string
	var cost float64
	if err := h.db.QueryRow(`SELECT id, name, COALESCE(cost_price,0) FROM products
		WHERE tenant_id=$1 AND deleted_at IS NULL AND (name ILIKE '%'||$2||'%' OR sku=$2 OR barcode=$2)
		ORDER BY name ASC LIMIT 1`, tenantID, name).Scan(&id, &nm, &cost); err != nil {
		return uuid.Nil, "", 0, fmt.Errorf("no product found matching %q", name)
	}
	return id, nm, cost, nil
}

func resolveWarehouse(h *Handler, tenantID uuid.UUID, orgArg interface{}, name string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var nm string
	if err := h.db.QueryRow(`SELECT id, name FROM warehouses
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($3::uuid IS NULL OR organization_id=$3)
		  AND name ILIKE '%'||$2||'%' ORDER BY name ASC LIMIT 1`, tenantID, name, orgArg).Scan(&id, &nm); err != nil {
		return uuid.Nil, "", fmt.Errorf("no warehouse found matching %q", name)
	}
	return id, nm, nil
}

func strFallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

type parsedLine struct {
	ProductID uuid.UUID
	Name      string
	Qty       float64
	Price     float64
	Total     float64
}

// parseLines resolves the model's line array into product ids + prices.
func parseLines(h *Handler, tenantID uuid.UUID, raw interface{}) ([]parsedLine, float64, error) {
	arr, ok := raw.([]interface{})
	if !ok || len(arr) == 0 {
		return nil, 0, fmt.Errorf("at least one line is required")
	}
	var out []parsedLine
	var subtotal float64
	for _, it := range arr {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		pname := argStr(m, "product")
		pid, nm, cost, err := resolveProduct(h, tenantID, pname)
		if err != nil {
			return nil, 0, err
		}
		qty := toFloat(m["quantity"])
		if qty <= 0 {
			return nil, 0, fmt.Errorf("line for %q needs a positive quantity", nm)
		}
		price := cost
		if p := toFloat(m["unit_price"]); p > 0 {
			price = p
		}
		total := qty * price
		out = append(out, parsedLine{ProductID: pid, Name: nm, Qty: qty, Price: price, Total: total})
		subtotal += total
	}
	if len(out) == 0 {
		return nil, 0, fmt.Errorf("no valid lines")
	}
	return out, subtotal, nil
}

func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

func docNumber(prefix string) string {
	return prefix + "-" + strings.ToUpper(uuid.New().String()[:8])
}

func argStr(args map[string]interface{}, k string) string {
	if v, ok := args[k]; ok {
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	return ""
}
func argInt(args map[string]interface{}, k string, def, max int) int {
	if v, ok := args[k]; ok {
		switch n := v.(type) {
		case float64:
			if int(n) > 0 {
				def = int(n)
			}
		}
	}
	if def > max {
		def = max
	}
	return def
}

func toolFindContacts(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	q := argStr(args, "query")
	typ := argStr(args, "type")
	qry := `SELECT id::text, COALESCE(name,''), COALESCE(type,''), COALESCE(phone,''), COALESCE(tax_id,''), COALESCE(current_balance,0)
	        FROM contacts WHERE tenant_id=$1 AND deleted_at IS NULL AND name ILIKE '%'||$2||'%'`
	qargs := []interface{}{tenantID, q}
	if typ == "customer" || typ == "vendor" {
		qry += " AND type=$3"
		qargs = append(qargs, typ)
	}
	qry += " ORDER BY name ASC LIMIT 10"
	rows, err := h.db.Query(qry, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, name, t, phone, inn string
		var bal float64
		if rows.Scan(&id, &name, &t, &phone, &inn, &bal) == nil {
			out = append(out, gin.H{"id": id, "name": name, "type": t, "phone": phone, "inn": inn, "balance": bal})
		}
	}
	return out, nil
}

func toolFindProducts(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	q := argStr(args, "query")
	rows, err := h.db.Query(`
		SELECT p.id::text, COALESCE(p.name,''), COALESCE(p.sku,''), COALESCE(p.barcode,''), COALESCE(p.cost_price,0),
		       COALESCE((SELECT SUM(quantity_on_hand) FROM inventory i WHERE i.product_id=p.id),0)
		FROM products p
		WHERE p.tenant_id=$1 AND p.deleted_at IS NULL
		  AND (p.name ILIKE '%'||$2||'%' OR p.sku ILIKE '%'||$2||'%' OR p.barcode = $2)
		ORDER BY p.name ASC LIMIT 10`, tenantID, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, name, sku, bc string
		var cost, stock float64
		if rows.Scan(&id, &name, &sku, &bc, &cost, &stock) == nil {
			out = append(out, gin.H{"id": id, "name": name, "sku": sku, "barcode": bc, "cost_price": cost, "stock": stock})
		}
	}
	return out, nil
}

func toolCheckStock(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	q := argStr(args, "query")
	rows, err := h.db.Query(`
		SELECT p.name, w.name, i.quantity_on_hand
		FROM inventory i
		JOIN products p ON p.id=i.product_id
		JOIN warehouses w ON w.id=i.warehouse_id
		WHERE p.tenant_id=$1 AND p.deleted_at IS NULL AND p.name ILIKE '%'||$2||'%'
		  AND ($3::uuid IS NULL OR w.organization_id=$3)
		ORDER BY p.name, w.name LIMIT 60`, tenantID, q, orgArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var pn, wn string
		var qty float64
		if rows.Scan(&pn, &wn, &qty) == nil {
			out = append(out, gin.H{"product": pn, "warehouse": wn, "quantity": qty})
		}
	}
	return out, nil
}

func toolListSalesOrders(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT so.order_number, COALESCE(v.name,''), COALESCE(so.status,''), COALESCE(so.total_amount,0), so.order_date
	        FROM sales_orders so LEFT JOIN contacts v ON v.id=so.customer_id
	        WHERE so.tenant_id=$1 AND so.deleted_at IS NULL AND ($2::uuid IS NULL OR so.organization_id=$2)`
	qargs := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND so.status=$3"
		qargs = append(qargs, status)
	}
	qry += fmt.Sprintf(" ORDER BY so.order_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var num, cust, st string
		var total float64
		var dt interface{}
		if rows.Scan(&num, &cust, &st, &total, &dt) == nil {
			out = append(out, gin.H{"order_number": num, "customer": cust, "status": st, "total": total, "date": dt})
		}
	}
	return out, nil
}

func toolListSalesInvoices(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT invoice_number, COALESCE(customer_name,''), COALESCE(status,''), COALESCE(total_amount,0), COALESCE(amount_paid,0),
	               COALESCE(total_amount,0)-COALESCE(amount_paid,0) AS due, invoice_date
	        FROM sales_invoices WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qargs := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND status=$3"
		qargs = append(qargs, status)
	}
	qry += fmt.Sprintf(" ORDER BY invoice_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var num, cust, st string
		var total, paid, due float64
		var dt interface{}
		if rows.Scan(&num, &cust, &st, &total, &paid, &due, &dt) == nil {
			out = append(out, gin.H{"invoice_number": num, "customer": cust, "status": st, "total": total, "paid": paid, "due": due, "date": dt})
		}
	}
	return out, nil
}

func toolFinancialSummary(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	// Sum current_balance for the well-known account codes in this chart.
	balByPrefix := func(prefix string) float64 {
		var v float64
		_ = h.db.QueryRow(`SELECT COALESCE(SUM(current_balance),0) FROM accounts
			WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)
			  AND regexp_replace(code,'[^0-9]','','g') LIKE $3`, tenantID, orgArg, prefix+"%").Scan(&v)
		return v
	}
	return gin.H{
		"cash_5010":        balByPrefix("5010"),
		"bank_5110":        balByPrefix("5110"),
		"receivables_4010": balByPrefix("4010"),
		"payables_6010":    balByPrefix("6010"),
		"revenue_9010":     balByPrefix("9010"),
		"note":             "Balances are the current chart-of-accounts balances (so'm).",
	}, nil
}

func toolLowStock(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	// Stock is summed only over warehouses in the active organization, so a
	// multi-company tenant doesn't mix another company's stock into the total.
	rows, err := h.db.Query(`
		SELECT name, stock FROM (
			SELECT p.name AS name, COALESCE((
				SELECT SUM(i.quantity_on_hand)
				FROM inventory i JOIN warehouses w ON w.id=i.warehouse_id
				WHERE i.product_id=p.id AND ($2::uuid IS NULL OR w.organization_id=$2)
			),0) AS stock
			FROM products p
			WHERE p.tenant_id=$1 AND p.deleted_at IS NULL
		) t
		WHERE stock <= 0
		ORDER BY stock ASC LIMIT 15`, tenantID, orgArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var name string
		var stock float64
		if rows.Scan(&name, &stock) == nil {
			out = append(out, gin.H{"product": name, "stock": stock})
		}
	}
	return out, nil
}

func toolCreateContact(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	name := argStr(args, "name")
	typ := argStr(args, "type")
	if name == "" || (typ != "customer" && typ != "vendor") {
		return nil, fmt.Errorf("name and a valid type (customer|vendor) are required")
	}
	phone := argStr(args, "phone")
	inn := argStr(args, "inn")
	id := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO contacts (id, tenant_id, organization_id, type, name, phone, tax_id, is_active, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,true,$8,NOW(),NOW())`,
		id, tenantID, orgArg, typ, name, nullIfEmpty(phone), nullIfEmpty(inn), userID); err != nil {
		return nil, err
	}
	return gin.H{"id": id.String(), "name": name, "type": typ, "created": true}, nil
}

// ---- write execs -----------------------------------------------------------

func toolCreateSalesOrder(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	custID, custName, err := resolveContactID(h, tenantID, argStr(args, "customer"), "customer")
	if err != nil {
		return nil, err
	}
	lines, subtotal, err := parseLines(h, tenantID, args["lines"])
	if err != nil {
		return nil, err
	}
	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	soID := uuid.New()
	num := docNumber("SO")
	if _, err := tx.Exec(`INSERT INTO sales_orders
		(id, tenant_id, organization_id, order_number, customer_id, order_date, subtotal, total_amount, paid_amount, status, payment_status, notes, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,CURRENT_DATE,$6,$6,0,'draft','unpaid',$7,$8,now(),now())`,
		soID, tenantID, orgArg, num, custID, subtotal, "Created by AI agent", userID); err != nil {
		return nil, err
	}
	for i, l := range lines {
		if _, err := tx.Exec(`INSERT INTO sales_order_lines
			(id, sales_order_id, line_number, product_id, quantity, unit_price, line_total, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,now(),now())`,
			uuid.New(), soID, i+1, l.ProductID, l.Qty, l.Price, l.Total); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return gin.H{"order_number": num, "customer": custName, "lines": len(lines), "total": subtotal, "status": "draft"}, nil
}

func toolRecordPayment(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	dir := argStr(args, "direction")
	typ, ctype := "receipt", "customer"
	if dir == "out" {
		typ, ctype = "payment", "vendor"
	}
	contID, contName, err := resolveContactID(h, tenantID, argStr(args, "contact"), ctype)
	if err != nil {
		if contID, contName, err = resolveContactID(h, tenantID, argStr(args, "contact"), ""); err != nil {
			return nil, err
		}
	}
	amount := toFloat(args["amount"])
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	num := docNumber("PAY")
	if _, err := h.db.Exec(`INSERT INTO payments
		(id, tenant_id, organization_id, payment_number, type, contact_id, payment_date, amount, exchange_rate, reference, notes, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,CURRENT_DATE,$7,1,$8,$9,'draft',now(),now())`,
		uuid.New(), tenantID, orgArg, num, typ, contID, amount, nullIfEmpty(argStr(args, "reference")), "AI agent draft"); err != nil {
		return nil, err
	}
	return gin.H{"payment_number": num, "contact": contName, "amount": amount, "direction": dir, "status": "draft (confirm in app to post)"}, nil
}

func toolCreateSalesInvoice(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	custID, custName, err := resolveContactID(h, tenantID, argStr(args, "customer"), "customer")
	if err != nil {
		return nil, err
	}
	lines, subtotal, err := parseLines(h, tenantID, args["lines"])
	if err != nil {
		return nil, err
	}
	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	invID := uuid.New()
	num := docNumber("INV")
	if _, err := tx.Exec(`INSERT INTO sales_invoices
		(id, tenant_id, organization_id, invoice_number, customer_id, customer_name, sales_order_id, invoice_date, due_date,
		 subtotal, discount_amount, tax_amount, total_amount, amount_paid, status, notes, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULL,CURRENT_DATE,CURRENT_DATE+30,$7,0,0,$7,0,'draft',$8,$9,now(),now())`,
		invID, tenantID, orgArg, num, custID, custName, subtotal, "Created by AI agent", userID); err != nil {
		return nil, err
	}
	for i, l := range lines {
		if _, err := tx.Exec(`INSERT INTO sales_invoice_lines
			(id, sales_invoice_id, line_number, product_id, description, quantity, unit_price, discount_amount, tax_amount, line_total, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,0,0,$8,now())`,
			uuid.New(), invID, i+1, l.ProductID, l.Name, l.Qty, l.Price, l.Total); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return gin.H{"invoice_number": num, "customer": custName, "lines": len(lines), "total": subtotal, "status": "draft"}, nil
}

func toolStockAdjust(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	pid, pname, _, err := resolveProduct(h, tenantID, argStr(args, "product"))
	if err != nil {
		return nil, err
	}
	whid, whname, err := resolveWarehouse(h, tenantID, orgArg, argStr(args, "warehouse"))
	if err != nil {
		return nil, err
	}
	newQty := toFloat(args["new_quantity"])
	var invID uuid.UUID
	var cur float64
	e := h.db.QueryRow(`SELECT id, quantity_on_hand FROM inventory
		WHERE tenant_id=$1 AND product_id=$2 AND warehouse_id=$3 AND (lot_number IS NULL OR lot_number='')
		ORDER BY quantity_on_hand DESC LIMIT 1`, tenantID, pid, whid).Scan(&invID, &cur)
	if e == sql.ErrNoRows {
		invID = uuid.New()
		if _, err := h.db.Exec(`INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, quantity_on_hand, quantity_reserved, created_at, updated_at)
			VALUES ($1,$2,$3,$4,0,0,now(),now())`, invID, tenantID, pid, whid); err != nil {
			return nil, err
		}
	} else if e != nil {
		return nil, e
	}
	delta := newQty - cur
	if _, err := h.db.Exec(`UPDATE inventory SET quantity_on_hand=$1, last_movement_date=now(), updated_at=now() WHERE id=$2`, newQty, invID); err != nil {
		return nil, err
	}
	h.db.Exec(`INSERT INTO inventory_transactions
		(id, tenant_id, organization_id, inventory_id, transaction_type, reference_type, quantity, reason, transaction_date, created_at)
		VALUES ($1,$2,$3,$4,'adjustment','ai_agent',$5,$6,now(),now())`,
		uuid.New(), tenantID, orgArg, invID, delta, nullIfEmpty(argStr(args, "reason")))
	return gin.H{"product": pname, "warehouse": whname, "from": cur, "to": newQty, "delta": delta}, nil
}

func toolStockTransfer(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	pid, pname, _, err := resolveProduct(h, tenantID, argStr(args, "product"))
	if err != nil {
		return nil, err
	}
	fromWh, fromName, err := resolveWarehouse(h, tenantID, orgArg, argStr(args, "from_warehouse"))
	if err != nil {
		return nil, err
	}
	toWh, toName, err := resolveWarehouse(h, tenantID, orgArg, argStr(args, "to_warehouse"))
	if err != nil {
		return nil, err
	}
	qty := toFloat(args["quantity"])
	if qty <= 0 {
		return nil, fmt.Errorf("quantity must be greater than zero")
	}
	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var srcID uuid.UUID
	if err := tx.QueryRow(`SELECT id FROM inventory WHERE tenant_id=$1 AND product_id=$2 AND warehouse_id=$3 AND (lot_number IS NULL OR lot_number='') LIMIT 1`,
		tenantID, pid, fromWh).Scan(&srcID); err != nil {
		return nil, fmt.Errorf("no stock of %q in %q to transfer", pname, fromName)
	}
	var dstID uuid.UUID
	e := tx.QueryRow(`SELECT id FROM inventory WHERE tenant_id=$1 AND product_id=$2 AND warehouse_id=$3 AND (lot_number IS NULL OR lot_number='') LIMIT 1`,
		tenantID, pid, toWh).Scan(&dstID)
	if e == sql.ErrNoRows {
		dstID = uuid.New()
		if _, err := tx.Exec(`INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, quantity_on_hand, quantity_reserved, created_at, updated_at)
			VALUES ($1,$2,$3,$4,0,0,now(),now())`, dstID, tenantID, pid, toWh); err != nil {
			return nil, err
		}
	} else if e != nil {
		return nil, e
	}
	if _, err := tx.Exec(`UPDATE inventory SET quantity_on_hand=quantity_on_hand-$1, last_movement_date=now(), updated_at=now() WHERE id=$2`, qty, srcID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE inventory SET quantity_on_hand=quantity_on_hand+$1, last_movement_date=now(), updated_at=now() WHERE id=$2`, qty, dstID); err != nil {
		return nil, err
	}
	reason := fmt.Sprintf("AI agent transfer %s -> %s", fromName, toName)
	tx.Exec(`INSERT INTO inventory_transactions (id, tenant_id, organization_id, inventory_id, transaction_type, reference_type, quantity, reason, transaction_date, created_at)
		VALUES ($1,$2,$3,$4,'transfer','ai_agent',$5,$6,now(),now())`, uuid.New(), tenantID, orgArg, srcID, -qty, reason)
	tx.Exec(`INSERT INTO inventory_transactions (id, tenant_id, organization_id, inventory_id, transaction_type, reference_type, quantity, reason, transaction_date, created_at)
		VALUES ($1,$2,$3,$4,'transfer','ai_agent',$5,$6,now(),now())`, uuid.New(), tenantID, orgArg, dstID, qty, reason)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return gin.H{"product": pname, "from": fromName, "to": toName, "quantity": qty}, nil
}

// ---- extra module read execs ----------------------------------------------

func rowsToList(rows *sql.Rows, err error, scan func(*sql.Rows) (gin.H, bool)) (interface{}, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		if h, ok := scan(rows); ok {
			out = append(out, h)
		}
	}
	return out, nil
}

func toolListPurchaseOrders(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT po.order_number, COALESCE(v.name,''), COALESCE(po.status,''), COALESCE(po.total_amount,0), po.order_date
	        FROM purchase_orders po LEFT JOIN contacts v ON v.id=po.vendor_id
	        WHERE po.tenant_id=$1 AND po.deleted_at IS NULL AND ($2::uuid IS NULL OR po.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND po.status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY po.order_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, vend, st string
		var total float64
		var dt interface{}
		if r.Scan(&num, &vend, &st, &total, &dt) != nil {
			return nil, false
		}
		return gin.H{"order_number": num, "vendor": vend, "status": st, "total": total, "date": dt}, true
	})
}

func toolListVendorBills(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT pi.invoice_number, COALESCE(v.name,''), COALESCE(pi.status,''), COALESCE(pi.total_amount,0), COALESCE(pi.amount_paid,0),
	               COALESCE(pi.total_amount,0)-COALESCE(pi.amount_paid,0), pi.invoice_date
	        FROM purchase_invoices pi LEFT JOIN contacts v ON v.id=pi.vendor_id
	        WHERE pi.tenant_id=$1 AND pi.deleted_at IS NULL AND ($2::uuid IS NULL OR pi.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND pi.status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY pi.invoice_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, vend, st string
		var total, paid, due float64
		var dt interface{}
		if r.Scan(&num, &vend, &st, &total, &paid, &due, &dt) != nil {
			return nil, false
		}
		return gin.H{"invoice_number": num, "vendor": vend, "status": st, "total": total, "paid": paid, "due": due, "date": dt}, true
	})
}

func toolListProductionOrders(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT po.code, COALESCE(p.name,''), COALESCE(po.status,''), COALESCE(po.quantity_planned,0), COALESCE(po.quantity_produced,0)
	        FROM production_orders po LEFT JOIN products p ON p.id=po.product_id
	        WHERE po.tenant_id=$1 AND po.deleted_at IS NULL AND ($2::uuid IS NULL OR po.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND po.status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY po.created_at DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var code, prod, st string
		var planned, produced float64
		if r.Scan(&code, &prod, &st, &planned, &produced) != nil {
			return nil, false
		}
		return gin.H{"code": code, "product": prod, "status": st, "planned": planned, "produced": produced}, true
	})
}

func toolFindEmployees(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	q := argStr(args, "query")
	rows, err := h.db.Query(`
		SELECT TRIM(COALESCE(first_name,'')||' '||COALESCE(last_name,'')), COALESCE(job_title,''), COALESCE(phone,''), COALESCE(status,'')
		FROM employees
		WHERE tenant_id=$1 AND ($3::uuid IS NULL OR organization_id=$3)
		  AND (COALESCE(first_name,'')||' '||COALESCE(last_name,'')) ILIKE '%'||$2||'%'
		ORDER BY first_name ASC LIMIT 10`, tenantID, q, orgArg)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var name, title, phone, st string
		if r.Scan(&name, &title, &phone, &st) != nil {
			return nil, false
		}
		return gin.H{"name": name, "job_title": title, "phone": phone, "status": st}, true
	})
}

func toolListExpenses(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT COALESCE(expense_number,''), COALESCE(description,''), COALESCE(NULLIF(vendor_name,''), employee_name, ''),
	               COALESCE(total_amount, amount, 0), COALESCE(status,''), expense_date
	        FROM expenses WHERE tenant_id=$1 AND deleted_at IS NULL`
	qa := []interface{}{tenantID}
	if status != "" {
		qry += " AND status=$2"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY expense_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, desc, who, st string
		var amt float64
		var dt interface{}
		if r.Scan(&num, &desc, &who, &amt, &st, &dt) != nil {
			return nil, false
		}
		return gin.H{"expense_number": num, "description": desc, "party": who, "amount": amt, "status": st, "date": dt}, true
	})
}

func toolListProjects(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT COALESCE(code,''), COALESCE(name,''), COALESCE(client_name,''), COALESCE(status,''),
		       COALESCE(contract_amount,0), COALESCE(progress_percent,0)
		FROM construction_projects
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)
		ORDER BY created_date DESC NULLS LAST LIMIT %d`, limit), tenantID, orgArg)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var code, name, client, st string
		var amount, progress float64
		if r.Scan(&code, &name, &client, &st, &amount, &progress) != nil {
			return nil, false
		}
		return gin.H{"code": code, "name": name, "client": client, "status": st, "contract_amount": amount, "progress_percent": progress}, true
	})
}

func toolCreateVendorBill(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	venID, venName, err := resolveContactID(h, tenantID, argStr(args, "vendor"), "vendor")
	if err != nil {
		return nil, err
	}
	amount := toFloat(args["amount"])
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	num := docNumber("BILL")
	if _, err := h.db.Exec(`INSERT INTO purchase_invoices
		(id, tenant_id, organization_id, invoice_number, vendor_id, vendor_invoice_number, invoice_date, due_date,
		 subtotal, discount_amount, tax_rate_id, tax_amount, total_amount, amount_paid, status, three_way_match_status,
		 notes, currency_id, exchange_rate, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,CURRENT_DATE,CURRENT_DATE+30,$7,0,NULL,0,$7,0,'draft','pending',$8,NULL,1,$9,now(),now())`,
		uuid.New(), tenantID, orgArg, num, venID, nullIfEmpty(argStr(args, "vendor_invoice_number")),
		amount, nullIfEmpty(argStr(args, "description")), userID); err != nil {
		return nil, err
	}
	return gin.H{"bill_number": num, "vendor": venName, "amount": amount, "status": "draft"}, nil
}

// ---- drill-down & report read execs ----------------------------------------

func toolGetSalesOrder(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	num := argStr(args, "order_number")
	if num == "" {
		return nil, fmt.Errorf("order_number is required")
	}
	var soID uuid.UUID
	var cust, st, payst, notes string
	var subtotal, total, paid float64
	var dt interface{}
	err := h.db.QueryRow(`SELECT so.id, COALESCE(v.name,''), COALESCE(so.status,''), COALESCE(so.payment_status,''),
		COALESCE(so.notes,''), COALESCE(so.subtotal,0), COALESCE(so.total_amount,0), COALESCE(so.paid_amount,0), so.order_date
		FROM sales_orders so LEFT JOIN contacts v ON v.id=so.customer_id
		WHERE so.tenant_id=$1 AND so.deleted_at IS NULL AND so.order_number=$2
		  AND ($3::uuid IS NULL OR so.organization_id=$3) LIMIT 1`, tenantID, num, orgArg).
		Scan(&soID, &cust, &st, &payst, &notes, &subtotal, &total, &paid, &dt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no sales order found with number %q", num)
	}
	if err != nil {
		return nil, err
	}
	rows, err := h.db.Query(`SELECT COALESCE(p.name,''), COALESCE(sol.quantity,0), COALESCE(sol.unit_price,0), COALESCE(sol.line_total,0)
		FROM sales_order_lines sol LEFT JOIN products p ON p.id=sol.product_id
		WHERE sol.sales_order_id=$1 ORDER BY sol.line_number ASC`, soID)
	lines, lerr := rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var pn string
		var qty, price, lt float64
		if r.Scan(&pn, &qty, &price, &lt) != nil {
			return nil, false
		}
		return gin.H{"product": pn, "quantity": qty, "unit_price": price, "line_total": lt}, true
	})
	if lerr != nil {
		return nil, lerr
	}
	return gin.H{
		"order_number": num, "customer": cust, "status": st, "payment_status": payst,
		"subtotal": subtotal, "total": total, "paid": paid, "due": total - paid,
		"notes": notes, "date": dt, "lines": lines,
	}, nil
}

func toolCustomerStatement(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	custID, custName, err := resolveContactID(h, tenantID, argStr(args, "customer"), "customer")
	if err != nil {
		return nil, err
	}
	limit := argInt(args, "limit", 10, 30)
	var balance float64
	_ = h.db.QueryRow(`SELECT COALESCE(current_balance,0) FROM contacts WHERE id=$1`, custID).Scan(&balance)

	invRows, ierr := h.db.Query(fmt.Sprintf(`SELECT invoice_number, COALESCE(status,''), COALESCE(total_amount,0),
		COALESCE(amount_paid,0), COALESCE(total_amount,0)-COALESCE(amount_paid,0), invoice_date
		FROM sales_invoices WHERE tenant_id=$1 AND deleted_at IS NULL AND customer_id=$2
		ORDER BY invoice_date DESC NULLS LAST LIMIT %d`, limit), tenantID, custID)
	invoices, err := rowsToList(invRows, ierr, func(r *sql.Rows) (gin.H, bool) {
		var num, st string
		var total, paid, due float64
		var dt interface{}
		if r.Scan(&num, &st, &total, &paid, &due, &dt) != nil {
			return nil, false
		}
		return gin.H{"invoice_number": num, "status": st, "total": total, "paid": paid, "due": due, "date": dt}, true
	})
	if err != nil {
		return nil, err
	}
	payRows, perr := h.db.Query(fmt.Sprintf(`SELECT payment_number, COALESCE(type,''), COALESCE(amount,0), payment_date, COALESCE(reference,'')
		FROM payments WHERE tenant_id=$1 AND contact_id=$2
		ORDER BY payment_date DESC NULLS LAST LIMIT %d`, limit), tenantID, custID)
	payments, err := rowsToList(payRows, perr, func(r *sql.Rows) (gin.H, bool) {
		var num, typ, ref string
		var amt float64
		var dt interface{}
		if r.Scan(&num, &typ, &amt, &dt, &ref) != nil {
			return nil, false
		}
		return gin.H{"payment_number": num, "type": typ, "amount": amt, "date": dt, "reference": ref}, true
	})
	if err != nil {
		return nil, err
	}
	return gin.H{"customer": custName, "current_balance": balance, "invoices": invoices, "payments": payments}, nil
}

// agingBuckets runs an aging query over an invoice table and returns bucket
// totals plus the top outstanding parties. party = the joined contact name.
func agingBuckets(h *Handler, tenantID uuid.UUID, orgArg interface{}, table, partyCol string) (interface{}, error) {
	qry := fmt.Sprintf(`
		SELECT COALESCE(v.name,''), COALESCE(t.total_amount,0)-COALESCE(t.amount_paid,0) AS due,
		       CASE
		         WHEN t.due_date IS NULL OR t.due_date >= CURRENT_DATE THEN 'current'
		         WHEN CURRENT_DATE - t.due_date <= 30 THEN '1_30'
		         WHEN CURRENT_DATE - t.due_date <= 60 THEN '31_60'
		         WHEN CURRENT_DATE - t.due_date <= 90 THEN '61_90'
		         ELSE '90_plus'
		       END AS bucket
		FROM %s t LEFT JOIN contacts v ON v.id=t.%s
		WHERE t.tenant_id=$1 AND t.deleted_at IS NULL AND ($2::uuid IS NULL OR t.organization_id=$2)
		  AND COALESCE(t.total_amount,0)-COALESCE(t.amount_paid,0) > 0.005`, table, partyCol)
	rows, err := h.db.Query(qry, tenantID, orgArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := map[string]float64{"current": 0, "1_30": 0, "31_60": 0, "61_90": 0, "90_plus": 0}
	perParty := map[string]float64{}
	var totalDue float64
	for rows.Next() {
		var party, bucket string
		var due float64
		if rows.Scan(&party, &due, &bucket) != nil {
			continue
		}
		buckets[bucket] += due
		if party == "" {
			party = "(unknown)"
		}
		perParty[party] += due
		totalDue += due
	}
	// Top parties by outstanding.
	type kv struct {
		Name string  `json:"name"`
		Due  float64 `json:"due"`
	}
	top := []kv{}
	for k, v := range perParty {
		top = append(top, kv{k, v})
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].Due > top[i].Due {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	if len(top) > 10 {
		top = top[:10]
	}
	return gin.H{"total_outstanding": totalDue, "buckets": buckets, "top": top}, nil
}

func toolAgedReceivables(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	return agingBuckets(h, tenantID, orgArg, "sales_invoices", "customer_id")
}

func toolAgedPayables(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	return agingBuckets(h, tenantID, orgArg, "purchase_invoices", "vendor_id")
}

func toolSalesSummary(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	// Map the period to a SQL lower bound on invoice_date.
	lower := "date_trunc('month', CURRENT_DATE)"
	switch argStr(args, "period") {
	case "today":
		lower = "CURRENT_DATE"
	case "this_week":
		lower = "date_trunc('week', CURRENT_DATE)"
	case "this_year":
		lower = "date_trunc('year', CURRENT_DATE)"
	case "last_30_days":
		lower = "CURRENT_DATE - INTERVAL '30 days'"
	case "this_month", "":
		lower = "date_trunc('month', CURRENT_DATE)"
	}
	var cnt int
	var invoiced, collected float64
	err := h.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*), COALESCE(SUM(total_amount),0), COALESCE(SUM(amount_paid),0)
		FROM sales_invoices WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)
		  AND invoice_date >= %s`, lower), tenantID, orgArg).Scan(&cnt, &invoiced, &collected)
	if err != nil {
		return nil, err
	}
	period := argStr(args, "period")
	if period == "" {
		period = "this_month"
	}
	return gin.H{"period": period, "invoice_count": cnt, "invoiced": invoiced, "collected": collected, "outstanding": invoiced - collected}, nil
}

func toolListBankAccounts(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	rows, err := h.db.Query(`
		SELECT COALESCE(NULLIF(name,''), bank_name, ''), COALESCE(bank_name,''), COALESCE(account_number,''),
		       COALESCE(currency,'UZS'), COALESCE(account_type,''), COALESCE(balance,0)
		FROM bank_accounts
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)
		  AND COALESCE(is_active,true)=true
		ORDER BY balance DESC`, tenantID, orgArg)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var name, bank, acct, cur, atype string
		var bal float64
		if r.Scan(&name, &bank, &acct, &cur, &atype, &bal) != nil {
			return nil, false
		}
		return gin.H{"name": name, "bank": bank, "account_number": acct, "currency": cur, "type": atype, "balance": bal}, true
	})
}

func toolListFixedAssets(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 15, 50)
	status := argStr(args, "status")
	qry := `SELECT COALESCE(asset_code,''), COALESCE(name,''), COALESCE(category_name,''), COALESCE(acquisition_cost,0),
	               COALESCE(accumulated_depreciation,0), COALESCE(book_value, current_value, 0), COALESCE(status,''),
	               COALESCE(custodian_name,''), COALESCE(location,'')
	        FROM fixed_assets WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY acquisition_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var code, name, cat, st, custodian, loc string
		var cost, accDep, bookVal float64
		if r.Scan(&code, &name, &cat, &cost, &accDep, &bookVal, &st, &custodian, &loc) != nil {
			return nil, false
		}
		return gin.H{"asset_code": code, "name": name, "category": cat, "acquisition_cost": cost,
			"accumulated_depreciation": accDep, "book_value": bookVal, "status": st, "custodian": custodian, "location": loc}, true
	})
}

func toolListContracts(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 15, 50)
	qry := `SELECT COALESCE(contract_number,''), COALESCE(vendor_name,''), COALESCE(title,''), COALESCE(contract_type,''),
	               start_date, end_date, COALESCE(value,0), COALESCE(currency,'UZS'), COALESCE(status,'')
	        FROM procurement_contracts WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if d := argInt(args, "expiring_within_days", 0, 3650); d > 0 {
		qry += fmt.Sprintf(" AND status='active' AND end_date IS NOT NULL AND end_date <= CURRENT_DATE + %d", d)
	}
	qry += fmt.Sprintf(" ORDER BY end_date ASC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, vend, title, ctype, cur, st string
		var start, end interface{}
		var val float64
		if r.Scan(&num, &vend, &title, &ctype, &start, &end, &val, &cur, &st) != nil {
			return nil, false
		}
		return gin.H{"contract_number": num, "vendor": vend, "title": title, "type": ctype,
			"start_date": start, "end_date": end, "value": val, "currency": cur, "status": st}, true
	})
}

func toolGetPurchaseOrder(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	num := argStr(args, "order_number")
	if num == "" {
		return nil, fmt.Errorf("order_number is required")
	}
	var poID uuid.UUID
	var vend, st string
	var total float64
	var dt interface{}
	err := h.db.QueryRow(`SELECT po.id, COALESCE(v.name,''), COALESCE(po.status,''), COALESCE(po.total_amount,0), po.order_date
		FROM purchase_orders po LEFT JOIN contacts v ON v.id=po.vendor_id
		WHERE po.tenant_id=$1 AND po.deleted_at IS NULL AND po.order_number=$2
		  AND ($3::uuid IS NULL OR po.organization_id=$3) LIMIT 1`, tenantID, num, orgArg).
		Scan(&poID, &vend, &st, &total, &dt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no purchase order found with number %q", num)
	}
	if err != nil {
		return nil, err
	}
	rows, err := h.db.Query(`SELECT COALESCE(p.name,''), COALESCE(pol.quantity,0), COALESCE(pol.unit_price,0),
		COALESCE(pol.line_total,0), COALESCE(pol.quantity_received,0)
		FROM purchase_order_lines pol LEFT JOIN products p ON p.id=pol.product_id
		WHERE pol.purchase_order_id=$1 ORDER BY pol.line_number ASC`, poID)
	lines, lerr := rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var pn string
		var qty, price, lt, recv float64
		if r.Scan(&pn, &qty, &price, &lt, &recv) != nil {
			return nil, false
		}
		return gin.H{"product": pn, "quantity": qty, "unit_price": price, "line_total": lt, "quantity_received": recv}, true
	})
	if lerr != nil {
		return nil, lerr
	}
	return gin.H{"order_number": num, "vendor": vend, "status": st, "total": total, "date": dt, "lines": lines}, nil
}
