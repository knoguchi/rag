// Package config loads configuration from environment variables and .env files.
package config

import (
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

	// Ollama
	OllamaURL            string `env:"OLLAMA_URL" envDefault:"http://localhost:11434"`
	OllamaEmbeddingModel string `env:"OLLAMA_EMBEDDING_MODEL" envDefault:"nomic-embed-text"`
	OllamaLLMModel       string `env:"OLLAMA_LLM_MODEL" envDefault:"llama3.2"`

	// Auth
	JWTSecret     string        `env:"JWT_SECRET" envDefault:"change-this-in-production"`
	JWTExpiry     time.Duration `env:"JWT_EXPIRY" envDefault:"24h"`
	SessionSecret string        `env:"SESSION_SECRET" envDefault:"change-this-in-production"`

	// Default Tenant Config
	DefaultChunkMethod     string  `env:"DEFAULT_CHUNK_METHOD" envDefault:"semantic"`
	DefaultChunkTargetSize int     `env:"DEFAULT_CHUNK_TARGET_SIZE" envDefault:"512"`
	DefaultChunkMaxSize    int     `env:"DEFAULT_CHUNK_MAX_SIZE" envDefault:"1024"`
	DefaultChunkOverlap    int     `env:"DEFAULT_CHUNK_OVERLAP" envDefault:"50"`
	DefaultTopK            int     `env:"DEFAULT_TOP_K" envDefault:"4"`
	DefaultMinScore        float32 `env:"DEFAULT_MIN_SCORE" envDefault:"0.35"`
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
