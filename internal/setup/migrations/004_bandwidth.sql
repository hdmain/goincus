-- Per-VPS NIC bandwidth (Mbit/s both directions via Incus limits.max). 0 = unlimited.
ALTER TABLE instances
  ADD COLUMN IF NOT EXISTS bandwidth_mbps INTEGER NOT NULL DEFAULT 100
  CHECK (bandwidth_mbps >= 0);
