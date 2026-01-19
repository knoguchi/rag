package vectorstore

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/qdrant/go-client/qdrant"
)

const (
	// Vector field names for hybrid search
	denseVectorName  = "dense"
	sparseVectorName = "sparse"
)

// QdrantStore implements VectorStore using Qdrant
type QdrantStore struct {
	client *qdrant.Client
}

// NewQdrantStore creates a new Qdrant vector store client
// url should be in format "host:port" (e.g., "localhost:6334")
func NewQdrantStore(ctx context.Context, url string) (*QdrantStore, error) {
	host, portStr, err := net.SplitHostPort(url)
	if err != nil {
		// If no port specified, assume default
		host = url
		portStr = "6334"
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port in qdrant url: %w", err)
	}

	client, err := qdrant.NewClient(&qdrant.Config{
		Host: host,
		Port: port,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create qdrant client: %w", err)
	}

	return &QdrantStore{client: client}, nil
}

// Close closes the Qdrant client connection
func (s *QdrantStore) Close() error {
	return s.client.Close()
}

// collectionName returns the collection name for a tenant
func (s *QdrantStore) collectionName(tenantID string) string {
	return fmt.Sprintf("tenant_%s", tenantID)
}

// CreateCollection creates a new collection for a tenant (dense vectors only)
func (s *QdrantStore) CreateCollection(ctx context.Context, tenantID string, dimension int) error {
	name := s.collectionName(tenantID)

	err := s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(dimension),
			Distance: qdrant.Distance_Cosine,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}

	return nil
}

// CreateHybridCollection creates a collection with both dense and sparse vector support
func (s *QdrantStore) CreateHybridCollection(ctx context.Context, tenantID string, dimension int) error {
	name := s.collectionName(tenantID)

	err := s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{
			denseVectorName: {
				Size:     uint64(dimension),
				Distance: qdrant.Distance_Cosine,
			},
		}),
		SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
			sparseVectorName: {}, // Use default sparse vector config
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to create hybrid collection: %w", err)
	}

	return nil
}

// DeleteCollection deletes a tenant's collection
func (s *QdrantStore) DeleteCollection(ctx context.Context, tenantID string) error {
	name := s.collectionName(tenantID)

	err := s.client.DeleteCollection(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to delete collection: %w", err)
	}

	return nil
}

// CollectionExists checks if a collection exists
func (s *QdrantStore) CollectionExists(ctx context.Context, tenantID string) (bool, error) {
	name := s.collectionName(tenantID)

	exists, err := s.client.CollectionExists(ctx, name)
	if err != nil {
		return false, fmt.Errorf("failed to check collection existence: %w", err)
	}

	return exists, nil
}

// Upsert inserts or updates chunks in the vector store
// Supports both dense-only and hybrid (dense + sparse) collections
func (s *QdrantStore) Upsert(ctx context.Context, tenantID string, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	name := s.collectionName(tenantID)

	points := make([]*qdrant.PointStruct, len(chunks))
	for i, chunk := range chunks {
		payload := map[string]*qdrant.Value{
			"document_id": qdrant.NewValueString(chunk.DocumentID),
			"content":     qdrant.NewValueString(chunk.Content),
		}
		for k, v := range chunk.Metadata {
			payload[k] = qdrant.NewValueString(v)
		}

		point := &qdrant.PointStruct{
			Id:      qdrant.NewIDUUID(chunk.ID),
			Payload: payload,
		}

		// Check if we have sparse vectors (hybrid collection)
		if chunk.SparseVector != nil {
			// Named vectors for hybrid collection
			point.Vectors = &qdrant.Vectors{
				VectorsOptions: &qdrant.Vectors_Vectors{
					Vectors: &qdrant.NamedVectors{
						Vectors: map[string]*qdrant.Vector{
							denseVectorName: {
								Data: chunk.Vector,
							},
							sparseVectorName: {
								Indices: &qdrant.SparseIndices{Data: chunk.SparseVector.Indices},
								Data:    chunk.SparseVector.Values,
							},
						},
					},
				},
			}
		} else {
			// Single dense vector for non-hybrid collection
			point.Vectors = qdrant.NewVectors(chunk.Vector...)
		}

		points[i] = point
	}

	_, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: name,
		Points:         points,
	})
	if err != nil {
		return fmt.Errorf("failed to upsert points: %w", err)
	}

	return nil
}

// Search performs similarity search
func (s *QdrantStore) Search(ctx context.Context, tenantID string, vector []float32, topK int, minScore float32) ([]SearchResult, error) {
	name := s.collectionName(tenantID)

	response, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: name,
		Query:          qdrant.NewQuery(vector...),
		Limit:          qdrant.PtrOf(uint64(topK)),
		WithPayload:    qdrant.NewWithPayload(true),
		ScoreThreshold: qdrant.PtrOf(float32(minScore)),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	results := make([]SearchResult, 0, len(response))
	for _, point := range response {
		result := SearchResult{
			ID:       point.Id.GetUuid(),
			Score:    point.Score,
			Metadata: make(map[string]string),
		}

		if payload := point.Payload; payload != nil {
			if docID, ok := payload["document_id"]; ok {
				result.DocumentID = docID.GetStringValue()
			}
			if content, ok := payload["content"]; ok {
				result.Content = content.GetStringValue()
			}
			for k, v := range payload {
				if k != "document_id" && k != "content" {
					result.Metadata[k] = v.GetStringValue()
				}
			}
		}

		results = append(results, result)
	}

	return results, nil
}

// Delete removes chunks by document ID
func (s *QdrantStore) Delete(ctx context.Context, tenantID string, documentID string) error {
	name := s.collectionName(tenantID)

	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: name,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
				Filter: &qdrant.Filter{
					Must: []*qdrant.Condition{
						qdrant.NewMatch("document_id", documentID),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete by document ID: %w", err)
	}

	return nil
}

// DeleteByIDs removes specific chunks by their IDs
func (s *QdrantStore) DeleteByIDs(ctx context.Context, tenantID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	name := s.collectionName(tenantID)

	pointIDs := make([]*qdrant.PointId, len(ids))
	for i, id := range ids {
		pointIDs[i] = qdrant.NewIDUUID(id)
	}

	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: name,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Points{
				Points: &qdrant.PointsIdsList{
					Ids: pointIDs,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete by IDs: %w", err)
	}

	return nil
}

// HybridSearch performs hybrid search combining dense and sparse vectors with RRF fusion
func (s *QdrantStore) HybridSearch(ctx context.Context, tenantID string, denseVector []float32, sparseVector *SparseVector, topK int, minScore float32) ([]SearchResult, error) {
	name := s.collectionName(tenantID)

	// Build prefetch queries for both dense and sparse
	prefetchLimit := uint64(topK * 2) // Get more candidates for fusion

	prefetch := []*qdrant.PrefetchQuery{
		{
			Query: qdrant.NewQueryDense(denseVector),
			Using: qdrant.PtrOf(denseVectorName),
			Limit: qdrant.PtrOf(prefetchLimit),
		},
	}

	// Add sparse prefetch if sparse vector is provided
	if sparseVector != nil && len(sparseVector.Indices) > 0 {
		prefetch = append(prefetch, &qdrant.PrefetchQuery{
			Query: qdrant.NewQuerySparse(sparseVector.Indices, sparseVector.Values),
			Using: qdrant.PtrOf(sparseVectorName),
			Limit: qdrant.PtrOf(prefetchLimit),
		})
	}

	// Query with RRF fusion
	response, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: name,
		Prefetch:       prefetch,
		Query:          qdrant.NewQueryFusion(qdrant.Fusion_RRF),
		Limit:          qdrant.PtrOf(uint64(topK)),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to hybrid search: %w", err)
	}

	results := make([]SearchResult, 0, len(response))
	for _, point := range response {
		result := SearchResult{
			ID:       point.Id.GetUuid(),
			Score:    point.Score,
			Metadata: make(map[string]string),
		}

		if payload := point.Payload; payload != nil {
			if docID, ok := payload["document_id"]; ok {
				result.DocumentID = docID.GetStringValue()
			}
			if content, ok := payload["content"]; ok {
				result.Content = content.GetStringValue()
			}
			for k, v := range payload {
				if k != "document_id" && k != "content" {
					result.Metadata[k] = v.GetStringValue()
				}
			}
		}

		// Skip results below minScore threshold
		if result.Score >= minScore {
			results = append(results, result)
		}
	}

	return results, nil
}

// Ensure QdrantStore implements VectorStore
var _ VectorStore = (*QdrantStore)(nil)
