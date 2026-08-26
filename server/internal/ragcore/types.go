package ragcore

import "time"

// Options holds per-call parameters for querying. The caller (app layer) is
// responsible for resolving these from its own configuration.
type Options struct {
	TopK         int
	MinScore     float32
	SystemPrompt string
	Temperature  float32
	MaxTokens    int
	Model        string

	// HybridEnabled uses dense+sparse hybrid search (requires a
	// SparseVectorizer on the engine).
	HybridEnabled bool
	// RerankEnabled reranks retrieved chunks (requires a Reranker on the
	// engine).
	RerankEnabled bool
	// SessionID enables conversation memory when non-empty.
	SessionID string
}

// RetrievedChunk is a chunk returned from retrieval.
type RetrievedChunk struct {
	ID         string
	DocumentID string
	Content    string
	// Context is the LLM-generated situating context from contextual
	// ingestion, when the chunk was ingested with it.
	Context  string
	Score    float32
	Source   string
	Title    string
	Metadata map[string]string
}

// Timings captures pipeline latencies.
type Timings struct {
	Retrieval  time.Duration
	Generation time.Duration
	Total      time.Duration
}

// QueryResult is the result of a full RAG query.
type QueryResult struct {
	Answer  string
	Sources []RetrievedChunk
	Model   string
	Timings Timings
}

// StreamEvent is a single event emitted during a streaming query. Exactly one
// field is set per event.
type StreamEvent struct {
	Source *RetrievedChunk // a retrieved source, sent before generation
	Token  string          // a generated token (non-empty)
	Meta   *QueryResult    // final event: timings + model (Answer empty)
	Err    error           // generation error (terminal)
}

// RetrieveResult is the result of retrieval without generation.
type RetrieveResult struct {
	Chunks    []RetrievedChunk
	Retrieval time.Duration
}
