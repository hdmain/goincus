-- add root password for SSH access
ALTER TABLE instances ADD COLUMN IF NOT EXISTS root_password TEXT NOT NULL DEFAULT '';
