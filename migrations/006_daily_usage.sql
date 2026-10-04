-- Daily disk + bandwidth samples for usage charts (UTC calendar days).
CREATE TABLE IF NOT EXISTS instance_daily_usage (
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    disk_used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (disk_used_bytes >= 0),
    disk_total_bytes BIGINT NOT NULL DEFAULT 0 CHECK (disk_total_bytes >= 0),
    -- Bytes transferred during this UTC day (rx+tx), derived from monthly traffic counters.
    bandwidth_bytes BIGINT NOT NULL DEFAULT 0 CHECK (bandwidth_bytes >= 0),
    -- Monthly traffic_used_bytes at first sample of this day (internal).
    bandwidth_baseline_bytes BIGINT NOT NULL DEFAULT 0 CHECK (bandwidth_baseline_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, day)
);

CREATE INDEX IF NOT EXISTS idx_instance_daily_usage_day ON instance_daily_usage(day);
