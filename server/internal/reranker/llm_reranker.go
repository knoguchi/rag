package reranker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/knoguchi/rag/internal/llm"
	"github.com/knoguchi/rag/internal/vectorstore"
)

// LLMReranker uses an LLM to re-score query-document pairs for improved relevance.
// This implements a cross-encoder-like approach where the model sees both
// query and document together, enabling more accurate relevance assessment.
type LLMReranker struct {
	llmClient llm.LLM
	model     string
}

// LLMRerankerOption is a functional option for configuring LLMReranker.
type LLMRerankerOption func(*LLMReranker)

// WithModel sets the model to use for reranking.
func WithModel(model string) LLMRerankerOption {
	return func(r *LLMReranker) {
		r.model = model
	}
}

// NewLLMReranker creates a new LLM-based reranker.
func NewLLMReranker(llmClient llm.LLM, opts ...LLMRerankerOption) *LLMReranker {
	r := &LLMReranker{
		llmClient: llmClient,
		model:     "llama3.2", // Default model
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// relevanceScore represents the structured output from the LLM.
type relevanceScore struct {
	DocIndex int     `json:"doc_index"`
	Score    float32 `json:"score"`
	Reason   string  `json:"reason,omitempty"`
}

type rerankResponse struct {
	Scores []relevanceScore `json:"scores"`
}

// Rerank uses the LLM to score each document's relevance to the query.
func (r *LLMReranker) Rerank(ctx context.Context, query string, results []vectorstore.SearchResult, topK int) ([]ScoredResult, error) {
	if len(results) == 0 {
		return nil, nil
	}

	// If we have fewer results than topK, just score what we have
	if len(results) <= topK {
		topK = len(results)
	}

	// Build the reranking prompt
	prompt := r.buildRerankPrompt(query, results)

	opts := llm.GenerateOptions{
		Model:       r.model,
		Temperature: 0.0, // Deterministic scoring
		MaxTokens:   1024,
	}

	response, err := r.llmClient.Generate(ctx, prompt, opts)
	if err != nil {
		return nil, fmt.Errorf("LLM reranking failed: %w", err)
	}

	// Parse the response
	scores, err := r.parseRerankResponse(response, len(results))
	if err != nil {
		// Fallback: return original results with their vector scores
		return r.fallbackScoring(results, topK), nil
	}

	// Build scored results
	scoredResults := make([]ScoredResult, len(results))
	for i, result := range results {
		scoredResults[i] = ScoredResult{
			SearchResult:  result,
			RerankerScore: scores[i],
		}
	}

	// Sort by reranker score (descending)
	sort.Slice(scoredResults, func(i, j int) bool {
		return scoredResults[i].RerankerScore > scoredResults[j].RerankerScore
	})

	// Return top K
	if len(scoredResults) > topK {
		scoredResults = scoredResults[:topK]
	}

	return scoredResults, nil
}

// buildRerankPrompt constructs the prompt for LLM-based reranking.
func (r *LLMReranker) buildRerankPrompt(query string, results []vectorstore.SearchResult) string {
	var sb strings.Builder

	sb.WriteString("You are a relevance scoring system. Score each document's relevance to the query.\n\n")
	sb.WriteString("Query: ")
	sb.WriteString(query)
	sb.WriteString("\n\n")

	sb.WriteString("Documents to score:\n")
	for i, result := range results {
		// Truncate content to avoid token limits
		content := result.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		sb.WriteString(fmt.Sprintf("[Doc %d]: %s\n\n", i, content))
	}

	sb.WriteString(`Score each document from 0.0 to 1.0 based on relevance to the query.
Output ONLY valid JSON in this exact format:
{"scores": [{"doc_index": 0, "score": 0.9}, {"doc_index": 1, "score": 0.3}, ...]}

Be strict: irrelevant documents should score below 0.3, somewhat relevant 0.3-0.7, highly relevant above 0.7.
Output only JSON, no explanation:`)

	return sb.String()
}

// parseRerankResponse extracts scores from the LLM response.
func (r *LLMReranker) parseRerankResponse(response string, numResults int) ([]float32, error) {
	// Try to find JSON in the response
	response = strings.TrimSpace(response)

	// Try to extract JSON from markdown code blocks if present
	if idx := strings.Index(response, "```json"); idx != -1 {
		start := idx + 7
		if end := strings.Index(response[start:], "```"); end != -1 {
			response = response[start : start+end]
		}
	} else if idx := strings.Index(response, "```"); idx != -1 {
		start := idx + 3
		if end := strings.Index(response[start:], "```"); end != -1 {
			response = response[start : start+end]
		}
	}

	response = strings.TrimSpace(response)

	var parsed rerankResponse
	if err := json.Unmarshal([]byte(response), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse rerank response: %w", err)
	}

	// Build score array indexed by doc_index
	scores := make([]float32, numResults)
	for i := range scores {
		scores[i] = 0.5 // Default score for missing entries
	}

	for _, s := range parsed.Scores {
		if s.DocIndex >= 0 && s.DocIndex < numResults {
			// Clamp score to valid range
			score := s.Score
			if score < 0 {
				score = 0
			}
			if score > 1 {
				score = 1
			}
			scores[s.DocIndex] = score
		}
	}

	return scores, nil
}

// fallbackScoring returns results with their original vector similarity scores.
func (r *LLMReranker) fallbackScoring(results []vectorstore.SearchResult, topK int) []ScoredResult {
	scoredResults := make([]ScoredResult, len(results))
	for i, result := range results {
		scoredResults[i] = ScoredResult{
			SearchResult:  result,
			RerankerScore: result.Score, // Use original vector score
		}
	}

	if len(scoredResults) > topK {
		scoredResults = scoredResults[:topK]
	}

	return scoredResults
}

// Ensure LLMReranker implements Reranker interface.
var _ Reranker = (*LLMReranker)(nil)
