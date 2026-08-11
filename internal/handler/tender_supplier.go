package handler

import (
	"database/sql"
	"fmt"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListSuppliers lists verified supplier profiles
func (h *Handler) ListSuppliers(c *gin.Context) {
	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	regionID := c.Query("region_id")
	search := c.Query("search")
	ordering := c.DefaultQuery("ordering", "-rating")

	pagination := entity.NewPagination(page, limit)

	countQuery := `SELECT COUNT(*) FROM tender_company_profiles WHERE role = 'supplier' AND deleted_at IS NULL AND is_verified = true`
	args := []interface{}{}
	argIdx := 1

	if regionID != "" {
		countQuery += fmt.Sprintf(" AND region_id = $%d", argIdx)
		args = append(args, regionID)
		argIdx++
	}
	if search != "" {
		countQuery += fmt.Sprintf(" AND (company_name ILIKE $%d OR inn ILIKE $%d)", argIdx, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)
	pagination.Calculate(total)

	query := `
		SELECT cp.id, cp.user_id, cp.company_name, cp.inn, cp.phone,
		       cp.region_id, cp.address, cp.logo, cp.description, cp.website,
		       cp.is_verified, cp.rating, cp.review_count, cp.tender_count,
		       cp.bid_count, cp.won_count, cp.activity_areas, cp.created_at,
		       COALESCE(r.name, '') as region_name
		FROM tender_company_profiles cp
		LEFT JOIN tender_regions r ON r.id = cp.region_id
		WHERE cp.role = 'supplier' AND cp.deleted_at IS NULL AND cp.is_verified = true
	`
	qArgs := []interface{}{}
	qIdx := 1

	if regionID != "" {
		query += fmt.Sprintf(" AND cp.region_id = $%d", qIdx)
		qArgs = append(qArgs, regionID)
		qIdx++
	}
	if search != "" {
		query += fmt.Sprintf(" AND (cp.company_name ILIKE $%d OR cp.inn ILIKE $%d)", qIdx, qIdx)
		qArgs = append(qArgs, "%"+search+"%")
		qIdx++
	}

	switch ordering {
	case "rating":
		query += " ORDER BY cp.rating ASC, cp.id ASC"
	case "-rating":
		query += " ORDER BY cp.rating DESC, cp.id ASC"
	case "name":
		query += " ORDER BY cp.company_name ASC, cp.id ASC"
	case "won_count":
		query += " ORDER BY cp.won_count DESC, cp.id ASC"
	default:
		query += " ORDER BY cp.rating DESC, cp.id ASC"
	}

	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", qIdx, qIdx+1)
	qArgs = append(qArgs, pagination.Limit, pagination.Offset())

	rows, err := h.db.Query(query, qArgs...)
	if err != nil {
		h.log.Error("Failed to list suppliers", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var suppliers []entity.CompanyProfileResponse
	for rows.Next() {
		var s entity.CompanyProfileResponse
		var regionID, logo, description, website sql.NullString
		var regionName sql.NullString
		var activityAreas sql.NullString

		err := rows.Scan(
			&s.ID, &s.UserID, &s.CompanyName, &s.INN, &s.Phone,
			&regionID, &s.Address, &logo, &description, &website,
			&s.IsVerified, &s.Rating, &s.ReviewCount, &s.TenderCount,
			&s.BidCount, &s.WonCount, &activityAreas, &s.CreatedAt,
			&regionName,
		)
		if err != nil {
			h.log.Error("Failed to scan supplier", "error", err)
			continue
		}

		if regionID.Valid {
			parsed, _ := uuid.Parse(regionID.String)
			s.RegionID = &parsed
		}
		if regionName.Valid {
			s.RegionName = regionName.String
		}
		if logo.Valid {
			s.Logo = logo.String
		}
		if description.Valid {
			s.Description = description.String
		}
		if website.Valid {
			s.Website = website.String
		}

		suppliers = append(suppliers, s)
	}

	response.SuccessWithPagination(c, suppliers, pagination)
}

// GetSupplierProfile retrieves a single supplier's public profile
func (h *Handler) GetSupplierProfile(c *gin.Context) {
	supplierID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid supplier ID")
		return
	}

	query := `
		SELECT cp.id, cp.user_id, cp.company_name, cp.inn, cp.phone,
		       cp.region_id, cp.address, cp.logo, cp.description, cp.website,
		       cp.is_verified, cp.rating, cp.review_count, cp.tender_count,
		       cp.bid_count, cp.won_count, cp.activity_areas, cp.created_at,
		       COALESCE(r.name, '') as region_name
		FROM tender_company_profiles cp
		LEFT JOIN tender_regions r ON r.id = cp.region_id
		WHERE cp.id = $1 AND cp.deleted_at IS NULL
	`

	var s entity.CompanyProfileResponse
	var regionID, logo, description, website sql.NullString
	var regionName sql.NullString
	var activityAreas sql.NullString

	err = h.db.QueryRow(query, supplierID).Scan(
		&s.ID, &s.UserID, &s.CompanyName, &s.INN, &s.Phone,
		&regionID, &s.Address, &logo, &description, &website,
		&s.IsVerified, &s.Rating, &s.ReviewCount, &s.TenderCount,
		&s.BidCount, &s.WonCount, &activityAreas, &s.CreatedAt,
		&regionName,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Supplier not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get supplier", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if regionID.Valid {
		parsed, _ := uuid.Parse(regionID.String)
		s.RegionID = &parsed
	}
	if regionName.Valid {
		s.RegionName = regionName.String
	}
	if logo.Valid {
		s.Logo = logo.String
	}
	if description.Valid {
		s.Description = description.String
	}
	if website.Valid {
		s.Website = website.String
	}

	response.Success(c, s)
}

// GetMyCompanyProfile gets the current user's company profile
func (h *Handler) GetMyCompanyProfile(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	query := `
		SELECT cp.id, cp.user_id, cp.role, cp.company_name, cp.inn, cp.phone,
		       cp.region_id, cp.address, cp.logo, cp.banner, cp.description, cp.website,
		       cp.is_verified, cp.rating, cp.review_count, cp.tender_count,
		       cp.bid_count, cp.won_count, cp.activity_areas,
		       cp.license_number, cp.license_file, cp.created_at,
		       COALESCE(r.name, '') as region_name
		FROM tender_company_profiles cp
		LEFT JOIN tender_regions r ON r.id = cp.region_id
		WHERE cp.user_id = $1 AND cp.deleted_at IS NULL
	`

	var s entity.CompanyProfileResponse
	var regionID, logo, banner, description, website sql.NullString
	var regionName sql.NullString
	var activityAreas, licenseNumber, licenseFile sql.NullString

	err := h.db.QueryRow(query, userID).Scan(
		&s.ID, &s.UserID, &s.Role, &s.CompanyName, &s.INN, &s.Phone,
		&regionID, &s.Address, &logo, &banner, &description, &website,
		&s.IsVerified, &s.Rating, &s.ReviewCount, &s.TenderCount,
		&s.BidCount, &s.WonCount, &activityAreas,
		&licenseNumber, &licenseFile, &s.CreatedAt,
		&regionName,
	)
	if err == sql.ErrNoRows {
		// Auto-create company profile from user registration data
		_, createErr := h.db.Exec(`
			INSERT INTO tender_company_profiles (id, user_id, role, company_name, inn, phone, region_id)
			SELECT u.id, u.id, u.role, COALESCE(NULLIF(u.company_name, ''), u.full_name), COALESCE(u.inn, ''), COALESCE(u.phone, ''), u.region_id
			FROM tender_users u WHERE u.id = $1
			ON CONFLICT (user_id) WHERE deleted_at IS NULL DO NOTHING
		`, userID)
		if createErr != nil {
			h.log.Error("Failed to auto-create company profile", "error", createErr)
			response.NotFound(c, "Company profile not found")
			return
		}
		// Re-fetch the auto-created profile
		err = h.db.QueryRow(query, userID).Scan(
			&s.ID, &s.UserID, &s.Role, &s.CompanyName, &s.INN, &s.Phone,
			&regionID, &s.Address, &logo, &banner, &description, &website,
			&s.IsVerified, &s.Rating, &s.ReviewCount, &s.TenderCount,
			&s.BidCount, &s.WonCount, &activityAreas,
			&licenseNumber, &licenseFile, &s.CreatedAt,
			&regionName,
		)
		if err != nil {
			response.NotFound(c, "Company profile not found")
			return
		}
	} else if err != nil {
		h.log.Error("Failed to get company profile", "error", err)
		response.InternalServerError(c, "")
		return
	}

	if regionID.Valid {
		parsed, _ := uuid.Parse(regionID.String)
		s.RegionID = &parsed
	}
	if regionName.Valid {
		s.RegionName = regionName.String
	}
	if logo.Valid {
		s.Logo = logo.String
	}
	if description.Valid {
		s.Description = description.String
	}
	if website.Valid {
		s.Website = website.String
	}

	response.Success(c, s)
}

// CreateCompanyProfile creates a company profile for the current user
func (h *Handler) CreateCompanyProfile(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var input entity.CreateCompanyProfileInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Check if profile already exists
	var existingCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_company_profiles WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&existingCount)
	if existingCount > 0 {
		response.BadRequest(c, "Company profile already exists")
		return
	}

	// Check INN uniqueness
	var innCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_company_profiles WHERE inn = $1 AND deleted_at IS NULL`, input.INN).Scan(&innCount)
	if innCount > 0 {
		response.BadRequest(c, "INN already registered")
		return
	}

	profileID := uuid.New()
	_, err := h.db.Exec(`
		INSERT INTO tender_company_profiles (id, user_id, role, company_name, inn, phone, region_id, address, activity_areas, license_number)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, profileID, userID, input.Role, input.CompanyName, input.INN, input.Phone,
		input.RegionID, input.Address, "{}", input.LicenseNumber)
	if err != nil {
		h.log.Error("Failed to create company profile", "error", err)
		response.InternalServerError(c, "Failed to create company profile")
		return
	}

	response.Created(c, map[string]interface{}{"id": profileID})
}

// UpdateCompanyProfile updates the current user's company profile
func (h *Handler) UpdateCompanyProfile(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var profileID uuid.UUID
	err := h.db.QueryRow(`SELECT id FROM tender_company_profiles WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&profileID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Company profile not found")
		return
	}

	var input entity.UpdateCompanyProfileInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	query := "UPDATE tender_company_profiles SET updated_at = NOW()"
	args := []interface{}{}
	argIdx := 1

	if input.CompanyName != "" {
		query += fmt.Sprintf(", company_name = $%d", argIdx)
		args = append(args, input.CompanyName)
		argIdx++
	}
	if input.Phone != "" {
		query += fmt.Sprintf(", phone = $%d", argIdx)
		args = append(args, input.Phone)
		argIdx++
	}
	if input.RegionID != nil {
		query += fmt.Sprintf(", region_id = $%d", argIdx)
		args = append(args, input.RegionID)
		argIdx++
	}
	if input.Address != "" {
		query += fmt.Sprintf(", address = $%d", argIdx)
		args = append(args, input.Address)
		argIdx++
	}
	if input.Description != "" {
		query += fmt.Sprintf(", description = $%d", argIdx)
		args = append(args, input.Description)
		argIdx++
	}
	if input.Website != "" {
		query += fmt.Sprintf(", website = $%d", argIdx)
		args = append(args, input.Website)
		argIdx++
	}
	if input.LicenseNumber != "" {
		query += fmt.Sprintf(", license_number = $%d", argIdx)
		args = append(args, input.LicenseNumber)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, profileID)

	_, err = h.db.Exec(query, args...)
	if err != nil {
		response.InternalServerError(c, "Failed to update profile")
		return
	}

	response.Success(c, map[string]interface{}{"id": profileID, "message": "Profile updated"})
}

// AddSupplierReview adds a review for a supplier (Buyer only, after completed tender)
func (h *Handler) AddSupplierReview(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	supplierID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid supplier ID")
		return
	}

	var input entity.CreateReviewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	// Calculate overall rating
	overallRating := float64(input.QualityRating+input.PriceRating+input.DeliveryRating+input.CommunicationRating) / 4.0

	reviewID := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO tender_reviews (id, tender_id, reviewer_id, supplier_id,
		                            quality_rating, price_rating, delivery_rating, communication_rating,
		                            overall_rating, comment)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, reviewID, input.TenderID, userID, supplierID,
		input.QualityRating, input.PriceRating, input.DeliveryRating, input.CommunicationRating,
		overallRating, input.Comment)
	if err != nil {
		h.log.Error("Failed to create review", "error", err)
		response.InternalServerError(c, "Failed to create review")
		return
	}

	// Update supplier's average rating
	h.db.Exec(`
		UPDATE tender_company_profiles SET
			rating = (SELECT COALESCE(AVG(overall_rating), 0) FROM tender_reviews WHERE supplier_id = $1 AND is_visible = true),
			review_count = (SELECT COUNT(*) FROM tender_reviews WHERE supplier_id = $1 AND is_visible = true),
			updated_at = NOW()
		WHERE user_id = $1
	`, supplierID)

	response.Created(c, map[string]interface{}{"id": reviewID})
}

// GetSupplierReviews lists reviews for a supplier
func (h *Handler) GetSupplierReviews(c *gin.Context) {
	supplierID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid supplier ID")
		return
	}

	page := getIntParam(c, "page", 1)
	limit := getIntParam(c, "page_size", 20)
	pagination := entity.NewPagination(page, limit)

	var total int
	h.db.QueryRow(`SELECT COUNT(*) FROM tender_reviews WHERE supplier_id = $1 AND is_visible = true`, supplierID).Scan(&total)
	pagination.Calculate(total)

	rows, err := h.db.Query(`
		SELECT r.id, r.tender_id, r.reviewer_id, r.quality_rating, r.price_rating,
		       r.delivery_rating, r.communication_rating, r.overall_rating, r.comment, r.created_at,
		       COALESCE(cp.company_name, '') as reviewer_name
		FROM tender_reviews r
		LEFT JOIN tender_company_profiles cp ON cp.user_id = r.reviewer_id AND cp.deleted_at IS NULL
		WHERE r.supplier_id = $1 AND r.is_visible = true
		ORDER BY r.created_at DESC
		LIMIT $2 OFFSET $3
	`, supplierID, pagination.Limit, pagination.Offset())
	if err != nil {
		h.log.Error("Failed to list reviews", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var reviews []entity.ReviewResponse
	for rows.Next() {
		var r entity.ReviewResponse
		var tenderID sql.NullString

		err := rows.Scan(
			&r.ID, &tenderID, &r.ReviewerID, &r.QualityRating, &r.PriceRating,
			&r.DeliveryRating, &r.CommunicationRating, &r.OverallRating, &r.Comment, &r.CreatedAt,
			&r.ReviewerName,
		)
		if err != nil {
			continue
		}
		if tenderID.Valid {
			parsed, _ := uuid.Parse(tenderID.String)
			r.TenderID = &parsed
		}

		reviews = append(reviews, r)
	}

	response.SuccessWithPagination(c, reviews, pagination)
}

// ListTenderRegions returns all regions
func (h *Handler) ListTenderRegions(c *gin.Context) {
	rows, err := h.db.Query(`SELECT id, name, code FROM tender_regions ORDER BY name ASC`)
	if err != nil {
		h.log.Error("Failed to list regions", "error", err)
		response.InternalServerError(c, "")
		return
	}
	defer rows.Close()

	var regions []entity.TenderRegionResponse
	for rows.Next() {
		var r entity.TenderRegionResponse
		if err := rows.Scan(&r.ID, &r.Name, &r.Code); err == nil {
			regions = append(regions, r)
		}
	}

	response.Success(c, regions)
}
