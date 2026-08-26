-- API keys: store only a SHA-256 hash plus a short display prefix.
-- Existing keys keep working (lookups hash the presented key); the plaintext
-- becomes unrecoverable, which is the point. Lost keys => regenerate.
ALTER TABLE tenants
    ADD COLUMN api_key_hash CHAR(64),
    ADD COLUMN key_prefix VARCHAR(12);

UPDATE tenants SET
    api_key_hash = encode(sha256(api_key::bytea), 'hex'),
    key_prefix = left(api_key, 12);

ALTER TABLE tenants
    ALTER COLUMN api_key_hash SET NOT NULL,
    ADD CONSTRAINT tenants_api_key_hash_key UNIQUE (api_key_hash),
    DROP COLUMN api_key;

-- Tenant-scope chunk rows so chunk queries can enforce tenancy in SQL
ALTER TABLE document_chunks
    ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE;

UPDATE document_chunks dc
    SET tenant_id = d.tenant_id
    FROM documents d
    WHERE dc.document_id = d.id;

ALTER TABLE document_chunks ALTER COLUMN tenant_id SET NOT NULL;

CREATE INDEX idx_document_chunks_tenant_doc ON document_chunks(tenant_id, document_id);
