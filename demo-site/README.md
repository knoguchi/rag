# RAG Service Demo

This guide walks through setting up and testing the RAG service with a sample tenant website.

## Prerequisites

- Docker and Docker Compose
- Go 1.22+
- Node.js 18+ (for the crawler and client-sdk)
- `jq` (for JSON formatting)

## Quick Start

```bash
# Run the complete demo setup (from repo root)
./demo-site/setup.sh
```

This script will:
1. Start infrastructure (PostgreSQL, Qdrant, Ollama)
2. Build and start the RAG and Crawler services
3. Create the Demo Cloud test tenant
4. Build the client SDK
5. Start the sample website
6. Crawl and ingest the documentation
7. Run sample queries

## Manual Setup

### Step 1: Start Infrastructure

```bash
# Start PostgreSQL and Qdrant
docker-compose -f deployments/docker-compose.dev.yml up -d postgres qdrant

# Wait for services to be ready
sleep 5

# Start Ollama natively (for Metal GPU acceleration on Mac)
# Install if needed: brew install ollama
ollama serve &

# Pull the required Ollama models
ollama pull nomic-embed-text
ollama pull llama3.2
```

### Step 2: Build and Start Services

```bash
cd server

# Build the services
make build

# Start RAG service (in one terminal)
./bin/ragd

# Start Crawler service (in another terminal)
./bin/crawlerd
```

### Step 3: Create Test Tenant

The Demo Cloud tenant uses a fixed UUID for easy reference:

**Tenant ID:** `00000000-0000-0000-0000-000000000001`

```bash
# Create the tenant
curl -X POST http://localhost:8080/v1/tenants \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Demo Cloud",
    "id": "00000000-0000-0000-0000-000000000001"
  }'
```

### Step 4: Build Client SDK and Start Sample Website

```bash
# Build the client SDK
cd client-sdk
npm install
npm run build

# Start the website from repo root (so ../client-sdk paths work)
cd ..
python3 -m http.server 8000
```

Visit http://localhost:8000/demo-site/ to see the sample site.

### Step 5: Crawl and Ingest

```bash
cd crawler
npm install
npx playwright install chromium

# Crawl the documentation
node crawl.js \
  --tenant-id 00000000-0000-0000-0000-000000000001 \
  --url http://localhost:8000/demo-site \
  --include "/demo-site/docs/**" \
  --max-pages 20
```

### Step 6: Query the RAG Service

```bash
TENANT_ID="00000000-0000-0000-0000-000000000001"

# Basic query
curl -X POST http://localhost:8080/v1/query \
  -H "Content-Type: application/json" \
  -d "{
    \"tenant_id\": \"$TENANT_ID\",
    \"query\": \"How do I create a PostgreSQL database?\"
  }" | jq
```

## Sample Queries to Try

| Query | Expected Answer From |
|-------|---------------------|
| "How do I create a PostgreSQL database?" | docs/database.html |
| "What compute instance types are available?" | docs/compute.html |
| "How do I upload files to storage?" | docs/storage.html |
| "What authentication methods are supported?" | docs/authentication.html |
| "How much does a standard-2 instance cost?" | pricing.html, docs/compute.html |

## Cleanup

```bash
# Stop services
pkill -f ragd
pkill -f crawlerd

# Stop sample site
pkill -f "python.*http.server"

# Stop infrastructure
docker-compose -f deployments/docker-compose.dev.yml down

# Remove data (optional)
docker-compose -f deployments/docker-compose.dev.yml down -v
```
