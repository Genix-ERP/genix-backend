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
		operationTypes.POST("", middleware.RequirePermission("inventory", "warehouse", "create"), h.CreateOperationType)
		operationTypes.PUT("/:id", middleware.RequirePermission("inventory", "warehouse", "update"), h.UpdateOperationType)
		operationTypes.DELETE("/:id", middleware.RequirePermission("inventory", "warehouse", "delete"), h.DeleteOperationType)
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
		workOrders.POST("/:id/complete", h.CompleteWorkOrder)
		workOrders.POST("/:id/time", h.RecordWorkOrderTime)
	}

	// Quality Control
	qualityChecks := rg.Group("/quality-checks")
	qualityChecks.Use(middleware.RequirePermission("manufacturing", "quality_checks", "read"))
	{
		qualityChecks.GET("", h.ListQualityChecks)
		qualityChecks.POST("", middleware.RequirePermission("manufacturing", "quality_checks", "create"), h.CreateQualityCheck)
		qualityChecks.GET("/stats", h.GetQualityStats)
		qualityChecks.GET("/defects", h.ListQualityDefects)
		qualityChecks.POST("/defects", middleware.RequirePermission("manufacturing", "quality_checks", "create"), h.CreateQualityDefect)
		qualityChecks.GET("/:id", h.GetQualityCheck)
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

		// Time Entries
		projects.GET("/:id/time-entries", h.ListTimeEntries)
		projects.POST("/:id/time-entries", middleware.RequirePermission("projects", "time_entry", "create"), h.CreateTimeEntry)
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
