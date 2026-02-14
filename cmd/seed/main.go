package main

import (
	"fmt"
	"os"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
	"github.com/genixerp/genix-backend/internal/infrastructure/database"
	"github.com/genixerp/genix-backend/internal/pkg/crypto"
	"github.com/google/uuid"
)

func main() {
	fmt.Println("GenixERP Database Seeder")
	fmt.Println("========================")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize database
	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		fmt.Printf("Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Println("Connected to database")

	// Create demo tenant
	tenantID := uuid.New()
	now := time.Now()

	// Check if demo tenant already exists
	fmt.Println("Checking for existing demo tenant...")
	err = db.QueryRow("SELECT id FROM tenants WHERE code = 'demo'").Scan(&tenantID)
	if err != nil {
		// Create new tenant
		fmt.Println("Creating demo tenant...")
		_, err = db.Exec(`
			INSERT INTO tenants (id, code, name, subscription_plan, subscription_status, settings, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, tenantID, "demo", "Demo Company", "professional", "active", "{}", true, now, now)
		if err != nil {
			fmt.Printf("Failed to create tenant: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created tenant: %s\n", tenantID)
	} else {
		fmt.Printf("Using existing tenant: %s\n", tenantID)
	}

	// Hash passwords
	adminPasswordHash, err := crypto.HashPassword("admin123")
	if err != nil {
		fmt.Printf("Failed to hash admin password: %v\n", err)
		os.Exit(1)
	}

	userPasswordHash, err := crypto.HashPassword("user123")
	if err != nil {
		fmt.Printf("Failed to hash user password: %v\n", err)
		os.Exit(1)
	}

	// Create admin user
	adminID := uuid.New()
	fmt.Println("Creating admin user...")
	_, err = db.Exec(`
		INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, is_active, is_verified, is_system_admin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (tenant_id, email) DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			first_name = EXCLUDED.first_name,
			last_name = EXCLUDED.last_name,
			is_system_admin = EXCLUDED.is_system_admin,
			updated_at = EXCLUDED.updated_at
	`, adminID, tenantID, "admin@genixerp.com", adminPasswordHash, "Admin", "User", true, true, true, now, now)
	if err != nil {
		fmt.Printf("Failed to create admin user: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Admin user created: admin@genixerp.com / admin123")

	// Create regular user
	userID := uuid.New()
	fmt.Println("Creating regular user...")
	_, err = db.Exec(`
		INSERT INTO users (id, tenant_id, email, password_hash, first_name, last_name, is_active, is_verified, is_system_admin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (tenant_id, email) DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			first_name = EXCLUDED.first_name,
			last_name = EXCLUDED.last_name,
			updated_at = EXCLUDED.updated_at
	`, userID, tenantID, "user@genixerp.com", userPasswordHash, "Demo", "User", true, true, false, now, now)
	if err != nil {
		fmt.Printf("Failed to create regular user: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Regular user created: user@genixerp.com / user123")

	fmt.Println("")
	fmt.Println("Seeding completed successfully!")
	fmt.Println("")
	fmt.Println("Demo Credentials:")
	fmt.Println("  Admin: admin@genixerp.com / admin123")
	fmt.Println("  User:  user@genixerp.com / user123")
}
