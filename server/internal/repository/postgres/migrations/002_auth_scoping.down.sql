-- NOTE: plaintext API keys cannot be restored from their hashes. This down
-- migration generates fresh random keys; distribute them via the admin API.
ALTER TABLE tenants ADD COLUMN api_key VARCHAR(64);

UPDATE tenants SET api_key = 'rag_' || md5(random()::text || clock_timestamp()::text);

ALTER TABLE tenants
    ALTER COLUMN api_key SET NOT NULL,
    ADD CONSTRAINT tenants_api_key_key UNIQUE (api_key),
    DROP COLUMN api_key_hash,
    DROP COLUMN key_prefix;

DROP INDEX IF EXISTS idx_document_chunks_tenant_doc;
ALTER TABLE document_chunks DROP COLUMN tenant_id;
