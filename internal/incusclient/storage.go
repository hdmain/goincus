package incusclient

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

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
// Installs LVM (and optionally ZFS) host packages when missing, then creates a loop-backed pool.
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

	// Auto-install disk tools, then create/select a quota-capable pool.
	if err := ensureStorageHostPackages(); err != nil {
		slog.Warn("storage package install", "err", err)
	}

	names := uniquePoolNames(wanted, quotaStoragePool, quotaStoragePool+"-lvm", quotaStoragePool+"-zfs")
	var lastErr error
	for _, name := range names {
		if err := c.ensureQuotaCapablePool(name); err != nil {
			lastErr = err
			continue
		}
		if pool, _, err := c.server.GetStoragePool(name); err == nil {
			c.cfg.StoragePool = name
			slog.Info("storage pool ready", "pool", name, "driver", pool.Driver)
			return nil
		}
		c.cfg.StoragePool = name
		return nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no suitable pool")
	}
	return fmt.Errorf(
		"could not prepare zfs/lvm storage pool automatically: %w "+
			"(dir/btrfs always show host disk in guest df)",
		lastErr,
	)
}

// ActiveStoragePool returns the pool name currently used for new instances.
func (c *Client) ActiveStoragePool() string {
	return strings.TrimSpace(c.cfg.StoragePool)
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
	// Prefer LVM: we auto-install it and it makes df show the quota. ZFS if already present.
	candidates := []struct {
		driver string
		config map[string]string
		ready  func() bool
	}{
		{"lvm", map[string]string{"size": loopSize}, hasLVMTools},
		{"zfs", map[string]string{"size": loopSize}, hasZFSTools},
	}

	var errs []string
	for _, cand := range candidates {
		if !cand.ready() {
			errs = append(errs, fmt.Sprintf("%s: tools missing", cand.driver))
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

func hasLVMTools() bool {
	_, err1 := exec.LookPath("lvcreate")
	_, err2 := exec.LookPath("vgcreate")
	return err1 == nil && err2 == nil
}

func hasZFSTools() bool {
	_, err1 := exec.LookPath("zpool")
	_, err2 := exec.LookPath("zfs")
	return err1 == nil && err2 == nil
}

// ensureStorageHostPackages installs LVM (required for df-isolated pools) and
// optionally ZFS when the package manager is available. Safe to call repeatedly.
func ensureStorageHostPackages() error {
	if hasLVMTools() {
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("not root; cannot install lvm2")
	}

	slog.Info("installing host storage packages (lvm2, thin-provisioning-tools)")
	env := map[string]string{
		"DEBIAN_FRONTEND": "noninteractive",
		"NEEDRESTART_MODE": "a",
	}

	switch {
	case lookPath("apt-get"):
		_ = runEnv(env, "apt-get", "update", "-y")
		if err := runEnv(env, "apt-get", "install", "-y", "lvm2", "thin-provisioning-tools"); err != nil {
			return fmt.Errorf("apt-get install lvm2: %w", err)
		}
		// Best-effort ZFS (large; may need reboot / DKMS — ignore failure).
		_ = runEnv(env, "apt-get", "install", "-y", "zfsutils-linux")
	case lookPath("dnf"):
		if err := runEnv(nil, "dnf", "install", "-y", "lvm2"); err != nil {
			return fmt.Errorf("dnf install lvm2: %w", err)
		}
	case lookPath("yum"):
		if err := runEnv(nil, "yum", "install", "-y", "lvm2"); err != nil {
			return fmt.Errorf("yum install lvm2: %w", err)
		}
	default:
		return fmt.Errorf("no apt-get/dnf/yum found to install lvm2")
	}

	// Give udev/lvm a moment after package install.
	time.Sleep(500 * time.Millisecond)
	if !hasLVMTools() {
		return fmt.Errorf("lvm2 installed but lvcreate still not in PATH")
	}
	slog.Info("host storage packages ready", "lvm", true, "zfs", hasZFSTools())
	return nil
}

func lookPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func runEnv(env map[string]string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
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
