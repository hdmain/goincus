package incusclient

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

// HardenedInstanceConfig returns security keys that confine a multi-tenant VPS guest.
// Goal: unprivileged user namespace, no nesting, no Incus guest API, isolated idmap,
// default seccomp deny, no syscall intercept (intercept widens attack surface).
func HardenedInstanceConfig() map[string]string {
	return map[string]string{
		"security.privileged":             "false",
		"security.nesting":                "false",
		"security.idmap.isolated":         "true",
		"security.guestapi":               "false",
		"security.syscalls.deny_default":  "true",
		"security.syscalls.deny_compat":   "true",
		"security.syscalls.intercept.bpf": "false",
		"security.syscalls.intercept.mknod":               "false",
		"security.syscalls.intercept.mount":              "false",
		"security.syscalls.intercept.setxattr":           "false",
		"security.syscalls.intercept.sysinfo":            "false",
		"security.syscalls.intercept.sched_setscheduler": "false",
		// Do not allow loading arbitrary kernel modules from the guest.
		"linux.kernel_modules": "",
	}
}

// HardenedNIC returns eth0 settings that block MAC/IP spoofing and bridge hairpin to peers.
func HardenedNIC(network, ipv4 string) map[string]string {
	nic := map[string]string{
		"type":                    "nic",
		"network":                 network,
		"name":                    "eth0",
		"security.mac_filtering":  "true",
		"security.ipv4_filtering": "true",
		"security.ipv6_filtering": "true",
	}
	// Port isolation: block guest↔guest on the same bridge (egress to internet still OK via host NAT).
	nic["security.port_isolation"] = "true"
	if ipv4 != "" {
		nic["ipv4.address"] = ipv4
	}
	return nic
}

// EnsureUnprivilegedProfile creates or updates the hardened multi-tenant profile.
func (c *Client) EnsureUnprivilegedProfile() error {
	_ = EnsureHostIsolation()

	cfg := HardenedInstanceConfig()
	profile := api.ProfilesPost{
		Name: UnprivilegedProfile,
		ProfilePut: api.ProfilePut{
			Description: "goincus multi-tenant isolation — unprivileged, no nesting, isolated idmap, filtered NIC",
			Config:      cfg,
			Devices:     map[string]map[string]string{},
		},
	}

	existing, etag, err := c.server.GetProfile(UnprivilegedProfile)
	if err != nil {
		return c.server.CreateProfile(profile)
	}

	existing.Description = profile.Description
	if existing.Config == nil {
		existing.Config = map[string]string{}
	}
	for k, v := range cfg {
		existing.Config[k] = v
	}
	// Never leave intercept/nesting/privileged enabled from older profile versions.
	existing.Config["security.privileged"] = "false"
	existing.Config["security.nesting"] = "false"
	existing.Config["security.guestapi"] = "false"
	delete(existing.Config, "limits.kernel.pid_max")
	// Profile must not attach host paths or privileged devices.
	if existing.Devices == nil {
		existing.Devices = map[string]map[string]string{}
	}
	for name, dev := range existing.Devices {
		if dev["type"] == "disk" && dev["path"] != "" && dev["path"] != "/" && dev["source"] != "" {
			delete(existing.Devices, name)
		}
		if dev["type"] == "unix-char" || dev["type"] == "unix-block" || dev["type"] == "gpu" || dev["type"] == "infiniband" {
			delete(existing.Devices, name)
		}
	}
	return c.server.UpdateProfile(UnprivilegedProfile, existing.Writable(), etag)
}

// EnsureHostIsolation expands subuid/subgid and applies host sysctl hardening for containers.
func EnsureHostIsolation() error {
	if err := ensureLargeIDMap(); err != nil {
		return err
	}
	_ = applyHostSysctlHardening()
	return nil
}

func ensureLargeIDMap() error {
	// Isolated idmaps need a large contiguous allocation for root.
	const entry = "root:1000000:1000000000"
	for _, path := range []string{"/etc/subuid", "/etc/subgid"} {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				if err := os.WriteFile(path, []byte(entry+"\n"), 0o644); err != nil {
					return err
				}
				continue
			}
			return err
		}
		text := string(data)
		if strings.Contains(text, "root:1000000:1000000000") {
			continue
		}
		// Keep existing root maps; append a large secondary range if none is huge.
		hasHuge := false
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "root:") {
				continue
			}
			parts := strings.Split(line, ":")
			if len(parts) != 3 {
				continue
			}
			size, _ := strconv.Atoi(parts[2])
			if size >= 65536*16 {
				hasHuge = true
				break
			}
		}
		if hasHuge {
			continue
		}
		if !strings.HasSuffix(text, "\n") && text != "" {
			text += "\n"
		}
		if err := os.WriteFile(path, []byte(text+entry+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func applyHostSysctlHardening() error {
	content := `# Managed by goincus — reduce container escape / info-leak surface
kernel.unprivileged_bpf_disabled = 1
kernel.kptr_restrict = 2
kernel.dmesg_restrict = 1
kernel.yama.ptrace_scope = 1
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
net.ipv4.conf.all.accept_redirects = 0
net.ipv6.conf.all.accept_redirects = 0
`
	_ = os.MkdirAll("/etc/sysctl.d", 0o755)
	if err := os.WriteFile("/etc/sysctl.d/99-goincus-isolation.conf", []byte(content), 0o644); err != nil {
		return err
	}
	_ = exec.Command("sysctl", "--system").Run()
	return nil
}

// sanitizeProfiles drops the Incus "default" profile (often shares host NIC/disk)
// and always includes the hardened goincus profile.
func sanitizeProfiles(profiles []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(profiles)+1)
	add := func(name string) {
		if name == "" || name == "default" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	add(UnprivilegedProfile)
	for _, p := range profiles {
		add(p)
	}
	if len(out) == 0 {
		return []string{UnprivilegedProfile}
	}
	return out
}

// mergeConfig overlays b onto a copy of a.
func mergeConfig(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// IsolationError wraps start failures that look like idmap/isolation issues.
func IsolationError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "idmap") || strings.Contains(msg, "newuidmap") || strings.Contains(msg, "subuid") {
		return fmt.Errorf("%w (ensure /etc/subuid and /etc/subgid give root a large range, e.g. root:1000000:1000000000)", err)
	}
	return err
}
