package handler

import (
	"context"

	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/crm"
	"github.com/genixerp/genix-backend/internal/infrastructure/cache"
	"github.com/genixerp/genix-backend/internal/infrastructure/database"
	"github.com/genixerp/genix-backend/internal/infrastructure/email"
	"github.com/genixerp/genix-backend/internal/infrastructure/payment"
	"github.com/genixerp/genix-backend/internal/infrastructure/sms"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/genixerp/genix-backend/internal/pkg/logger"
	"github.com/genixerp/genix-backend/internal/pkg/push"
	"github.com/genixerp/genix-backend/internal/service"
	"github.com/gin-gonic/gin"
)

// Handler holds all handler dependencies
type Handler struct {
	db              *database.DB
	redis           *cache.RedisClient
	config          *config.Config
	log             logger.Logger
	jwtManager      *crypto.JWTManager
	emailService    *email.Service
	smsService      *sms.Service
	icSync          *service.IntercompanySyncService
	perm            *middleware.PermissionChecker
	multicardClient *payment.Client
	// crmSync pushes construction photo reports into the Yuksalish CRM.
	// nil-safe at the call sites (see CreatePhotoReport): if env vars
	// aren't set, syncer is constructed but reports itself as
	// not-configured and every Enqueue is a no-op.
	crmSync *crm.Syncer
	// fcm sends mobile push notifications. Always non-nil; when FCM isn't
	// configured it reports Enabled()==false and every Send is a no-op.
	fcm *push.Sender
}

// NewHandler creates a new handler instance
func NewHandler(db *database.DB, redis *cache.RedisClient, cfg *config.Config, log logger.Logger) *Handler {
	// Build the FCM sender once. On a bad credentials JSON we log and fall back
	// to a disabled sender so a push misconfig never blocks server startup.
	fcmSender, err := push.NewSender(context.Background(), cfg.FCM.CredentialsJSON, cfg.FCM.ProjectID)
	if err != nil {
		log.Error("FCM sender init failed; push disabled", "error", err)
		fcmSender = &push.Sender{}
	}
	if fcmSender.Enabled() {
		log.Info("FCM push enabled")
	}

	return &Handler{
		db:           db,
		redis:        redis,
		config:       cfg,
		log:          log,
		jwtManager:   crypto.NewJWTManager(cfg.JWT),
		emailService: email.NewService(&cfg.Email),
		smsService:   sms.NewService(&cfg.SMS),
		icSync:       service.NewIntercompanySyncService(db.DB),
		perm:         middleware.NewPermissionChecker(db, redis, log),
		multicardClient: payment.NewClient(
			cfg.Multicard.ApplicationID,
			cfg.Multicard.Secret,
			cfg.Multicard.StoreID,
			cfg.Multicard.BaseURL,
		),
		crmSync: crm.NewSyncer(db.DB, log),
		fcm:     fcmSender,
	}
}

// CRMSyncer exposes the syncer to main() for starting the background
// retry worker.
func (h *Handler) CRMSyncer() *crm.Syncer { return h.crmSync }

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
		protected.Use(middleware.TrialCheck(h.db))
		h.registerProtectedRoutes(protected)
	}

	// Tender platform routes (separate module with own auth flow)
	h.RegisterTenderRoutes(r)
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
		auth.POST("/send-phone-otp", h.SendPasswordResetOTP)         // Send OTP via SMS for password reset
		auth.POST("/verify-phone-otp", h.VerifyPasswordResetOTP)     // Verify phone OTP for password reset
		auth.POST("/reset-password-phone", h.ResetPasswordWithPhone) // Reset password with phone OTP
		auth.POST("/verify-email", h.VerifyEmail)
		auth.GET("/validate-invite", h.ValidateInvite) // Public - validate invite token
		auth.POST("/accept-invite", h.AcceptInvite)    // Public - accept invite and set password
	}

	// Public info
	rg.GET("/info", h.GetAPIInfo)

	// Mobile app version gate (public - the app checks this on launch, before login)
	rg.GET("/mobile/version", h.CheckMobileVersion)

	// Contact form (public - no auth needed)
	rg.POST("/contact", h.SubmitContactForm)

	// Public file access (for serving images in <img> tags)
	rg.GET("/files/:id", h.GetFile)

	// Public shared reconciliation act (no auth — accessed via share token)
	rg.GET("/shared/reconciliation/:token", h.GetPublicReconciliationAct)
	rg.POST("/shared/reconciliation/:token/respond", h.RespondReconciliationAct)

	// PBX Webhook (public - called by OnlinePBX)
	rg.GET("/webhooks/pbx", h.PBXWebhook)
	rg.POST("/webhooks/pbx", h.PBXWebhook)

	// Multicard payment webhook (public - called by Multicard servers)
	rg.POST("/webhooks/multicard", h.MulticardWebhook)

	// Public lead capture from external websites (embed script)
	rg.POST("/public/leads", h.PublicCreateLead)
}

// registerProtectedRoutes registers routes that require authentication
func (h *Handler) registerProtectedRoutes(rg *gin.RouterGroup) {
	// Subscription / trial status (allowed even when trial expired — TrialCheck skips these)
	subscription := rg.Group("/subscription")
	{
		subscription.GET("/status", h.GetSubscriptionStatus)
		subscription.POST("/activate", h.ActivateSubscription)
		subscription.POST("/checkout", h.CreateCheckout)
		subscription.GET("/plans", h.GetPlans)
		subscription.GET("/payments", h.GetPaymentHistory)
		subscription.POST("/verify-payment", h.VerifyPayment)
	}

	// Authentication (protected)
	auth := rg.Group("/auth")
	{
		auth.POST("/logout", h.Logout)
		auth.GET("/me", h.GetCurrentUser)
		auth.PUT("/me", h.UpdateCurrentUser)
		auth.PUT("/me/password", h.ChangePassword)
		auth.PUT("/me/email", h.UpdateCurrentUserEmail)
		auth.POST("/me/phone/send-otp", h.SendPhoneOTP)
		auth.POST("/me/phone/verify-otp", h.VerifyPhoneOTP)
		auth.POST("/send-invite", h.SendInvite)                  // Send invitation to a user
		auth.GET("/me/permissions", h.GetCurrentUserPermissions) // Get current user's module permissions
		auth.GET("/me/organizations", h.GetCurrentUserOrganizations)
	}

	// Send credentials — HR workers need this without requiring users:user:read
	rg.POST("/send-credentials", h.perm.Require("hr", "employee", "update"), h.SendCredentials)

	// Users
	users := rg.Group("/users")
	users.Use(h.perm.Require("users", "user", "read"))
	{
		users.GET("", h.ListUsers)
		users.POST("", h.perm.Require("users", "user", "create"), h.CreateUser)
		users.GET("/:id", h.GetUser)
		users.PUT("/:id", h.perm.Require("users", "user", "update"), h.UpdateUser)
		users.DELETE("/:id", h.perm.Require("users", "user", "delete"), h.DeleteUser)
		users.POST("/:id/roles", h.perm.Require("users", "role", "assign"), h.AssignRoles)
	}

	// Roles
	roles := rg.Group("/roles")
	roles.Use(h.perm.Require("users", "role", "read"))
	{
		roles.GET("", h.ListRoles)
		roles.POST("", h.perm.Require("users", "role", "create"), h.CreateRole)
		roles.GET("/:id", h.GetRole)
		roles.PUT("/:id", h.perm.Require("users", "role", "update"), h.UpdateRole)
		roles.DELETE("/:id", h.perm.Require("users", "role", "delete"), h.DeleteRole)
		roles.GET("/:id/employees", h.GetRoleEmployees)
		roles.POST("/:id/assign", h.perm.Require("users", "role", "update"), h.AssignRole)
		roles.POST("/:id/unassign", h.perm.Require("users", "role", "update"), h.UnassignRole)
	}

	// Permissions
	rg.GET("/permissions", h.ListPermissions)

	// Organizations
	// NOTE: GET endpoints are NOT permission-gated. CompanyContext on the
	// frontend calls `GET /organizations` on app startup; without it
	// `activeCompany` is null and basically every other page breaks. The
	// user is already JWT-scoped to their tenant, so they can only ever
	// see their own tenant's orgs. Mutations remain gated.
	orgs := rg.Group("/organizations")
	{
		orgs.GET("", h.ListOrganizations)
		orgs.POST("", h.perm.Require("organization", "organization", "create"), h.CreateOrganization)
		orgs.POST("/import", h.perm.Require("organization", "organization", "create"), h.ImportOrganizations)
		orgs.GET("/:id", h.GetOrganization)
		orgs.PUT("/:id", h.perm.Require("organization", "organization", "update"), h.UpdateOrganization)
		orgs.DELETE("/:id", h.perm.Require("organization", "organization", "delete"), h.DeleteOrganization)
		// Initialize default accounts for organization
		orgs.POST("/:id/initialize-accounts", h.perm.Require("organization", "organization", "update"), h.InitializeOrganizationAccounts)
		// Restore (un-delete) all soft-deleted accounts for an organization.
		orgs.POST("/:id/restore-accounts", h.perm.Require("organization", "organization", "update"), h.RestoreOrganizationAccounts)
		// Organization employees
		orgs.GET("/:id/employees", h.ListOrganizationEmployees)
	}

	// Departments
	depts := rg.Group("/departments")
	depts.Use(h.perm.Require("hr", "employee", "read"))
	{
		depts.GET("", h.ListDepartments)
		depts.POST("", h.perm.Require("hr", "employee", "create"), h.CreateDepartment)
		depts.GET("/:id", h.GetDepartment)
		depts.PUT("/:id", h.perm.Require("hr", "employee", "update"), h.UpdateDepartment)
		depts.DELETE("/:id", h.perm.Require("hr", "employee", "delete"), h.DeleteDepartment)
	}

	// Job Positions
	jobPositions := rg.Group("/job-positions")
	jobPositions.Use(h.perm.Require("hr", "employee", "read"))
	{
		jobPositions.GET("", h.ListJobPositions)
		jobPositions.POST("", h.perm.Require("hr", "employee", "create"), h.CreateJobPosition)
		jobPositions.GET("/:id", h.GetJobPosition)
		jobPositions.PUT("/:id", h.perm.Require("hr", "employee", "update"), h.UpdateJobPosition)
		jobPositions.DELETE("/:id", h.perm.Require("hr", "employee", "delete"), h.DeleteJobPosition)
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
		// CRM "create customer + sales order + manufacture order" in one save,
		// and the customer's sales history (links to sales detail on the front-end).
		contacts.POST("/full-order", h.CreateCustomerWithFullOrder)
		contacts.GET("/:id/sales", h.ListContactSalesHistory)
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

	// PBX Integration (OnlinePBX)
	pbx := rg.Group("/pbx")
	{
		pbx.GET("/config", h.GetPBXConfig)
		pbx.POST("/config", h.SavePBXConfig)
		pbx.POST("/test-connection", h.TestPBXConnection)
		pbx.POST("/call", h.InitiateCall)
	}

	// Leads (CRM Lead Management)
	leads := rg.Group("/leads")
	{
		leads.GET("", h.ListLeads)
		leads.POST("", h.CreateLead)
		leads.GET("/stats", h.GetLeadStats)
		leads.GET("/:id", h.GetLead)
		leads.GET("/:id/audit-logs", h.GetLeadAuditLogs)
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
	products.Use(h.perm.Require("inventory", "product", "read"))
	{
		products.GET("", h.ListProducts)
		products.POST("", h.perm.Require("inventory", "product", "create"), h.CreateProduct)
		products.POST("/bulk", h.perm.Require("inventory", "product", "create"), h.BulkCreateProducts)
		products.GET("/by-search-key", h.FindProductsBySearchKey)
		products.GET("/:id", h.GetProduct)
		products.PUT("/:id", h.perm.Require("inventory", "product", "update"), h.UpdateProduct)
		products.POST("/:id/generate-search-key", h.perm.Require("inventory", "product", "update"), h.GenerateProductSearchKey)
		products.DELETE("/:id", h.perm.Require("inventory", "product", "delete"), h.DeleteProduct)
		products.GET("/:id/packaging-materials", h.ListProductPackagingMaterials)
		products.POST("/:id/packaging-materials", h.SaveProductPackagingMaterials)
		products.DELETE("/:id/packaging-materials/:materialId", h.DeleteProductPackagingMaterial)
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
	warehouses.Use(h.perm.Require("inventory", "warehouse", "manage"))
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
	locations.Use(h.perm.Require("inventory", "warehouse", "read"))
	{
		locations.GET("", h.ListAllLocations)
	}

	// Warehouse Operation Types
	operationTypes := rg.Group("/operation-types")
	operationTypes.Use(h.perm.Require("inventory", "warehouse", "read"))
	{
		operationTypes.GET("", h.ListOperationTypes)
		operationTypes.GET("/:id", h.GetOperationType)
		operationTypes.POST("", h.perm.Require("inventory", "warehouse", "create"), h.CreateOperationType)
		operationTypes.PUT("/:id", h.perm.Require("inventory", "warehouse", "update"), h.UpdateOperationType)
		operationTypes.DELETE("/:id", h.perm.Require("inventory", "warehouse", "delete"), h.DeleteOperationType)
	}

	// Carriers
	carriers := rg.Group("/carriers")
	carriers.Use(h.perm.Require("inventory", "carrier", "read"))
	{
		carriers.GET("", h.ListCarriers)
		carriers.POST("", h.perm.Require("inventory", "carrier", "create"), h.CreateCarrier)
		carriers.GET("/:id", h.GetCarrier)
		carriers.PUT("/:id", h.perm.Require("inventory", "carrier", "update"), h.UpdateCarrier)
		carriers.DELETE("/:id", h.perm.Require("inventory", "carrier", "delete"), h.DeleteCarrier)
	}

	// Inventory
	inventory := rg.Group("/inventory")
	inventory.Use(h.perm.Require("inventory", "stock", "read"))
	{
		inventory.GET("", h.ListInventory)
		inventory.GET("/summary", h.GetInventorySummary)
		inventory.POST("/adjust", h.perm.Require("inventory", "stock", "adjust"), h.AdjustInventory)
		inventory.POST("/transfer", h.perm.Require("inventory", "stock", "transfer"), h.TransferInventory)
		inventory.GET("/movements", h.ListInventoryMovements)
		inventory.GET("/valuation", h.GetInventoryValuation)
		inventory.GET("/cogs", h.GetCOGSData)
		// As-of date stock report — replays inventory_transactions up to
		// the chosen date so the user can see what each warehouse held
		// (incl. soft-deleted SKUs) on any historical day.
		inventory.GET("/stock-at-date", h.GetStockAtDate)
		// Per-(product, warehouse) turnover for a date range: opening
		// balance, kirim/chiqim within the period, and closing balance +
		// weighted-average cost. Powers the "Ombor holati" report sub-tab.
		inventory.GET("/turnover", h.GetInventoryTurnover)

		// Inventory Lots (Partiyalar) - lots are created automatically during Goods Receipt
		inventory.GET("/lots", h.ListInventoryLots)
		inventory.GET("/lots/:id", h.GetInventoryLot)
		inventory.PUT("/lots/:id", h.perm.Require("inventory", "stock", "update"), h.UpdateInventoryLot)
		inventory.DELETE("/lots/:id", h.perm.Require("inventory", "stock", "delete"), h.DeleteInventoryLot)
	}

	// Stock Counts (Inventarizatsiya)
	stockCounts := rg.Group("/stock-counts")
	stockCounts.Use(h.perm.Require("inventory", "stock", "read"))
	{
		stockCounts.GET("", h.ListStockCounts)
		stockCounts.POST("", h.perm.Require("inventory", "stock", "create"), h.CreateStockCount)
		stockCounts.GET("/:id", h.GetStockCount)
		stockCounts.POST("/:id/record", h.perm.Require("inventory", "stock", "adjust"), h.RecordCountLine)
		stockCounts.POST("/:id/complete", h.perm.Require("inventory", "stock", "adjust"), h.CompleteStockCount)
		stockCounts.DELETE("/:id", h.perm.Require("inventory", "stock", "delete"), h.DeleteStockCount)
	}

	// Stock Count Lines — shortage assignment
	stockCountLines := rg.Group("/stock-count-lines")
	stockCountLines.Use(h.perm.Require("inventory", "stock", "read"))
	{
		stockCountLines.POST("/:id/assign", h.perm.Require("inventory", "stock", "adjust"), h.AssignResponsible)
	}

	// Employee Deductions (inventory shortage → payroll bridge)
	edDeductions := rg.Group("/employee-deductions")
	edDeductions.Use(h.perm.Require("hr", "employee", "read"))
	{
		edDeductions.GET("", h.ListAllDeductions)
	}

	// Stock Operations (TT: Receipt, Delivery, Internal Transfer, Write-off)
	stockOps := rg.Group("/stock-operations")
	stockOps.Use(h.perm.Require("inventory", "stock", "read"))
	{
		stockOps.GET("", h.ListStockOperations)
		stockOps.GET("/summary", h.GetStockOperationSummary)
		stockOps.POST("", h.perm.Require("inventory", "stock", "create"), h.CreateStockOperation)
		stockOps.GET("/:id", h.GetStockOperation)
		stockOps.PUT("/:id", h.perm.Require("inventory", "stock", "update"), h.UpdateStockOperation)
		stockOps.PUT("/:id/lines", h.perm.Require("inventory", "stock", "update"), h.UpdateStockOperationLines)
		stockOps.POST("/:id/lines", h.perm.Require("inventory", "stock", "update"), h.AddStockOperationLine)
		stockOps.DELETE("/:id/lines/:lineId", h.perm.Require("inventory", "stock", "update"), h.DeleteStockOperationLine)
		stockOps.POST("/:id/validate", h.perm.Require("inventory", "stock", "update"), h.ValidateStockOperation)
		stockOps.POST("/:id/advance", h.perm.Require("inventory", "stock", "update"), h.AdvanceStockOperationStep)
		stockOps.POST("/:id/backorder", h.perm.Require("inventory", "stock", "create"), h.CreateBackorder)
		stockOps.POST("/:id/cancel", h.perm.Require("inventory", "stock", "update"), h.CancelStockOperation)
		stockOps.POST("/:id/approve", h.perm.Require("inventory", "stock", "update"), h.ApproveStockOperationStep)
		stockOps.POST("/:id/reject", h.perm.Require("inventory", "stock", "update"), h.RejectStockOperationStep)
		stockOps.POST("/:id/steps/:step/documents", h.perm.Require("inventory", "stock", "update"), h.AddStepDocument)
	}

	// Operation Type Steps (per operation type configuration)
	operationTypes.GET("/:id/steps", h.ListOperationTypeSteps)
	operationTypes.PUT("/:id/steps", h.perm.Require("inventory", "warehouse", "update"), h.SaveOperationTypeSteps)

	// Warehouse Alerts Dashboard
	rg.GET("/warehouse-alerts", h.perm.Require("inventory", "stock", "read"), h.GetWarehouseAlerts)

	// Product Attributes
	productAttributes := rg.Group("/product-attributes")
	productAttributes.Use(h.perm.Require("inventory", "product_attribute", "read"))
	{
		productAttributes.GET("", h.ListProductAttributes)
		productAttributes.POST("", h.perm.Require("inventory", "product_attribute", "create"), h.CreateProductAttribute)
		productAttributes.PUT("/:id", h.perm.Require("inventory", "product_attribute", "update"), h.UpdateProductAttribute)
		productAttributes.DELETE("/:id", h.perm.Require("inventory", "product_attribute", "delete"), h.DeleteProductAttribute)
		productAttributes.POST("/:id/values", h.perm.Require("inventory", "product_attribute", "update"), h.CreateAttributeValue)
		productAttributes.DELETE("/:id/values/:valueId", h.perm.Require("inventory", "product_attribute", "delete"), h.DeleteAttributeValue)
	}

	// Product Variants
	productVariants := rg.Group("/product-variants")
	productVariants.Use(h.perm.Require("inventory", "product_variant", "read"))
	{
		productVariants.GET("", h.ListProductVariants)
		productVariants.POST("", h.perm.Require("inventory", "product_variant", "create"), h.CreateProductVariant)
		productVariants.GET("/:id", h.GetProductVariant)
		productVariants.PUT("/:id", h.perm.Require("inventory", "product_variant", "update"), h.UpdateProductVariant)
		productVariants.DELETE("/:id", h.perm.Require("inventory", "product_variant", "delete"), h.DeleteProductVariant)
		productVariants.POST("/generate", h.perm.Require("inventory", "product_variant", "create"), h.GenerateProductVariants)
	}

	// Product Template Attributes (link attributes to products)
	rg.POST("/products/:id/attributes", h.perm.Require("inventory", "product", "update"), h.AddProductAttribute)
	rg.GET("/products/:id/attributes", h.perm.Require("inventory", "product", "read"), h.GetProductTemplateAttributes)

	// Product Packagings (Odoo-style - how products are sold/purchased in bulk)
	productPackagings := rg.Group("/product-packagings")
	productPackagings.Use(h.perm.Require("inventory", "product", "read"))
	{
		productPackagings.GET("", h.ListProductPackagings)
		productPackagings.POST("", h.perm.Require("inventory", "product", "create"), h.CreateProductPackaging)
		productPackagings.GET("/:id", h.GetProductPackaging)
		productPackagings.PUT("/:id", h.perm.Require("inventory", "product", "update"), h.UpdateProductPackaging)
		productPackagings.DELETE("/:id", h.perm.Require("inventory", "product", "delete"), h.DeleteProductPackaging)
	}

	// Product Packagings by Product
	rg.GET("/products/:id/packagings", h.perm.Require("inventory", "product", "read"), h.ListProductPackagingsByProduct)
	rg.POST("/products/:id/packagings", h.perm.Require("inventory", "product", "update"), h.CreateProductPackagingForProduct)

	// Package Types (box sizes, pallets, containers)
	packageTypes := rg.Group("/package-types")
	packageTypes.Use(h.perm.Require("inventory", "warehouse", "read"))
	{
		packageTypes.GET("", h.ListPackageTypes)
		packageTypes.POST("", h.perm.Require("inventory", "warehouse", "create"), h.CreatePackageType)
		packageTypes.GET("/:id", h.GetPackageType)
		packageTypes.PUT("/:id", h.perm.Require("inventory", "warehouse", "update"), h.UpdatePackageType)
		packageTypes.DELETE("/:id", h.perm.Require("inventory", "warehouse", "delete"), h.DeletePackageType)
	}

	// Packages (physical packages in warehouse)
	packages := rg.Group("/packages")
	packages.Use(h.perm.Require("inventory", "warehouse", "read"))
	{
		packages.GET("", h.ListPackages)
		packages.POST("", h.perm.Require("inventory", "warehouse", "create"), h.CreatePackage)
		packages.GET("/:id", h.GetPackage)
		packages.PUT("/:id", h.perm.Require("inventory", "warehouse", "update"), h.UpdatePackage)
		packages.DELETE("/:id", h.perm.Require("inventory", "warehouse", "delete"), h.DeletePackage)
		packages.POST("/:id/contents", h.perm.Require("inventory", "warehouse", "update"), h.AddPackageContent)
		packages.DELETE("/:id/contents/:contentId", h.perm.Require("inventory", "warehouse", "update"), h.RemovePackageContent)
	}

	// Bill of Materials (BOM)
	bom := rg.Group("/bom")
	bom.Use(h.perm.Require("inventory", "bom", "read"))
	{
		bom.GET("", h.ListBOMs)
		bom.POST("", h.perm.Require("inventory", "bom", "create"), h.CreateBOM)
		bom.GET("/:id", h.GetBOM)
		bom.PUT("/:id", h.perm.Require("inventory", "bom", "update"), h.UpdateBOM)
		bom.DELETE("/:id", h.perm.Require("inventory", "bom", "delete"), h.DeleteBOM)
		bom.POST("/:id/lines", h.perm.Require("inventory", "bom", "update"), h.CreateBOMLine)
		bom.DELETE("/:id/lines/:lineId", h.perm.Require("inventory", "bom", "update"), h.DeleteBOMLine)
		// BOM Operations (routing)
		bom.GET("/:id/operations", h.ListBOMOperations)
		bom.POST("/:id/operations", h.perm.Require("inventory", "bom", "update"), h.CreateBOMOperation)
		bom.PUT("/:id/operations/:operationId", h.perm.Require("inventory", "bom", "update"), h.UpdateBOMOperation)
		bom.DELETE("/:id/operations/:operationId", h.perm.Require("inventory", "bom", "update"), h.DeleteBOMOperation)
	}

	// Alias route for /boms (plural) - frontend compatibility
	boms := rg.Group("/boms")
	boms.Use(h.perm.Require("inventory", "bom", "read"))
	{
		boms.GET("", h.ListBOMs)
		boms.POST("", h.perm.Require("inventory", "bom", "create"), h.CreateBOM)
		boms.GET("/:id", h.GetBOM)
		boms.PUT("/:id", h.perm.Require("inventory", "bom", "update"), h.UpdateBOM)
		boms.DELETE("/:id", h.perm.Require("inventory", "bom", "delete"), h.DeleteBOM)
		// BOM Lines (components)
		boms.POST("/:id/lines", h.perm.Require("inventory", "bom", "update"), h.CreateBOMLine)
		boms.DELETE("/:id/lines/:lineId", h.perm.Require("inventory", "bom", "update"), h.DeleteBOMLine)
		// BOM Operations (routing)
		boms.GET("/:id/operations", h.ListBOMOperations)
		boms.POST("/:id/operations", h.perm.Require("inventory", "bom", "update"), h.CreateBOMOperation)
		boms.PUT("/:id/operations/:operationId", h.perm.Require("inventory", "bom", "update"), h.UpdateBOMOperation)
		boms.DELETE("/:id/operations/:operationId", h.perm.Require("inventory", "bom", "update"), h.DeleteBOMOperation)
	}

	// Scrap Management
	scrap := rg.Group("/scrap")
	scrap.Use(h.perm.Require("inventory", "scrap", "read"))
	{
		scrap.GET("/reasons", h.ListScrapReasons)
		scrap.POST("/reasons", h.perm.Require("inventory", "scrap", "create"), h.CreateScrapReason)
		scrap.GET("/orders", h.ListScrapOrders)
		scrap.POST("/orders", h.perm.Require("inventory", "scrap", "create"), h.CreateScrapOrder)
		scrap.GET("/orders/:id", h.GetScrapOrder)
		scrap.POST("/orders/:id/confirm", h.perm.Require("inventory", "scrap", "approve"), h.ConfirmScrapOrder)
		scrap.POST("/orders/:id/cancel", h.CancelScrapOrder)
		scrap.GET("/summary", h.GetScrapSummary)
	}

	// Reorder Rules
	reorder := rg.Group("/reorder-rules")
	reorder.Use(h.perm.Require("inventory", "reorder", "read"))
	{
		reorder.GET("", h.ListReorderRules)
		reorder.POST("", h.perm.Require("inventory", "reorder", "create"), h.CreateReorderRule)
		reorder.GET("/alerts", h.GetReorderAlerts)
		reorder.GET("/:id", h.GetReorderRule)
		reorder.PUT("/:id", h.perm.Require("inventory", "reorder", "update"), h.UpdateReorderRule)
		reorder.DELETE("/:id", h.perm.Require("inventory", "reorder", "delete"), h.DeleteReorderRule)
	}

	// Replenishment
	replenishment := rg.Group("/replenishment")
	replenishment.Use(h.perm.Require("inventory", "reorder", "read"))
	{
		replenishment.GET("/preview", h.GetReplenishmentPreview)
		replenishment.POST("/run", h.perm.Require("purchase", "order", "create"), h.RunReplenishment)
	}

	// Quotations
	quotations := rg.Group("/quotations")
	quotations.Use(h.perm.Require("sales", "quotation", "read"))
	{
		quotations.GET("", h.ListQuotations)
		quotations.POST("", h.perm.Require("sales", "quotation", "create"), h.CreateQuotation)
		quotations.GET("/:id", h.GetQuotation)
		quotations.PUT("/:id", h.perm.Require("sales", "quotation", "update"), h.UpdateQuotation)
		quotations.DELETE("/:id", h.perm.Require("sales", "quotation", "delete"), h.DeleteQuotation)
		quotations.POST("/:id/convert", h.perm.Require("sales", "quotation", "update"), h.ConvertQuotationToOrder)
	}

	// Sales Orders
	salesOrders := rg.Group("/sales-orders")
	salesOrders.Use(h.perm.Require("sales", "order", "read"))
	{
		salesOrders.GET("", h.ListSalesOrders)
		salesOrders.POST("", h.perm.Require("sales", "order", "create"), h.CreateSalesOrder)
		salesOrders.GET("/:id", h.GetSalesOrder)
		salesOrders.PUT("/:id", h.perm.Require("sales", "order", "update"), h.UpdateSalesOrder)
		salesOrders.DELETE("/:id", h.perm.Require("sales", "order", "delete"), h.DeleteSalesOrder)
		salesOrders.POST("/:id/confirm", h.perm.Require("sales", "order", "approve"), h.ConfirmSalesOrder)
		salesOrders.POST("/:id/cancel", h.CancelSalesOrder)
		salesOrders.POST("/:id/invoice", h.CreateInvoiceFromOrder)
	}

	// Sales Delivery Orders (outbound shipments from sales orders)
	deliveryOrders := rg.Group("/sales/delivery-orders")
	deliveryOrders.Use(h.perm.Require("sales", "delivery", "read"))
	{
		deliveryOrders.GET("", h.ListDeliveryOrders)
		deliveryOrders.POST("", h.perm.Require("sales", "delivery", "create"), h.CreateDeliveryOrder)
		deliveryOrders.GET("/:id", h.GetDeliveryOrder)
		deliveryOrders.PUT("/:id", h.perm.Require("sales", "delivery", "update"), h.UpdateDeliveryOrder)
		deliveryOrders.POST("/:id/validate", h.perm.Require("sales", "delivery", "update"), h.ValidateDeliveryOrder)
		deliveryOrders.POST("/:id/cancel", h.perm.Require("sales", "delivery", "update"), h.CancelDeliveryOrder)
	}

	// Sales Invoices
	invoices := rg.Group("/sales-invoices")
	invoices.Use(h.perm.Require("sales", "invoice", "read"))
	{
		invoices.GET("", h.ListSalesInvoices)
		invoices.POST("", h.perm.Require("sales", "invoice", "create"), h.CreateSalesInvoice)
		invoices.POST("/repair-revenue", h.perm.Require("sales", "invoice", "update"), h.RepairRevenueJournalEntries)
		invoices.GET("/:id", h.GetSalesInvoice)
		invoices.PUT("/:id", h.perm.Require("sales", "invoice", "update"), h.UpdateSalesInvoice)
		invoices.DELETE("/:id", h.perm.Require("sales", "invoice", "delete"), h.DeleteSalesInvoice)
		invoices.POST("/:id/send", h.SendInvoice)
		invoices.POST("/:id/record-payment", h.RecordPayment)
		invoices.POST("/:id/credit-note", h.perm.Require("sales", "invoice", "create"), h.CreateCreditNote)
		invoices.POST("/:id/confirm-credit-note", h.perm.Require("sales", "invoice", "update"), h.ConfirmCreditNote)
	}

	// Sales Returns
	salesReturns := rg.Group("/sales-returns")
	salesReturns.Use(h.perm.Require("sales", "return", "read"))
	{
		salesReturns.GET("", h.ListSalesReturns)
		salesReturns.POST("", h.perm.Require("sales", "return", "create"), h.CreateSalesReturn)
		salesReturns.GET("/:id", h.GetSalesReturn)
		salesReturns.PUT("/:id", h.perm.Require("sales", "return", "update"), h.UpdateSalesReturn)
		salesReturns.DELETE("/:id", h.perm.Require("sales", "return", "delete"), h.DeleteSalesReturn)
		salesReturns.POST("/:id/approve", h.perm.Require("sales", "return", "approve"), h.ApproveSalesReturn)
		salesReturns.POST("/:id/reject", h.perm.Require("sales", "return", "approve"), h.RejectSalesReturn)
		salesReturns.POST("/:id/refund", h.perm.Require("sales", "return", "update"), h.ProcessRefund)
	}

	// Payment Terms
	paymentTerms := rg.Group("/payment-terms")
	paymentTerms.Use(h.perm.Require("sales", "payment_term", "read"))
	{
		paymentTerms.GET("", h.ListPaymentTerms)
		paymentTerms.POST("", h.perm.Require("sales", "payment_term", "create"), h.CreatePaymentTerm)
		paymentTerms.GET("/default", h.GetDefaultPaymentTerm)
		paymentTerms.GET("/stats", h.GetPaymentTermStats)
		paymentTerms.POST("/calculate-due-date", h.CalculateDueDate)
		paymentTerms.GET("/:id", h.GetPaymentTerm)
		paymentTerms.PUT("/:id", h.perm.Require("sales", "payment_term", "update"), h.UpdatePaymentTerm)
		paymentTerms.DELETE("/:id", h.perm.Require("sales", "payment_term", "delete"), h.DeletePaymentTerm)
		paymentTerms.POST("/:id/set-default", h.perm.Require("sales", "payment_term", "update"), h.SetDefaultPaymentTerm)
	}

	// Pricelists
	pricelists := rg.Group("/pricelists")
	pricelists.Use(h.perm.Require("sales", "pricelist", "read"))
	{
		pricelists.GET("", h.ListPricelists)
		pricelists.POST("", h.perm.Require("sales", "pricelist", "create"), h.CreatePricelist)
		pricelists.GET("/default", h.GetDefaultPricelist)
		pricelists.POST("/get-price", h.GetProductPrice)
		pricelists.POST("/get-bulk-prices", h.GetBulkProductPrices)
		pricelists.POST("/assign-customer", h.perm.Require("sales", "pricelist", "update"), h.AssignCustomerPricelist)
		pricelists.GET("/:id", h.GetPricelist)
		pricelists.PUT("/:id", h.perm.Require("sales", "pricelist", "update"), h.UpdatePricelist)
		pricelists.DELETE("/:id", h.perm.Require("sales", "pricelist", "delete"), h.DeletePricelist)
		pricelists.POST("/:id/set-default", h.perm.Require("sales", "pricelist", "update"), h.SetDefaultPricelist)
	}

	// Quotation Templates
	quotationTemplates := rg.Group("/quotation-templates")
	quotationTemplates.Use(h.perm.Require("sales", "quotation_template", "read"))
	{
		quotationTemplates.GET("", h.ListQuotationTemplates)
		quotationTemplates.POST("", h.perm.Require("sales", "quotation_template", "create"), h.CreateQuotationTemplate)
		quotationTemplates.GET("/:id", h.GetQuotationTemplate)
		quotationTemplates.PUT("/:id", h.perm.Require("sales", "quotation_template", "update"), h.UpdateQuotationTemplate)
		quotationTemplates.DELETE("/:id", h.perm.Require("sales", "quotation_template", "delete"), h.DeleteQuotationTemplate)
		quotationTemplates.POST("/:id/duplicate", h.perm.Require("sales", "quotation_template", "create"), h.DuplicateQuotationTemplate)
	}

	// Customer Follow-ups (Dunning)
	followups := rg.Group("/followups")
	followups.Use(h.perm.Require("finance", "followup", "read"))
	{
		followups.GET("/summary", h.GetFollowupSummary)
		followups.GET("/customers", h.ListCustomerFollowups)
		followups.GET("/customers/:customer_id", h.GetCustomerFollowupDetails)
		followups.POST("/actions", h.perm.Require("finance", "followup", "create"), h.CreateFollowupAction)
		followups.POST("/send-reminder", h.perm.Require("finance", "followup", "create"), h.SendFollowupReminder)
		followups.POST("/promises", h.perm.Require("finance", "followup", "create"), h.CreatePaymentPromise)
		followups.PUT("/promises/:id/status", h.perm.Require("finance", "followup", "update"), h.UpdatePaymentPromiseStatus)
	}

	// Follow-up Levels
	followupLevels := rg.Group("/followup-levels")
	followupLevels.Use(h.perm.Require("finance", "followup_level", "read"))
	{
		followupLevels.GET("", h.ListFollowupLevels)
		followupLevels.POST("", h.perm.Require("finance", "followup_level", "create"), h.CreateFollowupLevel)
		followupLevels.PUT("/:id", h.perm.Require("finance", "followup_level", "update"), h.UpdateFollowupLevel)
		followupLevels.DELETE("/:id", h.perm.Require("finance", "followup_level", "delete"), h.DeleteFollowupLevel)
	}

	// Tax Reports
	taxReports := rg.Group("/tax-reports")
	taxReports.Use(h.perm.Require("finance", "tax_report", "read"))
	{
		taxReports.GET("/summary", h.GetTaxReportSummary)
		taxReports.GET("/transactions", h.GetTaxTransactions)
		taxReports.GET("/periods", h.ListTaxReportPeriods)
		taxReports.POST("/periods", h.perm.Require("finance", "tax_report", "create"), h.CreateTaxReportPeriod)
		taxReports.GET("/periods/:id", h.GetTaxReportPeriod)
		taxReports.POST("/periods/:id/calculate", h.perm.Require("finance", "tax_report", "update"), h.CalculateTaxReport)
		taxReports.POST("/periods/:id/file", h.perm.Require("finance", "tax_report", "file"), h.FileTaxReport)
		taxReports.DELETE("/periods/:id", h.perm.Require("finance", "tax_report", "delete"), h.DeleteTaxReportPeriod)
		taxReports.POST("/periods/:id/pay", h.perm.Require("finance", "tax_report", "update"), h.PayTaxPeriod)
		// Employee-taxes aggregation (migration 330)
		taxReports.GET("/employee-taxes", h.GetEmployeeTaxReport)
		// Per-tax XML export for regulator filings (NDFL quarterly, ESP
		// monthly, INPS monthly — §10 reports #2–4 of the TZ).
		taxReports.GET("/employee-taxes/xml", h.ExportEmployeeTaxReportXML)
	}

	// Discounts
	discounts := rg.Group("/discounts")
	discounts.Use(h.perm.Require("sales", "discount", "read"))
	{
		discounts.GET("", h.ListDiscounts)
		discounts.POST("", h.perm.Require("sales", "discount", "create"), h.CreateDiscount)
		discounts.GET("/:id", h.GetDiscount)
		discounts.PUT("/:id", h.perm.Require("sales", "discount", "update"), h.UpdateDiscount)
		discounts.DELETE("/:id", h.perm.Require("sales", "discount", "delete"), h.DeleteDiscount)
		discounts.POST("/validate", h.ValidateDiscountCode)
		discounts.POST("/:id/use", h.UseDiscountCode)
	}

	// Purchase Orders
	purchaseOrders := rg.Group("/purchase-orders")
	purchaseOrders.Use(h.perm.Require("purchase", "order", "read"))
	{
		purchaseOrders.GET("", h.ListPurchaseOrders)
		purchaseOrders.POST("", h.perm.Require("purchase", "order", "create"), h.CreatePurchaseOrder)
		purchaseOrders.POST("/scan-receipt", h.perm.Require("purchase", "order", "create"), h.ScanPurchaseReceipt)
		purchaseOrders.GET("/:id", h.GetPurchaseOrder)
		purchaseOrders.PUT("/:id", h.perm.Require("purchase", "order", "update"), h.UpdatePurchaseOrder)
		purchaseOrders.DELETE("/:id", h.perm.Require("purchase", "order", "delete"), h.DeletePurchaseOrder)
		purchaseOrders.POST("/:id/submit", h.perm.Require("purchase", "order", "update"), h.SubmitPOForApproval)
		purchaseOrders.POST("/:id/approve", h.perm.Require("purchase", "order", "approve"), h.ApprovePurchaseOrder)
		purchaseOrders.POST("/:id/receive", h.ReceivePurchaseOrder)
		purchaseOrders.POST("/:id/bill", h.perm.Require("purchase", "invoice", "create"), h.CreateBillFromPO)
	}

	// Purchase Invoices (Vendor Bills)
	purchaseInvoices := rg.Group("/purchase-invoices")
	purchaseInvoices.Use(h.perm.Require("purchase", "invoice", "read"))
	{
		purchaseInvoices.GET("", h.ListPurchaseInvoices)
		purchaseInvoices.GET("/stats", h.GetPurchaseInvoiceStats)
		purchaseInvoices.POST("", h.perm.Require("purchase", "invoice", "create"), h.CreatePurchaseInvoice)
		purchaseInvoices.GET("/:id", h.GetPurchaseInvoice)
		purchaseInvoices.PUT("/:id", h.perm.Require("purchase", "invoice", "update"), h.UpdatePurchaseInvoice)
		purchaseInvoices.DELETE("/:id", h.perm.Require("purchase", "invoice", "delete"), h.DeletePurchaseInvoice)
		purchaseInvoices.POST("/:id/confirm", h.perm.Require("purchase", "invoice", "approve"), h.ConfirmPurchaseInvoice)
		purchaseInvoices.POST("/:id/post", h.perm.Require("purchase", "invoice", "approve"), h.PostPurchaseInvoice)
		purchaseInvoices.POST("/:id/pay", h.PayPurchaseInvoice)
		purchaseInvoices.POST("/:id/debit-note", h.perm.Require("purchase", "invoice", "create"), h.CreateDebitNote)
		purchaseInvoices.POST("/:id/confirm-debit-note", h.perm.Require("purchase", "invoice", "approve"), h.ConfirmDebitNote)
	}

	// Request for Quotations (RFQ)
	rfqs := rg.Group("/rfqs")
	rfqs.Use(h.perm.Require("purchase", "rfq", "read"))
	{
		rfqs.GET("", h.ListRFQs)
		rfqs.POST("", h.perm.Require("purchase", "rfq", "create"), h.CreateRFQ)
		rfqs.GET("/:id", h.GetRFQ)
		rfqs.PUT("/:id", h.perm.Require("purchase", "rfq", "update"), h.UpdateRFQ)
		rfqs.DELETE("/:id", h.perm.Require("purchase", "rfq", "delete"), h.DeleteRFQ)
		rfqs.POST("/:id/open", h.perm.Require("purchase", "rfq", "update"), h.OpenRFQ)
		rfqs.POST("/:id/responses", h.SubmitRFQResponse)
		rfqs.POST("/:id/select-winner", h.perm.Require("purchase", "rfq", "update"), h.SelectRFQWinner)
		rfqs.POST("/:id/convert-to-po", h.perm.Require("purchase", "purchase_order", "create"), h.ConvertRFQToPO)
	}

	// Procurement Contracts
	contracts := rg.Group("/contracts")
	contracts.Use(h.perm.Require("purchase", "contract", "read"))
	{
		contracts.GET("", h.ListContracts)
		contracts.POST("", h.perm.Require("purchase", "contract", "create"), h.CreateContract)
		contracts.GET("/:id", h.GetContract)
		contracts.PUT("/:id", h.perm.Require("purchase", "contract", "update"), h.UpdateContract)
		contracts.DELETE("/:id", h.perm.Require("purchase", "contract", "delete"), h.DeleteContract)
		contracts.POST("/:id/activate", h.perm.Require("purchase", "contract", "update"), h.ActivateContract)
		contracts.POST("/:id/terminate", h.perm.Require("purchase", "contract", "update"), h.TerminateContract)
	}

	// Supplier Price History
	priceHistory := rg.Group("/price-history")
	priceHistory.Use(h.perm.Require("purchase", "price_history", "read"))
	{
		priceHistory.GET("", h.ListPriceHistory)
		priceHistory.POST("", h.perm.Require("purchase", "price_history", "create"), h.CreatePriceHistory)
		priceHistory.GET("/:id", h.GetPriceHistory)
		priceHistory.DELETE("/:id", h.perm.Require("purchase", "price_history", "delete"), h.DeletePriceHistory)
	}

	// Vendor Prices (pricelist)
	vendorPrices := rg.Group("/vendor-prices")
	vendorPrices.Use(h.perm.Require("purchase", "price_history", "read"))
	{
		vendorPrices.GET("", h.ListVendorPrices)
		vendorPrices.GET("/lookup", h.LookupVendorPrice)
		vendorPrices.POST("", h.perm.Require("purchase", "price_history", "create"), h.CreateVendorPrice)
		vendorPrices.GET("/:id", h.GetVendorPrice)
		vendorPrices.PUT("/:id", h.perm.Require("purchase", "price_history", "update"), h.UpdateVendorPrice)
		vendorPrices.DELETE("/:id", h.perm.Require("purchase", "price_history", "delete"), h.DeleteVendorPrice)
	}

	// Purchase Requisitions
	requisitions := rg.Group("/purchase-requisitions")
	requisitions.Use(h.perm.Require("purchase", "requisition", "read"))
	{
		requisitions.GET("", h.ListPurchaseRequisitions)
		requisitions.POST("", h.perm.Require("purchase", "requisition", "create"), h.CreatePurchaseRequisition)
		requisitions.GET("/:id", h.GetPurchaseRequisition)
		requisitions.PUT("/:id", h.perm.Require("purchase", "requisition", "update"), h.UpdatePurchaseRequisition)
		requisitions.DELETE("/:id", h.perm.Require("purchase", "requisition", "delete"), h.DeletePurchaseRequisition)
		requisitions.POST("/:id/submit", h.perm.Require("purchase", "requisition", "update"), h.SubmitPurchaseRequisition)
		requisitions.POST("/:id/approve", h.perm.Require("purchase", "requisition", "approve"), h.ApprovePurchaseRequisition)
		requisitions.POST("/:id/reject", h.perm.Require("purchase", "requisition", "approve"), h.RejectPurchaseRequisition)
		requisitions.POST("/:id/convert-to-po", h.perm.Require("purchase", "order", "create"), h.ConvertPRToPO)
	}

	// Goods Receipts
	goodsReceipts := rg.Group("/goods-receipts")
	goodsReceipts.Use(h.perm.Require("purchase", "receipt", "read"))
	{
		goodsReceipts.GET("", h.ListGoodsReceipts)
		goodsReceipts.POST("", h.perm.Require("purchase", "receipt", "create"), h.CreateGoodsReceipt)
		goodsReceipts.GET("/:id", h.GetGoodsReceipt)
		goodsReceipts.DELETE("/:id", h.perm.Require("purchase", "receipt", "delete"), h.DeleteGoodsReceipt)
		goodsReceipts.POST("/:id/inspect", h.perm.Require("purchase", "receipt", "update"), h.InspectGoodsReceipt)
		goodsReceipts.POST("/:id/complete", h.perm.Require("purchase", "receipt", "update"), h.CompleteGoodsReceipt)
		goodsReceipts.POST("/:id/cancel", h.perm.Require("purchase", "receipt", "update"), h.CancelGoodsReceipt)
		goodsReceipts.GET("/:id/landed-cost-data", h.GetGRForLandedCost)
	}

	// Landed Costs
	landedCosts := rg.Group("/landed-costs")
	landedCosts.Use(h.perm.Require("purchase", "receipt", "read"))
	{
		landedCosts.GET("", h.ListLandedCosts)
		landedCosts.POST("", h.perm.Require("purchase", "receipt", "create"), h.CreateLandedCost)
		landedCosts.GET("/:id", h.GetLandedCost)
		landedCosts.POST("/:id/validate", h.perm.Require("purchase", "receipt", "update"), h.ValidateLandedCost)
		landedCosts.POST("/:id/cancel", h.perm.Require("purchase", "receipt", "update"), h.CancelLandedCost)
	}

	// Landed Cost Types
	landedCostTypes := rg.Group("/landed-cost-types")
	landedCostTypes.Use(h.perm.Require("purchase", "receipt", "read"))
	{
		landedCostTypes.GET("", h.ListLandedCostTypes)
		landedCostTypes.POST("", h.perm.Require("purchase", "receipt", "create"), h.CreateLandedCostType)
	}

	// Purchase Returns
	purchaseReturns := rg.Group("/purchase-returns")
	purchaseReturns.Use(h.perm.Require("purchase", "return", "read"))
	{
		purchaseReturns.GET("", h.ListPurchaseReturns)
		purchaseReturns.POST("", h.perm.Require("purchase", "return", "create"), h.CreatePurchaseReturn)
		purchaseReturns.GET("/:id", h.GetPurchaseReturn)
		purchaseReturns.DELETE("/:id", h.perm.Require("purchase", "return", "delete"), h.DeletePurchaseReturn)
		purchaseReturns.POST("/:id/submit", h.perm.Require("purchase", "return", "update"), h.SubmitPurchaseReturn)
		purchaseReturns.POST("/:id/approve", h.perm.Require("purchase", "return", "approve"), h.ApprovePurchaseReturn)
		purchaseReturns.POST("/:id/reject", h.perm.Require("purchase", "return", "approve"), h.RejectPurchaseReturn)
		purchaseReturns.POST("/:id/ship", h.perm.Require("purchase", "return", "update"), h.ShipPurchaseReturn)
		purchaseReturns.POST("/:id/receive", h.perm.Require("purchase", "return", "update"), h.ReceivePurchaseReturn)
		purchaseReturns.POST("/:id/credit", h.perm.Require("purchase", "return", "update"), h.ApplyCreditNote)
		purchaseReturns.POST("/:id/cancel", h.perm.Require("purchase", "return", "update"), h.CancelPurchaseReturn)
	}

	// Blanket Orders (Standing Orders / Call-off Contracts)
	blanketOrders := rg.Group("/blanket-orders")
	blanketOrders.Use(h.perm.Require("purchase", "contract", "read"))
	{
		blanketOrders.GET("", h.ListBlanketOrders)
		blanketOrders.POST("", h.perm.Require("purchase", "contract", "create"), h.CreateBlanketOrder)
		blanketOrders.GET("/:id", h.GetBlanketOrder)
		blanketOrders.PUT("/:id", h.perm.Require("purchase", "contract", "update"), h.UpdateBlanketOrder)
		blanketOrders.DELETE("/:id", h.perm.Require("purchase", "contract", "delete"), h.DeleteBlanketOrder)
		blanketOrders.POST("/:id/activate", h.perm.Require("purchase", "contract", "update"), h.ActivateBlanketOrder)
		blanketOrders.GET("/:id/releases", h.ListBlanketOrderReleases)
		blanketOrders.POST("/:id/releases", h.perm.Require("purchase", "order", "create"), h.CreateBlanketOrderRelease)
		blanketOrders.POST("/:id/releases/:release_id/confirm", h.perm.Require("purchase", "order", "approve"), h.ConfirmBlanketOrderRelease)
		blanketOrders.POST("/:id/releases/:release_id/cancel", h.perm.Require("purchase", "order", "update"), h.CancelBlanketOrderRelease)
	}

	// Procurement Rules Engine
	procurementRules := rg.Group("/procurement-rules")
	procurementRules.Use(h.perm.Require("purchase", "rule", "read"))
	{
		procurementRules.GET("", h.ListProcurementRules)
		procurementRules.POST("", h.perm.Require("purchase", "rule", "create"), h.CreateProcurementRule)
		procurementRules.GET("/:id", h.GetProcurementRule)
		procurementRules.PUT("/:id", h.perm.Require("purchase", "rule", "update"), h.UpdateProcurementRule)
		procurementRules.DELETE("/:id", h.perm.Require("purchase", "rule", "delete"), h.DeleteProcurementRule)
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
	dropship.Use(h.perm.Require("sales", "dropship", "read"))
	{
		// Dropship Orders
		dropship.GET("/orders", h.ListDropshipOrders)
		dropship.POST("/orders", h.perm.Require("sales", "dropship", "create"), h.CreateDropshipOrder)
		dropship.GET("/orders/:id", h.GetDropshipOrder)
		dropship.PUT("/orders/:id", h.perm.Require("sales", "dropship", "update"), h.UpdateDropshipOrder)
		dropship.POST("/orders/:id/ship", h.perm.Require("sales", "dropship", "update"), h.MarkDropshipShipped)
		dropship.POST("/orders/:id/deliver", h.perm.Require("sales", "dropship", "update"), h.MarkDropshipDelivered)
		dropship.POST("/orders/:id/cancel", h.perm.Require("sales", "dropship", "update"), h.CancelDropshipOrder)

		// Dropship Stats
		dropship.GET("/stats", h.GetDropshipStats)

		// Dropship Vendor Settings
		dropship.GET("/vendors", h.ListDropshipVendors)
		dropship.POST("/vendors", h.perm.Require("sales", "dropship", "create"), h.CreateDropshipVendorSettings)
		dropship.PUT("/vendors/:id", h.perm.Require("sales", "dropship", "update"), h.UpdateDropshipVendorSettings)
		dropship.DELETE("/vendors/:id", h.perm.Require("sales", "dropship", "delete"), h.DeleteDropshipVendorSettings)

		// Dropship Product-Vendor Links
		dropship.GET("/product-vendors", h.ListDropshipProductVendors)
		dropship.POST("/product-vendors", h.perm.Require("sales", "dropship", "create"), h.CreateDropshipProductVendor)
		dropship.PUT("/product-vendors/:id", h.perm.Require("sales", "dropship", "update"), h.UpdateDropshipProductVendor)
		dropship.DELETE("/product-vendors/:id", h.perm.Require("sales", "dropship", "delete"), h.DeleteDropshipProductVendor)

		// Dropshippable Products
		dropship.GET("/products", h.GetDropshippableProducts)
	}

	// Account Types (reference data)
	rg.GET("/account-types", h.ListAccountTypes)

	// Chart of Accounts
	accounts := rg.Group("/accounts")
	accounts.Use(h.perm.Require("finance", "account", "read"))
	{
		accounts.GET("", h.ListAccounts)
		accounts.GET("/next-code", h.GetNextAccountCode)
		accounts.POST("", h.perm.Require("finance", "account", "create"), h.CreateAccount)
		accounts.GET("/:id", h.GetAccount)
		accounts.PUT("/:id", h.perm.Require("finance", "account", "update"), h.UpdateAccount)
		accounts.DELETE("/:id", h.perm.Require("finance", "account", "delete"), h.DeleteAccount)
		accounts.GET("/:id/transactions", h.GetAccountTransactions)
	}

	// Payment journals (bank/cash only) — no finance permission required, any authenticated user can access
	rg.GET("/journals/payment", h.ListPaymentJournals)

	// Journals (accounting journals like GEN, SAL, PUR, MISC).
	//
	// Read access (GET) is INTENTIONALLY ungated. Sales-invoice and
	// purchase-bill creation flows fetch this list to populate the
	// "journal" dropdown — gating reads behind `finance:journal:read`
	// breaks invoice creation for employees who legitimately have
	// only sales / purchase permissions but no finance permission.
	// Journals are reference data (like products, units of measure,
	// tax rates), not sensitive financial details — so any
	// authenticated user can list them.
	//
	// Mutating operations (POST / PUT / DELETE) remain gated behind
	// the finance permission — only Buxgalter / Admin can create or
	// modify the journal *definitions*.
	journalGroup := rg.Group("/journals")
	{
		journalGroup.GET("", h.ListJournals)
		journalGroup.POST("", h.perm.Require("finance", "journal", "create"), h.CreateJournal)
		journalGroup.GET("/:id", h.GetJournal)
		journalGroup.PUT("/:id", h.perm.Require("finance", "journal", "update"), h.UpdateJournal)
		journalGroup.DELETE("/:id", h.perm.Require("finance", "journal", "delete"), h.DeleteJournal)
		journalGroup.GET("/:id/payment-methods", h.ListJournalPaymentMethods)
		journalGroup.POST("/:id/payment-methods", h.perm.Require("finance", "journal", "update"), h.AddJournalPaymentMethod)
		journalGroup.PUT("/:id/payment-methods/:pmId", h.perm.Require("finance", "journal", "update"), h.UpdateJournalPaymentMethod)
		journalGroup.DELETE("/:id/payment-methods/:pmId", h.perm.Require("finance", "journal", "update"), h.RemoveJournalPaymentMethod)
	}

	// Payment Methods
	paymentMethodsGroup := rg.Group("/payment-methods")
	{
		paymentMethodsGroup.GET("", h.ListPaymentMethods)
	}

	// Journal Entries.
	//
	// Read access (GET) is also ungated — same reason as /journals
	// above. Sales / purchase modules render JE references on invoice
	// detail screens (e.g. "linked to journal entry JE-20260507-…"),
	// and the auto-post code paths emit JE rows that the originating
	// module sometimes needs to read back. Mutations stay gated.
	journals := rg.Group("/journal-entries")
	{
		journals.GET("", h.ListJournalEntries)
		journals.POST("", h.perm.Require("finance", "journal", "create"), h.CreateJournalEntry)
		journals.GET("/:id", h.GetJournalEntry)
		journals.PUT("/:id", h.perm.Require("finance", "journal", "create"), h.UpdateJournalEntry)
		journals.DELETE("/:id", h.perm.Require("finance", "journal", "delete"), h.DeleteJournalEntry)
		journals.POST("/:id/post", h.perm.Require("finance", "journal", "post"), h.PostJournalEntry)
		journals.POST("/:id/reset-to-draft", h.perm.Require("finance", "journal", "post"), h.ResetJournalEntryToDraft)
		journals.POST("/:id/cancel", h.perm.Require("finance", "journal", "create"), h.CancelJournalEntry)
		journals.POST("/:id/reverse", h.perm.Require("finance", "journal", "reverse"), h.ReverseJournalEntry)
		journals.GET("/:id/audit-logs", h.GetJournalEntryAuditLogs)
	}

	// Payments
	payments := rg.Group("/payments")
	payments.Use(h.perm.Require("finance", "payment", "read"))
	{
		payments.GET("", h.ListPayments)
		payments.POST("", h.perm.Require("finance", "payment", "create"), h.CreatePayment)
		payments.GET("/:id", h.GetPayment)
		payments.POST("/:id/confirm", h.perm.Require("finance", "payment", "approve"), h.ConfirmPayment)
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
	bankAccounts.Use(h.perm.Require("finance", "bank_account", "read"))
	{
		bankAccounts.GET("", h.ListBankAccounts)
		bankAccounts.POST("", h.perm.Require("finance", "bank_account", "create"), h.CreateBankAccount)
		bankAccounts.GET("/:id", h.GetBankAccount)
		bankAccounts.PUT("/:id", h.perm.Require("finance", "bank_account", "update"), h.UpdateBankAccount)
		bankAccounts.DELETE("/:id", h.perm.Require("finance", "bank_account", "delete"), h.DeleteBankAccount)
		// Bank Transactions (nested under bank accounts)
		bankAccounts.GET("/:id/transactions", h.ListBankTransactions)
		bankAccounts.POST("/:id/transactions", h.perm.Require("finance", "bank_account", "update"), h.CreateBankTransaction)
		bankAccounts.POST("/:id/transactions/:transactionId/reconcile", h.perm.Require("finance", "bank_account", "update"), h.ReconcileBankTransaction)

		// Bank Reconciliation
		bankAccounts.GET("/:id/reconciliations", h.ListBankReconciliations)
		bankAccounts.POST("/:id/reconciliations", h.perm.Require("finance", "bank_account", "update"), h.CreateBankReconciliation)
		bankAccounts.GET("/:id/reconciliations/:reconciliationId", h.GetBankReconciliation)
		bankAccounts.PUT("/:id/reconciliations/:reconciliationId", h.perm.Require("finance", "bank_account", "update"), h.UpdateBankReconciliation)
		bankAccounts.POST("/:id/reconciliations/:reconciliationId/complete", h.perm.Require("finance", "bank_account", "update"), h.CompleteBankReconciliation)
		bankAccounts.DELETE("/:id/reconciliations/:reconciliationId", h.perm.Require("finance", "bank_account", "delete"), h.DeleteBankReconciliation)

		// Bank Statement Import
		bankAccounts.POST("/:id/import", h.perm.Require("finance", "bank_account", "update"), h.ImportBankStatement)
	}

	// Cash Transactions (Kassa)
	cashTransactions := rg.Group("/cash-transactions")
	cashTransactions.Use(h.perm.Require("finance", "cash_transaction", "read"))
	{
		cashTransactions.GET("", h.ListCashTransactions)
		cashTransactions.POST("", h.perm.Require("finance", "cash_transaction", "create"), h.CreateCashTransaction)
		cashTransactions.GET("/:id", h.GetCashTransaction)
		cashTransactions.PUT("/:id", h.perm.Require("finance", "cash_transaction", "update"), h.UpdateCashTransaction)
		cashTransactions.DELETE("/:id", h.perm.Require("finance", "cash_transaction", "delete"), h.DeleteCashTransaction)
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
		fiscalPeriods.POST("/:id/lock", h.LockFiscalPeriod)
		fiscalPeriods.POST("/:id/unlock", h.UnlockFiscalPeriod)
	}

	// Accounting Periods
	periods := rg.Group("/accounting-periods")
	{
		periods.GET("", h.ListAccountingPeriods)
		periods.POST("", h.CreateAccountingPeriod)
		periods.POST("/auto-create", h.AutoCreatePeriods)
		periods.POST("/:id/lock", h.LockAccountingPeriod)
		periods.POST("/:id/unlock", h.UnlockAccountingPeriod)
	}

	// Period Close Procedure (TT Buxgalteriya §4.3)
	periodClose := rg.Group("/period-close")
	{
		periodClose.POST("/run", h.ClosePeriod)
		periodClose.GET("", h.ListClosings)
		periodClose.GET("/:id", h.GetClosing)
		periodClose.POST("/:id/reopen", h.ReopenPeriod)
	}

	// Bank Statement Import (TT Buxgalteriya §8.1 Bank-mijoz) — 1C format
	bankImport := rg.Group("/bank-statement-imports")
	{
		bankImport.POST("", h.ImportBankStatement1C)
		bankImport.GET("", h.ListBankImports)
	}

	// E-invoice (TT Buxgalteriya §8.2)
	einvoices := rg.Group("/einvoices")
	{
		einvoices.POST("/ingest", h.IngestEInvoice)
		einvoices.GET("", h.ListEInvoices)
		einvoices.POST("/:id/approve", h.ApproveEInvoice)
		einvoices.POST("/:id/reject", h.RejectEInvoice)
		// Provider adapter calls
		einvoices.POST("/sync", h.SyncEInvoices)
		einvoices.POST("/:id/send", h.SendEInvoice)
	}

	// Webhook subscriptions (TT Buxgalteriya §7.4)
	hooks := rg.Group("/webhook-subscriptions")
	{
		hooks.POST("", h.CreateWebhookSubscription)
		hooks.GET("", h.ListWebhookSubscriptions)
		hooks.DELETE("/:id", h.DeleteWebhookSubscription)
		hooks.GET("/:id/deliveries", h.ListWebhookDeliveries)
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
	recurringJournals.Use(h.perm.Require("finance", "journal_entry", "read"))
	{
		recurringJournals.GET("", h.ListRecurringJournalTemplates)
		recurringJournals.POST("", h.perm.Require("finance", "journal_entry", "create"), h.CreateRecurringJournalTemplate)
		recurringJournals.GET("/pending", h.GetPendingRecurringEntries)
		recurringJournals.GET("/:id", h.GetRecurringJournalTemplate)
		recurringJournals.PUT("/:id", h.perm.Require("finance", "journal_entry", "update"), h.UpdateRecurringJournalTemplate)
		recurringJournals.DELETE("/:id", h.perm.Require("finance", "journal_entry", "delete"), h.DeleteRecurringJournalTemplate)
		recurringJournals.POST("/:id/generate", h.perm.Require("finance", "journal_entry", "create"), h.GenerateRecurringJournalEntry)
	}

	// Cash Registers (Kassa)
	cashRegisters := rg.Group("/cash/registers")
	cashRegisters.Use(h.perm.Require("finance", "cash", "read"))
	{
		cashRegisters.GET("", h.ListCashRegisters)
		cashRegisters.POST("", h.perm.Require("finance", "cash", "create"), h.CreateCashRegister)
		cashRegisters.GET("/:id", h.GetCashRegister)
		cashRegisters.PUT("/:id", h.perm.Require("finance", "cash", "update"), h.UpdateCashRegister)
	}

	// Cash Orders (PKO/RKO)
	cashOrders := rg.Group("/cash/orders")
	cashOrders.Use(h.perm.Require("finance", "cash", "read"))
	{
		cashOrders.GET("", h.ListCashOrders)
		cashOrders.POST("", h.perm.Require("finance", "cash", "create"), h.CreateCashOrder)
		cashOrders.GET("/:id", h.GetCashOrder)
		cashOrders.PUT("/:id", h.perm.Require("finance", "cash", "update"), h.UpdateCashOrder)
		cashOrders.POST("/:id/confirm", h.perm.Require("finance", "cash", "approve"), h.ConfirmCashOrder)
	}

	// Cash Book (Kassa kitob)
	rg.GET("/cash/book", h.perm.Require("finance", "cash", "read"), h.GetCashBook)

	// Currency Rates Sync & Revaluation
	currencyOps := rg.Group("/currency")
	currencyOps.Use(h.perm.Require("finance", "currency", "read"))
	{
		currencyOps.GET("/rates", h.ListExchangeDiffs)
		currencyOps.POST("/rates/sync", h.perm.Require("finance", "currency", "create"), h.SyncCurrencyRates)
		currencyOps.POST("/revalue", h.perm.Require("finance", "currency", "create"), h.RevalueCurrency)
		currencyOps.GET("/debt-report", h.CurrencyDebtReport)
	}

	// Reconciliation Acts (Akt sverka)
	reconciliation := rg.Group("/reconciliation")
	reconciliation.Use(h.perm.Require("finance", "reconciliation", "read"))
	{
		reconciliation.GET("", h.ListReconciliationActs)
		reconciliation.POST("", h.perm.Require("finance", "reconciliation", "create"), h.CreateReconciliationAct)
		reconciliation.POST("/bulk-generate", h.perm.Require("finance", "reconciliation", "create"), h.BulkGenerateReconciliation)
		reconciliation.GET("/:id", h.GetReconciliationAct)
		reconciliation.POST("/:id/refresh", h.perm.Require("finance", "reconciliation", "update"), h.RefreshReconciliationAct)
		reconciliation.PUT("/:id", h.perm.Require("finance", "reconciliation", "update"), h.UpdateReconciliationAct)
		reconciliation.DELETE("/:id", h.perm.Require("finance", "reconciliation", "delete"), h.DeleteReconciliationAct)
		reconciliation.GET("/:id/export", h.ExportReconciliationAct)
		reconciliation.POST("/:id/send", h.perm.Require("finance", "reconciliation", "update"), h.SendReconciliationAct)
		reconciliation.POST("/:id/remind", h.perm.Require("finance", "reconciliation", "update"), h.SendReconciliationReminder)
	}

	// Budgets (Byudjetlashtirish)
	budget := rg.Group("/budget")
	budget.Use(h.perm.Require("finance", "budget", "read"))
	{
		budget.GET("", h.ListBudgetsV2)
		budget.POST("", h.perm.Require("finance", "budget", "create"), h.CreateBudgetV2)
		budget.GET("/consolidated", h.GetConsolidatedBudget)
		budget.GET("/cash-flow", h.GetBudgetCashFlow)
		budget.GET("/plan-vs-actual", h.GetBudgetPlanVsActual)
		budget.GET("/:id", h.GetBudgetV2)
		budget.PUT("/:id", h.perm.Require("finance", "budget", "update"), h.UpdateBudgetV2)
		budget.DELETE("/:id", h.perm.Require("finance", "budget", "delete"), h.DeleteBudgetV2)
		budget.POST("/:id/submit", h.perm.Require("finance", "budget", "update"), h.SubmitBudgetForApproval)
		budget.POST("/:id/approve", h.perm.Require("finance", "budget", "update"), h.ApproveBudget)
		budget.POST("/:id/reject", h.perm.Require("finance", "budget", "update"), h.RejectBudget)
		budget.GET("/:id/lines", h.ListBudgetLines)
		budget.POST("/:id/lines", h.perm.Require("finance", "budget", "create"), h.CreateBudgetLine)
		budget.PUT("/:id/lines/:lineId", h.perm.Require("finance", "budget", "update"), h.UpdateBudgetLine)
	}

	// HR - Employees
	employees := rg.Group("/employees")
	employees.Use(h.perm.Require("hr", "employee", "read"))
	{
		employees.GET("", h.ListEmployees)
		employees.POST("", h.perm.Require("hr", "employee", "create"), h.CreateEmployee)
		employees.GET("/:id", h.GetEmployee)
		employees.PUT("/:id", h.perm.Require("hr", "employee", "update"), h.UpdateEmployee)
		employees.DELETE("/:id", h.perm.Require("hr", "employee", "delete"), h.DeleteEmployee)
		// Employee-Organization assignments
		employees.GET("/:id/organizations", h.ListEmployeeOrganizations)
		// Employee module permissions
		employees.GET("/:id/permissions", h.GetEmployeePermissions)
		employees.PUT("/:id/permissions", h.perm.Require("hr", "employee", "update"), h.BulkUpdateEmployeePermissions)
		employees.PUT("/:id/permissions/module", h.perm.Require("hr", "employee", "update"), h.UpdateEmployeeModulePermission)
		employees.DELETE("/:id/permissions", h.perm.Require("hr", "employee", "delete"), h.DeleteEmployeePermissions)
		// Employee deductions
		employees.GET("/:id/deductions", h.ListEmployeeDeductions)
		employees.POST("/:id/deductions/:did/cancel", h.perm.Require("hr", "employee", "update"), h.CancelDeduction)
		// Salary calculation with deductions
		employees.GET("/:id/salary-calculate", h.CalculateSalaryWithDeductions)
	}

	// Employee-Organization Assignments
	employeeOrgs := rg.Group("/employee-organizations")
	employeeOrgs.Use(h.perm.Require("hr", "employee", "read"))
	{
		employeeOrgs.POST("", h.perm.Require("hr", "employee", "update"), h.AssignEmployeeToOrganization)
		employeeOrgs.PUT("/:id", h.perm.Require("hr", "employee", "update"), h.UpdateEmployeeOrganization)
		employeeOrgs.DELETE("/:id", h.perm.Require("hr", "employee", "update"), h.RemoveEmployeeFromOrganization)
	}

	// HR - Attendance Records
	attendance := rg.Group("/attendance")
	attendance.Use(h.perm.Require("hr", "attendance", "read"))
	{
		attendance.GET("", h.ListAttendanceRecords)
		attendance.POST("", h.perm.Require("hr", "attendance", "create"), h.CreateAttendanceRecord)
		attendance.GET("/:id", h.GetAttendanceRecord)
		attendance.PUT("/:id", h.perm.Require("hr", "attendance", "update"), h.UpdateAttendanceRecord)
		attendance.DELETE("/:id", h.perm.Require("hr", "attendance", "delete"), h.DeleteAttendanceRecord)
	}

	// HR - Leave Requests
	leaveRequests := rg.Group("/leave-requests")
	leaveRequests.Use(h.perm.Require("hr", "leave", "read"))
	{
		leaveRequests.GET("", h.ListLeaveRequests)
		leaveRequests.POST("", h.perm.Require("hr", "leave", "create"), h.CreateLeaveRequest)
		leaveRequests.GET("/:id", h.GetLeaveRequest)
		leaveRequests.PUT("/:id", h.perm.Require("hr", "leave", "update"), h.UpdateLeaveRequest)
		leaveRequests.DELETE("/:id", h.perm.Require("hr", "leave", "delete"), h.DeleteLeaveRequest)
	}

	// HR - Leave Balances
	leaveBalances := rg.Group("/leave-balances")
	leaveBalances.Use(h.perm.Require("hr", "leave", "read"))
	{
		leaveBalances.GET("", h.ListLeaveBalances)
		leaveBalances.POST("", h.perm.Require("hr", "leave", "create"), h.CreateLeaveBalance)
		leaveBalances.GET("/:id", h.GetLeaveBalance)
		leaveBalances.PUT("/:id", h.perm.Require("hr", "leave", "update"), h.UpdateLeaveBalance)
		leaveBalances.DELETE("/:id", h.perm.Require("hr", "leave", "delete"), h.DeleteLeaveBalance)
	}

	// HR - Employee Contracts
	employeeContracts := rg.Group("/employee-contracts")
	employeeContracts.Use(h.perm.Require("hr", "contract", "read"))
	{
		employeeContracts.GET("", h.ListEmployeeContracts)
		employeeContracts.POST("", h.perm.Require("hr", "contract", "create"), h.CreateEmployeeContract)
		employeeContracts.GET("/:id", h.GetEmployeeContract)
		employeeContracts.PUT("/:id", h.perm.Require("hr", "contract", "update"), h.UpdateEmployeeContract)
		employeeContracts.DELETE("/:id", h.perm.Require("hr", "contract", "delete"), h.DeleteEmployeeContract)
	}

	// Workflows
	workflows := rg.Group("/workflows")
	workflows.Use(h.perm.Require("workflow", "workflow", "read"))
	{
		workflows.GET("", h.ListWorkflows)
		workflows.POST("", h.perm.Require("workflow", "workflow", "create"), h.CreateWorkflow)
		workflows.GET("/:id", h.GetWorkflow)
		workflows.PUT("/:id", h.perm.Require("workflow", "workflow", "update"), h.UpdateWorkflow)
		workflows.DELETE("/:id", h.perm.Require("workflow", "workflow", "delete"), h.DeleteWorkflow)
	}

	// Workflow Automation Rules
	workflowRules := rg.Group("/workflow-rules")
	workflowRules.Use(h.perm.Require("workflow", "workflow", "read"))
	{
		workflowRules.GET("", h.ListWorkflowRules)
		workflowRules.POST("", h.perm.Require("workflow", "workflow", "create"), h.CreateWorkflowRule)
		workflowRules.GET("/:id", h.GetWorkflowRule)
		workflowRules.PUT("/:id", h.perm.Require("workflow", "workflow", "update"), h.UpdateWorkflowRule)
		workflowRules.DELETE("/:id", h.perm.Require("workflow", "workflow", "delete"), h.DeleteWorkflowRule)
	}

	// Workflow Logs
	rg.GET("/workflow-logs", h.perm.Require("workflow", "workflow", "read"), h.ListWorkflowLogs)

	// AI Assistant — accessible to all authenticated users
	ai := rg.Group("/ai")
	{
		ai.GET("/capabilities", h.GetAICapabilities)
		ai.POST("/chat", h.AIChat)
		// Agentic chat — reads real ERP data via tools; proposes writes for
		// confirmation (executed via /ai/agent/execute after user approval).
		ai.POST("/agent", h.AIAgentChat)
		ai.POST("/agent/execute", h.AIAgentExecute)
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
	// Dashboard
	dashboard := rg.Group("/dashboard")
	{
		dashboard.GET("/top-products", h.GetDashboardTopProducts)
	}

	reports := rg.Group("/reports")
	// sales-summary is accessible to sales users, not just finance
	reports.GET("/sales-summary", h.perm.Require("sales", "order", "read"), h.GetSalesSummary)
	// director-summary aggregates cross-org metrics in one call for the Director Dashboard.
	// No extra permission beyond auth — the dashboard itself is gated by the installable app on the frontend.
	reports.GET("/director-summary", h.GetDirectorSummary)
	reports.Use(h.perm.Require("finance", "report", "read"))
	{
		reports.GET("/balance-sheet", h.GetBalanceSheet)
		reports.GET("/income-statement", h.GetIncomeStatement)
		reports.GET("/cash-flow", h.GetCashFlow)
		reports.GET("/trial-balance", h.GetTrialBalance)
		// ASQ per TT Buxgalteriya §6.1 — opening/turnover/closing + Excel export
		reports.GET("/trial-balance/turnover", h.GetTrialBalanceWithTurnover)
		reports.GET("/trial-balance/excel", h.ExportTrialBalanceExcel)
		// Diagnostics (genix_diagnostika §2.4) — anomaly scan over the trial balance
		reports.GET("/trial-balance/anomalies", h.GetTrialBalanceAnomalies)
		reports.GET("/general-ledger", h.GetGeneralLedger)
		// Bosh kitob — monthly breakdown per TT Buxgalteriya §6.2
		reports.GET("/general-ledger/monthly", h.GetGeneralLedgerMonthly)
		reports.GET("/aging-receivables", h.GetAgingReceivables)
		reports.GET("/aging-payables", h.GetAgingPayables)
		reports.GET("/inventory-summary", h.GetInventoryReport)
		reports.GET("/account-card", h.GetAccountCard)
		// BHMS №21 regulated reports (TT §6.4)
		reports.GET("/forma-1", h.GetForma1)
		reports.GET("/forma-2", h.GetForma2)
		reports.GET("/forma-3", h.GetForma3)
		reports.GET("/formas/excel", h.ExportFormasExcel)
	}

	// Settings
	settings := rg.Group("/settings")
	settings.Use(h.perm.Require("settings", "tenant", "read"))
	{
		settings.GET("", h.GetSettings)
		settings.PUT("", h.perm.Require("settings", "tenant", "update"), h.UpdateSettings)
	}

	// Admin Settings (alias for /settings to support frontend admin panel)
	adminSettings := rg.Group("/admin/settings")
	adminSettings.Use(h.perm.Require("settings", "tenant", "read"))
	{
		adminSettings.GET("", h.GetAdminSettings)
		adminSettings.PUT("", h.perm.Require("settings", "tenant", "update"), h.UpdateAdminSettings)
		adminSettings.PATCH("/:section", h.perm.Require("settings", "tenant", "update"), h.UpdateAdminSettingsSection)
		adminSettings.POST("/:section/reset", h.perm.Require("settings", "tenant", "update"), h.ResetAdminSettingsSection)
		adminSettings.POST("/reset", h.perm.Require("settings", "tenant", "update"), h.ResetAllAdminSettings)
	}

	// Per-tenant AI provider settings (user-supplied API key + model).
	adminAI := rg.Group("/admin/ai-settings")
	adminAI.Use(h.perm.Require("settings", "tenant", "read"))
	{
		adminAI.GET("", h.GetTenantAISettings)
		adminAI.PUT("", h.perm.Require("settings", "tenant", "update"), h.UpdateTenantAISettings)
	}

	// System Admin routes (cross-tenant access)
	admin := rg.Group("/admin")
	admin.Use(middleware.RequireSystemAdmin())
	{
		admin.GET("/users", h.ListAllSystemUsers)
		admin.GET("/tenants", h.ListAllTenants)
		admin.GET("/tenants/:id", h.GetTenantDetails)
		admin.PUT("/tenants/:id/activate", h.ActivateTenantSubscription)
		admin.DELETE("/users/:id", h.DeleteSystemUser)
		admin.POST("/clean-expired-tenants", h.CleanExpiredTenants)

		// Mobile app version gate — global config (one app for all tenants),
		// managed from Settings → "Mobile App". Read/write is system-admin only
		// (this group is gated by RequireSystemAdmin above).
		admin.GET("/mobile-versions", h.ListMobileVersions)
		admin.PUT("/mobile-versions/:platform", h.UpsertMobileVersion)
		// /admin/migrations — read-only view over schema_migrations so a
		// system admin can verify which DB migrations are applied on the
		// running backend without needing direct psql access. Intended
		// for production deploy verification.
		admin.GET("/migrations", h.GetMigrationStatus)

		// /admin/inventory-reconcile — diagnostic view that lines up the
		// inventory MODULE total (Ombor "Jami qiymat") against the
		// inventory GL ACCOUNT balance (Buxgalteriya "Xom ashyo")
		// per (tenant, organization), with top contributing products.
		// Use to pinpoint which org / product is driving any gap.
		// Read-only, system-admin gated by the parent group.
		admin.GET("/inventory-reconcile", h.GetInventoryReconcile)
	}

	// Audit Logs
	auditLogs := rg.Group("/audit-logs")
	auditLogs.Use(h.perm.Require("audit", "log", "read"))
	{
		auditLogs.GET("", h.ListAuditLogs)
		auditLogs.GET("/export", h.perm.Require("audit", "log", "export"), h.ExportAuditLogs)
	}

	// Notifications
	notifications := rg.Group("/notifications")
	{
		notifications.GET("", h.ListNotifications)
		notifications.GET("/unread-count", h.UnreadCount)
		notifications.PUT("/:id/read", h.MarkNotificationRead)
		notifications.DELETE("/:id", h.DeleteNotification)
		notifications.PUT("/read-all", h.MarkAllNotificationsRead)
	}

	// Mobile push device tokens (FCM). Authenticated; every user manages their
	// own device tokens — no extra permission needed.
	devices := rg.Group("/devices")
	{
		devices.POST("", h.RegisterDevice)
		devices.DELETE("", h.UnregisterDevice)
		devices.POST("/test", h.TestPush)
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
	workCenters.Use(h.perm.Require("manufacturing", "work_centers", "read"))
	{
		workCenters.GET("", h.ListWorkCenters)
		workCenters.POST("", h.perm.Require("manufacturing", "work_centers", "create"), h.CreateWorkCenter)
		workCenters.GET("/:id", h.GetWorkCenter)
		workCenters.PUT("/:id", h.perm.Require("manufacturing", "work_centers", "update"), h.UpdateWorkCenter)
		workCenters.DELETE("/:id", h.perm.Require("manufacturing", "work_centers", "delete"), h.DeleteWorkCenter)
		workCenters.GET("/:id/employees", h.ListWorkCenterEmployees)
		workCenters.POST("/:id/employees", h.perm.Require("manufacturing", "work_centers", "update"), h.AssignWorkCenterEmployees)
		workCenters.DELETE("/:id/employees/:employee_id", h.perm.Require("manufacturing", "work_centers", "update"), h.RemoveWorkCenterEmployee)
	}

	// Production Orders
	productionOrders := rg.Group("/production-orders")
	productionOrders.Use(h.perm.Require("manufacturing", "production_orders", "read"))
	{
		productionOrders.GET("", h.ListProductionOrders)
		productionOrders.POST("", h.perm.Require("manufacturing", "production_orders", "create"), h.CreateProductionOrder)
		productionOrders.GET("/schedule", h.GetProductionSchedule)
		productionOrders.GET("/stats", h.GetManufacturingStats)
		productionOrders.GET("/:id", h.GetProductionOrder)
		productionOrders.PUT("/:id", h.perm.Require("manufacturing", "production_orders", "update"), h.UpdateProductionOrder)
		productionOrders.DELETE("/:id", h.perm.Require("manufacturing", "production_orders", "delete"), h.DeleteProductionOrder)
		productionOrders.POST("/:id/confirm", h.perm.Require("manufacturing", "production_orders", "approve"), h.ConfirmProductionOrder)
		productionOrders.POST("/:id/start", h.StartProductionOrder)
		productionOrders.POST("/:id/pause", h.PauseProductionOrder)
		productionOrders.POST("/:id/complete", h.CompleteProductionOrder)
		productionOrders.POST("/:id/cancel", h.CancelProductionOrder)
		productionOrders.POST("/:id/record-production", h.RecordProduction)
		productionOrders.POST("/:id/complete-split", h.perm.Require("manufacturing", "production_orders", "update"), h.CompleteSplitOutput)
		productionOrders.GET("/:id/split-outputs", h.GetSplitOutputs)
	}

	// Work Orders
	workOrders := rg.Group("/work-orders")
	workOrders.Use(h.perm.Require("manufacturing", "work_orders", "read"))
	{
		workOrders.GET("", h.ListWorkOrders)
		workOrders.POST("", h.perm.Require("manufacturing", "work_orders", "create"), h.CreateWorkOrder)
		workOrders.GET("/:id", h.GetWorkOrder)
		workOrders.POST("/:id/start", h.StartWorkOrder)
		workOrders.POST("/:id/pause", h.PauseWorkOrder)
		workOrders.POST("/:id/complete", h.CompleteWorkOrder)
		workOrders.POST("/:id/time", h.RecordWorkOrderTime)
		workOrders.GET("/:id/materials", h.ListWorkOrderMaterials)
		workOrders.POST("/:id/materials", h.AddWorkOrderMaterial)
		workOrders.DELETE("/:id/materials/:material_id", h.RemoveWorkOrderMaterial)
		workOrders.GET("/:id/attachments", h.ListWorkOrderAttachments)
		workOrders.POST("/:id/attachments", h.UploadWorkOrderAttachment)
		workOrders.DELETE("/:id/attachments/:attachment_id", h.DeleteWorkOrderAttachment)
	}

	// Manufacturing Transfers (Pick Components / Store Finished)
	mfgTransfers := rg.Group("/manufacturing-transfers")
	mfgTransfers.Use(h.perm.Require("manufacturing", "transfers", "read"))
	{
		mfgTransfers.GET("", h.ListManufacturingTransfers)
		mfgTransfers.GET("/:id", h.GetManufacturingTransfer)
		mfgTransfers.POST("/:id/validate", h.perm.Require("manufacturing", "transfers", "update"), h.ValidateManufacturingTransfer)
	}

	// Equipment & Maintenance
	equipment := rg.Group("/equipment")
	equipment.Use(h.perm.Require("manufacturing", "work_centers", "read"))
	{
		equipment.GET("", h.ListEquipment)
		equipment.POST("", h.perm.Require("manufacturing", "work_centers", "create"), h.CreateEquipment)
		equipment.PUT("/:id", h.perm.Require("manufacturing", "work_centers", "update"), h.UpdateEquipment)
		equipment.DELETE("/:id", h.perm.Require("manufacturing", "work_centers", "delete"), h.DeleteEquipment)
		equipment.GET("/:id/maintenance", h.ListMaintenanceTasks)
		equipment.POST("/:id/maintenance", h.perm.Require("manufacturing", "work_centers", "create"), h.CreateMaintenanceTask)
		equipment.PUT("/:id/maintenance/:task_id", h.perm.Require("manufacturing", "work_centers", "update"), h.CompleteMaintenanceTask)
		equipment.PATCH("/:id/maintenance/:task_id", h.perm.Require("manufacturing", "work_centers", "update"), h.UpdateMaintenanceTask)
	}

	// Manufacturing Categories
	mfgCategories := rg.Group("/manufacturing-categories")
	mfgCategories.Use(h.perm.Require("manufacturing", "work_centers", "read"))
	{
		mfgCategories.GET("", h.ListManufacturingCategories)
		mfgCategories.POST("", h.perm.Require("manufacturing", "work_centers", "create"), h.CreateManufacturingCategory)
		mfgCategories.PUT("/:id", h.perm.Require("manufacturing", "work_centers", "update"), h.UpdateManufacturingCategory)
		mfgCategories.DELETE("/:id", h.perm.Require("manufacturing", "work_centers", "delete"), h.DeleteManufacturingCategory)
	}

	// Cost Calculations
	costCalcs := rg.Group("/cost-calculations")
	costCalcs.Use(h.perm.Require("manufacturing", "work_centers", "read"))
	{
		costCalcs.GET("", h.ListCostCalculations)
		costCalcs.POST("", h.CreateCostCalculation)
		costCalcs.GET("/:id", h.GetCostCalculation)
		costCalcs.PUT("/:id", h.UpdateCostCalculation)
		costCalcs.DELETE("/:id", h.DeleteCostCalculation)
	}

	// =====================================================
	// ERP EXTENSIONS MODULE ROUTES
	// =====================================================

	// Payroll Periods
	payrollPeriods := rg.Group("/payroll-periods")
	payrollPeriods.Use(h.perm.Require("hr", "payroll", "read"))
	{
		payrollPeriods.GET("", h.ListPayrollPeriods)
		payrollPeriods.POST("", h.perm.Require("hr", "payroll", "create"), h.CreatePayrollPeriod)
		payrollPeriods.GET("/:id", h.GetPayrollPeriod)
		payrollPeriods.PUT("/:id", h.perm.Require("hr", "payroll", "update"), h.UpdatePayrollPeriod)
		payrollPeriods.DELETE("/:id", h.perm.Require("hr", "payroll", "delete"), h.DeletePayrollPeriod)
		payrollPeriods.POST("/:id/process", h.perm.Require("hr", "payroll", "approve"), h.ProcessPayroll)
		// TZ §7.1 / §9.3 "calculate-all" — bulk-create payroll entries
		// for every active employee in the period. Idempotent: skips
		// employees who already have an entry.
		payrollPeriods.POST("/:id/calculate-all", h.perm.Require("hr", "payroll", "create"), h.CalculateAllPayroll)
		payrollPeriods.GET("/:id/entries", h.ListPayrollEntries)
		payrollPeriods.POST("/:id/entries", h.perm.Require("hr", "payroll", "create"), h.CreatePayrollEntry)
		payrollPeriods.PUT("/:id/entries/:eid", h.perm.Require("hr", "payroll", "update"), h.UpdatePayrollEntry)
		payrollPeriods.GET("/:id/entries/:eid/taxes", h.GetPayrollEntryTaxes)
		payrollPeriods.POST("/:id/entries/:eid/confirm", h.perm.Require("hr", "payroll", "approve"), h.ConfirmSalaryPaymentByEntry)
		// Vedomost (maosh vedomosti) — aggregated per-period payroll view
		// with every tax pivoted into its own column. Drives the Excel
		// export in §7.4 + §10 report #1 of ТЗ_Ish_Haqi_Soliq_Tolik.docx.
		payrollPeriods.GET("/:id/vedomost", h.GetPayrollVedomost)
	}

	// ────────────── Employee Taxes (migration 330) ──────────────
	// Configurable per-tenant catalog of employee taxes. Drives the Settings →
	// Finance → Employee Taxes UI, the create-payroll modal's tax picker, and
	// the Tax Reports → Employee Taxes section. Matches the loose auth pattern
	// of the existing /tax-rates group (auth-only, no extra permission check).
	employeeTaxes := rg.Group("/employee-taxes")
	{
		employeeTaxes.GET("", h.ListEmployeeTaxes)
		employeeTaxes.POST("", h.CreateEmployeeTax)
		employeeTaxes.PUT("/:id", h.UpdateEmployeeTax)
		employeeTaxes.DELETE("/:id", h.DeleteEmployeeTax)
		employeeTaxes.POST("/preview", h.PreviewPayrollTaxes)
		// Record a payment against a tax-period liability (migration 360).
		// Posts a Dr-liability / Cr-cash journal entry and inserts a
		// row in employee_tax_payments so the Tax Reports → Employee
		// Taxes tab can subtract it from the accrued total to display
		// the running pending balance.
		employeeTaxes.POST("/payments", h.RecordEmployeeTaxPayment)
	}

	// ────────────── Company Tax Rates (migration 340) ──────────────
	// Activity-level taxes (NDS / Profit / Turnover / Dividend / …).
	// Complements /employee-taxes so Settings → Finance can present the
	// full 8-tax default catalog from TZ §1.2 of ТЗ_Ish_Haqi_Soliq_Tolik.
	companyTaxRates := rg.Group("/company-tax-rates")
	{
		companyTaxRates.GET("", h.ListCompanyTaxRates)
		companyTaxRates.POST("", h.CreateCompanyTaxRate)
		companyTaxRates.PUT("/:id", h.UpdateCompanyTaxRate)
		companyTaxRates.DELETE("/:id", h.DeleteCompanyTaxRate)
	}

	// ────────────── TT "Ish haqi" (payroll simple model) ──────────────
	// Settings, auto-create, mark-paid-with-day, and backup export.
	// Share the same "hr.payroll" permission with the existing payroll flow.
	payrollTT := rg.Group("/payroll")
	payrollTT.Use(h.perm.Require("hr", "payroll", "read"))
	{
		payrollTT.GET("/settings", h.GetPayrollSettings)
		payrollTT.PUT("/settings", h.perm.Require("hr", "payroll", "update"), h.UpdatePayrollSettings)
		payrollTT.POST("/periods/current-or-create",
			h.perm.Require("hr", "payroll", "create"), h.GetOrCreateCurrentMonthPayroll)
		payrollTT.POST("/entries/:id/advance-paid",
			h.perm.Require("hr", "payroll", "update"), h.MarkAdvancePaid)
		payrollTT.POST("/entries/:id/remainder-paid",
			h.perm.Require("hr", "payroll", "update"), h.MarkRemainderPaid)
		payrollTT.GET("/export", h.ExportPayrollBackup)
	}

	// Employee Loans
	loans := rg.Group("/employee-loans")
	{
		loans.GET("", h.ListEmployeeLoans)
		loans.POST("", h.perm.Require("hr", "payroll", "create"), h.CreateEmployeeLoan)
		loans.GET("/:id", h.GetEmployeeLoan)
		loans.POST("/:id/payments/:paymentId/mark-paid", h.perm.Require("hr", "payroll", "approve"), h.MarkLoanPaymentPaid)
	}

	// Employee Self-Service Portal
	myPortal := rg.Group("/my")
	{
		myPortal.GET("/profile", h.GetMyEmployeeProfile)
		myPortal.GET("/payroll-history", h.GetMyPayrollHistory)
		myPortal.GET("/loan", h.GetMyLoan)
	}

	// Expense Categories — list is open to anyone with read on expenses
	// (foremen need it for the submit-claim dropdown). Mutations are
	// gated to the same write permission as the rest of the expense
	// admin surface.
	expenseCategories := rg.Group("/expense-categories")
	{
		expenseCategories.GET("", h.ListExpenseCategories)
		expenseCategories.POST("", h.perm.Require("finance", "expense", "write"), h.CreateExpenseCategory)
		expenseCategories.PUT("/:id", h.perm.Require("finance", "expense", "write"), h.UpdateExpenseCategory)
		expenseCategories.DELETE("/:id", h.perm.Require("finance", "expense", "write"), h.DeleteExpenseCategory)
	}

	// Expenses
	expenses := rg.Group("/expenses")
	expenses.Use(h.perm.Require("finance", "expense", "read"))
	{
		expenses.GET("", h.ListExpenses)
		expenses.POST("", h.perm.Require("finance", "expense", "create"), h.CreateExpense)
		expenses.GET("/:id", h.GetExpense)
		expenses.PUT("/:id", h.perm.Require("finance", "expense", "update"), h.UpdateExpense)
		expenses.DELETE("/:id", h.perm.Require("finance", "expense", "delete"), h.DeleteExpense)
		expenses.POST("/:id/approve", h.perm.Require("finance", "expense", "approve"), h.ApproveExpense)
		// Dedicated recognition toggle — see RecognizeExpense / §7.2 of
		// ТЗ_Ish_Haqi_Soliq_Tolik.docx. Re-uses the generic "update"
		// permission; wire to its own perm node if/when RBAC grows.
		expenses.PATCH("/:id/recognize", h.perm.Require("finance", "expense", "update"), h.RecognizeExpense)
	}

	// Profit-tax calculation + snapshots (migrations 336/337)
	profitTax := rg.Group("/profit-tax")
	profitTax.Use(h.perm.Require("finance", "expense", "read"))
	{
		profitTax.GET("", h.GetProfitTax)
		profitTax.GET("/revenue", h.GetProfitTaxRevenue)
		profitTax.GET("/snapshots", h.ListProfitTaxSnapshots)
		profitTax.POST("/snapshot", h.perm.Require("finance", "expense", "approve"), h.SnapshotProfitTax)
	}

	// Turnover tax calculator (TZ §5.2). Driven by company_tax_rates
	// applies_to='turnover'. Same revenue source as profit-tax to keep
	// the two calculators reconciled.
	turnoverTax := rg.Group("/turnover-tax")
	turnoverTax.Use(h.perm.Require("finance", "expense", "read"))
	{
		turnoverTax.GET("", h.GetTurnoverTax)
	}

	// NDS/VAT per-period calculator (TZ §5.1). Sums stamped tax_amount on
	// sales and purchase invoices and returns realizatsiya / zachet /
	// balansi. Rate display pulls from company_tax_rates applies_to='sales'.
	ndsTax := rg.Group("/nds-tax")
	ndsTax.Use(h.perm.Require("finance", "expense", "read"))
	{
		ndsTax.GET("", h.GetNdsTax)
	}

	// Dividend withholding helper (TZ §5.3). GET/POST compute-only preview;
	// POST /dividend-distributions actually posts the journal entry.
	dividendTax := rg.Group("/dividend-tax")
	dividendTax.Use(h.perm.Require("finance", "expense", "read"))
	{
		dividendTax.GET("", h.ComputeDividendTax)
		dividendTax.POST("/compute", h.ComputeDividendTax)
	}
	dividendDist := rg.Group("/dividend-distributions")
	{
		dividendDist.POST("", h.perm.Require("finance", "expense", "approve"), h.CreateDividendDistribution)
	}

	// Combined tax summary — director-level dashboard (TZ §10 "Umumiy soliq
	// jamlanmasi"). Aggregates payroll + NDS + Foyda + Aylanma + Dividend
	// rate into one response so the UI renders the whole picture in one
	// round-trip.
	taxSummary := rg.Group("/tax-summary")
	taxSummary.Use(h.perm.Require("finance", "expense", "read"))
	{
		taxSummary.GET("/combined", h.GetTaxSummaryCombined)
	}

	// Asset Categories
	assetCategories := rg.Group("/asset-categories")
	{
		assetCategories.GET("", h.ListAssetCategories)
		assetCategories.POST("", h.perm.Require("finance", "asset", "create"), h.CreateAssetCategory)
		assetCategories.PUT("/:id", h.perm.Require("finance", "asset", "update"), h.UpdateAssetCategory)
		assetCategories.DELETE("/:id", h.perm.Require("finance", "asset", "delete"), h.DeleteAssetCategory)
	}

	// Fixed Assets
	fixedAssets := rg.Group("/fixed-assets")
	fixedAssets.Use(h.perm.Require("finance", "asset", "read"))
	{
		fixedAssets.GET("", h.ListFixedAssets)
		fixedAssets.GET("/dashboard", h.GetAssetDashboard)
		fixedAssets.POST("", h.perm.Require("finance", "asset", "create"), h.CreateFixedAsset)
		fixedAssets.GET("/:id", h.GetFixedAsset)
		fixedAssets.PUT("/:id", h.perm.Require("finance", "asset", "update"), h.UpdateFixedAsset)
		fixedAssets.DELETE("/:id", h.perm.Require("finance", "asset", "delete"), h.DeleteFixedAsset)
		fixedAssets.POST("/:id/dispose", h.perm.Require("finance", "asset", "approve"), h.DisposeFixedAsset)
		fixedAssets.GET("/:id/depreciation", h.GetDepreciationEntries)
		fixedAssets.POST("/:id/maintenance", h.perm.Require("finance", "asset", "update"), h.RecordMaintenance)
		fixedAssets.GET("/:id/maintenance", h.ListMaintenanceHistory)
		fixedAssets.POST("/:id/payments", h.perm.Require("finance", "asset", "create"), h.RecordAssetPayment)
		fixedAssets.GET("/:id/payments", h.ListAssetPayments)
	}

	// Run Depreciation (batch operation)
	rg.POST("/run-depreciation", h.perm.Require("finance", "asset", "approve"), h.RunDepreciation)

	// =====================================================
	// FIXED ASSETS v2 — register, lifecycle, auto-depreciation (TZ v1.0)
	// =====================================================
	assetsV2 := rg.Group("/assets")
	assetsV2.Use(h.perm.Require("finance", "asset", "read"))
	{
		assetsV2.GET("", h.ListAssets)
		assetsV2.POST("", h.perm.Require("finance", "asset", "create"), h.CreateAsset)
		assetsV2.GET("/:id", h.GetAsset)
		assetsV2.GET("/:id/schedule", h.GetAssetSchedule)
		assetsV2.POST("/:id/commission", h.perm.Require("finance", "asset", "update"), h.CommissionAsset)
		assetsV2.POST("/:id/conserve", h.perm.Require("finance", "asset", "update"), h.ConserveAsset)
		assetsV2.POST("/:id/reactivate", h.perm.Require("finance", "asset", "update"), h.ReactivateAsset)
		assetsV2.POST("/:id/dispose", h.perm.Require("finance", "asset", "approve"), h.DisposeAsset)
		assetsV2.POST("/:id/change-params", h.perm.Require("finance", "asset", "update"), h.ChangeAssetParams)
		// Per-asset GL override — gated by the dedicated accounting permission (§2.6).
		assetsV2.PATCH("/:id/accounts", h.perm.Require("accounting", "asset_accounts", "override"), h.PatchAssetAccounts)
	}

	depreciation := rg.Group("/depreciation/runs")
	depreciation.Use(h.perm.Require("finance", "asset", "read"))
	{
		depreciation.POST("", h.perm.Require("finance", "asset", "approve"), h.CreateDepreciationRunHandler)
		depreciation.GET("/:id", h.GetDepreciationRun)
		depreciation.POST("/:id/exclude", h.perm.Require("finance", "asset", "approve"), h.ExcludeFromRun)
		depreciation.POST("/:id/post", h.perm.Require("finance", "asset", "approve"), h.PostDepreciationRun)
		depreciation.POST("/:id/reverse", h.perm.Require("finance", "asset", "approve"), h.ReverseDepreciationRun)
	}

	// Asset account mapping (§2.5) — read for form population, edit gated by
	// the accounting.edit_mapping permission.
	assetMapping := rg.Group("/settings/asset-mapping")
	{
		assetMapping.GET("", h.GetAssetMapping)
		assetMapping.PUT("", h.perm.Require("accounting", "asset_mapping", "edit"), h.UpdateAssetMapping)
	}
	rg.GET("/asset-mapping/accounts", h.ListChartAccountsFiltered)

	// Projects
	projects := rg.Group("/projects")
	projects.Use(h.perm.Require("projects", "project", "read"))
	{
		projects.GET("", h.ListProjects)
		projects.GET("/by-organization", h.ListProjectsByOrganization)
		projects.POST("", h.perm.Require("projects", "project", "create"), h.CreateProject)
		projects.GET("/:id", h.GetProject)
		projects.PUT("/:id", h.perm.Require("projects", "project", "update"), h.UpdateProject)
		projects.DELETE("/:id", h.perm.Require("projects", "project", "delete"), h.DeleteProject)

		// Project Tasks
		projects.GET("/:id/tasks", h.ListProjectTasks)
		projects.POST("/:id/tasks", h.perm.Require("projects", "task", "create"), h.CreateProjectTask)
		projects.PUT("/:id/tasks/:taskId", h.perm.Require("projects", "task", "update"), h.UpdateProjectTask)
		projects.DELETE("/:id/tasks/:taskId", h.perm.Require("projects", "task", "delete"), h.DeleteProjectTask)

		// Project Milestones
		projects.GET("/:id/milestones", h.ListProjectMilestones)
		projects.POST("/:id/milestones", h.perm.Require("projects", "milestone", "create"), h.CreateProjectMilestone)
		projects.PUT("/:id/milestones/:milestoneId", h.perm.Require("projects", "milestone", "update"), h.UpdateProjectMilestone)
		projects.DELETE("/:id/milestones/:milestoneId", h.perm.Require("projects", "milestone", "delete"), h.DeleteProjectMilestone)

		// Time Entries
		projects.GET("/:id/time-entries", h.ListTimeEntries)
		projects.POST("/:id/time-entries", h.perm.Require("projects", "time_entry", "create"), h.CreateTimeEntry)

		// Project Expenses
		projects.GET("/:id/expenses", h.ListProjectExpenses)
		projects.POST("/:id/expenses", h.perm.Require("projects", "expense", "create"), h.CreateProjectExpense)
		projects.DELETE("/:id/expenses/:expenseId", h.perm.Require("projects", "expense", "delete"), h.DeleteProjectExpense)

		// Project Team Members
		projects.GET("/:id/team-members", h.ListProjectTeamMembers)
		projects.POST("/:id/team-members", h.perm.Require("projects", "project", "update"), h.AddProjectTeamMember)
		projects.DELETE("/:id/team-members/:memberId", h.perm.Require("projects", "project", "update"), h.RemoveProjectTeamMember)
	}

	// =====================================================
	// CARGO MODULE ROUTES
	// =====================================================

	// Cargo Shipments
	cargoShipments := rg.Group("/cargo/shipments")
	cargoShipments.Use(h.perm.Require("cargo", "shipment", "read"))
	{
		cargoShipments.GET("", h.ListCargoShipments)
		cargoShipments.POST("", h.perm.Require("cargo", "shipment", "create"), h.CreateCargoShipment)
		cargoShipments.GET("/:id", h.GetCargoShipment)
		cargoShipments.PUT("/:id", h.perm.Require("cargo", "shipment", "update"), h.UpdateCargoShipment)
		cargoShipments.PUT("/:id/status", h.perm.Require("cargo", "shipment", "update"), h.UpdateCargoShipmentStatus)
		cargoShipments.DELETE("/:id", h.perm.Require("cargo", "shipment", "delete"), h.DeleteCargoShipment)
		cargoShipments.POST("/:id/distribution", h.perm.Require("cargo", "distribution", "create"), h.CreateCargoDistribution)
	}

	// Cargo Cash Register
	cargoCash := rg.Group("/cargo/cash")
	cargoCash.Use(h.perm.Require("cargo", "cash", "read"))
	{
		cargoCash.GET("", h.ListCargoCashTransactions)
		cargoCash.POST("", h.perm.Require("cargo", "cash", "create"), h.CreateCargoCashTransaction)
		cargoCash.PUT("/:id", h.perm.Require("cargo", "cash", "update"), h.UpdateCargoCashTransaction)
		cargoCash.DELETE("/:id", h.perm.Require("cargo", "cash", "delete"), h.DeleteCargoCashTransaction)
		cargoCash.GET("/summary", h.GetCargoCashSummary)
	}

	// =====================================================
	// CONSTRUCTION MODULE ROUTES
	// =====================================================

	// Construction Projects
	constructionProjects := rg.Group("/construction/projects")
	constructionProjects.Use(h.perm.Require("construction", "project", "read"))
	{
		constructionProjects.GET("", h.ListConstructionProjects)
		constructionProjects.POST("", h.perm.Require("construction", "project", "create"), h.CreateConstructionProject)
		constructionProjects.GET("/:id", h.GetConstructionProject)
		constructionProjects.PUT("/:id", h.perm.Require("construction", "project", "update"), h.UpdateConstructionProject)
		constructionProjects.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteConstructionProject)
		constructionProjects.GET("/:id/dashboard", h.GetConstructionProjectDashboard)
		// Buildings/Blocks
		constructionProjects.GET("/:id/buildings", h.ListConstructionBuildings)
		constructionProjects.POST("/:id/buildings", h.perm.Require("construction", "project", "create"), h.CreateConstructionBuilding)
		constructionProjects.PUT("/:id/buildings/:building_id", h.perm.Require("construction", "project", "update"), h.UpdateConstructionBuilding)
		constructionProjects.DELETE("/:id/buildings/:building_id", h.perm.Require("construction", "project", "delete"), h.DeleteConstructionBuilding)
		// Clone all estimates (Единич, ВОР, Ресурс) + their lines from a
		// sibling block. Target must be empty; same project only. Used by
		// the "Klonlash" entry in the block card "..." menu.
		constructionProjects.POST("/:id/buildings/:building_id/clone-estimates", h.perm.Require("construction", "smeta", "create"), h.CloneBuildingEstimates)
		// CRM linkage — set/clear crm_project_id on the project and
		// crm_block_id / current_crm_stage / auto_sync on the building.
		constructionProjects.PUT("/:id/crm-link", h.perm.Require("construction", "project", "update"), h.UpdateProjectCRMLink)
		constructionProjects.PUT("/:id/buildings/:building_id/crm-link", h.perm.Require("construction", "project", "update"), h.UpdateBuildingCRMLink)
		// Building Files
		constructionProjects.GET("/:id/buildings/:building_id/files", h.ListBuildingFiles)
		constructionProjects.POST("/:id/buildings/:building_id/files", h.perm.Require("construction", "project", "create"), h.CreateBuildingFile)
		constructionProjects.DELETE("/:id/buildings/:building_id/files/:file_id", h.perm.Require("construction", "project", "delete"), h.DeleteBuildingFile)
		// Project Files
		constructionProjects.GET("/:id/files", h.ListProjectFiles)
		constructionProjects.POST("/:id/files", h.perm.Require("construction", "project", "create"), h.CreateProjectFile)
		constructionProjects.DELETE("/:id/files/:file_id", h.perm.Require("construction", "project", "delete"), h.DeleteProjectFile)
		// Smeta Sections
		constructionProjects.GET("/:id/sections", h.ListSmetaSections)
		constructionProjects.POST("/:id/sections", h.perm.Require("construction", "smeta", "create"), h.CreateSmetaSection)

		// Project Vendors
		constructionProjects.GET("/:id/vendors", h.ListProjectVendors)
		constructionProjects.POST("/:id/vendors", h.perm.Require("construction", "projects", "update"), h.CreateProjectVendor)

		// Photo Reports
		constructionProjects.GET("/:id/photo-reports", h.ListPhotoReports)
		constructionProjects.POST("/:id/photo-reports", h.perm.Require("construction", "reports", "submit"), h.CreatePhotoReport)

		// Daily Reports
		constructionProjects.GET("/:id/daily-reports", h.ListDailyReports)
		constructionProjects.POST("/:id/daily-reports", h.perm.Require("construction", "reports", "submit"), h.CreateDailyReport)

		// Material Requests
		constructionProjects.GET("/:id/material-requests", h.ListMaterialRequests)
		constructionProjects.POST("/:id/material-requests", h.perm.Require("construction", "projects", "update"), h.CreateMaterialRequest)

		// Project Materials (tracked approved materials per project)
		constructionProjects.GET("/:id/project-materials", h.ListProjectMaterials)

		// Deliveries
		constructionProjects.GET("/:id/deliveries", h.ListDeliveries)

		// Site Warehouses
		constructionProjects.GET("/:id/warehouses", h.ListSiteWarehouses)
		constructionProjects.POST("/:id/warehouses", h.perm.Require("construction", "projects", "update"), h.CreateSiteWarehouse)

		// Team Members
		constructionProjects.GET("/:id/team", h.ListConstructionTeamMembers)
		constructionProjects.POST("/:id/team", h.perm.Require("construction", "project", "update"), h.CreateConstructionTeamMember)
		constructionProjects.PUT("/:id/team/:memberId", h.perm.Require("construction", "project", "update"), h.UpdateConstructionTeamMember)
		constructionProjects.DELETE("/:id/team/:memberId", h.perm.Require("construction", "project", "update"), h.DeleteConstructionTeamMember)
		constructionProjects.POST("/:id/team/:memberId/transfer", h.perm.Require("construction", "project", "update"), h.TransferTeamMember)

		// WBS (Work Breakdown Structure)
		constructionProjects.GET("/:id/wbs", h.ListWBSItems)
		constructionProjects.GET("/:id/wbs/tree", h.GetWBSTree)
		constructionProjects.POST("/:id/wbs", h.perm.Require("construction", "wbs", "create"), h.CreateWBSItem)

		// Activity Log
		constructionProjects.GET("/:id/activity-log", h.ListConstructionActivityLog)

		// Estimate Resources (grouped by type for substage dropdowns)
		constructionProjects.GET("/:id/estimate-resources", h.ListProjectEstimateResources)
		// Create a new project resource (lands in the project's catalog
		// estimate). Used by the AddResourcePickerModal "+" button.
		constructionProjects.POST("/:id/resources", h.CreateProjectResource)

		// Resource prices — Smeta boshqaruvi → Resurslar tab. Bulk-edits
		// resource unit prices across every matching estimate line in the
		// project, with audit history.
		constructionProjects.GET("/:id/resource-prices", h.ListResourcePrices)
		constructionProjects.POST("/:id/resource-prices/bulk-update",
			h.perm.Require("construction", "estimate", "update"), h.BulkUpdateResourcePrice)
		constructionProjects.POST("/:id/resource-prices/material-type",
			h.perm.Require("construction", "estimate", "update"), h.BulkUpdateResourceMaterialType)
		// Reset-to-original (migration 349 anchors). Per-resource and project-wide.
		constructionProjects.POST("/:id/resource-prices/reset",
			h.perm.Require("construction", "estimate", "update"), h.ResetResourcePrice)
		constructionProjects.POST("/:id/resource-prices/reset-all",
			h.perm.Require("construction", "estimate", "update"), h.ResetAllResourcePrices)
		constructionProjects.GET("/:id/resource-prices/history", h.GetResourcePriceHistory)

		// Estimates
		constructionProjects.GET("/:id/estimates", h.ListEstimates)
		constructionProjects.POST("/:id/estimates", h.perm.Require("construction", "estimate", "create"), h.CreateEstimate)

		// Estimate Summary (Свод)
		constructionProjects.GET("/:id/estimate-summary", h.ListEstimateSummary)
		constructionProjects.POST("/:id/estimate-summary/import", h.perm.Require("construction", "estimate", "create"), h.ImportEstimateSummary)

		// Daily Logs (WBS-linked)
		constructionProjects.GET("/:id/daily-logs", h.ListConstructionDailyLogs)
		constructionProjects.POST("/:id/daily-logs", h.perm.Require("construction", "daily_log", "create"), h.CreateConstructionDailyLog)
		constructionProjects.GET("/:id/daily-logs/summary", h.GetDailyLogSummary)

		// Stages
		constructionProjects.GET("/:id/stages", h.ListConstructionStages)
		constructionProjects.GET("/:id/stages/overview", h.GetConstructionStagesOverview)
		constructionProjects.POST("/:id/stages", h.perm.Require("construction", "project", "update"), h.CreateConstructionStage)

		// Flat feed of everything currently in_progress (stages + sub-stages).
		// Drives the "Jarayon" tab on the frontend.
		constructionProjects.GET("/:id/in-progress", h.GetProjectInProgressItems)

		// Expense Lines (Xarajat operatsiyalari)
		constructionProjects.GET("/:id/expenses", h.ListExpenseLines)
		constructionProjects.POST("/:id/expenses", h.perm.Require("construction", "project", "update"), h.CreateExpenseLine)

		// Material Usage
		constructionProjects.GET("/:id/material-usage", h.ListMaterialUsage)
		constructionProjects.POST("/:id/material-usage", h.perm.Require("construction", "project", "update"), h.CreateMaterialUsage)
		constructionProjects.GET("/:id/material-summary", h.GetMaterialSummary)

		// Progress Visualization
		constructionProjects.GET("/:id/progress-summary", h.GetProgressSummary)
		constructionProjects.GET("/:id/gantt", h.GetGanttData)

		// Subcontracts
		constructionProjects.GET("/:id/subcontracts", h.ListSubcontracts)
		constructionProjects.POST("/:id/subcontracts", h.perm.Require("construction", "project", "update"), h.CreateSubcontract)

		// Acts (Forma 2 / Forma 3 / Forma 19)
		constructionProjects.GET("/:id/acts", h.ListConstructionActs)
		constructionProjects.POST("/:id/acts", h.perm.Require("construction", "project", "update"), h.CreateConstructionAct)
		constructionProjects.POST("/:id/acts/generate-ks2/preview", h.perm.Require("construction", "project", "read"), h.PreviewAutoGenerateKS2)
		constructionProjects.POST("/:id/acts/generate-ks2", h.perm.Require("construction", "project", "update"), h.AutoGenerateKS2)
		constructionProjects.POST("/:id/acts/generate-ks3", h.perm.Require("construction", "project", "update"), h.GenerateForma3)

		// Forma 19 — Material consumption tracking
		constructionProjects.GET("/:id/f19", h.ListForma19)
		constructionProjects.POST("/:id/f19", h.perm.Require("construction", "project", "update"), h.CreateForma19)
		constructionProjects.GET("/:id/f19/:actId", h.GetForma19Detail)
		constructionProjects.POST("/:id/f19/:actId/change-row", h.perm.Require("construction", "project", "update"), h.AddF19ChangeRow)
		constructionProjects.PUT("/:id/f19/:actId/rows/:rowId", h.perm.Require("construction", "project", "update"), h.UpdateF19Row)
		constructionProjects.POST("/:id/f19/:actId/approve", h.perm.Require("construction", "project", "update"), h.ApproveForma19)
		constructionProjects.DELETE("/:id/f19/:actId", h.perm.Require("construction", "project", "delete"), h.DeleteForma19)

		// Smeta vs Fact analytics
		constructionProjects.GET("/:id/smeta-vs-fact", h.GetSmetaVsFact)

		// Financial Analysis
		constructionProjects.GET("/:id/financial/pnl", h.GetProjectPnL)
		constructionProjects.GET("/:id/financial/budget-actual", h.GetBudgetVsActualByWBS)
		constructionProjects.GET("/:id/financial/cost-trend", h.GetCostTrend)
		constructionProjects.GET("/:id/financial/payment-schedule", h.GetPaymentSchedule)

		// Reports
		constructionProjects.GET("/:id/reports/summary", h.GetProjectSummaryReport)
		constructionProjects.GET("/:id/reports/budget", h.GetStageBudgetReport)
		constructionProjects.GET("/:id/reports/svod", h.GetSvodReport)
		constructionProjects.GET("/:id/reports/material-consolidation", h.GetMaterialConsolidationReport)
		constructionProjects.GET("/:id/reports/materials", h.GetMaterialsReport)
		constructionProjects.GET("/:id/reports/journal-entries", h.GetJournalEntriesReport)

		// Reja vs Fakt (Plan vs Fact)
		constructionProjects.GET("/:id/reja-fakt", h.GetRejaFakt)
		constructionProjects.GET("/:id/reja-fakt/audit", h.ListRejaFaktAudit)

		// Smeta audit log (project-wide, every estimate).
		constructionProjects.GET("/:id/smeta-audit", h.ListProjectSmetaAudit)

		// v2 Stages workflow: who am I on this project?
		constructionProjects.GET("/:id/my-role", h.GetMyProjectRole)

		// Commission (complete project)
		constructionProjects.PUT("/:id/commission", h.perm.Require("construction", "project", "update"), h.CommissionProject)
	}

	// Project Vendors (direct access for update/delete)
	projectVendors := rg.Group("/construction/project-vendors")
	projectVendors.Use(h.perm.Require("construction", "projects", "read"))
	{
		projectVendors.PUT("/:id", h.perm.Require("construction", "projects", "update"), h.UpdateProjectVendor)
		projectVendors.DELETE("/:id", h.perm.Require("construction", "projects", "delete"), h.DeleteProjectVendor)
	}

	// Smeta Sections (direct access)
	smetaSections := rg.Group("/construction/sections")
	smetaSections.Use(h.perm.Require("construction", "smeta", "read"))
	{
		smetaSections.PUT("/:id", h.perm.Require("construction", "smeta", "update"), h.UpdateSmetaSection)
		smetaSections.DELETE("/:id", h.perm.Require("construction", "smeta", "delete"), h.DeleteSmetaSection)
		// Smeta Items
		smetaSections.GET("/:id/items", h.ListSmetaItems)
		smetaSections.POST("/:id/items", h.perm.Require("construction", "smeta", "create"), h.CreateSmetaItem)
	}

	// Smeta Items (direct access)
	smetaItems := rg.Group("/construction/smeta-items")
	smetaItems.Use(h.perm.Require("construction", "smeta", "read"))
	{
		smetaItems.PUT("/:id", h.perm.Require("construction", "smeta", "update"), h.UpdateSmetaItem)
		smetaItems.DELETE("/:id", h.perm.Require("construction", "smeta", "delete"), h.DeleteSmetaItem)
	}

	// Material Requests (direct access)
	materialRequests := rg.Group("/construction/material-requests")
	materialRequests.Use(h.perm.Require("construction", "projects", "read"))
	{
		materialRequests.PUT("/:id", h.perm.Require("construction", "projects", "update"), h.UpdateMaterialRequest)
		materialRequests.PUT("/:id/approve", h.perm.Require("construction", "projects", "update"), h.ApproveMaterialRequest)
		materialRequests.DELETE("/:id", h.perm.Require("construction", "projects", "delete"), h.DeleteMaterialRequest)
	}

	// Daily Reports (direct access)
	dailyReports := rg.Group("/construction/daily-reports")
	dailyReports.Use(h.perm.Require("construction", "reports", "read"))
	{
		dailyReports.GET("/:id", h.GetDailyReport)
		dailyReports.PUT("/:id", h.perm.Require("construction", "reports", "submit"), h.UpdateDailyReport)
		dailyReports.DELETE("/:id", h.perm.Require("construction", "reports", "submit"), h.DeleteDailyReport)
	}

	// WBS Items (direct access for update/delete)
	wbsItems := rg.Group("/construction/wbs")
	wbsItems.Use(h.perm.Require("construction", "wbs", "read"))
	{
		wbsItems.PUT("/:id", h.perm.Require("construction", "wbs", "update"), h.UpdateWBSItem)
		wbsItems.DELETE("/:id", h.perm.Require("construction", "wbs", "delete"), h.DeleteWBSItem)
		wbsItems.POST("/reorder", h.perm.Require("construction", "wbs", "update"), h.ReorderWBSItems)
	}

	// Estimates (direct access)
	estimates := rg.Group("/construction/estimates")
	estimates.Use(h.perm.Require("construction", "estimate", "read"))
	{
		estimates.GET("/:id", h.GetEstimate)
		estimates.PUT("/:id", h.perm.Require("construction", "estimate", "update"), h.UpdateEstimate)
		estimates.DELETE("/:id", h.perm.Require("construction", "estimate", "delete"), h.DeleteEstimate)
		estimates.POST("/:id/approve", h.perm.Require("construction", "estimate", "update"), h.ApproveEstimate)
		estimates.POST("/:id/duplicate", h.perm.Require("construction", "estimate", "create"), h.DuplicateEstimate)
		// Estimate stat-card aggregates (cost breakdown + counts) computed
		// server-side so mobile doesn't have to download every line to show
		// the headline numbers. See construction_estimate_summary.go.
		estimates.GET("/:id/summary", h.GetEstimateSummary)
		// Server-side calcs for mobile (which can't replicate the web math):
		// Bosqichlar progress roll-ups + Forma 2 money summary.
		estimates.GET("/:id/stages-progress", h.GetEstimateStagesProgress)
		estimates.GET("/:id/forma2-summary", h.GetEstimateForma2Summary)
		// Estimate Lines
		estimates.GET("/:id/lines", h.ListEstimateLines)
		estimates.POST("/:id/lines", h.perm.Require("construction", "estimate", "update"), h.CreateEstimateLine)
		estimates.POST("/:id/lines/bulk", h.perm.Require("construction", "estimate", "update"), h.BulkCreateEstimateLines)
		// Clone-by-code: create a new line and, if an existing parent line with
		// the same code lives anywhere in the project, copy all of its
		// resources (sub-lines) onto the new one. See CloneEstimateLineByCode.
		estimates.POST("/:id/lines/clone-by-code", h.perm.Require("construction", "estimate", "update"), h.CloneEstimateLineByCode)
		estimates.POST("/:id/create-products", h.perm.Require("construction", "estimate", "update"), h.CreateProductsFromEstimate)
		estimates.PUT("/:id/lines/:line_id", h.perm.Require("construction", "estimate", "update"), h.UpdateEstimateLine)
		estimates.DELETE("/:id/lines/:line_id", h.perm.Require("construction", "estimate", "update"), h.DeleteEstimateLine)
		// Reset a single line's quantity back to its original_quantity anchor (migration 349).
		estimates.POST("/:id/lines/:line_id/reset-quantity",
			h.perm.Require("construction", "estimate", "update"), h.ResetEstimateLineQuantity)
		// Bulk-zero every top-level work qty in this estimate. Non-override
		// sub-line qtys cascade to 0 via parent.qty × norm_rate.
		estimates.POST("/:id/reset-quantities",
			h.perm.Require("construction", "estimate", "update"), h.ResetAllEstimateQuantities)

		// Resource top-ups — when a foreman orders MORE of a material
		// than the smeta planned (often at a different price). Each
		// top-up is appended to construction_resource_topup; the
		// estimate's amount_total recomputes to fold them in.
		estimates.POST("/:id/lines/:line_id/topups",
			h.perm.Require("construction", "estimate", "update"), h.CreateResourceTopup)
		estimates.DELETE("/:id/lines/:line_id/topups/:topup_id",
			h.perm.Require("construction", "estimate", "update"), h.DeleteResourceTopup)

		// Forma 2 snapshots — Smeta boshqaruvi → Tarix tab.
		estimates.GET("/:id/form2-snapshots", h.ListForm2Snapshots)
		estimates.POST("/:id/form2-snapshots",
			h.perm.Require("construction", "estimate", "update"), h.CreateForm2Snapshot)

		// Smeta boshqaruvi → Jurnal tab (audit log scoped to one estimate).
		estimates.GET("/:id/audit", h.ListSmetaAudit)
	}

	// Forma 2 snapshot direct access (get/delete by snapshot id).
	form2Snapshots := rg.Group("/construction/form2-snapshots")
	form2Snapshots.Use(h.perm.Require("construction", "estimate", "read"))
	{
		form2Snapshots.GET("/:snapshot_id", h.GetForm2Snapshot)
		form2Snapshots.DELETE("/:snapshot_id",
			h.perm.Require("construction", "estimate", "update"), h.DeleteForm2Snapshot)
	}

	// Forma 2 iterations — multi-run series (migration 419). The iter
	// header lives at the project level (one open per project); the
	// Bosqichlar tab uses these to render "Forma 2 #1, #2, #3" tabs.
	// Freezing an iteration also writes a construction_form2_snapshot
	// row so the Smeta boshqaruvi → Formalar tarixi list never drifts
	// out of sync with the iteration tab strip.
	form2Iters := rg.Group("/construction/projects/:id/form2-iterations")
	form2Iters.Use(h.perm.Require("construction", "estimate", "read"))
	{
		form2Iters.GET("", h.ListForm2Iterations)
		form2Iters.POST("",
			h.perm.Require("construction", "estimate", "update"), h.CreateForm2Iteration)
		form2Iters.GET("/:iter_id/lines", h.GetForm2IterationLines)
		// Delete (undo) the most recent frozen Forma 2 — re-opens it and
		// drops the empty open iteration + its snapshot. Gated on the
		// estimate "delete" action so the frontend can hide the button for
		// users without it.
		form2Iters.DELETE("/:iter_id",
			h.perm.Require("construction", "estimate", "delete"), h.DeleteForm2Iteration)
	}

	// Project-level Forma 2 snapshot history. Lists every saved Forma 2 for
	// the whole project (across all blocks/estimates), mirroring the
	// project-wide iteration strip — used by Smeta boshqaruvi → Formalar
	// tarixi so freezes made from any block stay visible regardless of which
	// block is currently selected.
	projectForm2Snapshots := rg.Group("/construction/projects/:id/form2-snapshots")
	projectForm2Snapshots.Use(h.perm.Require("construction", "estimate", "read"))
	{
		projectForm2Snapshots.GET("", h.ListProjectForm2Snapshots)
	}

	// v2 Stages workflow — work approval transitions.
	// Permissions are enforced inside the handlers based on the caller's
	// project role (foreman / supervisor / engineer); the route-level
	// "estimate update" gate keeps random users out.
	works := rg.Group("/construction/works")
	works.Use(h.perm.Require("construction", "estimate", "update"))
	{
		// Foreman.
		works.POST("/:id/done-quantity", h.UpdateWorkDoneQuantity)
		works.POST("/:id/submit", h.SubmitWork)
		works.POST("/bulk-submit", h.BulkSubmitWorks)
		// Supervisor.
		works.POST("/:id/confirm-supervisor", h.ConfirmWorkSupervisor)
		works.POST("/:id/reject-supervisor", h.RejectWorkSupervisor)
		works.POST("/bulk-confirm-supervisor", h.BulkConfirmSupervisor)
		// Engineer (final).
		works.POST("/:id/confirm-engineer", h.ConfirmWorkEngineer)
		works.POST("/:id/reject-engineer", h.RejectWorkEngineer)
		works.POST("/bulk-confirm-engineer", h.BulkConfirmEngineer)
	}

	// Estimate Summary (direct access)
	estimateSummary := rg.Group("/construction/estimate-summary")
	estimateSummary.Use(h.perm.Require("construction", "estimate", "read"))
	{
		estimateSummary.DELETE("/:batch_id", h.perm.Require("construction", "estimate", "delete"), h.DeleteEstimateSummaryBatch)
	}

	// Construction Daily Logs (direct access)
	constructionDailyLogs := rg.Group("/construction/daily-logs")
	constructionDailyLogs.Use(h.perm.Require("construction", "daily_log", "read"))
	{
		constructionDailyLogs.PUT("/:id", h.perm.Require("construction", "daily_log", "update"), h.UpdateConstructionDailyLog)
		constructionDailyLogs.DELETE("/:id", h.perm.Require("construction", "daily_log", "delete"), h.DeleteConstructionDailyLog)
	}

	// Construction Stages (direct access for update/delete)
	constructionStages := rg.Group("/construction/stages")
	constructionStages.Use(h.perm.Require("construction", "project", "read"))
	{
		constructionStages.PUT("/:id", h.perm.Require("construction", "project", "update"), h.UpdateConstructionStage)
		constructionStages.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteConstructionStage)
		constructionStages.GET("/:id/works", h.GetConstructionStageWorks)
		constructionStages.GET("/:id/sub-stages", h.ListConstructionSubStages)
		constructionStages.POST("/:id/sub-stages", h.perm.Require("construction", "project", "update"), h.CreateConstructionSubStage)
		constructionStages.GET("/:id/materials", h.ListStageMaterials)
	}

	// Construction Sub-Stages (direct access for update/delete)
	constructionSubStages := rg.Group("/construction/sub-stages")
	constructionSubStages.Use(h.perm.Require("construction", "project", "read"))
	{
		constructionSubStages.PUT("/:id", h.perm.Require("construction", "project", "update"), h.UpdateConstructionSubStage)
		constructionSubStages.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteConstructionSubStage)
		constructionSubStages.GET("/:id/materials", h.ListSubStageMaterials)
		constructionSubStages.POST("/:id/materials", h.perm.Require("construction", "project", "update"), h.CreateSubStageMaterial)
		// Equipment
		constructionSubStages.GET("/:id/equipment", h.ListSubStageEquipment)
		constructionSubStages.POST("/:id/equipment", h.perm.Require("construction", "project", "update"), h.CreateSubStageEquipment)
	}

	// Construction Sub-Stage Materials (direct access for update/delete)
	subStageMaterials := rg.Group("/construction/sub-stage-materials")
	subStageMaterials.Use(h.perm.Require("construction", "project", "read"))
	{
		subStageMaterials.PUT("/:id", h.perm.Require("construction", "project", "update"), h.UpdateSubStageMaterial)
		subStageMaterials.PUT("/:id/plan-fact", h.perm.Require("construction", "project", "update"), h.UpdateSubStageMaterialPlanFact)
		subStageMaterials.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteSubStageMaterial)
	}

	// Construction Sub-Stage Equipment (direct access for update/delete)
	subStageEquipment := rg.Group("/construction/sub-stage-equipment")
	subStageEquipment.Use(h.perm.Require("construction", "project", "read"))
	{
		subStageEquipment.PUT("/:id", h.perm.Require("construction", "project", "update"), h.UpdateSubStageEquipment)
		subStageEquipment.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteSubStageEquipment)
	}

	// Expense Lines (direct access for update/delete/approve/cancel)
	constructionExpenses := rg.Group("/construction/expenses")
	constructionExpenses.Use(h.perm.Require("construction", "project", "read"))
	{
		constructionExpenses.PUT("/:id", h.perm.Require("construction", "project", "update"), h.UpdateExpenseLine)
		constructionExpenses.DELETE("/:id", h.perm.Require("construction", "project", "update"), h.DeleteExpenseLine)
		constructionExpenses.PUT("/:id/approve", h.perm.Require("construction", "project", "update"), h.ApproveExpenseLine)
		constructionExpenses.PUT("/:id/cancel", h.perm.Require("construction", "project", "update"), h.CancelExpenseLine)
	}

	// Material Reservations (Materiallar zaxirasi)
	materialReservations := rg.Group("/material-reservations")
	materialReservations.Use(h.perm.Require("inventory", "stock", "read"))
	{
		materialReservations.GET("", h.ListMaterialReservations)
		materialReservations.POST("", h.perm.Require("construction", "project", "update"), h.CreateMaterialReservation)
		materialReservations.PUT(":id/approve", h.perm.Require("inventory", "stock", "adjust"), h.ApproveMaterialReservation)
		materialReservations.PUT(":id/reject", h.perm.Require("inventory", "stock", "adjust"), h.RejectMaterialReservation)
		materialReservations.DELETE(":id", h.perm.Require("construction", "project", "update"), h.DeleteMaterialReservation)
	}

	// Cost Categories & Account Mapping (settings)
	constructionSettings := rg.Group("/construction")
	constructionSettings.Use(h.perm.Require("construction", "project", "read"))
	{
		constructionSettings.GET("/cost-categories", h.ListCostCategories)
		constructionSettings.POST("/cost-categories", h.perm.Require("construction", "project", "update"), h.CreateCostCategory)
		constructionSettings.PUT("/cost-categories/:id", h.perm.Require("construction", "project", "update"), h.UpdateCostCategory)
		constructionSettings.GET("/account-mapping", h.GetAccountMapping)
		constructionSettings.PUT("/account-mapping", h.perm.Require("construction", "project", "update"), h.UpsertAccountMapping)
		// Portfolio dashboard (cross-project)
		constructionSettings.GET("/dashboard", h.GetConstructionPortfolioDashboard)
	}

	// Photo Reports (direct access)
	photoReports := rg.Group("/construction/photo-reports")
	photoReports.Use(h.perm.Require("construction", "reports", "read"))
	{
		photoReports.GET("/:id", h.GetPhotoReport)
		photoReports.PUT("/:id", h.perm.Require("construction", "reports", "submit"), h.UpdatePhotoReport)
		photoReports.DELETE("/:id", h.perm.Require("construction", "reports", "submit"), h.DeletePhotoReport)
		// Manual re-sync to CRM (used after a failure or when the building
		// was re-linked after the report was created).
		photoReports.POST("/:id/resync-crm", h.perm.Require("construction", "reports", "submit"), h.ResyncPhotoReportToCRM)
	}

	// CRM proxy + linkage endpoints (used by the construction page UI to
	// populate dropdowns when configuring the project↔CRM-project and
	// building↔CRM-block links).
	constructionCRM := rg.Group("/construction/crm")
	constructionCRM.Use(h.perm.Require("construction", "project", "read"))
	{
		constructionCRM.GET("/projects", h.ListCRMProjects)
		constructionCRM.GET("/blocks", h.ListCRMBlocks)
	}

	// Material Usage (direct access)
	materialUsage := rg.Group("/construction/material-usage")
	materialUsage.Use(h.perm.Require("construction", "project", "read"))
	{
		materialUsage.PUT("/:id", h.perm.Require("construction", "project", "update"), h.UpdateMaterialUsage)
		materialUsage.DELETE("/:id", h.perm.Require("construction", "project", "update"), h.DeleteMaterialUsage)
	}

	// Subcontracts (direct access)
	subcontracts := rg.Group("/construction/subcontracts")
	subcontracts.Use(h.perm.Require("construction", "project", "read"))
	{
		subcontracts.GET("/:id", h.GetSubcontract)
		subcontracts.PUT("/:id", h.perm.Require("construction", "project", "update"), h.UpdateSubcontract)
		subcontracts.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteSubcontract)
		subcontracts.PUT("/:id/state", h.perm.Require("construction", "project", "update"), h.UpdateSubcontractState)
	}

	// Act Types (user-manageable list of act types)
	actTypes := rg.Group("/construction/act-types")
	actTypes.Use(h.perm.Require("construction", "project", "read"))
	{
		actTypes.GET("", h.ListConstructionActTypes)
		actTypes.POST("", h.perm.Require("construction", "project", "update"), h.CreateConstructionActType)
		actTypes.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteConstructionActType)
	}

	// Acts (direct access) — Forma 2 / Forma 3 / Forma 19
	acts := rg.Group("/construction/acts")
	acts.Use(h.perm.Require("construction", "project", "read"))
	{
		acts.GET("/:id", h.GetConstructionAct)
		acts.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteConstructionAct)
		acts.PUT("/:id/approve", h.perm.Require("construction", "project", "update"), h.ApproveConstructionAct)
		acts.PUT("/:id/reject", h.perm.Require("construction", "project", "update"), h.RejectConstructionAct)
		acts.POST("/:id/generate-ks3", h.perm.Require("construction", "project", "update"), h.GenerateKS3FromKS2)
		acts.PUT("/:id/sign", h.perm.Require("construction", "project", "update"), h.SignAct)
		acts.PUT("/:id/cancel", h.perm.Require("construction", "project", "update"), h.CancelAct)
		acts.GET("/:id/export", h.ExportActDocument)
		acts.PUT("/:id/lines/:lineId", h.perm.Require("construction", "project", "update"), h.UpdateActLine)
	}

	// =====================================================
	// FORMA 2 / 3 / 19 DEDICATED ROUTES
	// =====================================================
	f2 := constructionProjects.Group("/f2")
	f2.Use(h.perm.Require("construction", "project", "read"))
	{
		f2.GET("", h.ListConstructionActs)
		f2.POST("", h.perm.Require("construction", "project", "create"), h.CreateConstructionAct)
		f2.GET("/:id", h.GetConstructionAct)
		f2.PUT("/:id/lines/:lineId", h.perm.Require("construction", "project", "update"), h.UpdateActLine)
		f2.POST("/:id/submit", h.perm.Require("construction", "project", "update"), h.SubmitForSigning)
		f2.PUT("/:id/sign", h.perm.Require("construction", "project", "update"), h.SignAct)
		f2.POST("/:id/cancel", h.perm.Require("construction", "project", "update"), h.CancelAct)
		f2.GET("/:id/pdf", h.ExportActDocument)
		f2.GET("/:id/xlsx", h.ExportActDocument)
		f2.DELETE("/:id", h.perm.Require("construction", "project", "delete"), h.DeleteConstructionAct)
	}

	f3 := constructionProjects.Group("/f3")
	f3.Use(h.perm.Require("construction", "project", "read"))
	{
		f3.GET("", h.ListConstructionActs)
		f3.POST("/generate", h.perm.Require("construction", "project", "create"), h.GenerateForma3)
		f3.GET("/:id", h.GetConstructionAct)
		f3.POST("/:id/sign", h.perm.Require("construction", "project", "update"), h.SignAct)
		f3.GET("/:id/pdf", h.ExportActDocument)
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
	pos.Use(h.perm.Require("sales", "order", "read"))
	{
		// POS Configuration
		pos.GET("/configs", h.ListPOSConfigs)
		pos.POST("/configs", h.perm.Require("sales", "order", "create"), h.CreatePOSConfig)
		pos.PUT("/configs/:id", h.perm.Require("sales", "order", "update"), h.UpdatePOSConfig)

		// POS Sessions
		pos.GET("/sessions", h.ListPOSSessions)
		pos.POST("/sessions/open", h.OpenPOSSession)
		pos.GET("/sessions/current", h.GetCurrentPOSSession)
		pos.POST("/sessions/:id/close", h.ClosePOSSession)
		pos.GET("/sessions/:id/summary", h.GetSessionSummary)

		// POS Orders
		pos.GET("/orders", h.ListPOSOrders)
		pos.POST("/orders", h.perm.Require("sales", "order", "create"), h.CreatePOSOrder)
		pos.GET("/orders/:id", h.GetPOSOrder)

		// POS Products (optimized search)
		pos.GET("/products", h.SearchPOSProducts)
	}

	// =====================================================
	// INTERCOMPANY TRANSFERS MODULE ROUTES
	// =====================================================

	// Intercompany Transfers (Product)
	icTransfers := rg.Group("/intercompany-transfers")
	icTransfers.Use(h.perm.Require("inventory", "stock", "read"))
	{
		icTransfers.GET("", h.ListIntercompanyTransfers)
		icTransfers.POST("", h.perm.Require("inventory", "stock", "create"), h.CreateIntercompanyTransfer)
		icTransfers.GET("/:id", h.GetIntercompanyTransfer)
		icTransfers.PUT("/:id", h.perm.Require("inventory", "stock", "update"), h.UpdateIntercompanyTransfer)
		icTransfers.DELETE("/:id", h.perm.Require("inventory", "stock", "delete"), h.DeleteIntercompanyTransfer)
		icTransfers.POST("/:id/approve", h.perm.Require("inventory", "stock", "transfer"), h.ApproveIntercompanyTransfer)
		icTransfers.POST("/:id/ship", h.perm.Require("inventory", "stock", "transfer"), h.ShipIntercompanyTransfer)
		icTransfers.POST("/:id/receive", h.perm.Require("inventory", "stock", "transfer"), h.ReceiveIntercompanyTransfer)
	}

	// Intercompany Payments (Money)
	icPayments := rg.Group("/intercompany-payments")
	icPayments.Use(h.perm.Require("finance", "payment", "read"))
	{
		icPayments.GET("", h.ListIntercompanyPayments)
		icPayments.POST("", h.perm.Require("finance", "payment", "create"), h.CreateIntercompanyPayment)
		icPayments.GET("/:id", h.GetIntercompanyPayment)
		icPayments.POST("/:id/approve", h.perm.Require("finance", "payment", "approve"), h.ApproveIntercompanyPayment)
		icPayments.DELETE("/:id", h.perm.Require("finance", "payment", "delete"), h.DeleteIntercompanyPayment)
	}

	// Intercompany Balances
	rg.GET("/intercompany-balances", h.perm.Require("finance", "account", "read"), h.GetIntercompanyBalances)

	// Intercompany Rules (Odoo-like configuration)
	icRules := rg.Group("/intercompany-rules")
	icRules.Use(h.perm.Require("settings", "organization", "read"))
	{
		icRules.GET("", h.ListIntercompanyRules)
		icRules.POST("", h.perm.Require("settings", "organization", "create"), h.CreateIntercompanyRule)
		icRules.GET("/:id", h.GetIntercompanyRule)
		icRules.PUT("/:id", h.perm.Require("settings", "organization", "update"), h.UpdateIntercompanyRule)
		icRules.DELETE("/:id", h.perm.Require("settings", "organization", "delete"), h.DeleteIntercompanyRule)
	}

	// Intercompany Transaction Logs (Audit Trail)
	rg.GET("/intercompany-logs", h.perm.Require("settings", "organization", "read"), h.ListIntercompanyTransactionLogs)
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
