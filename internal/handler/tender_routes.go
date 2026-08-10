package handler

import (
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterTenderRoutes registers all tender platform API routes.
// This should be called from RegisterRoutes in handler.go.
// Add the following call at the end of RegisterRoutes:
//
//	h.RegisterTenderRoutes(r)
func (h *Handler) RegisterTenderRoutes(r *gin.Engine) {
	v1 := r.Group("/api/v1")

	// =============================================
	// TENDER AUTH ROUTES (separate from ERP auth)
	// =============================================
	tenderAuth := v1.Group("/tender/auth")
	{
		tenderAuth.POST("/register", h.TenderRegister)
		tenderAuth.POST("/login", h.TenderLogin)
		tenderAuth.POST("/token/refresh", h.TenderRefreshToken)
	}

	// Tender auth-protected user routes
	tenderMe := v1.Group("/tender/auth")
	tenderMe.Use(middleware.Auth(h.jwtManager))
	{
		tenderMe.GET("/me", h.TenderGetMe)
		tenderMe.PUT("/me", h.TenderUpdateMe)
	}

	// =============================================
	// PUBLIC ROUTES (no auth required)
	// =============================================

	// Regions (public)
	v1.GET("/tender/regions", h.ListTenderRegions)

	// Categories (public)
	v1.GET("/tender/categories", h.ListTenderCategories)

	// Product catalog (public)
	v1.GET("/tender/products", h.ListTenderProducts)
	v1.GET("/tender/products/:id", h.GetTenderProduct)

	// Active tenders (public)
	v1.GET("/tender/tenders", h.ListTenders)
	v1.GET("/tender/tenders/:id", h.GetTender)

	// Supplier profiles (public)
	v1.GET("/tender/suppliers", h.ListSuppliers)
	v1.GET("/tender/suppliers/:id", h.GetSupplierProfile)
	v1.GET("/tender/suppliers/:id/reviews", h.GetSupplierReviews)

	// =============================================
	// PROTECTED ROUTES (auth required)
	// =============================================
	protected := v1.Group("/tender")
	protected.Use(middleware.Auth(h.jwtManager))

	// ----- Company Profile -----
	profile := protected.Group("/profile")
	{
		profile.GET("", h.GetMyCompanyProfile)
		profile.POST("", h.CreateCompanyProfile)
		profile.PUT("", h.UpdateCompanyProfile)
	}

	// ----- Tenders (Buyer) -----
	tenders := protected.Group("/tenders")
	{
		tenders.POST("", h.CreateTender)
		tenders.PUT("/:id", h.UpdateTender)
		tenders.DELETE("/:id", h.DeleteTender)
		tenders.GET("/my", h.GetMyTenders)

		// Bids within a tender
		tenders.GET("/:id/bids", h.GetTenderBids)
		tenders.POST("/:id/bids", h.SubmitBid)
		tenders.PUT("/:id/bids/:bid_id", h.UpdateBid)
		tenders.POST("/:id/bids/:bid_id/accept", h.AcceptBid)
	}

	// ----- Bids (Supplier) -----
	protected.GET("/bids/my", h.GetMyBids)

	// ----- Products (Supplier) -----
	products := protected.Group("/products")
	{
		products.POST("", h.CreateTenderProduct)
		products.PUT("/:id", h.UpdateTenderProduct)
		products.DELETE("/:id", h.DeleteTenderProduct)
		products.GET("/my", h.GetMyTenderProducts)
	}

	// ----- Reviews -----
	protected.POST("/suppliers/:id/reviews", h.AddSupplierReview)

	// ----- Notifications -----
	notifications := protected.Group("/notifications")
	{
		notifications.GET("", h.ListTenderNotifications)
		notifications.PUT("/:id/read", h.MarkTenderNotificationRead)
		notifications.POST("/read-all", h.MarkAllTenderNotificationsRead)
	}

	// =============================================
	// ADMIN ROUTES
	// =============================================
	// SEC-01 (docs/admin-panel/audit.md): this group administers the WHOLE
	// tender platform (verify companies, change roles, dump every company's
	// INN/phone, CRUD the catalog). It previously ran behind middleware.Auth
	// only — the role check below was commented out — so any authenticated
	// JWT, including a lowest-privilege ERP tenant user (both planes share one
	// signing key + Auth middleware), could administer it. Gate it on the
	// platform super-admin flag (is_system_admin). A finer-grained tender-admin
	// role can be layered on later without reopening the hole.
	admin := protected.Group("/admin")
	admin.Use(middleware.RequireSystemAdmin())
	{
		admin.GET("/stats", h.TenderAdminDashboard)
		admin.GET("/users", h.TenderAdminListUsers)
		admin.PUT("/users/:id", h.TenderAdminUpdateUser)
		admin.POST("/companies/:id/verify", h.TenderAdminVerifyCompany)
		admin.GET("/tenders", h.TenderAdminListTenders)
		admin.GET("/reports", h.TenderAdminReports)

		// Category management
		adminCats := admin.Group("/categories")
		{
			adminCats.GET("", h.ListTenderCategories)
			adminCats.POST("", h.TenderAdminCreateCategory)
			adminCats.PUT("/:id", h.TenderAdminUpdateCategory)
			adminCats.DELETE("/:id", h.TenderAdminDeleteCategory)
		}
	}
}
