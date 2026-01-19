-- Drop triggers
DROP TRIGGER IF EXISTS update_documents_updated_at ON documents;
DROP TRIGGER IF EXISTS update_tenants_updated_at ON tenants;
DROP FUNCTION IF EXISTS update_updated_at_column();

-- Drop tables in reverse order of creation
DROP TABLE IF EXISTS crawled_pages;
DROP TABLE IF EXISTS crawl_jobs;
DROP TABLE IF EXISTS document_chunks;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS tenants;
