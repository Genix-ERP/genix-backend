package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// QUOTATION TEMPLATE HANDLERS
// =====================================================

// ListQuotationTemplates returns all quotation templates for the tenant
func (h *Handler) ListQuotationTemplates(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var filter entity.QuotationTemplateListFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid filter parameters")
		return
	}

	query := `
		SELECT qt.id, qt.tenant_id, qt.name, qt.code, qt.description,
		       qt.validity_days, qt.payment_term_id, qt.pricelist_id,
		       qt.terms_and_conditions, qt.header_note, qt.footer_note,
		       qt.require_signature, qt.require_payment, qt.payment_percentage,
		       qt.show_line_subtotals, qt.show_line_discounts, qt.group_by_section,
		       qt.is_active, qt.created_by, qt.created_at, qt.updated_at,
		       (SELECT COUNT(*) FROM quotation_template_lines WHERE template_id = qt.id) as line_count,
		       (SELECT COUNT(*) FROM quotation_template_optionals WHERE template_id = qt.id) as optional_count,
		       COALESCE((SELECT SUM(COALESCE(unit_price, 0) * quantity) FROM quotation_template_lines WHERE template_id = qt.id), 0) as total_amount
		FROM quotation_templates qt
		WHERE qt.tenant_id = $1
	`
	args := []interface{}{tenantID}
	argCount := 1

	if filter.Search != "" {
		argCount++
		query += fmt.Sprintf(" AND (qt.name ILIKE $%d OR qt.code ILIKE $%d OR qt.description ILIKE $%d)", argCount, argCount, argCount)
		args = append(args, "%"+filter.Search+"%")
	}

	if filter.IsActive != nil {
		argCount++
		query += fmt.Sprintf(" AND qt.is_active = $%d", argCount)
		args = append(args, *filter.IsActive)
	}

	query += " ORDER BY qt.name ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to query quotation templates", "error", err)
		response.InternalServerError(c, "Failed to query quotation templates")
		return
	}
	defer rows.Close()

	var templates []entity.QuotationTemplateResponse
	for rows.Next() {
		var t entity.QuotationTemplateResponse
		var code, description, termsAndConditions, headerNote, footerNote sql.NullString
		var paymentTermID, pricelistID, createdBy sql.NullString

		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.Name, &code, &description,
			&t.ValidityDays, &paymentTermID, &pricelistID,
			&termsAndConditions, &headerNote, &footerNote,
			&t.RequireSignature, &t.RequirePayment, &t.PaymentPercentage,
			&t.ShowLineSubtotals, &t.ShowLineDiscounts, &t.GroupBySection,
			&t.IsActive, &createdBy, &t.CreatedAt, &t.UpdatedAt,
			&t.LineCount, &t.OptionalCount, &t.TotalAmount,
		); err != nil {
			h.log.Error("Failed to scan quotation template", "error", err)
			continue
		}

		if code.Valid {
			t.Code = &code.String
		}
		if description.Valid {
			t.Description = &description.String
		}
		if termsAndConditions.Valid {
			t.TermsAndConditions = &termsAndConditions.String
		}
		if headerNote.Valid {
			t.HeaderNote = &headerNote.String
		}
		if footerNote.Valid {
			t.FooterNote = &footerNote.String
		}
		if paymentTermID.Valid {
			uid, _ := uuid.Parse(paymentTermID.String)
			t.PaymentTermID = &uid
		}
		if pricelistID.Valid {
			uid, _ := uuid.Parse(pricelistID.String)
			t.PricelistID = &uid
		}
		if createdBy.Valid {
			uid, _ := uuid.Parse(createdBy.String)
			t.CreatedBy = &uid
		}

		templates = append(templates, t)
	}

	response.Success(c, templates)
}

// GetQuotationTemplate returns a single quotation template with all details
func (h *Handler) GetQuotationTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	templateID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid template ID")
		return
	}

	// Get template
	query := `
		SELECT qt.id, qt.tenant_id, qt.name, qt.code, qt.description,
		       qt.validity_days, qt.payment_term_id, qt.pricelist_id,
		       qt.terms_and_conditions, qt.header_note, qt.footer_note,
		       qt.require_signature, qt.require_payment, qt.payment_percentage,
		       qt.show_line_subtotals, qt.show_line_discounts, qt.group_by_section,
		       qt.is_active, qt.created_by, qt.created_at, qt.updated_at
		FROM quotation_templates qt
		WHERE qt.id = $1 AND qt.tenant_id = $2
	`

	var t entity.QuotationTemplate
	var code, description, termsAndConditions, headerNote, footerNote sql.NullString
	var paymentTermID, pricelistID, createdBy sql.NullString

	err = h.db.QueryRow(query, templateID, tenantID).Scan(
		&t.ID, &t.TenantID, &t.Name, &code, &description,
		&t.ValidityDays, &paymentTermID, &pricelistID,
		&termsAndConditions, &headerNote, &footerNote,
		&t.RequireSignature, &t.RequirePayment, &t.PaymentPercentage,
		&t.ShowLineSubtotals, &t.ShowLineDiscounts, &t.GroupBySection,
		&t.IsActive, &createdBy, &t.CreatedAt, &t.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Template not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get quotation template", "error", err)
		response.InternalServerError(c, "Failed to get quotation template")
		return
	}

	if code.Valid {
		t.Code = &code.String
	}
	if description.Valid {
		t.Description = &description.String
	}
	if termsAndConditions.Valid {
		t.TermsAndConditions = &termsAndConditions.String
	}
	if headerNote.Valid {
		t.HeaderNote = &headerNote.String
	}
	if footerNote.Valid {
		t.FooterNote = &footerNote.String
	}
	if paymentTermID.Valid {
		uid, _ := uuid.Parse(paymentTermID.String)
		t.PaymentTermID = &uid
	}
	if pricelistID.Valid {
		uid, _ := uuid.Parse(pricelistID.String)
		t.PricelistID = &uid
	}
	if createdBy.Valid {
		uid, _ := uuid.Parse(createdBy.String)
		t.CreatedBy = &uid
	}

	// Get sections
	sectionsQuery := `
		SELECT id, template_id, name, description, sequence, created_at
		FROM quotation_template_sections
		WHERE template_id = $1
		ORDER BY sequence ASC
	`
	sectionRows, err := h.db.Query(sectionsQuery, templateID)
	if err != nil {
		h.log.Error("Failed to get template sections", "error", err)
	} else {
		defer sectionRows.Close()
		for sectionRows.Next() {
			var s entity.QuotationTemplateSection
			var desc sql.NullString
			if err := sectionRows.Scan(&s.ID, &s.TemplateID, &s.Name, &desc, &s.Sequence, &s.CreatedAt); err != nil {
				continue
			}
			if desc.Valid {
				s.Description = &desc.String
			}
			t.Sections = append(t.Sections, s)
		}
	}

	// Get lines
	linesQuery := `
		SELECT l.id, l.template_id, l.section_id, l.product_id, l.product_name,
		       l.description, l.quantity, l.unit_id, l.unit_price,
		       l.discount_type, l.discount_value, l.sequence, l.display_type,
		       l.created_at, l.updated_at,
		       p.name as actual_product_name, p.list_price
		FROM quotation_template_lines l
		LEFT JOIN products p ON l.product_id = p.id
		WHERE l.template_id = $1
		ORDER BY l.sequence ASC
	`
	lineRows, err := h.db.Query(linesQuery, templateID)
	if err != nil {
		h.log.Error("Failed to get template lines", "error", err)
	} else {
		defer lineRows.Close()
		for lineRows.Next() {
			var l entity.QuotationTemplateLine
			var sectionID, productID, unitID sql.NullString
			var productName, description, discountType sql.NullString
			var unitPrice sql.NullFloat64
			var actualProductName sql.NullString
			var productListPrice sql.NullFloat64

			if err := lineRows.Scan(
				&l.ID, &l.TemplateID, &sectionID, &productID, &productName,
				&description, &l.Quantity, &unitID, &unitPrice,
				&discountType, &l.DiscountValue, &l.Sequence, &l.DisplayType,
				&l.CreatedAt, &l.UpdatedAt,
				&actualProductName, &productListPrice,
			); err != nil {
				continue
			}

			if sectionID.Valid {
				uid, _ := uuid.Parse(sectionID.String)
				l.SectionID = &uid
			}
			if productID.Valid {
				uid, _ := uuid.Parse(productID.String)
				l.ProductID = &uid
			}
			if productName.Valid {
				l.ProductName = &productName.String
			} else if actualProductName.Valid {
				l.ProductName = &actualProductName.String
			}
			if description.Valid {
				l.Description = &description.String
			}
			if unitID.Valid {
				uid, _ := uuid.Parse(unitID.String)
				l.UnitID = &uid
			}
			if unitPrice.Valid {
				l.UnitPrice = &unitPrice.Float64
			} else if productListPrice.Valid {
				l.UnitPrice = &productListPrice.Float64
			}
			if discountType.Valid {
				l.DiscountType = &discountType.String
			}

			t.Lines = append(t.Lines, l)
		}
	}

	// Get optionals
	optionalsQuery := `
		SELECT o.id, o.template_id, o.product_id, o.product_name,
		       o.description, o.quantity, o.unit_id, o.unit_price,
		       o.discount_type, o.discount_value, o.sequence,
		       o.created_at, o.updated_at,
		       p.name as actual_product_name, p.list_price
		FROM quotation_template_optionals o
		LEFT JOIN products p ON o.product_id = p.id
		WHERE o.template_id = $1
		ORDER BY o.sequence ASC
	`
	optionalRows, err := h.db.Query(optionalsQuery, templateID)
	if err != nil {
		h.log.Error("Failed to get template optionals", "error", err)
	} else {
		defer optionalRows.Close()
		for optionalRows.Next() {
			var o entity.QuotationTemplateOptional
			var productID, unitID sql.NullString
			var productName, description, discountType sql.NullString
			var unitPrice sql.NullFloat64
			var actualProductName sql.NullString
			var productListPrice sql.NullFloat64

			if err := optionalRows.Scan(
				&o.ID, &o.TemplateID, &productID, &productName,
				&description, &o.Quantity, &unitID, &unitPrice,
				&discountType, &o.DiscountValue, &o.Sequence,
				&o.CreatedAt, &o.UpdatedAt,
				&actualProductName, &productListPrice,
			); err != nil {
				continue
			}

			if productID.Valid {
				uid, _ := uuid.Parse(productID.String)
				o.ProductID = &uid
			}
			if productName.Valid {
				o.ProductName = &productName.String
			} else if actualProductName.Valid {
				o.ProductName = &actualProductName.String
			}
			if description.Valid {
				o.Description = &description.String
			}
			if unitID.Valid {
				uid, _ := uuid.Parse(unitID.String)
				o.UnitID = &uid
			}
			if unitPrice.Valid {
				o.UnitPrice = &unitPrice.Float64
			} else if productListPrice.Valid {
				o.UnitPrice = &productListPrice.Float64
			}
			if discountType.Valid {
				o.DiscountType = &discountType.String
			}

			t.Optionals = append(t.Optionals, o)
		}
	}

	response.Success(c, t)
}

// CreateQuotationTemplate creates a new quotation template
func (h *Handler) CreateQuotationTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.CreateQuotationTemplateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalServerError(c, "Failed to create template")
		return
	}
	defer tx.Rollback()

	// Set defaults
	validityDays := 30
	if input.ValidityDays > 0 {
		validityDays = input.ValidityDays
	}

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	showLineSubtotals := true
	if input.ShowLineSubtotals != nil {
		showLineSubtotals = *input.ShowLineSubtotals
	}

	showLineDiscounts := true
	if input.ShowLineDiscounts != nil {
		showLineDiscounts = *input.ShowLineDiscounts
	}

	groupBySection := true
	if input.GroupBySection != nil {
		groupBySection = *input.GroupBySection
	}

	// Insert template
	query := `
		INSERT INTO quotation_templates (
			tenant_id, name, code, description, validity_days,
			payment_term_id, pricelist_id, terms_and_conditions,
			header_note, footer_note, require_signature, require_payment,
			payment_percentage, show_line_subtotals, show_line_discounts,
			group_by_section, is_active, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING id, created_at, updated_at
	`

	var paymentTermID, pricelistID *uuid.UUID
	if input.PaymentTermID != "" {
		uid, _ := uuid.Parse(input.PaymentTermID)
		paymentTermID = &uid
	}
	if input.PricelistID != "" {
		uid, _ := uuid.Parse(input.PricelistID)
		pricelistID = &uid
	}

	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		userIDPtr = &userID
	}

	var templateID uuid.UUID
	var createdAt, updatedAt time.Time

	err = tx.QueryRow(
		query,
		tenantID, input.Name,
		sql.NullString{String: input.Code, Valid: input.Code != ""},
		sql.NullString{String: input.Description, Valid: input.Description != ""},
		validityDays, paymentTermID, pricelistID,
		sql.NullString{String: input.TermsAndConditions, Valid: input.TermsAndConditions != ""},
		sql.NullString{String: input.HeaderNote, Valid: input.HeaderNote != ""},
		sql.NullString{String: input.FooterNote, Valid: input.FooterNote != ""},
		input.RequireSignature, input.RequirePayment, input.PaymentPercentage,
		showLineSubtotals, showLineDiscounts, groupBySection,
		isActive, userIDPtr,
	).Scan(&templateID, &createdAt, &updatedAt)

	if err != nil {
		h.log.Error("Failed to create quotation template", "error", err)
		response.InternalServerError(c, "Failed to create template")
		return
	}

	// Insert sections
	sectionIDMap := make(map[string]uuid.UUID) // Map old IDs to new IDs for lines
	for i, section := range input.Sections {
		sectionQuery := `
			INSERT INTO quotation_template_sections (template_id, name, description, sequence)
			VALUES ($1, $2, $3, $4)
			RETURNING id
		`
		sequence := section.Sequence
		if sequence == 0 {
			sequence = (i + 1) * 10
		}

		var sectionID uuid.UUID
		err = tx.QueryRow(
			sectionQuery, templateID, section.Name,
			sql.NullString{String: section.Description, Valid: section.Description != ""},
			sequence,
		).Scan(&sectionID)

		if err != nil {
			h.log.Error("Failed to create section", "error", err)
			continue
		}

		if section.ID != "" {
			sectionIDMap[section.ID] = sectionID
		}
	}

	// Insert lines
	for i, line := range input.Lines {
		lineQuery := `
			INSERT INTO quotation_template_lines (
				template_id, section_id, product_id, product_name, description,
				quantity, unit_id, unit_price, discount_type, discount_value,
				sequence, display_type
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`

		var sectionID, productID, unitID *uuid.UUID
		if line.SectionID != "" {
			if mappedID, ok := sectionIDMap[line.SectionID]; ok {
				sectionID = &mappedID
			} else if uid, err := uuid.Parse(line.SectionID); err == nil {
				sectionID = &uid
			}
		}
		if line.ProductID != "" {
			uid, _ := uuid.Parse(line.ProductID)
			productID = &uid
		}
		if line.UnitID != "" {
			uid, _ := uuid.Parse(line.UnitID)
			unitID = &uid
		}

		quantity := line.Quantity
		if quantity == 0 {
			quantity = 1
		}

		sequence := line.Sequence
		if sequence == 0 {
			sequence = (i + 1) * 10
		}

		displayType := line.DisplayType
		if displayType == "" {
			displayType = "product"
		}

		_, err = tx.Exec(
			lineQuery,
			templateID, sectionID, productID,
			sql.NullString{String: line.ProductName, Valid: line.ProductName != ""},
			sql.NullString{String: line.Description, Valid: line.Description != ""},
			quantity, unitID, line.UnitPrice,
			sql.NullString{String: line.DiscountType, Valid: line.DiscountType != ""},
			line.DiscountValue, sequence, displayType,
		)

		if err != nil {
			h.log.Error("Failed to create line", "error", err)
		}
	}

	// Insert optionals
	for i, optional := range input.Optionals {
		optionalQuery := `
			INSERT INTO quotation_template_optionals (
				template_id, product_id, product_name, description,
				quantity, unit_id, unit_price, discount_type, discount_value, sequence
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`

		var productID, unitID *uuid.UUID
		if optional.ProductID != "" {
			uid, _ := uuid.Parse(optional.ProductID)
			productID = &uid
		}
		if optional.UnitID != "" {
			uid, _ := uuid.Parse(optional.UnitID)
			unitID = &uid
		}

		quantity := optional.Quantity
		if quantity == 0 {
			quantity = 1
		}

		sequence := optional.Sequence
		if sequence == 0 {
			sequence = (i + 1) * 10
		}

		_, err = tx.Exec(
			optionalQuery,
			templateID, productID,
			sql.NullString{String: optional.ProductName, Valid: optional.ProductName != ""},
			sql.NullString{String: optional.Description, Valid: optional.Description != ""},
			quantity, unitID, optional.UnitPrice,
			sql.NullString{String: optional.DiscountType, Valid: optional.DiscountType != ""},
			optional.DiscountValue, sequence,
		)

		if err != nil {
			h.log.Error("Failed to create optional", "error", err)
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "Failed to create template")
		return
	}

	response.Created(c, gin.H{
		"id":         templateID,
		"created_at": createdAt,
		"message":    "Template created successfully",
	})
}

// UpdateQuotationTemplate updates an existing quotation template
func (h *Handler) UpdateQuotationTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	templateID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid template ID")
		return
	}

	var input entity.UpdateQuotationTemplateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalServerError(c, "Failed to update template")
		return
	}
	defer tx.Rollback()

	// Build update query
	query := "UPDATE quotation_templates SET updated_at = NOW()"
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		query += fmt.Sprintf(", name = $%d", argCount)
		args = append(args, *input.Name)
	}
	if input.Code != nil {
		argCount++
		query += fmt.Sprintf(", code = $%d", argCount)
		args = append(args, *input.Code)
	}
	if input.Description != nil {
		argCount++
		query += fmt.Sprintf(", description = $%d", argCount)
		args = append(args, *input.Description)
	}
	if input.ValidityDays != nil {
		argCount++
		query += fmt.Sprintf(", validity_days = $%d", argCount)
		args = append(args, *input.ValidityDays)
	}
	if input.PaymentTermID != nil {
		argCount++
		if *input.PaymentTermID == "" {
			query += fmt.Sprintf(", payment_term_id = $%d", argCount)
			args = append(args, nil)
		} else {
			query += fmt.Sprintf(", payment_term_id = $%d", argCount)
			uid, _ := uuid.Parse(*input.PaymentTermID)
			args = append(args, uid)
		}
	}
	if input.PricelistID != nil {
		argCount++
		if *input.PricelistID == "" {
			query += fmt.Sprintf(", pricelist_id = $%d", argCount)
			args = append(args, nil)
		} else {
			query += fmt.Sprintf(", pricelist_id = $%d", argCount)
			uid, _ := uuid.Parse(*input.PricelistID)
			args = append(args, uid)
		}
	}
	if input.TermsAndConditions != nil {
		argCount++
		query += fmt.Sprintf(", terms_and_conditions = $%d", argCount)
		args = append(args, *input.TermsAndConditions)
	}
	if input.HeaderNote != nil {
		argCount++
		query += fmt.Sprintf(", header_note = $%d", argCount)
		args = append(args, *input.HeaderNote)
	}
	if input.FooterNote != nil {
		argCount++
		query += fmt.Sprintf(", footer_note = $%d", argCount)
		args = append(args, *input.FooterNote)
	}
	if input.RequireSignature != nil {
		argCount++
		query += fmt.Sprintf(", require_signature = $%d", argCount)
		args = append(args, *input.RequireSignature)
	}
	if input.RequirePayment != nil {
		argCount++
		query += fmt.Sprintf(", require_payment = $%d", argCount)
		args = append(args, *input.RequirePayment)
	}
	if input.PaymentPercentage != nil {
		argCount++
		query += fmt.Sprintf(", payment_percentage = $%d", argCount)
		args = append(args, *input.PaymentPercentage)
	}
	if input.ShowLineSubtotals != nil {
		argCount++
		query += fmt.Sprintf(", show_line_subtotals = $%d", argCount)
		args = append(args, *input.ShowLineSubtotals)
	}
	if input.ShowLineDiscounts != nil {
		argCount++
		query += fmt.Sprintf(", show_line_discounts = $%d", argCount)
		args = append(args, *input.ShowLineDiscounts)
	}
	if input.GroupBySection != nil {
		argCount++
		query += fmt.Sprintf(", group_by_section = $%d", argCount)
		args = append(args, *input.GroupBySection)
	}
	if input.IsActive != nil {
		argCount++
		query += fmt.Sprintf(", is_active = $%d", argCount)
		args = append(args, *input.IsActive)
	}

	argCount++
	query += fmt.Sprintf(" WHERE id = $%d", argCount)
	args = append(args, templateID)

	argCount++
	query += fmt.Sprintf(" AND tenant_id = $%d", argCount)
	args = append(args, tenantID)

	result, err := tx.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update template", "error", err)
		response.InternalServerError(c, "Failed to update template")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Template not found")
		return
	}

	// Update sections if provided
	if input.Sections != nil {
		// Delete existing sections
		tx.Exec("DELETE FROM quotation_template_sections WHERE template_id = $1", templateID)

		// Insert new sections
		for i, section := range input.Sections {
			sectionQuery := `
				INSERT INTO quotation_template_sections (template_id, name, description, sequence)
				VALUES ($1, $2, $3, $4)
			`
			sequence := section.Sequence
			if sequence == 0 {
				sequence = (i + 1) * 10
			}
			tx.Exec(sectionQuery, templateID, section.Name,
				sql.NullString{String: section.Description, Valid: section.Description != ""},
				sequence)
		}
	}

	// Update lines if provided
	if input.Lines != nil {
		// Delete existing lines
		tx.Exec("DELETE FROM quotation_template_lines WHERE template_id = $1", templateID)

		// Insert new lines
		for i, line := range input.Lines {
			lineQuery := `
				INSERT INTO quotation_template_lines (
					template_id, section_id, product_id, product_name, description,
					quantity, unit_id, unit_price, discount_type, discount_value,
					sequence, display_type
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			`

			var sectionID, productID, unitID *uuid.UUID
			if line.SectionID != "" {
				uid, _ := uuid.Parse(line.SectionID)
				sectionID = &uid
			}
			if line.ProductID != "" {
				uid, _ := uuid.Parse(line.ProductID)
				productID = &uid
			}
			if line.UnitID != "" {
				uid, _ := uuid.Parse(line.UnitID)
				unitID = &uid
			}

			quantity := line.Quantity
			if quantity == 0 {
				quantity = 1
			}

			sequence := line.Sequence
			if sequence == 0 {
				sequence = (i + 1) * 10
			}

			displayType := line.DisplayType
			if displayType == "" {
				displayType = "product"
			}

			tx.Exec(lineQuery,
				templateID, sectionID, productID,
				sql.NullString{String: line.ProductName, Valid: line.ProductName != ""},
				sql.NullString{String: line.Description, Valid: line.Description != ""},
				quantity, unitID, line.UnitPrice,
				sql.NullString{String: line.DiscountType, Valid: line.DiscountType != ""},
				line.DiscountValue, sequence, displayType)
		}
	}

	// Update optionals if provided
	if input.Optionals != nil {
		// Delete existing optionals
		tx.Exec("DELETE FROM quotation_template_optionals WHERE template_id = $1", templateID)

		// Insert new optionals
		for i, optional := range input.Optionals {
			optionalQuery := `
				INSERT INTO quotation_template_optionals (
					template_id, product_id, product_name, description,
					quantity, unit_id, unit_price, discount_type, discount_value, sequence
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			`

			var productID, unitID *uuid.UUID
			if optional.ProductID != "" {
				uid, _ := uuid.Parse(optional.ProductID)
				productID = &uid
			}
			if optional.UnitID != "" {
				uid, _ := uuid.Parse(optional.UnitID)
				unitID = &uid
			}

			quantity := optional.Quantity
			if quantity == 0 {
				quantity = 1
			}

			sequence := optional.Sequence
			if sequence == 0 {
				sequence = (i + 1) * 10
			}

			tx.Exec(optionalQuery,
				templateID, productID,
				sql.NullString{String: optional.ProductName, Valid: optional.ProductName != ""},
				sql.NullString{String: optional.Description, Valid: optional.Description != ""},
				quantity, unitID, optional.UnitPrice,
				sql.NullString{String: optional.DiscountType, Valid: optional.DiscountType != ""},
				optional.DiscountValue, sequence)
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "Failed to update template")
		return
	}

	response.Success(c, gin.H{"message": "Template updated successfully"})
}

// DeleteQuotationTemplate deletes a quotation template
func (h *Handler) DeleteQuotationTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	templateID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid template ID")
		return
	}

	result, err := h.db.Exec(
		"DELETE FROM quotation_templates WHERE id = $1 AND tenant_id = $2",
		templateID, tenantID,
	)
	if err != nil {
		h.log.Error("Failed to delete template", "error", err)
		response.InternalServerError(c, "Failed to delete template")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Template not found")
		return
	}

	response.Success(c, gin.H{"message": "Template deleted successfully"})
}

// DuplicateQuotationTemplate creates a copy of an existing template
func (h *Handler) DuplicateQuotationTemplate(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	templateID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid template ID")
		return
	}

	// Get original template
	originalQuery := `
		SELECT name, code, description, validity_days, payment_term_id, pricelist_id,
		       terms_and_conditions, header_note, footer_note, require_signature,
		       require_payment, payment_percentage, show_line_subtotals,
		       show_line_discounts, group_by_section
		FROM quotation_templates
		WHERE id = $1 AND tenant_id = $2
	`

	var name string
	var code, description, terms, header, footer sql.NullString
	var validityDays int
	var paymentTermID, pricelistID sql.NullString
	var requireSignature, requirePayment bool
	var paymentPercentage float64
	var showSubtotals, showDiscounts, groupBySection bool

	err = h.db.QueryRow(originalQuery, templateID, tenantID).Scan(
		&name, &code, &description, &validityDays, &paymentTermID, &pricelistID,
		&terms, &header, &footer, &requireSignature,
		&requirePayment, &paymentPercentage, &showSubtotals,
		&showDiscounts, &groupBySection,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Template not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get template", "error", err)
		response.InternalServerError(c, "Failed to duplicate template")
		return
	}

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalServerError(c, "Failed to duplicate template")
		return
	}
	defer tx.Rollback()

	// Create new template
	newName := name + " (Copy)"
	var newCode sql.NullString
	if code.Valid {
		newCode = sql.NullString{String: code.String + "_COPY", Valid: true}
	}

	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		userIDPtr = &userID
	}

	var ptID, plID *uuid.UUID
	if paymentTermID.Valid {
		uid, _ := uuid.Parse(paymentTermID.String)
		ptID = &uid
	}
	if pricelistID.Valid {
		uid, _ := uuid.Parse(pricelistID.String)
		plID = &uid
	}

	insertQuery := `
		INSERT INTO quotation_templates (
			tenant_id, name, code, description, validity_days, payment_term_id, pricelist_id,
			terms_and_conditions, header_note, footer_note, require_signature,
			require_payment, payment_percentage, show_line_subtotals,
			show_line_discounts, group_by_section, is_active, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, true, $17)
		RETURNING id
	`

	var newTemplateID uuid.UUID
	err = tx.QueryRow(
		insertQuery,
		tenantID, newName, newCode, description, validityDays, ptID, plID,
		terms, header, footer, requireSignature,
		requirePayment, paymentPercentage, showSubtotals,
		showDiscounts, groupBySection, userIDPtr,
	).Scan(&newTemplateID)

	if err != nil {
		h.log.Error("Failed to create template copy", "error", err)
		response.InternalServerError(c, "Failed to duplicate template")
		return
	}

	// Copy sections
	sectionMap := make(map[uuid.UUID]uuid.UUID)
	sectionsQuery := `SELECT id, name, description, sequence FROM quotation_template_sections WHERE template_id = $1`
	sectionRows, _ := tx.Query(sectionsQuery, templateID)
	if sectionRows != nil {
		defer sectionRows.Close()
		for sectionRows.Next() {
			var oldID uuid.UUID
			var sName string
			var sDesc sql.NullString
			var sSeq int
			sectionRows.Scan(&oldID, &sName, &sDesc, &sSeq)

			var newSectionID uuid.UUID
			tx.QueryRow(
				`INSERT INTO quotation_template_sections (template_id, name, description, sequence)
				 VALUES ($1, $2, $3, $4) RETURNING id`,
				newTemplateID, sName, sDesc, sSeq,
			).Scan(&newSectionID)
			sectionMap[oldID] = newSectionID
		}
	}

	// Copy lines
	linesQuery := `
		SELECT section_id, product_id, product_name, description, quantity,
		       unit_id, unit_price, discount_type, discount_value, sequence, display_type
		FROM quotation_template_lines WHERE template_id = $1
	`
	lineRows, _ := tx.Query(linesQuery, templateID)
	if lineRows != nil {
		defer lineRows.Close()
		for lineRows.Next() {
			var sectionID, productID, unitID sql.NullString
			var productName, desc, discType sql.NullString
			var qty float64
			var unitPrice sql.NullFloat64
			var discVal float64
			var seq int
			var dispType string

			lineRows.Scan(&sectionID, &productID, &productName, &desc, &qty,
				&unitID, &unitPrice, &discType, &discVal, &seq, &dispType)

			var newSectionID *uuid.UUID
			if sectionID.Valid {
				oldSID, _ := uuid.Parse(sectionID.String)
				if newSID, ok := sectionMap[oldSID]; ok {
					newSectionID = &newSID
				}
			}

			var pID, uID *uuid.UUID
			if productID.Valid {
				uid, _ := uuid.Parse(productID.String)
				pID = &uid
			}
			if unitID.Valid {
				uid, _ := uuid.Parse(unitID.String)
				uID = &uid
			}

			tx.Exec(`
				INSERT INTO quotation_template_lines (
					template_id, section_id, product_id, product_name, description,
					quantity, unit_id, unit_price, discount_type, discount_value, sequence, display_type
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
				newTemplateID, newSectionID, pID, productName, desc,
				qty, uID, unitPrice, discType, discVal, seq, dispType)
		}
	}

	// Copy optionals
	optionalsQuery := `
		SELECT product_id, product_name, description, quantity,
		       unit_id, unit_price, discount_type, discount_value, sequence
		FROM quotation_template_optionals WHERE template_id = $1
	`
	optRows, _ := tx.Query(optionalsQuery, templateID)
	if optRows != nil {
		defer optRows.Close()
		for optRows.Next() {
			var productID, unitID sql.NullString
			var productName, desc, discType sql.NullString
			var qty float64
			var unitPrice sql.NullFloat64
			var discVal float64
			var seq int

			optRows.Scan(&productID, &productName, &desc, &qty, &unitID, &unitPrice, &discType, &discVal, &seq)

			var pID, uID *uuid.UUID
			if productID.Valid {
				uid, _ := uuid.Parse(productID.String)
				pID = &uid
			}
			if unitID.Valid {
				uid, _ := uuid.Parse(unitID.String)
				uID = &uid
			}

			tx.Exec(`
				INSERT INTO quotation_template_optionals (
					template_id, product_id, product_name, description,
					quantity, unit_id, unit_price, discount_type, discount_value, sequence
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				newTemplateID, pID, productName, desc, qty, uID, unitPrice, discType, discVal, seq)
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalServerError(c, "Failed to duplicate template")
		return
	}

	response.Created(c, gin.H{
		"id":      newTemplateID,
		"message": "Template duplicated successfully",
	})
}
