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
	ID        uuid.UUID      `json:"id"`
	Name      string         `json:"name"`
	IncusName string         `json:"incus_name"`
	Image     string         `json:"image"`
	Status    InstanceStatus `json:"status"`
	// CPUCores may be fractional (e.g. 0.5). Whole cores pin via cpuset;
	// fractions use a hard CFS quota (limits.cpu.allowance).
	CPUCores  float64 `json:"cpu_cores"`
	MemoryMB  int     `json:"memory_mb"`
	StorageGB int     `json:"storage_gb"`
	Processes int     `json:"processes"`
	// BandwidthMbps is eth0 max for both directions (Incus limits.max). 0 = unlimited.
	BandwidthMbps int `json:"bandwidth_mbps"`
	// TrafficMonthlyGB is the calendar-month transfer quota (GiB, rx+tx). 0 = unlimited.
	TrafficMonthlyGB int `json:"traffic_monthly_gb"`
	// TrafficUsedBytes is transfer consumed in TrafficPeriod.
	TrafficUsedBytes int64 `json:"traffic_used_bytes"`
	// TrafficPeriod is UTC YYYY-MM for the current billing window.
	TrafficPeriod string `json:"traffic_period"`
	// TrafficThrottled is true when the monthly quota was exceeded (NIC capped to a trickle).
	TrafficThrottled bool `json:"traffic_throttled"`
	// TrafficCounterSnap is the last observed Incus eth0 rx+tx total (internal accounting).
	TrafficCounterSnap int64  `json:"-"`
	RootPassword       string `json:"root_password,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
	Ports            []PortMapping `json:"ports,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
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
	Name      string  `json:"name"`
	Image     string  `json:"image,omitempty"`
	CPUCores  float64 `json:"cpu_cores,omitempty"` // supports fractions, e.g. 0.5
	MemoryMB  int     `json:"memory_mb,omitempty"`
	StorageGB int     `json:"storage_gb,omitempty"`
	Processes int     `json:"processes,omitempty"`
	// BandwidthMbps: 0 = server default, -1 = unlimited, >0 = Mbit/s both ways.
	BandwidthMbps int `json:"bandwidth_mbps,omitempty"`
	// TrafficMonthlyGB: 0 = server default, -1 = unlimited, >0 = GiB/month (rx+tx).
	TrafficMonthlyGB int `json:"traffic_monthly_gb,omitempty"`
	// InternalPorts is deprecated and ignored. Each VPS gets ports_per_instance
	// contiguous ports mapped 1:1 (host:N → guest:N); sshd listens on the first.
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

// DailyUsagePoint is one UTC day of disk + bandwidth samples for charts.
type DailyUsagePoint struct {
	Date            string `json:"date"` // YYYY-MM-DD (UTC)
	DiskUsedBytes   int64  `json:"disk_used_bytes"`
	DiskTotalBytes  int64  `json:"disk_total_bytes"`
	BandwidthBytes  int64  `json:"bandwidth_bytes"` // rx+tx transferred that day
}

// UsageChartResponse is returned by GET /instances/{id}/usage.
type UsageChartResponse struct {
	InstanceID uuid.UUID         `json:"instance_id"`
	Name       string            `json:"name"`
	Days       int               `json:"days"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Points     []DailyUsagePoint `json:"points"`
}

// HourlyMetricsPoint is one UTC hour of CPU/RAM/disk/bandwidth for charts.
type HourlyMetricsPoint struct {
	Hour               string  `json:"hour"` // RFC3339 UTC hour bucket
	CPUPercent         float64 `json:"cpu_percent"`
	MemoryUsedBytes    int64   `json:"memory_used_bytes"`
	MemoryTotalBytes   int64   `json:"memory_total_bytes"`
	DiskUsedBytes      int64   `json:"disk_used_bytes"`
	DiskTotalBytes     int64   `json:"disk_total_bytes"`
	BandwidthBytes     int64   `json:"bandwidth_bytes"`
}

// InstanceMetricsSeries is hourly history for one VPS.
type InstanceMetricsSeries struct {
	InstanceID uuid.UUID            `json:"instance_id"`
	Name       string               `json:"name"`
	Points     []HourlyMetricsPoint `json:"points"`
}

// MetricsChartResponse is returned by GET /metrics and GET /instances/{id}/metrics.
type MetricsChartResponse struct {
	Hours int                     `json:"hours"`
	From  string                  `json:"from"`
	To    string                  `json:"to"`
	Items []InstanceMetricsSeries `json:"items"`
}

// HostStatsResponse is returned by GET /hoststats (node capacity).
type HostStatsResponse struct {
	Ports HostPortsStats `json:"ports"`
	Disk  HostDiskStats  `json:"disk"`
}

// HostPortsStats is free/used host ports in the configured NAT range.
type HostPortsStats struct {
	RangeStart       int `json:"range_start"`
	RangeEnd         int `json:"range_end"`
	PortsPerInstance int `json:"ports_per_instance"`
	Total            int `json:"total"`
	Used             int `json:"used"`
	Free             int `json:"free"`
}

// HostDiskStats is free/used space on the active Incus storage pool.
type HostDiskStats struct {
	Pool       string `json:"pool"`
	Driver     string `json:"driver"`
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
}
