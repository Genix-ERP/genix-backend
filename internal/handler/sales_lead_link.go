package handler

// sales_lead_link.go — CRM won-deal → Savdo handoff (savdo-audit.md §6.1).
// Until now NO path existed from a won lead to a sales order: WinLead resolves
// a partner and stops. These endpoints close the loop:
//   POST /leads/:id/sales-order   — create a draft order pre-filled from the lead
//   GET  /leads/:id/sales-orders  — the lead's orders (for the lead detail UI)

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateSalesOrderFromLead creates a draft sales order shell from a won lead:
// customer = the lead's resolved partner, responsible = the lead's assignee,
// lead_id back-link set. Lines are added afterwards in the order form.
func (h *Handler) CreateSalesOrderFromLead(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}
	userID, _ := middleware.GetUserID(c)

	leadID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}

	var contactName, companyName string
	var partnerID, assignedTo, leadOrgID sql.NullString
	var expectedValue float64
	err = h.db.QueryRow(`
		SELECT COALESCE(contact_name, ''), COALESCE(company_name, ''),
		       partner_id, assigned_to, organization_id, COALESCE(expected_value, 0)
		FROM leads WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		leadID, tenantID,
	).Scan(&contactName, &companyName, &partnerID, &assignedTo, &leadOrgID, &expectedValue)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Lead")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load lead")
		return
	}
	if !partnerID.Valid || partnerID.String == "" {
		response.BadRequest(c, "Lead has no partner yet — win the lead first (partner is created on win)")
		return
	}
	customerID, err := uuid.Parse(partnerID.String)
	if err != nil {
		response.InternalError(c, "Lead partner reference is invalid")
		return
	}

	// Org: the lead's org, else the request header org.
	var orderOrgID *uuid.UUID
	if leadOrgID.Valid && leadOrgID.String != "" {
		if oid, oerr := uuid.Parse(leadOrgID.String); oerr == nil {
			orderOrgID = &oid
		}
	}
	if orderOrgID == nil {
		if headerOrg, orgOk := middleware.GetOrganizationID(c); orgOk && headerOrg != uuid.Nil {
			ho := headerOrg
			orderOrgID = &ho
		}
	}

	var salesRepID *uuid.UUID
	if assignedTo.Valid && assignedTo.String != "" {
		if rid, rerr := uuid.Parse(assignedTo.String); rerr == nil {
			salesRepID = &rid
		}
	}
	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	leadLabel := contactName
	if companyName != "" {
		leadLabel = fmt.Sprintf("%s (%s)", contactName, companyName)
	}
	notes := fmt.Sprintf("CRM lead: %s", leadLabel)
	if expectedValue > 0 {
		notes += fmt.Sprintf(" · kutilgan summa: %.0f", expectedValue)
	}

	now := time.Now()
	orderID := uuid.New()

	tx, txErr := h.db.Begin()
	if txErr != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Same S-series as CreateSalesOrder / quote conversion.
	var maxNum int64
	if orderOrgID != nil {
		tx.QueryRow(`
			SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number, '[^0-9]', '', 'g') AS BIGINT)), 0)
			FROM sales_orders
			WHERE tenant_id = $1 AND organization_id = $2 AND order_number ~ '^S[0-9]+$'`,
			tenantID, *orderOrgID).Scan(&maxNum)
	} else {
		tx.QueryRow(`
			SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number, '[^0-9]', '', 'g') AS BIGINT)), 0)
			FROM sales_orders
			WHERE tenant_id = $1 AND organization_id IS NULL AND order_number ~ '^S[0-9]+$'`,
			tenantID).Scan(&maxNum)
	}
	orderNumber := fmt.Sprintf("S%05d", maxNum+1)

	_, err = tx.Exec(`
		INSERT INTO sales_orders (
			id, tenant_id, organization_id, order_number, customer_id, lead_id,
			order_date, subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
			status, payment_status, notes, sales_rep_id, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 0, 0, 0, 0, 'draft', 'unpaid', $8, $9, $10, $11, $11)`,
		orderID, tenantID, orderOrgID, orderNumber, customerID, leadID,
		now, notes, salesRepID, createdBy, now,
	)
	if err != nil {
		h.log.Error("CreateSalesOrderFromLead: insert failed", "error", err, "lead_id", leadID)
		response.InternalError(c, "Failed to create sales order from lead")
		return
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit")
		return
	}

	go h.EmitWorkflowEvent(tenantID, "sales_order.created", map[string]interface{}{
		"record_id":     orderID.String(),
		"order_number":  orderNumber,
		"customer_name": leadLabel,
		"total_amount":  0.0,
		"lead_id":       leadID.String(),
	})

	response.Success(c, gin.H{
		"id":           orderID.String(),
		"order_number": orderNumber,
		"customer_id":  customerID.String(),
		"lead_id":      leadID.String(),
		"status":       "draft",
	})
}

// ListLeadSalesOrders returns the sales orders created from a lead.
func (h *Handler) ListLeadSalesOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}
	leadID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid lead ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT so.id, so.order_number, so.status, so.payment_status,
		       COALESCE(so.total_amount, 0), COALESCE(so.paid_amount, 0), so.order_date
		FROM sales_orders so
		WHERE so.lead_id = $1 AND so.tenant_id = $2 AND so.deleted_at IS NULL
		ORDER BY so.created_at DESC`,
		leadID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to list lead orders")
		return
	}
	defer rows.Close()

	orders := []map[string]interface{}{}
	for rows.Next() {
		var id, number, status, payStatus string
		var total, paid float64
		var orderDate time.Time
		if rows.Scan(&id, &number, &status, &payStatus, &total, &paid, &orderDate) == nil {
			orders = append(orders, map[string]interface{}{
				"id": id, "order_number": number, "status": status,
				"payment_status": payStatus, "total_amount": total,
				"paid_amount": paid, "order_date": orderDate.Format("2006-01-02"),
			})
		}
	}
	response.Success(c, orders)
}
