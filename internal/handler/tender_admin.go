package handler

import (
	"database/sql"
	"fmt"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TenderAdminDashboard returns platform-wide statistics
func (h *Handler) TenderAdminDashboard(c *gin.Context) {
	var totalCompanies, totalBuyers, totalSuppliers, totalTenders, activeTenders, completedTenders int
	var totalBids, totalProducts, totalReviews, pendingVerifications int
	var tendersThisWeek, bidsThisWeek, newCompaniesThisWeek int

	// Total counts
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_company_profiles WHERE deleted_at IS NULL`).Scan(&totalCompanies)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_company_profiles WHERE role = 'buyer' AND deleted_at IS NULL`).Scan(&totalBuyers)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_company_profiles WHERE role = 'supplier' AND deleted_at IS NULL`).Scan(&totalSuppliers)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_tenders WHERE deleted_at IS NULL`).Scan(&totalTenders)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_tenders WHERE status = 'active' AND deleted_at IS NULL`).Scan(&activeTenders)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_tenders WHERE status = 'completed' AND deleted_at IS NULL`).Scan(&completedTenders)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_bids`).Scan(&totalBids)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_products WHERE deleted_at IS NULL`).Scan(&totalProducts)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_reviews`).Scan(&totalReviews)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_company_profiles WHERE is_verified = false AND deleted_at IS NULL`).Scan(&pendingVerifications)

	// Recent activity
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_tenders WHERE created_at >= NOW() - INTERVAL '7 days' AND deleted_at IS NULL`).Scan(&tendersThisWeek)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_bids WHERE created_at >= NOW() - INTERVAL '7 days'`).Scan(&bidsThisWeek)
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_company_profiles WHERE created_at >= NOW() - INTERVAL '7 days' AND deleted_at IS NULL`).Scan(&newCompaniesThisWeek)

	response.Success(c, gin.H{
		"total_companies":         totalCompanies,
		"total_buyers":            totalBuyers,
		"total_suppliers":         totalSuppliers,
		"total_tenders":           totalTenders,
		"active_tenders":          activeTenders,
		"completed_tenders":       completedTenders,
		"total_bids":              totalBids,
		"total_products":          totalProducts,
		"total_reviews":           totalReviews,
		"pending_verifications":   pendingVerifications,
		"tenders_this_week":       tendersThisWeek,
		"bids_this_week":          bidsThisWeek,
		"new_companies_this_week": newCompaniesThisWeek,
	})
}

// TenderAdminListUsers lists all tender platform users (company profiles)
func (h *Handler) TenderAdminListUsers(c *gin.Context) {
	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	role := c.Query("role")
	search := c.Query("search")
	isVerified := c.Query("is_verified")

	pagination := entity.NewPagination(page, limit)

	countQuery := `SELECT COUNT(*) FROM tender_company_profiles WHERE deleted_at IS NULL`
	args := []interface{}{}
	argIdx := 1

	if role != "" {
		countQuery += fmt.Sprintf(" AND role = $%d", argIdx)
		args = append(args, role)
		argIdx++
	}
	if isVerified == "true" {
		countQuery += fmt.Sprintf(" AND is_verified = $%d", argIdx)
		args = append(args, true)
		argIdx++
	} else if isVerified == "false" {
		countQuery += fmt.Sprintf(" AND is_verified = $%d", argIdx)
		args = append(args, false)
		argIdx++
	}
	if search != "" {
		countQuery += fmt.Sprintf(" AND (company_name ILIKE $%d OR inn ILIKE $%d OR phone ILIKE $%d)", argIdx, argIdx, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)
	pagination.Calculate(total)

	query := `
		SELECT cp.id, cp.user_id, cp.role, cp.company_name, cp.inn, cp.phone,
		       cp.region_id, cp.is_verified, cp.rating, cp.review_count,
		       cp.tender_count, cp.bid_count, cp.won_count, cp.created_at,
		       COALESCE(r.name, '') as region_name
		FROM tender_company_profiles cp
		LEFT JOIN tender_regions r ON r.id = cp.region_id
		WHERE cp.deleted_at IS NULL
	`
	qArgs := []interface{}{}
	qIdx := 1

	if role != "" {
		query += fmt.Sprintf(" AND cp.role = $%d", qIdx)
		qArgs = append(qArgs, role)
		qIdx++
	}
	if isVerified == "true" {
		query += fmt.Sprintf(" AND cp.is_verified = $%d", qIdx)
		qArgs = append(qArgs, true)
		qIdx++
	} else if isVerified == "false" {
		query += fmt.Sprintf(" AND cp.is_verified = $%d", qIdx)
		qArgs = append(qArgs, false)
		qIdx++
	}
	if search != "" {
		query += fmt.Sprintf(" AND (cp.company_name ILIKE $%d OR cp.inn ILIKE $%d OR cp.phone ILIKE $%d)", qIdx, qIdx, qIdx)
		qArgs = append(qArgs, "%"+search+"%")
		qIdx++
	}

	query += fmt.Sprintf(" ORDER BY cp.created_at DESC LIMIT $%d OFFSET $%d", qIdx, qIdx+1)
	qArgs = append(qArgs, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, qArgs...)
	if err != nil {
		h.log.Error("Failed to list admin users", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var profiles []entity.CompanyProfileResponse
	for rows.Next() {
		var p entity.CompanyProfileResponse
		var regionID sql.NullString
		var regionName sql.NullString

		err := rows.Scan(
			&p.ID, &p.UserID, &p.Role, &p.CompanyName, &p.INN, &p.Phone,
			&regionID, &p.IsVerified, &p.Rating, &p.ReviewCount,
			&p.TenderCount, &p.BidCount, &p.WonCount, &p.CreatedAt,
			&regionName,
		)
		if err != nil {
			continue
		}
		if regionID.Valid {
			parsed, _ := uuid.Parse(regionID.String)
			p.RegionID = &parsed
		}
		if regionName.Valid {
			p.RegionName = regionName.String
		}

		profiles = append(profiles, p)
	}

	response.SuccessWithPagination(c, profiles, pagination)
}

// TenderAdminVerifyCompany verifies or rejects a company
func (h *Handler) TenderAdminVerifyCompany(c *gin.Context) {
	companyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	var input struct {
		Verified bool   `json:"verified"`
		Reason   string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	_, err = h.db.Exec(`
		UPDATE tender_company_profiles SET is_verified = $1, verified_at = CASE WHEN $1 THEN NOW() ELSE NULL END, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`, input.Verified, companyID)
	if err != nil {
		response.InternalServerError(c, "Failed to update verification status")
		return
	}

	status := "verified"
	if !input.Verified {
		status = "rejected"
	}

	h.writePlatformAudit(c, "tender.company.verify", "company", companyID.String(), nil, nil,
		map[string]interface{}{"is_verified": input.Verified, "status": status, "reason": input.Reason})

	response.Success(c, map[string]interface{}{"id": companyID, "status": status})
}

// TenderAdminUpdateUser updates a user's profile (activate, deactivate, change role)
func (h *Handler) TenderAdminUpdateUser(c *gin.Context) {
	companyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	var input struct {
		Role       string `json:"role"`
		IsVerified *bool  `json:"is_verified"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	query := "UPDATE tender_company_profiles SET updated_at = NOW()"
	args := []interface{}{}
	argIdx := 1

	if input.Role != "" {
		query += fmt.Sprintf(", role = $%d", argIdx)
		args = append(args, input.Role)
		argIdx++
	}
	if input.IsVerified != nil {
		query += fmt.Sprintf(", is_verified = $%d", argIdx)
		args = append(args, *input.IsVerified)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d AND deleted_at IS NULL", argIdx)
	args = append(args, companyID)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		response.InternalServerError(c, "Failed to update user")
		return
	}

	after := map[string]interface{}{}
	if input.Role != "" {
		after["role"] = input.Role
	}
	if input.IsVerified != nil {
		after["is_verified"] = *input.IsVerified
	}
	h.writePlatformAudit(c, "tender.user.update", "company", companyID.String(), nil, nil, after)

	response.Success(c, map[string]interface{}{"id": companyID, "message": "User updated"})
}

// TenderAdminListTenders lists all tenders for admin oversight
func (h *Handler) TenderAdminListTenders(c *gin.Context) {
	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	status := c.Query("status")
	search := c.Query("search")

	pagination := entity.NewPagination(page, limit)

	countQuery := `SELECT COUNT(*) FROM tender_tenders WHERE deleted_at IS NULL`
	args := []interface{}{}
	argIdx := 1

	if status != "" {
		countQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}
	if search != "" {
		countQuery += fmt.Sprintf(" AND title ILIKE $%d", argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)
	pagination.Calculate(total)

	query := `
		SELECT t.id, t.buyer_id, t.title, t.status, t.tender_type,
		       t.deadline, t.bid_count, t.created_at,
		       COALESCE(cp.company_name, '') as buyer_name,
		       COALESCE(r.name, '') as region_name
		FROM tender_tenders t
		LEFT JOIN tender_company_profiles cp ON cp.user_id = t.buyer_id AND cp.deleted_at IS NULL
		LEFT JOIN tender_regions r ON r.id = t.region_id
		WHERE t.deleted_at IS NULL
	`
	qArgs := []interface{}{}
	qIdx := 1

	if status != "" {
		query += fmt.Sprintf(" AND t.status = $%d", qIdx)
		qArgs = append(qArgs, status)
		qIdx++
	}
	if search != "" {
		query += fmt.Sprintf(" AND t.title ILIKE $%d", qIdx)
		qArgs = append(qArgs, "%"+search+"%")
		qIdx++
	}

	query += fmt.Sprintf(" ORDER BY t.created_at DESC LIMIT $%d OFFSET $%d", qIdx, qIdx+1)
	qArgs = append(qArgs, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, qArgs...)
	if err != nil {
		h.log.Error("Failed to list admin tenders", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	type AdminTenderRow struct {
		ID         uuid.UUID `json:"id"`
		BuyerID    uuid.UUID `json:"buyer_id"`
		BuyerName  string    `json:"buyer_name"`
		Title      string    `json:"title"`
		Status     string    `json:"status"`
		TenderType string    `json:"tender_type"`
		RegionName string    `json:"region_name"`
		Deadline   string    `json:"deadline"`
		BidCount   int       `json:"bid_count"`
		CreatedAt  string    `json:"created_at"`
	}

	var tenders []AdminTenderRow
	for rows.Next() {
		var t AdminTenderRow
		var deadline, createdAt sql.NullTime
		var regionName sql.NullString

		err := rows.Scan(
			&t.ID, &t.BuyerID, &t.Title, &t.Status, &t.TenderType,
			&deadline, &t.BidCount, &createdAt,
			&t.BuyerName, &regionName,
		)
		if err != nil {
			continue
		}
		if deadline.Valid {
			t.Deadline = deadline.Time.Format("2006-01-02T15:04:05Z")
		}
		if createdAt.Valid {
			t.CreatedAt = createdAt.Time.Format("2006-01-02T15:04:05Z")
		}
		if regionName.Valid {
			t.RegionName = regionName.String
		}

		tenders = append(tenders, t)
	}

	response.SuccessWithPagination(c, tenders, pagination)
}

// TenderAdminCreateCategory creates a new category
func (h *Handler) TenderAdminCreateCategory(c *gin.Context) {
	var input entity.CreateTenderCategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	catID := uuid.New()
	_, err := h.db.Exec(`
		INSERT INTO tender_categories (id, parent_id, name, name_ru, slug, icon, banner, level, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, catID, input.ParentID, input.Name, input.NameRu, input.Slug, input.Icon, input.Banner, input.Level, input.SortOrder)
	if err != nil {
		h.log.Error("Failed to create category", "error", err)
		response.InternalServerError(c, "Failed to create category")
		return
	}

	response.Created(c, map[string]interface{}{"id": catID})
}

// TenderAdminUpdateCategory updates a category
func (h *Handler) TenderAdminUpdateCategory(c *gin.Context) {
	catID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	var input entity.CreateTenderCategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	_, err = h.db.Exec(`
		UPDATE tender_categories SET name = $1, name_ru = $2, slug = $3, icon = $4,
		       banner = $5, level = $6, sort_order = $7, parent_id = $8, updated_at = NOW()
		WHERE id = $9 AND deleted_at IS NULL
	`, input.Name, input.NameRu, input.Slug, input.Icon, input.Banner, input.Level, input.SortOrder, input.ParentID, catID)
	if err != nil {
		response.InternalServerError(c, "Failed to update category")
		return
	}

	response.Success(c, map[string]interface{}{"id": catID, "message": "Category updated"})
}

// TenderAdminDeleteCategory soft-deletes a category
func (h *Handler) TenderAdminDeleteCategory(c *gin.Context) {
	catID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	_, err = h.db.Exec(`UPDATE tender_categories SET deleted_at = NOW() WHERE id = $1`, catID)
	if err != nil {
		response.InternalServerError(c, "")
		return
	}

	response.NoContent(c)
}

// TenderAdminReports returns aggregate reports
func (h *Handler) TenderAdminReports(c *gin.Context) {
	reportType := c.DefaultQuery("type", "summary")

	switch reportType {
	case "tenders_by_region":
		rows, err := h.db.Query(`
			SELECT COALESCE(r.name, 'Belgilanmagan') as region, COUNT(*) as count
			FROM tender_tenders t
			LEFT JOIN tender_regions r ON r.id = t.region_id
			WHERE t.deleted_at IS NULL
			GROUP BY r.name
			ORDER BY count DESC
		`)
		if err != nil {
			response.InternalServerError(c, "")
			return
		}
		defer rows.Close()

		var results []map[string]interface{}
		for rows.Next() {
			var region string
			var count int
			if rows.Scan(&region, &count) == nil {
				results = append(results, map[string]interface{}{"region": region, "count": count})
			}
		}
		response.Success(c, results)

	case "tenders_by_status":
		rows, err := h.db.Query(`
			SELECT status, COUNT(*) as count
			FROM tender_tenders WHERE deleted_at IS NULL
			GROUP BY status
		`)
		if err != nil {
			response.InternalServerError(c, "")
			return
		}
		defer rows.Close()

		var results []map[string]interface{}
		for rows.Next() {
			var status string
			var count int
			if rows.Scan(&status, &count) == nil {
				results = append(results, map[string]interface{}{"status": status, "count": count})
			}
		}
		response.Success(c, results)

	case "top_suppliers":
		rows, err := h.db.Query(`
			SELECT cp.company_name, cp.rating, cp.won_count, cp.bid_count
			FROM tender_company_profiles cp
			WHERE cp.role = 'supplier' AND cp.deleted_at IS NULL
			ORDER BY cp.won_count DESC
			LIMIT 20
		`)
		if err != nil {
			response.InternalServerError(c, "")
			return
		}
		defer rows.Close()

		var results []map[string]interface{}
		for rows.Next() {
			var name string
			var rating float64
			var wonCount, bidCount int
			if rows.Scan(&name, &rating, &wonCount, &bidCount) == nil {
				results = append(results, map[string]interface{}{
					"company_name": name, "rating": rating,
					"won_count": wonCount, "bid_count": bidCount,
				})
			}
		}
		response.Success(c, results)

	default: // summary
		var sTotalTenders, sTotalBids, sTotalCompanies int
		var sAvgBids float64
		h.db.QueryRow(`SELECT COUNT(*) FROM tender_tenders WHERE deleted_at IS NULL`).Scan(&sTotalTenders)
		h.db.QueryRow(`SELECT COUNT(*) FROM tender_bids`).Scan(&sTotalBids)
		h.db.QueryRow(`SELECT COALESCE(AVG(bid_count), 0) FROM tender_tenders WHERE deleted_at IS NULL AND status = 'completed'`).Scan(&sAvgBids)
		h.db.QueryRow(`SELECT COUNT(*) FROM tender_company_profiles WHERE deleted_at IS NULL`).Scan(&sTotalCompanies)
		response.Success(c, gin.H{
			"total_tenders":       sTotalTenders,
			"total_bids":          sTotalBids,
			"avg_bids_per_tender": sAvgBids,
			"total_companies":     sTotalCompanies,
		})
	}
}
