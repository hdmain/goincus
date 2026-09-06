package incusclient

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

const (
	quotaStoragePool    = "goincus"
	defaultPoolLoopSize = "200GiB"
)

// drivers that give each instance its own filesystem so `df` inside the guest
// shows storage_gb (not the host disk). btrfs/dir enforce quotas poorly for df.
func isDFIsolatingDriver(driver string) bool {
	switch strings.ToLower(driver) {
	case "zfs", "lvm", "lvmcluster", "ceph", "cephfs":
		return true
	default:
		return false
	}
}

// EnsureStoragePool ensures a storage pool that isolates per-instance disk size in `df`.
// Prefer zfs → lvm (loop-backed). Never silently keep using dir/btrfs for new VPS roots.
func (c *Client) EnsureStoragePool() error {
	wanted := strings.TrimSpace(c.cfg.StoragePool)
	if wanted == "" {
		wanted = quotaStoragePool
	}

	if pool, _, err := c.server.GetStoragePool(wanted); err == nil && isDFIsolatingDriver(pool.Driver) {
		c.cfg.StoragePool = wanted
		slog.Info("storage pool ready", "pool", wanted, "driver", pool.Driver)
		return nil
	}

	// Configured pool missing or is dir/btrfs — stand up a real quota pool.
	for _, name := range uniquePoolNames(wanted, quotaStoragePool, quotaStoragePool+"-lvm", quotaStoragePool+"-zfs") {
		if err := c.ensureQuotaCapablePool(name); err == nil {
			if pool, _, err := c.server.GetStoragePool(name); err == nil {
				c.cfg.StoragePool = name
				slog.Info("storage pool ready", "pool", name, "driver", pool.Driver)
				return nil
			}
			c.cfg.StoragePool = name
			return nil
		}
	}

	return fmt.Errorf(
		"no storage pool that isolates disk size in df (need zfs or lvm). "+
			"Install packages then recreate: apt install -y lvm2 thin-provisioning-tools  # or zfsutils-linux; "+
			"incus storage create goincus-lvm lvm size=200GiB; set storage_pool: goincus-lvm in config. "+
			"dir/btrfs pools always show the host disk in guest df",
	)
}

func uniquePoolNames(names ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || n == "default" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func (c *Client) ensureQuotaCapablePool(name string) error {
	if pool, _, err := c.server.GetStoragePool(name); err == nil {
		if !isDFIsolatingDriver(pool.Driver) {
			return fmt.Errorf("pool %q uses %s (df shows host disk)", name, pool.Driver)
		}
		return nil
	}
	return c.createQuotaStoragePool(name)
}

func (c *Client) createQuotaStoragePool(name string) error {
	loopSize := defaultPoolLoopSize
	candidates := []struct {
		driver string
		config map[string]string
		check  func() error
	}{
		{
			"zfs", map[string]string{"size": loopSize},
			func() error {
				if _, err := exec.LookPath("zpool"); err != nil {
					return err
				}
				if _, err := exec.LookPath("zfs"); err != nil {
					return err
				}
				return nil
			},
		},
		{
			"lvm", map[string]string{"size": loopSize},
			func() error {
				if _, err := exec.LookPath("lvcreate"); err != nil {
					return err
				}
				if _, err := exec.LookPath("vgcreate"); err != nil {
					return err
				}
				return nil
			},
		},
	}

	var errs []string
	for _, cand := range candidates {
		if err := cand.check(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: tools missing (%v)", cand.driver, err))
			continue
		}
		req := api.StoragePoolsPost{
			Name:   name,
			Driver: cand.driver,
			StoragePoolPut: api.StoragePoolPut{
				Description: "goincus storage pool (per-instance disk; df-isolated)",
				Config:      cand.config,
			},
		}
		if err := c.server.CreateStoragePool(req); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", cand.driver, err))
			continue
		}
		slog.Info("created storage pool", "pool", name, "driver", cand.driver, "size", loopSize)
		return nil
	}
	return fmt.Errorf("create storage pool %q: %s", name, strings.Join(errs, "; "))
}

// RootDiskDevice builds the instance root disk with an enforced size quota.
func (c *Client) RootDiskDevice(storageGB int) map[string]string {
	if storageGB < 1 {
		storageGB = 10
	}
	pool := c.cfg.StoragePool
	if pool == "" {
		pool = quotaStoragePool
	}
	return map[string]string{
		"type": "disk",
		"pool": pool,
		"path": "/",
		"size": fmt.Sprintf("%dGiB", storageGB),
	}
}

// StoragePoolDriver returns the driver of the configured pool, or "".
func (c *Client) StoragePoolDriver() string {
	name := strings.TrimSpace(c.cfg.StoragePool)
	if name == "" {
		return ""
	}
	pool, _, err := c.server.GetStoragePool(name)
	if err != nil {
		return ""
	}
	return pool.Driver
}
