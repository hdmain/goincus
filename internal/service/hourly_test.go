package service

import (
	"testing"
	"time"
)

func TestComputeCPUPercent(t *testing.T) {
	// 0.5s of CPU time over 1s wall on 1 core => 50%
	elapsed := time.Second
	prev := int64(0)
	cur := int64(500 * time.Millisecond)
	got := computeCPUPercent(prev, cur, elapsed, 1)
	if got < 49.9 || got > 50.1 {
		t.Fatalf("got %v, want ~50", got)
	}
	// half-core plan: 0.25s CPU over 1s on 0.5 cores => 50% of allocation
	got = computeCPUPercent(0, int64(250*time.Millisecond), elapsed, 0.5)
	if got < 49.9 || got > 50.1 {
		t.Fatalf("half-core got %v, want ~50", got)
	}
}
