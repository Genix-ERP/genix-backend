package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================
// POS CONFIG TYPES
// ============================================

type POSConfig struct {
	ID                     uuid.UUID  `json:"id"`
	TenantID               uuid.UUID  `json:"tenant_id"`
	Name                   string     `json:"name"`
	WarehouseID            *uuid.UUID `json:"warehouse_id,omitempty"`
	WarehouseName          string     `json:"warehouse_name,omitempty"`
	ReceiptHeader          string     `json:"receipt_header,omitempty"`
	ReceiptFooter          string     `json:"receipt_footer,omitempty"`
	DefaultPaymentMethodID *uuid.UUID `json:"default_payment_method_id,omitempty"`
	IsActive               bool       `json:"is_active"`
	AllowDiscount          bool       `json:"allow_discount"`
	MaxDiscountPercent     float64    `json:"max_discount_percent"`
	RequireCustomer        bool       `json:"require_customer"`
	DefaultTaxRate         float64    `json:"default_tax_rate"`
	PricesIncludeTax       bool       `json:"prices_include_tax"`
	UpdateStock            bool       `json:"update_stock"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type POSConfigInput struct {
	Name                   string     `json:"name" binding:"required"`
	WarehouseID            *uuid.UUID `json:"warehouse_id,omitempty"`
	ReceiptHeader          string     `json:"receipt_header,omitempty"`
	ReceiptFooter          string     `json:"receipt_footer,omitempty"`
	DefaultPaymentMethodID *uuid.UUID `json:"default_payment_method_id,omitempty"`
	IsActive               *bool      `json:"is_active,omitempty"`
	AllowDiscount          *bool      `json:"allow_discount,omitempty"`
	MaxDiscountPercent     *float64   `json:"max_discount_percent,omitempty"`
	RequireCustomer        *bool      `json:"require_customer,omitempty"`
	DefaultTaxRate         *float64   `json:"default_tax_rate,omitempty"`
	PricesIncludeTax       *bool      `json:"prices_include_tax,omitempty"`
	UpdateStock            *bool      `json:"update_stock,omitempty"`
}

// ============================================
// POS SESSION TYPES
// ============================================

type POSSession struct {
	ID                 uuid.UUID  `json:"id"`
	TenantID           uuid.UUID  `json:"tenant_id"`
	POSConfigID        uuid.UUID  `json:"pos_config_id"`
	POSConfigName      string     `json:"pos_config_name,omitempty"`
	SessionNumber      string     `json:"session_number"`
	UserID             uuid.UUID  `json:"user_id"`
	UserName           string     `json:"user_name,omitempty"`
	OpenedAt           time.Time  `json:"opened_at"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
	OpeningBalance     float64    `json:"opening_balance"`
	ClosingBalance     *float64   `json:"closing_balance,omitempty"`
	ExpectedBalance    *float64   `json:"expected_balance,omitempty"`
	Difference         *float64   `json:"difference,omitempty"`
	TotalSales         float64    `json:"total_sales"`
	TotalRefunds       float64    `json:"total_refunds"`
	TotalCashPayments  float64    `json:"total_cash_payments"`
	TotalCardPayments  float64    `json:"total_card_payments"`
	TotalOtherPayments float64    `json:"total_other_payments"`
	OrderCount         int        `json:"order_count"`
	Status             string     `json:"status"`
	Notes              string     `json:"notes,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

type OpenSessionInput struct {
	POSConfigID    uuid.UUID `json:"pos_config_id" binding:"required"`
	OpeningBalance float64   `json:"opening_balance"`
}

type CloseSessionInput struct {
	ClosingBalance float64 `json:"closing_balance" binding:"required"`
	Notes          string  `json:"notes,omitempty"`
}

// ============================================
// POS ORDER TYPES
// ============================================

type POSOrder struct {
	ID              uuid.UUID      `json:"id"`
	TenantID        uuid.UUID      `json:"tenant_id"`
	SessionID       uuid.UUID      `json:"session_id"`
	OrderNumber     string         `json:"order_number"`
	OrderDate       time.Time      `json:"order_date"`
	CustomerID      *uuid.UUID     `json:"customer_id,omitempty"`
	CustomerName    string         `json:"customer_name,omitempty"`
	CustomerPhone   string         `json:"customer_phone,omitempty"`
	Subtotal        float64        `json:"subtotal"`
	DiscountPercent float64        `json:"discount_percent"`
	DiscountAmount  float64        `json:"discount_amount"`
	TaxAmount       float64        `json:"tax_amount"`
	TotalAmount     float64        `json:"total_amount"`
	AmountPaid      float64        `json:"amount_paid"`
	AmountReturn    float64        `json:"amount_return"`
	Status          string         `json:"status"`
	WarehouseID     *uuid.UUID     `json:"warehouse_id,omitempty"`
	Notes           string         `json:"notes,omitempty"`
	CreatedBy       *uuid.UUID     `json:"created_by,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	Lines           []POSOrderLine `json:"lines,omitempty"`
	Payments        []POSPayment   `json:"payments,omitempty"`
}

type POSOrderLine struct {
	ID              uuid.UUID  `json:"id"`
	OrderID         uuid.UUID  `json:"order_id"`
	LineNumber      int        `json:"line_number"`
	ProductID       uuid.UUID  `json:"product_id"`
	ProductName     string     `json:"product_name"`
	ProductCode     string     `json:"product_code,omitempty"`
	Barcode         string     `json:"barcode,omitempty"`
	Quantity        float64    `json:"quantity"`
	UnitPrice       float64    `json:"unit_price"`
	DiscountPercent float64    `json:"discount_percent"`
	DiscountAmount  float64    `json:"discount_amount"`
	TaxPercent      float64    `json:"tax_percent"`
	TaxAmount       float64    `json:"tax_amount"`
	LineTotal       float64    `json:"line_total"`
	UnitCost        float64    `json:"unit_cost,omitempty"`
	Notes           string     `json:"notes,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type POSPayment struct {
	ID                uuid.UUID  `json:"id"`
	OrderID           uuid.UUID  `json:"order_id"`
	SessionID         uuid.UUID  `json:"session_id"`
	PaymentMethodID   *uuid.UUID `json:"payment_method_id,omitempty"`
	PaymentMethodName string     `json:"payment_method_name"`
	PaymentType       string     `json:"payment_type"`
	Amount            float64    `json:"amount"`
	TransactionID     string     `json:"transaction_id,omitempty"`
	CardLastFour      string     `json:"card_last_four,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

type POSOrderInput struct {
	CustomerID      *uuid.UUID          `json:"customer_id,omitempty"`
	CustomerName    string              `json:"customer_name,omitempty"`
	CustomerPhone   string              `json:"customer_phone,omitempty"`
	DiscountPercent float64             `json:"discount_percent,omitempty"`
	Notes           string              `json:"notes,omitempty"`
	Lines           []POSOrderLineInput `json:"lines" binding:"required,min=1"`
	Payments        []POSPaymentInput   `json:"payments" binding:"required,min=1"`
}

type POSOrderLineInput struct {
	ProductID       uuid.UUID `json:"product_id" binding:"required"`
	Quantity        float64   `json:"quantity" binding:"required,gt=0"`
	UnitPrice       float64   `json:"unit_price" binding:"required,gte=0"`
	DiscountPercent float64   `json:"discount_percent,omitempty"`
	TaxPercent      float64   `json:"tax_percent,omitempty"`
	Notes           string    `json:"notes,omitempty"`
}

type POSPaymentInput struct {
	PaymentMethodID   *uuid.UUID `json:"payment_method_id,omitempty"`
	PaymentMethodName string     `json:"payment_method_name" binding:"required"`
	PaymentType       string     `json:"payment_type" binding:"required"`
	Amount            float64    `json:"amount" binding:"required,gt=0"`
	TransactionID     string     `json:"transaction_id,omitempty"`
	CardLastFour      string     `json:"card_last_four,omitempty"`
}

// ============================================
// POS CONFIG HANDLERS
// ============================================

// ListPOSConfigs returns all POS configurations
func (h *Handler) ListPOSConfigs(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)

	query := `
		SELECT pc.id, pc.tenant_id, pc.name, pc.warehouse_id, w.name as warehouse_name,
			   pc.receipt_header, pc.receipt_footer, pc.default_payment_method_id,
			   pc.is_active, pc.allow_discount, pc.max_discount_percent, pc.require_customer,
			   pc.default_tax_rate, pc.prices_include_tax, pc.update_stock,
			   pc.created_at, pc.updated_at
		FROM pos_configs pc
		LEFT JOIN warehouses w ON w.id = pc.warehouse_id
		WHERE pc.tenant_id = $1 AND pc.deleted_at IS NULL
		ORDER BY pc.name
	`

	rows, err := h.db.Query(query, tenantID)
	if err != nil {
		h.log.Error("Failed to fetch POS configs", "error", err)
		response.InternalError(c, "Failed to fetch POS configs")
		return
	}
	defer rows.Close()

	var configs []POSConfig
	for rows.Next() {
		var config POSConfig
		var warehouseName sql.NullString
		err := rows.Scan(
			&config.ID, &config.TenantID, &config.Name, &config.WarehouseID, &warehouseName,
			&config.ReceiptHeader, &config.ReceiptFooter, &config.DefaultPaymentMethodID,
			&config.IsActive, &config.AllowDiscount, &config.MaxDiscountPercent, &config.RequireCustomer,
			&config.DefaultTaxRate, &config.PricesIncludeTax, &config.UpdateStock,
			&config.CreatedAt, &config.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan POS config", "error", err)
			response.InternalError(c, "Failed to scan POS config")
			return
		}
		if warehouseName.Valid {
			config.WarehouseName = warehouseName.String
		}
		configs = append(configs, config)
	}

	response.Success(c, configs)
}

// CreatePOSConfig creates a new POS configuration
func (h *Handler) CreatePOSConfig(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)

	var input POSConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}
	allowDiscount := true
	if input.AllowDiscount != nil {
		allowDiscount = *input.AllowDiscount
	}
	maxDiscountPercent := 100.0
	if input.MaxDiscountPercent != nil {
		maxDiscountPercent = *input.MaxDiscountPercent
	}
	requireCustomer := false
	if input.RequireCustomer != nil {
		requireCustomer = *input.RequireCustomer
	}
	defaultTaxRate := 12.0
	if input.DefaultTaxRate != nil {
		defaultTaxRate = *input.DefaultTaxRate
	}
	pricesIncludeTax := false
	if input.PricesIncludeTax != nil {
		pricesIncludeTax = *input.PricesIncludeTax
	}
	updateStock := true
	if input.UpdateStock != nil {
		updateStock = *input.UpdateStock
	}

	query := `
		INSERT INTO pos_configs (
			id, tenant_id, name, warehouse_id, receipt_header, receipt_footer,
			default_payment_method_id, is_active, allow_discount, max_discount_percent,
			require_customer, default_tax_rate, prices_include_tax, update_stock, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING created_at, updated_at
	`

	var config POSConfig
	config.ID = id
	config.TenantID = tenantID
	config.Name = input.Name
	config.WarehouseID = input.WarehouseID
	config.ReceiptHeader = input.ReceiptHeader
	config.ReceiptFooter = input.ReceiptFooter
	config.DefaultPaymentMethodID = input.DefaultPaymentMethodID
	config.IsActive = isActive
	config.AllowDiscount = allowDiscount
	config.MaxDiscountPercent = maxDiscountPercent
	config.RequireCustomer = requireCustomer
	config.DefaultTaxRate = defaultTaxRate
	config.PricesIncludeTax = pricesIncludeTax
	config.UpdateStock = updateStock

	err := h.db.QueryRow(query,
		id, tenantID, input.Name, input.WarehouseID, input.ReceiptHeader, input.ReceiptFooter,
		input.DefaultPaymentMethodID, isActive, allowDiscount, maxDiscountPercent,
		requireCustomer, defaultTaxRate, pricesIncludeTax, updateStock, userID,
	).Scan(&config.CreatedAt, &config.UpdatedAt)

	if err != nil {
		h.log.Error("Failed to create POS config", "error", err)
		response.InternalError(c, "Failed to create POS config")
		return
	}

	response.Success(c, config)
}

// UpdatePOSConfig updates a POS configuration
func (h *Handler) UpdatePOSConfig(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	configID := c.Param("id")

	var input POSConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	query := `
		UPDATE pos_configs SET
			name = $1, warehouse_id = $2, receipt_header = $3, receipt_footer = $4,
			default_payment_method_id = $5, is_active = COALESCE($6, is_active),
			allow_discount = COALESCE($7, allow_discount), max_discount_percent = COALESCE($8, max_discount_percent),
			require_customer = COALESCE($9, require_customer), default_tax_rate = COALESCE($10, default_tax_rate),
			prices_include_tax = COALESCE($11, prices_include_tax), update_stock = COALESCE($12, update_stock),
			updated_at = NOW()
		WHERE id = $13 AND tenant_id = $14 AND deleted_at IS NULL
		RETURNING id
	`

	var id uuid.UUID
	err := h.db.QueryRow(query,
		input.Name, input.WarehouseID, input.ReceiptHeader, input.ReceiptFooter,
		input.DefaultPaymentMethodID, input.IsActive, input.AllowDiscount, input.MaxDiscountPercent,
		input.RequireCustomer, input.DefaultTaxRate, input.PricesIncludeTax, input.UpdateStock,
		configID, tenantID,
	).Scan(&id)

	if err == sql.ErrNoRows {
		response.Error(c, 404, "POS config not found", "")
		return
	}
	if err != nil {
		h.log.Error("Failed to update POS config", "error", err)
		response.InternalError(c, "Failed to update POS config")
		return
	}

	response.Success(c, map[string]string{"message": "POS config updated successfully"})
}

// ============================================
// POS SESSION HANDLERS
// ============================================

// OpenPOSSession opens a new POS session
func (h *Handler) OpenPOSSession(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)

	var input OpenSessionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Check if user already has an open session
	var existingSessionID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM pos_sessions
		WHERE tenant_id = $1 AND user_id = $2 AND status = 'open'
	`, tenantID, userID).Scan(&existingSessionID)

	if err == nil {
		response.Error(c, 400, "You already have an open session", "Please close your existing session first")
		return
	}
	if err != sql.ErrNoRows {
		h.log.Error("Failed to check existing session", "error", err)
		response.InternalError(c, "Failed to check existing session")
		return
	}

	// Get user name
	var userName string
	h.db.QueryRow(`SELECT COALESCE(first_name || ' ' || last_name, email) FROM users WHERE id = $1`, userID).Scan(&userName)

	// Get POS config name
	var configName string
	err = h.db.QueryRow(`SELECT name FROM pos_configs WHERE id = $1 AND tenant_id = $2`, input.POSConfigID, tenantID).Scan(&configName)
	if err != nil {
		response.Error(c, 404, "POS config not found", "")
		return
	}

	// Generate session number
	var posSessionCount int
	h.db.QueryRow("SELECT COUNT(*) FROM pos_sessions WHERE tenant_id = $1", tenantID).Scan(&posSessionCount)
	sessionNumber := fmt.Sprintf("POS%05d", posSessionCount+1)

	id := uuid.New()
	query := `
		INSERT INTO pos_sessions (
			id, tenant_id, pos_config_id, session_number, user_id, user_name,
			opening_balance, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'open')
		RETURNING opened_at, created_at
	`

	var session POSSession
	session.ID = id
	session.TenantID = tenantID
	session.POSConfigID = input.POSConfigID
	session.POSConfigName = configName
	session.SessionNumber = sessionNumber
	session.UserID = userID
	session.UserName = userName
	session.OpeningBalance = input.OpeningBalance
	session.Status = "open"

	err = h.db.QueryRow(query,
		id, tenantID, input.POSConfigID, sessionNumber, userID, userName, input.OpeningBalance,
	).Scan(&session.OpenedAt, &session.CreatedAt)

	if err != nil {
		h.log.Error("Failed to open session", "error", err)
		response.InternalError(c, "Failed to open session")
		return
	}

	response.Success(c, session)
}

// GetCurrentPOSSession gets the current open session for the user
func (h *Handler) GetCurrentPOSSession(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)

	query := `
		SELECT ps.id, ps.tenant_id, ps.pos_config_id, pc.name as config_name,
			   ps.session_number, ps.user_id, ps.user_name, ps.opened_at, ps.closed_at,
			   ps.opening_balance, ps.closing_balance, ps.expected_balance, ps.difference,
			   ps.total_sales, ps.total_refunds, ps.total_cash_payments, ps.total_card_payments,
			   ps.total_other_payments, ps.order_count, ps.status, ps.notes, ps.created_at
		FROM pos_sessions ps
		JOIN pos_configs pc ON pc.id = ps.pos_config_id
		WHERE ps.tenant_id = $1 AND ps.user_id = $2 AND ps.status = 'open'
		ORDER BY ps.opened_at DESC
		LIMIT 1
	`

	var session POSSession
	var configName sql.NullString
	var closedAt sql.NullTime
	var closingBalance, expectedBalance, difference sql.NullFloat64
	var notes sql.NullString

	err := h.db.QueryRow(query, tenantID, userID).Scan(
		&session.ID, &session.TenantID, &session.POSConfigID, &configName,
		&session.SessionNumber, &session.UserID, &session.UserName, &session.OpenedAt, &closedAt,
		&session.OpeningBalance, &closingBalance, &expectedBalance, &difference,
		&session.TotalSales, &session.TotalRefunds, &session.TotalCashPayments, &session.TotalCardPayments,
		&session.TotalOtherPayments, &session.OrderCount, &session.Status, &notes, &session.CreatedAt,
	)

	if err == sql.ErrNoRows {
		response.Error(c, 404, "No open session found", "Please open a session first")
		return
	}
	if err != nil {
		h.log.Error("Failed to get session", "error", err)
		response.InternalError(c, "Failed to get session")
		return
	}

	if configName.Valid {
		session.POSConfigName = configName.String
	}
	if closedAt.Valid {
		session.ClosedAt = &closedAt.Time
	}
	if closingBalance.Valid {
		session.ClosingBalance = &closingBalance.Float64
	}
	if expectedBalance.Valid {
		session.ExpectedBalance = &expectedBalance.Float64
	}
	if difference.Valid {
		session.Difference = &difference.Float64
	}
	if notes.Valid {
		session.Notes = notes.String
	}

	response.Success(c, session)
}

// ClosePOSSession closes a POS session
func (h *Handler) ClosePOSSession(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)
	sessionID := c.Param("id")

	var input CloseSessionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get session and calculate expected balance
	var session POSSession
	var openingBalance, totalCashPayments, totalRefunds float64

	err := h.db.QueryRow(`
		SELECT id, opening_balance, total_cash_payments, total_refunds, user_id
		FROM pos_sessions
		WHERE id = $1 AND tenant_id = $2 AND status = 'open'
	`, sessionID, tenantID).Scan(&session.ID, &openingBalance, &totalCashPayments, &totalRefunds, &session.UserID)

	if err == sql.ErrNoRows {
		response.Error(c, 404, "Session not found or already closed", "")
		return
	}
	if err != nil {
		h.log.Error("Failed to get session", "error", err)
		response.InternalError(c, "Failed to get session")
		return
	}

	// Verify ownership
	if session.UserID != userID {
		response.Error(c, 403, "You can only close your own session", "")
		return
	}

	// Get cash movements (in/out)
	var cashIn, cashOut float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(CASE WHEN movement_type = 'in' THEN amount ELSE 0 END), 0),
			   COALESCE(SUM(CASE WHEN movement_type = 'out' THEN amount ELSE 0 END), 0)
		FROM pos_cash_movements WHERE session_id = $1
	`, sessionID).Scan(&cashIn, &cashOut)

	expectedBalance := openingBalance + totalCashPayments - totalRefunds + cashIn - cashOut
	difference := input.ClosingBalance - expectedBalance

	// Update session
	query := `
		UPDATE pos_sessions SET
			status = 'closed',
			closed_at = NOW(),
			closing_balance = $1,
			expected_balance = $2,
			difference = $3,
			notes = $4,
			updated_at = NOW()
		WHERE id = $5
	`

	_, err = h.db.Exec(query, input.ClosingBalance, expectedBalance, difference, input.Notes, sessionID)
	if err != nil {
		h.log.Error("Failed to close session", "error", err)
		response.InternalError(c, "Failed to close session")
		return
	}

	response.Success(c, map[string]interface{}{
		"message":          "Session closed successfully",
		"closing_balance":  input.ClosingBalance,
		"expected_balance": expectedBalance,
		"difference":       difference,
	})
}

// ListPOSSessions lists all sessions
func (h *Handler) ListPOSSessions(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	status := c.Query("status")

	query := `
		SELECT ps.id, ps.tenant_id, ps.pos_config_id, pc.name as config_name,
			   ps.session_number, ps.user_id, ps.user_name, ps.opened_at, ps.closed_at,
			   ps.opening_balance, ps.closing_balance, ps.expected_balance, ps.difference,
			   ps.total_sales, ps.total_refunds, ps.total_cash_payments, ps.total_card_payments,
			   ps.total_other_payments, ps.order_count, ps.status, ps.notes, ps.created_at
		FROM pos_sessions ps
		JOIN pos_configs pc ON pc.id = ps.pos_config_id
		WHERE ps.tenant_id = $1
	`
	args := []interface{}{tenantID}

	if status != "" {
		query += " AND ps.status = $2"
		args = append(args, status)
	}

	query += " ORDER BY ps.opened_at DESC LIMIT 100"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to fetch sessions", "error", err)
		response.InternalError(c, "Failed to fetch sessions")
		return
	}
	defer rows.Close()

	var sessions []POSSession
	for rows.Next() {
		var session POSSession
		var configName, notes sql.NullString
		var closedAt sql.NullTime
		var closingBalance, expectedBalance, difference sql.NullFloat64

		err := rows.Scan(
			&session.ID, &session.TenantID, &session.POSConfigID, &configName,
			&session.SessionNumber, &session.UserID, &session.UserName, &session.OpenedAt, &closedAt,
			&session.OpeningBalance, &closingBalance, &expectedBalance, &difference,
			&session.TotalSales, &session.TotalRefunds, &session.TotalCashPayments, &session.TotalCardPayments,
			&session.TotalOtherPayments, &session.OrderCount, &session.Status, &notes, &session.CreatedAt,
		)
		if err != nil {
			continue
		}

		if configName.Valid {
			session.POSConfigName = configName.String
		}
		if closedAt.Valid {
			session.ClosedAt = &closedAt.Time
		}
		if closingBalance.Valid {
			session.ClosingBalance = &closingBalance.Float64
		}
		if expectedBalance.Valid {
			session.ExpectedBalance = &expectedBalance.Float64
		}
		if difference.Valid {
			session.Difference = &difference.Float64
		}
		if notes.Valid {
			session.Notes = notes.String
		}

		sessions = append(sessions, session)
	}

	response.Success(c, sessions)
}

// ============================================
// POS ORDER HANDLERS
// ============================================

// CreatePOSOrder creates a new POS order (sale)
func (h *Handler) CreatePOSOrder(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)

	var input POSOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Get current session
	var sessionID uuid.UUID
	var warehouseID uuid.UUID
	var updateStock bool

	err := h.db.QueryRow(`
		SELECT ps.id, pc.warehouse_id, pc.update_stock
		FROM pos_sessions ps
		JOIN pos_configs pc ON pc.id = ps.pos_config_id
		WHERE ps.tenant_id = $1 AND ps.user_id = $2 AND ps.status = 'open'
	`, tenantID, userID).Scan(&sessionID, &warehouseID, &updateStock)

	if err == sql.ErrNoRows {
		response.Error(c, 400, "No open session", "Please open a session first")
		return
	}
	if err != nil {
		h.log.Error("Failed to get session", "error", err)
		response.InternalError(c, "Failed to get session")
		return
	}

	// Warehouse org scopes the inventory row and the consumption JE
	var warehouseOrgID *uuid.UUID
	_ = h.db.QueryRow(`SELECT organization_id FROM warehouses WHERE id = $1`, warehouseID).Scan(&warehouseOrgID)

	// COGS lines collected during the tx, posted to the GL after commit
	type posConsumedLine struct {
		ProductID uuid.UUID
		Quantity  float64
		UnitCost  float64
	}
	var consumedLines []posConsumedLine

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Generate order number
	var posOrderCount int
	h.db.QueryRow("SELECT COUNT(*) FROM pos_orders WHERE tenant_id = $1", tenantID).Scan(&posOrderCount)
	orderNumber := fmt.Sprintf("POS%05d", posOrderCount+1)

	// Calculate totals
	var subtotal float64
	for _, line := range input.Lines {
		lineSubtotal := line.Quantity * line.UnitPrice
		discount := lineSubtotal * line.DiscountPercent / 100
		subtotal += lineSubtotal - discount
	}

	discountAmount := subtotal * input.DiscountPercent / 100
	afterDiscount := subtotal - discountAmount

	var taxAmount float64
	for _, line := range input.Lines {
		lineSubtotal := line.Quantity * line.UnitPrice
		lineDiscount := lineSubtotal * line.DiscountPercent / 100
		lineNet := lineSubtotal - lineDiscount
		lineTax := lineNet * line.TaxPercent / 100
		taxAmount += lineTax
	}

	totalAmount := afterDiscount + taxAmount

	// Calculate total paid
	var amountPaid float64
	for _, payment := range input.Payments {
		amountPaid += payment.Amount
	}
	amountReturn := amountPaid - totalAmount
	if amountReturn < 0 {
		amountReturn = 0
	}

	// Create order
	orderID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO pos_orders (
			id, tenant_id, session_id, order_number, customer_id, customer_name, customer_phone,
			subtotal, discount_percent, discount_amount, tax_amount, total_amount,
			amount_paid, amount_return, status, warehouse_id, notes, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 'paid', $15, $16, $17)
	`, orderID, tenantID, sessionID, orderNumber, input.CustomerID, input.CustomerName, input.CustomerPhone,
		subtotal, input.DiscountPercent, discountAmount, taxAmount, totalAmount,
		amountPaid, amountReturn, warehouseID, input.Notes, userID)

	if err != nil {
		h.log.Error("Failed to create order", "error", err)
		response.InternalError(c, "Failed to create order")
		return
	}

	// Create order lines and update inventory
	for i, line := range input.Lines {
		// Get product info
		var productName, productCode string
		var barcode sql.NullString
		var unitCost float64
		err := tx.QueryRow(`
			SELECT name, COALESCE(code, ''), barcode, COALESCE(cost_price, 0)
			FROM products WHERE id = $1
		`, line.ProductID).Scan(&productName, &productCode, &barcode, &unitCost)

		if err != nil {
			response.Error(c, 404, "Product not found", line.ProductID.String())
			return
		}

		lineSubtotal := line.Quantity * line.UnitPrice
		lineDiscount := lineSubtotal * line.DiscountPercent / 100
		lineNet := lineSubtotal - lineDiscount
		lineTax := lineNet * line.TaxPercent / 100
		lineTotal := lineNet + lineTax

		barcodeStr := ""
		if barcode.Valid {
			barcodeStr = barcode.String
		}

		_, err = tx.Exec(`
			INSERT INTO pos_order_lines (
				id, order_id, line_number, product_id, product_name, product_code, barcode,
				quantity, unit_price, discount_percent, discount_amount, tax_percent, tax_amount,
				line_total, unit_cost, notes
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		`, uuid.New(), orderID, i+1, line.ProductID, productName, productCode, barcodeStr,
			line.Quantity, line.UnitPrice, line.DiscountPercent, lineDiscount, line.TaxPercent, lineTax,
			lineTotal, unitCost, line.Notes)

		if err != nil {
			h.log.Error("Failed to create order line", "error", err)
			response.InternalError(c, "Failed to create order line")
			return
		}

		// Update inventory if enabled. Upsert-decrement: a POS sale of a
		// product with no inventory row books a negative balance instead of
		// silently updating zero rows (the old UPDATE also wrote the
		// GENERATED quantity_available column, so this path always errored).
		if updateStock {
			var inventoryID uuid.UUID
			var invUnitCost float64
			err = tx.QueryRow(`
				INSERT INTO inventory (id, tenant_id, organization_id, product_id, warehouse_id,
					quantity_on_hand, quantity_reserved, unit_cost, last_movement_date, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, 0, $7, NOW(), NOW(), NOW())
				ON CONFLICT (tenant_id, product_id, warehouse_id) DO UPDATE SET
					quantity_on_hand = inventory.quantity_on_hand + EXCLUDED.quantity_on_hand,
					last_movement_date = NOW(),
					updated_at = NOW()
				RETURNING id, unit_cost
			`, uuid.New(), tenantID, warehouseOrgID, line.ProductID, warehouseID,
				-line.Quantity, unitCost).Scan(&inventoryID, &invUnitCost)

			if err != nil {
				h.log.Error("Failed to update inventory", "error", err)
				response.InternalError(c, "Failed to update inventory")
				return
			}
			if invUnitCost == 0 {
				invUnitCost = unitCost
			}

			// Create inventory transaction (ledger row; quantity stored
			// negative + type 'issue' — stock_ledger normalizes either way)
			_, err = tx.Exec(`
				INSERT INTO inventory_transactions (
					id, tenant_id, organization_id, inventory_id, product_id, warehouse_id,
					transaction_type, quantity, unit_cost, total_cost,
					reference_type, reference_id, notes, transaction_date, created_by, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, 'issue', $7, $8, $9, 'pos_order', $10, $11, NOW(), $12, NOW())
			`, uuid.New(), tenantID, warehouseOrgID, inventoryID, line.ProductID, warehouseID,
				-line.Quantity, invUnitCost, line.Quantity*invUnitCost, orderID, "POS Sale: "+orderNumber, userID)

			if err != nil {
				h.log.Error("Failed to create inventory transaction", "error", err)
				response.InternalError(c, "Failed to create inventory transaction")
				return
			}

			consumedLines = append(consumedLines, posConsumedLine{
				ProductID: line.ProductID,
				Quantity:  line.Quantity,
				UnitCost:  invUnitCost,
			})
		}
	}

	// Create payments
	var totalCash, totalCard, totalOther float64
	for _, payment := range input.Payments {
		_, err = tx.Exec(`
			INSERT INTO pos_payments (
				id, order_id, session_id, payment_method_id, payment_method_name, payment_type,
				amount, transaction_id, card_last_four
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, uuid.New(), orderID, sessionID, payment.PaymentMethodID, payment.PaymentMethodName,
			payment.PaymentType, payment.Amount, payment.TransactionID, payment.CardLastFour)

		if err != nil {
			h.log.Error("Failed to create payment", "error", err)
			response.InternalError(c, "Failed to create payment")
			return
		}

		switch payment.PaymentType {
		case "cash":
			totalCash += payment.Amount
		case "card":
			totalCard += payment.Amount
		default:
			totalOther += payment.Amount
		}
	}

	// Update session totals
	_, err = tx.Exec(`
		UPDATE pos_sessions SET
			total_sales = total_sales + $1,
			total_cash_payments = total_cash_payments + $2,
			total_card_payments = total_card_payments + $3,
			total_other_payments = total_other_payments + $4,
			order_count = order_count + 1,
			updated_at = NOW()
		WHERE id = $5
	`, totalAmount, totalCash, totalCard, totalOther, sessionID)

	if err != nil {
		h.log.Error("Failed to update session", "error", err)
		response.InternalError(c, "Failed to update session")
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	// Revenue side: DR kassa/karta, CR savdo daromadi (+ CR QQS). POS sales
	// previously never touched the GL at all (docs/ombor-audit.md §2).
	h.postPOSRevenueJE(tenantID, warehouseOrgID, orderID, orderNumber, userID,
		totalCash-amountReturn, totalCard+totalOther, totalAmount, taxAmount)

	// Post COGS to the GL (DR cost-of-goods / CR inventory) per consumed
	// line. Best-effort + idempotency-keyed — POS sales previously never
	// touched the GL at all (docs/ombor-audit.md §2, finding: POS never
	// hits the GL), which was one source of the Ombor↔Buxgalteriya drift.
	for _, cl := range consumedLines {
		h.postInventoryConsumptionJE(h.db, postInventoryConsumptionArgs{
			TenantID:       tenantID,
			OrganizationID: warehouseOrgID,
			ProductID:      cl.ProductID,
			Quantity:       cl.Quantity,
			UnitCost:       cl.UnitCost,
			SourceType:     "pos_order",
			SourceID:       &orderID,
			IdempotencyKey: fmt.Sprintf("POS-CONS-%s-%s", orderID, cl.ProductID),
			Description:    "POS sotuv tannarxi: " + orderNumber,
		})
	}

	// Return order with lines
	order := POSOrder{
		ID:              orderID,
		TenantID:        tenantID,
		SessionID:       sessionID,
		OrderNumber:     orderNumber,
		OrderDate:       time.Now(),
		CustomerID:      input.CustomerID,
		CustomerName:    input.CustomerName,
		CustomerPhone:   input.CustomerPhone,
		Subtotal:        subtotal,
		DiscountPercent: input.DiscountPercent,
		DiscountAmount:  discountAmount,
		TaxAmount:       taxAmount,
		TotalAmount:     totalAmount,
		AmountPaid:      amountPaid,
		AmountReturn:    amountReturn,
		Status:          "paid",
		CreatedBy:       &userID,
		CreatedAt:       time.Now(),
	}

	response.Success(c, order)
}

// GetPOSOrder gets a single POS order with lines and payments
func (h *Handler) GetPOSOrder(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	orderID := c.Param("id")

	// Get order
	query := `
		SELECT id, tenant_id, session_id, order_number, order_date,
			   customer_id, customer_name, customer_phone,
			   subtotal, discount_percent, discount_amount, tax_amount, total_amount,
			   amount_paid, amount_return, status, warehouse_id, notes, created_by, created_at
		FROM pos_orders
		WHERE id = $1 AND tenant_id = $2
	`

	var order POSOrder
	var customerID, warehouseID, createdBy sql.NullString
	var notes sql.NullString

	err := h.db.QueryRow(query, orderID, tenantID).Scan(
		&order.ID, &order.TenantID, &order.SessionID, &order.OrderNumber, &order.OrderDate,
		&customerID, &order.CustomerName, &order.CustomerPhone,
		&order.Subtotal, &order.DiscountPercent, &order.DiscountAmount, &order.TaxAmount, &order.TotalAmount,
		&order.AmountPaid, &order.AmountReturn, &order.Status, &warehouseID, &notes, &createdBy, &order.CreatedAt,
	)

	if err == sql.ErrNoRows {
		response.Error(c, 404, "Order not found", "")
		return
	}
	if err != nil {
		h.log.Error("Failed to get order", "error", err)
		response.InternalError(c, "Failed to get order")
		return
	}

	if customerID.Valid {
		id, _ := uuid.Parse(customerID.String)
		order.CustomerID = &id
	}
	if warehouseID.Valid {
		id, _ := uuid.Parse(warehouseID.String)
		order.WarehouseID = &id
	}
	if createdBy.Valid {
		id, _ := uuid.Parse(createdBy.String)
		order.CreatedBy = &id
	}
	if notes.Valid {
		order.Notes = notes.String
	}

	// Get lines
	lineRows, err := h.db.Query(`
		SELECT id, order_id, line_number, product_id, product_name, product_code, barcode,
			   quantity, unit_price, discount_percent, discount_amount, tax_percent, tax_amount,
			   line_total, unit_cost, notes, created_at
		FROM pos_order_lines WHERE order_id = $1 ORDER BY line_number
	`, orderID)

	if err == nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var line POSOrderLine
			var barcode, lineNotes sql.NullString
			lineRows.Scan(
				&line.ID, &line.OrderID, &line.LineNumber, &line.ProductID, &line.ProductName, &line.ProductCode, &barcode,
				&line.Quantity, &line.UnitPrice, &line.DiscountPercent, &line.DiscountAmount, &line.TaxPercent, &line.TaxAmount,
				&line.LineTotal, &line.UnitCost, &lineNotes, &line.CreatedAt,
			)
			if barcode.Valid {
				line.Barcode = barcode.String
			}
			if lineNotes.Valid {
				line.Notes = lineNotes.String
			}
			order.Lines = append(order.Lines, line)
		}
	}

	// Get payments
	paymentRows, err := h.db.Query(`
		SELECT id, order_id, session_id, payment_method_id, payment_method_name, payment_type,
			   amount, transaction_id, card_last_four, created_at
		FROM pos_payments WHERE order_id = $1
	`, orderID)

	if err == nil {
		defer paymentRows.Close()
		for paymentRows.Next() {
			var payment POSPayment
			var paymentMethodID sql.NullString
			var transactionID, cardLastFour sql.NullString
			paymentRows.Scan(
				&payment.ID, &payment.OrderID, &payment.SessionID, &paymentMethodID, &payment.PaymentMethodName, &payment.PaymentType,
				&payment.Amount, &transactionID, &cardLastFour, &payment.CreatedAt,
			)
			if paymentMethodID.Valid {
				id, _ := uuid.Parse(paymentMethodID.String)
				payment.PaymentMethodID = &id
			}
			if transactionID.Valid {
				payment.TransactionID = transactionID.String
			}
			if cardLastFour.Valid {
				payment.CardLastFour = cardLastFour.String
			}
			order.Payments = append(order.Payments, payment)
		}
	}

	response.Success(c, order)
}

// ListPOSOrders lists orders for current session or all orders
func (h *Handler) ListPOSOrders(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	sessionID := c.Query("session_id")

	query := `
		SELECT id, tenant_id, session_id, order_number, order_date,
			   customer_id, customer_name, customer_phone,
			   subtotal, discount_percent, discount_amount, tax_amount, total_amount,
			   amount_paid, amount_return, status, warehouse_id, notes, created_by, created_at
		FROM pos_orders
		WHERE tenant_id = $1
	`
	args := []interface{}{tenantID}

	if sessionID != "" {
		query += " AND session_id = $2"
		args = append(args, sessionID)
	}

	query += " ORDER BY order_date DESC LIMIT 100"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to fetch orders", "error", err)
		response.InternalError(c, "Failed to fetch orders")
		return
	}
	defer rows.Close()

	var orders []POSOrder
	for rows.Next() {
		var order POSOrder
		var customerID, warehouseID, createdBy sql.NullString
		var notes sql.NullString

		rows.Scan(
			&order.ID, &order.TenantID, &order.SessionID, &order.OrderNumber, &order.OrderDate,
			&customerID, &order.CustomerName, &order.CustomerPhone,
			&order.Subtotal, &order.DiscountPercent, &order.DiscountAmount, &order.TaxAmount, &order.TotalAmount,
			&order.AmountPaid, &order.AmountReturn, &order.Status, &warehouseID, &notes, &createdBy, &order.CreatedAt,
		)

		if customerID.Valid {
			id, _ := uuid.Parse(customerID.String)
			order.CustomerID = &id
		}
		if warehouseID.Valid {
			id, _ := uuid.Parse(warehouseID.String)
			order.WarehouseID = &id
		}
		if createdBy.Valid {
			id, _ := uuid.Parse(createdBy.String)
			order.CreatedBy = &id
		}
		if notes.Valid {
			order.Notes = notes.String
		}

		orders = append(orders, order)
	}

	response.Success(c, orders)
}

// SearchPOSProducts searches products optimized for POS
func (h *Handler) SearchPOSProducts(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	query := c.Query("q")
	categoryID := c.Query("category_id")

	sqlQuery := `
		SELECT p.id, p.name, p.code, p.barcode, p.sale_price, p.cost,
			   COALESCE(i.quantity_available, 0) as stock,
			   p.image_url
		FROM products p
		LEFT JOIN inventory i ON i.product_id = p.id AND i.tenant_id = p.tenant_id
		WHERE p.tenant_id = $1 AND p.is_active = true AND p.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argIndex := 2

	if query != "" {
		sqlQuery += fmt.Sprintf(" AND (p.name ILIKE $%d OR p.code ILIKE $%d OR p.barcode = $%d)", argIndex, argIndex, argIndex+1)
		args = append(args, "%"+query+"%", query)
		argIndex += 2
	}

	if categoryID != "" {
		sqlQuery += fmt.Sprintf(" AND p.category_id = $%d", argIndex)
		args = append(args, categoryID)
		argIndex++
	}

	sqlQuery += " ORDER BY p.name LIMIT 50"

	rows, err := h.db.Query(sqlQuery, args...)
	if err != nil {
		h.log.Error("Failed to search products", "error", err)
		response.InternalError(c, "Failed to search products")
		return
	}
	defer rows.Close()

	type POSProduct struct {
		ID        uuid.UUID `json:"id"`
		Name      string    `json:"name"`
		Code      string    `json:"code,omitempty"`
		Barcode   string    `json:"barcode,omitempty"`
		SalePrice float64   `json:"sale_price"`
		Cost      float64   `json:"cost"`
		Stock     float64   `json:"stock"`
		ImageURL  string    `json:"image_url,omitempty"`
	}

	var products []POSProduct
	for rows.Next() {
		var p POSProduct
		var code, barcode, imageURL sql.NullString
		rows.Scan(&p.ID, &p.Name, &code, &barcode, &p.SalePrice, &p.Cost, &p.Stock, &imageURL)

		if code.Valid {
			p.Code = code.String
		}
		if barcode.Valid {
			p.Barcode = barcode.String
		}
		if imageURL.Valid {
			p.ImageURL = imageURL.String
		}

		products = append(products, p)
	}

	response.Success(c, products)
}

// GetSessionSummary returns summary for a session
func (h *Handler) GetSessionSummary(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	sessionID := c.Param("id")

	type SessionSummary struct {
		SessionID          uuid.UUID `json:"session_id"`
		SessionNumber      string    `json:"session_number"`
		OpenedAt           time.Time `json:"opened_at"`
		ClosedAt           *time.Time `json:"closed_at,omitempty"`
		Status             string    `json:"status"`
		OpeningBalance     float64   `json:"opening_balance"`
		TotalSales         float64   `json:"total_sales"`
		TotalRefunds       float64   `json:"total_refunds"`
		NetSales           float64   `json:"net_sales"`
		TotalCashPayments  float64   `json:"total_cash_payments"`
		TotalCardPayments  float64   `json:"total_card_payments"`
		TotalOtherPayments float64   `json:"total_other_payments"`
		OrderCount         int       `json:"order_count"`
		ExpectedBalance    float64   `json:"expected_balance"`
		ClosingBalance     *float64  `json:"closing_balance,omitempty"`
		Difference         *float64  `json:"difference,omitempty"`
	}

	query := `
		SELECT id, session_number, opened_at, closed_at, status,
			   opening_balance, total_sales, total_refunds,
			   total_cash_payments, total_card_payments, total_other_payments,
			   order_count, expected_balance, closing_balance, difference
		FROM pos_sessions
		WHERE id = $1 AND tenant_id = $2
	`

	var summary SessionSummary
	var closedAt sql.NullTime
	var expectedBalance, closingBalance, difference sql.NullFloat64

	err := h.db.QueryRow(query, sessionID, tenantID).Scan(
		&summary.SessionID, &summary.SessionNumber, &summary.OpenedAt, &closedAt, &summary.Status,
		&summary.OpeningBalance, &summary.TotalSales, &summary.TotalRefunds,
		&summary.TotalCashPayments, &summary.TotalCardPayments, &summary.TotalOtherPayments,
		&summary.OrderCount, &expectedBalance, &closingBalance, &difference,
	)

	if err == sql.ErrNoRows {
		response.Error(c, 404, "Session not found", "")
		return
	}
	if err != nil {
		h.log.Error("Failed to get session summary", "error", err)
		response.InternalError(c, "Failed to get session summary")
		return
	}

	summary.NetSales = summary.TotalSales - summary.TotalRefunds

	if closedAt.Valid {
		summary.ClosedAt = &closedAt.Time
	}
	if expectedBalance.Valid {
		summary.ExpectedBalance = expectedBalance.Float64
	} else {
		// Calculate expected balance if session is still open
		summary.ExpectedBalance = summary.OpeningBalance + summary.TotalCashPayments - summary.TotalRefunds
	}
	if closingBalance.Valid {
		summary.ClosingBalance = &closingBalance.Float64
	}
	if difference.Valid {
		summary.Difference = &difference.Float64
	}

	response.Success(c, summary)
}

// postPOSRevenueJE posts the sale-side entry for a POS order:
//
//	DR  Kassa (5010)            cash received minus change
//	DR  Bank/karta (5110)       card + other payments
//	CR  Savdo daromadi (9020)   net of tax
//	CR  QQS (6410)              tax portion, if any
//
// Idempotency-keyed on POS-REV-<orderID> (journal_entries.reference), all
// legs + account-balance bumps in one tx (migration 416 balance trigger).
// Best-effort: failures are logged, the sale itself is already committed.
func (h *Handler) postPOSRevenueJE(tenantID uuid.UUID, orgID *uuid.UUID, orderID uuid.UUID, orderNumber string, userID uuid.UUID, cashLeg, cardLeg, totalAmount, taxAmount float64) {
	if totalAmount <= 0 {
		return
	}

	ref := fmt.Sprintf("POS-REV-%s", orderID)
	var existing int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM journal_entries WHERE reference = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		ref, tenantID).Scan(&existing)
	if existing > 0 {
		return
	}

	revenueNet := totalAmount - taxAmount

	cashAcct := findAccount(h.db, tenantID, orgID, "kassa", "5010")
	if cashAcct == uuid.Nil {
		cashAcct = findAccount(h.db, tenantID, orgID, "cash", "5010")
	}
	var cardAcct uuid.UUID
	if cardLeg > 0 {
		cardAcct = findAccount(h.db, tenantID, orgID, "bank account", "5110")
	}
	revenueAcct := findAccount(h.db, tenantID, orgID, "sales revenue", "9020")
	if revenueAcct == uuid.Nil {
		revenueAcct = findAccount(h.db, tenantID, orgID, "revenue", "9010")
	}
	var taxAcct uuid.UUID
	if taxAmount > 0 {
		taxAcct = findAccount(h.db, tenantID, orgID, "qqs", "6410")
		if taxAcct == uuid.Nil {
			taxAcct = findAccount(h.db, tenantID, orgID, "tax payable", "6410")
		}
	}

	if cashAcct == uuid.Nil || revenueAcct == uuid.Nil || (cardLeg > 0 && cardAcct == uuid.Nil) || (taxAmount > 0 && taxAcct == uuid.Nil) {
		h.log.Warn("POS revenue JE skipped — accounts unresolved", "order", orderNumber,
			"cash", cashAcct, "card", cardAcct, "revenue", revenueAcct, "tax", taxAcct)
		return
	}

	var journalID uuid.UUID
	var nextNumber int
	h.db.QueryRow(`SELECT id, COALESCE(next_number,1) FROM journals WHERE tenant_id=$1 AND code IN ('CASH','GEN','GENERAL','MISC') AND deleted_at IS NULL ORDER BY CASE code WHEN 'CASH' THEN 0 WHEN 'GEN' THEN 1 WHEN 'GENERAL' THEN 2 ELSE 3 END LIMIT 1`, tenantID).Scan(&journalID, &nextNumber)
	if journalID == uuid.Nil {
		h.log.Warn("POS revenue JE skipped — no journal found", "order", orderNumber)
		return
	}

	now := time.Now()
	tx, err := h.db.Begin()
	if err != nil {
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	entryID := uuid.New()
	entryNumber := fmt.Sprintf("POS%06d", nextEntryNumberSeq(tx, tenantID, orgID, "POS", nextNumber))
	description := "POS sotuv: " + orderNumber
	if _, err := tx.Exec(`
		INSERT INTO journal_entries (
			id, tenant_id, organization_id, journal_id, entry_number, entry_date,
			description, reference, source_type, source_id, status, total_debit, total_credit,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pos_order', $9, 'posted', $10, $10, $11, $12, $12)
	`, entryID, tenantID, orgID, journalID, entryNumber, now,
		description, ref, orderID.String(), totalAmount, nilIfZeroUUID(userID), now); err != nil {
		h.log.Error("POS revenue JE header failed", "error", err)
		return
	}

	lineNo := 1
	addLine := func(acct uuid.UUID, dr, cr float64, desc string) bool {
		if _, err := tx.Exec(`INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			uuid.New(), entryID, acct, desc, dr, cr, lineNo, now); err != nil {
			h.log.Error("POS revenue JE line failed", "error", err, "line", desc)
			return false
		}
		lineNo++
		if _, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`,
			dr-cr, now, acct); err != nil {
			h.log.Error("POS revenue JE balance bump failed", "error", err)
			return false
		}
		return true
	}

	if cashLeg > 0 && !addLine(cashAcct, cashLeg, 0, "Kassa tushumi") {
		return
	}
	if cardLeg > 0 && !addLine(cardAcct, cardLeg, 0, "Karta/bank tushumi") {
		return
	}
	if !addLine(revenueAcct, 0, revenueNet, "Savdo daromadi") {
		return
	}
	if taxAmount > 0 && !addLine(taxAcct, 0, taxAmount, "QQS") {
		return
	}
	if _, err := tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID); err != nil {
		return
	}
	if err := tx.Commit(); err != nil {
		h.log.Error("POS revenue JE commit failed", "error", err)
		return
	}
	committed = true
}
