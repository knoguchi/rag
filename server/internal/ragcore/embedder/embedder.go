// Package embedder provides interfaces and implementations for text embedding.
package embedder

import "context"

// Embedder defines the interface for text embedding services.
type Embedder interface {
	// Embed generates an embedding vector for a single text input.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embedding vectors for multiple text inputs.
	// Returns a slice of embeddings in the same order as the input texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the dimensionality of the embedding vectors.
	Dimension() int

	// ModelName returns the name of the embedding model being used.
	ModelName() string
}

// ModelConfig holds configuration for a specific embedding model.
type ModelConfig struct {
	Dimension       int // Embedding dimension
	ContextLength   int // Max tokens the model can process
	MaxChunkWords   int // Recommended max chunk size in words (safe limit)
	TargetChunkWords int // Recommended target chunk size in words
}

// KnownModels maps embedding model names to their configurations.
// These limits are conservative to avoid "context length exceeded" errors.
var KnownModels = map[string]ModelConfig{
	"nomic-embed-text": {
		Dimension:        768,
		ContextLength:    8192,
		MaxChunkWords:    512,  // ~700 tokens, safe margin under 8192
		TargetChunkWords: 256,
	},
	"mxbai-embed-large": {
		Dimension:        1024,
		ContextLength:    512,
		MaxChunkWords:    300,  // Very limited context
		TargetChunkWords: 150,
	},
	"all-minilm": {
		Dimension:        384,
		ContextLength:    256,
		MaxChunkWords:    150,
		TargetChunkWords: 100,
	},
	"snowflake-arctic-embed": {
		Dimension:        1024,
		ContextLength:    8192,
		MaxChunkWords:    512,
		TargetChunkWords: 256,
	},
}

// GetModelConfig returns the configuration for a model, or defaults if unknown.
func GetModelConfig(modelName string) ModelConfig {
	if cfg, ok := KnownModels[modelName]; ok {
		return cfg
	}
	// Conservative defaults for unknown models
	return ModelConfig{
		Dimension:        768,
		ContextLength:    2048,
		MaxChunkWords:    256,
		TargetChunkWords: 128,
	}
}
