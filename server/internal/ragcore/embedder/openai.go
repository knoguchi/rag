package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIEmbedder implements Embedder against an OpenAI-compatible
// /v1/embeddings API. It works with llama.cpp's llama-server started with
// --embedding, vLLM, and similar servers, and uses true batch requests.
type OpenAIEmbedder struct {
	baseURL   string // e.g. "http://localhost:8082/v1"
	model     string
	apiKey    string
	dimension int
	client    *http.Client
}

// OpenAIEmbedderConfig configures an OpenAIEmbedder.
type OpenAIEmbedderConfig struct {
	// BaseURL is the API base URL including the /v1 prefix.
	BaseURL string
	// Model is the embedding model name (may be ignored by llama.cpp, which
	// serves whatever model it was started with).
	Model string
	// APIKey is an optional bearer token.
	APIKey string
	// Dimension overrides the embedding dimension; when 0 it is resolved
	// from the known-models table (falls back to 768).
	Dimension int
	// HTTPClient is an optional custom HTTP client.
	HTTPClient *http.Client
}

// NewOpenAIEmbedder creates an embedder for an OpenAI-compatible server.
func NewOpenAIEmbedder(cfg OpenAIEmbedderConfig) *OpenAIEmbedder {
	dimension := cfg.Dimension
	if dimension <= 0 {
		dimension = GetModelConfig(cfg.Model).Dimension
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &OpenAIEmbedder{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		model:     cfg.Model,
		apiKey:    cfg.APIKey,
		dimension: dimension,
		client:    client,
	}
}

type openaiEmbedRequest struct {
	Model string   `json:"model,omitempty"`
	Input []string `json:"input"`
}

type openaiEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed generates an embedding for one text.
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	out, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// EmbedBatch generates embeddings for multiple texts in one request.
func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	jsonBody, err := json.Marshal(openaiEmbedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embeddings API error (status %d): %s", resp.StatusCode, string(body))
	}

	var out openaiEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(out.Data))
	}

	// Order by index (servers usually return in order, but the field exists
	// for a reason)
	results := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(results) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		results[d.Index] = d.Embedding
	}
	for i, r := range results {
		if len(r) == 0 {
			return nil, fmt.Errorf("missing embedding at index %d", i)
		}
	}
	return results, nil
}

// Dimension returns the embedding dimensionality.
func (e *OpenAIEmbedder) Dimension() int { return e.dimension }

// ModelName returns the embedding model name.
func (e *OpenAIEmbedder) ModelName() string { return e.model }

// Ensure OpenAIEmbedder implements Embedder.
var _ Embedder = (*OpenAIEmbedder)(nil)
