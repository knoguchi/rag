package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/knoguchi/rag/internal/repository"
)

// DocumentRepo implements repository.DocumentRepository
type DocumentRepo struct {
	db *DB
}

// NewDocumentRepo creates a new document repository
func NewDocumentRepo(db *DB) *DocumentRepo {
	return &DocumentRepo{db: db}
}

// Create creates a new document
func (r *DocumentRepo) Create(ctx context.Context, doc *repository.Document) error {
	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO documents (id, tenant_id, source, title, content_hash, chunk_count, status, error_message, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err = r.db.Pool.Exec(ctx, query,
		doc.ID, doc.TenantID, doc.Source, doc.Title, doc.ContentHash,
		doc.ChunkCount, doc.Status, doc.ErrorMessage, metadataJSON,
		doc.CreatedAt, doc.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create document: %w", err)
	}
	return nil
}

// GetByID retrieves a document by ID
func (r *DocumentRepo) GetByID(ctx context.Context, id uuid.UUID) (*repository.Document, error) {
	query := `
		SELECT id, tenant_id, source, title, content_hash, chunk_count, status, error_message, metadata, created_at, updated_at
		FROM documents
		WHERE id = $1
	`
	return r.scanDocument(ctx, query, id)
}

// GetByHash retrieves a document by content hash for a tenant
func (r *DocumentRepo) GetByHash(ctx context.Context, tenantID uuid.UUID, hash string) (*repository.Document, error) {
	query := `
		SELECT id, tenant_id, source, title, content_hash, chunk_count, status, error_message, metadata, created_at, updated_at
		FROM documents
		WHERE tenant_id = $1 AND content_hash = $2
	`
	return r.scanDocument(ctx, query, tenantID, hash)
}

func (r *DocumentRepo) scanDocument(ctx context.Context, query string, args ...any) (*repository.Document, error) {
	var doc repository.Document
	var metadataJSON []byte

	err := r.db.Pool.QueryRow(ctx, query, args...).Scan(
		&doc.ID, &doc.TenantID, &doc.Source, &doc.Title, &doc.ContentHash,
		&doc.ChunkCount, &doc.Status, &doc.ErrorMessage, &metadataJSON,
		&doc.CreatedAt, &doc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get document: %w", err)
	}

	doc.Metadata = make(map[string]string)
	if err := json.Unmarshal(metadataJSON, &doc.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &doc, nil
}

// List retrieves documents for a tenant with pagination
func (r *DocumentRepo) List(ctx context.Context, tenantID uuid.UUID, status string, limit, offset int) ([]*repository.Document, int, error) {
	// Build query with optional status filter
	countQuery := `SELECT COUNT(*) FROM documents WHERE tenant_id = $1`
	listQuery := `
		SELECT id, tenant_id, source, title, content_hash, chunk_count, status, error_message, metadata, created_at, updated_at
		FROM documents
		WHERE tenant_id = $1
	`
	args := []any{tenantID}

	if status != "" {
		countQuery += ` AND status = $2`
		listQuery += ` AND status = $2`
		args = append(args, status)
	}

	listQuery += ` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)

	// Get total count
	var total int
	err := r.db.Pool.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count documents: %w", err)
	}

	// Get documents
	args = append(args, limit, offset)
	rows, err := r.db.Pool.Query(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list documents: %w", err)
	}
	defer rows.Close()

	var docs []*repository.Document
	for rows.Next() {
		var doc repository.Document
		var metadataJSON []byte
		if err := rows.Scan(&doc.ID, &doc.TenantID, &doc.Source, &doc.Title, &doc.ContentHash,
			&doc.ChunkCount, &doc.Status, &doc.ErrorMessage, &metadataJSON,
			&doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("failed to scan document: %w", err)
		}
		doc.Metadata = make(map[string]string)
		if err := json.Unmarshal(metadataJSON, &doc.Metadata); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
		docs = append(docs, &doc)
	}

	return docs, total, nil
}

// Update updates a document
func (r *DocumentRepo) Update(ctx context.Context, doc *repository.Document) error {
	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		UPDATE documents
		SET source = $2, title = $3, content_hash = $4, chunk_count = $5,
		    status = $6, error_message = $7, metadata = $8, updated_at = NOW()
		WHERE id = $1
	`
	result, err := r.db.Pool.Exec(ctx, query,
		doc.ID, doc.Source, doc.Title, doc.ContentHash,
		doc.ChunkCount, doc.Status, doc.ErrorMessage, metadataJSON)
	if err != nil {
		return fmt.Errorf("failed to update document: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("document not found")
	}
	return nil
}

// Delete deletes a document
func (r *DocumentRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Pool.Exec(ctx, `DELETE FROM documents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("document not found")
	}
	return nil
}

// CreateChunks creates multiple document chunks
func (r *DocumentRepo) CreateChunks(ctx context.Context, chunks []*repository.DocumentChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, chunk := range chunks {
		metadataJSON, err := json.Marshal(chunk.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal chunk metadata: %w", err)
		}
		batch.Queue(`
			INSERT INTO document_chunks (id, document_id, chunk_index, content, metadata, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, chunk.ID, chunk.DocumentID, chunk.ChunkIndex, chunk.Content, metadataJSON, chunk.CreatedAt)
	}

	results := r.db.Pool.SendBatch(ctx, batch)
	defer results.Close()

	for range chunks {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("failed to create chunk: %w", err)
		}
	}

	return nil
}

// GetChunks retrieves chunks for a document
func (r *DocumentRepo) GetChunks(ctx context.Context, documentID uuid.UUID, limit, offset int) ([]*repository.DocumentChunk, error) {
	query := `
		SELECT id, document_id, chunk_index, content, metadata, created_at
		FROM document_chunks
		WHERE document_id = $1
		ORDER BY chunk_index
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.Pool.Query(ctx, query, documentID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get chunks: %w", err)
	}
	defer rows.Close()

	var chunks []*repository.DocumentChunk
	for rows.Next() {
		var chunk repository.DocumentChunk
		var metadataJSON []byte
		if err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.ChunkIndex, &chunk.Content,
			&metadataJSON, &chunk.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan chunk: %w", err)
		}
		chunk.Metadata = make(map[string]string)
		if err := json.Unmarshal(metadataJSON, &chunk.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
		chunks = append(chunks, &chunk)
	}

	return chunks, nil
}

// DeleteChunks deletes all chunks for a document
func (r *DocumentRepo) DeleteChunks(ctx context.Context, documentID uuid.UUID) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM document_chunks WHERE document_id = $1`, documentID)
	if err != nil {
		return fmt.Errorf("failed to delete chunks: %w", err)
	}
	return nil
}

// Ensure DocumentRepo implements the interface
var _ repository.DocumentRepository = (*DocumentRepo)(nil)
