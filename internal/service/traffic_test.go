package service

import (
	"testing"
	"time"
)

func TestAccumulateTraffic(t *testing.T) {
	used, snap := AccumulateTraffic(100, 50, 80)
	if used != 130 || snap != 80 {
		t.Fatalf("delta = %d/%d, want 130/80", used, snap)
	}
	used, snap = AccumulateTraffic(1000, 900, 200) // counter reset
	if used != 1200 || snap != 200 {
		t.Fatalf("reset = %d/%d, want 1200/200", used, snap)
	}
}

func TestTrafficExceeded(t *testing.T) {
	if TrafficExceeded(100, 0) {
		t.Fatal("unlimited must not exceed")
	}
	oneGiB := int64(1024 * 1024 * 1024)
	if !TrafficExceeded(oneGiB, 1) {
		t.Fatal("exactly 1 GiB must exceed 1 GiB quota")
	}
	if TrafficExceeded(oneGiB-1, 1) {
		t.Fatal("under quota must not exceed")
	}
}

func TestCurrentTrafficPeriod(t *testing.T) {
	ts, err := time.Parse(time.RFC3339, "2026-10-04T22:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if got := CurrentTrafficPeriod(ts); got != "2026-10" {
		t.Fatalf("period = %q", got)
	}
}
