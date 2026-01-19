// Package reranker provides re-ranking capabilities for RAG retrieval results.
//
// Re-ranking uses cross-encoder scoring to improve retrieval precision by
// evaluating query-document pairs together rather than independently.
//
// # Trade-offs
//
// Reranking is a per-tenant configuration option (TenantConfig.RerankerEnabled).
//
//   - Latency: Adds 1-3 seconds per query (extra LLM call to score each result)
//   - Quality: Significantly better relevance when top-k vector results have similar scores
//   - Cost: Roughly doubles LLM token usage per query
//
// Enable reranking for use cases where accuracy matters more than speed.
// Disable for high-throughput or latency-sensitive applications.
package reranker

import (
	"context"

	"github.com/knoguchi/rag/internal/vectorstore"
)

// ScoredResult represents a search result with an additional reranking score.
type ScoredResult struct {
	vectorstore.SearchResult
	RerankerScore float32
}

// Reranker defines the interface for re-ranking search results.
type Reranker interface {
	// Rerank takes a query and search results, and returns them re-ordered
	// by relevance with updated scores. The topK parameter limits the output.
	Rerank(ctx context.Context, query string, results []vectorstore.SearchResult, topK int) ([]ScoredResult, error)
}
