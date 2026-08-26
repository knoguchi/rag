package ragcore

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/knoguchi/rag/internal/ragcore/chunk"
	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
)

// IngestInput describes a document to ingest into a namespace.
type IngestInput struct {
	Namespace  string
	DocumentID string
	Content    string
	// Metadata is document-level metadata merged into every chunk.
	Metadata map[string]string
	// ChunkDefaults is low-priority metadata (e.g. source, title) applied to
	// every chunk and to the vector payload.
	ChunkDefaults map[string]string
	Chunker       chunk.Config
	// Hybrid stores sparse vectors alongside dense ones. Only valid for
	// namespaces created with hybrid support; must be false for legacy
	// dense-only namespaces.
	Hybrid bool
	// Contextual generates a short situating context per chunk (one LLM
	// call each) that is embedded with the chunk. Slow on local LLMs.
	Contextual bool
	// Model is the LLM used for contextual ingestion.
	Model string
}

// IngestedChunk is a chunk produced by ingestion, returned so the caller can
// persist it in its own store.
type IngestedChunk struct {
	ID       string
	Index    int
	Content  string
	Metadata map[string]string
}

// Ingest chunks the content, invokes persist (if non-nil) so the caller can
// durably record the chunks, then embeds and upserts them into the vector
// store. If persist returns an error, ingestion stops before any vectors are
// written.
func (e *Engine) Ingest(ctx context.Context, in IngestInput, persist func([]IngestedChunk) error) ([]IngestedChunk, error) {
	pipeline := chunk.NewPipeline(chunk.PipelineConfig{
		Chunker:         in.Chunker,
		DefaultMetadata: in.ChunkDefaults,
	})

	result, err := pipeline.ProcessWithMetadata(ctx, in.Content, in.Metadata)
	if err != nil {
		return nil, fmt.Errorf("chunking failed: %w", err)
	}

	chunks := make([]IngestedChunk, len(result.Chunks))
	for i, c := range result.Chunks {
		// The pipeline stamps a throwaway document_id; replace it with the
		// caller's real document ID.
		if c.Metadata == nil {
			c.Metadata = make(map[string]string)
		}
		c.Metadata["document_id"] = in.DocumentID
		chunks[i] = IngestedChunk{
			ID:       uuid.New().String(),
			Index:    c.Index,
			Content:  c.Content,
			Metadata: c.Metadata,
		}
	}

	if in.Contextual {
		// Runs before persist so the situating context lands in the
		// caller's store too (enables reindexing without re-running it)
		e.contextualize(ctx, in.Content, in.Model, chunks)
	}

	if persist != nil {
		if err := persist(chunks); err != nil {
			return nil, err
		}
	}

	if err := e.IndexChunks(ctx, in.Namespace, in.DocumentID, chunks, in.ChunkDefaults, in.Hybrid); err != nil {
		return nil, err
	}

	return chunks, nil
}

// IndexChunks embeds pre-chunked content and upserts it into the vector
// store. Used by Ingest and by reindexing tools that already have chunk
// content persisted elsewhere.
func (e *Engine) IndexChunks(ctx context.Context, namespace, documentID string, chunks []IngestedChunk, defaults map[string]string, hybrid bool) error {
	if len(chunks) == 0 {
		return nil
	}

	// Embed the situating context (when present) together with the content
	contents := make([]string, len(chunks))
	for i, c := range chunks {
		contents[i] = embedText(c)
	}
	embeddings, err := e.embedder.EmbedBatch(ctx, contents)
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}

	vectorChunks := make([]vectorstore.Chunk, len(chunks))
	for i, c := range chunks {
		metadata := make(map[string]string, len(c.Metadata)+len(defaults)+1)
		for k, v := range c.Metadata {
			metadata[k] = v
		}
		for k, v := range defaults {
			metadata[k] = v
		}
		metadata["document_id"] = documentID

		var sparse *vectorstore.SparseVector
		if hybrid && e.sparse != nil {
			sparse = e.sparse.Vectorize(embedText(c))
		}

		vectorChunks[i] = vectorstore.Chunk{
			ID:           c.ID,
			DocumentID:   documentID,
			Content:      c.Content,
			Vector:       embeddings[i],
			SparseVector: sparse,
			Metadata:     metadata,
		}
	}

	if err := e.store.Upsert(ctx, namespace, vectorChunks); err != nil {
		return fmt.Errorf("vector storage failed: %w", err)
	}

	return nil
}
