DROP INDEX IF EXISTS idx_tenants_last_used_at;
ALTER TABLE tenants DROP COLUMN last_used_at;
