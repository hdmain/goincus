-- Monthly transfer quota (GiB). 0 = unlimited.
-- Used bytes accumulate from Incus eth0 counters with counter-reset handling.
ALTER TABLE instances
  ADD COLUMN IF NOT EXISTS traffic_monthly_gb INTEGER NOT NULL DEFAULT 1024
    CHECK (traffic_monthly_gb >= 0);

ALTER TABLE instances
  ADD COLUMN IF NOT EXISTS traffic_used_bytes BIGINT NOT NULL DEFAULT 0
    CHECK (traffic_used_bytes >= 0);

ALTER TABLE instances
  ADD COLUMN IF NOT EXISTS traffic_counter_snap BIGINT NOT NULL DEFAULT 0
    CHECK (traffic_counter_snap >= 0);

ALTER TABLE instances
  ADD COLUMN IF NOT EXISTS traffic_period TEXT NOT NULL DEFAULT '';

ALTER TABLE instances
  ADD COLUMN IF NOT EXISTS traffic_throttled BOOLEAN NOT NULL DEFAULT FALSE;
