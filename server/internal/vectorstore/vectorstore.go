// Package vectorstore provides interfaces and implementations for vector similarity search.
package vectorstore

import (
	"context"
)

// SparseVector represents a sparse vector with indices and values
type SparseVector struct {
	Indices []uint32
	Values  []float32
}

// Chunk represents a document chunk with its embedding
type Chunk struct {
	ID           string
	DocumentID   string
	TenantID     string
	Content      string
	Vector       []float32     // Dense vector from embedding model
	SparseVector *SparseVector // Optional sparse vector for keyword search
	Metadata     map[string]string
}

// SearchResult represents a search result from the vector store
type SearchResult struct {
	ID         string
	DocumentID string
	Content    string
	Score      float32
	Metadata   map[string]string
}

// VectorStore defines the interface for vector storage operations
type VectorStore interface {
	// CreateCollection creates a new collection for a tenant (dense vectors only)
	CreateCollection(ctx context.Context, tenantID string, dimension int) error

	// CreateHybridCollection creates a collection with both dense and sparse vector support
	CreateHybridCollection(ctx context.Context, tenantID string, dimension int) error

	// DeleteCollection deletes a tenant's collection
	DeleteCollection(ctx context.Context, tenantID string) error

	// CollectionExists checks if a collection exists
	CollectionExists(ctx context.Context, tenantID string) (bool, error)

	// Upsert inserts or updates chunks in the vector store
	Upsert(ctx context.Context, tenantID string, chunks []Chunk) error

	// Search performs similarity search using dense vectors only
	Search(ctx context.Context, tenantID string, vector []float32, topK int, minScore float32) ([]SearchResult, error)

	// HybridSearch performs hybrid search combining dense and sparse vectors with RRF fusion
	HybridSearch(ctx context.Context, tenantID string, denseVector []float32, sparseVector *SparseVector, topK int, minScore float32) ([]SearchResult, error)

	// Delete removes chunks by document ID
	Delete(ctx context.Context, tenantID string, documentID string) error

	// DeleteByIDs removes specific chunks by their IDs
	DeleteByIDs(ctx context.Context, tenantID string, ids []string) error
}
