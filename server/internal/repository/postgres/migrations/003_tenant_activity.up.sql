-- Track tenant activity for retention: self-signup tenants idle longer than
-- their configured retention are reaped (documents, vectors, and all).
ALTER TABLE tenants ADD COLUMN last_used_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX idx_tenants_last_used_at ON tenants(last_used_at);
