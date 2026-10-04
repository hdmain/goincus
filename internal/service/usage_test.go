package service

import (
	"testing"
	"time"
)

func TestUsageDayTruncateUTC(t *testing.T) {
	ts, err := time.Parse(time.RFC3339, "2026-10-04T22:15:00Z")
	if err != nil {
		t.Fatal(err)
	}
	day := ts.UTC().Truncate(24 * time.Hour)
	if day.Format("2006-01-02") != "2026-10-04" {
		t.Fatalf("day = %s", day.Format("2006-01-02"))
	}
}
