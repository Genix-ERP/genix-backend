package handler

import (
	"net/http"
	"runtime"
	"time"

	"github.com/genixerp/genix-backend/internal/infrastructure/cache"
	"github.com/genixerp/genix-backend/internal/infrastructure/database"
	"github.com/gin-gonic/gin"
)

var startTime = time.Now()

// HealthStatus represents the health check response
type HealthStatus struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Version   string            `json:"version"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]Check  `json:"checks,omitempty"`
}

// Check represents an individual health check
type Check struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Latency string `json:"latency,omitempty"`
}

// HealthCheck returns a basic health check endpoint
func HealthCheck(db *database.DB, redis *cache.RedisClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, HealthStatus{
			Status:    "healthy",
			Timestamp: time.Now(),
			Version:   "2.0.0",
			Uptime:    time.Since(startTime).String(),
		})
	}
}

// ReadinessCheck returns a detailed readiness check
func ReadinessCheck(db *database.DB, redis *cache.RedisClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		checks := make(map[string]Check)
		overallStatus := "healthy"

		// Check database
		dbCheck := checkDatabase(c, db)
		checks["database"] = dbCheck
		if dbCheck.Status != "healthy" {
			overallStatus = "unhealthy"
		}

		// Check Redis
		if redis != nil {
			redisCheck := checkRedis(c, redis)
			checks["redis"] = redisCheck
			if redisCheck.Status != "healthy" && overallStatus == "healthy" {
				overallStatus = "degraded"
			}
		}

		// Add system info
		checks["system"] = Check{
			Status:  "healthy",
			Message: getSystemInfo(),
		}

		statusCode := http.StatusOK
		if overallStatus == "unhealthy" {
			statusCode = http.StatusServiceUnavailable
		}

		c.JSON(statusCode, HealthStatus{
			Status:    overallStatus,
			Timestamp: time.Now(),
			Version:   "2.0.0",
			Uptime:    time.Since(startTime).String(),
			Checks:    checks,
		})
	}
}

func checkDatabase(c *gin.Context, db *database.DB) Check {
	start := time.Now()
	err := db.HealthCheck(c.Request.Context())
	latency := time.Since(start)

	if err != nil {
		return Check{
			Status:  "unhealthy",
			Message: err.Error(),
			Latency: latency.String(),
		}
	}

	_ = db.Stats() // Could add stats to message if needed
	return Check{
		Status:  "healthy",
		Message: "Connected",
		Latency: latency.String(),
	}
}

func checkRedis(c *gin.Context, redis *cache.RedisClient) Check {
	start := time.Now()
	err := redis.HealthCheck(c.Request.Context())
	latency := time.Since(start)

	if err != nil {
		return Check{
			Status:  "unhealthy",
			Message: err.Error(),
			Latency: latency.String(),
		}
	}

	return Check{
		Status:  "healthy",
		Message: "Connected",
		Latency: latency.String(),
	}
}

func getSystemInfo() string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return "goroutines: " + itoa(runtime.NumGoroutine()) +
		", heap_alloc: " + formatBytes(m.HeapAlloc) +
		", sys: " + formatBytes(m.Sys)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	result := ""
	for i > 0 {
		result = string(rune('0'+i%10)) + result
		i /= 10
	}
	return result
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return itoa(int(b)) + "B"
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return itoa(int(b/div)) + "KMGTPE"[exp:exp+1] + "iB"
}
