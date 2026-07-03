package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "deployable:"

// Client wraps a go-redis client, namespacing every key with "deployable:".
type Client struct {
	rdb *redis.Client
}

// Connect parses a redis:// URL and returns a ready Client, verifying
// connectivity with a Ping.
func Connect(redisURL string) (*Client, error) {
	if redisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is not set")
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}

	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// Ping verifies the Redis connection is alive.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Close closes the underlying Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

func namespaced(key string) string {
	return keyPrefix + key
}

// Set stores a value under key (namespaced) with the given TTL.
func (c *Client) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return c.rdb.Set(ctx, namespaced(key), value, ttl).Err()
}

// Get retrieves the string value stored at key (namespaced).
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, namespaced(key)).Result()
}

// Del deletes the value stored at key (namespaced).
func (c *Client) Del(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, namespaced(key)).Err()
}

// Incr increments the integer value stored at key (namespaced) by 1.
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	return c.rdb.Incr(ctx, namespaced(key)).Result()
}

// Expire sets a TTL on an existing key (namespaced).
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, namespaced(key), ttl).Err()
}
