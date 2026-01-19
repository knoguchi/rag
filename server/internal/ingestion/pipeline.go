package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/knoguchi/rag/internal/repository"
)

// PipelineConfig holds configuration for the ingestion pipeline
type PipelineConfig struct {
	// Chunker configuration
	Chunker repository.ChunkerConfig

	// Additional metadata to include in all chunks
	DefaultMetadata map[string]string
}

// PipelineResult holds the result of processing content through the pipeline
type PipelineResult struct {
	// DocumentID is a unique identifier for this ingestion
	DocumentID uuid.UUID

	// ContentHash is the SHA-256 hash of the original content
	ContentHash string

	// Chunks contains all generated chunks
	Chunks []Chunk

	// Stats contains processing statistics
	Stats PipelineStats
}

// PipelineStats contains statistics about the pipeline execution
type PipelineStats struct {
	// OriginalLength is the character length of the original content
	OriginalLength int

	// OriginalWordCount is the word count of the original content
	OriginalWordCount int

	// ChunkCount is the number of chunks generated
	ChunkCount int

	// TotalChunkWords is the total word count across all chunks (may include overlap)
	TotalChunkWords int

	// AvgChunkWords is the average word count per chunk
	AvgChunkWords int

	// ProcessingTime is how long the chunking took
	ProcessingTime time.Duration
}

// Pipeline orchestrates the ingestion process
type Pipeline struct {
	config  PipelineConfig
	chunker *Chunker
}

// NewPipeline creates a new ingestion pipeline
func NewPipeline(config PipelineConfig) *Pipeline {
	return &Pipeline{
		config:  config,
		chunker: NewChunker(config.Chunker),
	}
}

// NewPipelineWithDefaults creates a pipeline with default configuration
func NewPipelineWithDefaults() *Pipeline {
	return NewPipeline(PipelineConfig{
		Chunker: repository.ChunkerConfig{
			Method:     "semantic",
			TargetSize: 512,
			MaxSize:    1024,
			Overlap:    50,
		},
	})
}

// Process takes content and processes it through the ingestion pipeline
func (p *Pipeline) Process(ctx context.Context, content string) (*PipelineResult, error) {
	startTime := time.Now()

	// Validate input
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}

	// Check for context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Generate document ID and content hash
	documentID := uuid.New()
	contentHash := hashContent(content)

	// Perform chunking
	chunks := p.chunker.Chunk(content)

	// Add default metadata to all chunks
	if p.config.DefaultMetadata != nil {
		for i := range chunks {
			if chunks[i].Metadata == nil {
				chunks[i].Metadata = make(map[string]string)
			}
			for k, v := range p.config.DefaultMetadata {
				// Don't override existing metadata
				if _, exists := chunks[i].Metadata[k]; !exists {
					chunks[i].Metadata[k] = v
				}
			}
		}
	}

	// Add document reference to all chunks
	for i := range chunks {
		if chunks[i].Metadata == nil {
			chunks[i].Metadata = make(map[string]string)
		}
		chunks[i].Metadata["document_id"] = documentID.String()
		chunks[i].Metadata["content_hash"] = contentHash
	}

	// Calculate statistics
	processingTime := time.Since(startTime)
	stats := p.calculateStats(content, chunks, processingTime)

	return &PipelineResult{
		DocumentID:  documentID,
		ContentHash: contentHash,
		Chunks:      chunks,
		Stats:       stats,
	}, nil
}

// ProcessWithMetadata processes content with additional metadata
func (p *Pipeline) ProcessWithMetadata(ctx context.Context, content string, metadata map[string]string) (*PipelineResult, error) {
	startTime := time.Now()

	// Validate input
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}

	// Check for context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Generate document ID and content hash
	documentID := uuid.New()
	contentHash := hashContent(content)

	// Perform chunking
	chunks := p.chunker.Chunk(content)

	// Merge metadata sources (priority: chunk metadata > provided metadata > default metadata)
	for i := range chunks {
		if chunks[i].Metadata == nil {
			chunks[i].Metadata = make(map[string]string)
		}

		// Add default metadata first (lowest priority)
		if p.config.DefaultMetadata != nil {
			for k, v := range p.config.DefaultMetadata {
				if _, exists := chunks[i].Metadata[k]; !exists {
					chunks[i].Metadata[k] = v
				}
			}
		}

		// Add provided metadata (middle priority)
		if metadata != nil {
			for k, v := range metadata {
				if _, exists := chunks[i].Metadata[k]; !exists {
					chunks[i].Metadata[k] = v
				}
			}
		}

		// Add document reference (always added)
		chunks[i].Metadata["document_id"] = documentID.String()
		chunks[i].Metadata["content_hash"] = contentHash
	}

	// Calculate statistics
	processingTime := time.Since(startTime)
	stats := p.calculateStats(content, chunks, processingTime)

	return &PipelineResult{
		DocumentID:  documentID,
		ContentHash: contentHash,
		Chunks:      chunks,
		Stats:       stats,
	}, nil
}

// ProcessBatch processes multiple content items
func (p *Pipeline) ProcessBatch(ctx context.Context, contents []string) ([]*PipelineResult, error) {
	results := make([]*PipelineResult, 0, len(contents))

	for _, content := range contents {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		result, err := p.Process(ctx, content)
		if err != nil {
			// Skip empty content but continue processing
			if strings.Contains(err.Error(), "cannot be empty") {
				continue
			}
			return results, fmt.Errorf("failed to process content: %w", err)
		}
		results = append(results, result)
	}

	return results, nil
}

// Rechunk allows reprocessing with a different chunking configuration
func (p *Pipeline) Rechunk(ctx context.Context, content string, chunkerConfig repository.ChunkerConfig) (*PipelineResult, error) {
	// Create a temporary chunker with the new config
	tempChunker := NewChunker(chunkerConfig)

	startTime := time.Now()

	// Validate input
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}

	// Check for context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Generate document ID and content hash
	documentID := uuid.New()
	contentHash := hashContent(content)

	// Perform chunking with temporary chunker
	chunks := tempChunker.Chunk(content)

	// Add document reference to all chunks
	for i := range chunks {
		if chunks[i].Metadata == nil {
			chunks[i].Metadata = make(map[string]string)
		}
		chunks[i].Metadata["document_id"] = documentID.String()
		chunks[i].Metadata["content_hash"] = contentHash
	}

	// Calculate statistics
	processingTime := time.Since(startTime)
	stats := p.calculateStats(content, chunks, processingTime)

	return &PipelineResult{
		DocumentID:  documentID,
		ContentHash: contentHash,
		Chunks:      chunks,
		Stats:       stats,
	}, nil
}

// GetConfig returns the current pipeline configuration
func (p *Pipeline) GetConfig() PipelineConfig {
	return p.config
}

// UpdateConfig updates the pipeline configuration
func (p *Pipeline) UpdateConfig(config PipelineConfig) {
	p.config = config
	p.chunker = NewChunker(config.Chunker)
}

// calculateStats computes statistics for the pipeline result
func (p *Pipeline) calculateStats(content string, chunks []Chunk, processingTime time.Duration) PipelineStats {
	originalWords := len(strings.Fields(content))

	totalChunkWords := 0
	for _, chunk := range chunks {
		totalChunkWords += len(strings.Fields(chunk.Content))
	}

	avgChunkWords := 0
	if len(chunks) > 0 {
		avgChunkWords = totalChunkWords / len(chunks)
	}

	return PipelineStats{
		OriginalLength:    len(content),
		OriginalWordCount: originalWords,
		ChunkCount:        len(chunks),
		TotalChunkWords:   totalChunkWords,
		AvgChunkWords:     avgChunkWords,
		ProcessingTime:    processingTime,
	}
}

// hashContent generates a SHA-256 hash of the content
func hashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// ChunkToDocumentChunk converts a Chunk to a DocumentChunk for storage
func ChunkToDocumentChunk(chunk Chunk, documentID uuid.UUID) *repository.DocumentChunk {
	return &repository.DocumentChunk{
		ID:         uuid.New(),
		DocumentID: documentID,
		ChunkIndex: chunk.Index,
		Content:    chunk.Content,
		Metadata:   chunk.Metadata,
		CreatedAt:  time.Now(),
	}
}

// ChunksToDocumentChunks converts multiple Chunks to DocumentChunks
func ChunksToDocumentChunks(chunks []Chunk, documentID uuid.UUID) []*repository.DocumentChunk {
	docChunks := make([]*repository.DocumentChunk, len(chunks))
	now := time.Now()

	for i, chunk := range chunks {
		docChunks[i] = &repository.DocumentChunk{
			ID:         uuid.New(),
			DocumentID: documentID,
			ChunkIndex: chunk.Index,
			Content:    chunk.Content,
			Metadata:   chunk.Metadata,
			CreatedAt:  now,
		}
	}

	return docChunks
}

// ValidateChunkerConfig validates a chunker configuration
func ValidateChunkerConfig(config repository.ChunkerConfig) error {
	validMethods := map[string]bool{
		"fixed":    true,
		"semantic": true,
		"sentence": true,
	}

	if config.Method != "" && !validMethods[config.Method] {
		return fmt.Errorf("invalid chunking method: %s (valid: fixed, semantic, sentence)", config.Method)
	}

	if config.TargetSize < 0 {
		return fmt.Errorf("target_size cannot be negative")
	}

	if config.MaxSize < 0 {
		return fmt.Errorf("max_size cannot be negative")
	}

	if config.TargetSize > 0 && config.MaxSize > 0 && config.TargetSize > config.MaxSize {
		return fmt.Errorf("target_size (%d) cannot be greater than max_size (%d)", config.TargetSize, config.MaxSize)
	}

	if config.Overlap < 0 {
		return fmt.Errorf("overlap cannot be negative")
	}

	if config.Overlap > 0 && config.TargetSize > 0 && config.Overlap >= config.TargetSize {
		return fmt.Errorf("overlap (%d) must be less than target_size (%d)", config.Overlap, config.TargetSize)
	}

	return nil
}

// DefaultChunkerConfig returns the default chunker configuration
func DefaultChunkerConfig() repository.ChunkerConfig {
	return repository.ChunkerConfig{
		Method:     "semantic",
		TargetSize: 512,
		MaxSize:    1024,
		Overlap:    50,
	}
}
