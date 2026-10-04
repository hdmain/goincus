package service

import (
	"context"
	"fmt"

	"github.com/hdmain/goincus/internal/models"
)

// GetHostStats returns free host ports and free disk on the active storage pool.
func (s *Service) GetHostStats(ctx context.Context) (*models.HostStatsResponse, error) {
	portStats, err := s.ports.Stats(ctx)
	if err != nil {
		return nil, fmt.Errorf("port stats: %w", err)
	}
	disk, err := s.incus.StoragePoolSpace()
	if err != nil {
		return nil, fmt.Errorf("disk stats: %w", err)
	}
	return &models.HostStatsResponse{
		Ports: models.HostPortsStats{
			RangeStart:       portStats.RangeStart,
			RangeEnd:         portStats.RangeEnd,
			PortsPerInstance: portStats.PortsPerInstance,
			Total:            portStats.Total,
			Used:             portStats.Used,
			Free:             portStats.Free,
		},
		Disk: models.HostDiskStats{
			Pool:       disk.Pool,
			Driver:     disk.Driver,
			TotalBytes: int64(disk.TotalBytes),
			UsedBytes:  int64(disk.UsedBytes),
			FreeBytes:  int64(disk.FreeBytes),
		},
	}, nil
}
