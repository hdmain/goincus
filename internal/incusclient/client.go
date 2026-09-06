package incusclient

import (
	"fmt"
	"strconv"

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
	return c, nil
}

// Ping verifies Incus API reachability.
func (c *Client) Ping() error {
	_, _, err := c.server.GetServer()
	return err
}

// EnsureUnprivilegedProfile creates or updates the hardened security profile.
// Unprivileged containers, isolated idmaps, and disabled nesting reduce escape surface.
func (c *Client) EnsureUnprivilegedProfile() error {
	profile := api.ProfilesPost{
		Name: UnprivilegedProfile,
		ProfilePut: api.ProfilePut{
			Description: "goincus hardened unprivileged profile — blocks common container escape vectors",
			Config: map[string]string{
				"security.privileged":                    "false",
				"security.nesting":                       "false",
				"security.idmap.isolated":                "true",
				"security.syscalls.intercept.mknod":       "false",
				"security.syscalls.intercept.setxattr":    "false",
				"security.syscalls.intercept.sysinfo":     "false",
				"security.syscalls.intercept.mount":       "false",
				"security.syscalls.intercept.sched_setscheduler": "false",
				"limits.kernel.pid_max":                  "4096",
			},
			Devices: map[string]map[string]string{},
		},
	}

	existing, etag, err := c.server.GetProfile(UnprivilegedProfile)
	if err != nil {
		// Profile missing — create it.
		return c.server.CreateProfile(profile)
	}

	existing.Description = profile.Description
	existing.Config = profile.Config
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
	image := args.Image
	if image == "" {
		image = c.cfg.DefaultImage
	}

	profiles := args.Profiles
	if len(profiles) == 0 {
		profiles = c.cfg.Profiles
	}

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

// ResourceConfig builds Incus instance config keys for CPU, memory, and process limits.
func ResourceConfig(cpuCores, memoryMB, processes int) map[string]string {
	return map[string]string{
		"limits.cpu":              strconv.Itoa(cpuCores),
		"limits.cpu.allowance":    "100%",
		"limits.memory":           fmt.Sprintf("%dMiB", memoryMB),
		"limits.memory.swap":      "false",
		"limits.processes":        strconv.Itoa(processes),
		"security.privileged":     "false",
		"security.nesting":        "false",
		"security.idmap.isolated": "true",
	}
}

// StartContainer starts a container.
func (c *Client) StartContainer(name string) error {
	return c.updateState(name, "start")
}

// StopContainer stops a container.
func (c *Client) StopContainer(name string, force bool) error {
	action := "stop"
	req := api.InstanceStatePut{
		Action:  action,
		Timeout: 30,
		Force:   force,
	}
	op, err := c.server.UpdateInstanceState(name, req, "")
	if err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}
	return op.Wait()
}

// RestartContainer restarts a container.
func (c *Client) RestartContainer(name string) error {
	return c.updateState(name, "restart")
}

func (c *Client) updateState(name, action string) error {
	req := api.InstanceStatePut{
		Action:  action,
		Timeout: 30,
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
