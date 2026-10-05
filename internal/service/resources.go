package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hdmain/goincus/internal/models"
)

const liveCPUSampleWindow = time.Second

// GetInstanceResources returns live CPU/RAM/disk/bandwidth usage for one VPS.
func (s *Service) GetInstanceResources(ctx context.Context, id uuid.UUID) (*models.InstanceResourcesResponse, error) {
	inst, err := s.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}

	sample1, err1 := s.incus.ResourceSample(inst.IncusName)
	var sample = sample1
	cpuPercent := 0.0
	if err1 == nil {
		timer := time.NewTimer(liveCPUSampleWindow)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		sample2, err2 := s.incus.ResourceSample(inst.IncusName)
		if err2 == nil {
			sample = sample2
			cpuPercent = computeCPUPercent(sample1.CPUUsageNS, sample2.CPUUsageNS, liveCPUSampleWindow, inst.CPUCores)
		}
	} else if inst.Status == models.StatusRunning {
		return nil, fmt.Errorf("resource sample: %w", err1)
	}

	memTotal := sample.MemoryTotalBytes
	if memTotal <= 0 && inst.MemoryMB > 0 {
		memTotal = int64(inst.MemoryMB) * 1024 * 1024
	}
	diskTotal := sample.DiskTotalBytes
	if diskTotal <= 0 && inst.StorageGB > 0 {
		diskTotal = int64(inst.StorageGB) * 1024 * 1024 * 1024
	}

	return &models.InstanceResourcesResponse{
		InstanceID: inst.ID,
		Name:       inst.Name,
		Status:     inst.Status,
		SampledAt:  time.Now().UTC().Format(time.RFC3339),
		CPU: models.ResourceCPU{
			Cores:   inst.CPUCores,
			Percent: clampNonNegFloat(cpuPercent),
		},
		Memory: models.ResourceMemory{
			LimitMB:    inst.MemoryMB,
			TotalBytes: clampNonNeg(memTotal),
			UsedBytes:  clampNonNeg(sample.MemoryUsedBytes),
		},
		Disk: models.ResourceDisk{
			LimitGB:    inst.StorageGB,
			TotalBytes: clampNonNeg(diskTotal),
			UsedBytes:  clampNonNeg(sample.DiskUsedBytes),
		},
		Bandwidth: models.ResourceBandwidth{
			LimitMbps:        inst.BandwidthMbps,
			MonthlyLimitGB:   inst.TrafficMonthlyGB,
			MonthlyUsedBytes: clampNonNeg(inst.TrafficUsedBytes),
			Period:           inst.TrafficPeriod,
			Throttled:        inst.TrafficThrottled,
			TotalBytes:       clampNonNeg(sample.NetBytesTotal),
		},
	}, nil
}
