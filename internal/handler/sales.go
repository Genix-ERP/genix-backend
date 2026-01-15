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
)

// ListSalesOrders returns paginated list of sales orders
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
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Build query with filters - JOIN with contacts to get customer_name
	baseQuery := `
		SELECT so.id, so.tenant_id, so.organization_id, so.order_number, so.customer_id, so.contact_person_id,
			   so.order_date, so.expected_date, so.billing_address, so.shipping_address,
			   so.currency_id, so.exchange_rate, so.subtotal, so.discount_type, so.discount_value, so.discount_amount,
			   so.tax_amount, so.shipping_amount, so.total_amount, so.status, so.payment_status, so.payment_terms,
			   so.reference, so.po_number, so.notes, so.internal_notes, so.warehouse_id, so.sales_rep_id,
			   so.approved_by, so.approved_at, so.created_by, so.created_at, so.updated_at,
			   COALESCE(c.name, '') as customer_name
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.tenant_id = $1 AND so.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM sales_orders so WHERE so.tenant_id = $1 AND so.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

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
		response.InternalError(c, "Failed to fetch sales orders: "+err.Error())
		return
	}
	defer rows.Close()

	var orders []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan, customerID uuid.UUID
		var organizationID, contactPersonID, currencyID, warehouseID, salesRepID, approvedBy, createdBy sql.NullString
		var orderNumber, customerName string
		var orderDate time.Time
		var expectedDate, approvedAt sql.NullTime
		var billingAddress, shippingAddress []byte
		var exchangeRate, subtotal, discountValue, discountAmount, taxAmount, shippingAmount, totalAmount float64
		var discountType, status, paymentStatus, reference, poNumber, notes, internalNotes sql.NullString
		var paymentTerms int
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &tenantIDScan, &organizationID, &orderNumber, &customerID, &contactPersonID,
			&orderDate, &expectedDate, &billingAddress, &shippingAddress,
			&currencyID, &exchangeRate, &subtotal, &discountType, &discountValue, &discountAmount,
			&taxAmount, &shippingAmount, &totalAmount, &status, &paymentStatus, &paymentTerms,
			&reference, &poNumber, &notes, &internalNotes, &warehouseID, &salesRepID,
			&approvedBy, &approvedAt, &createdBy, &createdAt, &updatedAt,
			&customerName,
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
		if warehouseID.Valid {
			order["warehouse_id"] = warehouseID.String
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
	ShippingAmount  float64                             `json:"shipping_amount,omitempty"`
	ShippingCost    float64                             `json:"shipping_cost,omitempty"`
	PaymentTerms    int                                 `json:"payment_terms,omitempty"`
	Reference       string                              `json:"reference,omitempty"`
	PONumber        string                              `json:"po_number,omitempty"`
	Notes           string                              `json:"notes,omitempty"`
	InternalNotes   string                              `json:"internal_notes,omitempty"`
	WarehouseID     string                              `json:"warehouse_id,omitempty"`
	SalesRepID      string                              `json:"sales_rep_id,omitempty"`
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
func (h *Handler) CreateSalesOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input SimpleSalesOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
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

	// Generate order number
	orderNumber := input.OrderNumber
	if orderNumber == "" {
		orderNumber = "SO-" + time.Now().Format("20060102") + "-" + uuid.New().String()[:6]
	}

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
		// Apply order-level discount
		if input.DiscountType == "percentage" && input.DiscountValue > 0 {
			discountAmount = subtotal * input.DiscountValue / 100
		} else if input.DiscountType == "fixed" && input.DiscountValue > 0 {
			discountAmount = input.DiscountValue
		}
		totalAmount = subtotal - discountAmount + taxAmount + shippingAmount
	} else {
		// Use provided totals from frontend
		subtotal = input.Subtotal
		taxAmount = input.TaxAmount
		totalAmount = input.TotalAmount
		if totalAmount == 0 {
			totalAmount = subtotal + taxAmount + shippingAmount
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

	// Insert sales order
	query := `
		INSERT INTO sales_orders (
			id, tenant_id, order_number, customer_id, contact_person_id,
			order_date, expected_date, billing_address, shipping_address,
			currency_id, exchange_rate, subtotal, discount_type, discount_value, discount_amount,
			tax_amount, shipping_amount, total_amount, status, payment_status, payment_terms,
			reference, po_number, notes, internal_notes, warehouse_id, sales_rep_id,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30)`

	_, err = h.db.Exec(query,
		orderID, tenantID, orderNumber, customerID, contactPersonID,
		orderDate, expectedDate, billingAddressJSON, shippingAddressJSON,
		currencyID, 1.0, subtotal, input.DiscountType, input.DiscountValue, discountAmount,
		taxAmount, input.ShippingAmount, totalAmount, entity.OrderStatusDraft, entity.PaymentStatusUnpaid, input.PaymentTerms,
		input.Reference, input.PONumber, input.Notes, input.InternalNotes, warehouseID, salesRepID,
		createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create sales order", "error", err, "customer_id", customerID, "order_number", orderNumber)
		response.InternalError(c, "Failed to create sales order: "+err.Error())
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

		var unitID, taxID, lineWarehouseID *uuid.UUID
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

		lineQuery := `
			INSERT INTO sales_order_lines (
				id, sales_order_id, line_number, product_id, description,
				quantity, unit_id, unit_price, discount_type, discount_value, discount_amount,
				tax_id, tax_amount, line_total, quantity_delivered, quantity_invoiced,
				warehouse_id, notes, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`

		h.db.Exec(lineQuery,
			lineID, orderID, i+1, productID, line.Description,
			line.Quantity, unitID, line.UnitPrice, line.DiscountType, line.DiscountValue, lineDiscount,
			taxID, 0.0, lineTotal-lineDiscount, 0.0, 0.0,
			lineWarehouseID, line.Notes, now, now,
		)
	}

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
			   so.reference, so.po_number, so.notes, so.internal_notes, so.warehouse_id, so.sales_rep_id,
			   so.approved_by, so.approved_at, so.created_by, so.created_at, so.updated_at,
			   COALESCE(c.name, '') as customer_name
		FROM sales_orders so
		LEFT JOIN contacts c ON so.customer_id = c.id
		WHERE so.id = $1 AND so.tenant_id = $2 AND so.deleted_at IS NULL`

	var id, tenantIDScan, customerID uuid.UUID
	var organizationID, contactPersonID, currencyID, warehouseID, salesRepID, approvedBy, createdBy sql.NullString
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
		&reference, &poNumber, &notes, &internalNotes, &warehouseID, &salesRepID,
		&approvedBy, &approvedAt, &createdBy, &createdAt, &updatedAt,
		&customerName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch sales order: "+err.Error())
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
	if salesRepID.Valid {
		order["sales_rep_id"] = salesRepID.String
	}

	// Get order lines
	linesQuery := `
		SELECT id, line_number, product_id, description, quantity, unit_id, unit_price,
			   discount_type, discount_value, discount_amount, tax_id, tax_amount, line_total,
			   quantity_delivered, quantity_invoiced, warehouse_id, notes
		FROM sales_order_lines
		WHERE sales_order_id = $1
		ORDER BY line_number`

	linesRows, err := h.db.Query(linesQuery, orderID)
	if err == nil {
		defer linesRows.Close()
		var lines []map[string]interface{}
		for linesRows.Next() {
			var lineID, productID uuid.UUID
			var lineNumber int
			var description, lineDiscountType, lineNotes sql.NullString
			var quantity, unitPrice, lineDiscountValue, lineDiscountAmount, lineTaxAmount, lineTotal, qtyDelivered, qtyInvoiced float64
			var unitID, taxID, lineWarehouseID sql.NullString

			err := linesRows.Scan(
				&lineID, &lineNumber, &productID, &description, &quantity, &unitID, &unitPrice,
				&lineDiscountType, &lineDiscountValue, &lineDiscountAmount, &taxID, &lineTaxAmount, &lineTotal,
				&qtyDelivered, &qtyInvoiced, &lineWarehouseID, &lineNotes,
			)
			if err != nil {
				continue
			}

			line := map[string]interface{}{
				"id":                 lineID.String(),
				"line_number":        lineNumber,
				"product_id":         productID.String(),
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

			lines = append(lines, line)
		}
		order["lines"] = lines
	}

	response.Success(c, order)
}

// UpdateSalesOrder updates an existing sales order
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
		response.BadRequest(c, err.Error())
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
		input.InternalNotes == nil && input.WarehouseID == nil && input.SalesRepID == nil &&
		input.PaymentStatus == nil

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
		response.InternalError(c, "Failed to update sales order: "+err.Error())
		return
	}

	// Fetch and return updated order
	h.GetSalesOrder(c)
}

// DeleteSalesOrder soft deletes a sales order
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
	if currentStatus != string(entity.OrderStatusDraft) {
		response.BadRequest(c, "Can only delete orders in draft status")
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

// ConfirmSalesOrder confirms a draft sales order
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

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM sales_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", orderID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if currentStatus != string(entity.OrderStatusDraft) {
		response.BadRequest(c, "Can only confirm orders in draft status")
		return
	}

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE sales_orders SET status = $1, approved_by = $2, approved_at = $3, updated_at = $4 WHERE id = $5 AND tenant_id = $6",
		entity.OrderStatusConfirmed, userID, now, now, orderID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to confirm sales order")
		return
	}

	h.GetSalesOrder(c)
}

// CancelSalesOrder cancels a sales order
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

	// Get order details
	var customerID uuid.UUID
	var orderNumber string
	var subtotal, discountAmount, taxAmount, shippingAmount, totalAmount float64
	var paymentTerms int
	var billingAddress, shippingAddress []byte
	var currentStatus string

	err = h.db.QueryRow(`
		SELECT customer_id, order_number, subtotal, discount_amount, tax_amount, shipping_amount, total_amount,
		       payment_terms, billing_address, shipping_address, status
		FROM sales_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		orderID, tenantID).Scan(
		&customerID, &orderNumber, &subtotal, &discountAmount, &taxAmount, &shippingAmount, &totalAmount,
		&paymentTerms, &billingAddress, &shippingAddress, &currentStatus,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Sales order")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch sales order")
		return
	}

	if currentStatus != string(entity.OrderStatusConfirmed) && currentStatus != string(entity.OrderStatusProcessing) {
		response.BadRequest(c, "Can only create invoice from confirmed or processing orders")
		return
	}

	invoiceID := uuid.New()
	invoiceNumber := "INV-" + time.Now().Format("20060102") + "-" + uuid.New().String()[:6]
	now := time.Now()
	dueDate := now.AddDate(0, 0, paymentTerms)

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Create invoice
	_, err = h.db.Exec(`
		INSERT INTO sales_invoices (
			id, tenant_id, invoice_number, customer_id, sales_order_id,
			invoice_date, due_date, billing_address, shipping_address,
			exchange_rate, subtotal, discount_amount, tax_amount, total_amount,
			amount_paid, amount_due, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		invoiceID, tenantID, invoiceNumber, customerID, orderID,
		now, dueDate, billingAddress, shippingAddress,
		1.0, subtotal, discountAmount, taxAmount, totalAmount,
		0, totalAmount, entity.InvoiceStatusDraft, createdBy, now, now,
	)
	if err != nil {
		response.InternalError(c, "Failed to create invoice: "+err.Error())
		return
	}

	// Copy order lines to invoice lines
	linesRows, err := h.db.Query(`
		SELECT id, line_number, product_id, description, quantity, unit_id, unit_price,
		       discount_amount, tax_id, tax_amount, line_total
		FROM sales_order_lines WHERE sales_order_id = $1`, orderID)
	if err == nil {
		defer linesRows.Close()
		for linesRows.Next() {
			var orderLineID, productID uuid.UUID
			var lineNumber int
			var description sql.NullString
			var quantity, unitPrice, lineDiscountAmount, lineTaxAmount, lineTotal float64
			var unitID, taxID sql.NullString

			if err := linesRows.Scan(&orderLineID, &lineNumber, &productID, &description, &quantity, &unitID, &unitPrice,
				&lineDiscountAmount, &taxID, &lineTaxAmount, &lineTotal); err != nil {
				continue
			}

			invoiceLineID := uuid.New()
			h.db.Exec(`
				INSERT INTO sales_invoice_lines (
					id, sales_invoice_id, sales_order_line_id, line_number, product_id, description,
					quantity, unit_id, unit_price, discount_amount, tax_id, tax_amount, line_total, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
				invoiceLineID, invoiceID, orderLineID, lineNumber, productID, description,
				quantity, unitID, unitPrice, lineDiscountAmount, taxID, lineTaxAmount, lineTotal, now,
			)
		}
	}

	// Update order status to processing
	h.db.Exec("UPDATE sales_orders SET status = $1, updated_at = $2 WHERE id = $3",
		entity.OrderStatusProcessing, now, orderID)

	response.Created(c, map[string]interface{}{
		"id":             invoiceID.String(),
		"invoice_number": invoiceNumber,
		"customer_id":    customerID.String(),
		"sales_order_id": orderID.String(),
		"invoice_date":   now.Format("2006-01-02"),
		"due_date":       dueDate.Format("2006-01-02"),
		"total_amount":   totalAmount,
		"amount_due":     totalAmount,
		"status":         string(entity.InvoiceStatusDraft),
		"created_at":     now,
	})
}
