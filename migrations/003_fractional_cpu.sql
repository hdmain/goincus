-- Allow fractional CPU (e.g. 0.5) via CFS quota on Incus.
ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_cpu_cores_check;
ALTER TABLE instances
  ALTER COLUMN cpu_cores TYPE NUMERIC(8,2)
  USING cpu_cores::numeric;
ALTER TABLE instances
  ADD CONSTRAINT instances_cpu_cores_check CHECK (cpu_cores >= 0.10);
