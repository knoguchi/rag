package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

const (
	// DefaultOllamaBaseURL is the default Ollama API base URL.
	DefaultOllamaBaseURL = "http://localhost:11434"

	// DefaultOllamaModel is the default embedding model.
	DefaultOllamaModel = "nomic-embed-text"

	// DefaultOllamaDimension is the default embedding dimension for nomic-embed-text.
	DefaultOllamaDimension = 768

	// DefaultBatchConcurrency is the default number of concurrent embedding requests.
	DefaultBatchConcurrency = 4
)

// OllamaConfig holds configuration for the Ollama embedder.
type OllamaConfig struct {
	// BaseURL is the Ollama API base URL (default: http://localhost:11434).
	BaseURL string

	// Model is the embedding model to use (default: nomic-embed-text).
	Model string

	// Dimension is the embedding dimension (default: 768 for nomic-embed-text).
	Dimension int

	// BatchConcurrency is the number of concurrent requests for batch embedding.
	BatchConcurrency int

	// HTTPClient is an optional custom HTTP client.
	HTTPClient *http.Client
}

// OllamaEmbedder implements the Embedder interface using Ollama's API.
type OllamaEmbedder struct {
	baseURL          string
	model            string
	dimension        int
	batchConcurrency int
	client           *http.Client
}

// ollamaRequest represents the request body for Ollama embedding API.
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// ollamaResponse represents the response from Ollama embedding API.
type ollamaResponse struct {
	Embedding []float64 `json:"embedding"`
}

// NewOllamaEmbedder creates a new Ollama embedder with the given configuration.
func NewOllamaEmbedder(cfg OllamaConfig) *OllamaEmbedder {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}

	model := cfg.Model
	if model == "" {
		model = DefaultOllamaModel
	}

	dimension := cfg.Dimension
	if dimension <= 0 {
		dimension = DefaultOllamaDimension
	}

	batchConcurrency := cfg.BatchConcurrency
	if batchConcurrency <= 0 {
		batchConcurrency = DefaultBatchConcurrency
	}

	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	return &OllamaEmbedder{
		baseURL:          baseURL,
		model:            model,
		dimension:        dimension,
		batchConcurrency: batchConcurrency,
		client:           client,
	}
}

// Embed generates an embedding vector for a single text input.
func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	reqBody := ollamaRequest{
		Model:  e.model,
		Prompt: text,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/embeddings", e.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(ollamaResp.Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding returned from Ollama")
	}

	// Convert float64 to float32
	embedding := make([]float32, len(ollamaResp.Embedding))
	for i, v := range ollamaResp.Embedding {
		embedding[i] = float32(v)
	}

	return embedding, nil
}

// EmbedBatch generates embedding vectors for multiple text inputs.
// It processes requests concurrently for efficiency.
func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	results := make([][]float32, len(texts))
	errors := make([]error, len(texts))

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, e.batchConcurrency)

	for i, text := range texts {
		wg.Add(1)
		go func(idx int, t string) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				errors[idx] = ctx.Err()
				return
			}

			embedding, err := e.Embed(ctx, t)
			if err != nil {
				errors[idx] = fmt.Errorf("failed to embed text at index %d: %w", idx, err)
				return
			}
			results[idx] = embedding
		}(i, text)
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			return nil, fmt.Errorf("batch embedding failed at index %d: %w", i, err)
		}
	}

	return results, nil
}

// Dimension returns the dimensionality of the embedding vectors.
func (e *OllamaEmbedder) Dimension() int {
	return e.dimension
}

// ModelName returns the name of the embedding model being used.
func (e *OllamaEmbedder) ModelName() string {
	return e.model
}

// Ensure OllamaEmbedder implements Embedder interface.
var _ Embedder = (*OllamaEmbedder)(nil)
