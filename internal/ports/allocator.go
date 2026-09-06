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
	ports, err := a.AllocateBlock(ctx, 1)
	if err != nil {
		return 0, err
	}
	return ports[0], nil
}

// AllocateBlock finds and reserves a contiguous free block of host ports (1:1 with guest).
// Returns the starting port; the block is [start, start+size).
func (a *Allocator) AllocateBlock(ctx context.Context, size int) ([]int, error) {
	if size < 1 {
		return nil, fmt.Errorf("block size must be >= 1")
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	ok, err := a.redis.AcquireLock(ctx, "port-alloc", allocLockTTL)
	if err != nil {
		return nil, fmt.Errorf("acquire port lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("port allocator busy")
	}
	defer func() { _ = a.redis.ReleaseLock(ctx, "port-alloc") }()

	usedDB, err := a.store.ListUsedHostPorts(ctx)
	if err != nil {
		return nil, err
	}
	used := make(map[int]struct{}, len(usedDB))
	for _, p := range usedDB {
		used[p] = struct{}{}
	}

	isFree := func(port int) (bool, error) {
		if _, taken := used[port]; taken {
			return false, nil
		}
		allocated, err := a.redis.IsPortAllocated(ctx, port)
		if err != nil {
			return false, err
		}
		return !allocated, nil
	}

	lastStart := a.cfg.HostRangeEnd - size + 1
	for start := a.cfg.HostRangeStart; start <= lastStart; start++ {
		okBlock := true
		for p := start; p < start+size; p++ {
			free, err := isFree(p)
			if err != nil {
				return nil, err
			}
			if !free {
				okBlock = false
				break
			}
		}
		if !okBlock {
			continue
		}
		block := make([]int, 0, size)
		for p := start; p < start+size; p++ {
			if err := a.redis.MarkPortAllocated(ctx, p); err != nil {
				for _, marked := range block {
					_ = a.redis.ReleasePort(ctx, marked)
				}
				return nil, err
			}
			block = append(block, p)
		}
		return block, nil
	}
	return nil, fmt.Errorf("no free contiguous %d-port block in range %d-%d", size, a.cfg.HostRangeStart, a.cfg.HostRangeEnd)
}

// ReleaseBlock frees multiple host ports in Redis.
func (a *Allocator) ReleaseBlock(ctx context.Context, ports []int) {
	for _, p := range ports {
		_ = a.redis.ReleasePort(ctx, p)
	}
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
