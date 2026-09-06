package incusclient

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/hdmain/goincus/internal/config"
)

const (
	// UnprivilegedProfile is the Incus profile enforcing escape hardening.
	UnprivilegedProfile = "goincus-unprivileged"
)

// Client wraps the official Incus Go SDK for container lifecycle operations.
type Client struct {
	server incus.InstanceServer
	cfg    config.IncusConfig
}

// Connect dials the local Incus daemon over its unix socket.
func Connect(cfg config.IncusConfig) (*Client, error) {
	server, err := incus.ConnectIncusUnix(cfg.SocketPath, nil)
	if err != nil {
		return nil, fmt.Errorf("connect incus: %w", err)
	}

	if cfg.Project != "" && cfg.Project != "default" {
		server = server.UseProject(cfg.Project)
	}

	c := &Client{server: server, cfg: cfg}
	if err := c.EnsureUnprivilegedProfile(); err != nil {
		return nil, err
	}
	if err := c.EnsureInfrastructure(); err != nil {
		return nil, err
	}
	return c, nil
}

// Ping verifies Incus API reachability.
func (c *Client) Ping() error {
	_, _, err := c.server.GetServer()
	return err
}

// EnsureInfrastructure makes sure the configured storage pool and network exist.
// If the configured network is missing, it prefers an existing bridge; otherwise it creates one.
func (c *Client) EnsureInfrastructure() error {
	if err := c.EnsureStoragePool(); err != nil {
		return err
	}
	network, err := c.EnsureNetwork()
	if err != nil {
		return err
	}
	c.cfg.Network = network
	if err := c.alignDefaultProfileNetwork(network); err != nil {
		return err
	}
	return nil
}

// alignDefaultProfileNetwork rewrites default profile eth0 to a network that exists.
func (c *Client) alignDefaultProfileNetwork(network string) error {
	profile, etag, err := c.server.GetProfile("default")
	if err != nil {
		return nil
	}
	changed := false
	if profile.Devices == nil {
		profile.Devices = map[string]map[string]string{}
	}
	eth0, ok := profile.Devices["eth0"]
	if !ok {
		profile.Devices["eth0"] = map[string]string{
			"type":    "nic",
			"network": network,
			"name":    "eth0",
		}
		changed = true
	} else if eth0["network"] != network {
		eth0["network"] = network
		eth0["type"] = "nic"
		if eth0["name"] == "" {
			eth0["name"] = "eth0"
		}
		profile.Devices["eth0"] = eth0
		changed = true
	}
	if !changed {
		return nil
	}
	return c.server.UpdateProfile("default", profile.Writable(), etag)
}

// EnsureStoragePool creates the configured storage pool when absent.
func (c *Client) EnsureStoragePool() error {
	name := c.cfg.StoragePool
	if name == "" {
		name = "default"
		c.cfg.StoragePool = name
	}
	if _, _, err := c.server.GetStoragePool(name); err == nil {
		return nil
	}

	req := api.StoragePoolsPost{
		Name:   name,
		Driver: "dir",
		StoragePoolPut: api.StoragePoolPut{
			Description: "goincus default storage pool",
		},
	}
	if err := c.server.CreateStoragePool(req); err != nil {
		return fmt.Errorf("create storage pool %q: %w", name, err)
	}
	return nil
}

// EnsureNetwork returns a usable managed network name, creating one if needed.
func (c *Client) EnsureNetwork() (string, error) {
	wanted := c.cfg.Network
	if wanted == "" {
		wanted = "incusbr0"
	}

	if _, _, err := c.server.GetNetwork(wanted); err == nil {
		return wanted, nil
	}

	// Prefer any existing bridge network instead of failing hard.
	networks, err := c.server.GetNetworks()
	if err == nil {
		for _, n := range networks {
			if n.Type == "bridge" && n.Managed {
				return n.Name, nil
			}
		}
		for _, n := range networks {
			if n.Managed {
				return n.Name, nil
			}
		}
	}

	req := api.NetworksPost{
		Name: wanted,
		Type: "bridge",
		NetworkPut: api.NetworkPut{
			Description: "goincus NAT bridge",
			Config: map[string]string{
				"ipv4.address": "auto",
				"ipv4.nat":     "true",
				"ipv6.address": "none",
			},
		},
	}
	if err := c.server.CreateNetwork(req); err != nil {
		return "", fmt.Errorf("create network %q: %w", wanted, err)
	}
	return wanted, nil
}

// EnsureUnprivilegedProfile creates or updates the hardened security profile.
// Unprivileged containers with nesting disabled reduce escape surface.
func (c *Client) EnsureUnprivilegedProfile() error {
	profile := api.ProfilesPost{
		Name: UnprivilegedProfile,
		ProfilePut: api.ProfilePut{
			Description: "goincus hardened unprivileged profile — blocks common container escape vectors",
			Config: map[string]string{
				"security.privileged": "false",
				"security.nesting":    "false",
				// Isolated idmaps often break starts when host subuid/subgid ranges are tight.
				"security.idmap.isolated": "false",
			},
			Devices: map[string]map[string]string{},
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
	for k, v := range profile.Config {
		existing.Config[k] = v
	}
	// Drop keys that previously caused start failures on some hosts.
	delete(existing.Config, "limits.kernel.pid_max")
	delete(existing.Config, "security.syscalls.intercept.mknod")
	delete(existing.Config, "security.syscalls.intercept.setxattr")
	delete(existing.Config, "security.syscalls.intercept.sysinfo")
	delete(existing.Config, "security.syscalls.intercept.mount")
	delete(existing.Config, "security.syscalls.intercept.sched_setscheduler")
	return c.server.UpdateProfile(UnprivilegedProfile, existing.Writable(), etag)
}

// CreateArgs describes a new NAT VPS container.
type CreateArgs struct {
	Name      string
	Image     string
	CPUCores  int
	MemoryMB  int
	StorageGB int
	Processes int
	Profiles  []string
}

// CreateContainer provisions an unprivileged LXC container with resource limits.
func (c *Client) CreateContainer(args CreateArgs) error {
	if err := c.EnsureInfrastructure(); err != nil {
		return err
	}

	image := args.Image
	if image == "" {
		image = c.cfg.DefaultImage
	}

	profiles := args.Profiles
	if len(profiles) == 0 {
		profiles = c.cfg.Profiles
	}
	profiles = c.filterAvailableProfiles(profiles)

	req := api.InstancesPost{
		Name: args.Name,
		Type: api.InstanceTypeContainer,
		Source: api.InstanceSource{
			Type:     "image",
			Alias:    image,
			Server:   c.cfg.ImageServer,
			Protocol: c.cfg.ImageProtocol,
		},
		InstancePut: api.InstancePut{
			Profiles: profiles,
			Config:   ResourceConfig(args.CPUCores, args.MemoryMB, args.Processes),
			Devices: map[string]map[string]string{
				"root": {
					"type": "disk",
					"pool": c.cfg.StoragePool,
					"path": "/",
					"size": fmt.Sprintf("%dGiB", args.StorageGB),
				},
				"eth0": {
					"type":    "nic",
					"network": c.cfg.Network,
					"name":    "eth0",
				},
			},
		},
	}

	op, err := c.server.CreateInstance(req)
	if err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait create instance: %w", err)
	}
	return nil
}

func (c *Client) filterAvailableProfiles(profiles []string) []string {
	out := make([]string, 0, len(profiles))
	for _, name := range profiles {
		if _, _, err := c.server.GetProfile(name); err != nil {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return []string{UnprivilegedProfile}
	}
	return out
}

// ResourceConfig builds Incus instance config keys for CPU, memory, and process limits.
func ResourceConfig(cpuCores, memoryMB, processes int) map[string]string {
	return map[string]string{
		"limits.cpu":           strconv.Itoa(cpuCores),
		"limits.cpu.allowance": "100%",
		"limits.memory":        fmt.Sprintf("%dMiB", memoryMB),
		"limits.memory.swap":   "false",
		"limits.processes":     strconv.Itoa(processes),
		"security.privileged":  "false",
		"security.nesting":     "false",
	}
}

// StartContainer starts a container.
func (c *Client) StartContainer(name string) error {
	if err := c.updateState(name, "start"); err != nil {
		return fmt.Errorf("%w%s", err, c.logSuffix(name))
	}
	return nil
}

// StopContainer stops a container.
func (c *Client) StopContainer(name string, force bool) error {
	req := api.InstanceStatePut{
		Action:  "stop",
		Timeout: 30,
		Force:   force,
	}
	op, err := c.server.UpdateInstanceState(name, req, "")
	if err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait stop instance: %w%s", err, c.logSuffix(name))
	}
	return nil
}

// RestartContainer restarts a container.
func (c *Client) RestartContainer(name string) error {
	if err := c.updateState(name, "restart"); err != nil {
		return fmt.Errorf("%w%s", err, c.logSuffix(name))
	}
	return nil
}

func (c *Client) updateState(name, action string) error {
	req := api.InstanceStatePut{
		Action:  action,
		Timeout: 120,
	}
	op, err := c.server.UpdateInstanceState(name, req, "")
	if err != nil {
		return fmt.Errorf("%s instance: %w", action, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait %s instance: %w", action, err)
	}
	return nil
}

func (c *Client) logSuffix(name string) string {
	snippet := c.InstanceLogSnippet(name)
	if snippet == "" {
		return ""
	}
	return "; lxc.log: " + snippet
}

// InstanceLogSnippet returns the tail of lxc.log for diagnostics.
func (c *Client) InstanceLogSnippet(name string) string {
	rc, err := c.server.GetInstanceLogfile(name, "lxc.log")
	if err != nil {
		return ""
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 64*1024))
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	out := strings.Join(lines, " | ")
	out = strings.ReplaceAll(out, "\n", " ")
	if len(out) > 1500 {
		out = "..." + out[len(out)-1500:]
	}
	return out
}

// DeleteContainer removes a container. Force-stops if still running.
func (c *Client) DeleteContainer(name string) error {
	inst, _, err := c.server.GetInstance(name)
	if err == nil && inst.StatusCode == api.Running {
		_ = c.StopContainer(name, true)
	}

	op, err := c.server.DeleteInstance(name)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	return op.Wait()
}

// GetStatus returns the Incus status string for a container.
func (c *Client) GetStatus(name string) (string, error) {
	inst, _, err := c.server.GetInstance(name)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// AddProxyDevice attaches an Incus proxy device mapping host:external -> container:internal.
func (c *Client) AddProxyDevice(instanceName, deviceName, protocol string, hostPort, internalPort int) error {
	inst, etag, err := c.server.GetInstance(instanceName)
	if err != nil {
		return fmt.Errorf("get instance: %w", err)
	}

	if inst.Devices == nil {
		inst.Devices = map[string]map[string]string{}
	}

	inst.Devices[deviceName] = map[string]string{
		"type":    "proxy",
		"listen":  fmt.Sprintf("%s:0.0.0.0:%d", protocol, hostPort),
		"connect": fmt.Sprintf("%s:127.0.0.1:%d", protocol, internalPort),
		"nat":     "true",
	}

	op, err := c.server.UpdateInstance(instanceName, inst.Writable(), etag)
	if err != nil {
		return fmt.Errorf("add proxy device: %w", err)
	}
	return op.Wait()
}

// RemoveProxyDevice deletes a proxy device from the container.
func (c *Client) RemoveProxyDevice(instanceName, deviceName string) error {
	inst, etag, err := c.server.GetInstance(instanceName)
	if err != nil {
		return fmt.Errorf("get instance: %w", err)
	}

	if _, ok := inst.Devices[deviceName]; !ok {
		return nil
	}
	delete(inst.Devices, deviceName)

	op, err := c.server.UpdateInstance(instanceName, inst.Writable(), etag)
	if err != nil {
		return fmt.Errorf("remove proxy device: %w", err)
	}
	return op.Wait()
}

// UpdateResourceLimits adjusts CPU/memory/process limits on a running definition.
func (c *Client) UpdateResourceLimits(name string, cpuCores, memoryMB, processes int) error {
	inst, etag, err := c.server.GetInstance(name)
	if err != nil {
		return err
	}
	for k, v := range ResourceConfig(cpuCores, memoryMB, processes) {
		inst.Config[k] = v
	}
	op, err := c.server.UpdateInstance(name, inst.Writable(), etag)
	if err != nil {
		return err
	}
	return op.Wait()
}

// UpdateStorageQuota sets the root disk size quota.
func (c *Client) UpdateStorageQuota(name string, storageGB int) error {
	inst, etag, err := c.server.GetInstance(name)
	if err != nil {
		return err
	}
	root, ok := inst.Devices["root"]
	if !ok {
		root = map[string]string{
			"type": "disk",
			"pool": c.cfg.StoragePool,
			"path": "/",
		}
	}
	root["size"] = fmt.Sprintf("%dGiB", storageGB)
	inst.Devices["root"] = root

	op, err := c.server.UpdateInstance(name, inst.Writable(), etag)
	if err != nil {
		return err
	}
	return op.Wait()
}
