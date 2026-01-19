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
)

// =====================================================
// CARGO SHIPMENT HANDLERS
// =====================================================

// ListCargoShipments returns a paginated list of cargo shipments
func (h *Handler) ListCargoShipments(c *gin.Context) {
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Parse filters
	status := c.Query("status")
	search := c.Query("search")

	// Build query
	baseQuery := `
		SELECT id, company_id, tracking_number, supplier_country, supplier_company,
		       transport_type, expected_date, actual_arrival_date, status,
		       transport_cost, customs_cost, insurance_cost, other_cost, total_cost,
		       notes, created_by, created_date, updated_date
		FROM cargo_shipments
		WHERE company_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM cargo_shipments WHERE company_id = $1`

	args := []interface{}{companyID}
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
	var total int64
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count shipments", "error", err)
		response.InternalServerError(c, "Failed to count shipments")
		return
	}

	// Get shipments
	baseQuery += " ORDER BY created_date DESC LIMIT $" + strconv.Itoa(argCount+1) + " OFFSET $" + strconv.Itoa(argCount+2)
	args = append(args, limit, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to query shipments", "error", err)
		response.InternalServerError(c, "Failed to query shipments")
		return
	}
	defer rows.Close()

	shipments := []entity.CargoShipment{}
	for rows.Next() {
		var s entity.CargoShipment
		if err := rows.Scan(
			&s.ID, &s.CompanyID, &s.TrackingNumber, &s.SupplierCountry, &s.SupplierCompany,
			&s.TransportType, &s.ExpectedDate, &s.ActualArrivalDate, &s.Status,
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

	response.SuccessWithPagination(c, shipments, total, page, limit)
}

// GetCargoShipment returns a single cargo shipment by ID
func (h *Handler) GetCargoShipment(c *gin.Context) {
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid shipment ID")
		return
	}

	query := `
		SELECT id, company_id, tracking_number, supplier_country, supplier_company,
		       transport_type, expected_date, actual_arrival_date, status,
		       transport_cost, customs_cost, insurance_cost, other_cost, total_cost,
		       notes, created_by, created_date, updated_date
		FROM cargo_shipments
		WHERE id = $1 AND company_id = $2
	`

	var shipment entity.CargoShipment
	err = h.db.QueryRow(query, id, companyID).Scan(
		&shipment.ID, &shipment.CompanyID, &shipment.TrackingNumber, &shipment.SupplierCountry, &shipment.SupplierCompany,
		&shipment.TransportType, &shipment.ExpectedDate, &shipment.ActualArrivalDate, &shipment.Status,
		&shipment.TransportCost, &shipment.CustomsCost, &shipment.InsuranceCost, &shipment.OtherCost, &shipment.TotalCost,
		&shipment.Notes, &shipment.CreatedBy, &shipment.CreatedDate, &shipment.UpdatedDate,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Shipment not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to query shipment", "error", err)
		response.InternalServerError(c, "Failed to query shipment")
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
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req entity.CreateCargoShipmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Calculate total cost
	totalCost := req.TransportCost + req.CustomsCost + req.InsuranceCost + req.OtherCost

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalServerError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Insert shipment
	query := `
		INSERT INTO cargo_shipments (
			company_id, tracking_number, supplier_country, supplier_company,
			transport_type, expected_date, status,
			transport_cost, customs_cost, insurance_cost, other_cost,
			notes, created_by, created_date, updated_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW())
		RETURNING id
	`

	var shipmentID int64
	err = tx.QueryRow(
		query, companyID, req.TrackingNumber, req.SupplierCountry, sql.NullString{String: req.SupplierCompany, Valid: req.SupplierCompany != ""},
		req.TransportType, req.ExpectedDate, "ordered",
		req.TransportCost, req.CustomsCost, req.InsuranceCost, req.OtherCost,
		sql.NullString{String: req.Notes, Valid: req.Notes != ""}, sql.NullInt64{Int64: userID, Valid: userID != 0},
	).Scan(&shipmentID)
	if err != nil {
		h.log.Error("Failed to insert shipment", "error", err)
		response.InternalServerError(c, "Failed to create shipment")
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
			response.InternalServerError(c, "Failed to create shipment items")
			return
		}
	}

	// Insert initial status history
	statusQuery := `
		INSERT INTO cargo_shipment_status_history (shipment_id, status, note, changed_by, changed_date)
		VALUES ($1, $2, $3, $4, NOW())
	`
	_, err = tx.Exec(statusQuery, shipmentID, "ordered", "Shipment created", sql.NullInt64{Int64: userID, Valid: userID != 0})
	if err != nil {
		h.log.Error("Failed to insert status history", "error", err)
		response.InternalServerError(c, "Failed to create status history")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "Failed to commit transaction")
		return
	}

	response.Created(c, gin.H{"id": shipmentID})
}

// UpdateCargoShipmentStatus updates the status of a shipment
func (h *Handler) UpdateCargoShipmentStatus(c *gin.Context) {
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
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
		response.BadRequest(c, err.Error())
		return
	}

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalServerError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Update shipment status
	query := `
		UPDATE cargo_shipments
		SET status = $1, updated_date = NOW()
		WHERE id = $2 AND company_id = $3
	`
	result, err := tx.Exec(query, req.Status, id, companyID)
	if err != nil {
		h.log.Error("Failed to update shipment status", "error", err)
		response.InternalServerError(c, "Failed to update status")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Shipment not found")
		return
	}

	// Insert status history
	statusQuery := `
		INSERT INTO cargo_shipment_status_history (shipment_id, status, note, location, changed_by, changed_date)
		VALUES ($1, $2, $3, $4, $5, NOW())
	`
	_, err = tx.Exec(
		statusQuery, id, req.Status,
		sql.NullString{String: req.Note, Valid: req.Note != ""},
		sql.NullString{String: req.Location, Valid: req.Location != ""},
		sql.NullInt64{Int64: userID, Valid: userID != 0},
	)
	if err != nil {
		h.log.Error("Failed to insert status history", "error", err)
		response.InternalServerError(c, "Failed to create status history")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "Failed to commit transaction")
		return
	}

	response.Success(c, gin.H{"message": "Status updated successfully"})
}

// DeleteCargoShipment deletes a cargo shipment
func (h *Handler) DeleteCargoShipment(c *gin.Context) {
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid shipment ID")
		return
	}

	query := `DELETE FROM cargo_shipments WHERE id = $1 AND company_id = $2`
	result, err := h.db.Exec(query, id, companyID)
	if err != nil {
		h.log.Error("Failed to delete shipment", "error", err)
		response.InternalServerError(c, "Failed to delete shipment")
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
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
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
		response.BadRequest(c, err.Error())
		return
	}

	// Calculate totals
	var totalItemsCost, allocatedCosts float64
	for _, item := range req.Items {
		totalItemsCost += item.Quantity * item.UnitCost
	}

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		response.InternalServerError(c, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Insert distribution
	query := `
		INSERT INTO cargo_distributions (
			shipment_id, recipient_company_id, recipient_company_name, recipient_company_type,
			distribution_date, total_items_cost, allocated_costs,
			invoice_number, waybill_number, notes, created_by, created_date
		) VALUES ($1, $2, $3, $4, NOW(), $5, $6, $7, $8, $9, $10, NOW())
		RETURNING id
	`

	var distID int64
	err = tx.QueryRow(
		query, id, req.RecipientCompanyID, req.RecipientCompanyName, req.RecipientCompanyType,
		totalItemsCost, allocatedCosts,
		sql.NullString{String: req.InvoiceNumber, Valid: req.InvoiceNumber != ""},
		sql.NullString{String: req.WaybillNumber, Valid: req.WaybillNumber != ""},
		sql.NullString{String: req.Notes, Valid: req.Notes != ""},
		sql.NullInt64{Int64: userID, Valid: userID != 0},
	).Scan(&distID)
	if err != nil {
		h.log.Error("Failed to insert distribution", "error", err)
		response.InternalServerError(c, "Failed to create distribution")
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
			response.InternalServerError(c, "Failed to create distribution items")
			return
		}
	}

	// Update shipment status to distributed
	_, err = tx.Exec(`UPDATE cargo_shipments SET status = 'distributed', updated_date = NOW() WHERE id = $1`, id)
	if err != nil {
		h.log.Error("Failed to update shipment status", "error", err)
		response.InternalServerError(c, "Failed to update shipment status")
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "Failed to commit transaction")
		return
	}

	response.Created(c, gin.H{"id": distID})
}

// =====================================================
// CARGO CASH HANDLERS
// =====================================================

// ListCargoCashTransactions returns a list of cash transactions
func (h *Handler) ListCargoCashTransactions(c *gin.Context) {
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
		return
	}

	// Parse filters
	transactionType := c.Query("transaction_type")
	currency := c.Query("currency")

	query := `
		SELECT id, company_id, transaction_type, amount, currency, category,
		       shipment_id, distribution_id, related_company_id, description, reference_number,
		       transaction_date, created_by, created_date
		FROM cargo_cash_transactions
		WHERE company_id = $1
	`
	args := []interface{}{companyID}
	argCount := 1

	if transactionType != "" {
		argCount++
		query += fmt.Sprintf(" AND transaction_type = $%d", argCount)
		args = append(args, transactionType)
	}

	if currency != "" {
		argCount++
		query += fmt.Sprintf(" AND currency = $%d", argCount)
		args = append(args, currency)
	}

	query += " ORDER BY transaction_date DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query cash transactions", "error", err)
		response.InternalServerError(c, "Failed to query transactions")
		return
	}
	defer rows.Close()

	transactions := []entity.CargoCashTransaction{}
	for rows.Next() {
		var t entity.CargoCashTransaction
		if err := rows.Scan(
			&t.ID, &t.CompanyID, &t.TransactionType, &t.Amount, &t.Currency, &t.Category,
			&t.ShipmentID, &t.DistributionID, &t.RelatedCompanyID, &t.Description, &t.ReferenceNumber,
			&t.TransactionDate, &t.CreatedBy, &t.CreatedDate,
		); err != nil {
			h.log.Error("Failed to scan transaction", "error", err)
			continue
		}
		transactions = append(transactions, t)
	}

	response.Success(c, transactions)
}

// CreateCargoCashTransaction creates a new cash transaction
func (h *Handler) CreateCargoCashTransaction(c *gin.Context) {
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var req entity.CreateCashTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	transactionDate := time.Now()
	if req.TransactionDate != nil {
		transactionDate = *req.TransactionDate
	}

	query := `
		INSERT INTO cargo_cash_transactions (
			company_id, transaction_type, amount, currency, category,
			shipment_id, distribution_id, related_company_id, description, reference_number,
			transaction_date, created_by, created_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
		RETURNING id
	`

	var transactionID int64
	err := h.db.QueryRow(
		query, companyID, req.TransactionType, req.Amount, req.Currency, req.Category,
		req.ShipmentID, req.DistributionID, req.RelatedCompanyID,
		sql.NullString{String: req.Description, Valid: req.Description != ""},
		sql.NullString{String: req.ReferenceNumber, Valid: req.ReferenceNumber != ""},
		transactionDate, sql.NullInt64{Int64: userID, Valid: userID != 0},
	).Scan(&transactionID)
	if err != nil {
		h.log.Error("Failed to insert cash transaction", "error", err)
		response.InternalServerError(c, "Failed to create transaction")
		return
	}

	response.Created(c, gin.H{"id": transactionID})
}

// GetCargoCashSummary returns cash register summary
func (h *Handler) GetCargoCashSummary(c *gin.Context) {
	companyID, ok := middleware.GetCompanyID(c)
	if !ok || companyID == 0 {
		response.Unauthorized(c, "Company not found")
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
		WHERE company_id = $1
	`

	var summary entity.CargoCashSummary
	err := h.db.QueryRow(query, companyID).Scan(&summary.UZSBalance, &summary.USDBalance)
	if err != nil {
		h.log.Error("Failed to calculate cash summary", "error", err)
		response.InternalServerError(c, "Failed to calculate summary")
		return
	}

	// Get recent transactions
	txQuery := `
		SELECT id, company_id, transaction_type, amount, currency, category,
		       shipment_id, distribution_id, related_company_id, description, reference_number,
		       transaction_date, created_by, created_date
		FROM cargo_cash_transactions
		WHERE company_id = $1
		ORDER BY transaction_date DESC
		LIMIT 100
	`

	rows, err := h.db.Query(txQuery, companyID)
	if err != nil {
		h.log.Error("Failed to query transactions", "error", err)
		response.InternalServerError(c, "Failed to query transactions")
		return
	}
	defer rows.Close()

	summary.Transactions = []entity.CargoCashTransaction{}
	for rows.Next() {
		var t entity.CargoCashTransaction
		if err := rows.Scan(
			&t.ID, &t.CompanyID, &t.TransactionType, &t.Amount, &t.Currency, &t.Category,
			&t.ShipmentID, &t.DistributionID, &t.RelatedCompanyID, &t.Description, &t.ReferenceNumber,
			&t.TransactionDate, &t.CreatedBy, &t.CreatedDate,
		); err != nil {
			h.log.Error("Failed to scan transaction", "error", err)
			continue
		}
		summary.Transactions = append(summary.Transactions, t)
	}

	response.Success(c, summary)
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
