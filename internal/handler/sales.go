package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ListSalesOrders returns paginated list of sales orders
// ListSalesOrders godoc
// @Summary List sales orders
// @Description Get a paginated list of sales orders
// @Tags Sales
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param status query string false "Filter by status (draft, confirmed, invoiced)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /sales/orders [get]
func (h *Handler) ListSalesOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	// Pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 10000 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Build query with filters - JOIN with contacts to get customer_name
	baseQuery := `
		SELECT so.id, so.tenant_id, so.organization_id, so.order_number, so.customer_id, so.contact_person_id,
			   so.order_date, so.expected_date, so.billing_address, so.shipping_address,
			   so.currency_id, so.exchange_rate, so.subtotal, so.discount_type, so.discount_value, so.discount_amount,
			   so.tax_amount, so.shipping_amount, so.total_amount, so.status, so.payment_status, so.payment_terms,
			   so.payment_term_id,
			   so.reference, so.po_number, so.notes, so.internal_notes, so.warehouse_id, so.vehicle_number, so.sales_rep_id,
			   so.approved_by, so.approved_at, so.created_by, so.created_at, so.updated_at,
			   COALESCE(c.name, '') as customer_name,
			   EXISTS(SELECT 1 FROM sales_invoices si WHERE si.sales_order_id = so.id AND si.tenant_id = so.tenant_id AND si.deleted_at IS NULL AND si.status != 'cancelled') as has_invoice
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.tenant_id = $1 AND so.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM sales_orders so WHERE so.tenant_id = $1 AND so.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND so.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND so.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND so.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND so.status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by payment_status
	if paymentStatus := c.Query("payment_status"); paymentStatus != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND so.payment_status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND so.payment_status = $%d", argCount)
		args = append(args, paymentStatus)
	}

	// Filter by customer_id
	if customerID := c.Query("customer_id"); customerID != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND so.customer_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND so.customer_id = $%d", argCount)
		args = append(args, customerID)
	}

	// Filter by date range
	if dateFrom := c.Query("date_from"); dateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND so.order_date >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND so.order_date >= $%d", argCount)
		args = append(args, dateFrom)
	}
	if dateTo := c.Query("date_to"); dateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND so.order_date <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND so.order_date <= $%d", argCount)
		args = append(args, dateTo)
	}

	// Search - also search by customer name
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(so.order_number) LIKE $%d OR LOWER(so.reference) LIKE $%d OR LOWER(so.po_number) LIKE $%d OR LOWER(c.name) LIKE $%d)", argCount, argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(so.order_number) LIKE $%d OR LOWER(so.reference) LIKE $%d OR LOWER(so.po_number) LIKE $%d)", argCount, argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count sales orders")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY so.created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to fetch sales orders", "error", err)
		response.InternalError(c, "Failed to fetch sales orders")
		return
	}
	defer rows.Close()

	var orders []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan, customerID uuid.UUID
		var organizationID, contactPersonID, currencyID, warehouseID, vehicleNumber, salesRepID, approvedBy, createdBy sql.NullString
		var orderNumber, customerName string
		var orderDate time.Time
		var expectedDate, approvedAt sql.NullTime
		var billingAddress, shippingAddress []byte
		var exchangeRate, subtotal, discountValue, discountAmount, taxAmount, shippingAmount, totalAmount float64
		var discountType, status, paymentStatus, reference, poNumber, notes, internalNotes sql.NullString
		var paymentTerms int
		var paymentTermID sql.NullString
		var createdAt, updatedAt time.Time
		var hasInvoice bool

		err := rows.Scan(
			&id, &tenantIDScan, &organizationID, &orderNumber, &customerID, &contactPersonID,
			&orderDate, &expectedDate, &billingAddress, &shippingAddress,
			&currencyID, &exchangeRate, &subtotal, &discountType, &discountValue, &discountAmount,
			&taxAmount, &shippingAmount, &totalAmount, &status, &paymentStatus, &paymentTerms,
			&paymentTermID,
			&reference, &poNumber, &notes, &internalNotes, &warehouseID, &vehicleNumber, &salesRepID,
			&approvedBy, &approvedAt, &createdBy, &createdAt, &updatedAt,
			&customerName, &hasInvoice,
		)
		if err != nil {
			continue
		}

		order := map[string]interface{}{
			"id":              id.String(),
			"tenant_id":       tenantIDScan.String(),
			"order_number":    orderNumber,
			"customer_id":     customerID.String(),
			"customer_name":   customerName,
			"order_date":      orderDate.Format("2006-01-02"),
			"exchange_rate":   exchangeRate,
			"subtotal":        subtotal,
			"discount_value":  discountValue,
			"discount_amount": discountAmount,
			"tax_amount":      taxAmount,
			"shipping_amount": shippingAmount,
			"total_amount":    totalAmount,
			"status":          status.String,
			"payment_status":  paymentStatus.String,
			"payment_terms":   paymentTerms,
			"has_invoice":     hasInvoice,
			"created_at":      createdAt,
			"updated_at":      updatedAt,
		}

		if paymentTermID.Valid {
			order["payment_term_id"] = paymentTermID.String
		}
		if organizationID.Valid {
			order["organization_id"] = organizationID.String
		}
		if contactPersonID.Valid {
			order["contact_person_id"] = contactPersonID.String
		}
		if expectedDate.Valid {
			order["expected_date"] = expectedDate.Time.Format("2006-01-02")
		}
		if currencyID.Valid {
			order["currency_id"] = currencyID.String
		}
		if discountType.Valid {
			order["discount_type"] = discountType.String
		}
		if reference.Valid {
			order["reference"] = reference.String
		}
		if poNumber.Valid {
			order["po_number"] = poNumber.String
		}
		if notes.Valid {
			order["notes"] = notes.String
		}
		if warehouseID.Valid {
			order["warehouse_id"] = warehouseID.String
		}
		if vehicleNumber.Valid {
			order["vehicle_number"] = vehicleNumber.String
		}
		if salesRepID.Valid {
			order["sales_rep_id"] = salesRepID.String
		}

		// Parse addresses
		if len(billingAddress) > 0 {
			var addr map[string]interface{}
			if json.Unmarshal(billingAddress, &addr) == nil {
				order["billing_address"] = addr
			}
		}
		if len(shippingAddress) > 0 {
			var addr map[string]interface{}
			if json.Unmarshal(shippingAddress, &addr) == nil {
				order["shipping_address"] = addr
			}
		}

		orders = append(orders, order)
	}

	response.Paginated(c, orders, page, pageSize, total)
}

// SimpleSalesOrderInput represents a simplified input for creating sales orders from frontend
type SimpleSalesOrderInput struct {
	// Standard API fields
	OrganizationID  string                              `json:"organization_id,omitempty"`
	CustomerID      string                              `json:"customer_id,omitempty"`
	ContactPersonID string                              `json:"contact_person_id,omitempty"`
	OrderDate       string                              `json:"order_date"`
	ExpectedDate    string                              `json:"expected_date,omitempty"`
	DeliveryDate    string                              `json:"delivery_date,omitempty"`
	BillingAddress  *entity.Address                     `json:"billing_address,omitempty"`
	ShippingAddress *entity.Address                     `json:"shipping_address,omitempty"`
	CurrencyID      string                              `json:"currency_id,omitempty"`
	DiscountType    string                              `json:"discount_type,omitempty"`
	DiscountValue   float64                             `json:"discount_value,omitempty"`
	DiscountAmount  float64                             `json:"discount_amount,omitempty"`
	DiscountCode    string                              `json:"discount_code,omitempty"`
	ShippingAmount  float64                             `json:"shipping_amount,omitempty"`
	ShippingCost    float64                             `json:"shipping_cost,omitempty"`
	PaymentTerms    int                                 `json:"payment_terms,omitempty"`
	Reference       string                              `json:"reference,omitempty"`
	PONumber        string                              `json:"po_number,omitempty"`
	Notes           string                              `json:"notes,omitempty"`
	InternalNotes   string                              `json:"internal_notes,omitempty"`
	WarehouseID     string                              `json:"warehouse_id,omitempty"`
	Carrier         string                              `json:"carrier,omitempty"`
	VehicleNumber   string                              `json:"vehicle_number,omitempty"`
	SalesRepID      string                              `json:"sales_rep_id,omitempty"`
	ProjectID       string                              `json:"project_id,omitempty"`
	ProjectName     string                              `json:"project_name,omitempty"`
	Lines           []entity.CreateSalesOrderLineInput  `json:"lines,omitempty"`

	// Simplified frontend fields
	OrderNumber   string  `json:"order_number,omitempty"`
	CustomerName  string  `json:"customer_name,omitempty"`
	Subtotal      float64 `json:"subtotal,omitempty"`
	TaxAmount     float64 `json:"tax_amount,omitempty"`
	TotalAmount   float64 `json:"total_amount,omitempty"`
	Status        string  `json:"status,omitempty"`
	PaymentStatus string  `json:"payment_status,omitempty"`
}

// CreateSalesOrder creates a new sales order
// CreateSalesOrder godoc
// @Summary Create a new sales order
// @Description Create a new sales order with line items, customer info, and pricing details
// @Tags Sales
// @Accept json
// @Produce json
// @Param order body SimpleSalesOrderInput true "Sales order details"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /sales/orders [post]
func (h *Handler) CreateSalesOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input SimpleSalesOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Handle customer - either by ID or by name lookup
	var customerID uuid.UUID
	var customerName string
	var err error

	if input.CustomerID != "" {
		customerID, err = uuid.Parse(input.CustomerID)
		if err != nil {
			response.BadRequest(c, "Invalid customer_id")
			return
		}
		// Get customer name for response
		h.db.QueryRow("SELECT name FROM contacts WHERE id = $1 AND tenant_id = $2", customerID, tenantID).Scan(&customerName)
	} else if input.CustomerName != "" {
		// Try to find customer by name, or create a placeholder contact
		customerName = input.CustomerName
		err = h.db.QueryRow(
			"SELECT id FROM contacts WHERE tenant_id = $1 AND name ILIKE $2 LIMIT 1",
			tenantID, input.CustomerName,
		).Scan(&customerID)
		if err == sql.ErrNoRows {
			// Create a placeholder contact for this customer
			customerID = uuid.New()
			now := time.Now()
			// Generate a unique code for the contact
			contactCode := "CUST-" + uuid.New().String()[:8]
			_, execErr := h.db.Exec(`
				INSERT INTO contacts (id, tenant_id, type, code, name, created_at, updated_at)
				VALUES ($1, $2, 'customer', $3, $4, $5, $6)`,
				customerID, tenantID, contactCode, input.CustomerName, now, now,
			)
			if execErr != nil {
				h.log.Error("Failed to create placeholder contact", "error", execErr)
				response.InternalError(c, "Failed to create customer contact")
				return
			}
		} else if err != nil {
			response.InternalError(c, "Failed to lookup customer")
			return
		}
	} else {
		response.BadRequest(c, "Either customer_id or customer_name is required")
		return
	}

	// Parse order date
	orderDateStr := input.OrderDate
	if orderDateStr == "" {
		orderDateStr = time.Now().Format("2006-01-02")
	}
	orderDate, err := time.Parse("2006-01-02", orderDateStr[:10]) // Handle ISO format
	if err != nil {
		response.BadRequest(c, "Invalid order_date format, expected YYYY-MM-DD")
		return
	}

	// Order number is generated below, AFTER org resolution, because the unique
	// constraint is (tenant_id, organization_id, order_number). The old code used
	// SELECT COUNT(*) scoped only by tenant which caused collisions across orgs
	// and a classic race when two POSTs landed back-to-back.
	orderNumber := input.OrderNumber

	orderID := uuid.New()
	now := time.Now()

	// Calculate totals - either from lines or from provided totals
	var subtotal, taxAmount, discountAmount, totalAmount float64
	shippingAmount := input.ShippingAmount
	if shippingAmount == 0 {
		shippingAmount = input.ShippingCost
	}

	if len(input.Lines) > 0 {
		// Calculate from lines
		for _, line := range input.Lines {
			lineTotal := line.Quantity * line.UnitPrice
			var lineDiscount float64
			if line.DiscountType == "percentage" && line.DiscountValue > 0 {
				lineDiscount = lineTotal * line.DiscountValue / 100
			} else if line.DiscountType == "fixed" && line.DiscountValue > 0 {
				lineDiscount = line.DiscountValue
			}
			subtotal += lineTotal - lineDiscount
		}
		// Apply order-level discount - prefer frontend's calculated discount_amount
		if input.DiscountAmount > 0 {
			discountAmount = input.DiscountAmount
		} else if input.DiscountType == "percentage" && input.DiscountValue > 0 {
			discountAmount = subtotal * input.DiscountValue / 100
		} else if input.DiscountType == "fixed" && input.DiscountValue > 0 {
			discountAmount = input.DiscountValue
		}
		// Use frontend's tax_amount if provided
		if input.TaxAmount > 0 {
			taxAmount = input.TaxAmount
		}
		// Use frontend's total if provided, otherwise calculate
		if input.TotalAmount > 0 {
			totalAmount = input.TotalAmount
		} else {
			totalAmount = subtotal - discountAmount + taxAmount + shippingAmount
		}
	} else {
		// Use provided totals from frontend
		subtotal = input.Subtotal
		taxAmount = input.TaxAmount
		discountAmount = input.DiscountAmount // Use discount amount from frontend
		totalAmount = input.TotalAmount
		if totalAmount == 0 {
			totalAmount = subtotal + taxAmount + shippingAmount - discountAmount
		}
	}

	// Marshal addresses to JSON - use nil for NULL in DB, or JSON string for value
	var billingAddressJSON, shippingAddressJSON *string
	if input.BillingAddress != nil {
		b, _ := json.Marshal(input.BillingAddress)
		s := string(b)
		billingAddressJSON = &s
	}
	if input.ShippingAddress != nil {
		b, _ := json.Marshal(input.ShippingAddress)
		s := string(b)
		shippingAddressJSON = &s
	}

	// Parse expected date if provided
	var expectedDate *time.Time
	if input.ExpectedDate != "" {
		ed, err := time.Parse("2006-01-02", input.ExpectedDate)
		if err == nil {
			expectedDate = &ed
		}
	}

	// Parse optional UUIDs
	var contactPersonID, currencyID, warehouseID, salesRepID *uuid.UUID
	if input.ContactPersonID != "" {
		id, _ := uuid.Parse(input.ContactPersonID)
		contactPersonID = &id
	}
	if input.CurrencyID != "" {
		id, _ := uuid.Parse(input.CurrencyID)
		currencyID = &id
	}
	if input.WarehouseID != "" {
		id, _ := uuid.Parse(input.WarehouseID)
		warehouseID = &id
	}
	if input.SalesRepID != "" {
		id, _ := uuid.Parse(input.SalesRepID)
		salesRepID = &id
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Resolve organization ID (input body first, then header)
	var orgID *uuid.UUID
	if input.OrganizationID != "" {
		if parsed, parseErr := uuid.Parse(input.OrganizationID); parseErr == nil {
			orgID = &parsed
		}
	}
	if orgID == nil {
		if headerOrgID, orgOk := middleware.GetOrganizationID(c); orgOk && headerOrgID != uuid.Nil {
			orgID = &headerOrgID
		}
	}

	// Handle project_id
	var projectID *uuid.UUID
	var projectName *string
	if input.ProjectID != "" {
		pid, pidErr := uuid.Parse(input.ProjectID)
		if pidErr == nil {
			projectID = &pid
		}
	}
	if input.ProjectName != "" {
		projectName = &input.ProjectName
	}

	// Insert sales order
	query := `
		INSERT INTO sales_orders (
			id, tenant_id, organization_id, order_number, customer_id, contact_person_id,
			order_date, expected_date, billing_address, shipping_address,
			currency_id, exchange_rate, subtotal, discount_type, discount_value, discount_amount, discount_code,
			tax_amount, shipping_amount, total_amount, status, payment_status, payment_terms,
			reference, po_number, notes, internal_notes, warehouse_id, carrier, vehicle_number, sales_rep_id,
			project_id, project_name,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36)`

	// Handle discount code - use nil for empty string
	var discountCode *string
	if input.DiscountCode != "" {
		discountCode = &input.DiscountCode
	}

	// Generate order number if not supplied. Uses MAX + 1 scoped by (tenant, org)
	// so we don't collide across orgs, and retries on unique-constraint violation
	// to survive concurrent POSTs racing for the same next number.
	nextOrderNumber := func() string {
		var maxNum int
		if orgID != nil {
			h.db.QueryRow(`
				SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number, '[^0-9]', '', 'g') AS BIGINT)), 0)
				FROM sales_orders
				WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL
				  AND order_number ~ '^S[0-9]+$'`,
				tenantID, *orgID,
			).Scan(&maxNum)
		} else {
			h.db.QueryRow(`
				SELECT COALESCE(MAX(CAST(REGEXP_REPLACE(order_number, '[^0-9]', '', 'g') AS BIGINT)), 0)
				FROM sales_orders
				WHERE tenant_id = $1 AND organization_id IS NULL AND deleted_at IS NULL
				  AND order_number ~ '^S[0-9]+$'`,
				tenantID,
			).Scan(&maxNum)
		}
		return fmt.Sprintf("S%05d", maxNum+1)
	}

	maxAttempts := 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if orderNumber == "" {
			orderNumber = nextOrderNumber()
		}

		_, err = h.db.Exec(query,
			orderID, tenantID, orgID, orderNumber, customerID, contactPersonID,
			orderDate, expectedDate, billingAddressJSON, shippingAddressJSON,
			currencyID, 1.0, subtotal, input.DiscountType, input.DiscountValue, discountAmount, discountCode,
			taxAmount, input.ShippingAmount, totalAmount, entity.OrderStatusDraft, entity.PaymentStatusUnpaid, input.PaymentTerms,
			input.Reference, input.PONumber, input.Notes, input.InternalNotes, warehouseID, input.Carrier, input.VehicleNumber, salesRepID,
			projectID, projectName,
			createdBy, now, now,
		)
		if err == nil {
			break
		}
		// Retry only on the order-number unique-violation collision; everything else fails fast.
		if strings.Contains(err.Error(), "sales_orders_tenant_org_order_number_key") && input.OrderNumber == "" {
			h.log.Warn("Sales order number collision, retrying", "order_number", orderNumber, "attempt", attempt+1)
			orderNumber = "" // force regeneration next iteration
			continue
		}
		h.log.Error("Failed to create sales order", "error", err, "customer_id", customerID, "order_number", orderNumber)
		response.InternalError(c, "Failed to create sales order")
		return
	}
	if err != nil {
		h.log.Error("Failed to create sales order after retries", "error", err, "customer_id", customerID, "order_number", orderNumber)
		response.InternalError(c, "Failed to create sales order")
		return
	}

	// Insert order lines
	for i, line := range input.Lines {
		lineID := uuid.New()
		productID, _ := uuid.Parse(line.ProductID)

		lineTotal := line.Quantity * line.UnitPrice
		var lineDiscount float64
		if line.DiscountType == "percentage" && line.DiscountValue > 0 {
			lineDiscount = lineTotal * line.DiscountValue / 100
		} else if line.DiscountType == "fixed" && line.DiscountValue > 0 {
			lineDiscount = line.DiscountValue
		}

		var unitID, taxID, lineWarehouseID, packagingID *uuid.UUID
		if line.UnitID != "" {
			id, _ := uuid.Parse(line.UnitID)
			unitID = &id
		}
		if line.TaxID != "" {
			id, _ := uuid.Parse(line.TaxID)
			taxID = &id
		}
		if line.WarehouseID != "" {
			id, _ := uuid.Parse(line.WarehouseID)
			lineWarehouseID = &id
		}
		if line.PackagingID != "" {
			id, _ := uuid.Parse(line.PackagingID)
			packagingID = &id
		}

		lineQuery := `
			INSERT INTO sales_order_lines (
				id, sales_order_id, line_number, product_id, description,
				quantity, unit_id, unit_price, discount_type, discount_value, discount_amount,
				tax_id, tax_amount, line_total, quantity_delivered, quantity_invoiced,
				warehouse_id, notes, packaging_id, packaging_qty, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`

		h.db.Exec(lineQuery,
			lineID, orderID, i+1, productID, line.Description,
			line.Quantity, unitID, line.UnitPrice, line.DiscountType, line.DiscountValue, lineDiscount,
			taxID, 0.0, lineTotal-lineDiscount, 0.0, 0.0,
			lineWarehouseID, line.Notes, packagingID, line.PackagingQty, now, now,
		)
	}

	// Intercompany: PO will be created when SO is confirmed, not at creation time

	// Use frontend status/payment_status if provided, otherwise use defaults
	orderStatus := input.Status
	if orderStatus == "" {
		orderStatus = string(entity.OrderStatusDraft)
	}
	paymentStatus := input.PaymentStatus
	if paymentStatus == "" {
		paymentStatus = string(entity.PaymentStatusUnpaid)
	}

	// Return created order with customer_name for frontend
	orderResponse := map[string]interface{}{
		"id":              orderID.String(),
		"tenant_id":       tenantID.String(),
		"order_number":    orderNumber,
		"customer_id":     customerID.String(),
		"customer_name":   customerName, // Include customer_name for frontend
		"order_date":      orderDate.Format("2006-01-02"),
		"subtotal":        subtotal,
		"discount_type":   input.DiscountType,
		"discount_value":  input.DiscountValue,
		"discount_amount": discountAmount,
		"discount_code":   input.DiscountCode,
		"tax_amount":      taxAmount,
		"shipping_amount": shippingAmount,
		"shipping_cost":   shippingAmount, // Alias for frontend
		"total_amount":    totalAmount,
		"status":          orderStatus,
		"payment_status":  paymentStatus,
		"payment_terms":   input.PaymentTerms,
		"created_at":      now,
		"created_date":    now.Format(time.RFC3339), // Alias for frontend
	}

	if input.Reference != "" {
		orderResponse["reference"] = input.Reference
	}
	if input.PONumber != "" {
		orderResponse["po_number"] = input.PONumber
	}
	if input.Notes != "" {
		orderResponse["notes"] = input.Notes
	}
	if expectedDate != nil {
		orderResponse["expected_date"] = expectedDate.Format("2006-01-02")
		orderResponse["delivery_date"] = expectedDate.Format("2006-01-02") // Alias for frontend
	} else if input.DeliveryDate != "" {
		orderResponse["delivery_date"] = input.DeliveryDate
	}

	response.Created(c, orderResponse)
}

// GetSalesOrder returns a single sales order by ID
// GetSalesOrder godoc
// @Summary Get a sales order by ID
// @Description Retrieve detailed information about a specific sales order including line items
// @Tags Sales
// @Accept json
// @Produce json
// @Param id path string true "Sales Order ID (UUID)"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /sales/orders/{id} [get]
func (h *Handler) GetSalesOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Get order with customer name
	query := `
		SELECT so.id, so.tenant_id, so.organization_id, so.order_number, so.customer_id, so.contact_person_id,
			   so.order_date, so.expected_date, so.billing_address, so.shipping_address,
			   so.currency_id, so.exchange_rate, so.subtotal, so.discount_type, so.discount_value, so.discount_amount,
			   so.tax_amount, so.shipping_amount, so.total_amount, so.status, so.payment_status, so.payment_terms,
			   so.reference, so.po_number, so.notes, so.internal_notes, so.warehouse_id, so.carrier, so.vehicle_number, so.sales_rep_id,
			   so.project_id, so.project_name,
			   so.approved_by, so.approved_at, so.created_by, so.created_at, so.updated_at,
			   COALESCE(c.name, '') as customer_name
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.id = $1 AND so.tenant_id = $2 AND so.deleted_at IS NULL`

	var id, tenantIDScan, customerID uuid.UUID
	var organizationID, contactPersonID, currencyID, warehouseID, carrier, vehicleNumber, salesRepID, approvedBy, createdBy sql.NullString
	var projectIDStr, projectNameStr sql.NullString
	var orderNumber, customerName string
	var orderDate time.Time
	var expectedDate, approvedAt sql.NullTime
	var billingAddress, shippingAddress []byte
	var exchangeRate, subtotal, discountValue, discountAmount, taxAmount, shippingAmount, totalAmount float64
	var discountType, status, paymentStatus, reference, poNumber, notes, internalNotes sql.NullString
	var paymentTerms int
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(query, orderID, tenantID).Scan(
		&id, &tenantIDScan, &organizationID, &orderNumber, &customerID, &contactPersonID,
		&orderDate, &expectedDate, &billingAddress, &shippingAddress,
		&currencyID, &exchangeRate, &subtotal, &discountType, &discountValue, &discountAmount,
		&taxAmount, &shippingAmount, &totalAmount, &status, &paymentStatus, &paymentTerms,
		&reference, &poNumber, &notes, &internalNotes, &warehouseID, &carrier, &vehicleNumber, &salesRepID,
		&projectIDStr, &projectNameStr,
		&approvedBy, &approvedAt, &createdBy, &createdAt, &updatedAt,
		&customerName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch sales order", "error", err)
		response.InternalError(c, "Failed to fetch sales order")
		return
	}

	order := map[string]interface{}{
		"id":              id.String(),
		"tenant_id":       tenantIDScan.String(),
		"order_number":    orderNumber,
		"customer_id":     customerID.String(),
		"customer_name":   customerName,
		"order_date":      orderDate.Format("2006-01-02"),
		"exchange_rate":   exchangeRate,
		"subtotal":        subtotal,
		"discount_value":  discountValue,
		"discount_amount": discountAmount,
		"tax_amount":      taxAmount,
		"shipping_amount": shippingAmount,
		"total_amount":    totalAmount,
		"status":          status.String,
		"payment_status":  paymentStatus.String,
		"payment_terms":   paymentTerms,
		"created_at":      createdAt,
		"updated_at":      updatedAt,
	}

	if organizationID.Valid {
		order["organization_id"] = organizationID.String
	}
	if contactPersonID.Valid {
		order["contact_person_id"] = contactPersonID.String
	}
	if expectedDate.Valid {
		order["expected_date"] = expectedDate.Time.Format("2006-01-02")
	}
	if currencyID.Valid {
		order["currency_id"] = currencyID.String
	}
	if discountType.Valid {
		order["discount_type"] = discountType.String
	}
	if reference.Valid {
		order["reference"] = reference.String
	}
	if poNumber.Valid {
		order["po_number"] = poNumber.String
	}
	if notes.Valid {
		order["notes"] = notes.String
	}
	if internalNotes.Valid {
		order["internal_notes"] = internalNotes.String
	}
	if warehouseID.Valid {
		order["warehouse_id"] = warehouseID.String
	}
	if carrier.Valid {
		order["carrier"] = carrier.String
	}
	if vehicleNumber.Valid {
		order["vehicle_number"] = vehicleNumber.String
	}
	if salesRepID.Valid {
		order["sales_rep_id"] = salesRepID.String
	}
	if projectIDStr.Valid {
		order["project_id"] = projectIDStr.String
	}
	if projectNameStr.Valid {
		order["project_name"] = projectNameStr.String
	}

	// Get order lines with product name, unit name and packaging info
	linesQuery := `
		SELECT sol.id, sol.line_number, sol.product_id, sol.description, sol.quantity, sol.unit_id, sol.unit_price,
			   sol.discount_type, sol.discount_value, sol.discount_amount, sol.tax_id, sol.tax_amount, sol.line_total,
			   sol.quantity_delivered, sol.quantity_invoiced, sol.warehouse_id, sol.notes,
			   sol.packaging_id, sol.packaging_qty,
			   COALESCE(p.name, '') as product_name,
			   COALESCE(pp.name, '') as packaging_name, COALESCE(pp.qty, 0) as packaging_unit_qty,
			   COALESCE(u.name, '') as unit_name
		FROM sales_order_lines sol
		LEFT JOIN products p ON p.id = sol.product_id
		LEFT JOIN product_packagings pp ON pp.id = sol.packaging_id
		LEFT JOIN units_of_measure u ON sol.unit_id = u.id
		WHERE sol.sales_order_id = $1
		ORDER BY sol.line_number`

	linesRows, err := h.db.Query(linesQuery, orderID)
	if err == nil {
		defer linesRows.Close()
		var lines []map[string]interface{}
		for linesRows.Next() {
			var lineID, productID uuid.UUID
			var lineNumber int
			var description, lineDiscountType, lineNotes sql.NullString
			var quantity, unitPrice, lineDiscountValue, lineDiscountAmount, lineTaxAmount, lineTotal, qtyDelivered, qtyInvoiced float64
			var unitID, taxID, lineWarehouseID, packagingID sql.NullString
			var packagingQty sql.NullFloat64
			var productName, packagingName string
			var packagingUnitQty float64
			var unitName string

			err := linesRows.Scan(
				&lineID, &lineNumber, &productID, &description, &quantity, &unitID, &unitPrice,
				&lineDiscountType, &lineDiscountValue, &lineDiscountAmount, &taxID, &lineTaxAmount, &lineTotal,
				&qtyDelivered, &qtyInvoiced, &lineWarehouseID, &lineNotes,
				&packagingID, &packagingQty,
				&productName, &packagingName, &packagingUnitQty,
				&unitName,
			)
			if err != nil {
				continue
			}

			line := map[string]interface{}{
				"id":                 lineID.String(),
				"line_number":        lineNumber,
				"product_id":         productID.String(),
				"product_name":       productName,
				"quantity":           quantity,
				"unit_price":         unitPrice,
				"discount_value":     lineDiscountValue,
				"discount_amount":    lineDiscountAmount,
				"tax_amount":         lineTaxAmount,
				"line_total":         lineTotal,
				"quantity_delivered": qtyDelivered,
				"quantity_invoiced":  qtyInvoiced,
			}

			if description.Valid {
				line["description"] = description.String
			}
			if unitID.Valid {
				line["unit_id"] = unitID.String
			}
			if unitName != "" {
				line["unit_name"] = unitName
			}
			if lineDiscountType.Valid {
				line["discount_type"] = lineDiscountType.String
			}
			if taxID.Valid {
				line["tax_id"] = taxID.String
			}
			if lineWarehouseID.Valid {
				line["warehouse_id"] = lineWarehouseID.String
			}
			if lineNotes.Valid {
				line["notes"] = lineNotes.String
			}
			if packagingID.Valid {
				line["packaging_id"] = packagingID.String
				line["packaging_name"] = packagingName
				line["packaging_unit_qty"] = packagingUnitQty
			}
			if packagingQty.Valid {
				line["packaging_qty"] = packagingQty.Float64
			}

			lines = append(lines, line)
		}
		order["lines"] = lines
	}

	response.Success(c, order)
}

// UpdateSalesOrder updates an existing sales order
// UpdateSalesOrder godoc
// @Summary Update a sales order
// @Description Update sales order details. Only draft orders can have full updates. Status updates are allowed for any status. When status changes to 'shipped', inventory is automatically decreased and journal entries are created.
// @Tags Sales
// @Accept json
// @Produce json
// @Param id path string true "Sales Order ID (UUID)"
// @Param order body entity.UpdateSalesOrderInput true "Updated sales order details"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /sales/orders/{id} [put]
func (h *Handler) UpdateSalesOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	var input entity.UpdateSalesOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check if order exists
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", orderID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch sales order")
		return
	}

	// Allow status updates from any status, but restrict other field updates to draft orders
	isStatusOnlyUpdate := input.Status != nil && input.ExpectedDate == nil && input.DiscountType == nil &&
		input.DiscountValue == nil && input.ShippingAmount == nil && input.PaymentTerms == nil &&
		input.Reference == nil && input.PONumber == nil && input.Notes == nil &&
		input.InternalNotes == nil && input.WarehouseID == nil && input.Carrier == nil &&
		input.SalesRepID == nil && input.PaymentStatus == nil

	if !isStatusOnlyUpdate && currentStatus != string(entity.OrderStatusDraft) {
		response.BadRequest(c, "Can only update order details in draft status")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.ExpectedDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("expected_date = $%d", argCount))
		if *input.ExpectedDate != "" {
			ed, _ := time.Parse("2006-01-02", *input.ExpectedDate)
			args = append(args, ed)
		} else {
			args = append(args, nil)
		}
	}
	if input.DiscountType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("discount_type = $%d", argCount))
		args = append(args, *input.DiscountType)
	}
	if input.DiscountValue != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("discount_value = $%d", argCount))
		args = append(args, *input.DiscountValue)
	}
	if input.ShippingAmount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("shipping_amount = $%d", argCount))
		args = append(args, *input.ShippingAmount)
	}
	if input.PaymentTerms != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("payment_terms = $%d", argCount))
		args = append(args, *input.PaymentTerms)
	}
	if input.Reference != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("reference = $%d", argCount))
		args = append(args, *input.Reference)
	}
	if input.PONumber != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("po_number = $%d", argCount))
		args = append(args, *input.PONumber)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if input.InternalNotes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("internal_notes = $%d", argCount))
		args = append(args, *input.InternalNotes)
	}
	if input.WarehouseID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("warehouse_id = $%d", argCount))
		if *input.WarehouseID != "" {
			wid, _ := uuid.Parse(*input.WarehouseID)
			args = append(args, wid)
		} else {
			args = append(args, nil)
		}
	}
	if input.SalesRepID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("sales_rep_id = $%d", argCount))
		if *input.SalesRepID != "" {
			sid, _ := uuid.Parse(*input.SalesRepID)
			args = append(args, sid)
		} else {
			args = append(args, nil)
		}
	}
	if input.Carrier != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("carrier = $%d", argCount))
		args = append(args, *input.Carrier)
	}
	if input.VehicleNumber != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("vehicle_number = $%d", argCount))
		args = append(args, *input.VehicleNumber)
	}
	if input.ProjectID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("project_id = $%d", argCount))
		if *input.ProjectID != "" {
			pid, _ := uuid.Parse(*input.ProjectID)
			args = append(args, pid)
		} else {
			args = append(args, nil)
		}
	}
	if input.ProjectName != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("project_name = $%d", argCount))
		args = append(args, *input.ProjectName)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.PaymentStatus != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("payment_status = $%d", argCount))
		args = append(args, *input.PaymentStatus)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, orderID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE sales_orders SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update sales order", "error", err)
		response.InternalError(c, "Failed to update sales order")
		return
	}

	// ============================================
	// DECREASE INVENTORY WHEN SO STATUS → "shipped"
	// ============================================
	if input.Status != nil && *input.Status == "shipped" {
		now := time.Now()

		// Skip inventory deduction if stock operation already completed it
		var stockOpDone bool
		{
			var stockOpState string
			if err := h.db.QueryRow(`
				SELECT state FROM stock_operations
				WHERE source_type = 'sales_order' AND source_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
				ORDER BY created_at DESC LIMIT 1
			`, orderID, tenantID).Scan(&stockOpState); err == nil && stockOpState == "done" {
				stockOpDone = true
			}
		}

		// Get SO warehouse_id and organization_id
		var soWarehouseID sql.NullString
		var soOrgID sql.NullString
		h.db.QueryRow("SELECT warehouse_id, organization_id FROM sales_orders WHERE id = $1 AND tenant_id = $2", orderID, tenantID).Scan(&soWarehouseID, &soOrgID)

		// Determine warehouse
		var warehouseID uuid.UUID
		if soWarehouseID.Valid && soWarehouseID.String != "" {
			warehouseID, _ = uuid.Parse(soWarehouseID.String)
		} else {
			// Try delivery order's warehouse
			h.db.QueryRow(`
				SELECT warehouse_id FROM sales_delivery_orders
				WHERE sales_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
				ORDER BY created_at DESC LIMIT 1
			`, orderID, tenantID).Scan(&warehouseID)

			if warehouseID == uuid.Nil {
				// Fallback to org's first warehouse
				if soOrgID.Valid && soOrgID.String != "" {
					orgID, _ := uuid.Parse(soOrgID.String)
					h.db.QueryRow(`
						SELECT id FROM warehouses
						WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL
						ORDER BY created_at ASC LIMIT 1
					`, tenantID, orgID).Scan(&warehouseID)
				}
				if warehouseID == uuid.Nil {
					h.db.QueryRow(`
						SELECT id FROM warehouses
						WHERE tenant_id = $1 AND deleted_at IS NULL
						ORDER BY created_at ASC LIMIT 1
					`, tenantID).Scan(&warehouseID)
				}
			}
		}

		if warehouseID != uuid.Nil && !stockOpDone {
			// Get SO lines
			soLines, err := h.db.Query(`
				SELECT id, product_id, quantity, unit_price
				FROM sales_order_lines
				WHERE sales_order_id = $1 AND product_id IS NOT NULL
			`, orderID)
			if err == nil {
				// Collect all SO lines first
				type soLine struct {
					LineID    uuid.UUID
					ProductID uuid.UUID
					Qty       float64
					UnitPrice float64
				}
				var lines []soLine
				var allProductIDs []uuid.UUID
				for soLines.Next() {
					var l soLine
					if err := soLines.Scan(&l.LineID, &l.ProductID, &l.Qty, &l.UnitPrice); err != nil {
						continue
					}
					lines = append(lines, l)
					allProductIDs = append(allProductIDs, l.ProductID)
				}
				soLines.Close()

				if len(lines) > 0 {
					// Batch UPDATE quantity_delivered on all SO lines
					solIDs := make([]uuid.UUID, len(lines))
					for i, l := range lines {
						solIDs[i] = l.LineID
					}
					h.db.Exec(`
						UPDATE sales_order_lines
						SET quantity_delivered = quantity, updated_at = $1
						WHERE id = ANY($2)
					`, now, pq.Array(solIDs))

					// ONE query: get all existing inventory records for these products
					invMap := make(map[uuid.UUID]uuid.UUID) // productID -> inventoryID
					invRows, invErr := h.db.Query(`
						SELECT DISTINCT ON (product_id) id, product_id FROM inventory
						WHERE tenant_id = $1 AND product_id = ANY($2) AND warehouse_id = $3
						ORDER BY product_id, created_at ASC
					`, tenantID, pq.Array(allProductIDs), warehouseID)
					if invErr == nil {
						for invRows.Next() {
							var invID, pid uuid.UUID
							if err := invRows.Scan(&invID, &pid); err == nil {
								invMap[pid] = invID
							}
						}
						invRows.Close()
					}

					// Determine orgID once for potential inserts
					var orgIDVal interface{}
					if soOrgID.Valid && soOrgID.String != "" {
						orgIDVal, _ = uuid.Parse(soOrgID.String)
					}

					// Batch INSERT for missing inventory records
					var newInvValues []string
					var newInvArgs []interface{}
					newInvArgIdx := 0
					for _, l := range lines {
						if _, ok := invMap[l.ProductID]; !ok {
							newID := uuid.New()
							invMap[l.ProductID] = newID
							newInvValues = append(newInvValues, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,0,0,$%d,$%d,$%d)",
								newInvArgIdx+1, newInvArgIdx+2, newInvArgIdx+3, newInvArgIdx+4, newInvArgIdx+5, newInvArgIdx+6, newInvArgIdx+7, newInvArgIdx+8))
							newInvArgs = append(newInvArgs, newID, tenantID, l.ProductID, warehouseID, orgIDVal, l.UnitPrice, now, now)
							newInvArgIdx += 8
						}
					}
					if len(newInvValues) > 0 {
						h.db.Exec(`
							INSERT INTO inventory (id, tenant_id, product_id, warehouse_id, organization_id, quantity_on_hand, quantity_reserved, unit_cost, created_at, updated_at)
							VALUES `+strings.Join(newInvValues, ","), newInvArgs...)
					}

					// Batch UPDATE inventory quantities and batch INSERT inventory transactions
					var txValues []string
					var txArgs []interface{}
					txArgIdx := 0
					for _, l := range lines {
						inventoryID, ok := invMap[l.ProductID]
						if !ok {
							continue
						}

						// Individual UPDATE for inventory (each has different qty)
						h.db.Exec(`
							UPDATE inventory
							SET quantity_on_hand = quantity_on_hand - $1,
								last_movement_date = $2,
								updated_at = $2
							WHERE id = $3
						`, l.Qty, now, inventoryID)

						txID := uuid.New()
						txValues = append(txValues, fmt.Sprintf("($%d,$%d,$%d,'issue',$%d,$%d,$%d,'sales_order',$%d,'Sales Order Shipped',$%d,$%d)",
							txArgIdx+1, txArgIdx+2, txArgIdx+3, txArgIdx+4, txArgIdx+5, txArgIdx+6, txArgIdx+7, txArgIdx+8, txArgIdx+9))
						txArgs = append(txArgs, txID, tenantID, inventoryID, -l.Qty, l.UnitPrice, l.Qty*l.UnitPrice, orderID, now, now)
						txArgIdx += 9
					}

					// ONE INSERT for all inventory transactions
					if len(txValues) > 0 {
						h.db.Exec(`
							INSERT INTO inventory_transactions (
								id, tenant_id, inventory_id, transaction_type, quantity,
								unit_cost, total_cost, reference_type, reference_id,
								reason, transaction_date, created_at
							) VALUES `+strings.Join(txValues, ","), txArgs...)
					}
				}
			}

			// Also update any draft delivery orders to shipped
			h.db.Exec(`
				UPDATE sales_delivery_orders
				SET status = 'shipped', updated_at = $1
				WHERE sales_order_id = $2 AND tenant_id = $3 AND status != 'shipped' AND deleted_at IS NULL
			`, now, orderID, tenantID)

			h.log.Info("Inventory decreased from SO shipped", "so_id", orderID)
		}

		// Also mark related stock operation as done
		if !stockOpDone {
			h.db.Exec(`
				UPDATE stock_operations SET state = 'done', done_at = $1, updated_at = $1
				WHERE source_type = 'sales_order' AND source_id = $2 AND tenant_id = $3
				  AND state != 'done' AND state != 'cancelled' AND deleted_at IS NULL
			`, now, orderID, tenantID)
		}

		// ============================================
		// CREATE JOURNAL ENTRY: Debit Stock Interim Delivery, Credit Stock Valuation (Odoo-style)
		// Stock movement only — AR/Revenue entries created when invoice is sent
		// ============================================
		var soNumber string
		var soCustomerName sql.NullString
		var soCustomerID sql.NullString
		var soOrgIDForJE sql.NullString
		h.db.QueryRow(`
			SELECT so.order_number, c.name, so.customer_id, so.organization_id
			FROM sales_orders so
			LEFT JOIN contacts c ON so.customer_id = c.id
			WHERE so.id = $1
		`, orderID).Scan(&soNumber, &soCustomerName, &soCustomerID, &soOrgIDForJE)

		{
			var orgIDPtr *uuid.UUID
			if soOrgIDForJE.Valid && soOrgIDForJE.String != "" {
				parsed, _ := uuid.Parse(soOrgIDForJE.String)
				if parsed != uuid.Nil {
					orgIDPtr = &parsed
				}
			}

			// Get SO lines with cost price for stock movement JE
			type soLineAcct struct {
				ProductID  uuid.UUID
				CostAmount float64
				OutputAcct uuid.UUID
				ValuationAcct uuid.UUID
			}
			var soLines []soLineAcct
			rows, err := h.db.Query(`
				SELECT sol.product_id, sol.quantity, COALESCE(p.cost_price, 0)
				FROM sales_order_lines sol
				JOIN products p ON sol.product_id = p.id
				WHERE sol.sales_order_id = $1
			`, orderID)
			if err == nil {
				type rawSOLine struct {
					ProductID uuid.UUID
					Qty       float64
					CostPrice float64
				}
				var rawLines []rawSOLine
				for rows.Next() {
					var rl rawSOLine
					if err := rows.Scan(&rl.ProductID, &rl.Qty, &rl.CostPrice); err == nil && rl.CostPrice > 0 && rl.Qty > 0 {
						rawLines = append(rawLines, rl)
					}
				}
				rows.Close()
				// Resolve category accounts after closing rows
				for _, rl := range rawLines {
					ca := getCategoryAccounts(h.db, tenantID, orgIDPtr, rl.ProductID)
					soLines = append(soLines, soLineAcct{
						ProductID:     rl.ProductID,
						CostAmount:    rl.Qty * rl.CostPrice,
						OutputAcct:    ca.StockOutputAccountID,
						ValuationAcct: ca.StockValuationAccountID,
					})
				}
			}

			// Group by account pair
			type acctPair struct {
				Debit  uuid.UUID
				Credit uuid.UUID
			}
			grouped := make(map[acctPair]float64)
			for _, sl := range soLines {
				key := acctPair{Debit: sl.OutputAcct, Credit: sl.ValuationAcct}
				grouped[key] += sl.CostAmount
			}

			if len(grouped) > 0 {
				// Calculate total cost for JE header
				var totalCost float64
				for _, amt := range grouped {
					totalCost += amt
				}

				// Find stock journal or general
				var journalID uuid.UUID
				var numberPrefix sql.NullString
				err := h.db.QueryRow(`
					SELECT id, number_prefix FROM journals
					WHERE tenant_id = $1 AND type = 'sales' AND is_active = true
					ORDER BY created_at ASC LIMIT 1
				`, tenantID).Scan(&journalID, &numberPrefix)
				if err != nil {
					h.db.QueryRow(`
						SELECT id, number_prefix FROM journals
						WHERE tenant_id = $1 AND type = 'general' AND is_active = true
						ORDER BY created_at ASC LIMIT 1
					`, tenantID).Scan(&journalID, &numberPrefix)
				}

				if journalID != uuid.Nil {
					entryID := uuid.New()
					prefix := "SAL-"
					if numberPrefix.Valid && numberPrefix.String != "" {
						prefix = numberPrefix.String
					}
					entryNumber := fmt.Sprintf("%s%s-%s", prefix, time.Now().Format("20060102"), uuid.New().String()[:6])
					customerName := ""
					if soCustomerName.Valid {
						customerName = soCustomerName.String
					}
					description := fmt.Sprintf("Goods Delivery %s - %s", soNumber, customerName)

					var contactID *uuid.UUID
					if soCustomerID.Valid {
						parsed, _ := uuid.Parse(soCustomerID.String)
						if parsed != uuid.Nil {
							contactID = &parsed
						}
					}

					if _, err := h.db.Exec(`
						INSERT INTO journal_entries (
							id, tenant_id, organization_id, journal_id, entry_number,
							entry_date, description, source_type, source_id, status, total_debit, total_credit,
							created_at, updated_at
						) VALUES ($1, $2, $3, $4, $5, $6, $7, 'sales_order', $8, 'posted', $9, $9, $10, $10)
					`, entryID, tenantID, orgIDPtr, journalID, entryNumber,
						now, description, orderID.String(), totalCost, now); err != nil {
						h.log.Error("Stock movement JE INSERT failed", "error", err, "entry_number", entryNumber)
					} else {
					lineNumber := 1
					for pair, amount := range grouped {
						// Debit: Stock Interim Delivery
						debitLineID := uuid.New()
						if _, err := h.db.Exec(`
							INSERT INTO journal_entry_lines (
								id, journal_entry_id, account_id, contact_id, description,
								debit_amount, credit_amount, line_number, created_at
							) VALUES ($1, $2, $3, $4, $5, $6, 0, $7, $8)
						`, debitLineID, entryID, pair.Debit, contactID, "Stock Interim Delivery", amount, lineNumber, now); err != nil {
							h.log.Error("Stock movement JE debit line INSERT failed", "error", err)
						}
						lineNumber++

						// Credit: Stock Valuation
						creditLineID := uuid.New()
						if _, err := h.db.Exec(`
							INSERT INTO journal_entry_lines (
								id, journal_entry_id, account_id, contact_id, description,
								debit_amount, credit_amount, line_number, created_at
							) VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $8)
						`, creditLineID, entryID, pair.Credit, contactID, "Stock Valuation", amount, lineNumber, now); err != nil {
							h.log.Error("Stock movement JE credit line INSERT failed", "error", err)
						}
						lineNumber++

						// Update account balances
						h.db.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, amount, now, pair.Debit)
						h.db.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, amount, now, pair.Credit)
					}

					h.log.Info("Stock movement JE created for SO shipped (Odoo-style)", "entry_id", entryID, "cost", totalCost)
					}
				}
			}
		}
	}

	// Fetch and return updated order
	h.GetSalesOrder(c)
}

// DeleteSalesOrder soft deletes a sales order
// DeleteSalesOrder godoc
// @Summary Delete a sales order
// @Description Soft delete a sales order. Only orders in draft or cancelled status can be deleted.
// @Tags Sales
// @Accept json
// @Produce json
// @Param id path string true "Sales Order ID (UUID)"
// @Success 204 "No Content"
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /sales/orders/{id} [delete]
func (h *Handler) DeleteSalesOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Check if order is in draft status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", orderID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if currentStatus != string(entity.OrderStatusDraft) && currentStatus != string(entity.OrderStatusCancelled) {
		response.BadRequest(c, "Can only delete orders in draft or cancelled status")
		return
	}

	result, err := h.db.Exec(
		"UPDATE sales_orders SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), orderID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete sales order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Sales order")
		return
	}

	response.NoContent(c)
}

// ConfirmSalesOrder confirms a draft sales order and auto-creates a Delivery Order
// ConfirmSalesOrder godoc
// @Summary Confirm a sales order
// @Description Confirm a draft sales order and automatically create a delivery order. Only draft orders can be confirmed.
// @Tags Sales
// @Accept json
// @Produce json
// @Param id path string true "Sales Order ID (UUID)"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /sales/orders/{id}/confirm [post]
func (h *Handler) ConfirmSalesOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Get order details including warehouse_id and carrier for the delivery order
	var currentStatus, orderNumber string
	var customerID uuid.UUID
	var customerName sql.NullString
	var warehouseID sql.NullString
	var carrier sql.NullString
	var expectedDate sql.NullTime
	var organizationID *uuid.UUID

	err = h.db.QueryRow(`
		SELECT so.status, so.order_number, so.customer_id, c.name, so.warehouse_id, so.carrier, so.expected_date, so.organization_id
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.id = $1 AND so.tenant_id = $2 AND so.deleted_at IS NULL`,
		orderID, tenantID).Scan(&currentStatus, &orderNumber, &customerID, &customerName, &warehouseID, &carrier, &expectedDate, &organizationID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch sales order", "error", err)
		response.InternalError(c, "Failed to fetch sales order")
		return
	}
	if currentStatus != string(entity.OrderStatusDraft) {
		response.BadRequest(c, "Can only confirm orders in draft status")
		return
	}

	now := time.Now()

	// Update sales order status to confirmed
	_, err = h.db.Exec(
		"UPDATE sales_orders SET status = $1, approved_by = $2, approved_at = $3, updated_at = $4 WHERE id = $5 AND tenant_id = $6",
		entity.OrderStatusConfirmed, userID, now, now, orderID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to confirm sales order")
		return
	}

	// Auto-create Delivery Order (Odoo-like behavior)
	doID := uuid.New()
	doNumber := "DO-" + time.Now().Format("20060102150405")

	// Set delivery date to expected_date or today
	deliveryDate := now
	if expectedDate.Valid {
		deliveryDate = expectedDate.Time
	}

	// Parse warehouse_id if valid, otherwise get default warehouse
	var warehouseUUID *uuid.UUID
	if warehouseID.Valid && warehouseID.String != "" {
		wid, parseErr := uuid.Parse(warehouseID.String)
		if parseErr == nil {
			warehouseUUID = &wid
		}
	}

	// If no warehouse specified, get the default warehouse for this tenant
	if warehouseUUID == nil {
		var defaultWarehouseID uuid.UUID
		err := h.db.QueryRow(`
			SELECT id FROM warehouses
			WHERE tenant_id = $1 AND is_default = true AND deleted_at IS NULL
			LIMIT 1
		`, tenantID).Scan(&defaultWarehouseID)
		if err == nil {
			warehouseUUID = &defaultWarehouseID
		} else {
			// If no default, try to get any warehouse
			err = h.db.QueryRow(`
				SELECT id FROM warehouses
				WHERE tenant_id = $1 AND deleted_at IS NULL
				LIMIT 1
			`, tenantID).Scan(&defaultWarehouseID)
			if err == nil {
				warehouseUUID = &defaultWarehouseID
			}
		}
	}

	// Insert delivery order
	var carrierValue *string
	if carrier.Valid && carrier.String != "" {
		carrierValue = &carrier.String
	}

	_, err = h.db.Exec(`
		INSERT INTO sales_delivery_orders (
			id, tenant_id, organization_id, delivery_number, sales_order_id, so_number,
			customer_id, customer_name, delivery_date, scheduled_date, warehouse_id, carrier,
			status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'draft', $13, $14, $14)`,
		doID, tenantID, organizationID, doNumber, orderID, orderNumber,
		customerID, customerName.String, deliveryDate, expectedDate, warehouseUUID, carrierValue,
		userID, now,
	)
	if err != nil {
		h.log.Error("Failed to create delivery order", "error", err)
		// Don't fail the confirmation, just log the error
	} else {
		// Copy SO lines to DO lines for remaining quantities
		_, err = h.db.Exec(`
			INSERT INTO sales_delivery_order_lines (
				id, delivery_order_id, so_line_id, product_id, product_name,
				quantity_ordered, quantity_to_deliver, unit_price, warehouse_id, created_at
			)
			SELECT uuid_generate_v4(), $1, sol.id, sol.product_id, COALESCE(p.name, 'Unknown'),
				   sol.quantity, sol.quantity - COALESCE(sol.quantity_delivered, 0),
				   sol.unit_price, $2, NOW()
			FROM sales_order_lines sol
			LEFT JOIN products p ON p.id = sol.product_id
			WHERE sol.sales_order_id = $3 AND sol.quantity > COALESCE(sol.quantity_delivered, 0)`,
			doID, warehouseUUID, orderID,
		)
		if err != nil {
			h.log.Error("Failed to create delivery order lines", "error", err)
		}
	}

	// Auto-create stock operations (delivery chain) — TT 12.3: SO → delivery via stock_operations
	// For multi-step delivery: Pick → Pack → Ship (3-step) or Pick → Ship (2-step)
	if chainErr := h.createDeliveryChainForSO(tenantID, orderID, organizationID, warehouseUUID, customerID, orderNumber, expectedDate, userID, now); chainErr != nil {
		h.log.Error("Failed to create delivery chain for SO", "error", chainErr, "order_id", orderID, "warehouse_id", warehouseUUID)
	}

	// Auto-create Production Orders for products with insufficient stock
	h.autoCreateProductionOrders(tenantID, orderID, customerID, warehouseUUID, organizationID, userID, now)

	// Intercompany: create PO in draft in the buying company when SO is confirmed
	if err := h.icSync.SyncSaleOrderToPurchaseOrder(tenantID, orderID); err != nil {
		h.log.Error("Intercompany SO->PO sync failed", "error", err, "order_id", orderID)
	}

	// Intercompany: if this SO was created from a PO (vendor confirms SO),
	// update the linked PO to confirmed and create a receipt op for it.
	h.syncIntercompanySOConfirmToPO(tenantID, orderID, now)

	// Notify: sales order confirmed
	go func() {
		var totalAmt float64
		h.db.QueryRow(`SELECT COALESCE(total_amount, 0) FROM sales_orders WHERE id = $1`, orderID).Scan(&totalAmt)
		custName := ""
		if customerName.Valid {
			custName = customerName.String
		}
		amountStr := fmt.Sprintf("%.0f", totalAmt)
		h.createTranslatedNotification(tenantID, userID, "sales_order_confirmed",
			map[string]interface{}{
				"order_id":      orderID.String(),
				"order_number":  orderNumber,
				"customer_id":   customerID.String(),
				"customer_name": custName,
				"amount":        totalAmt,
			},
			orderNumber, custName, amountStr,
		)
	}()

	h.GetSalesOrder(c)
}

// createDeliveryChainForSO creates stock operations for SO delivery.
// For multi-step warehouses it creates the full chain: Pick → Pack → Ship.
func (h *Handler) createDeliveryChainForSO(
	tenantID, orderID uuid.UUID,
	organizationID *uuid.UUID,
	warehouseUUID *uuid.UUID,
	customerID uuid.UUID,
	orderNumber string,
	expectedDate sql.NullTime,
	userID uuid.UUID,
	now time.Time,
) error {
	// Determine delivery steps from warehouse config
	deliverySteps := 1
	if warehouseUUID != nil {
		h.db.QueryRow("SELECT COALESCE(delivery_steps, 1) FROM warehouses WHERE id = $1 AND tenant_id = $2",
			*warehouseUUID, tenantID).Scan(&deliverySteps)
	}

	// Collect SO lines once for reuse
	type soLine struct {
		ProductID uuid.UUID
		Qty       float64
		UnitPrice float64
		UOM       string
	}
	var soLines []soLine
	rows, err := h.db.Query(`
		SELECT sol.product_id, sol.quantity, sol.unit_price, COALESCE(u.name, 'unit') as uom
		FROM sales_order_lines sol
		LEFT JOIN units_of_measure u ON u.id = sol.unit_id
		WHERE sol.sales_order_id = $1 AND sol.product_id IS NOT NULL
		  AND sol.quantity > COALESCE(sol.quantity_delivered, 0)
	`, orderID)
	if err != nil {
		h.log.Error("Failed to fetch SO lines for delivery chain", "error", err)
		return fmt.Errorf("failed to fetch SO lines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l soLine
		if err := rows.Scan(&l.ProductID, &l.Qty, &l.UnitPrice, &l.UOM); err == nil {
			soLines = append(soLines, l)
		}
	}
	if len(soLines) == 0 {
		h.log.Warn("No SO lines with products found for delivery chain", "order_id", orderID)
		return fmt.Errorf("no order lines with products found")
	}

	// Helper: find operation type by warehouse + type code
	findOpType := func(typeCode string) (uuid.UUID, *uuid.UUID, *uuid.UUID) {
		var id uuid.UUID
		var srcLoc, destLoc *uuid.UUID
		q := `SELECT id, default_location_src_id, default_location_dest_id
		      FROM warehouse_operation_types
		      WHERE tenant_id = $1 AND type = $2 AND is_active = true`
		args := []interface{}{tenantID, typeCode}
		argIdx := 2
		if organizationID != nil {
			argIdx++
			q += fmt.Sprintf(" AND (organization_id = $%d OR organization_id IS NULL)", argIdx)
			args = append(args, *organizationID)
		}
		if warehouseUUID != nil {
			argIdx++
			q += fmt.Sprintf(" AND warehouse_id = $%d", argIdx)
			args = append(args, *warehouseUUID)
		}
		q += " ORDER BY organization_id IS NULL, sequence LIMIT 1"
		h.db.QueryRow(q, args...).Scan(&id, &srcLoc, &destLoc)
		return id, srcLoc, destLoc
	}

	// Convert NullTime to *time.Time for SQL
	var schedDate *time.Time
	if expectedDate.Valid {
		schedDate = &expectedDate.Time
	}

	// Helper: create a stock operation with its lines
	createOp := func(opTypeID uuid.UUID, direction, initialState string, srcLoc, destLoc *uuid.UUID) uuid.UUID {
		if opTypeID == uuid.Nil {
			return uuid.Nil
		}
		var totalSteps int
		h.db.QueryRow("SELECT COUNT(*) FROM operation_type_steps WHERE operation_type_id = $1 AND tenant_id = $2",
			opTypeID, tenantID).Scan(&totalSteps)
		if totalSteps == 0 {
			totalSteps = 1
		}

		opID := uuid.New()
		opName := h.nextStockOperationName(tenantID, direction)
		_, opErr := h.db.Exec(`
			INSERT INTO stock_operations (
				id, tenant_id, organization_id, name, operation_type_id, direction,
				date, scheduled_date, partner_id, source_document,
				source_location_id, dest_location_id,
				state, current_step, total_steps, priority,
				source_type, source_id,
				responsible_id, created_by, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1,$14,'normal','sales_order',$17,$15,$15,$16,$16)
		`,
			opID, tenantID, organizationID, opName, opTypeID, direction,
			now, schedDate, customerID, orderNumber,
			srcLoc, destLoc,
			initialState, totalSteps, userID, now, orderID,
		)
		if opErr != nil {
			h.log.Error("Failed to create stock operation in delivery chain", "error", opErr, "direction", direction)
			return uuid.Nil
		}

		// Create lines
		for _, l := range soLines {
			h.db.Exec(`
				INSERT INTO stock_operation_lines (
					id, tenant_id, operation_id, product_id,
					expected_qty, done_qty, uom, unit_price,
					quality_status, created_at, updated_at
				) VALUES (uuid_generate_v4(),$1,$2,$3,$4,0,$5,$6,'good',$7,$7)
			`, tenantID, opID, l.ProductID, l.Qty, l.UOM, l.UnitPrice, now)
		}

		// Create initial step log
		var firstStep struct {
			ID   uuid.UUID
			Name string
		}
		if stepErr := h.db.QueryRow(`
			SELECT id, name FROM operation_type_steps
			WHERE operation_type_id = $1 AND tenant_id = $2 ORDER BY sequence LIMIT 1
		`, opTypeID, tenantID).Scan(&firstStep.ID, &firstStep.Name); stepErr == nil {
			h.db.Exec(`
				INSERT INTO stock_operation_step_log (
					id, tenant_id, operation_id, step_id, step_sequence, step_name, state, created_at
				) VALUES (uuid_generate_v4(),$1,$2,$3,1,$4,'ready',$5)
			`, tenantID, opID, firstStep.ID, firstStep.Name, now)
		}
		return opID
	}

	// Build the chain based on delivery_steps
	switch deliverySteps {
	case 3:
		// 3-step: Pick → Pack → Ship
		pickTypeID, pickSrc, pickDest := findOpType("pick")
		packTypeID, packSrc, packDest := findOpType("pack")
		deliveryTypeID, delSrc, delDest := findOpType("delivery")

		if pickTypeID == uuid.Nil || packTypeID == uuid.Nil || deliveryTypeID == uuid.Nil {
			h.log.Warn("Missing operation types for 3-step delivery", "tenant_id", tenantID, "warehouse_id", warehouseUUID,
				"pick", pickTypeID, "pack", packTypeID, "delivery", deliveryTypeID)
			return fmt.Errorf("missing operation types for 3-step delivery chain")
		}

		pickID := createOp(pickTypeID, "internal", "draft", pickSrc, pickDest)
		packID := createOp(packTypeID, "internal", "waiting", packSrc, packDest)
		deliveryID := createOp(deliveryTypeID, "delivery", "waiting", delSrc, delDest)

		h.log.Info("Created 3-step delivery chain for SO",
			"so_id", orderID, "pick", pickID, "pack", packID, "delivery", deliveryID)

	case 2:
		// 2-step: Pick → Ship
		pickTypeID, pickSrc, pickDest := findOpType("pick")
		deliveryTypeID, delSrc, delDest := findOpType("delivery")

		if pickTypeID == uuid.Nil || deliveryTypeID == uuid.Nil {
			h.log.Warn("Missing operation types for 2-step delivery", "tenant_id", tenantID, "warehouse_id", warehouseUUID,
				"pick", pickTypeID, "delivery", deliveryTypeID)
			return fmt.Errorf("missing operation types for 2-step delivery chain")
		}

		pickID := createOp(pickTypeID, "internal", "draft", pickSrc, pickDest)
		deliveryID := createOp(deliveryTypeID, "delivery", "waiting", delSrc, delDest)

		h.log.Info("Created 2-step delivery chain for SO",
			"so_id", orderID, "pick", pickID, "delivery", deliveryID)

	default:
		// 1-step: Direct delivery
		deliveryTypeID, delSrc, delDest := findOpType("delivery")
		if deliveryTypeID == uuid.Nil {
			h.log.Warn("No delivery operation type found for SO", "tenant_id", tenantID, "warehouse_id", warehouseUUID)
			return fmt.Errorf("no delivery operation type found for warehouse")
		}
		deliveryID := createOp(deliveryTypeID, "delivery", "draft", delSrc, delDest)
		if deliveryID == uuid.Nil {
			return fmt.Errorf("failed to create delivery stock operation")
		}
		h.log.Info("Created 1-step delivery for SO", "so_id", orderID, "delivery", deliveryID)
	}
	return nil
}

// syncIntercompanySOConfirmToPO handles the case where a vendor confirms an intercompany SO.
// It finds the linked PO (that created this SO), updates it to confirmed, and creates
// a receipt stock op for the PO side (buyer).
func (h *Handler) syncIntercompanySOConfirmToPO(tenantID uuid.UUID, soID uuid.UUID, now time.Time) {
	// Check if this SO was created from a PO via intercompany link.
	// Link direction: source=purchase_order, linked=sale_order
	link, err := h.icSync.GetLinkedDocumentReverse(tenantID, "sale_order", soID)
	if err != nil || link == nil || link.SourceDocumentType != "purchase_order" {
		return // Not an intercompany SO created from a PO
	}

	linkedPOID := link.SourceDocumentID

	// Update the linked PO status to approved (shown as "Confirmed" in UI)
	h.db.Exec(`
		UPDATE purchase_orders SET status = 'approved', updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status = 'ordered'
	`, now, linkedPOID, tenantID)

	// Create receipt stock op for the linked PO (buyer side)
	h.createReceiptStockOpForPO(tenantID, linkedPOID, now)

	h.log.Info("Intercompany SO confirmed: updated linked PO to confirmed and created receipt op",
		"so_id", soID, "po_id", linkedPOID)
}

// autoCreateProductionOrders checks stock levels for each SO line and creates
// production orders for products where the ordered quantity exceeds available inventory.
func (h *Handler) autoCreateProductionOrders(tenantID, orderID, customerID uuid.UUID, warehouseID, organizationID *uuid.UUID, userID uuid.UUID, now time.Time) {
	// Fetch SO lines with product info and UOM, only for products with auto_manufacture enabled
	rows, err := h.db.Query(`
		SELECT sol.product_id, sol.quantity, p.name, COALESCE(u.code, 'unit') as uom_code
		FROM sales_order_lines sol
		JOIN products p ON p.id = sol.product_id
		LEFT JOIN units_of_measure u ON sol.unit_id = u.id
		WHERE sol.sales_order_id = $1
		  AND p.is_manufacturable = true
		  AND p.auto_manufacture = true
	`, orderID)
	if err != nil {
		h.log.Error("Auto MO: failed to fetch SO lines", "error", err)
		return
	}
	defer rows.Close()

	type soLine struct {
		ProductID uuid.UUID
		Quantity  float64
		Name      string
		UOM       string
	}

	var lines []soLine
	for rows.Next() {
		var l soLine
		if err := rows.Scan(&l.ProductID, &l.Quantity, &l.Name, &l.UOM); err != nil {
			h.log.Error("Auto MO: failed to scan SO line", "error", err)
			continue
		}
		lines = append(lines, l)
	}

	for _, line := range lines {
		// Check total available stock for this product across all warehouses
		var totalAvailable float64
		stockQuery := `
			SELECT COALESCE(SUM(quantity_available), 0)
			FROM inventory
			WHERE tenant_id = $1 AND product_id = $2
		`
		stockArgs := []interface{}{tenantID, line.ProductID}

		// If a specific warehouse is set, check only that warehouse
		if warehouseID != nil {
			stockQuery += " AND warehouse_id = $3"
			stockArgs = append(stockArgs, *warehouseID)
		}

		err := h.db.QueryRow(stockQuery, stockArgs...).Scan(&totalAvailable)
		if err != nil {
			h.log.Error("Auto MO: failed to check stock", "error", err, "product_id", line.ProductID)
			continue
		}

		deficit := line.Quantity - totalAvailable
		if deficit <= 0 {
			continue // Sufficient stock, no MO needed
		}

		// Create a production order for the deficit
		moID := uuid.New()
		var moCount int
		h.db.QueryRow("SELECT COUNT(*) FROM production_orders WHERE tenant_id = $1", tenantID).Scan(&moCount)
		moCode := fmt.Sprintf("MO%05d", moCount+1)
		moName := fmt.Sprintf("Auto: %s (SO)", line.Name)
		sourceType := "sales_order"

		_, err = h.db.Exec(`
			INSERT INTO production_orders (
				id, tenant_id, organization_id, code, name, product_id, quantity_planned, uom,
				current_stage, status, priority,
				source_type, source_id, sales_order_id, customer_id, warehouse_id,
				tags, created_by, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8,
				'draft', 'draft', 5,
				$9, $10, $10, $11, $12,
				'[]'::jsonb, $13, $14, $14
			)`,
			moID, tenantID, organizationID, moCode, moName, line.ProductID, deficit, line.UOM,
			sourceType, orderID, customerID, warehouseID,
			userID, now,
		)
		if err != nil {
			h.log.Error("Auto MO: failed to create production order",
				"error", err, "product_id", line.ProductID, "deficit", deficit)
			continue
		}

		// Find default BOM for this product and create work orders from its operations
		var bomID uuid.UUID
		var orgVal uuid.UUID
		if organizationID != nil {
			orgVal = *organizationID
		}
		bomErr := h.db.QueryRow(`
			SELECT id FROM product_boms
			WHERE product_id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND is_active = true
			ORDER BY is_default DESC, created_at DESC LIMIT 1
		`, line.ProductID, tenantID).Scan(&bomID)
		if bomErr == nil {
			if woErr := h.CreateWorkOrdersFromBOM(moID, bomID, tenantID, orgVal, deficit, userID); woErr != nil {
				h.log.Error("Auto MO: failed to create work orders from BOM", "error", woErr, "mo_id", moID)
			}
		}

		// If no BOM or BOM had no operations, create a default single work order
		var woCount int
		h.db.QueryRow(`SELECT COUNT(*) FROM work_orders WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, moID, tenantID).Scan(&woCount)
		if woCount == 0 {
			h.db.Exec(`
				INSERT INTO work_orders (
					id, tenant_id, organization_id, production_order_id, code, name,
					sequence, status, quantity_to_produce, uom, created_by, created_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,10,'pending',$7,$8,$9,$10,$10)`,
				uuid.New(), tenantID, organizationID, moID,
				fmt.Sprintf("%s-1", moCode),
				fmt.Sprintf("%s - Ishlab chiqarish", line.Name),
				deficit, line.UOM, userID, now,
			)
		}

		h.log.Info("Auto MO: created production order",
			"mo_code", moCode, "product", line.Name, "deficit", deficit, "sales_order_id", orderID)
	}
}

// CancelSalesOrder cancels a sales order
// CancelSalesOrder godoc
// @Summary Cancel a sales order
// @Description Cancel a sales order. Draft and confirmed orders can be cancelled. Delivered orders cannot be cancelled.
// @Tags Sales
// @Accept json
// @Produce json
// @Param id path string true "Sales Order ID (UUID)"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /sales/orders/{id}/cancel [post]
func (h *Handler) CancelSalesOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Check current status - can cancel draft or confirmed orders
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", orderID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if currentStatus == string(entity.OrderStatusCancelled) {
		response.BadRequest(c, "Order is already cancelled")
		return
	}
	if currentStatus == string(entity.OrderStatusDelivered) {
		response.BadRequest(c, "Cannot cancel delivered orders")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE sales_orders SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		entity.OrderStatusCancelled, now, orderID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to cancel sales order")
		return
	}

	h.GetSalesOrder(c)
}

// CreateInvoiceFromOrder creates an invoice from a sales order
// CreateInvoiceFromOrder godoc
// @Summary Create invoice from sales order
// @Description Generate a sales invoice from an existing sales order. The order must be in confirmed, processing, shipped, or delivered status. Automatically copies all order lines to the invoice.
// @Tags Sales
// @Accept json
// @Produce json
// @Param id path string true "Sales Order ID (UUID)"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /sales/orders/{id}/invoice [post]
func (h *Handler) CreateInvoiceFromOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	// Get order details with customer name
	var customerID uuid.UUID
	var organizationID *uuid.UUID
	var orderNumber string
	var subtotal, discountAmount, taxAmount, shippingAmount, totalAmount float64
	var paymentTerms int
	var orderPaymentTermID sql.NullString
	var billingAddress, shippingAddress string
	var currentStatus string
	var customerName string

	err = h.db.QueryRow(`
		SELECT so.customer_id, so.organization_id, so.order_number, COALESCE(so.subtotal, 0), COALESCE(so.discount_amount, 0), COALESCE(so.tax_amount, 0), COALESCE(so.shipping_amount, 0), COALESCE(so.total_amount, 0),
		       COALESCE(so.payment_terms, 30), so.payment_term_id, COALESCE(so.billing_address::text, ''), COALESCE(so.shipping_address::text, ''), so.status,
		       COALESCE(c.name, '')
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.id = $1 AND so.tenant_id = $2 AND so.deleted_at IS NULL`,
		orderID, tenantID).Scan(
		&customerID, &organizationID, &orderNumber, &subtotal, &discountAmount, &taxAmount, &shippingAmount, &totalAmount,
		&paymentTerms, &orderPaymentTermID, &billingAddress, &shippingAddress, &currentStatus, &customerName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch sales order", "error", err, "order_id", orderID)
		h.log.Error("Failed to fetch sales order", "error", err)
		response.InternalError(c, "Failed to fetch sales order")
		return
	}

	h.log.Info("CreateInvoiceFromOrder: order status check", "status", currentStatus, "orderID", orderID)
	// Allow invoice creation from confirmed, processing, shipped, or delivered orders
	allowedStatuses := map[string]bool{
		string(entity.OrderStatusConfirmed):  true,
		string(entity.OrderStatusProcessing): true,
		string(entity.OrderStatusShipped):    true,
		string(entity.OrderStatusDelivered):  true,
	}
	if !allowedStatuses[currentStatus] {
		response.BadRequest(c, "Can only create invoice from confirmed, processing, shipped, or delivered orders")
		return
	}

	// Check if an invoice already exists for this order (prevent duplicates)
	var existingInvoiceCount int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM sales_invoices
		WHERE sales_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND status != 'cancelled'`,
		orderID, tenantID).Scan(&existingInvoiceCount)
	if existingInvoiceCount > 0 {
		response.BadRequest(c, "An invoice already exists for this order")
		return
	}

	invoiceID := uuid.New()
	invoiceNumber := "INV-" + time.Now().Format("20060102") + "-" + uuid.New().String()[:6]
	now := time.Now()

	// Resolve payment term and calculate due date
	// Priority: order's payment_term_id > tenant settings default > fallback to integer days
	var dueDate time.Time
	var invoicePaymentTermID *uuid.UUID
	var earlyDiscountAmount float64
	var earlyDiscountDate *time.Time

	// Helper to apply a found payment term
	applyPaymentTerm := func(pt *entity.PaymentTerm) {
		dueDate = pt.CalculateDueDate(now)
		invoicePaymentTermID = &pt.ID
		earlyDiscountDate = pt.CalculateEarlyDiscountDate(now)
		earlyDiscountAmount = pt.CalculateDiscountAmount(totalAmount)
	}

	ptResolved := false

	// 1. Try order's payment_term_id
	if orderPaymentTermID.Valid {
		ptID, ptErr := uuid.Parse(orderPaymentTermID.String)
		if ptErr == nil {
			var pt entity.PaymentTerm
			ptQueryErr := h.db.QueryRow(`
				SELECT id, tenant_id, code, name, description, term_type, due_days,
				       has_early_discount, discount_percentage, discount_days,
				       end_of_month_day, display_order, is_default, is_active,
				       created_by, created_at, updated_at
				FROM payment_terms
				WHERE id = $1 AND tenant_id = $2
			`, ptID, tenantID).Scan(
				&pt.ID, &pt.TenantID, &pt.Code, &pt.Name, &pt.Description,
				&pt.TermType, &pt.DueDays, &pt.HasEarlyDiscount, &pt.DiscountPercentage,
				&pt.DiscountDays, &pt.EndOfMonthDay, &pt.DisplayOrder, &pt.IsDefault,
				&pt.IsActive, &pt.CreatedBy, &pt.CreatedAt, &pt.UpdatedAt,
			)
			if ptQueryErr == nil {
				applyPaymentTerm(&pt)
				ptResolved = true
			}
		}
	}

	// 2. Try default payment term from tenant settings (e.g. "Net 30")
	if !ptResolved {
		var settingsJSON []byte
		settingsErr := h.db.QueryRow(
			`SELECT settings FROM tenant_settings WHERE tenant_id = $1`, tenantID,
		).Scan(&settingsJSON)
		if settingsErr == nil && len(settingsJSON) > 0 {
			var settings map[string]interface{}
			if json.Unmarshal(settingsJSON, &settings) == nil {
				// Navigate: sales -> payment -> default_terms
				if salesMap, ok := settings["sales"].(map[string]interface{}); ok {
					if paymentMap, ok := salesMap["payment"].(map[string]interface{}); ok {
						if defaultTermName, ok := paymentMap["default_terms"].(string); ok && defaultTermName != "" {
							// Look up payment term by name (case-insensitive)
							var pt entity.PaymentTerm
							ptQueryErr := h.db.QueryRow(`
								SELECT id, tenant_id, code, name, description, term_type, due_days,
								       has_early_discount, discount_percentage, discount_days,
								       end_of_month_day, display_order, is_default, is_active,
								       created_by, created_at, updated_at
								FROM payment_terms
								WHERE tenant_id = $1 AND LOWER(name) = LOWER($2) AND is_active = true
								LIMIT 1
							`, tenantID, defaultTermName).Scan(
								&pt.ID, &pt.TenantID, &pt.Code, &pt.Name, &pt.Description,
								&pt.TermType, &pt.DueDays, &pt.HasEarlyDiscount, &pt.DiscountPercentage,
								&pt.DiscountDays, &pt.EndOfMonthDay, &pt.DisplayOrder, &pt.IsDefault,
								&pt.IsActive, &pt.CreatedBy, &pt.CreatedAt, &pt.UpdatedAt,
							)
							if ptQueryErr == nil {
								applyPaymentTerm(&pt)
								ptResolved = true
							} else {
								h.log.Info("CreateInvoiceFromOrder: default payment term from settings not found", "name", defaultTermName)
							}
						}
					}
				}
			}
		}
	}

	// 3. Fallback: use the old integer payment_terms field from the order
	if !ptResolved {
		dueDate = now.AddDate(0, 0, paymentTerms)
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Convert addresses to interface{} for JSONB - nil for empty, otherwise the JSON string
	var billingAddrParam, shippingAddrParam interface{}
	if billingAddress != "" && billingAddress != "{}" {
		billingAddrParam = []byte(billingAddress)
	}
	if shippingAddress != "" && shippingAddress != "{}" {
		shippingAddrParam = []byte(shippingAddress)
	}

	// Use a transaction for invoice creation + GL posting
	tx, txErr := h.db.Begin()
	if txErr != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Create invoice (note: amount_due is a GENERATED column, don't insert into it)
	h.log.Info("CreateInvoiceFromOrder: attempting INSERT",
		"invoiceID", invoiceID,
		"tenantID", tenantID,
		"invoiceNumber", invoiceNumber,
		"customerID", customerID,
		"orderID", orderID)

	_, err = tx.Exec(`
		INSERT INTO sales_invoices (
			id, tenant_id, organization_id, invoice_number, customer_id, customer_name, sales_order_id,
			invoice_date, due_date, billing_address, shipping_address,
			exchange_rate, subtotal, discount_amount, tax_amount, total_amount,
			amount_paid, status, payment_term_id, early_discount_amount, early_discount_date,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`,
		invoiceID, tenantID, organizationID, invoiceNumber, customerID, customerName, orderID,
		now, dueDate, billingAddrParam, shippingAddrParam,
		1.0, subtotal, discountAmount, taxAmount, totalAmount,
		0, entity.InvoiceStatusSent, invoicePaymentTermID, earlyDiscountAmount, earlyDiscountDate,
		createdBy, now, now,
	)
	if err != nil {
		h.log.Error("CreateInvoiceFromOrder: INSERT failed", "error", err)
		h.log.Error("Failed to create invoice", "error", err)
		response.InternalError(c, "Failed to create invoice")
		return
	}
	h.log.Info("CreateInvoiceFromOrder: INSERT succeeded")

	// Copy order lines to invoice lines (join with products to get name if description is null)
	type invoiceLineInfo struct {
		OrderLineID uuid.UUID
		LineNumber  int
		ProductID   uuid.UUID
		Description sql.NullString
		Quantity    float64
		UnitID      sql.NullString
		UnitPrice   float64
		Discount    float64
		TaxID       sql.NullString
		TaxAmount   float64
		LineTotal   float64
	}
	var invoiceLineInfos []invoiceLineInfo

	linesRows, err := tx.Query(`
		SELECT sol.id, sol.line_number, sol.product_id, COALESCE(NULLIF(sol.description, ''), p.name) as description,
		       sol.quantity, sol.unit_id, sol.unit_price, sol.discount_amount, sol.tax_id, sol.tax_amount, sol.line_total
		FROM sales_order_lines sol
		LEFT JOIN products p ON p.id = sol.product_id
		WHERE sol.sales_order_id = $1`, orderID)
	if err != nil {
		h.log.Error("CreateInvoiceFromOrder: query order lines failed", "error", err)
	} else {
		for linesRows.Next() {
			var li invoiceLineInfo
			if scanErr := linesRows.Scan(&li.OrderLineID, &li.LineNumber, &li.ProductID, &li.Description, &li.Quantity, &li.UnitID, &li.UnitPrice,
				&li.Discount, &li.TaxID, &li.TaxAmount, &li.LineTotal); scanErr != nil {
				h.log.Error("CreateInvoiceFromOrder: scan order line failed", "error", scanErr)
				continue
			}
			invoiceLineInfos = append(invoiceLineInfos, li)
		}
		linesRows.Close()
	}
	h.log.Info("CreateInvoiceFromOrder: order lines fetched", "count", len(invoiceLineInfos))

	for _, li := range invoiceLineInfos {
		invoiceLineID := uuid.New()
		_, lineErr := tx.Exec(`
			INSERT INTO sales_invoice_lines (
				id, sales_invoice_id, sales_order_line_id, line_number, product_id, description,
				quantity, unit_id, unit_price, discount_amount, tax_id, tax_amount, line_total, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			invoiceLineID, invoiceID, li.OrderLineID, li.LineNumber, li.ProductID, li.Description,
			li.Quantity, li.UnitID, li.UnitPrice, li.Discount, li.TaxID, li.TaxAmount, li.LineTotal, now,
		)
		if lineErr != nil {
			h.log.Error("CreateInvoiceFromOrder: invoice line INSERT failed", "error", lineErr, "line_number", li.LineNumber, "product_id", li.ProductID)
		}
	}

	// ========== GL JOURNAL ENTRY POSTING (auto-post on creation) ==========
	var journalEntryID *uuid.UUID

	// Get Sales Journal
	var salesJournalID uuid.UUID
	var numberPrefix sql.NullString
	journalErr := tx.QueryRow(`
		SELECT id, number_prefix
		FROM journals WHERE tenant_id = $1 AND code IN ('SALES', 'SAL') AND deleted_at IS NULL`,
		tenantID,
	).Scan(&salesJournalID, &numberPrefix)

	if journalErr == nil {
		// Find AR account
		arAccountID := findAccount(tx, tenantID, organizationID, "accounts receivable", "4010")

		if arAccountID != uuid.Nil {
			taxAccountID := findAccount(tx, tenantID, organizationID, "tax", "6990")

			// Get invoice lines for per-category accounting
			type invoiceLineAcct struct {
				ProductID  uuid.UUID
				LineTotal  float64
				Quantity   float64
				CostPrice  float64
				IncomeAcct uuid.UUID
				ExpenseAcct uuid.UUID
				OutputAcct uuid.UUID
			}
			var acctLines []invoiceLineAcct
			acctRows, acctErr := tx.Query(`
				SELECT sil.product_id, COALESCE(sil.line_total, 0), COALESCE(sil.quantity, 0), COALESCE(p.cost_price, 0)
				FROM sales_invoice_lines sil
				JOIN products p ON sil.product_id = p.id
				WHERE sil.sales_invoice_id = $1
			`, invoiceID)
			if acctErr != nil {
				h.log.Error("CreateInvoiceFromOrder: query invoice lines for acct failed", "error", acctErr)
			}
			if acctErr == nil {
				for acctRows.Next() {
					var al invoiceLineAcct
					if err := acctRows.Scan(&al.ProductID, &al.LineTotal, &al.Quantity, &al.CostPrice); err == nil {
						acctLines = append(acctLines, al)
					}
				}
				acctRows.Close()
				for i := range acctLines {
					ca := getCategoryAccounts(tx, tenantID, organizationID, acctLines[i].ProductID)
					acctLines[i].IncomeAcct = ca.IncomeAccountID
					acctLines[i].ExpenseAcct = ca.ExpenseAccountID
					acctLines[i].OutputAcct = ca.StockOutputAccountID
				}
			}

			// Group revenue by income account
			revenueGrouped := make(map[uuid.UUID]float64)
			type cogsPair struct {
				Expense uuid.UUID
				Output  uuid.UUID
			}
			cogsGrouped := make(map[cogsPair]float64)

			// Resolve fallback revenue account for products without category income account
			fallbackRevenue := findAccount(tx, tenantID, organizationID, "sales revenue", "9010")

			for _, al := range acctLines {
				if al.LineTotal > 0 {
					if al.IncomeAcct != uuid.Nil {
						revenueGrouped[al.IncomeAcct] += al.LineTotal
					} else if fallbackRevenue != uuid.Nil {
						revenueGrouped[fallbackRevenue] += al.LineTotal
					}
				}
				costAmount := al.Quantity * al.CostPrice
				if costAmount > 0 && al.ExpenseAcct != uuid.Nil && al.OutputAcct != uuid.Nil {
					cogsGrouped[cogsPair{Expense: al.ExpenseAcct, Output: al.OutputAcct}] += costAmount
				}
			}

			// Fallback if no lines at all but subtotal exists
			if len(revenueGrouped) == 0 && subtotal > 0 && fallbackRevenue != uuid.Nil {
				revenueGrouped[fallbackRevenue] = subtotal
			}

			// Generate unique entry number using UUID suffix (guaranteed unique)
			prefix := "INV-"
			if numberPrefix.Valid && numberPrefix.String != "" {
				prefix = numberPrefix.String
			}
			entryNumber := fmt.Sprintf("%s%s-%s", prefix, now.Format("20060102"), uuid.New().String()[:6])

			// Total COGS
			var totalCogs float64
			for _, amt := range cogsGrouped {
				totalCogs += amt
			}
			totalDebit := totalAmount + totalCogs
			totalCredit := totalDebit

			// Create journal entry
			jeID := uuid.New()
			journalEntryID = &jeID
			jeDescription := fmt.Sprintf("Sales Invoice %s", invoiceNumber)

			var createdBy *uuid.UUID
			if userID != uuid.Nil {
				createdBy = &userID
			}
			_, jeErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
					source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16)`,
				jeID, tenantID, organizationID, salesJournalID, entryNumber, now, invoiceNumber, jeDescription,
				"sales_invoice", invoiceID, 1.0, totalDebit, totalCredit, createdBy, now, now,
			)
			if jeErr != nil {
				h.log.Error("CreateInvoiceFromOrder: journal entry INSERT failed, skipping GL posting", "error", jeErr)
			}

			jeLineNumber := 1
			if jeErr == nil {

			// Debit: Accounts Receivable
			_, arErr := tx.Exec(`
				INSERT INTO journal_entry_lines (
					id, journal_entry_id, line_number, account_id, contact_id, description,
					debit_amount, credit_amount, exchange_rate, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				uuid.New(), jeID, jeLineNumber, arAccountID, customerID, "Accounts Receivable",
				totalAmount, 0.0, 1.0, now,
			)
			if arErr != nil {
				h.log.Error("CreateInvoiceFromOrder: AR journal line failed", "error", arErr, "arAccountID", arAccountID)
			}
			if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalAmount, now, arAccountID); err != nil {
				h.log.Error("CreateInvoiceFromOrder: update AR account balance failed", "error", err, "arAccountID", arAccountID)
			}
			jeLineNumber++

			// Credit: Revenue per category
			for incomeAcct, amount := range revenueGrouped {
				if _, err := tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					uuid.New(), jeID, jeLineNumber, incomeAcct, "Sales Revenue",
					0.0, amount, 1.0, now,
				); err != nil {
					h.log.Error("CreateInvoiceFromOrder: revenue journal line failed", "error", err, "incomeAcct", incomeAcct, "amount", amount)
				}
				if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", amount, now, incomeAcct); err != nil {
					h.log.Error("CreateInvoiceFromOrder: update revenue account balance failed", "error", err, "incomeAcct", incomeAcct)
				}
				jeLineNumber++
			}

			// Credit: Tax Payable
			if taxAccountID != uuid.Nil && taxAmount > 0 {
				if _, err := tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					uuid.New(), jeID, jeLineNumber, taxAccountID, "Sales Tax Payable",
					0.0, taxAmount, 1.0, now,
				); err != nil {
					h.log.Error("CreateInvoiceFromOrder: tax payable journal line failed", "error", err, "taxAccountID", taxAccountID)
				}
				if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", taxAmount, now, taxAccountID); err != nil {
					h.log.Error("CreateInvoiceFromOrder: update tax account balance failed", "error", err, "taxAccountID", taxAccountID)
				}
				jeLineNumber++
			}

			// COGS entries
			for pair, costAmount := range cogsGrouped {
				// Debit: COGS
				if _, err := tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					uuid.New(), jeID, jeLineNumber, pair.Expense, "Cost of Goods Sold",
					costAmount, 0.0, 1.0, now,
				); err != nil {
					h.log.Error("CreateInvoiceFromOrder: COGS journal line failed", "error", err, "expenseAcct", pair.Expense)
				}
				if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", costAmount, now, pair.Expense); err != nil {
					h.log.Error("CreateInvoiceFromOrder: update COGS account balance failed", "error", err, "expenseAcct", pair.Expense)
				}
				jeLineNumber++

				// Credit: Stock Interim Delivery
				if _, err := tx.Exec(`
					INSERT INTO journal_entry_lines (
						id, journal_entry_id, line_number, account_id, description,
						debit_amount, credit_amount, exchange_rate, created_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					uuid.New(), jeID, jeLineNumber, pair.Output, "Stock Interim Delivery",
					0.0, costAmount, 1.0, now,
				); err != nil {
					h.log.Error("CreateInvoiceFromOrder: stock output journal line failed", "error", err, "outputAcct", pair.Output)
				}
				if _, err := tx.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", costAmount, now, pair.Output); err != nil {
					h.log.Error("CreateInvoiceFromOrder: update stock output balance failed", "error", err, "outputAcct", pair.Output)
				}
				jeLineNumber++
			}

			// Link JE to invoice
			if _, err := tx.Exec("UPDATE sales_invoices SET journal_entry_id = $1, sent_at = $2 WHERE id = $3", jeID, now, invoiceID); err != nil {
				h.log.Error("CreateInvoiceFromOrder: link JE to invoice failed", "error", err)
			}
			} // end if jeErr == nil
		}
	}

	// Update order status to processing
	if _, err := tx.Exec("UPDATE sales_orders SET status = $1, updated_at = $2 WHERE id = $3",
		entity.OrderStatusProcessing, now, orderID); err != nil {
		h.log.Error("CreateInvoiceFromOrder: update order status failed", "error", err)
	}

	// Commit
	if err := tx.Commit(); err != nil {
		h.log.Error("CreateInvoiceFromOrder: commit failed", "error", err)
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	invoiceStatus := string(entity.InvoiceStatusSent)
	resp := map[string]interface{}{
		"id":                    invoiceID.String(),
		"invoice_number":        invoiceNumber,
		"customer_id":           customerID.String(),
		"customer_name":         customerName,
		"sales_order_id":        orderID.String(),
		"invoice_date":          now.Format("2006-01-02"),
		"due_date":              dueDate.Format("2006-01-02"),
		"total_amount":          totalAmount,
		"amount_due":            totalAmount,
		"status":                invoiceStatus,
		"journal_entry_id":      journalEntryID,
		"payment_term_id":       invoicePaymentTermID,
		"early_discount_amount": earlyDiscountAmount,
		"created_at":            now,
	}
	if earlyDiscountDate != nil {
		resp["early_discount_date"] = earlyDiscountDate.Format("2006-01-02")
	}
	response.Created(c, resp)
}
