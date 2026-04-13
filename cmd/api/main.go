package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/handler"
	"github.com/genixerp/genix-backend/internal/infrastructure/cache"
	"github.com/genixerp/genix-backend/internal/infrastructure/database"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/logger"
	"github.com/gin-gonic/gin"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	_ "github.com/genixerp/genix-backend/docs" // swagger docs
)

// @title GenixERP API
// @version 2.0
// @description Generative AI-based Enterprise Resource Planning System
// @termsOfService http://genixerp.io/terms/

// @contact.name GenixERP API Support
// @contact.url http://genixerp.io/support
// @contact.email support@genixerp.io

// @license.name Enterprise License
// @license.url http://genixerp.io/license

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := logger.NewLogger(cfg.Log.Level, cfg.Log.Format)
	log.Info("Starting GenixERP Backend", "version", "2.0.0", "env", cfg.App.Env)

	// Set Gin mode
	if cfg.App.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize database
	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		log.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Info("Database connection established")

	// Run migrations
	if err := database.RunMigrations(db, cfg.Database.MigrationsPath); err != nil {
		log.Error("Failed to run migrations", "error", err)
		os.Exit(1)
	}
	log.Info("Database migrations completed")

	// Initialize Redis cache
	redisClient, err := cache.NewRedisClient(cfg.Redis)
	if err != nil {
		log.Warn("Redis connection failed, running without cache", "error", err)
	} else {
		defer redisClient.Close()
		log.Info("Redis connection established")
	}

	// Initialize Gin router
	router := gin.New()

	// Apply global middleware
	router.Use(gin.Recovery())
	router.Use(middleware.RequestID())
	router.Use(middleware.Logger(log))
	router.Use(middleware.CORS(cfg.CORS))
	router.Use(middleware.RateLimiter(cfg.RateLimit, redisClient))
	router.Use(middleware.SecurityHeaders())
	router.Use(middleware.Timeout(cfg.App.RequestTimeout))

	// Health check endpoints (no auth required)
	router.GET("/health", handler.HealthCheck(db, redisClient))
	router.GET("/ready", handler.ReadinessCheck(db, redisClient))

	// Swagger documentation endpoint (disabled in production)
	if cfg.App.Env != "production" {
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	// Serve favicon
	router.StaticFile("/favicon.ico", "./static/favicon.png")
	router.StaticFile("/favicon.png", "./static/favicon.png")

	// Initialize and register all handlers
	h := handler.NewHandler(db, redisClient, cfg, log)
	h.RegisterRoutes(router)

	// Create a context that cancels on shutdown signal
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())

	// Start workflow automation scheduler (checks thresholds every 15 minutes)
	h.RunWorkflowScheduler(shutdownCtx, 15*time.Minute)

	// Start warehouse step timeout checker (runs every 15 minutes)
	handler.StartBackgroundJobs(db, log)

	// Start daily currency sync from CBU at 09:00 Tashkent time
	h.RunCurrencySyncScheduler(shutdownCtx)

	// Start daily reconciliation act auto-reminders (email + in-app) at 09:00 Tashkent time
	h.RunReconciliationReminderScheduler(shutdownCtx)

	// Start daily vendor bill overdue notifications at 09:00 Tashkent time
	h.RunVendorBillOverdueScheduler(shutdownCtx)

	// Start daily subscription cleanup at 03:00 Tashkent time (expire trials, delete abandoned)
	h.RunSubscriptionCleanupScheduler(shutdownCtx)

	// Create HTTP server
	server := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.App.Port),
		Handler:        router,
		ReadTimeout:    cfg.App.ReadTimeout,
		WriteTimeout:   cfg.App.WriteTimeout,
		IdleTimeout:    cfg.App.IdleTimeout,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	// Start server in goroutine
	go func() {
		log.Info("Server starting", "port", cfg.App.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("Server failed to start", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down server...")

	// Cancel scheduler context to stop background goroutines
	shutdownCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error("Server forced to shutdown", "error", err)
	}

	log.Info("Server exited gracefully")
}
