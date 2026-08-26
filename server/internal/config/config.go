// Package config loads configuration from environment variables and .env files.
package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

// Config holds all configuration for the RAG service
type Config struct {
	// Server
	GRPCPort    int    `env:"GRPC_PORT" envDefault:"9090"`
	HTTPPort    int    `env:"HTTP_PORT" envDefault:"8080"`
	Environment string `env:"ENVIRONMENT" envDefault:"development"`
	LogLevel    string `env:"LOG_LEVEL" envDefault:"info"`

	// PostgreSQL
	DatabaseURL string `env:"DATABASE_URL" envDefault:"postgres://rag:rag@localhost:5432/rag?sslmode=disable"`

	// Qdrant
	QdrantURL     string `env:"QDRANT_URL" envDefault:"http://localhost:6333"`
	QdrantGRPCURL string `env:"QDRANT_GRPC_URL" envDefault:"localhost:6334"`

	// LLM provider: "ollama" (native Ollama API) or "openai"
	// (OpenAI-compatible API — llama.cpp llama-server, vLLM, etc.)
	LLMProvider string `env:"LLM_PROVIDER" envDefault:"ollama"`

	// OpenAI-compatible provider (LLM_PROVIDER=openai). llama.cpp serves one
	// model per process, so generation and embeddings have separate URLs.
	// URLs must include the /v1 prefix.
	LLMBaseURL         string `env:"LLM_BASE_URL" envDefault:"http://localhost:8081/v1"`
	EmbeddingBaseURL   string `env:"EMBEDDING_BASE_URL" envDefault:"http://localhost:8082/v1"`
	LLMAPIKey          string `env:"LLM_API_KEY"`
	EmbeddingDimension int    `env:"EMBEDDING_DIMENSION"` // 0 = resolve from model table

	// Ollama (LLM_PROVIDER=ollama)
	OllamaURL            string `env:"OLLAMA_URL" envDefault:"http://localhost:11434"`
	OllamaEmbeddingModel string `env:"OLLAMA_EMBEDDING_MODEL" envDefault:"nomic-embed-text"`
	OllamaLLMModel       string `env:"OLLAMA_LLM_MODEL" envDefault:"llama3.2"`

	// RAG pipeline
	QueryRewriteEnabled   bool          `env:"QUERY_REWRITE_ENABLED" envDefault:"true"`
	QueryRewriteTimeout   time.Duration `env:"QUERY_REWRITE_TIMEOUT" envDefault:"10s"`
	ContextualConcurrency int           `env:"CONTEXTUAL_CONCURRENCY" envDefault:"2"`

	// Defaults for new tenants
	DefaultHybridEnabled       bool `env:"DEFAULT_HYBRID_ENABLED" envDefault:"true"`
	DefaultRerankerEnabled     bool `env:"DEFAULT_RERANKER_ENABLED" envDefault:"false"`
	DefaultContextualRetrieval bool `env:"DEFAULT_CONTEXTUAL_RETRIEVAL" envDefault:"false"`

	// Self-serve signup (e.g. the browser extension registering itself).
	// Disabled by default; when enabled, signup tenants expire after
	// SIGNUP_RETENTION_DAYS of inactivity and are reaped with their data.
	SignupEnabled          bool          `env:"SIGNUP_ENABLED" envDefault:"false"`
	SignupRetentionDays    int           `env:"SIGNUP_RETENTION_DAYS" envDefault:"30"`
	RetentionSweepInterval time.Duration `env:"RETENTION_SWEEP_INTERVAL" envDefault:"1h"`

	// Allow IngestURL to fetch private/loopback addresses (SSRF guard off).
	// Enable only for local development.
	URLFetchAllowPrivate bool `env:"URL_FETCH_ALLOW_PRIVATE" envDefault:"false"`

	// Auth
	AdminAPIKey        string   `env:"ADMIN_API_KEY"`
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:"," envDefault:"*"`

	// Rate Limiting
	RateLimitRPS   float64 `env:"RATE_LIMIT_RPS" envDefault:"10"`
	RateLimitBurst int     `env:"RATE_LIMIT_BURST" envDefault:"20"`

	// Default Tenant Config
	DefaultChunkMethod     string  `env:"DEFAULT_CHUNK_METHOD" envDefault:"semantic"`
	DefaultChunkTargetSize int     `env:"DEFAULT_CHUNK_TARGET_SIZE" envDefault:"512"`
	DefaultChunkMaxSize    int     `env:"DEFAULT_CHUNK_MAX_SIZE" envDefault:"1024"`
	DefaultChunkOverlap    int     `env:"DEFAULT_CHUNK_OVERLAP" envDefault:"50"`
	DefaultTopK            int     `env:"DEFAULT_TOP_K" envDefault:"4"`
	DefaultMinScore        float32 `env:"DEFAULT_MIN_SCORE" envDefault:"0.35"`
}

// IsDevelopment returns true if the environment is development
func (c *Config) IsDevelopment() bool {
	return c.Environment == "development"
}

// Validate checks the configuration. Cheap sanity checks run in every
// environment; insecure settings are hard errors outside development and
// loud warnings inside it.
func (c *Config) Validate() error {
	if c.RateLimitRPS < 0 || c.RateLimitBurst < 0 {
		return fmt.Errorf("rate limit values cannot be negative")
	}
	if p := c.LLMProvider; p != "" && p != "ollama" && p != "openai" {
		return fmt.Errorf("LLM_PROVIDER must be \"ollama\" or \"openai\", got %q", p)
	}

	wildcardCORS := false
	for _, origin := range c.CORSAllowedOrigins {
		if strings.TrimSpace(origin) == "*" {
			wildcardCORS = true
			break
		}
	}

	if c.IsDevelopment() {
		if c.AdminAPIKey == "" {
			slog.Warn("ADMIN_API_KEY is not set; tenant management endpoints will reject all requests")
		}
		return nil
	}

	if c.AdminAPIKey == "" {
		return fmt.Errorf("ADMIN_API_KEY must be set in %s environment", c.Environment)
	}
	if wildcardCORS {
		slog.Warn("CORS_ALLOWED_ORIGINS contains wildcard '*' in non-development environment")
	}

	return nil
}

// Load loads configuration from .env file (if present) and environment variables
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if not found)
	_ = godotenv.Load()

	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
