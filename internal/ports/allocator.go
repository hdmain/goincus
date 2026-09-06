package ports

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hdmain/goincus/internal/config"
	"github.com/hdmain/goincus/internal/db"
	"github.com/hdmain/goincus/internal/redisstore"
)

const allocLockTTL = 10 * time.Second

// Allocator dynamically assigns host external ports from a configured range.
// Postgres is the source of truth; Redis provides fast contention checks under lock.
type Allocator struct {
	cfg   config.PortsConfig
	store *db.Store
	redis *redisstore.Client
	mu    sync.Mutex
}

// NewAllocator constructs a port allocator.
func NewAllocator(cfg config.PortsConfig, store *db.Store, redis *redisstore.Client) *Allocator {
	return &Allocator{cfg: cfg, store: store, redis: redis}
}

// Sync loads allocated ports from the database into Redis.
func (a *Allocator) Sync(ctx context.Context) error {
	ports, err := a.store.ListUsedHostPorts(ctx)
	if err != nil {
		return fmt.Errorf("list used ports: %w", err)
	}
	if err := a.redis.SyncAllocatedPorts(ctx, ports); err != nil {
		return fmt.Errorf("sync redis ports: %w", err)
	}
	return nil
}

// Allocate finds and reserves a free host port.
func (a *Allocator) Allocate(ctx context.Context) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ok, err := a.redis.AcquireLock(ctx, "port-alloc", allocLockTTL)
	if err != nil {
		return 0, fmt.Errorf("acquire port lock: %w", err)
	}
	if !ok {
		return 0, fmt.Errorf("port allocator busy")
	}
	defer func() { _ = a.redis.ReleaseLock(ctx, "port-alloc") }()

	usedDB, err := a.store.ListUsedHostPorts(ctx)
	if err != nil {
		return 0, err
	}
	used := make(map[int]struct{}, len(usedDB))
	for _, p := range usedDB {
		used[p] = struct{}{}
	}

	for port := a.cfg.HostRangeStart; port <= a.cfg.HostRangeEnd; port++ {
		if _, taken := used[port]; taken {
			continue
		}
		allocated, err := a.redis.IsPortAllocated(ctx, port)
		if err != nil {
			return 0, err
		}
		if allocated {
			continue
		}
		if err := a.redis.MarkPortAllocated(ctx, port); err != nil {
			return 0, err
		}
		return port, nil
	}
	return 0, fmt.Errorf("no free host ports in range %d-%d", a.cfg.HostRangeStart, a.cfg.HostRangeEnd)
}

// Reserve marks a specific host port as allocated if available.
func (a *Allocator) Reserve(ctx context.Context, port int) error {
	if port < a.cfg.HostRangeStart || port > a.cfg.HostRangeEnd {
		return fmt.Errorf("host port %d outside allowed range %d-%d", port, a.cfg.HostRangeStart, a.cfg.HostRangeEnd)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	ok, err := a.redis.AcquireLock(ctx, "port-alloc", allocLockTTL)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("port allocator busy")
	}
	defer func() { _ = a.redis.ReleaseLock(ctx, "port-alloc") }()

	used, err := a.store.ListUsedHostPorts(ctx)
	if err != nil {
		return err
	}
	for _, p := range used {
		if p == port {
			return fmt.Errorf("host port %d already allocated", port)
		}
	}
	allocated, err := a.redis.IsPortAllocated(ctx, port)
	if err != nil {
		return err
	}
	if allocated {
		return fmt.Errorf("host port %d already allocated", port)
	}
	return a.redis.MarkPortAllocated(ctx, port)
}

// Release frees a host port in Redis.
func (a *Allocator) Release(ctx context.Context, port int) error {
	return a.redis.ReleasePort(ctx, port)
}
