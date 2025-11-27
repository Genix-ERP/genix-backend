package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
	_ "github.com/lib/pq"
)

// DB wraps the standard sql.DB with additional functionality
type DB struct {
	*sql.DB
	config config.DatabaseConfig
}

// NewPostgresDB creates a new PostgreSQL database connection with optimized settings
func NewPostgresDB(cfg config.DatabaseConfig) (*DB, error) {
	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool for high performance
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{DB: db, config: cfg}, nil
}

// Transaction executes a function within a database transaction
func (db *DB) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("failed to rollback transaction: %v (original error: %w)", rbErr, err)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// TransactionWithIsolation executes a function within a database transaction with custom isolation level
func (db *DB) TransactionWithIsolation(ctx context.Context, isolation sql.IsolationLevel, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: isolation})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("failed to rollback transaction: %v (original error: %w)", rbErr, err)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// HealthCheck verifies the database connection is healthy
func (db *DB) HealthCheck(ctx context.Context) error {
	return db.PingContext(ctx)
}

// Stats returns database connection pool statistics
func (db *DB) Stats() sql.DBStats {
	return db.DB.Stats()
}
