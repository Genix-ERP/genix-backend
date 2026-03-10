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

// BlanketOrder represents a blanket order (standing order / call-off contract)
type BlanketOrder struct {
	ID                    uuid.UUID              `json:"id"`
	TenantID              uuid.UUID              `json:"tenant_id"`
	BlanketNumber         string                 `json:"blanket_number"`
	Title                 string                 `json:"title"`
	Description           string                 `json:"description,omitempty"`
	VendorID              *uuid.UUID             `json:"vendor_id,omitempty"`
	VendorName            string                 `json:"vendor_name,omitempty"`
	StartDate             string                 `json:"start_date"`
	EndDate               string                 `json:"end_date"`
	AgreementType         string                 `json:"agreement_type"` // quantity or value
	TotalValue            float64                `json:"total_value"`
	ReleasedValue         float64                `json:"released_value"`
	RemainingValue        float64                `json:"remaining_value"`
	Currency              string                 `json:"currency"`
	PaymentTerms          string                 `json:"payment_terms,omitempty"`
	DeliveryTerms         string                 `json:"delivery_terms,omitempty"`
	Incoterms             string                 `json:"incoterms,omitempty"`
	WarehouseID           *uuid.UUID             `json:"warehouse_id,omitempty"`
	WarehouseName         string                 `json:"warehouse_name,omitempty"`
	Status                string                 `json:"status"`
	RequiresApproval      bool                   `json:"requires_approval"`
	ApprovedBy            *uuid.UUID             `json:"approved_by,omitempty"`
	ApprovedAt            *time.Time             `json:"approved_at,omitempty"`
	TermsConditions       string                 `json:"terms_conditions,omitempty"`
	SpecialInstructions   string                 `json:"special_instructions,omitempty"`
	MinReleaseValue       *float64               `json:"min_release_value,omitempty"`
	MaxReleaseValue       *float64               `json:"max_release_value,omitempty"`
	ReleaseFrequency      string                 `json:"release_frequency,omitempty"`
	AutoRelease           bool                   `json:"auto_release"`
	NextReleaseDate       *string                `json:"next_release_date,omitempty"`
	PriceAdjustmentAllowed bool                  `json:"price_adjustment_allowed"`
	MaxPriceIncreasePercent *float64             `json:"max_price_increase_percent,omitempty"`
	ExpiryAlertDays       int                    `json:"expiry_alert_days"`
	LowQuantityAlertPercent *float64             `json:"low_quantity_alert_percent,omitempty"`
	Notes                 string                 `json:"notes,omitempty"`
	Lines                 []BlanketOrderLine     `json:"lines,omitempty"`
	Releases              []BlanketOrderRelease  `json:"releases,omitempty"`
	CreatedBy             *uuid.UUID             `json:"created_by,omitempty"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`

	// Computed fields
	TotalAgreedQuantity   float64                `json:"total_agreed_quantity,omitempty"`
	TotalReleasedQuantity float64                `json:"total_released_quantity,omitempty"`
	UtilizationPercent    float64                `json:"utilization_percent,omitempty"`
	DaysRemaining         int                    `json:"days_remaining,omitempty"`
	ReleaseCount          int                    `json:"release_count,omitempty"`
}

// BlanketOrderLine represents a line item in a blanket order
type BlanketOrderLine struct {
	ID                uuid.UUID  `json:"id"`
	BlanketOrderID    uuid.UUID  `json:"blanket_order_id"`
	LineNumber        int        `json:"line_number"`
	ProductID         *uuid.UUID `json:"product_id,omitempty"`
	ProductName       string     `json:"product_name"`
	ProductCode       string     `json:"product_code,omitempty"`
	Description       string     `json:"description,omitempty"`
	AgreedQuantity    float64    `json:"agreed_quantity"`
	ReleasedQuantity  float64    `json:"released_quantity"`
	ReceivedQuantity  float64    `json:"received_quantity"`
	RemainingQuantity float64    `json:"remaining_quantity"`
	UnitID            *uuid.UUID `json:"unit_id,omitempty"`
	UnitName          string     `json:"unit_name,omitempty"`
	UnitPrice         float64    `json:"unit_price"`
	DiscountPercent   float64    `json:"discount_percent"`
	TaxID             *uuid.UUID `json:"tax_id,omitempty"`
	LineValue         float64    `json:"line_value"`
	MinReleaseQty     *float64   `json:"min_release_qty,omitempty"`
	MaxReleaseQty     *float64   `json:"max_release_qty,omitempty"`
	LeadTimeDays      int        `json:"lead_time_days"`
	WarehouseID       *uuid.UUID `json:"warehouse_id,omitempty"`
	Notes             string     `json:"notes,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// BlanketOrderRelease represents a release (call-off) against a blanket order
type BlanketOrderRelease struct {
	ID              uuid.UUID                  `json:"id"`
	TenantID        uuid.UUID                  `json:"tenant_id"`
	BlanketOrderID  uuid.UUID                  `json:"blanket_order_id"`
	ReleaseNumber   string                     `json:"release_number"`
	ReleaseDate     string                     `json:"release_date"`
	ExpectedDate    *string                    `json:"expected_date,omitempty"`
	PurchaseOrderID *uuid.UUID                 `json:"purchase_order_id,omitempty"`
	PONumber        string                     `json:"po_number,omitempty"`
	Status          string                     `json:"status"`
	Subtotal        float64                    `json:"subtotal"`
	DiscountAmount  float64                    `json:"discount_amount"`
	TaxAmount       float64                    `json:"tax_amount"`
	TotalAmount     float64                    `json:"total_amount"`
	ShippingAddress map[string]interface{}     `json:"shipping_address,omitempty"`
	ShippingMethod  string                     `json:"shipping_method,omitempty"`
	TrackingNumber  string                     `json:"tracking_number,omitempty"`
	Notes           string                     `json:"notes,omitempty"`
	Lines           []BlanketOrderReleaseLine  `json:"lines,omitempty"`
	CreatedBy       *uuid.UUID                 `json:"created_by,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
	UpdatedAt       time.Time                  `json:"updated_at"`
}

// BlanketOrderReleaseLine represents a line item in a release
type BlanketOrderReleaseLine struct {
	ID               uuid.UUID  `json:"id"`
	ReleaseID        uuid.UUID  `json:"release_id"`
	BlanketLineID    uuid.UUID  `json:"blanket_line_id"`
	LineNumber       int        `json:"line_number"`
	ProductID        *uuid.UUID `json:"product_id,omitempty"`
	ProductName      string     `json:"product_name,omitempty"`
	Quantity         float64    `json:"quantity"`
	QuantityReceived float64    `json:"quantity_received"`
	UnitPrice        float64    `json:"unit_price"`
	DiscountPercent  float64    `json:"discount_percent"`
	TaxAmount        float64    `json:"tax_amount"`
	LineTotal        float64    `json:"line_total"`
	WarehouseID      *uuid.UUID `json:"warehouse_id,omitempty"`
	Notes            string     `json:"notes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// ListBlanketOrders returns all blanket orders for a tenant
func (h *Handler) ListBlanketOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Build query with filters
	query := `
		SELECT bo.id, bo.blanket_number, bo.title, bo.description,
			   bo.vendor_id, bo.vendor_name, bo.start_date, bo.end_date,
			   bo.agreement_type, bo.total_value, bo.released_value, bo.remaining_value,
			   bo.currency, bo.status, bo.warehouse_id, w.name as warehouse_name,
			   bo.created_at, bo.updated_at,
			   COALESCE(SUM(bol.agreed_quantity), 0) as total_agreed_qty,
			   COALESCE(SUM(bol.released_quantity), 0) as total_released_qty,
			   COUNT(DISTINCT bor.id) as release_count
		FROM blanket_orders bo
		LEFT JOIN warehouses w ON w.id = bo.warehouse_id
		LEFT JOIN blanket_order_lines bol ON bol.blanket_order_id = bo.id
		LEFT JOIN blanket_order_releases bor ON bor.blanket_order_id = bo.id
		WHERE bo.tenant_id = $1 AND bo.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		query += fmt.Sprintf(" AND bo.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		query += fmt.Sprintf(" AND bo.status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by vendor
	if vendorID := c.Query("vendor_id"); vendorID != "" {
		argCount++
		query += fmt.Sprintf(" AND bo.vendor_id = $%d", argCount)
		args = append(args, vendorID)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		query += fmt.Sprintf(" AND (bo.blanket_number ILIKE $%d OR bo.title ILIKE $%d OR bo.vendor_name ILIKE $%d)", argCount, argCount, argCount)
		args = append(args, "%"+search+"%")
	}

	query += `
		GROUP BY bo.id, bo.blanket_number, bo.title, bo.description,
				 bo.vendor_id, bo.vendor_name, bo.start_date, bo.end_date,
				 bo.agreement_type, bo.total_value, bo.released_value, bo.remaining_value,
				 bo.currency, bo.status, bo.warehouse_id, w.name,
				 bo.created_at, bo.updated_at
		ORDER BY bo.created_at DESC
	`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list blanket orders", "error", err)
		response.InternalError(c, "Failed to list blanket orders")
		return
	}
	defer rows.Close()

	orders := make([]BlanketOrder, 0)
	now := time.Now()

	for rows.Next() {
		var order BlanketOrder
		var vendorID, warehouseID sql.NullString
		var description, warehouseName sql.NullString
		var startDate, endDate time.Time

		err := rows.Scan(
			&order.ID, &order.BlanketNumber, &order.Title, &description,
			&vendorID, &order.VendorName, &startDate, &endDate,
			&order.AgreementType, &order.TotalValue, &order.ReleasedValue, &order.RemainingValue,
			&order.Currency, &order.Status, &warehouseID, &warehouseName,
			&order.CreatedAt, &order.UpdatedAt,
			&order.TotalAgreedQuantity, &order.TotalReleasedQuantity, &order.ReleaseCount,
		)
		if err != nil {
			h.log.Error("Failed to scan blanket order", "error", err)
			continue
		}

		if vendorID.Valid {
			vid, _ := uuid.Parse(vendorID.String)
			order.VendorID = &vid
		}
		if warehouseID.Valid {
			wid, _ := uuid.Parse(warehouseID.String)
			order.WarehouseID = &wid
		}
		if description.Valid {
			order.Description = description.String
		}
		if warehouseName.Valid {
			order.WarehouseName = warehouseName.String
		}

		order.StartDate = startDate.Format("2006-01-02")
		order.EndDate = endDate.Format("2006-01-02")

		// Calculate utilization percent
		if order.TotalValue > 0 {
			order.UtilizationPercent = (order.ReleasedValue / order.TotalValue) * 100
		} else if order.TotalAgreedQuantity > 0 {
			order.UtilizationPercent = (order.TotalReleasedQuantity / order.TotalAgreedQuantity) * 100
		}

		// Calculate days remaining
		order.DaysRemaining = int(endDate.Sub(now).Hours() / 24)
		if order.DaysRemaining < 0 {
			order.DaysRemaining = 0
		}

		orders = append(orders, order)
	}

	response.Success(c, orders)
}

// GetBlanketOrder returns a single blanket order with lines and releases
func (h *Handler) GetBlanketOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid blanket order ID")
		return
	}

	// Get blanket order
	var order BlanketOrder
	var vendorID, warehouseID, approvedBy, createdBy sql.NullString
	var description, vendorName, paymentTerms, deliveryTerms, incoterms sql.NullString
	var termsConditions, specialInstructions, releaseFrequency, notes sql.NullString
	var warehouseName, nextReleaseDate sql.NullString
	var minReleaseValue, maxReleaseValue, maxPriceIncrease, lowQtyAlert sql.NullFloat64
	var approvedAt sql.NullTime
	var startDate, endDate time.Time

	err = h.db.QueryRow(`
		SELECT bo.id, bo.tenant_id, bo.blanket_number, bo.title, bo.description,
			   bo.vendor_id, bo.vendor_name, bo.start_date, bo.end_date,
			   bo.agreement_type, bo.total_value, bo.released_value, bo.remaining_value,
			   bo.currency, bo.payment_terms, bo.delivery_terms, bo.incoterms,
			   bo.warehouse_id, w.name as warehouse_name, bo.status,
			   bo.requires_approval, bo.approved_by, bo.approved_at,
			   bo.terms_conditions, bo.special_instructions,
			   bo.min_release_value, bo.max_release_value, bo.release_frequency,
			   bo.auto_release, bo.next_release_date,
			   bo.price_adjustment_allowed, bo.max_price_increase_percent,
			   bo.expiry_alert_days, bo.low_quantity_alert_percent,
			   bo.notes, bo.created_by, bo.created_at, bo.updated_at
		FROM blanket_orders bo
		LEFT JOIN warehouses w ON w.id = bo.warehouse_id
		WHERE bo.id = $1 AND bo.tenant_id = $2 AND bo.deleted_at IS NULL
	`, id, tenantID).Scan(
		&order.ID, &order.TenantID, &order.BlanketNumber, &order.Title, &description,
		&vendorID, &vendorName, &startDate, &endDate,
		&order.AgreementType, &order.TotalValue, &order.ReleasedValue, &order.RemainingValue,
		&order.Currency, &paymentTerms, &deliveryTerms, &incoterms,
		&warehouseID, &warehouseName, &order.Status,
		&order.RequiresApproval, &approvedBy, &approvedAt,
		&termsConditions, &specialInstructions,
		&minReleaseValue, &maxReleaseValue, &releaseFrequency,
		&order.AutoRelease, &nextReleaseDate,
		&order.PriceAdjustmentAllowed, &maxPriceIncrease,
		&order.ExpiryAlertDays, &lowQtyAlert,
		&notes, &createdBy, &order.CreatedAt, &order.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Blanket order")
		return
	}
	if err != nil {
		h.log.Error("Failed to get blanket order", "error", err)
		response.InternalError(c, "Failed to get blanket order")
		return
	}

	// Set nullable fields
	if description.Valid {
		order.Description = description.String
	}
	if vendorID.Valid {
		vid, _ := uuid.Parse(vendorID.String)
		order.VendorID = &vid
	}
	if vendorName.Valid {
		order.VendorName = vendorName.String
	}
	if paymentTerms.Valid {
		order.PaymentTerms = paymentTerms.String
	}
	if deliveryTerms.Valid {
		order.DeliveryTerms = deliveryTerms.String
	}
	if incoterms.Valid {
		order.Incoterms = incoterms.String
	}
	if warehouseID.Valid {
		wid, _ := uuid.Parse(warehouseID.String)
		order.WarehouseID = &wid
	}
	if warehouseName.Valid {
		order.WarehouseName = warehouseName.String
	}
	if approvedBy.Valid {
		aid, _ := uuid.Parse(approvedBy.String)
		order.ApprovedBy = &aid
	}
	if approvedAt.Valid {
		order.ApprovedAt = &approvedAt.Time
	}
	if termsConditions.Valid {
		order.TermsConditions = termsConditions.String
	}
	if specialInstructions.Valid {
		order.SpecialInstructions = specialInstructions.String
	}
	if minReleaseValue.Valid {
		order.MinReleaseValue = &minReleaseValue.Float64
	}
	if maxReleaseValue.Valid {
		order.MaxReleaseValue = &maxReleaseValue.Float64
	}
	if releaseFrequency.Valid {
		order.ReleaseFrequency = releaseFrequency.String
	}
	if nextReleaseDate.Valid {
		order.NextReleaseDate = &nextReleaseDate.String
	}
	if maxPriceIncrease.Valid {
		order.MaxPriceIncreasePercent = &maxPriceIncrease.Float64
	}
	if lowQtyAlert.Valid {
		order.LowQuantityAlertPercent = &lowQtyAlert.Float64
	}
	if notes.Valid {
		order.Notes = notes.String
	}
	if createdBy.Valid {
		cid, _ := uuid.Parse(createdBy.String)
		order.CreatedBy = &cid
	}

	order.StartDate = startDate.Format("2006-01-02")
	order.EndDate = endDate.Format("2006-01-02")

	// Get lines
	order.Lines = h.getBlanketOrderLines(id)

	// Get releases
	order.Releases = h.getBlanketOrderReleases(id, tenantID)

	// Calculate totals
	for _, line := range order.Lines {
		order.TotalAgreedQuantity += line.AgreedQuantity
		order.TotalReleasedQuantity += line.ReleasedQuantity
	}

	// Calculate utilization
	if order.TotalValue > 0 {
		order.UtilizationPercent = (order.ReleasedValue / order.TotalValue) * 100
	} else if order.TotalAgreedQuantity > 0 {
		order.UtilizationPercent = (order.TotalReleasedQuantity / order.TotalAgreedQuantity) * 100
	}

	// Days remaining
	ed, _ := time.Parse("2006-01-02", order.EndDate)
	order.DaysRemaining = int(ed.Sub(time.Now()).Hours() / 24)
	if order.DaysRemaining < 0 {
		order.DaysRemaining = 0
	}

	order.ReleaseCount = len(order.Releases)

	response.Success(c, order)
}

// getBlanketOrderLines retrieves lines for a blanket order
func (h *Handler) getBlanketOrderLines(blanketOrderID uuid.UUID) []BlanketOrderLine {
	rows, err := h.db.Query(`
		SELECT bol.id, bol.blanket_order_id, bol.line_number,
			   bol.product_id, bol.product_name, bol.product_code, bol.description,
			   bol.agreed_quantity, bol.released_quantity, bol.received_quantity, bol.remaining_quantity,
			   bol.unit_id, bol.unit_name, bol.unit_price, bol.discount_percent,
			   bol.tax_id, bol.line_value,
			   bol.min_release_qty, bol.max_release_qty, bol.lead_time_days,
			   bol.warehouse_id, bol.notes, bol.created_at, bol.updated_at
		FROM blanket_order_lines bol
		WHERE bol.blanket_order_id = $1
		ORDER BY bol.line_number
	`, blanketOrderID)
	if err != nil {
		h.log.Error("Failed to get blanket order lines", "error", err)
		return nil
	}
	defer rows.Close()

	lines := make([]BlanketOrderLine, 0)
	for rows.Next() {
		var line BlanketOrderLine
		var productID, unitID, taxID, warehouseID sql.NullString
		var productCode, description, unitName, notes sql.NullString
		var minReleaseQty, maxReleaseQty sql.NullFloat64

		err := rows.Scan(
			&line.ID, &line.BlanketOrderID, &line.LineNumber,
			&productID, &line.ProductName, &productCode, &description,
			&line.AgreedQuantity, &line.ReleasedQuantity, &line.ReceivedQuantity, &line.RemainingQuantity,
			&unitID, &unitName, &line.UnitPrice, &line.DiscountPercent,
			&taxID, &line.LineValue,
			&minReleaseQty, &maxReleaseQty, &line.LeadTimeDays,
			&warehouseID, &notes, &line.CreatedAt, &line.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan blanket order line", "error", err)
			continue
		}

		if productID.Valid {
			pid, _ := uuid.Parse(productID.String)
			line.ProductID = &pid
		}
		if productCode.Valid {
			line.ProductCode = productCode.String
		}
		if description.Valid {
			line.Description = description.String
		}
		if unitID.Valid {
			uid, _ := uuid.Parse(unitID.String)
			line.UnitID = &uid
		}
		if unitName.Valid {
			line.UnitName = unitName.String
		}
		if taxID.Valid {
			tid, _ := uuid.Parse(taxID.String)
			line.TaxID = &tid
		}
		if minReleaseQty.Valid {
			line.MinReleaseQty = &minReleaseQty.Float64
		}
		if maxReleaseQty.Valid {
			line.MaxReleaseQty = &maxReleaseQty.Float64
		}
		if warehouseID.Valid {
			wid, _ := uuid.Parse(warehouseID.String)
			line.WarehouseID = &wid
		}
		if notes.Valid {
			line.Notes = notes.String
		}

		lines = append(lines, line)
	}

	return lines
}

// getBlanketOrderReleases retrieves releases for a blanket order
func (h *Handler) getBlanketOrderReleases(blanketOrderID, tenantID uuid.UUID) []BlanketOrderRelease {
	rows, err := h.db.Query(`
		SELECT bor.id, bor.tenant_id, bor.blanket_order_id, bor.release_number,
			   bor.release_date, bor.expected_date, bor.purchase_order_id, po.order_number,
			   bor.status, bor.subtotal, bor.discount_amount, bor.tax_amount, bor.total_amount,
			   bor.shipping_method, bor.tracking_number, bor.notes,
			   bor.created_by, bor.created_at, bor.updated_at
		FROM blanket_order_releases bor
		LEFT JOIN purchase_orders po ON po.id = bor.purchase_order_id
		WHERE bor.blanket_order_id = $1 AND bor.tenant_id = $2
		ORDER BY bor.release_date DESC
	`, blanketOrderID, tenantID)
	if err != nil {
		h.log.Error("Failed to get blanket order releases", "error", err)
		return nil
	}
	defer rows.Close()

	releases := make([]BlanketOrderRelease, 0)
	for rows.Next() {
		var release BlanketOrderRelease
		var expectedDate, poNumber, shippingMethod, trackingNumber, notes sql.NullString
		var poID, createdBy sql.NullString
		var releaseDate time.Time

		err := rows.Scan(
			&release.ID, &release.TenantID, &release.BlanketOrderID, &release.ReleaseNumber,
			&releaseDate, &expectedDate, &poID, &poNumber,
			&release.Status, &release.Subtotal, &release.DiscountAmount, &release.TaxAmount, &release.TotalAmount,
			&shippingMethod, &trackingNumber, &notes,
			&createdBy, &release.CreatedAt, &release.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan blanket order release", "error", err)
			continue
		}

		release.ReleaseDate = releaseDate.Format("2006-01-02")
		if expectedDate.Valid {
			release.ExpectedDate = &expectedDate.String
		}
		if poID.Valid {
			pid, _ := uuid.Parse(poID.String)
			release.PurchaseOrderID = &pid
		}
		if poNumber.Valid {
			release.PONumber = poNumber.String
		}
		if shippingMethod.Valid {
			release.ShippingMethod = shippingMethod.String
		}
		if trackingNumber.Valid {
			release.TrackingNumber = trackingNumber.String
		}
		if notes.Valid {
			release.Notes = notes.String
		}
		if createdBy.Valid {
			cid, _ := uuid.Parse(createdBy.String)
			release.CreatedBy = &cid
		}

		releases = append(releases, release)
	}

	return releases
}

// CreateBlanketOrder creates a new blanket order
func (h *Handler) CreateBlanketOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	// Get organization ID from middleware header
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	var input struct {
		Title               string  `json:"title" binding:"required"`
		Description         string  `json:"description"`
		VendorID            string  `json:"vendor_id"`
		VendorName          string  `json:"vendor_name"`
		StartDate           string  `json:"start_date" binding:"required"`
		EndDate             string  `json:"end_date" binding:"required"`
		AgreementType       string  `json:"agreement_type"`
		TotalValue          float64 `json:"total_value"`
		Currency            string  `json:"currency"`
		PaymentTerms        string  `json:"payment_terms"`
		DeliveryTerms       string  `json:"delivery_terms"`
		Incoterms           string  `json:"incoterms"`
		WarehouseID         string  `json:"warehouse_id"`
		RequiresApproval    bool    `json:"requires_approval"`
		TermsConditions     string  `json:"terms_conditions"`
		SpecialInstructions string  `json:"special_instructions"`
		MinReleaseValue     *float64 `json:"min_release_value"`
		MaxReleaseValue     *float64 `json:"max_release_value"`
		ReleaseFrequency    string  `json:"release_frequency"`
		AutoRelease         bool    `json:"auto_release"`
		ExpiryAlertDays     int     `json:"expiry_alert_days"`
		Notes               string  `json:"notes"`
		Lines               []struct {
			ProductID       string   `json:"product_id"`
			ProductName     string   `json:"product_name"`
			ProductCode     string   `json:"product_code"`
			Description     string   `json:"description"`
			AgreedQuantity  float64  `json:"agreed_quantity"`
			UnitID          string   `json:"unit_id"`
			UnitName        string   `json:"unit_name"`
			UnitPrice       float64  `json:"unit_price"`
			DiscountPercent float64  `json:"discount_percent"`
			TaxID           string   `json:"tax_id"`
			MinReleaseQty   *float64 `json:"min_release_qty"`
			MaxReleaseQty   *float64 `json:"max_release_qty"`
			LeadTimeDays    int      `json:"lead_time_days"`
			WarehouseID     string   `json:"warehouse_id"`
			Notes           string   `json:"notes"`
		} `json:"lines"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Generate blanket order number
	var blanketNumber string
	err := h.db.QueryRow(`
		SELECT 'BO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-' ||
			   LPAD((COALESCE(MAX(CAST(SUBSTRING(blanket_number FROM 'BO-[0-9]+-([0-9]+)') AS INTEGER)), 0) + 1)::TEXT, 4, '0')
		FROM blanket_orders WHERE tenant_id = $1 AND blanket_number LIKE 'BO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-%'
	`, tenantID).Scan(&blanketNumber)
	if err != nil {
		blanketNumber = fmt.Sprintf("BO-%s-0001", time.Now().Format("20060102"))
	}

	// Default values
	if input.AgreementType == "" {
		input.AgreementType = "quantity"
	}
	if input.Currency == "" {
		input.Currency = "UZS"
	}
	if input.ExpiryAlertDays == 0 {
		input.ExpiryAlertDays = 30
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create blanket order")
		return
	}
	defer tx.Rollback()

	// Insert blanket order
	orderID := uuid.New()
	now := time.Now()

	var vendorID, warehouseID *uuid.UUID
	if input.VendorID != "" {
		vid, _ := uuid.Parse(input.VendorID)
		vendorID = &vid
	}
	if input.WarehouseID != "" {
		wid, _ := uuid.Parse(input.WarehouseID)
		warehouseID = &wid
	}

	_, err = tx.Exec(`
		INSERT INTO blanket_orders (
			id, tenant_id, organization_id, blanket_number, title, description,
			vendor_id, vendor_name, start_date, end_date,
			agreement_type, total_value, currency,
			payment_terms, delivery_terms, incoterms,
			warehouse_id, status, requires_approval,
			terms_conditions, special_instructions,
			min_release_value, max_release_value, release_frequency,
			auto_release, expiry_alert_days, notes,
			created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13,
			$14, $15, $16,
			$17, $18, $19,
			$20, $21,
			$22, $23, $24,
			$25, $26, $27,
			$28, $29, $30
		)
	`, orderID, tenantID, orgIDPtr, blanketNumber, input.Title, input.Description,
		vendorID, input.VendorName, input.StartDate, input.EndDate,
		input.AgreementType, input.TotalValue, input.Currency,
		input.PaymentTerms, input.DeliveryTerms, input.Incoterms,
		warehouseID, "draft", input.RequiresApproval,
		input.TermsConditions, input.SpecialInstructions,
		input.MinReleaseValue, input.MaxReleaseValue, input.ReleaseFrequency,
		input.AutoRelease, input.ExpiryAlertDays, input.Notes,
		userID, now, now)

	if err != nil {
		h.log.Error("Failed to insert blanket order", "error", err)
		response.InternalError(c, "Failed to create blanket order")
		return
	}

	// Insert lines
	for i, line := range input.Lines {
		lineID := uuid.New()
		lineNumber := i + 1

		var productID, unitID, taxID, lineWarehouseID *uuid.UUID
		if line.ProductID != "" {
			pid, _ := uuid.Parse(line.ProductID)
			productID = &pid
		}
		if line.UnitID != "" {
			uid, _ := uuid.Parse(line.UnitID)
			unitID = &uid
		}
		if line.TaxID != "" {
			tid, _ := uuid.Parse(line.TaxID)
			taxID = &tid
		}
		if line.WarehouseID != "" {
			wid, _ := uuid.Parse(line.WarehouseID)
			lineWarehouseID = &wid
		} else if warehouseID != nil {
			lineWarehouseID = warehouseID
		}

		_, err = tx.Exec(`
			INSERT INTO blanket_order_lines (
				id, blanket_order_id, line_number,
				product_id, product_name, product_code, description,
				agreed_quantity, unit_id, unit_name, unit_price, discount_percent,
				tax_id, min_release_qty, max_release_qty, lead_time_days,
				warehouse_id, notes, created_at, updated_at
			) VALUES (
				$1, $2, $3,
				$4, $5, $6, $7,
				$8, $9, $10, $11, $12,
				$13, $14, $15, $16,
				$17, $18, $19, $20
			)
		`, lineID, orderID, lineNumber,
			productID, line.ProductName, line.ProductCode, line.Description,
			line.AgreedQuantity, unitID, line.UnitName, line.UnitPrice, line.DiscountPercent,
			taxID, line.MinReleaseQty, line.MaxReleaseQty, line.LeadTimeDays,
			lineWarehouseID, line.Notes, now, now)

		if err != nil {
			h.log.Error("Failed to insert blanket order line", "error", err)
			response.InternalError(c, "Failed to create blanket order")
			return
		}
	}

	// Calculate and update total value if quantity-based
	if input.AgreementType == "quantity" {
		_, err = tx.Exec(`
			UPDATE blanket_orders
			SET total_value = (
				SELECT COALESCE(SUM(line_value), 0)
				FROM blanket_order_lines
				WHERE blanket_order_id = $1
			)
			WHERE id = $1
		`, orderID)
		if err != nil {
			h.log.Error("Failed to update total value", "error", err)
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create blanket order")
		return
	}

	response.Created(c, gin.H{
		"id":             orderID,
		"blanket_number": blanketNumber,
		"message":        "Blanket order created successfully",
	})
}

// UpdateBlanketOrder updates an existing blanket order
func (h *Handler) UpdateBlanketOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid blanket order ID")
		return
	}

	// Check if order exists and is editable
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM blanket_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Blanket order")
		return
	}
	if err != nil {
		h.log.Error("Failed to get blanket order", "error", err)
		response.InternalError(c, "Failed to update blanket order")
		return
	}

	if currentStatus != "draft" {
		response.BadRequest(c, "Only draft blanket orders can be edited")
		return
	}

	var input struct {
		Title               string   `json:"title"`
		Description         string   `json:"description"`
		VendorID            string   `json:"vendor_id"`
		VendorName          string   `json:"vendor_name"`
		StartDate           string   `json:"start_date"`
		EndDate             string   `json:"end_date"`
		AgreementType       string   `json:"agreement_type"`
		TotalValue          float64  `json:"total_value"`
		Currency            string   `json:"currency"`
		PaymentTerms        string   `json:"payment_terms"`
		DeliveryTerms       string   `json:"delivery_terms"`
		Incoterms           string   `json:"incoterms"`
		WarehouseID         string   `json:"warehouse_id"`
		RequiresApproval    bool     `json:"requires_approval"`
		TermsConditions     string   `json:"terms_conditions"`
		SpecialInstructions string   `json:"special_instructions"`
		MinReleaseValue     *float64 `json:"min_release_value"`
		MaxReleaseValue     *float64 `json:"max_release_value"`
		ReleaseFrequency    string   `json:"release_frequency"`
		AutoRelease         bool     `json:"auto_release"`
		ExpiryAlertDays     int      `json:"expiry_alert_days"`
		Notes               string   `json:"notes"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	var vendorID, warehouseID *uuid.UUID
	if input.VendorID != "" {
		vid, _ := uuid.Parse(input.VendorID)
		vendorID = &vid
	}
	if input.WarehouseID != "" {
		wid, _ := uuid.Parse(input.WarehouseID)
		warehouseID = &wid
	}

	_, err = h.db.Exec(`
		UPDATE blanket_orders SET
			title = $1, description = $2,
			vendor_id = $3, vendor_name = $4,
			start_date = $5, end_date = $6,
			agreement_type = $7, total_value = $8, currency = $9,
			payment_terms = $10, delivery_terms = $11, incoterms = $12,
			warehouse_id = $13, requires_approval = $14,
			terms_conditions = $15, special_instructions = $16,
			min_release_value = $17, max_release_value = $18, release_frequency = $19,
			auto_release = $20, expiry_alert_days = $21, notes = $22,
			updated_at = $23
		WHERE id = $24 AND tenant_id = $25
	`, input.Title, input.Description,
		vendorID, input.VendorName,
		input.StartDate, input.EndDate,
		input.AgreementType, input.TotalValue, input.Currency,
		input.PaymentTerms, input.DeliveryTerms, input.Incoterms,
		warehouseID, input.RequiresApproval,
		input.TermsConditions, input.SpecialInstructions,
		input.MinReleaseValue, input.MaxReleaseValue, input.ReleaseFrequency,
		input.AutoRelease, input.ExpiryAlertDays, input.Notes,
		time.Now(), id, tenantID)

	if err != nil {
		h.log.Error("Failed to update blanket order", "error", err)
		response.InternalError(c, "Failed to update blanket order")
		return
	}

	response.Success(c, gin.H{"message": "Blanket order updated successfully"})
}

// DeleteBlanketOrder soft deletes a blanket order
func (h *Handler) DeleteBlanketOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid blanket order ID")
		return
	}

	// Check status
	var status string
	err = h.db.QueryRow("SELECT status FROM blanket_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Blanket order")
		return
	}
	if status == "active" {
		response.BadRequest(c, "Cannot delete an active blanket order. Please cancel it first.")
		return
	}

	_, err = h.db.Exec("UPDATE blanket_orders SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3", time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete blanket order", "error", err)
		response.InternalError(c, "Failed to delete blanket order")
		return
	}

	response.Success(c, gin.H{"message": "Blanket order deleted successfully"})
}

// ActivateBlanketOrder activates a draft blanket order
func (h *Handler) ActivateBlanketOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid blanket order ID")
		return
	}

	// Check current status
	var status string
	var requiresApproval bool
	err = h.db.QueryRow("SELECT status, requires_approval FROM blanket_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", id, tenantID).Scan(&status, &requiresApproval)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Blanket order")
		return
	}
	if status != "draft" {
		response.BadRequest(c, "Only draft blanket orders can be activated")
		return
	}

	// Activate the order
	now := time.Now()
	_, err = h.db.Exec(`
		UPDATE blanket_orders
		SET status = 'active', approved_by = $1, approved_at = $2, updated_at = $3
		WHERE id = $4 AND tenant_id = $5
	`, userID, now, now, id, tenantID)

	if err != nil {
		h.log.Error("Failed to activate blanket order", "error", err)
		response.InternalError(c, "Failed to activate blanket order")
		return
	}

	response.Success(c, gin.H{"message": "Blanket order activated successfully", "status": "active"})
}

// CreateBlanketOrderRelease creates a new release against a blanket order
func (h *Handler) CreateBlanketOrderRelease(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	blanketOrderID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid blanket order ID")
		return
	}

	// Get organization ID from middleware header
	releaseOrgID, _ := middleware.GetOrganizationID(c)
	var releaseOrgIDPtr *uuid.UUID
	if releaseOrgID != uuid.Nil {
		releaseOrgIDPtr = &releaseOrgID
	}

	// Check blanket order status
	var status string
	var vendorID sql.NullString
	var vendorName string
	err = h.db.QueryRow(`
		SELECT status, vendor_id, vendor_name
		FROM blanket_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, blanketOrderID, tenantID).Scan(&status, &vendorID, &vendorName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Blanket order")
		return
	}
	if status != "active" {
		response.BadRequest(c, "Releases can only be created for active blanket orders")
		return
	}

	var input struct {
		ReleaseDate     string `json:"release_date"`
		ExpectedDate    string `json:"expected_date"`
		ShippingAddress map[string]interface{} `json:"shipping_address"`
		ShippingMethod  string `json:"shipping_method"`
		Notes           string `json:"notes"`
		CreatePO        bool   `json:"create_po"`
		Lines           []struct {
			BlanketLineID string  `json:"blanket_line_id" binding:"required"`
			Quantity      float64 `json:"quantity" binding:"required"`
			Notes         string  `json:"notes"`
		} `json:"lines" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if len(input.Lines) == 0 {
		response.BadRequest(c, "At least one line item is required")
		return
	}

	// Set default release date
	if input.ReleaseDate == "" {
		input.ReleaseDate = time.Now().Format("2006-01-02")
	}

	// Generate release number
	var releaseNumber string
	err = h.db.QueryRow(`
		SELECT 'REL-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-' ||
			   LPAD((COALESCE(MAX(CAST(SUBSTRING(release_number FROM 'REL-[0-9]+-([0-9]+)') AS INTEGER)), 0) + 1)::TEXT, 4, '0')
		FROM blanket_order_releases WHERE tenant_id = $1 AND release_number LIKE 'REL-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-%'
	`, tenantID).Scan(&releaseNumber)
	if err != nil {
		releaseNumber = fmt.Sprintf("REL-%s-0001", time.Now().Format("20060102"))
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to create release")
		return
	}
	defer tx.Rollback()

	// Insert release
	releaseID := uuid.New()
	now := time.Now()

	_, err = tx.Exec(`
		INSERT INTO blanket_order_releases (
			id, tenant_id, organization_id, blanket_order_id, release_number,
			release_date, expected_date, status,
			shipping_method, notes,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, releaseID, tenantID, releaseOrgIDPtr, blanketOrderID, releaseNumber,
		input.ReleaseDate, input.ExpectedDate, "draft",
		input.ShippingMethod, input.Notes,
		userID, now, now)

	if err != nil {
		h.log.Error("Failed to insert release", "error", err)
		response.InternalError(c, "Failed to create release")
		return
	}

	// Insert release lines and calculate totals
	var subtotal float64
	for i, line := range input.Lines {
		blanketLineID, err := uuid.Parse(line.BlanketLineID)
		if err != nil {
			response.BadRequest(c, "Invalid blanket line ID")
			return
		}

		// Get blanket line info and validate quantity
		var productID sql.NullString
		var productName string
		var unitPrice float64
		var discountPercent float64
		var remainingQty float64
		var minReleaseQty, maxReleaseQty sql.NullFloat64
		var warehouseID sql.NullString

		err = h.db.QueryRow(`
			SELECT product_id, product_name, unit_price, discount_percent,
				   remaining_quantity, min_release_qty, max_release_qty, warehouse_id
			FROM blanket_order_lines
			WHERE id = $1 AND blanket_order_id = $2
		`, blanketLineID, blanketOrderID).Scan(
			&productID, &productName, &unitPrice, &discountPercent,
			&remainingQty, &minReleaseQty, &maxReleaseQty, &warehouseID)

		if err == sql.ErrNoRows {
			response.BadRequest(c, fmt.Sprintf("Blanket line %s not found", line.BlanketLineID))
			return
		}
		if err != nil {
			h.log.Error("Failed to get blanket line", "error", err)
			response.InternalError(c, "Failed to create release")
			return
		}

		// Validate quantity
		if line.Quantity > remainingQty {
			response.BadRequest(c, fmt.Sprintf("Quantity %.2f exceeds remaining quantity %.2f for %s", line.Quantity, remainingQty, productName))
			return
		}
		if minReleaseQty.Valid && line.Quantity < minReleaseQty.Float64 {
			response.BadRequest(c, fmt.Sprintf("Quantity %.2f is below minimum release quantity %.2f for %s", line.Quantity, minReleaseQty.Float64, productName))
			return
		}
		if maxReleaseQty.Valid && line.Quantity > maxReleaseQty.Float64 {
			response.BadRequest(c, fmt.Sprintf("Quantity %.2f exceeds maximum release quantity %.2f for %s", line.Quantity, maxReleaseQty.Float64, productName))
			return
		}

		lineTotal := line.Quantity * unitPrice * (1 - discountPercent/100)
		subtotal += lineTotal

		var pid *uuid.UUID
		if productID.Valid {
			p, _ := uuid.Parse(productID.String)
			pid = &p
		}
		var wid *uuid.UUID
		if warehouseID.Valid {
			w, _ := uuid.Parse(warehouseID.String)
			wid = &w
		}

		_, err = tx.Exec(`
			INSERT INTO blanket_order_release_lines (
				id, release_id, blanket_line_id, line_number,
				product_id, product_name, quantity,
				unit_price, discount_percent, notes, warehouse_id, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`, uuid.New(), releaseID, blanketLineID, i+1,
			pid, productName, line.Quantity,
			unitPrice, discountPercent, line.Notes, wid, now)

		if err != nil {
			h.log.Error("Failed to insert release line", "error", err)
			response.InternalError(c, "Failed to create release")
			return
		}
	}

	// Update release totals
	_, err = tx.Exec(`
		UPDATE blanket_order_releases
		SET subtotal = $1, total_amount = $1
		WHERE id = $2
	`, subtotal, releaseID)

	if err != nil {
		h.log.Error("Failed to update release totals", "error", err)
		response.InternalError(c, "Failed to create release")
		return
	}

	// Optionally create PO from release
	var poID *uuid.UUID
	var poNumber string
	if input.CreatePO {
		poID, poNumber, err = h.createPOFromRelease(tx, tenantID, userID, releaseID, blanketOrderID, vendorID, vendorName, input.ReleaseDate, input.ExpectedDate, subtotal)
		if err != nil {
			h.log.Error("Failed to create PO from release", "error", err)
			response.InternalError(c, "Failed to create purchase order")
			return
		}

		// Link PO to release
		_, err = tx.Exec("UPDATE blanket_order_releases SET purchase_order_id = $1 WHERE id = $2", poID, releaseID)
		if err != nil {
			h.log.Error("Failed to link PO to release", "error", err)
		}
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to create release")
		return
	}

	result := gin.H{
		"id":             releaseID,
		"release_number": releaseNumber,
		"message":        "Release created successfully",
	}
	if poID != nil {
		result["purchase_order_id"] = poID
		result["po_number"] = poNumber
	}

	response.Created(c, result)
}

// createPOFromRelease creates a PO from a blanket order release
func (h *Handler) createPOFromRelease(tx *sql.Tx, tenantID, userID uuid.UUID, releaseID, blanketOrderID uuid.UUID, vendorIDStr sql.NullString, vendorName, releaseDate, expectedDate string, subtotal float64) (*uuid.UUID, string, error) {
	// Generate PO number
	var poNumber string
	err := h.db.QueryRow(`
		SELECT 'PO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-' ||
			   LPAD((COALESCE(MAX(CAST(SUBSTRING(order_number FROM 'PO-[0-9]+-([0-9]+)') AS INTEGER)), 0) + 1)::TEXT, 4, '0')
		FROM purchase_orders WHERE tenant_id = $1 AND order_number LIKE 'PO-' || TO_CHAR(NOW(), 'YYYYMMDD') || '-%'
	`, tenantID).Scan(&poNumber)
	if err != nil {
		poNumber = fmt.Sprintf("PO-%s-0001", time.Now().Format("20060102"))
	}

	poID := uuid.New()
	now := time.Now()

	var vendorID *uuid.UUID
	if vendorIDStr.Valid {
		vid, _ := uuid.Parse(vendorIDStr.String)
		vendorID = &vid
	}

	// Parse dates
	orderDate, _ := time.Parse("2006-01-02", releaseDate)
	if orderDate.IsZero() {
		orderDate = now
	}

	var expDate *time.Time
	if expectedDate != "" {
		ed, _ := time.Parse("2006-01-02", expectedDate)
		if !ed.IsZero() {
			expDate = &ed
		}
	}

	// Insert PO
	_, err = tx.Exec(`
		INSERT INTO purchase_orders (
			id, tenant_id, order_number, vendor_id, vendor_name,
			order_date, expected_date, status, payment_status,
			subtotal, total_amount, blanket_release_id,
			notes, requested_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, poID, tenantID, poNumber, vendorID, vendorName,
		orderDate, expDate, "draft", "unpaid",
		subtotal, subtotal, releaseID,
		"Created from blanket order release", userID, now, now)

	if err != nil {
		return nil, "", err
	}

	// Copy release lines to PO lines
	rows, err := tx.Query(`
		SELECT borl.line_number, borl.product_id, borl.product_name, borl.quantity,
			   borl.unit_price, borl.discount_percent, borl.warehouse_id
		FROM blanket_order_release_lines borl
		WHERE borl.release_id = $1
		ORDER BY borl.line_number
	`, releaseID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	for rows.Next() {
		var lineNumber int
		var productID, warehouseID sql.NullString
		var productName string
		var quantity, unitPrice, discountPercent float64

		err := rows.Scan(&lineNumber, &productID, &productName, &quantity, &unitPrice, &discountPercent, &warehouseID)
		if err != nil {
			continue
		}

		lineTotal := quantity * unitPrice * (1 - discountPercent/100)

		var pid *uuid.UUID
		if productID.Valid {
			p, _ := uuid.Parse(productID.String)
			pid = &p
		}
		var wid *uuid.UUID
		if warehouseID.Valid {
			w, _ := uuid.Parse(warehouseID.String)
			wid = &w
		}

		_, err = tx.Exec(`
			INSERT INTO purchase_order_lines (
				id, purchase_order_id, line_number, product_id, description,
				quantity, unit_price, discount_amount, line_total, warehouse_id,
				quantity_received, quantity_invoiced, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		`, uuid.New(), poID, lineNumber, pid, productName,
			quantity, unitPrice, 0, lineTotal, wid,
			0, 0, now, now)

		if err != nil {
			return nil, "", err
		}
	}

	return &poID, poNumber, nil
}

// ConfirmBlanketOrderRelease confirms a release and updates blanket order quantities
func (h *Handler) ConfirmBlanketOrderRelease(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("release_id")
	releaseID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid release ID")
		return
	}

	// Check release status
	var status string
	var blanketOrderID uuid.UUID
	err = h.db.QueryRow(`
		SELECT status, blanket_order_id FROM blanket_order_releases
		WHERE id = $1 AND tenant_id = $2
	`, releaseID, tenantID).Scan(&status, &blanketOrderID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Release")
		return
	}
	if status != "draft" {
		response.BadRequest(c, "Only draft releases can be confirmed")
		return
	}

	// Update release status - trigger will update blanket order quantities
	_, err = h.db.Exec(`
		UPDATE blanket_order_releases
		SET status = 'confirmed', updated_at = $1
		WHERE id = $2
	`, time.Now(), releaseID)

	if err != nil {
		h.log.Error("Failed to confirm release", "error", err)
		response.InternalError(c, "Failed to confirm release")
		return
	}

	// Check if blanket order is fully consumed
	var remainingValue float64
	h.db.QueryRow("SELECT remaining_value FROM blanket_orders WHERE id = $1", blanketOrderID).Scan(&remainingValue)

	if remainingValue <= 0 {
		h.db.Exec("UPDATE blanket_orders SET status = 'completed', updated_at = $1 WHERE id = $2", time.Now(), blanketOrderID)
	}

	response.Success(c, gin.H{"message": "Release confirmed successfully", "status": "confirmed"})
}

// CancelBlanketOrderRelease cancels a release
func (h *Handler) CancelBlanketOrderRelease(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("release_id")
	releaseID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid release ID")
		return
	}

	// Check release status
	var status string
	err = h.db.QueryRow("SELECT status FROM blanket_order_releases WHERE id = $1 AND tenant_id = $2", releaseID, tenantID).Scan(&status)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Release")
		return
	}
	if status == "received" {
		response.BadRequest(c, "Cannot cancel a received release")
		return
	}

	// Update release status - trigger will reverse quantities if was confirmed
	_, err = h.db.Exec("UPDATE blanket_order_releases SET status = 'cancelled', updated_at = $1 WHERE id = $2", time.Now(), releaseID)

	if err != nil {
		h.log.Error("Failed to cancel release", "error", err)
		response.InternalError(c, "Failed to cancel release")
		return
	}

	response.Success(c, gin.H{"message": "Release cancelled successfully", "status": "cancelled"})
}

// ListBlanketOrderReleases lists all releases for a blanket order
func (h *Handler) ListBlanketOrderReleases(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	blanketOrderID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid blanket order ID")
		return
	}

	releases := h.getBlanketOrderReleases(blanketOrderID, tenantID)
	response.Success(c, releases)
}
