package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/knoguchi/rag/internal/auth"
	"github.com/knoguchi/rag/internal/config"
	"github.com/knoguchi/rag/internal/ragcore"
	"github.com/knoguchi/rag/internal/ragcore/embedder"
	"github.com/knoguchi/rag/internal/ragcore/llm"
	"github.com/knoguchi/rag/internal/ragcore/reranker"
	"github.com/knoguchi/rag/internal/ragcore/sparse"
	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
	"github.com/knoguchi/rag/internal/repository"
	"github.com/knoguchi/rag/internal/repository/postgres"
	"github.com/knoguchi/rag/internal/server"
	"github.com/knoguchi/rag/internal/service"
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

	// Validate configuration (rejects insecure defaults in non-development)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
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

	// Initialize embedder and LLM for the configured provider
	var embed embedder.Embedder
	var llmClient llm.LLM
	switch cfg.LLMProvider {
	case "openai":
		// OpenAI-compatible servers: llama.cpp llama-server, vLLM, ...
		embed = embedder.NewOpenAIEmbedder(embedder.OpenAIEmbedderConfig{
			BaseURL:   cfg.EmbeddingBaseURL,
			Model:     cfg.OllamaEmbeddingModel,
			APIKey:    cfg.LLMAPIKey,
			Dimension: cfg.EmbeddingDimension,
		})
		llmClient = llm.NewOpenAIClient(
			llm.WithOpenAIBaseURL(cfg.LLMBaseURL),
			llm.WithOpenAIModel(cfg.OllamaLLMModel),
			llm.WithOpenAIAPIKey(cfg.LLMAPIKey),
		)
		slog.Info("initialized OpenAI-compatible provider",
			"llm_url", cfg.LLMBaseURL, "embedding_url", cfg.EmbeddingBaseURL,
			"llm_model", cfg.OllamaLLMModel, "embedding_model", cfg.OllamaEmbeddingModel)
	default:
		embed = embedder.NewOllamaEmbedder(embedder.OllamaConfig{
			BaseURL: cfg.OllamaURL,
			Model:   cfg.OllamaEmbeddingModel,
		})
		llmClient = llm.NewOllamaClient(
			llm.WithBaseURL(cfg.OllamaURL),
			llm.WithModel(cfg.OllamaLLMModel),
		)
		slog.Info("initialized Ollama provider",
			"llm_model", cfg.OllamaLLMModel, "embedding_model", cfg.OllamaEmbeddingModel)
	}

	// Initialize auth interceptor
	authInterceptor := auth.NewAPIKeyInterceptor(tenantRepo, cfg.AdminAPIKey)

	// Assemble the RAG engine: BM25 sparse vectors for hybrid search,
	// LLM reranking, conversation-aware query rewriting, and contextual
	// ingestion (the last three gated per tenant / per config)
	engineOpts := []ragcore.Option{
		ragcore.WithSparseVectorizer(sparse.New()),
		ragcore.WithReranker(reranker.NewLLMReranker(llmClient, reranker.WithModel(cfg.OllamaLLMModel))),
		ragcore.WithContextualConcurrency(cfg.ContextualConcurrency),
	}
	if cfg.QueryRewriteEnabled {
		engineOpts = append(engineOpts, ragcore.WithRewriter(ragcore.NewRewriter(llmClient, cfg.QueryRewriteTimeout)))
	}
	engine := ragcore.New(embed, llmClient, vectorStore, engineOpts...)
	defer engine.Close()

	// Reap self-signup tenants whose retention policy has lapsed
	go runRetentionReaper(ctx, tenantRepo, engine, cfg.RetentionSweepInterval)

	// Initialize services
	tenantSvc := service.NewTenantService(tenantRepo, engine, cfg)
	documentSvc := service.NewDocumentService(documentRepo, tenantRepo, engine)
	ragSvc := service.NewRAGService(engine)

	// Create gRPC server
	grpcServer, err := server.NewGRPCServer(server.GRPCServerConfig{
		Port:            cfg.GRPCPort,
		Logger:          slog.Default(),
		AuthInterceptor: authInterceptor,
		Environment:     cfg.Environment,
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
		AllowedOrigins: cfg.CORSAllowedOrigins,
		DBChecker:      db,
		RateLimitRPS:   cfg.RateLimitRPS,
		RateLimitBurst: cfg.RateLimitBurst,
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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Wait for in-flight document processing to complete
	if err := documentSvc.Shutdown(shutdownCtx); err != nil {
		slog.Warn("document processing shutdown timeout", "error", err)
	}

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("failed to shutdown HTTP server", "error", err)
	}
	if err := grpcServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("failed to shutdown gRPC server", "error", err)
	}

	slog.Info("servers stopped")
	return nil
}

// runRetentionReaper periodically deletes tenants (rows cascade to their
// documents and chunks) and their vector collections once they have been
// idle longer than their configured retention. Only tenants with a non-zero
// retention_days config — i.e. self-signup installs — ever expire.
func runRetentionReaper(ctx context.Context, tenantRepo repository.TenantRepository, engine *ragcore.Engine, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		expired, err := tenantRepo.ListExpired(ctx)
		if err != nil {
			slog.Warn("retention sweep failed", "error", err)
			continue
		}
		for _, tenant := range expired {
			slog.Info("reaping expired tenant",
				"tenant_id", tenant.ID, "name", tenant.Name,
				"retention_days", tenant.Config.RetentionDays)
			if err := engine.DeleteNamespace(ctx, tenant.ID.UUIDString()); err != nil {
				slog.Warn("failed to delete expired tenant's vectors", "error", err, "tenant_id", tenant.ID)
			}
			if err := tenantRepo.Delete(ctx, tenant.ID); err != nil {
				slog.Warn("failed to delete expired tenant", "error", err, "tenant_id", tenant.ID)
			}
		}
	}
}

// Ensure interfaces are satisfied at compile time
var (
	_ repository.TenantRepository   = (*postgres.TenantRepo)(nil)
	_ repository.DocumentRepository = (*postgres.DocumentRepo)(nil)
	_ vectorstore.VectorStore       = (*vectorstore.QdrantStore)(nil)
	_ embedder.Embedder             = (*embedder.OllamaEmbedder)(nil)
	_ llm.LLM                       = (*llm.OllamaClient)(nil)
)
