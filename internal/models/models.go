package models

import (
	"time"

	"github.com/google/uuid"
)

// InstanceStatus represents the lifecycle state tracked by goincus.
type InstanceStatus string

const (
	StatusPending   InstanceStatus = "pending"
	StatusCreating  InstanceStatus = "creating"
	StatusRunning   InstanceStatus = "running"
	StatusStopped   InstanceStatus = "stopped"
	StatusError     InstanceStatus = "error"
	StatusDeleting  InstanceStatus = "deleting"
	StatusDeleted   InstanceStatus = "deleted"
)

// Instance is a NAT VPS backed by an Incus LXC container.
type Instance struct {
	ID           uuid.UUID      `json:"id"`
	Name         string         `json:"name"`
	IncusName    string         `json:"incus_name"`
	Image        string         `json:"image"`
	Status       InstanceStatus `json:"status"`
	CPUCores     int            `json:"cpu_cores"`
	MemoryMB     int            `json:"memory_mb"`
	StorageGB    int            `json:"storage_gb"`
	Processes    int            `json:"processes"`
	RootPassword string         `json:"root_password,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
	Ports        []PortMapping  `json:"ports,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// PortMapping maps a host external port to a container internal port via Incus proxy.
type PortMapping struct {
	ID           uuid.UUID `json:"id"`
	InstanceID   uuid.UUID `json:"instance_id"`
	Protocol     string    `json:"protocol"`
	HostPort     int       `json:"host_port"`
	InternalPort int       `json:"internal_port"`
	DeviceName   string    `json:"device_name"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateInstanceRequest is the body for provisioning a new NAT VPS.
type CreateInstanceRequest struct {
	Name      string `json:"name"`
	Image     string `json:"image,omitempty"`
	CPUCores  int    `json:"cpu_cores,omitempty"`
	MemoryMB  int    `json:"memory_mb,omitempty"`
	StorageGB int    `json:"storage_gb,omitempty"`
	Processes int    `json:"processes,omitempty"`
	// InternalPorts overrides default port forwards (e.g. [22, 80]).
	InternalPorts []int `json:"internal_ports,omitempty"`
}

// AddPortRequest adds a proxy port mapping to an existing instance.
type AddPortRequest struct {
	Protocol     string `json:"protocol"`
	InternalPort int    `json:"internal_port"`
	// HostPort is optional; when 0, goincus allocates from the configured range.
	HostPort int `json:"host_port,omitempty"`
}

// ErrorResponse is a standard JSON error payload.
type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// HealthResponse reports dependency connectivity.
type HealthResponse struct {
	Status   string            `json:"status"`
	Database string            `json:"database"`
	Redis    string            `json:"redis"`
	Incus    string            `json:"incus"`
	Checks   map[string]string `json:"checks,omitempty"`
}
