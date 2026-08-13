package handler

import (
	"database/sql"
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

// PR Status constants
const (
	PRStatusDraft           = "draft"
	PRStatusPendingApproval = "pending_approval"
	PRStatusApproved        = "approved"
	PRStatusRejected        = "rejected"
	PRStatusConverted       = "converted"
	PRStatusCancelled       = "cancelled"
)

// PR Priority constants
const (
	PRPriorityLow    = "low"
	PRPriorityMedium = "medium"
	PRPriorityHigh   = "high"
	PRPriorityUrgent = "urgent"
)

// ListPurchaseRequisitions returns all purchase requisitions for the tenant
func (h *Handler) ListPurchaseRequisitions(c *gin.Context) {
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
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	// Build query
	baseQuery := `
		SELECT pr.id, pr.tenant_id, pr.pr_number, pr.requested_by, pr.department, pr.request_date,
			   pr.required_date, pr.status, pr.priority, pr.total_amount, pr.purpose, pr.notes,
			   pr.approved_by, pr.approved_at, pr.rejection_reason, pr.converted_to_po,
			   pr.created_at, pr.updated_at,
			   pr.material_request_id, COALESCE(mr.request_number, ''),
			   COALESCE(po.order_number, ''), COALESCE(po.status, '')
		FROM purchase_requisitions pr
		LEFT JOIN construction_material_requests mr ON mr.id = pr.material_request_id
		LEFT JOIN purchase_orders po ON po.id = pr.converted_to_po
		WHERE pr.tenant_id = $1 AND pr.deleted_at IS NULL`
	countQuery := `SELECT COUNT(*) FROM purchase_requisitions pr WHERE pr.tenant_id = $1 AND pr.deleted_at IS NULL`
	args := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND pr.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND pr.organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	// Filter by status
	if status := c.Query("status"); status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pr.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND pr.status = $%d", argCount)
		args = append(args, status)
	}

	// Filter by department
	if dept := c.Query("department"); dept != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pr.department = $%d", argCount)
		countQuery += fmt.Sprintf(" AND pr.department = $%d", argCount)
		args = append(args, dept)
	}

	// Filter by priority
	if priority := c.Query("priority"); priority != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND pr.priority = $%d", argCount)
		countQuery += fmt.Sprintf(" AND pr.priority = $%d", argCount)
		args = append(args, priority)
	}

	// Search
	if search := c.Query("search"); search != "" {
		argCount++
		searchPattern := "%" + strings.ToLower(search) + "%"
		baseQuery += fmt.Sprintf(" AND (LOWER(pr.pr_number) LIKE $%d OR LOWER(pr.requested_by) LIKE $%d OR LOWER(pr.department) LIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (LOWER(pr.pr_number) LIKE $%d OR LOWER(pr.requested_by) LIKE $%d OR LOWER(pr.department) LIKE $%d)", argCount, argCount, argCount)
		args = append(args, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		response.InternalError(c, "Failed to count requisitions")
		return
	}

	// Add sorting and pagination
	baseQuery += fmt.Sprintf(" ORDER BY pr.created_at DESC LIMIT $%d OFFSET $%d", argCount+1, argCount+2)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to fetch requisitions", "error", err)
		response.InternalError(c, "Failed to fetch requisitions")
		return
	}
	defer rows.Close()

	var requisitions []map[string]interface{}
	for rows.Next() {
		var id, tenantIDScan uuid.UUID
		var prNumber, requestedBy, status, priority string
		var department, purpose, notes, approvedBy, rejectionReason sql.NullString
		var convertedToPO sql.NullString
		var requestDate time.Time
		var requiredDate, approvedAt sql.NullTime
		var totalAmount float64
		var createdAt, updatedAt time.Time
		var materialRequestID sql.NullInt64
		var materialRequestNumber, poNumber, poStatus string

		err := rows.Scan(
			&id, &tenantIDScan, &prNumber, &requestedBy, &department, &requestDate,
			&requiredDate, &status, &priority, &totalAmount, &purpose, &notes,
			&approvedBy, &approvedAt, &rejectionReason, &convertedToPO,
			&createdAt, &updatedAt,
			&materialRequestID, &materialRequestNumber, &poNumber, &poStatus,
		)
		if err != nil {
			continue
		}

		pr := map[string]interface{}{
			"id":           id.String(),
			"tenant_id":    tenantIDScan.String(),
			"pr_number":    prNumber,
			"requested_by": requestedBy,
			"request_date": requestDate.Format("2006-01-02"),
			"status":       status,
			"priority":     priority,
			"total_amount": totalAmount,
			"created_at":   createdAt,
			"updated_at":   updatedAt,
			"po_number":    poNumber,
			"po_status":    poStatus,
		}
		if materialRequestID.Valid {
			pr["material_request_id"] = materialRequestID.Int64
			pr["material_request_number"] = materialRequestNumber
		}

		if department.Valid {
			pr["department"] = department.String
		}
		if requiredDate.Valid {
			pr["required_date"] = requiredDate.Time.Format("2006-01-02")
		}
		if purpose.Valid {
			pr["purpose"] = purpose.String
		}
		if notes.Valid {
			pr["notes"] = notes.String
		}
		if approvedBy.Valid {
			pr["approved_by"] = approvedBy.String
		}
		if approvedAt.Valid {
			pr["approved_at"] = approvedAt.Time
		}
		if rejectionReason.Valid {
			pr["rejection_reason"] = rejectionReason.String
		}
		if convertedToPO.Valid {
			pr["converted_to_po"] = convertedToPO.String
		}

		requisitions = append(requisitions, pr)
	}

	response.Paginated(c, requisitions, page, pageSize, total)
}

// CreatePurchaseRequisition creates a new purchase requisition
func (h *Handler) CreatePurchaseRequisition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input struct {
		RequestedBy  string `json:"requested_by" binding:"required"`
		Department   string `json:"department,omitempty"`
		RequestDate  string `json:"request_date,omitempty"`
		RequiredDate string `json:"required_date,omitempty"`
		Priority     string `json:"priority,omitempty"`
		Purpose      string `json:"purpose,omitempty"`
		Notes        string `json:"notes,omitempty"`
		Lines        []struct {
			ProductID       string  `json:"product_id,omitempty"`
			ProductName     string  `json:"product_name" binding:"required"`
			ProductCode     string  `json:"product_code,omitempty"`
			Description     string  `json:"description,omitempty"`
			Quantity        float64 `json:"quantity" binding:"required,gt=0"`
			Unit            string  `json:"unit,omitempty"`
			EstimatedPrice  float64 `json:"estimated_price,omitempty" binding:"omitempty,gte=0"`
			PreferredVendor string  `json:"preferred_vendor,omitempty"`
			VendorName      string  `json:"vendor_name,omitempty"`
			Specifications  string  `json:"specifications,omitempty"`
		} `json:"lines" binding:"required,min=1,dive"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Generate PR number
	prNumber := "PR-" + time.Now().Format("200601") + "-" + uuid.New().String()[:6]

	prID := uuid.New()
	now := time.Now()

	// Parse request date
	requestDate := now
	if input.RequestDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.RequestDate); err == nil {
			requestDate = parsed
		}
	}

	// Parse required date
	var requiredDate *time.Time
	if input.RequiredDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.RequiredDate); err == nil {
			requiredDate = &parsed
		}
	}

	priority := input.Priority
	if priority == "" {
		priority = PRPriorityMedium
	}

	// Calculate total amount
	var totalAmount float64
	for _, line := range input.Lines {
		totalAmount += line.Quantity * line.EstimatedPrice
	}

	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Insert requisition
	query := `
		INSERT INTO purchase_requisitions (
			id, tenant_id, organization_id, pr_number, requested_by, department, request_date,
			required_date, status, priority, total_amount, purpose, notes,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`

	_, err := h.db.Exec(query,
		prID, tenantID, orgIDPtr, prNumber, input.RequestedBy, input.Department, requestDate,
		requiredDate, PRStatusDraft, priority, totalAmount, input.Purpose, input.Notes,
		createdBy, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create requisition", "error", err)
		response.InternalError(c, "Failed to create requisition")
		return
	}

	// Insert lines
	lineQuery := `
		INSERT INTO purchase_requisition_lines (
			id, requisition_id, product_id, product_name, product_code, description,
			quantity, unit, estimated_price, total_price, preferred_vendor, vendor_name,
			specifications, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	for _, line := range input.Lines {
		lineID := uuid.New()

		var productID, preferredVendor *uuid.UUID
		if line.ProductID != "" {
			if pid, err := uuid.Parse(line.ProductID); err == nil {
				productID = &pid
			}
		}
		if line.PreferredVendor != "" {
			if vid, err := uuid.Parse(line.PreferredVendor); err == nil {
				preferredVendor = &vid
			}
		}

		unit := line.Unit
		if unit == "" {
			unit = "pcs"
		}

		totalPrice := line.Quantity * line.EstimatedPrice

		if _, execErr := h.db.Exec(lineQuery,
			lineID, prID, productID, line.ProductName, line.ProductCode, line.Description,
			line.Quantity, unit, line.EstimatedPrice, totalPrice, preferredVendor, line.VendorName,
			line.Specifications, now, now,
		); execErr != nil {

			h.log.Error("write failed (was silently discarded)", "stmt", "exec", "error", execErr)

		}
	}

	// Return created requisition
	prResponse := map[string]interface{}{
		"id":           prID.String(),
		"tenant_id":    tenantID.String(),
		"pr_number":    prNumber,
		"requested_by": input.RequestedBy,
		"request_date": requestDate.Format("2006-01-02"),
		"status":       PRStatusDraft,
		"priority":     priority,
		"total_amount": totalAmount,
		"created_at":   now,
	}

	if input.Department != "" {
		prResponse["department"] = input.Department
	}
	if requiredDate != nil {
		prResponse["required_date"] = requiredDate.Format("2006-01-02")
	}
	if input.Purpose != "" {
		prResponse["purpose"] = input.Purpose
	}

	response.Created(c, prResponse)
}

// GetPurchaseRequisition returns a single purchase requisition
func (h *Handler) GetPurchaseRequisition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	prID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid requisition ID")
		return
	}

	// Get requisition
	query := `
		SELECT id, tenant_id, pr_number, requested_by, department, request_date,
			   required_date, status, priority, total_amount, purpose, notes,
			   approved_by, approved_at, rejection_reason, converted_to_po,
			   created_at, updated_at
		FROM purchase_requisitions
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`

	var id, tenantIDScan uuid.UUID
	var prNumber, requestedBy, status, priority string
	var department, purpose, notes, approvedBy, rejectionReason sql.NullString
	var convertedToPO sql.NullString
	var requestDate time.Time
	var requiredDate, approvedAt sql.NullTime
	var totalAmount float64
	var createdAt, updatedAt time.Time

	err = h.db.QueryRow(query, prID, tenantID).Scan(
		&id, &tenantIDScan, &prNumber, &requestedBy, &department, &requestDate,
		&requiredDate, &status, &priority, &totalAmount, &purpose, &notes,
		&approvedBy, &approvedAt, &rejectionReason, &convertedToPO,
		&createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase requisition")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch requisition", "error", err)
		response.InternalError(c, "Failed to fetch requisition")
		return
	}

	pr := map[string]interface{}{
		"id":           id.String(),
		"tenant_id":    tenantIDScan.String(),
		"pr_number":    prNumber,
		"requested_by": requestedBy,
		"request_date": requestDate.Format("2006-01-02"),
		"status":       status,
		"priority":     priority,
		"total_amount": totalAmount,
		"created_at":   createdAt,
		"updated_at":   updatedAt,
	}

	if department.Valid {
		pr["department"] = department.String
	}
	if requiredDate.Valid {
		pr["required_date"] = requiredDate.Time.Format("2006-01-02")
	}
	if purpose.Valid {
		pr["purpose"] = purpose.String
	}
	if notes.Valid {
		pr["notes"] = notes.String
	}
	if approvedBy.Valid {
		pr["approved_by"] = approvedBy.String
	}
	if approvedAt.Valid {
		pr["approved_at"] = approvedAt.Time
	}
	if rejectionReason.Valid {
		pr["rejection_reason"] = rejectionReason.String
	}
	if convertedToPO.Valid {
		pr["converted_to_po"] = convertedToPO.String
	}

	// Get lines
	linesQuery := `
		SELECT id, product_id, product_name, product_code, description,
			   quantity, unit, estimated_price, total_price, approved_quantity,
			   preferred_vendor, vendor_name, specifications
		FROM purchase_requisition_lines
		WHERE requisition_id = $1
		ORDER BY created_at`

	linesRows, err := h.db.Query(linesQuery, prID)
	if err == nil {
		defer linesRows.Close()
		var lines []map[string]interface{}
		for linesRows.Next() {
			var lineID uuid.UUID
			var productID, preferredVendor sql.NullString
			var productName, unit string
			var productCode, description, vendorName, specifications sql.NullString
			var quantity, estimatedPrice, totalPrice, approvedQty float64

			err := linesRows.Scan(
				&lineID, &productID, &productName, &productCode, &description,
				&quantity, &unit, &estimatedPrice, &totalPrice, &approvedQty,
				&preferredVendor, &vendorName, &specifications,
			)
			if err != nil {
				continue
			}

			line := map[string]interface{}{
				"id":                lineID.String(),
				"product_name":      productName,
				"quantity":          quantity,
				"unit":              unit,
				"estimated_price":   estimatedPrice,
				"total_price":       totalPrice,
				"approved_quantity": approvedQty,
			}

			if productID.Valid {
				line["product_id"] = productID.String
			}
			if productCode.Valid {
				line["product_code"] = productCode.String
			}
			if description.Valid {
				line["description"] = description.String
			}
			if preferredVendor.Valid {
				line["preferred_vendor"] = preferredVendor.String
			}
			if vendorName.Valid {
				line["vendor_name"] = vendorName.String
			}
			if specifications.Valid {
				line["specifications"] = specifications.String
			}

			lines = append(lines, line)
		}
		pr["lines"] = lines
	}

	response.Success(c, pr)
}

// UpdatePurchaseRequisition updates a purchase requisition
func (h *Handler) UpdatePurchaseRequisition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	prID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid requisition ID")
		return
	}

	// Check current status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_requisitions WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", prID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase requisition")
		return
	}
	if currentStatus != PRStatusDraft {
		response.BadRequest(c, "Only draft requisitions can be updated")
		return
	}

	var input struct {
		RequestedBy  *string `json:"requested_by,omitempty"`
		Department   *string `json:"department,omitempty"`
		RequiredDate *string `json:"required_date,omitempty"`
		Priority     *string `json:"priority,omitempty"`
		Purpose      *string `json:"purpose,omitempty"`
		Notes        *string `json:"notes,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.RequestedBy != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("requested_by = $%d", argCount))
		args = append(args, *input.RequestedBy)
	}
	if input.Department != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("department = $%d", argCount))
		args = append(args, *input.Department)
	}
	if input.RequiredDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("required_date = $%d", argCount))
		if *input.RequiredDate != "" {
			rd, _ := time.Parse("2006-01-02", *input.RequiredDate)
			args = append(args, rd)
		} else {
			args = append(args, nil)
		}
	}
	if input.Priority != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("priority = $%d", argCount))
		args = append(args, *input.Priority)
	}
	if input.Purpose != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("purpose = $%d", argCount))
		args = append(args, *input.Purpose)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, prID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE purchase_requisitions SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update requisition", "error", err)
		response.InternalError(c, "Failed to update requisition")
		return
	}

	h.GetPurchaseRequisition(c)
}

// DeletePurchaseRequisition soft deletes a purchase requisition
func (h *Handler) DeletePurchaseRequisition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	prID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid requisition ID")
		return
	}

	// Check status
	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_requisitions WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", prID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase requisition")
		return
	}
	if currentStatus != PRStatusDraft && currentStatus != PRStatusRejected {
		response.BadRequest(c, "Only draft or rejected requisitions can be deleted")
		return
	}

	result, err := h.db.Exec(
		"UPDATE purchase_requisitions SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), prID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete requisition")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Purchase requisition")
		return
	}

	response.NoContent(c)
}

// SubmitPurchaseRequisition submits a PR for approval
func (h *Handler) SubmitPurchaseRequisition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	prID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid requisition ID")
		return
	}

	// Get PR details
	var currentStatus string
	var totalAmount float64
	var prNumber string
	err = h.db.QueryRow(`
		SELECT status, COALESCE(total_amount, 0), pr_number
		FROM purchase_requisitions
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, prID, tenantID).Scan(&currentStatus, &totalAmount, &prNumber)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase requisition")
		return
	}
	if currentStatus != PRStatusDraft {
		response.BadRequest(c, "Only draft requisitions can be submitted")
		return
	}

	// Evaluate procurement rules
	result, err := h.EvaluateRules(tenantID, entity.DocumentTypePurchaseRequisition, prID, totalAmount, nil, userID)
	if err != nil {
		h.log.Error("Failed to evaluate rules", "error", err)
		// Fall back to pending_approval
		result = &entity.RuleEvaluationResult{Action: entity.ActionRoute}
	}

	now := time.Now()

	switch result.Action {
	case entity.ActionAutoApprove:
		// Auto-approve
		_, err = h.db.Exec(
			"UPDATE purchase_requisitions SET status = $1, approved_by = $2, approved_at = $3, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
			PRStatusApproved, userID.String(), now, prID, tenantID,
		)
		if err != nil {
			h.log.Error("Failed to auto-approve PR", "error", err)
			response.InternalError(c, "Failed to approve requisition")
			return
		}
		response.Success(c, gin.H{
			"message":       "Requisition auto-approved",
			"status":        PRStatusApproved,
			"auto_approved": true,
		})

	case entity.ActionBlock:
		response.BadRequest(c, result.Message)

	default: // ActionRoute, ActionWarn, or any other
		// Create approval workflow if approvers specified
		if len(result.ApproverIDs) > 0 {
			workflow, err := h.CreateApprovalWorkflow(tenantID, entity.DocumentTypePurchaseRequisition, prID, prNumber, result)
			if err != nil {
				h.log.Error("Failed to create approval workflow", "error", err)
			}

			_, err = h.db.Exec(
				"UPDATE purchase_requisitions SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
				PRStatusPendingApproval, now, prID, tenantID,
			)
			if err != nil {
				response.InternalError(c, "Failed to submit requisition")
				return
			}

			resp := gin.H{
				"message":     "Requisition submitted for approval",
				"status":      PRStatusPendingApproval,
				"workflow_id": workflow.ID,
				"approvers":   result.ApproverIDs,
			}
			if result.Action == entity.ActionWarn {
				resp["warning"] = result.Message
			}
			response.Success(c, resp)
		} else {
			// No specific approvers, just set to pending approval
			_, err = h.db.Exec(
				"UPDATE purchase_requisitions SET status = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
				PRStatusPendingApproval, now, prID, tenantID,
			)
			if err != nil {
				response.InternalError(c, "Failed to submit requisition")
				return
			}

			resp := gin.H{
				"message": "Requisition submitted for approval",
				"status":  PRStatusPendingApproval,
			}
			if result.Action == entity.ActionWarn {
				resp["warning"] = result.Message
			}
			response.Success(c, resp)
		}
	}
}

// ApprovePurchaseRequisition approves a PR
func (h *Handler) ApprovePurchaseRequisition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	prID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid requisition ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_requisitions WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", prID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase requisition")
		return
	}
	if currentStatus != PRStatusPendingApproval {
		response.BadRequest(c, "Only pending requisitions can be approved")
		return
	}

	var input struct {
		ApprovedBy string `json:"approved_by,omitempty"`
		Lines      []struct {
			LineID           string  `json:"line_id"`
			ApprovedQuantity float64 `json:"approved_quantity"`
		} `json:"lines,omitempty"`
	}
	c.ShouldBindJSON(&input)

	approver := input.ApprovedBy
	if approver == "" && userID != uuid.Nil {
		approver = userID.String()
	}

	now := time.Now()
	_, err = h.db.Exec(
		"UPDATE purchase_requisitions SET status = $1, approved_by = $2, approved_at = $3, updated_at = $4 WHERE id = $5 AND tenant_id = $6",
		PRStatusApproved, approver, now, now, prID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to approve requisition")
		return
	}

	// Update approved quantities if provided
	for _, line := range input.Lines {
		lineID, err := uuid.Parse(line.LineID)
		if err != nil {
			continue
		}
		if _, execErr := h.db.Exec("UPDATE purchase_requisition_lines SET approved_quantity = $1, updated_at = $2 WHERE id = $3", line.ApprovedQuantity, now, lineID); execErr != nil {
			h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE purchase_requisition_lines", "error", execErr)
		}
	}

	// Cancel any pending workflow for this document
	_, _ = h.db.Exec(`
		UPDATE approval_workflow_instances
		SET status = 'cancelled', completed_at = $1, updated_at = $1
		WHERE document_type = 'purchase_requisition' AND document_id = $2 AND status = 'pending'
	`, now, prID)

	h.GetPurchaseRequisition(c)
}

// RejectPurchaseRequisition rejects a PR
func (h *Handler) RejectPurchaseRequisition(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	prID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid requisition ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow("SELECT status FROM purchase_requisitions WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", prID, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase requisition")
		return
	}
	if currentStatus != PRStatusPendingApproval {
		response.BadRequest(c, "Only pending requisitions can be rejected")
		return
	}

	var input struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	_, err = h.db.Exec(
		"UPDATE purchase_requisitions SET status = $1, rejection_reason = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		PRStatusRejected, input.Reason, time.Now(), prID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to reject requisition")
		return
	}

	h.GetPurchaseRequisition(c)
}

// ConvertPRToPO converts an approved PR to a Purchase Order
func (h *Handler) ConvertPRToPO(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	userID, _ := middleware.GetUserID(c)

	prID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid requisition ID")
		return
	}

	// Check status
	var currentStatus string
	var totalAmount float64
	var prOrgID uuid.NullUUID
	var prRequiredDate sql.NullTime
	var prMaterialRequestID sql.NullInt64
	var prNumberStr string
	err = h.db.QueryRow(`SELECT status, total_amount, organization_id, required_date, material_request_id, pr_number
		FROM purchase_requisitions WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, prID, tenantID).
		Scan(&currentStatus, &totalAmount, &prOrgID, &prRequiredDate, &prMaterialRequestID, &prNumberStr)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Purchase requisition")
		return
	}
	if currentStatus != PRStatusApproved {
		response.BadRequest(c, "Only approved requisitions can be converted to PO")
		return
	}

	var input struct {
		SupplierID   string `json:"supplier_id" binding:"required"`
		PaymentTerms string `json:"payment_terms,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	supplierID, err := uuid.Parse(input.SupplierID)
	if err != nil {
		response.BadRequest(c, "Invalid supplier ID")
		return
	}

	// Get supplier name
	var supplierName string
	err = h.db.QueryRow("SELECT name FROM contacts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL", supplierID, tenantID).Scan(&supplierName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Supplier")
		return
	}

	// Generate PO number
	poNumber := "PO-" + time.Now().Format("200601") + "-" + uuid.New().String()[:6]
	poID := uuid.New()
	now := time.Now()

	// purchase_orders.payment_terms is INTEGER (days); the legacy "net_30"
	// string 500'ed on insert, so this endpoint never worked post-migration.
	paymentDays := 30
	if input.PaymentTerms != "" {
		if n, err := strconv.Atoi(strings.TrimPrefix(input.PaymentTerms, "net_")); err == nil && n > 0 {
			paymentDays = n
		}
	}

	// PR'dan kelgan kontekstni PO'ga ko'chirish: organization_id (org-scoped
	// ro'yxat/statslarda ko'rinishi uchun) va kerak-sana → expected_date.
	var poOrgArg interface{}
	if prOrgID.Valid {
		poOrgArg = prOrgID.UUID
	} else if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		poOrgArg = orgID
	}
	var poExpectedArg interface{}
	if prRequiredDate.Valid {
		poExpectedArg = prRequiredDate.Time
	}

	// Create PO
	poQuery := `
		INSERT INTO purchase_orders (
			id, tenant_id, organization_id, order_number, vendor_id, order_date, expected_date,
			subtotal, total_amount, status, payment_status, payment_terms,
			requested_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	_, err = h.db.Exec(poQuery,
		poID, tenantID, poOrgArg, poNumber, supplierID, now, poExpectedArg,
		totalAmount, totalAmount, "draft", "unpaid", paymentDays,
		userID, now, now,
	)
	if err != nil {
		h.log.Error("Failed to create purchase order", "error", err)
		response.InternalError(c, "Failed to create purchase order")
		return
	}

	// Get PR lines and create PO lines. NULL-safe COALESCEs: approved_quantity
	// and unit are nullable — a bare scan into float64/string errored and the
	// `continue` silently dropped every line (PO ended up line-less).
	linesQuery := `
		SELECT id, product_id, COALESCE(product_name, ''), product_code, description,
			   COALESCE(quantity, 0), COALESCE(unit, ''), COALESCE(estimated_price, 0),
			   COALESCE(approved_quantity, 0)
		FROM purchase_requisition_lines
		WHERE requisition_id = $1`

	rows, err := h.db.Query(linesQuery, prID)
	if err == nil {
		defer rows.Close()

		poLineQuery := `
			INSERT INTO purchase_order_lines (
				id, purchase_order_id, line_number, product_id, description,
				quantity, unit_price, line_total, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

		lineNum := 0
		for rows.Next() {
			var lineID uuid.UUID
			var productID sql.NullString
			var productName, unit string
			var productCode, description sql.NullString
			var quantity, estimatedPrice, approvedQty float64

			err := rows.Scan(&lineID, &productID, &productName, &productCode, &description, &quantity, &unit, &estimatedPrice, &approvedQty)
			if err != nil {
				continue
			}

			lineNum++
			poLineID := uuid.New()

			qty := quantity
			if approvedQty > 0 {
				qty = approvedQty
			}

			var prodID *uuid.UUID
			if productID.Valid {
				if pid, err := uuid.Parse(productID.String); err == nil {
					prodID = &pid
				}
			}

			desc := productName
			if description.Valid {
				desc = description.String
			}

			lineTotal := qty * estimatedPrice

			if _, execErr := h.db.Exec(poLineQuery, poLineID, poID, lineNum, prodID, desc, qty, estimatedPrice, lineTotal, now, now); execErr != nil {

				h.log.Error("write failed (was silently discarded)", "stmt", "exec", "error", execErr)

			}
		}
	}

	// Update PR status
	_, err = h.db.Exec(
		"UPDATE purchase_requisitions SET status = $1, converted_to_po = $2, updated_at = $3 WHERE id = $4 AND tenant_id = $5",
		PRStatusConverted, poID, now, prID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to update PR status", "error", err)
	}

	// Material zayavkasidan kelgan so'rov — zayavka timeline'iga PO yozuvi.
	if prMaterialRequestID.Valid {
		var mrProjectID int64
		if err := h.db.QueryRow(`SELECT project_id FROM construction_material_requests WHERE id = $1 AND tenant_id = $2 AND flow = 'v2'`,
			prMaterialRequestID.Int64, tenantID).Scan(&mrProjectID); err == nil {
			h.mrV2Activity(tenantID, mrProjectID, userID, prMaterialRequestID.Int64, "po_created",
				fmt.Sprintf("Buyurtma (PO) yaratildi: %s — %s", poNumber, supplierName),
				map[string]interface{}{"purchase_order_id": poID, "po_number": poNumber, "pr_number": prNumberStr})
		}
	}

	response.Success(c, map[string]interface{}{
		"id":           poID.String(),
		"po_number":    poNumber,
		"supplier_id":  supplierID.String(),
		"total_amount": totalAmount,
		"status":       "draft",
		"created_at":   now,
	})
}
