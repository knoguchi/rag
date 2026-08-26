# Changelog

## v2.0.0 — 2026-08-26

Major modernization: tenant-agnostic RAG core, hybrid retrieval, and a
breaking authentication overhaul.

### Breaking changes

- **`tenant_id` removed from all request messages** (`QueryRequest`,
  `RetrieveRequest`, `IngestDocumentRequest`, `IngestURLRequest`,
  `ListDocumentsRequest`). The server derives the tenant from the
  authenticated API key; cross-tenant requests are impossible by
  construction.
- **API keys are stored hashed** (SHA-256 + display prefix). Migration
  `002_auth_scoping` hashes existing keys in place — existing keys keep
  working, but the plaintext is unrecoverable from the database. The full
  key is returned exactly once: in the new `CreateTenantResponse` or from
  `RegenerateAPIKey`. `Tenant` messages expose `key_prefix` instead of
  `api_key`.
- **Client SDK 2.0**: `apiKey` is required and `tenantId` is gone. Pass
  `{ sessionId }` in query options to enable server-side conversation
  memory and follow-up rewriting. The stream parser now understands the
  gateway's `{"result": ...}` NDJSON framing.
- **Crawler**: `--tenant-id` replaced by `--api-key` (or `RAG_API_KEY`).
- `GetTenant`/`UpdateTenant` require the tenant's own key or the admin key;
  tenant management (create/list/delete/regenerate) is admin-key only.
- Removed unused `JWT_SECRET`/`JWT_EXPIRY`/`SESSION_SECRET` config.

### Migration

1. Run migration 002 (`make migrate-up`).
2. Update SDK/crawler/scripts to send `X-API-Key` and drop `tenant_id`
   fields (see README examples).
3. Pre-existing tenants have dense-only vector collections. Rebuild them as
   hybrid from the chunks already in Postgres (no re-crawl):
   `cd server && go run ./cmd/ragreindex --tenant <uuid>` (or `--all`).
   New tenants get hybrid collections automatically.
4. Anyone who lost an API key: admin `POST /v1/tenants/{id}/regenerate-key`.

### Added

- **Hybrid retrieval**: BM25 sparse vectors (server-side IDF) fused with
  dense embeddings via RRF in Qdrant. Fixes a latent bug where cosine
  thresholds were applied to rank-scale RRF scores, filtering all results.
- **Conversation-aware query rewriting**: follow-up questions are condensed
  with history into standalone search queries before retrieval.
- **LLM reranking** wired into the pipeline (per-tenant `reranker_enabled`).
- **Contextual retrieval** (per-tenant `contextual_retrieval_enabled`):
  LLM-generated situating context embedded with each chunk at ingestion.
- **OpenAI-compatible provider** (`LLM_PROVIDER=openai`): works with
  llama.cpp `llama-server`, vLLM, etc., with separate generation and
  embedding base URLs.
- `ragreindex` CLI for migrating/rebuilding tenant collections.
- Tenant-agnostic engine extracted to `server/internal/ragcore`.

### Fixed

- HTTP gateway now forwards `X-API-Key` into gRPC metadata (REST
  authentication previously could not work at all).
- CORS no longer reflects arbitrary origins with credentials; wildcard
  configurations answer `*` without credentials.
- All single-document operations are tenant-scoped in SQL
  (`document_chunks.tenant_id`); foreign documents return 404 with no
  existence oracle.
- Conversation memory keys are namespaced per tenant (no cross-tenant
  session collisions); the memory store's cleanup goroutine is stoppable.
- Dense search works against hybrid collections (named-vector fallback).
- Embedder resolves dimensions from the model table instead of a hardcoded
  768; HTTP clients have timeouts.
