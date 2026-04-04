package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	App       AppConfig
	Database  DatabaseConfig
	Redis     RedisConfig
	JWT       JWTConfig
	CORS      CORSConfig
	RateLimit RateLimitConfig
	AI        AIConfig
	Log       LogConfig
	Storage   StorageConfig
	Email     EmailConfig
	SMS       SMSConfig
	Queue     QueueConfig
	Google     GoogleConfig
	Multicard  MulticardConfig
}

// MulticardConfig holds Multicard payment gateway settings
type MulticardConfig struct {
	ApplicationID string
	Secret        string
	StoreID       int
	BaseURL       string
	StarterAmount  int64 // in UZS
	ProfessionalAmount int64
	EnterpriseAmount   int64
}

// GoogleConfig holds Google OAuth settings
type GoogleConfig struct {
	ClientID string
}

// AppConfig holds application settings
type AppConfig struct {
	Name           string
	Env            string
	Port           int
	BaseURL        string
	FrontendURL    string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	RequestTimeout time.Duration
	Debug          bool
}

// DatabaseConfig holds PostgreSQL settings
type DatabaseConfig struct {
	Host            string
	Port            int
	User            string
	Password        string
	DBName          string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	MigrationsPath  string
}

// RedisConfig holds Redis settings
type RedisConfig struct {
	Host         string
	Port         int
	Password     string
	DB           int
	PoolSize     int
	MinIdleConns int
	MaxRetries   int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// JWTConfig holds JWT settings
type JWTConfig struct {
	SecretKey          string
	AccessTokenExpiry  time.Duration
	RefreshTokenExpiry time.Duration
	Issuer             string
	Audience           string
}

// CORSConfig holds CORS settings
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// RateLimitConfig holds rate limiting settings
type RateLimitConfig struct {
	Enabled       bool
	RequestsLimit int
	WindowSize    time.Duration
	BurstSize     int
}

// AIConfig holds AI/ML service settings
type AIConfig struct {
	Provider        string // openai, anthropic, local
	APIKey          string
	Model           string
	MaxTokens       int
	Temperature     float64
	Endpoint        string
	RequestTimeout  time.Duration
	RateLimitPerMin int
}

// LogConfig holds logging settings
type LogConfig struct {
	Level  string
	Format string // json or text
	Output string // stdout, stderr, or file path
}

// StorageConfig holds file storage settings
type StorageConfig struct {
	Provider        string // local, s3, gcs, azure
	LocalPath       string
	S3Bucket        string
	S3Region        string
	S3AccessKey     string
	S3SecretKey     string
	MaxFileSize     int64
	AllowedMimeTypes []string
}

// EmailConfig holds email settings
type EmailConfig struct {
	Provider   string // smtp, sendgrid, ses
	SMTPHost   string
	SMTPPort   int
	Username   string
	Password   string
	FromEmail  string
	FromName   string
	APIKey     string
}

// SMSConfig holds SMS provider settings
type SMSConfig struct {
	Provider string // eskiz, twilio
	APIEmail string
	APIKey   string
	BaseURL  string
	From     string
}

// QueueConfig holds message queue settings
type QueueConfig struct {
	Provider string // redis, rabbitmq, kafka
	URL      string
	Workers  int
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if not found)
	_ = godotenv.Load()

	cfg := &Config{
		App: AppConfig{
			Name:           getEnv("APP_NAME", "GenixERP"),
			Env:            getEnv("APP_ENV", "development"),
			Port:           getEnvAsInt("APP_PORT", 8080),
			BaseURL:        getEnv("APP_BASE_URL", "http://localhost:8080"),
			FrontendURL:    getEnv("FRONTEND_URL", "http://localhost:5173"),
			ReadTimeout:    getEnvAsDuration("APP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:   getEnvAsDuration("APP_WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:    getEnvAsDuration("APP_IDLE_TIMEOUT", 60*time.Second),
			RequestTimeout: getEnvAsDuration("APP_REQUEST_TIMEOUT", 30*time.Second),
			Debug:          getEnvAsBool("APP_DEBUG", false),
		},
		Database: DatabaseConfig{
			Host:            getEnv("DB_HOST", "localhost"),
			Port:            getEnvAsInt("DB_PORT", 5432),
			User:            getEnv("DB_USER", "genix"),
			Password:        getEnv("DB_PASSWORD", "genix_secret"),
			DBName:          getEnv("DB_NAME", "genixerp"),
			SSLMode:         getEnv("DB_SSLMODE", "require"),
			MaxOpenConns:    getEnvAsInt("DB_MAX_OPEN_CONNS", 100),
			MaxIdleConns:    getEnvAsInt("DB_MAX_IDLE_CONNS", 25),
			ConnMaxLifetime: getEnvAsDuration("DB_CONN_MAX_LIFETIME", 15*time.Minute),
			ConnMaxIdleTime: getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
			MigrationsPath:  getEnv("DB_MIGRATIONS_PATH", "migrations"),
		},
		Redis: RedisConfig{
			Host:         getEnv("REDIS_HOST", "localhost"),
			Port:         getEnvAsInt("REDIS_PORT", 6379),
			Password:     getEnv("REDIS_PASSWORD", ""),
			DB:           getEnvAsInt("REDIS_DB", 0),
			PoolSize:     getEnvAsInt("REDIS_POOL_SIZE", 100),
			MinIdleConns: getEnvAsInt("REDIS_MIN_IDLE_CONNS", 10),
			MaxRetries:   getEnvAsInt("REDIS_MAX_RETRIES", 3),
			DialTimeout:  getEnvAsDuration("REDIS_DIAL_TIMEOUT", 5*time.Second),
			ReadTimeout:  getEnvAsDuration("REDIS_READ_TIMEOUT", 3*time.Second),
			WriteTimeout: getEnvAsDuration("REDIS_WRITE_TIMEOUT", 3*time.Second),
		},
		JWT: JWTConfig{
			SecretKey:          getEnv("JWT_SECRET_KEY", "your-super-secret-key-change-in-production"),
			AccessTokenExpiry:  getEnvAsDuration("JWT_ACCESS_TOKEN_EXPIRY", 24*time.Hour),
			RefreshTokenExpiry: getEnvAsDuration("JWT_REFRESH_TOKEN_EXPIRY", 7*24*time.Hour),
			Issuer:             getEnv("JWT_ISSUER", "genixerp"),
			Audience:           getEnv("JWT_AUDIENCE", "genixerp-api"),
		},
		CORS: CORSConfig{
			AllowedOrigins:   getEnvAsSlice("CORS_ALLOWED_ORIGINS", []string{"http://localhost:5173", "http://localhost:3000", "http://localhost:3001", "https://app.genixerp.com", "https://genixerp.com"}),
			AllowedMethods:   getEnvAsSlice("CORS_ALLOWED_METHODS", []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}),
			AllowedHeaders:   getEnvAsSlice("CORS_ALLOWED_HEADERS", []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID", "X-Tenant-ID", "X-Organization-ID"}),
			ExposedHeaders:   getEnvAsSlice("CORS_EXPOSED_HEADERS", []string{"Content-Length", "X-Request-ID"}),
			AllowCredentials: getEnvAsBool("CORS_ALLOW_CREDENTIALS", true),
			MaxAge:           getEnvAsInt("CORS_MAX_AGE", 86400),
		},
		RateLimit: RateLimitConfig{
			Enabled:       getEnvAsBool("RATE_LIMIT_ENABLED", true),
			RequestsLimit: getEnvAsInt("RATE_LIMIT_REQUESTS", 1000),
			WindowSize:    getEnvAsDuration("RATE_LIMIT_WINDOW", 1*time.Minute),
			BurstSize:     getEnvAsInt("RATE_LIMIT_BURST", 50),
		},
		AI: AIConfig{
			Provider:        getEnv("AI_PROVIDER", "openai"),
			APIKey:          getEnv("AI_API_KEY", ""),
			Model:           getEnv("AI_MODEL", "gpt-4"),
			MaxTokens:       getEnvAsInt("AI_MAX_TOKENS", 4096),
			Temperature:     getEnvAsFloat("AI_TEMPERATURE", 0.7),
			Endpoint:        getEnv("AI_ENDPOINT", ""),
			RequestTimeout:  getEnvAsDuration("AI_REQUEST_TIMEOUT", 60*time.Second),
			RateLimitPerMin: getEnvAsInt("AI_RATE_LIMIT_PER_MIN", 60),
		},
		Log: LogConfig{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
			Output: getEnv("LOG_OUTPUT", "stdout"),
		},
		Storage: StorageConfig{
			Provider:        getEnv("STORAGE_PROVIDER", "local"),
			LocalPath:       getEnv("STORAGE_LOCAL_PATH", "./storage"),
			S3Bucket:        getEnv("STORAGE_S3_BUCKET", ""),
			S3Region:        getEnv("STORAGE_S3_REGION", "us-east-1"),
			S3AccessKey:     getEnv("STORAGE_S3_ACCESS_KEY", ""),
			S3SecretKey:     getEnv("STORAGE_S3_SECRET_KEY", ""),
			MaxFileSize:     getEnvAsInt64("STORAGE_MAX_FILE_SIZE", 100*1024*1024), // 100MB
			AllowedMimeTypes: getEnvAsSlice("STORAGE_ALLOWED_MIME_TYPES", []string{
				"image/jpeg", "image/png", "image/gif", "image/webp",
				"application/pdf", "application/msword",
				"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
				"application/vnd.ms-excel",
				"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
				"text/csv", "text/plain",
			}),
		},
		Email: EmailConfig{
			Provider:  getEnv("EMAIL_PROVIDER", "smtp"),
			SMTPHost:  getEnv("EMAIL_SMTP_HOST", "localhost"),
			SMTPPort:  getEnvAsInt("EMAIL_SMTP_PORT", 587),
			Username:  getEnv("EMAIL_USERNAME", ""),
			Password:  getEnv("EMAIL_PASSWORD", ""),
			FromEmail: getEnv("EMAIL_FROM_EMAIL", "noreply@genixerp.io"),
			FromName:  getEnv("EMAIL_FROM_NAME", "GenixERP"),
			APIKey:    getEnv("EMAIL_API_KEY", ""),
		},
		SMS: SMSConfig{
			Provider: getEnv("SMS_PROVIDER", "eskiz"),
			APIEmail: getEnv("SMS_API_EMAIL", ""),
			APIKey:   getEnv("SMS_API_KEY", ""),
			BaseURL:  getEnv("SMS_BASE_URL", "https://notify.eskiz.uz/api"),
			From:     getEnv("SMS_FROM", "4546"),
		},
		Queue: QueueConfig{
			Provider: getEnv("QUEUE_PROVIDER", "redis"),
			URL:      getEnv("QUEUE_URL", "redis://localhost:6379/1"),
			Workers:  getEnvAsInt("QUEUE_WORKERS", 10),
		},
		Google: GoogleConfig{
			ClientID: getEnv("GOOGLE_CLIENT_ID", ""),
		},
		Multicard: MulticardConfig{
			ApplicationID:      getEnv("MULTICARD_APPLICATION_ID", ""),
			Secret:             getEnv("MULTICARD_SECRET", ""),
			StoreID:            getEnvAsInt("MULTICARD_STORE_ID", 0),
			BaseURL:            getEnv("MULTICARD_BASE_URL", "https://mesh.multicard.uz"),
			StarterAmount:      getEnvAsInt64("MULTICARD_STARTER_AMOUNT", 299000),
			ProfessionalAmount: getEnvAsInt64("MULTICARD_PROFESSIONAL_AMOUNT", 499000),
			EnterpriseAmount:   getEnvAsInt64("MULTICARD_ENTERPRISE_AMOUNT", 999000),
		},
	}

	// Validate required configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Database.Host == "" {
		return fmt.Errorf("database host is required")
	}
	if c.Database.DBName == "" {
		return fmt.Errorf("database name is required")
	}
	if c.App.Env == "production" {
		if c.JWT.SecretKey == "" || c.JWT.SecretKey == "your-super-secret-key-change-in-production" {
			return fmt.Errorf("JWT secret key must be set in production")
		}
		if len(c.JWT.SecretKey) < 32 {
			return fmt.Errorf("JWT secret key must be at least 32 characters in production")
		}
	}
	return nil
}

// DSN returns the PostgreSQL connection string
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode,
	)
}

// Helper functions to get environment variables with defaults
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := getEnv(key, "")
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsInt64(key string, defaultValue int64) int64 {
	valueStr := getEnv(key, "")
	if value, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsFloat(key string, defaultValue float64) float64 {
	valueStr := getEnv(key, "")
	if value, err := strconv.ParseFloat(valueStr, 64); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := getEnv(key, "")
	if value, err := strconv.ParseBool(valueStr); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := getEnv(key, "")
	if value, err := time.ParseDuration(valueStr); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsSlice(key string, defaultValue []string) []string {
	valueStr := getEnv(key, "")
	if valueStr == "" {
		return defaultValue
	}
	return strings.Split(valueStr, ",")
}
