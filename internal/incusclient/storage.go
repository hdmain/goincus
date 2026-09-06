package incusclient

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

const (
	quotaStoragePool     = "goincus"
	defaultPoolLoopSize  = "200GiB"
)

// EnsureStoragePool ensures a storage pool that can enforce per-instance disk size.
// The dir driver shows the host disk in `df` and does not isolate guests — we prefer
// zfs → lvm → btrfs (loop-backed) so a 10GiB root actually looks like ~10GiB inside.
func (c *Client) EnsureStoragePool() error {
	wanted := strings.TrimSpace(c.cfg.StoragePool)

	// Prefer a dedicated quota-capable pool for VPS isolation.
	if wanted == "" || wanted == "default" {
		if err := c.ensureQuotaCapablePool(quotaStoragePool); err == nil {
			c.cfg.StoragePool = quotaStoragePool
			return nil
		}
		if wanted == "" {
			wanted = "default"
		}
		c.cfg.StoragePool = wanted
	}

	if pool, _, err := c.server.GetStoragePool(wanted); err == nil {
		if pool.Driver == "dir" && wanted != quotaStoragePool {
			// Config points at dir — still try to stand up goincus for new instances.
			if err := c.ensureQuotaCapablePool(quotaStoragePool); err == nil {
				c.cfg.StoragePool = quotaStoragePool
			}
		}
		return nil
	}

	if err := c.createBestStoragePool(wanted); err != nil {
		return err
	}
	c.cfg.StoragePool = wanted
	return nil
}

func (c *Client) ensureQuotaCapablePool(name string) error {
	if pool, _, err := c.server.GetStoragePool(name); err == nil {
		if pool.Driver == "dir" {
			return fmt.Errorf("pool %q uses dir driver (no df isolation)", name)
		}
		return nil
	}
	return c.createBestStoragePool(name)
}

func (c *Client) createBestStoragePool(name string) error {
	loopSize := defaultPoolLoopSize
	candidates := []struct {
		driver string
		config map[string]string
		need   string // optional host binary hint
	}{
		{"zfs", map[string]string{"size": loopSize}, "zfs"},
		{"lvm", map[string]string{"size": loopSize}, "lvm"},
		{"btrfs", map[string]string{"size": loopSize}, "mkfs.btrfs"},
		{"dir", nil, ""},
	}

	var errs []string
	for _, cand := range candidates {
		if cand.need != "" {
			if _, err := exec.LookPath(cand.need); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", cand.driver, err))
				continue
			}
		}
		// Skip zfs/lvm/btrfs if tools missing for that stack.
		if cand.driver == "zfs" {
			if _, err := exec.LookPath("zpool"); err != nil {
				errs = append(errs, "zfs: zpool not found")
				continue
			}
		}
		if cand.driver == "lvm" {
			if _, err := exec.LookPath("lvcreate"); err != nil {
				errs = append(errs, "lvm: lvcreate not found")
				continue
			}
		}

		req := api.StoragePoolsPost{
			Name:   name,
			Driver: cand.driver,
			StoragePoolPut: api.StoragePoolPut{
				Description: "goincus storage pool (per-instance disk quotas)",
				Config:      cand.config,
			},
		}
		if err := c.server.CreateStoragePool(req); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", cand.driver, err))
			continue
		}
		return nil
	}
	return fmt.Errorf("create storage pool %q: %s", name, strings.Join(errs, "; "))
}

// RootDiskDevice builds the instance root disk with an enforced size quota.
func (c *Client) RootDiskDevice(storageGB int) map[string]string {
	if storageGB < 1 {
		storageGB = 10
	}
	return map[string]string{
		"type": "disk",
		"pool": c.cfg.StoragePool,
		"path": "/",
		"size": fmt.Sprintf("%dGiB", storageGB),
	}
}
