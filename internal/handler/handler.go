package handler

import (
	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/infrastructure/cache"
	"github.com/genixerp/genix-backend/internal/infrastructure/database"
	"github.com/genixerp/genix-backend/internal/infrastructure/email"
	"github.com/genixerp/genix-backend/internal/infrastructure/sms"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// Handler holds all handler dependencies
type Handler struct {
	db           *database.DB
	redis        *cache.RedisClient
	config       *config.Config
	log          logger.Logger
	jwtManager   *crypto.JWTManager
	emailService *email.Service
	smsService   *sms.Service
}

// NewHandler creates a new handler instance
func NewHandler(db *database.DB, redis *cache.RedisClient, cfg *config.Config, log logger.Logger) *Handler {
	return &Handler{
		db:           db,
		redis:        redis,
		config:       cfg,
		log:          log,
		jwtManager:   crypto.NewJWTManager(cfg.JWT),
		emailService: email.NewService(&cfg.Email),
		smsService:   sms.NewService(&cfg.SMS),
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
		protected.Use(middleware.OrganizationResolver())
		h.registerProtectedRoutes(protected)
	}
}

// registerPublicRoutes registers routes that don't require authentication
func (h *Handler) registerPublicRoutes(rg *gin.RouterGroup) {
	// Authentication
	auth := rg.Group("/auth")
	{
		auth.POST("/register", h.Register)
		auth.POST("/register-with-otp", h.RegisterWithOTP) // Registration with OTP verification
		auth.POST("/send-otp", h.SendOTP)                  // Send OTP to email
		auth.POST("/verify-otp", h.VerifyOTP)              // Verify OTP code
		auth.POST("/login", h.Login)
		auth.POST("/google", h.GoogleAuth) // Google Sign-In/Sign-Up
		auth.POST("/refresh", h.RefreshToken)
		auth.POST("/forgot-password", h.ForgotPassword)
		auth.POST("/reset-password", h.ResetPassword)
		auth.POST("/verify-email", h.VerifyEmail)
		auth.GET("/validate-invite", h.ValidateInvite)  // Public - validate invite token
		auth.POST("/accept-invite", h.AcceptInvite)     // Public - accept invite and set password
	}

	// Public info
	rg.GET("/info", h.GetAPIInfo)

	// Contact form (public - no auth needed)
	rg.POST("/contact", h.SubmitContactForm)

	// Public file access (for serving images in <img> tags)
	rg.GET("/files/:id", h.GetFile)
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
		auth.POST("/send-invite", h.SendInvite) // Send invitation to a user
		auth.GET("/me/permissions", h.GetCurrentUserPermissions) // Get current user's module permissions
	}

	// Users
	users := rg.Group("/users")
	users.Use(middleware.RequirePermission("users", "user", "read"))
	{
		users.GET("", h.ListUsers)
		users.POST("", middleware.RequirePermission("users", "user", "create"), h.CreateUser)
		users.POST("/send-credentials", middleware.RequirePermission("users", "user", "create"), h.SendCredentials)
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
		roles.GET("/:id/employees", h.GetRoleEmployees)
		roles.POST("/:id/assign", middleware.RequirePermission("users", "role", "update"), h.AssignRole)
		roles.POST("/:id/unassign", middleware.RequirePermission("users", "role", "update"), h.UnassignRole)
	}

	// Permissions
	rg.GET("/permissions", h.ListPermissions)

	// Organizations
	orgs := rg.Group("/organizations")
	orgs.Use(middleware.RequirePermission("organization", "organization", "read"))
	{
		orgs.GET("", h.ListOrganizations)
		orgs.POST("", middleware.RequirePermission("organization", "organization", "create"), h.CreateOrganization)
		orgs.POST("/import", middleware.RequirePermission("organization", "organization", "create"), h.ImportOrganizations)
		orgs.GET("/:id", h.GetOrganization)
		orgs.PUT("/:id", middleware.RequirePermission("organization", "organization", "update"), h.UpdateOrganization)
		orgs.DELETE("/:id", middleware.RequirePermission("organization", "organization", "delete"), h.DeleteOrganization)
		// Initialize default accounts for organization
		orgs.POST("/:id/initialize-accounts", middleware.RequirePermission("organization", "organization", "update"), h.InitializeOrganizationAccounts)
		// Organization employees
		orgs.GET("/:id/employees", h.ListOrganizationEmployees)
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

	// Job Positions
	jobPositions := rg.Group("/job-positions")
	jobPositions.Use(middleware.RequirePermission("organization", "department", "read"))
	{
		jobPositions.GET("", h.ListJobPositions)
		jobPositions.POST("", middleware.RequirePermission("organization", "department", "create"), h.CreateJobPosition)
		jobPositions.GET("/:id", h.GetJobPosition)
		jobPositions.PUT("/:id", middleware.RequirePermission("organization", "department", "update"), h.UpdateJobPosition)
		jobPositions.DELETE("/:id", middleware.RequirePermission("organization", "department", "delete"), h.DeleteJobPosition)
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
		contacts.POST("/:id/rate", h.RateSupplier)
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
		leads.POST("/:id/convert", h.ConvertLead)
	}

	// Opportunities (CRM Sales Pipeline)
	opportunities := rg.Group("/opportunities")
	{
		opportunities.GET("", h.ListOpportunities)
		opportunities.POST("", h.CreateOpportunity)
		opportunities.GET("/stats", h.GetOpportunityStats)
		opportunities.GET("/by-stage", h.GetOpportunitiesByStage)
		opportunities.GET("/:id", h.GetOpportunity)
		opportunities.PUT("/:id", h.UpdateOpportunity)
		opportunities.DELETE("/:id", h.DeleteOpportunity)
	}

	// Pipeline Stages
	pipelineStages := rg.Group("/pipeline-stages")
	{
		pipelineStages.GET("", h.ListPipelineStages)
		pipelineStages.POST("", h.CreatePipelineStage)
		pipelineStages.PUT("/:id", h.UpdatePipelineStage)
		pipelineStages.DELETE("/:id", h.DeletePipelineStage)
	}

	// Activities (CRM Activities)
	activities := rg.Group("/activities")
	{
		activities.GET("", h.ListActivities)
		activities.POST("", h.CreateActivity)
		activities.GET("/stats", h.GetActivityStats)
		activities.GET("/timeline", h.GetActivityTimeline)
		activities.GET("/:id", h.GetActivity)
		activities.PUT("/:id", h.UpdateActivity)
		activities.DELETE("/:id", h.DeleteActivity)
	}

	// Tasks (CRM Tasks)
	tasks := rg.Group("/tasks")
	{
		tasks.GET("", h.ListTasks)
		tasks.POST("", h.CreateTask)
		tasks.GET("/stats", h.GetTaskStats)
		tasks.GET("/kanban", h.GetTasksKanban)
		tasks.GET("/:id", h.GetTask)
		tasks.PUT("/:id", h.UpdateTask)
		tasks.DELETE("/:id", h.DeleteTask)
		tasks.POST("/:id/complete", h.CompleteTask)
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

	// Units of Measure
	uom := rg.Group("/units-of-measure")
	{
		uom.GET("", h.ListUnitsOfMeasure)
		uom.POST("", h.CreateUnitOfMeasure)
		uom.PUT("/:id", h.UpdateUnitOfMeasure)
		uom.DELETE("/:id", h.DeleteUnitOfMeasure)
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
		warehouses.PUT("/:id/locations/:locationId", h.UpdateWarehouseLocation)
		warehouses.DELETE("/:id/locations/:locationId", h.DeleteWarehouseLocation)
		warehouses.GET("/:id/operation-types", h.GetWarehouseOperationTypes)
	}

	// Warehouse Locations (global - for cross-warehouse view like Odoo)
	locations := rg.Group("/locations")
	locations.Use(middleware.RequirePermission("inventory", "warehouse", "read"))
	{
		locations.GET("", h.ListAllLocations)
	}

	// Warehouse Operation Types
	operationTypes := rg.Group("/operation-types")
	operationTypes.Use(middleware.RequirePermission("inventory", "warehouse", "read"))
	{
		operationTypes.GET("", h.ListOperationTypes)
		operationTypes.GET("/:id", h.GetOperationType)
		operationTypes.POST("", middleware.RequirePermission("inventory", "warehouse", "create"), h.CreateOperationType)
		operationTypes.PUT("/:id", middleware.RequirePermission("inventory", "warehouse", "update"), h.UpdateOperationType)
		operationTypes.DELETE("/:id", middleware.RequirePermission("inventory", "warehouse", "delete"), h.DeleteOperationType)
	}

	// Carriers
	carriers := rg.Group("/carriers")
	carriers.Use(middleware.RequirePermission("inventory", "carrier", "read"))
	{
		carriers.GET("", h.ListCarriers)
		carriers.POST("", middleware.RequirePermission("inventory", "carrier", "create"), h.CreateCarrier)
		carriers.GET("/:id", h.GetCarrier)
		carriers.PUT("/:id", middleware.RequirePermission("inventory", "carrier", "update"), h.UpdateCarrier)
		carriers.DELETE("/:id", middleware.RequirePermission("inventory", "carrier", "delete"), h.DeleteCarrier)
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
		inventory.GET("/cogs", h.GetCOGSData)
	}

	// Stock Counts (Inventarizatsiya)
	stockCounts := rg.Group("/stock-counts")
	stockCounts.Use(middleware.RequirePermission("inventory", "stock", "read"))
	{
		stockCounts.GET("", h.ListStockCounts)
		stockCounts.POST("", middleware.RequirePermission("inventory", "stock", "create"), h.CreateStockCount)
		stockCounts.GET("/:id", h.GetStockCount)
		stockCounts.POST("/:id/record", middleware.RequirePermission("inventory", "stock", "adjust"), h.RecordCountLine)
		stockCounts.POST("/:id/complete", middleware.RequirePermission("inventory", "stock", "adjust"), h.CompleteStockCount)
		stockCounts.DELETE("/:id", middleware.RequirePermission("inventory", "stock", "delete"), h.DeleteStockCount)
	}

	// Stock Operations (TT: Receipt, Delivery, Internal Transfer, Write-off)
	stockOps := rg.Group("/stock-operations")
	stockOps.Use(middleware.RequirePermission("inventory", "stock", "read"))
	{
		stockOps.GET("", h.ListStockOperations)
		stockOps.GET("/summary", h.GetStockOperationSummary)
		stockOps.POST("", middleware.RequirePermission("inventory", "stock", "create"), h.CreateStockOperation)
		stockOps.GET("/:id", h.GetStockOperation)
		stockOps.PUT("/:id", middleware.RequirePermission("inventory", "stock", "update"), h.UpdateStockOperation)
		stockOps.PUT("/:id/lines", middleware.RequirePermission("inventory", "stock", "update"), h.UpdateStockOperationLines)
		stockOps.POST("/:id/lines", middleware.RequirePermission("inventory", "stock", "update"), h.AddStockOperationLine)
		stockOps.DELETE("/:id/lines/:lineId", middleware.RequirePermission("inventory", "stock", "update"), h.DeleteStockOperationLine)
		stockOps.POST("/:id/validate", middleware.RequirePermission("inventory", "stock", "update"), h.ValidateStockOperation)
		stockOps.POST("/:id/advance", middleware.RequirePermission("inventory", "stock", "update"), h.AdvanceStockOperationStep)
		stockOps.POST("/:id/backorder", middleware.RequirePermission("inventory", "stock", "create"), h.CreateBackorder)
		stockOps.POST("/:id/cancel", middleware.RequirePermission("inventory", "stock", "update"), h.CancelStockOperation)
	}

	// Operation Type Steps (per operation type configuration)
	operationTypes.GET("/:id/steps", h.ListOperationTypeSteps)
	operationTypes.PUT("/:id/steps", middleware.RequirePermission("inventory", "warehouse", "update"), h.SaveOperationTypeSteps)

	// Product Attributes
	productAttributes := rg.Group("/product-attributes")
	productAttributes.Use(middleware.RequirePermission("inventory", "product_attribute", "read"))
	{
		productAttributes.GET("", h.ListProductAttributes)
		productAttributes.POST("", middleware.RequirePermission("inventory", "product_attribute", "create"), h.CreateProductAttribute)
		productAttributes.PUT("/:id", middleware.RequirePermission("inventory", "product_attribute", "update"), h.UpdateProductAttribute)
		productAttributes.DELETE("/:id", middleware.RequirePermission("inventory", "product_attribute", "delete"), h.DeleteProductAttribute)
		productAttributes.POST("/:id/values", middleware.RequirePermission("inventory", "product_attribute", "update"), h.CreateAttributeValue)
		productAttributes.DELETE("/:id/values/:valueId", middleware.RequirePermission("inventory", "product_attribute", "delete"), h.DeleteAttributeValue)
	}

	// Product Variants
	productVariants := rg.Group("/product-variants")
	productVariants.Use(middleware.RequirePermission("inventory", "product_variant", "read"))
	{
		productVariants.GET("", h.ListProductVariants)
		productVariants.POST("", middleware.RequirePermission("inventory", "product_variant", "create"), h.CreateProductVariant)
		productVariants.GET("/:id", h.GetProductVariant)
		productVariants.PUT("/:id", middleware.RequirePermission("inventory", "product_variant", "update"), h.UpdateProductVariant)
		productVariants.DELETE("/:id", middleware.RequirePermission("inventory", "product_variant", "delete"), h.DeleteProductVariant)
		productVariants.POST("/generate", middleware.RequirePermission("inventory", "product_variant", "create"), h.GenerateProductVariants)
	}

	// Product Template Attributes (link attributes to products)
	rg.POST("/products/:id/attributes", middleware.RequirePermission("inventory", "product", "update"), h.AddProductAttribute)
	rg.GET("/products/:id/attributes", middleware.RequirePermission("inventory", "product", "read"), h.GetProductTemplateAttributes)

	// Product Packagings (Odoo-style - how products are sold/purchased in bulk)
	productPackagings := rg.Group("/product-packagings")
	productPackagings.Use(middleware.RequirePermission("inventory", "product", "read"))
	{
		productPackagings.GET("", h.ListProductPackagings)
		productPackagings.POST("", middleware.RequirePermission("inventory", "product", "create"), h.CreateProductPackaging)
		productPackagings.GET("/:id", h.GetProductPackaging)
		productPackagings.PUT("/:id", middleware.RequirePermission("inventory", "product", "update"), h.UpdateProductPackaging)
		productPackagings.DELETE("/:id", middleware.RequirePermission("inventory", "product", "delete"), h.DeleteProductPackaging)
	}

	// Product Packagings by Product
	rg.GET("/products/:id/packagings", middleware.RequirePermission("inventory", "product", "read"), h.ListProductPackagingsByProduct)
	rg.POST("/products/:id/packagings", middleware.RequirePermission("inventory", "product", "update"), h.CreateProductPackagingForProduct)

	// Package Types (box sizes, pallets, containers)
	packageTypes := rg.Group("/package-types")
	packageTypes.Use(middleware.RequirePermission("inventory", "warehouse", "read"))
	{
		packageTypes.GET("", h.ListPackageTypes)
		packageTypes.POST("", middleware.RequirePermission("inventory", "warehouse", "create"), h.CreatePackageType)
		packageTypes.GET("/:id", h.GetPackageType)
		packageTypes.PUT("/:id", middleware.RequirePermission("inventory", "warehouse", "update"), h.UpdatePackageType)
		packageTypes.DELETE("/:id", middleware.RequirePermission("inventory", "warehouse", "delete"), h.DeletePackageType)
	}

	// Packages (physical packages in warehouse)
	packages := rg.Group("/packages")
	packages.Use(middleware.RequirePermission("inventory", "warehouse", "read"))
	{
		packages.GET("", h.ListPackages)
		packages.POST("", middleware.RequirePermission("inventory", "warehouse", "create"), h.CreatePackage)
		packages.GET("/:id", h.GetPackage)
		packages.PUT("/:id", middleware.RequirePermission("inventory", "warehouse", "update"), h.UpdatePackage)
		packages.DELETE("/:id", middleware.RequirePermission("inventory", "warehouse", "delete"), h.DeletePackage)
		packages.POST("/:id/contents", middleware.RequirePermission("inventory", "warehouse", "update"), h.AddPackageContent)
		packages.DELETE("/:id/contents/:contentId", middleware.RequirePermission("inventory", "warehouse", "update"), h.RemovePackageContent)
	}

	// Bill of Materials (BOM)
	bom := rg.Group("/bom")
	bom.Use(middleware.RequirePermission("inventory", "bom", "read"))
	{
		bom.GET("", h.ListBOMs)
		bom.POST("", middleware.RequirePermission("inventory", "bom", "create"), h.CreateBOM)
		bom.GET("/:id", h.GetBOM)
		bom.PUT("/:id", middleware.RequirePermission("inventory", "bom", "update"), h.UpdateBOM)
		bom.DELETE("/:id", middleware.RequirePermission("inventory", "bom", "delete"), h.DeleteBOM)
		bom.POST("/:id/lines", middleware.RequirePermission("inventory", "bom", "update"), h.CreateBOMLine)
		bom.DELETE("/:id/lines/:lineId", middleware.RequirePermission("inventory", "bom", "update"), h.DeleteBOMLine)
		// BOM Operations (routing)
		bom.GET("/:id/operations", h.ListBOMOperations)
		bom.POST("/:id/operations", middleware.RequirePermission("inventory", "bom", "update"), h.CreateBOMOperation)
		bom.PUT("/:id/operations/:operationId", middleware.RequirePermission("inventory", "bom", "update"), h.UpdateBOMOperation)
		bom.DELETE("/:id/operations/:operationId", middleware.RequirePermission("inventory", "bom", "update"), h.DeleteBOMOperation)
	}

	// Alias route for /boms (plural) - frontend compatibility
	boms := rg.Group("/boms")
	boms.Use(middleware.RequirePermission("inventory", "bom", "read"))
	{
		boms.GET("", h.ListBOMs)
		boms.POST("", middleware.RequirePermission("inventory", "bom", "create"), h.CreateBOM)
		boms.GET("/:id", h.GetBOM)
		boms.PUT("/:id", middleware.RequirePermission("inventory", "bom", "update"), h.UpdateBOM)
		boms.DELETE("/:id", middleware.RequirePermission("inventory", "bom", "delete"), h.DeleteBOM)
		// BOM Lines (components)
		boms.POST("/:id/lines", middleware.RequirePermission("inventory", "bom", "update"), h.CreateBOMLine)
		boms.DELETE("/:id/lines/:lineId", middleware.RequirePermission("inventory", "bom", "update"), h.DeleteBOMLine)
		// BOM Operations (routing)
		boms.GET("/:id/operations", h.ListBOMOperations)
		boms.POST("/:id/operations", middleware.RequirePermission("inventory", "bom", "update"), h.CreateBOMOperation)
		boms.PUT("/:id/operations/:operationId", middleware.RequirePermission("inventory", "bom", "update"), h.UpdateBOMOperation)
		boms.DELETE("/:id/operations/:operationId", middleware.RequirePermission("inventory", "bom", "update"), h.DeleteBOMOperation)
	}

	// Scrap Management
	scrap := rg.Group("/scrap")
	scrap.Use(middleware.RequirePermission("inventory", "scrap", "read"))
	{
		scrap.GET("/reasons", h.ListScrapReasons)
		scrap.POST("/reasons", middleware.RequirePermission("inventory", "scrap", "create"), h.CreateScrapReason)
		scrap.GET("/orders", h.ListScrapOrders)
		scrap.POST("/orders", middleware.RequirePermission("inventory", "scrap", "create"), h.CreateScrapOrder)
		scrap.GET("/orders/:id", h.GetScrapOrder)
		scrap.POST("/orders/:id/confirm", middleware.RequirePermission("inventory", "scrap", "approve"), h.ConfirmScrapOrder)
		scrap.POST("/orders/:id/cancel", h.CancelScrapOrder)
		scrap.GET("/summary", h.GetScrapSummary)
	}

	// Reorder Rules
	reorder := rg.Group("/reorder-rules")
	reorder.Use(middleware.RequirePermission("inventory", "reorder", "read"))
	{
		reorder.GET("", h.ListReorderRules)
		reorder.POST("", middleware.RequirePermission("inventory", "reorder", "create"), h.CreateReorderRule)
		reorder.GET("/alerts", h.GetReorderAlerts)
		reorder.GET("/:id", h.GetReorderRule)
		reorder.PUT("/:id", middleware.RequirePermission("inventory", "reorder", "update"), h.UpdateReorderRule)
		reorder.DELETE("/:id", middleware.RequirePermission("inventory", "reorder", "delete"), h.DeleteReorderRule)
	}

	// Replenishment
	replenishment := rg.Group("/replenishment")
	replenishment.Use(middleware.RequirePermission("inventory", "reorder", "read"))
	{
		replenishment.GET("/preview", h.GetReplenishmentPreview)
		replenishment.POST("/run", middleware.RequirePermission("purchase", "order", "create"), h.RunReplenishment)
	}

	// Quotations
	quotations := rg.Group("/quotations")
	quotations.Use(middleware.RequirePermission("sales", "quotation", "read"))
	{
		quotations.GET("", h.ListQuotations)
		quotations.POST("", middleware.RequirePermission("sales", "quotation", "create"), h.CreateQuotation)
		quotations.GET("/:id", h.GetQuotation)
		quotations.PUT("/:id", middleware.RequirePermission("sales", "quotation", "update"), h.UpdateQuotation)
		quotations.DELETE("/:id", middleware.RequirePermission("sales", "quotation", "delete"), h.DeleteQuotation)
		quotations.POST("/:id/convert", middleware.RequirePermission("sales", "quotation", "update"), h.ConvertQuotationToOrder)
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

	// Sales Delivery Orders (outbound shipments from sales orders)
	deliveryOrders := rg.Group("/sales/delivery-orders")
	deliveryOrders.Use(middleware.RequirePermission("sales", "delivery", "read"))
	{
		deliveryOrders.GET("", h.ListDeliveryOrders)
		deliveryOrders.POST("", middleware.RequirePermission("sales", "delivery", "create"), h.CreateDeliveryOrder)
		deliveryOrders.GET("/:id", h.GetDeliveryOrder)
		deliveryOrders.PUT("/:id", middleware.RequirePermission("sales", "delivery", "update"), h.UpdateDeliveryOrder)
		deliveryOrders.POST("/:id/validate", middleware.RequirePermission("sales", "delivery", "update"), h.ValidateDeliveryOrder)
		deliveryOrders.POST("/:id/cancel", middleware.RequirePermission("sales", "delivery", "update"), h.CancelDeliveryOrder)
	}

	// Sales Invoices
	invoices := rg.Group("/sales-invoices")
	invoices.Use(middleware.RequirePermission("sales", "invoice", "read"))
	{
		invoices.GET("", h.ListSalesInvoices)
		invoices.POST("", middleware.RequirePermission("sales", "invoice", "create"), h.CreateSalesInvoice)
		invoices.POST("/repair-revenue", middleware.RequirePermission("sales", "invoice", "update"), h.RepairRevenueJournalEntries)
		invoices.GET("/:id", h.GetSalesInvoice)
		invoices.PUT("/:id", middleware.RequirePermission("sales", "invoice", "update"), h.UpdateSalesInvoice)
		invoices.DELETE("/:id", middleware.RequirePermission("sales", "invoice", "delete"), h.DeleteSalesInvoice)
		invoices.POST("/:id/send", h.SendInvoice)
		invoices.POST("/:id/record-payment", h.RecordPayment)
		invoices.POST("/:id/credit-note", middleware.RequirePermission("sales", "invoice", "create"), h.CreateCreditNote)
		invoices.POST("/:id/confirm-credit-note", middleware.RequirePermission("sales", "invoice", "update"), h.ConfirmCreditNote)
	}

	// Sales Returns
	salesReturns := rg.Group("/sales-returns")
	salesReturns.Use(middleware.RequirePermission("sales", "return", "read"))
	{
		salesReturns.GET("", h.ListSalesReturns)
		salesReturns.POST("", middleware.RequirePermission("sales", "return", "create"), h.CreateSalesReturn)
		salesReturns.GET("/:id", h.GetSalesReturn)
		salesReturns.PUT("/:id", middleware.RequirePermission("sales", "return", "update"), h.UpdateSalesReturn)
		salesReturns.DELETE("/:id", middleware.RequirePermission("sales", "return", "delete"), h.DeleteSalesReturn)
		salesReturns.POST("/:id/approve", middleware.RequirePermission("sales", "return", "approve"), h.ApproveSalesReturn)
		salesReturns.POST("/:id/reject", middleware.RequirePermission("sales", "return", "approve"), h.RejectSalesReturn)
		salesReturns.POST("/:id/refund", middleware.RequirePermission("sales", "return", "update"), h.ProcessRefund)
	}

	// Payment Terms
	paymentTerms := rg.Group("/payment-terms")
	paymentTerms.Use(middleware.RequirePermission("sales", "payment_term", "read"))
	{
		paymentTerms.GET("", h.ListPaymentTerms)
		paymentTerms.POST("", middleware.RequirePermission("sales", "payment_term", "create"), h.CreatePaymentTerm)
		paymentTerms.GET("/default", h.GetDefaultPaymentTerm)
		paymentTerms.GET("/stats", h.GetPaymentTermStats)
		paymentTerms.POST("/calculate-due-date", h.CalculateDueDate)
		paymentTerms.GET("/:id", h.GetPaymentTerm)
		paymentTerms.PUT("/:id", middleware.RequirePermission("sales", "payment_term", "update"), h.UpdatePaymentTerm)
		paymentTerms.DELETE("/:id", middleware.RequirePermission("sales", "payment_term", "delete"), h.DeletePaymentTerm)
		paymentTerms.POST("/:id/set-default", middleware.RequirePermission("sales", "payment_term", "update"), h.SetDefaultPaymentTerm)
	}

	// Pricelists
	pricelists := rg.Group("/pricelists")
	pricelists.Use(middleware.RequirePermission("sales", "pricelist", "read"))
	{
		pricelists.GET("", h.ListPricelists)
		pricelists.POST("", middleware.RequirePermission("sales", "pricelist", "create"), h.CreatePricelist)
		pricelists.GET("/default", h.GetDefaultPricelist)
		pricelists.POST("/get-price", h.GetProductPrice)
		pricelists.POST("/get-bulk-prices", h.GetBulkProductPrices)
		pricelists.POST("/assign-customer", middleware.RequirePermission("sales", "pricelist", "update"), h.AssignCustomerPricelist)
		pricelists.GET("/:id", h.GetPricelist)
		pricelists.PUT("/:id", middleware.RequirePermission("sales", "pricelist", "update"), h.UpdatePricelist)
		pricelists.DELETE("/:id", middleware.RequirePermission("sales", "pricelist", "delete"), h.DeletePricelist)
		pricelists.POST("/:id/set-default", middleware.RequirePermission("sales", "pricelist", "update"), h.SetDefaultPricelist)
	}

	// Quotation Templates
	quotationTemplates := rg.Group("/quotation-templates")
	quotationTemplates.Use(middleware.RequirePermission("sales", "quotation_template", "read"))
	{
		quotationTemplates.GET("", h.ListQuotationTemplates)
		quotationTemplates.POST("", middleware.RequirePermission("sales", "quotation_template", "create"), h.CreateQuotationTemplate)
		quotationTemplates.GET("/:id", h.GetQuotationTemplate)
		quotationTemplates.PUT("/:id", middleware.RequirePermission("sales", "quotation_template", "update"), h.UpdateQuotationTemplate)
		quotationTemplates.DELETE("/:id", middleware.RequirePermission("sales", "quotation_template", "delete"), h.DeleteQuotationTemplate)
		quotationTemplates.POST("/:id/duplicate", middleware.RequirePermission("sales", "quotation_template", "create"), h.DuplicateQuotationTemplate)
	}

	// Customer Follow-ups (Dunning)
	followups := rg.Group("/followups")
	followups.Use(middleware.RequirePermission("finance", "followup", "read"))
	{
		followups.GET("/summary", h.GetFollowupSummary)
		followups.GET("/customers", h.ListCustomerFollowups)
		followups.GET("/customers/:customer_id", h.GetCustomerFollowupDetails)
		followups.POST("/actions", middleware.RequirePermission("finance", "followup", "create"), h.CreateFollowupAction)
		followups.POST("/send-reminder", middleware.RequirePermission("finance", "followup", "create"), h.SendFollowupReminder)
		followups.POST("/promises", middleware.RequirePermission("finance", "followup", "create"), h.CreatePaymentPromise)
		followups.PUT("/promises/:id/status", middleware.RequirePermission("finance", "followup", "update"), h.UpdatePaymentPromiseStatus)
	}

	// Follow-up Levels
	followupLevels := rg.Group("/followup-levels")
	followupLevels.Use(middleware.RequirePermission("finance", "followup_level", "read"))
	{
		followupLevels.GET("", h.ListFollowupLevels)
		followupLevels.POST("", middleware.RequirePermission("finance", "followup_level", "create"), h.CreateFollowupLevel)
		followupLevels.PUT("/:id", middleware.RequirePermission("finance", "followup_level", "update"), h.UpdateFollowupLevel)
		followupLevels.DELETE("/:id", middleware.RequirePermission("finance", "followup_level", "delete"), h.DeleteFollowupLevel)
	}

	// Tax Reports
	taxReports := rg.Group("/tax-reports")
	taxReports.Use(middleware.RequirePermission("finance", "tax_report", "read"))
	{
		taxReports.GET("/summary", h.GetTaxReportSummary)
		taxReports.GET("/transactions", h.GetTaxTransactions)
		taxReports.GET("/periods", h.ListTaxReportPeriods)
		taxReports.POST("/periods", middleware.RequirePermission("finance", "tax_report", "create"), h.CreateTaxReportPeriod)
		taxReports.GET("/periods/:id", h.GetTaxReportPeriod)
		taxReports.POST("/periods/:id/calculate", middleware.RequirePermission("finance", "tax_report", "update"), h.CalculateTaxReport)
		taxReports.POST("/periods/:id/file", middleware.RequirePermission("finance", "tax_report", "file"), h.FileTaxReport)
		taxReports.DELETE("/periods/:id", middleware.RequirePermission("finance", "tax_report", "delete"), h.DeleteTaxReportPeriod)
	}

	// Discounts
	discounts := rg.Group("/discounts")
	discounts.Use(middleware.RequirePermission("sales", "discount", "read"))
	{
		discounts.GET("", h.ListDiscounts)
		discounts.POST("", middleware.RequirePermission("sales", "discount", "create"), h.CreateDiscount)
		discounts.GET("/:id", h.GetDiscount)
		discounts.PUT("/:id", middleware.RequirePermission("sales", "discount", "update"), h.UpdateDiscount)
		discounts.DELETE("/:id", middleware.RequirePermission("sales", "discount", "delete"), h.DeleteDiscount)
		discounts.POST("/validate", h.ValidateDiscountCode)
		discounts.POST("/:id/use", h.UseDiscountCode)
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
		purchaseOrders.POST("/:id/submit", middleware.RequirePermission("purchase", "order", "update"), h.SubmitPOForApproval)
		purchaseOrders.POST("/:id/approve", middleware.RequirePermission("purchase", "order", "approve"), h.ApprovePurchaseOrder)
		purchaseOrders.POST("/:id/receive", h.ReceivePurchaseOrder)
		purchaseOrders.POST("/:id/bill", middleware.RequirePermission("purchase", "invoice", "create"), h.CreateBillFromPO)
	}

	// Purchase Invoices (Vendor Bills)
	purchaseInvoices := rg.Group("/purchase-invoices")
	purchaseInvoices.Use(middleware.RequirePermission("purchase", "invoice", "read"))
	{
		purchaseInvoices.GET("", h.ListPurchaseInvoices)
		purchaseInvoices.POST("", middleware.RequirePermission("purchase", "invoice", "create"), h.CreatePurchaseInvoice)
		purchaseInvoices.GET("/:id", h.GetPurchaseInvoice)
		purchaseInvoices.PUT("/:id", middleware.RequirePermission("purchase", "invoice", "update"), h.UpdatePurchaseInvoice)
		purchaseInvoices.DELETE("/:id", middleware.RequirePermission("purchase", "invoice", "delete"), h.DeletePurchaseInvoice)
		purchaseInvoices.POST("/:id/confirm", middleware.RequirePermission("purchase", "invoice", "approve"), h.ConfirmPurchaseInvoice)
		purchaseInvoices.POST("/:id/post", middleware.RequirePermission("purchase", "invoice", "approve"), h.PostPurchaseInvoice)
		purchaseInvoices.POST("/:id/pay", h.PayPurchaseInvoice)
		purchaseInvoices.POST("/:id/debit-note", middleware.RequirePermission("purchase", "invoice", "create"), h.CreateDebitNote)
		purchaseInvoices.POST("/:id/confirm-debit-note", middleware.RequirePermission("purchase", "invoice", "approve"), h.ConfirmDebitNote)
	}

	// Request for Quotations (RFQ)
	rfqs := rg.Group("/rfqs")
	rfqs.Use(middleware.RequirePermission("purchase", "rfq", "read"))
	{
		rfqs.GET("", h.ListRFQs)
		rfqs.POST("", middleware.RequirePermission("purchase", "rfq", "create"), h.CreateRFQ)
		rfqs.GET("/:id", h.GetRFQ)
		rfqs.PUT("/:id", middleware.RequirePermission("purchase", "rfq", "update"), h.UpdateRFQ)
		rfqs.DELETE("/:id", middleware.RequirePermission("purchase", "rfq", "delete"), h.DeleteRFQ)
		rfqs.POST("/:id/open", middleware.RequirePermission("purchase", "rfq", "update"), h.OpenRFQ)
		rfqs.POST("/:id/responses", h.SubmitRFQResponse)
		rfqs.POST("/:id/select-winner", middleware.RequirePermission("purchase", "rfq", "update"), h.SelectRFQWinner)
		rfqs.POST("/:id/convert-to-po", middleware.RequirePermission("purchase", "purchase_order", "create"), h.ConvertRFQToPO)
	}

	// Procurement Contracts
	contracts := rg.Group("/contracts")
	contracts.Use(middleware.RequirePermission("purchase", "contract", "read"))
	{
		contracts.GET("", h.ListContracts)
		contracts.POST("", middleware.RequirePermission("purchase", "contract", "create"), h.CreateContract)
		contracts.GET("/:id", h.GetContract)
		contracts.PUT("/:id", middleware.RequirePermission("purchase", "contract", "update"), h.UpdateContract)
		contracts.DELETE("/:id", middleware.RequirePermission("purchase", "contract", "delete"), h.DeleteContract)
		contracts.POST("/:id/activate", middleware.RequirePermission("purchase", "contract", "update"), h.ActivateContract)
		contracts.POST("/:id/terminate", middleware.RequirePermission("purchase", "contract", "update"), h.TerminateContract)
	}

	// Supplier Price History
	priceHistory := rg.Group("/price-history")
	priceHistory.Use(middleware.RequirePermission("purchase", "price_history", "read"))
	{
		priceHistory.GET("", h.ListPriceHistory)
		priceHistory.POST("", middleware.RequirePermission("purchase", "price_history", "create"), h.CreatePriceHistory)
		priceHistory.GET("/:id", h.GetPriceHistory)
		priceHistory.DELETE("/:id", middleware.RequirePermission("purchase", "price_history", "delete"), h.DeletePriceHistory)
	}

	// Vendor Prices (pricelist)
	vendorPrices := rg.Group("/vendor-prices")
	vendorPrices.Use(middleware.RequirePermission("purchase", "price_history", "read"))
	{
		vendorPrices.GET("", h.ListVendorPrices)
		vendorPrices.GET("/lookup", h.LookupVendorPrice)
		vendorPrices.POST("", middleware.RequirePermission("purchase", "price_history", "create"), h.CreateVendorPrice)
		vendorPrices.GET("/:id", h.GetVendorPrice)
		vendorPrices.PUT("/:id", middleware.RequirePermission("purchase", "price_history", "update"), h.UpdateVendorPrice)
		vendorPrices.DELETE("/:id", middleware.RequirePermission("purchase", "price_history", "delete"), h.DeleteVendorPrice)
	}

	// Purchase Requisitions
	requisitions := rg.Group("/purchase-requisitions")
	requisitions.Use(middleware.RequirePermission("purchase", "requisition", "read"))
	{
		requisitions.GET("", h.ListPurchaseRequisitions)
		requisitions.POST("", middleware.RequirePermission("purchase", "requisition", "create"), h.CreatePurchaseRequisition)
		requisitions.GET("/:id", h.GetPurchaseRequisition)
		requisitions.PUT("/:id", middleware.RequirePermission("purchase", "requisition", "update"), h.UpdatePurchaseRequisition)
		requisitions.DELETE("/:id", middleware.RequirePermission("purchase", "requisition", "delete"), h.DeletePurchaseRequisition)
		requisitions.POST("/:id/submit", middleware.RequirePermission("purchase", "requisition", "update"), h.SubmitPurchaseRequisition)
		requisitions.POST("/:id/approve", middleware.RequirePermission("purchase", "requisition", "approve"), h.ApprovePurchaseRequisition)
		requisitions.POST("/:id/reject", middleware.RequirePermission("purchase", "requisition", "approve"), h.RejectPurchaseRequisition)
		requisitions.POST("/:id/convert-to-po", middleware.RequirePermission("purchase", "order", "create"), h.ConvertPRToPO)
	}

	// Goods Receipts
	goodsReceipts := rg.Group("/goods-receipts")
	goodsReceipts.Use(middleware.RequirePermission("purchase", "receipt", "read"))
	{
		goodsReceipts.GET("", h.ListGoodsReceipts)
		goodsReceipts.POST("", middleware.RequirePermission("purchase", "receipt", "create"), h.CreateGoodsReceipt)
		goodsReceipts.GET("/:id", h.GetGoodsReceipt)
		goodsReceipts.DELETE("/:id", middleware.RequirePermission("purchase", "receipt", "delete"), h.DeleteGoodsReceipt)
		goodsReceipts.POST("/:id/inspect", middleware.RequirePermission("purchase", "receipt", "update"), h.InspectGoodsReceipt)
		goodsReceipts.POST("/:id/complete", middleware.RequirePermission("purchase", "receipt", "update"), h.CompleteGoodsReceipt)
		goodsReceipts.POST("/:id/cancel", middleware.RequirePermission("purchase", "receipt", "update"), h.CancelGoodsReceipt)
		goodsReceipts.GET("/:id/landed-cost-data", h.GetGRForLandedCost)
	}

	// Landed Costs
	landedCosts := rg.Group("/landed-costs")
	landedCosts.Use(middleware.RequirePermission("purchase", "receipt", "read"))
	{
		landedCosts.GET("", h.ListLandedCosts)
		landedCosts.POST("", middleware.RequirePermission("purchase", "receipt", "create"), h.CreateLandedCost)
		landedCosts.GET("/:id", h.GetLandedCost)
		landedCosts.POST("/:id/validate", middleware.RequirePermission("purchase", "receipt", "update"), h.ValidateLandedCost)
		landedCosts.POST("/:id/cancel", middleware.RequirePermission("purchase", "receipt", "update"), h.CancelLandedCost)
	}

	// Landed Cost Types
	landedCostTypes := rg.Group("/landed-cost-types")
	landedCostTypes.Use(middleware.RequirePermission("purchase", "receipt", "read"))
	{
		landedCostTypes.GET("", h.ListLandedCostTypes)
		landedCostTypes.POST("", middleware.RequirePermission("purchase", "receipt", "create"), h.CreateLandedCostType)
	}

	// Purchase Returns
	purchaseReturns := rg.Group("/purchase-returns")
	purchaseReturns.Use(middleware.RequirePermission("purchase", "return", "read"))
	{
		purchaseReturns.GET("", h.ListPurchaseReturns)
		purchaseReturns.POST("", middleware.RequirePermission("purchase", "return", "create"), h.CreatePurchaseReturn)
		purchaseReturns.GET("/:id", h.GetPurchaseReturn)
		purchaseReturns.DELETE("/:id", middleware.RequirePermission("purchase", "return", "delete"), h.DeletePurchaseReturn)
		purchaseReturns.POST("/:id/submit", middleware.RequirePermission("purchase", "return", "update"), h.SubmitPurchaseReturn)
		purchaseReturns.POST("/:id/approve", middleware.RequirePermission("purchase", "return", "approve"), h.ApprovePurchaseReturn)
		purchaseReturns.POST("/:id/reject", middleware.RequirePermission("purchase", "return", "approve"), h.RejectPurchaseReturn)
		purchaseReturns.POST("/:id/ship", middleware.RequirePermission("purchase", "return", "update"), h.ShipPurchaseReturn)
		purchaseReturns.POST("/:id/receive", middleware.RequirePermission("purchase", "return", "update"), h.ReceivePurchaseReturn)
		purchaseReturns.POST("/:id/credit", middleware.RequirePermission("purchase", "return", "update"), h.ApplyCreditNote)
		purchaseReturns.POST("/:id/cancel", middleware.RequirePermission("purchase", "return", "update"), h.CancelPurchaseReturn)
	}

	// Blanket Orders (Standing Orders / Call-off Contracts)
	blanketOrders := rg.Group("/blanket-orders")
	blanketOrders.Use(middleware.RequirePermission("purchase", "contract", "read"))
	{
		blanketOrders.GET("", h.ListBlanketOrders)
		blanketOrders.POST("", middleware.RequirePermission("purchase", "contract", "create"), h.CreateBlanketOrder)
		blanketOrders.GET("/:id", h.GetBlanketOrder)
		blanketOrders.PUT("/:id", middleware.RequirePermission("purchase", "contract", "update"), h.UpdateBlanketOrder)
		blanketOrders.DELETE("/:id", middleware.RequirePermission("purchase", "contract", "delete"), h.DeleteBlanketOrder)
		blanketOrders.POST("/:id/activate", middleware.RequirePermission("purchase", "contract", "update"), h.ActivateBlanketOrder)
		blanketOrders.GET("/:id/releases", h.ListBlanketOrderReleases)
		blanketOrders.POST("/:id/releases", middleware.RequirePermission("purchase", "order", "create"), h.CreateBlanketOrderRelease)
		blanketOrders.POST("/:id/releases/:release_id/confirm", middleware.RequirePermission("purchase", "order", "approve"), h.ConfirmBlanketOrderRelease)
		blanketOrders.POST("/:id/releases/:release_id/cancel", middleware.RequirePermission("purchase", "order", "update"), h.CancelBlanketOrderRelease)
	}

	// Procurement Rules Engine
	procurementRules := rg.Group("/procurement-rules")
	procurementRules.Use(middleware.RequirePermission("purchase", "rule", "read"))
	{
		procurementRules.GET("", h.ListProcurementRules)
		procurementRules.POST("", middleware.RequirePermission("purchase", "rule", "create"), h.CreateProcurementRule)
		procurementRules.GET("/:id", h.GetProcurementRule)
		procurementRules.PUT("/:id", middleware.RequirePermission("purchase", "rule", "update"), h.UpdateProcurementRule)
		procurementRules.DELETE("/:id", middleware.RequirePermission("purchase", "rule", "delete"), h.DeleteProcurementRule)
	}

	// Approval Workflows
	approvalWorkflows := rg.Group("/approval-workflows")
	{
		approvalWorkflows.GET("/my-approvals", h.GetMyPendingApprovals)
		approvalWorkflows.GET("/by-document", h.GetWorkflowByDocument)
		approvalWorkflows.GET("/:id", h.GetApprovalWorkflow)
		approvalWorkflows.POST("/:id/approve", h.ApproveWorkflowStep)
		approvalWorkflows.POST("/:id/reject", h.RejectWorkflowStep)
	}

	// Dropshipping
	dropship := rg.Group("/dropship")
	dropship.Use(middleware.RequirePermission("sales", "dropship", "read"))
	{
		// Dropship Orders
		dropship.GET("/orders", h.ListDropshipOrders)
		dropship.POST("/orders", middleware.RequirePermission("sales", "dropship", "create"), h.CreateDropshipOrder)
		dropship.GET("/orders/:id", h.GetDropshipOrder)
		dropship.PUT("/orders/:id", middleware.RequirePermission("sales", "dropship", "update"), h.UpdateDropshipOrder)
		dropship.POST("/orders/:id/ship", middleware.RequirePermission("sales", "dropship", "update"), h.MarkDropshipShipped)
		dropship.POST("/orders/:id/deliver", middleware.RequirePermission("sales", "dropship", "update"), h.MarkDropshipDelivered)
		dropship.POST("/orders/:id/cancel", middleware.RequirePermission("sales", "dropship", "update"), h.CancelDropshipOrder)

		// Dropship Stats
		dropship.GET("/stats", h.GetDropshipStats)

		// Dropship Vendor Settings
		dropship.GET("/vendors", h.ListDropshipVendors)
		dropship.POST("/vendors", middleware.RequirePermission("sales", "dropship", "create"), h.CreateDropshipVendorSettings)
		dropship.PUT("/vendors/:id", middleware.RequirePermission("sales", "dropship", "update"), h.UpdateDropshipVendorSettings)
		dropship.DELETE("/vendors/:id", middleware.RequirePermission("sales", "dropship", "delete"), h.DeleteDropshipVendorSettings)

		// Dropship Product-Vendor Links
		dropship.GET("/product-vendors", h.ListDropshipProductVendors)
		dropship.POST("/product-vendors", middleware.RequirePermission("sales", "dropship", "create"), h.CreateDropshipProductVendor)
		dropship.PUT("/product-vendors/:id", middleware.RequirePermission("sales", "dropship", "update"), h.UpdateDropshipProductVendor)
		dropship.DELETE("/product-vendors/:id", middleware.RequirePermission("sales", "dropship", "delete"), h.DeleteDropshipProductVendor)

		// Dropshippable Products
		dropship.GET("/products", h.GetDropshippableProducts)
	}

	// Account Types (reference data)
	rg.GET("/account-types", h.ListAccountTypes)

	// Chart of Accounts
	accounts := rg.Group("/accounts")
	accounts.Use(middleware.RequirePermission("finance", "account", "read"))
	{
		accounts.GET("", h.ListAccounts)
		accounts.GET("/next-code", h.GetNextAccountCode)
		accounts.POST("", middleware.RequirePermission("finance", "account", "create"), h.CreateAccount)
		accounts.GET("/:id", h.GetAccount)
		accounts.PUT("/:id", middleware.RequirePermission("finance", "account", "update"), h.UpdateAccount)
		accounts.DELETE("/:id", middleware.RequirePermission("finance", "account", "delete"), h.DeleteAccount)
		accounts.GET("/:id/transactions", h.GetAccountTransactions)
	}

	// Journals (accounting journals like GEN, SAL, PUR, MISC)
	journalGroup := rg.Group("/journals")
	journalGroup.Use(middleware.RequirePermission("finance", "journal", "read"))
	{
		journalGroup.GET("", h.ListJournals)
		journalGroup.POST("", middleware.RequirePermission("finance", "journal", "create"), h.CreateJournal)
		journalGroup.GET("/:id", h.GetJournal)
		journalGroup.PUT("/:id", middleware.RequirePermission("finance", "journal", "update"), h.UpdateJournal)
		journalGroup.DELETE("/:id", middleware.RequirePermission("finance", "journal", "delete"), h.DeleteJournal)
		journalGroup.GET("/:id/payment-methods", h.ListJournalPaymentMethods)
		journalGroup.POST("/:id/payment-methods", middleware.RequirePermission("finance", "journal", "update"), h.AddJournalPaymentMethod)
		journalGroup.PUT("/:id/payment-methods/:pmId", middleware.RequirePermission("finance", "journal", "update"), h.UpdateJournalPaymentMethod)
		journalGroup.DELETE("/:id/payment-methods/:pmId", middleware.RequirePermission("finance", "journal", "update"), h.RemoveJournalPaymentMethod)
	}

	// Payment Methods
	paymentMethodsGroup := rg.Group("/payment-methods")
	{
		paymentMethodsGroup.GET("", h.ListPaymentMethods)
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
		currencies.POST("", h.CreateCurrency)
		currencies.GET("/:code", h.GetCurrency)
		currencies.PUT("/:code", h.UpdateCurrency)
		currencies.DELETE("/:code", h.DeleteCurrency)
		currencies.GET("/:code/rate", h.GetExchangeRate)
		currencies.POST("/:code/rate", h.SetExchangeRate)
	}

	// Exchange Rates
	exchangeRates := rg.Group("/exchange-rates")
	{
		exchangeRates.GET("", h.ListExchangeRates)
	}

	// Bank Accounts
	bankAccounts := rg.Group("/bank-accounts")
	bankAccounts.Use(middleware.RequirePermission("finance", "bank_account", "read"))
	{
		bankAccounts.GET("", h.ListBankAccounts)
		bankAccounts.POST("", middleware.RequirePermission("finance", "bank_account", "create"), h.CreateBankAccount)
		bankAccounts.GET("/:id", h.GetBankAccount)
		bankAccounts.PUT("/:id", middleware.RequirePermission("finance", "bank_account", "update"), h.UpdateBankAccount)
		bankAccounts.DELETE("/:id", middleware.RequirePermission("finance", "bank_account", "delete"), h.DeleteBankAccount)
		// Bank Transactions (nested under bank accounts)
		bankAccounts.GET("/:id/transactions", h.ListBankTransactions)
		bankAccounts.POST("/:id/transactions", middleware.RequirePermission("finance", "bank_account", "update"), h.CreateBankTransaction)
		bankAccounts.POST("/:id/transactions/:transactionId/reconcile", middleware.RequirePermission("finance", "bank_account", "update"), h.ReconcileBankTransaction)

		// Bank Reconciliation
		bankAccounts.GET("/:id/reconciliations", h.ListBankReconciliations)
		bankAccounts.POST("/:id/reconciliations", middleware.RequirePermission("finance", "bank_account", "update"), h.CreateBankReconciliation)
		bankAccounts.GET("/:id/reconciliations/:reconciliationId", h.GetBankReconciliation)
		bankAccounts.PUT("/:id/reconciliations/:reconciliationId", middleware.RequirePermission("finance", "bank_account", "update"), h.UpdateBankReconciliation)
		bankAccounts.POST("/:id/reconciliations/:reconciliationId/complete", middleware.RequirePermission("finance", "bank_account", "update"), h.CompleteBankReconciliation)
		bankAccounts.DELETE("/:id/reconciliations/:reconciliationId", middleware.RequirePermission("finance", "bank_account", "delete"), h.DeleteBankReconciliation)

		// Bank Statement Import
		bankAccounts.POST("/:id/import", middleware.RequirePermission("finance", "bank_account", "update"), h.ImportBankStatement)
	}

	// Cash Transactions (Kassa)
	cashTransactions := rg.Group("/cash-transactions")
	cashTransactions.Use(middleware.RequirePermission("finance", "cash_transaction", "read"))
	{
		cashTransactions.GET("", h.ListCashTransactions)
		cashTransactions.POST("", middleware.RequirePermission("finance", "cash_transaction", "create"), h.CreateCashTransaction)
		cashTransactions.GET("/:id", h.GetCashTransaction)
		cashTransactions.PUT("/:id", middleware.RequirePermission("finance", "cash_transaction", "update"), h.UpdateCashTransaction)
		cashTransactions.DELETE("/:id", middleware.RequirePermission("finance", "cash_transaction", "delete"), h.DeleteCashTransaction)
	}

	// Fiscal Years
	fiscalYears := rg.Group("/fiscal-years")
	{
		fiscalYears.GET("", h.ListFiscalYears)
		fiscalYears.POST("", h.CreateFiscalYear)
		fiscalYears.GET("/:id", h.GetFiscalYear)
		fiscalYears.PUT("/:id", h.UpdateFiscalYear)
		fiscalYears.POST("/:id/close", h.CloseFiscalYear)
		fiscalYears.DELETE("/:id", h.DeleteFiscalYear)
	}

	// Fiscal Periods
	fiscalPeriods := rg.Group("/fiscal-periods")
	{
		fiscalPeriods.GET("", h.ListFiscalPeriods)
		fiscalPeriods.POST("", h.CreateFiscalPeriod)
		fiscalPeriods.POST("/batch", h.BatchCreateFiscalPeriods)
		fiscalPeriods.POST("/:id/close", h.CloseFiscalPeriod)
		fiscalPeriods.POST("/:id/reopen", h.ReopenFiscalPeriod)
	}

	// Budgets
	budgets := rg.Group("/budgets")
	{
		budgets.GET("", h.ListBudgets)
		budgets.POST("", h.CreateBudget)
		budgets.GET("/:id", h.GetBudget)
		budgets.PUT("/:id", h.UpdateBudget)
		budgets.DELETE("/:id", h.DeleteBudget)
		budgets.POST("/:id/activate", h.ActivateBudget)
	}

	// Budget Lines
	budgetLines := rg.Group("/budget-lines")
	{
		budgetLines.GET("", h.ListBudgetLines)
		budgetLines.POST("", h.CreateBudgetLine)
		budgetLines.PUT("/:id", h.UpdateBudgetLine)
		budgetLines.DELETE("/:id", h.DeleteBudgetLine)
	}

	// Recurring Journal Entries
	recurringJournals := rg.Group("/recurring-journals")
	recurringJournals.Use(middleware.RequirePermission("finance", "journal_entry", "read"))
	{
		recurringJournals.GET("", h.ListRecurringJournalTemplates)
		recurringJournals.POST("", middleware.RequirePermission("finance", "journal_entry", "create"), h.CreateRecurringJournalTemplate)
		recurringJournals.GET("/pending", h.GetPendingRecurringEntries)
		recurringJournals.GET("/:id", h.GetRecurringJournalTemplate)
		recurringJournals.PUT("/:id", middleware.RequirePermission("finance", "journal_entry", "update"), h.UpdateRecurringJournalTemplate)
		recurringJournals.DELETE("/:id", middleware.RequirePermission("finance", "journal_entry", "delete"), h.DeleteRecurringJournalTemplate)
		recurringJournals.POST("/:id/generate", middleware.RequirePermission("finance", "journal_entry", "create"), h.GenerateRecurringJournalEntry)
	}

	// Cash Registers (Kassa)
	cashRegisters := rg.Group("/cash/registers")
	cashRegisters.Use(middleware.RequirePermission("finance", "cash", "read"))
	{
		cashRegisters.GET("", h.ListCashRegisters)
		cashRegisters.POST("", middleware.RequirePermission("finance", "cash", "create"), h.CreateCashRegister)
		cashRegisters.GET("/:id", h.GetCashRegister)
		cashRegisters.PUT("/:id", middleware.RequirePermission("finance", "cash", "update"), h.UpdateCashRegister)
	}

	// Cash Orders (PKO/RKO)
	cashOrders := rg.Group("/cash/orders")
	cashOrders.Use(middleware.RequirePermission("finance", "cash", "read"))
	{
		cashOrders.GET("", h.ListCashOrders)
		cashOrders.POST("", middleware.RequirePermission("finance", "cash", "create"), h.CreateCashOrder)
		cashOrders.GET("/:id", h.GetCashOrder)
		cashOrders.PUT("/:id", middleware.RequirePermission("finance", "cash", "update"), h.UpdateCashOrder)
		cashOrders.POST("/:id/confirm", middleware.RequirePermission("finance", "cash", "approve"), h.ConfirmCashOrder)
	}

	// Cash Book (Kassa kitob)
	rg.GET("/cash/book", middleware.RequirePermission("finance", "cash", "read"), h.GetCashBook)

	// Currency Rates Sync & Revaluation
	currencyOps := rg.Group("/currency")
	currencyOps.Use(middleware.RequirePermission("finance", "currency", "read"))
	{
		currencyOps.GET("/rates", h.ListExchangeDiffs)
		currencyOps.POST("/rates/sync", middleware.RequirePermission("finance", "currency", "create"), h.SyncCurrencyRates)
		currencyOps.POST("/revalue", middleware.RequirePermission("finance", "currency", "create"), h.RevalueCurrency)
	}

	// Reconciliation Acts (Akt sverka)
	reconciliation := rg.Group("/reconciliation")
	reconciliation.Use(middleware.RequirePermission("finance", "reconciliation", "read"))
	{
		reconciliation.GET("", h.ListReconciliationActs)
		reconciliation.POST("", middleware.RequirePermission("finance", "reconciliation", "create"), h.CreateReconciliationAct)
		reconciliation.POST("/bulk-generate", middleware.RequirePermission("finance", "reconciliation", "create"), h.BulkGenerateReconciliation)
		reconciliation.GET("/:id", h.GetReconciliationAct)
		reconciliation.POST("/:id/refresh", middleware.RequirePermission("finance", "reconciliation", "update"), h.RefreshReconciliationAct)
		reconciliation.PUT("/:id", middleware.RequirePermission("finance", "reconciliation", "update"), h.UpdateReconciliationAct)
		reconciliation.DELETE("/:id", middleware.RequirePermission("finance", "reconciliation", "delete"), h.DeleteReconciliationAct)
		reconciliation.GET("/:id/export", h.ExportReconciliationAct)
	}

	// Budgets (Byudjetlashtirish)
	budget := rg.Group("/budget")
	budget.Use(middleware.RequirePermission("finance", "budget", "read"))
	{
		budget.GET("", h.ListBudgetsV2)
		budget.POST("", middleware.RequirePermission("finance", "budget", "create"), h.CreateBudgetV2)
		budget.GET("/consolidated", h.GetConsolidatedBudget)
		budget.GET("/cash-flow", h.GetBudgetCashFlow)
		budget.GET("/plan-vs-actual", h.GetBudgetPlanVsActual)
		budget.GET("/:id", h.GetBudgetV2)
		budget.PUT("/:id", middleware.RequirePermission("finance", "budget", "update"), h.UpdateBudgetV2)
		budget.DELETE("/:id", middleware.RequirePermission("finance", "budget", "delete"), h.DeleteBudgetV2)
		budget.POST("/:id/submit", middleware.RequirePermission("finance", "budget", "update"), h.SubmitBudgetForApproval)
		budget.POST("/:id/approve", middleware.RequirePermission("finance", "budget", "update"), h.ApproveBudget)
		budget.POST("/:id/reject", middleware.RequirePermission("finance", "budget", "update"), h.RejectBudget)
		budget.GET("/:id/lines", h.ListBudgetLines)
		budget.POST("/:id/lines", middleware.RequirePermission("finance", "budget", "create"), h.CreateBudgetLine)
		budget.PUT("/:id/lines/:lineId", middleware.RequirePermission("finance", "budget", "update"), h.UpdateBudgetLine)
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
		// Employee-Organization assignments
		employees.GET("/:id/organizations", h.ListEmployeeOrganizations)
		// Employee module permissions
		employees.GET("/:id/permissions", h.GetEmployeePermissions)
		employees.PUT("/:id/permissions", middleware.RequirePermission("hr", "employee", "update"), h.BulkUpdateEmployeePermissions)
		employees.PUT("/:id/permissions/module", middleware.RequirePermission("hr", "employee", "update"), h.UpdateEmployeeModulePermission)
		employees.DELETE("/:id/permissions", middleware.RequirePermission("hr", "employee", "delete"), h.DeleteEmployeePermissions)
	}

	// Employee-Organization Assignments
	employeeOrgs := rg.Group("/employee-organizations")
	employeeOrgs.Use(middleware.RequirePermission("hr", "employee", "read"))
	{
		employeeOrgs.POST("", middleware.RequirePermission("hr", "employee", "update"), h.AssignEmployeeToOrganization)
		employeeOrgs.PUT("/:id", middleware.RequirePermission("hr", "employee", "update"), h.UpdateEmployeeOrganization)
		employeeOrgs.DELETE("/:id", middleware.RequirePermission("hr", "employee", "update"), h.RemoveEmployeeFromOrganization)
	}

	// HR - Attendance Records
	attendance := rg.Group("/attendance")
	attendance.Use(middleware.RequirePermission("hr", "attendance", "read"))
	{
		attendance.GET("", h.ListAttendanceRecords)
		attendance.POST("", middleware.RequirePermission("hr", "attendance", "create"), h.CreateAttendanceRecord)
		attendance.GET("/:id", h.GetAttendanceRecord)
		attendance.PUT("/:id", middleware.RequirePermission("hr", "attendance", "update"), h.UpdateAttendanceRecord)
		attendance.DELETE("/:id", middleware.RequirePermission("hr", "attendance", "delete"), h.DeleteAttendanceRecord)
	}

	// HR - Leave Requests
	leaveRequests := rg.Group("/leave-requests")
	leaveRequests.Use(middleware.RequirePermission("hr", "leave", "read"))
	{
		leaveRequests.GET("", h.ListLeaveRequests)
		leaveRequests.POST("", middleware.RequirePermission("hr", "leave", "create"), h.CreateLeaveRequest)
		leaveRequests.GET("/:id", h.GetLeaveRequest)
		leaveRequests.PUT("/:id", middleware.RequirePermission("hr", "leave", "update"), h.UpdateLeaveRequest)
		leaveRequests.DELETE("/:id", middleware.RequirePermission("hr", "leave", "delete"), h.DeleteLeaveRequest)
	}

	// HR - Leave Balances
	leaveBalances := rg.Group("/leave-balances")
	leaveBalances.Use(middleware.RequirePermission("hr", "leave", "read"))
	{
		leaveBalances.GET("", h.ListLeaveBalances)
		leaveBalances.POST("", middleware.RequirePermission("hr", "leave", "create"), h.CreateLeaveBalance)
		leaveBalances.GET("/:id", h.GetLeaveBalance)
		leaveBalances.PUT("/:id", middleware.RequirePermission("hr", "leave", "update"), h.UpdateLeaveBalance)
		leaveBalances.DELETE("/:id", middleware.RequirePermission("hr", "leave", "delete"), h.DeleteLeaveBalance)
	}

	// HR - Employee Contracts
	employeeContracts := rg.Group("/employee-contracts")
	employeeContracts.Use(middleware.RequirePermission("hr", "contract", "read"))
	{
		employeeContracts.GET("", h.ListEmployeeContracts)
		employeeContracts.POST("", middleware.RequirePermission("hr", "contract", "create"), h.CreateEmployeeContract)
		employeeContracts.GET("/:id", h.GetEmployeeContract)
		employeeContracts.PUT("/:id", middleware.RequirePermission("hr", "contract", "update"), h.UpdateEmployeeContract)
		employeeContracts.DELETE("/:id", middleware.RequirePermission("hr", "contract", "delete"), h.DeleteEmployeeContract)
	}

	// Workflows
	workflows := rg.Group("/workflows")
	workflows.Use(middleware.RequirePermission("workflow", "workflow", "read"))
	{
		workflows.GET("", h.ListWorkflows)
		workflows.POST("", middleware.RequirePermission("workflow", "workflow", "create"), h.CreateWorkflow)
		workflows.GET("/:id", h.GetWorkflow)
		workflows.PUT("/:id", middleware.RequirePermission("workflow", "workflow", "update"), h.UpdateWorkflow)
		workflows.DELETE("/:id", middleware.RequirePermission("workflow", "workflow", "delete"), h.DeleteWorkflow)
	}

	// Workflow Automation Rules
	workflowRules := rg.Group("/workflow-rules")
	workflowRules.Use(middleware.RequirePermission("workflow", "workflow", "read"))
	{
		workflowRules.GET("", h.ListWorkflowRules)
		workflowRules.POST("", middleware.RequirePermission("workflow", "workflow", "create"), h.CreateWorkflowRule)
		workflowRules.GET("/:id", h.GetWorkflowRule)
		workflowRules.PUT("/:id", middleware.RequirePermission("workflow", "workflow", "update"), h.UpdateWorkflowRule)
		workflowRules.DELETE("/:id", middleware.RequirePermission("workflow", "workflow", "delete"), h.DeleteWorkflowRule)
	}

	// Workflow Logs
	rg.GET("/workflow-logs", middleware.RequirePermission("workflow", "workflow", "read"), h.ListWorkflowLogs)

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

		// Invoice Extraction
		ai.POST("/extract-invoice", h.ExtractInvoice)

		// Usage Stats
		ai.GET("/usage", h.GetAIUsageStats)
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

	// Admin Settings (alias for /settings to support frontend admin panel)
	adminSettings := rg.Group("/admin/settings")
	adminSettings.Use(middleware.RequirePermission("settings", "tenant", "read"))
	{
		adminSettings.GET("", h.GetAdminSettings)
		adminSettings.PUT("", middleware.RequirePermission("settings", "tenant", "update"), h.UpdateAdminSettings)
		adminSettings.PATCH("/:section", middleware.RequirePermission("settings", "tenant", "update"), h.UpdateAdminSettingsSection)
		adminSettings.POST("/:section/reset", middleware.RequirePermission("settings", "tenant", "update"), h.ResetAdminSettingsSection)
		adminSettings.POST("/reset", middleware.RequirePermission("settings", "tenant", "update"), h.ResetAllAdminSettings)
	}

	// System Admin routes (cross-tenant access)
	admin := rg.Group("/admin")
	admin.Use(middleware.RequireSystemAdmin())
	{
		admin.GET("/users", h.ListAllSystemUsers)
		admin.DELETE("/users/:id", h.DeleteSystemUser)
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

	// Attachments/Files (upload and delete require auth, GET is public - see registerPublicRoutes)
	files := rg.Group("/files")
	{
		files.POST("/upload", h.UploadFile)
		files.DELETE("/:id", h.DeleteFile)
	}

	// =====================================================
	// MANUFACTURING MODULE ROUTES
	// =====================================================

	// Work Centers
	workCenters := rg.Group("/work-centers")
	workCenters.Use(middleware.RequirePermission("manufacturing", "work_centers", "read"))
	{
		workCenters.GET("", h.ListWorkCenters)
		workCenters.POST("", middleware.RequirePermission("manufacturing", "work_centers", "create"), h.CreateWorkCenter)
		workCenters.GET("/:id", h.GetWorkCenter)
		workCenters.PUT("/:id", middleware.RequirePermission("manufacturing", "work_centers", "update"), h.UpdateWorkCenter)
		workCenters.DELETE("/:id", middleware.RequirePermission("manufacturing", "work_centers", "delete"), h.DeleteWorkCenter)
	}

	// Production Orders
	productionOrders := rg.Group("/production-orders")
	productionOrders.Use(middleware.RequirePermission("manufacturing", "production_orders", "read"))
	{
		productionOrders.GET("", h.ListProductionOrders)
		productionOrders.POST("", middleware.RequirePermission("manufacturing", "production_orders", "create"), h.CreateProductionOrder)
		productionOrders.GET("/schedule", h.GetProductionSchedule)
		productionOrders.GET("/stats", h.GetManufacturingStats)
		productionOrders.GET("/:id", h.GetProductionOrder)
		productionOrders.PUT("/:id", middleware.RequirePermission("manufacturing", "production_orders", "update"), h.UpdateProductionOrder)
		productionOrders.DELETE("/:id", middleware.RequirePermission("manufacturing", "production_orders", "delete"), h.DeleteProductionOrder)
		productionOrders.POST("/:id/confirm", middleware.RequirePermission("manufacturing", "production_orders", "approve"), h.ConfirmProductionOrder)
		productionOrders.POST("/:id/start", h.StartProductionOrder)
		productionOrders.POST("/:id/pause", h.PauseProductionOrder)
		productionOrders.POST("/:id/complete", h.CompleteProductionOrder)
		productionOrders.POST("/:id/cancel", h.CancelProductionOrder)
		productionOrders.POST("/:id/record-production", h.RecordProduction)
	}

	// Work Orders
	workOrders := rg.Group("/work-orders")
	workOrders.Use(middleware.RequirePermission("manufacturing", "work_orders", "read"))
	{
		workOrders.GET("", h.ListWorkOrders)
		workOrders.POST("", middleware.RequirePermission("manufacturing", "work_orders", "create"), h.CreateWorkOrder)
		workOrders.GET("/:id", h.GetWorkOrder)
		workOrders.POST("/:id/start", h.StartWorkOrder)
		workOrders.POST("/:id/pause", h.PauseWorkOrder)
		workOrders.POST("/:id/complete", h.CompleteWorkOrder)
		workOrders.POST("/:id/time", h.RecordWorkOrderTime)
	}

	// Manufacturing Transfers (Pick Components / Store Finished)
	mfgTransfers := rg.Group("/manufacturing-transfers")
	mfgTransfers.Use(middleware.RequirePermission("manufacturing", "transfers", "read"))
	{
		mfgTransfers.GET("", h.ListManufacturingTransfers)
		mfgTransfers.GET("/:id", h.GetManufacturingTransfer)
		mfgTransfers.POST("/:id/validate", middleware.RequirePermission("manufacturing", "transfers", "update"), h.ValidateManufacturingTransfer)
	}

	// Equipment & Maintenance
	equipment := rg.Group("/equipment")
	equipment.Use(middleware.RequirePermission("manufacturing", "work_centers", "read"))
	{
		equipment.GET("", h.ListEquipment)
		equipment.POST("", middleware.RequirePermission("manufacturing", "work_centers", "create"), h.CreateEquipment)
		equipment.PUT("/:id", middleware.RequirePermission("manufacturing", "work_centers", "update"), h.UpdateEquipment)
		equipment.DELETE("/:id", middleware.RequirePermission("manufacturing", "work_centers", "delete"), h.DeleteEquipment)
		equipment.GET("/:id/maintenance", h.ListMaintenanceTasks)
		equipment.POST("/:id/maintenance", middleware.RequirePermission("manufacturing", "work_centers", "create"), h.CreateMaintenanceTask)
		equipment.PUT("/:id/maintenance/:task_id", middleware.RequirePermission("manufacturing", "work_centers", "update"), h.CompleteMaintenanceTask)
		equipment.PATCH("/:id/maintenance/:task_id", middleware.RequirePermission("manufacturing", "work_centers", "update"), h.UpdateMaintenanceTask)
	}

	// =====================================================
	// ERP EXTENSIONS MODULE ROUTES
	// =====================================================

	// Payroll Periods
	payrollPeriods := rg.Group("/payroll-periods")
	payrollPeriods.Use(middleware.RequirePermission("hr", "payroll", "read"))
	{
		payrollPeriods.GET("", h.ListPayrollPeriods)
		payrollPeriods.POST("", middleware.RequirePermission("hr", "payroll", "create"), h.CreatePayrollPeriod)
		payrollPeriods.GET("/:id", h.GetPayrollPeriod)
		payrollPeriods.PUT("/:id", middleware.RequirePermission("hr", "payroll", "update"), h.UpdatePayrollPeriod)
		payrollPeriods.DELETE("/:id", middleware.RequirePermission("hr", "payroll", "delete"), h.DeletePayrollPeriod)
		payrollPeriods.POST("/:id/process", middleware.RequirePermission("hr", "payroll", "approve"), h.ProcessPayroll)
		payrollPeriods.GET("/:id/entries", h.ListPayrollEntries)
		payrollPeriods.POST("/:id/entries", middleware.RequirePermission("hr", "payroll", "create"), h.CreatePayrollEntry)
	}

	// Expense Categories
	expenseCategories := rg.Group("/expense-categories")
	{
		expenseCategories.GET("", h.ListExpenseCategories)
	}

	// Expenses
	expenses := rg.Group("/expenses")
	expenses.Use(middleware.RequirePermission("finance", "expense", "read"))
	{
		expenses.GET("", h.ListExpenses)
		expenses.POST("", middleware.RequirePermission("finance", "expense", "create"), h.CreateExpense)
		expenses.GET("/:id", h.GetExpense)
		expenses.PUT("/:id", middleware.RequirePermission("finance", "expense", "update"), h.UpdateExpense)
		expenses.DELETE("/:id", middleware.RequirePermission("finance", "expense", "delete"), h.DeleteExpense)
		expenses.POST("/:id/approve", middleware.RequirePermission("finance", "expense", "approve"), h.ApproveExpense)
	}

	// Asset Categories
	assetCategories := rg.Group("/asset-categories")
	{
		assetCategories.GET("", h.ListAssetCategories)
	}

	// Fixed Assets
	fixedAssets := rg.Group("/fixed-assets")
	fixedAssets.Use(middleware.RequirePermission("finance", "asset", "read"))
	{
		fixedAssets.GET("", h.ListFixedAssets)
		fixedAssets.POST("", middleware.RequirePermission("finance", "asset", "create"), h.CreateFixedAsset)
		fixedAssets.GET("/:id", h.GetFixedAsset)
		fixedAssets.PUT("/:id", middleware.RequirePermission("finance", "asset", "update"), h.UpdateFixedAsset)
		fixedAssets.DELETE("/:id", middleware.RequirePermission("finance", "asset", "delete"), h.DeleteFixedAsset)
		fixedAssets.POST("/:id/dispose", middleware.RequirePermission("finance", "asset", "approve"), h.DisposeFixedAsset)
		fixedAssets.GET("/:id/depreciation", h.GetDepreciationEntries)
	}

	// Run Depreciation (batch operation)
	rg.POST("/run-depreciation", middleware.RequirePermission("finance", "asset", "approve"), h.RunDepreciation)

	// Projects
	projects := rg.Group("/projects")
	projects.Use(middleware.RequirePermission("projects", "project", "read"))
	{
		projects.GET("", h.ListProjects)
		projects.POST("", middleware.RequirePermission("projects", "project", "create"), h.CreateProject)
		projects.GET("/:id", h.GetProject)
		projects.PUT("/:id", middleware.RequirePermission("projects", "project", "update"), h.UpdateProject)
		projects.DELETE("/:id", middleware.RequirePermission("projects", "project", "delete"), h.DeleteProject)

		// Project Tasks
		projects.GET("/:id/tasks", h.ListProjectTasks)
		projects.POST("/:id/tasks", middleware.RequirePermission("projects", "task", "create"), h.CreateProjectTask)
		projects.PUT("/:id/tasks/:taskId", middleware.RequirePermission("projects", "task", "update"), h.UpdateProjectTask)
		projects.DELETE("/:id/tasks/:taskId", middleware.RequirePermission("projects", "task", "delete"), h.DeleteProjectTask)

		// Project Milestones
		projects.GET("/:id/milestones", h.ListProjectMilestones)
		projects.POST("/:id/milestones", middleware.RequirePermission("projects", "milestone", "create"), h.CreateProjectMilestone)
		projects.PUT("/:id/milestones/:milestoneId", middleware.RequirePermission("projects", "milestone", "update"), h.UpdateProjectMilestone)
		projects.DELETE("/:id/milestones/:milestoneId", middleware.RequirePermission("projects", "milestone", "delete"), h.DeleteProjectMilestone)

		// Time Entries
		projects.GET("/:id/time-entries", h.ListTimeEntries)
		projects.POST("/:id/time-entries", middleware.RequirePermission("projects", "time_entry", "create"), h.CreateTimeEntry)

		// Project Expenses
		projects.GET("/:id/expenses", h.ListProjectExpenses)
		projects.POST("/:id/expenses", middleware.RequirePermission("projects", "expense", "create"), h.CreateProjectExpense)
		projects.DELETE("/:id/expenses/:expenseId", middleware.RequirePermission("projects", "expense", "delete"), h.DeleteProjectExpense)

		// Project Team Members
		projects.GET("/:id/team-members", h.ListProjectTeamMembers)
		projects.POST("/:id/team-members", middleware.RequirePermission("projects", "project", "update"), h.AddProjectTeamMember)
		projects.DELETE("/:id/team-members/:memberId", middleware.RequirePermission("projects", "project", "update"), h.RemoveProjectTeamMember)
	}

	// =====================================================
	// CARGO MODULE ROUTES
	// =====================================================

	// Cargo Shipments
	cargoShipments := rg.Group("/cargo/shipments")
	cargoShipments.Use(middleware.RequirePermission("cargo", "shipment", "read"))
	{
		cargoShipments.GET("", h.ListCargoShipments)
		cargoShipments.POST("", middleware.RequirePermission("cargo", "shipment", "create"), h.CreateCargoShipment)
		cargoShipments.GET("/:id", h.GetCargoShipment)
		cargoShipments.PUT("/:id", middleware.RequirePermission("cargo", "shipment", "update"), h.UpdateCargoShipment)
		cargoShipments.PUT("/:id/status", middleware.RequirePermission("cargo", "shipment", "update"), h.UpdateCargoShipmentStatus)
		cargoShipments.DELETE("/:id", middleware.RequirePermission("cargo", "shipment", "delete"), h.DeleteCargoShipment)
		cargoShipments.POST("/:id/distribution", middleware.RequirePermission("cargo", "distribution", "create"), h.CreateCargoDistribution)
	}

	// Cargo Cash Register
	cargoCash := rg.Group("/cargo/cash")
	cargoCash.Use(middleware.RequirePermission("cargo", "cash", "read"))
	{
		cargoCash.GET("", h.ListCargoCashTransactions)
		cargoCash.POST("", middleware.RequirePermission("cargo", "cash", "create"), h.CreateCargoCashTransaction)
		cargoCash.PUT("/:id", middleware.RequirePermission("cargo", "cash", "update"), h.UpdateCargoCashTransaction)
		cargoCash.DELETE("/:id", middleware.RequirePermission("cargo", "cash", "delete"), h.DeleteCargoCashTransaction)
		cargoCash.GET("/summary", h.GetCargoCashSummary)
	}

	// =====================================================
	// CONSTRUCTION MODULE ROUTES
	// =====================================================

	// Construction Projects
	constructionProjects := rg.Group("/construction/projects")
	constructionProjects.Use(middleware.RequirePermission("construction", "project", "read"))
	{
		constructionProjects.GET("", h.ListConstructionProjects)
		constructionProjects.POST("", middleware.RequirePermission("construction", "project", "create"), h.CreateConstructionProject)
		constructionProjects.GET("/:id", h.GetConstructionProject)
		constructionProjects.PUT("/:id", middleware.RequirePermission("construction", "project", "update"), h.UpdateConstructionProject)
		constructionProjects.DELETE("/:id", middleware.RequirePermission("construction", "project", "delete"), h.DeleteConstructionProject)
		constructionProjects.GET("/:id/dashboard", h.GetConstructionProjectDashboard)
		// Buildings/Blocks
		constructionProjects.GET("/:id/buildings", h.ListConstructionBuildings)
		constructionProjects.POST("/:id/buildings", middleware.RequirePermission("construction", "project", "create"), h.CreateConstructionBuilding)
		constructionProjects.PUT("/:id/buildings/:building_id", middleware.RequirePermission("construction", "project", "update"), h.UpdateConstructionBuilding)
		constructionProjects.DELETE("/:id/buildings/:building_id", middleware.RequirePermission("construction", "project", "delete"), h.DeleteConstructionBuilding)
		// Smeta Sections
		constructionProjects.GET("/:id/sections", h.ListSmetaSections)
		constructionProjects.POST("/:id/sections", middleware.RequirePermission("construction", "smeta", "create"), h.CreateSmetaSection)

		// Project Vendors
		constructionProjects.GET("/:id/vendors", h.ListProjectVendors)
		constructionProjects.POST("/:id/vendors", middleware.RequirePermission("construction", "projects", "update"), h.CreateProjectVendor)

		// Photo Reports
		constructionProjects.GET("/:id/photo-reports", h.ListPhotoReports)
		constructionProjects.POST("/:id/photo-reports", middleware.RequirePermission("construction", "reports", "submit"), h.CreatePhotoReport)

		// Daily Reports
		constructionProjects.GET("/:id/daily-reports", h.ListDailyReports)
		constructionProjects.POST("/:id/daily-reports", middleware.RequirePermission("construction", "reports", "submit"), h.CreateDailyReport)

		// Material Requests
		constructionProjects.GET("/:id/material-requests", h.ListMaterialRequests)
		constructionProjects.POST("/:id/material-requests", middleware.RequirePermission("construction", "projects", "update"), h.CreateMaterialRequest)

		// Deliveries
		constructionProjects.GET("/:id/deliveries", h.ListDeliveries)

		// Site Warehouses
		constructionProjects.GET("/:id/warehouses", h.ListSiteWarehouses)
		constructionProjects.POST("/:id/warehouses", middleware.RequirePermission("construction", "projects", "update"), h.CreateSiteWarehouse)

		// Team Members
		constructionProjects.GET("/:id/team", h.ListConstructionTeamMembers)
		constructionProjects.POST("/:id/team", middleware.RequirePermission("construction", "project", "update"), h.CreateConstructionTeamMember)
		constructionProjects.DELETE("/:id/team/:memberId", middleware.RequirePermission("construction", "project", "update"), h.DeleteConstructionTeamMember)

		// WBS (Work Breakdown Structure)
		constructionProjects.GET("/:id/wbs", h.ListWBSItems)
		constructionProjects.GET("/:id/wbs/tree", h.GetWBSTree)
		constructionProjects.POST("/:id/wbs", middleware.RequirePermission("construction", "wbs", "create"), h.CreateWBSItem)

		// Activity Log
		constructionProjects.GET("/:id/activity-log", h.ListConstructionActivityLog)

		// Estimates
		constructionProjects.GET("/:id/estimates", h.ListEstimates)
		constructionProjects.POST("/:id/estimates", middleware.RequirePermission("construction", "estimate", "create"), h.CreateEstimate)

		// Daily Logs (WBS-linked)
		constructionProjects.GET("/:id/daily-logs", h.ListConstructionDailyLogs)
		constructionProjects.POST("/:id/daily-logs", middleware.RequirePermission("construction", "daily_log", "create"), h.CreateConstructionDailyLog)

		// Stages
		constructionProjects.GET("/:id/stages", h.ListConstructionStages)
		constructionProjects.POST("/:id/stages", middleware.RequirePermission("construction", "project", "update"), h.CreateConstructionStage)

		// Expense Lines (Xarajat operatsiyalari)
		constructionProjects.GET("/:id/expenses", h.ListExpenseLines)
		constructionProjects.POST("/:id/expenses", middleware.RequirePermission("construction", "project", "update"), h.CreateExpenseLine)

		// Reports
		constructionProjects.GET("/:id/reports/summary", h.GetProjectSummaryReport)
		constructionProjects.GET("/:id/reports/budget", h.GetStageBudgetReport)
		constructionProjects.GET("/:id/reports/materials", h.GetMaterialsReport)
		constructionProjects.GET("/:id/reports/journal-entries", h.GetJournalEntriesReport)

		// Commission (complete project)
		constructionProjects.PUT("/:id/commission", middleware.RequirePermission("construction", "project", "update"), h.CommissionProject)
	}

	// Project Vendors (direct access for update/delete)
	projectVendors := rg.Group("/construction/project-vendors")
	projectVendors.Use(middleware.RequirePermission("construction", "projects", "read"))
	{
		projectVendors.PUT("/:id", middleware.RequirePermission("construction", "projects", "update"), h.UpdateProjectVendor)
		projectVendors.DELETE("/:id", middleware.RequirePermission("construction", "projects", "delete"), h.DeleteProjectVendor)
	}

	// Smeta Sections (direct access)
	smetaSections := rg.Group("/construction/sections")
	smetaSections.Use(middleware.RequirePermission("construction", "smeta", "read"))
	{
		smetaSections.PUT("/:id", middleware.RequirePermission("construction", "smeta", "update"), h.UpdateSmetaSection)
		smetaSections.DELETE("/:id", middleware.RequirePermission("construction", "smeta", "delete"), h.DeleteSmetaSection)
		// Smeta Items
		smetaSections.GET("/:id/items", h.ListSmetaItems)
		smetaSections.POST("/:id/items", middleware.RequirePermission("construction", "smeta", "create"), h.CreateSmetaItem)
	}

	// Smeta Items (direct access)
	smetaItems := rg.Group("/construction/smeta-items")
	smetaItems.Use(middleware.RequirePermission("construction", "smeta", "read"))
	{
		smetaItems.PUT("/:id", middleware.RequirePermission("construction", "smeta", "update"), h.UpdateSmetaItem)
		smetaItems.DELETE("/:id", middleware.RequirePermission("construction", "smeta", "delete"), h.DeleteSmetaItem)
	}

	// Material Requests (direct access)
	materialRequests := rg.Group("/construction/material-requests")
	materialRequests.Use(middleware.RequirePermission("construction", "projects", "read"))
	{
		materialRequests.PUT("/:id", middleware.RequirePermission("construction", "projects", "update"), h.UpdateMaterialRequest)
		materialRequests.PUT("/:id/approve", middleware.RequirePermission("construction", "projects", "update"), h.ApproveMaterialRequest)
		materialRequests.DELETE("/:id", middleware.RequirePermission("construction", "projects", "delete"), h.DeleteMaterialRequest)
	}

	// Daily Reports (direct access)
	dailyReports := rg.Group("/construction/daily-reports")
	dailyReports.Use(middleware.RequirePermission("construction", "reports", "read"))
	{
		dailyReports.GET("/:id", h.GetDailyReport)
		dailyReports.PUT("/:id", middleware.RequirePermission("construction", "reports", "submit"), h.UpdateDailyReport)
		dailyReports.DELETE("/:id", middleware.RequirePermission("construction", "reports", "submit"), h.DeleteDailyReport)
	}

	// WBS Items (direct access for update/delete)
	wbsItems := rg.Group("/construction/wbs")
	wbsItems.Use(middleware.RequirePermission("construction", "wbs", "read"))
	{
		wbsItems.PUT("/:id", middleware.RequirePermission("construction", "wbs", "update"), h.UpdateWBSItem)
		wbsItems.DELETE("/:id", middleware.RequirePermission("construction", "wbs", "delete"), h.DeleteWBSItem)
		wbsItems.POST("/reorder", middleware.RequirePermission("construction", "wbs", "update"), h.ReorderWBSItems)
	}

	// Estimates (direct access)
	estimates := rg.Group("/construction/estimates")
	estimates.Use(middleware.RequirePermission("construction", "estimate", "read"))
	{
		estimates.GET("/:id", h.GetEstimate)
		estimates.PUT("/:id", middleware.RequirePermission("construction", "estimate", "update"), h.UpdateEstimate)
		estimates.DELETE("/:id", middleware.RequirePermission("construction", "estimate", "delete"), h.DeleteEstimate)
		estimates.POST("/:id/approve", middleware.RequirePermission("construction", "estimate", "update"), h.ApproveEstimate)
		estimates.POST("/:id/duplicate", middleware.RequirePermission("construction", "estimate", "create"), h.DuplicateEstimate)
		// Estimate Lines
		estimates.GET("/:id/lines", h.ListEstimateLines)
		estimates.POST("/:id/lines", middleware.RequirePermission("construction", "estimate", "update"), h.CreateEstimateLine)
		estimates.PUT("/:id/lines/:line_id", middleware.RequirePermission("construction", "estimate", "update"), h.UpdateEstimateLine)
		estimates.DELETE("/:id/lines/:line_id", middleware.RequirePermission("construction", "estimate", "update"), h.DeleteEstimateLine)
	}

	// Construction Daily Logs (direct access)
	constructionDailyLogs := rg.Group("/construction/daily-logs")
	constructionDailyLogs.Use(middleware.RequirePermission("construction", "daily_log", "read"))
	{
		constructionDailyLogs.PUT("/:id", middleware.RequirePermission("construction", "daily_log", "update"), h.UpdateConstructionDailyLog)
		constructionDailyLogs.DELETE("/:id", middleware.RequirePermission("construction", "daily_log", "delete"), h.DeleteConstructionDailyLog)
	}

	// Construction Stages (direct access for update/delete)
	constructionStages := rg.Group("/construction/stages")
	constructionStages.Use(middleware.RequirePermission("construction", "project", "read"))
	{
		constructionStages.PUT("/:id", middleware.RequirePermission("construction", "project", "update"), h.UpdateConstructionStage)
		constructionStages.DELETE("/:id", middleware.RequirePermission("construction", "project", "delete"), h.DeleteConstructionStage)
		constructionStages.GET("/:id/sub-stages", h.ListConstructionSubStages)
		constructionStages.POST("/:id/sub-stages", middleware.RequirePermission("construction", "project", "update"), h.CreateConstructionSubStage)
	}

	// Construction Sub-Stages (direct access for update/delete)
	constructionSubStages := rg.Group("/construction/sub-stages")
	constructionSubStages.Use(middleware.RequirePermission("construction", "project", "read"))
	{
		constructionSubStages.PUT("/:id", middleware.RequirePermission("construction", "project", "update"), h.UpdateConstructionSubStage)
		constructionSubStages.DELETE("/:id", middleware.RequirePermission("construction", "project", "delete"), h.DeleteConstructionSubStage)
	}

	// Expense Lines (direct access for update/delete/approve/cancel)
	constructionExpenses := rg.Group("/construction/expenses")
	constructionExpenses.Use(middleware.RequirePermission("construction", "project", "read"))
	{
		constructionExpenses.PUT("/:id", middleware.RequirePermission("construction", "project", "update"), h.UpdateExpenseLine)
		constructionExpenses.DELETE("/:id", middleware.RequirePermission("construction", "project", "update"), h.DeleteExpenseLine)
		constructionExpenses.PUT("/:id/approve", middleware.RequirePermission("construction", "project", "update"), h.ApproveExpenseLine)
		constructionExpenses.PUT("/:id/cancel", middleware.RequirePermission("construction", "project", "update"), h.CancelExpenseLine)
	}

	// Cost Categories & Account Mapping (settings)
	constructionSettings := rg.Group("/construction")
	constructionSettings.Use(middleware.RequirePermission("construction", "project", "read"))
	{
		constructionSettings.GET("/cost-categories", h.ListCostCategories)
		constructionSettings.POST("/cost-categories", middleware.RequirePermission("construction", "project", "update"), h.CreateCostCategory)
		constructionSettings.PUT("/cost-categories/:id", middleware.RequirePermission("construction", "project", "update"), h.UpdateCostCategory)
		constructionSettings.GET("/account-mapping", h.GetAccountMapping)
		constructionSettings.PUT("/account-mapping", middleware.RequirePermission("construction", "project", "update"), h.UpsertAccountMapping)
		// Portfolio dashboard (cross-project)
		constructionSettings.GET("/dashboard", h.GetConstructionPortfolioDashboard)
	}

	// Photo Reports (direct access)
	photoReports := rg.Group("/construction/photo-reports")
	photoReports.Use(middleware.RequirePermission("construction", "reports", "read"))
	{
		photoReports.GET("/:id", h.GetPhotoReport)
		photoReports.PUT("/:id", middleware.RequirePermission("construction", "reports", "submit"), h.UpdatePhotoReport)
		photoReports.DELETE("/:id", middleware.RequirePermission("construction", "reports", "submit"), h.DeletePhotoReport)
	}

	// =====================================================
	// INSTALLED APPS ROUTES
	// =====================================================
	installedApps := rg.Group("/installed-apps")
	{
		installedApps.GET("", h.ListInstalledApps)
		installedApps.POST("", h.InstallApp)
		installedApps.GET("/:app_id", h.GetInstalledApp)
		installedApps.PUT("/:app_id", h.UpdateInstalledApp)
		installedApps.DELETE("/:app_id", h.UninstallApp)
	}

	// =====================================================
	// ACTIVITY LOG / CHATTER ROUTES
	// =====================================================
	activityLogs := rg.Group("/activity")
	{
		// Activity logs for any record
		activityLogs.GET("/:model/:record_id/logs", h.ListActivityLogs)

		// Comments (Chatter)
		activityLogs.GET("/:model/:record_id/comments", h.ListComments)
		activityLogs.POST("/:model/:record_id/comments", h.CreateComment)
		activityLogs.DELETE("/comments/:comment_id", h.DeleteComment)

		// Scheduled Activities
		activityLogs.GET("/:model/:record_id/scheduled", h.ListScheduledActivities)
		activityLogs.POST("/:model/:record_id/scheduled", h.CreateScheduledActivity)
		activityLogs.PUT("/scheduled/:activity_id/complete", h.CompleteScheduledActivity)

		// My Activities (all pending activities for current user)
		activityLogs.GET("/my-activities", h.ListMyActivities)
	}

	// =====================================================
	// POINT OF SALE (POS) ROUTES
	// =====================================================
	pos := rg.Group("/pos")
	pos.Use(middleware.RequirePermission("sales", "order", "read"))
	{
		// POS Configuration
		pos.GET("/configs", h.ListPOSConfigs)
		pos.POST("/configs", middleware.RequirePermission("sales", "order", "create"), h.CreatePOSConfig)
		pos.PUT("/configs/:id", middleware.RequirePermission("sales", "order", "update"), h.UpdatePOSConfig)

		// POS Sessions
		pos.GET("/sessions", h.ListPOSSessions)
		pos.POST("/sessions/open", h.OpenPOSSession)
		pos.GET("/sessions/current", h.GetCurrentPOSSession)
		pos.POST("/sessions/:id/close", h.ClosePOSSession)
		pos.GET("/sessions/:id/summary", h.GetSessionSummary)

		// POS Orders
		pos.GET("/orders", h.ListPOSOrders)
		pos.POST("/orders", middleware.RequirePermission("sales", "order", "create"), h.CreatePOSOrder)
		pos.GET("/orders/:id", h.GetPOSOrder)

		// POS Products (optimized search)
		pos.GET("/products", h.SearchPOSProducts)
	}

	// =====================================================
	// INTERCOMPANY TRANSFERS MODULE ROUTES
	// =====================================================

	// Intercompany Transfers (Product)
	icTransfers := rg.Group("/intercompany-transfers")
	icTransfers.Use(middleware.RequirePermission("inventory", "stock", "read"))
	{
		icTransfers.GET("", h.ListIntercompanyTransfers)
		icTransfers.POST("", middleware.RequirePermission("inventory", "stock", "create"), h.CreateIntercompanyTransfer)
		icTransfers.GET("/:id", h.GetIntercompanyTransfer)
		icTransfers.PUT("/:id", middleware.RequirePermission("inventory", "stock", "update"), h.UpdateIntercompanyTransfer)
		icTransfers.DELETE("/:id", middleware.RequirePermission("inventory", "stock", "delete"), h.DeleteIntercompanyTransfer)
		icTransfers.POST("/:id/approve", middleware.RequirePermission("inventory", "stock", "transfer"), h.ApproveIntercompanyTransfer)
		icTransfers.POST("/:id/ship", middleware.RequirePermission("inventory", "stock", "transfer"), h.ShipIntercompanyTransfer)
		icTransfers.POST("/:id/receive", middleware.RequirePermission("inventory", "stock", "transfer"), h.ReceiveIntercompanyTransfer)
	}

	// Intercompany Payments (Money)
	icPayments := rg.Group("/intercompany-payments")
	icPayments.Use(middleware.RequirePermission("finance", "payment", "read"))
	{
		icPayments.GET("", h.ListIntercompanyPayments)
		icPayments.POST("", middleware.RequirePermission("finance", "payment", "create"), h.CreateIntercompanyPayment)
		icPayments.GET("/:id", h.GetIntercompanyPayment)
		icPayments.POST("/:id/approve", middleware.RequirePermission("finance", "payment", "approve"), h.ApproveIntercompanyPayment)
		icPayments.DELETE("/:id", middleware.RequirePermission("finance", "payment", "delete"), h.DeleteIntercompanyPayment)
	}

	// Intercompany Balances
	rg.GET("/intercompany-balances", middleware.RequirePermission("finance", "account", "read"), h.GetIntercompanyBalances)

	// Intercompany Rules (Odoo-like configuration)
	icRules := rg.Group("/intercompany-rules")
	icRules.Use(middleware.RequirePermission("settings", "organization", "read"))
	{
		icRules.GET("", h.ListIntercompanyRules)
		icRules.POST("", middleware.RequirePermission("settings", "organization", "create"), h.CreateIntercompanyRule)
		icRules.GET("/:id", h.GetIntercompanyRule)
		icRules.PUT("/:id", middleware.RequirePermission("settings", "organization", "update"), h.UpdateIntercompanyRule)
		icRules.DELETE("/:id", middleware.RequirePermission("settings", "organization", "delete"), h.DeleteIntercompanyRule)
	}

	// Intercompany Transaction Logs (Audit Trail)
	rg.GET("/intercompany-logs", middleware.RequirePermission("settings", "organization", "read"), h.ListIntercompanyTransactionLogs)
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
