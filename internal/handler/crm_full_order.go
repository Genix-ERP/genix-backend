package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// crm_full_order.go — "create customer + sales order + manufacture order" in one
// save, used by the CRM Add-customer modal.
//
// Pricing: material_cost = BOM cost rollup(product, qty) + Σ(extra components),
// total = material_cost * (1 + profit%/100). The customer may pay part upfront;
// remaining = total - paid (tracked on the sales order via paid_amount).
//
// Phase 1 (this file): customer + sales order(+line, paid/remaining) + production
// order(+work orders from BOM) + extra components attached to the MO. The upfront
// payment is recorded as paid_amount/payment_status here; the posted cash-receipt
// journal entry (DR Cash / CR AR) is layered on in Phase 2.

type fullOrderComponent struct {
	ProductID string  `json:"product_id"` // optional; required to attach to the MO
	Name      string  `json:"name"`
	Quantity  float64 `json:"quantity"`
	Unit      string  `json:"unit"`
	UnitPrice float64 `json:"unit_price"`
}

type FullOrderInput struct {
	// Customer (new)
	Customer struct {
		Name        string `json:"name" binding:"required"` // Kompaniya nomi
		ContactName string `json:"contact_name"`            // Aloqa shaxsi
		Email       string `json:"email"`
		Phone       string `json:"phone"`
		Address     string `json:"address"`
		City        string `json:"city"`
		Country     string `json:"country"`
		Notes       string `json:"notes"`
	} `json:"customer"`

	// Order / calculator
	ProductID     string               `json:"product_id" binding:"required"`
	Quantity      float64              `json:"quantity"`       // default 1
	ProfitPercent float64              `json:"profit_percent"` // default 45
	PaidAmount    float64              `json:"paid_amount"`
	Components    []fullOrderComponent `json:"components"`
	Notes         string               `json:"notes"`
}

// CreateCustomerWithFullOrder — POST /contacts/full-order
func (h *Handler) CreateCustomerWithFullOrder(c *gin.Context) {
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

	var in FullOrderInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}
	productID, err := uuid.Parse(in.ProductID)
	if err != nil {
		response.BadRequest(c, "Invalid product_id")
		return
	}
	qty := in.Quantity
	if qty <= 0 {
		qty = 1
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// ---- 1. Customer (contact, type=customer) ----
	customerID := uuid.New()
	now := time.Now()
	billing, _ := json.Marshal(map[string]string{
		"street1": in.Customer.Address, "city": in.Customer.City, "country": in.Customer.Country,
	})
	custCode := fmt.Sprintf("CUST-%d", now.UnixNano()/1e6)
	if _, err := tx.Exec(`
		INSERT INTO contacts (id, tenant_id, organization_id, type, code, name, email, phone,
		                      billing_address, current_balance, notes, is_active, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,'customer',$4,$5,$6,$7,$8,0,$9,true,$10,$11,$11)`,
		customerID, tenantID, orgArg, custCode, in.Customer.Name,
		nullIfEmpty(in.Customer.Email), nullIfEmpty(in.Customer.Phone),
		billing, nullIfEmpty(in.Customer.Notes), userID, now,
	); err != nil {
		h.log.Error("full-order: create customer", "error", err)
		response.InternalError(c, "Failed to create customer: "+err.Error())
		return
	}

	// ---- 2. Resolve product's default BOM + cost rollup ----
	var bomID sql.NullString
	_ = h.db.QueryRow(`
		SELECT id::text FROM product_boms
		WHERE product_id=$1 AND tenant_id=$2 AND is_active=true AND deleted_at IS NULL
		ORDER BY is_default DESC, created_at DESC LIMIT 1`, productID, tenantID).Scan(&bomID)

	var bomCost float64
	if bomID.Valid {
		_ = h.db.QueryRow(`
			SELECT COALESCE(SUM(bl.quantity * COALESCE(p.cost_price,0) * $1
			       / GREATEST(COALESCE(pb.quantity,1),1) * (1 + COALESCE(bl.scrap_percent,0)/100.0)),0)
			FROM bom_lines bl
			JOIN products p ON p.id = bl.component_id
			JOIN product_boms pb ON pb.id = bl.bom_id
			WHERE bl.bom_id = $2`, qty, bomID.String).Scan(&bomCost)
	}

	// extra components cost (Σ qty*unit_price)
	var extraCost float64
	for _, comp := range in.Components {
		extraCost += comp.Quantity * comp.UnitPrice
	}

	materialCost := bomCost + extraCost
	total := materialCost * (1 + in.ProfitPercent/100.0)
	paid := in.PaidAmount
	if paid > total {
		paid = total
	}
	paymentStatus := "unpaid"
	if paid >= total && total > 0 {
		paymentStatus = "paid"
	} else if paid > 0 {
		paymentStatus = "partial"
	}

	// ---- 3. Sales order (+1 line for the product) ----
	salesOrderID := uuid.New()
	orderNumber := h.nextSalesOrderNumber(tenantID, orgID)
	unitPrice := 0.0
	if qty > 0 {
		unitPrice = total / qty
	}
	if _, err := tx.Exec(`
		INSERT INTO sales_orders (id, tenant_id, organization_id, order_number, customer_id, order_date,
		                          subtotal, total_amount, paid_amount, status, payment_status, notes,
		                          created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,CURRENT_DATE,$6,$6,$7,'confirmed',$8,$9,$10,$11,$11)`,
		salesOrderID, tenantID, orgArg, orderNumber, customerID,
		total, paid, paymentStatus, nullIfEmpty(in.Notes), userID, now,
	); err != nil {
		h.log.Error("full-order: create sales order", "error", err)
		response.InternalError(c, "Failed to create sales order: "+err.Error())
		return
	}
	if _, err := tx.Exec(`
		INSERT INTO sales_order_lines (id, sales_order_id, line_number, product_id, quantity, unit_price, line_total, created_at, updated_at)
		VALUES ($1,$2,1,$3,$4,$5,$6,$7,$7)`,
		uuid.New(), salesOrderID, productID, qty, unitPrice, total, now,
	); err != nil {
		h.log.Error("full-order: create sales order line", "error", err)
		response.InternalError(c, "Failed to create sales order line: "+err.Error())
		return
	}

	// ---- 4. Production / manufacture order ----
	productionOrderID := uuid.New()
	moCode := h.nextProductionOrderCode(tenantID)
	var prodUOM string
	// products has no `uom` column — the unit lives in units_of_measure via unit_id.
	_ = h.db.QueryRow(`SELECT COALESCE(u.name, 'pcs') FROM products p
		LEFT JOIN units_of_measure u ON u.id = p.unit_id
		WHERE p.id=$1 AND p.tenant_id=$2`, productID, tenantID).Scan(&prodUOM)
	if prodUOM == "" {
		prodUOM = "pcs"
	}
	var bomArg interface{}
	if bomID.Valid {
		bomArg = bomID.String
	}
	if _, err := tx.Exec(`
		INSERT INTO production_orders (id, tenant_id, organization_id, code, name, product_id, bom_id,
		                               quantity_planned, uom, status, current_stage, priority,
		                               source_type, sales_order_id, customer_id, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'draft','draft',5,'sales_order',$10,$11,$12,$13,$13)`,
		productionOrderID, tenantID, orgArg, moCode, nullIfEmpty(in.Customer.Name+" order"),
		productID, bomArg, qty, prodUOM, salesOrderID, customerID, userID, now,
	); err != nil {
		h.log.Error("full-order: create production order", "error", err)
		response.InternalError(c, "Failed to create manufacture order: "+err.Error())
		return
	}

	// Work orders from BOM (operations + planned materials). Uses the existing
	// helper; runs on its own connection so commit ordering matters — we create
	// the work orders AFTER the production order row is visible, so do it post-commit
	// is unsafe; instead call it within this tx is not possible (helper uses h.db).
	// We therefore defer work-order generation + extra-component attach to AFTER commit.

	// ---- 4b. Upfront payment → posted cash-receipt (DR Cash / CR AR) ----
	glPosted := false
	if paid > 0 {
		glPosted = h.postUpfrontReceipt(tx, tenantID, orgID, orgArg, customerID, salesOrderID, orderNumber, paid, userID, now)
	}

	// ---- 5. Commit, then generate work orders + attach extras ----
	if err := tx.Commit(); err != nil {
		h.log.Error("full-order: commit", "error", err)
		response.InternalError(c, "Failed to save: "+err.Error())
		return
	}
	committed = true

	// Generate work orders from the BOM (separate connection; the PO is committed).
	if bomID.Valid {
		bomUUID, _ := uuid.Parse(bomID.String)
		if woErr := h.CreateWorkOrdersFromBOM(productionOrderID, bomUUID, tenantID, orgID, qty, userID); woErr != nil {
			h.log.Error("full-order: work orders from BOM", "error", woErr, "po", productionOrderID)
		}
	}
	// Attach extra components to the MO (shop-floor extras).
	h.attachExtraComponents(tenantID, productionOrderID, in.Components, userID)

	response.Success(c, gin.H{
		"customer_id":         customerID,
		"sales_order_id":      salesOrderID,
		"order_number":        orderNumber,
		"production_order_id": productionOrderID,
		"mo_code":             moCode,
		"material_cost":       materialCost,
		"total":               total,
		"paid":                paid,
		"remaining":           total - paid,
		"gl_posted":           glPosted,
	})
}

// postUpfrontReceipt posts a cash-receipt journal entry (DR Cash / CR Accounts
// Receivable) for the upfront amount, inside the given tx, mirroring
// ConfirmPayment's receipt logic. Best-effort: if the cash/AR account or a
// journal can't be resolved it logs and returns false — the order still
// succeeds, just without a GL entry (so a half-configured chart of accounts
// can't block creating the order).
func (h *Handler) postUpfrontReceipt(tx *sql.Tx, tenantID, orgID uuid.UUID, orgArg interface{},
	customerID, salesOrderID uuid.UUID, orderNumber string, amount float64, userID uuid.UUID, now time.Time) bool {
	if amount <= 0 {
		return false
	}
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}
	// Cash/bank account.
	cashAccountID := findAccount(tx, tenantID, orgPtr, "kassa", "5010")
	if cashAccountID == uuid.Nil {
		cashAccountID = findAccount(tx, tenantID, orgPtr, "bank", "5110")
	}
	// AR account: prefer the customer's default, else by name/code.
	var arStr sql.NullString
	_ = tx.QueryRow(`SELECT default_receivable_account_id::text FROM contacts WHERE id=$1 AND tenant_id=$2`, customerID, tenantID).Scan(&arStr)
	var arAccountID uuid.UUID
	if arStr.Valid && arStr.String != "" {
		arAccountID, _ = uuid.Parse(arStr.String)
	}
	if arAccountID == uuid.Nil {
		arAccountID = findAccount(tx, tenantID, orgPtr, "debitor", "4010")
	}
	if arAccountID == uuid.Nil {
		arAccountID = findAccount(tx, tenantID, orgPtr, "receivable", "4010")
	}
	if cashAccountID == uuid.Nil || arAccountID == uuid.Nil {
		h.log.Error("full-order: cannot resolve cash/AR account; GL not posted", "cash", cashAccountID, "ar", arAccountID)
		return false
	}

	// Journal: prefer a cash journal, then GENERAL, then any — scoped to org.
	var journalID uuid.UUID
	var nextNumber int
	var numberPrefix sql.NullString
	_ = tx.QueryRow(`
		SELECT id, COALESCE(next_number,1), number_prefix
		FROM journals
		WHERE tenant_id=$1 AND deleted_at IS NULL
		  AND ($2::uuid IS NULL OR organization_id = $2 OR organization_id IS NULL)
		ORDER BY CASE WHEN LOWER(COALESCE(type,''))='cash' THEN 0 WHEN code='GENERAL' THEN 1 ELSE 2 END,
		         CASE WHEN organization_id = $2 THEN 0 ELSE 1 END
		LIMIT 1`, tenantID, orgArg).Scan(&journalID, &nextNumber, &numberPrefix)
	if journalID == uuid.Nil {
		h.log.Error("full-order: no journal found; GL not posted")
		return false
	}

	prefix := ""
	if numberPrefix.Valid {
		prefix = numberPrefix.String
	}
	var maxNum int
	_ = tx.QueryRow(`SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(entry_number,'[^0-9]','','g') AS BIGINT)),0)
		FROM journal_entries WHERE tenant_id=$1 AND journal_id=$2 AND deleted_at IS NULL`, tenantID, journalID).Scan(&maxNum)
	actualNum := maxNum + 1
	if nextNumber > actualNum {
		actualNum = nextNumber
	}
	entryNumber := fmt.Sprintf("%s%06d", prefix, actualNum)
	desc := fmt.Sprintf("%s — oldindan to'lov (upfront)", orderNumber)

	jeID := uuid.New()
	if _, err := tx.Exec(`
		INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
		                             source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'payment_receipt',$9,1.0,$10,$10,'posted',$11,$12,$12)`,
		jeID, tenantID, orgArg, journalID, entryNumber, now, orderNumber, desc, salesOrderID.String(), amount, userID, now); err != nil {
		h.log.Error("full-order: journal entry insert failed; GL not posted", "error", err)
		return false
	}
	// DR Cash (no contact on the cash line).
	if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, contact_id, description, debit_amount, credit_amount, exchange_rate, created_at)
		VALUES ($1,$2,1,$3,NULL,$4,$5,0,1.0,$6)`, uuid.New(), jeID, cashAccountID, desc, amount, now); err != nil {
		h.log.Error("full-order: cash line insert failed", "error", err)
		return false
	}
	// CR Accounts Receivable (customer on the AR line).
	if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, line_number, account_id, contact_id, description, debit_amount, credit_amount, exchange_rate, created_at)
		VALUES ($1,$2,2,$3,$4,$5,0,$6,1.0,$7)`, uuid.New(), jeID, arAccountID, customerID, desc, amount, now); err != nil {
		h.log.Error("full-order: AR line insert failed", "error", err)
		return false
	}
	// Balances + journal counter + denormalized customer balance (prepaid → AR down).
	tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at=$2 WHERE id=$3`, amount, now, cashAccountID)
	tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at=$2 WHERE id=$3`, amount, now, arAccountID)
	tx.Exec(`UPDATE journals SET next_number = next_number + 1 WHERE id=$1`, journalID)
	tx.Exec(`UPDATE contacts SET current_balance = current_balance - $1, updated_at=$2 WHERE id=$3`, amount, now, customerID)
	return true
}

// attachExtraComponents adds the user-entered extra components to the MO. If a
// work order exists (created from the BOM) they go to work_order_materials
// (shop-floor extras); otherwise to production_material_consumption (planned).
// Components without a product_id are skipped (both tables require product_id).
func (h *Handler) attachExtraComponents(tenantID, productionOrderID uuid.UUID, comps []fullOrderComponent, userID uuid.UUID) {
	if len(comps) == 0 {
		return
	}
	var workOrderID sql.NullString
	_ = h.db.QueryRow(`SELECT id::text FROM work_orders WHERE production_order_id=$1 AND tenant_id=$2 ORDER BY created_at ASC LIMIT 1`,
		productionOrderID, tenantID).Scan(&workOrderID)

	for _, comp := range comps {
		if comp.ProductID == "" {
			continue // free-text material: counts toward cost only, can't be a stock movement
		}
		pid, err := uuid.Parse(comp.ProductID)
		if err != nil {
			continue
		}
		uom := comp.Unit
		if uom == "" {
			uom = "pcs"
		}
		if workOrderID.Valid {
			woUUID, _ := uuid.Parse(workOrderID.String)
			if _, err := h.db.Exec(`
				INSERT INTO work_order_materials (id, tenant_id, work_order_id, production_order_id, product_id,
				                                  product_name, quantity, uom, unit_cost, total_cost, created_by, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())`,
				uuid.New(), tenantID, woUUID, productionOrderID, pid, nullIfEmpty(comp.Name),
				comp.Quantity, uom, comp.UnitPrice, comp.Quantity*comp.UnitPrice, userID); err != nil {
				h.log.Error("full-order: attach work_order_material", "error", err)
			}
		} else {
			if _, err := h.db.Exec(`
				INSERT INTO production_material_consumption (id, tenant_id, production_order_id, product_id, quantity_planned, uom, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,NOW())`,
				uuid.New(), tenantID, productionOrderID, pid, comp.Quantity, uom); err != nil {
				h.log.Error("full-order: attach production_material_consumption", "error", err)
			}
		}
	}
}

// nextSalesOrderNumber generates S##### scoped by (tenant, org).
func (h *Handler) nextSalesOrderNumber(tenantID, orgID uuid.UUID) string {
	var maxNum int
	if orgID != uuid.Nil {
		h.db.QueryRow(`SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number,'[^0-9]','','g') AS BIGINT)),0)
			FROM sales_orders WHERE tenant_id=$1 AND organization_id=$2 AND order_number ~ '^S[0-9]+$'`,
			tenantID, orgID).Scan(&maxNum)
	} else {
		h.db.QueryRow(`SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number,'[^0-9]','','g') AS BIGINT)),0)
			FROM sales_orders WHERE tenant_id=$1 AND organization_id IS NULL AND order_number ~ '^S[0-9]+$'`,
			tenantID).Scan(&maxNum)
	}
	return fmt.Sprintf("S%05d", maxNum+1)
}

// nextProductionOrderCode generates MO##### scoped by tenant.
func (h *Handler) nextProductionOrderCode(tenantID uuid.UUID) string {
	var maxCode sql.NullString
	h.db.QueryRow(`SELECT MAX(code) FROM production_orders WHERE tenant_id=$1 AND code ~ '^MO[0-9]+$'`, tenantID).Scan(&maxCode)
	next := 1
	if maxCode.Valid && len(maxCode.String) > 2 {
		if n, err := parseIntSafe(maxCode.String[2:]); err == nil {
			next = n + 1
		}
	}
	return fmt.Sprintf("MO%05d", next)
}

// ListContactSalesHistory — GET /contacts/:id/sales — sales orders for a customer.
func (h *Handler) ListContactSalesHistory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	customerID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid customer id")
		return
	}
	rows, err := h.db.Query(`
		SELECT id, order_number, order_date, total_amount, paid_amount,
		       (total_amount - paid_amount) AS remaining, status, payment_status, created_at
		FROM sales_orders
		WHERE tenant_id=$1 AND customer_id=$2 AND deleted_at IS NULL
		ORDER BY created_at DESC`, tenantID, customerID)
	if err != nil {
		h.log.Error("contact sales history", "error", err)
		response.InternalError(c, "Failed to load sales history")
		return
	}
	defer rows.Close()

	type saleRow struct {
		ID            string    `json:"id"`
		OrderNumber   string    `json:"order_number"`
		OrderDate     time.Time `json:"order_date"`
		TotalAmount   float64   `json:"total_amount"`
		PaidAmount    float64   `json:"paid_amount"`
		Remaining     float64   `json:"remaining"`
		Status        string    `json:"status"`
		PaymentStatus string    `json:"payment_status"`
		CreatedAt     time.Time `json:"created_at"`
	}
	out := []saleRow{}
	for rows.Next() {
		var s saleRow
		if err := rows.Scan(&s.ID, &s.OrderNumber, &s.OrderDate, &s.TotalAmount, &s.PaidAmount,
			&s.Remaining, &s.Status, &s.PaymentStatus, &s.CreatedAt); err != nil {
			continue
		}
		out = append(out, s)
	}
	response.Success(c, out)
}

func parseIntSafe(s string) (int, error) {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}
