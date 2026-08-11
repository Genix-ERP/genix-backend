package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CARGO SHIPMENT HANDLERS
// =====================================================

// ListCargoShipments returns a paginated list of cargo shipments
func (h *Handler) ListCargoShipments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	// Parse filters
	status := c.Query("status")
	search := c.Query("search")

	// Build query
	baseQuery := `
		SELECT id, tenant_id, tracking_number, supplier_country, supplier_company,
		       expected_date, actual_arrival_date, status,
		       transport_cost, customs_cost, insurance_cost, other_cost, total_cost,
		       notes, created_by, created_date, updated_date
		FROM cargo_shipments
		WHERE tenant_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM cargo_shipments WHERE tenant_id = $1`

	args := []interface{}{tenantID}
	argCount := 1

	if status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
	}

	if search != "" {
		argCount++
		searchPattern := "%" + search + "%"
		baseQuery += fmt.Sprintf(" AND (tracking_number ILIKE $%d OR supplier_company ILIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (tracking_number ILIKE $%d OR supplier_company ILIKE $%d)", argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count shipments", "error", err)
		response.InternalError(c, "Failed to count shipments")
		return
	}

	// Add ordering and pagination
	baseQuery += " ORDER BY created_date DESC, id ASC"
	pagination := entity.NewPagination(page, limit)
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", pagination.Limit, pagination.Offset())

	// Get shipments
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to query shipments", "error", err)
		response.InternalError(c, "Failed to query shipments")
		return
	}
	defer rows.Close()

	shipments := []entity.CargoShipment{}
	for rows.Next() {
		var s entity.CargoShipment
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.TrackingNumber, &s.SupplierCountry, &s.SupplierCompany,
			&s.ExpectedDate, &s.ActualArrivalDate, &s.Status,
			&s.TransportCost, &s.CustomsCost, &s.InsuranceCost, &s.OtherCost, &s.TotalCost,
			&s.Notes, &s.CreatedBy, &s.CreatedDate, &s.UpdatedDate,
		); err != nil {
			h.log.Error("Failed to scan shipment", "error", err)
			continue
		}

		// Load items
		s.Items, _ = h.loadShipmentItems(s.ID)

		// Load status history
		s.StatusHistory, _ = h.loadShipmentStatusHistory(s.ID)

		shipments = append(shipments, s)
	}

	pagination.Calculate(total)
	response.SuccessWithPagination(c, shipments, pagination)
}

// GetCargoShipment returns a single cargo shipment by ID
func (h *Handler) GetCargoShipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid shipment ID")
		return
	}

	query := `
		SELECT id, tenant_id, tracking_number, supplier_country, supplier_company,
		       expected_date, actual_arrival_date, status,
		       transport_cost, customs_cost, insurance_cost, other_cost, total_cost,
		       notes, created_by, created_date, updated_date
		FROM cargo_shipments
		WHERE id = $1 AND tenant_id = $2
	`

	var shipment entity.CargoShipment
	err = h.db.QueryRow(query, id, tenantID).Scan(
		&shipment.ID, &shipment.TenantID, &shipment.TrackingNumber, &shipment.SupplierCountry, &shipment.SupplierCompany,
		&shipment.ExpectedDate, &shipment.ActualArrivalDate, &shipment.Status,
		&shipment.TransportCost, &shipment.CustomsCost, &shipment.InsuranceCost, &shipment.OtherCost, &shipment.TotalCost,
		&shipment.Notes, &shipment.CreatedBy, &shipment.CreatedDate, &shipment.UpdatedDate,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Shipment not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to query shipment", "error", err)
		response.InternalError(c, "Failed to query shipment")
		return
	}

	// Load items
	shipment.Items, _ = h.loadShipmentItems(shipment.ID)

	// Load status history
	shipment.StatusHistory, _ = h.loadShipmentStatusHistory(shipment.ID)

	response.Success(c, shipment)
}

// CreateCargoShipment creates a new cargo shipment
func (h *Handler) CreateCargoShipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req entity.CreateCargoShipmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Insert shipment
	query := `
		INSERT INTO cargo_shipments (
			tenant_id, tracking_number, supplier_country, supplier_company,
			expected_date, status,
			transport_cost, customs_cost, insurance_cost, other_cost,
			notes, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
		RETURNING id
	`

	var shipmentID int64
	var createdByUUID *uuid.UUID
	if userID != uuid.Nil {
		createdByUUID = &userID
	}

	err = tx.QueryRow(
		query, tenantID, req.TrackingNumber, req.SupplierCountry, sql.NullString{String: req.SupplierCompany, Valid: req.SupplierCompany != ""},
		req.ExpectedDate, "ordered",
		req.TransportCost, req.CustomsCost, req.InsuranceCost, req.OtherCost,
		sql.NullString{String: req.Notes, Valid: req.Notes != ""}, createdByUUID,
	).Scan(&shipmentID)
	if err != nil {
		h.log.Error("Failed to insert shipment", "error", err)
		response.InternalError(c, "Failed to create shipment")
		return
	}

	// Insert items
	itemQuery := `
		INSERT INTO cargo_shipment_items (
			shipment_id, item_name, quantity, unit_price, currency, hs_code, description,
			created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
	`

	for _, item := range req.Items {
		_, err = tx.Exec(
			itemQuery, shipmentID, item.ItemName, item.Quantity, item.UnitPrice, item.Currency,
			sql.NullString{String: item.HSCode, Valid: item.HSCode != ""},
			sql.NullString{String: item.Description, Valid: item.Description != ""},
		)
		if err != nil {
			h.log.Error("Failed to insert shipment item", "error", err)
			response.InternalError(c, "Failed to create shipment items")
			return
		}
	}

	// Insert initial status history
	statusQuery := `
		INSERT INTO cargo_shipment_status_history (shipment_id, status, note, changed_by, changed_date)
		VALUES ($1, $2, $3, $4, NOW())
	`
	_, err = tx.Exec(statusQuery, shipmentID, "ordered", "Shipment created", createdByUUID)
	if err != nil {
		h.log.Error("Failed to insert status history", "error", err)
		response.InternalError(c, "Failed to create status history")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	response.Created(c, gin.H{"id": shipmentID})
}

// UpdateCargoShipmentStatus updates the status of a shipment
func (h *Handler) UpdateCargoShipmentStatus(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid shipment ID")
		return
	}

	var req entity.UpdateShipmentStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Update shipment status
	query := `
		UPDATE cargo_shipments
		SET status = $1, updated_date = NOW()
		WHERE id = $2 AND tenant_id = $3
	`
	result, err := tx.Exec(query, req.Status, id, tenantID)
	if err != nil {
		h.log.Error("Failed to update shipment status", "error", err)
		response.InternalError(c, "Failed to update status")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Shipment not found")
		return
	}

	// Insert status history
	var changedByUUID *uuid.UUID
	if userID != uuid.Nil {
		changedByUUID = &userID
	}

	statusQuery := `
		INSERT INTO cargo_shipment_status_history (shipment_id, status, note, location, changed_by, changed_date)
		VALUES ($1, $2, $3, $4, $5, NOW())
	`
	_, err = tx.Exec(
		statusQuery, id, req.Status,
		sql.NullString{String: req.Note, Valid: req.Note != ""},
		sql.NullString{String: req.Location, Valid: req.Location != ""},
		changedByUUID,
	)
	if err != nil {
		h.log.Error("Failed to insert status history", "error", err)
		response.InternalError(c, "Failed to create status history")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	response.Success(c, gin.H{"message": "Status updated successfully"})
}

// DeleteCargoShipment deletes a cargo shipment
func (h *Handler) DeleteCargoShipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid shipment ID")
		return
	}

	query := `DELETE FROM cargo_shipments WHERE id = $1 AND tenant_id = $2`
	result, err := h.db.Exec(query, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete shipment", "error", err)
		response.InternalError(c, "Failed to delete shipment")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Shipment not found")
		return
	}

	response.Success(c, gin.H{"message": "Shipment deleted successfully"})
}

// =====================================================
// CARGO DISTRIBUTION HANDLERS
// =====================================================

// CreateCargoDistribution creates a distribution for a shipment
func (h *Handler) CreateCargoDistribution(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid shipment ID")
		return
	}

	var req entity.CreateDistributionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Calculate totals
	var totalItemsCost float64
	for _, item := range req.Items {
		totalItemsCost += item.Quantity * item.UnitCost
	}

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Insert distribution
	var createdByUUID *uuid.UUID
	if userID != uuid.Nil {
		createdByUUID = &userID
	}

	query := `
		INSERT INTO cargo_distributions (
			shipment_id, recipient_tenant_id, recipient_company_name, recipient_company_type,
			distribution_date, total_items_cost, allocated_costs,
			invoice_number, waybill_number, notes, created_by, created_date
		) VALUES ($1, $2, $3, $4, NOW(), $5, $6, $7, $8, $9, $10, NOW())
		RETURNING id
	`

	var distID int64
	err = tx.QueryRow(
		query, id, req.RecipientTenantID, req.RecipientCompanyName, req.RecipientCompanyType,
		totalItemsCost, 0, // allocated_costs will be 0 for now
		sql.NullString{String: req.InvoiceNumber, Valid: req.InvoiceNumber != ""},
		sql.NullString{String: req.WaybillNumber, Valid: req.WaybillNumber != ""},
		sql.NullString{String: req.Notes, Valid: req.Notes != ""},
		createdByUUID,
	).Scan(&distID)
	if err != nil {
		h.log.Error("Failed to insert distribution", "error", err)
		response.InternalError(c, "Failed to create distribution")
		return
	}

	// Insert distribution items
	itemQuery := `
		INSERT INTO cargo_distribution_items (distribution_id, shipment_item_id, quantity, unit_cost, created_date)
		VALUES ($1, $2, $3, $4, NOW())
	`
	for _, item := range req.Items {
		_, err = tx.Exec(itemQuery, distID, item.ShipmentItemID, item.Quantity, item.UnitCost)
		if err != nil {
			h.log.Error("Failed to insert distribution item", "error", err)
			response.InternalError(c, "Failed to create distribution items")
			return
		}
	}

	// Update shipment status to distributed
	_, err = tx.Exec(`UPDATE cargo_shipments SET status = 'distributed', updated_date = NOW() WHERE id = $1`, id)
	if err != nil {
		h.log.Error("Failed to update shipment status", "error", err)
		response.InternalError(c, "Failed to update shipment status")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	response.Created(c, gin.H{"id": distID})
}

// =====================================================
// CARGO CASH HANDLERS
// =====================================================

// ListCargoCashTransactions returns a list of cash transactions
func (h *Handler) ListCargoCashTransactions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Parse filters
	transactionType := c.Query("transaction_type")
	currency := c.Query("currency")

	paginate, page, pageSize, offset := optPagination(c)

	baseWhere := " WHERE tenant_id = $1"
	whereExtra := ""
	args := []interface{}{tenantID}
	argCount := 1

	if transactionType != "" {
		argCount++
		whereExtra += fmt.Sprintf(" AND transaction_type = $%d", argCount)
		args = append(args, transactionType)
	}

	if currency != "" {
		argCount++
		whereExtra += fmt.Sprintf(" AND currency = $%d", argCount)
		args = append(args, currency)
	}

	query := `
		SELECT id, tenant_id, transaction_type, amount, currency, category,
		       shipment_id, distribution_id, related_tenant_id, description, reference_number,
		       transaction_date, created_by, created_date
		FROM cargo_cash_transactions` + baseWhere + whereExtra + " ORDER BY transaction_date DESC"

	if paginate {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query cash transactions", "error", err)
		response.InternalError(c, "Failed to query transactions")
		return
	}
	defer rows.Close()

	transactions := []entity.CargoCashTransaction{}
	for rows.Next() {
		var t entity.CargoCashTransaction
		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.TransactionType, &t.Amount, &t.Currency, &t.Category,
			&t.ShipmentID, &t.DistributionID, &t.RelatedTenantID, &t.Description, &t.ReferenceNumber,
			&t.TransactionDate, &t.CreatedBy, &t.CreatedDate,
		); err != nil {
			h.log.Error("Failed to scan transaction", "error", err)
			continue
		}
		transactions = append(transactions, t)
	}

	if !paginate {
		response.Success(c, transactions)
		return
	}

	var total int
	_ = h.db.QueryRow("SELECT COUNT(*) FROM cargo_cash_transactions"+baseWhere+whereExtra, args[:argCount]...).Scan(&total)
	response.Paginated(c, transactions, page, pageSize, total)
}

// CreateCargoCashTransaction creates a new cash transaction
func (h *Handler) CreateCargoCashTransaction(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req entity.CreateCashTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	transactionDate := time.Now()
	if req.TransactionDate != nil {
		transactionDate = *req.TransactionDate
	}

	var createdByUUID *uuid.UUID
	if userID != uuid.Nil {
		createdByUUID = &userID
	}

	query := `
		INSERT INTO cargo_cash_transactions (
			tenant_id, transaction_type, amount, currency, category,
			shipment_id, distribution_id, related_tenant_id, description, reference_number,
			transaction_date, created_by, created_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
		RETURNING id
	`

	var transactionID int64
	err := h.db.QueryRow(
		query, tenantID, req.TransactionType, req.Amount, req.Currency, req.Category,
		req.ShipmentID, req.DistributionID, req.RelatedTenantID,
		sql.NullString{String: req.Description, Valid: req.Description != ""},
		sql.NullString{String: req.ReferenceNumber, Valid: req.ReferenceNumber != ""},
		transactionDate, createdByUUID,
	).Scan(&transactionID)
	if err != nil {
		h.log.Error("Failed to insert cash transaction", "error", err)
		response.InternalError(c, "Failed to create transaction")
		return
	}

	response.Created(c, gin.H{"id": transactionID})
}

// GetCargoCashSummary returns cash register summary
func (h *Handler) GetCargoCashSummary(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Calculate balances
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN currency = 'UZS' AND transaction_type = 'income' THEN amount ELSE 0 END), 0) -
			COALESCE(SUM(CASE WHEN currency = 'UZS' AND transaction_type = 'expense' THEN amount ELSE 0 END), 0) as uzs_balance,
			COALESCE(SUM(CASE WHEN currency = 'USD' AND transaction_type = 'income' THEN amount ELSE 0 END), 0) -
			COALESCE(SUM(CASE WHEN currency = 'USD' AND transaction_type = 'expense' THEN amount ELSE 0 END), 0) as usd_balance
		FROM cargo_cash_transactions
		WHERE tenant_id = $1
	`

	var summary entity.CargoCashSummary
	err := h.db.QueryRow(query, tenantID).Scan(&summary.UZSBalance, &summary.USDBalance)
	if err != nil {
		h.log.Error("Failed to calculate cash summary", "error", err)
		response.InternalError(c, "Failed to calculate summary")
		return
	}

	// Get recent transactions
	txQuery := `
		SELECT id, tenant_id, transaction_type, amount, currency, category,
		       shipment_id, distribution_id, related_tenant_id, description, reference_number,
		       transaction_date, created_by, created_date
		FROM cargo_cash_transactions
		WHERE tenant_id = $1
		ORDER BY transaction_date DESC
		LIMIT 100
	`

	rows, err := h.db.Query(txQuery, tenantID)
	if err != nil {
		h.log.Error("Failed to query transactions", "error", err)
		response.InternalError(c, "Failed to query transactions")
		return
	}
	defer rows.Close()

	summary.Transactions = []entity.CargoCashTransaction{}
	for rows.Next() {
		var t entity.CargoCashTransaction
		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.TransactionType, &t.Amount, &t.Currency, &t.Category,
			&t.ShipmentID, &t.DistributionID, &t.RelatedTenantID, &t.Description, &t.ReferenceNumber,
			&t.TransactionDate, &t.CreatedBy, &t.CreatedDate,
		); err != nil {
			h.log.Error("Failed to scan transaction", "error", err)
			continue
		}
		summary.Transactions = append(summary.Transactions, t)
	}

	response.Success(c, summary)
}

// UpdateCargoShipment updates an existing cargo shipment
func (h *Handler) UpdateCargoShipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid shipment ID")
		return
	}

	var req entity.CreateCargoShipmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Check if shipment exists and belongs to tenant
	var existingTenantID uuid.UUID
	checkQuery := `SELECT tenant_id FROM cargo_shipments WHERE id = $1`
	err = h.db.QueryRow(checkQuery, id).Scan(&existingTenantID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Shipment not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to check shipment", "error", err)
		response.InternalError(c, "Failed to check shipment")
		return
	}
	if existingTenantID != tenantID {
		response.Forbidden(c, "Access denied")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Update shipment (total_cost is a generated column, don't update it directly)
	updateQuery := `
		UPDATE cargo_shipments
		SET tracking_number = $1, supplier_country = $2, supplier_company = $3,
		    expected_date = $4, transport_cost = $5, customs_cost = $6,
		    insurance_cost = $7, other_cost = $8, notes = $9,
		    updated_date = NOW()
		WHERE id = $10 AND tenant_id = $11
	`
	_, err = tx.Exec(updateQuery,
		req.TrackingNumber, req.SupplierCountry,
		sql.NullString{String: req.SupplierCompany, Valid: req.SupplierCompany != ""},
		req.ExpectedDate, req.TransportCost, req.CustomsCost,
		req.InsuranceCost, req.OtherCost,
		sql.NullString{String: req.Notes, Valid: req.Notes != ""},
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update shipment", "error", err)
		response.InternalError(c, "Failed to update shipment")
		return
	}

	// Delete existing items
	_, err = tx.Exec(`DELETE FROM cargo_shipment_items WHERE shipment_id = $1`, id)
	if err != nil {
		h.log.Error("Failed to delete old items", "error", err)
		response.InternalError(c, "Failed to update items")
		return
	}

	// Insert new items (total_price is a generated column)
	itemQuery := `
		INSERT INTO cargo_shipment_items (shipment_id, item_name, quantity, unit_price, currency, hs_code, description, created_date, updated_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
	`
	for _, item := range req.Items {
		_, err = tx.Exec(itemQuery, id, item.ItemName, item.Quantity, item.UnitPrice, item.Currency,
			sql.NullString{String: item.HSCode, Valid: item.HSCode != ""},
			sql.NullString{String: item.Description, Valid: item.Description != ""})
		if err != nil {
			h.log.Error("Failed to insert shipment item", "error", err)
			response.InternalError(c, "Failed to update shipment items")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to commit transaction")
		return
	}

	response.Success(c, gin.H{"id": id, "message": "Shipment updated successfully"})
}

// UpdateCargoCashTransaction updates an existing cash transaction
func (h *Handler) UpdateCargoCashTransaction(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid transaction ID")
		return
	}

	var req entity.CreateCashTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Check if transaction exists and belongs to tenant
	var existingTenantID uuid.UUID
	checkQuery := `SELECT tenant_id FROM cargo_cash_transactions WHERE id = $1`
	err = h.db.QueryRow(checkQuery, id).Scan(&existingTenantID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transaction not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to check transaction", "error", err)
		response.InternalError(c, "Failed to check transaction")
		return
	}
	if existingTenantID != tenantID {
		response.Forbidden(c, "Access denied")
		return
	}

	// Update transaction
	updateQuery := `
		UPDATE cargo_cash_transactions
		SET transaction_type = $1, amount = $2, currency = $3, category = $4,
		    description = $5, related_tenant_id = $6, transaction_date = COALESCE($7, transaction_date)
		WHERE id = $8 AND tenant_id = $9
	`
	_, err = h.db.Exec(updateQuery,
		req.TransactionType, req.Amount, req.Currency, req.Category,
		req.Description, req.RelatedTenantID, req.TransactionDate,
		id, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update transaction", "error", err)
		response.InternalError(c, "Failed to update transaction")
		return
	}

	response.Success(c, gin.H{"id": id, "message": "Transaction updated successfully"})
}

// DeleteCargoCashTransaction deletes a cash transaction
func (h *Handler) DeleteCargoCashTransaction(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid transaction ID")
		return
	}

	// Check if transaction exists and belongs to tenant
	var existingTenantID uuid.UUID
	checkQuery := `SELECT tenant_id FROM cargo_cash_transactions WHERE id = $1`
	err = h.db.QueryRow(checkQuery, id).Scan(&existingTenantID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Transaction not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to check transaction", "error", err)
		response.InternalError(c, "Failed to check transaction")
		return
	}
	if existingTenantID != tenantID {
		response.Forbidden(c, "Access denied")
		return
	}

	// Delete transaction
	deleteQuery := `DELETE FROM cargo_cash_transactions WHERE id = $1 AND tenant_id = $2`
	_, err = h.db.Exec(deleteQuery, id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete transaction", "error", err)
		response.InternalError(c, "Failed to delete transaction")
		return
	}

	response.Success(c, gin.H{"message": "Transaction deleted successfully"})
}

// =====================================================
// HELPER FUNCTIONS
// =====================================================

func (h *Handler) loadShipmentItems(shipmentID int64) ([]entity.CargoShipmentItem, error) {
	query := `
		SELECT id, shipment_id, item_name, quantity, unit_price, currency, total_price,
		       hs_code, description, created_date, updated_date
		FROM cargo_shipment_items
		WHERE shipment_id = $1
	`

	rows, err := h.db.Query(query, shipmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []entity.CargoShipmentItem{}
	for rows.Next() {
		var item entity.CargoShipmentItem
		if err := rows.Scan(
			&item.ID, &item.ShipmentID, &item.ItemName, &item.Quantity, &item.UnitPrice, &item.Currency, &item.TotalPrice,
			&item.HSCode, &item.Description, &item.CreatedDate, &item.UpdatedDate,
		); err != nil {
			continue
		}
		items = append(items, item)
	}

	return items, nil
}

func (h *Handler) loadShipmentStatusHistory(shipmentID int64) ([]entity.CargoShipmentStatusHistory, error) {
	query := `
		SELECT id, shipment_id, status, note, location, changed_by, changed_date
		FROM cargo_shipment_status_history
		WHERE shipment_id = $1
		ORDER BY changed_date DESC
	`

	rows, err := h.db.Query(query, shipmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := []entity.CargoShipmentStatusHistory{}
	for rows.Next() {
		var h entity.CargoShipmentStatusHistory
		if err := rows.Scan(
			&h.ID, &h.ShipmentID, &h.Status, &h.Note, &h.Location, &h.ChangedBy, &h.ChangedDate,
		); err != nil {
			continue
		}
		history = append(history, h)
	}

	return history, nil
}
