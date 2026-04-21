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
// LANDED COST TYPES
// ============================================

type LandedCostType struct {
	ID                      uuid.UUID `json:"id"`
	TenantID                uuid.UUID `json:"tenant_id"`
	Name                    string    `json:"name"`
	Code                    string    `json:"code,omitempty"`
	Description             string    `json:"description,omitempty"`
	DefaultAllocationMethod string    `json:"default_allocation_method"`
	ExpenseAccountID        *string   `json:"expense_account_id,omitempty"`
	IsActive                bool      `json:"is_active"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type LandedCostTypeInput struct {
	Name                    string  `json:"name" binding:"required"`
	Code                    string  `json:"code,omitempty"`
	Description             string  `json:"description,omitempty"`
	DefaultAllocationMethod string  `json:"default_allocation_method,omitempty"`
	ExpenseAccountID        *string `json:"expense_account_id,omitempty"`
	IsActive                *bool   `json:"is_active,omitempty"`
}

// ListLandedCostTypes returns all landed cost types
func (h *Handler) ListLandedCostTypes(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)

	// Ensure default types exist
	h.ensureDefaultLandedCostTypes(tenantID)

	query := `
		SELECT id, tenant_id, name, code, description, default_allocation_method,
			   expense_account_id, is_active, created_at, updated_at
		FROM landed_cost_types
		WHERE tenant_id = $1`

	args := []interface{}{tenantID}

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND (organization_id = $2 OR organization_id IS NULL)"
		args = append(args, orgID)
	}

	query += " ORDER BY name"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to fetch landed cost types", "error", err)
		response.InternalError(c, "Failed to fetch landed cost types")
		return
	}
	defer rows.Close()

	var types []LandedCostType
	for rows.Next() {
		var t LandedCostType
		var code, description, expenseAccountID sql.NullString

		err := rows.Scan(
			&t.ID, &t.TenantID, &t.Name, &code, &description,
			&t.DefaultAllocationMethod, &expenseAccountID, &t.IsActive,
			&t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if code.Valid {
			t.Code = code.String
		}
		if description.Valid {
			t.Description = description.String
		}
		if expenseAccountID.Valid {
			t.ExpenseAccountID = &expenseAccountID.String
		}

		types = append(types, t)
	}

	response.Success(c, types)
}

// CreateLandedCostType creates a new landed cost type
func (h *Handler) CreateLandedCostType(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)

	var input LandedCostTypeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()

	allocMethod := "by_value"
	if input.DefaultAllocationMethod != "" {
		allocMethod = input.DefaultAllocationMethod
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	_, err := h.db.Exec(`
		INSERT INTO landed_cost_types (
			id, tenant_id, name, code, description, default_allocation_method,
			expense_account_id, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		id, tenantID, input.Name, input.Code, input.Description,
		allocMethod, input.ExpenseAccountID, isActive, now,
	)
	if err != nil {
		h.log.Error("Failed to create landed cost type", "error", err)
		response.InternalError(c, "Failed to create landed cost type")
		return
	}

	t := LandedCostType{
		ID:                      id,
		TenantID:                tenantID,
		Name:                    input.Name,
		Code:                    input.Code,
		Description:             input.Description,
		DefaultAllocationMethod: allocMethod,
		ExpenseAccountID:        input.ExpenseAccountID,
		IsActive:                isActive,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	response.Success(c, t)
}

// ensureDefaultLandedCostTypes creates default cost types if none exist
func (h *Handler) ensureDefaultLandedCostTypes(tenantID uuid.UUID) {
	var count int
	h.db.QueryRow("SELECT COUNT(*) FROM landed_cost_types WHERE tenant_id = $1", tenantID).Scan(&count)

	if count > 0 {
		return
	}

	now := time.Now()
	defaultTypes := []struct {
		Name        string
		Code        string
		Description string
		Method      string
	}{
		{"Freight / Shipping", "FREIGHT", "Transportation and shipping costs", "by_value"},
		{"Customs Duties", "CUSTOMS", "Import duties and tariffs", "by_value"},
		{"Insurance", "INSURANCE", "Transit insurance", "by_value"},
		{"Handling Fees", "HANDLING", "Port handling and warehousing", "by_quantity"},
		{"Brokerage", "BROKERAGE", "Customs broker fees", "equal"},
		{"Inspection Fees", "INSPECT", "Quality inspection fees", "by_quantity"},
		{"Other", "OTHER", "Other landed costs", "by_value"},
	}

	for _, dt := range defaultTypes {
		id := uuid.New()
		h.db.Exec(`
			INSERT INTO landed_cost_types (id, tenant_id, name, code, description, default_allocation_method, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7, $7)`,
			id, tenantID, dt.Name, dt.Code, dt.Description, dt.Method, now,
		)
	}
}

// ============================================
// LANDED COSTS
// ============================================

type LandedCost struct {
	ID               uuid.UUID             `json:"id"`
	TenantID         uuid.UUID             `json:"tenant_id"`
	LandedCostNumber string                `json:"landed_cost_number"`
	GoodsReceiptID   uuid.UUID             `json:"goods_receipt_id"`
	GRNumber         string                `json:"gr_number,omitempty"`
	PurchaseOrderID  *uuid.UUID            `json:"purchase_order_id,omitempty"`
	PONumber         string                `json:"po_number,omitempty"`
	SupplierID       *uuid.UUID            `json:"supplier_id,omitempty"`
	SupplierName     string                `json:"supplier_name,omitempty"`
	CostDate         string                `json:"cost_date"`
	ProductValue     float64               `json:"product_value"`
	TotalLandedCost  float64               `json:"total_landed_cost"`
	TotalCost        float64               `json:"total_cost"`
	Status           string                `json:"status"`
	AllocationMethod string                `json:"allocation_method"`
	Notes            string                `json:"notes,omitempty"`
	CreatedBy        *uuid.UUID            `json:"created_by,omitempty"`
	ValidatedBy      *uuid.UUID            `json:"validated_by,omitempty"`
	ValidatedAt      *time.Time            `json:"validated_at,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
	Lines            []LandedCostLine      `json:"lines,omitempty"`
	Allocations      []LandedCostAllocSum  `json:"allocations,omitempty"`
	GRLines          []GRLineForAllocation `json:"gr_lines,omitempty"`
}

type LandedCostLine struct {
	ID               uuid.UUID  `json:"id"`
	LandedCostID     uuid.UUID  `json:"landed_cost_id"`
	LineNumber       int        `json:"line_number"`
	CostTypeID       *uuid.UUID `json:"cost_type_id,omitempty"`
	CostTypeName     string     `json:"cost_type_name"`
	VendorID         *uuid.UUID `json:"vendor_id,omitempty"`
	VendorName       string     `json:"vendor_name,omitempty"`
	Amount           float64    `json:"amount"`
	Currency         string     `json:"currency"`
	Reference        string     `json:"reference,omitempty"`
	AllocationMethod string     `json:"allocation_method,omitempty"`
	Notes            string     `json:"notes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type LandedCostAllocSum struct {
	ProductID        uuid.UUID `json:"product_id"`
	ProductName      string    `json:"product_name"`
	Quantity         float64   `json:"quantity"`
	OriginalUnitCost float64   `json:"original_unit_cost"`
	OriginalTotal    float64   `json:"original_total"`
	AllocatedAmount  float64   `json:"allocated_amount"`
	NewUnitCost      float64   `json:"new_unit_cost"`
	NewTotal         float64   `json:"new_total"`
}

type GRLineForAllocation struct {
	ID          uuid.UUID `json:"id"`
	ProductID   uuid.UUID `json:"product_id"`
	ProductName string    `json:"product_name"`
	Quantity    float64   `json:"quantity"`
	UnitPrice   float64   `json:"unit_price"`
	TotalValue  float64   `json:"total_value"`
}

type LandedCostInput struct {
	GoodsReceiptID   string `json:"goods_receipt_id" binding:"required"`
	CostDate         string `json:"cost_date,omitempty"`
	AllocationMethod string `json:"allocation_method,omitempty"`
	Notes            string `json:"notes,omitempty"`
	Lines            []struct {
		CostTypeID       string  `json:"cost_type_id,omitempty"`
		CostTypeName     string  `json:"cost_type_name" binding:"required"`
		VendorID         string  `json:"vendor_id,omitempty"`
		VendorName       string  `json:"vendor_name,omitempty"`
		Amount           float64 `json:"amount" binding:"required"`
		Currency         string  `json:"currency,omitempty"`
		Reference        string  `json:"reference,omitempty"`
		AllocationMethod string  `json:"allocation_method,omitempty"`
		Notes            string  `json:"notes,omitempty"`
	} `json:"lines" binding:"required,min=1"`
}

// ListLandedCosts returns all landed costs
func (h *Handler) ListLandedCosts(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	grID := c.Query("goods_receipt_id")

	query := `
		SELECT id, tenant_id, landed_cost_number, goods_receipt_id, gr_number,
			   purchase_order_id, po_number, supplier_id, supplier_name,
			   cost_date, product_value, total_landed_cost, total_cost,
			   status, allocation_method, notes, created_by, validated_by,
			   validated_at, created_at, updated_at
		FROM landed_costs
		WHERE tenant_id = $1 AND deleted_at IS NULL`

	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	if grID != "" {
		argCount++
		query += fmt.Sprintf(" AND goods_receipt_id = $%d", argCount)
		args = append(args, grID)
	}

	query += " ORDER BY created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to fetch landed costs", "error", err)
		response.InternalError(c, "Failed to fetch landed costs")
		return
	}
	defer rows.Close()

	var costs []LandedCost
	for rows.Next() {
		var lc LandedCost
		var poID, supplierID, createdBy, validatedBy sql.NullString
		var grNumber, poNumber, supplierName, notes sql.NullString
		var validatedAt sql.NullTime
		var costDate time.Time

		err := rows.Scan(
			&lc.ID, &lc.TenantID, &lc.LandedCostNumber, &lc.GoodsReceiptID, &grNumber,
			&poID, &poNumber, &supplierID, &supplierName,
			&costDate, &lc.ProductValue, &lc.TotalLandedCost, &lc.TotalCost,
			&lc.Status, &lc.AllocationMethod, &notes, &createdBy, &validatedBy,
			&validatedAt, &lc.CreatedAt, &lc.UpdatedAt,
		)
		if err != nil {
			continue
		}

		lc.CostDate = costDate.Format("2006-01-02")
		if grNumber.Valid {
			lc.GRNumber = grNumber.String
		}
		if poID.Valid {
			if uid, err := uuid.Parse(poID.String); err == nil {
				lc.PurchaseOrderID = &uid
			}
		}
		if poNumber.Valid {
			lc.PONumber = poNumber.String
		}
		if supplierID.Valid {
			if uid, err := uuid.Parse(supplierID.String); err == nil {
				lc.SupplierID = &uid
			}
		}
		if supplierName.Valid {
			lc.SupplierName = supplierName.String
		}
		if notes.Valid {
			lc.Notes = notes.String
		}
		if validatedAt.Valid {
			lc.ValidatedAt = &validatedAt.Time
		}

		costs = append(costs, lc)
	}

	response.Success(c, costs)
}

// CreateLandedCost creates a new landed cost record
func (h *Handler) CreateLandedCost(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)

	// Get organization ID from middleware header
	lcOrgID, _ := middleware.GetOrganizationID(c)
	var lcOrgIDPtr *uuid.UUID
	if lcOrgID != uuid.Nil {
		lcOrgIDPtr = &lcOrgID
	}

	var input LandedCostInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	grID, err := uuid.Parse(input.GoodsReceiptID)
	if err != nil {
		response.Error(c, 400, "Invalid goods receipt ID", "")
		return
	}

	// Get GR info
	var grNumber, poNumber, supplierName string
	var poID, supplierID sql.NullString
	var productValue float64

	err = h.db.QueryRow(`
		SELECT gr.gr_number, gr.po_number, gr.purchase_order_id, gr.supplier_id, gr.supplier_name,
			   COALESCE(SUM(grl.accepted_quantity * grl.unit_price), 0) as product_value
		FROM goods_receipts gr
		LEFT JOIN goods_receipt_lines grl ON grl.goods_receipt_id = gr.id
		WHERE gr.id = $1 AND gr.tenant_id = $2 AND gr.deleted_at IS NULL
		GROUP BY gr.id`, grID, tenantID).Scan(&grNumber, &poNumber, &poID, &supplierID, &supplierName, &productValue)

	if err == sql.ErrNoRows {
		response.Error(c, 404, "Goods receipt not found", "")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch goods receipt", "error", err)
		response.InternalError(c, "Failed to fetch goods receipt")
		return
	}

	// Generate number
	var count int
	h.db.QueryRow("SELECT COUNT(*) FROM landed_costs WHERE tenant_id = $1", tenantID).Scan(&count)
	lcNumber := fmt.Sprintf("LC-%s-%04d", time.Now().Format("200601"), count+1)

	id := uuid.New()
	now := time.Now()

	costDate := now
	if input.CostDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.CostDate); err == nil {
			costDate = parsed
		}
	}

	allocMethod := "by_value"
	if input.AllocationMethod != "" {
		allocMethod = input.AllocationMethod
	}

	// Calculate total landed cost
	var totalLandedCost float64
	for _, line := range input.Lines {
		totalLandedCost += line.Amount
	}

	// Insert main record
	var poIDVal, supplierIDVal *uuid.UUID
	if poID.Valid {
		if uid, err := uuid.Parse(poID.String); err == nil {
			poIDVal = &uid
		}
	}
	if supplierID.Valid {
		if uid, err := uuid.Parse(supplierID.String); err == nil {
			supplierIDVal = &uid
		}
	}

	_, err = h.db.Exec(`
		INSERT INTO landed_costs (
			id, tenant_id, organization_id, landed_cost_number, goods_receipt_id, gr_number,
			purchase_order_id, po_number, supplier_id, supplier_name,
			cost_date, product_value, total_landed_cost, total_cost,
			status, allocation_method, notes, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $19)`,
		id, tenantID, lcOrgIDPtr, lcNumber, grID, grNumber,
		poIDVal, poNumber, supplierIDVal, supplierName,
		costDate, productValue, totalLandedCost, productValue+totalLandedCost,
		"draft", allocMethod, input.Notes, userID, now,
	)
	if err != nil {
		h.log.Error("Failed to create landed cost", "error", err)
		response.InternalError(c, "Failed to create landed cost")
		return
	}

	// Insert lines
	for i, lineInput := range input.Lines {
		lineID := uuid.New()

		var costTypeID *uuid.UUID
		if lineInput.CostTypeID != "" {
			if uid, err := uuid.Parse(lineInput.CostTypeID); err == nil {
				costTypeID = &uid
			}
		}

		var vendorID *uuid.UUID
		if lineInput.VendorID != "" {
			if uid, err := uuid.Parse(lineInput.VendorID); err == nil {
				vendorID = &uid
			}
		}

		currency := "UZS"
		if lineInput.Currency != "" {
			currency = lineInput.Currency
		}

		h.db.Exec(`
			INSERT INTO landed_cost_lines (
				id, landed_cost_id, line_number, cost_type_id, cost_type_name,
				vendor_id, vendor_name, amount, currency, reference,
				allocation_method, notes, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			lineID, id, i+1, costTypeID, lineInput.CostTypeName,
			vendorID, lineInput.VendorName, lineInput.Amount, currency, lineInput.Reference,
			lineInput.AllocationMethod, lineInput.Notes, now,
		)
	}

	// Calculate and create allocations
	h.calculateLandedCostAllocations(id, grID, allocMethod)

	// Return the created record
	h.GetLandedCost(c)
}

// GetLandedCost returns a single landed cost with lines and allocations
func (h *Handler) GetLandedCost(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	lcID := c.Param("id")

	var id uuid.UUID
	var err error

	// Try to parse as UUID, if not use goods_receipt_id query
	id, err = uuid.Parse(lcID)
	if err != nil {
		// Check if it's a goods_receipt_id query
		grID := c.Query("goods_receipt_id")
		if grID == "" {
			response.Error(c, 400, "Invalid landed cost ID", "")
			return
		}
		// Get the latest landed cost for this GR
		err = h.db.QueryRow(`
			SELECT id FROM landed_costs
			WHERE goods_receipt_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
			ORDER BY created_at DESC LIMIT 1`, grID, tenantID).Scan(&id)
		if err == sql.ErrNoRows {
			response.Error(c, 404, "Landed cost not found", "")
			return
		}
	}

	// Get main record
	var lc LandedCost
	var poID, supplierID, createdBy, validatedBy sql.NullString
	var grNumber, poNumber, supplierName, notes sql.NullString
	var validatedAt sql.NullTime
	var costDate time.Time

	err = h.db.QueryRow(`
		SELECT id, tenant_id, landed_cost_number, goods_receipt_id, gr_number,
			   purchase_order_id, po_number, supplier_id, supplier_name,
			   cost_date, product_value, total_landed_cost, total_cost,
			   status, allocation_method, notes, created_by, validated_by,
			   validated_at, created_at, updated_at
		FROM landed_costs
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		id, tenantID).Scan(
		&lc.ID, &lc.TenantID, &lc.LandedCostNumber, &lc.GoodsReceiptID, &grNumber,
		&poID, &poNumber, &supplierID, &supplierName,
		&costDate, &lc.ProductValue, &lc.TotalLandedCost, &lc.TotalCost,
		&lc.Status, &lc.AllocationMethod, &notes, &createdBy, &validatedBy,
		&validatedAt, &lc.CreatedAt, &lc.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		response.Error(c, 404, "Landed cost not found", "")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch landed cost", "error", err)
		response.InternalError(c, "Failed to fetch landed cost")
		return
	}

	lc.CostDate = costDate.Format("2006-01-02")
	if grNumber.Valid {
		lc.GRNumber = grNumber.String
	}
	if poID.Valid {
		if uid, err := uuid.Parse(poID.String); err == nil {
			lc.PurchaseOrderID = &uid
		}
	}
	if poNumber.Valid {
		lc.PONumber = poNumber.String
	}
	if supplierID.Valid {
		if uid, err := uuid.Parse(supplierID.String); err == nil {
			lc.SupplierID = &uid
		}
	}
	if supplierName.Valid {
		lc.SupplierName = supplierName.String
	}
	if notes.Valid {
		lc.Notes = notes.String
	}
	if validatedAt.Valid {
		lc.ValidatedAt = &validatedAt.Time
	}

	// Get lines
	lineRows, err := h.db.Query(`
		SELECT id, landed_cost_id, line_number, cost_type_id, cost_type_name,
			   vendor_id, vendor_name, amount, currency, reference,
			   allocation_method, notes, created_at
		FROM landed_cost_lines
		WHERE landed_cost_id = $1
		ORDER BY line_number`, id)
	if err == nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var line LandedCostLine
			var costTypeID, vendorID sql.NullString
			var vendorName, reference, allocMethod, lineNotes sql.NullString

			err := lineRows.Scan(
				&line.ID, &line.LandedCostID, &line.LineNumber, &costTypeID, &line.CostTypeName,
				&vendorID, &vendorName, &line.Amount, &line.Currency, &reference,
				&allocMethod, &lineNotes, &line.CreatedAt,
			)
			if err != nil {
				continue
			}

			if costTypeID.Valid {
				if uid, err := uuid.Parse(costTypeID.String); err == nil {
					line.CostTypeID = &uid
				}
			}
			if vendorID.Valid {
				if uid, err := uuid.Parse(vendorID.String); err == nil {
					line.VendorID = &uid
				}
			}
			if vendorName.Valid {
				line.VendorName = vendorName.String
			}
			if reference.Valid {
				line.Reference = reference.String
			}
			if allocMethod.Valid {
				line.AllocationMethod = allocMethod.String
			}
			if lineNotes.Valid {
				line.Notes = lineNotes.String
			}

			lc.Lines = append(lc.Lines, line)
		}
	}

	// Get allocations summary (grouped by product)
	allocRows, err := h.db.Query(`
		SELECT product_id, product_name,
			   SUM(quantity) as quantity,
			   AVG(original_unit_cost) as original_unit_cost,
			   SUM(original_total_cost) as original_total,
			   SUM(allocated_amount) as allocated_amount,
			   AVG(new_unit_cost) as new_unit_cost,
			   SUM(new_total_cost) as new_total
		FROM landed_cost_allocations
		WHERE landed_cost_id = $1
		GROUP BY product_id, product_name`, id)
	if err == nil {
		defer allocRows.Close()
		for allocRows.Next() {
			var alloc LandedCostAllocSum
			err := allocRows.Scan(
				&alloc.ProductID, &alloc.ProductName,
				&alloc.Quantity, &alloc.OriginalUnitCost, &alloc.OriginalTotal,
				&alloc.AllocatedAmount, &alloc.NewUnitCost, &alloc.NewTotal,
			)
			if err != nil {
				continue
			}
			lc.Allocations = append(lc.Allocations, alloc)
		}
	}

	// Get GR lines for reference
	grLineRows, err := h.db.Query(`
		SELECT id, COALESCE(product_id, '00000000-0000-0000-0000-000000000000'::uuid),
			   product_name, accepted_quantity, unit_price,
			   accepted_quantity * unit_price as total_value
		FROM goods_receipt_lines
		WHERE goods_receipt_id = $1 AND accepted_quantity > 0`, lc.GoodsReceiptID)
	if err == nil {
		defer grLineRows.Close()
		for grLineRows.Next() {
			var grLine GRLineForAllocation
			err := grLineRows.Scan(
				&grLine.ID, &grLine.ProductID, &grLine.ProductName,
				&grLine.Quantity, &grLine.UnitPrice, &grLine.TotalValue,
			)
			if err != nil {
				continue
			}
			lc.GRLines = append(lc.GRLines, grLine)
		}
	}

	response.Success(c, lc)
}

// ValidateLandedCost validates and applies landed cost allocations to inventory
func (h *Handler) ValidateLandedCost(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)
	lcID := c.Param("id")

	id, err := uuid.Parse(lcID)
	if err != nil {
		response.Error(c, 400, "Invalid landed cost ID", "")
		return
	}

	// Get current status
	var status string
	var grID uuid.UUID
	err = h.db.QueryRow(`
		SELECT status, goods_receipt_id FROM landed_costs
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		id, tenantID).Scan(&status, &grID)
	if err == sql.ErrNoRows {
		response.Error(c, 404, "Landed cost not found", "")
		return
	}

	if status != "draft" {
		response.Error(c, 400, "Only draft landed costs can be validated", "")
		return
	}

	now := time.Now()

	// Update inventory costs based on allocations
	allocRows, err := h.db.Query(`
		SELECT DISTINCT product_id, SUM(allocated_amount) as total_allocated,
			   AVG(new_unit_cost) as new_unit_cost
		FROM landed_cost_allocations
		WHERE landed_cost_id = $1
		GROUP BY product_id`, id)
	if err == nil {
		defer allocRows.Close()

		// Get warehouse from GR
		var warehouseID sql.NullString
		h.db.QueryRow("SELECT warehouse_id FROM goods_receipts WHERE id = $1 AND tenant_id = $2", grID, tenantID).Scan(&warehouseID)

		for allocRows.Next() {
			var productID uuid.UUID
			var totalAllocated, newUnitCost float64

			if err := allocRows.Scan(&productID, &totalAllocated, &newUnitCost); err != nil {
				continue
			}

			if !warehouseID.Valid {
				continue
			}

			whID, err := uuid.Parse(warehouseID.String)
			if err != nil {
				continue
			}

			// Update inventory unit_cost
			_, err = h.db.Exec(`
				UPDATE inventory
				SET unit_cost = $1, updated_at = $2
				WHERE tenant_id = $3 AND product_id = $4 AND warehouse_id = $5`,
				newUnitCost, now, tenantID, productID, whID,
			)
			if err != nil {
				h.log.Error("Failed to update inventory cost", "error", err)
			}

			// Create inventory transaction for audit
			txID := uuid.New()
			h.db.Exec(`
				INSERT INTO inventory_transactions (
					id, tenant_id, inventory_id, transaction_type, quantity,
					unit_cost, total_cost, reference_type, reference_id,
					reason, transaction_date, created_at
				) SELECT $1, $2, id, 'adjustment', 0, $3, $4, 'landed_cost', $5,
					'Landed cost allocation', $6, $6
				FROM inventory
				WHERE tenant_id = $2 AND product_id = $7 AND warehouse_id = $8`,
				txID, tenantID, newUnitCost, totalAllocated, id, now, productID, whID,
			)
		}
	}

	// ============================================
	// CREATE JOURNAL ENTRY FOR LANDED COST
	// ============================================
	func() {
		// Get total landed cost amount
		var totalLandedCost float64
		h.db.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM landed_cost_lines WHERE landed_cost_id = $1`, id).Scan(&totalLandedCost)

		if totalLandedCost <= 0 {
			return
		}

		// Get organization_id from landed cost
		var lcOrgID sql.NullString
		h.db.QueryRow(`SELECT organization_id FROM landed_costs WHERE id = $1`, id).Scan(&lcOrgID)

		var orgIDPtr *uuid.UUID
		if lcOrgID.Valid {
			if parsedOrgID, err := uuid.Parse(lcOrgID.String); err == nil {
				orgIDPtr = &parsedOrgID
			}
		}

		// Look up journal
		var journalID uuid.UUID
		var nextNumber int
		err := h.db.QueryRow(`
			SELECT id, COALESCE(next_number, 1)
			FROM journals WHERE tenant_id = $1 AND code IN ('INVENTORY','GENERAL') AND deleted_at IS NULL
			ORDER BY CASE WHEN code='INVENTORY' THEN 0 ELSE 1 END LIMIT 1`,
			tenantID).Scan(&journalID, &nextNumber)
		if err != nil {
			return
		}

		// Debit: Stock Valuation (inventory)
		invAcct := findAccount(h.db, tenantID, orgIDPtr, "inventory", "1010")
		if invAcct == uuid.Nil {
			invAcct = findAccount(h.db, tenantID, orgIDPtr, "stock valuation", "1010")
		}
		// Credit: Accounts Payable
		apAcct := findAccount(h.db, tenantID, orgIDPtr, "accounts payable", "6010")

		if invAcct == uuid.Nil || apAcct == uuid.Nil {
			return
		}

		entryNumber := fmt.Sprintf("LC%06d", nextNumber)
		journalEntryID := uuid.New()
		description := fmt.Sprintf("Landed Cost Allocation: LC-%s", id.String()[:8])

		_, err = h.db.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description,
				source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'landed_cost', $9, 1.0, $10, $10, 'posted', $11, $12, $12)`,
			journalEntryID, tenantID, orgIDPtr, journalID, entryNumber, now, entryNumber, description,
			id.String(), totalLandedCost, userID, now,
		)
		if err != nil {
			h.log.Error("Failed to create landed cost journal entry", "error", err)
			return
		}

		// Debit: Inventory / Stock Valuation
		h.db.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 1, $3, $4, $5, 0, 1.0, $6)`,
			uuid.New(), journalEntryID, invAcct, "Stock Valuation (Landed Cost)", totalLandedCost, now,
		)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3", totalLandedCost, now, invAcct)

		// Credit: Accounts Payable
		h.db.Exec(`
			INSERT INTO journal_entry_lines (
				id, journal_entry_id, line_number, account_id, description,
				debit_amount, credit_amount, exchange_rate, created_at
			) VALUES ($1, $2, 2, $3, $4, 0, $5, 1.0, $6)`,
			uuid.New(), journalEntryID, apAcct, "Accounts Payable (Landed Cost)", totalLandedCost, now,
		)
		h.db.Exec("UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3", totalLandedCost, now, apAcct)

		h.db.Exec("UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2", now, journalID)
	}()

	// Update status
	_, err = h.db.Exec(`
		UPDATE landed_costs
		SET status = 'validated', validated_by = $1, validated_at = $2, updated_at = $2
		WHERE id = $3`,
		userID, now, id,
	)
	if err != nil {
		h.log.Error("Failed to validate landed cost", "error", err)
		response.InternalError(c, "Failed to validate landed cost")
		return
	}

	h.GetLandedCost(c)
}

// CancelLandedCost cancels a landed cost
func (h *Handler) CancelLandedCost(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	lcID := c.Param("id")

	id, err := uuid.Parse(lcID)
	if err != nil {
		response.Error(c, 400, "Invalid landed cost ID", "")
		return
	}

	// Check status
	var status string
	err = h.db.QueryRow(`
		SELECT status FROM landed_costs
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		id, tenantID).Scan(&status)
	if err == sql.ErrNoRows {
		response.Error(c, 404, "Landed cost not found", "")
		return
	}

	if status == "validated" {
		response.Error(c, 400, "Cannot cancel validated landed cost", "")
		return
	}

	_, err = h.db.Exec(`
		UPDATE landed_costs SET status = 'cancelled', updated_at = $1 WHERE id = $2`,
		time.Now(), id,
	)
	if err != nil {
		h.log.Error("Failed to cancel landed cost", "error", err)
		response.InternalError(c, "Failed to cancel landed cost")
		return
	}

	h.GetLandedCost(c)
}

// calculateLandedCostAllocations calculates and stores allocation per product
func (h *Handler) calculateLandedCostAllocations(lcID, grID uuid.UUID, defaultMethod string) {
	now := time.Now()

	// Get GR lines
	grLines, err := h.db.Query(`
		SELECT id, COALESCE(product_id, '00000000-0000-0000-0000-000000000000'::uuid),
			   product_name, accepted_quantity, unit_price
		FROM goods_receipt_lines
		WHERE goods_receipt_id = $1 AND accepted_quantity > 0`, grID)
	if err != nil {
		return
	}
	defer grLines.Close()

	type grLineData struct {
		ID          uuid.UUID
		ProductID   uuid.UUID
		ProductName string
		Quantity    float64
		UnitPrice   float64
		TotalValue  float64
	}

	var lines []grLineData
	var totalValue, totalQty float64

	for grLines.Next() {
		var line grLineData
		if err := grLines.Scan(&line.ID, &line.ProductID, &line.ProductName, &line.Quantity, &line.UnitPrice); err != nil {
			continue
		}
		line.TotalValue = line.Quantity * line.UnitPrice
		totalValue += line.TotalValue
		totalQty += line.Quantity
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return
	}

	// Get landed cost lines
	lcLines, err := h.db.Query(`
		SELECT id, amount, COALESCE(allocation_method, $1)
		FROM landed_cost_lines
		WHERE landed_cost_id = $2`, defaultMethod, lcID)
	if err != nil {
		return
	}
	defer lcLines.Close()

	for lcLines.Next() {
		var lcLineID uuid.UUID
		var amount float64
		var method string

		if err := lcLines.Scan(&lcLineID, &amount, &method); err != nil {
			continue
		}

		// Calculate allocation for each GR line
		for _, grLine := range lines {
			var allocBase, allocPercent, allocAmount float64

			switch method {
			case "by_value":
				if totalValue > 0 {
					allocBase = grLine.TotalValue
					allocPercent = grLine.TotalValue / totalValue
					allocAmount = amount * allocPercent
				}
			case "by_quantity":
				if totalQty > 0 {
					allocBase = grLine.Quantity
					allocPercent = grLine.Quantity / totalQty
					allocAmount = amount * allocPercent
				}
			case "equal":
				allocBase = 1
				allocPercent = 1.0 / float64(len(lines))
				allocAmount = amount / float64(len(lines))
			default:
				// Default to by_value
				if totalValue > 0 {
					allocBase = grLine.TotalValue
					allocPercent = grLine.TotalValue / totalValue
					allocAmount = amount * allocPercent
				}
			}

			newUnitCost := grLine.UnitPrice
			if grLine.Quantity > 0 {
				newUnitCost = (grLine.TotalValue + allocAmount) / grLine.Quantity
			}
			newTotalCost := grLine.TotalValue + allocAmount

			allocID := uuid.New()
			h.db.Exec(`
				INSERT INTO landed_cost_allocations (
					id, landed_cost_id, landed_cost_line_id, goods_receipt_line_id,
					product_id, product_name, quantity, original_unit_cost, original_total_cost,
					allocation_base, allocation_percent, allocated_amount,
					new_unit_cost, new_total_cost, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
				allocID, lcID, lcLineID, grLine.ID,
				grLine.ProductID, grLine.ProductName, grLine.Quantity, grLine.UnitPrice, grLine.TotalValue,
				allocBase, allocPercent, allocAmount,
				newUnitCost, newTotalCost, now,
			)
		}
	}
}

// GetGRForLandedCost returns GR data for creating landed cost
func (h *Handler) GetGRForLandedCost(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	grID := c.Param("id")

	id, err := uuid.Parse(grID)
	if err != nil {
		response.Error(c, 400, "Invalid goods receipt ID", "")
		return
	}

	// Get GR
	var gr struct {
		ID           uuid.UUID `json:"id"`
		GRNumber     string    `json:"gr_number"`
		PONumber     string    `json:"po_number"`
		SupplierName string    `json:"supplier_name"`
		ProductValue float64   `json:"product_value"`
		Status       string    `json:"status"`
	}

	var poNumber, supplierName sql.NullString

	err = h.db.QueryRow(`
		SELECT gr.id, gr.gr_number, gr.po_number, gr.supplier_name, gr.status,
			   COALESCE(SUM(grl.accepted_quantity * grl.unit_price), 0) as product_value
		FROM goods_receipts gr
		LEFT JOIN goods_receipt_lines grl ON grl.goods_receipt_id = gr.id
		WHERE gr.id = $1 AND gr.tenant_id = $2 AND gr.deleted_at IS NULL
		GROUP BY gr.id`, id, tenantID).Scan(
		&gr.ID, &gr.GRNumber, &poNumber, &supplierName, &gr.Status, &gr.ProductValue,
	)
	if err == sql.ErrNoRows {
		response.Error(c, 404, "Goods receipt not found", "")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch goods receipt", "error", err)
		response.InternalError(c, "Failed to fetch goods receipt")
		return
	}

	if poNumber.Valid {
		gr.PONumber = poNumber.String
	}
	if supplierName.Valid {
		gr.SupplierName = supplierName.String
	}

	// Get lines
	var lines []GRLineForAllocation
	lineRows, err := h.db.Query(`
		SELECT id, COALESCE(product_id, '00000000-0000-0000-0000-000000000000'::uuid),
			   product_name, accepted_quantity, unit_price,
			   accepted_quantity * unit_price as total_value
		FROM goods_receipt_lines
		WHERE goods_receipt_id = $1 AND accepted_quantity > 0`, id)
	if err == nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var line GRLineForAllocation
			if err := lineRows.Scan(&line.ID, &line.ProductID, &line.ProductName, &line.Quantity, &line.UnitPrice, &line.TotalValue); err != nil {
				continue
			}
			lines = append(lines, line)
		}
	}

	// Check if landed cost already exists
	var existingLC sql.NullString
	h.db.QueryRow(`
		SELECT id FROM landed_costs
		WHERE goods_receipt_id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND status != 'cancelled'
		LIMIT 1`, id, tenantID).Scan(&existingLC)

	result := map[string]interface{}{
		"goods_receipt":      gr,
		"lines":              lines,
		"has_landed_cost":    existingLC.Valid,
		"existing_lc_id":     existingLC.String,
	}

	response.Success(c, result)
}
