package redisstore

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hdmain/goincus/internal/config"
)

// Client wraps Redis for lock and ephemeral state management.
type Client struct {
	rdb    *redis.Client
	prefix string
}

// Connect opens a Redis client to the local goincus Redis instance.
func Connect(ctx context.Context, cfg config.RedisConfig) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr(),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Client{rdb: rdb, prefix: cfg.KeyPrefix}, nil
}

// Close shuts down the Redis client.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Ping checks Redis connectivity.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) key(parts ...string) string {
	out := c.prefix
	for i, p := range parts {
		if i > 0 {
			out += ":"
		}
		out += p
	}
	return out
}

// AcquireLock attempts to take a distributed lock with TTL.
func (c *Client) AcquireLock(ctx context.Context, name string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, c.key("lock", name), "1", ttl).Result()
}

// ReleaseLock deletes a lock key.
func (c *Client) ReleaseLock(ctx context.Context, name string) error {
	return c.rdb.Del(ctx, c.key("lock", name)).Err()
}

// MarkPortAllocated records a host port as in-use in Redis.
func (c *Client) MarkPortAllocated(ctx context.Context, port int) error {
	return c.rdb.SAdd(ctx, c.key("ports", "allocated"), port).Err()
}

// ReleasePort removes a host port from the allocated set.
func (c *Client) ReleasePort(ctx context.Context, port int) error {
	return c.rdb.SRem(ctx, c.key("ports", "allocated"), port).Err()
}

// IsPortAllocated checks whether a host port is marked allocated.
func (c *Client) IsPortAllocated(ctx context.Context, port int) (bool, error) {
	return c.rdb.SIsMember(ctx, c.key("ports", "allocated"), port).Result()
}

// SyncAllocatedPorts replaces the Redis allocated-port set with DB truth.
func (c *Client) SyncAllocatedPorts(ctx context.Context, ports []int) error {
	key := c.key("ports", "allocated")
	pipe := c.rdb.TxPipeline()
	pipe.Del(ctx, key)
	if len(ports) > 0 {
		members := make([]interface{}, len(ports))
		for i, p := range ports {
			members[i] = p
		}
		pipe.SAdd(ctx, key, members...)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// SetInstanceState stores ephemeral lifecycle state for an instance.
func (c *Client) SetInstanceState(ctx context.Context, instanceID, state string, ttl time.Duration) error {
	return c.rdb.Set(ctx, c.key("instance", instanceID, "state"), state, ttl).Err()
}

// GetInstanceState reads ephemeral lifecycle state.
func (c *Client) GetInstanceState(ctx context.Context, instanceID string) (string, error) {
	val, err := c.rdb.Get(ctx, c.key("instance", instanceID, "state")).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

// DeleteInstanceState clears ephemeral state.
func (c *Client) DeleteInstanceState(ctx context.Context, instanceID string) error {
	return c.rdb.Del(ctx, c.key("instance", instanceID, "state")).Err()
}
