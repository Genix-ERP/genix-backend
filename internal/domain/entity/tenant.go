package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Tenant represents a tenant in the multi-tenant system
type Tenant struct {
	ID                 uuid.UUID       `json:"id" db:"id"`
	Code               string          `json:"code" db:"code"`
	Name               string          `json:"name" db:"name"`
	Domain             *string         `json:"domain,omitempty" db:"domain"`
	LogoURL            *string         `json:"logo_url,omitempty" db:"logo_url"`
	Settings           json.RawMessage `json:"settings" db:"settings" swaggertype:"object"`
	SubscriptionPlan   string          `json:"subscription_plan" db:"subscription_plan"`
	SubscriptionStatus string          `json:"subscription_status" db:"subscription_status"`
	MaxUsers           int             `json:"max_users" db:"max_users"`
	MaxStorageGB       int             `json:"max_storage_gb" db:"max_storage_gb"`
	Features           json.RawMessage `json:"features" db:"features" swaggertype:"object"`
	IsActive           bool            `json:"is_active" db:"is_active"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
	DeletedAt          sql.NullTime    `json:"-" db:"deleted_at"`
}

// TenantSettings represents tenant configuration
type TenantSettings struct {
	Locale struct {
		Language      string `json:"language"`
		Timezone      string `json:"timezone"`
		DateFormat    string `json:"date_format"`
		TimeFormat    string `json:"time_format"`
		NumberFormat  string `json:"number_format"`
		DefaultCurrency string `json:"default_currency"`
	} `json:"locale"`
	Branding struct {
		PrimaryColor   string `json:"primary_color"`
		SecondaryColor string `json:"secondary_color"`
		LogoURL        string `json:"logo_url"`
		FaviconURL     string `json:"favicon_url"`
	} `json:"branding"`
	Security struct {
		PasswordMinLength       int  `json:"password_min_length"`
		PasswordRequireUppercase bool `json:"password_require_uppercase"`
		PasswordRequireNumbers   bool `json:"password_require_numbers"`
		PasswordRequireSpecial   bool `json:"password_require_special"`
		SessionTimeout          int  `json:"session_timeout"`
		MaxLoginAttempts        int  `json:"max_login_attempts"`
		TwoFactorRequired       bool `json:"two_factor_required"`
	} `json:"security"`
	Notifications struct {
		EmailEnabled bool `json:"email_enabled"`
		SMSEnabled   bool `json:"sms_enabled"`
		PushEnabled  bool `json:"push_enabled"`
	} `json:"notifications"`
	Modules struct {
		Finance    bool `json:"finance"`
		Inventory  bool `json:"inventory"`
		Sales      bool `json:"sales"`
		Purchase   bool `json:"purchase"`
		HR         bool `json:"hr"`
		CRM        bool `json:"crm"`
		AI         bool `json:"ai"`
	} `json:"modules"`
}

// SubscriptionPlan represents available subscription plans
type SubscriptionPlan string

const (
	PlanFree       SubscriptionPlan = "free"
	PlanStarter    SubscriptionPlan = "starter"
	PlanProfessional SubscriptionPlan = "professional"
	PlanEnterprise SubscriptionPlan = "enterprise"
)

// SubscriptionStatus represents subscription statuses
type SubscriptionStatus string

const (
	SubscriptionActive    SubscriptionStatus = "active"
	SubscriptionTrialing  SubscriptionStatus = "trialing"
	SubscriptionPastDue   SubscriptionStatus = "past_due"
	SubscriptionCancelled SubscriptionStatus = "cancelled"
	SubscriptionExpired   SubscriptionStatus = "expired"
)

// CreateTenantInput represents input for creating a tenant
type CreateTenantInput struct {
	Code             string           `json:"code" binding:"required,min=2,max=50"`
	Name             string           `json:"name" binding:"required,min=2,max=255"`
	Domain           string           `json:"domain,omitempty"`
	SubscriptionPlan SubscriptionPlan `json:"subscription_plan"`
	AdminEmail       string           `json:"admin_email" binding:"required,email"`
	AdminFirstName   string           `json:"admin_first_name" binding:"required"`
	AdminLastName    string           `json:"admin_last_name" binding:"required"`
	AdminPassword    string           `json:"admin_password" binding:"required,min=8"`
}

// UpdateTenantInput represents input for updating a tenant
type UpdateTenantInput struct {
	Name             *string                 `json:"name,omitempty"`
	Domain           *string                 `json:"domain,omitempty"`
	LogoURL          *string                 `json:"logo_url,omitempty"`
	Settings         map[string]interface{}  `json:"settings,omitempty"`
	SubscriptionPlan *SubscriptionPlan       `json:"subscription_plan,omitempty"`
	MaxUsers         *int                    `json:"max_users,omitempty"`
	MaxStorageGB     *int                    `json:"max_storage_gb,omitempty"`
	Features         []string                `json:"features,omitempty"`
	IsActive         *bool                   `json:"is_active,omitempty"`
}

// TenantStats represents tenant statistics
type TenantStats struct {
	TenantID       uuid.UUID `json:"tenant_id"`
	UserCount      int       `json:"user_count"`
	StorageUsedGB  float64   `json:"storage_used_gb"`
	ActiveSessions int       `json:"active_sessions"`
	APICallsToday  int       `json:"api_calls_today"`
}

// =====================================================
// INSTALLED APPS
// =====================================================

// TenantInstalledApp represents an installed app for a tenant
type TenantInstalledApp struct {
	ID             int64           `json:"id" db:"id"`
	TenantID       uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	AppID          string          `json:"app_id" db:"app_id"`
	AppName        string          `json:"app_name" db:"app_name"`
	AppDescription sql.NullString  `json:"app_description" db:"app_description"`
	AppVersion     string          `json:"app_version" db:"app_version"`
	AppIcon        sql.NullString  `json:"app_icon" db:"app_icon"`
	AppColor       sql.NullString  `json:"app_color" db:"app_color"`
	Status         string          `json:"status" db:"status"` // active, inactive, suspended
	InstalledBy    uuid.NullUUID   `json:"installed_by" db:"installed_by"`
	InstalledDate  time.Time       `json:"installed_date" db:"installed_date"`
	UpdatedDate    time.Time       `json:"updated_date" db:"updated_date"`
	Settings       json.RawMessage `json:"settings" db:"settings" swaggertype:"object"`
}

// InstallAppRequest represents request to install an app
type InstallAppRequest struct {
	AppID          string `json:"app_id" binding:"required"`
	AppName        string `json:"app_name" binding:"required"`
	AppDescription string `json:"app_description"`
	AppVersion     string `json:"app_version"`
	AppIcon        string `json:"app_icon"`
	AppColor       string `json:"app_color"`
}

// UpdateInstalledAppRequest represents request to update an installed app
type UpdateInstalledAppRequest struct {
	Status   string                 `json:"status"`
	Settings map[string]interface{} `json:"settings"`
}
