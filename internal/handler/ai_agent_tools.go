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
		(id, tenant_id, organization_id, invoice_number, customer_id, sales_order_id, invoice_date, due_date,
		 subtotal, discount_amount, tax_amount, total_amount, amount_paid, status, notes, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,NULL,CURRENT_DATE,CURRENT_DATE+30,$6,0,0,$6,0,'draft',$7,$8,now(),now())`,
		invID, tenantID, orgArg, num, custID, subtotal, "Created by AI agent", userID); err != nil {
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
