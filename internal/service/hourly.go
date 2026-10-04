package service

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/hdmain/goincus/internal/db"
	"github.com/hdmain/goincus/internal/models"
)

// StartBackgroundJobs launches traffic/daily sampling and optional hourly metrics.
func (s *Service) StartBackgroundJobs(ctx context.Context) {
	go s.trafficLoop(ctx)
	if s.cfg.Usage.HourlyEnabled {
		go s.hourlyMetricsLoop(ctx)
	}
}

func (s *Service) hourlyMetricsLoop(ctx context.Context) {
	interval := s.cfg.Usage.HourlyInterval.Duration()
	if interval <= 0 {
		interval = time.Hour
	}
	// Align first tick soon after start, then on interval.
	s.sampleAllHourlyMetrics(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sampleAllHourlyMetrics(context.Background())
		}
	}
}

func (s *Service) sampleAllHourlyMetrics(ctx context.Context) {
	list, err := s.store.ListActiveInstances(ctx)
	if err != nil {
		s.logger.Warn("hourly metrics list", "err", err)
		return
	}
	hour := time.Now().UTC().Truncate(time.Hour)
	for i := range list {
		inst := &list[i]
		if inst.Status != models.StatusRunning && inst.Status != models.StatusStopped {
			continue
		}
		if err := s.sampleInstanceHourlyMetrics(ctx, inst, hour); err != nil {
			s.logger.Warn("hourly metrics sample", "name", inst.Name, "err", err)
		}
	}
	retention := s.cfg.Usage.HourlyRetentionDays
	if retention <= 0 {
		retention = 30
	}
	if err := s.store.PurgeOldHourlyMetrics(ctx, time.Now().UTC().AddDate(0, 0, -retention)); err != nil {
		s.logger.Warn("purge hourly metrics", "err", err)
	}
}

func (s *Service) sampleInstanceHourlyMetrics(ctx context.Context, inst *models.Instance, hour time.Time) error {
	sample, err := s.incus.ResourceSample(inst.IncusName)
	if err != nil {
		// Stopped guests may still expose disk/memory; ignore hard failures.
		return err
	}

	memTotal := sample.MemoryTotalBytes
	if memTotal <= 0 && inst.MemoryMB > 0 {
		memTotal = int64(inst.MemoryMB) * 1024 * 1024
	}
	diskTotal := sample.DiskTotalBytes
	if diskTotal <= 0 && inst.StorageGB > 0 {
		diskTotal = int64(inst.StorageGB) * 1024 * 1024 * 1024
	}

	cpuPercent := 0.0
	bandwidth := int64(0)

	// Same-hour refresh: accumulate deltas from counters stored in this bucket.
	existing, existErr := s.store.ListHourlyMetrics(ctx, inst.ID, hour, hour)
	if existErr != nil {
		return existErr
	}
	if len(existing) > 0 {
		prev := existing[0]
		elapsed := s.cfg.Usage.HourlyInterval.Duration()
		if elapsed <= 0 {
			elapsed = time.Hour
		}
		cpuPercent = computeCPUPercent(prev.CPUUsageNS, sample.CPUUsageNS, elapsed, inst.CPUCores)
		deltaNet := int64(0)
		if sample.NetBytesTotal >= prev.NetBytesTotal {
			deltaNet = sample.NetBytesTotal - prev.NetBytesTotal
		} else {
			deltaNet = sample.NetBytesTotal
		}
		bandwidth = prev.BandwidthBytes + deltaNet
	} else {
		prev, prevErr := s.store.LatestHourlyMetrics(ctx, inst.ID, hour)
		if prevErr == nil && prev != nil {
			elapsed := hour.Sub(prev.Hour)
			if elapsed <= 0 {
				elapsed = s.cfg.Usage.HourlyInterval.Duration()
				if elapsed <= 0 {
					elapsed = time.Hour
				}
			}
			cpuPercent = computeCPUPercent(prev.CPUUsageNS, sample.CPUUsageNS, elapsed, inst.CPUCores)
			if sample.NetBytesTotal >= prev.NetBytesTotal {
				bandwidth = sample.NetBytesTotal - prev.NetBytesTotal
			} else {
				bandwidth = sample.NetBytesTotal
			}
		} else if prevErr != nil && prevErr != db.ErrNotFound {
			return prevErr
		}
	}

	row := db.HourlyMetricsRow{
		InstanceID:       inst.ID,
		Hour:             hour,
		CPUPercent:       cpuPercent,
		MemoryUsedBytes:  sample.MemoryUsedBytes,
		MemoryTotalBytes: memTotal,
		DiskUsedBytes:    sample.DiskUsedBytes,
		DiskTotalBytes:   diskTotal,
		BandwidthBytes:   bandwidth,
		CPUUsageNS:       sample.CPUUsageNS,
		NetBytesTotal:    sample.NetBytesTotal,
	}
	return s.store.UpsertHourlyMetrics(ctx, row)
}

func computeCPUPercent(prevNS, curNS int64, elapsed time.Duration, cpuCores float64) float64 {
	if elapsed <= 0 || cpuCores <= 0 {
		return 0
	}
	if curNS < prevNS {
		return 0
	}
	delta := float64(curNS - prevNS)
	capacity := float64(elapsed.Nanoseconds()) * cpuCores
	if capacity <= 0 {
		return 0
	}
	pct := (delta / capacity) * 100
	if pct < 0 {
		return 0
	}
	if pct > 100*cpuCores {
		pct = 100 * cpuCores
	}
	return math.Round(pct*100) / 100
}

// GetHourlyMetrics returns hourly CPU/RAM/disk/bandwidth history.
// If id is uuid.Nil, returns all instances ("all hosts"/VPS).
func (s *Service) GetHourlyMetrics(ctx context.Context, id uuid.UUID, hours int) (*models.MetricsChartResponse, error) {
	if hours <= 0 {
		hours = 24
	}
	if hours > 24*90 {
		hours = 24 * 90
	}
	to := time.Now().UTC().Truncate(time.Hour)
	from := to.Add(-time.Duration(hours-1) * time.Hour)

	var rows []db.HourlyMetricsRow
	var err error
	var filterID uuid.UUID
	if id != uuid.Nil {
		if _, err := s.store.GetInstance(ctx, id); err != nil {
			if err == db.ErrNotFound {
				return nil, ErrNotFound
			}
			return nil, err
		}
		filterID = id
		rows, err = s.store.ListHourlyMetrics(ctx, id, from, to)
	} else {
		rows, err = s.store.ListHourlyMetricsAll(ctx, from, to)
	}
	if err != nil {
		return nil, err
	}

	// Map instance id -> name
	list, err := s.store.ListActiveInstances(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(list))
	order := make([]uuid.UUID, 0, len(list))
	for _, inst := range list {
		if filterID != uuid.Nil && inst.ID != filterID {
			continue
		}
		names[inst.ID] = inst.Name
		order = append(order, inst.ID)
	}

	byInst := make(map[uuid.UUID][]models.HourlyMetricsPoint)
	for _, r := range rows {
		byInst[r.InstanceID] = append(byInst[r.InstanceID], models.HourlyMetricsPoint{
			Hour:             r.Hour.UTC().Format(time.RFC3339),
			CPUPercent:       r.CPUPercent,
			MemoryUsedBytes:  r.MemoryUsedBytes,
			MemoryTotalBytes: r.MemoryTotalBytes,
			DiskUsedBytes:    r.DiskUsedBytes,
			DiskTotalBytes:   r.DiskTotalBytes,
			BandwidthBytes:   r.BandwidthBytes,
		})
	}

	items := make([]models.InstanceMetricsSeries, 0, len(order))
	for _, iid := range order {
		pts := byInst[iid]
		if pts == nil {
			pts = []models.HourlyMetricsPoint{}
		}
		items = append(items, models.InstanceMetricsSeries{
			InstanceID: iid,
			Name:       names[iid],
			Points:     pts,
		})
	}

	return &models.MetricsChartResponse{
		Hours: hours,
		From:  from.Format(time.RFC3339),
		To:    to.Format(time.RFC3339),
		Items: items,
	}, nil
}
