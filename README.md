Note: this repo is a proof of concept. Further development is required to be production worthy.

# RAG as a Service

[![CI](https://github.com/knoguchi/rag/actions/workflows/ci.yml/badge.svg)](https://github.com/knoguchi/rag/actions/workflows/ci.yml)

Add an AI chat widget to any website. Multi-tenant RAG (Retrieval-Augmented Generation) system with a Go backend and TypeScript client SDK.

## Features

**Backend**
- Multi-tenant with isolated vector collections and config per tenant
- Local LLM via Ollama (`llama3.2` + `nomic-embed-text`)
- Semantic chunking that preserves code blocks and heading hierarchy
- Streaming responses via Server-Sent Events (SSE)
- gRPC and REST APIs via grpc-gateway

**Client SDK**
- Drop-in chat widget for any website (JAMstack friendly)
- TypeScript API client for custom integrations
- Browser bundle for `<script>` tag usage
- Shadow DOM to avoid CSS conflicts with host pages

**Crawler**
- Playwright for JavaScript-rendered pages
- HTML-to-Markdown conversion via Turndown
- URL include/exclude patterns

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for technical details.

## Architecture

```mermaid
graph LR
    A[Chat Widget] <--> B[RAG Service]
    B --> C[Ollama]
    B --> D[Qdrant]
    B --> E[PostgreSQL]
```

## Prerequisites

```bash
# macOS
brew install go buf golang-migrate ollama node

# Start Ollama and pull models
ollama serve &
ollama pull nomic-embed-text
ollama pull llama3.2
```

## Demo

For a complete working demo with sample data:

```bash
cd demo-site
./setup.sh
```

This script will:
- Start infrastructure (PostgreSQL, Qdrant, Ollama)
- Build and run the RAG service
- Create the default tenant (`00000000-0000-0000-0000-000000000001`)
- Start the sample Demo Cloud documentation site
- Crawl and ingest all documentation

---

## Quick Start

### 1. Start Backend

```bash
# Start infrastructure (Postgres, Qdrant)
docker-compose -f deployments/docker-compose.dev.yml up -d

# Run migrations and start server
cd server
make migrate-up
make run
```

### 2. Build Client SDK

```bash
cd client-sdk
npm install
npm run build
```

### 3. Add Chat Widget to Your Site

```html
<script src="path/to/rag-sdk.browser.js"></script>
<script>
  new ChatWidget({
    tenantId: 'your-tenant-id',
    baseUrl: 'http://localhost:8080'
  });
</script>
```

### 4. Create a Tenant and Ingest Documents

1. **Create a tenant** via API:
   ```bash
   curl -X POST http://localhost:8080/v1/tenants \
     -H "Content-Type: application/json" \
     -d '{"name": "My Tenant"}'
   ```

2. **Run the crawler** to ingest documents:
   ```bash
   cd crawler
   npm install
   npx playwright install chromium

   node crawl.js \
     --tenant-id YOUR_TENANT_ID \
     --url https://your-docs-site.com \
     --max-pages 50
   ```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/tenants` | POST | Create tenant |
| `/v1/tenants/:id` | GET | Get tenant |
| `/v1/documents/ingest` | POST | Ingest document |
| `/v1/documents/ingest-url` | POST | Ingest from URL |
| `/v1/query` | POST | Query (non-streaming) |
| `/v1/query/stream` | POST | Query (streaming SSE) |

## Development

```bash
# Backend (from server/)
make generate  # Regenerate proto
make build     # Build binary
make test      # Run tests
make run       # Run RAG service

# Client SDK (from client-sdk/)
npm run build  # Build ES module and browser bundle
npm run dev    # Watch mode (TypeScript only)
```

## Tech Stack

**Backend:** Go, PostgreSQL, Qdrant, Ollama, gRPC/REST

**Client SDK:** TypeScript, esbuild

## License

Apache 2.0 - See [LICENSE](LICENSE) for details.
