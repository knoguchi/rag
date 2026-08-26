#!/bin/bash
# setup-demo.sh - Set up the complete RAG demo environment
#
# This script:
# 1. Starts infrastructure (PostgreSQL, Qdrant, Ollama)
# 2. Builds and starts RAG and Crawler services
# 3. Creates the Demo Cloud test tenant
# 4. Starts the sample website
# 5. Crawls and ingests the documentation
# 6. Runs sample queries

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
SERVER_DIR="$PROJECT_DIR/server"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Fixed tenant ID for Demo Cloud
TENANT_ID="00000000-0000-0000-0000-000000000001"
RAG_URL="http://localhost:8080"
SAMPLE_SITE_PORT="8000"
DATABASE_URL="${DATABASE_URL:-postgres://rag:rag@localhost:5432/rag?sslmode=disable}"

# Admin key gates tenant management; generate one for the demo if unset
if [ -z "$ADMIN_API_KEY" ]; then
    ADMIN_API_KEY="demo-admin-$(head -c16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    export ADMIN_API_KEY
fi
TENANT_API_KEY=""  # captured from tenant creation

log() {
    echo -e "${BLUE}[DEMO]${NC} $1"
}

success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

check_prereqs() {
    log "Checking prerequisites..."

    command -v docker >/dev/null 2>&1 || error "Docker is required but not installed"
    command -v docker-compose >/dev/null 2>&1 || error "Docker Compose is required but not installed"
    command -v go >/dev/null 2>&1 || error "Go is required but not installed"
    command -v node >/dev/null 2>&1 || error "Node.js is required but not installed"
    command -v jq >/dev/null 2>&1 || error "jq is required but not installed"
    command -v migrate >/dev/null 2>&1 || error "golang-migrate is required (brew install golang-migrate)"

    success "Prerequisites OK"
}

start_infrastructure() {
    log "Starting infrastructure (PostgreSQL, Qdrant)..."
    cd "$PROJECT_DIR"

    docker-compose -f deployments/docker-compose.dev.yml up -d postgres qdrant

    # Wait for services
    log "Waiting for services to be ready..."
    sleep 5

    # Check PostgreSQL
    until docker-compose -f deployments/docker-compose.dev.yml exec -T postgres pg_isready -U rag >/dev/null 2>&1; do
        log "Waiting for PostgreSQL..."
        sleep 2
    done
    success "PostgreSQL ready"

    # Check Qdrant
    until curl -s http://localhost:6333/collections >/dev/null 2>&1; do
        log "Waiting for Qdrant..."
        sleep 2
    done
    success "Qdrant ready"

    # Check if Ollama is running (should be started natively on Mac for GPU)
    log "Checking Ollama..."
    if ! curl -s http://localhost:11434/api/tags >/dev/null 2>&1; then
        warn "Ollama not running. Starting it..."
        if command -v ollama >/dev/null 2>&1; then
            ollama serve >/dev/null 2>&1 &
            sleep 3
        else
            error "Ollama not installed. Install with: brew install ollama"
        fi
    fi

    # Verify Ollama is ready
    until curl -s http://localhost:11434/api/tags >/dev/null 2>&1; do
        log "Waiting for Ollama..."
        sleep 2
    done
    success "Ollama ready"

    # Pull required models
    log "Pulling Ollama models (this may take a while on first run)..."
    ollama pull nomic-embed-text >/dev/null 2>&1 || warn "nomic-embed-text may already exist"
    ollama pull llama3.2 >/dev/null 2>&1 || warn "llama3.2 may already exist"
    success "Ollama models ready"
}

build_services() {
    log "Building services..."
    cd "$SERVER_DIR"

    make generate 2>/dev/null || true
    go build -o bin/ragd ./cmd/ragd

    success "Services built"
}

run_migrations() {
    log "Running database migrations..."
    cd "$SERVER_DIR"
    migrate -path internal/repository/postgres/migrations -database "$DATABASE_URL" up
    success "Migrations applied"
}

start_services() {
    log "Starting RAG service..."
    cd "$SERVER_DIR"

    # Kill existing services if running
    pkill -f "bin/ragd" 2>/dev/null || true
    sleep 2

    # Start RAG service in background (inherits ADMIN_API_KEY)
    ./bin/ragd > /tmp/ragd.log 2>&1 &
    RAG_PID=$!

    # Wait for services to be ready
    log "Waiting for services to start..."
    sleep 3

    until curl -s "$RAG_URL/healthz" >/dev/null 2>&1; do
        log "Waiting for RAG service..."
        sleep 2
    done
    success "RAG service ready (PID: $RAG_PID)"
}

cleanup_existing_data() {
    log "Cleaning up existing demo data..."

    # Delete tenant if it exists (this removes all documents and vectors)
    existing=$(curl -s -H "X-API-Key: $ADMIN_API_KEY" "$RAG_URL/v1/tenants/$TENANT_ID" 2>&1)
    if echo "$existing" | jq -e '.id' >/dev/null 2>&1; then
        log "Deleting existing tenant and all its data..."
        curl -s -X DELETE -H "X-API-Key: $ADMIN_API_KEY" "$RAG_URL/v1/tenants/$TENANT_ID" >/dev/null 2>&1
        sleep 2
        success "Existing data cleaned up"
    else
        log "No existing demo data found"
    fi
}

create_tenant() {
    log "Creating Demo Cloud tenant..."

    # Create tenant (always fresh since we cleaned up first). The API key
    # is returned exactly once, in this response.
    result=$(curl -s -X POST "$RAG_URL/v1/tenants" \
        -H "Content-Type: application/json" \
        -H "X-API-Key: $ADMIN_API_KEY" \
        -d "{
            \"name\": \"Demo Cloud\",
            \"id\": \"$TENANT_ID\"
        }")

    TENANT_API_KEY=$(echo "$result" | jq -r '.api_key // empty')
    if [ -n "$TENANT_API_KEY" ]; then
        success "Tenant created: $TENANT_ID"
    else
        error "Failed to create tenant: $result"
    fi
}

build_client_sdk() {
    log "Building client SDK..."
    cd "$PROJECT_DIR/client-sdk"

    if [ ! -d "node_modules" ]; then
        npm install >/dev/null 2>&1
    fi

    npm run build >/dev/null 2>&1

    # Copy browser bundle to demo-site for easy serving
    cp dist/rag-sdk.browser.js "$SCRIPT_DIR/"

    # Widget config with the tenant API key; loaded by every demo page
    printf "window.DEMO_RAG_CONFIG = {\n  apiKey: '%s',\n  baseUrl: '%s'\n};\n" \
        "$TENANT_API_KEY" "$RAG_URL" > "$SCRIPT_DIR/demo-config.js"
    cp "$SCRIPT_DIR/demo-config.js" "$SCRIPT_DIR/docs/demo-config.js"
    success "Client SDK built"
}

start_sample_site() {
    log "Starting sample website..."
    cd "$SCRIPT_DIR"

    # Kill existing server if running
    pkill -f "python.*http.server.*$SAMPLE_SITE_PORT" 2>/dev/null || true
    sleep 1

    # Start server in demo-site directory
    python3 -m http.server "$SAMPLE_SITE_PORT" > /tmp/sample-site.log 2>&1 &
    SITE_PID=$!

    sleep 2

    if curl -s "http://localhost:$SAMPLE_SITE_PORT" >/dev/null 2>&1; then
        success "Sample site running at http://localhost:$SAMPLE_SITE_PORT (PID: $SITE_PID)"
    else
        error "Failed to start sample site"
    fi
}

setup_crawler() {
    log "Setting up crawler..."
    cd "$PROJECT_DIR/crawler"

    if [ ! -d "node_modules" ]; then
        npm install >/dev/null 2>&1
    fi

    # Check if Playwright browsers are installed
    if ! npx playwright install chromium --dry-run >/dev/null 2>&1; then
        log "Installing Playwright Chromium..."
        npx playwright install chromium >/dev/null 2>&1
    fi

    success "Crawler ready"
}

run_crawler() {
    log "Crawling and ingesting sample site documentation..."
    cd "$PROJECT_DIR/crawler"

    node crawl.js \
        --api-key "$TENANT_API_KEY" \
        --url "http://localhost:$SAMPLE_SITE_PORT" \
        --include "/docs/**" \
        --include "/pricing.html" \
        --include "/about.html" \
        --max-pages 15 \
        --delay 500

    success "Documentation ingested"
}

verify_ingestion() {
    log "Verifying ingestion..."

    response=$(curl -s -H "X-API-Key: $TENANT_API_KEY" "$RAG_URL/v1/documents")
    doc_count=$(echo "$response" | jq '.documents | length')

    success "Ingested $doc_count documents"
}

run_sample_queries() {
    log "Running sample queries..."
    echo ""

    queries=(
        "How do I create a PostgreSQL database?"
        "What compute instance types are available?"
        "How much does storage cost per GB?"
    )

    for query in "${queries[@]}"; do
        echo -e "${BLUE}Query:${NC} $query"
        echo ""

        response=$(curl -s -X POST "$RAG_URL/v1/query" \
            -H "Content-Type: application/json" \
            -H "X-API-Key: $TENANT_API_KEY" \
            -d "{\"query\": \"$query\"}")

        if command -v jq >/dev/null 2>&1; then
            answer=$(echo "$response" | jq -r '.answer // .error // "No response"')
            echo -e "${GREEN}Answer:${NC}"
            echo "$answer" | head -20
        else
            echo "$response" | head -500
        fi

        echo ""
        echo "---"
        echo ""
    done
}

print_summary() {
    echo ""
    echo "=============================================="
    echo -e "${GREEN}Demo Setup Complete!${NC}"
    echo "=============================================="
    echo ""
    echo "Services running:"
    echo "  - RAG Service:     http://localhost:8080"
    echo "  - Sample Site:     http://localhost:$SAMPLE_SITE_PORT/"
    echo ""
    echo "Tenant ID:      $TENANT_ID"
    echo "Tenant API key: $TENANT_API_KEY  (shown once; store it securely)"
    echo "Admin API key:  $ADMIN_API_KEY"
    echo ""
    echo "Try these commands:"
    echo ""
    echo "  # Query the RAG service"
    echo "  curl -X POST $RAG_URL/v1/query \\"
    echo "    -H 'Content-Type: application/json' \\"
    echo "    -H 'X-API-Key: $TENANT_API_KEY' \\"
    echo "    -d '{\"query\": \"How do I create a database?\"}' | jq"
    echo ""
    echo "  # List documents"
    echo "  curl -H 'X-API-Key: $TENANT_API_KEY' '$RAG_URL/v1/documents' | jq"
    echo ""
    echo "  # View logs"
    echo "  tail -f /tmp/ragd.log"
    echo ""
    echo "To stop everything:"
    echo "  pkill -f ragd; pkill -f 'python.*http.server'"
    echo "  docker-compose -f deployments/docker-compose.dev.yml down"
    echo ""
}

# Main
main() {
    echo ""
    echo "=============================================="
    echo "       RAG Service Demo Setup"
    echo "=============================================="
    echo ""

    check_prereqs
    start_infrastructure
    build_services
    run_migrations
    start_services
    cleanup_existing_data
    create_tenant
    build_client_sdk
    start_sample_site
    setup_crawler
    run_crawler
    verify_ingestion
    run_sample_queries
    print_summary
}

main "$@"
