package database

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migration represents a database migration
type Migration struct {
	Version   int
	Name      string
	SQL       string
	AppliedAt time.Time
}

// RunMigrations executes all pending database migrations
func RunMigrations(db *DB, migrationsPath string) error {
	// Create migrations table if not exists
	if err := createMigrationsTable(db); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get applied migrations
	applied, err := getAppliedMigrations(db)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Get pending migrations
	pending, err := getPendingMigrations(applied)
	if err != nil {
		return fmt.Errorf("failed to get pending migrations: %w", err)
	}

	// Apply pending migrations
	for _, m := range pending {
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", m.Name, err)
		}
	}

	return nil
}

func createMigrationsTable(db *DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)
	`
	_, err := db.Exec(query)
	return err
}

func getAppliedMigrations(db *DB) (map[int]bool, error) {
	applied := make(map[int]bool)

	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}

	return applied, rows.Err()
}

func getPendingMigrations(applied map[int]bool) ([]Migration, error) {
	var pending []Migration

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		// No embedded migrations, check external migrations
		return pending, nil
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version, name, err := parseMigrationFilename(entry.Name())
		if err != nil {
			continue
		}

		if applied[version] {
			continue
		}

		content, err := fs.ReadFile(migrationsFS, filepath.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration file %s: %w", entry.Name(), err)
		}

		pending = append(pending, Migration{
			Version: version,
			Name:    name,
			SQL:     string(content),
		})
	}

	// Sort by version
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].Version < pending[j].Version
	})

	return pending, nil
}

func parseMigrationFilename(filename string) (int, string, error) {
	// Expected format: 001_create_users_table.sql
	parts := strings.SplitN(strings.TrimSuffix(filename, ".sql"), "_", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid migration filename: %s", filename)
	}

	var version int
	if _, err := fmt.Sscanf(parts[0], "%d", &version); err != nil {
		return 0, "", fmt.Errorf("invalid migration version: %s", parts[0])
	}

	return version, parts[1], nil
}

func applyMigration(db *DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Execute migration SQL
	if _, err = tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	// Record migration
	if _, err = tx.Exec(
		"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)",
		m.Version, m.Name,
	); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}

// RollbackMigration rolls back the last applied migration
func RollbackMigration(db *DB) error {
	var version int
	var name string

	err := db.QueryRow(
		"SELECT version, name FROM schema_migrations ORDER BY version DESC LIMIT 1",
	).Scan(&version, &name)

	if err == sql.ErrNoRows {
		return fmt.Errorf("no migrations to rollback")
	}
	if err != nil {
		return fmt.Errorf("failed to get last migration: %w", err)
	}

	// Look for rollback file
	rollbackFile := fmt.Sprintf("%03d_%s.down.sql", version, name)
	content, err := fs.ReadFile(migrationsFS, filepath.Join("migrations", rollbackFile))
	if err != nil {
		return fmt.Errorf("rollback file not found: %s", rollbackFile)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if _, err = tx.Exec(string(content)); err != nil {
		return fmt.Errorf("failed to execute rollback: %w", err)
	}

	if _, err = tx.Exec("DELETE FROM schema_migrations WHERE version = $1", version); err != nil {
		return fmt.Errorf("failed to remove migration record: %w", err)
	}

	return tx.Commit()
}
