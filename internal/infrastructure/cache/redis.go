package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
	"github.com/redis/go-redis/v9"
)

// RedisClient wraps the Redis client with additional functionality
type RedisClient struct {
	client *redis.Client
	config config.RedisConfig
}

// NewRedisClient creates a new Redis client with optimized settings
func NewRedisClient(cfg config.RedisConfig) (*RedisClient, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		MaxRetries:   cfg.MaxRetries,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisClient{client: client, config: cfg}, nil
}

// Close closes the Redis connection
func (r *RedisClient) Close() error {
	return r.client.Close()
}

// Client returns the underlying Redis client
func (r *RedisClient) Client() *redis.Client {
	return r.client
}

// HealthCheck verifies the Redis connection is healthy
func (r *RedisClient) HealthCheck(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Get retrieves a value from cache
func (r *RedisClient) Get(ctx context.Context, key string) (string, error) {
	return r.client.Get(ctx, key).Result()
}

// Set stores a value in cache with expiration
func (r *RedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return r.client.Set(ctx, key, value, expiration).Err()
}

// Delete removes a key from cache
func (r *RedisClient) Delete(ctx context.Context, keys ...string) error {
	return r.client.Del(ctx, keys...).Err()
}
