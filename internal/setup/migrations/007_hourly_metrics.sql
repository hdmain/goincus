-- Hourly CPU/RAM/disk/bandwidth samples for all instances (UTC hour buckets).
CREATE TABLE IF NOT EXISTS instance_hourly_metrics (
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    hour TIMESTAMPTZ NOT NULL,
    cpu_percent DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (cpu_percent >= 0),
    memory_used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (memory_used_bytes >= 0),
    memory_total_bytes BIGINT NOT NULL DEFAULT 0 CHECK (memory_total_bytes >= 0),
    disk_used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (disk_used_bytes >= 0),
    disk_total_bytes BIGINT NOT NULL DEFAULT 0 CHECK (disk_total_bytes >= 0),
    bandwidth_bytes BIGINT NOT NULL DEFAULT 0 CHECK (bandwidth_bytes >= 0),
    -- Counters used to compute the next sample's deltas (not exposed by API).
    cpu_usage_ns BIGINT NOT NULL DEFAULT 0 CHECK (cpu_usage_ns >= 0),
    net_bytes_total BIGINT NOT NULL DEFAULT 0 CHECK (net_bytes_total >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, hour)
);

CREATE INDEX IF NOT EXISTS idx_instance_hourly_metrics_hour ON instance_hourly_metrics(hour);
