package handler

// Tool registry for the Genix ERP agent. Every exec is scoped to the caller's
// tenant + active organization. Read tools are pure SELECTs; the single write
// tool (create_contact) demonstrates the confirm-before-write flow.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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
			name:        "manufacturing_stats",
			description: "Ishlab chiqarish KPIlari (joriy oy): buyurtmalar holati bo'yicha soni, yakunlangan, kechikkan, ishlab chiqarilgan/brak miqdori va brak foizi. Answers 'ishlab chiqarish qanday ketyapti'.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolManufacturingStats,
		},
		{
			name:        "production_shortages",
			description: "MRP-lite yetishmovchilik ro'yxati: tasdiqlangan/jarayondagi ishlab chiqarish buyurtmalari uchun yetishmayotgan komponentlar (BOM ehtiyoj − ombor − ochiq xarid). Answers 'nima yetishmayapti', 'nimani sotib olish kerak'.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolProductionShortages,
		},
		{
			name:        "inventory_valuation",
			description: "Total stock value and value per warehouse for the active company; answers 'omborda qancha pul turibdi'. Also returns the top products by value.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolInventoryValuation,
		},
		{
			name:        "stock_movements",
			description: "Recent stock movements (kirim/chiqim/ko'chirish) from the movement ledger, newest first. Optional product-name filter and days window; answers 'X bilan nima bo'ldi', 'qaysi mahsulotlar qimirlamayapti' (compare against this list).",
			parameters: obj(map[string]interface{}{
				"query": str("Optional: part of a product name to filter by."),
				"days":  intp("Look-back window in days (default 30, max 365)."),
				"limit": intp("Max rows (default 20, max 100)."),
			}),
			exec: toolStockMovements,
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
			name:        "supplier_prices",
			description: "Per-supplier prices and delivery KPIs for ONE product: negotiated price-list price, last purchase price and date, average delivery days. Answers 'sementni oxirgi marta kimdan necha pulga oldik', 'qaysi yetkazib beruvchi eng tez/arzon'.",
			parameters:  obj(map[string]interface{}{"product": str("Part of the product name / SKU.")}, "product"),
			exec:        toolSupplierPrices,
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
			name:        "hr_stats",
			description: "HR summary numbers: total/active/on-leave/terminated employees, hires and exits this month, active salary fund, headcount by department. Answers 'nechta xodim bor', 'bu oy nechta xodim olindi', 'maosh fondi qancha', 'qaysi bo'limda nechta xodim'.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolHRStats,
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
			name:        "construction_stats",
			description: "Construction portfolio summary: project counts by status, contract total vs approved actual spend (obyekt xarajatlari), overdue projects, and the top budget-overrun projects with computed readiness. Answers 'qurilish qay ahvolda', 'qaysi loyihada byudjet oshyapti', 'nechta loyiha jarayonda', 'obyektlarga qancha sarflandi'.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolConstructionStats,
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
			description: "The fixed-asset (asosiy vositalar) register: inventory number, name, category, cost, accumulated depreciation, book value (qoldiq qiymat), status, responsible employee (javobgar shaxs) and location/construction object. Use for 'qoldiq qiymati qancha', 'qaysi texnika to'liq amortizatsiya bo'lgan', 'kimga qanday aktiv biriktirilgan'. Optional filters: status (draft, in_service, conserved, disposed) and query (name or inventory number).",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter: draft, in_service, conserved, disposed."), "query": str("Optional search by name or inventory number."), "limit": intp("Max rows (default 15, max 50).")}),
			exec:        toolListFixedAssets,
		},
		{
			name:        "list_contracts",
			description: "Contracts registry (number, counterparty, title, direction kirim/chiqim, start/end dates, value, status). Filter by status or counterparty name; set expiring_within_days to only show active contracts ending soon (renewal watch).",
			parameters: obj(map[string]interface{}{
				"status":               str("Optional status filter: draft, negotiation, signing, active, completed, cancelled, expired."),
				"counterparty":         str("Optional counterparty (contact) name filter."),
				"expiring_within_days": intp("Only active contracts whose end_date is within this many days from today."),
				"limit":                intp("Max rows (default 15, max 50)."),
			}),
			exec: toolListContracts,
		},
		{
			name:        "get_contract",
			description: "Full detail of ONE contract by its number: counterparty, direction, status, dates, value, effective amount (with amendments), paid total and outstanding balance.",
			parameters:  obj(map[string]interface{}{"contract_number": str("The contract number.")}, "contract_number"),
			exec:        toolGetContract,
		},
		{
			name:        "get_purchase_order",
			description: "Full detail of ONE purchase order by its number, including every product line (qty, unit price, line total, quantity received). Use after list_purchase_orders to drill in.",
			parameters:  obj(map[string]interface{}{"order_number": str("The purchase order number.")}, "order_number"),
			exec:        toolGetPurchaseOrder,
		},
		{
			name:        "list_workflows",
			description: "List automation workflows (name, category, trigger, status active/paused/draft, automation level, success rate, monthly cost savings). Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter (active, paused, draft)."), "limit": intp("Max rows (default 15, max 50).")}),
			exec:        toolListWorkflows,
		},
		{
			name:        "business_overview",
			description: "One-glance snapshot of the whole business for the active company: cash/bank total, receivables & payables outstanding, sales this month, open sales orders, out-of-stock products, active workflows. Use for 'how is my business', 'give me a summary'.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolBusinessOverview,
		},
		{
			name:        "list_payments",
			description: "List recorded payments (money in from customers / out to vendors): number, direction, party, amount, status, date. Use for 'recent payments', 'what did we receive/pay'.",
			parameters: obj(map[string]interface{}{
				"direction": map[string]interface{}{"type": "string", "enum": []string{"in", "out"}, "description": "in = customer receipts, out = vendor payments. Optional."},
				"status":    str("Optional status filter (draft, confirmed, cancelled)."),
				"limit":     intp("Max rows (default 10, max 50)."),
			}),
			exec: toolListPayments,
		},
		{
			name:        "list_quotations",
			description: "List sales quotations (quote number, title, customer, status, total, dates). Optional status filter (draft, sent, accepted, rejected, expired).",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListQuotations,
		},
		{
			name:        "list_leads",
			description: "List CRM leads/deals (company, contact, phone/email, source, stage, amount, responsible, won/lost). Optional status filter (stage code, e.g. new, in_progress, won, lost) and open_only flag.",
			parameters:  obj(map[string]interface{}{"status": str("Optional stage-code filter."), "open_only": map[string]interface{}{"type": "boolean", "description": "Only open (not won/lost) leads."}, "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListLeads,
		},
		{
			name:        "crm_summary",
			description: "CRM pipeline summary: open leads count + value, won this month (count + value), conversion %, top loss reasons. Use for 'bu oy nechta lid yutdik', 'voronka holati', 'nega yo'qotyapmiz'.",
			parameters:  obj(map[string]interface{}{}),
			exec:        toolCRMSummary,
		},
		{
			name:        "list_opportunities",
			description: "List sales opportunities / pipeline (name, customer, stage, expected revenue, probability, expected close date). Use for 'sales pipeline', 'deals in progress'.",
			parameters:  obj(map[string]interface{}{"limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListOpportunities,
		},
		{
			name:        "list_journal_entries",
			description: "List general-ledger journal entries (entry number, date, description, debit/credit totals, status). Use for accounting/audit questions. Optional status filter (draft, posted, cancelled).",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListJournalEntries,
		},
		{
			name:        "tax_summary",
			description: "VAT/NDS position for a period: output VAT collected on sales, input VAT paid on purchases, and the net VAT payable. Use for 'how much VAT/NDS do we owe', 'tax this month'.",
			parameters:  obj(map[string]interface{}{"period": map[string]interface{}{"type": "string", "enum": []string{"this_month", "this_year", "last_30_days", "this_quarter"}, "description": "Time window. Default this_month."}}),
			exec:        toolTaxSummary,
		},
		{
			name:        "list_leave_requests",
			description: "Employee leave/time-off requests (employee, type, dates, days, status). Use for 'who is on leave', 'pending leave requests'. Optional status filter (pending, approved, rejected).",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListLeaveRequests,
		},
		{
			name:        "list_attendance",
			description: "Recent employee attendance records (employee, date, clock in/out, status, department). Use for 'today's attendance', 'who clocked in'.",
			parameters:  obj(map[string]interface{}{"limit": intp("Max rows (default 15, max 50).")}),
			exec:        toolListAttendance,
		},
		{
			name:        "list_payroll_periods",
			description: "Payroll periods/runs (code, name, dates, pay date, status, employee count, gross/net totals). Use for 'payroll this month', 'last payroll run'.",
			parameters:  obj(map[string]interface{}{"limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListPayrollPeriods,
		},
		{
			name:        "list_boms",
			description: "List bills of materials / manufacturing recipes (code, name, finished product, version, type, output quantity, default/active). Use for 'what BOMs exist', 'recipe for X'.",
			parameters:  obj(map[string]interface{}{"limit": intp("Max rows (default 15, max 50).")}),
			exec:        toolListBOMs,
		},
		{
			name:        "get_bom",
			description: "Full detail of ONE bill of materials (by code or product name), including every component line (component, quantity, unit, scrap %). Use to see what a product is made of.",
			parameters:  obj(map[string]interface{}{"bom": str("BOM code, BOM name, or the finished product's name.")}, "bom"),
			exec:        toolGetBOM,
		},
		{
			name:        "list_work_orders",
			description: "Manufacturing work orders / shop-floor tasks (number, name, work center, status, qty to produce vs produced, progress %). Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListWorkOrders,
		},
		{
			name:        "list_stock_counts",
			description: "Inventory stock counts / audits (count number, date, warehouse, type, status, total variance value). Use for 'recent stock counts', 'inventory audit results'.",
			parameters:  obj(map[string]interface{}{"limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListStockCounts,
		},
		{
			name:        "list_goods_receipts",
			description: "Goods receipts (incoming deliveries against POs): GR number, PO, supplier, warehouse, status, quality status, quantity, date. Use for 'what came in', 'recent deliveries'.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListGoodsReceipts,
		},
		{
			name:        "list_purchase_requisitions",
			description: "Internal purchase requisitions (PR number, dates, department, priority, status, total, purpose). Use for 'pending purchase requests'. Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListPurchaseRequisitions,
		},
		{
			name:        "list_rfqs",
			description: "Requests for quotation to vendors (RFQ number, title, status, deadline). Use for 'open RFQs', 'sourcing requests'. Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListRFQs,
		},
		{
			name:        "list_sales_returns",
			description: "Customer sales returns (return number, customer, date, status, refund status, total, reason). Use for 'recent returns from customers'. Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListSalesReturns,
		},
		{
			name:        "list_purchase_returns",
			description: "Returns to vendors (return number, supplier, date, status, credit/total value, reason). Use for 'returns to suppliers'. Optional status filter.",
			parameters:  obj(map[string]interface{}{"status": str("Optional status filter."), "limit": intp("Max rows (default 10, max 50).")}),
			exec:        toolListPurchaseReturns,
		},
		// ---- write tools (confirmation required) ----
		{
			name:        "create_lead",
			description: "Create a new CRM lead (potential deal). Requires confirmation before it runs.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"contact_name": str("Person's name."),
				"company_name": str("Optional company name."),
				"phone":        str("Optional phone number."),
				"email":        str("Optional email."),
				"amount":       map[string]interface{}{"type": "number", "description": "Optional expected deal amount (UZS)."},
				"source":       str("Optional source (telegram, website, referral, cold_call, other)."),
				"notes":        str("Optional note about what the client needs."),
			}, "contact_name"),
			exec: toolCreateLead,
		},
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
			name:        "create_contract",
			description: "Create a new DRAFT contract with an existing counterparty. Requires confirmation before it runs. The contract number is auto-generated.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"title":        str("Contract title/name."),
				"counterparty": str("Existing contact name (the counterparty)."),
				"direction":    map[string]interface{}{"type": "string", "enum": []string{"income", "expense"}, "description": "income = kirim (customer pays us), expense = chiqim (we pay). Default expense."},
				"start_date":   str("Start date YYYY-MM-DD (default today)."),
				"end_date":     str("Optional end date YYYY-MM-DD (omit for muddatsiz)."),
				"value":        map[string]interface{}{"type": "number", "description": "Contract amount."},
				"currency":     str("Currency code, default UZS."),
			}, "title", "counterparty"),
			exec: toolCreateContract,
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
			name:        "create_quotation",
			description: "Create a DRAFT sales quotation (taklif) for a customer with product lines. The user reviews it in the app, sends it and can convert it to an order with one click. Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"customer":    str("Customer name (must already exist; use find_contacts first)."),
				"valid_until": str("Optional validity date YYYY-MM-DD."),
				"lines": map[string]interface{}{"type": "array", "description": "Quotation lines.", "items": obj(map[string]interface{}{
					"product":    str("Product name (must exist)."),
					"quantity":   map[string]interface{}{"type": "number", "description": "Quantity."},
					"unit_price": map[string]interface{}{"type": "number", "description": "Optional unit price; defaults to the product's price."},
				}, "product", "quantity")},
			}, "customer", "lines"),
			exec: toolCreateQuotation,
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
		{
			name:        "create_workflow",
			description: "Create a DRAFT automation workflow. It stays draft until activated. Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"name":             str("Workflow name."),
				"category":         map[string]interface{}{"type": "string", "enum": []string{"hr", "procurement", "customer_support", "sales", "inventory", "finance", "marketing"}, "description": "Business area."},
				"trigger":          map[string]interface{}{"type": "string", "enum": []string{"manual", "scheduled", "event_based", "ai_triggered"}, "description": "What starts it. Default manual."},
				"automation_level": map[string]interface{}{"type": "string", "enum": []string{"manual", "semi_automated", "fully_automated"}, "description": "Default manual."},
				"description":      str("Optional description."),
			}, "name", "category"),
			exec: toolCreateWorkflow,
		},
		{
			name:        "set_workflow_status",
			description: "Activate, pause, or set to draft an existing workflow (by name). Requires confirmation.",
			mutating:    true,
			parameters: obj(map[string]interface{}{
				"name":   str("Workflow name (must exist)."),
				"status": map[string]interface{}{"type": "string", "enum": []string{"active", "paused", "draft"}, "description": "New status."},
			}, "name", "status"),
			exec: toolSetWorkflowStatus,
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

func toolInventoryValuation(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	// Same value formula as GET /inventory/stats (and the reconcile
	// endpoint): qty × COALESCE(NULLIF(unit_cost,0), cost_price, 0),
	// scrap warehouses excluded.
	whRows, whErr := h.db.Query(`
		SELECT w.name, COALESCE(SUM(i.quantity_on_hand * COALESCE(NULLIF(i.unit_cost,0), p.cost_price, 0)), 0) AS v
		FROM inventory i
		JOIN warehouses w ON w.id = i.warehouse_id AND w.deleted_at IS NULL
			AND COALESCE(w.warehouse_type,'regular') != 'scrap'
		LEFT JOIN products p ON p.id = i.product_id
		WHERE i.tenant_id = $1 AND ($2::uuid IS NULL OR w.organization_id = $2)
		GROUP BY w.name ORDER BY v DESC`, tenantID, orgArg)
	perWH, err := rowsToList(whRows, whErr, func(r *sql.Rows) (gin.H, bool) {
		var name string
		var v float64
		if r.Scan(&name, &v) != nil {
			return nil, false
		}
		return gin.H{"warehouse": name, "value": v}, true
	})
	if err != nil {
		return nil, err
	}

	tpRows, tpErr := h.db.Query(`
		SELECT p.name, COALESCE(SUM(i.quantity_on_hand), 0) AS qty,
		       COALESCE(SUM(i.quantity_on_hand * COALESCE(NULLIF(i.unit_cost,0), p.cost_price, 0)), 0) AS v
		FROM inventory i
		JOIN warehouses w ON w.id = i.warehouse_id AND COALESCE(w.warehouse_type,'regular') != 'scrap'
		JOIN products p ON p.id = i.product_id AND p.deleted_at IS NULL
		WHERE i.tenant_id = $1 AND i.quantity_on_hand > 0
		  AND ($2::uuid IS NULL OR w.organization_id = $2)
		GROUP BY p.name ORDER BY v DESC LIMIT 10`, tenantID, orgArg)
	topProducts, err := rowsToList(tpRows, tpErr, func(r *sql.Rows) (gin.H, bool) {
		var name string
		var qty, v float64
		if r.Scan(&name, &qty, &v) != nil {
			return nil, false
		}
		return gin.H{"product": name, "qty": qty, "value": v}, true
	})
	if err != nil {
		return nil, err
	}

	total := 0.0
	if list, ok := perWH.([]gin.H); ok {
		for _, r := range list {
			if v, ok := r["value"].(float64); ok {
				total += v
			}
		}
	}
	return gin.H{"total_value": total, "per_warehouse": perWH, "top_products_by_value": topProducts}, nil
}

func toolStockMovements(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	days := argInt(args, "days", 30, 365)
	limit := argInt(args, "limit", 20, 100)
	query := "%" + argStr(args, "query") + "%"
	mvRows, mvErr := h.db.Query(`
		SELECT COALESCE(p.name,'—'), COALESCE(w.name,'—'), sl.transaction_type,
		       sl.qty_delta, COALESCE(sl.total_cost,0), COALESCE(sl.reference_type,''),
		       to_char(sl.transaction_date, 'YYYY-MM-DD')
		FROM stock_ledger sl
		LEFT JOIN products p ON p.id = sl.product_id
		LEFT JOIN warehouses w ON w.id = sl.warehouse_id
		WHERE sl.tenant_id = $1
		  AND sl.transaction_date >= NOW() - ($2 || ' days')::interval
		  AND ($3::uuid IS NULL OR sl.organization_id = $3)
		  AND ($4 = '%%' OR p.name ILIKE $4)
		ORDER BY sl.transaction_date DESC LIMIT $5`,
		tenantID, days, orgArg, query, limit)
	return rowsToList(mvRows, mvErr, func(r *sql.Rows) (gin.H, bool) {
		var product, warehouse, txType, refType, date string
		var qty, cost float64
		if r.Scan(&product, &warehouse, &txType, &qty, &cost, &refType, &date) != nil {
			return nil, false
		}
		return gin.H{"product": product, "warehouse": warehouse, "type": txType,
			"qty": qty, "value": cost, "source": refType, "date": date}, true
	})
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

func toolCreateQuotation(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	custID, custName, err := resolveContactID(h, tenantID, argStr(args, "customer"), "customer")
	if err != nil {
		return nil, err
	}
	lines, subtotal, err := parseLines(h, tenantID, args["lines"])
	if err != nil {
		return nil, err
	}
	var validUntil interface{}
	if s := argStr(args, "valid_until"); s != "" {
		if t, perr := time.Parse("2006-01-02", s); perr == nil {
			validUntil = t
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Same QT series as the quotations handler (MAX+1 over sales_quotations)
	var qtNum int64
	_ = tx.QueryRow(`
		SELECT COALESCE(MAX(CAST(SUBSTRING(quotation_number FROM 3) AS BIGINT)), 0) + 1
		FROM sales_quotations
		WHERE tenant_id = $1 AND quotation_number ~ '^QT[0-9]+$'`,
		tenantID).Scan(&qtNum)
	if qtNum < 1 {
		qtNum = 1
	}
	num := fmt.Sprintf("QT%05d", qtNum)

	qID := uuid.New()
	if _, err := tx.Exec(`INSERT INTO sales_quotations
		(id, tenant_id, organization_id, quotation_number, customer_id, customer_name,
		 valid_until, subtotal, discount_percent, discount_amount, tax_percent, tax_amount,
		 total_amount, status, notes, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0,0,0,0,$8,'draft',$9,$10,now(),now())`,
		qID, tenantID, orgArg, num, custID, custName, validUntil, subtotal, "Created by AI agent", userID); err != nil {
		return nil, err
	}
	for i, l := range lines {
		var productName string
		_ = tx.QueryRow("SELECT COALESCE(name, '') FROM products WHERE id = $1", l.ProductID).Scan(&productName)
		if _, err := tx.Exec(`INSERT INTO sales_quotation_items
			(id, quotation_id, product_id, product_name, quantity, unit_price, total, sort_order, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())`,
			uuid.New(), qID, l.ProductID, productName, l.Qty, l.Price, l.Total, i+1); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return gin.H{"quotation_number": num, "customer": custName, "lines": len(lines), "total": subtotal, "status": "draft (review and send in the app)"}, nil
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
	var cur float64
	_ = h.db.QueryRow(`SELECT COALESCE(SUM(quantity_on_hand),0) FROM inventory
		WHERE tenant_id=$1 AND product_id=$2 AND warehouse_id=$3`, tenantID, pid, whid).Scan(&cur)
	delta := newQty - cur
	if delta == 0 {
		return gin.H{"product": pname, "warehouse": whname, "from": cur, "to": newQty, "delta": 0.0}, nil
	}

	// Routed through applyStockDelta so the AI path gets the same balance+
	// ledger atomicity, self-contained ledger row and AVECO handling as the
	// UI path (the old direct writes skipped unit_cost, created_by and events).
	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	newBal, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
		TenantID: tenantID, OrgID: aiOrgPtr(orgArg), ProductID: pid,
		WarehouseID: whid, Qty: delta, UnitCost: 0,
		TxType: "adjustment", RefType: "ai_agent",
		Reason:    argStr(args, "reason"),
		CreatedBy: userID, AllowNeg: true,
	})
	if dErr != nil {
		return nil, dErr
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	h.emitInventoryAdjusted(tenantID, pid, delta, newBal)
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
	reason := fmt.Sprintf("AI agent transfer %s -> %s", fromName, toName)
	// Both legs through applyStockDelta in one tx: negative-stock guard on
	// the source, destination valued at the source's average cost.
	newBal, cost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
		TenantID: tenantID, OrgID: aiOrgPtr(orgArg), ProductID: pid,
		WarehouseID: fromWh, Qty: -qty, UnitCost: 0,
		TxType: "transfer", RefType: "ai_agent",
		Reason: reason, FromWH: &fromWh, ToWH: &toWh,
		CreatedBy: userID,
	})
	if errors.Is(dErr, errInsufficientStock) {
		return nil, fmt.Errorf("not enough stock of %q in %q to transfer %v", pname, fromName, qty)
	}
	if dErr != nil {
		return nil, dErr
	}
	if _, _, dErr = h.applyStockDelta(tx, stockDeltaArgs{
		TenantID: tenantID, OrgID: aiOrgPtr(orgArg), ProductID: pid,
		WarehouseID: toWh, Qty: qty, UnitCost: cost,
		TxType: "transfer", RefType: "ai_agent",
		Reason: reason, FromWH: &fromWh, ToWH: &toWh,
		CreatedBy: userID,
	}); dErr != nil {
		return nil, dErr
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	h.emitInventoryAdjusted(tenantID, pid, -qty, newBal)
	return gin.H{"product": pname, "from": fromName, "to": toName, "quantity": qty}, nil
}

// aiOrgPtr converts the loosely-typed orgArg the agent tools carry into the
// *uuid.UUID applyStockDelta expects.
func aiOrgPtr(orgArg interface{}) *uuid.UUID {
	switch v := orgArg.(type) {
	case uuid.UUID:
		if v != uuid.Nil {
			return &v
		}
	case *uuid.UUID:
		if v != nil && *v != uuid.Nil {
			return v
		}
	}
	return nil
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

// toolSupplierPrices answers "kimdan necha pulga olamiz": for one product it
// merges the active vendor price list, the latest actual purchase price
// (supplier_price_history) and delivery speed (completed goods receipts vs
// order date) into one row per supplier, cheapest effective price first.
func toolSupplierPrices(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	productID, productName, _, err := resolveProduct(h, tenantID, argStr(args, "product"))
	if err != nil {
		return nil, err
	}

	rows, err := h.db.Query(`
		SELECT ct.id, ct.name,
		       vp.price, vp.lead_time_days,
		       hist.last_price, hist.last_date,
		       gr.avg_days
		FROM contacts ct
		LEFT JOIN LATERAL (
			SELECT v.price, v.lead_time_days FROM vendor_prices v
			WHERE v.tenant_id = $1 AND v.vendor_id = ct.id AND v.product_id = $2
			  AND v.is_active = true AND v.deleted_at IS NULL
			ORDER BY v.valid_from DESC NULLS LAST LIMIT 1
		) vp ON true
		LEFT JOIN LATERAL (
			SELECT sph.unit_price AS last_price, sph.effective_date AS last_date
			FROM supplier_price_history sph
			WHERE sph.tenant_id = $1 AND sph.vendor_id = ct.id AND sph.product_id = $2
			ORDER BY sph.effective_date DESC LIMIT 1
		) hist ON true
		LEFT JOIN LATERAL (
			SELECT AVG(g.receipt_date::date - po.order_date::date)::float AS avg_days
			FROM goods_receipts g
			JOIN purchase_orders po ON po.id = g.purchase_order_id
			WHERE g.supplier_id = ct.id AND g.tenant_id = $1 AND g.status = 'completed' AND g.deleted_at IS NULL
		) gr ON true
		WHERE ct.tenant_id = $1 AND ct.type IN ('vendor', 'both') AND ct.deleted_at IS NULL
		  AND (vp.price IS NOT NULL OR hist.last_price IS NOT NULL)
		ORDER BY COALESCE(vp.price, hist.last_price) ASC
		LIMIT 15
	`, tenantID, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []gin.H{}
	for rows.Next() {
		var vid uuid.UUID
		var name string
		var listPrice, lastPrice, avgDays *float64
		var leadDays *int
		var lastDate *time.Time
		if rows.Scan(&vid, &name, &listPrice, &leadDays, &lastPrice, &lastDate, &avgDays) != nil {
			continue
		}
		row := gin.H{"supplier": name}
		if listPrice != nil {
			row["pricelist_price"] = *listPrice
		}
		if lastPrice != nil {
			row["last_purchase_price"] = *lastPrice
		}
		if lastDate != nil {
			row["last_purchase_date"] = lastDate.Format("2006-01-02")
		}
		if leadDays != nil {
			row["lead_time_days"] = *leadDays
		}
		if avgDays != nil {
			row["avg_delivery_days"] = *avgDays
		}
		list = append(list, row)
	}
	if len(list) == 0 {
		return gin.H{"product": productName, "suppliers": list,
			"note": "no price data for this product yet (no price list entries or purchase history)"}, nil
	}
	return gin.H{"product": productName, "suppliers": list}, nil
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
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($3::uuid IS NULL OR organization_id=$3)
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

func toolHRStats(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	var total, active, onLeave, terminated, hiredThisMonth, exitsThisMonth int
	var salaryFund float64
	err := h.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'active'),
		       COUNT(*) FILTER (WHERE status = 'on_leave'),
		       COUNT(*) FILTER (WHERE status = 'terminated'),
		       COUNT(*) FILTER (WHERE date_trunc('month', hire_date) = date_trunc('month', CURRENT_DATE)),
		       COUNT(*) FILTER (WHERE termination_date IS NOT NULL AND date_trunc('month', termination_date) = date_trunc('month', CURRENT_DATE)),
		       COALESCE(SUM(base_salary) FILTER (WHERE status = 'active'), 0)
		FROM employees
		WHERE tenant_id = $1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id = $2)`,
		tenantID, orgArg).Scan(&total, &active, &onLeave, &terminated, &hiredThisMonth, &exitsThisMonth, &salaryFund)
	if err != nil {
		return nil, err
	}
	rows, err := h.db.Query(`
		SELECT COALESCE(d.name, ''), COUNT(*) FROM employees e
		LEFT JOIN departments d ON d.id = e.department_id AND d.deleted_at IS NULL
		WHERE e.tenant_id = $1 AND e.deleted_at IS NULL AND ($2::uuid IS NULL OR e.organization_id = $2)
		GROUP BY 1 ORDER BY 2 DESC LIMIT 10`, tenantID, orgArg)
	depts := []gin.H{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var cnt int
			if rows.Scan(&name, &cnt) == nil {
				depts = append(depts, gin.H{"department": name, "count": cnt})
			}
		}
	}
	return gin.H{
		"total": total, "active": active, "on_leave": onLeave, "terminated": terminated,
		"hired_this_month": hiredThisMonth, "exits_this_month": exitsThisMonth,
		"salary_fund": salaryFund, "by_department": depts,
	}, nil
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
	// Reads the unified fa_assets register (the legacy fixed_assets table was
	// deprecated by migration 453; assets created in the module were invisible
	// to the AI before this re-point — audit 2026-08-03).
	limit := argInt(args, "limit", 15, 50)
	status := argStr(args, "status")
	qry := `SELECT a.inventory_number, a.name, c.name_uz, a.cost,
	               a.accumulated_depreciation, (a.cost - a.accumulated_depreciation), a.status::text,
	               COALESCE(e.first_name || ' ' || e.last_name, ''), COALESCE(cp.name, COALESCE(a.location,'')),
	               a.commissioning_date, a.useful_life_months
	        FROM fa_assets a
	        JOIN fa_categories c ON c.id = a.category_id
	        LEFT JOIN employees e ON e.id = a.assigned_employee_id
	        LEFT JOIN construction_projects cp ON cp.id = a.construction_object_id
	        WHERE a.tenant_id=$1 AND a.deleted_at IS NULL AND ($2::uuid IS NULL OR a.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND a.status::text=$3"
		qa = append(qa, status)
	}
	if q := argStr(args, "query"); q != "" {
		qa = append(qa, q)
		qry += fmt.Sprintf(" AND (a.name ILIKE '%%'||$%d||'%%' OR a.inventory_number ILIKE '%%'||$%d||'%%')", len(qa), len(qa))
	}
	qry += fmt.Sprintf(" ORDER BY a.purchase_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var code, name, cat, st, custodian, loc string
		var cost, accDep, bookVal float64
		var comm interface{}
		var life int
		if r.Scan(&code, &name, &cat, &cost, &accDep, &bookVal, &st, &custodian, &loc, &comm, &life) != nil {
			return nil, false
		}
		return gin.H{"inventory_number": code, "name": name, "category": cat, "cost": cost,
			"accumulated_depreciation": accDep, "book_value": bookVal, "status": st,
			"responsible_employee": custodian, "location": loc, "commissioning_date": comm,
			"useful_life_months": life}, true
	})
}

func toolListContracts(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 15, 50)
	qry := `SELECT COALESCE(contract_number,''), COALESCE(vendor_name,''), COALESCE(title,''), COALESCE(direction,'expense'),
	               start_date, end_date, COALESCE(value,0), COALESCE(currency,'UZS'), COALESCE(status,'')
	        FROM procurement_contracts WHERE tenant_id=$1 AND deleted_at IS NULL AND archived_at IS NULL
	          AND ($2::uuid IS NULL OR organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if st := argStr(args, "status"); st != "" {
		qa = append(qa, st)
		qry += fmt.Sprintf(" AND status=$%d", len(qa))
	}
	if cp := argStr(args, "counterparty"); cp != "" {
		qa = append(qa, cp)
		qry += fmt.Sprintf(" AND vendor_name ILIKE '%%'||$%d||'%%'", len(qa))
	}
	if d := argInt(args, "expiring_within_days", 0, 3650); d > 0 {
		qry += fmt.Sprintf(" AND status='active' AND end_date IS NOT NULL AND end_date <= CURRENT_DATE + %d", d)
	}
	qry += fmt.Sprintf(" ORDER BY end_date ASC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, vend, title, direction, cur, st string
		var start, end interface{}
		var val float64
		if r.Scan(&num, &vend, &title, &direction, &start, &end, &val, &cur, &st) != nil {
			return nil, false
		}
		return gin.H{"contract_number": num, "counterparty": vend, "title": title, "direction": direction,
			"start_date": start, "end_date": end, "value": val, "currency": cur, "status": st}, true
	})
}

func toolGetContract(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	num := argStr(args, "contract_number")
	if num == "" {
		return nil, fmt.Errorf("contract_number is required")
	}
	var (
		id                                       uuid.UUID
		title, vendor, direction, status, cur    string
		value, effective, paid                   float64
		start                                    interface{}
		end, signed                              interface{}
		respName                                 sql.NullString
	)
	err := h.db.QueryRow(`
		SELECT c.id, COALESCE(c.title,''), COALESCE(v.name, c.vendor_name, ''), COALESCE(c.direction,'expense'),
		       COALESCE(c.status,''), COALESCE(c.currency,'UZS'), COALESCE(c.value,0),
		       COALESCE(c.value,0) + COALESCE((SELECT SUM(a.amount_delta) FROM contract_amendments a
		           WHERE a.contract_id = c.id AND a.deleted_at IS NULL), 0),
		       COALESCE((SELECT SUM(x.amount_paid) FROM (
		           SELECT si.amount_paid FROM sales_invoices si WHERE si.contract_id=c.id AND si.deleted_at IS NULL AND si.status<>'cancelled'
		           UNION ALL
		           SELECT pi.amount_paid FROM purchase_invoices pi WHERE pi.contract_id=c.id AND pi.deleted_at IS NULL AND pi.status<>'cancelled') x), 0),
		       c.start_date, c.end_date, c.signed_date,
		       TRIM(COALESCE(e.first_name,'')||' '||COALESCE(e.last_name,''))
		FROM procurement_contracts c
		LEFT JOIN contacts v ON v.id = c.vendor_id AND v.tenant_id = c.tenant_id
		LEFT JOIN employees e ON e.id = c.responsible_employee_id AND e.tenant_id = c.tenant_id
		WHERE c.tenant_id=$1 AND c.contract_number=$2 AND c.deleted_at IS NULL
		  AND ($3::uuid IS NULL OR c.organization_id=$3)
	`, tenantID, num, orgArg).Scan(&id, &title, &vendor, &direction, &status, &cur, &value,
		&effective, &paid, &start, &end, &signed, &respName)
	if err == sql.ErrNoRows {
		return gin.H{"found": false, "contract_number": num}, nil
	}
	if err != nil {
		return nil, err
	}
	return gin.H{
		"found": true, "id": id.String(), "contract_number": num, "title": title,
		"counterparty": vendor, "direction": direction, "status": status,
		"value": value, "effective_amount": effective, "paid_total": paid,
		"outstanding": effective - paid, "currency": cur,
		"start_date": start, "end_date": end, "signed_date": signed,
		"responsible": respName.String,
	}, nil
}

func toolCreateContract(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	title := argStr(args, "title")
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	contactID, contactName, err := resolveContactID(h, tenantID, argStr(args, "counterparty"), "")
	if err != nil {
		return nil, err
	}
	direction := argStr(args, "direction")
	if direction != "income" && direction != "expense" {
		direction = "expense"
	}
	startDate := time.Now()
	if s := argStr(args, "start_date"); s != "" {
		if d, perr := time.Parse("2006-01-02", s); perr == nil {
			startDate = d
		} else {
			return nil, fmt.Errorf("invalid start_date, expected YYYY-MM-DD")
		}
	}
	var endDate interface{}
	if s := argStr(args, "end_date"); s != "" {
		d, perr := time.Parse("2006-01-02", s)
		if perr != nil {
			return nil, fmt.Errorf("invalid end_date, expected YYYY-MM-DD")
		}
		endDate = d
	}
	value := 0.0
	if v, okV := args["value"].(float64); okV && v >= 0 {
		value = v
	}
	currency := argStr(args, "currency")
	if currency == "" {
		currency = "UZS"
	}

	id := uuid.New()
	number := h.nextContractNumber(tenantID)
	if _, err := h.db.Exec(`
		INSERT INTO procurement_contracts (
			id, tenant_id, organization_id, contract_number, title, vendor_id, vendor_name,
			direction, contract_type, status, start_date, end_date, value, currency,
			created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'fixed','draft',$9,$10,$11,$12,$13,NOW(),NOW())
	`, id, tenantID, orgArg, number, title, contactID, contactName,
		direction, startDate, endDate, value, currency, userID); err != nil {
		return nil, err
	}

	h.contractAudit(tenantID, userID, id, "create", nil, map[string]interface{}{
		"contract_number": number, "title": title, "vendor_name": contactName, "via": "ai_assistant",
	})
	h.EmitWorkflowEvent(tenantID, "contracts.created", map[string]interface{}{
		"record_id": id.String(), "contract_number": number, "title": title,
		"contact_name": contactName, "value": value, "direction": direction,
	})

	return gin.H{"id": id.String(), "contract_number": number, "title": title,
		"counterparty": contactName, "status": "draft", "created": true}, nil
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

// ---- workflow tools --------------------------------------------------------

func resolveWorkflow(h *Handler, tenantID uuid.UUID, name string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var nm string
	if err := h.db.QueryRow(`SELECT id, name FROM workflows
		WHERE tenant_id=$1 AND deleted_at IS NULL AND name ILIKE '%'||$2||'%'
		ORDER BY name ASC LIMIT 1`, tenantID, name).Scan(&id, &nm); err != nil {
		return uuid.Nil, "", fmt.Errorf("no workflow found matching %q", name)
	}
	return id, nm, nil
}

func toolListWorkflows(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 15, 50)
	status := argStr(args, "status")
	qry := `SELECT name, COALESCE(category,''), COALESCE(trigger,''), COALESCE(status,''),
	               COALESCE(automation_level,''), COALESCE(success_rate,0), COALESCE(cost_savings,0)
	        FROM workflows WHERE tenant_id=$1 AND deleted_at IS NULL`
	qa := []interface{}{tenantID}
	if status != "" {
		qry += " AND status=$2"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY created_at DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var name, cat, trig, st, auto string
		var succ, savings float64
		if r.Scan(&name, &cat, &trig, &st, &auto, &succ, &savings) != nil {
			return nil, false
		}
		return gin.H{"name": name, "category": cat, "trigger": trig, "status": st,
			"automation_level": auto, "success_rate": succ, "cost_savings": savings}, true
	})
}

func toolCreateWorkflow(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	name := argStr(args, "name")
	category := strings.ToLower(argStr(args, "category"))
	if name == "" || category == "" {
		return nil, fmt.Errorf("name and category are required")
	}
	validCat := map[string]bool{"hr": true, "procurement": true, "customer_support": true, "sales": true, "inventory": true, "finance": true, "marketing": true}
	if !validCat[category] {
		return nil, fmt.Errorf("category must be one of hr, procurement, customer_support, sales, inventory, finance, marketing")
	}
	trigger := strings.ToLower(argStr(args, "trigger"))
	if trigger == "" {
		trigger = "manual"
	}
	if !map[string]bool{"manual": true, "scheduled": true, "event_based": true, "ai_triggered": true}[trigger] {
		return nil, fmt.Errorf("trigger must be one of manual, scheduled, event_based, ai_triggered")
	}
	auto := strings.ToLower(argStr(args, "automation_level"))
	if auto == "" {
		auto = "manual"
	}
	if !map[string]bool{"manual": true, "semi_automated": true, "fully_automated": true}[auto] {
		return nil, fmt.Errorf("automation_level must be one of manual, semi_automated, fully_automated")
	}
	id := uuid.New()
	if _, err := h.db.Exec(`INSERT INTO workflows
		(id, tenant_id, name, description, category, trigger, status, automation_level, steps, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,'draft',$7,'[]',now(),now())`,
		id, tenantID, name, nullIfEmpty(argStr(args, "description")), category, trigger, auto); err != nil {
		return nil, err
	}
	return gin.H{"id": id.String(), "name": name, "category": category, "trigger": trigger, "automation_level": auto, "status": "draft"}, nil
}

func toolSetWorkflowStatus(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	wfID, wfName, err := resolveWorkflow(h, tenantID, argStr(args, "name"))
	if err != nil {
		return nil, err
	}
	status := strings.ToLower(argStr(args, "status"))
	if !map[string]bool{"active": true, "paused": true, "draft": true}[status] {
		return nil, fmt.Errorf("status must be one of active, paused, draft")
	}
	var old string
	_ = h.db.QueryRow(`SELECT status FROM workflows WHERE id=$1`, wfID).Scan(&old)
	if _, err := h.db.Exec(`UPDATE workflows SET status=$1, updated_at=now() WHERE id=$2 AND tenant_id=$3`, status, wfID, tenantID); err != nil {
		return nil, err
	}
	return gin.H{"workflow": wfName, "from": old, "to": status}, nil
}

// ---- business snapshot & remaining read execs -------------------------------

func toolBusinessOverview(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	f := func(q string, a ...interface{}) float64 { var v float64; _ = h.db.QueryRow(q, a...).Scan(&v); return v }
	i := func(q string, a ...interface{}) int { var v int; _ = h.db.QueryRow(q, a...).Scan(&v); return v }
	cash := f(`SELECT COALESCE(SUM(balance),0) FROM bank_accounts
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2) AND COALESCE(is_active,true)=true`, tenantID, orgArg)
	ar := f(`SELECT COALESCE(SUM(total_amount-amount_paid),0) FROM sales_invoices
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2) AND total_amount-amount_paid > 0.005`, tenantID, orgArg)
	ap := f(`SELECT COALESCE(SUM(total_amount-amount_paid),0) FROM purchase_invoices
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2) AND total_amount-amount_paid > 0.005`, tenantID, orgArg)
	salesMonth := f(`SELECT COALESCE(SUM(total_amount),0) FROM sales_invoices
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2) AND invoice_date >= date_trunc('month', CURRENT_DATE)`, tenantID, orgArg)
	openSO := i(`SELECT COUNT(*) FROM sales_orders
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2) AND status NOT IN ('delivered','cancelled','completed','draft')`, tenantID, orgArg)
	outOfStock := i(`SELECT COUNT(*) FROM (
		SELECT p.id FROM products p WHERE p.tenant_id=$1 AND p.deleted_at IS NULL
		AND COALESCE((SELECT SUM(inv.quantity_on_hand) FROM inventory inv JOIN warehouses w ON w.id=inv.warehouse_id
			WHERE inv.product_id=p.id AND ($2::uuid IS NULL OR w.organization_id=$2)),0) <= 0) t`, tenantID, orgArg)
	activeWf := i(`SELECT COUNT(*) FROM workflows WHERE tenant_id=$1 AND deleted_at IS NULL AND status='active'`, tenantID)
	return gin.H{
		"cash_and_bank":           cash,
		"receivables_outstanding": ar,
		"payables_outstanding":    ap,
		"sales_this_month":        salesMonth,
		"open_sales_orders":       openSO,
		"out_of_stock_products":   outOfStock,
		"active_workflows":        activeWf,
		"note":                    "Amounts in so'm; cash is the raw sum of bank/cash account balances.",
	}, nil
}

func toolListPayments(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	// payments has no deleted_at column.
	qry := `SELECT p.payment_number, COALESCE(p.type,''), COALESCE(v.name,''), COALESCE(p.amount,0),
	               COALESCE(p.status,''), p.payment_date, COALESCE(p.reference,'')
	        FROM payments p LEFT JOIN contacts v ON v.id=p.contact_id
	        WHERE p.tenant_id=$1 AND ($2::uuid IS NULL OR p.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if dir := argStr(args, "direction"); dir == "in" {
		qry += " AND p.type='receipt'"
	} else if dir == "out" {
		qry += " AND p.type='payment'"
	}
	if status := argStr(args, "status"); status != "" {
		qry += fmt.Sprintf(" AND p.status=$%d", len(qa)+1)
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY p.payment_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, typ, party, st, ref string
		var amt float64
		var dt interface{}
		if r.Scan(&num, &typ, &party, &amt, &st, &dt, &ref) != nil {
			return nil, false
		}
		dir := "in"
		if typ == "payment" {
			dir = "out"
		}
		return gin.H{"payment_number": num, "direction": dir, "party": party, "amount": amt, "status": st, "date": dt, "reference": ref}, true
	})
}

func toolListQuotations(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT q.quotation_number, COALESCE(q.title,''), COALESCE(v.name,''), COALESCE(q.status,''),
	               COALESCE(q.total_amount,0), q.quotation_date, q.expiry_date
	        FROM quotations q LEFT JOIN contacts v ON v.id=q.contact_id
	        WHERE q.tenant_id=$1 AND q.deleted_at IS NULL AND ($2::uuid IS NULL OR q.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND q.status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY q.quotation_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, title, cust, st string
		var total float64
		var qdate, expiry interface{}
		if r.Scan(&num, &title, &cust, &st, &total, &qdate, &expiry) != nil {
			return nil, false
		}
		return gin.H{"quotation_number": num, "title": title, "customer": cust, "status": st, "total": total, "date": qdate, "expiry_date": expiry}, true
	})
}

func toolListLeads(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	openOnly, _ := args["open_only"].(bool)
	qry := `SELECT COALESCE(l.company_name,''), COALESCE(l.contact_name,''), COALESCE(l.phone,''), COALESCE(l.email,''),
	               COALESCE(l.source,''), COALESCE(l.status,''), COALESCE(l.expected_value,0)::float8, COALESCE(l.currency,'UZS'),
	               COALESCE(TRIM(e.first_name || ' ' || e.last_name), ''),
	               (l.won_at IS NOT NULL), (l.lost_at IS NOT NULL)
	        FROM leads l
	        LEFT JOIN employees e ON e.id = l.responsible_employee_id
	        WHERE l.tenant_id=$1 AND l.deleted_at IS NULL AND ($2::uuid IS NULL OR l.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND l.status=$3"
		qa = append(qa, status)
	}
	if openOnly {
		qry += " AND l.won_at IS NULL AND l.lost_at IS NULL"
	}
	qry += fmt.Sprintf(" ORDER BY l.created_at DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var company, contact, phone, email, source, st, currency, responsible string
		var value float64
		var won, lost bool
		if r.Scan(&company, &contact, &phone, &email, &source, &st, &value, &currency, &responsible, &won, &lost) != nil {
			return nil, false
		}
		return gin.H{"company": company, "contact": contact, "phone": phone, "email": email,
			"source": source, "stage": st, "amount": value, "currency": currency,
			"responsible": responsible, "won": won, "lost": lost}, true
	})
}

func toolCRMSummary(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	var open, wonMonth, lostMonth int
	var openValue, wonValueMonth, conversion float64
	err := h.db.QueryRow(`
		SELECT COUNT(*) FILTER (WHERE won_at IS NULL AND lost_at IS NULL),
		       COALESCE(SUM(expected_value) FILTER (WHERE won_at IS NULL AND lost_at IS NULL), 0),
		       COUNT(*) FILTER (WHERE won_at >= date_trunc('month', CURRENT_DATE)),
		       COALESCE(SUM(expected_value) FILTER (WHERE won_at >= date_trunc('month', CURRENT_DATE)), 0),
		       COUNT(*) FILTER (WHERE lost_at >= date_trunc('month', CURRENT_DATE)),
		       COALESCE(COUNT(*) FILTER (WHERE won_at IS NOT NULL)::float /
		                NULLIF(COUNT(*) FILTER (WHERE won_at IS NOT NULL OR lost_at IS NOT NULL), 0) * 100, 0)
		FROM leads
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)
	`, tenantID, orgArg).Scan(&open, &openValue, &wonMonth, &wonValueMonth, &lostMonth, &conversion)
	if err != nil {
		return nil, err
	}
	reasons := []gin.H{}
	rows, err := h.db.Query(`
		SELECT r.name, COUNT(*)
		FROM leads l JOIN lost_reasons r ON r.id = l.lost_reason_id
		WHERE l.tenant_id=$1 AND l.deleted_at IS NULL AND l.lost_at IS NOT NULL
		  AND ($2::uuid IS NULL OR l.organization_id=$2)
		GROUP BY r.name ORDER BY COUNT(*) DESC LIMIT 5
	`, tenantID, orgArg)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var cnt int
			if rows.Scan(&name, &cnt) == nil {
				reasons = append(reasons, gin.H{"reason": name, "count": cnt})
			}
		}
	}
	return gin.H{
		"open_leads": open, "open_value": openValue,
		"won_this_month": wonMonth, "won_value_this_month": wonValueMonth,
		"lost_this_month": lostMonth, "conversion_percent": conversion,
		"top_loss_reasons": reasons,
	}, nil
}

func toolCreateLead(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	contactName := argStr(args, "contact_name")
	if contactName == "" {
		return nil, fmt.Errorf("contact_name is required")
	}
	source := argStr(args, "source")
	if source == "" {
		source = "other"
	}
	amount := 0.0
	if v, ok := args["amount"].(float64); ok {
		amount = v
	}
	// first open stage of the org's default pipeline
	var stageID, pipelineID interface{}
	var sid, pid uuid.UUID
	if err := h.db.QueryRow(`
		SELECT ps.id, p.id
		FROM pipeline_stages ps
		JOIN pipelines p ON p.id = ps.pipeline_id AND p.is_default
		WHERE ps.tenant_id = $1 AND ps.pipeline_type = 'lead' AND ps.is_active
		  AND NOT ps.is_won AND NOT ps.is_lost
		  AND ($2::uuid IS NULL OR p.organization_id = $2 OR p.organization_id IS NULL)
		ORDER BY ps.sequence LIMIT 1
	`, tenantID, orgArg).Scan(&sid, &pid); err == nil {
		stageID, pipelineID = sid, pid
	}
	id := uuid.New()
	var amountArg interface{}
	if amount > 0 {
		amountArg = amount
	}
	if _, err := h.db.Exec(`
		INSERT INTO leads (id, tenant_id, organization_id, contact_name, company_name, email, phone,
			status, source, notes, expected_value, currency, pipeline_id, stage_id,
			assigned_to, last_activity_at, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'new',$8,$9,$10,'UZS',$11,$12,$13,NOW(),$13,NOW(),NOW())`,
		id, tenantID, orgArg, contactName, nullIfEmpty(argStr(args, "company_name")),
		argStr(args, "email"), nullIfEmpty(argStr(args, "phone")),
		source, nullIfEmpty(argStr(args, "notes")), amountArg, pipelineID, stageID, userID); err != nil {
		return nil, err
	}
	if sid != uuid.Nil {
		h.recordLeadStageChange(h.db, tenantID, id, nil, &sid, userID)
	}
	h.EmitWorkflowEvent(tenantID, "lead.created", map[string]interface{}{
		"record_id":    id.String(),
		"contact_name": contactName,
		"company_name": argStr(args, "company_name"),
		"source":       source,
	})
	return gin.H{"id": id.String(), "contact_name": contactName, "created": true}, nil
}

func toolListOpportunities(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT COALESCE(o.name,''), COALESCE(v.name,''), COALESCE(o.stage,''),
		       COALESCE(o.expected_revenue,0)::float8, COALESCE(o.probability,0)::float8, o.expected_close_date
		FROM opportunities o LEFT JOIN contacts v ON v.id=o.contact_id
		WHERE o.tenant_id=$1 AND o.deleted_at IS NULL AND ($2::uuid IS NULL OR o.organization_id=$2)
		ORDER BY o.expected_close_date ASC NULLS LAST LIMIT %d`, limit), tenantID, orgArg)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var name, cust, stage string
		var revenue, prob float64
		var closeDate interface{}
		if r.Scan(&name, &cust, &stage, &revenue, &prob, &closeDate) != nil {
			return nil, false
		}
		return gin.H{"name": name, "customer": cust, "stage": stage, "expected_revenue": revenue,
			"probability": prob, "expected_close_date": closeDate}, true
	})
}

func toolListJournalEntries(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT je.entry_number, je.entry_date, COALESCE(je.description,''), COALESCE(je.reference,''),
	               COALESCE(je.total_debit,0), COALESCE(je.total_credit,0), COALESCE(je.status,'')
	        FROM journal_entries je
	        WHERE je.tenant_id=$1 AND je.deleted_at IS NULL AND ($2::uuid IS NULL OR je.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND je.status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY je.entry_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, desc, ref, st string
		var debit, credit float64
		var dt interface{}
		if r.Scan(&num, &dt, &desc, &ref, &debit, &credit, &st) != nil {
			return nil, false
		}
		return gin.H{"entry_number": num, "date": dt, "description": desc, "reference": ref,
			"total_debit": debit, "total_credit": credit, "status": st}, true
	})
}

// periodLowerBound maps a period keyword to a SQL lower bound on a date column.
func periodLowerBound(period string) string {
	switch period {
	case "this_year":
		return "date_trunc('year', CURRENT_DATE)"
	case "this_quarter":
		return "date_trunc('quarter', CURRENT_DATE)"
	case "last_30_days":
		return "CURRENT_DATE - INTERVAL '30 days'"
	default: // this_month
		return "date_trunc('month', CURRENT_DATE)"
	}
}

func toolTaxSummary(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	lower := periodLowerBound(argStr(args, "period"))
	f := func(table string) float64 {
		var v float64
		_ = h.db.QueryRow(fmt.Sprintf(`SELECT COALESCE(SUM(tax_amount),0) FROM %s
			WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)
			  AND invoice_date >= %s`, table, lower), tenantID, orgArg).Scan(&v)
		return v
	}
	output := f("sales_invoices")   // VAT collected on sales
	input := f("purchase_invoices") // VAT paid on purchases
	period := argStr(args, "period")
	if period == "" {
		period = "this_month"
	}
	return gin.H{
		"period": period, "output_vat_on_sales": output, "input_vat_on_purchases": input,
		"net_vat_payable": output - input,
		"note":            "VAT/NDS only, from invoice tax_amount (so'm). Excludes payroll/profit/turnover taxes.",
	}, nil
}

func toolListLeaveRequests(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	// leave_requests is tenant-scoped (no organization_id).
	qry := `SELECT COALESCE(employee_name,''), COALESCE(leave_type,''), start_date, end_date,
	               COALESCE(days,0)::float8, COALESCE(status,''), COALESCE(reason,'')
	        FROM leave_requests WHERE tenant_id=$1 AND deleted_at IS NULL`
	qa := []interface{}{tenantID}
	if status != "" {
		qry += " AND status=$2"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY start_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var emp, ltype, st, reason string
		var days float64
		var start, end interface{}
		if r.Scan(&emp, &ltype, &start, &end, &days, &st, &reason) != nil {
			return nil, false
		}
		return gin.H{"employee": emp, "leave_type": ltype, "start_date": start, "end_date": end,
			"days": days, "status": st, "reason": reason}, true
	})
}

func toolListAttendance(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 15, 50)
	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT COALESCE(employee_name,''), date, clock_in, clock_out, COALESCE(status,''), COALESCE(department,'')
		FROM attendance_records WHERE tenant_id=$1 AND deleted_at IS NULL
		ORDER BY date DESC NULLS LAST, employee_name ASC LIMIT %d`, limit), tenantID)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var emp, st, dept string
		var date, in, out interface{}
		if r.Scan(&emp, &date, &in, &out, &st, &dept) != nil {
			return nil, false
		}
		return gin.H{"employee": emp, "date": date, "clock_in": in, "clock_out": out, "status": st, "department": dept}, true
	})
}

func toolListPayrollPeriods(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT COALESCE(period_code,''), COALESCE(period_name,''), start_date, end_date, pay_date,
		       COALESCE(status,''), COALESCE(employee_count,0)::int, COALESCE(total_gross,0)::float8, COALESCE(total_net,0)::float8
		FROM payroll_periods WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)
		ORDER BY start_date DESC NULLS LAST LIMIT %d`, limit), tenantID, orgArg)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var code, name, st string
		var start, end, pay interface{}
		var cnt int
		var gross, net float64
		if r.Scan(&code, &name, &start, &end, &pay, &st, &cnt, &gross, &net) != nil {
			return nil, false
		}
		return gin.H{"period_code": code, "period_name": name, "start_date": start, "end_date": end,
			"pay_date": pay, "status": st, "employee_count": cnt, "total_gross": gross, "total_net": net}, true
	})
}

// ---- operations read execs (manufacturing, inventory ops, procurement) ------

func toolListBOMs(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 15, 50)
	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT COALESCE(b.code,''), COALESCE(b.name,''), COALESCE(p.name,''), COALESCE(b.version::text,''),
		       COALESCE(b.bom_type,''), COALESCE(b.quantity,0), COALESCE(b.is_default,false), COALESCE(b.is_active,true)
		FROM product_boms b LEFT JOIN products p ON p.id=b.product_id
		WHERE b.tenant_id=$1 AND b.deleted_at IS NULL AND ($2::uuid IS NULL OR b.organization_id=$2)
		ORDER BY b.created_at DESC NULLS LAST LIMIT %d`, limit), tenantID, orgArg)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var code, name, prod, ver, btype string
		var qty float64
		var isDefault, isActive bool
		if r.Scan(&code, &name, &prod, &ver, &btype, &qty, &isDefault, &isActive) != nil {
			return nil, false
		}
		return gin.H{"code": code, "name": name, "product": prod, "version": ver, "type": btype,
			"output_quantity": qty, "is_default": isDefault, "is_active": isActive}, true
	})
}

func toolGetBOM(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	q := argStr(args, "bom")
	if q == "" {
		return nil, fmt.Errorf("bom (code, name or product) is required")
	}
	var bomID uuid.UUID
	var code, name, prod string
	var qty float64
	err := h.db.QueryRow(`SELECT b.id, COALESCE(b.code,''), COALESCE(b.name,''), COALESCE(p.name,''), COALESCE(b.quantity,0)
		FROM product_boms b LEFT JOIN products p ON p.id=b.product_id
		WHERE b.tenant_id=$1 AND b.deleted_at IS NULL AND ($3::uuid IS NULL OR b.organization_id=$3)
		  AND (b.code ILIKE '%'||$2||'%' OR b.name ILIKE '%'||$2||'%' OR p.name ILIKE '%'||$2||'%')
		ORDER BY b.is_default DESC NULLS LAST LIMIT 1`, tenantID, q, orgArg).Scan(&bomID, &code, &name, &prod, &qty)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no BOM found matching %q", q)
	}
	if err != nil {
		return nil, err
	}
	rows, err := h.db.Query(`SELECT COALESCE(cp.name,''), COALESCE(l.quantity,0),
		COALESCE(l.unit_of_measure,''), COALESCE(l.scrap_percent,0)
		FROM bom_lines l LEFT JOIN products cp ON cp.id=l.component_id
		WHERE l.bom_id=$1 ORDER BY l.line_number ASC NULLS LAST`, bomID)
	comps, lerr := rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var cname, uom string
		var cqty, scrap float64
		if r.Scan(&cname, &cqty, &uom, &scrap) != nil {
			return nil, false
		}
		return gin.H{"component": cname, "quantity": cqty, "unit": uom, "scrap_percent": scrap}, true
	})
	if lerr != nil {
		return nil, lerr
	}
	return gin.H{"code": code, "name": name, "product": prod, "output_quantity": qty, "components": comps}, nil
}

func toolListWorkOrders(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT COALESCE(w.work_order_number,''), COALESCE(w.name,''), COALESCE(w.work_center_name,''),
	               COALESCE(w.status,''), COALESCE(w.quantity_to_produce,0), COALESCE(w.quantity_produced,0),
	               COALESCE(w.progress_percent,0)::float8, w.scheduled_start
	        FROM work_orders w WHERE w.tenant_id=$1 AND w.deleted_at IS NULL AND ($2::uuid IS NULL OR w.organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND w.status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY w.created_at DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, name, wc, st string
		var toProduce, produced, progress float64
		var sched interface{}
		if r.Scan(&num, &name, &wc, &st, &toProduce, &produced, &progress, &sched) != nil {
			return nil, false
		}
		return gin.H{"work_order_number": num, "name": name, "work_center": wc, "status": st,
			"quantity_to_produce": toProduce, "quantity_produced": produced, "progress_percent": progress, "scheduled_start": sched}, true
	})
}

func toolListStockCounts(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	// stock_counts has no deleted_at column.
	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT COALESCE(sc.count_number,''), sc.count_date, COALESCE(w.name,''), COALESCE(sc.count_type,''),
		       COALESCE(sc.status,''), COALESCE(sc.total_variance_value,0)
		FROM stock_counts sc LEFT JOIN warehouses w ON w.id=sc.warehouse_id
		WHERE sc.tenant_id=$1 AND ($2::uuid IS NULL OR sc.organization_id=$2)
		ORDER BY sc.count_date DESC NULLS LAST LIMIT %d`, limit), tenantID, orgArg)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, wh, ctype, st string
		var variance float64
		var dt interface{}
		if r.Scan(&num, &dt, &wh, &ctype, &st, &variance) != nil {
			return nil, false
		}
		return gin.H{"count_number": num, "date": dt, "warehouse": wh, "type": ctype, "status": st, "variance_value": variance}, true
	})
}

func toolListGoodsReceipts(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT COALESCE(gr_number,''), COALESCE(po_number,''), COALESCE(supplier_name,''), COALESCE(warehouse_name,''),
	               COALESCE(status,''), COALESCE(quality_status,''), COALESCE(total_quantity,0), receipt_date
	        FROM goods_receipts WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY receipt_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, po, sup, wh, st, qual string
		var qty float64
		var dt interface{}
		if r.Scan(&num, &po, &sup, &wh, &st, &qual, &qty, &dt) != nil {
			return nil, false
		}
		return gin.H{"gr_number": num, "po_number": po, "supplier": sup, "warehouse": wh,
			"status": st, "quality_status": qual, "quantity": qty, "date": dt}, true
	})
}

func toolListPurchaseRequisitions(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT COALESCE(pr_number,''), request_date, required_date, COALESCE(department,''),
	               COALESCE(priority,''), COALESCE(status,''), COALESCE(total_amount,0), COALESCE(purpose,'')
	        FROM purchase_requisitions WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY request_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, dept, prio, st, purpose string
		var total float64
		var req, need interface{}
		if r.Scan(&num, &req, &need, &dept, &prio, &st, &total, &purpose) != nil {
			return nil, false
		}
		return gin.H{"pr_number": num, "request_date": req, "required_date": need, "department": dept,
			"priority": prio, "status": st, "total": total, "purpose": purpose}, true
	})
}

func toolListRFQs(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT COALESCE(rfq_number,''), COALESCE(title,''), COALESCE(status,''), deadline
	        FROM rfqs WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY created_at DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, title, st string
		var deadline interface{}
		if r.Scan(&num, &title, &st, &deadline) != nil {
			return nil, false
		}
		return gin.H{"rfq_number": num, "title": title, "status": st, "deadline": deadline}, true
	})
}

func toolListSalesReturns(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT COALESCE(return_number,''), COALESCE(customer_name,''), return_date, COALESCE(status,''),
	               COALESCE(refund_status,''), COALESCE(total_amount,0), COALESCE(reason,'')
	        FROM sales_returns WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY return_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, cust, st, refund, reason string
		var total float64
		var dt interface{}
		if r.Scan(&num, &cust, &dt, &st, &refund, &total, &reason) != nil {
			return nil, false
		}
		return gin.H{"return_number": num, "customer": cust, "date": dt, "status": st,
			"refund_status": refund, "total": total, "reason": reason}, true
	})
}

func toolListPurchaseReturns(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	limit := argInt(args, "limit", 10, 50)
	status := argStr(args, "status")
	qry := `SELECT COALESCE(return_number,''), COALESCE(supplier_name,''), return_date, COALESCE(status,''),
	               COALESCE(total_value,0), COALESCE(return_reason,'')
	        FROM purchase_returns WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2)`
	qa := []interface{}{tenantID, orgArg}
	if status != "" {
		qry += " AND status=$3"
		qa = append(qa, status)
	}
	qry += fmt.Sprintf(" ORDER BY return_date DESC NULLS LAST LIMIT %d", limit)
	rows, err := h.db.Query(qry, qa...)
	return rowsToList(rows, err, func(r *sql.Rows) (gin.H, bool) {
		var num, sup, st, reason string
		var total float64
		var dt interface{}
		if r.Scan(&num, &sup, &dt, &st, &total, &reason) != nil {
			return nil, false
		}
		return gin.H{"return_number": num, "supplier": sup, "date": dt, "status": st, "credit_value": total, "reason": reason}, true
	})
}

// ── Manufacturing tools (Ishlab chiqarish v2, integration map §6 AI) ────

// toolManufacturingStats — the KPI totals family from GetManufacturingStats
// (manufacturing_stats.go), current month, read-only.
func toolManufacturingStats(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	nowT := time.Now()
	monthStart := time.Date(nowT.Year(), nowT.Month(), 1, 0, 0, 0, 0, nowT.Location())

	var totalOrders, draftOrders, confirmedOrders, inProgressOrders, activeOrders int
	var completedMonth, overdueOrders int
	var qtyProduced, qtyScrapped float64
	err := h.db.QueryRow(`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'draft'),
			COUNT(*) FILTER (WHERE status = 'confirmed'),
			COUNT(*) FILTER (WHERE status = 'in_progress'),
			COUNT(*) FILTER (WHERE status IN ('in_progress', 'paused', 'packaging')),
			COUNT(*) FILTER (WHERE status = 'completed' AND actual_end >= $3),
			COUNT(*) FILTER (WHERE status IN ('draft', 'confirmed', 'ready', 'in_progress', 'paused', 'packaging')
			                 AND scheduled_end IS NOT NULL AND scheduled_end < NOW()),
			COALESCE(SUM(quantity_produced) FILTER (WHERE status = 'completed' AND actual_end >= $3), 0),
			COALESCE(SUM(quantity_scrapped) FILTER (WHERE status = 'completed' AND actual_end >= $3), 0)
		FROM production_orders
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND ($2::uuid IS NULL OR organization_id = $2)
	`, tenantID, orgArg, monthStart).Scan(
		&totalOrders, &draftOrders, &confirmedOrders, &inProgressOrders, &activeOrders,
		&completedMonth, &overdueOrders, &qtyProduced, &qtyScrapped)
	if err != nil {
		return nil, err
	}
	scrapRate := 0.0
	if qtyProduced+qtyScrapped > 0 {
		scrapRate = qtyScrapped / (qtyProduced + qtyScrapped) * 100
	}
	return gin.H{
		"total_orders":       totalOrders,
		"draft":              draftOrders,
		"confirmed":          confirmedOrders,
		"in_progress":        inProgressOrders,
		"active":             activeOrders,
		"completed_month":    completedMonth,
		"overdue":            overdueOrders,
		"produced_month":     qtyProduced,
		"scrapped_month":     qtyScrapped,
		"scrap_rate_percent": scrapRate,
	}, nil
}

// toolProductionShortages — the MRP-lite shortages query (same SQL as the
// stats endpoint's shortages block): confirmed/in-progress MO BOM demand
// minus on-hand minus open purchase quantity.
func toolProductionShortages(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	rows, err := h.db.Query(`
		WITH need AS (
			SELECT bl.component_id AS product_id,
			       SUM(bl.quantity
			           * GREATEST(po.quantity_planned - COALESCE(po.quantity_produced, 0), 0)
			           / GREATEST(COALESCE(pb.quantity, 1), 0.0001)
			           * (1 + COALESCE(bl.scrap_percent, 0) / 100.0)) AS required
			FROM production_orders po
			JOIN product_boms pb ON pb.id = po.bom_id
			JOIN bom_lines bl ON bl.bom_id = pb.id
			JOIN products cp ON cp.id = bl.component_id AND COALESCE(cp.track_inventory, true)
			WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
			  AND po.status IN ('confirmed', 'in_progress')
			  AND ($2::uuid IS NULL OR po.organization_id = $2)
			GROUP BY bl.component_id
		)
		SELECT COALESCE(p.name, ''), n.required,
		       COALESCE(oh.qty, 0) AS on_hand, COALESCE(oo.qty, 0) AS on_order,
		       n.required - COALESCE(oh.qty, 0) - COALESCE(oo.qty, 0) AS missing
		FROM need n
		JOIN products p ON p.id = n.product_id
		LEFT JOIN LATERAL (
			SELECT SUM(i.quantity_on_hand) AS qty
			FROM inventory i
			JOIN warehouses w ON w.id = i.warehouse_id
			WHERE i.tenant_id = $1 AND i.product_id = n.product_id
			  AND w.deleted_at IS NULL
			  AND ($2::uuid IS NULL OR w.organization_id = $2)
		) oh ON true
		LEFT JOIN LATERAL (
			SELECT SUM(pol.quantity - COALESCE(pol.quantity_received, 0)) AS qty
			FROM purchase_order_lines pol
			JOIN purchase_orders p2 ON p2.id = pol.purchase_order_id
			WHERE p2.tenant_id = $1 AND p2.deleted_at IS NULL
			  AND p2.status IN ('approved', 'ordered', 'partial')
			  AND pol.product_id = n.product_id
			  AND ($2::uuid IS NULL OR p2.organization_id = $2)
		) oo ON true
		WHERE n.required - COALESCE(oh.qty, 0) - COALESCE(oo.qty, 0) > 0.0001
		ORDER BY missing DESC
		LIMIT 15
	`, tenantID, orgArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var name string
		var required, onHand, onOrder, missing float64
		if rows.Scan(&name, &required, &onHand, &onOrder, &missing) == nil {
			out = append(out, gin.H{
				"product": name, "required": required,
				"on_hand": onHand, "on_order": onOrder, "missing": missing,
			})
		}
	}
	return out, nil
}

// toolConstructionStats mirrors GET /construction/projects/stats in compact
// form for the AI agent (docs/construction-roadmap.md S7 phase 1): status
// counts, contract vs approved actual, overdue count, and the projects whose
// approved object costs are closest to / over their contract amount, each
// with the cost-weighted readiness the portfolio cards show.
func toolConstructionStats(h *Handler, c *gin.Context, tenantID uuid.UUID, orgArg interface{}, userID uuid.UUID, args map[string]interface{}) (interface{}, error) {
	byStatus := map[string]int{}
	total := 0
	var contractTotal float64
	overdue := 0
	rows, err := h.db.Query(`
		SELECT COALESCE(status, 'draft'), COUNT(*), COALESCE(SUM(contract_amount), 0),
		       COUNT(*) FILTER (WHERE planned_end_date IS NOT NULL AND planned_end_date < CURRENT_DATE
		                        AND COALESCE(status, '') NOT IN ('completed', 'cancelled'))
		FROM construction_projects
		WHERE tenant_id = $1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id = $2)
		GROUP BY 1`, tenantID, orgArg)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status string
		var cnt, od int
		var sum float64
		if rows.Scan(&status, &cnt, &sum, &od) == nil {
			byStatus[status] = cnt
			total += cnt
			contractTotal += sum
			overdue += od
		}
	}
	rows.Close()

	var actualTotal float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(cel.amount), 0)
		FROM construction_expense_lines cel
		JOIN construction_projects p ON p.id = cel.project_id
		WHERE cel.tenant_id = $1 AND cel.status = 'approved' AND cel.deleted_at IS NULL
		  AND p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND ($2::uuid IS NULL OR p.organization_id = $2)`, tenantID, orgArg).Scan(&actualTotal)

	// Projects ranked by budget pressure (actual/contract), with readiness.
	top := []gin.H{}
	if prows, err := h.db.Query(`
		WITH spend AS (
			SELECT cel.project_id, SUM(cel.amount) AS actual
			FROM construction_expense_lines cel
			WHERE cel.tenant_id = $1 AND cel.status = 'approved' AND cel.deleted_at IS NULL
			GROUP BY cel.project_id
		), readiness AS (
			SELECT e.project_id,
			       CASE WHEN SUM(COALESCE(el.total_amount, 0)) > 0
			            THEN SUM(COALESCE(el.total_amount, 0) * LEAST(COALESCE(el.done_quantity, 0) / NULLIF(
			                 CASE WHEN COALESCE(el.imported_quantity, 0) > 0 THEN el.imported_quantity
			                      WHEN COALESCE(el.original_quantity, 0) > 0 THEN el.original_quantity
			                      ELSE COALESCE(el.quantity, 0) END, 0), 1))
			                 / SUM(COALESCE(el.total_amount, 0)) * 100
			            ELSE 0 END AS pct
			FROM construction_estimate_line el
			JOIN construction_estimate e ON e.id = el.estimate_id AND e.tenant_id = el.tenant_id
			WHERE el.tenant_id = $1 AND LOWER(COALESCE(e.source_type, '')) = 'edinich'
			  AND COALESCE(el.resource_type, '') = '' AND COALESCE(el.parent_line_id, 0) = 0
			GROUP BY e.project_id
		)
		SELECT p.name, COALESCE(p.status, ''), COALESCE(p.contract_amount, 0),
		       COALESCE(s.actual, 0), COALESCE(r.pct, 0)
		FROM construction_projects p
		LEFT JOIN spend s ON s.project_id = p.id
		LEFT JOIN readiness r ON r.project_id = p.id
		WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
		  AND ($2::uuid IS NULL OR p.organization_id = $2)
		  AND COALESCE(s.actual, 0) > 0
		ORDER BY CASE WHEN COALESCE(p.contract_amount, 0) > 0
		              THEN COALESCE(s.actual, 0) / p.contract_amount ELSE 0 END DESC
		LIMIT 8`, tenantID, orgArg); err == nil {
		for prows.Next() {
			var name, status string
			var budget, actual, pct float64
			if prows.Scan(&name, &status, &budget, &actual, &pct) == nil {
				row := gin.H{"name": name, "status": status, "contract_amount": budget,
					"actual_spend": actual, "readiness_pct": pct}
				if budget > 0 {
					row["budget_used_pct"] = actual / budget * 100
				}
				top = append(top, row)
			}
		}
		prows.Close()
	}

	return gin.H{
		"total_projects": total, "by_status": byStatus,
		"contract_total": contractTotal, "actual_total": actualTotal,
		"overdue_projects": overdue, "top_budget_pressure": top,
	}, nil
}
