package service

import (
	"context"
	"time"

	"github.com/hdmain/goincus/internal/models"
)

const trafficPollInterval = time.Minute

// StartBackgroundJobs launches long-running reconcile loops (monthly traffic, etc.).
func (s *Service) StartBackgroundJobs(ctx context.Context) {
	go s.trafficLoop(ctx)
}

func (s *Service) trafficLoop(ctx context.Context) {
	ticker := time.NewTicker(trafficPollInterval)
	defer ticker.Stop()
	s.reconcileTraffic(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileTraffic(context.Background())
		}
	}
}

func (s *Service) reconcileTraffic(ctx context.Context) {
	list, err := s.store.ListActiveInstances(ctx)
	if err != nil {
		s.logger.Warn("traffic list instances", "err", err)
		return
	}
	now := time.Now().UTC()
	period := CurrentTrafficPeriod(now)
	for i := range list {
		inst := &list[i]
		if inst.Status != models.StatusRunning && inst.Status != models.StatusStopped {
			continue
		}
		if err := s.reconcileInstanceTraffic(ctx, inst, period); err != nil {
			s.logger.Warn("traffic reconcile", "name", inst.Name, "incus", inst.IncusName, "err", err)
		}
	}
	// Cheap retention trim once per reconcile cycle.
	s.purgeOldDailyUsage(ctx)
}

func (s *Service) reconcileInstanceTraffic(ctx context.Context, inst *models.Instance, period string) error {
	used := inst.TrafficUsedBytes
	snap := inst.TrafficCounterSnap
	throttled := inst.TrafficThrottled
	periodChanged := inst.TrafficPeriod != period
	changed := false

	if periodChanged {
		// New UTC month (or first assign): reset usage; re-baseline snap below.
		used = 0
		throttled = false
		changed = true
	}

	if s.incus.IsRunning(inst.IncusName) {
		total, err := s.incus.NetworkBytesTotal(inst.IncusName)
		if err != nil {
			return err
		}
		if periodChanged {
			// Do not charge lifetime Incus counters into the new period.
			snap = total
		} else {
			newUsed, newSnap := AccumulateTraffic(used, snap, total)
			if newUsed != used || newSnap != snap {
				used, snap = newUsed, newSnap
				changed = true
			}
		}
	} else if periodChanged {
		snap = 0
	}

	shouldThrottle := TrafficExceeded(used, inst.TrafficMonthlyGB)
	if shouldThrottle != throttled {
		throttled = shouldThrottle
		changed = true
		if err := s.incus.ApplyNetworkPolicy(inst.IncusName, inst.BandwidthMbps, throttled); err != nil {
			s.logger.Warn("apply traffic throttle", "name", inst.Name, "throttled", throttled, "err", err)
		} else if throttled {
			s.logger.Info("monthly traffic exceeded; throttling NIC",
				"name", inst.Name, "used_bytes", used, "quota_gb", inst.TrafficMonthlyGB)
		} else {
			s.logger.Info("monthly traffic period ok; restoring NIC bandwidth",
				"name", inst.Name, "bandwidth_mbps", inst.BandwidthMbps)
		}
	}

	if changed {
		if err := s.store.UpdateTrafficAccounting(ctx, inst.ID, used, snap, period, throttled); err != nil {
			return err
		}
		inst.TrafficUsedBytes = used
		inst.TrafficCounterSnap = snap
		inst.TrafficPeriod = period
		inst.TrafficThrottled = throttled
	}

	// Persist today's disk snapshot + daily bandwidth for charts.
	if err := s.recordDailyUsage(ctx, inst, used); err != nil {
		s.logger.Warn("daily usage sample", "name", inst.Name, "err", err)
	}
	return nil
}

// CurrentTrafficPeriod returns the UTC calendar month key YYYY-MM.
func CurrentTrafficPeriod(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// AccumulateTraffic adds the delta between counter snapshots into used bytes.
// When Incus counters reset (stop/start), current < last and the new total is counted as delta.
func AccumulateTraffic(used, last, current int64) (newUsed, newLast int64) {
	if current < 0 {
		current = 0
	}
	if last < 0 {
		last = 0
	}
	if used < 0 {
		used = 0
	}
	var delta int64
	if current >= last {
		delta = current - last
	} else {
		delta = current
	}
	return used + delta, current
}

// TrafficExceeded reports whether used bytes meet/exceed the monthly GiB quota.
// monthlyGB <= 0 means unlimited.
func TrafficExceeded(usedBytes int64, monthlyGB int) bool {
	if monthlyGB <= 0 || usedBytes <= 0 {
		return false
	}
	limit := int64(monthlyGB) * 1024 * 1024 * 1024
	return usedBytes >= limit
}
