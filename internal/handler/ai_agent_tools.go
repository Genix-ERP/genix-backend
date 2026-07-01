package handler

// Tool registry for the Genix ERP agent. Every exec is scoped to the caller's
// tenant + active organization. Read tools are pure SELECTs; the single write
// tool (create_contact) demonstrates the confirm-before-write flow.

import (
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
	}
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
	sql := `SELECT id::text, COALESCE(name,''), COALESCE(type,''), COALESCE(phone,''), COALESCE(tax_id,''), COALESCE(current_balance,0)
	        FROM contacts WHERE tenant_id=$1 AND deleted_at IS NULL AND name ILIKE '%'||$2||'%'`
	qargs := []interface{}{tenantID, q}
	if typ == "customer" || typ == "vendor" {
		sql += " AND type=$3"
		qargs = append(qargs, typ)
	}
	sql += " ORDER BY name ASC LIMIT 10"
	rows, err := h.db.Query(sql, qargs...)
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
	sql := `SELECT order_number, COALESCE(customer_name,''), COALESCE(status,''), COALESCE(total_amount,0), order_date
	        FROM sales_orders WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qargs := []interface{}{tenantID, orgArg}
	if status != "" {
		sql += " AND status=$3"
		qargs = append(qargs, status)
	}
	sql += fmt.Sprintf(" ORDER BY order_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(sql, qargs...)
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
	sql := `SELECT invoice_number, COALESCE(customer_name,''), COALESCE(status,''), COALESCE(total_amount,0), COALESCE(amount_paid,0),
	               COALESCE(total_amount,0)-COALESCE(amount_paid,0) AS due, invoice_date
	        FROM sales_invoices WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qargs := []interface{}{tenantID, orgArg}
	if status != "" {
		sql += " AND status=$3"
		qargs = append(qargs, status)
	}
	sql += fmt.Sprintf(" ORDER BY invoice_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(sql, qargs...)
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
	rows, err := h.db.Query(`
		SELECT p.name, COALESCE(SUM(i.quantity_on_hand),0) AS stock
		FROM products p LEFT JOIN inventory i ON i.product_id=p.id
		WHERE p.tenant_id=$1 AND p.deleted_at IS NULL
		GROUP BY p.id, p.name
		HAVING COALESCE(SUM(i.quantity_on_hand),0) <= 0
		ORDER BY stock ASC LIMIT 15`, tenantID)
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
