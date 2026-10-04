package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hdmain/goincus/internal/db"
	"github.com/hdmain/goincus/internal/models"
)

const dailyUsageRetentionDays = 400

// GetUsageChart returns daily disk + bandwidth points for charts.
// days is the lookback window ending today UTC (default 30, max 366).
func (s *Service) GetUsageChart(ctx context.Context, id uuid.UUID, days int) (*models.UsageChartResponse, error) {
	inst, err := s.store.GetInstance(ctx, id)
	if err != nil {
		if err == db.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if days <= 0 {
		days = 30
	}
	if days > 366 {
		days = 366
	}

	to := time.Now().UTC().Truncate(24 * time.Hour)
	from := to.AddDate(0, 0, -(days - 1))

	rows, err := s.store.ListDailyUsage(ctx, id, from, to)
	if err != nil {
		return nil, err
	}

	byDay := make(map[string]db.DailyUsageRow, len(rows))
	for _, r := range rows {
		byDay[r.Day.UTC().Format("2006-01-02")] = r
	}

	points := make([]models.DailyUsagePoint, 0, days)
	var lastDiskUsed, lastDiskTotal int64
	if inst.StorageGB > 0 {
		lastDiskTotal = int64(inst.StorageGB) * 1024 * 1024 * 1024
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		pt := models.DailyUsagePoint{
			Date:           key,
			DiskUsedBytes:  lastDiskUsed,
			DiskTotalBytes: lastDiskTotal,
			BandwidthBytes: 0,
		}
		if row, ok := byDay[key]; ok {
			pt.DiskUsedBytes = row.DiskUsedBytes
			pt.DiskTotalBytes = row.DiskTotalBytes
			pt.BandwidthBytes = row.BandwidthBytes
			lastDiskUsed = row.DiskUsedBytes
			if row.DiskTotalBytes > 0 {
				lastDiskTotal = row.DiskTotalBytes
			}
		}
		points = append(points, pt)
	}

	return &models.UsageChartResponse{
		InstanceID: inst.ID,
		Name:       inst.Name,
		Days:       days,
		From:       from.Format("2006-01-02"),
		To:         to.Format("2006-01-02"),
		Points:     points,
	}, nil
}

// recordDailyUsage upserts today's UTC disk + bandwidth sample.
// monthlyUsed is the current monthly traffic_used_bytes after reconcile.
func (s *Service) recordDailyUsage(ctx context.Context, inst *models.Instance, monthlyUsed int64) error {
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)

	diskUsed, diskTotal, err := s.incus.DiskUsageBytes(inst.IncusName)
	if err != nil {
		// Stopped / missing state: keep previous disk if any; still record bandwidth.
		diskUsed, diskTotal = 0, 0
		if prev, getErr := s.store.GetDailyUsage(ctx, inst.ID, day); getErr == nil && prev != nil {
			diskUsed = prev.DiskUsedBytes
			diskTotal = prev.DiskTotalBytes
		}
	}
	if diskTotal <= 0 && inst.StorageGB > 0 {
		diskTotal = int64(inst.StorageGB) * 1024 * 1024 * 1024
	}
	if diskUsed < 0 {
		diskUsed = 0
	}

	baseline := monthlyUsed
	bandwidth := int64(0)
	if existing, getErr := s.store.GetDailyUsage(ctx, inst.ID, day); getErr == nil && existing != nil {
		baseline = existing.BandwidthBaselineBytes
		if monthlyUsed < baseline {
			// Monthly counter reset mid-day.
			baseline = monthlyUsed
			bandwidth = 0
		} else {
			bandwidth = monthlyUsed - baseline
		}
	} else if getErr != nil && getErr != db.ErrNotFound {
		return getErr
	}

	row := db.DailyUsageRow{
		InstanceID:             inst.ID,
		Day:                    day,
		DiskUsedBytes:          diskUsed,
		DiskTotalBytes:         diskTotal,
		BandwidthBytes:         bandwidth,
		BandwidthBaselineBytes: baseline,
	}
	if err := s.store.UpsertDailyUsage(ctx, row); err != nil {
		return fmt.Errorf("upsert daily usage: %w", err)
	}
	return nil
}

func (s *Service) purgeOldDailyUsage(ctx context.Context) {
	before := time.Now().UTC().AddDate(0, 0, -dailyUsageRetentionDays)
	if err := s.store.PurgeOldDailyUsage(ctx, before); err != nil {
		s.logger.Warn("purge daily usage", "err", err)
	}
}
