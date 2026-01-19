package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/knoguchi/rag/internal/config"
	"github.com/knoguchi/rag/internal/embedder"
	"github.com/knoguchi/rag/internal/llm"
	"github.com/knoguchi/rag/internal/repository"
	"github.com/knoguchi/rag/internal/repository/postgres"
	"github.com/knoguchi/rag/internal/server"
	"github.com/knoguchi/rag/internal/service"
	"github.com/knoguchi/rag/internal/vectorstore"
)

func main() {
	// Set up structured logging
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("failed to run server", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	slog.Info("starting RAG service",
		"grpc_port", cfg.GRPCPort,
		"http_port", cfg.HTTPPort,
		"environment", cfg.Environment,
	)

	// Initialize PostgreSQL
	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()
	slog.Info("connected to PostgreSQL")

	// Initialize repositories
	tenantRepo := postgres.NewTenantRepo(db)
	documentRepo := postgres.NewDocumentRepo(db)

	// Initialize Qdrant vector store
	vectorStore, err := vectorstore.NewQdrantStore(ctx, cfg.QdrantGRPCURL)
	if err != nil {
		return fmt.Errorf("failed to connect to Qdrant: %w", err)
	}
	defer vectorStore.Close()
	slog.Info("connected to Qdrant")

	// Initialize Ollama embedder
	embed := embedder.NewOllamaEmbedder(embedder.OllamaConfig{
		BaseURL: cfg.OllamaURL,
		Model:   cfg.OllamaEmbeddingModel,
	})
	slog.Info("initialized Ollama embedder", "model", cfg.OllamaEmbeddingModel)

	// Initialize Ollama LLM
	llmClient := llm.NewOllamaClient(
		llm.WithBaseURL(cfg.OllamaURL),
		llm.WithModel(cfg.OllamaLLMModel),
	)
	slog.Info("initialized Ollama LLM", "model", cfg.OllamaLLMModel)

	// Initialize services
	tenantSvc := service.NewTenantService(tenantRepo, vectorStore, cfg)
	documentSvc := service.NewDocumentService(documentRepo, tenantRepo, embed, vectorStore)
	ragSvc := service.NewRAGService(tenantRepo, documentRepo, embed, vectorStore, llmClient)

	// Create gRPC server
	grpcServer, err := server.NewGRPCServer(server.GRPCServerConfig{
		Port:   cfg.GRPCPort,
		Logger: slog.Default(),
	}, server.Services{
		TenantService:   tenantSvc,
		DocumentService: documentSvc,
		RAGService:      ragSvc,
	})
	if err != nil {
		return fmt.Errorf("failed to create gRPC server: %w", err)
	}

	// Create HTTP server with grpc-gateway
	httpServer, err := server.NewHTTPServer(server.HTTPServerConfig{
		Port:           cfg.HTTPPort,
		GRPCAddr:       fmt.Sprintf("localhost:%d", cfg.GRPCPort),
		Logger:         slog.Default(),
		AllowedOrigins: []string{"*"}, // Configure in production
	})
	if err != nil {
		return fmt.Errorf("failed to create HTTP server: %w", err)
	}

	// Start servers
	errCh := make(chan error, 2)

	go func() {
		slog.Info("starting gRPC server", "port", cfg.GRPCPort)
		if err := grpcServer.Start(); err != nil {
			errCh <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	go func() {
		// Wait a bit for gRPC server to start before connecting gateway
		if err := httpServer.RegisterHandlers(ctx); err != nil {
			errCh <- fmt.Errorf("failed to register HTTP handlers: %w", err)
			return
		}
		slog.Info("starting HTTP server", "port", cfg.HTTPPort)
		if err := httpServer.Start(); err != nil {
			errCh <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		slog.Info("received shutdown signal", "signal", sig)
	}

	// Graceful shutdown
	slog.Info("shutting down servers...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("failed to shutdown HTTP server", "error", err)
	}
	if err := grpcServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("failed to shutdown gRPC server", "error", err)
	}

	slog.Info("servers stopped")
	return nil
}

// Ensure interfaces are satisfied at compile time
var (
	_ repository.TenantRepository   = (*postgres.TenantRepo)(nil)
	_ repository.DocumentRepository = (*postgres.DocumentRepo)(nil)
	_ vectorstore.VectorStore       = (*vectorstore.QdrantStore)(nil)
	_ embedder.Embedder             = (*embedder.OllamaEmbedder)(nil)
	_ llm.LLM                       = (*llm.OllamaClient)(nil)
)
