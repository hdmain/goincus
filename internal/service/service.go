package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hdmain/goincus/internal/config"
	"github.com/hdmain/goincus/internal/db"
	"github.com/hdmain/goincus/internal/incusclient"
	"github.com/hdmain/goincus/internal/models"
	"github.com/hdmain/goincus/internal/ports"
	"github.com/hdmain/goincus/internal/redisstore"
	"github.com/hdmain/goincus/internal/secrets"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,61}[a-z0-9]$`)

// Service orchestrates DB, Redis, port allocation, and Incus operations.
type Service struct {
	cfg    *config.Config
	store  *db.Store
	redis  *redisstore.Client
	incus  *incusclient.Client
	ports  *ports.Allocator
	logger *slog.Logger
}

// New builds the application service.
func New(
	cfg *config.Config,
	store *db.Store,
	redis *redisstore.Client,
	incus *incusclient.Client,
	allocator *ports.Allocator,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:    cfg,
		store:  store,
		redis:  redis,
		incus:  incus,
		ports:  allocator,
		logger: logger,
	}
}

// Health reports dependency status.
func (s *Service) Health(ctx context.Context) models.HealthResponse {
	resp := models.HealthResponse{
		Status: "ok",
		Checks: map[string]string{},
	}

	if err := s.store.Ping(ctx); err != nil {
		resp.Database = "down"
		resp.Checks["database"] = err.Error()
		resp.Status = "degraded"
	} else {
		resp.Database = "up"
	}

	if err := s.redis.Ping(ctx); err != nil {
		resp.Redis = "down"
		resp.Checks["redis"] = err.Error()
		resp.Status = "degraded"
	} else {
		resp.Redis = "up"
	}

	if err := s.incus.Ping(); err != nil {
		resp.Incus = "down"
		resp.Checks["incus"] = err.Error()
		resp.Status = "degraded"
	} else {
		resp.Incus = "up"
	}

	return resp
}

// CreateInstance provisions a new NAT VPS container asynchronously after DB insert.
func (s *Service) CreateInstance(ctx context.Context, req models.CreateInstanceRequest) (*models.Instance, error) {
	name := strings.TrimSpace(strings.ToLower(req.Name))
	if !namePattern.MatchString(name) {
		return nil, fmt.Errorf("%w: name must be 3-63 chars, lowercase alphanumeric/hyphen", ErrInvalidInput)
	}

	if existing, err := s.store.GetInstanceByName(ctx, name); err == nil && existing != nil {
		return nil, fmt.Errorf("%w: instance name already exists", ErrConflict)
	} else if err != nil && err != db.ErrNotFound {
		return nil, err
	}

	cpu := req.CPUCores
	if cpu <= 0 {
		cpu = s.cfg.Defaults.CPUCores
	}
	mem := req.MemoryMB
	if mem <= 0 {
		mem = s.cfg.Defaults.MemoryMB
	}
	storage := req.StorageGB
	if storage <= 0 {
		storage = s.cfg.Defaults.StorageGB
	}
	procs := req.Processes
	if procs <= 0 {
		procs = s.cfg.Defaults.Processes
	}
	image := req.Image
	if image == "" {
		image = s.cfg.Incus.DefaultImage
	}

	now := time.Now().UTC()
	id := uuid.New()
	incusName := fmt.Sprintf("goincus-%s", id.String()[:8])

	rootPass, err := secrets.RandomPassword(18)
	if err != nil {
		return nil, fmt.Errorf("generate root password: %w", err)
	}

	inst := &models.Instance{
		ID:           id,
		Name:         name,
		IncusName:    incusName,
		Image:        image,
		Status:       models.StatusPending,
		CPUCores:     cpu,
		MemoryMB:     mem,
		StorageGB:    storage,
		Processes:    procs,
		RootPassword: rootPass,
		CreatedAt:    now,
		UpdatedAt:    now,
		Ports:        []models.PortMapping{},
	}

	if err := s.store.CreateInstance(ctx, inst); err != nil {
		return nil, fmt.Errorf("persist instance: %w", err)
	}

	_ = s.redis.SetInstanceState(ctx, id.String(), string(models.StatusCreating), 30*time.Minute)

	go s.provision(context.Background(), inst)

	inst.Status = models.StatusCreating
	return inst, nil
}

func (s *Service) provision(ctx context.Context, inst *models.Instance) {
	_ = s.store.UpdateInstanceStatus(ctx, inst.ID, models.StatusCreating, "")

	blockSize := s.cfg.Ports.PortsPerInstance
	if blockSize <= 0 {
		blockSize = 20
	}

	block, err := s.ports.AllocateBlock(ctx, blockSize)
	if err != nil {
		s.fail(ctx, inst.ID, fmt.Errorf("allocate port block: %w", err))
		return
	}
	sshPort := block[0]
	releaseUnused := func() {
		existing, listErr := s.store.ListPorts(ctx, inst.ID)
		if listErr != nil {
			s.ports.ReleaseBlock(ctx, block)
			return
		}
		have := make(map[int]struct{}, len(existing))
		for _, p := range existing {
			have[p.HostPort] = struct{}{}
		}
		for _, p := range block {
			if _, ok := have[p]; !ok {
				_ = s.ports.Release(ctx, p)
			}
		}
	}

	if err := s.incus.CreateContainer(incusclient.CreateArgs{
		Name:         inst.IncusName,
		Image:        inst.Image,
		CPUCores:     inst.CPUCores,
		MemoryMB:     inst.MemoryMB,
		StorageGB:    inst.StorageGB,
		Processes:    inst.Processes,
		Profiles:     s.cfg.Incus.Profiles,
		RootPassword: inst.RootPassword,
		SSHPort:      sshPort,
	}); err != nil {
		s.ports.ReleaseBlock(ctx, block)
		s.fail(ctx, inst.ID, fmt.Errorf("create container: %w", err))
		return
	}

	if err := s.incus.EnsureStarted(inst.IncusName); err != nil {
		releaseUnused()
		s.fail(ctx, inst.ID, fmt.Errorf("start container: %w", err))
		return
	}

	if _, err := s.incus.EnsureInstanceIPv4(inst.IncusName); err != nil {
		releaseUnused()
		s.fail(ctx, inst.ID, fmt.Errorf("assign ipv4: %w", err))
		return
	}

	// Give the guest a moment to boot before apt/ssh setup.
	time.Sleep(5 * time.Second)

	if err := s.bootstrapSSH(ctx, inst, sshPort); err != nil {
		releaseUnused()
		s.fail(ctx, inst.ID, err)
		return
	}

	if err := s.attachPortBlock(ctx, inst, block); err != nil {
		releaseUnused()
		s.fail(ctx, inst.ID, err)
		return
	}

	if err := s.store.UpdateInstanceStatus(ctx, inst.ID, models.StatusRunning, ""); err != nil {
		s.logger.Error("update status running", "id", inst.ID, "err", err)
	}
	_ = s.redis.SetInstanceState(ctx, inst.ID.String(), string(models.StatusRunning), 24*time.Hour)
	s.logger.Info("instance provisioned", "id", inst.ID, "incus", inst.IncusName, "ports", fmt.Sprintf("%d-%d", block[0], block[len(block)-1]))
}

func (s *Service) bootstrapSSH(ctx context.Context, inst *models.Instance, sshPort int) error {
	pass := inst.RootPassword
	if pass == "" {
		generated, err := secrets.RandomPassword(18)
		if err != nil {
			return fmt.Errorf("generate root password: %w", err)
		}
		pass = generated
		inst.RootPassword = pass
		if err := s.store.UpdateRootPassword(ctx, inst.ID, pass); err != nil {
			return fmt.Errorf("persist root password: %w", err)
		}
	}
	if netName := s.cfg.Incus.Network; netName != "" {
		_ = s.incus.HardenNetwork(netName)
	}
	if _, err := s.incus.EnsureInstanceIPv4(inst.IncusName); err != nil {
		return fmt.Errorf("assign ipv4: %w", err)
	}
	_ = s.incus.ConfigureGuestDNS(inst.IncusName)
	if err := s.incus.EnsureSSH(inst.IncusName, pass, sshPort); err != nil {
		return fmt.Errorf("bootstrap ssh: %w", err)
	}
	return nil
}

// attachPortBlock creates 1:1 proxies host:P → guest:P for every port in the block.
func (s *Service) attachPortBlock(ctx context.Context, inst *models.Instance, block []int) error {
	existing, err := s.store.ListPorts(ctx, inst.ID)
	if err != nil {
		return fmt.Errorf("list ports: %w", err)
	}
	have := map[int]struct{}{}
	for _, p := range existing {
		have[p.HostPort] = struct{}{}
	}

	for _, port := range block {
		if _, ok := have[port]; ok {
			continue
		}
		device := fmt.Sprintf("proxy-%d", port)
		if err := s.incus.AddProxyDevice(inst.IncusName, device, "tcp", port, port); err != nil {
			return fmt.Errorf("proxy device %s: %w", device, err)
		}
		pm := models.PortMapping{
			ID:           uuid.New(),
			InstanceID:   inst.ID,
			Protocol:     "tcp",
			HostPort:     port,
			InternalPort: port,
			DeviceName:   device,
			CreatedAt:    time.Now().UTC(),
		}
		if err := s.store.AddPort(ctx, &pm); err != nil {
			_ = s.incus.RemoveProxyDevice(inst.IncusName, device)
			return fmt.Errorf("persist port: %w", err)
		}
	}
	return nil
}

// instanceSSHPort returns the first port of an existing 1:1 block, or 0 if unknown.
func instanceSSHPort(ports []models.PortMapping) int {
	if len(ports) == 0 {
		return 0
	}
	min := ports[0].HostPort
	for _, p := range ports[1:] {
		if p.HostPort < min {
			min = p.HostPort
		}
	}
	return min
}

// ensurePortBlock makes sure the instance has a full contiguous 1:1 port block.
func (s *Service) ensurePortBlock(ctx context.Context, inst *models.Instance) (sshPort int, err error) {
	blockSize := s.cfg.Ports.PortsPerInstance
	if blockSize <= 0 {
		blockSize = 20
	}

	existing, err := s.store.ListPorts(ctx, inst.ID)
	if err != nil {
		return 0, fmt.Errorf("list ports: %w", err)
	}
	inst.Ports = existing

	if len(existing) == 0 {
		block, err := s.ports.AllocateBlock(ctx, blockSize)
		if err != nil {
			return 0, err
		}
		if err := s.attachPortBlock(ctx, inst, block); err != nil {
			s.ports.ReleaseBlock(ctx, block)
			return 0, err
		}
		return block[0], nil
	}

	sshPort = instanceSSHPort(existing)
	// Fill missing ports in [sshPort, sshPort+blockSize) when they look like a 1:1 block.
	need := make([]int, 0, blockSize)
	have := map[int]struct{}{}
	for _, p := range existing {
		have[p.HostPort] = struct{}{}
	}
	for p := sshPort; p < sshPort+blockSize; p++ {
		if _, ok := have[p]; !ok {
			need = append(need, p)
		}
	}
	for _, port := range need {
		if err := s.ports.Reserve(ctx, port); err != nil {
			// Outside range or taken — skip; keep existing mapping.
			s.logger.Warn("cannot reserve fill port", "port", port, "err", err)
			continue
		}
		device := fmt.Sprintf("proxy-%d", port)
		if err := s.incus.AddProxyDevice(inst.IncusName, device, "tcp", port, port); err != nil {
			_ = s.ports.Release(ctx, port)
			return sshPort, fmt.Errorf("proxy device %s: %w", device, err)
		}
		pm := models.PortMapping{
			ID:           uuid.New(),
			InstanceID:   inst.ID,
			Protocol:     "tcp",
			HostPort:     port,
			InternalPort: port,
			DeviceName:   device,
			CreatedAt:    time.Now().UTC(),
		}
		if err := s.store.AddPort(ctx, &pm); err != nil {
			_ = s.incus.RemoveProxyDevice(inst.IncusName, device)
			_ = s.ports.Release(ctx, port)
			return sshPort, err
		}
	}
	return sshPort, nil
}

func (s *Service) fail(ctx context.Context, id uuid.UUID, cause error) {
	s.logger.Error("provision failed", "id", id, "err", cause)
	_ = s.store.UpdateInstanceStatus(ctx, id, models.StatusError, cause.Error())
	_ = s.redis.SetInstanceState(ctx, id.String(), string(models.StatusError), time.Hour)
}

// ListInstances returns all active instances and refreshes status from Incus.
func (s *Service) ListInstances(ctx context.Context) ([]models.Instance, error) {
	list, err := s.store.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		s.syncStatusFromIncus(ctx, &list[i])
	}
	return list, nil
}

// ResolveInstance loads an instance by UUID or by name.
func (s *Service) ResolveInstance(ctx context.Context, idOrName string) (*models.Instance, error) {
	if id, err := uuid.Parse(idOrName); err == nil {
		return s.GetInstance(ctx, id)
	}
	inst, err := s.store.GetInstanceByName(ctx, strings.ToLower(strings.TrimSpace(idOrName)))
	if err != nil {
		if err == db.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.syncStatusFromIncus(ctx, inst)
	return inst, nil
}

// GetInstance returns one instance by ID and refreshes status from Incus.
func (s *Service) GetInstance(ctx context.Context, id uuid.UUID) (*models.Instance, error) {
	inst, err := s.store.GetInstance(ctx, id)
	if err != nil {
		if err == db.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.syncStatusFromIncus(ctx, inst)
	return inst, nil
}

func (s *Service) syncStatusFromIncus(ctx context.Context, inst *models.Instance) {
	status, err := s.incus.GetStatus(inst.IncusName)
	if err != nil {
		return
	}
	var next models.InstanceStatus
	switch {
	case strings.EqualFold(status, "Running"):
		next = models.StatusRunning
	case strings.EqualFold(status, "Stopped"):
		next = models.StatusStopped
	default:
		return
	}
	if inst.Status == next && (next != models.StatusRunning || inst.ErrorMessage == "") {
		return
	}
	inst.Status = next
	if next == models.StatusRunning {
		inst.ErrorMessage = ""
	}
	_ = s.store.UpdateInstanceStatus(ctx, inst.ID, next, inst.ErrorMessage)
	_ = s.redis.SetInstanceState(ctx, inst.ID.String(), string(next), 24*time.Hour)
}

// StartInstance starts a stopped container, bootstraps SSH if needed, and completes missing ports.
func (s *Service) StartInstance(ctx context.Context, id uuid.UUID) (*models.Instance, error) {
	inst, err := s.store.GetInstance(ctx, id)
	if err != nil {
		if err == db.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := s.incus.EnsureStarted(inst.IncusName); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	sshPort, err := s.ensurePortBlock(ctx, inst)
	if err != nil {
		return nil, err
	}
	if sshPort == 0 {
		sshPort = 22
	}
	if err := s.bootstrapSSH(ctx, inst, sshPort); err != nil {
		return nil, err
	}
	_ = s.store.UpdateInstanceStatus(ctx, id, models.StatusRunning, "")
	_ = s.redis.SetInstanceState(ctx, id.String(), string(models.StatusRunning), 24*time.Hour)
	return s.GetInstance(ctx, id)
}

// RepairInstance clears error state for a live container and attaches missing ports.
func (s *Service) RepairInstance(ctx context.Context, id uuid.UUID) (*models.Instance, error) {
	return s.StartInstance(ctx, id)
}

// StopInstance stops a running container.
func (s *Service) StopInstance(ctx context.Context, id uuid.UUID) (*models.Instance, error) {
	inst, err := s.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.incus.StopContainer(inst.IncusName, false); err != nil {
		return nil, fmt.Errorf("stop: %w", err)
	}
	_ = s.store.UpdateInstanceStatus(ctx, id, models.StatusStopped, "")
	_ = s.redis.SetInstanceState(ctx, id.String(), string(models.StatusStopped), 24*time.Hour)
	return s.GetInstance(ctx, id)
}

// RestartInstance restarts a container.
func (s *Service) RestartInstance(ctx context.Context, id uuid.UUID) (*models.Instance, error) {
	inst, err := s.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.incus.RestartContainer(inst.IncusName); err != nil {
		return nil, fmt.Errorf("restart: %w", err)
	}
	_ = s.store.UpdateInstanceStatus(ctx, id, models.StatusRunning, "")
	_ = s.redis.SetInstanceState(ctx, id.String(), string(models.StatusRunning), 24*time.Hour)
	return s.GetInstance(ctx, id)
}

// DeleteInstance destroys the container and releases ports.
func (s *Service) DeleteInstance(ctx context.Context, id uuid.UUID) error {
	inst, err := s.GetInstance(ctx, id)
	if err != nil {
		return err
	}

	_ = s.store.UpdateInstanceStatus(ctx, id, models.StatusDeleting, "")
	_ = s.redis.SetInstanceState(ctx, id.String(), string(models.StatusDeleting), time.Hour)

	for _, p := range inst.Ports {
		_ = s.incus.RemoveProxyDevice(inst.IncusName, p.DeviceName)
		_ = s.ports.Release(ctx, p.HostPort)
		_ = s.store.DeletePort(ctx, p.ID)
	}

	if err := s.incus.DeleteContainer(inst.IncusName); err != nil {
		// Continue soft-delete even if Incus object is already gone.
		s.logger.Warn("delete container", "incus", inst.IncusName, "err", err)
	}

	_ = s.redis.DeleteInstanceState(ctx, id.String())
	return s.store.SoftDeleteInstance(ctx, id)
}

// AddPortMapping allocates a host port and attaches an Incus proxy device.
func (s *Service) AddPortMapping(ctx context.Context, id uuid.UUID, req models.AddPortRequest) (*models.PortMapping, error) {
	inst, err := s.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.InternalPort <= 0 || req.InternalPort > 65535 {
		return nil, fmt.Errorf("%w: invalid internal_port", ErrInvalidInput)
	}
	proto := strings.ToLower(req.Protocol)
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" && proto != "udp" {
		return nil, fmt.Errorf("%w: protocol must be tcp or udp", ErrInvalidInput)
	}

	hostPort := req.HostPort
	if hostPort == 0 {
		// Prefer 1:1 mapping (public:N ↔ guest:N) when the guest port is free on the host.
		if err := s.ports.Reserve(ctx, req.InternalPort); err == nil {
			hostPort = req.InternalPort
		} else {
			hostPort, err = s.ports.Allocate(ctx)
			if err != nil {
				return nil, err
			}
		}
	} else {
		if err := s.ports.Reserve(ctx, hostPort); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrConflict, err)
		}
	}

	device := fmt.Sprintf("proxy-%s-%d", proto, req.InternalPort)
	// Disambiguate if device already exists for same internal with different proto usage.
	for _, existing := range inst.Ports {
		if existing.DeviceName == device {
			device = fmt.Sprintf("proxy-%s-%d-%s", proto, req.InternalPort, uuid.New().String()[:8])
			break
		}
	}

	if err := s.incus.AddProxyDevice(inst.IncusName, device, proto, hostPort, req.InternalPort); err != nil {
		_ = s.ports.Release(ctx, hostPort)
		return nil, fmt.Errorf("add proxy: %w", err)
	}

	pm := &models.PortMapping{
		ID:           uuid.New(),
		InstanceID:   id,
		Protocol:     proto,
		HostPort:     hostPort,
		InternalPort: req.InternalPort,
		DeviceName:   device,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.store.AddPort(ctx, pm); err != nil {
		_ = s.incus.RemoveProxyDevice(inst.IncusName, device)
		_ = s.ports.Release(ctx, hostPort)
		return nil, err
	}
	return pm, nil
}

// RemovePortMapping removes a proxy device and frees the host port.
func (s *Service) RemovePortMapping(ctx context.Context, instanceID, portID uuid.UUID) error {
	inst, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	pm, err := s.store.GetPort(ctx, instanceID, portID)
	if err != nil {
		if err == db.ErrNotFound {
			return ErrNotFound
		}
		return err
	}

	_ = s.incus.RemoveProxyDevice(inst.IncusName, pm.DeviceName)
	_ = s.ports.Release(ctx, pm.HostPort)
	return s.store.DeletePort(ctx, pm.ID)
}
