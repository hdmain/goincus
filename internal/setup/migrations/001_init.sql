-- goincus schema
CREATE TABLE IF NOT EXISTS instances (
    id            UUID PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    incus_name    TEXT NOT NULL UNIQUE,
    image         TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    cpu_cores     INTEGER NOT NULL CHECK (cpu_cores >= 1),
    memory_mb     INTEGER NOT NULL CHECK (memory_mb >= 64),
    storage_gb    INTEGER NOT NULL CHECK (storage_gb >= 1),
    processes     INTEGER NOT NULL DEFAULT 512 CHECK (processes >= 1),
    error_message TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS port_mappings (
    id            UUID PRIMARY KEY,
    instance_id   UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    protocol      TEXT NOT NULL DEFAULT 'tcp' CHECK (protocol IN ('tcp', 'udp')),
    host_port     INTEGER NOT NULL CHECK (host_port > 0 AND host_port <= 65535),
    internal_port INTEGER NOT NULL CHECK (internal_port > 0 AND internal_port <= 65535),
    device_name   TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (host_port, protocol),
    UNIQUE (instance_id, device_name)
);

CREATE INDEX IF NOT EXISTS idx_instances_status ON instances(status);
CREATE INDEX IF NOT EXISTS idx_port_mappings_instance ON port_mappings(instance_id);
CREATE INDEX IF NOT EXISTS idx_port_mappings_host_port ON port_mappings(host_port);
