package handler

import (
	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/infrastructure/cache"
	"github.com/genixerp/genix-backend/internal/infrastructure/database"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// Handler holds all handler dependencies
type Handler struct {
	db         *database.DB
	redis      *cache.RedisClient
	config     *config.Config
	log        logger.Logger
	jwtManager *crypto.JWTManager
}

// NewHandler creates a new handler instance
func NewHandler(db *database.DB, redis *cache.RedisClient, cfg *config.Config, log logger.Logger) *Handler {
	return &Handler{
		db:         db,
		redis:      redis,
		config:     cfg,
		log:        log,
		jwtManager: crypto.NewJWTManager(cfg.JWT),
	}
}

// RegisterRoutes registers all API routes
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// API v1 routes
	v1 := r.Group("/api/v1")
	{
		// Public routes (no authentication required)
		h.registerPublicRoutes(v1)

		// Protected routes (authentication required)
		protected := v1.Group("")
		protected.Use(middleware.Auth(h.jwtManager))
		protected.Use(middleware.TenantResolver())
		h.registerProtectedRoutes(protected)
	}
}

// registerPublicRoutes registers routes that don't require authentication
func (h *Handler) registerPublicRoutes(rg *gin.RouterGroup) {
	// Authentication
	auth := rg.Group("/auth")
	{
		auth.POST("/register", h.Register)
		auth.POST("/login", h.Login)
		auth.POST("/refresh", h.RefreshToken)
		auth.POST("/forgot-password", h.ForgotPassword)
		auth.POST("/reset-password", h.ResetPassword)
		auth.POST("/verify-email", h.VerifyEmail)
	}

	// Public info
	rg.GET("/info", h.GetAPIInfo)
}

// registerProtectedRoutes registers routes that require authentication
func (h *Handler) registerProtectedRoutes(rg *gin.RouterGroup) {
	// Authentication (protected)
	auth := rg.Group("/auth")
	{
		auth.POST("/logout", h.Logout)
		auth.GET("/me", h.GetCurrentUser)
		auth.PUT("/me", h.UpdateCurrentUser)
		auth.PUT("/me/password", h.ChangePassword)
	}

	// Users
	users := rg.Group("/users")
	users.Use(middleware.RequirePermission("users", "user", "read"))
	{
		users.GET("", h.ListUsers)
		users.POST("", middleware.RequirePermission("users", "user", "create"), h.CreateUser)
		users.GET("/:id", h.GetUser)
		users.PUT("/:id", middleware.RequirePermission("users", "user", "update"), h.UpdateUser)
		users.DELETE("/:id", middleware.RequirePermission("users", "user", "delete"), h.DeleteUser)
		users.POST("/:id/roles", middleware.RequirePermission("users", "role", "assign"), h.AssignRoles)
	}

	// Roles
	roles := rg.Group("/roles")
	roles.Use(middleware.RequirePermission("users", "role", "read"))
	{
		roles.GET("", h.ListRoles)
		roles.POST("", middleware.RequirePermission("users", "role", "create"), h.CreateRole)
		roles.GET("/:id", h.GetRole)
		roles.PUT("/:id", middleware.RequirePermission("users", "role", "update"), h.UpdateRole)
		roles.DELETE("/:id", middleware.RequirePermission("users", "role", "delete"), h.DeleteRole)
	}

	// Permissions
	rg.GET("/permissions", h.ListPermissions)

	// Organizations
	orgs := rg.Group("/organizations")
	orgs.Use(middleware.RequirePermission("organization", "organization", "read"))
	{
		orgs.GET("", h.ListOrganizations)
		orgs.POST("", middleware.RequirePermission("organization", "organization", "create"), h.CreateOrganization)
		orgs.GET("/:id", h.GetOrganization)
		orgs.PUT("/:id", middleware.RequirePermission("organization", "organization", "update"), h.UpdateOrganization)
		orgs.DELETE("/:id", middleware.RequirePermission("organization", "organization", "delete"), h.DeleteOrganization)
	}

	// Departments
	depts := rg.Group("/departments")
	depts.Use(middleware.RequirePermission("organization", "department", "read"))
	{
		depts.GET("", h.ListDepartments)
		depts.POST("", middleware.RequirePermission("organization", "department", "create"), h.CreateDepartment)
		depts.GET("/:id", h.GetDepartment)
		depts.PUT("/:id", middleware.RequirePermission("organization", "department", "update"), h.UpdateDepartment)
		depts.DELETE("/:id", middleware.RequirePermission("organization", "department", "delete"), h.DeleteDepartment)
	}

	// Contacts (Customers & Vendors)
	contacts := rg.Group("/contacts")
	{
		contacts.GET("", h.ListContacts)
		contacts.POST("", h.CreateContact)
		contacts.GET("/:id", h.GetContact)
		contacts.PUT("/:id", h.UpdateContact)
		contacts.DELETE("/:id", h.DeleteContact)
		contacts.GET("/:id/persons", h.ListContactPersons)
		contacts.POST("/:id/persons", h.CreateContactPerson)
	}

	// Call Logs (CRM PBX Integration)
	callLogs := rg.Group("/call-logs")
	{
		callLogs.GET("", h.ListCallLogs)
		callLogs.POST("", h.CreateCallLog)
		callLogs.GET("/stats", h.GetCallLogStats)
		callLogs.GET("/:id", h.GetCallLog)
		callLogs.PUT("/:id", h.UpdateCallLog)
		callLogs.DELETE("/:id", h.DeleteCallLog)
	}

	// Leads (CRM Lead Management)
	leads := rg.Group("/leads")
	{
		leads.GET("", h.ListLeads)
		leads.POST("", h.CreateLead)
		leads.GET("/stats", h.GetLeadStats)
		leads.GET("/:id", h.GetLead)
		leads.PUT("/:id", h.UpdateLead)
		leads.DELETE("/:id", h.DeleteLead)
	}

	// Products
	products := rg.Group("/products")
	products.Use(middleware.RequirePermission("inventory", "product", "read"))
	{
		products.GET("", h.ListProducts)
		products.POST("", middleware.RequirePermission("inventory", "product", "create"), h.CreateProduct)
		products.GET("/:id", h.GetProduct)
		products.PUT("/:id", middleware.RequirePermission("inventory", "product", "update"), h.UpdateProduct)
		products.DELETE("/:id", middleware.RequirePermission("inventory", "product", "delete"), h.DeleteProduct)
	}

	// Product Categories
	categories := rg.Group("/product-categories")
	{
		categories.GET("", h.ListProductCategories)
		categories.POST("", h.CreateProductCategory)
		categories.GET("/:id", h.GetProductCategory)
		categories.PUT("/:id", h.UpdateProductCategory)
		categories.DELETE("/:id", h.DeleteProductCategory)
	}

	// Warehouses
	warehouses := rg.Group("/warehouses")
	warehouses.Use(middleware.RequirePermission("inventory", "warehouse", "manage"))
	{
		warehouses.GET("", h.ListWarehouses)
		warehouses.POST("", h.CreateWarehouse)
		warehouses.GET("/:id", h.GetWarehouse)
		warehouses.PUT("/:id", h.UpdateWarehouse)
		warehouses.DELETE("/:id", h.DeleteWarehouse)
		warehouses.GET("/:id/locations", h.ListWarehouseLocations)
		warehouses.POST("/:id/locations", h.CreateWarehouseLocation)
	}

	// Inventory
	inventory := rg.Group("/inventory")
	inventory.Use(middleware.RequirePermission("inventory", "stock", "read"))
	{
		inventory.GET("", h.ListInventory)
		inventory.GET("/summary", h.GetInventorySummary)
		inventory.POST("/adjust", middleware.RequirePermission("inventory", "stock", "adjust"), h.AdjustInventory)
		inventory.POST("/transfer", middleware.RequirePermission("inventory", "stock", "transfer"), h.TransferInventory)
		inventory.GET("/movements", h.ListInventoryMovements)
		inventory.GET("/valuation", h.GetInventoryValuation)
	}

	// Sales Orders
	salesOrders := rg.Group("/sales-orders")
	salesOrders.Use(middleware.RequirePermission("sales", "order", "read"))
	{
		salesOrders.GET("", h.ListSalesOrders)
		salesOrders.POST("", middleware.RequirePermission("sales", "order", "create"), h.CreateSalesOrder)
		salesOrders.GET("/:id", h.GetSalesOrder)
		salesOrders.PUT("/:id", middleware.RequirePermission("sales", "order", "update"), h.UpdateSalesOrder)
		salesOrders.DELETE("/:id", middleware.RequirePermission("sales", "order", "delete"), h.DeleteSalesOrder)
		salesOrders.POST("/:id/confirm", middleware.RequirePermission("sales", "order", "approve"), h.ConfirmSalesOrder)
		salesOrders.POST("/:id/cancel", h.CancelSalesOrder)
		salesOrders.POST("/:id/invoice", h.CreateInvoiceFromOrder)
	}

	// Sales Invoices
	invoices := rg.Group("/sales-invoices")
	invoices.Use(middleware.RequirePermission("sales", "invoice", "read"))
	{
		invoices.GET("", h.ListSalesInvoices)
		invoices.POST("", middleware.RequirePermission("sales", "invoice", "create"), h.CreateSalesInvoice)
		invoices.GET("/:id", h.GetSalesInvoice)
		invoices.PUT("/:id", middleware.RequirePermission("sales", "invoice", "update"), h.UpdateSalesInvoice)
		invoices.DELETE("/:id", middleware.RequirePermission("sales", "invoice", "delete"), h.DeleteSalesInvoice)
		invoices.POST("/:id/send", h.SendInvoice)
		invoices.POST("/:id/record-payment", h.RecordPayment)
	}

	// Purchase Orders
	purchaseOrders := rg.Group("/purchase-orders")
	purchaseOrders.Use(middleware.RequirePermission("purchase", "order", "read"))
	{
		purchaseOrders.GET("", h.ListPurchaseOrders)
		purchaseOrders.POST("", middleware.RequirePermission("purchase", "order", "create"), h.CreatePurchaseOrder)
		purchaseOrders.GET("/:id", h.GetPurchaseOrder)
		purchaseOrders.PUT("/:id", middleware.RequirePermission("purchase", "order", "update"), h.UpdatePurchaseOrder)
		purchaseOrders.DELETE("/:id", middleware.RequirePermission("purchase", "order", "delete"), h.DeletePurchaseOrder)
		purchaseOrders.POST("/:id/approve", middleware.RequirePermission("purchase", "order", "approve"), h.ApprovePurchaseOrder)
		purchaseOrders.POST("/:id/receive", h.ReceivePurchaseOrder)
	}

	// Chart of Accounts
	accounts := rg.Group("/accounts")
	accounts.Use(middleware.RequirePermission("finance", "account", "read"))
	{
		accounts.GET("", h.ListAccounts)
		accounts.POST("", middleware.RequirePermission("finance", "account", "create"), h.CreateAccount)
		accounts.GET("/:id", h.GetAccount)
		accounts.PUT("/:id", middleware.RequirePermission("finance", "account", "update"), h.UpdateAccount)
		accounts.DELETE("/:id", middleware.RequirePermission("finance", "account", "delete"), h.DeleteAccount)
		accounts.GET("/:id/transactions", h.GetAccountTransactions)
	}

	// Journal Entries
	journals := rg.Group("/journal-entries")
	journals.Use(middleware.RequirePermission("finance", "journal", "read"))
	{
		journals.GET("", h.ListJournalEntries)
		journals.POST("", middleware.RequirePermission("finance", "journal", "create"), h.CreateJournalEntry)
		journals.GET("/:id", h.GetJournalEntry)
		journals.POST("/:id/post", middleware.RequirePermission("finance", "journal", "post"), h.PostJournalEntry)
		journals.POST("/:id/reverse", middleware.RequirePermission("finance", "journal", "reverse"), h.ReverseJournalEntry)
	}

	// Payments
	payments := rg.Group("/payments")
	payments.Use(middleware.RequirePermission("finance", "payment", "read"))
	{
		payments.GET("", h.ListPayments)
		payments.POST("", middleware.RequirePermission("finance", "payment", "create"), h.CreatePayment)
		payments.GET("/:id", h.GetPayment)
		payments.POST("/:id/confirm", middleware.RequirePermission("finance", "payment", "approve"), h.ConfirmPayment)
	}

	// Tax Rates
	taxes := rg.Group("/tax-rates")
	{
		taxes.GET("", h.ListTaxRates)
		taxes.POST("", h.CreateTaxRate)
		taxes.GET("/:id", h.GetTaxRate)
		taxes.PUT("/:id", h.UpdateTaxRate)
		taxes.DELETE("/:id", h.DeleteTaxRate)
	}

	// Currencies
	currencies := rg.Group("/currencies")
	{
		currencies.GET("", h.ListCurrencies)
		currencies.GET("/:code/rate", h.GetExchangeRate)
	}

	// HR - Employees
	employees := rg.Group("/employees")
	employees.Use(middleware.RequirePermission("hr", "employee", "read"))
	{
		employees.GET("", h.ListEmployees)
		employees.POST("", middleware.RequirePermission("hr", "employee", "create"), h.CreateEmployee)
		employees.GET("/:id", h.GetEmployee)
		employees.PUT("/:id", middleware.RequirePermission("hr", "employee", "update"), h.UpdateEmployee)
		employees.DELETE("/:id", middleware.RequirePermission("hr", "employee", "delete"), h.DeleteEmployee)
	}

	// AI Assistant
	ai := rg.Group("/ai")
	ai.Use(middleware.RequirePermission("ai", "conversation", "create"))
	{
		ai.GET("/capabilities", h.GetAICapabilities)
		ai.POST("/chat", h.AIChat)
		ai.GET("/conversations", h.ListAIConversations)
		ai.POST("/conversations", h.CreateAIConversation)
		ai.GET("/conversations/:id", h.GetAIConversation)
		ai.DELETE("/conversations/:id", h.DeleteAIConversation)
		ai.POST("/conversations/:id/messages", h.AddAIMessage)

		// Prompts
		ai.GET("/prompts", h.ListAIPrompts)
		ai.POST("/prompts", h.CreateAIPrompt)
		ai.GET("/prompts/:id", h.GetAIPrompt)
		ai.PUT("/prompts/:id", h.UpdateAIPrompt)
		ai.DELETE("/prompts/:id", h.DeleteAIPrompt)
	}

	// Reports
	reports := rg.Group("/reports")
	reports.Use(middleware.RequirePermission("finance", "report", "read"))
	{
		reports.GET("/balance-sheet", h.GetBalanceSheet)
		reports.GET("/income-statement", h.GetIncomeStatement)
		reports.GET("/cash-flow", h.GetCashFlow)
		reports.GET("/trial-balance", h.GetTrialBalance)
		reports.GET("/general-ledger", h.GetGeneralLedger)
		reports.GET("/aging-receivables", h.GetAgingReceivables)
		reports.GET("/aging-payables", h.GetAgingPayables)
		reports.GET("/sales-summary", h.GetSalesSummary)
		reports.GET("/inventory-summary", h.GetInventoryReport)
	}

	// Settings
	settings := rg.Group("/settings")
	settings.Use(middleware.RequirePermission("settings", "tenant", "read"))
	{
		settings.GET("", h.GetSettings)
		settings.PUT("", middleware.RequirePermission("settings", "tenant", "update"), h.UpdateSettings)
	}

	// Audit Logs
	auditLogs := rg.Group("/audit-logs")
	auditLogs.Use(middleware.RequirePermission("audit", "log", "read"))
	{
		auditLogs.GET("", h.ListAuditLogs)
		auditLogs.GET("/export", middleware.RequirePermission("audit", "log", "export"), h.ExportAuditLogs)
	}

	// Notifications
	notifications := rg.Group("/notifications")
	{
		notifications.GET("", h.ListNotifications)
		notifications.PUT("/:id/read", h.MarkNotificationRead)
		notifications.PUT("/read-all", h.MarkAllNotificationsRead)
	}

	// Attachments/Files
	files := rg.Group("/files")
	{
		files.POST("/upload", h.UploadFile)
		files.GET("/:id", h.GetFile)
		files.DELETE("/:id", h.DeleteFile)
	}
}

// GetAPIInfo returns API information
func (h *Handler) GetAPIInfo(c *gin.Context) {
	c.JSON(200, gin.H{
		"name":        "GenixERP API",
		"version":     "2.0.0",
		"description": "Generative AI-based Enterprise Resource Planning System",
		"docs":        "/api/v1/docs",
	})
}
