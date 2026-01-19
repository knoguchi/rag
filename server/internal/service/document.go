package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/embedder"
	"github.com/knoguchi/rag/internal/ingestion"
	"github.com/knoguchi/rag/internal/repository"
	"github.com/knoguchi/rag/internal/vectorstore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DocumentService implements ragv1.DocumentServiceServer
type DocumentService struct {
	ragv1.UnimplementedDocumentServiceServer

	docRepo    repository.DocumentRepository
	tenantRepo repository.TenantRepository
	embedder   embedder.Embedder
	vectorDB   vectorstore.VectorStore
	httpClient *http.Client
}

// NewDocumentService creates a new DocumentService
func NewDocumentService(
	docRepo repository.DocumentRepository,
	tenantRepo repository.TenantRepository,
	embedder embedder.Embedder,
	vectorDB vectorstore.VectorStore,
) *DocumentService {
	return &DocumentService{
		docRepo:    docRepo,
		tenantRepo: tenantRepo,
		embedder:   embedder,
		vectorDB:   vectorDB,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// IngestDocument ingests raw text content
func (s *DocumentService) IngestDocument(ctx context.Context, req *ragv1.IngestDocumentRequest) (*ragv1.IngestDocumentResponse, error) {
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.Content == "" {
		return nil, status.Error(codes.InvalidArgument, "content is required")
	}

	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id format")
	}

	// Verify tenant exists and get config
	tenant, err := s.tenantRepo.GetByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "tenant not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get tenant: %v", err)
	}

	// Calculate content hash for deduplication
	// Include source URL in hash so different pages with similar content are not deduplicated
	contentHash := hashContent(req.Source + "\n" + req.Content)

	// Debug logging
	fmt.Printf("[IngestDocument] tenant=%s source=%s contentLen=%d hash=%s\n",
		tenantID.String(), req.Source, len(req.Content), contentHash[:16])

	// Check for duplicate document (same source + content = true duplicate)
	existingDoc, err := s.docRepo.GetByHash(ctx, tenantID, contentHash)
	if err == nil && existingDoc != nil {
		fmt.Printf("[IngestDocument] DUPLICATE FOUND: existing doc_id=%s\n", existingDoc.ID.String())
		return &ragv1.IngestDocumentResponse{
			DocumentId: existingDoc.ID.String(),
			Status:     convertStatus(existingDoc.Status),
		}, nil
	}

	// Create document record
	now := time.Now()
	docID := uuid.New()
	doc := &repository.Document{
		ID:          docID,
		TenantID:    tenantID,
		Source:      req.Source,
		Title:       req.Title,
		ContentHash: contentHash,
		Status:      "PROCESSING",
		Metadata:    req.Metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if doc.Title == "" {
		doc.Title = "Untitled Document"
	}
	if doc.Source == "" {
		doc.Source = "direct-upload"
	}

	if err := s.docRepo.Create(ctx, doc); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create document: %v", err)
	}

	// Process document asynchronously
	go s.processDocument(context.Background(), doc, req.Content, tenant)

	return &ragv1.IngestDocumentResponse{
		DocumentId: docID.String(),
		Status:     ragv1.DocumentStatus_DOCUMENT_STATUS_PROCESSING,
	}, nil
}

// IngestURL fetches and ingests content from a URL
func (s *DocumentService) IngestURL(ctx context.Context, req *ragv1.IngestURLRequest) (*ragv1.IngestDocumentResponse, error) {
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.Url == "" {
		return nil, status.Error(codes.InvalidArgument, "url is required")
	}

	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id format")
	}

	// Verify tenant exists and get config
	tenant, err := s.tenantRepo.GetByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "tenant not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get tenant: %v", err)
	}

	// Create document record first with PENDING status
	now := time.Now()
	docID := uuid.New()
	doc := &repository.Document{
		ID:        docID,
		TenantID:  tenantID,
		Source:    req.Url,
		Status:    "PENDING",
		Metadata:  req.Metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.docRepo.Create(ctx, doc); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create document: %v", err)
	}

	// Fetch and process URL asynchronously
	go s.processURL(context.Background(), doc, req.Url, req.UseHeadless, tenant)

	return &ragv1.IngestDocumentResponse{
		DocumentId: docID.String(),
		Status:     ragv1.DocumentStatus_DOCUMENT_STATUS_PENDING,
	}, nil
}

// GetDocument retrieves a document by ID
func (s *DocumentService) GetDocument(ctx context.Context, req *ragv1.GetDocumentRequest) (*ragv1.Document, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid document ID format")
	}

	doc, err := s.docRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get document: %v", err)
	}

	return s.documentToProto(doc), nil
}

// ListDocuments lists documents for a tenant
func (s *DocumentService) ListDocuments(ctx context.Context, req *ragv1.ListDocumentsRequest) (*ragv1.ListDocumentsResponse, error) {
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}

	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id format")
	}

	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if req.PageToken != "" {
		if _, err := fmt.Sscanf(req.PageToken, "%d", &offset); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page token")
		}
	}

	// Convert status filter
	statusFilter := ""
	if req.StatusFilter != ragv1.DocumentStatus_DOCUMENT_STATUS_UNSPECIFIED {
		statusFilter = statusToString(req.StatusFilter)
	}

	docs, total, err := s.docRepo.List(ctx, tenantID, statusFilter, pageSize, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list documents: %v", err)
	}

	protoDocs := make([]*ragv1.Document, len(docs))
	for i, doc := range docs {
		protoDocs[i] = s.documentToProto(doc)
	}

	var nextPageToken string
	if offset+len(docs) < total {
		nextPageToken = fmt.Sprintf("%d", offset+len(docs))
	}

	return &ragv1.ListDocumentsResponse{
		Documents:     protoDocs,
		NextPageToken: nextPageToken,
		TotalCount:    int32(total),
	}, nil
}

// DeleteDocument deletes a document and its chunks
func (s *DocumentService) DeleteDocument(ctx context.Context, req *ragv1.DeleteDocumentRequest) (*ragv1.DeleteDocumentResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid document ID format")
	}

	// Get document to find tenant ID
	doc, err := s.docRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get document: %v", err)
	}

	// Delete vectors from vector store
	if err := s.vectorDB.Delete(ctx, doc.TenantID.String(), doc.ID.String()); err != nil {
		// Log error but continue with deletion
		_ = err
	}

	// Delete chunks from repository
	if err := s.docRepo.DeleteChunks(ctx, id); err != nil {
		// Log error but continue with deletion
		_ = err
	}

	// Delete document
	if err := s.docRepo.Delete(ctx, id); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete document: %v", err)
	}

	return &ragv1.DeleteDocumentResponse{
		Success: true,
	}, nil
}

// GetDocumentChunks retrieves chunks for a document
func (s *DocumentService) GetDocumentChunks(ctx context.Context, req *ragv1.GetDocumentChunksRequest) (*ragv1.GetDocumentChunksResponse, error) {
	if req.DocumentId == "" {
		return nil, status.Error(codes.InvalidArgument, "document_id is required")
	}

	docID, err := uuid.Parse(req.DocumentId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid document_id format")
	}

	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if req.PageToken != "" {
		if _, err := fmt.Sscanf(req.PageToken, "%d", &offset); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page token")
		}
	}

	chunks, err := s.docRepo.GetChunks(ctx, docID, pageSize, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get chunks: %v", err)
	}

	protoChunks := make([]*ragv1.DocumentChunk, len(chunks))
	for i, chunk := range chunks {
		protoChunks[i] = s.chunkToProto(chunk)
	}

	var nextPageToken string
	if len(chunks) == pageSize {
		nextPageToken = fmt.Sprintf("%d", offset+len(chunks))
	}

	return &ragv1.GetDocumentChunksResponse{
		Chunks:        protoChunks,
		NextPageToken: nextPageToken,
	}, nil
}

// processDocument processes a document asynchronously
func (s *DocumentService) processDocument(ctx context.Context, doc *repository.Document, content string, tenant *repository.Tenant) {
	// Update status to PROCESSING
	doc.Status = "PROCESSING"
	doc.UpdatedAt = time.Now()
	_ = s.docRepo.Update(ctx, doc)

	// Create ingestion pipeline with tenant config
	pipeline := ingestion.NewPipeline(ingestion.PipelineConfig{
		Chunker: tenant.Config.Chunker,
		DefaultMetadata: map[string]string{
			"source": doc.Source,
			"title":  doc.Title,
		},
	})

	// Process content into chunks
	result, err := pipeline.ProcessWithMetadata(ctx, content, doc.Metadata)
	if err != nil {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("chunking failed: %v", err))
		return
	}

	// Convert chunks for storage
	docChunks := ingestion.ChunksToDocumentChunks(result.Chunks, doc.ID)

	// Store chunks in repository
	if err := s.docRepo.CreateChunks(ctx, docChunks); err != nil {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("failed to store chunks: %v", err))
		return
	}

	// Generate embeddings for all chunks
	chunkContents := make([]string, len(result.Chunks))
	for i, chunk := range result.Chunks {
		chunkContents[i] = chunk.Content
	}

	embeddings, err := s.embedder.EmbedBatch(ctx, chunkContents)
	if err != nil {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("embedding failed: %v", err))
		return
	}

	// Store vectors in vector store
	vectorChunks := make([]vectorstore.Chunk, len(docChunks))
	for i, chunk := range docChunks {
		metadata := make(map[string]string)
		for k, v := range chunk.Metadata {
			metadata[k] = v
		}
		metadata["document_id"] = doc.ID.String()
		metadata["title"] = doc.Title
		metadata["source"] = doc.Source

		vectorChunks[i] = vectorstore.Chunk{
			ID:         chunk.ID.String(),
			DocumentID: doc.ID.String(),
			TenantID:   doc.TenantID.String(),
			Content:    chunk.Content,
			Vector:     embeddings[i],
			Metadata:   metadata,
		}
	}

	if err := s.vectorDB.Upsert(ctx, doc.TenantID.String(), vectorChunks); err != nil {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("vector storage failed: %v", err))
		return
	}

	// Mark document as ready
	doc.Status = "READY"
	doc.ChunkCount = len(docChunks)
	doc.UpdatedAt = time.Now()
	_ = s.docRepo.Update(ctx, doc)

	// Update tenant usage
	_ = s.tenantRepo.UpdateUsage(ctx, doc.TenantID, repository.TenantUsage{
		DocumentCount: 1, // Increment
		ChunkCount:    len(docChunks),
	})
}

// processURL fetches a URL and processes its content
func (s *DocumentService) processURL(ctx context.Context, doc *repository.Document, url string, useHeadless bool, tenant *repository.Tenant) {
	// Update status to PROCESSING
	doc.Status = "PROCESSING"
	doc.UpdatedAt = time.Now()
	_ = s.docRepo.Update(ctx, doc)

	// Note: useHeadless is ignored - for JS-heavy sites, use the standalone Playwright crawler
	// and submit content via IngestDocument instead

	// Fetch URL content with simple HTTP GET
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("failed to create request: %v", err))
		return
	}
	req.Header.Set("User-Agent", "RAG-Service/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("failed to fetch URL: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status))
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("failed to read response: %v", err))
		return
	}

	content := string(body)

	// Extract title from HTML if present
	title := extractTitle(content)
	if title != "" {
		doc.Title = title
	} else {
		doc.Title = url
	}

	// Strip HTML tags for plain text content
	content = stripHTML(content)

	doc.ContentHash = hashContent(content)

	// Check for duplicate by hash
	existingDoc, err := s.docRepo.GetByHash(ctx, doc.TenantID, doc.ContentHash)
	if err == nil && existingDoc != nil && existingDoc.ID != doc.ID {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("duplicate content exists in document %s", existingDoc.ID.String()))
		return
	}

	// Process the fetched content
	s.processDocument(ctx, doc, content, tenant)
}

// extractTitle extracts the title from HTML content
func extractTitle(html string) string {
	re := regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// stripHTML removes HTML tags and returns plain text
func stripHTML(html string) string {
	// Remove script and style elements
	re := regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</\1>`)
	text := re.ReplaceAllString(html, "")

	// Remove all HTML tags
	re = regexp.MustCompile(`<[^>]+>`)
	text = re.ReplaceAllString(text, " ")

	// Clean up whitespace
	re = regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

// markDocumentFailed marks a document as failed with an error message
func (s *DocumentService) markDocumentFailed(ctx context.Context, doc *repository.Document, errorMsg string) {
	doc.Status = "FAILED"
	doc.ErrorMessage = errorMsg
	doc.UpdatedAt = time.Now()
	_ = s.docRepo.Update(ctx, doc)
}

// hashContent generates a SHA-256 hash of content
func hashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// documentToProto converts a repository Document to proto Document
func (s *DocumentService) documentToProto(doc *repository.Document) *ragv1.Document {
	return &ragv1.Document{
		Id:           doc.ID.String(),
		TenantId:     doc.TenantID.String(),
		Source:       doc.Source,
		Title:        doc.Title,
		ContentHash:  doc.ContentHash,
		ChunkCount:   int32(doc.ChunkCount),
		Status:       convertStatus(doc.Status),
		ErrorMessage: doc.ErrorMessage,
		Metadata:     doc.Metadata,
		CreatedAt:    timestamppb.New(doc.CreatedAt),
		UpdatedAt:    timestamppb.New(doc.UpdatedAt),
	}
}

// chunkToProto converts a repository DocumentChunk to proto DocumentChunk
func (s *DocumentService) chunkToProto(chunk *repository.DocumentChunk) *ragv1.DocumentChunk {
	return &ragv1.DocumentChunk{
		Id:         chunk.ID.String(),
		DocumentId: chunk.DocumentID.String(),
		ChunkIndex: int32(chunk.ChunkIndex),
		Content:    chunk.Content,
		Metadata:   chunk.Metadata,
		CreatedAt:  timestamppb.New(chunk.CreatedAt),
	}
}

// convertStatus converts a string status to proto DocumentStatus
func convertStatus(status string) ragv1.DocumentStatus {
	switch status {
	case "PENDING":
		return ragv1.DocumentStatus_DOCUMENT_STATUS_PENDING
	case "PROCESSING":
		return ragv1.DocumentStatus_DOCUMENT_STATUS_PROCESSING
	case "READY":
		return ragv1.DocumentStatus_DOCUMENT_STATUS_READY
	case "FAILED":
		return ragv1.DocumentStatus_DOCUMENT_STATUS_FAILED
	default:
		return ragv1.DocumentStatus_DOCUMENT_STATUS_UNSPECIFIED
	}
}

// statusToString converts a proto DocumentStatus to string
func statusToString(status ragv1.DocumentStatus) string {
	switch status {
	case ragv1.DocumentStatus_DOCUMENT_STATUS_PENDING:
		return "PENDING"
	case ragv1.DocumentStatus_DOCUMENT_STATUS_PROCESSING:
		return "PROCESSING"
	case ragv1.DocumentStatus_DOCUMENT_STATUS_READY:
		return "READY"
	case ragv1.DocumentStatus_DOCUMENT_STATUS_FAILED:
		return "FAILED"
	default:
		return ""
	}
}
