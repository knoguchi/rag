.PHONY: all generate build test run clean lint proto docker-up docker-down help

# Load .env if it exists
-include .env
export

# Default database URL for local development
DATABASE_URL ?= postgres://rag:rag@localhost:5432/rag?sslmode=disable

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOFMT=gofmt

# Binary names
RAG_BINARY=ragd

# Directories
BIN_DIR=bin
GEN_DIR=gen
PROTO_DIR=proto

# Default target
all: generate build

## help: Show this help message
help:
	@echo "RAG as a Service - Available targets:"
	@echo ""
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## generate: Generate Go code from proto files
generate:
	buf generate

## lint-proto: Lint proto files
lint-proto:
	buf lint

## build: Build RAG service
build:
	$(GOBUILD) -o $(BIN_DIR)/$(RAG_BINARY) ./cmd/ragd

## test: Run all tests
test:
	$(GOTEST) -v -race -cover ./...

## test-short: Run tests without integration tests
test-short:
	$(GOTEST) -v -short ./...

## lint: Run Go linters
lint:
	golangci-lint run ./...

## fmt: Format Go code
fmt:
	$(GOFMT) -s -w .

## tidy: Tidy Go modules
tidy:
	$(GOMOD) tidy

## clean: Clean build artifacts
clean:
	rm -rf $(BIN_DIR)
	rm -rf $(GEN_DIR)

## docker-up: Start dev infrastructure (Postgres, Qdrant)
docker-up:
	docker-compose -f ../deployments/docker-compose.dev.yml up -d

## docker-down: Stop dev infrastructure
docker-down:
	docker-compose -f ../deployments/docker-compose.dev.yml down

## docker-logs: View logs from dev infrastructure
docker-logs:
	docker-compose -f ../deployments/docker-compose.dev.yml logs -f

## docker-up-prod: Start all services with docker-compose (production)
docker-up-prod:
	docker-compose -f ../deployments/docker-compose.yml up -d

## run: Run RAG service locally
run:
	$(GOCMD) run ./cmd/ragd

## migrate-up: Run database migrations
migrate-up:
	migrate -path internal/repository/postgres/migrations -database "$(DATABASE_URL)" up

## migrate-down: Rollback database migrations
migrate-down:
	migrate -path internal/repository/postgres/migrations -database "$(DATABASE_URL)" down

## ollama-pull: Pull required Ollama models
ollama-pull:
	ollama pull nomic-embed-text
	ollama pull llama3.2

## deps: Install development dependencies
deps:
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
