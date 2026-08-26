// Package ragcore implements the tenant-agnostic RAG engine: ingestion
// (chunk -> embed -> upsert) and querying (retrieve -> dedupe -> rerank ->
// prompt -> generate). Callers address data through an opaque namespace
// string; the engine has no knowledge of tenants, protos, or transport.
package ragcore

import (
	"context"

	"github.com/knoguchi/rag/internal/ragcore/embedder"
	"github.com/knoguchi/rag/internal/ragcore/llm"
	"github.com/knoguchi/rag/internal/ragcore/memory"
	"github.com/knoguchi/rag/internal/ragcore/reranker"
	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
)

// SparseVectorizer converts text to sparse vectors for hybrid search.
type SparseVectorizer interface {
	// Vectorize produces a document-side sparse vector.
	Vectorize(text string) *vectorstore.SparseVector
	// VectorizeQuery produces a query-side sparse vector.
	VectorizeQuery(text string) *vectorstore.SparseVector
}

// Engine orchestrates the RAG pipeline over pluggable providers.
type Engine struct {
	embedder embedder.Embedder
	llm      llm.LLM
	store    vectorstore.VectorStore
	reranker reranker.Reranker // optional
	sparse   SparseVectorizer  // optional; required for hybrid search
	memory   *memory.Store
}

// Option is a functional option for configuring the Engine.
type Option func(*Engine)

// WithReranker sets a reranker used when Options.RerankEnabled is true.
func WithReranker(r reranker.Reranker) Option {
	return func(e *Engine) { e.reranker = r }
}

// WithSparseVectorizer enables hybrid search support.
func WithSparseVectorizer(sv SparseVectorizer) Option {
	return func(e *Engine) { e.sparse = sv }
}

// WithMemory sets the conversation memory store.
func WithMemory(m *memory.Store) Option {
	return func(e *Engine) { e.memory = m }
}

// New creates an Engine.
func New(emb embedder.Embedder, llmClient llm.LLM, store vectorstore.VectorStore, opts ...Option) *Engine {
	e := &Engine{
		embedder: emb,
		llm:      llmClient,
		store:    store,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.memory == nil {
		e.memory = memory.DefaultStore()
	}
	return e
}

// Close releases engine resources (stops the memory cleanup goroutine).
func (e *Engine) Close() {
	if e.memory != nil {
		e.memory.Close()
	}
}

// CreateNamespace provisions vector storage for a namespace. The embedding
// dimension is taken from the configured embedder. When a sparse vectorizer
// is configured, the namespace is created with hybrid (dense + sparse)
// support.
func (e *Engine) CreateNamespace(ctx context.Context, namespace string) error {
	if e.sparse != nil {
		return e.store.CreateHybridCollection(ctx, namespace, e.embedder.Dimension())
	}
	return e.store.CreateCollection(ctx, namespace, e.embedder.Dimension())
}

// DeleteNamespace removes all vector data for a namespace.
func (e *Engine) DeleteNamespace(ctx context.Context, namespace string) error {
	return e.store.DeleteCollection(ctx, namespace)
}

// NamespaceExists reports whether vector storage exists for a namespace.
func (e *Engine) NamespaceExists(ctx context.Context, namespace string) (bool, error) {
	return e.store.CollectionExists(ctx, namespace)
}

// DeleteDocument removes all vectors belonging to a document in a namespace.
func (e *Engine) DeleteDocument(ctx context.Context, namespace, documentID string) error {
	return e.store.Delete(ctx, namespace, documentID)
}

// sessionKey namespaces conversation memory so sessions never collide across
// namespaces (tenants).
func sessionKey(namespace, sessionID string) string {
	return namespace + "\x00" + sessionID
}
